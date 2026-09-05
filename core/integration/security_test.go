package integration

import (
	"context"
	"crypto/tls"
	"net"
	"testing"
	"time"

	"github.com/nyxveil/nvp/core/internal/testutil"
	"github.com/nyxveil/nvp/core/transport"
	tlsstream "github.com/nyxveil/nvp/core/transport/tlsstream"
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
	// Real downgrade attempt: peer only offers TLS ≤1.2; NVP TLS client requires TLS 1.3.
	bundle, err := testutil.GenerateCertBundle("localhost")
	if err != nil {
		t.Fatal(err)
	}
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		Certificates: []tls.Certificate{bundle.Cert},
		MinVersion:   tls.VersionTLS12,
		MaxVersion:   tls.VersionTLS12,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		_ = c.(*tls.Conn).Handshake()
	}()

	addr := ln.Addr().(*net.TCPAddr)
	tr := tlsstream.NewTransport()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	_, err = tr.Dial(ctx, transport.DialConfig{
		Endpoint:   transport.Endpoint{Host: addr.IP.String(), Port: addr.Port},
		ServerName: bundle.ServerName,
		RootCAs:    bundle.CAPool,
		Timeout:    2 * time.Second,
	})
	if err == nil {
		t.Fatal("TLS 1.2-only peer must be rejected by TLS 1.3-only NVP client (downgrade)")
	}
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
