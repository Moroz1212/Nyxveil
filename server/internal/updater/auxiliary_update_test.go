package updater_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/nyxveil/server/internal/updater"
)

func shaHex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func completeRequiredTestAssets(m *updater.Manifest, fallback updater.Asset) {
	seen := make(map[string]bool, len(m.Assets))
	for _, a := range m.Assets {
		seen[a.Name] = true
	}
	for _, name := range updater.RequiredAssetNames {
		if !seen[name] {
			a := fallback
			a.Name = name
			m.Assets = append(m.Assets, a)
		}
	}
}

func mapRequiredTestAssets(u *updater.Updater, root string) {
	if u.ExtraBinaries == nil {
		u.ExtraBinaries = map[string]string{}
	}
	if u.ExtraPrev == nil {
		u.ExtraPrev = map[string]string{}
	}
	for _, name := range updater.RequiredAssetNames {
		if name == "nyxveil-server" {
			continue
		}
		if u.ExtraBinaries[name] == "" {
			u.ExtraBinaries[name] = filepath.Join(root, name)
		}
		if u.ExtraPrev[name] == "" {
			u.ExtraPrev[name] = filepath.Join(root, name+".prev")
		}
	}
}

func tempRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "nyxveil-aux-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for i := 0; i < 8; i++ {
			if os.RemoveAll(dir) == nil {
				return
			}
			time.Sleep(25 * time.Millisecond)
		}
	})
	return dir
}

func TestUpdateInstallsProductionGate(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	payloads := map[string][]byte{
		"nyxveil-server":         []byte("server-1.1.2"),
		"nyxveilctl":             []byte("ctl-1.1.2"),
		"nyxveil-catalog-verify": []byte("catalog-1.1.2"),
		"production-gate":        []byte("#!/bin/sh\necho gate\n"),
		"share-version":          []byte("1.1.2\n"),
		"share-third-party-core": []byte("# frozen\n7b13097da410c79e4ad3292642f4a7bc03e576489edb058597cc538468e63b4b\n"),
	}
	mux := http.NewServeMux()
	for name, body := range payloads {
		n, b := name, body
		mux.HandleFunc("/"+n, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(b) })
	}
	hs := httptest.NewServer(mux)
	t.Cleanup(hs.Close)

	root := tempRoot(t)
	serverBin := filepath.Join(root, "sbin", "nyxveil-server")
	ctlBin := filepath.Join(root, "sbin", "nyxveilctl")
	catalogBin := filepath.Join(root, "sbin", "nyxveil-catalog-verify")
	gatePath := filepath.Join(root, "share", "scripts", "production-gate.sh")
	versionPath := filepath.Join(root, "share", "VERSION")
	thirdPartyPath := filepath.Join(root, "share", "THIRD_PARTY_CORE.md")
	_ = os.MkdirAll(filepath.Dir(serverBin), 0o755)
	_ = os.WriteFile(serverBin, []byte("old-server"), 0o755)
	_ = os.WriteFile(ctlBin, []byte("old-ctl"), 0o755)

	m := &updater.Manifest{
		Version: "1.1.2", Arch: updater.ArchString(), MinCore: "1.0.0", MinProtocol: 1,
	}
	for _, name := range updater.RequiredAssetNames {
		m.Assets = append(m.Assets, updater.Asset{
			Name: name, SHA256: shaHex(payloads[name]), URL: hs.URL + "/" + name,
		})
	}
	updater.SignManifest(m, priv)

	u := updater.New(serverBin, filepath.Join(root, "server.prev"), filepath.Join(root, "marker"))
	u.PublicKey = pub
	u.ExtraBinaries = map[string]string{
		"nyxveilctl":             ctlBin,
		"nyxveil-catalog-verify": catalogBin,
		"production-gate":        gatePath,
		"share-version":          versionPath,
		"share-third-party-core": thirdPartyPath,
	}
	u.ExtraPrev = map[string]string{
		"nyxveilctl":             filepath.Join(root, "ctl.prev"),
		"nyxveil-catalog-verify": filepath.Join(root, "catalog.prev"),
		"production-gate":        filepath.Join(root, "gate.prev"),
		"share-version":          filepath.Join(root, "version.prev"),
		"share-third-party-core": filepath.Join(root, "tp.prev"),
	}
	if err := u.Apply(m, func() bool { return true }); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(gatePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(payloads["production-gate"]) {
		t.Fatalf("gate=%q", got)
	}
}

