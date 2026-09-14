package nex

// Task 7c: mount and engine-level invariants, driven through the REAL
// nexen.Assemble (New() + Init) behind a fresh http.ServeMux with no outer
// pdx chain. A fake claude script plays one turn (init, one assistant
// frame, result) so the engine actually spawns processes and the events
// these tests read are the ones production would record.
//
// Spec invariants pinned here (see the plan's Task 7c brief):
//   - routing:  /api/nex/... is served, /api/nexus/... is not.
//   - I3:       every URL the engine emits is rendered under RoutePrefix.
//   - I4:       an unknown id is Nexen's own 404 body, not pdx's.
//   - I5:       after Stop, mutations are 503 "draining" while reads stay 200.
//   - I11:      every request authenticates as "pdx:<host_id>" — the lease
//               owner and the principal_id on delegated/message_accepted.
//   - I14:      Last-Event-ID resume through the mount yields the next
//               durable event first (engine half; pdx adds no buffering).

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// fakeTurnClaude is the plan's "Fake claude fixture": consume the turn's
// stdin message, emit init + one assistant frame + result, then block on
// stdin until the adapter closes it (Gate A: the process must not exit
// before the adapter has seen "result").
const fakeTurnClaude = `#!/bin/sh
head -n 1 >/dev/null
echo '{"type":"system","subtype":"init","session_id":"sess-fake","capabilities":[]}'
echo '{"type":"assistant","session_id":"sess-fake","message":{"role":"assistant","content":[{"type":"text","text":"hi"}]}}'
echo '{"type":"result","session_id":"sess-fake","subtype":"success"}'
cat >/dev/null
`

// mountFixture is one assembled module mounted on its own mux.
type mountFixture struct {
	m      *Module
	srv    *httptest.Server
	hostID string
	root   string // the single allowlisted repo root
}

// newMountFixture assembles the real engine and mounts it. Cleanup runs
// the spec's lifecycle order: Stop (drain) → HTTP server stops → Close.
func newMountFixture(t *testing.T) *mountFixture {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", launchdPath)

	cfg := baseConfig(t)
	cfg.Nex.ClaudeBin = writeScript(t, t.TempDir(), "claude", fakeTurnClaude)

	m := New()
	m.logf = discardLogf
	if err := m.Init(newTestCore(&cfg)); err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	mux := http.NewServeMux()
	m.RegisterRoutes(mux)
	srv := httptest.NewServer(mux)

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := m.Stop(ctx); err != nil {
			t.Errorf("Stop() error = %v", err)
		}
		srv.Close()
		if err := m.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	return &mountFixture{m: m, srv: srv, hostID: cfg.HostID, root: cfg.Nex.RepoRoots[0]}
}

// do issues one request against the mounted engine with no auth header
// (the pdx Authenticator accepts everything) and returns status + body.
func (f *mountFixture) do(t *testing.T, method, path string, body any) (int, []byte) {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		rdr = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, f.srv.URL+path, rdr)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("%s %s: reading body: %v", method, path, err)
	}
	return resp.StatusCode, raw
}

// doJSON is do plus a decode of the body into out, failing on a non-200.
func (f *mountFixture) doJSON(t *testing.T, method, path string, body, out any) {
	t.Helper()
	status, raw := f.do(t, method, path, body)
	if status != http.StatusOK {
		t.Fatalf("%s %s: status %d, body %s", method, path, status, raw)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		t.Fatalf("%s %s: decoding %s: %v", method, path, raw, err)
	}
}

// delegate starts one execution on the fixture's root and returns its id.
func (f *mountFixture) delegate(t *testing.T) string {
	t.Helper()
	var res struct {
		ID           string `json:"id"`
		State        string `json:"state"`
		RejectReason string `json:"reject_reason"`
	}
	f.doJSON(t, http.MethodPost, "/api/nex/v1/executions", map[string]any{
		"provider":        "claude",
		"brief":           "say hi",
		"sandbox_profile": "trusted",
		"mounts": []map[string]any{
			{"path": f.root, "role": "cwd", "writable": true},
		},
	}, &res)
	if res.ID == "" || res.State == "rejected" {
		t.Fatalf("delegate: unexpected result %+v", res)
	}
	return res.ID
}

