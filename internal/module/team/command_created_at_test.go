package teammod

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/wake/purdex/internal/team"
)

// #2398: an adopt / spawn command carries created_at (the lead host's clock, unix ms, stamped when the command is
// queued); the member host refuses one older than the lead's 10 minute expiry plus a skew allowance. An absent field
// (an older lead) keeps today's behaviour; an older member ignores the field.

const commandMaxAgeMS = 12 * 60 * 1000 // 10 minutes of expiry + 2 of clock skew, written out so the policy is pinned

func ageAt(now, age int64) int64 { return now - age }

// ---- member side ----

func TestApplyAdopt_RefusesACommandPastTheExpiryAndTheSkew(t *testing.T) {
	const now = int64(500_000_000_000)
	for name, c := range map[string]struct {
		age  int64
		want int
	}{
		"fresh":                  {60_000, http.StatusOK},
		"at the expiry":          {commandExpiryMS, http.StatusOK},
		"inside the skew":        {commandMaxAgeMS - 1, http.StatusOK},
		"exactly the limit":      {commandMaxAgeMS, http.StatusOK},
		"one ms past the limit":  {commandMaxAgeMS + 1, http.StatusConflict},
		"a day old":              {24 * 3600 * 1000, http.StatusConflict},
		"created in the future":  {-5 * 60 * 1000, http.StatusOK}, // the lead's clock is ahead: not an old command
		"created at 1 ms (zero)": {now - 1, http.StatusConflict},
	} {
		s := openTestStore(t)
		cmd := adoptCmd("c1", "mk-1", "sid-t")
		cmd.CreatedAt = ageAt(now, c.age)
		p := plan(cmd, true, targetOrigin("sid-t"))
		p.Now = now
		res := mustApply(t, s, p)
		if res.Status != c.want {
			t.Errorf("%s: status %d, want %d (%s)", name, res.Status, c.want, res.Body)
		}
		if c.want == http.StatusConflict && refusalCode(t, res) != team.ErrCommandExpired {
			t.Errorf("%s: code %q, want %q", name, refusalCode(t, res), team.ErrCommandExpired)
		}
	}
}

// A negative created_at is not a time a lead can have written: refused (and without an overflow for the extremes). Zero is
// "absent" (the lead omits it), and a time in the future is accepted: the check is about OLD commands, and refusing a
// lead whose clock runs ahead would stop every adopt instead of only degrading to today's behaviour.
func TestApplyAdopt_ANegativeCreatedAtIsRefused(t *testing.T) {
	for _, v := range []int64{-1, -1 << 62, -1 << 63} {
		s := openTestStore(t)
		cmd := adoptCmd("c1", "mk-1", "sid-t")
		cmd.CreatedAt = v
		p := plan(cmd, true, targetOrigin("sid-t"))
		p.Now = 500_000_000_000
		if res := mustApply(t, s, p); res.Status != http.StatusConflict || refusalCode(t, res) != team.ErrCommandExpired {
			t.Errorf("created_at %d: %d %s", v, res.Status, res.Body)
		}
	}
	for _, v := range []int64{1 << 62, 1<<63 - 1} { // far in the future: accepted, no overflow
		s := openTestStore(t)
		cmd := adoptCmd("c2", "mk-2", "sid-t")
		cmd.CreatedAt = v
		p := plan(cmd, true, targetOrigin("sid-t"))
		p.Now = 500_000_000_000
		if res := mustApply(t, s, p); res.Status != http.StatusOK {
			t.Errorf("created_at %d: %d %s", v, res.Status, res.Body)
		}
	}
}

// A refused adopt applies nothing and is logged like every refusal, so its replay is the same refusal.
func TestApplyAdopt_AnExpiredCommandChangesNothingAndReplaysAsRefused(t *testing.T) {
	const now = int64(500_000_000_000)
	s := openTestStore(t)
	cmd := adoptCmd("c1", "mk-1", "sid-t")
	cmd.CreatedAt = now - commandMaxAgeMS - 1
	p := plan(cmd, true, targetOrigin("sid-t"))
	p.Now = now
	first := mustApply(t, s, p)
	if first.Status != http.StatusConflict || first.Replayed {
		t.Fatalf("first: %+v", first)
	}
	if role, _ := s.SessionRole("sid-t"); role != sessionRoleNone {
		t.Fatalf("an expired adopt made role %s", role)
	}
	if n := len(noticesOf(t, s, "mk-1")); n != 0 {
		t.Fatalf("an expired adopt owes %d notices", n)
	}
	again := mustApply(t, s, p)
	if again.Status != http.StatusConflict || !again.Replayed || string(again.Body) != string(first.Body) {
		t.Fatalf("replay: %+v", again)
	}
}

