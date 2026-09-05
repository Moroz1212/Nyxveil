package integration

import (
	"context"
	"crypto/ed25519"
	"crypto/tls"
	"fmt"
	"net"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nyxveil/nvp/core/auth/ticket"
	"github.com/nyxveil/nvp/core/authhandler"
	"github.com/nyxveil/nvp/core/control"
	"github.com/nyxveil/nvp/core/internal/testutil"
	"github.com/nyxveil/nvp/core/session"
	"github.com/nyxveil/nvp/core/transport"
	quictransport "github.com/nyxveil/nvp/core/transport/quic"
	tlsstream "github.com/nyxveil/nvp/core/transport/tlsstream"
)

type realLoopProfile int

const (
	realTLS realLoopProfile = iota
	realQUIC
)

// realLoopHarness is a real-socket TLS or QUIC node with AuthHandler + ticket issuer.
type realLoopHarness struct {
	bundle  *testutil.CertBundle
	tok     string
	devPriv ed25519.PrivateKey
	ln      transport.Listener
	profile transport.Profile
	closeLn func()
}

func startEchoLoop(t *testing.T, profile realLoopProfile, serverCfg session.Config) *realLoopHarness {
	t.Helper()
	tok, devPriv, verifier := setupTicket(t)
	bundle, err := testutil.GenerateCertBundle("localhost")
	if err != nil {
		t.Fatal(err)
	}
	tlsCfg := &tls.Config{
		Certificates: []tls.Certificate{bundle.Cert},
		MinVersion:   tls.VersionTLS13,
	}
	ctx := context.Background()
	var ln transport.Listener
	var trProfile transport.Profile
	switch profile {
	case realTLS:
		ln, err = tlsstream.NewTransport().Listen(ctx, "127.0.0.1:0", tlsCfg)
		trProfile = transport.ProfileTLSTCP
	case realQUIC:
		ln, err = quictransport.NewTransport().Listen(ctx, "127.0.0.1:0", tlsCfg)
		trProfile = transport.ProfileQUICUDP
	default:
		t.Fatalf("unknown profile %v", profile)
	}
	if err != nil {
		t.Fatal(err)
	}
	if serverCfg.ReplayWindow == 0 {
		serverCfg = session.DefaultConfig(false)
	} else {
		serverCfg.IsClient = false
	}

	authHandler := authhandler.NewAuthHandler("fi-hel-01", "fi", verifier)
	acceptCtx, acceptCancel := context.WithCancel(context.Background())
	go func() {
		for {
			conn, err := ln.Accept(acceptCtx)
			if err != nil {
				return
			}
			go func(c transport.Conn) {
				defer c.Close()
				sess := session.New(serverCfg)
				sess.OnControl(func(msgType byte, payload []byte) error {
					if msgType == control.TypeAuth {
						return authHandler.HandleAuth(acceptCtx, sess, payload)
					}
					return nil
				})
				sess.OnData(func(b []byte) error {
					return sess.SendData(context.Background(), append([]byte("echo:"), b...))
				})
				if err := sess.Connect(acceptCtx, c); err != nil {
					return
				}
				if err := sess.RunHandshake(acceptCtx); err != nil {
					return
				}
				_ = sess.ReadLoop(acceptCtx)
			}(conn)
		}
	}()

	h := &realLoopHarness{
		bundle:  bundle,
		tok:     tok,
		devPriv: devPriv,
		ln:      ln,
		profile: trProfile,
		closeLn: func() {
			acceptCancel()
			_ = ln.Close()
		},
	}
	t.Cleanup(h.closeLn)
	return h
}

