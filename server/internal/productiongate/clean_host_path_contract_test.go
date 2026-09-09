package productiongate_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nyxveil/server/internal/paths"
)

func TestCleanHostGateUsesCanonicalPaths(t *testing.T) {
	root := findServerRoot(t)
	raw, err := os.ReadFile(filepath.Join(root, "scripts", "clean-host-install-gate.sh"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)

	// Forbidden legacy defaults (comparison that rejects /etc paths is OK).
	if strings.Contains(text, `TLS_CERT=/etc/nyxveil/tls.crt`) || strings.Contains(text, `TLS_CERT="/etc/nyxveil/tls.crt"`) {
		t.Fatal("clean-host gate must not default TLS_CERT to /etc/nyxveil/tls.crt")
	}
	if strings.Contains(text, `TLS_KEY=/etc/nyxveil/tls.key`) || strings.Contains(text, `TLS_KEY="/etc/nyxveil/tls.key"`) {
		t.Fatal("clean-host gate must not default TLS_KEY to /etc/nyxveil/tls.key")
	}
	if strings.Contains(text, "/var/lib/nyxveil/node_id") {
		t.Fatal("clean-host gate must not read nonexistent /var/lib/nyxveil/node_id")
	}

	// Required contracts aligned with server/internal/paths.
	wantCert := paths.TLSCert()
	wantKey := paths.TLSKey()
	wantCfg := paths.ServerConfig()
	if !strings.Contains(text, wantCert) {
		t.Fatalf("gate must default TLS cert to %s", wantCert)
	}
	if !strings.Contains(text, wantKey) {
		t.Fatalf("gate must default TLS key to %s", wantKey)
	}
	if !strings.Contains(text, wantCfg) && !strings.Contains(text, "server.json") {
		t.Fatal("gate must read node_id from server.json")
	}
	if !strings.Contains(text, "tls_cert_file") || !strings.Contains(text, "tls_key_file") {
		t.Fatal("gate must parse TLS paths from server.json")
	}
	if !strings.Contains(text, `"node_id"`) {
		t.Fatal("gate must parse node_id from server.json")
	}
}
