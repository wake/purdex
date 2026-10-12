// internal/module/team/pause_remote_notice_test.go — a paused lead tells the members that live on other hosts (unattended quota pause
// spec 2026-10-12 §3.3 "Remote members", UP-3c). They are told, not paused: the lead host queues the team-level commands
// team.lead_paused / team.lead_resumed in the transaction that pauses / resumes; the member host owes each active remote member a
// notice (remote_notices) and its pump delivers it in the fixed texts.
package teammod

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/team"
)

// remoteLeadWorld: sid-1 leads the team with a local member sid-m1 and remote members on hostM (mk1, mk3) and hostN (mk2); unattended
// on, the rule on, the quota 0, the release gate open, the registry showing sid-1.
func remoteLeadWorld(t *testing.T) (*fixture, *fakeHostCaller) {
	t.Helper()
	enablePauseEntry(t)
	f, fc := remoteFixture(t)
	f.liveMember("sid-m1", "_mem001", "worker", "self/w-one", "tm-1")
	if err := f.m.store.InsertMember(newMember("op-m1", uid(1), "sid-m1", "_mem001", f.clock.Load())); err != nil {
		t.Fatal(err)
	}
	f.remoteRow("abc12", "hostM", "mk1", rowActive)
	f.remoteRow("ghi56", "hostM", "mk3", rowActive)
	f.remoteRow("def34", "hostN", "mk2", rowActive)
	f.quotaWorld()
	f.setQuota("sid-1", 0)
	f.origins.show(team.Origin{SessionID: "sid-1", Ref: "_abc123", Title: "boss", Address: "self/boss", PID: 10})
	return f, fc
}

