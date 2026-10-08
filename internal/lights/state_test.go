package lights

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	agentpkg "github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/modevents"
)

const (
	sidA = "11111111-1111-4111-8111-111111111111"
	sidB = "22222222-2222-4222-8222-222222222222"
)

var t0 = time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)

// e builds one event of sid A; seq and at are filled by play.
func e(typ, data string) modevents.Event {
	return modevents.Event{SID: sidA, Type: typ, Data: json.RawMessage(data)}
}

// eSID builds one event of the given sid.
func eSID(sid, typ, data string) modevents.Event {
	ev := e(typ, data)
	ev.SID = sid
	return ev
}

// play applies evs in order (seq 1, 2, …; at = 1000·seq ms; one second
// apart in daemon time) and returns the state.
func play(evs ...modevents.Event) *StreamState {
	s := NewStreamState("stream-test-1")
	for i, ev := range evs {
		ev.Seq = int64(i + 1)
		if ev.At == 0 {
			ev.At = int64(i+1) * 1000
		}
		s.Apply(ev, t0.Add(time.Duration(i)*time.Second))
	}
	return s
}

type statusCase struct {
	name string
	evs  []modevents.Event
	want agentpkg.Status
}

func runStatus(t *testing.T, cases []statusCase) {
	t.Helper()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := play(c.evs...).Status(); got != c.want {
				t.Fatalf("Status() = %q, want %q", got, c.want)
			}
		})
	}
}

var (
	start      = e(modevents.TypeSessionStart, `{"cwd":"/w","surface":"cli"}`)
	turnStart  = e(modevents.TypeTurnStart, `{"turn_id":"t1"}`)
	turnAnswer = e(modevents.TypeTurnComplete, `{"turn_id":"t1","reason":"answer","duration_ms":5,"aborted":false}`)
	turnError  = e(modevents.TypeTurnComplete, `{"turn_id":"t1","reason":"error","duration_ms":5,"aborted":false}`)
)

func TestNewStreamState_Empty(t *testing.T) {
	s := NewStreamState("stream-x-0001")
	if s.Stream != "stream-x-0001" || s.Status() != agentpkg.StatusIdle || len(s.DotList()) != 0 || s.Background != "" {
		t.Fatalf("new state = %+v, status %q", s, s.Status())
	}
	if s.Live(t0) {
		t.Fatal("a state that never saw an event is not live")
	}
}

func TestStatus_TurnLifecycle(t *testing.T) {
	runStatus(t, []statusCase{
		{"started session is idle", []modevents.Event{start}, agentpkg.StatusIdle},
		{"main turn.start is running", []modevents.Event{start, turnStart}, agentpkg.StatusRunning},
		{"main turn.complete answer is idle", []modevents.Event{start, turnStart, turnAnswer}, agentpkg.StatusIdle},
		{"refusal is idle", []modevents.Event{start, turnStart, e(modevents.TypeTurnComplete, `{"turn_id":"t1","reason":"refusal"}`)}, agentpkg.StatusIdle},
		{"aborted is idle", []modevents.Event{start, turnStart, e(modevents.TypeTurnComplete, `{"turn_id":"t1","reason":"aborted","aborted":true}`)}, agentpkg.StatusIdle},
		{"turn.start without session.start is running", []modevents.Event{turnStart}, agentpkg.StatusRunning},
		{"subagent turn.start does not run main", []modevents.Event{start, e(modevents.TypeTurnStart, `{"turn_id":"s1","agent_id":"a1"}`)}, agentpkg.StatusIdle},
		{"subagent turn.complete does not end the main turn", []modevents.Event{start, turnStart, e(modevents.TypeTurnComplete, `{"turn_id":"s1","reason":"answer","agent_id":"a1"}`)}, agentpkg.StatusRunning},
		{"main turn.complete clears asks", []modevents.Event{start, turnStart, e(modevents.TypeToolCheck, `{"tool":"Bash","tool_use_id":"u1","decision":"ask"}`), turnAnswer}, agentpkg.StatusIdle},
		{"main turn.start clears asks", []modevents.Event{start, e(modevents.TypeToolCheck, `{"tool":"Bash","tool_use_id":"u1","decision":"ask"}`), turnStart}, agentpkg.StatusRunning},
		{"undecodable turn.start changes nothing", []modevents.Event{start, e(modevents.TypeTurnStart, `{"turn_id":7}`)}, agentpkg.StatusIdle},
		{"usage changes nothing", []modevents.Event{start, turnStart, e(modevents.TypeUsage, `{"context":{"window":1}}`)}, agentpkg.StatusRunning},
		{"unknown type changes nothing", []modevents.Event{start, turnStart, e("turn.step", `{}`)}, agentpkg.StatusRunning},
	})
}

