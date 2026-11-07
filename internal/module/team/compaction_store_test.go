package teammod

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/wake/purdex/internal/team"
)

func compactionRows(t *testing.T, s *Store, sid string) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM session_compaction WHERE session_id = ?`, sid).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestCompactionOverride_RoundTripWithNulls(t *testing.T) {
	s := openTestStore(t)
	if err := s.SetCompactionOverride("s1", team.CompactionOverride{Threshold: ptr(55)}, 1); err != nil {
		t.Fatal(err)
	}
	got, err := s.CompactionOverride("s1")
	if err != nil || got.Auto != nil || got.Idle != nil || got.Threshold == nil || *got.Threshold != 55 {
		t.Fatalf("got %+v err=%v", got, err)
	}
	if err := s.SetCompactionOverride("s1", team.CompactionOverride{Auto: ptr(false), Idle: ptr(true)}, 2); err != nil {
		t.Fatal(err)
	}
	got, _ = s.CompactionOverride("s1")
	if got.Auto == nil || *got.Auto || got.Idle == nil || !*got.Idle || got.Threshold != nil {
		t.Fatalf("got %+v", got)
	}
}

func TestCompactionOverride_AllNilDeletesTheRow(t *testing.T) {
	s := openTestStore(t)
	_ = s.SetCompactionOverride("s1", team.CompactionOverride{Auto: ptr(true)}, 1)
	if err := s.SetCompactionOverride("s1", team.CompactionOverride{}, 2); err != nil {
		t.Fatal(err)
	}
	if n := compactionRows(t, s, "s1"); n != 0 {
		t.Fatalf("rows = %d", n)
	}
}

func TestCompactionOverride_UnknownSessionIsEmpty(t *testing.T) {
	s := openTestStore(t)
	got, err := s.CompactionOverride("nope")
	if err != nil || got != (team.CompactionOverride{}) {
		t.Fatalf("got %+v err=%v", got, err)
	}
}

func TestReportRelay_ClearedCopiesTheOverride(t *testing.T) {
	for _, kind := range []team.RelayKind{team.RelayKindSelf, team.RelayKindMember} {
		s := openTestStore(t)
		_ = s.SetCompactionOverride("old", team.CompactionOverride{Auto: ptr(false), Threshold: ptr(40)}, 1)
		op := selfOp("op", "old", "_aaaaaa", 1000)
		op.Kind = kind
		op.State = team.RelayClaimed
		if err := s.CreateRelayOp(op); err != nil {
			t.Fatal(err)
		}
		mustReport(t, s, "op", RelayReport{State: team.RelayCleared, NewSessionID: "new", NewRef: "_bbbbbb", At: 2000})
		got, _ := s.CompactionOverride("new")
		if got.Auto == nil || *got.Auto || got.Threshold == nil || *got.Threshold != 40 || got.Idle != nil {
			t.Fatalf("%s: got %+v", kind, got)
		}
		if old, _ := s.CompactionOverride("old"); old.Threshold == nil {
			t.Fatalf("%s: predecessor row must stay", kind)
		}
	}
}

func TestReportRelay_NotClearedCopiesNothing(t *testing.T) {
	s := openTestStore(t)
	_ = s.SetCompactionOverride("old", team.CompactionOverride{Auto: ptr(true)}, 1)
	op := selfOp("op", "old", "_aaaaaa", 1000)
	op.State = team.RelayClaimed
	if err := s.CreateRelayOp(op); err != nil {
		t.Fatal(err)
	}
	mustReport(t, s, "op", RelayReport{State: team.RelayWriting, At: 1500})
	if n := compactionRows(t, s, "new"); n != 0 {
		t.Fatal("copied before cleared")
	}
}

func TestReportRelay_NoOverrideLeavesTheNewSessionEmpty(t *testing.T) {
	s := openTestStore(t)
	op := selfOp("op", "old", "_aaaaaa", 1000)
	op.State = team.RelayClaimed
	if err := s.CreateRelayOp(op); err != nil {
		t.Fatal(err)
	}
	mustReport(t, s, "op", RelayReport{State: team.RelayCleared, NewSessionID: "new", NewRef: "_bbbbbb", At: 2000})
	if n := compactionRows(t, s, "new"); n != 0 {
		t.Fatal("row invented")
	}
}

func TestOpenStore_AddsSessionCompactionToTheLiveSchema(t *testing.T) {
	schema, err := os.ReadFile("testdata/team-alpha615.schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "team.db")
	old, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.Exec(string(schema)); err != nil {
		t.Fatal(err)
	}
	old.Close()
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.CompactionOverride("x"); err != nil {
		t.Fatalf("no session_compaction table: %v", err)
	}
}
