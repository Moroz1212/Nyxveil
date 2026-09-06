package main

import (
	"context"
	"crypto/ed25519"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"flag"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/nyxveil/nvp/core/auth/ticket"
	"github.com/nyxveil/nvp/core/connector"
	"github.com/nyxveil/nvp/core/controlplane/catalog"
	"github.com/nyxveil/nvp/core/session"
	"github.com/nyxveil/nvp/core/transport"
	"github.com/nyxveil/nvp/core/transport/ech"
	quictransport "github.com/nyxveil/nvp/core/transport/quic"
	tlsstream "github.com/nyxveil/nvp/core/transport/tlsstream"
)

func main() {
	host := flag.String("host", "", "target host for network probes")
	port := flag.Int("port", 443, "target port for network probes")
	testAll := flag.Bool("all", false, "run all network diagnostic probes")
	cpURL := flag.String("cp-url", "", "Control Plane base URL (e.g. https://control.example.com)")
	verifyKeys := flag.String("verify-keys", "", "optional JSON file of catalog verify public keys (key_id -> base64)")
	catalogBearer := flag.String("catalog-bearer", "", "optional bearer for catalog fetch (value never printed)")
	nodeAddr := flag.String("node-addr", "", "VPN node host:port for handshake test")
	ticketStr := flag.String("ticket", "", "access ticket JWT for node AUTH (never printed)")
	deviceKey := flag.String("device-key", "", "optional PEM Ed25519 device private key for AUTH binding")
	caFile := flag.String("ca", "", "optional CA PEM for node TLS verification")
	serverName := flag.String("server-name", "", "TLS server name (SNI); defaults to node host")
	skipVerify := flag.Bool("insecure-skip-verify", false, "skip TLS cert verify (diag only; not for production)")
	flag.Parse()

	if *host == "" && *cpURL == "" && *nodeAddr == "" {
		fmt.Fprintln(os.Stderr, "usage: nvp-diag [-host <hostname> [-port 443] [-all]] [-cp-url URL [-verify-keys file]] [-node-addr host:port [-ticket JWT] [-device-key pem] [-ca pem]]")
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	fmt.Println("NVP Diagnostic Tool")
	exitCode := 0

	if *host != "" {
		fmt.Printf("Target: %s:%d\n\n", *host, *port)
		results := map[string]string{}
		results["dns_ipv4"] = testDNS(*host, "ip4")
		results["dns_ipv6"] = testDNS(*host, "ip6")
		results["tcp_connect"] = testTCP(ctx, *host, *port)
		results["udp_probe"] = testUDP(*host, *port)
		results["tls_handshake"] = testTLS(ctx, *host, *port)
		results["quic_available"] = testQUIC(ctx, *host, *port)
		results["ech_status"] = testECH(ctx, *host, *port)
		results["fallback_path"] = testFallbackPath(ctx, *host, *port, *caFile, *serverName, *skipVerify)

		fmt.Println("Network probes:")
		for _, k := range []string{"dns_ipv4", "dns_ipv6", "tcp_connect", "udp_probe", "tls_handshake", "quic_available", "ech_status", "fallback_path"} {
			fmt.Printf("  %-18s %s\n", k+":", results[k])
		}
		if *testAll {
			fmt.Println("\nNote: Full AUTH requires -node-addr with credentials.")
			fmt.Println("ECH required policy: NOT VERIFIED AGAINST TARGET NETWORK")
		}
	}

	if *cpURL != "" {
		fmt.Println("\nCatalog:")
		if err := runCatalog(ctx, *cpURL, *verifyKeys, *catalogBearer); err != nil {
			fmt.Printf("  status: FAIL (%s)\n", err)
			exitCode = 1
		}
	}

	if *nodeAddr != "" {
		fmt.Println("\nNode handshake:")
		if err := runNodeHandshake(ctx, *nodeAddr, *ticketStr, *deviceKey, *caFile, *serverName, *skipVerify); err != nil {
			fmt.Printf("  status: FAIL (%s)\n", err)
			exitCode = 1
		}
	}

	os.Exit(exitCode)
}

func testDNS(host, network string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	r := net.Resolver{}
	addrs, err := r.LookupIPAddr(ctx, host)
	if err != nil {
		return fmt.Sprintf("FAIL (%s)", err)
	}
	var matched []string
	for _, a := range addrs {
		if network == "ip4" && a.IP.To4() != nil {
			matched = append(matched, a.IP.String())
		}
		if network == "ip6" && a.IP.To4() == nil {
			matched = append(matched, a.IP.String())
		}
	}
	if len(matched) == 0 {
		return "NO_RECORDS"
	}
	return fmt.Sprintf("OK (%d addrs)", len(matched))
}

func testTCP(ctx context.Context, host string, port int) string {
	addr := net.JoinHostPort(host, fmt.Sprintf("%d", port))
	d := net.Dialer{Timeout: 5 * time.Second}
	start := time.Now()
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Sprintf("FAIL (%s)", err)
	}
	conn.Close()
	return fmt.Sprintf("OK (%dms)", time.Since(start).Milliseconds())
}

