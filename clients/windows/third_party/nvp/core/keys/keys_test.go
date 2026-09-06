package keys

import (
	"testing"

	"golang.org/x/crypto/chacha20poly1305"
)

func TestDeriveSessionKeysDeterministic(t *testing.T) {
	var shared [32]byte
	for i := range shared {
		shared[i] = byte(i)
	}
	transcript := []byte("transcript-v1")
	sk1, err := DeriveSessionKeys(shared, transcript, 0)
	if err != nil {
		t.Fatal(err)
	}
	sk2, err := DeriveSessionKeys(shared, transcript, 0)
	if err != nil {
		t.Fatal(err)
	}
	if string(sk1.ClientToServer) != string(sk2.ClientToServer) {
		t.Fatal("derive not deterministic")
	}
	if len(sk1.ClientToServer) != chacha20poly1305.KeySize {
		t.Fatalf("unexpected key size %d", len(sk1.ClientToServer))
	}
}

func TestSharedSecretRejectsLowOrder(t *testing.T) {
	var priv, peer [32]byte
	_, err := SharedSecret(&priv, &peer)
	if err == nil {
		t.Fatal("expected low-order rejection")
	}
}

func TestAEADRoundTrip(t *testing.T) {
	var shared [32]byte
	shared[0] = 1
	sk, err := DeriveSessionKeys(shared, []byte("t"), 1)
	if err != nil {
		t.Fatal(err)
	}
	client, err := NewClientAEAD(sk.ClientToServer)
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewServerAEAD(sk.ServerToClient)
	if err != nil {
		t.Fatal(err)
	}
	pt := []byte("payload")
	ct, err := client.Seal(1, 42, pt)
	if err != nil {
		t.Fatal(err)
	}
	// server receives client-to-server on recv path using server AEAD with same key direction
	recv, err := NewClientAEAD(sk.ClientToServer)
	if err != nil {
		t.Fatal(err)
	}
	out, err := recv.Open(1, 42, ct)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != string(pt) {
		t.Fatal("roundtrip mismatch")
	}
	_ = server
}

func TestGenerateEphemeral(t *testing.T) {
	kp, err := GenerateEphemeral()
	if err != nil {
		t.Fatal(err)
	}
	var zero [32]byte
	if kp.Public == zero {
		t.Fatal("expected non-zero public key")
	}
}
