package conversation

import (
	"context"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/wake/purdex/internal/promptq"
)

// codex A-1: the same client_msg_id on two sessions at once must type once (the claim is atomic, before typing).
func TestKeysLedger_ClaimIsAtomicAcrossSessions(t *testing.T) {
	var l keysLedger
	now := time.Now()
	var wg sync.WaitGroup
	wins := make(chan string, 16)
	for i := 0; i < 16; i++ {
		sid := "s" + string(rune('a'+i))
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, claimed, _ := l.claim("same-id", sid, now); claimed {
				wins <- sid
			}
		}()
	}
	wg.Wait()
	close(wins)
	if len(wins) != 1 {
		t.Fatalf("%d claims won, want exactly one", len(wins))
	}
}

func TestKeysSubmit_SecondClaimOfAnInFlightIdNeverTypes(t *testing.T) {
	te := keysEnv(t)
	tune := te.mod.tuning()
	// A request that already passed the replay check meets a claim another session made in between: keysSubmit itself must
	// refuse (the replay check is racy by nature; the claim is the guard).
	te.mod.keysSeen.claim("dup1", "some-other-session", tune.now())
	c := &typedCtx{td: te.mod.td, id: "dup1", text: "x", expected: tInst, tmuxID: "t", owner: tOwner}
	w := httptest.NewRecorder()
	te.mod.keysSubmit(w, c, "idle", tune)
	answer(t, w, 409, "refused", "", "client_msg_id_reused")
	// The same session's in-flight claim answers unknown.
	te.mod.keysSeen.claim("dup2", tOwner.SessionID, tune.now())
	c.id = "dup2"
	w = httptest.NewRecorder()
	te.mod.keysSubmit(w, c, "idle", tune)
	answer(t, w, 200, "unknown", "keys", "")
	if te.keys.n() != 0 {
		t.Fatal("typed although the id was already claimed")
	}
}

// codex A-2: a light read that fails right before Enter fails closed.
func TestTypedSubmit_LightUnavailableBeforeEnterClearsAndNeverEnters(t *testing.T) {
	te := newTEnv(t)
	te.light.seq = []string{"idle", "!"} // the pre-fill read is fine, the one right before Enter fails
	answer(t, te.submit("lu1", "x"), 409, "refused", "", "light_unavailable")
	if te.keys.n() != 0 || te.fm.count(promptq.KindClearIf) != 1 || phaseOf(t, te.q, "lu1").Phase != promptq.PhaseCleared {
		t.Fatalf("keys=%d clear_if=%d phase=%s", te.keys.n(), te.fm.count(promptq.KindClearIf), phaseOf(t, te.q, "lu1").Phase)
	}
}

func TestKeysSubmit_LightUnavailableOnTheReReadTypesNothing(t *testing.T) {
	te := keysEnv(t)
	te.light.seq = []string{"idle", "!"}
	answer(t, te.submit("lu2", "x"), 409, "refused", "", "light_unavailable")
	if te.keys.n() != 0 {
		t.Fatal("typed blind with an unreadable light")
	}
	te.light.seq = []string{"idle"}
	answer(t, te.submit("lu2", "x"), 200, "handed", "keys", "") // nothing went out, so a retry is safe
}

// codex A-4: after an unknown Enter outcome the clean-up is tried twice.
func TestTypedSubmit_UnknownEnterCleanupRetriesOnce(t *testing.T) {
	te := newTEnv(t)
	te.keys.errs = []error{context.DeadlineExceeded}
	te.fm.onClear = func(n int) promptq.Outcome {
		if n == 1 {
			return promptq.Outcome{Status: promptq.Dropped, Reason: "not_ours"}
		}
		return promptq.Outcome{Status: promptq.Accepted}
	}
	answer(t, te.submit("uc1", "x"), 200, "unknown", "fill", "")
	if n := te.fm.count(promptq.KindClearIf); n != 2 {
		t.Fatalf("clear_if = %d, want a try and a retry", n)
	}
}

func TestTypedSubmit_UnknownEnterCleanupThatFailsTwiceStopsAtTwo(t *testing.T) {
	te := newTEnv(t)
	te.keys.errs = []error{context.DeadlineExceeded}
	te.fm.onClear = func(int) promptq.Outcome { return promptq.Outcome{Status: promptq.Dropped, Reason: "not_ours"} }
	answer(t, te.submit("uc2", "x"), 200, "unknown", "fill", "")
	if n := te.fm.count(promptq.KindClearIf); n != 2 {
		t.Fatalf("clear_if = %d, want exactly two", n)
	}
	if r := phaseOf(t, te.q, "uc2"); r.Phase != promptq.PhaseUnknown {
		t.Fatalf("phase = %s", r.Phase)
	}
}
