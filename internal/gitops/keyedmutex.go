package gitops

import "sync"

// keyedMutex serializes operations that share a string key (typically an on-disk
// path). Running two git operations against the same directory concurrently
// races .git/index.lock, packed-refs, and the object store into corruption, so
// callers take the key's lock for the duration of their git work.
//
// The per-key mutexes are kept for the process lifetime. The key space is bounded
// by the number of repositories/pools, so this does not grow without bound.
type keyedMutex struct {
	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

func newKeyedMutex() *keyedMutex {
	return &keyedMutex{locks: make(map[string]*sync.Mutex)}
}

// get returns the mutex for key, creating it on first use.
func (k *keyedMutex) get(key string) *sync.Mutex {
	k.mu.Lock()
	defer k.mu.Unlock()
	m, ok := k.locks[key]
	if !ok {
		m = &sync.Mutex{}
		k.locks[key] = m
	}
	return m
}

// Lock blocks until the key's lock is acquired and returns its unlock function.
func (k *keyedMutex) Lock(key string) func() {
	m := k.get(key)
	m.Lock()
	return m.Unlock
}

// TryLock attempts to acquire the key's lock without blocking. It returns the
// unlock function and true on success, or nil and false if the key is already
// held.
func (k *keyedMutex) TryLock(key string) (func(), bool) {
	m := k.get(key)
	if m.TryLock() {
		return m.Unlock, true
	}
	return nil, false
}
