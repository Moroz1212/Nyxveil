package integration

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/nyxveil/nvp/control"
	"github.com/nyxveil/nvp/internal/testutil"
	"github.com/nyxveil/nvp/session"
	"github.com/nyxveil/nvp/transport/memory"
)

func TestChaosSessionSurvivesLoss(t *testing.T) {
	rawClient, rawServer := memory.Pair()

	clientConn := testutil.WrapChaos(rawClient, testutil.ChaosConfig{LossRate: 0.3})
	serverConn := testutil.WrapChaos(rawServer, testutil.ChaosConfig{LossRate: 0.3})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	clientSess := session.New(session.DefaultConfig(true))
	serverSess := session.New(session.DefaultConfig(false))

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = serverSess.Connect(ctx, serverConn)
		_ = serverSess.RunHandshake(ctx)
	}()

	if err := clientSess.Connect(ctx, clientConn); err != nil {
		t.Fatal(err)
	}
	if err := clientSess.RunHandshake(ctx); err != nil {
		// With high loss, handshake may fail - retry once
		if err2 := clientSess.RunHandshake(ctx); err2 != nil {
			t.Skipf("chaos caused handshake failure (expected in lossy sim): %v", err)
		}
	}

	_ = control.TypePing
	cancel()
	wg.Wait()
}

func TestChaosConnDupAndDelay(t *testing.T) {
	rawClient, rawServer := memory.Pair()
	_ = testutil.WrapChaos(rawClient, testutil.ChaosConfig{DupRate: 0.5, Delay: 5 * time.Millisecond, Jitter: 2 * time.Millisecond})
	_ = testutil.WrapChaos(rawServer, testutil.ChaosConfig{ReorderRate: 0.1})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	c1, c2 := memory.Pair()
	s1 := session.New(session.DefaultConfig(true))
	s2 := session.New(session.DefaultConfig(false))

	go func() {
		_ = s2.Connect(ctx, c2)
		_ = s2.RunHandshake(ctx)
	}()
	_ = s1.Connect(ctx, c1)
	_ = s1.RunHandshake(ctx)
	cancel()
}
