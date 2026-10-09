// internal/module/team/proxy_handler_test.go
package teammod

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/middleware"
	"github.com/wake/purdex/internal/team"
)

// The proxy adapter on the lead host (cross-host team spec §7, plan X6-1). sid-1 leads team uid(1); the member host air26
// (host id hostM) holds the remote member mk1 (spawn_op abc12, session sid-abc12). The point of these tests is who the
// proxy may be made to act as.

func proxyFixture(t *testing.T) (*fixture, *middleware.Principal) {
	t.Helper()
	f, _ := remoteFixture(t)
	f.core.CfgMu.Lock()
	f.core.Cfg.Peers.Hosts = []config.PeerHost{
		{Alias: "air26", URL: "https://air26.example", HostID: "hostM", InboundToken: "i1"},
		{Alias: "other", URL: "https://other.example", HostID: "hostN", InboundToken: "i2"},
	}
	f.core.CfgMu.Unlock()
	f.remoteRow("abc12", "hostM", "mk1", rowActive)
	return f, &middleware.Principal{Kind: middleware.PrincipalHost, Alias: "air26", HostID: "hostM"}
}

func (f *fixture) postProxy(p *middleware.Principal, req team.ProxyRequest) (int, team.ProxyAnswer, []byte) {
	f.t.Helper()
	code, body := f.postProxyRaw(p, req)
	var ans team.ProxyAnswer
	_ = json.Unmarshal(body, &ans)
	return code, ans, body
}

func (f *fixture) postProxyRaw(p *middleware.Principal, body any) (int, []byte) {
	f.t.Helper()
	raw, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, team.ProxyRoute, strings.NewReader(string(raw)))
	if p != nil {
		req = req.WithContext(middleware.WithPrincipal(req.Context(), *p))
	}
	rec := newCapture()
	f.mux.ServeHTTP(rec, req)
	return rec.status, rec.body
}

func proxyReq(mk, method, path string, body any) team.ProxyRequest {
	r := team.ProxyRequest{ToHostID: "h:1", MK: mk, Method: method, Path: path}
	if body != nil {
		r.Body, _ = json.Marshal(body)
	}
	return r
}

// ownTask makes a task of team uid(1) owned by the member with spawn_op key and returns its display id.
func (f *fixture) ownTask(key, subject string) string {
	f.t.Helper()
	at := f.clock.Load()
	row, err := f.m.store.CreateTask(TaskRow{TeamID: uid(1), Subject: subject, OwnerKey: key, CreatedByRef: "_abc123", CreatedAt: at, UpdatedAt: at})
	if err != nil {
		f.t.Fatal(err)
	}
	return team.TaskDisplayID(uid(1), row.Seq)
}

func TestProxy_AReportIsPostedAsTheBoundMember(t *testing.T) {
	f, p := proxyFixture(t)
	id := f.ownTask("abc12", "ios")
	code, ans, raw := f.postProxy(p, proxyReq("mk1", http.MethodPost, "/api/team/reports", map[string]any{"id": reportID(1), "task": id, "kind": "ack", "summary": "on it"}))
	if code != http.StatusOK || ans.HostID != "h:1" || ans.Status != http.StatusCreated {
		t.Fatalf("proxy = %d %s", code, raw)
	}
	var out team.ReportResponse
	if err := json.Unmarshal(ans.Body, &out); err != nil || out.Report.Task != id || out.Report.Member.Ref != "_rabc12" {
		t.Fatalf("report = %+v (%v)", out, err)
	}
	rows, err := f.m.store.ListReports(uid(1), 0, 0, 0)
	if err != nil || len(rows) != 1 || rows[0].MemberKey != "abc12" {
		t.Fatalf("stored = %+v err=%v", rows, err)
	}
}

func TestProxy_TasksMineIsTheMembersOwnOnly(t *testing.T) {
	f, p := proxyFixture(t)
	f.remoteRow("def34", "hostM", "mk2", rowActive)
	mine := f.ownTask("abc12", "mine")
	f.ownTask("def34", "another member's")
	code, ans, raw := f.postProxy(p, proxyReq("mk1", http.MethodGet, "/api/team/tasks?mine=1", nil))
	var list team.TaskList
	if code != http.StatusOK || ans.Status != http.StatusOK || json.Unmarshal(ans.Body, &list) != nil || len(list.Tasks) != 1 || list.Tasks[0].ID != mine {
		t.Fatalf("list = %d %s", code, raw)
	}
}

