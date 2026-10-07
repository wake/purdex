package main

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wake/purdex/cmd/pdx/daemonclient"
	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/team"
)

// fakeRelayDaemon speaks the P5a routes (plan preamble "Routes (P5a)") with
// canned answers: begin answers beginStatus/beginBody, wait answers open
// openUntil times then final, report answers reportStatus/reportBody, hello
// and self answer fixed bodies. It records every path and body.
type fakeRelayDaemon struct {
	mu           sync.Mutex
	paths        []string
	bodies       []string
	beginStatus  int
	beginBody    any
	openUntil    int
	final        team.Approval
	reportStatus int
	reportBody   any
	polls        int
	pollDelay    time.Duration // >0: every wait poll sleeps this long (or until the request is cancelled) before answering
	onPoll       func(n int)   // runs on every wait poll (1-based) before any delay
}

func (f *fakeRelayDaemon) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var b bytes.Buffer
	_, _ = b.ReadFrom(r.Body)
	f.mu.Lock()
	f.paths = append(f.paths, r.Method+" "+r.URL.RequestURI())
	f.bodies = append(f.bodies, b.String())
	f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.URL.Path == "/api/health":
		_ = json.NewEncoder(w).Encode(map[string]string{"boot_id": "b1"})
	case r.URL.Path == "/api/relay/hello":
		_ = json.NewEncoder(w).Encode(team.RelayHelloResponse{OK: true, Role: "none", SelfRelay: "on", Threshold: 70, MinGrowth: 20000})
	case r.URL.Path == "/api/relay/begin":
		if f.beginStatus == 0 {
			f.beginStatus = http.StatusCreated
		}
		w.WriteHeader(f.beginStatus)
		_ = json.NewEncoder(w).Encode(f.beginBody)
	case strings.HasPrefix(r.URL.Path, "/api/relay/wait/"):
		f.mu.Lock()
		f.polls++
		n := f.polls
		onPoll := f.onPoll
		f.mu.Unlock()
		if onPoll != nil {
			onPoll(n)
		}
		if r.URL.Query().Get("wait") == "0" {
			// The bound's final short read: no long poll, the row as it is.
			id := strings.TrimPrefix(r.URL.Path, "/api/relay/wait/")
			ap := f.final
			if ap.State == "" {
				ap = team.Approval{Kind: team.KindSelfRelay, State: team.StateOpen}
			}
			ap.ID = id
			_ = json.NewEncoder(w).Encode(ap)
			return
		}
		if f.pollDelay > 0 {
			select {
			case <-time.After(f.pollDelay):
			case <-r.Context().Done():
				return
			}
		}
		id := strings.TrimPrefix(r.URL.Path, "/api/relay/wait/")
		if n <= f.openUntil {
			_ = json.NewEncoder(w).Encode(team.Approval{ID: id, Kind: team.KindSelfRelay, State: team.StateOpen})
			return
		}
		ap := f.final
		ap.ID = id
		_ = json.NewEncoder(w).Encode(ap)
	case r.URL.Path == "/api/relay/self":
		_ = json.NewEncoder(w).Encode(team.RelaySelfResponse{SelfRelay: "paused", HostSwitch: true})
	case strings.HasSuffix(r.URL.Path, "/report"):
		if f.reportStatus == 0 {
			f.reportStatus = http.StatusOK
		}
		w.WriteHeader(f.reportStatus)
		_ = json.NewEncoder(w).Encode(f.reportBody)
	case strings.HasPrefix(r.URL.Path, "/api/relay/ops/"):
		_ = json.NewEncoder(w).Encode(team.RelayOp{ID: strings.TrimPrefix(r.URL.Path, "/api/relay/ops/"), State: team.RelayClaimed})
	case r.URL.Path == "/api/relay/prompts":
		_ = json.NewEncoder(w).Encode(team.NewRelayPrompts(team.RelayPromptBodies{Write: "寫 {{path}}\n<&>"}))
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeRelayDaemon) snapshot() (paths, bodies []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string{}, f.paths...), append([]string{}, f.bodies...)
}

