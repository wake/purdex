package promptq

import (
	"context"
	"strings"
	"testing"
	"time"
)

// Review fixes of K-1a-1: clock trust, cleanup of lost fills, text bounds, open typed rows.

// The mod's clock is not trusted: ReceivedAt is the daemon's time of arrival and FilledAt is clamped into
// [handed out, received]; a missing one is ReceivedAt. Mutation gate: pass the mod's value through -> red.
func TestFill_FilledAtIsClampedAndReceivedAtIsTheDaemons(t *testing.T) {
	handed := time.Unix(1_700_000_000, 0)
	recv := handed.Add(300 * time.Millisecond)
	for name, c := range map[string]struct{ modAt, want int64 }{
		"honest":  {handed.UnixMilli() + 100, handed.UnixMilli() + 100},
		"future":  {recv.UnixMilli() + 3_600_000, recv.UnixMilli()},
		"past":    {handed.UnixMilli() - 3_600_000, handed.UnixMilli()},
		"missing": {0, recv.UnixMilli()},
	} {
		q, _ := newQ(t)
		q.HandTimeout = time.Minute
		now := handed
		q.Now = func() time.Time { return now }
		ch := fillAsync(q, "s1", "c1", "x")
		j := next(t, q, "mod1", "s1")
		now = recv
		if err := q.Result("mod1", j.ID, Outcome{Status: Accepted, FilledAt: c.modAt}); err != nil {
			t.Fatal(err)
		}
		r := take(t, ch)
		if r.FilledAt != c.want || r.ReceivedAt != recv.UnixMilli() {
			t.Fatalf("%s: filled_at %d (want %d), received_at %d (want %d)", name, r.FilledAt, c.want, r.ReceivedAt, recv.UnixMilli())
		}
	}
}

// The mod reports that it cannot tell what is in the composer: unknown, with its reason, phase unknown.
func TestFill_ModReportsUnknown(t *testing.T) {
	q, _ := newQ(t)
	ch := fillAsync(q, "s1", "c1", "x")
	j := next(t, q, "mod1", "s1")
	if err := q.Result("mod1", j.ID, Outcome{Status: Unknown, Reason: "fill_error"}); err != nil {
		t.Fatal(err)
	}
	if r := take(t, ch); r.Status != Unknown || r.Reason != "fill_error" || r.Phase != PhaseUnknown {
		t.Fatalf("result = %+v", r)
	}
}

// A lost result leaves the row unknown with our text maybe in the composer: it may still be cleared, but never once Enter
// may have gone. Mutation gate: allow cleared from any unknown -> red.
func TestFill_UnknownMayBeClearedUnlessEnterMayHaveGone(t *testing.T) {
	q, _ := newQ(t)
	ch := fillAsync(q, "s1", "lost", "x")
	next(t, q, "mod1", "s1") // handed out, no result
	if r := take(t, ch); r.Phase != PhaseUnknown {
		t.Fatalf("%+v", r)
	}
	if err := q.MarkCleared("lost"); err != nil {
		t.Fatalf("unknown -> cleared: %v", err)
	}
	fillOK(t, q, "sent", "x", 1)
	q.MarkEntering("sent")
	q.MarkUnknown("sent")
	if err := q.MarkCleared("sent"); err != ErrBadTransition {
		t.Fatalf("cleared after Enter may have gone: %v", err)
	}
}

// ClearFilled sends clear_if with the row's own text for a filled / unknown row and marks it cleared when the mod accepts.
func TestClearFilled_UnknownRowIsClearedThroughTheMod(t *testing.T) {
	q, _ := newQ(t)
	ch := fillAsync(q, "s1", "lost", "typed text")
	next(t, q, "mod1", "s1")
	take(t, ch) // unknown
	out := make(chan answer, 1)
	go func() { r, err := q.ClearFilled(context.Background(), "lost"); out <- answer{r, err} }()
	j := next(t, q, "mod1", "s1")
	if j.Kind != KindClearIf || j.Text != "typed text" {
		t.Fatalf("job = %+v", j)
	}
	q.Result("mod1", j.ID, Outcome{Status: Accepted})
	if r := take(t, out); r.Status != Accepted {
		t.Fatalf("result = %+v", r)
	}
	if got, _ := q.Lookup("lost"); got.Phase != PhaseCleared {
		t.Fatalf("phase = %s", got.Phase)
	}
}

func TestClearFilled_NotAfterEnterAndNotForUnknownIDs(t *testing.T) {
	q, _ := newQ(t)
	fillOK(t, q, "e", "x", 1)
	q.MarkEntering("e")
	if r, err := q.ClearFilled(context.Background(), "e"); err != nil || r.Status != Dropped || r.Reason != "not_clearable" {
		t.Fatalf("entering: %+v %v", r, err)
	}
	if r, _ := q.ClearFilled(context.Background(), "nope"); r.Status != Dropped || r.Reason != "not_clearable" {
		t.Fatalf("no such id: %+v", r)
	}
	if _, ok := q.Next(context.Background(), "mod1", "s1", 60*time.Millisecond); ok {
		t.Fatal("a refused cleanup queued a job")
	}
}

