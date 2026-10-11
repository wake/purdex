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
//
// Invariant: a row that is in flight is touched only by the claim that created it (matched by its gen token). Expiry, capacity
// eviction, put, and another claim's settle/forget all skip it. Every mutator below honours this.
type keysLedger struct {
	mu      sync.Mutex
	m       map[string]keysRow
	nextGen uint64
}

type keysRow struct {
	handed bool   // false: the text may have landed (unknown)
	sid    string // the session that claimed the id
	at     time.Time
	// inflight: claimed and still being typed. Never evicted: dropping it would let a replay claim the id again and type twice.
	inflight bool
	// gen identifies the claim that created the row; settle/forget/hand act only when it still matches.
	gen uint64
}

func (k *keysLedger) get(id string, now time.Time) (keysRow, bool) {
	k.mu.Lock()
	defer k.mu.Unlock()
	r, ok := k.m[id]
	if ok && !r.inflight && now.Sub(r.at) > keysTombstoneTTL { // an in-flight row never expires
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
	if old, exists := k.m[id]; exists {
		if old.inflight { // never overwrite another claim's in-flight row
			return false
		}
	} else {
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

// settle turns the in-flight claim carrying gen into an ordinary row (whatever it holds: handed, or unknown). A row that is
// gone or belongs to a newer claim is left alone.
func (k *keysLedger) settle(id string, gen uint64) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if r, ok := k.m[id]; ok && r.inflight && r.gen == gen {
		r.inflight = false
		k.m[id] = r
	}
}

// forget drops the row only while it is still the claim carrying gen (a clean refusal: nothing was typed).
func (k *keysLedger) forget(id string, gen uint64) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if r, ok := k.m[id]; ok && r.gen == gen {
		delete(k.m, id)
	}
}

// hand records that the claim carrying gen was handed; it stays in flight until settle.
func (k *keysLedger) hand(id string, gen uint64) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if r, ok := k.m[id]; ok && r.inflight && r.gen == gen {
		r.handed = true
		k.m[id] = r
	}
}

// claim is the atomic check-and-insert before typing: the first caller records the id (unknown until it is handed) under the
// ledger mutex and gets claimed=true; any other caller gets the row that is there and must not type.
//
// The claim is in flight until settle: it is never evicted. full is true (claimed=false, nothing recorded) when every row is in
// flight and the ledger has no room. On claimed=true, row.gen is the token for settle/forget/hand. On claimed=true, row.gen is the token for settle/forget/hand.
func (k *keysLedger) claim(id, sid string, now time.Time) (row keysRow, claimed, full bool) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if r, ok := k.m[id]; ok && (r.inflight || now.Sub(r.at) <= keysTombstoneTTL) {
		return r, false, false
	}
	k.nextGen++
	gen := k.nextGen
	if !k.putLocked(id, keysRow{sid: sid, inflight: true, gen: gen}, now) {
		return keysRow{}, false, true
	}
	return keysRow{sid: sid, gen: gen}, true, false
}
