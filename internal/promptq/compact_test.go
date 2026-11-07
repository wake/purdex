package promptq

import (
	"context"
	"testing"
)

// A compact job goes to the prompt owner and the mod's reason (now | turn_end) survives; a submit's does not.
func TestSubmitCompact_KeepsTheModsReason(t *testing.T) {
	q, _ := newQ(t)
	ch := make(chan answer, 1)
	go func() {
		r, err := q.SubmitCompact(context.Background(), "s1")
		ch <- answer{r, err}
	}()
	j := next(t, q, "mod1", "s1")
	if j.Kind != KindCompact || j.SessionID != "s1" {
		t.Fatalf("job = %+v", j)
	}
	if err := q.Result("mod1", j.ID, Outcome{Status: Accepted, Reason: "turn_end"}); err != nil {
		t.Fatal(err)
	}
	if r := take(t, ch); r.Status != Accepted || r.Reason != "turn_end" {
		t.Fatalf("compact result = %+v", r)
	}
	sch := make(chan answer, 1)
	go func() {
		r, err := q.Submit(context.Background(), "s1", "m1", "hi")
		sch <- answer{r, err}
	}()
	j = next(t, q, "mod1", "s1")
	q.Result("mod1", j.ID, Outcome{Status: Accepted, Reason: "x"})
	if r := take(t, sch); r.Status != Accepted || r.Reason != "" {
		t.Fatalf("submit result = %+v, want no reason", r)
	}
}
