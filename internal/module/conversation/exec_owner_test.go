package conversation

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/wake/purdex/internal/convfeed"
	"github.com/wake/purdex/internal/module/nex"
)

type fakeExecOwners struct {
	mu    sync.Mutex
	owner nex.ExecOwner
	ok    bool
	err   error
}

func (f *fakeExecOwners) LiveWorker(context.Context, string) (nex.ExecOwner, bool, error) {
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
	e.mod.core.Registry.Register(nex.ConversationOwnerKey, x)
	e.mod.resolver.ExecOwners = execAdapter{e.mod}
	return e, x
}

func TestExec_SnapshotReportsBackendStatusLive(t *testing.T) {
	e, x := execEnv(t)
	p := e.transcript(idleTurns(1))
	x.owner, x.ok = nex.ExecOwner{ExecID: "E1", Light: "waiting", TranscriptPath: p, Live: true}, true
	s := decode(t, e.get("/api/conversations/claude/"+sid))
	if s.Header.Backend != "execution" || s.Header.Status != "waiting" || !s.Header.Live {
		t.Fatalf("header = %+v", s.Header)
	}
}

func TestExec_TerminalOwnerFirst(t *testing.T) {
	e, x := execEnv(t)
	p := e.transcript(idleTurns(1))
	x.owner, x.ok = nex.ExecOwner{ExecID: "E1", Light: "waiting", Live: true}, true
	e.owners.own = []convfeed.Owner{{TranscriptPath: p, Status: "idle", SeenAt: 1, FrameID: "f1"}}
	s := decode(t, e.get("/api/conversations/claude/"+sid))
	if s.Header.Backend != "terminal" || s.Header.Status != "idle" {
		t.Fatalf("header = %+v", s.Header)
	}
}

func TestExec_TerminalErrorNotOverridden(t *testing.T) {
	e, x := execEnv(t)
	e.transcript(idleTurns(1))
	x.owner, x.ok = nex.ExecOwner{ExecID: "E1", Light: "running", Live: true}, true
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
	if c := e.mod.capabilitiesFor(sid); c.Send != "" || c.Reasons["send"] != "not_wired" {
		t.Fatalf("caps = %+v", c)
	}
}

func TestExec_CapabilitiesPerBackend(t *testing.T) {
	e, x := execEnv(t)
	x.owner, x.ok = nex.ExecOwner{ExecID: "E1", Light: "idle", Live: true}, true
	c := e.mod.capabilitiesFor(sid)
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

	// a terminal prompt owner keeps `prompt` and does not consult the execution
	f := &fakeSender{owner: true}
	e.sender(f)
	if c := e.mod.capabilitiesFor(sid); c.Send != "prompt" || c.AnswerPermission != "" {
		t.Fatalf("terminal caps = %+v", c)
	}

	// no execution, or a failed lookup: fail closed
	e2, x2 := execEnv(t)
	if c := e2.mod.capabilitiesFor(sid); c.Send != "" {
		t.Fatalf("no owner: %+v", c)
	}
	x2.err = errors.New("boom")
	if c := e2.mod.capabilitiesFor(sid); c.Send != "" || c.Reasons["send"] != "not_wired" {
		t.Fatalf("lookup error: %+v", c)
	}
}

// The worker's light reaches the stream within a poll, without waiting for the full re-resolve.
func TestExec_LightChangeIsAHeaderFrame(t *testing.T) {
	e, x := execEnv(t)
	p := e.transcript(idleTurns(1))
	x.owner, x.ok = nex.ExecOwner{ExecID: "E1", Light: "running", TranscriptPath: p, Live: true}, true
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
