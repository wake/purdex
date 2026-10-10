package teammod

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/wake/purdex/internal/team"
)

// #1848: the terminal's report by (session, tool use) for a mod that never got begin's answer. The row is found by the tool use,
// among the rows created at or after `since` (taken before begin was called).

func (f *fixture) reportByToolUse(sid, toolUse string, since int64, state team.State, hook *team.HookDecision) (int, []byte) {
	f.t.Helper()
	return f.do(http.MethodPost, "/api/ask/report", team.AskReportRequest{State: state, Hook: hook, SessionID: sid, ToolUseID: toolUse, Since: since})
}

var localAnswer = &team.HookDecision{Answers: map[string]string{"紅還是藍？": "紅"}}

func TestAskReportByToolUse_ClosesTheOpenRowAsTheIdRouteDoes(t *testing.T) {
	f := newFixture(t)
	since := f.clock.Load()
	id := f.askBegin("toolu_1")
	code, body := f.reportByToolUse("sid-1", "toolu_1", since, team.StateAnsweredLocal, localAnswer)
	if code != http.StatusOK {
		t.Fatalf("report = %d %s", code, body)
	}
	if a, _, _ := f.m.store.Get(id); a.State != team.StateAnsweredLocal || a.DecidedBy == nil || a.DecidedBy.Kind != team.ClientKindTerminal {
		t.Fatalf("row = %+v", a)
	}
}

func TestAskReportByToolUse_DismissedClosesTheOpenRow(t *testing.T) {
	f := newFixture(t)
	since := f.clock.Load()
	id := f.askBegin("toolu_1")
	if code, body := f.reportByToolUse("sid-1", "toolu_1", since, team.StateDismissed, nil); code != http.StatusOK {
		t.Fatalf("report = %d %s", code, body)
	}
	if a, _, _ := f.m.store.Get(id); a.State != team.StateDismissed {
		t.Fatalf("row = %s", a.State)
	}
}

// The gap itself: a remote client answered first; the terminal's report, by the tool use, makes it terminal_override and tells
// every card. Mutation gate: apply the open-only close (no override) → the row stays approved (red).
func TestAskReportByToolUse_ARemoteAnswerThatWonIsOverriddenByTheTerminal(t *testing.T) {
	f := newFixture(t)
	since := f.clock.Load()
	id := f.askBegin("toolu_2")
	if code, body := f.do(http.MethodPost, "/api/team/approvals/"+id+"/decide",
		team.DecideRequest{Decision: "approve", Hook: &team.HookDecision{Answers: map[string]string{"紅還是藍？": "藍"}}, Client: appClient()}); code != http.StatusOK {
		t.Fatalf("decide = %d %s", code, body)
	}
	f.events()
	code, body := f.reportByToolUse("sid-1", "toolu_2", since, team.StateAnsweredLocal, localAnswer)
	if code != http.StatusOK {
		t.Fatalf("report = %d %s", code, body)
	}
	a, _, _ := f.m.store.Get(id)
	if a.State != team.StateTerminalOverride || a.Hook == nil || a.Hook.Answers["紅還是藍？"] != "紅" {
		t.Fatalf("row = %+v, want terminal_override with the terminal's answer", a)
	}
	if evs := f.events(); len(evs) != 1 || evs[0].Op != "closed" || evs[0].Approval.ID != id {
		t.Fatalf("events = %+v, want one closed", evs)
	}
}

// No row yet: 404 not_found and nothing changes (the CLI retries until begin's row lands).
func TestAskReportByToolUse_NoRowYetIsNotFound(t *testing.T) {
	f := newFixture(t)
	code, body := f.reportByToolUse("sid-1", "toolu_9", f.clock.Load(), team.StateAnsweredLocal, localAnswer)
	if code != http.StatusNotFound || decodeErr(t, body).Error != team.ErrNotFound {
		t.Fatalf("report = %d %s, want 404 not_found", code, body)
	}
}

// One session, two tool uses: the report names its own. Mutation gate: ignore tool_use_id → the other row is closed (red).
func TestAskReportByToolUse_OtherToolUsesAndSessionsAreNotTouched(t *testing.T) {
	f := newFixture(t)
	since := f.clock.Load()
	a := f.askBegin("toolu_a")
	b := f.askBegin("toolu_b")
	other := f.askBeginPermission("toolu_a") // sid-2, the same tool use id
	if code, body := f.reportByToolUse("sid-1", "toolu_b", since, team.StateDismissed, nil); code != http.StatusOK {
		t.Fatalf("report = %d %s", code, body)
	}
	for id, want := range map[string]team.State{a: team.StateOpen, b: team.StateDismissed, other: team.StateOpen} {
		if got, _, _ := f.m.store.Get(id); got.State != want {
			t.Errorf("row %s = %s, want %s", id, got.State, want)
		}
	}
}

