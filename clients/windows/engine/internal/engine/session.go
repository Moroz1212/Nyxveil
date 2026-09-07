package engine

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log"
	"net/netip"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nyxveil/client-windows/internal/diag"
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

	// earlyTypeConfig buffers TypeConfig that may arrive during WaitEstablished.
	// Server sends AUTH_OK then TypeConfig immediately; Frozen Core WaitEstablished
	// still owns the stream reader and would otherwise drop TypeConfig when
	// OnControl is nil (handleMessage has no TypeConfig case).
	earlyTypeConfig chan *netcfg.Message
	earlyTypeConfigErr chan error

	lastConnect *ConnectRequest
	lastCatalog model.Catalog

	// connectedAt is set when entering Connected; zero when not connected.
	// Used only for GUI session timer via StatusSnapshot (read-only).
	connectedAt time.Time

	// OnStatusChange is optional; Windows service uses it to push StatusSnapshot
	// after lifecycle disconnects so the GUI cannot stay stale on "Подключено".
	OnStatusChange func()
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
		// Idempotent Connect: session already committed. GUI double-click must not
		// surface "already connected" as a hard error while Status stays Connected.
		return nil
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
	diag.InfoFields("SESSION", "Connect start", map[string]string{
		"location":       req.DesiredLocationID,
		"user_initiated": fmt.Sprintf("%v", userInitiated),
	})
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
	// Tolerate small not-before skew (CP IssuedAt slightly ahead / unsynced client clock)
	// without changing Frozen Core catalog.Verify or extending expires_at.
	waitCatalogNotBefore(signed.Catalog.IssuedAt, 5*time.Minute)
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
		diag.Error("TRANSPORT", "open_failed", err.Error())
		return err
	}
	diag.InfoFields("TRANSPORT", "session_open", map[string]string{
		"node":     node.NodeID,
		"location": node.LocationID,
	})
	if conn != nil {
		ep := ""
		if ra := conn.RemoteAddr(); ra != nil {
			ep = ra.String()
		}
		diag.InfoFields("TRANSPORT", "selected", map[string]string{
			"type":     string(conn.Profile()),
			"endpoint": ep,
		})
	}

	m.State.Set(state.WaitingForConfig)
	diag.Info("STATE", "WaitingForConfig", "")
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
	diag.InfoFields("TYPECONFIG", "received", map[string]string{
		"vpn_ip":  cfgMsg.VPNIP,
		"prefix":  fmt.Sprintf("%d", cfgMsg.VPNPrefix),
		"gateway": cfgMsg.Gateway,
		"mtu":     fmt.Sprintf("%d", cfgMsg.MTU),
		"dns":     fmt.Sprintf("%v", cfgMsg.DNSServers),
	})

	// Local effective tunnel MTU: never apply TypeConfig MTU blindly if QUIC DATAGRAM
	// cannot carry NVP-framed DATA of that size. TypeConfig wire value is unchanged.
	typeConfigMTU := cfgMsg.MTU
	effectiveMTU := typeConfigMTU
	if maxPayload, probed, probeErr := ProbeDatagramPayloadCeiling(cctx, sess, conn); probeErr != nil {
		diag.Warn("TRANSPORT", "datagram_probe_failed", probeErr.Error())
	} else if probed {
		eff, budget, calcErr := EffectiveTunnelMTU(typeConfigMTU, int(maxPayload))
		if calcErr != nil {
			diag.Warn("TRANSPORT", "datagram_budget_failed", calcErr.Error())
		} else {
			effectiveMTU = eff
			diag.InfoFields("TRANSPORT", "transport_datagram_limit", map[string]string{
				"max_datagram_payload": fmt.Sprintf("%d", maxPayload),
				"typeconfig_mtu":       fmt.Sprintf("%d", typeConfigMTU),
				"nvp_overhead":         fmt.Sprintf("%d", NVPOverheadWorstCase()),
				"nvp_fixed_overhead":   fmt.Sprintf("%d", NVPFixedWireOverhead),
				"nvp_max_padding":      fmt.Sprintf("%d", NVPDefaultMaxPadding),
				"http3_prefix_max":     fmt.Sprintf("%d", HTTP3DatagramPrefixMax),
				"max_ip_packet":        fmt.Sprintf("%d", budget.MaxIPPacket),
				"effective_tunnel_mtu": fmt.Sprintf("%d", effectiveMTU),
			})
		}
	} else {
		diag.InfoFields("TRANSPORT", "transport_datagram_limit", map[string]string{
			"max_datagram_payload": "n/a",
			"typeconfig_mtu":       fmt.Sprintf("%d", typeConfigMTU),
			"nvp_overhead":         fmt.Sprintf("%d", NVPOverheadWorstCase()),
			"effective_tunnel_mtu": fmt.Sprintf("%d", effectiveMTU),
			"note":                 "datagrams_disabled_or_unprobed",
		})
	}

	m.State.Set(state.ConfiguringTunnel)
	diag.Info("STATE", "ConfiguringTunnel", "")
	if err := plan.ApplyTypeConfig(tunAdapterName, cfgMsg.VPNIP, cfgMsg.VPNPrefix, cfgMsg.Gateway, cfgMsg.DNSServers, effectiveMTU); err != nil {
		_ = sess.Close(context.Background())
		_ = conn.Close()
		_ = m.Routes.Restore(plan)
		m.State.Fail(err.Error())
		return err
	}
	plan.Gate.TypeConfigOK = true
	if err := alive(); err != nil {
		_ = sess.Close(context.Background())
		_ = conn.Close()
		_ = m.Routes.Restore(plan)
		return err
	}

	// Session lifetime context — independent of connect/handshake deadlines.
	// Must be armed BEFORE ApplyTunnel: production ApplyTunnel (netsh + PowerShell
	// IPv6) can take seconds with no transport reader; the server may CLOSE/idle
	// the stream, and starting ReadLoop only after Connected makes the GUI flash
	// Connected then immediately tear down via onSessionLost.
	runCtx, runCancel := context.WithCancel(context.Background())
	var dataHandler atomic.Value // stores func([]byte) error
	dataHandler.Store(func([]byte) error { return nil })
	sess.OnData(func(pkt []byte) error {
		h, _ := dataHandler.Load().(func([]byte) error)
		if h == nil {
			return nil
		}
		return h(pkt)
	})
	readErrCh := make(chan error, 1)
	authOKAt := time.Now()
	go func() {
		diag.Info("TRANSPORT", "ReadLoop start", "")
		err := sess.ReadLoop(runCtx)
		lifetime := time.Since(authOKAt).Milliseconds()
		diag.Error("TRANSPORT", "ReadLoop exit", fmt.Sprintf("%v", err))
		diag.InfoFields("TRANSPORT", "close", map[string]string{
			"local":        "false",
			"close_source": "readloop",
			"error":        fmt.Sprintf("%v", err),
			"lifetime_ms":  fmt.Sprintf("%d", lifetime),
		})
		readErrCh <- err
	}()
	go func() {
		diag.Info("TRANSPORT", "keepalive start", "")
		err := runKeepaliveLogged(runCtx, sess)
		if err != nil && !errors.Is(err, context.Canceled) {
			diag.Warn("TRANSPORT", "keepalive exit", err.Error())
		}
	}()
	abortSetup := func(err error) error {
		runCancel()
		select {
		case <-readErrCh:
		case <-time.After(2 * time.Second):
		}
		_ = sess.Close(context.Background())
		_ = conn.Close()
		_ = m.Routes.Restore(plan)
		return err
	}
	checkTransportAlive := func(stage string) error {
		select {
		case err := <-readErrCh:
			msg := fmt.Sprintf("engine: session transport closed during %s", stage)
			if err != nil && !errors.Is(err, context.Canceled) {
				msg = msg + ": " + err.Error()
			}
			log.Printf("%s", msg)
			_ = abortSetup(errors.New(msg))
			m.State.Fail(msg)
			return errors.New(msg)
		default:
			return nil
		}
	}

	if err := m.enterPhase(PhaseTUN, cctx); err != nil {
		_ = abortSetup(err)
		return err
	}
	tunDev, err := m.TUN.Open(cctx, tunnel.Config{Name: tunAdapterName, MTU: plan.TunMTU})
	if err != nil {
		_ = abortSetup(err)
		m.State.Fail(err.Error())
		diag.Error("WINTUN", "open_failed", err.Error())
		return err
	}
	if err := bindTunnelIdentity(plan, tunDev); err != nil {
		_ = tunDev.Close()
		_ = abortSetup(err)
		m.State.Fail(err.Error())
		return err
	}
	diag.InfoFields("WINTUN", "opened", map[string]string{
		"name":    tunAdapterName,
		"ifIndex": fmt.Sprintf("%d", plan.TunIfIndex),
		"luid":    fmt.Sprintf("%d", plan.TunLUID),
	})
	if err := checkTransportAlive("tun_open"); err != nil {
		_ = tunDev.Close()
		return err
	}

	diag.Info("NETWORK", "ApplyTunnel begin", "")
	applyErrCh := make(chan error, 1)
	go func() {
		applyErrCh <- m.Routes.ApplyTunnel(plan)
	}()
	var applyErr error
	select {
	case err := <-readErrCh:
		_ = tunDev.Close()
		msg := "engine: session transport closed during ApplyTunnel"
		if err != nil && !errors.Is(err, context.Canceled) {
			msg = msg + ": " + err.Error()
		}
		log.Printf("%s", msg)
		runCancel()
		<-applyErrCh // wait applier finish/rollback path; may still error
		_ = sess.Close(context.Background())
		_ = conn.Close()
		_ = m.Routes.Restore(plan)
		m.State.Fail(msg)
		return errors.New(msg)
	case applyErr = <-applyErrCh:
	}
	if applyErr != nil {
		_ = tunDev.Close()
		_ = abortSetup(applyErr)
		m.State.Fail(applyErr.Error())
		diag.Error("NETWORK", "ApplyTunnel failed", applyErr.Error())
		return applyErr
	}
	diag.Info("NETWORK", "VerifyTunnel begin", "")
	if err := m.Routes.VerifyTunnel(plan); err != nil {
		_ = tunDev.Close()
		_ = abortSetup(err)
		m.State.Fail(err.Error())
		diag.Error("NETWORK", "VerifyTunnel FAIL", err.Error())
		return err
	}
	diag.Info("NETWORK", "VerifyTunnel PASS", "")
	// Stickiness re-check: reject ephemeral / zero-lifetime routes before Connected.
	time.Sleep(500 * time.Millisecond)
	if err := checkTransportAlive("stickiness"); err != nil {
		_ = tunDev.Close()
		return err
	}
	if err := m.Routes.VerifyTunnel(plan); err != nil {
		_ = tunDev.Close()
		_ = abortSetup(err)
		m.State.Fail("engine: dataplane vanished after apply: " + err.Error())
		diag.Error("NETWORK", "VerifyTunnel stickiness FAIL", err.Error())
		return err
	}
	if err := alive(); err != nil {
		_ = tunDev.Close()
		_ = abortSetup(err)
		return err
	}

	var rxReceived, rxInjected, rxError atomic.Uint64
	dataHandler.Store(func(pkt []byte) error {
		rxReceived.Add(1)
		n := rxReceived.Load()
		if n <= 20 || n%100 == 0 {
			meta := parseTunnelPacketMeta(pkt)
			diag.InfoFields("PUMP", "rx_received", map[string]string{
				"src":         meta.Src.String(),
				"dst":         meta.Dst.String(),
				"protocol":    fmt.Sprintf("%d", meta.Protocol),
				"len":         fmt.Sprintf("%d", meta.Length),
				"count":       fmt.Sprintf("%d", n),
				"rx_injected": fmt.Sprintf("%d", rxInjected.Load()),
				"rx_error":    fmt.Sprintf("%d", rxError.Load()),
			})
		}
		if _, err := tunDev.Write(pkt); err != nil {
			rxError.Add(1)
			return err
		}
		rxInjected.Add(1)
		return nil
	})

	// Arm packet pumps before committing Connected.
	plan.Gate.PumpsStarted = true
	if err := plan.Gate.ErrIncomplete(); err != nil {
		_ = tunDev.Close()
		_ = abortSetup(err)
		m.State.Fail(err.Error())
		return err
	}
	if err := checkTransportAlive("pre_commit"); err != nil {
		_ = tunDev.Close()
		return err
	}

	m.mu.Lock()
	if m.opGen != gen || !m.desiredConnected {
		m.mu.Unlock()
		_ = tunDev.Close()
		_ = abortSetup(ErrStaleConnect)
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
	m.connectedAt = time.Now()
	m.mu.Unlock()
	m.State.Set(state.Connected)
	m.notifyStatus()
	diag.InfoFields("STATE", "Connected", map[string]string{"node": node.NodeID})
	log.Printf("engine: Connected node=%s — transport supervised since TypeConfig", node.NodeID)

	mtuCtrl := newTunnelMTU(typeConfigMTU, plan.TunMTU, tunAdapterName, setTunnelInterfaceMTU)
	mtuCtrl.injectICMP = func(pkt []byte) {
		if _, err := tunDev.Write(pkt); err != nil {
			diag.Warn("PUMP", "icmp_pmtu_inject_failed", err.Error())
		}
	}

	go func() {
		diag.Info("PUMP", "tx start", "Wintun→NVP")
		// Authoritative expected source = current TypeConfig via plan (not a stale cache).
		expectedSrc := canonicalVPNIP(plan.TunPrefix.Addr())
		diag.InfoFields("PUMP", "tx_filter_expected", map[string]string{
			"expected_src": expectedSrc.String(),
			"source":       "plan.TunPrefix/TypeConfig",
		})
		var (
			txTotal, txIPv4, txNonIPv4, txSpoof, txAccepted, txWriteOK, txWriteErr, txDatagramTooLarge uint64
			detailLogged                                                                              uint64
		)
		logDropDetail := func(reason string, meta tunnelPacketMeta) {
			detailLogged++
			if detailLogged > 20 && detailLogged%100 != 0 {
				return
			}
			diag.InfoFields("PUMP", "tx_drop", map[string]string{
				"reason":       reason,
				"src":          meta.Src.String(),
				"dst":          meta.Dst.String(),
				"expected_src": expectedSrc.String(),
				"protocol":     fmt.Sprintf("%d", meta.Protocol),
				"len":          fmt.Sprintf("%d", meta.Length),
				"count":        fmt.Sprintf("%d", detailLogged),
			})
		}
		logAcceptDetail := func(meta tunnelPacketMeta) {
			if txAccepted <= 20 || txAccepted%100 == 0 {
				diag.InfoFields("PUMP", "tx_accept", map[string]string{
					"src":      meta.Src.String(),
					"dst":      meta.Dst.String(),
					"protocol": fmt.Sprintf("%d", meta.Protocol),
					"len":      fmt.Sprintf("%d", meta.Length),
					"count":    fmt.Sprintf("%d", txAccepted),
				})
			}
		}
		logCounters := func(ev string) {
			diag.InfoFields("PUMP", ev, map[string]string{
				"tx_total":                fmt.Sprintf("%d", txTotal),
				"tx_ipv4":                 fmt.Sprintf("%d", txIPv4),
				"tx_non_ipv4_drop":        fmt.Sprintf("%d", txNonIPv4),
				"tx_spoof_drop":           fmt.Sprintf("%d", txSpoof),
				"tx_accepted":             fmt.Sprintf("%d", txAccepted),
				"tx_write_ok":             fmt.Sprintf("%d", txWriteOK),
				"tx_write_error":          fmt.Sprintf("%d", txWriteErr),
				"tx_datagram_too_large":   fmt.Sprintf("%d", txDatagramTooLarge),
				"effective_tunnel_mtu":    fmt.Sprintf("%d", mtuCtrl.Effective()),
			})
		}
		buf := make([]byte, 65535)
		for {
			select {
			case <-runCtx.Done():
				logCounters("tx_exit_counters")
				diag.Info("PUMP", "tx exit", "context done")
				return
			default:
			}
			n, err := tunDev.Read(buf)
			if err != nil {
				logCounters("tx_exit_counters")
				diag.Warn("PUMP", "tx exit", err.Error())
				return
			}
			if n <= 0 {
				continue
			}
			txTotal++
			cp := make([]byte, n)
			copy(cp, buf[:n])
			meta := parseTunnelPacketMeta(cp)
			if meta.Version == 4 {
				txIPv4++
			}
			if ok, reason := shouldForwardTunnelPacket(cp, expectedSrc); !ok {
				switch reason {
				case "non_ipv4":
					txNonIPv4++
				case "spoofed_source":
					txSpoof++
				}
				logDropDetail(reason, meta)
				continue
			}
			txAccepted++
			logAcceptDetail(meta)
			if err := sess.WritePacket(runCtx, cp); err != nil {
				if dtl, ok := AsDatagramTooLarge(err); ok {
					// Recoverable MTU event — drop this packet, shrink MTU, keep pump alive.
					txDatagramTooLarge++
					mtuCtrl.HandleDatagramTooLarge(cp, dtl)
					continue
				}
				txWriteErr++
				logCounters("tx_exit_counters")
				diag.Warn("PUMP", "tx exit", err.Error())
				return
			}
			txWriteOK++
			if txWriteOK == 1 || txWriteOK%100 == 0 {
				logCounters("tx_progress")
			}
		}
	}()
	go func() {
		diag.Info("PUMP", "rx start", "NVP→Wintun (OnData)")
		err := <-readErrCh
		log.Printf("engine: ReadLoop exited: %v", err)
		diag.Error("TRANSPORT", "ReadLoop EOF/exit", fmt.Sprintf("%v", err))
		m.onSessionLost(err)
	}()
	return nil
}

