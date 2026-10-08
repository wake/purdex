package agent

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	agentpkg "github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/core"
)

type snapV2Entry struct {
	Session string                   `json:"session"`
	Event   agentpkg.NormalizedEvent `json:"event"`
}

type snapV2Frame struct {
	Epoch    string        `json:"epoch"`
	Seq      uint64        `json:"seq"`
	Sessions []snapV2Entry `json:"sessions"`
}

// rawFrames returns every frame queued for sub as {type, session, value}.
func rawFrames(t *testing.T, sub *core.EventSubscriber) []core.HostEvent {
	t.Helper()
	var out []core.HostEvent
	for {
		select {
		case msg := <-sub.SendCh():
			var ev core.HostEvent
			if err := json.Unmarshal(msg, &ev); err != nil {
				t.Fatal(err)
			}
			out = append(out, ev)
		default:
			return out
		}
	}
}

// v2Rig is a worker rig with a bus boot id, the idle hook provider, and an
// agent.v2 subscriber next to the rig's legacy one.
func v2Rig(t *testing.T) (*workerRig, *core.EventSubscriber) {
	t.Helper()
	r := newWorkerRig(t)
	r.m.core.BootID = "boot-v2"
	sub := r.m.core.Events.AddTestSubscriberWith(core.FeatureAgentV2)
	t.Cleanup(func() { r.m.core.Events.RemoveTestSubscriber(sub) })
	return r, sub
}

func oneSnapshot(t *testing.T, sub *core.EventSubscriber) (snapV2Frame, string) {
	t.Helper()
	frames := rawFrames(t, sub)
	if len(frames) != 1 || frames[0].Type != "agent.snapshot" {
		t.Fatalf("frames = %+v, want exactly one agent.snapshot", frames)
	}
	var f snapV2Frame
	if err := json.Unmarshal([]byte(frames[0].Value), &f); err != nil {
		t.Fatal(err)
	}
	return f, frames[0].Value
}

func entryFor(f snapV2Frame, code string) (snapV2Entry, bool) {
	for _, e := range f.Sessions {
		if e.Session == code {
			return e, true
		}
	}
	return snapV2Entry{}, false
}

// Two live sessions are one frame, every event stamped with the slot's
// high-water mark and marked as a replay; a session with no agent is absent.
func TestSnapshot_V2IsOneCompleteFrame(t *testing.T) {
	r, sub := v2Rig(t)
	r.registerIdleHooks()
	seedIdentityFrame(t, r.m, "%5", "cc", 200, "Sun Apr 20 01:30:00 2026", 10, modSID1, "/w")
	seedIdentityFrame(t, r.m, "%6", "cc", 201, "Sun Apr 20 01:30:01 2026", 11, "sid-other", "/o")
	if w := postEvent(r.m, hookStopBody); w.Code != http.StatusOK { // spends seq 1 for "work"
		t.Fatalf("hook: %d", w.Code)
	}
	r.drain(t)
	rawFrames(t, sub) // the live hook frame the v2 subscriber also got

	r.m.sendSnapshot(sub)
	f, _ := oneSnapshot(t, sub)
	if f.Epoch != "boot-v2" || f.Seq != 1 || len(f.Sessions) != 2 {
		t.Fatalf("snapshot = %+v, want epoch boot-v2, seq 1, two sessions", f)
	}
	for _, code := range []string{"code-work", "code-other"} {
		e, ok := entryFor(f, code)
		if !ok {
			t.Fatalf("%s missing: %+v", code, f)
		}
		if e.Event.Epoch != "boot-v2" || e.Event.Seq != 1 || !e.Event.Snapshot || e.Event.RawEventName != "replay" {
			t.Fatalf("%s event = %+v, want epoch/seq 1/snapshot/replay", code, e.Event)
		}
	}
}

// An empty host sends an empty list and spells seq 0 out.
func TestSnapshot_V2EmptyHostSpellsEmptyListAndSeqZero(t *testing.T) {
	r, sub := v2Rig(t)
	r.m.sendSnapshot(sub)
	_, raw := oneSnapshot(t, sub)
	if !strings.Contains(raw, `"sessions":[]`) || !strings.Contains(raw, `"seq":0`) || !strings.Contains(raw, `"epoch":"boot-v2"`) {
		t.Fatalf("empty snapshot = %s", raw)
	}
}

