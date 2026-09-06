package engine

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"net/netip"
	"sync"
	"time"

	"github.com/nyxveil/client-windows/internal/ipc"
	"github.com/nyxveil/client-windows/internal/netcfg"
	"github.com/nyxveil/client-windows/internal/state"
	"github.com/nyxveil/nvp/core/auth/ticket"
	"github.com/nyxveil/nvp/core/connector"
	"github.com/nyxveil/nvp/core/control"
	"github.com/nyxveil/nvp/core/controlplane/catalog"
	"github.com/nyxveil/nvp/core/controlplane/model"
	"github.com/nyxveil/nvp/core/failover"
	"github.com/nyxveil/nvp/core/nvperr"
	"github.com/nyxveil/nvp/core/session"
	"github.com/nyxveil/nvp/core/transport"
	nvpquic "github.com/nyxveil/nvp/core/transport/quic"
	tlsstream "github.com/nyxveil/nvp/core/transport/tlsstream"
	"github.com/nyxveil/nvp/core/tunnel"
)

const (
	defaultConfigWait = 30 * time.Second
	tunAdapterName    = "Nyxveil"
)

// TicketRequester is invoked when reconnect/failover needs a fresh access ticket
// without the service holding a license credential (GUI answers via IPC).
type TicketRequester func(ctx context.Context, need ipc.NeedAccessTicket) (accessTicket string, err error)

// ConnectPhase names blocking Connect stages for cancel tests / diagnostics.
type ConnectPhase string

const (
	PhaseDial       ConnectPhase = "dial"
	PhaseAuth       ConnectPhase = "auth"
	PhaseTypeConfig ConnectPhase = "typeconfig"
	PhaseTUN        ConnectPhase = "tun"
)

// LifecycleHooks are optional; production leaves them nil.
type LifecycleHooks struct {
	OnEnterPhase func(phase ConnectPhase, ctx context.Context) error
}

// SessionOpener overrides openSessionWithTicket (tests inject hang/cancel phases).
type SessionOpener func(ctx context.Context, cat model.Catalog, req ConnectRequest) (*session.Session, transport.Conn, model.NodeRegistryEntry, error)

// TunOpener opens the OS TUN device.
type TunOpener interface {
	Open(ctx context.Context, cfg tunnel.Config) (tunnel.Device, error)
}

// Manager owns Frozen Connector settings + session lifecycle for the Windows service.
type Manager struct {
	mu sync.Mutex

	Connector     *connector.Connector
	State         *state.Machine
	Routes        RouteApplier
	TUN           TunOpener
	RequestTicket TicketRequester
	ConfigWait    time.Duration
	Hooks         *LifecycleHooks
	SessionOpener SessionOpener
	TypeConfigFn  func(ctx context.Context) (*netcfg.Message, error)

	sess          *session.Session
	conn          transport.Conn
	tun           tunnel.Device
	node          model.NodeRegistryEntry
	plan          *Plan
	runCancel     context.CancelFunc
	connectCancel context.CancelFunc
	ticketCancel  context.CancelFunc // cancels pending reconnect ticket wait
	connecting    bool
	opGen         uint64
	desiredConnected bool  // false after user Disconnect; blocks auto-reconnect
	reconnectGen     uint64 // bumped on user Disconnect/Connect; stale onSessionLost must stop
	reconnectInFlight bool

	lastConnect *ConnectRequest
	lastCatalog model.Catalog
}

// ErrConnectInProgress is returned when a second Connect overlaps an in-flight one.
var ErrConnectInProgress = errors.New("engine: connection already in progress")

// ErrStaleConnect is returned when a superseded Connect tries to commit after Disconnect.
var ErrStaleConnect = errors.New("engine: connect operation superseded")


// Options configures a Manager.
type Options struct {
	Trust         *TrustProvider
	Routes        RouteApplier
	TUN           TunOpener
	RequestTicket TicketRequester
	Policy        failover.ConnectPolicy
	Hooks         *LifecycleHooks
	SessionOpener SessionOpener
	TypeConfigFn  func(ctx context.Context) (*netcfg.Message, error)
}

