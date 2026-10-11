package conversation

import (
	"sync"
	"time"

	"github.com/wake/purdex/internal/promptq"
)

const (
	maxKeysLedger = 4096
	// keysTombstoneTTL: how long a blind-typed id is remembered (the same as promptq's tombstones).
	keysTombstoneTTL = promptq.TombstoneTTL
)

// keysLedger remembers the ids typed blind (no mod, so no promptq row): a repeat never types again. It is in memory only: a
// daemon restart forgets it. Rows are kept 24 h; when full the OLDEST by time goes first. The caller passes the tuned clock.
type keysLedger struct {
	mu sync.Mutex
	m  map[string]keysRow
}

type keysRow struct {
	handed bool   // false: the text may have landed (unknown)
	sid    string // the session that claimed the id
	at     time.Time
	// inflight: claimed and still being typed. Never evicted: dropping it would let a replay claim the id again and type twice.
	inflight bool
}

func (k *keysLedger) get(id string, now time.Time) (keysRow, bool) {
	k.mu.Lock()
	defer k.mu.Unlock()
	r, ok := k.m[id]
	if ok && now.Sub(r.at) > keysTombstoneTTL {
		delete(k.m, id)
		return keysRow{}, false
	}
	return r, ok
}

func (k *keysLedger) put(id string, r keysRow, now time.Time) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.putLocked(id, r, now)
}

// putLocked stores the row; false when the ledger is full of in-flight claims and the (new) id cannot be stored.
func (k *keysLedger) putLocked(id string, r keysRow, now time.Time) bool {
	if k.m == nil {
		k.m = map[string]keysRow{}
	}
	for key, old := range k.m {
		if !old.inflight && now.Sub(old.at) > keysTombstoneTTL {
			delete(k.m, key)
		}
	}
	if _, exists := k.m[id]; !exists {
		for len(k.m) >= maxKeysLedger {
			var oldest string
			var at time.Time
			for key, old := range k.m {
				if old.inflight { // only settled rows are candidates
					continue
				}
				if oldest == "" || old.at.Before(at) {
					oldest, at = key, old.at
				}
			}
			if oldest == "" {
				return false
			}
			delete(k.m, oldest)
		}
	}
	r.at = now
	k.m[id] = r
	return true
}

// settle turns an in-flight claim into an ordinary row (whatever it holds: handed, or unknown).
func (k *keysLedger) settle(id string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if r, ok := k.m[id]; ok && r.inflight {
		r.inflight = false
		k.m[id] = r
	}
}

func (k *keysLedger) forget(id string) {
	k.mu.Lock()
	delete(k.m, id)
	k.mu.Unlock()
}

// claim is the atomic check-and-insert before typing: the first caller records the id (unknown until it is handed) under the
// ledger mutex and gets claimed=true; any other caller gets the row that is there and must not type.
//
// The claim is in flight until settle: it is never evicted. full is true (claimed=false, nothing recorded) when every row is in
// flight and the ledger has no room.
func (k *keysLedger) claim(id, sid string, now time.Time) (row keysRow, claimed, full bool) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if r, ok := k.m[id]; ok && (r.inflight || now.Sub(r.at) <= keysTombstoneTTL) {
		return r, false, false
	}
	if !k.putLocked(id, keysRow{sid: sid, inflight: true}, now) {
		return keysRow{}, false, true
	}
	return keysRow{sid: sid}, true, false
}
