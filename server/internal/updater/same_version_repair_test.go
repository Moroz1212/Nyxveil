package updater_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/nyxveil/server/internal/updater"
)

type sameVersionFixture struct {
	root     string
	payloads map[string][]byte
	paths    map[string]string
	manifest *updater.Manifest
	updater  *updater.Updater
}

func newSameVersionFixture(t *testing.T) *sameVersionFixture {
	t.Helper()
	root := tempRoot(t)
	payloads := map[string][]byte{
		"nyxveil-server":            []byte("server-1.1.2"),
		"nyxveilctl":                []byte("ctl-1.1.2"),
		"nyxveil-catalog-verify":    []byte("catalog-1.1.2"),
		"production-gate":           []byte("#!/usr/bin/env bash\nexit 0\n"),
		"share-version":             []byte("1.1.2\n"),
		"share-third-party-core":    []byte("frozen-core\n"),
		"nyxveil-update-service":    []byte("[Unit]\nDescription=Nyxveil signed update (oneshot)\n[Service]\nType=oneshot\nUser=root\nExecStart=/usr/local/sbin/nyxveilctl update\n"),
		"nyxveil-management-polkit": []byte("polkit.addRule(function(action, subject){ if (subject.user !== \"nyxveil\") return undefined; });\n"),
	}
	paths := map[string]string{
		"nyxveil-server":            filepath.Join(root, "usr", "local", "sbin", "nyxveil-server"),
		"nyxveilctl":                filepath.Join(root, "usr", "local", "sbin", "nyxveilctl"),
		"nyxveil-catalog-verify":    filepath.Join(root, "usr", "local", "sbin", "nyxveil-catalog-verify"),
		"production-gate":           filepath.Join(root, "usr", "local", "share", "nyxveil", "scripts", "production-gate.sh"),
		"share-version":             filepath.Join(root, "usr", "local", "share", "nyxveil", "VERSION"),
		"share-third-party-core":    filepath.Join(root, "usr", "local", "share", "nyxveil", "THIRD_PARTY_CORE.md"),
		"nyxveil-update-service":    filepath.Join(root, "etc", "systemd", "system", "nyxveil-update.service"),
		"nyxveil-management-polkit": filepath.Join(root, "etc", "polkit-1", "rules.d", "50-nyxveil-management.rules"),
	}
	mux := http.NewServeMux()
	for name, body := range payloads {
		name, body := name, body
		mux.HandleFunc("/"+name, func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write(body)
		})
	}
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	m := &updater.Manifest{
		Version: "1.1.2", Arch: updater.ArchString(), MinCore: "1.0.0", MinProtocol: 1,
	}
	signedDestinations := map[string]string{
		"nyxveil-server":            "/usr/local/sbin/nyxveil-server",
		"nyxveilctl":                "/usr/local/sbin/nyxveilctl",
		"nyxveil-catalog-verify":    "/usr/local/sbin/nyxveil-catalog-verify",
		"production-gate":           "/usr/local/share/nyxveil/scripts/production-gate.sh",
		"share-version":             "/usr/local/share/nyxveil/VERSION",
		"share-third-party-core":    "/usr/local/share/nyxveil/THIRD_PARTY_CORE.md",
		"nyxveil-update-service":    "/etc/systemd/system/nyxveil-update.service",
		"nyxveil-management-polkit": "/etc/polkit-1/rules.d/50-nyxveil-management.rules",
	}
	for _, name := range updater.RequiredAssetNames {
		mode := "0755"
		switch name {
		case "share-version", "share-third-party-core", "nyxveil-update-service", "nyxveil-management-polkit":
			mode = "0644"
		}
		m.Assets = append(m.Assets, updater.Asset{
			Name: name, SHA256: shaHex(payloads[name]), URL: server.URL + "/" + name,
			Destination: signedDestinations[name], Mode: mode, Required: true,
		})
	}
	u := updater.New(paths["nyxveil-server"], filepath.Join(root, "state", "server.prev"), filepath.Join(root, "state", "marker"))
	u.HTTP = server.Client()
	u.StateDir = filepath.Join(root, "state")
	u.EnforceOwnership = func(string) error { return nil }
	u.DaemonReload = func() error { return nil }
	u.ExtraBinaries = map[string]string{}
	u.ExtraPrev = map[string]string{}
	for _, name := range updater.RequiredAssetNames {
		if name == "nyxveil-server" {
			continue
		}
		u.ExtraBinaries[name] = paths[name]
		u.ExtraPrev[name] = filepath.Join(root, "state", name+".prev")
	}
	return &sameVersionFixture{root: root, payloads: payloads, paths: paths, manifest: m, updater: u}
}

