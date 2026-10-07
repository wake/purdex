# Lead / member / team and context relay — Implementation Plan v2 (P2c, P5a, P5b, P8a)

> **Status (2026-10-07):** written by `mlab/_81nu3d` after plan v1 (P0–P3) shipped in alpha.513–527 and the lead request was accepted end to end on mlab. Every `file:line` was re-verified against origin/main `alpha.527`. Not yet through codex review.
> **Scope of v2:** the next four phases in spec §12's order — **P2c** (hook hard lock), **P5a** (daemon relay core), **P5b** (plugin packaging and the mod's self relay), **P8a** (分流 for AskUserQuestion). **P4, P4b, P4c, P6, P7** come in plan v3 after P8a merges, so the self-relay goal (spec §1 goal 1) ships first and the team phases build on a working mod.

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** a solo or lead session relays itself at 70 % context after a one-click approval, through a Purdex mod that runs inside Claude Code; a lead request cannot be worked around by backgrounding the call; a session's AskUserQuestion reaches every client while the terminal keeps its native dialog.

**Architecture:**
- **P2c.** `pdx hook` gains a decision path for `PreToolUse` / `PermissionRequest`, gated by a per-session flag file; the team module answers `POST /api/hooks/decide` from its open lead requests.
- **P5a.** The team module gains `relay_ops` and `session_lineage` in `team.db`, the `self_relay` approval kind, relay switches in host config, a per-session pause, the `/api/relay/*` routes, `pdx relay *` subcommands, `previous_refs` on peer rows with a `Resolve` tier, the title move, the handoff retention sweeper; the SPA gets the Hosts 「接力」 toggles and the self-relay dialog body.
- **P5b.** The plugin (mod + skill) is embedded in `pdx`, extracted under the data dir, loaded through `CLAUDE_CODE_PLUGIN_DIRS`; the mod runs the relay steps (begin, hold, write, check, clear, seed, report) and `/relay`.
- **P8a.** Two more approval kinds (`hook_ask`, `hook_permission`), `RemoteResponders`, `/api/ask/*` and `pdx ask *`; the mod races the native AskUserQuestion dialog against the daemon's answer; a session without the mod degrades to a terminal-only card.

**Tech Stack:** Go (net/http, modernc.org/sqlite, `embed`, plain `testing`), the shipped `cmd/pdx/daemonclient`, React 19 + Zustand 5 + Vitest, Claude Code 2.1.291 plugin hooks (plain JS `register.js`, `claude plugin test` / `validate`).

**Spec:** `docs/specs/2026-10-06-lead-team-relay-spec.md` (U1–U19, M1–M24). Read §2, §5, §6.6, §8, §9, §10, §12, §14, §15 before any task. **Shipped contracts to build on, not re-derive:** `internal/team/wire.go`, `internal/module/team/*`, `cmd/pdx/daemonclient`, `cmd/pdx/lead.go`, `spa/src/lib/team/*`, `spa/src/stores/useApprovalStore.ts`, `spa/src/components/ApprovalDialogHost.tsx` — and plan v1's "Coordinator decisions" sections.

## Global Constraints

- **PR size.** One PR ≤ 800 lines of diff or ≤ 20 files; the sections below are already split. Every task is its own commit, test first (TDD). Each PR says what must be merged before it.
- **Order.** P2c → P5a-1 → P5a-2 → P5a-3 → P5b-1 → P5b-2 → P5b-3 → P8a-1 → P8a-2.
- **Tests and builds:** Go `go test ./<pkg>/ -race`; SPA `cd spa && npx vitest run <path>`, `pnpm run lint`, `npx tsc -p tsconfig.app.json --noEmit`, `pnpm run build` (pnpm, never npm; a fresh worktree needs `pnpm install --frozen-lockfile`); mod `claude plugin validate <folder>` and `claude plugin test <folder>`.
- **Commits.** Every commit ends with `Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>`. Parallel subagents in one worktree commit with `git commit --only <files>`.
- **Copy.** User-visible strings are Traditional Chinese exactly as the spec quotes them (U16: 接力 / 切換, never 交接); SPA strings in both `spa/src/locales/en.json` and `zh-TW.json`, pinned by `locale-completeness.test.ts`.
- **Exit codes** (spec §14): 0 ok, 1 runtime/API error, 2 usage, 10 denied, 11 timed out, 12 cancelled or abandoned, 13 refused by team rules, 14 member did not start or respond, 20 daemon unreachable through the grace, 21 daemon does not support the route.
- **Restart grace** (spec §9.1, as shipped in `daemonclient`): retry only on refused / 503 shutting_down|not_ready (before send) and reset / EOF (after send); 30 s hard grace; writes replay only with `Idempotent()`; `ErrNoAnswer` on a hung attempt; the hook path uses a **5 s** grace.
- **Hook decisions** (spec §6.6): `pdx hook` always exits 0; a decision is printed only when obtained; an unreachable daemon prints nothing (the normal permission flow), never a forced allow.
- **Mod limits** (M24): `$.process.run` `timeoutMs` ≤ 10 min, so every wait is a loop of ≤ 9 min calls; a hook's own 10 s budget does not run while a `$` call is in flight; `$.ui.ask` and `tool.call` cannot be aborted — the hook's return is what closes a native dialog.
- **Time fields** are unix milliseconds.
- **Secrets.** Never print a token; tests use literal fake tokens.
- **Deploys.** daemon and `pdx` binaries are deployed by the conversation-entity line's coordinator (`mlab/_giihrj`); SPA changes ride HMR after the main checkout is fast-forwarded; the plugin reaches sessions through `pdx setup` (new sessions only — a running Claude Code loads plugins at start).

## Review Focus

1. **A session with no flag file calls `pdx hook` on every tool call:** no daemon decision call is made, stdout stays empty, latency is today's. → P2c Task (hook gate test).
2. **The daemon is unreachable while a flag is present:** `pdx hook` exits 0 with empty stdout within 5 s; the session continues under its normal permission flow. → P2c (unreachable test with a fake clock).
3. **A relay reaches `cleared` while the daemon restarts:** the lineage is written once — by the mod's idempotent `cleared` report (P5a-2b; a re-send after the restart is a no-op) — and the old ref still resolves (P5a-1b); the title moved once (`moveTitle` idempotent, re-run by boot reconciliation for ops still in `cleared`). Boot reconciliation **from frames** (a past-`claimed` self op whose pane now shows another session id) is **P6**, with the member ops. → P5a-1/P5a-2 (idempotent `Report`, lineage test, Resolve tier test), **P5a-2b `TestRelayReport_ClearedAcrossRestartIsIdempotentEndToEnd`** (cleared once → a second `Module` over the same team.db + meta.db → cleared again: one lineage row, title moved once, old ref → new row through `LineageReader` → `Build` → `Resolve`; codex round), P5b-2 (the mod re-sends `cleared` at the next `turn.complete`).
4. **A typed prompt arrives while a self-relay request is open:** it waits (the hold), runs in the old conversation with NOTE after approval, passes unchanged after denial; the write turn is recognised as the mod's own even with queued prompts ahead. → P5b-3 (hold, NOTE, own-turn tests; mutation "treat the next `turn.complete` as the write turn → red").
5. **AskUserQuestion with no client connected, or a remote answer landing while the terminal answers:** no request is opened / the terminal wins and the card says 「已在終端機回答」; the native dialog never shows anything unusual. → P8a-1 (`no_responders`, `terminal_override` tests), P8a-2 (mod race tests).

---

## PR P2c — hook decisions: the hard lock for lead requests (spec §6.4, §6.6, §11, §14, §15 "Hook decisions (U17)"; M17, M18, M20)

**Scope.** `pdx hook` learns to wait for the daemon's answer for exactly two events, `PreToolUse` and `PermissionRequest`, and only when the session's flag file `<data_dir>/hooklocks/<agent>/<session_id>` exists; every other event and every session without a flag stays fire-and-forget (M17). The daemon answers `POST /api/hooks/decide` from the session's open lead request (`OpenByOrigin`), with the spec's deny reason on PreToolUse and `{}` on PermissionRequest; `pdx lead request` writes the flag right after its 201 and removes it on every exit path; the team sweeper prunes flags of sessions the registry no longer lists (10th tick and boot); the Codex installer raises `PreToolUse`'s `timeout` 5 → 10 and the CC installer stays untouched (pinned). The relay lock (P6) and the 分流 kinds (P8a) are not here; the route and the wire leave room for them.

**Measured here** (2026-10-07, scratch copy of the worktree at origin/main `746759d2`, alpha.527; Go 1.26.0; every block below compiled and ran, `-race` included). **Code facts the tasks rest on:**
- `pdx hook` today: `cmd/pdx/hook.go:47-87` parses `--agent`, queries tmux (`:73`), builds the payload with `io.ReadAll(stdin)` and **no timeout** (`:136`), POSTs `/api/agent/event` with a 2 s client (`:170`), returns — `main` exits 0. The only non-zero exit is the missing `--agent` (`:69-70`). Test seams are package vars (`:36-41`). `readStdinWithTimeout(r io.Reader, timeoutSec int) []byte` exists in the same package (`cmd/pdx/statusline_proxy.go:19-38`) and is reused.
- `pdx lead request` knows only its inbox (`cmd/pdx/lead.go:143`); the daemon's 201 (and the idempotent 200) body is the stored `team.Approval` whose `Origin.SessionID` is the CC session id (`internal/module/team/handler.go:206-207, 225`; `internal/team/wire.go:57`). `runLeadCmd` **returns an int on every path** — create error `:176-182`, signal → `leadCancel` `:186-187, :193-194`, hung polls `:196-204`, `leadFinish` `:210` — and `runLead` does `os.Exit(runLeadCmd(...))` (`:52`), so a `defer` inside `runLeadCmd` runs on all of them, SIGINT/SIGTERM included; only SIGKILL skips it.
- `daemonclient` has **no grace option**: `Grace` is a package constant (`cmd/pdx/daemonclient/client.go:39`) read by `outage.remaining` (`:200`) from `attemptCtx` (`:293`) and `wait` (`:444`). `WithAttemptTimeout` (`:123`) exists; a caller ctx **with** a deadline disables the per-attempt bound (`:289-291`), so the hook passes no deadline and relies on `WithAttemptTimeout`. `Do` probes `/api/health` once before the first request (`:230-246`). `Idempotent()` (`:139`) lets a POST be replayed after a reset.
- Team module: `OpenByOrigin(sessionID, kind)` (`internal/module/team/store.go:275-285`); routes `module.go:115-122`; `Init` has `c.Cfg.DataDir` (`:106`); `Start` (`:127-140`); the sweeper's liveness cadence is `checkLive := m.tickN%livenessEvery == 0` (`sweeper.go:58`) **after** an early return when nothing is open (`:51-53`), so a flag prune must sit before that return. `fakeOrigins.LiveSession` / `markDead` and `newFixture` (`handler_test.go:30-111`) are the test fixture.
- Every route outside `/api/peers` goes through `TokenAuth` (`cmd/pdx/http_chain.go:28-29, 33`), so `/api/hooks/decide` is behind it with no extra wiring; `daemonclient.Once` sends `Authorization: Bearer` (`client.go:348`).
- Codex installer: `codexHookTimeouts` map + default 5 (`internal/agent/codex/hooks.go:20-32`), written as `"timeout": codexHookTimeoutSeconds(key)` (`:332`); install strips pdx-owned entries and re-appends (`:319-336`), so a re-install rewrites an existing `5`. `CheckHooks` never reads `timeout` (`checkCodexEvent` `:207-243`): an installed `5` reports *Installed* until re-installed. `trusted_hash` lives in `config.toml` `[hooks.state]`, round-tripped by the installer (`hooks_test.go:2199-2222`); whether Codex re-prompts after the hooks.json change is M20's unmeasured point — measured once in this PR's manual step, not automated.
- CC installer: `makePdxEntry` (`internal/agent/cc/hooks.go:213-222`) writes `type` + `command` only — no `timeout`, no `matcher`. Nothing changes; a test pins it.
- Hook stdin JSON: CC PreToolUse carries `session_id, transcript_path, cwd, permission_mode, hook_event_name, tool_name, tool_input, tool_use_id` (M19); Codex uses the **same names** (`internal/agent/codex/testdata/codex-0.153.4-payloads/PdxPreToolUse.json`; `PdxPermissionRequest.json` has no `tool_use_id`).
- `config.WriteFile` encodes `data_dir = ""` for a zero `DataDir`, so `config.Load` of such a file gives `""` (measured; the existing `writeTestConfig` does this). The CLI tests below therefore write a real `data_dir`, and `pdx lead request` treats an empty one as "no flag, warn".
- Codex sessions **cannot run `pdx lead request` yet**: it requires `CLAUDE_CODE_MESSAGING_SOCKET` (`lead.go:143-147`) and the origin resolver attributes CC registry entries only (`internal/module/peers/origin_resolver.go:28-58`). The flag is written under `hooklocks/cc/`; the decide route accepts `agent: "codex"` and answers `{}` in P2c.

**Needs merged first:** P2a-3 (the team module and `OpenByOrigin`), P2b-1 (`daemonclient`), P2b-2 (`pdx lead request`). Nothing from P3.

**Split (measured, see `## Size estimate`):** **P2c-1** = Tasks 2c.1–2c.5 (wire, client grace, daemon route, sweeper, installers; ≈ 718 lines, 14 files); **P2c-2** = Tasks 2c.6–2c.7 (CLI: the flag in `pdx lead request`, the decision path in `pdx hook`; ≈ 724 lines, 7 files). P2c-2 depends on P2c-1 merged; against an older daemon the hook's decide call is a 404 → nothing printed, so deploy order does not matter for safety.

The worktree for these PRs is `/Users/wake/Workspace/wake/purdex/.claude/worktrees/lead-team-p2c` (one for both; P2c-2 branches after P2c-1 merges). Every command below starts there.

---

### Task 2c.1: Wire types, constants and `HookLockPath` — `internal/team`

**Files:**
- Modify: `internal/team/wire.go` (append after line 164, the end of `EventValue.MarshalJSON`)
- Create: `internal/team/hooklock.go`
- Test: `internal/team/wire_hooks_test.go`, `internal/team/hooklock_test.go`

**Interfaces:**
- Produces (package `team`, leaf; JSON names are the contract across Go, CLI and, in P8a, the mod):
  ```go
  const HookLocksDir = "hooklocks"
  const (
  	HookAgentCC    = "cc"
  	HookAgentCodex = "codex"
  	HookEventPreToolUse        = "PreToolUse"
  	HookEventPermissionRequest = "PermissionRequest"
  )
  const (
  	HookLockLeadRequest = "lead_request"
  	HookLockRelay       = "relay" // P6
  )
  const LeadLockReasonFmt = "lead 申請等待核准中（%s），核准或拒絕前這個 session 不能執行工具；請在 Purdex 介面處理"
  type HookDecideRequest struct {
  	Agent     string          `json:"agent"`
  	Event     string          `json:"event"`
  	SessionID string          `json:"session_id"`
  	ToolName  string          `json:"tool_name,omitempty"`
  	ToolInput json.RawMessage `json:"tool_input,omitempty"`
  	ToolUseID string          `json:"tool_use_id,omitempty"`
  	Raw       json.RawMessage `json:"raw,omitempty"`
  }
  type HookDecideResponse struct {
  	Decision string `json:"decision,omitempty"` // "deny" | ""
  	Reason   string `json:"reason,omitempty"`
  	Lock     string `json:"lock,omitempty"`     // "lead_request" | "relay" | ""
  	ID       string `json:"id,omitempty"`
  }
  // HookLockPath is <dataDir>/hooklocks/<agent>/<sessionID>, or "" when any
  // part is missing or could escape the directory.
  func HookLockPath(dataDir, agent, sessionID string) string
  ```
- Consumes: nothing new (`encoding/json`, `path/filepath`, `strings`).

- [ ] **Step 1: Write the failing tests.**

`internal/team/wire_hooks_test.go`:

```go
package team

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// The JSON names are the contract between pdx hook, the daemon and (P8a)
// the mod; the empty response must serialise as exactly {} so the hook's
// "no decision" is the spec's empty object, not a set of empty strings.
func TestHookDecide_JSONKeysAndEmptyResponse(t *testing.T) {
	req := HookDecideRequest{
		Agent: HookAgentCC, Event: HookEventPreToolUse, SessionID: "sid-1",
		ToolName: "Bash", ToolInput: json.RawMessage(`{"command":"ls"}`), ToolUseID: "toolu_1",
		Raw: json.RawMessage(`{"session_id":"sid-1"}`),
	}
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(raw, &keys); err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(keys))
	for k := range keys {
		got = append(got, k)
	}
	sort.Strings(got)
	want := []string{"agent", "event", "raw", "session_id", "tool_input", "tool_name", "tool_use_id"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("request keys = %v, want %v", got, want)
	}
	var back HookDecideRequest
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if back.Agent != req.Agent || back.Event != req.Event || back.SessionID != req.SessionID ||
		back.ToolName != req.ToolName || string(back.ToolInput) != string(req.ToolInput) ||
		back.ToolUseID != req.ToolUseID || string(back.Raw) != string(req.Raw) {
		t.Fatalf("round trip: %+v", back)
	}

	empty, err := json.Marshal(HookDecideResponse{})
	if err != nil {
		t.Fatal(err)
	}
	if string(empty) != `{}` {
		t.Fatalf("empty response = %s, want {}", empty)
	}
	deny, err := json.Marshal(HookDecideResponse{Decision: "deny", Reason: "r", Lock: HookLockLeadRequest, ID: "id-1"})
	if err != nil {
		t.Fatal(err)
	}
	if string(deny) != `{"decision":"deny","reason":"r","lock":"lead_request","id":"id-1"}` {
		t.Fatalf("deny = %s", deny)
	}
}

// The reason text is the spec's (§6.6), with the request id in the
// full-width parentheses.
func TestLeadLockReason_IsTheSpecText(t *testing.T) {
	got := fmt.Sprintf(LeadLockReasonFmt, "11111111-2222-4333-8444-555555555555")
	want := "lead 申請等待核准中（11111111-2222-4333-8444-555555555555），核准或拒絕前這個 session 不能執行工具；請在 Purdex 介面處理"
	if got != want {
		t.Fatalf("reason = %q", got)
	}
	if strings.Count(LeadLockReasonFmt, "%s") != 1 {
		t.Fatalf("LeadLockReasonFmt must take exactly one %%s: %q", LeadLockReasonFmt)
	}
	if HookLocksDir != "hooklocks" || HookAgentCC != "cc" || HookAgentCodex != "codex" ||
		HookEventPreToolUse != "PreToolUse" || HookEventPermissionRequest != "PermissionRequest" {
		t.Fatal("hook constants drifted from the contract")
	}
}
```

`internal/team/hooklock_test.go`:

```go
package team

import (
	"path/filepath"
	"testing"
)

func TestHookLockPath_RejectsEscapesAndUnknownAgents(t *testing.T) {
	for _, sid := range []string{"", ".", "..", "a/b", `a\b`, "../x", "/abs"} {
		if p := HookLockPath("/d", HookAgentCC, sid); p != "" {
			t.Errorf("sid %q → %q, want \"\"", sid, p)
		}
	}
	if p := HookLockPath("/d", "opencode", "sid"); p != "" {
		t.Errorf("unknown agent → %q", p)
	}
	if p := HookLockPath("", HookAgentCC, "sid"); p != "" {
		t.Errorf("no data dir → %q", p)
	}
	want := filepath.Join("/d", "hooklocks", "cc", "11111111-2222-4333-8444-555555555555")
	if p := HookLockPath("/d", HookAgentCC, "11111111-2222-4333-8444-555555555555"); p != want {
		t.Errorf("path = %q, want %q", p, want)
	}
	if p := HookLockPath("/d", HookAgentCodex, "01a0"); p != filepath.Join("/d", "hooklocks", "codex", "01a0") {
		t.Errorf("codex path = %q", p)
	}
}
```

- [ ] **Step 2: Run the tests and verify they fail.**
  - Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/lead-team-p2c && go test ./internal/team/ -run 'TestHookDecide|TestLeadLockReason|TestHookLockPath' -v`
  - Expected (measured): compile failure —
    ```
    internal/team/hooklock_test.go:10:11: undefined: HookLockPath
    internal/team/hooklock_test.go:10:30: undefined: HookAgentCC
    ...
    internal/team/wire_hooks_test.go:16:9: undefined: HookDecideRequest
    internal/team/wire_hooks_test.go:16:9: too many errors
    ```

- [ ] **Step 3: Implement.**

Append to `internal/team/wire.go` after line 164:

```go

// ---- P2c: hook decisions (lock path), spec §6.6 ----

// HookLocksDir is the flag directory under <data_dir>: an empty file
// <data_dir>/hooklocks/<agent>/<session_id> means that session has
// something pending and its PreToolUse / PermissionRequest hook must ask
// the daemon. Written by `pdx lead request` while its request is open (P2c)
// and by the Purdex mod during a relay (P6); nobody else (U19 (c)).
const HookLocksDir = "hooklocks"

// Hook agents and the two events whose hook waits for a decision.
const (
	HookAgentCC    = "cc"
	HookAgentCodex = "codex"

	HookEventPreToolUse        = "PreToolUse"
	HookEventPermissionRequest = "PermissionRequest"
)

// Lock names on HookDecideResponse.Lock.
const (
	HookLockLeadRequest = "lead_request"
	HookLockRelay       = "relay" // P6
)

// LeadLockReasonFmt is the PreToolUse deny reason while a lead request is
// open (spec §6.6); the argument is the request id.
const LeadLockReasonFmt = "lead 申請等待核准中（%s），核准或拒絕前這個 session 不能執行工具；請在 Purdex 介面處理"

// HookDecideRequest is POST /api/hooks/decide: what `pdx hook` read on
// stdin, for the two events that wait. Raw is the whole stdin, for later
// kinds (P8a).
type HookDecideRequest struct {
	Agent     string          `json:"agent"`      // "cc" | "codex"
	Event     string          `json:"event"`      // "PreToolUse" | "PermissionRequest" decide; any other hook event name is answered {} (P8a-1d forwards PostToolUse / Stop / … here)
	SessionID string          `json:"session_id"` // the agent's own session id (the hook stdin's session_id)
	ToolName  string          `json:"tool_name,omitempty"`
	ToolInput json.RawMessage `json:"tool_input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Raw       json.RawMessage `json:"raw,omitempty"`
}

// HookDecideResponse is the 200 body. The empty struct ({}) is "no
// decision": the hook prints nothing and the normal permission flow runs.
type HookDecideResponse struct {
	Decision string `json:"decision,omitempty"` // "deny" | ""
	Reason   string `json:"reason,omitempty"`
	Lock     string `json:"lock,omitempty"` // "lead_request" | "relay" | ""
	ID       string `json:"id,omitempty"`   // the request / op that holds the lock
}
```

Create `internal/team/hooklock.go`:

```go
package team

import (
	"path/filepath"
	"strings"
)

// HookLockPath is <dataDir>/hooklocks/<agent>/<sessionID>, the flag file
// of spec §6.6, or "" when any part is missing or could escape the
// directory: the agent must be one of the two known names and the session
// id a single path element (no separator, not "." or ".."). The CLI
// (writer and reader) and the daemon (remover) share this one rule; the
// hook's stdin is input any process on the host can produce, so the
// check is not optional on either side.
func HookLockPath(dataDir, agent, sessionID string) string {
	if dataDir == "" || sessionID == "" {
		return ""
	}
	if agent != HookAgentCC && agent != HookAgentCodex {
		return ""
	}
	if strings.ContainsAny(sessionID, `/\`) || sessionID == "." || sessionID == ".." || filepath.Base(sessionID) != sessionID {
		return ""
	}
	return filepath.Join(dataDir, HookLocksDir, agent, sessionID)
}
```

- [ ] **Step 4: Run the tests and verify they pass.**
  - Run: `go test ./internal/team/ -v`
  - Expected: PASS, including the existing `TestEventValue_*`, `TestApproval_*` tests and the three new ones.

- [ ] **Step 5: Commit.**
  ```bash
  git add internal/team/wire.go internal/team/hooklock.go internal/team/wire_hooks_test.go internal/team/hooklock_test.go
  git commit -m "feat(team): wire types for hook decisions and the hook lock flag path

  Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
  ```

---

### Task 2c.2: `daemonclient.WithGrace` — a per-client restart grace

**Files:**
- Modify: `cmd/pdx/daemonclient/client.go:121-123` (new option after `WithAttemptTimeout`), `:142-157` (field), `:161-176` (default), `:198-200` (`outage.remaining` takes the grace), `:293` and `:444` (the two call sites)
- Test: `cmd/pdx/daemonclient/grace_test.go`

**Interfaces:**
- Produces:
  ```go
  // WithGrace replaces Grace for this Client; d <= 0 is ignored.
  func WithGrace(d time.Duration) Option
  ```
- Consumes: the existing `outage`, `attemptCtx`, `wait` internals; the test uses `newTestClient`, `freeAddr`, `newFakeClock` from `client_test.go:45-197`.

- [ ] **Step 1: Write the failing test.**

`cmd/pdx/daemonclient/grace_test.go`:

```go
package daemonclient

import (
	"context"
	"errors"
	"io"
	"net/http"
	"testing"
	"time"
)

// WithGrace shortens the hard bound: against a port nobody listens on, Do
// gives up at exactly the given grace (the last sleep cut to the
// remainder), not at the 30 s default. `pdx hook` depends on this (spec
// §6.6: a 5 s grace, "a session must not stall on a daemon restart").
func TestDo_WithGraceShortensTheHardBound(t *testing.T) {
	addr := freeAddr(t) // nothing listens
	clock := newFakeClock()
	start := clock.now()
	c := newTestClient("http://"+addr, clock, io.Discard, WithGrace(5*time.Second))

	_, err := c.Do(context.Background(), http.MethodPost, "/api/hooks/decide", map[string]string{"x": "y"}, nil, Idempotent())
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
	if got := clock.elapsed(start); got != 5*time.Second {
		t.Fatalf("elapsed = %v, want exactly 5s", got)
	}
	// 0.25 + 0.5 + 1 + 1 + 1 + 1 = 4.75 s, then the remainder 0.25 s.
	if n := len(clock.sleeps); n != 7 || clock.sleeps[n-1] != 250*time.Millisecond {
		t.Fatalf("sleeps = %v, want 7 ending in 250ms", clock.sleeps)
	}
}

// Zero or negative is ignored: the default stays.
func TestWithGrace_IgnoresNonPositive(t *testing.T) {
	c := New("http://127.0.0.1:1", "t", WithGrace(0))
	if c.grace != Grace {
		t.Fatalf("grace = %v, want %v", c.grace, Grace)
	}
	c = New("http://127.0.0.1:1", "t", WithGrace(-time.Second))
	if c.grace != Grace {
		t.Fatalf("grace = %v, want %v", c.grace, Grace)
	}
}
```

- [ ] **Step 2: Run the test and verify it fails.**
  - Run: `go test ./cmd/pdx/daemonclient/ -run 'WithGrace' -v`
  - Expected: compile failure, `undefined: WithGrace` and `c.grace undefined (type *Client has no field or method grace)`.

- [ ] **Step 3: Implement.** Exact edits to `cmd/pdx/daemonclient/client.go`:

After line 123 (`func WithAttemptTimeout…`) insert:

```go

// WithGrace replaces Grace for this Client: how long Do keeps retrying
// after the first restart signal. `pdx hook` uses 5 s (spec §6.6: a
// session's tool call must not stall 30 s on a daemon restart); every
// other command keeps the default. d <= 0 is ignored.
func WithGrace(d time.Duration) Option {
	return func(c *Client) {
		if d > 0 {
			c.grace = d
		}
	}
}
```

In the `Client` struct (line 149, after `attemptTimeout time.Duration`) add `	grace          time.Duration`. In `New` (line 169, after `attemptTimeout: DefaultAttemptTimeout,`) add `		grace:          Grace,`.

Replace lines 198-200:

```go
// remaining is how much of grace is left; it is only meaningful once
// failures > 0.
func (o *outage) remaining(now time.Time, grace time.Duration) time.Duration {
	return grace - now.Sub(o.first)
}
```

Line 293: `rem := o.remaining(c.now())` → `rem := o.remaining(c.now(), c.grace)`. Line 444: `rem := o.remaining(now)` → `rem := o.remaining(now, c.grace)`.

- [ ] **Step 4: Run the tests and verify they pass.**
  - Run: `go test ./cmd/pdx/daemonclient/ -v`
  - Expected: PASS for every existing test (the four `*Grace*` tests still measure the 30 s default) plus `TestDo_WithGraceShortensTheHardBound` and `TestWithGrace_IgnoresNonPositive`.

- [ ] **Step 5: Commit.**
  ```bash
  git add cmd/pdx/daemonclient/client.go cmd/pdx/daemonclient/grace_test.go
  git commit -m "feat(daemonclient): WithGrace sets a per-client restart grace

  Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
  ```

---

### Task 2c.3: `POST /api/hooks/decide` — the lead lock answer

**Files:**
- Create: `internal/module/team/hooks.go` (the handler and `removeHookLock`; Task 2c.4 appends the prune)
- Modify: `internal/module/team/module.go:32-37` (field `dataDir`), `:106-111` (set in `Init`), `:114-122` (the route)
- Modify: `cmd/pdx/team_register_test.go:3-15` (imports), `:38-40` (two assertions), end of file (helper)
- Test: `internal/module/team/hooks_test.go` (the decide tests; Task 2c.4 appends the prune tests)

**Interfaces:**
- Produces: `POST /api/hooks/decide` → `200 team.HookDecideResponse` (`{}` = no decision), `400 bad_request` (agent not cc/codex, blank `event`, blank `session_id`, invalid JSON), `500 storage_error`, `503 not_ready` while stopping. **Any non-blank event other than `PreToolUse` / `PermissionRequest` (`PostToolUse`, `Stop`, `UserPromptSubmit`, `SessionEnd`, …) is answered `200 {}`, never 400** — P8a-1d forwards exactly those events to this route for the terminal-only degradation (`observeHookEvent` runs before this answer there), so a 400 here would block them. Behind `TokenAuth` through the general chain. Internal:
  ```go
  func (m *Module) handleHookDecide(w http.ResponseWriter, r *http.Request)
  func (m *Module) removeHookLock(agent, sessionID string)
  ```
- Consumes: `m.store.OpenByOrigin(sessionID, team.KindLead)`, `m.stopping()`, `m.decodeBody`, `m.writeJSON`, `m.writeErr`, `errStorage`; `team.HookLockPath` (Task 2c.1).

- [ ] **Step 1: Write the failing tests.**

`internal/module/team/hooks_test.go` (first half; Task 2c.4 appends two tests):

```go
package teammod

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/wake/purdex/internal/team"
)

// touchLock writes the flag file the CLI (P2c) or the mod (P6) would write.
func touchLock(t *testing.T, f *fixture, agent, sid string) string {
	t.Helper()
	p := filepath.Join(f.m.dataDir, team.HookLocksDir, agent, sid)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func decideReq(agent, event, sid string) team.HookDecideRequest {
	return team.HookDecideRequest{Agent: agent, Event: event, SessionID: sid, ToolName: "Bash",
		ToolInput: json.RawMessage(`{"command":"ls"}`), ToolUseID: "toolu_1", Raw: json.RawMessage(`{"session_id":"` + sid + `"}`)}
}

func decodeDecide(t *testing.T, body []byte) team.HookDecideResponse {
	t.Helper()
	var d team.HookDecideResponse
	if err := json.Unmarshal(body, &d); err != nil {
		t.Fatalf("decode HookDecideResponse: %v; body=%s", err, body)
	}
	return d
}

// Spec §6.6, §15 "flag + open lead request ⇒ PreToolUse deny with the
// reason, PermissionRequest {}": the lock answer comes from the session's
// open lead request, by origin_session_id; the request id is in the reason.
func TestHookDecide_OpenLeadRequestDeniesPreToolUseOnly(t *testing.T) {
	f := newFixture(t)
	ap := f.create(uid(1)) // origin sid-1
	code, body := f.do(http.MethodPost, "/api/hooks/decide", decideReq("cc", "PreToolUse", "sid-1"))
	if code != http.StatusOK {
		t.Fatalf("PreToolUse: %d %s", code, body)
	}
	d := decodeDecide(t, body)
	want := team.HookDecideResponse{Decision: "deny", Reason: fmt.Sprintf(team.LeadLockReasonFmt, ap.ID), Lock: team.HookLockLeadRequest, ID: ap.ID}
	if d != want {
		t.Fatalf("PreToolUse = %+v, want %+v", d, want)
	}
	if d.Reason != "lead 申請等待核准中（"+ap.ID+"），核准或拒絕前這個 session 不能執行工具；請在 Purdex 介面處理" {
		t.Fatalf("reason = %q", d.Reason)
	}
	code, body = f.do(http.MethodPost, "/api/hooks/decide", decideReq("cc", "PermissionRequest", "sid-1"))
	if code != http.StatusOK || string(body) != "{}\n" {
		t.Fatalf("PermissionRequest: %d %q, want 200 {}", code, body)
	}
	// Another session on the same host is not locked by sid-1's request.
	code, body = f.do(http.MethodPost, "/api/hooks/decide", decideReq("cc", "PreToolUse", "sid-2"))
	if code != http.StatusOK || string(body) != "{}\n" {
		t.Fatalf("other session: %d %q, want 200 {}", code, body)
	}
	if n := f.countOps("closed"); n != 0 {
		t.Fatalf("a hook decision must not close anything; closed events = %d", n)
	}
}

// Once the request is closed (any state) the lock is gone and the answer
// is {}; the flag the CLI left behind (SIGKILL) is removed with that
// answer, so the next hook finds no flag and makes no call.
func TestHookDecide_ClosedRequestAnswersEmptyAndRemovesTheFlag(t *testing.T) {
	f := newFixture(t)
	ap := f.create(uid(1))
	flag := touchLock(t, f, "cc", "sid-1")
	code, body := f.do(http.MethodPost, "/api/hooks/decide", decideReq("cc", "PreToolUse", "sid-1"))
	if code != http.StatusOK || decodeDecide(t, body).Decision != "deny" {
		t.Fatalf("while open: %d %s", code, body)
	}
	if !exists(flag) {
		t.Fatal("the flag must stay while the request is open")
	}
	f.do(http.MethodDelete, "/api/team/approvals/"+ap.ID, nil)
	code, body = f.do(http.MethodPost, "/api/hooks/decide", decideReq("cc", "PreToolUse", "sid-1"))
	if code != http.StatusOK || string(body) != "{}\n" {
		t.Fatalf("after close: %d %q, want 200 {}", code, body)
	}
	if exists(flag) {
		t.Fatal("a flag answered {} must be removed (stale flag costs one {} and then disappears)")
	}
	// No flag on disk is not an error either.
	if code, body = f.do(http.MethodPost, "/api/hooks/decide", decideReq("cc", "PreToolUse", "sid-1")); code != http.StatusOK || string(body) != "{}\n" {
		t.Fatalf("no flag: %d %q", code, body)
	}
}

func TestHookDecide_RejectsBadInputAndStopping(t *testing.T) {
	f := newFixture(t)
	for name, req := range map[string]team.HookDecideRequest{
		"agent":   decideReq("opencode", "PreToolUse", "sid-1"),
		"event":   decideReq("cc", " ", "sid-1"),
		"session": decideReq("cc", "PreToolUse", " "),
	} {
		code, body := f.do(http.MethodPost, "/api/hooks/decide", req)
		if code != http.StatusBadRequest || decodeErr(t, body).Error != team.ErrBadRequest {
			t.Errorf("%s: %d %s, want 400 bad_request", name, code, body)
		}
	}
	if code, body := f.do(http.MethodPost, "/api/hooks/decide", "{not json"); code != http.StatusBadRequest {
		t.Errorf("invalid JSON: %d %s", code, body)
	}
	// Any other known event is not a decision point for P2c: 200 {} even
	// while a lead request is open, and the flag stays (P8a-1d forwards
	// PostToolUse / Stop / UserPromptSubmit / SessionEnd here and must not
	// meet a 400).
	f.create(uid(1))
	flag := touchLock(t, f, "cc", "sid-1")
	for _, ev := range []string{"PostToolUse", "PostToolUseFailure", "Stop", "UserPromptSubmit", "SessionEnd", "Notification"} {
		if code, body := f.do(http.MethodPost, "/api/hooks/decide", decideReq("cc", ev, "sid-1")); code != http.StatusOK || string(body) != "{}\n" {
			t.Errorf("%s: %d %q, want 200 {}", ev, code, body)
		}
	}
	if !exists(flag) {
		t.Fatal("a non-decision event must not remove the lead lock flag")
	}
	// A session id that is not a single path element never touches the
	// disk: the answer is still {} and nothing outside locksDir is removed.
	outside := filepath.Join(f.m.dataDir, "keep.txt")
	if err := os.WriteFile(outside, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, body := f.do(http.MethodPost, "/api/hooks/decide", decideReq("cc", "PreToolUse", "../keep.txt")); code != http.StatusOK || string(body) != "{}\n" {
		t.Fatalf("traversal id: %d %q", code, body)
	}
	if !exists(outside) {
		t.Fatal("a traversal session id must not delete files outside hooklocks")
	}
	_ = f.m.Stop(context.Background())
	code, body := f.do(http.MethodPost, "/api/hooks/decide", decideReq("cc", "PreToolUse", "sid-1"))
	if code != http.StatusServiceUnavailable || decodeErr(t, body).Error != team.ErrNotReady {
		t.Fatalf("stopping: %d %s, want 503 not_ready", code, body)
	}
}
```

In `cmd/pdx/team_register_test.go`, after line 40 (`assert.Equal(t, http.StatusUnauthorized, doRequest(t, outer, http.MethodGet, "/api/team/inflight", "").Code)`) insert:

```go
	// P2c: the hook decision route is live and behind TokenAuth too.
	res = doRequestBody(t, outer, http.MethodPost, "/api/hooks/decide", "t", `{"agent":"cc","event":"PreToolUse","session_id":"sid-x"}`)
	assert.Equal(t, http.StatusOK, res.Code, res.Body.String())
	assert.JSONEq(t, `{}`, res.Body.String())
	assert.Equal(t, http.StatusUnauthorized, doRequestBody(t, outer, http.MethodPost, "/api/hooks/decide", "", `{"agent":"cc","event":"PreToolUse","session_id":"sid-x"}`).Code)
```

and at the end of the file:

```go

// doRequestBody is doRequest with a JSON body.
func doRequestBody(t *testing.T, h http.Handler, method, target, bearer, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}
```

with `"net/http/httptest"` and `"strings"` added to that file's imports (lines 3-15).

- [ ] **Step 2: Run the tests and verify they fail.**
  - Run: `go test ./internal/module/team/ -run 'TestHookDecide' -v`
  - Expected: compile failure, `f.m.dataDir undefined (type *Module has no field or method dataDir)`.
  - Run: `go test ./cmd/pdx/ -run 'TestRegisterServeModules_MountsTeam' -v`
  - Expected: FAIL, `Not equal: expected: 200 actual: 404` on the `/api/hooks/decide` line (the route is not mounted).

- [ ] **Step 3: Implement.**

Create `internal/module/team/hooks.go`:

```go
package teammod

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/wake/purdex/internal/team"
)

// handleHookDecide is POST /api/hooks/decide (spec §6.6): the lock answer
// for a session whose flag file exists. P2c knows one lock, the open lead
// request of the session: PreToolUse is denied with the spec's reason,
// PermissionRequest gets {} (the PreToolUse deny already stopped the call).
// No lock ⇒ {} — and the session's flag file is removed, so a flag whose
// writer died (a SIGKILLed `pdx lead request`) costs exactly one answered
// {} and then disappears (spec §6.6 "a stale flag costs one answered {}").
// P6 adds the relay lock before that removal; P8a adds the terminal-only
// kinds.
func (m *Module) handleHookDecide(w http.ResponseWriter, r *http.Request) {
	if m.stopping() {
		m.writeErr(w, http.StatusServiceUnavailable, team.ErrNotReady, "daemon is stopping", nil)
		return
	}
	var req team.HookDecideRequest
	if !m.decodeBody(w, r, &req) {
		return
	}
	if req.Agent != team.HookAgentCC && req.Agent != team.HookAgentCodex {
		m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, `agent must be "cc" or "codex"`, nil)
		return
	}
	if strings.TrimSpace(req.Event) == "" {
		m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, "event is required", nil)
		return
	}
	if strings.TrimSpace(req.SessionID) == "" {
		m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, "session_id is required", nil)
		return
	}
	if req.Event != team.HookEventPreToolUse && req.Event != team.HookEventPermissionRequest {
		// PostToolUse, PostToolUseFailure, Stop, UserPromptSubmit,
		// SessionEnd, …: no decision exists for them, and nothing is
		// removed. Always 200 {} — P8a-1d forwards these very events to
		// this route (its observeHookEvent runs above this line) and a 400
		// would stop the terminal-only degradation from ever closing.
		m.writeJSON(w, http.StatusOK, team.HookDecideResponse{})
		return
	}
	open, found, err := m.store.OpenByOrigin(req.SessionID, team.KindLead)
	if err != nil {
		m.logf("[team] hook decide %s: %v", req.SessionID, err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
		return
	}
	if !found {
		m.removeHookLock(req.Agent, req.SessionID)
		m.writeJSON(w, http.StatusOK, team.HookDecideResponse{})
		return
	}
	if req.Event != team.HookEventPreToolUse {
		m.writeJSON(w, http.StatusOK, team.HookDecideResponse{})
		return
	}
	m.logf("[team] hook deny: session %s tool %q while lead request %s is open", req.SessionID, req.ToolName, open.ID)
	m.writeJSON(w, http.StatusOK, team.HookDecideResponse{
		Decision: "deny",
		Reason:   fmt.Sprintf(team.LeadLockReasonFmt, open.ID),
		Lock:     team.HookLockLeadRequest,
		ID:       open.ID,
	})
}

// removeHookLock deletes the session's flag file; a missing file is fine.
func (m *Module) removeHookLock(agent, sessionID string) {
	p := team.HookLockPath(m.dataDir, agent, sessionID)
	if p == "" {
		return
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		m.logf("[team] remove hook lock %s: %v", p, err)
	}
}
```

Edits to `internal/module/team/module.go`:

After line 37 (`logf    func(format string, args ...any)`) add:

```go
	// dataDir is the daemon's data dir; the hook lock flags live under
	// <dataDir>/hooklocks (spec §6.6): the hook decide route removes a flag
	// it answered {} for, and the sweeper prunes flags of sessions the
	// registry no longer lists.
	dataDir string
```

After line 110 (`m.store = store`) add `	m.dataDir = c.Cfg.DataDir`.

Replace lines 114-122 with:

```go
// RegisterRoutes mounts the six /api/team/* routes and the hook decision
// route (Go method patterns).
func (m *Module) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/team/approvals", m.handleCreate)
	mux.HandleFunc("GET /api/team/approvals", m.handleList)
	mux.HandleFunc("GET /api/team/approvals/{id}", m.handleGet)
	mux.HandleFunc("DELETE /api/team/approvals/{id}", m.handleDelete)
	mux.HandleFunc("POST /api/team/approvals/{id}/decide", m.handleDecide)
	mux.HandleFunc("GET /api/team/inflight", m.handleInflight)
	mux.HandleFunc("POST /api/hooks/decide", m.handleHookDecide)
}
```

- [ ] **Step 4: Run the tests and verify they pass.**
  - Run: `go test ./internal/module/team/ -run 'TestHookDecide' -v && go test ./cmd/pdx/ -run 'TestRegisterServeModules_MountsTeam' -v`
  - Expected: PASS ×3 and PASS.

- [ ] **Step 5: Commit.**
  ```bash
  git add internal/module/team/hooks.go internal/module/team/hooks_test.go internal/module/team/module.go cmd/pdx/team_register_test.go
  git commit -m "feat(team): POST /api/hooks/decide answers the lead lock from the session's open request

  Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
  ```

---

### Task 2c.4: The sweeper prunes stale flags (10th tick and boot)

**Files:**
- Modify: `internal/module/team/hooks.go` (append `pruneHookLocks`), `internal/module/team/sweeper.go:44-58` (`tick`), `internal/module/team/module.go:132-135` (`Start`)
- Test: `internal/module/team/hooks_test.go` (append)

**Interfaces:**
- Produces: `func (m *Module) pruneHookLocks() int` — removes every `<dataDir>/hooklocks/cc/<sid>` with `!m.origins.LiveSession(sid)`; returns the count.
- Consumes: `OriginResolver.LiveSession` (answers `true` on a registry read error, so an unreadable registry prunes nothing; a missing registry dir is an empty registry and does prune — correct: no registry means no live CC session).

- [ ] **Step 1: Write the failing tests.** Append to `internal/module/team/hooks_test.go`:

```go

// Spec §15 "stale flag ⇒ {} and the sweeper removes it": a flag whose
// session the registry no longer lists goes on the 10th tick (the liveness
// cadence), whether or not any request is open; a live session's flag and
// the codex directory are left alone.
func TestTick_PrunesStaleFlagsOnTheTenthTick(t *testing.T) {
	f := newFixture(t)
	dead := touchLock(t, f, "cc", "sid-dead")
	live := touchLock(t, f, "cc", "sid-1")
	codex := touchLock(t, f, "codex", "sid-dead")
	f.origins.markDead("sid-dead")
	for i := 1; i <= 9; i++ {
		f.m.tick()
		if !exists(dead) {
			t.Fatalf("tick %d: pruned before the 10th tick", i)
		}
	}
	f.m.tick()
	if exists(dead) {
		t.Fatal("10th tick: the dead session's flag must be removed")
	}
	if !exists(live) {
		t.Fatal("10th tick: the live session's flag must stay")
	}
	if !exists(codex) {
		t.Fatal("codex flags have no liveness oracle in P2c and must be left alone")
	}
}

// Start prunes too (the registry is read on boot; a flag left by a session
// that died while the daemon was down must not outlive the restart).
func TestStart_PrunesStaleFlags(t *testing.T) {
	f := newFixture(t)
	dead := touchLock(t, f, "cc", "sid-dead")
	live := touchLock(t, f, "cc", "sid-1")
	f.origins.markDead("sid-dead")
	if err := f.m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if exists(dead) || !exists(live) {
		t.Fatalf("after Start: dead=%v live=%v, want false/true", exists(dead), exists(live))
	}
	if n := f.m.pruneHookLocks(); n != 0 {
		t.Fatalf("second prune removed %d, want 0", n)
	}
}
```

- [ ] **Step 2: Run the tests and verify they fail.**
  - Run: `go test ./internal/module/team/ -run 'TestTick_PrunesStaleFlags|TestStart_PrunesStaleFlags' -v`
  - Expected: compile failure, `f.m.pruneHookLocks undefined`.

- [ ] **Step 3: Implement.**

Append to `internal/module/team/hooks.go` (and add `"path/filepath"` to its imports):

```go

// pruneHookLocks removes every <dataDir>/hooklocks/cc/<session_id> whose session the
// registry no longer lists (spec §6.6 "the team sweeper deletes flags whose
// session is gone"). Only the cc directory: LiveSession is a CC registry
// check and codex flags have no liveness oracle yet (nobody writes them in
// P2c; a stale one goes through handleHookDecide's removal instead). A
// missing directory is nothing to prune. Returns how many were removed.
func (m *Module) pruneHookLocks() int {
	if m.dataDir == "" {
		return 0
	}
	dir := filepath.Join(m.dataDir, team.HookLocksDir, team.HookAgentCC)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			m.logf("[team] prune hook locks: %v", err)
		}
		return 0
	}
	n := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		sid := e.Name()
		if m.origins.LiveSession(sid) {
			continue
		}
		if err := os.Remove(filepath.Join(dir, sid)); err != nil && !errors.Is(err, os.ErrNotExist) {
			m.logf("[team] prune hook lock %s: %v", sid, err)
			continue
		}
		n++
	}
	if n > 0 {
		m.logf("[team] pruned %d stale hook lock flag(s)", n)
	}
	return n
}
```

In `internal/module/team/sweeper.go`, replace lines 44-58 (the head of `tick` up to and including `checkLive := …`) with:

```go
func (m *Module) tick() {
	m.tickN++
	checkLive := m.tickN%livenessEvery == 0
	if checkLive {
		// Flags outlive their request (spec §6.6): prune them on the same
		// cadence as the liveness check, whether or not anything is open.
		// With no flag on disk this is one ReadDir that answers ENOENT.
		m.pruneHookLocks()
	}
	open, err := m.store.ListOpen()
	if err != nil {
		m.logf("[team] sweep: %v", err)
		return
	}
	if len(open) == 0 {
		return
	}
	if m.afterListOpen != nil {
		m.afterListOpen()
	}
	now := m.now()
```

(the old `checkLive := m.tickN%livenessEvery == 0` line after `now := m.now()` is deleted; the loop below it is unchanged).

In `internal/module/team/module.go` `Start`, after the `if n > 0 { m.logf(... boot: extended the lease ...) }` block (line 134) and before `m.core.Events.OnSubscribe(m.sendSnapshot)` insert `	m.pruneHookLocks()`.

- [ ] **Step 4: Run the tests and verify they pass.**
  - Run: `go test -race ./internal/module/team/ -v 2>&1 | grep -E '^(--- |ok|FAIL)'`
  - Expected: PASS for every test in the package (the existing `TestTick_*`, `TestStart_*` included) and the two new ones.

- [ ] **Step 5: Commit.**
  ```bash
  git add internal/module/team/hooks.go internal/module/team/hooks_test.go internal/module/team/sweeper.go internal/module/team/module.go
  git commit -m "feat(team): sweeper prunes hook lock flags of sessions the registry no longer lists

  Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
  ```

---

### Task 2c.5: Codex installer — `PreToolUse` timeout 5 → 10; CC installer pinned untouched

**Files:**
- Modify: `internal/agent/codex/hooks.go:17-23` (the map and a constant)
- Test: `internal/agent/codex/hooks_locktimeout_test.go`, `internal/agent/cc/hooks_notimeout_test.go`

**Interfaces:**
- Produces: `codexHookTimeoutSeconds("PreToolUse") == 10`; every other key unchanged (3 for SessionEnd/Interrupt, 5 default). Written `hooks.json` entries carry it; a re-install over an existing `5` rewrites it (the installer strips and re-appends pdx-owned entries, `hooks.go:319-336`).
- Consumes: the existing test helpers `writeHooksFile`, `pdxGroupEntry`, `readHooksFile`, `hooksSection`, `codexMatcherGroups`, `toCodexEntrySlice`, `isPdxCommandCodex` (codex) and `mergeClaudeHooks`, `readSettings`, `hooksMap`, `toEntrySlice` (cc).

- [ ] **Step 1: Write the failing tests.**

`internal/agent/codex/hooks_locktimeout_test.go`:

```go
package codex

import (
	"path/filepath"
	"testing"
)

// codexTimeoutOf reads the timeout the installer wrote for one upstream key.
func codexTimeoutOf(t *testing.T, hooks map[string]any, key string) float64 {
	t.Helper()
	groups := codexMatcherGroups(hooks[key])
	if len(groups) != 1 {
		t.Fatalf("%s: %d matcher groups, want 1", key, len(groups))
	}
	inner := toCodexEntrySlice(groups[0].(map[string]any)["hooks"])
	m, _ := inner[0].(map[string]any)
	v, _ := m["timeout"].(float64)
	return v
}

// Lead-team spec §6.6 "the installer raises Codex's PreToolUse timeout from
// 5 to 10 s and leaves Claude Code's entries alone": PreToolUse is 10,
// PermissionRequest (answers {} at once) and every other default event stay
// 5, SessionEnd / Interrupt stay clamped at 3.
func TestCodexHookTimeoutSeconds_PreToolUseIsTen(t *testing.T) {
	if got := codexHookTimeoutSeconds("PreToolUse"); got != 10 {
		t.Fatalf("PreToolUse timeout = %d, want 10", got)
	}
	for _, key := range []string{"PermissionRequest", "PostToolUse", "SessionStart", "Stop", "UserPromptSubmit"} {
		if got := codexHookTimeoutSeconds(key); got != 5 {
			t.Errorf("%s timeout = %d, want 5 (only PreToolUse changes)", key, got)
		}
	}
	for _, key := range []string{"SessionEnd", "Interrupt"} {
		if got := codexHookTimeoutSeconds(key); got != 3 {
			t.Errorf("%s timeout = %d, want 3", key, got)
		}
	}
}

// The written hooks.json carries it, and a re-install over a file whose
// PreToolUse entry still says 5 rewrites that one entry (the installer
// strips and re-appends every pdx-owned entry) without touching the others
// or a third-party group under the same key.
func TestCodexInstallHooks_WritesPreToolUseTimeoutTenAndUpgradesFive(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	third := map[string]any{"hooks": []any{map[string]any{"type": "command", "command": "/usr/bin/notify pre", "timeout": 7}}}
	writeHooksFile(t, home, map[string]any{
		"PreToolUse":        []any{pdxGroupEntry("PdxPreToolUse"), third}, // the pre-P2c install: 5
		"PermissionRequest": []any{pdxGroupEntry("PdxPermissionRequest")},
	})
	if err := (&Provider{}).InstallHooks("/usr/local/bin/pdx"); err != nil {
		t.Fatalf("InstallHooks: %v", err)
	}
	hooks := hooksSection(t, readHooksFile(t, filepath.Join(home, ".codex", "hooks.json")))
	groups := codexMatcherGroups(hooks["PreToolUse"])
	if len(groups) != 2 {
		t.Fatalf("PreToolUse groups = %d, want the third-party one and ours", len(groups))
	}
	var ours, theirs float64
	for _, g := range groups {
		inner := toCodexEntrySlice(g.(map[string]any)["hooks"])
		m, _ := inner[0].(map[string]any)
		cmd, _ := m["command"].(string)
		v, _ := m["timeout"].(float64)
		if isPdxCommandCodex(cmd) {
			ours = v
		} else {
			theirs = v
		}
	}
	if ours != 10 {
		t.Errorf("our PreToolUse timeout = %v, want 10 (re-install must upgrade a 5)", ours)
	}
	if theirs != 7 {
		t.Errorf("third-party PreToolUse timeout = %v, want 7 untouched", theirs)
	}
	if got := codexTimeoutOf(t, hooks, "PermissionRequest"); got != 5 {
		t.Errorf("PermissionRequest timeout = %v, want 5", got)
	}
	if got := codexTimeoutOf(t, hooks, "PostToolUse"); got != 5 {
		t.Errorf("PostToolUse timeout = %v, want 5", got)
	}
	if got := codexTimeoutOf(t, hooks, "SessionEnd"); got != 3 {
		t.Errorf("SessionEnd timeout = %v, want 3", got)
	}
}
```

`internal/agent/cc/hooks_notimeout_test.go`:

```go
package cc

import (
	"path/filepath"
	"testing"
)

// Lead-team spec §6.6 "leaves Claude Code's entries alone": the CC installer
// writes no timeout and no matcher on any entry — PreToolUse and
// PermissionRequest included — so Claude Code's 600 s default applies and
// the P2c lock path (5 s) never comes near it. Pinned so a later "fix"
// does not quietly cap a hook that may wait.
func TestCCInstallHooks_EntriesCarryNoTimeoutOrMatcher(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	if err := mergeClaudeHooks(path, "/usr/local/bin/pdx", false); err != nil {
		t.Fatalf("mergeClaudeHooks: %v", err)
	}
	hooks := hooksMap(t, readSettings(t, path))
	for _, key := range []string{"PreToolUse", "PermissionRequest"} {
		if _, ok := hooks[key]; !ok {
			t.Fatalf("%s must be installed", key)
		}
	}
	n := 0
	for event, entries := range hooks {
		for _, entry := range toEntrySlice(entries) {
			em, _ := entry.(map[string]any)
			if _, has := em["matcher"]; has {
				t.Errorf("%s: entry carries a matcher: %v", event, em)
			}
			for _, inner := range toEntrySlice(em["hooks"]) {
				im, _ := inner.(map[string]any)
				if _, has := im["timeout"]; has {
					t.Errorf("%s: hook carries a timeout: %v", event, im)
				}
				n++
			}
		}
	}
	if n != len(hooks) {
		t.Fatalf("inner hooks = %d, want one per event (%d)", n, len(hooks))
	}
}
```

- [ ] **Step 2: Run the tests and verify the codex ones fail.**
  - Run: `go test ./internal/agent/codex/ -run 'PreToolUseIsTen|PreToolUseTimeoutTen' -v`
  - Expected (measured): FAIL —
    ```
    hooks_locktimeout_test.go:27: PreToolUse timeout = 5, want 10
    hooks_locktimeout_test.go:74: our PreToolUse timeout = 5, want 10 (re-install must upgrade a 5)
    ```
  - Run: `go test ./internal/agent/cc/ -run 'NoTimeoutOrMatcher' -v`
  - Expected: PASS already (it pins today's behaviour; it is the gate against a later change).

- [ ] **Step 3: Implement.** Replace `internal/agent/codex/hooks.go:17-23` with:

```go
// codexHookTimeouts is the per-event hook timeout in seconds. codex clamps
// SessionEnd and Interrupt to 3 s and warns at every start if the file says
// more (measured on codex-cli 0.153.4 startup, 2026-09-18). PreToolUse is
// the one event whose hook may wait for a daemon decision (lead-team spec
// §6.6: the flag-gated lock path, one 5 s budget shared with the event
// POST), so it gets room for that wait; PermissionRequest answers {} and
// stays at the default. Claude Code's entries carry no timeout at all
// (600 s default).
var codexHookTimeouts = map[string]int{
	"SessionEnd": 3,
	"Interrupt":  3,
	"PreToolUse": codexHookLockPathTimeout,
}

// codexHookLockPathTimeout is PreToolUse's timeout: the 5 s hook budget
// (cmd/pdx hookDecideGrace — the event POST and the decision run under it
// together) and the ≤ 5 s stdin read, with no room to spare in the worst
// case of both; the stdin read is instant in practice (codex writes the
// payload and closes). A hook that still times out does not block the
// call (spec M18, M20).
const codexHookLockPathTimeout = 10
```

- [ ] **Step 4: Run the tests and verify they pass.**
  - Run: `go test ./internal/agent/codex/ ./internal/agent/cc/`
  - Expected: `ok` for both (the existing `TestCodexHookTimeoutSeconds_ClampedEvents` and `TestCodexInstallHooks_WritesPerEventTimeout` do not name PreToolUse and stay green; `TestCodexInstallHooks_PreservesHooksStateTrustedHash` stays green).

- [ ] **Step 5: Commit.**
  ```bash
  git add internal/agent/codex/hooks.go internal/agent/codex/hooks_locktimeout_test.go internal/agent/cc/hooks_notimeout_test.go
  git commit -m "feat(codex): PreToolUse hook timeout 10 s for the lock path; CC entries pinned without timeout

  Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
  ```

- [ ] **Step 6 (P2c-1 gate, no commit):** `go build ./... && go vet ./cmd/pdx/... ./internal/team/ ./internal/module/team/ ./internal/agent/cc/ ./internal/agent/codex/ && go test -race ./cmd/pdx/ ./cmd/pdx/daemonclient/ ./internal/team/ ./internal/module/team/ ./internal/agent/cc/ ./internal/agent/codex/` — all `ok`. Open PR P2c-1.

---

### Task 2c.6: `pdx lead request` writes the flag after the 201 and removes it on every exit path

**Files:**
- Create: `cmd/pdx/hooklock.go`
- Modify: `cmd/pdx/lead.go:175-184` (after the create's error handling, before `hung := 0`)
- Modify (test helpers): `cmd/pdx/lead_test.go:138-157` (`fakeTeamDaemon` gains `noOrigin`), `:180-206` (create answers `Origin.SessionID`), after `:252` (`fakeLeadSessionID`), `:281-290` (`driveLeadHook` → `driveLeadDir`); `cmd/pdx/peers_test.go:354-375` (`writeTestConfigDataDir`)
- Test: `cmd/pdx/lead_hooklock_test.go`

**Interfaces:**
- Produces (package `main`):
  ```go
  func writeHookLock(path string, stderr io.Writer) // MkdirAll 0700 + empty file 0600; failure = one stderr line
  func removeHookLock(path string)                  // os.Remove, silent
  func hookLockExists(path string) bool             // one stat; "" is false (Task 2c.7's gate)
  ```
  stderr line when the flag cannot be placed: `pdx lead: 無法建立硬鎖旗標（<why>），這次只有軟鎖` (`<why>` is `data_dir 或 session id 為空` or the OS error).
- Consumes: `team.HookLockPath` (Task 2c.1), `cfg.DataDir`, `ap.Origin.SessionID` from the create answer.

- [ ] **Step 1: Write the failing tests.**

Edits to `cmd/pdx/lead_test.go`: in the `fakeTeamDaemon` struct (after line 156 `onFirstPoll     func()`) add `	noOrigin        bool // answer create without origin.session_id (an older daemon)`. In the create branch, replace line 183 (`status, openID, drop, refuse := …`) with `		status, openID, drop, refuse, noOrigin := f.createStatus, f.openID, f.dropFirstCreate, f.refuseCode, f.noOrigin` and replace lines 207-208 (`w.WriteHeader(status)` + the `Encode(team.Approval{ID: req.ID, Kind: req.Kind, State: team.StateOpen})`) with:

```go
		w.WriteHeader(status)
		ap := team.Approval{ID: req.ID, Kind: req.Kind, State: team.StateOpen, Origin: team.Origin{SessionID: fakeLeadSessionID}}
		if noOrigin {
			ap.Origin = team.Origin{}
		}
		json.NewEncoder(w).Encode(ap)
```

Before `func (f *fakeTeamDaemon) snapshot()` (line 252) add:

```go
// fakeLeadSessionID is the origin the fake daemon attributes every create
// to: the CC session id the hard-lock flag is named after (P2c).
const fakeLeadSessionID = "cc-sid-lead-1"

```

Replace `driveLeadHook` (lines 281-290) with:

```go
func driveLeadHook(t *testing.T, ctx context.Context, d http.Handler, opts []daemonclient.Option, onCancelled func(), args ...string) (int, string, string) {
	t.Helper()
	code, stdout, stderr, _ := driveLeadDir(t, ctx, d, opts, onCancelled, t.TempDir(), args...)
	return code, stdout, stderr
}

// driveLeadDir is driveLeadHook with the config's data_dir chosen by the
// test (the hard-lock flag lives under it; "" means the config has none)
// and returned, so the test can look for the flag.
func driveLeadDir(t *testing.T, ctx context.Context, d http.Handler, opts []daemonclient.Option, onCancelled func(), dataDir string, args ...string) (int, string, string, string) {
	t.Helper()
	srv := httptest.NewServer(d)
	defer srv.Close()
	cfgPath := writeTestConfigDataDir(t, srv.URL, "admin-tok", dataDir)
	var stdout, stderr bytes.Buffer
	full := append(append([]string{"request"}, args...), "--config", cfgPath)
	code := runLeadCmd(ctx, full, leadEnv(), &stdout, &stderr, fixedID(), onCancelled, opts...)
	return code, stdout.String(), stderr.String(), dataDir
}
```

In `cmd/pdx/peers_test.go`, replace lines 354-375 (`writeTestConfig`) with:

```go
func writeTestConfig(t *testing.T, addr, token string) string {
	t.Helper()
	return writeTestConfigDataDir(t, addr, token, "")
}

// writeTestConfigDataDir is writeTestConfig with data_dir set: the toml
// encoder writes data_dir = "" otherwise, so config.Load gives "" (measured
// 2026-10-07), and commands that put files under the data dir need a real
// one in tests.
func writeTestConfigDataDir(t *testing.T, addr, token, dataDir string) string {
	t.Helper()
	host, portStr, err := splitHostPort(addr)
	if err != nil {
		t.Fatalf("split host/port %q: %v", addr, err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("parse port %q: %v", portStr, err)
	}

	cfg := config.Config{
		Bind:    host,
		Port:    port,
		Token:   token,
		DataDir: dataDir,
	}
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := config.WriteFile(path, cfg); err != nil {
		t.Fatalf("config.WriteFile: %v", err)
	}
	return path
}
```

`cmd/pdx/lead_hooklock_test.go`:

```go
package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/wake/purdex/cmd/pdx/daemonclient"
	"github.com/wake/purdex/internal/team"
)

// leadFlagPath is where `pdx lead request` puts the hard-lock flag for the
// fake daemon's origin session (spec §6.6: <data_dir>/hooklocks/cc/<sid>).
func leadFlagPath(dataDir string) string {
	return filepath.Join(dataDir, team.HookLocksDir, team.HookAgentCC, fakeLeadSessionID)
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// The flag is up while the request is open (seen by the daemon at the
// first poll, i.e. after the 201) and gone once the request is approved.
func TestRunLeadCmd_FlagUpWhileOpenGoneAfterApproval(t *testing.T) {
	d := newFakeTeamDaemon(team.Approval{State: team.StateApproved, Grant: &team.Grant{MaxMembers: 3, Roots: []string{"/w"}}})
	dataDir := t.TempDir()
	var seenAtPoll atomic.Bool
	d.onPoll = func(n int) {
		if n == 1 {
			seenAtPoll.Store(fileExists(leadFlagPath(dataDir)))
		}
	}
	code, _, stderr, _ := driveLeadDir(t, context.Background(), d, nil, nil, dataDir, "--reason", "r")
	if code != ExitOK {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
	if !seenAtPoll.Load() {
		t.Fatal("the flag must exist while the request is open (at the first poll, after the 201)")
	}
	if fileExists(leadFlagPath(dataDir)) {
		t.Fatal("the flag must be removed once the request closed")
	}
	if fi, err := os.Stat(filepath.Dir(leadFlagPath(dataDir))); err != nil || !fi.IsDir() {
		t.Fatalf("the hooklocks cc directory must have been created (%v)", err)
	}
	if strings.Contains(stderr, "硬鎖") {
		t.Fatalf("no warning expected: %q", stderr)
	}
}

// Every other exit path removes it too: denial, timeout, the signal path
// (leadCancel) and the daemon-unavailable path out of the poll loop.
func TestRunLeadCmd_FlagRemovedOnEveryExitPath(t *testing.T) {
	t.Run("denied", func(t *testing.T) {
		d := newFakeTeamDaemon(team.Approval{State: team.StateDenied})
		code, _, _, dataDir := driveLeadDir(t, context.Background(), d, nil, nil, t.TempDir(), "--reason", "r")
		if code != ExitDenied || fileExists(leadFlagPath(dataDir)) {
			t.Fatalf("code=%d flag=%v", code, fileExists(leadFlagPath(dataDir)))
		}
	})
	t.Run("timeout", func(t *testing.T) {
		d := newFakeTeamDaemon(team.Approval{State: team.StateTimeout})
		code, _, _, dataDir := driveLeadDir(t, context.Background(), d, nil, nil, t.TempDir(), "--reason", "r")
		if code != ExitTimeout || fileExists(leadFlagPath(dataDir)) {
			t.Fatalf("code=%d flag=%v", code, fileExists(leadFlagPath(dataDir)))
		}
	})
	t.Run("signal", func(t *testing.T) {
		d := newFakeTeamDaemon(team.Approval{})
		d.hold = true
		dataDir := t.TempDir()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		var upWhileHeld atomic.Bool
		go func() {
			<-d.pollStarted
			upWhileHeld.Store(fileExists(leadFlagPath(dataDir)))
			cancel()
		}()
		code, _, _, _ := driveLeadDir(t, ctx, d, nil, nil, dataDir, "--reason", "r")
		if code != ExitCancelled {
			t.Fatalf("code=%d", code)
		}
		if !upWhileHeld.Load() {
			t.Fatal("the flag must be up while the poll is held")
		}
		if fileExists(leadFlagPath(dataDir)) {
			t.Fatal("the signal path must remove the flag")
		}
	})
	t.Run("hung polls exit 20", func(t *testing.T) {
		clock := newLeadClock()
		d := newFakeTeamDaemon(team.Approval{})
		d.hold = true
		d.onPoll = func(int) { clock.fireNext() } // every poll runs out its attempt timeout
		code, _, _, dataDir := driveLeadDir(t, context.Background(), d, []daemonclient.Option{clock.opt()}, nil, t.TempDir(), "--reason", "r")
		if code != ExitUnavailable || fileExists(leadFlagPath(dataDir)) {
			t.Fatalf("code=%d flag=%v", code, fileExists(leadFlagPath(dataDir)))
		}
	})
}

// A config without data_dir cannot place the flag: one stderr line, the
// request still runs (soft lock only), nothing is written anywhere.
func TestRunLeadCmd_NoDataDirWarnsAndRunsWithoutFlag(t *testing.T) {
	d := newFakeTeamDaemon(team.Approval{State: team.StateApproved})
	code, _, stderr, _ := driveLeadDir(t, context.Background(), d, nil, nil, "", "--reason", "r")
	if code != ExitOK {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
	if !strings.Contains(stderr, "pdx lead: 無法建立硬鎖旗標（data_dir 或 session id 為空），這次只有軟鎖") {
		t.Fatalf("stderr = %q", stderr)
	}
	if fileExists(filepath.Join(team.HookLocksDir, team.HookAgentCC, fakeLeadSessionID)) {
		t.Fatal("nothing may be written relative to the cwd")
	}
}

// A create answered without origin.session_id (an older daemon) is the
// same: warn, no flag.
func TestRunLeadCmd_NoOriginSessionIDWarns(t *testing.T) {
	d := newFakeTeamDaemon(team.Approval{State: team.StateApproved})
	d.noOrigin = true
	code, _, stderr, dataDir := driveLeadDir(t, context.Background(), d, nil, nil, t.TempDir(), "--reason", "r")
	if code != ExitOK || !strings.Contains(stderr, "這次只有軟鎖") {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
	if entries, _ := os.ReadDir(dataDir); len(entries) != 0 {
		t.Fatalf("data dir must stay empty: %v", entries)
	}
}
```

- [ ] **Step 2: Run the tests and verify they fail.**
  - Run: `go test ./cmd/pdx/ -run 'TestRunLeadCmd_Flag|TestRunLeadCmd_NoDataDir|TestRunLeadCmd_NoOrigin' -v`
  - Expected: FAIL — `the flag must exist while the request is open (at the first poll, after the 201)`, `the flag must be up while the poll is held`, and the two `stderr = ""` failures (no warning yet). The helpers compile (they are plain test code).

- [ ] **Step 3: Implement.**

Create `cmd/pdx/hooklock.go`:

```go
package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// writeHookLock creates the empty flag file at path (spec §6.6), making
// its directory first. Best effort: a failure is one stderr line and the
// request goes on with the soft lock only — the hard lock is an extra
// guard, never a reason to refuse a lead request.
func writeHookLock(path string, stderr io.Writer) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		fmt.Fprintf(stderr, "pdx lead: 無法建立硬鎖旗標（%v），這次只有軟鎖\n", err)
		return
	}
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		fmt.Fprintf(stderr, "pdx lead: 無法建立硬鎖旗標（%v），這次只有軟鎖\n", err)
	}
}

// removeHookLock deletes the flag. A file already gone (the daemon removed
// it with a {} answer) and any other failure are silent: the command is
// exiting and the daemon's sweeper prunes what is left.
func removeHookLock(path string) { _ = os.Remove(path) }

// hookLockExists is the gate `pdx hook` checks before it calls the daemon
// (spec §6.6): one stat, no daemon round trip when the flag is absent.
func hookLockExists(path string) bool {
	if path == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}
```

In `cmd/pdx/lead.go`, after line 182 (the closing `}` of `if err != nil { … leadReportErr … }` following the create `Do`) and before `hung := 0` insert:

```go
	// The hard lock (spec §6.6): while the request is open this session's
	// PreToolUse hooks ask the daemon, which denies them. The flag is the
	// gate the hook checks before calling; it goes up right after the
	// daemon confirmed the row and comes down on every exit path below —
	// approval, denial, timeout, the signal path through leadCancel, every
	// error. The session id is the daemon's attribution of this caller
	// (Approval.origin.session_id): the CLI knows only its inbox. A SIGKILL
	// skips the defer; the daemon then removes the flag with its first {}.
	if lock := team.HookLockPath(cfg.DataDir, team.HookAgentCC, ap.Origin.SessionID); lock == "" {
		fmt.Fprintln(stderr, "pdx lead: 無法建立硬鎖旗標（data_dir 或 session id 為空），這次只有軟鎖")
	} else {
		writeHookLock(lock, stderr)
		defer removeHookLock(lock)
	}
```

- [ ] **Step 4: Run the tests and verify they pass.**
  - Run: `go test -race ./cmd/pdx/ -run 'TestRunLeadCmd|TestLeadRequest|TestFixtures' -v 2>&1 | grep -E '^(--- |ok|FAIL)'`
  - Expected: PASS for every existing `TestRunLeadCmd_*` / `TestLeadRequest_*` test, the 4 new ones, and `TestFixtures_HoldNoV3AddressShapes` (the repo guard that scans test literals for the retired `<name>:<suffix>` address form; a first draft's message `hooklocks/cc must have been created: %v` tripped it — keep failure messages free of `<word>/<word>: <word>` shapes).

- [ ] **Step 5: Commit.**
  ```bash
  git add cmd/pdx/hooklock.go cmd/pdx/lead.go cmd/pdx/lead_test.go cmd/pdx/peers_test.go cmd/pdx/lead_hooklock_test.go
  git commit -m "feat(pdx): lead request writes the hook lock flag after the 201 and removes it on every exit

  Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
  ```

---

### Task 2c.7: `pdx hook` decision path — flag gate, one 5 s budget for the event POST and the decision, the deny JSON, always exit 0

**Files:**
- Modify: `cmd/pdx/hook.go:3-15` (imports), `:41` (constants and seams after `loadConfigFn`), `:43-47` (doc), `:73-75` (stdin read), `:77-87` (the event POST and the decision run concurrently), `:155-172` (`postHookEvent` takes a ctx), and new types + `hookDecision` after `runHook`
- Modify: `cmd/pdx/hook_test.go:60`, `:361`, `:411` — the three `postHookEventFn` stubs gain the leading `context.Context` parameter (`func(_ context.Context, _ string, _ string, payload hookPayload) error`); nothing else in them changes
- Test: `cmd/pdx/hook_decide_test.go`

**Interfaces:**
- Produces (package `main`):
  ```go
  var hookClientOpts []daemonclient.Option            // test seam, nil in production
  var hookAfterFn = func(d time.Duration, f func()) (stop func() bool) { t := time.AfterFunc(d, f); return t.Stop } // test seam: the budget timer
  var postHookEventFn func(ctx context.Context, url, token string, payload hookPayload) error // existing seam, now takes the budget ctx
  const hookStdinTimeoutS = 5
  const hookDecideGrace = 5 * time.Second             // the whole hook budget: event POST ∥ decision
  const hookDecideMaxInline = 64 << 10
  var hookDecideEvents = map[string]string{"PdxPreToolUse": "PreToolUse", "PdxPermissionRequest": "PermissionRequest"}
  type hookDecideInput struct{ DataDir, Base, Token, Agent, PurdexName string; Raw []byte; ClientOpts []daemonclient.Option }
  func hookDecision(ctx context.Context, in hookDecideInput) (out []byte, asked bool) // out nil = print nothing; asked = the daemon was asked (flag + decision event)
  ```
  stdout on a PreToolUse deny, exactly: `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"<reason>"}}` + `\n`. Nothing otherwise. `runHook` never exits non-zero on this path.
  **Timing contract (spec §6.6, §15):** `runHook` opens one budget context cancelled by `hookAfterFn(hookDecideGrace, cancel)`; the event POST (`postHookEventFn`, its own 2 s client timeout kept) runs in a goroutine under that ctx **concurrently** with `hookDecision`, whose client gets `WithGrace(hookDecideGrace)` + `WithAttemptTimeout(hookDecideGrace)` on the same ctx (the ctx carries a cancel, not a `Deadline()`, so `daemonclient` keeps its fake-able attempt timer — `client.go:289-291`). When the daemon was asked, `runHook` returns as soon as the decision returns (an answer, or the grace spent — that *is* the budget's end) and cancels the budget, which ends a still-running event POST; when it was not asked (no flag / another event), `runHook` waits for the event POST to finish or the budget to end, whichever first. End to end the hook holds the agent **≤ 5 s** on every path.
- Consumes: `readStdinWithTimeout` (`statusline_proxy.go:19`), `resolveDaemonHost` (`:145`), `hookLockExists` (Task 2c.6), `team.HookLockPath`, `team.HookDecideRequest/Response`, `daemonclient.New/Do/WithGrace/WithAttemptTimeout/Idempotent`.

- [ ] **Step 1: Write the failing tests.** `cmd/pdx/hook_decide_test.go`:

```go
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
// drop the hookLockExists check in hookDecision → red (the daemon is called).
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
	postHookEventFn = func(ctx context.Context, _ string, _ string, p hookPayload) error {
		postMu.Lock()
		posted = append(posted, p)
		hang := hangPost
		postMu.Unlock()
		ended := false
		if hang {
			<-ctx.Done()
			ended = true
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

	// Not a decision event: the event POST alone, and runHook waits for it
	// (a hook that returned before the POST ended would lose the event when
	// main exits). The stub returns at once here, so nothing to time.
	if got := run(t, srv.URL, "PdxPostToolUse"); got != "" {
		t.Fatalf("PostToolUse printed %q", got)
	}
	postMu.Lock()
	if len(posted) != 2 || d.calls() != 1 {
		t.Fatalf("PostToolUse: posts = %d (want 2), decides = %d (want 1)", len(posted), d.calls())
	}
	postMu.Unlock()

	// Daemon unreachable, event POST hanging until its ctx ends: one budget.
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	dead := ln.Addr().String()
	ln.Close()
	clock := newLeadClock()
	start := clock.now()
	hookClientOpts = []daemonclient.Option{clock.opt()}
	hookAfterFn = clock.afterFunc // the budget timer is the fake clock's too
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
	if len(posted) != 3 {
		t.Fatalf("event POST count = %d, want 3", len(posted))
	}
	if len(postCtxEnded) != 3 || !postCtxEnded[2] {
		t.Fatalf("the hanging event POST must have been ended by the budget ctx: %v", postCtxEnded)
	}
}
```

- [ ] **Step 2: Run the tests and verify they fail.**
  - Run: `go test ./cmd/pdx/ -run 'TestHookDecision|TestRunHook_DecisionPath' -v`
  - Expected: compile failure, `undefined: hookDecideInput`, `undefined: hookDecision`, `undefined: hookDecideGrace`, `undefined: hookClientOpts`, `undefined: hookAfterFn`, and `cannot use func(_ string, _ string, p hookPayload) error … as func(context.Context, string, string, hookPayload) error` on the three `hook_test.go` stubs.

- [ ] **Step 3: Implement.** Edits to `cmd/pdx/hook.go`:

Imports (lines 3-15) become:

```go
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
```

After line 41 (`var loadConfigFn = config.Load`) insert, and replace the `runHook` doc comment (lines 43-46) with the longer one:

```go

// hookClientOpts are appended to the decision client's options; nil in
// production, a fake clock in tests.
var hookClientOpts []daemonclient.Option

// hookAfterFn arms the hook's budget timer; time.AfterFunc in production,
// the fake clock's afterFunc in tests.
var hookAfterFn = func(d time.Duration, f func()) (stop func() bool) {
	t := time.AfterFunc(d, f)
	return t.Stop
}

// hookStdinTimeoutS bounds the stdin read (CC and Codex write the payload
// and close; a hook started by hand would otherwise hang forever).
const hookStdinTimeoutS = 5

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
```

Replace line 75 (`payload := buildHookPayload(tmuxSessionID, tmuxSession, purdexName, os.Stdin, agentType, provenance)`) with:

```go
	raw := readStdinWithTimeout(os.Stdin, hookStdinTimeoutS)
	payload := buildHookPayload(tmuxSessionID, tmuxSession, purdexName, bytes.NewReader(raw), agentType, provenance)
```

Replace lines 77-87 (from `cfg, err := loadConfigFn("")` to the closing `}` of `runHook`) with:

```go
	cfg, err := loadConfigFn("")
	var url, token string
	if err != nil {
		url = "http://127.0.0.1:7860/api/agent/event"
	} else {
		url = fmt.Sprintf("http://%s:%d/api/agent/event", cfg.Bind, cfg.Port)
		token = cfg.Token
	}

	// One budget for everything after the stdin read. The ctx carries a
	// cancel, not a Deadline(), so daemonclient keeps its own (fake-able)
	// attempt timer (client.go:289-291).
	budget, cancelBudget := context.WithCancel(context.Background())
	defer cancelBudget()
	stopBudget := hookAfterFn(hookDecideGrace, cancelBudget)
	defer stopBudget()

	eventDone := make(chan struct{})
	go func() {
		defer close(eventDone)
		_ = postHookEventFn(budget, url, token, payload) // its own 2 s client timeout, and the budget
	}()

	asked := false
	if err == nil { // no config: no data dir to find a flag in
		var out []byte
		out, asked = hookDecision(budget, hookDecideInput{
			DataDir: cfg.DataDir,
			Base:    fmt.Sprintf("http://%s:%d", resolveDaemonHost(cfg.Bind), cfg.Port),
			Token:   cfg.Token,
			Agent:   agentType, PurdexName: purdexName, Raw: raw,
			ClientOpts: hookClientOpts,
		})
		if len(out) > 0 {
			os.Stdout.Write(out)
		}
	}
	if asked {
		// The daemon answered, or the 5 s grace is spent — either way the
		// budget is over for this hook; a still-running event POST ends now
		// rather than holding the agent's tool call any longer.
		cancelBudget()
	}
	<-eventDone // ≤ 2 s on its own, ≤ the budget always; immediate after a cancel
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
```

`postHookEvent` (lines 155-172) takes the budget ctx; its 2 s client timeout stays — the request is bounded by whichever ends first:

```go
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
```

(the rest of the function is unchanged). `var postHookEventFn = postHookEvent` (line 40) keeps its name; the three stubs in `cmd/pdx/hook_test.go` (`:60`, `:361`, `:411`) become `func(_ context.Context, _ string, _ string, payload hookPayload) error` with their bodies untouched, and `hook_test.go` imports `context`.

**Why a cancel and not a ctx deadline:** `daemonclient` disables the per-attempt timeout when the caller's ctx has a `Deadline()` (`client.go:289-291`); the hook wants both of its bounds to be the fake-able timers, so the budget ctx is `context.WithCancel` armed by `hookAfterFn` (a `time.AfterFunc` in production, the fake clock in tests) and the client gets `WithAttemptTimeout` + `WithGrace`. **Worst case on every path is the 5 s budget, end to end**: health probe answers, decide hangs → 5 s; nothing listens → 5 s grace; `not_ready` through a restart → 5 s grace; the event POST runs *concurrently* under the same budget (its own 2 s client timeout is the shorter bound in practice), so the sum is never 5 s + 2 s. That is inside Codex's new 10 s (Task 2c.5) and far inside CC's 600 s default. The stdin read (≤ 5 s, `hookStdinTimeoutS`) precedes the budget and is not part of it — CC and Codex write the payload and close the pipe at once; the timeout exists for a hook started by hand.

- [ ] **Step 4: Run the tests and verify they pass.**
  - Run: `go vet ./cmd/pdx/ && go test -race ./cmd/pdx/ 2>&1 | tail -1`
  - Expected: `ok  	github.com/wake/purdex/cmd/pdx` (≈ 10 s under `-race`); in `-v`, `TestHookDecision_*` (6, with 5 subtests), `TestRunHook_DecisionPathEndToEnd`, and every existing `TestRunHook_*` / `TestBuildHookPayload_*` / `TestPostHookEvent*` pass.

- [ ] **Step 5: Commit.**
  ```bash
  git add cmd/pdx/hook.go cmd/pdx/hook_decide_test.go
  git commit -m "feat(pdx): hook asks the daemon's lock decision behind the flag file, event POST and decision under one 5 s budget, exit 0 always

  Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
  ```

- [ ] **Step 6 (P2c-2 gate, no commit):** `go build ./... && go vet ./cmd/pdx/... && go test -race ./cmd/pdx/ ./cmd/pdx/daemonclient/` — all `ok`. Open PR P2c-2.

### Manual acceptance after both PRs are deployed (mlab; not a gate)

1. In a CC session in tmux: `pdx lead request --reason test` in the foreground; from another pane `ls ~/.config/pdx/hooklocks/cc/` (mlab's daemon data dir is the default `~/.config/pdx`) shows the session id; in the locked session ask the model to run `echo hi` → the tool call is denied and the transcript shows `lead 申請等待核准中（<id>）…`; approve or deny on the App → the flag is gone and `echo hi` runs. (The App's dialog is P3b; before it merges, `POST /api/team/approvals/<id>/decide` with `{"decision":"deny","client":{"kind":"app","label":"curl @ mlab"}}` and the bearer token stands in.)
2. `kill -9` a running `pdx lead request` → the flag stays; the next tool call gets `{}` and the flag disappears (daemon log `[team] remove hook lock`); within 30 s the request is `abandoned`.
3. `pdx stop`, then a tool call in a flagged session: it runs after ≤ 5 s (`pdx hook` printed nothing); `pdx start`.
4. Codex (M20's open point): `pdx setup --agent codex` on a host whose `hooks.json` had `PreToolUse` at 5; start `codex` and note whether `/hooks` asks to re-approve `PreToolUse` (the `trusted_hash` question). Record the answer in the spec at §6.6 "Hook timeouts". Existing installs keep `5` until re-installed — `CheckHooks` does not flag it (see Open questions 3).

### Deviations from the preamble

1. **`team.HookLockPath(dataDir, agent, sessionID)` was added to the leaf `internal/team`** (not in the preamble's wire list): the CLI writes and reads the flag, the daemon removes and prunes it, and the path rule (two agents, a single-element session id, no `..`) must be one function. The daemon-side tests of path traversal rest on it.
2. **Named constants beside the preamble's two structs:** `HookAgentCC/Codex`, `HookEventPreToolUse/PermissionRequest`, `HookLockLeadRequest/Relay`, `LeadLockReasonFmt`. The preamble carried the agent/event/lock strings and the reason as comments; the tests pin them as constants so P6/P8a reuse them.
3. **`daemonclient.WithGrace(d)`** is the "smallest addition" the preamble asked for: a `grace` field defaulting to the `Grace` constant, `outage.remaining(now, grace)`, two call sites. No test of the existing 27 changed.
4. **The decide handler removes the flag when it answers `{}` for a session with no open lead request** (`handleHookDecide` → `removeHookLock`). The spec's sentence "a stale flag costs one answered `{}` and then disappears" is thereby literal even when the session is alive (a SIGKILLed `pdx lead request`, the Bash tool's kill after its default 2 min); the sweeper's registry-liveness prune (Task 2c.4) covers dead sessions. **P6 must insert its relay-lock check before this removal** or the mod's flag would vanish on the first tool call of a relay. Flagged under Open questions 2.
5. **The sweeper prunes `hooklocks/cc/` only.** `LiveSession` is a CC registry check; a `codex/` flag would always read as dead. Nobody writes codex flags in P2c; a stale one disappears through deviation 4.
6. **`PermissionRequest` answers the empty `{}`, not `{lock, id}`.** The preamble's table says `{}`; the `Lock`/`ID` fields are filled only on the deny. Trivial to change if the coordinator wants the lock holder visible on both.
7. **The hook sends `raw` (the whole stdin) on every decide call, except when the stdin exceeds 64 KiB** — then `tool_input` and `raw` are dropped and only the ids go (`hookDecideMaxInline`). Without this a large `Write` would exceed the daemon's 1 MiB `bodyCap` → 400 → no decision → the lock bypassed for exactly the biggest writes. P8a's `AskUserQuestion` payloads are small and unaffected.
8. **`pdx lead request` prints one stderr line when it cannot place the flag** — `pdx lead: 無法建立硬鎖旗標（<why>），這次只有軟鎖` — and goes on with the soft lock (an empty `data_dir`, a create answer without `origin.session_id` from an older daemon, an `mkdir`/write error). A new user-visible string the spec does not list; it is a diagnostic, not a flow change.
9. **The hook's stdin read now has a 5 s timeout** (`readStdinWithTimeout`, reused from `statusline_proxy.go`), as the task asked; the measured absence of any timeout is M17's `io.ReadAll`.
10. **Test seams and helpers:** `hookClientOpts` (package var, nil in production) injects the fake clock into the end-to-end `runHook` test and `hookAfterFn` (package var, `time.AfterFunc` in production) lets the same fake clock own the hook's budget timer; `postHookEventFn` gains a leading `context.Context` (the budget) — the three existing stubs in `hook_test.go` change their signature only; `writeTestConfigDataDir` (the existing `writeTestConfig` wrote `data_dir = ""`, measured); `fakeTeamDaemon` answers `Origin.SessionID` on create and gains `noOrigin`; `driveLeadDir` returns the data dir. No production code path changed for them beyond `hookClientOpts`, `hookAfterFn` and the ctx parameter.
11. **Two PRs, not one.** The preamble estimated ≈ 500 lines; measured ≈ 1 480 (≈ 62 % tests). Split at the daemon/CLI seam, each under 800.
12. **Spec §12's P2c row says "installer `timeout: 600` on the two events"; §6.6 says "5 → 10 for `PreToolUse`, Claude Code's entries alone".** The task text and §6.6 agree; this plan follows them. Open question 1.

### Size estimate

Measured in the scratch build (`diff -u … | grep -c '^+[^+]'` for edits, `wc -l` for new files):

**P2c-1 — wire, client grace, daemon route, sweeper, installers (Tasks 2c.1–2c.5):**

| File | Lines |
|---|---|
| `internal/team/wire.go` | +42 |
| `internal/team/hooklock.go` (new) | 26 |
| `internal/team/wire_hooks_test.go` (new) | 79 |
| `internal/team/hooklock_test.go` (new) | 27 |
| `cmd/pdx/daemonclient/client.go` | +19 −4 |
| `cmd/pdx/daemonclient/grace_test.go` (new) | 45 |
| `internal/module/team/hooks.go` (new) | 116 |
| `internal/module/team/hooks_test.go` (new) | 185 |
| `internal/module/team/module.go` | +10 −1 |
| `internal/module/team/sweeper.go` | +7 −1 |
| `cmd/pdx/team_register_test.go` | +19 |
| `internal/agent/codex/hooks.go` | +11 −1 |
| `internal/agent/codex/hooks_locktimeout_test.go` (new) | 88 |
| `internal/agent/cc/hooks_notimeout_test.go` (new) | 44 |
| **Total** | **≈ 718 added, 14 files** |

**P2c-2 — CLI (Tasks 2c.6–2c.7):**

| File | Lines |
|---|---|
| `cmd/pdx/hooklock.go` (new) | 37 |
| `cmd/pdx/lead.go` | +14 |
| `cmd/pdx/lead_test.go` | +20 −4 |
| `cmd/pdx/peers_test.go` | +12 −3 |
| `cmd/pdx/lead_hooklock_test.go` (new) | 135 |
| `cmd/pdx/hook.go` | +152 −12 |
| `cmd/pdx/hook_test.go` | +4 −3 |
| `cmd/pdx/hook_decide_test.go` (new) | 418 |
| **Total** | **≈ 792 added, 8 files** |

Both under the 800-line bound and the 20-file bound. If the coordinator wants one PR anyway it is ≈ 1 510 lines / 22 files.

### Open questions for the coordinator

1. **Spec §12 vs §6.6 on the Codex timeout.** §12's P2c row reads "installer `timeout: 600` on the two events"; §6.6 "Hook timeouts" reads "raises Codex's `PreToolUse` timeout from 5 to 10 s and leaves Claude Code's entries alone", which the task text repeats. Implemented per §6.6. Please align the §12 row when P2c ships (one-line spec edit, no code).
2. **The `{}`-answer flag removal (deviation 4) binds P6:** `handleHookDecide` must check the relay op before `removeHookLock`, or add the relay lock's answer above it. Say so in P6's handoff. The alternative — no removal in the handler, sweeper only — leaves a live session with a dead `pdx lead request` paying one daemon round trip per tool call (and up to 5 s each during a daemon restart) until the session ends.
3. **Existing Codex installs keep `PreToolUse: 5` until re-installed**, and `CheckHooks` does not report the drift (it never reads `timeout`). Deploy note: run `pdx setup --agent codex` (or Hosts › Hooks › Install) on each host after P2c-1 lands. Should `CheckHooks` learn to flag a pdx-owned entry whose `timeout` differs from `codexHookTimeoutSeconds(key)`? Not done here (it would turn every pre-P2c install into *not installed*).
4. **The deny is logged once per denied tool call** (`[team] hook deny: session … tool … while lead request … is open`). A model that keeps trying while locked produces one line per attempt. Keep (audit of what the lock stopped), or log only the first per request?
5. **`permission_mode` is not a top-level field** of `HookDecideRequest` (spec §6.6 lists it; the preamble's wire type does not). It travels inside `raw` when the stdin is ≤ 64 KiB. P8a may promote it when the terminal-only kinds need it.

### Mutation gates

Each line is a change to the implementation and the test that must go red; the ones marked *verified* were run in the scratch build.

- Drop the `hookLockExists(...)` gate in `hookDecision` (`cmd/pdx/hook.go`) → `TestHookDecision_NoFlagMeansNoCallAndNoOutput` red: `PdxPreToolUse without a flag printed "{\"hookSpecificOutput\":…deny…}"`. *Verified.*
- `os.Exit(1)` on the `client.Do` error in `hookDecision` → `TestRunHook_DecisionPathEndToEnd` red (the test binary exits 1 on the unreachable-daemon run). *Verified.*
- Run the event POST and the decision sequentially in `runHook` (`_ = postHookEventFn(budget, …)` inline before `hookDecision`, no goroutine) → `TestRunHook_DecisionPathEndToEnd` red: the hanging POST stub never returns before the decision starts, the test's 5 s wall-time `select` fires (`runHook did not return within 5 s of wall time`). Variant: keep the goroutine but drop `if asked { cancelBudget() }` → the hanging POST waits for a budget timer the fake clock never fires → the same red.
- Remove `"PreToolUse": codexHookLockPathTimeout` from `codexHookTimeouts` → `TestCodexHookTimeoutSeconds_PreToolUseIsTen` and `TestCodexInstallHooks_WritesPreToolUseTimeoutTenAndUpgradesFive` red (`PreToolUse timeout = 5, want 10`). *Verified.*
- Answer the deny on `PermissionRequest` too (drop the `req.Event != team.HookEventPreToolUse` branch in `handleHookDecide`) → `TestHookDecide_OpenLeadRequestDeniesPreToolUseOnly` red. *Verified.*
- Prune on every tick (`if checkLive` → `if true` in `tick`) → `TestTick_PrunesStaleFlagsOnTheTenthTick` red (`tick 1: pruned before the 10th tick`). *Verified.*
- Ignore `WithGrace` (keep `Grace` in `outage.remaining`) → `TestDo_WithGraceShortensTheHardBound` red (`elapsed = 30s, want exactly 5s`).
- Skip `m.removeHookLock` in the `!found` branch → `TestHookDecide_ClosedRequestAnswersEmptyAndRemovesTheFlag` red.
- Prune without consulting `LiveSession` → `TestTick_PrunesStaleFlagsOnTheTenthTick` red (`the live session's flag must stay`).
- Drop the `defer removeHookLock(lock)` in `lead.go` → `TestRunLeadCmd_FlagRemovedOnEveryExitPath` red on all four subtests and `TestRunLeadCmd_FlagUpWhileOpenGoneAfterApproval` red.
- Add `"timeout": 5` to `makePdxEntry` in the CC installer → `TestCCInstallHooks_EntriesCarryNoTimeoutOrMatcher` red.
- Drop the `hookDecideMaxInline` trimming → `TestHookDecision_LargeStdinSendsIdsOnly` red (`raw=2097…`, and against a real daemon a 400).

---

# PR P5a — daemon relay core (spec §8.1, §8.3, §8.4, §8.7, §9.3 self ops, §12, §14, §15)

> Written by the P5a writer, 2026-10-07, against origin/main alpha.527 (`746759d2`). **Every Go and TS block below was compiled and run** in a scratch copy of the worktree (`scratchpad/plan2-build/p5a`, node_modules symlinked): `go vet ./...` clean, `go test ./internal/... ./cmd/pdx/` green except `internal/module/dev` (needs `.git`, which the scratch copy has not — unrelated), `npx vitest run` green on every touched SPA file plus `src/components/hosts`, `src/lib/team`, `src/lib/register-modules.test.ts`; `npx tsc -p tsconfig.app.json --noEmit` and `pnpm run lint` clean. The "Expected" outputs are the measured ones.

### Contract problems

None that stop the contract. One line of the preamble cannot be done literally and is handled under Deviations 1: *"Lineage … written at state=cleared in one tx with the title move (peer_labels)"* — `peer_labels` lives in **meta.db** (`internal/store/meta.go:120`), `session_lineage` in **team.db**; `database/sql` has no cross-database transaction. The lineage row and the op's state change are one team.db transaction; the title move follows at once in its own meta.db transaction, is idempotent, and is re-run by the boot reconciliation for every op still in `cleared`.

### What must be merged first

- **P2c** (preamble order) — nothing here depends on it technically; it is the agreed order.
- Everything P2a-1 … P3b shipped: `internal/team/wire.go` as on `746759d2`, `internal/module/team/*` (CAS `CloseIfOpen` / `CloseIfExpired`, `closeWith`, `eventMu`), `internal/module/peers/origin_resolver.go`, `cmd/pdx/daemonclient`, `cmd/pdx/lead.go` + `exitcodes.go`, `spa/src/lib/team/*`, `ApprovalDialogHost.tsx`.
- Inside P5a the order is **P5a-0 → P5a-1a → P5a-1b → P5a-2a → P5a-2b → P5a-2c → P5a-3a → P5a-3b** (the eight PRs the size estimate forces — P5a-0 and the Task 5a.6 move came out of the codex round; each is independently green). P5b consumes the routes from P5a-2b and the CLI from P5a-2c; nothing in the mod is touched here.

### Scope of P5a (what the three parts own)

- **P5a-0 / P5a-1** — contract, store and identity: the wire contract alone in **P5a-0** (`wire_relay.go`, `APIError.Op`); then `relay_ops`, `session_lineage`, `session_prefs` tables in `team.db` with idempotent `ReportRelay` transitions and the lineage row written at `cleared`; `PeerRecord.previous_refs` (uncapped, newest first) filled from the team module's `LineageReader` through the registry; the `ipeers.Resolve` lineage tier (a live ref always wins); `peerNotFoundHint` says "renames and relays"; `PeerLabelStore.Move` (the title move); the peers resolver gains `ResolveOriginBySession`; the host config section `relay {self_solo, self_lead}` (hostconfig module, defaults true — Task 5a.6, in P5a-1b). **Final PR list of P5a (eight):** P5a-0 (Task 5a.1) · P5a-1a (5a.2, 5a.3) · P5a-1b (5a.4, 5a.5, 5a.6) · P5a-2a (5a.7) · P5a-2b (5a.8) · P5a-2c (5a.9) · P5a-3a (5a.10, 5a.11) · P5a-3b (5a.12–5a.14).
- **P5a-2** — daemon behaviour and CLI: `session_prefs` pause; `self_relay` approval rows opened only by `POST /api/relay/begin`, their close moving the op (approve → `claimed`; deny / timeout / abandon → `cancelled{denied|timeout|abandoned}`); the six `/api/relay/*` routes; `relays_active` in inflight; boot reconciliation of **self** ops; `cmd/pdx/relay.go` with spec §14 exit codes.
- **P5a-3** — the retention sweeper (hourly + boot), `pdx peers` `(was _xxxxxx)`, the Hosts 「接力」 section, the `self_relay` dialog body with 「這個 session 不再詢問」, the notification title for a relay request.

**Deferred and said so:** `teams.lead_session_id` / member-row moves at `cleared` (spec §8.4) and persisted usage on team rows (§8.5) need the team tables — **P4** (the `cleared` transaction in `ReportRelay` is where P4 adds its two UPDATEs); the lead-handover notice to members (§8.4) — **P4**; `pdx relay <ref>`, `claim`, the control message, `requested`-op reconciliation, **boot reconciliation from frames** (spec §9.3 "past `claimed`": a self op whose pane's verified frame shows another session id is `cleared` with that id — P5a-2b's `reconcileRelays` handles `awaiting_approval` and `cleared` only) and the member lock — **P6**; the restart-confirm line `N 個接力進行中` — **P6** (spec §9.5; the daemon already fills `relays_active` here); member role detection (`relayRole`) returns `"none"` until P4 fills it from team rows.

**Verified code facts (worktree line numbers, `746759d2`):**
- `internal/team/wire.go`: `KindSelfRelay` with the P2 comment `:16`; `APIError` `:121-126`; limits `:44-53`. The file is kept and ADDED to (a sibling file `wire_relay.go` plus one field on `APIError`).
- `internal/module/team/store.go`: `OpenStore` migrates at `:39-60` (`CREATE TABLE IF NOT EXISTS approval_requests …`), the relay schema is added right after `:60`; `rowScanner` `:70`; `Create` `:125-149`; `CloseIfOpen` / `CloseIfExpired` / `closeWhere` `:165-224`; `OpenByOrigin` `:275-285`.
- `internal/module/team/module.go`: imports `:3-16`; `OriginResolver` `:26-29`; `Module` fields `:33-37`; `New` `:78-87`; `Dependencies` `:90`; `Init` `:95-112`; `RegisterRoutes` `:115-122`; `Start` `:127-140` (`sweepWG.Add(1); go m.runSweeper()` at `:136-137`); `closeWith` `:187-197` (the winner branch `:192-195`); `mu`/`waiters` `:52-53`.
- `internal/module/team/handler.go`: the kind switch `:120-128`; `requestHash` `:93-98`; `handleInflight` `:247-255`; `handleGet` `:279-324` (renews the lease, `r.PathValue("id")`); the grant block in `handleDecide` `:387-410`; `writeErr` `:36-38`, `decodeBody` `:41-52`, `errStorage` `:26`.
- `internal/module/team/handler_test.go`: `fakeOrigins` `:29-55`, `fixtureOrigins` `:40-43` (sid-1 at `/tmp/10.sock`, sid-2 at `/tmp/20.sock`), `uid` `:58`, `fixture` `:77-85`, `newFixture` `:88-109`, `f.do` `:111-129`, `decodeApproval` / `decodeErr` `:146-162`, `events` / `countOps` `:165-194`. `sweeper.go`: `livenessEvery = 10` `:15`, `tick` `:44-81`.
- `internal/peers/record.go`: `PeerRecord.Ref` `:55`; `BuildInput.Contexts` `:132`; the per-row attach loop `:166-173`. `internal/peers/address.go`: the combined-form predicate `:230-233`; `resolveRefHead` `:351-368`; `hasLiveEntry` `:376-378`. Test helpers `liveRow` / `inboxDeadRow` (`address_test.go:22-47`).
- `internal/module/peers/module.go`: imports `:30-41`; `localEnvelope`'s `ipeers.Build(ipeers.BuildInput{…})` `:789-805`; `titleSnapshot` `:822`; `m.core.Registry` is the `*core.ServiceRegistry` (`internal/core/registry.go:20-32`). `send.go:54` `peerNotFoundHint`; its test `send_test.go:1470-1492`. `origin_resolver.go:28-58` `ResolveOrigin`; its fixture `origin_resolver_test.go:18-32`.
- `internal/store/peer_label.go`: `Claim` `:63-83`, `Release` `:98-128` (write-first transaction rationale `:88-97`), `nextRev` `:48-52`. Table in `meta.go:120-137`.
- `internal/module/hostconfig/`: keys `store.go:16-21`; `handleGet` field map `handler.go:57-63`; `emptyFor` `:48-53`; `putHandler` `:76-129`; routes `module.go:35-42`; `Init` `:27-32`; `TestHandlerGetEmpty` asserts the whole GET body `handler_test.go:15-25`; `newTestModule` / `serve` helpers in `module_test.go`.
- `cmd/pdx/main.go`: usage line `:43`; the switch `:47-80` (`case "lead"` `:70-71`); `registerServeModules` `:343-376` (`titles` `:352-358`, `teammod.New()` `:365-366`). `exitcodes.go:7-18`. `peers.go:666-669` `displayAddress`; `addressWithRef` `address_render.go:30-35`; `sanitizeCell` `peers.go:54`. `lead.go` is the template for `relay.go` (`runLeadCmd` `:132-211`, `leadReportErr` `:231-255`, `leadFinish` `:265-298`, `leadDecidedBy` `:301-306`); `lead_test.go`: `leadClockOpt` `:117`, `writeTestConfig` `peers_test.go:354`.
- `daemonclient`: `Do(ctx, method, path, body, out, ...RequestOption)` `client.go:219`, `Idempotent()` `:139`, `WithAttemptTimeout` `:123`, `WithStderr` `:126`, `DefaultAttemptTimeout` `:44`, `ErrUnavailable` / `ErrUnsupported` / `ErrNoAnswer` `:67-75`, `StatusError{API team.APIError}` `:86`.
- SPA: `host-config-api.ts:15` (`QuickReply`), `:19-25` (`HostConfigPayload`), `:30-36` (collection map); `useHostConfigStore.ts:5-17` imports, `:22-35` entry, `:37-47` empty, `:54-63` state, `:168-182` load patch, `:207-210` savers; `register-modules/index.tsx:63` import, `:379-391` `setHostBuiltinSections`; `register-modules.test.ts:82` the localId list; `ToggleSwitch` (`settings/ToggleSwitch.tsx`, `role="switch"`, `aria-checked`); `HostConfigNotice.tsx:16-31` `useHostConfigGate`; `host-config-queue.ts` `hostConfigQueueKey` / `queueHostConfigSave`; `team/types.ts:71-75` `Grant`, `:175` `leadPayloadOf`; `team/approval-api.ts:63-78` `send`, `:95` `count`; `team/approval-notify.ts:39-50`; `ApprovalDialogHost.tsx` (whole file, 236 lines, rewritten below); locales `en.json` / `zh-TW.json`: `"hosts.peers"` `:1322`, `"approval.restart.pending"` `:1948` (last key); `locale-completeness.test.ts:113-120` the pinned-strings block. `useUndoToast.show(message)` (`stores/useUndoToast.ts:24`).

---

## PR P5a-0 — the wire contract

**Scope.** The leaf contract alone (`internal/team/wire_relay.go`, `APIError.Op`): contract first, as P2a-1 did. Nothing consumes it until P5a-1a; a running daemon is unchanged. (Split out of P5a-1a by the codex round so that every P5a PR is ≤ 800 lines.)

### Task 5a.1: The wire contract — `internal/team/wire_relay.go` and `APIError.Op`

**Files:**
- Create: `internal/team/wire_relay.go`
- Modify: `internal/team/wire.go:125` (one field after `Approval` in `APIError`)
- Test: `internal/team/wire_relay_test.go`

**Interfaces:**
- Produces (package `team`, all exact): `RelayKind` (`RelayKindSelf`, `RelayKindMember`), `RelayState` with the nine states and `func (RelayState) Terminal() bool`, the eight `RelayReason*` constants, error codes `ErrMemberRelayIsLeads`, `ErrSelfRelayOff`, `ErrSelfRelayPaused`, `ErrRelayOpen`, `ErrUnknownSession`, `ErrBadTransition`, limits `RelayThresholdPct = 70`, `RelayMinGrowth = 20000`, `SelfRelayDeadlineS = 600`, `RelayDir = "relay"`, types `RelayOp`, `SelfRelayPayload`, `RelayHelloRequest`, `RelayHelloResponse`, `RelayBeginRequest`, `RelayBeginResponse`, `RelaySelfRequest`, `RelaySelfResponse`, `RelayReportRequest`, `LineageReaderKey = "team.lineage"`, `LineageReader interface { PreviousRefs() (map[string][]string, error) }`; `APIError` gains `Op *RelayOp \`json:"op,omitempty"\``.
- Consumes: `encoding/json` only.

- [ ] **Step 1: Write the failing test.**

```go
package team

import (
	"encoding/json"
	"testing"
)

// The JSON names are the contract across Go, CLI, SPA and the mod (plan
// preamble "Wire additions"); this pins them and the omitempty of the
// optional fields.
func TestRelayOp_JSONNames(t *testing.T) {
	pct := 72.4
	op := RelayOp{ID: "op", Kind: RelayKindSelf, HostID: "h", SessionID: "s", Ref: "_abc123", RequestID: "r",
		State: RelayAwaitingApproval, HandoffPath: "/d/relay/op.md", UsedPercentage: &pct, CreatedAt: 1, UpdatedAt: 2}
	b, err := json.Marshal(op)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"id":"op","kind":"self","host_id":"h","session_id":"s","ref":"_abc123","request_id":"r","state":"awaiting_approval","handoff_path":"/d/relay/op.md","used_percentage":72.4,"created_at":1,"updated_at":2}`
	if string(b) != want {
		t.Fatalf("RelayOp JSON =\n%s\nwant\n%s", b, want)
	}
	var back RelayOp
	if err := json.Unmarshal(b, &back); err != nil || back.State != RelayAwaitingApproval || *back.UsedPercentage != 72.4 {
		t.Fatalf("round trip: %+v err=%v", back, err)
	}
}

func TestRelayState_Terminal(t *testing.T) {
	for s, want := range map[RelayState]bool{
		RelayAwaitingApproval: false, RelayRequested: false, RelayClaimed: false, RelayWriting: false,
		RelayWritten: false, RelayCleared: false, RelayDone: true, RelayFailed: true, RelayCancelled: true,
	} {
		if s.Terminal() != want {
			t.Errorf("%s.Terminal() = %v, want %v", s, s.Terminal(), want)
		}
	}
}

func TestAPIError_CarriesOp(t *testing.T) {
	b, _ := json.Marshal(APIError{Error: ErrRelayOpen, Op: &RelayOp{ID: "op", State: RelayClaimed}})
	var e APIError
	if err := json.Unmarshal(b, &e); err != nil || e.Op == nil || e.Op.ID != "op" {
		t.Fatalf("APIError op: %s err=%v", b, err)
	}
	if b, _ := json.Marshal(APIError{Error: ErrBadRequest}); string(b) != `{"error":"bad_request"}` {
		t.Fatalf("op must be omitted when nil: %s", b)
	}
	if SelfRelayDeadlineS != 600 || RelayThresholdPct != 70 || RelayMinGrowth != 20000 || RelayDir != "relay" {
		t.Fatal("limits moved")
	}
}
```

- [ ] **Step 2: Run the test and verify it fails.**
  - Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/<worktree> && go test ./internal/team/ -run 'TestRelay|TestAPIError_CarriesOp' -v`
  - Expected: FAIL to compile: `undefined: RelayOp`, `undefined: RelayKindSelf`, `unknown field Op in struct literal of type APIError`.

- [ ] **Step 3: Implement.** Create `internal/team/wire_relay.go`:

```go
package team

// ---- P5a: relay ops, lineage, switches, pause (spec §8.1, §8.3, §8.4, §8.7) ----

// RelayKind is who started a relay op (spec §8.1).
type RelayKind string

const (
	RelayKindSelf   RelayKind = "self"   // the session's own mod, after the user's approval (U13)
	RelayKindMember RelayKind = "member" // the lead, with pdx relay <ref> (P6)
)

// RelayState is a relay op's state (spec §8.1). A self op starts in
// awaiting_approval; a member op in requested.
type RelayState string

const (
	RelayAwaitingApproval RelayState = "awaiting_approval"
	RelayRequested        RelayState = "requested"
	RelayClaimed          RelayState = "claimed"
	RelayWriting          RelayState = "writing"
	RelayWritten          RelayState = "written"
	RelayCleared          RelayState = "cleared"
	RelayDone             RelayState = "done"
	RelayFailed           RelayState = "failed"
	RelayCancelled        RelayState = "cancelled"
)

// Terminal reports whether s is a final state (done, failed, cancelled).
func (s RelayState) Terminal() bool {
	return s == RelayDone || s == RelayFailed || s == RelayCancelled
}

// Relay reasons: RelayOp.Reason for failed and cancelled.
const (
	RelayReasonHandoffIncomplete  = "handoff_incomplete"  // failed
	RelayReasonMemberUnresponsive = "member_unresponsive" // failed (P6)
	RelayReasonMemberGone         = "member_gone"         // failed (P6)
	RelayReasonDaemonUnavailable  = "daemon_unavailable"  // failed
	RelayReasonDenied             = "denied"              // cancelled
	RelayReasonTimeout            = "timeout"             // cancelled
	RelayReasonCompacted          = "compacted"           // cancelled
	RelayReasonAbandoned          = "abandoned"           // cancelled
)

// Relay error codes (APIError.Error on /api/relay/*); 409 unless noted.
const (
	ErrMemberRelayIsLeads = "member_relay_is_leads" // a member's relay is the lead's (U9)
	ErrSelfRelayOff       = "self_relay_off"        // the host switch is off
	ErrSelfRelayPaused    = "self_relay_paused"     // the session is paused
	ErrRelayOpen          = "relay_open"            // an op is already open for this session; carries Op
	ErrUnknownSession     = "unknown_session"       // 404: session_id is not a live CC session on this host
	ErrBadTransition      = "bad_transition"        // report: the op's state does not lead to this one; carries Op
)

// Relay limits (spec §8.1, §8.7).
const (
	RelayThresholdPct  = 70      // U1: used ≥ 70 % triggers the ask
	RelayMinGrowth     = 20000   // §8.1 loop guard: tokens a seeded conversation must grow before asking again
	SelfRelayDeadlineS = 600     // §8.7: 10 minutes, absolute
	RelayDir           = "relay" // <data_dir>/relay/<op id>.md
)

// RelayOp is one relay operation, self or member (spec §8.1).
type RelayOp struct {
	ID             string     `json:"id"`
	Kind           RelayKind  `json:"kind"`
	HostID         string     `json:"host_id"`
	SessionID      string     `json:"session_id"`               // the session being relayed (old id)
	NewSessionID   string     `json:"new_session_id,omitempty"` // after cleared
	Ref            string     `json:"ref"`                      // old ref
	NewRef         string     `json:"new_ref,omitempty"`
	TeamID         string     `json:"team_id,omitempty"`    // member relays (P6)
	RequestID      string     `json:"request_id,omitempty"` // the self_relay approval row
	State          RelayState `json:"state"`
	Reason         string     `json:"reason,omitempty"`
	HandoffPath    string     `json:"handoff_path"` // <data_dir>/relay/<op id>.md
	Pruned         bool       `json:"pruned,omitempty"`
	UsedPercentage *float64   `json:"used_percentage,omitempty"`
	CreatedAt      int64      `json:"created_at"`
	UpdatedAt      int64      `json:"updated_at"`
}

// SelfRelayPayload is Approval.Payload for KindSelfRelay (dialog body, spec §8.7).
type SelfRelayPayload struct {
	OpID           string  `json:"op_id"`
	UsedPercentage float64 `json:"used_percentage"`
	Window         int     `json:"window"`
	ModelID        string  `json:"model_id,omitempty"`
	Effort         string  `json:"effort,omitempty"`
}

// RelayHelloRequest is POST /api/relay/hello.
type RelayHelloRequest struct {
	SessionID  string `json:"session_id"`
	ModVersion string `json:"mod_version,omitempty"` // the mod ↔ daemon PROTOCOL version as a decimal string ("1"; P5b-1's VERSION), not a pdx release; P6 compares it for relay_unsupported
	Agent      string `json:"agent,omitempty"`       // "cc"
}

// RelayHelloResponse answers hello: the session's role and effective self-relay state.
type RelayHelloResponse struct {
	OK        bool   `json:"ok"`
	Role      string `json:"role"`       // "none" | "lead" | "member"
	SelfRelay string `json:"self_relay"` // "on" | "off" | "paused"
	Threshold int    `json:"threshold"`  // RelayThresholdPct
	MinGrowth int    `json:"min_growth"` // RelayMinGrowth
}

// RelayBeginRequest is POST /api/relay/begin. It carries no model or
// effort: the daemon fills SelfRelayPayload.ModelID / Effort itself from
// the session's last statusline reading (agent.ContextUsageReader, P1 —
// the only place Claude Code reports them, M21); the mod has no effort
// accessor (MP8) and need not pass what the daemon already knows.
type RelayBeginRequest struct {
	SessionID      string  `json:"session_id"`
	Self           bool    `json:"self"`
	UsedPercentage float64 `json:"used_percentage"`
	Window         int     `json:"window"`
}

// RelayBeginResponse is begin's 201 body.
type RelayBeginResponse struct {
	Op        RelayOp `json:"op"`
	RequestID string  `json:"request_id"`
}

// RelaySelfRequest is POST /api/relay/self: the per-session pause.
type RelaySelfRequest struct {
	SessionID string `json:"session_id"`
	Action    string `json:"action"` // "off" | "on" | "status"
}

// RelaySelfResponse answers self: the effective state and what makes it so.
type RelaySelfResponse struct {
	SelfRelay  string `json:"self_relay"`  // "on" | "off" | "paused"
	HostSwitch bool   `json:"host_switch"` // the host switch that applies to this session's role
	Member     bool   `json:"member"`      // a member has no switch (U13); false until P4
}

// RelayReportRequest is POST /api/relay/ops/{id}/report.
type RelayReportRequest struct {
	State        RelayState `json:"state"`
	NewSessionID string     `json:"new_session_id,omitempty"` // cleared
	Error        string     `json:"error,omitempty"`          // failed: the reason
}

// LineageReaderKey is the service-registry key under which the team module
// publishes its LineageReader; the peers module reads it at request time
// (team depends on peers, so peers cannot import the team module).
const LineageReaderKey = "team.lineage"

// LineageReader answers, for every session id that heads a relay chain, the
// refs it took over from — newest first, the whole chain, uncapped (§8.4).
type LineageReader interface {
	PreviousRefs() (map[string][]string, error)
}
```

In `internal/team/wire.go`, replace lines 121-126 with:

```go
// APIError is every non-2xx body on /api/team/*.
type APIError struct {
	Error    string    `json:"error"`
	Detail   string    `json:"detail,omitempty"`
	Approval *Approval `json:"approval,omitempty"` // request_open, already_decided
	Op       *RelayOp  `json:"op,omitempty"`       // relay_open, bad_transition (P5a)
}
```

- [ ] **Step 4: Run the tests and verify they pass.**
  - Run: `go test ./internal/team/ -v`
  - Expected: PASS (the new three plus the existing `wire_test.go`).

- [ ] **Step 5: Commit.**
  ```bash
  git add internal/team/wire_relay.go internal/team/wire_relay_test.go internal/team/wire.go
  git commit -m "feat(team): relay wire contract — ops, states, reasons, routes' bodies, lineage reader

  Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
  ```

- [ ] **Gate for P5a-0:** `go build ./... && go vet ./internal/team/ && go test ./internal/team/` green. Open PR P5a-0.

---

## PR P5a-1a — relay store, lineage row, title move

**Scope.** The three tables and their store methods in `teammod` (`relay_store.go`), and `PeerLabelStore.Move`. Consumes P5a-0's wire types. No route, no behaviour change for a running daemon; `OpenStore` only creates three more empty tables.

### Task 5a.2: The relay store — `relay_ops`, `session_lineage`, `session_prefs`

**Files:**
- Create: `internal/module/team/relay_store.go`
- Modify: `internal/module/team/store.go:57-61` (run `relaySchema` after the approval_requests migration)
- Test: `internal/module/team/relay_store_test.go`

**Interfaces:**
- Produces (package `teammod`):
  ```go
  var ErrNoSuchRelayOp = errors.New("no such relay op")
  type RelayReport struct { State team.RelayState; NewSessionID, NewRef, Reason string; At int64 }
  type ReportResult int // ReportApplied, ReportNoop, ReportBadTransition
  func (s *Store) CreateRelayOp(op team.RelayOp) error                                   // duplicate id is an error
  func (s *Store) GetRelayOp(id string) (team.RelayOp, bool, error)
  func (s *Store) OpenRelayOpBySession(sessionID string) (team.RelayOp, bool, error)     // non-terminal only
  func (s *Store) RelayOpByRequest(requestID string) (team.RelayOp, bool, error)
  func (s *Store) ListActiveRelayOps() ([]team.RelayOp, error)                          // never nil
  func (s *Store) ReportRelay(id string, r RelayReport) (team.RelayOp, ReportResult, error) // CAS; cleared writes lineage in the same tx
  func (s *Store) PreviousRefs() (map[string][]string, error)                           // team.LineageReader
  func (s *Store) SetSelfRelayPaused(sessionID string, paused bool, now int64) error
  func (s *Store) SelfRelayPaused(sessionID string) (bool, error)
  ```
  `var _ team.LineageReader = (*Store)(nil)` holds.
- Consumes: `rowScanner` (`store.go:70`), the `*Store` and `OpenStore` of P2a.

**State machine** (spec §8.1; a report of the current state is a no-op, anything not listed is `bad_transition`):

| from | to |
|---|---|
| awaiting_approval | claimed, cancelled |
| requested | claimed, cancelled, failed |
| claimed | writing, written, cleared, failed, cancelled |
| writing | written, cleared, failed, cancelled |
| written | cleared, failed, cancelled |
| cleared | done, failed |
| done / failed / cancelled | — |

- [ ] **Step 1: Write the failing tests.**

```go
package teammod

import (
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"

	"github.com/wake/purdex/internal/team"
)

func selfOp(id, sid, ref string, createdAt int64) team.RelayOp {
	pct := 72.4
	return team.RelayOp{
		ID: id, Kind: team.RelayKindSelf, HostID: "h:1", SessionID: sid, Ref: ref, RequestID: "req-" + id,
		State: team.RelayAwaitingApproval, HandoffPath: "/d/relay/" + id + ".md", UsedPercentage: &pct,
		CreatedAt: createdAt, UpdatedAt: createdAt,
	}
}

func mustReport(t *testing.T, s *Store, id string, r RelayReport) team.RelayOp {
	t.Helper()
	op, res, err := s.ReportRelay(id, r)
	if err != nil || res != ReportApplied {
		t.Fatalf("report %s → %s: res=%v err=%v op=%+v", id, r.State, res, err, op)
	}
	return op
}

func TestRelayStore_CreateGetAndOpenBySession(t *testing.T) {
	s := openTestStore(t)
	op := selfOp("op-1", "sid-old", "_aaaaaa", 1000)
	if err := s.CreateRelayOp(op); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateRelayOp(op); err == nil {
		t.Fatal("a duplicate op id must be an error (ids are daemon-minted, never retried)")
	}
	got, ok, err := s.GetRelayOp("op-1")
	if err != nil || !ok || !reflect.DeepEqual(got, op) {
		t.Fatalf("get: %+v ok=%v err=%v", got, ok, err)
	}
	if _, ok, err := s.GetRelayOp("nope"); err != nil || ok {
		t.Fatalf("get unknown: ok=%v err=%v", ok, err)
	}
	open, ok, err := s.OpenRelayOpBySession("sid-old")
	if err != nil || !ok || open.ID != "op-1" {
		t.Fatalf("open by session: %+v ok=%v err=%v", open, ok, err)
	}
	byReq, ok, err := s.RelayOpByRequest("req-op-1")
	if err != nil || !ok || byReq.ID != "op-1" {
		t.Fatalf("by request: %+v ok=%v err=%v", byReq, ok, err)
	}
	active, err := s.ListActiveRelayOps()
	if err != nil || len(active) != 1 {
		t.Fatalf("active = %+v err=%v", active, err)
	}
	mustReport(t, s, "op-1", RelayReport{State: team.RelayCancelled, Reason: team.RelayReasonDenied, At: 2000})
	if _, ok, _ := s.OpenRelayOpBySession("sid-old"); ok {
		t.Fatal("a cancelled op is not open")
	}
	if active, _ := s.ListActiveRelayOps(); len(active) != 0 || active == nil {
		t.Fatalf("active after cancel = %+v (must be [] not nil)", active)
	}
}

// Spec §8.3: reports are idempotent per (op, state); §8.1: the forward path
// awaiting_approval → claimed → writing → written → cleared → done.
func TestRelayStore_ReportTransitionsAndIdempotency(t *testing.T) {
	s := openTestStore(t)
	if err := s.CreateRelayOp(selfOp("op-1", "sid-old", "_aaaaaa", 1000)); err != nil {
		t.Fatal(err)
	}
	// A report of a state the op is not in and cannot reach: bad_transition, row unchanged.
	op, res, err := s.ReportRelay("op-1", RelayReport{State: team.RelayWriting, At: 1500})
	if err != nil || res != ReportBadTransition || op.State != team.RelayAwaitingApproval {
		t.Fatalf("awaiting → writing: res=%v err=%v op=%+v", res, err, op)
	}
	mustReport(t, s, "op-1", RelayReport{State: team.RelayClaimed, At: 2000})
	// Same state again: no-op, 200-equivalent, updated_at untouched.
	op, res, err = s.ReportRelay("op-1", RelayReport{State: team.RelayClaimed, At: 2500})
	if err != nil || res != ReportNoop || op.UpdatedAt != 2000 {
		t.Fatalf("claimed twice: res=%v err=%v op=%+v", res, err, op)
	}
	mustReport(t, s, "op-1", RelayReport{State: team.RelayWriting, At: 3000})
	mustReport(t, s, "op-1", RelayReport{State: team.RelayWritten, At: 4000})
	// A stale re-send of an earlier state is refused, not applied.
	if _, res, _ := s.ReportRelay("op-1", RelayReport{State: team.RelayWriting, At: 4500}); res != ReportBadTransition {
		t.Fatalf("written → writing: res=%v, want bad_transition", res)
	}
	cleared := mustReport(t, s, "op-1", RelayReport{State: team.RelayCleared, NewSessionID: "sid-new", NewRef: "_bbbbbb", At: 5000})
	if cleared.NewSessionID != "sid-new" || cleared.NewRef != "_bbbbbb" {
		t.Fatalf("cleared = %+v", cleared)
	}
	done := mustReport(t, s, "op-1", RelayReport{State: team.RelayDone, At: 6000})
	if !done.State.Terminal() {
		t.Fatalf("done = %+v", done)
	}
	// Terminal: nothing leads out of it.
	if _, res, _ := s.ReportRelay("op-1", RelayReport{State: team.RelayFailed, Reason: "x", At: 7000}); res != ReportBadTransition {
		t.Fatalf("done → failed: res=%v, want bad_transition", res)
	}
	if _, _, err := s.ReportRelay("nope", RelayReport{State: team.RelayDone}); !errors.Is(err, ErrNoSuchRelayOp) {
		t.Fatalf("unknown op: err=%v", err)
	}
}

// Spec §8.4 / §15 "Lineage": cleared writes session_lineage in the same
// transaction, and an uncapped chain still resolves the oldest ref after 11
// or more relays. PreviousRefs is newest first.
func TestRelayStore_ClearedWritesLineageAndChainIsUncapped(t *testing.T) {
	s := openTestStore(t)
	const n = 12
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("op-%d", i)
		old, next := fmt.Sprintf("sid-%d", i), fmt.Sprintf("sid-%d", i+1)
		oldRef, nextRef := fmt.Sprintf("_r%05d", i), fmt.Sprintf("_r%05d", i+1)
		op := selfOp(id, old, oldRef, int64(1000*(i+1)))
		op.State = team.RelayClaimed
		if err := s.CreateRelayOp(op); err != nil {
			t.Fatal(err)
		}
		mustReport(t, s, id, RelayReport{State: team.RelayCleared, NewSessionID: next, NewRef: nextRef, At: int64(1000*(i+1) + 500)})
	}
	refs, err := s.PreviousRefs()
	if err != nil {
		t.Fatal(err)
	}
	head := refs[fmt.Sprintf("sid-%d", n)]
	if len(head) != n {
		t.Fatalf("head chain has %d refs, want %d (uncapped): %v", len(head), n, head)
	}
	if head[0] != fmt.Sprintf("_r%05d", n-1) || head[n-1] != "_r00000" {
		t.Fatalf("chain must be newest first: %v", head)
	}
	// An intermediate session is also a head of its own (shorter) chain.
	if mid := refs["sid-3"]; !reflect.DeepEqual(mid, []string{"_r00002", "_r00001", "_r00000"}) {
		t.Fatalf("sid-3 chain = %v", mid)
	}
	// A second cleared report for the same op is a no-op and writes no second lineage row.
	op, res, err := s.ReportRelay("op-0", RelayReport{State: team.RelayCleared, NewSessionID: "other", NewRef: "_zzzzzz", At: 9})
	if err != nil || res != ReportNoop || op.NewSessionID != "sid-1" {
		t.Fatalf("cleared twice: res=%v err=%v op=%+v", res, err, op)
	}
	if refs2, _ := s.PreviousRefs(); !reflect.DeepEqual(refs2, refs) {
		t.Fatal("a no-op report must not change the lineage")
	}
}

// Two concurrent reports of different next states: exactly one applies; the
// loser sees bad_transition against the winner's state, never an error.
func TestRelayStore_ConcurrentReportsOneWins(t *testing.T) {
	s := openTestStore(t)
	op := selfOp("op-1", "sid", "_aaaaaa", 1000)
	op.State = team.RelayClaimed
	if err := s.CreateRelayOp(op); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make([]ReportResult, 2)
	errs := make([]error, 2)
	for i, st := range []team.RelayState{team.RelayFailed, team.RelayCancelled} {
		wg.Add(1)
		go func(i int, st team.RelayState) {
			defer wg.Done()
			_, results[i], errs[i] = s.ReportRelay("op-1", RelayReport{State: st, Reason: "r", At: 2000})
		}(i, st)
	}
	wg.Wait()
	applied := 0
	for i := range results {
		if errs[i] != nil {
			t.Fatalf("report %d: %v", i, errs[i])
		}
		if results[i] == ReportApplied {
			applied++
		} else if results[i] != ReportBadTransition {
			t.Fatalf("report %d: res=%v", i, results[i])
		}
	}
	if applied != 1 {
		t.Fatalf("%d reports applied, want exactly 1", applied)
	}
}

func TestRelayStore_SelfRelayPause(t *testing.T) {
	s := openTestStore(t)
	if p, err := s.SelfRelayPaused("sid"); err != nil || p {
		t.Fatalf("default paused=%v err=%v", p, err)
	}
	if err := s.SetSelfRelayPaused("sid", true, 10); err != nil {
		t.Fatal(err)
	}
	if p, _ := s.SelfRelayPaused("sid"); !p {
		t.Fatal("paused must read back true")
	}
	if err := s.SetSelfRelayPaused("sid", false, 20); err != nil {
		t.Fatal(err)
	}
	if p, _ := s.SelfRelayPaused("sid"); p {
		t.Fatal("on lifts the pause")
	}
}
```

- [ ] **Step 2: Run the tests and verify they fail.**
  - Run: `go test ./internal/module/team/ -run TestRelayStore -v`
  - Expected: FAIL to compile: `s.CreateRelayOp undefined`, `undefined: RelayReport`, `undefined: ReportApplied`.

- [ ] **Step 3: Implement `relay_store.go`** (the retention methods `ListUnprunedRelayOps`, `MarkRelayPruned`, `ChainRoots` are **P5a-3a**, not here):

```go
package teammod

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/wake/purdex/internal/team"
)

// ErrNoSuchRelayOp is returned by the per-id relay methods for an unknown id.
var ErrNoSuchRelayOp = errors.New("no such relay op")

// relaySchema holds the three P5a tables (spec §8.1, §8.4, §8.7). It is
// run by OpenStore after approval_requests; every statement is idempotent.
const relaySchema = `
	CREATE TABLE IF NOT EXISTS relay_ops (
		id              TEXT PRIMARY KEY,
		kind            TEXT    NOT NULL,
		host_id         TEXT    NOT NULL,
		session_id      TEXT    NOT NULL,
		new_session_id  TEXT    NOT NULL DEFAULT '',
		ref             TEXT    NOT NULL,
		new_ref         TEXT    NOT NULL DEFAULT '',
		team_id         TEXT    NOT NULL DEFAULT '',
		request_id      TEXT    NOT NULL DEFAULT '',
		state           TEXT    NOT NULL,
		reason          TEXT    NOT NULL DEFAULT '',
		handoff_path    TEXT    NOT NULL,
		pruned          INTEGER NOT NULL DEFAULT 0,
		used_percentage REAL,
		created_at      INTEGER NOT NULL,
		updated_at      INTEGER NOT NULL
	);
	CREATE INDEX IF NOT EXISTS relay_ops_session_state ON relay_ops (session_id, state);
	CREATE TABLE IF NOT EXISTS session_lineage (
		session_id             TEXT PRIMARY KEY,
		predecessor_session_id TEXT    NOT NULL,
		predecessor_ref        TEXT    NOT NULL,
		op_id                  TEXT    NOT NULL,
		at                     INTEGER NOT NULL
	);
	CREATE TABLE IF NOT EXISTS session_prefs (
		session_id        TEXT PRIMARY KEY,
		self_relay_paused INTEGER NOT NULL DEFAULT 0,
		updated_at        INTEGER NOT NULL
	);`

// relayTransitions is the state machine of spec §8.1: from → the states a
// report may move the op to. A report of the op's current state is a
// no-op (idempotent per (op, state)); anything not listed is bad_transition.
var relayTransitions = map[team.RelayState]map[team.RelayState]bool{
	team.RelayAwaitingApproval: {team.RelayClaimed: true, team.RelayCancelled: true},
	team.RelayRequested:        {team.RelayClaimed: true, team.RelayCancelled: true, team.RelayFailed: true},
	team.RelayClaimed:          {team.RelayWriting: true, team.RelayWritten: true, team.RelayCleared: true, team.RelayFailed: true, team.RelayCancelled: true},
	team.RelayWriting:          {team.RelayWritten: true, team.RelayCleared: true, team.RelayFailed: true, team.RelayCancelled: true},
	team.RelayWritten:          {team.RelayCleared: true, team.RelayFailed: true, team.RelayCancelled: true},
	team.RelayCleared:          {team.RelayDone: true, team.RelayFailed: true},
}

// RelayReport is one transition: the target state, the new session id and
// ref (cleared only), the reason (failed / cancelled) and the time.
type RelayReport struct {
	State        team.RelayState
	NewSessionID string
	NewRef       string
	Reason       string
	At           int64
}

// ReportResult says what ReportRelay did.
type ReportResult int

const (
	ReportApplied       ReportResult = iota // the op moved to the reported state
	ReportNoop                              // the op was already in that state
	ReportBadTransition                     // the op's state does not lead to the reported one
)

const relayCols = `id, kind, host_id, session_id, new_session_id, ref, new_ref, team_id, request_id,
	state, reason, handoff_path, pruned, used_percentage, created_at, updated_at`

func scanRelayOp(r rowScanner) (team.RelayOp, error) {
	var op team.RelayOp
	var pruned int
	var used sql.NullFloat64
	if err := r.Scan(&op.ID, &op.Kind, &op.HostID, &op.SessionID, &op.NewSessionID, &op.Ref, &op.NewRef, &op.TeamID, &op.RequestID,
		&op.State, &op.Reason, &op.HandoffPath, &pruned, &used, &op.CreatedAt, &op.UpdatedAt); err != nil {
		return team.RelayOp{}, err
	}
	op.Pruned = pruned != 0
	if used.Valid {
		v := used.Float64
		op.UsedPercentage = &v
	}
	return op, nil
}

// CreateRelayOp inserts op as given (the caller sets State and times). A
// duplicate id is an error: op ids are daemon-minted UUIDs, never retried.
func (s *Store) CreateRelayOp(op team.RelayOp) error {
	var used any
	if op.UsedPercentage != nil {
		used = *op.UsedPercentage
	}
	if _, err := s.db.Exec(`
		INSERT INTO relay_ops (id, kind, host_id, session_id, new_session_id, ref, new_ref, team_id, request_id,
			state, reason, handoff_path, pruned, used_percentage, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?, ?)`,
		op.ID, string(op.Kind), op.HostID, op.SessionID, op.NewSessionID, op.Ref, op.NewRef, op.TeamID, op.RequestID,
		string(op.State), op.Reason, op.HandoffPath, used, op.CreatedAt, op.UpdatedAt); err != nil {
		return fmt.Errorf("insert relay op %s: %w", op.ID, err)
	}
	return nil
}

// GetRelayOp returns the op with id; ok is false when there is none.
func (s *Store) GetRelayOp(id string) (team.RelayOp, bool, error) {
	op, err := scanRelayOp(s.db.QueryRow(`SELECT `+relayCols+` FROM relay_ops WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return team.RelayOp{}, false, nil
	}
	if err != nil {
		return team.RelayOp{}, false, fmt.Errorf("get relay op %s: %w", id, err)
	}
	return op, true, nil
}

// OpenRelayOpBySession returns the session's non-terminal op, if any
// (spec §8.7: at most one open relay per session).
func (s *Store) OpenRelayOpBySession(sessionID string) (team.RelayOp, bool, error) {
	op, err := scanRelayOp(s.db.QueryRow(`SELECT `+relayCols+` FROM relay_ops
		WHERE session_id = ? AND state NOT IN ('done', 'failed', 'cancelled') ORDER BY created_at, id LIMIT 1`, sessionID))
	if errors.Is(err, sql.ErrNoRows) {
		return team.RelayOp{}, false, nil
	}
	if err != nil {
		return team.RelayOp{}, false, fmt.Errorf("open relay op by session %s: %w", sessionID, err)
	}
	return op, true, nil
}

// RelayOpByRequest returns the op opened for a self_relay approval row.
func (s *Store) RelayOpByRequest(requestID string) (team.RelayOp, bool, error) {
	op, err := scanRelayOp(s.db.QueryRow(`SELECT `+relayCols+` FROM relay_ops WHERE request_id = ? ORDER BY created_at, id LIMIT 1`, requestID))
	if errors.Is(err, sql.ErrNoRows) {
		return team.RelayOp{}, false, nil
	}
	if err != nil {
		return team.RelayOp{}, false, fmt.Errorf("relay op by request %s: %w", requestID, err)
	}
	return op, true, nil
}

// ListActiveRelayOps returns every op not in done/failed/cancelled, oldest first. Never nil.
func (s *Store) ListActiveRelayOps() ([]team.RelayOp, error) {
	rows, err := s.db.Query(`SELECT ` + relayCols + ` FROM relay_ops
		WHERE state NOT IN ('done', 'failed', 'cancelled') ORDER BY created_at, id`)
	if err != nil {
		return nil, fmt.Errorf("list active relay ops: %w", err)
	}
	defer rows.Close()
	out := []team.RelayOp{}
	for rows.Next() {
		op, err := scanRelayOp(rows)
		if err != nil {
			return nil, fmt.Errorf("list active relay ops: %w", err)
		}
		out = append(out, op)
	}
	return out, rows.Err()
}

// ReportRelay applies one transition under a write transaction: a report of
// the current state is ReportNoop (idempotent per (op, state)); a state
// relayTransitions does not allow is ReportBadTransition; otherwise the row
// is updated with a CAS on its state. For cleared, the lineage row is
// written in the same transaction (spec §8.4): session_lineage{new →
// old, old ref, op}. The row after the attempt is returned in every case;
// ErrNoSuchRelayOp for an unknown id.
func (s *Store) ReportRelay(id string, r RelayReport) (team.RelayOp, ReportResult, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return team.RelayOp{}, ReportBadTransition, fmt.Errorf("report relay %s: begin: %w", id, err)
	}
	defer tx.Rollback()
	// The first statement is a write, so SQLite takes the write lock at
	// once (same reasoning as peer_label.go Release): two concurrent
	// reports cannot both read the same state and both pass the CAS.
	if _, err := tx.Exec(`UPDATE relay_ops SET updated_at = updated_at WHERE id = ?`, id); err != nil {
		return team.RelayOp{}, ReportBadTransition, fmt.Errorf("report relay %s: lock: %w", id, err)
	}
	cur, err := scanRelayOp(tx.QueryRow(`SELECT `+relayCols+` FROM relay_ops WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return team.RelayOp{}, ReportBadTransition, ErrNoSuchRelayOp
	}
	if err != nil {
		return team.RelayOp{}, ReportBadTransition, fmt.Errorf("report relay %s: %w", id, err)
	}
	if cur.State == r.State {
		return cur, ReportNoop, nil
	}
	if !relayTransitions[cur.State][r.State] {
		return cur, ReportBadTransition, nil
	}
	next := cur
	next.State, next.Reason, next.UpdatedAt = r.State, r.Reason, r.At
	if r.State == team.RelayCleared {
		next.NewSessionID, next.NewRef = r.NewSessionID, r.NewRef
	}
	res, err := tx.Exec(`UPDATE relay_ops SET state = ?, reason = ?, new_session_id = ?, new_ref = ?, updated_at = ?
		WHERE id = ? AND state = ?`, string(next.State), next.Reason, next.NewSessionID, next.NewRef, next.UpdatedAt, id, string(cur.State))
	if err != nil {
		return team.RelayOp{}, ReportBadTransition, fmt.Errorf("report relay %s: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return team.RelayOp{}, ReportBadTransition, fmt.Errorf("report relay %s: state changed under the transaction", id)
	}
	if r.State == team.RelayCleared {
		if _, err := tx.Exec(`INSERT INTO session_lineage (session_id, predecessor_session_id, predecessor_ref, op_id, at)
			VALUES (?, ?, ?, ?, ?) ON CONFLICT(session_id) DO NOTHING`, r.NewSessionID, cur.SessionID, cur.Ref, id, r.At); err != nil {
			return team.RelayOp{}, ReportBadTransition, fmt.Errorf("report relay %s: lineage: %w", id, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return team.RelayOp{}, ReportBadTransition, fmt.Errorf("report relay %s: commit: %w", id, err)
	}
	return next, ReportApplied, nil
}

// lineageRow is one session_lineage row.
type lineageRow struct {
	predecessorSessionID, predecessorRef string
}

// PreviousRefs implements team.LineageReader: for every session id that
// appears as the head of a lineage row, the predecessor refs walking back
// the whole chain, newest first, uncapped (spec §8.4). A cycle (impossible
// by construction, guarded anyway) stops the walk.
func (s *Store) PreviousRefs() (map[string][]string, error) {
	rows, err := s.db.Query(`SELECT session_id, predecessor_session_id, predecessor_ref FROM session_lineage`)
	if err != nil {
		return nil, fmt.Errorf("read lineage: %w", err)
	}
	defer rows.Close()
	back := map[string]lineageRow{}
	for rows.Next() {
		var sid string
		var lr lineageRow
		if err := rows.Scan(&sid, &lr.predecessorSessionID, &lr.predecessorRef); err != nil {
			return nil, fmt.Errorf("read lineage: %w", err)
		}
		back[sid] = lr
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read lineage: %w", err)
	}
	out := make(map[string][]string, len(back))
	for head := range back {
		seen := map[string]bool{head: true}
		var refs []string
		for cur := head; ; {
			lr, ok := back[cur]
			if !ok || seen[lr.predecessorSessionID] {
				break
			}
			seen[lr.predecessorSessionID] = true
			refs = append(refs, lr.predecessorRef)
			cur = lr.predecessorSessionID
		}
		out[head] = refs
	}
	return out, nil
}

// SetSelfRelayPaused records the per-session pause (spec §8.7 "a pause").
func (s *Store) SetSelfRelayPaused(sessionID string, paused bool, now int64) error {
	v := 0
	if paused {
		v = 1
	}
	if _, err := s.db.Exec(`INSERT INTO session_prefs (session_id, self_relay_paused, updated_at) VALUES (?, ?, ?)
		ON CONFLICT(session_id) DO UPDATE SET self_relay_paused = excluded.self_relay_paused, updated_at = excluded.updated_at`,
		sessionID, v, now); err != nil {
		return fmt.Errorf("set self relay paused %s: %w", sessionID, err)
	}
	return nil
}

// SelfRelayPaused reads the pause; a session without a row is not paused.
func (s *Store) SelfRelayPaused(sessionID string) (bool, error) {
	var v int
	err := s.db.QueryRow(`SELECT self_relay_paused FROM session_prefs WHERE session_id = ?`, sessionID).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("self relay paused %s: %w", sessionID, err)
	}
	return v != 0, nil
}
```

In `store.go` (`OpenStore`), the five lines `57-61` of alpha.527 read exactly

```go
			ON approval_requests (state, created_at);`); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate team db: %w", err)
	}
	return &Store{db: db}, nil
```

(line 57 is the tail of the multi-line raw-string `db.Exec(` that starts at `:39` with `CREATE TABLE IF NOT EXISTS approval_requests`; the earlier part of that statement is untouched). Replace those five lines with:

```go
			ON approval_requests (state, created_at);`); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate team db: %w", err)
	}
	if _, err := db.Exec(relaySchema); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate team db (relay): %w", err)
	}
	return &Store{db: db}, nil
```

- [ ] **Step 4: Run the tests and verify they pass.**
  - Run: `go test ./internal/module/team/ -run 'TestRelayStore|TestStore' -v`
  - Expected: PASS — five `TestRelayStore_*` and every existing `TestStore_*`.

- [ ] **Step 5: Commit.**
  ```bash
  git add internal/module/team/relay_store.go internal/module/team/relay_store_test.go internal/module/team/store.go
  git commit -m "feat(team): relay_ops, session_lineage and session_prefs with idempotent CAS reports

  Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
  ```

### Task 5a.3: The title move — `PeerLabelStore.Move`

**Files:**
- Create: `internal/store/peer_label_move.go`
- Test: `internal/store/peer_label_move_test.go`

**Interfaces:**
- Produces: `func (s *PeerLabelStore) Move(fromSessionID, toSessionID string, now time.Time) (moved bool, err error)`.
- Consumes: `nextRev` (`peer_label.go:48`), the `peer_labels` table (`meta.go:120`).

- [ ] **Step 1: Write the failing test.**

```go
// internal/store/peer_label_move_test.go
package store

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Lead-team-relay spec §8.4: the title moves to the new session id. The
// old row is released (kept, label NULL), the new row carries the label
// with a fresh rev, and a second Move is a no-op (the boot reconciliation
// re-runs it for a cleared op).
func TestPeerLabels_MoveCarriesLabelOnce(t *testing.T) {
	ms, err := OpenMeta(":memory:")
	require.NoError(t, err)
	defer ms.Close()
	ls := ms.PeerLabels()
	now := time.UnixMilli(1000)

	_, err = ls.Claim("sid-old", "purdex-tester", now) // rev 1
	require.NoError(t, err)

	moved, err := ls.Move("sid-old", "sid-new", time.UnixMilli(2000))
	require.NoError(t, err)
	assert.True(t, moved)
	rows, err := ls.Snapshot()
	require.NoError(t, err)
	byID := map[string]PeerLabel{}
	for _, r := range rows {
		byID[r.SessionID] = r
	}
	require.Len(t, byID, 2)
	assert.Equal(t, "purdex-tester", byID["sid-new"].Label)
	assert.Equal(t, int64(2), byID["sid-new"].Rev)
	assert.Equal(t, "", byID["sid-old"].Label, "the old row is released, not deleted")
	assert.Equal(t, int64(3), byID["sid-old"].Rev)
	assert.Equal(t, int64(2000), byID["sid-old"].SetAt.UnixMilli())

	// Idempotent: the old row has no label now, so nothing moves and no rev is spent.
	moved, err = ls.Move("sid-old", "sid-new", time.UnixMilli(3000))
	require.NoError(t, err)
	assert.False(t, moved)
	rows, _ = ls.Snapshot()
	for _, r := range rows {
		if r.SessionID == "sid-new" {
			assert.Equal(t, int64(2), r.Rev, "a no-op move must not bump the new row")
		}
	}

	// No row at all, same id, or empty ids: false, no error.
	for _, c := range [][2]string{{"never", "x"}, {"sid-new", "sid-new"}, {"", "x"}, {"x", ""}} {
		moved, err := ls.Move(c[0], c[1], now)
		require.NoError(t, err, c)
		assert.False(t, moved, c)
	}

	// The new session already had a label: the relayed identity replaces it.
	_, err = ls.Claim("sid-a", "alpha", now)
	require.NoError(t, err)
	_, err = ls.Claim("sid-b", "beta", now)
	require.NoError(t, err)
	moved, err = ls.Move("sid-a", "sid-b", now)
	require.NoError(t, err)
	assert.True(t, moved)
	rows, _ = ls.Snapshot()
	for _, r := range rows {
		if r.SessionID == "sid-b" {
			assert.Equal(t, "alpha", r.Label)
		}
	}
}
```

- [ ] **Step 2: Run and see it fail.** `go test ./internal/store/ -run TestPeerLabels_Move -v` → `ls.Move undefined`.

- [ ] **Step 3: Implement.**

```go
// internal/store/peer_label_move.go
package store

import (
	"database/sql"
	"errors"
	"time"
)

// Move carries fromSessionID's label to toSessionID (lead-team-relay spec
// §8.4: "the title moves to the new session id"), in one transaction: the
// new row takes the label with a fresh rev, the old row is released (kept,
// label NULL, rev bumped). moved is false — and nothing is written — when
// the old session has no label; that is what makes a retry (the daemon's
// boot reconciliation re-runs the move for a cleared op) a no-op. A label
// the new session already held is replaced: the relay's identity wins.
//
// The first statement is a write on the old row (same reasoning as
// Release): SQLite takes the write lock at once, so a concurrent Claim
// cannot slip between the read and the write. It is a no-op UPDATE with
// RETURNING, which reads the current label under that lock in one step.
func (s *PeerLabelStore) Move(fromSessionID, toSessionID string, now time.Time) (moved bool, err error) {
	if fromSessionID == "" || toSessionID == "" || fromSessionID == toSessionID {
		return false, nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var label sql.NullString
	err = tx.QueryRow(`UPDATE peer_labels SET set_at = set_at WHERE session_id = ? RETURNING label`, fromSessionID).Scan(&label)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && (!label.Valid || label.String == "")) {
		return false, nil // no row, or already released: nothing to move
	}
	if err != nil {
		return false, err
	}
	ms := now.UnixMilli()
	newRev, err := nextRev(tx)
	if err != nil {
		return false, err
	}
	if _, err := tx.Exec(`
		INSERT INTO peer_labels (session_id, label, rev, set_at) VALUES (?, ?, ?, ?)
		ON CONFLICT(session_id) DO UPDATE SET label = excluded.label, rev = excluded.rev, set_at = excluded.set_at
	`, toSessionID, label.String, newRev, ms); err != nil {
		return false, err
	}
	oldRev, err := nextRev(tx)
	if err != nil {
		return false, err
	}
	if _, err := tx.Exec(`UPDATE peer_labels SET label = NULL, rev = ?, set_at = ? WHERE session_id = ?`, oldRev, ms, fromSessionID); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}
```

- [ ] **Step 4: Run and see it pass.** `go test ./internal/store/ -run TestPeerLabels -v` → PASS (3 tests).

- [ ] **Step 5: Commit.**
  ```bash
  git add internal/store/peer_label_move.go internal/store/peer_label_move_test.go
  git commit -m "feat(store): PeerLabelStore.Move carries a title to a relayed session id, once

  Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
  ```

- [ ] **Gate for P5a-1a:** `go build ./... && go vet ./... && go test ./internal/team/ ./internal/module/team/ ./internal/store/` green.

---

## PR P5a-1b — `previous_refs` on peer rows, the Resolve lineage tier, the hint, the resolver by session id, host config `relay`

**Scope.** `PeerRecord.PreviousRefs` filled by `Build` from `BuildInput.PreviousRefs`; `ipeers.Resolve` gains the lineage tier below the live-ref tier (bare form and combined form); the peers module reads the lineage through `team.LineageReaderKey` at request time; `peerNotFoundHint` says "renames and relays"; `OriginResolver.ResolveOriginBySession` (peers side only — the team-side interface change is P5a-2a). No daemon registers a `LineageReader` yet (P5a-2a does), so `pdx peers` output is unchanged until then. Also here (moved from P5a-2a): the host config section `relay {self_solo, self_lead}` with its `RelaySwitchReader` on the registry (Task 5a.6) — a `hostconfig` leaf nobody reads until P5a-2a.

### Task 5a.4: `PeerRecord.previous_refs` and the lineage tier in `Resolve`

**Files:**
- Modify: `internal/peers/record.go:55` (field after `Ref`), `:132` (`BuildInput.PreviousRefs`), `:166-173` (attach in the per-row loop); `internal/peers/address.go:230-233` (combined form: a second predicate), `:351-368` (`resolveRefHead`: the tier) plus `hasPreviousRef`
- Test: `internal/peers/address_lineage_test.go`

**Interfaces:**
- Produces: `PeerRecord.PreviousRefs []string \`json:"previous_refs,omitempty"\`` (newest first, uncapped); `BuildInput.PreviousRefs map[string][]string` (by CC session id; Build copies the slice); `Resolve`: a `_ref` (with or without underscore) matching no live row but exactly one live row's `PreviousRefs` → that row; the combined form `<name> [<oldref>]` likewise, with the name still checked.
- Consumes: `hasLiveEntry`, `resolveTier`, `IsRef`.

- [ ] **Step 1: Write the failing tests.**

```go
package peers

import (
	"errors"
	"testing"
)

// relayedRow is a live row that took over from older refs (newest first).
func relayedRow(ref, name string, pid int, previous ...string) PeerRecord {
	r := liveRow(ref, name, "", "", pid)
	r.PreviousRefs = previous
	return r
}

// Spec §8.4 / §15 "Relay": Resolve finds an old ref in exactly one row's
// previous_refs, and a live ref wins over previous_refs.
func TestResolve_PreviousRefsTier(t *testing.T) {
	recs := []PeerRecord{
		relayedRow(refB, "purdex-b0", 2, refA, refC), // relayed twice: A then C are its past
		liveRow("_other1", "someone", "", "", 3),
	}
	for _, in := range []string{refA, refA[1:], refC, "purdex-b0 [" + refA[1:] + "]"} {
		rec, err := Resolve(recs, in, ResolveSnapshot{})
		if err != nil || rec.Agent.PID != 2 {
			t.Fatalf("Resolve(%q): pid=%d err=%v; the row that relayed from it must answer", in, pidOf(rec), err)
		}
	}
	// A LIVE row carrying refA wins over the row that merely relayed from it.
	withLive := append([]PeerRecord{liveRow(refA, "fresh", "", "", 9)}, recs...)
	rec, err := Resolve(withLive, refA, ResolveSnapshot{})
	if err != nil || rec.Agent.PID != 9 {
		t.Fatalf("live ref must win: pid=%d err=%v", pidOf(rec), err)
	}
	// Two rows listing the same old ref: ambiguous, never a guess.
	two := append([]PeerRecord{relayedRow("_dup001", "dup", 7, refA)}, recs...)
	var amb *AmbiguousError
	if _, err := Resolve(two, refA, ResolveSnapshot{}); !errors.As(err, &amb) || len(amb.Candidates) != 2 {
		t.Fatalf("two rows with the same previous ref: err=%v", err)
	}
	// The combined form still checks the name: an old ref with the wrong name is a mismatch.
	if _, err := Resolve(recs, "wrong-name ["+refA[1:]+"]", ResolveSnapshot{}); !errors.Is(err, ErrNameMismatch) {
		t.Fatalf("old ref + wrong name: err=%v, want ErrNameMismatch", err)
	}
	// Mutation gate (spec §15): a row that carries no previous_refs leaves the old ref at peer_not_found.
	bare := []PeerRecord{liveRow(refB, "purdex-b0", "", "", 2)}
	if _, err := Resolve(bare, refA, ResolveSnapshot{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("without previous_refs the old ref must be not found: err=%v", err)
	}
	// An inert row (owner fallback, no live entry) never answers through its lineage either.
	dead := inboxDeadRow(refB, "", "mt0")
	dead.PreviousRefs = []string{refA}
	if _, err := Resolve([]PeerRecord{dead}, refA, ResolveSnapshot{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a dead holder's lineage must not resolve: err=%v", err)
	}
	// Partial snapshot: a lineage miss is not-ready, like a bare ref miss.
	if _, err := Resolve(bare, refA, ResolveSnapshot{Partial: true}); !errors.Is(err, ErrResolveNotReady) {
		t.Fatalf("lineage miss under Partial: err=%v", err)
	}
}

func pidOf(r PeerRecord) int {
	if r.Agent == nil {
		return 0
	}
	return r.Agent.PID
}

// Build copies the lineage onto the row that carries the session id, and a
// row without one has no previous_refs key on the wire.
func TestBuild_AttachesPreviousRefsBySessionID(t *testing.T) {
	in := BuildInput{
		HostID: "h", Alias: "mlab",
		Sessions: []SessionSummary{{Code: "c1", Name: "mt1"}, {Code: "c2", Name: "mt2"}},
		Owners: map[string]Owner{
			"c1": {AgentType: "cc", SessionID: "sid-new"},
			"c2": {AgentType: "cc", SessionID: "sid-plain"},
		},
		Entries: []Entry{
			{PID: 1, SessionID: "sid-new", Name: "purdex-b0", Inbox: "/tmp/1.sock"},
			{PID: 2, SessionID: "sid-plain", Name: "other", Inbox: "/tmp/2.sock"},
		},
		PreviousRefs: map[string][]string{"sid-new": {refA, refC}},
	}
	recs := Build(in)
	var got, plain PeerRecord
	for _, r := range recs {
		switch r.SessionCode {
		case "c1":
			got = r
		case "c2":
			plain = r
		}
	}
	if len(got.PreviousRefs) != 2 || got.PreviousRefs[0] != refA {
		t.Fatalf("c1 previous_refs = %v", got.PreviousRefs)
	}
	if plain.PreviousRefs != nil {
		t.Fatalf("c2 previous_refs = %v, want none", plain.PreviousRefs)
	}
	in.PreviousRefs["sid-new"][0] = "_mutate"
	if got.PreviousRefs[0] != refA {
		t.Fatal("Build must copy the slice, not alias the caller's map")
	}
}
```

- [ ] **Step 2: Run and see it fail.** `go test ./internal/peers/ -run 'TestResolve_PreviousRefsTier|TestBuild_AttachesPreviousRefs' -v` → `r.PreviousRefs undefined`, `unknown field PreviousRefs in struct literal of type BuildInput`.

- [ ] **Step 3: Implement.** In `record.go`, replace line 55 (`Ref          string \`json:"ref"\``) with:

```go
	Ref string `json:"ref"`
	// PreviousRefs are the refs this conversation took over from through
	// relays (lead-team-relay spec §8.4): newest first, the whole chain,
	// uncapped. Resolve delivers a bare old ref to the row that lists it
	// when no live row carries it; `pdx peers` prints the newest as
	// "(was _xxxxxx)". Absent (omitempty) for a conversation that never
	// relayed, and on a daemon that predates P5a.
	PreviousRefs []string `json:"previous_refs,omitempty"`
```

(then `gofmt -w internal/peers/record.go`: the following fields `Title` … `TmuxInstance` re-align). After line 132 (`Contexts map[string]ContextInfo …`) add:

```go
	// PreviousRefs is the relay lineage per CC session id (lead-team-relay
	// spec §8.4): the refs that session took over from, newest first. Build
	// copies each onto the row carrying that session id. nil means no
	// lineage is known (no team module, or it could not be read).
	PreviousRefs map[string][]string
```

In the per-row loop (`:166-173`), after `a.Context = &c }` and before the loop's closing brace, add:

```go
			// The lineage rides on the row, not the agent: Resolve decides
			// on PeerRecord fields, and the ref it falls back from lives
			// there too. A copy, so a caller mutating the input map later
			// cannot change a built row.
			if refs := in.PreviousRefs[a.SessionID]; len(refs) > 0 {
				records[i].PreviousRefs = append([]string(nil), refs...)
			}
```

In `address.go`, replace `resolveRefHead` (`:351-368`) with:

```go
func resolveRefHead(records []PeerRecord, ref string, snap ResolveSnapshot) (PeerRecord, error) {
	if !strings.HasPrefix(ref, "_") {
		ref = "_" + ref
	}
	if !IsRef(ref) {
		return PeerRecord{}, ErrNotFound
	}
	rec, err := resolveTier(records, ref, func(r PeerRecord) bool {
		return hasLiveEntry(r) && r.Ref == ref
	})
	if errors.Is(err, ErrNotFound) {
		// The lineage tier (lead-team-relay spec §8.4): a ref no live row
		// carries, but exactly one live row lists among the refs it took
		// over from through relays. It sits strictly BELOW the live-ref
		// tier — a live ref always wins — and above nothing else: a bare
		// tmux name (tier 4) is decided by the caller after this returns.
		// Two rows listing the same old ref is an ambiguity, not a guess.
		rec, err = resolveTier(records, ref, func(r PeerRecord) bool {
			return hasLiveEntry(r) && hasPreviousRef(r, ref)
		})
	}
	if err == nil && snap.RegistryIncomplete {
		return PeerRecord{}, ErrResolveNotReady
	}
	if errors.Is(err, ErrNotFound) && snap.Partial {
		return PeerRecord{}, ErrResolveNotReady
	}
	return rec, err
}

// hasPreviousRef reports whether ref is one of the refs r relayed from.
func hasPreviousRef(r PeerRecord, ref string) bool {
	for _, p := range r.PreviousRefs {
		if p == ref {
			return true
		}
	}
	return false
}
```

and in the combined form, right after the first `resolveTier` (`:230-233`) and before `switch {`, add:

```go
		if errors.Is(err, ErrNotFound) {
			// The combined form with a relayed-from ref (lead-team-relay
			// spec §8.4): the name must still be the row's, so the check
			// the bracket exists for is kept; only the ref is read through
			// the lineage. Below the live pair, as the bare tier is.
			rec, err = resolveTier(records, session, func(r PeerRecord) bool {
				return hasLiveEntry(r) && RoutableName(r.Agent.PeerName) &&
					hasPreviousRef(r, ref) && r.Agent.PeerName == typedName
			})
		}
```

- [ ] **Step 4: Run and see it pass.** `go test ./internal/peers/` → `ok` (every existing Resolve/Build/wire test still green; the wire tests do not pin the absence of `previous_refs`, it is omitempty).

- [ ] **Step 5: Commit.**
  ```bash
  git add internal/peers/record.go internal/peers/address.go internal/peers/address_lineage_test.go
  git commit -m "feat(peers): previous_refs on peer rows and the lineage tier in Resolve (live ref wins)

  Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
  ```

### Task 5a.5: The peers module reads the lineage, the hint, and the resolver by session id

**Files:**
- Modify: `internal/module/peers/module.go:40-41` (import `internal/team`), `:789-805` (`PreviousRefs: m.previousRefs()` in the `BuildInput`), new method `previousRefs` before `titleSnapshot` (`:822`); `internal/module/peers/send.go:53-54`; `internal/module/peers/send_test.go:1490-1492`; `internal/module/peers/origin_resolver.go:38-58` (split into `originOf` + `ResolveOriginBySession`)
- Test: `internal/module/peers/lineage_test.go`, `internal/module/peers/origin_resolver_session_test.go`, and **`internal/module/team/lineage_path_test.go`** (new, package `teammod` — the one package that can hold the real `LineageReader` (P5a-1a's `Store`) and `ipeers` together without a cycle; the full-path 12-hop test of spec §15 "Lineage", codex round; uses `openTestStore` / `selfOp` / `mustReport` from `relay_store_test.go`)

**Interfaces:**
- Produces: `func (r *OriginResolver) ResolveOriginBySession(sessionID string) (team.Origin, bool, error)` (same ok/err contract as `ResolveOrigin`); the inventory rows carry `previous_refs` when a `team.LineageReader` is registered under `team.LineageReaderKey`; the hint text.
- Consumes: `team.LineageReaderKey`, `team.LineageReader` (Task 5a.1), `m.core.Registry`.

- [ ] **Step 1: Write the failing tests.** `lineage_test.go`:

```go
package peers

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/wake/purdex/internal/module/agent"
	"github.com/wake/purdex/internal/module/session"
	"github.com/wake/purdex/internal/team"
)

// fakeLineage is the team module's LineageReader as the peers inventory
// sees it through the registry.
type fakeLineage struct {
	refs map[string][]string
	err  error
}

func (f *fakeLineage) PreviousRefs() (map[string][]string, error) { return f.refs, f.err }

var _ team.LineageReader = (*fakeLineage)(nil)

// Lead-team-relay spec §8.4: the row whose CC session id heads a relay
// chain carries previous_refs (newest first); a reader error leaves every
// row without the field and the envelope NOT partial.
func TestLocalEnvelope_AttachesPreviousRefsFromLineageReader(t *testing.T) {
	dir := t.TempDir()
	writeRegistryFixture(t, dir, "76973.json", fixture76973)
	sessions := &fakeSessions{sessions: []session.SessionInfo{
		{Code: "mt1code", Name: "mt1", Cwd: "/Users/wake/Workspace/wake/purdex", TmuxInstance: "inst1"},
	}}
	owners := &fakeOwners{owners: map[string]agent.PaneOwner{
		"mt1code": {AgentType: "cc", SessionID: "fa5d4c07-d9d9-4184-9e13-e491f2f4bf7c", Cwd: "/Users/wake/Workspace/wake/purdex", TmuxPaneID: "%10", LastSeenAt: 1789314156000, Status: "busy"},
	}}
	clock := &fakeClock{times: []time.Time{time.Unix(0, 0)}}
	c := newTestCore(t, "mlab:abc123", "mlab")
	lineage := &fakeLineage{refs: map[string][]string{"fa5d4c07-d9d9-4184-9e13-e491f2f4bf7c": {"_b1xxxx", "_a0xxxx"}}}
	c.Registry.Register(team.LineageReaderKey, lineage)
	m := newTestModule(t, c, sessions, owners, dir, allLiveLiveness(fixture76973ProcStart), clock, 2*time.Second)

	read := func() (refs []string, partial bool) {
		t.Helper()
		rr := doGetPeers(t, m, "/api/peers")
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d; body=%s", rr.Code, rr.Body.String())
		}
		var got struct {
			Partial bool `json:"partial"`
			Peers   []struct {
				SessionCode  string   `json:"session_code"`
				PreviousRefs []string `json:"previous_refs"`
			} `json:"peers"`
		}
		if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
			t.Fatalf("unmarshal: %v; body=%s", err, rr.Body.String())
		}
		for _, p := range got.Peers {
			if p.SessionCode == "mt1code" {
				return p.PreviousRefs, got.Partial
			}
		}
		t.Fatal("mt1code row missing")
		return nil, false
	}

	refs, partial := read()
	if len(refs) != 2 || refs[0] != "_b1xxxx" || refs[1] != "_a0xxxx" || partial {
		t.Fatalf("previous_refs = %v partial=%v", refs, partial)
	}

	lineage.err = errors.New("team.db locked")
	refs, partial = read()
	if refs != nil || partial {
		t.Fatalf("after a reader error: previous_refs = %v partial=%v (want absent, not partial)", refs, partial)
	}
}
```

`origin_resolver_session_test.go`:

```go
package peers

import (
	"testing"

	ipeers "github.com/wake/purdex/internal/peers"
)

// Lead-team-relay spec §8.3: the relay routes attribute the caller by CC
// session id. Same Origin as the inbox path, same not-found and read-error
// contract.
func TestOriginResolver_ResolveOriginBySession(t *testing.T) {
	r, dir := resolverFixture(t, allLiveLiveness(fixture76973ProcStart))
	o, ok, err := r.ResolveOriginBySession("sid-1")
	if !ok || err != nil {
		t.Fatalf("sid-1 must resolve: ok=%v err=%v", ok, err)
	}
	byInbox, _, _ := r.ResolveOrigin(dir + "/10.sock")
	if o != byInbox {
		t.Fatalf("by session = %+v, by inbox = %+v; they must agree", o, byInbox)
	}
	if o.Address != "mlab/n10" || o.Title != "lead-team" || o.Ref != ipeers.RefID("sid-1") {
		t.Fatalf("origin = %+v", o)
	}
	for _, sid := range []string{"", "sid-99"} {
		if _, ok, err := r.ResolveOriginBySession(sid); ok || err != nil {
			t.Fatalf("%q: ok=%v err=%v, want not found without error", sid, ok, err)
		}
	}
	// A dead holder does not resolve (the registry was read; it is not live).
	live := allLiveLiveness(fixture76973ProcStart)
	live.PidAlive = func(pid int) bool { return pid != 10 }
	dead, _ := resolverFixture(t, live)
	if _, ok, err := dead.ResolveOriginBySession("sid-1"); ok || err != nil {
		t.Fatalf("dead: ok=%v err=%v", ok, err)
	}
}
```

And in `send_test.go`, after line 1492 (`t.Errorf("hint must say what a ref survives: %q", ae.Detail) }`), add:

```go
	if !strings.Contains(ae.Detail, "renames and relays") {
		t.Errorf("a relay keeps the old ref reachable (spec §8.4); the hint must say so: %q", ae.Detail)
	}
```

New `internal/module/team/lineage_path_test.go` — spec §15 "Lineage: an uncapped chain still resolves a lead's oldest ref after 11 or more relays", through the **whole** path rather than one layer at a time: the real `Store` (team.db) as `team.LineageReader` → `ipeers.BuildInput.PreviousRefs` → `ipeers.Build` → `PeerRecord.PreviousRefs` → `ipeers.Resolve` with the **oldest** ref. (`TestRelayStore_ClearedWritesLineageAndChainIsUncapped` proves the store alone, `TestResolve_PreviousRefsTier` the resolver alone; a cap introduced in `Build`'s copy or in the module's map would pass both and fail here.)

```go
package teammod

import (
	"errors"
	"fmt"
	"testing"

	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/team"
)

// Spec §15 "Lineage", end to end: twelve chained relays sid-0 → … → sid-12
// written by the store at cleared; the live head row built by ipeers.Build
// from the store's PreviousRefs() (as the peers module feeds it, Task
// 5a.5); Resolve of the OLDEST ref, bare and in the combined form, answers
// the live row. Mutation gate: cap previous_refs at 10 anywhere on the path
// (the store's query, the module's map copy, Build's copy, resolveRefHead's
// scan) → _r00000 and _r00001 are ErrNotFound → red.
func TestLineagePath_TwelveHopsOldestRefResolvesToTheLiveRow(t *testing.T) {
	s := openTestStore(t)
	const n = 12
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("op-%d", i)
		old, next := fmt.Sprintf("sid-%d", i), fmt.Sprintf("sid-%d", i+1)
		oldRef, nextRef := fmt.Sprintf("_r%05d", i), fmt.Sprintf("_r%05d", i+1)
		op := selfOp(id, old, oldRef, int64(1000*(i+1)))
		op.State = team.RelayClaimed
		if err := s.CreateRelayOp(op); err != nil {
			t.Fatal(err)
		}
		mustReport(t, s, id, RelayReport{State: team.RelayCleared, NewSessionID: next, NewRef: nextRef, At: int64(1000*(i+1) + 500)})
	}
	var reader team.LineageReader = s // the registry value the peers module reads
	refs, err := reader.PreviousRefs()
	if err != nil {
		t.Fatal(err)
	}
	head := fmt.Sprintf("sid-%d", n)
	records := ipeers.Build(ipeers.BuildInput{
		HostID: "h:1", Alias: "mlab",
		// The live conversation is the chain's head; only it is alive.
		Entries:      []ipeers.Entry{{PID: 4242, SessionID: head, Name: "purdex-x", Inbox: "/s/4242", ProcStart: "Sun Sep 13 15:22:36 2026"}},
		PreviousRefs: refs,
	})
	var live ipeers.PeerRecord
	for _, r := range records {
		if r.Agent != nil && r.Agent.SessionID == head {
			live = r
		}
	}
	if live.Agent == nil || len(live.PreviousRefs) != n || live.PreviousRefs[0] != fmt.Sprintf("_r%05d", n-1) || live.PreviousRefs[n-1] != "_r00000" {
		t.Fatalf("live row previous_refs = %v (want %d, newest first)", live.PreviousRefs, n)
	}
	for _, in := range []string{"_r00000", "r00000", "_r00001", "purdex-x [r00000]", fmt.Sprintf("_r%05d", n-1)} {
		rec, err := ipeers.Resolve(records, in, ipeers.ResolveSnapshot{})
		if err != nil || rec.Agent == nil || rec.Agent.PID != 4242 || rec.Agent.SessionID != head {
			t.Fatalf("Resolve(%q) = %+v err=%v; want the live head row (pid 4242)", in, rec, err)
		}
	}
	// A ref that was never in the chain stays not found.
	if _, err := ipeers.Resolve(records, "_r99999", ipeers.ResolveSnapshot{}); !errors.Is(err, ipeers.ErrNotFound) {
		t.Fatalf("unknown ref: err=%v, want ErrNotFound", err)
	}
}
```

- [ ] **Step 2: Run and see them fail.** `go test ./internal/module/peers/ -run 'TestLocalEnvelope_AttachesPreviousRefs|TestOriginResolver_ResolveOriginBySession|TestSend_PeerNotFoundTeachesTheV4AddressForms' -v` → compile error `r.ResolveOriginBySession undefined`; after stubbing, the lineage test fails with `previous_refs = [] partial=false` and the hint test with `hint must say so`. `go test ./internal/module/team/ -run TestLineagePath -v` → with Task 5a.4 merged it passes at once (it exercises P5a-1a + Task 5a.4 code; it is the cross-layer gate, not a red-first test) — run it, record `PASS`, then apply the mutation gate once (cap `Build`'s copy at 10) and see `Resolve("_r00000") … err=peer not found`; revert.

- [ ] **Step 3: Implement.** `module.go`: add `"github.com/wake/purdex/internal/team"` after the `internal/store` import (`:40`); in the `ipeers.Build(ipeers.BuildInput{` literal (`:789-805`) add `PreviousRefs: m.previousRefs(),` after `Contexts:   contexts,` (gofmt re-aligns the keys); before `// titleSnapshot reads …` (`:822`) add:

```go
// previousRefs reads the relay lineage the team module publishes under
// team.LineageReaderKey (lead-team-relay spec §8.4). Looked up per call,
// not in Init: team depends on peers, so it registers after peers' Init
// ran. No reader (an older build, or tests without the team module) or a
// read error means no lineage — rows render without previous_refs and an
// old ref is peer_not_found, exactly the pre-P5a behaviour; the error is
// logged, and it never marks the envelope partial (a stale lineage is a
// display and fallback-routing matter, not an inventory one).
func (m *Module) previousRefs() map[string][]string {
	if m.core == nil || m.core.Registry == nil {
		return nil
	}
	svc, ok := m.core.Registry.Get(team.LineageReaderKey)
	if !ok {
		return nil
	}
	reader, ok := svc.(team.LineageReader)
	if !ok {
		return nil
	}
	refs, err := reader.PreviousRefs()
	if err != nil {
		m.logf("peers: inventory: read relay lineage: %v", err)
		return nil
	}
	return refs
}
```

`send.go:53-54`: replace the comment line and the constant with:

```go
// 2026-10-06: a ref changes on /clear (lead-team-relay spec M3).
// 2026-10-07 (P5a): a relay keeps the old ref reachable through the
// lineage (spec §8.4), so the hint says "renames and relays"; a manual
// /clear still does not.
const peerNotFoundHint = "an address is `<host>/<name>`, where <name> is the session's own name — not the title it calls itself; add its ref as `<host>/<name> [<ref>]` when two sessions share a name, or use `<host>/_<ref>` alone, which survives renames and relays (a manual /clear starts a new ref) — run `pdx peers --all` for the current addresses, or `pdx msg whoami` for your own"
```

`origin_resolver.go`: replace lines 38-58 (from `if !found {` to the closing `}` of `ResolveOrigin`) with:

```go
	if !found {
		return team.Origin{}, false, nil
	}
	return r.originOf(e), true, nil
}

// ResolveOriginBySession is ResolveOrigin keyed by the CC session id the
// relay mod reports (lead-team-relay spec §8.3): the live, non-proxy
// registry entry with that session id. Two live entries for one session id
// (a process pair mid-resume) answer the first in registry order; both
// describe the same conversation, and the Origin fields that differ (pid,
// inbox) are display and liveness only.
func (r *OriginResolver) ResolveOriginBySession(sessionID string) (team.Origin, bool, error) {
	if sessionID == "" {
		return team.Origin{}, false, nil
	}
	entries, _, err := ipeers.ReadRegistry(r.m.registryDir, r.m.liveness)
	if err != nil {
		r.m.logf("peers: origin resolver: read registry: %v", err)
		return team.Origin{}, false, fmt.Errorf("read registry: %w", err)
	}
	proxies := r.m.proxyPIDs()
	for _, e := range entries {
		if e.SessionID == sessionID && !e.IsProxy && !proxies[e.PID] {
			return r.originOf(e), true, nil
		}
	}
	return team.Origin{}, false, nil
}

// originOf renders a registry entry as a team.Origin: ref, address (the
// rule GET /api/peers uses, record.go applyIdentity) and title.
func (r *OriginResolver) originOf(e ipeers.Entry) team.Origin {
	ref := ipeers.RefID(e.SessionID)
	alias := r.m.configSnapshot().alias
	addr := alias + "/" + ref
	if ipeers.RoutableName(e.Name) { // the rule GET /api/peers uses (internal/peers/record.go applyIdentity)
		addr = alias + "/" + e.Name
	}
	return team.Origin{
		SessionID: e.SessionID,
		Ref:       ref,
		Name:      e.Name,
		PID:       e.PID,
		ProcStart: e.ProcStart,
		Cwd:       e.Cwd,
		Tmux:      e.Tmux,
		Title:     r.titleOf(e.SessionID),
		Address:   addr,
	}
}
```

- [ ] **Step 4: Run and see it pass.** `go test ./internal/module/peers/` → `ok` (≈ 15 s; the whole package).

- [ ] **Step 5: Commit.**
  ```bash
  git add internal/module/peers/module.go internal/module/peers/lineage_test.go internal/module/peers/send.go internal/module/peers/send_test.go internal/module/peers/origin_resolver.go internal/module/peers/origin_resolver_session_test.go internal/module/team/lineage_path_test.go
  git commit -m "feat(peers): inventory carries relay lineage, hint says relays keep a ref, resolver by session id

  Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
  ```

### Task 5a.6: Host config section `relay`

> In **P5a-1b** (moved here from P5a-2a by the codex round so that P5a-2a is ≤ 800 lines): the section is a leaf of `hostconfig` with no reader yet — the team module starts reading `RelaySwitchesKey` in P5a-2a (Task 5a.7).

**Files:**
- Create: `internal/module/hostconfig/relay.go`
- Modify: `internal/module/hostconfig/handler.go:48-53` (`emptyFor`), `:57-63` (GET field map); `internal/module/hostconfig/module.go:26-32` (`Init` registers the reader), `:35-42` (the PUT route); `internal/module/hostconfig/handler_test.go:19-24` (`TestHandlerGetEmpty`'s body gains the `relay` field)
- Test: `internal/module/hostconfig/relay_test.go`

**Interfaces:**
- Produces (package `hostconfig`): `KeyRelay = "relay"`, `RelaySwitchesKey = "hostconfig.relay-switches"`, `type RelaySwitches struct { SelfSolo bool \`json:"self_solo"\`; SelfLead bool \`json:"self_lead"\` }`, `DefaultRelaySwitches`, `type RelaySwitchReader interface { RelaySwitches() (RelaySwitches, error) }`, `func (m *Module) RelaySwitches() (RelaySwitches, error)`; `GET /api/hostconfig` gains `"relay": {"items": {...}, "revision": N}` (defaults `{"self_solo":true,"self_lead":true}` at revision 0); `PUT /api/hostconfig/relay {items, baseRevision}` with the usual CAS (200 / 409 with the server copy / 400).
- Consumes: `putHandler`, `Store.Get`, `firstByte`.

- [ ] **Step 1: Write the failing test.**

```go
package hostconfig

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Lead-team-relay spec §8.7 (a): the relay switches are a host config
// section; both default to true, a PUT may set either, the reader the team
// module uses answers the stored value or the defaults.
func TestRelaySwitches_DefaultsPutAndReader(t *testing.T) {
	m := newTestModule(t)
	sw, err := m.RelaySwitches()
	require.NoError(t, err)
	assert.Equal(t, DefaultRelaySwitches, sw)

	rr := serve(m, http.MethodPut, "/api/hostconfig/relay", `{"items":{"self_solo":false},"baseRevision":0}`)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	assert.JSONEq(t, `{"items":{"self_solo":false,"self_lead":true},"revision":1}`, rr.Body.String())
	sw, err = m.RelaySwitches()
	require.NoError(t, err)
	assert.Equal(t, RelaySwitches{SelfSolo: false, SelfLead: true}, sw)

	rr = serve(m, http.MethodGet, "/api/hostconfig", "")
	require.Equal(t, http.StatusOK, rr.Code)
	assert.Contains(t, rr.Body.String(), `"relay":{"items":{"self_solo":false,"self_lead":true},"revision":1}`)

	// Stale revision: 409 with the server copy.
	rr = serve(m, http.MethodPut, "/api/hostconfig/relay", `{"items":{"self_lead":false},"baseRevision":0}`)
	require.Equal(t, http.StatusConflict, rr.Code, rr.Body.String())
	assert.JSONEq(t, `{"items":{"self_solo":false,"self_lead":true},"revision":1}`, rr.Body.String())

	// Not an object, or not booleans: 400, nothing stored.
	for _, body := range []string{`{"items":[true],"baseRevision":1}`, `{"items":{"self_solo":"yes"},"baseRevision":1}`} {
		rr = serve(m, http.MethodPut, "/api/hostconfig/relay", body)
		assert.Equal(t, http.StatusBadRequest, rr.Code, body)
	}
	e, err := m.store.Get(KeyRelay)
	require.NoError(t, err)
	assert.Equal(t, int64(1), e.Revision)
}
```

And in `handler_test.go:19-24`, the expected GET body of `TestHandlerGetEmpty` gains one line:

```go
	assert.JSONEq(t, `{
		"projects":{"items":[],"revision":0},
		"commands":{"items":[],"revision":0},
		"resumeTemplates":{"items":{},"revision":0},
		"quickReplies":{"items":[],"revision":0},
		"relay":{"items":{"self_solo":true,"self_lead":true},"revision":0}
	}`, rr.Body.String())
```

- [ ] **Step 2: Run and see it fail.** `go test ./internal/module/hostconfig/ -run 'TestRelaySwitches|TestHandlerGetEmpty' -v` → `m.RelaySwitches undefined`, `undefined: DefaultRelaySwitches`; `TestHandlerGetEmpty` fails on the missing `relay` field.

- [ ] **Step 3: Implement.** Create `relay.go`:

```go
package hostconfig

import (
	"encoding/json"
	"errors"
)

// KeyRelay is the host_config row of the relay switches (lead-team-relay
// spec §8.7 (a)): per host, read by the team module when a session asks to
// self-relay. Both default to true; a member has no switch (U13).
const KeyRelay = "relay"

// RelaySwitchesKey is the service-registry key under which Init publishes
// the module as a RelaySwitchReader for the team module.
const RelaySwitchesKey = "hostconfig.relay-switches"

// RelaySwitches is the stored shape and the GET field `relay.items`.
type RelaySwitches struct {
	SelfSolo bool `json:"self_solo"` // a session that is neither lead nor member
	SelfLead bool `json:"self_lead"` // a lead
}

// DefaultRelaySwitches is what a host that never wrote the row reads as.
var DefaultRelaySwitches = RelaySwitches{SelfSolo: true, SelfLead: true}

// relaySwitchesJSON is DefaultRelaySwitches as the GET answers it for a
// never-written key (emptyFor).
const relaySwitchesJSON = `{"self_solo":true,"self_lead":true}`

// RelaySwitchReader is what the team module type-asserts on the registry value.
type RelaySwitchReader interface {
	RelaySwitches() (RelaySwitches, error)
}

// normalizeRelay validates a PUT body: a JSON object whose two fields are
// booleans; a field left out keeps its default (true). Anything else is
// a validation error.
func normalizeRelay(raw json.RawMessage) (RelaySwitches, error) {
	if firstByte(raw) != '{' {
		return RelaySwitches{}, errors.New("items must be a JSON object")
	}
	var in struct {
		SelfSolo *bool `json:"self_solo"`
		SelfLead *bool `json:"self_lead"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return RelaySwitches{}, errors.New("self_solo and self_lead must be booleans")
	}
	out := DefaultRelaySwitches
	if in.SelfSolo != nil {
		out.SelfSolo = *in.SelfSolo
	}
	if in.SelfLead != nil {
		out.SelfLead = *in.SelfLead
	}
	return out, nil
}

// RelaySwitches reads the stored switches, defaults for a never-written
// key. A stored value that no longer decodes is an error, not a silent
// "on": the team module then refuses self relay with 503 rather than
// relaying against a switch it could not read.
func (m *Module) RelaySwitches() (RelaySwitches, error) {
	e, err := m.store.Get(KeyRelay)
	if err != nil {
		return RelaySwitches{}, err
	}
	if e.Value == nil {
		return DefaultRelaySwitches, nil
	}
	return normalizeRelay(e.Value)
}
```

`handler.go`: replace `emptyFor` (`:48-53`) with

```go
func emptyFor(key string) string {
	switch key {
	case KeyResumeTemplates:
		return `{}`
	case KeyRelay:
		return relaySwitchesJSON
	}
	return `[]`
}
```

and add `"relay":           KeyRelay,` after `"quickReplies":    KeyQuickReplies,` in `handleGet`'s map (`:62`). `module.go`: in `RegisterRoutes` add, before the `check-path` line (`:41`):

```go
	mux.HandleFunc("PUT /api/hostconfig/relay", m.putHandler(KeyRelay, func(raw []byte) (any, error) { return normalizeRelay(raw) }))
```

and replace `Init`'s body (`:28-31`) with:

```go
	m.core = c
	var err error
	m.store, err = OpenStore(filepath.Join(c.Cfg.DataDir, "host_config.db"))
	if err != nil {
		return err
	}
	// The team module reads the relay switches through this view (spec §8.7 (a)).
	c.Registry.Register(RelaySwitchesKey, m)
	return nil
```

- [ ] **Step 4: Run and see it pass.** `go test ./internal/module/hostconfig/` → `ok`.

- [ ] **Step 5: Commit.**
  ```bash
  git add internal/module/hostconfig/relay.go internal/module/hostconfig/relay_test.go internal/module/hostconfig/handler.go internal/module/hostconfig/module.go internal/module/hostconfig/handler_test.go
  git commit -m "feat(hostconfig): relay section — self_solo and self_lead switches, default on, with a reader for team

  Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
  ```

- [ ] **Gate for P5a-1b:** `go build ./... && go vet ./... && go test ./internal/peers/ ./internal/module/peers/ ./internal/module/hostconfig/ ./internal/module/team/` green (the team package for `TestLineagePath_*`).

---

## PR P5a-2a — the pause, `hello` / `self` / `begin`, and an approval's close moves its op

**Scope.** The team module depends on `hostconfig` (reads P5a-1b's `RelaySwitchReader` through the registry), takes a `TitleMover`, registers its `LineageReader`, mints ids, and gains the relay directory; `OriginResolver` gains `ResolveOriginBySession`; routes `POST /api/relay/hello`, `POST /api/relay/begin`, `GET /api/relay/wait/{id}`, `POST /api/relay/self`; `handleCreate` keeps refusing kind `self_relay` (the one door is `begin`); `handleDecide` skips the grant for a `self_relay` row; `closeWith`'s winner branch calls `afterClose`, which moves the op (approve → `claimed`; deny → `cancelled{denied}`; timeout → `cancelled{timeout}`; cancelled/abandoned → `cancelled{abandoned}`). The report/op routes, inflight and boot reconciliation are **P5a-2b**.

### Task 5a.7: Team module wiring, `hello` / `self` / `begin`, and `afterClose`

> `:line`s below are against alpha.527; **re-verify them after P2c-1 merges** (`module.go` gains `dataDir` after `:37`, `m.dataDir = …` after `:110`, the decide route at `:121` and `pruneHookLocks()` in `Start`; `handler_test.go`'s fixture may have moved). Apply by anchor text (the function name or the quoted line), not by line.

**Files:**
- Create: `internal/module/team/relay_handler.go`
- Modify: `internal/module/team/module.go:3-16` (imports `github.com/google/uuid`, `internal/module/hostconfig`, `os`), `:26-29` (`OriginResolver` gains `ResolveOriginBySession`), `:33-37` (fields), `:78-87` (`New` + `WithTitles`), `:90` (`Dependencies`), `:95-112` (`Init`), `:115-122` (four routes), `:127-140` (`Start`: `MkdirAll(m.relayDir)` first), `:192-195` (`closeWith` winner → `afterClose`); `internal/module/team/handler.go:122-124` (the self_relay detail), `:387-388` (the grant guard); `internal/module/team/handler_test.go:17-21` (import), `:58` (after `uid`: `sequentialIDs`, `rid`, `fakeOrigins.ResolveOriginBySession`, `fakeSwitches`), `:77-85` (`fixture` fields + `fakeTitles`), `:91-96` (`newFixture`); `cmd/pdx/main.go:352-358, 365-366` (`titleMover`, `teammod.New().WithTitles(...)`); `cmd/pdx/team_register_test.go` (two asserts)
- Test: `internal/module/team/relay_handler_test.go`

**Interfaces:**
- Produces (package `teammod`): `type TitleMover interface { Move(fromSessionID, toSessionID string, now time.Time) (bool, error) }`; `func (m *Module) WithTitles(t TitleMover) *Module`; `OriginResolver` gains `ResolveOriginBySession(sessionID string) (team.Origin, bool, error)`; `Dependencies()` = `{"agent", "peers", "hostconfig"}`; `Init` registers `team.LineageReaderKey → *Store`, requires `hostconfig.RelaySwitchesKey` and optionally takes `agent.ContextUsageReader` from `agent.OwnerResolverKey` (fills the self-relay payload's `model_id` / `effort`); routes `POST /api/relay/hello` → 200 `RelayHelloResponse`; `POST /api/relay/self` → 200 `RelaySelfResponse` | 409 `member_relay_is_leads` | 400; `POST /api/relay/begin` → 201 `RelayBeginResponse` | 400 (not self / bad numbers) | 404 `unknown_session` | 409 `member_relay_is_leads` / `self_relay_off` / `self_relay_paused` / `relay_open` (carries `op`) | 503 `not_ready`; `GET /api/relay/wait/{id}?wait=N` → `handleGet` (200 Approval, lease renewed; 404 `not_found`). `relayRole(sessionID) string` returns `"none"` (P4 replaces its body).
- Consumes: Tasks 5a.2, 5a.3, 5a.5, 5a.6; `closeWith`, `broadcast`, `createMu`, `requestHash`, `hostID()`.

**Decisions (binding for P5b):**
1. `begin` is the only door: `POST /api/team/approvals` with kind `self_relay` stays `400 unsupported_kind` (detail now names `/api/relay/begin`). A self relay needs an op beside its row and the usage fields; the generic route has neither.
2. The op row is inserted **before** the approval row; if the approval insert fails, the op is cancelled{abandoned} and the call is 500. A begin while any non-terminal op exists for the session — awaiting, claimed, writing … — is `409 relay_open` **carrying that op** (`APIError.op`): the mod's replay after a dropped connection gets the op it opened, exit 13 at the CLI (P5a-2c prints the op on stdout).
3. `afterClose` runs after the winner's broadcast and wake, inside `closeWith`, so decide, DELETE, the sweeper's timeout/lease and the origin-gone close all move the op through one function. It is not transactional with the row's close; P5a-2b's boot reconciliation re-derives the op from a closed row it missed.
4. The approval row's `deadline_at` is `now + 600 s` (spec §8.7 "10 minutes, absolute"), `lease_until` `now + 30 s`, renewed by `GET /api/relay/wait/{id}` exactly as a lead request's poll renews it; the sweeper's lease path (`abandoned`) is what closes a request whose mod stopped polling (spec §8.7 (d)).
5. `self on` under a host switch that is off answers `self_relay: "off"`: a session switch only narrows (spec §8.7 (a)).
6. `hello` is recorded in memory (`m.modSeen map[string]helloInfo`, cap 512, oldest evicted) — the **single** mod-presence record: P6's `relay_unsupported` check reads it, and P8a-1a's `modPresent(sid)` reads it for the terminal-only degradation (P8a declares no map of its own). It does not require the session to be live.

- [ ] **Step 1: Write the failing tests.** Test fixture changes in `handler_test.go` — import `"github.com/wake/purdex/internal/module/agent"` and `"github.com/wake/purdex/internal/module/hostconfig"` after `internal/core` (`:19`); after `uid` (`:58`) add:

```go
// sequentialIDs mints the daemon-side ids relay begin uses: op then request,
// "11111111-…-0001", "…-0002", … so tests can name them.
func sequentialIDs() func() string {
	n := 0
	return func() string {
		n++
		return fmt.Sprintf("11111111-1111-4111-8111-%012x", n)
	}
}

func rid(i int) string { return fmt.Sprintf("11111111-1111-4111-8111-%012x", i) }

// ResolveOriginBySession answers the fixture origin with that session id.
func (f *fakeOrigins) ResolveOriginBySession(sid string) (team.Origin, bool, error) {
	f.mu.Lock()
	readErr := f.readErr
	f.mu.Unlock()
	if readErr {
		return team.Origin{}, false, errors.New("read registry: not a directory")
	}
	for _, o := range fixtureOrigins {
		if o.SessionID == sid {
			return o, true, nil
		}
	}
	return team.Origin{}, false, nil
}

// fakeSwitches is the hostconfig RelaySwitchReader of these tests.
type fakeSwitches struct {
	mu  sync.Mutex
	sw  hostconfig.RelaySwitches
	err error
}

func (f *fakeSwitches) RelaySwitches() (hostconfig.RelaySwitches, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sw, f.err
}

func (f *fakeSwitches) set(sw hostconfig.RelaySwitches) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sw = sw
}
```

Replace the `fixture` struct (`:77-85`) with:

```go
type fixture struct {
	t        *testing.T
	m        *Module
	mux      *http.ServeMux
	core     *core.Core
	clock    atomic.Int64 // unix ms
	origins  *fakeOrigins
	switches *fakeSwitches
	titles   *fakeTitles
	usage    *fakeUsage
	sub      *core.EventSubscriber
}

// fakeUsage is the agent module's ContextUsageReader of these tests: the
// per-session statusline reading begin copies model_id / effort from.
// Registered under agent.OwnerResolverKey, as the agent module is in
// production (the team module type-asserts the reader on that service,
// as peers does).
type fakeUsage struct {
	mu sync.Mutex
	by map[string]agent.ContextUsage
}

func (f *fakeUsage) set(sid, model, effort string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.by == nil {
		f.by = map[string]agent.ContextUsage{}
	}
	f.by[sid] = agent.ContextUsage{ModelID: model, Effort: effort}
}

func (f *fakeUsage) ContextUsage(sid string) (agent.ContextUsage, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	u, ok := f.by[sid]
	return u, ok
}

var _ agent.ContextUsageReader = (*fakeUsage)(nil)

// fakeTitles records title moves (spec §8.4); *store.PeerLabelStore in production.
type fakeTitles struct {
	mu    sync.Mutex
	moves [][2]string
	has   map[string]bool // sessions that currently hold a title
}

func (f *fakeTitles) Move(from, to string, _ time.Time) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.has[from] {
		return false, nil
	}
	delete(f.has, from)
	f.has[to] = true
	f.moves = append(f.moves, [2]string{from, to})
	return true, nil
}
```

and in `newFixture` replace lines 91-96 with:

```go
	f := &fixture{t: t, origins: &fakeOrigins{}, switches: &fakeSwitches{sw: hostconfig.DefaultRelaySwitches}, titles: &fakeTitles{has: map[string]bool{"sid-1": true}}, usage: &fakeUsage{}}
	f.clock.Store(1_000_000)
	f.core = core.New(core.CoreDeps{Config: &config.Config{HostID: "h:1", DataDir: t.TempDir()}})
	f.core.Registry.Register(peersmod.OriginResolverKey, f.origins)
	f.core.Registry.Register(hostconfig.RelaySwitchesKey, f.switches)
	f.core.Registry.Register(agent.OwnerResolverKey, f.usage) // the team module asserts agent.ContextUsageReader on it
	f.m = New().WithTitles(f.titles)
	f.m.newID = sequentialIDs()
```

New `relay_handler_test.go`:

```go
package teammod

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wake/purdex/internal/module/hostconfig"
	"github.com/wake/purdex/internal/team"
)

func beginReq(sid string) team.RelayBeginRequest {
	return team.RelayBeginRequest{SessionID: sid, Self: true, UsedPercentage: 72.4, Window: 1_000_000}
}

func (f *fixture) begin(sid string) team.RelayBeginResponse {
	f.t.Helper()
	code, body := f.do(http.MethodPost, "/api/relay/begin", beginReq(sid))
	if code != http.StatusCreated {
		f.t.Fatalf("begin %s: %d %s", sid, code, body)
	}
	var out team.RelayBeginResponse
	if err := json.Unmarshal(body, &out); err != nil {
		f.t.Fatal(err)
	}
	return out
}

func (f *fixture) op(id string) team.RelayOp {
	f.t.Helper()
	op, ok, err := f.m.store.GetRelayOp(id)
	if err != nil || !ok {
		f.t.Fatalf("op %s: ok=%v err=%v", id, ok, err)
	}
	return op
}

func (f *fixture) decide(id, decision string) (int, []byte) {
	return f.do(http.MethodPost, "/api/team/approvals/"+id+"/decide",
		team.DecideRequest{Decision: decision, Client: team.Client{Kind: "app", Label: "Purdex.app @ air26"}})
}

// Spec §8.7 (b): begin opens one op in awaiting_approval and one self_relay
// row together (10 min deadline, 30 s lease, payload with op_id and the
// usage), broadcasts opened, and makes the handoff directory. The payload's
// model_id / effort come from the DAEMON's statusline reading for the
// session (agent.ContextUsageReader), not from the request — the request
// has no such fields. Mutation gate: drop the m.usage lookup in
// handleRelayBegin → p.ModelID == "" → red.
func TestRelayBegin_OpensOpAndApprovalTogether(t *testing.T) {
	f := newFixture(t)
	f.usage.set("sid-1", "claude-opus-5-5", "high")
	out := f.begin("sid-1")
	if out.Op.ID != rid(1) || out.RequestID != rid(2) || out.Op.RequestID != rid(2) {
		t.Fatalf("ids: %+v", out)
	}
	if out.Op.State != team.RelayAwaitingApproval || out.Op.Kind != team.RelayKindSelf || out.Op.SessionID != "sid-1" || out.Op.Ref != "_abc123" ||
		out.Op.UsedPercentage == nil || *out.Op.UsedPercentage != 72.4 || out.Op.HostID != "h:1" {
		t.Fatalf("op = %+v", out.Op)
	}
	if want := filepath.Join(f.core.Cfg.DataDir, "relay", rid(1)+".md"); out.Op.HandoffPath != want {
		t.Fatalf("handoff_path = %q, want %q", out.Op.HandoffPath, want)
	}
	if st, err := os.Stat(filepath.Join(f.core.Cfg.DataDir, "relay")); err != nil || !st.IsDir() {
		t.Fatalf("relay dir: %v", err)
	}
	a, ok, err := f.m.store.Get(rid(2))
	if err != nil || !ok || a.Kind != team.KindSelfRelay || a.State != team.StateOpen || a.Origin.SessionID != "sid-1" ||
		a.DeadlineAt != 1_000_000+600_000 || a.LeaseUntil != 1_000_000+30_000 {
		t.Fatalf("approval = %+v ok=%v err=%v", a, ok, err)
	}
	var p team.SelfRelayPayload
	if err := json.Unmarshal(a.Payload, &p); err != nil || p.OpID != rid(1) || p.UsedPercentage != 72.4 || p.Window != 1_000_000 || p.ModelID != "claude-opus-5-5" || p.Effort != "high" {
		t.Fatalf("payload = %+v err=%v", p, err)
	}
	evs := f.events()
	if len(evs) != 1 || evs[0].Op != "opened" || evs[0].Approval.ID != rid(2) || evs[0].Approval.Kind != team.KindSelfRelay {
		t.Fatalf("events = %+v", evs)
	}
	// A second begin while the first is open: 409 relay_open carrying the op.
	code, body := f.do(http.MethodPost, "/api/relay/begin", beginReq("sid-1"))
	ae := decodeErr(t, body)
	if code != http.StatusConflict || ae.Error != team.ErrRelayOpen || ae.Op == nil || ae.Op.ID != rid(1) {
		t.Fatalf("second begin: %d %+v", code, ae)
	}
	// The generic create route still refuses the kind: begin is the one door.
	code, body = f.do(http.MethodPost, "/api/team/approvals", team.CreateApprovalRequest{ID: uid(9), Kind: team.KindSelfRelay, OriginInbox: "/tmp/20.sock", Reason: "x"})
	if code != http.StatusBadRequest || decodeErr(t, body).Error != team.ErrUnsupportedKind {
		t.Fatalf("generic create self_relay: %d %s", code, body)
	}
}

// A session whose statusline never reported (or a daemon without the agent
// module, m.usage nil) gets a payload without model_id / effort — the JSON
// omits both (omitempty) and the dialog shows neither; nothing fails.
func TestRelayBegin_NoStatuslineReadingLeavesModelAndEffortEmpty(t *testing.T) {
	f := newFixture(t) // f.usage holds no reading for sid-2
	out := f.begin("sid-2")
	a, _, _ := f.m.store.Get(out.RequestID)
	if strings.Contains(string(a.Payload), "model_id") || strings.Contains(string(a.Payload), "effort") {
		t.Fatalf("payload must omit model_id/effort without a reading: %s", a.Payload)
	}
	g := newFixture(t)
	g.m.usage = nil // a daemon whose agent module is absent
	if out := g.begin("sid-1"); out.Op.SessionID != "sid-1" {
		t.Fatalf("begin without a usage reader: %+v", out)
	}
}

// Spec §8.1: the three 409 refusals and the two 4xx shapes.
func TestRelayBegin_Refusals(t *testing.T) {
	f := newFixture(t)
	check := func(sid string, body any, wantCode int, wantErr string) {
		t.Helper()
		code, raw := f.do(http.MethodPost, "/api/relay/begin", body)
		if code != wantCode || decodeErr(t, raw).Error != wantErr {
			t.Fatalf("%s: %d %s, want %d %s", sid, code, raw, wantCode, wantErr)
		}
	}
	check("unknown", beginReq("sid-99"), http.StatusNotFound, team.ErrUnknownSession)
	notSelf := beginReq("sid-1")
	notSelf.Self = false
	check("not self", notSelf, http.StatusBadRequest, team.ErrBadRequest)
	f.switches.set(hostconfig.RelaySwitches{SelfSolo: false, SelfLead: true})
	check("switch off", beginReq("sid-1"), http.StatusConflict, team.ErrSelfRelayOff)
	f.switches.set(hostconfig.DefaultRelaySwitches)
	if code, _ := f.do(http.MethodPost, "/api/relay/self", team.RelaySelfRequest{SessionID: "sid-1", Action: "off"}); code != http.StatusOK {
		t.Fatalf("self off: %d", code)
	}
	check("paused", beginReq("sid-1"), http.StatusConflict, team.ErrSelfRelayPaused)
	// Nothing opened on any refusal.
	if active, _ := f.m.store.ListActiveRelayOps(); len(active) != 0 {
		t.Fatalf("active ops after refusals = %+v", active)
	}
	if n := len(f.events()); n != 0 {
		t.Fatalf("%d events after refusals", n)
	}
}

// Spec §8.3 hello and §8.7 (a) the pause: hello reports role none (P4 fills
// it), the thresholds and the effective state; self off/on/status narrows
// the session only — never lifts a host switch that is off.
func TestRelayHelloAndSelf(t *testing.T) {
	f := newFixture(t)
	code, body := f.do(http.MethodPost, "/api/relay/hello", team.RelayHelloRequest{SessionID: "sid-1", ModVersion: "1", Agent: "cc"})
	var h team.RelayHelloResponse
	if err := json.Unmarshal(body, &h); code != http.StatusOK || err != nil || !h.OK || h.Role != "none" || h.SelfRelay != "on" || h.Threshold != 70 || h.MinGrowth != 20000 {
		t.Fatalf("hello: %d %s", code, body)
	}
	self := func(action string) team.RelaySelfResponse {
		t.Helper()
		code, body := f.do(http.MethodPost, "/api/relay/self", team.RelaySelfRequest{SessionID: "sid-1", Action: action})
		var r team.RelaySelfResponse
		if err := json.Unmarshal(body, &r); code != http.StatusOK || err != nil {
			t.Fatalf("self %s: %d %s", action, code, body)
		}
		return r
	}
	if r := self("status"); r.SelfRelay != "on" || !r.HostSwitch || r.Member {
		t.Fatalf("status = %+v", r)
	}
	if r := self("off"); r.SelfRelay != "paused" || !r.HostSwitch {
		t.Fatalf("off = %+v", r)
	}
	f.switches.set(hostconfig.RelaySwitches{SelfSolo: false, SelfLead: true})
	if r := self("on"); r.SelfRelay != "off" || r.HostSwitch {
		t.Fatalf("on under a host switch that is off = %+v (a session switch only narrows)", r)
	}
	f.switches.set(hostconfig.DefaultRelaySwitches)
	if r := self("status"); r.SelfRelay != "on" {
		t.Fatalf("after on + switch restored = %+v", r)
	}
	for _, bad := range []team.RelaySelfRequest{{SessionID: "", Action: "on"}, {SessionID: "sid-1", Action: "maybe"}} {
		if code, _ := f.do(http.MethodPost, "/api/relay/self", bad); code != http.StatusBadRequest {
			t.Fatalf("%+v: %d", bad, code)
		}
	}
	if code, _ := f.do(http.MethodPost, "/api/relay/hello", team.RelayHelloRequest{}); code != http.StatusBadRequest {
		t.Fatalf("hello without session_id: %d", code)
	}
}

// Spec §8.7 (b): approved → the op is claimed; denied → cancelled{denied};
// the sweeper's timeout → cancelled{timeout}; a vanished origin →
// cancelled{abandoned}. Every path goes through the same CAS and one
// closed broadcast, and /api/relay/wait is the row's long-poll.
func TestRelayApprovalCloseMovesTheOp(t *testing.T) {
	f := newFixture(t)
	// approve
	out := f.begin("sid-1")
	code, _ := f.decide(out.RequestID, "approve")
	if code != http.StatusOK {
		t.Fatalf("approve: %d", code)
	}
	if op := f.op(out.Op.ID); op.State != team.RelayClaimed {
		t.Fatalf("after approve: %+v", op)
	}
	f.events()
	// A claimed op is still "open" for the session: a new begin is relay_open until it ends.
	if code, _ := f.do(http.MethodPost, "/api/relay/begin", beginReq("sid-1")); code != http.StatusConflict {
		t.Fatalf("begin while claimed: %d", code)
	}
	// End it through the store (the report route is P5a-2b).
	if _, res, err := f.m.store.ReportRelay(out.Op.ID, RelayReport{State: team.RelayFailed, Reason: team.RelayReasonHandoffIncomplete, At: f.clock.Load()}); err != nil || res != ReportApplied {
		t.Fatalf("end op: res=%v err=%v", res, err)
	}

	// deny
	out = f.begin("sid-1")
	if code, _ := f.decide(out.RequestID, "deny"); code != http.StatusOK {
		t.Fatalf("deny: %d", code)
	}
	if op := f.op(out.Op.ID); op.State != team.RelayCancelled || op.Reason != team.RelayReasonDenied {
		t.Fatalf("after deny: %+v", op)
	}

	// timeout (sweeper): the wait route renews the lease first, as the mod does.
	out = f.begin("sid-1")
	code, body := f.do(http.MethodGet, "/api/relay/wait/"+out.RequestID+"?wait=0", nil)
	if code != http.StatusOK || decodeApproval(t, body).ID != out.RequestID {
		t.Fatalf("wait: %d %s", code, body)
	}
	f.clock.Add(600_001)
	f.m.tick()
	if a, _, _ := f.m.store.Get(out.RequestID); a.State != team.StateTimeout {
		t.Fatalf("approval after deadline: %+v", a)
	}
	if op := f.op(out.Op.ID); op.State != team.RelayCancelled || op.Reason != team.RelayReasonTimeout {
		t.Fatalf("after timeout: %+v", op)
	}

	// abandoned: the origin session is gone (10th tick liveness check).
	out = f.begin("sid-2")
	f.origins.markDead("sid-2")
	f.m.tickN = livenessEvery - 1
	f.m.tick()
	if op := f.op(out.Op.ID); op.State != team.RelayCancelled || op.Reason != team.RelayReasonAbandoned {
		t.Fatalf("after origin gone: %+v", op)
	}
	if n := f.countOps("closed"); n != 3 { // deny + timeout + abandoned since the drain after approve
		t.Fatalf("closed events = %d, want 3 (one per close, whoever closed)", n)
	}
}

// Coordinator decision: <data_dir>/relay/ is made by the daemon at Start
// and again in begin; the mod never creates it. Start on a fresh data dir
// leaves the directory in place (0700) before any begin.
func TestStart_MakesTheRelayDir(t *testing.T) {
	f := newFixture(t)
	dir := filepath.Join(f.core.Cfg.DataDir, team.RelayDir)
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("precondition: relay dir must not exist before Start (err=%v)", err)
	}
	if err := f.m.Start(context.Background()); err != nil { // newFixture's Cleanup stops it
		t.Fatal(err)
	}
	st, err := os.Stat(dir)
	if err != nil || !st.IsDir() || st.Mode().Perm() != 0o700 {
		t.Fatalf("relay dir after Start: err=%v mode=%v", err, st)
	}
	// begin after an operator removed it: re-created, the handoff path is under it.
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	out := f.begin("sid-1")
	if _, err := os.Stat(dir); err != nil || filepath.Dir(out.Op.HandoffPath) != dir {
		t.Fatalf("begin must re-create the dir: err=%v path=%s", err, out.Op.HandoffPath)
	}
}
```

(`context` joins the test file's imports. `newFixture` must not call `Start` itself; it does not in the shipped fixture — `handler_test.go:88-109` builds and `Init`s only.)

- [ ] **Step 2: Run and see them fail.** `go test ./internal/module/team/ -run 'TestRelayBegin|TestRelayHello|TestRelayApproval|TestStart_MakesTheRelayDir' -v` → compile errors (`undefined: hostconfig`, `f.m.newID undefined`, `New().WithTitles undefined`, `*fakeOrigins does not implement OriginResolver (missing method ResolveOriginBySession)` once the interface changes).

- [ ] **Step 3: Implement.** `module.go` edits (exact):
  - imports: add `"github.com/google/uuid"` (own group, after the stdlib block), `"github.com/wake/purdex/internal/module/agent"` and `"github.com/wake/purdex/internal/module/hostconfig"` after `internal/core` (teammod → agent is a new edge; agent imports nothing of team, and peersmod already imports agent, so no cycle);
  - `OriginResolver` (`:26-29`): add after `ResolveOrigin`:
    ```go
    	// ResolveOriginBySession is ResolveOrigin keyed by CC session id: the
    	// relay routes are called by the mod with its session id, not its
    	// inbox (P5a). Same ok/err contract.
    	ResolveOriginBySession(sessionID string) (team.Origin, bool, error)
    ```
  - fields (after `logf` at `:37`):
    ```go
    	// P5a: the relay switches (host config), the title mover (meta.db; nil
    	// without a meta store), the op/request id minter, the handoff
    	// directory and what each session's mod said in hello (under mu).
    	// modSeen is THE mod-presence record: P6 reads it for
    	// relay_unsupported, P8a-1a's modPresent() reads it for the
    	// terminal-only degradation; nothing else writes it.
    	switches hostconfig.RelaySwitchReader
    	titles   TitleMover
    	// usage is the agent module's per-session statusline reading; begin
    	// copies model_id / effort from it into the self_relay payload (the mod
    	// sends neither). Nil when the agent module is absent: both stay "".
    	usage    agent.ContextUsageReader
    	newID    func() string
    	relayDir string
    	modSeen  map[string]helloInfo
    ```
  - `New` (`:85`): add `newID: uuid.NewString,` and `modSeen: map[string]helloInfo{},` after `waiters`; then after `New`:
    ```go
    // WithTitles sets the title mover (the meta store's PeerLabels in
    // production). Nil is allowed: titles then stay on the old session id.
    func (m *Module) WithTitles(t TitleMover) *Module {
    	m.titles = t
    	return m
    }
    ```
  - `:90`: `func (m *Module) Dependencies() []string { return []string{"agent", "peers", "hostconfig"} }`
  - `Init`: after `m.origins = origins` (`:105`):
    ```go
    	sw, ok := c.Registry.Get(hostconfig.RelaySwitchesKey)
    	if !ok {
    		return fmt.Errorf("team: service %q not registered", hostconfig.RelaySwitchesKey)
    	}
    	switches, ok := sw.(hostconfig.RelaySwitchReader)
    	if !ok {
    		return fmt.Errorf("team: service %q does not implement RelaySwitchReader (%T)", hostconfig.RelaySwitchesKey, sw)
    	}
    	m.switches = switches
    	// The statusline reading lives in the agent module (P1); as peers does,
    	// type-assert the reader on the owner-resolver service rather than add
    	// a registry key. Optional: a daemon without it fills no model/effort.
    	if svc, ok := c.Registry.Get(agent.OwnerResolverKey); ok {
    		if r, ok := svc.(agent.ContextUsageReader); ok {
    			m.usage = r
    		}
    	}
    ```
    `Dependencies()` therefore lists `"agent"` too: `[]string{"agent", "peers", "hostconfig"}` (peers already depends on agent, so the order is unchanged in practice).
    and after `m.store = store` (`:110`):
    ```go
    	m.relayDir = filepath.Join(c.Cfg.DataDir, team.RelayDir)
    	// The peers inventory reads the relay lineage through this (spec §8.4).
    	c.Registry.Register(team.LineageReaderKey, store)
    ```
  - `Start` (`:127-140`), first statement: the relay directory is created at boot **and** again in `begin` (coordinator decision; the mod never creates it — a `begin` after an operator removed the dir still works, and the retention sweeper of P5a-3a finds the dir on a fresh install):
    ```go
    	// <data_dir>/relay/ exists from boot (spec §8.3); begin re-creates it
    	// too. A failure is logged, not fatal: begin reports its own.
    	if err := os.MkdirAll(m.relayDir, 0o700); err != nil {
    		m.logf("[team] relay dir %s: %v", m.relayDir, err)
    	}
    ```
  - `RegisterRoutes`: after the inflight line (`:121`):
    ```go
    	// P5a relay routes (spec §8.3, §8.7); all under TokenAuth like /api/team/*.
    	mux.HandleFunc("POST /api/relay/hello", m.handleRelayHello)
    	mux.HandleFunc("POST /api/relay/begin", m.handleRelayBegin)
    	mux.HandleFunc("GET /api/relay/wait/{id}", m.handleRelayWait)
    	mux.HandleFunc("POST /api/relay/self", m.handleRelaySelf)
    ```
  - `closeWith` (`:192-195`): the winner branch becomes
    ```go
    	if won {
    		m.broadcast("closed", &after)
    		m.wake(id)
    		m.afterClose(after)
    	}
    ```
  
  `handler.go`: at `:122-124` replace the detail with `// A self relay opens an op with its row; that is POST /api/relay/begin (P5a).` + `m.writeErr(w, http.StatusBadRequest, team.ErrUnsupportedKind, "kind self_relay opens through POST /api/relay/begin", nil)`; at `:387-388` replace `if state == team.StateApproved {` with
  ```go
	// A self_relay approval carries no grant (its payload is a
	// SelfRelayPayload); its op moves in afterClose.
	if state == team.StateApproved && a.Kind == team.KindLead {
  ```

  New `relay_handler.go`:

```go
package teammod

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/wake/purdex/internal/team"
)

// TitleMover moves a session title to a new session id (lead-team-relay
// spec §8.4); *store.PeerLabelStore in production, nil when the daemon
// runs without a meta store (tests): then no title moves and that is logged.
type TitleMover interface {
	Move(fromSessionID, toSessionID string, now time.Time) (bool, error)
}

// helloInfo is what a session's mod last said in hello (spec §8.3); P6's
// relay_unsupported check reads it, P8a-1a's modPresent() reads presence.
type helloInfo struct {
	ModVersion, Agent string
	At                int64
}

// modSeenCap bounds the modSeen map; sessions come and go and nothing else prunes it.
const modSeenCap = 512

// relayRole is the session's role for the switches (spec §8.7): "none",
// "lead" or "member". Until P4 there are no team rows, so every session
// is "none"; P4 replaces the body of this function, nothing else.
func (m *Module) relayRole(sessionID string) string { return "none" }

// selfRelayState is the effective self-relay state of a session (spec
// §8.7): a member is off with no switch (U13); otherwise the host switch
// for the role, then the session's pause. hostSwitch is the switch that
// applied; member says a member has no switch at all.
func (m *Module) selfRelayState(sessionID string) (state string, hostSwitch bool, member bool, err error) {
	if m.relayRole(sessionID) == "member" {
		return "off", false, true, nil
	}
	sw, err := m.switches.RelaySwitches()
	if err != nil {
		return "", false, false, err
	}
	hostSwitch = sw.SelfSolo
	if m.relayRole(sessionID) == "lead" {
		hostSwitch = sw.SelfLead
	}
	if !hostSwitch {
		return "off", false, false, nil
	}
	paused, err := m.store.SelfRelayPaused(sessionID)
	if err != nil {
		return "", hostSwitch, false, err
	}
	if paused {
		return "paused", hostSwitch, false, nil
	}
	return "on", hostSwitch, false, nil
}

// handleRelayHello is POST /api/relay/hello (spec §8.3): the mod says this
// session can relay and learns its role, switch state and the thresholds.
func (m *Module) handleRelayHello(w http.ResponseWriter, r *http.Request) {
	var req team.RelayHelloRequest
	if !m.decodeBody(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.SessionID) == "" {
		m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, "session_id is required", nil)
		return
	}
	state, _, _, err := m.selfRelayState(req.SessionID)
	if err != nil {
		m.logf("[team] relay hello %s: %v", req.SessionID, err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "switches unreadable; see the daemon log", nil)
		return
	}
	m.mu.Lock()
	if _, known := m.modSeen[req.SessionID]; !known && len(m.modSeen) >= modSeenCap {
		oldest, oldestAt := "", int64(0)
		for sid, h := range m.modSeen {
			if oldest == "" || h.At < oldestAt {
				oldest, oldestAt = sid, h.At
			}
		}
		delete(m.modSeen, oldest)
	}
	m.modSeen[req.SessionID] = helloInfo{ModVersion: req.ModVersion, Agent: req.Agent, At: m.now()}
	m.mu.Unlock()
	m.writeJSON(w, http.StatusOK, team.RelayHelloResponse{
		OK: true, Role: m.relayRole(req.SessionID), SelfRelay: state,
		Threshold: team.RelayThresholdPct, MinGrowth: team.RelayMinGrowth,
	})
}

// handleRelaySelf is POST /api/relay/self (spec §8.7): the per-session
// pause. "on" lifts the session's own pause only — never a host switch
// that is off; "status" reads. A member is told its relay is the lead's.
func (m *Module) handleRelaySelf(w http.ResponseWriter, r *http.Request) {
	var req team.RelaySelfRequest
	if !m.decodeBody(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.SessionID) == "" {
		m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, "session_id is required", nil)
		return
	}
	switch req.Action {
	case "on", "off":
		if m.relayRole(req.SessionID) == "member" {
			m.writeErr(w, http.StatusConflict, team.ErrMemberRelayIsLeads, "member 的接力由 lead 安排", nil)
			return
		}
		if err := m.store.SetSelfRelayPaused(req.SessionID, req.Action == "off", m.now()); err != nil {
			m.logf("[team] relay self %s: %v", req.SessionID, err)
			m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
			return
		}
	case "status":
	default:
		m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, `action must be "off", "on" or "status"`, nil)
		return
	}
	state, hostSwitch, member, err := m.selfRelayState(req.SessionID)
	if err != nil {
		m.logf("[team] relay self %s: %v", req.SessionID, err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "switches unreadable; see the daemon log", nil)
		return
	}
	m.writeJSON(w, http.StatusOK, team.RelaySelfResponse{SelfRelay: state, HostSwitch: hostSwitch, Member: member})
}

// handleRelayBegin is POST /api/relay/begin (spec §8.1, §8.7 (b)): the mod
// asks to self-relay. Role, switch and pause are checked first (409s of
// spec §14), then — under createMu, like a lead request — one op in
// awaiting_approval and one self_relay approval row open together, and
// the request id is answered. Only self: a member relay is the lead's
// POST /api/team/relays (P6).
func (m *Module) handleRelayBegin(w http.ResponseWriter, r *http.Request) {
	if m.stopping() {
		m.writeErr(w, http.StatusServiceUnavailable, team.ErrNotReady, "daemon is stopping", nil)
		return
	}
	var req team.RelayBeginRequest
	if !m.decodeBody(w, r, &req) {
		return
	}
	if !req.Self {
		m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, "only self relays begin here; a member relay is POST /api/team/relays", nil)
		return
	}
	if strings.TrimSpace(req.SessionID) == "" || req.UsedPercentage < 0 || req.UsedPercentage > 100 || req.Window < 0 {
		m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, "session_id is required; used_percentage must be 0–100 and window non-negative", nil)
		return
	}
	origin, ok, err := m.origins.ResolveOriginBySession(req.SessionID)
	if err != nil {
		m.writeErr(w, http.StatusServiceUnavailable, team.ErrNotReady, "registry unavailable; retry", nil)
		return
	}
	if !ok {
		m.writeErr(w, http.StatusNotFound, team.ErrUnknownSession, "session_id is not a live Claude Code session on this host", nil)
		return
	}
	state, _, member, err := m.selfRelayState(req.SessionID)
	if err != nil {
		m.logf("[team] relay begin %s: %v", req.SessionID, err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "switches unreadable; see the daemon log", nil)
		return
	}
	switch {
	case member:
		m.writeErr(w, http.StatusConflict, team.ErrMemberRelayIsLeads, "member 的接力由 lead 安排", nil)
		return
	case state == "off":
		m.writeErr(w, http.StatusConflict, team.ErrSelfRelayOff, "self relay is off on this host (host config relay)", nil)
		return
	case state == "paused":
		m.writeErr(w, http.StatusConflict, team.ErrSelfRelayPaused, "this session paused self relay (/relay on lifts it)", nil)
		return
	}
	// model_id / effort are the daemon's to fill (spec U18 (b), M21): the
	// session's last statusline reading, kept by the agent module. The mod
	// sends neither; a session without a reading shows neither.
	sp := team.SelfRelayPayload{UsedPercentage: req.UsedPercentage, Window: req.Window}
	if m.usage != nil {
		if u, ok := m.usage.ContextUsage(req.SessionID); ok {
			sp.ModelID, sp.Effort = u.ModelID, u.Effort
		}
	}
	payload, err := json.Marshal(sp)
	if err != nil {
		m.writeErr(w, http.StatusInternalServerError, errStorage, "encode payload: "+err.Error(), nil)
		return
	}

	m.createMu.Lock()
	defer m.createMu.Unlock()
	if m.stopping() {
		m.writeErr(w, http.StatusServiceUnavailable, team.ErrNotReady, "daemon is stopping", nil)
		return
	}
	// At most one open self-relay per session (spec §8.7 (c)): the op is
	// the authority; the approval row follows it.
	if open, found, err := m.store.OpenRelayOpBySession(req.SessionID); err != nil {
		m.logf("[team] relay begin %s: %v", req.SessionID, err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
		return
	} else if found {
		m.writeJSON(w, http.StatusConflict, team.APIError{Error: team.ErrRelayOpen, Detail: "this session already has a relay in progress", Op: &open})
		return
	}
	if err := os.MkdirAll(m.relayDir, 0o700); err != nil {
		m.logf("[team] relay begin %s: mkdir %s: %v", req.SessionID, m.relayDir, err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "cannot create the relay directory; see the daemon log", nil)
		return
	}
	now := m.now()
	opID, reqID := m.newID(), m.newID()
	pct := req.UsedPercentage
	op := team.RelayOp{
		ID: opID, Kind: team.RelayKindSelf, HostID: m.hostID(), SessionID: origin.SessionID, Ref: origin.Ref,
		RequestID: reqID, State: team.RelayAwaitingApproval, HandoffPath: filepath.Join(m.relayDir, opID+".md"),
		UsedPercentage: &pct, CreatedAt: now, UpdatedAt: now,
	}
	if err := m.store.CreateRelayOp(op); err != nil {
		m.logf("[team] relay begin %s: %v", req.SessionID, err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
		return
	}
	var p team.SelfRelayPayload
	_ = json.Unmarshal(payload, &p)
	p.OpID = opID
	payload, _ = json.Marshal(p)
	stored, _, inserted, err := m.store.Create(team.Approval{
		ID: reqID, Kind: team.KindSelfRelay, HostID: m.hostID(), Origin: origin, Payload: payload, State: team.StateOpen,
		CreatedAt: now, DeadlineAt: now + team.SelfRelayDeadlineS*1000, LeaseUntil: now + team.LeaseS*1000,
	}, requestHash(team.KindSelfRelay, origin.SessionID, team.SelfRelayDeadlineS, payload))
	if err != nil || !inserted {
		// The op is already there: close it so the session is not stuck
		// behind an op whose approval never opened.
		if _, _, rerr := m.store.ReportRelay(opID, RelayReport{State: team.RelayCancelled, Reason: team.RelayReasonAbandoned, At: now}); rerr != nil {
			m.logf("[team] relay begin %s: cancel orphan op %s: %v", req.SessionID, opID, rerr)
		}
		m.logf("[team] relay begin %s: approval row: inserted=%v err=%v", req.SessionID, inserted, err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
		return
	}
	m.logf("[team] relay op %s opened: self, origin=%s (%s) used=%.0f%% request=%s", opID, origin.Ref, origin.SessionID, req.UsedPercentage, reqID)
	m.broadcast("opened", &stored)
	m.writeJSON(w, http.StatusCreated, team.RelayBeginResponse{Op: op, RequestID: reqID})
}

// handleRelayWait is GET /api/relay/wait/{id}?wait=N: the self_relay
// approval row's long-poll, with the same lease renewal as a lead request
// (spec §8.7 (b) "The lease is renewed by the mod's wait"). It is
// handleGet under another path: the route is registered with {id} so
// PathValue agrees.
func (m *Module) handleRelayWait(w http.ResponseWriter, r *http.Request) { m.handleGet(w, r) }

func reasonSuffix(op team.RelayOp) string {
	if op.Reason == "" {
		return ""
	}
	return " (" + op.Reason + ")"
}

// afterClose follows every approval close that won (closeWith): a
// self_relay row's close moves its op (spec §8.7 (b)): approved →
// claimed; denied → cancelled{denied}; timeout → cancelled{timeout};
// cancelled or abandoned → cancelled{abandoned}. A lead row has no op.
// The two writes are not one transaction; the boot reconciliation
// re-derives the op's state from a closed row it missed.
func (m *Module) afterClose(a team.Approval) {
	if a.Kind != team.KindSelfRelay {
		return
	}
	op, ok, err := m.store.RelayOpByRequest(a.ID)
	if err != nil || !ok {
		m.logf("[team] approval %s closed but its relay op is missing: ok=%v err=%v", a.ID, ok, err)
		return
	}
	rep := RelayReport{At: m.now()}
	switch a.State {
	case team.StateApproved:
		rep.State = team.RelayClaimed
	case team.StateDenied:
		rep.State, rep.Reason = team.RelayCancelled, team.RelayReasonDenied
	case team.StateTimeout:
		rep.State, rep.Reason = team.RelayCancelled, team.RelayReasonTimeout
	default: // cancelled, abandoned
		rep.State, rep.Reason = team.RelayCancelled, team.RelayReasonAbandoned
	}
	after, res, err := m.store.ReportRelay(op.ID, rep)
	if err != nil {
		m.logf("[team] approval %s %s: relay op %s: %v", a.ID, a.State, op.ID, err)
		return
	}
	if res == ReportApplied {
		m.logf("[team] relay op %s → %s%s (approval %s %s)", op.ID, after.State, reasonSuffix(after), a.ID, a.State)
	}
}
```

  `cmd/pdx/main.go`: replace lines 352-358 with

```go
	var audit peersmod.AuditStore
	var titles peersmod.TitleStore
	var titleMover teammod.TitleMover
	if meta != nil {
		audit = meta.PeerMessages()
		titles = meta.PeerLabels()
		titleMover = meta.PeerLabels()
	}
```

  and lines 365-366 with

```go
	// team depends on peers (the origin resolver) and hostconfig (the relay
	// switches); InitModules topo-sorts. The title mover is meta.db's
	// peer_labels (nil in tests: titles then stay on the old session id).
	c.AddModule(teammod.New().WithTitles(titleMover))
```

  `cmd/pdx/team_register_test.go`: after the two `inflight` asserts add

```go
	res = doRequest(t, outer, http.MethodPost, "/api/relay/hello", "t")
	assert.Equal(t, http.StatusBadRequest, res.Code, "the relay routes are mounted (400 for an empty body, not 404)")
	assert.Equal(t, http.StatusUnauthorized, doRequest(t, outer, http.MethodPost, "/api/relay/hello", "").Code)
```

  (`doRequest` sends no body; `decodeBody` answers `400 bad_request` for empty JSON — that is what proves the route exists behind `TokenAuth`.)

- [ ] **Step 4: Run and see it pass.** `go test ./internal/module/team/ ./cmd/pdx/ -run 'TestRelay|TestRegisterServeModules|TestDecide|TestCreate|TestTick|TestStart' -v` → PASS; then the whole `go test ./internal/module/team/` (≈ 3 s) → `ok`.

- [ ] **Step 5: Commit.**
  ```bash
  git add internal/module/team/relay_handler.go internal/module/team/relay_handler_test.go internal/module/team/module.go internal/module/team/handler.go internal/module/team/handler_test.go cmd/pdx/main.go cmd/pdx/team_register_test.go
  git commit -m "feat(team): self relay begin/hello/self/wait routes; an approval's close moves its op

  Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
  ```

- [ ] **Gate for P5a-2a:** `go build ./... && go vet ./... && go test ./internal/module/team/ ./internal/module/hostconfig/ ./cmd/pdx/` green.

---

## PR P5a-2b — `report` / `op` routes, the title move, `relays_active`, boot reconciliation of self ops

**Scope.** `POST /api/relay/ops/{id}/report` (idempotent per (op, state); `cleared` writes the lineage in the store's transaction and then moves the title; 409 `bad_transition` carries the op), `GET /api/relay/ops/{id}`, `GET /api/team/inflight` fills `relays_active`, and `Start` reconciles **self** ops before the sweeper starts (spec §9.3's P5a share). Member ops in `requested` are P6's.

### Task 5a.8: `relay_report.go` — report, op, title move, reconciliation

> `:line`s below are against alpha.527; **re-verify them after P2c-1 and P5a-2a merge** (`module.go` gains `dataDir`, the decide route, `pruneHookLocks()` in `Start` and the P5a-2a fields/routes; `handler.go` gains the P5a-2a `handleDecide` guard). Apply by anchor text (the function name or the quoted line), not by line.

**Files:**
- Create: `internal/module/team/relay_report.go`
- Modify: `internal/module/team/module.go` (`RegisterRoutes`: two routes after the P5a-2a four; `Start`: `m.reconcileRelays()` right before `m.core.Events.OnSubscribe(m.sendSnapshot)`); `internal/module/team/handler.go:243-255` (`handleInflight`); `internal/team/wire.go:130` (the `InflightResponse.RelaysActive` comment only)
- Test: `internal/module/team/relay_report_test.go`

**Interfaces:**
- Produces: `POST /api/relay/ops/{id}/report {state, new_session_id?, error?}` → 200 `RelayOp` (applied or no-op) | 400 (`cleared` without `new_session_id`; `failed`/`cancelled` without `error`; unknown state) | 404 `not_found` | 409 `bad_transition` (carries `op`) | 503 `not_ready` while stopping; **a report that puts the op in a terminal state while its approval row is still open also closes that row as `cancelled` through `closeAs` (one `closed` event)** — this is what P5b-3's `report <op> cancelled --error compacted` relies on; `GET /api/relay/ops/{id}` → 200 `RelayOp` | 404; `GET /api/team/inflight` → `relays_active` = ops not in done/failed/cancelled; `func (m *Module) reconcileRelays()`; `func (m *Module) moveTitle(op team.RelayOp)`; `func (m *Module) closeRequestOfReportedOp(op team.RelayOp)`.
- Consumes: `Store.ReportRelay`, `ipeers.RefID` (`new_ref` is derived from `new_session_id`, never trusted from the body), `TitleMover`, `afterClose`, `closeAs`, `origins.LiveSession`.

**Decisions:**
1. `new_ref` is `ipeers.RefID(new_session_id)` computed by the daemon; the mod never sends a ref.
2. The `cleared` lineage row and the op's state are one team.db transaction (Task 5a.2); the title move follows in meta.db (deviation 1). `moveTitle` is idempotent (`Move` is a no-op once the old row has no label) and is re-run by `reconcileRelays` for every op still in `cleared` (a `done` op's failed title move is not retried — it is logged; the move is display only).
3. Reconciliation (self ops): an `awaiting_approval` op whose row is already closed takes the row's verdict through `afterClose`; one whose row is open but whose session is gone is **abandoned through the row's CAS** (`closeAs` → broadcast → `afterClose`), so the clients see one `closed` as in the live path; one with no row at all is `cancelled{abandoned}` directly. Nothing waits for a hook.

- [ ] **Step 1: Write the failing tests.**

```go
package teammod

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/store"
	"github.com/wake/purdex/internal/team"
)

func (f *fixture) report(id string, req team.RelayReportRequest) (int, team.RelayOp, team.APIError) {
	f.t.Helper()
	code, body := f.do(http.MethodPost, "/api/relay/ops/"+id+"/report", req)
	var op team.RelayOp
	var ae team.APIError
	if code == http.StatusOK {
		if err := json.Unmarshal(body, &op); err != nil {
			f.t.Fatal(err)
		}
	} else {
		ae = decodeErr(f.t, body)
	}
	return code, op, ae
}

func decodeRelayOp(t *testing.T, body []byte) team.RelayOp {
	t.Helper()
	var op team.RelayOp
	if err := json.Unmarshal(body, &op); err != nil {
		t.Fatalf("decode op: %v; body=%s", err, body)
	}
	return op
}

// Spec §8.3 reports and §8.4 lineage: the forward path from the mod, the
// lineage row and the title move at cleared, new_ref derived from the new
// session id, idempotent re-sends, 409 bad_transition carrying the op,
// and relays_active in inflight.
func TestRelayReport_ForwardPathLineageAndTitle(t *testing.T) {
	f := newFixture(t)
	out := f.begin("sid-1")
	f.decide(out.RequestID, "approve")
	id := out.Op.ID
	inflight := func() team.InflightResponse {
		t.Helper()
		_, body := f.do(http.MethodGet, "/api/team/inflight", nil)
		var r team.InflightResponse
		_ = json.Unmarshal(body, &r)
		return r
	}
	if r := inflight(); r.RelaysActive != 1 || r.ApprovalsOpen != 0 {
		t.Fatalf("inflight = %+v", r)
	}
	for _, st := range []team.RelayState{team.RelayWriting, team.RelayWritten} {
		if code, op, ae := f.report(id, team.RelayReportRequest{State: st}); code != http.StatusOK || op.State != st {
			t.Fatalf("report %s: %d %+v %+v", st, code, op, ae)
		}
	}
	// Re-sending the same state is 200 with the same row.
	if code, op, _ := f.report(id, team.RelayReportRequest{State: team.RelayWritten}); code != http.StatusOK || op.State != team.RelayWritten {
		t.Fatalf("written twice: %d %+v", code, op)
	}
	// cleared needs new_session_id.
	if code, _, ae := f.report(id, team.RelayReportRequest{State: team.RelayCleared}); code != http.StatusBadRequest || ae.Error != team.ErrBadRequest {
		t.Fatalf("cleared without new_session_id: %d %+v", code, ae)
	}
	code, op, _ := f.report(id, team.RelayReportRequest{State: team.RelayCleared, NewSessionID: "sid-1b"})
	if code != http.StatusOK || op.State != team.RelayCleared || op.NewSessionID != "sid-1b" || op.NewRef != ipeers.RefID("sid-1b") {
		t.Fatalf("cleared: %d %+v", code, op)
	}
	refs, err := f.m.store.PreviousRefs()
	if err != nil || len(refs["sid-1b"]) != 1 || refs["sid-1b"][0] != "_abc123" {
		t.Fatalf("lineage = %v err=%v", refs, err)
	}
	if len(f.titles.moves) != 1 || f.titles.moves[0] != [2]string{"sid-1", "sid-1b"} {
		t.Fatalf("title moves = %v", f.titles.moves)
	}
	// A stale earlier state: 409 bad_transition with the op as it is.
	code, _, ae := f.report(id, team.RelayReportRequest{State: team.RelayWriting})
	if code != http.StatusConflict || ae.Error != team.ErrBadTransition || ae.Op == nil || ae.Op.State != team.RelayCleared {
		t.Fatalf("stale report: %d %+v", code, ae)
	}
	if code, op, _ := f.report(id, team.RelayReportRequest{State: team.RelayDone}); code != http.StatusOK || op.State != team.RelayDone {
		t.Fatalf("done: %d %+v", code, op)
	}
	if r := inflight(); r.RelaysActive != 0 {
		t.Fatalf("inflight after done = %+v", r)
	}
	// failed/cancelled need a reason; unknown state and unknown op are 400/404.
	if code, _, _ := f.report(id, team.RelayReportRequest{State: team.RelayFailed}); code != http.StatusBadRequest {
		t.Fatalf("failed without error: %d", code)
	}
	if code, _, _ := f.report(id, team.RelayReportRequest{State: "flying"}); code != http.StatusBadRequest {
		t.Fatalf("unknown state: %d", code)
	}
	if code, _, _ := f.report("nope", team.RelayReportRequest{State: team.RelayDone}); code != http.StatusNotFound {
		t.Fatalf("unknown op: %d", code)
	}
	code, body := f.do(http.MethodGet, "/api/relay/ops/"+id, nil)
	if code != http.StatusOK || decodeRelayOp(t, body).State != team.RelayDone {
		t.Fatalf("get op: %d %s", code, body)
	}
}

// Spec §8.7 (c): the mod reports `cancelled --error compacted` while the
// request is still open (auto-compact mid-wait). The report moves the op
// AND closes the approval row through the same CAS as a cancel, so every
// client's dialog goes away now (one `closed` event), `pdx relay wait`
// exits 12, and afterClose — which runs on that close — is a no-op on the
// already-cancelled op (no bad_transition logged). A report on an op whose
// row is already closed closes nothing more.
func TestRelayReport_CancelledClosesTheOpenApprovalOnce(t *testing.T) {
	f := newFixture(t)
	out := f.begin("sid-1")
	f.events()
	code, op, _ := f.report(out.Op.ID, team.RelayReportRequest{State: team.RelayCancelled, Error: team.RelayReasonCompacted})
	if code != http.StatusOK || op.State != team.RelayCancelled || op.Reason != team.RelayReasonCompacted {
		t.Fatalf("report cancelled: %d %+v", code, op)
	}
	a, _, _ := f.m.store.Get(out.RequestID)
	if a.State != team.StateCancelled || a.DecidedBy != nil {
		t.Fatalf("approval after compacted = %+v, want cancelled with no decided_by", a)
	}
	closed := 0
	for _, ev := range f.events() {
		if ev.Op == "closed" && ev.Approval.ID == out.RequestID {
			closed++
		}
	}
	if closed != 1 {
		t.Fatalf("closed events = %d, want exactly 1", closed)
	}
	// The op keeps the report's reason: afterClose's cancelled{abandoned}
	// must not overwrite compacted (ReportRelay is a no-op on the same state).
	if again, _, _ := f.m.store.RelayOpByRequest(out.RequestID); again.Reason != team.RelayReasonCompacted {
		t.Fatalf("reason after afterClose = %q, want compacted", again.Reason)
	}
	// Idempotent re-send: 200, nothing closes again.
	if code, _, _ := f.report(out.Op.ID, team.RelayReportRequest{State: team.RelayCancelled, Error: team.RelayReasonCompacted}); code != http.StatusOK {
		t.Fatalf("re-send: %d", code)
	}
	if evs := f.events(); len(evs) != 0 {
		t.Fatalf("re-send must broadcast nothing, got %+v", evs)
	}
	// failed on a claimed op (approval already closed by the approve): the op moves, no second close.
	o2 := f.begin("sid-2")
	f.decide(o2.RequestID, "approve")
	f.events()
	if code, op, _ := f.report(o2.Op.ID, team.RelayReportRequest{State: team.RelayFailed, Error: team.RelayReasonHandoffIncomplete}); code != http.StatusOK || op.State != team.RelayFailed {
		t.Fatalf("failed: %d %+v", code, op)
	}
	if evs := f.events(); len(evs) != 0 {
		t.Fatalf("a closed row is not closed again: %+v", evs)
	}
}

// Spec §9.3 (P5a's share): at boot, an awaiting_approval op whose row
// already closed takes the verdict; one whose session is gone is abandoned
// through the row (one closed broadcast); a cleared op re-runs the title
// move, which is a no-op when it already happened.
func TestStart_ReconcilesSelfRelayOps(t *testing.T) {
	f := newFixture(t)
	// (1) approved while the daemon was down: close the row directly, bypassing afterClose.
	o1 := f.begin("sid-1")
	if _, won, err := f.m.store.CloseIfOpen(o1.RequestID, Close{State: team.StateApproved, DecidedAt: f.clock.Load()}); err != nil || !won {
		t.Fatal(err)
	}
	// (2) still open, but its session is gone.
	o2 := f.begin("sid-2")
	f.origins.markDead("sid-2")
	f.events()

	if err := f.m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if op := f.op(o1.Op.ID); op.State != team.RelayClaimed {
		t.Fatalf("(1) after boot: %+v", op)
	}
	if op := f.op(o2.Op.ID); op.State != team.RelayCancelled || op.Reason != team.RelayReasonAbandoned {
		t.Fatalf("(2) after boot: %+v", op)
	}
	if a, _, _ := f.m.store.Get(o2.RequestID); a.State != team.StateAbandoned {
		t.Fatalf("(2) approval after boot: %+v", a)
	}
	if n := f.countOps("closed"); n != 1 {
		t.Fatalf("closed events at boot = %d, want 1 (the abandoned row)", n)
	}
	// (3): clear o1 now, then a second reconcile must not move the title twice.
	f.report(o1.Op.ID, team.RelayReportRequest{State: team.RelayCleared, NewSessionID: "sid-1c"})
	before := len(f.titles.moves)
	f.m.reconcileRelays()
	if len(f.titles.moves) != before {
		t.Fatalf("title moved again at reconcile: %v", f.titles.moves)
	}
}

// reboot is "the daemon restarted": the first Module is stopped and closed,
// and a SECOND Module is built over the same core (same data dir ⇒ same
// team.db) with the given title mover (the same meta.db), its routes on a
// fresh mux, Start run (boot reconciliation included).
func (f *fixture) reboot(titles TitleMover) *fixture {
	f.t.Helper()
	_ = f.m.Stop(context.Background())
	_ = f.m.Close()
	g := &fixture{t: f.t, core: f.core, origins: f.origins, switches: f.switches, usage: f.usage}
	g.clock.Store(f.clock.Load())
	g.m = New().WithTitles(titles)
	g.m.logf = func(string, ...any) {}
	g.m.now = func() int64 { return g.clock.Load() }
	g.m.newID = sequentialIDs()
	if err := g.m.Init(f.core); err != nil {
		f.t.Fatal(err)
	}
	g.mux = http.NewServeMux()
	g.m.RegisterRoutes(g.mux)
	g.sub = f.core.Events.AddTestSubscriber()
	if err := g.m.Start(context.Background()); err != nil {
		f.t.Fatal(err)
	}
	f.t.Cleanup(func() {
		f.core.Events.RemoveTestSubscriber(g.sub)
		_ = g.m.Stop(context.Background())
		_ = g.m.Close()
	})
	return g
}

// Review Focus 3 across the layers (codex round): the mod reports cleared,
// the daemon restarts, and the mod re-sends the same cleared (P5b-2 re-sends
// a failed report at the next turn.complete). Over a REAL meta.db
// (store.OpenMeta + PeerLabels, the production TitleMover) and the same
// team.db: exactly one lineage row, the title moved once — the new session
// id holds it, the old row is released (label "", kept: P5a-1a's Move) —
// and the old ref resolves to the new row through LineageReader → Build →
// Resolve. Mutation gates: drop the no-op branch of ReportRelay's cleared
// transition (apply it again) → two lineage rows → red; make Move not NULL
// the old row → two rows with the label → red.
func TestRelayReport_ClearedAcrossRestartIsIdempotentEndToEnd(t *testing.T) {
	ms, err := store.OpenMeta(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer ms.Close()
	labels := ms.PeerLabels()
	if _, err := labels.Claim("sid-1", "purdex-tester", time.UnixMilli(1000)); err != nil {
		t.Fatal(err)
	}

	f := newFixture(t)
	f.m.titles = labels // the real mover for this test, over the fake-titles default
	out := f.begin("sid-1")
	f.decide(out.RequestID, "approve")
	id := out.Op.ID
	for _, st := range []team.RelayState{team.RelayWriting, team.RelayWritten} {
		f.report(id, team.RelayReportRequest{State: st})
	}
	if code, op, ae := f.report(id, team.RelayReportRequest{State: team.RelayCleared, NewSessionID: "sid-1b"}); code != http.StatusOK || op.State != team.RelayCleared {
		t.Fatalf("cleared #1: %d %+v %+v", code, op, ae)
	}

	g := f.reboot(labels) // the daemon came back; Start ran reconcileRelays over the op still in cleared
	if code, op, ae := g.report(id, team.RelayReportRequest{State: team.RelayCleared, NewSessionID: "sid-1b"}); code != http.StatusOK || op.State != team.RelayCleared || op.NewSessionID != "sid-1b" {
		t.Fatalf("cleared #2 after restart: %d %+v %+v", code, op, ae)
	}

	// One lineage row.
	refs, err := g.m.store.PreviousRefs()
	if err != nil || len(refs) != 1 || len(refs["sid-1b"]) != 1 || refs["sid-1b"][0] != "_abc123" {
		t.Fatalf("lineage after restart = %v err=%v (want one row sid-1b ← _abc123)", refs, err)
	}
	var n int
	if err := g.m.store.db.QueryRow(`SELECT COUNT(*) FROM session_lineage`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("session_lineage rows = %d err=%v, want 1", n, err)
	}
	// The title moved once: the new id holds it, the old row is released.
	rows, err := labels.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]store.PeerLabel{}
	for _, r := range rows {
		byID[r.SessionID] = r
	}
	if len(byID) != 2 || byID["sid-1b"].Label != "purdex-tester" || byID["sid-1b"].Rev != 2 || byID["sid-1"].Label != "" {
		t.Fatalf("peer_labels after restart = %+v (want the label on sid-1b at rev 2 once, sid-1 released)", byID)
	}
	// The old ref resolves to the new row through the whole path.
	records := ipeers.Build(ipeers.BuildInput{
		HostID: "h:1", Alias: "mlab",
		Entries:      []ipeers.Entry{{PID: 4242, SessionID: "sid-1b", Name: "purdex-tester", Inbox: "/s/4242", ProcStart: "Sun Sep 13 15:22:36 2026"}},
		PreviousRefs: refs,
	})
	rec, err := ipeers.Resolve(records, "_abc123", ipeers.ResolveSnapshot{})
	if err != nil || rec.Agent == nil || rec.Agent.SessionID != "sid-1b" {
		t.Fatalf("Resolve(old ref) after restart = %+v err=%v; want the sid-1b row", rec, err)
	}
}
```

- [ ] **Step 2: Run and see them fail.** `go test ./internal/module/team/ -run 'TestRelayReport|TestStart_Reconciles' -v` → 404 on the report route (`report writing: 404 …`), `f.m.reconcileRelays undefined`.

- [ ] **Step 3: Implement.** `relay_report.go`:

```go
package teammod

import (
	"errors"
	"net/http"
	"strings"
	"time"

	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/team"
)

// handleRelayOp is GET /api/relay/ops/{id} (debug).
func (m *Module) handleRelayOp(w http.ResponseWriter, r *http.Request) {
	op, ok, err := m.store.GetRelayOp(r.PathValue("id"))
	if err != nil {
		m.logf("[team] relay op %s: %v", r.PathValue("id"), err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
		return
	}
	if !ok {
		m.writeErr(w, http.StatusNotFound, team.ErrNotFound, "no such relay op", nil)
		return
	}
	m.writeJSON(w, http.StatusOK, op)
}

// handleRelayReport is POST /api/relay/ops/{id}/report (spec §8.3): one
// transition from the mod, idempotent per (op, state). cleared needs
// new_session_id and writes the lineage (store, one tx) and moves the
// title (meta.db, right after); failed and cancelled need error (the
// reason). A state the op cannot reach is 409 bad_transition carrying the
// op as it is. A report that moves the op to a terminal state while its
// approval row is still open (the mod's `cancelled --error compacted`
// mid-wait, spec §8.7 (c)) also closes that row as cancelled through the
// usual CAS — one `closed` event, the dialogs go away, `pdx relay wait`
// exits 12; afterClose then finds the op already there and is a no-op.
func (m *Module) handleRelayReport(w http.ResponseWriter, r *http.Request) {
	if m.stopping() {
		m.writeErr(w, http.StatusServiceUnavailable, team.ErrNotReady, "daemon is stopping", nil)
		return
	}
	id := r.PathValue("id")
	var req team.RelayReportRequest
	if !m.decodeBody(w, r, &req) {
		return
	}
	switch req.State {
	case team.RelayClaimed, team.RelayWriting, team.RelayWritten, team.RelayDone:
	case team.RelayCleared:
		if strings.TrimSpace(req.NewSessionID) == "" {
			m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, "new_session_id is required for cleared", nil)
			return
		}
	case team.RelayFailed, team.RelayCancelled:
		if strings.TrimSpace(req.Error) == "" {
			m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, "error (the reason) is required for failed and cancelled", nil)
			return
		}
	default:
		m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, "unknown state", nil)
		return
	}
	rep := RelayReport{State: req.State, Reason: strings.TrimSpace(req.Error), At: m.now()}
	if req.State == team.RelayCleared {
		rep.NewSessionID = strings.TrimSpace(req.NewSessionID)
		rep.NewRef = ipeers.RefID(rep.NewSessionID)
		rep.Reason = ""
	}
	op, res, err := m.store.ReportRelay(id, rep)
	if errors.Is(err, ErrNoSuchRelayOp) {
		m.writeErr(w, http.StatusNotFound, team.ErrNotFound, "no such relay op", nil)
		return
	}
	if err != nil {
		m.logf("[team] relay report %s: %v", id, err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
		return
	}
	switch res {
	case ReportBadTransition:
		m.writeJSON(w, http.StatusConflict, team.APIError{Error: team.ErrBadTransition, Detail: "state " + string(op.State) + " does not lead to " + string(req.State), Op: &op})
		return
	case ReportApplied:
		m.logf("[team] relay op %s → %s%s", id, op.State, reasonSuffix(op))
		if op.State == team.RelayCleared {
			m.moveTitle(op)
		}
		if op.State.Terminal() && op.RequestID != "" {
			m.closeRequestOfReportedOp(op)
		}
	}
	m.writeJSON(w, http.StatusOK, op)
}

// closeRequestOfReportedOp closes the op's approval row when a report put
// the op in a terminal state while the row is still open (compacted mid-
// wait; a failed op whose row is somehow open). The same CAS as every
// close: a row already closed makes the CAS lose and nothing is broadcast.
// closeAs runs afterClose, which reports cancelled{abandoned} on an op that
// is already cancelled — ReportRelay answers ReportNoop for the same state,
// so the report's reason (compacted) stands and nothing is logged.
func (m *Module) closeRequestOfReportedOp(op team.RelayOp) {
	_, won, err := m.closeAs(op.RequestID, Close{State: team.StateCancelled, DecidedAt: m.now()})
	if err != nil && !errors.Is(err, ErrNoSuchApproval) {
		m.logf("[team] relay op %s %s: close request %s: %v", op.ID, op.State, op.RequestID, err)
		return
	}
	if won {
		m.logf("[team] relay op %s %s: request %s closed", op.ID, op.State, op.RequestID)
	}
}

// moveTitle carries the old session's title to the new one (spec §8.4).
// It runs after the lineage tx commits (meta.db is another database, so
// it cannot share the transaction) and is idempotent: a title already
// moved, or none to move, is a no-op; a failure is logged and the boot
// reconciliation retries it for every op still in cleared.
func (m *Module) moveTitle(op team.RelayOp) {
	if m.titles == nil || op.NewSessionID == "" {
		return
	}
	moved, err := m.titles.Move(op.SessionID, op.NewSessionID, time.UnixMilli(m.now()))
	if err != nil {
		m.logf("[team] relay op %s: move title %s → %s: %v", op.ID, op.SessionID, op.NewSessionID, err)
		return
	}
	if moved {
		m.logf("[team] relay op %s: title moved %s → %s", op.ID, op.SessionID, op.NewSessionID)
	}
}

// reconcileRelays runs at Start (spec §9.3, the part P5a owns: self ops).
// For every active op: an awaiting_approval op whose approval row is
// already closed takes that row's verdict (afterClose missed it); one whose
// row is still open but whose session is gone is abandoned through the
// row's CAS, so the broadcast and the op follow as in the live path; a
// cleared op re-runs the title move (idempotent). Member ops in
// requested are P6's. Nothing here waits for a hook.
func (m *Module) reconcileRelays() {
	ops, err := m.store.ListActiveRelayOps()
	if err != nil {
		m.logf("[team] boot: list relay ops: %v", err)
		return
	}
	now := m.now()
	for _, op := range ops {
		switch {
		case op.Kind == team.RelayKindSelf && op.State == team.RelayAwaitingApproval:
			a, ok, err := m.store.Get(op.RequestID)
			if err != nil {
				m.logf("[team] boot: relay op %s: approval %s: %v", op.ID, op.RequestID, err)
				continue
			}
			switch {
			case !ok:
				if _, _, err := m.store.ReportRelay(op.ID, RelayReport{State: team.RelayCancelled, Reason: team.RelayReasonAbandoned, At: now}); err != nil {
					m.logf("[team] boot: relay op %s without an approval row: %v", op.ID, err)
				}
			case a.State != team.StateOpen:
				m.afterClose(a)
			case !m.origins.LiveSession(op.SessionID):
				if _, won, err := m.closeAs(a.ID, Close{State: team.StateAbandoned, DecidedAt: now}); err != nil {
					m.logf("[team] boot: abandon approval %s of relay op %s: %v", a.ID, op.ID, err)
				} else if won {
					m.logf("[team] boot: approval %s abandoned, its session %s is gone (relay op %s)", a.ID, op.SessionID, op.ID)
				}
			}
		case op.State == team.RelayCleared:
			m.moveTitle(op)
		}
	}
}
```

`module.go`: in `RegisterRoutes` add after the `self` route

```go
	mux.HandleFunc("POST /api/relay/ops/{id}/report", m.handleRelayReport)
	mux.HandleFunc("GET /api/relay/ops/{id}", m.handleRelayOp)
```

and in `Start`, before `m.core.Events.OnSubscribe(m.sendSnapshot)`, add `m.reconcileRelays()`.

`handler.go` `handleInflight` (`:243-255`): the comment's last sentence becomes `// block. relays_active counts ops not in done/failed/cancelled (P5a).` and the final write becomes

```go
	active, err := m.store.ListActiveRelayOps()
	if err != nil {
		m.logf("[team] inflight: %v", err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
		return
	}
	m.writeJSON(w, http.StatusOK, team.InflightResponse{ApprovalsOpen: len(open), RelaysActive: len(active)})
```

Also update the `InflightResponse` comment in `wire.go:130` (`RelaysActive is a literal 0 until P6.` → `RelaysActive counts relay ops not yet done/failed/cancelled (P5a).`) — comment only.

- [ ] **Step 4: Run and see it pass.** `go test ./internal/module/team/` → `ok` (≈ 3 s).

- [ ] **Step 5: Commit.**
  ```bash
  git add internal/module/team/relay_report.go internal/module/team/relay_report_test.go internal/module/team/module.go internal/module/team/handler.go internal/team/wire.go
  git commit -m "feat(team): relay report/op routes, title move at cleared, relays_active, boot reconciliation of self ops

  Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
  ```

- [ ] **Gate for P5a-2b:** `go build ./... && go vet ./... && go test ./internal/module/team/ ./cmd/pdx/` green.

---

## PR P5a-2c — `pdx relay hello | begin | wait | self | report | op`

**Scope.** `cmd/pdx/relay.go`: the six subcommands the mod calls (spec §8.3) on the restart-aware `daemonclient`, with spec §14 exit codes — no new code: a `wait` whose `--wait` budget ran out with the request still open **exits 0 with `{"state":"open"}` on stdout** (coordinator decision; P5b-2's `waitLoop` loops on exactly that shape); `relay_open` and `bad_transition` print the op the daemon sent on stdout; every refusal prints the 409 code as the **last stderr token** (P5b-2's `stderrCode()` reads it). `pdx relay <ref>` and `claim` are P6.

### Task 5a.9: `cmd/pdx/relay.go`

**Files:**
- Create: `cmd/pdx/relay.go`
- Modify: `cmd/pdx/main.go:43` (usage lists `relay`), `:70-71` (`case "relay": runRelay(os.Args[2:])`). `cmd/pdx/exitcodes.go` and `exitcodes_test.go` are **not** touched: the still-open end of a `wait` is exit 0 with `{"state":"open"}`, not a new code.
- Test: `cmd/pdx/relay_test.go`

**Interfaces:**
- Produces: `func runRelayCmd(ctx context.Context, args []string, stdout, stderr io.Writer, clientOpts ...daemonclient.Option) int`. Grammar:
  ```
  pdx relay hello --session <sid> [--version <v>] [--agent cc] [--config <path>]         → stdout RelayHelloResponse; 0
  pdx relay begin --self --session <sid> --used <pct> --window <n>  → stdout {op, request_id} (no --model / --effort: the daemon fills both from the statusline reading); 0 | 13 on the 409s (code on stderr; relay_open also prints the open op on stdout) | 1 on 404 unknown_session
  pdx relay wait <request_id> [--wait 9m]   → each GET ?wait=25 renews the lease; 0 approved (stdout Approval, state approved) | 0 still open after --wait (stdout `{"state":"open"}` — always printed, even when no poll answered; call again) | 10 denied | 11 timeout | 12 cancelled/abandoned or ctx cancelled | 20 | 21
  pdx relay self off|on|status --session <sid>   → stdout RelaySelfResponse; 0 | 13 member_relay_is_leads
  pdx relay report <op> <state> [--new-session <sid>] [--error <e>]   → stdout RelayOp; 0 | 13 bad_transition (op on stdout) | 1 not_found
  pdx relay op <id>   → stdout RelayOp; 0
  ```
  Usage errors exit 2 before any config load. `--config` on every form. The positional of `wait`, `op`, `self`, `report` comes **first** (`flag` stops at the first non-flag).
- Consumes: `daemonclient.New/Do/Idempotent/WithAttemptTimeout/WithStderr`, `resolveDaemonHost`, `config.Load`, `sanitizeCell`, `leadDecidedBy`, the `team` wire.

- [ ] **Step 1: Write the failing tests.**

```go
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

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
		w.WriteHeader(f.beginStatus)
		_ = json.NewEncoder(w).Encode(f.beginBody)
	case strings.HasPrefix(r.URL.Path, "/api/relay/wait/"):
		f.mu.Lock()
		f.polls++
		n := f.polls
		f.mu.Unlock()
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
		w.WriteHeader(f.reportStatus)
		_ = json.NewEncoder(w).Encode(f.reportBody)
	case strings.HasPrefix(r.URL.Path, "/api/relay/ops/"):
		_ = json.NewEncoder(w).Encode(team.RelayOp{ID: strings.TrimPrefix(r.URL.Path, "/api/relay/ops/"), State: team.RelayClaimed})
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
	srv := httptest.NewServer(d)
	defer srv.Close()
	cfgPath := writeTestConfig(t, srv.URL, "admin-tok")
	var stdout, stderr bytes.Buffer
	code := runRelayCmd(ctx, append(args, "--config", cfgPath), &stdout, &stderr, leadClockOpt())
	return code, stdout.String(), stderr.String()
}

func TestRelayCmd_UsageErrorsExit2BeforeAnyRequest(t *testing.T) {
	d := &fakeRelayDaemon{}
	for _, args := range [][]string{
		{}, {"dance"}, {"hello"}, {"begin", "--session", "s"}, {"begin", "--self", "--session", "s", "--used", "150"},
		{"wait"}, {"self", "maybe", "--session", "s"}, {"self", "on"}, {"report", "op"}, {"report", "op", "flying"},
		{"report", "op", "cleared"}, {"report", "op", "failed"}, {"op"},
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
	for _, c := range []struct {
		code   string
		wantOp bool
	}{{team.ErrSelfRelayOff, false}, {team.ErrSelfRelayPaused, false}, {team.ErrMemberRelayIsLeads, false}, {team.ErrRelayOpen, true}} {
		body := team.APIError{Error: c.code, Detail: "d"}
		if c.wantOp {
			body.Op = &team.RelayOp{ID: "op-open", State: team.RelayClaimed}
		}
		d := &fakeRelayDaemon{beginStatus: http.StatusConflict, beginBody: body}
		code, stdout, stderr := driveRelay(t, context.Background(), d, "begin", "--self", "--session", "sid-1", "--used", "72")
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
	if code, _, stderr := driveRelay(t, context.Background(), nf, "begin", "--self", "--session", "sid-x", "--used", "72"); code != ExitError || !strings.Contains(stderr, team.ErrUnknownSession) {
		t.Fatalf("unknown_session: code=%d stderr=%q", code, stderr)
	}
}

// Exit codes for each terminal state (spec §14), plus "still open" (exit 0,
// stdout {"state":"open"}) and the 404 → 21 path.
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
	// Still open when --wait runs out: exit 0, `{"state":"open"}` on stdout
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
	// stdout still carries {"state":"open"} — never empty at exit 0.
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
```

`exitcodes.go` / `exitcodes_test.go` are unchanged: the still-open end of a `wait` is **exit 0 with `{"state":"open"}` on stdout** (coordinator decision), so the pinned spec §14 table stays as shipped.

- [ ] **Step 2: Run and see them fail.** `go test ./cmd/pdx/ -run 'TestRelayCmd' -v` → `undefined: runRelayCmd`.

- [ ] **Step 3: Implement.** `main.go:43`: `Commands: serve, start, stop, status, statusline-proxy, hook, setup, token, peers, msg, lead, relay, nex, path, version`; after `case "lead": runLead(os.Args[2:])` (`:70-71`) add `case "relay": runRelay(os.Args[2:])`.

`relay.go`:

```go
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/wake/purdex/cmd/pdx/daemonclient"
	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/team"
)

// relayUsage is the grammar-rejection message for `pdx relay` (exit 2).
// These are the mod's calls (lead-team-relay spec §8.3); `pdx relay <ref>`
// (the lead's member relay) and `claim` arrive with P6.
const relayUsage = "usage: pdx relay hello --session <sid> [--version <v>] [--agent cc] [--config <path>]\n" +
	"       pdx relay begin --self --session <sid> --used <pct> --window <n> [--config <path>]\n" +
	"       pdx relay wait <request_id> [--wait 9m] [--config <path>]\n" +
	"       pdx relay self off|on|status --session <sid> [--config <path>]\n" +
	"       pdx relay report <op> <state> [--new-session <sid>] [--error <e>] [--config <path>]\n" +
	"       pdx relay op <id> [--config <path>]"

const (
	// relayAttemptTimeout bounds one long-poll (team.MaxPollWaitS plus room), as lead's does.
	relayAttemptTimeout = 35 * time.Second
	// relayMaxHungPolls: consecutive polls without any answer before exit 20 (spec §9.1, P2b decision).
	relayMaxHungPolls = 3
)

// relayRefusalCodes are the 409 team-rule codes `pdx relay` can meet (spec
// §14): all exit 13. relay_open and bad_transition also print the op the
// daemon sent, on stdout, so the mod can continue from it.
var relayRefusalCodes = map[string]bool{
	team.ErrMemberRelayIsLeads: true,
	team.ErrSelfRelayOff:       true,
	team.ErrSelfRelayPaused:    true,
	team.ErrRelayOpen:          true,
	team.ErrBadTransition:      true,
}

// runRelay is the `pdx relay` switch target. SIGINT/SIGTERM cancel ctx;
// a wait that is cancelled exits 12 without touching the request (the
// daemon's lease closes it if nobody polls again).
func runRelay(args []string) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(runRelayCmd(ctx, args, os.Stdout, os.Stderr))
}

// relayFlags parses the flags common to every subcommand plus the ones the
// caller declares on fs; ok=false means the usage line was written (exit 2).
func relayFlags(fs *flag.FlagSet, args []string, stderr io.Writer) (cfgPath string, ok bool) {
	fs.SetOutput(io.Discard)
	fs.StringVar(&cfgPath, "config", "", "")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(stderr, "pdx relay: %s\n%s\n", err.Error(), relayUsage)
		return "", false
	}
	return cfgPath, true
}

// relayClient loads the config and builds the restart-aware client.
func relayClient(cfgPath string, stderr io.Writer, attempt time.Duration, clientOpts []daemonclient.Option) (*daemonclient.Client, int) {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		fmt.Fprintf(stderr, "pdx relay: %v\n", err)
		return nil, ExitError
	}
	base := fmt.Sprintf("http://%s:%d", resolveDaemonHost(cfg.Bind), cfg.Port)
	opts := append([]daemonclient.Option{daemonclient.WithStderr(stderr), daemonclient.WithAttemptTimeout(attempt)}, clientOpts...)
	return daemonclient.New(base, cfg.Token, opts...), ExitOK
}

// runRelayCmd implements `pdx relay <sub>` and returns the exit code (spec
// §14). Grammar rejections return 2 before any config load.
func runRelayCmd(ctx context.Context, args []string, stdout, stderr io.Writer, clientOpts ...daemonclient.Option) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, relayUsage)
		return ExitUsage
	}
	switch args[0] {
	case "hello":
		return runRelayHello(ctx, args[1:], stdout, stderr, clientOpts)
	case "begin":
		return runRelayBegin(ctx, args[1:], stdout, stderr, clientOpts)
	case "wait":
		return runRelayWait(ctx, args[1:], stdout, stderr, clientOpts)
	case "self":
		return runRelaySelf(ctx, args[1:], stdout, stderr, clientOpts)
	case "report":
		return runRelayReport(ctx, args[1:], stdout, stderr, clientOpts)
	case "op":
		return runRelayOp(ctx, args[1:], stdout, stderr, clientOpts)
	default:
		fmt.Fprintf(stderr, "pdx relay: unknown subcommand %q\n%s\n", args[0], relayUsage)
		return ExitUsage
	}
}

func relayUsageErr(stderr io.Writer, msg string) int {
	fmt.Fprintf(stderr, "pdx relay: %s\n%s\n", msg, relayUsage)
	return ExitUsage
}

// relayPrintJSON writes v as one JSON line on stdout.
func relayPrintJSON(stdout, stderr io.Writer, v any) int {
	out, err := json.Marshal(v)
	if err != nil {
		fmt.Fprintf(stderr, "pdx relay: %v\n", err)
		return ExitError
	}
	fmt.Fprintln(stdout, string(out))
	return ExitOK
}

// relayReportErr maps a client error to stderr text, stdout (the op on the
// two refusals that carry one) and an exit code. On every StatusError the
// line is `pdx relay: <detail> <code>` — the API code is the LAST
// whitespace-separated stderr token (P5b-2's `stderrCode()` and P8a-2's
// `ask.js` read it that way); sanitizeCell strips newlines and control
// characters from the detail, so nothing can follow the code.
func relayReportErr(err error, stdout, stderr io.Writer) int {
	var se *daemonclient.StatusError
	switch {
	case errors.Is(err, daemonclient.ErrUnavailable):
		fmt.Fprintln(stderr, "pdx relay: 等了 30 秒 daemon 仍沒有回應 daemon_unavailable")
		return ExitUnavailable
	case errors.Is(err, daemonclient.ErrUnsupported):
		fmt.Fprintln(stderr, "pdx relay: 這個 daemon 沒有 /api/relay 路由，請先更新 daemon unsupported")
		return ExitUnsupported
	case errors.As(err, &se) && relayRefusalCodes[se.API.Error]:
		fmt.Fprintf(stderr, "pdx relay: %s %s\n", sanitizeCell(se.API.Detail), se.API.Error)
		if se.API.Op != nil {
			if out, merr := json.Marshal(se.API.Op); merr == nil {
				fmt.Fprintln(stdout, string(out))
			}
		}
		return ExitRefused
	case errors.As(err, &se):
		fmt.Fprintf(stderr, "pdx relay: %s %s\n", sanitizeCell(se.API.Detail), sanitizeCell(se.API.Error))
		return ExitError
	default:
		fmt.Fprintf(stderr, "pdx relay: %v\n", err)
		return ExitError
	}
}

func runRelayHello(ctx context.Context, args []string, stdout, stderr io.Writer, clientOpts []daemonclient.Option) int {
	fs := flag.NewFlagSet("pdx relay hello", flag.ContinueOnError)
	var req team.RelayHelloRequest
	fs.StringVar(&req.SessionID, "session", "", "")
	fs.StringVar(&req.ModVersion, "version", "", "")
	fs.StringVar(&req.Agent, "agent", "cc", "")
	cfgPath, ok := relayFlags(fs, args, stderr)
	if !ok {
		return ExitUsage
	}
	if fs.NArg() != 0 || strings.TrimSpace(req.SessionID) == "" {
		return relayUsageErr(stderr, "--session 不能為空")
	}
	client, code := relayClient(cfgPath, stderr, daemonclient.DefaultAttemptTimeout, clientOpts)
	if code != ExitOK {
		return code
	}
	var res team.RelayHelloResponse
	if _, err := client.Do(ctx, http.MethodPost, "/api/relay/hello", req, &res, daemonclient.Idempotent()); err != nil {
		return relayReportErr(err, stdout, stderr)
	}
	return relayPrintJSON(stdout, stderr, res)
}

func runRelayBegin(ctx context.Context, args []string, stdout, stderr io.Writer, clientOpts []daemonclient.Option) int {
	fs := flag.NewFlagSet("pdx relay begin", flag.ContinueOnError)
	var req team.RelayBeginRequest
	used := fs.Float64("used", -1, "")
	fs.BoolVar(&req.Self, "self", false, "")
	fs.StringVar(&req.SessionID, "session", "", "")
	fs.IntVar(&req.Window, "window", 0, "")
	cfgPath, ok := relayFlags(fs, args, stderr)
	if !ok {
		return ExitUsage
	}
	switch {
	case fs.NArg() != 0:
		return relayUsageErr(stderr, fmt.Sprintf("unexpected argument %q", fs.Arg(0)))
	case !req.Self:
		return relayUsageErr(stderr, "--self is required (a member relay is `pdx relay <ref>`, P6)")
	case strings.TrimSpace(req.SessionID) == "":
		return relayUsageErr(stderr, "--session 不能為空")
	case *used < 0 || *used > 100:
		return relayUsageErr(stderr, "--used 必須在 0 到 100 之間")
	case req.Window < 0:
		return relayUsageErr(stderr, "--window 不能是負數")
	}
	req.UsedPercentage = *used
	client, code := relayClient(cfgPath, stderr, daemonclient.DefaultAttemptTimeout, clientOpts)
	if code != ExitOK {
		return code
	}
	// Replayable: a second begin for a session with an open op answers 409
	// relay_open carrying that op, so a replay after a dropped connection
	// cannot open two ops.
	var res team.RelayBeginResponse
	if _, err := client.Do(ctx, http.MethodPost, "/api/relay/begin", req, &res, daemonclient.Idempotent()); err != nil {
		return relayReportErr(err, stdout, stderr)
	}
	fmt.Fprintf(stderr, "接力申請已送出（%s），等待核准；接著執行 pdx relay wait %s\n", res.Op.ID, res.RequestID)
	return relayPrintJSON(stdout, stderr, res)
}

// runRelayWait long-polls the self_relay approval row (spec §8.7 (b)):
// each GET ?wait=25 renews the lease; the loop runs until the row closes
// or --wait (default 9 min, cap 10 min) runs out. Exit: 0 approved with the
// Approval on stdout; 0 still open when --wait ran out, with
// {"state":"open"} on stdout (always — even when no poll has answered yet —
// so the mod's waitLoop can tell "call again" from "approved" by `state`
// alone); 10 denied; 11 timeout; 12 cancelled or abandoned, or ctx
// cancelled; 20 / 21 as everywhere.
func runRelayWait(ctx context.Context, args []string, stdout, stderr io.Writer, clientOpts []daemonclient.Option) int {
	// The positional comes first (flag stops at the first non-flag), so
	// `pdx relay wait <id> --wait 9m` parses: id, then the flags.
	if len(args) == 0 || strings.TrimSpace(args[0]) == "" || strings.HasPrefix(args[0], "-") {
		return relayUsageErr(stderr, "需要一個 request_id")
	}
	id := args[0]
	fs := flag.NewFlagSet("pdx relay wait", flag.ContinueOnError)
	wait := fs.Duration("wait", time.Duration(team.DefaultWaitS)*time.Second, "")
	cfgPath, ok := relayFlags(fs, args[1:], stderr)
	if !ok {
		return ExitUsage
	}
	if fs.NArg() != 0 {
		return relayUsageErr(stderr, fmt.Sprintf("unexpected argument %q", fs.Arg(0)))
	}
	if *wait <= 0 || *wait > time.Duration(team.MaxWaitS)*time.Second {
		return relayUsageErr(stderr, fmt.Sprintf("--wait 必須大於 0 且不超過 %ds", team.MaxWaitS))
	}
	client, code := relayClient(cfgPath, stderr, relayAttemptTimeout, clientOpts)
	if code != ExitOK {
		return code
	}
	deadline, cancel := context.WithTimeout(ctx, *wait)
	defer cancel()
	hung := 0
	var ap team.Approval
	for {
		if ctx.Err() != nil {
			fmt.Fprintln(stderr, "pdx relay: 等待已中斷，申請仍然開著")
			return ExitCancelled
		}
		if deadline.Err() != nil {
			fmt.Fprintln(stderr, "pdx relay: --wait 已到，申請仍在等待核准；請再呼叫一次 pdx relay wait")
			// Always {"state":"open"} (the last polled row when there is
			// one, a bare state otherwise): the mod re-calls on this shape.
			if ap.ID == "" {
				ap = team.Approval{ID: id, Kind: team.KindSelfRelay, State: team.StateOpen}
			}
			return relayPrintJSON(stdout, stderr, ap)
		}
		var polled team.Approval
		_, err := client.Do(deadline, http.MethodGet, fmt.Sprintf("/api/relay/wait/%s?wait=%d", id, team.MaxPollWaitS), nil, &polled)
		if err != nil {
			if ctx.Err() != nil || deadline.Err() != nil {
				continue // the loop head reports which one
			}
			if errors.Is(err, daemonclient.ErrNoAnswer) || errors.Is(err, context.DeadlineExceeded) {
				hung++
				if hung >= relayMaxHungPolls {
					fmt.Fprintln(stderr, "pdx relay: daemon 沒有回應")
					return ExitUnavailable
				}
				continue
			}
			return relayReportErr(err, stdout, stderr)
		}
		hung = 0
		ap = polled
		if ap.State != team.StateOpen {
			return relayFinish(ap, stdout, stderr)
		}
	}
}

// relayFinish maps a closed self_relay Approval to output and exit code (spec §14).
func relayFinish(ap team.Approval, stdout, stderr io.Writer) int {
	switch ap.State {
	case team.StateApproved:
		return relayPrintJSON(stdout, stderr, ap)
	case team.StateDenied:
		fmt.Fprintf(stderr, "pdx relay: 接力申請已被拒絕%s\n", leadDecidedBy(ap))
		return ExitDenied
	case team.StateTimeout:
		fmt.Fprintln(stderr, "pdx relay: 接力申請逾時，視同拒絕")
		return ExitTimeout
	case team.StateCancelled:
		fmt.Fprintln(stderr, "pdx relay: 接力申請已取消")
		return ExitCancelled
	case team.StateAbandoned:
		fmt.Fprintln(stderr, "pdx relay: 接力申請已失效（lease 到期或來源 session 已結束）")
		return ExitCancelled
	default:
		fmt.Fprintf(stderr, "pdx relay: 未知狀態 %q\n", sanitizeCell(string(ap.State)))
		return ExitError
	}
}

func runRelaySelf(ctx context.Context, args []string, stdout, stderr io.Writer, clientOpts []daemonclient.Option) int {
	if len(args) == 0 {
		return relayUsageErr(stderr, "需要 off、on 或 status")
	}
	action := args[0]
	if action != "off" && action != "on" && action != "status" {
		return relayUsageErr(stderr, fmt.Sprintf("unknown action %q", action))
	}
	fs := flag.NewFlagSet("pdx relay self", flag.ContinueOnError)
	req := team.RelaySelfRequest{Action: action}
	fs.StringVar(&req.SessionID, "session", "", "")
	cfgPath, ok := relayFlags(fs, args[1:], stderr)
	if !ok {
		return ExitUsage
	}
	if fs.NArg() != 0 || strings.TrimSpace(req.SessionID) == "" {
		return relayUsageErr(stderr, "--session 不能為空")
	}
	client, code := relayClient(cfgPath, stderr, daemonclient.DefaultAttemptTimeout, clientOpts)
	if code != ExitOK {
		return code
	}
	var res team.RelaySelfResponse
	if _, err := client.Do(ctx, http.MethodPost, "/api/relay/self", req, &res, daemonclient.Idempotent()); err != nil {
		return relayReportErr(err, stdout, stderr)
	}
	return relayPrintJSON(stdout, stderr, res)
}

func runRelayReport(ctx context.Context, args []string, stdout, stderr io.Writer, clientOpts []daemonclient.Option) int {
	if len(args) < 2 {
		return relayUsageErr(stderr, "需要 <op> 與 <state>")
	}
	opID, state := args[0], team.RelayState(args[1])
	fs := flag.NewFlagSet("pdx relay report", flag.ContinueOnError)
	req := team.RelayReportRequest{State: state}
	fs.StringVar(&req.NewSessionID, "new-session", "", "")
	fs.StringVar(&req.Error, "error", "", "")
	cfgPath, ok := relayFlags(fs, args[2:], stderr)
	if !ok {
		return ExitUsage
	}
	if fs.NArg() != 0 || strings.TrimSpace(opID) == "" {
		return relayUsageErr(stderr, "需要 <op> 與 <state>")
	}
	switch state {
	case team.RelayClaimed, team.RelayWriting, team.RelayWritten, team.RelayCleared, team.RelayDone, team.RelayFailed, team.RelayCancelled:
	default:
		return relayUsageErr(stderr, fmt.Sprintf("unknown state %q", string(state)))
	}
	if state == team.RelayCleared && strings.TrimSpace(req.NewSessionID) == "" {
		return relayUsageErr(stderr, "cleared 需要 --new-session")
	}
	if (state == team.RelayFailed || state == team.RelayCancelled) && strings.TrimSpace(req.Error) == "" {
		return relayUsageErr(stderr, "failed / cancelled 需要 --error")
	}
	client, code := relayClient(cfgPath, stderr, daemonclient.DefaultAttemptTimeout, clientOpts)
	if code != ExitOK {
		return code
	}
	// Idempotent per (op, state) on the daemon (spec §8.3), so a replay is safe.
	var op team.RelayOp
	if _, err := client.Do(ctx, http.MethodPost, "/api/relay/ops/"+opID+"/report", req, &op, daemonclient.Idempotent()); err != nil {
		return relayReportErr(err, stdout, stderr)
	}
	return relayPrintJSON(stdout, stderr, op)
}

func runRelayOp(ctx context.Context, args []string, stdout, stderr io.Writer, clientOpts []daemonclient.Option) int {
	if len(args) == 0 || strings.TrimSpace(args[0]) == "" || strings.HasPrefix(args[0], "-") {
		return relayUsageErr(stderr, "需要一個 op id")
	}
	id := args[0]
	fs := flag.NewFlagSet("pdx relay op", flag.ContinueOnError)
	cfgPath, ok := relayFlags(fs, args[1:], stderr)
	if !ok {
		return ExitUsage
	}
	if fs.NArg() != 0 {
		return relayUsageErr(stderr, fmt.Sprintf("unexpected argument %q", fs.Arg(0)))
	}
	client, code := relayClient(cfgPath, stderr, daemonclient.DefaultAttemptTimeout, clientOpts)
	if code != ExitOK {
		return code
	}
	var op team.RelayOp
	if _, err := client.Do(ctx, http.MethodGet, "/api/relay/ops/"+id, nil, &op); err != nil {
		return relayReportErr(err, stdout, stderr)
	}
	return relayPrintJSON(stdout, stderr, op)
}
```

- [ ] **Step 4: Run and see it pass.** `go test ./cmd/pdx/ -run 'TestRelayCmd|TestExitCodes|TestRegisterServeModules' -v` → PASS (`TestRelayCmd_WaitExitCodes` takes ≈ 0.3 s for the still-open case); `go vet ./cmd/pdx/` clean.

- [ ] **Step 5: Commit.**
  ```bash
  git add cmd/pdx/relay.go cmd/pdx/relay_test.go cmd/pdx/main.go cmd/pdx/exitcodes.go cmd/pdx/exitcodes_test.go
  git commit -m "feat(pdx): pdx relay hello/begin/wait/self/report/op on the restart-aware client, spec §14 exit codes

  Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
  ```

- [ ] **Gate for P5a-2c:** `go build ./... && go vet ./... && go test ./cmd/pdx/` green. Manual smoke (not a gate, never against mlab's live daemon): `pdx relay hello --session x --config <scratch cfg>` against a dev daemon → `{"ok":true,"role":"none","self_relay":"on","threshold":70,"min_growth":20000}`.

---

## PR P5a-3a — the retention sweeper and `(was _xxxxxx)` in `pdx peers`

**Scope.** The hourly + boot sweep of `<data_dir>/relay/` (spec §8.3 "Retention": 3 per lineage chain, 14 days, 3 days for failed/cancelled; the row is marked `pruned`; nothing outside the relay dir is touched — a row whose path is not `<relayDir>/<op id>.md` is marked pruned without removing anything), and the peers table's `(was _xxxxxx)` suffix (spec §8.4 display).

### Task 5a.10: Retention — `retention.go` and the three store methods

**Files:**
- Create: `internal/module/team/retention.go`
- Modify: `internal/module/team/relay_store.go` (append `ListUnprunedRelayOps`, `MarkRelayPruned`, `ChainRoots`); `internal/module/team/module.go` `Start` (`m.sweepWG.Add(2)`, `go m.runRetention()`)
- Test: `internal/module/team/retention_test.go`

**Interfaces:**
- Produces: `retentionVictims(ops []team.RelayOp, chainOf func(team.RelayOp) string, now int64) []team.RelayOp` (pure), `func (m *Module) sweepRetention()`, `func (m *Module) runRetention()`; store: `ListUnprunedRelayOps() ([]team.RelayOp, error)`, `MarkRelayPruned(id string) error`, `ChainRoots() (map[string]string, error)` (every session id in `session_lineage` → the root of its chain).
- Consumes: `m.relayDir`, `m.stopCtx`, `m.sweepWG`, `m.now`.

**Rule as implemented** (`retentionVictims`): a `failed`/`cancelled` op whose `updated_at` is ≥ 3 d ago; any unpruned op whose `created_at` is ≥ 14 d ago; among `done` ops grouped by chain root (`chainOf`), all but the newest 3 by `created_at`. Active ops are never victims except by the 14 d rule.

- [ ] **Step 1: Write the failing tests.**

```go
package teammod

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/wake/purdex/internal/team"
)

const day = int64(24 * 60 * 60 * 1000)

func doneOp(id, sid string, createdAt int64) team.RelayOp {
	op := selfOp(id, sid, "_r"+id, createdAt)
	op.State, op.UpdatedAt = team.RelayDone, createdAt+60_000
	return op
}

// Spec §15 "Retention": 3 per chain, 14 days, and 3 days for failed ops.
// retentionVictims is pure; `now` is day 20.
func TestRetentionVictims_Rules(t *testing.T) {
	now := 20 * day
	chain := func(op team.RelayOp) string {
		if op.SessionID == "a1" || op.SessionID == "a2" || op.SessionID == "a3" || op.SessionID == "a4" {
			return "a"
		}
		return op.SessionID
	}
	ops := []team.RelayOp{
		// chain a: four done ops, newest first by created_at → the oldest (a1) goes.
		doneOp("a1", "a1", 16*day), doneOp("a2", "a2", 17*day), doneOp("a3", "a3", 18*day), doneOp("a4", "a4", 19*day),
		// another chain with one done op: kept.
		doneOp("b1", "b1", 18*day),
		// a done op from 15 days ago, alone in its chain: the 14 d rule takes it.
		doneOp("old", "old", 5*day),
		// failed 2 days ago: kept; failed 3 days ago: goes; cancelled 4 days ago: goes.
		{ID: "f-young", State: team.RelayFailed, SessionID: "f1", CreatedAt: 17*day + 1, UpdatedAt: 18 * day, HandoffPath: "x"},
		{ID: "f-old", State: team.RelayFailed, SessionID: "f2", CreatedAt: 16 * day, UpdatedAt: 17 * day, HandoffPath: "x"},
		{ID: "c-old", State: team.RelayCancelled, SessionID: "c1", CreatedAt: 15 * day, UpdatedAt: 16 * day, HandoffPath: "x"},
		// an active op from yesterday: never a victim; one stuck for 15 days: the 14 d rule.
		{ID: "live", State: team.RelayWriting, SessionID: "l1", CreatedAt: 19 * day, UpdatedAt: 19 * day, HandoffPath: "x"},
		{ID: "stuck", State: team.RelayClaimed, SessionID: "l2", CreatedAt: 5 * day, UpdatedAt: 5 * day, HandoffPath: "x"},
		// already pruned: ignored even though old.
		{ID: "gone", State: team.RelayDone, SessionID: "g1", CreatedAt: 1 * day, UpdatedAt: 1 * day, Pruned: true, HandoffPath: "x"},
	}
	got := retentionVictims(ops, chain, now)
	ids := map[string]bool{}
	for _, op := range got {
		ids[op.ID] = true
	}
	want := map[string]bool{"a1": true, "old": true, "f-old": true, "c-old": true, "stuck": true}
	for id := range want {
		if !ids[id] {
			t.Errorf("%s must be a victim", id)
		}
	}
	for id := range ids {
		if !want[id] {
			t.Errorf("%s must NOT be a victim", id)
		}
	}
}

// The sweep removes only <relayDir>/<op id>.md, marks the row pruned, and
// touches nothing else — not a sibling file, not a path outside the dir.
func TestSweepRetention_RemovesOnlyOwnFilesAndMarksPruned(t *testing.T) {
	f := newFixture(t)
	relayDir := filepath.Join(f.core.Cfg.DataDir, "relay")
	if err := os.MkdirAll(relayDir, 0o700); err != nil {
		t.Fatal(err)
	}
	write := func(path string) {
		t.Helper()
		if err := os.WriteFile(path, []byte("# handoff"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	outside := filepath.Join(f.core.Cfg.DataDir, "keep.md")
	write(outside)
	// Four done ops in one chain (sessions s1→s2→s3→s4→s5) at days 16–19; now is day 20,
	// so none is 14 d old and only the chain rule applies to them.
	for i := 1; i <= 4; i++ {
		id := "op" + string(rune('0'+i))
		op := doneOp(id, "s"+string(rune('0'+i)), int64(15+i)*day)
		op.HandoffPath = filepath.Join(relayDir, id+".md")
		op.NewSessionID = "s" + string(rune('0'+i+1))
		if err := f.m.store.CreateRelayOp(op); err != nil {
			t.Fatal(err)
		}
		write(op.HandoffPath)
		// lineage rows make them one chain
		if _, err := f.m.store.db.Exec(`INSERT INTO session_lineage (session_id, predecessor_session_id, predecessor_ref, op_id, at) VALUES (?, ?, ?, ?, ?)`,
			op.NewSessionID, op.SessionID, op.Ref, id, op.UpdatedAt); err != nil {
			t.Fatal(err)
		}
	}
	// A 19-day-old row whose path points outside the relay dir: marked pruned, file untouched.
	evil := doneOp("evil", "e1", 1*day)
	evil.HandoffPath = outside
	if err := f.m.store.CreateRelayOp(evil); err != nil {
		t.Fatal(err)
	}
	sibling := filepath.Join(relayDir, "notes.md")
	write(sibling)

	f.clock.Store(20 * day)
	f.m.sweepRetention()

	if _, err := os.Stat(filepath.Join(relayDir, "op1.md")); !os.IsNotExist(err) {
		t.Fatalf("op1.md (4th newest in its chain) must be removed: %v", err)
	}
	for _, id := range []string{"op2", "op3", "op4"} {
		if _, err := os.Stat(filepath.Join(relayDir, id+".md")); err != nil {
			t.Fatalf("%s.md must stay: %v", id, err)
		}
	}
	for _, p := range []string{outside, sibling} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("%s must not be touched: %v", p, err)
		}
	}
	if op := f.op("op1"); !op.Pruned || op.HandoffPath == "" {
		t.Fatalf("op1 = %+v (pruned, path kept)", op)
	}
	if op := f.op("evil"); !op.Pruned {
		t.Fatalf("evil = %+v (marked pruned without removing anything)", op)
	}
	if op := f.op("op2"); op.Pruned {
		t.Fatalf("op2 = %+v", op)
	}
	// Idempotent: a second sweep changes nothing and does not fail on the missing file.
	f.m.sweepRetention()
	if op := f.op("op2"); op.Pruned {
		t.Fatalf("second sweep pruned op2: %+v", op)
	}
}

func TestChainRoots(t *testing.T) {
	s := openTestStore(t)
	for _, r := range [][2]string{{"s2", "s1"}, {"s3", "s2"}, {"t2", "t1"}} {
		if _, err := s.db.Exec(`INSERT INTO session_lineage (session_id, predecessor_session_id, predecessor_ref, op_id, at) VALUES (?, ?, 'x', 'op', 1)`, r[0], r[1]); err != nil {
			t.Fatal(err)
		}
	}
	roots, err := s.ChainRoots()
	if err != nil {
		t.Fatal(err)
	}
	for sid, want := range map[string]string{"s1": "s1", "s2": "s1", "s3": "s1", "t1": "t1", "t2": "t1"} {
		if roots[sid] != want {
			t.Errorf("root(%s) = %q, want %q", sid, roots[sid], want)
		}
	}
}
```

- [ ] **Step 2: Run and see them fail.** `go test ./internal/module/team/ -run 'TestRetention|TestSweepRetention|TestChainRoots' -v` → `undefined: retentionVictims`, `f.m.sweepRetention undefined`, `s.ChainRoots undefined`.

- [ ] **Step 3: Implement.** Append to `relay_store.go`:

```go
// ListUnprunedRelayOps returns every op whose file has not been pruned, in
// any state, oldest first. The retention sweeper's input.
func (s *Store) ListUnprunedRelayOps() ([]team.RelayOp, error) {
	rows, err := s.db.Query(`SELECT ` + relayCols + ` FROM relay_ops WHERE pruned = 0 ORDER BY created_at, id`)
	if err != nil {
		return nil, fmt.Errorf("list unpruned relay ops: %w", err)
	}
	defer rows.Close()
	out := []team.RelayOp{}
	for rows.Next() {
		op, err := scanRelayOp(rows)
		if err != nil {
			return nil, fmt.Errorf("list unpruned relay ops: %w", err)
		}
		out = append(out, op)
	}
	return out, rows.Err()
}

// MarkRelayPruned records that op's handoff file is gone (spec §8.3
// retention: "the row keeps the path, marked pruned").
func (s *Store) MarkRelayPruned(id string) error {
	if _, err := s.db.Exec(`UPDATE relay_ops SET pruned = 1 WHERE id = ?`, id); err != nil {
		return fmt.Errorf("mark relay op %s pruned: %w", id, err)
	}
	return nil
}

// ChainRoots maps every session id that appears in session_lineage (as a
// head or a predecessor) to the root of its chain — the one session with
// no predecessor. Two ops whose sessions share a root are in one chain.
func (s *Store) ChainRoots() (map[string]string, error) {
	rows, err := s.db.Query(`SELECT session_id, predecessor_session_id FROM session_lineage`)
	if err != nil {
		return nil, fmt.Errorf("read lineage: %w", err)
	}
	defer rows.Close()
	pred := map[string]string{}
	for rows.Next() {
		var sid, p string
		if err := rows.Scan(&sid, &p); err != nil {
			return nil, fmt.Errorf("read lineage: %w", err)
		}
		pred[sid] = p
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read lineage: %w", err)
	}
	roots := make(map[string]string, len(pred)*2)
	rootOf := func(sid string) string {
		seen := map[string]bool{sid: true}
		for {
			p, ok := pred[sid]
			if !ok || seen[p] {
				return sid
			}
			seen[p] = true
			sid = p
		}
	}
	for sid, p := range pred {
		roots[sid] = rootOf(sid)
		roots[p] = rootOf(p)
	}
	return roots, nil
}
```

Create `retention.go`:

```go
package teammod

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/wake/purdex/internal/team"
)

// Retention of handoff files (spec §8.3 "Retention"): the daemon cleans,
// nobody else. Per lineage chain the newest 3 files stay; nothing older
// than 14 days; a failed or cancelled op's file 3 days. The row keeps the
// path and is marked pruned once the file is gone.
const (
	retentionInterval     = time.Hour
	retentionKeepPerChain = 3
	retentionMaxAge       = 14 * 24 * time.Hour
	retentionFailedAge    = 3 * 24 * time.Hour
)

// runRetention runs the sweep at boot and then hourly until Stop.
func (m *Module) runRetention() {
	defer m.sweepWG.Done()
	m.sweepRetention()
	ticker := time.NewTicker(retentionInterval)
	defer ticker.Stop()
	for {
		select {
		case <-m.stopCtx.Done():
			return
		case <-ticker.C:
			m.sweepRetention()
		}
	}
}

// retentionVictims decides, from every unpruned row, which ops lose their
// file at now (unix ms). Pure, so the rule is tested without a filesystem:
//   - failed / cancelled: older than 3 d (by updated_at — when it ended);
//   - any op: older than 14 d (by created_at);
//   - done: per chain (chainOf), all but the newest 3 by created_at.
//
// Active ops (awaiting_approval … cleared) are never victims except by the
// 14 d rule, which an op that long in flight has earned.
func retentionVictims(ops []team.RelayOp, chainOf func(team.RelayOp) string, now int64) []team.RelayOp {
	var out []team.RelayOp
	seen := map[string]bool{}
	take := func(op team.RelayOp) {
		if !seen[op.ID] {
			seen[op.ID] = true
			out = append(out, op)
		}
	}
	byChain := map[string][]team.RelayOp{}
	for _, op := range ops {
		if op.Pruned {
			continue
		}
		switch {
		case op.State == team.RelayFailed || op.State == team.RelayCancelled:
			if now-op.UpdatedAt >= retentionFailedAge.Milliseconds() {
				take(op)
			}
		case now-op.CreatedAt >= retentionMaxAge.Milliseconds():
			take(op)
		}
		if op.State == team.RelayDone {
			c := chainOf(op)
			byChain[c] = append(byChain[c], op)
		}
	}
	for _, chain := range byChain {
		if len(chain) <= retentionKeepPerChain {
			continue
		}
		sort.Slice(chain, func(i, j int) bool { return chain[i].CreatedAt > chain[j].CreatedAt })
		for _, op := range chain[retentionKeepPerChain:] {
			take(op)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt < out[j].CreatedAt })
	return out
}

// sweepRetention applies retentionVictims to the store and the relay dir.
// Only a path inside m.relayDir named <op id>.md is ever removed (spec §15:
// nothing outside <data_dir>/relay/ is touched); a row whose path is
// anything else is marked pruned without a removal, and logged.
func (m *Module) sweepRetention() {
	ops, err := m.store.ListUnprunedRelayOps()
	if err != nil {
		m.logf("[team] retention: %v", err)
		return
	}
	chains, err := m.store.ChainRoots()
	if err != nil {
		m.logf("[team] retention: chains: %v", err)
		return
	}
	chainOf := func(op team.RelayOp) string {
		if root, ok := chains[op.SessionID]; ok {
			return root
		}
		return op.SessionID
	}
	for _, op := range retentionVictims(ops, chainOf, m.now()) {
		if err := m.removeHandoff(op); err != nil {
			m.logf("[team] retention: op %s: %v", op.ID, err)
			continue
		}
		if err := m.store.MarkRelayPruned(op.ID); err != nil {
			m.logf("[team] retention: op %s: %v", op.ID, err)
		}
	}
}

// removeHandoff deletes op's handoff file when, and only when, it is
// <relayDir>/<op id>.md; a file already gone is fine.
func (m *Module) removeHandoff(op team.RelayOp) error {
	want := filepath.Join(m.relayDir, op.ID+".md")
	if filepath.Clean(op.HandoffPath) != want {
		m.logf("[team] retention: op %s: handoff path %q is not %q; marking pruned without removing anything", op.ID, op.HandoffPath, want)
		return nil
	}
	if err := os.Remove(want); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove %s: %w", want, err)
	}
	m.logf("[team] retention: removed %s (op %s, %s)", want, op.ID, op.State)
	return nil
}
```

`module.go` `Start`: `m.sweepWG.Add(1); go m.runSweeper()` → `m.sweepWG.Add(2); go m.runSweeper(); go m.runRetention()` (`Stop`'s `sweepWG.Wait()` joins both; `runRetention` exits on `stopCtx`).

- [ ] **Step 4: Run and see it pass.** `go test ./internal/module/team/` → `ok`.

- [ ] **Step 5: Commit.**
  ```bash
  git add internal/module/team/retention.go internal/module/team/retention_test.go internal/module/team/relay_store.go internal/module/team/module.go
  git commit -m "feat(team): handoff retention sweeper — 3 per chain, 14 d, 3 d for failed; only <data_dir>/relay/<op>.md

  Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
  ```

### Task 5a.11: `pdx peers` shows `(was _xxxxxx)`

**Files:**
- Modify: `cmd/pdx/peers.go:666-669` (`displayAddress`)
- Test: `cmd/pdx/peers_was_test.go`

**Interfaces:**
- Produces: `displayAddress(rec)` = `addressWithRef(rec.Address, rec.Ref)` + ` (was <rec.PreviousRefs[0]>)` when the row has lineage. Only the newest previous ref is printed (the row carries the whole chain and every ref in it resolves).
- Consumes: `PeerRecord.PreviousRefs` (P5a-1b).

- [ ] **Step 1: Write the failing test.**

```go
package main

import (
	"strings"
	"testing"

	"github.com/wake/purdex/internal/peers"
)

// Lead-team-relay spec §8.4 display: a row that relayed shows its newest
// previous ref after the address; a row without lineage is unchanged.
func TestDisplayAddress_WasPreviousRef(t *testing.T) {
	rec := peers.PeerRecord{Address: "mlab/purdex-b0", Ref: "_b3xxxx", PreviousRefs: []string{"_b1xxxx", "_a0xxxx"}}
	if got, want := displayAddress(rec), "mlab/purdex-b0 [b3xxxx] (was _b1xxxx)"; got != want {
		t.Fatalf("displayAddress = %q, want %q", got, want)
	}
	if got := displayAddress(peers.PeerRecord{Address: "mlab/purdex-b0", Ref: "_b3xxxx"}); strings.Contains(got, "was") {
		t.Fatalf("no lineage must print no (was …): %q", got)
	}
	if got := addressField(peers.PeerRecord{RowKind: "entry", Address: "mlab/_b3xxxx", Ref: "_b3xxxx", PreviousRefs: []string{"_b1xxxx"}}); got != "  mlab/_b3xxxx (was _b1xxxx)" {
		t.Fatalf("addressField = %q", got)
	}
}
```

- [ ] **Step 2: Run and see it fail.** `go test ./cmd/pdx/ -run TestDisplayAddress_WasPreviousRef -v` → `displayAddress = "mlab/purdex-b0 [b3xxxx]", want "… (was _b1xxxx)"`.

- [ ] **Step 3: Implement.** Replace `peers.go:666-669` with:

```go
// displayAddress renders one peer row's address through addressWithRef,
// followed by "(was _xxxxxx)" for a conversation that relayed (lead-team-
// relay spec §8.4): the NEWEST previous ref only — the one an operator is
// most likely to still hold — even though the row carries the whole chain
// and every ref in it still resolves. Not sanitized here: addressField
// does that for the table, and previous refs are RefID output anyway.
func displayAddress(rec peers.PeerRecord) string {
	addr := addressWithRef(rec.Address, rec.Ref)
	if len(rec.PreviousRefs) > 0 {
		addr += " (was " + rec.PreviousRefs[0] + ")"
	}
	return addr
}
```

- [ ] **Step 4: Run and see it pass.** `go test ./cmd/pdx/ -run 'TestDisplayAddress|TestFormatPeers' -v` → PASS (the bracket-rule tests and `TestDisplayAddressAndMsgCandidateLineAgree` use rows without lineage, unchanged).

- [ ] **Step 5: Commit.**
  ```bash
  git add cmd/pdx/peers.go cmd/pdx/peers_was_test.go
  git commit -m "feat(pdx): peers table shows (was _xxxxxx) for a relayed conversation

  Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
  ```

- [ ] **Gate for P5a-3a:** `go build ./... && go vet ./... && go test ./internal/module/team/ ./cmd/pdx/` green.

---

## PR P5a-3b — SPA: Hosts 「接力」 toggles, the `self_relay` dialog body, the relay notification

**Scope.** `host-config-api` / `useHostConfigStore` learn the `relay` section (defaults until loaded; `relaySupported` for an older daemon); a `RelaySection` host sub-page (localId `relay`, order 11) with two `ToggleSwitch`es and the line `member 的接力一律由 lead 安排`; `ApprovalDialogHost` switches its body on `kind` — `self_relay` shows host, session, address, ref, cwd, `已用 72%`, the note `核准後這個 session 會寫接力檔、清空並在原處接手（約 1 分鐘）`, the countdown, one-click 核准 / 拒絕 with no grant, and the checkbox 「這個 session 不再詢問」 which POSTs `/api/relay/self {action:"off"}` for the origin session before the decision; `approval-notify` titles a relay request `{{host}}：{{session}} 申請接力（已用 {{pct}}%）`. No browser fallback (U14).

### Task 5a.12: Host config `relay` in the API client and the store

**Files:**
- Modify: `spa/src/lib/host-config-api.ts:15` (type), `:19-25` (payload field), `:30-36` (collection map); `spa/src/stores/useHostConfigStore.ts:5-17` (import), `:22-35` (entry), `:37-47` (empty), `:54-63` (state), `:168-182` (load), `:207-210` (saver); `spa/src/stores/useHostConfigStore.test.ts:51` (the revisions assertion)
- Test: the existing `useHostConfigStore.test.ts` (one assertion extended; see Step 1)

**Interfaces:**
- Produces: `export interface RelaySwitches { self_solo: boolean; self_lead: boolean }`; `HostConfigPayload.relay?: Versioned<RelaySwitches>`; `HostConfigCollectionItems.relay: RelaySwitches` (so `putHostConfig(hostId, 'relay', items, baseRevision)` type-checks); `HostConfigEntry.relay`, `.relaySupported`, `.revisions.relay`; `DEFAULT_RELAY_SWITCHES`; `saveRelay(hostId, items)`.

- [ ] **Step 1: Extend the existing test.** In `useHostConfigStore.test.ts`, line 51 becomes:

```ts
    expect(e.revisions).toEqual({ projects: 3, commands: 0, resumeTemplates: 1, quickReplies: 0, relay: 0 })
    expect(e.relaySupported).toBe(false)
    expect(e.relay).toEqual({ self_solo: true, self_lead: true })
```

- [ ] **Step 2: Run and see it fail.** `cd spa && npx vitest run src/stores/useHostConfigStore.test.ts` → `expected { projects: 3, … } to deeply equal { …, relay: 0 }`.

- [ ] **Step 3: Implement.** `host-config-api.ts`: after line 15 add

```ts
/** Host config `relay` (lead-team-relay spec §8.7 (a)): the two self-relay switches; a member has none (U13). */
export interface RelaySwitches { self_solo: boolean; self_lead: boolean }
```

in `HostConfigPayload` after `quickReplies?` add

```ts
  /** Absent on a daemon that predates P5a. */
  relay?: Versioned<RelaySwitches>
```

and in `HostConfigCollectionItems` after `'quick-replies': QuickReply[]` add `relay: RelaySwitches`.

`useHostConfigStore.ts`: import `type RelaySwitches,` after `type QuickReply,`; in `HostConfigEntry` after `quickRepliesSupported: boolean` add

```ts
  /** Lead-team-relay spec §8.7 (a). Defaults (both on) until the daemon's copy loads. */
  relay: RelaySwitches
  /** The daemon's GET carried a `relay` field (P5a+). */
  relaySupported: boolean
```

and `revisions` becomes `{ projects: number; commands: number; resumeTemplates: number; quickReplies: number; relay: number }`; after the interface add

```ts
export const DEFAULT_RELAY_SWITCHES: RelaySwitches = Object.freeze({ self_solo: true, self_lead: true }) as RelaySwitches
```

`emptyHostConfigEntry` gains `relay: DEFAULT_RELAY_SWITCHES,`, `relaySupported: false,` and `relay: 0` in `revisions`; `HostConfigState` gains `saveRelay: (hostId: string, items: RelaySwitches) => Promise<void>`; the `load` patch gains `relay: p.relay?.items ?? DEFAULT_RELAY_SWITCHES,`, `relaySupported: p.relay !== undefined,` and `relay: p.relay?.revision ?? 0,` in `revisions`; after `saveQuickReplies:` add `saveRelay: (hostId, items) => save(hostId, 'relay', 'relay', items),`.

- [ ] **Step 4: Run and see it pass.** `npx vitest run src/stores/useHostConfigStore.test.ts src/components/hosts` → all green; `npx tsc -p tsconfig.app.json --noEmit` clean.

- [ ] **Step 5: Commit.**
  ```bash
  git add spa/src/lib/host-config-api.ts spa/src/stores/useHostConfigStore.ts spa/src/stores/useHostConfigStore.test.ts
  git commit -m "feat(spa): host config relay switches in the API client and the store

  Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
  ```

### Task 5a.13: The Hosts 「接力」 section

**Files:**
- Create: `spa/src/components/hosts/RelaySection.tsx`
- Modify: `spa/src/lib/register-modules/index.tsx:63` (import), `:390` (the row after `peers`); `spa/src/lib/register-modules.test.ts:82` (`'relay'` appended to the localId list); `spa/src/locales/zh-TW.json:1322` and `en.json:1322` (seven `hosts.relay*` keys after `hosts.peers`)
- Test: `spa/src/components/hosts/RelaySection.test.tsx`

**Interfaces:**
- Produces: `RelaySection({ hostId })`; test ids `relay-section`, `relay-self-solo`, `relay-self-lead` (`role="switch"`, `aria-checked`), `relay-member-note`, `relay-unsupported`, `relay-save-error`.
- Consumes: `useHostConfigGate`, `HostConfigNotice`, `ToggleSwitch`, `queueHostConfigSave` / `hostConfigQueueKey(hostId, 'relay')`, `saveRelay`.

**Locale keys** (zh-TW / en):

| key | zh-TW | en |
|---|---|---|
| `hosts.relay` | 接力 | Relay |
| `hosts.relay.desc` | 已用超過 70% 時，session 會先申請核准再接力；接力申請會推到所有 Purdex.app。 | Past 70% used, a session asks for approval before it relays; the request is pushed to every Purdex.app. |
| `hosts.relay.self_solo` | 一般 session 自我接力 | Self relay for plain sessions |
| `hosts.relay.self_lead` | lead 自我接力 | Self relay for leads |
| `hosts.relay.member` | member 自我接力 | Self relay for members |
| `hosts.relay.member_note` | member 的接力一律由 lead 安排 | A member's relay is always arranged by its lead |
| `hosts.relay.unsupported` | 這台主機的 daemon 太舊，不支援接力設定。請更新這台主機上的 pdx。 | This host's daemon is too old for relay settings. Update pdx on that host. |

- [ ] **Step 1: Write the failing test.**

```tsx
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { RelaySection } from './RelaySection'
import { useHostStore } from '../../stores/useHostStore'
import { emptyHostConfigEntry, useHostConfigStore, type HostConfigEntry } from '../../stores/useHostConfigStore'
import { useI18nStore } from '../../stores/useI18nStore'
import type { RelaySwitches } from '../../lib/host-config-api'

const H = 'h1'
const saveRelay = vi.fn()

function entry(relay: RelaySwitches, supported = true, status: HostConfigEntry['status'] = 'ready'): HostConfigEntry {
  const e = emptyHostConfigEntry(status)
  return { ...e, relay, relaySupported: supported, revisions: { ...e.revisions, relay: 1 } }
}

function seed(e: HostConfigEntry) {
  useHostConfigStore.setState({ byHost: { [H]: e }, load: vi.fn(async () => {}), saveRelay })
}

beforeEach(() => {
  useI18nStore.getState().setLocale('zh-TW')
  saveRelay.mockReset().mockImplementation(async (hostId: string, items: RelaySwitches) => {
    useHostConfigStore.setState((s) => {
      const cur = s.byHost[hostId]
      return { byHost: { ...s.byHost, [hostId]: { ...cur, relay: items, revisions: { ...cur.revisions, relay: cur.revisions.relay + 1 } } } }
    })
  })
  useHostStore.setState({
    hosts: { [H]: { id: H, name: 'mlab', ip: '1.2.3.4', port: 7860, order: 0 } },
    hostOrder: [H], runtime: { [H]: { status: 'connected' } },
  })
})

describe('RelaySection', () => {
  it('shows the two switches with the stored values and the member line (spec §8.7 (a))', () => {
    seed(entry({ self_solo: true, self_lead: false }))
    render(<RelaySection hostId={H} />)
    expect(screen.getByText('接力')).toBeTruthy()
    expect(screen.getByTestId('relay-self-solo').getAttribute('aria-checked')).toBe('true')
    expect(screen.getByTestId('relay-self-lead').getAttribute('aria-checked')).toBe('false')
    expect(screen.getByTestId('relay-member-note').textContent).toBe('member 的接力一律由 lead 安排')
  })

  it('a toggle PUTs the whole relay object with the one field changed', async () => {
    seed(entry({ self_solo: true, self_lead: true }))
    render(<RelaySection hostId={H} />)
    fireEvent.click(screen.getByTestId('relay-self-solo'))
    await waitFor(() => expect(saveRelay).toHaveBeenCalledWith(H, { self_solo: false, self_lead: true }))
    await waitFor(() => expect(screen.getByTestId('relay-self-solo').getAttribute('aria-checked')).toBe('false'))
    fireEvent.click(screen.getByTestId('relay-self-lead'))
    await waitFor(() => expect(saveRelay).toHaveBeenLastCalledWith(H, { self_solo: false, self_lead: false }))
  })

  it('two clicks in one tick serialize: the second saves on top of the first, not over it', async () => {
    seed(entry({ self_solo: true, self_lead: true }))
    render(<RelaySection hostId={H} />)
    fireEvent.click(screen.getByTestId('relay-self-solo'))
    fireEvent.click(screen.getByTestId('relay-self-lead'))
    await waitFor(() => expect(saveRelay).toHaveBeenCalledTimes(2))
    expect(saveRelay.mock.calls[1][1]).toEqual({ self_solo: false, self_lead: false })
  })

  it('a save failure is shown and the switch keeps the daemon copy', async () => {
    seed(entry({ self_solo: true, self_lead: true }))
    saveRelay.mockRejectedValueOnce(new Error('boom'))
    render(<RelaySection hostId={H} />)
    fireEvent.click(screen.getByTestId('relay-self-lead'))
    await waitFor(() => expect(screen.getByTestId('relay-save-error').textContent).toContain('boom'))
    expect(screen.getByTestId('relay-self-lead').getAttribute('aria-checked')).toBe('true')
  })

  it('an older daemon (no relay field) shows the unsupported note and no switches', () => {
    seed(entry({ self_solo: true, self_lead: true }, false))
    render(<RelaySection hostId={H} />)
    expect(screen.getByTestId('relay-unsupported').textContent).toContain('太舊')
    expect(screen.queryByTestId('relay-self-solo')).toBeNull()
  })

  it('offline: the notice shows and a click saves nothing', () => {
    seed(entry({ self_solo: true, self_lead: true }))
    useHostStore.setState((s) => ({ runtime: { ...s.runtime, [H]: { status: 'disconnected' } } }))
    render(<RelaySection hostId={H} />)
    expect(screen.getByTestId('host-config-notice').dataset.notice).toBe('host_config.offline')
    fireEvent.click(screen.getByTestId('relay-self-solo'))
    expect(saveRelay).not.toHaveBeenCalled()
  })
})
```

And `register-modules.test.ts:82`: append `'relay',` after `'peers',`.

- [ ] **Step 2: Run and see it fail.** `npx vitest run src/components/hosts/RelaySection.test.tsx src/lib/register-modules.test.ts` → `Failed to resolve import "./RelaySection"`; `expected [ …(11) ] to deeply equal [ …(12) ]`.

- [ ] **Step 3: Implement.** `RelaySection.tsx`:

```tsx
// spa/src/components/hosts/RelaySection.tsx — Hosts › 接力 (lead-team-relay spec §8.7 (a)): the two per-host
// self-relay switches the daemon reads when a session asks to relay, and the one line that says a member has
// none. Same gate and queued CAS saves as the other host config sections; a toggle is one PUT of the whole
// `relay` object, so two quick clicks serialize through the queue instead of racing on the revision.
import { useCallback, useState } from 'react'
import { HostConfigConflictError, type RelaySwitches } from '../../lib/host-config-api'
import { hostConfigQueueKey, queueHostConfigSave } from '../../lib/host-config-queue'
import { useHostConfigStore } from '../../stores/useHostConfigStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { ToggleSwitch } from '../settings/ToggleSwitch'
import { HostConfigNotice, useHostConfigGate } from './HostConfigNotice'

export function RelaySection({ hostId }: { hostId: string }) {
  const t = useI18nStore((s) => s.t)
  const { entry, editable, notice } = useHostConfigGate(hostId)
  const [saveError, setSaveError] = useState<string | null>(null)
  const [pending, setPending] = useState(false)

  const known = entry.status === 'ready' || entry.status === 'unsupported'
  const unsupported = known && !entry.relaySupported

  const set = useCallback((field: keyof RelaySwitches, value: boolean) => {
    setPending(true)
    setSaveError(null)
    // The value saved is a function of the store when the task RUNS, so a second toggle queued behind a first
    // does not overwrite it with a stale copy.
    void queueHostConfigSave(hostConfigQueueKey(hostId, 'relay'), async () => {
      try {
        const current = useHostConfigStore.getState().byHost[hostId]?.relay
        if (!current) return
        await useHostConfigStore.getState().saveRelay(hostId, { ...current, [field]: value })
      } catch (err) {
        setSaveError(err instanceof HostConfigConflictError
          ? t('host_config.conflict')
          : t('host_config.save_failed', { reason: err instanceof Error ? err.message : String(err) }))
      } finally {
        setPending(false)
      }
    })
  }, [hostId, t])

  // Not locked while a save is in flight: the queue serializes a second toggle behind the first (and the task reads
  // the store when it runs), so a fast second click is kept, not dropped.
  const locked = !editable

  return (
    <div className="max-w-3xl" data-testid="relay-section" aria-busy={pending || undefined}>
      <h2 className="text-lg font-semibold mb-4">{t('hosts.relay')}</h2>
      <HostConfigNotice notice={notice} />
      {unsupported ? (
        <p data-testid="relay-unsupported" className="text-xs text-text-muted">{t('hosts.relay.unsupported')}</p>
      ) : (
        <>
          <p className="mb-4 text-xs text-text-muted">{t('hosts.relay.desc')}</p>
          <div className="border border-border-subtle rounded-lg divide-y divide-border-subtle">
            <div className="flex items-center justify-between gap-4 px-3 py-2">
              <span className="text-sm text-text-primary">{t('hosts.relay.self_solo')}</span>
              <span className={locked ? 'opacity-50 pointer-events-none' : ''}>
                <ToggleSwitch testId="relay-self-solo" label={t('hosts.relay.self_solo')} checked={entry.relay.self_solo}
                  onChange={(v) => { if (!locked) set('self_solo', v) }} />
              </span>
            </div>
            <div className="flex items-center justify-between gap-4 px-3 py-2">
              <span className="text-sm text-text-primary">{t('hosts.relay.self_lead')}</span>
              <span className={locked ? 'opacity-50 pointer-events-none' : ''}>
                <ToggleSwitch testId="relay-self-lead" label={t('hosts.relay.self_lead')} checked={entry.relay.self_lead}
                  onChange={(v) => { if (!locked) set('self_lead', v) }} />
              </span>
            </div>
            <div className="flex items-center justify-between gap-4 px-3 py-2">
              <span className="text-sm text-text-primary">{t('hosts.relay.member')}</span>
              <span data-testid="relay-member-note" className="text-xs text-text-muted">{t('hosts.relay.member_note')}</span>
            </div>
          </div>
          {saveError && <p data-testid="relay-save-error" className="mt-3 text-xs text-status-warning whitespace-pre-wrap">{saveError}</p>}
        </>
      )}
    </div>
  )
}
```

`register-modules/index.tsx`: after line 63 add `import { RelaySection } from '../../components/hosts/RelaySection'`; after the `peers` row (`:390`) add `{ localId: 'relay',     labelKey: 'hosts.relay',     order: 11, component: RelaySection },`. Locales: insert the seven keys of the table right after `"hosts.peers"` in both files (same order in both; `locale-completeness.test.ts` enforces identical key sets).

- [ ] **Step 4: Run and see it pass.** `npx vitest run src/components/hosts/RelaySection.test.tsx src/lib/register-modules.test.ts src/locales` → green (6 + 59 + the locale suite).

- [ ] **Step 5: Commit.**
  ```bash
  git add spa/src/components/hosts/RelaySection.tsx spa/src/components/hosts/RelaySection.test.tsx spa/src/lib/register-modules/index.tsx spa/src/lib/register-modules.test.ts spa/src/locales/en.json spa/src/locales/zh-TW.json
  git commit -m "feat(spa): Hosts 接力 section — self_solo / self_lead toggles, member 的接力一律由 lead 安排

  Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
  ```

### Task 5a.14: The `self_relay` dialog body, the pause, the notification title

**Files:**
- Modify: `spa/src/lib/team/types.ts:71-75` (before `Grant`: `SelfRelayPayload`), `:175` (before `leadPayloadOf`: `selfRelayPayloadOf`); `spa/src/lib/team/approval-api.ts:95` (before `count`: `RelaySelfResponse`, `setSelfRelayPause`); `spa/src/lib/team/approval-notify.ts:12, 39-50`; `spa/src/components/ApprovalDialogHost.tsx` (whole file); `spa/src/locales/zh-TW.json` / `en.json` (nine `approval.*` keys after `approval.restart.pending`); `spa/src/locales/locale-completeness.test.ts:120` (a second pinned block)
- Test: `spa/src/components/ApprovalDialogHost.selfRelay.test.tsx`

**Interfaces:**
- Produces: `interface SelfRelayPayload { op_id; used_percentage; window; model_id?; effort? }`, `selfRelayPayloadOf(a: Approval): SelfRelayPayload` (defensive: non-finite → 0, non-string → ''); `setSelfRelayPause(hostId, sessionId, 'off'|'on'|'status'): Promise<RelaySelfResponse>` (`POST /api/relay/self`, via `send`, so an older daemon is `unsupported`); the dialog's `data-kind` attribute; test ids `approval-ref`, `approval-usage`, `approval-self-relay-note`, `approval-no-more-asking`.
- Consumes: `submitDecision`, `useUndoToast.show`, `approvalSessionLabel`, `formatOriginAddress`, `formatCountdown`.

**Decisions (binding for the mod and the coordinator to see):**
1. 「這個 session 不再詢問」 is a **checkbox applied with the click**, either button: when checked, the dialog POSTs `/api/relay/self {session_id: origin.session_id, action: "off"}` **before** the decision; a failed pause toasts `無法暫停這個 session 的接力詢問（<code>），決定照常送出` and the decision still goes out. While disconnected the decision is queued (P3b) and the pause is **not** queued: there is no daemon to tell, and the mod asks again only at +10 points (spec §8.7 (c)).
2. The `self_relay` body has no grant: 核准 is enabled as soon as the dialog opens (U13a), and `submitDecision` is called with `grant` undefined.
3. The notification body for a relay request is the spec's note (`approval.dialog.self_relay_note`); the title carries the rounded percentage.

**Locale keys** (zh-TW / en), after `approval.restart.pending`:

| key | zh-TW | en |
|---|---|---|
| `approval.dialog.title_self_relay` | {{host}}：{{session}} 申請接力 | {{host}}: {{session}} requests a relay |
| `approval.dialog.ref` | Ref | Ref |
| `approval.dialog.usage` | 用量 | Usage |
| `approval.dialog.usage_value` | 已用 {{pct}}% | {{pct}}% used |
| `approval.dialog.self_relay_note` | 核准後這個 session 會寫接力檔、清空並在原處接手（約 1 分鐘） | Once approved, this session writes its relay file, clears, and takes over in place (about 1 minute) |
| `approval.dialog.no_more_asking` | 這個 session 不再詢問 | Stop asking for this session |
| `approval.dialog.pause_failed` | 無法暫停這個 session 的接力詢問（{{code}}），決定照常送出 | Could not pause relay requests for this session ({{code}}); the decision is sent anyway |
| `approval.notify.title_self_relay` | {{host}}：{{session}} 申請接力（已用 {{pct}}%） | {{host}}: {{session}} requests a relay ({{pct}}% used) |

- [ ] **Step 1: Write the failing tests.** `ApprovalDialogHost.selfRelay.test.tsx`:

```tsx
// spa/src/components/ApprovalDialogHost.selfRelay.test.tsx — the `self_relay` body (lead-team spec §8.7 (b)): host,
// session (title / address / ref / cwd), `已用 72%`, the note, one-click 核准 / 拒絕 with no grant, and
// 「這個 session 不再詢問」, which POSTs the pause before the decision.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, act, waitFor } from '@testing-library/react'
import { ApprovalDialogHost } from './ApprovalDialogHost'
import { useApprovalStore } from '../stores/useApprovalStore'
import { useHostStore } from '../stores/useHostStore'
import { useI18nStore } from '../stores/useI18nStore'
import { useUndoToast } from '../stores/useUndoToast'
import { ApprovalApiError, decideApproval, setSelfRelayPause } from '../lib/team/approval-api'
import { __resetClientDescriptorForTests } from '../lib/team/client-label'
import type { Approval } from '../lib/team/types'

vi.mock('../lib/team/approval-api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../lib/team/approval-api')>()),
  decideApproval: vi.fn(),
  setSelfRelayPause: vi.fn(),
}))
const mockedDecide = vi.mocked(decideApproval)
const mockedPause = vi.mocked(setSelfRelayPause)

const H = 'h1'
const relay = (over: Partial<Approval> = {}): Approval => ({
  id: 'req-9', kind: 'self_relay', host_id: 'd1',
  origin: { session_id: 'S9', ref: '_b1xxxx', name: 'purdex-b0', pid: 4242, proc_start: 'p', cwd: '/w/purdex', tmux: 'purdex:@1.%2', title: 'purdex-tester', address: 'mlab/purdex-b0' },
  payload: { op_id: 'op-1', used_percentage: 72.4, window: 1_000_000, model_id: 'claude-opus-5-5', effort: 'high' },
  state: 'open', created_at: 1_000, deadline_at: 1_000 + 600_000, lease_until: 31_000,
  ...over,
})

const open = (a: Approval) => act(() => { useApprovalStore.getState().applyOpened(H, a) })

beforeEach(() => {
  useI18nStore.getState().setLocale('zh-TW')
  useApprovalStore.getState().reset()
  useHostStore.setState({
    hosts: { [H]: { id: H, name: 'mlab', ip: '1.2.3.4', port: 7860, order: 0 } },
    hostOrder: [H], runtime: { [H]: { status: 'connected' } }, activeHostId: H,
  })
  useUndoToast.setState({ toast: null, notice: null })
  mockedDecide.mockReset()
  mockedPause.mockReset().mockResolvedValue({ self_relay: 'paused', host_switch: true, member: false })
  __resetClientDescriptorForTests()
  Object.defineProperty(window, 'electronAPI', { value: undefined, writable: true, configurable: true })
})
afterEach(() => { useHostStore.getState().reset() })

describe('ApprovalDialogHost — self_relay', () => {
  it('shows the spec §8.7 body: host, session, address, ref, cwd, 已用 72% and the note; no grant fields', () => {
    render(<ApprovalDialogHost />)
    open(relay())
    expect(screen.getByTestId('approval-dialog').dataset.kind).toBe('self_relay')
    expect(screen.getByText('mlab：purdex-tester 申請接力')).toBeTruthy()
    expect(screen.getByTestId('approval-host').textContent).toBe('mlab')
    expect(screen.getByTestId('approval-session').textContent).toBe('purdex-tester')
    expect(screen.getByTestId('approval-address').textContent).toBe('mlab/purdex-b0')
    expect(screen.getByTestId('approval-ref').textContent).toBe('_b1xxxx')
    expect(screen.getByTestId('approval-cwd').textContent).toBe('/w/purdex')
    expect(screen.getByTestId('approval-usage').textContent).toBe('已用 72%')
    expect(screen.getByTestId('approval-self-relay-note').textContent).toBe('核准後這個 session 會寫接力檔、清空並在原處接手（約 1 分鐘）')
    expect(screen.getByText('這個 session 不再詢問')).toBeTruthy()
    expect(screen.queryByTestId('approval-max-members')).toBeNull()
    expect(screen.queryByTestId('approval-roots')).toBeNull()
    expect(screen.queryByTestId('approval-reason')).toBeNull()
  })

  it('核准 is one click with no grant (U13a); the dialog closes on the 200', async () => {
    mockedDecide.mockResolvedValueOnce(relay({ state: 'approved', decided_by: { kind: 'app', label: 'Purdex.app' } }))
    render(<ApprovalDialogHost />)
    open(relay())
    fireEvent.click(screen.getByTestId('approval-approve'))
    await waitFor(() => expect(mockedDecide).toHaveBeenCalledTimes(1))
    expect(mockedDecide.mock.calls[0][2]).toEqual({ decision: 'approve', client: { kind: 'app', label: 'Purdex.app' } })
    expect(mockedPause).not.toHaveBeenCalled()
    await waitFor(() => expect(screen.queryByTestId('approval-dialog')).toBeNull())
  })

  it('「這個 session 不再詢問」 POSTs the pause for the origin session before the decision', async () => {
    mockedDecide.mockResolvedValueOnce(relay({ state: 'denied', decided_by: { kind: 'app', label: 'Purdex.app' } }))
    render(<ApprovalDialogHost />)
    open(relay())
    fireEvent.click(screen.getByTestId('approval-no-more-asking'))
    fireEvent.click(screen.getByTestId('approval-deny'))
    await waitFor(() => expect(mockedDecide).toHaveBeenCalledTimes(1))
    expect(mockedPause).toHaveBeenCalledWith(H, 'S9', 'off')
    expect(mockedPause.mock.invocationCallOrder[0]).toBeLessThan(mockedDecide.mock.invocationCallOrder[0])
  })

  it('a pause that fails toasts and the decision still goes out', async () => {
    mockedPause.mockRejectedValueOnce(new ApprovalApiError(0, 'network', 'Failed to fetch'))
    mockedDecide.mockResolvedValueOnce(relay({ state: 'denied', decided_by: { kind: 'app', label: 'Purdex.app' } }))
    render(<ApprovalDialogHost />)
    open(relay())
    fireEvent.click(screen.getByTestId('approval-no-more-asking'))
    fireEvent.click(screen.getByTestId('approval-deny'))
    await waitFor(() => expect(mockedDecide).toHaveBeenCalledTimes(1))
    expect(useUndoToast.getState().toast?.message).toContain('無法暫停')
  })

  it('a lead request still renders the lead body (the kind switch is per request)', () => {
    render(<ApprovalDialogHost />)
    open({ ...relay(), id: 'req-lead', kind: 'lead', payload: { reason: 'r', max_members: 3, roots: ['/w'] } })
    expect(screen.getByTestId('approval-dialog').dataset.kind).toBe('lead')
    expect(screen.getByTestId('approval-max-members')).toBeTruthy()
    expect(screen.queryByTestId('approval-usage')).toBeNull()
  })
})
```

`locale-completeness.test.ts`, after the `carries the spec §6.3 / §9.5 strings in zh-TW` block (`:120`):

```ts
    it('carries the spec §8.7 self-relay strings in zh-TW', () => {
      const zh = zhTW as Record<string, string>
      expect(zh['approval.dialog.usage_value']).toBe('已用 {{pct}}%')
      expect(zh['approval.dialog.self_relay_note']).toBe('核准後這個 session 會寫接力檔、清空並在原處接手（約 1 分鐘）')
      expect(zh['approval.dialog.no_more_asking']).toBe('這個 session 不再詢問')
      expect(zh['hosts.relay']).toBe('接力')
      expect(zh['hosts.relay.member_note']).toBe('member 的接力一律由 lead 安排')
    })
```

- [ ] **Step 2: Run and see them fail.** `npx vitest run src/components/ApprovalDialogHost.selfRelay.test.tsx src/locales/locale-completeness.test.ts` → `No "setSelfRelayPause" export is defined on the mock`; the `data-kind` and `approval-usage` assertions fail; the pinned strings are `undefined`.

- [ ] **Step 3: Implement.** `types.ts`: before `/** What the user approved … */ export interface Grant {` (`:71`) add

```ts
/** `Approval.payload` for kind `self_relay` (spec §8.7): the usage the mod reported when it asked. */
export interface SelfRelayPayload {
  op_id: string
  used_percentage: number
  window: number
  model_id?: string
  effort?: string
}
```

and before `leadPayloadOf`'s doc comment (`:175`) add

```ts
/** The self-relay payload, defensively: a missing or non-finite percentage reads as 0, strings as ''. */
export function selfRelayPayloadOf(a: Approval): SelfRelayPayload {
  const p = isRecord(a.payload) ? a.payload : {}
  const pct = typeof p.used_percentage === 'number' && Number.isFinite(p.used_percentage) ? p.used_percentage : 0
  const window = typeof p.window === 'number' && Number.isFinite(p.window) ? Math.trunc(p.window) : 0
  return {
    op_id: typeof p.op_id === 'string' ? p.op_id : '',
    used_percentage: pct,
    window,
    ...(typeof p.model_id === 'string' && p.model_id !== '' ? { model_id: p.model_id } : {}),
    ...(typeof p.effort === 'string' && p.effort !== '' ? { effort: p.effort } : {}),
  }
}
```

`approval-api.ts`: before `const count = …` (`:95`) add

```ts
/** `POST /api/relay/self` (spec §8.7 (a)): the dialog's 「這個 session 不再詢問」 sets the per-session pause. */
export interface RelaySelfResponse { self_relay: 'on' | 'off' | 'paused'; host_switch: boolean; member: boolean }

export function setSelfRelayPause(hostId: string, sessionId: string, action: 'off' | 'on' | 'status'): Promise<RelaySelfResponse> {
  return send<RelaySelfResponse>(hostId, '/api/relay/self', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ session_id: sessionId, action }),
  })
}
```

`approval-notify.ts`: the import at `:12` becomes `import { leadPayloadOf, selfRelayPayloadOf, type Approval } from './types'`; lines 41-44 become

```ts
  const t = useI18nStore.getState().t
  const host = hostLabel(hostId, hostLookOf(hostId))
  const session = approvalSessionLabel(approval.origin)
  // The kind decides the words (spec §8.7): a relay request names the usage, a lead request its reason.
  const selfRelay = approval.kind === 'self_relay'
  const pct = selfRelay ? Math.round(selfRelayPayloadOf(approval).used_percentage) : 0
  void window.electronAPI.showNotification({
    title: selfRelay ? t('approval.notify.title_self_relay', { host, session, pct }) : t('approval.notify.title', { host, session }),
    body: selfRelay ? t('approval.dialog.self_relay_note') : leadPayloadOf(approval).reason,
```

(the rest of the call — `sessionCode`, `eventName`, `broadcastTs`, `action` — unchanged).

`ApprovalDialogHost.tsx` — the whole file:

```tsx
// spa/src/components/ApprovalDialogHost.tsx — the one approval dialog (lead-team spec §6.3, §8.7), mounted once with the
// app-level overlays, next to HandoffDialogHost. It renders the oldest open request across hosts from
// `useApprovalStore`, which the `approval.request` WS branch and the daemon's snapshot feed (P3b); several requests
// queue, one dialog at a time. The body switches on `kind`: `lead` (reason, editable grant) and `self_relay` (the
// session, its usage, what approving does, and 「這個 session 不再詢問」, which pauses that session's asks — P5a).
//
// It cannot be dismissed: no Escape, no backdrop click. A request ends by a decision — 核准 / 拒絕, one click each on any
// Purdex.app (U5b, U13a) — or by the daemon closing it (decided elsewhere, timeout, cancel), which removes it from the
// store and unmounts this. While the host is not connected (spec §9.4) the buttons dim under `daemon 重啟中…`; a click
// then is queued in the store and sent when the reconnect snapshot re-adds the request (P3b). A send that fails on the
// network while the host is still connected is not queued (nothing would resend it): it toasts and the buttons come back.
//
// Focus: the panel takes focus on open and Tab stays inside, as ConfirmDialog does, so a stray keystroke never
// reaches the pane behind; Escape is swallowed so a dialog beneath does not dismiss. The i18n strings are the spec's.
import { useEffect, useRef, useState } from 'react'
import { ArrowsClockwise } from '@phosphor-icons/react'
import { useI18nStore } from '../stores/useI18nStore'
import { useHostStore } from '../stores/useHostStore'
import { useUndoToast } from '../stores/useUndoToast'
import { approvalKey, selectCurrent, selectOpenCount, useApprovalStore, type ApprovalEntry, type Decision } from '../stores/useApprovalStore'
import { hostLabel, useHostLook } from '../lib/host-look'
import { leadPayloadOf, selfRelayPayloadOf, MAX_MAX_MEMBERS, type Grant } from '../lib/team/types'
import { approvalSessionLabel, formatCountdown, formatOriginAddress } from '../lib/team/approval-format'
import { ApprovalApiError, setSelfRelayPause } from '../lib/team/approval-api'
import { submitDecision } from '../lib/team/approval-decide'

const FOCUSABLE_SELECTOR = 'input, button, textarea, [tabindex]:not([tabindex="-1"])'

function tabStops(panel: HTMLElement): HTMLElement[] {
  return Array.from(panel.querySelectorAll<HTMLElement>(FOCUSABLE_SELECTOR))
    .filter((el) => !(el as HTMLButtonElement).disabled && el.tabIndex >= 0)
}

function parseRoots(text: string): string[] {
  return text.split('\n').map((line) => line.trim()).filter((line) => line !== '')
}

const fieldClass = 'rounded-md border border-border-default bg-surface-input px-2 py-1 text-xs text-text-primary disabled:opacity-50'
// `aria-disabled` dims like RestartDaemonButton's counting state: the button still takes the click (it queues).
const buttonBase = 'px-3 py-1 rounded-md text-xs cursor-pointer disabled:opacity-50 disabled:cursor-default aria-disabled:opacity-50 flex items-center gap-1.5'

export function ApprovalDialogHost() {
  const current = useApprovalStore(selectCurrent)
  if (!current) return null
  // Keyed by the request: the next one is a fresh dialog (the payload's defaults, nothing in flight).
  return <OpenApprovalDialog key={approvalKey(current.hostId, current.approval.id)} entry={current} />
}

function OpenApprovalDialog({ entry }: { entry: ApprovalEntry }) {
  const { hostId, approval } = entry
  const t = useI18nStore((s) => s.t)
  const hostName = hostLabel(hostId, useHostLook(hostId))
  const connected = useHostStore((s) => s.runtime[hostId]?.status === 'connected')
  const queued = useApprovalStore((s) => s.queued[approvalKey(hostId, approval.id)])
  const openCount = useApprovalStore(selectOpenCount)
  const isSelfRelay = approval.kind === 'self_relay'
  const payload = leadPayloadOf(approval)
  const relay = selfRelayPayloadOf(approval)
  const [maxMembers, setMaxMembers] = useState(String(payload.max_members))
  const [rootsText, setRootsText] = useState(payload.roots.join('\n'))
  // 「這個 session 不再詢問」 (spec §8.7 (a)): applied with the decision, whichever it is.
  const [noMoreAsking, setNoMoreAsking] = useState(false)
  const [busy, setBusy] = useState(false)
  const [now, setNow] = useState(() => Date.now())
  // Ref, not state: two clicks in one event burst both see `busy === false` before React commits the first setBusy.
  const inFlight = useRef(false)
  const panelRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(id)
  }, [])

  useEffect(() => { panelRef.current?.focus() }, [])

  // Escape is swallowed, not handled: nothing dismisses this dialog, and nothing beneath it may be dismissed either
  // (ConfirmDialog and FloatingPanel both listen for Escape on `document`; a handoff confirm under this modal would
  // otherwise cancel). Capture phase on `window`, not `document`: capture listeners on one target run in registration
  // order, and the dialog beneath registered first — `window` capture runs before every `document` listener regardless.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== 'Escape') return
      e.preventDefault()
      e.stopImmediatePropagation()
    }
    window.addEventListener('keydown', onKey, { capture: true })
    return () => window.removeEventListener('keydown', onKey, { capture: true })
  }, [])

  // Tab stays inside the panel (ConfirmDialog's rule).
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== 'Tab' || e.ctrlKey || e.metaKey || e.altKey) return
      const panel = panelRef.current
      if (!panel) return
      e.preventDefault()
      const stops = tabStops(panel)
      if (stops.length === 0) {
        panel.focus()
        return
      }
      const at = stops.indexOf(document.activeElement as HTMLElement)
      const next = at === -1
        ? (e.shiftKey ? stops.length - 1 : 0)
        : (at + (e.shiftKey ? stops.length - 1 : 1)) % stops.length
      stops[next].focus()
    }
    document.addEventListener('keydown', onKey, { capture: true })
    return () => document.removeEventListener('keydown', onKey, { capture: true })
  }, [])

  const members = maxMembers.trim() === '' ? NaN : Number(maxMembers)
  const membersOk = Number.isInteger(members) && members >= 1 && members <= MAX_MAX_MEMBERS
  const roots = parseRoots(rootsText)
  const rootsOk = roots.length > 0
  // A self relay carries no grant (U13a: one click); only the lead kind validates its fields.
  const grantOk = isSelfRelay || (membersOk && rootsOk)
  const locked = busy || queued !== undefined
  const session = approvalSessionLabel(approval.origin)

  const decide = async (decision: Decision) => {
    if (inFlight.current || locked) return
    if (decision === 'approve' && !grantOk) return
    const grant: Grant | undefined = !isSelfRelay && decision === 'approve' ? { max_members: members, roots } : undefined
    if (!connected) {
      // Spec §9.4: kept locally, sent on reconnect (the snapshot re-adds the request, or shows it gone). The pause is
      // not queued: there is no daemon to tell, and the mod asks again only at +10 points anyway (spec §8.7 (c)).
      useApprovalStore.getState().queueDecision(hostId, approval, decision, grant)
      return
    }
    inFlight.current = true
    setBusy(true)
    if (isSelfRelay && noMoreAsking) {
      // Best effort, before the decision: a pause that fails must not swallow the click.
      try {
        await setSelfRelayPause(hostId, approval.origin.session_id, 'off')
      } catch (e: unknown) {
        const code = e instanceof ApprovalApiError ? e.code : 'network'
        useUndoToast.getState().show(t('approval.dialog.pause_failed', { code }))
      }
    }
    const outcome = await submitDecision(hostId, approval, decision, grant)
    // 'closed' and 'decided_elsewhere' unmount this dialog through the store; the other two keep it.
    if (outcome === 'failed' || outcome === 'queued') {
      inFlight.current = false
      setBusy(false)
    }
  }

  const titleId = 'approval-dialog-title'
  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/50"
      role="dialog"
      aria-modal="true"
      aria-labelledby={titleId}
      data-testid="approval-dialog"
      data-kind={approval.kind}
      // The backdrop also covers the Electron title bar, a window drag region that would otherwise swallow clicks there.
      style={{ WebkitAppRegion: 'no-drag' } as React.CSSProperties}
    >
      <div
        ref={panelRef}
        tabIndex={-1}
        data-testid="approval-panel"
        className="w-[480px] rounded-lg border border-border-default bg-surface-primary shadow-lg outline-none"
      >
        <div className="border-b border-border-subtle px-4 py-3">
          <h3 id={titleId} className="text-sm font-medium text-text-primary">
            {t(isSelfRelay ? 'approval.dialog.title_self_relay' : 'approval.dialog.title_lead', { host: hostName, session })}
          </h3>
          <dl className="mt-2 grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 text-xs">
            <dt className="text-text-muted">{t('approval.dialog.host')}</dt>
            <dd data-testid="approval-host" className="text-text-primary">{hostName}</dd>
            <dt className="text-text-muted">{t('approval.dialog.session')}</dt>
            <dd data-testid="approval-session" className="text-text-primary">{session}</dd>
            <dt className="text-text-muted">{t('approval.dialog.address')}</dt>
            <dd data-testid="approval-address" className="font-mono text-text-primary">{formatOriginAddress(hostName, approval.origin)}</dd>
            {isSelfRelay && (
              <>
                <dt className="text-text-muted">{t('approval.dialog.ref')}</dt>
                <dd data-testid="approval-ref" className="font-mono text-text-primary">{approval.origin.ref}</dd>
              </>
            )}
            <dt className="text-text-muted">{t('approval.dialog.cwd')}</dt>
            <dd data-testid="approval-cwd" className="font-mono break-all text-text-primary">{approval.origin.cwd}</dd>
            {isSelfRelay ? (
              <>
                <dt className="text-text-muted">{t('approval.dialog.usage')}</dt>
                <dd data-testid="approval-usage" className="text-text-primary">{t('approval.dialog.usage_value', { pct: Math.round(relay.used_percentage) })}</dd>
              </>
            ) : (
              <>
                <dt className="text-text-muted">{t('approval.dialog.tmux')}</dt>
                <dd data-testid="approval-tmux" className="font-mono text-text-primary">{approval.origin.tmux !== '' ? approval.origin.tmux : '—'}</dd>
                <dt className="text-text-muted">{t('approval.dialog.reason')}</dt>
                <dd data-testid="approval-reason" className="whitespace-pre-wrap text-text-primary">{payload.reason}</dd>
              </>
            )}
            <dt className="text-text-muted">{t('approval.dialog.deadline')}</dt>
            <dd data-testid="approval-countdown" className="font-mono text-text-primary">{formatCountdown(approval.deadline_at - now)}</dd>
          </dl>
          {isSelfRelay ? (
            <>
              <p data-testid="approval-self-relay-note" className="mt-3 text-xs text-text-secondary">{t('approval.dialog.self_relay_note')}</p>
              <label className="mt-2 flex items-center gap-2 text-xs text-text-secondary">
                <input
                  type="checkbox"
                  checked={noMoreAsking}
                  disabled={locked}
                  onChange={(e) => setNoMoreAsking(e.target.checked)}
                  data-testid="approval-no-more-asking"
                />
                {t('approval.dialog.no_more_asking')}
              </label>
            </>
          ) : (
            <>
              <label className="mt-3 flex items-center gap-2 text-xs text-text-secondary">
                {t('approval.dialog.max_members')}
                <input
                  type="number"
                  min={1}
                  max={MAX_MAX_MEMBERS}
                  step={1}
                  value={maxMembers}
                  disabled={locked}
                  onChange={(e) => setMaxMembers(e.target.value)}
                  data-testid="approval-max-members"
                  className={`w-16 ${fieldClass}`}
                />
              </label>
              {!membersOk && (
                <p data-testid="approval-max-members-error" className="mt-1 text-xs text-status-warning">{t('approval.dialog.max_members_range', { max: MAX_MAX_MEMBERS })}</p>
              )}
              <label className="mt-2 block text-xs text-text-secondary">
                {t('approval.dialog.roots')}
                <textarea
                  rows={3}
                  value={rootsText}
                  disabled={locked}
                  onChange={(e) => setRootsText(e.target.value)}
                  data-testid="approval-roots"
                  className={`mt-1 block w-full font-mono ${fieldClass}`}
                />
              </label>
              {!rootsOk && (
                <p data-testid="approval-roots-error" className="mt-1 text-xs text-status-warning">{t('approval.dialog.roots_required')}</p>
              )}
            </>
          )}
          {!connected && (
            <p data-testid="approval-disconnected" className="mt-2 text-xs text-status-warning">{t('approval.dialog.daemon_restarting')}</p>
          )}
          {queued && (
            <p data-testid="approval-queued" className="mt-1 text-xs text-text-muted">
              {t('approval.dialog.queued', { decision: t(queued.decision === 'approve' ? 'approval.dialog.approve' : 'approval.dialog.deny') })}
            </p>
          )}
          {openCount > 1 && (
            <p data-testid="approval-more" className="mt-2 text-xs text-text-muted">{t('approval.dialog.more_pending', { count: openCount - 1 })}</p>
          )}
        </div>
        <div className="flex justify-end gap-2 px-4 py-3">
          <button
            type="button"
            data-testid="approval-deny"
            disabled={locked}
            aria-disabled={!connected || undefined}
            onClick={() => void decide('deny')}
            className={`${buttonBase} text-text-secondary hover:bg-surface-hover`}
          >
            {t('approval.dialog.deny')}
          </button>
          <button
            type="button"
            data-testid="approval-approve"
            disabled={locked || !grantOk}
            aria-disabled={!connected || undefined}
            onClick={() => void decide('approve')}
            className={`${buttonBase} bg-accent text-white`}
          >
            {busy && <ArrowsClockwise size={12} aria-hidden="true" className="animate-spin" />}
            {t('approval.dialog.approve')}
          </button>
        </div>
      </div>
    </div>
  )
}
```

Locales: insert the eight `approval.*` keys of the table after `"approval.restart.pending"` in both files (it is the last key: the previous line gains a trailing comma).

- [ ] **Step 4: Run and see it pass.** `npx vitest run src/components/ApprovalDialogHost.selfRelay.test.tsx src/components/ApprovalDialogHost.test.tsx src/lib/team src/hooks/useNotificationDispatcher.approval.test.ts src/locales` → green (the existing 30 lead-dialog tests unchanged); `npx tsc -p tsconfig.app.json --noEmit`; `pnpm run lint`; `pnpm run build`.

- [ ] **Step 5: Commit.**
  ```bash
  git add spa/src/components/ApprovalDialogHost.tsx spa/src/components/ApprovalDialogHost.selfRelay.test.tsx spa/src/lib/team/types.ts spa/src/lib/team/approval-api.ts spa/src/lib/team/approval-notify.ts spa/src/locales/en.json spa/src/locales/zh-TW.json spa/src/locales/locale-completeness.test.ts
  git commit -m "feat(spa): self_relay approval body — usage, note, 這個 session 不再詢問 pauses the session; relay notification title

  Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
  ```

- [ ] **Gate for P5a-3b:** `cd spa && npx vitest run && pnpm run lint && npx tsc -p tsconfig.app.json --noEmit && pnpm run build` green.

---

### Deviations from the preamble

1. **"written at state=cleared in one tx with the title move (peer_labels)" cannot be literal.** `peer_labels` is in meta.db, `session_lineage` in team.db. The op's state change and the lineage row are one team.db transaction (`ReportRelay`); the title move is `PeerLabelStore.Move`, its own meta.db transaction right after, idempotent (a no-op once the old row has no label), and re-run by `reconcileRelays` for every op still in `cleared`. The team/member moves of §8.4 are **P4** (the team tables); P4 adds its UPDATEs inside `ReportRelay`'s `cleared` branch.
2. **`previous_refs` lives on `PeerRecord`, not `AgentInfo`.** `Resolve` decides on `PeerRecord` fields and `Ref` is there; a remote host's rows carry it over the wire unchanged (`normalizeRemoteRows` copies the record). `BuildInput.PreviousRefs map[string][]string` is the input; the peers module fills it per request through `team.LineageReaderKey` (team depends on peers, so the dependency is inverted through the registry; the interface and key live in the leaf `internal/team`).
3. **`pdx relay wait` adds no exit code for "`--wait` ran out and the request is still open": it exits 0 with `{"state":"open"}` on stdout** (coordinator decision; spec §14 stays at 0/10/11/12/20/21). A 9-minute wait on a 10-minute deadline needs a "call again" answer the mod can tell from approved, and the mod tells them apart by `state` (`approved` vs `open`) — P5b-2's `waitLoop` loops only on `state:"open"` and treats any other exit-0 body that is not `state:"approved"` as not approved.
4. **`relay_open` and `bad_transition` carry the op** (`APIError.Op`), and the CLI prints it on stdout while exiting 13 with the code on stderr, so a mod retry after a dropped connection can continue from the op it opened. `begin` and `report` are sent with `daemonclient.Idempotent()` for that reason (begin's replay is answered by `relay_open`).
5. **The combined address form also reads the lineage** (`<name> [<oldref>]` with the name checked), not only the bare `_ref` the preamble names: an operator pastes what the table printed, and the table may print the newest `(was …)` beside the current name.
6. **`ResolveOriginBySession` is added to the `OriginResolver` interface** (peers side in P5a-1b, team side in P5a-2a); the relay routes are called with a CC session id, not an inbox. The P2a fake in the team tests gains the method.
7. **`handleCreate` keeps refusing kind `self_relay`** (`400 unsupported_kind`, detail names `/api/relay/begin`) rather than accepting it: a self relay needs its op row and the usage fields, and `begin` is the one door (Task 5a.7 decision 1).
8. **`relayRole` returns `"none"`** until P4 fills it from team rows; `member_relay_is_leads` is therefore unreachable before P4 — the branch and its test shape exist, P4 changes one function body.
9. **`afterClose` is not transactional with the row's CAS** (two writes); P5a-2b's reconciliation re-derives the op from a closed row and the tests pin that path. `reconcileRelays` only looks at **self** ops; `requested` (member) ops are P6's.
10. **The dialog's 「這個 session 不再詢問」 is a checkbox applied with the click** (either decision), not a third button; a failed pause toasts and never blocks the decision; it is not queued while disconnected. P5b's mod is unaffected (the pause is read by `begin`).
11. **`(was …)` prints only the newest previous ref**; the row carries the whole chain and every ref resolves.
12. **`hello` is in-memory only** (`m.modSeen`, cap 512); P6 reads it for `relay_unsupported`, P8a-1a reads it for mod presence (one owner, one map).
13. **`pdx relay` positional-first grammar**: `wait <id> --wait 9m`, `op <id>`, `self off --session`, `report <op> <state> --new-session` (Go's `flag` stops at the first non-flag).
14. **The restart-confirm line `N 個接力進行中`** (spec §9.5) stays with P6; the daemon fills `relays_active` here.
15. **`TitleMover` is injected with `teammod.New().WithTitles(meta.PeerLabels())`**; with a nil meta store (tests) titles stay on the old session id and the move is skipped (not an error).

### Size estimate

Measured in the scratch build (`wc -l` for new files, `diff | grep -c '^[<>]'` for edits):

| PR | Files | Lines |
|---|---|---|
| **P5a-0** | `wire_relay.go` 154, `wire_relay_test.go` ≈ 45, `wire.go` +1 | **≈ 200** (2 new, 1 edit) |
| **P5a-1a** | `relay_store.go` 293 (without the 75 retention lines), `relay_store_test.go` 204, `store.go` +4, `peer_label_move.go` 61, `peer_label_move_test.go` 74 | **≈ 636** (4 new, 1 edit) |
| **P5a-1b** | `record.go` +33, `address.go` +31, `address_lineage_test.go` 104, `team/lineage_path_test.go` ≈ 60 (the 12-hop full-path test), `peers/module.go` +48, `lineage_test.go` 79, `send.go` +5, `send_test.go` +3, `origin_resolver.go` +32, `origin_resolver_session_test.go` 37; `hostconfig/relay.go` 72, `relay_test.go` 44, `handler.go` +6, `module.go` +8, `handler_test.go` +3 | **≈ 565** (6 new, 9 edits) |
| **P5a-2a** | team `relay_handler.go` ≈ 300, `relay_handler_test.go` ≈ 245 (incl. `TestStart_MakesTheRelayDir`), `module.go` +50 (incl. the `Start` `MkdirAll`), `handler.go` +5, `handler_test.go` +89; `cmd/pdx/main.go` +12, `team_register_test.go` +3 | **≈ 704** (2 new, 5 edits) |
| **P5a-2b** | `relay_report.go` ≈ 165 (incl. `closeRequestOfReportedOp`, the `not_ready` guard), `relay_report_test.go` ≈ 285 (incl. `TestRelayReport_CancelledClosesTheOpenApprovalOnce` and the cross-layer restart test `TestRelayReport_ClearedAcrossRestartIsIdempotentEndToEnd` with its `reboot` helper), `module.go` +4, `handler.go` +8, `wire.go` +1 | **≈ 463** (2 new, 3 edits) |
| **P5a-2c** | `relay.go` ≈ 405, `relay_test.go` ≈ 250, `main.go` +3 (no `exitcodes.go` change: still-open is exit 0 + `{"state":"open"}`) | **≈ 658** (2 new, 1 edit) |
| **P5a-3a** | `retention.go` 134, `retention_test.go` 154, `relay_store.go` +75, `module.go` +2, `peers.go` +13, `peers_was_test.go` 23 | **≈ 401** (3 new, 3 edits) |
| **P5a-3b** | `RelaySection.tsx` 80, `RelaySection.test.tsx` 88, `host-config-api.ts` +5, `useHostConfigStore.ts` +18, its test +4, `register-modules/index.tsx` +2, its test +2, `en.json` +17, `zh-TW.json` +17, `locale-completeness.test.ts` +9, `types.ts` +23, `approval-api.ts` +11, `approval-notify.ts` +11, `ApprovalDialogHost.tsx` 146 changed lines (file rewritten), `ApprovalDialogHost.selfRelay.test.tsx` 108 | **≈ 541** (3 new, 12 edits) |
| **Total** | 24 new files, 37 edits | **≈ 3 900 lines** |

**Eight PRs, every one ≤ 800 lines and ≤ 20 files** (codex round: the line bound is applied strictly). The two that were a hair over — P5a-1a at ≈ 836 and P5a-2a at ≈ 837 — were split at the seams the first draft named: `wire_relay.go` + its test + the `wire.go` line (≈ 200) are **P5a-0** of their own (contract first, as P2a-1 did; Task 5a.1), and `hostconfig/relay.go` + its test + the three hostconfig edits (≈ 133; Task 5a.6) moved from P5a-2a into **P5a-1b**, a `hostconfig` leaf nobody reads until P5a-2a's Task 5a.7. Final list: **P5a-0 → P5a-1a → P5a-1b → P5a-2a → P5a-2b → P5a-2c → P5a-3a → P5a-3b.** (The earlier "merge P5a-1b + P5a-3a into six" option is withdrawn: with Task 5a.6 and the 12-hop test in P5a-1b it would be ≈ 950.)

### Open questions for the coordinator

1. **"Still open" shape (deviation 3).** Decided: exit 0 with `{"state":"open"}` on stdout (see Coordinator decisions, P5a); the section above is written to that.
2. **The 「不再詢問」 affordance (deviation 10): checkbox-with-click vs a third button that pauses without deciding.** The spec says "offers … which sets the pause" and nothing about the request itself; a button that only pauses would leave the dialog open with nothing to do about it. Recommendation: the checkbox.
3. **`relay_open` as 409 with the op (deviation 4) vs 200.** For a replayed `begin` the 409 is informative, not a refusal; P5b decides whether to treat exit 13 + op on stdout as "continue". Recommendation: keep 13 (spec §14 lists the code) and let the mod branch on the stdout op.
4. **PR count** (size estimate): **decided by the codex round — eight** (P5a-0 for the wire, Task 5a.6 into P5a-1b); no PR is over 800. The "six with a P5a-1b + P5a-3a merge" option is withdrawn (it would be ≈ 950 now).

### Mutation gates

Each PR's gate is the test that goes red when the named line is removed; spec §15's mutation deliverable is the third P5a-1b item.

- **P5a-0:** change any wire constant or JSON tag → `wire_relay_test.go` red (the pinned strings).
- **P5a-1a:** drop `AND state = ?` from `ReportRelay`'s UPDATE → `TestRelayStore_ConcurrentReportsOneWins` red (two reports apply); drop the `session_lineage` INSERT → `TestRelayStore_ClearedWritesLineageAndChainIsUncapped` red; drop `!relayTransitions[cur.State][r.State]` → `TestRelayStore_ReportTransitionsAndIdempotency` red (`awaiting → writing` applies); make `Move` not NULL the old row → `TestPeerLabels_MoveCarriesLabelOnce` red (idempotency and the released row).
- **P5a-1b:** **drop the `previous_refs` tier from `resolveRefHead` → `TestResolve_PreviousRefsTier` red: the old ref is `ErrNotFound` (= `peer_not_found` at the HTTP edge)** — spec §15's named mutation; swap the two tiers (lineage before live) → the "live ref must win" assertion red; drop `hasLiveEntry` from the lineage predicate → the dead-holder assertion red; drop the `PreviousRefs` copy in `Build` → `TestBuild_AttachesPreviousRefsBySessionID` red; return `nil` from `previousRefs()` → `TestLocalEnvelope_AttachesPreviousRefsFromLineageReader` red; revert the hint → `TestSend_PeerNotFoundTeachesTheV4AddressForms` red; **cap `previous_refs` at 10 anywhere on the path** (the store's `Lineage` query, `Build`'s copy, or `resolveRefHead`'s scan) → `TestLineagePath_TwelveHopsOldestRefResolvesToTheLiveRow` red (the oldest of 12 refs is `ErrNotFound`); hostconfig (Task 5a.6): drop `RelaySwitchesKey` from `Init` → `TestRelaySwitches_DefaultsPutAndReader` red at the registry lookup; `normalizeRelay` ignoring a non-boolean → the same test red.
- **P5a-2a:** drop any of the `member` / `off` / `paused` branches in `handleRelayBegin` → `TestRelayBegin_Refusals` red; drop `m.afterClose(after)` from `closeWith` → `TestRelayApprovalCloseMovesTheOp` red (op stays `awaiting_approval` after approve); drop `&& a.Kind == team.KindLead` in `handleDecide` → approving a `self_relay` row is 500 (payload decode) → same test red; drop the `OpenRelayOpBySession` check → the `relay_open` assertion red; drop the `MkdirAll` from `Start` → `TestStart_MakesTheRelayDir` red (`relay dir after Start: err=… no such file`). (The hostconfig gates moved to P5a-1b with Task 5a.6; `drop RelaySwitchesKey from hostconfig Init → every team fixture test fails at Init` still shows here once P5a-2a's fixture reads the key.)
- **P5a-2b:** drop the `new_session_id` requirement → `TestRelayReport_ForwardPathLineageAndTitle` red (400 expected); drop `m.moveTitle(op)` → its `title moves` assertion red; drop `closeRequestOfReportedOp` from the `ReportApplied` branch → `TestRelayReport_CancelledClosesTheOpenApprovalOnce` red (approval stays `open`, 0 closed events); close it with `CloseIfOpen` directly instead of `closeAs` → the same test red (no `closed` broadcast); return `RelaysActive: 0` → its inflight assertion red; drop `reconcileRelays` from `Start` → `TestStart_ReconcilesSelfRelayOps` red; trust a body-supplied `new_ref` instead of `ipeers.RefID` → the `NewRef` assertion red; **drop the idempotency of the `cleared` transition in `ReportRelay` (apply a second `cleared` as a new transition instead of `ReportNoop`) → `TestRelayReport_ClearedAcrossRestartIsIdempotentEndToEnd` red (`session_lineage rows = 2`)**; make `Move` not NULL the old row → the same test red (`peer_labels … sid-1 released`).
- **P5a-2c:** return a non-zero code, or print nothing on stdout, when `--wait` runs out with the request still open → `TestRelayCmd_WaitExitCodes` red (both the "after two open polls" and the "before the first answer" cases); map `relay_open` to `ExitError` or stop printing the op → `TestRelayCmd_BeginPrintsOpAndRequestOrRefuses` red; print the 409 code before the detail → the "last stderr token == code" assertion in the same test red; stop sending `self` in `begin` → the `sent.Self` assertion red; parse `wait`'s flags before its positional → `TestRelayCmd_WaitExitCodes` red (usage error).
- **P5a-3a:** remove the `filepath.Clean(op.HandoffPath) != want` guard and remove `op.HandoffPath` → `TestSweepRetention_RemovesOnlyOwnFilesAndMarksPruned` red (`keep.md` deleted); drop the per-chain rule → `op1.md must be removed` red; drop the 3 d rule → `TestRetentionVictims_Rules` red (`f-old must be a victim`); drop `MarkRelayPruned` → `op1 … pruned` red; drop the `(was …)` suffix → `TestDisplayAddress_WasPreviousRef` red.
- **P5a-3b:** drop the `kind` switch in the dialog → the `self_relay` body test red (`approval-usage` missing); drop `setSelfRelayPause` before `submitDecision` → the `invocationCallOrder` assertion red; swallow the pause error without a toast → `a pause that fails toasts` red; drop `'relay'` from the section list → `register-modules.test.ts` red; drop a locale key → `locale-completeness.test.ts` red (identical key sets, pinned strings); lock the toggles on `pending` → `two clicks in one tick serialize` red.

---

## PR P5b — plugin packaging and the mod's self relay (spec §5 "Shipping", §8.1–§8.3 steps 4–8, §8.7, §10, §15 "Mod"; U2, U13, U13a, U16, U18 (a))

> Written 2026-10-07 by the P5b writer against the shared preamble (`plan2-preamble.md`) and origin/main alpha.527 (`746759d2`). Every `file:line` was read on that commit; every Go block compiled and passed in a scratch module (`scratchpad/plan2-build/p5b/gomod/`, `go vet` clean, `gofmt -l` empty); every mod block passed under `claude plugin test` (Claude Code **2.1.292** on mlab, the types are 2.1.291) and `claude plugin validate --strict`. Facts measured for this section are numbered **MP1–MP9** below.

**Scope.** Three PRs, in order:

| PR | Content | Needs merged first |
|---|---|---|
| **P5b-1** | The plugin tree embedded in `pdx` (`cmd/pdx/plugin/purdex/`), extraction to `<data_dir>/cc-plugin/purdex/` with a `VERSION` stamp and `pdx.json`, the `CLAUDE_CODE_PLUGIN_DIRS` merge/remove in `~/.claude/settings.json` `env`, wired into the CC `HookInstaller` so `pdx setup --agent cc` and `POST /api/hooks/cc/setup` install and remove it; a `register.js` that only says `hello` — at every interactive `session.start` **and again after each `/clear`** (`classic.SessionStart{source:'clear'}` → `pdx relay hello --session <new sid>`; moved here from P5b-2 by the codex round so P8a-1d / P8a-2 need only P5b-1); the skill as a placeholder; Go tests and the `claude plugin validate --strict` gate | nothing of P5a (the `hello` call fails harmlessly until P5a-2 ships the subcommand); P2c per the preamble's order |
| **P5b-2** | The mod's relay core in `register.js`: `begin` (used ≥ 70 % and growth ≥ 20K), the `pdx relay wait` loop from a timer, the write prompt (8 sections), own-turn recognition by nonce, the file check with two fix rounds, `/clear` from a timer, `cleared` → `hello` → seed `↪ 接手自 <old ref>` → `done`, `tool.check` allow for the exact handoff path, failed reports re-sent, headless and daemon-unreachable do nothing | **P5a-2** (the routes and `pdx relay hello|begin|wait|report|self`), P5b-1 |
| **P5b-3** | The prompt hold (`prompt.submit` awaits the shared wait loop; status `接力等待核准中`; one toast), release with NOTE in `context` on approval and unchanged on denial / timeout / daemon gone, the +10-point re-ask, the `session.compact{trigger:auto}` rule, `/relay off|on|status`, the §10 skill text, the acceptance recipe at a test threshold | P5b-2 |

**Architecture of the mod** (one plain-JS module, `hooks/register.js`, state in one object `s`):

```
idle ──(turn.complete: used ≥ threshold, growth ≥ minGrowth, +10 since last ask)──▶ awaiting
awaiting: `pdx relay begin --self` opened a request; a $.clock.after timer runs the one `pdx relay wait` loop;
          every prompt.submit awaits that loop's promise (P5b-3)
awaiting ──approved──▶ approved: write prompt submitted (nonce `[pdx-relay op=<id>]`), report writing
approved ──(turn.complete whose turnId is the write turn's; file ok)──▶ clearing: report written, timer → $.command.run('clear')
clearing ──(classic.SessionStart source=clear)──▶ seeding: report cleared --new-session, hello, seed prompt
seeding ──(turn.complete of the seed turn)──▶ idle: report done, floor = tokens now
awaiting ──denied / timeout / cancelled / unavailable──▶ idle (ask again at +10 points)
```

Everything that starts a turn or runs a command goes out from a `$.clock.after` timer, never inside the hook a turn waits on (`$.command.run` "rejects … inside a hook the turn is waiting on", d.ts:3000-3003; prototype F3).

### Measured for this section (2026-10-07, mlab, Claude Code 2.1.292 CLI, 2.1.291 types)

- **MP1 `claude plugin test` runs only `*.test.ts` and `*.test.tsx`** (`claude plugin test --help`: "Runs every *.test.ts and *.test.tsx under dir, each file in a child of this binary"). The preamble's `hooks/*.test.js` cannot run; the tests below are `hooks/relay.test.ts`. `register.js` stays plain JS (the probes' shape).
- **MP2 The kit's shapes** (from the kit's own error messages, then green): an op the mod calls (`$.process.run`, `$.session.id`, `$.session.usage`, `$.fs.read`, `$.ui.toast`, `$.ui.status`, `$.ui.log`, `$.command.register`) is answered by the test's `on('<op>', …)` returning `{ value }` or `{ deny }`; the bottom of an event the test raises must be answered too (`on('session.start')` → `{ cwd }`, `on('turn.start')` → `{ turnId }`, `on('turn.complete')` → `{ text }`, `on('prompt.submit')` → `{ text, context }`, `on('command.run')` → `{ text }`, `on('session.compact')` → `{ messages }` with **at least one message**, `on('classic.SessionStart')` → `{}`); `$.turn.complete` takes `answer`, not `text`; every `on(...)` of the test must be registered **before the test's first call on `$`**; `mock.clock(on)` makes `$.clock.after` drive from `clock.advance(ms)`; `mock.env(on, {…})` answers `$.env.get`.
- **MP3 A plugin's own `prompt.submit` hook does not see its own `$.prompt.submit`** (d.ts:2871-2876 "every hook but the calling one"; measured: a toast logged from the hook never fired for the plugin's own submit). So the hold never holds the mod's own write or seed prompt, and **own-turn recognition cannot use `prompt.submit`**: the mod recognises its turn at `turn.start` by a nonce in `e.text` (`[pdx-relay op=<id>]`), records `e.turnId`, and acts at the `turn.complete` carrying that id (`TurnStartInput.text`, d.ts:12982-12993; `turn.complete` carries `turnId`, d.ts:12906).
- **MP4 `$.process.run` from a `$.clock.after` timer and `$.command.run` from a timer both work in the kit**; `$.env.get("PDX_RELAY_THRESHOLD")` is a string-literal read `claude plugin validate` lists ("env reads: PDX_RELAY_THRESHOLD"), so the acceptance threshold comes from the environment as in the prototype, no debug command needed.
- **MP5 `tool.check` with a matcher `{ tool: 'Write' }` answering `{ decision: 'allow' }` wins over a bottom `ask`** for exactly the matching `file_path`; other paths and tools keep the bottom verdict.
- **MP6 `claude plugin validate --strict` fails on a manifest without `author`** ("No author information provided"); with `author` it passes and prints the hooks, the `$` calls and the env reads of `register.js`.
- **MP7 `session.start`'s input carries `isInteractive`** (d.ts:11345-11350: "true under the REPL, false for a `-p` run or the SDK"), which is the headless test; `$.plugin.root` is the folder holding `plugin.json` (d.ts:2269-2278).
- **MP8 `$.session.model()` exists; there is no effort accessor** (d.ts:2660-2745 members: `cwd, root, model, turns, id, repo, usage, version, …`). **Decided (codex round): the mod passes neither `--model` nor `--effort`** — the daemon's `begin` (Task 5a.7) fills both into the `self_relay` payload from the statusline reading it already keeps per session (`internal/module/agent/context_usage.go:16-24, 65-68`: `ModelID`, `Effort`, via `agent.ContextUsageReader`), which is the only place Claude Code reports them (M21). `pdx relay begin` has no such flags (Task 5a.9); deviation 8 records it.
- **MP9 No `go:embed` exists in the repo yet** (`grep -rn go:embed` over `*.go` is empty). `embed.FS` with the pattern `all:purdex` brings the dot-folder `.claude-plugin/` in (a plain pattern skips names starting with `.`); verified by `TestFiles_HasTheLayoutClaudeLoads`.

---

## PR P5b-1 — plugin packaging: embed, extract, settings env, `pdx setup`, `hello`

**Scope.** `cmd/pdx/plugin/` becomes a Go package that embeds `purdex/` (manifest, `hooks/hooks.json`, `hooks/register.js`, `hooks/relay.test.ts`, `skills/pdx-team/SKILL.md`). `internal/agent/cc` gains `plugin.go`: extraction with a `VERSION` stamp (re-extracted when the stamp differs or the build is a dev build), a `pdx.json` beside it naming the pdx binary and the data dir for the mod, and the `CLAUDE_CODE_PLUGIN_DIRS` merge in `~/.claude/settings.json` `env` (appended to an existing list, idempotent, entries recognised by the `<data_dir>/cc-plugin/` prefix, removed on uninstall). The CC `Provider`'s `InstallHooks` / `RemoveHooks` call it, so `pdx setup --agent cc` (`cmd/pdx/setup.go:60-70, 101-118`) and `POST /api/hooks/{agent}/setup` (`internal/module/agent/handler.go:843-892`) need no new branch. `cmd/pdx/main.go` hands the embedded tree to the installer at start. The mod only says `hello`.

### Task 5b.1: The embedded plugin tree — `cmd/pdx/plugin/`

**Files:**
- Create: `cmd/pdx/plugin/embed.go`, `cmd/pdx/plugin/embed_test.go`
- Create: `cmd/pdx/plugin/purdex/.claude-plugin/plugin.json`, `cmd/pdx/plugin/purdex/hooks/hooks.json`, `cmd/pdx/plugin/purdex/hooks/register.js`, `cmd/pdx/plugin/purdex/hooks/relay.test.ts`, `cmd/pdx/plugin/purdex/skills/pdx-team/SKILL.md`
- Test: `cmd/pdx/plugin/embed_test.go`; mod: `claude plugin test cmd/pdx/plugin/purdex`; gate: `claude plugin validate --strict cmd/pdx/plugin/purdex`

**Interfaces:**
- Produces:
  ```go
  package plugin // github.com/wake/purdex/cmd/pdx/plugin
  // Files is the plugin folder as an fs.FS rooted at the folder that holds .claude-plugin/plugin.json.
  func Files() fs.FS
  ```
- Produces (mod ↔ daemon): `pdx relay hello --session <sid> --version 1 --agent cc` at every interactive `session.start` (spec §8.3). `1` is the mod protocol version the daemon compares (`relay_unsupported`, spec §8.2).
- Consumes: `pdx.json` `{"pdx": "<abs path>", "data_dir": "<abs path>"}` beside `VERSION` in the extracted folder (written by Task 5b.2); absent (as under `claude plugin test`) the mod runs `pdx` from `PATH`.

- [ ] **Step 1: Write the failing Go test.**

```go
package plugin

import (
	"encoding/json"
	"io/fs"
	"testing"
)

func TestFiles_HasTheLayoutClaudeLoads(t *testing.T) {
	f := Files()
	for _, rel := range []string{".claude-plugin/plugin.json", "hooks/hooks.json", "hooks/register.js", "skills/pdx-team/SKILL.md"} {
		if _, err := fs.Stat(f, rel); err != nil {
			t.Errorf("%s: %v", rel, err)
		}
	}
	b, err := fs.ReadFile(f, ".claude-plugin/plugin.json")
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil || m["name"] != "purdex" {
		t.Fatalf("plugin.json = %s (%v)", b, err)
	}
	var h struct{ Modules []string }
	hb, _ := fs.ReadFile(f, "hooks/hooks.json")
	if err := json.Unmarshal(hb, &h); err != nil || len(h.Modules) != 1 || h.Modules[0] != "./register.js" {
		t.Fatalf("hooks.json = %s", hb)
	}
}
```

- [ ] **Step 2: Run it and see it fail.**
  - Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/lead-team-p5b && go test ./cmd/pdx/plugin/ -run TestFiles -v`
  - Expected: `pattern all:purdex: no matching files found` (the package does not compile until the tree exists; with `embed.go` present and the tree absent the same message).

- [ ] **Step 3: Create the tree and `embed.go`.**

`cmd/pdx/plugin/embed.go`:

```go
// Package plugin embeds the Purdex Claude Code plugin (the mod and the
// pdx-team skill) into the pdx binary. The files live under ./purdex/ and are
// the same tree `claude plugin test` and `claude plugin validate` run on.
package plugin

import (
	"embed"
	"io/fs"
)

// tree holds purdex/ whole. The all: prefix is what brings the dot-folder
// .claude-plugin/ in; a plain pattern skips names starting with '.'.
//
//go:embed all:purdex
var tree embed.FS

// Files is the plugin folder as an fs.FS rooted at the folder that holds
// .claude-plugin/plugin.json.
func Files() fs.FS {
	sub, err := fs.Sub(tree, "purdex")
	if err != nil {
		panic("plugin: embedded tree lacks purdex/: " + err.Error())
	}
	return sub
}
```

`cmd/pdx/plugin/purdex/.claude-plugin/plugin.json` (the `version` is restamped at extraction, Task 5b.2; `author` is what `--strict` demands, MP6):

```json
{
  "name": "purdex",
  "version": "1.0.0",
  "description": "Purdex mod: context relay for this session (self relay with approval, member relay on the lead's order) and the pdx-team skill.",
  "author": { "name": "Purdex", "url": "https://github.com/wake/purdex" }
}
```

`cmd/pdx/plugin/purdex/hooks/hooks.json`:

```json
{ "modules": ["./register.js"] }
```

`cmd/pdx/plugin/purdex/hooks/register.js`:

```js
// Purdex mod (P5b-1): says hello to the daemon at an interactive session.start
// and again after every /clear (the new conversation has a new session id,
// M1, and the daemon keys mod presence by it — spec §8.3, P8a-1d).
// The relay itself lands in P5b-2/P5b-3 (spec §8.7).

const VERSION = '1' // the mod ↔ daemon protocol version `pdx relay hello --version` reports
const CALL_TIMEOUT_MS = 35_000 // one daemonclient grace (30 s) plus slack

const s = { interactive: false, pdx: 'pdx' }

function parseJSON(text) {
  try { return JSON.parse(text) } catch { return undefined }
}

async function run($, argv, timeoutMs) {
  try {
    return await $.process.run([s.pdx, ...argv], { timeoutMs })
  } catch (err) {
    return { exitCode: 20, stdout: '', stderr: String(err) }
  }
}

async function hello($) {
  const sid = await $.session.id()
  await run($, ['relay', 'hello', '--session', sid, '--version', VERSION, '--agent', 'cc'], CALL_TIMEOUT_MS)
}

export function register(on) {
  on('session.start', async ($, e, next) => {
    s.interactive = !!e.isInteractive
    if (!s.interactive) return next(e) // a Nexen worker's `claude -p`: the mod does nothing (spec §5)
    const cfg = parseJSON(await $.fs.read($.plugin.root + '/pdx.json').catch(() => ''))
    if (cfg && cfg.pdx) s.pdx = cfg.pdx // written beside VERSION by the extractor; absent in `claude plugin test`
    await hello($)
    return next(e)
  })

  // /clear gives the conversation a new session id (M1): say hello again
  // under it, or the daemon's presence record (and P8a-1d's terminal-only
  // backstop) would still name the old one. Not for startup / resume (that
  // is session.start's hello) and never when headless.
  on('classic.SessionStart', async ($, e, next) => {
    const r = await next(e)
    if (s.interactive && e.source === 'clear') await hello($)
    return r
  })
}
```

`cmd/pdx/plugin/purdex/hooks/relay.test.ts`:

```ts
// Run with `claude plugin test cmd/pdx/plugin/purdex`. The test's `on` hooks
// stand for the engine beneath the mod (a fake pdx behind $.process.run).
import { test, expect } from 'claude-code/testing'

function world(on: any, ids: { sid: string } = { sid: 'sid-1' }) {
  const argvs: string[][] = []
  on('process.run', async (_$: any, e: any) => {
    argvs.push([...e.argv])
    return { value: { exitCode: 0, stdout: '{"ok":true,"role":"none","self_relay":"on","threshold":70,"min_growth":20000}', stderr: '', isStdoutTruncated: false, isStderrTruncated: false } }
  })
  on('session.id', async () => ({ value: ids.sid }))
  on('fs.read', async (_$: any, e: any) => (e.path.endsWith('/pdx.json') ? { value: '{"pdx":"/opt/pdx/bin/pdx","data_dir":"/tmp/pdx"}' } : { deny: 'ENOENT' }))
  on('session.start', async (_$: any, e: any) => ({ cwd: e.cwd }))
  on('classic.SessionStart', async () => ({}))
  return argvs
}

const sub = (a: string[]) => a.slice(1).join(' ')

test('an interactive session.start says hello through the pdx named in pdx.json', async ($, on) => {
  const argvs = world(on)
  await $.session.start({ cwd: '/tmp', surface: 'terminal', isInteractive: true })
  expect(argvs).toEqual([['/opt/pdx/bin/pdx', 'relay', 'hello', '--session', 'sid-1', '--version', '1', '--agent', 'cc']])
})

test('a headless session.start (claude -p) calls nothing', async ($, on) => {
  const argvs = world(on)
  await $.session.start({ cwd: '/tmp', surface: null, isInteractive: false })
  expect(argvs).toEqual([])
})

// Spec §8.3 / P8a-1d: presence is keyed by session id and /clear mints a new
// one. Mutation gate: drop the classic.SessionStart hook → one hello only.
test('after /clear the mod says hello again with the new session id', async ($, on) => {
  const ids = { sid: 'sid-1' }
  const argvs = world(on, ids)
  await $.session.start({ cwd: '/tmp', surface: 'terminal', isInteractive: true })
  ids.sid = 'sid-2'
  await $.classic.SessionStart({ source: 'clear' })
  expect(argvs.map(sub)).toEqual(['relay hello --session sid-1 --version 1 --agent cc', 'relay hello --session sid-2 --version 1 --agent cc'])
})

test('a SessionStart that is not a clear adds no hello (startup / resume are session.start’s)', async ($, on) => {
  const argvs = world(on)
  await $.session.start({ cwd: '/tmp', surface: 'terminal', isInteractive: true })
  await $.classic.SessionStart({ source: 'startup' })
  await $.classic.SessionStart({ source: 'resume' })
  expect(argvs.length).toBe(1)
})

test('a /clear while headless says nothing', async ($, on) => {
  const argvs = world(on)
  await $.session.start({ cwd: '/tmp', surface: null, isInteractive: false })
  await $.classic.SessionStart({ source: 'clear' })
  expect(argvs).toEqual([])
})

test('without pdx.json the mod falls back to pdx on PATH', async ($, on) => {
  const argvs: string[][] = []
  on('process.run', async (_$: any, e: any) => { argvs.push([...e.argv]); return { value: { exitCode: 1, stdout: '', stderr: 'unknown command', isStdoutTruncated: false, isStderrTruncated: false } } })
  on('session.id', async () => ({ value: 'sid-1' }))
  on('fs.read', async () => ({ deny: 'ENOENT' }))
  on('session.start', async (_$: any, e: any) => ({ cwd: e.cwd }))
  const r = await $.session.start({ cwd: '/tmp', surface: 'terminal', isInteractive: true })
  expect(argvs[0][0]).toBe('pdx')
  expect(r).toEqual({ cwd: '/tmp' }) // a failed hello never fails the session
})
```

`cmd/pdx/plugin/purdex/skills/pdx-team/SKILL.md` (placeholder; the §10 text lands in Task 5b.6):

```markdown
---
name: pdx-team
description: Purdex lead / member / team and context relay (text lands in P5b-3).
---

# pdx-team

Placeholder; the skill text (spec §10) ships with P5b-3.
```

- [ ] **Step 4: Run the Go test, the mod tests and the validate gate; see them pass.**
  - Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/lead-team-p5b && go test ./cmd/pdx/plugin/ -run TestFiles -v` → `PASS`
  - Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/lead-team-p5b && claude plugin test cmd/pdx/plugin/purdex` → `6 pass`, `0 fail` (the 3 measured at 0.15 s plus the three `/clear` cases)
  - Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/lead-team-p5b && claude plugin validate --strict cmd/pdx/plugin/purdex` → ends `✔ Validation passed`, with `./register.js env reads: nothing` and `calls: $.fs.read, $.process.run (via run), $.session.id`

- [ ] **Step 5: Commit.**
  ```bash
  git add cmd/pdx/plugin/embed.go cmd/pdx/plugin/embed_test.go cmd/pdx/plugin/purdex/.claude-plugin/plugin.json cmd/pdx/plugin/purdex/hooks/hooks.json cmd/pdx/plugin/purdex/hooks/register.js cmd/pdx/plugin/purdex/hooks/relay.test.ts cmd/pdx/plugin/purdex/skills/pdx-team/SKILL.md
  git commit -m "feat(pdx): embed the Purdex Claude Code plugin (hello-only mod: session.start and after /clear; skill placeholder)

  Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
  ```

### Task 5b.2: Extraction and the `CLAUDE_CODE_PLUGIN_DIRS` merge — `internal/agent/cc/plugin.go`

**Files:**
- Create: `internal/agent/cc/plugin.go`, `internal/agent/cc/plugin_test.go`
- Reuses (no change): `loadSettings(path)` and `writeSettingsAtomic(path, settings)` in `internal/agent/cc/statusline.go:93-134` (the `.tmp` + rename write that keeps the file mode), `readSettings(t, path)` in `internal/agent/cc/hooks_test.go:49-60`.

**Interfaces:**
- Produces:
  ```go
  package cc
  const PluginDirName = "cc-plugin"
  const PluginName = "purdex"
  var PluginSource fs.FS                       // set by cmd/pdx/main.go; nil ⇒ no plugin to install
  func PluginRoot(dataDir string) string       // <data_dir>/cc-plugin/purdex
  func ExtractPlugin(src fs.FS, dataDir, version, pdxPath string) (root string, changed bool, err error)
  func RemovePluginDir(dataDir string) error
  func mergePluginDirs(settingsPath, dataDir, pluginRoot string, remove bool) error
  ```
- Rules: a `VERSION` equal to `version` ⇒ no rewrite of the tree (only `pdx.json` is refreshed, so a moved binary is found); `version` `""` or `"unknown"` (a `go build` without the Makefile's ldflags, `internal/buildinfo/buildinfo.go:11-16`) ⇒ always re-extract; extraction goes to `<root>.tmp` then `os.Rename`, so a session that watches the folder never sees a half tree; `.claude-plugin/plugin.json` `version` is restamped when `version` is a semver. The env list uses `os.PathListSeparator` (reference.md:68 "the platform's path-list separator"); every entry under `<data_dir>/cc-plugin/` is ours and is dropped before appending, so the merge is idempotent and replaces a stale path; an empty list deletes the key and an empty `env` deletes the block; `env` not an object or the key not a string ⇒ error (the hooks installer's "unsupported value shape" rule, `hooks.go:184-199`).

- [ ] **Step 1: Write the failing tests.**

```go
package cc

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

func fakePlugin(version string) fstest.MapFS {
	return fstest.MapFS{
		".claude-plugin/plugin.json": {Data: []byte(`{"name":"purdex","version":"` + version + `"}`)},
		"hooks/hooks.json":           {Data: []byte(`{"modules":["./register.js"]}`)},
		"hooks/register.js":          {Data: []byte("export function register(on) {} // " + version)},
		"skills/pdx-team/SKILL.md":   {Data: []byte("---\nname: pdx-team\n---\n")},
	}
}

func envDirs(t *testing.T, settings map[string]any) (string, bool) {
	t.Helper()
	env, ok := settings["env"].(map[string]any)
	if !ok {
		return "", false
	}
	v, ok := env["CLAUDE_CODE_PLUGIN_DIRS"].(string)
	return v, ok
}

func TestExtractPlugin_WritesTreeVersionAndPdxJSON(t *testing.T) {
	dataDir := t.TempDir()
	root, changed, err := ExtractPlugin(fakePlugin("1.0.0-alpha.530"), dataDir, "1.0.0-alpha.530", "/opt/pdx")
	if err != nil || !changed {
		t.Fatalf("first extract: changed=%v err=%v", changed, err)
	}
	if root != filepath.Join(dataDir, "cc-plugin", "purdex") {
		t.Fatalf("root = %s", root)
	}
	for _, rel := range []string{".claude-plugin/plugin.json", "hooks/hooks.json", "hooks/register.js", "skills/pdx-team/SKILL.md", "VERSION", "pdx.json"} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); err != nil {
			t.Errorf("%s missing: %v", rel, err)
		}
	}
	v, _ := os.ReadFile(filepath.Join(root, "VERSION"))
	if strings.TrimSpace(string(v)) != "1.0.0-alpha.530" {
		t.Fatalf("VERSION = %q", v)
	}
	var pj map[string]string
	b, _ := os.ReadFile(filepath.Join(root, "pdx.json"))
	if err := json.Unmarshal(b, &pj); err != nil || pj["pdx"] != "/opt/pdx" || pj["data_dir"] != dataDir {
		t.Fatalf("pdx.json = %s (%v)", b, err)
	}
	if _, err := os.Stat(root + ".tmp"); !os.IsNotExist(err) {
		t.Fatal("the .tmp sibling must not remain")
	}
}

func TestExtractPlugin_SameVersionIsNoop_NewVersionReplaces(t *testing.T) {
	dataDir := t.TempDir()
	if _, _, err := ExtractPlugin(fakePlugin("a"), dataDir, "a", "/opt/pdx"); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(PluginRoot(dataDir), "hooks", "stale.js")
	os.WriteFile(stale, []byte("old"), 0o644)
	_, changed, err := ExtractPlugin(fakePlugin("a"), dataDir, "a", "/opt/pdx")
	if err != nil || changed {
		t.Fatalf("same version: changed=%v err=%v", changed, err)
	}
	if _, err := os.Stat(stale); err != nil {
		t.Fatal("a same-version extract must not touch the tree")
	}
	_, changed, err = ExtractPlugin(fakePlugin("b"), dataDir, "b", "/opt/pdx")
	if err != nil || !changed {
		t.Fatalf("new version: changed=%v err=%v", changed, err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatal("a re-extract must replace the whole tree, not merge into it")
	}
	js, _ := os.ReadFile(filepath.Join(PluginRoot(dataDir), "hooks", "register.js"))
	if !strings.HasSuffix(strings.TrimSpace(string(js)), "// b") {
		t.Fatalf("register.js not replaced: %q", js)
	}
}

func TestExtractPlugin_UnknownVersionAlwaysReextracts_AndSemverStampsManifest(t *testing.T) {
	dataDir := t.TempDir()
	for i := 0; i < 2; i++ {
		_, changed, err := ExtractPlugin(fakePlugin("x"), dataDir, "unknown", "/opt/pdx")
		if err != nil || !changed {
			t.Fatalf("run %d with version unknown: changed=%v err=%v (a dev build must always re-extract)", i, changed, err)
		}
	}
	b, _ := os.ReadFile(filepath.Join(PluginRoot(dataDir), ".claude-plugin", "plugin.json"))
	if !strings.Contains(string(b), `"version":"unknown"`) && strings.Contains(string(b), `"unknown"`) {
		t.Fatalf("unknown must not be stamped into plugin.json: %s", b)
	}
	if _, _, err := ExtractPlugin(fakePlugin("x"), dataDir, "1.0.0-alpha.530", "/opt/pdx"); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(filepath.Join(PluginRoot(dataDir), ".claude-plugin", "plugin.json"))
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil || m["version"] != "1.0.0-alpha.530" || m["name"] != "purdex" {
		t.Fatalf("plugin.json = %s (%v)", b, err)
	}
}

func TestExtractPlugin_NilSourceErrors(t *testing.T) {
	if _, _, err := ExtractPlugin(nil, t.TempDir(), "v", "/opt/pdx"); err == nil {
		t.Fatal("nil source must error")
	}
}

func TestMergePluginDirs_CreatesEnvAndIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	dataDir := filepath.Join(dir, "pdx")
	root := PluginRoot(dataDir)
	for i := 0; i < 2; i++ {
		if err := mergePluginDirs(path, dataDir, root, false); err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
	}
	v, ok := envDirs(t, readSettings(t, path))
	if !ok || v != root {
		t.Fatalf("CLAUDE_CODE_PLUGIN_DIRS = %q ok=%v, want exactly %q once", v, ok, root)
	}
}

func TestMergePluginDirs_AppendsToExistingListAndKeepsOtherKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	dataDir := filepath.Join(dir, "pdx")
	root := PluginRoot(dataDir)
	sep := string(os.PathListSeparator)
	os.WriteFile(path, []byte(`{"env":{"FOO":"1","CLAUDE_CODE_PLUGIN_DIRS":"/Users/x/mods/a`+sep+`/Users/x/mods/b"},"hooks":{}}`), 0o644)
	if err := mergePluginDirs(path, dataDir, root, false); err != nil {
		t.Fatal(err)
	}
	s := readSettings(t, path)
	v, _ := envDirs(t, s)
	if v != "/Users/x/mods/a"+sep+"/Users/x/mods/b"+sep+root {
		t.Fatalf("got %q", v)
	}
	if s["env"].(map[string]any)["FOO"] != "1" {
		t.Fatal("other env keys must be kept")
	}
	if _, ok := s["hooks"]; !ok {
		t.Fatal("other settings keys must be kept")
	}
}

func TestMergePluginDirs_ReplacesStaleEntryUnderCcPluginPrefix(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	dataDir := filepath.Join(dir, "pdx")
	root := PluginRoot(dataDir)
	sep := string(os.PathListSeparator)
	stale := filepath.Join(dataDir, "cc-plugin", "purdex-old")
	os.WriteFile(path, []byte(`{"env":{"CLAUDE_CODE_PLUGIN_DIRS":"`+stale+sep+`/Users/x/mods/a"}}`), 0o644)
	if err := mergePluginDirs(path, dataDir, root, false); err != nil {
		t.Fatal(err)
	}
	v, _ := envDirs(t, readSettings(t, path))
	if v != "/Users/x/mods/a"+sep+root {
		t.Fatalf("got %q", v)
	}
}

func TestMergePluginDirs_RemoveKeepsOthersAndDeletesEmptyEnv(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	dataDir := filepath.Join(dir, "pdx")
	root := PluginRoot(dataDir)
	sep := string(os.PathListSeparator)
	os.WriteFile(path, []byte(`{"env":{"CLAUDE_CODE_PLUGIN_DIRS":"/Users/x/mods/a`+sep+root+`"}}`), 0o644)
	if err := mergePluginDirs(path, dataDir, root, true); err != nil {
		t.Fatal(err)
	}
	v, _ := envDirs(t, readSettings(t, path))
	if v != "/Users/x/mods/a" {
		t.Fatalf("got %q", v)
	}
	os.WriteFile(path, []byte(`{"env":{"CLAUDE_CODE_PLUGIN_DIRS":"`+root+`"},"other":true}`), 0o644)
	if err := mergePluginDirs(path, dataDir, root, true); err != nil {
		t.Fatal(err)
	}
	s := readSettings(t, path)
	if _, ok := s["env"]; ok {
		t.Fatalf("env block must go when empty: %v", s["env"])
	}
	if s["other"] != true {
		t.Fatal("other keys kept")
	}
}

func TestMergePluginDirs_RemoveOnMissingFileIsNoop(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	if err := mergePluginDirs(path, filepath.Join(dir, "pdx"), PluginRoot(filepath.Join(dir, "pdx")), true); err != nil {
		t.Fatal(err)
	}
	s := readSettings(t, path)
	if len(s) != 0 {
		t.Fatalf("got %v", s)
	}
}

func TestMergePluginDirs_UnsupportedShapesError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	dataDir := filepath.Join(dir, "pdx")
	os.WriteFile(path, []byte(`{"env":[]}`), 0o644)
	if err := mergePluginDirs(path, dataDir, PluginRoot(dataDir), false); err == nil {
		t.Fatal("env array must error")
	}
	os.WriteFile(path, []byte(`{"env":{"CLAUDE_CODE_PLUGIN_DIRS":["/a"]}}`), 0o644)
	if err := mergePluginDirs(path, dataDir, PluginRoot(dataDir), false); err == nil {
		t.Fatal("non-string value must error")
	}
}
```

- [ ] **Step 2: Run them and see them fail.**
  - Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/lead-team-p5b && go test ./internal/agent/cc/ -run 'TestExtractPlugin|TestMergePluginDirs' -v`
  - Expected: FAIL to compile: `undefined: ExtractPlugin`, `undefined: PluginRoot`, `undefined: mergePluginDirs`.

- [ ] **Step 3: Implement `plugin.go`.**

```go
package cc

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// PluginDirName is the folder under <data_dir> that holds extracted Claude
// Code plugins; the Purdex mod lives in <data_dir>/cc-plugin/purdex/.
const PluginDirName = "cc-plugin"

// PluginName is the plugin folder and the manifest's name.
const PluginName = "purdex"

// pluginDirsEnv is the settings.json env key Claude Code reads for extra
// plugin folders (platform path-list separated; spec M2).
const pluginDirsEnv = "CLAUDE_CODE_PLUGIN_DIRS"

// PluginSource is the embedded plugin tree, set by cmd/pdx at start
// (plugin.Files()). nil means "no plugin to install" (tests of the hook
// installer alone, or a build without the tree).
var PluginSource fs.FS

// PluginRoot is where the plugin is extracted for a data dir.
func PluginRoot(dataDir string) string {
	return filepath.Join(dataDir, PluginDirName, PluginName)
}

// ExtractPlugin writes src into PluginRoot(dataDir) when the VERSION stamp
// there differs from version (or is missing), then writes VERSION and
// pdx.json {pdx, data_dir}. It returns the root and whether files were
// written. Extraction goes to a sibling .tmp dir and is renamed into place
// so a session loading the folder never sees a half-written tree.
func ExtractPlugin(src fs.FS, dataDir, version, pdxPath string) (root string, changed bool, err error) {
	root = PluginRoot(dataDir)
	if src == nil {
		return root, false, errors.New("plugin source is nil")
	}
	// A binary built without ldflags reports "unknown"; such a dev build always
	// re-extracts, so an edited mod reaches the next session without a bump.
	if cur, err := os.ReadFile(filepath.Join(root, "VERSION")); err == nil && version != "" && version != "unknown" && strings.TrimSpace(string(cur)) == version {
		if err := writePdxJSON(root, pdxPath, dataDir); err != nil {
			return root, false, err
		}
		return root, false, nil
	}
	tmp := root + ".tmp"
	_ = os.RemoveAll(tmp)
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		return root, false, fmt.Errorf("create %s: %w", tmp, err)
	}
	if err := copyFS(tmp, src); err != nil {
		_ = os.RemoveAll(tmp)
		return root, false, err
	}
	if err := os.WriteFile(filepath.Join(tmp, "VERSION"), []byte(version+"\n"), 0o644); err != nil {
		_ = os.RemoveAll(tmp)
		return root, false, fmt.Errorf("write VERSION: %w", err)
	}
	if err := writePdxJSON(tmp, pdxPath, dataDir); err != nil {
		_ = os.RemoveAll(tmp)
		return root, false, err
	}
	if err := stampManifest(tmp, version); err != nil {
		_ = os.RemoveAll(tmp)
		return root, false, err
	}
	_ = os.RemoveAll(root)
	if err := os.Rename(tmp, root); err != nil {
		_ = os.RemoveAll(tmp)
		return root, false, fmt.Errorf("rename %s: %w", tmp, err)
	}
	return root, true, nil
}

// stampManifest sets .claude-plugin/plugin.json "version" to the pdx version
// when it is a semver (1.0.0-alpha.527); "unknown" leaves the file as embedded.
func stampManifest(root, version string) error {
	if !semverRe.MatchString(version) {
		return nil
	}
	path := filepath.Join(root, ".claude-plugin", "plugin.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read plugin.json: %w", err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return fmt.Errorf("parse plugin.json: %w", err)
	}
	m["version"] = version
	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(out, '\n'), 0o644)
}

var semverRe = regexp.MustCompile(`^\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?$`)

func writePdxJSON(root, pdxPath, dataDir string) error {
	b, _ := json.Marshal(map[string]string{"pdx": pdxPath, "data_dir": dataDir})
	return os.WriteFile(filepath.Join(root, "pdx.json"), append(b, '\n'), 0o644)
}

func copyFS(dst string, src fs.FS) error {
	return fs.WalkDir(src, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		target := filepath.Join(dst, filepath.FromSlash(p))
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := fs.ReadFile(src, p)
		if err != nil {
			return fmt.Errorf("read embedded %s: %w", p, err)
		}
		return os.WriteFile(target, data, 0o644)
	})
}

// RemovePluginDir deletes the extracted tree (and its .tmp sibling).
func RemovePluginDir(dataDir string) error {
	root := PluginRoot(dataDir)
	_ = os.RemoveAll(root + ".tmp")
	if err := os.RemoveAll(root); err != nil {
		return fmt.Errorf("remove %s: %w", root, err)
	}
	return nil
}

// mergePluginDirs adds pluginRoot to settings.env.CLAUDE_CODE_PLUGIN_DIRS
// (remove=false) or takes every entry under <data_dir>/cc-plugin/ out of it
// (remove=true). Other entries are kept in order; the env block and the
// key are created or deleted as needed. Entries are recognised by the
// cc-plugin prefix, not by equality, so a moved data dir's stale entry is
// still ours to replace.
func mergePluginDirs(settingsPath, dataDir, pluginRoot string, remove bool) error {
	settings, err := loadSettings(settingsPath)
	if err != nil {
		return err
	}
	env, err := envMapForMerge(settings)
	if err != nil {
		return err
	}
	var current string
	if v, ok := env[pluginDirsEnv]; ok && v != nil {
		s, isStr := v.(string)
		if !isStr {
			return fmt.Errorf("claude env %s has unsupported value shape", pluginDirsEnv)
		}
		current = s
	}
	prefix := filepath.Join(dataDir, PluginDirName) + string(filepath.Separator)
	kept := make([]string, 0, 4)
	for _, p := range strings.Split(current, string(os.PathListSeparator)) {
		if p == "" || isUnderPrefix(p, prefix) {
			continue
		}
		kept = append(kept, p)
	}
	if !remove {
		kept = append(kept, pluginRoot)
	}
	if len(kept) == 0 {
		delete(env, pluginDirsEnv)
	} else {
		env[pluginDirsEnv] = strings.Join(kept, string(os.PathListSeparator))
	}
	if len(env) == 0 {
		delete(settings, "env")
	} else {
		settings["env"] = env
	}
	return writeSettingsAtomic(settingsPath, settings)
}

func isUnderPrefix(p, prefix string) bool {
	return strings.HasPrefix(filepath.Clean(p)+string(filepath.Separator), prefix)
}

func envMapForMerge(settings map[string]any) (map[string]any, error) {
	v, ok := settings["env"]
	if !ok || v == nil {
		return make(map[string]any), nil
	}
	env, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("claude settings env has unsupported value shape")
	}
	return env, nil
}
```

- [ ] **Step 4: Run and see them pass.**
  - Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/lead-team-p5b && go test ./internal/agent/cc/ -run 'TestExtractPlugin|TestMergePluginDirs' -v` → 10 `PASS`
  - Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/lead-team-p5b && go vet ./internal/agent/cc/ && gofmt -l internal/agent/cc/` → empty

- [ ] **Step 5: Commit.**
  ```bash
  git add internal/agent/cc/plugin.go internal/agent/cc/plugin_test.go
  git commit -m "feat(cc): extract the embedded plugin under the data dir and merge CLAUDE_CODE_PLUGIN_DIRS

  Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
  ```

### Task 5b.3: Install and remove through the CC `HookInstaller`; hand the tree over in `main`

**Files:**
- Modify: `internal/agent/cc/hooks.go:15-28` (`InstallHooks`, `RemoveHooks`)
- Modify: `cmd/pdx/main.go:17-37` (two imports) and `cmd/pdx/main.go:40-46` (one line after the usage check)
- Test: `internal/agent/cc/hooks_test.go` (append), `cmd/pdx/setup_test.go` (append a sub-test), `internal/module/agent/handler_test.go` (append)

**Interfaces:**
- Consumes: `agent.HookInstaller` (`internal/agent/provider.go:42-47`), unchanged. `pdx setup --agent cc` (`cmd/pdx/setup.go`) and `handleHookSetup` (`internal/module/agent/handler.go:843-892`) call `InstallHooks(pdxPath)` / `RemoveHooks(pdxPath)` and so install the plugin with no change of their own. `localSetup` builds the provider with a nil config (`setup.go:114`), so the data dir falls back to `config.Load("")` → `$HOME/.config/pdx` (`internal/config/config.go:281-285`).
- Produces: after install, `~/.claude/settings.json` has `env.CLAUDE_CODE_PLUGIN_DIRS` containing `<data_dir>/cc-plugin/purdex` and that folder holds the tree, `VERSION` and `pdx.json`; after remove, neither.

- [ ] **Step 1: Write the failing tests.**

Append to `internal/agent/cc/hooks_test.go`:

```go
func TestCCInstallHooks_InstallsPluginWhenSourceSet_RemoveTakesItOut(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dataDir := t.TempDir()
	old := PluginSource
	PluginSource = fakePlugin("v")
	t.Cleanup(func() { PluginSource = old })
	var mu sync.RWMutex
	p := NewProvider(nil, nil, &config.Config{DataDir: dataDir}, &mu)

	if err := p.InstallHooks("/usr/local/bin/pdx"); err != nil {
		t.Fatalf("InstallHooks: %v", err)
	}
	settingsPath := filepath.Join(home, ".claude", "settings.json")
	s := readSettings(t, settingsPath)
	if _, ok := hooksMap(t, s)["Stop"]; !ok {
		t.Fatal("the hooks are still installed")
	}
	dirs, ok := envDirs(t, s)
	if !ok || dirs != PluginRoot(dataDir) {
		t.Fatalf("CLAUDE_CODE_PLUGIN_DIRS = %q ok=%v", dirs, ok)
	}
	for _, rel := range []string{"VERSION", "pdx.json", "hooks/register.js"} {
		if _, err := os.Stat(filepath.Join(PluginRoot(dataDir), filepath.FromSlash(rel))); err != nil {
			t.Errorf("%s: %v", rel, err)
		}
	}

	if err := p.RemoveHooks("/usr/local/bin/pdx"); err != nil {
		t.Fatalf("RemoveHooks: %v", err)
	}
	s = readSettings(t, settingsPath)
	if _, ok := envDirs(t, s); ok {
		t.Fatalf("env still names the plugin: %v", s["env"])
	}
	if _, err := os.Stat(PluginRoot(dataDir)); !os.IsNotExist(err) {
		t.Fatal("the extracted folder must be removed")
	}
}

func TestCCInstallHooks_NilPluginSourceLeavesEnvAlone(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	old := PluginSource
	PluginSource = nil
	t.Cleanup(func() { PluginSource = old })
	p := NewProvider(nil, nil, nil, nil)
	if err := p.InstallHooks("/usr/local/bin/pdx"); err != nil {
		t.Fatalf("InstallHooks: %v", err)
	}
	s := readSettings(t, filepath.Join(home, ".claude", "settings.json"))
	if _, ok := s["env"]; ok {
		t.Fatalf("no plugin source ⇒ no env block, got %v", s["env"])
	}
}
```
(`hooks_test.go` needs `"sync"` and `"github.com/wake/purdex/internal/config"` in its imports; `fakePlugin` and `envDirs` come from `plugin_test.go`, same package.)

Append to `cmd/pdx/setup_test.go` inside `TestLocalSetup`, after the `"cc install creates settings.json"` sub-test:

```go
	t.Run("cc install extracts the plugin under $HOME/.config/pdx and names it in env", func(t *testing.T) {
		tmpHome := t.TempDir()
		t.Setenv("HOME", tmpHome)
		old := agentcc.PluginSource
		agentcc.PluginSource = fstest.MapFS{
			".claude-plugin/plugin.json": {Data: []byte(`{"name":"purdex","version":"0"}`)},
			"hooks/hooks.json":           {Data: []byte(`{"modules":["./register.js"]}`)},
			"hooks/register.js":          {Data: []byte("export function register() {}")},
		}
		t.Cleanup(func() { agentcc.PluginSource = old })

		if err := localSetup("cc", false); err != nil {
			t.Fatalf("localSetup cc install: %v", err)
		}
		root := filepath.Join(tmpHome, ".config", "pdx", "cc-plugin", "purdex")
		if _, err := os.Stat(filepath.Join(root, "VERSION")); err != nil {
			t.Fatalf("VERSION: %v", err)
		}
		data, _ := os.ReadFile(filepath.Join(tmpHome, ".claude", "settings.json"))
		var settings map[string]any
		if err := json.Unmarshal(data, &settings); err != nil {
			t.Fatal(err)
		}
		env, _ := settings["env"].(map[string]any)
		if env["CLAUDE_CODE_PLUGIN_DIRS"] != root {
			t.Fatalf("CLAUDE_CODE_PLUGIN_DIRS = %v, want %s", env["CLAUDE_CODE_PLUGIN_DIRS"], root)
		}

		if err := localSetup("cc", true); err != nil {
			t.Fatalf("localSetup cc remove: %v", err)
		}
		if _, err := os.Stat(root); !os.IsNotExist(err) {
			t.Fatal("remove must delete the extracted plugin")
		}
	})
```
(`setup_test.go` needs `"testing/fstest"` and `agentcc "github.com/wake/purdex/internal/agent/cc"` in its imports.)

Append to `internal/module/agent/handler_test.go`:

```go
func TestHandleHookSetup_CC_InstallsAndRemovesPlugin(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	old := agentcc.PluginSource
	agentcc.PluginSource = fstest.MapFS{
		".claude-plugin/plugin.json": {Data: []byte(`{"name":"purdex","version":"0"}`)},
		"hooks/hooks.json":           {Data: []byte(`{"modules":["./register.js"]}`)},
		"hooks/register.js":          {Data: []byte("export function register() {}")},
	}
	t.Cleanup(func() { agentcc.PluginSource = old })

	m := newTestModule(t)
	m.registry.Register(agentcc.NewProvider(nil, nil, nil, nil))
	root := filepath.Join(home, ".config", "pdx", "cc-plugin", "purdex")

	for _, action := range []string{"install", "remove"} {
		req := httptest.NewRequest("POST", "/api/hooks/cc/setup", strings.NewReader(`{"action":"`+action+`"}`))
		req.SetPathValue("agent", "cc")
		w := httptest.NewRecorder()
		m.handleHookSetup(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("%s: status = %d (body: %s)", action, w.Code, w.Body.String())
		}
		_, err := os.Stat(filepath.Join(root, "VERSION"))
		if action == "install" && err != nil {
			t.Fatalf("install: VERSION missing: %v", err)
		}
		if action == "remove" && !os.IsNotExist(err) {
			t.Fatalf("remove: plugin folder still there (%v)", err)
		}
	}
}
```
(`handler_test.go` needs `"testing/fstest"` in its imports if not already there; `agentcc`, `httptest`, `strings`, `os`, `filepath` are already imported per `handler_test.go:1061, 1289`.)

- [ ] **Step 2: Run them and see them fail.**
  - Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/lead-team-p5b && go test ./internal/agent/cc/ -run 'TestCCInstallHooks_InstallsPlugin|TestCCInstallHooks_NilPluginSource' -v`
  - Expected: `TestCCInstallHooks_InstallsPluginWhenSourceSet_RemoveTakesItOut` FAILs at `CLAUDE_CODE_PLUGIN_DIRS = "" ok=false` (the installer does not touch `env` yet); the nil-source test passes already (it pins the current behaviour).
  - Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/lead-team-p5b && go test ./cmd/pdx/ -run TestLocalSetup -v` → the new sub-test FAILs with `VERSION: stat …/cc-plugin/purdex/VERSION: no such file or directory`.
  - Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/lead-team-p5b && go test ./internal/module/agent/ -run TestHandleHookSetup_CC -v` → FAILs with `install: VERSION missing`.

- [ ] **Step 3: Implement.**

Replace `internal/agent/cc/hooks.go:15-28` with:

```go
func (p *Provider) InstallHooks(pdxPath string) error {
	settingsPath, err := ccSettingsPath()
	if err != nil {
		return fmt.Errorf("cannot determine home directory: %w", err)
	}
	if err := mergeClaudeHooks(settingsPath, pdxPath, false); err != nil {
		return err
	}
	return p.installPlugin(settingsPath, pdxPath)
}

func (p *Provider) RemoveHooks(pdxPath string) error {
	settingsPath, err := ccSettingsPath()
	if err != nil {
		return fmt.Errorf("cannot determine home directory: %w", err)
	}
	if err := mergeClaudeHooks(settingsPath, pdxPath, true); err != nil {
		return err
	}
	return p.removePlugin(settingsPath)
}

// dataDir is the daemon's data dir when the provider has a config (the
// daemon's own provider, module.go:238), else the default config's
// ($HOME/.config/pdx) — the case of `pdx setup` without a daemon
// (cmd/pdx/setup.go:114 builds the provider with nil deps).
func (p *Provider) dataDir() string {
	if p.cfg != nil {
		if p.cfgMu != nil {
			p.cfgMu.RLock()
			defer p.cfgMu.RUnlock()
		}
		if p.cfg.DataDir != "" {
			return p.cfg.DataDir
		}
	}
	cfg, _ := config.Load("")
	return cfg.DataDir
}

// installPlugin extracts the embedded plugin (spec §5 "Shipping") and names
// it in settings.json env. Without an embedded tree it does nothing, so the
// hook installer's own tests are unaffected.
func (p *Provider) installPlugin(settingsPath, pdxPath string) error {
	if PluginSource == nil {
		return nil
	}
	dataDir := p.dataDir()
	root, _, err := ExtractPlugin(PluginSource, dataDir, buildinfo.Version, pdxPath)
	if err != nil {
		return fmt.Errorf("extract plugin: %w", err)
	}
	return mergePluginDirs(settingsPath, dataDir, root, false)
}

func (p *Provider) removePlugin(settingsPath string) error {
	dataDir := p.dataDir()
	if err := mergePluginDirs(settingsPath, dataDir, PluginRoot(dataDir), true); err != nil {
		return err
	}
	return RemovePluginDir(dataDir)
}
```
and add `"github.com/wake/purdex/internal/buildinfo"` and `"github.com/wake/purdex/internal/config"` to `hooks.go`'s imports (`config` is already imported by `provider.go:10`, so the package compiles either way; the file's own import list is what `goimports` wants).

In `cmd/pdx/main.go`, add the imports
```go
	"github.com/wake/purdex/cmd/pdx/plugin"
	agentcc "github.com/wake/purdex/internal/agent/cc"
```
and, in `main()` right after the usage check (`main.go:41-45`), before the `switch`:
```go
	// The embedded Claude Code plugin reaches the CC hook installer here, so
	// internal/ never imports cmd/ (the installer only sees an fs.FS).
	agentcc.PluginSource = plugin.Files()
```

- [ ] **Step 4: Run and see them pass.**
  - Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/lead-team-p5b && go test ./internal/agent/cc/ ./cmd/pdx/ ./internal/module/agent/ -run 'TestCCInstallHooks|TestMergeClaudeHooks|TestLocalSetup|TestHandleHookSetup|TestExtractPlugin|TestMergePluginDirs' -v` → all `PASS` (the existing `TestMergeClaudeHooks_*` and `TestCCInstallHooks_*` are untouched: `mergeClaudeHooks` did not change).
  - Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/lead-team-p5b && go build ./... && go vet ./... && gofmt -l cmd internal` → empty.

- [ ] **Step 5: Commit.**
  ```bash
  git add internal/agent/cc/hooks.go internal/agent/cc/hooks_test.go cmd/pdx/main.go cmd/pdx/setup_test.go internal/module/agent/handler_test.go
  git commit -m "feat(cc): pdx setup and the setup route install and remove the embedded plugin

  Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
  ```

### Manual check for P5b-1 (not a gate; mlab, after the daemon deploy)

1. `pdx setup --agent cc` → `pdx hooks for cc installed`; `cat ~/.config/pdx/cc-plugin/purdex/VERSION` prints the deployed version; `jq .env ~/.claude/settings.json` shows `CLAUDE_CODE_PLUGIN_DIRS` ending in `/.config/pdx/cc-plugin/purdex` (appended to whatever was there).
2. Open a throwaway `claude` in tmux (the probes' recipe, `docs/specs/assets/2026-10-06-lead-team-probes/README.md`); `/plugins` lists `purdex`; `claude --debug` shows one `session.start` (M5: the same folder given twice loads once). Until P5a-2 ships, `pdx relay hello` exits 2 (`unknown command`) and nothing else happens — expected.
3. `pdx setup --agent cc --remove` → the env entry and the folder are gone; other `env` keys stay.

---

## PR P5b-2 — the mod's relay core (spec §8.1–§8.3 steps 4–8, §8.7 (b) approved path, §15 "Mod")

**Scope.** `cmd/pdx/plugin/purdex/hooks/register.js` grows from `hello` to the whole relay: `begin` at a turn's end, the wait loop from a timer, the write prompt, own-turn recognition, the file check with fix rounds, `/clear`, `cleared` → `hello` → seed → `done`, `tool.check` allow for the exact path, the failed-report queue. **Not here:** the prompt hold, NOTE, the compact rule and `/relay` (P5b-3), so a request opened by P5b-2 alone does not block typed prompts — which is why P5b-2 and P5b-3 ship back to back and P5b-2 is not deployed to mlab's daemon-side installer on its own (the daemon deploy that carries the plugin is batched after P5b-3).

**Needs merged first:** P5a-2 (routes `/api/relay/*` and `pdx relay hello|begin|wait|report|self`) and P5b-1.

**What the mod expects of `pdx relay` (P5a-2c's CLI as decided by the coordinator — exit 0 + `state:"open"` for a still-open wait, the 409 code as the last stderr token):**

| Call | Exit / stdout the mod reads |
|---|---|
| `pdx relay hello --session <sid> --version 1 --agent cc` | 0 + `{ok, role, self_relay, threshold, min_growth}`; any other exit ⇒ keep defaults |
| `pdx relay begin --self --session <sid> --used <pct> --window <n>` (no model / effort: the daemon fills both from the statusline reading) | 0 + `{op: RelayOp, request_id}`; 13 with the stderr line `pdx relay: <detail> <code>` — the 409 code is **the last whitespace-separated token of stderr** (`member_relay_is_leads` ⇒ the mod remembers it is a member; `self_relay_off` / `self_relay_paused` / `relay_open` ⇒ nothing); 20 / 21 / 1 ⇒ nothing |
| `pdx relay wait <request_id>` | 0 + `Approval` JSON: `state: "approved"` ⇒ approved, `state: "open"` ⇒ the CLI's own ≤ 9 min bound ended with the request still open (P5a-2c always prints it, even when no poll answered), **loop again**; any other exit-0 body (empty, unparsable, another state) ⇒ not approved; 10 denied; 11 timeout; 12 cancelled / abandoned; 20 / 21 / other ⇒ treated as not approved (§8.7 (d)) |
| `pdx relay report <op> <state> [--new-session <sid>] [--error <e>]` | 0; 13 `bad_transition` ⇒ the daemon is already past this state, **dropped** (re-sending would be refused forever); 20 / 21 / 1 ⇒ queued and re-sent at the next `turn.complete` (§8.3 "keeps relaying") |
| `pdx msg whoami` | stdout pasted into the write prompt's facts (the prototype's `who`) |

The mod runs `pdx` by the path in `pdx.json` (Task 5b.2) with `timeoutMs` 35 s for every call but `wait` (590 s, under the 10-min cap of `$.process.run`, M24); a rejected `$.process.run` (cannot start, timed out) is read as exit 20.

### Task 5b.4: `register.js` — begin, wait, write, check, clear, seed, report

**Files:**
- Modify: `cmd/pdx/plugin/purdex/hooks/register.js` (whole file replaced; the P5b-1 `hello` shape is kept inside, including its `classic.SessionStart{source:'clear'}` → `hello` branch — P5b-2 adds the `clearing` → `seeding` branch above it)
- Modify: `cmd/pdx/plugin/purdex/hooks/relay.test.ts` (whole file replaced; the three P5b-1 tests survive as the first `hello` / headless / fallback blocks)
- Test: `claude plugin test cmd/pdx/plugin/purdex`; gate `claude plugin validate --strict cmd/pdx/plugin/purdex`

**Interfaces:**
- Consumes: the `pdx relay` table above; `$.session.usage().context` `{tokens?, window, percent?}` (d.ts:10619-10645: `percent` is the status line's `used_percentage`, absent early in a session); not `$.session.model()` — the daemon fills model/effort (MP8); `turn.start.text` / `turnId` and `turn.complete.turnId` (MP3); `classic.SessionStart{source:'clear'}` after `$.command.run({command:'clear'})` (M1); `tool.check{tool, input.file_path}` (d.ts:12476-12520).
- Produces (the prompts the model reads — U16: 接力, never 交接; 接手 stays):
  - **Write prompt** (first line carries the nonce the mod recognises its turn by):
    > `[pdx-relay op=<op id>] 這個 session 的 context 已達接力門檻，使用者已核准接力（之後會 /clear）。`
    > `請先停下手邊工作，用你完整的工具撰寫接力檔：<handoff path>`
    > 要求：自己跑 `git status`、`git diff --stat`、`git log --oneline -10` …；接力檔必須自成一體…；寫完後只回一行「HANDOFF-WRITTEN」…
    > 格式：`# HANDOFF` and the eight headings
    > `## 1. 目標與完成定義（使用者要的是什麼、怎樣算完成、範圍外）` / `## 2. 進度（已完成且驗證 / 進行中停在哪 / 下一步第一個動作具體到指令）` / `## 3. 檔案異動（git status 與 diff --stat 的結果，加上每個檔案的用途）` / `## 4. 決策紀錄（選了什麼、為什麼、否決了什麼）` / `## 5. 死路（試過失敗、不要再試的）` / `## 6. 環境與指令（測試 / 執行方式）` / `## 7. 未決問題與需要使用者決定的事` / `## 8. 協作關係（下面的 pdx 身分；我的 lead 與我管理的 members，沒有就寫無）`
    > 機器提供的事實：舊 session id、舊 ref、接力時 context、pdx 身分
  - **Fix prompt:** `[pdx-relay op=<id>] 接力檔 <path> 不完整，缺少段落：<## n.、…>。請補齊後只回「HANDOFF-WRITTEN」。` (or `(內容過短)`)
  - **Seed prompt:** first line `↪ 接手自 <old ref>`, then `[pdx-relay seed op=<id>] 你是接手的新對話：前一段對話 context 已滿並已清空。` + read the file, three-line recap, `git status`, continue from 「下一步」, and `回覆的第一行請寫「↪ 接手自 <old ref>」。`
  - Status line while waiting: `接力等待核准中`; one toast: `接力等待核准：請在 Purdex App 按核准或拒絕`; give-up toast: `接力檔不完整，已放棄接力；對話照常繼續`.
- File check (spec §8.2 step 4): all eight `## n.` headings present **and** more than 200 characters; two fix rounds; else `report failed --error handoff_incomplete`, no `/clear`, state back to idle.
- Loop guard (spec §8.1): after a seed, `floor = tokens`; a new ask needs `tokens ≥ floor + min_growth` (20 000 by default, or hello's `min_growth`).
- Threshold: `PDX_RELAY_THRESHOLD` from the environment wins (acceptance), else hello's `threshold`, else 70.

- [ ] **Step 1: Write the failing tests** — replace `cmd/pdx/plugin/purdex/hooks/relay.test.ts` with:

```ts
// Purdex mod tests — run with `claude plugin test <plugin folder>`.
// The test's `on` hooks stand for the engine beneath the mod: they answer
// $.process.run (a fake pdx), $.session.*, $.fs.read, $.ui.*, and the
// bottom of every event the test raises (session.start, turn.*, prompt.submit…).
import { test, expect, mock } from 'claude-code/testing'

type Run = { argv: string[] }
type Fake = {
  argvs: string[][]
  submits: any[]
  commands: string[]
  toasts: string[]
  statuses: (string | undefined)[]
  files: Record<string, string>
  pdx: (argv: string[]) => { exitCode: number; stdout?: string; stderr?: string } | Promise<{ exitCode: number; stdout?: string; stderr?: string }>
  sessionId: string
  usage: { tokens?: number; window: number; percent?: number }
  clock: any
}

const OP = { id: 'op-1', kind: 'self', host_id: 'h', session_id: 'sid-old', ref: '_abc123', state: 'awaiting_approval', handoff_path: '/data/relay/op-1.md', created_at: 1, updated_at: 1 }
const BEGIN_OK = JSON.stringify({ op: OP, request_id: 'req-1' })
const HELLO = (role = 'none', extra = {}) => JSON.stringify({ ok: true, role, self_relay: 'on', threshold: 70, min_growth: 20000, ...extra })
const APPROVAL = (state: string) => JSON.stringify({ id: 'req-1', kind: 'self_relay', state })
const GOOD_FILE = '# HANDOFF\n' + ['## 1. a', '## 2. b', '## 3. c', '## 4. d', '## 5. e', '## 6. f', '## 7. g', '## 8. h'].map((h) => h + '\n' + 'x'.repeat(40)).join('\n')

function world(on: any, opts: Partial<Fake> = {}, env: Record<string, string> = {}): Fake {
  const f: Fake = {
    argvs: [], submits: [], commands: [], toasts: [], statuses: [], files: {},
    pdx: () => ({ exitCode: 0, stdout: HELLO() }),
    sessionId: 'sid-old',
    usage: { tokens: 10000, window: 200000, percent: 5 },
    ...opts,
  }
  f.clock = mock.clock(on)
  mock.env(on, env)
  on('tool.check', async () => ({ decision: 'ask', reason: 'mode' }))
  on('process.run', async (_$: any, e: any) => {
    f.argvs.push([...e.argv])
    const r = await f.pdx([...e.argv].slice(1))
    return { value: { exitCode: r.exitCode, stdout: r.stdout ?? '', stderr: r.stderr ?? '', isStdoutTruncated: false, isStderrTruncated: false } }
  })
  on('session.id', async () => ({ value: f.sessionId }))
  on('session.usage', async () => ({ value: { startedAt: 0, context: f.usage, rateLimits: [] } }))
  on('fs.read', async (_$: any, e: any) => (e.path in f.files ? { value: f.files[e.path] } : { deny: 'ENOENT' }))
  on('ui.toast', async (_$: any, e: any) => { f.toasts.push(e.text); return { value: undefined } })
  on('ui.status', async (_$: any, e: any) => { f.statuses.push(e.text); return { value: undefined } })
  on('ui.log', async () => ({ value: undefined }))
  on('session.start', async (_$: any, e: any) => ({ cwd: e.cwd }))
  on('turn.start', async (_$: any, e: any) => ({ turnId: e.turnId }))
  on('turn.complete', async (_$: any, e: any) => ({ text: e.answer }))
  on('classic.SessionStart', async () => ({}))
  on('prompt.submit', async (_$: any, e: any) => { f.submits.push(e); return { text: e.text, context: e.context } })
  on('command.run', async (_$: any, e: any) => { f.commands.push(e.command); return { text: 'ran ' + e.command } })
  on('session.compact', async (_$: any, e: any) => ({ messages: e.messages }))
  return f
}

const start = ($: any, interactive = true) => $.session.start({ cwd: '/tmp', surface: interactive ? 'terminal' : null, isInteractive: interactive })
const turn = ($: any, turnId: string) => $.turn.complete({ answer: 'ok', reason: 'answer', durationMs: 1, isAborted: false, turnId })
const sub = (argv: string[]) => argv.slice(1).join(' ')
const MSGS = [{ role: 'user' as const, text: 'hi', toolUses: [] }]
const compact = ($: any, trigger: string) => $.session.compact({ trigger, messages: MSGS })

// A fake pdx: hello ok; begin ok; wait answers from a queue; everything else ok.
function pdxWith(waits: Array<{ exitCode: number; stdout?: string }>, role = 'none') {
  return (argv: string[]) => {
    const [, cmd] = argv
    if (cmd === 'hello') return { exitCode: 0, stdout: HELLO(role) }
    if (cmd === 'begin') return { exitCode: 0, stdout: BEGIN_OK }
    if (cmd === 'wait') return waits.shift() ?? new Promise<never>(() => {}) // the long-poll blocks until the test ends
    if (argv[0] === 'msg') return { exitCode: 0, stdout: 'mlab/purdex-x [abc123]' }
    return { exitCode: 0, stdout: '{}' }
  }
}

// ---------- P5b-2: hello, begin, guards ----------

test('hello at an interactive session.start', async ($, on) => {
  const f = world(on)
  await start($)
  expect(f.argvs.map(sub)).toEqual(['relay hello --session sid-old --version 1 --agent cc'])
})

test('headless does nothing, ever: no hello, no begin at 90 %, prompts and compaction untouched', async ($, on) => {
  const f = world(on, { usage: { tokens: 180000, window: 200000, percent: 90 } })
  await start($, false)
  await turn($, 't1')
  await $.prompt.submit({ text: 'hi', wait: false, origin: { kind: 'composer' } })
  expect(await compact($, 'auto')).toEqual({ messages: MSGS })
  expect(f.argvs).toEqual([])
  expect(f.submits.length).toBe(1)
})

test('begin at used ≥ 70: pdx relay begin --self with usage, model; status line set; wait loop starts from a timer', async ($, on) => {
  const f = world(on, { pdx: pdxWith([]), usage: { tokens: 144000, window: 200000, percent: 72 } })
  const clock = f.clock
  await start($)
  await turn($, 't1')
  expect(f.argvs.map(sub).slice(1)).toEqual(['relay begin --self --session sid-old --used 72 --window 200000'])
  expect(f.statuses).toEqual(['接力等待核准中'])
  await clock.advance(50)
  expect(f.argvs.map(sub).at(-1)).toBe('relay wait req-1')
  expect(f.toasts).toEqual(['接力等待核准：請在 Purdex App 按核准或拒絕'])
})

test('below the threshold nothing is asked', async ($, on) => {
  const f = world(on, { pdx: pdxWith([]), usage: { tokens: 100000, window: 200000, percent: 50 } })
  await start($)
  await turn($, 't1')
  expect(f.argvs.length).toBe(1) // hello only
})

test('PDX_RELAY_THRESHOLD lowers the threshold for an acceptance run', async ($, on) => {
  const f = world(on, { pdx: pdxWith([]), usage: { tokens: 12000, window: 200000, percent: 6 } }, { PDX_RELAY_THRESHOLD: '5' })
  await start($)
  await turn($, 't1')
  expect(f.argvs.map((a) => a[2])).toContain('begin')
})

test('a member does not self-relay', async ($, on) => {
  const f = world(on, { pdx: pdxWith([], 'member'), usage: { tokens: 180000, window: 200000, percent: 90 } })
  await start($)
  await turn($, 't1')
  expect(f.argvs.length).toBe(1)
})

test('self_relay_off / self_relay_paused (exit 13) are respected: no wait, idle, asks again only at +10', async ($, on) => {
  const f = world(on, { usage: { tokens: 144000, window: 200000, percent: 72 } })
  f.pdx = (argv) => argv[1] === 'begin' ? { exitCode: 13, stderr: 'pdx relay: self_relay_paused' } : { exitCode: 0, stdout: HELLO() }
  await start($)
  await turn($, 't1')
  expect(f.argvs.filter((a) => a[2] === 'begin').length).toBe(1)
  expect(f.argvs.filter((a) => a[2] === 'wait').length).toBe(0)
  expect(f.statuses).toEqual([])
  f.usage = { tokens: 150000, window: 200000, percent: 79 }
  await turn($, 't2')
  expect(f.argvs.filter((a) => a[2] === 'begin').length).toBe(1)
  f.usage = { tokens: 164000, window: 200000, percent: 82 }
  await turn($, 't3')
  expect(f.argvs.filter((a) => a[2] === 'begin').length).toBe(2)
})

test('daemon unreachable (exit 20) at begin: nothing, no wait, no status', async ($, on) => {
  const f = world(on, { usage: { tokens: 144000, window: 200000, percent: 72 } })
  f.pdx = (argv) => argv[1] === 'begin' ? { exitCode: 20, stderr: 'daemon unavailable' } : { exitCode: 0, stdout: HELLO() }
  await start($)
  await turn($, 't1')
  expect(f.argvs.filter((a) => a[2] === 'wait').length).toBe(0)
  expect(f.statuses).toEqual([])
})

// ---------- P5b-2: wait → write → check → clear → seed → done ----------

async function approvedRelay($: any, on: any, waits = [{ exitCode: 0, stdout: APPROVAL('approved') }]) {
  const f = world(on, { pdx: pdxWith(waits), usage: { tokens: 144000, window: 200000, percent: 72 } })
  const clock = f.clock
  await start($)
  await turn($, 't1')
  await clock.advance(50) // the wait timer
  await clock.advance(50) // the write-prompt timer after approval
  return { f, clock }
}

test('wait loops on a still-open answer and, once approved, submits the write prompt (8 sections, op nonce) and reports writing', async ($, on) => {
  const { f } = await approvedRelay($, on, [{ exitCode: 0, stdout: APPROVAL('open') }, { exitCode: 0, stdout: APPROVAL('open') }, { exitCode: 0, stdout: APPROVAL('approved') }])
  expect(f.argvs.filter((a) => a[2] === 'wait').length).toBe(3)
  expect(f.submits.length).toBe(1)
  const text = f.submits[0].text as string
  expect(text.startsWith('[pdx-relay op=op-1]')).toBe(true)
  expect(text).toContain('/data/relay/op-1.md')
  for (const h of ['## 1.', '## 2.', '## 3.', '## 4.', '## 5.', '## 6.', '## 7.', '## 8.']) expect(text).toContain(h)
  expect(text).toContain('接力檔')
  expect(text).not.toContain('交接')
  expect(f.argvs.map(sub)).toContain('relay report op-1 writing')
  expect(f.statuses).toEqual(['接力等待核准中', undefined])
})

test('the write turn is recognised by its own turn: a queued prompt that runs first does not trigger the check', async ($, on) => {
  const { f, clock } = await approvedRelay($, on)
  f.files['/data/relay/op-1.md'] = GOOD_FILE
  await $.turn.start({ text: 'the user’s queued prompt', turnId: 'tq' })
  await turn($, 'tq')
  expect(f.argvs.map(sub)).not.toContain('relay report op-1 written')
  expect(f.commands).toEqual([])
  await $.turn.start({ text: 'The purdex plugin sent a message: ' + f.submits[0].text, turnId: 'tw' })
  await turn($, 'tw')
  expect(f.argvs.map(sub)).toContain('relay report op-1 written')
  expect(f.commands).toEqual([])
  await clock.advance(50)
  expect(f.commands).toEqual(['clear'])
})

test('file check: missing headings get two fix rounds, then failed{handoff_incomplete} and no clear', async ($, on) => {
  const { f, clock } = await approvedRelay($, on)
  f.files['/data/relay/op-1.md'] = '# HANDOFF\n## 1. a\n' + 'x'.repeat(300)
  await $.turn.start({ text: f.submits[0].text, turnId: 'tw' })
  await turn($, 'tw')
  await clock.advance(50)
  expect(f.submits.length).toBe(2)
  expect(f.submits[1].text).toContain('不完整，缺少段落：## 2.、## 3.')
  await $.turn.start({ text: f.submits[1].text, turnId: 'tf1' })
  await turn($, 'tf1')
  await clock.advance(50)
  expect(f.submits.length).toBe(3)
  await $.turn.start({ text: f.submits[2].text, turnId: 'tf2' })
  await turn($, 'tf2')
  await clock.advance(50)
  expect(f.submits.length).toBe(3)
  expect(f.argvs.map(sub)).toContain('relay report op-1 failed --error handoff_incomplete')
  expect(f.commands).toEqual([])
  expect(f.toasts).toContain('接力檔不完整，已放棄接力；對話照常繼續')
})

test('a short file (≤ 200 chars) with all headings is incomplete too', async ($, on) => {
  const { f, clock } = await approvedRelay($, on)
  f.files['/data/relay/op-1.md'] = ['## 1.', '## 2.', '## 3.', '## 4.', '## 5.', '## 6.', '## 7.', '## 8.'].join('\n')
  await $.turn.start({ text: f.submits[0].text, turnId: 'tw' })
  await turn($, 'tw')
  await clock.advance(50)
  expect(f.submits[1].text).toContain('(內容過短)')
})

test('cleared: report cleared --new-session, hello again, seed prompt ↪ 接手自 <old ref>; the seed turn reports done and sets the floor', async ($, on) => {
  const { f, clock } = await approvedRelay($, on)
  f.files['/data/relay/op-1.md'] = GOOD_FILE
  await $.turn.start({ text: f.submits[0].text, turnId: 'tw' })
  await turn($, 'tw')
  await clock.advance(50)
  expect(f.commands).toEqual(['clear'])
  f.sessionId = 'sid-new'
  await $.classic.SessionStart({ source: 'clear' })
  const calls = f.argvs.map(sub)
  expect(calls).toContain('relay report op-1 cleared --new-session sid-new')
  expect(calls.filter((c) => c.startsWith('relay hello')).at(-1)).toBe('relay hello --session sid-new --version 1 --agent cc')
  await clock.advance(50)
  expect(f.submits.length).toBe(2)
  expect(f.submits[1].text.split('\n')[0]).toBe('↪ 接手自 _abc123')
  expect(f.submits[1].text).toContain('[pdx-relay seed op=op-1]')
  f.usage = { tokens: 30000, window: 200000, percent: 15 }
  await $.turn.start({ text: f.submits[1].text, turnId: 'ts' })
  await turn($, 'ts')
  expect(f.argvs.map(sub)).toContain('relay report op-1 done')
  // the 20K loop guard: 75 % but only 10K over the floor → no new ask
  f.usage = { tokens: 40000, window: 200000, percent: 75 }
  await turn($, 't9')
  expect(f.argvs.filter((a) => a[2] === 'begin').length).toBe(1)
  f.usage = { tokens: 50000, window: 200000, percent: 75 }
  await turn($, 't10')
  expect(f.argvs.filter((a) => a[2] === 'begin').length).toBe(2)
})

test('a failed report is re-sent at the next turn.complete and the relay goes on', async ($, on) => {
  const { f, clock } = await approvedRelay($, on)
  let failWriting = true
  const inner = f.pdx
  f.pdx = (argv) => (argv[1] === 'report' && argv[3] === 'written' && failWriting ? { exitCode: 20 } : inner(argv))
  f.files['/data/relay/op-1.md'] = GOOD_FILE
  await $.turn.start({ text: f.submits[0].text, turnId: 'tw' })
  await turn($, 'tw')
  await clock.advance(50)
  expect(f.commands).toEqual(['clear']) // relaying went on despite the failed report
  failWriting = false
  await turn($, 'tx')
  expect(f.argvs.map(sub).filter((c) => c === 'relay report op-1 written').length).toBe(2)
})

test('a report refused with 13 bad_transition is dropped, not re-sent (the daemon is already past it)', async ($, on) => {
  const { f, clock } = await approvedRelay($, on)
  const inner = f.pdx
  f.pdx = (argv) => (argv[1] === 'report' && argv[3] === 'written' ? { exitCode: 13, stderr: 'pdx relay: state cleared does not lead to written bad_transition' } : inner(argv))
  f.files['/data/relay/op-1.md'] = GOOD_FILE
  await $.turn.start({ text: f.submits[0].text, turnId: 'tw' })
  await turn($, 'tw')
  await clock.advance(50)
  expect(f.commands).toEqual(['clear'])
  await turn($, 'tx')
  await turn($, 'ty')
  expect(f.argvs.map(sub).filter((c) => c === 'relay report op-1 written').length).toBe(1) // sent once, never queued
})

test('tool.check allows Write/Edit to exactly the handoff path while an op is pending, nothing else', async ($, on) => {
  const { f } = await approvedRelay($, on)
  expect((await $.tool.check({ tool: 'Write', input: { file_path: '/data/relay/op-1.md', content: 'x' } })).decision).toBe('allow')
  expect((await $.tool.check({ tool: 'Edit', input: { file_path: '/data/relay/op-1.md', old_string: 'a', new_string: 'b' } })).decision).toBe('allow')
  expect((await $.tool.check({ tool: 'Write', input: { file_path: '/data/relay/op-2.md', content: 'x' } })).decision).toBe('ask')
  expect((await $.tool.check({ tool: 'Bash', input: { command: 'rm -rf /' } })).decision).toBe('ask')
  void f
})

// The hello after /clear is P5b-1's (its own test there); this one pins that the
// relay state resets on the user's own /clear and the hello still goes out.
test('the user’s own /clear while idle resets the guards and says hello again', async ($, on) => {
  const f = world(on, { pdx: pdxWith([]), usage: { tokens: 1000, window: 200000, percent: 1 } })
  await start($)
  f.sessionId = 'sid-2'
  await $.classic.SessionStart({ source: 'clear' })
  expect(f.argvs.map(sub)).toEqual(['relay hello --session sid-old --version 1 --agent cc', 'relay hello --session sid-2 --version 1 --agent cc'])
})
```

- [ ] **Step 2: Run them and see them fail.**
  - Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/lead-team-p5b && claude plugin test cmd/pdx/plugin/purdex`
  - Expected (measured with the P5b-1 `register.js` in place): `6 pass` (`hello…`, `headless…`, `below the threshold…`, `a member does not self-relay`, `daemon unreachable…`, `the user’s own /clear … says hello again` — the hello-only mod trivially does nothing, and its P5b-1 `/clear` hello already satisfies the last) and `10 fail`, the first of them `begin at used ≥ 70…` with `AssertionError … Expected: ["relay begin --self --session sid-old --used 72 --window 200000 --model claude-opus-5-5"] Received: []`.

- [ ] **Step 3: Implement** — replace `cmd/pdx/plugin/purdex/hooks/register.js` with:

```js
// Purdex mod — self relay (spec §8.1–§8.3 steps 4–8, §8.7).
//
//   idle ──(turn.complete: used ≥ threshold, growth ≥ minGrowth, +10 since last ask)──▶ awaiting
//   awaiting: `pdx relay begin --self` opened a request; a timer loops `pdx relay wait`;
//             every prompt.submit waits on that loop (P5b-3)
//   awaiting ──approved──▶ approved: write prompt submitted (nonce = op id), report writing
//   approved ──(turn.complete of the write turn, file ok)──▶ clearing: report written, timer → /clear
//   clearing ──(classic.SessionStart source=clear)──▶ seeding: report cleared --new-session, hello, seed prompt
//   seeding ──(turn.complete of the seed turn)──▶ idle: report done, floor = tokens now
//   awaiting ──denied / timeout / cancelled / unavailable──▶ idle (ask again at +10 points)
//
// Everything that starts a turn or a command runs from a $.clock.after timer, never
// inside the hook a turn waits on (F3; $.command.run rejects there).

const VERSION = '1'
const DEFAULT_THRESHOLD = 70
const DEFAULT_MIN_GROWTH = 20000
const REASK_POINTS = 10
const MAX_FIX_ROUNDS = 2
const WAIT_TIMEOUT_MS = 590_000 // $.process.run caps at 10 min (M24); pdx relay wait bounds itself to 9
const CALL_TIMEOUT_MS = 35_000 // one daemonclient grace (30 s) plus slack
const REQUIRED = ['## 1.', '## 2.', '## 3.', '## 4.', '## 5.', '## 6.', '## 7.', '## 8.']
const STATUS_WAITING = '接力等待核准中'
const TOAST_WAITING = '接力等待核准：請在 Purdex App 按核准或拒絕'

const s = {
  interactive: false,
  envThreshold: false,
  pdx: 'pdx',
  threshold: DEFAULT_THRESHOLD,
  minGrowth: DEFAULT_MIN_GROWTH,
  role: 'none',
  state: 'idle',
  pending: undefined, // { op, requestId, path, oldSession, oldRef, before, nonce, seedNonce }
  lastAskPct: undefined,
  floor: undefined,
  fixRounds: 0,
  writeTurnId: undefined,
  seedTurnId: undefined,
  waitPromise: undefined,
  toasted: false,
  reportQueue: [], // reports that failed; re-sent at the next turn.complete
}

function resetState() {
  Object.assign(s, { interactive: false, envThreshold: false, pdx: 'pdx', threshold: DEFAULT_THRESHOLD, minGrowth: DEFAULT_MIN_GROWTH, role: 'none', state: 'idle', pending: undefined, lastAskPct: undefined, floor: undefined, fixRounds: 0, writeTurnId: undefined, seedTurnId: undefined, waitPromise: undefined, toasted: false, reportQueue: [] })
}

function later($, fn) {
  $.clock.after(50, () => fn().catch((err) => $.ui.log('pdx-relay: deferred call failed: ' + String(err))))
}

async function run($, argv, timeoutMs) {
  try {
    return await $.process.run([s.pdx, ...argv], { timeoutMs })
  } catch (err) {
    return { exitCode: 20, stdout: '', stderr: String(err) }
  }
}

function parseJSON(text) {
  try { return JSON.parse(text) } catch { return undefined }
}

function stderrCode(r) {
  return (r.stderr || '').trim().split(/\s+/).pop() || ''
}

async function hello($) {
  const sid = await $.session.id()
  const r = await run($, ['relay', 'hello', '--session', sid, '--version', VERSION, '--agent', 'cc'], CALL_TIMEOUT_MS)
  if (r.exitCode !== 0) return
  const h = parseJSON(r.stdout)
  if (!h) return
  if (h.role) s.role = h.role
  if (!s.envThreshold && h.threshold > 0) s.threshold = h.threshold
  if (h.min_growth > 0) s.minGrowth = h.min_growth
}

// retryableReport: a report that did not reach the daemon (20 unreachable,
// 21 unsupported, 1 runtime) is re-sent at the next turn.complete (§8.3).
// Exit 13 (`bad_transition`) means the daemon is already PAST this state —
// a later report landed first, or the op was closed — so re-sending it
// would be refused forever; it is dropped.
function retryableReport(r) {
  return r.exitCode !== 0 && r.exitCode !== 13
}

async function report($, state, extra = []) {
  if (!s.pending) return
  const argv = ['relay', 'report', s.pending.op.id, state, ...extra]
  const r = await run($, argv, CALL_TIMEOUT_MS)
  if (retryableReport(r)) s.reportQueue.push(argv) // keep relaying; re-send later (§8.3)
}

async function flushReports($) {
  const queue = s.reportQueue
  s.reportQueue = []
  for (const argv of queue) {
    const r = await run($, argv, CALL_TIMEOUT_MS)
    if (retryableReport(r)) s.reportQueue.push(argv)
  }
}

function writePrompt(p) {
  return [
    '[pdx-relay op=' + p.op.id + '] 這個 session 的 context 已達接力門檻，使用者已核准接力（之後會 /clear）。',
    '請先停下手邊工作，用你完整的工具撰寫接力檔：' + p.path,
    '',
    '要求：',
    '- 自己跑 `git status`、`git diff --stat`、`git log --oneline -10` 取得檔案狀態，不要憑記憶寫。',
    '- 接力檔必須自成一體：讀它的是一個完全沒有這段對話記憶的新對話。',
    '- 寫完後只回一行「HANDOFF-WRITTEN」，不要繼續原本的工作。',
    '',
    '格式（每一段都要有，沒有內容就寫「無」）：',
    '# HANDOFF',
    '## 1. 目標與完成定義（使用者要的是什麼、怎樣算完成、範圍外）',
    '## 2. 進度（已完成且驗證 / 進行中停在哪 / 下一步第一個動作具體到指令）',
    '## 3. 檔案異動（git status 與 diff --stat 的結果，加上每個檔案的用途）',
    '## 4. 決策紀錄（選了什麼、為什麼、否決了什麼）',
    '## 5. 死路（試過失敗、不要再試的）',
    '## 6. 環境與指令（測試 / 執行方式）',
    '## 7. 未決問題與需要使用者決定的事',
    '## 8. 協作關係（下面的 pdx 身分；我的 lead 與我管理的 members，沒有就寫無）',
    '',
    '機器提供的事實（請照抄進對應段落）：',
    '- 舊 session id：' + p.oldSession,
    '- 舊 ref：' + p.oldRef,
    '- 接力時 context：' + p.before,
    '- pdx 身分：' + p.who,
  ].join('\n')
}

function fixPrompt(p, missing) {
  return '[pdx-relay op=' + p.op.id + '] 接力檔 ' + p.path + ' 不完整，缺少段落：' + (missing.join('、') || '(內容過短)') + '。請補齊後只回「HANDOFF-WRITTEN」。'
}

function seedPrompt(p) {
  return [
    '↪ 接手自 ' + p.oldRef,
    '[pdx-relay seed op=' + p.op.id + '] 你是接手的新對話：前一段對話 context 已滿並已清空。',
    '請先讀接力檔 ' + p.path + '，然後：',
    '1. 用三行複述：目標、下一步第一個動作、目前有哪些檔案異動。',
    '2. 跑 `git status` 確認與接力檔一致，不一致就指出來。',
    '3. 接著從「下一步」繼續原本的工作。',
    '回覆的第一行請寫「↪ 接手自 ' + p.oldRef + '」。',
  ].join('\n')
}

function usageLine(u) {
  return (u.tokens ?? '?') + ' tokens / ' + u.window + ' (' + (u.percent ?? '?') + '%)'
}

async function whoami($) {
  const r = await run($, ['msg', 'whoami'], 10_000)
  return (r.stdout || '').trim().replace(/\n/g, ' | ') || '(unknown)'
}

// begin opens the self-relay request. Called at a turn's end, state idle.
async function begin($, u) {
  s.lastAskPct = u.percent
  const sid = await $.session.id()
  const argv = ['relay', 'begin', '--self', '--session', sid, '--used', String(u.percent), '--window', String(u.window)]
  const r = await run($, argv, CALL_TIMEOUT_MS)
  if (r.exitCode === 13) {
    if (stderrCode(r) === 'member_relay_is_leads') s.role = 'member'
    return // self_relay_off | self_relay_paused | relay_open: nothing (§8.1); ask again at +10
  }
  if (r.exitCode !== 0) return // 20 unreachable, 21 unsupported, 1: nothing (§8.7 (d))
  const body = parseJSON(r.stdout)
  if (!body || !body.op || !body.request_id) return
  s.pending = {
    op: body.op,
    requestId: body.request_id,
    path: body.op.handoff_path,
    oldSession: sid,
    oldRef: body.op.ref,
    before: usageLine(u),
    nonce: '[pdx-relay op=' + body.op.id + ']',
    seedNonce: '[pdx-relay seed op=' + body.op.id + ']',
    who: '',
  }
  s.state = 'awaiting'
  s.fixRounds = 0
  s.toasted = false
  $.ui.status(STATUS_WAITING)
  later($, () => waitLoop($).then(() => {}))
}

// waitLoop is the one long-poll loop per request. It runs in a timer's own
// dispatch, so a held prompt that is abandoned (Esc) never kills it; hooks
// only await the promise it returns.
function waitLoop($) {
  if (s.waitPromise) return s.waitPromise
  const p = s.pending
  s.waitPromise = (async () => {
    if (!s.toasted) { s.toasted = true; $.ui.toast(TOAST_WAITING) }
    for (;;) {
      const r = await run($, ['relay', 'wait', p.requestId], WAIT_TIMEOUT_MS)
      if (r.exitCode === 0) {
        // P5a-2c's shape: exit 0 + Approval JSON, state 'approved' or 'open'
        // (the CLI's own 9 min bound ran out: ask again). Anything else at
        // exit 0 — empty or unparsable stdout, an unknown state — is NOT an
        // approval: never start the write turn on it (§8.7 (d)).
        const a = parseJSON(r.stdout)
        if (a && a.state === 'open') continue
        if (a && a.state === 'approved') return 'approved'
        return 'unavailable'
      }
      if (r.exitCode === 10) return 'denied'
      if (r.exitCode === 11) return 'timeout'
      if (r.exitCode === 12) return 'cancelled'
      return 'unavailable' // 20, 21, 1: treat as not approved (§8.7 (d))
    }
  })().then((outcome) => { settle($, outcome); return outcome })
  return s.waitPromise
}

function settle($, outcome) {
  s.waitPromise = undefined
  $.ui.status(undefined)
  if (s.state !== 'awaiting' || !s.pending) return
  if (outcome !== 'approved') {
    s.state = 'idle'
    s.pending = undefined
    return
  }
  s.state = 'approved'
  later($, async () => {
    s.pending.who = await whoami($)
    await $.prompt.submit({ text: writePrompt(s.pending) })
    await report($, 'writing')
  })
}

async function checkHandoff($) {
  const text = await $.fs.read(s.pending.path).catch(() => '')
  const missing = REQUIRED.filter((h) => !text.includes(h))
  return { ok: text.length > 200 && missing.length === 0, missing, size: text.length }
}

async function onWriteTurnDone($) {
  const c = await checkHandoff($)
  if (c.ok) {
    s.state = 'clearing'
    await report($, 'written')
    later($, () => $.command.run({ command: 'clear' }))
    return
  }
  if (s.fixRounds < MAX_FIX_ROUNDS) {
    s.fixRounds += 1
    const p = s.pending
    later($, () => $.prompt.submit({ text: fixPrompt(p, c.missing) }))
    return
  }
  await report($, 'failed', ['--error', 'handoff_incomplete'])
  $.ui.toast('接力檔不完整，已放棄接力；對話照常繼續')
  s.state = 'idle'
  s.pending = undefined
  s.writeTurnId = undefined
}

async function onSeedTurnDone($) {
  await report($, 'done')
  s.floor = (await $.session.usage()).context.tokens
  s.state = 'idle'
  s.pending = undefined
  s.seedTurnId = undefined
  s.lastAskPct = undefined
}

async function maybeBegin($) {
  if (s.role === 'member') return
  const u = (await $.session.usage()).context
  if (u.percent === undefined || u.percent < s.threshold) return
  if (s.floor !== undefined && (u.tokens ?? 0) < s.floor + s.minGrowth) return
  if (s.lastAskPct !== undefined && u.percent < s.lastAskPct + REASK_POINTS) return
  await begin($, u)
}

export function register(on) {
  on('session.start', async ($, e, next) => {
    resetState()
    s.interactive = !!e.isInteractive
    if (!s.interactive) return next(e)
    const t = Number(await $.env.get('PDX_RELAY_THRESHOLD'))
    if (t > 0 && t <= 100) { s.threshold = t; s.envThreshold = true }
    const cfg = parseJSON(await $.fs.read($.plugin.root + '/pdx.json').catch(() => ''))
    if (cfg && cfg.pdx) s.pdx = cfg.pdx
    await hello($)
    return next(e)
  })

  on('turn.start', async ($, e, next) => {
    if (s.interactive && s.pending) {
      if (e.text.includes(s.pending.nonce)) s.writeTurnId = e.turnId
      else if (e.text.includes(s.pending.seedNonce)) s.seedTurnId = e.turnId
    }
    return next(e)
  })

  on('turn.complete', async ($, e, next) => {
    const r = await next(e)
    if (!s.interactive || e.agentId) return r
    try {
      if (s.reportQueue.length) await flushReports($)
      if (s.pending && e.turnId === s.writeTurnId) await onWriteTurnDone($)
      else if (s.pending && e.turnId === s.seedTurnId) await onSeedTurnDone($)
      else if (s.state === 'idle') await maybeBegin($)
    } catch (err) {
      $.ui.log('pdx-relay: turn.complete failed: ' + String(err))
    }
    return r
  })

  on('classic.SessionStart', async ($, e, next) => {
    const r = await next(e)
    if (!s.interactive || e.source !== 'clear') return r
    if (s.state === 'clearing' && s.pending) {
      const newSession = await $.session.id()
      s.state = 'seeding'
      await report($, 'cleared', ['--new-session', newSession])
      await hello($)
      const p = s.pending
      later($, () => $.prompt.submit({ text: seedPrompt(p) }))
      return r
    }
    // the user's own /clear: start over; the hello under the new session id is P5b-1's rule, kept here
    s.state = 'idle'; s.pending = undefined; s.floor = undefined; s.lastAskPct = undefined
    await hello($)
    return r
  })

  on('tool.check', { tool: 'Write' }, async ($, e, next) => {
    if (s.pending && e.input && e.input.file_path === s.pending.path) return { decision: 'allow', reason: 'Purdex 接力檔' }
    return next(e)
  }).catch(($, e, next) => (next.called ? next(e) : { decision: 'deny', reason: 'pdx-relay guard failed' }))

  on('tool.check', { tool: 'Edit' }, async ($, e, next) => {
    if (s.pending && e.input && e.input.file_path === s.pending.path) return { decision: 'allow', reason: 'Purdex 接力檔' }
    return next(e)
  }).catch(($, e, next) => (next.called ? next(e) : { decision: 'deny', reason: 'pdx-relay guard failed' }))
}
```

- [ ] **Step 4: Run and see them pass.**
  - Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/lead-team-p5b && claude plugin test cmd/pdx/plugin/purdex` → `16 pass`, `0 fail` (measured 0.36 s)
  - Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/lead-team-p5b && claude plugin validate --strict cmd/pdx/plugin/purdex` → `✔ Validation passed`; the report lists `hooks: session.start, turn.start, turn.complete, classic.SessionStart, tool.check{tool=Write}, tool.check{tool=Edit}`, `gating hook with .catch: tool.check{tool=Write}`, `…{tool=Edit}`, `env reads: PDX_RELAY_THRESHOLD`.
  - Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/lead-team-p5b && go test ./cmd/pdx/plugin/` → `PASS` (the embed test still finds every file).

- [ ] **Step 5: Mutation gate (spec §15, run once, then revert):** change `if (s.pending && e.turnId === s.writeTurnId) await onWriteTurnDone($)` to `if (s.pending && s.state === 'approved') await onWriteTurnDone($)` → `claude plugin test` reports exactly one fail: `the write turn is recognised by its own turn: a queued prompt that runs first does not trigger the check`. Revert.

- [ ] **Step 6: Commit.**
  ```bash
  git add cmd/pdx/plugin/purdex/hooks/register.js cmd/pdx/plugin/purdex/hooks/relay.test.ts
  git commit -m "feat(mod): self relay core — begin, wait, write, check, clear, seed, report

  Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
  ```

---

## PR P5b-3 — the hold, NOTE, re-ask, the compact rule, `/relay`, the skill (spec §8.7 (b)(c)(d), §10, §15)

**Scope.** Three hooks appended to `register.js` (`prompt.submit`, `session.compact`, `command.run{relay}`) plus one line in `session.start` (`$.command.register`) and the NOTE constant; twelve tests appended to `relay.test.ts` (ten measured in the scratch build plus the two manual-`/compact` tests added at the consistency fix, unmeasured); the §10 skill text; the acceptance recipe. **Needs merged first:** P5b-2 (whose `relay.test.ts` already has the exit-13 drop test). **It relies on P5a-2b's `handleRelayReport` closing the approval row when a report moves the op to `cancelled`** (`closeRequestOfReportedOp`) — without it the dialogs would stay up for 10 minutes after a compaction.

### Task 5b.5: `prompt.submit` hold with NOTE, `session.compact`, `/relay`

**Files:**
- Modify: `cmd/pdx/plugin/purdex/hooks/register.js` (add one const, one line in `session.start`, three hooks before the closing brace of `register`)
- Modify: `cmd/pdx/plugin/purdex/hooks/relay.test.ts` (append; one line added to `world()`)
- Test: `claude plugin test cmd/pdx/plugin/purdex`

**Interfaces:**
- Consumes: `prompt.submit` `e.context` / `e.origin` (d.ts:8740-8790); `PromptSubmitResult.context` "what entered beside the prompt for the model, never shown the user" (d.ts:8793-8812); `session.compact` `trigger` `'manual' | 'auto' | 'plugin' | 'precompute'` and `{ skip }` (d.ts:10464-10530); `$.command.register` / `command.run{command:'relay'}` with `e.args` (d.ts:1743-1788, 3012-3023); `pdx relay self off|on|status --session <sid>` → `{self_relay: "on"|"off"|"paused", host_switch: bool, member: bool}`.
- Produces:
  - **The hold (U7, §8.7 (b)):** while `s.state === 'awaiting'`, every `prompt.submit` of the main conversation, whatever its origin (typed, peer, bridge, another plugin — the mod's own never reaches this hook, MP3), awaits the shared `waitLoop($)` promise. **Approved** ⇒ `next({ ...e, context: [...(e.context ?? []), NOTE] })` with NOTE exactly:
    > 接力已核准，這一輪只做簡短回應；如果這是一件新工作，不要開始做，把它寫進接力檔「下一步」的第一項，由接手後的新對話處理。

    **Denied / timeout / cancelled / daemon gone** ⇒ `next(e)` unchanged. The loop runs in the timer's dispatch (P5b-2), so an abandoned prompt dispatch (Esc) does not end it and the request stays open (§8.7 "Esc"); the status line and toast are the loop's, not the hook's, so "no prompt arrives" and "a prompt arrives" look the same.
  - **Asking again (§8.7 (c)):** `begin` records `lastAskPct`; `maybeBegin` needs `percent ≥ lastAskPct + 10`. A seed clears it; `/relay on` clears it; a compaction clears it ("after a compaction, the next ask needs usage ≥ 70 % again").
  - **Compact (§8.7 (c), coordinator decision):** `trigger` `precompute` or a subagent's ⇒ `next(e)`. `s.state === 'approved'` (approved, not yet written) **and `trigger === 'auto'`** ⇒ `{ skip: '接力已核准，略過壓縮，改為寫接力檔' }`; the same state on a **manual** `/compact` ⇒ `next(e)` (the person asked for it; nothing is reported). `s.state === 'awaiting'`, **any trigger** (manual included) ⇒ `pdx relay report <op> cancelled --error compacted`, state idle, then `next(e)` — the daemon moves the op **and closes the approval row** (P5a-2b `handleRelayReport` → `closeRequestOfReportedOp`, the same CAS as a cancel): every client's dialog closes on one `closed` event, the wait loop ends with exit 12 and releases any held prompt. Anything else ⇒ `next(e)`.
  - **`/relay off|on|status`:** runs `pdx relay self <action> --session <sid>`; `member: true` ⇒ text `member 的接力由 lead 安排` (spec §8.7 (a)); otherwise `自我接力：開啟|關閉|本 session 暫停（主機開關 開|關；門檻 NN%）`; exit 20 ⇒ `Purdex daemon 連不上，無法變更自我接力`; bad args ⇒ `用法：/relay off|on|status`.

- [ ] **Step 1: Write the failing tests** — in `relay.test.ts`, add to `world()` right after the `on('ui.log', …)` line:

```ts
  on('command.register', async (_$: any, e: any) => ({ value: { command: e.name } }))
```

and append at the end of the file:

```ts
// ---------- P5b-3: the hold, NOTE, denial, +10, compact, /relay ----------

test('a prompt that arrives while a request is open waits; on approval it runs in the current conversation with NOTE appended after existing context, and the write prompt follows', async ($, on) => {
  let release!: (v: any) => void
  const gate = new Promise<any>((r) => { release = r })
  const f = world(on, { usage: { tokens: 144000, window: 200000, percent: 72 } })
  const clock = f.clock
  f.pdx = async (argv) => {
    if (argv[1] === 'hello') return { exitCode: 0, stdout: HELLO() }
    if (argv[1] === 'begin') return { exitCode: 0, stdout: BEGIN_OK }
    if (argv[1] === 'wait') return gate
    return { exitCode: 0, stdout: '{}' }
  }
  await start($)
  await turn($, 't1')
  await clock.advance(50)
  const p = $.prompt.submit({ text: '請幫我看一下', context: ['prior'], wait: false, origin: { kind: 'composer' } })
  await clock.settle()
  expect(f.submits.length).toBe(0) // held
  release({ exitCode: 0, stdout: APPROVAL('approved') })
  await p
  expect(f.submits.length).toBe(1)
  expect(f.submits[0].text).toBe('請幫我看一下')
  expect(f.submits[0].context).toEqual(['prior', '接力已核准，這一輪只做簡短回應；如果這是一件新工作，不要開始做，把它寫進接力檔「下一步」的第一項，由接手後的新對話處理。'])
  await clock.advance(50)
  expect(f.submits.length).toBe(2)
  expect(f.submits[1].text.startsWith('[pdx-relay op=op-1]')).toBe(true)
  expect(f.argvs.filter((a) => a[2] === 'wait').length).toBe(1) // one loop served both the timer and the held prompt
})

test('on denial the held prompt passes unchanged (no NOTE); the request is gone; asking again only at +10 points', async ($, on) => {
  const f = world(on, { pdx: pdxWith([{ exitCode: 10 }]), usage: { tokens: 144000, window: 200000, percent: 72 } })
  const clock = f.clock
  await start($)
  await turn($, 't1')
  const p = $.prompt.submit({ text: 'hi', wait: false, origin: { kind: 'peer' } })
  await clock.advance(50)
  await p
  expect(f.submits.length).toBe(1)
  expect(f.submits[0].context).toBeUndefined()
  expect(f.statuses).toEqual(['接力等待核准中', undefined])
  f.usage = { tokens: 160000, window: 200000, percent: 80 }
  await turn($, 't2')
  expect(f.argvs.filter((a) => a[2] === 'begin').length).toBe(1)
  f.usage = { tokens: 164000, window: 200000, percent: 82 }
  await turn($, 't3')
  expect(f.argvs.filter((a) => a[2] === 'begin').length).toBe(2)
})

for (const code of [11, 20]) {
  test('on exit ' + code + ' (timeout / daemon gone) the hold releases unchanged', async ($, on) => {
    const f = world(on, { pdx: pdxWith([{ exitCode: code }]), usage: { tokens: 144000, window: 200000, percent: 72 } })
    await start($)
    await turn($, 't1')
    const p = $.prompt.submit({ text: 'hi', wait: false, origin: { kind: 'composer' } })
    await f.clock.advance(50)
    await p
    expect(f.submits.length).toBe(1)
    expect(f.submits[0].context).toBeUndefined()
  })
}

test('a prompt submitted while nothing is open passes straight through', async ($, on) => {
  const f = world(on)
  await start($)
  await $.prompt.submit({ text: 'hi', wait: false, origin: { kind: 'composer' } })
  expect(f.submits.length).toBe(1)
  expect(f.argvs.filter((a) => a[2] === 'wait').length).toBe(0)
})

test('auto-compact while a request is open: compaction runs and the request is reported cancelled{compacted}; the next ask needs ≥ threshold again', async ($, on) => {
  const f = world(on, { pdx: pdxWith([{ exitCode: 12 }]), usage: { tokens: 144000, window: 200000, percent: 72 } })
  const clock = f.clock
  await start($)
  await turn($, 't1')
  const r = await compact($, 'auto')
  expect(r).toEqual({ messages: MSGS })
  expect(f.argvs.map(sub)).toContain('relay report op-1 cancelled --error compacted')
  await clock.advance(50)
  f.usage = { tokens: 142000, window: 200000, percent: 71 }
  await turn($, 't2')
  expect(f.argvs.filter((a) => a[2] === 'begin').length).toBe(2) // 71 ≥ 70 is enough after a compaction
})

test('auto-compact with an approved relay not yet written is skipped', async ($, on) => {
  const { f } = await approvedRelay($, on)
  const r = await compact($, 'auto')
  expect(r).toEqual({ skip: '接力已核准，略過壓縮，改為寫接力檔' })
  expect(f.argvs.map(sub)).not.toContain('relay report op-1 cancelled --error compacted')
})

test('manual /compact with an approved relay not yet written runs (the person asked) and reports nothing', async ($, on) => {
  const { f } = await approvedRelay($, on)
  const r = await compact($, 'manual')
  expect(r).toEqual({ messages: MSGS })
  expect(f.argvs.filter((a) => a[2] === 'report').length).toBe(0)
})

test('manual /compact while a request is open cancels it like an auto one', async ($, on) => {
  const f = world(on, { pdx: pdxWith([{ exitCode: 12 }]), usage: { tokens: 144000, window: 200000, percent: 72 } })
  await start($)
  await turn($, 't1')
  expect(await compact($, 'manual')).toEqual({ messages: MSGS })
  expect(f.argvs.map(sub)).toContain('relay report op-1 cancelled --error compacted')
})

test('auto-compact passes through for a member', async ($, on) => {
  const f = world(on, { pdx: pdxWith([], 'member'), usage: { tokens: 180000, window: 200000, percent: 90 } })
  await start($)
  await turn($, 't1')
  expect(await compact($, 'auto')).toEqual({ messages: MSGS })
  expect(f.argvs.filter((a) => a[2] === 'report').length).toBe(0)
})

test('auto-compact passes through for an idle solo session, and so does a precompute', async ($, on) => {
  const f = world(on)
  await start($)
  expect(await compact($, 'auto')).toEqual({ messages: MSGS })
  expect(await compact($, 'precompute')).toEqual({ messages: MSGS })
  expect(f.argvs.filter((a) => a[2] === 'report').length).toBe(0)
})

test('/relay status|off|on call pdx relay self; on resets the +10 guard; a member is refused', async ($, on) => {
  const f = world(on, { usage: { tokens: 144000, window: 200000, percent: 72 } })
  let selfBody = { self_relay: 'on', host_switch: true, member: false }
  f.pdx = (argv) => {
    if (argv[1] === 'self') return { exitCode: 0, stdout: JSON.stringify(selfBody) }
    if (argv[1] === 'begin') return { exitCode: 13, stderr: 'self_relay_paused' }
    return { exitCode: 0, stdout: HELLO() }
  }
  await start($)
  const pres = { isFullscreen: false, columns: 100 }
  const st = await $.command.run({ command: 'relay', args: 'status', origin: { kind: 'composer' }, presentation: pres })
  expect(st.text).toBe('自我接力：開啟（主機開關 開；門檻 70%）')
  expect(f.argvs.map(sub)).toContain('relay self status --session sid-old')
  selfBody = { self_relay: 'paused', host_switch: true, member: false }
  const off = await $.command.run({ command: 'relay', args: 'off', origin: { kind: 'composer' }, presentation: pres })
  expect(off.text).toBe('自我接力：本 session 暫停（主機開關 開；門檻 70%）')
  await turn($, 't1') // asks, refused 13 → lastAskPct = 72
  expect(f.argvs.filter((a) => a[2] === 'begin').length).toBe(1)
  selfBody = { self_relay: 'on', host_switch: true, member: false }
  await $.command.run({ command: 'relay', args: 'on', origin: { kind: 'composer' }, presentation: pres })
  await turn($, 't2') // same 72 %, but /relay on cleared the guard
  expect(f.argvs.filter((a) => a[2] === 'begin').length).toBe(2)
  selfBody = { self_relay: 'off', host_switch: true, member: true }
  const m = await $.command.run({ command: 'relay', args: 'on', origin: { kind: 'composer' }, presentation: pres })
  expect(m.text).toBe('member 的接力由 lead 安排')
  const bad = await $.command.run({ command: 'relay', args: 'maybe', origin: { kind: 'composer' }, presentation: pres })
  expect(bad.text).toBe('用法：/relay off|on|status')
})
```

- [ ] **Step 2: Run them and see them fail.**
  - Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/lead-team-p5b && claude plugin test cmd/pdx/plugin/purdex`
  - Expected (measured with the P5b-2 `register.js` in place, before the two manual-compact tests were added): `22 pass`, `4 fail` — `a prompt that arrives while a request is open waits…` (fails at `expect(f.submits.length).toBe(0)`, `Received: 1`: nothing holds yet), `auto-compact while a request is open…` (no `cancelled --error compacted` report), `auto-compact with an approved relay not yet written is skipped` (gets `{ messages }`), `/relay status|off|on…` (`HooksError: no implementation for command.run`). Of the two unmeasured manual-compact tests, expect `manual /compact while a request is open cancels it…` to fail the same way (no report) and `manual /compact with an approved relay … runs` to pass already (no hook ⇒ `{ messages }`). The denial / timeout / pass-through tests are green already because without a hold a prompt passes unchanged — which is why the mutation gate below is the hold test, not those.

- [ ] **Step 3: Implement** — in `register.js`:

Add after `const TOAST_WAITING = …`:
```js
const NOTE = '接力已核准，這一輪只做簡短回應；如果這是一件新工作，不要開始做，把它寫進接力檔「下一步」的第一項，由接手後的新對話處理。'
```

In the `session.start` hook, right before `await hello($)`:
```js
    await $.command.register({ name: 'relay', description: 'Purdex 自我接力：off 暫停、on 恢復、status 查看', argumentHint: 'off|on|status' })
```

Before the closing `}` of `register(on)`, after the second `tool.check` hook:

```js
  // ---- P5b-3: the hold, the compact rule, /relay ----

  on('prompt.submit', async ($, e, next) => {
    if (!s.interactive || s.state !== 'awaiting' || !s.pending) return next(e)
    const outcome = await waitLoop($)
    if (outcome === 'approved') return next({ ...e, context: [...(e.context ?? []), NOTE] })
    return next(e)
  }).catch(($, e, next) => (next.called ? next(e) : { drop: 'pdx-relay: hold failed' }))

  on('session.compact', async ($, e, next) => {
    if (!s.interactive || e.agentId || e.trigger === 'precompute') return next(e)
    // Approved, not yet written: only an AUTO compaction is skipped (the
    // handoff is about to be written from the full context). A manual
    // /compact is what the person asked for and runs (§8.7 (c), coordinator).
    if (s.state === 'approved') {
      if (e.trigger === 'auto') return { skip: '接力已核准，略過壓縮，改為寫接力檔' }
      return next(e)
    }
    if (s.state === 'awaiting' && s.pending) {
      // Open request, any trigger (manual included): the daemon moves the op
      // to cancelled{compacted} AND closes the approval row (P5a-2b
      // handleRelayReport → closeRequestOfReportedOp): every client's
      // dialog closes, `pdx relay wait` exits 12, the held prompt is released.
      await report($, 'cancelled', ['--error', 'compacted'])
      s.state = 'idle'; s.pending = undefined
    }
    s.lastAskPct = undefined // after a compaction the next ask needs ≥ threshold again
    return next(e)
  })

  on('command.run', { command: 'relay' }, async ($, e) => {
    const action = (e.args || 'status').trim()
    if (!['off', 'on', 'status'].includes(action)) return { text: '用法：/relay off|on|status' }
    const r = await run($, ['relay', 'self', action, '--session', await $.session.id()], CALL_TIMEOUT_MS)
    if (r.exitCode === 20) return { text: 'Purdex daemon 連不上，無法變更自我接力' }
    if (r.exitCode !== 0) return { text: 'pdx relay self ' + action + ' 失敗：' + (r.stderr || '').trim() }
    const b = parseJSON(r.stdout) || {}
    if (b.member) return { text: 'member 的接力由 lead 安排' }
    if (action === 'on') s.lastAskPct = undefined
    const host = b.host_switch === false ? '主機開關 關' : '主機開關 開'
    const label = { on: '開啟', off: '關閉', paused: '本 session 暫停' }[b.self_relay] || String(b.self_relay)
    return { text: '自我接力：' + label + '（' + host + '；門檻 ' + s.threshold + '%）' }
  })
}
```

- [ ] **Step 4: Run and see them pass.**
  - Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/lead-team-p5b && claude plugin test cmd/pdx/plugin/purdex` → `26 pass`, `0 fail` (measured 0.49 s)
  - Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/lead-team-p5b && claude plugin validate --strict cmd/pdx/plugin/purdex` → `✔ Validation passed`; the report adds `prompt.submit, session.compact, command.run{command=relay}`, `answers its own command: command.run{command=relay}`, `gating hook with .catch: prompt.submit`.

- [ ] **Step 5: Mutation gate (spec §15 "dropping the prompt hold", run once, then revert):** replace the body of the `prompt.submit` hook with `return next(e)` → exactly one fail: `a prompt that arrives while a request is open waits; on approval it runs in the current conversation with NOTE appended after existing context, and the write prompt follows`. Revert.

- [ ] **Step 6: Commit.**
  ```bash
  git add cmd/pdx/plugin/purdex/hooks/register.js cmd/pdx/plugin/purdex/hooks/relay.test.ts
  git commit -m "feat(mod): hold prompts while a self-relay request is open, NOTE on release, compact rule, /relay

  Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
  ```

### Task 5b.6: The `pdx-team` skill (spec §10)

**Files:**
- Modify: `cmd/pdx/plugin/purdex/skills/pdx-team/SKILL.md` (replace the placeholder); `cmd/pdx/plugin/embed_test.go` (Step 1 appends the skill-text test)
- Test: `claude plugin validate --strict cmd/pdx/plugin/purdex`; `go test ./cmd/pdx/plugin/` (the embed test reads the file)

**Interfaces:** the text an agent reads; it names `pdx lead request` (P2b), `pdx spawn|kill|team` (P4, not yet shipped — the skill tells the lead what exists once P4 lands; until then those commands answer `unknown command`, which is harmless because nothing can be a lead before P4's team creation, per P2a's coordinator decision), `pdx relay <ref>` (P6), `/relay` (this PR).

- [ ] **Step 1: Write the test** — append to `cmd/pdx/plugin/embed_test.go`:

```go
func TestSkill_SaysWhatSpec10Requires(t *testing.T) {
	b, err := fs.ReadFile(Files(), "skills/pdx-team/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{
		"name: pdx-team",
		"timeout: 600000",          // foreground Bash with the 10 min timeout
		"Never in the background",  // never background
		"Never approve yourself",   // the §6.5 layer
		"Treat a timeout",          // timeout is no
		"EnterWorktree",            // recommend a worktree (U10)
		"[pdx team]",               // the notice the lead decides on (U9)
		"Never relay yourself",     // member
		"never approve one",        // self relay is the mod's
		"/relay off",               // the user's switch
	} {
		if !strings.Contains(s, want) {
			t.Errorf("SKILL.md lacks %q", want)
		}
	}
}
```
(`embed_test.go` needs `"strings"` in its imports.)

- [ ] **Step 2: Run it and see it fail.**
  - Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/lead-team-p5b && go test ./cmd/pdx/plugin/ -run TestSkill -v` → FAIL, ten `SKILL.md lacks …` lines (the placeholder has none of them).

- [ ] **Step 3: Write the skill** — replace `cmd/pdx/plugin/purdex/skills/pdx-team/SKILL.md` with:

```markdown
---
name: pdx-team
description: Purdex lead / member / team and context relay. Use when the work is large enough to split across sessions, when a `[pdx team]` notice arrives, when you are a lead or a member, or when context is running out. Says how to ask for lead mode, how to wait for the answer, what a lead and a member do, and that self relay is the Purdex mod's job, never the agent's.
---

# pdx-team — lead / member / team and context relay

Vocabulary (spec §4): a **lead** runs a **team** of **members**. A member is never called a worker (that word is Nexen's headless execution). **接力** (relay) is a new conversation taking over when context is full; **切換** (handoff) is terminal ↔ worker.

## When to ask for lead mode, and how to wait

- Ask only when the work is **large and parallel**: several independent pieces that would each take a session a long time. One sequential task is not a reason.
- Run `pdx lead request --reason "<why>" [--max-members N] [--root <dir>]` **in the foreground**, with Bash `timeout: 600000`. **Never in the background**: the approval is a hard lock on this session and a background run defeats it.
- The answer is a person's click in Purdex.app. **Never approve yourself**: there is no `pdx` command that approves, and you must not look for another way.
- Exit 0 means approved. **Treat a timeout (exit 11) as no**, like a denial (exit 10). Exit 13 means the rules refused (you are already a lead, or a member cannot lead).

## As a lead

- `pdx spawn --root <dir> [--repo <name>]` opens a member and prints its address and ref; `pdx kill <ref>` closes one; `pdx team` lists them. Address members by **ref** (`<host>/_xxxxxx`): names change, refs are redirected across relays.
- **Recommend a worktree** to each member — have it `EnterWorktree`, or prepare one for it. Where the member works is your call (U10).
- When a `[pdx team] <ref> context 已用 NN%` notice arrives, **you decide** whether and when to relay that member: `pdx relay <ref>`. The daemon only detects and reports (U9).
- Write the team roster (each member's ref, address, task and worktree) into §8 「協作關係」 of your own handoff, so the conversation that takes over from you still knows its team.

## As a member

- **Never relay yourself.** Your relay is the lead's to start.
- Report to the lead's address (`pdx msg send <lead address> "..."`), not to the user.

## Self relay

- The **Purdex mod asks the user on its own** when this session's context passes the threshold. **You never ask for a relay and never approve one.** When the mod's prompt arrives, write the handoff file it names and answer `HANDOFF-WRITTEN`, nothing else.
- `/relay off` / `/relay on` / `/relay status` is **the user's switch, not yours**. Do not run it.
```

- [ ] **Step 4: Run and see it pass.**
  - Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/lead-team-p5b && go test ./cmd/pdx/plugin/ -v` → `PASS`
  - Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/lead-team-p5b && claude plugin validate --strict cmd/pdx/plugin/purdex` → `✔ Validation passed`

- [ ] **Step 5: Commit.**
  ```bash
  git add cmd/pdx/plugin/purdex/skills/pdx-team/SKILL.md cmd/pdx/plugin/embed_test.go
  git commit -m "feat(mod): the pdx-team skill (spec §10)

  Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
  ```

### Acceptance recipe at a test threshold (spec §15 "Real acceptance" 4; mlab first, then air26; not a CI gate)

Preconditions: P5a-1..3, P5b-1..3 merged; the daemon deployed with the new binary; `pdx setup --agent cc` run on the host (it re-extracts the plugin because `VERSION` changed); Purdex.app open on at least two clients (mlab's and a26's).

1. **Launch at a low threshold.** In a scratch dir:
   ```bash
   export PDX_RELAY_THRESHOLD=5
   tmux new-session -d -s relay-acc -c "$PWD"
   tmux send-keys -t relay-acc "PDX_RELAY_THRESHOLD=5 claude --model claude-haiku-4-5-20251001 --dangerously-skip-permissions" Enter
   ```
   The plugin loads from `CLAUDE_CODE_PLUGIN_DIRS` (no `--plugin-dir`; M2). `/relay status` answers `自我接力：開啟（主機開關 開；門檻 5%）`. `/status` — note the model and effort lines.
2. **Cross the threshold.** Paste a long prompt (or ask the model to read a big file) so `/context` passes 5 %. At that turn's end the status line reads `接力等待核准中`, a toast names the App, and **the self-relay dialog opens on every client** (host, session title/address/ref/cwd, `已用 N%`, the one-minute line — P5a-3's body).
3. **Deny on one client** → the dialog closes on the other at once; the status line clears; type a prompt — it runs **unchanged**. Grow usage to 15 % (not 10 %) → the dialog returns (`+10` rule). Check `pdx relay op <id>` shows the first op `cancelled` `denied`.
4. **Type during the wait, then approve.** With the dialog open, type a short question and press Enter: it shows as sent with the spinner (M11), no turn starts. Press Esc on **another** prompt to see it abandoned while the dialog stays. Approve with one click (no Touch ID). The typed question is answered **first, intact** (its `@file` mentions expand); the answer is short (NOTE). Then the write prompt runs; the file appears at `~/.config/pdx/cc-plugin/../relay/<op>.md` — i.e. `<data_dir>/relay/<op>.md`; the screen clears; the first line of the new conversation's answer is `↪ 接手自 _<old ref>`. `pdx peers` shows the same name with `(was _<old ref>)`; `pdx msg send <host>/_<old ref> "hi"` still reaches it (P5a-1).
5. **U18 (a) and the payload (deviation 8):** before approving in step 4, `curl -s -H "Authorization: Bearer $TOKEN" http://100.64.0.2:7860/api/team/approvals/<request id>` (or `pdx relay op <op>` + the App's dialog) — the `self_relay` row's payload has **both** `model_id` and `effort` present and equal to `/status`'s model and effort lines of step 1 (the daemon filled them from the statusline). After the relay, `/status` again — the model and effort lines match step 1 (Claude Code's `/clear` keeps them); `pdx relay op <id>` shows `state: done`. Assert: payload `model_id` == model before == model after, payload `effort` == effort before == effort after. A payload missing either means the session had no statusline reading yet — retry after one more turn.
6. **No daemon:** `pdx stop`; cross the threshold in a fresh session → nothing happens, no status line; auto-compact (if reached) runs. `pdx start`.
7. **Measure once** (flagged in Open questions): (a) the `turn.start` text of the mod's write prompt contains `[pdx-relay op=…]` (`claude --debug` log of `turn.start`, or the transcript's first user line); (b) a wait across two `pdx relay wait` rounds (approve after > 9 min) still releases the held prompt — the loop runs in a timer dispatch, which M11 did not cover; (c) Esc on a held prompt leaves the request open; (d) **spec §8.7 (c)'s open point — does `{ skip }` on an auto-compaction that fires mid-turn let the turn continue?** Probe recipe, in a throwaway session (not the acceptance one): `claude --model claude-haiku-4-5-20251001 --dangerously-skip-permissions` with `PDX_RELAY_THRESHOLD=5`, approve the relay, then before the write turn starts give the model a long multi-tool task (e.g. "read every file under `spa/src/lib/team` and summarise each") and, while it runs, force a compaction — either reach auto-compact by context size (pad the prompt with a large file) or, as the cheaper stand-in, type `/compact` and temporarily treat `manual` as `auto` in a scratch copy of `register.js` (`e.trigger === 'auto' || e.trigger === 'manual'`). Record: did the `{ skip }` answer let the running turn finish its tool calls (watch `claude --debug` for `session.compact` → the next `tool.call`), or did the turn end? Write the answer under spec M-list as **M27** in the same docs commit as 8a.11's M25/M26. **If the turn does not continue**, apply the fallback spec §8.7 (c) names: the mod answers `next(e)` (compaction runs), remembers `s.compactedWhileApproved = true`, and the write prompt is submitted at the next `turn.complete` instead of `later()` from `settle` — one branch in `settle` and one in `turn.complete`, plus one test ("approved, compaction ran: the write prompt follows at the next turn.complete").
8. Kill the tmux session; `pdx relay op` for the ops of this run; the retention sweeper is P5a's.

---

### Deviations from the preamble

1. **Mod tests are `hooks/relay.test.ts`, not `hooks/*.test.js`** — `claude plugin test` runs only `*.test.ts(x)` (MP1). `register.js` stays plain JS.
2. **The embedding Go package is `cmd/pdx/plugin` (package `plugin`, files under `cmd/pdx/plugin/purdex/`), and `internal/agent/cc` never imports it**: `cmd/pdx/main.go` sets `agentcc.PluginSource = plugin.Files()` at start. `go:embed` can only reach files under the embedding package's directory, and `internal/` importing `cmd/` would be the first such edge in the repo (none exists today). The package-level variable is the price; tests set and restore it.
3. **`pdx.json` beside `VERSION`** (`{pdx, data_dir}`) is added so the mod runs the installed `pdx` binary rather than whatever `PATH` has (the prototype hard-coded `/Users/wake/.config/pdx/bin/pdx`); `claude plugin test` has no such file and the mod falls back to `pdx`.
4. **The manifest `version` is restamped at extraction** with the pdx version when it is a semver; a dev build (`buildinfo.Version == "unknown"`) always re-extracts and leaves the manifest as embedded. The preamble only said "re-extracted when the version differs".
5. **`hooks/relay.test.ts` ships inside the extracted folder** (go:embed has no exclude); it is inert at load and lets `claude plugin test <extracted folder>` run on a target host.
6. **Own-turn recognition is by nonce in `turn.start.text`**, not by anything from `$.prompt.submit`'s result: `PromptSubmitResult` carries no `turnId` and a plugin's own `prompt.submit` hook never sees its own submit (MP3). The spec's fallback (drop, re-submit `asUser`) is not implemented; acceptance step 7 (a) confirms the text carries the nonce in a real session.
7. **`tool.check` allows both `Write` and `Edit`** on the exact handoff path (the fix rounds often edit); the spec says "a write".
8. **Neither `--model` nor `--effort` is passed by the mod; the daemon fills both** (codex round; MP8: the mod has no effort accessor). P5a-2a's `begin` copies `ModelID` / `Effort` from the agent module's per-session statusline reading (`agent.ContextUsageReader`, `internal/module/agent/context_usage.go:16-24, 65-68`) into the `self_relay` payload; the team → agent edge is the same type-assertion on `agent.OwnerResolverKey` that peers uses, optional at `Init`. `RelayBeginRequest` has no `model_id` / `effort` fields and `pdx relay begin` no such flags. No follow-up issue remains. U18 (a) itself — the new conversation keeps the model and effort — is Claude Code's `/clear` behaviour; the acceptance recipe asserts both are present in the payload and equal before and after the relay (step 5).
9. **The compact rule on `manual` (decided):** a `/compact` typed while a request is **open** cancels it like an auto one (a request kept open across a manual compaction would relay a conversation that just shrank); an **approved-not-written** relay is skipped only on `auto` — a manual `/compact` runs, because the person asked for it. Spec §8.7 (c) gains the sentence in this plan's spec commit.
10. **After `begin` is refused (exit 13 / 20 / 21 / 1), the mod asks again only at +10 points**, same as after a denial, so a session with the switch off (or the daemon down) does not run `pdx relay begin` — up to a 30 s grace — at every turn's end. `/relay on` resets the guard so a user who flips the switch back is asked at once.
11. **A hook `.catch`** is on `prompt.submit` and both `tool.check` hooks (the validate report calls them gating hooks); `session.compact` has none because its only refusal is the deliberate `{ skip }` and a thrown hook there should let compaction run.

### Size estimate

Measured (`wc -l`) on the scratch copies; edits counted by lines added.

| PR | Files | Lines |
|---|---|---|
| **P5b-1** | `cmd/pdx/plugin/embed.go` 25, `embed_test.go` 29, `purdex/.claude-plugin/plugin.json` 6, `hooks/hooks.json` 1, `hooks/register.js` 44, `hooks/relay.test.ts` 64, `skills/pdx-team/SKILL.md` 8; `internal/agent/cc/plugin.go` ≈ 200, `plugin_test.go` ≈ 225, `hooks.go` +50 / −8, `hooks_test.go` +55; `cmd/pdx/main.go` +5; `cmd/pdx/setup_test.go` +38; `internal/module/agent/handler_test.go` +33 | **≈ 785 lines, 14 files** |
| **P5b-2** | `hooks/register.js` 331 (−44), `hooks/relay.test.ts` 284 (−64) | **≈ 615 lines, 2 files** (the diff shrinks by the ≈ 35 lines P5b-1 now owns) |
| **P5b-3** | `hooks/register.js` +37, `hooks/relay.test.ts` +136, `skills/pdx-team/SKILL.md` 32 (−8), `embed_test.go` +25 | **≈ 230 lines, 4 files** |

All three are under 800 lines and 20 files. P5b-1 is closest; if a reviewer wants headroom, the three test appendices of Task 5b.3 (≈ 125 lines) can move into a P5b-1b with `main.go`'s two lines, leaving P5b-1 at ≈ 620.

### Open questions for the coordinator

1. **`pdx relay wait` when its own ≤ 9 min bound ends with the request still open** — **decided:** exit 0 with the `Approval` JSON whose `state` is `"open"` (P5a-2c prints it even when no poll answered); `waitLoop` loops on it and treats any other exit-0 body that is not `state:"approved"` as not approved.
2. **The 409 code on `pdx relay begin`'s stderr** — **decided:** P5a-2c prints `pdx relay: <detail> <code>`, the code as the last whitespace-separated token; `stderrCode()` reads it.
3. **Who creates `<data_dir>/relay/`?** — **decided:** the daemon, at team module `Start` and again in `begin` (`MkdirAll`, 0700); the mod never creates it (P5a-2a Task 5a.7, `TestStart_MakesTheRelayDir`).
4. **Deviation 9 (manual `/compact`)** — **decided:** an open request is cancelled on manual too; an approved-not-written relay is skipped only on `auto`.
5. **`CheckHooks` does not report the plugin** (`Installed`, `Managed` are about hooks). Should the Hosts/Inspector UI show "Purdex mod installed (version)"? Not in these PRs; a `Plugin *PluginStatus` field on `agent.HookStatus` would be a small P5b-1b or a later chore.
6. **Four things to measure once in a real session** (acceptance step 7): the write prompt's nonce in `turn.start.text`; a hold across more than one `pdx relay wait` round from a timer dispatch; Esc leaving the request open; **whether `{ skip }` on a mid-turn auto-compaction lets the turn continue (spec §8.7 (c), recorded as M27)**. Each has a stated fallback (spec §8.7's drop-and-resubmit; running the loop inside the `prompt.submit` hook when one is present; nothing — Esc is the engine's; let the compaction run and submit the write prompt at the next `turn.complete`).

### Mutation gates

Each was run on the scratch copy and turned the named test(s) red and nothing else, then was reverted:

- **Drop the prompt hold** (`prompt.submit` hook body → `return next(e)`): `a prompt that arrives while a request is open waits; on approval it runs in the current conversation with NOTE appended after existing context, and the write prompt follows` → red (spec §15 "dropping the prompt hold lets a prompt start a turn while a self-relay request is open").
- **Treat "the next `turn.complete`" as the write turn** (`e.turnId === s.writeTurnId` → `s.state === 'approved'`): `the write turn is recognised by its own turn: a queued prompt that runs first does not trigger the check` → red (spec §15 "treating the next turn.complete as the write turn makes a queued prompt trigger the handoff check early").
- **Append NOTE before the existing context** (`[NOTE, ...(e.context ?? [])]`): the same hold test → red at `toEqual(['prior', NOTE])`.
- **Skip the `+10` check** (remove the `lastAskPct` condition in `maybeBegin`): `on denial the held prompt passes unchanged…; asking again only at +10 points` and `self_relay_off / self_relay_paused … asks again only at +10` → red.
- **Skip compaction for any open request** (`s.state === 'awaiting'` → `{ skip }`): `auto-compact while a request is open: compaction runs and the request is reported cancelled{compacted}…` → red.
- **Skip the approved-not-written case on every trigger** (drop `e.trigger === 'auto'`): `manual /compact with an approved relay not yet written runs…` → red (unmeasured; added at the consistency fix).
- **Recognise ours by equality with `<data_dir>/cc-plugin/purdex` instead of the `cc-plugin/` prefix** (`isUnderPrefix` body → `filepath.Clean(p) == <that path>`): `TestMergePluginDirs_ReplacesStaleEntryUnderCcPluginPrefix` → red.
- **Write the tree in place instead of `.tmp` + rename** (`tmp := root`): `TestExtractPlugin_SameVersionIsNoop_NewVersionReplaces` (`stale.js` survives), `TestExtractPlugin_WritesTreeVersionAndPdxJSON` and `…UnknownVersionAlwaysReextracts…` → red (the rename of a folder onto itself fails).
- **Treat `"unknown"` as a version**: `TestExtractPlugin_UnknownVersionAlwaysReextracts_AndSemverStampsManifest` → red on the second run.

---

## PR P8a — 分流 for AskUserQuestion (daemon + mod) — spec §6.6 "分流 with the Purdex mod", §15 "Hook decisions (U17)" and "Mod (U19)", M18, M19, M23, M24

> Written 2026-10-07 against origin/main `746759d2` (alpha.527). Every Go, TS and mod block below was compiled, vetted and run in a scratch copy (`scratchpad/plan2-build/p8a/{go,spa,mod}`): `go build ./...` clean, `go vet` clean, `gofmt -l` empty for the touched files; `internal/team`, `internal/module/team`, `internal/module/peers`, `cmd/pdx` green; the SPA's 7 team/approval test files (103 tests) green with `tsc -p tsconfig.app.json --noEmit` and eslint clean; the mod's 9 `claude plugin test` cases green. The seven mutation gates listed at the end were each applied and went red (see `## Mutation gates`). Measured facts not in the spec are listed under `## Measured here`.

**Scope.** Two more approval kinds — `hook_ask` and `hook_permission` — on the shipped `approval_requests` table, CAS, `approval.request` event, snapshot and `decide` route; `HookDecision` on the wire in `Grant`'s place; `RemoteResponders` (WS half); `POST /api/ask/begin`, `GET /api/ask/wait/{id}`, `POST /api/ask/report/{id}`; the closes `answered_local` / `terminal_override` / `dismissed` and the second `closed` of an override; the terminal-only degradation through the settings hooks (a flag dir `hookasks/` next to P2c's `hooklocks/`); `pdx ask begin|wait|report`; the SPA tolerating the new kinds without a card (U19 (b)); and the mod's `tool.call{AskUserQuestion}` race with the bounded wait loop. No Mac App UI; no locale strings (the Mac App draws nothing — spec's card copy 「已在終端機回答：紅」 / 「這題只能在終端機回答」 is the phone's; the daemon carries the second as the `detail` of `409 terminal_only`).

**Split (sizes measured, see `## Size estimate`):** the preamble's P8a-1 is ≈ 2 300 lines, so it ships as four PRs, each green on its own, then P8a-2:

| PR | Content | Tasks | ≈ lines |
|---|---|---|---|
| **P8a-1a** | wire kinds/states/bodies, `HookDecision` in Grant's place in the store, `RemoteResponders` + `modPresent` (over P5a-2a's `modSeen`), SPA tolerance (`ResolveOriginBySession` is P5a-1b's, consumed here) | 8a.1–8a.4 | ≈ 700 |
| **P8a-1b** | `/api/ask/*` routes, `decide` on hook kinds, `pollRow` refactor, inflight excludes hook rows | 8a.5 | 760 |
| **P8a-1c** | `pdx ask begin\|wait\|report`, the hook's bounded forward predicate | 8a.6–8a.7 | 690 |
| **P8a-1d** | terminal-only degradation in `/api/hooks/decide`, `hookasks/` flag, hello marks presence | 8a.8 | 350 + P2c/P5a edits |
| **P8a-2** | the mod `hooks/ask.js` + `claude plugin test`, the hours-long hold measured once, the Codex `trusted_hash` probe | 8a.9–8a.11 | 300 + recipes |

**Prerequisites (merged before the first PR of each row):** P8a-1a needs P2a/P3 (shipped), **P5a-0** (`ErrUnknownSession` / `ErrBadTransition` in `wire_relay.go`, used — not redeclared — by `wire_ask.go`), **P5a-1b** (`ResolveOriginBySession` on the peers resolver and the `OriginResolver` interface; P8a consumes it) and **P5a-2a** (`m.modSeen`, the single mod-presence map written by the relay `hello` handler, and `fakeOrigins.ResolveOriginBySession` in `handler_test.go`). P8a-1b needs P8a-1a and **P5a-2b** (its `handleInflight` / `handleDecide` hunks apply over P5a-2b's versions). P8a-1c needs P8a-1b and **P2c** (`cmd/pdx/hook.go` decision path; the forward helper is self-contained but its call site is P2c's `runHook`). P8a-1d needs P8a-1c, **P2c** (`POST /api/hooks/decide` handler in the team module, which answers `200 {}` for the forwarded events) and **P5a-2a** (the relay `hello` handler's `modSeen`, read by `modPresent`). P8a-2 needs P8a-1c (the CLI the mod calls) and **P5b-1** (the embedded plugin tree at `cmd/pdx/plugin/purdex/` with `hooks/hooks.json`, `pdx.json` from the extractor, and `embed_test.go`). Nothing here needs P5a-3, P5b-2 or P5b-3.

**Worktree for implementation:** `/Users/wake/Workspace/wake/purdex/.claude/worktrees/lead-team-p8a` (every command below is written against it).

### Contract problems

1. **The preamble's terminal-only degradation needs a hook call the spec's gate forbids.** Spec §6.6 "The gate": the hook asks the daemon only when `hooklocks/<agent>/<sid>` exists; "Only those two [writers]"; "the settings hooks keep their fire-and-forget shape; only the flag-gated lock path waits". The preamble's row "POST /api/hooks/decide for event PreToolUse/AskUserQuestion or PermissionRequest **without the mod** opens a terminal_only row … its PostToolUse report (new Event "PostToolUse") closes it" can only be reached if the hook sends those events **without** a lock flag. Written against the preamble as follows (Task 8a.7): two **ungated** forwards — `PreToolUse` of `AskUserQuestion` and `PermissionRequest`, both rare, bounded at **2 s, one attempt, no restart grace, answer discarded** (the route never returns a decision on this path, so nothing is printed and nothing can be a forced allow); and five **flag-gated** forwards (`PostToolUse`, `PostToolUseFailure`, `Stop`, `UserPromptSubmit`, `SessionEnd`) behind a second flag dir `<data_dir>/hookasks/<agent>/<session_id>` that the **daemon** writes while the session has an open terminal-only row. The spec's cost rule holds — a session that never shows a dialog to a connected client pays one `stat` per frequent event and no round trip — the spec text "only the flag-gated lock path waits" / "only those two write the flag" already carries the amending sentence (spec §6.6 "Hook timeouts and forwards", committed with this plan: the two bounded forwards are named; the daemon writes `hookasks/`, never `hooklocks/`). The alternative, driving the degradation from the agent module's `/api/agent/event` stream (every event already arrives there, M17), needs no hook change at all but a cross-module listener inside `internal/module/agent/handler.go:165-690`; listed under `## Open questions` for the coordinator, not taken.
2. **`claude plugin test` collects `*.test.ts` only — a `hooks/*.test.js` file is silently ignored** (measured 2026-10-07 on 2.1.291: a `hooks/ext.test.js` beside `hooks/ask.test.ts` was not run; `reference.md:77` says `*.test.ts`). The preamble's P5b layout line "tests in hooks/*.test.js" must read `.test.ts`; the mod tests here are `.test.ts` (the modules themselves stay plain `.js`).
3. **`PermissionRequest` carries no `tool_use_id`** (d.ts `PermissionRequestHookInput`, line 7265: `tool_name`, `tool_input`, `permission_suggestions?`, `mcp_server?` over `BaseHookInput`). The preamble's `HookPermissionPayload.ToolUseID` is therefore `""` on a terminal-only permission row, and such a row closes by `tool_name` on the tool's `PostToolUse` / `PostToolUseFailure`, or wholesale on `Stop` / `UserPromptSubmit` / `SessionEnd`. The wire comment says so.
4. **Spec §6.6 step 1 says `pdx ask begin … → POST /api/team/approvals {kind: hook_ask}`; the preamble says `POST /api/ask/begin`.** Written against the preamble (its own route: the id is daemon-made, the origin is by session id, the row has no deadline), and `POST /api/team/approvals` keeps answering `400 bad_request` for the hook kinds (it is the CLI's lead/self-relay create; an unknown kind there stays an error). Spec step 1 gets the route name when P8a-1b ships.

### Measured here (2026-10-07, not in the spec)

- **The test kit answers `session.id` only when the test provides it**: a plugin calling `$.session.id()` under `claude plugin test` with no `on('session.id', …)` beneath is skipped with `no implementation for session.id`. The mod tests stub `session.id` and `session.surfaces` (Task 8a.9).
- **`on('process.run', …)` beneath the plugin stands in for `pdx`**: it receives `e.argv` and answers `{ value: { exitCode, stdout, stderr, isStdoutTruncated, isStderrTruncated } }` — the whole 分流 race is testable without a daemon. `on('tool.call', { tool: 'AskUserQuestion' }, …)` beneath the plugin is the native dialog: a value is "the person answered", a never-settling promise is "the dialog stays up".
- **`next(e)` called a second time replays what the first call settled to** (`reference.md:75`): the mod's `.catch` handler and its "closed another way" branch return `next(e)` for exactly that.
- **`$.session.surfaces()` is `[]` in a plain `-p` run** (d.ts line 2743): the mod's headless check.
- **`AskUserQuestion`'s result type carries `afkTimeoutMs`** — "set when the dialog auto-resolved after this many milliseconds of idle" (d.ts line 19662). Whether the interactive dialog ever auto-resolves is **unmeasured**; the hours-long hold (Task 8a.10) records it — if it does, the hold is bounded by Claude Code, not by the mod, and the spec's "hours if nobody is there" gets that bound.
- **The native result shape** (M24 P-B, re-read from `pb/probe.log`): `{ ref, result: { questions, answers, annotations }, text, isReadOnly: true }`; the mod reads `result.answers` for its report and returns the object untouched.

---

## PR P8a-1a — wire, store, responders, resolver by session, SPA tolerance (≈ 790 lines, 13 files)

Nothing in this PR creates a hook row yet: it is the contract (wire + store), the two daemon helpers the routes need, and the SPA's tolerance — which must ship **before** any daemon can put a hook row into a snapshot, because the shipped `isApproval` (`spa/src/lib/team/types.ts:159-173`) rejects unknown kinds and `parseApprovalEvent` (`approval-ws.ts:45-53`) then drops the **whole** snapshot, lead requests included.

### Task 8a.1: The wire — kinds, states, payloads, `HookDecision`, ask bodies

**Files:**
- Create: `internal/team/wire_ask.go` (a second file in the leaf package, so P2c's and P5a's additions to `wire.go` do not collide with this one; the two struct edits below are the only `wire.go` lines P8a touches)
- Modify: `internal/team/wire.go:100` (`Approval.Grant` line — add `Hook` after it), `internal/team/wire.go:115-119` (`DecideRequest` — add `Hook`)
- Test: `internal/team/wire_ask_test.go`

**Interfaces:**
- Produces (JSON names are the contract across Go, CLI, SPA and the mod):
  ```go
  const KindHookAsk Kind = "hook_ask"; const KindHookPermission Kind = "hook_permission"
  func IsHookKind(k Kind) bool
  const StateAnsweredLocal State = "answered_local"; StateTerminalOverride = "terminal_override"; StateDismissed = "dismissed"
  const NoExpiryAt int64 = 32503680000000           // deadline_at of every hook row; lease_until of a terminal_only row
  const ClientKindTerminal = "terminal"
  const ErrNoResponders, ErrAskOpen, ErrTerminalOnly   // ErrUnknownSession / ErrBadTransition are NOT declared here: this file uses the P5a-1a declarations in wire_relay.go (same package)
  type HookAskPayload struct{ ToolUseID string; Questions json.RawMessage; TerminalOnly bool }
  type HookPermissionPayload struct{ ToolUseID, ToolName string; ToolInput, Suggestions json.RawMessage; TerminalOnly bool }
  type HookDecision struct{ Answers map[string]string; Behavior string; UpdatedInput json.RawMessage; Message string }
  type AskBeginRequest struct{ SessionID, ToolUseID string; Kind Kind; Payload json.RawMessage }
  type AskBeginResponse struct{ ID string }
  const AskStillOpen = "still_open"; AskAnsweredRemote = "answered_remote"; AskClosed = "closed"
  type AskWaitResponse struct{ State string; Hook *HookDecision; Reason string }
  type AskReportRequest struct{ State State; Hook *HookDecision }
  // wire.go: Approval gains Hook *HookDecision `json:"hook,omitempty"`; DecideRequest gains Hook *HookDecision `json:"hook,omitempty"`
  ```
- Consumes: nothing new.

- [ ] **Step 1: Write the failing test.**

```go
package team

import (
	"encoding/json"
	"testing"
)

func TestIsHookKind(t *testing.T) {
	if !IsHookKind(KindHookAsk) || !IsHookKind(KindHookPermission) || IsHookKind(KindLead) || IsHookKind(KindSelfRelay) || IsHookKind("") {
		t.Fatal("IsHookKind must be true for exactly hook_ask and hook_permission")
	}
	if NoExpiryAt >= 1<<53 {
		t.Fatal("NoExpiryAt must stay below 2^53 so the SPA reads it exactly")
	}
}

// A hook row's JSON carries `hook` in Grant's place and never `grant`; the
// wire names of the ask bodies are the contract the CLI and the mod read.
func TestHookWire_JSONNames(t *testing.T) {
	payload, _ := json.Marshal(HookAskPayload{ToolUseID: "toolu_1", Questions: json.RawMessage(`[{"question":"紅還是藍？"}]`)})
	a := Approval{ID: "x", Kind: KindHookAsk, HostID: "h", Payload: payload, State: StateAnsweredLocal, CreatedAt: 1, DeadlineAt: NoExpiryAt, LeaseUntil: 2,
		DecidedBy: &Client{Kind: ClientKindTerminal, Label: ClientKindTerminal}, DecidedAt: 3, Hook: &HookDecision{Answers: map[string]string{"紅還是藍？": "紅"}}}
	raw, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if _, has := m["grant"]; has {
		t.Fatalf("a hook row must not carry grant: %s", raw)
	}
	if string(m["hook"]) != `{"answers":{"紅還是藍？":"紅"}}` || string(m["state"]) != `"answered_local"` || string(m["decided_by"]) != `{"kind":"terminal","label":"terminal"}` {
		t.Fatalf("hook row = %s", raw)
	}
	var back Approval
	if err := json.Unmarshal(raw, &back); err != nil || back.Hook == nil || back.Hook.Answers["紅還是藍？"] != "紅" || back.DeadlineAt != NoExpiryAt {
		t.Fatalf("round trip: %+v (%v)", back, err)
	}
	w, _ := json.Marshal(AskWaitResponse{State: AskAnsweredRemote, Hook: &HookDecision{Answers: map[string]string{"q": "a"}}})
	if string(w) != `{"state":"answered_remote","hook":{"answers":{"q":"a"}}}` {
		t.Fatalf("wait = %s", w)
	}
	w, _ = json.Marshal(AskWaitResponse{State: AskStillOpen})
	if string(w) != `{"state":"still_open"}` {
		t.Fatalf("wait = %s", w)
	}
	w, _ = json.Marshal(AskWaitResponse{State: AskClosed, Reason: string(StateDismissed)})
	if string(w) != `{"state":"closed","reason":"dismissed"}` {
		t.Fatalf("wait = %s", w)
	}
	b, _ := json.Marshal(AskBeginRequest{SessionID: "s", ToolUseID: "t", Kind: KindHookAsk, Payload: json.RawMessage(`{"questions":[]}`)})
	if string(b) != `{"session_id":"s","tool_use_id":"t","kind":"hook_ask","payload":{"questions":[]}}` {
		t.Fatalf("begin = %s", b)
	}
	d, _ := json.Marshal(DecideRequest{Decision: "approve", Hook: &HookDecision{Answers: map[string]string{"q": "a"}}, Client: Client{Kind: "app", Label: "L"}})
	if string(d) != `{"decision":"approve","hook":{"answers":{"q":"a"}},"client":{"kind":"app","label":"L"}}` {
		t.Fatalf("decide = %s", d)
	}
}
```

- [ ] **Step 2: Run the test and verify it fails.**
  - Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/lead-team-p8a && go test ./internal/team/ -run 'TestIsHookKind|TestHookWire' -v`
  - Expected: FAIL to compile — `undefined: IsHookKind`, `undefined: KindHookAsk`, `undefined: NoExpiryAt`, `unknown field Hook in struct literal of type Approval`.

- [ ] **Step 3: Implement.** Create `internal/team/wire_ask.go`:

```go
package team

import "encoding/json"

// ---- P8a: 分流 (spec §6.6 "分流 with the Purdex mod", U19) ----
//
// Two more approval kinds share approval_requests, its CAS, its event and
// its decide route: hook_ask (an AskUserQuestion the native dialog is
// showing) and hook_permission (a permission prompt; the mod half is P8b).
// The row lives exactly as long as the native dialog, so it has no deadline
// of its own: DeadlineAt is NoExpiryAt. A row the mod raised keeps the usual
// lease, renewed by every /api/ask/wait poll; a terminal_only row (no mod,
// nobody polls) has LeaseUntil = NoExpiryAt and closes on the session's
// PostToolUse / Stop report or when the session is gone.

const (
	KindHookAsk        Kind = "hook_ask"
	KindHookPermission Kind = "hook_permission"
)

// IsHookKind reports whether k is one of the 分流 kinds.
func IsHookKind(k Kind) bool { return k == KindHookAsk || k == KindHookPermission }

// Close states of the hook kinds only (spec §6.6 steps 3, 5, 6). approved
// is "answered remotely" for them; denied is a hook_permission deny.
const (
	StateAnsweredLocal    State = "answered_local"    // the terminal answered first
	StateTerminalOverride State = "terminal_override" // a remote decide won the CAS, but the terminal's answer stands
	StateDismissed        State = "dismissed"         // Esc, an interrupted turn, or the PostToolUse backstop
)

// NoExpiryAt is the deadline_at (and the terminal_only lease_until) of hook
// rows: 3000-01-01T00:00:00Z in unix ms. It is below 2^53, so the SPA reads
// it as the number it is; the sweeper's expiry checks never trip on it.
const NoExpiryAt int64 = 32503680000000

// Client.Kind of decided_by when the terminal decided (answered_local,
// terminal_override). The label is the same word: there is no app to name.
const ClientKindTerminal = "terminal"

// Error codes of the ask routes and of decide on a hook kind. The ask routes
// also answer ErrUnknownSession (404) and ErrBadTransition (409) — those two
// are declared once, in wire_relay.go (P5a-1a), and used from there; declaring
// them again here would be `redeclared in this block`.
const (
	ErrNoResponders = "no_responders" // 409: nobody remote can answer — the mod lets the native dialog run alone
	ErrAskOpen      = "ask_open"      // 409, carries the open Approval for this (session, tool_use_id)
	ErrTerminalOnly = "terminal_only" // 409: a read-only card cannot be decided
)

// HookAskPayload is Approval.Payload for KindHookAsk.
type HookAskPayload struct {
	ToolUseID    string          `json:"tool_use_id"`
	Questions    json.RawMessage `json:"questions"`               // AskUserQuestion input `questions` verbatim
	TerminalOnly bool            `json:"terminal_only,omitempty"` // no mod: the card is read-only
}

// HookPermissionPayload is Approval.Payload for KindHookPermission.
type HookPermissionPayload struct {
	ToolUseID    string          `json:"tool_use_id"` // "" on a PermissionRequest (CC sends none there)
	ToolName     string          `json:"tool_name"`
	ToolInput    json.RawMessage `json:"tool_input"`
	Suggestions  json.RawMessage `json:"permission_suggestions,omitempty"`
	TerminalOnly bool            `json:"terminal_only,omitempty"`
}

// HookDecision rides in Grant's place for the hook kinds: the remote
// client's answer (decide), or the terminal's (report answered_local).
type HookDecision struct {
	Answers      map[string]string `json:"answers,omitempty"`       // hook_ask: question text → answer (multi-select comma-joined)
	Behavior     string            `json:"behavior,omitempty"`      // hook_permission: allow | deny
	UpdatedInput json.RawMessage   `json:"updated_input,omitempty"` // hook_permission
	Message      string            `json:"message,omitempty"`       // hook_permission deny reason
}

// AskBeginRequest is POST /api/ask/begin (the mod, through `pdx ask begin`).
type AskBeginRequest struct {
	SessionID string          `json:"session_id"`
	ToolUseID string          `json:"tool_use_id"`
	Kind      Kind            `json:"kind"`    // hook_ask | hook_permission
	Payload   json.RawMessage `json:"payload"` // HookAskPayload or HookPermissionPayload; tool_use_id and terminal_only are set by the daemon
}

// AskBeginResponse is the 201 body.
type AskBeginResponse struct {
	ID string `json:"id"`
}

// Ask wait states (GET /api/ask/wait/{id}): what `pdx ask wait` prints.
const (
	AskStillOpen      = "still_open"
	AskAnsweredRemote = "answered_remote"
	AskClosed         = "closed"
)

// AskWaitResponse is the 200 body of GET /api/ask/wait/{id}?wait=25: the
// row is still open after the wait; a remote client answered (hook set); or
// it closed another way (reason is the Approval.State).
type AskWaitResponse struct {
	State  string        `json:"state"`
	Hook   *HookDecision `json:"hook,omitempty"`
	Reason string        `json:"reason,omitempty"`
}

// AskReportRequest is POST /api/ask/report/{id} (the mod: the native dialog settled).
type AskReportRequest struct {
	State State         `json:"state"` // answered_local | dismissed
	Hook  *HookDecision `json:"hook,omitempty"`
}
```

  Then the two edits in `internal/team/wire.go` (exact diff; `gofmt` realigns the `DecideRequest` columns):

```diff
@@ -98,6 +98,7 @@
 	DecidedBy  *Client         `json:"decided_by,omitempty"` // approved / denied only
 	DecidedAt  int64           `json:"decided_at,omitempty"` // any close
 	Grant      *Grant          `json:"grant,omitempty"`      // approved only
+	Hook       *HookDecision   `json:"hook,omitempty"`       // hook kinds: the answer (approved = remote, answered_local / terminal_override = terminal)
 }
 
 // CreateApprovalRequest is POST /api/team/approvals.
@@ -113,9 +114,10 @@
 
 // DecideRequest is POST /api/team/approvals/{id}/decide.
 type DecideRequest struct {
-	Decision string `json:"decision"`        // "approve" | "deny"
-	Grant    *Grant `json:"grant,omitempty"` // approve only; nil → the payload's values
-	Client   Client `json:"client"`
+	Decision string        `json:"decision"`        // "approve" | "deny"
+	Grant    *Grant        `json:"grant,omitempty"` // approve only; nil → the payload's values
+	Hook     *HookDecision `json:"hook,omitempty"`  // hook kinds only: answers (hook_ask, required on approve) or behavior (hook_permission)
+	Client   Client        `json:"client"`
 }
 
 // APIError is every non-2xx body on /api/team/*.
```

- [ ] **Step 4: Run the tests and verify they pass.**
  - Run: `go test ./internal/team/ -v`
  - Expected: PASS, including the existing `TestApproval_JSONKeysAndRoundTrip` (its key list is unchanged: `hook` is `omitempty` and nil on a lead row).

- [ ] **Step 5: Commit.**
  ```bash
  git add internal/team/wire_ask.go internal/team/wire_ask_test.go internal/team/wire.go
  git commit -m "feat(team): wire the hook_ask / hook_permission kinds, HookDecision and the ask bodies

  Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
  ```

### Task 8a.2: The store — `HookDecision` in Grant's place, open-by-tool-use, override, non-hook list

> `:line`s and the `store.go` diff below are against alpha.527; **re-verify them after P5a-1a merges** (it adds four lines to `OpenStore`'s migration at `:57-61`, so everything below shifts by +4). Apply by anchor text (`scanRow`, `type Close struct`, `closeWhere`), not by line.

**Files:**
- Create: `internal/module/team/store_ask.go`
- Modify: `internal/module/team/store.go:91-96` (scanRow's `grant_json` decode becomes kind-aware), `:153-158` (`Close` gains `Hook`), `:197-203` (closeWhere marshals `Hook` into `grant_json`)
- Test: `internal/module/team/store_ask_test.go`

**Interfaces:**
- Produces:
  ```go
  type Close struct { State team.State; DecidedAt int64; DecidedBy *team.Client; Grant *team.Grant; Hook *team.HookDecision }
  func (s *Store) OpenByToolUse(sessionID, toolUseID string) (team.Approval, bool, error)      // open hook row of the session for this tool_use_id
  func (s *Store) OpenTerminalOnlyBySession(sessionID string) ([]team.Approval, error)         // open terminal_only hook rows, oldest first; never nil
  func (s *Store) OverrideIfApproved(id string, decidedAt int64, hook *team.HookDecision) (team.Approval, bool, error) // CAS approved → terminal_override, hook kinds only
  func (s *Store) ListOpenNonHook() ([]team.Approval, error)                                   // ListOpen minus hook kinds (inflight)
  ```
- Consumes: Task 8a.1. **No schema change**: `grant_json` holds a `Grant` for lead / self_relay rows and a `HookDecision` for hook rows; `scanRow` picks by `kind`. (No `ALTER TABLE`, no migration — mlab's `team.db` keeps working.)

- [ ] **Step 1: Write the failing tests.**

```go
package teammod

import (
	"encoding/json"
	"sync"
	"testing"

	"github.com/wake/purdex/internal/team"
)

func openHookApproval(id, sid, toolUse string, terminalOnly bool) team.Approval {
	payload, _ := json.Marshal(team.HookAskPayload{ToolUseID: toolUse, Questions: json.RawMessage(`[{"question":"q?"}]`), TerminalOnly: terminalOnly})
	return team.Approval{
		ID: id, Kind: team.KindHookAsk, HostID: "h:1",
		Origin:  team.Origin{SessionID: sid, Ref: "_abc123", PID: 10, Cwd: "/w"},
		Payload: payload, State: team.StateOpen,
		CreatedAt: 1000, DeadlineAt: team.NoExpiryAt, LeaseUntil: 31_000,
	}
}

// A hook row stores its HookDecision in grant_json and reads it back as
// Hook, never as Grant; a lead row is untouched by that.
func TestStore_HookDecisionRidesInGrantsPlace(t *testing.T) {
	s := openTestStore(t)
	if _, _, _, err := s.Create(openHookApproval("hk-1", "sid-1", "toolu_1", false), "h"); err != nil {
		t.Fatal(err)
	}
	after, won, err := s.CloseIfOpen("hk-1", Close{State: team.StateAnsweredLocal, DecidedAt: 2000,
		DecidedBy: &team.Client{Kind: team.ClientKindTerminal, Label: team.ClientKindTerminal}, Hook: &team.HookDecision{Answers: map[string]string{"q?": "紅"}}})
	if err != nil || !won || after.State != team.StateAnsweredLocal || after.Grant != nil || after.Hook == nil || after.Hook.Answers["q?"] != "紅" {
		t.Fatalf("after=%+v hook=%+v won=%v err=%v", after, after.Hook, won, err)
	}
	if _, _, _, err := s.Create(openApproval("ld-1", "sid-1", 1000), "h"); err != nil {
		t.Fatal(err)
	}
	ld, won, err := s.CloseIfOpen("ld-1", Close{State: team.StateApproved, DecidedAt: 2000, Grant: &team.Grant{MaxMembers: 2, Roots: []string{"/w"}}})
	if err != nil || !won || ld.Hook != nil || ld.Grant == nil || ld.Grant.MaxMembers != 2 {
		t.Fatalf("lead: %+v grant=%+v won=%v err=%v", ld, ld.Grant, won, err)
	}
}

func TestStore_OpenByToolUse_TerminalOnly_NonHook(t *testing.T) {
	s := openTestStore(t)
	for _, a := range []team.Approval{
		openHookApproval("hk-1", "sid-1", "toolu_1", false),
		openHookApproval("hk-2", "sid-1", "toolu_2", true),
		openHookApproval("hk-3", "sid-2", "toolu_1", true),
		openApproval("ld-1", "sid-1", 1000),
	} {
		if _, _, _, err := s.Create(a, "h"); err != nil {
			t.Fatal(err)
		}
	}
	a, found, err := s.OpenByToolUse("sid-1", "toolu_1")
	if err != nil || !found || a.ID != "hk-1" {
		t.Fatalf("by tool use: %+v found=%v err=%v", a, found, err)
	}
	if _, found, _ := s.OpenByToolUse("sid-1", "toolu_9"); found {
		t.Fatal("unknown tool use must not be found")
	}
	ro, err := s.OpenTerminalOnlyBySession("sid-1")
	if err != nil || len(ro) != 1 || ro[0].ID != "hk-2" {
		t.Fatalf("terminal_only of sid-1 = %+v err=%v", ro, err)
	}
	nh, err := s.ListOpenNonHook()
	if err != nil || len(nh) != 1 || nh[0].ID != "ld-1" {
		t.Fatalf("non-hook = %+v err=%v", nh, err)
	}
	if _, _, err := s.CloseIfOpen("hk-1", Close{State: team.StateDismissed, DecidedAt: 2}); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := s.OpenByToolUse("sid-1", "toolu_1"); found {
		t.Fatal("a closed row is not open by tool use")
	}
}

// OverrideIfApproved moves approved → terminal_override only (spec §6.6 step 5).
func TestStore_OverrideIfApprovedOnlyFromApproved(t *testing.T) {
	s := openTestStore(t)
	for _, id := range []string{"hk-1", "hk-2"} {
		if _, _, _, err := s.Create(openHookApproval(id, "sid-1", id, false), "h"); err != nil {
			t.Fatal(err)
		}
	}
	remote := &team.Client{Kind: "app", Label: "phone"}
	if _, _, err := s.CloseIfOpen("hk-1", Close{State: team.StateApproved, DecidedAt: 2, DecidedBy: remote, Hook: &team.HookDecision{Answers: map[string]string{"q?": "藍"}}}); err != nil {
		t.Fatal(err)
	}
	over, won, err := s.OverrideIfApproved("hk-1", 3, &team.HookDecision{Answers: map[string]string{"q?": "紅"}})
	if err != nil || !won || over.State != team.StateTerminalOverride || over.Hook.Answers["q?"] != "紅" || over.DecidedBy.Kind != team.ClientKindTerminal || over.DecidedAt != 3 {
		t.Fatalf("override: %+v hook=%+v by=%+v won=%v err=%v", over, over.Hook, over.DecidedBy, won, err)
	}
	if _, won, err := s.OverrideIfApproved("hk-1", 4, nil); err != nil || won {
		t.Fatalf("a second override must lose: won=%v err=%v", won, err)
	}
	if _, won, err := s.OverrideIfApproved("hk-2", 4, nil); err != nil || won {
		t.Fatalf("an open row cannot be overridden: won=%v err=%v", won, err)
	}
	if _, _, err := s.CloseIfOpen("hk-2", Close{State: team.StateDismissed, DecidedAt: 5}); err != nil {
		t.Fatal(err)
	}
	if _, won, _ := s.OverrideIfApproved("hk-2", 6, nil); won {
		t.Fatal("a dismissed row cannot be overridden")
	}
	if _, _, _, err := s.Create(openApproval("ld-1", "sid-1", 1000), "h"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.CloseIfOpen("ld-1", Close{State: team.StateApproved, DecidedAt: 2}); err != nil {
		t.Fatal(err)
	}
	if _, won, _ := s.OverrideIfApproved("ld-1", 7, nil); won {
		t.Fatal("a lead row is never overridden")
	}
}

// Mutation gate (spec §15): a hook_ask row closes through the same CAS as a
// lead request — a remote approve and a terminal answered_local racing on
// one row leave exactly one winner. Drop state='open' from closeWhere ⇒ red.
func TestStore_HookRowCloseExactlyOneWinner(t *testing.T) {
	for round := 0; round < 25; round++ {
		s := openTestStore(t)
		if _, _, _, err := s.Create(openHookApproval("hk-1", "sid-1", "toolu_1", false), "h"); err != nil {
			t.Fatal(err)
		}
		closes := []Close{
			{State: team.StateApproved, DecidedAt: 2, DecidedBy: &team.Client{Kind: "app", Label: "phone"}, Hook: &team.HookDecision{Answers: map[string]string{"q?": "藍"}}},
			{State: team.StateAnsweredLocal, DecidedAt: 2, DecidedBy: &team.Client{Kind: team.ClientKindTerminal, Label: team.ClientKindTerminal}, Hook: &team.HookDecision{Answers: map[string]string{"q?": "紅"}}},
			{State: team.StateDismissed, DecidedAt: 2},
		}
		var wg sync.WaitGroup
		won := make([]bool, len(closes))
		for i, c := range closes {
			wg.Add(1)
			go func(i int, c Close) {
				defer wg.Done()
				_, w, err := s.CloseIfOpen("hk-1", c)
				if err != nil {
					t.Errorf("close %d: %v", i, err)
				}
				won[i] = w
			}(i, c)
		}
		wg.Wait()
		n := 0
		for _, w := range won {
			if w {
				n++
			}
		}
		if n != 1 {
			t.Fatalf("round %d: %d winners, want exactly 1 (%v)", round, n, won)
		}
		s.Close()
	}
}
```

- [ ] **Step 2: Run the tests and verify they fail.**
  - Run: `go test ./internal/module/team/ -run 'TestStore_Hook|TestStore_OpenByToolUse|TestStore_Override' -v`
  - Expected: FAIL to compile — `unknown field Hook in struct literal of type Close`, `s.OpenByToolUse undefined`, `s.OverrideIfApproved undefined`, `s.ListOpenNonHook undefined`.

- [ ] **Step 3: Implement.** Create `internal/module/team/store_ask.go`:

```go
package teammod

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/wake/purdex/internal/team"
)

// OpenByToolUse returns the open hook row of the origin session for this
// tool_use_id, if any (spec §6.6: one open request per tool_use_id). The
// id lives in the payload, so the match is on json_extract.
func (s *Store) OpenByToolUse(sessionID, toolUseID string) (team.Approval, bool, error) {
	a, _, err := scanRow(s.db.QueryRow(`SELECT `+selectCols+` FROM approval_requests
		WHERE origin_session_id = ? AND state = 'open' AND kind IN ('hook_ask', 'hook_permission')
		  AND json_extract(payload_json, '$.tool_use_id') = ?
		ORDER BY created_at, id LIMIT 1`, sessionID, toolUseID))
	if errors.Is(err, sql.ErrNoRows) {
		return team.Approval{}, false, nil
	}
	if err != nil {
		return team.Approval{}, false, fmt.Errorf("open approval by tool use %s/%s: %w", sessionID, toolUseID, err)
	}
	return a, true, nil
}

// OpenTerminalOnlyBySession returns the open terminal_only hook rows of a
// session, oldest first (the settings-hook backstop closes them by
// tool_use_id, tool_name, or all at once on Stop).
func (s *Store) OpenTerminalOnlyBySession(sessionID string) ([]team.Approval, error) {
	rows, err := s.db.Query(`SELECT `+selectCols+` FROM approval_requests
		WHERE origin_session_id = ? AND state = 'open' AND kind IN ('hook_ask', 'hook_permission')
		  AND json_extract(payload_json, '$.terminal_only') = 1
		ORDER BY created_at, id`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("open terminal_only approvals of %s: %w", sessionID, err)
	}
	defer rows.Close()
	out := []team.Approval{}
	for rows.Next() {
		a, _, err := scanRow(rows)
		if err != nil {
			return nil, fmt.Errorf("open terminal_only approvals of %s: %w", sessionID, err)
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("open terminal_only approvals of %s: %w", sessionID, err)
	}
	return out, nil
}

// OverrideIfApproved is the second CAS of spec §6.6 step 5: a hook row a
// remote decide already closed as approved becomes terminal_override,
// carrying the terminal's answers and decided_by terminal. The UPDATE is
// guarded by state='approved', so a row closed any other way is left as it
// is (won=false) and the caller answers with it.
func (s *Store) OverrideIfApproved(id string, decidedAt int64, hook *team.HookDecision) (team.Approval, bool, error) {
	var hookJSON any
	if hook != nil {
		b, err := json.Marshal(hook)
		if err != nil {
			return team.Approval{}, false, fmt.Errorf("encode hook decision: %w", err)
		}
		hookJSON = string(b)
	}
	by, err := json.Marshal(team.Client{Kind: team.ClientKindTerminal, Label: team.ClientKindTerminal})
	if err != nil {
		return team.Approval{}, false, fmt.Errorf("encode decided_by: %w", err)
	}
	res, err := s.db.Exec(`
		UPDATE approval_requests
		SET state = ?, decided_at = ?, decided_by_json = ?, grant_json = ?
		WHERE id = ? AND state = 'approved' AND kind IN ('hook_ask', 'hook_permission')`,
		string(team.StateTerminalOverride), decidedAt, string(by), hookJSON, id)
	if err != nil {
		return team.Approval{}, false, fmt.Errorf("override approval %s: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return team.Approval{}, false, fmt.Errorf("override approval %s rows affected: %w", id, err)
	}
	a, _, err := s.getRow(id)
	if err != nil {
		return team.Approval{}, false, err
	}
	return a, n == 1, nil
}

// ListOpenNonHook is ListOpen without the hook kinds: what a restart would
// interrupt (GET /api/team/inflight). A hook row rides out a restart: its
// poller is restart-aware and its lease gets the boot grace.
func (s *Store) ListOpenNonHook() ([]team.Approval, error) {
	all, err := s.ListOpen()
	if err != nil {
		return nil, err
	}
	out := []team.Approval{}
	for _, a := range all {
		if !team.IsHookKind(a.Kind) {
			out = append(out, a)
		}
	}
	return out, nil
}
```

  Edits in `internal/module/team/store.go` (exact diff):

```diff
@@ -89,9 +89,19 @@
 		}
 	}
 	if grant.Valid {
-		a.Grant = new(team.Grant)
-		if err := json.Unmarshal([]byte(grant.String), a.Grant); err != nil {
-			return team.Approval{}, "", fmt.Errorf("decode grant of %s: %w", a.ID, err)
+		// grant_json holds the Grant of a lead row and, in its place, the
+		// HookDecision of a hook row (the wire says the decision "rides in
+		// Grant's place"); the kind says which (P8a).
+		if team.IsHookKind(a.Kind) {
+			a.Hook = new(team.HookDecision)
+			if err := json.Unmarshal([]byte(grant.String), a.Hook); err != nil {
+				return team.Approval{}, "", fmt.Errorf("decode hook decision of %s: %w", a.ID, err)
+			}
+		} else {
+			a.Grant = new(team.Grant)
+			if err := json.Unmarshal([]byte(grant.String), a.Grant); err != nil {
+				return team.Approval{}, "", fmt.Errorf("decode grant of %s: %w", a.ID, err)
+			}
 		}
 	}
 	return a, hash, nil
@@ -155,6 +165,7 @@
 	DecidedAt int64
 	DecidedBy *team.Client
 	Grant     *team.Grant
+	Hook      *team.HookDecision // hook kinds: stored in grant_json in Grant's place
 }
 
 // CloseIfOpen is the compare-and-set every close goes through: the UPDATE
@@ -201,6 +212,13 @@
 		}
 		grant = string(b)
 	}
+	if c.Hook != nil {
+		b, err := json.Marshal(c.Hook)
+		if err != nil {
+			return team.Approval{}, false, fmt.Errorf("encode hook decision: %w", err)
+		}
+		grant = string(b)
+	}
 	args := []any{string(c.State), c.DecidedAt, decidedBy, grant, id}
 	if guard != "" {
 		args = append(args, guardArg)
```

- [ ] **Step 4: Run the tests and verify they pass.**
  - Run: `go test ./internal/module/team/ -run 'TestStore' -v`
  - Expected: PASS for the four new tests and every existing `TestStore_*` (the lead row's `Grant` decode is unchanged).

- [ ] **Step 5: Commit.**
  ```bash
  git add internal/module/team/store_ask.go internal/module/team/store_ask_test.go internal/module/team/store.go
  git commit -m "feat(team): store the hook decision in Grant's place; open-by-tool-use, override and non-hook queries

  Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
  ```

### Task 8a.3: `RemoteResponders` (WS half) and `modPresent` over P5a-2a's `modSeen`

> Line numbers in this task's `module.go` hunks are against alpha.527; **re-verify them after P2c-1 and P5a-2a merge** (P2c-1 adds `dataDir` and the decide route, P5a-2a adds `switches` / `titles` / `newID` / `relayDir` / `modSeen` to the same struct and `Init`). Apply by anchor text (the field or line quoted), not by line.

**Files:**
- Create: `internal/module/team/responders.go`
- Modify: `internal/module/team/module.go:33-37` (field `responders`), `:106-111` (`Init`: default `wsResponders`)
- Test: `internal/module/team/responders_test.go`; the behaviour at the routes is pinned by Task 8a.5's `TestAskBegin_NoRespondersOpensNothing` and Task 8a.8's `TestObserve_ModPresentOrNoRespondersOpensNothing_StopDismissesAll`
- **Not here (single owners, coordinator decision):** `ResolveOriginBySession` — the peers resolver's method is **P5a-1b** (`internal/module/peers/origin_resolver.go`, test `origin_resolver_session_test.go`), the `OriginResolver` interface line and `fakeOrigins.ResolveOriginBySession` in `handler_test.go` are **P5a-2a** (Task 5a.7). P8a only calls `m.origins.ResolveOriginBySession(sid)` (Task 8a.5). Mod presence — the map is **P5a-2a's** `m.modSeen map[string]helloInfo` (written by the relay `hello` handler, under `m.mu`); this task adds the read-only `modPresent` and declares no map, no mutex, no registry key.

**Interfaces:**
- Produces:
  ```go
  // internal/module/team
  type RemoteResponders interface { Any() bool }          // WS half: core.Events.HasSubscribers() (M23); push registry is the iOS line's
  func (m *Module) modPresent(sessionID string) bool       // reads P5a-2a's m.modSeen under m.mu
  ```
- Consumes: `core.EventsBroadcaster.HasSubscribers` (`internal/core/events.go:210-214`); P5a-2a's `m.modSeen` and `POST /api/relay/hello`; P5a-1b/P5a-2a's `OriginResolver.ResolveOriginBySession(sessionID string) (team.Origin, bool, error)` (ok=false for an unknown, dead or proxy entry; err only when the registry could not be read — the contract Task 8a.5 maps to 404 / 503).

- [ ] **Step 1: Write the failing test.**

```go
package teammod

import (
	"net/http"
	"testing"

	"github.com/wake/purdex/internal/team"
)

// modPresent reads the one presence record the relay hello handler keeps
// (P5a-2a's modSeen): a session that said hello is present, any other is not.
func TestModPresent_ReadsTheHelloRecord(t *testing.T) {
	f := newFixture(t)
	if f.m.modPresent("sid-1") || f.m.modPresent("") {
		t.Fatal("nobody said hello yet")
	}
	if code, body := f.do(http.MethodPost, "/api/relay/hello", team.RelayHelloRequest{SessionID: "sid-1", ModVersion: "1", Agent: "cc"}); code != http.StatusOK {
		t.Fatalf("hello: %d %s", code, body)
	}
	if !f.m.modPresent("sid-1") || f.m.modPresent("sid-2") {
		t.Fatal("hello must make exactly that session present")
	}
}

// The WS half of RemoteResponders is the host-events subscriber set.
func TestWSResponders_FollowsTheSubscriberSet(t *testing.T) {
	f := newFixture(t)
	if !f.m.responders.Any() {
		t.Fatal("the fixture subscribes one test client: Any() must be true")
	}
	f.core.Events.RemoveTestSubscriber(f.sub)
	if f.m.responders.Any() {
		t.Fatal("no subscriber: Any() must be false")
	}
	if (wsResponders{}).Any() {
		t.Fatal("a nil broadcaster has no responders")
	}
}
```

- [ ] **Step 2: Run the test and verify it fails.**
  - Run: `go test ./internal/module/team/ -run 'TestModPresent|TestWSResponders' -v`
  - Expected: FAIL to compile — `f.m.modPresent undefined`, `f.m.responders undefined`, `undefined: wsResponders`.

- [ ] **Step 3: Implement.** Create `internal/module/team/responders.go`:

```go
package teammod

import "github.com/wake/purdex/internal/core"

// RemoteResponders answers whether anyone remote could answer a 分流 row
// right now (spec §6.6 step 1, air26 point 1): a connected host-events
// subscriber — an App or a phone viewing this host (M23) — or, once the iOS
// line adds it, a device registered for push. P8a ships the WS half only.
type RemoteResponders interface {
	Any() bool
}

// wsResponders is the WS half: the /ws/host-events subscriber set.
type wsResponders struct{ events *core.EventsBroadcaster }

func (r wsResponders) Any() bool { return r.events != nil && r.events.HasSubscribers() }

// modPresent reports whether the session's mod said hello: it reads the
// one presence record, P5a-2a's modSeen (written by handleRelayHello under
// m.mu). A PreToolUse/AskUserQuestion from a present session opens no
// terminal_only row — the mod raises its own (spec §6.6, U19 point 4).
func (m *Module) modPresent(sessionID string) bool {
	if sessionID == "" {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.modSeen[sessionID]
	return ok
}
```

  Edits in `internal/module/team/module.go` (diff against alpha.527 — **take only the hunks for the field and `Init` here; the `RegisterRoutes` hunk belongs to Task 8a.5**; the `OriginResolver` interface line and the `modSeen` field are already there from P5a-2a and are NOT added again):

```diff
@@ -33,8 +36,13 @@
 	core    *core.Core
 	store   *Store
 	origins OriginResolver
-	now     func() int64 // unix ms; injectable for tests
-	logf    func(format string, args ...any)
+	// responders answers "can anyone remote answer a hook_ask right now?"
+	// (spec §6.6 step 1): the WS half (core.Events.HasSubscribers) in P8a;
+	// the iOS line adds the push registry behind the same interface.
+	responders RemoteResponders
+	now        func() int64 // unix ms; injectable for tests
+	logf       func(format string, args ...any)
 
 	// stopCtx is cancelled first in Stop: long-polls return, the sweeper
 	// exits and POST create answers 503 not_ready. The DB stays open until
@@ -108,6 +121,9 @@
 		return fmt.Errorf("team: %w", err)
 	}
 	m.store = store
+	if m.responders == nil {
+		m.responders = wsResponders{events: c.Events}
+	}
 	return nil
 }
 
@@ -119,6 +136,9 @@
 	mux.HandleFunc("DELETE /api/team/approvals/{id}", m.handleDelete)
 	mux.HandleFunc("POST /api/team/approvals/{id}/decide", m.handleDecide)
 	mux.HandleFunc("GET /api/team/inflight", m.handleInflight)
+	mux.HandleFunc("POST /api/ask/begin", m.handleAskBegin)
+	mux.HandleFunc("GET /api/ask/wait/{id}", m.handleAskWait)
+	mux.HandleFunc("POST /api/ask/report/{id}", m.handleAskReport)
 }
 
 // Start applies the boot lease grace (spec §9.2: every open request's
```

  `internal/module/team/handler_test.go` is **not** edited: `fakeOrigins.ResolveOriginBySession` (the fixture table keyed by session id, `readErr` → `errors.New("read registry: …")`) is P5a-2a's (Task 5a.7) and the P8a tests use it as is.

- [ ] **Step 4: Run the tests and verify they pass.**
  - Run: `go build ./... && go vet ./internal/module/team/ && go test ./internal/module/team/`
  - Expected: build and vet clean; the package PASSes (`TestModPresent_ReadsTheHelloRecord`, `TestWSResponders_FollowsTheSubscriberSet` included).

- [ ] **Step 5: Commit.**
  ```bash
  git add internal/module/team/responders.go internal/module/team/responders_test.go internal/module/team/module.go
  git commit -m "feat(team): RemoteResponders (WS half) and modPresent over the relay hello record

  Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
  ```

### Task 8a.4: SPA tolerance — the kinds parse, the Mac App draws no card (U19 (b))

**Decision (asked for in the handoff): the hook kinds are validated at the trust boundary and dropped before the store.** `isApproval` accepts them (a snapshot carrying one is **not** malformed), `handleApprovalEvent` filters them out of a snapshot and ignores their `opened` / `closed`. The store never holds them, so `selectOpenCountFor` (the restart confirm's fallback count), `selectCurrent` (the dialog), `notifyApprovalOpened` and the "closed elsewhere" toasts never see them — the terminal the App displays is the answer surface, and a hook row is not a 申請 awaiting the user. The daemon's `GET /api/team/inflight` excludes them too (Task 8a.5), so the restart confirm's primary count agrees. No locale keys: the Mac App shows no string for these rows.

> `:line`s below are against alpha.527; **re-verify them after P5a-3b merges** (it adds `SelfRelayPayload` / `selfRelayPayloadOf` and the `self_relay` kind to `types.ts` — the `ApprovalKind` union already reads `'lead' | 'self_relay'` then — and extends `approval-api.ts` / `approval-notify.ts`). Apply by anchor text, not by line.

**Files:**
- Modify: `spa/src/lib/team/types.ts:10` (`ApprovalKind`), `:12` (`ApprovalState`), `:78-82` (`Client.kind`), `:84-100` (`Approval.hook?`), `:134-135` (the validator's lists), `:159-173` (`isApproval` accepts `hook`); `spa/src/lib/team/approval-ws.ts:19` (import), `:67-70` (`handleApprovalEvent` filters)
- Test: `spa/src/lib/team/approval-ws.hooks.test.ts`

**Interfaces:**
- Produces (TS):
  ```ts
  export type HookKind = 'hook_ask' | 'hook_permission'
  export type ApprovalKind = 'lead' | 'self_relay' | HookKind
  export type ApprovalState = 'open' | 'approved' | 'denied' | 'timeout' | 'cancelled' | 'abandoned' | 'answered_local' | 'terminal_override' | 'dismissed'
  export const HOOK_KINDS: readonly string[]
  export const isHookKind: (kind: string) => kind is HookKind
  export interface Client { kind: 'app' | 'terminal'; label: string; addr?: string }
  export interface Approval { …; hook?: Record<string, unknown> }
  ```
- Consumes: `useApprovalStore`, `parseApprovalEvent` as shipped. `approvalKindLabel` (`approval-format.ts:29-31`) and `ApprovalDialogHost` need no change: a hook kind never reaches them (`tsc` confirms the widened union compiles).

- [ ] **Step 1: Write the failing test.**

```ts
// spa/src/lib/team/approval-ws.hooks.test.ts — the 分流 kinds on the wire (lead-team spec §6.6, U19 (b)): a
// hook_ask / hook_permission row is a valid approval (a snapshot carrying one is not malformed and must not be
// dropped whole), but the Mac App draws no card for it — it never reaches the store, the toast or the notification.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { useApprovalStore, selectOpenCountFor } from '../../stores/useApprovalStore'
import { useHostStore } from '../../stores/useHostStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { useUndoToast } from '../../stores/useUndoToast'
import { handleApprovalEvent, parseApprovalEvent } from './approval-ws'
import { isApproval, isHookKind, type Approval } from './types'

const H = 'h1'
const lead = (over: Partial<Approval> = {}): Approval => ({
  id: 'req-1', kind: 'lead', host_id: 'd1',
  origin: { session_id: 'S1', ref: '_40iueq', name: 'purdex-7c', pid: 1, proc_start: 'p', cwd: '/w/purdex', tmux: '' },
  payload: { reason: 'r', max_members: 3, roots: ['/w/purdex'] },
  state: 'open', created_at: 1_000, deadline_at: 541_000, lease_until: 31_000,
  ...over,
})
const ask = (over: Partial<Approval> = {}): Approval => lead({
  id: 'ask-1', kind: 'hook_ask',
  payload: { tool_use_id: 'toolu_1', questions: [{ question: '紅還是藍？', header: '顏色', options: [{ label: '紅' }, { label: '藍' }], multiSelect: false }] },
  deadline_at: 32503680000000,
  ...over,
})

beforeEach(() => {
  useI18nStore.getState().setLocale('zh-TW')
  useApprovalStore.getState().reset()
  useHostStore.setState({
    hosts: { [H]: { id: H, name: 'mlab', ip: '1.2.3.4', port: 7860, order: 0 } },
    hostOrder: [H],
    runtime: { [H]: { status: 'connected' } },
    activeHostId: H,
  })
  useUndoToast.setState({ toast: null, notice: null })
  Object.defineProperty(window, 'electronAPI', { value: { showNotification: vi.fn() }, writable: true, configurable: true })
})
afterEach(() => useHostStore.getState().reset())

describe('approval-ws: 分流 kinds (spec §6.6, U19 (b))', () => {
  it('the wire shape accepts the two kinds, the three close states, decided_by terminal and `hook`', () => {
    expect(isHookKind('hook_ask') && isHookKind('hook_permission') && !isHookKind('lead')).toBe(true)
    expect(isApproval(ask())).toBe(true)
    expect(isApproval(ask({ kind: 'hook_permission', payload: { tool_use_id: '', tool_name: 'Bash', tool_input: {} } }))).toBe(true)
    for (const state of ['answered_local', 'terminal_override', 'dismissed'] as const) {
      expect(isApproval(ask({ state, decided_by: { kind: 'terminal', label: 'terminal' }, decided_at: 5, hook: { answers: { '紅還是藍？': '紅' } } }))).toBe(true)
    }
    expect(isApproval(ask({ hook: 'x' as unknown as Record<string, unknown> }))).toBe(false)
  })

  it('a snapshot with a hook_ask beside a lead request is NOT dropped whole: the lead request is held, the hook row is not', () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    handleApprovalEvent(H, JSON.stringify({ op: 'snapshot', approvals: [ask(), lead()] }))
    expect(warn).not.toHaveBeenCalled()
    warn.mockRestore()
    const entries = Object.values(useApprovalStore.getState().entries)
    expect(entries.map((e) => e.approval.id)).toEqual(['req-1'])
    expect(selectOpenCountFor(H)(useApprovalStore.getState())).toBe(1)
  })

  it('parseApprovalEvent still returns the hook rows (the boundary validates them); handleApprovalEvent ignores opened and closed for them', () => {
    expect(parseApprovalEvent(JSON.stringify({ op: 'opened', approval: ask() }))).toEqual({ op: 'opened', approval: ask() })
    handleApprovalEvent(H, JSON.stringify({ op: 'opened', approval: ask() }))
    expect(useApprovalStore.getState().entries).toEqual({})
    expect(window.electronAPI?.showNotification).not.toHaveBeenCalled()
    const closed = ask({ state: 'answered_local', decided_by: { kind: 'terminal', label: 'terminal' }, decided_at: 5, hook: { answers: { '紅還是藍？': '紅' } } })
    handleApprovalEvent(H, JSON.stringify({ op: 'closed', approval: closed }))
    expect(useUndoToast.getState().toast).toBeNull()
    const remote = ask({ state: 'approved', decided_by: { kind: 'app', label: 'Purdex iOS @ phone' }, decided_at: 5, hook: { answers: { '紅還是藍？': '藍' } } })
    handleApprovalEvent(H, JSON.stringify({ op: 'closed', approval: remote }))
    expect(useUndoToast.getState().toast).toBeNull()
    expect(useApprovalStore.getState().closedIds[H]).toBeUndefined()
  })
})
```

- [ ] **Step 2: Run the test and verify it fails.**
  - Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/lead-team-p8a/spa && npx vitest run src/lib/team/approval-ws.hooks.test.ts`
  - Expected: FAIL — `isHookKind` is not exported (`SyntaxError: The requested module './types' does not provide an export named 'isHookKind'`); after adding only the export, the second test fails with `expected [] to deeply equal ['req-1']` (the snapshot is rejected whole and warns once).

- [ ] **Step 3: Implement.** `spa/src/lib/team/types.ts` (exact diff):

```diff
@@ -7,10 +7,16 @@
 /** `HostEvent.type` of every approval event (`team.EventType`). */
 export const APPROVAL_EVENT_TYPE = 'approval.request'
 
-export type ApprovalKind = 'lead' | 'self_relay'
+/** The two 分流 kinds (spec §6.6, U19) ride the same event; the Mac App draws no card for them (U19 (b)) and drops them at the WS boundary (approval-ws.ts). */
+export type HookKind = 'hook_ask' | 'hook_permission'
+export type ApprovalKind = 'lead' | 'self_relay' | HookKind
 
-export type ApprovalState = 'open' | 'approved' | 'denied' | 'timeout' | 'cancelled' | 'abandoned'
+/** `answered_local`, `terminal_override` and `dismissed` close hook kinds only; `approved` on a hook kind means "answered remotely". */
+export type ApprovalState = 'open' | 'approved' | 'denied' | 'timeout' | 'cancelled' | 'abandoned' | 'answered_local' | 'terminal_override' | 'dismissed'
 
+export const HOOK_KINDS: readonly string[] = ['hook_ask', 'hook_permission'] satisfies HookKind[]
+export const isHookKind = (kind: string): kind is HookKind => HOOK_KINDS.includes(kind)
+
 /** Spec §6.1–§6.2 limits (`team.DefaultMaxMembers`, `team.MaxMaxMembers`). */
 export const DEFAULT_MAX_MEMBERS = 3
 export const MAX_MAX_MEMBERS = 8
@@ -76,7 +82,8 @@
 
 /** The audit label of whoever decided (spec §6.5). `addr` is set by the daemon from RemoteAddr. */
 export interface Client {
-  kind: 'app'
+  /** `terminal` on the closes the terminal made (answered_local, terminal_override). */
+  kind: 'app' | 'terminal'
   label: string
   addr?: string
 }
@@ -97,6 +104,8 @@
   decided_at?: number
   /** approved only. */
   grant?: Grant
+  /** Hook kinds only: the answer (`answers` for hook_ask; `behavior` for hook_permission). Never read by the Mac App. */
+  hook?: Record<string, unknown>
 }
 
 /** `POST /api/team/approvals/{id}/decide`. `grant` is approve-only; absent → the payload's values. */
@@ -131,8 +140,8 @@
   return typeof v === 'object' && v !== null && !Array.isArray(v)
 }
 
-const APPROVAL_KINDS: readonly string[] = ['lead', 'self_relay'] satisfies ApprovalKind[]
-const APPROVAL_STATES: readonly string[] = ['open', 'approved', 'denied', 'timeout', 'cancelled', 'abandoned'] satisfies ApprovalState[]
+const APPROVAL_KINDS: readonly string[] = ['lead', 'self_relay', 'hook_ask', 'hook_permission'] satisfies ApprovalKind[]
+const APPROVAL_STATES: readonly string[] = ['open', 'approved', 'denied', 'timeout', 'cancelled', 'abandoned', 'answered_local', 'terminal_override', 'dismissed'] satisfies ApprovalState[]
 
 /** Absent (`omitempty`) or of the given type; `null` is neither. */
 const optional = (v: unknown, ok: (x: unknown) => boolean): boolean => v === undefined || ok(v)
@@ -170,6 +179,7 @@
     && optional(v.decided_by, isRecord)
     && optional(v.decided_at, isNumber)
     && optional(v.grant, isRecord)
+    && optional(v.hook, isRecord)
 }
 
 /** The lead payload, normalised as the daemon normalises it: `max_members` 0 → 3, cap 8; roots default `[origin.cwd]`. */
```

  `spa/src/lib/team/approval-ws.ts` (exact diff):

```diff
@@ -16,7 +16,7 @@
 import { submitDecision, toastClosed } from './approval-decide'
 import { approvalKindLabel, approvalSessionLabel } from './approval-format'
 import { notifyApprovalOpened } from './approval-notify'
-import { isApproval, type Approval, type ApprovalEventValue } from './types'
+import { isApproval, isHookKind, type Approval, type ApprovalEventValue } from './types'
 
 function isRecord(v: unknown): v is Record<string, unknown> {
   return typeof v === 'object' && v !== null && !Array.isArray(v)
@@ -64,8 +64,19 @@
   }))
 }
 
+/**
+ * The 分流 kinds (hook_ask, hook_permission; spec §6.6, U19 (b)) are valid wire shapes — a snapshot carrying one is
+ * NOT malformed — but the Mac App shows no card for them: the terminal it displays is the answer surface. They are
+ * dropped here, before the store, so no dialog, toast, notification or restart-confirm count ever sees them.
+ */
+function withoutHookKinds(ev: ApprovalEventValue): ApprovalEventValue | null {
+  if (ev.op === 'snapshot') return { op: 'snapshot', approvals: ev.approvals.filter((a) => !isHookKind(a.kind)) }
+  return isHookKind(ev.approval.kind) ? null : ev
+}
+
 export function handleApprovalEvent(hostId: string, value: unknown): void {
-  const ev = parseApprovalEvent(value)
+  const parsed = parseApprovalEvent(value)
+  const ev = parsed ? withoutHookKinds(parsed) : null
   if (!ev) return
   const store = useApprovalStore.getState()
   if (ev.op === 'snapshot') {
```

- [ ] **Step 4: Run the tests and verify they pass.**
  - Run: `npx vitest run src/lib/team/ src/stores/useApprovalStore.test.ts src/components/ApprovalDialogHost.test.tsx && npx tsc -p tsconfig.app.json --noEmit && pnpm run lint && pnpm run build`
  - Expected: 7 files / 103+3 tests PASS (the existing `rejects an approval with kind unknown` case still passes: `boss` is still unknown); tsc, lint and build clean.

- [ ] **Step 5: Commit.**
  ```bash
  git add spa/src/lib/team/types.ts spa/src/lib/team/approval-ws.ts spa/src/lib/team/approval-ws.hooks.test.ts
  git commit -m "feat(spa): accept the hook_ask / hook_permission kinds on the wire and draw no card for them (U19 (b))

  Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
  ```

---

## PR P8a-1b — `/api/ask/*`, `decide` on hook kinds, inflight (≈ 760 lines, 3 files)

### Task 8a.5: The ask routes and the hook branch of `decide`

> `:line`s and the `handler.go` diff below are against alpha.527; **re-verify them after P2c-1, P5a-2a and P5a-2b merge** and apply the hunks **over P5a-2b's `handleInflight`** (it fills `relays_active` from the open relay ops — keep that; only `ListOpen()` → `ListOpenNonHook()` changes) **and over P5a-2a's `handleDecide`** (its grant guard already reads `state == team.StateApproved && a.Kind == team.KindLead`; the `IsHookKind` branch goes right above it). Apply by anchor text, not by line.

**Files:**
- Create: `internal/module/team/ask_handler.go`
- Modify: `internal/module/team/module.go:115-122` (`RegisterRoutes`: three routes — the `RegisterRoutes` hunk of Task 8a.3's diff, after P5a-2a's four relay routes and P5a-2b's two), `internal/module/team/handler.go:14` (drop the `time` import), `:247-255` (`handleInflight` uses `ListOpenNonHook`; P5a-2b's `RelaysActive` fill stays), `:271-324` (`handleGet` becomes a thin wrapper over `pollRow`), `:383-386` (`handleDecide` branches to `decideHook` after the "already closed" check and before P5a-2a's grant guard)
- Test: `internal/module/team/ask_handler_test.go`

**Interfaces:**
- Produces (routes, all `TokenAuth` like `/api/team/*`):
  ```
  POST /api/ask/begin        AskBeginRequest → 201 AskBeginResponse
                             | 400 bad_request | 404 unknown_session | 409 no_responders | 409 ask_open (carries the open Approval) | 503 not_ready
  GET  /api/ask/wait/{id}?wait=25 → 200 AskWaitResponse ({state:still_open} | {state:answered_remote, hook} | {state:closed, reason})
                             | 400 (not a hook row / bad wait) | 404 not_found | 503 not_ready (lease renewal failed)
  POST /api/ask/report/{id}  AskReportRequest{state: answered_local|dismissed, hook?} → 200 Approval (as it now is; idempotent)
                             | 400 | 404
  POST /api/team/approvals/{id}/decide on a hook row: approve + hook.answers (hook_ask) / approve|deny (hook_permission) → 200;
                             409 terminal_only (detail 這題只能在終端機回答) for a read-only card; 400 for deny on hook_ask or missing answers
  GET  /api/team/inflight    approvals_open now counts lead/self_relay only
  ```
  ```go
  func (m *Module) pollRow(w http.ResponseWriter, r *http.Request, id string) (team.Approval, bool)   // the long-poll shared by handleGet and handleAskWait
  func (m *Module) openHookRow(origin team.Origin, kind team.Kind, payload []byte, terminalOnly bool) (team.Approval, error) // callers hold createMu
  func hookPayloadFor(kind team.Kind, toolUseID string, raw json.RawMessage, terminalOnly bool) ([]byte, string)
  func isTerminalOnly(a team.Approval) bool
  func askWaitOf(a team.Approval) team.AskWaitResponse
  func terminalClient() *team.Client
  func (m *Module) decideHook(w http.ResponseWriter, a team.Approval, req team.DecideRequest, state team.State, client team.Client)
  ```
- Consumes: Tasks 8a.1–8a.3; `closeAs` / `broadcast` / `addWaiter` / `removeWaiter` / `createMu` / `stopping` as shipped (`module.go:177-302`).
- **Rows:** `DeadlineAt = NoExpiryAt` always (the row lives as long as the native dialog, spec §6.6 "no wait of its own"); `LeaseUntil = now + 30 s` for a mod-raised row (renewed by every `/api/ask/wait`; a mod that dies with the dialog up is abandoned by the shipped sweeper within 30 s), `NoExpiryAt` for a terminal_only row (nobody polls it). The sweeper's deadline and lease checks (`sweeper.go:64-67`) never trip on `NoExpiryAt`; its liveness check (`:68`) still abandons a row whose session is gone (spec step 7). The boot grace (`ExtendOpenLeases`) extends a mod row's lease like any other.
- **One open row per `(session, tool_use_id)`:** a second `begin` is `409 ask_open` carrying the row (the CLI adopts it, Task 8a.6). A `begin` that meets an open **terminal_only** row for its tool use (the settings hook got there first because the mod's hello was missed) **takes it over**: the read-only row is closed `dismissed` and a fresh answerable row is opened — two ordinary events, no new op.

- [ ] **Step 1: Write the failing tests.**

```go
package teammod

import (
	"encoding/json"
	"net/http"
	"sync"
	"testing"

	"github.com/wake/purdex/internal/team"
)

const askQuestions = `[{"question":"紅還是藍？","header":"顏色","options":[{"label":"紅","description":"r"},{"label":"藍","description":"b"}],"multiSelect":false}]`

func askBeginBody(sid, toolUse string) team.AskBeginRequest {
	return team.AskBeginRequest{SessionID: sid, ToolUseID: toolUse, Kind: team.KindHookAsk, Payload: json.RawMessage(`{"questions":` + askQuestions + `}`)}
}

// askBegin opens a hook_ask for sid-1 / toolUse and returns its id.
func (f *fixture) askBegin(toolUse string) string {
	f.t.Helper()
	code, body := f.do(http.MethodPost, "/api/ask/begin", askBeginBody("sid-1", toolUse))
	if code != http.StatusCreated {
		f.t.Fatalf("ask begin: %d %s", code, body)
	}
	var out team.AskBeginResponse
	if err := json.Unmarshal(body, &out); err != nil || out.ID == "" {
		f.t.Fatalf("ask begin body %s: %v", body, err)
	}
	return out.ID
}

func decodeWait(t *testing.T, body []byte) team.AskWaitResponse {
	t.Helper()
	var w team.AskWaitResponse
	if err := json.Unmarshal(body, &w); err != nil {
		t.Fatalf("decode wait: %v; body=%s", err, body)
	}
	return w
}

func appClient() team.Client { return team.Client{Kind: "app", Label: "Purdex iOS @ phone"} }

// Mutation gate: with no subscriber RemoteResponders.Any() is false, begin
// answers 409 no_responders and opens nothing (invert Any ⇒ red).
func TestAskBegin_NoRespondersOpensNothing(t *testing.T) {
	f := newFixture(t)
	f.core.Events.RemoveTestSubscriber(f.sub)
	code, body := f.do(http.MethodPost, "/api/ask/begin", askBeginBody("sid-1", "toolu_1"))
	if code != http.StatusConflict || decodeErr(t, body).Error != team.ErrNoResponders {
		t.Fatalf("begin without subscribers = %d %s", code, body)
	}
	open, err := f.m.store.ListOpen()
	if err != nil || len(open) != 0 {
		t.Fatalf("open rows = %v err=%v, want none", open, err)
	}
	f.sub = f.core.Events.AddTestSubscriber() // for the fixture's Cleanup
}

func TestAskBegin_OpensRowWithNoDeadlineAndBroadcasts(t *testing.T) {
	f := newFixture(t)
	id := f.askBegin("toolu_1")
	a, ok, err := f.m.store.Get(id)
	if err != nil || !ok {
		t.Fatalf("get: ok=%v err=%v", ok, err)
	}
	if a.Kind != team.KindHookAsk || a.State != team.StateOpen || a.Origin.SessionID != "sid-1" || a.DeadlineAt != team.NoExpiryAt || a.LeaseUntil != f.clock.Load()+team.LeaseS*1000 {
		t.Fatalf("row = %+v", a)
	}
	var p team.HookAskPayload
	if err := json.Unmarshal(a.Payload, &p); err != nil || p.ToolUseID != "toolu_1" || p.TerminalOnly || string(p.Questions) != askQuestions {
		t.Fatalf("payload = %s (%v)", a.Payload, err)
	}
	evs := f.events()
	if len(evs) != 1 || evs[0].Op != "opened" || evs[0].Approval.ID != id {
		t.Fatalf("events = %+v", evs)
	}
	// A second begin for the same tool use is 409 ask_open carrying the row.
	code, body := f.do(http.MethodPost, "/api/ask/begin", askBeginBody("sid-1", "toolu_1"))
	e := decodeErr(t, body)
	if code != http.StatusConflict || e.Error != team.ErrAskOpen || e.Approval == nil || e.Approval.ID != id {
		t.Fatalf("second begin = %d %s", code, body)
	}
	// Inflight does not count hook rows: a restart does not interrupt them.
	code, body = f.do(http.MethodGet, "/api/team/inflight", nil)
	var inf team.InflightResponse
	if err := json.Unmarshal(body, &inf); err != nil || code != 200 || inf.ApprovalsOpen != 0 {
		t.Fatalf("inflight = %d %s", code, body)
	}
}

func TestAskBegin_Rejections(t *testing.T) {
	f := newFixture(t)
	cases := []struct {
		name string
		body any
		code int
		err  string
	}{
		{"unknown session", askBeginBody("sid-9", "t"), http.StatusNotFound, team.ErrUnknownSession},
		{"no questions", team.AskBeginRequest{SessionID: "sid-1", ToolUseID: "t", Kind: team.KindHookAsk, Payload: json.RawMessage(`{"questions":[]}`)}, http.StatusBadRequest, team.ErrBadRequest},
		{"bad kind", team.AskBeginRequest{SessionID: "sid-1", ToolUseID: "t", Kind: team.KindLead}, http.StatusBadRequest, team.ErrBadRequest},
		{"no tool use", team.AskBeginRequest{SessionID: "sid-1", Kind: team.KindHookAsk, Payload: json.RawMessage(`{"questions":` + askQuestions + `}`)}, http.StatusBadRequest, team.ErrBadRequest},
		{"permission needs tool_name", team.AskBeginRequest{SessionID: "sid-1", ToolUseID: "t", Kind: team.KindHookPermission, Payload: json.RawMessage(`{}`)}, http.StatusBadRequest, team.ErrBadRequest},
	}
	for _, c := range cases {
		code, body := f.do(http.MethodPost, "/api/ask/begin", c.body)
		if code != c.code || decodeErr(t, body).Error != c.err {
			t.Errorf("%s: %d %s, want %d %s", c.name, code, body, c.code, c.err)
		}
	}
	if n := f.countOps("opened"); n != 0 {
		t.Fatalf("opened = %d, want 0", n)
	}
}

// Remote first (step 4): decide closes approved with the answers; wait
// answers answered_remote; a late answered_local is a terminal_override
// with a second closed (step 5).
func TestAsk_RemoteFirstThenLateTerminalIsOverride(t *testing.T) {
	f := newFixture(t)
	id := f.askBegin("toolu_2")
	f.events()
	code, body := f.do(http.MethodPost, "/api/team/approvals/"+id+"/decide",
		team.DecideRequest{Decision: "approve", Hook: &team.HookDecision{Answers: map[string]string{"紅還是藍？": "藍"}}, Client: appClient()})
	if code != http.StatusOK {
		t.Fatalf("decide = %d %s", code, body)
	}
	a := decodeApproval(t, body)
	if a.State != team.StateApproved || a.Hook == nil || a.Hook.Answers["紅還是藍？"] != "藍" || a.Grant != nil || a.DecidedBy.Label != "Purdex iOS @ phone" {
		t.Fatalf("decided row = %+v hook=%+v", a, a.Hook)
	}
	code, body = f.do(http.MethodGet, "/api/ask/wait/"+id+"?wait=25", nil)
	w := decodeWait(t, body)
	if code != 200 || w.State != team.AskAnsweredRemote || w.Hook == nil || w.Hook.Answers["紅還是藍？"] != "藍" {
		t.Fatalf("wait = %d %+v", code, w)
	}
	evs := f.events()
	if len(evs) != 1 || evs[0].Op != "closed" || evs[0].Approval.State != team.StateApproved {
		t.Fatalf("events after decide = %+v", evs)
	}
	// The terminal had already shown 紅: its answer stands.
	code, body = f.do(http.MethodPost, "/api/ask/report/"+id,
		team.AskReportRequest{State: team.StateAnsweredLocal, Hook: &team.HookDecision{Answers: map[string]string{"紅還是藍？": "紅"}}})
	a = decodeApproval(t, body)
	if code != 200 || a.State != team.StateTerminalOverride || a.Hook.Answers["紅還是藍？"] != "紅" || a.DecidedBy == nil || a.DecidedBy.Kind != team.ClientKindTerminal {
		t.Fatalf("override = %d %+v hook=%+v by=%+v", code, a, a.Hook, a.DecidedBy)
	}
	evs = f.events()
	if len(evs) != 1 || evs[0].Op != "closed" || evs[0].Approval.State != team.StateTerminalOverride || evs[0].Approval.DecidedBy.Kind != team.ClientKindTerminal {
		t.Fatalf("second closed = %+v", evs)
	}
	// A repeat changes nothing and broadcasts nothing.
	code, body = f.do(http.MethodPost, "/api/ask/report/"+id,
		team.AskReportRequest{State: team.StateAnsweredLocal, Hook: &team.HookDecision{Answers: map[string]string{"紅還是藍？": "紅"}}})
	if code != 200 || decodeApproval(t, body).State != team.StateTerminalOverride || f.countOps("closed") != 0 {
		t.Fatalf("repeat = %d %s", code, body)
	}
}

// Terminal first (step 3): answered_local closes with the terminal's
// answers and one closed; a late decide gets 409 already_decided carrying
// decided_by terminal.
func TestAsk_TerminalFirstThenLateDecideIs409(t *testing.T) {
	f := newFixture(t)
	id := f.askBegin("toolu_3")
	f.events()
	code, body := f.do(http.MethodPost, "/api/ask/report/"+id,
		team.AskReportRequest{State: team.StateAnsweredLocal, Hook: &team.HookDecision{Answers: map[string]string{"紅還是藍？": "紅"}}})
	a := decodeApproval(t, body)
	if code != 200 || a.State != team.StateAnsweredLocal || a.Hook.Answers["紅還是藍？"] != "紅" || a.DecidedBy.Kind != team.ClientKindTerminal {
		t.Fatalf("report = %d %+v", code, a)
	}
	if evs := f.events(); len(evs) != 1 || evs[0].Op != "closed" || evs[0].Approval.State != team.StateAnsweredLocal {
		t.Fatalf("events = %+v", evs)
	}
	code, body = f.do(http.MethodGet, "/api/ask/wait/"+id+"?wait=25", nil)
	if w := decodeWait(t, body); code != 200 || w.State != team.AskClosed || w.Reason != string(team.StateAnsweredLocal) {
		t.Fatalf("wait = %d %+v", code, w)
	}
	code, body = f.do(http.MethodPost, "/api/team/approvals/"+id+"/decide",
		team.DecideRequest{Decision: "approve", Hook: &team.HookDecision{Answers: map[string]string{"紅還是藍？": "藍"}}, Client: appClient()})
	e := decodeErr(t, body)
	if code != http.StatusConflict || e.Error != team.ErrAlreadyDecided || e.Approval == nil || e.Approval.DecidedBy == nil || e.Approval.DecidedBy.Kind != team.ClientKindTerminal {
		t.Fatalf("late decide = %d %s", code, body)
	}
	if f.countOps("closed") != 0 {
		t.Fatal("a lost decide must not broadcast")
	}
}

// Mutation gate (spec §15): a hook_ask row closes through the same CAS as a
// lead request — of a remote decide and a terminal report racing, exactly
// one wins; the loser sees the winner's row. Drop the CAS ⇒ both win ⇒ red.
func TestAsk_DecideAndReportRaceExactlyOneWins(t *testing.T) {
	for round := 0; round < 20; round++ {
		f := newFixture(t)
		id := f.askBegin("toolu_race")
		f.events()
		var wg sync.WaitGroup
		codes := make([]int, 2)
		wg.Add(2)
		go func() {
			defer wg.Done()
			codes[0], _ = f.do(http.MethodPost, "/api/team/approvals/"+id+"/decide",
				team.DecideRequest{Decision: "approve", Hook: &team.HookDecision{Answers: map[string]string{"q": "藍"}}, Client: appClient()})
		}()
		go func() {
			defer wg.Done()
			codes[1], _ = f.do(http.MethodPost, "/api/ask/report/"+id,
				team.AskReportRequest{State: team.StateDismissed})
		}()
		wg.Wait()
		// decide: 200 won / 409 lost. report dismissed: 200 either way (idempotent), so count closes by events.
		closes := 0
		for _, ev := range f.events() {
			if ev.Op == "closed" {
				closes++
			}
		}
		if closes != 1 {
			t.Fatalf("round %d: %d closed events, want exactly 1 (codes %v)", round, closes, codes)
		}
		a, _, _ := f.m.store.Get(id)
		if (codes[0] == 200) != (a.State == team.StateApproved) {
			t.Fatalf("round %d: decide=%d but state=%s", round, codes[0], a.State)
		}
	}
}

func TestAsk_DismissedClosesAndWaitSaysSo(t *testing.T) {
	f := newFixture(t)
	id := f.askBegin("toolu_4")
	f.events()
	code, body := f.do(http.MethodPost, "/api/ask/report/"+id, team.AskReportRequest{State: team.StateDismissed})
	if code != 200 || decodeApproval(t, body).State != team.StateDismissed {
		t.Fatalf("dismiss = %d %s", code, body)
	}
	if evs := f.events(); len(evs) != 1 || evs[0].Approval.State != team.StateDismissed || evs[0].Approval.DecidedBy != nil {
		t.Fatalf("events = %+v", evs)
	}
	code, body = f.do(http.MethodGet, "/api/ask/wait/"+id, nil)
	if w := decodeWait(t, body); code != 200 || w.State != team.AskClosed || w.Reason != "dismissed" {
		t.Fatalf("wait = %d %+v", code, w)
	}
	code, body = f.do(http.MethodPost, "/api/ask/report/"+id, team.AskReportRequest{State: "approved"})
	if code != http.StatusBadRequest {
		t.Fatalf("bad state = %d %s", code, body)
	}
}

func TestAsk_WaitLongPollWakesOnDecide(t *testing.T) {
	f := newFixture(t)
	id := f.askBegin("toolu_5")
	done := make(chan team.AskWaitResponse, 1)
	go func() {
		_, body := f.do(http.MethodGet, "/api/ask/wait/"+id+"?wait=25", nil)
		done <- decodeWait(t, body)
	}()
	waitForWaiter(t, f, id)
	f.do(http.MethodPost, "/api/team/approvals/"+id+"/decide",
		team.DecideRequest{Decision: "approve", Hook: &team.HookDecision{Answers: map[string]string{"紅還是藍？": "藍"}}, Client: appClient()})
	w := <-done
	if w.State != team.AskAnsweredRemote || w.Hook == nil {
		t.Fatalf("wait woke with %+v", w)
	}
}

func TestDecide_HookAskNeedsAnswers_TerminalOnlyIsReadOnly(t *testing.T) {
	f := newFixture(t)
	id := f.askBegin("toolu_6")
	code, body := f.do(http.MethodPost, "/api/team/approvals/"+id+"/decide", team.DecideRequest{Decision: "approve", Client: appClient()})
	if code != http.StatusBadRequest {
		t.Fatalf("approve without answers = %d %s", code, body)
	}
	code, body = f.do(http.MethodPost, "/api/team/approvals/"+id+"/decide", team.DecideRequest{Decision: "deny", Client: appClient()})
	if code != http.StatusBadRequest {
		t.Fatalf("deny a hook_ask = %d %s", code, body)
	}
	// A terminal_only row (opened by the settings hook path) cannot be decided.
	payload, _ := hookPayloadFor(team.KindHookAsk, "toolu_7", json.RawMessage(`{"questions":`+askQuestions+`}`), true)
	f.m.createMu.Lock()
	ro, err := f.m.openHookRow(fixtureOrigins["/tmp/10.sock"], team.KindHookAsk, payload, true)
	f.m.createMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if ro.LeaseUntil != team.NoExpiryAt {
		t.Fatalf("terminal_only lease = %d, want NoExpiryAt (nobody polls it)", ro.LeaseUntil)
	}
	code, body = f.do(http.MethodPost, "/api/team/approvals/"+ro.ID+"/decide",
		team.DecideRequest{Decision: "approve", Hook: &team.HookDecision{Answers: map[string]string{"q": "a"}}, Client: appClient()})
	if e := decodeErr(t, body); code != http.StatusConflict || e.Error != team.ErrTerminalOnly || e.Detail != "這題只能在終端機回答" {
		t.Fatalf("decide terminal_only = %d %s", code, body)
	}
	// The mod's begin for the same tool use takes it over: dismissed + a fresh answerable row.
	f.events()
	newID := f.askBegin("toolu_7")
	evs := f.events()
	if newID == ro.ID || len(evs) != 2 || evs[0].Op != "closed" || evs[0].Approval.ID != ro.ID || evs[0].Approval.State != team.StateDismissed || evs[1].Op != "opened" || evs[1].Approval.ID != newID {
		t.Fatalf("takeover events = %+v", evs)
	}
}

// The sweeper never times a hook row out, and a mod-raised row whose poller
// stopped is abandoned when its lease runs out (the mod died with the
// dialog still up); a terminal_only row is not (no lease).
func TestAsk_SweeperLeaseButNoDeadline(t *testing.T) {
	f := newFixture(t)
	id := f.askBegin("toolu_8")
	payload, _ := hookPayloadFor(team.KindHookAsk, "toolu_9", json.RawMessage(`{"questions":`+askQuestions+`}`), true)
	f.m.createMu.Lock()
	ro, _ := f.m.openHookRow(fixtureOrigins["/tmp/10.sock"], team.KindHookAsk, payload, true)
	f.m.createMu.Unlock()
	f.clock.Add(11 * 60 * 1000) // past any lead deadline and the 30 s lease
	f.m.tick()
	a, _, _ := f.m.store.Get(id)
	if a.State != team.StateAbandoned {
		t.Fatalf("mod row after lease = %s, want abandoned", a.State)
	}
	b, _, _ := f.m.store.Get(ro.ID)
	if b.State != team.StateOpen {
		t.Fatalf("terminal_only row = %s, want still open", b.State)
	}
}
```

- [ ] **Step 2: Run the tests and verify they fail.**
  - Run: `go test ./internal/module/team/ -run 'TestAsk|TestDecide_Hook' -v`
  - Expected: FAIL to compile — `undefined: hookPayloadFor`, `f.m.openHookRow undefined`, `undefined: isTerminalOnly`.

- [ ] **Step 3: Implement.** Create `internal/module/team/ask_handler.go`:

```go
package teammod

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/wake/purdex/internal/team"
)

// 分流 routes (spec §6.6 steps 1–7; wire: internal/team/wire_ask.go).
//
//	POST /api/ask/begin        the mod opens a hook row for the native dialog it is showing
//	GET  /api/ask/wait/{id}    the mod's bounded long-poll (renews the lease, like /api/team/approvals/{id})
//	POST /api/ask/report/{id}  the native dialog settled: answered_local (terminal first) or dismissed
//
// A remote client answers through the ordinary decide route (handleDecide →
// decideHook). Every close goes through closeAs, so a hook row competes in
// the same CAS as a lead request and broadcasts exactly one closed — plus
// the one deliberate second closed of a terminal_override.

// terminalClient is decided_by for a close the terminal made.
func terminalClient() *team.Client {
	return &team.Client{Kind: team.ClientKindTerminal, Label: team.ClientKindTerminal}
}

// hookPayloadFor validates the begin payload for kind and returns it
// normalised with tool_use_id and terminal_only set by the daemon. An error
// string names what is wrong (a 400).
func hookPayloadFor(kind team.Kind, toolUseID string, raw json.RawMessage, terminalOnly bool) ([]byte, string) {
	switch kind {
	case team.KindHookAsk:
		var p team.HookAskPayload
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &p); err != nil {
				return nil, "payload is not a hook_ask payload: " + err.Error()
			}
		}
		var qs []json.RawMessage
		if err := json.Unmarshal(p.Questions, &qs); err != nil || len(qs) == 0 {
			return nil, "payload.questions must be a non-empty array"
		}
		p.ToolUseID, p.TerminalOnly = toolUseID, terminalOnly
		b, err := json.Marshal(p)
		if err != nil {
			return nil, "encode payload: " + err.Error()
		}
		return b, ""
	case team.KindHookPermission:
		var p team.HookPermissionPayload
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &p); err != nil {
				return nil, "payload is not a hook_permission payload: " + err.Error()
			}
		}
		if strings.TrimSpace(p.ToolName) == "" {
			return nil, "payload.tool_name is required"
		}
		if len(p.ToolInput) == 0 {
			p.ToolInput = json.RawMessage(`{}`)
		}
		p.ToolUseID, p.TerminalOnly = toolUseID, terminalOnly
		b, err := json.Marshal(p)
		if err != nil {
			return nil, "encode payload: " + err.Error()
		}
		return b, ""
	default:
		return nil, "kind must be hook_ask or hook_permission"
	}
}

// openHookRow inserts and announces a hook row for origin. A terminal_only
// row has no lease (nobody polls it); a mod-raised one keeps the usual
// lease, renewed by each /api/ask/wait. Neither has a deadline (NoExpiryAt):
// the row lives as long as the native dialog. Callers hold createMu.
func (m *Module) openHookRow(origin team.Origin, kind team.Kind, payload []byte, terminalOnly bool) (team.Approval, error) {
	now := m.now()
	lease := now + team.LeaseS*1000
	if terminalOnly {
		lease = team.NoExpiryAt
	}
	id := uuid.NewString()
	a := team.Approval{
		ID: id, Kind: kind, HostID: m.hostID(), Origin: origin, Payload: payload, State: team.StateOpen,
		CreatedAt: now, DeadlineAt: team.NoExpiryAt, LeaseUntil: lease,
	}
	stored, _, inserted, err := m.store.Create(a, requestHash(kind, origin.SessionID, 0, payload))
	if err != nil {
		return team.Approval{}, err
	}
	if !inserted {
		return team.Approval{}, errors.New("fresh uuid already present")
	}
	m.logf("[team] approval %s opened: kind=%s origin=%s (%s) terminal_only=%v", stored.ID, kind, origin.Ref, origin.SessionID, terminalOnly)
	m.broadcast("opened", &stored)
	return stored, nil
}

// handleAskBegin is POST /api/ask/begin (spec §6.6 step 1).
func (m *Module) handleAskBegin(w http.ResponseWriter, r *http.Request) {
	if m.stopping() {
		m.writeErr(w, http.StatusServiceUnavailable, team.ErrNotReady, "daemon is stopping", nil)
		return
	}
	var req team.AskBeginRequest
	if !m.decodeBody(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.SessionID) == "" || strings.TrimSpace(req.ToolUseID) == "" {
		m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, "session_id and tool_use_id are required", nil)
		return
	}
	payload, bad := hookPayloadFor(req.Kind, req.ToolUseID, req.Payload, false)
	if bad != "" {
		m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, bad, nil)
		return
	}
	// No remote responder ⇒ no row at all: the native dialog runs alone and
	// the mod pays nothing more (step 1). Checked before the origin lookup,
	// which reads the registry.
	if !m.responders.Any() {
		m.writeErr(w, http.StatusConflict, team.ErrNoResponders, "no client is connected to this host", nil)
		return
	}
	origin, ok, err := m.origins.ResolveOriginBySession(req.SessionID)
	if err != nil {
		m.writeErr(w, http.StatusServiceUnavailable, team.ErrNotReady, "registry unavailable; retry", nil)
		return
	}
	if !ok {
		m.writeErr(w, http.StatusNotFound, team.ErrUnknownSession, "session_id is not a live Claude Code session on this host", nil)
		return
	}
	m.createMu.Lock()
	defer m.createMu.Unlock()
	if m.stopping() {
		m.writeErr(w, http.StatusServiceUnavailable, team.ErrNotReady, "daemon is stopping", nil)
		return
	}
	if open, found, err := m.store.OpenByToolUse(origin.SessionID, req.ToolUseID); err != nil {
		m.logf("[team] ask begin: %v", err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
		return
	} else if found {
		if !isTerminalOnly(open) {
			m.writeErr(w, http.StatusConflict, team.ErrAskOpen, "this tool use already has an open request", &open)
			return
		}
		// The settings hook got here first (the mod's hello was missed):
		// the read-only card gives way to an answerable one.
		if _, _, err := m.closeAs(open.ID, Close{State: team.StateDismissed, DecidedAt: m.now()}); err != nil {
			m.logf("[team] ask begin: take over %s: %v", open.ID, err)
		}
	}
	stored, err := m.openHookRow(origin, req.Kind, payload, false)
	if err != nil {
		m.logf("[team] ask begin: %v", err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
		return
	}
	m.writeJSON(w, http.StatusCreated, team.AskBeginResponse{ID: stored.ID})
}

// isTerminalOnly reads the payload flag both hook payloads carry.
func isTerminalOnly(a team.Approval) bool {
	var p struct {
		TerminalOnly bool `json:"terminal_only"`
	}
	return json.Unmarshal(a.Payload, &p) == nil && p.TerminalOnly
}

// pollRow is the long-poll of GET /api/team/approvals/{id} and of
// GET /api/ask/wait/{id}: register the waiter, renew the lease, read, and
// while open and wait > 0 wait for the close, the timer, the client or Stop,
// then read again. ok=false means an error response was written.
func (m *Module) pollRow(w http.ResponseWriter, r *http.Request, id string) (team.Approval, bool) {
	wait, err := pollWait(r.URL.Query().Get("wait"))
	if err != nil {
		m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, err.Error(), nil)
		return team.Approval{}, false
	}
	ch := m.addWaiter(id)
	defer m.removeWaiter(id, ch)
	if err := m.store.RenewLease(id, m.now()+team.LeaseS*1000); err != nil {
		m.logf("[team] get %s: %v", id, err)
		m.writeErr(w, http.StatusServiceUnavailable, team.ErrNotReady, "storage error; retry", nil)
		return team.Approval{}, false
	}
	a, ok, err := m.store.Get(id)
	if err != nil {
		m.logf("[team] get %s: %v", id, err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
		return team.Approval{}, false
	}
	if !ok {
		m.writeErr(w, http.StatusNotFound, team.ErrNotFound, "no such approval request", nil)
		return team.Approval{}, false
	}
	if m.afterRead != nil {
		m.afterRead(id)
	}
	if a.State == team.StateOpen && wait > 0 {
		timer := time.NewTimer(time.Duration(wait) * time.Second)
		defer timer.Stop()
		select {
		case <-ch:
		case <-timer.C:
		case <-r.Context().Done():
		case <-m.stopCtx.Done():
		}
		if a, ok, err = m.store.Get(id); err != nil || !ok {
			m.logf("[team] get %s after wait: ok=%v err=%v", id, ok, err)
			m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
			return team.Approval{}, false
		}
	}
	return a, true
}

// askWaitOf maps a hook row to the wait body (spec §6.6 step 2).
func askWaitOf(a team.Approval) team.AskWaitResponse {
	switch {
	case a.State == team.StateOpen:
		return team.AskWaitResponse{State: team.AskStillOpen}
	case a.State == team.StateApproved:
		return team.AskWaitResponse{State: team.AskAnsweredRemote, Hook: a.Hook}
	default:
		return team.AskWaitResponse{State: team.AskClosed, Reason: string(a.State)}
	}
}

// handleAskWait is GET /api/ask/wait/{id}?wait=25.
func (m *Module) handleAskWait(w http.ResponseWriter, r *http.Request) {
	a, ok := m.pollRow(w, r, r.PathValue("id"))
	if !ok {
		return
	}
	if !team.IsHookKind(a.Kind) {
		m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, "not a hook request", nil)
		return
	}
	m.writeJSON(w, http.StatusOK, askWaitOf(a))
}

// handleAskReport is POST /api/ask/report/{id} (spec §6.6 steps 3, 5, 6).
// answered_local closes an open row through the CAS; when a remote decide
// already won, the terminal's answer still stands: the row becomes
// terminal_override and a second closed is broadcast. dismissed closes an
// open row; against a closed one it is a no-op. Both answer the row as it
// now is, so a repeat is idempotent.
func (m *Module) handleAskReport(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req team.AskReportRequest
	if !m.decodeBody(w, r, &req) {
		return
	}
	if req.State != team.StateAnsweredLocal && req.State != team.StateDismissed {
		m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, `state must be "answered_local" or "dismissed"`, nil)
		return
	}
	a, ok, err := m.store.Get(id)
	if err != nil {
		m.logf("[team] ask report %s: %v", id, err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
		return
	}
	if !ok {
		m.writeErr(w, http.StatusNotFound, team.ErrNotFound, "no such approval request", nil)
		return
	}
	if !team.IsHookKind(a.Kind) {
		m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, "not a hook request", nil)
		return
	}
	now := m.now()
	c := Close{State: req.State, DecidedAt: now}
	if req.State == team.StateAnsweredLocal {
		c.DecidedBy = terminalClient()
		c.Hook = req.Hook
	}
	after, won, err := m.closeAs(id, c)
	if err != nil {
		m.logf("[team] ask report %s: %v", id, err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
		return
	}
	if won {
		m.logf("[team] approval %s %s by the terminal (origin %s)", id, after.State, after.Origin.Ref)
		m.writeJSON(w, http.StatusOK, after)
		return
	}
	if req.State == team.StateAnsweredLocal && after.State == team.StateApproved {
		// Step 5: the remote decide won the CAS, but the terminal had already
		// shown its answer. Record the override and tell every card.
		over, won, err := m.store.OverrideIfApproved(id, now, req.Hook)
		if err != nil {
			m.logf("[team] ask report %s: %v", id, err)
			m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
			return
		}
		if won {
			m.logf("[team] approval %s terminal_override: the terminal's answer replaces %q's (origin %s)", id, labelOf(after.DecidedBy), after.Origin.Ref)
			m.broadcast("closed", &over)
		}
		m.writeJSON(w, http.StatusOK, over)
		return
	}
	m.writeJSON(w, http.StatusOK, after)
}

func labelOf(c *team.Client) string {
	if c == nil {
		return ""
	}
	return c.Label
}

// decideHook is handleDecide's branch for a hook row (spec §6.6 steps 4–5):
// the remote client's answer rides in `hook`; a terminal_only card is
// read-only; hook_ask takes approve with answers only, hook_permission takes
// approve (allow) or deny. The close goes through the same closeAs.
func (m *Module) decideHook(w http.ResponseWriter, a team.Approval, req team.DecideRequest, state team.State, client team.Client) {
	if isTerminalOnly(a) {
		m.writeErr(w, http.StatusConflict, team.ErrTerminalOnly, "這題只能在終端機回答", &a)
		return
	}
	hook := req.Hook
	if hook == nil {
		hook = &team.HookDecision{}
	}
	switch a.Kind {
	case team.KindHookAsk:
		if state != team.StateApproved {
			m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, "a hook_ask is answered with decision approve and hook.answers", nil)
			return
		}
		if len(hook.Answers) == 0 {
			m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, "hook.answers is required", nil)
			return
		}
		hook = &team.HookDecision{Answers: hook.Answers}
	case team.KindHookPermission:
		if state == team.StateApproved {
			hook = &team.HookDecision{Behavior: "allow", UpdatedInput: hook.UpdatedInput}
		} else {
			hook = &team.HookDecision{Behavior: "deny", Message: hook.Message}
		}
	}
	after, won, err := m.closeAs(a.ID, Close{State: state, DecidedAt: m.now(), DecidedBy: &client, Hook: hook})
	if err != nil {
		m.logf("[team] decide %s: %v", a.ID, err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
		return
	}
	if !won {
		m.writeErr(w, http.StatusConflict, team.ErrAlreadyDecided, "this request was closed first by someone else", &after)
		return
	}
	m.logf("[team] approval %s %s by %s %q from %s (origin %s)", a.ID, after.State, client.Kind, client.Label, client.Addr, after.Origin.Ref)
	m.writeJSON(w, http.StatusOK, after)
}
```

  Edits in `internal/module/team/handler.go` (diff against alpha.527 — the context lines of the `handleInflight` and `handleDecide` hunks read as P5a-2b / P5a-2a left them; the `handleGet` hunk is untouched by P5a):

```diff
@@ -11,7 +11,6 @@
 	"path/filepath"
 	"strconv"
 	"strings"
-	"time"
 
 	"github.com/google/uuid"
 
@@ -245,7 +244,7 @@
 // survive a restart (boot lease grace), so the count informs, it does not
 // block. relays_active counts the open relay ops (P5a-2b).
 func (m *Module) handleInflight(w http.ResponseWriter, r *http.Request) {
-	open, err := m.store.ListOpen()
+	open, err := m.store.ListOpenNonHook()
 	if err != nil {
 		m.logf("[team] inflight: %v", err)
 		m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
@@ -268,58 +267,17 @@
 	return min(n, team.MaxPollWaitS), nil
 }
 
-// handleGet is GET /api/team/approvals/{id}?wait=N: it renews the lease
-// and, while the request is open and N > 0, waits for the close, N
-// seconds (≤ 25), the client going away, or Stop — whichever is first —
-// then answers the row as it is. A poll cut by Stop therefore answers 200
+// handleGet is GET /api/team/approvals/{id}?wait=N: the long-poll of
+// pollRow (shared with GET /api/ask/wait/{id}), answering the row as it is. A poll cut by Stop therefore answers 200
 // with the row still open; the CLI re-polls and meets the restart. A poll
 // whose renewal failed answers 503 not_ready instead of a 200 that would
 // let the CLI believe the lease holds while the sweeper abandons it; the
 // restart-aware CLI retries a 503.
 func (m *Module) handleGet(w http.ResponseWriter, r *http.Request) {
-	id := r.PathValue("id")
-	wait, err := pollWait(r.URL.Query().Get("wait"))
-	if err != nil {
-		m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, err.Error(), nil)
-		return
-	}
-	// Register before the read, so a close between the read and the
-	// select cannot be missed.
-	ch := m.addWaiter(id)
-	defer m.removeWaiter(id, ch)
-	if err := m.store.RenewLease(id, m.now()+team.LeaseS*1000); err != nil {
-		m.logf("[team] get %s: %v", id, err)
-		m.writeErr(w, http.StatusServiceUnavailable, team.ErrNotReady, "storage error; retry", nil)
-		return
-	}
-	a, ok, err := m.store.Get(id)
-	if err != nil {
-		m.logf("[team] get %s: %v", id, err)
-		m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
-		return
-	}
+	a, ok := m.pollRow(w, r, r.PathValue("id"))
 	if !ok {
-		m.writeErr(w, http.StatusNotFound, team.ErrNotFound, "no such approval request", nil)
 		return
 	}
-	if m.afterRead != nil {
-		m.afterRead(id)
-	}
-	if a.State == team.StateOpen && wait > 0 {
-		timer := time.NewTimer(time.Duration(wait) * time.Second)
-		defer timer.Stop()
-		select {
-		case <-ch:
-		case <-timer.C:
-		case <-r.Context().Done():
-		case <-m.stopCtx.Done():
-		}
-		if a, ok, err = m.store.Get(id); err != nil || !ok {
-			m.logf("[team] get %s after wait: ok=%v err=%v", id, ok, err)
-			m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
-			return
-		}
-	}
 	m.writeJSON(w, http.StatusOK, a)
 }
 
@@ -384,6 +342,10 @@
 		m.writeErr(w, http.StatusConflict, team.ErrAlreadyDecided, "this request is already closed", &a)
 		return
 	}
+	if team.IsHookKind(a.Kind) {
+		m.decideHook(w, a, req, state, client)
+		return
+	}
 	var grant *team.Grant
 	// A self_relay approval carries no grant (its payload is a
 	// SelfRelayPayload); its op moves in afterClose.
 	if state == team.StateApproved && a.Kind == team.KindLead {
 		var payload team.LeadPayload
```

  And the `RegisterRoutes` hunk of `module.go` (from Task 8a.3's diff): the three `mux.HandleFunc` lines after `GET /api/team/inflight`.

- [ ] **Step 4: Run the tests and verify they pass.**
  - Run: `go vet ./internal/module/team/ && go test ./internal/module/team/ -count=3`
  - Expected: PASS (the race test runs 20 rounds; the existing `TestGet_*`, `TestDecide_*`, `TestInflight_*` still pass on `pollRow` / `ListOpenNonHook`).

- [ ] **Step 5: Commit.**
  ```bash
  git add internal/module/team/ask_handler.go internal/module/team/ask_handler_test.go internal/module/team/handler.go internal/module/team/module.go
  git commit -m "feat(team): /api/ask begin|wait|report, decide on hook kinds, terminal_override's second closed

  Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
  ```

---

## PR P8a-1c — `pdx ask` and the hook's bounded forward (≈ 690 lines, 5 files)

### Task 8a.6: `pdx ask begin|wait|report`

**Files:**
- Create: `cmd/pdx/ask.go`
- Modify: `cmd/pdx/main.go:70-71` (dispatch `case "ask"` after `lead`)
- Test: `cmd/pdx/ask_test.go` (uses `writeTestConfig` from `peers_test.go:354` and `leadClockOpt` from `lead_test.go:120`)

**Interfaces:**
- Produces:
  ```
  pdx ask begin --session <sid> --tool-use <id> --kind hook_ask|hook_permission (--payload <json> | --payload-file <f>) [--config <path>]
      stdout {"id":"…"} exit 0 · 409 no_responders → stderr `pdx ask: <detail> no_responders` (the code is the last stderr token, as in pdx relay), exit 13 · 409 ask_open → adopts: stdout {"id":<open row>} exit 0 · 20 / 21 · else 1
  pdx ask wait <id> [--config <path>]
      one round of GET /api/ask/wait/{id}?wait=25 polls, ≤ 9 min; stdout the daemon's AskWaitResponse verbatim; exit 0 (also for a JSON 404 → {"state":"closed","reason":"not_found"}); 20 after three polls with no answer / grace exhausted; 21 on a plain 404
  pdx ask report <id> answered_local|dismissed [--hook <json> | --hook-file <f>] [--config <path>]
      stdout the Approval, exit 0 · 1 / 20 / 21
  ```
  ```go
  func runAsk(args []string)
  func runAskCmd(ctx context.Context, args []string, stdout, stderr io.Writer, now func() time.Time, clientOpts ...daemonclient.Option) int
  ```
- Consumes: `daemonclient.New / Do / Once / Idempotent / WithStderr / WithAttemptTimeout / ErrUnavailable / ErrUnsupported / ErrNoAnswer / *StatusError` (`cmd/pdx/daemonclient/client.go`), `resolveDaemonHost` (`statusline_proxy.go:145`), `Exit*` (`exitcodes.go`), Task 8a.1 bodies. `begin` **is** marked `Idempotent()` (codex round): the daemon keeps one open row per `(session, tool_use_id)`, so a replay after a lost response is answered `409 ask_open` with that row and the CLI adopts it — the same `{"id":…}` on stdout as a 201, exit 0. `report` is idempotent too (the daemon answers the row as it is).

- [ ] **Step 1: Write the failing tests.**

```go
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wake/purdex/internal/team"
)

// fakeAskDaemon answers the three ask routes and /api/health. waits is the
// sequence of wait bodies; after it is exhausted the last one repeats.
type fakeAskDaemon struct {
	mu          sync.Mutex
	beginStatus int
	beginBody   any
	// dropFirstBegin: the first begin's body is read and "applied" (the row
	// opened, id ask-1), then the connection dies without a response; every
	// later begin for the same (session, tool_use) is 409 ask_open with it.
	dropFirstBegin bool
	waits       []team.AskWaitResponse
	polls       int
	reports     []team.AskReportRequest
	begins      []team.AskBeginRequest
	srv         *httptest.Server
}

func newFakeAskDaemon(t *testing.T) *fakeAskDaemon {
	t.Helper()
	d := &fakeAskDaemon{beginStatus: http.StatusCreated, beginBody: team.AskBeginResponse{ID: "ask-1"}}
	d.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer admin-tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		d.mu.Lock()
		defer d.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/health":
			_, _ = io.WriteString(w, `{"boot_id":"b1"}`)
		case r.Method == http.MethodPost && r.URL.Path == "/api/ask/begin":
			var req team.AskBeginRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			d.begins = append(d.begins, req)
			if d.dropFirstBegin {
				if len(d.begins) == 1 {
					// The body was read and the row written; then the connection
					// dies without an answer (as lead_test.go's dropFirstCreate).
					if conn, _, err := w.(http.Hijacker).Hijack(); err == nil {
						conn.Close()
					}
					return
				}
				w.WriteHeader(http.StatusConflict)
				_ = json.NewEncoder(w).Encode(team.APIError{Error: team.ErrAskOpen, Approval: &team.Approval{ID: "ask-1", Kind: team.KindHookAsk, State: team.StateOpen}})
				return
			}
			w.WriteHeader(d.beginStatus)
			_ = json.NewEncoder(w).Encode(d.beginBody)
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/ask/wait/"):
			i := d.polls
			d.polls++
			if i >= len(d.waits) {
				i = len(d.waits) - 1
			}
			_ = json.NewEncoder(w).Encode(d.waits[i])
		case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/api/ask/report/"):
			var req team.AskReportRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			d.reports = append(d.reports, req)
			_ = json.NewEncoder(w).Encode(team.Approval{ID: strings.TrimPrefix(r.URL.Path, "/api/ask/report/"), Kind: team.KindHookAsk, State: req.State, Hook: req.Hook})
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, "404 page not found\n")
		}
	}))
	t.Cleanup(d.srv.Close)
	return d
}

func driveAsk(t *testing.T, d *fakeAskDaemon, now func() time.Time, args ...string) (int, string, string) {
	t.Helper()
	cfg := writeTestConfig(t, d.srv.URL, "admin-tok")
	var stdout, stderr bytes.Buffer
	code := runAskCmd(context.Background(), append(args, "--config", cfg), &stdout, &stderr, now, leadClockOpt())
	return code, stdout.String(), stderr.String()
}

func TestRunAskCmd_UsageErrorsExit2(t *testing.T) {
	for _, args := range [][]string{
		{}, {"frob"}, {"begin"}, {"begin", "--session", "s"}, {"begin", "--session", "s", "--tool-use", "t"},
		{"begin", "--session", "s", "--tool-use", "t", "--payload", "{not json"},
		{"begin", "--session", "s", "--tool-use", "t", "--kind", "lead", "--payload", "{}"},
		{"wait"}, {"wait", "a", "b"}, {"report", "a"}, {"report", "a", "approved"}, {"report", "a", "dismissed", "--hook", "{", "--hook-file", "/x"},
	} {
		var stdout, stderr bytes.Buffer
		code := runAskCmd(context.Background(), args, &stdout, &stderr, time.Now)
		if code != ExitUsage || !strings.Contains(stderr.String(), "usage: pdx ask") || stdout.Len() != 0 {
			t.Errorf("%v: code=%d stdout=%q stderr=%q", args, code, stdout.String(), stderr.String())
		}
	}
}

func TestRunAskCmd_BeginPrintsIDAndSendsBody(t *testing.T) {
	d := newFakeAskDaemon(t)
	payload := `{"questions":[{"question":"q?","header":"h","options":[{"label":"a"},{"label":"b"}],"multiSelect":false}]}`
	code, stdout, stderr := driveAsk(t, d, time.Now, "begin", "--session", "sid-1", "--tool-use", "toolu_1", "--kind", "hook_ask", "--payload", payload)
	if code != ExitOK || strings.TrimSpace(stdout) != `{"id":"ask-1"}` {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if len(d.begins) != 1 || d.begins[0].SessionID != "sid-1" || d.begins[0].ToolUseID != "toolu_1" || d.begins[0].Kind != team.KindHookAsk || string(d.begins[0].Payload) != payload {
		t.Fatalf("begin body = %+v", d.begins)
	}
}

func TestRunAskCmd_BeginNoRespondersExit13_AskOpenAdopts(t *testing.T) {
	d := newFakeAskDaemon(t)
	d.beginStatus, d.beginBody = http.StatusConflict, team.APIError{Error: team.ErrNoResponders, Detail: "沒有連線中的客戶端可以回答"}
	code, stdout, stderr := driveAsk(t, d, time.Now, "begin", "--session", "s", "--tool-use", "t", "--payload", `{"questions":[1]}`)
	if code != ExitRefused || stdout != "" || !strings.HasPrefix(stderr, "pdx ask: ") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	// The code is the LAST stderr token (the mod's stderrCode() reads it that way).
	if toks := strings.Fields(stderr); len(toks) == 0 || toks[len(toks)-1] != team.ErrNoResponders {
		t.Fatalf("the code must be the last stderr token: %q", stderr)
	}
	d.beginStatus, d.beginBody = http.StatusConflict, team.APIError{Error: team.ErrAskOpen, Approval: &team.Approval{ID: "ask-open-7"}}
	code, stdout, _ = driveAsk(t, d, time.Now, "begin", "--session", "s", "--tool-use", "t", "--payload", `{"questions":[1]}`)
	if code != ExitOK || strings.TrimSpace(stdout) != `{"id":"ask-open-7"}` {
		t.Fatalf("adopt: code=%d stdout=%q", code, stdout)
	}
}

// The POST landed (the row is open) but the response was lost: begin is
// Idempotent(), so the client replays inside the grace, the daemon answers
// 409 ask_open with the row it opened, and the CLI adopts it — stdout
// {"id":"ask-1"} exactly as a 201 would print, exit 0, nothing on stderr.
// Mutation gates: drop Idempotent() → ErrSentNoResponse → exit 1 and an
// empty stdout (the mod would run the dialog alone while a row is open);
// treat ask_open as an error → exit 13.
func TestRunAskCmd_BeginReplayAfterLostResponseAdoptsTheSameID(t *testing.T) {
	d := newFakeAskDaemon(t)
	d.dropFirstBegin = true
	code, stdout, stderr := driveAsk(t, d, time.Now, "begin", "--session", "s", "--tool-use", "t", "--payload", `{"questions":[1]}`)
	if code != ExitOK || strings.TrimSpace(stdout) != `{"id":"ask-1"}` || stderr != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.begins) != 2 || d.begins[0].ToolUseID != "t" || d.begins[1].ToolUseID != "t" {
		t.Fatalf("want the dropped begin and one replay for the same tool use, got %+v", d.begins)
	}
}

// still_open polls repeat inside one round; the round ends with still_open
// after 9 min, or sooner with the daemon's answer, printed verbatim.
func TestRunAskCmd_WaitRoundAndAnswers(t *testing.T) {
	d := newFakeAskDaemon(t)
	d.waits = []team.AskWaitResponse{{State: team.AskStillOpen}, {State: team.AskStillOpen}, {State: team.AskAnsweredRemote, Hook: &team.HookDecision{Answers: map[string]string{"q?": "a"}}}}
	code, stdout, stderr := driveAsk(t, d, time.Now, "wait", "ask-1")
	if code != ExitOK || strings.TrimSpace(stdout) != `{"state":"answered_remote","hook":{"answers":{"q?":"a"}}}` || d.polls != 3 {
		t.Fatalf("code=%d stdout=%q stderr=%q polls=%d", code, stdout, stderr, d.polls)
	}
	// A round whose every poll is still_open ends after askWaitRound: the
	// fake clock jumps 5 min per call, so the third poll crosses 9 min.
	d2 := newFakeAskDaemon(t)
	d2.waits = []team.AskWaitResponse{{State: team.AskStillOpen}}
	t0 := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	calls := 0
	now := func() time.Time { calls++; return t0.Add(time.Duration(calls-1) * 5 * time.Minute) }
	code, stdout, _ = driveAsk(t, d2, now, "wait", "ask-1")
	if code != ExitOK || strings.TrimSpace(stdout) != `{"state":"still_open"}` || d2.polls != 2 {
		t.Fatalf("round: code=%d stdout=%q polls=%d", code, stdout, d2.polls)
	}
	d3 := newFakeAskDaemon(t)
	d3.waits = []team.AskWaitResponse{{State: team.AskClosed, Reason: "dismissed"}}
	code, stdout, _ = driveAsk(t, d3, time.Now, "wait", "ask-1")
	if code != ExitOK || strings.TrimSpace(stdout) != `{"state":"closed","reason":"dismissed"}` {
		t.Fatalf("closed: code=%d stdout=%q", code, stdout)
	}
}

func TestRunAskCmd_WaitUnsupportedExit21(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/health" {
			_, _ = io.WriteString(w, `{"boot_id":"b1"}`)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	cfg := writeTestConfig(t, srv.URL, "admin-tok")
	var stdout, stderr bytes.Buffer
	code := runAskCmd(context.Background(), []string{"wait", "ask-1", "--config", cfg}, &stdout, &stderr, time.Now, leadClockOpt())
	if code != ExitUnsupported || stdout.Len() != 0 || !strings.Contains(stderr.String(), "unsupported") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestRunAskCmd_ReportSendsHookAndPrintsRow(t *testing.T) {
	d := newFakeAskDaemon(t)
	code, stdout, stderr := driveAsk(t, d, time.Now, "report", "ask-1", "answered_local", "--hook", `{"answers":{"q?":"a"}}`)
	if code != ExitOK {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
	var ap team.Approval
	if err := json.Unmarshal([]byte(stdout), &ap); err != nil || ap.ID != "ask-1" || ap.State != team.StateAnsweredLocal || ap.Hook == nil || ap.Hook.Answers["q?"] != "a" {
		t.Fatalf("stdout = %q (%v)", stdout, err)
	}
	if len(d.reports) != 1 || d.reports[0].State != team.StateAnsweredLocal || d.reports[0].Hook.Answers["q?"] != "a" {
		t.Fatalf("report body = %+v", d.reports)
	}
	code, _, _ = driveAsk(t, d, time.Now, "report", "ask-1", "dismissed")
	if code != ExitOK || len(d.reports) != 2 || d.reports[1].State != team.StateDismissed || d.reports[1].Hook != nil {
		t.Fatalf("dismissed: code=%d reports=%+v", code, d.reports)
	}
}
```

- [ ] **Step 2: Run the tests and verify they fail.**
  - Run: `go test ./cmd/pdx/ -run TestRunAskCmd -v`
  - Expected: FAIL to compile — `undefined: runAskCmd`.

- [ ] **Step 3: Implement.** Create `cmd/pdx/ask.go`:

```go
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/wake/purdex/cmd/pdx/daemonclient"
	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/team"
)

// `pdx ask` is the Purdex mod's side of 分流 (spec §6.6, U19). The mod runs
// each subcommand through $.process.run with the daemon's answer on stdout:
//
//	pdx ask begin --session <sid> --tool-use <id> --kind hook_ask (--payload <json> | --payload-file <f>)
//	    → stdout {"id":…} exit 0; exit 13 on 409 no_responders (code on stderr); 20 / 21; 1 otherwise.
//	      A 409 ask_open adopts the open row: stdout {"id":<its id>} exit 0.
//	pdx ask wait <id>
//	    → one bounded round (≤ 9 min of GET ?wait=25 polls), stdout the daemon's
//	      {"state":"still_open"} | {"state":"answered_remote","hook":…} | {"state":"closed","reason":…}
//	      exit 0; a JSON 404 prints {"state":"closed","reason":"not_found"}; 20 / 21 only otherwise.
//	pdx ask report <id> <answered_local|dismissed> [--hook <json> | --hook-file <f>]
//	    → stdout the Approval, exit 0; 1 / 20 / 21.
const askUsage = "usage: pdx ask begin --session <sid> --tool-use <id> --kind hook_ask|hook_permission (--payload <json> | --payload-file <f>) [--config <path>]\n" +
	"       pdx ask wait <id> [--config <path>]\n" +
	"       pdx ask report <id> answered_local|dismissed [--hook <json> | --hook-file <f>] [--config <path>]"

const (
	// askAttemptTimeout bounds one poll: 25 s of daemon-side wait plus room (as lead's).
	askAttemptTimeout = 35 * time.Second
	// askWaitRound is how long one `pdx ask wait` keeps polling before it
	// prints still_open: $.process.run is capped at ten minutes (M24), the
	// mod gives it 590 s, so a round ends well inside that.
	askWaitRound = 9 * time.Minute
	// askMaxHungPolls is lead's rule: three polls with no answer at all ⇒ 20.
	askMaxHungPolls = 3
)

func runAsk(args []string) {
	os.Exit(runAskCmd(context.Background(), args, os.Stdout, os.Stderr, time.Now))
}

// askArgs is the parsed grammar of one `pdx ask` invocation.
type askArgs struct {
	verb    string
	cfgPath string
	// begin
	session, toolUse string
	kind             team.Kind
	payload          json.RawMessage
	// wait / report
	id    string
	state team.State
	hook  *team.HookDecision
}

// jsonArg reads an inline JSON flag or a file flag (exactly one may be set).
func jsonArg(inline, file, what string) (json.RawMessage, error) {
	if inline != "" && file != "" {
		return nil, fmt.Errorf("give --%s or --%s-file, not both", what, what)
	}
	raw := []byte(inline)
	if file != "" {
		b, err := os.ReadFile(file)
		if err != nil {
			return nil, err
		}
		raw = b
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return nil, nil
	}
	if !json.Valid(raw) {
		return nil, fmt.Errorf("--%s is not valid JSON", what)
	}
	return json.RawMessage(raw), nil
}

// parseAskArgs validates the grammar; ok=false means a usage line was
// written and the caller exits 2 before any config load.
func parseAskArgs(args []string, stderr io.Writer) (askArgs, bool) {
	var a askArgs
	reject := func(msg string) (askArgs, bool) {
		fmt.Fprintf(stderr, "pdx ask: %s\n%s\n", msg, askUsage)
		return a, false
	}
	if len(args) == 0 {
		return reject("a subcommand is required")
	}
	a.verb = args[0]
	fs := flag.NewFlagSet("pdx ask "+a.verb, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&a.cfgPath, "config", "", "")
	var kind, payload, payloadFile, hook, hookFile string
	switch a.verb {
	case "begin":
		fs.StringVar(&a.session, "session", "", "")
		fs.StringVar(&a.toolUse, "tool-use", "", "")
		fs.StringVar(&kind, "kind", string(team.KindHookAsk), "")
		fs.StringVar(&payload, "payload", "", "")
		fs.StringVar(&payloadFile, "payload-file", "", "")
	case "wait", "report":
		fs.StringVar(&hook, "hook", "", "")
		fs.StringVar(&hookFile, "hook-file", "", "")
	default:
		return reject(fmt.Sprintf("unknown subcommand %q", a.verb))
	}
	// Positionals come first for wait/report (`pdx ask wait <id>`); flag
	// parses from the first flag on.
	rest := args[1:]
	var pos []string
	for len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
		pos = append(pos, rest[0])
		rest = rest[1:]
	}
	if err := fs.Parse(rest); err != nil {
		return reject(err.Error())
	}
	if fs.NArg() != 0 {
		return reject(fmt.Sprintf("unexpected argument %q", fs.Arg(0)))
	}
	switch a.verb {
	case "begin":
		if len(pos) != 0 {
			return reject(fmt.Sprintf("unexpected argument %q", pos[0]))
		}
		if a.session == "" || a.toolUse == "" {
			return reject("--session and --tool-use are required")
		}
		a.kind = team.Kind(kind)
		if !team.IsHookKind(a.kind) {
			return reject("--kind must be hook_ask or hook_permission")
		}
		p, err := jsonArg(payload, payloadFile, "payload")
		if err != nil {
			return reject(err.Error())
		}
		if p == nil {
			return reject("--payload or --payload-file is required")
		}
		a.payload = p
	case "wait":
		if len(pos) != 1 || pos[0] == "" {
			return reject("wait takes exactly one <id>")
		}
		a.id = pos[0]
	case "report":
		if len(pos) != 2 || pos[0] == "" {
			return reject("report takes <id> and <state>")
		}
		a.id, a.state = pos[0], team.State(pos[1])
		if a.state != team.StateAnsweredLocal && a.state != team.StateDismissed {
			return reject("state must be answered_local or dismissed")
		}
		h, err := jsonArg(hook, hookFile, "hook")
		if err != nil {
			return reject(err.Error())
		}
		if h != nil {
			a.hook = new(team.HookDecision)
			if err := json.Unmarshal(h, a.hook); err != nil {
				return reject("--hook: " + err.Error())
			}
		}
	}
	return a, true
}

// runAskCmd implements `pdx ask` and returns the exit code. now is the
// clock the wait round is measured on; clientOpts are appended for tests.
func runAskCmd(ctx context.Context, args []string, stdout, stderr io.Writer, now func() time.Time, clientOpts ...daemonclient.Option) int {
	a, ok := parseAskArgs(args, stderr)
	if !ok {
		return ExitUsage
	}
	cfg, err := config.Load(a.cfgPath)
	if err != nil {
		fmt.Fprintf(stderr, "pdx ask: %v\n", err)
		return ExitError
	}
	base := fmt.Sprintf("http://%s:%d", resolveDaemonHost(cfg.Bind), cfg.Port)
	opts := append([]daemonclient.Option{daemonclient.WithStderr(stderr), daemonclient.WithAttemptTimeout(askAttemptTimeout)}, clientOpts...)
	client := daemonclient.New(base, cfg.Token, opts...)
	switch a.verb {
	case "begin":
		return askBegin(ctx, client, a, stdout, stderr)
	case "wait":
		return askWait(ctx, client, a.id, stdout, stderr, now)
	default:
		return askReport(ctx, client, a, stdout, stderr)
	}
}

func askBegin(ctx context.Context, client *daemonclient.Client, a askArgs, stdout, stderr io.Writer) int {
	var out team.AskBeginResponse
	// Idempotent(): the daemon keeps one open row per (session, tool_use_id)
	// (Task 8a.2), so a replay after a lost response cannot open a second
	// row — it is answered 409 ask_open with the row the first send opened,
	// and that answer is a success below. Without the replay a drop after
	// the send would be ErrSentNoResponse → exit 1 → the mod runs the native
	// dialog alone while a row sits open for the phone.
	_, err := client.Do(ctx, http.MethodPost, "/api/ask/begin",
		team.AskBeginRequest{SessionID: a.session, ToolUseID: a.toolUse, Kind: a.kind, Payload: a.payload}, &out, daemonclient.Idempotent())
	if err != nil {
		var se *daemonclient.StatusError
		switch {
		case errors.As(err, &se) && se.API.Error == team.ErrAskOpen && se.API.Approval != nil:
			// The row for this tool use is already open (a replayed or retried
			// begin): adopt it — the same stdout as a 201.
			out.ID = se.API.Approval.ID
		case errors.As(err, &se) && se.API.Error == team.ErrNoResponders:
			// Same shape as pdx relay: `pdx ask: <detail> <code>`, the code
			// the LAST stderr token (the mod's stderrCode() reads it).
			fmt.Fprintf(stderr, "pdx ask: %s %s\n", sanitizeCell(se.API.Detail), team.ErrNoResponders)
			return ExitRefused
		default:
			return askReportErr(err, stderr)
		}
	}
	b, _ := json.Marshal(out)
	fmt.Fprintln(stdout, string(b))
	return ExitOK
}

func askWait(ctx context.Context, client *daemonclient.Client, id string, stdout, stderr io.Writer, now func() time.Time) int {
	start := now()
	hung := 0
	for {
		var w team.AskWaitResponse
		_, err := client.Do(ctx, http.MethodGet, fmt.Sprintf("/api/ask/wait/%s?wait=%d", id, team.MaxPollWaitS), nil, &w)
		if err != nil {
			if errors.Is(err, daemonclient.ErrNoAnswer) || errors.Is(err, context.DeadlineExceeded) {
				hung++
				if hung >= askMaxHungPolls {
					fmt.Fprintln(stderr, "pdx ask: daemon 沒有回應")
					return ExitUnavailable
				}
				continue
			}
			var se *daemonclient.StatusError
			if errors.As(err, &se) && se.Status == http.StatusNotFound {
				// The row is gone (a reset team.db): tell the mod to stop looping.
				w = team.AskWaitResponse{State: team.AskClosed, Reason: team.ErrNotFound}
				return printJSON(stdout, w)
			}
			return askReportErr(err, stderr)
		}
		hung = 0
		if w.State != team.AskStillOpen || now().Sub(start) >= askWaitRound {
			return printJSON(stdout, w)
		}
	}
}

func askReport(ctx context.Context, client *daemonclient.Client, a askArgs, stdout, stderr io.Writer) int {
	var ap team.Approval
	// A report is idempotent on the daemon (it answers the row as it is), so a replay is safe.
	_, err := client.Do(ctx, http.MethodPost, "/api/ask/report/"+a.id, team.AskReportRequest{State: a.state, Hook: a.hook}, &ap, daemonclient.Idempotent())
	if err != nil {
		return askReportErr(err, stderr)
	}
	return printJSON(stdout, ap)
}

func printJSON(stdout io.Writer, v any) int {
	b, err := json.Marshal(v)
	if err != nil {
		return ExitError
	}
	fmt.Fprintln(stdout, string(b))
	return ExitOK
}

// askReportErr maps a client error to stderr and an exit code (spec §14).
// Every line ends with the bare code (`pdx ask: <detail> <code>`), the
// shape pdx relay uses, so a mod can read the code as the last token.
func askReportErr(err error, stderr io.Writer) int {
	var se *daemonclient.StatusError
	switch {
	case errors.Is(err, daemonclient.ErrUnavailable):
		fmt.Fprintln(stderr, "pdx ask: 等了 30 秒 daemon 仍沒有回應 daemon_unavailable")
		return ExitUnavailable
	case errors.Is(err, daemonclient.ErrUnsupported):
		fmt.Fprintln(stderr, "pdx ask: 這個 daemon 沒有 /api/ask 路由，請先更新 daemon unsupported")
		return ExitUnsupported
	case errors.As(err, &se):
		fmt.Fprintf(stderr, "pdx ask: %s %s\n", sanitizeCell(se.API.Detail), sanitizeCell(se.API.Error))
		return ExitError
	default:
		fmt.Fprintf(stderr, "pdx ask: %v\n", err)
		return ExitError
	}
}
```

  Edit `cmd/pdx/main.go` (diff against alpha.527; after P5a-2c the switch already has `case "relay": runRelay(os.Args[2:])` right after `lead` — put `ask` after `relay`, and add `ask` to the usage line at `:43` next to `relay`):

```diff
@@ -69,6 +69,8 @@
 		runMsg(os.Args[2:])
 	case "lead":
 		runLead(os.Args[2:])
+	case "ask":
+		runAsk(os.Args[2:])
 	case "nex":
 		runNexMain(os.Args[2:])
 	case "path":
```

- [ ] **Step 4: Run the tests and verify they pass.**
  - Run: `go vet ./cmd/pdx/ && go test ./cmd/pdx/ -run 'TestRunAskCmd' -v && go test ./cmd/pdx/`
  - Expected: six PASS lines, then the whole package PASS (the dispatch tests in `main` are unchanged).

- [ ] **Step 5: Commit.**
  ```bash
  git add cmd/pdx/ask.go cmd/pdx/ask_test.go cmd/pdx/main.go
  git commit -m "feat(pdx): ask begin|wait|report — the mod's side of 分流

  Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
  ```

### Task 8a.7: The hook's bounded forward for the terminal-only degradation

**Files:**
- Create: `cmd/pdx/hook_ask.go`
- Modify: `cmd/pdx/hook.go` — **P2c-2's shape (Task 2c.7), re-read after it merges; `:line`s are alpha.527's**: in `runHook`, inside the `if err == nil` block right after `hookDecision` returns and before `if asked { cancelBudget() }`, add one call `forwardHookAsk(base, cfg.Token, cfg.DataDir, agentType, payload.RawEvent)` **guarded by `!asked`** — P2c's `hookDecision` returns `asked == true` exactly when it already sent `/api/hooks/decide` for this event (then the daemon has already observed it); the forward runs inside the same 5 s budget (its 2 s bound is the shorter) and the event POST is still in flight concurrently, as P2c left it. `base` is the same string P2c's decision path builds — `fmt.Sprintf("http://%s:%d", resolveDaemonHost(cfg.Bind), cfg.Port)` (Task 2c.7; a `Bind` of `0.0.0.0` is not dialable as is) — not the raw `cfg.Bind` the event URL used at alpha.527 (`hook.go:82`). Nothing is printed by this call and the exit code stays 0.
- Test: `cmd/pdx/hook_ask_test.go`

**Interfaces:**
- Produces:
  ```go
  const hookAskTimeout = 2 * time.Second
  type hookAskEvent struct { HookEventName, SessionID, ToolName string; ToolInput json.RawMessage; ToolUseID string }
  func parseHookAskEvent(raw json.RawMessage) hookAskEvent
  func askFlagExists(dataDir, agent, sessionID string) bool                       // <data_dir>/hookasks/<agent>/<session_id>
  func askForward(e hookAskEvent, flag bool) bool                                   // the predicate
  func forwardHookAsk(base, token, dataDir, agent string, raw json.RawMessage) bool  // one Once POST within 2 s, answer discarded
  ```
- Consumes: `daemonclient.Once`, `team.HookDecideRequest` (P2c). The predicate: ungated `PreToolUse`/`AskUserQuestion` and `PermissionRequest`; flag-gated `PostToolUse`, `PostToolUseFailure`, `Stop`, `UserPromptSubmit`, `SessionEnd`; nothing else, ever (`Contract problems` 1).

- [ ] **Step 1: Write the failing tests.**

```go
package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

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
	if !forwardHookAsk(srv.URL, "admin-tok", dir, "cc", raw) {
		t.Fatal("PreToolUse/AskUserQuestion must be forwarded")
	}
	if forwardHookAsk(srv.URL, "admin-tok", dir, "cc", json.RawMessage(`{"hook_event_name":"Stop","session_id":"sid-1"}`)) {
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
	if !forwardHookAsk(url, "admin-tok", t.TempDir(), "cc", raw) {
		t.Fatal("PermissionRequest must be attempted (and silently fail)")
	}
}
```

- [ ] **Step 2: Run the tests and verify they fail.**
  - Run: `go test ./cmd/pdx/ -run 'TestAskForward|TestAskFlagExists|TestForwardHookAsk' -v`
  - Expected: FAIL to compile — `undefined: askForward`, `undefined: parseHookAskEvent`, `undefined: forwardHookAsk`.

- [ ] **Step 3: Implement.** Create `cmd/pdx/hook_ask.go`:

```go
package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/wake/purdex/cmd/pdx/daemonclient"
	"github.com/wake/purdex/internal/team"
)

// 分流's settings-hook backstop (spec §6.6 table row 3; daemon side in
// internal/module/team/hook_observe.go). P2c's decision path calls the
// daemon only behind the hooklocks flag; this adds the few events the
// terminal-only degradation needs, bounded at hookAskTimeout and never
// printing anything (the daemon answers {} on this path):
//
//   - ungated: PreToolUse of AskUserQuestion, PermissionRequest — the
//     daemon may open a read-only card for the dialog the terminal shows;
//   - behind <data_dir>/hookasks/<agent>/<session_id> (written by the
//     daemon while such a card is open): PostToolUse, PostToolUseFailure,
//     Stop, UserPromptSubmit, SessionEnd — they close the card.
//
// So a session that never shows a dialog to a connected client pays one
// stat per frequent event and no round trip (U17's cost rule).

// hookAskTimeout bounds the forward: one attempt, no restart grace. A
// daemon that is restarting simply gets no card for this dialog.
const hookAskTimeout = 2 * time.Second

// hookAskEvent is the subset of a hook's stdin the predicate reads.
type hookAskEvent struct {
	HookEventName string          `json:"hook_event_name"`
	SessionID     string          `json:"session_id"`
	ToolName      string          `json:"tool_name"`
	ToolInput     json.RawMessage `json:"tool_input"`
	ToolUseID     string          `json:"tool_use_id"`
}

// parseHookAskEvent reads the fields; a malformed stdin is the zero value.
func parseHookAskEvent(raw json.RawMessage) hookAskEvent {
	var e hookAskEvent
	_ = json.Unmarshal(raw, &e)
	return e
}

// askFlagExists reports whether the daemon says this session has an open
// terminal-only card.
func askFlagExists(dataDir, agent, sessionID string) bool {
	if dataDir == "" || sessionID == "" {
		return false
	}
	if agent == "" {
		agent = "cc"
	}
	_, err := os.Stat(filepath.Join(dataDir, "hookasks", filepath.Base(agent), filepath.Base(sessionID)))
	return err == nil
}

// askForward decides whether this event is sent to POST /api/hooks/decide
// for the terminal-only degradation (independently of P2c's lock gate).
func askForward(e hookAskEvent, flag bool) bool {
	switch e.HookEventName {
	case "PreToolUse":
		return e.ToolName == "AskUserQuestion"
	case "PermissionRequest":
		return true
	case "PostToolUse", "PostToolUseFailure", "Stop", "UserPromptSubmit", "SessionEnd":
		return flag
	}
	return false
}

// forwardHookAsk sends the event to POST /api/hooks/decide when askForward
// says so: one attempt within hookAskTimeout, the answer discarded (this
// path never carries a decision). It returns whether a request went out,
// for the tests; the hook ignores it. P2c's lock path, when it ran for the
// same event, already sent the same request — the caller skips this then.
func forwardHookAsk(base, token, dataDir, agent string, raw json.RawMessage) bool {
	e := parseHookAskEvent(raw)
	if !askForward(e, askFlagExists(dataDir, agent, e.SessionID)) {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), hookAskTimeout)
	defer cancel()
	client := daemonclient.New(base, token, daemonclient.WithStderr(io.Discard), daemonclient.WithAttemptTimeout(hookAskTimeout))
	_, _ = client.Once(ctx, http.MethodPost, "/api/hooks/decide", team.HookDecideRequest{
		Agent: agent, Event: e.HookEventName, SessionID: e.SessionID,
		ToolName: e.ToolName, ToolInput: e.ToolInput, ToolUseID: e.ToolUseID, Raw: raw,
	}, nil)
	return true
}
```

  Then wire the call into `runHook` as described under **Files** (one line plus the "P2c already sent it" condition; quote P2c's `file:line` in the commit message).

- [ ] **Step 4: Run the tests and verify they pass.**
  - Run: `go vet ./cmd/pdx/ && go test ./cmd/pdx/ -run 'TestAskForward|TestAskFlagExists|TestForwardHookAsk|TestRunHook|TestPostHookEvent' -v`
  - Expected: PASS; the existing `TestRunHook_*` still see one event POST and exit 0 (their events are not forwarded: no `AskUserQuestion`, no flag).

- [ ] **Step 5: Commit.**
  ```bash
  git add cmd/pdx/hook_ask.go cmd/pdx/hook_ask_test.go cmd/pdx/hook.go
  git commit -m "feat(pdx): hook forwards AskUserQuestion / PermissionRequest and, behind the hookasks flag, the closing events

  Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
  ```

---

## PR P8a-1d — the terminal-only degradation (≈ 350 lines + two one-line edits in P2c / P5a code)

### Task 8a.8: `observeHookEvent` — rows for sessions without the mod, closed by the settings hooks

> P2c-1 (`hooks.go`) and P5a-2a (`relay_handler.go`, `modSeen`) are merged before this PR; the edit below is described by anchor text against P2c's `handleHookDecide` as written in Task 2c.3 — re-read the merged file before applying.

**Files:**
- Create: `internal/module/team/hook_observe.go`
- Modify: `internal/module/team/hooks.go` (P2c's `handleHookDecide`): it writes `team.HookDecideResponse{}` on three paths — the "other event" branch (`PostToolUse` / `Stop` / …, which P2c answers `200 {}` for exactly so that this PR can observe them), the "no open lead request" branch (after `removeHookLock`) and the `PermissionRequest`-with-open-request branch. Factor the three into one `m.answerEmptyDecision(w, req)` that calls `m.observeHookEvent(req)` **first** and then writes `{}`; the `deny` path is untouched (a locked session never gets a terminal-only row). No other file: mod presence is read from P5a-2a's `modSeen` by `modPresent` (Task 8a.3), so the relay `hello` handler is **not** edited.
- Modify: `internal/module/team/hooks_test.go` (P2c's): append the route-level test below.
- Test: `internal/module/team/hook_observe_test.go`

**Interfaces:**
- Produces:
  ```go
  const HookAsksDir = "hookasks"                                   // <data_dir>/hookasks/<agent>/<session_id>, an empty file while the session has an open terminal_only row
  func (m *Module) observeHookEvent(req team.HookDecideRequest)   // never fails the request; never produces a decision
  ```
  Behaviour (spec §6.6 table row 3, U19 point 4, air26 point 4):
  - `PreToolUse` with `tool_name == AskUserQuestion` → a `hook_ask` row flagged `terminal_only` (payload: the input's `questions`), **unless** the session's mod said hello, nobody remote is connected, the daemon is stopping, or a row for that `tool_use_id` is already open. `PermissionRequest` → the same with `hook_permission` (payload: `tool_name`, `tool_input`, `permission_suggestions` from `raw`; `tool_use_id` is `""`, see `Contract problems` 3), **de-duplicated by session + `tool_name` + sha256 of the compacted `tool_input`** (`permissionRowOpen`): a re-fired prompt for the same call opens nothing more. Opening writes the flag.
  - `PostToolUse` of the row's `tool_use_id` (hook_ask): `answered_local` with `decided_by terminal` and the answers in `raw.tool_response.answers` when CC put them there, else `dismissed`. `PostToolUse` / `PostToolUseFailure` whose `tool_name` matches a permission row: `dismissed` (the user said Yes or the tool failed; a No never fires either — see Stop). `Stop`, `UserPromptSubmit`, `SessionEnd`: every open terminal_only row of the session → `dismissed`. The flag is removed when none is left.
  - Every close is `closeAs` (one `closed` event each); a `hook_ask` closed `answered_local` here is what the phone's card shows as 「已在終端機回答：紅」.
- Consumes: Tasks 8a.1–8a.5 (`openHookRow`, `hookPayloadFor`, `OpenTerminalOnlyBySession`, `OpenByToolUse`, `terminalClient`, `modPresent`, `responders`), P2c's `team.HookDecideRequest`.

- [ ] **Step 1: Write the failing tests.**

```go
package teammod

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/wake/purdex/internal/team"
)

func preAsk(sid, toolUse string) team.HookDecideRequest {
	return team.HookDecideRequest{Agent: "cc", Event: "PreToolUse", SessionID: sid, ToolName: "AskUserQuestion", ToolUseID: toolUse,
		ToolInput: json.RawMessage(`{"questions":` + askQuestions + `}`)}
}

func (f *fixture) flagExists(sid string) bool {
	_, err := os.Stat(filepath.Join(f.core.Cfg.DataDir, HookAsksDir, "cc", sid))
	return err == nil
}

// Without the mod, a PreToolUse/AskUserQuestion opens a terminal_only row
// and writes the flag; the matching PostToolUse closes it as answered_local
// with CC's answers and removes the flag.
func TestObserve_TerminalOnlyOpensAndClosesOnPostToolUse(t *testing.T) {
	f := newFixture(t)
	f.m.observeHookEvent(preAsk("sid-1", "toolu_a"))
	rows, _ := f.m.store.OpenTerminalOnlyBySession("sid-1")
	if len(rows) != 1 || rows[0].Kind != team.KindHookAsk || !isTerminalOnly(rows[0]) || rows[0].LeaseUntil != team.NoExpiryAt {
		t.Fatalf("rows = %+v", rows)
	}
	if !f.flagExists("sid-1") {
		t.Fatal("flag must exist while a terminal_only row is open")
	}
	if evs := f.events(); len(evs) != 1 || evs[0].Op != "opened" {
		t.Fatalf("events = %+v", evs)
	}
	// Repeat of the same PreToolUse opens nothing more.
	f.m.observeHookEvent(preAsk("sid-1", "toolu_a"))
	if rows, _ = f.m.store.OpenTerminalOnlyBySession("sid-1"); len(rows) != 1 {
		t.Fatalf("duplicate open: %d rows", len(rows))
	}
	f.m.observeHookEvent(team.HookDecideRequest{Agent: "cc", Event: "PostToolUse", SessionID: "sid-1", ToolName: "AskUserQuestion", ToolUseID: "toolu_a",
		Raw: json.RawMessage(`{"tool_response":{"questions":[],"answers":{"紅還是藍？":"紅"}}}`)})
	a, _, _ := f.m.store.Get(rows[0].ID)
	if a.State != team.StateAnsweredLocal || a.Hook == nil || a.Hook.Answers["紅還是藍？"] != "紅" || a.DecidedBy.Kind != team.ClientKindTerminal {
		t.Fatalf("after PostToolUse = %+v hook=%+v", a, a.Hook)
	}
	if f.flagExists("sid-1") {
		t.Fatal("flag must go with the last terminal_only row")
	}
}

// A mod-present session gets no terminal_only row; so does a host with no
// remote responder; a Stop closes everything of the session as dismissed.
func TestObserve_ModPresentOrNoRespondersOpensNothing_StopDismissesAll(t *testing.T) {
	f := newFixture(t)
	// Presence is the relay hello record (P5a-2a's modSeen): say hello as the mod would.
	if code, body := f.do(http.MethodPost, "/api/relay/hello", team.RelayHelloRequest{SessionID: "sid-1", ModVersion: "1", Agent: "cc"}); code != http.StatusOK {
		t.Fatalf("hello: %d %s", code, body)
	}
	f.m.observeHookEvent(preAsk("sid-1", "toolu_b"))
	if rows, _ := f.m.store.OpenTerminalOnlyBySession("sid-1"); len(rows) != 0 {
		t.Fatalf("mod present: rows = %+v", rows)
	}
	f.core.Events.RemoveTestSubscriber(f.sub)
	f.m.observeHookEvent(preAsk("sid-2", "toolu_c"))
	if rows, _ := f.m.store.OpenTerminalOnlyBySession("sid-2"); len(rows) != 0 {
		t.Fatalf("no responders: rows = %+v", rows)
	}
	f.sub = f.core.Events.AddTestSubscriber()
	f.m.observeHookEvent(preAsk("sid-2", "toolu_c"))
	f.m.observeHookEvent(team.HookDecideRequest{Agent: "cc", Event: "PermissionRequest", SessionID: "sid-2", ToolName: "Bash",
		ToolInput: json.RawMessage(`{"command":"ls"}`), Raw: json.RawMessage(`{"permission_suggestions":[{"type":"addRules"}]}`)})
	rows, _ := f.m.store.OpenTerminalOnlyBySession("sid-2")
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}
	perm := rows[0]
	if perm.Kind != team.KindHookPermission {
		perm = rows[1] // same created_at: the order is by id
	}
	var pp team.HookPermissionPayload
	if json.Unmarshal(perm.Payload, &pp) != nil || pp.ToolName != "Bash" || string(pp.Suggestions) != `[{"type":"addRules"}]` || !pp.TerminalOnly {
		t.Fatalf("permission payload = %s", perm.Payload)
	}
	f.events()
	f.m.observeHookEvent(team.HookDecideRequest{Agent: "cc", Event: "Stop", SessionID: "sid-2"})
	if rows, _ := f.m.store.OpenTerminalOnlyBySession("sid-2"); len(rows) != 0 {
		t.Fatalf("after Stop: rows = %+v", rows)
	}
	closed := 0
	for _, ev := range f.events() {
		if ev.Op == "closed" && ev.Approval.State == team.StateDismissed {
			closed++
		}
	}
	if closed != 2 || f.flagExists("sid-2") {
		t.Fatalf("closed=%d flag=%v", closed, f.flagExists("sid-2"))
	}
}

// A permission row closes when its tool's PostToolUse arrives (the user
// pressed Yes), by tool_name since PermissionRequest carries no tool_use_id.
func TestObserve_PermissionRowClosesOnItsToolsPostToolUse(t *testing.T) {
	f := newFixture(t)
	f.m.observeHookEvent(team.HookDecideRequest{Agent: "cc", Event: "PermissionRequest", SessionID: "sid-1", ToolName: "Bash", ToolInput: json.RawMessage(`{"command":"ls"}`)})
	// No tool_use_id on a PermissionRequest: the key is session + tool_name +
	// sha256(tool_input). The same prompt again (whitespace differs) opens
	// nothing; a different input for the same tool opens a second row.
	f.m.observeHookEvent(team.HookDecideRequest{Agent: "cc", Event: "PermissionRequest", SessionID: "sid-1", ToolName: "Bash", ToolInput: json.RawMessage(`{ "command": "ls" }`)})
	if rows, _ := f.m.store.OpenTerminalOnlyBySession("sid-1"); len(rows) != 1 {
		t.Fatalf("a re-fired PermissionRequest for the same call must not open a second row: %d rows", len(rows))
	}
	f.m.observeHookEvent(team.HookDecideRequest{Agent: "cc", Event: "PermissionRequest", SessionID: "sid-1", ToolName: "Bash", ToolInput: json.RawMessage(`{"command":"rm x"}`)})
	if rows, _ := f.m.store.OpenTerminalOnlyBySession("sid-1"); len(rows) != 2 {
		t.Fatalf("a different input is another prompt: %d rows, want 2", len(rows))
	}
	f.m.observeHookEvent(team.HookDecideRequest{Agent: "cc", Event: "PostToolUse", SessionID: "sid-1", ToolName: "Edit", ToolUseID: "toolu_x"})
	if rows, _ := f.m.store.OpenTerminalOnlyBySession("sid-1"); len(rows) != 2 {
		t.Fatalf("another tool's PostToolUse must not close them: %d rows", len(rows))
	}
	f.m.observeHookEvent(team.HookDecideRequest{Agent: "cc", Event: "PostToolUse", SessionID: "sid-1", ToolName: "Bash", ToolUseID: "toolu_y"})
	if rows, _ := f.m.store.OpenTerminalOnlyBySession("sid-1"); len(rows) != 0 {
		t.Fatalf("Bash's PostToolUse must close it: %d rows", len(rows))
	}
}
```

Append to `internal/module/team/hooks_test.go` (P2c's file) — the route-level check that the forwarded events reach `observeHookEvent` through `POST /api/hooks/decide` and are answered `200 {}` (not 400), with no lock flag involved:

```go
// P8a-1d: the settings hooks forward PreToolUse/AskUserQuestion and the
// closing events to the decide route. Through the route — not by calling
// observeHookEvent directly — a terminal_only row opens and a Stop closes
// it, each answered 200 {}; a session with no hooklocks flag is the normal
// case here.
func TestHookDecide_RouteOpensAndClosesTerminalOnlyRows(t *testing.T) {
	f := newFixture(t)
	code, body := f.do(http.MethodPost, "/api/hooks/decide", team.HookDecideRequest{Agent: "cc", Event: "PreToolUse", SessionID: "sid-1", ToolName: "AskUserQuestion", ToolUseID: "toolu_r",
		ToolInput: json.RawMessage(`{"questions":[{"question":"紅還是藍？"}]}`)})
	if code != http.StatusOK || string(body) != "{}\n" {
		t.Fatalf("PreToolUse/AskUserQuestion: %d %q, want 200 {}", code, body)
	}
	if rows, _ := f.m.store.OpenTerminalOnlyBySession("sid-1"); len(rows) != 1 {
		t.Fatalf("rows after PreToolUse = %d, want 1", len(rows))
	}
	code, body = f.do(http.MethodPost, "/api/hooks/decide", team.HookDecideRequest{Agent: "cc", Event: "Stop", SessionID: "sid-1"})
	if code != http.StatusOK || string(body) != "{}\n" {
		t.Fatalf("Stop: %d %q, want 200 {} (never 400)", code, body)
	}
	if rows, _ := f.m.store.OpenTerminalOnlyBySession("sid-1"); len(rows) != 0 {
		t.Fatalf("rows after Stop = %d, want 0", len(rows))
	}
}
```

(`encoding/json` is already imported in `hooks_test.go` by P2c's tests; add it if not.)

- [ ] **Step 2: Run the tests and verify they fail.**
  - Run: `go test ./internal/module/team/ -run 'TestObserve|TestHookDecide_Route' -v`
  - Expected: FAIL to compile — `f.m.observeHookEvent undefined`, `undefined: HookAsksDir`; once stubbed, `TestHookDecide_RouteOpensAndClosesTerminalOnlyRows` fails at `rows after PreToolUse = 0` until the handler calls `observeHookEvent`.

- [ ] **Step 3: Implement.** Create `internal/module/team/hook_observe.go`:

```go
package teammod

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"

	"github.com/wake/purdex/internal/team"
)

// Terminal-only degradation (spec §6.6 table row 3, U19 point 4): a
// session WITHOUT the Purdex mod (--safe-mode, started before the install,
// Codex) still shows its AskUserQuestion and permission prompts to remote
// clients as read-only cards, through the settings hooks:
//
//   - PreToolUse/AskUserQuestion, PermissionRequest (forwarded ungated by
//     `pdx hook`): open a hook row flagged terminal_only when the session's
//     mod has not said hello and someone remote is connected; the answer to
//     the hook is {} either way, so the native dialog shows at once.
//   - PostToolUse, PostToolUseFailure, Stop, UserPromptSubmit, SessionEnd
//     (forwarded only while the session's HookAsksDir flag exists): close
//     the session's terminal_only rows — PostToolUse/AskUserQuestion as
//     answered_local with the answers CC put in tool_response, a matching
//     PostToolUse(Failure) of a permission row's tool as dismissed, Stop /
//     UserPromptSubmit / SessionEnd everything of the session as dismissed.
//
// The flag file <data_dir>/hookasks/<agent>/<session_id> exists while the
// session has at least one open terminal_only row; the daemon writes and
// removes it here, so `pdx hook` pays a stat, not a round trip, on the
// frequent events. Nothing here ever produces a decision.

// HookAsksDir is the flag dir, next to P2c's HookLocksDir.
const HookAsksDir = "hookasks"

// observeHookEvent is called by the /api/hooks/decide handler for every
// request that produced no lock decision. It never fails the request.
func (m *Module) observeHookEvent(req team.HookDecideRequest) {
	if req.SessionID == "" {
		return
	}
	switch req.Event {
	case "PreToolUse":
		if req.ToolName == "AskUserQuestion" {
			m.openTerminalOnly(req, team.KindHookAsk)
		}
	case "PermissionRequest":
		m.openTerminalOnly(req, team.KindHookPermission)
	case "PostToolUse", "PostToolUseFailure":
		m.closeTerminalOnlyForTool(req)
	case "Stop", "UserPromptSubmit", "SessionEnd":
		m.closeTerminalOnlyAll(req.Agent, req.SessionID)
	}
}

// terminalOnlyPayload builds the row payload from the hook's fields.
func terminalOnlyPayload(req team.HookDecideRequest, kind team.Kind) ([]byte, string) {
	switch kind {
	case team.KindHookAsk:
		var in struct {
			Questions json.RawMessage `json:"questions"`
		}
		_ = json.Unmarshal(req.ToolInput, &in)
		p, _ := json.Marshal(team.HookAskPayload{Questions: in.Questions})
		return hookPayloadFor(kind, req.ToolUseID, p, true)
	default:
		var raw struct {
			Suggestions json.RawMessage `json:"permission_suggestions"`
		}
		_ = json.Unmarshal(req.Raw, &raw)
		p, _ := json.Marshal(team.HookPermissionPayload{ToolName: req.ToolName, ToolInput: req.ToolInput, Suggestions: raw.Suggestions})
		return hookPayloadFor(kind, req.ToolUseID, p, true)
	}
}

func (m *Module) openTerminalOnly(req team.HookDecideRequest, kind team.Kind) {
	if m.modPresent(req.SessionID) || !m.responders.Any() || m.stopping() {
		return
	}
	payload, bad := terminalOnlyPayload(req, kind)
	if bad != "" {
		m.logf("[team] hook observe %s/%s: %s", req.Event, req.ToolName, bad)
		return
	}
	origin, ok, err := m.origins.ResolveOriginBySession(req.SessionID)
	if err != nil || !ok {
		return
	}
	m.createMu.Lock()
	defer m.createMu.Unlock()
	if m.stopping() {
		return
	}
	if req.ToolUseID != "" {
		if _, found, err := m.store.OpenByToolUse(origin.SessionID, req.ToolUseID); err != nil || found {
			return // the mod got here first (or the DB failed): nothing to add
		}
	} else if kind == team.KindHookPermission {
		// PermissionRequest carries no tool_use_id (Contract problems 3): the
		// row's key is session + tool_name + sha256(tool_input). A re-fired
		// prompt for the same call opens nothing more.
		if m.permissionRowOpen(origin.SessionID, req.ToolName, req.ToolInput) {
			return
		}
	}
	if _, err := m.openHookRow(origin, kind, payload, true); err != nil {
		m.logf("[team] hook observe: open terminal_only: %v", err)
		return
	}
	m.setAskFlag(req.Agent, req.SessionID, true)
}

// toolInputHash is sha256 over the compacted tool_input JSON (byte-equal
// inputs hash equal whatever the whitespace); "" for an empty input.
func toolInputHash(in json.RawMessage) string {
	var buf bytes.Buffer
	if len(in) == 0 || json.Compact(&buf, in) != nil {
		return ""
	}
	sum := sha256.Sum256(buf.Bytes())
	return hex.EncodeToString(sum[:])
}

// permissionRowOpen reports whether a terminal_only hook_permission row for
// the same (session, tool_name, tool_input hash) is already open.
func (m *Module) permissionRowOpen(sessionID, toolName string, in json.RawMessage) bool {
	rows, err := m.store.OpenTerminalOnlyBySession(sessionID)
	if err != nil {
		return true // do not pile rows on a failing DB
	}
	want := toolInputHash(in)
	for _, a := range rows {
		if a.Kind != team.KindHookPermission {
			continue
		}
		var p team.HookPermissionPayload
		if json.Unmarshal(a.Payload, &p) == nil && p.ToolName == toolName && toolInputHash(p.ToolInput) == want {
			return true
		}
	}
	return false
}

// answersOf reads PostToolUse's tool_response.answers for AskUserQuestion.
func answersOf(raw json.RawMessage) map[string]string {
	var r struct {
		ToolResponse struct {
			Answers map[string]string `json:"answers"`
		} `json:"tool_response"`
	}
	if json.Unmarshal(raw, &r) != nil || len(r.ToolResponse.Answers) == 0 {
		return nil
	}
	return r.ToolResponse.Answers
}

func (m *Module) closeTerminalOnlyForTool(req team.HookDecideRequest) {
	rows, err := m.store.OpenTerminalOnlyBySession(req.SessionID)
	if err != nil {
		m.logf("[team] hook observe: %v", err)
		return
	}
	now := m.now()
	for _, a := range rows {
		switch a.Kind {
		case team.KindHookAsk:
			var p team.HookAskPayload
			if json.Unmarshal(a.Payload, &p) != nil || p.ToolUseID != req.ToolUseID {
				continue
			}
			c := Close{State: team.StateDismissed, DecidedAt: now}
			if req.Event == "PostToolUse" {
				if answers := answersOf(req.Raw); answers != nil {
					c = Close{State: team.StateAnsweredLocal, DecidedAt: now, DecidedBy: terminalClient(), Hook: &team.HookDecision{Answers: answers}}
				}
			}
			if _, _, err := m.closeAs(a.ID, c); err != nil {
				m.logf("[team] hook observe: close %s: %v", a.ID, err)
			}
		case team.KindHookPermission:
			var p team.HookPermissionPayload
			if json.Unmarshal(a.Payload, &p) != nil || p.ToolName != req.ToolName {
				continue
			}
			if _, _, err := m.closeAs(a.ID, Close{State: team.StateDismissed, DecidedAt: now}); err != nil {
				m.logf("[team] hook observe: close %s: %v", a.ID, err)
			}
		}
	}
	m.refreshAskFlag(req.Agent, req.SessionID)
}

func (m *Module) closeTerminalOnlyAll(agent, sessionID string) {
	rows, err := m.store.OpenTerminalOnlyBySession(sessionID)
	if err != nil {
		m.logf("[team] hook observe: %v", err)
		return
	}
	now := m.now()
	for _, a := range rows {
		if _, _, err := m.closeAs(a.ID, Close{State: team.StateDismissed, DecidedAt: now}); err != nil {
			m.logf("[team] hook observe: close %s: %v", a.ID, err)
		}
	}
	m.setAskFlag(agent, sessionID, false)
}

// refreshAskFlag keeps the flag iff the session still has an open
// terminal_only row.
func (m *Module) refreshAskFlag(agent, sessionID string) {
	rows, err := m.store.OpenTerminalOnlyBySession(sessionID)
	if err != nil {
		return
	}
	m.setAskFlag(agent, sessionID, len(rows) > 0)
}

// askFlagPath is <data_dir>/hookasks/<agent>/<session_id>; agent defaults
// to cc. A session id is a UUID, so it is a safe file name. m.dataDir is
// P2c's copy of Cfg.DataDir (set in Init), the same one hooklocks uses.
func (m *Module) askFlagPath(agent, sessionID string) string {
	if agent == "" {
		agent = "cc"
	}
	return filepath.Join(m.dataDir, HookAsksDir, filepath.Base(agent), filepath.Base(sessionID))
}

func (m *Module) setAskFlag(agent, sessionID string, on bool) {
	p := m.askFlagPath(agent, sessionID)
	if !on {
		_ = os.Remove(p)
		return
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		m.logf("[team] hook observe: flag dir: %v", err)
		return
	}
	if err := os.WriteFile(p, nil, 0o600); err != nil {
		m.logf("[team] hook observe: flag: %v", err)
	}
}
```

  Then the `hooks.go` edit named under **Files**: in `handleHookDecide`, replace each of the three `m.writeJSON(w, http.StatusOK, team.HookDecideResponse{})` lines with `m.answerEmptyDecision(w, req)` and add, in `hook_observe.go`:

```go
// answerEmptyDecision is every "no decision" answer of /api/hooks/decide:
// the event is observed for the terminal-only degradation first (it never
// fails the request), then {} is written. P2c's deny path never comes here.
func (m *Module) answerEmptyDecision(w http.ResponseWriter, req team.HookDecideRequest) {
	m.observeHookEvent(req)
	m.writeJSON(w, http.StatusOK, team.HookDecideResponse{})
}
```

  (`net/http` joins `hook_observe.go`'s imports.) The route-level test appended to `hooks_test.go` (Step 1) pins it: `PreToolUse`/`AskUserQuestion` without a lock flag answers `200 {}` **and** leaves one open `hook_ask` row with `terminal_only: true`; `Stop` answers `200 {}` and closes it. (`PreToolUse`/`Bash` answers `{}` and opens nothing — covered by `TestObserve_*` through `askForward`'s predicate on the hook side and `observeHookEvent`'s tool-name check.)

- [ ] **Step 4: Run the tests and verify they pass.**
  - Run: `go vet ./internal/module/team/ && go test ./internal/module/team/ -count=3 && go test ./internal/module/peers/ ./cmd/pdx/`
  - Expected: PASS (P2c's `TestHookDecide_*` still pass: the `{}` bodies are byte-identical and the lead-lock flag handling did not move).

- [ ] **Step 5: Commit.**
  ```bash
  git add internal/module/team/hook_observe.go internal/module/team/hook_observe_test.go internal/module/team/hooks.go internal/module/team/hooks_test.go
  git commit -m "feat(team): terminal-only 分流 rows for sessions without the mod, closed by the settings hooks

  Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
  ```

---

## PR P8a-2 — the mod: `tool.call{AskUserQuestion}` race, the hold measured, the Codex probe (≈ 300 lines + two measurements)

### Task 8a.9: `hooks/ask.js` and its `claude plugin test` cases

**Files** (the plugin tree lives under `cmd/pdx/plugin/purdex/` — `cmd/pdx/plugin/` itself is the Go package `embed.go`, P5b-1 Task 5b.1):
- Create: `cmd/pdx/plugin/purdex/hooks/ask.js`, `cmd/pdx/plugin/purdex/hooks/ask.test.ts`
- Modify: `cmd/pdx/plugin/purdex/hooks/hooks.json` (P5b-1's `{"modules":["./register.js"]}` → `{"modules":["./register.js","./ask.js"]}`)
- Modify: `cmd/pdx/plugin/embed_test.go` — P5b-1's `TestFiles_HasTheLayoutClaudeLoads` pins `len(h.Modules) != 1 || h.Modules[0] != "./register.js"`; change that assertion to `len(h.Modules) != 2 || h.Modules[0] != "./register.js" || h.Modules[1] != "./ask.js"` and add `"hooks/ask.js"` to the `fs.Stat` list (without this the Go test goes red the moment `hooks.json` changes)
- Not modified: `cmd/pdx/plugin/purdex/.claude-plugin/plugin.json` — P5b-1 restamps `version` from `pdx version` at extraction, so a new pdx build is what redeploys the mod
- Test: `claude plugin test cmd/pdx/plugin/purdex`; `go test ./cmd/pdx/plugin/`

**Interfaces:**
- Produces: the `tool.call` hook on `AskUserQuestion` (spec §6.6 steps 1–7). Order of operations, which differs from the spec's step order in one deliberate way: **`next(e)` is issued first, `pdx ask begin` second**, so the native dialog never waits for the daemon (a restarting daemon costs the person nothing; `no_responders` and every failure just let the dialog run). If the person answers before `begin` returns, the row `begin` did open is reported `answered_local` / `dismissed` after the fact so the cards still show the terminal's answer.
  - a subagent's call (`e.agentId` set) ⇒ `next(e)` as the very first line, no pdx call (coordinator decision: subagents are not relayed in v1);
  - headless (`$.session.surfaces()` empty) ⇒ `next(e)`, no pdx call;
  - `begin` ≠ 0 / no id (13 no_responders, 20, 21, 1) ⇒ the native outcome; a 13's code is read as the **last whitespace-separated token of stderr** (P8a-1c prints `pdx ask: <detail> <code>`, the same shape as `pdx relay`), only for the log line — every non-zero exit takes the same branch; a `409 ask_open` never reaches the mod as an error: `pdx ask begin` adopts the open row and exits 0 with its id (Task 8a.6), so a replayed `begin` after a lost response continues with the same `id`;
  - loop `$.process.run([<pdx>,'ask','wait',id], { timeoutMs: 590_000 })` while `still_open`; `Promise.race` with the native promise;
  - terminal first ⇒ `report <id> answered_local --hook {"answers":…}` (or `dismissed` when the native result has no answers — Esc, an interrupted turn, an error result), return the native object **unchanged**;
  - remote first ⇒ `return { result: { questions: e.questions, answers } }` with `next(e)` pending (closes the dialog, M24 P-B);
  - row closed another way (abandoned, the backstop's dismissed) or the loop failed ⇒ the native outcome;
  - `.catch(($, e, next) => next(e))`: any failure of ours replays the native call.
- Consumes: `pdx ask begin|wait|report` (Task 8a.6), run **by the path in `$.plugin.root + '/pdx.json'`** (`{pdx, data_dir}`, written beside `VERSION` by P5b-1's extractor) exactly as `register.js` does — the install flow does not put `pdx` on `PATH` (project CLAUDE.md "`pdx: command not found`"), so a bare `'pdx'` would reject `begin` on a real host and the 分流 would silently never run; `'pdx'` is the fallback only when `pdx.json` is absent (`claude plugin test`). The file is read once per `tool.call` (cheap, and the binary can move between sessions). `$.session.id()`, `$.session.surfaces()`, `$.fs.read`, `$.process.run` (d.ts 3447: `timeoutMs` ten minutes at most).

- [ ] **Step 1: Write the failing tests.** Create `cmd/pdx/plugin/purdex/hooks/ask.test.ts`:

```ts
// hooks/ask.test.ts — 分流 for AskUserQuestion (lead-team spec §15 "Mod (U19)"), run by `claude plugin test`.
// The test's `on` hooks stand beneath the plugin as the engine: `tool.call` is the native dialog,
// `process.run` is `pdx`, `session.id` / `session.surfaces` are the session.
import { test, expect } from 'claude-code/testing'

const Q = [{ question: '紅還是藍？', header: '顏色', options: [{ label: '紅', description: 'r' }, { label: '藍', description: 'b' }], multiSelect: false }]
const NATIVE_RED = { ref: 1, result: { questions: Q, answers: { '紅還是藍？': '紅' }, annotations: {} }, text: 'Your questions have been answered', isReadOnly: true }
const ok = (stdout: string, exitCode = 0, stderr = '') => ({ value: { exitCode, stdout, stderr, isStdoutTruncated: false, isStderrTruncated: false } })
const never = () => new Promise<never>(() => {})

type Call = string[]
type Resolver = ((v: unknown) => void) | null
// session stands in for the engine: the id, the surfaces, and pdx.json
// (absent by default, as under `claude plugin test` on a dev machine).
function session(on: Parameters<Parameters<typeof test>[1]>[1], surfaces: string[] = ['terminal'], pdxJSON?: string) {
  on('session.id', () => ({ value: 'sess-test' }))
  on('session.surfaces', () => ({ value: surfaces }))
  on('fs.read', (_$, e) => (pdxJSON !== undefined && e.path.endsWith('/pdx.json') ? { value: pdxJSON } : { deny: 'ENOENT' }))
}
const sub = (a: readonly string[]) => a[2] // <pdx> ask <sub> …

test('no_responders ⇒ the native dialog runs alone: next(e) once, no further pdx call', async ($, on) => {
  session(on)
  const calls: Call[] = []
  let nextCalls = 0
  let answerNative: Resolver = null
  on('process.run', ($, e) => {
    calls.push([...e.argv])
    // The person answers only after the daemon has said no_responders: the branch under test is the one that runs.
    setTimeout(() => answerNative && answerNative(NATIVE_RED), 50)
    // P8a-1c's stderr shape: `pdx ask: <detail> <code>`, the code last.
    return ok('', 13, 'pdx ask: 沒有連線中的客戶端可以回答 no_responders\n')
  })
  on('tool.call', { tool: 'AskUserQuestion' }, () => { nextCalls++; return new Promise((res) => { answerNative = res }) })
  const r = await $.tool.call({ tool: 'AskUserQuestion', questions: Q })
  expect(r.result).toEqual(NATIVE_RED.result)
  expect(nextCalls).toBe(1)
  expect(calls.map(sub)).toEqual(['begin'])
  expect(calls[0].slice(0, 3)).toEqual(['pdx', 'ask', 'begin']) // no pdx.json ⇒ `pdx` from PATH
  expect(calls[0]).toContain('--session'); expect(calls[0]).toContain('sess-test')
  expect(JSON.parse(calls[0][calls[0].indexOf('--payload') + 1])).toEqual({ questions: Q })
})

test('a subagent\'s AskUserQuestion (agentId set) is not relayed: plain next(e), no pdx at all', async ($, on) => {
  session(on)
  const calls: Call[] = []
  let nextCalls = 0
  on('process.run', ($, e) => { calls.push([...e.argv]); return ok('{"id":"x"}') })
  on('tool.call', { tool: 'AskUserQuestion' }, () => { nextCalls++; return NATIVE_RED })
  const r = await $.tool.call({ tool: 'AskUserQuestion', questions: Q, agentId: 'agent-7' })
  expect(r.result).toEqual(NATIVE_RED.result)
  expect(nextCalls).toBe(1)
  expect(calls).toEqual([])
})

test('pdx is run by the path in pdx.json when the file exists (the installed binary is not on PATH)', async ($, on) => {
  session(on, ['terminal'], '{"pdx":"/opt/pdx/bin/pdx","data_dir":"/tmp/pdx"}')
  const calls: Call[] = []
  on('process.run', ($, e) => {
    calls.push([...e.argv])
    if (sub(e.argv) === 'begin') return ok('{"id":"r7"}')
    return ok('{"state":"answered_remote","hook":{"answers":{"紅還是藍？":"藍"}}}')
  })
  on('tool.call', { tool: 'AskUserQuestion' }, never)
  const r = await $.tool.call({ tool: 'AskUserQuestion', questions: Q })
  expect(r.result).toEqual({ questions: Q, answers: { '紅還是藍？': '藍' } })
  expect(calls.map((a) => a[0])).toEqual(['/opt/pdx/bin/pdx', '/opt/pdx/bin/pdx'])
  expect(calls.map(sub)).toEqual(['begin', 'wait'])
})

test('headless (no surface) ⇒ plain next(e), no pdx at all', async ($, on) => {
  session(on, [])
  const calls: Call[] = []
  on('process.run', ($, e) => { calls.push([...e.argv]); return ok('{"id":"x"}') })
  on('tool.call', { tool: 'AskUserQuestion' }, () => NATIVE_RED)
  const r = await $.tool.call({ tool: 'AskUserQuestion', questions: Q })
  expect(r.result).toEqual(NATIVE_RED.result)
  expect(calls).toEqual([])
})

test('terminal first ⇒ the native result is returned unchanged and answered_local is reported with its answers', async ($, on) => {
  session(on)
  let releaseWait: Resolver = null
  let answerNative: Resolver = null
  const reported = new Promise<Call>((resolve) => {
    on('process.run', ($, e) => {
      const a = e.argv
      if (sub(a) === 'begin') return ok('{"id":"r2"}\n')
      // The person answers while the first wait round is in flight (the race proper, not the early branch).
      if (sub(a) === 'wait') { setTimeout(() => answerNative && answerNative(NATIVE_RED), 20); return new Promise((res) => { releaseWait = res }) }
      if (sub(a) === 'report') { resolve([...a]); return ok('{}') }
      return ok('')
    })
  })
  on('tool.call', { tool: 'AskUserQuestion' }, () => new Promise((res) => { answerNative = res }))
  const r = await $.tool.call({ tool: 'AskUserQuestion', questions: Q })
  expect(r).toEqual(expect.objectContaining({ result: NATIVE_RED.result, isReadOnly: true, ref: 1 }))
  const rep = await reported
  expect(rep.slice(0, 5)).toEqual(['pdx', 'ask', 'report', 'r2', 'answered_local'])
  expect(JSON.parse(rep[6])).toEqual({ answers: { '紅還是藍？': '紅' } })
  if (releaseWait) releaseWait(ok('{"state":"closed","reason":"answered_local"}'))
})

// Review Focus 5 / spec §6.6 step 5 (terminal_override), the interleaving proper: the phone's
// answer reaches the mod a beat AFTER the terminal's. The fake pdx holds `wait` until the native
// promise has resolved and only then answers answered_remote 藍. The mod must return the native
// result (紅) and report answered_local; the late remote answer is ignored (the daemon, which saw
// the remote decide win its CAS first, records terminal_override — the terminal still stands).
// Mutation gate: consult `remote` again after the race (`const late = await remote; if (late.who
// === 'remote') return { result: … }`), i.e. prefer a remote answer that arrived after the native
// one → `answers` is 藍 and no answered_local report → red.
test('interleaving: answered_remote arrives after the terminal already answered ⇒ native result, answered_local reported, the late remote ignored', async ($, on) => {
  session(on)
  let answerNative: Resolver = null
  let nativeDone: Promise<unknown> = Promise.resolve()
  const reported = new Promise<Call>((resolve) => {
    on('process.run', ($, e) => {
      const a = e.argv
      if (sub(a) === 'begin') return ok('{"id":"r5"}')
      // answered_remote only once the native promise has resolved — never before.
      if (sub(a) === 'wait') return nativeDone.then(() => ok('{"state":"answered_remote","hook":{"answers":{"紅還是藍？":"藍"}}}'))
      if (sub(a) === 'report') { resolve([...a]); return ok('{}') }
      return ok('')
    })
  })
  nativeDone = new Promise((done) => {
    on('tool.call', { tool: 'AskUserQuestion' }, () => new Promise((res) => { answerNative = (v) => { res(v); done(v) } }))
  })
  setTimeout(() => answerNative && answerNative(NATIVE_RED), 20)
  const r = await $.tool.call({ tool: 'AskUserQuestion', questions: Q })
  expect(r).toEqual(expect.objectContaining({ result: NATIVE_RED.result, isReadOnly: true, ref: 1 }))
  expect(r.result.answers).toEqual({ '紅還是藍？': '紅' }) // the terminal's, not the phone's
  const rep = await reported
  expect(rep.slice(2, 5)).toEqual(['report', 'r5', 'answered_local'])
  expect(JSON.parse(rep[6])).toEqual({ answers: { '紅還是藍？': '紅' } })
})

test('the person answers before begin has even returned ⇒ native result, and the row begin opened is reported', async ($, on) => {
  session(on)
  let answerBegin: Resolver = null
  const reported = new Promise<Call>((resolve) => {
    on('process.run', ($, e) => {
      const a = e.argv
      if (sub(a) === 'begin') return new Promise((res) => { answerBegin = res })
      if (sub(a) === 'report') { resolve([...a]); return ok('{}') }
      return ok('')
    })
  })
  on('tool.call', { tool: 'AskUserQuestion' }, () => NATIVE_RED)
  const r = await $.tool.call({ tool: 'AskUserQuestion', questions: Q })
  expect(r).toEqual(expect.objectContaining({ result: NATIVE_RED.result, isReadOnly: true }))
  if (answerBegin) answerBegin(ok('{"id":"r9"}'))
  expect((await reported).slice(2, 5)).toEqual(['report', 'r9', 'answered_local'])
})

test('remote first ⇒ { result: { questions, answers } } in the shape M24 measured; the native dialog is left pending', async ($, on) => {
  session(on)
  on('process.run', ($, e) => {
    const a = e.argv
    if (sub(a) === 'begin') return ok('{"id":"r1"}\n')
    if (sub(a) === 'wait') return ok('{"state":"answered_remote","hook":{"answers":{"紅還是藍？":"藍"}}}\n')
    return ok('')
  })
  on('tool.call', { tool: 'AskUserQuestion' }, never)
  const r = await $.tool.call({ tool: 'AskUserQuestion', questions: Q })
  expect(r.result).toEqual({ questions: Q, answers: { '紅還是藍？': '藍' } })
})

test('still_open loops without returning: N rounds, then the remote answer', async ($, on) => {
  session(on)
  let waits = 0
  on('process.run', ($, e) => {
    const a = e.argv
    if (sub(a) === 'begin') return ok('{"id":"r1"}')
    if (sub(a) === 'wait') { waits++; return ok(waits < 12 ? '{"state":"still_open"}' : '{"state":"answered_remote","hook":{"answers":{"紅還是藍？":"藍"}}}') }
    return ok('')
  })
  on('tool.call', { tool: 'AskUserQuestion' }, never)
  const r = await $.tool.call({ tool: 'AskUserQuestion', questions: Q })
  expect(r.result).toEqual({ questions: Q, answers: { '紅還是藍？': '藍' } })
  expect(waits).toBe(12)
})

test('a hold across several wait rounds keeps the dialog open: the terminal answers after 3 rounds and wins', async ($, on) => {
  session(on)
  let waits = 0
  let answerNative: Resolver = null
  const reported = new Promise<Call>((resolve) => {
    on('process.run', ($, e) => {
      const a = e.argv
      if (sub(a) === 'begin') return ok('{"id":"r3"}')
      if (sub(a) === 'wait') {
        waits++
        if (waits === 3 && answerNative) answerNative(NATIVE_RED) // the person answers during the third round
        return waits <= 3 ? ok('{"state":"still_open"}') : new Promise(() => {})
      }
      if (sub(a) === 'report') { resolve([...a]); return ok('{}') }
      return ok('')
    })
  })
  on('tool.call', { tool: 'AskUserQuestion' }, () => new Promise((res) => { answerNative = res }))
  const r = await $.tool.call({ tool: 'AskUserQuestion', questions: Q })
  expect(r).toEqual(expect.objectContaining({ result: NATIVE_RED.result, isReadOnly: true }))
  expect((await reported)[4]).toBe('answered_local')
  expect(waits).toBeGreaterThanOrEqual(3)
})

test('Esc (the native dialog settles without answers) ⇒ dismissed is reported and the native outcome is returned', async ($, on) => {
  session(on)
  const DECLINED = { ref: 2, result: { questions: Q, answers: {} }, text: 'User declined to answer questions', isError: true }
  let answerNative: Resolver = null
  const reported = new Promise<Call>((resolve) => {
    on('process.run', ($, e) => {
      const a = e.argv
      if (sub(a) === 'begin') return ok('{"id":"r4"}')
      if (sub(a) === 'wait') { setTimeout(() => answerNative && answerNative(DECLINED), 20); return new Promise(() => {}) }
      if (sub(a) === 'report') { resolve([...a]); return ok('{}') }
      return ok('')
    })
  })
  on('tool.call', { tool: 'AskUserQuestion' }, () => new Promise((res) => { answerNative = res }))
  const r = await $.tool.call({ tool: 'AskUserQuestion', questions: Q })
  expect(r).toEqual(expect.objectContaining({ isError: true, text: 'User declined to answer questions' }))
  const rep = await reported
  expect(rep.slice(2, 5)).toEqual(['report', 'r4', 'dismissed'])
  expect(rep).not.toContain('--hook')
})

test('begin that fails (daemon down, exit 20) ⇒ the native dialog runs alone and nothing more is called', async ($, on) => {
  session(on)
  const calls: Call[] = []
  let answerNative: Resolver = null
  on('process.run', ($, e) => {
    calls.push([...e.argv])
    setTimeout(() => answerNative && answerNative(NATIVE_RED), 50)
    return ok('', 20, 'pdx ask: daemon_unavailable\n')
  })
  on('tool.call', { tool: 'AskUserQuestion' }, () => new Promise((res) => { answerNative = res }))
  const r = await $.tool.call({ tool: 'AskUserQuestion', questions: Q })
  expect(r.result).toEqual(NATIVE_RED.result)
  expect(calls.map(sub)).toEqual(['begin'])
})
```

- [ ] **Step 2: Run the tests and verify they fail.**
  - Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/lead-team-p8a && claude plugin test cmd/pdx/plugin/purdex`
  - Expected: with `ask.js` absent from `hooks.json`, every test but "headless" and "a subagent's AskUserQuestion …" fails — e.g. `no_responders …`: `expect(calls.map(sub)).toEqual(['begin'])` → `Expected: ["begin"] Received: []` (no hook ran; the native stand-in answered). (The nine tests measured in the scratch build were run against the `cmd/pdx/plugin/hooks/` layout of that build; the two tests added at the consistency fix — `agentId`, `pdx.json` path — and the `interleaving …` test of the codex round are unmeasured.)
  - Run: `go test ./cmd/pdx/plugin/` after editing `hooks.json` and before `embed_test.go` → FAIL at `hooks.json = {"modules":["./register.js","./ask.js"]}`; then apply the `embed_test.go` change under **Files**.

- [ ] **Step 3: Implement.** Create `cmd/pdx/plugin/purdex/hooks/ask.js` and list it in `hooks.json`:

```js
// hooks/ask.js — 分流 for AskUserQuestion (lead-team spec §6.6 steps 1–7, U19; facts M24).
//
// The engine's own dialog is drawn untouched (`next(e)` issued, not awaited) and raced
// against the daemon: `pdx ask begin` opens a hook_ask row for the connected clients,
// `pdx ask wait` long-polls it in bounded rounds (each ≤ 9 min, inside $.process.run's
// ten-minute cap), and whichever answers first wins. Terminal first ⇒ the native result
// is returned unchanged and reported as answered_local; remote first ⇒ `{ result }` is
// returned, which closes the native dialog at once; Esc ⇒ dismissed. No responder, a
// daemon that is down, or any failure ⇒ the native dialog runs alone.
//
// Nothing here ever holds the dialog: `next(e)` goes out before the first daemon call.

const WAIT_TIMEOUT_MS = 590_000 // $.process.run is capped at 600 000; `pdx ask wait` returns after ≤ 9 min
const CALL_TIMEOUT_MS = 40_000 // begin / report: the daemon client's 30 s restart grace plus room

const parse = (s) => { try { return JSON.parse(s) } catch { return null } }

// pdxPath is the installed binary named in pdx.json beside VERSION (P5b-1's
// extractor writes it; the install flow does not put pdx on PATH). Absent —
// as under `claude plugin test` — it is `pdx` from PATH, like register.js.
async function pdxPath($) {
  const cfg = parse(await $.fs.read($.plugin.root + '/pdx.json').catch(() => ''))
  return cfg && typeof cfg.pdx === 'string' && cfg.pdx ? cfg.pdx : 'pdx'
}

function pdx($, bin, args, timeoutMs) {
  return $.process.run([bin, 'ask', ...args], { timeoutMs })
}

// stderrCode reads the API code `pdx ask` prints as the LAST whitespace-
// separated stderr token (`pdx ask: <detail> <code>`, the same shape as
// `pdx relay`); used for the log line only — every non-zero exit takes the
// same "native dialog alone" branch.
function stderrCode(r) {
  return ((r && r.stderr) || '').trim().split(/\s+/).pop() || ''
}

// What the native dialog settled to, read for the report: the answers when
// the person answered, else a dismissal (Esc, an interrupted turn, an error).
function nativeOutcome(r) {
  const answers = r && r.deny === undefined && r.isError !== true && r.result && r.result.answers
  return answers && typeof answers === 'object' && Object.keys(answers).length > 0
    ? { state: 'answered_local', hook: { answers } }
    : { state: 'dismissed' }
}

function report($, bin, id, outcome) {
  const args = ['report', id, outcome.state]
  if (outcome.hook) args.push('--hook', JSON.stringify(outcome.hook))
  return pdx($, bin, args, CALL_TIMEOUT_MS).catch(() => {})
}

export function register(on) {
  on('tool.call', { tool: 'AskUserQuestion' }, async ($, e, next) => {
    // A subagent's question is not relayed in v1 (coordinator decision): its
    // dialog shows in the same terminal, but the row would name the wrong
    // conversation. Plain pass-through, before anything else.
    if (e.agentId) return next(e)
    // Headless (claude -p) has no dialog to race; a worker's questions are Nexen's (spec §6.6).
    if ((await $.session.surfaces()).length === 0) return next(e)

    const native = next(e).then((r) => ({ who: 'native', r }), (err) => ({ who: 'native-error', err }))

    const bin = await pdxPath($)
    const sid = await $.session.id()
    const begin = pdx($, bin, ['begin', '--session', sid, '--tool-use', e.tool_use_id, '--kind', 'hook_ask',
      '--payload', JSON.stringify({ questions: e.questions })], CALL_TIMEOUT_MS)
      .then((r) => ({ who: 'begin', r }), (err) => ({ who: 'begin-error', err }))

    const first = await Promise.race([native, begin])
    if (first.who !== 'begin') {
      // The person answered (or dismissed) before the daemon even replied, or
      // begin failed: the native outcome stands. A row begin did open is told.
      if (first.who === 'begin-error') return settleNative(await native, e, next)
      begin.then((b) => { const o = b.who === 'begin' && b.r.exitCode === 0 && parse(b.r.stdout); if (o && o.id) report($, bin, o.id, nativeOutcome(first.r)) })
      return settleNative(first, e, next)
    }
    const opened = first.r.exitCode === 0 ? parse(first.r.stdout) : null
    if (!opened || !opened.id) {
      // no_responders (13), daemon down (20), unsupported (21), anything else: the dialog runs alone.
      if (first.r.exitCode === 13) $.ui.log('pdx-ask: ' + stderrCode(first.r) + ' — native dialog only')
      return settleNative(await native, e, next)
    }
    const id = opened.id

    let settled = false
    const remote = (async () => {
      for (;;) {
        const r = await pdx($, bin, ['wait', id], WAIT_TIMEOUT_MS)
        if (settled) return { who: 'remote-stopped' }
        if (r.exitCode !== 0) return { who: 'remote-error', r }
        const out = parse(r.stdout)
        if (!out || out.state === 'still_open') continue // another bounded round; the dialog stays up
        return { who: 'remote', out }
      }
    })().catch((err) => ({ who: 'remote-error', err }))

    const w = await Promise.race([native, remote])
    settled = true
    if (w.who === 'native' || w.who === 'native-error') {
      // Step 3 (and step 5: if a remote decide won the CAS meanwhile, the daemon
      // records terminal_override — the terminal's answer still stands).
      report($, bin, id, nativeOutcome(w.who === 'native' ? w.r : null))
      return settleNative(w, e, next)
    }
    if (w.who === 'remote' && w.out.state === 'answered_remote' && w.out.hook && w.out.hook.answers) {
      // Step 4: returning with next(e) pending closes the native dialog (M24 P-B).
      return { result: { questions: e.questions, answers: w.out.hook.answers } }
    }
    // closed another way (abandoned, dismissed by the backstop, …) or the loop failed: the dialog runs on.
    return settleNative(await native, e, next)
  }).catch(($, e, next) => next(e)) // any failure of ours: the engine's own dialog, as without the mod
}

// settleNative returns the native outcome as the hook's answer: the result as
// it came, or — when next(e) rejected — the same rejection replayed.
function settleNative(n, e, next) {
  return n.who === 'native' ? n.r : next(e) // a rejected next(e) replays as it settled (reference.md: "nothing runs twice")
}
```

- [ ] **Step 4: Run the tests and verify they pass.**
  - Run: `claude plugin validate --strict cmd/pdx/plugin/purdex && claude plugin test cmd/pdx/plugin/purdex && go test ./cmd/pdx/plugin/`
  - Expected: `✔ Validation passed` (the report line for `./ask.js` reads `gating hook with .catch: tool.call{tool=AskUserQuestion}` and lists `$.process.run (via pdx), $.fs.read, $.session.id, $.session.surfaces, $.ui.log`); `12 pass 0 fail` for `hooks/ask.test.ts` (9 measured + 2 added at the consistency fix + the `terminal_override` interleaving case of the codex round) plus P5b's own files; `TestFiles_HasTheLayoutClaudeLoads` PASS with the two-module assertion.

- [ ] **Step 5: Commit.**
  ```bash
  git add cmd/pdx/plugin/purdex/hooks/ask.js cmd/pdx/plugin/purdex/hooks/ask.test.ts cmd/pdx/plugin/purdex/hooks/hooks.json cmd/pdx/plugin/embed_test.go
  git commit -m "feat(plugin): 分流 for AskUserQuestion — native dialog raced against pdx ask wait

  Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
  ```

### Task 8a.10: The hours-long hold, measured once (spec §6.6 step 2, §15 "a hold across several pdx ask wait rounds")

Not a code task: one measurement on mlab, its result appended to the spec as **M25** in the P8a-2 PR (one paragraph under §3.2, same style as M24). The unit test above proves the loop; this proves the engine keeps the dialog for hours while a `$.process.run` child is replaced every ≤ 9 min, and whether Claude Code's `afkTimeoutMs` auto-resolve exists interactively.

**Recipe (≈ 2 h 15 min wall, Haiku, no cost beyond two short turns):**
1. Build and install the P8a-2 `pdx` on mlab (the plugin re-extracts on the VERSION change). Keep **one** Purdex.app connected to mlab (so `RemoteResponders.Any()` is true; it shows no card — expected).
2. In tmux: `tmux new -d -s u19hold -x 180 -y 50 'cd /tmp && claude --model claude-haiku-4-5-20251001 --dangerously-skip-permissions --debug-file /private/tmp/claude-501/<scratchpad>/u19hold/debug.log'` (the global plugin loads through `CLAUDE_CODE_PLUGIN_DIRS`, P5b-1; confirm with `/plugins` or the debug log's `purdex` load line before step 3).
3. `tmux send-keys -t u19hold '請用 AskUserQuestion 問我一題：紅還是藍（選項 紅、藍），拿到答案後只回「你的答案是 X」。' Enter` (a prompt over ~60 characters sometimes swallows Enter — M24 "操作坑"; if the composer still holds the text, send `C-m` once more after 1 s). Capture `tmux capture-pane -p -t u19hold > cap-00.txt`: the native dialog (`❯ 1. 紅 … Esc to cancel`).
4. Confirm the row: `curl -s -H "Authorization: Bearer $(pdx token)" http://100.64.0.2:7860/api/team/approvals | jq '.approvals[] | select(.kind=="hook_ask") | {id, state, lease_until, deadline_at}'` → one open row, `deadline_at` 32503680000000, `lease_until` ≈ now + 30 s and moving on every re-read (the poll renews it). Note the id.
5. Sample for 2 h, every 5 min, into one file: `date -u; pgrep -f 'pdx ask wait'; for p in $(pgrep -f 'pdx ask wait'); do ps -o pid=,lstart= -p $p; done` (pids only — never `pgrep -l`, per the host notes). Expected: exactly one `pdx ask wait` process at every sample, its pid (and `lstart`) changing every ≤ 9 min — about 13–14 rounds in 2 h. Every 30 min also `tmux capture-pane -p -t u19hold > cap-<hh>.txt`: the dialog is still drawn, byte for byte as cap-00.
6. After 2 h press `1` in the pane (`tmux send-keys -t u19hold 1`). Expected: the transcript row `⏺ User answered Claude's questions: ⎿ · 紅還是藍？ → 紅`, the model's `你的答案是 紅`, the row `state: answered_local` with `hook.answers`, and `pgrep -f 'pdx ask wait'` empty within a second (the report closed the row and woke the poll).
7. Read the transcript JSONL (`~/.claude/projects/-tmp/<session id>.jsonl`) for the `AskUserQuestion` `tool_result`: record whether `afkTimeoutMs` is present. If the dialog **auto-resolved before step 6** (a transcript row appears on its own, `afkTimeoutMs` set), record the elapsed time: that is Claude Code's bound on the hold, and spec §6.6 step 2's "hours if nobody is there" becomes "until Claude Code's AFK auto-resolve at N minutes".
8. Variant, 15 min: repeat steps 3–4, wait one full round (≥ 9 min, pid changed once), then answer remotely: `curl -s -X POST -H "Authorization: Bearer $(pdx token)" -H 'Content-Type: application/json' http://100.64.0.2:7860/api/team/approvals/<id>/decide -d '{"decision":"approve","hook":{"answers":{"紅還是藍？":"藍"}},"client":{"kind":"app","label":"curl @ mlab"}}'` → the dialog disappears within a second (capture), the transcript shows `→ 藍`, the row is `approved`. Then press `1` anyway: it only types into the composer (M24 P-B).
9. Record in M25: rounds observed, dialog intact at each capture, `budget`/`skipped` lines in `debug.log` (expected none), `afkTimeoutMs` presence, both variants' outcomes. The debug log and captures stay in the scratchpad, as M24's did.

### Task 8a.11: The Codex `trusted_hash` probe (M20 "unmeasured"; preamble P8a-2)

Also a measurement, recorded as **M26**. P2c's installer raises Codex's `PreToolUse` timeout 5 → 10 (`internal/agent/codex/hooks.go:327-335` writes `"timeout": 5` today); Codex keeps a `[hooks.state] trusted_hash` in `~/.codex/config.toml`, and whether a changed `timeout` re-prompts the user once is unknown (M20).

**Recipe (10 min, on mlab, no model calls needed):**
1. `cp ~/.codex/hooks.json /private/tmp/claude-501/<scratchpad>/codexprobe/hooks.json.bak`; `grep -n -A2 '\[hooks.state\]' ~/.codex/config.toml` → note `trusted_hash` (a hash, not a secret).
2. Edit **one** entry's `timeout` in `~/.codex/hooks.json` from `5` to `10` (what P2c's installer will do), nothing else.
3. `tmux new -d -s codexprobe -x 160 -y 48 'codex --model gpt-5.6-sol'`; after 5 s `tmux capture-pane -p -t codexprobe`: record whether a hooks-trust prompt appears (its exact text), and what key accepts it. If it does, accept once, quit (`/quit` or `C-c` twice), re-read `trusted_hash`: changed? Start `codex` again: no prompt the second time?
4. Change the `timeout` back to `5`, start once more: does it prompt again (hash is of the content, not a monotonic version)? Restore `hooks.json` from the backup if the file differs from what P2c will write.
5. Record in M26: prompt or no prompt, its text, whether the hash changed, and therefore whether P2c's installer change costs the user one acknowledgement per Codex host. If it prompts, P2c's installer notes `codex 第一次啟動時會問一次是否信任 hooks（P2c 把 PreToolUse 的 timeout 調成 10 秒）` in its `pdx setup` output — a one-line follow-up on P2c, not on this PR.

- [ ] **Commit (both recipes' results):**
  ```bash
  git add docs/specs/2026-10-06-lead-team-relay-spec.md
  git commit -m "docs(spec): M25 hours-long 分流 hold and M26 Codex trusted_hash, measured

  Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
  ```

---

### Deviations from the preamble

1. **P8a-1 is four PRs, not one** (`## Size estimate`): 1a wire/store/responders/SPA, 1b routes, 1c CLI + hook forward, 1d terminal-only degradation. Each is green alone; a daemon with only 1a/1b deployed answers `/api/ask/*` but no mod calls it yet, and the SPA already tolerates the kinds.
2. **`hook_ask` / `hook_permission` additions live in `internal/team/wire_ask.go`**, not appended to `wire.go` (which P2c and P5a are both editing in parallel); `wire.go` gets only the two `Hook` fields. Same JSON names as the preamble.
3. **`Approval.State` after a remote answer is `approved`**, not a new `answered_remote` state: the preamble lists `answered_remote` only as a **wait-route** state. `askWaitOf` maps `approved` (hook kind) → `{state: "answered_remote", hook}`; the row and its `closed` event say `approved` with `hook` set. A `hook_permission` deny is `denied` with `hook.behavior = "deny"`.
4. **`HookDecision` is stored in `grant_json`** (no new column, no migration): `scanRow` decodes it as `Hook` for hook kinds and as `Grant` otherwise — literally "rides in Grant's place". A lead row never carries `hook`; a hook row never carries `grant` (pinned by `TestHookWire_JSONNames` and `TestStore_HookDecisionRidesInGrantsPlace`).
5. **Hook rows have no deadline and the terminal-only ones no lease**: `DeadlineAt = NoExpiryAt` (3000-01-01 in ms, below 2^53), `LeaseUntil = NoExpiryAt` for terminal_only. The preamble did not say; spec §6.6 "no wait of its own" and "the row lives exactly as long as the native dialog" require it, and a sentinel keeps the shipped sweeper and `ExtendOpenLeases` untouched (verified: the shipped UPDATE is guarded by `lease_until < ?`, `store.go:239-242`, so a `NoExpiryAt` lease is never lowered at boot).
6. **The terminal-only `PostToolUse` close is `answered_local` with CC's `tool_response.answers` when present, `dismissed` otherwise** — the preamble says "closes it as dismissed". The answers are in the hook's stdin (d.ts `PostToolUseHookInput.tool_response`); closing a row the person did answer as "dismissed" would hide 「已在終端機回答：紅」 from the phone for exactly the sessions that need the backstop. Both states exist in the contract; nothing else changes.
7. **Five more events reach `/api/hooks/decide`, flag-gated** (`PostToolUse`, `PostToolUseFailure`, `Stop`, `UserPromptSubmit`, `SessionEnd`) and the daemon writes a second flag dir `hookasks/` — see `Contract problems` 1. Without them a terminal-only row dismissed with Esc (no `PostToolUse` fires) would linger until the session ends, and a `hook_permission` row (no `tool_use_id`, `Contract problems` 3) could never close.
8. **`pdx ask begin` adopts a `409 ask_open`** (prints the open row's id, exit 0) **and is sent with `Idempotent()`** (codex round): a lost response is replayed inside the grace and the daemon's one-open-row-per-`(session, tool_use_id)` rule turns the replay into `ask_open` → the same id. `ask_open` is a success for the CLI and the mod, not an exit 13 (spec §14 says so). The preamble listed `ask_open` as a plain 409.
9. **`pdx ask report` takes `--hook <json>` as well as `--hook-file <f>`; `begin` takes `--payload <json>` as well as `--payload-file <f>`.** The mod passes inline JSON on argv (`$.process.run` is argv, no shell; a 4-question payload is a few KB, far under `ARG_MAX`), so it needs no temp file and no cleanup. Both spellings are tested.
10. **The mod issues `next(e)` before `pdx ask begin`** (Task 8a.9) so the dialog never waits on the daemon; spec step 1's "then the mod simply returns next(e)" is satisfied — `next(e)` is called exactly once and its result returned — but the daemon call is not on the dialog's critical path. The test `no_responders ⇒ … next(e) once` pins the count.
11. **Mod tests are `hooks/ask.test.ts`** (`.test.js` is not collected — `Measured here`).
12. **A hook row raised by the mod keeps the 30 s lease** renewed by the wait polls, so a mod that dies with the dialog up is abandoned within 30 s by the shipped sweeper (and the mod's loop, if any, ends with `closed`). The spec only names the liveness check (step 7); the lease is the cheaper signal and already exists.
13. **`GET /api/team/inflight` excludes hook kinds** (`ListOpenNonHook`): a restart does not interrupt them (restart-aware poller, boot lease grace), and the App's restart confirm would otherwise count an open AskUserQuestion as a 申請. `GET /api/team/approvals?state=open` still lists them (the phone reads it); the SPA's unused `listOpenApprovals` would need the same `isHookKind` filter if it is ever called from UI code.
14. **`decided_by` for a terminal close is `{kind:"terminal", label:"terminal"}`**; the spec says `{kind: "terminal"}` and the shipped `handleDecide` requires a non-empty label for app clients — one word keeps the audit line readable.
15. **`decide` on a terminal_only row answers `409 terminal_only` with `detail` 「這題只能在終端機回答」** — the spec's card text, carried once by the daemon so a phone that tries anyway can show it.

### Size estimate

Measured in the scratch build (`wc -l` for new files, `diff | grep -c '^[<>]'` for edits):

| PR | File | Lines |
|---|---|---|
| **P8a-1a** | `internal/team/wire_ask.go` ≈ 104 (no `ErrUnknownSession` / `ErrBadTransition`: P5a-1a's) · `wire_ask_test.go` 61 · `wire.go` +8 | ≈ 173 |
| | `internal/module/team/store_ask.go` 107 · `store_ask_test.go` 155 · `store.go` +24 | 286 |
| | `internal/module/team/responders.go` ≈ 30 · `responders_test.go` ≈ 35 · `module.go` +9 (without the routes hunk; no interface / `modSeen` hunks — P5a-2a's) | ≈ 74 |
| | `spa/src/lib/team/types.ts` +20 · `approval-ws.ts` +15 · `approval-ws.hooks.test.ts` 75 | 110 |
| | **total** | **≈ 643, 12 files** (the peers `origin_resolver_session*.go` pair and the `handler_test.go` hunk moved to P5a-1b / P5a-2a) |
| **P8a-1b** | `ask_handler.go` 366 · `ask_handler_test.go` 324 · `handler.go` ±54 · `module.go` +3 | **≈ 747, 4 files** |
| **P8a-1c** | `cmd/pdx/ask.go` 289 · `ask_test.go` 183 · `main.go` +2 · `hook_ask.go` 96 · `hook_ask_test.go` 110 · `hook.go` ≈ +6 | **≈ 686, 6 files** |
| **P8a-1d** | `hook_observe.go` 204 · `hook_observe_test.go` ≈ 140 (incl. the route-level `Stop` test) · P2c's `hooks.go` +6 and `hooks_test.go` ≈ +40 (no P5a hello edit: presence is read from `modSeen`) | **≈ 390, 4 files** |
| **P8a-2** | `cmd/pdx/plugin/purdex/hooks/ask.js` ≈ 100 (`agentId` guard, `pdx.json` path) · `ask.test.ts` ≈ 230 (incl. the `terminal_override` interleaving case) · `hooks.json` +1 · `cmd/pdx/plugin/embed_test.go` ±3 · spec M25/M26 ≈ +30 | **≈ 364, 5 files** |

All five are under 800 lines and 20 files. The seams are real: 1a has no behaviour change for a running daemon beyond accepting `hook` on the wire; 1b adds routes nothing calls; 1c adds a CLI nothing calls and a hook forward the daemon answers `{}` to (P8a-1d is what makes it open rows); 1d is the degradation; P8a-2 is the only user-visible change and needs the whole line.

### Open questions for the coordinator

1. **Degradation transport (Contract problems 1).** The plan forwards two ungated and five flag-gated hook events through `/api/hooks/decide`, as the preamble's route says. The alternative is an `OnHookEvent` listener in the agent module (`internal/module/agent/handler.go:165`, which already receives every hook's raw stdin via `/api/agent/event`, M17): zero new hook calls, but a cross-module hook into a 1 300-line handler. If you prefer that, Task 8a.7 disappears and Task 8a.8's `observeHookEvent` is called from the listener with the same `HookDecideRequest` shape; the daemon-side tests are unchanged.
2. **Mod presence after `/clear`.** `observeHookEvent` trusts the relay `hello` record (P5a-2a's `modSeen`) keyed by session id. P5b-1's `hello` must run on **every** `session.start`, including `classic.SessionStart{source:'clear'}` (M1: a clear gives a new session id) and on resume; otherwise the first AskUserQuestion after a self relay opens a terminal-only row that the mod's `begin` then takes over (two extra events, a flicker on the phone — correct but noisy). **Decided:** P5b-1's hello runs there (Coordinator decisions, P8a: "hello is sent on every `session.start` and after each `/clear`").
3. **`ResolveOriginBySession` ownership.** **Decided:** P5a-1b defines it (peers side), P5a-2a adds the interface line and the fake; P8a consumes it (Task 8a.3 is written to that).
4. **The answered-locally terminal-only close (Deviation 6):** keep `answered_local`-with-answers, or the preamble's plain `dismissed`? One `if` either way. **Decided:** `answered_local` with CC's answers when present, else `dismissed`.
5. **Subagent questions.** `tool.call` also fires for a subagent's `AskUserQuestion` (`e.agentId` set, d.ts `ToolCallInput`). **Decided:** subagents are not relayed in v1 — `ask.js` starts with `if (e.agentId) return next(e)` and `ask.test.ts` pins it (Task 8a.9).

### Mutation gates

Each was applied in the scratch build, the named test went red, the mutation was reverted and the suite went green again:

1. **CAS on a hook row** — in `store.go` `closeWhere`, drop `AND state = 'open'` → `TestStore_HookRowCloseExactlyOneWinner` (`3 winners, want exactly 1`), `TestAsk_DecideAndReportRaceExactlyOneWins` (`2 closed events, want exactly 1 (codes [200 200])`) and the shipped `TestStore_CloseIfOpenExactlyOneWinner` all red.
2. **`no_responders` when `HasSubscribers()` is false** — in `responders.go` invert `wsResponders.Any` → `TestAskBegin_NoRespondersOpensNothing` red (`begin without subscribers = 201`), and `TestObserve_ModPresentOrNoRespondersOpensNothing_StopDismissesAll` red (a row opens with nobody connected). Measured: both red with the inverted `Any`.
3. **Mod: `no_responders` ⇒ plain `next(e)` and no further daemon call** — ignore `begin`'s exit code (`const opened = parse(first.r.stdout) || { id: 'x' }`) → `no_responders ⇒ …` and `begin that fails (daemon down) …` red (`calls.map(sub)` gains `wait`); measured 2 of 9 fail. (Both tests answer the native dialog only after `begin` has settled; with an immediate native answer the early branch runs and the mutant survives — that is why the tests are written with the 50 ms delay.)
4. **Mod: terminal first ⇒ native result returned unchanged and `answered_local` reported** — (a) return `{ result: { questions, answers } }` built from the native answers instead of the native object → `terminal first …`, `a hold across several wait rounds …` and `Esc …` red (`ref`/`isReadOnly`/`isError` missing); measured 3 of 9 fail. (b) drop the `report` call on that branch → the same three red on `await reported` (5 s timeout); measured 3 of 9 fail.
5. **Mod: `still_open` loops without returning** — replace `continue` with a return → `still_open loops …` red (`expected 12, received 1`) and `a hold across several wait rounds …` red; measured 2 of 9 fail.
6. **Terminal-only backstop (P8a-1d)** — make `observeHookEvent` ignore `modPresent` → `TestObserve_ModPresentOrNoRespondersOpensNothing_StopDismissesAll` red (`mod present: rows = […]`) (measured). The hook side is pinned, not mutated: `TestAskForward_UngatedOnlyForAskAndPermission` lists `PostToolUse` without the flag as `false` (U17's cost rule).
7. **Override only from `approved`** — drop `AND state = 'approved'` in `OverrideIfApproved` → `TestStore_OverrideIfApprovedOnlyFromApproved` red (`an open row cannot be overridden: won=true`).
8. **Mod: the terminal's answer stands when the remote one lands after it** (`terminal_override` interleaving, codex round; not yet applied in the scratch build) — after `Promise.race` consult `remote` again (`const late = await remote; if (late.who === 'remote' && late.out.state === 'answered_remote') return { result: { questions: e.questions, answers: late.out.hook.answers } }`), i.e. prefer a remote answer that arrived after the native one → `interleaving: answered_remote arrives after the terminal already answered …` red (`answers` is 藍, and `reported` never resolves within the test). Also: drop `settled = true` → the same test stays green (the late answer is ignored by the race either way) — the guard exists to stop the loop's next `wait`, pinned by no test; noted, not a gate.

---

## Coordinator decisions (plan v2, 2026-10-07, `mlab/_81nu3d`)

Answers to the sections' open questions and contract problems. They bind the implementers; where they amend the spec, the spec commit is named.

**P2c**
- Spec §12's P2c row said `timeout: 600` on two events; §6.6 (rewritten for U19) says only Codex's `PreToolUse` goes 5 → 10 s for the lock path and Claude Code's entries stay untouched. **§6.6 wins**; §12's row is reworded in the same commit as this plan.
- Existing Codex installs keep `timeout: 5` until `pdx setup --agent codex` runs again; `CheckHooks` does not flag it. Accepted; the P2c CHANGELOG entry tells the operator to re-run setup.
- One daemon log line per denied call: accepted (it is the audit trail §6.5 asks for).
- `permission_mode` travels inside `raw`, not top-level: accepted.
- The decide handler removing a flag when it answers `{}` (a SIGKILLed `pdx lead request` self-heals): accepted. **P6 must check the relay lock before that removal**, as the section notes.
- A stdin over 64 KiB sends ids only: accepted.

**P5a**
- `pdx relay wait` reaching its 9 min bound with the request still open: **exit 0 with `{state:"open"}` on stdout**, not a new exit code 3 (spec §14 has no 3; P5b's loop already assumes `state:"open"`). P5a-2c changes accordingly.
- The dialog's 「這個 session 不再詢問」 is a checkbox applied with whichever button is pressed (approve or deny): accepted.
- `409 relay_open` / `bad_transition` carry the op and map to exit 13: accepted.
- ~~Seven PRs (1a, 1b, 2a, 2b, 2c, 3a, 3b)~~ **Eight PRs after the codex round: P5a-0 (wire, Task 5a.1) → 1a → 1b (now with Task 5a.6 hostconfig `relay`) → 2a → 2b → 2c → 3a → 3b**; every one ≤ 800 lines; merge order as listed.
- The title move is a separate idempotent meta.db transaction (different database from `team.db`): accepted, with the boot reconciliation covering a crash between the two.
- `ResolveOriginBySession` is added in **P5a-1b**; P8a consumes it.
- Team/member moves at `cleared`, persisted usage on team rows, `relayRole` beyond `"none"`: P4. `pdx relay <ref>`, `claim`, `requested` reconciliation, the restart-confirm relay line: P6.

**P5b**
- `pdx relay wait` end-of-bound shape: see P5a (exit 0, `state:"open"`).
- A `409` code is printed as the **last stderr token** by every `pdx relay *` subcommand so the mod can parse it: P5a-2c implements, P5b-2 relies on it.
- `<data_dir>/relay/` is created by the daemon at team module `Start` and again in `begin` (`MkdirAll`); the mod never creates it.
- A manual `/compact` is treated like an auto one for an **open** request (the request becomes `cancelled{compacted}`); an approved-not-written relay is not skipped on a manual compact (the person asked for it). Spec §8.7 (c) gains one sentence in this plan's spec commit.
- `claude plugin test` collects only `*.test.ts` (measured by two writers): all mod tests are `.test.ts`; `register.js` stays plain JS.
- Own-turn recognition uses the `turn.start` text nonce (`[pdx-relay op=<id>]`) because a plugin's own `prompt.submit` hook does not see its own `$.prompt.submit`: accepted (this is the fallback spec §8.7 allowed for).
- `PDX_RELAY_THRESHOLD` via `$.env.get` for acceptance: accepted.

**P8a**
- **Degradation transport:** the settings hook forwards two ungated events (`PreToolUse` for `AskUserQuestion`, and `PermissionRequest`) to `/api/hooks/decide` with a 2 s bound and discards the answer, plus the flag-gated closing events; **spec §6.6's sentence "the settings hooks keep their fire-and-forget shape; only the flag-gated lock path waits" is amended** in this plan's spec commit to name these two bounded forwards.
- Route names: the preamble's `/api/ask/begin|wait|report` stand; the spec's step wording is aligned in the same commit.
- `hello` is sent on every `session.start` and after each `/clear` (spec §8.3), so mod presence per session id is current.
- The `PostToolUse` backstop closes a terminal-only row as `answered_local` **with CC's answers when present**, else `dismissed`: accepted.
- Subagent (`agentId` present) `AskUserQuestion` calls are not relayed in v1: the mod returns `next(e)` for them.
- Hook rows have no deadline (`DeadlineAt = NoExpiryAt`), mod-raised rows keep the 30 s lease renewed by the wait polls, terminal-only rows have no lease and close on the `PostToolUse` report or when the session is gone: accepted.
- `PermissionRequest` carries no `tool_use_id`: the row is keyed by session + tool + input hash for that kind: accepted as the section wrote it.
- Five PRs (1a, 1b, 1c, 1d, 2): accepted.

**Plan v3** (after P8a-2 merges): P4, P4b, P4c, P6, P7 — one codex round with the spec, as before.

**Fix notes (consistency pass, 2026-10-07 — where the sections had to be reconciled with the decisions above, and the choices made where the decision left room):**
- `ErrUnknownSession` / `ErrBadTransition` are declared once, in P5a-1a's `wire_relay.go`; P8a-1a's `wire_ask.go` uses them. P8a-1a therefore needs P5a-1a merged (it did anyway by the global order).
- `pdx relay wait` prints `{"state":"open"}` at exit 0 **always** when its bound runs out — a bare `{id, kind, state:"open"}` when no poll has answered yet — so the mod never sees an empty stdout at exit 0; the mod treats any exit-0 body that is not `state:"approved"` as not approved.
- Stderr shape for every `pdx relay *` / `pdx ask *` API error: `pdx relay: <detail> <code>` / `pdx ask: <detail> <code>`, code last; the fixed strings (`daemon_unavailable`, `unsupported`) follow the same shape.
- P2c's decide route answers `200 {}` for any non-blank event other than `PreToolUse` / `PermissionRequest`, with the lead-lock flag untouched (400 only for a blank `event` / `session_id`, a bad agent or a malformed body); P8a-1d's `observeHookEvent` is called from the one `answerEmptyDecision` helper on every `{}` path.
- A report that moves an op to a terminal state while its approval row is open closes the row as `cancelled` via `closeAs` (`closeRequestOfReportedOp`); `afterClose` is a no-op on it because `ReportRelay` is idempotent per state. `handleRelayReport` also gained the `503 not_ready` guard the other write routes have.
- Mod presence has one owner: P5a-2a's `m.modSeen map[string]helloInfo` (renamed from `m.hello`), written by the relay `hello` handler under `m.mu`; P8a-1a adds only the reader `modPresent`. `ResolveOriginBySession`: P5a-1b (peers) + P5a-2a (interface, fake); P8a consumes. P8a-1a's size drops to ≈ 643 lines / 12 files.
- `<data_dir>/relay/`: `MkdirAll` in team `Start` (logged, not fatal) and in `begin`; `TestStart_MakesTheRelayDir` pins both.
- Manual `/compact`: cancels an open request (as auto), never skips an approved-not-written relay (only `trigger:auto` skips); two mod tests added (unmeasured, said so).
- `ask.js`: `if (e.agentId) return next(e)` first; `pdx` is run by the path in `pdx.json` (fallback `pdx`); paths under `cmd/pdx/plugin/purdex/`; P8a-2 updates `embed_test.go`'s two-module assertion.
- ~~`effort` on a self-relay payload stays empty in these PRs~~ **Codex round: the daemon fills `model_id` and `effort` in `begin`** from the agent module's per-session statusline reading (`agent.ContextUsageReader`, P1), the mod passes neither, `pdx relay begin` has no `--model` / `--effort`; no follow-up issue. The acceptance recipe (P5b, step 5) asserts both present and equal before and after the relay.
- `PermissionRequest` rows (no `tool_use_id`) are de-duplicated by session + `tool_name` + sha256(compacted `tool_input`) in `openTerminalOnly`, as the decision text says; a test pins it.
- `{ skip }` mid-turn (spec §8.7 (c)) is measured in P5b-3's acceptance step 7 (d) and recorded as M27; the fallback (compaction runs, write prompt at the next `turn.complete`) is named there.
- Boot reconciliation **from frames** (past-`claimed` self ops) is P6, named in P5a's deferred list and in Review Focus 3; P5a-2b's `reconcileRelays` covers `awaiting_approval` and `cleared`.
- Every P5a-2a / P5a-2b / P8a task whose hunks touch `module.go`, `handler.go`, `store.go`, `types.ts`, `main.go` or `hook.go` now says its `:line`s are alpha.527's and names the preceding PR to re-verify against.
