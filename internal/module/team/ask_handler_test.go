package teammod

import (
	"bytes"
	"encoding/json"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/wake/purdex/internal/team"
)

const askQuestions = `[{"question":"紅還是藍？","header":"顏色","options":[{"label":"紅","description":"r"},{"label":"藍","description":"b"}],"multiSelect":false}]`

func askBeginBody(sid, toolUse string) team.AskBeginRequest {
	return team.AskBeginRequest{SessionID: sid, ToolUseID: toolUse, Kind: team.KindHookAsk, Payload: json.RawMessage(`{"questions":` + askQuestions + `}`)}
}

// askBegin opens a hook_ask for sid-1 / toolUse and returns its id.
func (f *fixture) askBegin(toolUse string) string {
	f.t.Helper()
	code, body := f.do(http.MethodPost, "/api/ask/begin", askBeginBody("sid-1", toolUse))
	if code != http.StatusCreated {
		f.t.Fatalf("ask begin: %d %s", code, body)
	}
	var out team.AskBeginResponse
	if err := json.Unmarshal(body, &out); err != nil || out.ID == "" {
		f.t.Fatalf("ask begin body %s: %v", body, err)
	}
	return out.ID
}

// askBeginPermission opens a hook_permission (Bash `ls`) for sid-2 / toolUse
// and returns its id.
func (f *fixture) askBeginPermission(toolUse string) string {
	f.t.Helper()
	code, body := f.do(http.MethodPost, "/api/ask/begin", team.AskBeginRequest{SessionID: "sid-2", ToolUseID: toolUse, Kind: team.KindHookPermission,
		Payload: json.RawMessage(`{"tool_name":"Bash","tool_input":{"command":"ls"}}`)})
	var out team.AskBeginResponse
	if err := json.Unmarshal(body, &out); err != nil || code != http.StatusCreated || out.ID == "" {
		f.t.Fatalf("permission begin: %d %s (%v)", code, body, err)
	}
	return out.ID
}

func decodeWait(t *testing.T, body []byte) team.AskWaitResponse {
	t.Helper()
	var w team.AskWaitResponse
	if err := json.Unmarshal(body, &w); err != nil {
		t.Fatalf("decode wait: %v; body=%s", err, body)
	}
	return w
}

func appClient() team.Client { return team.Client{Kind: "app", Label: "Purdex iOS @ phone"} }

// Mutation gate: with no subscriber RemoteResponders.Any() is false, begin
// answers 409 no_responders and opens nothing (invert Any ⇒ red).
func TestAskBegin_NoRespondersOpensNothing(t *testing.T) {
	f := newFixture(t)
	f.core.Events.RemoveTestSubscriber(f.sub)
	code, body := f.do(http.MethodPost, "/api/ask/begin", askBeginBody("sid-1", "toolu_1"))
	if code != http.StatusConflict || decodeErr(t, body).Error != team.ErrNoResponders {
		t.Fatalf("begin without subscribers = %d %s", code, body)
	}
	open, err := f.m.store.ListOpen()
	if err != nil || len(open) != 0 {
		t.Fatalf("open rows = %v err=%v, want none", open, err)
	}
	f.sub = f.core.Events.AddTestSubscriber() // for the fixture's Cleanup
}