func TestProductionGatePathExistsAfterUpgrade(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	payloads := map[string][]byte{
		"nyxveil-server":         []byte("S"),
		"nyxveilctl":             []byte("C"),
		"nyxveil-catalog-verify": []byte("V"),
		"production-gate":        []byte("#!/bin/sh\necho ok\n"),
		"share-version":          []byte("1.1.2\n"),
		"share-third-party-core": []byte("core\n"),
	}
	mux := http.NewServeMux()
	for name, body := range payloads {
		n, b := name, body
		mux.HandleFunc("/"+n, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(b) })
	}
	hs := httptest.NewServer(mux)
	t.Cleanup(hs.Close)

	root := tempRoot(t)
	serverBin := filepath.Join(root, "nyxveil-server")
	gatePath := filepath.Join(root, "share", "nyxveil", "scripts", "production-gate.sh")
	_ = os.WriteFile(serverBin, []byte("old"), 0o755)

	m := &updater.Manifest{Version: "1.1.2", Arch: updater.ArchString(), MinCore: "1.0.0", MinProtocol: 1}
	for _, name := range updater.RequiredAssetNames {
		m.Assets = append(m.Assets, updater.Asset{
			Name: name, SHA256: shaHex(payloads[name]), URL: hs.URL + "/" + name,
		})
	}
	updater.SignManifest(m, priv)

	u := updater.New(serverBin, filepath.Join(root, "server.prev"), filepath.Join(root, "marker"))
	u.PublicKey = pub
	u.ExtraBinaries = map[string]string{
		"nyxveilctl": filepath.Join(root, "ctl"), "nyxveil-catalog-verify": filepath.Join(root, "cat"),
		"production-gate": gatePath, "share-version": filepath.Join(root, "share", "nyxveil", "VERSION"),
		"share-third-party-core": filepath.Join(root, "share", "nyxveil", "THIRD_PARTY_CORE.md"),
	}
	u.ExtraPrev = map[string]string{
		"nyxveilctl": filepath.Join(root, "ctl.prev"), "nyxveil-catalog-verify": filepath.Join(root, "cat.prev"),
		"production-gate": filepath.Join(root, "gate.prev"), "share-version": filepath.Join(root, "ver.prev"),
		"share-third-party-core": filepath.Join(root, "tp.prev"),
	}
	if err := u.Apply(m, func() bool { return true }); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(gatePath); err != nil {
		t.Fatalf("expected production-gate at %s: %v", gatePath, err)
	}
}

func TestProductionGateExecutableAfterUpgrade(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	payloads := map[string][]byte{
		"nyxveil-server":         []byte("S"),
		"nyxveilctl":             []byte("C"),
		"nyxveil-catalog-verify": []byte("V"),
		"production-gate":        []byte("#!/bin/sh\n"),
		"share-version":          []byte("1.1.2\n"),
		"share-third-party-core": []byte("core\n"),
	}
	mux := http.NewServeMux()
	for name, body := range payloads {
		n, b := name, body
		mux.HandleFunc("/"+n, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(b) })
	}
	hs := httptest.NewServer(mux)
	t.Cleanup(hs.Close)

	root := tempRoot(t)
	serverBin := filepath.Join(root, "nyxveil-server")
	gatePath := filepath.Join(root, "scripts", "production-gate.sh")
	_ = os.WriteFile(serverBin, []byte("old"), 0o755)

	m := &updater.Manifest{Version: "1.1.2", Arch: updater.ArchString(), MinCore: "1.0.0", MinProtocol: 1}
	for _, name := range updater.RequiredAssetNames {
		m.Assets = append(m.Assets, updater.Asset{
			Name: name, SHA256: shaHex(payloads[name]), URL: hs.URL + "/" + name,
		})
	}
	updater.SignManifest(m, priv)

	u := updater.New(serverBin, filepath.Join(root, "server.prev"), filepath.Join(root, "marker"))
	u.PublicKey = pub
	u.ExtraBinaries = map[string]string{
		"nyxveilctl":             filepath.Join(root, "nyxveilctl"),
		"nyxveil-catalog-verify": filepath.Join(root, "nyxveil-catalog-verify"),
		"production-gate":        gatePath,
		"share-version":          filepath.Join(root, "VERSION"),
		"share-third-party-core": filepath.Join(root, "THIRD_PARTY_CORE.md"),
	}
	u.ExtraPrev = map[string]string{
		"nyxveilctl":             filepath.Join(root, "ctl.prev"),
		"nyxveil-catalog-verify": filepath.Join(root, "cat.prev"),
		"production-gate":        filepath.Join(root, "gate.prev"),
		"share-version":          filepath.Join(root, "ver.prev"),
		"share-third-party-core": filepath.Join(root, "tp.prev"),
	}
	if err := u.Apply(m, func() bool { return true }); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(gatePath)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && st.Mode().Perm()&0o111 == 0 {
		t.Fatalf("production-gate not executable: mode=%o", st.Mode().Perm())
	}
	got, err := os.ReadFile(gatePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(payloads["production-gate"]) {
		t.Fatalf("gate contents=%q", got)
	}
}

