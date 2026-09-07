package engine

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/quic-go/quic-go"
)

// TestQuicGoSendDatagramRejectsLocally confirms quic-go@v0.48.2 returns
// *DatagramTooLargeError from SendDatagram without delivering an oversized
// payload to the peer (connection.SendDatagram checks length before queue.Add).
func TestQuicGoSendDatagramRejectsLocally(t *testing.T) {
	tlsCert := generateTestTLSCert(t)
	serverTLS := &tls.Config{
		Certificates: []tls.Certificate{tlsCert},
		NextProtos:   []string{"nvp-test"},
	}
	clientTLS := &tls.Config{
		InsecureSkipVerify: true,
		NextProtos:         []string{"nvp-test"},
	}

	udpAddr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	udpConn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer udpConn.Close()

	tr := &quic.Transport{Conn: udpConn}
	defer tr.Close()
	ln, err := tr.Listen(serverTLS, &quic.Config{EnableDatagrams: true})
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	var peerGot atomic.Int64
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		c, err := ln.Accept(ctx)
		if err != nil {
			return
		}
		for {
			b, err := c.ReceiveDatagram(ctx)
			if err != nil {
				return
			}
			peerGot.Add(int64(len(b)))
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	clientConn, err := quic.DialAddr(ctx, ln.Addr().String(), clientTLS, &quic.Config{EnableDatagrams: true})
	if err != nil {
		t.Fatal(err)
	}
	defer clientConn.CloseWithError(0, "done")

	str, err := clientConn.OpenStreamSync(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = str.Write([]byte("hi"))
	_ = str.Close()

	oversized := make([]byte, 65535)
	err = clientConn.SendDatagram(oversized)
	if err == nil {
		t.Fatal("expected DatagramTooLargeError")
	}
	var dtl *quic.DatagramTooLargeError
	if !errors.As(err, &dtl) || dtl.MaxDatagramPayloadSize <= 0 {
		t.Fatalf("want typed DatagramTooLargeError with MaxDatagramPayloadSize, got %v", err)
	}
	if dtl.Error() != "DATAGRAM frame too large" {
		t.Fatalf("error text=%q", dtl.Error())
	}

	okPayload := make([]byte, dtl.MaxDatagramPayloadSize)
	if err := clientConn.SendDatagram(okPayload); err != nil {
		t.Fatalf("exact max should succeed: %v", err)
	}
	if err := clientConn.SendDatagram(make([]byte, dtl.MaxDatagramPayloadSize+1)); err == nil {
		t.Fatal("max+1 should fail")
	}

	time.Sleep(150 * time.Millisecond)
	if peerGot.Load() >= 65535 {
		t.Fatalf("oversized probe must not be delivered to peer; peerGot=%d", peerGot.Load())
	}
}

func generateTestTLSCert(t *testing.T) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "nvp-test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1)},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	return cert
}
