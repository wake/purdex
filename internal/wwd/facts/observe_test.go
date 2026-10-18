package facts

import (
	"strings"
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

var ot0 = time.Date(2026, 10, 18, 14, 0, 0, 0, time.UTC)

// host is a small machine the tracker watches: a Claude Code session (pid 100, ref _sess01), a codex harness reparented to pid 1
// (broker 700 -> wrapper 710 -> codex 720 -> code-mode-host 730) whose environment names session _env999, and whatever commands
// a test adds. Processes are described once; each sample sets their CPU and memory.
type host struct {
	procs []ProcRec
	args  map[int][]string
	jobs  map[int]string
}

func newHost() *host {
	h := &host{args: map[int][]string{}, jobs: map[int]string{}}
	h.add(1, 0, "launchd", nil)
	h.add(100, 1, "claude", []string{"claude"})
	h.add(700, 1, "node", []string{"node", "/home/u/.claude/plugins/cache/openai-codex/codex/1.0.2/scripts/app-server-broker.mjs", "serve"})
	h.add(710, 700, "node", []string{"node", "/usr/lib/node_modules/@openai/codex/bin/codex.js", "app-server"})
	h.add(720, 710, "codex", []string{"codex", "app-server"})
	h.add(730, 720, "codex-code-mode-host", []string{"codex-code-mode-host"})
	return h
}

func (h *host) add(pid, ppid int, name string, argv []string) *ProcRec {
	h.procs = append(h.procs, ProcRec{PID: pid, PPID: ppid, UID: 501, Name: name, FootprintBytes: mb(5)})
	h.args[pid] = argv
	return &h.procs[len(h.procs)-1]
}

func (h *host) rec(pid int) *ProcRec {
	for i := range h.procs {
		if h.procs[i].PID == pid {
			return &h.procs[i]
		}
	}
	return nil
}

func (h *host) remove(pids ...int) {
	keep := h.procs[:0]
	for _, p := range h.procs {
		drop := false
		for _, d := range pids {
			drop = drop || p.PID == d
		}
		if !drop {
			keep = append(keep, p)
		}
	}
	h.procs = keep
}

func (h *host) snapshot() (ProcSnapshot, ProcContext) {
	snap := ProcSnapshot{Procs: append([]ProcRec(nil), h.procs...)}
	ctx := ProcContext{
		JobOf: func(pid int) (string, bool) {
			j, ok := h.jobs[pid]
			return j, ok
		},
		SessionOf: func(pid int) (string, bool) { return "_sess01", pid == 100 },
		ArgsOf:    func(pid int) []string { return h.args[pid] },
		EnvSessionOf: func(pid int) (string, bool) {
			return "_env999", pid == 700
		},
	}
	return snap, ctx
}

func newTracker() *Tracker {
	tr := NewTracker(ObserveConfig{Host: "mlab", MinCPUSeconds: 60, MinMemMB: 1024, Interval: 5 * time.Second})
	tr.ident = func(int) (string, bool) { return "", false } // the fake pids are not real processes
	return tr
}

// gone feeds the two samples without the tree that it takes to end one: the first must end nothing (a read can miss a process), the
// second returns what ended.
func gone(t *testing.T, tr *Tracker, h *host, n int) []wire.Observation {
	t.Helper()
	if early := step(tr, h, n); len(early) != 0 {
		t.Fatalf("one sample without the tree must not end it: %+v", early)
	}
	return step(tr, h, n+1)
}

// step feeds the tracker one sample n*5 s after ot0.
func step(tr *Tracker, h *host, n int) []wire.Observation {
	snap, ctx := h.snapshot()
	return tr.Update(ot0.Add(time.Duration(n)*5*time.Second), snap, ctx)
}

func TestTrackerRecordsAHeavyCommandUnderASessionWhenItEnds(t *testing.T) {
	h, tr := newHost(), newTracker()
	h.add(200, 100, "pnpm", []string{"pnpm", "vitest", "run", "--token", "hunter2"})
	h.add(201, 200, "node", []string{"node", "vitest"})
	h.add(202, 200, "node", []string{"node", "vitest-worker"})
	var got []wire.Observation
	for n := 0; n < 20; n++ { // 20 samples = 100 s; the tree uses 300% CPU (3 cores) = 3 CPU seconds a second
		h.rec(200).CPUPct, h.rec(201).CPUPct, h.rec(202).CPUPct = 10, 150, 140
		h.rec(201).FootprintBytes, h.rec(202).FootprintBytes = mb(400), mb(300)
		got = append(got, step(tr, h, n)...)
	}
	if len(got) != 0 {
		t.Fatalf("still running: nothing recorded yet, got %+v", got)
	}
	if live := tr.Live(); len(live) != 1 || live[0].EndedAt != nil {
		t.Fatalf("a running tree over the threshold is listed live: %+v", live)
	}
	h.remove(200, 201, 202)
	got = gone(t, tr, h, 20)
	if len(got) != 1 {
		t.Fatalf("want one observation, got %+v", got)
	}
	o := got[0]
	if o.Host != "mlab" || o.Session != "_sess01" || o.Via != wire.ObsViaSession || o.Signature != "pnpm vitest run" {
		t.Fatalf("identity: %+v", o)
	}
	if strings.Contains(o.Command, "hunter2") || !strings.Contains(o.Command, "--token <redacted>") {
		t.Fatalf("the command must be redacted: %q", o.Command)
	}
	// 20 samples x 5 s x 3.0 cores = 300 CPU seconds
	if o.CPUSeconds < 295 || o.CPUSeconds > 305 || o.CPUPeak != 300 || o.MemPeakMB != 705 || o.ProcsPeak != 3 {
		t.Fatalf("figures: %+v", o)
	}
	if o.EndedAt == nil || !o.EndedAt.Equal(ot0.Add(19*5*time.Second)) || !o.StartedAt.Equal(ot0) {
		t.Fatalf("times: start %v end %v", o.StartedAt, o.EndedAt)
	}
	if err := o.Validate(); err != nil {
		t.Fatal(err)
	}
	if len(tr.Live()) != 0 {
		t.Error("it is not live any more")
	}
}

func TestTrackerIgnoresATreeBelowBothThresholds(t *testing.T) {
	h, tr := newHost(), newTracker()
	h.add(200, 100, "go", []string{"go", "vet", "./..."})
	for n := 0; n < 4; n++ {
		h.rec(200).CPUPct, h.rec(200).FootprintBytes = 100, mb(200) // 1 core for 20 s = 20 CPU seconds, 200 MB
		step(tr, h, n)
	}
	h.remove(200)
	if got := step(tr, h, 4); len(got) != 0 {
		t.Fatalf("under the thresholds: %+v", got)
	}
	if len(tr.Live()) != 0 {
		t.Fatal("nothing live")
	}
}

func TestTrackerRecordsATreeThatHoldsALotOfMemory(t *testing.T) {
	h, tr := newHost(), newTracker()
	h.add(200, 100, "swift-frontend", []string{"swift-frontend", "-c"})
	h.rec(200).FootprintBytes = mb(1500)
	step(tr, h, 0)
	h.remove(200)
	got := gone(t, tr, h, 1)
	if len(got) != 1 || got[0].MemPeakMB != 1500 || got[0].CPUSeconds != 0 {
		t.Fatalf("memory alone is enough: %+v", got)
	}
}

// codex: the broker was reparented to pid 1, its harness (wrapper, codex, code-mode-host) is not recorded, and a command under it is
// its own tree, the session's being the environment's.
func TestTrackerRecordsACodexCommandViaCodexAndSkipsTheHarness(t *testing.T) {
	h, tr := newHost(), newTracker()
	for _, pid := range []int{700, 710, 720, 730} {
		h.rec(pid).CPUPct, h.rec(pid).FootprintBytes = 30, mb(1100) // each a big process: still harness, never a tree
	}
	h.add(800, 720, "zsh", []string{"zsh", "-c", "cd /x && pnpm vitest run"})
	h.add(801, 800, "node", []string{"node", "vitest"})
	for n := 0; n < 8; n++ {
		h.rec(801).CPUPct, h.rec(801).FootprintBytes = 400, mb(600)
		step(tr, h, n)
	}
	// while it runs the only live tree over a threshold is the command's: the big harness processes are never trees
	if live := tr.Live(); len(live) != 1 || live[0].Via != wire.ObsViaCodex || live[0].Signature != "pnpm vitest run" {
		t.Fatalf("live: %+v", live)
	}
	h.remove(800, 801)
	got := gone(t, tr, h, 8)
	if len(got) != 1 {
		t.Fatalf("want the command's tree only, got %+v", got)
	}
	o := got[0]
	if o.Via != wire.ObsViaCodex || o.Session != "_env999" || o.Signature != "pnpm vitest run" {
		t.Fatalf("codex command: %+v", o)
	}
}

// A shell between the session and codex must not swallow codex's commands into one big tree, nor count them twice.
func TestTrackerDoesNotCountACodexCommandInsideTheOuterTree(t *testing.T) {
	h, tr := newHost(), newTracker()
	h.add(300, 100, "bash", []string{"bash", "-c", "codex exec run it"}) // a command the session ran, which runs codex
	h.add(310, 300, "codex", []string{"codex", "exec", "run it"})
	h.add(320, 310, "node", []string{"node", "vitest"}) // codex's command
	for n := 0; n < 20; n++ {
		h.rec(300).CPUPct, h.rec(310).CPUPct, h.rec(320).CPUPct = 5, 20, 300
		step(tr, h, n)
	}
	live := tr.Live()
	var viaCodex, viaSession []wire.Observation
	for _, o := range live {
		if o.Via == wire.ObsViaCodex {
			viaCodex = append(viaCodex, o)
		} else {
			viaSession = append(viaSession, o)
		}
	}
	if len(viaCodex) != 1 || viaCodex[0].CPUPeak != 300 {
		t.Fatalf("codex's command is its own tree: %+v", live)
	}
	for _, o := range viaSession {
		if o.CPUPeak >= 300 {
			t.Fatalf("the outer tree counted codex's command too: %+v", o)
		}
	}
}

func TestTrackerSkipsJobsResidentsAndStrangers(t *testing.T) {
	h, tr := newHost(), newTracker()
	h.add(200, 100, "pnpm", []string{"pnpm", "test"}) // a job's tree: carries the marker
	h.add(201, 200, "node", []string{"node", "vitest"})
	h.jobs[200] = "j_01JC"
	h.add(210, 100, "node", []string{"node", "/x/node_modules/.bin/vite", "--port", "5174"}) // a dev server: a resident
	h.add(220, 1, "Safari", []string{"Safari"})                                              // belongs to no session
	for n := 0; n < 20; n++ {
		for _, pid := range []int{200, 201, 210, 220} {
			h.rec(pid).CPUPct, h.rec(pid).FootprintBytes = 200, mb(900)
		}
		step(tr, h, n)
	}
	if live := tr.Live(); len(live) != 0 {
		t.Fatalf("none of these is an undeclared command: %+v", live)
	}
}

// A job or a resident started below a command is not the command's: its subtree stays out of the totals.
func TestTrackerLeavesJobAndResidentSubtreesOutOfACommandsTotals(t *testing.T) {
	h, tr := newHost(), newTracker()
	h.add(200, 100, "zsh", []string{"zsh", "-c", "make all"}) // the command, small itself
	h.add(201, 200, "node", []string{"node", "worker.js"})    // a job it started
	h.jobs[201] = "j_01JC"
	h.add(202, 201, "node", []string{"node", "job-child.js"})
	h.add(210, 200, "node", []string{"node", "/x/node_modules/.bin/vite", "--port", "5174"}) // a dev server: a resident
	h.add(211, 210, "node", []string{"node", "esbuild"})
	for n := 0; n < 20; n++ {
		for _, pid := range []int{201, 202, 210, 211} {
			h.rec(pid).CPUPct, h.rec(pid).FootprintBytes = 200, mb(900)
		}
		step(tr, h, n)
	}
	if live := tr.Live(); len(live) != 0 {
		t.Fatalf("the heavy processes belong to a job and a resident, not to the command: %+v", live)
	}
}

// A sample that failed to read (empty) ends nothing, and one sample without the root is not the end of its tree.
func TestTrackerSurvivesAnEmptyOrPartialSample(t *testing.T) {
	h, tr := newHost(), newTracker()
	h.add(200, 100, "swift-frontend", []string{"swift-frontend", "-c"})
	h.rec(200).FootprintBytes = mb(1500)
	step(tr, h, 0)
	if got := tr.Update(ot0.Add(5*time.Second), ProcSnapshot{}, ProcContext{}); len(got) != 0 {
		t.Fatalf("an empty read ended something: %+v", got)
	}
	rec := *h.rec(200)
	h.remove(200)
	if got := step(tr, h, 2); len(got) != 0 { // the root missing once
		t.Fatalf("one miss ended the tree: %+v", got)
	}
	h.procs = append(h.procs, rec) // it was only a missed read
	h.rec(200).FootprintBytes = mb(1500)
	if got := step(tr, h, 3); len(got) != 0 {
		t.Fatalf("%+v", got)
	}
	h.remove(200)
	step(tr, h, 4)
	got := step(tr, h, 5)
	if len(got) != 1 || !got[0].StartedAt.Equal(ot0) {
		t.Fatalf("one tree from the first sample to the end, got %+v", got)
	}
}

// A long command outlives the shell that started it: its root goes to pid 1 and is still the same tree.
func TestTrackerFollowsARootThatWasReparentedToPid1(t *testing.T) {
	h, tr := newHost(), newTracker()
	h.add(200, 100, "make", []string{"make", "build"})
	for n := 0; n < 3; n++ {
		h.rec(200).CPUPct = 100
		step(tr, h, n)
	}
	h.rec(200).PPID = 1 // the shell above it exited
	for n := 3; n < 20; n++ {
		h.rec(200).CPUPct = 100
		step(tr, h, n)
	}
	h.remove(200)
	got := gone(t, tr, h, 20)
	if len(got) != 1 || got[0].CPUSeconds < 90 || got[0].Signature != "make build" || !got[0].StartedAt.Equal(ot0) {
		t.Fatalf("both phases belong to one record, got %+v", got)
	}
}

// A harness child with no session to name is recorded as 'orphan', not dropped.
func TestTrackerRecordsAnUnnamedCodexChildAsOrphan(t *testing.T) {
	h, tr := newHost(), newTracker()
	h.add(900, 1, "node", []string{"node", "/usr/lib/node_modules/@openai/codex/bin/codex.js", "app-server"}) // another harness, no env session
	h.add(910, 900, "swift", []string{"swift", "build"})
	for n := 0; n < 3; n++ {
		h.rec(910).CPUPct, h.rec(910).FootprintBytes = 100, mb(2000)
		step(tr, h, n)
	}
	live := tr.Live()
	if len(live) != 1 || live[0].Session != "orphan" || live[0].Via != wire.ObsViaCodex {
		t.Fatalf("%+v", live)
	}
}

// A pid reused by another program is another tree, not a continuation.
func TestTrackerTreatsAReusedPIDAsANewTree(t *testing.T) {
	h, tr := newHost(), newTracker()
	h.add(200, 100, "make", []string{"make", "build"})
	h.rec(200).FootprintBytes = mb(1500)
	step(tr, h, 0)
	h.rec(200).Name, h.args[200] = "ruby", []string{"ruby", "x.rb"} // same pid, a different program, in the very next sample
	h.rec(200).FootprintBytes = mb(10)
	got := gone(t, tr, h, 1)
	if len(got) != 1 || got[0].Signature != "make build" {
		t.Fatalf("the make tree ended: %+v", got)
	}
	if len(tr.Live()) != 0 {
		t.Fatalf("the new, small tree is not over the threshold")
	}
}

// The same name does not make it the same tree: the start identity of the root decides (a node that ends and another node that
// takes its pid).
func TestTrackerTreatsASameNameReusedPIDAsANewTree(t *testing.T) {
	h, tr := newHost(), newTracker()
	started := "t1"
	tr.ident = func(int) (string, bool) { return started, true }
	h.add(200, 100, "node", []string{"node", "build.js"})
	h.rec(200).FootprintBytes = mb(1500)
	step(tr, h, 0)
	started = "t2" // another process holds pid 200 now, also named node
	h.args[200] = []string{"node", "other.js"}
	h.rec(200).FootprintBytes = mb(1500)
	got := step(tr, h, 1)
	if len(got) != 1 || got[0].Signature != "node build.js" || got[0].EndedAt == nil {
		t.Fatalf("the first node tree ended: %+v", got)
	}
	live := tr.Live()
	if len(live) != 1 || live[0].Signature != "node other.js" {
		t.Fatalf("the second one is a tree of its own: %+v", live)
	}
}

// The harness decision belongs to the process, not to its pid and name: a node that took the pid of a codex wrapper (and the
// other way round) is classified again.
func TestTrackerClassifiesAgainWhenASameNamedProcessTakesAPid(t *testing.T) {
	codex := []string{"node", "/usr/lib/node_modules/@openai/codex/bin/codex.js", "app-server"}
	plain := []string{"node", "server.js"}

	h, tr := newHost(), newTracker()
	started := "t1"
	tr.ident = func(int) (string, bool) { return started, true }
	h.add(200, 100, "node", codex)
	h.rec(200).FootprintBytes = mb(1500)
	step(tr, h, 0)
	if len(tr.Live()) != 0 {
		t.Fatal("a codex wrapper is never a tree")
	}
	started, h.args[200] = "t2", plain // an ordinary node holds the pid now
	step(tr, h, 1)
	if live := tr.Live(); len(live) != 1 || live[0].Signature != "node server.js" {
		t.Fatalf("the ordinary node is a command and must not inherit the harness verdict: %+v", live)
	}

	h, tr = newHost(), newTracker()
	started = "t1"
	tr.ident = func(int) (string, bool) { return started, true }
	h.add(200, 100, "node", plain)
	h.rec(200).FootprintBytes = mb(1500)
	step(tr, h, 0)
	started, h.args[200] = "t2", codex // a codex wrapper holds the pid now
	step(tr, h, 1)
	step(tr, h, 2)
	if got := step(tr, h, 3); len(got) != 0 || len(tr.Live()) != 0 {
		t.Fatalf("the wrapper is not the command that ran before: ended %+v live %+v", got, tr.Live())
	}
}

// A verdict made while the start identity could not be read is not carried over to a process whose identity can be read.
func TestTrackerDoesNotCarryAVerdictAcrossAnUnknownIdentity(t *testing.T) {
	h, tr := newHost(), newTracker() // newTracker: the identity is unknown
	h.add(200, 100, "node", []string{"node", "/usr/lib/node_modules/@openai/codex/bin/codex.js", "app-server"})
	h.rec(200).FootprintBytes = mb(1500)
	step(tr, h, 0)
	tr.ident = func(int) (string, bool) { return "t2", true } // now it can be read, and it is another process
	h.args[200] = []string{"node", "server.js"}
	step(tr, h, 1)
	if live := tr.Live(); len(live) != 1 || live[0].Signature != "node server.js" {
		t.Fatalf("%+v", live)
	}
}

// The other way: a verdict made with an identity survives a sample whose identity read failed.
func TestTrackerKeepsAVerdictWhenTheIdentityReadFailsOnce(t *testing.T) {
	h, tr := newHost(), newTracker()
	known := true
	tr.ident = func(int) (string, bool) {
		if !known {
			return "", false
		}
		return "t1", true
	}
	h.add(200, 100, "node", []string{"node", "/usr/lib/node_modules/@openai/codex/bin/codex.js", "app-server"})
	h.rec(200).FootprintBytes = mb(1500)
	step(tr, h, 0)
	known = false
	h.args[200] = nil // the argument read fails in the same sample
	step(tr, h, 1)
	if len(tr.Live()) != 0 {
		t.Fatalf("the codex wrapper became a command: %+v", tr.Live())
	}
}

// ...but only for one sample: when the reads keep failing the process is classified again from what can be read.
func TestTrackerStopsKeepingAVerdictWhenTheIdentityReadKeepsFailing(t *testing.T) {
	h, tr := newHost(), newTracker()
	known := true
	tr.ident = func(int) (string, bool) {
		if !known {
			return "", false
		}
		return "t1", true
	}
	h.add(200, 100, "node", []string{"node", "/usr/lib/node_modules/@openai/codex/bin/codex.js", "app-server"})
	h.rec(200).FootprintBytes = mb(1500)
	step(tr, h, 0)
	known = false
	h.args[200] = []string{"node", "server.js"} // another node took the pid, and the identity cannot be read
	step(tr, h, 1)
	if len(tr.Live()) != 0 {
		t.Fatalf("one failed read keeps the verdict: %+v", tr.Live())
	}
	step(tr, h, 2)
	if live := tr.Live(); len(live) != 1 || live[0].Signature != "node server.js" {
		t.Fatalf("a second failed read in a row classifies again: %+v", live)
	}
}

// With an identity that is never readable nothing is carried from one sample to the next: what can be read decides each time.
func TestTrackerClassifiesEverySampleWhileTheIdentityIsNeverReadable(t *testing.T) {
	h, tr := newHost(), newTracker() // newTracker: the identity is unknown
	h.add(200, 100, "node", []string{"node", "/usr/lib/node_modules/@openai/codex/bin/codex.js", "app-server"})
	h.rec(200).FootprintBytes = mb(1500)
	for n := 0; n < 3; n++ {
		step(tr, h, n)
	}
	if len(tr.Live()) != 0 {
		t.Fatal("a codex wrapper is never a tree")
	}
	h.args[200] = []string{"node", "server.js"} // another node holds the pid
	step(tr, h, 3)
	if live := tr.Live(); len(live) != 1 || live[0].Signature != "node server.js" {
		t.Fatalf("%+v", live)
	}
}

// Every failed read after a good one gets its one sample, not only the first.
func TestTrackerGivesEachSeparateFailedReadItsSample(t *testing.T) {
	h, tr := newHost(), newTracker()
	known := true
	tr.ident = func(int) (string, bool) {
		if !known {
			return "", false
		}
		return "t1", true
	}
	h.add(200, 100, "node", []string{"node", "/usr/lib/node_modules/@openai/codex/bin/codex.js", "app-server"})
	h.rec(200).FootprintBytes = mb(1500)
	step(tr, h, 0)
	known = false
	step(tr, h, 1) // a first failed read
	known = true
	step(tr, h, 2) // recovered
	known = false
	h.args[200] = nil // a second, separate failure that also loses the arguments
	step(tr, h, 3)
	if len(tr.Live()) != 0 {
		t.Fatalf("the second failed read lost the verdict: %+v", tr.Live())
	}
}

// A long gap between samples (the machine slept, wwd was stopped) is not charged as CPU.
func TestTrackerCapsAGapBetweenSamples(t *testing.T) {
	h := newHost()
	tr := NewTracker(ObserveConfig{Host: "mlab", MinCPUSeconds: 1, MinMemMB: 1024, Interval: 5 * time.Second})
	h.add(200, 100, "make", []string{"make"})
	h.rec(200).CPUPct = 100
	step(tr, h, 0)
	step(tr, h, 1000) // 5000 s later
	live := tr.Live()
	if len(live) != 1 {
		t.Fatalf("the tree is over the threshold: %+v", live)
	}
	// the first sample is charged one interval (5 s) and the sample after the gap at most three (15 s): 20 CPU seconds at one core
	if live[0].CPUSeconds > 21 {
		t.Fatalf("a gap of 5000 s charged %v CPU seconds", live[0].CPUSeconds)
	}
}

func TestTrackerBoundsWhatItKeeps(t *testing.T) {
	h := newHost()
	tr := NewTracker(ObserveConfig{Host: "mlab", MinCPUSeconds: 60, MinMemMB: 1024, Interval: 5 * time.Second, MaxLive: 5})
	for i := 0; i < 50; i++ {
		h.add(1000+i, 100, "make", []string{"make", "t"})
		h.rec(1000 + i).FootprintBytes = mb(uint64(2000 + i))
	}
	step(tr, h, 0)
	if n := len(tr.Live()); n > 5 {
		t.Fatalf("kept %d live trees, bound 5", n)
	}
}
