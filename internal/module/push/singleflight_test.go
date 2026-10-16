package push

import (
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wake/purdex/internal/module/session"
	"github.com/wake/purdex/internal/team"
)

// A reader that ignores its context must not pile up: while one read is outstanding no other starts, every Job still goes out
// within the budget with approval-id keys and no session_code, and once it returns the next Job reads again.
func TestSnapshot_AtMostOneOutstandingSessionRead(t *testing.T) {
	te := newTriggerEnv(t, nil)
	rc := &readerCalls{release: make(chan struct{}), refs: map[string]session.SessionRef{"dev": {Code: "c0de01", Created: sessionAt}}}
	t.Cleanup(rc.unblock)
	te.mod.sessions = rc.read
	te.mod.sessBudget = 30 * time.Millisecond
	te.register(tokA, "en", "mlab")
	var cur atomic.Value // the card being pushed now: a card is pushed once, so every job is about a new one
	te.events.readOpen = func() ([]team.Approval, error) { return []team.Approval{anonLead(cur.Load().(string))}, nil }
	for i := 1; i <= 4; i++ {
		start := time.Now()
		id := fmt.Sprintf("ap%d", i)
		cur.Store(id)
		te.events.emit("opened", anonLead(id))
		calls := te.waitSends(t, i)
		if d := time.Since(start); d > time.Second {
			t.Fatalf("job %d took %v", i, d)
		}
		p := calls[i-1].Payload
		if keys, _ := keysOf(t, p); strings.Join(keys, ",") != "a:"+id {
			t.Fatalf("job %d keys = %v", i, keys)
		}
		if _, has := sessionCodeOf(t, p); has {
			t.Fatalf("job %d carries a session_code: %s", i, p)
		}
	}
	if rc.count() != 1 {
		t.Fatalf("reader invoked %d times while the first read was stuck, want 1", rc.count())
	}
	rc.unblock()
	deadline := time.Now().Add(2 * time.Second)
	for sent := 4; rc.count() < 2; {
		if time.Now().After(deadline) {
			t.Fatalf("no new read after the stuck one returned (reads = %d)", rc.count())
		}
		sent++
		id := fmt.Sprintf("ap%d", sent)
		cur.Store(id)
		te.events.emit("opened", anonLead(id))
		te.waitSends(t, sent)
	}
}
