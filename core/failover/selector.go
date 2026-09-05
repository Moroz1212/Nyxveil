package failover

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/nyxveil/nvp/core/controlplane/model"
	"github.com/nyxveil/nvp/core/nvperr"
	"github.com/nyxveil/nvp/core/transport"
)

// ExhaustedError is returned when all node/transport attempts fail.
// TriedNodes lists node IDs only (no tickets, keys, or other secrets).
type ExhaustedError struct {
	TriedNodes []string
	Cause      error // typically nvperr.ErrNoHealthyNodes or nvperr.ErrTransportUnavailable
}

func (e *ExhaustedError) Error() string {
	if e == nil {
		return "failover exhausted"
	}
	nodes := strings.Join(e.TriedNodes, ",")
	if nodes == "" {
		nodes = "(none)"
	}
	if e.Cause != nil {
		return fmt.Sprintf("failover exhausted tried_nodes=%s: %v", nodes, e.Cause)
	}
	return fmt.Sprintf("failover exhausted tried_nodes=%s", nodes)
}

func (e *ExhaustedError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func exhausted(cause error, tried []string) error {
	ids := append([]string(nil), tried...)
	return &ExhaustedError{TriedNodes: ids, Cause: cause}
}

// HealthScore weights for node selection.
type HealthScore struct {
	NodeID    string
	Score     float64
	LatencyMs float64
}

// Selector chooses nodes for connection with failover support.
type Selector struct {
	Catalog    model.Catalog
	Role       string // "master", "user", etc.
	LocationID string
}

// CandidateNodes returns healthy nodes for location filtered by role.
func (s *Selector) CandidateNodes() []model.NodeRegistryEntry {
	var candidates []model.NodeRegistryEntry
	for _, n := range s.Catalog.Nodes {
		if !n.Enabled || n.Draining || n.Status == "offline" {
			continue
		}
		if n.TestOnly && s.Role != "master" {
			continue
		}
		if s.LocationID != "" && n.LocationID != s.LocationID {
			continue
		}
		if !n.LastSeen.IsZero() && !n.Health.Healthy {
			continue
		}
		candidates = append(candidates, n)
	}
	sort.Slice(candidates, func(i, j int) bool {
		loadI := float64(candidates[i].CurrentSessions) / float64(max(candidates[i].Capacity, 1))
		loadJ := float64(candidates[j].CurrentSessions) / float64(max(candidates[j].Capacity, 1))
		return loadI < loadJ
	})
	return candidates
}

// SelectNode returns the best candidate without dialing.
func (s *Selector) SelectNode() (model.NodeRegistryEntry, error) {
	nodes := s.CandidateNodes()
	if len(nodes) == 0 {
		return model.NodeRegistryEntry{}, nvperr.ErrNoHealthyNodes
	}
	return nodes[0], nil
}

// ScoreNode computes health score for a node.
func ScoreNode(n model.NodeRegistryEntry, recentFailures int, latencyMs float64) HealthScore {
	capacityFactor := 1.0 - float64(n.CurrentSessions)/float64(max(n.Capacity, 1))
	latencyFactor := 1.0 / (1.0 + latencyMs/100.0)
	failurePenalty := float64(recentFailures) * 0.2
	score := capacityFactor*0.4 + latencyFactor*0.4 + 0.2 - failurePenalty
	if score < 0 {
		score = 0
	}
	return HealthScore{NodeID: n.NodeID, Score: score, LatencyMs: latencyMs}
}

// ConnectPolicy defines failover behavior within a single DesiredLocationID.
// Cross-location failover is NOT performed by Core — the application must call
// OpenSession with a new DesiredLocationID.
type ConnectPolicy struct {
	MaxNodeAttempts int
	TransportRacing transport.RacingConfig
	RetryDelay      time.Duration
	// AllowedNodeIDs, when non-empty, restricts dial attempts to this NodeScope
	// (intersection with location candidates). Empty means location-scoped.
	AllowedNodeIDs []string
}

// DefaultConnectPolicy returns commercial defaults.
func DefaultConnectPolicy() ConnectPolicy {
	return ConnectPolicy{
		MaxNodeAttempts: 3,
		TransportRacing: transport.DefaultRacingConfig(),
		RetryDelay:      500 * time.Millisecond,
	}
}

// DialConfigProvider optionally supplies TLS dial parameters for a node.
type DialConfigProvider interface {
	RootCAs() interface{}
	ServerNameFor(node model.NodeRegistryEntry) string
	PinnedPubKeyFor(node model.NodeRegistryEntry) []byte
	ECHPolicy() transport.ECHPolicy
	ECHConfigList() []byte
}

// ConnectWithFailover dials candidates for sel.LocationID only (same-location
// node + transport failover). Cross-location automatic failover is not supported
// in NVP/1 — application must OpenSession with a new DesiredLocationID.
func ConnectWithFailover(ctx context.Context, sel *Selector, registry *transport.Registry, policy ConnectPolicy, provider DialConfigProvider) (transport.Conn, model.NodeRegistryEntry, error) {
	if sel.LocationID == "" && len(sel.CandidateNodes()) == 0 {
		return nil, model.NodeRegistryEntry{}, exhausted(nvperr.ErrNoHealthyNodes, nil)
	}
	conn, node, err := connectNodes(ctx, sel, registry, policy, provider)
	if err == nil {
		return conn, node, nil
	}
	var ex *ExhaustedError
	if errors.As(err, &ex) {
		return nil, model.NodeRegistryEntry{}, err
	}
	var cause error
	if errors.Is(err, nvperr.ErrNoHealthyNodes) {
		cause = nvperr.ErrNoHealthyNodes
	} else if errors.Is(err, nvperr.ErrTransportUnavailable) {
		cause = nvperr.ErrTransportUnavailable
	} else {
		cause = fmt.Errorf("%w: %v", nvperr.ErrTransportUnavailable, err)
	}
	return nil, model.NodeRegistryEntry{}, exhausted(cause, nil)
}

func appendUnique(dst []string, ids ...string) []string {
	seen := make(map[string]struct{}, len(dst)+len(ids))
	for _, id := range dst {
		seen[id] = struct{}{}
	}
	for _, id := range ids {
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		dst = append(dst, id)
	}
	return dst
}

func connectNodes(ctx context.Context, sel *Selector, registry *transport.Registry, policy ConnectPolicy, provider DialConfigProvider) (transport.Conn, model.NodeRegistryEntry, error) {
	nodes := filterNodesByScope(sel.CandidateNodes(), policy.AllowedNodeIDs)
	if len(nodes) == 0 {
		return nil, model.NodeRegistryEntry{}, exhausted(nvperr.ErrNoHealthyNodes, nil)
	}

	attempts := policy.MaxNodeAttempts
	if attempts <= 0 {
		attempts = 3
	}
	if attempts > len(nodes) {
		attempts = len(nodes)
	}

	var lastErr error
	var tried []string
	for i := 0; i < attempts; i++ {
		node := nodes[i]
		tried = appendUnique(tried, node.NodeID)
		for _, ep := range node.Endpoints {
			cfg := transport.DialConfig{
				Endpoint:   ep,
				ServerName: ep.Host,
				Timeout:    10 * time.Second,
			}
			if node.ServerName != "" {
				cfg.ServerName = node.ServerName
			}
			if len(node.SPKIPin) > 0 {
				cfg.PinnedPubKey = append([]byte(nil), node.SPKIPin...)
			}
			if provider != nil {
				cfg.RootCAs = provider.RootCAs()
				if sn := provider.ServerNameFor(node); sn != "" {
					cfg.ServerName = sn
				}
				if pin := provider.PinnedPubKeyFor(node); len(pin) > 0 {
					cfg.PinnedPubKey = append([]byte(nil), pin...)
				}
				cfg.ECHPolicy = provider.ECHPolicy()
				if list := provider.ECHConfigList(); len(list) > 0 {
					cfg.ECHConfigList = append([]byte(nil), list...)
				}
			}
			conn, err := registry.DialWithRacing(ctx, cfg, policy.TransportRacing)
			if err == nil {
				return conn, node, nil
			}
			lastErr = err
		}
		select {
		case <-ctx.Done():
			return nil, model.NodeRegistryEntry{}, ctx.Err()
		case <-time.After(policy.RetryDelay):
		}
	}
	cause := nvperr.ErrTransportUnavailable
	if lastErr != nil {
		cause = fmt.Errorf("%w: %v", nvperr.ErrTransportUnavailable, lastErr)
	}
	return nil, model.NodeRegistryEntry{}, exhausted(cause, tried)
}

func filterNodesByScope(nodes []model.NodeRegistryEntry, allowedIDs []string) []model.NodeRegistryEntry {
	if len(allowedIDs) == 0 {
		return nodes
	}
	set := make(map[string]struct{}, len(allowedIDs))
	for _, id := range allowedIDs {
		set[id] = struct{}{}
	}
	out := make([]model.NodeRegistryEntry, 0, len(nodes))
	for _, n := range nodes {
		if _, ok := set[n.NodeID]; ok {
			out = append(out, n)
		}
	}
	return out
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
