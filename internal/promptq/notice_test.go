package promptq

import (
	"context"
	"strings"
	"testing"
	"time"
)

// Notices (lease v2 amendment A §2.10): a job of kind notice, owned by the stream that announced CapLeaseNotice.

func noticeAsync(q *Queue, sid, key, text string) chan answer {
	ch := make(chan answer, 1)
	go func() {
		r, err := q.SubmitNotice(context.Background(), sid, key, text)
		ch <- answer{r, err}
	}()
	return ch
}

// A notice needs a stream that announced the notice capability: the prompt owner alone is not enough.
// (M: use OwnerOf.)
func TestSubmitNotice_NeedsTheCapability(t *testing.T) {
	q, o := newQ(t) // mod1 owns the prompts of s1, nobody announced the notice capability
	if r, err := q.SubmitNotice(context.Background(), "s1", "n1", "輪到你了"); err != nil || r.Status != NoMod {
		t.Fatalf("no notice-capable stream: %+v %v", r, err)
	}
	o.setNotice("s1", "mod1")
	ch := noticeAsync(q, "s1", "n2", "輪到你了")
	j := next(t, q, "mod1", "s1")
	if j.Kind != KindNotice || j.Text != "輪到你了" || j.SessionID != "s1" {
		t.Fatalf("job = %+v", j)
	}
	if err := q.Result("mod1", j.ID, Outcome{Status: Accepted}); err != nil {
		t.Fatal(err)
	}
	if r := take(t, ch); r.Status != Accepted {
		t.Fatalf("result = %+v", r)
	}
	// and a session that has the capability but no prompt owner is still answered: the notice has its own owner
	o.set("s1", "")
	ch = noticeAsync(q, "s1", "n3", "again")
	j = next(t, q, "mod1", "s1")
	q.Result("mod1", j.ID, Outcome{Status: Accepted})
	if r := take(t, ch); r.Status != Accepted {
		t.Fatalf("no prompt owner: %+v", r)
	}
}

// The notice goes to the notice-capable stream, and a prompt behind it to the prompt owner; neither is handed to the
// other. (M: hand the head to whoever polls.)
func TestNext_NoticeGoesToItsOwnerOnly(t *testing.T) {
	q, o := newQ(t)
	o.setNotice("s1", "modN") // modN takes notices, mod1 takes prompts
	ch := noticeAsync(q, "s1", "n1", "輪到你了")
	if _, ok := q.Next(context.Background(), "mod1", "s1", 100*time.Millisecond); ok {
		t.Fatal("the prompt owner was handed a notice")
	}
	j := next(t, q, "modN", "s1")
	if j.Kind != KindNotice {
		t.Fatalf("job = %+v", j)
	}
	// a result from the prompt owner for a job leased to modN is refused
	if err := q.Result("mod1", j.ID, Outcome{Status: Accepted}); err == nil {
		t.Fatal("a stream that was not handed the job reported it")
	}
	if err := q.Result("modN", j.ID, Outcome{Status: Accepted}); err != nil {
		t.Fatal(err)
	}
	take(t, ch)
	// a prompt is the prompt owner's, not modN's
	pch := submitAsync(q, "s1", "c1", "hello")
	if _, ok := q.Next(context.Background(), "modN", "s1", 100*time.Millisecond); ok {
		t.Fatal("the notice stream was handed a prompt")
	}
	pj := next(t, q, "mod1", "s1")
	q.Result("mod1", pj.ID, Outcome{Status: Accepted})
	take(t, pch)
}

// busy (a turn was running) and timeout (nothing was handed out in time) were never sent: the same key may be sent
// again. (M: give up on busy.)
func TestSubmitNotice_BusyMaySendAgain(t *testing.T) {
	q, o := newQ(t)
	o.setNotice("s1", "mod1")
	ch := noticeAsync(q, "s1", "n1", "輪到你了")
	j := next(t, q, "mod1", "s1")
	q.Result("mod1", j.ID, Outcome{Status: Busy})
	if r := take(t, ch); r.Status != Busy {
		t.Fatalf("result = %+v", r)
	}
	ch = noticeAsync(q, "s1", "n1", "輪到你了") // the same key, again
	j2 := next(t, q, "mod1", "s1")
	if j2.ID == j.ID {
		t.Fatal("the same job was handed out twice")
	}
	q.Result("mod1", j2.ID, Outcome{Status: Accepted})
	if r := take(t, ch); r.Status != Accepted {
		t.Fatalf("result = %+v", r)
	}

	// timeout: nobody fetched it within the wait
	ch = noticeAsync(q, "s1", "n2", "x")
	if r := take(t, ch); r.Status != Timeout {
		t.Fatalf("not fetched: %+v", r)
	}
	ch = noticeAsync(q, "s1", "n2", "x")
	j3 := next(t, q, "mod1", "s1")
	q.Result("mod1", j3.ID, Outcome{Status: Accepted})
	if r := take(t, ch); r.Status != Accepted {
		t.Fatalf("again after timeout: %+v", r)
	}
}

// An accepted (or dropped, or unknown) notice is answered again from the ledger and never sent twice.
// (M: remove the ledger row of an accepted notice.)
func TestSubmitNotice_AtMostOnceByKey(t *testing.T) {
	q, o := newQ(t)
	o.setNotice("s1", "mod1")
	ch := noticeAsync(q, "s1", "n1", "輪到你了")
	j := next(t, q, "mod1", "s1")
	q.Result("mod1", j.ID, Outcome{Status: Accepted})
	take(t, ch)
	r, err := q.SubmitNotice(context.Background(), "s1", "n1", "輪到你了")
	if err != nil || r.Status != Accepted {
		t.Fatalf("the repeat = %+v %v", r, err)
	}
	if _, ok := q.Next(context.Background(), "mod1", "s1", 100*time.Millisecond); ok {
		t.Fatal("the repeat was handed out again")
	}
	// the same key for another text is a client bug
	if r, _ := q.SubmitNotice(context.Background(), "s1", "n1", "something else"); r.Status != Dropped || r.Reason != "client_msg_id_reused" {
		t.Fatalf("reused key: %+v", r)
	}
	// a dropped one is final too
	ch = noticeAsync(q, "s1", "n2", "x")
	j = next(t, q, "mod1", "s1")
	q.Result("mod1", j.ID, Outcome{Status: Dropped, Reason: "session_changed"})
	take(t, ch)
	if r, _ := q.SubmitNotice(context.Background(), "s1", "n2", "x"); r.Status != Dropped || r.Reason != "session_changed" {
		t.Fatalf("the repeat of a dropped one: %+v", r)
	}
}

func TestSubmitNotice_BadText(t *testing.T) {
	q, o := newQ(t)
	o.setNotice("s1", "mod1")
	for name, text := range map[string]string{"blank": "  \n", "empty": "", "too long": strings.Repeat("字", MaxNoticeBytes/3+1)} {
		if r, _ := q.SubmitNotice(context.Background(), "s1", "n-"+name, text); r.Status != Dropped || r.Reason != "bad_text" {
			t.Errorf("%s: %+v", name, r)
		}
	}
	if r, _ := q.SubmitNotice(context.Background(), "s1", "n-edge", strings.Repeat("a", MaxNoticeBytes)); r.Status == Dropped {
		t.Errorf("a notice at the limit was refused: %+v", r)
	}
}
