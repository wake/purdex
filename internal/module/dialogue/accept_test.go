package dialogue

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// T12 (PR 2 acceptance) through the real module: a summary, then a recap, then a compaction; the new segment opens with
// the recap carried over, and the turn after the boundary gets its own summary in the new segment.
func TestAccept_RecapCarriedIntoTheSegmentAfterACompaction(t *testing.T) {
	f := newFlow(t, true, true)
	now := time.Now().UnixMilli()
	t1 := ended("t1")
	f.setTurns(t1)
	f.stop(now + 1000)
	f.answer(f.job(), haikuJSON)
	waitFor(t, "the summary block", func() bool { return strings.Contains(f.seg(), "<turn-summary 1") })
	if got := f.m.requestRecap(sidS, "manual"); got != "queued" {
		t.Fatalf("request: %s", got)
	}
	f.answer(f.job(), goodRecap)
	waitFor(t, "the recap block", func() bool { return strings.Contains(f.seg(), "<recap ") })

	t2 := done("t2", compactItem("t2-c", "auto"), userItem("t2-u", "after the boundary"), agentItem("t2-a", "answer t2"))
	f.setTurns(t1, t2)
	f.stop(now + 2000)
	f.answer(f.job(), haikuJSON)

	second := filepath.Join(f.dir, "dialogue", sidS, "002.md")
	read := func() string { b, _ := os.ReadFile(second); return string(b) }
	waitFor(t, "the summary of t2 in the second segment", func() bool { return strings.Contains(read(), "<turn-summary 2") })
	got := read()
	carried := strings.Index(got, "<carried recap ")
	body := strings.Index(got, "user: after the boundary")
	if carried < 0 || body < 0 || carried > body {
		t.Fatalf("the second segment does not open with the carried recap:\n%s", got)
	}
	if !strings.Contains(got, "headline: h") {
		t.Errorf("the carried recap lost its text:\n%s", got)
	}
	first := f.seg()
	if !strings.Contains(first, "<compacted | auto>") || !strings.Contains(first, "<next: "+second+">") {
		t.Errorf("the first segment does not close at the boundary:\n%s", first)
	}
}
