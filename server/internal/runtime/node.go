// Package runtime is the Nyxveil VPN node process lifecycle.
package runtime

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"log"
	"math/big"
	mrand "math/rand"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nyxveil/nvp/core/auth/ticket"
	"github.com/nyxveil/nvp/core/transport/ech"
	"github.com/nyxveil/server/internal/catalogverify"
	"github.com/nyxveil/server/internal/configure"
	"github.com/nyxveil/server/internal/controlplane"
	"github.com/nyxveil/server/internal/controlsock"
	"github.com/nyxveil/server/internal/datapath"
	"github.com/nyxveil/server/internal/health"
	"github.com/nyxveil/server/internal/identity"
	"github.com/nyxveil/server/internal/listeners"
	"github.com/nyxveil/server/internal/localconfig"
	"github.com/nyxveil/server/internal/metrics"
	"github.com/nyxveil/server/internal/nodeauth"
	"github.com/nyxveil/server/internal/nodetls"
	"github.com/nyxveil/server/internal/paths"
	"github.com/nyxveil/server/internal/revocation"
	"github.com/nyxveil/server/internal/sessions"
	"github.com/nyxveil/server/internal/ticketkeys"
	"github.com/nyxveil/server/internal/tun"
	"github.com/nyxveil/server/internal/version"
)

// Options configures Node construction.
type Options struct {
	ConfigPath   string
	KeyPath      string
	AppliedPath  string
	ControlSock  string
	ControlHTTP  string // Windows / test fallback
	SkipTUN      bool   // only when explicit flag / tests
	TestMode     bool   // allow register without PublicHost
	TicketIssuer string
	TicketAud    string
	CPPublicKeys map[string]ed25519.PublicKey
}

// Node is the main VPN node runtime.
type Node struct {
	opts Options

	mu         sync.RWMutex
	local      *localconfig.File
	applied    controlplane.NodeConfig
	key        *identity.NodeKey
	keyCreated bool // true if LoadOrCreate generated a fresh key this process
	cp         *controlplane.Client
	sessions   *sessions.Manager
	rev        *revocation.Cache
	bridge     *datapath.Bridge
	listen     *listeners.Server
	ctl        *controlsock.Server
	sampler    *metrics.Sampler
	tunDev     *tun.Device

	ticketKeysPath string
	echKeys        *ech.KeySet
	requireECH     bool
	enableTLS      bool
	enableQUIC     bool

	startedAt        time.Time
	running          atomic.Bool
	cpOK             atomic.Bool
	accepting        atomic.Bool
	enabled          atomic.Bool
	draining         atomic.Bool
	maintenance      atomic.Bool
	versionBlocked   atomic.Bool
	ticketKeysLoaded atomic.Bool
	tunReady         atomic.Bool
	bridgeOK         atomic.Bool

	lastTicketRefresh time.Time
	hbFailStreak      int

	cpMu                 sync.RWMutex
	lastCPSuccess        time.Time
	lastHeartbeatSuccess time.Time
	lastConfigSuccess    time.Time
	lastTicketKeysOK     time.Time
	lastRevocationOK     time.Time
	cpLastError          string
	cpTLSMode            string

	renewMu               sync.RWMutex
	lastRenewalAttempt    time.Time
	lastSuccessfulRenewal time.Time
	nextPlannedRenewal    time.Time
	lastRenewalError      string
	acmeIssuer            func(context.Context, nodetls.ACMEConfig) (tls.Certificate, []byte, []byte, bool, error)
	advertiseSPKI         func(context.Context, []byte) error
	verifyCatalogSPKI     func(context.Context, []byte) error
	commitTLS             func(string, string, string, string) error
	reloadTLS             func(tls.Certificate) error
	verifyServedSPKI      func(context.Context, []byte) error
	validateStagedTLS     func(string, string, string, time.Time) error

	commandStore *commandDedupeStore

	cancel context.CancelFunc
	runCtx context.Context
	wg     sync.WaitGroup
}

// New loads identity and local config; does not start loops.
func New(opts Options) (*Node, error) {
	if opts.ConfigPath == "" {
		opts.ConfigPath = paths.ServerConfig()
	}
	if opts.KeyPath == "" {
		opts.KeyPath = paths.NodeKey()
	}
	if opts.AppliedPath == "" {
		opts.AppliedPath = paths.AppliedConfig()
	}
	if opts.ControlSock == "" {
		// TestMode + ControlHTTP: force loopback HTTP so process tests can
		// drive nyxveilctl via NYXVEIL_CONTROL_HTTP (no unix socket).
		if !(opts.TestMode && strings.TrimSpace(opts.ControlHTTP) != "") {
			opts.ControlSock = paths.ControlSocket()
		}
	}
	if opts.TicketIssuer == "" {
		opts.TicketIssuer = "nyxveil-control-plane"
	}
	if opts.TicketAud == "" {
		opts.TicketAud = "nvp-node"
	}

	cfg, err := localconfig.Load(opts.ConfigPath)
	if err != nil {
		return nil, err
	}

	keysPath := ticketkeys.PathBesideKey(opts.KeyPath)
	if iss, keys, err := ticketkeys.Load(keysPath); err == nil {
		opts.TicketIssuer = iss
		opts.CPPublicKeys = keys
	}
	key, created, err := identity.LoadOrCreate(opts.KeyPath)
	if err != nil {
		return nil, err
	}

	cp, tlsRes, err := controlplane.NewClientWithTLS(controlplane.TLSOptions{
		BaseURL:      cfg.ControlPlaneURL,
		SPKIPinHex:   cfg.ControlPlaneSPKIPin,
		PinnedCAFile: cfg.PinnedCAFile,
	})
	if err != nil {
		return nil, err
	}
	_ = tlsRes
	cp.NodeID = cfg.NodeID
	cp.PrivateKey = key.Private

	capN := 100
	mgr, err := sessions.New(capN, cfg.VPNSubnetCIDR)
	if err != nil {
		return nil, err
	}
	n := &Node{
		opts:           opts,
		local:          cfg,
		key:            key,
		keyCreated:     created,
		cp:             cp,
		sessions:       mgr,
		rev:            revocation.New(),
		sampler:        metrics.NewSampler(),
		ticketKeysPath: keysPath,
		enableTLS:      true,
		enableQUIC:     true,
	}
	if len(opts.CPPublicKeys) > 0 {
		n.ticketKeysLoaded.Store(true)
	}
	n.enabled.Store(true)
	n.accepting.Store(true)
	if snap, err := localconfig.LoadApplied(opts.AppliedPath); err == nil {
		n.applied = snap.Config
		n.mergeAppliedIntoLocalLocked(snap.Config)
		n.applyFlags(snap.Config)
		if snap.Config.Capacity > 0 {
			mgr.SetCapacity(snap.Config.Capacity)
		}
		n.enableTLS, n.enableQUIC = parseTransportPolicy(snap.Config.TransportPolicyJSON)
	}
	return n, nil
}

