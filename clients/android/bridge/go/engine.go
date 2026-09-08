package nyxveilbridge

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nyxveil/client-android/bridge/mtu"
	"github.com/nyxveil/client-android/bridge/netcfg"
	"github.com/nyxveil/client-android/bridge/protectquic"
	"github.com/nyxveil/client-android/bridge/protecttls"
	"github.com/nyxveil/nvp/core/auth/ticket"
	"github.com/nyxveil/nvp/core/connector"
	"github.com/nyxveil/nvp/core/control"
	"github.com/nyxveil/nvp/core/controlplane/catalog"
	"github.com/nyxveil/nvp/core/controlplane/model"
	"github.com/nyxveil/nvp/core/failover"
	"github.com/nyxveil/nvp/core/nvperr"
	"github.com/nyxveil/nvp/core/session"
	"github.com/nyxveil/nvp/core/transport"
	"github.com/quic-go/quic-go"
)

// Protector is implemented on the Java/Kotlin side (VpnService.protect).
type Protector interface {
	Protect(fd int) bool
}

// Engine owns one NVP session for Android.
type Engine struct {
	mu sync.Mutex

	protector Protector

	desiredConnected bool
	opGen            uint64
	connecting       bool

	sess   *session.Session
	conn   transport.Conn
	tun    *tunFile
	cancel context.CancelFunc

	cfgMsg         *netcfg.Message
	effectiveMTU   int
	maxDatagram    int64
	nodeID         string
	locationID     string
	transportName  string
	txBytes         atomic.Uint64
	rxBytes         atomic.Uint64
	txTooLarge      atomic.Uint64
	connectedAtUnix atomic.Int64
	lastErr         string
	dataplaneReady  bool
	dp              dataplaneCounters

	earlyCfg chan *netcfg.Message
	earlyErr chan error
}

// NewEngine constructs a native engine instance.
func NewEngine() *Engine {
	return &Engine{}
}

// SetProtector installs VpnService.protect callback (required before Begin).
func (e *Engine) SetProtector(p Protector) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.protector = p
}

