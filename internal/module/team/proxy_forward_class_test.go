// internal/module/team/proxy_forward_class_test.go
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

// What the host caller knows about a failed call is kept: only a transport failure (or a token the lead host has not learnt)
// is "retry"; a lead host that is no longer paired, does not serve the route or refused the call is not (codex attack).
func TestForward_EachFailureClassKeepsItsMeaning(t *testing.T) {
	for _, c := range []struct {
		class  peersmod.CallClass
		status int
		code   string
	}{
		{peersmod.ClassTransient, http.StatusServiceUnavailable, team.ErrProxyLeadUnreachable},
		{peersmod.ClassUnauthorized, http.StatusServiceUnavailable, team.ErrProxyLeadUnreachable},
		{peersmod.ClassUnpaired, http.StatusConflict, team.ErrProxyLeadUnpaired},
		{peersmod.ClassWrongHost, http.StatusConflict, team.ErrCommandWrongHost},
		{peersmod.ClassUnsupported, http.StatusConflict, team.ErrRemoteUnsupported},
		{peersmod.ClassRefused, http.StatusBadGateway, team.ErrProxyLeadRefused},
	} {
		t.Run(string(c.class), func(t *testing.T) {
			f, fc := forwardFixture(t, nil)
			fc.script = func(string, map[string]any) peersmod.CallResult {
				return peersmod.CallResult{Class: c.class, Code: "x", Err: fmt.Errorf("boom")}
			}
			code, body := f.do(http.MethodGet, "/api/team/tasks?origin_inbox=%2Ftmp%2F20.sock&mine=1", nil)
			var e team.APIError
			_ = json.Unmarshal(body, &e)
			if code != c.status || e.Error != c.code {
				t.Fatalf("%s = %d %s, want %d %s", c.class, code, body, c.status, c.code)
			}
		})
	}
}

// A transport failure of a report (it may have been stored with the answer lost) is tried once more with the VERY SAME
// request, which the lead host dedups by report id; a status change is not replayed.
func TestForward_ATransportFailureRetriesIdempotentCallsOnceWithTheSameRequest(t *testing.T) {
	f, fc := forwardFixture(t, nil)
	n := 0
	fc.script = func(host string, body map[string]any) peersmod.CallResult {
		n++
		if n == 1 {
			return peersmod.CallResult{Class: peersmod.ClassTransient, Err: fmt.Errorf("timeout")}
		}
		raw, _ := json.Marshal(team.ProxyAnswer{HostID: host, Status: http.StatusOK, Body: json.RawMessage(`{"report":{"id":"x"},"task":{},"lead":{}}`)})
		return peersmod.CallResult{Class: peersmod.ClassDone, Status: 200, Body: raw}
	}
	code, body := f.do(http.MethodPost, "/api/team/reports", team.CreateReportRequest{OriginInbox: memberInbox, ReportRequest: rreq(7, team.ReportAck, "000000-1")})
	if code != http.StatusOK {
		t.Fatalf("report = %d %s", code, body)
	}
	calls := fc.sent()
	if len(calls) != 2 || string(calls[0].Body) != string(calls[1].Body) {
		t.Fatalf("calls = %d, want two with the same body", len(calls))
	}

	// the report that still fails says how to resend it safely
	fc2 := &fakeHostCaller{script: func(string, map[string]any) peersmod.CallResult {
		return peersmod.CallResult{Class: peersmod.ClassTransient, Err: fmt.Errorf("timeout")}
	}}
	f.m.cmdCaller = fc2
	code, body = f.do(http.MethodPost, "/api/team/reports", team.CreateReportRequest{OriginInbox: memberInbox, ReportRequest: rreq(8, team.ReportAck, "000000-1")})
	if code != http.StatusServiceUnavailable || !strings.Contains(string(body), "--id "+reportID(8)) {
		t.Fatalf("failed report = %d %s, want a 503 naming the report id to resend with", code, body)
	}
	if len(fc2.sent()) != 2 {
		t.Fatalf("a report was tried %d times, want 2", len(fc2.sent()))
	}

	// a lead host that ANSWERED (429, 5xx, a redirect) is not asked again at once: only a transport failure is retried
	for _, status := range []int{http.StatusTooManyRequests, http.StatusBadGateway, http.StatusFound} {
		fcs := &fakeHostCaller{script: func(string, map[string]any) peersmod.CallResult {
			return peersmod.CallResult{Class: peersmod.ClassTransient, Status: status}
		}}
		f.m.cmdCaller = fcs
		if code, _ := f.do(http.MethodPost, "/api/team/reports", team.CreateReportRequest{OriginInbox: memberInbox, ReportRequest: rreq(9, team.ReportAck, "000000-1")}); code != http.StatusServiceUnavailable {
			t.Fatalf("answered %d: report = %d", status, code)
		}
		if len(fcs.sent()) != 1 {
			t.Fatalf("a lead host that answered %d was asked %d times", status, len(fcs.sent()))
		}
	}

	// a status change is sent once
	fc3 := &fakeHostCaller{script: func(string, map[string]any) peersmod.CallResult {
		return peersmod.CallResult{Class: peersmod.ClassTransient, Err: fmt.Errorf("timeout")}
	}}
	f.m.cmdCaller = fc3
	if code, _ := f.do(http.MethodPost, "/api/team/tasks/000000-1/status", map[string]any{"origin_inbox": memberInbox, "status": "completed"}); code != http.StatusServiceUnavailable {
		t.Fatalf("status change = %d", code)
	}
	if len(fc3.sent()) != 1 {
		t.Fatalf("a status change was sent %d times, want 1", len(fc3.sent()))
	}
}
