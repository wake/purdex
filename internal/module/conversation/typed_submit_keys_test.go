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
	te.mod.tune.keysSettle = time.Millisecond
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

// Measured (K-1c): the text and the Enter must be two writes with a settle between them, or Claude Code reads the Enter as
// part of a paste and swallows it. Multi-line text keeps its LF bytes (they stay newlines in the composer).
func TestKeysSubmit_TextThenSettleThenEnterAsTwoGuardedCalls(t *testing.T) {
	te := keysEnv(t)
	te.mod.tune.keysSettle = 40 * time.Millisecond
	var at []time.Time
	te.keys.onCall = func(keyCall) { at = append(at, time.Now()) }
	answer(t, te.submit("k5", "line1\nline2 @src"), 200, "handed", "keys", "")
	if te.keys.n() != 2 {
		t.Fatalf("calls = %d, want text then Enter", te.keys.n())
	}
	c0, c1 := te.keys.calls[0], te.keys.calls[1]
	if len(c0.keys) != 1 || c0.keys[0] != "line1\nline2 @src " || c0.pane != "%5" {
		t.Fatalf("text call = %+v", c0)
	}
	if len(c1.keys) != 1 || c1.keys[0] != tmux.KeyEnter || c1.pane != "%5" || c1.sid != c0.sid || c1.inst != c0.inst {
		t.Fatalf("enter call = %+v", c1)
	}
	if gap := at[1].Sub(at[0]); gap < 40*time.Millisecond {
		t.Fatalf("settle between the text and the Enter = %v, want >= 40ms", gap)
	}
	answer(t, te.submit("k5", "line1\nline2 @src"), 200, "handed", "keys", "") // the same id never types twice
	if te.keys.n() != 2 {
		t.Fatalf("a replay typed again: %d calls", te.keys.n())
	}
}

// A mod that reconnects during the settle does not strand the text: the second check is about the light only (spec §2), so
// with an idle light the Enter still goes out.
func TestKeysSubmit_OwnerAppearingDuringSettleStillSendsTheEnter(t *testing.T) {
	te := keysEnv(t)
	te.keys.onCall = func(c keyCall) {
		if c.keys[0] == "hello" {
			te.owners.mu.Lock()
			te.owners.m[sid] = "mod1"
			te.owners.mu.Unlock()
		}
	}
	answer(t, te.submit("ko1", "hello"), 200, "handed", "keys", "")
	if te.keys.n() != 2 || te.keys.calls[1].keys[0] != tmux.KeyEnter {
		t.Fatalf("calls = %+v", te.keys.calls)
	}
}

// A request still settling holds an in-flight claim: filling the ledger with other ids must not evict it, or a replay would
// claim again and type twice.
func TestKeysSubmit_InFlightClaimSurvivesCapacityEviction(t *testing.T) {
	te := keysEnv(t)
	inText, release := make(chan struct{}), make(chan struct{})
	te.keys.onCall = func(c keyCall) {
		if c.keys[0] == "hello" {
			close(inText)
			<-release
		}
	}
	done := make(chan struct{})
	go func() { te.submit("kf1", "hello"); close(done) }()
	<-inText
	now := time.Now()
	for i := 0; i < maxKeysLedger+5; i++ {
		te.mod.keysSeen.put("fill"+time.Duration(i).String(), keysRow{handed: true}, now.Add(time.Duration(i)*time.Millisecond))
	}
	answer(t, te.submit("kf1", "hello"), 200, "unknown", "keys", "")
	if te.keys.n() != 0 { // the blocked call is recorded only after it returns
		t.Fatalf("calls = %d", te.keys.n())
	}
	close(release)
	<-done
	if te.keys.n() != 2 {
		t.Fatalf("calls = %d, want the one text and one Enter", te.keys.n())
	}
	answer(t, te.submit("kf1", "hello"), 200, "handed", "keys", "")
}

