package dialogue

import (
	"context"
	"errors"
	"testing"
	"time"
)

// rebuild replaces the rig's channel with one that has extra handlers, a clock and a call cap.
func (r *recapRig) rebuild(extra map[string]kindHandler, now func() time.Time, callCap int) {
	hs := map[string]kindHandler{kindRecap: &recapKind{r: r.rc}}
	for k, h := range extra {
		hs[k] = h
	}
	r.ch = newChannel(chanDeps{
		capable:  func(string) bool { r.mu.Lock(); defer r.mu.Unlock(); return r.capOK },
		handlers: hs, now: now, callCap: callCap, logf: func(string, ...any) {},
	})
	r.rc.enqueue = r.ch.enqueue
}

// waitAsync runs wait in the background and delivers its answer.
func (r *recapRig) waitAsync(ctx context.Context, d time.Duration) <-chan bool {
	out := make(chan bool, 1)
	go func() { out <- r.rc.wait(ctx, sidS, d) }()
	return out
}

func mustEnd(t *testing.T, c <-chan bool, want bool) {
	t.Helper()
	select {
	case got := <-c:
		if got != want {
			t.Fatalf("wait = %v, want %v", got, want)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("wait did not return")
	}
}

func mustPend(t *testing.T, c <-chan bool) {
	t.Helper()
	select {
	case got := <-c:
		t.Fatalf("wait returned %v too early", got)
	case <-time.After(50 * time.Millisecond):
	}
}

const recapAnswer = `{"headline":"h","done":["a"],"now":"n","decided":[],"waiting":[],"next":"x"}`

func TestRecapWait_NothingInFlight(t *testing.T) {
	r := newRecapRig(t)
	if !r.rc.wait(context.Background(), sidS, time.Hour) {
		t.Fatal("wait = false with nothing in flight")
	}
}

func TestRecapWait_EndsWhenAnswered(t *testing.T) {
	r := newRecapRig(t)
	r.seed(3)
	if got := r.rc.request(sidS, "compact"); got != recapQueued {
		t.Fatal(got)
	}
	c := r.waitAsync(context.Background(), 10*time.Second)
	mustPend(t, c)
	j, ok := r.take()
	if !ok {
		t.Fatal("no job")
	}
	r.answer(j, recapAnswer)
	mustEnd(t, c, true)
	if _, found, err := r.st.LatestRecap(sidS); err != nil || !found {
		t.Fatalf("recap found=%v err=%v", found, err)
	}
}

func TestRecapWait_EndsWhenTheJobFails(t *testing.T) {
	r := newRecapRig(t)
	r.seed(3)
	r.rc.request(sidS, "compact")
	c := r.waitAsync(context.Background(), 10*time.Second)
	mustPend(t, c)
	j, _ := r.take()
	if _, err := r.ch.Result("streamA", j.ID, result{Answered: false}); err != nil {
		t.Fatal(err)
	}
	mustEnd(t, c, true)
}

func TestRecapWait_TimesOutAndTheJobGoesOn(t *testing.T) {
	r := newRecapRig(t)
	r.seed(3)
	r.rc.request(sidS, "compact")
	if r.rc.wait(context.Background(), sidS, 20*time.Millisecond) {
		t.Fatal("wait = true before the job ended")
	}
	j, _ := r.take()
	r.answer(j, recapAnswer)
	if _, found, _ := r.st.LatestRecap(sidS); !found {
		t.Fatal("the recap was not stored after the wait timed out")
	}
}

func TestRecapWait_ContextEnds(t *testing.T) {
	r := newRecapRig(t)
	r.seed(3)
	r.rc.request(sidS, "compact")
	ctx, cancel := context.WithCancel(context.Background())
	c := r.waitAsync(ctx, time.Hour)
	mustPend(t, c)
	cancel()
	mustEnd(t, c, false)
}

func TestRecapWait_AfterTurnJobsAheadOfIt(t *testing.T) {
	r := newRecapRig(t)
	r.seed(3)
	r.rebuild(map[string]kindHandler{kindTurnSummary: &jcKind{}}, nil, 0)
	if _, why := r.ch.enqueue(sidS, kindTurnSummary, 1); why != "" {
		t.Fatal(why)
	}
	r.rc.request(sidS, "compact")
	c := r.waitAsync(context.Background(), 10*time.Second)
	j1, _ := r.take()
	if j1.Kind != kindTurnSummary {
		t.Fatalf("first job is %s", j1.Kind)
	}
	r.answer(j1, "x")
	mustPend(t, c)
	j2, _ := r.take()
	r.answer(j2, recapAnswer)
	mustEnd(t, c, true)
}

func TestRecapWait_SecondRecapHasItsOwnWait(t *testing.T) {
	r := newRecapRig(t)
	r.seed(3)
	r.rc.request(sidS, "compact")
	j, _ := r.take()
	r.answer(j, recapAnswer)
	if !r.rc.wait(context.Background(), sidS, time.Second) {
		t.Fatal("first wait")
	}
	id, _, err := r.st.InsertPendingSummary(SummaryRow{SessionID: sidS, ConvKey: sidS, TurnID: "t4", TurnNo: 4, TurnAt: 1_800_000_300_000})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.st.SetSummaryDone(id, "題4", "", "內容4。", false, usage{}); err != nil {
		t.Fatal(err)
	}
	if got := r.rc.request(sidS, "compact"); got != recapQueued {
		t.Fatal(got)
	}
	c := r.waitAsync(context.Background(), 10*time.Second)
	mustPend(t, c)
	j, _ = r.take()
	r.answer(j, recapAnswer)
	mustEnd(t, c, true)
}

func TestRecapWait_ChannelStop(t *testing.T) {
	r := newRecapRig(t)
	r.seed(3)
	r.rc.request(sidS, "compact")
	c := r.waitAsync(context.Background(), 10*time.Second)
	mustPend(t, c)
	r.ch.Stop()
	mustEnd(t, c, true)
}

func TestRecapWait_CapabilityLost(t *testing.T) {
	r := newRecapRig(t)
	r.seed(3)
	r.rc.request(sidS, "compact")
	c := r.waitAsync(context.Background(), 10*time.Second)
	mustPend(t, c)
	r.setCapable(false)
	r.ch.sweepIncapable()
	mustEnd(t, c, true)
}

func TestRecapWait_LeaseRunsOut(t *testing.T) {
	r := newRecapRig(t)
	r.seed(3)
	clk := &jcClock{t: time.Unix(1_800_000_000, 0)}
	r.rebuild(nil, clk.Now, 0)
	r.rc.request(sidS, "compact")
	c := r.waitAsync(context.Background(), 10*time.Second)
	if _, ok := r.take(); !ok {
		t.Fatal("no job")
	}
	mustPend(t, c)
	clk.Add(time.Minute)
	r.ch.reapLeases()
	// a lost lease may requeue the job for another try; take and lose it until the channel gives up
	for i := 0; i < 10; i++ {
		select {
		case got := <-c:
			if !got {
				t.Fatal("wait = false")
			}
			return
		default:
		}
		r.take()
		clk.Add(time.Minute)
		r.ch.reapLeases()
	}
	mustEnd(t, c, true)
}

func TestRecapWait_HourlyCapDrops(t *testing.T) {
	r := newRecapRig(t)
	r.seed(3)
	clk := &jcClock{t: time.Unix(1_800_000_000, 0)}
	r.rebuild(map[string]kindHandler{kindTurnSummary: &jcKind{}}, clk.Now, 1)
	if _, why := r.ch.enqueue(sidS, kindTurnSummary, 1); why != "" {
		t.Fatal(why)
	}
	r.rc.request(sidS, "compact")
	c := r.waitAsync(context.Background(), 10*time.Second)
	j1, _ := r.take() // uses the only call of the hour
	r.answer(j1, "x")
	mustPend(t, c)
	r.take() // the recap is skipped by the cap
	mustEnd(t, c, true)
}

// failingApply stores nothing: every apply of its answer fails.
type failingApply struct{ *recapKind }

func (failingApply) apply(job, string, usage) error { return errors.New("disk full") }

func TestRecapWait_ApplyGivesUp(t *testing.T) {
	r := newRecapRig(t)
	r.seed(3)
	r.rebuild(map[string]kindHandler{kindRecap: failingApply{&recapKind{r: r.rc}}}, nil, 0)
	r.rc.request(sidS, "compact")
	c := r.waitAsync(context.Background(), 10*time.Second)
	j, _ := r.take()
	r.answer(j, recapAnswer)
	for i := 0; i < maxApplyRetries-1; i++ {
		r.ch.reap()
		mustPend(t, c)
	}
	r.ch.reap()
	mustEnd(t, c, true)
}
