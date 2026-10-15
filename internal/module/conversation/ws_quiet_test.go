package conversation

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/wake/purdex/internal/convfeed"
)

// genLight is a fakeLight that also tells the agent module's generation, so the stream's quiet-stream gate is on (G3c).
type genLight struct {
	fakeLight
	gen atomic.Uint64
}

func (g *genLight) LightGeneration() uint64 { return g.gen.Load() }

func (g *genLight) lookups() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.frames)
}

func quietEnv(t *testing.T, full time.Duration) (*env, *genLight, string) {
	t.Helper()
	e, _ := wsEnv(t)
	p := e.transcript(idleTurns(1))
	e.owners.own = []convfeed.Owner{{TranscriptPath: p, Status: "idle", SeenAt: 1, FrameID: "f1"}}
	e.mod.tweak.reresolve = time.Hour
	e.mod.tweak.full = full
	light := &genLight{fakeLight: fakeLight{st: "idle", ok: true}}
	e.mod.light = light
	return e, light, p
}

// G3c: a stream whose file did not grow and whose agent generation did not move does a full pass (the light lookup) only
// every fullEvery, not every poll.
func TestWS_AQuietStreamIsNotPassedInFullEveryPoll(t *testing.T) {
	e, light, _ := quietEnv(t, 400*time.Millisecond)
	srv := e.server()
	c := e.connect(srv, "")
	c.expect("conversation.snapshot")
	c.expect("approvals.snapshot")
	time.Sleep(100 * time.Millisecond) // the first full pass(es)
	start := light.lookups()
	time.Sleep(1200 * time.Millisecond) // 120 polls of 10 ms
	got := light.lookups() - start
	if got > 6 {
		t.Fatalf("%d light lookups in 1.2 s of a quiet stream (polls every 10 ms, full pass every 400 ms), want about 3", got)
	}
	if got < 1 {
		t.Fatal("no full pass at all: the fallback poll is gone")
	}
}

// The gate lets a change through at the next poll, not at the next full pass: a file that grew, and a generation that moved.
func TestWS_AQuietStreamStillSeesAGrownFileAndAMovedGeneration(t *testing.T) {
	e, light, p := quietEnv(t, time.Hour)
	srv := e.server()
	c := e.connect(srv, "")
	c.expect("conversation.snapshot")
	c.expect("approvals.snapshot")
	time.Sleep(150 * time.Millisecond)
	appendRows(t, p, userRow("u9", 60, "live row"))
	c.expect("conversation.changes") // within a poll or two, although the full pass would be an hour away
	light.set("running", true)
	light.gen.Add(1) // as the agent module does when a hook event comes in
	if got := headerStatus(t, c.expect("conversation.header")); got != "running" {
		t.Fatalf("header status %q", got)
	}
}