// mergeAppliedIntoLocalLocked overlays dynamic CP fields onto in-memory local
// config. Does not write /etc/nyxveil/server.json. Caller must hold n.mu or be
// in New before concurrent access.
func (n *Node) mergeAppliedIntoLocalLocked(cfg controlplane.NodeConfig) {
	if cfg.LocationID != "" {
		n.local.LocationID = cfg.LocationID
	}
	if cfg.ConfigVersion > 0 {
		n.local.ConfigVersion = cfg.ConfigVersion
	}
}

// Register bootstraps or re-registers the node with Control Plane.
func (n *Node) Register(ctx context.Context, bootstrapToken string) (*controlplane.RegisterResponse, error) {
	return n.register(ctx, bootstrapToken, nil, true)
}

// RegisterWithSPKI advertises an explicitly supplied staged SPKI without
// requiring that staged material be activated on disk first.
func (n *Node) RegisterWithSPKI(ctx context.Context, spkiPin []byte) (*controlplane.RegisterResponse, error) {
	if len(spkiPin) == 0 {
		return nil, errors.New("runtime: SPKI override is empty")
	}
	return n.register(ctx, "", append([]byte(nil), spkiPin...), false)
}

func (n *Node) register(ctx context.Context, bootstrapToken string, spkiOverride []byte, requireBootstrap bool) (*controlplane.RegisterResponse, error) {
	n.mu.RLock()
	cfg := *n.local
	key := n.key
	freshKey := n.keyCreated
	n.mu.RUnlock()

	if cfg.PublicHost == "" && !n.opts.TestMode {
		return nil, errors.New("runtime: public_host required for production register (set public_host or TestMode)")
	}

	req := controlplane.RegisterRequest{
		NodeID:          cfg.NodeID,
		LocationID:      cfg.LocationID,
		DisplayName:     cfg.DisplayName,
		PublicIdentity:  key.PublicIdentity(),
		PublicKey:       append([]byte(nil), key.Public...),
		ServerName:      cfg.ServerName,
		ProtocolVersion: version.ProtocolNumber,
		ServerVersion:   version.ServerVersion,
		Capacity:        n.sessions.Capacity(),
		TestOnly:        n.opts.TestMode,
		Endpoints:       nil,
	}
	if cfg.ServerName == "" && cfg.PublicHost != "" {
		req.ServerName = cfg.PublicHost
	}
	// Operator Register always requires a bootstrap token (fresh install and repair).
	// Existing local key additionally proves possession via NodeToken (PoP).
	// RegisterWithSPKI (internal) may omit bootstrap when identity is already committed.
	token := strings.TrimSpace(bootstrapToken)
	if requireBootstrap && token == "" {
		return nil, errors.New("runtime: bootstrap token required for registration")
	}
	if token != "" {
		req.BootstrapToken = token
	}
	if !freshKey {
		req.NodeToken = nodeauth.SignCoreNodeToken(cfg.NodeID, key.Private, time.Now().Unix())
	}
	if req.BootstrapToken == "" && req.NodeToken == "" {
		return nil, errors.New("runtime: bootstrap token required for registration")
	}

	if len(spkiOverride) > 0 {
		req.SPKIPin = append([]byte(nil), spkiOverride...)
	} else if cert, err := n.loadTLSCert(ctx, cfg); err == nil {
		if pin, err := SPKIPinSHA256(cert); err == nil {
			req.SPKIPin = pin
		}
	}

	tlsPort := listenPort(cfg.TLSListen, 443)
	quicPort := listenPort(cfg.QUICListen, 443)
	host := cfg.PublicHost
	af := "hostname"
	if host != "" {
		if ip := net.ParseIP(host); ip != nil {
			if ip.To4() != nil {
				af = "ipv4"
			} else {
				af = "ipv6"
			}
		}
		// One catalog endpoint per (host,port). TLS+QUIC sharing :443 must not
		// create duplicate identical endpoint rows; profiles come from transports.
		if tlsPort == quicPort {
			req.Endpoints = append(req.Endpoints,
				controlplane.Endpoint{Host: host, Port: tlsPort, AddressFamily: af, Priority: 1, Enabled: true},
			)
		} else {
			req.Endpoints = append(req.Endpoints,
				controlplane.Endpoint{Host: host, Port: tlsPort, AddressFamily: af, Priority: 1, Enabled: true},
				controlplane.Endpoint{Host: host, Port: quicPort, AddressFamily: af, Priority: 2, Enabled: true},
			)
		}
	}

	resp, err := n.cp.Register(ctx, req)
	bootstrapToken = "" // scrub local copy
	_ = bootstrapToken
	if err != nil {
		// HTTP 2xx then decode failure: CP already committed registration/bootstrap.
		// Keep node.key and clear fresh-key so retry uses the same identity + PoP.
		if controlplane.IsAcceptedLocal(err) {
			n.markIdentityCommittedForRetry()
			return nil, fmt.Errorf("%w\nWARNING: Control Plane may already have registered this node; node.key preserved — retry with the same identity/PoP (do not mint a new key)", err)
		}
		return nil, err
	}
	if err := n.PersistRegistrationConfig(resp); err != nil {
		n.markIdentityCommittedForRetry()
		return nil, fmt.Errorf("runtime: persist after Control Plane registration accepted: %w\nWARNING: Control Plane registration likely succeeded; node.key preserved — retry with the same identity/PoP (do not mint a new key)", err)
	}
	n.markIdentityCommittedForRetry()
	return resp, nil
}

// markIdentityCommittedForRetry clears the in-process "fresh key" flag so a
// subsequent Register in this process uses Core PoP instead of bootstrap.
// The key file on disk is never deleted here.
func (n *Node) markIdentityCommittedForRetry() {
	n.mu.Lock()
	n.keyCreated = false
	n.mu.Unlock()
}