// eventView mirrors the fields of Nexen's history page this test reads.
type eventView struct {
	Seq     int64           `json:"seq"`
	Kind    string          `json:"kind"`
	Payload json.RawMessage `json:"payload"`
}

// events reads the execution's full durable history in seq order.
func (f *mountFixture) events(t *testing.T, id string) []eventView {
	t.Helper()
	var page struct {
		Items []eventView `json:"items"`
	}
	f.doJSON(t, http.MethodGet, "/api/nex/v1/executions/"+id+"/events?limit=500", nil, &page)
	return page.Items
}

// summary reads the execution's authoritative summary.
func (f *mountFixture) summary(t *testing.T, id string) map[string]json.RawMessage {
	t.Helper()
	var out map[string]json.RawMessage
	f.doJSON(t, http.MethodGet, "/api/nex/v1/executions/"+id, nil, &out)
	return out
}

// waitFor polls cond until it reports true or the deadline passes. The
// only sleeps in this file live inside this bounded loop.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		if cond() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// waitTurnDone waits until the fake claude's "result" has been recorded
// and the execution has settled back to idle, so a following send starts a
// fresh turn rather than queueing behind this one.
func (f *mountFixture) waitTurnDone(t *testing.T, id string, turn int) {
	t.Helper()
	waitFor(t, "turn "+strconv.Itoa(turn)+" result", func() bool {
		n := 0
		for _, ev := range f.events(t, id) {
			if ev.Kind == "result" {
				n++
			}
		}
		return n >= turn
	})
	waitFor(t, "execution idle after turn "+strconv.Itoa(turn), func() bool {
		var state string
		_ = json.Unmarshal(f.summary(t, id)["state"], &state)
		return state == "idle"
	})
}

// principalOf decodes payload.principal_id.
func principalOf(t *testing.T, ev eventView) string {
	t.Helper()
	var p struct {
		PrincipalID string `json:"principal_id"`
	}
	if err := json.Unmarshal(ev.Payload, &p); err != nil {
		t.Fatalf("decoding %s payload %s: %v", ev.Kind, ev.Payload, err)
	}
	return p.PrincipalID
}

// TestMountRouting: the engine answers under RoutePrefix and nowhere else.
// /api/nexus/... shares every byte of /api/nex as a prefix, so it is the
// case a naive HasPrefix mount would wrongly serve.
func TestMountRouting(t *testing.T) {
	f := newMountFixture(t)

	if status, body := f.do(t, http.MethodGet, "/api/nex/v1/capabilities", nil); status != http.StatusOK {
		t.Errorf("GET /api/nex/v1/capabilities = %d, want 200; body %s", status, body)
	}
	if status, _ := f.do(t, http.MethodGet, "/api/nexus/v1/capabilities", nil); status != http.StatusNotFound {
		t.Errorf("GET /api/nexus/v1/capabilities = %d, want 404", status)
	}
}

// TestMountI3PrefixRendering: every URL the engine hands to a client —
// the lease operations in capabilities and attach(observe)'s stream_url —
// is rendered under RoutePrefix, so a consumer can follow them verbatim
// through the pdx mount.
func TestMountI3PrefixRendering(t *testing.T) {
	f := newMountFixture(t)

	var caps struct {
		HostID          string   `json:"host_id"`
		SandboxProfiles []string `json:"sandbox_profiles"`
		Lease           struct {
			Renew   struct{ Path string } `json:"renew"`
			Release struct{ Path string } `json:"release"`
		} `json:"lease"`
	}
	f.doJSON(t, http.MethodGet, "/api/nex/v1/capabilities", nil, &caps)
	if caps.HostID != f.hostID {
		t.Errorf("capabilities.host_id = %q, want %q", caps.HostID, f.hostID)
	}
	if len(caps.SandboxProfiles) == 0 {
		t.Errorf("capabilities.sandbox_profiles is empty")
	}
	for name, p := range map[string]string{
		"lease.renew.path":   caps.Lease.Renew.Path,
		"lease.release.path": caps.Lease.Release.Path,
	} {
		if !strings.HasPrefix(p, RoutePrefix+"/v1/") {
			t.Errorf("capabilities.%s = %q, want prefix %q", name, p, RoutePrefix+"/v1/")
		}
	}

	id := f.delegate(t)
	var attach struct {
		Mode      string `json:"mode"`
		StreamURL string `json:"stream_url"`
		Cursor    int64  `json:"cursor"`
		State     string `json:"state"`
	}
	f.doJSON(t, http.MethodPost, "/api/nex/v1/executions/"+id+"/attach", map[string]string{"mode": "observe"}, &attach)
	if attach.Mode != "observe" {
		t.Errorf("attach.mode = %q, want observe", attach.Mode)
	}
	if want := RoutePrefix + "/v1/events?"; !strings.HasPrefix(attach.StreamURL, want) {
		t.Errorf("attach.stream_url = %q, want prefix %q", attach.StreamURL, want)
	}
	if !strings.Contains(attach.StreamURL, "execution_id="+id) {
		t.Errorf("attach.stream_url = %q, want it to scope execution_id=%s", attach.StreamURL, id)
	}
	// The stream_url must actually resolve through the mount: follow it.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.srv.URL+attach.StreamURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", attach.StreamURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET %s = %d, want 200", attach.StreamURL, resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("GET %s Content-Type = %q, want text/event-stream", attach.StreamURL, ct)
	}
	f.waitTurnDone(t, id, 1)
}

