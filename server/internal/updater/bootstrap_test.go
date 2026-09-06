package updater_test

import (
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/nyxveil/server/internal/filemeta"
	"github.com/nyxveil/server/internal/updater"
)

func TestBootstrapCLIReplacesOnlyCtl(t *testing.T) {
	dir := tempDir(t)
	server := filepath.Join(dir, "nyxveil-server")
	ctl := filepath.Join(dir, "nyxveilctl")
	state := filepath.Join(dir, "state")
	_ = os.MkdirAll(state, 0o700)
	tlsKey := filepath.Join(state, "tls.key")
	tlsCert := filepath.Join(state, "tls.crt")
	_ = os.WriteFile(server, []byte("SERVER-1.0.3"), 0o755)
	_ = os.WriteFile(ctl, []byte("CTL-1.0.3"), 0o755)
	_ = os.WriteFile(tlsKey, []byte("GOOD-KEY"), 0o600)
	_ = os.WriteFile(tlsCert, []byte("GOOD-CERT"), 0o644)
	serverBefore, _ := os.ReadFile(server)
	keyBefore, _ := os.ReadFile(tlsKey)

	newCtl := []byte("CTL-1.0.5")
	sumCtl := hex.EncodeToString(sha256Sum(newCtl))
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/ctl", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(newCtl) })
	m := &updater.Manifest{
		Version: "1.0.5", Arch: updater.ArchString(), MinCore: "1.0.0", MinProtocol: 1,
		Assets: []updater.Asset{
			{Name: "nyxveil-server", SHA256: strings.Repeat("a", 64), URL: "http://127.0.0.1/unused"},
			{Name: "nyxveilctl", SHA256: sumCtl, URL: ""}, // filled after server start
		},
	}
	hs := httptest.NewServer(mux)
	defer hs.Close()
	m.Assets[1].URL = hs.URL + "/ctl"
	updater.SignManifest(m, priv)
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	mux.HandleFunc("/manifest.json", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(raw) })

	res, err := updater.BootstrapCLI(updater.BootstrapCLIOpts{
		ManifestURL: hs.URL + "/manifest.json",
		WantVersion: "1.0.5",
		CtlPath:     ctl,
		PublicKey:   pub,
		HTTP:        hs.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Replaced || res.Version != "1.0.5" {
		t.Fatalf("%+v", res)
	}
	gotCtl, _ := os.ReadFile(ctl)
	if string(gotCtl) != "CTL-1.0.5" {
		t.Fatalf("ctl=%q", gotCtl)
	}
	gotServer, _ := os.ReadFile(server)
	if string(gotServer) != string(serverBefore) {
		t.Fatal("server binary must not change during bootstrap")
	}
	gotKey, _ := os.ReadFile(tlsKey)
	if string(gotKey) != string(keyBefore) {
		t.Fatal("TLS must not change during bootstrap")
	}
}

func TestBootstrapCLIBadSignatureKeepsOld(t *testing.T) {
	dir := tempDir(t)
	ctl := filepath.Join(dir, "nyxveilctl")
	_ = os.WriteFile(ctl, []byte("CTL-1.0.3"), 0o755)
	pub, _, _ := ed25519.GenerateKey(nil)
	_, other, _ := ed25519.GenerateKey(nil)

	m := &updater.Manifest{
		Version: "1.0.5", Arch: updater.ArchString(), MinCore: "1.0.0", MinProtocol: 1,
		Assets: []updater.Asset{{Name: "nyxveilctl", SHA256: strings.Repeat("b", 64), URL: "http://example/ctl"}},
	}
	updater.SignManifest(m, other)
	raw, _ := json.Marshal(m)
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(raw) }))
	defer hs.Close()

	_, err := updater.BootstrapCLI(updater.BootstrapCLIOpts{
		ManifestURL: hs.URL, WantVersion: "1.0.5", CtlPath: ctl, PublicKey: pub, HTTP: hs.Client(),
	})
	if err == nil {
		t.Fatal("expected signature reject")
	}
	got, _ := os.ReadFile(ctl)
	if string(got) != "CTL-1.0.3" {
		t.Fatal("old CLI must remain")
	}
}

