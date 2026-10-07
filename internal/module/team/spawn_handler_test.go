package teammod

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/wake/purdex/internal/team"
)

// Spec §7.2 step 2, §15 "Spawn": who may spawn, the limit, the roots and
// the symlink escape, and the body's shape (U20: model and effort again).
// Nothing is created for a refusal. Mutation gates: drop the running ops
// from the count → "team full counting a running op" red; skip
// EvalSymlinks on the cwd → "symlink escape" red.
func TestSpawn_Refusals(t *testing.T) {
	outside := t.TempDir()
	cases := []struct {
		name   string
		setup  func(f *fixture, root string) // optional
		edit   func(r *team.SpawnRequest, root string)
		status int
		code   string
	}{
		{"not a lead", nil, func(r *team.SpawnRequest, _ string) { r.OriginInbox = "/tmp/20.sock" }, 409, team.ErrNotLead},
		{"origin unknown", nil, func(r *team.SpawnRequest, _ string) { r.OriginInbox = "/tmp/99.sock" }, 400, team.ErrOriginUnknown},
		{"team full counting a running op", func(f *fixture, root string) {
			op := spawnRow{ID: spawnID(9), TeamID: uid(1), HostID: "h:1", OriginSessionID: "sid-1", Cwd: root, TmuxName: "tm-0000000900",
				Step: team.StepAccepted, State: team.SpawnRunning, CreatedAt: 1, UpdatedAt: 1}
			mustCreateSpawn(t, f.m.store, op)
		}, nil, 409, team.ErrTeamFull},
		{"team full counting a live member", func(f *fixture, _ string) { seedMember(t, f.m.store, "op-1", uid(1), "sid-m9", 1) }, nil, 409, team.ErrTeamFull},
		{"cwd outside the roots", nil, func(r *team.SpawnRequest, _ string) { r.Cwd = outside }, 409, team.ErrCwdOutsideGrant},
		{"symlink escape", func(_ *fixture, root string) {
			if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
				t.Fatal(err)
			}
		}, func(r *team.SpawnRequest, root string) { r.Cwd = filepath.Join(root, "link") }, 409, team.ErrCwdOutsideGrant},
		{"cwd missing", nil, func(r *team.SpawnRequest, root string) { r.Cwd = filepath.Join(root, "nope") }, 400, team.ErrBadRequest},
		{"cwd relative", nil, func(r *team.SpawnRequest, _ string) { r.Cwd = "w" }, 400, team.ErrBadRequest},
		{"id not a UUID v4", nil, func(r *team.SpawnRequest, _ string) { r.ID = "spawn-1" }, 400, team.ErrBadRequest},
		{"model with a space", nil, func(r *team.SpawnRequest, _ string) { r.Model = "a b" }, 400, team.ErrBadRequest},
		{"model with $(", nil, func(r *team.SpawnRequest, _ string) { r.Model = "$(x)" }, 400, team.ErrBadRequest},
		{"effort out of M25", nil, func(r *team.SpawnRequest, _ string) { r.Effort = "High" }, 400, team.ErrBadRequest},
		{"title not printable", nil, func(r *team.SpawnRequest, _ string) { r.Title = "a\x01" }, 400, team.ErrBadRequest},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f, root := newSpawnFixture(t, 1)
			if c.setup != nil {
				c.setup(f, root)
			}
			code, _, e := f.spawn(spawnID(1), root, func(r *team.SpawnRequest) {
				if c.edit != nil {
					c.edit(r, root)
				}
			})
			if code != c.status || e.Error != c.code {
				t.Fatalf("spawn = %d %+v, want %d %s", code, e, c.status, c.code)
			}
			if _, found, _ := f.m.store.GetSpawnOp(spawnID(1)); found || f.sessions.count() != 0 {
				t.Fatalf("a refused spawn left an op (%v) or created a session (%d)", found, f.sessions.count())
			}
		})
	}
}

// Spec §13 D4: the limit counts live members only. An active member whose
// session ended frees its place, unless its relay is in flight (the old
// session leaves the registry at the relay's /clear).
func TestSpawn_LimitCountsLiveMembersOnly(t *testing.T) {
	f, root := newSpawnFixture(t, 1)
	seedMember(t, f.m.store, "op-1", uid(1), "sid-m9", 1)
	f.origins.markDead("sid-m9")
	claimedOp(t, f.m.store, rid(1), "sid-m9", "_mem9")
	if code, _, e := f.spawn(spawnID(1), root, nil); code != 409 || e.Error != team.ErrTeamFull {
		t.Fatalf("a member mid-relay must hold its place: %d %+v", code, e)
	}
	if _, _, err := f.m.store.ReportRelay(rid(1), RelayReport{State: team.RelayFailed, Reason: "member_gone", At: 2}); err != nil {
		t.Fatal(err)
	}
	f.register("%0", "sid-m1")
	if code, op, e := f.spawn(spawnID(1), root, nil); code != 200 || op.State != team.SpawnDone {
		t.Fatalf("a gone member's place is free: %d %+v %+v", code, op, e)
	}
}

// Spec §7.2 step 3, §9.1: a POST is create-or-join by id. The same body
// joins the op (one create, no check again: the team is full by then);
// another body under the id is 409 id_conflict.
func TestSpawn_ReplayJoinsTheSameOp(t *testing.T) {
	f, root := newSpawnFixture(t, 1)
	f.register("%0", "sid-m1")
	_, first, _ := f.spawn(spawnID(1), root, func(r *team.SpawnRequest) { r.Title = "worker" })
	code, again, e := f.spawn(spawnID(1), root, func(r *team.SpawnRequest) { r.Title = "worker" })
	if code != 200 || again.State != team.SpawnDone || again.Member == nil || again.Member.SessionID != first.Member.SessionID {
		t.Fatalf("replay = %d %+v %+v", code, again, e)
	}
	if n := f.sessions.count(); n != 1 {
		t.Fatalf("creates = %d, want 1", n)
	}
	if code, _, e := f.spawn(spawnID(1), root, func(r *team.SpawnRequest) { r.Title = "other" }); code != 409 || e.Error != team.ErrIDConflict {
		t.Fatalf("another body under the id = %d %+v", code, e)
	}
}