// NewManager builds a Connector with RequirePin=true, QUIC primary + TLS fallback.
func NewManager(opts Options) *Manager {
	trust := opts.Trust
	if trust == nil {
		trust = NewSystemTrust()
	}
	routes := opts.Routes
	if routes == nil {
		routes = &NoopApplier{}
	}
	tun := opts.TUN
	if tun == nil {
		tun = NewTunFactory()
	}
	policy := opts.Policy
	if policy.MaxNodeAttempts == 0 {
		policy = failover.DefaultConnectPolicy()
	}
	reg := transport.NewRegistry()
	// Catalog-authoritative racing: DefaultRacingConfig prefers QUIC then TLS.
	reg.Register(nvpquic.NewTransport())
	reg.Register(tlsstream.NewTransport())
	c := &connector.Connector{
		Registry:   reg,
		RequirePin: true,
		Provider:   trust,
		Policy:     policy,
	}
	return &Manager{
		Connector:     c,
		State:         state.New(),
		Routes:        routes,
		TUN:           tun,
		RequestTicket: opts.RequestTicket,
		ConfigWait:    defaultConfigWait,
		Hooks:         opts.Hooks,
		SessionOpener: opts.SessionOpener,
		TypeConfigFn:  opts.TypeConfigFn,
	}
}

// ConnectRequest is the engine-facing view of ipc.ConnectRequest.
type ConnectRequest struct {
	DesiredLocationID string
	AccessTicket      string
	SignedCatalogJSON []byte
	CatalogKeys       map[string]string
	DevicePrivateKey  ed25519.PrivateKey
	ControlPlaneHost  string
}

// Connect is a user-initiated connect (sets desiredConnected, invalidates stale reconnects).
func (m *Manager) Connect(ctx context.Context, req ConnectRequest) error {
	return m.connect(ctx, req, true)
}

