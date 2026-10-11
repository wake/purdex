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

func (k *keysLedger) putLocked(id string, r keysRow, now time.Time) {
	if k.m == nil {
		k.m = map[string]keysRow{}
	}
	for key, old := range k.m {
		if now.Sub(old.at) > keysTombstoneTTL {
			delete(k.m, key)
		}
	}
	if _, exists := k.m[id]; !exists {
		for len(k.m) >= maxKeysLedger {
			var oldest string
			var at time.Time
			for key, old := range k.m {
				if oldest == "" || old.at.Before(at) {
					oldest, at = key, old.at
				}
			}
			delete(k.m, oldest)
		}
	}
	r.at = now
	k.m[id] = r
}

func (k *keysLedger) forget(id string) {
	k.mu.Lock()
	delete(k.m, id)
	k.mu.Unlock()
}

// claim is the atomic check-and-insert before typing: the first caller records the id (unknown until it is handed) under the
// ledger mutex and gets claimed=true; any other caller gets the row that is there and must not type.
func (k *keysLedger) claim(id, sid string, now time.Time) (row keysRow, claimed bool) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if r, ok := k.m[id]; ok && now.Sub(r.at) <= keysTombstoneTTL {
		return r, false
	}
	k.putLocked(id, keysRow{sid: sid}, now)
	return keysRow{sid: sid}, true
}