// driveRelay runs runRelayCmd against d with a fake clock and --config appended.
func driveRelay(t *testing.T, ctx context.Context, d http.Handler, args ...string) (int, string, string) {
	t.Helper()
	return driveRelayWith(t, ctx, d, []daemonclient.Option{leadClockOpt()}, args...)
}

// driveRelayWith is driveRelay with the client options chosen by the test.
func driveRelayWith(t *testing.T, ctx context.Context, d http.Handler, opts []daemonclient.Option, args ...string) (int, string, string) {
	t.Helper()
	srv := httptest.NewServer(d)
	defer srv.Close()
	cfgPath := writeTestConfig(t, srv.URL, "admin-tok")
	var stdout, stderr bytes.Buffer
	code := runRelayCmd(ctx, append(args, "--config", cfgPath), &stdout, &stderr, opts...)
	return code, stdout.String(), stderr.String()
}

func TestRelayCmd_UsageErrorsExit2BeforeAnyRequest(t *testing.T) {
	d := &fakeRelayDaemon{}
	for _, args := range [][]string{
		{}, {"dance"}, {"hello"}, {"begin", "--session", "s"}, {"begin", "--self", "--session", "s", "--used", "150"},
		{"begin", "--self", "--session", "s", "--used", "NaN", "--window", "1000"}, // PR #1726 R1: non-finite is a grammar error, not a JSON failure
		{"begin", "--self", "--session", "s", "--used", "+Inf", "--window", "1000"},
		{"begin", "--self", "--session", "s", "--used", "70"}, // PR #1726 R1: --window is required
		{"wait"}, {"self", "maybe", "--session", "s"}, {"self", "on"}, {"report", "op"}, {"report", "op", "flying"},
		{"report", "op", "cleared"}, {"report", "op", "failed"}, {"op"},
		// claimed is not a reportable state (P5a-2b codex R1): a self op is
		// claimed by its approval's close, a member op by P6's claim route.
		{"report", "op", "claimed"},
	} {
		code, _, stderr := driveRelay(t, context.Background(), d, args...)
		if code != ExitUsage || !strings.Contains(stderr, "usage: pdx relay") {
			t.Fatalf("%v: code=%d stderr=%q", args, code, stderr)
		}
	}
	if paths, _ := d.snapshot(); len(paths) != 0 {
		t.Fatalf("usage errors must not reach the daemon: %v", paths)
	}
}