// PersistRegistrationConfig writes the Control Plane's initial/repair config
// snapshot to applied-config.json. Preserves a newer local applied snapshot
// (repair) unless the response carries an equal-or-newer ConfigVersion.
func (n *Node) PersistRegistrationConfig(resp *controlplane.RegisterResponse) error {
	if resp == nil {
		return errors.New("runtime: nil register response")
	}
	n.mu.RLock()
	expectID := n.local.NodeID
	appliedPath := n.opts.AppliedPath
	n.mu.RUnlock()

	if resp.NodeID != "" && expectID != "" && resp.NodeID != expectID {
		return fmt.Errorf("runtime: register node_id mismatch: got %q want %q", resp.NodeID, expectID)
	}

	var cfg controlplane.NodeConfig
	if resp.Config != nil {
		cfg = *resp.Config
	} else if resp.ConfigVersion > 0 {
		cfg = controlplane.NodeConfig{
			NodeID:        resp.NodeID,
			ConfigVersion: resp.ConfigVersion,
			Enabled:       true,
		}
	} else {
		return nil
	}
	if cfg.NodeID == "" {
		cfg.NodeID = resp.NodeID
	}
	if cfg.NodeID == "" {
		cfg.NodeID = expectID
	}
	if cfg.ConfigVersion == 0 {
		cfg.ConfigVersion = resp.ConfigVersion
	}

	if existing, err := localconfig.LoadApplied(appliedPath); err == nil {
		if existing.Config.ConfigVersion > cfg.ConfigVersion {
			// Repair: keep newer authoritative applied snapshot.
			return nil
		}
	}

	if err := localconfig.SaveApplied(appliedPath, cfg); err != nil {
		return fmt.Errorf("runtime: persist registration config: %w", err)
	}
	n.mu.Lock()
	n.applied = cfg
	n.mergeAppliedIntoLocalLocked(cfg)
	n.applyFlags(cfg)
	if cfg.Capacity > 0 {
		n.sessions.SetCapacity(cfg.Capacity)
	}
	n.enableTLS, n.enableQUIC = parseTransportPolicy(cfg.TransportPolicyJSON)
	n.mu.Unlock()
	return nil
}

// Start runs heartbeat, revocation sync, listeners, datapath, and control socket.
func (n *Node) Start(parent context.Context) error {
	if n.running.Swap(true) {
		return errors.New("runtime: already running")
	}
	ctx, cancel := context.WithCancel(parent)
	n.cancel = cancel
	n.runCtx = ctx
	n.startedAt = time.Now()

	n.mu.RLock()
	cfg := *n.local
	n.mu.RUnlock()

	cert, err := n.loadTLSCert(ctx, cfg)
	if err != nil {
		n.running.Store(false)
		cancel()
		return err
	}

	mtu := 1420
	if n.applied.MTU != nil && *n.applied.MTU > 0 {
		mtu = *n.applied.MTU
	}

	var bridge *datapath.Bridge
	if !n.opts.SkipTUN {
		nodeAddr, err := sessions.NodeAddress(cfg.VPNSubnetCIDR)
		if err != nil {
			n.running.Store(false)
			cancel()
			return err
		}
		dev, err := tun.Open("nyxveil0", nodeAddr.String(), mtu)
		if err != nil {
			n.running.Store(false)
			cancel()
			return fmt.Errorf("runtime: TUN open failed (fail-closed): %w", err)
		}
		n.tunDev = dev
		n.tunReady.Store(true)
		bridge = datapath.New(n.sessions, dev, 64)
		if err := bridge.Start(ctx); err != nil {
			_ = dev.Close()
			n.tunReady.Store(false)
			n.running.Store(false)
			cancel()
			return fmt.Errorf("runtime: bridge start failed (fail-closed): %w", err)
		}
		n.bridge = bridge
		n.bridgeOK.Store(true)
	}

	// Best-effort ticket key fetch before accepting (required for health).
	if err := n.fetchTicketKeys(ctx); err != nil {
		log.Printf("runtime: ticket keys fetch: %v", err)
	}

	requireECH, wantECH := parseECHPolicy(n.applied.ECHPolicyJSON)
	n.requireECH = requireECH
	if wantECH {
		if err := n.ensureECHKeys(cfg); err != nil {
			log.Printf("runtime: ECH keys: %v", err)
		}
	}

	verifier := ticket.VerifierConfig{
		Issuer:     n.opts.TicketIssuer,
		Audience:   n.opts.TicketAud,
		PublicKeys: n.copyPublicKeys(),
		Revoked:    n.rev,
	}

	lnCfg := listeners.Config{
		TLSAddr:    cfg.TLSListen,
		QUICAddr:   cfg.QUICListen,
		Cert:       cert,
		NodeID:     cfg.NodeID,
		LocationID: cfg.LocationID,
		Verifier:   verifier,
		MTU:        mtu,
		ECHKeys:    n.echKeys,
		RequireECH: n.requireECH,
		EnableTLS:  n.enableTLS,
		EnableQUIC: n.enableQUIC,
		SubnetCIDR: cfg.VPNSubnetCIDR,
		DNSServers: append([]string(nil), cfg.DNSServers...),
	}
	srv := listeners.New(lnCfg, n, n.sessions, bridge)
	srv.SetUnknownKIDHook(func(kid string) {
		n.refreshTicketKeysRateLimited(context.Background())
		_ = kid
	})
	if n.Accepting() {
		if err := srv.Start(ctx); err != nil {
			n.Shutdown(context.Background())
			return fmt.Errorf("runtime: listeners: %w", err)
		}
	}
	n.listen = srv

	ctl := &controlsock.Server{
		SocketPath: n.opts.ControlSock,
		HTTPAddr:   n.opts.ControlHTTP,
		Status:     func() any { return n.Status() },
		Health: func() any {
			st := n.Status()
			return map[string]any{"healthy": st.Healthy, "running": st.Running}
		},
	}
	if err := ctl.Start(ctx); err != nil {
		log.Printf("runtime: control socket: %v", err)
	} else {
		n.ctl = ctl
	}

	n.wg.Add(1)
	go n.loop(ctx)
	n.wg.Add(1)
	go n.commandPollLoop(ctx)
	if strings.TrimSpace(cfg.ACMEDomain) != "" {
		n.wg.Add(1)
		go n.acmeRenewLoop(ctx)
	}
	return nil
}

// ControlHTTPAddr returns the loopback control HTTP host:port when bound
// (TestMode / Windows). Empty when using a Unix control socket only.
func (n *Node) ControlHTTPAddr() string {
	if n == nil || n.ctl == nil {
		return ""
	}
	addr := n.ctl.Addr()
	if strings.Contains(addr, "/") || strings.HasPrefix(addr, "@") {
		return ""
	}
	return addr
}

// Accepting implements listeners.Gate.
func (n *Node) Accepting() bool {
	return n.accepting.Load() && n.enabled.Load() && !n.draining.Load() && !n.maintenance.Load() && !n.versionBlocked.Load()
}

// Available implements listeners.Gate.
func (n *Node) Available() bool {
	return n.sessions.Available()
}

