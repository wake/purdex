// internal/module/team/commands_handler_test.go
package teammod

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/middleware"
	peersmod "github.com/wake/purdex/internal/module/peers"
	"github.com/wake/purdex/internal/team"
)

const (
	cmdRoute  = "/api/peers/team/commands"
	cmdUUID1  = "11111111-1111-4111-8111-111111111111"
	cmdUUID2  = "22222222-2222-4222-8222-222222222222"
	cmdUUID3  = "33333333-3333-4333-8333-333333333333"
	cmdUUID4  = "44444444-4444-4444-8444-444444444444"
	cmdBadUID = "not-a-uuid"
)

// setLeadHost puts the paired lead host's entry in the live config; allow is its AllowTeam.
func (f *fixture) setLeadHost(allow bool) {
	f.t.Helper()
	f.core.CfgMu.Lock()
	defer f.core.CfgMu.Unlock()
	f.core.Cfg.Peers.Hosts = []config.PeerHost{{Alias: "lead", URL: "https://lead.example", HostID: "lead:1", InboundToken: "i", AllowTeam: allow}}
}

func leadPrincipal() *middleware.Principal {
	return &middleware.Principal{Kind: middleware.PrincipalHost, Alias: "lead", HostID: "lead:1"}
}

// postCmd sends one command as principal p (nil: none) and returns status and body.
func (f *fixture) postCmd(p *middleware.Principal, body any) (int, []byte) {
	f.t.Helper()
	var rd *bytes.Reader
	if s, ok := body.(string); ok {
		rd = bytes.NewReader([]byte(s))
	} else {
		raw, err := json.Marshal(body)
		if err != nil {
			f.t.Fatal(err)
		}
		rd = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(http.MethodPost, cmdRoute, rd)
	if p != nil {
		req = req.WithContext(middleware.WithPrincipal(req.Context(), *p))
	}
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, req)
	return rec.Code, rec.Body.Bytes()
}

func wireAdopt(id, mk, target string) team.TeamCommand {
	c := adoptCmd(id, mk, target)
	c.ToHostID = "h:1"
	return c
}

func errCode(t *testing.T, body []byte) string {
	t.Helper()
	var e team.CommandRefusal
	if err := json.Unmarshal(body, &e); err != nil {
		t.Fatalf("body %q: %v", body, err)
	}
	return e.Error
}

