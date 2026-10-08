package resourcesmod

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/resources"
)

// eventFix is a passFix whose module has an event broadcaster, a test
// subscriber and a short throttle window; the publisher goroutine runs.
type eventFix struct {
	*passFix
	sub *core.EventSubscriber
}

func newEventFix(t *testing.T, every time.Duration) *eventFix {
	old := eventEvery
	eventEvery = every
	t.Cleanup(func() { eventEvery = old })
	f := &eventFix{passFix: newPassFix(t, resources.ModeLease)}
	f.m.core = &core.Core{Events: core.NewEventsBroadcaster()}
	f.sub = f.m.core.Events.AddTestSubscriber()
	f.m.skipPass = false
	ctx, cancel := context.WithCancel(context.Background())
	f.m.wg.Add(1)
	go f.m.runEvents(ctx)
	t.Cleanup(func() { cancel(); f.m.wg.Wait(); f.m.core.Events.RemoveTestSubscriber(f.sub) })
	return f
}

// got drains the subscriber for d and returns the events.
func (f *eventFix) got(d time.Duration) []core.HostEvent {
	var out []core.HostEvent
	deadline := time.After(d)
	for {
		select {
		case b := <-f.sub.SendCh():
			var ev core.HostEvent
			if err := json.Unmarshal(b, &ev); err != nil {
				f.t.Fatal(err)
			}
			out = append(out, ev)
		case <-deadline:
			return out
		}
	}
}

// Ten changes in a window are one send at once and one trailing send, never
// more; the value is the snapshot without recent.
func TestEvents_ThrottledAndCoalesced(t *testing.T) {
	f := newEventFix(t, 300*time.Millisecond)
	f.queue("a", 35, 5*time.Minute)
	f.m.admissionPass(context.Background(), "a") // a grant: wake -> an event, at once
	var first []core.HostEvent
	select {
	case b := <-f.sub.SendCh():
		var ev core.HostEvent
		_ = json.Unmarshal(b, &ev)
		first = append(first, ev)
	case <-time.After(time.Second):
		t.Fatal("the grant sent no event")
	}
	sentAt := time.Now()
	for i := 0; i < 10; i++ { // ten changes inside the window
		f.m.requestEvent()
	}
	var trailing []core.HostEvent
	select {
	case b := <-f.sub.SendCh():
		var ev core.HostEvent
		_ = json.Unmarshal(b, &ev)
		trailing = append(trailing, ev)
	case <-time.After(2 * time.Second):
		t.Fatal("no trailing event")
	}
	if gap := time.Since(sentAt); gap < 200*time.Millisecond {
		t.Errorf("the trailing event came after %v: not held to the 300 ms window", gap)
	}
	if more := f.got(400 * time.Millisecond); len(more) != 0 {
		t.Fatalf("ten changes sent %d extra events", len(more))
	}
	evs := append(first, trailing...)
	if evs[0].Type != resources.EventType {
		t.Errorf("type = %q", evs[0].Type)
	}
	var snap map[string]json.RawMessage
	if err := json.Unmarshal([]byte(evs[0].Value), &snap); err != nil {
		t.Fatalf("value is not the snapshot: %v", err)
	}
	if _, ok := snap["recent"]; ok {
		t.Error("recent is in the event")
	}
	if _, ok := snap["leases"]; !ok {
		t.Errorf("the held lease is not in the event: %s", evs[0].Value)
	}
}

// Nothing is sent while nothing is held or waiting, however many ticks pass.
func TestEvents_SilentWhenIdle(t *testing.T) {
	f := newEventFix(t, 50*time.Millisecond)
	f.m.sampler = &fakeSampler{fn: func(context.Context, int) (resources.HostRaw, []resources.Proc, error) {
		return goodRaw(), nil, nil
	}}
	for i := 0; i < 5; i++ {
		f.m.tick(context.Background())
	}
	if evs := f.got(300 * time.Millisecond); len(evs) != 0 {
		t.Fatalf("idle tick sent %+v", evs)
	}
	// With a lease held, a tick sends.
	f.held("h", 99, "", f.nowMS()-1000)
	f.m.tick(context.Background())
	if evs := f.got(300 * time.Millisecond); len(evs) != 1 {
		t.Fatalf("a tick with a held lease sent %d events", len(evs))
	}
}

// A waiting request is activity too, and a creation is worth an event.
func TestEvents_CreateAndEndAreSent(t *testing.T) {
	f := newEventFix(t, 20*time.Millisecond)
	rf := &routeFix{passFix: f.passFix, mux: nil}
	_ = rf
	r := f.queue("w", 35, 5*time.Minute)
	f.m.requestEvent()
	if evs := f.got(300 * time.Millisecond); len(evs) != 1 {
		t.Fatalf("events = %d", len(evs))
	}
	f.m.admissionPass(context.Background(), r.ID)
	f.m.stateMu.Lock()
	f.m.store.End(r.ID, resources.EndReleased, f.nowMS())
	f.m.wake() // the end
	f.m.stateMu.Unlock()
	evs := f.got(300 * time.Millisecond)
	if len(evs) == 0 {
		t.Fatal("the last end sent no event")
	}
	var snap resources.Snapshot
	if err := json.Unmarshal([]byte(evs[len(evs)-1].Value), &snap); err != nil || len(snap.Leases) != 0 {
		t.Errorf("last event = %s (%v), want no leases", evs[len(evs)-1].Value, err)
	}
}

// A new subscriber gets the state once on connect.
func TestEvents_SnapshotOnSubscribe(t *testing.T) {
	f := newEventFix(t, time.Second)
	f.held("h", 99, "", f.nowMS()-1000)
	f.m.core.Events.OnSubscribe(f.m.sendEventSnapshot)
	late := f.m.core.Events.AddTestSubscriber()
	defer f.m.core.Events.RemoveTestSubscriber(late)
	f.m.sendEventSnapshot(late) // what HandleHostEvents does for a connection
	select {
	case b := <-late.SendCh():
		var ev core.HostEvent
		if json.Unmarshal(b, &ev) != nil || ev.Type != resources.EventType {
			t.Fatalf("snapshot = %s", b)
		}
	case <-time.After(time.Second):
		t.Fatal("no snapshot on subscribe")
	}
}

// The vanished rule (plan Task 1.5): a session-new lease past its warmup whose
// tree was empty for two samples in a row ends; nothing else does.
func TestSweeper_VanishedSessionNew(t *testing.T) {
	f := newPassFix(t, resources.ModeLease)
	mk := func(id, scope string, grantedAgo time.Duration, empty int) {
		f.held(id, 99, "", f.nowMS()-grantedAgo.Milliseconds())
		if _, err := f.m.store.db.Exec(`UPDATE resource_leases SET scope = ?, empty_samples = ? WHERE id = ?`, scope, empty, id); err != nil {
			t.Fatal(err)
		}
	}
	mk("gone", resources.ScopeSessionNew, time.Minute, 2)
	mk("one", resources.ScopeSessionNew, time.Minute, 1)
	mk("warm", resources.ScopeSessionNew, 5*time.Second, 9) // still inside the 20 s warmup
	mk("proc", resources.ScopeProcess, time.Minute, 9)      // a process lease is judged by its holder
	f.m.sweepOnce(context.Background())
	if r := f.row("gone"); r.State != "ended" || r.EndReason != resources.EndVanished {
		t.Errorf("gone = %+v", r)
	}
	for _, id := range []string{"one", "warm", "proc"} {
		if f.state(id) != "held" {
			t.Errorf("%s ended", id)
		}
	}
}