func TestAskBegin_OpensRowWithNoDeadlineAndBroadcasts(t *testing.T) {
	f := newFixture(t)
	id := f.askBegin("toolu_1")
	a, ok, err := f.m.store.Get(id)
	if err != nil || !ok {
		t.Fatalf("get: ok=%v err=%v", ok, err)
	}
	if a.Kind != team.KindHookAsk || a.State != team.StateOpen || a.Origin.SessionID != "sid-1" || a.DeadlineAt != team.NoExpiryAt || a.LeaseUntil != f.clock.Load()+team.LeaseS*1000 {
		t.Fatalf("row = %+v", a)
	}
	var p team.HookAskPayload
	if err := json.Unmarshal(a.Payload, &p); err != nil || p.ToolUseID != "toolu_1" || p.TerminalOnly || string(p.Questions) != askQuestions {
		t.Fatalf("payload = %s (%v)", a.Payload, err)
	}
	evs := f.events()
	if len(evs) != 1 || evs[0].Op != "opened" || evs[0].Approval.ID != id {
		t.Fatalf("events = %+v", evs)
	}
	// A second begin for the same tool use is 409 ask_open carrying the row.
	code, body := f.do(http.MethodPost, "/api/ask/begin", askBeginBody("sid-1", "toolu_1"))
	e := decodeErr(t, body)
	if code != http.StatusConflict || e.Error != team.ErrAskOpen || e.Approval == nil || e.Approval.ID != id {
		t.Fatalf("second begin = %d %s", code, body)
	}
	// Inflight does not count hook rows: a restart does not interrupt them.
	code, body = f.do(http.MethodGet, "/api/team/inflight", nil)
	var inf team.InflightResponse
	if err := json.Unmarshal(body, &inf); err != nil || code != 200 || inf.ApprovalsOpen != 0 {
		t.Fatalf("inflight = %d %s", code, body)
	}
}

func TestAskBegin_Rejections(t *testing.T) {
	f := newFixture(t)
	cases := []struct {
		name string
		body any
		code int
		err  string
	}{
		{"unknown session", askBeginBody("sid-9", "t"), http.StatusNotFound, team.ErrUnknownSession},
		{"no questions", team.AskBeginRequest{SessionID: "sid-1", ToolUseID: "t", Kind: team.KindHookAsk, Payload: json.RawMessage(`{"questions":[]}`)}, http.StatusBadRequest, team.ErrBadRequest},
		{"bad kind", team.AskBeginRequest{SessionID: "sid-1", ToolUseID: "t", Kind: team.KindLead}, http.StatusBadRequest, team.ErrBadRequest},
		{"no tool use", team.AskBeginRequest{SessionID: "sid-1", Kind: team.KindHookAsk, Payload: json.RawMessage(`{"questions":` + askQuestions + `}`)}, http.StatusBadRequest, team.ErrBadRequest},
		{"permission needs tool_name", team.AskBeginRequest{SessionID: "sid-1", ToolUseID: "t", Kind: team.KindHookPermission, Payload: json.RawMessage(`{}`)}, http.StatusBadRequest, team.ErrBadRequest},
	}
	for _, c := range cases {
		code, body := f.do(http.MethodPost, "/api/ask/begin", c.body)
		if code != c.code || decodeErr(t, body).Error != c.err {
			t.Errorf("%s: %d %s, want %d %s", c.name, code, body, c.code, c.err)
		}
	}
	if n := f.countOps("opened"); n != 0 {
		t.Fatalf("opened = %d, want 0", n)
	}
}