func logCount(t *testing.T, f *fixture) int {
	t.Helper()
	var n int
	if err := f.m.store.db.QueryRow(`SELECT COUNT(*) FROM team_command_log`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestCommands_BindingRefusals(t *testing.T) {
	f := newFixture(t)
	f.setLeadHost(true)
	good := wireAdopt(cmdUUID1, cmdUUID1, "sid-t")
	for name, tc := range map[string]struct {
		p      *middleware.Principal
		status int
		code   string
	}{
		"no principal":              {nil, 403, "host_unverified"},
		"admin":                     {&middleware.Principal{Kind: middleware.PrincipalAdmin}, 403, "admin_not_allowed"},
		"unverified host":           {&middleware.Principal{Kind: middleware.PrincipalHost, Alias: "lead"}, 403, "host_unverified"},
		"entry re-created":          {&middleware.Principal{Kind: middleware.PrincipalHost, Alias: "lead", HostID: "other:9"}, 403, "host_unverified"},
		"alias no longer in config": {&middleware.Principal{Kind: middleware.PrincipalHost, Alias: "gone", HostID: "gone:1"}, 403, "host_unverified"},
	} {
		code, body := f.postCmd(tc.p, good)
		if code != tc.status || errCode(t, body) != tc.code {
			t.Fatalf("%s: %d %s, want %d %s", name, code, body, tc.status, tc.code)
		}
	}
	if logCount(t, f) != 0 {
		t.Fatal("a refused binding was logged")
	}
}

func TestCommands_WrongHostIsNotStored(t *testing.T) {
	f := newFixture(t)
	f.setLeadHost(true)
	c := wireAdopt(cmdUUID1, cmdUUID1, "sid-t")
	c.ToHostID = "somebody-else"
	code, body := f.postCmd(leadPrincipal(), c)
	if code != http.StatusConflict || errCode(t, body) != team.ErrCommandWrongHost {
		t.Fatalf("%d %s", code, body)
	}
	if logCount(t, f) != 0 {
		t.Fatal("wrong_host was logged")
	}
}

func TestCommands_UnsupportedKindsAreRefusedNotStored(t *testing.T) {
	f := newFixture(t)
	f.setLeadHost(true)
	for _, kind := range []string{"spawn", "kill", "void", "bogus"} {
		c := wireAdopt(cmdUUID1, cmdUUID1, "sid-t")
		c.Kind = kind
		code, body := f.postCmd(leadPrincipal(), c)
		if code != http.StatusBadRequest || errCode(t, body) != team.ErrCommandUnsupportedKind {
			t.Fatalf("%s: %d %s", kind, code, body)
		}
	}
	if logCount(t, f) != 0 {
		t.Fatal("an unsupported kind was logged")
	}
}

func TestCommands_ValidationIs400(t *testing.T) {
	f := newFixture(t)
	f.setLeadHost(true)
	mut := func(fn func(*team.TeamCommand)) team.TeamCommand {
		c := wireAdopt(cmdUUID1, cmdUUID1, "sid-t")
		fn(&c)
		return c
	}
	for name, c := range map[string]team.TeamCommand{
		"id not a uuid":      mut(func(c *team.TeamCommand) { c.ID = cmdBadUID }),
		"no team":            mut(func(c *team.TeamCommand) { c.TeamID = "" }),
		"adopt without mk":   mut(func(c *team.TeamCommand) { c.MK = "" }),
		"adopt no target":    mut(func(c *team.TeamCommand) { c.TargetSessionID = "" }),
		"adopt no lead addr": mut(func(c *team.TeamCommand) { c.Lead.Address = "" }),
		"control char":       mut(func(c *team.TeamCommand) { c.Lead.Title = "a\x07b" }),
		"too long":           mut(func(c *team.TeamCommand) { c.TeamName = strings.Repeat("x", 300) }),
		"release without mk": mut(func(c *team.TeamCommand) { c.Kind, c.MK = team.CommandRelease, "" }),
		"lead_moved no new lead": mut(func(c *team.TeamCommand) {
			c.Kind, c.MK, c.LeadSessionID, c.LeadRef = team.CommandLeadMoved, "", "", ""
		}),
	} {
		code, body := f.postCmd(leadPrincipal(), c)
		if code != http.StatusBadRequest || errCode(t, body) != team.ErrCommandBadRequest {
			t.Fatalf("%s: %d %s", name, code, body)
		}
	}
	if code, body := f.postCmd(leadPrincipal(), "{not json"); code != http.StatusBadRequest || errCode(t, body) != team.ErrCommandBadRequest {
		t.Fatalf("bad json: %d %s", code, body)
	}
	if logCount(t, f) != 0 {
		t.Fatal("a bad request was logged")
	}
}

// §3.1 rule 8: the rate limit is spent before the body is decoded, the body is capped at 64 KiB.
func TestCommands_RateLimitBeforeDecodeAndBodyCap(t *testing.T) {
	f := newFixture(t)
	f.setLeadHost(true)
	f.m.cmdLimit = peersmod.NewHostLimiter(1, time.Minute, time.Now)
	if code, _ := f.postCmd(leadPrincipal(), wireAdopt(cmdUUID1, cmdUUID1, "sid-t")); code == http.StatusTooManyRequests {
		t.Fatal("the first request was limited")
	}
	code, body := f.postCmd(leadPrincipal(), "{garbage that would be a 400 if decoded")
	if code != http.StatusTooManyRequests {
		t.Fatalf("second request: %d %s, want 429 before decode", code, body)
	}

	f.m.cmdLimit = peersmod.NewHostLimiter(100, time.Minute, time.Now)
	big := `{"id":"` + strings.Repeat("a", 70<<10) + `"}`
	if code, body := f.postCmd(leadPrincipal(), big); code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized body: %d %s", code, body)
	}
}

func TestCommands_AdoptEndToEndAndReplay(t *testing.T) {
	f := newFixture(t)
	f.setLeadHost(true)
	f.origins.show(team.Origin{SessionID: "sid-t", Ref: "_tgt001", PID: 42, ProcStart: "ps2", Cwd: "/w", Title: "m"})
	cmd := wireAdopt(cmdUUID1, cmdUUID1, "sid-t")

	code, body := f.postCmd(leadPrincipal(), cmd)
	var ans team.TeamCommandAnswer
	if err := json.Unmarshal(body, &ans); err != nil || code != http.StatusOK || ans.ID != cmdUUID1 || ans.HostID != "h:1" {
		t.Fatalf("%d %s (%v)", code, body, err)
	}
	var out team.AdoptOutcome
	if err := json.Unmarshal(ans.Outcome, &out); err != nil || out.State != "applied" || out.MemberSession != "sid-t" {
		t.Fatalf("outcome = %s", ans.Outcome)
	}
	if role, _ := f.m.store.SessionRole("sid-t"); role != sessionRoleMemberRemote {
		t.Fatalf("role = %s", role)
	}

	code2, body2 := f.postCmd(leadPrincipal(), cmd)
	if code2 != code || string(body2) != string(body) {
		t.Fatalf("replay: %d %s, first %d %s", code2, body2, code, body)
	}
	changed := cmd
	changed.TeamName = "renamed"
	if code, body := f.postCmd(leadPrincipal(), changed); code != http.StatusConflict || errCode(t, body) != team.ErrCommandIDConflict {
		t.Fatalf("same id other content: %d %s", code, body)
	}
}

func TestCommands_AdoptRefusals(t *testing.T) {
	f := newFixture(t)
	f.setLeadHost(false)
	f.origins.show(team.Origin{SessionID: "sid-t", Ref: "_tgt001", PID: 42, ProcStart: "ps2", Cwd: "/w"})
	if code, body := f.postCmd(leadPrincipal(), wireAdopt(cmdUUID1, cmdUUID1, "sid-t")); code != http.StatusForbidden || errCode(t, body) != team.ErrCommandHostNotAllowed {
		t.Fatalf("no consent: %d %s", code, body)
	}
	f.setLeadHost(true)
	// A session the registry does not show, and one shown under another ref, are both "not found".
	if code, body := f.postCmd(leadPrincipal(), wireAdopt(cmdUUID2, cmdUUID2, "sid-missing")); code != http.StatusConflict || errCode(t, body) != team.ErrAdoptTargetNotFound {
		t.Fatalf("missing: %d %s", code, body)
	}
	wrongRef := wireAdopt(cmdUUID3, cmdUUID3, "sid-t")
	wrongRef.TargetRef = "_other9"
	if code, body := f.postCmd(leadPrincipal(), wrongRef); code != http.StatusConflict || errCode(t, body) != team.ErrAdoptTargetNotFound {
		t.Fatalf("wrong ref: %d %s", code, body)
	}
	if role, _ := f.m.store.SessionRole("sid-t"); role != sessionRoleNone {
		t.Fatalf("a refused adopt gave role %s", role)
	}
}

func TestCommands_ReleaseEndLeadMovedOverHTTP(t *testing.T) {
	f := newFixture(t)
	f.setLeadHost(true)
	f.origins.show(team.Origin{SessionID: "sid-t", Ref: "_tgt001", PID: 42, ProcStart: "ps2", Cwd: "/w"})
	if code, body := f.postCmd(leadPrincipal(), wireAdopt(cmdUUID1, cmdUUID1, "sid-t")); code != http.StatusOK {
		t.Fatalf("adopt: %d %s", code, body)
	}
	mv := relCmd(cmdUUID2, team.CommandLeadMoved, "")
	mv.ToHostID, mv.LeadSessionID, mv.LeadRef = "h:1", "lead-new", "_lead02"
	if code, body := f.postCmd(leadPrincipal(), mv); code != http.StatusOK {
		t.Fatalf("lead_moved: %d %s", code, body)
	}
	if row, _, _ := f.m.store.RemoteMember(cmdUUID1); row.LeadSessionID != "lead-new" || row.State != remoteActive {
		t.Fatalf("row = %+v", row)
	}
	rel := relCmd(cmdUUID3, team.CommandRelease, cmdUUID1)
	rel.ToHostID = "h:1"
	if code, body := f.postCmd(leadPrincipal(), rel); code != http.StatusOK {
		t.Fatalf("release: %d %s", code, body)
	}
	end := relCmd(cmdUUID4, team.CommandEnd, "")
	end.ToHostID = "h:1"
	if code, body := f.postCmd(leadPrincipal(), end); code != http.StatusOK {
		t.Fatalf("end: %d %s", code, body)
	}
	if row, _, _ := f.m.store.RemoteMember(cmdUUID1); row.State != remoteReleased {
		t.Fatalf("row = %+v", row)
	}
}