// Shutdown stops listeners, datapath, and background loops with a hard bound.
// Every wait is timed; never block past ctx / internal phase budgets (well under
// systemd TimeoutStopSec=20).
func (n *Node) Shutdown(ctx context.Context) error {
	if !n.running.Swap(false) {
		return nil
	}
	log.Printf("shutdown phase=signal")
	n.accepting.Store(false)

	phase := func(name string, fn func()) {
		log.Printf("shutdown phase=%s", name)
		fn()
	}

	phase("sessions_closing", func() {
		shCtx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()
		n.sessions.CloseAll(shCtx)
	})

	phase("listeners_closed", func() {
		if n.listen != nil {
			n.listen.StopWithTimeout(1500 * time.Millisecond)
		}
	})

	phase("control_socket_stopped", func() {
		if n.ctl != nil {
			_ = n.ctl.Stop()
		}
	})

	phase("context_cancelled", func() {
		if n.cancel != nil {
			n.cancel()
		}
	})

	phase("tun_closed", func() {
		if n.tunDev != nil {
			_ = n.tunDev.Close()
			n.tunReady.Store(false)
		}
	})

	phase("bridge_stopped", func() {
		if n.bridge != nil {
			n.bridge.StopWithTimeout(1500 * time.Millisecond)
			n.bridgeOK.Store(false)
		}
	})

	phase("workers_wait", func() {
		done := make(chan struct{})
		go func() {
			n.wg.Wait()
			close(done)
		}()
		waitCtx := ctx
		if waitCtx == nil {
			var cancel context.CancelFunc
			waitCtx, cancel = context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
		}
		select {
		case <-done:
		case <-waitCtx.Done():
			log.Printf("shutdown phase=workers_wait timed out")
		}
	})

	log.Printf("shutdown phase=complete")
	return nil
}

// Status builds a health.Status snapshot.
func (n *Node) Status() health.Status {
	cpu, memPct, memBytes, _, _ := n.sampler.Sample()
	n.mu.RLock()
	cfg := *n.local
	applied := n.applied
	n.mu.RUnlock()
	st := health.Status{
		Running:          n.running.Load(),
		NodeID:           cfg.NodeID,
		LocationID:       cfg.LocationID,
		CPConnected:      n.cpOK.Load(),
		CPURL:            strings.TrimSpace(cfg.ControlPlaneURL),
		Sessions:         n.sessions.Count(),
		Capacity:         n.sessions.Capacity(),
		CPUUsage:         cpu,
		MemoryUsage:      memPct,
		MemoryBytes:      memBytes,
		ConfigVersion:    applied.ConfigVersion,
		Enabled:          n.enabled.Load(),
		Draining:         n.draining.Load(),
		MaintenanceMode:  n.maintenance.Load(),
		Accepting:        n.Accepting(),
		RevocationStale:  n.rev.Stale(),
		TUNReady:         n.tunReady.Load() || n.opts.SkipTUN,
		BridgeOK:         n.bridgeOK.Load() || n.opts.SkipTUN,
		SkipTUN:          n.opts.SkipTUN,
		TicketKeysLoaded: n.ticketKeysLoaded.Load(),
		IdentityPresent:  n.key != nil && len(n.key.Public) == ed25519.PublicKeySize,
		VersionBlocked:   n.versionBlocked.Load(),
	}
	n.cpMu.RLock()
	st.CPTLSMode = n.cpTLSMode
	if st.CPTLSMode != "" {
		st.CPTLSStatus = "ok"
	}
	if !n.lastCPSuccess.IsZero() {
		st.LastCPSuccess = n.lastCPSuccess.UTC().Format(time.RFC3339)
	}
	if !n.lastHeartbeatSuccess.IsZero() {
		st.LastHeartbeatOK = n.lastHeartbeatSuccess.UTC().Format(time.RFC3339)
	}
	if !n.lastConfigSuccess.IsZero() {
		st.LastConfigOK = n.lastConfigSuccess.UTC().Format(time.RFC3339)
	}
	if !n.lastTicketKeysOK.IsZero() {
		st.LastTicketKeysOK = n.lastTicketKeysOK.UTC().Format(time.RFC3339)
	}
	if !n.lastRevocationOK.IsZero() {
		st.LastRevocationOK = n.lastRevocationOK.UTC().Format(time.RFC3339)
	}
	st.CPLastError = n.cpLastError
	if !st.CPConnected && st.CPLastError != "" {
		st.CPTLSStatus = "error"
	}
	n.cpMu.RUnlock()
	if n.cp != nil && n.cp.TLS != nil {
		st.CPTLSMode = string(n.cp.TLS.TrustMode)
		if st.CPTLSStatus == "" {
			st.CPTLSStatus = "configured"
		}
	}
	if !n.startedAt.IsZero() {
		st.UptimeSeconds = int64(time.Since(n.startedAt).Seconds())
	}
	if n.listen != nil {
		st.TLSOK = n.listen.TLSOK()
		st.QUICOK = n.listen.QUICOK()
	}
	st.DefaultVersions()
	st.Healthy = st.ComputeHealthy()
	return st
}

func (n *Node) loop(ctx context.Context) {
	defer n.wg.Done()
	hbSec := n.local.HeartbeatSec
	if hbSec <= 0 {
		hbSec = 30
	}
	baseHB := time.Duration(hbSec) * time.Second
	nextHB := time.NewTimer(0) // fire immediately
	revTicker := time.NewTicker(60 * time.Second)
	keysTicker := time.NewTicker(time.Hour)
	defer nextHB.Stop()
	defer revTicker.Stop()
	defer keysTicker.Stop()

	_ = n.syncRevocation(ctx)
	_ = n.fetchTicketKeys(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-nextHB.C:
			err := n.heartbeat(ctx)
			delay := baseHB
			if err != nil {
				n.hbFailStreak++
				delay = heartbeatBackoff(n.hbFailStreak, baseHB)
			} else {
				n.hbFailStreak = 0
			}
			nextHB.Reset(delay)
		case <-revTicker.C:
			_ = n.syncRevocation(ctx)
		case <-keysTicker.C:
			_ = n.fetchTicketKeys(ctx)
		}
	}
}

func heartbeatBackoff(streak int, base time.Duration) time.Duration {
	if streak < 1 {
		streak = 1
	}
	// exponential: base * 2^(streak-1), capped at 5 minutes, plus jitter.
	mult := 1 << uint(min(streak-1, 4))
	d := base * time.Duration(mult)
	if d > 5*time.Minute {
		d = 5 * time.Minute
	}
	jitter := time.Duration(mrand.Int63n(int64(d/4) + 1))
	return d + jitter
}

