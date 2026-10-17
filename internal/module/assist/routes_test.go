package assistmod

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wake/purdex/internal/gate"
	"github.com/wake/purdex/internal/team"
)

func TestAdminOnly(t *testing.T) {
	r := newRig(t)
	r.addHost("air26")
	for _, c := range []struct{ method, path string }{
		{"GET", "/api/assist"}, {"POST", "/api/assist/host"}, {"DELETE", "/api/assist/host/air26"}, {"POST", "/api/assist/switch"},
		{"POST", "/api/assist/allow"}, {"POST", "/api/assist/deny"}, {"GET", "/api/assist/hosts"},
	} {
		if code, _ := r.doAs(c.method, c.path, map[string]any{}, true); code != 403 {
			t.Errorf("%s %s with a device token: %d", c.method, c.path, code)
		}
	}
	if r.count("assist-") != 0 {
		t.Fatal("a refused request ran a script")
	}
}

func TestHostSetKeepsTheFieldsNotGiven(t *testing.T) {
	r := newRig(t)
	body := map[string]any{"alias": "air26", "ssh": "air26-lan", "scripts": r.scripts, "slots": 3, "can": []string{"vitest", "tsc"},
		"cannot": []string{"go vet ./..."}, "load_limit": 1.5, "idle_min": 20, "hold_min": 7}
	if code, b := r.do("POST", "/api/assist/host", body); code != 200 || b["result"] != "added" {
		t.Fatalf("%d %v", code, b)
	}
	g := r.file().Hosts["air26"].Gen
	if code, b := r.do("POST", "/api/assist/host", map[string]any{"alias": "air26", "slots": 4}); code != 200 || b["result"] != "changed" {
		t.Fatalf("%d %v", code, b)
	}
	h := r.file().Hosts["air26"]
	if h.Slots != 4 || h.SSH != "air26-lan" || h.Scripts != r.scripts || len(h.Can) != 2 || len(h.Cannot) != 1 || h.LoadLimit != 1.5 || h.IdleMin != 20 || h.HoldMin != 7 {
		t.Fatalf("set lost a field: %+v", h)
	}
	if h.Gen <= g {
		t.Fatal("a change took no new generation")
	}
	if _, b := r.do("POST", "/api/assist/host", map[string]any{"alias": "air26", "slots": 4}); b["result"] != "unchanged" || r.file().Hosts["air26"].Gen != h.Gen {
		t.Fatalf("an unchanged set took a generation: %v", b)
	}
	r.do("POST", "/api/assist/host", map[string]any{"alias": "air26", "can": []string{"pnpm"}, "clear_cannot": true})
	h = r.file().Hosts["air26"]
	if len(h.Can) != 1 || h.Can[0] != "pnpm" || len(h.Cannot) != 0 {
		t.Fatalf("can / clear_cannot: %+v", h)
	}
	r.do("POST", "/api/assist/host", map[string]any{"alias": "air26", "clear_can": true})
	if h = r.file().Hosts["air26"]; len(h.Can) != 0 {
		t.Fatalf("clear_can: %+v", h)
	}
}

func TestHostRefusals(t *testing.T) {
	r := newRig(t)
	for _, b := range []map[string]any{
		{"alias": "Bad Name", "scripts": "/x"}, {"alias": "a", "ssh": "-oProxyCommand=x", "scripts": "/x"}, {"alias": "a"},
		{"alias": "a", "scripts": "rel"}, {"alias": "a", "scripts": "/x", "slots": -1}, {"alias": "a", "scripts": "/x", "can": []string{"a\nb"}},
	} {
		if code, _ := r.do("POST", "/api/assist/host", b); code != 400 {
			t.Errorf("%v: %d", b, code)
		}
	}
}

