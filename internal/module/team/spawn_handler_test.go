package teammod

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/team"
)

// ResolveOrigin adds the address the registry would carry ("mlab/<name>").
func (s *spawnOrigins) ResolveOrigin(inbox string) (team.Origin, bool, error) {
	o, ok, err := s.fakeOrigins.ResolveOrigin(inbox)
	o.Address = "mlab/" + o.Name
	return o, ok, err
}

// spawn posts a spawn of op i from sid-1 into cwd; edit changes the body.
func (f *fixture) spawn(i int, cwd string, edit func(*team.SpawnRequest)) (int, team.SpawnOp, team.APIError) {
	f.t.Helper()
	req := team.SpawnRequest{ID: spawnID(i), OriginInbox: "/tmp/10.sock", Cwd: cwd}
	if edit != nil {
		edit(&req)
	}
	code, body := f.do(http.MethodPost, "/api/team/spawns", req)
	var op team.SpawnOp
	if code != http.StatusOK {
		return code, op, decodeErr(f.t, body)
	}
	if err := json.Unmarshal(body, &op); err != nil {
		f.t.Fatalf("decode spawn op: %v; %s", err, body)
	}
	return code, op, team.APIError{}
}

// holdRunners keeps every runner polling for its registration until Stop,
// and makes the POST answer at once: a spawn stays running.
func (f *fixture) holdRunners() {
	f.m.spawnSleep = func(ctx context.Context, _ time.Duration) { <-ctx.Done() }
	f.m.spawnWait = 10 * time.Millisecond
}

// Spec §7.2 steps 1–2 and 6: the POST answers done with the member (ref,
// address, tmux session, session id) and the lead's address for the brief.
func TestSpawn_PostAnswersTheMemberAndTheLeadAddress(t *testing.T) {
	f, root := newSpawnFixture(t, 2)
	f.register("%0", "sid-m1")
	code, op, e := f.spawn(1, root, func(r *team.SpawnRequest) { r.Model, r.Effort, r.Title = "opus[1m]", "high", "worker" })
	ref := ipeers.RefID("sid-m1")
	want := team.Member{SessionID: "sid-m1", Ref: ref, Address: "mlab/" + ref, TeamID: uid(1), HostID: "h:1", Title: "worker",
		Cwd: root, TmuxSession: "tm-0000000100", State: team.MemberActive, Origin: team.MemberOriginSpawned, Model: "opus[1m]", Effort: "high", SpawnOp: spawnID(1)}
	if code != 200 || op.State != team.SpawnDone || op.LeadAddress != "mlab/n10" || op.Member == nil {
		t.Fatalf("spawn = %d %+v %+v", code, op, e)
	}
	want.CreatedAt = op.Member.CreatedAt
	if *op.Member != want {
		t.Fatalf("member = %+v\nwant     %+v", *op.Member, want)
	}
}

