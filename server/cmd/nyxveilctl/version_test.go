package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
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

	var out bytes.Buffer
	printVersion(&out)
	text := out.String()
	for _, line := range []string{
		"cli_version=" + version.CLIVersion,
		"running_server_version=9.8.7",
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