func TestStatus_AskFromCheckAndAskTools(t *testing.T) {
	runStatus(t, []statusCase{
		{"check ask waits", []modevents.Event{start, turnStart, e(modevents.TypeToolCheck, `{"tool":"Bash","tool_use_id":"u1","decision":"ask"}`)}, agentpkg.StatusWaiting},
		{"check ask without tool_use_id waits", []modevents.Event{start, turnStart, e(modevents.TypeToolCheck, `{"tool":"Bash","decision":"ask"}`)}, agentpkg.StatusWaiting},
		{"subagent check ask waits", []modevents.Event{start, turnStart, e(modevents.TypeToolCheck, `{"tool":"Bash","tool_use_id":"u1","agent_id":"a1","decision":"ask"}`)}, agentpkg.StatusWaiting},
		{"check allow runs", []modevents.Event{start, turnStart, e(modevents.TypeToolCheck, `{"tool":"Bash","tool_use_id":"u1","decision":"allow"}`)}, agentpkg.StatusRunning},
		{"check without decision runs", []modevents.Event{start, turnStart, e(modevents.TypeToolCheck, `{"tool":"Bash","tool_use_id":"u1"}`)}, agentpkg.StatusRunning},
		{"AskUserQuestion waits", []modevents.Event{start, turnStart, e(modevents.TypeToolStart, `{"tool":"AskUserQuestion","tool_use_id":"u2"}`)}, agentpkg.StatusWaiting},
		{"ExitPlanMode waits", []modevents.Event{start, turnStart, e(modevents.TypeToolStart, `{"tool":"ExitPlanMode","tool_use_id":"u3"}`)}, agentpkg.StatusWaiting},
		{"a subagent's AskUserQuestion waits", []modevents.Event{start, turnStart, e(modevents.TypeToolStart, `{"tool":"AskUserQuestion","tool_use_id":"u4","agent_id":"a1"}`)}, agentpkg.StatusWaiting},
		{"other tool runs", []modevents.Event{start, turnStart, e(modevents.TypeToolStart, `{"tool":"Bash","tool_use_id":"u5"}`)}, agentpkg.StatusRunning},
		{"ask tool without tool_use_id is not an ask", []modevents.Event{start, turnStart, e(modevents.TypeToolStart, `{"tool":"AskUserQuestion"}`)}, agentpkg.StatusRunning},
	})
}

func TestStatus_ToolEndClearsOnlyItsAsk(t *testing.T) {
	ask1 := e(modevents.TypeToolCheck, `{"tool":"Bash","tool_use_id":"u1","decision":"ask"}`)
	ask2 := e(modevents.TypeToolStart, `{"tool":"AskUserQuestion","tool_use_id":"u2"}`)
	runStatus(t, []statusCase{
		{"end of the only ask runs", []modevents.Event{start, turnStart, ask1, e(modevents.TypeToolEnd, `{"tool_use_id":"u1","ms":3,"error":false}`)}, agentpkg.StatusRunning},
		{"end of one of two asks still waits", []modevents.Event{start, turnStart, ask1, ask2, e(modevents.TypeToolEnd, `{"tool_use_id":"u1","ms":3}`)}, agentpkg.StatusWaiting},
		{"end of another tool still waits", []modevents.Event{start, turnStart, ask1, e(modevents.TypeToolEnd, `{"tool_use_id":"u9","ms":3}`)}, agentpkg.StatusWaiting},
		{"end of both asks runs", []modevents.Event{start, turnStart, ask1, ask2, e(modevents.TypeToolEnd, `{"tool_use_id":"u2"}`), e(modevents.TypeToolEnd, `{"tool_use_id":"u1"}`)}, agentpkg.StatusRunning},
		{"undecodable tool.end still waits", []modevents.Event{start, turnStart, ask1, e(modevents.TypeToolEnd, `{"tool_use_id":["u1"]}`)}, agentpkg.StatusWaiting},
	})
}

func TestStatus_ApprovedLeavesWaiting(t *testing.T) {
	ask1 := e(modevents.TypeToolCheck, `{"tool":"Bash","tool_use_id":"u1","decision":"ask"}`)
	ask2 := e(modevents.TypeToolCheck, `{"tool":"Edit","tool_use_id":"u2","decision":"ask"}`)
	approved1 := e(typeToolApproved, `{"tool_use_id":"u1"}`)
	runStatus(t, []statusCase{
		{"the only ask approved runs", []modevents.Event{start, turnStart, ask1, approved1}, agentpkg.StatusRunning},
		{"one of two asks approved still waits", []modevents.Event{start, turnStart, ask1, ask2, approved1}, agentpkg.StatusWaiting},
		{"a new ask after an approval waits again", []modevents.Event{start, turnStart, ask1, approved1, ask2}, agentpkg.StatusWaiting},
		{"approval of another tool still waits", []modevents.Event{start, turnStart, ask1, e(typeToolApproved, `{"tool_use_id":"u9"}`)}, agentpkg.StatusWaiting},
	})
}

