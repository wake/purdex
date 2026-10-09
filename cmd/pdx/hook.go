package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/wake/purdex/cmd/pdx/daemonclient"
	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/team"
)

type hookPayload struct {
	TmuxSession   string `json:"tmux_session"`
	TmuxSessionID string `json:"tmux_session_id,omitempty"`
	TmuxPaneID    string `json:"tmux_pane_id"`
	// SessionID is the agent's session_id from its hook stdin. The daemon keys
	// a session that is not inside tmux (no tmux identity) by it; an older
	// daemon ignores the field.
	SessionID       string          `json:"session_id,omitempty"`
	PurdexName      string          `json:"purdex_name"`
	RawEvent        json.RawMessage `json:"raw_event"`
	AgentType       string          `json:"agent_type"`
	SenderPID       int             `json:"sender_pid"`
	SenderStartTime string          `json:"sender_start_time"`
	SenderUncertain bool            `json:"sender_uncertain,omitempty"`
}

type hookProvenance struct {
	TmuxPaneID      string
	SenderPID       int
	SenderStartTime string
	SenderUncertain bool
}

var queryTmuxSessionFn = queryTmuxSession
var queryTmuxSessionInfoFn = queryTmuxSessionInfo
var queryTmuxPaneIDFn = queryTmuxPaneID
var resolveHookProvenanceFn = resolveHookProvenance
var postHookEventFn = postHookEvent
var loadConfigFn = config.Load

// hookClientOpts are appended to the decision client's options; nil in
// production, a fake clock in tests.
var hookClientOpts []daemonclient.Option

// hookAfterFn arms the hook's budget timer; time.AfterFunc in production,
// the fake clock's afterFunc in tests.
var hookAfterFn = func(d time.Duration, f func()) (stop func() bool) {
	t := time.AfterFunc(d, f)
	return t.Stop
}

// readStdinUntil reads r to EOF unless ctx ends first; then, as on a read
// error or an empty payload, it is "{}" (CC and Codex write the payload and
// close at once; a hook started by hand would otherwise hang forever).
func readStdinUntil(ctx context.Context, r io.Reader) []byte {
	type result struct {
		data []byte
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		data, err := io.ReadAll(r)
		ch <- result{data, err}
	}()
	select {
	case res := <-ch:
		if res.err != nil || len(res.data) == 0 {
			return []byte("{}")
		}
		return res.data
	case <-ctx.Done():
		return []byte("{}")
	}
}

// hookDecideGrace is the hook's whole budget after the stdin read (spec
// §6.6): the event POST and the decision run concurrently under one ctx
// that this timer cancels, and the decision client's restart grace and
// per-attempt timeout are the same 5 s. An unreachable, restarting or
// silent daemon costs this session's tool call at most 5 s — not 5 s plus
// the event POST — and then the normal permission flow runs.
const hookDecideGrace = 5 * time.Second

// hookDecideMaxInline is the stdin size above which the decision request
// carries only the ids (no tool_input, no raw): the daemon caps bodies at
// 1 MiB, and the lock answer never needs the tool's input.
const hookDecideMaxInline = 64 << 10

// hookDecideEvents maps the PurdexName of the two events whose hook waits
// for a decision to the wire event name; every other event is
// fire-and-forget as before.
var hookDecideEvents = map[string]string{
	"PdxPreToolUse":        team.HookEventPreToolUse,
	"PdxPermissionRequest": team.HookEventPermissionRequest,
}