// Remote first (step 4): decide closes approved with the answers; wait
// answers answered_remote; a late answered_local is a terminal_override
// with a second closed (step 5).
func TestAsk_RemoteFirstThenLateTerminalIsOverride(t *testing.T) {
	f := newFixture(t)
	id := f.askBegin("toolu_2")
	f.events()
	code, body := f.do(http.MethodPost, "/api/team/approvals/"+id+"/decide",
		team.DecideRequest{Decision: "approve", Hook: &team.HookDecision{Answers: map[string]string{"紅還是藍？": "藍"}}, Client: appClient()})
	if code != http.StatusOK {
		t.Fatalf("decide = %d %s", code, body)
	}
	a := decodeApproval(t, body)
	if a.State != team.StateApproved || a.Hook == nil || a.Hook.Answers["紅還是藍？"] != "藍" || a.Grant != nil || a.DecidedBy.Label != "Purdex iOS @ phone" {
		t.Fatalf("decided row = %+v hook=%+v", a, a.Hook)
	}
	code, body = f.do(http.MethodGet, "/api/ask/wait/"+id+"?wait=25", nil)
	w := decodeWait(t, body)
	if code != 200 || w.State != team.AskAnsweredRemote || w.Hook == nil || w.Hook.Answers["紅還是藍？"] != "藍" {
		t.Fatalf("wait = %d %+v", code, w)
	}
	evs := f.events()
	if len(evs) != 1 || evs[0].Op != "closed" || evs[0].Approval.State != team.StateApproved {
		t.Fatalf("events after decide = %+v", evs)
	}
	// The terminal had already shown 紅: its answer stands.
	code, body = f.do(http.MethodPost, "/api/ask/report/"+id,
		team.AskReportRequest{State: team.StateAnsweredLocal, Hook: &team.HookDecision{Answers: map[string]string{"紅還是藍？": "紅"}}})
	a = decodeApproval(t, body)
	if code != 200 || a.State != team.StateTerminalOverride || a.Hook.Answers["紅還是藍？"] != "紅" || a.DecidedBy == nil || a.DecidedBy.Kind != team.ClientKindTerminal {
		t.Fatalf("override = %d %+v hook=%+v by=%+v", code, a, a.Hook, a.DecidedBy)
	}
	evs = f.events()
	if len(evs) != 1 || evs[0].Op != "closed" || evs[0].Approval.State != team.StateTerminalOverride || evs[0].Approval.DecidedBy.Kind != team.ClientKindTerminal {
		t.Fatalf("second closed = %+v", evs)
	}
	// A repeat changes nothing and broadcasts nothing.
	code, body = f.do(http.MethodPost, "/api/ask/report/"+id,
		team.AskReportRequest{State: team.StateAnsweredLocal, Hook: &team.HookDecision{Answers: map[string]string{"紅還是藍？": "紅"}}})
	if code != 200 || decodeApproval(t, body).State != team.StateTerminalOverride || f.countOps("closed") != 0 {
		t.Fatalf("repeat = %d %s", code, body)
	}
}

// Terminal first (step 3): answered_local closes with the terminal's
// answers and one closed; a late decide gets 409 already_decided carrying
// decided_by terminal.
func TestAsk_TerminalFirstThenLateDecideIs409(t *testing.T) {
	f := newFixture(t)
	id := f.askBegin("toolu_3")
	f.events()
	code, body := f.do(http.MethodPost, "/api/ask/report/"+id,
		team.AskReportRequest{State: team.StateAnsweredLocal, Hook: &team.HookDecision{Answers: map[string]string{"紅還是藍？": "紅"}}})
	a := decodeApproval(t, body)
	if code != 200 || a.State != team.StateAnsweredLocal || a.Hook.Answers["紅還是藍？"] != "紅" || a.DecidedBy.Kind != team.ClientKindTerminal {
		t.Fatalf("report = %d %+v", code, a)
	}
	if evs := f.events(); len(evs) != 1 || evs[0].Op != "closed" || evs[0].Approval.State != team.StateAnsweredLocal {
		t.Fatalf("events = %+v", evs)
	}
	code, body = f.do(http.MethodGet, "/api/ask/wait/"+id+"?wait=25", nil)
	if w := decodeWait(t, body); code != 200 || w.State != team.AskClosed || w.Reason != string(team.StateAnsweredLocal) {
		t.Fatalf("wait = %d %+v", code, w)
	}
	code, body = f.do(http.MethodPost, "/api/team/approvals/"+id+"/decide",
		team.DecideRequest{Decision: "approve", Hook: &team.HookDecision{Answers: map[string]string{"紅還是藍？": "藍"}}, Client: appClient()})
	e := decodeErr(t, body)
	if code != http.StatusConflict || e.Error != team.ErrAlreadyDecided || e.Approval == nil || e.Approval.DecidedBy == nil || e.Approval.DecidedBy.Kind != team.ClientKindTerminal {
		t.Fatalf("late decide = %d %s", code, body)
	}
	if f.countOps("closed") != 0 {
		t.Fatal("a lost decide must not broadcast")
	}
}