// `since` keeps an earlier row of the same tool use out: a row that closed before the begin was called is not the begin's row, so
// the report is 404 until the begin's row lands, and then it lands on that one. Mutation gate: drop the since filter → the old
// row takes the report (200 at once, and the new row stays open) (red).
func TestAskReportByToolUse_AnEarlierRowOfTheSameToolUseIsNotTheOne(t *testing.T) {
	f := newFixture(t)
	old := f.askBegin("toolu_3")
	if code, _ := f.reportByToolUse("sid-1", "toolu_3", 0, team.StateDismissed, nil); code != http.StatusOK {
		t.Fatal("setup: the old row did not close")
	}
	f.clock.Add(5_000)
	since := f.clock.Load() // taken before the new begin is called
	f.clock.Add(1_000)
	if code, _ := f.reportByToolUse("sid-1", "toolu_3", since, team.StateAnsweredLocal, localAnswer); code != http.StatusNotFound {
		t.Fatalf("report before the begin's row exists = %d, want 404 (the old row is before since)", code)
	}
	if a, _, _ := f.m.store.Get(old); a.State != team.StateDismissed {
		t.Fatalf("old row = %s, it must not change", a.State)
	}
	f.clock.Add(1_000)
	fresh := f.askBegin("toolu_3")
	if code, body := f.reportByToolUse("sid-1", "toolu_3", since, team.StateAnsweredLocal, localAnswer); code != http.StatusOK {
		t.Fatalf("report after the row landed = %d %s", code, body)
	}
	if a, _, _ := f.m.store.Get(fresh); a.State != team.StateAnsweredLocal {
		t.Fatalf("new row = %s, want answered_local", a.State)
	}
}

// Of several rows at or after since, the newest. Mutation gate: the oldest → red.
func TestAskReportByToolUse_TheNewestRowAtOrAfterSinceIsTheOne(t *testing.T) {
	f := newFixture(t)
	since := f.clock.Load()
	first := f.askBegin("toolu_4")
	if code, _ := f.reportByToolUse("sid-1", "toolu_4", since, team.StateDismissed, nil); code != http.StatusOK {
		t.Fatal("setup")
	}
	f.clock.Add(1_000)
	second := f.askBegin("toolu_4")
	if code, body := f.reportByToolUse("sid-1", "toolu_4", since, team.StateAnsweredLocal, localAnswer); code != http.StatusOK {
		t.Fatalf("report = %d %s", code, body)
	}
	if a, _, _ := f.m.store.Get(second); a.State != team.StateAnsweredLocal {
		t.Fatalf("newest row = %s, want answered_local", a.State)
	}
	if a, _, _ := f.m.store.Get(first); a.State != team.StateDismissed {
		t.Fatalf("older row = %s, it must not change", a.State)
	}
}

// Validation first, nothing touched: no session / tool use, a bad state, a terminal answer without answers.
func TestAskReportByToolUse_Validation(t *testing.T) {
	f := newFixture(t)
	since := f.clock.Load()
	id := f.askBegin("toolu_5")
	for name, req := range map[string]team.AskReportRequest{
		"no session":      {State: team.StateDismissed, ToolUseID: "toolu_5", Since: since},
		"no tool use":     {State: team.StateDismissed, SessionID: "sid-1", Since: since},
		"bad state":       {State: team.StateApproved, SessionID: "sid-1", ToolUseID: "toolu_5", Since: since},
		"hookless answer": {State: team.StateAnsweredLocal, SessionID: "sid-1", ToolUseID: "toolu_5", Since: since},
	} {
		code, body := f.do(http.MethodPost, "/api/ask/report", req)
		if code != http.StatusBadRequest || decodeErr(t, body).Error != team.ErrBadRequest {
			t.Errorf("%s: %d %s, want 400 bad_request", name, code, body)
		}
	}
	if a, _, _ := f.m.store.Get(id); a.State != team.StateOpen {
		t.Fatalf("row = %s: a refused report changed it", a.State)
	}
}

// The same answer twice is the same row as it is now (the CLI retries; the detached report may also land twice).
func TestAskReportByToolUse_IsIdempotent(t *testing.T) {
	f := newFixture(t)
	since := f.clock.Load()
	f.askBegin("toolu_6")
	for range 2 {
		code, body := f.reportByToolUse("sid-1", "toolu_6", since, team.StateAnsweredLocal, localAnswer)
		var a team.Approval
		if code != http.StatusOK || json.Unmarshal(body, &a) != nil || a.State != team.StateAnsweredLocal {
			t.Fatalf("report = %d %s", code, body)
		}
	}
}