func TestStatus_ErrorUntilNextMainTurn(t *testing.T) {
	ask := e(modevents.TypeToolCheck, `{"tool":"Bash","tool_use_id":"u1","decision":"ask"}`)
	runStatus(t, []statusCase{
		{"main turn ends in error", []modevents.Event{start, turnStart, turnError}, agentpkg.StatusError},
		{"error outranks waiting", []modevents.Event{start, turnStart, turnError, ask}, agentpkg.StatusError},
		{"error outranks compacting", []modevents.Event{start, turnStart, turnError, e(modevents.TypeCompactStart, `{"trigger":"manual"}`)}, agentpkg.StatusError},
		{"next main turn.start leaves error", []modevents.Event{start, turnStart, turnError, turnStart}, agentpkg.StatusRunning},
		{"next main turn answer leaves error", []modevents.Event{start, turnStart, turnError, turnStart, turnAnswer}, agentpkg.StatusIdle},
		{"session.start leaves error", []modevents.Event{start, turnStart, turnError, start}, agentpkg.StatusIdle},
		{"session.switch leaves error", []modevents.Event{start, turnStart, turnError, eSID(sidB, modevents.TypeSessionSwitch, `{"prev_sid":"`+sidA+`","source":"clear"}`)}, agentpkg.StatusIdle},
		{"heartbeat without the error field keeps error", []modevents.Event{start, turnStart, turnError, e(modevents.TypeHeartbeat, `{"asks":[],"compacting":false,"agents":[]}`)}, agentpkg.StatusError},
	})
}

func TestStatus_SubagentErrorIsNotMain(t *testing.T) {
	subErr := e(modevents.TypeTurnComplete, `{"turn_id":"s1","reason":"error","agent_id":"a1"}`)
	runStatus(t, []statusCase{
		{"subagent error while the main turn runs", []modevents.Event{start, turnStart, subErr}, agentpkg.StatusRunning},
		{"subagent error while idle", []modevents.Event{start, subErr}, agentpkg.StatusIdle},
		{"subagent answer does not clear a main error", []modevents.Event{start, turnStart, turnError, e(modevents.TypeTurnComplete, `{"turn_id":"s1","reason":"answer","agent_id":"a1"}`)}, agentpkg.StatusError},
	})
}

func TestStatus_CompactIsRunningPrecomputeIsNot(t *testing.T) {
	runStatus(t, []statusCase{
		{"manual compact runs", []modevents.Event{start, e(modevents.TypeCompactStart, `{"trigger":"manual"}`)}, agentpkg.StatusRunning},
		{"auto compact runs", []modevents.Event{start, e(modevents.TypeCompactStart, `{"trigger":"auto"}`)}, agentpkg.StatusRunning},
		{"compact end is idle", []modevents.Event{start, e(modevents.TypeCompactStart, `{"trigger":"auto"}`), e(modevents.TypeCompactEnd, `{"trigger":"auto","ok":true}`)}, agentpkg.StatusIdle},
		{"manual compact end is idle", []modevents.Event{start, e(modevents.TypeCompactStart, `{"trigger":"manual"}`), e(modevents.TypeCompactEnd, `{"trigger":"manual","ok":true}`)}, agentpkg.StatusIdle},
		{"skipped compaction (ok false) ends too", []modevents.Event{start, e(modevents.TypeCompactStart, `{"trigger":"auto"}`), e(modevents.TypeCompactEnd, `{"trigger":"auto","ok":false}`)}, agentpkg.StatusIdle},
		{"precompute start and end stay idle", []modevents.Event{start, e(modevents.TypeCompactStart, `{"trigger":"precompute"}`), e(modevents.TypeCompactEnd, `{"trigger":"precompute","ok":true}`)}, agentpkg.StatusIdle},
		{"precompute does not run", []modevents.Event{start, e(modevents.TypeCompactStart, `{"trigger":"precompute"}`)}, agentpkg.StatusIdle},
		{"precompute end does not end a real compaction", []modevents.Event{start, e(modevents.TypeCompactStart, `{"trigger":"auto"}`), e(modevents.TypeCompactEnd, `{"trigger":"precompute","ok":true}`)}, agentpkg.StatusRunning},
		{"subagent compact does not run main", []modevents.Event{start, e(modevents.TypeCompactStart, `{"trigger":"auto","agent_id":"a1"}`)}, agentpkg.StatusIdle},
		{"subagent compact end does not end main compaction", []modevents.Event{start, e(modevents.TypeCompactStart, `{"trigger":"auto"}`), e(modevents.TypeCompactEnd, `{"trigger":"auto","agent_id":"a1","ok":true}`)}, agentpkg.StatusRunning},
		{"compact during a turn ends back to running", []modevents.Event{start, turnStart, e(modevents.TypeCompactStart, `{"trigger":"auto"}`), e(modevents.TypeCompactEnd, `{"trigger":"auto","ok":true}`)}, agentpkg.StatusRunning},
	})
}