// Disconnect cancels Connect, pending reconnect tickets, and forbids auto-reconnect.
// Returns a non-nil error if network restore failed (journal preserved for retry).
func (m *Manager) Disconnect(ctx context.Context) error {
	return m.DisconnectWithReason(ctx, "")
}

// DisconnectWithReason is Disconnect with a GUI-visible LastError retained until the
// next clean Connected/Disconnected transition clears it via Set().
func (m *Manager) DisconnectWithReason(ctx context.Context, reason string) error {
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
	if reason != "" {
		m.State.SetDetail(state.Disconnecting, reason)
	} else {
		m.State.Set(state.Disconnecting)
	}
	restoreErr := m.teardownLocked()
	m.mu.Unlock()
	if reason != "" {
		m.State.SetDetail(state.Disconnected, reason)
	} else {
		m.State.Set(state.Disconnected)
	}
	m.notifyStatus()
	return restoreErr
}

func (m *Manager) notifyStatus() {
	if m != nil && m.OnStatusChange != nil {
		m.OnStatusChange()
	}
}

func (m *Manager) onSessionLost(readErr error) {
	reason := "session transport closed"
	if readErr != nil && !errors.Is(readErr, context.Canceled) {
		reason = "session transport closed: " + readErr.Error()
	}
	log.Printf("engine: onSessionLost: %s", reason)
	diag.Warn("SESSION", "onSessionLost", reason)

	m.mu.Lock()
	if !m.desiredConnected {
		diag.Info("TEARDOWN", "trigger", "desiredConnected=false")
		_ = m.teardownLocked()
		m.mu.Unlock()
		m.State.SetDetail(state.Disconnected, reason)
		m.notifyStatus()
		return
	}
	if m.reconnectInFlight {
		diag.Info("TEARDOWN", "trigger", "reconnect already in flight")
		_ = m.teardownLocked()
		m.mu.Unlock()
		m.State.SetDetail(state.Reconnecting, reason)
		m.notifyStatus()
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
		diag.Info("TEARDOWN", "trigger", "no ticket requester / lastConnect")
		_ = m.teardownLocked()
		m.desiredConnected = false
		m.mu.Unlock()
		m.State.SetDetail(state.Disconnected, reason)
		m.notifyStatus()
		return
	}
	m.reconnectInFlight = true
	ticketCtx, cancel := context.WithCancel(context.Background())
	m.ticketCancel = cancel
	diag.Info("TEARDOWN", "old session", "before reconnect")
	_ = m.teardownLocked()
	m.mu.Unlock()

	m.State.SetDetail(state.Reconnecting, reason)
	diag.Info("STATE", "Reconnecting", reason)
	m.notifyStatus()
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
		m.State.Fail(reason + "; reconnect ticket: " + err.Error())
		m.notifyStatus()
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
	if err := m.connect(context.Background(), creq, false); err != nil {
		if !errors.Is(err, ErrStaleConnect) && !errors.Is(err, context.Canceled) {
			m.State.Fail(reason + "; reconnect connect: " + err.Error())
			m.notifyStatus()
		}
	}
}