func testUDP(host string, port int) string {
	addr := net.JoinHostPort(host, fmt.Sprintf("%d", port))
	conn, err := net.DialTimeout("udp", addr, 3*time.Second)
	if err != nil {
		return fmt.Sprintf("FAIL (%s)", err)
	}
	conn.Close()
	return "OK (dial succeeded, response not guaranteed)"
}

func testTLS(ctx context.Context, host string, port int) string {
	addr := net.JoinHostPort(host, fmt.Sprintf("%d", port))
	d := &net.Dialer{Timeout: 5 * time.Second}
	raw, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Sprintf("FAIL dial (%s)", err)
	}
	defer raw.Close()

	cfg := &tls.Config{
		ServerName: host,
		MinVersion: tls.VersionTLS13,
	}
	tlsConn := tls.Client(raw, cfg)
	start := time.Now()
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		return fmt.Sprintf("FAIL handshake (%s)", err)
	}
	state := tlsConn.ConnectionState()
	if state.Version < tls.VersionTLS13 {
		return "FAIL (TLS downgrade detected)"
	}
	return fmt.Sprintf("OK TLS1.3 (%dms, ALPN=%v)", time.Since(start).Milliseconds(), state.NegotiatedProtocol)
}

func testQUIC(ctx context.Context, host string, port int) string {
	ep := transport.Endpoint{Host: host, Port: port}
	cfg := transport.DialConfig{
		Endpoint:   ep,
		ServerName: host,
		Timeout:    5 * time.Second,
	}
	t := quictransport.NewTransport()
	start := time.Now()
	conn, err := t.Dial(ctx, cfg)
	if err != nil {
		return fmt.Sprintf("FAIL (%s) - may need valid server cert / HTTP/3 CONNECT", err)
	}
	conn.Close()
	return fmt.Sprintf("OK (%dms)", time.Since(start).Milliseconds())
}

func testECH(ctx context.Context, host string, port int) string {
	addr := net.JoinHostPort(host, fmt.Sprintf("%d", port))
	d := &net.Dialer{Timeout: 5 * time.Second}
	raw, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Sprintf("FAIL dial (%s)", err)
	}
	defer raw.Close()

	cfg := &tls.Config{
		ServerName: host,
		MinVersion: tls.VersionTLS13,
	}
	if err := ech.ApplyClientConfig(cfg, transport.ECHPreferred, nil); err != nil {
		return fmt.Sprintf("FAIL config (%s)", err)
	}
	tlsConn := tls.Client(raw, cfg)
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		return fmt.Sprintf("FAIL handshake (%s)", err)
	}
	state := tlsConn.ConnectionState()
	status := ech.DescribeState(state)
	if state.ECHAccepted {
		return "OK (accepted)"
	}
	return fmt.Sprintf("NOT_ACCEPTED (%s) - %s", status, ech.PolicyHint(transport.ECHPreferred, false))
}