func (f *fixture) cmdHosts(kind string) []string {
	f.t.Helper()
	var out []string
	for _, c := range f.commandsOf(kind) {
		out = append(out, c.HostID)
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ---- the lead host ----

// A lead that pauses queues ONE team.lead_paused per member host with an active remote member — not one per member — and its local
// member is paused as before.
func TestLeadNotice_APausedLeadQueuesLeadPausedForEachMemberHost(t *testing.T) {
	f, _ := remoteLeadWorld(t)
	if code, body := f.beginRaw("sid-1"); code != http.StatusConflict {
		t.Fatalf("begin: %d %s", code, body)
	}
	if got := f.cmdHosts(CmdLeadPaused); !equalStrings(got, []string{"hostM", "hostN"}) {
		t.Fatalf("lead_paused hosts = %v, want hostM, hostN once each", got)
	}
	c := f.commandsOf(CmdLeadPaused)[0]
	var body team.TeamCommand
	if err := json.Unmarshal(c.Body, &body); err != nil || body.Kind != team.CommandLeadPaused || body.TeamID != uid(1) || body.ToHostID != "hostM" || body.MK != "" || body.Lead.SessionID != "sid-1" {
		t.Fatalf("body = %+v (%v)", body, err)
	}
	if _, ok := f.pauses()["sid-m1/lead"]; !ok {
		t.Fatalf("the local member was not paused: %+v", f.pauses())
	}
	if len(f.commandsOf(CmdLeadResumed)) != 0 {
		t.Fatal("a resume was queued by a pause")
	}
}

// The command and the pause are one transaction: a failure after the command was queued leaves neither.
// Mutation gate: queue the command after the commit / in its own transaction → the failed begin still leaves a command (red).
func TestLeadNotice_TheCommandIsQueuedInThePauseTransaction(t *testing.T) {
	f, _ := remoteLeadWorld(t)
	f.m.store.afterLeadNotice = func() error { return errors.New("boom") }
	if code, _ := f.beginRaw("sid-1"); code == http.StatusConflict {
		t.Fatal("the begin paused through an injected failure")
	}
	f.m.store.afterLeadNotice = nil
	if len(f.pauses()) != 0 || len(f.commandsOf(CmdLeadPaused)) != 0 || len(f.outbox()) != 0 {
		t.Fatalf("pauses %d, commands %d, outbox %d: want a rollback of all", len(f.pauses()), len(f.commandsOf(CmdLeadPaused)), len(f.outbox()))
	}
}

// Only ACTIVE remote members of a live team are told: one that is killing, releasing, joining, released or gone is not.
// Mutation gate: count `killing` as a remote member in play → the host gets a command (red).
func TestLeadNotice_OnlyActiveRemoteMembersAreTold(t *testing.T) {
	for _, st := range []string{rowKilling, rowReleasing, rowJoining, rowGone, rowFailed} {
		enablePauseEntry(t)
		f, _ := remoteFixture(t)
		f.remoteRow("abc12", "hostM", "mk1", st)
		f.quotaWorld()
		f.setQuota("sid-1", 0)
		f.origins.show(team.Origin{SessionID: "sid-1", Ref: "_abc123", Title: "boss", Address: "self/boss", PID: 10})
		if code, body := f.beginRaw("sid-1"); code != http.StatusConflict {
			t.Fatalf("%s: begin: %d %s", st, code, body)
		}
		if got := f.cmdHosts(CmdLeadPaused); len(got) != 0 {
			t.Fatalf("a %s remote member was told: %v", st, got)
		}
	}
}

// A solo session (no team) queues nothing.
func TestLeadNotice_ASoloSessionQueuesNothing(t *testing.T) {
	enablePauseEntry(t)
	f := newFixture(t)
	f.quotaWorld()
	f.setQuota("sid-1", 0)
	f.origins.show(team.Origin{SessionID: "sid-1", Ref: "_abc123", Title: "boss", Address: "self/boss", PID: 10})
	if code, body := f.beginRaw("sid-1"); code != http.StatusConflict {
		t.Fatalf("begin: %d %s", code, body)
	}
	if len(f.commandsOf(CmdLeadPaused)) != 0 {
		t.Fatal("a solo session queued a lead_paused")
	}
}

// The release gate closed: no pause, so no command.
func TestLeadNotice_GateClosedQueuesNothing(t *testing.T) {
	disablePauseEntry(t) // the release gate closed: today's held card
	f, _ := remoteFixture(t)
	f.remoteRow("abc12", "hostM", "mk1", rowActive)
	f.quotaWorld()
	f.setQuota("sid-1", 0)
	f.origins.show(team.Origin{SessionID: "sid-1", Ref: "_abc123", Title: "boss", Address: "self/boss", PID: 10})
	f.begin("sid-1")
	if len(f.commandsOf(CmdLeadPaused)) != 0 || len(f.pauses()) != 0 {
		t.Fatalf("commands %d, pauses %d with the gate closed", len(f.commandsOf(CmdLeadPaused)), len(f.pauses()))
	}
}

// The held-card sweep that pauses a lead queues the command too (UP-2b's cascade).
func TestLeadNotice_TheHeldCardSweepQueuesItToo(t *testing.T) {
	enablePauseEntry(t)
	f, _ := remoteFixture(t)
	f.remoteRow("abc12", "hostM", "mk1", rowActive)
	f.qrule.set(true, nil)
	f.setQuota("sid-1", 0)
	f.origins.show(team.Origin{SessionID: "sid-1", Ref: "_abc123", Title: "boss", Address: "self/boss", PID: 10})
	f.begin("sid-1") // unattended off: a person's card
	f.unatt.set(true)
	f.m.tick()
	if _, ok := f.pauses()["sid-1/quota"]; !ok {
		t.Fatalf("the sweep did not pause: %+v", f.pauses())
	}
	if got := f.cmdHosts(CmdLeadPaused); !equalStrings(got, []string{"hostM"}) {
		t.Fatalf("lead_paused hosts = %v", got)
	}
}

// The resume group writes team.lead_resumed for the same hosts: the switch off, and a person's release.
func TestLeadNotice_TheResumeGroupQueuesLeadResumed(t *testing.T) {
	for name, resume := range map[string]func(*fixture){
		"switch off": func(f *fixture) { f.switchOff() },
		"release": func(f *fixture) {
			if code, raw := f.releasePaused("sid-1", appRelease()); code != http.StatusOK {
				f.t.Fatalf("release: %d %s", code, raw)
			}
		},
	} {
		f, _ := remoteLeadWorld(t)
		if code, body := f.beginRaw("sid-1"); code != http.StatusConflict {
			t.Fatalf("%s: begin: %d %s", name, code, body)
		}
		resume(f)
		if got := f.cmdHosts(CmdLeadResumed); !equalStrings(got, []string{"hostM", "hostN"}) {
			t.Fatalf("%s: lead_resumed hosts = %v", name, got)
		}
	}
}

// A member host that does not announce the kind (an older daemon): the entry is DROPPED, not held — the command behind it is still
// sent. An announcing host gets it.
// Mutation gate: hold it instead (no kindDropper) → the next command waits behind it (red).
func TestLeadNotice_AnOlderMemberHostDropsItAndTheQueueGoesOn(t *testing.T) {
	f, fc := remoteFixture(t)
	fc.caps["hostM"] = ipeers.TeamCaps{Kinds: []string{CmdRelease}, AllowTeam: true} // no team.lead_paused
	f.remoteRow("abc12", "hostM", "mk1", rowActive)
	f.enqueue(f.cmd("c1", CmdLeadPaused, "hostM", ""))
	f.enqueue(f.cmd("c2", CmdRelease, "hostM", "mk1"))
	f.m.sweepWG.Add(1)
	go f.m.cmdPump.run()
	f.m.kickCommands()
	waitFor(t, func() bool { return f.cmdState("c2").State == cmdDone })
	if st := f.cmdState("c1").State; st != cmdDropped {
		t.Fatalf("the unannounced command is %s, want dropped", st)
	}
	for _, c := range fc.sent() {
		var b map[string]any
		_ = json.Unmarshal(c.Body, &b)
		if b["kind"] == CmdLeadPaused {
			t.Fatal("an unannounced kind was sent")
		}
	}
}

func TestLeadNotice_AnAnnouncingHostIsSentIt(t *testing.T) {
	f, fc := remoteFixture(t)
	fc.caps["hostM"] = ipeers.TeamCaps{Kinds: append(append([]string{}, allKinds...), CmdLeadPaused), AllowTeam: true}
	f.remoteRow("abc12", "hostM", "mk1", rowActive)
	f.enqueue(f.cmd("c1", CmdLeadPaused, "hostM", ""))
	f.m.sweepWG.Add(1)
	go f.m.cmdPump.run()
	f.m.kickCommands()
	waitFor(t, func() bool { return f.cmdState("c1").State == cmdDone })
}

// ---- the member host ----

// The member host's route takes both kinds from a paired lead host, owes the notices and renders them: the whole path from the wire.
// Mutation gate: leave a kind out of the route's accepted kinds → 400 unsupported_kind (red).
func TestLeadNoticeRoute_ThePairedLeadHostCanSendBothKinds(t *testing.T) {
	f := newFixture(t)
	f.setLeadHost(true)
	f.origins.show(team.Origin{SessionID: "sid-t", Ref: "_tgt001", PID: 42, ProcStart: "ps2", Cwd: "/w"})
	if code, body := f.postCmd(leadPrincipal(), wireAdopt(cmdUUID1, cmdUUID1, "sid-t")); code != http.StatusOK {
		t.Fatalf("adopt: %d %s", code, body)
	}
	for i, kind := range []string{team.CommandLeadPaused, team.CommandLeadResumed} {
		c := relCmd([]string{cmdUUID2, cmdUUID3}[i], kind, "")
		c.ToHostID = "h:1"
		if code, body := f.postCmd(leadPrincipal(), c); code != http.StatusOK {
			t.Fatalf("%s: %d %s", kind, code, body)
		}
	}
	n, err := f.m.store.RemoteNotices(cmdUUID1)
	if err != nil {
		t.Fatal(err)
	}
	// adopted, then lead_paused (superseded by the resume: the pause was still unsent, so nothing more is owed)
	if len(n) != 2 || n[1].Kind != noticeLeadPaused || n[1].State != noticeSuperseded {
		t.Fatalf("notices = %+v", n)
	}
}

func leadNoticeCmd(id, kind string) team.TeamCommand {
	return team.TeamCommand{ID: id, Kind: kind, ToHostID: "h:1", TeamID: "team-L", TeamName: "T",
		Lead: team.TeamLead{SessionID: "lead-sid", Ref: "_lead01", Title: "lead", Address: "lead/x [lead01]", PID: 7, ProcStart: "ps1"}}
}

// The member host owes every ACTIVE remote member of that lead host and team one notice; a released one gets none; a replay of the
// same command owes nothing more.
func TestLeadNoticeApply_OwesEachActiveRemoteMemberOnce(t *testing.T) {
	s := openTestStore(t)
	seedRemote(t, s, "mk-1", "sid-1", 1000)
	seedRemote(t, s, "mk-2", "sid-2", 1000)
	seedRemote(t, s, "mk-3", "sid-3", 1000)
	if ok, err := s.SetRemoteMemberState("mk-3", []string{remoteActive}, remoteReleased, 1000); err != nil || !ok {
		t.Fatal(ok, err)
	}
	p := plan(leadNoticeCmd("c1", team.CommandLeadPaused), true, nil)
	p.LeadHostID = "host-L"
	res := mustApply(t, s, p)
	if res.Status != http.StatusOK {
		t.Fatalf("apply: %d %s", res.Status, res.Body)
	}
	for _, mk := range []string{"mk-1", "mk-2"} {
		n := noticesOf(t, s, mk)
		if len(n) != 1 || n[0].Kind != noticeLeadPaused || n[0].State != noticeOwed {
			t.Fatalf("%s notices = %+v", mk, n)
		}
	}
	if n := noticesOf(t, s, "mk-3"); len(n) != 0 {
		t.Fatalf("a released member was owed %+v", n)
	}
	if r := mustApply(t, s, p); !r.Replayed {
		t.Fatalf("a replay applied again: %+v", r)
	}
	if n := noticesOf(t, s, "mk-1"); len(n) != 1 {
		t.Fatalf("a replay owed more: %+v", n)
	}
}

// A lead_resumed that finds the lead_paused still unsent cancels it and owes nothing (the member never heard of the pause); one that
// finds it sent owes the resume.
// Mutation gate: always owe the resume → the first case owes one (red).
func TestLeadNoticeApply_AResumeSupersedesAnUnsentPause(t *testing.T) {
	s := openTestStore(t)
	seedRemote(t, s, "mk-1", "sid-1", 1000)
	seedRemote(t, s, "mk-2", "sid-2", 1000)
	pp := plan(leadNoticeCmd("c1", team.CommandLeadPaused), true, nil)
	pp.LeadHostID = "host-L"
	mustApply(t, s, pp)
	rows, _ := s.RemoteNotices("mk-2")
	if ok, err := s.SettleRemoteNotice(rows[0].ID, noticeSent, 1000); err != nil || !ok { // mk-2's pause notice went out
		t.Fatal(ok, err)
	}
	rp := plan(leadNoticeCmd("c2", team.CommandLeadResumed), true, nil)
	rp.LeadHostID = "host-L"
	mustApply(t, s, rp)
	n1, n2 := noticesOf(t, s, "mk-1"), noticesOf(t, s, "mk-2")
	if len(n1) != 1 || n1[0].Kind != noticeLeadPaused || n1[0].State != noticeSuperseded {
		t.Fatalf("mk-1 (pause unsent) = %+v, want the pause superseded and no resume", n1)
	}
	if len(n2) != 2 || n2[1].Kind != noticeLeadResumed || n2[1].State != noticeOwed {
		t.Fatalf("mk-2 (pause sent) = %+v, want the resume owed", n2)
	}
}

// A member that joined between the pause and the resume never heard of the pause, so it is not told of the resume; a second pause /
// resume cycle tells the member again.
// Mutation gate: owe the resume to everyone → the late joiner is told (red).
func TestLeadNoticeApply_ALateJoinerIsNotToldOfAResumeItNeverHeardOfAPauseFor(t *testing.T) {
	s := openTestStore(t)
	seedRemote(t, s, "mk-1", "sid-1", 1000)
	pp := plan(leadNoticeCmd("c1", team.CommandLeadPaused), true, nil)
	pp.LeadHostID = "host-L"
	mustApply(t, s, pp)
	rows, _ := s.RemoteNotices("mk-1")
	if ok, err := s.SettleRemoteNotice(rows[0].ID, noticeSent, 1000); err != nil || !ok {
		t.Fatal(ok, err)
	}
	seedRemote(t, s, "mk-9", "sid-9", 1000) // joins after the pause was applied
	rp := plan(leadNoticeCmd("c2", team.CommandLeadResumed), true, nil)
	rp.LeadHostID = "host-L"
	mustApply(t, s, rp)
	if n := noticesOf(t, s, "mk-9"); len(n) != 0 {
		t.Fatalf("the late joiner was owed %+v", n)
	}
	if n := noticesOf(t, s, "mk-1"); len(n) != 2 || n[1].Kind != noticeLeadResumed {
		t.Fatalf("mk-1 = %+v", n)
	}
	// a second cycle: paused again (sent), resumed again ⇒ a second resume is owed
	pp2 := plan(leadNoticeCmd("c3", team.CommandLeadPaused), true, nil)
	pp2.LeadHostID = "host-L"
	mustApply(t, s, pp2)
	all, _ := s.RemoteNotices("mk-1")
	if ok, err := s.SettleRemoteNotice(all[2].ID, noticeSent, 1000); err != nil || !ok {
		t.Fatal(ok, err)
	}
	rp2 := plan(leadNoticeCmd("c4", team.CommandLeadResumed), true, nil)
	rp2.LeadHostID = "host-L"
	mustApply(t, s, rp2)
	if n := noticesOf(t, s, "mk-1"); len(n) != 4 || n[3].Kind != noticeLeadResumed || n[3].State != noticeOwed {
		t.Fatalf("mk-1 after the second cycle = %+v", n)
	}
}

// The pump renders both kinds in the fixed texts, to the member's session as the remote lead.
func TestLeadNoticePump_RendersBothKinds(t *testing.T) {
	for kind, want := range map[string]string{noticeLeadPaused: team.PauseNoticeLeadPaused, noticeLeadResumed: team.PauseResumedMemberLead} {
		f, d := remoteNoticeFixture(t, kind)
		f.m.drainRemoteNotices()
		got := d.calls()
		if len(got) != 1 || got[0].Text != want || got[0].Target.AgentSessionID != "sid-1" {
			t.Fatalf("%s: calls = %+v, want one with %q", kind, got, want)
		}
		if r := noticeRow(t, f, "mk-1"); r.State != noticeSent {
			t.Fatalf("%s: state = %s", kind, r.State)
		}
	}
}

// A notice of a kind this version does not know is dropped (superseded) and the notices behind it still go out: the pump does not stall.
func TestLeadNoticePump_AnUnknownKindIsDroppedAndTheOthersGoOn(t *testing.T) {
	f, d := remoteNoticeFixture(t, "from_the_future")
	owe(t, f, "mk-1", noticeLeadPaused, "cause-2")
	f.m.drainRemoteNotices()
	got := d.calls()
	if len(got) != 1 || got[0].Text != team.PauseNoticeLeadPaused {
		t.Fatalf("calls = %+v, want only the known notice", got)
	}
	rows, _ := f.m.store.RemoteNotices("mk-1")
	if len(rows) != 2 || rows[0].State != noticeSuperseded || rows[1].State != noticeSent {
		t.Fatalf("rows = %+v", rows)
	}
}

// A notice for a member that is no longer active is superseded, not sent.
func TestLeadNoticePump_AMemberThatLeftIsNotTold(t *testing.T) {
	f, d := remoteNoticeFixture(t, noticeLeadPaused)
	moveRemote(t, f, "mk-1", remoteReleased)
	f.m.drainRemoteNotices()
	if len(d.calls()) != 0 {
		t.Fatalf("calls = %+v", d.calls())
	}
	if r := noticeRow(t, f, "mk-1"); r.State != noticeSuperseded {
		t.Fatalf("state = %s", r.State)
	}
}
