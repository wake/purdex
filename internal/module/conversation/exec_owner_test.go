package conversation

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/wake/purdex/internal/convfeed"
)

type fakeExecOwners struct {
	mu    sync.Mutex
	owner convfeed.ExecOwner
	ok    bool
	err   error
}

func (f *fakeExecOwners) LiveWorker(context.Context, string) (convfeed.ExecOwner, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.owner, f.ok, f.err
}

func (f *fakeExecOwners) setLight(l string) {
	f.mu.Lock()
	f.owner.Light = l
	f.mu.Unlock()
}

// execEnv registers a fake nex.conversation-owner and wires the resolver the way Init does.
func execEnv(t *testing.T) (*env, *fakeExecOwners) {
	t.Helper()
	e, _ := wsEnv(t)
	x := &fakeExecOwners{}
	e.mod.core.Registry.Register(convfeed.ExecOwnerKey, x)
	e.mod.resolver.ExecOwners = execAdapter{e.mod}
	return e, x
}

func TestExec_SnapshotReportsBackendStatusLive(t *testing.T) {
	e, x := execEnv(t)
	p := e.transcript(idleTurns(1))
	x.owner, x.ok = convfeed.ExecOwner{ExecID: "E1", Light: "waiting", TranscriptPath: p, Live: true}, true
	s := decode(t, e.get("/api/conversations/claude/"+sid))
	if s.Header.Backend != "execution" || s.Header.Status != "waiting" || !s.Header.Live {
		t.Fatalf("header = %+v", s.Header)
	}
}

func TestExec_TerminalOwnerFirst(t *testing.T) {
	e, x := execEnv(t)
	p := e.transcript(idleTurns(1))
	x.owner, x.ok = convfeed.ExecOwner{ExecID: "E1", Light: "waiting", Live: true}, true
	e.owners.own = []convfeed.Owner{{TranscriptPath: p, Status: "idle", SeenAt: 1, FrameID: "f1"}}
	s := decode(t, e.get("/api/conversations/claude/"+sid))
	if s.Header.Backend != "terminal" || s.Header.Status != "idle" {
		t.Fatalf("header = %+v", s.Header)
	}
}

func TestExec_TerminalErrorNotOverridden(t *testing.T) {
	e, x := execEnv(t)
	e.transcript(idleTurns(1))
	x.owner, x.ok = convfeed.ExecOwner{ExecID: "E1", Light: "running", Live: true}, true
	e.owners.err = errors.New("boom")
	s := decode(t, e.get("/api/conversations/claude/"+sid))
	if s.Header.Backend != "" || s.Header.Live || s.Header.Status != "unknown" {
		t.Fatalf("header = %+v", s.Header)
	}
}

func TestExec_NexAbsentIsUnchanged(t *testing.T) {
	e, _ := wsEnv(t) // no registry entry, the adapter is wired as Init wires it
	e.mod.resolver.ExecOwners = execAdapter{e.mod}
	e.transcript(idleTurns(1))
	s := decode(t, e.get("/api/conversations/claude/"+sid))
	if s.Header.Backend != "" || s.Header.Live || s.Header.Status != "ended" {
		t.Fatalf("header = %+v", s.Header)
	}
	if c := e.mod.capabilitiesFor(sid, "", false); c.Send != "" || c.Reasons["send"] != "not_wired" {
		t.Fatalf("caps = %+v", c)
	}
}

func TestExec_CapabilitiesPerBackend(t *testing.T) {
	e, _ := execEnv(t)
	c := e.mod.capabilitiesFor(sid, "execution", true)
	if c.Send != "execution" || c.Interrupt != "execution" || c.AnswerPermission != "execution" || c.BackgroundTasks != "execution" {
		t.Fatalf("execution caps = %+v", c)
	}
	for _, k := range []string{"send", "interrupt", "answer_permission", "background_tasks"} {
		if c.Reasons[k] != "" {
			t.Errorf("reason %q left on a supported capability", k)
		}
	}
	if c.AnswerQuestion != "approval" || c.Steer != "" {
		t.Errorf("unrelated caps changed: %+v", c)
	}

	// a terminal backend keeps the prompt logic, whatever the execution source says
	f := &fakeSender{owner: true}
	e.sender(f)
	if c := e.mod.capabilitiesFor(sid, "terminal", true); c.Send != "prompt" || c.AnswerPermission != "" {
		t.Fatalf("terminal caps = %+v", c)
	}
	// no backend (ended / unknown): not execution caps either
	e2, _ := execEnv(t)
	if c := e2.mod.capabilitiesFor(sid, "", false); c.Send != "" || c.Reasons["send"] != "not_wired" {
		t.Fatalf("no owner: %+v", c)
	}
}

type capsView struct {
	Conversation struct {
		Backend      string `json:"backend"`
		Capabilities struct {
			Send             string `json:"send"`
			AnswerPermission string `json:"answer_permission"`
		} `json:"capabilities"`
	} `json:"conversation"`
}