// BeginJSON dials (protected), AUTH, AUTH_OK, waits TypeConfig.
// configJSON fields: location_id, access_ticket, device_private_key_b64,
// signed_catalog_json (base64 or raw UTF-8 in field catalog_json), catalog_keys_json.
// Returns TypeConfig JSON including effective_mtu. Does NOT attach TUN.
func (e *Engine) BeginJSON(configJSON string) (string, error) {
	var in beginInput
	if err := json.Unmarshal([]byte(configJSON), &in); err != nil {
		return "", fmt.Errorf("begin: bad config json: %w", err)
	}
	if in.LocationID == "" || in.AccessTicket == "" {
		return "", fmt.Errorf("begin: location_id and access_ticket required")
	}
	privRaw, err := base64.StdEncoding.DecodeString(in.DevicePrivateKeyB64)
	if err != nil {
		return "", fmt.Errorf("begin: device private key required")
	}
	var priv ed25519.PrivateKey
	switch len(privRaw) {
	case ed25519.SeedSize:
		priv = ed25519.NewKeyFromSeed(privRaw)
	case ed25519.PrivateKeySize:
		priv = ed25519.PrivateKey(privRaw)
	default:
		return "", fmt.Errorf("begin: device private key required")
	}
	catRaw := []byte(in.CatalogJSON)
	if in.CatalogJSONB64 != "" {
		catRaw, err = base64.StdEncoding.DecodeString(in.CatalogJSONB64)
		if err != nil {
			return "", fmt.Errorf("begin: catalog b64: %w", err)
		}
	}
	keys, err := parseCatalogKeys(in.CatalogKeysJSON)
	if err != nil {
		return "", err
	}
	signed, err := catalog.Parse(catRaw)
	if err != nil {
		return "", fmt.Errorf("begin: catalog parse: %w", err)
	}
	// Windows semantics: tolerate small IssuedAt not-before skew, then Frozen Core Verify.
	waitCatalogNotBefore(signed.Catalog.IssuedAt, 5*time.Minute)
	if err := catalog.Verify(keys, signed); err != nil {
		return "", fmt.Errorf("begin: catalog verify: %w", err)
	}

	e.mu.Lock()
	if e.connecting || e.sess != nil {
		e.mu.Unlock()
		return "", fmt.Errorf("begin: session already active")
	}
	if e.protector == nil {
		e.mu.Unlock()
		return "", fmt.Errorf("begin: protector not set")
	}
	protect := e.protector
	e.desiredConnected = true
	e.opGen++
	op := e.opGen
	e.connecting = true
	e.lastErr = ""
	e.dataplaneReady = false
	e.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	reg := transport.NewRegistry()
	reg.Register(protectquic.New(func(fd int) bool { return protect.Protect(fd) }))
	reg.Register(protecttls.New(func(fd int) bool { return protect.Protect(fd) }))

	c := &connector.Connector{
		Registry:          reg,
		RequirePin:        true,
		Provider:          &systemTrust{},
		Policy:            failover.DefaultConnectPolicy(),
		CatalogVerifyKeys: keys,
	}

	sess, conn, node, err := openSessionWithTicket(ctx, c, signed.Catalog, in.LocationID, in.AccessTicket, priv, e)
	if err != nil {
		e.mu.Lock()
		e.connecting = false
		e.lastErr = err.Error()
		e.mu.Unlock()
		return "", err
	}

	e.mu.Lock()
	if !e.desiredConnected || e.opGen != op {
		e.mu.Unlock()
		_ = sess.Close(context.Background())
		_ = conn.Close()
		e.mu.Lock()
		e.connecting = false
		e.mu.Unlock()
		return "", fmt.Errorf("begin: cancelled")
	}
	e.sess = sess
	e.conn = conn
	e.nodeID = node.NodeID
	e.locationID = in.LocationID
	e.transportName = string(conn.Profile())

	runCtx, runCancel := context.WithCancel(context.Background())
	e.cancel = runCancel
	e.mu.Unlock()

	go func() { _ = sess.ReadLoop(runCtx) }()

	cfgMsg, err := e.waitTypeConfig(runCtx)
	if err != nil {
		e.cleanupSession()
		return "", err
	}
	if netcfg.IsNetworkBase(cfgMsg.VPNIP, cfgMsg.VPNPrefix) {
		e.cleanupSession()
		return "", fmt.Errorf("begin: vpn_ip %s is network base for /%d", cfgMsg.VPNIP, cfgMsg.VPNPrefix)
	}

	maxDG, ok, probeErr := probeDatagramCeiling(ctx, sess, conn)
	eff, mtuErr := mtu.EffectiveTunnelMTU(cfgMsg.MTU, int(maxDG))
	if mtuErr != nil {
		e.cleanupSession()
		return "", mtuErr
	}
	if !ok && probeErr != nil {
		// TLS path / no datagrams: still apply conservative clamp via EffectiveTunnelMTU(maxDG=0).
		_ = probeErr
	}

	e.mu.Lock()
	e.cfgMsg = cfgMsg
	e.effectiveMTU = eff
	e.maxDatagram = maxDG
	e.connecting = false
	e.mu.Unlock()

	out := map[string]any{
		"vpn_ip":               cfgMsg.VPNIP,
		"vpn_prefix":           cfgMsg.VPNPrefix,
		"gateway":              cfgMsg.Gateway,
		"dns_servers":          cfgMsg.DNSServers,
		"typeconfig_mtu":       cfgMsg.MTU,
		"max_datagram_payload": maxDG,
		"nvp_overhead":         mtu.NVPOverheadWorstCase(),
		"effective_tunnel_mtu": eff,
		"node_id":              node.NodeID,
		"transport":            string(conn.Profile()),
	}
	b, _ := json.Marshal(out)
	return string(b), nil
}

// AttachTun takes ownership of tunFd (Kotlin must detachFd). Starts dataplane pumps.
func (e *Engine) AttachTun(tunFd int) error {
	if tunFd < 0 {
		return fmt.Errorf("attach: invalid tun fd")
	}
	e.mu.Lock()
	if e.sess == nil || e.cfgMsg == nil {
		e.mu.Unlock()
		_ = closeFD(tunFd)
		return fmt.Errorf("attach: begin first")
	}
	if !e.desiredConnected {
		e.mu.Unlock()
		_ = closeFD(tunFd)
		return fmt.Errorf("attach: disconnected")
	}
	if e.tun != nil {
		e.mu.Unlock()
		_ = closeFD(tunFd)
		return fmt.Errorf("attach: tun already attached")
	}
	mtuVal := e.effectiveMTU
	if mtuVal <= 0 {
		mtuVal = e.cfgMsg.MTU
	}
	sess := e.sess
	vpnIP := e.cfgMsg.VPNIP
	e.mu.Unlock()

	dev, err := newTunFile(tunFd, mtuVal)
	if err != nil {
		return fmt.Errorf("attach: %w", err)
	}

	e.mu.Lock()
	e.resetDataplaneCounters()
	e.tun = dev
	e.mu.Unlock()

	e.startDataplane(sess, dev, vpnIP)

	e.mu.Lock()
	e.dataplaneReady = true
	e.connectedAtUnix.Store(time.Now().Unix())
	e.lastErr = ""
	e.mu.Unlock()
	return nil
}