// runHook is the entry point for `pdx hook --agent <cc|codex> <PurdexName>`.
// It reads stdin, queries tmux for the session name, and POSTs to the daemon.
// Daemon/tmux/runtime failures are swallowed to avoid breaking the agent hook path.
// CLI misuse still exits non-zero so local invocation mistakes are visible.
//
// For PreToolUse and PermissionRequest only (spec §6.6), and only when the
// session's flag file <data_dir>/hooklocks/<agent>/<session_id> exists, it
// also asks POST /api/hooks/decide and prints the agent's JSON for a deny;
// otherwise it prints nothing. The event POST and the decision run
// concurrently under one 5 s budget (hookDecideGrace): the hook holds the
// agent at most 5 s on every path, and exits 0 on every path.
func runHook(args []string) {
	if len(args) < 1 {
		os.Exit(0)
	}

	// Parse --agent flag manually; remaining positional arg is the PurdexName.
	var agentType string
	var positional []string
	for i := 0; i < len(args); i++ {
		if args[i] == "--agent" && i+1 < len(args) {
			agentType = args[i+1]
			i++ // skip value
		} else {
			positional = append(positional, args[i])
		}
	}
	if len(positional) < 1 {
		os.Exit(0)
	}
	purdexName := positional[0]

	if agentType == "" {
		fmt.Fprintf(os.Stderr, "pdx hook: --agent flag is required\n")
		os.Exit(1)
	}

	// One 5 s budget: the stdin read, the event POST and the decision share
	// it, so what the lock path adds holds the agent ≤ 5 s on every path
	// (spec §6.6, §15; Codex's outer hook timeout is 10 s). The tmux and
	// provenance lookups above predate it and are not under it. The ctx
	// carries a cancel, not a Deadline(), so daemonclient keeps its own
	// (fake-able) attempt timer (client.go attemptCtx).
	budget, cancelBudget := context.WithCancel(context.Background())
	defer cancelBudget()
	stopBudget := hookAfterFn(hookDecideGrace, cancelBudget)
	defer stopBudget()

	tmuxSessionID, tmuxSession := queryTmuxSessionInfoFn()
	provenance := resolveHookProvenanceFn()
	raw := readStdinUntil(budget, os.Stdin)
	payload := buildHookPayload(tmuxSessionID, tmuxSession, purdexName, bytes.NewReader(raw), agentType, provenance)

	cfg, err := loadConfigFn("")
	var url, token string
	if err != nil {
		url = "http://127.0.0.1:7860/api/agent/event"
	} else {
		url = fmt.Sprintf("http://%s:%d/api/agent/event", cfg.Bind, cfg.Port)
		token = cfg.Token
	}

	eventDone := make(chan struct{})
	go func() {
		defer close(eventDone)
		_ = postHookEventFn(budget, url, token, payload) // its own 2 s client timeout, and the budget
	}()

	if err == nil { // no config: no data dir to find a flag in
		base := fmt.Sprintf("http://%s:%d", resolveDaemonHost(cfg.Bind), cfg.Port)
		out, asked := hookDecision(budget, hookDecideInput{
			DataDir: cfg.DataDir,
			Base:    base,
			Token:   cfg.Token,
			Agent:   agentType, PurdexName: purdexName, Raw: raw,
			ClientOpts: hookClientOpts,
		})
		if len(out) > 0 {
			os.Stdout.Write(out)
		}
		// The terminal-only degradation's forward (P8a, hook_ask.go): not
		// when the lock path already sent this event to the daemon; under
		// the same budget, printing nothing.
		if !asked {
			forwardHookAsk(budget, base, cfg.Token, cfg.DataDir, agentType, payload.RawEvent)
		}
	}
	// The event POST is never cut short by the decision (spec §6.6: the
	// event is still posted as today). Its own 2 s client timeout and the
	// 5 s budget bound it, so the hook holds the agent ≤ 5 s on every path.
	<-eventDone
}

// hookStdin is the part of the agent's hook payload the decision needs.
// CC and Codex use the same names (spec M18, M20; Codex fixtures under
// internal/agent/codex/testdata/codex-0.153.4-payloads/).
type hookStdin struct {
	SessionID string          `json:"session_id"`
	ToolName  string          `json:"tool_name"`
	ToolInput json.RawMessage `json:"tool_input"`
	ToolUseID string          `json:"tool_use_id"`
}

// hookDecideInput is what hookDecision needs from runHook.
type hookDecideInput struct {
	DataDir    string
	Base       string // http://host:port
	Token      string
	Agent      string // "cc" | "codex"
	PurdexName string // the argv event, PdxXxx
	Raw        []byte // the hook's stdin
	ClientOpts []daemonclient.Option
}

// hookDecisionOutput is the agent's stdout shape for a PreToolUse deny
// (spec M18; identical for Codex, M20).
type hookDecisionOutput struct {
	HookSpecificOutput hookSpecificOutput `json:"hookSpecificOutput"`
}

type hookSpecificOutput struct {
	HookEventName            string `json:"hookEventName"`
	PermissionDecision       string `json:"permissionDecision"`
	PermissionDecisionReason string `json:"permissionDecisionReason"`
}

