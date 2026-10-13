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
	unansweredAt int64         // >0: the open polls carry it (a final card that went unanswered, RT-2a1)
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
		_ = json.NewEncoder(w).Encode(team.RelayHelloResponse{OK: true, Role: "none", SelfRelay: "on", Threshold: 70, MinGrowth: 20000, IdleMinutes: 40, IdleThreshold: 40})
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
			_ = json.NewEncoder(w).Encode(team.Approval{ID: id, Kind: team.KindSelfRelay, State: team.StateOpen, UnansweredAt: f.unansweredAt})
			return
		}
		ap := f.final
		ap.ID = id
		_ = json.NewEncoder(w).Encode(ap)
	case r.URL.Path == "/api/relay/compacted":
		_ = json.NewEncoder(w).Encode(team.RelayCompactedResponse{Noticed: strings.Contains(b.String(), `"auto"`) || strings.Contains(b.String(), `"idle"`)})
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
		// P7-2: compacted needs both flags and a known trigger
		{"compacted"}, {"compacted", "--session", "s"}, {"compacted", "--trigger", "auto"}, {"compacted", "--session", "s", "--trigger", "sideways"},
		{"compacted", "--session", "s", "--trigger", "auto", "extra"},
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
	}{{team.ErrSelfRelayOff, false}, {team.ErrSelfRelayPaused, false}, {team.ErrMemberRelayIsLeads, false}, {team.ErrRelayOpen, true},
		{team.ErrPausedNoQuota, false}} { // UP-2a's 409: exit 13, no op on stdout, the code last
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

// P7-2: the compacted call posts the session and the trigger and prints the daemon's answer.
func TestRelayCmd_Compacted(t *testing.T) {
	d := &fakeRelayDaemon{}
	code, stdout, stderr := driveRelay(t, context.Background(), d, "compacted", "--session", "sid-1", "--trigger", "auto")
	if code != ExitOK || strings.TrimSpace(stdout) != `{"noticed":true}` {
		t.Fatalf("auto: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	code, stdout, _ = driveRelay(t, context.Background(), d, "compacted", "--session", "sid-1", "--trigger", "manual")
	if code != ExitOK || strings.TrimSpace(stdout) != `{"noticed":false}` {
		t.Fatalf("manual: code=%d stdout=%q", code, stdout)
	}
	paths, bodies := d.snapshot()
	var sent team.RelayCompactedRequest
	for i, p := range paths {
		if strings.HasSuffix(p, "/api/relay/compacted") && sent.Trigger == "" {
			_ = json.Unmarshal([]byte(bodies[i]), &sent)
		}
	}
	if sent.SessionID != "sid-1" || sent.Trigger != "auto" {
		t.Fatalf("sent = %+v", sent)
	}
}

// MR-1: `pdx relay begin --manual` is a person's /relay (the mod sends it); the flag is the request's `manual`, and
// its absence is false. Mutation gate: the flag not read into the request → sent.Manual stays false (red).
func TestRelayCmd_BeginManualFlagReachesTheRequest(t *testing.T) {
	pct := 72.4
	for _, tc := range []struct {
		extra []string
		want  bool
	}{{nil, false}, {[]string{"--manual"}, true}} {
		d := &fakeRelayDaemon{beginStatus: 201, beginBody: team.RelayBeginResponse{Op: team.RelayOp{ID: "op-1", State: team.RelayAwaitingApproval, UsedPercentage: &pct}, RequestID: "req-1"}}
		args := append([]string{"begin", "--self", "--session", "sid-1", "--used", "72", "--window", "200000"}, tc.extra...)
		if code, _, stderr := driveRelay(t, context.Background(), d, args...); code != ExitOK {
			t.Fatalf("%v: code=%d stderr=%q", args, code, stderr)
		}
		_, bodies := d.snapshot()
		var sent team.RelayBeginRequest
		_ = json.Unmarshal([]byte(bodies[len(bodies)-1]), &sent)
		if sent.Manual != tc.want {
			t.Fatalf("%v: sent manual = %v, want %v", args, sent.Manual, tc.want)
		}
	}
}

// IR-1a: `relay hello` prints the daemon's idle numbers (the mod's capability check, idle relay D9).
func TestRelayCmd_HelloPrintsIdleNumbers(t *testing.T) {
	code, stdout, stderr := driveRelay(t, context.Background(), &fakeRelayDaemon{}, "hello", "--session", "sid-1", "--version", "1")
	if code != ExitOK || !strings.Contains(stdout, `"idle_minutes":40`) || !strings.Contains(stdout, `"idle_threshold":40`) {
		t.Fatalf("hello: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

// IR-1a: `relay begin --reason idle --idle-minutes N` reaches the wire; a bad combination is a usage error (exit 2) before
// any request. Mutation gate: drop the validation → the bad cases reach the daemon / exit 0 (red).
// `--reask <uuid>` reaches the begin body as reask_id (UP-3b); a value that is not a UUID is a usage error that never reaches the daemon.
// Mutation gate: do not copy the flag into the request → the daemon sees no reask_id (red).
func TestRelayCmd_BeginReaskReachesTheWireAndMustBeAUUID(t *testing.T) {
	pct := 55.0
	base := []string{"begin", "--self", "--session", "sid-1", "--used", "55", "--window", "1000000"}
	d := &fakeRelayDaemon{beginStatus: 201, beginBody: team.RelayBeginResponse{Op: team.RelayOp{ID: "op-1", State: team.RelayAwaitingApproval, UsedPercentage: &pct}, RequestID: "req-1"}}
	id := uuid.NewString()
	if code, _, stderr := driveRelay(t, context.Background(), d, append(append([]string{}, base...), "--reask", id)...); code != ExitOK {
		t.Fatalf("reask begin: code=%d stderr=%q", code, stderr)
	}
	_, bodies := d.snapshot()
	var sent team.RelayBeginRequest
	_ = json.Unmarshal([]byte(bodies[len(bodies)-1]), &sent)
	if sent.ReaskID != id {
		t.Fatalf("sent = %+v, want reask_id %s", sent, id)
	}
	bad := &fakeRelayDaemon{}
	if code, _, _ := driveRelay(t, context.Background(), bad, append(append([]string{}, base...), "--reask", "not-a-uuid")...); code != ExitUsage {
		t.Fatalf("code = %d, want %d", code, ExitUsage)
	}
	if paths, _ := bad.snapshot(); len(paths) != 0 {
		t.Fatalf("a bad --reask reached the daemon: %v", paths)
	}
}

func TestRelayCmd_BeginIdleFlagsReachTheWireAndAreValidated(t *testing.T) {
	pct := 55.0
	base := []string{"begin", "--self", "--session", "sid-1", "--used", "55", "--window", "1000000"}
	d := &fakeRelayDaemon{beginStatus: 201, beginBody: team.RelayBeginResponse{Op: team.RelayOp{ID: "op-1", State: team.RelayAwaitingApproval, UsedPercentage: &pct}, RequestID: "req-1"}}
	args := append(append([]string{}, base...), "--reason", "idle", "--idle-minutes", "40")
	if code, _, stderr := driveRelay(t, context.Background(), d, args...); code != ExitOK {
		t.Fatalf("idle begin: code=%d stderr=%q", code, stderr)
	}
	_, bodies := d.snapshot()
	var sent team.RelayBeginRequest
	_ = json.Unmarshal([]byte(bodies[len(bodies)-1]), &sent)
	if sent.Reason != "idle" || sent.IdleMinutes != 40 {
		t.Fatalf("sent = %+v, want reason idle, 40 minutes", sent)
	}
	for _, extra := range [][]string{
		{"--reason", "idle"},                                     // minutes missing
		{"--reason", "idle", "--idle-minutes", "0"},              // below 1
		{"--reason", "idle", "--idle-minutes", "60"},             // above 59
		{"--reason", "lazy", "--idle-minutes", "40"},             // unknown reason
		{"--idle-minutes", "40"},                                 // minutes without a reason
		{"--reason", "idle", "--idle-minutes", "40", "--manual"}, // idle is never a /relay
		{"--reason", "idle", "--idle-minutes", "x"},              // not a number
	} {
		bad := &fakeRelayDaemon{}
		if code, _, _ := driveRelay(t, context.Background(), bad, append(append([]string{}, base...), extra...)...); code != ExitUsage {
			t.Fatalf("%v: code = %d, want %d", extra, code, ExitUsage)
		}
		if paths, _ := bad.snapshot(); len(paths) != 0 {
			t.Fatalf("%v reached the daemon: %v", extra, paths)
		}
	}
}

// IR-1a: `report <op> cancelled --error resumed` carries the reason.
func TestRelayCmd_ReportResumedReachesTheWire(t *testing.T) {
	d := &fakeRelayDaemon{reportStatus: http.StatusOK, reportBody: team.RelayOp{ID: "op-1", State: team.RelayCancelled, Reason: team.RelayReasonResumed}}
	code, stdout, stderr := driveRelay(t, context.Background(), d, "report", "op-1", "cancelled", "--error", "resumed")
	if code != ExitOK || !strings.Contains(stdout, `"reason":"resumed"`) {
		t.Fatalf("report resumed: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	_, bodies := d.snapshot()
	var sent team.RelayReportRequest
	_ = json.Unmarshal([]byte(bodies[len(bodies)-1]), &sent)
	if sent.State != team.RelayCancelled || sent.Error != team.RelayReasonResumed {
		t.Fatalf("sent = %+v", sent)
	}
}

// IR-1b: `--trigger idle` reaches the wire; a trigger the daemon does not know is still a usage error (exit 2).
func TestRelayCmd_CompactedIdleReachesTheWire(t *testing.T) {
	d := &fakeRelayDaemon{}
	code, stdout, stderr := driveRelay(t, context.Background(), d, "compacted", "--session", "sid-1", "--trigger", "idle")
	if code != ExitOK || strings.TrimSpace(stdout) != `{"noticed":true}` {
		t.Fatalf("idle: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	paths, bodies := d.snapshot()
	var sent team.RelayCompactedRequest
	for i, p := range paths {
		if strings.HasSuffix(p, "/api/relay/compacted") {
			_ = json.Unmarshal([]byte(bodies[i]), &sent)
		}
	}
	if sent.SessionID != "sid-1" || sent.Trigger != "idle" {
		t.Fatalf("sent = %+v", sent)
	}
	if code, _, _ := driveRelay(t, context.Background(), d, "compacted", "--session", "sid-1", "--trigger", "sideways"); code != ExitUsage {
		t.Fatalf("sideways = %d, want %d", code, ExitUsage)
	}
}

// Codex attack: `resumed` is a cancelled reason only; failed / writing carrying it is a usage error before any request.
func TestRelayCmd_ResumedOnlyWithCancelled(t *testing.T) {
	for _, st := range []string{"failed", "writing", "done"} {
		d := &fakeRelayDaemon{}
		if code, _, _ := driveRelay(t, context.Background(), d, "report", "op-1", st, "--error", "resumed"); code != ExitUsage {
			t.Fatalf("%s --error resumed: code = %d, want %d", st, code, ExitUsage)
		}
		if paths, _ := d.snapshot(); len(paths) != 0 {
			t.Fatalf("%s --error resumed reached the daemon: %v", st, paths)
		}
	}
}

// `pdx relay wait --return-unanswered` returns at once, exit 0, when a poll shows an open row that went unanswered (RT-2c T27);
// without the flag the wait goes on. Mutation gates: ignore the flag → the first case waits for the final (red); return without
// the flag → the second case returns early (red).
func TestRelayCmd_WaitReturnUnanswered(t *testing.T) {
	d := &fakeRelayDaemon{openUntil: 3, unansweredAt: 1234, final: team.Approval{Kind: team.KindSelfRelay, State: team.StateApproved}}
	code, out, _ := driveRelay(t, context.Background(), d, "wait", "req-1", "--return-unanswered")
	if code != ExitOK || !strings.Contains(out, `"unanswered_at":1234`) || !strings.Contains(out, `"state":"open"`) || d.polls != 1 {
		t.Fatalf("with the flag: exit %d out=%q polls=%d, want one poll and the open row", code, out, d.polls)
	}
	d = &fakeRelayDaemon{openUntil: 3, unansweredAt: 1234, final: team.Approval{Kind: team.KindSelfRelay, State: team.StateApproved}}
	code, out, _ = driveRelay(t, context.Background(), d, "wait", "req-1")
	if code != ExitOK || !strings.Contains(out, `"state":"approved"`) || d.polls != 4 {
		t.Fatalf("without the flag: exit %d out=%q polls=%d, want the approval after 4 polls", code, out, d.polls)
	}
}