// No created_at (an older lead): today's behaviour, however late "now" is.
func TestApplyAdopt_NoCreatedAtIsNotRefused(t *testing.T) {
	s := openTestStore(t)
	cmd := adoptCmd("c1", "mk-1", "sid-t")
	p := plan(cmd, true, targetOrigin("sid-t"))
	p.Now = 1 << 50
	if res := mustApply(t, s, p); res.Status != http.StatusOK {
		t.Fatalf("no created_at: %+v %s", res, res.Body)
	}
}

// A command already applied is answered from the log when it is replayed, however old the command has become: the age
// check is for new commands only (#2265 keeps those log rows for exactly this).
func TestApplyAdopt_ReplayOfAnAppliedCommandIgnoresItsAge(t *testing.T) {
	const now = int64(500_000_000_000)
	s := openTestStore(t)
	cmd := adoptCmd("c1", "mk-1", "sid-t")
	cmd.CreatedAt = now - 60_000
	p := plan(cmd, true, targetOrigin("sid-t"))
	p.Now = now
	if res := mustApply(t, s, p); res.Status != http.StatusOK {
		t.Fatalf("apply: %+v", res)
	}
	p.Now = now + 3*3600*1000
	if res := mustApply(t, s, p); res.Status != http.StatusOK || !res.Replayed {
		t.Fatalf("late replay: %+v %s", res, res.Body)
	}
}

// Only adopt and spawn carry the check: a release (or any other kind) with an old created_at is applied as before.
func TestApply_OtherKindsAreNotAgeChecked(t *testing.T) {
	const now = int64(500_000_000_000)
	s := openTestStore(t)
	cmd := team.TeamCommand{ID: "c9", Kind: team.CommandEnd, ToHostID: "h:1", TeamID: "team-L", CreatedAt: 1}
	p := plan(cmd, true, nil)
	p.Now = now
	if res := mustApply(t, s, p); refusalCode(t, res) == team.ErrCommandExpired {
		t.Fatalf("an end command was age-checked: %s", res.Body)
	}
}

func TestRemoteSpawn_RefusesAnExpiredCommandAndAcceptsAFreshOne(t *testing.T) {
	f, root := remoteSpawnFixture(t)
	f.register("%0", "sid-m1")
	old := spawnCommand(cmdUUID1, root)
	old.CreatedAt = f.m.now() - commandMaxAgeMS - 1
	code, body := f.postCmd(leadPrincipal(), old)
	if code != http.StatusConflict || errCode(t, body) != team.ErrCommandExpired {
		t.Fatalf("expired spawn = %d %s", code, body)
	}
	var n int
	f.m.store.db.QueryRow(`SELECT COUNT(*) FROM spawn_ops`).Scan(&n)
	if n != 0 {
		t.Fatalf("an expired spawn made %d spawn ops", n)
	}
	fresh := spawnCommand(cmdUUID2, root)
	fresh.CreatedAt = f.m.now() - 30_000
	if code, body := f.postCmd(leadPrincipal(), fresh); code != http.StatusOK || outcomeState(t, body) != "accepted" {
		t.Fatalf("fresh spawn = %d %s", code, body)
	}
	f.m.spawnWG.Wait()
	// and one without the field (an older lead)
	f.register("%1", "sid-m2")
	if code, body := f.postCmd(leadPrincipal(), spawnCommand(cmdUUID3, root)); code != http.StatusOK || outcomeState(t, body) != "accepted" {
		t.Fatalf("spawn without created_at = %d %s", code, body)
	}
	f.m.spawnWG.Wait()
}

// ---- compatibility: an older member ignores the field ----

// The member's decode is not strict (json.Unmarshal into TeamCommand, in the route and in the store): a field it does
// not know — what an older member sees of created_at — is ignored, and the command is applied.
func TestCommands_AFieldTheMemberDoesNotKnowIsIgnored(t *testing.T) {
	f := newFixture(t)
	f.setLeadHost(true)
	f.origins.show(team.Origin{SessionID: "sid-t", Ref: "_tgt001", PID: 42, ProcStart: "ps2", Cwd: "/w"})
	raw, _ := json.Marshal(wireAdopt(cmdUUID1, cmdUUID1, "sid-t"))
	withExtra := strings.TrimSuffix(string(raw), "}") + `,"a_field_from_a_newer_lead":123}`
	if code, body := f.postCmd(leadPrincipal(), withExtra); code != http.StatusOK {
		t.Fatalf("unknown field: %d %s", code, body)
	}
}

