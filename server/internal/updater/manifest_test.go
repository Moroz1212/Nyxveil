package updater

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestParseManifestVerifiesSignature(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	m := &Manifest{
		Version:     "1.0.1",
		Arch:        ArchString(),
		SHA256:      "aabb",
		URL:         "https://example/bin",
		MinCore:     "1.0.0",
		MinProtocol: 1,
	}
	SignManifest(m, priv)
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseManifest(raw, pub)
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != "1.0.1" {
		t.Fatalf("%+v", got)
	}

	m.Signature = "AAAA"
	rawBad, _ := json.Marshal(m)
	if _, err := ParseManifest(rawBad, pub); err == nil {
		t.Fatal("expected signature failure")
	}
}

func TestParseManifestRejectsPlaceholderKey(t *testing.T) {
	m := &Manifest{Version: "1", SHA256: "aa", URL: "http://x", Signature: "x"}
	raw, _ := json.Marshal(m)
	if _, err := ParseManifest(raw, UpdatePublicKey); err == nil {
		t.Fatal("expected placeholder key rejection")
	}
}

func TestApplySHAAndRollback(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte("fake-binary-v2")
	sum := sha256.Sum256(payload)
	shaHex := hex.EncodeToString(sum[:])

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(payload)
	}))
	t.Cleanup(srv.Close)

	dir, err := os.MkdirTemp("", "nyxveil-upd-test-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for i := 0; i < 5; i++ {
			if os.RemoveAll(dir) == nil {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
	})
	bin := filepath.Join(dir, "nyxveil-server")
	prev := filepath.Join(dir, "nyxveil-server.prev")
	marker := filepath.Join(dir, "rollback")
	if err := os.WriteFile(bin, []byte("old-binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	m := &Manifest{
		Version:     "1.0.1",
		Arch:        ArchString(),
		SHA256:      shaHex,
		URL:         srv.URL,
		MinCore:     "1.0.0",
		MinProtocol: 1,
	}
	SignManifest(m, priv)

	u := New(bin, prev, marker)
	u.PublicKey = pub

	if err := u.Apply(m, func() bool { return true }); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(bin)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(payload) {
		t.Fatalf("binary not replaced: %q", got)
	}
	old, err := os.ReadFile(prev)
	if err != nil {
		t.Fatal(err)
	}
	if string(old) != "old-binary" {
		t.Fatalf("backup=%q", old)
	}

	// Health fail → rollback
	payload2 := []byte("broken")
	sum2 := sha256.Sum256(payload2)
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(payload2)
	}))
	t.Cleanup(srv2.Close)
	m2 := &Manifest{
		Version: "1.0.2", Arch: ArchString(), SHA256: hex.EncodeToString(sum2[:]),
		URL: srv2.URL, MinCore: "1.0.0", MinProtocol: 1,
	}
	SignManifest(m2, priv)
	if err := u.Apply(m2, func() bool { return false }); err == nil {
		t.Fatal("expected health failure")
	}
	got, _ = os.ReadFile(bin)
	if string(got) != string(payload) {
		t.Fatalf("expected rollback to previous good binary, got %q", got)
	}
}

func TestCanonicalManifestBytesStable(t *testing.T) {
	m := &Manifest{Version: "1", Arch: "linux/amd64", SHA256: "ab", URL: "u", MinCore: "1.0.0", MinProtocol: 1, Signature: "ignore"}
	a := string(CanonicalManifestBytes(m))
	b := string(CanonicalManifestBytes(m))
	if a != b {
		t.Fatal("unstable")
	}
	if !json.Valid([]byte(a)) {
		t.Fatal("not json")
	}
}