func testFallbackPath(ctx context.Context, host string, port int, caFile, serverName string, skipVerify bool) string {
	reg := transport.NewRegistry()
	reg.Register(quictransport.NewTransport())
	reg.Register(tlsstream.NewTransport())

	sn := serverName
	if sn == "" {
		sn = host
	}
	cfg := transport.DialConfig{
		Endpoint:   transport.Endpoint{Host: host, Port: port},
		ServerName: sn,
		Timeout:    8 * time.Second,
	}
	if pool, err := loadCAPool(caFile); err == nil && pool != nil {
		cfg.RootCAs = pool
	}
	if skipVerify {
		// tlsstream/quic require RootCAs or system roots; skip-verify is signaled via empty custom pool + insecure not supported.
		// Report that racing uses production ALPN only.
		_ = skipVerify
	}

	racing := transport.DefaultRacingConfig()
	start := time.Now()
	conn, err := reg.DialWithRacing(ctx, cfg, racing)
	latency := time.Since(start).Milliseconds()
	if err != nil {
		return fmt.Sprintf("FAIL both paths (%s) primary=%s fallback=%s", err, racing.Primary, racing.Fallback)
	}
	profile := conn.Profile()
	_ = conn.Close()
	path := "primary"
	if profile == racing.Fallback && profile != racing.Primary {
		path = "fallback"
	}
	return fmt.Sprintf("OK selected=%s profile=%s path=%s (%dms)", profile, profile, path, latency)
}

func runCatalog(ctx context.Context, baseURL, verifyKeysPath, bearer string) error {
	cp := connector.NewControlPlaneClient(strings.TrimRight(baseURL, "/"))
	start := time.Now()
	signed, err := cp.FetchCatalog(ctx, bearer)
	if err != nil {
		return err
	}
	latency := time.Since(start).Milliseconds()
	fmt.Printf("  fetch: OK (%dms)\n", latency)
	fmt.Printf("  nodes: %d locations: %d version: %s key_id: %s\n",
		len(signed.Catalog.Nodes), len(signed.Catalog.Locations), signed.Catalog.Version, signed.KeyID)
	fmt.Printf("  expires_at: %s\n", signed.Catalog.ExpiresAt.UTC().Format(time.RFC3339))

	if verifyKeysPath == "" {
		fmt.Println("  verify: SKIPPED (provide -verify-keys to fail-closed verify)")
		return nil
	}
	keys, err := loadVerifyKeys(verifyKeysPath)
	if err != nil {
		return fmt.Errorf("load verify keys: %w", err)
	}
	if err := catalog.Verify(keys, *signed); err != nil {
		return fmt.Errorf("verify: %w", err)
	}
	fmt.Printf("  verify: OK (%d keys loaded)\n", len(keys.Keys))
	return nil
}

