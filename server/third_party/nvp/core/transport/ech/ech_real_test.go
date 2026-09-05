package ech_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"math/big"
	"net"
	"testing"
	"time"

	"github.com/nyxveil/nvp/core/transport"
	"github.com/nyxveil/nvp/core/transport/ech"
	"github.com/nyxveil/nvp/core/transport/serverconfig"
	tlsstream "github.com/nyxveil/nvp/core/transport/tlsstream"
)

const (
	echPublicName = "public.localhost"
	echSecretName = "secret.localhost"
)

func TestECHRealLoopbackHandshake(t *testing.T) {
	clientState, serverState := echLoopback(t, echLoopOpts{})
	if !clientState.ECHAccepted {
		t.Fatal("client ConnectionState.ECHAccepted == false (want real ECH negotiation)")
	}
	if !serverState.ECHAccepted {
		t.Fatal("server ConnectionState.ECHAccepted == false (want real ECH negotiation)")
	}
}

func TestECHRequiredRealHandshake(t *testing.T) {
	clientState, serverState := echLoopback(t, echLoopOpts{requireECH: true})
	if !clientState.ECHAccepted || !serverState.ECHAccepted {
		t.Fatal("ECHRequired handshake must set ECHAccepted on both sides")
	}
}

func TestECHWrongConfigFails(t *testing.T) {
	serverKey, err := ech.GenerateKey(echPublicName, 1)
	if err != nil {
		t.Fatal(err)
	}
	wrongKey, err := ech.GenerateKey(echPublicName, 2)
	if err != nil {
		t.Fatal(err)
	}
	cert, pool := multiNameCert(t, echPublicName, echSecretName)

	ctx := context.Background()
	ln, err := serverconfig.NewTLSListener(ctx, "127.0.0.1:0", serverconfig.TLSServerConfig{
		Cert:    cert,
		ECHKeys: ech.NewKeySet([]tls.EncryptedClientHelloKey{serverKey.Key}),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	go func() {
		c, err := ln.Accept(ctx)
		if err != nil {
			return
		}
		_ = c.Close()
	}()

	client := tlsstream.NewTransport()
	_, err = client.Dial(ctx, transport.DialConfig{
		Endpoint:      transport.Endpoint{Host: "127.0.0.1", Port: mustPort(ln.Addr().String())},
		ServerName:    echSecretName,
		RootCAs:       pool,
		Timeout:       5 * time.Second,
		ECHPolicy:     transport.ECHRequired,
		ECHConfigList: wrongKey.ConfigList,
	})
	if err == nil {
		t.Fatal("expected handshake failure with wrong ECH config")
	}
}

func TestECHRotationRealHandshake(t *testing.T) {
	oldKey, err := ech.GenerateKey(echPublicName, 10)
	if err != nil {
		t.Fatal(err)
	}
	newKey, err := ech.GenerateKey(echPublicName, 11)
	if err != nil {
		t.Fatal(err)
	}
	set := ech.NewKeySet([]tls.EncryptedClientHelloKey{oldKey.Key, newKey.Key})

	clientState, serverState := echLoopback(t, echLoopOpts{
		serverKeys: set,
		clientList: newKey.ConfigList,
	})
	if !clientState.ECHAccepted || !serverState.ECHAccepted {
		t.Fatal("rotation overlap must accept client using new config")
	}

	set.Rotate([]tls.EncryptedClientHelloKey{newKey.Key})
	clientState, serverState = echLoopback(t, echLoopOpts{
		serverKeys: set,
		clientList: newKey.ConfigList,
	})
	if !clientState.ECHAccepted || !serverState.ECHAccepted {
		t.Fatal("post-rotation handshake with new key must accept ECH")
	}
}

type echLoopOpts struct {
	serverKeys *ech.KeySet
	clientList []byte
	requireECH bool
}

func echLoopback(t *testing.T, opts echLoopOpts) (clientState, serverState tls.ConnectionState) {
	t.Helper()
	keySet := opts.serverKeys
	clientList := opts.clientList
	if keySet == nil {
		gen, err := ech.GenerateKey(echPublicName, 42)
		if err != nil {
			t.Fatal(err)
		}
		keySet = ech.NewKeySet([]tls.EncryptedClientHelloKey{gen.Key})
		clientList = gen.ConfigList
	}

	cert, pool := multiNameCert(t, echPublicName, echSecretName)
	ctx := context.Background()
	ln, err := serverconfig.NewTLSListener(ctx, "127.0.0.1:0", serverconfig.TLSServerConfig{
		Cert:       cert,
		ECHKeys:    keySet,
		RequireECH: opts.requireECH,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	type result struct {
		state tls.ConnectionState
		err   error
	}
	srvCh := make(chan result, 1)
	go func() {
		c, err := ln.Accept(ctx)
		if err != nil {
			srvCh <- result{err: err}
			return
		}
		defer c.Close()
		stater, ok := c.(interface{ ConnectionState() tls.ConnectionState })
		if !ok {
			srvCh <- result{err: fmt.Errorf("conn missing ConnectionState")}
			return
		}
		st := stater.ConnectionState()
		// Drain one framed read so the client Write completes cleanly.
		_, _ = c.Read(ctx)
		srvCh <- result{state: st}
	}()

	tlsCfg := &tls.Config{
		MinVersion:                     tls.VersionTLS13,
		ServerName:                     echSecretName,
		RootCAs:                        pool,
		EncryptedClientHelloConfigList: clientList,
	}
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	raw, err := dialer.DialContext(ctx, "tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	client := tls.Client(raw, tlsCfg)
	if err := client.HandshakeContext(ctx); err != nil {
		t.Fatalf("client handshake: %v", err)
	}
	clientState = client.ConnectionState()

	// Send a minimal length-prefixed frame so Accept path can finish.
	frame := []byte{0, 0, 0, 1, 0x2a}
	if _, err := client.Write(frame); err != nil {
		t.Fatal(err)
	}
	_ = client.Close()

	srv := <-srvCh
	if srv.err != nil {
		t.Fatalf("server accept: %v", srv.err)
	}
	return clientState, srv.state
}

func multiNameCert(t *testing.T, names ...string) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	caTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "NVP ECH Test CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}

	srvKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	dns := append([]string{}, names...)
	dns = append(dns, "localhost")
	srvTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: names[0]},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     dns,
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	srvDER, err := x509.CreateCertificate(rand.Reader, srvTmpl, caCert, &srvKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	cert := tls.Certificate{
		Certificate: [][]byte{srvDER, caDER},
		PrivateKey:  srvKey,
	}
	pool := x509.NewCertPool()
	pool.AddCert(caCert)
	return cert, pool
}

func mustPort(addr string) int {
	_, p, _ := net.SplitHostPort(addr)
	var port int
	fmt.Sscanf(p, "%d", &port)
	return port
}
