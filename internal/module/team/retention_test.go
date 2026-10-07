package teammod

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/wake/purdex/internal/team"
)

const day = int64(24 * 60 * 60 * 1000)

func doneOp(id, sid string, createdAt int64) team.RelayOp {
	op := selfOp(id, sid, "_r"+id, createdAt)
	op.State, op.UpdatedAt = team.RelayDone, createdAt+60_000
	return op
}

// Spec §15 "Retention": 3 per chain, 14 days, and 3 days for failed ops.
// retentionVictims is pure; `now` is day 20.
func TestRetentionVictims_Rules(t *testing.T) {
	now := 20 * day
	chain := func(op team.RelayOp) string {
		if op.SessionID == "a1" || op.SessionID == "a2" || op.SessionID == "a3" || op.SessionID == "a4" {
			return "a"
		}
		return op.SessionID
	}
	ops := []team.RelayOp{
		// chain a: four done ops, newest first by created_at → the oldest (a1) goes.
		doneOp("a1", "a1", 16*day), doneOp("a2", "a2", 17*day), doneOp("a3", "a3", 18*day), doneOp("a4", "a4", 19*day),
		// another chain with one done op: kept.
		doneOp("b1", "b1", 18*day),
		// a done op from 15 days ago, alone in its chain: the 14 d rule takes it.
		doneOp("old", "old", 5*day),
		// failed 2 days ago: kept; failed 3 days ago: goes; cancelled 4 days ago: goes.
		{ID: "f-young", State: team.RelayFailed, SessionID: "f1", CreatedAt: 17*day + 1, UpdatedAt: 18 * day, HandoffPath: "x"},
		{ID: "f-old", State: team.RelayFailed, SessionID: "f2", CreatedAt: 16 * day, UpdatedAt: 17 * day, HandoffPath: "x"},
		{ID: "c-old", State: team.RelayCancelled, SessionID: "c1", CreatedAt: 15 * day, UpdatedAt: 16 * day, HandoffPath: "x"},
		// Codex round: a file older than 14 d (created day 2) whose op turned
		// failed YESTERDAY is kept — the 3 d rule governs failed/cancelled ops
		// alone; the 14 d rule does not reach them. Mutation gate: make the
		// two age checks independent (`if` + `if` instead of the switch) →
		// "f-ancient must NOT be a victim" → red.
		{ID: "f-ancient", State: team.RelayFailed, SessionID: "f3", CreatedAt: 2 * day, UpdatedAt: 19 * day, HandoffPath: "x"},
		// an active op from yesterday: never a victim; one stuck for 15 days: the 14 d rule.
		{ID: "live", State: team.RelayWriting, SessionID: "l1", CreatedAt: 19 * day, UpdatedAt: 19 * day, HandoffPath: "x"},
		{ID: "stuck", State: team.RelayClaimed, SessionID: "l2", CreatedAt: 5 * day, UpdatedAt: 5 * day, HandoffPath: "x"},
		// already pruned: ignored even though old.
		{ID: "gone", State: team.RelayDone, SessionID: "g1", CreatedAt: 1 * day, UpdatedAt: 1 * day, Pruned: true, HandoffPath: "x"},
	}
	got := retentionVictims(ops, chain, now)
	ids := map[string]bool{}
	for _, op := range got {
		ids[op.ID] = true
	}
	want := map[string]bool{"a1": true, "old": true, "f-old": true, "c-old": true, "stuck": true}
	for id := range want {
		if !ids[id] {
			t.Errorf("%s must be a victim", id)
		}
	}
	for id := range ids {
		if !want[id] {
			t.Errorf("%s must NOT be a victim", id)
		}
	}
}

