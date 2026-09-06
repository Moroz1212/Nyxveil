package ticketbroker_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nyxveil/client-windows/internal/ticketbroker"
)

func mustBegin(t *testing.T, b *ticketbroker.Broker, loc, reason, state string) ticketbroker.Request {
	t.Helper()
	req, err := b.Begin(loc, reason, state)
	if err != nil {
		t.Fatal(err)
	}
	return req
}

func TestRoundtrip(t *testing.T) {
	b := ticketbroker.New()
	req := mustBegin(t, b, "loc-a", "reconnect", "Reconnecting")
	go func() {
		time.Sleep(20 * time.Millisecond)
		if err := b.Provide(req.RequestID, "ticket-1"); err != nil {
			t.Errorf("provide: %v", err)
		}
	}()
	tok, err := b.Wait(context.Background(), req, time.Second)
	if err != nil || tok != "ticket-1" {
		t.Fatalf("got %q %v", tok, err)
	}
}

func TestWrongRequestIDRejected(t *testing.T) {
	b := ticketbroker.New()
	_ = mustBegin(t, b, "loc-a", "failover", "ConnectingTransport")
	err := b.Provide("deadbeefdeadbeefdeadbeefdeadbeef", "x")
	if !errors.Is(err, ticketbroker.ErrNoWaiter) {
		t.Fatalf("want ErrNoWaiter, got %v", err)
	}
}

func TestTimeout(t *testing.T) {
	b := ticketbroker.New()
	req := mustBegin(t, b, "loc-a", "reconnect", "Reconnecting")
	_, err := b.Wait(context.Background(), req, 30*time.Millisecond)
	if !errors.Is(err, ticketbroker.ErrTimeout) {
		t.Fatalf("want timeout, got %v", err)
	}
	if b.Pending() != 0 {
		t.Fatalf("waiter leaked")
	}
}

func TestDisconnectCancels(t *testing.T) {
	b := ticketbroker.New()
	req := mustBegin(t, b, "loc-a", "reconnect", "Reconnecting")
	done := make(chan error, 1)
	go func() {
		_, err := b.Wait(context.Background(), req, time.Minute)
		done <- err
	}()
	time.Sleep(20 * time.Millisecond)
	b.CancelAll()
	err := <-done
	if !errors.Is(err, ticketbroker.ErrCanceled) {
		t.Fatalf("want canceled, got %v", err)
	}
}

func TestConcurrentWaitersIsolated(t *testing.T) {
	b := ticketbroker.New()
	r1 := mustBegin(t, b, "loc-a", "a", "ConnectingTransport")
	r2 := mustBegin(t, b, "loc-a", "b", "ConnectingTransport")
	var wg sync.WaitGroup
	wg.Add(2)
	got := make([]string, 2)
	go func() {
		defer wg.Done()
		tok, err := b.Wait(context.Background(), r1, time.Second)
		if err != nil {
			t.Errorf("r1: %v", err)
			return
		}
		got[0] = tok
	}()
	go func() {
		defer wg.Done()
		tok, err := b.Wait(context.Background(), r2, time.Second)
		if err != nil {
			t.Errorf("r2: %v", err)
			return
		}
		got[1] = tok
	}()
	_ = b.Provide(r2.RequestID, "t2")
	_ = b.Provide(r1.RequestID, "t1")
	wg.Wait()
	if got[0] != "t1" || got[1] != "t2" {
		t.Fatalf("cross-talk: %#v", got)
	}
}

func TestProvideVsCancelAllNoPanic(t *testing.T) {
	const N = 5000
	for i := 0; i < N; i++ {
		b := ticketbroker.New()
		req := mustBegin(t, b, "loc", "r", "S")
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			_ = b.Provide(req.RequestID, "t")
		}()
		go func() {
			defer wg.Done()
			b.CancelAll()
		}()
		wg.Wait()
		_, _ = b.Wait(context.Background(), req, time.Millisecond)
	}
}

func TestProvideVsCancelRequestNoPanic(t *testing.T) {
	const N = 5000
	for i := 0; i < N; i++ {
		b := ticketbroker.New()
		req := mustBegin(t, b, "loc", "r", "S")
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			_ = b.Provide(req.RequestID, "t")
		}()
		go func() {
			defer wg.Done()
			b.CancelRequest(req.RequestID)
		}()
		wg.Wait()
	}
}

func TestTimeoutVsProvide(t *testing.T) {
	const N = 2000
	for i := 0; i < N; i++ {
		b := ticketbroker.New()
		req := mustBegin(t, b, "loc", "r", "S")
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, _ = b.Wait(context.Background(), req, time.Microsecond)
		}()
		go func() {
			defer wg.Done()
			_ = b.Provide(req.RequestID, "t")
		}()
		wg.Wait()
		if b.Pending() != 0 {
			t.Fatalf("waiter leak pending=%d", b.Pending())
		}
	}
}

func TestDisconnectWhileGUIReturnsTicket(t *testing.T) {
	b := ticketbroker.New()
	req := mustBegin(t, b, "loc-a", "reconnect", "Reconnecting")
	var got atomic.Value
	done := make(chan struct{})
	go func() {
		tok, err := b.Wait(context.Background(), req, time.Minute)
		got.Store([2]any{tok, err})
		close(done)
	}()
	time.Sleep(10 * time.Millisecond)
	go func() { _ = b.Provide(req.RequestID, "late-ticket") }()
	b.CancelAll()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Wait hung")
	}
	if b.Pending() != 0 {
		t.Fatal("waiter leak")
	}
}
