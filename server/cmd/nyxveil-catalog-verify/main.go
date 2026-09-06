// Command nyxveil-catalog-verify cryptographically verifies a signed catalog
// using Frozen Core controlplane/catalog.Verify (same canonical payload as CP).
//
// Usage:
//
//	nyxveil-catalog-verify -keys keys.json -catalog signed-catalog.json
//	nyxveil-catalog-verify -keys keys.json -catalog signed-catalog.json -expect-node nv-id -expect-spki-b64 ...
package main

import (
	"encoding/base64"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/nyxveil/server/internal/catalogverify"
)

func main() {
	keysPath := flag.String("keys", "", "path to GET /api/v1/catalog-keys JSON")
	catalogPath := flag.String("catalog", "", "path to GET /api/v1/catalog JSON")
	expectNode := flag.String("expect-node", "", "optional node_id that must appear")
	expectSPKI := flag.String("expect-spki-b64", "", "optional standard Base64 SPKI that expect-node must advertise")
	expectServer := flag.String("expect-server-name", "", "optional TLS server_name for expect-node")
	flag.Parse()

	if strings.TrimSpace(*keysPath) == "" || strings.TrimSpace(*catalogPath) == "" {
		fmt.Fprintln(os.Stderr, "usage: nyxveil-catalog-verify -keys keys.json -catalog catalog.json")
		os.Exit(2)
	}
	keysRaw, err := os.ReadFile(*keysPath)
	if err != nil {
		fail("keys_read", err)
	}
	catRaw, err := os.ReadFile(*catalogPath)
	if err != nil {
		fail("catalog_read", err)
	}

	signed, err := catalogverify.VerifySignedCatalogJSON(keysRaw, catRaw)
	if err != nil {
		fail("signature", err)
	}
	fmt.Println("Catalog signature ........ PASS")

	if id := strings.TrimSpace(*expectNode); id != "" {
		n, ok := catalogverify.FindNode(signed, id)
		if !ok {
			fail("node_missing", fmt.Errorf("node %q not in verified catalog", id))
		}
		if want := strings.TrimSpace(*expectSPKI); want != "" {
			got := base64.StdEncoding.EncodeToString(n.SPKIPin)
			if got != want {
				fail("spki", fmt.Errorf("node %s spki_pin want %s got %s", id, want, got))
			}
		}
		if want := strings.TrimSpace(*expectServer); want != "" && n.ServerName != want {
			fail("server_name", fmt.Errorf("node %s server_name want %s got %s", id, want, n.ServerName))
		}
		fmt.Printf("Catalog node metadata .... PASS node_id=%s\n", id)
	}

	os.Exit(0)
}

func fail(gate string, err error) {
	fmt.Fprintf(os.Stderr, "RESULT=FAIL failed_gate=catalog_%s detail=%v\n", gate, err)
	os.Exit(1)
}
