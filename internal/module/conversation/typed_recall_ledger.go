package conversation

import (
	"net/http"
	"sync"
	"time"
)

// recallLedger remembers the answer of a recall that pressed Up: in memory, 24 h, bounded. The key binds the answer to the
// session code and tmux instance (see recallKey), so an id reused on another session never reads this one's private text.
type recallLedger struct {
	mu sync.Mutex
	m  map[string]recallRow
}

type recallRow struct {
	status int
	a      typedAnswer
	at     time.Time
}

const maxRecallLedger = 1024

// recallKey: session code + tmux instance + client_msg_id, NUL-joined (none of them can hold a NUL). The handler requires an
// id, so the key is never empty; the owner's session id is deliberately not part of it (a replay happens before the owner is
// resolved), the code + tmux instance already identify the tmux session generation.
func recallKey(scode, inst, id string) string {
	return scode + "\x00" + inst + "\x00" + id
}

func (l *recallLedger) replay(w http.ResponseWriter, key string, now time.Time) bool {
	l.mu.Lock()
	row, ok := l.m[key]
	if ok && now.Sub(row.at) > keysTombstoneTTL {
		delete(l.m, key)
		ok = false
	}
	l.mu.Unlock()
	if ok {
		writeTyped(w, row.status, row.a)
	}
	return ok
}

func (l *recallLedger) put(key string, status int, a typedAnswer, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.m == nil {
		l.m = map[string]recallRow{}
	}
	for len(l.m) >= maxRecallLedger {
		var oldest string
		var at time.Time
		for k, v := range l.m {
			if oldest == "" || v.at.Before(at) {
				oldest, at = k, v.at
			}
		}
		delete(l.m, oldest)
	}
	l.m[key] = recallRow{status, a, now}
}
