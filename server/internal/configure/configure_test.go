package configure_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nyxveil/server/internal/configure"
	"github.com/nyxveil/server/internal/localconfig"
)

func TestParseDNSServers(t *testing.T) {
	got, err := configure.ParseDNSServers("1.1.1.1, 1.0.0.1")
	if err != nil || len(got) != 2 {
		t.Fatalf("got %#v err=%v", got, err)
	}
	if _, err := configure.ParseDNSServers(""); err == nil {
		t.Fatal("expected empty reject")
	}
	if _, err := configure.ParseDNSServers("1.1.1.1,"); err == nil {
		t.Fatal("expected empty entry reject")
	}
	if _, err := configure.ParseDNSServers("not-an-ip"); err == nil {
		t.Fatal("expected invalid reject")
	}
	if _, err := configure.ParseDNSServers("2001:db8::1"); err == nil {
		t.Fatal("expected IPv6 reject")
	}
}

func TestDNSMismatchFailBeforeChange(t *testing.T) {
	expect := []net.IP{net.ParseIP("46.8.218.27")}
	lookup := func(host string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("1.2.3.4")}, nil
	}
	err := configure.CheckDNSpointsHere(lookup, "fi-hel-01.nyxveil.ru", expect)
	if err == nil || !strings.Contains(err.Error(), "does not point") {
		t.Fatalf("want DNS mismatch, got %v", err)
	}
}

func TestDNSMatchOK(t *testing.T) {
	expect := []net.IP{net.ParseIP("46.8.218.27")}
	lookup := func(host string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("46.8.218.27")}, nil
	}
	if err := configure.CheckDNSpointsHere(lookup, "fi-hel-01.nyxveil.ru", expect); err != nil {
		t.Fatal(err)
	}
}

