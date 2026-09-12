// Command quic-handshake-probe dials the node's QUIC/HTTP3 listener and proves
// a real TLS-over-QUIC handshake (ALPN h3) without requiring system trust.
// Lab-only: uses InsecureSkipVerify so Pebble-issued leaves are acceptable.
package main

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8443", "host:port of QUIC listener")
	serverName := flag.String("servername", "node-e2e.test", "TLS SNI / ServerName")
	timeout := flag.Duration("timeout", 12*time.Second, "dial timeout")
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	tlsConf := &tls.Config{
		MinVersion:         tls.VersionTLS13,
		ServerName:         *serverName,
		NextProtos:         []string{http3.NextProtoH3},
		InsecureSkipVerify: true, //nolint:gosec // lab probe against Pebble-issued leaf
	}
	qconf := &quic.Config{
		MaxIdleTimeout:  15 * time.Second,
		EnableDatagrams: true,
	}

	session, err := quic.DialAddr(ctx, *addr, tlsConf, qconf)
	if err != nil {
		fmt.Fprintf(os.Stderr, "quic_handshake=FAIL dial: %v\n", err)
		os.Exit(1)
	}
	defer session.CloseWithError(0, "probe done")

	state := session.ConnectionState().TLS
	if state.NegotiatedProtocol != http3.NextProtoH3 {
		fmt.Fprintf(os.Stderr, "quic_handshake=FAIL alpn=%q want=%s\n", state.NegotiatedProtocol, http3.NextProtoH3)
		os.Exit(1)
	}
	fp := ""
	if len(state.PeerCertificates) > 0 {
		sum := sha256.Sum256(state.PeerCertificates[0].Raw)
		fp = hex.EncodeToString(sum[:])
	}
	fmt.Printf("quic_handshake=PASS alpn=%s leaf_sha256=%s\n", state.NegotiatedProtocol, fp)
}
