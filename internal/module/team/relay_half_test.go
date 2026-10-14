package teammod

import (
	"testing"

	"github.com/wake/purdex/internal/team"
)

// RT-3a1 T28: the mode column and relay_fresh_half on a database from before. Mutation gate: skip the ensureColumn → the old table has
// no mode column and a read of an op fails (red).
func TestRelayOps_ModeColumnOnAnOldTable(t *testing.T) {
	s := openTestStore(t)
	op := team.RelayOp{ID: "op-1", Kind: team.RelayKindSelf, HostID: "h", SessionID: "s", Ref: "_abc123", State: team.RelayRequested, HandoffPath: "/p", CreatedAt: 1, UpdatedAt: 1}
	if err := s.CreateRelayOp(op); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`ALTER TABLE relay_ops DROP COLUMN mode`); err != nil {
		t.Fatalf("drop: %v", err)
	}
	if err := migrateRelayOpBinding(s.db); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.GetRelayOp("op-1")
	if err != nil || !ok || got.Mode != "" || got.ID != "op-1" {
		t.Fatalf("old row = %+v ok=%v err=%v, want it kept with mode ''", got, ok, err)
	}
	half := op
	half.ID, half.SessionID, half.Mode = "op-2", "s2", team.RelayModeHalf
	if err := s.CreateRelayOp(half); err != nil {
		t.Fatal(err)
	}
	if got, _, _ := s.GetRelayOp("op-2"); got.Mode != team.RelayModeHalf {
		t.Fatalf("mode = %q, want half", got.Mode)
	}
}

func TestRelayFreshHalf_TableExists(t *testing.T) {
	s := openTestStore(t)
	if _, err := s.db.Exec(`INSERT INTO relay_fresh_half (session_id, op_id, path, written_at) VALUES ('s', 'o', '/p', 1)`); err != nil {
		t.Fatalf("relay_fresh_half: %v", err)
	}
}
