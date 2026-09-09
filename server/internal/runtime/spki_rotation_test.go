package runtime

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/nyxveil/server/internal/configure"
	"github.com/nyxveil/server/internal/localconfig"
	"github.com/nyxveil/server/internal/nodetls"
)

type rotationHarness struct {
	node               *Node
	cfg                localconfig.File
	certPath, keyPath  string
	oldKey             *ecdsa.PrivateKey
	oldCert, oldKeyPEM []byte
	events             []string
}

func newRotationHarness(t *testing.T, rotateKey bool) *rotationHarness {
	t.Helper()
	dir := tempDir(t)
	h := &rotationHarness{
		certPath: filepath.Join(dir, "tls.crt"),
		keyPath:  filepath.Join(dir, "tls.key"),
		cfg: localconfig.File{
			ACMEDomain:  "node.example.test",
			TLSCertFile: filepath.Join(dir, "tls.crt"),
			TLSKeyFile:  filepath.Join(dir, "tls.key"),
		},
	}
	h.oldKey = writeRotationLeaf(t, h.certPath, h.keyPath, h.cfg.ACMEDomain, nil, 10*24*time.Hour)
	h.oldCert, _ = os.ReadFile(h.certPath)
	h.oldKeyPEM, _ = os.ReadFile(h.keyPath)
	h.node = &Node{}
	h.node.validateStagedTLS = func(cert, key, domain string, now time.Time) error {
		return configure.ValidateLeafForDomainOpts(cert, key, domain, now, false)
	}
	h.node.acmeIssuer = func(_ context.Context, cfg nodetls.ACMEConfig) (tls.Certificate, []byte, []byte, bool, error) {
		key := h.oldKey
		if rotateKey {
			key = nil
		}
		writeRotationLeaf(t, cfg.Dest.CertFile, cfg.Dest.KeyFile, h.cfg.ACMEDomain, key, 90*24*time.Hour)
		return tls.Certificate{}, nil, nil, false, nil
	}
	h.node.advertiseSPKI = func(_ context.Context, _ []byte) error {
		h.events = append(h.events, "advertise")
		return nil
	}
	h.node.verifyCatalogSPKI = func(_ context.Context, _ []byte) error {
		h.events = append(h.events, "verify-catalog")
		return nil
	}
	h.node.commitTLS = func(sc, sk, lc, lk string) error {
		h.events = append(h.events, "activate")
		return configure.AtomicCommitTLS(sc, sk, lc, lk)
	}
	h.node.reloadTLS = func(tls.Certificate) error {
		h.events = append(h.events, "reload")
		return nil
	}
	h.node.verifyServedSPKI = func(_ context.Context, _ []byte) error {
		h.events = append(h.events, "verify-served")
		return nil
	}
	return h
}

func (h *rotationHarness) assertOldTLS(t *testing.T) {
	t.Helper()
	gotCert, _ := os.ReadFile(h.certPath)
	gotKey, _ := os.ReadFile(h.keyPath)
	if !bytes.Equal(gotCert, h.oldCert) || !bytes.Equal(gotKey, h.oldKeyPEM) {
		t.Fatal("live TLS changed")
	}
}

func TestNormalRenewStableSPKI(t *testing.T) {
	h := newRotationHarness(t, false)
	_, _, _, changed, err := h.node.issueACME(context.Background(), h.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatal("stable key unexpectedly changed SPKI")
	}
	if want := []string{"activate", "reload"}; !reflect.DeepEqual(h.events, want) {
		t.Fatalf("events=%v want=%v", h.events, want)
	}
}

func TestKeyRotationSuccessAdvertisesBeforeActivate(t *testing.T) {
	h := newRotationHarness(t, true)
	_, _, _, changed, err := h.node.issueACME(context.Background(), h.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("expected SPKI change")
	}
	want := []string{"advertise", "verify-catalog", "activate", "reload", "verify-served"}
	if !reflect.DeepEqual(h.events, want) {
		t.Fatalf("events=%v want=%v", h.events, want)
	}
}

func TestKeyRotationCPUnavailableKeepsOldTLS(t *testing.T) {
	h := newRotationHarness(t, true)
	h.node.advertiseSPKI = func(context.Context, []byte) error { return errors.New("cp unavailable") }
	if _, _, _, _, err := h.node.issueACME(context.Background(), h.cfg); err == nil {
		t.Fatal("expected error")
	}
	h.assertOldTLS(t)
}

func TestKeyRotationCatalogRejectKeepsOldTLS(t *testing.T) {
	h := newRotationHarness(t, true)
	h.node.verifyCatalogSPKI = func(context.Context, []byte) error { return errors.New("catalog pin rejected") }
	if _, _, _, _, err := h.node.issueACME(context.Background(), h.cfg); err == nil {
		t.Fatal("expected error")
	}
	h.assertOldTLS(t)
}

func TestKeyRotationCatalogVerifyFailKeepsOldTLS(t *testing.T) {
	h := newRotationHarness(t, true)
	h.node.verifyCatalogSPKI = func(context.Context, []byte) error { return errors.New("signature verification failed") }
	if _, _, _, _, err := h.node.issueACME(context.Background(), h.cfg); err == nil {
		t.Fatal("expected error")
	}
	h.assertOldTLS(t)
}

