package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/nyxveil/nvp/core/auth/nodeauth"
	"github.com/nyxveil/nvp/core/auth/ticket"
	"github.com/nyxveil/nvp/core/controlplane/catalog"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	cmd := os.Args[1]
	args := os.Args[2:]
	var err error
	switch cmd {
	case "verify-ticket":
		err = verifyTicket(args)
	case "verify-catalog":
		err = verifyCatalog(args)
	case "sign-node-token":
		err = signNodeToken(args)
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage:
  verify-ticket --token-file <path> --pubkey-hex <32-byte-hex> --kid <id> --issuer <iss> --audience <aud> [--expected-location-id loc-ams] [--device-id <id>]
  verify-catalog --catalog-file <path> --pubkey-hex <32-byte-hex> --kid <id> [--expected-node-id node-ams-1] [--expected-location-id loc-ams]
  sign-node-token --node-id <id> --privkey-hex <32-byte-seed-hex> [--out <path>]`)
}

func verifyTicket(args []string) error {
	fs := flag.NewFlagSet("verify-ticket", flag.ContinueOnError)
	tokenFile := fs.String("token-file", "", "")
	pubHex := fs.String("pubkey-hex", "", "")
	kid := fs.String("kid", "", "")
	issuer := fs.String("issuer", "nyxveil-control-plane", "")
	audience := fs.String("audience", "nvp-node", "")
	expectedLoc := fs.String("expected-location-id", "loc-ams", "")
	deviceID := fs.String("device-id", "", "")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *tokenFile == "" || *pubHex == "" || *kid == "" {
		return fmt.Errorf("verify-ticket requires --token-file --pubkey-hex --kid")
	}
	tokBytes, err := os.ReadFile(*tokenFile)
	if err != nil {
		return err
	}
	token := strings.TrimSpace(string(tokBytes))
	pub, err := decodeKey32(*pubHex)
	if err != nil {
		return err
	}
	cfg := ticket.VerifierConfig{
		Issuer:   *issuer,
		Audience: *audience,
		PublicKeys: map[string]ed25519.PublicKey{
			*kid: ed25519.PublicKey(pub),
		},
		Revoked: ticket.NopRevocation{},
	}
	claims, err := ticket.VerifyAt(cfg, token, *deviceID, "", *expectedLoc)
	if err != nil {
		return fmt.Errorf("ticket VerifyAt failed: %w", err)
	}
	if claims.ProtocolVer != "NVP/1" {
		return fmt.Errorf("unexpected protocol_version %q", claims.ProtocolVer)
	}
	foundCanonical := false
	for _, loc := range claims.Locations {
		if loc == "ams" {
			return fmt.Errorf("TICKET_LOCATION_SCOPE fail: found Code alias ams instead of LocationId")
		}
		if loc == *expectedLoc {
			foundCanonical = true
		}
	}
	if !foundCanonical {
		return fmt.Errorf("TICKET_LOCATION_SCOPE fail: expected %q in locations %v", *expectedLoc, claims.Locations)
	}
	fmt.Printf("OK ticket jti=%s aud=%s location=%s\n", claims.ID, *audience, *expectedLoc)
	fmt.Println("TICKET_LOCATION_SCOPE=PASS")
	return nil
}

func verifyCatalog(args []string) error {
	fs := flag.NewFlagSet("verify-catalog", flag.ContinueOnError)
	catalogFile := fs.String("catalog-file", "", "")
	pubHex := fs.String("pubkey-hex", "", "")
	kid := fs.String("kid", "", "")
	expectedNode := fs.String("expected-node-id", "node-ams-1", "")
	expectedLoc := fs.String("expected-location-id", "loc-ams", "")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *catalogFile == "" || *pubHex == "" || *kid == "" {
		return fmt.Errorf("verify-catalog requires --catalog-file --pubkey-hex --kid")
	}
	raw, err := os.ReadFile(*catalogFile)
	if err != nil {
		return err
	}
	signed, err := catalog.Parse(raw)
	if err != nil {
		return err
	}
	pub, err := decodeKey32(*pubHex)
	if err != nil {
		return err
	}
	v := catalog.VerifyKeys{Keys: map[string]ed25519.PublicKey{*kid: ed25519.PublicKey(pub)}}
	if err := catalog.Verify(v, signed); err != nil {
		return fmt.Errorf("catalog verify failed: %w", err)
	}

	var node *struct {
		found bool
		idx   int
	}
	node = &struct {
		found bool
		idx   int
	}{}
	for i := range signed.Catalog.Nodes {
		if signed.Catalog.Nodes[i].NodeID == *expectedNode {
			node.found = true
			node.idx = i
			break
		}
	}
	if !node.found {
		return fmt.Errorf("CATALOG_NODE_MAPPING fail: node %q not found", *expectedNode)
	}
	n := signed.Catalog.Nodes[node.idx]
	if n.LocationID != *expectedLoc {
		return fmt.Errorf("CATALOG_NODE_MAPPING fail: location_id=%q want %q", n.LocationID, *expectedLoc)
	}
	if !n.Enabled {
		return fmt.Errorf("CATALOG_NODE_MAPPING fail: enabled=false")
	}
	if n.TestOnly {
		return fmt.Errorf("CATALOG_NODE_MAPPING fail: test_only should be false for prod node")
	}
	if n.Draining {
		return fmt.Errorf("CATALOG_NODE_MAPPING fail: draining should be false")
	}
	if n.ServerName == "" {
		return fmt.Errorf("CATALOG_NODE_MAPPING fail: server_name empty")
	}
	if len(n.SPKIPin) == 0 {
		return fmt.Errorf("CATALOG_NODE_MAPPING fail: spki_pin empty")
	}
	if len(n.Endpoints) == 0 {
		return fmt.Errorf("CATALOG_NODE_MAPPING fail: no endpoints")
	}
	ep := n.Endpoints[0]
	if ep.Host == "" || ep.Port == 0 {
		return fmt.Errorf("CATALOG_NODE_MAPPING fail: endpoint host/port")
	}
	if ep.IPFamily != "dual" && ep.IPFamily != "ipv4" && ep.IPFamily != "ipv6" {
		return fmt.Errorf("CATALOG_NODE_MAPPING fail: ip_family=%q", ep.IPFamily)
	}
	hasQuic, hasTLS := false, false
	for _, p := range ep.Profiles {
		ps := string(p)
		if ps == "quic-udp-443" {
			hasQuic = true
		}
		if ps == "tls-tcp-443" {
			hasTLS = true
		}
	}
	if !hasQuic || !hasTLS {
		return fmt.Errorf("CATALOG_NODE_MAPPING fail: profiles=%v", ep.Profiles)
	}
	fmt.Printf("OK catalog version=%s kid=%s node=%s\n", signed.Catalog.Version, signed.KeyID, n.NodeID)
	fmt.Println("CATALOG_NODE_MAPPING=PASS")
	fmt.Println("CATALOG_PROFILES=PASS")
	return nil
}

func signNodeToken(args []string) error {
	fs := flag.NewFlagSet("sign-node-token", flag.ContinueOnError)
	nodeID := fs.String("node-id", "", "")
	privHex := fs.String("privkey-hex", "", "")
	out := fs.String("out", "", "")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *nodeID == "" || *privHex == "" {
		return fmt.Errorf("sign-node-token requires --node-id --privkey-hex")
	}
	seed, err := decodeKey32(*privHex)
	if err != nil {
		return err
	}
	priv := ed25519.NewKeyFromSeed(seed)
	token := nodeauth.SignToken(*nodeID, priv, time.Now().UTC())
	if *out != "" {
		if err := os.WriteFile(*out, []byte(token+"\n"), 0o600); err != nil {
			return err
		}
	} else {
		fmt.Println(token)
	}
	meta, _ := json.Marshal(map[string]string{
		"node_id": *nodeID,
		"token":   token,
		"pub_hex": hex.EncodeToString(priv.Public().(ed25519.PublicKey)),
	})
	fmt.Fprintln(os.Stderr, string(meta))
	return nil
}

func decodeKey32(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if b, err := hex.DecodeString(s); err == nil {
		if len(b) != 32 {
			return nil, fmt.Errorf("expected 32-byte hex key, got %d", len(b))
		}
		return b, nil
	}
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		b, err = base64.RawURLEncoding.DecodeString(s)
	}
	if err != nil {
		return nil, fmt.Errorf("key must be hex or base64: %w", err)
	}
	if len(b) != 32 {
		return nil, fmt.Errorf("expected 32-byte key, got %d", len(b))
	}
	return b, nil
}
