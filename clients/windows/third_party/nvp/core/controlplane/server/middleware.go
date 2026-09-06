package server

import (
	"net"
	"net/http"
	"sync"
	"time"
)

type ipLimiter struct {
	tokens float64
	last   time.Time
}

// RateLimitMiddleware limits requests per client IP.
func RateLimitMiddleware(cfg RateLimitConfig) func(http.Handler) http.Handler {
	if cfg.RequestsPerMinute <= 0 {
		cfg = DefaultRateLimit()
	}
	if cfg.Burst <= 0 {
		cfg.Burst = 30
	}
	rate := float64(cfg.RequestsPerMinute) / 60.0
	var mu sync.Mutex
	limits := make(map[string]*ipLimiter)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip, _, _ := net.SplitHostPort(r.RemoteAddr)
			if ip == "" {
				ip = r.RemoteAddr
			}
			now := time.Now()
			mu.Lock()
			lim, ok := limits[ip]
			if !ok {
				lim = &ipLimiter{tokens: float64(cfg.Burst), last: now}
				limits[ip] = lim
			}
			elapsed := now.Sub(lim.last).Seconds()
			lim.tokens += elapsed * rate
			if lim.tokens > float64(cfg.Burst) {
				lim.tokens = float64(cfg.Burst)
			}
			lim.last = now
			if now.Unix()%64 == 0 {
				for k, v := range limits {
					if now.Sub(v.last) > 2*time.Minute {
						delete(limits, k)
					}
				}
			}
			if lim.tokens < 1 {
				mu.Unlock()
				http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
				return
			}
			lim.tokens--
			mu.Unlock()
			next.ServeHTTP(w, r)
		})
	}
}

// SecurityHeadersMiddleware adds baseline security headers.
func SecurityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

func wrapHandler(h http.Handler, rateLimit RateLimitConfig) http.Handler {
	h = SecurityHeadersMiddleware(h)
	if rateLimit.RequestsPerMinute > 0 {
		h = RateLimitMiddleware(rateLimit)(h)
	}
	return h
}
