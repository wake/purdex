package main

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wake/purdex/cmd/pdx/daemonclient"
	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/team"
)

// fakeHookDaemon answers /api/health and POST /api/hooks/decide with a
// fixed response, recording every decide body and the bearer token.
type fakeHookDaemon struct {
	mu      sync.Mutex
	decides []team.HookDecideRequest
	auths   []string
	resp    team.HookDecideResponse
	status  int
	hang    bool
}

func newFakeHookDaemon(resp team.HookDecideResponse) *fakeHookDaemon {
	return &fakeHookDaemon{resp: resp, status: http.StatusOK}
}

func (f *fakeHookDaemon) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.URL.Path == "/api/health" {
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "boot_id": "b1"})
		return
	}
	if r.Method != http.MethodPost || r.URL.Path != "/api/hooks/decide" {
		http.NotFound(w, r)
		return
	}
	var req team.HookDecideRequest
	json.NewDecoder(r.Body).Decode(&req)
	f.mu.Lock()
	f.decides = append(f.decides, req)
	f.auths = append(f.auths, r.Header.Get("Authorization"))
	resp, status, hang := f.resp, f.status, f.hang
	f.mu.Unlock()
	if hang {
		<-r.Context().Done()
		return
	}
	w.WriteHeader(status)
	if status == http.StatusServiceUnavailable {
		json.NewEncoder(w).Encode(team.APIError{Error: team.ErrNotReady}) // the daemon is stopping
		return
	}
	json.NewEncoder(w).Encode(resp)
}

func (f *fakeHookDaemon) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.decides)
}

// ccPreToolUseStdin is a CC 2.1.291 PreToolUse payload (the fields the
// decision reads; the real payload has more).
const ccPreToolUseStdin = `{"session_id":"cc-sid-1","transcript_path":"/t.jsonl","cwd":"/w","permission_mode":"bypassPermissions","hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"ls -la","description":"List"},"tool_use_id":"toolu_01ABC"}`