func TestProxy_StatusOfOwnTaskYesOfAnothersNo(t *testing.T) {
	f, p := proxyFixture(t)
	f.remoteRow("def34", "hostM", "mk2", rowActive)
	own, other := f.ownTask("abc12", "own"), f.ownTask("def34", "other")
	_, ans, raw := f.postProxy(p, proxyReq("mk1", http.MethodPost, "/api/team/tasks/"+own+"/status", map[string]any{"status": "in_progress"}))
	if ans.Status != http.StatusOK {
		t.Fatalf("own = %s", raw)
	}
	_, ans, raw = f.postProxy(p, proxyReq("mk1", http.MethodPost, "/api/team/tasks/"+other+"/status", map[string]any{"status": "in_progress"}))
	var e team.APIError
	_ = json.Unmarshal(ans.Body, &e)
	if ans.Status != http.StatusConflict || e.Error != team.ErrTaskNotFound {
		t.Fatalf("another's = %s", raw)
	}
	row, _, _ := f.m.store.GetTask(uid(1), mustSeq(t, other))
	if row.Status != team.TaskPending {
		t.Fatalf("another member's task moved to %s", row.Status)
	}
}

func mustSeq(t *testing.T, id string) int {
	t.Helper()
	seq, ok := team.ParseTaskID(id, uid(1))
	if !ok {
		t.Fatalf("task id %q", id)
	}
	return seq
}

// Who the proxy may act as is decided by {host id of the caller, mk} and nothing else: another host's member, a LOCAL
// member and the lead itself are all "not a member" for it, and nothing is written.
func TestProxy_CannotActAsAnotherMemberOrTheLead(t *testing.T) {
	f, p := proxyFixture(t)
	f.remoteRow("zzz99", "hostN", "mkN", rowActive) // a member of ANOTHER paired host
	seedMember(t, f.m.store, "op-local", uid(1), "sid-local", f.clock.Load())
	id := f.ownTask("zzz99", "belongs to hostN's member")
	report := map[string]any{"id": reportID(2), "task": id, "kind": "ack", "summary": "x"}
	for name, mk := range map[string]string{"another host's mk": "mkN", "a local member's spawn op": "op-local", "the lead's session id": "sid-1", "unknown": "nope"} {
		code, ans, raw := f.postProxy(p, proxyReq(mk, http.MethodPost, "/api/team/reports", report))
		var e team.APIError
		_ = json.Unmarshal(ans.Body, &e)
		if code != http.StatusOK || ans.Status != http.StatusConflict || e.Error != team.ErrNotMember {
			t.Fatalf("%s = %d %s", name, code, raw)
		}
	}
	if rows, _ := f.m.store.ListReports(uid(1), 0, 0, 0); len(rows) != 0 {
		t.Fatalf("a report was written: %+v", rows)
	}
}

// Any origin_inbox — in the body (whatever its case), the query or the path — is refused before anything runs: a paired host
// must not be able to name the lead's inbox.
func TestProxy_OriginInboxIsRefusedEverywhere(t *testing.T) {
	f, p := proxyFixture(t)
	id := f.ownTask("abc12", "ios")
	leadInbox := "/tmp/10.sock"
	for name, req := range map[string]team.ProxyRequest{
		"body":        proxyReq("mk1", http.MethodPost, "/api/team/reports", map[string]any{"origin_inbox": leadInbox, "id": reportID(3), "task": id, "kind": "ack", "summary": "x"}),
		"body, case":  proxyReq("mk1", http.MethodPost, "/api/team/reports", map[string]any{"Origin_Inbox": leadInbox, "id": reportID(4), "task": id, "kind": "ack", "summary": "x"}),
		"query":       proxyReq("mk1", http.MethodGet, "/api/team/tasks?mine=1&origin_inbox="+leadInbox, nil),
		"query, case": proxyReq("mk1", http.MethodGet, "/api/team/tasks?mine=1&ORIGIN_INBOX="+leadInbox, nil),
		"status body": proxyReq("mk1", http.MethodPost, "/api/team/tasks/"+id+"/status", map[string]any{"origin_inbox": leadInbox, "status": "in_progress"}),
	} {
		code, body := f.postProxyRaw(p, req)
		if code != http.StatusBadRequest || !strings.Contains(string(body), team.ErrProxyOriginInbox) {
			t.Fatalf("%s = %d %s, want 400 origin_inbox_forbidden", name, code, body)
		}
	}
	if rows, _ := f.m.store.ListReports(uid(1), 0, 0, 0); len(rows) != 0 {
		t.Fatal("a report was written")
	}
}

