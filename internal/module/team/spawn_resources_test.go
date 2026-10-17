package teammod

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/wake/purdex/internal/resources"
	"github.com/wake/purdex/internal/team"
)

// RT-5a: the spawn resource check and its card (spec §4.6).

func zoneReader(zone string) fakeHostReader {
	return fakeHostReader{h: resources.HostUse{Zone: zone, Trend: "rising", Mem: 86, Pressure: 2, Load1: 7.5, NCPU: 10, ZoneSince: 1000}, ok: true}
}

// warnedSpawn posts spawn 1 under a warning zone and returns the waiting op.
func warnedSpawn(t *testing.T, f *fixture, root string) team.SpawnOp {
	t.Helper()
	f.m.hostReader = zoneReader("warning")
	f.m.spawnWait = 10 * time.Millisecond // the POST answers while the op waits
	code, op, e := f.spawn(1, root, nil)
	if code != http.StatusOK || op.State != team.SpawnAwaitingResources {
		t.Fatalf("spawn = %d %+v %+v; want 200 awaiting_resources", code, op, e)
	}
	return op
}

func (f *fixture) seatsOf(teamID string) int {
	f.t.Helper()
	n, err := seatsTaken(f.m.store.db, teamID, "")
	if err != nil {
		f.t.Fatal(err)
	}
	return n
}

// (M) normal and unknown zones go straight ahead; no card exists.
func TestSpawnResources_NormalAndUnknownRunAsToday(t *testing.T) {
	for name, reader := range map[string]resources.HostReader{
		"normal":  zoneReader("normal"),
		"unknown": zoneReader("unknown"),
		"no zone": fakeHostReader{h: resources.HostUse{}, ok: true},
		"no data": fakeHostReader{ok: false},
	} {
		t.Run(name, func(t *testing.T) {
			f, root := newSpawnFixture(t, 2)
			f.register("%0", "sid-m1")
			f.m.hostReader = reader
			code, op, e := f.spawn(1, root, nil)
			if code != http.StatusOK || op.State != team.SpawnDone {
				t.Fatalf("spawn = %d %+v %+v; want done", code, op, e)
			}
			if _, ok, _ := f.m.store.Get(spawnID(1)); ok {
				t.Fatal("a card was written for a spawn that needs none")
			}
		})
	}
}

// (M) warning: the op waits holding its seat, a card with the id of the spawn is open, nothing was started.
func TestSpawnResources_WarningWaitsWithACardAndSeat(t *testing.T) {
	f, root := newSpawnFixture(t, 1)
	op := warnedSpawn(t, f, root)
	if op.Resources == nil || op.Resources.Host.Zone != "warning" || op.Resources.Host.Mem != 86 || op.Resources.Host.Pressure != 2 || op.Resources.TeamID != uid(1) {
		t.Fatalf("resources = %+v", op.Resources)
	}
	a, ok, err := f.m.store.Get(spawnID(1))
	if err != nil || !ok || a.Kind != team.KindSpawnResources || a.State != team.StateOpen ||
		a.DeadlineAt-a.CreatedAt != team.SpawnResourcesDeadlineS*1000 {
		t.Fatalf("card = %+v ok=%v err=%v", a, ok, err)
	}
	if f.sessions.count() != 0 {
		t.Fatalf("a tmux session was created for a spawn that has not been let through")
	}
	if n := f.seatsOf(uid(1)); n != 1 {
		t.Fatalf("seats = %d, want the waiting spawn's one", n)
	}
	if code, _, e := f.spawn(2, root, nil); code != http.StatusConflict || e.Error != team.ErrTeamFull {
		t.Fatalf("second spawn = %d %+v; want team_full while the first holds the seat", code, e)
	}
}

func TestSpawnResources_ApproveRuns(t *testing.T) {
	f, root := newSpawnFixture(t, 1)
	f.register("%0", "sid-m1")
	warnedSpawn(t, f, root)
	if code, body := f.decide(spawnID(1), "approve"); code != http.StatusOK {
		t.Fatalf("decide = %d %s", code, body)
	}
	f.m.spawnWait = 5 * time.Second
	code, op, e := f.spawn(1, root, nil) // the CLI posts again
	if code != http.StatusOK || op.State != team.SpawnDone || op.Member == nil {
		t.Fatalf("after approve = %d %+v %+v; want done", code, op, e)
	}
}