// connect verifies catalog, dials, AUTH, TypeConfig, TUN.
// userInitiated=true: desiredConnected=true and bump reconnectGen (kills stale onSessionLost).
func (m *Manager) connect(ctx context.Context, req ConnectRequest, userInitiated bool) error {
	if req.AccessTicket == "" {
		return fmt.Errorf("engine: access_ticket required")
	}
	if len(req.DevicePrivateKey) != ed25519.PrivateKeySize {
		return fmt.Errorf("%w", nvperr.ErrDeviceKeyRequired)
	}
	if req.DesiredLocationID == "" {
		return fmt.Errorf("engine: desired_location_id required")
	}

	m.mu.Lock()
	if m.sess != nil {
		m.mu.Unlock()
		return fmt.Errorf("engine: already connected")
	}
	if m.connecting {
		m.mu.Unlock()
		return ErrConnectInProgress
	}
	if userInitiated {
		m.desiredConnected = true
		m.reconnectGen++
		if m.ticketCancel != nil {
			m.ticketCancel()
			m.ticketCancel = nil
		}
	} else if !m.desiredConnected {
		m.mu.Unlock()
		return fmt.Errorf("engine: reconnect aborted (desiredConnected=false)")
	}
	m.opGen++
	gen := m.opGen
	m.connecting = true
	cctx, cancel := context.WithCancel(ctx)
	m.connectCancel = cancel
	m.mu.Unlock()
	defer func() {
		cancel()
		m.mu.Lock()
		if m.opGen == gen {
			m.connecting = false
			m.connectCancel = nil
		}
		m.mu.Unlock()
	}()

	alive := func() error {
		m.mu.Lock()
		ok := m.opGen == gen && m.desiredConnected
		m.mu.Unlock()
		if !ok {
			return ErrStaleConnect
		}
		return cctx.Err()
	}

	m.State.Set(state.SelectingNode)

	keys, err := DecodeCatalogKeys(req.CatalogKeys)
	if err != nil {
		m.State.Fail(err.Error())
		return err
	}
	// Catalog keys only under our single-flight slot (no concurrent Connect writers).
	m.Connector.CatalogVerifyKeys = keys

	signed, err := catalog.Parse(req.SignedCatalogJSON)
	if err != nil {
		m.State.Fail(err.Error())
		return fmt.Errorf("engine: catalog parse: %w", err)
	}
	if err := catalog.Verify(keys, signed); err != nil {
		m.State.Fail(err.Error())
		return fmt.Errorf("engine: catalog verify: %w", err)
	}
	if err := alive(); err != nil {
		return err
	}

	plan := NewPlan()
	if err := m.Routes.Capture(plan); err != nil {
		m.State.Fail(err.Error())
		return err
	}
	bypass, err := buildBypassHosts(req.ControlPlaneHost, signed.Catalog, req.DesiredLocationID)
	if err != nil {
		m.State.Fail(err.Error())
		return err
	}
	plan.SetBypassHosts(bypass)
	if err := m.Routes.ApplyBypass(plan); err != nil {
		_ = m.Routes.Restore(plan)
		m.State.Fail(err.Error())
		return err
	}
	if err := alive(); err != nil {
		_ = m.Routes.Restore(plan)
		return err
	}

	m.State.Set(state.ConnectingTransport)
	var (
		sess *session.Session
		conn transport.Conn
		node model.NodeRegistryEntry
	)
	if m.SessionOpener != nil {
		sess, conn, node, err = m.SessionOpener(cctx, signed.Catalog, req)
	} else {
		sess, conn, node, err = m.openSessionWithTicket(cctx, signed.Catalog, req)
	}
	if err != nil {
		_ = m.Routes.Restore(plan)
		if !errors.Is(err, ErrStaleConnect) && !errors.Is(err, context.Canceled) {
			m.State.Fail(err.Error())
		}
		return err
	}

	m.State.Set(state.WaitingForConfig)
	cfgMsg, err := m.waitTypeConfig(cctx, sess, conn)
	if err != nil {
		_ = sess.Close(context.Background())
		_ = conn.Close()
		_ = m.Routes.Restore(plan)
		if !errors.Is(err, context.Canceled) && !errors.Is(err, ErrStaleConnect) {
			m.State.Fail(err.Error())
		}
		return err
	}

	m.State.Set(state.ConfiguringTunnel)
	if err := plan.ApplyTypeConfig(tunAdapterName, cfgMsg.VPNIP, cfgMsg.VPNPrefix, cfgMsg.Gateway, cfgMsg.DNSServers, cfgMsg.MTU); err != nil {
		_ = sess.Close(context.Background())
		_ = conn.Close()
		_ = m.Routes.Restore(plan)
		m.State.Fail(err.Error())
		return err
	}
	if err := alive(); err != nil {
		_ = sess.Close(context.Background())
		_ = conn.Close()
		_ = m.Routes.Restore(plan)
		return err
	}

	if err := m.enterPhase(PhaseTUN, cctx); err != nil {
		_ = sess.Close(context.Background())
		_ = conn.Close()
		_ = m.Routes.Restore(plan)
		return err
	}
	tunDev, err := m.TUN.Open(cctx, tunnel.Config{Name: tunAdapterName, MTU: cfgMsg.MTU})
	if err != nil {
		_ = sess.Close(context.Background())
		_ = conn.Close()
		_ = m.Routes.Restore(plan)
		m.State.Fail(err.Error())
		return err
	}

	if err := m.Routes.ApplyTunnel(plan); err != nil {
		_ = tunDev.Close()
		_ = sess.Close(context.Background())
		_ = conn.Close()
		_ = m.Routes.Restore(plan)
		m.State.Fail(err.Error())
		return err
	}
	if err := alive(); err != nil {
		_ = tunDev.Close()
		_ = sess.Close(context.Background())
		_ = conn.Close()
		_ = m.Routes.Restore(plan)
		return err
	}

	sess.OnData(func(pkt []byte) error {
		_, err := tunDev.Write(pkt)
		return err
	})

	runCtx, runCancel := context.WithCancel(context.Background())
	m.mu.Lock()
	if m.opGen != gen || !m.desiredConnected {
		m.mu.Unlock()
		runCancel()
		_ = tunDev.Close()
		_ = sess.Close(context.Background())
		_ = conn.Close()
		_ = m.Routes.Restore(plan)
		return ErrStaleConnect
	}
	m.sess = sess
	m.conn = conn
	m.tun = tunDev
	m.node = node
	m.plan = plan
	m.runCancel = runCancel
	reqCopy := req
	m.lastConnect = &reqCopy
	m.lastCatalog = signed.Catalog
	m.connecting = false
	m.connectCancel = nil
	m.mu.Unlock()
	m.State.Set(state.Connected)

	go func() {
		buf := make([]byte, 65535)
		for {
			select {
			case <-runCtx.Done():
				return
			default:
			}
			n, err := tunDev.Read(buf)
			if err != nil {
				return
			}
			if n <= 0 {
				continue
			}
			cp := make([]byte, n)
			copy(cp, buf[:n])
			if err := sess.WritePacket(runCtx, cp); err != nil {
				return
			}
		}
	}()
	go func() {
		_ = sess.ReadLoop(runCtx)
		m.onSessionLost()
	}()
	return nil
}

