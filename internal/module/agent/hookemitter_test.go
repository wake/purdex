package agent

import (
	"encoding/json"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	agentpkg "github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/core"
)

// plainBuild is the build closure most slot tests use: the frame is the
// projection the slot read, nothing else.
func plainBuild(p *SessionProjection) (agentpkg.NormalizedEvent, bool) {
	return buildProjectionNormalized(p, "cc", "slot:test", 1, agentpkg.DeriveResult{}), true
}

// waitParkedOnEmitLock returns once a goroutine inside emitSession is parked
// on the slot's mutex. It is the barrier that replaces a sleep: whatever the
// implementation reads before taking the lock has been read by then, and
// whatever it reads after the lock has not.
func waitParkedOnEmitLock(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	buf := make([]byte, 1<<20)
	for time.Now().Before(deadline) {
		n := runtime.Stack(buf, true)
		for _, g := range strings.Split(string(buf[:n]), "\n\n") {
			if strings.Contains(g, "(*Module).emitSession") && strings.Contains(g, "sync.(*Mutex)") {
				return
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("no goroutine parked on the emit slot's mutex")
}

// TestEmitSlot_FreshReadInsideSlot: two emits for one session. The first
// holds the slot in its build; the second is parked on the lock. The session
// changes meanwhile. The second emit goes out second and must carry the
// state as of its own turn in the slot, not the state as of its call.
func TestEmitSlot_FreshReadInsideSlot(t *testing.T) {
	r := newWorkerRig(t)
	frame := seedIdentityFrame(t, r.m, "%5", "cc", 200, "Sun Apr 20 01:30:00 2026", 10, modSID1, "/w") // idle

	inA, releaseA := make(chan struct{}), make(chan struct{})
	doneA, doneB := make(chan bool, 1), make(chan bool, 1)
	go func() {
		doneA <- r.m.emitSession(kindHook, "code-work", "work", func(p *SessionProjection) (agentpkg.NormalizedEvent, bool) {
			close(inA)
			<-releaseA
			return plainBuild(p)
		})
	}()
	<-inA // A holds the slot, with its (idle) projection read
	go func() { doneB <- r.m.emitSession(kindHook, "code-work", "work", plainBuild) }()
	waitParkedOnEmitLock(t) // B is waiting for A

	if err := r.m.frames.UpdateStatusAndLastSeen(frame.FrameID, agentpkg.StatusRunning, 20); err != nil {
		t.Fatal(err)
	}
	close(releaseA)
	if !<-doneA || !<-doneB {
		t.Fatal("an emit did not go out")
	}

	got := r.drain(t)
	if len(got) != 2 {
		t.Fatalf("%d frames, want 2: %+v", len(got), got)
	}
	if got[0].Ev.Status != "idle" || got[1].Ev.Status != "running" {
		t.Fatalf("frames = %s then %s, want idle then running: the later frame must carry the later state",
			got[0].Ev.Status, got[1].Ev.Status)
	}
}

// TestEmitSlot_BuildFalseSkipsAndDoesNotConsumeSeq: a build that declines
// puts nothing on the wire and leaves the counter where it was.
func TestEmitSlot_BuildFalseSkipsAndDoesNotConsumeSeq(t *testing.T) {
	r := newWorkerRig(t)
	seedIdentityFrame(t, r.m, "%5", "cc", 200, "Sun Apr 20 01:30:00 2026", 10, modSID1, "/w")

	if r.m.emitSession(kindHook, "code-work", "work", func(*SessionProjection) (agentpkg.NormalizedEvent, bool) {
		return agentpkg.NormalizedEvent{}, false
	}) {
		t.Fatal("a declined build reported a send")
	}
	if got := r.drain(t); len(got) != 0 {
		t.Fatalf("a declined build put frames on the wire: %+v", got)
	}
	if r.m.emit.seq != 0 {
		t.Fatalf("seq = %d after a declined build, want 0", r.m.emit.seq)
	}
	if !r.m.emitSession(kindHook, "code-work", "work", plainBuild) || r.m.emit.seq != 1 {
		t.Fatalf("the next emit: seq = %d, want 1", r.m.emit.seq)
	}
}

// TestEmitSlot_BroadcastFailureDoesNotConsumeSeq: a frame that never reached
// the bus must not leave a hole in the sequence.
func TestEmitSlot_BroadcastFailureDoesNotConsumeSeq(t *testing.T) {
	r := newWorkerRig(t)
	seedIdentityFrame(t, r.m, "%5", "cc", 200, "Sun Apr 20 01:30:00 2026", 10, modSID1, "/w")
	bus := r.m.core.Events
	r.m.core = &core.Core{} // no events bus: emitNormalizedToCode fails

	if r.m.emitSession(kindHook, "code-work", "work", plainBuild) {
		t.Fatal("an emit with no bus reported a send")
	}
	if r.m.emit.seq != 0 {
		t.Fatalf("seq = %d after a failed broadcast, want 0", r.m.emit.seq)
	}
	r.m.core = &core.Core{Events: bus}
	if !r.m.emitSession(kindHook, "code-work", "work", plainBuild) || r.m.emit.seq != 1 {
		t.Fatalf("after the bus is back: seq = %d, want 1", r.m.emit.seq)
	}
}

// TestEmitSlot_SeqContiguousAcrossSessions: one counter for the whole daemon.
// Frames for two sessions, interleaved, reach a subscriber as 1, 2, 3, ...
func TestEmitSlot_SeqContiguousAcrossSessions(t *testing.T) {
	r := newWorkerRig(t)
	r.m.core.BootID = "boot-a"
	seedIdentityFrame(t, r.m, "%5", "cc", 200, "Sun Apr 20 01:30:00 2026", 10, modSID1, "/w")
	seedIdentityFrame(t, r.m, "%6", "cc", 201, "Sun Apr 20 01:30:01 2026", 11, "S-other", "/o")

	for _, step := range []struct{ code, name string }{
		{"code-work", "work"}, {"code-other", "other"}, {"code-other", "other"},
		{"code-work", "work"}, {"code-other", "other"},
	} {
		if !r.m.emitSession(kindHook, step.code, step.name, plainBuild) {
			t.Fatalf("emit %+v did not go out", step)
		}
	}
	got := r.drain(t)
	if len(got) != 5 {
		t.Fatalf("%d frames, want 5: %+v", len(got), got)
	}
	for i, f := range got {
		if f.Ev.Seq != uint64(i+1) || f.Ev.Epoch != "boot-a" {
			t.Fatalf("frame %d (%s) = epoch %q seq %d, want boot-a/%d", i, f.Session, f.Ev.Epoch, f.Ev.Seq, i+1)
		}
	}
}

// TestEmitSlot_WireJSON: epoch and seq are in the frame's value, always, as
// the first frame's raw JSON shows.
func TestEmitSlot_WireJSON(t *testing.T) {
	r := newWorkerRig(t)
	r.m.core.BootID = "boot-a"
	seedIdentityFrame(t, r.m, "%5", "cc", 200, "Sun Apr 20 01:30:00 2026", 10, modSID1, "/w")
	if !r.m.emitSession(kindHook, "code-work", "work", plainBuild) {
		t.Fatal("emit did not go out")
	}
	var env struct{ Value string }
	if err := json.Unmarshal(<-r.sub.SendCh(), &env); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(env.Value, `"epoch":"boot-a","seq":1`) {
		t.Fatalf("value = %s, want it to carry \"epoch\":\"boot-a\",\"seq\":1", env.Value)
	}
}

// TestEmitSlot_EpochRotatesAtMax: at the largest integer a JS client counts
// exactly the counter starts over under a new epoch, which a client takes as
// "not mine, wait for a snapshot".
func TestEmitSlot_EpochRotatesAtMax(t *testing.T) {
	if hookSeqMax != 1<<53-1 {
		t.Fatalf("hookSeqMax = %d, want 2^53-1", hookSeqMax)
	}
	r := newWorkerRig(t)
	r.m.core.BootID = "boot-a"
	r.m.emit.seqMax = 3
	seedIdentityFrame(t, r.m, "%5", "cc", 200, "Sun Apr 20 01:30:00 2026", 10, modSID1, "/w")
	for i := 0; i < 5; i++ {
		if !r.m.emitSession(kindHook, "code-work", "work", plainBuild) {
			t.Fatalf("emit %d did not go out", i)
		}
	}
	type pos struct {
		epoch string
		seq   uint64
	}
	var got []pos
	for _, f := range r.drain(t) {
		got = append(got, pos{f.Ev.Epoch, f.Ev.Seq})
	}
	want := []pos{{"boot-a", 1}, {"boot-a", 2}, {"boot-a", 3}, {"boot-a-1", 1}, {"boot-a-1", 2}}
	if !slices.Equal(got, want) {
		t.Fatalf("frames = %v, want %v", got, want)
	}
}
