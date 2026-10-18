//go:build e2e

// End-to-end scenarios of the wwd worker (WWD-4a-4c). The harness and its safety rules are in e2e_harness_test.go.
//
// Run with: go test -tags e2e -timeout 25m -run TestE2E ./cmd/wwd/
// Ask the load watcher first: this builds wwd and starts two or three of them.
package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

// What a run that was killed leaves behind is reaped by the next one, and only what proves itself ours: the dead run's sleeper goes,
// a process with another command line that a pid file names (a reused pid) stays, and so does a directory whose run is alive.
func TestE2E_HarnessReapsOnlyWhatADeadRunLeft(t *testing.T) {
	mk := func(owner int, arg string) string {
		d, err := os.MkdirTemp("", "wwde")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.RemoveAll(d) })
		for name, body := range map[string]string{"owner.pid": strconv.Itoa(owner), "sleep-arg": arg} {
			if err := os.WriteFile(filepath.Join(d, name), []byte(body+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		return d
	}
	spawn := func(arg string) *exec.Cmd {
		c := exec.Command("/bin/sleep", arg)
		if err := c.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { c.Process.Kill(); c.Wait() })
		return c
	}
	pidFile := func(d string, pid int) {
		dir := filepath.Join(d, "ea", "work", "job1", ".home")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "sleeper.pid"), []byte(strconv.Itoa(pid)+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	gone := func(c *exec.Cmd) bool {
		done := make(chan struct{})
		go func() { c.Wait(); close(done) }()
		select {
		case <-done:
			return true
		case <-time.After(5 * time.Second):
			return false
		}
	}
	dead := mk(2147483646, "300.111111")
	ours := spawn("300.111111")
	pidFile(dead, ours.Process.Pid)
	reused := mk(2147483646, "300.222222") // the pid file names a process whose command line is another one
	other := spawn("300.333333")
	pidFile(reused, other.Process.Pid)
	live := mk(os.Getpid(), "300.444444")
	liveSleeper := spawn("300.444444")
	pidFile(live, liveSleeper.Process.Pid)

	reapStale()

	if !gone(ours) {
		t.Fatal("the dead run's sleeper survived")
	}
	if _, err := os.Stat(dead); !os.IsNotExist(err) {
		t.Fatal("the dead run's directory was not removed")
	}
	if !alive(other.Process.Pid) {
		t.Fatal("a process with another command line was killed on the word of a pid file")
	}
	if !alive(liveSleeper.Process.Pid) {
		t.Fatal("the sleeper of a live run was killed")
	}
	if _, err := os.Stat(live); err != nil {
		t.Fatal("the directory of a live run was removed")
	}
}

// A directory a run has just made and not yet given an owner.pid is not stale; an old one without is.
func TestE2E_HarnessLeavesAFreshDirectoryWithoutAnOwnerAlone(t *testing.T) {
	fresh, err := os.MkdirTemp("", "wwde")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(fresh) })
	old, err := os.MkdirTemp("", "wwde")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(old) })
	when := time.Now().Add(-10 * time.Minute)
	if err := os.Chtimes(old, when, when); err != nil {
		t.Fatal(err)
	}
	reapStale()
	if _, err := os.Stat(fresh); err != nil {
		t.Fatal("a directory still being set up by another run was removed")
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatal("an old directory without an owner was kept")
	}
}

// The processes find each other: each reports the other reachable in its pool, through TCP with the shared token.
func TestE2E_TwoDaemonsSeeEachOther(t *testing.T) {
	w := newWorld(t)
	a := w.start(procOpts{name: "ea"}, 0)
	b := w.start(procOpts{name: "eb", peers: []*proc{a}}, 0)
	// eb's config names ea (ea's names nobody), so the pool to read is eb's
	eventually(t, "eb to see ea reachable", 60*time.Second, func() bool {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		pool, err := b.api().Pool(ctx)
		if err != nil {
			return false
		}
		for _, peer := range pool.Peers {
			if peer.Info.Alias == a.alias && peer.Reachable {
				return true
			}
		}
		return false
	})
}