// hookDecision is the lock path of spec §6.6. It returns the bytes to
// print on stdout, or nil for "no decision" — which is every path but one:
// an event other than the two, a stdin without a session id, no flag file
// (the gate: then the daemon is not called at all), the daemon unreachable
// or restarting past the 5 s grace, silent, a 404, any other error, and a
// {} or PermissionRequest answer. Only a PreToolUse deny prints. asked is
// true once the gate passed and the daemon was called: runHook then treats
// the decision's return as the end of the hook's budget.
func hookDecision(ctx context.Context, in hookDecideInput) (out []byte, asked bool) {
	event, ok := hookDecideEvents[in.PurdexName]
	if !ok {
		return nil, false
	}
	var stdin hookStdin
	if err := json.Unmarshal(in.Raw, &stdin); err != nil || stdin.SessionID == "" {
		return nil, false
	}
	if !hookLockExists(team.HookLockPath(in.DataDir, in.Agent, stdin.SessionID)) {
		return nil, false
	}
	opts := append([]daemonclient.Option{
		daemonclient.WithGrace(hookDecideGrace),
		daemonclient.WithAttemptTimeout(hookDecideGrace),
	}, in.ClientOpts...)
	client := daemonclient.New(in.Base, in.Token, opts...)
	req := team.HookDecideRequest{
		Agent: in.Agent, Event: event, SessionID: stdin.SessionID,
		ToolName: stdin.ToolName, ToolInput: stdin.ToolInput, ToolUseID: stdin.ToolUseID,
		Raw: json.RawMessage(in.Raw),
	}
	if len(in.Raw) > hookDecideMaxInline {
		// A Write of a large file puts its whole content in tool_input; the
		// lock answer needs only the ids, and a body over the daemon's 1 MiB
		// cap would be a 400 — i.e. no decision, and a lock silently
		// bypassed for exactly the biggest writes.
		req.ToolInput, req.Raw = nil, nil
	}
	var resp team.HookDecideResponse
	// The decision is a read in POST clothing (the daemon writes nothing
	// for it), so a connection lost after the send may be replayed inside
	// the grace.
	if _, err := client.Do(ctx, http.MethodPost, "/api/hooks/decide", req, &resp, daemonclient.Idempotent()); err != nil {
		return nil, true
	}
	if resp.Decision != "deny" || event != team.HookEventPreToolUse {
		return nil, true
	}
	out, err := json.Marshal(hookDecisionOutput{HookSpecificOutput: hookSpecificOutput{
		HookEventName: event, PermissionDecision: "deny", PermissionDecisionReason: resp.Reason,
	}})
	if err != nil {
		return nil, true
	}
	return append(out, '\n'), true
}

// queryTmuxSession runs `tmux display-message -p '#{session_name}'` and returns
// the session name, or "" on any error. Retained for non-hook callers
// (cmd/pdx/statusline_proxy.go) that only need the name.
func queryTmuxSession() string {
	out, err := exec.Command(tmuxExecutable(), "display-message", "-p", "#{session_name}").Output()
	if err != nil {
		return ""
	}
	return strings.TrimRight(string(out), "\n")
}

// queryTmuxSessionInfo returns (sessionID, sessionName) from a single
// `tmux display-message -p '#{session_id}|#{session_name}'` call. Both empty
// on any error. The sessionID ($N format) is immutable across rename and is
// the primary key the daemon uses to bypass the name cache rename-race
// window when resolving session codes for hook events.
func queryTmuxSessionInfo() (string, string) {
	out, err := exec.Command(tmuxExecutable(), "display-message", "-p", "#{session_id}|#{session_name}").Output()
	if err != nil {
		return "", ""
	}
	return parseTmuxSessionInfo(string(out))
}

// parseTmuxSessionInfo splits "<sessionID>|<sessionName>" output on the FIRST
// '|'. Names may contain '|' characters (real tmux permits them); the parser
// preserves them in the name half. When no delimiter is present we treat the
// whole string as the name and return an empty ID — the daemon falls back to
// the name path safely.
func parseTmuxSessionInfo(out string) (string, string) {
	trimmed := strings.TrimRight(out, "\n")
	idx := strings.Index(trimmed, "|")
	if idx < 0 {
		return "", trimmed
	}
	return trimmed[:idx], trimmed[idx+1:]
}

func queryTmuxPaneID() string {
	return os.Getenv("TMUX_PANE")
}

// buildHookPayload constructs a hookPayload from the given parameters.
// If stdin is empty or cannot be read, raw_event defaults to {}.
// tmuxSessionID is optional ($N format); when non-empty the daemon prefers
// it over tmuxSession for code resolution to dodge the rename-race window.
func buildHookPayload(tmuxSessionID, tmuxSession, purdexName string, stdin io.Reader, agentType string, provenance hookProvenance) hookPayload {
	raw, err := io.ReadAll(stdin)
	if err != nil || len(bytes.TrimSpace(raw)) == 0 {
		raw = []byte("{}")
	}
	var stdinIDs hookStdin
	_ = json.Unmarshal(raw, &stdinIDs) // best effort; tool_input etc. are ignored
	return hookPayload{
		SessionID:       stdinIDs.SessionID,
		TmuxSession:     tmuxSession,
		TmuxSessionID:   tmuxSessionID,
		TmuxPaneID:      provenance.TmuxPaneID,
		PurdexName:      purdexName,
		RawEvent:        json.RawMessage(raw),
		AgentType:       agentType,
		SenderPID:       provenance.SenderPID,
		SenderStartTime: provenance.SenderStartTime,
		SenderUncertain: provenance.SenderUncertain,
	}
}

// postHookEvent POSTs the payload as JSON to the given URL with a 2-second
// timeout, under the caller's ctx (the hook's budget) as well.
// If token is non-empty, it is sent as a Bearer Authorization header.
func postHookEvent(ctx context.Context, url, token string, payload hookPayload) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("post event: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("daemon returned %d", resp.StatusCode)
	}
	return nil
}