func TestHostChangesTakeANewGenAndRemoveThenAddNeverMeetsOldReadiness(t *testing.T) {
	r := newRig(t)
	r.addHost("air26")
	g1 := r.file().Hosts["air26"].Gen
	r.do("POST", "/api/assist/host", map[string]any{"alias": "air26", "ssh": "other"})
	g2 := r.file().Hosts["air26"].Gen
	r.do("POST", "/api/assist/host", map[string]any{"alias": "air26", "scripts": r.scripts + "/"})
	g3 := r.file().Hosts["air26"].Gen
	if !(g1 < g2 && g2 < g3) {
		t.Fatalf("%d %d %d", g1, g2, g3)
	}
	r.m.setReady("air26", r.readyOf(g3, gateAnswerRepo("on", "purdex")))
	if code, _ := r.do("DELETE", "/api/assist/host/air26", nil); code != 200 {
		t.Fatal(code)
	}
	r.do("POST", "/api/assist/host", map[string]any{"alias": "air26", "scripts": r.scripts})
	if h := r.file().Hosts["air26"]; h.Gen <= g3 {
		t.Fatalf("re-added host reused generation %d (old %d)", h.Gen, g3)
	}
	r.edit(func(f *gate.AssistFile) {
		h := f.Hosts["air26"]
		h.On = true
		f.Hosts["air26"] = h
		f.Allowed = []gate.AssistAllow{{Kind: "team", ID: "t1"}}
	})
	if ok, why := gate.Usable(r.file(), r.m.readyCopy(), r.clock.now(), "air26", []gate.AssistEntry{{Kind: "team", ID: "t1"}}, "purdex"); ok {
		t.Fatalf("usable on the old check: %s", why)
	}
}

func TestHostRemoveRefusedWhileOnOrBusy(t *testing.T) {
	r := newRig(t)
	r.addHost("air26")
	r.edit(func(f *gate.AssistFile) { h := f.Hosts["air26"]; h.On = true; f.Hosts["air26"] = h })
	if code, b := r.do("DELETE", "/api/assist/host/air26", nil); code != 409 || b["error"] != "switch_on" {
		t.Fatalf("%d %v", code, b)
	}
	r.edit(func(f *gate.AssistFile) {
		h := f.Hosts["air26"]
		h.Op = &gate.AssistOp{Kind: "on", ID: "x"}
		f.Hosts["air26"] = h
	})
	if code, b := r.do("DELETE", "/api/assist/host/air26", nil); code != 409 || b["error"] != "busy" {
		t.Fatalf("%d %v", code, b)
	}
	if code, _ := r.do("DELETE", "/api/assist/host/nohost", nil); code != 404 {
		t.Fatal(code)
	}
}

func (r *rig) liveTeam(id, name, label, lead string) {
	r.teams.teams = append(r.teams.teams, team.Team{ID: id, TeamName: name, TeamLabel: label, LeadSessionID: lead})
}

func (r *rig) live(sid, ref, addr string) {
	r.origins.live[sid] = team.Origin{SessionID: sid, Ref: ref, Address: addr}
}

func allowReq(kind, id string) map[string]any {
	return map[string]any{"kind": kind, "id": id, "reason": "because"}
}

func TestAllowTeamByIDNameAndLeadAddress(t *testing.T) {
	r := newRig(t)
	r.addHost("air26")
	r.liveTeam("t1", "alpha", "", "lead1")
	r.liveTeam("t2", "beta", "", "lead2")
	r.live("lead2", "_lead22", "mlab/purdex-lead2")
	for _, arg := range []string{"t1", "alpha"} {
		if code, b := r.do("POST", "/api/assist/allow", allowReq("team", arg)); code != 200 || b["id"] != "t1" {
			t.Fatalf("%s: %d %v", arg, code, b)
		}
	}
	if len(r.file().Allowed) != 1 {
		t.Fatalf("allowed twice: %+v", r.file().Allowed)
	}
	if _, b := r.do("POST", "/api/assist/allow", allowReq("team", "alpha")); b["result"] != "already" {
		t.Fatalf("%v", b)
	}
	if code, b := r.do("POST", "/api/assist/allow", allowReq("team", "mlab/purdex-lead2")); code != 200 || b["id"] != "t2" {
		t.Fatalf("lead address: %d %v", code, b)
	}
	if a := r.file().Allowed[0]; a.Name != "alpha" || a.By != "person" || a.Reason != "because" || a.Host != "" {
		t.Fatalf("%+v", a)
	}
	if code, _ := r.do("POST", "/api/assist/allow", allowReq("team", "ghost")); code != 404 {
		t.Fatalf("unknown team: %d", code)
	}
}

func TestAllowAmbiguousNameIsRefusedAndListsBoth(t *testing.T) {
	r := newRig(t)
	r.liveTeam("t1", "same", "", "l1")
	r.liveTeam("t2", "same", "", "l2")
	code, b := r.do("POST", "/api/assist/allow", allowReq("team", "same"))
	if code != 409 || b["error"] != "ambiguous" || !strings.Contains(b["detail"].(string), "t1") || !strings.Contains(b["detail"].(string), "t2") {
		t.Fatalf("%d %v", code, b)
	}
	if len(r.file().Allowed) != 0 {
		t.Fatal("an ambiguous name was stored")
	}
}

