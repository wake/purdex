package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/team"
)

// Mutation gate (U17 cost rule): a frequent event with no flag ⇒ no call.
func TestAskForward_UngatedOnlyForAskAndPermission(t *testing.T) {
	cases := []struct {
		raw  string
		flag bool
		want bool
	}{
		{`{"hook_event_name":"PreToolUse","tool_name":"AskUserQuestion","session_id":"s"}`, false, true},
		{`{"hook_event_name":"PreToolUse","tool_name":"Bash","session_id":"s"}`, false, false},
		{`{"hook_event_name":"PreToolUse","tool_name":"Bash","session_id":"s"}`, true, false}, // the lock gate is P2c's, not this one
		{`{"hook_event_name":"PermissionRequest","tool_name":"Bash","session_id":"s"}`, false, true},
		{`{"hook_event_name":"PostToolUse","tool_name":"AskUserQuestion","session_id":"s"}`, false, false},
		{`{"hook_event_name":"PostToolUse","tool_name":"AskUserQuestion","session_id":"s"}`, true, true},
		{`{"hook_event_name":"PostToolUseFailure","tool_name":"Bash","session_id":"s"}`, true, true},
		{`{"hook_event_name":"Stop","session_id":"s"}`, false, false},
		{`{"hook_event_name":"Stop","session_id":"s"}`, true, true},
		{`{"hook_event_name":"UserPromptSubmit","session_id":"s"}`, true, true},
		{`{"hook_event_name":"SessionEnd","session_id":"s"}`, true, true},
		{`{"hook_event_name":"Notification","session_id":"s"}`, true, false},
		{`not json`, true, false},
	}
	for _, c := range cases {
		if got := askForward(parseHookAskEvent(json.RawMessage(c.raw)), c.flag); got != c.want {
			t.Errorf("askForward(%s, flag=%v) = %v, want %v", c.raw, c.flag, got, c.want)
		}
	}
}

