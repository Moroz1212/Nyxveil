package configure_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nyxveil/server/internal/configure"
	"github.com/nyxveil/server/internal/identity"
	"github.com/nyxveil/server/internal/localconfig"
	"github.com/nyxveil/server/internal/version"
)

func TestConfigureControlPlaneUrlDryRunNoChanges(t *testing.T) {
	h := setupCPURLHarness(t)
	before, _ := os.ReadFile(h.cfgPath)
	_, err := configure.Apply(context.Background(), h.baseOpts(func(o *configure.Options) {
		o.DryRun = true
		o.ControlPlaneURL = "https://cp.nyxveil.ru:18443"
		o.ExecProbeCP = func(ctx context.Context, u string) (*configure.ControlPlaneProbeResult, error) {
			return &configure.ControlPlaneProbeResult{URL: u, TLSOK: true, AuthOK: true, NodeID: h.nodeID}, nil
		}
	}))
	if err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(h.cfgPath)
	if string(before) != string(after) {
		t.Fatal("dry-run must not change server.json")
	}
}

func TestControlPlaneUrlRequiresHTTPS(t *testing.T) {
	_, err := configure.NormalizeControlPlaneURL("http://cp.nyxveil.ru:18443")
	if err == nil || !strings.Contains(err.Error(), "https") {
		t.Fatalf("want https reject, got %v", err)
	}
}

func TestWrongHostnameFailsBeforeConfigWrite(t *testing.T) {
	h := setupCPURLHarness(t)
	before, _ := os.ReadFile(h.cfgPath)
	_, err := configure.Apply(context.Background(), h.baseOpts(func(o *configure.Options) {
		o.ControlPlaneURL = "https://cp.nyxveil.ru:18443"
		o.ExecProbeCP = func(ctx context.Context, u string) (*configure.ControlPlaneProbeResult, error) {
			return nil, fmt.Errorf("configure: control plane hostname mismatch: x")
		}
	}))
	if err == nil {
		t.Fatal("expected fail")
	}
	after, _ := os.ReadFile(h.cfgPath)
	if string(before) != string(after) {
		t.Fatal("must not write config on probe failure")
	}
}

func TestUntrustedCPFailsBeforeConfigWrite(t *testing.T) {
	h := setupCPURLHarness(t)
	before, _ := os.ReadFile(h.cfgPath)
	_, err := configure.Apply(context.Background(), h.baseOpts(func(o *configure.Options) {
		o.ControlPlaneURL = "https://cp.nyxveil.ru:18443"
		o.ExecProbeCP = func(ctx context.Context, u string) (*configure.ControlPlaneProbeResult, error) {
			return nil, fmt.Errorf("configure: control plane SystemTrust TLS failed")
		}
	}))
	if err == nil {
		t.Fatal("expected untrusted fail")
	}
	after, _ := os.ReadFile(h.cfgPath)
	if string(before) != string(after) {
		t.Fatal("config mutated")
	}
}

func TestUnreachableCPFailsBeforeConfigWrite(t *testing.T) {
	h := setupCPURLHarness(t)
	before, _ := os.ReadFile(h.cfgPath)
	_, err := configure.Apply(context.Background(), h.baseOpts(func(o *configure.Options) {
		o.ControlPlaneURL = "https://cp.nyxveil.ru:18443"
		o.ExecProbeCP = func(ctx context.Context, u string) (*configure.ControlPlaneProbeResult, error) {
			return nil, fmt.Errorf("configure: control plane TCP unreachable")
		}
	}))
	if err == nil {
		t.Fatal("expected unreachable fail")
	}
	after, _ := os.ReadFile(h.cfgPath)
	if string(before) != string(after) {
		t.Fatal("config mutated")
	}
}