// Disconnect tears down session, transport, TUN (idempotent).
func (e *Engine) Disconnect() {
	e.mu.Lock()
	e.desiredConnected = false
	e.opGen++
	cancel := e.cancel
	e.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	e.cleanupSession()
}

func (e *Engine) cleanupSession() {
	e.mu.Lock()
	cancel := e.cancel
	sess := e.sess
	conn := e.conn
	tun := e.tun
	e.cancel = nil
	e.sess = nil
	e.conn = nil
	e.tun = nil
	e.cfgMsg = nil
	e.dataplaneReady = false
	e.connecting = false
	e.connectedAtUnix.Store(0)
	e.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if sess != nil {
		_ = sess.Close(context.Background())
	}
	if conn != nil {
		_ = conn.Close()
	}
	if tun != nil {
		_ = tun.Close()
	}
}

// StatusJSON returns telemetry without secrets.
func (e *Engine) StatusJSON() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	connected := e.dataplaneReady && e.sess != nil && e.desiredConnected && e.dp.txPumpAlive.Load()
	vpnIP := ""
	dns := ""
	prefix := 0
	tcMTU := 0
	if e.cfgMsg != nil {
		vpnIP = e.cfgMsg.VPNIP
		prefix = e.cfgMsg.VPNPrefix
		tcMTU = e.cfgMsg.MTU
		if len(e.cfgMsg.DNSServers) > 0 {
			dns = e.cfgMsg.DNSServers[0]
			for i := 1; i < len(e.cfgMsg.DNSServers); i++ {
				dns += "," + e.cfgMsg.DNSServers[i]
			}
		}
	}
	out := map[string]any{
		"connected":             connected,
		"vpn_ip":                vpnIP,
		"vpn_prefix":            prefix,
		"dns":                   dns,
		"typeconfig_mtu":        tcMTU,
		"max_datagram_payload":  e.maxDatagram,
		"nvp_overhead":          mtu.NVPOverheadWorstCase(),
		"effective_tunnel_mtu":  e.effectiveMTU,
		"mtu":                   e.effectiveMTU,
		"transport":             e.transportName,
		"protocol":              "NVP/1",
		"node_id":               e.nodeID,
		"location_id":           e.locationID,
		"tx_bytes":              e.txBytes.Load(),
		"rx_bytes":              e.rxBytes.Load(),
		"tx_datagram_too_large": e.txTooLarge.Load(),
		"connected_at_unix":     e.connectedAtUnix.Load(),
		"message":               e.lastErr,
		"tun_blocking":          true,
	}
	for k, v := range e.dp.snapshotMap() {
		out[k] = v
	}
	b, _ := json.Marshal(out)
	return string(b)
}

type beginInput struct {
	LocationID          string `json:"location_id"`
	AccessTicket        string `json:"access_ticket"`
	DevicePrivateKeyB64 string `json:"device_private_key_b64"`
	CatalogJSON         string `json:"catalog_json"`
	CatalogJSONB64      string `json:"catalog_json_b64"`
	CatalogKeysJSON     string `json:"catalog_keys_json"`
}

type systemTrust struct{}

func (systemTrust) RootCAs() interface{}                         { return nil }
func (systemTrust) ServerNameFor(node model.NodeRegistryEntry) string {
	return node.ServerName
}
func (systemTrust) PinnedPubKeyFor(model.NodeRegistryEntry) []byte { return nil }
func (systemTrust) ECHPolicy() transport.ECHPolicy                 { return "" }
func (systemTrust) ECHConfigList() []byte                          { return nil }

func parseCatalogKeys(keysJSON string) (catalog.VerifyKeys, error) {
	var raw map[string]string
	if err := json.Unmarshal([]byte(keysJSON), &raw); err != nil {
		return catalog.VerifyKeys{}, fmt.Errorf("catalog keys: %w", err)
	}
	keys := catalog.VerifyKeys{Keys: make(map[string]ed25519.PublicKey, len(raw))}
	for kid, b64 := range raw {
		pub, err := base64.StdEncoding.DecodeString(b64)
		if err != nil || len(pub) != ed25519.PublicKeySize {
			return catalog.VerifyKeys{}, fmt.Errorf("catalog key %s invalid", kid)
		}
		keys.Keys[kid] = ed25519.PublicKey(pub)
	}
	return keys, nil
}

