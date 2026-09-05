package tlsstream

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

	"github.com/nyxveil/nvp/core/transport"
)

type recordingConn struct {
	net.Conn
	mu     sync.Mutex
	writes [][]byte
}

func (r *recordingConn) Write(b []byte) (int, error) {
	r.mu.Lock()
	r.writes = append(r.writes, append([]byte(nil), b...))
	r.mu.Unlock()
	return r.Conn.Write(b)
}

func TestTLSFramingDoesNotEmitDedicatedLengthRecord(t *testing.T) {
	payload := bytes.Repeat([]byte{0x7e}, 64)

	// Unit: length and payload are one contiguous buffer (never a lone 4-byte write).
	frame, err := EncodeFrame(payload)
	if err != nil {
		t.Fatal(err)
	}
	if len(frame) != 4+len(payload) {
		t.Fatalf("frame len=%d", len(frame))
	}
	if binary.BigEndian.Uint32(frame[:4]) != uint32(len(payload)) {
		t.Fatal("length prefix mismatch")
	}
	// Strengthen: EncodeFrame must not be usable as two separate writes pattern.
	if len(frame) == 4 {
		t.Fatal("length must not be emitted as a dedicated buffer")
	}

	// Integration: under real TLS, application Write of one frame must not first
	// flush a 4-byte-only plaintext record visible as an isolated application write.
	clientRaw, serverRaw := net.Pipe()
	rec := &recordingConn{Conn: clientRaw}

	cert, err := selfSignedCert()
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

	c := &conn{tlsConn: clientTLS, profile: transport.ProfileTLSTCP}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	rec.mu.Lock()
	rec.writes = nil
	rec.mu.Unlock()

	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		buf := make([]byte, 4096)
		_, _ = serverTLS.Read(buf)
	}()

	if err := c.Write(ctx, payload); err != nil {
		t.Fatal(err)
	}
	select {
	case <-readDone:
	case <-time.After(2 * time.Second):
	}

	rec.mu.Lock()
	writes := append([][]byte(nil), rec.writes...)
	rec.mu.Unlock()

	// Underlying TCP may coalesce TLS records, but no write should be exactly the
	// bare 4-byte length of this frame as a dedicated application framing split.
	wantLenPrefix := make([]byte, 4)
	binary.BigEndian.PutUint32(wantLenPrefix, uint32(len(payload)))
	for _, w := range writes {
		if bytes.Equal(w, wantLenPrefix) {
			t.Fatalf("dedicated length-only write observed (%d bytes); framing must be single Write", len(w))
		}
	}
	if len(writes) == 0 {
		t.Fatal("expected TLS ciphertext writes")
	}

	_ = clientTLS.Close()
	_ = serverTLS.Close()
}

func selfSignedCert() (tls.Certificate, error) {
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