// touchHookLock writes the flag file hookDecision gates on.
func touchHookLock(t *testing.T, dataDir, agent, sid string) string {
	t.Helper()
	p := team.HookLockPath(dataDir, agent, sid)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func hookInput(dataDir, base, agent, purdexName, raw string, opts ...daemonclient.Option) hookDecideInput {
	return hookDecideInput{DataDir: dataDir, Base: base, Token: "tok", Agent: agent, PurdexName: purdexName, Raw: []byte(raw), ClientOpts: opts}
}

// Spec §15 "no flag ⇒ no daemon call and empty stdout". Mutation gate:
// drop the HookLockExists check in hookDecision → red (the daemon is called).
func TestHookDecision_NoFlagMeansNoCallAndNoOutput(t *testing.T) {
	d := newFakeHookDaemon(team.HookDecideResponse{Decision: "deny", Reason: "r"})
	srv := httptest.NewServer(d)
	defer srv.Close()
	dataDir := t.TempDir()
	for _, ev := range []string{"PdxPreToolUse", "PdxPermissionRequest"} {
		if out, asked := hookDecision(context.Background(), hookInput(dataDir, srv.URL, "cc", ev, ccPreToolUseStdin)); out != nil || asked {
			t.Fatalf("%s without a flag printed %q (asked=%v)", ev, out, asked)
		}
	}
	// A flag for another session, or the other agent, is not this one's.
	touchHookLock(t, dataDir, "cc", "cc-sid-other")
	touchHookLock(t, dataDir, "codex", "cc-sid-1")
	if out, asked := hookDecision(context.Background(), hookInput(dataDir, srv.URL, "cc", "PdxPreToolUse", ccPreToolUseStdin)); out != nil || asked {
		t.Fatalf("other flags printed %q (asked=%v)", out, asked)
	}
	if n := d.calls(); n != 0 {
		t.Fatalf("daemon was called %d times without this session's flag; want 0", n)
	}
}

// Spec §15 "flag + open lead request ⇒ PreToolUse deny with the reason,
// PermissionRequest {}": the daemon's deny becomes the agent's JSON, with
// the reason verbatim; the same answer on PermissionRequest prints nothing
// (the PreToolUse deny already stopped the call). The request carries the
// stdin's fields and the whole stdin as raw, under the bearer token.
func TestHookDecision_FlagAndDenyPrintsPreToolUseJSONOnly(t *testing.T) {
	reason := "lead 申請等待核准中（11111111-2222-4333-8444-555555555555），核准或拒絕前這個 session 不能執行工具；請在 Purdex 介面處理"
	d := newFakeHookDaemon(team.HookDecideResponse{Decision: "deny", Reason: reason, Lock: team.HookLockLeadRequest, ID: "11111111-2222-4333-8444-555555555555"})
	srv := httptest.NewServer(d)
	defer srv.Close()
	dataDir := t.TempDir()
	touchHookLock(t, dataDir, "cc", "cc-sid-1")

	out, _ := hookDecision(context.Background(), hookInput(dataDir, srv.URL, "cc", "PdxPreToolUse", ccPreToolUseStdin))
	want := `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"` + reason + `"}}` + "\n"
	if string(out) != want {
		t.Fatalf("PreToolUse out = %q\nwant %q", out, want)
	}
	if out, _ := hookDecision(context.Background(), hookInput(dataDir, srv.URL, "cc", "PdxPermissionRequest", ccPreToolUseStdin)); out != nil {
		t.Fatalf("PermissionRequest printed %q, want nothing", out)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.decides) != 2 {
		t.Fatalf("decides = %d, want 2", len(d.decides))
	}
	got := d.decides[0]
	if got.Agent != "cc" || got.Event != "PreToolUse" || got.SessionID != "cc-sid-1" || got.ToolName != "Bash" ||
		got.ToolUseID != "toolu_01ABC" || string(got.ToolInput) != `{"command":"ls -la","description":"List"}` ||
		string(got.Raw) != ccPreToolUseStdin {
		t.Fatalf("decide body = %+v", got)
	}
	if d.decides[1].Event != "PermissionRequest" {
		t.Fatalf("second event = %q", d.decides[1].Event)
	}
	for _, a := range d.auths {
		if a != "Bearer tok" {
			t.Fatalf("auth = %q", a)
		}
	}
}

// A Write of a large file: the stdin is far over 64 KiB, the request goes
// out with the ids only (no tool_input, no raw) and the deny still lands.
// Without the cap a > 1 MiB body would be a 400 from the daemon — no
// decision, the lock bypassed for the biggest writes exactly.
func TestHookDecision_LargeStdinSendsIdsOnly(t *testing.T) {
	d := newFakeHookDaemon(team.HookDecideResponse{Decision: "deny", Reason: "r"})
	srv := httptest.NewServer(d)
	defer srv.Close()
	dataDir := t.TempDir()
	touchHookLock(t, dataDir, "cc", "cc-sid-1")
	big := `{"session_id":"cc-sid-1","hook_event_name":"PreToolUse","tool_name":"Write","tool_use_id":"toolu_big","tool_input":{"file_path":"/w/big.txt","content":"` + strings.Repeat("x", 2<<20) + `"}}`
	out, _ := hookDecision(context.Background(), hookInput(dataDir, srv.URL, "cc", "PdxPreToolUse", big))
	if string(out) != `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"r"}}`+"\n" {
		t.Fatalf("large stdin out = %q", out)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	got := d.decides[0]
	if got.SessionID != "cc-sid-1" || got.ToolName != "Write" || got.ToolUseID != "toolu_big" || len(got.ToolInput) != 0 || len(got.Raw) != 0 {
		t.Fatalf("decide body must carry the ids only: session=%q tool=%q use=%q input=%d raw=%d", got.SessionID, got.ToolName, got.ToolUseID, len(got.ToolInput), len(got.Raw))
	}
}

// The same path with a Codex payload (the repo's 0.153.4 fixture) and
// --agent codex: the names match, the JSON is identical.
func TestHookDecision_CodexFixtureSameShape(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "internal", "agent", "codex", "testdata", "codex-0.153.4-payloads", "PdxPreToolUse.json"))
	if err != nil {
		t.Fatal(err)
	}
	d := newFakeHookDaemon(team.HookDecideResponse{Decision: "deny", Reason: "r"})
	srv := httptest.NewServer(d)
	defer srv.Close()
	dataDir := t.TempDir()
	touchHookLock(t, dataDir, "codex", "01a00000-0000-7000-8000-000000000001")
	out, _ := hookDecision(context.Background(), hookInput(dataDir, srv.URL, "codex", "PdxPreToolUse", string(raw)))
	if string(out) != `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"r"}}`+"\n" {
		t.Fatalf("codex out = %q", out)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.decides[0].Agent != "codex" || d.decides[0].SessionID != "01a00000-0000-7000-8000-000000000001" || d.decides[0].ToolUseID != "call_example0001" {
		t.Fatalf("decide body = %+v", d.decides[0])
	}
}

// Spec §15 "flag + daemon unreachable ⇒ exit 0, empty stdout, within 5 s":
// nobody listens on the port; the client retries inside its 5 s grace and
// gives up; nothing is printed. The clock is fake, so the test measures
// the grace, not wall time.
func TestHookDecision_UnreachableDaemonIsSilentWithinFiveSeconds(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close() // nothing listens now
	clock := newLeadClock()
	start := clock.now()
	dataDir := t.TempDir()
	touchHookLock(t, dataDir, "cc", "cc-sid-1")
	wall := time.Now()
	out, asked := hookDecision(context.Background(), hookInput(dataDir, "http://"+addr, "cc", "PdxPreToolUse", ccPreToolUseStdin, clock.opt()))
	if out != nil || !asked {
		t.Fatalf("unreachable daemon printed %q (asked=%v, want true: the flag was there)", out, asked)
	}
	if got := clock.now().Sub(start); got != hookDecideGrace {
		t.Fatalf("gave up after %v of fake time, want exactly %v", got, hookDecideGrace)
	}
	if real := time.Since(wall); real > 2*time.Second {
		t.Fatalf("took %v of wall time; the clock must be the fake one", real)
	}
}

// A daemon that accepts and never answers, a 404 (older daemon), a 503
// (restarting past the grace) and a {} all print nothing.
func TestHookDecision_ErrorsAndEmptyAnswersPrintNothing(t *testing.T) {
	dataDir := t.TempDir()
	touchHookLock(t, dataDir, "cc", "cc-sid-1")
	t.Run("silent daemon ends at the attempt timeout", func(t *testing.T) {
		clock := newLeadClock()
		d := newFakeHookDaemon(team.HookDecideResponse{})
		d.hang = true
		srv := httptest.NewServer(d)
		defer srv.Close()
		done := make(chan []byte, 1)
		go func() {
			out, _ := hookDecision(context.Background(), hookInput(dataDir, srv.URL, "cc", "PdxPreToolUse", ccPreToolUseStdin, clock.opt()))
			done <- out
		}()
		deadline := time.Now().Add(5 * time.Second)
		for d.calls() == 0 && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
		clock.fireNext() // the 5 s attempt timer
		select {
		case out := <-done:
			if out != nil {
				t.Fatalf("silent daemon printed %q", out)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("hookDecision did not return after the attempt timer fired")
		}
	})
	t.Run("404 older daemon", func(t *testing.T) {
		srv := httptest.NewServer(http.NotFoundHandler())
		defer srv.Close()
		if out, _ := hookDecision(context.Background(), hookInput(dataDir, srv.URL, "cc", "PdxPreToolUse", ccPreToolUseStdin)); out != nil {
			t.Fatalf("404 printed %q", out)
		}
	})
	t.Run("503 not_ready is retried through the 5 s grace, then nothing", func(t *testing.T) {
		clock := newLeadClock()
		start := clock.now()
		d := newFakeHookDaemon(team.HookDecideResponse{})
		d.status = http.StatusServiceUnavailable
		srv := httptest.NewServer(d)
		defer srv.Close()
		if out, _ := hookDecision(context.Background(), hookInput(dataDir, srv.URL, "cc", "PdxPreToolUse", ccPreToolUseStdin, clock.opt())); out != nil {
			t.Fatalf("503 printed %q", out)
		}
		if got := clock.now().Sub(start); got != hookDecideGrace {
			t.Fatalf("gave up after %v, want the 5 s grace exactly", got)
		}
		if d.calls() < 2 {
			t.Fatalf("decide calls = %d, want retries inside the grace", d.calls())
		}
	})
	t.Run("empty answer", func(t *testing.T) {
		d := newFakeHookDaemon(team.HookDecideResponse{})
		srv := httptest.NewServer(d)
		defer srv.Close()
		if out, _ := hookDecision(context.Background(), hookInput(dataDir, srv.URL, "cc", "PdxPreToolUse", ccPreToolUseStdin)); out != nil {
			t.Fatalf("{} printed %q", out)
		}
	})
	t.Run("other events and id-less stdin never call", func(t *testing.T) {
		d := newFakeHookDaemon(team.HookDecideResponse{Decision: "deny", Reason: "r"})
		srv := httptest.NewServer(d)
		defer srv.Close()
		for _, c := range []struct{ ev, raw string }{
			{"PdxPostToolUse", ccPreToolUseStdin},
			{"PdxUserPromptSubmit", ccPreToolUseStdin},
			{"PdxPreToolUse", `{"hook_event_name":"PreToolUse"}`},
			{"PdxPreToolUse", `not json`},
			{"PdxPreToolUse", `{"session_id":"../cc-sid-1"}`},
		} {
			if out, _ := hookDecision(context.Background(), hookInput(dataDir, srv.URL, "cc", c.ev, c.raw)); out != nil {
				t.Fatalf("%s %q printed %q", c.ev, c.raw, out)
			}
		}
		if d.calls() != 0 {
			t.Fatalf("daemon called %d times, want 0", d.calls())
		}
	})
}

// End to end through runHook with the production seams: the event POST
// and the decision run concurrently under one budget, the deny is printed
// on stdout, and runHook returns (exit 0 is main's). Then the same with the
// daemon unreachable AND the event POST hanging (the stub blocks until its
// ctx ends, as a real POST to a half-dead daemon would until its own 2 s
// timeout): runHook returns with nothing on stdout, having spent exactly
// the 5 s grace of fake time — not 5 s + the event POST. Mutation gates:
// an os.Exit(1) on the error path kills the test binary → red; running the
// event POST and the decision sequentially → the hanging POST never ends
// before the decision starts → the test's 5 s wall deadline → red.
func TestRunHook_DecisionPathEndToEnd(t *testing.T) {
	origInfo, origResolve, origPost, origLoad := queryTmuxSessionInfoFn, resolveHookProvenanceFn, postHookEventFn, loadConfigFn
	origStdin, origStdout, origOpts, origAfter := os.Stdin, os.Stdout, hookClientOpts, hookAfterFn
	t.Cleanup(func() {
		queryTmuxSessionInfoFn, resolveHookProvenanceFn, postHookEventFn, loadConfigFn = origInfo, origResolve, origPost, origLoad
		os.Stdin, os.Stdout, hookClientOpts, hookAfterFn = origStdin, origStdout, origOpts, origAfter
	})
	queryTmuxSessionInfoFn = func() (string, string) { return "$1", "work" }
	resolveHookProvenanceFn = func() hookProvenance { return hookProvenance{TmuxPaneID: "%5", SenderPID: 42} }
	var postMu sync.Mutex
	var posted []hookPayload
	var postCtxEnded []bool // per call: did the stub return because its ctx ended?
	hangPost := false
	slowPost := time.Duration(0) // > 0: the stub takes this long unless its ctx ends first
	postHookEventFn = func(ctx context.Context, _ string, _ string, p hookPayload) error {
		postMu.Lock()
		posted = append(posted, p)
		hang, slow := hangPost, slowPost
		postMu.Unlock()
		ended := false
		if hang {
			<-ctx.Done()
			ended = true
		} else if slow > 0 {
			select {
			case <-ctx.Done():
				ended = true
			case <-time.After(slow):
			}
		}
		postMu.Lock()
		postCtxEnded = append(postCtxEnded, ended)
		postMu.Unlock()
		return nil
	}

	run := func(t *testing.T, base string, purdexName string) string {
		t.Helper()
		host, portStr, _ := net.SplitHostPort(strings.TrimPrefix(base, "http://"))
		var port int
		for _, c := range portStr {
			port = port*10 + int(c-'0')
		}
		dataDir := t.TempDir()
		touchHookLock(t, dataDir, "cc", "cc-sid-1")
		loadConfigFn = func(string) (config.Config, error) {
			return config.Config{Bind: host, Port: port, Token: "tok", DataDir: dataDir}, nil
		}
		in, inW, _ := os.Pipe()
		inW.WriteString(ccPreToolUseStdin)
		inW.Close()
		os.Stdin = in
		outR, outW, _ := os.Pipe()
		os.Stdout = outW
		runHook([]string{"--agent", "cc", purdexName})
		outW.Close()
		got, _ := io.ReadAll(outR)
		return string(got)
	}

	d := newFakeHookDaemon(team.HookDecideResponse{Decision: "deny", Reason: "r"})
	srv := httptest.NewServer(d)
	defer srv.Close()
	if got := run(t, srv.URL, "PdxPreToolUse"); got != `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"r"}}`+"\n" {
		t.Fatalf("stdout = %q", got)
	}
	postMu.Lock()
	if len(posted) != 1 || posted[0].PurdexName != "PdxPreToolUse" || string(posted[0].RawEvent) != ccPreToolUseStdin {
		t.Fatalf("the event POST must still be sent: %+v", posted)
	}
	postMu.Unlock()
	if d.calls() != 1 {
		t.Fatalf("decide calls = %d", d.calls())
	}

	// A fast decision must not cut the event POST short (R1 of PR #1697):
	// the daemon denies at once while the POST takes 150 ms; the POST must
	// still complete, and not because its ctx ended.
	postMu.Lock()
	slowPost = 150 * time.Millisecond
	postMu.Unlock()
	if got := run(t, srv.URL, "PdxPreToolUse"); !strings.Contains(got, `"permissionDecision":"deny"`) {
		t.Fatalf("slow POST: stdout = %q", got)
	}
	postMu.Lock()
	if len(posted) != 2 || len(postCtxEnded) != 2 || postCtxEnded[1] {
		t.Fatalf("a fast decision must not end the event POST: posts = %d, ctxEnded = %v", len(posted), postCtxEnded)
	}
	slowPost = 0
	postMu.Unlock()

	// Not a decision event: the event POST alone, and runHook waits for it
	// (a hook that returned before the POST ended would lose the event when
	// main exits). The stub returns at once here, so nothing to time.
	if got := run(t, srv.URL, "PdxPostToolUse"); got != "" {
		t.Fatalf("PostToolUse printed %q", got)
	}
	postMu.Lock()
	if len(posted) != 3 || d.calls() != 2 {
		t.Fatalf("PostToolUse: posts = %d (want 3), decides = %d (want 2)", len(posted), d.calls())
	}
	postMu.Unlock()

	// Daemon unreachable, event POST hanging until its ctx ends: one budget.
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	dead := ln.Addr().String()
	ln.Close()
	clock := newLeadClock()
	start := clock.now()
	hookClientOpts = []daemonclient.Option{clock.opt()}
	hookAfterFn = clock.afterFunc                 // the budget timer is the fake clock's too
	clock.onSleep = func(int) { clock.fireDue() } // and fires once the grace sleeps reach it, as a real one would
	postMu.Lock()
	hangPost = true
	postMu.Unlock()
	wall := time.Now()
	done := make(chan string, 1)
	go func() { done <- run(t, "http://"+dead, "PdxPreToolUse") }()
	select {
	case got := <-done:
		if got != "" {
			t.Fatalf("unreachable daemon: stdout = %q, want empty", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runHook did not return within 5 s of wall time: the event POST and the decision are not under one budget")
	}
	if got := clock.now().Sub(start); got != hookDecideGrace {
		t.Fatalf("spent %v of fake time, want exactly the %v budget (not budget + event POST)", got, hookDecideGrace)
	}
	if real := time.Since(wall); real > 2*time.Second {
		t.Fatalf("took %v of wall time; the clock must be the fake one", real)
	}
	postMu.Lock()
	defer postMu.Unlock()
	if len(posted) != 4 {
		t.Fatalf("event POST count = %d, want 4", len(posted))
	}
	if len(postCtxEnded) != 4 || !postCtxEnded[3] {
		t.Fatalf("the hanging event POST must have been ended by the budget ctx: %v", postCtxEnded)
	}
}

// A hook whose stdin never closes (started by hand, or an agent that died
// mid-write) is bounded by the same 5 s budget as everything else: the
// read ends with the budget, the payload is "{}", nothing is printed, and
// runHook returns — not 5 s of stdin plus another budget (PR #1697 A-2).
func TestRunHook_StdinThatNeverClosesIsInsideTheBudget(t *testing.T) {
	origInfo, origResolve, origPost, origLoad := queryTmuxSessionInfoFn, resolveHookProvenanceFn, postHookEventFn, loadConfigFn
	origStdin, origStdout, origAfter := os.Stdin, os.Stdout, hookAfterFn
	t.Cleanup(func() {
		queryTmuxSessionInfoFn, resolveHookProvenanceFn, postHookEventFn, loadConfigFn = origInfo, origResolve, origPost, origLoad
		os.Stdin, os.Stdout, hookAfterFn = origStdin, origStdout, origAfter
	})
	queryTmuxSessionInfoFn = func() (string, string) { return "$1", "work" }
	resolveHookProvenanceFn = func() hookProvenance { return hookProvenance{} }
	dataDir := t.TempDir()
	loadConfigFn = func(string) (config.Config, error) {
		return config.Config{Bind: "127.0.0.1", Port: 1, Token: "tok", DataDir: dataDir}, nil
	}
	var postMu sync.Mutex
	var posted []hookPayload
	postHookEventFn = func(ctx context.Context, _ string, _ string, p hookPayload) error {
		postMu.Lock()
		posted = append(posted, p)
		postMu.Unlock()
		return nil
	}
	clock := newLeadClock()
	hookAfterFn = clock.afterFunc

	in, inW, _ := os.Pipe()
	defer inW.Close() // never closed while runHook runs
	os.Stdin = in
	outR, outW, _ := os.Pipe()
	os.Stdout = outW

	done := make(chan struct{})
	go func() {
		defer close(done)
		runHook([]string{"--agent", "cc", "PdxPreToolUse"})
	}()
	time.Sleep(100 * time.Millisecond) // runHook is blocked in the stdin read
	select {
	case <-done:
		t.Fatal("runHook returned before the budget ended, with stdin still open")
	default:
	}
	clock.fireNext() // the 5 s budget, by the fake clock
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("runHook did not return once the budget ended: the stdin read is outside the budget")
	}
	outW.Close()
	got, _ := io.ReadAll(outR)
	if string(got) != "" {
		t.Fatalf("stdout = %q, want empty", got)
	}
	postMu.Lock()
	defer postMu.Unlock()
	if len(posted) != 1 || string(posted[0].RawEvent) != "{}" {
		t.Fatalf("the event POST still goes out with the empty payload: %+v", posted)
	}
}