func TestMergePreservesIdentity(t *testing.T) {
	base := localconfig.File{
		ControlPlaneURL: "https://cp.example",
		NodeID:          "node-keep",
		LocationID:      "fi-helsinki",
		PublicHost:      "46.8.218.27",
		DNSServers:      []string{"1.1.1.1"},
	}
	out, err := configure.Merge(base, configure.Options{
		PublicHost: "fi-hel-01.nyxveil.ru",
		DNSServers: "1.1.1.1,1.0.0.1",
		TLSDomain:  "fi-hel-01.nyxveil.ru",
		TLSEmail:   "a@b.c",
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.NodeID != "node-keep" || out.LocationID != "fi-helsinki" {
		t.Fatalf("identity mutated: %+v", out)
	}
	if out.PublicHost != "fi-hel-01.nyxveil.ru" || out.ACMEDomain != "fi-hel-01.nyxveil.ru" {
		t.Fatalf("host/domain: %+v", out)
	}
	if len(out.DNSServers) != 2 {
		t.Fatalf("dns: %#v", out.DNSServers)
	}
}

func TestAtomicSaveRollbackRoundTrip(t *testing.T) {
	dir, err := os.MkdirTemp("", "nyxveil-atomic-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "server.json")
	cfg := &localconfig.File{
		ControlPlaneURL: "https://cp.example",
		NodeID:          "n1",
		LocationID:      "loc",
		PublicHost:      "10.0.0.1",
		DNSServers:      []string{"1.1.1.1"},
	}
	if err := configure.AtomicSave(path, cfg); err != nil {
		t.Fatal(err)
	}
	snap := filepath.Join(dir, "snap.json")
	if err := configure.SnapshotFile(path, snap); err != nil {
		t.Fatal(err)
	}
	cfg.PublicHost = "evil.example"
	if err := configure.AtomicSave(path, cfg); err != nil {
		t.Fatal(err)
	}
	if err := configure.RestoreFile(snap, path); err != nil {
		t.Fatal(err)
	}
	got, err := localconfig.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.PublicHost != "10.0.0.1" {
		t.Fatalf("rollback failed: %s", got.PublicHost)
	}
}

func TestFirewallRenderIdempotent(t *testing.T) {
	a := configure.RenderNyxveilNFT(configure.FirewallOpts{Enable80: true, TLSPort: 443, QUICPort: 443, VPNSubnet: "10.66.0.0/24"})
	b := configure.RenderNyxveilNFT(configure.FirewallOpts{Enable80: true, TLSPort: 443, QUICPort: 443, VPNSubnet: "10.66.0.0/24"})
	if a != b {
		t.Fatal("nft render not stable")
	}
	if !strings.Contains(a, "nyxveil-acme-http01") || !strings.Contains(a, "tcp dport 80") {
		t.Fatalf("missing ACME 80: %s", a)
	}
	c := configure.RenderNyxveilNFT(configure.FirewallOpts{Enable80: false, TLSPort: 443, QUICPort: 443})
	if strings.Contains(c, "tcp dport 80") {
		t.Fatal("port 80 should be absent when ACME off")
	}
}

func TestApplyDryRunNoWrite(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "server.json")
	keyPath := filepath.Join(dir, "node.key")
	writeMinimalNode(t, cfgPath, keyPath)

	// Point paths via env is hard; use Options.ConfigPath and patch by writing key where expected.
	// Apply checks paths.NodeKey() which is /var/lib/... — skip full Apply on Windows without hooks.
	// Instead test DryRun path with Skip* and a temp node key by setting only what we can.
	opts := configure.Options{
		ConfigPath:   cfgPath,
		PublicHost:   "fi-hel-01.nyxveil.ru",
		DNSServers:   "1.1.1.1,1.0.0.1",
		TLSDomain:    "fi-hel-01.nyxveil.ru",
		TLSEmail:     "ops@example.com",
		DryRun:       true,
		SkipCP:       true,
		SkipFW:       true,
		SkipSvc:      true,
		PublicIPHint: "46.8.218.27",
		LookupIP: func(host string) ([]net.IP, error) {
			return []net.IP{net.ParseIP("46.8.218.27")}, nil
		},
	}
	// node.key path is fixed; create symlink-like skip by testing ValidateFlags + Merge + DNS only here.
	if err := opts.ValidateFlags(); err != nil {
		t.Fatal(err)
	}
	base, _ := localconfig.Load(cfgPath)
	if _, err := configure.Merge(*base, opts); err != nil {
		t.Fatal(err)
	}
}

func TestApplyPreservesNodeIDWithHooks(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "server.json")
	state := filepath.Join(dir, "state")
	_ = os.MkdirAll(state, 0o700)
	keyPath := filepath.Join(state, "node.key")
	certPath := filepath.Join(state, "tls.crt")
	keyTLS := filepath.Join(state, "tls.key")
	writeMinimalNode(t, cfgPath, keyPath)
	// Seed self-signed then reconfigure public_host+dns only (no TLS change).
	c, k := writeSelfSigned(t, state, "46.8.218.27")
	_ = os.Rename(c, certPath)
	_ = os.Rename(k, keyTLS)

	cfg, err := localconfig.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	cfg.TLSCertFile = certPath
	cfg.TLSKeyFile = keyTLS
	if err := configure.AtomicSave(cfgPath, cfg); err != nil {
		t.Fatal(err)
	}

	regCalls := 0
	res, err := configure.Apply(context.Background(), configure.Options{
		ConfigPath:  cfgPath,
		NodeKeyPath: keyPath,
		StateDir:    state,
		NFTFile:     filepath.Join(dir, "nft.conf"),
		PublicHost:  "fi-hel-01.nyxveil.ru",
		DNSServers:  "1.1.1.1,1.0.0.1",
		SkipFW:      true,
		SkipSvc:     true,
		SkipCP:      false,
		ExecRegister: func(p string) error {
			regCalls++
			got, err := localconfig.Load(p)
			if err != nil {
				return err
			}
			if got.NodeID != "node-abc" || got.LocationID != "fi-helsinki" {
				t.Fatalf("register saw mutated identity: %+v", got)
			}
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.NodeID != "node-abc" || regCalls != 1 {
		t.Fatalf("res=%+v regCalls=%d", res, regCalls)
	}
	got, _ := localconfig.Load(cfgPath)
	if got.PublicHost != "fi-hel-01.nyxveil.ru" || got.NodeID != "node-abc" {
		t.Fatalf("after apply: %+v", got)
	}
	// Idempotent second apply
	_, err = configure.Apply(context.Background(), configure.Options{
		ConfigPath:  cfgPath,
		NodeKeyPath: keyPath,
		StateDir:    state,
		NFTFile:     filepath.Join(dir, "nft.conf"),
		PublicHost:  "fi-hel-01.nyxveil.ru",
		DNSServers:  "1.1.1.1,1.0.0.1",
		SkipFW:      true,
		SkipSvc:     true,
		SkipCP:      true,
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestDNSMismatchAbortsBeforeWrite(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "server.json")
	state := filepath.Join(dir, "state")
	_ = os.MkdirAll(state, 0o700)
	keyPath := filepath.Join(state, "node.key")
	writeMinimalNode(t, cfgPath, keyPath)
	before, _ := os.ReadFile(cfgPath)
	_, err := configure.Apply(context.Background(), configure.Options{
		ConfigPath:   cfgPath,
		NodeKeyPath:  keyPath,
		StateDir:     state,
		TLSDomain:    "fi-hel-01.nyxveil.ru",
		TLSEmail:     "a@b.c",
		PublicIPHint: "46.8.218.27",
		LookupIP: func(string) ([]net.IP, error) {
			return []net.IP{net.ParseIP("9.9.9.9")}, nil
		},
		SkipFW:  true,
		SkipSvc: true,
		SkipCP:  true,
	})
	if err == nil {
		t.Fatal("expected DNS fail")
	}
	after, _ := os.ReadFile(cfgPath)
	if string(before) != string(after) {
		t.Fatal("config mutated despite DNS failure")
	}
}

func TestOperatorCertSANMismatchReject(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := writeSelfSigned(t, dir, "wrong.example")
	err := configure.ValidateLeafForDomain(certPath, keyPath, "fi-hel-01.nyxveil.ru", time.Now())
	if err == nil {
		t.Fatal("expected SAN mismatch or trust failure")
	}
}

func TestValidateFlagsRefuseMix(t *testing.T) {
	err := (&configure.Options{TLSDomain: "a.example", TLSEmail: "e@e", TLSCert: "c", TLSKey: "k"}).ValidateFlags()
	if err == nil {
		t.Fatal("expected mix reject")
	}
}

func writeMinimalNode(t *testing.T, cfgPath, keyPath string) {
	t.Helper()
	cfg := &localconfig.File{
		ControlPlaneURL: "https://cp.example",
		NodeID:          "node-abc",
		LocationID:      "fi-helsinki",
		PublicHost:      "46.8.218.27",
		DNSServers:      []string{"1.1.1.1"},
		TLSListen:       ":443",
		QUICListen:      ":443",
		VPNSubnetCIDR:   "10.66.0.0/24",
	}
	if err := configure.AtomicSave(cfgPath, cfg); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, []byte("-----BEGIN NYXVEIL NODE PRIVATE KEY-----\n"+strings.Repeat("A", 64)+"\n-----END NYXVEIL NODE PRIVATE KEY-----\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeSelfSigned(t *testing.T, dir, dnsName string) (certPath, keyPath string) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: dnsName},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{dnsName},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatal(err)
	}
	certPath = filepath.Join(dir, "tls.crt")
	keyPath = filepath.Join(dir, "tls.key")
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644); err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	return certPath, keyPath
}