func (n *Node) heartbeat(ctx context.Context) error {
	st := n.Status()
	cpu := st.CPUUsage
	memPct := st.MemoryUsage
	memBytes := st.MemoryBytes
	rx, tx := n.sampler.Rates()
	up := int64(time.Since(n.startedAt).Seconds())
	healthy := st.Healthy
	hb := controlplane.HeartbeatRequest{
		Capacity:        n.sessions.Capacity(),
		CurrentSessions: n.sessions.Count(),
		MemoryUsage:     &memPct,
		MemoryBytes:     &memBytes,
		Uptime:          &up,
		NetworkRxRate:   &rx,
		NetworkTxRate:   &tx,
		Healthy:         &healthy,
	}
	if n.sampler.Ready() {
		hb.Load = cpu / 100
		hb.CPUUsage = &cpu
	}
	n.addHeartbeatMetadata(&hb, st)
	resp, err := n.cp.Heartbeat(ctx, hb)
	if err != nil {
		n.cpOK.Store(false)
		n.recordCPError(err)
		return err
	}
	n.cpOK.Store(true)
	n.recordCPSuccess("heartbeat")
	n.mu.RLock()
	curVer := n.applied.ConfigVersion
	if curVer == 0 {
		curVer = n.local.ConfigVersion
	}
	n.mu.RUnlock()
	if resp.ConfigVersion > curVer {
		if err := n.pullAndApplyConfig(ctx); err != nil {
			log.Printf("runtime: config pull: %v", err)
		}
	}
	return nil
}

func (n *Node) recordCPSuccess(kind string) {
	now := time.Now().UTC()
	n.cpMu.Lock()
	defer n.cpMu.Unlock()
	n.lastCPSuccess = now
	n.cpLastError = ""
	if n.cp != nil && n.cp.TLS != nil {
		n.cpTLSMode = string(n.cp.TLS.TrustMode)
	}
	switch kind {
	case "heartbeat":
		n.lastHeartbeatSuccess = now
	case "config":
		n.lastConfigSuccess = now
	case "ticket_keys":
		n.lastTicketKeysOK = now
	case "revocation":
		n.lastRevocationOK = now
	}
}

func (n *Node) recordCPError(err error) {
	if err == nil {
		return
	}
	msg := err.Error()
	// Sanitize obvious secrets / tokens from error text.
	msg = strings.ReplaceAll(msg, "\n", " ")
	if len(msg) > 240 {
		msg = msg[:240] + "…"
	}
	n.cpMu.Lock()
	n.cpLastError = msg
	if n.cp != nil && n.cp.TLS != nil {
		n.cpTLSMode = string(n.cp.TLS.TrustMode)
	}
	n.cpMu.Unlock()
}

func (n *Node) addHeartbeatMetadata(hb *controlplane.HeartbeatRequest, st health.Status) {
	if hb == nil {
		return
	}
	n.mu.RLock()
	cfg := *n.local
	n.mu.RUnlock()
	certFile, keyFile := configure.DefaultTLSPaths(cfg.TLSCertFile, cfg.TLSKeyFile)
	info := configure.InspectCert(certFile, keyFile, strings.TrimSpace(cfg.ACMEDomain))
	hb.TLSMode = info.TLSMode
	hb.CertSubject = info.Subject
	hb.CertIssuer = info.Issuer
	hb.CertSAN = strings.Join(info.SANs, ",")
	hb.CertNotBefore = info.NotBefore
	hb.CertNotAfter = info.NotAfter
	hb.CertThumbprint = info.Thumbprint
	acmeAutoRenew := strings.TrimSpace(cfg.ACMEDomain) != ""
	hb.ACMEAutoRenew = boolPtr(acmeAutoRenew)
	hb.TUNReady = boolPtr(st.TUNReady)
	hb.TLSOK = boolPtr(st.TLSOK)
	hb.QUICOK = boolPtr(st.QUICOK)
	hb.BridgeOK = boolPtr(st.BridgeOK)
	hb.TicketKeysLoaded = boolPtr(st.TicketKeysLoaded)
	hb.RevocationStale = boolPtr(st.RevocationStale)
	hb.CPConnected = boolPtr(st.CPConnected)
	hb.SupportsCommands = boolPtr(true)
	hb.ManagementCapabilities = managementCapabilitiesList
	hb.BootID = readBootID()

	n.renewMu.RLock()
	hb.LastRenewalAttempt = formatOptionalTime(n.lastRenewalAttempt)
	hb.LastSuccessfulRenewal = formatOptionalTime(n.lastSuccessfulRenewal)
	hb.NextPlannedRenewal = formatOptionalTime(n.nextPlannedRenewal)
	hb.LastRenewalError = n.lastRenewalError
	n.renewMu.RUnlock()
}

func boolPtr(v bool) *bool { return &v }

func formatOptionalTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func (n *Node) syncRevocation(ctx context.Context) error {
	snap, err := n.cp.GetRevocation(ctx)
	if err != nil {
		n.recordCPError(err)
		return err
	}
	n.rev.Apply(*snap)
	n.recordCPSuccess("revocation")
	return nil
}

func (n *Node) pullAndApplyConfig(ctx context.Context) error {
	cfg, err := n.cp.GetConfig(ctx)
	if err != nil {
		n.recordCPError(err)
		return err
	}
	if err := n.ApplyConfig(*cfg); err != nil {
		n.recordCPError(err)
		return err
	}
	n.recordCPSuccess("config")
	return nil
}