// The sweep removes only <relayDir>/<op id>.md, marks the row pruned, and
// touches nothing else — not a sibling file, not a path outside the dir.
func TestSweepRetention_RemovesOnlyOwnFilesAndMarksPruned(t *testing.T) {
	f := newFixture(t)
	relayDir := filepath.Join(f.core.Cfg.DataDir, "relay")
	if err := os.MkdirAll(relayDir, 0o700); err != nil {
		t.Fatal(err)
	}
	write := func(path string) {
		t.Helper()
		if err := os.WriteFile(path, []byte("# handoff"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	outside := filepath.Join(f.core.Cfg.DataDir, "keep.md")
	write(outside)
	// Four done ops in one chain (sessions s1→s2→s3→s4→s5) at days 16–19; now is day 20,
	// so none is 14 d old and only the chain rule applies to them.
	for i := 1; i <= 4; i++ {
		id := "op" + string(rune('0'+i))
		op := doneOp(id, "s"+string(rune('0'+i)), int64(15+i)*day)
		op.HandoffPath = filepath.Join(relayDir, id+".md")
		op.NewSessionID = "s" + string(rune('0'+i+1))
		if err := f.m.store.CreateRelayOp(op); err != nil {
			t.Fatal(err)
		}
		write(op.HandoffPath)
		// lineage rows make them one chain
		if _, err := f.m.store.db.Exec(`INSERT INTO session_lineage (session_id, predecessor_session_id, predecessor_ref, op_id, at) VALUES (?, ?, ?, ?, ?)`,
			op.NewSessionID, op.SessionID, op.Ref, id, op.UpdatedAt); err != nil {
			t.Fatal(err)
		}
	}
	// A 19-day-old row whose path points outside the relay dir: marked pruned, file untouched.
	evil := doneOp("evil", "e1", 1*day)
	evil.HandoffPath = outside
	if err := f.m.store.CreateRelayOp(evil); err != nil {
		t.Fatal(err)
	}
	sibling := filepath.Join(relayDir, "notes.md")
	write(sibling)

	f.clock.Store(20 * day)
	f.m.sweepRetention()

	if _, err := os.Stat(filepath.Join(relayDir, "op1.md")); !os.IsNotExist(err) {
		t.Fatalf("op1.md (4th newest in its chain) must be removed: %v", err)
	}
	for _, id := range []string{"op2", "op3", "op4"} {
		if _, err := os.Stat(filepath.Join(relayDir, id+".md")); err != nil {
			t.Fatalf("%s.md must stay: %v", id, err)
		}
	}
	for _, p := range []string{outside, sibling} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("%s must not be touched: %v", p, err)
		}
	}
	if op := f.op("op1"); !op.Pruned || op.HandoffPath == "" {
		t.Fatalf("op1 = %+v (pruned, path kept)", op)
	}
	if op := f.op("evil"); !op.Pruned {
		t.Fatalf("evil = %+v (marked pruned without removing anything)", op)
	}
	if op := f.op("op2"); op.Pruned {
		t.Fatalf("op2 = %+v", op)
	}
	// Idempotent: a second sweep changes nothing and does not fail on the missing file.
	f.m.sweepRetention()
	if op := f.op("op2"); op.Pruned {
		t.Fatalf("second sweep pruned op2: %+v", op)
	}
}

// Start runs one retention sweep at boot (runRetention's first statement,
// before its hourly ticker) — a file that aged past 14 d while the daemon
// was down is gone once Start's goroutines have been joined by Stop.
// Mutation: drop the boot sweep from runRetention, or do not start
// runRetention in Start → red.
func TestStart_RunsRetentionSweepAtBoot(t *testing.T) {
	f := newFixture(t)
	relayDir := filepath.Join(f.core.Cfg.DataDir, "relay")
	if err := os.MkdirAll(relayDir, 0o700); err != nil {
		t.Fatal(err)
	}
	old := doneOp("old", "o1", 1*day)
	old.HandoffPath = filepath.Join(relayDir, "old.md")
	if err := f.m.store.CreateRelayOp(old); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(old.HandoffPath, []byte("# handoff"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.clock.Store(20 * day)
	if err := f.m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := f.m.Stop(context.Background()); err != nil { // joins the boot sweep
		t.Fatal(err)
	}
	if _, err := os.Stat(old.HandoffPath); !os.IsNotExist(err) {
		t.Fatalf("old.md must be removed by the boot sweep: %v", err)
	}
	if op := f.op("old"); !op.Pruned {
		t.Fatalf("old = %+v (pruned)", op)
	}
}

func TestChainRoots(t *testing.T) {
	s := openTestStore(t)
	for _, r := range [][2]string{{"s2", "s1"}, {"s3", "s2"}, {"t2", "t1"}} {
		if _, err := s.db.Exec(`INSERT INTO session_lineage (session_id, predecessor_session_id, predecessor_ref, op_id, at) VALUES (?, ?, 'x', 'op', 1)`, r[0], r[1]); err != nil {
			t.Fatal(err)
		}
	}
	roots, err := s.ChainRoots()
	if err != nil {
		t.Fatal(err)
	}
	for sid, want := range map[string]string{"s1": "s1", "s2": "s1", "s3": "s1", "t1": "t1", "t2": "t1"} {
		if roots[sid] != want {
			t.Errorf("root(%s) = %q, want %q", sid, roots[sid], want)
		}
	}
}
