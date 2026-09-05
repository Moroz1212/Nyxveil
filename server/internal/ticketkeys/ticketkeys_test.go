package ticketkeys

import (
	"crypto/ed25519"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
)

func TestLoad(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "keys.json")
	body := `{"issuer":"nyxveil-control-plane","keys":{"k1":"` + base64.StdEncoding.EncodeToString(pub) + `"}}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	iss, keys, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if iss != "nyxveil-control-plane" {
		t.Fatalf("issuer %q", iss)
	}
	if len(keys["k1"]) != ed25519.PublicKeySize {
		t.Fatal("missing key")
	}
}
