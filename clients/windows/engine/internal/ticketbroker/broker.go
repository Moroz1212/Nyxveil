package ticketbroker

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
	"time"
)

var (
	ErrTimeout      = errors.New("ticketbroker: timeout waiting for access ticket")
	ErrCanceled     = errors.New("ticketbroker: canceled")
	ErrWrongRequest = errors.New("ticketbroker: wrong request_id")
	ErrNoWaiter     = errors.New("ticketbroker: no waiter for request_id")
	ErrRand         = errors.New("ticketbroker: crypto/rand failed")
)

// Request is a pending NeedAccessTicket correlated by RequestID.
type Request struct {
	RequestID  string
	LocationID string
	Reason     string
	State      string
}

// waiter holds one pending request. Cancellation never closes a channel that
// Provide might still send on; Provide and Cancel share an explicit state under mu.
type waiter struct {
	mu        sync.Mutex
	ticket    string
	done      bool // provided, canceled, or timed out / forgotten
	canceled  bool
	ready     chan struct{} // closed once when terminal
	closeOnce sync.Once
}

func newWaiter() *waiter {
	return &waiter{ready: make(chan struct{})}
}

func (w *waiter) provide(ticket string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.done {
		return false
	}
	w.ticket = ticket
	w.done = true
	w.closeOnce.Do(func() { close(w.ready) })
	return true
}

func (w *waiter) markCanceled() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.done {
		return
	}
	w.canceled = true
	w.done = true
	w.closeOnce.Do(func() { close(w.ready) })
}

func (w *waiter) result() (string, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.canceled || w.ticket == "" {
		return "", ErrCanceled
	}
	return w.ticket, nil
}

// Broker correlates provide_access_ticket replies by request_id.
type Broker struct {
	mu      sync.Mutex
	waiters map[string]*waiter
	byLoc   map[string]string
}

func New() *Broker {
	return &Broker{
		waiters: make(map[string]*waiter),
		byLoc:   make(map[string]string),
	}
}

func newRequestID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// Begin registers a waiter and returns the request metadata to emit on the pipe.
func (b *Broker) Begin(locationID, reason, state string) (Request, error) {
	id, err := newRequestID()
	if err != nil {
		return Request{}, ErrRand
	}
	w := newWaiter()
	b.mu.Lock()
	b.waiters[id] = w
	b.byLoc[locationID] = id
	b.mu.Unlock()
	return Request{RequestID: id, LocationID: locationID, Reason: reason, State: state}, nil
}

// Wait blocks until Provide, Cancel, parent ctx done, or timeout.
func (b *Broker) Wait(ctx context.Context, req Request, timeout time.Duration) (string, error) {
	b.mu.Lock()
	w := b.waiters[req.RequestID]
	b.mu.Unlock()
	if w == nil {
		return "", ErrNoWaiter
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		w.markCanceled()
		b.forget(req.RequestID)
		return "", ErrCanceled
	case <-timer.C:
		w.markCanceled()
		b.forget(req.RequestID)
		return "", ErrTimeout
	case <-w.ready:
		b.forget(req.RequestID)
		return w.result()
	}
}

// Provide delivers a ticket for an exact request_id.
func (b *Broker) Provide(requestID, ticket string) error {
	if requestID == "" {
		return ErrWrongRequest
	}
	b.mu.Lock()
	w, ok := b.waiters[requestID]
	b.mu.Unlock()
	if !ok {
		return ErrNoWaiter
	}
	_ = w.provide(ticket) // false if already canceled/satisfied — not an error
	return nil
}

// CancelRequest cancels one waiter without closing a send channel.
func (b *Broker) CancelRequest(requestID string) {
	b.mu.Lock()
	w, ok := b.waiters[requestID]
	if ok {
		delete(b.waiters, requestID)
	}
	b.mu.Unlock()
	if ok {
		w.markCanceled()
	}
}

// CancelAll cancels every waiter (Disconnect).
func (b *Broker) CancelAll() {
	b.mu.Lock()
	ws := make([]*waiter, 0, len(b.waiters))
	for id, w := range b.waiters {
		ws = append(ws, w)
		delete(b.waiters, id)
	}
	b.byLoc = make(map[string]string)
	b.mu.Unlock()
	for _, w := range ws {
		w.markCanceled()
	}
}

func (b *Broker) forget(requestID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.waiters, requestID)
}

func (b *Broker) Pending() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.waiters)
}