func TestAuxiliaryFilesHashVerified(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	good := []byte("#!/bin/sh\ntrue\n")
	mux := http.NewServeMux()
	mux.HandleFunc("/gate", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("TAMPERED")) })
	mux.HandleFunc("/server", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("S")) })
	mux.HandleFunc("/ctl", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("C")) })
	mux.HandleFunc("/cat", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("V")) })
	mux.HandleFunc("/ver", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("1.1.2\n")) })
	mux.HandleFunc("/tp", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("core\n")) })
	hs := httptest.NewServer(mux)
	t.Cleanup(hs.Close)

	root := tempRoot(t)
	serverBin := filepath.Join(root, "nyxveil-server")
	_ = os.WriteFile(serverBin, []byte("old"), 0o755)
	m := &updater.Manifest{
		Version: "1.1.2", Arch: updater.ArchString(), MinCore: "1.0.0", MinProtocol: 1,
		Assets: []updater.Asset{
			{Name: "nyxveil-server", SHA256: shaHex([]byte("S")), URL: hs.URL + "/server"},
			{Name: "nyxveilctl", SHA256: shaHex([]byte("C")), URL: hs.URL + "/ctl"},
			{Name: "nyxveil-catalog-verify", SHA256: shaHex([]byte("V")), URL: hs.URL + "/cat"},
			{Name: "production-gate", SHA256: shaHex(good), URL: hs.URL + "/gate"},
			{Name: "share-version", SHA256: shaHex([]byte("1.1.2\n")), URL: hs.URL + "/ver"},
			{Name: "share-third-party-core", SHA256: shaHex([]byte("core\n")), URL: hs.URL + "/tp"},
		},
	}
	updater.SignManifest(m, priv)
	u := updater.New(serverBin, filepath.Join(root, "server.prev"), filepath.Join(root, "marker"))
	u.PublicKey = pub
	u.ExtraBinaries = map[string]string{
		"nyxveilctl": filepath.Join(root, "ctl"), "nyxveil-catalog-verify": filepath.Join(root, "cat"),
		"production-gate": filepath.Join(root, "gate.sh"), "share-version": filepath.Join(root, "VERSION"),
		"share-third-party-core": filepath.Join(root, "TP.md"),
	}
	u.ExtraPrev = map[string]string{
		"nyxveilctl": filepath.Join(root, "ctl.prev"), "nyxveil-catalog-verify": filepath.Join(root, "cat.prev"),
		"production-gate": filepath.Join(root, "gate.prev"), "share-version": filepath.Join(root, "ver.prev"),
		"share-third-party-core": filepath.Join(root, "tp.prev"),
	}
	err = u.Apply(m, func() bool { return true })
	if err == nil {
		t.Fatal("expected sha256 mismatch for production-gate")
	}
	if _, statErr := os.Stat(filepath.Join(root, "gate.sh")); !os.IsNotExist(statErr) {
		t.Fatalf("tampered gate must not be installed: %v", statErr)
	}
}

func TestAuxiliaryFilesRollback(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	payloads := map[string][]byte{
		"nyxveil-server":         []byte("NEW-S"),
		"nyxveilctl":             []byte("NEW-C"),
		"nyxveil-catalog-verify": []byte("NEW-V"),
		"production-gate":        []byte("NEW-GATE"),
		"share-version":          []byte("1.1.2\n"),
		"share-third-party-core": []byte("core\n"),
	}
	mux := http.NewServeMux()
	for name, body := range payloads {
		n, b := name, body
		mux.HandleFunc("/"+n, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(b) })
	}
	hs := httptest.NewServer(mux)
	t.Cleanup(hs.Close)

	root := tempRoot(t)
	serverBin := filepath.Join(root, "nyxveil-server")
	ctlBin := filepath.Join(root, "nyxveilctl")
	gatePath := filepath.Join(root, "scripts", "production-gate.sh")
	_ = os.WriteFile(serverBin, []byte("OLD-S"), 0o755)
	_ = os.WriteFile(ctlBin, []byte("OLD-C"), 0o755)
	// Gate intentionally absent before upgrade.

	m := &updater.Manifest{Version: "1.1.2", Arch: updater.ArchString(), MinCore: "1.0.0", MinProtocol: 1}
	for _, name := range updater.RequiredAssetNames {
		m.Assets = append(m.Assets, updater.Asset{
			Name: name, SHA256: shaHex(payloads[name]), URL: hs.URL + "/" + name,
		})
	}
	updater.SignManifest(m, priv)

	u := updater.New(serverBin, filepath.Join(root, "server.prev"), filepath.Join(root, "marker"))
	u.PublicKey = pub
	u.ExtraBinaries = map[string]string{
		"nyxveilctl": ctlBin, "nyxveil-catalog-verify": filepath.Join(root, "catalog"),
		"production-gate": gatePath, "share-version": filepath.Join(root, "VERSION"),
		"share-third-party-core": filepath.Join(root, "TP.md"),
	}
	u.ExtraPrev = map[string]string{
		"nyxveilctl": filepath.Join(root, "ctl.prev"), "nyxveil-catalog-verify": filepath.Join(root, "cat.prev"),
		"production-gate": filepath.Join(root, "gate.prev"), "share-version": filepath.Join(root, "ver.prev"),
		"share-third-party-core": filepath.Join(root, "tp.prev"),
	}
	err = u.Apply(m, func() bool { return false })
	if err == nil {
		t.Fatal("expected health rollback")
	}
	got, _ := os.ReadFile(serverBin)
	if string(got) != "OLD-S" {
		t.Fatalf("server not rolled back: %q", got)
	}
	got, _ = os.ReadFile(ctlBin)
	if string(got) != "OLD-C" {
		t.Fatalf("ctl not rolled back: %q", got)
	}
	if _, err := os.Stat(gatePath); !os.IsNotExist(err) {
		t.Fatalf("new production-gate must be removed on rollback, err=%v", err)
	}
}