func TestKeyRotationActivationFailRollsBack(t *testing.T) {
	h := newRotationHarness(t, true)
	advertisements := 0
	h.node.advertiseSPKI = func(context.Context, []byte) error {
		advertisements++
		return nil
	}
	h.node.commitTLS = func(sc, sk, lc, lk string) error {
		if err := configure.AtomicCommitTLS(sc, sk, lc, lk); err != nil {
			return err
		}
		return errors.New("simulated activation failure")
	}
	if _, _, _, _, err := h.node.issueACME(context.Background(), h.cfg); err == nil {
		t.Fatal("expected error")
	}
	h.assertOldTLS(t)
	if advertisements != 2 {
		t.Fatalf("advertisements=%d want new then old", advertisements)
	}
}

func TestKeyRotationReloadFailRollsBackAndVerifiesOldSPKI(t *testing.T) {
	h := newRotationHarness(t, true)
	advertisements := 0
	catalogPins := 0
	servedPins := 0
	reloadCalls := 0
	h.node.advertiseSPKI = func(context.Context, []byte) error {
		advertisements++
		return nil
	}
	h.node.verifyCatalogSPKI = func(context.Context, []byte) error {
		catalogPins++
		return nil
	}
	h.node.verifyServedSPKI = func(context.Context, []byte) error {
		servedPins++
		return nil
	}
	h.node.reloadTLS = func(tls.Certificate) error {
		reloadCalls++
		if reloadCalls == 1 {
			return errors.New("reload failed")
		}
		return nil
	}
	if _, _, _, _, err := h.node.issueACME(context.Background(), h.cfg); err == nil {
		t.Fatal("expected reload failure")
	}
	h.assertOldTLS(t)
	if advertisements < 2 {
		t.Fatalf("must re-advertise old SPKI after failure; ads=%d", advertisements)
	}
	if catalogPins < 2 {
		t.Fatalf("must verify old SPKI in catalog; catalogs=%d", catalogPins)
	}
	if servedPins < 1 {
		t.Fatalf("must verify served old SPKI; served=%d", servedPins)
	}
}

func TestKeyRotationServedSPKIFailRollsBack(t *testing.T) {
	h := newRotationHarness(t, true)
	h.node.verifyServedSPKI = func(_ context.Context, pin []byte) error {
		return errors.New("served SPKI mismatch")
	}
	if _, _, _, _, err := h.node.issueACME(context.Background(), h.cfg); err == nil {
		t.Fatal("expected served SPKI failure")
	}
	h.assertOldTLS(t)
}

func TestKeyRotationOldSPKIAdvertiseFailureSurfaces(t *testing.T) {
	h := newRotationHarness(t, true)
	calls := 0
	h.node.advertiseSPKI = func(context.Context, []byte) error {
		calls++
		if calls == 1 {
			return nil
		}
		return errors.New("restore advertise failed")
	}
	h.node.commitTLS = func(sc, sk, lc, lk string) error {
		if err := configure.AtomicCommitTLS(sc, sk, lc, lk); err != nil {
			return err
		}
		return errors.New("activation failed")
	}
	_, _, _, _, err := h.node.issueACME(context.Background(), h.cfg)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "restore advertise failed") && !strings.Contains(err.Error(), "advertise") {
		t.Fatalf("must surface old-SPKI advertise failure: %v", err)
	}
}

func TestKeyRotationOldSPKICatalogVerifyFailureSurfaces(t *testing.T) {
	h := newRotationHarness(t, true)
	catalogCalls := 0
	h.node.verifyCatalogSPKI = func(context.Context, []byte) error {
		catalogCalls++
		if catalogCalls == 1 {
			return nil // new pin OK
		}
		return errors.New("old pin catalog verify failed")
	}
	h.node.commitTLS = func(sc, sk, lc, lk string) error {
		if err := configure.AtomicCommitTLS(sc, sk, lc, lk); err != nil {
			return err
		}
		return errors.New("activation failed")
	}
	_, _, _, _, err := h.node.issueACME(context.Background(), h.cfg)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "old pin catalog verify failed") && !strings.Contains(err.Error(), "catalog") {
		t.Fatalf("must surface old-SPKI catalog verify failure: %v", err)
	}
}

func TestKeyRotationRestartMidRotationRecovers(t *testing.T) {
	h := newRotationHarness(t, true)
	stageCert, stageKey := configure.StagingTLSPaths(filepath.Dir(h.keyPath))
	if err := os.WriteFile(stageCert, []byte("interrupted"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stageKey, []byte("interrupted"), 0o600); err != nil {
		t.Fatal(err)
	}
	h.node.advertiseSPKI = func(context.Context, []byte) error {
		h.assertOldTLS(t)
		return nil
	}
	if _, _, _, changed, err := h.node.issueACME(context.Background(), h.cfg); err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	if _, err := os.Stat(stageCert); !os.IsNotExist(err) {
		t.Fatal("staging cert not cleaned")
	}
}

func writeRotationLeaf(t *testing.T, certPath, keyPath, domain string, key *ecdsa.PrivateKey, lifetime time.Duration) *ecdsa.PrivateKey {
	t.Helper()
	if key == nil {
		var err error
		key, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: domain},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(lifetime),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{domain},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	return key
}