func (h *realLoopHarness) dial(t *testing.T, ctx context.Context) transport.Conn {
	t.Helper()
	host, portStr, err := net.SplitHostPort(h.ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatal(err)
	}
	cfg := transport.DialConfig{
		Endpoint:   transport.Endpoint{Host: host, Port: port},
		ServerName: h.bundle.ServerName,
		RootCAs:    h.bundle.CAPool,
		Timeout:    5 * time.Second,
	}
	var conn transport.Conn
	switch h.profile {
	case transport.ProfileTLSTCP:
		conn, err = tlsstream.NewTransport().Dial(ctx, cfg)
	case transport.ProfileQUICUDP:
		conn, err = quictransport.NewTransport().Dial(ctx, cfg)
	default:
		t.Fatalf("unsupported profile %s", h.profile)
	}
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	return conn
}

func openEstablishedClient(t *testing.T, h *realLoopHarness, clientCfg session.Config) (*session.Session, transport.Conn, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	conn := h.dial(t, ctx)
	if clientCfg.ReplayWindow == 0 {
		clientCfg = session.DefaultConfig(true)
	} else {
		clientCfg.IsClient = true
	}
	client := session.New(clientCfg)
	if err := client.Connect(ctx, conn); err != nil {
		cancel()
		conn.Close()
		t.Fatal(err)
	}
	if err := client.RunHandshake(ctx); err != nil {
		cancel()
		conn.Close()
		t.Fatal(err)
	}
	authBody, err := ticket.EncodeAuthPayload(h.tok, client.Transcript(), h.devPriv)
	if err != nil {
		cancel()
		conn.Close()
		t.Fatal(err)
	}
	if err := client.SendAuth(ctx, authBody); err != nil {
		cancel()
		conn.Close()
		t.Fatal(err)
	}
	if err := client.WaitEstablished(ctx); err != nil {
		cancel()
		conn.Close()
		t.Fatalf("WaitEstablished: %v (state=%s)", err, client.State())
	}
	if client.State() != session.StateEstablished {
		cancel()
		conn.Close()
		t.Fatalf("want ESTABLISHED got %s", client.State())
	}
	return client, conn, cancel
}

func runFullSession(t *testing.T, profile realLoopProfile) {
	t.Helper()
	h := startEchoLoop(t, profile, session.DefaultConfig(false))
	client, conn, cancel := openEstablishedClient(t, h, session.DefaultConfig(true))
	defer cancel()
	defer conn.Close()

	var echo atomic.Value
	pongCh := make(chan struct{}, 1)
	client.OnData(func(b []byte) error {
		echo.Store(append([]byte(nil), b...))
		return nil
	})
	client.OnControl(func(msgType byte, _ []byte) error {
		if msgType == control.TypePong {
			select {
			case pongCh <- struct{}{}:
			default:
			}
		}
		return nil
	})

	loopCtx, loopCancel := context.WithCancel(context.Background())
	defer loopCancel()
	go func() { _ = client.ReadLoop(loopCtx) }()

	payload := []byte("nvp-real-loopback-data")
	if err := client.SendData(context.Background(), payload); err != nil {
		t.Fatalf("DATA C→S: %v", err)
	}
	deadline := time.After(3 * time.Second)
	for {
		if v, ok := echo.Load().([]byte); ok && string(v) == "echo:"+string(payload) {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("DATA S→C echo timeout; got=%v", echo.Load())
		default:
			time.Sleep(20 * time.Millisecond)
		}
	}

	if err := client.SendPing(context.Background()); err != nil {
		t.Fatalf("PING: %v", err)
	}
	select {
	case <-pongCh:
	case <-time.After(3 * time.Second):
		t.Fatal("PONG timeout")
	}

	if err := client.Close(context.Background()); err != nil {
		t.Fatalf("close: %v", err)
	}
}

func TestRealTLSFullSession(t *testing.T) {
	runFullSession(t, realTLS)
}

func TestRealQUICFullSession(t *testing.T) {
	runFullSession(t, realQUIC)
}