func TestStatus_EndedIsClearAndAnyEventReopens(t *testing.T) {
	end := e(modevents.TypeSessionEnd, `{"reason":"prompt_input_exit"}`)
	runStatus(t, []statusCase{
		{"end is clear", []modevents.Event{start, turnStart, end}, agentpkg.StatusClear},
		{"end without reason is clear", []modevents.Event{start, e(modevents.TypeSessionEnd, `{}`)}, agentpkg.StatusClear},
		{"end outranks error", []modevents.Event{start, turnStart, turnError, end}, agentpkg.StatusClear},
		{"a later turn.start reopens", []modevents.Event{start, end, turnStart}, agentpkg.StatusRunning},
		{"a later usage reopens", []modevents.Event{start, end, e(modevents.TypeUsage, `{}`)}, agentpkg.StatusIdle},
		{"a later unknown event reopens", []modevents.Event{start, end, e("turn.step", `{}`)}, agentpkg.StatusIdle},
		{"end leaves error (spec §7)", []modevents.Event{start, turnStart, turnError, end, e(modevents.TypeUsage, `{}`)}, agentpkg.StatusIdle},
	})
	t.Run("end clears background", func(t *testing.T) {
		s := play(start, e(modevents.TypeBackground, `{"tasks":[{"id":"w","type":"workflow","status":"running"}],"crons":0}`), end)
		if s.Background != "" || !s.Ended {
			t.Fatalf("Background = %q, Ended = %v", s.Background, s.Ended)
		}
	})
}

func TestStatus_ClearResumeEndIsNotAnEnd(t *testing.T) {
	runStatus(t, []statusCase{
		{"clear end keeps running", []modevents.Event{start, turnStart, e(modevents.TypeSessionEnd, `{"reason":"clear"}`)}, agentpkg.StatusRunning},
		{"resume end keeps error", []modevents.Event{start, turnStart, turnError, e(modevents.TypeSessionEnd, `{"reason":"resume"}`)}, agentpkg.StatusError},
	})
	t.Run("clear end keeps background", func(t *testing.T) {
		s := play(start, e(modevents.TypeBackground, `{"tasks":[],"crons":1}`), e(modevents.TypeSessionEnd, `{"reason":"clear"}`))
		if s.Background != BackgroundSchedule || s.Ended {
			t.Fatalf("Background = %q, Ended = %v", s.Background, s.Ended)
		}
	})
}

func TestSwitch_ResetsTurnAndDots(t *testing.T) {
	sw := eSID(sidB, modevents.TypeSessionSwitch, `{"prev_sid":"`+sidA+`","source":"clear"}`)
	s := play(start, turnStart,
		e(modevents.TypeToolCheck, `{"tool":"Bash","tool_use_id":"u1","decision":"ask"}`),
		e(modevents.TypeCompactStart, `{"trigger":"auto"}`),
		e(modevents.TypeAgentSpawn, `{"agent_id":"a1","tool_use_id":"t","background":false,"subagent_type":"x"}`),
		e(modevents.TypeBackground, `{"tasks":[{"id":"m","type":"monitor","status":"running"}],"crons":0}`),
		e(modevents.TypeSessionEnd, `{"reason":"clear"}`),
		sw)
	if s.Status() != agentpkg.StatusIdle || s.TurnID != "" || len(s.Asks) != 0 || s.Compacting || s.Err {
		t.Fatalf("after switch: status %q, %+v", s.Status(), s)
	}
	if len(s.DotList()) != 0 || s.Background != "" {
		t.Fatalf("after switch: dots %v, background %q", s.DotList(), s.Background)
	}
	if s.SID != sidB {
		t.Fatalf("SID = %q, want %q", s.SID, sidB)
	}

	t.Run("session.start resets the same way", func(t *testing.T) {
		s := play(start, turnStart,
			e(modevents.TypeAgentSpawn, `{"agent_id":"a1","tool_use_id":"t","background":false,"subagent_type":"x"}`),
			e(modevents.TypeBackground, `{"tasks":[],"crons":2}`),
			start)
		if s.Status() != agentpkg.StatusIdle || len(s.DotList()) != 0 || s.Background != "" {
			t.Fatalf("after session.start: status %q, dots %v, background %q", s.Status(), s.DotList(), s.Background)
		}
	})
}

func TestDots_SpawnAddsWorkflowSpawnDoesNot(t *testing.T) {
	spawn := func(at int64, data string) modevents.Event {
		ev := e(modevents.TypeAgentSpawn, data)
		ev.At = at
		return ev
	}
	cases := []struct {
		name string
		evs  []modevents.Event
		want []Dot
	}{
		{"plain spawn adds a dot", []modevents.Event{start, spawn(5000, `{"agent_id":"a1","tool_use_id":"t1","background":false,"subagent_type":"x"}`)}, []Dot{{ID: "a1", StartedAt: 5000}}},
		{"workflow spawn adds nothing", []modevents.Event{start, spawn(5000, `{"agent_id":"a1","tool_use_id":"t1","background":true,"subagent_type":"x","workflow_run_id":"wf1"}`)}, nil},
		{"spawn without agent_id adds nothing", []modevents.Event{start, spawn(5000, `{"tool_use_id":"t1"}`)}, nil},
		{"undecodable spawn adds nothing", []modevents.Event{start, spawn(5000, `{"agent_id":1}`)}, nil},
		{"sorted by start then id", []modevents.Event{start,
			spawn(9000, `{"agent_id":"c"}`),
			spawn(7000, `{"agent_id":"b"}`),
			spawn(7000, `{"agent_id":"a"}`),
		}, []Dot{{ID: "a", StartedAt: 7000}, {ID: "b", StartedAt: 7000}, {ID: "c", StartedAt: 9000}}},
		{"respawn of the same id keeps the first start", []modevents.Event{start, spawn(5000, `{"agent_id":"a1"}`), spawn(6000, `{"agent_id":"a1"}`)}, []Dot{{ID: "a1", StartedAt: 5000}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := play(c.evs...).DotList()
			if len(got) == 0 && len(c.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("DotList() = %v, want %v", got, c.want)
			}
		})
	}
}

