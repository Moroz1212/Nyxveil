package diag

import (
	"sync"
	"time"
)

// DefaultCap is the in-memory ring capacity for IPC backlog.
const DefaultCap = 3000

// Ring is a thread-safe fixed-capacity event buffer (oldest dropped).
type Ring struct {
	mu   sync.RWMutex
	buf  []Event
	cap  int
	seq  uint64
	subs map[uint64]chan Event
}

// NewRing creates a ring with the given capacity (minimum 8).
func NewRing(capacity int) *Ring {
	if capacity < 8 {
		capacity = 8
	}
	return &Ring{
		buf:  make([]Event, 0, capacity),
		cap:  capacity,
		subs: make(map[uint64]chan Event),
	}
}

// Append stores and fans out to live subscribers (non-blocking per sub).
func (r *Ring) Append(e Event) {
	if e.Time.IsZero() {
		e.Time = time.Now()
	}
	e.Message = Sanitize(e.Message)
	e.Component = Sanitize(e.Component)
	e.Event = Sanitize(e.Event)

	r.mu.Lock()
	if len(r.buf) >= r.cap {
		copy(r.buf, r.buf[1:])
		r.buf[len(r.buf)-1] = e
		r.buf = r.buf[:r.cap]
	} else {
		r.buf = append(r.buf, e)
	}
	subs := make([]chan Event, 0, len(r.subs))
	for _, ch := range r.subs {
		subs = append(subs, ch)
	}
	r.mu.Unlock()

	for _, ch := range subs {
		select {
		case ch <- e:
		default:
			// Drop if subscriber is slow — backlog still has the event.
		}
	}
}

// Snapshot returns a copy of current events in order (oldest → newest).
func (r *Ring) Snapshot() []Event {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Event, len(r.buf))
	copy(out, r.buf)
	return out
}

// Len returns the number of buffered events.
func (r *Ring) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.buf)
}

// Cap returns ring capacity.
func (r *Ring) Cap() int { return r.cap }

// Subscribe registers a live fan-out channel (buffer 256). Caller must Unsubscribe.
func (r *Ring) Subscribe() (id uint64, ch <-chan Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seq++
	id = r.seq
	c := make(chan Event, 256)
	r.subs[id] = c
	return id, c
}

// Unsubscribe removes a live subscriber and closes its channel.
func (r *Ring) Unsubscribe(id uint64) {
	r.mu.Lock()
	ch, ok := r.subs[id]
	if ok {
		delete(r.subs, id)
	}
	r.mu.Unlock()
	if ok {
		close(ch)
	}
}
