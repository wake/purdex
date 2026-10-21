// cmd/pdx/device_scope_test.go
package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/devices"
)

// QP-1b-ii task 5: a device principal reaches only the patterns of deviceAllowed, decided by the mux itself.

var wildcardRe = regexp.MustCompile(`\{[^}]*\}`)

// requestFor turns a registered pattern into a request the mux would dispatch to it.
func requestFor(pattern string) *http.Request {
	method, path := http.MethodGet, pattern
	if i := strings.IndexByte(pattern, ' '); i > 0 {
		method, path = pattern[:i], pattern[i+1:]
	}
	return httptest.NewRequest(method, wildcardRe.ReplaceAllString(path, "x"), nil)
}

func realDaemonMux(t *testing.T) (*core.Core, *http.ServeMux) {
	t.Helper()
	c := core.New(core.CoreDeps{Config: &config.Config{DataDir: t.TempDir(), Token: "t", Push: &config.PushConfig{APNsDir: pushKeyDir(t)}}})
	require.NoError(t, registerServeModules(c, nil, nil))
	require.NoError(t, c.InitModules())
	mux := http.NewServeMux()
	c.RegisterCoreRoutes(mux)
	c.RegisterRoutes(mux)
	return c, mux
}

// Every pattern of the allow-list is a pattern the real daemon registers (a renamed route would otherwise silently lock the
// phone out), except the two nex ones which only exist with an engine configured. Mutation gate: a typo in an entry → red.
func TestDeviceAllowed_EveryEntryIsARegisteredPattern(t *testing.T) {
	_, mux := realDaemonMux(t)
	for pattern := range deviceAllowed {
		if strings.Contains(pattern, "/api/nex/") {
			continue
		}
		_, got := mux.Handler(requestFor(pattern))
		assert.Equal(t, pattern, got, "allow-list entry is not what the daemon dispatches to")
	}
}

func TestDeviceAllowed_ExactSet(t *testing.T) {
	want := []string{
		"GET /api/info", "POST /api/ws-ticket", "GET /api/hostconfig", "GET /api/fleet",
		"GET /api/pool/hosts", "GET /api/pool/hosts/{alias}/exceptions", "GET /api/pool/hosts/{alias}/qualification", "GET /api/pool/hosts/{alias}/alerts", "GET /api/pool/facts/history", "GET /api/pool/facts/series", // the resource pool from wwd (read-only)
		"GET /api/pool/jobs", "GET /api/pool/jobs/{id}", "GET /api/pool/jobs/{id}/log", // the pool's jobs (read-only)
		"GET /api/watch/wake-settings", // the watcher's wake settings (read-only; the PUT is the admin's)
		"/ws/host-events", "/ws/terminal/{code}", "GET /ws/conversations/{provider}/{session_id}",
		"POST /api/sessions", "POST /api/sessions/{code}/send-keys", "POST /api/sessions/{code}/read", "POST /api/sessions/{code}/submit", "POST /api/sessions/{code}/recall", "POST /api/sessions/{code}/send-now", "GET /api/sessions/{code}/provenance", "GET /api/sessions/{code}/transcript",
		"GET /api/sessions/{code}/launch-plan", "POST /api/sessions/{code}/launch", // the no-agent launch panel
		"GET /api/conversations/{provider}/{session_id}", "GET /api/conversations/{provider}/{session_id}/subagents/{agent_id}",
		"GET /api/team/approvals/{id}", "GET /api/team/adoptions/{id}", "POST /api/team/approvals/{id}/decide", "GET /api/team/approvals/{id}/lead-names", "GET /api/team/unattended", "PUT /api/team/unattended", "PUT /api/team/relay-quota", "PUT /api/team/max-members", "POST /api/relay/self",
		"GET /api/team/paused", "POST /api/team/paused/{session_id}/release", // the unattended quota pause: the 被擋下 list and 解除
		"GET /api/nex/v1/executions", "/api/nex/",
		"POST /api/push/devices", "GET /api/push/devices", "DELETE /api/push/devices/{device_id}",
		"GET /api/profiles", "GET /api/profiles/{id}", "GET /api/profiles/{id}/sections/{section}", "PUT /api/profiles/{id}/sections/{section}",
		"PUT /api/devices/self",
		"POST /api/agent/upload",
		"GET /api/workbook/conversations/{provider}/{session_id}", "GET /api/workbook/entries",
		"GET /api/workbook/conversations/{provider}/{session_id}/todos", // not the refresh: that is the Mac App's
		"GET /api/journal", "GET /api/journal/tasks", "GET /api/journal/roadmap", "PUT /api/journal/confirm", "GET /api/journal/members", // the workbook journal (iOS later)
	}
	got := make([]string, 0, len(deviceAllowed))
	for p := range deviceAllowed {
		got = append(got, p)
	}
	assert.ElementsMatch(t, want, got)
}

