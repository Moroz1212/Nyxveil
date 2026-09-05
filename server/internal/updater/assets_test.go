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

func TestParseManifestMultiAsset(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	m := &Manifest{
		Version:     "1.0.1",
		Arch:        ArchString(),
		MinCore:     "1.0.0",
		MinProtocol: 1,
		Assets: []Asset{
			{Name: "nyxveil-server", SHA256: "aa", URL: "https://example/server"},
			{Name: "nyxveilctl", SHA256: "bb", URL: "https://example/ctl"},
		},
	}
	SignManifest(m, priv)
	raw, _ := json.Marshal(m)
	got, err := ParseManifest(raw, pub)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Assets) != 2 {
		t.Fatalf("%+v", got)
	}
}

func TestApplyMultiAsset(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serverPayload := []byte("server-v2")
	ctlPayload := []byte("ctl-v2")
	serverSum := sha256.Sum256(serverPayload)
	ctlSum := sha256.Sum256(ctlPayload)

	mux := http.NewServeMux()
	mux.HandleFunc("/server", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(serverPayload) })
	mux.HandleFunc("/ctl", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(ctlPayload) })
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	dir, err := os.MkdirTemp("", "nyxveil-multi-*")
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
	serverBin := filepath.Join(dir, "nyxveil-server")
	ctlBin := filepath.Join(dir, "nyxveilctl")
	_ = os.WriteFile(serverBin, []byte("old-server"), 0o755)
	_ = os.WriteFile(ctlBin, []byte("old-ctl"), 0o755)

	m := &Manifest{
		Version: "1.0.1", Arch: ArchString(), MinCore: "1.0.0", MinProtocol: 1,
		Assets: []Asset{
			{Name: "nyxveil-server", SHA256: hex.EncodeToString(serverSum[:]), URL: srv.URL + "/server"},
			{Name: "nyxveilctl", SHA256: hex.EncodeToString(ctlSum[:]), URL: srv.URL + "/ctl"},
		},
	}
	SignManifest(m, priv)

	u := New(serverBin, filepath.Join(dir, "server.prev"), filepath.Join(dir, "marker"))
	u.PublicKey = pub
	u.ExtraBinaries = map[string]string{"nyxveilctl": ctlBin}
	u.ExtraPrev = map[string]string{"nyxveilctl": filepath.Join(dir, "ctl.prev")}

	if err := u.Apply(m, func() bool { return true }); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(serverBin)
	if string(got) != string(serverPayload) {
		t.Fatalf("server=%q", got)
	}
	got, _ = os.ReadFile(ctlBin)
	if string(got) != string(ctlPayload) {
		t.Fatalf("ctl=%q", got)
	}
}

func TestCanonicalManifestBytesIncludesAssets(t *testing.T) {
	m := &Manifest{
		Version: "1", Arch: "linux/amd64", MinCore: "1.0.0", MinProtocol: 1,
		Assets:    []Asset{{Name: "nyxveil-server", SHA256: "ab", URL: "u"}},
		Signature: "ignore",
	}
	b := CanonicalManifestBytes(m)
	if !json.Valid(b) {
		t.Fatal("not json")
	}
	var probe map[string]any
	_ = json.Unmarshal(b, &probe)
	if _, ok := probe["assets"]; !ok {
		t.Fatalf("%s", b)
	}
	if _, ok := probe["signature"]; ok {
		t.Fatal("signature must be omitted")
	}
}