func TestSpawnResources_DenyFailsAndFreesTheSeat(t *testing.T) {
	f, root := newSpawnFixture(t, 1)
	warnedSpawn(t, f, root)
	if code, body := f.decide(spawnID(1), "deny"); code != http.StatusOK {
		t.Fatalf("decide = %d %s", code, body)
	}
	_, op, _ := f.spawn(1, root, nil)
	if op.State != team.SpawnFailed || op.Reason != team.SpawnReasonDenied {
		t.Fatalf("after deny = %+v; want failed spawn_denied", op)
	}
	if n := f.seatsOf(uid(1)); n != 0 {
		t.Fatalf("seats = %d after a denial, want 0", n)
	}
}

// (M) 480 s without an answer: failed spawn_timeout, the seat freed.
func TestSpawnResources_TimeoutFails(t *testing.T) {
	f, root := newSpawnFixture(t, 1)
	warnedSpawn(t, f, root)
	for i := 0; i < 20; i++ { // the CLI keeps polling, so the lease never lapses before the deadline
		f.advance(25_000)
		f.spawn(1, root, nil)
		f.m.tick()
	}
	_, op, _ := f.spawn(1, root, nil)
	if op.State != team.SpawnFailed || op.Reason != team.SpawnReasonTimeout {
		t.Fatalf("after 500 s = %+v; want failed spawn_timeout", op)
	}
	if n := f.seatsOf(uid(1)); n != 0 {
		t.Fatalf("seats = %d, want 0", n)
	}
}

// The CLI stops polling: the lease lapses well before the deadline → spawn_abandoned.
func TestSpawnResources_AbandonedWhenTheCLIStopsPolling(t *testing.T) {
	f, root := newSpawnFixture(t, 1)
	warnedSpawn(t, f, root)
	f.advance(31_000)
	f.m.tick()
	op, _, _ := f.m.store.GetSpawnOp(spawnID(1))
	if op.State != team.SpawnFailed || op.Reason != team.SpawnReasonCLIGone {
		t.Fatalf("op = %+v; want failed spawn_abandoned", op)
	}
}

// (M) unattended: an agent's spawn under warning is refused and nothing is written; the person's spawn gets the card.
func TestSpawnResources_UnattendedRefusesAgentSpawnNotPersons(t *testing.T) {
	f, root := newSpawnFixture(t, 2)
	f.m.hostReader = zoneReader("danger")
	f.unatt.set(true)
	code, _, e := f.spawn(1, root, nil)
	if code != http.StatusConflict || e.Error != team.ErrUnattendedRefused {
		t.Fatalf("agent spawn = %d %+v; want 409 unattended_refused", code, e)
	}
	if _, ok, _ := f.m.store.GetSpawnOp(spawnID(1)); ok {
		t.Fatal("a refused spawn left an op")
	}
	f.m.spawnWait = 10 * time.Millisecond
	tok := f.mintPerson("sid-1", team.PersonCmdSpawn).Token
	code, op, e := f.spawn(2, root, func(r *team.SpawnRequest) { r.PersonToken = tok })
	if code != http.StatusOK || op.State != team.SpawnAwaitingResources {
		t.Fatalf("person spawn = %d %+v %+v; want the card", code, op, e)
	}
}

// A card is never auto-approved, even while unattended is on; the switch-on sweep refuses an agent's open card and leaves a person's.
func TestSpawnResources_SwitchOnRefusesAgentCardKeepsPersons(t *testing.T) {
	f, root := newSpawnFixture(t, 2)
	warnedSpawn(t, f, root) // an agent's
	f.m.hostReader = zoneReader("warning")
	tok := f.mintPerson("sid-1", team.PersonCmdSpawn).Token
	if code, op, e := f.spawn(2, root, func(r *team.SpawnRequest) { r.PersonToken = tok }); code != http.StatusOK || op.State != team.SpawnAwaitingResources {
		t.Fatalf("person spawn = %d %+v %+v", code, op, e)
	}
	f.unatt.set(true)
	f.sweep()
	agent, _, _ := f.m.store.GetSpawnOp(spawnID(1))
	if agent.State != team.SpawnFailed || agent.Reason != team.ErrUnattendedRefused {
		t.Fatalf("agent op = %+v; want failed unattended_refused", agent)
	}
	a, _, _ := f.m.store.Get(spawnID(1))
	if a.State != team.StateCancelled || a.CloseReason != team.ErrUnattendedRefused {
		t.Fatalf("agent card = %+v", a)
	}
	person, _, _ := f.m.store.GetSpawnOp(spawnID(2))
	pa, _, _ := f.m.store.Get(spawnID(2))
	if person.State != team.SpawnAwaitingResources || pa.State != team.StateOpen {
		t.Fatalf("person op %+v card %+v; want both still waiting", person, pa)
	}
}

