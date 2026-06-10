package server

import (
	"net"
	"net/http"
	"sync"
	"time"
)

// rateLimiter is a small per-key token-bucket limiter used to blunt brute-force
// attacks against authentication. It is intentionally simple and in-process;
// distributed deployments can swap in a shared limiter later.
type rateLimiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket
}

type bucket struct {
	tokens   float64
	lastSeen time.Time
}

const (
	// authRate is the sustained allowance: tokens refilled per second.
	authRate = 0.2 // one attempt every 5s sustained
	// authBurst is the maximum burst of attempts allowed before throttling.
	authBurst = 5
)

func newRateLimiter() *rateLimiter {
	rl := &rateLimiter{buckets: make(map[string]*bucket)}
	go rl.gc()
	return rl
}

// allow reports whether an action keyed by key may proceed, consuming a token.
func (rl *rateLimiter) allow(key string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	b, ok := rl.buckets[key]
	if !ok {
		rl.buckets[key] = &bucket{tokens: authBurst - 1, lastSeen: now}
		return true
	}

	elapsed := now.Sub(b.lastSeen).Seconds()
	b.tokens = min(authBurst, b.tokens+elapsed*authRate)
	b.lastSeen = now

	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// gc periodically evicts idle buckets to bound memory.
func (rl *rateLimiter) gc() {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		rl.mu.Lock()
		cutoff := time.Now().Add(-30 * time.Minute)
		for k, b := range rl.buckets {
			if b.lastSeen.Before(cutoff) {
				delete(rl.buckets, k)
			}
		}
		rl.mu.Unlock()
	}
}

// clientIP extracts the best-effort client address. Forwarded headers are only
// honored when the operator has explicitly declared the proxy trusted, since
// those headers are trivially spoofable when exposed directly.
func clientIP(r *http.Request, trustProxy bool) string {
	if trustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			if i := indexByte(xff, ','); i >= 0 {
				return trimSpace(xff[:i])
			}
			return trimSpace(xff)
		}
		if xrip := r.Header.Get("X-Real-Ip"); xrip != "" {
			return trimSpace(xrip)
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// small local helpers to avoid pulling in strings for two operations.
func indexByte(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}

func trimSpace(s string) string {
	start := 0
	for start < len(s) && (s[start] == ' ' || s[start] == '\t') {
		start++
	}
	end := len(s)
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t') {
		end--
	}
	return s[start:end]
}
