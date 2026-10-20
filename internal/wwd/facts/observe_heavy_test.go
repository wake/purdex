package facts

import (
	"strings"
	"testing"
	"time"
)

// A shell root whose script the scrubber masks (here a $( ) has no signature of its own: the tree is named after the heaviest
// program below it.

func ccRoot() []string { return ccWrap(`'H=$(git rev-parse HEAD); go test ./x/'`) }

// runHeavy samples n times with the callback setting the figures, ends the tree and returns its one observation.
func runHeavy(t *testing.T, h *host, tr *Tracker, n int, set func(i int)) (sig, cmd string) {
	t.Helper()
	for i := 0; i < n; i++ {
		set(i)
		step(tr, h, i)
	}
	h.remove(200, 201, 202, 203, 204)
	got := gone(t, tr, h, n)
	if len(got) != 1 {
		t.Fatalf("want one observation, got %+v", got)
	}
	return got[0].Signature, got[0].Command
}

func TestTrackerNamesAMaskedShellRootAfterItsHeaviestProgram(t *testing.T) {
	h, tr := newHost(), newTracker()
	h.add(200, 100, "zsh", ccRoot())
	h.add(201, 200, "git", []string{"git", "rev-parse", "HEAD"})
	h.add(202, 200, "go", []string{"go", "test", "./x/"})
	h.add(203, 202, "compile", []string{"compile", "-o", "/tmp/x.a"})
	sig, cmd := runHeavy(t, h, tr, 20, func(int) {
		h.rec(201).CPUPct, h.rec(202).CPUPct, h.rec(203).CPUPct = 1, 5, 280 // the compiler is the heavy one, but go test owns it
		h.rec(203).FootprintBytes = mb(1500)
	})
	if sig != "go test" {
		t.Errorf("Signature = %q, want go test (the program under the shell, not its helper)", sig)
	}
	if strings.Contains(cmd, "go test") || !strings.Contains(cmd, "redacted") {
		t.Errorf("Command must stay the root's masked line: %q", cmd)
	}
}

func TestTrackerFollowsTheHeaviestProgramOverTime(t *testing.T) {
	h, tr := newHost(), newTracker()
	h.add(200, 100, "zsh", ccRoot())
	h.add(201, 200, "go", []string{"go", "vet", "./..."})
	h.add(202, 200, "pnpm", []string{"pnpm", "vitest", "run"})
	sig, _ := runHeavy(t, h, tr, 20, func(i int) {
		if i < 3 { // vet is first and heavy for a while, vitest then runs much longer
			h.rec(201).CPUPct, h.rec(202).CPUPct = 300, 0
			return
		}
		h.rec(201).CPUPct, h.rec(202).CPUPct = 0, 300
	})
	if sig != "pnpm vitest run" {
		t.Errorf("Signature = %q, want the program with the most CPU in all", sig)
	}
}

func TestTrackerNamesTheProgramBelowANestedShell(t *testing.T) {
	h, tr := newHost(), newTracker()
	h.add(200, 100, "zsh", ccRoot())
	h.add(201, 200, "bash", []string{"bash", "-c", "x=$(date); make test"}) // a shell with a name of its own would be a program
	h.add(202, 201, "make", []string{"make", "test"})
	sig, _ := runHeavy(t, h, tr, 20, func(int) { h.rec(202).CPUPct = 300 })
	if sig != "make test" {
		t.Errorf("Signature = %q, want make test", sig)
	}
}

func TestTrackerLeavesAnAlreadyNamedRootAlone(t *testing.T) {
	h, tr := newHost(), newTracker()
	h.add(200, 100, "pnpm", []string{"pnpm", "vitest", "run"})
	h.add(201, 200, "node", []string{"node", "vitest-worker"})
	sig, _ := runHeavy(t, h, tr, 20, func(int) { h.rec(201).CPUPct = 300 })
	if sig != "pnpm vitest run" {
		t.Errorf("Signature = %q, want the root's own", sig)
	}
	h2, tr2 := newHost(), newTracker()
	h2.add(200, 100, "zsh", ccWrap(`'cd /x && swift build'`)) // a root the parser reads keeps its own signature
	h2.add(201, 200, "go", []string{"go", "test"})
	if sig, _ = runHeavy(t, h2, tr2, 20, func(int) { h2.rec(201).CPUPct = 300 }); sig != "swift build" {
		t.Errorf("Signature = %q, want swift build", sig)
	}
}

func TestTrackerDoesNotCarryTheCPUOfAReusedPIDToTheNewProgram(t *testing.T) {
	h, tr := newHost(), newTracker()
	h.add(200, 100, "zsh", ccRoot())
	h.add(201, 200, "go", []string{"go", "vet"})
	for i := 0; i < 10; i++ {
		h.rec(201).CPUPct = 300
		step(tr, h, i)
	}
	// the pid goes to another program (same name, other argv, no start time to tell them apart by) that then runs longer
	h.args[201] = []string{"go", "test"}
	for i := 10; i < 30; i++ {
		h.rec(201).CPUPct = 300
		step(tr, h, i)
	}
	h.remove(200, 201)
	got := gone(t, tr, h, 30)
	if len(got) != 1 || got[0].Signature != "go test" {
		t.Fatalf("the program that took the pid is a candidate of its own: %+v", got)
	}
	// and the same pid with a new start time is a new process too
	h3, tr3 := newHost(), newTracker()
	h3.add(200, 100, "zsh", ccRoot())
	h3.add(201, 200, "go", []string{"go", "vet"})
	h3.rec(201).Start = ot0
	for i := 0; i < 2; i++ {
		h3.rec(201).CPUPct = 300
		step(tr3, h3, i)
	}
	h3.args[201] = []string{"go", "test"}
	h3.rec(201).Start = ot0.Add(time.Hour)
	for i := 2; i < 30; i++ {
		h3.rec(201).CPUPct = 300
		step(tr3, h3, i)
	}
	h3.remove(200, 201)
	got = gone(t, tr3, h3, 30)
	if len(got) != 1 || got[0].Signature != "go test" {
		t.Fatalf("a reused pid with a new start is a new candidate: %+v", got)
	}
}