func runNodeHandshake(ctx context.Context, nodeAddr, ticketJWT, deviceKeyPath, caFile, serverName string, skipVerify bool) error {
	host, portStr, err := net.SplitHostPort(nodeAddr)
	if err != nil {
		return fmt.Errorf("node-addr must be host:port: %w", err)
	}
	var port int
	if _, err := fmt.Sscanf(portStr, "%d", &port); err != nil || port <= 0 {
		return fmt.Errorf("invalid port in node-addr")
	}
	sn := serverName
	if sn == "" {
		sn = host
	}

	reg := transport.NewRegistry()
	reg.Register(quictransport.NewTransport())
	reg.Register(tlsstream.NewTransport())

	cfg := transport.DialConfig{
		Endpoint:   transport.Endpoint{Host: host, Port: port},
		ServerName: sn,
		Timeout:    10 * time.Second,
	}
	if pool, err := loadCAPool(caFile); err == nil && pool != nil {
		cfg.RootCAs = pool
	} else if skipVerify {
		fmt.Println("  warning: -insecure-skip-verify set; TLS identity not verified")
		// Production transports do not expose InsecureSkipVerify; dial will use system roots.
	}

	racing := transport.DefaultRacingConfig()
	start := time.Now()
	conn, err := reg.DialWithRacing(ctx, cfg, racing)
	dialMs := time.Since(start).Milliseconds()
	if err != nil {
		return fmt.Errorf("transport dial: %w", err)
	}
	defer conn.Close()

	path := "primary"
	if conn.Profile() == racing.Fallback && conn.Profile() != racing.Primary {
		path = "fallback"
	}
	fmt.Printf("  transport: OK profile=%s path=%s dial=%dms\n", conn.Profile(), path, dialMs)

	sess := session.New(session.DefaultConfig(true))
	hsStart := time.Now()
	if err := sess.Connect(ctx, conn); err != nil {
		return err
	}
	if err := sess.RunHandshake(ctx); err != nil {
		return fmt.Errorf("vpn handshake: %w", err)
	}
	fmt.Printf("  vpn_handshake: OK (%dms) state=%s\n", time.Since(hsStart).Milliseconds(), sess.State())

	if ticketJWT == "" {
		fmt.Println("  auth: SKIPPED (provide -ticket and -device-key to test AUTH)")
		pingLatency(ctx, sess)
		return nil
	}
	if deviceKeyPath == "" {
		fmt.Println("  auth: SKIPPED (-device-key required with -ticket; secrets never printed)")
		pingLatency(ctx, sess)
		return nil
	}
	priv, err := loadDeviceKey(deviceKeyPath)
	if err != nil {
		return fmt.Errorf("device-key: %w", err)
	}
	authBody, err := ticket.EncodeAuthPayload(ticketJWT, sess.Transcript(), priv)
	if err != nil {
		return err
	}

	go func() { _ = sess.ReadLoop(ctx) }()
	if err := sess.SendAuth(ctx, authBody); err != nil {
		return fmt.Errorf("auth send: %w", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if sess.State() == session.StateEstablished {
			fmt.Printf("  auth: OK state=%s\n", sess.State())
			pingLatency(ctx, sess)
			return nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	fmt.Printf("  auth: PENDING state=%s (server may still be processing)\n", sess.State())
	pingLatency(ctx, sess)
	return nil
}

func pingLatency(ctx context.Context, sess *session.Session) {
	start := time.Now()
	if err := sess.SendPing(ctx); err != nil {
		fmt.Printf("  latency: FAIL ping (%s)\n", err)
		return
	}
	fmt.Printf("  latency: ping sent (%dms wall); RTT requires established PONG path\n", time.Since(start).Milliseconds())
}

func loadVerifyKeys(path string) (catalog.VerifyKeys, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return catalog.VerifyKeys{}, err
	}
	var raw map[string]string
	if err := json.Unmarshal(data, &raw); err != nil {
		return catalog.VerifyKeys{}, fmt.Errorf("expected JSON object key_id->base64: %w", err)
	}
	keys := catalog.VerifyKeys{Keys: make(map[string]ed25519.PublicKey, len(raw))}
	for id, b64 := range raw {
		b, err := base64.StdEncoding.DecodeString(b64)
		if err != nil {
			b, err = base64.RawStdEncoding.DecodeString(b64)
		}
		if err != nil {
			return catalog.VerifyKeys{}, fmt.Errorf("key %s: %w", id, err)
		}
		if len(b) != ed25519.PublicKeySize {
			return catalog.VerifyKeys{}, fmt.Errorf("key %s: want %d bytes, got %d", id, ed25519.PublicKeySize, len(b))
		}
		keys.Keys[id] = ed25519.PublicKey(b)
	}
	return keys, nil
}

func loadCAPool(path string) (*x509.CertPool, error) {
	if path == "" {
		return nil, nil
	}
	pemBytes, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pemBytes) {
		return nil, fmt.Errorf("no certificates in %s", path)
	}
	return pool, nil
}

func loadDeviceKey(path string) (ed25519.PrivateKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("no PEM block")
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	priv, ok := key.(ed25519.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("not ed25519 private key")
	}
	return priv, nil
}