// Spec §7.2 step 2, §15 "Spawn": who may spawn, the roots and the symlink
// escape, and the body's shape (U20: model and effort again). Nothing is
// created for a refusal. Mutation gate: skip EvalSymlinks on the cwd →
// "symlink escape" red.
func TestSpawn_Refusals(t *testing.T) {
	outside := t.TempDir()
	cases := []struct {
		name   string
		setup  func(f *fixture, root string)
		edit   func(r *team.SpawnRequest, root string)
		status int
		code   string
	}{
		{"not a lead", nil, func(r *team.SpawnRequest, _ string) { r.OriginInbox = "/tmp/20.sock" }, 409, team.ErrNotLead},
		{"origin unknown", nil, func(r *team.SpawnRequest, _ string) { r.OriginInbox = "/tmp/99.sock" }, 400, team.ErrOriginUnknown},
		{"team full counting a running op", func(f *fixture, root string) { f.acceptOp(9, root, nil) }, nil, 409, team.ErrTeamFull},
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
			code, _, e := f.spawn(1, root, func(r *team.SpawnRequest) {
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

// Review H1: the lead and the limit are checked again in the write
// transaction that inserts the op, so what changes after the handler read
// the team wins: the team ended, the lead moved (a relay), or another
// writer took the last place. Mutation gates: drop the team condition →
// the first two red; drop the count → the third red.
func TestSpawn_TheTeamAndTheLimitAreCheckedWhereTheOpIsWritten(t *testing.T) {
	cases := []struct {
		name string
		race func(f *fixture, root string)
		code string
	}{
		{"team ended", func(f *fixture, _ string) { _, _ = f.m.store.EndTeam(uid(1), "sid-1", team.TeamEndLeadGone, 2) }, team.ErrNotLead},
		{"lead moved", func(f *fixture, _ string) {
			_, _ = f.m.store.db.Exec(`UPDATE teams SET lead_session_id = 'sid-1b' WHERE id = ?`, uid(1))
		}, team.ErrNotLead},
		{"last place taken", func(f *fixture, root string) { f.acceptOp(9, root, nil) }, team.ErrTeamFull},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f, root := newSpawnFixture(t, 1)
			f.m.afterSpawnTeamRead = func() { c.race(f, root) }
			if code, _, e := f.spawn(1, root, nil); code != 409 || e.Error != c.code {
				t.Fatalf("spawn = %d %+v, want 409 %s", code, e, c.code)
			}
			if _, found, _ := f.m.store.GetSpawnOp(spawnID(1)); found {
				t.Fatal("the op was written")
			}
		})
	}
}

// Review H1: of two concurrent POSTs for the last place, one is accepted.
func TestSpawn_TwoConcurrentSpawnsForTheLastPlace(t *testing.T) {
	f, root := newSpawnFixture(t, 1)
	f.holdRunners()
	codes := make([]int, 2)
	var wg sync.WaitGroup
	for i := range codes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes[i], _, _ = f.spawn(i+1, root, nil)
		}()
	}
	wg.Wait()
	if codes[0]+codes[1] != 200+409 {
		t.Fatalf("codes = %v, want one 200 and one 409", codes)
	}
}

// P4-5 critic on H1, ruled by the coordinator: the limit is counted from
// team.db alone, inside the write transaction. An active member row holds
// its place even when the registry no longer shows its session; the place
// frees once the row is marked gone (P4-6's sweeper; spec §13 D4). A
// registry read before the transaction could not be trusted at its commit.
// Mutation gate: deduct the members the registry shows not live → red.
func TestSpawn_AnActiveMemberHoldsItsPlaceUntilMarkedGone(t *testing.T) {
	f, root := newSpawnFixture(t, 1)
	seedMember(t, f.m.store, "op-1", uid(1), "sid-m9", 1)
	f.origins.markDead("sid-m9")
	if code, _, e := f.spawn(1, root, nil); code != 409 || e.Error != team.ErrTeamFull {
		t.Fatalf("an active member row must hold its place: %d %+v", code, e)
	}
	if err := f.m.store.SetMemberState("op-1", team.MemberGone, 2); err != nil {
		t.Fatal(err)
	}
	f.register("%0", "sid-m1")
	if code, op, e := f.spawn(1, root, nil); code != 200 || op.State != team.SpawnDone {
		t.Fatalf("a gone member's place is free: %d %+v %+v", code, op, e)
	}
}

// Spec §7.2 step 3, §9.1: a POST is create-or-join by id. The same body
// joins the op (one create, no check again: the team is full by then);
// another body under the id is 409 id_conflict.
func TestSpawn_ReplayJoinsTheSameOp(t *testing.T) {
	f, root := newSpawnFixture(t, 1)
	f.register("%0", "sid-m1")
	titled := func(r *team.SpawnRequest) { r.Title = "worker" }
	_, first, _ := f.spawn(1, root, titled)
	code, again, e := f.spawn(1, root, titled)
	if code != 200 || again.State != team.SpawnDone || again.Member == nil || first.Member == nil || again.Member.SessionID != first.Member.SessionID {
		t.Fatalf("replay = %d %+v %+v", code, again, e)
	}
	if n := f.sessions.count(); n != 1 {
		t.Fatalf("creates = %d, want 1", n)
	}
	if code, _, e := f.spawn(1, root, func(r *team.SpawnRequest) { r.Title = "other" }); code != 409 || e.Error != team.ErrIDConflict {
		t.Fatalf("another body under the id = %d %+v", code, e)
	}
}
