package teammod

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/wake/purdex/internal/team"
)

// putDecisionRow inserts one approval_requests row as raw SQL: the state a daemon left it in, whatever wrote it.
func putDecisionRow(t *testing.T, s *Store, id string, kind team.Kind, state team.State, decidedAt, deadlineAt int64, reason string) {
	t.Helper()
	if _, err := s.db.Exec(`INSERT INTO approval_requests
		(id, kind, host_id, origin_session_id, origin_json, payload_json, request_hash, state, created_at, deadline_at, lease_until, decided_by_json, decided_at, close_reason)
		VALUES (?, ?, 'h1', 's1', '{"session_id":"s1","ref":"_abc123","pid":1,"cwd":"/w"}', '{}', 'hash', ?, 1, ?, 0, NULL, ?, ?)`,
		id, string(kind), string(state), deadlineAt, decidedAt, reason); err != nil {
		t.Fatalf("insert %s: %v", id, err)
	}
}

func decisionIDs(rows []team.Approval) []string {
	ids := []string{}
	for _, a := range rows {
		ids = append(ids, a.ID)
	}
	return ids
}

func TestListRecentDecisions_SelectsDecisionsOfTeamKinds(t *testing.T) {
	s := openTestStore(t)
	putDecisionRow(t, s, "ok-lead", team.KindLead, team.StateApproved, 500, 9999, "")
	putDecisionRow(t, s, "denied-adopt", team.KindAdopt, team.StateDenied, 400, 9999, "")
	putDecisionRow(t, s, "refused-lead", team.KindLead, team.StateCancelled, 300, 9999, team.ErrUnattendedRefused)
	putDecisionRow(t, s, "refused-adopt", team.KindAdopt, team.StateCancelled, 200, 9999, team.ErrUnattendedRefused)
	putDecisionRow(t, s, "timeout-relay", team.KindMemberRelay, team.StateTimeout, 0, 100, "")
	// left out: a withdrawn request, an abandoned one, an open one, the chat prompts
	putDecisionRow(t, s, "cancelled-lead", team.KindLead, team.StateCancelled, 600, 9999, "")
	putDecisionRow(t, s, "abandoned-lead", team.KindLead, team.StateAbandoned, 600, 9999, "")
	putDecisionRow(t, s, "open-lead", team.KindLead, team.StateOpen, 0, 9999, "")
	putDecisionRow(t, s, "hook-ask", team.KindHookAsk, team.StateApproved, 700, 9999, "")
	putDecisionRow(t, s, "hook-perm", team.KindHookPermission, team.StateDenied, 700, 9999, "")

	rows, err := s.ListRecentDecisions(30)
	if err != nil {
		t.Fatal(err)
	}
	// newest effective time first: a timeout counts at its deadline (100), the rest at decided_at
	want := []string{"ok-lead", "denied-adopt", "refused-lead", "refused-adopt", "timeout-relay"}
	if got := decisionIDs(rows); !reflect.DeepEqual(got, want) {
		t.Fatalf("ids = %v, want %v", got, want)
	}
}

func TestListRecentDecisions_TimeoutSortsByDeadline(t *testing.T) {
	s := openTestStore(t)
	// decided_at 9000 would put the timeout first; its deadline 100 puts it after the approval decided at 500
	putDecisionRow(t, s, "timeout", team.KindSelfRelay, team.StateTimeout, 9000, 100, "")
	putDecisionRow(t, s, "approved", team.KindSelfRelay, team.StateApproved, 500, 9999, "")
	rows, err := s.ListRecentDecisions(30)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := decisionIDs(rows), []string{"approved", "timeout"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ids = %v, want %v", got, want)
	}
}

func TestListRecentDecisions_SameTimeLargerIDFirstAndLimit(t *testing.T) {
	s := openTestStore(t)
	putDecisionRow(t, s, "a", team.KindLead, team.StateApproved, 500, 9999, "")
	putDecisionRow(t, s, "b", team.KindLead, team.StateDenied, 500, 9999, "")
	putDecisionRow(t, s, "c", team.KindLead, team.StateCancelled, 500, 9999, team.ErrUnattendedRefused)
	putDecisionRow(t, s, "d", team.KindLead, team.StateApproved, 100, 9999, "")
	rows, err := s.ListRecentDecisions(3)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := decisionIDs(rows), []string{"c", "b", "a"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ids = %v, want %v", got, want)
	}
	if rows, err = s.ListRecentDecisions(0); err != nil || len(rows) != 0 {
		t.Fatalf("limit 0 = %v, %v, want empty", decisionIDs(rows), err)
	}
}

// Each of the four reads is cut at limit on its own, so a long run of one state cannot push out the newest of another.
func TestListRecentDecisions_ManyOfOneStateKeepsTheOthers(t *testing.T) {
	s := openTestStore(t)
	for i := 0; i < 40; i++ {
		putDecisionRow(t, s, fmt.Sprintf("ap-%02d", i), team.KindLead, team.StateApproved, int64(1000+i), 9999, "")
	}
	putDecisionRow(t, s, "denied-newest", team.KindLead, team.StateDenied, 5000, 9999, "")
	putDecisionRow(t, s, "timeout-old", team.KindLead, team.StateTimeout, 0, 10, "")
	rows, err := s.ListRecentDecisions(30)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 30 || rows[0].ID != "denied-newest" || rows[1].ID != "ap-39" {
		t.Fatalf("got %d rows, first %v", len(rows), decisionIDs(rows)[:2])
	}
}

func TestListRecentDecisions_EmptyIsNotNil(t *testing.T) {
	rows, err := openTestStore(t).ListRecentDecisions(30)
	if err != nil || rows == nil || len(rows) != 0 {
		t.Fatalf("rows = %#v, err = %v, want empty non-nil", rows, err)
	}
}

// Every read of ListRecentDecisions is a range scan of an index that holds its state and kind (or the partial index of the
// refusals), never a walk of the whole state, which the chat prompts fill. Mutation gate: read by state alone → red.
func TestListRecentDecisions_ReadsUseTheirOwnIndex(t *testing.T) {
	s := openTestStore(t)
	for _, c := range []struct {
		where, order, index string
		args                []any
	}{
		{decisionsByState, "decided_at", "approval_requests_state_kind_decided", []any{"approved", "lead"}},
		{decisionsByState, "deadline_at", "approval_requests_state_kind_deadline", []any{"timeout", "lead"}},
		{decisionsRefused, "decided_at", "approval_requests_refused", []any{"lead", "cancelled"}},
	} {
		q, err := s.db.Query(`EXPLAIN QUERY PLAN `+recentDecisionsQuery(c.where, c.order), append(c.args, 30)...)
		if err != nil {
			t.Fatal(err)
		}
		var plan []string
		for q.Next() {
			var id, parent, unused int
			var detail string
			if err := q.Scan(&id, &parent, &unused, &detail); err != nil {
				q.Close()
				t.Fatal(err)
			}
			plan = append(plan, detail)
		}
		q.Close()
		if !strings.Contains(strings.Join(plan, "\n"), "USING INDEX "+c.index) {
			t.Errorf("plan %q does not use %s", plan, c.index)
		}
	}
}