// Only the allow-list is served: no lead operation is reachable through the proxy.
func TestProxy_OnlyTheAllowListIsServed(t *testing.T) {
	f, p := proxyFixture(t)
	id := f.ownTask("abc12", "ios")
	for _, c := range []struct{ method, path string }{
		{http.MethodPost, "/api/team/tasks"}, {http.MethodGet, "/api/team/reports"}, {http.MethodGet, "/api/team"},
		{http.MethodPost, "/api/team/kill"}, {http.MethodPost, "/api/team/release"}, {http.MethodPost, "/api/team/spawns"},
		{http.MethodPost, "/api/team/tasks/" + id + "/reassign"}, {http.MethodPost, "/api/team/approvals"},
		{http.MethodGet, "/api/team/tasks/" + id}, {http.MethodPost, "/api/team/tasks"}, {http.MethodGet, "/api/team/reports?task=" + id},
		{http.MethodDelete, "/api/team/tasks"}, {http.MethodPost, "/api/team/tasks/" + id + "/status/x"},
		{http.MethodGet, "http://evil.example/api/team/tasks"}, {http.MethodGet, "/api/team/tasks#frag"}, {http.MethodGet, "api/team/tasks"},
	} {
		code, body := f.postProxyRaw(p, proxyReq("mk1", c.method, c.path, nil))
		if code != http.StatusForbidden && code != http.StatusBadRequest {
			t.Fatalf("%s %s = %d %s, want a refusal of the route", c.method, c.path, code, body)
		}
	}
}

// The binding is the route's first act: an admin, no principal, an unverified host, the wrong host id.
func TestProxy_BindingComesFirst(t *testing.T) {
	f, p := proxyFixture(t)
	req := proxyReq("mk1", http.MethodGet, "/api/team/tasks?mine=1", nil)
	if code, body := f.postProxyRaw(&middleware.Principal{Kind: middleware.PrincipalAdmin}, req); code != http.StatusForbidden {
		t.Fatalf("admin = %d %s", code, body)
	}
	if code, body := f.postProxyRaw(nil, req); code != http.StatusForbidden {
		t.Fatalf("no principal = %d %s", code, body)
	}
	stale := *p
	stale.HostID = "someone-else"
	if code, body := f.postProxyRaw(&stale, req); code != http.StatusForbidden {
		t.Fatalf("stale host id = %d %s", code, body)
	}
	wrong := req
	wrong.ToHostID = "not-us"
	if code, body := f.postProxyRaw(p, wrong); code != http.StatusConflict || !strings.Contains(string(body), team.ErrCommandWrongHost) {
		t.Fatalf("wrong to_host_id = %d %s", code, body)
	}
}

// A member that left (released) or whose team ended is no member for the proxy either — in the cores' own transaction too.
func TestProxy_ReleasedMemberAndEndedTeamAreNotMembers(t *testing.T) {
	f, p := proxyFixture(t)
	req := proxyReq("mk1", http.MethodGet, "/api/team/tasks?mine=1", nil)
	if ok, err := f.m.store.db.Exec(`UPDATE team_members SET state = 'released' WHERE spawn_op = 'abc12'`); err != nil || ok == nil {
		t.Fatal(err)
	}
	_, ans, _ := f.postProxy(p, req)
	var e team.APIError
	_ = json.Unmarshal(ans.Body, &e)
	if ans.Status != http.StatusConflict || e.Error != team.ErrNotMember {
		t.Fatalf("released = %d %+v", ans.Status, e)
	}
	if _, err := f.m.store.db.Exec(`UPDATE team_members SET state = 'active' WHERE spawn_op = 'abc12'`); err != nil {
		t.Fatal(err)
	}
	if ended, err := f.m.store.EndTeam(uid(1), "sid-1", team.TeamEndLeadGone, f.clock.Load()); err != nil || !ended {
		t.Fatalf("end team: %v %v", ended, err)
	}
	e = team.APIError{}
	_, ans, _ = f.postProxy(p, req)
	_ = json.Unmarshal(ans.Body, &e)
	if ans.Status != http.StatusConflict || e.Error != team.ErrNotMember {
		t.Fatalf("ended team = %d %+v", ans.Status, e)
	}
}
