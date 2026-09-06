package integration

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nyxveil/nvp/core/session"
	"github.com/nyxveil/nvp/core/transport/memory"
)

// Forbidden plaintext markers must never appear on the transport before AEAD.
var forbiddenPlaintextMagic = []string{
	"NVP1",
	"NVP/1",
	"NYXVEIL",
	"NYX",
}

func TestFingerprintNoPlaintextMagicBeforeSecureChannel(t *testing.T) {
	clientConn, serverConn := memory.Pair()
	var captured []byte
	var mu sync.Mutex
	wrapped := &captureConn{Conn: clientConn, onWrite: func(b []byte) {
		mu.Lock()
		captured = append(captured, b...)
		mu.Unlock()
	}}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	clientSess := session.New(session.DefaultConfig(true))
	serverSess := session.New(session.DefaultConfig(false))

	go func() {
		_ = serverSess.Connect(ctx, serverConn)
		_ = serverSess.RunHandshake(ctx)
	}()

	if err := clientSess.Connect(ctx, wrapped); err != nil {
		t.Fatal(err)
	}
	if err := clientSess.RunHandshake(ctx); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()
	wire := string(captured)
	for _, mag := range forbiddenPlaintextMagic {
		if strings.Contains(wire, mag) {
			t.Fatalf("forbidden plaintext magic %q found in pre/post-handshake capture", mag)
		}
	}
	// Handshake init is version(2)+pubkey(32) with no ASCII banner.
	if len(captured) == 0 {
		t.Fatal("expected handshake bytes on wire")
	}
}

func TestFingerprintNoCustomALPNInProductionTransports(t *testing.T) {
	root := findModuleRoot(t)
	forbiddenALPNs := []string{`"nvp/1"`, `"nyxveil"`, `"nvp"`, `"vpn"`}
	paths := []string{
		filepath.Join(root, "core", "transport", "transport.go"),
		filepath.Join(root, "core", "transport", "quic", "transport.go"),
		filepath.Join(root, "core", "transport", "tlsstream", "transport.go"),
	}
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		src := string(data)
		for _, bad := range forbiddenALPNs {
			if strings.Contains(src, bad) {
				t.Fatalf("%s must not contain custom ALPN marker %s", p, bad)
			}
		}
		if strings.Contains(src, `"h2"`) {
			t.Fatalf("%s must not advertise fake ALPN h2", p)
		}
	}

	quicPath := filepath.Join(root, "core", "transport", "quic", "transport.go")
	quicSrc, err := os.ReadFile(quicPath)
	if err != nil {
		t.Fatal(err)
	}
	qs := string(quicSrc)
	if !strings.Contains(qs, `github.com/quic-go/quic-go/http3`) {
		t.Fatal("QUIC transport must use real http3 package for h3 ALPN")
	}
	if !strings.Contains(qs, `http3.NextProtoH3`) && !strings.Contains(qs, `"h3"`) {
		t.Fatal("QUIC transport must negotiate real h3 via http3")
	}
	if !strings.Contains(qs, `http.MethodConnect`) {
		t.Fatal("QUIC transport must speak HTTP/3 CONNECT")
	}

	tlsPath := filepath.Join(root, "core", "transport", "tlsstream", "transport.go")
	tlsSrc, err := os.ReadFile(tlsPath)
	if err != nil {
		t.Fatal(err)
	}
	ts := string(tlsSrc)
	if strings.Contains(ts, `"h3"`) || strings.Contains(ts, `"h2"`) {
		t.Fatal("TLS transport must not set h2/h3 ALPN")
	}
	if strings.Contains(ts, "NextProtos: []string{") {
		t.Fatal("TLS transport must leave NextProtos empty (no application ALPN)")
	}
}

func TestFingerprintPaddingChangesSizeVariance(t *testing.T) {
	payload := []byte("fixed-size-payload-for-padding-variance-test")

	sizesOff := collectCiphertextSizes(t, session.PaddingPolicy{Enabled: false}, payload, 40)
	sizesOn := collectCiphertextSizes(t, session.PaddingPolicy{
		Enabled:     true,
		MinBytes:    16,
		MaxBytes:    128,
		Probability: 1.0,
	}, payload, 40)

	varOff := variance(sizesOff)
	varOn := variance(sizesOn)
	if varOn <= varOff {
		t.Fatalf("padding enabled should increase size variance: off=%v on=%v sizesOff=%v sizesOn=%v",
			varOff, varOn, uniqueCount(sizesOff), uniqueCount(sizesOn))
	}
	if uniqueCount(sizesOn) < 2 {
		t.Fatalf("padding with Prob=1 and Max>Min should produce multiple wire sizes, got %v", sizesOn)
	}
}

func collectCiphertextSizes(t *testing.T, pad session.PaddingPolicy, payload []byte, n int) []int {
	t.Helper()
	sizes := make([]int, 0, n)
	for i := 0; i < n; i++ {
		clientConn, serverConn := memory.Pair()
		var last []byte
		var mu sync.Mutex
		wrapped := &captureConn{Conn: clientConn, onWrite: func(b []byte) {
			mu.Lock()
			last = append([]byte(nil), b...)
			mu.Unlock()
		}}

		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		clientCfg := session.DefaultConfig(true)
		clientCfg.PaddingPolicy = pad
		clientSess := session.New(clientCfg)
		serverSess := session.New(session.DefaultConfig(false))

		done := make(chan struct{})
		go func() {
			defer close(done)
			_ = serverSess.Connect(ctx, serverConn)
			_ = serverSess.RunHandshake(ctx)
			_ = serverSess.MarkEstablished()
			_ = serverSess.ReadLoop(ctx)
		}()

		if err := clientSess.Connect(ctx, wrapped); err != nil {
			cancel()
			t.Fatal(err)
		}
		if err := clientSess.RunHandshake(ctx); err != nil {
			cancel()
			t.Fatal(err)
		}
		if err := clientSess.MarkEstablished(); err != nil {
			cancel()
			t.Fatal(err)
		}
		mu.Lock()
		last = nil
		mu.Unlock()
		if err := clientSess.SendData(ctx, payload); err != nil {
			cancel()
			t.Fatal(err)
		}
		mu.Lock()
		if len(last) == 0 {
			mu.Unlock()
			cancel()
			t.Fatal("no ciphertext captured")
		}
		sizes = append(sizes, len(last))
		mu.Unlock()
		_ = clientSess.Close(context.Background())
		_ = serverSess.Close(context.Background())
		cancel()
		<-done
	}
	return sizes
}

func variance(vals []int) float64 {
	if len(vals) == 0 {
		return 0
	}
	var sum float64
	for _, v := range vals {
		sum += float64(v)
	}
	mean := sum / float64(len(vals))
	var acc float64
	for _, v := range vals {
		d := float64(v) - mean
		acc += d * d
	}
	return acc / float64(len(vals))
}

func uniqueCount(vals []int) int {
	m := map[int]struct{}{}
	for _, v := range vals {
		m[v] = struct{}{}
	}
	return len(m)
}

func findModuleRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	dir := wd
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found")
		}
		dir = parent
	}
}
