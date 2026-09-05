// Package listeners accepts TLS and QUIC VPN connections.
package listeners

import (
	"context"
	"crypto/tls"
	"errors"
	"log"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/nyxveil/nvp/core/auth/ticket"
	"github.com/nyxveil/nvp/core/authhandler"
	"github.com/nyxveil/nvp/core/control"
	"github.com/nyxveil/nvp/core/session"
	"github.com/nyxveil/nvp/core/transport"
	"github.com/nyxveil/nvp/core/transport/ech"
	"github.com/nyxveil/nvp/core/transport/serverconfig"
	"github.com/nyxveil/server/internal/datapath"
	"github.com/nyxveil/server/internal/netcfg"
	"github.com/nyxveil/server/internal/sessions"
)

// Config controls listener bind addresses and auth.
type Config struct {
	TLSAddr    string
	QUICAddr   string
	Cert       tls.Certificate
	NodeID     string
	LocationID string
	Verifier   ticket.VerifierConfig
	MTU        int
	ECHKeys    *ech.KeySet
	RequireECH bool
	EnableTLS  bool
	EnableQUIC bool
	SubnetCIDR string
	Gateway    string // optional; default node .1 from subnet
}

// Gate decides whether new sessions may be accepted.
type Gate interface {
	Accepting() bool
	Available() bool
}

// UnknownKIDHook is invoked when ticket verify fails with an unknown key id.
type UnknownKIDHook func(kid string)

// sendConfigFn is the TypeConfig sender (overridable in tests).
var sendConfigFn = netcfg.SendConfig

// attachSessionFn wires the session into the datapath (overridable in tests).
var attachSessionFn = func(b *datapath.Bridge, sess *session.Session) {
	if b != nil {
		b.AttachSession(sess)
	}
}

// Server runs TLS+QUIC accept loops and wires sessions into the datapath.
type Server struct {
	cfg    Config
	gate   Gate
	mgr    *sessions.Manager
	bridge *datapath.Bridge
	auth   *authhandler.AuthHandler

	onUnknownKID UnknownKIDHook

	mu      sync.Mutex
	tlsLn   transport.Listener
	quicLn  transport.Listener
	cancel  context.CancelFunc
	ctx     context.Context
	wg      sync.WaitGroup
	running bool
	tlsOK   atomic.Bool
	quicOK  atomic.Bool
}

// New constructs a server (listeners not started until Start / Reconcile).
// EnableTLS/EnableQUIC are honored as-is: both false means zero VPN listeners
// (node control channel / heartbeat remain independent of this package).
func New(cfg Config, gate Gate, mgr *sessions.Manager, bridge *datapath.Bridge) *Server {
	auth := authhandler.NewAuthHandler(cfg.NodeID, cfg.LocationID, cfg.Verifier)
	return &Server{
		cfg:    cfg,
		gate:   gate,
		mgr:    mgr,
		bridge: bridge,
		auth:   auth,
	}
}

// SetUnknownKIDHook registers a callback for unknown ticket kid errors.
func (s *Server) SetUnknownKIDHook(h UnknownKIDHook) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onUnknownKID = h
}

// UpdateAuth refreshes node/location and verifier used for new connections.
func (s *Server) UpdateAuth(nodeID, locationID string, verifier ticket.VerifierConfig) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cfg.NodeID = nodeID
	s.cfg.LocationID = locationID
	s.cfg.Verifier = verifier
	s.auth = authhandler.NewAuthHandler(nodeID, locationID, verifier)
}

// UpdateECH replaces ECH keys / require flag (takes effect on next StartTLS/QUIC).
func (s *Server) UpdateECH(keys *ech.KeySet, require bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cfg.ECHKeys = keys
	s.cfg.RequireECH = require
}

// UpdateCert replaces the TLS certificate used when (re)starting listeners.
func (s *Server) UpdateCert(cert tls.Certificate) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cfg.Cert = cert
}

// SetTransports updates desired TLS/QUIC enable flags.
func (s *Server) SetTransports(tlsOn, quicOn bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cfg.EnableTLS = tlsOn
	s.cfg.EnableQUIC = quicOn
}