// Full of in-flight claims: a new id is refused cleanly (busy), nothing is evicted and nothing is typed.
func TestKeysSubmit_LedgerFullOfInFlightRefusesBusy(t *testing.T) {
	te := keysEnv(t)
	now := time.Now()
	for i := 0; i < maxKeysLedger; i++ {
		if _, claimed, full := te.mod.keysSeen.claim("p"+time.Duration(i).String(), "s", now); !claimed || full {
			t.Fatalf("claim %d: %v %v", i, claimed, full)
		}
	}
	if w := te.submit("kfull", "x"); w.Code != 429 {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	if te.keys.n() != 0 {
		t.Fatal("typed while refusing")
	}
	if _, ok := te.mod.keysSeen.get("p0s", now); !ok {
		t.Fatal("an in-flight row was evicted")
	}
	te.mod.keysSeen.settle("p0s") // once one settles it is evictable and the new id fits
	answer(t, te.submit("kfull", "x"), 200, "handed", "keys", "")
}

// The default settle is the measured one plus margin.
func TestKeysTuning_DefaultSettle(t *testing.T) {
	if got := (&Module{}).tuning().keysSettle; got != 150*time.Millisecond {
		t.Fatalf("default keysSettle = %v", got)
	}
}

// Measured: Claude Code reads a trailing backslash (odd or even run) plus Enter as a newline, so keys mode pads every one.
func TestKeysSubmit_AnyTrailingBackslashGetsASpace(t *testing.T) {
	te := keysEnv(t)
	answer(t, te.submit("kb1", `two \\`), 200, "handed", "keys", "")
	if te.keys.calls[0].keys[0] != `two \\ ` {
		t.Fatalf("keys got %q", te.keys.calls[0].keys[0])
	}
	// padded once at the shared normalisation: exactly one trailing space, never two
	answer(t, te.submit("kb2", `x\\\`), 200, "handed", "keys", "")
	if te.keys.calls[2].keys[0] != `x\\\ ` {
		t.Fatalf("keys got %q", te.keys.calls[2].keys[0])
	}
}

// The light is read once more after the text and before the Enter: a dialog that opened meanwhile must not get the Enter.
// The text is in the terminal then, so the answer is text_left_in_terminal and the id stays recorded as unknown.
func TestKeysSubmit_LightNotIdleAfterTheTextSendsNoEnter(t *testing.T) {
	for _, light := range []string{"waiting", "running", "!"} {
		te := keysEnv(t)
		te.light.seq = []string{"idle", "idle", light} // after the locks, pre-type, pre-Enter
		answer(t, te.submit("kl1", "hello"), 409, "refused", "", "text_left_in_terminal")
		if te.keys.n() != 1 || te.keys.calls[0].keys[0] != "hello" {
			t.Fatalf("%s: calls = %+v", light, te.keys.calls)
		}
		answer(t, te.submit("kl1", "hello"), 200, "unknown", "keys", "")
		if te.keys.n() != 1 {
			t.Fatalf("%s: a replay typed again", light)
		}
	}
}

// A guard refusal on the Enter step means the text is in the terminal unsent: not a clean refusal.
func TestKeysSubmit_EnterGuardRefusedLeavesTheTextInTheTerminal(t *testing.T) {
	for _, e := range []error{tmux.ErrPaneBusy, tmux.ErrGenerationMismatch, tmux.ErrPaneMismatch, context.DeadlineExceeded} {
		te := keysEnv(t)
		te.keys.errs = []error{nil, e}
		answer(t, te.submit("ke1", "hello"), 409, "refused", "", "text_left_in_terminal")
		answer(t, te.submit("ke1", "hello"), 200, "unknown", "keys", "")
		if te.keys.n() != 2 {
			t.Fatalf("%v: calls = %d", e, te.keys.n())
		}
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

// A guard refusal of the text call means nothing was typed: the id is free for a retry.
func TestKeysSubmit_GuardRefusalTypedNothing(t *testing.T) {
	te := keysEnv(t)
	te.keys.errs = []error{tmux.ErrPaneBusy}
	answer(t, te.submit("k7", "x"), 409, "refused", "", "pane_busy")
	answer(t, te.submit("k7", "x"), 200, "handed", "keys", "")
	if te.keys.n() != 3 { // the refused text, then the retry's text and Enter
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
