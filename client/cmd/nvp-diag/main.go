package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"net"
	"os"
	"time"

	"github.com/nyxveil/nvp/transport"
	"github.com/nyxveil/nvp/transport/ech"
	quictransport "github.com/nyxveil/nvp/transport/quic"
	tlsstream "github.com/nyxveil/nvp/transport/tlsstream"
)

func main() {
	host := flag.String("host", "", "target host to diagnose")
	port := flag.Int("port", 443, "target port")
	testAll := flag.Bool("all", false, "run all diagnostic tests")
	flag.Parse()

	if *host == "" {
		fmt.Fprintln(os.Stderr, "usage: nvp-diag -host <hostname> [-port 443] [-all]")
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	fmt.Printf("NVP Diagnostic Tool\n")
	fmt.Printf("Target: %s:%d\n\n", *host, *port)

	results := make(map[string]string)

	results["dns_ipv4"] = testDNS(*host, "ip4")
	results["dns_ipv6"] = testDNS(*host, "ip6")
	results["tcp_connect"] = testTCP(ctx, *host, *port)
	results["udp_probe"] = testUDP(*host, *port)
	results["tls_handshake"] = testTLS(ctx, *host, *port)
	results["quic_available"] = testQUIC(ctx, *host, *port)
	results["ech_status"] = testECH(ctx, *host, *port)

	fmt.Println("Results:")
	for k, v := range results {
		fmt.Printf("  %-18s %s\n", k+":", v)
	}

	if *testAll {
		fmt.Println("\nNote: Full handshake test requires valid node credentials.")
		fmt.Println("ECH required policy: NOT VERIFIED AGAINST TARGET NETWORK")
	}
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
	_ = quictransport.NewTransport()
	ep := transport.Endpoint{Host: host, Port: port}
	cfg := transport.DialConfig{
		Endpoint:   ep,
		ServerName: host,
		Timeout:    5 * time.Second,
	}
	// Without valid certs this will fail - report status honestly
	t := quictransport.NewTransport()
	start := time.Now()
	conn, err := t.Dial(ctx, cfg)
	if err != nil {
		return fmt.Sprintf("FAIL (%s) - may need valid server cert", err)
	}
	conn.Close()
	return fmt.Sprintf("OK (%dms)", time.Since(start).Milliseconds())
}

func testECH(ctx context.Context, host string, port int) string {
	_ = tlsstream.NewTransport()
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
