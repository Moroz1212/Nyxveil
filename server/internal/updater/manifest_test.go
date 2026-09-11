package updater

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseManifestAcceptsUnsigned(t *testing.T) {
	m := &Manifest{
		Version:     "1.0.1",
		Arch:        ArchString(),
		SHA256:      "aabb",
		URL:         "https://example/bin",
		MinCore:     "1.0.0",
		MinProtocol: 1,
	}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseManifest(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != "1.0.1" {
		t.Fatalf("%+v", got)
	}
}

func TestParseManifestIgnoresLegacySignatureField(t *testing.T) {
	m := &Manifest{
		Version:     "1.0.1",
		Arch:        ArchString(),
		SHA256:      "aabbccddeeff00112233445566778899aabbccddeeff00112233445566778899",
		URL:         "https://example/bin",
		MinCore:     "1.0.0",
		MinProtocol: 1,
		Signature:   "not-a-real-ed25519-signature-but-present-for-legacy",
	}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseManifest(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != "1.0.1" {
		t.Fatalf("%+v", got)
	}
	if got.Signature == "" {
		t.Fatal("legacy signature field should round-trip when present")
	}
}

func TestParseManifestRejectsMissingRequired(t *testing.T) {
	raw, _ := json.Marshal(&Manifest{Version: "1"})
	if _, err := ParseManifest(raw); err == nil {
		t.Fatal("expected missing fields rejection")
	}
	raw, _ = json.Marshal(&Manifest{})
	if _, err := ParseManifest(raw); err == nil {
		t.Fatal("expected missing version rejection")
	}
}

func TestApplySHAAndRollback(t *testing.T) {
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

	u := New(bin, prev, marker)
	u.StateDir = t.TempDir()

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
	if err := u.Apply(m2, func() bool { return false }); err == nil {
		t.Fatal("expected health failure")
	}
	got, _ = os.ReadFile(bin)
	if string(got) != string(payload) {
		t.Fatalf("expected rollback to previous good binary, got %q", got)
	}
}

func TestApplyRejectsWrongSHA256(t *testing.T) {
	payload := []byte("payload")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(payload)
	}))
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	bin := filepath.Join(dir, "nyxveil-server")
	if err := os.WriteFile(bin, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	m := &Manifest{
		Version: "1.0.1", Arch: ArchString(),
		SHA256: strings.Repeat("ab", 32), URL: srv.URL,
		MinCore: "1.0.0", MinProtocol: 1,
	}
	u := New(bin, filepath.Join(dir, "prev"), filepath.Join(dir, "marker"))
	u.StateDir = t.TempDir()
	if err := u.Apply(m, nil); err == nil || !strings.Contains(err.Error(), "sha256") {
		t.Fatalf("expected sha256 reject, got %v", err)
	}
	if got, _ := os.ReadFile(bin); string(got) != "old" {
		t.Fatal("binary must remain on hash failure")
	}
}

func TestApplyRejectsWrongArch(t *testing.T) {
	m := &Manifest{
		Version: "1.0.1", Arch: "not/" + ArchString(),
		SHA256: strings.Repeat("aa", 32), URL: "http://example/bin",
		MinCore: "1.0.0", MinProtocol: 1,
	}
	u := New(filepath.Join(t.TempDir(), "bin"), "", "")
	u.StateDir = t.TempDir()
	if err := u.Apply(m, nil); err == nil || !strings.Contains(strings.ToLower(err.Error()), "arch") {
		t.Fatalf("expected arch reject, got %v", err)
	}
}