// ApplyConfig atomically applies a Control Plane node config.
// Dynamic state is persisted only to applied-config.json (under StateDir).
// /etc/nyxveil/server.json is never written by the daemon.
func (n *Node) ApplyConfig(cfg controlplane.NodeConfig) error {
	n.mu.RLock()
	wasAccepting := n.Accepting()
	appliedPath := n.opts.AppliedPath
	n.mu.RUnlock()

	// Persist first — if this fails, in-memory state and ConfigVersion stay unchanged.
	if err := localconfig.SaveApplied(appliedPath, cfg); err != nil {
		return fmt.Errorf("runtime: save applied config: %w", err)
	}

	n.mu.Lock()
	n.applied = cfg
	n.mergeAppliedIntoLocalLocked(cfg)
	if cfg.Capacity > 0 {
		n.sessions.SetCapacity(cfg.Capacity)
	}
	n.applyFlags(cfg)
	n.enableTLS, n.enableQUIC = parseTransportPolicy(cfg.TransportPolicyJSON)
	require, wantECH := parseECHPolicy(cfg.ECHPolicyJSON)
	n.requireECH = require

	localCopy := *n.local
	nowAccepting := n.Accepting()
	listen := n.listen
	mtu := 1420
	if cfg.MTU != nil && *cfg.MTU > 0 {
		mtu = *cfg.MTU
	}
	n.mu.Unlock()

	if wantECH {
		if err := n.ensureECHKeys(localCopy); err != nil {
			log.Printf("runtime: ECH after apply: %v", err)
		}
	} else {
		n.mu.Lock()
		n.echKeys = nil
		n.requireECH = false
		n.mu.Unlock()
	}

	if listen != nil {
		listen.UpdateAuth(localCopy.NodeID, localCopy.LocationID, ticket.VerifierConfig{
			Issuer:     n.opts.TicketIssuer,
			Audience:   n.opts.TicketAud,
			PublicKeys: n.copyPublicKeys(),
			Revoked:    n.rev,
		})
		listen.SetTransports(n.enableTLS, n.enableQUIC)
		n.mu.RLock()
		ek, req := n.echKeys, n.requireECH
		n.mu.RUnlock()
		listen.UpdateECH(ek, req)
		_ = mtu

		if !wasAccepting && nowAccepting {
			if !listen.Running() {
				if n.runCtx != nil {
					if err := listen.Start(n.runCtx); err != nil {
						log.Printf("runtime: start listeners on re-enable: %v", err)
					}
				}
			} else if err := listen.Reconcile(n.runCtx); err != nil {
				log.Printf("runtime: reconcile listeners: %v", err)
			}
		} else if wasAccepting && !nowAccepting {
			// Stop accepting via gate; keep listeners or stop — gate is sufficient.
		} else if nowAccepting {
			if err := listen.Reconcile(n.runCtx); err != nil {
				log.Printf("runtime: reconcile listeners: %v", err)
			}
		}
	}
	return nil
}

func (n *Node) applyFlags(cfg controlplane.NodeConfig) {
	// ConfigVersion==0 means no CP config yet → treat as enabled.
	enabled := cfg.Enabled || cfg.ConfigVersion == 0
	n.enabled.Store(enabled)
	n.draining.Store(cfg.Draining)
	n.maintenance.Store(cfg.MaintenanceMode)

	blocked := false
	if cfg.MinimumServerVersion != nil && *cfg.MinimumServerVersion != "" {
		if compareSemverApprox(version.ServerVersion, *cfg.MinimumServerVersion) < 0 {
			blocked = true
		}
	}
	if cfg.MinimumProtocolVersion != nil && version.ProtocolNumber < *cfg.MinimumProtocolVersion {
		blocked = true
	}
	n.versionBlocked.Store(blocked)
	n.accepting.Store(enabled && !cfg.Draining && !cfg.MaintenanceMode && !blocked)
}

func (n *Node) copyPublicKeys() map[string]ed25519.PublicKey {
	n.mu.RLock()
	defer n.mu.RUnlock()
	src := n.opts.CPPublicKeys
	if src == nil {
		return map[string]ed25519.PublicKey{}
	}
	out := make(map[string]ed25519.PublicKey, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

func (n *Node) fetchTicketKeys(ctx context.Context) error {
	resp, err := n.cp.GetTicketKeys(ctx)
	if err != nil {
		n.recordCPError(err)
		return err
	}
	keys, err := ticketkeys.DecodeKeys(resp.Keys)
	if err != nil {
		n.recordCPError(err)
		return err
	}
	if len(keys) == 0 {
		err := errors.New("runtime: ticket keys response empty")
		n.recordCPError(err)
		return err
	}
	issuer := resp.Issuer
	if issuer == "" {
		issuer = n.opts.TicketIssuer
	}
	if err := ticketkeys.Save(n.ticketKeysPath, issuer, keys, resp.UpdatedAt); err != nil {
		n.recordCPError(err)
		return err
	}
	n.mu.Lock()
	n.opts.TicketIssuer = issuer
	n.opts.CPPublicKeys = keys
	n.lastTicketRefresh = time.Now()
	n.mu.Unlock()
	n.ticketKeysLoaded.Store(true)
	if n.listen != nil {
		n.listen.UpdateAuth(n.local.NodeID, n.local.LocationID, ticket.VerifierConfig{
			Issuer:     issuer,
			Audience:   n.opts.TicketAud,
			PublicKeys: n.copyPublicKeys(),
			Revoked:    n.rev,
		})
	}
	n.recordCPSuccess("ticket_keys")
	return nil
}

func (n *Node) refreshTicketKeysRateLimited(ctx context.Context) {
	n.mu.Lock()
	if time.Since(n.lastTicketRefresh) < time.Minute {
		n.mu.Unlock()
		return
	}
	n.lastTicketRefresh = time.Now()
	n.mu.Unlock()
	if err := n.fetchTicketKeys(ctx); err != nil {
		log.Printf("runtime: ticket keys refresh: %v", err)
	}
}

func (n *Node) ensureECHKeys(cfg localconfig.File) error {
	serverName := cfg.ServerName
	if serverName == "" {
		serverName = cfg.PublicHost
	}
	if serverName == "" {
		serverName = "nyxveil-node"
	}
	dir := filepath.Dir(n.opts.KeyPath)
	privPath := filepath.Join(dir, "ech-private.key")
	cfgPath := filepath.Join(dir, "ech-config.bin")

	var gen *ech.GeneratedKey
	if priv, err := os.ReadFile(privPath); err == nil {
		if conf, err2 := os.ReadFile(cfgPath); err2 == nil && len(priv) > 0 && len(conf) > 0 {
			gen = &ech.GeneratedKey{
				Key: tls.EncryptedClientHelloKey{
					Config:      append([]byte(nil), conf...),
					PrivateKey:  append([]byte(nil), priv...),
					SendAsRetry: true,
				},
				Config: conf,
			}
		}
	}
	if gen == nil {
		var err error
		gen, err = ech.GenerateKey(serverName, 0)
		if err != nil {
			return err
		}
		if err := os.WriteFile(privPath, gen.Key.PrivateKey, 0o600); err != nil {
			return err
		}
		if err := os.WriteFile(cfgPath, gen.Config, 0o644); err != nil {
			return err
		}
	}
	ks := ech.NewKeySet([]tls.EncryptedClientHelloKey{gen.Key})
	n.mu.Lock()
	n.echKeys = ks
	n.mu.Unlock()
	return nil
}

func (n *Node) loadTLSCert(ctx context.Context, cfg localconfig.File) (tls.Certificate, error) {
	certFile := cfg.TLSCertFile
	keyFile := cfg.TLSKeyFile
	if certFile == "" {
		certFile = paths.TLSCert()
	}
	if keyFile == "" {
		keyFile = paths.TLSKey()
	}
	dest := nodetls.Paths{CertFile: certFile, KeyFile: keyFile}

	if domain := strings.TrimSpace(cfg.ACMEDomain); domain != "" {
		acmeCtx, cancel := context.WithTimeout(ctx, 8*time.Minute)
		defer cancel()
		cert, _, _, pinChanged, err := n.issueACME(acmeCtx, cfg)
		if err != nil {
			if errors.Is(acmeCtx.Err(), context.DeadlineExceeded) {
				return tls.Certificate{}, fmt.Errorf("runtime: ACME TLS for %s timed out: %w", domain, err)
			}
			return tls.Certificate{}, fmt.Errorf("runtime: ACME TLS for %s: %w", domain, err)
		}
		if pinChanged {
			log.Printf("runtime: ACME SPKI changed — re-register so Control Plane catalog pin matches")
		}
		return cert, nil
	}

	// Prefer existing on-disk material (operator-provided) — never overwrite here.
	if nodetls.Exists(dest) {
		cert, err := nodetls.Load(dest)
		if err != nil {
			return tls.Certificate{}, fmt.Errorf("runtime: load TLS cert: %w", err)
		}
		return cert, nil
	}

	serverName := cfg.ServerName
	if serverName == "" {
		serverName = cfg.PublicHost
	}
	if serverName == "" {
		serverName = "nyxveil-node"
	}
	log.Printf("runtime: no TLS material and no acme_domain — generating self-signed leaf for %s (Windows SystemTrust clients will fail until a trusted cert is installed)", serverName)
	if err := nodetls.GenerateSelfSignedDev(dest, serverName, false); err != nil {
		return tls.Certificate{}, err
	}
	return nodetls.Load(dest)
}

// acmeRenewLoop periodically renews Let's Encrypt certificates and reloads listeners
// without rewriting server.json. Stable leaf key keeps SPKI pin constant when possible.
func (n *Node) acmeRenewLoop(ctx context.Context) {
	defer n.wg.Done()
	const interval = 12 * time.Hour
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	n.setNextPlannedRenewal(time.Now().Add(interval))
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			n.setNextPlannedRenewal(time.Now().Add(interval))
			n.mu.RLock()
			cfg := *n.local
			n.mu.RUnlock()
			if strings.TrimSpace(cfg.ACMEDomain) == "" {
				continue
			}
			_, _, _, _, err := n.issueACME(ctx, cfg)
			if err != nil {
				log.Printf("runtime: ACME renew: %v", err)
				continue
			}
		}
	}
}