// A session that only has a legacy agent_events row is in the list.
func TestSnapshot_V2IncludesLegacySessions(t *testing.T) {
	r, sub := v2Rig(t)
	r.registerIdleHooks()
	if err := r.m.events.Set("other", "PdxStop", json.RawMessage(`{}`), "cc", 1); err != nil {
		t.Fatal(err)
	}
	r.m.sendSnapshot(sub)
	f, _ := oneSnapshot(t, sub)
	e, ok := entryFor(f, "code-other")
	if len(f.Sessions) != 1 || !ok || !e.Event.Snapshot || e.Event.Epoch != "boot-v2" {
		t.Fatalf("snapshot = %+v, want the legacy session stamped as a replay", f)
	}
}

// A subscriber that did not opt in keeps today's per-session hook frames,
// which now carry the epoch, the high-water seq and snapshot:true.
func TestSnapshot_LegacyFramesCarryEpochSeqSnapshot(t *testing.T) {
	r, _ := v2Rig(t)
	r.registerIdleHooks()
	seedIdentityFrame(t, r.m, "%5", "cc", 200, "Sun Apr 20 01:30:00 2026", 10, modSID1, "/w")
	if w := postEvent(r.m, hookStopBody); w.Code != http.StatusOK {
		t.Fatalf("hook: %d", w.Code)
	}
	r.drain(t)

	r.m.sendSnapshot(r.sub)
	got := r.drain(t)
	if len(got) != 1 {
		t.Fatalf("legacy replay = %+v, want one hook frame", got)
	}
	ev := got[0].Ev
	if ev.RawEventName != "replay" || ev.Epoch != "boot-v2" || ev.Seq != 1 || !ev.Snapshot {
		t.Fatalf("legacy replay frame = %+v", ev)
	}
}

// The snapshot is taken inside the emit slot: while a frame is being
// broadcast the snapshot waits, and its seq is the one after that frame.
func TestSnapshot_OrderedAgainstLiveEmits(t *testing.T) {
	r, sub := v2Rig(t)
	seedIdentityFrame(t, r.m, "%5", "cc", 200, "Sun Apr 20 01:30:00 2026", 10, modSID1, "/w")

	r.m.emit.mu.Lock() // an emit is inside the slot
	done := make(chan struct{})
	go func() {
		defer close(done)
		r.m.sendSnapshot(sub)
	}()
	select {
	case <-done:
		t.Fatal("the snapshot did not wait for the slot")
	case <-time.After(100 * time.Millisecond):
	}
	r.m.emit.seq = 7 // the emit's frame goes out as seq 7
	r.m.emit.epoch = "boot-v2"
	r.m.emit.mu.Unlock()
	<-done

	f, _ := oneSnapshot(t, sub)
	if f.Seq != 7 {
		t.Fatalf("snapshot seq = %d, want 7 (after the frame that held the slot)", f.Seq)
	}
}

// Non-tmux sessions are listed from the slot's table, and one that sent a
// clear is gone.
func TestSnapshot_V2IncludesNonTmuxSessions(t *testing.T) {
	r, sub := v2Rig(t)
	r.m.registry.Register(&fakeAgentProvider{
		typeName: "cc",
		derive: func(event string, _ json.RawMessage) agentpkg.DeriveResult {
			if event == "PdxSessionEnd" {
				return agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusClear}
			}
			return agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusIdle}
		},
	})
	postNonTmux(t, r.m, "s1", "PdxStop")
	postNonTmux(t, r.m, "s2", "PdxStop")
	r.drain(t)
	rawFrames(t, sub)

	r.m.sendSnapshot(sub)
	f, _ := oneSnapshot(t, sub)
	for _, sid := range []string{"s1", "s2"} {
		e, ok := entryFor(f, NonTmuxAgentCode(sid))
		if !ok || !e.Event.Snapshot || e.Event.Seq != f.Seq || e.Event.RawEventName != "replay" {
			t.Fatalf("%s missing or not stamped as a replay: %+v", sid, f)
		}
	}

	postNonTmux(t, r.m, "s2", "PdxSessionEnd")
	rawFrames(t, sub)
	r.m.sendSnapshot(sub)
	f, _ = oneSnapshot(t, sub)
	if _, ok := entryFor(f, NonTmuxAgentCode("s2")); ok || len(f.Sessions) != 1 {
		t.Fatalf("after the clear the snapshot = %+v, want only s1", f)
	}
}
