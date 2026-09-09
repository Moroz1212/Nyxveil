// Package nvp exposes Frozen Core to Swift through gomobile; no wire implementation here.
package nvp

import (
	"context"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nyxveil/client-ios/bridge/mtu"
	"github.com/nyxveil/client-ios/bridge/netcfg"
	qt "github.com/nyxveil/client-ios/bridge/quictransport"
	"github.com/nyxveil/nvp/core/auth/ticket"
	"github.com/nyxveil/nvp/core/control"
	"github.com/nyxveil/nvp/core/controlplane/catalog"
	"github.com/nyxveil/nvp/core/controlplane/model"
	"github.com/nyxveil/nvp/core/failover"
	"github.com/nyxveil/nvp/core/session"
	"github.com/nyxveil/nvp/core/transport"
	"github.com/quic-go/quic-go"
)

func init() { debug.SetMemoryLimit(24 << 20) }

// Sink must copy packet bytes before returning. Errors are fixed codes, never peer text.
type Sink interface {
	Receive(packet []byte)
	Failed(code string)
}

type input struct {
	LocationID string            `json:"location_id"`
	Ticket     string            `json:"access_ticket"`
	Seed       []byte            `json:"device_seed"`
	Catalog    []byte            `json:"catalog"`
	Keys       map[string]string `json:"keys"`
}

func verified(raw []byte, encoded map[string]string) (*model.SignedCatalog, error) {
	keys := catalog.VerifyKeys{Keys: map[string]ed25519.PublicKey{}}
	for id, v := range encoded {
		key, err := base64.StdEncoding.DecodeString(v)
		if err != nil || len(key) != ed25519.PublicKeySize {
			return nil, errors.New("catalog_key_invalid")
		}
		keys.Keys[id] = key
	}
	signed, err := catalog.Parse(raw)
	if err != nil {
		return nil, errors.New("catalog_invalid")
	}
	if err = catalog.Verify(keys, signed); err != nil {
		return nil, errors.New("catalog_signature_or_time_invalid")
	}
	return &signed, nil
}

// VerifyCatalog uses the exact Go canonical JSON and Ed25519 verifier.
func VerifyCatalog(raw, keysJSON []byte) error {
	var keys map[string]string
	if json.Unmarshal(keysJSON, &keys) != nil {
		return errors.New("catalog_keys_invalid")
	}
	_, err := verified(raw, keys)
	return err
}

// Engine is single-use. Create a new instance for each reconnect attempt.
type Engine struct {
	roots   *x509.CertPool // nil = system trust; injected only by same-package loopback tests.
	mu      sync.Mutex
	ctx     context.Context
	cancel  context.CancelFunc
	started bool
	sess    *session.Session
	conn    transport.Conn
	sink    Sink
	vpn     netip.Addr
	mtu     int
	failed  atomic.Bool
	rx      atomic.Uint64
	tx      atomic.Uint64
}

func NewEngine(sink Sink) *Engine {
	ctx, cancel := context.WithCancel(context.Background())
	return &Engine{ctx: ctx, cancel: cancel, sink: sink}
}

