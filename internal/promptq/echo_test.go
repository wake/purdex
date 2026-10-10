package promptq

import (
	"testing"
	"time"
)

// U3-2 echo pairing: the daemon names the client_msg_id of a request on the transcript's user item.

func ranRequest(t *testing.T, q *Queue, sid, id, text string, outcome Outcome) {
	t.Helper()
	ch := submitAsync(q, sid, id, text)
	j := next(t, q, "mod1", sid)
	if outcome.Status != "" {
		q.Result("mod1", j.ID, outcome)
	}
	take(t, ch)
}

func TestMatch_AcceptedRequestNamesItsItem(t *testing.T) {
	q, _ := newQ(t)
	ranRequest(t, q, "s1", "cm-1", "hello", Outcome{Status: Accepted})
	now := time.Now()
	got := q.Match("s1", []EchoItem{{Text: "other", At: now}, {Text: "hello", At: now}})
	if got[0] != "" || got[1] != "cm-1" {
		t.Fatalf("got %v", got)
	}
}

// Mutation gate: drop the text test → the first item takes it → red.
func TestMatch_NeedsTheSameSessionTextAndTime(t *testing.T) {
	q, _ := newQ(t)
	ranRequest(t, q, "s1", "cm-1", "hello", Outcome{Status: Accepted})
	now := time.Now()
	for name, it := range map[string]EchoItem{
		"another text":           {Text: "hello!", At: now},
		"a row from long before": {Text: "hello", At: now.Add(-time.Minute)},
		"a row from long after":  {Text: "hello", At: now.Add(time.Minute)},
	} {
		if got := q.Match("s1", []EchoItem{it}); got[0] != "" {
			t.Errorf("%s paired: %v", name, got)
		}
	}
	if got := q.Match("s2", []EchoItem{{Text: "hello", At: now}}); got[0] != "" {
		t.Errorf("another session paired: %v", got)
	}
}

// A request that did not run (dropped, busy) names nothing; one that may have (unknown) does.
func TestMatch_OnlyRequestsThatRanOrMayHave(t *testing.T) {
	q, _ := newQ(t)
	ranRequest(t, q, "s1", "cm-drop", "a", Outcome{Status: Dropped, Reason: "x"})
	ranRequest(t, q, "s1", "cm-busy", "b", Outcome{Status: Busy})
	ch := submitAsync(q, "s1", "cm-unknown", "c")
	next(t, q, "mod1", "s1") // handed out, no result: unknown after the hand timeout
	take(t, ch)
	now := time.Now()
	got := q.Match("s1", []EchoItem{{Text: "a", At: now}, {Text: "b", At: now}, {Text: "c", At: now}})
	if got[0] != "" || got[1] != "" || got[2] != "cm-unknown" {
		t.Fatalf("got %v", got)
	}
}

// Two identical messages sent close together each get their own id; one request names one item only.
func TestMatch_IdenticalMessagesPairOneToOne(t *testing.T) {
	q, _ := newQ(t)
	q.Wait, q.HandTimeout = 2*time.Second, time.Second
	ranRequest(t, q, "s1", "cm-1", "again", Outcome{Status: Accepted})
	time.Sleep(300 * time.Millisecond)
	ranRequest(t, q, "s1", "cm-2", "again", Outcome{Status: Accepted})
	now := time.Now()
	got := q.Match("s1", []EchoItem{{Text: "again", At: now.Add(-300 * time.Millisecond)}, {Text: "again", At: now}})
	if got[0] != "cm-1" || got[1] != "cm-2" {
		t.Fatalf("got %v", got)
	}
	// a third item of the same text, with no third request, has no id
	got = q.Match("s1", []EchoItem{{Text: "again", At: now.Add(-300 * time.Millisecond)}, {Text: "again", At: now}, {Text: "again", At: now}})
	if got[2] != "" && got[0] != "" && got[1] != "" {
		t.Fatalf("three items took two ids: %v", got)
	}
	n := 0
	for _, id := range got {
		if id != "" {
			n++
		}
	}
	if n != 2 {
		t.Fatalf("paired %d of 3 items with 2 requests: %v", n, got)
	}
}

// The pairing is a function of the items and the ledger: asking twice gives the same answer.
func TestMatch_IsDeterministic(t *testing.T) {
	q, _ := newQ(t)
	ranRequest(t, q, "s1", "cm-1", "x", Outcome{Status: Accepted})
	items := []EchoItem{{Text: "x", At: time.Now()}}
	a, b := q.Match("s1", items), q.Match("s1", items)
	if a[0] != b[0] || a[0] != "cm-1" {
		t.Fatalf("%v %v", a, b)
	}
}