func TestDots_SubagentTurnCompleteRemoves(t *testing.T) {
	s := play(start, turnStart,
		e(modevents.TypeAgentSpawn, `{"agent_id":"a1"}`),
		e(modevents.TypeAgentSpawn, `{"agent_id":"a2"}`),
		e(modevents.TypeTurnComplete, `{"turn_id":"s1","reason":"answer","agent_id":"a1"}`))
	got := s.DotList()
	if len(got) != 1 || got[0].ID != "a2" {
		t.Fatalf("DotList() = %v, want only a2", got)
	}
	if s.Status() != agentpkg.StatusRunning {
		t.Fatalf("Status() = %q, want running", s.Status())
	}
	s2 := play(start, turnStart, e(modevents.TypeAgentSpawn, `{"agent_id":"a1"}`), turnAnswer)
	if len(s2.DotList()) != 1 {
		t.Fatalf("a main turn.complete must not remove dots: %v", s2.DotList())
	}
}

func TestHeartbeat_RepairsLostTurnComplete(t *testing.T) {
	runStatus(t, []statusCase{
		{"turn.start then heartbeat without turn_id is idle", []modevents.Event{start, turnStart, e(modevents.TypeHeartbeat, `{"asks":[],"compacting":false,"agents":[]}`)}, agentpkg.StatusIdle},
		{"heartbeat with turn_id is running", []modevents.Event{start, e(modevents.TypeHeartbeat, `{"turn_id":"t9","asks":[],"compacting":false,"agents":[]}`)}, agentpkg.StatusRunning},
		{"heartbeat with turn_id and asks is waiting", []modevents.Event{start, e(modevents.TypeHeartbeat, `{"turn_id":"t9","asks":["u1"],"compacting":false,"agents":[]}`)}, agentpkg.StatusWaiting},
		{"heartbeat compacting is running", []modevents.Event{start, e(modevents.TypeHeartbeat, `{"asks":[],"compacting":true,"agents":[]}`)}, agentpkg.StatusRunning},
		{"heartbeat clears compacting", []modevents.Event{start, e(modevents.TypeCompactStart, `{"trigger":"auto"}`), e(modevents.TypeHeartbeat, `{"asks":[],"compacting":false,"agents":[]}`)}, agentpkg.StatusIdle},
		{"heartbeat error true is error", []modevents.Event{start, e(modevents.TypeHeartbeat, `{"asks":[],"compacting":false,"agents":[],"error":true}`)}, agentpkg.StatusError},
		{"heartbeat error false clears error", []modevents.Event{start, turnStart, turnError, e(modevents.TypeHeartbeat, `{"asks":[],"compacting":false,"agents":[],"error":false}`)}, agentpkg.StatusIdle},
		{"undecodable heartbeat changes nothing", []modevents.Event{start, turnStart, e(modevents.TypeHeartbeat, `{"asks":"u1","compacting":false}`)}, agentpkg.StatusRunning},
	})
}

func TestHeartbeat_ReconcilesDotsAndAsks(t *testing.T) {
	hb := e(modevents.TypeHeartbeat, `{"turn_id":"t1","asks":["u2"],"compacting":false,"agents":[
		{"id":"keep","status":"running"},
		{"id":"new-pending","status":"pending"},
		{"id":"new-waiting","status":"waiting"},
		{"id":"done","status":"completed"},
		{"id":"failed-new","status":"failed"}]}`)
	hb.At = 42000
	s := play(start, turnStart,
		e(modevents.TypeToolCheck, `{"tool":"Bash","tool_use_id":"u1","decision":"ask"}`),
		e(modevents.TypeAgentSpawn, `{"agent_id":"keep"}`),
		e(modevents.TypeAgentSpawn, `{"agent_id":"done"}`),
		e(modevents.TypeAgentSpawn, `{"agent_id":"gone"}`),
		hb)
	want := []Dot{{ID: "keep", StartedAt: 4000}, {ID: "new-pending", StartedAt: 42000}, {ID: "new-waiting", StartedAt: 42000}}
	if got := s.DotList(); !reflect.DeepEqual(got, want) {
		t.Fatalf("DotList() = %v, want %v", got, want)
	}
	if !reflect.DeepEqual(s.Asks, map[string]bool{"u2": true}) {
		t.Fatalf("Asks = %v, want exactly u2", s.Asks)
	}
	if s.Status() != agentpkg.StatusWaiting {
		t.Fatalf("Status() = %q, want waiting", s.Status())
	}
	t.Run("empty asks list clears a synthetic check ask", func(t *testing.T) {
		s := play(start, turnStart,
			e(modevents.TypeToolCheck, `{"tool":"Bash","decision":"ask"}`),
			e(modevents.TypeHeartbeat, `{"turn_id":"t1","asks":[],"compacting":false,"agents":[]}`))
		if s.Status() != agentpkg.StatusRunning || len(s.Asks) != 0 {
			t.Fatalf("Status() = %q, Asks = %v", s.Status(), s.Asks)
		}
	})
}