// Mutation gate (spec §15): a hook_ask row closes through the same CAS as a
// lead request — of a remote decide and a terminal report racing, exactly
// one wins; the loser sees the winner's row. Drop the CAS ⇒ both win ⇒ red.
func TestAsk_DecideAndReportRaceExactlyOneWins(t *testing.T) {
	for round := 0; round < 20; round++ {
		f := newFixture(t)
		id := f.askBegin("toolu_race")
		f.events()
		var wg sync.WaitGroup
		codes := make([]int, 2)
		wg.Add(2)
		go func() {
			defer wg.Done()
			codes[0], _ = f.do(http.MethodPost, "/api/team/approvals/"+id+"/decide",
				team.DecideRequest{Decision: "approve", Hook: &team.HookDecision{Answers: map[string]string{"q": "藍"}}, Client: appClient()})
		}()
		go func() {
			defer wg.Done()
			codes[1], _ = f.do(http.MethodPost, "/api/ask/report/"+id,
				team.AskReportRequest{State: team.StateDismissed})
		}()
		wg.Wait()
		// decide: 200 won / 409 lost. report dismissed: 200 either way (idempotent), so count closes by events.
		closes := 0
		for _, ev := range f.events() {
			if ev.Op == "closed" {
				closes++
			}
		}
		if closes != 1 {
			t.Fatalf("round %d: %d closed events, want exactly 1 (codes %v)", round, closes, codes)
		}
		a, _, _ := f.m.store.Get(id)
		if (codes[0] == 200) != (a.State == team.StateApproved) {
			t.Fatalf("round %d: decide=%d but state=%s", round, codes[0], a.State)
		}
	}
}

func TestAsk_DismissedClosesAndWaitSaysSo(t *testing.T) {
	f := newFixture(t)
	id := f.askBegin("toolu_4")
	f.events()
	code, body := f.do(http.MethodPost, "/api/ask/report/"+id, team.AskReportRequest{State: team.StateDismissed})
	if code != 200 || decodeApproval(t, body).State != team.StateDismissed {
		t.Fatalf("dismiss = %d %s", code, body)
	}
	if evs := f.events(); len(evs) != 1 || evs[0].Approval.State != team.StateDismissed || evs[0].Approval.DecidedBy != nil {
		t.Fatalf("events = %+v", evs)
	}
	code, body = f.do(http.MethodGet, "/api/ask/wait/"+id, nil)
	if w := decodeWait(t, body); code != 200 || w.State != team.AskClosed || w.Reason != "dismissed" {
		t.Fatalf("wait = %d %+v", code, w)
	}
	code, body = f.do(http.MethodPost, "/api/ask/report/"+id, team.AskReportRequest{State: "approved"})
	if code != http.StatusBadRequest {
		t.Fatalf("bad state = %d %s", code, body)
	}
}

func TestAsk_WaitLongPollWakesOnDecide(t *testing.T) {
	f := newFixture(t)
	id := f.askBegin("toolu_5")
	done := make(chan team.AskWaitResponse, 1)
	go func() {
		_, body := f.do(http.MethodGet, "/api/ask/wait/"+id+"?wait=25", nil)
		done <- decodeWait(t, body)
	}()
	waitForWaiter(t, f, id)
	f.do(http.MethodPost, "/api/team/approvals/"+id+"/decide",
		team.DecideRequest{Decision: "approve", Hook: &team.HookDecision{Answers: map[string]string{"紅還是藍？": "藍"}}, Client: appClient()})
	w := <-done
	if w.State != team.AskAnsweredRemote || w.Hook == nil {
		t.Fatalf("wait woke with %+v", w)
	}
}

