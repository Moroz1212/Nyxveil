package ech

import (
	"crypto/tls"
	"testing"

	"github.com/nyxveil/nvp/core/transport"
)

func TestECHRequiredWithoutConfig(t *testing.T) {
	cfg := &tls.Config{MinVersion: tls.VersionTLS13}
	err := ApplyClientConfig(cfg, transport.ECHRequired, nil)
	if err != ErrECHConfigMissing {
		t.Fatalf("expected config missing, got %v", err)
	}
}

func TestECHRequiredUnavailableFails(t *testing.T) {
	cfg := &tls.Config{MinVersion: tls.VersionTLS13}
	if err := ApplyClientConfig(cfg, transport.ECHRequired, nil); err != ErrECHConfigMissing {
		t.Fatalf("ECHRequired with empty config must fail before dial, got %v", err)
	}
	state := tls.ConnectionState{Version: tls.VersionTLS13, ECHAccepted: false}
	if err := VerifyNegotiated(transport.ECHRequired, state); err != ErrECHRequiredButMissing {
		t.Fatalf("expected required error, got %v", err)
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

func TestECHRequiredSuccess(t *testing.T) {
	cfgList := []byte{0x01, 0x02, 0x03} // opaque list; ApplyClientConfig only requires non-empty
	cfg := &tls.Config{MinVersion: tls.VersionTLS13}
	if err := ApplyClientConfig(cfg, transport.ECHRequired, cfgList); err != nil {
		t.Fatal(err)
	}
	if len(cfg.EncryptedClientHelloConfigList) == 0 {
		t.Fatal("expected config list installed")
	}
	state := tls.ConnectionState{Version: tls.VersionTLS13, ECHAccepted: true}
	if err := VerifyNegotiated(transport.ECHRequired, state); err != nil {
		t.Fatal(err)
	}
}

func TestECHPreferredFallback(t *testing.T) {
	cfg := &tls.Config{MinVersion: tls.VersionTLS13}
	if err := ApplyClientConfig(cfg, transport.ECHPreferred, nil); err != nil {
		t.Fatal(err)
	}
	state := tls.ConnectionState{Version: tls.VersionTLS13, ECHAccepted: false}
	if err := VerifyNegotiated(transport.ECHPreferred, state); err != nil {
		t.Fatal(err)
	}
	// Preferred with config still allows non-ECH peer.
	if err := ApplyClientConfig(cfg, transport.ECHPreferred, []byte{0x0a}); err != nil {
		t.Fatal(err)
	}
	if err := VerifyNegotiated(transport.ECHPreferred, state); err != nil {
		t.Fatal(err)
	}
}

func TestECHPreferredAllowsPlaintext(t *testing.T) {
	state := tls.ConnectionState{Version: tls.VersionTLS13, ECHAccepted: false}
	if err := VerifyNegotiated(transport.ECHPreferred, state); err != nil {
		t.Fatal(err)
	}
}

func TestECHServerKeyRotation(t *testing.T) {
	k1 := tls.EncryptedClientHelloKey{Config: []byte("cfg-a"), PrivateKey: []byte("key-a")}
	k2 := tls.EncryptedClientHelloKey{Config: []byte("cfg-b"), PrivateKey: []byte("key-b")}
	set := NewKeySet([]tls.EncryptedClientHelloKey{k1})
	cfg := &tls.Config{MinVersion: tls.VersionTLS13}
	set.ApplyTo(cfg)
	if len(cfg.EncryptedClientHelloKeys) != 1 || string(cfg.EncryptedClientHelloKeys[0].Config) != "cfg-a" {
		t.Fatalf("initial keys: %+v", cfg.EncryptedClientHelloKeys)
	}
	// Overlap rotation: publish both, then drop old.
	set.Rotate([]tls.EncryptedClientHelloKey{k1, k2})
	set.ApplyTo(cfg)
	if len(cfg.EncryptedClientHelloKeys) != 2 {
		t.Fatalf("overlap keys len=%d", len(cfg.EncryptedClientHelloKeys))
	}
	set.Rotate([]tls.EncryptedClientHelloKey{k2})
	set.ApplyTo(cfg)
	if len(cfg.EncryptedClientHelloKeys) != 1 || string(cfg.EncryptedClientHelloKeys[0].Config) != "cfg-b" {
		t.Fatalf("rotated keys: %+v", cfg.EncryptedClientHelloKeys)
	}
}
