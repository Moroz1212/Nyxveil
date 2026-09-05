package updater

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nyxveil/server/internal/version"
)

func TestUpdateRejectsNewerRequiredCore(t *testing.T) {
	m := &Manifest{Version: "9.0.0", Arch: ArchString(), MinCore: "99.0.0", MinProtocol: 1}
	if err := CheckCompatibility(m, "1.0.0", 1); err == nil {
		t.Fatal("expected min_core rejection")
	}
	u := New(filepath.Join(t.TempDir(), "srv"), "", "")
	if err := u.Apply(m, nil); err == nil {
		t.Fatal("Apply must fail before download when min_core is too new")
	}
}

func TestUpdateRejectsNewerRequiredProtocol(t *testing.T) {
	m := &Manifest{Version: "9.0.0", Arch: ArchString(), MinCore: "1.0.0", MinProtocol: 9999}
	if err := CheckCompatibility(m, version.CoreVersion, version.ProtocolNumber); err == nil {
		t.Fatal("expected min_protocol rejection")
	}
	u := New(filepath.Join(t.TempDir(), "srv"), "", "")
	if err := u.Apply(m, nil); err == nil {
		t.Fatal("Apply must fail before download when min_protocol is too new")
	}
}

func TestCompatibleManifestAccepted(t *testing.T) {
	m := &Manifest{
		Version:     "1.0.0",
		Arch:        ArchString(),
		MinCore:     version.CoreVersion,
		MinProtocol: version.ProtocolNumber,
	}
	if err := CheckCompatibility(m, version.CoreVersion, version.ProtocolNumber); err != nil {
		t.Fatal(err)
	}
}

func TestDefaultManifestURLIsArchAware(t *testing.T) {
	u := DefaultManifestURL()
	if strings.HasSuffix(u, "/release-manifest.json") {
		t.Fatal("legacy release-manifest.json must not be the default")
	}
	if !strings.Contains(u, "/release-manifest-linux-") || !strings.HasSuffix(u, ".json") {
		t.Fatalf("unexpected default URL %s", u)
	}
}

func TestRollbackRestoresPrevBinaries(t *testing.T) {
	dir := t.TempDir()
	server := filepath.Join(dir, "nyxveil-server")
	prev := filepath.Join(dir, "nyxveil-server.prev")
	ctl := filepath.Join(dir, "nyxveilctl")
	ctlPrev := filepath.Join(dir, "nyxveilctl.prev")
	if err := os.WriteFile(server, []byte("OLD-SERVER"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ctl, []byte("OLD-CTL"), 0o755); err != nil {
		t.Fatal(err)
	}

	newServer := []byte("NEW-SERVER")
	newCtl := []byte("NEW-CTL")
	sumServer := hex.EncodeToString(sha256Sum(newServer))
	sumCtl := hex.EncodeToString(sha256Sum(newCtl))

	mux := http.NewServeMux()
	mux.HandleFunc("/server", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(newServer) })
	mux.HandleFunc("/ctl", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(newCtl) })
	hs := httptest.NewServer(mux)
	defer hs.Close()

	m := &Manifest{
		Version:     "9.9.9",
		Arch:        ArchString(),
		MinCore:     "0.0.1",
		MinProtocol: 1,
		Assets: []Asset{
			{Name: "nyxveil-server", SHA256: sumServer, URL: hs.URL + "/server"},
			{Name: "nyxveilctl", SHA256: sumCtl, URL: hs.URL + "/ctl"},
		},
	}

	u := New(server, prev, filepath.Join(dir, "marker"))
	u.ExtraBinaries = map[string]string{"nyxveilctl": ctl}
	u.ExtraPrev = map[string]string{"nyxveilctl": ctlPrev}

	var healthCalls int
	err := u.Apply(m, func() bool {
		healthCalls++
		got, _ := os.ReadFile(server)
		if string(got) != "NEW-SERVER" {
			t.Errorf("health saw server=%q", got)
		}
		return false
	})
	if err == nil {
		t.Fatal("expected health failure")
	}
	if !strings.Contains(err.Error(), "rolled back") {
		t.Fatalf("err=%v", err)
	}
	got, _ := os.ReadFile(server)
	if string(got) != "OLD-SERVER" {
		t.Fatalf("server not restored: %q", got)
	}
	got, _ = os.ReadFile(ctl)
	if string(got) != "OLD-CTL" {
		t.Fatalf("ctl not restored: %q", got)
	}
	if healthCalls != 1 {
		t.Fatalf("healthCalls=%d", healthCalls)
	}
}

func sha256Sum(b []byte) []byte {
	sum := sha256.Sum256(b)
	return sum[:]
}
