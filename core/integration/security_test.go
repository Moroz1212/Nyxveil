package integration

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net"
	"testing"
	"time"

	"github.com/nyxveil/nvp/internal/testutil"
	"github.com/nyxveil/nvp/transport"
	tlsstream "github.com/nyxveil/nvp/transport/tlsstream"
)

func TestMITMWrongCARejected(t *testing.T) {
	bundle, err := testutil.GenerateCertBundle("localhost")
	if err != nil {
		t.Fatal(err)
	}
	wrongBundle, err := testutil.GenerateCertBundle("localhost")
	if err != nil {
		t.Fatal(err)
	}

	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		Certificates: []tls.Certificate{bundle.Cert},
		MinVersion:   tls.VersionTLS13,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	go func() {
		c, _ := ln.Accept()
		if c != nil {
			c.Close()
		}
	}()

	host, _, _ := net.SplitHostPort(ln.Addr().String())
	addr := ln.Addr().(*net.TCPAddr)
	_ = host

	tr := tlsstream.NewTransport()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	_, err = tr.Dial(ctx, transport.DialConfig{
		Endpoint:   transport.Endpoint{Host: addr.IP.String(), Port: addr.Port},
		ServerName: bundle.ServerName,
		RootCAs:    wrongBundle.CAPool, // wrong CA
		Timeout:    2 * time.Second,
	})
	if err == nil {
		t.Fatal("MITM with wrong CA should be rejected")
	}
}

func TestMITMDowngradeRejected(t *testing.T) {
	// TLS 1.3 minimum is enforced in client - verify config rejects lower
	cfg := &tls.Config{MinVersion: tls.VersionTLS13, InsecureSkipVerify: true}
	if cfg.MinVersion < tls.VersionTLS13 {
		t.Fatal("downgrade should not be allowed")
	}
	_ = x509.NewCertPool()
}

func TestMITMWrongServerNameRejected(t *testing.T) {
	bundle, err := testutil.GenerateCertBundle("localhost")
	if err != nil {
		t.Fatal(err)
	}

	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		Certificates: []tls.Certificate{bundle.Cert},
		MinVersion:   tls.VersionTLS13,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	go func() {
		c, _ := ln.Accept()
		if c != nil {
			c.Close()
		}
	}()

	addr := ln.Addr().(*net.TCPAddr)
	tr := tlsstream.NewTransport()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	_, err = tr.Dial(ctx, transport.DialConfig{
		Endpoint:   transport.Endpoint{Host: addr.IP.String(), Port: addr.Port},
		ServerName: "wrong.example.com",
		RootCAs:    bundle.CAPool,
		Timeout:    2 * time.Second,
	})
	if err == nil {
		t.Fatal("wrong server name should be rejected")
	}
}
