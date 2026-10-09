package push

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wake/purdex/internal/module/session"
	"github.com/wake/purdex/internal/team"
)

// readerCalls counts the session reads and optionally hangs or fails them.
type readerCalls struct {
	mu   sync.Mutex
	n    int
	refs map[string]session.SessionRef
	err  error
	hang bool // never returns, and ignores its context
}

func (r *readerCalls) read(ctx context.Context) (map[string]session.SessionRef, error) {
	r.mu.Lock()
	r.n++
	hang, refs, err := r.hang, r.refs, r.err
	r.mu.Unlock()
	if hang {
		select {} // an uncooperative reader: the snapshot must not wait for it
	}
	return refs, err
}

func (r *readerCalls) count() int { r.mu.Lock(); defer r.mu.Unlock(); return r.n }

func sessionCodeOf(t *testing.T, payload string) (string, bool) {
	t.Helper()
	var pl struct {
		Purdex map[string]any `json:"purdex"`
	}
	_ = json.Unmarshal([]byte(payload), &pl)
	v, ok := pl.Purdex["session_code"].(string)
	return v, ok
}

func TestSnapshot_HungSessionReadStillSendsWithinBudgetAndFallsBack(t *testing.T) {
	te := newTriggerEnv(t, nil)
	rc := &readerCalls{hang: true}
	te.mod.sessions = rc.read
	te.mod.sessBudget = 50 * time.Millisecond
	te.register(tokA, "en", "mlab")
	te.register(tokB, "en", "mlab")
	te.events.readOpen = func() ([]team.Approval, error) {
		return []team.Approval{leadApproval("ap1"), leadApproval("ap2")}, nil
	}
	start := time.Now()
	te.events.emit("opened", leadApproval("ap1"))
	calls := te.waitSends(t, 2)
	if d := time.Since(start); d > time.Second {
		t.Fatalf("took %v", d)
	}
	for _, c := range calls {
		keys, ok := keysOf(t, c.Payload)
		if !ok || strings.Join(keys, ",") != "a:ap1,a:ap2" {
			t.Fatalf("keys = %v: %s", keys, c.Payload)
		}
		if code, has := sessionCodeOf(t, c.Payload); has {
			t.Fatalf("session_code %q on a failed read: %s", code, c.Payload)
		}
	}
	if rc.count() != 1 {
		t.Fatalf("reads = %d, want 1 for a Job with 2 devices and 2 approvals", rc.count())
	}
}

func TestSnapshot_FailingReadFallsBackToApprovalIDs(t *testing.T) {
	te := newTriggerEnv(t, nil)
	rc := &readerCalls{err: errors.New("tmux gone")}
	te.mod.sessions = rc.read
	te.register(tokA, "en", "mlab")
	te.events.readOpen = func() ([]team.Approval, error) { return []team.Approval{leadApproval("ap1")}, nil }
	te.events.emit("opened", leadApproval("ap1"))
	p := te.waitSends(t, 1)[0].Payload
	if keys, _ := keysOf(t, p); strings.Join(keys, ",") != "a:ap1" {
		t.Fatalf("keys = %v", keys)
	}
}

func TestSnapshot_SharedAcrossDevices(t *testing.T) {
	te := newTriggerEnv(t, nil)
	rc := &readerCalls{refs: map[string]session.SessionRef{"dev": {Code: "c0de01", Created: sessionAt}}}
	te.mod.sessions = rc.read
	te.register(tokA, "en", "mlab")
	te.register(tokB, "zh-TW", "mlab")
	te.events.readOpen = func() ([]team.Approval, error) {
		return []team.Approval{leadApproval("ap1"), leadApproval("ap2"), leadApproval("ap3")}, nil
	}
	te.events.emit("opened", leadApproval("ap1"))
	for _, c := range te.waitSends(t, 2) {
		if code, ok := sessionCodeOf(t, c.Payload); !ok || code != "c0de01" {
			t.Fatalf("session_code = %q: %s", code, c.Payload)
		}
	}
	if rc.count() != 1 {
		t.Fatalf("reads = %d, want 1 per Job (not per device or per approval)", rc.count())
	}
}

// The approval callback runs on the feed's goroutine and must only enqueue: it never reads the sessions itself. With no
// open-approvals reader the sender's snapshot has nothing to resolve either, so any read here would be the callback's.
func TestCallback_NeverReadsSessions(t *testing.T) {
	te := newTriggerEnv(t, nil)
	rc := &readerCalls{refs: map[string]session.SessionRef{"dev": {Code: "c0de01", Created: sessionAt}}}
	te.mod.sessions = rc.read
	te.mod.reader = nil
	te.register(tokA, "en", "mlab")
	te.events.emit("opened", leadApproval("ap1"))
	te.waitSends(t, 1)
	if rc.count() != 0 {
		t.Fatalf("sessions read %d times with no open set to resolve", rc.count())
	}
}

func TestIdentity_SessionRecreatedAfterTheApprovalIsNotItsSession(t *testing.T) {
	cases := []struct {
		name    string
		created int64
		want    string
		code    bool
	}{
		{"created before", sessionAt, "s:c0de01", true},
		{"within skew after", approvalAtMs/1000 + 1, "s:c0de01", true},
		{"recreated after", approvalAtMs/1000 + 60, "a:ap1", false},
		{"unknown creation time", 0, "a:ap1", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			te := newTriggerEnv(t, nil)
			rc := &readerCalls{refs: map[string]session.SessionRef{"dev": {Code: "c0de01", Created: c.created}}}
			te.mod.sessions = rc.read
			te.register(tokA, "en", "mlab")
			te.events.readOpen = func() ([]team.Approval, error) { return []team.Approval{leadApproval("ap1")}, nil }
			te.events.emit("opened", leadApproval("ap1"))
			p := te.waitSends(t, 1)[0].Payload
			if keys, _ := keysOf(t, p); strings.Join(keys, ",") != c.want {
				t.Fatalf("keys = %v, want %s", keys, c.want)
			}
			if _, has := sessionCodeOf(t, p); has != c.code {
				t.Fatalf("session_code present = %v, want %v: %s", has, c.code, p)
			}
		})
	}
}