func mintAndUse(t *testing.T, c *core.Core, pairing, profile string) (id, token string) {
	t.Helper()
	mint := callWith(t, c, http.MethodPost, "/api/devices", "t", map[string]any{
		"pairing_id": pairing, "profile_id": profile, "label": "iPhone", "client": map[string]any{"kind": "app", "label": "Purdex.app"},
	})
	require.Equal(t, http.StatusCreated, mint.Code, mint.Body.String())
	var m struct{ ID, Token string }
	require.NoError(t, json.Unmarshal(mint.Body.Bytes(), &m))
	require.Equal(t, http.StatusOK, callWith(t, c, http.MethodGet, "/api/info", m.Token, nil).Code) // first use
	return m.ID, m.Token
}

// Through the real outer handler: what a phone needs is reachable (the allowed pattern's own handler answers, never the
// scope's 403), everything else is 403 device_forbidden; the admin is never scoped.
func TestDeviceScope_RealDaemonRoutes(t *testing.T) {
	c, _ := realDaemonMux(t)
	_, tok := mintAndUse(t, c, "00000000-0000-4000-8000-00000000000a", "p_0123456789ab")

	forbidden := func(rec *httptest.ResponseRecorder) bool {
		return rec.Code == http.StatusForbidden && strings.Contains(rec.Body.String(), "device_forbidden")
	}
	// A path that is not canonical is redirected by the outer mux before any handler: never served, never allowed through.
	assert.Equal(t, http.StatusTemporaryRedirect, callWith(t, c, "GET", "/api/info/../config", tok, nil).Code)
	for _, r := range []struct{ method, path string }{
		{"GET", "/api/config"}, {"PUT", "/api/config"}, {"POST", "/api/daemon/restart"},
		{"PUT", "/api/hostconfig/projects"}, {"GET", "/api/fleet/ssh-aliases"}, {"POST", "/api/fleet/probe-check"}, {"POST", "/api/fleet"}, {"PUT", "/api/hostconfig/probe-hosts"}, {"PUT", "/api/hostconfig/fleet"}, {"POST", "/api/hostconfig/check-path"},
		{"GET", "/api/sessions"}, {"DELETE", "/api/sessions/x"}, {"PATCH", "/api/sessions/x"}, {"GET", "/api/sessions/x/home"},
		{"POST", "/api/profiles"}, {"DELETE", "/api/profiles/p_0123456789ab"},
		{"PUT", "/api/profiles/p_0123456789ab/attachment"},
		{"GET", "/api/team/approvals"}, {"POST", "/api/team/approvals"}, {"DELETE", "/api/team/approvals/x"},
		{"GET", "/api/team/roster"}, {"PUT", "/api/team/appearance"}, // TR-1: the panel edit is the Mac App's (admin), not a phone's
		{"POST", "/api/team/relay-quota"}, {"POST", "/api/team/max-members"}, {"GET", "/api/team/relay-quota"},
		{"PUT", "/api/push/presence"},
		{"GET", "/api/team/paused/x/release"}, {"DELETE", "/api/team/paused/x"}, {"POST", "/api/team/paused"},
		{"POST", "/api/devices"}, {"GET", "/api/devices"}, {"DELETE", "/api/devices/d_aaaaaaaaaaaa"},
		{"PUT", "/api/notify/group-labels"}, // the groups the App writes back are written with the admin token (notify.group_labels.v1), never a phone's
		{"GET", "/api/fs/read"}, {"POST", "/api/host-transfer/redeem"}, {"GET", "/api/nothing-here"},
		{"GET", "/debug/pprof/"}, {"GET", "/debug/pprof/cmdline"}, {"GET", "/debug/pprof/heap"}, {"GET", "/debug/pprof/profile"}, {"GET", "/api/debug/execstat"}, // the profiler and the fork counters are the admin's alone
		{"PUT", "/api/watch/wake-settings"},                                                                                                                // the wake settings are read here, never changed (the admin writes them)
		{"POST", "/api/pool/jobs"}, {"DELETE", "/api/pool/jobs/j_abc"}, {"POST", "/api/pool/jobs/j_abc/cancel"}, {"POST", "/api/pool/jobs/j_abc/position"}, // the jobs are read here, never changed (cancel and position are the admin's: jobs.control.v1)
		{"PUT", "/api/pool/hosts/air26/worker"}, {"POST", "/api/pool/hosts/air26/exceptions"}, {"DELETE", "/api/pool/hosts/air26/exceptions/pkg"}, // changing a host's worker is the admin's, not a phone's
		{"GET", "/api/pool/hosts/air26/observations"}, {"GET", "/api/pool/hosts/air26/undeclared"}, // redacted command lines: the admin's until a phone's event page is decided (pool.observations.v1)
		{"POST", "/api/journal"}, {"PUT", "/api/journal/pin"}, {"GET", "/api/journal/confirm"}, {"DELETE", "/api/journal/confirm"}, {"PUT", "/api/journal/members"}, {"GET", "/api/journal/other"}, // only the five journal routes
	} {
		assert.True(t, forbidden(callWith(t, c, r.method, r.path, tok, nil)), "%s %s", r.method, r.path)
	}
	// The fleet's peer calls are the paired daemons' (host tokens), never a phone's: refused before the handler, whichever way.
	for _, p := range []string{"/api/peers/fleet/role", "/api/peers/fleet/announce"} {
		rec := callWith(t, c, "POST", p, tok, map[string]any{"to_host_id": "x", "from_alias": "air26", "role": "primary", "term": 99})
		assert.Contains(t, []int{http.StatusUnauthorized, http.StatusForbidden}, rec.Code, "POST %s: %s", p, rec.Body.String())
	}
	// Reachable: the scope lets these through to their own handlers (whatever those answer, not device_forbidden).
	for _, r := range []struct{ method, path string }{
		{"GET", "/api/info"}, {"GET", "/api/hostconfig"}, {"GET", "/api/fleet"}, {"GET", "/api/watch/wake-settings"}, {"GET", "/api/pool/jobs"}, {"GET", "/api/pool/jobs/j_abc"}, {"GET", "/api/pool/jobs/j_abc/log"}, {"GET", "/api/pool/facts/history"}, {"GET", "/api/pool/facts/series"}, {"GET", "/api/sessions/x/launch-plan"}, {"POST", "/api/sessions/x/launch"}, {"POST", "/api/sessions/x/read"}, {"PUT", "/api/team/relay-quota"}, {"PUT", "/api/team/max-members"}, {"GET", "/api/sessions/x/provenance"}, {"GET", "/api/team/unattended"},
		{"GET", "/api/push/devices"}, {"GET", "/api/profiles"}, {"GET", "/api/profiles/p_0123456789ab"}, {"PUT", "/api/devices/self"},
		{"GET", "/api/conversations/claude/sid"}, {"POST", "/api/ws-ticket"},
		{"POST", "/api/agent/upload"},
		{"GET", "/api/team/paused"}, {"POST", "/api/team/paused/x/release"},
		{"GET", "/api/journal"}, {"GET", "/api/journal/tasks"}, {"GET", "/api/journal/roadmap"}, {"PUT", "/api/journal/confirm"}, {"GET", "/api/journal/members"},
	} {
		assert.False(t, forbidden(callWith(t, c, r.method, r.path, tok, nil)), "%s %s", r.method, r.path)
	}
	// The admin is not scoped.
	assert.False(t, forbidden(callWith(t, c, "GET", "/api/config", "t", nil)))
	for _, p := range []string{"/api/pool/hosts/air26/observations", "/api/pool/hosts/air26/undeclared"} {
		assert.False(t, forbidden(callWith(t, c, "GET", p, "t", nil)), "the admin reaches %s", p)
	}
}