// Disconnect cancels Connect, pending reconnect tickets, and forbids auto-reconnect.
// Returns a non-nil error if network restore failed (journal preserved for retry).
func (m *Manager) Disconnect(ctx context.Context) error {
	m.mu.Lock()
	m.desiredConnected = false
	m.reconnectGen++ // invalidate any in-flight onSessionLost
	if m.ticketCancel != nil {
		m.ticketCancel()
		m.ticketCancel = nil
	}
	m.opGen++ // invalidate any in-flight Connect commit
	if m.connectCancel != nil {
		m.connectCancel()
		m.connectCancel = nil
	}
	m.connecting = false
	m.reconnectInFlight = false
	m.lastConnect = nil
	m.lastCatalog = model.Catalog{}
	m.State.Set(state.Disconnecting)
	restoreErr := m.teardownLocked()
	m.mu.Unlock()
	m.State.Set(state.Disconnected)
	return restoreErr
}

func (m *Manager) onSessionLost() {
	m.mu.Lock()
	if !m.desiredConnected {
		_ = m.teardownLocked()
		m.mu.Unlock()
		m.State.Set(state.Disconnected)
		return
	}
	if m.reconnectInFlight {
		_ = m.teardownLocked()
		m.mu.Unlock()
		return
	}
	gen := m.reconnectGen
	loc := ""
	var last *ConnectRequest
	if m.lastConnect != nil {
		cp := *m.lastConnect
		last = &cp
		loc = last.DesiredLocationID
	}
	reqTicket := m.RequestTicket
	if last == nil || reqTicket == nil || loc == "" {
		_ = m.teardownLocked()
		m.desiredConnected = false
		m.mu.Unlock()
		m.State.Set(state.Disconnected)
		return
	}
	m.reconnectInFlight = true
	ticketCtx, cancel := context.WithCancel(context.Background())
	m.ticketCancel = cancel
	_ = m.teardownLocked()
	m.mu.Unlock()

	m.State.Set(state.Reconnecting)
	ticket, err := reqTicket(ticketCtx, EmitNeedAccessTicket(loc, "session_lost_failover", "Reconnecting"))

	m.mu.Lock()
	m.ticketCancel = nil
	still := m.desiredConnected && m.reconnectGen == gen
	m.reconnectInFlight = false
	m.mu.Unlock()
	if !still {
		return
	}
	if err != nil {
		m.State.Fail(err.Error())
		return
	}

	if hook := AfterReconnectTicketForTest; hook != nil {
		hook()
	}

	m.mu.Lock()
	still = m.desiredConnected && m.reconnectGen == gen
	m.mu.Unlock()
	if !still {
		return
	}

	creq := *last
	creq.AccessTicket = ticket
	_ = m.connect(context.Background(), creq, false)
}

// TriggerSessionLostForTest invokes onSessionLost (reconnect-race unit tests only).
func TriggerSessionLostForTest(m *Manager) { m.onSessionLost() }

// AfterReconnectTicketForTest runs after RequestTicket returns and before reconnect Connect (tests only).
var AfterReconnectTicketForTest func()

func (m *Manager) teardownLocked() error {
	if m.runCancel != nil {
		m.runCancel()
		m.runCancel = nil
	}
	if m.sess != nil {
		_ = m.sess.Close(context.Background())
		m.sess = nil
	}
	if m.conn != nil {
		_ = m.conn.Close()
		m.conn = nil
	}
	if m.tun != nil {
		_ = m.tun.Close()
		m.tun = nil
	}
	var restoreErr error
	if m.plan != nil {
		restoreErr = m.Routes.Restore(m.plan)
		m.plan = nil
	}
	m.node = model.NodeRegistryEntry{}
	return restoreErr
}