func (f *sameVersionFixture) installAll(t *testing.T) {
	t.Helper()
	for name, body := range f.payloads {
		path := f.paths[name]
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		mode := os.FileMode(0o755)
		switch name {
		case "share-version", "share-third-party-core", "nyxveil-update-service", "nyxveil-management-polkit":
			mode = 0o644
		}
		if err := os.WriteFile(path, body, mode); err != nil {
			t.Fatal(err)
		}
	}
}

func (f *sameVersionFixture) apply(t *testing.T) {
	t.Helper()
	if err := f.updater.Apply(f.manifest, func() bool { return true }); err != nil {
		t.Fatal(err)
	}
	if err := f.updater.VerifyReleaseInstall(f.manifest); err != nil {
		t.Fatal(err)
	}
}

func TestSameVersionRepairInstallsMissingGate(t *testing.T) {
	f := newSameVersionFixture(t)
	f.installAll(t)
	if err := os.Remove(f.paths["production-gate"]); err != nil {
		t.Fatal(err)
	}
	f.apply(t)
	if got := string(mustRead(t, f.paths["production-gate"])); got != string(f.payloads["production-gate"]) {
		t.Fatalf("gate=%q", got)
	}
}

func TestSameVersionRepairInstallsCatalogVerifier(t *testing.T) {
	f := newSameVersionFixture(t)
	f.installAll(t)
	if err := os.Remove(f.paths["nyxveil-catalog-verify"]); err != nil {
		t.Fatal(err)
	}
	f.apply(t)
	if got := string(mustRead(t, f.paths["nyxveil-catalog-verify"])); got != string(f.payloads["nyxveil-catalog-verify"]) {
		t.Fatalf("catalog verifier=%q", got)
	}
}

func TestSameVersionRepairFixesWrongAuxiliaryHash(t *testing.T) {
	f := newSameVersionFixture(t)
	f.installAll(t)
	if err := os.WriteFile(f.paths["production-gate"], []byte("tampered"), 0o755); err != nil {
		t.Fatal(err)
	}
	f.apply(t)
}

func TestSameVersionRepairFixesMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix permission bits are not preserved on Windows")
	}
	f := newSameVersionFixture(t)
	f.installAll(t)
	if err := os.Chmod(f.paths["production-gate"], 0o644); err != nil {
		t.Fatal(err)
	}
	f.apply(t)
	st, err := os.Stat(f.paths["production-gate"])
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o755 {
		t.Fatalf("mode=%04o want 0755", st.Mode().Perm())
	}
}

func TestSameVersionRepairInstallsMissingUpdateUnit(t *testing.T) {
	f := newSameVersionFixture(t)
	f.installAll(t)
	if err := os.Remove(f.paths["nyxveil-update-service"]); err != nil {
		t.Fatal(err)
	}
	f.apply(t)
	if got := string(mustRead(t, f.paths["nyxveil-update-service"])); got != string(f.payloads["nyxveil-update-service"]) {
		t.Fatalf("update unit=%q", got)
	}
}

func TestSameVersionRepairInstallsMissingPolkit(t *testing.T) {
	f := newSameVersionFixture(t)
	f.installAll(t)
	if err := os.Remove(f.paths["nyxveil-management-polkit"]); err != nil {
		t.Fatal(err)
	}
	f.apply(t)
	if got := string(mustRead(t, f.paths["nyxveil-management-polkit"])); got != string(f.payloads["nyxveil-management-polkit"]) {
		t.Fatalf("polkit=%q", got)
	}
}

func TestSameVersionCompleteInstallNoOp(t *testing.T) {
	f := newSameVersionFixture(t)
	f.installAll(t)
	before := map[string]string{}
	for name, path := range f.paths {
		before[name] = shaHex(mustRead(t, path))
	}
	f.apply(t)
	for name, path := range f.paths {
		if got := shaHex(mustRead(t, path)); got != before[name] {
			t.Fatalf("%s hash changed: got %s want %s", name, got, before[name])
		}
	}
}