// TriggerSessionLostForTest invokes onSessionLost (reconnect-race unit tests only).
func TriggerSessionLostForTest(m *Manager) { m.onSessionLost(io.EOF) }

// AfterReconnectTicketForTest runs after RequestTicket returns and before reconnect Connect (tests only).
var AfterReconnectTicketForTest func()

func (m *Manager) teardownLocked() error {
	diag.Info("TEARDOWN", "begin", "pumps/session/routes/adapter")
	if m.runCancel != nil {
		m.runCancel()
		m.runCancel = nil
	}
	if m.sess != nil {
		_ = m.sess.Close(context.Background())
		m.sess = nil
		diag.Info("TEARDOWN", "session_close", "")
	}
	if m.conn != nil {
		_ = m.conn.Close()
		m.conn = nil
		diag.Info("TEARDOWN", "transport_close", "")
	}
	if m.tun != nil {
		_ = m.tun.Close()
		m.tun = nil
		diag.Info("TEARDOWN", "adapter_close", "")
	}
	var restoreErr error
	if m.plan != nil {
		diag.Info("TEARDOWN", "route_restore", "")
		restoreErr = m.Routes.Restore(m.plan)
		m.plan = nil
	}
	m.node = model.NodeRegistryEntry{}
	m.connectedAt = time.Time{}
	if restoreErr != nil {
		diag.Error("TEARDOWN", "restore_failed", restoreErr.Error())
	} else {
		diag.Info("TEARDOWN", "done", "")
	}
	return restoreErr
}