// Start opens enabled listeners and begins accept loops.
func (s *Server) Start(parent context.Context) error {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return nil
	}
	ctx, cancel := context.WithCancel(parent)
	s.ctx = ctx
	s.cancel = cancel
	s.running = true
	wantTLS := s.cfg.EnableTLS
	wantQUIC := s.cfg.EnableQUIC
	s.mu.Unlock()

	if wantTLS {
		if err := s.StartTLS(ctx); err != nil {
			s.Stop()
			return err
		}
	}
	if wantQUIC {
		if err := s.StartQUIC(ctx); err != nil {
			s.Stop()
			return err
		}
	}
	return nil
}

// StartTLS starts (or restarts) the TLS listener.
func (s *Server) StartTLS(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tlsLn != nil {
		_ = s.tlsLn.Close()
		s.tlsLn = nil
		s.tlsOK.Store(false)
	}
	addr := s.cfg.TLSAddr
	if addr == "" {
		addr = ":443"
	}
	ln, err := serverconfig.NewTLSListener(ctx, addr, serverconfig.TLSServerConfig{
		Cert:       s.cfg.Cert,
		ECHKeys:    s.cfg.ECHKeys,
		RequireECH: s.cfg.RequireECH,
	})
	if err != nil {
		return err
	}
	s.tlsLn = ln
	s.tlsOK.Store(true)
	s.wg.Add(1)
	go s.serveLoop(ctx, ln, "tls")
	return nil
}

// StartQUIC starts (or restarts) the QUIC listener.
func (s *Server) StartQUIC(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.quicLn != nil {
		_ = s.quicLn.Close()
		s.quicLn = nil
		s.quicOK.Store(false)
	}
	addr := s.cfg.QUICAddr
	if addr == "" {
		addr = s.cfg.TLSAddr
	}
	if addr == "" {
		addr = ":443"
	}
	ln, err := serverconfig.ListenQUIC(ctx, addr, serverconfig.QUICServerConfig{
		Cert:       s.cfg.Cert,
		ECHKeys:    s.cfg.ECHKeys,
		RequireECH: s.cfg.RequireECH,
	})
	if err != nil {
		return err
	}
	s.quicLn = ln
	s.quicOK.Store(true)
	s.wg.Add(1)
	go s.serveLoop(ctx, ln, "quic")
	return nil
}

// stopTLSLocked closes the TLS listener (caller holds mu).
func (s *Server) stopTLSLocked() {
	if s.tlsLn != nil {
		_ = s.tlsLn.Close()
		s.tlsLn = nil
	}
	s.tlsOK.Store(false)
}

func (s *Server) stopQUICLocked() {
	if s.quicLn != nil {
		_ = s.quicLn.Close()
		s.quicLn = nil
	}
	s.quicOK.Store(false)
}

// Reconcile starts/stops listeners to match EnableTLS/EnableQUIC and rebuilds
// for ECH/cert rotation when a transport remains enabled.
func (s *Server) Reconcile(parent context.Context) error {
	s.mu.Lock()
	if !s.running {
		ctx, cancel := context.WithCancel(parent)
		s.ctx = ctx
		s.cancel = cancel
		s.running = true
	}
	ctx := s.ctx
	wantTLS := s.cfg.EnableTLS
	wantQUIC := s.cfg.EnableQUIC
	s.mu.Unlock()

	if wantTLS {
		if err := s.StartTLS(ctx); err != nil {
			return err
		}
	} else {
		s.mu.Lock()
		s.stopTLSLocked()
		s.mu.Unlock()
	}
	if wantQUIC {
		if err := s.StartQUIC(ctx); err != nil {
			return err
		}
	} else {
		s.mu.Lock()
		s.stopQUICLocked()
		s.mu.Unlock()
	}
	return nil
}

// Stop closes listeners and waits for accept loops.
func (s *Server) Stop() {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return
	}
	s.running = false
	cancel := s.cancel
	s.stopTLSLocked()
	s.stopQUICLocked()
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	s.wg.Wait()
}

// Running reports whether Start/Reconcile has marked the server running.
func (s *Server) Running() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running
}

// TLSOK reports whether the TLS listener is running.
func (s *Server) TLSOK() bool { return s.tlsOK.Load() }

