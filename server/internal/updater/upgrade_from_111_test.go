package updater_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/nyxveil/server/internal/paths"
	"github.com/nyxveil/server/internal/updater"
)

func TestUpgradeFromReal111OldCtlTo112CompleteRelease(t *testing.T) {
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
	if err := os.MkdirAll(filepath.Dir(serverPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(serverPath, []byte("nyxveil-server-1.1.1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ctlPath, []byte("nyxveilctl-1.1.1-old-updater"), 0o755); err != nil {
		t.Fatal(err)
	}

	payloads := map[string][]byte{
		"nyxveil-server":            []byte("nyxveil-server-1.1.2"),
		"nyxveilctl":                []byte("nyxveilctl-1.1.2-full-updater"),
		"nyxveil-catalog-verify":    []byte("nyxveil-catalog-verify-1.1.2"),
		"production-gate":           []byte("#!/usr/bin/env bash\nset -euo pipefail\nexit 0\n"),
		"share-version":             []byte("1.1.2\n"),
		"share-third-party-core":    []byte("frozen-core-sha\n"),
		"nyxveil-update-service":    []byte("[Unit]\nDescription=Nyxveil signed update (oneshot)\n[Service]\nType=oneshot\nUser=root\nExecStart=/usr/local/sbin/nyxveilctl update\n"),
		"nyxveil-management-polkit": []byte("polkit.addRule(function(action, subject){ if (subject.user !== \"nyxveil\") return undefined; });\n"),
	}
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
		Version: "1.1.2", Arch: updater.ArchString(), MinCore: "1.0.0", MinProtocol: 1,
	}
	for _, name := range updater.RequiredAssetNames {
		mode := "0755"
		switch name {
		case "share-version", "share-third-party-core", "nyxveil-update-service", "nyxveil-management-polkit":
			mode = "0644"
		}
		manifest.Assets = append(manifest.Assets, updater.Asset{
			Name: name, SHA256: shaHex(payloads[name]), URL: httpServer.URL + "/assets/" + name,
			Destination: signedDestinations[name], Mode: mode, Required: true,
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

	// Safe first step: replace only the old 1.1.1 ctl. Server and auxiliaries
	// remain untouched until the new updater is in place.
	if _, err := updater.BootstrapCLI(updater.BootstrapCLIOpts{
		ManifestURL: httpServer.URL + "/release-manifest.json",
		WantVersion: "1.1.2",
		CtlPath:     ctlPath,
		PublicKey:   pub,
		HTTP:        httpServer.Client(),
	}); err != nil {
		t.Fatal(err)
	}
	if got := string(mustRead(t, ctlPath)); got != string(payloads["nyxveilctl"]) {
		t.Fatalf("bootstrap ctl=%q", got)
	}
	if got := string(mustRead(t, serverPath)); got != "nyxveil-server-1.1.1" {
		t.Fatalf("bootstrap changed server: %q", got)
	}
	if _, err := os.Stat(extraDest["production-gate"]); !os.IsNotExist(err) {
		t.Fatalf("bootstrap installed gate, err=%v", err)
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

	for _, name := range updater.RequiredAssetNames {
		destination := serverPath
		if name != "nyxveil-server" {
			destination = extraDest[name]
		}
		if got := shaHex(mustRead(t, destination)); got != shaHex(payloads[name]) {
			t.Fatalf("%s hash=%s want=%s", name, got, shaHex(payloads[name]))
		}
		if runtime.GOOS != "windows" && (name == "nyxveil-catalog-verify" || name == "production-gate") {
			st, err := os.Stat(destination)
			if err != nil {
				t.Fatal(err)
			}
			if st.Mode().Perm()&0o111 == 0 {
				t.Fatalf("%s is not executable: %04o", name, st.Mode().Perm())
			}
		}
	}
	if runtime.GOOS != "windows" {
		bash, err := exec.LookPath("bash")
		if err != nil {
			t.Skip("bash required for production gate syntax check")
		}
		cmd := exec.Command(bash, "-n", extraDest["production-gate"])
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("production gate syntax: %v\n%s", err, out)
		}
	}
}
