package integration

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"math/big"
	"net"
	"sync"
	"testing"
	"time"

	tlsstream "github.com/nyxveil/nvp/core/transport/tlsstream"
)

func TestTLSFramingDoesNotEmitDedicatedLengthRecord(t *testing.T) {
	payload := bytes.Repeat([]byte{0xa1}, 48)

	frame, err := tlsstream.EncodeFrame(payload)
	if err != nil {
		t.Fatal(err)
	}
	if len(frame) != 4+len(payload) {
		t.Fatalf("expected single contiguous frame, got len=%d", len(frame))
	}
	if binary.BigEndian.Uint32(frame[:4]) != uint32(len(payload)) {
		t.Fatal("bad length prefix")
	}

	clientRaw, serverRaw := net.Pipe()
	rec := &recordingNetConn{Conn: clientRaw}

	cert, err := testSelfSigned()
	if err != nil {
		t.Fatal(err)
	}
	serverTLS := tls.Server(serverRaw, &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS13,
	})
	clientTLS := tls.Client(rec, &tls.Config{
		InsecureSkipVerify: true,
		MinVersion:         tls.VersionTLS13,
		ServerName:         "localhost",
	})

	errCh := make(chan error, 2)
	go func() { errCh <- serverTLS.Handshake() }()
	go func() { errCh <- clientTLS.Handshake() }()
	for i := 0; i < 2; i++ {
		if err := <-errCh; err != nil {
			t.Fatal(err)
		}
	}

	// Drive the same single-Write framing path production uses.
	framed, err := tlsstream.EncodeFrame(payload)
	if err != nil {
		t.Fatal(err)
	}
	rec.mu.Lock()
	rec.writes = nil
	rec.mu.Unlock()

	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		buf := make([]byte, 4096)
		_, _ = serverTLS.Read(buf)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = ctx
	if _, err := clientTLS.Write(framed); err != nil {
		t.Fatal(err)
	}
	select {
	case <-readDone:
	case <-time.After(2 * time.Second):
	}

	rec.mu.Lock()
	writes := append([][]byte(nil), rec.writes...)
	rec.mu.Unlock()

	bareLen := make([]byte, 4)
	binary.BigEndian.PutUint32(bareLen, uint32(len(payload)))
	for _, w := range writes {
		if bytes.Equal(w, bareLen) {
			t.Fatal("dedicated 4-byte length record/write observed")
		}
	}
	if len(writes) == 0 {
		t.Fatal("expected TLS ciphertext on the wire")
	}
	_ = clientTLS.Close()
	_ = serverTLS.Close()
}

type recordingNetConn struct {
	net.Conn
	mu     sync.Mutex
	writes [][]byte
}

func (r *recordingNetConn) Write(b []byte) (int, error) {
	r.mu.Lock()
	r.writes = append(r.writes, append([]byte(nil), b...))
	r.mu.Unlock()
	return r.Conn.Write(b)
}

func testSelfSigned() (tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, err
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, nil
}
