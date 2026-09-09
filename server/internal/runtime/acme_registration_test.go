package runtime

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/tls"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nyxveil/server/internal/configure"
	"github.com/nyxveil/server/internal/identity"
	"github.com/nyxveil/server/internal/localconfig"
	"github.com/nyxveil/server/internal/nodetls"
	"github.com/nyxveil/server/internal/sessions"
)

// TestPrepareACMEForRegistration_NoNodeAuthAdvertise ensures fresh install path
// stages ACME and returns SPKI without calling NodeAuth /advertise before register.
func TestPrepareACMEForRegistration_NoNodeAuthAdvertise(t *testing.T) {
	h := newRotationHarness(t, true)
	var advertised int
	h.node.advertiseSPKI = func(context.Context, []byte) error {
		advertised++
		return errors.New("NodeAuth must not run before bootstrap registration")
	}
	pin, staged, err := h.node.prepareACMEForRegistration(context.Background(), h.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(pin) != 32 {
		t.Fatalf("pin len=%d", len(pin))
	}
	if staged == nil {
		t.Fatal("expected staged TLS pending activation")
	}
	if advertised != 0 {
		t.Fatalf("advertise called %d times before registration", advertised)
	}
	if _, err := os.Stat(staged.stageCert); err != nil {
		t.Fatalf("staged cert missing: %v", err)
	}
	// Live cert must remain the old leaf until post-register activate.
	live, _ := os.ReadFile(h.certPath)
	if !bytes.Equal(live, h.oldCert) {
		t.Fatal("live TLS must not commit before bootstrap registration")
	}
}

func TestPrepareACMEForRegistration_ACMEFailure(t *testing.T) {
	dir := tempDir(t)
	cfg := localconfig.File{
		ACMEDomain:  "fail.example.test",
		TLSCertFile: filepath.Join(dir, "tls.crt"),
		TLSKeyFile:  filepath.Join(dir, "tls.key"),
	}
	n := &Node{}
	n.validateStagedTLS = func(string, string, string, time.Time) error { return nil }
	n.acmeIssuer = func(context.Context, nodetls.ACMEConfig) (tls.Certificate, []byte, []byte, bool, error) {
		return tls.Certificate{}, nil, nil, false, errors.New("acme order failed")
	}
	pin, staged, err := n.prepareACMEForRegistration(context.Background(), cfg)
	if err == nil {
		t.Fatal("expected ACME failure")
	}
	if pin != nil || staged != nil {
		t.Fatal("must not return staged material on ACME failure")
	}
	stageCert, stageKey := configure.StagingTLSPaths(dir)
	if _, e := os.Stat(stageCert); e == nil {
		t.Fatal("staged cert must be cleaned after ACME failure")
	}
	if _, e := os.Stat(stageKey); e == nil {
		t.Fatal("staged key must be cleaned after ACME failure")
	}
}

func TestAdvertiseStagedSPKI_UsesHookNotRegister(t *testing.T) {
	n := &Node{}
	var got []byte
	n.advertiseSPKI = func(_ context.Context, pin []byte) error {
		got = append([]byte(nil), pin...)
		return nil
	}
	want := bytes.Repeat([]byte{0xab}, 32)
	if err := n.advertiseStagedSPKI(context.Background(), want); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("pin mismatch")
	}
}

func TestRegister_ACMEFailureAbortsBeforeCP(t *testing.T) {
	dir := tempDir(t)
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	mgr, err := sessions.New(10, "10.66.0.0/24")
	if err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(dir, "node.key")
	if err := os.WriteFile(keyPath, []byte("identity-must-survive"), 0o600); err != nil {
		t.Fatal(err)
	}
	cpCalls := 0
	n := &Node{
		opts: Options{TestMode: true},
		local: &localconfig.File{
			NodeID:      "n1",
			LocationID:  "loc1",
			PublicHost:  "node.example.test",
			ACMEDomain:  "node.example.test",
			TLSCertFile: filepath.Join(dir, "tls.crt"),
			TLSKeyFile:  filepath.Join(dir, "tls.key"),
		},
		key: &identity.NodeKey{
			Private: priv,
			Public:  pub,
		},
		keyCreated: true,
		sessions:   mgr,
	}
	n.acmeIssuer = func(context.Context, nodetls.ACMEConfig) (tls.Certificate, []byte, []byte, bool, error) {
		return tls.Certificate{}, nil, nil, false, errors.New("acme unavailable")
	}
	n.validateStagedTLS = func(string, string, string, time.Time) error { return nil }
	n.advertiseSPKI = func(context.Context, []byte) error {
		cpCalls++
		return errors.New("must not advertise before successful ACME")
	}

	_, err = n.Register(context.Background(), "bootstrap-token-test")
	if err == nil {
		t.Fatal("expected registration abort")
	}
	if !bytes.Contains([]byte(err.Error()), []byte("registration aborted")) &&
		!bytes.Contains([]byte(err.Error()), []byte("TLS/ACME preparation failed")) {
		t.Fatalf("unexpected error: %v", err)
	}
	if cpCalls != 0 {
		t.Fatalf("must abort before CP/advertise; advertise calls=%d", cpCalls)
	}
	raw, rerr := os.ReadFile(keyPath)
	if rerr != nil || string(raw) != "identity-must-survive" {
		t.Fatalf("node identity must be preserved after ACME failure: %v %q", rerr, raw)
	}
	if _, err := os.Stat(filepath.Join(dir, "tls.crt")); err == nil {
		t.Fatal("must not commit live TLS on ACME failure")
	}
}
