// internal/module/team/proxy_forward_test.go
package teammod

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	peersmod "github.com/wake/purdex/internal/module/peers"
	"github.com/wake/purdex/internal/team"
)

// The member host's side of the report / task proxy (cross-host team spec §7, plan X6-2). sid-2 (/tmp/20.sock, ref
// _def456) is an active REMOTE member of this host under the member key mk-1, led from host-L.

const memberInbox = "/tmp/20.sock"

func forwardFixture(t *testing.T, answer func(body map[string]any) (int, any)) (*fixture, *fakeHostCaller) {
	t.Helper()
	f := newFixture(t)
	fc := &fakeHostCaller{}
	f.m.cmdCaller = fc
	if answer != nil {
		fc.script = func(host string, body map[string]any) peersmod.CallResult {
			status, v := answer(body)
			inner, _ := json.Marshal(v)
			raw, _ := json.Marshal(team.ProxyAnswer{HostID: host, Status: status, Body: inner})
			return peersmod.CallResult{Class: peersmod.ClassDone, Status: 200, Body: raw}
		}
	}
	if err := f.m.store.InsertRemoteMember(newRemote("mk-1", "sid-2", "host-L", f.clock.Load())); err != nil {
		t.Fatal(err)
	}
	return f, fc
}

func lastProxyCall(t *testing.T, fc *fakeHostCaller) (team.ProxyRequest, map[string]any) {
	t.Helper()
	calls := fc.sent()
	if len(calls) == 0 {
		t.Fatal("nothing was forwarded")
	}
	c := calls[len(calls)-1]
	if c.Host != "host-L" || c.Path != team.ProxyRoute {
		t.Fatalf("call = %s %s", c.Host, c.Path)
	}
	var req team.ProxyRequest
	var generic map[string]any
	if err := json.Unmarshal(c.Body, &req); err != nil || json.Unmarshal(c.Body, &generic) != nil {
		t.Fatalf("body %s: %v", c.Body, err)
	}
	return req, generic
}

func TestForward_AReportGoesToTheLeadHostAndComesBackWithTheLeadAsThisHostKnowsIt(t *testing.T) {
	f, fc := forwardFixture(t, func(map[string]any) (int, any) {
		return http.StatusCreated, team.ReportResponse{Report: team.Report{ID: reportID(1), Task: "000000-1", Kind: team.ReportAck},
			Lead: team.ReportLead{Ref: "_leadL", Address: "the-lead-hosts-own-name/lead"}}
	})
	code, body := f.do(http.MethodPost, "/api/team/reports", team.CreateReportRequest{OriginInbox: memberInbox, ReportRequest: rreq(1, team.ReportAck, "000000-1")})
	if code != http.StatusCreated {
		t.Fatalf("report = %d %s", code, body)
	}
	req, generic := lastProxyCall(t, fc)
	if req.ToHostID != "host-L" || req.MK != "mk-1" || req.Method != http.MethodPost || req.Path != "/api/team/reports" {
		t.Fatalf("request = %+v", req)
	}
	if strings.Contains(string(req.Body), "origin_inbox") || strings.Contains(string(req.Body), memberInbox) {
		t.Fatalf("the forwarded body names a caller: %s", req.Body)
	}
	if _, ok := generic["origin_inbox"]; ok {
		t.Fatal("origin_inbox at the top of the proxy request")
	}
	var out team.ReportResponse
	if err := json.Unmarshal(body, &out); err != nil || out.Report.ID != reportID(1) {
		t.Fatalf("answer = %s (%v)", body, err)
	}
	// the up message goes to the lead as THIS host knows it (the remote row's lead address), not as the lead host calls itself
	if out.Lead.Ref != "_lead01" || out.Lead.Address != "lead/x [lead01]" {
		t.Fatalf("lead = %+v", out.Lead)
	}
}

