package gitops

import (
	"sync"
	"testing"
)

// len reports how many keys currently have a live entry. Test-only helper for
// asserting that entries are evicted after release (kept out of the production
// file so it never ships as dead code).
func (k *keyedMutex) len() int {
	k.mu.Lock()
	defer k.mu.Unlock()
	return len(k.locks)
}

// TestKeyedMutexEvictsAfterRelease locks and unlocks N distinct keys and asserts
// the internal map is empty afterwards, i.e. entries are reference counted and
// evicted rather than retained for the process lifetime.
func TestKeyedMutexEvictsAfterRelease(t *testing.T) {
	k := newKeyedMutex()
	keys := []string{"a", "b", "c", "d", "e"}

	unlocks := make([]func(), 0, len(keys))
	for _, key := range keys {
		unlocks = append(unlocks, k.Lock(key))
	}
	if got := k.len(); got != len(keys) {
		t.Fatalf("expected %d live keys while held, got %d", len(keys), got)
	}

	for _, u := range unlocks {
		u()
	}
	if got := k.len(); got != 0 {
		t.Errorf("expected map empty after release, got %d live keys", got)
	}
}

// TestKeyedMutexTryLock verifies that a successful TryLock is evicted on release
// and that a contended (failed) TryLock does not leak an entry.
func TestKeyedMutexTryLock(t *testing.T) {
	k := newKeyedMutex()

	unlock, ok := k.TryLock("x")
	if !ok {
		t.Fatal("TryLock should succeed on a free key")
	}
	if got := k.len(); got != 1 {
		t.Fatalf("expected 1 live key after TryLock, got %d", got)
	}

	// A contended TryLock must fail and must not leave a permanent entry.
	if _, ok2 := k.TryLock("x"); ok2 {
		t.Fatal("TryLock should fail on a held key")
	}
	if got := k.len(); got != 1 {
		t.Fatalf("contended TryLock leaked an entry: %d live keys", got)
	}

	unlock()
	if got := k.len(); got != 0 {
		t.Errorf("expected map empty after final release, got %d live keys", got)
	}
}

// TestKeyedMutexSerializesSameKey ensures the reference-counted rewrite still
// provides mutual exclusion for the same key under concurrency.
func TestKeyedMutexSerializesSameKey(t *testing.T) {
	k := newKeyedMutex()
	const goroutines = 50
	var wg sync.WaitGroup
	counter := 0
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			unlock := k.Lock("shared")
			// Non-atomic increment is safe only under correct mutual exclusion.
			counter++
			unlock()
		}()
	}
	wg.Wait()
	if counter != goroutines {
		t.Errorf("expected counter %d, got %d (lost updates => broken exclusion)", goroutines, counter)
	}
	if got := k.len(); got != 0 {
		t.Errorf("expected map empty after all releases, got %d live keys", got)
	}
}
