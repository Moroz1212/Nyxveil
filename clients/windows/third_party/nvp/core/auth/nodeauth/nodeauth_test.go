package nodeauth

import (
	"testing"
	"time"

	"crypto/ed25519"
)

func TestSignVerifyRoundTrip(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	now := time.Now()
	tok := SignToken("node-1", priv, now)
	if err := VerifyToken("node-1", tok, pub, now); err != nil {
		t.Fatalf("verify: %v", err)
	}
}

func TestVerifyWrongNode(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	now := time.Now()
	tok := SignToken("node-1", priv, now)
	if err := VerifyToken("node-2", tok, pub, now); err == nil {
		t.Fatal("expected failure for wrong node")
	}
}

func TestVerifyExpired(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	past := time.Now().Add(-10 * time.Minute)
	tok := SignToken("node-1", priv, past)
	if err := VerifyToken("node-1", tok, pub, time.Now()); err == nil {
		t.Fatal("expected expired token failure")
	}
}
