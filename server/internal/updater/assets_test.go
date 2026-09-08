package updater

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nyxveil/server/internal/paths"
)

func completeInternalTestAssets(m *Manifest, fallback Asset) {
	seen := map[string]bool{}
	for _, a := range m.Assets {
		seen[a.Name] = true
	}
	for _, name := range RequiredAssetNames {
		if !seen[name] {
			a := fallback
			a.Name = name
			m.Assets = append(m.Assets, a)
		}
	}
}

func mapInternalTestAssets(u *Updater, dir string) {
	if u.ExtraBinaries == nil {
		u.ExtraBinaries = map[string]string{}
	}
	if u.ExtraPrev == nil {
		u.ExtraPrev = map[string]string{}
	}
	for _, name := range RequiredAssetNames {
		if name == "nyxveil-server" {
			continue
		}
		if u.ExtraBinaries[name] == "" {
			u.ExtraBinaries[name] = filepath.Join(dir, name)
		}
		if u.ExtraPrev[name] == "" {
			u.ExtraPrev[name] = filepath.Join(dir, name+".prev")
		}
	}
	// Unit tests must never invoke host systemctl daemon-reload.
	if u.DaemonReload == nil {
		u.DaemonReload = func() error { return nil }
	}
}

func TestParseManifestMultiAsset(t *testing.T) {
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
	raw, _ := json.Marshal(m)
	got, err := ParseManifest(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Assets) != 2 {
		t.Fatalf("%+v", got)
	}
}

func TestApplyMultiAsset(t *testing.T) {
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
	completeInternalTestAssets(m, Asset{SHA256: hex.EncodeToString(ctlSum[:]), URL: srv.URL + "/ctl"})

	u := New(serverBin, filepath.Join(dir, "server.prev"), filepath.Join(dir, "marker"))
	u.ExtraBinaries = map[string]string{"nyxveilctl": ctlBin}
	u.ExtraPrev = map[string]string{"nyxveilctl": filepath.Join(dir, "ctl.prev")}
	mapInternalTestAssets(u, dir)

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

func TestManifestJSONIncludesAuthoritativeAssetFields(t *testing.T) {
	m := &Manifest{
		Version: "1", Arch: "linux/amd64", MinCore: "1.0.0", MinProtocol: 1,
		Assets: []Asset{{
			Name: "nyxveil-server", SHA256: "ab", URL: "u",
			Destination: paths.BinaryPath(), Mode: "0755", Required: true,
		}},
		Signature: "ignore",
	}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(b) {
		t.Fatal("not json")
	}
	var probe map[string]any
	_ = json.Unmarshal(b, &probe)
	if _, ok := probe["assets"]; !ok {
		t.Fatalf("%s", b)
	}
	assets := probe["assets"].([]any)
	asset := assets[0].(map[string]any)
	if asset["destination"] != paths.BinaryPath() || asset["mode"] != "0755" || asset["required"] != true {
		t.Fatalf("authoritative fields missing: %s", b)
	}
}

func TestParseMode(t *testing.T) {
	if got, err := ParseMode("0755"); err != nil || got.Perm() != 0o755 {
		t.Fatalf("ParseMode(0755)=%04o, %v", got, err)
	}
	for _, bad := range []string{"755", "0999", "06444", ""} {
		if _, err := ParseMode(bad); err == nil {
			t.Errorf("ParseMode(%q) unexpectedly succeeded", bad)
		}
	}
}
