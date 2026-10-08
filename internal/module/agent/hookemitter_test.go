package agent

import (
	"runtime"
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
		doneA <- r.m.emitSession("code-work", "work", func(p *SessionProjection) (agentpkg.NormalizedEvent, bool) {
			close(inA)
			<-releaseA
			return plainBuild(p)
		})
	}()
	<-inA // A holds the slot, with its (idle) projection read
	go func() { doneB <- r.m.emitSession("code-work", "work", plainBuild) }()
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

	if r.m.emitSession("code-work", "work", func(*SessionProjection) (agentpkg.NormalizedEvent, bool) {
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
	if !r.m.emitSession("code-work", "work", plainBuild) || r.m.emit.seq != 1 {
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

	if r.m.emitSession("code-work", "work", plainBuild) {
		t.Fatal("an emit with no bus reported a send")
	}
	if r.m.emit.seq != 0 {
		t.Fatalf("seq = %d after a failed broadcast, want 0", r.m.emit.seq)
	}
	r.m.core = &core.Core{Events: bus}
	if !r.m.emitSession("code-work", "work", plainBuild) || r.m.emit.seq != 1 {
		t.Fatalf("after the bus is back: seq = %d, want 1", r.m.emit.seq)
	}
}