func TestHeartbeat_AbsentFieldsKeepState(t *testing.T) {
	s := play(start, turnStart,
		e(modevents.TypeToolCheck, `{"tool":"Bash","tool_use_id":"u1","decision":"ask"}`),
		e(modevents.TypeAgentSpawn, `{"agent_id":"a1"}`),
		e(modevents.TypeHeartbeat, `{"turn_id":"t1","error":false}`))
	if s.Status() != agentpkg.StatusWaiting {
		t.Fatalf("Status() = %q, want waiting", s.Status())
	}
	if !reflect.DeepEqual(s.Asks, map[string]bool{"u1": true}) {
		t.Fatalf("Asks = %v, want u1 kept", s.Asks)
	}
	if got := s.DotList(); len(got) != 1 || got[0].ID != "a1" {
		t.Fatalf("DotList() = %v, want a1 kept", got)
	}
	t.Run("absent compacting keeps it", func(t *testing.T) {
		s := play(start, e(modevents.TypeCompactStart, `{"trigger":"auto"}`), e(modevents.TypeHeartbeat, `{"asks":[],"agents":[]}`))
		if !s.Compacting || s.Status() != agentpkg.StatusRunning {
			t.Fatalf("Compacting = %v, Status() = %q", s.Compacting, s.Status())
		}
	})
	t.Run("absent turn_id still clears the turn", func(t *testing.T) {
		s := play(start, turnStart, e(modevents.TypeHeartbeat, `{}`))
		if s.TurnID != "" || s.Status() != agentpkg.StatusIdle {
			t.Fatalf("TurnID = %q, Status() = %q", s.TurnID, s.Status())
		}
	})
}

func TestHeartbeat_AgentsAbsentKeepsDots(t *testing.T) {
	cases := []struct {
		name string
		hb   string
		want []string
	}{
		// The mod omits agents when $.agent.list() throws.
		{"absent keeps every dot", `{"asks":[],"compacting":false}`, []string{"a1", "a2"}},
		{"empty list removes every dot", `{"asks":[],"compacting":false,"agents":[]}`, nil},
		{"listed active keeps only that dot", `{"asks":[],"compacting":false,"agents":[{"id":"a2","status":"running"}]}`, []string{"a2"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := play(start, e(modevents.TypeAgentSpawn, `{"agent_id":"a1"}`), e(modevents.TypeAgentSpawn, `{"agent_id":"a2"}`), e(modevents.TypeHeartbeat, c.hb))
			var got []string
			for _, d := range s.DotList() {
				got = append(got, d.ID)
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("dots = %v, want %v", got, c.want)
			}
		})
	}
}

func TestHeartbeat_NullFieldsKeepState(t *testing.T) {
	s := play(start, turnStart,
		e(modevents.TypeToolCheck, `{"tool":"Bash","tool_use_id":"u1","decision":"ask"}`),
		e(modevents.TypeCompactStart, `{"trigger":"auto"}`),
		e(modevents.TypeAgentSpawn, `{"agent_id":"a1"}`),
		e(modevents.TypeBackground, `{"tasks":[],"crons":1}`),
		e(modevents.TypeHeartbeat, `{"turn_id":"t1","asks":null,"compacting":null,"agents":null,"error":null,"background":null}`))
	if s.Status() != agentpkg.StatusWaiting || !reflect.DeepEqual(s.Asks, map[string]bool{"u1": true}) {
		t.Fatalf("Status() = %q, Asks = %v", s.Status(), s.Asks)
	}
	if !s.Compacting || len(s.DotList()) != 1 || s.Background != BackgroundSchedule || s.Err {
		t.Fatalf("Compacting %v, dots %v, background %q, Err %v", s.Compacting, s.DotList(), s.Background, s.Err)
	}
}

