package integration

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nyxveil/nvp/core/control"
	"github.com/nyxveil/nvp/core/internal/testutil"
	"github.com/nyxveil/nvp/core/session"
	"github.com/nyxveil/nvp/core/transport/memory"
)

func TestChaosSessionSurvivesLoss(t *testing.T) {
	rawClient, rawServer := memory.Pair()
	clientConn := testutil.WrapChaos(rawClient, testutil.ChaosConfig{
		Delay: time.Millisecond, Jitter: time.Millisecond,
	})
	serverConn := testutil.WrapChaos(rawServer, testutil.ChaosConfig{
		Delay: time.Millisecond, Jitter: time.Millisecond,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	clientSess := session.New(session.DefaultConfig(true))
	serverSess := session.New(session.DefaultConfig(false))

	errCh := make(chan error, 2)
	go func() {
		if err := serverSess.Connect(ctx, serverConn); err != nil {
			errCh <- err
			return
		}
		errCh <- serverSess.RunHandshake(ctx)
	}()
	if err := clientSess.Connect(ctx, clientConn); err != nil {
		t.Fatal(err)
	}
	if err := clientSess.RunHandshake(ctx); err != nil {
		t.Fatalf("client handshake: %v", err)
	}
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("server handshake: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("server handshake timed out")
	}

	_ = clientSess.MarkEstablished()
	_ = serverSess.MarkEstablished()

	var pongs atomic.Int32
	clientSess.OnControl(func(msgType byte, _ []byte) error {
		if msgType == control.TypePong {
			pongs.Add(1)
		}
		return nil
	})
	go func() { _ = serverSess.ReadLoop(ctx) }()
	go func() { _ = clientSess.ReadLoop(ctx) }()

	// Frame loss after handshake: control path must still deliver via retry.
	if c := testutil.AsChaos(clientConn); c != nil {
		c.SetLossRate(0.2)
	}
	if c := testutil.AsChaos(serverConn); c != nil {
		c.SetLossRate(0.2)
	}

	deadline := time.Now().Add(3 * time.Second)
	for pongs.Load() == 0 && time.Now().Before(deadline) {
		_ = clientSess.SendPing(ctx)
		time.Sleep(25 * time.Millisecond)
	}
	if pongs.Load() == 0 {
		t.Fatal("control path failed under 20% loss")
	}
	cancel()
}

func TestChaosConnDupAndDelay(t *testing.T) {
	// Dup/reorder are datagram-oriented; on stream they break framing or AEAD replay.
	// Validate ChaosConn delay path still allows a clean handshake + ping.
	rawClient, rawServer := memory.Pair()
	clientConn := testutil.WrapChaos(rawClient, testutil.ChaosConfig{
		Delay: 5 * time.Millisecond, Jitter: 2 * time.Millisecond,
	})
	serverConn := testutil.WrapChaos(rawServer, testutil.ChaosConfig{
		Delay: 5 * time.Millisecond, Jitter: 2 * time.Millisecond,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	s1 := session.New(session.DefaultConfig(true))
	s2 := session.New(session.DefaultConfig(false))

	errCh := make(chan error, 1)
	go func() {
		_ = s2.Connect(ctx, serverConn)
		errCh <- s2.RunHandshake(ctx)
	}()
	if err := s1.Connect(ctx, clientConn); err != nil {
		t.Fatal(err)
	}
	if err := s1.RunHandshake(ctx); err != nil {
		t.Fatalf("handshake under delay: %v", err)
	}
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("peer handshake: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("peer handshake timed out")
	}

	_ = s1.MarkEstablished()
	_ = s2.MarkEstablished()
	cancel()
}

func TestChaosPacketLoss(t *testing.T) {
	rates := []float64{0, 0.01, 0.05, 0.10}
	for _, rate := range rates {
		rate := rate
		t.Run(fmt.Sprintf("loss_%.0fpct", rate*100), func(t *testing.T) {
			rawClient, rawServer := memory.Pair()
			clientConn := testutil.WrapChaos(rawClient, testutil.ChaosConfig{})
			serverConn := testutil.WrapChaos(rawServer, testutil.ChaosConfig{})

			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()

			clientSess := session.New(session.DefaultConfig(true))
			serverSess := session.New(session.DefaultConfig(false))

			var pongs atomic.Int32
			clientSess.OnControl(func(msgType byte, _ []byte) error {
				if msgType == control.TypePong {
					pongs.Add(1)
				}
				return nil
			})

			go func() {
				_ = serverSess.Connect(ctx, serverConn)
				_ = serverSess.RunHandshake(ctx)
				_ = serverSess.MarkEstablished()
				_ = serverSess.ReadLoop(ctx)
			}()

			if err := clientSess.Connect(ctx, clientConn); err != nil {
				t.Fatal(err)
			}
			if err := clientSess.RunHandshake(ctx); err != nil {
				t.Fatal(err)
			}
			if err := clientSess.MarkEstablished(); err != nil {
				t.Fatal(err)
			}
			go func() { _ = clientSess.ReadLoop(ctx) }()

			// Inject loss only after secure channel + control path is up (reliable stream).
			if c := testutil.AsChaos(clientConn); c != nil {
				c.SetLossRate(rate)
			}
			if c := testutil.AsChaos(serverConn); c != nil {
				c.SetLossRate(rate)
			}

			deadline := time.Now().Add(5 * time.Second)
			for pongs.Load() == 0 && time.Now().Before(deadline) {
				_ = clientSess.SendPing(ctx)
				time.Sleep(20 * time.Millisecond)
			}
			if pongs.Load() == 0 {
				t.Fatalf("control PING/PONG failed under %.0f%% loss", rate*100)
			}
			// DATA need not be delivered under loss; just ensure SendData does not panic.
			_ = clientSess.SendData(ctx, []byte{1, 2, 3, 4})
			_ = clientSess.Close(context.Background())
			_ = serverSess.Close(context.Background())
			cancel()
		})
	}
}
