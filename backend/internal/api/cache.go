package api

import (
	"sync"
	"time"
)

// ttlCache keeps the result of an expensive call (a docker inspect of a
// whole server, its stats) for a short while and lets concurrent callers
// share one call instead of each hitting the server: twenty open topology
// pages then cost one round trip every few seconds, not twenty.
type ttlCache struct {
	mu      sync.Mutex
	entries map[string]*cacheEntry
}

type cacheEntry struct {
	mu  sync.Mutex // held while the value is being computed
	at  time.Time
	val any
	err error
}

func newTTLCache() *ttlCache { return &ttlCache{entries: map[string]*cacheEntry{}} }

func (c *ttlCache) entry(key string) *cacheEntry {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	if !ok {
		e = &cacheEntry{}
		c.entries[key] = e
	}
	return e
}

// cached returns the value under key computed at most once per ttl; a failed
// computation is not kept, so the next caller tries again.
func cached[T any](c *ttlCache, key string, ttl time.Duration, fn func() (T, error)) (T, error) {
	e := c.entry(key)
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.err == nil && !e.at.IsZero() && time.Since(e.at) < ttl {
		return e.val.(T), nil
	}
	v, err := fn()
	e.val, e.err, e.at = v, err, time.Now()
	if err != nil {
		e.at = time.Time{}
	}
	return v, err
}

// forget drops a cached value, after an action that changed what it describes.
func (c *ttlCache) forget(prefix string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for k := range c.entries {
		if len(k) >= len(prefix) && k[:len(prefix)] == prefix {
			delete(c.entries, k)
		}
	}
}