// Begin returns only after AUTH_OK and a valid TypeConfig; maximum total 45 seconds.
func (e *Engine) Begin(configJSON string) (string, error) {
	e.mu.Lock()
	if e.started {
		e.mu.Unlock()
		return "", errors.New("engine_already_used")
	}
	e.started = true
	e.mu.Unlock()
	ctx, cancel := context.WithTimeout(e.ctx, 45*time.Second)
	defer cancel()
	var in input
	if json.Unmarshal([]byte(configJSON), &in) != nil || len(in.Seed) != 32 || in.LocationID == "" {
		return "", errors.New("connect_parameters_invalid")
	}
	defer clear(in.Seed)
	signed, err := verified(in.Catalog, in.Keys)
	if err != nil {
		return "", err
	}
	claims, err := ticket.PeekClaims(in.Ticket)
	if err != nil || claims.ExpiresAt == nil || !time.Now().Before(claims.ExpiresAt.Time) {
		return "", errors.New("ticket_invalid_or_expired")
	}
	// Claims are only a narrowing filter; Node verifies signature, scope and PoP.
	selector := &failover.Selector{Catalog: signed.Catalog, LocationID: in.LocationID, Role: claims.Role}
	nodes := selector.CandidateNodes()
	var last error = errors.New("no_eligible_node")
	for _, node := range nodes {
		if len(claims.NodeScope) > 0 && !contains(claims.NodeScope, node.NodeID) {
			continue
		}
		if node.Capacity > 0 && node.CurrentSessions >= node.Capacity {
			continue
		}
		if len(node.SPKIPin) != 32 {
			continue
		}
		for _, ep := range node.Endpoints {
			if !hasQUIC(ep.Profiles) {
				continue
			}
			attempt, stop := context.WithTimeout(ctx, 15*time.Second)
			out, openErr := e.open(attempt, node, ep, in)
			stop()
			if openErr == nil {
				return out, nil
			}
			last = openErr
			if ctx.Err() != nil {
				return "", errors.New("connect_cancelled_or_timeout")
			}
		}
	}
	// Only local fixed stage codes leave the bridge; no TLS/peer text or credentials.
	switch last.Error() {
	case "quic_tls_or_connect_failed", "nvp_handshake_failed", "nvp_auth_failed", "typeconfig_session_ended", "typeconfig_invalid", "mtu_probe_failed":
		return "", last
	}
	return "", errors.New("quic_handshake_auth_or_config_failed")
}

func contains(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}
func hasQUIC(values []transport.Profile) bool {
	for _, v := range values {
		if v == transport.ProfileQUICUDP {
			return true
		}
	}
	return false
}

func (e *Engine) open(ctx context.Context, node model.NodeRegistryEntry, ep transport.Endpoint, in input) (out string, err error) {
	name := node.ServerName
	if name == "" {
		name = ep.Host
	}
	c, err := qt.NewTransport().Dial(ctx, transport.DialConfig{Endpoint: ep, ServerName: name, PinnedPubKey: node.SPKIPin, RootCAs: e.roots})
	if err != nil {
		return "", errors.New("quic_tls_or_connect_failed")
	}
	// Cancellation closes blocking Frozen Core reads during handshake/config.
	stop := context.AfterFunc(ctx, func() { _ = c.Close() })
	defer stop()
	s := session.New(session.DefaultConfig(true))
	runCtx, runCancel := context.WithCancel(e.ctx)
	committed := false
	defer func() {
		if !committed {
			runCancel()
			_ = c.Close()
			_ = s.Close(context.Background())
		}
	}()
	if err = s.Connect(ctx, c); err != nil {
		return "", err
	}
	if err = s.RunHandshake(ctx); err != nil {
		return "", errors.New("nvp_handshake_failed")
	}
	configs := make(chan *netcfg.Message, 1)
	ended := make(chan error, 1)
	s.OnControl(func(kind byte, data []byte) error {
		if kind != control.TypeConfig {
			return nil
		}
		cfg, decodeErr := netcfg.Decode(data)
		if decodeErr != nil {
			return errors.New("typeconfig_invalid")
		}
		if netcfg.IsNetworkBase(cfg.VPNIP, cfg.VPNPrefix) {
			return errors.New("typeconfig_network_address")
		}
		select {
		case configs <- cfg:
		default:
		}
		return nil
	})
	s.OnData(func(p []byte) error {
		if !e.failed.Load() && validIPv4(p) {
			e.rx.Add(uint64(len(p)))
			e.sink.Receive(append([]byte(nil), p...))
		}
		return nil
	})
	key := ed25519.NewKeyFromSeed(in.Seed)
	auth, err := ticket.EncodeAuthPayload(in.Ticket, s.Transcript(), key)
	clear(key)
	if err != nil {
		return "", err
	}
	defer clear(auth)
	if err = s.SendAuth(ctx, auth); err != nil {
		return "", errors.New("nvp_auth_failed")
	}
	if err = s.WaitEstablished(ctx); err != nil {
		return "", errors.New("nvp_auth_failed")
	}
	go func() { ended <- s.ReadLoop(runCtx) }()
	var cfg *netcfg.Message
	select {
	case cfg = <-configs:
	case err = <-ended:
		return "", errors.New("typeconfig_session_ended")
	case <-ctx.Done():
		return "", ctx.Err()
	}
	effective := cfg.MTU
	if dg, ok := c.(transport.DatagramConn); ok && dg.DatagramsEnabled() {
		probe := make([]byte, 4096)
		probe[0] = 0x45
		probeErr := s.WritePacket(ctx, probe)
		var large *quic.DatagramTooLargeError
		if errors.As(probeErr, &large) && large.MaxDatagramPayloadSize > 0 {
			effective, err = mtu.EffectiveTunnelMTU(cfg.MTU, int(large.MaxDatagramPayloadSize))
			if err != nil {
				return "", err
			}
		} else if probeErr != nil {
			return "", errors.New("mtu_probe_failed")
		} else {
			effective = min(cfg.MTU, 1135)
		}
	}
	if effective < 576 || effective > 65535 {
		return "", errors.New("mtu_invalid")
	}
	e.mu.Lock()
	if e.ctx.Err() != nil || ctx.Err() != nil {
		e.mu.Unlock()
		return "", context.Canceled
	}
	e.sess, e.conn, e.mtu = s, c, effective
	e.vpn = netip.MustParseAddr(cfg.VPNIP)
	e.mu.Unlock()
	committed = true
	go func() {
		select {
		case <-ended:
			e.fail("nvp_receive_failed")
		case <-e.ctx.Done():
		}
		runCancel()
	}()
	go func() {
		if s.RunKeepalive(runCtx) != nil && runCtx.Err() == nil {
			e.fail("nvp_keepalive_failed")
		}
	}()
	remote, _, _ := net.SplitHostPort(c.RemoteAddr().String())
	b, _ := json.Marshal(map[string]any{"vpn_ip": cfg.VPNIP, "vpn_prefix": cfg.VPNPrefix, "gateway": cfg.Gateway,
		"dns_servers": cfg.DNSServers, "mtu": effective, "typeconfig_mtu": cfg.MTU, "remote_address": remote})
	return string(b), nil
}

