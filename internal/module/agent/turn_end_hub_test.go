package agent

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	agentpkg "github.com/wake/purdex/internal/agent"
	agentcc "github.com/wake/purdex/internal/agent/cc"
)

// T-3a1: the accepted end of a Claude Code main turn, published to in-process
// subscribers without ever making the hook handler wait.

func newTurnEndModule(t *testing.T) *Module {
	t.Helper()
	m := newProvenanceTestModule(t, "inst-1")
	m.registry = agentpkg.NewRegistry()
	m.registry.Register(agentcc.NewProvider(nil, nil, nil, nil)) // the real provider: catalog, lifecycle and IdentifyEvent
	m.registry.Register(&fakeAgentProvider{typeName: "codex", derive: deriveWithSessionDetail})
	return m
}

// postStop sends a PdxStop from the root pane process, after the SessionStart
// that gives it a frame.
func postStop(t *testing.T, m *Module, raw string) int {
	t.Helper()
	withProcessTree(t, map[int]int{100: 999})
	return postHookEvent(t, m, "PdxStop", 100, "t100", raw).Code
}

func startSession(t *testing.T, m *Module, sid string) {
	t.Helper()
	if rec := postRootSessionStart(t, m, `{"session_id":"`+sid+`","cwd":"/w","source":"startup"}`); rec.Code != http.StatusOK {
		t.Fatalf("session start answered %d", rec.Code)
	}
}