func TestDecide_HookAskNeedsAnswers_TerminalOnlyIsReadOnly(t *testing.T) {
	f := newFixture(t)
	id := f.askBegin("toolu_6")
	code, body := f.do(http.MethodPost, "/api/team/approvals/"+id+"/decide", team.DecideRequest{Decision: "approve", Client: appClient()})
	if code != http.StatusBadRequest {
		t.Fatalf("approve without answers = %d %s", code, body)
	}
	code, body = f.do(http.MethodPost, "/api/team/approvals/"+id+"/decide", team.DecideRequest{Decision: "deny", Client: appClient()})
	if code != http.StatusBadRequest {
		t.Fatalf("deny a hook_ask = %d %s", code, body)
	}
	// A terminal_only row (opened by the settings hook path) cannot be decided.
	payload, _ := hookPayloadFor(team.KindHookAsk, "toolu_7", json.RawMessage(`{"questions":`+askQuestions+`}`), true)
	f.m.createMu.Lock()
	ro, err := f.m.openHookRow(fixtureOrigins["/tmp/10.sock"], team.KindHookAsk, payload, true)
	f.m.createMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if ro.LeaseUntil != team.NoExpiryAt {
		t.Fatalf("terminal_only lease = %d, want NoExpiryAt (nobody polls it)", ro.LeaseUntil)
	}
	code, body = f.do(http.MethodPost, "/api/team/approvals/"+ro.ID+"/decide",
		team.DecideRequest{Decision: "approve", Hook: &team.HookDecision{Answers: map[string]string{"q": "a"}}, Client: appClient()})
	if e := decodeErr(t, body); code != http.StatusConflict || e.Error != team.ErrTerminalOnly || e.Detail != "這題只能在終端機回答" {
		t.Fatalf("decide terminal_only = %d %s", code, body)
	}
	// The mod's begin for the same tool use takes it over: dismissed + a fresh answerable row.
	f.events()
	newID := f.askBegin("toolu_7")
	evs := f.events()
	if newID == ro.ID || len(evs) != 2 || evs[0].Op != "closed" || evs[0].Approval.ID != ro.ID || evs[0].Approval.State != team.StateDismissed || evs[1].Op != "opened" || evs[1].Approval.ID != newID {
		t.Fatalf("takeover events = %+v", evs)
	}
}

// The sweeper never times a hook row out, and a mod-raised row whose poller
// stopped is abandoned when its lease runs out (the mod died with the
// dialog still up); a terminal_only row is not (no lease).
func TestAsk_SweeperLeaseButNoDeadline(t *testing.T) {
	f := newFixture(t)
	id := f.askBegin("toolu_8")
	payload, _ := hookPayloadFor(team.KindHookAsk, "toolu_9", json.RawMessage(`{"questions":`+askQuestions+`}`), true)
	f.m.createMu.Lock()
	ro, _ := f.m.openHookRow(fixtureOrigins["/tmp/10.sock"], team.KindHookAsk, payload, true)
	f.m.createMu.Unlock()
	f.clock.Add(11 * 60 * 1000) // past any lead deadline and the 30 s lease
	f.m.tick()
	a, _, _ := f.m.store.Get(id)
	if a.State != team.StateAbandoned {
		t.Fatalf("mod row after lease = %s, want abandoned", a.State)
	}
	b, _, _ := f.m.store.Get(ro.ID)
	if b.State != team.StateOpen {
		t.Fatalf("terminal_only row = %s, want still open", b.State)
	}
}

// Fix note (P8a-1a R1): decide on a hook row stores the client's answer
// from req.Hook (in grant_json's place), read back from team.db: answers for
// hook_ask, allow + updated_input / deny + message for hook_permission.
// Mutation gate: decideHook closing without the hook ⇒ red.
func TestDecide_HookDecisionIsStoredFromTheRequest(t *testing.T) {
	f := newFixture(t)
	perm := func(toolUse string) string {
		code, body := f.do(http.MethodPost, "/api/ask/begin", team.AskBeginRequest{SessionID: "sid-2", ToolUseID: toolUse, Kind: team.KindHookPermission,
			Payload: json.RawMessage(`{"tool_name":"Bash","tool_input":{"command":"ls"}}`)})
		var out team.AskBeginResponse
		if err := json.Unmarshal(body, &out); err != nil || code != http.StatusCreated {
			t.Fatalf("permission begin: %d %s", code, body)
		}
		return out.ID
	}
	ask, allow, deny := f.askBegin("toolu_s1"), perm("toolu_p1"), perm("toolu_p2")
	for _, c := range []struct {
		id    string
		req   team.DecideRequest
		state team.State
		ok    func(h *team.HookDecision) bool
	}{
		{ask, team.DecideRequest{Decision: "approve", Hook: &team.HookDecision{Answers: map[string]string{"紅還是藍？": "藍"}}, Client: appClient()}, team.StateApproved,
			func(h *team.HookDecision) bool { return h.Answers["紅還是藍？"] == "藍" }},
		{allow, team.DecideRequest{Decision: "approve", Hook: &team.HookDecision{UpdatedInput: json.RawMessage(`{"command":"ls -la"}`)}, Client: appClient()}, team.StateApproved,
			func(h *team.HookDecision) bool {
				return h.Behavior == "allow" && string(h.UpdatedInput) == `{"command":"ls -la"}`
			}},
		{deny, team.DecideRequest{Decision: "deny", Hook: &team.HookDecision{Message: "不要"}, Client: appClient()}, team.StateDenied,
			func(h *team.HookDecision) bool { return h.Behavior == "deny" && h.Message == "不要" }},
	} {
		if code, body := f.do(http.MethodPost, "/api/team/approvals/"+c.id+"/decide", c.req); code != http.StatusOK {
			t.Fatalf("decide %s: %d %s", c.id, code, body)
		}
		a, ok, err := f.m.store.Get(c.id)
		if err != nil || !ok || a.State != c.state || a.Grant != nil || a.Hook == nil || !c.ok(a.Hook) {
			t.Fatalf("stored row %s = %+v hook=%+v (ok=%v err=%v)", c.id, a, a.Hook, ok, err)
		}
	}
}