func runRekeyAndPostRekeyData(t *testing.T, profile realLoopProfile) {
	t.Helper()
	serverCfg := session.DefaultConfig(false)
	serverCfg.RekeyPacketCount = 1 << 60
	serverCfg.RekeyByteCount = 1 << 60
	serverCfg.RekeyInterval = 24 * time.Hour
	serverCfg.ForceStreamData = true
	h := startEchoLoop(t, profile, serverCfg)

	clientCfg := session.DefaultConfig(true)
	clientCfg.RekeyPacketCount = 3
	clientCfg.RekeyByteCount = 1 << 60
	clientCfg.RekeyInterval = 50 * time.Millisecond
	clientCfg.RekeyAckTimeout = 5 * time.Second
	clientCfg.ForceStreamData = true

	client, conn, cancel := openEstablishedClient(t, h, clientCfg)
	defer cancel()
	defer conn.Close()

	var lastEcho atomic.Value
	client.OnData(func(b []byte) error {
		lastEcho.Store(append([]byte(nil), b...))
		return nil
	})

	loopCtx, loopCancel := context.WithCancel(context.Background())
	defer loopCancel()
	errCh := make(chan error, 1)
	go func() { errCh <- client.ReadLoop(loopCtx) }()

	deadline := time.After(12 * time.Second)
	for i := 0; client.Epoch() < 2; i++ {
		select {
		case <-deadline:
			t.Fatalf("expected rekey epoch >= 2, got %d state=%s rekeyErr=%v readErr=%v",
				client.Epoch(), client.State(), client.RekeyLastError(), drainErr(errCh))
		case err := <-errCh:
			t.Fatalf("ReadLoop exited early: %v state=%s epoch=%d rekeyErr=%v",
				err, client.State(), client.Epoch(), client.RekeyLastError())
		default:
		}
		msg := []byte(fmt.Sprintf("rekey-pkt-%d", i))
		if err := client.SendData(context.Background(), msg); err != nil {
			t.Fatalf("SendData %d: %v state=%s", i, err, client.State())
		}
		time.Sleep(40 * time.Millisecond)
	}

	// QUIC DATA uses DATAGRAM (unreliable). Retry until an echo arrives.
	post := []byte("post-rekey-data")
	want := "echo:" + string(post)
	deadline = time.After(10 * time.Second)
	for {
		if v, ok := lastEcho.Load().([]byte); ok && string(v) == want {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("post-rekey echo timeout; got=%v epoch=%d", lastEcho.Load(), client.Epoch())
		default:
			_ = client.SendData(context.Background(), post)
			time.Sleep(50 * time.Millisecond)
		}
	}
	_ = client.Close(context.Background())
}

func drainErr(ch <-chan error) error {
	select {
	case err := <-ch:
		return err
	default:
		return nil
	}
}

func TestRealTLSRekeyAndPostRekeyData(t *testing.T) {
	runRekeyAndPostRekeyData(t, realTLS)
}

func TestRealQUICRekeyAndPostRekeyData(t *testing.T) {
	runRekeyAndPostRekeyData(t, realQUIC)
}

func TestNoGoroutineLeakAfterSessionClose(t *testing.T) {
	runtime.GC()
	time.Sleep(50 * time.Millisecond)
	before := runtime.NumGoroutine()

	h := startEchoLoop(t, realTLS, session.DefaultConfig(false))
	client, conn, cancel := openEstablishedClient(t, h, session.DefaultConfig(true))
	loopCtx, loopCancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = client.ReadLoop(loopCtx)
	}()
	_ = client.SendData(context.Background(), []byte("leak-check"))
	_ = client.SendPing(context.Background())
	time.Sleep(100 * time.Millisecond)

	loopCancel()
	_ = client.Close(context.Background())
	_ = conn.Close()
	cancel()
	h.closeLn()
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("ReadLoop did not exit")
	}

	deadline := time.After(3 * time.Second)
	var after int
	for {
		runtime.GC()
		time.Sleep(50 * time.Millisecond)
		after = runtime.NumGoroutine()
		if after-before <= 8 {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("goroutine leak: before=%d after=%d delta=%d", before, after, after-before)
		default:
		}
	}
}
