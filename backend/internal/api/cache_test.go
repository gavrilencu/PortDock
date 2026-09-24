package api

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestTTLCache(t *testing.T) {
	c := newTTLCache()
	var calls atomic.Int32
	slow := func() (int, error) {
		calls.Add(1)
		time.Sleep(50 * time.Millisecond)
		return 42, nil
	}
	// Twenty concurrent callers share one computation.
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if v, err := cached(c, "k", time.Second, slow); err != nil || v != 42 {
				t.Errorf("got %v, %v", v, err)
			}
		}()
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("computed %d times, want 1", calls.Load())
	}
	// Within the TTL nothing is recomputed; after forget it is.
	_, _ = cached(c, "k", time.Second, slow)
	if calls.Load() != 1 {
		t.Fatalf("recomputed within ttl")
	}
	c.forget("k")
	_, _ = cached(c, "k", time.Second, slow)
	if calls.Load() != 2 {
		t.Fatalf("not recomputed after forget: %d", calls.Load())
	}
	// Errors are not cached.
	boom := errors.New("boom")
	n := 0
	fn := func() (int, error) {
		n++
		if n == 1 {
			return 0, boom
		}
		return 7, nil
	}
	if _, err := cached(c, "e", time.Second, fn); !errors.Is(err, boom) {
		t.Fatalf("first: %v", err)
	}
	if v, err := cached(c, "e", time.Second, fn); err != nil || v != 7 {
		t.Fatalf("retry after error: %v %v", v, err)
	}
	// Expiry.
	_, _ = cached(c, "x", 10*time.Millisecond, slow)
	time.Sleep(20 * time.Millisecond)
	before := calls.Load()
	_, _ = cached(c, "x", 10*time.Millisecond, slow)
	if calls.Load() != before+1 {
		t.Fatal("stale value served after the ttl")
	}
}
