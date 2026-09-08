package updater_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nyxveil/server/internal/paths"
	"github.com/nyxveil/server/internal/updater"
)

func TestWrongManagementDestinationFailsClosed(t *testing.T) {
	payloads := withRequiredPayloads(nil)
	mux := http.NewServeMux()
	for name, body := range payloads {
		n, b := name, body
		mux.HandleFunc("/"+n, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(b) })
	}
	hs := httptest.NewServer(mux)
	t.Cleanup(hs.Close)

	m := &updater.Manifest{Version: "1.1.10", Arch: updater.ArchString(), MinCore: "1.0.0", MinProtocol: 1}
	dest, _ := paths.DefaultExtraInstallMaps()
	dest["nyxveil-server"] = paths.BinaryPath()
	for _, name := range updater.RequiredAssetNames {
		d := dest[name]
		if name == "nyxveil-update-service" {
			d = "/tmp/evil-update.service"
		}
		m.Assets = append(m.Assets, updater.Asset{
			Name: name, SHA256: shaHex(payloads[name]), URL: hs.URL + "/" + name,
			Destination: d, Mode: requiredAssetMode(name), Required: true,
		})
	}

	root := tempRoot(t)
	u := updater.New(filepath.Join(root, "server"), filepath.Join(root, "server.prev"), "")
	u.DaemonReload = func() error { return nil }
	mapRequiredTestAssets(u, root)
	if err := u.Apply(m, nil); err == nil || !strings.Contains(err.Error(), "allowlist") {
		t.Fatalf("want allowlist failure, got %v", err)
	}
}

func TestWrongManagementModeFailsClosed(t *testing.T) {
	payloads := withRequiredPayloads(nil)
	mux := http.NewServeMux()
	for name, body := range payloads {
		n, b := name, body
		mux.HandleFunc("/"+n, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(b) })
	}
	hs := httptest.NewServer(mux)
	t.Cleanup(hs.Close)

	m := &updater.Manifest{Version: "1.1.10", Arch: updater.ArchString(), MinCore: "1.0.0", MinProtocol: 1}
	dest, _ := paths.DefaultExtraInstallMaps()
	dest["nyxveil-server"] = paths.BinaryPath()
	for _, name := range updater.RequiredAssetNames {
		mode := requiredAssetMode(name)
		if name == "nyxveil-management-polkit" {
			mode = "0755"
		}
		m.Assets = append(m.Assets, updater.Asset{
			Name: name, SHA256: shaHex(payloads[name]), URL: hs.URL + "/" + name,
			Destination: dest[name], Mode: mode, Required: true,
		})
	}

	root := tempRoot(t)
	u := updater.New(filepath.Join(root, "server"), filepath.Join(root, "server.prev"), "")
	u.DaemonReload = func() error { return nil }
	mapRequiredTestAssets(u, root)
	if err := u.Apply(m, nil); err == nil || !strings.Contains(err.Error(), "mode") {
		t.Fatalf("want mode failure, got %v", err)
	}
}

func TestUnknownRequiredPrivilegedAssetFailsClosed(t *testing.T) {
	payloads := withRequiredPayloads(nil)
	mux := http.NewServeMux()
	for name, body := range payloads {
		n, b := name, body
		mux.HandleFunc("/"+n, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(b) })
	}
	hs := httptest.NewServer(mux)
	t.Cleanup(hs.Close)

	m := &updater.Manifest{Version: "1.1.10", Arch: updater.ArchString(), MinCore: "1.0.0", MinProtocol: 1}
	dest, _ := paths.DefaultExtraInstallMaps()
	dest["nyxveil-server"] = paths.BinaryPath()
	for _, name := range updater.RequiredAssetNames {
		m.Assets = append(m.Assets, updater.Asset{
			Name: name, SHA256: shaHex(payloads[name]), URL: hs.URL + "/" + name,
			Destination: dest[name], Mode: requiredAssetMode(name), Required: true,
		})
	}
	m.Assets = append(m.Assets, updater.Asset{
		Name: "evil-root-writer", SHA256: shaHex([]byte("x")), URL: hs.URL + "/nyxveilctl",
		Destination: "/etc/passwd", Mode: "0644", Required: true,
	})

	root := tempRoot(t)
	u := updater.New(filepath.Join(root, "server"), filepath.Join(root, "server.prev"), "")
	u.DaemonReload = func() error { return nil }
	mapRequiredTestAssets(u, root)
	if err := u.Apply(m, nil); err == nil || !strings.Contains(err.Error(), "allowlist") {
		t.Fatalf("want unknown required asset failure, got %v", err)
	}
}