// A refusal of the lead host's is the CLI's refusal, status and body unchanged.
func TestForward_ABusinessRefusalPassesThroughUnchanged(t *testing.T) {
	f, _ := forwardFixture(t, func(map[string]any) (int, any) {
		return http.StatusConflict, team.APIError{Error: team.ErrTaskNotFound, Detail: "no such task"}
	})
	code, body := f.do(http.MethodPost, "/api/team/tasks/000000-9/status", map[string]any{"origin_inbox": memberInbox, "status": "in_progress"})
	var e team.APIError
	_ = json.Unmarshal(body, &e)
	if code != http.StatusConflict || e.Error != team.ErrTaskNotFound {
		t.Fatalf("status = %d %s", code, body)
	}
}

func TestForward_TheQueryAndThePathAreForwardedWithoutTheCaller(t *testing.T) {
	f, fc := forwardFixture(t, func(map[string]any) (int, any) { return http.StatusOK, team.TaskList{Tasks: []team.Task{}} })
	code, body := f.do(http.MethodGet, "/api/team/tasks?origin_inbox=%2Ftmp%2F20.sock&mine=1&all=1", nil)
	if code != http.StatusOK {
		t.Fatalf("list = %d %s", code, body)
	}
	req, _ := lastProxyCall(t, fc)
	if req.Method != http.MethodGet || req.Body != nil || strings.Contains(req.Path, "origin") || !strings.HasPrefix(req.Path, "/api/team/tasks?") ||
		!strings.Contains(req.Path, "mine=1") || !strings.Contains(req.Path, "all=1") {
		t.Fatalf("request = %+v", req)
	}
	if code, _ := f.do(http.MethodPost, "/api/team/tasks/000000-1/status", map[string]any{"origin_inbox": memberInbox, "status": "completed"}); code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	req, _ = lastProxyCall(t, fc)
	if req.Method != http.MethodPost || req.Path != "/api/team/tasks/000000-1/status" || strings.Contains(string(req.Body), "origin") || !strings.Contains(string(req.Body), "completed") {
		t.Fatalf("request = %+v body %s", req, req.Body)
	}
}

// The lead host cannot be reached: 503 lead_unreachable, the CLI retries.
func TestForward_AnUnreachableLeadHostIs503(t *testing.T) {
	f, fc := forwardFixture(t, nil)
	fc.script = func(string, map[string]any) peersmod.CallResult {
		return peersmod.CallResult{Class: peersmod.ClassTransient, Err: fmt.Errorf("timeout")}
	}
	code, body := f.do(http.MethodPost, "/api/team/reports", team.CreateReportRequest{OriginInbox: memberInbox, ReportRequest: rreq(2, team.ReportAck, "000000-1")})
	var e team.APIError
	_ = json.Unmarshal(body, &e)
	if code != http.StatusServiceUnavailable || e.Error != team.ErrProxyLeadUnreachable {
		t.Fatalf("report = %d %s", code, body)
	}
}

// Only an ACTIVE remote member of this host is forwarded: anyone else gets the ordinary answers, and nothing is sent.
func TestForward_OnlyAnActiveRemoteMemberIsForwarded(t *testing.T) {
	f, fc := forwardFixture(t, func(map[string]any) (int, any) { return http.StatusOK, map[string]any{} })
	if ok, err := f.m.store.SetRemoteMemberState("mk-1", []string{remoteActive}, remoteReleased, f.clock.Load()); err != nil || !ok {
		t.Fatal(err)
	}
	code, body := f.do(http.MethodPost, "/api/team/reports", team.CreateReportRequest{OriginInbox: memberInbox, ReportRequest: rreq(3, team.ReportAck, "000000-1")})
	var e team.APIError
	_ = json.Unmarshal(body, &e)
	if code != http.StatusConflict || e.Error != team.ErrNotMember {
		t.Fatalf("released member = %d %s", code, body)
	}
	// a session that is no remote member at all (the lead's, sid-1)
	if code, _ := f.do(http.MethodGet, "/api/team/tasks?origin_inbox=%2Ftmp%2F10.sock&mine=1", nil); code == http.StatusOK {
		t.Fatal("an unrelated session was served")
	}
	if len(fc.sent()) != 0 {
		t.Fatalf("something was forwarded: %+v", fc.sent())
	}
}
