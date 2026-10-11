package conversation

import (
	"encoding/json"
	"sync"
	"testing"

	"github.com/wake/purdex/internal/convfeed"
	"github.com/wake/purdex/internal/module/agent"
)

// c9d9c7-67 PR2 (#2592): header.background — the background work the session's last Stop listed, by kind — only while the conversation is live and
// something runs; the agent module is asked after every refresh.

type fakeBackground struct {
	mu sync.Mutex
	w  agent.HookBackground
	ok bool
}

func (f *fakeBackground) set(w agent.HookBackground, ok bool) {
	f.mu.Lock()
	f.w, f.ok = w, ok
	f.mu.Unlock()
}
func (f *fakeBackground) BackgroundWork(string) (agent.HookBackground, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.w, f.ok
}

type bgHeader struct {
	Header struct {
		Background map[string]int `json:"background"`
	} `json:"header"`
}

func bgOf(t *testing.T, e *env) map[string]int {
	t.Helper()
	var h bgHeader
	if err := json.Unmarshal(e.get("/api/conversations/claude/"+sid).Body.Bytes(), &h); err != nil {
		t.Fatal(err)
	}
	return h.Header.Background
}

func TestSnapshot_HeaderCarriesBackgroundWhileLive(t *testing.T) {
	e := newEnv(t)
	bg := &fakeBackground{}
	e.mod.bg = bg
	p := e.transcript(idleTurns(1))
	e.owners.own = []convfeed.Owner{{TranscriptPath: p, Status: "idle", SeenAt: 1}}

	if got := bgOf(t, e); got != nil {
		t.Fatalf("nothing running must omit the field, got %v", got)
	}
	bg.set(agent.HookBackground{Shell: 1, Subagent: 2}, true)
	got := bgOf(t, e)
	if got["shell"] != 1 || got["subagent"] != 2 || len(got) != 2 {
		t.Fatalf("background = %v, want shell 1 and subagent 2 (a zero kind is omitted)", got)
	}
	bg.set(agent.HookBackground{Other: 3}, true)
	if got := bgOf(t, e); got["other"] != 3 || len(got) != 1 {
		t.Fatalf("background = %v, want only other 3", got)
	}
	// the work ended (the next Stop listed none)
	bg.set(agent.HookBackground{}, false)
	if got := bgOf(t, e); got != nil {
		t.Fatalf("after the work ended the field must go, got %v", got)
	}
}

func TestSnapshot_BackgroundIsOmittedWhenTheSessionIsNotLive(t *testing.T) {
	e := newEnv(t)
	bg := &fakeBackground{}
	bg.set(agent.HookBackground{Shell: 1}, true)
	e.mod.bg = bg
	p := e.transcript(idleTurns(1))
	e.owners.own = []convfeed.Owner{{TranscriptPath: p, Status: "idle", SeenAt: 1}}
	if got := bgOf(t, e); got["shell"] != 1 {
		t.Fatalf("live: %v", got)
	}
	e.owners.own = nil // the pane is gone: ended, not live
	if got := bgOf(t, e); got != nil {
		t.Fatalf("an ended session must not carry background work, got %v", got)
	}
}

func TestSnapshot_NoBackgroundSourceNoField(t *testing.T) {
	e := newEnv(t) // e.mod.bg stays nil
	p := e.transcript(idleTurns(1))
	e.owners.own = []convfeed.Owner{{TranscriptPath: p, Status: "idle", SeenAt: 1}}
	if got := bgOf(t, e); got != nil {
		t.Fatalf("without a source the header carries none, got %v", got)
	}
}

// A background-only change is a conversation.header frame, like a title change.
func TestWS_BackgroundOnlyChangeIsAHeaderFrame(t *testing.T) {
	e, _ := wsEnv(t)
	bg := &fakeBackground{}
	e.mod.bg = bg
	p := e.transcript(idleTurns(1))
	e.owners.own = []convfeed.Owner{{TranscriptPath: p, Status: "running", SeenAt: 1}}
	srv := e.server()
	c := e.connect(srv, "")
	c.expect("conversation.snapshot")
	c.expect("approvals.snapshot")
	bg.set(agent.HookBackground{Shell: 1}, true)
	h := c.expect("conversation.header")
	var hv bgHeader
	if err := json.Unmarshal(h.Value, &hv); err != nil || hv.Header.Background["shell"] != 1 {
		t.Fatalf("header frame %v: %s", err, h.Value)
	}
	bg.set(agent.HookBackground{}, false)
	h = c.expect("conversation.header")
	hv.Header.Background = nil
	if err := json.Unmarshal(h.Value, &hv); err != nil || len(hv.Header.Background) != 0 {
		t.Fatalf("after the work ended %v: %s", err, h.Value)
	}
}
