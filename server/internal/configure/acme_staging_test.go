package configure_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
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

const targetFQDN = "fi-hel-01.nyxveil.ru"
const liveIP = "46.8.218.27"

func TestExistingNodeSelfSignedToACMEUsesStagedCertForHostnameValidation(t *testing.T) {
	h := setupACMEHarness(t)
	liveBefore, _ := os.ReadFile(h.liveCert)

	validatedPaths := []string{}
	// Wrap validation indirectly: ExecACME writes GOOD staged cert; if Apply validated live IP cert
	// against FQDN it would fail before/without using staging. Success proves staged path.
	res, err := configure.Apply(context.Background(), h.baseOpts(func(o *configure.Options) {
		o.ExecACME = func(ctx context.Context, a configure.ACMEIssueArgs) error {
			if a.StageCert == h.liveCert || a.StageKey == h.liveKey {
				t.Fatal("ACME Dest must be staging, not live TLS paths")
			}
			validatedPaths = append(validatedPaths, a.StageCert, a.StageKey)
			return writeTrustedLikeLeaf(t, a.StageCert, a.StageKey, targetFQDN, h.liveKey)
		}
	}))
	if err != nil {
		t.Fatal(err)
	}
	if res.RolledBack {
		t.Fatal("should not roll back")
	}
	if len(validatedPaths) == 0 {
		t.Fatal("ACME staging not invoked")
	}
	after, _ := os.ReadFile(h.liveCert)
	if string(after) == string(liveBefore) {
		t.Fatal("live cert should be replaced after successful staged commit")
	}
	if err := configure.ValidateLeafForDomainOpts(h.liveCert, h.liveKey, targetFQDN, time.Now(), false); err != nil {
		t.Fatalf("committed live cert must match FQDN: %v", err)
	}
}

func TestOldSelfSignedCertDoesNotFailTargetSANBeforeACME(t *testing.T) {
	h := setupACMEHarness(t)
	// Live cert is IP-only — VerifyHostname(FQDN) fails on purpose.
	if err := configure.ValidateLeafForDomainOpts(h.liveCert, h.liveKey, targetFQDN, time.Now(), false); err == nil {
		t.Fatal("fixture broken: live IP cert should not match FQDN")
	}
	_, err := configure.Apply(context.Background(), h.baseOpts(func(o *configure.Options) {
		o.ExecACME = func(ctx context.Context, a configure.ACMEIssueArgs) error {
			return writeTrustedLikeLeaf(t, a.StageCert, a.StageKey, targetFQDN, h.liveKey)
		}
	}))
	if err != nil {
		t.Fatalf("old IP SAN must not fail configure before ACME staging: %v", err)
	}
}

func TestACMEFailureLeavesOldTLSUntouched(t *testing.T) {
	h := setupACMEHarness(t)
	liveC, _ := os.ReadFile(h.liveCert)
	liveK, _ := os.ReadFile(h.liveKey)
	cfgBefore, _ := os.ReadFile(h.cfgPath)

	res, err := configure.Apply(context.Background(), h.baseOpts(func(o *configure.Options) {
		o.ExecACME = func(context.Context, configure.ACMEIssueArgs) error {
			return fmt.Errorf("acme simulated failure")
		}
	}))
	if err == nil {
		t.Fatal("expected failure")
	}
	if res == nil || !res.RolledBack {
		t.Fatalf("want rolled_back, got %+v", res)
	}
	liveC2, _ := os.ReadFile(h.liveCert)
	liveK2, _ := os.ReadFile(h.liveKey)
	cfgAfter, _ := os.ReadFile(h.cfgPath)
	if string(liveC) != string(liveC2) || string(liveK) != string(liveK2) {
		t.Fatal("live TLS mutated despite ACME failure")
	}
	if string(cfgBefore) != string(cfgAfter) {
		t.Fatal("server.json mutated despite ACME failure")
	}
}

func TestStagedCertWrongSANRollsBack(t *testing.T) {
	h := setupACMEHarness(t)
	liveC, _ := os.ReadFile(h.liveCert)
	res, err := configure.Apply(context.Background(), h.baseOpts(func(o *configure.Options) {
		o.ExecACME = func(ctx context.Context, a configure.ACMEIssueArgs) error {
			return writeTrustedLikeLeaf(t, a.StageCert, a.StageKey, "wrong.example", h.liveKey)
		}
	}))
	if err == nil {
		t.Fatal("expected SAN reject")
	}
	if !res.RolledBack {
		t.Fatal("expected rollback")
	}
	liveC2, _ := os.ReadFile(h.liveCert)
	if string(liveC) != string(liveC2) {
		t.Fatal("live cert changed on wrong SAN")
	}
}