func TestTrackerBoundsTheCandidatesOfATree(t *testing.T) {
	h, tr := newHost(), newTracker()
	h.add(200, 100, "zsh", ccRoot())
	for i := 0; i < 100; i++ { // a hundred at once, all running
		h.add(300+i, 200, "tool", []string{"tool", "n" + strings.Repeat("x", i%3)})
		h.rec(300 + i).CPUPct = float64(1 + i)
	}
	step(tr, h, 0)
	for _, x := range tr.live {
		if len(x.cands) > maxAliveCandidates {
			t.Errorf("%d candidates kept, want at most %d", len(x.cands), maxAliveCandidates)
		}
	}
	h.remove(300, 301, 302) // ... and a hundred that ended, one after the other
	h2, tr2 := newHost(), newTracker()
	h2.add(200, 100, "zsh", ccRoot())
	for i := 0; i < 40; i++ {
		h2.add(300+i, 200, "tool", []string{"tool", "e" + strings.Repeat("x", i)})
		h2.rec(300 + i).CPUPct = 100
		step(tr2, h2, i)
		h2.remove(300 + i)
	}
	for _, x := range tr2.live {
		if len(x.cands) > maxCandidates+1 {
			t.Errorf("%d candidates kept, want at most %d", len(x.cands), maxCandidates+1)
		}
	}
}

func TestTrackerKeepsAddingUpARunningProgramBeyondTheCapOfEndedOnes(t *testing.T) {
	h, tr := newHost(), newTracker()
	h.add(200, 100, "zsh", ccRoot())
	for i := 0; i < 17; i++ { // seventeen programs that stay, the first sixteen heavy in the first sample only
		h.add(300+i, 200, "tool", []string{"tool", "w" + strings.Repeat("x", i)})
	}
	for i := 0; i < 30; i++ {
		for j := 0; j < 16; j++ {
			h.rec(300 + j).CPUPct = 0
			if i == 0 {
				h.rec(300 + j).CPUPct = 100
			}
		}
		h.rec(316).CPUPct = 50
		step(tr, h, i)
	}
	h.remove(200)
	for j := 0; j < 17; j++ {
		h.remove(300 + j)
	}
	got := gone(t, tr, h, 30)
	if want := "tool w" + strings.Repeat("x", 16); len(got) != 1 || got[0].Signature != want {
		t.Fatalf("the seventeenth program adds up to the most: want %q, got %+v", want, got)
	}
}

func TestTrackerSeesThroughProgramsThatOnlyStartAnother(t *testing.T) {
	h, tr := newHost(), newTracker()
	h.add(200, 100, "zsh", ccRoot())
	h.add(201, 200, "sudo", []string{"sudo", "-n", "true"})
	h.add(202, 201, "go", []string{"go", "test", "./x/"})
	sig, _ := runHeavy(t, h, tr, 20, func(int) { h.rec(202).CPUPct = 300 })
	if sig != "go test" {
		t.Errorf("Signature = %q, want go test (not sudo)", sig)
	}
}

// BenchmarkUpdateShellRootedTrees is what the naming costs a sample: 50 trees under a session, half of them rooted in a masked shell
// with a program and a helper below it.
func BenchmarkUpdateShellRootedTrees(b *testing.B) {
	for _, shells := range []bool{false, true} {
		name := "plain roots"
		if shells {
			name = "half shell roots"
		}
		b.Run(name, func(b *testing.B) {
			h, tr := newHost(), newTracker()
			for i := 0; i < 50; i++ {
				root := 1000 + i*10
				if shells && i%2 == 0 {
					h.add(root, 100, "zsh", ccRoot())
					h.add(root+1, root, "go", []string{"go", "test", "./x/"})
					h.add(root+2, root+1, "compile", []string{"compile"})
				} else {
					h.add(root, 100, "pnpm", []string{"pnpm", "vitest", "run"})
					h.add(root+1, root, "node", []string{"node", "vitest"})
					h.add(root+2, root+1, "node", []string{"node", "worker"})
				}
				h.rec(root + 2).CPUPct = 100
				h.rec(root + 1).Start = ot0 // the readers set it (macOS and Linux)
			}
			snap, ctx := h.snapshot()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				tr.Update(ot0.Add(time.Duration(i)*5*time.Second), snap, ctx)
			}
		})
	}
}

func TestTrackerKeepsAddingUpALongProgramAfterManyShortOnesEnded(t *testing.T) {
	h, tr := newHost(), newTracker()
	h.add(200, 100, "zsh", ccRoot())
	for i := 0; i < 20; i++ { // twenty short programs, one after the other, each heavier in a sample than the long one will be
		h.add(300+i, 200, "tool", []string{"tool", "short" + strings.Repeat("x", i)})
		h.rec(300 + i).CPUPct = 200
		step(tr, h, i)
		h.remove(300 + i)
	}
	h.add(400, 200, "go", []string{"go", "test", "./x/"})
	for i := 20; i < 60; i++ {
		h.rec(400).CPUPct = 50
		step(tr, h, i)
	}
	h.remove(200, 400)
	got := gone(t, tr, h, 60)
	if len(got) != 1 || got[0].Signature != "go test" {
		t.Fatalf("the long program adds up to more than any short one: %+v", got)
	}
}