func TestUpgradePreservesNodeIdentityAndTLS(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	payloads := map[string][]byte{
		"nyxveil-server":         []byte("S2"),
		"nyxveilctl":             []byte("C2"),
		"nyxveil-catalog-verify": []byte("V2"),
		"production-gate":        []byte("G2"),
		"share-version":          []byte("1.1.2\n"),
		"share-third-party-core": []byte("core\n"),
	}
	mux := http.NewServeMux()
	for name, body := range payloads {
		n, b := name, body
		mux.HandleFunc("/"+n, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(b) })
	}
	hs := httptest.NewServer(mux)
	t.Cleanup(hs.Close)

	root := tempRoot(t)
	state := filepath.Join(root, "state")
	_ = os.MkdirAll(state, 0o700)
	cfg := []byte(`{"node_id":"nv-test","location_id":"fi-helsinki","control_plane_url":"https://cp.nyxveil.ru:18443"}`)
	_ = os.WriteFile(filepath.Join(root, "server.json"), cfg, 0o644)
	_ = os.WriteFile(filepath.Join(state, "tls.crt"), []byte("CERT"), 0o644)
	_ = os.WriteFile(filepath.Join(state, "tls.key"), []byte("KEY"), 0o600)
	_ = os.WriteFile(filepath.Join(state, "node.key"), []byte("NODEKEY"), 0o600)

	serverBin := filepath.Join(root, "nyxveil-server")
	_ = os.WriteFile(serverBin, []byte("S1"), 0o755)

	m := &updater.Manifest{Version: "1.1.2", Arch: updater.ArchString(), MinCore: "1.0.0", MinProtocol: 1}
	for _, name := range updater.RequiredAssetNames {
		m.Assets = append(m.Assets, updater.Asset{
			Name: name, SHA256: shaHex(payloads[name]), URL: hs.URL + "/" + name,
		})
	}
	updater.SignManifest(m, priv)

	u := updater.New(serverBin, filepath.Join(root, "server.prev"), filepath.Join(root, "marker"))
	u.PublicKey = pub
	u.StateDir = state
	u.EnforceOwnership = func(string) error { return nil }
	u.ExtraBinaries = map[string]string{
		"nyxveilctl": filepath.Join(root, "ctl"), "nyxveil-catalog-verify": filepath.Join(root, "cat"),
		"production-gate": filepath.Join(root, "gate.sh"), "share-version": filepath.Join(root, "VERSION"),
		"share-third-party-core": filepath.Join(root, "TP.md"),
	}
	u.ExtraPrev = map[string]string{
		"nyxveilctl": filepath.Join(root, "ctl.prev"), "nyxveil-catalog-verify": filepath.Join(root, "cat.prev"),
		"production-gate": filepath.Join(root, "gate.prev"), "share-version": filepath.Join(root, "ver.prev"),
		"share-third-party-core": filepath.Join(root, "tp.prev"),
	}
	if err := u.Apply(m, func() bool { return true }); err != nil {
		t.Fatal(err)
	}

	gotCfg, _ := os.ReadFile(filepath.Join(root, "server.json"))
	if string(gotCfg) != string(cfg) {
		t.Fatal("server.json mutated")
	}
	gotCert, _ := os.ReadFile(filepath.Join(state, "tls.crt"))
	gotKey, _ := os.ReadFile(filepath.Join(state, "tls.key"))
	gotNode, _ := os.ReadFile(filepath.Join(state, "node.key"))
	if string(gotCert) != "CERT" || string(gotKey) != "KEY" || string(gotNode) != "NODEKEY" {
		t.Fatal("TLS/identity mutated")
	}
}
