package conversation

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wake/purdex/internal/module/agent"
	"github.com/wake/purdex/internal/promptq"
)

// Review fixes for the typed submit route (R1 + attacker).

// Replay first: a known id is answered from the ledgers before the session or the owner is looked at.
func TestTypedSubmit_ReplayAnswersEvenWhenTheSessionVanished(t *testing.T) {
	te := newTEnv(t)
	answer(t, te.submit("rp1", "x"), 200, "handed", "fill", "")
	te.mod.td.sessions = tSessions{} // every code is unknown now
	te.mod.td.owners = tResolver{}
	te.owners.drop()
	answer(t, te.post("/api/sessions/gone/submit", body("rp1", "x", tInst)), 200, "handed", "fill", "")
	if te.keys.n() != 1 || te.fm.count(promptq.KindFill) != 1 {
		t.Fatal("a replay typed")
	}
	// the keys ledger too
	te2 := keysEnv(t)
	answer(t, te2.submit("rp2", "x"), 200, "handed", "keys", "")
	te2.mod.td.owners = tResolver{}
	answer(t, te2.post("/api/sessions/gone/submit", body("rp2", "x", tInst)), 200, "handed", "keys", "")
	if te2.keys.n() != 1 {
		t.Fatal("a keys replay typed")
	}
}

// An id the ledger evicted still answers its recorded outcome (an entered row replays as handed, not unknown).
func TestTypedSubmit_EvictedEnteredRowReplaysAsHanded(t *testing.T) {
	te := newTEnv(t)
	answer(t, te.submit("ev1", "x"), 200, "handed", "fill", "")
	te.q.Now = func() time.Time { return time.Now().Add(time.Hour) }
	_, _ = te.q.SubmitFill(context.Background(), sid, "sweeper", "y") // runs the sweep that evicts ev1
	answer(t, te.submit("ev1", "x"), 200, "handed", "fill", "")
	if te.fm.count(promptq.KindFill) != 2 || te.keys.n() != 1 { // ev1 and the sweeper's own fill; one Enter
		t.Fatalf("the replay created a job: fills=%d keys=%d", te.fm.count(promptq.KindFill), te.keys.n())
	}
}

// Locks: the pane lock (taken first) and the sid lock; either one held answers 429 busy within the bound.
func TestTypedSubmit_PaneLockIsTaken(t *testing.T) {
	te := newTEnv(t)
	release, ok := te.mod.paneLocks.acquire(context.Background(), "%5", time.Second)
	if !ok {
		t.Fatal("lock")
	}
	errBusy(t, te.submit("pl1", "x"))
	if len(te.fm.jobs) != 0 || te.keys.n() != 0 {
		t.Fatal("a busy pane must run nothing")
	}
	release()
	answer(t, te.submit("pl1", "x"), 200, "handed", "fill", "")
}

func errBusy(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	errOf(t, w, 429, "busy")
}

// After the locks, the owner is resolved again: a pane that moved while we waited is 409 owner_changed.
func TestTypedSubmit_OwnerChangedWhileWaitingForTheLocks(t *testing.T) {
	te := newTEnv(t)
	moved := tOwner
	moved.TmuxPaneID = "%9"
	te.mod.td.owners = &tFlip{first: tOwner, then: moved}
	answer(t, te.submit("oc1", "x"), 409, "refused", "", "owner_changed")
	if len(te.fm.jobs) != 0 || te.keys.n() != 0 {
		t.Fatal("nothing may be typed for a moved owner")
	}
}

// Pane identity (R1 F3): the mod's owner stream carries no frame id or pid, so when more than one live Claude Code pane has
// the session id the Enter could land in the wrong pane: refused before anything is typed.
func TestTypedSubmit_AmbiguousPaneRefused(t *testing.T) {
	te := newTEnv(t)
	other := tOwner
	other.TmuxPaneID, other.FrameID = "%7", "f2"
	te.panes.panes = []agent.PaneOwner{tOwner, other}
	answer(t, te.submit("ap1", "x"), 409, "refused", "", "ambiguous_pane")
	if len(te.fm.jobs) != 0 || te.keys.n() != 0 {
		t.Fatal("nothing may be typed when the pane is ambiguous")
	}
}

func TestTypedSubmit_ConfirmedPaneMustBeTheResolvedOwner(t *testing.T) {
	te := newTEnv(t)
	other := tOwner
	other.TmuxPaneID = "%7"
	te.panes.panes = []agent.PaneOwner{other}
	answer(t, te.submit("ap2", "x"), 409, "refused", "", "pane_mismatch")
	te.panes.panes = nil
	answer(t, te.submit("ap3", "x"), 409, "refused", "", "pane_mismatch")
	te.panes.panes, te.panes.err = nil, errors.New("boom")
	errOf(t, te.submit("ap4", "x"), 503, "owner_lookup_failed")
	if len(te.fm.jobs) != 0 || te.keys.n() != 0 {
		t.Fatal("nothing may be typed")
	}
}

func TestTypedSubmit_AmbiguousPaneAlsoRefusesKeysMode(t *testing.T) {
	te := keysEnv(t)
	other := tOwner
	other.TmuxPaneID = "%7"
	te.panes.panes = []agent.PaneOwner{tOwner, other}
	answer(t, te.submit("ap5", "x"), 409, "refused", "", "ambiguous_pane")
	if te.keys.n() != 0 {
		t.Fatal("typed blind into an ambiguous pane")
	}
}