func openSessionWithTicket(
	ctx context.Context,
	c *connector.Connector,
	cat model.Catalog,
	locationID, accessTicket string,
	deviceKey ed25519.PrivateKey,
	e *Engine,
) (*session.Session, transport.Conn, model.NodeRegistryEntry, error) {
	sel := &failover.Selector{Catalog: cat, LocationID: locationID}
	candidates := sel.CandidateNodes()
	if len(candidates) == 0 {
		return nil, nil, model.NodeRegistryEntry{}, nvperr.ErrNoHealthyNodes
	}
	for _, node := range candidates {
		if len(node.SPKIPin) == 0 && c.RequirePin {
			return nil, nil, model.NodeRegistryEntry{}, fmt.Errorf("%w: empty SPKI for %s", nvperr.ErrServerIdentityMismatch, node.NodeID)
		}
	}
	policy := c.Policy
	if claims, peekErr := ticket.PeekClaims(accessTicket); peekErr == nil && len(claims.NodeScope) > 0 {
		policy.AllowedNodeIDs = append([]string(nil), claims.NodeScope...)
	}
	conn, node, err := failover.ConnectWithFailover(ctx, sel, c.Registry, policy, c.Provider)
	if err != nil {
		return nil, nil, model.NodeRegistryEntry{}, err
	}
	sess := session.New(session.DefaultConfig(true))
	if err := sess.Connect(ctx, conn); err != nil {
		_ = conn.Close()
		return nil, nil, model.NodeRegistryEntry{}, err
	}
	if err := sess.RunHandshake(ctx); err != nil {
		_ = conn.Close()
		return nil, nil, model.NodeRegistryEntry{}, err
	}
	authCtx, cancel := context.WithTimeout(ctx, connector.DefaultAuthTimeout)
	defer cancel()
	authBody, err := ticket.EncodeAuthPayload(accessTicket, sess.Transcript(), deviceKey)
	if err != nil {
		_ = conn.Close()
		return nil, nil, model.NodeRegistryEntry{}, err
	}
	e.armTypeConfigCatcher(sess)
	if err := sess.SendAuth(authCtx, authBody); err != nil {
		_ = conn.Close()
		return nil, nil, model.NodeRegistryEntry{}, err
	}
	if err := sess.WaitEstablished(authCtx); err != nil {
		_ = conn.Close()
		return nil, nil, model.NodeRegistryEntry{}, err
	}
	return sess, conn, node, nil
}

func (e *Engine) armTypeConfigCatcher(sess *session.Session) {
	cfgCh := make(chan *netcfg.Message, 1)
	errCh := make(chan error, 1)
	e.mu.Lock()
	e.earlyCfg = cfgCh
	e.earlyErr = errCh
	e.mu.Unlock()
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
}

func (e *Engine) waitTypeConfig(ctx context.Context) (*netcfg.Message, error) {
	e.mu.Lock()
	cfgCh := e.earlyCfg
	errCh := e.earlyErr
	e.mu.Unlock()

	waitCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	if cfgCh != nil {
		select {
		case msg := <-cfgCh:
			if msg != nil {
				return msg, nil
			}
		default:
		}
	}

	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-waitCtx.Done():
			return nil, fmt.Errorf("typeconfig timeout")
		case err := <-errCh:
			if err != nil {
				return nil, err
			}
		case msg := <-cfgCh:
			if msg != nil {
				return msg, nil
			}
		case <-ticker.C:
			if cfgCh != nil {
				select {
				case msg := <-cfgCh:
					if msg != nil {
						return msg, nil
					}
				default:
				}
			}
		}
	}
}

func probeDatagramCeiling(ctx context.Context, sess *session.Session, conn transport.Conn) (int64, bool, error) {
	if dg, ok := conn.(transport.DatagramConn); !ok || !dg.DatagramsEnabled() {
		return 0, false, nil
	}
	probeIP := make([]byte, 4096)
	probeIP[0] = 0x45
	err := sess.WritePacket(ctx, probeIP)
	if err == nil {
		return 0, false, fmt.Errorf("datagram probe unexpectedly succeeded")
	}
	var tooLarge *quic.DatagramTooLargeError
	if !errors.As(err, &tooLarge) || tooLarge == nil || tooLarge.MaxDatagramPayloadSize <= 0 {
		return 0, false, err
	}
	return tooLarge.MaxDatagramPayloadSize, true, nil
}
