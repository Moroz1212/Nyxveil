package failover

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/nyxveil/nvp/controlplane/model"
	"github.com/nyxveil/nvp/transport"
)

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
		if n.Health.Healthy {
			candidates = append(candidates, n)
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		loadI := float64(candidates[i].CurrentSessions) / float64(max(candidates[i].Capacity, 1))
		loadJ := float64(candidates[j].CurrentSessions) / float64(max(candidates[j].Capacity, 1))
		return loadI < loadJ
	})
	return candidates
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

// ConnectPolicy defines failover behavior.
type ConnectPolicy struct {
	MaxNodeAttempts   int
	TransportRacing   transport.RacingConfig
	RetryDelay        time.Duration
	FallbackLocations []string
}

// DefaultConnectPolicy returns commercial defaults.
func DefaultConnectPolicy() ConnectPolicy {
	return ConnectPolicy{
		MaxNodeAttempts: 3,
		TransportRacing: transport.DefaultRacingConfig(),
		RetryDelay:      500 * time.Millisecond,
	}
}

// DialConfigProvider optionally supplies TLS root CAs for node dial.
type DialConfigProvider interface {
	RootCAs() interface{}
	ServerNameFor(node model.NodeRegistryEntry) string
}

// ConnectWithFailover attempts connection with transport and node failover.
func ConnectWithFailover(ctx context.Context, sel *Selector, registry *transport.Registry, policy ConnectPolicy, provider DialConfigProvider) (transport.Conn, model.NodeRegistryEntry, error) {
	nodes := sel.CandidateNodes()
	if len(nodes) == 0 {
		return nil, model.NodeRegistryEntry{}, fmt.Errorf("no healthy nodes available")
	}

	attempts := policy.MaxNodeAttempts
	if attempts <= 0 {
		attempts = 3
	}
	if attempts > len(nodes) {
		attempts = len(nodes)
	}

	var lastErr error
	for i := 0; i < attempts; i++ {
		node := nodes[i]
		for _, ep := range node.Endpoints {
			cfg := transport.DialConfig{
				Endpoint:   ep,
				ServerName: ep.Host,
				Timeout:    10 * time.Second,
			}
			if provider != nil {
				cfg.RootCAs = provider.RootCAs()
				if sn := provider.ServerNameFor(node); sn != "" {
					cfg.ServerName = sn
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
	return nil, model.NodeRegistryEntry{}, fmt.Errorf("failover exhausted: %w", lastErr)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