func TestAskFlagExists_ReadsTheDaemonsFlag(t *testing.T) {
	dir := t.TempDir()
	if askFlagExists(dir, "cc", "sid-1") {
		t.Fatal("no flag yet")
	}
	if err := os.MkdirAll(filepath.Join(dir, "hookasks", "cc"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "hookasks", "cc", "sid-1"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if !askFlagExists(dir, "cc", "sid-1") || !askFlagExists(dir, "", "sid-1") {
		t.Fatal("flag present must be seen (agent defaults to cc)")
	}
	if askFlagExists(dir, "cc", "../../etc/passwd") || askFlagExists(dir, "cc", "") || askFlagExists("", "cc", "sid-1") {
		t.Fatal("a traversal (Base-d, so it cannot escape), an empty session or no data dir must not match")
	}
	if askFlagExists(dir, "cc", "..") || askFlagExists(dir, "cc", ".") {
		t.Fatal("a directory (\"..\" names hookasks/ itself) is not a flag")
	}
}

// The forward carries the hook's fields and the raw stdin, with the token,
// and sends nothing for an event the predicate refuses.
func TestForwardHookAsk_SendsDecideWithRaw(t *testing.T) {
	var mu sync.Mutex
	var got []team.HookDecideRequest
	var auths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		auths = append(auths, r.Header.Get("Authorization"))
		if r.URL.Path != "/api/hooks/decide" || r.Method != http.MethodPost {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		var req team.HookDecideRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		got = append(got, req)
		_, _ = io.WriteString(w, `{}`)
	}))
	t.Cleanup(srv.Close)
	dir := t.TempDir()
	raw := json.RawMessage(`{"hook_event_name":"PreToolUse","session_id":"sid-1","tool_name":"AskUserQuestion","tool_use_id":"toolu_1","tool_input":{"questions":[]},"permission_mode":"bypassPermissions"}`)
	if !forwardHookAsk(context.Background(), srv.URL, "admin-tok", dir, "cc", raw) {
		t.Fatal("PreToolUse/AskUserQuestion must be forwarded")
	}
	if forwardHookAsk(context.Background(), srv.URL, "admin-tok", dir, "cc", json.RawMessage(`{"hook_event_name":"Stop","session_id":"sid-1"}`)) {
		t.Fatal("Stop without the flag must not be forwarded")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 || got[0].Agent != "cc" || got[0].Event != "PreToolUse" || got[0].SessionID != "sid-1" || got[0].ToolName != "AskUserQuestion" ||
		got[0].ToolUseID != "toolu_1" || string(got[0].ToolInput) != `{"questions":[]}` || string(got[0].Raw) != string(raw) {
		t.Fatalf("decide body = %+v", got)
	}
	if auths[0] != "Bearer admin-tok" {
		t.Fatalf("auth = %q", auths[0])
	}
}

// A daemon that is down costs at most hookAskTimeout and nothing else.
func TestForwardHookAsk_DaemonDownIsBoundedAndSilent(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	raw := json.RawMessage(`{"hook_event_name":"PermissionRequest","session_id":"sid-1","tool_name":"Bash"}`)
	if !forwardHookAsk(context.Background(), url, "admin-tok", t.TempDir(), "cc", raw) {
		t.Fatal("PermissionRequest must be attempted (and silently fail)")
	}
}

const ccAskStdin = `{"session_id":"cc-sid-1","hook_event_name":"PreToolUse","tool_name":"AskUserQuestion","tool_input":{"questions":[{"question":"q?"}]},"tool_use_id":"toolu_q"}`

// hookAskRig runs runHook end to end against the daemon at srvURL (config
// through loadConfigFn, the event POST stubbed, the budget timer on clock)
// and returns what it wrote to stdout and stderr; lock writes cc-sid-1's
// lead-lock flag first.
func hookAskRig(t *testing.T, srvURL string, clock *leadClock) func(purdexName, stdin string, lock bool) (string, string) {
	origInfo, origResolve, origPost, origLoad := queryTmuxSessionInfoFn, resolveHookProvenanceFn, postHookEventFn, loadConfigFn
	origStdin, origStdout, origStderr, origAfter := os.Stdin, os.Stdout, os.Stderr, hookAfterFn
	t.Cleanup(func() {
		queryTmuxSessionInfoFn, resolveHookProvenanceFn, postHookEventFn, loadConfigFn = origInfo, origResolve, origPost, origLoad
		os.Stdin, os.Stdout, os.Stderr, hookAfterFn = origStdin, origStdout, origStderr, origAfter
	})
	queryTmuxSessionInfoFn = func() (string, string) { return "$1", "work" }
	resolveHookProvenanceFn = func() hookProvenance { return hookProvenance{} }
	postHookEventFn = func(context.Context, string, string, hookPayload) error { return nil }
	hookAfterFn = clock.afterFunc
	host, portStr, _ := splitHostPort(srvURL)
	port, _ := strconv.Atoi(portStr)
	dataDir := t.TempDir()
	loadConfigFn = func(string) (config.Config, error) {
		return config.Config{Bind: host, Port: port, Token: "tok", DataDir: dataDir}, nil
	}
	return func(purdexName, stdin string, lock bool) (string, string) {
		if lock {
			touchHookLock(t, dataDir, "cc", "cc-sid-1")
		}
		in, inW, _ := os.Pipe()
		_, _ = inW.WriteString(stdin)
		inW.Close()
		outR, outW, _ := os.Pipe()
		errR, errW, _ := os.Pipe()
		os.Stdin, os.Stdout, os.Stderr = in, outW, errW
		runHook([]string{"--agent", "cc", purdexName})
		os.Stdout, os.Stderr = origStdout, origStderr
		outW.Close()
		errW.Close()
		out, _ := io.ReadAll(outR)
		errOut, _ := io.ReadAll(errR)
		return string(out), string(errOut)
	}
}

// End to end: an AskUserQuestion with no lock flag is forwarded once and the
// hook writes nothing (the daemon's answer, a deny here, is discarded) and
// returns (exit 0 is main's); with the flag the lock path already sent it,
// so the forward does not send it again. Mutation gates: drop the !asked
// guard → 3 decides; print the answer or os.Exit in the forward → red.
func TestRunHook_ForwardsAskOnceAndSilently(t *testing.T) {
	d := newFakeHookDaemon(team.HookDecideResponse{Decision: "deny", Reason: "r"})
	srv := httptest.NewServer(d)
	defer srv.Close()
	run := hookAskRig(t, srv.URL, newLeadClock())
	if out, errOut := run("PdxPreToolUse", ccAskStdin, false); out != "" || errOut != "" || d.calls() != 1 {
		t.Fatalf("no flag: stdout=%q stderr=%q decides=%d", out, errOut, d.calls())
	}
	d.mu.Lock()
	first := d.decides[0]
	d.mu.Unlock()
	if first.Event != "PreToolUse" || first.ToolName != "AskUserQuestion" || first.ToolUseID != "toolu_q" || string(first.Raw) != ccAskStdin {
		t.Fatalf("forward body = %+v", first)
	}
	if out, _ := run("PdxPreToolUse", ccAskStdin, true); !strings.Contains(out, `"permissionDecision":"deny"`) || d.calls() != 2 {
		t.Fatalf("flag: stdout=%q decides=%d, want the lock path's one decide and no forward", out, d.calls())
	}
}

// The forward is inside the hook's 5 s budget: a daemon holding it is cut
// when the budget ends (fired here by the fake clock), not 2 s later.
// Mutation gate: the forward on a context.Background() timeout → runHook
// outlives the budget by its 2 s → red.
func TestRunHook_ForwardEndsWithTheBudget(t *testing.T) {
	got := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body) // the server notices the client leaving only once the body is read
		got <- struct{}{}
		<-r.Context().Done()
	}))
	defer srv.Close()
	clock := newLeadClock()
	run := hookAskRig(t, srv.URL, clock)
	done := make(chan string, 1)
	go func() {
		out, errOut := run("PdxPermissionRequest", `{"session_id":"cc-sid-1","hook_event_name":"PermissionRequest","tool_name":"Bash"}`, false)
		done <- out + errOut
	}()
	<-got            // the forward is in flight
	clock.fireNext() // the budget ends
	select {
	case s := <-done:
		if s != "" {
			t.Fatalf("output = %q", s)
		}
	case <-time.After(time.Second):
		t.Fatal("runHook outlived its budget: the forward is not under it")
	}
}
