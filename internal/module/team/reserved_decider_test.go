package teammod

import (
	"errors"
	"net/http"
	"testing"

	"github.com/wake/purdex/internal/team"
)

// RQ-0 (#2062 plan review #2): `unattended` is the daemon's own decider. A decide request cannot be it, and the
// store refuses a non-Auto close that names it; an Auto close is always recorded as it.

// Mutation gate: drop the check in handleDecide → red.
func TestDecide_RefusesTheReservedUnattendedKind(t *testing.T) {
	for _, kind := range []string{"unattended", " Unattended ", "UNATTENDED"} {
		for _, decision := range []string{"approve", "deny"} {
			f := newFixture(t)
			f.create(uid(1))
			code, body := f.do(http.MethodPost, "/api/team/approvals/"+uid(1)+"/decide",
				team.DecideRequest{Decision: decision, Client: team.Client{Kind: kind, Label: "無人值守模式"}})
			if e := decodeErr(t, body); code != http.StatusBadRequest || e.Error != team.ErrBadRequest {
				t.Fatalf("%q %s = %d %s, want 400 bad_request", kind, decision, code, body)
			}
			if a, _, _ := f.m.store.Get(uid(1)); a.State != team.StateOpen {
				t.Fatalf("%q %s: the row is %s, want still open", kind, decision, a.State)
			}
			if list, _, err := f.m.store.ListAutoApproved(0, 0, 10); err != nil || len(list) != 0 {
				t.Fatalf("%q %s: the daemon's own list = %+v (%v), want empty", kind, decision, list, err)
			}
		}
	}
	// a person's click still works
	f := newFixture(t)
	f.approveLead(uid(1))
}

// Mutation gate: drop the refusal in decidedByOf → red.
func TestStoreClose_NonAutoCannotNameTheUnattendedDecider(t *testing.T) {
	s := openTestStore(t)
	if _, _, _, err := s.Create(openApproval(uid(1), "sid-1", 1000), "h"); err != nil {
		t.Fatal(err)
	}
	by := team.UnattendedClient()
	if _, _, err := s.CloseIfOpen(uid(1), Close{State: team.StateDenied, DecidedAt: 2000, DecidedBy: &by}); !errors.Is(err, ErrReservedDecider) {
		t.Fatalf("a non-Auto close naming unattended = %v, want ErrReservedDecider", err)
	}
	if a, _, _ := s.Get(uid(1)); a.State != team.StateOpen {
		t.Fatalf("row = %s, want open", a.State)
	}
}

// Mutation gate: record DecidedBy as given for an Auto close → red.
func TestStoreClose_AutoIsRecordedAsUnattendedWhateverDecidedByHolds(t *testing.T) {
	s := openTestStore(t)
	if _, _, _, err := s.Create(openApproval(uid(1), "sid-1", 1000), "h"); err != nil {
		t.Fatal(err)
	}
	app := team.Client{Kind: "app", Label: "Purdex.app"}
	after, won, err := s.CloseIfOpen(uid(1), Close{State: team.StateDenied, DecidedAt: 2000, DecidedBy: &app, Auto: true})
	if err != nil || !won {
		t.Fatalf("close = %v %v", won, err)
	}
	if after.DecidedBy == nil || after.DecidedBy.Kind != team.ClientKindUnattended || after.DecidedBy.Label != team.UnattendedLabel {
		t.Fatalf("decided_by = %+v, want the unattended decider", after.DecidedBy)
	}
}

// The daemon's own approvals still go through: every path sets Auto via daemonClose.
func TestDaemonClose_IsAuto(t *testing.T) {
	if c := daemonClose(1, nil); !c.Auto || c.DecidedBy == nil || c.DecidedBy.Kind != team.ClientKindUnattended {
		t.Fatalf("daemonClose = %+v", c)
	}
}

// A row written before RQ-0, when a decide request could name `unattended` (it always carried its RemoteAddr), is
// not the daemon's approval. Mutation gate: drop the addr condition from autoApprovedQuery → red.
func TestListAutoApproved_SkipsAPreRQ0RowThatNamedTheKindWithAnAddr(t *testing.T) {
	s := openTestStore(t)
	closedAt(t, s, "auto", 2000, team.UnattendedClient(), team.StateApproved)
	if _, _, _, err := s.Create(openApproval("forged", "sid-forged", 1900), "h-forged"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE approval_requests SET state = 'approved', decided_at = 2100,
		decided_by_json = '{"kind":"unattended","label":"無人值守模式","addr":"100.64.0.4:51234"}' WHERE id = 'forged'`); err != nil {
		t.Fatal(err)
	}
	rows, _, err := s.ListAutoApproved(1, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(rows); len(got) != 1 || got[0] != "auto" {
		t.Fatalf("auto-approved = %v, want only the daemon's own", got)
	}
}