// Fix note (P8a-1a R1): inflight counts lead / self_relay rows only, while
// the open list still shows hook rows (the phone reads it). Mutation gate:
// handleInflight back on ListOpen ⇒ approvals_open 2 ⇒ red.
func TestInflight_ExcludesHookRowsButListShowsThem(t *testing.T) {
	f := newFixture(t)
	f.create(uid(1))
	f.askBegin("toolu_i1")
	code, body := f.do(http.MethodGet, "/api/team/inflight", nil)
	var inf team.InflightResponse
	if err := json.Unmarshal(body, &inf); err != nil || code != 200 || inf.ApprovalsOpen != 1 {
		t.Fatalf("inflight = %d %s, want approvals_open 1 (the lead row only)", code, body)
	}
	code, body = f.do(http.MethodGet, "/api/team/approvals?state=open", nil)
	var list struct {
		Approvals []team.Approval `json:"approvals"`
	}
	if err := json.Unmarshal(body, &list); err != nil || code != 200 || len(list.Approvals) != 2 {
		t.Fatalf("open list = %d %s, want both rows", code, body)
	}
}

// Fix note (P8a-1a R2): OpenByToolUse has no unique index, so begin's
// read-then-insert is serialised by createMu. afterOpenByToolUse pauses the
// first begin between its lookup and its insert; a second begin for the same
// tool use must wait for the lock, then answer 409 ask_open carrying the
// first row. Mutation gate: no createMu in handleAskBegin ⇒ two rows ⇒ red.
func TestAskBegin_ConcurrentBeginsForOneToolUseOpenOne(t *testing.T) {
	f := newFixture(t)
	entered, release := make(chan struct{}), make(chan struct{})
	f.m.afterOpenByToolUse = func() {
		close(entered)
		<-release
	}
	first := make(chan []byte, 1)
	go func() {
		code, body := f.do(http.MethodPost, "/api/ask/begin", askBeginBody("sid-1", "toolu_c"))
		if code != http.StatusCreated {
			t.Errorf("first begin: %d %s", code, body)
		}
		first <- body
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the first begin did not reach afterOpenByToolUse")
	}
	f.m.afterOpenByToolUse = nil // only the paused begin uses the barrier
	second := make(chan []byte, 1)
	go func() {
		_, body := f.do(http.MethodPost, "/api/ask/begin", askBeginBody("sid-1", "toolu_c"))
		second <- body
	}()
	select {
	case body := <-second:
		close(release)
		t.Fatalf("second begin answered %s while the first was between its lookup and its insert; it must wait for createMu", body)
	case <-time.After(200 * time.Millisecond):
	}
	close(release)
	var out team.AskBeginResponse
	if err := json.Unmarshal(<-first, &out); err != nil {
		t.Fatal(err)
	}
	if e := decodeErr(t, <-second); e.Error != team.ErrAskOpen || e.Approval == nil || e.Approval.ID != out.ID {
		t.Fatalf("second begin = %+v, want ask_open carrying %s", e, out.ID)
	}
	if open, err := f.m.store.ListOpen(); err != nil || len(open) != 1 {
		t.Fatalf("open rows = %d err=%v, want 1", len(open), err)
	}
}