// Text the mod would refuse is refused here, at once, and never queued. Bytes, not characters.
func TestFillAndClearIf_RejectBlankOrOverlongTextAtOnce(t *testing.T) {
	q, _ := newQ(t)
	long := strings.Repeat("中", MaxTextBytes/3+1) // fewer characters than bytes: only a byte count catches it
	for name, text := range map[string]string{"blank": " \n\t", "empty": "", "long": long} {
		if r, err := q.SubmitFill(context.Background(), "s1", "c-"+name, text); err != nil || r.Status != Dropped || r.Reason != "bad_text" {
			t.Fatalf("fill %s: %+v %v", name, r, err)
		}
		if r, err := q.ClearIf(context.Background(), "s1", text); err != nil || r.Status != Dropped || r.Reason != "bad_text" {
			t.Fatalf("clear_if %s: %+v %v", name, r, err)
		}
		if _, ok := q.Lookup("c-" + name); ok {
			t.Fatalf("%s left a ledger row", name)
		}
	}
	if _, ok := q.Next(context.Background(), "mod1", "s1", 60*time.Millisecond); ok {
		t.Fatal("a rejected text was queued")
	}
	exact := strings.Repeat("a", MaxTextBytes)
	ch := fillAsync(q, "s1", "exact", exact)
	j := next(t, q, "mod1", "s1")
	if j.Text != exact {
		t.Fatal("a text of exactly the limit was altered")
	}
	q.Result("mod1", j.ID, Outcome{Status: Accepted, FilledAt: 1})
	take(t, ch)
}

// Rows whose text may be in the composer or sent are not forgotten by TTL or by flood while they are fresh; past
// OpenTypedMaxAge the sweep settles them (see settle_test.go). Mutation gate: evict by TTL/size regardless of phase -> red.
func TestLedger_OpenTypedRowsAreNeverEvicted(t *testing.T) {
	q, _ := newQ(t)
	now := time.Unix(1_700_000_000, 0)
	q.Now = func() time.Time { return now }
	phases := []string{PhaseFilled, PhaseEntering, PhaseUnknown, PhaseEntered, PhaseCleared}
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, p := range phases {
		q.ledger["t-"+p] = &entry{key: "t-" + p, job: Job{Kind: KindFill}, state: stDone, phase: p, finishedAt: now, done: make(chan struct{})}
	}
	now = now.Add(OpenTypedMaxAge - time.Minute)
	for i := 0; i < maxLedger+5; i++ { // a flood of settled rows
		id := "flood-" + time.Duration(i).String()
		q.ledger[id] = &entry{key: id, job: Job{Kind: KindSubmit}, state: stDone, finishedAt: now, done: make(chan struct{})}
	}
	q.sweepLedger()
	for _, p := range []string{PhaseFilled, PhaseEntering, PhaseUnknown} {
		if _, kept := q.ledger["t-"+p]; !kept {
			t.Fatalf("phase %s forgotten by the flood", p)
		}
	}
	now = now.Add(2 * LedgerTTL)
	q.sweepLedger()
	for _, p := range phases {
		if _, kept := q.ledger["t-"+p]; kept {
			t.Fatalf("phase %s kept after the TTL", p)
		}
		_, tomb := q.tombs["t-"+p]
		if tomb != (p == PhaseFilled || p == PhaseEntering || p == PhaseUnknown) {
			t.Fatalf("phase %s tombstone=%v", p, tomb)
		}
	}
}

// Open typed rows are bounded by count: over the cap a new fill is busy and nothing is queued; a repeat still answers.
func TestFill_TooManyOpenTypedRowsIsBusy(t *testing.T) {
	q, _ := newQ(t)
	q.mu.Lock()
	for i := 0; i < maxOpenTyped; i++ {
		id := "open-" + time.Duration(i).String()
		q.ledger[id] = &entry{key: id, job: Job{Kind: KindFill, SessionID: "s1", Text: "x"}, state: stDone, phase: PhaseUnknown, finishedAt: q.Now(), done: make(chan struct{})}
	}
	q.mu.Unlock()
	if r, err := q.SubmitFill(context.Background(), "s1", "new", "x"); err != nil || r.Status != Busy {
		t.Fatalf("new fill: %+v %v", r, err)
	}
	if r, _ := q.SubmitFill(context.Background(), "s1", "open-0s", "x"); r.Phase != PhaseUnknown {
		t.Fatalf("a repeat of an open row must still answer it: %+v", r)
	}
}

func TestRead_TruncatedFlagRides(t *testing.T) {
	q, _ := newQ(t)
	ch := make(chan answer, 1)
	go func() { r, err := q.Read(context.Background(), "s1"); ch <- answer{r, err} }()
	j := next(t, q, "mod1", "s1")
	q.Result("mod1", j.ID, Outcome{Status: Accepted, Text: "cut", Truncated: true})
	if r := take(t, ch); r.Text != "cut" || !r.Truncated {
		t.Fatalf("result = %+v", r)
	}
}
