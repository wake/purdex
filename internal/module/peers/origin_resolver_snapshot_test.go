package peers

import (
	"sync"
	"sync/atomic"
	"testing"

	ipeers "github.com/wake/purdex/internal/peers"
)

func countRegistryReads(t *testing.T) *atomic.Int32 {
	t.Helper()
	var n atomic.Int32
	orig := readRegistryFn
	readRegistryFn = func(dir string, live ipeers.Liveness) ([]ipeers.Entry, int, error) {
		n.Add(1)
		return orig(dir, live)
	}
	t.Cleanup(func() { readRegistryFn = orig })
	return &n
}

// #3092: a pass asks about every member and lead; the registry is read once, not once per question.
func TestPresenceSnapshot_OneRegistryReadWhateverTheQuestionCount(t *testing.T) {
	r, _ := resolverFixture(t, allLiveLiveness(fixture76973ProcStart))
	reads := countRegistryReads(t)
	snap := r.PresenceSnapshot()
	for i := 0; i < 25; i++ {
		if got := snap.LeadPresence("sid-1", 10, targetProcStart); got != PresenceLive {
			t.Fatalf("question %d: presence %v, want live", i, got)
		}
	}
	if got := reads.Load(); got != 1 {
		t.Fatalf("registry read %d times for 25 questions, want 1", got)
	}
	if got := countRegistryReadsOf(t, r); got != 25 {
		t.Fatalf("control: the plain LeadPresence read %d times for 25 questions, want 25", got)
	}
}

func countRegistryReadsOf(t *testing.T, r *OriginResolver) int {
	t.Helper()
	reads := countRegistryReads(t)
	for i := 0; i < 25; i++ {
		r.LeadPresence("sid-1", 10, targetProcStart)
	}
	return int(reads.Load())
}

// The snapshot answers what LeadPresence answers: live, gone (dead pid, reused pid, another conversation), unknown.
func TestPresenceSnapshot_AnswersAsLeadPresence(t *testing.T) {
	r, dir := resolverFixture(t, allLiveLiveness(fixture76973ProcStart))
	const other = "Mon Sep 14 09:00:00 2026"
	writeRegistryFixture(t, dir, "40.json", `{"pid":40,"sessionId":"sid-4","procSt`)
	r.m.liveness.PidAlive = func(pid int) bool { return pid != 30 }
	snap := r.PresenceSnapshot()
	for _, c := range []struct {
		name      string
		sid       string
		pid       int
		procStart string
		want      Presence
	}{
		{"listed live", "sid-1", 10, targetProcStart, PresenceLive},
		{"listed live in another process", "sid-2", 30, targetProcStart, PresenceLive},
		{"dead pid", "sid-3", 30, targetProcStart, PresenceGone},
		{"reused pid", "sid-3", 50, other, PresenceGone},
		{"alive in another conversation", "sid-1-old", 10, targetProcStart, PresenceGone},
		{"alive with no file of its own", "sid-5", 50, targetProcStart, PresenceUnknown},
		{"no recorded process", "sid-3", 0, "", PresenceUnknown},
	} {
		if got := snap.LeadPresence(c.sid, c.pid, c.procStart); got != c.want {
			t.Errorf("%s: snapshot %v, want %v", c.name, got, c.want)
		}
		if plain := r.LeadPresence(c.sid, c.pid, c.procStart); plain != c.want {
			t.Errorf("%s: plain %v, want %v", c.name, plain, c.want)
		}
	}
}

// A member relayed after the snapshot was read now has a new session id the snapshot has never seen, with its old
// entry still under another id there: read from the snapshot alone that is "alive in another conversation" = gone.
// Gone is never answered from the snapshot, so the member stays.
func TestPresenceSnapshot_RelayedAfterTheReadIsNotGone(t *testing.T) {
	r, dir := resolverFixture(t, allLiveLiveness(fixture76973ProcStart))
	snap := r.PresenceSnapshot()
	if got := snap.LeadPresence("sid-1", 10, targetProcStart); got != PresenceLive {
		t.Fatalf("before the relay: %v, want live", got)
	}
	writeRegistryFixture(t, dir, "10.json", `{"pid":10,"sessionId":"sid-1-relayed","cwd":"/w","procStart":"`+targetProcStart+`","version":"2.1.270","tmux":"mt0:@1.%1","messagingSocketPath":"`+dir+`/10.sock","name":"n10","status":"idle"}`)
	if got := r.LeadPresence("sid-1-relayed", 10, targetProcStart); got != PresenceLive {
		t.Fatalf("fixture: the relayed session reads %v from a fresh read, want live", got)
	}
	if got := snap.LeadPresence("sid-1-relayed", 10, targetProcStart); got == PresenceGone {
		t.Fatalf("a member relayed after the snapshot read as gone")
	}
}

func TestPresenceSnapshot_ConcurrentAsks(t *testing.T) {
	r, _ := resolverFixture(t, allLiveLiveness(fixture76973ProcStart))
	reads := countRegistryReads(t)
	snap := r.PresenceSnapshot()
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			snap.LeadPresence("sid-1", 10, targetProcStart)
		}()
	}
	wg.Wait()
	if got := reads.Load(); got != 1 {
		t.Fatalf("registry read %d times by 16 concurrent askers, want 1", got)
	}
}