// QUICOK reports whether the QUIC listener is running.
func (s *Server) QUICOK() bool { return s.quicOK.Load() }

func (s *Server) serveLoop(ctx context.Context, ln transport.Listener, label string) {
	defer s.wg.Done()
	for {
		conn, err := ln.Accept(ctx)
		if err != nil {
			// Exit on close or cancel so Reconcile/StartTLS can replace the listener.
			select {
			case <-ctx.Done():
				return
			default:
				log.Printf("listeners: %s accept: %v", label, err)
				return
			}
		}
		if s.gate != nil && (!s.gate.Accepting() || !s.gate.Available()) {
			_ = conn.Close()
			continue
		}
		go s.handleConn(ctx, conn)
	}
}

func (s *Server) handleConn(ctx context.Context, conn transport.Conn) {
	defer conn.Close()
	if s.gate != nil && (!s.gate.Accepting() || !s.gate.Available()) {
		return
	}

	cfg := session.DefaultConfig(false)
	if s.cfg.MTU > 0 {
		cfg.MTU = s.cfg.MTU
	}
	sess := session.New(cfg)

	s.mu.Lock()
	auth := s.auth
	onUnknown := s.onUnknownKID
	s.mu.Unlock()

	sess.OnControl(func(msgType byte, payload []byte) error {
		if msgType == control.TypeAuth {
			err := auth.HandleAuth(ctx, sess, payload)
			if err != nil && onUnknown != nil {
				if kid, ok := unknownKID(err); ok {
					onUnknown(kid)
				}
			}
			return err
		}
		return nil
	})

	if err := sess.Connect(ctx, conn); err != nil {
		return
	}
	if err := sess.RunHandshake(ctx); err != nil {
		return
	}
	if err := sess.WaitEstablished(ctx); err != nil {
		return
	}
	if s.gate != nil && (!s.gate.Accepting() || !s.gate.Available()) {
		_ = sess.Close(ctx)
		return
	}
	rec, err := s.mgr.Allocate(sess)
	if err != nil {
		_ = sess.Close(ctx)
		return
	}
	if err := s.activateAllocatedSession(ctx, sess, rec); err != nil {
		return
	}
	defer s.mgr.ReleaseBySession(sess)
	_ = sess.ReadLoop(ctx)
}

// activateAllocatedSession sends TypeConfig (when a VPN subnet is configured)
// then attaches the session to the datapath. On TypeConfig failure the VPN IP
// is released, the session is closed, and the datapath is not attached.
func (s *Server) activateAllocatedSession(ctx context.Context, sess *session.Session, rec *sessions.Record) error {
	s.mu.Lock()
	subnet := s.cfg.SubnetCIDR
	mtu := s.cfg.MTU
	bridge := s.bridge
	s.mu.Unlock()

	if subnet != "" {
		nodeAddr, err := sessions.NodeAddress(subnet)
		if err != nil {
			log.Printf("listeners: TypeConfig node address: %v", err)
			s.mgr.ReleaseBySession(sess)
			_ = sess.Close(ctx)
			return err
		}
		msg, err := netcfg.FromAllocation(rec.VPNIP, subnet, mtu, nodeAddr.Addr())
		if err != nil {
			log.Printf("listeners: TypeConfig encode: %v", err)
			s.mgr.ReleaseBySession(sess)
			_ = sess.Close(ctx)
			return err
		}
		if err := sendConfigFn(ctx, sess, msg); err != nil {
			log.Printf("listeners: TypeConfig: %v", err)
			s.mgr.ReleaseBySession(sess)
			_ = sess.Close(ctx)
			return err
		}
	}
	attachSessionFn(bridge, sess)
	return nil
}

func unknownKID(err error) (string, bool) {
	if err == nil {
		return "", false
	}
	const prefix = "unknown key id: "
	msg := err.Error()
	if i := strings.Index(msg, prefix); i >= 0 {
		return strings.TrimSpace(msg[i+len(prefix):]), true
	}
	return "", false
}

// ErrNotRunning is returned when operations require a started server.
var ErrNotRunning = errors.New("listeners: not running")