// A nil routes refuses every device request; the admin still works.
func TestDeviceScope_NilRoutesRefusesDevices(t *testing.T) {
	h := deviceScope(nil, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	req := httptest.NewRequest("GET", "/api/info", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req.WithContext(devices.WithPrincipal(req.Context(), devices.Principal{ID: "d_aaaaaaaaaaaa"})))
	assert.Equal(t, http.StatusForbidden, rec.Code)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
}

// The engine mount is one pattern; the scope looks at the path and method behind it.
func TestDeviceScope_NexEngineMountOnlyPreludeAndEventsByGet(t *testing.T) {
	mux := http.NewServeMux()
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	mux.Handle("/api/nex/", ok)
	mux.Handle("GET /api/nex/v1/executions", ok)
	mux.HandleFunc("POST /api/nex/executions/{id}/exit", ok)
	h := deviceScope(mux, mux)
	for _, c := range []struct {
		method, path string
		want         int
	}{
		{"GET", "/api/nex/v1/executions", 200},
		{"GET", "/api/nex/v1/executions/e1/prelude", 200},
		{"GET", "/api/nex/v1/executions/e1/events", 200},
		{"POST", "/api/nex/v1/executions/e1/events", 403},
		{"GET", "/api/nex/v1/executions/e1", 403},
		{"GET", "/api/nex/v1/executions/e1/events/x", 403},
		{"GET", "/api/nex/v1/executions//events", 403},
		{"POST", "/api/nex/v1/executions", 403},
		{"POST", "/api/nex/executions/e1/exit", 403},
		{"GET", "/api/nex/v1/other", 403},
	} {
		req := httptest.NewRequest(c.method, c.path, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req.WithContext(devices.WithPrincipal(req.Context(), devices.Principal{ID: "d_aaaaaaaaaaaa"})))
		assert.Equal(t, c.want, rec.Code, "%s %s", c.method, c.path)
	}
}

// End to end through newOuterHandler: a device asks for a ticket, opens an allowed WebSocket with it, and revoking the device
// severs it; the revoked device's ticket and token open nothing more.
func TestDeviceScope_TicketToAllowedWebSocketAndRevokeSevers(t *testing.T) {
	c, mux := realDaemonMux(t)
	id, tok := mintAndUse(t, c, "00000000-0000-4000-8000-00000000000a", "p_0123456789ab")
	srv := httptest.NewServer(newOuterHandler(c, mux, mux, []string{"127.0.0.1"}))
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")

	ticket := func() string {
		req, _ := http.NewRequest("POST", srv.URL+"/api/ws-ticket", nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode)
		var b struct{ Ticket string }
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&b))
		return b.Ticket
	}
	dial := func(path string) (*websocket.Conn, *http.Response, error) {
		u := url.URL{Scheme: "ws", Host: host, Path: path}
		return websocket.DefaultDialer.Dial(u.String(), nil)
	}

	conn, resp, err := func() (*websocket.Conn, *http.Response, error) {
		u := url.URL{Scheme: "ws", Host: host, Path: "/ws/host-events", RawQuery: "ticket=" + ticket()}
		return websocket.DefaultDialer.Dial(u.String(), nil)
	}()
	require.NoError(t, err, "allowed WebSocket route with the device's ticket: %v", resp)
	defer conn.Close()

	// A route outside the allow-list is refused for the same device, with a ticket too.
	req, _ := http.NewRequest("GET", srv.URL+"/api/config", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	out, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	out.Body.Close()
	assert.Equal(t, http.StatusForbidden, out.StatusCode)

	del, _ := http.NewRequest("DELETE", srv.URL+"/api/devices/"+id, nil)
	del.Header.Set("Authorization", "Bearer t")
	dr, err := http.DefaultClient.Do(del)
	require.NoError(t, err)
	dr.Body.Close()
	require.Equal(t, http.StatusNoContent, dr.StatusCode)

	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	for { // host-events sends its opening frames first; the revoke must end the stream
		if _, _, err = conn.ReadMessage(); err != nil {
			break
		}
	}
	if ne, ok := err.(interface{ Timeout() bool }); ok {
		assert.False(t, ne.Timeout(), "the connection was not severed by the revoke")
	}
	_, resp, err = dial("/ws/host-events")
	require.Error(t, err)
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

// The launch panel's two routes end to end with a device bearer: the scope lets them through to the session module's own
// handlers (whose answers are the module's, never device_forbidden), and the plan route stays GET-only and the launch POST-only.
func TestDeviceScope_LaunchPanelRoutesEndToEnd(t *testing.T) {
	c, _ := realDaemonMux(t)
	_, tok := mintAndUse(t, c, "00000000-0000-4000-8000-00000000000b", "p_0123456789ac")

	// POST: validation comes first, so a body with no expectation is the handler's own 400 bad_request JSON.
	rec := callWith(t, c, http.MethodPost, "/api/sessions/x/launch", tok, map[string]any{"kind": "fresh", "cd": false})
	assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	assert.JSONEq(t, `{"code":"bad_request"}`, rec.Body.String())

	// GET: the handler answers (an unknown session, or no tmux on the machine running the test) — not the scope's 403.
	rec = callWith(t, c, http.MethodGet, "/api/sessions/x/launch-plan?expected_tmux_instance=", tok, nil)
	assert.NotEqual(t, http.StatusForbidden, rec.Code, rec.Body.String())
	assert.NotContains(t, rec.Body.String(), "device_forbidden")

	// The other verbs on the same paths are not on the allow-list.
	for _, r := range []struct{ method, path string }{{"POST", "/api/sessions/x/launch-plan"}, {"GET", "/api/sessions/x/launch"}, {"DELETE", "/api/sessions/x/launch"}} {
		rec = callWith(t, c, r.method, r.path, tok, nil)
		assert.Equal(t, http.StatusForbidden, rec.Code, "%s %s", r.method, r.path)
		assert.Contains(t, rec.Body.String(), "device_forbidden")
	}
}

// The 403 a paired phone gets for PUT /api/watch/wake-settings is the device scope's own body, and the contract fixture holds exactly it
// (internal/wwd/wire/testdata/watch_wake_settings_put_403.json; the App keeps a byte copy). Rewrite: WWD_UPDATE_FIXTURES=1.
func TestDeviceScope_WakeSettingsPutFixture(t *testing.T) {
	c, _ := realDaemonMux(t)
	_, tok := mintAndUse(t, c, "00000000-0000-4000-8000-00000000000a", "p_0123456789ab")
	rec := callWith(t, c, "PUT", "/api/watch/wake-settings", tok, map[string]any{"settings": map[string]any{}, "expect_etag": "x"})
	require.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())

	var indented bytes.Buffer
	require.NoError(t, json.Indent(&indented, rec.Body.Bytes(), "", "  "))
	got := append(indented.Bytes(), '\n')
	path := "../../internal/wwd/wire/testdata/watch_wake_settings_put_403.json"
	if os.Getenv("WWD_UPDATE_FIXTURES") != "" {
		require.NoError(t, os.WriteFile(path, got, 0o644))
	}
	want, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, string(want), string(got))
}