func TestControlPlaneUrlAtomicConfigUpdate(t *testing.T) {
	h := setupCPURLHarness(t)
	res, err := configure.Apply(context.Background(), h.successOpts())
	if err != nil {
		t.Fatal(err)
	}
	if !res.CPURLChanged || !res.Registered || !res.CatalogVerified {
		t.Fatalf("%+v", res)
	}
	got, err := localconfig.Load(h.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if got.ControlPlaneURL != "https://cp.nyxveil.ru:18443" {
		t.Fatalf("url=%s", got.ControlPlaneURL)
	}
	if got.ControlPlaneSPKIPin != "" {
		t.Fatal("SPKI pin must be cleared for SystemTrust CP")
	}
}

func TestNodeIdentityPreservedCPURL(t *testing.T) {
	h := setupCPURLHarness(t)
	keyBefore, _ := os.ReadFile(h.nodeKey)
	res, err := configure.Apply(context.Background(), h.successOpts())
	if err != nil {
		t.Fatal(err)
	}
	if res.NodeID != h.nodeID {
		t.Fatal(res.NodeID)
	}
	if string(mustReadCP(t, h.nodeKey)) != string(keyBefore) {
		t.Fatal("node.key changed")
	}
}

func TestLocationPreservedCPURL(t *testing.T) {
	h := setupCPURLHarness(t)
	res, err := configure.Apply(context.Background(), h.successOpts())
	if err != nil {
		t.Fatal(err)
	}
	if res.LocationID != "fi-helsinki" {
		t.Fatal(res.LocationID)
	}
}

func TestVPNLeafSPKIPreserved(t *testing.T) {
	h := setupCPURLHarness(t)
	prev, _ := configure.CurrentSPKIHex(h.liveCert, h.liveKey)
	res, err := configure.Apply(context.Background(), h.successOpts())
	if err != nil {
		t.Fatal(err)
	}
	if res.SPKIChanged {
		t.Fatal("VPN SPKI must not change on CP URL cutover")
	}
	got, _ := configure.CurrentSPKIHex(h.liveCert, h.liveKey)
	if !strings.EqualFold(got, prev) {
		t.Fatalf("%s -> %s", prev, got)
	}
}

func TestPublicHostPreserved(t *testing.T) {
	h := setupCPURLHarness(t)
	_, err := configure.Apply(context.Background(), h.successOpts())
	if err != nil {
		t.Fatal(err)
	}
	got, _ := localconfig.Load(h.cfgPath)
	if got.PublicHost != "fi-hel-01.nyxveil.ru" {
		t.Fatal(got.PublicHost)
	}
}

func TestDnsServersPreserved(t *testing.T) {
	h := setupCPURLHarness(t)
	_, err := configure.Apply(context.Background(), h.successOpts())
	if err != nil {
		t.Fatal(err)
	}
	got, _ := localconfig.Load(h.cfgPath)
	if len(got.DNSServers) != 2 || got.DNSServers[0] != "1.1.1.1" {
		t.Fatalf("%v", got.DNSServers)
	}
}

func TestReconnectUsesSameNodeIdentity(t *testing.T) {
	h := setupCPURLHarness(t)
	reg := 0
	_, err := configure.Apply(context.Background(), h.successOpts(func(o *configure.Options) {
		o.ExecRegister = func(p string) error {
			reg++
			got, err := localconfig.Load(p)
			if err != nil {
				return err
			}
			if got.NodeID != h.nodeID || got.LocationID != "fi-helsinki" {
				return fmt.Errorf("identity mutated")
			}
			return nil
		}
	}))
	if err != nil {
		t.Fatal(err)
	}
	if reg != 1 {
		t.Fatalf("reg=%d", reg)
	}
}

func TestHeartbeatAcceptedAfterCPUrlChange(t *testing.T) {
	h := setupCPURLHarness(t)
	hb := false
	_, err := configure.Apply(context.Background(), h.successOpts(func(o *configure.Options) {
		o.ExecVerifyCatalog = func(ctx context.Context, cfgPath string) (*configure.CatalogFreshness, error) {
			hb = true
			return &configure.CatalogFreshness{
				Verified: true, NodeID: h.nodeID, LocationID: "fi-helsinki", Online: true,
				ServerVersion: version.ServerVersion, Message: "heartbeat ok",
			}, nil
		}
	}))
	if err != nil || !hb {
		t.Fatalf("err=%v hb=%v", err, hb)
	}
}

func TestCatalogFreshAfterCPUrlChange(t *testing.T) {
	h := setupCPURLHarness(t)
	res, err := configure.Apply(context.Background(), h.successOpts())
	if err != nil {
		t.Fatal(err)
	}
	if !res.CatalogVerified {
		t.Fatal("catalog not verified")
	}
}

func TestCatalogContainsCurrentServerVersion(t *testing.T) {
	h := setupCPURLHarness(t)
	var ver string
	_, err := configure.Apply(context.Background(), h.successOpts(func(o *configure.Options) {
		o.ExecVerifyCatalog = func(ctx context.Context, cfgPath string) (*configure.CatalogFreshness, error) {
			ver = version.ServerVersion
			return &configure.CatalogFreshness{
				Verified: true, NodeID: h.nodeID, LocationID: "fi-helsinki",
				ServerVersion: version.ServerVersion, Online: true,
			}, nil
		}
	}))
	if err != nil {
		t.Fatal(err)
	}
	if ver != version.ServerVersion {
		t.Fatalf("version=%s", ver)
	}
}

func TestCatalogContainsCurrentSPKI(t *testing.T) {
	h := setupCPURLHarness(t)
	want, _ := configure.CurrentSPKIHex(h.liveCert, h.liveKey)
	var got string
	_, err := configure.Apply(context.Background(), h.successOpts(func(o *configure.Options) {
		o.ExecVerifyCatalog = func(ctx context.Context, cfgPath string) (*configure.CatalogFreshness, error) {
			spki, _ := configure.CurrentSPKIHex(h.liveCert, h.liveKey)
			got = spki
			return &configure.CatalogFreshness{
				Verified: true, NodeID: h.nodeID, LocationID: "fi-helsinki",
				SPKIHex: spki, Online: true,
			}, nil
		}
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(got, want) {
		t.Fatalf("%s vs %s", got, want)
	}
}

func TestFailureRollbackRestoresPreviousConfig(t *testing.T) {
	h := setupCPURLHarness(t)
	before, _ := os.ReadFile(h.cfgPath)
	res, err := configure.Apply(context.Background(), h.baseOpts(func(o *configure.Options) {
		o.ControlPlaneURL = "https://cp.nyxveil.ru:18443"
		o.SkipCP = false
		o.ExecProbeCP = func(ctx context.Context, u string) (*configure.ControlPlaneProbeResult, error) {
			return &configure.ControlPlaneProbeResult{URL: u, TLSOK: true, AuthOK: true}, nil
		}
		o.ExecRegister = func(string) error { return fmt.Errorf("register boom") }
	}))
	if err == nil || res == nil || !res.RolledBack {
		t.Fatalf("err=%v res=%+v", err, res)
	}
	after, _ := os.ReadFile(h.cfgPath)
	if string(after) != string(before) {
		t.Fatal("server.json not restored")
	}
	got, _ := localconfig.Load(h.cfgPath)
	if got.ControlPlaneURL != "https://42mou.ru:18443" {
		t.Fatal(got.ControlPlaneURL)
	}
}

func TestDataPlaneRemainsOperationalWhenManagementPlaneUnavailable(t *testing.T) {
	h := setupCPURLHarness(t)
	res, err := configure.Apply(context.Background(), h.baseOpts(func(o *configure.Options) {
		o.ControlPlaneURL = "https://cp.nyxveil.ru:18443"
		o.SkipCP = false
		o.ExecProbeCP = func(ctx context.Context, u string) (*configure.ControlPlaneProbeResult, error) {
			return &configure.ControlPlaneProbeResult{URL: u, TLSOK: true, AuthOK: true}, nil
		}
		o.ExecRegister = func(string) error { return nil }
		o.ExecVerifyCatalog = func(ctx context.Context, cfgPath string) (*configure.CatalogFreshness, error) {
			return nil, fmt.Errorf("catalog stale / management unavailable")
		}
	}))
	if err == nil || res == nil || !res.RolledBack || !res.RollbackConfigComplete {
		t.Fatalf("%v %+v", err, res)
	}
	if _, err := os.Stat(h.liveCert); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(h.liveKey); err != nil {
		t.Fatal(err)
	}
	// VPN leaf unchanged
	got, _ := localconfig.Load(h.cfgPath)
	if got.ControlPlaneURL != "https://42mou.ru:18443" {
		t.Fatal("must restore old CP URL")
	}
}

func TestNormalizeControlPlaneURLStripsPath(t *testing.T) {
	got, err := configure.NormalizeControlPlaneURL("https://cp.nyxveil.ru:18443/api/v1/")
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://cp.nyxveil.ru:18443" {
		t.Fatal(got)
	}
}

func TestSelfSignedCPRejectedBySystemTrust(t *testing.T) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "cp.nyxveil.ru"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		DNSNames:     []string{"cp.nyxveil.ru"},
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	tlsCert := tls.Certificate{Certificate: [][]byte{der}, PrivateKey: priv, Leaf: cert}
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{tlsCert}})
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	d := &net.Dialer{Timeout: 3 * time.Second}
	_, err = tls.DialWithDialer(d, "tcp", "127.0.0.1:"+port, &tls.Config{
		MinVersion:         tls.VersionTLS12,
		ServerName:         "cp.nyxveil.ru",
		InsecureSkipVerify: false,
	})
	if err == nil {
		t.Fatal("self-signed must fail SystemTrust")
	}
}