func TestCommand_CreatedAtIsOmittedWhenZero(t *testing.T) {
	raw, _ := json.Marshal(adoptCmd("c1", "mk-1", "sid-t"))
	if strings.Contains(string(raw), "created_at") {
		t.Fatalf("a command without created_at encodes it: %s", raw)
	}
}

// ---- lead side ----

func (f *fixture) storedBody(id string) team.TeamCommand {
	f.t.Helper()
	c, ok, err := f.m.store.GetCommand(id)
	if err != nil || !ok {
		f.t.Fatalf("command %s: ok=%v err=%v", id, ok, err)
	}
	var tc team.TeamCommand
	if err := json.Unmarshal(c.Body, &tc); err != nil {
		f.t.Fatal(err)
	}
	return tc
}

// Queuing stamps created_at = the row's created_at on an adopt and a spawn, and only on those.
func TestEnqueue_StampsCreatedAtOnAdoptAndSpawnOnly(t *testing.T) {
	f, _ := cmdFixture(t)
	f.enqueue(f.cmd("a1", CmdAdopt, "hostM", "mk1"))
	f.enqueue(f.cmd("s1", CmdSpawn, "hostM", "mk2"))
	f.enqueue(f.cmd("r1", CmdRelease, "hostM", "mk3"))
	now := f.clock.Load()
	for _, id := range []string{"a1", "s1"} {
		if tc, row := f.storedBody(id), f.cmdState(id); tc.CreatedAt != now || row.CreatedAt != now {
			t.Errorf("%s: body created_at %d, row %d, want %d", id, tc.CreatedAt, row.CreatedAt, now)
		}
	}
	if tc := f.storedBody("r1"); tc.CreatedAt != 0 {
		t.Errorf("a release is stamped: %d", tc.CreatedAt)
	}
}

// A replay of the same id (the same command built again, later) is the same command, and the stored body keeps the
// FIRST created_at: the bytes the member host sees never change (its log compares them), and withoutLook-style
// comparisons are not fooled by the new field.
func TestEnqueue_AReplayKeepsTheFirstCreatedAt(t *testing.T) {
	f, _ := cmdFixture(t)
	first := f.clock.Load()
	f.enqueue(f.cmd("a1", CmdAdopt, "hostM", "mk1"))
	before := f.cmdState("a1").Body
	f.clock.Add(3 * 60 * 1000)
	f.enqueue(f.cmd("a1", CmdAdopt, "hostM", "mk1")) // must not fail as "the id is taken by another command"
	after := f.cmdState("a1").Body
	if string(before) != string(after) || f.storedBody("a1").CreatedAt != first {
		t.Fatalf("replay changed the stored command: %s → %s", before, after)
	}
	// the same id with a different content is still a conflict
	tx, _ := f.m.store.db.Begin()
	defer tx.Rollback()
	other := f.cmd("a1", CmdAdopt, "hostM", "mk1")
	other.Body = []byte(strings.Replace(string(other.Body), `"sid-1"`, `"sid-other"`, 1))
	if err := f.m.store.EnqueueCommand(tx, other, f.clock.Load()); err == nil {
		t.Fatal("a different command under the same id was accepted")
	}
}

// The lead takes the new refusal as any permanent refusal: the adopt row fails with its code (the seat is freed) and the
// command is settled — never sent again.
func TestRemote_AnExpiredRefusalSettlesTheCommandAndFailsTheRow(t *testing.T) {
	f, _ := cmdFixture(t)
	f.remoteRow("op-j", "hostM", "mk1", rowJoining)
	f.settleRemote(CmdAdopt, "a1", "mk1", refusedBy(team.ErrCommandExpired))
	if st, why := f.memberRowState("op-j"); st != rowFailed || why != team.ErrCommandExpired {
		t.Fatalf("row = %s/%s, want failed/%s", st, why, team.ErrCommandExpired)
	}
	if c := f.cmdState("a1"); c.State != "done" {
		t.Fatalf("command state = %s, want done (not sent again)", c.State)
	}
}