func TestStagedCertKeyMismatchRollsBack(t *testing.T) {
	h := setupACMEHarness(t)
	liveC, _ := os.ReadFile(h.liveCert)
	res, err := configure.Apply(context.Background(), h.baseOpts(func(o *configure.Options) {
		o.ExecACME = func(ctx context.Context, a configure.ACMEIssueArgs) error {
			// Cert for FQDN with a NEW key, then overwrite stage key with unrelated key.
			if err := writeTrustedLikeLeaf(t, a.StageCert, a.StageKey, targetFQDN, ""); err != nil {
				return err
			}
			other, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
			if err != nil {
				return err
			}
			der, err := x509.MarshalPKCS8PrivateKey(other)
			if err != nil {
				return err
			}
			return os.WriteFile(a.StageKey, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600)
		}
	}))
	if err == nil {
		t.Fatal("expected key mismatch reject")
	}
	if !res.RolledBack {
		t.Fatal("expected rollback")
	}
	liveC2, _ := os.ReadFile(h.liveCert)
	if string(liveC) != string(liveC2) {
		t.Fatal("live cert changed on key mismatch")
	}
}

func TestStagedCertUntrustedRollsBack(t *testing.T) {
	h := setupACMEHarness(t)
	liveC, _ := os.ReadFile(h.liveCert)
	res, err := configure.Apply(context.Background(), h.baseOpts(func(o *configure.Options) {
		o.SkipCertTrust = false // force system trust
		o.ExecACME = func(ctx context.Context, a configure.ACMEIssueArgs) error {
			return writeTrustedLikeLeaf(t, a.StageCert, a.StageKey, targetFQDN, h.liveKey)
		}
	}))
	if err == nil {
		t.Fatal("expected untrusted staged cert reject")
	}
	if !res.RolledBack {
		t.Fatal("expected rollback")
	}
	liveC2, _ := os.ReadFile(h.liveCert)
	if string(liveC) != string(liveC2) {
		t.Fatal("live cert changed on trust failure")
	}
}