// StatusSnapshot builds an IPC status frame from the state machine.
// Includes read-only tunnel telemetry for the GUI (VPN IP, byte counters, session start).
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
	if m.conn != nil {
		snap.Transport = string(m.conn.Profile())
	}
	if m.plan != nil && m.plan.TunPrefix.IsValid() {
		snap.VpnIP = m.plan.TunPrefix.Addr().String()
		snap.EffectiveMTU = m.plan.TunMTU
		if len(m.plan.TunDNS) > 0 {
			snap.DNSServers = make([]string, len(m.plan.TunDNS))
			for i, d := range m.plan.TunDNS {
				snap.DNSServers[i] = d.String()
			}
		}
	}
	if !m.connectedAt.IsZero() && (st == state.Connected || st == state.Reconnecting) {
		snap.ConnectedAtUnix = m.connectedAt.Unix()
	}
	if m.sess != nil && (st == state.Connected || st == state.Reconnecting || st == state.Disconnecting) {
		stats := m.sess.Stats()
		snap.TxBytes = stats.SendBytes
		snap.RxBytes = stats.RecvBytes
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
	// Arm TypeConfig catcher BEFORE WaitEstablished. Production servers send
	// TypeConfig immediately after AUTH_OK while the temporary auth reader is
	// still draining the stream; without a catcher those frames are dropped.
	m.armTypeConfigCatcher(sess)
	diag.Info("AUTH", "AUTH sent", "")
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
	diag.Info("AUTH", "AUTH_OK", "ESTABLISHED")
	return sess, conn, node, nil
}