func TestBootstrapCLIBadHashKeepsOld(t *testing.T) {
	dir := tempDir(t)
	ctl := filepath.Join(dir, "nyxveilctl")
	_ = os.WriteFile(ctl, []byte("CTL-1.0.3"), 0o755)
	pub, priv, _ := ed25519.GenerateKey(nil)
	mux := http.NewServeMux()
	mux.HandleFunc("/ctl", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("WRONG")) })
	hs := httptest.NewServer(mux)
	defer hs.Close()
	m := &updater.Manifest{
		Version: "1.0.5", Arch: updater.ArchString(), MinCore: "1.0.0", MinProtocol: 1,
		Assets: []updater.Asset{{Name: "nyxveilctl", SHA256: strings.Repeat("c", 64), URL: hs.URL + "/ctl"}},
	}
	updater.SignManifest(m, priv)
	raw, _ := json.Marshal(m)
	mux.HandleFunc("/m.json", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(raw) })

	_, err := updater.BootstrapCLI(updater.BootstrapCLIOpts{
		ManifestURL: hs.URL + "/m.json", WantVersion: "1.0.5", CtlPath: ctl, PublicKey: pub, HTTP: hs.Client(),
	})
	if err == nil || !strings.Contains(err.Error(), "sha256") {
		t.Fatalf("err=%v", err)
	}
	got, _ := os.ReadFile(ctl)
	if string(got) != "CTL-1.0.3" {
		t.Fatal("old CLI must remain")
	}
}

func TestBootstrapCLIAtomicRenameFailureKeepsOld(t *testing.T) {
	dir := tempDir(t)
	ctl := filepath.Join(dir, "nyxveilctl")
	_ = os.WriteFile(ctl, []byte("CTL-1.0.3"), 0o755)
	pub, priv, _ := ed25519.GenerateKey(nil)
	newCtl := []byte("CTL-1.0.5")
	sum := hex.EncodeToString(sha256Sum(newCtl))
	mux := http.NewServeMux()
	mux.HandleFunc("/ctl", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(newCtl) })
	hs := httptest.NewServer(mux)
	defer hs.Close()
	m := &updater.Manifest{
		Version: "1.0.5", Arch: updater.ArchString(), MinCore: "1.0.0", MinProtocol: 1,
		Assets: []updater.Asset{{Name: "nyxveilctl", SHA256: sum, URL: hs.URL + "/ctl"}},
	}
	updater.SignManifest(m, priv)
	raw, _ := json.Marshal(m)
	mux.HandleFunc("/m.json", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(raw) })

	_, err := updater.BootstrapCLI(updater.BootstrapCLIOpts{
		ManifestURL: hs.URL + "/m.json", WantVersion: "1.0.5", CtlPath: ctl, PublicKey: pub, HTTP: hs.Client(),
		AtomicInstall: func(src, dest string) error { return os.ErrPermission },
	})
	if err == nil {
		t.Fatal("expected install failure")
	}
	got, _ := os.ReadFile(ctl)
	if string(got) != "CTL-1.0.3" {
		t.Fatal("old CLI must remain on rename failure")
	}
}

