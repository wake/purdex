package teammod

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/wake/purdex/internal/team"
)

func logSummary(log []team.UnattendedLogEntry) string {
	var parts []string
	for _, e := range log {
		switch e.Type {
		case team.LogTypeToggle:
			parts = append(parts, fmt.Sprintf("toggle:%d:%v", e.At, *e.On))
		default:
			parts = append(parts, fmt.Sprintf("decision:%d:%s:%s", e.At, e.Kind, e.State))
		}
	}
	return strings.Join(parts, " ")
}

// The log holds the switch's own on/off records and the decisions of the team approvals, whoever decided, newest first; a
// decision is listed before a toggle of the same millisecond. GET and the PUT's view both carry it. Mutation gate: leave the
// toggles out → red; leave the decisions out → red; sort oldest first → red.
func TestUnattendedLog_MergesTogglesAndDecisionsNewestFirst(t *testing.T) {
	f := newFixture(t)
	f.realUnattended()
	f.clock.Store(1000)
	f.switchTo(true)
	putDecisionRow(t, f.m.store, "d-approved", team.KindLead, team.StateApproved, 1500, 9999, "")
	putDecisionRow(t, f.m.store, "d-timeout", team.KindSelfRelay, team.StateTimeout, 9000, 1700, "")
	putDecisionRow(t, f.m.store, "d-hook", team.KindHookAsk, team.StateApproved, 1800, 9999, "")
	f.clock.Store(2000)
	put := f.switchTo(false)
	putDecisionRow(t, f.m.store, "d-same-ms", team.KindAdopt, team.StateDenied, 2000, 9999, "")

	want := "decision:2000:adopt:denied toggle:2000:false decision:1700:self_relay:timeout decision:1500:lead:approved toggle:1000:true"
	code, v, raw := f.getUnattended("")
	if code != http.StatusOK || logSummary(v.Log) != want {
		t.Fatalf("GET log = %q (code %d, %s), want %q", logSummary(v.Log), code, raw, want)
	}
	if by := v.Log[1].By; by == nil || by.Kind != "app" || by.Label != appDesk.Label || by.Addr != "" {
		t.Fatalf("toggle by = %+v, want the App's kind and label without an address", by)
	}
	if want := "toggle:2000:false decision:1700:self_relay:timeout decision:1500:lead:approved toggle:1000:true"; logSummary(put.Log) != want {
		t.Fatalf("PUT log = %q, want %q", logSummary(put.Log), want)
	}
}

// A lead request the daemon refused while the switch was on is listed as a denial by the unattended mode.
func TestUnattendedLog_RefusedLeadIsADenialByTheDaemon(t *testing.T) {
	f := newFixture(t)
	f.realUnattended()
	f.clock.Store(1000)
	putDecisionRow(t, f.m.store, "refused", team.KindLead, team.StateCancelled, 900, 9999, team.ErrUnattendedRefused)
	putDecisionRow(t, f.m.store, "withdrawn", team.KindLead, team.StateCancelled, 950, 9999, "")
	_, v, _ := f.getUnattended("")
	if len(v.Log) != 1 || v.Log[0].State != team.StateDenied || v.Log[0].DecidedBy == nil || v.Log[0].DecidedBy.Kind != team.ClientKindUnattended {
		t.Fatalf("log = %+v, want the refusal alone, denied by the unattended mode", v.Log)
	}
}

// log is capped at team.DecisionLogMax, keeping the newest. Mutation gate: no cap → red.
func TestUnattendedLog_CappedAtThirtyNewest(t *testing.T) {
	f := newFixture(t)
	f.realUnattended()
	for i := 0; i < 40; i++ {
		putDecisionRow(t, f.m.store, fmt.Sprintf("d-%02d", i), team.KindLead, team.StateApproved, int64(1000+i), 9999, "")
	}
	_, v, _ := f.getUnattended("")
	if len(v.Log) != team.DecisionLogMax || v.Log[0].At != 1039 || v.Log[len(v.Log)-1].At != 1010 {
		t.Fatalf("log has %d entries, first %d, last %d; want 30, 1039 … 1010", len(v.Log), v.Log[0].At, v.Log[len(v.Log)-1].At)
	}
}

// Decisions and toggles together can pass the cap although each is cut at 30 on its own: the merged log is cut again, newest
// first. Mutation gate: no cut after the merge → red.
func TestUnattendedLog_MergedLogIsCutAtThirty(t *testing.T) {
	f := newFixture(t)
	f.realUnattended()
	for i := 0; i < 25; i++ {
		putDecisionRow(t, f.m.store, fmt.Sprintf("d-%02d", i), team.KindLead, team.StateApproved, int64(1000+i), 9999, "")
	}
	for i := 0; i < 25; i++ {
		f.clock.Store(int64(3000 + i))
		f.switchTo(i%2 == 0)
	}
	_, v, _ := f.getUnattended("")
	if len(v.Log) != team.DecisionLogMax {
		t.Fatalf("log has %d entries, want %d", len(v.Log), team.DecisionLogMax)
	}
	for i, e := range v.Log[:25] {
		if e.Type != team.LogTypeToggle || e.At != int64(3024-i) {
			t.Fatalf("entry %d = %+v, want the toggle at %d", i, e, 3024-i)
		}
	}
	if last := v.Log[len(v.Log)-1]; last.Type != team.LogTypeDecision || last.At != 1020 {
		t.Fatalf("last = %+v, want the decision at 1020", last)
	}
}

// The fixture's fake switch keeps no records: the log is the decisions alone, and an empty one is [] not null.
func TestUnattendedLog_FakeSwitchHasNoToggles(t *testing.T) {
	f := newFixture(t)
	code, raw := f.do(http.MethodGet, UnattendedRoute, nil)
	if code != http.StatusOK || !strings.Contains(string(raw), `"log":[]`) {
		t.Fatalf("GET = %d %s, want an empty log array", code, raw)
	}
	putDecisionRow(t, f.m.store, "d1", team.KindLead, team.StateApproved, 1500, 9999, "")
	_, v, _ := f.getUnattended("")
	if logSummary(v.Log) != "decision:1500:lead:approved" {
		t.Fatalf("log = %q", logSummary(v.Log))
	}
}

// A decision list that cannot be read: GET answers 500; the PUT, whose write took effect, answers 200 with approved [], log []
// and list_failed. Mutation gate: ignore the error → red.
func TestUnattendedLog_DecisionReadFailure(t *testing.T) {
	f := newFixture(t)
	f.realUnattended()
	f.m.store.beforeListRecentDecisions = func() error { return errors.New("disk I/O error") }
	if code, _, raw := f.getUnattended(""); code != http.StatusInternalServerError {
		t.Fatalf("GET = %d %s, want 500", code, raw)
	}
	on := true
	code, v, raw := f.putUnattended(team.UnattendedPutRequest{On: &on, Client: appDesk})
	if code != http.StatusOK || !v.On || !v.ListFailed || len(v.Approved) != 0 || len(v.Log) != 0 {
		t.Fatalf("PUT = %d %s, want 200, on, list_failed, nothing listed", code, raw)
	}
	if !strings.Contains(string(raw), `"log":[]`) || !strings.Contains(string(raw), `"approved":[]`) {
		t.Fatalf("PUT body = %s, want approved [] and log []", raw)
	}
}
