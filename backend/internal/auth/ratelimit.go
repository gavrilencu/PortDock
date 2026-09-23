package auth

import (
	"sync"
	"time"
)

// Limiter throttles repeated failures per key (e.g. "ip:<addr>", "email:<addr>").
// After Max failures within Window the key is locked until the window expires.
type Limiter struct {
	Max    int
	Window time.Duration

	mu   sync.Mutex
	hits map[string]*bucket
}

type bucket struct {
	n     int
	start time.Time
}

func NewLimiter(max int, window time.Duration) *Limiter {
	return &Limiter{Max: max, Window: window, hits: map[string]*bucket{}}
}

// Blocked reports whether key is locked and for how long.
func (l *Limiter) Blocked(key string) (time.Duration, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	b := l.hits[key]
	if b == nil {
		return 0, false
	}
	left := l.Window - time.Since(b.start)
	if left <= 0 {
		delete(l.hits, key)
		return 0, false
	}
	return left, b.n >= l.Max
}

// Fail records a failure for key.
func (l *Limiter) Fail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	b := l.hits[key]
	if b == nil || time.Since(b.start) > l.Window {
		b = &bucket{start: time.Now()}
		l.hits[key] = b
	}
	b.n++
	if len(l.hits) > 50_000 { // bound memory under a password-spraying attack
		for k, v := range l.hits {
			if time.Since(v.start) > l.Window {
				delete(l.hits, k)
			}
		}
	}
}

// Reset clears key after a success.
func (l *Limiter) Reset(key string) {
	l.mu.Lock()
	delete(l.hits, key)
	l.mu.Unlock()
}
