package conversation

import (
	"context"
	"testing"
	"time"

	"github.com/wake/purdex/internal/tmux"
)

// keysEnv: the mod is gone and has been for longer than keysAfter.
func keysEnv(t *testing.T) *tenv {
	t.Helper()
	te := newTEnv(t)
	te.owners.drop()
	te.mod.tune.keysAfter = time.Millisecond
	time.Sleep(5 * time.Millisecond)
	return te
}

// Keys mode (no mod) is allowed only when the light is idle (not running, not waiting) and no owner was seen for keysAfter.
func TestKeysSubmit_NeedsAnOwnerlessWindowAndAnIdleLight(t *testing.T) {
	te := newTEnv(t)
	te.owners.drop()
	te.mod.tune.keysAfter = time.Hour
	answer(t, te.submit("k1", "x"), 409, "refused", "", "no_owner_yet")
	te.mod.tune.keysAfter = time.Millisecond
	time.Sleep(5 * time.Millisecond)
	for _, light := range []string{"error", "running", "waiting"} {
		te.light.seq = []string{light}
		w := te.submit("k2-"+light, "x")
		if w.Code != 409 {
			t.Fatalf("%s: status %d %s", light, w.Code, w.Body.String())
		}
	}
	if te.keys.n() != 0 {
		t.Fatal("typed blind while the light was not idle")
	}
	te.light.seq = []string{"idle"}
	answer(t, te.submit("k3", "line1\nline2 @src"), 200, "handed", "keys", "")
	errOf(t, te.submit("k4", "/help"), 400, "needs_terminal")
}

// The text and the Enter go out in ONE guarded send-keys call, never two calls apart.
func TestKeysSubmit_TextAndEnterAreOneGuardedCall(t *testing.T) {
	te := keysEnv(t)
	answer(t, te.submit("k5", "line1\nline2 @src"), 200, "handed", "keys", "")
	if te.keys.n() != 1 {
		t.Fatalf("calls = %d, want one", te.keys.n())
	}
	c := te.keys.calls[0]
	if len(c.keys) != 2 || c.keys[0] != "line1\nline2 @src " || c.keys[1] != tmux.KeyEnter || c.pane != "%5" {
		t.Fatalf("call = %+v", c)
	}
	answer(t, te.submit("k5", "line1\nline2 @src"), 200, "handed", "keys", "") // the same id never types twice
	if te.keys.n() != 1 {
		t.Fatalf("a replay typed again: %d calls", te.keys.n())
	}
}

// The light is read again right before sending: a question that appeared since the first read stops the typing.
func TestKeysSubmit_LightIsReadAgainRightBeforeSending(t *testing.T) {
	te := keysEnv(t)
	te.light.seq = []string{"idle", "waiting"} // the read after the locks, then the pre-send read
	w := te.submit("k6", "x")
	if w.Code != 409 || te.keys.n() != 0 {
		t.Fatalf("status %d keys=%d %s", w.Code, te.keys.n(), w.Body.String())
	}
	te.light.seq = []string{"idle"}
	answer(t, te.submit("k6", "x"), 200, "handed", "keys", "") // nothing went out, so the id is free again
}

// A guard refusal of the single call means nothing was typed: the id is free for a retry.
func TestKeysSubmit_GuardRefusalTypedNothing(t *testing.T) {
	te := keysEnv(t)
	te.keys.errs = []error{tmux.ErrPaneBusy}
	answer(t, te.submit("k7", "x"), 409, "refused", "", "pane_busy")
	answer(t, te.submit("k7", "x"), 200, "handed", "keys", "")
	if te.keys.n() != 2 {
		t.Fatalf("calls = %d", te.keys.n())
	}
}

// A tmux invocation error: the text may have landed. 409 text_left_in_terminal, the id stays recorded as unknown (a repeat
// never types again) and the deck tells the user to look at the terminal.
func TestKeysSubmit_TmuxErrorMayHaveTypedSoItStaysUnknown(t *testing.T) {
	te := keysEnv(t)
	te.keys.errs = []error{context.DeadlineExceeded}
	answer(t, te.submit("kt1", "hello"), 409, "refused", "", "text_left_in_terminal")
	answer(t, te.submit("kt1", "hello"), 200, "unknown", "keys", "")
	if te.keys.n() != 1 {
		t.Fatalf("a replay typed again: %d calls", te.keys.n())
	}
}

// The keys ledger keeps tombstones for 24 h by the tuned clock, and when full forgets the OLDEST by time, never a random one.
func TestKeysLedger_TombstonesLastADayAndOldestGoesFirst(t *testing.T) {
	var k keysLedger
	t0 := time.Unix(1000, 0)
	k.put("old", keysRow{handed: true}, t0)
	if r, ok := k.get("old", t0.Add(23*time.Hour)); !ok || !r.handed {
		t.Fatalf("23 h: %+v %v", r, ok)
	}
	if _, ok := k.get("old", t0.Add(25*time.Hour)); ok {
		t.Fatal("a tombstone must expire after 24 h")
	}
	var f keysLedger
	for i := 0; i < maxKeysLedger+10; i++ {
		f.put("id"+time.Duration(i).String(), keysRow{}, t0.Add(time.Duration(i)*time.Second))
	}
	now := t0.Add(time.Hour)
	if _, ok := f.get("id0s", now); ok {
		t.Fatal("the oldest should have been evicted first")
	}
	last := "id" + time.Duration(maxKeysLedger+9).String()
	if _, ok := f.get(last, now); !ok {
		t.Fatal("the newest must be kept")
	}
	if _, ok := f.get("id"+time.Duration(11).String(), now); !ok {
		t.Fatal("an id newer than the evicted ones must be kept")
	}
}