func TestLegacy103To105BootstrapThenUpdate(t *testing.T) {
	dir := tempDir(t)
	state := filepath.Join(dir, "state")
	_ = os.MkdirAll(state, 0o700)
	server := filepath.Join(dir, "nyxveil-server")
	ctl := filepath.Join(dir, "nyxveilctl")
	prev := filepath.Join(state, "nyxveil-server.prev")
	ctlPrev := filepath.Join(state, "nyxveilctl.prev")
	tlsKey := filepath.Join(state, "tls.key")
	tlsCert := filepath.Join(state, "tls.crt")
	cfg := filepath.Join(dir, "server.json")
	_ = os.WriteFile(server, []byte("SERVER-1.0.3"), 0o755)
	_ = os.WriteFile(ctl, []byte("CTL-1.0.3"), 0o755)
	_ = os.WriteFile(tlsKey, []byte("GOOD-KEY"), 0o600)
	_ = os.WriteFile(tlsCert, []byte("GOOD-CERT"), 0o644)
	_ = os.WriteFile(cfg, []byte(`{"node_id":"nv-test","location_id":"fi-helsinki"}`), 0o644)

	newServer := []byte("SERVER-1.0.5")
	newCtl := []byte("CTL-1.0.5")
	sumS := hex.EncodeToString(sha256Sum(newServer))
	sumC := hex.EncodeToString(sha256Sum(newCtl))
	pub, priv, _ := ed25519.GenerateKey(nil)

	mux := http.NewServeMux()
	mux.HandleFunc("/server", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(newServer) })
	mux.HandleFunc("/ctl", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(newCtl) })
	hs := httptest.NewServer(mux)
	defer hs.Close()

	m := &updater.Manifest{
		Version: "1.0.5", Arch: updater.ArchString(), MinCore: "1.0.0", MinProtocol: 1,
		Assets: []updater.Asset{
			{Name: "nyxveil-server", SHA256: sumS, URL: hs.URL + "/server"},
			{Name: "nyxveilctl", SHA256: sumC, URL: hs.URL + "/ctl"},
		},
	}
	updater.SignManifest(m, priv)
	raw, _ := json.Marshal(m)
	mux.HandleFunc("/manifest.json", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(raw) })
	manifestURL := hs.URL + "/manifest.json"

	if _, err := updater.BootstrapCLI(updater.BootstrapCLIOpts{
		ManifestURL: manifestURL, WantVersion: "1.0.5", CtlPath: ctl, PublicKey: pub, HTTP: hs.Client(),
	}); err != nil {
		t.Fatal(err)
	}
	if string(mustRead(t, server)) != "SERVER-1.0.3" {
		t.Fatal("server must stay 1.0.3 after bootstrap")
	}
	if string(mustRead(t, ctl)) != "CTL-1.0.5" {
		t.Fatal("ctl must be 1.0.5 after bootstrap")
	}
	if string(mustRead(t, tlsKey)) != "GOOD-KEY" {
		t.Fatal("tls mutated during bootstrap")
	}

	u := updater.New(server, prev, filepath.Join(state, "marker"))
	u.ExtraBinaries = map[string]string{"nyxveilctl": ctl}
	u.ExtraPrev = map[string]string{"nyxveilctl": ctlPrev}
	u.StateDir = state
	u.HTTP = hs.Client()
	u.PublicKey = pub
	u.EnforceOwnership = filemeta.EnforceRuntimeTLS
	parsed, err := updater.ParseManifest(raw, pub)
	if err != nil {
		t.Fatal(err)
	}
	if err := u.Apply(parsed, func() bool { return true }); err != nil {
		t.Fatal(err)
	}
	if string(mustRead(t, server)) != "SERVER-1.0.5" || string(mustRead(t, ctl)) != "CTL-1.0.5" {
		t.Fatal("final binaries not 1.0.5")
	}
	if string(mustRead(t, tlsKey)) != "GOOD-KEY" {
		t.Fatal("TLS contents lost")
	}
	if runtime.GOOS != "windows" {
		st, _ := os.Stat(tlsKey)
		if st.Mode().Perm() != 0o600 {
			t.Fatalf("tls.key mode %o", st.Mode().Perm())
		}
	}
	if !strings.Contains(string(mustRead(t, cfg)), "nv-test") {
		t.Fatal("node identity config changed")
	}
}