func (n *Node) issueACME(ctx context.Context, cfg localconfig.File) (cert tls.Certificate, prevPin, newPin []byte, pinChanged bool, err error) {
	certFile := cfg.TLSCertFile
	keyFile := cfg.TLSKeyFile
	if certFile == "" {
		certFile = paths.TLSCert()
	}
	if keyFile == "" {
		keyFile = paths.TLSKey()
	}
	domain := strings.TrimSpace(cfg.ACMEDomain)
	stateDir := filepath.Join(filepath.Dir(keyFile), "acme")
	if ctx == nil {
		ctx = context.Background()
	}

	live := nodetls.Paths{CertFile: certFile, KeyFile: keyFile}
	// Reuse a still-fresh live leaf that covers acme_domain — do not open a new
	// ACME order on every register/start cycle (fresh install + first daemon start).
	validateLive := n.validateStagedTLS
	if validateLive == nil {
		validateLive = configure.ValidateLeafForDomain
	}
	if nodetls.Exists(live) && validateLive(certFile, keyFile, domain, time.Now()) == nil {
		existing, loadErr := nodetls.Load(live)
		if loadErr == nil && len(existing.Certificate) > 0 {
			leaf, parseErr := x509.ParseCertificate(existing.Certificate[0])
			if parseErr == nil && time.Until(leaf.NotAfter) > 30*24*time.Hour {
				prevPin, err = nodetls.SPKIPinSHA256(existing)
				if err != nil {
					return tls.Certificate{}, nil, nil, false, err
				}
				return existing, prevPin, append([]byte(nil), prevPin...), false, nil
			}
		}
	}

	n.recordRenewalAttempt()
	defer func() {
		n.recordRenewalResult(err)
	}()

	stageCert, stageKey := configure.StagingTLSPaths(filepath.Dir(keyFile))
	configure.CleanStaging(stageCert, stageKey)
	defer configure.CleanStaging(stageCert, stageKey)
	if err = configure.SeedStagingKeyFromLive(keyFile, stageKey); err != nil {
		return tls.Certificate{}, nil, nil, false, err
	}
	if nodetls.Exists(live) {
		if current, loadErr := nodetls.Load(live); loadErr == nil {
			prevPin, _ = nodetls.SPKIPinSHA256(current)
		}
	}
	issuer := n.acmeIssuer
	if issuer == nil {
		issuer = nodetls.IssueOrRenew
	}
	_, _, _, _, err = issuer(ctx, nodetls.ACMEConfig{
		Domain:     domain,
		Email:      strings.TrimSpace(cfg.ACMEEmail),
		StateDir:   stateDir,
		AccountKey: filepath.Join(stateDir, "acme-account.key"),
		Dest:       nodetls.Paths{CertFile: stageCert, KeyFile: stageKey},
		Replace:    true,
	})
	if err != nil {
		return tls.Certificate{}, prevPin, nil, false, err
	}
	validate := n.validateStagedTLS
	if validate == nil {
		validate = configure.ValidateLeafForDomain
	}
	if err = validate(stageCert, stageKey, domain, time.Now()); err != nil {
		return tls.Certificate{}, prevPin, nil, false, err
	}
	staged, err := nodetls.Load(nodetls.Paths{CertFile: stageCert, KeyFile: stageKey})
	if err != nil {
		return tls.Certificate{}, prevPin, nil, false, err
	}
	newPin, err = nodetls.SPKIPinSHA256(staged)
	if err != nil {
		return tls.Certificate{}, prevPin, nil, false, err
	}
	pinChanged = nodetls.PinChanged(prevPin, newPin)

	commit := n.commitTLS
	if commit == nil {
		commit = configure.AtomicCommitTLS
	}
	if !pinChanged {
		if err = commit(stageCert, stageKey, certFile, keyFile); err != nil {
			return tls.Certificate{}, prevPin, newPin, false, err
		}
		cert, err = nodetls.Load(live)
		if err == nil {
			err = n.reloadActivatedTLS(cert)
		}
		return cert, prevPin, newPin, false, err
	}

	backup, err := configure.BackupLiveTLS(certFile, keyFile)
	if err != nil {
		return tls.Certificate{}, prevPin, newPin, true, err
	}
	if err = n.advertiseStagedSPKI(ctx, newPin); err != nil {
		return tls.Certificate{}, prevPin, newPin, true, fmt.Errorf("runtime: advertise staged SPKI: %w", err)
	}
	if err = n.verifyAdvertisedSPKI(ctx, newPin); err != nil {
		return tls.Certificate{}, prevPin, newPin, true, fmt.Errorf("runtime: verify staged SPKI catalog: %w", err)
	}

	rollback := func(cause error) error {
		restoreErr := configure.RestoreLiveTLS(backup, certFile, keyFile)
		if oldCert, loadErr := nodetls.Load(live); loadErr == nil {
			_ = n.reloadActivatedTLS(oldCert)
		}
		var advertiseErr error
		if len(prevPin) > 0 {
			advertiseErr = n.advertiseStagedSPKI(ctx, prevPin)
		}
		return errors.Join(cause, restoreErr, advertiseErr)
	}
	if err = commit(stageCert, stageKey, certFile, keyFile); err != nil {
		return tls.Certificate{}, prevPin, newPin, true, rollback(fmt.Errorf("runtime: activate staged TLS: %w", err))
	}
	cert, err = nodetls.Load(live)
	if err != nil {
		return tls.Certificate{}, prevPin, newPin, true, rollback(err)
	}
	if err = n.reloadActivatedTLS(cert); err != nil {
		return tls.Certificate{}, prevPin, newPin, true, rollback(err)
	}
	if err = n.verifyActivatedSPKI(ctx, newPin, live); err != nil {
		return tls.Certificate{}, prevPin, newPin, true, rollback(err)
	}
	return cert, prevPin, newPin, true, nil
}

