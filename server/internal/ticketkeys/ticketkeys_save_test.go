package ticketkeys

import (
	"crypto/ed25519"
	"path/filepath"
	"testing"
)

func TestSaveLoadRoundTrip(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "ticket-keys.json")
	keys := map[string]ed25519.PublicKey{"k1": pub}
	if err := Save(path, "nyxveil-control-plane", keys, 99); err != nil {
		t.Fatal(err)
	}
	iss, got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if iss != "nyxveil-control-plane" || len(got["k1"]) != ed25519.PublicKeySize {
		t.Fatal()
	}
	if PathBesideKey(filepath.Join(dir, "node.key")) != path {
		t.Fatal(PathBesideKey(filepath.Join(dir, "node.key")))
	}
}