func TestFullUpdateFailureAfterBootstrapRestoresServer103(t *testing.T) {
	dir := tempDir(t)
	state := filepath.Join(dir, "state")
	_ = os.MkdirAll(state, 0o700)
	server := filepath.Join(dir, "nyxveil-server")
	ctl := filepath.Join(dir, "nyxveilctl")
	prev := filepath.Join(state, "nyxveil-server.prev")
	ctlPrev := filepath.Join(state, "nyxveilctl.prev")
	tlsKey := filepath.Join(state, "tls.key")
	_ = os.WriteFile(server, []byte("SERVER-1.0.3"), 0o755)
	_ = os.WriteFile(ctl, []byte("CTL-1.0.5"), 0o755)
	_ = os.WriteFile(tlsKey, []byte("GOOD-KEY"), 0o600)

	newServer := []byte("SERVER-1.0.5")
	newCtl := []byte("CTL-1.0.5-B")
	sumS := hex.EncodeToString(sha256Sum(newServer))
	sumC := hex.EncodeToString(sha256Sum(newCtl))
	pub, priv, _ := ed25519.GenerateKey(nil)
	mux := http.NewServeMux()
	mux.HandleFunc("/server", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(newServer) })
	mux.HandleFunc("/ctl", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(newCtl) })
	hs := httptest.NewServer(mux)
	defer hs.Close()
	m := &updater.Manifest{
		Version: "1.0.5", Arch: updater.ArchString(), MinCore: "1.0.0", MinProtocol: 1,
		Assets: []updater.Asset{
			{Name: "nyxveil-server", SHA256: sumS, URL: hs.URL + "/server"},
			{Name: "nyxveilctl", SHA256: sumC, URL: hs.URL + "/ctl"},
		},
	}
	updater.SignManifest(m, priv)
	raw, _ := json.Marshal(m)

	u := updater.New(server, prev, filepath.Join(state, "marker"))
	u.ExtraBinaries = map[string]string{"nyxveilctl": ctl}
	u.ExtraPrev = map[string]string{"nyxveilctl": ctlPrev}
	u.StateDir = state
	u.HTTP = hs.Client()
	u.PublicKey = pub
	u.EnforceOwnership = filemeta.EnforceRuntimeTLS
	parsed, _ := updater.ParseManifest(raw, pub)

	err := u.Apply(parsed, func() bool {
		_ = os.WriteFile(tlsKey, []byte("ROOT-KEY"), 0o600)
		return false
	})
	if err == nil {
		t.Fatal("expected health rollback")
	}
	if string(mustRead(t, server)) != "SERVER-1.0.3" {
		t.Fatal("server must roll back to 1.0.3")
	}
	if string(mustRead(t, tlsKey)) != "GOOD-KEY" {
		t.Fatal("TLS must restore; never leave root rewrite")
	}
}

func mustRead(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestBootstrapCLIUpdateDoesNotRestartServer(t *testing.T) {
	// BootstrapCLI never invokes systemctl; server binary unchanged proves no restart path.
	TestBootstrapCLIReplacesOnlyCtl(t)
}

func TestBootstrapCLIUpdateDoesNotTouchConfig(t *testing.T) {
	dir := tempDir(t)
	cfg := filepath.Join(dir, "server.json")
	orig := []byte(`{"node_id":"nv-test-227e939e","location_id":"fi-helsinki"}`)
	_ = os.WriteFile(cfg, orig, 0o644)
	ctl := filepath.Join(dir, "nyxveilctl")
	_ = os.WriteFile(ctl, []byte("CTL-OLD"), 0o755)
	newCtl := []byte("CTL-NEW")
	sumCtl := hex.EncodeToString(sha256Sum(newCtl))
	pub, priv, _ := ed25519.GenerateKey(nil)
	mux := http.NewServeMux()
	mux.HandleFunc("/ctl", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(newCtl) })
	hs := httptest.NewServer(mux)
	defer hs.Close()
	m := &updater.Manifest{
		Version: "1.0.9", Arch: updater.ArchString(), MinCore: "1.0.0", MinProtocol: 1,
		Assets: []updater.Asset{{Name: "nyxveilctl", SHA256: sumCtl, URL: hs.URL + "/ctl"}},
	}
	updater.SignManifest(m, priv)
	raw, _ := json.Marshal(m)
	mux.HandleFunc("/m.json", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(raw) })
	if _, err := updater.BootstrapCLI(updater.BootstrapCLIOpts{
		ManifestURL: hs.URL + "/m.json", WantVersion: "1.0.9", CtlPath: ctl, PublicKey: pub, HTTP: hs.Client(),
	}); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(cfg)
	if string(got) != string(orig) {
		t.Fatal("config must not change")
	}
}

func TestBootstrapCLIUpdateDoesNotTouchTLS(t *testing.T) {
	TestBootstrapCLIReplacesOnlyCtl(t)
}

func TestBootstrapCLIUpdatePreservesNodeIdentity(t *testing.T) {
	TestLegacy103To105BootstrapThenUpdate(t)
}

func TestBootstrapCLIUpdateVerifiesManifestSignature(t *testing.T) {
	TestBootstrapCLIBadSignatureKeepsOld(t)
}

func TestBootstrapCLIUpdateVerifiesSHA256(t *testing.T) {
	TestBootstrapCLIBadHashKeepsOld(t)
}
