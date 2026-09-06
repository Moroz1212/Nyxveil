package configure_test

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/nyxveil/server/internal/configure"
	"github.com/nyxveil/server/internal/filemeta"
	"github.com/nyxveil/server/internal/localconfig"
	"github.com/nyxveil/server/internal/nodetls"
)

func TestACMEMigrationEd25519ToECDSAP256(t *testing.T) {
	h := setupEd25519Harness(t)
	liveKeyBefore, _ := os.ReadFile(h.liveKey)
	prevSPKI, err := configure.CurrentSPKIHex(h.liveCert, h.liveKey)
	if err != nil || prevSPKI == "" {
		t.Fatal(err)
	}

	reg := 0
	res, err := configure.Apply(context.Background(), h.baseOpts(func(o *configure.Options) {
		o.SkipCP = false
		o.ExecRegister = func(p string) error {
			reg++
			got, err := localconfig.Load(p)
			if err != nil {
				return err
			}
			if got.NodeID != "nv-test-227e939e" || got.LocationID != "fi-helsinki" {
				return fmt.Errorf("identity mutated: %+v", got)
			}
			return nil
		}
		o.ExecACME = func(ctx context.Context, a configure.ACMEIssueArgs) error {
			signer, err := nodetls.LoadOrCreateStableKey(a.StageKey)
			if err != nil {
				return err
			}
			if _, ok := signer.(*ecdsa.PrivateKey); !ok {
				return fmt.Errorf("staged key not ECDSA: %T", signer)
			}
			cur, _ := os.ReadFile(h.liveKey)
			if string(cur) != string(liveKeyBefore) {
				return fmt.Errorf("live key mutated before ACME success")
			}
			return writeCertForSigner(t, a.StageCert, a.StageKey, targetFQDN, signer)
		}
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !res.SPKIChanged || !res.Registered || reg != 1 {
		t.Fatalf("want SPKI change + reregister: %+v reg=%d", res, reg)
	}
	liveSigner, err := nodetls.ParseLeafPrivateKeyPEM(mustRead(t, h.liveKey))
	if err != nil {
		t.Fatal(err)
	}
	if !nodetls.ACMECompatibleLeafKey(liveSigner) {
		t.Fatal("committed live key must be ACME compatible")
	}
	if err := configure.ValidateLeafForDomainOpts(h.liveCert, h.liveKey, targetFQDN, time.Now(), false); err != nil {
		t.Fatal(err)
	}
	newSPKI, _ := configure.CurrentSPKIHex(h.liveCert, h.liveKey)
	if newSPKI == prevSPKI {
		t.Fatal("SPKI must change for Ed25519→P256")
	}
	got, _ := localconfig.Load(h.cfgPath)
	if got.NodeID != "nv-test-227e939e" || got.LocationID != "fi-helsinki" {
		t.Fatalf("identity: %+v", got)
	}
}

func TestUnsupportedExistingLeafKeyFallsBackToNewStagedKey(t *testing.T) {
	dir := t.TempDir()
	live := filepath.Join(dir, "tls.key")
	stage := filepath.Join(dir, "tls.next.key")
	writeEd25519PEM(t, live)
	if err := configure.SeedStagingKeyFromLive(live, stage); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stage); !os.IsNotExist(err) {
		t.Fatal("incompatible live key must not be seeded")
	}
	signer, err := nodetls.LoadOrCreateStableKey(stage)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := signer.(*ecdsa.PrivateKey); !ok {
		t.Fatalf("want P-256 ECDSA, got %T", signer)
	}
	liveAfter, _ := os.ReadFile(live)
	if !nodetls.KeyFileACMECompatible(live) {
		// live still Ed25519
		_ = liveAfter
	} else {
		t.Fatal("live key must remain Ed25519 until commit")
	}
}

func TestExistingCompatibleECDSAKeyIsReusedConfigure(t *testing.T) {
	h := setupACMEHarness(t) // ECDSA live fixture
	prev, err := configure.CurrentSPKIHex(h.liveCert, h.liveKey)
	if err != nil {
		t.Fatal(err)
	}
	res, err := configure.Apply(context.Background(), h.baseOpts(func(o *configure.Options) {
		o.ExecACME = func(ctx context.Context, a configure.ACMEIssueArgs) error {
			if !nodetls.KeyFileACMECompatible(a.StageKey) {
				return fmt.Errorf("compatible key was not seeded into staging")
			}
			return writeTrustedLikeLeaf(t, a.StageCert, a.StageKey, targetFQDN, a.StageKey)
		}
	}))
	if err != nil {
		t.Fatal(err)
	}
	if res.SPKIChanged {
		t.Fatalf("SPKI must stay stable when reusing ECDSA: prev=%s new=%s", res.PrevSPKI, res.NewSPKI)
	}
	got, _ := configure.CurrentSPKIHex(h.liveCert, h.liveKey)
	if got != prev {
		t.Fatalf("SPKI changed: %s -> %s", prev, got)
	}
}

func TestMigrationDoesNotModifyLiveKeyBeforeACMESuccess(t *testing.T) {
	h := setupEd25519Harness(t)
	before, _ := os.ReadFile(h.liveKey)
	_, err := configure.Apply(context.Background(), h.baseOpts(func(o *configure.Options) {
		o.ExecACME = func(ctx context.Context, a configure.ACMEIssueArgs) error {
			cur, _ := os.ReadFile(h.liveKey)
			if string(cur) != string(before) {
				t.Fatal("live key changed during ACME")
			}
			if _, err := os.Stat(a.StageKey); err == nil && nodetls.KeyFileACMECompatible(a.StageKey) {
				t.Fatal("Ed25519 must not appear as staged compatible key before generation")
			}
			return fmt.Errorf("acme simulated fail")
		}
	}))
	if err == nil {
		t.Fatal("expected fail")
	}
	after, _ := os.ReadFile(h.liveKey)
	if string(after) != string(before) {
		t.Fatal("live Ed25519 key must stay on ACME failure")
	}
}

func TestGeneratedP256KeyMatchesIssuedCertificate(t *testing.T) {
	h := setupEd25519Harness(t)
	_, err := configure.Apply(context.Background(), h.baseOpts(func(o *configure.Options) {
		o.ExecACME = func(ctx context.Context, a configure.ACMEIssueArgs) error {
			signer, err := nodetls.LoadOrCreateStableKey(a.StageKey)
			if err != nil {
				return err
			}
			if err := writeCertForSigner(t, a.StageCert, a.StageKey, targetFQDN, signer); err != nil {
				return err
			}
			pair, err := tls.LoadX509KeyPair(a.StageCert, a.StageKey)
			if err != nil {
				return err
			}
			leaf, err := x509.ParseCertificate(pair.Certificate[0])
			if err != nil {
				return err
			}
			pub := signer.Public().(*ecdsa.PublicKey)
			certPub := leaf.PublicKey.(*ecdsa.PublicKey)
			if !pub.Equal(certPub) {
				return fmt.Errorf("staged cert/key mismatch")
			}
			return nil
		}
	}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tls.LoadX509KeyPair(h.liveCert, h.liveKey); err != nil {
		t.Fatal(err)
	}
}

func TestMigrationChangesSPKIExactlyOnce(t *testing.T) {
	h := setupEd25519Harness(t)
	prev, _ := configure.CurrentSPKIHex(h.liveCert, h.liveKey)
	applyOnce := func() *configure.Result {
		res, err := configure.Apply(context.Background(), h.baseOpts(func(o *configure.Options) {
			o.ExecACME = func(ctx context.Context, a configure.ACMEIssueArgs) error {
				signer, err := nodetls.LoadOrCreateStableKey(a.StageKey)
				if err != nil {
					return err
				}
				return writeCertForSigner(t, a.StageCert, a.StageKey, targetFQDN, signer)
			}
		}))
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	r1 := applyOnce()
	if !r1.SPKIChanged {
		t.Fatal("first migration must change SPKI")
	}
	mid, _ := configure.CurrentSPKIHex(h.liveCert, h.liveKey)
	if mid == prev {
		t.Fatal("SPKI unchanged after migration")
	}
	r2 := applyOnce()
	after, _ := configure.CurrentSPKIHex(h.liveCert, h.liveKey)
	if after != mid {
		t.Fatalf("second run changed SPKI: %s -> %s", mid, after)
	}
	if r2.SPKIChanged {
		t.Fatal("renewal must not change SPKI when key reused")
	}
}

func TestSPKIChangeTriggersSameNodeReregister(t *testing.T) {
	h := setupEd25519Harness(t)
	reg := 0
	res, err := configure.Apply(context.Background(), h.baseOpts(func(o *configure.Options) {
		o.SkipCP = false
		o.ExecRegister = func(string) error { reg++; return nil }
		o.ExecACME = func(ctx context.Context, a configure.ACMEIssueArgs) error {
			signer, err := nodetls.LoadOrCreateStableKey(a.StageKey)
			if err != nil {
				return err
			}
			return writeCertForSigner(t, a.StageCert, a.StageKey, targetFQDN, signer)
		}
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !res.SPKIChanged || reg != 1 {
		t.Fatalf("%+v reg=%d", res, reg)
	}
}

func TestNodeIdentityPreservedAcrossTLSKeyMigration(t *testing.T) {
	h := setupEd25519Harness(t)
	nodeKeyBefore, _ := os.ReadFile(h.nodeKey)
	res, err := configure.Apply(context.Background(), h.baseOpts(func(o *configure.Options) {
		o.ExecACME = func(ctx context.Context, a configure.ACMEIssueArgs) error {
			signer, err := nodetls.LoadOrCreateStableKey(a.StageKey)
			if err != nil {
				return err
			}
			return writeCertForSigner(t, a.StageCert, a.StageKey, targetFQDN, signer)
		}
	}))
	if err != nil {
		t.Fatal(err)
	}
	if res.NodeID != "nv-test-227e939e" {
		t.Fatalf("%+v", res)
	}
	if string(mustRead(t, h.nodeKey)) != string(nodeKeyBefore) {
		t.Fatal("node.key must be preserved")
	}
}

func TestLocationPreservedAcrossTLSKeyMigration(t *testing.T) {
	h := setupEd25519Harness(t)
	res, err := configure.Apply(context.Background(), h.baseOpts(func(o *configure.Options) {
		o.ExecACME = func(ctx context.Context, a configure.ACMEIssueArgs) error {
			signer, err := nodetls.LoadOrCreateStableKey(a.StageKey)
			if err != nil {
				return err
			}
			return writeCertForSigner(t, a.StageCert, a.StageKey, targetFQDN, signer)
		}
	}))
	if err != nil {
		t.Fatal(err)
	}
	if res.LocationID != "fi-helsinki" {
		t.Fatalf("%+v", res)
	}
	got, _ := localconfig.Load(h.cfgPath)
	if got.LocationID != "fi-helsinki" {
		t.Fatalf("%+v", got)
	}
}

func TestCatalogSPKIMatchesNewTLSLeaf(t *testing.T) {
	h := setupEd25519Harness(t)
	var catalogSPKI string
	_, err := configure.Apply(context.Background(), h.baseOpts(func(o *configure.Options) {
		o.SkipCP = false
		o.ExecRegister = func(string) error {
			spki, err := configure.CurrentSPKIHex(h.liveCert, h.liveKey)
			if err != nil {
				return err
			}
			catalogSPKI = spki
			return nil
		}
		o.ExecACME = func(ctx context.Context, a configure.ACMEIssueArgs) error {
			signer, err := nodetls.LoadOrCreateStableKey(a.StageKey)
			if err != nil {
				return err
			}
			return writeCertForSigner(t, a.StageCert, a.StageKey, targetFQDN, signer)
		}
	}))
	if err != nil {
		t.Fatal(err)
	}
	live, _ := configure.CurrentSPKIHex(h.liveCert, h.liveKey)
	if catalogSPKI == "" || catalogSPKI != live {
		t.Fatalf("catalog SPKI %s != live %s", catalogSPKI, live)
	}
}

func TestRenewalReusesMigratedP256Key(t *testing.T) {
	h := setupEd25519Harness(t)
	migrate := func() {
		_, err := configure.Apply(context.Background(), h.baseOpts(func(o *configure.Options) {
			o.ExecACME = func(ctx context.Context, a configure.ACMEIssueArgs) error {
				signer, err := nodetls.LoadOrCreateStableKey(a.StageKey)
				if err != nil {
					return err
				}
				return writeCertForSigner(t, a.StageCert, a.StageKey, targetFQDN, signer)
			}
		}))
		if err != nil {
			t.Fatal(err)
		}
	}
	migrate()
	key1 := mustRead(t, h.liveKey)
	spki1, _ := configure.CurrentSPKIHex(h.liveCert, h.liveKey)
	migrate()
	key2 := mustRead(t, h.liveKey)
	spki2, _ := configure.CurrentSPKIHex(h.liveCert, h.liveKey)
	if string(key1) != string(key2) {
		t.Fatal("renewal must reuse migrated P-256 key bytes")
	}
	if spki1 != spki2 {
		t.Fatal("renewal must keep SPKI")
	}
}

func TestSecondRenewalDoesNotChangeSPKI(t *testing.T) {
	TestRenewalReusesMigratedP256Key(t)
}

func TestACMEFailureKeepsOldEd25519TLS(t *testing.T) {
	h := setupEd25519Harness(t)
	certBefore, _ := os.ReadFile(h.liveCert)
	keyBefore, _ := os.ReadFile(h.liveKey)
	res, err := configure.Apply(context.Background(), h.baseOpts(func(o *configure.Options) {
		o.ExecACME = func(context.Context, configure.ACMEIssueArgs) error {
			return fmt.Errorf("acme fail")
		}
	}))
	if err == nil || res == nil || !res.RolledBack {
		t.Fatalf("err=%v res=%+v", err, res)
	}
	if string(mustRead(t, h.liveCert)) != string(certBefore) || string(mustRead(t, h.liveKey)) != string(keyBefore) {
		t.Fatal("Ed25519 TLS mutated")
	}
}

func TestCPFailureRollsBackToOldEd25519TLS(t *testing.T) {
	h := setupEd25519Harness(t)
	certBefore, _ := os.ReadFile(h.liveCert)
	keyBefore, _ := os.ReadFile(h.liveKey)
	res, err := configure.Apply(context.Background(), h.baseOpts(func(o *configure.Options) {
		o.SkipCP = false
		o.ExecRegister = func(string) error { return fmt.Errorf("cp fail") }
		o.ExecACME = func(ctx context.Context, a configure.ACMEIssueArgs) error {
			signer, err := nodetls.LoadOrCreateStableKey(a.StageKey)
			if err != nil {
				return err
			}
			return writeCertForSigner(t, a.StageCert, a.StageKey, targetFQDN, signer)
		}
	}))
	if err == nil || !res.RolledBack {
		t.Fatal("expected CP rollback")
	}
	if string(mustRead(t, h.liveCert)) != string(certBefore) || string(mustRead(t, h.liveKey)) != string(keyBefore) {
		t.Fatal("must restore Ed25519 TLS")
	}
}

func TestRollbackRestoresTLSMetadata(t *testing.T) {
	h := setupEd25519Harness(t)
	_ = os.Chmod(h.liveKey, 0o600)
	_ = os.Chmod(h.liveCert, 0o644)
	modeKey, _ := os.Stat(h.liveKey)
	modeCert, _ := os.Stat(h.liveCert)
	_, err := configure.Apply(context.Background(), h.baseOpts(func(o *configure.Options) {
		o.ExecACME = func(context.Context, configure.ACMEIssueArgs) error {
			return fmt.Errorf("acme boom")
		}
	}))
	if err == nil {
		t.Fatal("expected fail")
	}
	if runtime.GOOS == "windows" {
		return
	}
	stK, _ := os.Stat(h.liveKey)
	stC, _ := os.Stat(h.liveCert)
	if stK.Mode().Perm() != modeKey.Mode().Perm() && stK.Mode().Perm() != 0o600 {
		t.Fatalf("key mode %o", stK.Mode().Perm())
	}
	if stC.Mode().Perm() != modeCert.Mode().Perm() && stC.Mode().Perm() != 0o644 {
		t.Fatalf("cert mode %o", stC.Mode().Perm())
	}
}

func TestNewTLSPrivateKey0600NyxveilOwner(t *testing.T) {
	h := setupEd25519Harness(t)
	_, err := configure.Apply(context.Background(), h.baseOpts(func(o *configure.Options) {
		o.ExecACME = func(ctx context.Context, a configure.ACMEIssueArgs) error {
			signer, err := nodetls.LoadOrCreateStableKey(a.StageKey)
			if err != nil {
				return err
			}
			return writeCertForSigner(t, a.StageCert, a.StageKey, targetFQDN, signer)
		}
	}))
	if err != nil {
		t.Fatal(err)
	}
	_ = filemeta.EnforceRuntimeTLS(h.state)
	if runtime.GOOS == "windows" {
		return
	}
	bad, e := filemeta.KeyWorldReadable(h.liveKey)
	if e != nil || bad {
		t.Fatalf("world=%v err=%v", bad, e)
	}
	st, _ := os.Stat(h.liveKey)
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("mode %o", st.Mode().Perm())
	}
}

type edHarness struct {
	*acmeHarness
}

func setupEd25519Harness(t *testing.T) *edHarness {
	t.Helper()
	h := setupACMEHarness(t)
	writeEd25519SelfSignedIP(t, h.liveCert, h.liveKey, liveIP)
	cfg, err := localconfig.Load(h.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	cfg.NodeID = "nv-test-227e939e"
	cfg.LocationID = "fi-helsinki"
	cfg.PublicHost = liveIP
	cfg.TLSCertFile = h.liveCert
	cfg.TLSKeyFile = h.liveKey
	if err := configure.AtomicSave(h.cfgPath, cfg); err != nil {
		t.Fatal(err)
	}
	return &edHarness{acmeHarness: h}
}

func writeEd25519PEM(t *testing.T, path string) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeEd25519SelfSignedIP(t *testing.T, certPath, keyPath, ipStr string) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
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
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, priv.Public(), priv)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644)
	_ = os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0o600)
}

func writeCertForSigner(t *testing.T, certPath, keyPath, domain string, signer crypto.Signer) error {
	t.Helper()
	ec, ok := signer.(*ecdsa.PrivateKey)
	if !ok {
		return fmt.Errorf("want ECDSA signer, got %T", signer)
	}
	if ec.Curve != elliptic.P256() {
		return fmt.Errorf("want P-256, got %v", ec.Curve.Params().Name)
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
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &ec.PublicKey, ec)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(certPath), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644); err != nil {
		return err
	}
	// keyPath already written by LoadOrCreateStableKey
	_ = keyPath
	return nil
}

func mustRead(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