func TestSuccessfulACMEAtomicCommit(t *testing.T) {
	h := setupACMEHarness(t)
	res, err := configure.Apply(context.Background(), h.baseOpts(func(o *configure.Options) {
		o.ExecACME = func(ctx context.Context, a configure.ACMEIssueArgs) error {
			return writeTrustedLikeLeaf(t, a.StageCert, a.StageKey, targetFQDN, h.liveKey)
		}
	}))
	if err != nil {
		t.Fatal(err)
	}
	got, err := localconfig.Load(h.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if got.PublicHost != targetFQDN || got.ACMEDomain != targetFQDN || got.NodeID != "node-abc" {
		t.Fatalf("commit mismatch: %+v", got)
	}
	if res.NodeID != "node-abc" || res.LocationID != "fi-helsinki" {
		t.Fatalf("identity: %+v", res)
	}
	sc, sk := configure.StagingTLSPaths(h.state)
	if _, err := os.Stat(sc); !os.IsNotExist(err) {
		t.Fatal("staging cert should be cleaned after success")
	}
	_ = sk
}

func TestExistingLeafKeyReusePreservesSPKI(t *testing.T) {
	h := setupACMEHarness(t)
	prev, err := configure.CurrentSPKIHex(h.liveCert, h.liveKey)
	if err != nil || prev == "" {
		t.Fatal(err)
	}
	res, err := configure.Apply(context.Background(), h.baseOpts(func(o *configure.Options) {
		o.ExecACME = func(ctx context.Context, a configure.ACMEIssueArgs) error {
			return writeTrustedLikeLeaf(t, a.StageCert, a.StageKey, targetFQDN, h.liveKey)
		}
		o.SkipCP = true
	}))
	if err != nil {
		t.Fatal(err)
	}
	if res.SPKIChanged {
		t.Fatalf("SPKI should be preserved when reusing leaf key; prev=%s new=%s", res.PrevSPKI, res.NewSPKI)
	}
	got, _ := configure.CurrentSPKIHex(h.liveCert, h.liveKey)
	if !strings.EqualFold(got, prev) {
		t.Fatalf("live SPKI changed: %s -> %s", prev, got)
	}
}

func TestNewLeafKeyTriggersSameNodeReregister(t *testing.T) {
	h := setupACMEHarness(t)
	reg := 0
	res, err := configure.Apply(context.Background(), h.baseOpts(func(o *configure.Options) {
		o.SkipCP = false
		o.ExecRegister = func(p string) error {
			reg++
			got, err := localconfig.Load(p)
			if err != nil {
				return err
			}
			if got.NodeID != "node-abc" || got.LocationID != "fi-helsinki" {
				return fmt.Errorf("identity mutated")
			}
			return nil
		}
		o.ExecACME = func(ctx context.Context, a configure.ACMEIssueArgs) error {
			return writeTrustedLikeLeaf(t, a.StageCert, a.StageKey, targetFQDN, "") // new key
		}
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !res.SPKIChanged || reg != 1 || !res.Registered {
		t.Fatalf("want SPKI change + same-node register: %+v reg=%d", res, reg)
	}
}

func TestPublicHostChangeTriggersSameNodeMetadataUpdate(t *testing.T) {
	h := setupACMEHarness(t)
	reg := 0
	_, err := configure.Apply(context.Background(), configure.Options{
		ConfigPath:   h.cfgPath,
		NodeKeyPath:  h.nodeKey,
		StateDir:     h.state,
		NFTFile:      h.nft,
		PublicHost:   targetFQDN,
		DNSServers:   "1.1.1.1,1.0.0.1",
		SkipFW:       true,
		SkipSvc:      true,
		SkipCP:       false,
		ExecRegister: func(string) error { reg++; return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if reg != 1 {
		t.Fatalf("public_host change must re-register, got %d", reg)
	}
}

func TestCPReregisterFailureRestoresOldWorkingTLS(t *testing.T) {
	h := setupACMEHarness(t)
	liveC, _ := os.ReadFile(h.liveCert)
	cfgBefore, _ := os.ReadFile(h.cfgPath)
	res, err := configure.Apply(context.Background(), h.baseOpts(func(o *configure.Options) {
		o.SkipCP = false
		o.ExecRegister = func(string) error { return fmt.Errorf("cp down") }
		o.ExecACME = func(ctx context.Context, a configure.ACMEIssueArgs) error {
			return writeTrustedLikeLeaf(t, a.StageCert, a.StageKey, targetFQDN, h.liveKey)
		}
	}))
	if err == nil {
		t.Fatal("expected CP failure")
	}
	if !res.RolledBack {
		t.Fatal("expected rollback")
	}
	liveC2, _ := os.ReadFile(h.liveCert)
	cfgAfter, _ := os.ReadFile(h.cfgPath)
	if string(liveC) != string(liveC2) {
		t.Fatal("TLS not restored after CP failure")
	}
	if string(cfgBefore) != string(cfgAfter) {
		t.Fatal("config not restored after CP failure")
	}
}

func TestPort80AvailableBeforeHTTP01(t *testing.T) {
	h := setupACMEHarness(t)
	seq := []string{}
	_, err := configure.Apply(context.Background(), h.baseOpts(func(o *configure.Options) {
		o.SkipFW = false
		o.ExecFirewall = func(fo configure.FirewallOpts) error {
			if !fo.Enable80 {
				t.Fatal("Enable80 required before ACME")
			}
			seq = append(seq, "fw80")
			return configure.WriteNFTFileOnly(fo)
		}
		o.OnBeforeACME = func() { seq = append(seq, "before-acme") }
		o.ExecACME = func(ctx context.Context, a configure.ACMEIssueArgs) error {
			seq = append(seq, "acme")
			if !configure.NFTHasACME80(h.nft) {
				t.Fatal("port 80 rule missing before HTTP-01")
			}
			return writeTrustedLikeLeaf(t, a.StageCert, a.StageKey, targetFQDN, h.liveKey)
		}
	}))
	if err != nil {
		t.Fatal(err)
	}
	want := "fw80,before-acme,acme"
	got := strings.Join(seq, ",")
	if got != want {
		t.Fatalf("sequence %s want %s", got, want)
	}
}

func TestPort80PersistsAfterSuccessfulACME(t *testing.T) {
	h := setupACMEHarness(t)
	_, err := configure.Apply(context.Background(), h.baseOpts(func(o *configure.Options) {
		o.SkipFW = false
		o.ExecFirewall = configure.WriteNFTFileOnly
		o.ExecACME = func(ctx context.Context, a configure.ACMEIssueArgs) error {
			return writeTrustedLikeLeaf(t, a.StageCert, a.StageKey, targetFQDN, h.liveKey)
		}
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !configure.NFTHasACME80(h.nft) {
		t.Fatal("TCP/80 must persist after successful ACME")
	}
}

func TestRepeatedConfigureIdempotent(t *testing.T) {
	h := setupACMEHarness(t)
	opts := h.baseOpts(func(o *configure.Options) {
		o.ExecACME = func(ctx context.Context, a configure.ACMEIssueArgs) error {
			return writeTrustedLikeLeaf(t, a.StageCert, a.StageKey, targetFQDN, h.liveKey)
		}
	})
	if _, err := configure.Apply(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	cert1, _ := os.ReadFile(h.liveCert)
	cfg1, _ := os.ReadFile(h.cfgPath)
	if _, err := configure.Apply(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	cert2, _ := os.ReadFile(h.liveCert)
	cfg2, _ := os.ReadFile(h.cfgPath)
	if string(cfg1) != string(cfg2) {
		t.Fatal("config not idempotent")
	}
	// Second apply re-issues via mock; cert PEM may differ by serial but must still match FQDN.
	if err := configure.ValidateLeafForDomainOpts(h.liveCert, h.liveKey, targetFQDN, time.Now(), false); err != nil {
		t.Fatal(err)
	}
	_ = cert1
	_ = cert2
}

type acmeHarness struct {
	dir, state, cfgPath, nodeKey, liveCert, liveKey, nft string
}

func setupACMEHarness(t *testing.T) *acmeHarness {
	t.Helper()
	// Avoid t.TempDir on Windows: AV can lock freshly written PEMs and fail RemoveAll.
	dir, err := os.MkdirTemp("", "nyxveil-configure-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	state := filepath.Join(dir, "state")
	_ = os.MkdirAll(state, 0o700)
	h := &acmeHarness{
		dir:      dir,
		state:    state,
		cfgPath:  filepath.Join(dir, "server.json"),
		nodeKey:  filepath.Join(state, "node.key"),
		liveCert: filepath.Join(state, "tls.crt"),
		liveKey:  filepath.Join(state, "tls.key"),
		nft:      filepath.Join(dir, "nft.conf"),
	}
	writeMinimalNode(t, h.cfgPath, h.nodeKey)
	writeIPSelfSigned(t, h.liveCert, h.liveKey, liveIP)
	cfg, err := localconfig.Load(h.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	cfg.TLSCertFile = h.liveCert
	cfg.TLSKeyFile = h.liveKey
	if err := configure.AtomicSave(h.cfgPath, cfg); err != nil {
		t.Fatal(err)
	}
	return h
}

func (h *acmeHarness) baseOpts(mut ...func(*configure.Options)) configure.Options {
	o := configure.Options{
		ConfigPath:    h.cfgPath,
		NodeKeyPath:   h.nodeKey,
		StateDir:      h.state,
		NFTFile:       h.nft,
		PublicHost:    targetFQDN,
		TLSDomain:     targetFQDN,
		TLSEmail:      "ops@example.com",
		DNSServers:    "1.1.1.1,1.0.0.1",
		PublicIPHint:  liveIP,
		SkipFW:        true,
		SkipSvc:       true,
		SkipCP:        true,
		SkipCertTrust: true,
		LookupIP: func(string) ([]net.IP, error) {
			return []net.IP{net.ParseIP(liveIP)}, nil
		},
	}
	for _, m := range mut {
		m(&o)
	}
	return o
}

func writeIPSelfSigned(t *testing.T, certPath, keyPath, ipStr string) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ip := net.ParseIP(ipStr)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: ipStr},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{ip},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatal(err)
	}
}

// writeTrustedLikeLeaf writes a self-signed leaf for domain.
// If reuseKeyPath is set, reuse that private key (SPKI stable); else generate new.
func writeTrustedLikeLeaf(t *testing.T, certPath, keyPath, domain, reuseKeyPath string) error {
	t.Helper()
	var priv *ecdsa.PrivateKey
	if reuseKeyPath != "" {
		b, err := os.ReadFile(reuseKeyPath)
		if err != nil {
			return err
		}
		block, _ := pem.Decode(b)
		k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return err
		}
		priv = k.(*ecdsa.PrivateKey)
	} else {
		var err error
		priv, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			return err
		}
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: domain},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(90 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{domain},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		return err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(certPath), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644); err != nil {
		return err
	}
	return os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0o600)
}