func TestHeartbeat_ErrorFieldAbsentKeepsErr(t *testing.T) {
	hb := `{"asks":[],"compacting":false,"agents":[]}`
	cases := []struct {
		name string
		evs  []modevents.Event
		want bool
	}{
		{"absent keeps true", []modevents.Event{start, turnStart, turnError, e(modevents.TypeHeartbeat, hb)}, true},
		{"absent keeps false", []modevents.Event{start, turnStart, turnAnswer, e(modevents.TypeHeartbeat, hb)}, false},
		{"null keeps true", []modevents.Event{start, turnStart, turnError, e(modevents.TypeHeartbeat, `{"asks":[],"compacting":false,"agents":[],"error":null}`)}, true},
		{"present false clears", []modevents.Event{start, turnStart, turnError, e(modevents.TypeHeartbeat, `{"asks":[],"compacting":false,"agents":[],"error":false}`)}, false},
		{"present true sets", []modevents.Event{start, e(modevents.TypeHeartbeat, `{"asks":[],"compacting":false,"agents":[],"error":true}`)}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := play(c.evs...).Err; got != c.want {
				t.Fatalf("Err = %v, want %v", got, c.want)
			}
		})
	}
}

func TestHeartbeat_RestoresBackground(t *testing.T) {
	cases := []struct {
		name string
		evs  []modevents.Event
		want Background
	}{
		{"heartbeat background restores after a restart", []modevents.Event{e(modevents.TypeHeartbeat, `{"asks":[],"compacting":false,"agents":[],"background":{"tasks":[{"id":"w","type":"workflow","status":"running"}],"crons":0}}`)}, BackgroundWorkflow},
		{"heartbeat background restores after a switch", []modevents.Event{start,
			e(modevents.TypeBackground, `{"tasks":[{"id":"m","type":"monitor","status":"running"}],"crons":0}`),
			eSID(sidB, modevents.TypeSessionSwitch, `{"prev_sid":"`+sidA+`","source":"clear"}`),
			eSID(sidB, modevents.TypeHeartbeat, `{"asks":[],"compacting":false,"agents":[],"background":{"tasks":[{"id":"m","type":"monitor","status":"running"}],"crons":0}}`),
		}, BackgroundMonitor},
		{"heartbeat without background keeps it", []modevents.Event{start,
			e(modevents.TypeBackground, `{"tasks":[],"crons":3}`),
			e(modevents.TypeHeartbeat, `{"asks":[],"compacting":false,"agents":[]}`),
		}, BackgroundSchedule},
		{"heartbeat with null background keeps it", []modevents.Event{start,
			e(modevents.TypeBackground, `{"tasks":[],"crons":3}`),
			e(modevents.TypeHeartbeat, `{"asks":[],"compacting":false,"agents":[],"background":null}`),
		}, BackgroundSchedule},
		{"heartbeat with an empty background clears it", []modevents.Event{start,
			e(modevents.TypeBackground, `{"tasks":[],"crons":3}`),
			e(modevents.TypeHeartbeat, `{"asks":[],"compacting":false,"agents":[],"background":{"tasks":[],"crons":0}}`),
		}, ""},
		{"background event sets it", []modevents.Event{start, e(modevents.TypeBackground, `{"tasks":[{"id":"w","type":"workflow","status":"running"}],"crons":0}`)}, BackgroundWorkflow},
		{"later background event replaces it", []modevents.Event{start,
			e(modevents.TypeBackground, `{"tasks":[{"id":"w","type":"workflow","status":"running"}],"crons":0}`),
			e(modevents.TypeBackground, `{"tasks":[{"id":"s","type":"shell","status":"running"}],"crons":0}`),
		}, ""},
		{"undecodable background keeps it", []modevents.Event{start,
			e(modevents.TypeBackground, `{"tasks":[],"crons":3}`),
			e(modevents.TypeBackground, `{"tasks":{},"crons":0}`),
		}, BackgroundSchedule},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := play(c.evs...).Background; got != c.want {
				t.Fatalf("Background = %q, want %q", got, c.want)
			}
		})
	}
}

func TestBackgroundKind_Priority(t *testing.T) {
	task := func(typ string) Task { return Task{ID: typ + "-1", Type: typ, Status: "running"} }
	cases := []struct {
		name  string
		tasks []Task
		crons int
		want  Background
	}{
		{"nothing", nil, 0, ""},
		{"shell only", []Task{task("shell")}, 0, ""},
		{"subagent only", []Task{task("subagent")}, 0, ""},
		{"crons only", nil, 2, BackgroundSchedule},
		{"monitor", []Task{task("shell"), task("monitor")}, 0, BackgroundMonitor},
		{"monitor beats schedule", []Task{task("monitor")}, 1, BackgroundMonitor},
		{"workflow beats monitor and schedule", []Task{task("monitor"), task("workflow"), task("shell")}, 5, BackgroundWorkflow},
		{"status is not filtered", []Task{{ID: "w", Type: "workflow", Status: "completed"}}, 0, BackgroundWorkflow},
		{"negative crons is none", nil, -1, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := BackgroundKind(c.tasks, c.crons); got != c.want {
				t.Fatalf("BackgroundKind = %q, want %q", got, c.want)
			}
		})
	}
}