// armTypeConfigCatcher installs OnControl that buffers TypeConfig frames that
// arrive before waitTypeConfig starts its dedicated ReadLoop.
func (m *Manager) armTypeConfigCatcher(sess *session.Session) {
	cfgCh := make(chan *netcfg.Message, 1)
	errCh := make(chan error, 1)
	m.mu.Lock()
	m.earlyTypeConfig = cfgCh
	m.earlyTypeConfigErr = errCh
	m.mu.Unlock()

	sess.OnControl(func(msgType byte, payload []byte) error {
		if msgType != control.TypeConfig {
			// AUTH_OK / AUTH_FAIL / PING still handled by Session.handleMessage switch.
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
}

func (m *Manager) takeEarlyTypeConfigChannels() (cfgCh <-chan *netcfg.Message, errCh <-chan error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cfgCh = m.earlyTypeConfig
	errCh = m.earlyTypeConfigErr
	m.earlyTypeConfig = nil
	m.earlyTypeConfigErr = nil
	return cfgCh, errCh
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

	earlyCfg, earlyErr := m.takeEarlyTypeConfigChannels()
	// Non-blocking: TypeConfig may already have been buffered during WaitEstablished.
	if earlyCfg != nil {
		select {
		case msg := <-earlyCfg:
			if msg != nil {
				return msg, nil
			}
		case err := <-earlyErr:
			if err != nil {
				return nil, err
			}
		default:
		}
	}

	cfgCh := make(chan *netcfg.Message, 1)
	errCh := make(chan error, 1)

	// Keep forwarding into both the dedicated wait channels and any residual early
	// buffer path. Re-arm OnControl so frames arriving after WaitEstablished still
	// reach this waiter (SessionOpener tests may not have armed a catcher).
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
	case msg := <-earlyCfgOrNil(earlyCfg):
		stopTempTransportReader(loopCancel, loopDone, conn)
		return msg, nil
	case err := <-earlyErrOrNil(earlyErr):
		stopTempTransportReader(loopCancel, loopDone, conn)
		return nil, err
	case msg := <-typeConfigTestSignal:
		if msg != nil {
			stopTempTransportReader(loopCancel, loopDone, conn)
			return msg, nil
		}
		stopTempTransportReader(loopCancel, loopDone, conn)
		return nil, fmt.Errorf("engine: TypeConfig test signal nil")
	}
}

func earlyCfgOrNil(ch <-chan *netcfg.Message) <-chan *netcfg.Message {
	if ch != nil {
		return ch
	}
	return nil
}

func earlyErrOrNil(ch <-chan error) <-chan error {
	if ch != nil {
		return ch
	}
	return nil
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

// waitCatalogNotBefore sleeps until IssuedAt if the catalog is slightly not-yet-valid
// due to clock skew (within maxSkew). Does not alter expires_at or Frozen Core.
func waitCatalogNotBefore(issued time.Time, maxSkew time.Duration) {
	now := time.Now().UTC()
	if !now.Before(issued) {
		return
	}
	d := issued.Sub(now)
	if d <= 0 || d > maxSkew {
		return
	}
	time.Sleep(d + time.Millisecond)
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