func (n *Node) advertiseStagedSPKI(ctx context.Context, pin []byte) error {
	if n.advertiseSPKI != nil {
		return n.advertiseSPKI(ctx, append([]byte(nil), pin...))
	}
	_, err := n.RegisterWithSPKI(ctx, pin)
	return err
}

func (n *Node) verifyAdvertisedSPKI(ctx context.Context, pin []byte) error {
	if n.verifyCatalogSPKI != nil {
		return n.verifyCatalogSPKI(ctx, append([]byte(nil), pin...))
	}
	keysJSON, err := n.cp.GetCatalogKeys(ctx)
	if err != nil {
		return err
	}
	catalogJSON, err := n.cp.GetSignedSelf(ctx)
	if err != nil {
		return err
	}
	signed, err := catalogverify.VerifySignedCatalogJSON(keysJSON, catalogJSON)
	if err != nil {
		return err
	}
	node, ok := catalogverify.FindNode(signed, n.cp.NodeID)
	if !ok {
		return fmt.Errorf("node %q absent from signed self catalog", n.cp.NodeID)
	}
	if !bytes.Equal(node.SPKIPin, pin) {
		return fmt.Errorf("signed self catalog SPKI mismatch")
	}
	return nil
}

func (n *Node) reloadActivatedTLS(cert tls.Certificate) error {
	if n.reloadTLS != nil {
		return n.reloadTLS(cert)
	}
	if n.listen == nil {
		return nil
	}
	n.listen.UpdateCert(cert)
	ctx := n.runCtx
	if ctx == nil {
		ctx = context.Background()
	}
	if err := n.listen.StartTLS(ctx); err != nil {
		return fmt.Errorf("runtime: TLS reload after ACME: %w", err)
	}
	if err := n.listen.StartQUIC(ctx); err != nil {
		return fmt.Errorf("runtime: QUIC reload after ACME: %w", err)
	}
	return nil
}

func (n *Node) verifyActivatedSPKI(ctx context.Context, pin []byte, live nodetls.Paths) error {
	if n.verifyServedSPKI != nil {
		return n.verifyServedSPKI(ctx, append([]byte(nil), pin...))
	}
	cert, err := nodetls.Load(live)
	if err != nil {
		return err
	}
	got, err := nodetls.SPKIPinSHA256(cert)
	if err != nil {
		return err
	}
	if !bytes.Equal(got, pin) {
		return fmt.Errorf("runtime: activated TLS SPKI mismatch")
	}
	return nil
}

func (n *Node) recordRenewalAttempt() {
	n.renewMu.Lock()
	n.lastRenewalAttempt = time.Now().UTC()
	n.lastRenewalError = ""
	n.renewMu.Unlock()
}

func (n *Node) recordRenewalResult(err error) {
	n.renewMu.Lock()
	defer n.renewMu.Unlock()
	if err == nil {
		n.lastSuccessfulRenewal = time.Now().UTC()
		n.lastRenewalError = ""
		return
	}
	n.lastRenewalError = safeRenewalError(err)
}

func (n *Node) setNextPlannedRenewal(t time.Time) {
	n.renewMu.Lock()
	n.nextPlannedRenewal = t.UTC()
	n.renewMu.Unlock()
}

func safeRenewalError(err error) string {
	if err == nil {
		return ""
	}
	msg := strings.ToLower(err.Error())
	switch {
	case errors.Is(err, context.Canceled):
		return "ACME renewal canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "ACME renewal timed out"
	case strings.Contains(msg, "dns"):
		return "ACME renewal failed DNS validation"
	case strings.Contains(msg, "listen") || strings.Contains(msg, "port 80"):
		return "ACME renewal failed HTTP-01 listener setup"
	case strings.Contains(msg, "authorization") || strings.Contains(msg, "challenge"):
		return "ACME renewal failed domain authorization"
	case strings.Contains(msg, "certificate") || strings.Contains(msg, "x509"):
		return "ACME renewal returned an invalid certificate"
	default:
		return "ACME renewal failed; details are available in local logs"
	}
}

func generateSelfSigned(certFile, keyFile, serverName string) error {
	if err := os.MkdirAll(filepath.Dir(certFile), 0o700); err != nil {
		return err
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	if ip := net.ParseIP(serverName); ip != nil {
		tmpl.IPAddresses = []net.IP{ip}
	} else {
		tmpl.DNSNames = []string{serverName}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, pub, priv)
	if err != nil {
		return err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	if err := os.WriteFile(certFile, certPEM, 0o644); err != nil {
		return err
	}
	return os.WriteFile(keyFile, keyPEM, 0o600)
}

// SPKIPinSHA256 returns SHA-256 of the leaf certificate SPKI.
func SPKIPinSHA256(cert tls.Certificate) ([]byte, error) {
	if len(cert.Certificate) == 0 {
		return nil, errors.New("runtime: empty certificate")
	}
	parsed, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(parsed.RawSubjectPublicKeyInfo)
	return sum[:], nil
}

// LoadCPPublicKeyPEM loads an Ed25519 public key from PEM for ticket verify.
func LoadCPPublicKeyPEM(path string) (ed25519.PublicKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("runtime: no PEM block")
	}
	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	ed, ok := pub.(ed25519.PublicKey)
	if !ok {
		return nil, errors.New("runtime: not ed25519")
	}
	return ed, nil
}