func TestApply_ChangedOnlyOnVisibleChange(t *testing.T) {
	hb := e(modevents.TypeHeartbeat, `{"turn_id":"t1","asks":[],"compacting":false,"agents":[{"id":"a1","status":"running"}],"error":false,"background":{"tasks":[],"crons":1}}`)
	s := NewStreamState("stream-test-1")
	steps := []struct {
		name string
		ev   modevents.Event
		want bool
	}{
		{"first heartbeat sets sid, status, dot, background", hb, true},
		{"identical heartbeat", hb, false},
		{"identical heartbeat with a later at", func() modevents.Event { x := hb; x.At = 99000; return x }(), false},
		{"usage", e(modevents.TypeUsage, `{}`), false},
		{"unknown type", e("turn.step", `{}`), false},
		{"turn.start of the running turn id", turnStart, false},
		{"tool.end of no ask", e(modevents.TypeToolEnd, `{"tool_use_id":"u9"}`), false},
		{"ask", e(modevents.TypeToolCheck, `{"tool":"Bash","tool_use_id":"u1","decision":"ask"}`), true},
		{"second ask, still waiting", e(modevents.TypeToolStart, `{"tool":"AskUserQuestion","tool_use_id":"u2"}`), false},
		{"background change", e(modevents.TypeBackground, `{"tasks":[{"id":"w","type":"workflow","status":"running"}],"crons":0}`), true},
		{"dot added", e(modevents.TypeAgentSpawn, `{"agent_id":"a2"}`), true},
		{"sid change only", eSID(sidB, modevents.TypeUsage, `{}`), true},
		{"compact start while waiting", eSID(sidB, modevents.TypeCompactStart, `{"trigger":"auto"}`), false},
		{"sid back to A", eSID(sidA, modevents.TypeUsage, `{}`), true},
	}
	for i, st := range steps {
		ev := st.ev
		ev.Seq = int64(i + 1)
		if ev.At == 0 {
			ev.At = int64(i+1) * 1000
		}
		if got := s.Apply(ev, t0.Add(time.Duration(i)*time.Second)); got != st.want {
			t.Fatalf("step %d %q: changed = %v, want %v", i, st.name, got, st.want)
		}
	}
	if !s.LastEvent.Equal(t0.Add(time.Duration(len(steps)-1) * time.Second)) {
		t.Fatalf("LastEvent = %v: every applied event updates it", s.LastEvent)
	}
}

func TestLive_Window(t *testing.T) {
	s := play(start) // LastEvent = t0
	cases := []struct {
		name string
		now  time.Time
		want bool
	}{
		{"at the event", t0, true},
		{"inside the window", t0.Add(29 * time.Second), true},
		{"exactly 30 s is live", t0.Add(LiveWindow), true},
		{"past 30 s is not", t0.Add(LiveWindow + time.Nanosecond), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := s.Live(c.now); got != c.want {
				t.Fatalf("Live(%v) = %v, want %v", c.now.Sub(t0), got, c.want)
			}
		})
	}
	if LiveWindow != 30*time.Second {
		t.Fatalf("LiveWindow = %v, want 30s", LiveWindow)
	}
	ended := play(start, e(modevents.TypeSessionEnd, `{"reason":"other"}`))
	if ended.Live(t0.Add(time.Second)) {
		t.Fatal("an ended stream is never live")
	}
}

func TestApply_NeverPanicsOnBadData(t *testing.T) {
	notObject := []string{``, `null`, `[]`, `"x"`, `{`, `{"a":`}
	wrongShape := []string{`{"turn_id":{}}`, `{"agents":[1,2]}`, `{"tasks":[null],"crons":"x"}`, `{"asks":[{}],"agents":null}`, `{"reason":5}`, `{"agent_id":[]}`}
	types := append(modevents.KnownTypes(), "nope")
	for _, typ := range types {
		for _, d := range append(notObject, wrongShape...) {
			s := play(start, turnStart, e(modevents.TypeAgentSpawn, `{"agent_id":"a1"}`))
			s.Apply(modevents.Event{Seq: 9, At: 9, SID: sidB, Type: typ, Data: json.RawMessage(d)}, t0.Add(time.Hour))
			// A session.end whose data decodes without a reason is a real end.
			if s.SID != sidB || !s.LastEvent.Equal(t0.Add(time.Hour)) || (s.Ended && typ != modevents.TypeSessionEnd) {
				t.Fatalf("%s %q: the any row must still apply (SID %q, Ended %v)", typ, d, s.SID, s.Ended)
			}
		}
		// Data that is not a JSON object cannot be decoded for any type:
		// nothing but the any row changes.
		for _, d := range notObject {
			s := play(start, turnStart, e(modevents.TypeAgentSpawn, `{"agent_id":"a1"}`))
			s.Apply(modevents.Event{Seq: 9, At: 9, SID: sidA, Type: typ, Data: json.RawMessage(d)}, t0.Add(time.Hour))
			if s.Status() != agentpkg.StatusRunning || len(s.DotList()) != 1 {
				t.Fatalf("%s %q: status %q, dots %v", typ, d, s.Status(), s.DotList())
			}
		}
	}
}