// A grammar rejection is exit 2 even when the config cannot be loaded: the
// usage check runs before any config load (spec §14). The file must be
// unparsable, not missing — config.Load treats a missing file as defaults.
func TestRelayCmd_UsageErrorsBeforeConfigLoad(t *testing.T) {
	broken := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(broken, []byte("port = = 7860\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := config.Load(broken); err == nil {
		t.Fatal("the fixture config must fail to load")
	}
	for _, args := range [][]string{
		{"hello", "--config", broken}, {"begin", "--session", "s", "--config", broken},
		{"wait", "--config", broken}, {"self", "on", "--config", broken},
		{"report", "op", "claimed", "--config", broken}, {"op", "--config", broken},
	} {
		var stdout, stderr bytes.Buffer
		code := runRelayCmd(context.Background(), args, &stdout, &stderr)
		if code != ExitUsage || !strings.Contains(stderr.String(), "usage: pdx relay") || stdout.Len() != 0 {
			t.Fatalf("%v: code=%d stdout=%q stderr=%q", args, code, stdout.String(), stderr.String())
		}
	}
}

func TestRelayCmd_BeginPrintsOpAndRequestOrRefuses(t *testing.T) {
	pct := 72.4
	ok := &fakeRelayDaemon{beginStatus: 201, beginBody: team.RelayBeginResponse{Op: team.RelayOp{ID: "op-1", State: team.RelayAwaitingApproval, UsedPercentage: &pct}, RequestID: "req-1"}}
	code, stdout, stderr := driveRelay(t, context.Background(), ok, "begin", "--self", "--session", "sid-1", "--used", "72.4", "--window", "1000000")
	if code != ExitOK {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
	var out team.RelayBeginResponse
	if err := json.Unmarshal([]byte(stdout), &out); err != nil || out.Op.ID != "op-1" || out.RequestID != "req-1" {
		t.Fatalf("stdout=%q err=%v", stdout, err)
	}
	_, bodies := ok.snapshot()
	var sent team.RelayBeginRequest
	_ = json.Unmarshal([]byte(bodies[len(bodies)-1]), &sent)
	if !sent.Self || sent.SessionID != "sid-1" || sent.UsedPercentage != 72.4 || sent.Window != 1_000_000 {
		t.Fatalf("sent = %+v", sent)
	}
	// PR #1726 A-1: the CLI mints the request id (UUID v4) before the first
	// attempt and sends it, so a replay is the same request at the daemon.
	if u, err := uuid.Parse(sent.RequestID); err != nil || u.Version() != 4 {
		t.Fatalf("request_id = %q, want a UUID v4", sent.RequestID)
	}
	for _, c := range []struct {
		code   string
		wantOp bool
	}{{team.ErrSelfRelayOff, false}, {team.ErrSelfRelayPaused, false}, {team.ErrMemberRelayIsLeads, false}, {team.ErrRelayOpen, true}} {
		body := team.APIError{Error: c.code, Detail: "d"}
		if c.wantOp {
			body.Op = &team.RelayOp{ID: "op-open", State: team.RelayClaimed}
		}
		d := &fakeRelayDaemon{beginStatus: http.StatusConflict, beginBody: body}
		code, stdout, stderr := driveRelay(t, context.Background(), d, "begin", "--self", "--session", "sid-1", "--used", "72", "--window", "200000")
		if code != ExitRefused || !strings.HasPrefix(stderr, "pdx relay: ") {
			t.Fatalf("%s: code=%d stderr=%q", c.code, code, stderr)
		}
		// The 409 code is the LAST stderr token: the mod's stderrCode()
		// splits on whitespace and takes the last one.
		if toks := strings.Fields(stderr); len(toks) == 0 || toks[len(toks)-1] != c.code {
			t.Fatalf("%s: the code must be the last stderr token, got %q", c.code, stderr)
		}
		if c.wantOp != strings.Contains(stdout, `"id":"op-open"`) {
			t.Fatalf("%s: stdout=%q (relay_open must print the open op, others nothing)", c.code, stdout)
		}
	}
	nf := &fakeRelayDaemon{beginStatus: http.StatusNotFound, beginBody: team.APIError{Error: team.ErrUnknownSession}}
	if code, _, stderr := driveRelay(t, context.Background(), nf, "begin", "--self", "--session", "sid-x", "--used", "72", "--window", "200000"); code != ExitError || !strings.Contains(stderr, team.ErrUnknownSession) {
		t.Fatalf("unknown_session: code=%d stderr=%q", code, stderr)
	}
}

// Exit codes for each terminal state (spec §14), plus "still open" (exit 0,
// stdout the row with state "open") and the 404 → 21 path.
func TestRelayCmd_WaitExitCodes(t *testing.T) {
	cases := []struct {
		state team.State
		code  int
	}{
		{team.StateApproved, ExitOK}, {team.StateDenied, ExitDenied}, {team.StateTimeout, ExitTimeout},
		{team.StateCancelled, ExitCancelled}, {team.StateAbandoned, ExitCancelled},
	}
	for _, c := range cases {
		d := &fakeRelayDaemon{openUntil: 2, final: team.Approval{Kind: team.KindSelfRelay, State: c.state, DecidedBy: &team.Client{Kind: "app", Label: "Purdex.app @ air26"}}}
		code, stdout, stderr := driveRelay(t, context.Background(), d, "wait", "req-1")
		if code != c.code {
			t.Fatalf("%s: code=%d stderr=%q", c.state, code, stderr)
		}
		paths, _ := d.snapshot()
		polls := 0
		for _, p := range paths {
			if strings.HasPrefix(p, "GET /api/relay/wait/req-1?wait=25") {
				polls++
			}
		}
		if polls != 3 {
			t.Fatalf("%s: %d polls, want 3 (two open, one final): %v", c.state, polls, paths)
		}
		if c.state == team.StateApproved {
			var ap team.Approval
			if err := json.Unmarshal([]byte(stdout), &ap); err != nil || ap.State != team.StateApproved || ap.ID != "req-1" {
				t.Fatalf("approved stdout=%q err=%v", stdout, err)
			}
		} else if stdout != "" {
			t.Fatalf("%s: stdout=%q, want empty", c.state, stdout)
		}
		if c.state == team.StateDenied && !strings.Contains(stderr, "Purdex.app @ air26") {
			t.Fatalf("denied must say who: %q", stderr)
		}
	}
	// Still open when --wait runs out: exit 0, the approval row (`"state":"open"`) on stdout
	// (the mod's waitLoop re-calls on exactly that shape; anything else at
	// exit 0 that is not state approved is treated as not approved).
	d := &fakeRelayDaemon{openUntil: 1 << 30}
	code, stdout, stderr := driveRelay(t, context.Background(), d, "wait", "req-1", "--wait", "300ms")
	var open struct {
		State team.State `json:"state"`
	}
	if code != ExitOK || json.Unmarshal([]byte(stdout), &open) != nil || open.State != team.StateOpen || !strings.Contains(stderr, "再呼叫一次") {
		t.Fatalf("still open: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	// The bound can run out before the first poll answers (a slow daemon):
	// stdout still carries a row with "state":"open" — never empty at exit 0.
	slow := &fakeRelayDaemon{openUntil: 1 << 30, pollDelay: 2 * time.Second}
	code, stdout, _ = driveRelay(t, context.Background(), slow, "wait", "req-1", "--wait", "50ms")
	if code != ExitOK || !strings.Contains(stdout, `"state":"open"`) {
		t.Fatalf("still open before first answer: code=%d stdout=%q", code, stdout)
	}
	// An older daemon: plain 404 → 21.
	code, _, stderr = driveRelay(t, context.Background(), http.NotFoundHandler(), "wait", "req-1")
	if code != ExitUnsupported || !strings.Contains(stderr, "unsupported") {
		t.Fatalf("404: code=%d stderr=%q", code, stderr)
	}
}

// A daemon that accepts every poll and never answers: each poll ends on the
// client's attempt timer (ErrNoAnswer, fired here by the fake clock), and
// the third in a row is exit 20 (spec §9.1) — before the --wait bound.
func TestRelayCmd_WaitThreeHungPollsExit20(t *testing.T) {
	clock := newLeadClock()
	d := &fakeRelayDaemon{openUntil: 1 << 30, pollDelay: time.Hour}
	d.onPoll = func(int) { clock.fireNext() }
	// --wait 2s bounds a regression (hung polls no longer counted) to a 2 s
	// red instead of the 9 min default.
	code, stdout, stderr := driveRelayWith(t, context.Background(), d, []daemonclient.Option{clock.opt()}, "wait", "req-1", "--wait", "2s")
	if code != ExitUnavailable || stdout != "" || !strings.Contains(stderr, "daemon 沒有回應") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	paths, _ := d.snapshot()
	polls := 0
	for _, p := range paths {
		if strings.HasPrefix(p, "GET /api/relay/wait/") {
			polls++
		}
	}
	if polls != 3 {
		t.Fatalf("%d polls, want 3: %v", polls, paths)
	}
}

// A cancelled ctx (SIGINT) ends a wait with exit 12 and nothing on stdout;
// the request is left to the daemon's lease.
func TestRelayCmd_WaitCancelledCtxExit12(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	d := &fakeRelayDaemon{openUntil: 1 << 30, pollDelay: time.Hour}
	d.onPoll = func(int) { cancel() }
	code, stdout, stderr := driveRelay(t, ctx, d, "wait", "req-1")
	if code != ExitCancelled || stdout != "" || !strings.Contains(stderr, "等待已中斷") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestRelayCmd_HelloSelfReportOp(t *testing.T) {
	d := &fakeRelayDaemon{reportStatus: http.StatusOK, reportBody: team.RelayOp{ID: "op-1", State: team.RelayCleared, NewSessionID: "sid-new"}}
	code, stdout, _ := driveRelay(t, context.Background(), d, "hello", "--session", "sid-1", "--version", "1")
	if code != ExitOK || !strings.Contains(stdout, `"self_relay":"on"`) {
		t.Fatalf("hello: code=%d stdout=%q", code, stdout)
	}
	code, stdout, _ = driveRelay(t, context.Background(), d, "self", "off", "--session", "sid-1")
	if code != ExitOK || !strings.Contains(stdout, `"self_relay":"paused"`) {
		t.Fatalf("self off: code=%d stdout=%q", code, stdout)
	}
	code, stdout, _ = driveRelay(t, context.Background(), d, "report", "op-1", "cleared", "--new-session", "sid-new")
	if code != ExitOK || !strings.Contains(stdout, `"new_session_id":"sid-new"`) {
		t.Fatalf("report cleared: code=%d stdout=%q", code, stdout)
	}
	_, bodies := d.snapshot()
	var sent team.RelayReportRequest
	_ = json.Unmarshal([]byte(bodies[len(bodies)-1]), &sent)
	if sent.State != team.RelayCleared || sent.NewSessionID != "sid-new" {
		t.Fatalf("sent report = %+v", sent)
	}
	bad := &fakeRelayDaemon{reportStatus: http.StatusConflict, reportBody: team.APIError{Error: team.ErrBadTransition, Detail: "state done does not lead to writing", Op: &team.RelayOp{ID: "op-1", State: team.RelayDone}}}
	code, stdout, stderr := driveRelay(t, context.Background(), bad, "report", "op-1", "writing")
	if code != ExitRefused || !strings.Contains(stderr, team.ErrBadTransition) || !strings.Contains(stdout, `"state":"done"`) {
		t.Fatalf("bad_transition: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if toks := strings.Fields(stderr); len(toks) == 0 || toks[len(toks)-1] != team.ErrBadTransition {
		t.Fatalf("bad_transition: the code must be the last stderr token, got %q", stderr)
	}
	code, stdout, _ = driveRelay(t, context.Background(), d, "op", "op-1")
	if code != ExitOK || !strings.Contains(stdout, `"id":"op-1"`) {
		t.Fatalf("op: code=%d stdout=%q", code, stdout)
	}
	// Every request carried the token.
	paths, _ := d.snapshot()
	if len(paths) == 0 {
		t.Fatal("no requests recorded")
	}
}

// A cleared whose new session is not yet in the registry is 503 not_ready
// (P5a-2b codex R2): the client treats it as a restart signal and retries
// through its grace; when the daemon never gets ready the report is exit 20
// with daemon_unavailable as the last stderr token — the mod's re-send
// class, not a usage or API error.
func TestRelayCmd_ReportNotReady503IsExit20(t *testing.T) {
	d := &fakeRelayDaemon{reportStatus: http.StatusServiceUnavailable, reportBody: team.APIError{Error: team.ErrNotReady, Detail: "new session not registered yet"}}
	code, stdout, stderr := driveRelay(t, context.Background(), d, "report", "op-1", "cleared", "--new-session", "sid-new")
	if code != ExitUnavailable || stdout != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if toks := strings.Fields(stderr); len(toks) == 0 || toks[len(toks)-1] != "daemon_unavailable" {
		t.Fatalf("daemon_unavailable must be the last stderr token, got %q", stderr)
	}
	paths, _ := d.snapshot()
	reports := 0
	for _, p := range paths {
		if strings.HasSuffix(p, "/report") {
			reports++
		}
	}
	if reports < 2 {
		t.Fatalf("a 503 not_ready must be retried, got %d report(s): %v", reports, paths)
	}
}

// Spec §8.8: `pdx relay prompts` GETs /api/relay/prompts and prints its
// JSON on ONE line (the mod JSON.parses stdout), exit 0.
func TestRelayPrompts_PrintsOneJSONLine(t *testing.T) {
	d := &fakeRelayDaemon{}
	code, stdout, stderr := driveRelay(t, context.Background(), d, "prompts")
	if code != ExitOK || stderr != "" {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
	if strings.Count(stdout, "\n") != 1 || !strings.HasSuffix(stdout, "\n") {
		t.Fatalf("stdout must be one line: %q", stdout)
	}
	var got team.RelayPrompts
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("stdout=%q err=%v", stdout, err)
	}
	if want := team.NewRelayPrompts(team.RelayPromptBodies{Write: "寫 {{path}}\n<&>"}); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
	// The client reads /api/health (boot id) first; then one GET.
	if paths, _ := d.snapshot(); len(paths) == 0 || paths[len(paths)-1] != "GET /api/relay/prompts" || strings.Count(strings.Join(paths, ","), "prompts") != 1 {
		t.Fatalf("requests = %v", paths)
	}
}

// A daemon from before P9a-1 has no route: plain 404 → 21 (spec §8.8).
func TestRelayPrompts_Plain404Is21(t *testing.T) {
	code, stdout, stderr := driveRelay(t, context.Background(), http.NotFoundHandler(), "prompts")
	if code != ExitUnsupported || stdout != "" || !strings.Contains(stderr, "unsupported") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

// Any other API error is exit 1 with the code as the last stderr token.
func TestRelayPrompts_ServerErrorIs1(t *testing.T) {
	d := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(team.APIError{Error: "storage_error", Detail: "host config relay prompts unreadable"})
	})
	code, stdout, stderr := driveRelay(t, context.Background(), d, "prompts")
	if toks := strings.Fields(stderr); code != ExitError || stdout != "" || len(toks) == 0 || toks[len(toks)-1] != "storage_error" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

// A daemon that stays unreachable through the grace (the fake clock makes
// it instant) is exit 20 with daemon_unavailable as the last stderr token.
func TestRelayPrompts_UnreachableIs20(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	cfgPath := writeTestConfig(t, srv.URL, "admin-tok")
	srv.Close()
	var stdout, stderr bytes.Buffer
	code := runRelayCmd(context.Background(), []string{"prompts", "--config", cfgPath}, &stdout, &stderr, leadClockOpt())
	if toks := strings.Fields(stderr.String()); code != ExitUnavailable || stdout.Len() != 0 || len(toks) == 0 || toks[len(toks)-1] != "daemon_unavailable" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

// An extra argument or an unknown flag is a usage error (exit 2) that
// reaches no daemon.
func TestRelayPrompts_ExtraArgIs2(t *testing.T) {
	d := &fakeRelayDaemon{}
	for _, args := range [][]string{{"prompts", "write"}, {"prompts", "--session", "s"}} {
		code, stdout, stderr := driveRelay(t, context.Background(), d, args...)
		if code != ExitUsage || stdout != "" || !strings.Contains(stderr, "pdx relay prompts [--config <path>]") {
			t.Fatalf("%v: code=%d stdout=%q stderr=%q", args, code, stdout, stderr)
		}
	}
	if paths, _ := d.snapshot(); len(paths) != 0 {
		t.Fatalf("usage errors must not reach the daemon: %v", paths)
	}
}

// PR #1726 attacker A-2: when the --wait bound fires while a long poll is
// in flight, the outcome must be the daemon's state, not the scheduler's:
// one short read (wait=0) decides. A row that was approved in the meantime
// ends the wait approved (exit 0, state approved); one still open prints
// the open row as before.
func TestRelayCmd_WaitBoundReadsTheFinalState(t *testing.T) {
	approved := &fakeRelayDaemon{pollDelay: 10 * time.Second, final: team.Approval{Kind: team.KindSelfRelay, State: team.StateApproved}}
	code, stdout, stderr := driveRelayWith(t, context.Background(), approved, nil, "wait", "req-1", "--wait", "300ms")
	var ap team.Approval
	if err := json.Unmarshal([]byte(stdout), &ap); err != nil || code != ExitOK || ap.State != team.StateApproved {
		t.Fatalf("approved in flight at the bound: code=%d stdout=%q stderr=%q err=%v", code, stdout, stderr, err)
	}
	paths, _ := approved.snapshot()
	if len(paths) < 2 || !strings.Contains(paths[len(paths)-1], "wait=0") {
		t.Fatalf("the bound must end with one short read: %v", paths)
	}

	open := &fakeRelayDaemon{pollDelay: 10 * time.Second}
	code, stdout, _ = driveRelayWith(t, context.Background(), open, nil, "wait", "req-1", "--wait", "300ms")
	if err := json.Unmarshal([]byte(stdout), &ap); err != nil || code != ExitOK || ap.State != team.StateOpen || ap.ID != "req-1" {
		t.Fatalf("still open at the bound: code=%d stdout=%q err=%v", code, stdout, err)
	}
}