// A submitted task goes from the origin's queue to a worker on another process, runs, and its result and log come back.
func TestE2E_JobRunsOnThePeerWorker(t *testing.T) {
	w := newWorld(t)
	pr := w.startPair("eb", true)
	a := pr.origin
	id := w.submitTask(a, "hello")
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	job, err := a.api().WaitJob(ctx, id, nil)
	if err != nil {
		w.diagnose(a, id, pr.worker)
		t.Fatalf("wait: %v", err)
	}
	if job.State != wire.StateSucceeded || job.Result == nil || job.Result.Exit == nil || *job.Result.Exit != 0 {
		t.Fatalf("job = %+v", job)
	}
	if job.Result.Host != pr.worker.alias {
		t.Fatalf("ran on %q, want the peer %s", job.Result.Host, pr.worker.alias)
	}
	if s := job.Result.Summary; s == nil || !strings.Contains(strings.Join(s.Tail, "\n"), "hi") {
		t.Fatalf("summary = %+v", job.Result.Summary)
	}
	// the log is uploaded after the result (spec 4.3), so it arrives a moment later
	var log []byte
	var lerr error
	eventually(t, "the log to reach the origin", 30*time.Second, func() bool {
		log, lerr = a.api().Log(ctx, id)
		return lerr == nil
	})
	if !strings.Contains(string(log), "hi") {
		t.Fatalf("log = %q", log)
	}
}

// Cancelling a running job ends its command on the worker.
func TestE2E_CancelStopsTheRunningCommand(t *testing.T) {
	w := newWorld(t)
	pr := w.startPair("eb", true)
	a := pr.origin
	id := w.submitTask(a, "sleeper")
	eventually(t, "the sleeper to run", 90*time.Second, func() bool { return a.stateIs(id, wire.StateRunning)() && len(pr.worker.sleeperPids()) > 0 })
	pid := pr.worker.sleeperPids()[0]
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := a.api().Cancel(ctx, id); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the job to be cancelled", 60*time.Second, a.stateIs(id, wire.StateCancelled))
	eventually(t, "the sleeper to end", 30*time.Second, func() bool { return !alive(pid) })
}

// The worker dies without a word (kill -9): its lease lapses, the job is queued again and another worker takes it.
func TestE2E_KilledWorkerLeaseLapsesAndAnotherTakesTheJob(t *testing.T) {
	if testing.Short() {
		t.Skip("waits for a lease to lapse")
	}
	w := newWorld(t)
	pr := w.startPair("eb", true)
	a := pr.origin
	c := w.start(procOpts{name: "ec", clone: w.clone("ec"), peers: []*proc{a}}, 0) // a second worker, switched off for now
	id := w.submitTask(a, "sleeper")
	eventually(t, "the sleeper to run on the first worker", 90*time.Second, a.stateIs(id, wire.StateRunning))
	pr.worker.kill9()
	eventually(t, "the lapsed job to be queued again", 5*time.Minute, a.stateIs(id, wire.StateQueued))
	c.workerOn(true)
	eventually(t, "the second worker to run the job", 5*time.Minute, func() bool {
		j, err := a.job(id)
		return err == nil && j.State == wire.StateRunning && j.Claim != nil && j.Claim.Worker == c.alias
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = a.api().Cancel(ctx, id)
}

// The owner comes back (the fault hook stands in for the keyboard): the worker releases what it runs for reclaim, which does
// not count as an attempt, and takes it again once the owner is gone.
func TestE2E_OwnerReturnReclaimsTheRunningJob(t *testing.T) {
	if testing.Short() {
		t.Skip("waits for the owner rule")
	}
	w := newWorld(t)
	ownerFile := filepath.Join(w.root, "owner-active")
	a := w.start(procOpts{name: "ea"}, 0)
	w.start(procOpts{name: "eb", worker: true, clone: w.clone("eb"), peers: []*proc{a}, ownerFile: ownerFile}, 0)
	id := w.submitTask(a, "sleeper")
	eventually(t, "the sleeper to run", 90*time.Second, a.stateIs(id, wire.StateRunning))
	attempt := func() int {
		j, _ := a.job(id)
		if j.Claim == nil {
			return 0
		}
		return j.Claim.Attempt
	}
	first := attempt()
	if err := os.WriteFile(ownerFile, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the job to be released for reclaim", 2*time.Minute, a.stateIs(id, wire.StateQueued))
	if err := os.Remove(ownerFile); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the worker to take the job again", 2*time.Minute, a.stateIs(id, wire.StateClaimed, wire.StateRunning))
	if got := attempt(); got != first {
		t.Fatalf("a reclaim counted as an attempt: %d then %d", first, got)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = a.api().Cancel(ctx, id)
}
