package execstat

import (
	"context"
	"os/exec"
	"testing"
	"time"
)

func lookTrue(t *testing.T) string {
	p, err := exec.LookPath("true")
	if err != nil {
		t.Skip("no true")
	}
	return p
}

func find(t *testing.T, tag string) TagStat {
	t.Helper()
	for _, s := range Tags() {
		if s.Tag == tag {
			return s
		}
	}
	t.Fatalf("no tag %q in %v", tag, Tags())
	return TagStat{}
}

func TestCommandCountsRunOutputCombinedAndStartWait(t *testing.T) {
	ResetTags()
	tr := lookTrue(t)
	if err := Command("t.run", tr).Run(); err != nil {
		t.Fatal(err)
	}
	if _, err := Command("t.out", tr).Output(); err != nil {
		t.Fatal(err)
	}
	if _, err := Command("t.comb", tr).CombinedOutput(); err != nil {
		t.Fatal(err)
	}
	c := CommandContext(context.Background(), "t.sw", tr)
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	if got := find(t, "t.sw"); got.Total != 1 {
		t.Fatalf("a started command counts at once: %+v", got)
	}
	if err := c.Wait(); err != nil {
		t.Fatal(err)
	}
	for _, tag := range []string{"t.run", "t.out", "t.comb", "t.sw"} {
		if got := find(t, tag); got.Total != 1 || got.Last1 != 1 || got.Last5 != 1 || got.Last60 != 1 || got.TotalMS < 0 {
			t.Fatalf("%s: %+v", tag, got)
		}
	}
}

func TestAFailedAndAMissingCommandAreCountedToo(t *testing.T) {
	ResetTags()
	if err := Command("t.fail", "false").Run(); err == nil {
		t.Skip("false succeeded?")
	}
	if err := Command("t.missing", "/nonexistent/binary-x").Run(); err == nil {
		t.Fatal("expected an error")
	}
	if find(t, "t.fail").Total != 1 || find(t, "t.missing").Total != 1 {
		t.Fatalf("%+v", Tags())
	}
}

func TestSetupOfTheEmbeddedCmdSurvives(t *testing.T) {
	ResetTags()
	c := Command("t.env", "sh", "-c", "printf %s \"$X\"")
	c.Env = []string{"X=hello"}
	out, err := c.Output()
	if err != nil || string(out) != "hello" {
		t.Fatalf("out %q err %v", out, err)
	}
	if c.Tag() != "t.env" {
		t.Fatal("tag")
	}
}

func TestWindowsFollowTheClock(t *testing.T) {
	ResetTags()
	base := time.Date(2026, 10, 16, 3, 0, 30, 0, time.UTC)
	cur := base
	now = func() time.Time { return cur }
	defer func() { now = time.Now }()

	Observe("w", time.Millisecond) // t0
	cur = base.Add(20 * time.Second)
	Observe("w", time.Millisecond)
	cur = base.Add(50 * time.Second)
	Observe("w", time.Millisecond)
	got := find(t, "w")
	if got.Total != 3 || got.Last1 != 3 {
		t.Fatalf("within a minute: %+v", got)
	}
	cur = base.Add(70 * time.Second) // the first is 70 s old
	got = find(t, "w")
	if got.Last1 != 2 || got.Total != 3 {
		t.Fatalf("after 70 s: %+v", got)
	}
	cur = base.Add(4 * time.Minute) // all three are in the last 5 minute buckets, none in the last second ring
	got = find(t, "w")
	if got.Last1 != 0 || got.Last5 != 3 || got.Last60 != 3 {
		t.Fatalf("after 4 min: %+v", got)
	}
	cur = base.Add(10 * time.Minute)
	got = find(t, "w")
	if got.Last5 != 0 || got.Last60 != 3 {
		t.Fatalf("after 10 min: %+v", got)
	}
	cur = base.Add(2 * time.Hour)
	got = find(t, "w")
	if got.Last60 != 0 || got.Total != 3 {
		t.Fatalf("after 2 h: %+v", got)
	}
	// a slot reused after the ring came round does not keep the old count
	cur = base.Add(2*time.Hour + time.Second)
	Observe("w", time.Millisecond)
	got = find(t, "w")
	if got.Last1 != 1 || got.Total != 4 {
		t.Fatalf("reused slot: %+v", got)
	}
}

func TestTagsAreSortedAndDurationsAdd(t *testing.T) {
	ResetTags()
	Observe("b", 2*time.Millisecond)
	Observe("a", time.Millisecond)
	Observe("b", 3*time.Millisecond)
	tags := Tags()
	if len(tags) != 2 || tags[0].Tag != "a" || tags[1].Tag != "b" {
		t.Fatalf("%+v", tags)
	}
	if tags[1].Total != 2 || tags[1].TotalMS != 5 {
		t.Fatalf("%+v", tags[1])
	}
}

// A caller that starts the embedded Cmd itself (the pty library) still gets the fork counted and its wall time added at Wait.
func TestNoteStartedCountsAndWaitAddsTheTime(t *testing.T) {
	ResetTags()
	c := Command("t.pty", "sleep", "0.15")
	if err := c.Cmd.Start(); err != nil {
		t.Skip("no sleep")
	}
	c.NoteStarted()
	if got := find(t, "t.pty"); got.Total != 1 {
		t.Fatalf("%+v", got)
	}
	if err := c.Wait(); err != nil {
		t.Fatal(err)
	}
	if got := find(t, "t.pty"); got.Total != 1 || got.TotalMS < 100 {
		t.Fatalf("the time of the wait is added: %+v", got)
	}
}