// TestMountI4UnknownExecutionIsNexen404: an id no row matches is Nexen's
// own structured 404 (code execution_not_found), proving the request
// reached the engine rather than falling off the mux.
func TestMountI4UnknownExecutionIsNexen404(t *testing.T) {
	f := newMountFixture(t)

	status, body := f.do(t, http.MethodGet, "/api/nex/v1/executions/exc_nope", nil)
	if status != http.StatusNotFound {
		t.Fatalf("GET /api/nex/v1/executions/exc_nope = %d, want 404; body %s", status, body)
	}
	var e struct {
		Error string `json:"error"`
		Code  string `json:"code"`
	}
	if err := json.Unmarshal(body, &e); err != nil {
		t.Fatalf("404 body %s is not JSON: %v", body, err)
	}
	if e.Code != "execution_not_found" {
		t.Errorf("404 body code = %q, want execution_not_found; body %s", e.Code, body)
	}
	if e.Error == "" {
		t.Errorf("404 body has no error text; body %s", body)
	}
}

// TestMountI5DrainingAfterStop: once Stop has drained the engine, a
// mutation is refused with 503 "draining" while a plain read still
// answers — the daemon's shutdown window stays observable.
func TestMountI5DrainingAfterStop(t *testing.T) {
	f := newMountFixture(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := f.m.Stop(ctx); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}

	status, body := f.do(t, http.MethodPost, "/api/nex/v1/executions", map[string]any{
		"provider": "claude", "brief": "x", "sandbox_profile": "trusted",
		"mounts": []map[string]any{{"path": f.root, "role": "cwd", "writable": true}},
	})
	if status != http.StatusServiceUnavailable {
		t.Errorf("POST /api/nex/v1/executions after Stop = %d, want 503; body %s", status, body)
	}
	if !strings.Contains(string(body), "draining") {
		t.Errorf("POST /api/nex/v1/executions after Stop body = %s, want it to contain %q", body, "draining")
	}

	if status, body := f.do(t, http.MethodGet, "/api/nex/v1/executions", nil); status != http.StatusOK {
		t.Errorf("GET /api/nex/v1/executions after Stop = %d, want 200; body %s", status, body)
	}
}

