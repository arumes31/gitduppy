package gitops

import "sync"

// keyedMutex serializes operations that share a string key (typically an on-disk
// path). Running two git operations against the same directory concurrently
// races .git/index.lock, packed-refs, and the object store into corruption, so
// callers take the key's lock for the duration of their git work.
//
// Per-key entries are reference counted: an entry is created on first use and
// evicted once the last holder (or waiter) releases it, so the map does not grow
// without bound as new repository/pool keys appear over the process lifetime.
type keyedMutex struct {
	mu    sync.Mutex
	locks map[string]*refCountedMutex
}

// refCountedMutex is a mutex plus a count of how many callers currently hold or
// are waiting on it. refs is guarded by the parent keyedMutex.mu, not by mu.
type refCountedMutex struct {
	mu   sync.Mutex
	refs int
}

func newKeyedMutex() *keyedMutex {
	return &keyedMutex{locks: make(map[string]*refCountedMutex)}
}

// acquire returns the entry for key, creating it on first use, and records that
// the caller holds a reference. The reference is taken before the caller blocks
// on the returned mutex so the entry cannot be evicted while a waiter exists.
func (k *keyedMutex) acquire(key string) *refCountedMutex {
	k.mu.Lock()
	defer k.mu.Unlock()
	m, ok := k.locks[key]
	if !ok {
		m = &refCountedMutex{}
		k.locks[key] = m
	}
	m.refs++
	return m
}

// release drops one reference for key and evicts the entry once no caller holds
// or waits on it.
func (k *keyedMutex) release(key string, m *refCountedMutex) {
	k.mu.Lock()
	defer k.mu.Unlock()
	m.refs--
	if m.refs <= 0 {
		delete(k.locks, key)
	}
}

// Lock blocks until the key's lock is acquired and returns its unlock function.
// The unlock function releases the lock and the caller's reference exactly once.
func (k *keyedMutex) Lock(key string) func() {
	m := k.acquire(key)
	m.mu.Lock()
	return func() {
		m.mu.Unlock()
		k.release(key, m)
	}
}

// TryLock attempts to acquire the key's lock without blocking. It returns the
// unlock function and true on success, or nil and false if the key is already
// held (dropping the reference it briefly took so a contended attempt never
// leaks an entry).
func (k *keyedMutex) TryLock(key string) (func(), bool) {
	m := k.acquire(key)
	if m.mu.TryLock() {
		return func() {
			m.mu.Unlock()
			k.release(key, m)
		}, true
	}
	k.release(key, m)
	return nil, false
}
