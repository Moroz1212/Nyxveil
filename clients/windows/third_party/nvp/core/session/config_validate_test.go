package session_test

import (
	"testing"
	"time"

	"github.com/nyxveil/nvp/core/protocol"
	"github.com/nyxveil/nvp/core/session"
)

func TestDefaultConfigValidateOK(t *testing.T) {
	cfg := session.DefaultConfig(true)
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if cfg.RekeyPacketCount != protocol.DefaultRekeyPacketCount {
		t.Fatalf("RekeyPacketCount=%d", cfg.RekeyPacketCount)
	}
	if cfg.RekeyByteCount != protocol.DefaultRekeyByteCount {
		t.Fatalf("RekeyByteCount=%d", cfg.RekeyByteCount)
	}
	if cfg.RekeyInterval != protocol.DefaultRekeyInterval {
		t.Fatalf("RekeyInterval=%s", cfg.RekeyInterval)
	}
}

func TestConfigValidateRejectsZeroRekeyThresholds(t *testing.T) {
	cfg := session.DefaultConfig(true)
	cfg.RekeyInterval = 0
	cfg.RekeyPacketCount = 0
	cfg.RekeyByteCount = 0
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error when all rekey thresholds are zero")
	}
}

func TestConfigValidateRejectsNegativeKeepalive(t *testing.T) {
	cfg := session.DefaultConfig(true)
	cfg.KeepaliveInterval = -time.Second
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected negative keepalive rejection")
	}
}

func TestDefaultAuthTimeoutConstant(t *testing.T) {
	if protocol.DefaultAuthTimeout != 15*time.Second {
		t.Fatalf("DefaultAuthTimeout=%s", protocol.DefaultAuthTimeout)
	}
}
