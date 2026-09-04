package transport

import (
	"context"
	"fmt"
	"sync"
)

// Registry holds available transports by profile.
type Registry struct {
	mu         sync.RWMutex
	transports map[Profile]Transport
}

// NewRegistry creates an empty transport registry.
func NewRegistry() *Registry {
	return &Registry{
		transports: make(map[Profile]Transport),
	}
}

// Register adds a transport implementation.
func (r *Registry) Register(t Transport) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.transports[t.Profile()] = t
}

// Get returns transport for profile.
func (r *Registry) Get(profile Profile) (Transport, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.transports[profile]
	return t, ok
}

// DialWithRacing attempts primary transport then falls back after delay.
func (r *Registry) DialWithRacing(ctx context.Context, cfg DialConfig, racing RacingConfig) (Conn, error) {
	type result struct {
		conn Conn
		err  error
	}

	results := make(chan result, 2)
	var wg sync.WaitGroup

	dial := func(profile Profile) {
		defer wg.Done()
		t, ok := r.Get(profile)
		if !ok {
			results <- result{err: fmt.Errorf("transport not registered: %s", profile)}
			return
		}
		dcfg := cfg
		dcfg.Profile = profile
		conn, err := t.Dial(ctx, dcfg)
		results <- result{conn: conn, err: err}
	}

	wg.Add(1)
	go dial(racing.Primary)

	fallbackCtx, cancel := context.WithTimeout(ctx, racing.FallbackDelay)
	defer cancel()

	select {
	case res := <-results:
		if res.err == nil {
			return res.conn, nil
		}
	case <-fallbackCtx.Done():
	}

	wg.Add(1)
	go dial(racing.Fallback)

	wg.Wait()
	close(results)

	var lastErr error
	for res := range results {
		if res.err == nil {
			return res.conn, nil
		}
		lastErr = res.err
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("all transports failed")
}