// A crash between the card's close and the op's follow-up: boot settles the op from the closed card.
func TestSpawnResources_BootSettlesFromTheClosedCard(t *testing.T) {
	f, root := newSpawnFixture(t, 1)
	warnedSpawn(t, f, root)
	if _, won, err := f.m.store.CloseIfOpen(spawnID(1), Close{State: team.StateDenied, DecidedAt: f.clock.Load()}); err != nil || !won {
		t.Fatalf("close the card without the follow-up: won=%v err=%v", won, err)
	}
	if op, _, _ := f.m.store.GetSpawnOp(spawnID(1)); op.State != team.SpawnAwaitingResources {
		t.Fatalf("op = %+v, want it still waiting (the follow-up did not run)", op)
	}
	f.m.resumeSpawns() // boot: settles the waiting op from its closed card before it resumes the running ones
	if op, _, _ := f.m.store.GetSpawnOp(spawnID(1)); op.State != team.SpawnFailed || op.Reason != team.SpawnReasonDenied {
		t.Fatalf("op = %+v; want failed spawn_denied", op)
	}
}

// A store error between the card's close and the op's follow-up heals while the daemon stays up, not only at the next boot.
func TestSpawnResources_SweeperHealsAClosedCardWithAWaitingOp(t *testing.T) {
	f, root := newSpawnFixture(t, 1)
	warnedSpawn(t, f, root)
	if _, won, err := f.m.store.CloseIfOpen(spawnID(1), Close{State: team.StateDenied, DecidedAt: f.clock.Load()}); err != nil || !won {
		t.Fatalf("close the card without the follow-up: won=%v err=%v", won, err)
	}
	for i := 0; i < livenessEvery; i++ {
		f.m.tick()
	}
	if op, _, _ := f.m.store.GetSpawnOp(spawnID(1)); op.State != team.SpawnFailed || op.Reason != team.SpawnReasonDenied {
		t.Fatalf("op = %+v; want failed spawn_denied from the sweeper", op)
	}
}

// Ending the team leaves no card to approve: the sweep cancels it once the op has failed.
func TestSpawnResources_TeamEndCancelsTheCard(t *testing.T) {
	f, root := newSpawnFixture(t, 1)
	warnedSpawn(t, f, root)
	if won, err := f.m.store.EndTeam(uid(1), "sid-1", "test", f.clock.Load()); err != nil || !won {
		t.Fatalf("EndTeam: won=%v err=%v", won, err)
	}
	for i := 0; i < livenessEvery; i++ { // the sweeper's liveness tick runs it
		f.m.tick()
	}
	a, _, _ := f.m.store.Get(spawnID(1))
	if a.State != team.StateCancelled {
		t.Fatalf("card = %+v; want cancelled once its spawn failed with the team", a)
	}
}

// Ending the team fails its waiting op too.
func TestSpawnResources_TeamEndFailsAWaitingOp(t *testing.T) {
	f, root := newSpawnFixture(t, 1)
	warnedSpawn(t, f, root)
	if won, err := f.m.store.EndTeam(uid(1), "sid-1", "test", f.clock.Load()); err != nil || !won {
		t.Fatalf("EndTeam: won=%v err=%v", won, err)
	}
	if op, _, _ := f.m.store.GetSpawnOp(spawnID(1)); op.State != team.SpawnFailed {
		t.Fatalf("op = %+v; want failed with its team", op)
	}
}

// The stored payload's JSON shape is the App contract (plan, App contract item 6): these keys, nested host, arrays never null.
func TestSpawnResources_PayloadShapeIsTheAppContract(t *testing.T) {
	f, root := newSpawnFixture(t, 1)
	warnedSpawn(t, f, root)
	a, _, _ := f.m.store.Get(spawnID(1))
	var got map[string]json.RawMessage
	if err := json.Unmarshal(a.Payload, &got); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"spawn_id", "team_id", "lead_ref", "lead_title", "cwd", "title", "model", "effort", "task_subject", "person", "host", "heavy", "queue"} {
		if _, ok := got[k]; !ok {
			t.Errorf("payload has no %q: %s", k, a.Payload)
		}
	}
	if string(got["heavy"]) != "[]" || string(got["queue"]) != "[]" {
		t.Errorf("heavy = %s queue = %s; want [] when there are none", got["heavy"], got["queue"])
	}
	var host map[string]json.RawMessage
	if err := json.Unmarshal(got["host"], &host); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"zone", "trend", "mem", "pressure", "load1", "ncpu", "sampled_at"} {
		if _, ok := host[k]; !ok {
			t.Errorf("host has no %q: %s", k, got["host"])
		}
	}
}