func TestManagementAssetRollbackRemovesNewFiles(t *testing.T) {
	payloads := withRequiredPayloads(nil)
	mux := http.NewServeMux()
	for name, body := range payloads {
		n, b := name, body
		mux.HandleFunc("/"+n, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(b) })
	}
	hs := httptest.NewServer(mux)
	t.Cleanup(hs.Close)

	root := tempRoot(t)
	serverBin := filepath.Join(root, "server")
	_ = os.WriteFile(serverBin, []byte("old"), 0o755)

	m := &updater.Manifest{Version: "1.1.10", Arch: updater.ArchString(), MinCore: "1.0.0", MinProtocol: 1}
	for _, name := range updater.RequiredAssetNames {
		m.Assets = append(m.Assets, updater.Asset{
			Name: name, SHA256: shaHex(payloads[name]), URL: hs.URL + "/" + name,
			Mode: requiredAssetMode(name), Required: true,
		})
	}

	reloadCalls := 0
	u := updater.New(serverBin, filepath.Join(root, "server.prev"), "")
	u.DaemonReload = func() error {
		reloadCalls++
		return nil
	}
	mapRequiredTestAssets(u, root)

	err := u.Apply(m, func() bool { return false })
	if err == nil {
		t.Fatal("expected health failure")
	}
	for _, name := range []string{"nyxveil-update-service", "nyxveil-management-polkit"} {
		if _, err := os.Stat(u.ExtraBinaries[name]); !os.IsNotExist(err) {
			t.Fatalf("%s should be removed on rollback, err=%v", name, err)
		}
	}
	if reloadCalls < 2 {
		t.Fatalf("expected daemon-reload on commit and rollback, got %d", reloadCalls)
	}
}

func TestDaemonReloadInvokedAfterUpdateUnitInstall(t *testing.T) {
	payloads := withRequiredPayloads(nil)
	mux := http.NewServeMux()
	for name, body := range payloads {
		n, b := name, body
		mux.HandleFunc("/"+n, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(b) })
	}
	hs := httptest.NewServer(mux)
	t.Cleanup(hs.Close)

	root := tempRoot(t)
	serverBin := filepath.Join(root, "server")
	_ = os.WriteFile(serverBin, []byte("old"), 0o755)

	m := &updater.Manifest{Version: "1.1.10", Arch: updater.ArchString(), MinCore: "1.0.0", MinProtocol: 1}
	for _, name := range updater.RequiredAssetNames {
		m.Assets = append(m.Assets, updater.Asset{
			Name: name, SHA256: shaHex(payloads[name]), URL: hs.URL + "/" + name,
			Mode: requiredAssetMode(name), Required: true,
		})
	}

	calls := 0
	u := updater.New(serverBin, filepath.Join(root, "server.prev"), "")
	u.DaemonReload = func() error { calls++; return nil }
	mapRequiredTestAssets(u, root)
	if err := u.Apply(m, func() bool { return true }); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("daemon-reload calls=%d want 1", calls)
	}
	got := string(mustRead(t, u.ExtraBinaries["nyxveil-update-service"]))
	if !strings.Contains(got, "nyxveilctl update") {
		t.Fatalf("update unit content=%q", got)
	}
}