func TestAllowRefusals(t *testing.T) {
	r := newRig(t)
	r.liveTeam("t1", "alpha", "", "l1")
	b := allowReq("team", "t1")
	b["reason"] = ""
	if code, _ := r.do("POST", "/api/assist/allow", b); code != 400 {
		t.Fatalf("no reason: %d", code)
	}
	b = allowReq("team", "t1")
	b["host"] = "nohost"
	if code, _ := r.do("POST", "/api/assist/allow", b); code != 404 {
		t.Fatalf("unknown host: %d", code)
	}
	if code, _ := r.do("POST", "/api/assist/allow", allowReq("other", "t1")); code != 400 {
		t.Fatalf("bad kind: %d", code)
	}
	if code, _ := r.do("POST", "/api/assist/allow", allowReq("session", "notaref")); code != 400 {
		t.Fatalf("bad ref: %d", code)
	}
	if code, _ := r.do("POST", "/api/assist/allow", allowReq("session", "_abcdef")); code != 404 {
		t.Fatalf("no live session: %d", code)
	}
}

func TestDenyAndNoSuch(t *testing.T) {
	r := newRig(t)
	r.liveTeam("t1", "alpha", "", "l1")
	r.do("POST", "/api/assist/allow", allowReq("team", "t1"))
	if code, b := r.do("POST", "/api/assist/deny", map[string]any{"kind": "team", "id": "alpha"}); code != 200 || b["result"] != "removed" {
		t.Fatalf("%d %v", code, b)
	}
	if code, b := r.do("POST", "/api/assist/deny", map[string]any{"kind": "team", "id": "alpha"}); code != 404 || b["error"] != "no_such" {
		t.Fatalf("%d %v", code, b)
	}
	// An entry of a team that has ended is removed by the id it was stored with.
	r.edit(func(f *gate.AssistFile) { f.Allowed = []gate.AssistAllow{{Kind: "team", ID: "gone"}} })
	if code, _ := r.do("POST", "/api/assist/deny", map[string]any{"kind": "team", "id": "gone"}); code != 200 {
		t.Fatalf("ended team: %d", code)
	}
}

func TestAllowAndDenyOfOneEntryAtOnceEndAsTheLockLeavesThemAndBothSayWhatTheyFound(t *testing.T) {
	for i := 0; i < 20; i++ {
		r := newRig(t)
		r.liveTeam("t1", "alpha", "", "l1")
		var wg sync.WaitGroup
		var aRes, dRes map[string]any
		wg.Add(2)
		go func() { defer wg.Done(); _, aRes = r.do("POST", "/api/assist/allow", allowReq("team", "t1")) }()
		go func() {
			defer wg.Done()
			_, dRes = r.do("POST", "/api/assist/deny", map[string]any{"kind": "team", "id": "t1"})
		}()
		wg.Wait()
		n := len(r.file().Allowed)
		switch {
		case aRes["result"] == "allowed" && dRes["error"] == "no_such" && n == 1: // deny first
		case aRes["result"] == "allowed" && dRes["result"] == "removed" && n == 0: // allow first
		default:
			t.Fatalf("allow %v, deny %v, %d left", aRes, dRes, n)
		}
	}
}

func TestTwoAllowsAtOnceBothLand(t *testing.T) {
	r := newRig(t)
	r.liveTeam("t1", "a", "", "l1")
	r.liveTeam("t2", "b", "", "l2")
	var wg sync.WaitGroup
	for _, id := range []string{"t1", "t2"} {
		wg.Add(1)
		go func() { defer wg.Done(); r.do("POST", "/api/assist/allow", allowReq("team", id)) }()
	}
	wg.Wait()
	if n := len(r.file().Allowed); n != 2 {
		t.Fatalf("%d entries", n)
	}
}

// A session allowed by ref stays allowed after a relay, through the lineage.
func TestSessionAllowedByRefSurvivesARelay(t *testing.T) {
	r := newRig(t)
	r.addHost("air26")
	r.live("sid1", "_aaaaaa", "mlab/x-1")
	if code, b := r.do("POST", "/api/assist/allow", allowReq("session", "_aaaaaa")); code != 200 {
		t.Fatalf("%d %v", code, b)
	}
	f := r.file()
	if got := r.m.entriesOf(f, "sid1"); len(got) != 1 || got[0].ID != "_aaaaaa" {
		t.Fatalf("before the relay: %v", got)
	}
	// the relay: sid1 is gone, sid2 (ref _bbbbbb) took over
	delete(r.origins.live, "sid1")
	r.live("sid2", "_bbbbbb", "mlab/x-2")
	r.teams.succ["_aaaaaa"] = []string{"sid2"}
	if got := r.m.entriesOf(f, "sid2"); len(got) != 1 || got[0].ID != "_aaaaaa" {
		t.Fatalf("after the relay: %v", got)
	}
	if got := r.m.entriesOf(f, "other"); len(got) != 0 {
		t.Fatalf("another session matched: %v", got)
	}
}

