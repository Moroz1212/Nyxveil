package ech

import (
	"crypto/tls"
	"testing"

	"github.com/nyxveil/nvp/transport"
)

func TestECHRequiredWithoutConfig(t *testing.T) {
	cfg := &tls.Config{MinVersion: tls.VersionTLS13}
	err := ApplyClientConfig(cfg, transport.ECHRequired, nil)
	if err != ErrECHConfigMissing {
		t.Fatalf("expected config missing, got %v", err)
	}
}

func TestECHPreferredWithoutConfig(t *testing.T) {
	cfg := &tls.Config{MinVersion: tls.VersionTLS13}
	if err := ApplyClientConfig(cfg, transport.ECHPreferred, nil); err != nil {
		t.Fatal(err)
	}
}

func TestECHRequiredNotNegotiated(t *testing.T) {
	state := tls.ConnectionState{Version: tls.VersionTLS13, ECHAccepted: false}
	if err := VerifyNegotiated(transport.ECHRequired, state); err != ErrECHRequiredButMissing {
		t.Fatalf("expected required error, got %v", err)
	}
}

func TestECHPreferredAllowsPlaintext(t *testing.T) {
	state := tls.ConnectionState{Version: tls.VersionTLS13, ECHAccepted: false}
	if err := VerifyNegotiated(transport.ECHPreferred, state); err != nil {
		t.Fatal(err)
	}
}
