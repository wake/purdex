package workbook

import (
	"context"
	"strings"
	"testing"
	"time"
)

// WB-1b′-b: the queue, the leases and the outcome table (plan D3 / D9 / D10, spec §5.1).

// kitWith queues one turn per id for session sid (all capable) and returns the kit.
func kitWith(t *testing.T, sid string, ids ...string) *kit {
	t.Helper()
	k := newKit(t)
	k.capable[sid] = true
	for i, id := range ids {
		k.turns.set(sid, endedTurn(id, int64(100*(i+1)), "做了 "+id))
		k.event(sid, int64(1000*(i+1)))
	}
	return k
}

func (k *kit) finish(stream string, j Job, r Result) bool {
	k.t.Helper()
	more, err := k.e.Result(stream, j.ID, r)
	if err != nil {
		k.t.Fatalf("result: %v", err)
	}
	return more
}

// A job id is the lease's credential: 128 random bits, never a counter an outsider could guess (codex attack).
// Mutation gate: go back to a sequence → red.
func TestQueue_JobIDsAreUnguessable(t *testing.T) {
	k := kitWith(t, "s1", "t1")
	k.capable["s2"] = true
	k.turns.set("s2", endedTurn("b", 100, "b"))
	k.event("s2", 3000)
	a, b := mustNext(t, k, "m1", "s1"), mustNext(t, k, "m2", "s2")
	for _, id := range []string{a.ID, b.ID} {
		if len(id) != len("wbj-")+32 || !strings.HasPrefix(id, "wbj-") {
			t.Fatalf("id = %q", id)
		}
	}
	if a.ID == b.ID {
		t.Fatal("same id twice")
	}
}

func TestQueue_ConversationsAreIndependent(t *testing.T) {
	k := kitWith(t, "s1", "a")
	k.capable["s2"] = true
	k.turns.set("s2", endedTurn("b", 100, "b"))
	k.event("s2", 3000)
	ja := mustNext(t, k, "m1", "s1")
	jb := mustNext(t, k, "m2", "s2")
	if ja.ID == jb.ID {
		t.Fatal("one job twice")
	}
}

// After a relay the new session (same conversation) gets the job.
func TestQueue_ARelayHandsTheJobToTheNewSession(t *testing.T) {
	k := kitWith(t, "root", "a")
	k.capable["new"] = true
	k.lineage["new"] = "root"
	mustNext(t, k, "m2", "new")
}

// A session whose mod is not capable is handed nothing.
func TestQueue_NotCapableGetsNothing(t *testing.T) {
	k := kitWith(t, "s1", "a")
	k.capable["other"] = false
	if _, ok := k.next("m", "other"); ok {
		t.Fatal("an incapable session got a job")
	}
}

// A lease that runs out ends the entry failed:lost and the queue moves on; a late result is not leased.
// Mutation gate: never reap, or keep the lease after reaping → red.
func TestQueue_LeaseExpiryIsLostAndTheQueueMovesOn(t *testing.T) {
	k := kitWith(t, "s1", "t1")
	k.turns.set("s1", endedTurn("t1", 100, "a"), endedTurn("t2", 200, "b"))
	k.event("s1", 2000)
	j1 := mustNext(t, k, "m", "s1")
	k.clock.Add(39 * time.Second)
	k.e.reap()
	if k.entries("s1")[0].State != StatePending {
		t.Fatal("reaped before timeout_ms + 10 s")
	}
	k.clock.Add(2 * time.Second)
	j2 := mustNext(t, k, "m", "s1") // Next reaps first
	if e := k.entries("s1")[0]; e.State != StateFailed || e.Reason != ReasonLost {
		t.Fatalf("entry = %s/%s", e.State, e.Reason)
	}
	if !strings.Contains(j2.Complete.Prompt, "做 t2") {
		t.Fatalf("the queue must move on: %s", j2.Complete.Prompt)
	}
	if _, err := k.e.Result("m", j1.ID, answerJSON("x", "y", "z。", "s", "")); err != ErrNotLeased {
		t.Fatalf("late result: %v", err)
	}
	if e := k.entries("s1")[0]; e.State != StateFailed {
		t.Fatal("a late result must change nothing")
	}
}

