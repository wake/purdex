package conversation

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wake/purdex/internal/convfeed"
)

// fakeAborts is the agent module's AbortedAt: when the mod last said it interrupted the session's main turn.
type fakeAborts struct {
	mu sync.Mutex
	at time.Time
}

func (f *fakeAborts) AbortedAt(string) (time.Time, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.at, !f.at.IsZero()
}

func (f *fakeAborts) set(at time.Time) {
	f.mu.Lock()
	f.at = at
	f.mu.Unlock()
}

// The mod's own interrupt writes no marker into the transcript: the running turn ends as interrupted when the daemon
// is told, and the stream says so without the file growing.
func TestWS_AModInterruptEndsTheRunningTurnAsInterrupted(t *testing.T) {
	e, _ := wsEnv(t)
	p := e.transcript(idleTurns(1))
	e.owners.own = []convfeed.Owner{{TranscriptPath: p, Status: "running", SeenAt: 1, FrameID: "f1"}}
	aborts := &fakeAborts{}
	e.mod.aborts = aborts
	srv := e.server()
	c := e.connect(srv, "")
	snap := c.expect("conversation.snapshot")
	if !strings.Contains(string(snap.Value), `"outcome":"running"`) {
		t.Fatalf("the live last turn should be running: %s", snap.Value)
	}
	c.expect("approvals.snapshot")
	aborts.set(time.Now())
	f := c.expect("conversation.changes")
	if !strings.Contains(string(f.Value), `"outcome":"interrupted"`) {
		t.Fatalf("changes frame does not say interrupted: %s", f.Value)
	}
}

func TestSnapshot_AModInterruptEndsTheRunningTurn(t *testing.T) {
	e := newEnv(t)
	p := e.transcript(idleTurns(1))
	e.owners.own = []convfeed.Owner{{TranscriptPath: p, Status: "running", SeenAt: 1, FrameID: "f1"}}
	aborts := &fakeAborts{}
	e.mod.aborts = aborts
	if body := e.get("/api/conversations/claude/" + sid).Body.String(); !strings.Contains(body, `"outcome":"running"`) {
		t.Fatalf("before: %s", body)
	}
	aborts.set(time.Now())
	if body := e.get("/api/conversations/claude/" + sid).Body.String(); !strings.Contains(body, `"outcome":"interrupted"`) {
		t.Errorf("after the mod's interrupt: %s", body)
	}
}

func TestSnapshot_AnAbortOlderThanTheTurnLeavesItRunning(t *testing.T) {
	e := newEnv(t)
	p := e.transcript(idleTurns(1))
	e.owners.own = []convfeed.Owner{{TranscriptPath: p, Status: "running", SeenAt: 1, FrameID: "f1"}}
	aborts := &fakeAborts{}
	aborts.set(time.Unix(1, 0)) // long before the turn began
	e.mod.aborts = aborts
	if body := e.get("/api/conversations/claude/" + sid).Body.String(); !strings.Contains(body, `"outcome":"running"`) {
		t.Errorf("an abort from before the turn ended it: %s", body)
	}
}