func TestEndedTeamMatchesNoSession(t *testing.T) {
	r := newRig(t)
	r.edit(func(f *gate.AssistFile) { f.Allowed = []gate.AssistAllow{{Kind: "team", ID: "gone"}} })
	r.seats.m["s1"] = team.Seat{Role: team.SeatLead, TeamID: "t-live"}
	if got := r.m.entriesOf(r.file(), "s1"); len(got) != 1 || got[0].ID != "t-live" || gate.AllowedFor(r.file(), "air26", got) {
		t.Fatalf("%v", got)
	}
}

func repoDir(t *testing.T, origin string) string {
	d := t.TempDir()
	run := func(args ...string) {
		c := exec.Command("git", append([]string{"-C", d}, args...)...)
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	run("init", "-q")
	if origin != "" {
		run("remote", "add", "origin", origin)
	}
	return d
}

func TestPollerRecordsChecksUnderTheGenAndReadinessFailures(t *testing.T) {
	r := newRig(t)
	r.addHost("air26")
	r.edit(func(f *gate.AssistFile) {
		h := f.Hosts["air26"]
		h.On = true
		f.Hosts["air26"] = h
		f.Allowed = []gate.AssistAllow{{Kind: "team", ID: "t1"}}
	})
	entries := []gate.AssistEntry{{Kind: "team", ID: "t1"}}
	usable := func() (bool, string) {
		return gate.Usable(r.file(), r.m.readyCopy(), r.clock.now(), "air26", entries, "purdex")
	}
	// good JSON
	r.script("assist-check", checkOut("on", map[string]bool{"purdex": true, "other": false}), 0)
	r.m.pass()
	if ok, why := usable(); !ok {
		t.Fatalf("good check: %s", why)
	}
	if ok, why := gate.Usable(r.file(), r.m.readyCopy(), r.clock.now(), "air26", entries, "other"); ok || !strings.Contains(why, "not cloned") {
		t.Fatalf("repo not ready: %v %q", ok, why)
	}
	if got := r.calls(); got[len(got)-1] != "assist-check --json host=air26" {
		t.Fatalf("%v", got)
	}
	// persisted for other readers
	if m, _ := gate.ReadReady(r.dir); m["air26"].Gen != r.file().Hosts["air26"].Gen {
		t.Fatalf("assist-ready.json: %+v", m)
	}
	// a check older than 90 s
	r.clock.add(91 * time.Second)
	if ok, _ := usable(); ok {
		t.Fatal("a 91 s old check counts")
	}
	r.clock.add(-91 * time.Second)
	// bad JSON, exit 2, timeout
	for name, set := range map[string]func(){
		"bad JSON": func() { r.script("assist-check", "not json", 0) },
		"exit 2":   func() { r.script("assist-check", "", 2) },
		"timeout": func() {
			r.script("assist-check", "", 0)
			r.m.timeouts.check = 200 * time.Millisecond
			r.flag("assist-check", "block", true)
		},
	} {
		set()
		r.m.pass()
		if ok, why := usable(); ok || !strings.HasPrefix(why, "check failed:") {
			t.Errorf("%s: %v %q", name, ok, why)
		}
		r.flag("assist-check", "block", false)
		r.m.timeouts.check = 5 * time.Second
	}
}

func TestPollerChecksOnlyHostsAskedOnBusyOrNotSayingOff(t *testing.T) {
	r := newRig(t)
	r.addHost("air26")
	r.script("assist-check", checkOut("off", nil), 0)
	r.m.pass() // never checked: once
	r.m.pass() // says off at the current gen: not again
	if n := r.count("assist-check"); n != 1 {
		t.Fatalf("an off host was checked %d times", n)
	}
	r.edit(func(f *gate.AssistFile) {
		h := f.Hosts["air26"]
		h.On = true
		h.Gen = f.TakeGen()
		f.Hosts["air26"] = h
	})
	r.m.pass() // asked on: every pass
	r.m.pass()
	if n := r.count("assist-check"); n != 3 {
		t.Fatalf("an on host was checked %d times in all", n)
	}
}

// A check begun for an older generation is recorded under it and does not count for the new one.
func TestStaleGenCheckDoesNotCountAfterASwitch(t *testing.T) {
	r := newRig(t)
	r.addHost("air26")
	r.liveTeam("t1", "alpha", "", "l1")
	r.do("POST", "/api/assist/allow", allowReq("team", "t1"))
	r.script("assist-check", checkOut("on", map[string]bool{"purdex": true}), 0)
	r.m.pass()
	old := r.m.readyCopy()["air26"].Gen
	if code, _ := r.do("POST", "/api/assist/switch", sw("air26", true)); code != 200 {
		t.Fatal(code)
	}
	entries := []gate.AssistEntry{{Kind: "team", ID: "t1"}}
	if ok, _ := gate.Usable(r.file(), r.m.readyCopy(), r.clock.now(), "air26", entries, "purdex"); ok || r.file().Hosts["air26"].Gen == old {
		t.Fatal("usable on the check of an older generation")
	}
	r.m.pass()
	if ok, why := gate.Usable(r.file(), r.m.readyCopy(), r.clock.now(), "air26", entries, "purdex"); !ok {
		t.Fatalf("after the new check: %s", why)
	}
}

func TestHostsRoute(t *testing.T) {
	r := newRig(t)
	r.addHost("air26")
	dir := repoDir(t, "git@github.com:wake/purdex.git")
	r.edit(func(f *gate.AssistFile) { h := f.Hosts["air26"]; h.On = true; f.Hosts["air26"] = h })
	r.script("assist-check", checkOut("on", map[string]bool{"purdex": true}), 0)
	r.m.pass()
	r.seats.m["s1"] = team.Seat{Role: team.SeatMember, TeamID: "t1"}
	get := func(session, cwd string) (int, map[string]any) {
		return r.do("GET", "/api/assist/hosts?session="+session+"&cwd="+cwd, nil)
	}
	row := func(b map[string]any) map[string]any { return b["hosts"].([]any)[0].(map[string]any) }
	_, b := get("s1", dir)
	if x := row(b); b["repo"] != "purdex" || x["host"] != "air26" || x["on"] != true || x["ready"] != true || x["allowed"] != false || x["usable"] != false || x["why"] != "not allowed" {
		t.Fatalf("not allowed: %v", b)
	}
	r.edit(func(f *gate.AssistFile) { f.Allowed = []gate.AssistAllow{{Kind: "team", ID: "t1"}} })
	_, b = get("s1", dir)
	if x := row(b); x["usable"] != true || x["allowed"] != true {
		t.Fatalf("allowed: %v", b)
	}
	_, b = get("nobody", dir)
	if x := row(b); x["usable"] != false || x["ready"] != true {
		t.Fatalf("another session: %v", b)
	}
	_, b = get("s1", t.TempDir())
	if x := row(b); x["usable"] != false || !strings.Contains(x["why"].(string), "not in a git repository") {
		t.Fatalf("outside a repo: %v", b)
	}
}

func TestStatusAndMalformedFileSendsNothing(t *testing.T) {
	r := newRig(t)
	r.addHost("air26")
	if code, b := r.do("GET", "/api/assist", nil); code != 200 || b["file"] == nil || b["assist_error"] != nil {
		t.Fatalf("%d %v", code, b)
	}
	os.WriteFile(filepath.Join(r.dir, "assist.json"), []byte("{broken"), 0o644)
	if _, b := r.do("GET", "/api/assist", nil); b["assist_error"] == nil {
		t.Fatalf("%v", b)
	}
	if code, _ := r.do("POST", "/api/assist/switch", sw("air26", true)); code == 200 {
		t.Fatal("switch on a broken file")
	}
	r.m.pass()
	if r.count("assist-") != 0 {
		t.Fatalf("ran %v", r.calls())
	}
}

func TestNewerVersionRefusesEdits(t *testing.T) {
	r := newRig(t)
	os.WriteFile(filepath.Join(r.dir, "assist.json"), []byte(`{"Version":99,"Hosts":{"air26":{"SSH":"air26","Scripts":"/x"}}}`), 0o644)
	if code, b := r.do("POST", "/api/assist/switch", sw("air26", true)); code != 409 || b["error"] != "assist_version" {
		t.Fatalf("%d %v", code, b)
	}
	if code, b := r.do("POST", "/api/assist/host", map[string]any{"alias": "air26", "slots": 2}); code != 409 || b["error"] != "assist_version" {
		t.Fatalf("%d %v", code, b)
	}
}
