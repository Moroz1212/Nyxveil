package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nyxveil/server/internal/version"
)

func TestPrintVersionSeparatesCLIInstalledAndRunning(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/status" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"running":true,"server_version":"9.8.7"}`))
	}))
	defer srv.Close()
	t.Setenv("NYXVEIL_CONTROL_HTTP", srv.URL)
	t.Setenv("NYXVEIL_SERVER_BINARY", t.TempDir()+"/missing-server")
	share := t.TempDir()
	if err := os.WriteFile(filepath.Join(share, "VERSION"), []byte("9.9.9\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NYXVEIL_SHARE_DIR", share)

	var out bytes.Buffer
	printVersion(&out)
	text := out.String()
	for _, line := range []string{
		"cli_version=" + version.CLIVersion,
		"running_server_version=9.8.7",
		"release_version=9.9.9",
		"core_version=" + version.CoreVersion,
		"protocol=" + version.ProtocolVersion,
	} {
		if !strings.Contains(text, line+"\n") {
			t.Fatalf("missing %q in:\n%s", line, text)
		}
	}
	if !strings.Contains(text, "\ninstalled_server_version=") {
		t.Fatalf("missing installed server field in:\n%s", text)
	}
}

func TestVersionJSONMachineReadable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"running":true,"server_version":"1.2.3"}`))
	}))
	defer srv.Close()
	t.Setenv("NYXVEIL_CONTROL_HTTP", srv.URL)
	t.Setenv("NYXVEIL_SERVER_BINARY", filepath.Join(t.TempDir(), "missing"))
	share := t.TempDir()
	_ = os.WriteFile(filepath.Join(share, "VERSION"), []byte("1.2.3\n"), 0o644)
	t.Setenv("NYXVEIL_SHARE_DIR", share)

	var out bytes.Buffer
	printVersionReport(&out, collectVersionReport(), true)
	if !strings.Contains(out.String(), `"running_server_version":"1.2.3"`) {
		t.Fatalf("json missing running: %s", out.String())
	}
	if !strings.Contains(out.String(), `"release_version":"1.2.3"`) {
		t.Fatalf("json missing release: %s", out.String())
	}
}

func TestRunningServerVersionUnknownWhenStatusSaysStopped(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"running":false,"server_version":"9.8.7"}`))
	}))
	defer srv.Close()
	t.Setenv("NYXVEIL_CONTROL_HTTP", srv.URL)
	if got := runningServerVersion(); got != "unknown" {
		t.Fatalf("running version=%q want unknown", got)
	}
}

func TestAssertVersionsMatchTargetRejectsUnknown(t *testing.T) {
	t.Setenv("NYXVEIL_CONTROL_HTTP", "http://127.0.0.1:1")
	t.Setenv("NYXVEIL_SERVER_BINARY", filepath.Join(t.TempDir(), "missing"))
	t.Setenv("NYXVEIL_SHARE_DIR", t.TempDir())
	err := assertVersionsMatchTarget(version.ServerVersion)
	if err == nil {
		t.Fatal("expected mismatch when sources unavailable")
	}
}