func validIPv4(p []byte) bool {
	return len(p) >= 20 && p[0]>>4 == 4 && int(p[0]&15)*4 >= 20 && int(p[0]&15)*4 <= len(p) && int(p[2])<<8|int(p[3]) == len(p)
}

// Send drops unsupported IPv6, oversized packets and spoofed source addresses.
func (e *Engine) Send(packet []byte) error {
	e.mu.Lock()
	s, size, vpn := e.sess, e.mtu, e.vpn
	e.mu.Unlock()
	if s == nil || e.ctx.Err() != nil || e.failed.Load() {
		return errors.New("session_unavailable")
	}
	if !validIPv4(packet) || len(packet) > size {
		return nil
	}
	a := [4]byte{packet[12], packet[13], packet[14], packet[15]}
	if netip.AddrFrom4(a) != vpn {
		return nil
	}
	ctx, cancel := context.WithTimeout(e.ctx, 5*time.Second)
	defer cancel()
	if err := s.WritePacket(ctx, packet); err != nil {
		var large *quic.DatagramTooLargeError
		if errors.As(err, &large) {
			return nil
		}
		e.fail("nvp_send_failed")
		return errors.New("nvp_send_failed")
	}
	e.tx.Add(uint64(len(packet)))
	return nil
}

func (e *Engine) fail(code string) {
	if e.ctx.Err() == nil && e.failed.CompareAndSwap(false, true) {
		e.Close()
		e.sink.Failed(code)
	}
}
func (e *Engine) Close() {
	e.cancel()
	e.mu.Lock()
	s, c := e.sess, e.conn
	e.sess, e.conn = nil, nil
	e.mu.Unlock()
	// Close transport first to unblock any write holding the Core session mutex.
	if c != nil {
		_ = c.Close()
	}
	if s != nil {
		_ = s.Close(context.Background())
	}
}
func (e *Engine) StatsJSON() string {
	return fmt.Sprintf(`{"rxBytes":%d,"txBytes":%d}`, e.rx.Load(), e.tx.Load())
}