// pollRow serves three routes — GET /api/team/approvals/{id}, P5a-2a's
// /api/relay/wait/{id} (handleGet under another path) and /api/ask/wait/{id}:
// each renews the lease, long-polls until the close, and answers 404 for an
// unknown id and 400 for a bad wait. Mutation gate: drop RenewLease, or the
// wait, in pollRow ⇒ red on every route.
func TestPollRoutes_RenewLeaseLongPollAnd404(t *testing.T) {
	f := newFixture(t)
	lead, relay, ask := f.create(uid(1)).ID, f.begin("sid-2").RequestID, f.askBegin("toolu_poll")
	for _, c := range []struct{ path, id string }{{"/api/team/approvals/", lead}, {"/api/relay/wait/", relay}, {"/api/ask/wait/", ask}} {
		f.clock.Add(10_000)
		if code, body := f.do(http.MethodGet, c.path+c.id+"?wait=0", nil); code != 200 {
			t.Fatalf("%s: %d %s", c.path, code, body)
		}
		if a, _, _ := f.m.store.Get(c.id); a.LeaseUntil != f.clock.Load()+team.LeaseS*1000 {
			t.Fatalf("%s did not renew the lease: %d, want %d", c.path, a.LeaseUntil, f.clock.Load()+team.LeaseS*1000)
		}
		if code, body := f.do(http.MethodGet, c.path+"nope", nil); code != 404 || decodeErr(t, body).Error != team.ErrNotFound {
			t.Fatalf("%s unknown id: %d %s", c.path, code, body)
		}
		if code, _ := f.do(http.MethodGet, c.path+c.id+"?wait=abc", nil); code != 400 {
			t.Fatalf("%s bad wait: %d", c.path, code)
		}
		done := make(chan []byte, 1)
		go func() {
			_, body := f.do(http.MethodGet, c.path+c.id+"?wait=20", nil)
			done <- body
		}()
		waitForWaiter(t, f, c.id)
		f.do(http.MethodDelete, "/api/team/approvals/"+c.id, nil)
		select {
		case body := <-done:
			if !bytes.Contains(body, []byte(`"cancelled"`)) {
				t.Fatalf("%s woke with %s", c.path, body)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("%s long-poll did not wake on the close", c.path)
		}
	}
}

// Fix note (P8a-1b R1): a remote deny of a hook_permission is a remote
// answer too — the row is `denied` with hook.behavior "deny" (plan v2
// Coordinator decisions, P8a-1b item 3), and wait answers answered_remote
// carrying that hook, the same shape as an approve, so the mod returns the
// deny instead of reading it as "closed another way". Mutation gate: drop
// the denied case from askWaitOf ⇒ closed{denied} ⇒ red.
func TestAskWait_RemoteDenyIsAnsweredRemoteWithHook(t *testing.T) {
	f := newFixture(t)
	id := f.askBeginPermission("toolu_d1")
	if code, body := f.do(http.MethodPost, "/api/team/approvals/"+id+"/decide",
		team.DecideRequest{Decision: "deny", Hook: &team.HookDecision{Message: "不要刪"}, Client: appClient()}); code != http.StatusOK {
		t.Fatalf("deny = %d %s", code, body)
	}
	code, body := f.do(http.MethodGet, "/api/ask/wait/"+id+"?wait=25", nil)
	w := decodeWait(t, body)
	if code != 200 || w.State != team.AskAnsweredRemote || w.Reason != "" || w.Hook == nil || w.Hook.Behavior != "deny" || w.Hook.Message != "不要刪" {
		t.Fatalf("wait after a remote deny = %d %+v hook=%+v", code, w, w.Hook)
	}
}