func wantNoTurnEnd(t *testing.T, got chan TurnEndEvent) {
	t.Helper()
	select {
	case ev := <-got:
		t.Fatalf("published: %+v", ev)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestHandler_PublishesTurnEndForAcceptedStop(t *testing.T) {
	m := newTurnEndModule(t)
	got := make(chan TurnEndEvent, 4)
	defer m.SubscribeTurnEnd(func(ev TurnEndEvent) { got <- ev })()
	startSession(t, m, "S1")
	before := time.Now().UnixMilli()
	if code := postStop(t, m, `{"hook_event_name":"Stop","session_id":"S1","cwd":"/w","last_assistant_message":"做完了。接著測試。"}`); code != 200 {
		t.Fatalf("stop answered %d", code)
	}
	select {
	case ev := <-got:
		if ev.SessionID != "S1" || ev.Text != "做完了。接著測試。" || ev.Seq <= 0 || ev.At < before || ev.At > time.Now().UnixMilli() {
			t.Fatalf("event = %+v", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no TurnEndEvent for an accepted Stop")
	}
}

// Mutation gate: read the session id from the status detail instead of the provider → red.
func TestHandler_TurnEndSessionIDComesFromTheRawEvent(t *testing.T) {
	m := newTurnEndModule(t)
	got := make(chan TurnEndEvent, 4)
	defer m.SubscribeTurnEnd(func(ev TurnEndEvent) { got <- ev })()
	startSession(t, m, "S2")
	postStop(t, m, `{"hook_event_name":"Stop","session_id":"S2","last_assistant_message":"x"}`)
	if ev := <-got; ev.SessionID != "S2" {
		t.Fatalf("session = %q", ev.SessionID)
	}
}

func TestHandler_TurnEndNotForSubagentStop(t *testing.T) {
	m := newTurnEndModule(t)
	got := make(chan TurnEndEvent, 4)
	defer m.SubscribeTurnEnd(func(ev TurnEndEvent) { got <- ev })()
	startSession(t, m, "S3")
	withProcessTree(t, map[int]int{100: 999})
	postHookEvent(t, m, "PdxSubagentStop", 100, "t100", `{"hook_event_name":"SubagentStop","session_id":"S3","agent_id":"a1","last_assistant_message":"sub"}`)
	wantNoTurnEnd(t, got)
}

func TestHandler_TurnEndNotWithoutSessionID(t *testing.T) {
	m := newTurnEndModule(t)
	got := make(chan TurnEndEvent, 4)
	defer m.SubscribeTurnEnd(func(ev TurnEndEvent) { got <- ev })()
	startSession(t, m, "S4")
	postStop(t, m, `{"hook_event_name":"Stop","cwd":"/w","last_assistant_message":"no id"}`)
	wantNoTurnEnd(t, got)
}

func TestHandler_TurnEndNotForARejectedEvent(t *testing.T) {
	m := newTurnEndModule(t)
	got := make(chan TurnEndEvent, 4)
	defer m.SubscribeTurnEnd(func(ev TurnEndEvent) { got <- ev })()
	startSession(t, m, "S5")
	old := verifyEventFn
	verifyEventFn = func(*Module, EventRequest) verifyDecision { return verifyDecision{Reason: "pid_reused"} }
	defer func() { verifyEventFn = old }()
	postStop(t, m, `{"hook_event_name":"Stop","session_id":"S5","last_assistant_message":"rejected"}`)
	wantNoTurnEnd(t, got)
}

func TestHandler_TurnEndNotForAnotherProvider(t *testing.T) {
	m := newTurnEndModule(t)
	got := make(chan TurnEndEvent, 4)
	defer m.SubscribeTurnEnd(func(ev TurnEndEvent) { got <- ev })()
	withProcessTree(t, map[int]int{100: 999})
	postHookEventAs(t, m, "codex", "PdxStop", 100, "t100", `{"hook_event_name":"Stop","session_id":"C1","last_assistant_message":"codex"}`)
	wantNoTurnEnd(t, got)
}

// Two Stops of one session: the first is delayed in verification, the second
// finishes first. The stamps keep the arrival order. Mutation gate: stamp at
// publish → the first-arrived Stop carries the larger Seq → red.
func TestHandler_TurnEndStampedAtEntry(t *testing.T) {
	m := newTurnEndModule(t)
	got := make(chan TurnEndEvent, 4)
	defer m.SubscribeTurnEnd(func(ev TurnEndEvent) { got <- ev })()
	startSession(t, m, "S6")
	release := make(chan struct{})
	entered := make(chan struct{})
	var once sync.Once
	old := verifyEventFn
	verifyEventFn = func(mm *Module, req EventRequest) verifyDecision {
		if req.PurdexName == "PdxStop" {
			first := false
			once.Do(func() { first = true })
			if first {
				close(entered)
				<-release
			}
		}
		return old(mm, req)
	}
	defer func() { verifyEventFn = old }()
	withProcessTree(t, map[int]int{100: 999})
	done := make(chan struct{})
	go func() {
		postHookEvent(t, m, "PdxStop", 100, "t100", `{"hook_event_name":"Stop","session_id":"S6","last_assistant_message":"first"}`)
		close(done)
	}()
	<-entered
	postHookEvent(t, m, "PdxStop", 100, "t100", `{"hook_event_name":"Stop","session_id":"S6","last_assistant_message":"second"}`)
	close(release)
	<-done
	a, b := <-got, <-got
	if a.Text != "second" || b.Text != "first" {
		t.Fatalf("published order = %q, %q (the second finished first)", a.Text, b.Text)
	}
	if !(b.Seq < a.Seq) || b.At > a.At {
		t.Fatalf("stamps follow publication, not arrival: first %+v second %+v", b, a)
	}
}

// ---- the hub ----

// Mutation gate: a blocking send → 1000 publishes against a stuck subscriber never return → red.
func TestTurnEndHub_PublishNeverWaits(t *testing.T) {
	var h turnEndHub
	stuck := make(chan struct{})
	unsub := h.subscribe(func(TurnEndEvent) { <-stuck })
	defer func() { close(stuck); unsub() }()
	done := make(chan struct{})
	start := time.Now()
	go func() {
		for i := 0; i < 1000; i++ {
			h.publish(TurnEndEvent{SessionID: "S", Seq: int64(i)})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("publish waited for a stuck subscriber")
	}
	if time.Since(start) > time.Second {
		t.Fatalf("1000 publishes took %s", time.Since(start))
	}
	if d := h.Dropped(); d < 1000-turnEndSubBuffer-1 {
		t.Fatalf("dropped = %d, want about %d", d, 1000-turnEndSubBuffer)
	}
}

// 88's gate (the 610 lesson): a subscriber that is stuck with a full queue
// does not slow the hook handler: every Stop still answers, promptly.
func TestHandler_StuckSubscriberDoesNotSlowTheHook(t *testing.T) {
	m := newTurnEndModule(t)
	stuck := make(chan struct{})
	defer m.SubscribeTurnEnd(func(TurnEndEvent) { <-stuck })()
	defer close(stuck)
	startSession(t, m, "S7")
	start := time.Now()
	for i := 0; i < 3*turnEndSubBuffer; i++ {
		if code := postStop(t, m, `{"hook_event_name":"Stop","session_id":"S7","last_assistant_message":"x"}`); code != 200 {
			t.Fatalf("stop %d answered %d", i, code)
		}
	}
	if el := time.Since(start); el > 20*time.Second {
		t.Fatalf("%d hooks took %s with a stuck subscriber", 3*turnEndSubBuffer, el)
	}
	if m.turnEnds.Dropped() == 0 {
		t.Fatal("the queue never filled: the test did not reach the full-queue case")
	}
}

// Run with -race: subscribe, publish and unsubscribe from many goroutines.
func TestTurnEndHub_UnsubscribeRace(t *testing.T) {
	var h turnEndHub
	var got atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				unsub := h.subscribe(func(TurnEndEvent) { got.Add(1) })
				h.publish(TurnEndEvent{SessionID: "S"})
				unsub()
				unsub() // idempotent
				h.publish(TurnEndEvent{SessionID: "S"})
			}
		}()
	}
	wg.Wait()
}

func TestTurnEndHub_PublishAfterUnsubscribeDeliversNothing(t *testing.T) {
	var h turnEndHub
	got := make(chan TurnEndEvent, 4)
	unsub := h.subscribe(func(ev TurnEndEvent) { got <- ev })
	unsub()
	h.publish(TurnEndEvent{SessionID: "S"})
	select {
	case ev := <-got:
		t.Fatalf("delivered after unsubscribe: %+v", ev)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestTurnEndHub_ASubscriberPanicDoesNotEndDelivery(t *testing.T) {
	var h turnEndHub
	got := make(chan TurnEndEvent, 4)
	defer h.subscribe(func(ev TurnEndEvent) {
		if ev.Seq == 1 {
			panic("boom")
		}
		got <- ev
	})()
	h.publish(TurnEndEvent{Seq: 1})
	h.publish(TurnEndEvent{Seq: 2})
	select {
	case ev := <-got:
		if ev.Seq != 2 {
			t.Fatalf("event = %+v", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("delivery stopped after a panic")
	}
}

// postHookEventAs is postHookEvent for another agent type.
func postHookEventAs(t *testing.T, m *Module, agentType, purdexName string, senderPID int, startTime, raw string) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"tmux_session": "work", "tmux_pane_id": "%5", "sender_pid": senderPID, "sender_start_time": startTime,
		"purdex_name": purdexName, "agent_type": agentType, "raw_event": json.RawMessage(raw),
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/api/agent/event", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	m.handleEvent(w, req)
	return w
}

// A Stop whose frame application was skipped changed nothing and ends no
// tracked turn. Mutation gate: drop the skipped check → red.
func TestPublishTurnEnd_NotWhenTheFrameEventWasSkipped(t *testing.T) {
	m := newTurnEndModule(t)
	got := make(chan TurnEndEvent, 4)
	defer m.SubscribeTurnEnd(func(ev TurnEndEvent) { got <- ev })()
	p, _ := m.registry.Get("cc")
	req := EventRequest{AgentType: "cc", PurdexName: "PdxStop", RawEvent: []byte(`{"session_id":"S","last_assistant_message":"x"}`)}
	m.publishTurnEnd(req, p, agentpkg.LifecycleStop, FrameTraceMeta{Decision: "skipped", Reason: "pid_reused"}, turnEndStamp{at: 1, seq: 1})
	wantNoTurnEnd(t, got)
	m.publishTurnEnd(req, p, agentpkg.LifecycleStop, FrameTraceMeta{Decision: "updated_frame"}, turnEndStamp{at: 2, seq: 2})
	select {
	case ev := <-got:
		if ev.At != 2 || ev.Seq != 2 || ev.SessionID != "S" {
			t.Fatalf("event = %+v", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("an updated_frame Stop was not published")
	}
}
