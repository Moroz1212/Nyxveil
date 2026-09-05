package server_test

import (
	"os"
	"testing"

	cpserver "github.com/nyxveil/nvp/core/controlplane/server"
)

func TestRequireProductionFailClosed(t *testing.T) {
	os.Unsetenv("NVP_LICENSE_KEK")
	err := cpserver.RequireProduction(cpserver.ServerOptions{})
	if err == nil {
		t.Fatal("expected failure without TLS and KEK")
	}

	t.Setenv("NVP_LICENSE_KEK", "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	err = cpserver.RequireProduction(cpserver.ServerOptions{
		TLS: cpserver.TLSConfig{CertFile: "cert.pem", KeyFile: "key.pem"},
	})
	if err != nil {
		t.Fatalf("expected ok with TLS+KEK: %v", err)
	}

	err = cpserver.RequireProduction(cpserver.ServerOptions{AllowInsecureDev: true})
	if err != nil {
		t.Fatalf("allow-insecure-dev should skip checks: %v", err)
	}
}

func TestIsLocalhostListen(t *testing.T) {
	if !cpserver.IsLocalhostListen("127.0.0.1:8443") {
		t.Fatal("127.0.0.1 should be localhost")
	}
	if cpserver.IsLocalhostListen(":8443") {
		t.Fatal(":8443 binds all interfaces")
	}
}
