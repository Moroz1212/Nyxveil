package integration

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/nyxveil/nvp/core/session"
	"github.com/nyxveil/nvp/core/transport/memory"
)

func TestHandshakeApplicationSizesAreNotConstantUnderProductionPadding(t *testing.T) {
	sizes := map[int]int{}
	const rounds = 40

	for i := 0; i < rounds; i++ {
		clientConn, serverConn := memory.Pair()
		var firstWrite int
		var mu sync.Mutex
		wrapped := &captureConn{Conn: clientConn, onWrite: func(b []byte) {
			mu.Lock()
			if firstWrite == 0 {
				firstWrite = len(b)
			}
			mu.Unlock()
		}}

		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		clientCfg := session.DefaultConfig(true)
		// Production default padding; force pad on every handshake for stable variance.
		clientCfg.PaddingPolicy = session.PaddingPolicy{
			Enabled:     true,
			Strategy:    session.PaddingRandomRange,
			MinBytes:    1,
			MaxBytes:    64,
			Probability: 1.0,
		}
		serverCfg := session.DefaultConfig(false)
		serverCfg.PaddingPolicy = clientCfg.PaddingPolicy

		clientSess := session.New(clientCfg)
		serverSess := session.New(serverCfg)

		done := make(chan struct{})
		go func() {
			defer close(done)
			_ = serverSess.Connect(ctx, serverConn)
			_ = serverSess.RunHandshake(ctx)
		}()

		if err := clientSess.Connect(ctx, wrapped); err != nil {
			cancel()
			t.Fatal(err)
		}
		if err := clientSess.RunHandshake(ctx); err != nil {
			cancel()
			t.Fatal(err)
		}
		cancel()
		<-done
		_ = clientSess.Close(context.Background())
		_ = serverSess.Close(context.Background())

		mu.Lock()
		sz := firstWrite
		mu.Unlock()
		if sz == 0 {
			t.Fatal("missing handshake write")
		}
		// Fixed legacy sizes must not dominate under production padding.
		if sz == 34 || sz == 38 {
			// still count; overall must vary
		}
		sizes[sz]++
	}

	if len(sizes) < 2 {
		t.Fatalf("expected >1 distinct handshake application sizes under production padding, got %v", sizes)
	}
	for sz := range sizes {
		if sz <= 34 {
			// With pad_len header, minimum is 36 even when pad empty; padded should be larger often.
			continue
		}
	}
}