// A trailing odd run of backslashes would make Claude Code read \+Enter as a newline: one space is appended, in both modes.
func TestTypedSubmit_TrailingBackslashGetsASpace(t *testing.T) {
	te := newTEnv(t)
	answer(t, te.submit("bs1", `path C:\`), 200, "handed", "fill", "")
	answer(t, te.submit("bs2", `two \\`), 200, "handed", "fill", "")
	if te.fm.jobs[0].Text != `path C:\ ` || te.fm.jobs[1].Text != `two \\` {
		t.Fatalf("mod got %q and %q", te.fm.jobs[0].Text, te.fm.jobs[1].Text)
	}
	te2 := keysEnv(t)
	answer(t, te2.submit("bs3", `end\`), 200, "handed", "keys", "")
	if te2.keys.calls[0].keys[0] != `end\ ` {
		t.Fatalf("keys got %q", te2.keys.calls[0].keys[0])
	}
}

func TestTrailingBackslashPad(t *testing.T) {
	for in, want := range map[string]string{`a`: `a`, `a\`: `a\ `, `a\\`: `a\\`, `a\\\`: `a\\\ `, `a\b`: `a\b`, "a\\\nb": "a\\\nb"} {
		if got := padTrailingBackslash(in); got != want {
			t.Errorf("%q -> %q, want %q", in, got, want)
		}
	}
}

// failEntering: MarkEntering fails (a row that moved on): the composer is still cleaned up.
type failEntering struct{ TypedQueue }

func (failEntering) MarkEntering(string) error { return promptq.ErrBadTransition }

func TestTypedSubmit_MarkEnteringFailureRunsTheCleanup(t *testing.T) {
	te := newTEnv(t)
	te.mod.td.q = failEntering{te.q}
	answer(t, te.submit("me1", "x"), 409, "refused", "", "conflict")
	if te.keys.n() != 0 {
		t.Fatal("Enter went out without the entering mark")
	}
	if te.fm.count(promptq.KindClearIf) != 1 || phaseOf(t, te.q, "me1").Phase != promptq.PhaseCleared {
		t.Fatalf("clear_if=%d phase=%s", te.fm.count(promptq.KindClearIf), phaseOf(t, te.q, "me1").Phase)
	}
}

// One error shape for every non-2xx answer: {outcome?, reason, error}.
func TestTypedSubmit_EveryErrorHasReasonAndError(t *testing.T) {
	te := newTEnv(t)
	cases := map[string]*httptest.ResponseRecorder{
		"400":     te.submit("ue1", "/help"),
		"400json": te.post("/api/sessions/"+tCode+"/submit", "{"),
		"404":     te.post("/api/sessions/nope/submit", body("ue2", "x", tInst)),
		"409":     te.post("/api/sessions/"+tCode+"/submit", body("ue3", "x", "99:99")),
	}
	te.mod.td.owners = tResolver{}
	cases["409 not_claude"] = te.submit("ue4", "x")
	release, _ := te.mod.locks.acquire(context.Background(), sid, time.Second)
	te.mod.td.owners = tResolver{found: true, owner: tOwner}
	cases["429"] = te.submit("ue5", "x")
	release()
	te.mod.td = nil
	cases["503"] = te.submit("ue6", "x")
	for name, w := range cases {
		var a struct{ Reason, Error string }
		if err := json.Unmarshal(w.Body.Bytes(), &a); err != nil || a.Reason == "" || a.Reason != a.Error || w.Code < 400 {
			t.Errorf("%s: %d %s", name, w.Code, w.Body.String())
		}
	}
}

// The finaliser: one per id, and it takes the locks before it cleans up.
func TestTypedSubmit_FinaliserIsDedupedPerIDAndTakesTheLocks(t *testing.T) {
	te := newTEnv(t)
	te.fm.fillDelay = 60 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodPost, "/api/sessions/"+tCode+"/submit", strings.NewReader(body("fn1", "x", tInst))).WithContext(ctx)
	done := make(chan struct{})
	go func() { te.mux.ServeHTTP(httptest.NewRecorder(), req); close(done) }()
	time.Sleep(20 * time.Millisecond)
	cancel()
	<-done
	// the handler is gone; a second request for the same id (joins the first fill) also goes away
	c := &typedCtx{td: te.mod.td, id: "fn1", owner: tOwner}
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); te.mod.spawnFinaliser(c, te.mod.tuning()) }()
	}
	wg.Wait()
	// hold the locks: the cleanup must wait for them
	release, ok := te.mod.paneLocks.acquire(context.Background(), "%5", time.Second)
	if !ok {
		t.Fatal("lock")
	}
	time.Sleep(150 * time.Millisecond)
	if te.fm.count(promptq.KindClearIf) != 0 {
		release()
		t.Fatal("the finaliser cleaned up without the pane lock")
	}
	release()
	eventually(t, "cleared once", func() bool { return phaseOf(t, te.q, "fn1").Phase == promptq.PhaseCleared })
	time.Sleep(50 * time.Millisecond)
	if n := te.fm.count(promptq.KindClearIf); n != 1 {
		t.Fatalf("clear_if = %d, want exactly one finaliser", n)
	}
}

// A stopped module admits no finaliser (it stops on the daemon's shutdown).
func TestTypedSubmit_NoFinaliserAfterShutdown(t *testing.T) {
	te := newTEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	te.mod.cur = &generation{ctx: ctx, cancel: cancel}
	te.mod.spawnFinaliser(&typedCtx{td: te.mod.td, id: "sd1", owner: tOwner}, te.mod.tuning())
	te.mod.finMu.Lock()
	n := len(te.mod.finalising)
	te.mod.finMu.Unlock()
	if n != 0 {
		t.Fatalf("finalisers after shutdown = %d", n)
	}
}
