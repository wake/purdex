package teammod

import (
	"database/sql"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wake/purdex/internal/team"
)

// Mutation gate: leave PID/PaneID out of the begin's op → red.
func TestRelayBegin_FillsPIDAndPane(t *testing.T) {
	f := newFixture(t)
	out := f.begin("sid-1") // fixture origin: pid 10, tmux "mt0:@1.%1"
	if out.Op.PID != 10 || out.Op.PaneID != "%1" {
		t.Fatalf("begin op = pid %d pane %q, want 10 / %%1", out.Op.PID, out.Op.PaneID)
	}
	if got := f.op(out.Op.ID); got.PID != 10 || got.PaneID != "%1" {
		t.Fatalf("stored op = pid %d pane %q", got.PID, got.PaneID)
	}
}

func TestPaneOf(t *testing.T) {
	for in, want := range map[string]string{"mt0:@1.%1": "%1", "s:@12.%345": "%345", "": "", "s:@1": "", "s:@1.2": ""} {
		if got := paneOf(in); got != want {
			t.Errorf("paneOf(%q) = %q, want %q", in, got, want)
		}
	}
}

// A member op has no approval row: its binding is its own pid. Mutation gates: wave a pid-0 member op through → the
// pid-0 case red; bind to the approval row instead of the op's pid → the other-pid case red.
func TestCleared_MemberOpBindsToItsPID(t *testing.T) {
	f := newFixture(t)
	f.origins.cleared = map[string]int{"sid-same": 10, "sid-other": 20}
	mk := func(id, sid string, pid int) {
		t.Helper()
		op := team.RelayOp{ID: id, Kind: team.RelayKindMember, HostID: "h:1", SessionID: sid, Ref: "_" + id, TeamID: "t",
			State: team.RelayWritten, HandoffPath: "/d/" + id + ".md", PID: pid, CreatedAt: 1, UpdatedAt: 1}
		if err := f.m.store.CreateRelayOp(op); err != nil {
			t.Fatal(err)
		}
	}
	mk("op-a", "sid-ma", 10)
	if code, _, ae := f.report("op-a", team.RelayReportRequest{State: team.RelayCleared, NewSessionID: "sid-other"}); code != http.StatusBadRequest || !strings.Contains(ae.Detail, "another process") {
		t.Fatalf("other pid: %d %+v", code, ae)
	}
	if code, _, ae := f.report("op-a", team.RelayReportRequest{State: team.RelayCleared, NewSessionID: "sid-same"}); code != http.StatusOK {
		t.Fatalf("same pid: %d %+v", code, ae)
	}
	// pid 0 with a request id that names a live approval row (origin pid 10): a member op must NOT fall back to it.
	row := f.begin("sid-1")
	mk("op-z", "sid-mz", 0)
	if _, err := f.m.store.db.Exec(`UPDATE relay_ops SET request_id = ? WHERE id = 'op-z'`, row.RequestID); err != nil {
		t.Fatal(err)
	}
	code, _, ae := f.report("op-z", team.RelayReportRequest{State: team.RelayCleared, NewSessionID: "sid-same"})
	if code != http.StatusBadRequest || !strings.Contains(ae.Detail, "no process binding") {
		t.Fatalf("pid 0 must fail closed: %d %+v", code, ae)
	}
	if got := f.op("op-z"); got.State != team.RelayWritten {
		t.Fatalf("a refused cleared moved the op: %+v", got)
	}
}

// A self op written before the column existed (pid 0) still binds through its approval row's origin pid.
func TestCleared_SelfOpFromBeforeP62aFallsBackToTheApprovalRow(t *testing.T) {
	f := newFixture(t)
	out := f.begin("sid-1") // origin pid 10
	f.decide(out.RequestID, "approve")
	if _, err := f.m.store.db.Exec(`UPDATE relay_ops SET pid = 0, pane_id = '' WHERE id = ?`, out.Op.ID); err != nil {
		t.Fatal(err)
	}
	f.origins.cleared = map[string]int{"sid-bad": 20}
	if code, _, ae := f.report(out.Op.ID, team.RelayReportRequest{State: team.RelayCleared, NewSessionID: "sid-bad"}); code != http.StatusBadRequest || !strings.Contains(ae.Detail, "another process") {
		t.Fatalf("fallback must still bind to pid 10: %d %+v", code, ae)
	}
}

// The host's relay_ops (read with sqlite3 -readonly on 2026-10-09) has neither column: opening it adds both, and an
// op written before them reads pid 0. Mutation gate: drop migrateRelayOpBinding → OpenStore fails on the SELECT (red).
func TestOpenStore_AddsTheBindingColumnsToADeployedRelayOps(t *testing.T) {
	path := filepath.Join(t.TempDir(), "team.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE relay_ops (
		id TEXT PRIMARY KEY, kind TEXT NOT NULL, host_id TEXT NOT NULL, session_id TEXT NOT NULL,
		new_session_id TEXT NOT NULL DEFAULT '', ref TEXT NOT NULL, new_ref TEXT NOT NULL DEFAULT '',
		team_id TEXT NOT NULL DEFAULT '', request_id TEXT NOT NULL DEFAULT '', state TEXT NOT NULL,
		reason TEXT NOT NULL DEFAULT '', handoff_path TEXT NOT NULL, pruned INTEGER NOT NULL DEFAULT 0,
		used_percentage REAL, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL);
		INSERT INTO relay_ops (id, kind, host_id, session_id, ref, state, handoff_path, created_at, updated_at)
		VALUES ('old', 'self', 'h', 's', '_a', 'done', '/p', 1, 1)`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	op, ok, err := s.GetRelayOp("old")
	if err != nil || !ok || op.PID != 0 || op.PaneID != "" || op.ProcStart != "" || op.SeenAt != 0 {
		t.Fatalf("old op after migration: %+v ok=%v err=%v", op, ok, err)
	}
}
