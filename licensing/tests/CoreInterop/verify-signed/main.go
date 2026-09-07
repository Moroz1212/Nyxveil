package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"

	"github.com/nyxveil/nvp/core/controlplane/catalog"
)

// Verifies a SignedCatalog JSON file using Frozen Core catalog.Parse + Verify.
// Usage: verify-signed --catalog catalog.json --kid ID --pubkey-b64 ...|--pubkey-hex ...
func main() {
	var catalogFile, kid, pubB64, pubHex string
	args := os.Args[1:]
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--catalog":
			i++; catalogFile = args[i]
		case "--kid":
			i++; kid = args[i]
		case "--pubkey-b64":
			i++; pubB64 = args[i]
		case "--pubkey-hex":
			i++; pubHex = args[i]
		}
	}
	if catalogFile == "" || kid == "" || (pubB64 == "" && pubHex == "") {
		fmt.Fprintln(os.Stderr, "usage: --catalog <file> --kid <id> (--pubkey-b64|--pubkey-hex)")
		os.Exit(2)
	}
	raw, err := os.ReadFile(catalogFile)
	if err != nil {
		fatal(err)
	}
	signed, err := catalog.Parse(raw)
	if err != nil {
		fatal(err)
	}
	var pub ed25519.PublicKey
	if pubB64 != "" {
		b, err := base64.StdEncoding.DecodeString(pubB64)
		if err != nil {
			fatal(err)
		}
		pub = b
	} else {
		b, err := hex.DecodeString(pubHex)
		if err != nil {
			fatal(err)
		}
		pub = b
	}
	if len(pub) != ed25519.PublicKeySize {
		fatal(fmt.Errorf("pubkey len %d", len(pub)))
	}
	v := catalog.VerifyKeys{Keys: map[string]ed25519.PublicKey{kid: pub}}
	if err := catalog.Verify(v, signed); err != nil {
		fmt.Printf("RESULT=FAIL failed_gate=catalog_signature detail=%v\n", err)
		os.Exit(1)
	}
	fmt.Println("RESULT=PASS")
	fmt.Println("Catalog signature ........ PASS")
	fmt.Printf("version=%s key_id=%s nodes=%d\n", signed.Catalog.Version, signed.KeyID, len(signed.Catalog.Nodes))
	_ = json.Marshal
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "RESULT=FAIL detail=%v\n", err)
	os.Exit(1)
}
