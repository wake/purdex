package execstat

import (
	"bytes"
	"context"
	"os/exec"
	"strconv"
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

func find(t *testing.T, tag string) Series {
	t.Helper()
	for _, s := range Tags() {
		if s.Name == tag {
			return s
		}
	}
	t.Fatalf("no tag %q in %v", tag, Tags())
	return Series{}
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
		if got := find(t, tag); got.Total != 1 || got.Last1 != 1 || got.Last5 != 1 || got.Last60 != 1 || got.StartFailed != 0 {
			t.Fatalf("%s: %+v", tag, got)
		}
	}
}

// A command that started and then failed is a fork; one that could not be started is a failed start, not a fork.
func TestAFailedExitIsAForkAndAMissingBinaryIsAFailedStart(t *testing.T) {
	ResetTags()
	if err := Command("t.fail", "false").Run(); err == nil {
		t.Skip("false succeeded?")
	}
	if err := Command("t.missing", "/nonexistent/binary-x").Run(); err == nil {
		t.Fatal("expected an error")
	}
	if _, err := Command("t.missing", "/nonexistent/binary-x").Output(); err == nil {
		t.Fatal("expected an error")
	}
	if err := Command("t.missing", "/nonexistent/binary-x").Start(); err == nil {
		t.Fatal("expected an error")
	}
	if got := find(t, "t.fail"); got.Total != 1 || got.StartFailed != 0 {
		t.Fatalf("t.fail %+v", got)
	}
	if got := find(t, "t.missing"); got.Total != 0 || got.StartFailed != 3 {
		t.Fatalf("t.missing %+v", got)
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
	if len(tags) != 2 || tags[0].Name != "a" || tags[1].Name != "b" {
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

func TestEventsGaugesAndTheirReset(t *testing.T) {
	ResetTags()
	Event("hook.PdxPreToolUse")
	Event("hook.PdxPreToolUse")
	Event("peers-envelope")
	SetGauge("sessions", 15)
	SetGauge("sessions", 16)
	SetGaugeText("resources-mode", "measure")
	ev := Events()
	if len(ev) != 2 || ev[0].Name != "hook.PdxPreToolUse" || ev[0].Total != 2 || ev[0].Last1 != 2 || ev[1].Name != "peers-envelope" || ev[1].Total != 1 {
		t.Fatalf("events %+v", ev)
	}
	g := Gauges()
	if len(g) != 2 || g[0].Name != "resources-mode" || g[0].Text != "measure" || g[1].Name != "sessions" || g[1].Value != 16 || g[1].At.IsZero() {
		t.Fatalf("gauges %+v", g)
	}
	if len(Tags()) != 0 {
		t.Fatalf("events are not forks: %+v", Tags())
	}
	ResetTags()
	if len(Events()) != 0 || len(Gauges()) != 0 {
		t.Fatal("reset")
	}
}

// A clock that goes back clears the recent counts (NTP, a manual change, a resume): they are not counts of the past any more.
// The total survives.
func TestAClockThatGoesBackStartsTheRecentCountsAgain(t *testing.T) {
	ResetTags()
	base := time.Date(2026, 10, 16, 3, 0, 0, 0, time.UTC)
	cur := base
	now = func() time.Time { return cur }
	defer func() { now = time.Now }()

	Observe("c", time.Millisecond)
	Observe("c", time.Millisecond)
	cur = base.Add(10 * time.Minute)
	Observe("c", time.Millisecond)
	if got := find(t, "c"); got.Total != 3 || got.Last60 != 3 {
		t.Fatalf("before the step back: %+v", got)
	}
	cur = base.Add(-2*time.Hour - 17*time.Second) // the clock is set back (by an amount that is not a whole ring)
	got := find(t, "c")
	if got.Total != 3 || got.Last1 != 0 || got.Last5 != 0 || got.Last60 != 0 {
		t.Fatalf("after: %+v", got)
	}
	Observe("c", time.Millisecond)
	cur = base.Add(-2*time.Hour - 12*time.Second)
	got = find(t, "c")
	if got.Total != 4 || got.Last1 != 1 || got.Last60 != 1 {
		t.Fatalf("counting again from the new time: %+v", got)
	}
	// it comes back to the time the old counts were stamped for: they are not resurrected (the one count since is 2 h old now)
	cur = base
	got = find(t, "c")
	if got.Last60 != 0 || got.Total != 4 {
		t.Fatalf("old buckets must not return: %+v", got)
	}
}

// A command that is still running is already a fork: Run, Output and CombinedOutput count at Start, not when the child ends.
func TestRunOutputCombinedCountWhileTheChildIsStillRunning(t *testing.T) {
	sleep, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip("no sleep")
	}
	for name, run := range map[string]func(c *Cmd){
		"run":      func(c *Cmd) { c.Run() },
		"output":   func(c *Cmd) { c.Output() },
		"combined": func(c *Cmd) { c.CombinedOutput() },
	} {
		t.Run(name, func(t *testing.T) {
			ResetTags()
			c := Command("t.long", sleep, "5")
			done := make(chan struct{})
			go func() { run(c); close(done) }()
			deadline := time.Now().Add(3 * time.Second)
			for time.Now().Before(deadline) {
				for _, s := range Tags() {
					if s.Name == "t.long" && s.Total == 1 {
						c.Process.Kill()
						<-done
						return
					}
				}
				time.Sleep(5 * time.Millisecond)
			}
			c.Process.Kill()
			<-done
			t.Fatal("the fork was not counted while the child was running")
		})
	}
}

// Output and CombinedOutput keep the stdlib's behaviour: the output comes back, a failing child's stderr is in the ExitError, and
// a stream that was already set is refused.
func TestOutputKeepsTheStdlibSemantics(t *testing.T) {
	ResetTags()
	out, err := Command("t.o", "sh", "-c", "echo out; echo err >&2").Output()
	if err != nil || string(out) != "out\n" {
		t.Fatalf("Output = %q, %v", out, err)
	}
	_, err = Command("t.o", "sh", "-c", "echo boom >&2; exit 3").Output()
	ee, ok := err.(*exec.ExitError)
	if !ok || string(ee.Stderr) != "boom\n" {
		t.Fatalf("want an ExitError carrying stderr, got %#v", err)
	}
	both, err := Command("t.o", "sh", "-c", "echo out; echo err >&2").CombinedOutput()
	if err != nil || len(both) != len("out\nerr\n") {
		t.Fatalf("CombinedOutput = %q, %v", both, err)
	}
	c := Command("t.o", "true")
	c.Stdout = &bytes.Buffer{}
	if _, err := c.Output(); err == nil {
		t.Fatal("Output with Stdout already set must fail")
	}
	if got := find(t, "t.o"); got.Total != 3 {
		t.Fatalf("a refused Output starts nothing: %+v", got)
	}
}

// A failing child with far more stderr than the cap: the ExitError carries the same head, marker and tail as the standard
// library's, so the reason at the end of a long message survives (differential against exec.Cmd.Output).
func TestOutputLongStderrMatchesTheStdlib(t *testing.T) {
	script := "i=0; while [ $i -lt 3000 ]; do echo line-$i-xxxxxxxxxxxxxxxxxxxxxxxx >&2; i=$((i+1)); done; echo THE-REASON >&2; exit 2"
	_, want := exec.Command("sh", "-c", script).Output()
	_, got := Command("t.long-stderr", "sh", "-c", script).Output()
	we, ok1 := want.(*exec.ExitError)
	ge, ok2 := got.(*exec.ExitError)
	if !ok1 || !ok2 {
		t.Fatalf("want ExitErrors, got %v / %v", want, got)
	}
	if len(we.Stderr) < 64<<10 || !bytes.HasSuffix(ge.Stderr, []byte("THE-REASON\n")) || !bytes.Equal(ge.Stderr, we.Stderr) {
		t.Fatalf("stderr differs from the standard library's: %d bytes vs %d, tail %q", len(ge.Stderr), len(we.Stderr), ge.Stderr[max(0, len(ge.Stderr)-30):])
	}
}

// headTail against the same writes in the standard library's saver, for the three shapes a stream takes: small chunks, one huge
// write, and a write that straddles the end of the head.
func TestHeadTailWriterShapes(t *testing.T) {
	mk := func(n int, off byte) []byte {
		b := make([]byte, n)
		for i := range b {
			b[i] = 'a' + byte(i%26) + off%3
		}
		return b
	}
	for name, writes := range map[string][][]byte{
		"small":     {mk(10, 0), mk(10, 1), mk(7, 2)},
		"huge":      {mk(5000, 0)},
		"straddle":  {mk(60, 0), mk(200, 1), mk(300, 2)},
		"huge-many": {mk(40, 0), mk(900, 1), mk(25, 2), mk(1000, 0)},
	} {
		var out bytes.Buffer
		ht := &headTail{limit: 100}
		for _, w := range writes {
			ht.Write(w)
		}
		got := ht.Bytes()
		// reference: head 100, tail 100 of the concatenation, the rest counted
		var all []byte
		for _, w := range writes {
			all = append(all, w...)
		}
		if len(all) <= 200 {
			out.Write(all)
		} else {
			out.Write(all[:100])
			out.WriteString("\n... omitting " + strconv.Itoa(len(all)-200) + " bytes ...\n")
			out.Write(all[len(all)-100:])
		}
		if !bytes.Equal(got, out.Bytes()) {
			t.Errorf("%s: got %q\nwant %q", name, got, out.Bytes())
		}
	}
}

// ObserveForks adds the forks a remote process (the pdx hook CLI) reports: n of them under the tag, nothing for n < 1, and no
// more than maxReportedForks for one report so a client cannot swell a tag.
func TestObserveForksCountsAReportAndBoundsIt(t *testing.T) {
	ResetTags()
	t.Cleanup(ResetTags)
	total := func() int64 {
		for _, s := range Tags() {
			if s.Name == "hook-cli" {
				return s.Total
			}
		}
		return 0
	}
	ObserveForks("hook-cli", 3)
	if got := total(); got != 3 {
		t.Fatalf("after a report of 3: %d", got)
	}
	ObserveForks("hook-cli", 0)
	ObserveForks("hook-cli", -5)
	if got := total(); got != 3 {
		t.Fatalf("a report of 0 or less must add nothing: %d", got)
	}
	ObserveForks("hook-cli", 1_000_000)
	if got := total(); got != 3+maxReportedForks {
		t.Fatalf("a huge report must be cut to %d: total %d", maxReportedForks, got)
	}
}

// A caller that starts the embedded Cmd itself reports the outcome: NoteStarted counts a fork, NoteStartFailed a failed start.
func TestNoteStartFailedCountsAFailedStart(t *testing.T) {
	ResetTags()
	c := Command("t.pty", "x")
	c.NoteStartFailed()
	c.NoteStartFailed()
	c.NoteStarted()
	if got := find(t, "t.pty"); got.Total != 1 || got.StartFailed != 2 {
		t.Fatalf("want 1 fork and 2 failed starts, got %+v", got)
	}
}
