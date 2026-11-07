package teammod

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/module/hostconfig"
	"github.com/wake/purdex/internal/team"
)

// fakeCompaction is the host config's compaction row of these tests.
type fakeCompaction struct {
	d   hostconfig.CompactionDefaults
	err error
}

func (f *fakeCompaction) Compaction() (hostconfig.CompactionDefaults, error) { return f.d, f.err }

func (f *fixture) compactionGet(sid string) (int, team.CompactionView, []byte) {
	f.t.Helper()
	var v team.CompactionView
	code, raw := f.do(http.MethodGet, "/api/team/compaction/"+sid, nil)
	if code == http.StatusOK {
		if err := json.Unmarshal(raw, &v); err != nil {
			f.t.Fatalf("decode %s: %v", raw, err)
		}
	}
	return code, v, raw
}

func (f *fixture) view(sid string) team.CompactionView {
	f.t.Helper()
	v, err := f.m.compactionView(sid)
	if err != nil {
		f.t.Fatal(err)
	}
	return v
}

func TestCompactionView_Table(t *testing.T) {
	custom := hostconfig.CompactionDefaults{
		Solo: hostconfig.CompactionSet{Auto: false, Threshold: 30, Idle: false},
		Team: hostconfig.CompactionSet{Auto: true, Threshold: 80, Idle: false},
	}
	overrides := map[string]team.CompactionOverride{
		"none":      {},
		"threshold": {Threshold: ptr(55)},
		"all":       {Auto: ptr(true), Threshold: ptr(10), Idle: ptr(true)},
	}
	for _, role := range []string{"none", "lead", "member"} {
		for oname, o := range overrides {
			t.Run(role+"/"+oname, func(t *testing.T) {
				f := newFixture(t)
				f.compact.d = custom
				f.memberAuto(false) // a member is managed only while member_auto is off
				sid := "sid-x"
				switch role {
				case "lead":
					seedTeam(t, f.m.store, uid(9), sid, f.clock.Load())
				case "member":
					f.makeMember(sid)
				}
				f.switches.set(hostconfig.RelaySwitches{}) // self relay off: managed
				if err := f.m.store.SetCompactionOverride(sid, o, 1); err != nil {
					t.Fatal(err)
				}
				want := custom.Solo
				if role != "none" {
					want = custom.Team
				}
				if o.Auto != nil {
					want.Auto = *o.Auto
				}
				if o.Threshold != nil {
					want.Threshold = *o.Threshold
				}
				if o.Idle != nil {
					want.Idle = *o.Idle
				}
				v := f.view(sid)
				e := v.Effective
				if v.Role != role || !e.Managed || e.Reason != "" || e.Auto != want.Auto || e.Threshold != want.Threshold || e.Idle != want.Idle || !reflect.DeepEqual(v.Override, o) {
					t.Fatalf("got %+v, want role %s set %+v override %+v", v, role, want, o)
				}
			})
		}
	}
}

func TestCompactionView_SelfRelayOnIsNotManaged(t *testing.T) {
	f := newFixture(t) // default switches: self relay on for a solo session
	v := f.view("sid-x")
	if v.Effective.Managed || v.Effective.Reason != team.CompactionReasonSelfRelay || v.Effective.Threshold != 70 || !v.Effective.Auto {
		t.Fatalf("got %+v", v)
	}
}

func TestCompactionView_SelfRelayPausedIsManaged(t *testing.T) {
	f := newFixture(t)
	if err := f.m.store.SetSelfRelayPaused("sid-x", true, 1); err != nil {
		t.Fatal(err)
	}
	if v := f.view("sid-x"); !v.Effective.Managed {
		t.Fatalf("got %+v", v)
	}
}

func TestCompactionView_MemberAutoOnIsNotManaged(t *testing.T) {
	f := newFixture(t)
	f.makeMember("sid-m")
	v := f.view("sid-m")
	if v.Effective.Managed || v.Effective.Reason != team.CompactionReasonMemberAuto || v.Role != "member" {
		t.Fatalf("got %+v", v)
	}
}

func TestCompactionView_MemberAutoOffUsesTheTeamSet(t *testing.T) {
	f := newFixture(t)
	f.compact.d.Team.Threshold = 42
	f.makeMember("sid-m")
	f.memberAuto(false)
	if v := f.view("sid-m"); !v.Effective.Managed || v.Effective.Threshold != 42 {
		t.Fatalf("got %+v", v)
	}
}