// StatusSnapshot builds an IPC status frame from the state machine.
func (m *Manager) StatusSnapshot(clientVer, coreVer string) ipc.StatusSnapshot {
	st, errMsg := m.State.Get()
	snap := ipc.StatusSnapshot{
		Envelope:  ipc.Envelope{Version: ipc.ProtocolVersion, Type: ipc.TypeStatus},
		State:     st.String(),
		LastError: errMsg,
		ClientVer: clientVer,
		CoreVer:   coreVer,
		Protocol:  "NVP/1",
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.node.NodeID != "" {
		snap.NodeID = m.node.NodeID
		snap.LocationID = m.node.LocationID
	}
	return snap
}

func (m *Manager) enterPhase(phase ConnectPhase, ctx context.Context) error {
	if m.Hooks == nil || m.Hooks.OnEnterPhase == nil {
		return nil
	}
	return m.Hooks.OnEnterPhase(phase, ctx)
}

func (m *Manager) openSessionWithTicket(ctx context.Context, cat model.Catalog, req ConnectRequest) (*session.Session, transport.Conn, model.NodeRegistryEntry, error) {
	c := m.Connector
	sel := &failover.Selector{Catalog: cat, LocationID: req.DesiredLocationID}
	candidates := sel.CandidateNodes()
	if len(candidates) == 0 {
		return nil, nil, model.NodeRegistryEntry{}, nvperr.ErrNoHealthyNodes
	}
	for _, node := range candidates {
		if len(node.SPKIPin) == 0 && c.RequirePin {
			return nil, nil, model.NodeRegistryEntry{}, fmt.Errorf("%w: SPKI pin required but empty for node %s", nvperr.ErrServerIdentityMismatch, node.NodeID)
		}
	}

	policy := c.Policy
	if claims, peekErr := ticket.PeekClaims(req.AccessTicket); peekErr == nil {
		if len(claims.NodeScope) > 0 {
			policy.AllowedNodeIDs = append([]string(nil), claims.NodeScope...)
		}
	}

	m.State.Set(state.ConnectingTransport)
	if err := m.enterPhase(PhaseDial, ctx); err != nil {
		return nil, nil, model.NodeRegistryEntry{}, err
	}
	conn, node, err := failover.ConnectWithFailover(ctx, sel, c.Registry, policy, c.Provider)
	if err != nil {
		var ex *failover.ExhaustedError
		if errors.As(err, &ex) {
			return nil, nil, model.NodeRegistryEntry{}, err
		}
		return nil, nil, model.NodeRegistryEntry{}, fmt.Errorf("%w: %v", nvperr.ErrTransportUnavailable, err)
	}

	sess := session.New(session.DefaultConfig(true))
	if err := sess.Connect(ctx, conn); err != nil {
		_ = conn.Close()
		return nil, nil, model.NodeRegistryEntry{}, fmt.Errorf("%w: %v", nvperr.ErrHandshakeFailed, err)
	}
	if err := sess.RunHandshake(ctx); err != nil {
		_ = conn.Close()
		return nil, nil, model.NodeRegistryEntry{}, fmt.Errorf("%w: %v", nvperr.ErrHandshakeFailed, err)
	}

	m.State.Set(state.Authenticating)
	if err := m.enterPhase(PhaseAuth, ctx); err != nil {
		_ = conn.Close()
		return nil, nil, model.NodeRegistryEntry{}, err
	}
	timeout := connector.DefaultAuthTimeout
	authCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if dl, ok := authCtx.Deadline(); ok {
		_ = conn.SetReadDeadline(dl)
		_ = conn.SetWriteDeadline(dl)
		defer func() {
			_ = conn.SetReadDeadline(time.Time{})
			_ = conn.SetWriteDeadline(time.Time{})
		}()
	}

	authBody, err := ticket.EncodeAuthPayload(req.AccessTicket, sess.Transcript(), req.DevicePrivateKey)
	if err != nil {
		_ = conn.Close()
		return nil, nil, model.NodeRegistryEntry{}, fmt.Errorf("%w: %v", nvperr.ErrDeviceKeyRequired, err)
	}
	if err := sess.SendAuth(authCtx, authBody); err != nil {
		_ = conn.Close()
		return nil, nil, model.NodeRegistryEntry{}, fmt.Errorf("%w: %v", nvperr.ErrAuthFailed, err)
	}
	if err := sess.WaitEstablished(authCtx); err != nil {
		_ = conn.Close()
		return nil, nil, model.NodeRegistryEntry{}, err
	}
	if sess.State() != session.StateEstablished {
		_ = conn.Close()
		return nil, nil, model.NodeRegistryEntry{}, nvperr.ErrAuthFailed
	}
	return sess, conn, node, nil
}

func (m *Manager) waitTypeConfig(ctx context.Context, sess *session.Session, conn transport.Conn) (*netcfg.Message, error) {
	if err := m.enterPhase(PhaseTypeConfig, ctx); err != nil {
		return nil, err
	}
	if m.TypeConfigFn != nil {
		return m.TypeConfigFn(ctx)
	}
	wait := m.ConfigWait
	if wait <= 0 {
		wait = defaultConfigWait
	}
	cfgCtx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()

	cfgCh := make(chan *netcfg.Message, 1)
	errCh := make(chan error, 1)

	sess.OnControl(func(msgType byte, payload []byte) error {
		if msgType != control.TypeConfig {
			return nil
		}
		msg, err := netcfg.Decode(payload)
		if err != nil {
			select {
			case errCh <- err:
			default:
			}
			return err
		}
		select {
		case cfgCh <- msg:
		default:
		}
		return nil
	})

	loopCtx, loopCancel := context.WithCancel(cfgCtx)
	loopDone := make(chan struct{})
	go func() {
		defer close(loopDone)
		if typeConfigReadLoopForTest != nil {
			_ = typeConfigReadLoopForTest(loopCtx, sess)
			return
		}
		_ = sess.ReadLoop(loopCtx)
	}()

	select {
	case <-cfgCtx.Done():
		stopTempTransportReader(loopCancel, loopDone, conn)
		return nil, fmt.Errorf("engine: TypeConfig timeout: %w", cfgCtx.Err())
	case err := <-errCh:
		stopTempTransportReader(loopCancel, loopDone, conn)
		return nil, err
	case msg := <-cfgCh:
		stopTempTransportReader(loopCancel, loopDone, conn)
		return msg, nil
	case msg := <-typeConfigTestSignal:
		if msg != nil {
			stopTempTransportReader(loopCancel, loopDone, conn)
			return msg, nil
		}
		stopTempTransportReader(loopCancel, loopDone, conn)
		return nil, fmt.Errorf("engine: TypeConfig test signal nil")
	}
}

// stopTempTransportReader mirrors Frozen Core Session.WaitEstablished stopTempRead:
// cancel → SetReadDeadline(now) → wait for reader exit → clear deadline.
// Callers must not start another ReadLoop until this returns.
func stopTempTransportReader(cancel context.CancelFunc, done <-chan struct{}, conn transport.Conn) {
	if cancel != nil {
		cancel()
	}
	if conn != nil {
		_ = conn.SetReadDeadline(time.Now())
	}
	if done != nil {
		select {
		case <-done:
		case <-time.After(2 * time.Second):
		}
	}
	if conn != nil {
		_ = conn.SetReadDeadline(time.Time{})
	}
}

// Test hooks (nil in production).
var (
	typeConfigReadLoopForTest func(ctx context.Context, sess *session.Session) error
	typeConfigTestSignal      <-chan *netcfg.Message
)

// EmitNeedAccessTicket builds a NeedAccessTicket IPC frame for the GUI.
func EmitNeedAccessTicket(locationID, reason, stateName string) ipc.NeedAccessTicket {
	return ipc.NeedAccessTicket{
		Envelope:          ipc.Envelope{Version: ipc.ProtocolVersion, Type: ipc.TypeNeedAccessTicket},
		DesiredLocationID: locationID,
		Reason:            reason,
		State:             stateName,
	}
}

// DecodeCatalogKeys parses kid → std Base64 Ed25519 public keys.
func DecodeCatalogKeys(in map[string]string) (catalog.VerifyKeys, error) {
	out := catalog.VerifyKeys{Keys: make(map[string]ed25519.PublicKey, len(in))}
	if len(in) == 0 {
		return out, fmt.Errorf("engine: catalog_keys required")
	}
	for kid, b64 := range in {
		raw, err := base64.StdEncoding.DecodeString(b64)
		if err != nil {
			return out, fmt.Errorf("engine: catalog_keys[%s]: %w", kid, err)
		}
		if len(raw) != ed25519.PublicKeySize {
			return out, fmt.Errorf("engine: catalog_keys[%s]: want %d bytes, got %d", kid, ed25519.PublicKeySize, len(raw))
		}
		out.Keys[kid] = ed25519.PublicKey(raw)
	}
	return out, nil
}

func buildBypassHosts(cpHost string, cat model.Catalog, locationID string) ([]HostRoute, error) {
	seen := map[netip.Addr]struct{}{}
	var hosts []HostRoute
	add := func(ip netip.Addr) {
		if !ip.IsValid() || !ip.Is4() {
			return
		}
		if _, ok := seen[ip]; ok {
			return
		}
		seen[ip] = struct{}{}
		hosts = append(hosts, HostRoute{Destination: netip.PrefixFrom(ip, 32)})
	}
	if cpHost != "" {
		for _, ip := range ResolveHostIPs(cpHost) {
			add(ip)
		}
	}
	sel := &failover.Selector{Catalog: cat, LocationID: locationID}
	for _, n := range sel.CandidateNodes() {
		for _, ep := range n.Endpoints {
			for _, ip := range ResolveHostIPs(ep.Host) {
				add(ip)
			}
		}
	}
	return hosts, nil
}