type cpURLHarness struct {
	dir, state, cfgPath, nodeKey, liveCert, liveKey string
	nodeID                                          string
}

func setupCPURLHarness(t *testing.T) *cpURLHarness {
	t.Helper()
	dir, err := os.MkdirTemp("", "nyxveil-cpurl-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	state := filepath.Join(dir, "state")
	_ = os.MkdirAll(state, 0o700)
	h := &cpURLHarness{
		dir:      dir,
		state:    state,
		cfgPath:  filepath.Join(dir, "server.json"),
		nodeKey:  filepath.Join(state, "node.key"),
		liveCert: filepath.Join(state, "tls.crt"),
		liveKey:  filepath.Join(state, "tls.key"),
		nodeID:   "nv-test-227e939e",
	}
	k, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	if err := k.Save(h.nodeKey); err != nil {
		t.Fatal(err)
	}
	writeIPSelfSigned(t, h.liveCert, h.liveKey, "46.8.218.27")
	cfg := &localconfig.File{
		ControlPlaneURL:     "https://42mou.ru:18443",
		ControlPlaneSPKIPin: "aabbccddeeff00112233445566778899aabbccddeeff00112233445566778899",
		NodeID:              h.nodeID,
		LocationID:          "fi-helsinki",
		PublicHost:          "fi-hel-01.nyxveil.ru",
		ServerName:          "fi-hel-01.nyxveil.ru",
		DNSServers:          []string{"1.1.1.1", "1.0.0.1"},
		TLSCertFile:         h.liveCert,
		TLSKeyFile:          h.liveKey,
		TLSListen:           "0.0.0.0:443",
		QUICListen:          "0.0.0.0:443",
	}
	if err := configure.AtomicSave(h.cfgPath, cfg); err != nil {
		t.Fatal(err)
	}
	return h
}

func (h *cpURLHarness) baseOpts(mut ...func(*configure.Options)) configure.Options {
	o := configure.Options{
		ConfigPath:  h.cfgPath,
		NodeKeyPath: h.nodeKey,
		StateDir:    h.state,
		SkipFW:      true,
		SkipSvc:     true,
		SkipCP:      true,
	}
	for _, m := range mut {
		m(&o)
	}
	return o
}

func (h *cpURLHarness) successOpts(mut ...func(*configure.Options)) configure.Options {
	opts := h.baseOpts(func(o *configure.Options) {
		o.ControlPlaneURL = "https://cp.nyxveil.ru:18443"
		o.SkipCP = false
		o.ExecProbeCP = func(ctx context.Context, u string) (*configure.ControlPlaneProbeResult, error) {
			return &configure.ControlPlaneProbeResult{
				URL: u, TLSOK: true, AuthOK: true, NodeID: h.nodeID, LocationID: "fi-helsinki",
			}, nil
		}
		o.ExecRegister = func(p string) error { return nil }
		o.ExecVerifyCatalog = func(ctx context.Context, cfgPath string) (*configure.CatalogFreshness, error) {
			spki, _ := configure.CurrentSPKIHex(h.liveCert, h.liveKey)
			return &configure.CatalogFreshness{
				Verified: true, NodeID: h.nodeID, LocationID: "fi-helsinki",
				ServerVersion: version.ServerVersion, SPKIHex: spki, Online: true,
			}, nil
		}
	})
	for _, m := range mut {
		m(&opts)
	}
	return opts
}

func mustReadCP(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