// More than three waiting turns of a conversation drop the oldest ones (skipped:backlog); a started entry is never dropped.
// Mutation gate: drop the newest instead, or count retries → red.
func TestQueue_BacklogKeepsTheNewestThree(t *testing.T) {
	k := newKit(t)
	k.capable["s1"] = true
	for i, id := range []string{"a", "b", "c", "d", "e"} {
		k.turns.set("s1", endedTurn(id, int64(100+i), id))
		k.event("s1", int64(1000*(i+1)))
	}
	rows := k.entries("s1")
	var got []string
	for _, r := range rows {
		got = append(got, r.TurnID+":"+r.State+"/"+r.Reason)
	}
	want := "a:skipped/backlog b:skipped/backlog c:pending/ d:pending/ e:pending/"
	if strings.Join(got, " ") != want {
		t.Fatalf("got %v", got)
	}
	if strings.Join(k.lines, ",") != "1:false,2:false" {
		t.Fatalf("hook = %v", k.lines)
	}
}

// Stop waits for a result that is already being applied, so nothing writes after it returns (codex R1).
// Mutation gate: drop the inflight Wait → red.
func TestQueue_StopWaitsForAResultBeingApplied(t *testing.T) {
	k := kitWith(t, "s1", "t1")
	j := mustNext(t, k, "m", "s1")
	entered, release := make(chan struct{}), make(chan struct{})
	k.st.SetObserver(func(Event) {
		select {
		case <-entered:
		default:
			close(entered)
		}
		<-release
	})
	resultDone, stopDone := make(chan struct{}), make(chan struct{})
	go func() { k.e.Result("m", j.ID, Result{Reason: "refused"}); close(resultDone) }()
	<-entered // the result is inside its store write
	go func() { k.e.Stop(); close(stopDone) }()
	select {
	case <-stopDone:
		t.Fatal("Stop returned while a result was still being applied")
	case <-time.After(150 * time.Millisecond):
	}
	close(release)
	select {
	case <-stopDone:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop did not return after the result finished")
	}
	<-resultDone
}

// A job whose lease was reaped or stopped while its input was being built is not handed out (codex attack).
// Mutation gate: return the job without re-checking the lease → red.
func TestQueue_NextDoesNotHandOutARevokedLease(t *testing.T) {
	for name, revoke := range map[string]func(k *kit){
		"reaped":  func(k *kit) { k.clock.Add(time.Minute); k.e.reap() },
		"stopped": func(k *kit) { k.e.Stop() },
	} {
		t.Run(name, func(t *testing.T) {
			k := kitWith(t, "s1", "t1")
			k.e.afterBuild = func() { revoke(k) }
			if j, ok := k.next("m", "s1"); ok {
				t.Fatalf("handed out %+v", j)
			}
			if e := k.entries("s1")[0]; e.State != StateFailed {
				t.Fatalf("entry = %s/%s", e.State, e.Reason)
			}
		})
	}
}

// A long poll returns the job as soon as one is queued, and gives up at its wait or when the context ends.
func TestQueue_NextWaits(t *testing.T) {
	k := newKit(t)
	k.capable["s1"] = true
	done := make(chan Job, 1)
	go func() {
		j, _ := k.e.Next(context.Background(), "m", "s1", 5*time.Second)
		done <- j
	}()
	time.Sleep(50 * time.Millisecond)
	k.turns.set("s1", endedTurn("t1", 100, "a"))
	k.event("s1", 1000)
	select {
	case j := <-done:
		if j.ID == "" {
			t.Fatal("woke with no job")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the long poll did not wake")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	if _, ok := k.e.Next(ctx, "m", "s1", 5*time.Second); ok || time.Since(start) > time.Second {
		t.Fatal("a cancelled context must end the wait")
	}
	if _, ok := k.e.Next(context.Background(), "m", "s1", 30*time.Millisecond); ok {
		t.Fatal("nothing was queued")
	}
}

// Stop (plan D9): a leased turn fails (stopped), a queued turn is skipped (stopped), each tells the push hold false; a late
// result is not leased and no job is handed out afterwards.
// Mutation gate: leave the leased entry pending, or keep the lease table → red.
func TestQueue_StopEndsEveryEntry(t *testing.T) {
	k := kitWith(t, "s1", "leased")
	k.turns.set("s1", endedTurn("leased", 100, "x"), endedTurn("queued", 200, "y"))
	k.event("s1", 2000)
	jl := mustNext(t, k, "m1", "s1")
	k.e.Stop()
	var got []string
	for _, e := range k.entries("s1") {
		got = append(got, e.State+"/"+e.Reason)
	}
	if strings.Join(got, " ") != "failed/stopped skipped/stopped" {
		t.Fatalf("entries = %v", got)
	}
	if strings.Join(k.lines, ",") != "1:false,2:false" {
		t.Fatalf("hook = %v", k.lines)
	}
	if _, err := k.e.Result("m1", jl.ID, Result{Reason: "refused"}); err != ErrNotLeased {
		t.Fatalf("late result: %v", err)
	}
	if _, ok := k.next("m1", "s1"); ok {
		t.Fatal("a job after Stop")
	}
}
