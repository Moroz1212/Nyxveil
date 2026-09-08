package updater

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func requiredPayloads() map[string][]byte {
	return map[string][]byte{
		"nyxveil-server":            []byte("server"),
		"nyxveilctl":                []byte("ctl"),
		"nyxveil-catalog-verify":    []byte("catalog"),
		"production-gate":           []byte("#!/bin/sh\nexit 0\n"),
		"share-version":             []byte("1.1.2\n"),
		"share-third-party-core":    []byte("third party\n"),
		"nyxveil-update-service":    []byte("[Unit]\nDescription=test\n[Service]\nType=oneshot\nUser=root\nExecStart=/usr/local/sbin/nyxveilctl update\n"),
		"nyxveil-management-polkit": []byte("/* test */\npolkit.addRule(function(){ return undefined; });\n"),
	}
}

func testRequiredUpdate(t *testing.T) (*Updater, *Manifest, map[string]string) {
	t.Helper()
	payloads := requiredPayloads()
	mux := http.NewServeMux()
	for name, body := range payloads {
		name, body := name, body
		mux.HandleFunc("/"+name, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(body) })
	}
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	root := t.TempDir()
	destinations := map[string]string{}
	u := New(filepath.Join(root, "sbin", "nyxveil-server"), filepath.Join(root, "prev", "server"), filepath.Join(root, "marker"))
	u.StateDir = filepath.Join(root, "state")
	u.EnforceOwnership = func(string) error { return nil }
	u.DaemonReload = func() error { return nil }
	u.ExtraBinaries = map[string]string{}
	u.ExtraPrev = map[string]string{}
	m := &Manifest{Version: "1.1.2", Arch: ArchString(), MinCore: "1.0.0", MinProtocol: 1}
	for _, name := range RequiredAssetNames {
		body := payloads[name]
		sum := sha256.Sum256(body)
		contract := productionAssets[name]
		m.Assets = append(m.Assets, Asset{
			Name: name, SHA256: hex.EncodeToString(sum[:]), URL: server.URL + "/" + name,
			Destination: contract.destination, Mode: modeString(contract.mode), Required: true,
		})
		if name == "nyxveil-server" {
			destinations[name] = u.BinaryPath
			continue
		}
		destinations[name] = filepath.Join(root, "install", name)
		u.ExtraBinaries[name] = destinations[name]
		u.ExtraPrev[name] = filepath.Join(root, "prev", name)
	}
	return u, m, destinations
}

func modeString(mode os.FileMode) string {
	return fmt.Sprintf("%04o", mode.Perm())
}

func TestManifestDestinationsAreUnixSlashPaths(t *testing.T) {
	for name, contract := range productionAssets {
		if strings.Contains(contract.destination, `\`) {
			t.Fatalf("%s destination must be Unix slash path, got %q", name, contract.destination)
		}
		if !strings.HasPrefix(contract.destination, "/") {
			t.Fatalf("%s destination must be absolute Unix path, got %q", name, contract.destination)
		}
	}
}

func TestCatalogVerifierInstalled(t *testing.T) {
	u, m, destinations := testRequiredUpdate(t)
	if err := u.Apply(m, func() bool { return true }); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(destinations["nyxveil-catalog-verify"]); err != nil {
		t.Fatalf("catalog verifier not installed: %v", err)
	}
}

func TestMissingRequiredAuxiliaryFailsUpdate(t *testing.T) {
	u, m, _ := testRequiredUpdate(t)
	m.Assets = m.Assets[:3] // production-gate and share assets are absent.
	if err := u.Apply(m, nil); err == nil {
		t.Fatal("manifest missing required auxiliary assets must fail")
	}

	u, m, _ = testRequiredUpdate(t)
	u.ExtraBinaries = map[string]string{"nyxveilctl": filepath.Join(t.TempDir(), "nyxveilctl")}
	if err := u.Apply(m, nil); err == nil {
		t.Fatal("partial ExtraBinaries mapping must fail closed")
	}
}

func TestUpdaterSuccessRequiresAllFiles(t *testing.T) {
	u, m, _ := testRequiredUpdate(t)
	jobs, err := u.planJobs(m)
	if err != nil {
		t.Fatal(err)
	}
	for _, job := range jobs {
		if err := os.MkdirAll(filepath.Dir(job.dest), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(job.dest, requiredPayloads()[job.name], job.mode); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Remove(jobs[3].dest); err != nil {
		t.Fatal(err)
	}
	if err := VerifyCommitted(jobs); err == nil {
		t.Fatal("missing post-install file must prevent success")
	}
}

func TestReleaseConsumerFromDistOnly(t *testing.T) {
	dist := filepath.Clean(filepath.Join("..", "..", "dist", "release"))
	manifestPath := filepath.Join(dist, "release-manifest-linux-amd64.json")
	if _, err := os.Stat(manifestPath); os.IsNotExist(err) {
		t.Skip("packaged dist/release is absent")
	}
	if _, err := os.Stat(filepath.Join(dist, "production-gate.sh")); err != nil {
		t.Fatalf("dist missing production-gate.sh: %v", err)
	}
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	m, err := ParseManifest(raw, UpdatePublicKey)
	if err != nil {
		t.Skipf("dist/release manifests not production-signed (%v) — set NYXVEIL_RELEASE_SIGNING_KEY and re-package", err)
	}

	absoluteDist, err := filepath.Abs(dist)
	if err != nil {
		t.Fatal(err)
	}
	files := httptest.NewServer(http.FileServer(http.Dir(absoluteDist)))
	t.Cleanup(files.Close)
	m.Arch = ArchString()
	root := t.TempDir()
	u := New(filepath.Join(root, "usr", "local", "sbin", "nyxveil-server"), filepath.Join(root, "prev", "server"), "")
	u.StateDir = filepath.Join(root, "state")
	u.EnforceOwnership = func(string) error { return nil }
	u.DaemonReload = func() error { return nil }
	u.ExtraBinaries = map[string]string{}
	for i := range m.Assets {
		a := &m.Assets[i]
		a.URL = files.URL + "/" + releaseAssetBasename(a.Name, "amd64")
		if a.Name != "nyxveil-server" {
			u.ExtraBinaries[a.Name] = filepath.Join(root, filepath.Base(productionAssets[a.Name].destination))
		}
	}
	if err := u.Apply(m, func() bool { return true }); err != nil {
		t.Fatal(err)
	}
	gate := u.ExtraBinaries["production-gate"]
	if st, err := os.Stat(gate); err != nil {
		t.Fatalf("production gate not installed: %v", err)
	} else if runtime.GOOS != "windows" && st.Mode().Perm()&0o111 == 0 {
		t.Fatal("production gate is not executable")
	}
	if _, err := os.Stat(u.ExtraBinaries["nyxveil-catalog-verify"]); err != nil {
		t.Fatalf("catalog verifier not installed: %v", err)
	}
}

func releaseAssetBasename(name, arch string) string {
	switch name {
	case "nyxveil-server", "nyxveilctl", "nyxveil-catalog-verify":
		return name + "-linux-" + arch
	case "production-gate":
		return "production-gate.sh"
	case "share-version":
		return "VERSION"
	case "share-third-party-core":
		return "THIRD_PARTY_CORE.md"
	case "nyxveil-update-service":
		return "nyxveil-update.service"
	case "nyxveil-management-polkit":
		return "50-nyxveil-management.rules"
	default:
		return name
	}
}