func TestCompactionView_HostConfigUnreadableUsesTheDefaults(t *testing.T) {
	f := newFixture(t)
	f.switches.set(hostconfig.RelaySwitches{})
	f.compact.d = hostconfig.CompactionDefaults{}
	f.compact.err = errors.New("row broken")
	e := f.view("sid-x").Effective
	if !e.Managed || !e.Auto || e.Threshold != 70 || !e.Idle {
		t.Fatalf("got %+v", e)
	}
}

func TestCompactionGet_RelayStateErrorIs500(t *testing.T) {
	f := newFixture(t)
	f.switches.mu.Lock()
	f.switches.err = errors.New("store down")
	f.switches.mu.Unlock()
	if code, _, raw := f.compactionGet("sid-x"); code != http.StatusInternalServerError {
		t.Fatalf("%d %s", code, raw)
	}
}

func TestCompactionPut_ReplacesTheWholeOverride(t *testing.T) {
	f := newFixture(t)
	if code, raw := f.do(http.MethodPut, "/api/team/compaction/sid-x", `{"auto":false}`); code != http.StatusOK {
		t.Fatalf("%d %s", code, raw)
	}
	code, raw := f.do(http.MethodPut, "/api/team/compaction/sid-x", `{"threshold":50}`)
	if code != http.StatusOK {
		t.Fatalf("%d %s", code, raw)
	}
	var v team.CompactionView
	_ = json.Unmarshal(raw, &v)
	if v.Override.Auto != nil || v.Override.Idle != nil || v.Override.Threshold == nil || *v.Override.Threshold != 50 {
		t.Fatalf("response %+v", v.Override)
	}
	if o, _ := f.m.store.CompactionOverride("sid-x"); o.Auto != nil || o.Threshold == nil || *o.Threshold != 50 {
		t.Fatalf("stored %+v", o)
	}
}

func TestCompactionPut_AllNullClears(t *testing.T) {
	f := newFixture(t)
	_ = f.m.store.SetCompactionOverride("sid-x", team.CompactionOverride{Auto: ptr(false)}, 1)
	if code, raw := f.do(http.MethodPut, "/api/team/compaction/sid-x", `{"auto":null,"threshold":null}`); code != http.StatusOK {
		t.Fatalf("%d %s", code, raw)
	}
	if o, _ := f.m.store.CompactionOverride("sid-x"); o != (team.CompactionOverride{}) {
		t.Fatalf("stored %+v", o)
	}
}

func TestCompactionPut_Rejects(t *testing.T) {
	for name, body := range map[string]string{
		"unknown key":   `{"mode":"dev"}`,
		"zero":          `{"threshold":0}`,
		"over 100":      `{"threshold":101}`,
		"fraction":      `{"threshold":50.5}`,
		"string":        `{"threshold":"50"}`,
		"auto not bool": `{"auto":"yes"}`,
		"not an object": `[1]`,
		"bad json":      `{`,
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			if code, raw := f.do(http.MethodPut, "/api/team/compaction/sid-x", body); code != http.StatusBadRequest {
				t.Fatalf("%d %s", code, raw)
			}
			if o, _ := f.m.store.CompactionOverride("sid-x"); o != (team.CompactionOverride{}) {
				t.Fatalf("a refused PUT stored %+v", o)
			}
		})
	}
}

func TestCheckCompactionOverride(t *testing.T) {
	for _, n := range []int{1, 100} {
		if err := checkCompactionOverride(team.CompactionOverride{Threshold: ptr(n)}); err != nil {
			t.Fatalf("%d: %v", n, err)
		}
	}
	for _, n := range []int{0, 101, -1} {
		if checkCompactionOverride(team.CompactionOverride{Threshold: ptr(n)}) == nil {
			t.Fatalf("%d accepted", n)
		}
	}
	if err := checkCompactionOverride(team.CompactionOverride{}); err != nil {
		t.Fatal(err)
	}
}

// The real module in a real core: team.compaction.v1 is on GET /api/info (the lesson of fleet.v1).
func TestInfo_AnnouncesCompactionForTheRealModule(t *testing.T) {
	c := core.New(core.CoreDeps{Config: &config.Config{HostID: "h:1", DataDir: t.TempDir()}, Registry: core.NewServiceRegistry()})
	mux := http.NewServeMux()
	c.RegisterCoreRoutes(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/info", nil))
	var body map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	found := false
	list, _ := body["capabilities"].([]any)
	for _, c := range list {
		found = found || c == team.CapabilityCompaction
	}
	if !found {
		t.Fatalf("capabilities %v lack %s", list, team.CapabilityCompaction)
	}
}
