package updater_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nyxveil/server/internal/paths"
	"github.com/nyxveil/server/internal/updater"
)

// TestUpgradeFrom116StyleNodeTo119 proves the production upgrade path from the
// last published stable (1.1.6-style layout without management assets) to 1.1.9:
// bootstrap new ctl first, then full signed update installs management layer.
func TestUpgradeFrom116StyleNodeTo119(t *testing.T) {
	root := tempRoot(t)
	remap := func(productionPath string) string {
		return filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(productionPath, "/")))
	}

	serverPath := remap(paths.BinaryPath())
	extraDest, extraPrev := paths.DefaultExtraInstallMaps()
	for name, destination := range extraDest {
		extraDest[name] = remap(destination)
	}
	for name, previous := range extraPrev {
		extraPrev[name] = remap(previous)
	}
	ctlPath := extraDest["nyxveilctl"]
	nodeIDPath := remap(filepath.Join(paths.StateDir, "node.id"))
	nodeKeyPath := remap(paths.NodeKey())
	cfgPath := remap(paths.ServerConfig())
	tlsCert := remap(paths.TLSCert())
	tlsKey := remap(paths.TLSKey())

	mustWrite := func(path string, body []byte, mode os.FileMode) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, body, mode); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite(serverPath, []byte("nyxveil-server-1.1.6"), 0o755)
	mustWrite(ctlPath, []byte("nyxveilctl-1.1.6-old-updater"), 0o755)
	mustWrite(nodeIDPath, []byte("node-fi-hel-01\n"), 0o644)
	mustWrite(nodeKeyPath, []byte("NODE-KEY-BYTES"), 0o600)
	mustWrite(cfgPath, []byte(`{"node_id":"node-fi-hel-01","control_plane_url":"https://cp.example"}`+"\n"), 0o644)
	mustWrite(tlsCert, []byte("TLS-CERT"), 0o644)
	mustWrite(tlsKey, []byte("TLS-KEY"), 0o600)

	rootRepo := filepath.Clean(filepath.Join("..", ".."))
	unitBody, err := os.ReadFile(filepath.Join(rootRepo, "systemd", "nyxveil-update.service"))
	if err != nil {
		t.Fatal(err)
	}
	polkitBody, err := os.ReadFile(filepath.Join(rootRepo, "systemd", "50-nyxveil-management.rules"))
	if err != nil {
		t.Fatal(err)
	}
	payloads := withRequiredPayloads(map[string][]byte{
		"nyxveil-server":            []byte("nyxveil-server-1.1.9"),
		"nyxveilctl":                []byte("nyxveilctl-1.1.9-full-updater"),
		"nyxveil-catalog-verify":    []byte("nyxveil-catalog-verify-1.1.9"),
		"production-gate":           []byte("#!/usr/bin/env bash\nset -euo pipefail\nexit 0\n"),
		"share-version":             []byte("1.1.9\n"),
		"share-third-party-core":    []byte("7b13097da410c79e4ad3292642f4a7bc03e576489edb058597cc538468e63b4b\n"),
		"nyxveil-update-service":    unitBody,
		"nyxveil-management-polkit": polkitBody,
	})

	mux := http.NewServeMux()
	for name, body := range payloads {
		name, body := name, body
		mux.HandleFunc("/assets/"+name, func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write(body)
		})
	}
	httpServer := httptest.NewServer(mux)
	t.Cleanup(httpServer.Close)

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signedDestinations, _ := paths.DefaultExtraInstallMaps()
	signedDestinations["nyxveil-server"] = paths.BinaryPath()
	manifest := &updater.Manifest{
		Version: "1.1.9", Arch: updater.ArchString(), MinCore: "1.0.0", MinProtocol: 1,
	}
	for _, name := range updater.RequiredAssetNames {
		manifest.Assets = append(manifest.Assets, updater.Asset{
			Name: name, SHA256: shaHex(payloads[name]), URL: httpServer.URL + "/assets/" + name,
			Destination: signedDestinations[name], Mode: requiredAssetMode(name), Required: true,
		})
	}
	updater.SignManifest(manifest, priv)
	rawManifest, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	mux.HandleFunc("/release-manifest.json", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(rawManifest)
	})

	if _, err := updater.BootstrapCLI(updater.BootstrapCLIOpts{
		ManifestURL: httpServer.URL + "/release-manifest.json",
		WantVersion: "1.1.9",
		CtlPath:     ctlPath,
		PublicKey:   pub,
		HTTP:        httpServer.Client(),
	}); err != nil {
		t.Fatal(err)
	}
	if got := string(mustRead(t, ctlPath)); got != string(payloads["nyxveilctl"]) {
		t.Fatalf("bootstrap ctl=%q", got)
	}
	if got := string(mustRead(t, serverPath)); got != "nyxveil-server-1.1.6" {
		t.Fatalf("bootstrap must not change server: %q", got)
	}
	if _, err := os.Stat(extraDest["nyxveil-update-service"]); !os.IsNotExist(err) {
		t.Fatalf("bootstrap must not install update unit yet, err=%v", err)
	}

	full := updater.New(serverPath, remap(paths.PreviousBinary()), remap(paths.RollbackMarker()))
	full.HTTP = httpServer.Client()
	full.PublicKey = pub
	full.ExtraBinaries = extraDest
	full.ExtraPrev = extraPrev
	full.StateDir = remap(paths.StateDir)
	full.EnforceOwnership = func(string) error { return nil }
	full.DaemonReload = func() error { return nil }
	if err := full.Apply(manifest, func() bool { return true }); err != nil {
		t.Fatal(err)
	}
	if err := full.VerifyReleaseInstall(manifest); err != nil {
		t.Fatal(err)
	}

	if got := string(mustRead(t, serverPath)); got != string(payloads["nyxveil-server"]) {
		t.Fatalf("server=%q", got)
	}
	if got := strings.TrimSpace(string(mustRead(t, extraDest["share-version"]))); got != "1.1.9" {
		t.Fatalf("share version=%q", got)
	}
	unit := string(mustRead(t, extraDest["nyxveil-update-service"]))
	if !strings.Contains(unit, "Type=oneshot") || !strings.Contains(unit, "nyxveilctl update") {
		t.Fatalf("update unit=%q", unit)
	}
	rule := string(mustRead(t, extraDest["nyxveil-management-polkit"]))
	if !strings.Contains(rule, "nyxveil-update.service") || !strings.Contains(rule, `subject.user !== "nyxveil"`) {
		t.Fatalf("polkit=%q", rule)
	}

	// Identity / TLS / config preserved.
	if got := string(mustRead(t, nodeIDPath)); got != "node-fi-hel-01\n" {
		t.Fatalf("node id changed: %q", got)
	}
	if got := string(mustRead(t, nodeKeyPath)); got != "NODE-KEY-BYTES" {
		t.Fatalf("node key changed: %q", got)
	}
	if got := string(mustRead(t, cfgPath)); !strings.Contains(got, "node-fi-hel-01") {
		t.Fatalf("config changed: %q", got)
	}
	if got := string(mustRead(t, tlsCert)); got != "TLS-CERT" {
		t.Fatalf("tls cert changed: %q", got)
	}
	if got := string(mustRead(t, tlsKey)); got != "TLS-KEY" {
		t.Fatalf("tls key changed: %q", got)
	}
}