// TestMountI11PrincipalIsPdxHost: with no auth header at all, the engine
// attributes everything to "pdx:<host_id>" — the lease owner after
// attach(control), and principal_id on both execution.delegated and
// execution.message_accepted.
func TestMountI11PrincipalIsPdxHost(t *testing.T) {
	f := newMountFixture(t)
	want := "pdx:" + f.hostID

	id := f.delegate(t)
	f.waitTurnDone(t, id, 1)

	var lease struct {
		Mode      string `json:"mode"`
		LeaseID   string `json:"lease_id"`
		ExpiresAt int64  `json:"expires_at"`
	}
	f.doJSON(t, http.MethodPost, "/api/nex/v1/executions/"+id+"/attach", map[string]string{"mode": "control"}, &lease)
	if lease.Mode != "control" || lease.LeaseID == "" {
		t.Fatalf("attach(control) = %+v, want mode control with a lease id", lease)
	}
	var owner struct {
		PrincipalID string `json:"principal_id"`
	}
	if raw, ok := f.summary(t, id)["lease"]; !ok || string(raw) == "null" {
		t.Fatalf("summary.lease missing after attach(control)")
	} else if err := json.Unmarshal(raw, &owner); err != nil {
		t.Fatal(err)
	}
	if owner.PrincipalID != want {
		t.Errorf("summary.lease.principal_id = %q, want %q", owner.PrincipalID, want)
	}
	var summary struct {
		PrincipalID string `json:"principal_id"`
	}
	if err := json.Unmarshal(f.summary(t, id)["principal_id"], &summary.PrincipalID); err != nil {
		t.Fatal(err)
	}
	if summary.PrincipalID != want {
		t.Errorf("summary.principal_id = %q, want %q", summary.PrincipalID, want)
	}

	// A second turn is `claude --resume`, which the engine only launches
	// if the session transcript exists (execution.ErrSessionExpired
	// otherwise). The real claude writes that file as a side effect of
	// turn 1; the fake does not, so stand in for it here at the exact path
	// the engine computed and published on the summary.
	var transcript string
	if err := json.Unmarshal(f.summary(t, id)["transcript_path"], &transcript); err != nil || transcript == "" {
		t.Fatalf("summary.transcript_path unavailable after turn 1 (err %v)", err)
	}
	if err := os.MkdirAll(filepath.Dir(transcript), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(transcript, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var sent struct {
		TurnID   string `json:"turn_id"`
		Delivery string `json:"delivery"`
	}
	f.doJSON(t, http.MethodPost, "/api/nex/v1/executions/"+id+"/messages",
		map[string]string{"lease_id": lease.LeaseID, "text": "again"}, &sent)
	if sent.TurnID == "" {
		t.Fatalf("send = %+v, want a turn id", sent)
	}
	f.waitTurnDone(t, id, 2)

	byKind := map[string][]eventView{}
	for _, ev := range f.events(t, id) {
		byKind[ev.Kind] = append(byKind[ev.Kind], ev)
	}
	for _, kind := range []string{"execution.delegated", "execution.message_accepted", "lease.acquired"} {
		evs := byKind[kind]
		if len(evs) == 0 {
			t.Errorf("events contain no %s", kind)
			continue
		}
		for _, ev := range evs {
			if got := principalOf(t, ev); got != want {
				t.Errorf("%s (seq %d) principal_id = %q, want %q", kind, ev.Seq, got, want)
			}
		}
	}
}

// TestMountI14ResumeFromLastEventID: an SSE resume through the mount with
// Last-Event-ID set to the first durable event's seq yields the second
// durable event as the first id-bearing frame — no gap, no duplicate.
// Snapshot (partial) frames carry no id: line and are skipped.
func TestMountI14ResumeFromLastEventID(t *testing.T) {
	f := newMountFixture(t)

	id := f.delegate(t)
	f.waitTurnDone(t, id, 1)
	history := f.events(t, id)
	if len(history) < 2 {
		t.Fatalf("history has %d events, need at least 2: %+v", len(history), history)
	}
	first, second := history[0], history[1]

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		f.srv.URL+"/api/nex/v1/events?execution_id="+id, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Last-Event-ID", strconv.FormatInt(first.Seq, 10))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /api/nex/v1/events: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/nex/v1/events = %d, want 200", resp.StatusCode)
	}

	// Read frames until the first id: line; the request ctx bounds the
	// read, so a stream that never yields one fails instead of hanging.
	sc := bufio.NewScanner(resp.Body)
	var gotID int64 = -1
	var gotKind string
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "id: ") {
			gotID, err = strconv.ParseInt(strings.TrimPrefix(line, "id: "), 10, 64)
			if err != nil {
				t.Fatalf("parsing %q: %v", line, err)
			}
			continue
		}
		if gotID >= 0 && strings.HasPrefix(line, "event: ") {
			gotKind = strings.TrimPrefix(line, "event: ")
			break
		}
	}
	cancel() // release the stream before checking; the server holds it open otherwise
	if gotID < 0 {
		t.Fatalf("no id: frame read before deadline (scanner err %v)", sc.Err())
	}
	if gotID != second.Seq {
		t.Errorf("first resumed frame id = %d, want the 2nd event's seq %d (1st was %d)", gotID, second.Seq, first.Seq)
	}
	if gotKind != second.Kind {
		t.Errorf("first resumed frame kind = %q, want %q", gotKind, second.Kind)
	}
}
