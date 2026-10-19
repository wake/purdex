package teammod

import "sync"

// keyedMutex is a mutex per key, kept only while somebody holds or waits for it. The module holds one per approval id across the
// steps of a lead approval that name the lead (rename, close, undo; name pool v2 spec §5.3), so two decides, the sweeper's
// expiry close and a person's approve of one approval run one at a time. Different approvals never wait for each other.
type keyedMutex struct {
	mu sync.Mutex
	m  map[string]*keyedLock
}

type keyedLock struct {
	mu   sync.Mutex
	refs int // holders plus waiters; the entry goes at zero
}

// lock takes key's mutex and returns its unlock.
func (k *keyedMutex) lock(key string) (unlock func()) {
	k.mu.Lock()
	if k.m == nil {
		k.m = map[string]*keyedLock{}
	}
	e := k.m[key]
	if e == nil {
		e = &keyedLock{}
		k.m[key] = e
	}
	e.refs++
	k.mu.Unlock()
	e.mu.Lock()
	return func() {
		e.mu.Unlock()
		k.mu.Lock()
		if e.refs--; e.refs == 0 {
			delete(k.m, key)
		}
		k.mu.Unlock()
	}
}

// size is how many keys are held or waited for (tests).
func (k *keyedMutex) size() int {
	k.mu.Lock()
	defer k.mu.Unlock()
	return len(k.m)
}