// One owner decision: a terminal pane (no prompt owner) owns the session while a live execution also exists. The header
// says terminal and the capabilities must not say execution; and nothing flips between them while it stays that way.
func TestExec_TerminalPaneWithoutPromptOwnerAndLiveExecution(t *testing.T) {
	e, x := execEnv(t)
	p := e.transcript(idleTurns(1))
	x.owner, x.ok = convfeed.ExecOwner{ExecID: "E1", Light: "running", Live: true}, true
	e.owners.own = []convfeed.Owner{{TranscriptPath: p, Status: "idle", SeenAt: 1, FrameID: "f1"}}
	srv := e.server()
	c := e.connect(srv, "")
	snap := c.expect("conversation.snapshot")
	var v capsView
	if err := json.Unmarshal(snap.Value, &v); err != nil {
		t.Fatal(err)
	}
	if cv := v.Conversation; cv.Backend != "terminal" || cv.Capabilities.Send == "execution" || cv.Capabilities.AnswerPermission == "execution" {
		t.Fatalf("snapshot: %s", snap.Value)
	}
	c.expect("approvals.snapshot")
	x.setLight("waiting") // the execution moves; the terminal owner does not, and no frame follows
	_ = c.conn.SetReadDeadline(time.Now().Add(400 * time.Millisecond))
	if _, b, err := c.conn.ReadMessage(); err == nil {
		t.Fatalf("flip-flop: %s", b)
	}
}

// The worker's light reaches the stream within a poll, without waiting for the full re-resolve.
func TestExec_LightChangeIsAHeaderFrame(t *testing.T) {
	e, x := execEnv(t)
	p := e.transcript(idleTurns(1))
	x.owner, x.ok = convfeed.ExecOwner{ExecID: "E1", Light: "running", TranscriptPath: p, Live: true}, true
	e.mod.tweak.reresolve = time.Hour
	srv := e.server()
	c := e.connect(srv, "")
	if got := headerStatus(t, c.expect("conversation.snapshot")); got != "running" {
		t.Fatalf("snapshot status %q", got)
	}
	c.expect("approvals.snapshot")
	x.setLight("waiting")
	if got := headerStatus(t, c.expect("conversation.header")); got != "waiting" {
		t.Fatalf("header status %q", got)
	}
	x.setLight("idle")
	if got := headerStatus(t, c.expect("conversation.header")); got != "idle" {
		t.Fatalf("header status %q", got)
	}
}

// The snapshot frame of a worker-run conversation carries the execution capabilities.
func TestExec_SnapshotCapabilitiesAreExecution(t *testing.T) {
	e, x := execEnv(t)
	p := e.transcript(idleTurns(1))
	x.owner, x.ok = convfeed.ExecOwner{ExecID: "E1", Light: "idle", TranscriptPath: p, Live: true}, true
	srv := e.server()
	c := e.connect(srv, "")
	var v capsView
	if err := json.Unmarshal(c.expect("conversation.snapshot").Value, &v); err != nil || v.Conversation.Capabilities.Send != "execution" || v.Conversation.Capabilities.AnswerPermission != "execution" {
		t.Fatalf("snapshot caps %v: %+v", err, v)
	}
}

// The worker goes away: the follower drops its source and re-resolves at once (status ended), not after the long
// re-resolve interval.
func TestExec_OwnerGoneEndsTheSourceAtOnce(t *testing.T) {
	e, x := execEnv(t)
	p := e.transcript(idleTurns(1))
	x.owner, x.ok = convfeed.ExecOwner{ExecID: "E1", Light: "running", TranscriptPath: p, Live: true}, true
	e.mod.tweak.reresolve = time.Hour
	srv := e.server()
	c := e.connect(srv, "")
	if got := headerStatus(t, c.expect("conversation.snapshot")); got != "running" {
		t.Fatalf("snapshot status %q", got)
	}
	c.expect("approvals.snapshot")
	x.setLight("waiting") // the follower holds its source once this reaches the stream
	if got := headerStatus(t, c.expect("conversation.header")); got != "waiting" {
		t.Fatalf("header status %q", got)
	}
	x.mu.Lock()
	x.ok = false
	x.mu.Unlock()
	for i := 0; i < 4; i++ {
		f := c.read()
		if f.Type == "conversation.header" || f.Type == "conversation.changes" {
			if got := headerStatus(t, f); got != "ended" {
				t.Fatalf("header status %q", got)
			}
			return
		}
	}
	t.Fatal("no header frame after the worker went away")
}

// A failing lookup mid-stream keeps the source instead of dropping it.
func TestExec_LookupErrorKeepsTheSource(t *testing.T) {
	e, x := execEnv(t)
	p := e.transcript(idleTurns(1))
	x.owner, x.ok = convfeed.ExecOwner{ExecID: "E1", Light: "running", TranscriptPath: p, Live: true}, true
	e.mod.tweak.reresolve = time.Hour
	srv := e.server()
	c := e.connect(srv, "")
	c.expect("conversation.snapshot")
	c.expect("approvals.snapshot")
	x.setLight("waiting")
	if got := headerStatus(t, c.expect("conversation.header")); got != "waiting" {
		t.Fatalf("header status %q", got)
	}
	x.mu.Lock()
	x.err = errors.New("boom")
	x.mu.Unlock()
	time.Sleep(300 * time.Millisecond)
	x.mu.Lock()
	x.err = nil
	x.owner.Light = "idle"
	x.mu.Unlock()
	if got := headerStatus(t, c.expect("conversation.header")); got != "idle" {
		t.Fatalf("header status %q", got)
	}
}
