package push

import (
	"strings"
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
	te.events.readOpen = func() ([]team.Approval, error) { return []team.Approval{leadApproval("ap1")}, nil }
	for i := 1; i <= 4; i++ {
		start := time.Now()
		te.events.emit("opened", leadApproval("ap1"))
		calls := te.waitSends(t, i)
		if d := time.Since(start); d > time.Second {
			t.Fatalf("job %d took %v", i, d)
		}
		p := calls[i-1].Payload
		if keys, _ := keysOf(t, p); strings.Join(keys, ",") != "a:ap1" {
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
		te.events.emit("opened", leadApproval("ap1"))
		te.waitSends(t, sent)
	}
}
