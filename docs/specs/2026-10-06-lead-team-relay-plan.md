# Lead / member / team and context relay — Implementation Plan (v1)

> **DRAFT — paused 2026-10-07 by the user.** Do not execute.
> - **Written:** the header, PR P0 and PR P1.
> - **Not written yet:** PR P2a, P2b and P3 (the dialog). Nothing has been through codex review.
> - **Before resuming:** a large development round was under way on main when this paused. Re-verify every `file:line` here against the then-current main, and rebase this branch first.

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Lay the foundation of the lead/team and relay spec:
- context usage and correct cwd in `pdx peers`;
- the approval-request model with restart-proof waiting;
- `pdx lead request`;
- the approval dialog on every Purdex.app.

**Architecture:**
- **New daemon module.** `internal/module/team` owns `team.db` and the `approval_requests` table: one state machine, closed by compare-and-set. It broadcasts `approval.request` host events, and snapshots open requests to each new subscriber.
- **Agent module.** It learns each CC session's context usage from the statusline payload, keyed by CC session id.
- **Peers module.** It shows that usage, and exports an origin resolver the team module uses to attribute callers.
- **CLI.** A shared restart-aware client (`cmd/pdx/daemonclient`) carries every new CLI call.
- **SPA.** A global `ApprovalDialogHost` mirrors open requests per host.

**Tech Stack:** Go (net/http, modernc.org/sqlite, plain `testing`), React 19 + Zustand 5 + Vitest + Testing Library, the custom `useI18nStore`.

**Spec:** `docs/specs/2026-10-06-lead-team-relay-spec.md` (passed review 2026-10-06). Read §2 (U1–U14), §6 and §9 before any task.

**Scope of v1:**
- PRs **P0, P1, P2a, P2b, P3**, in that order.
- The rest gets **plan v2**, with one codex round, after P3 merges:
  - P5a/P5b, self relay — next per spec §12's suggested order;
  - P4, team and spawn;
  - P6, member relay;
  - P7, notices.

## Global Constraints

- **PR size.** One PR ≤ 800 lines of diff or ≤ 20 files. Every task is its own commit, test first (TDD).
- **Tests and builds:**
  - Go: `cd <worktree> && go test ./<pkg>/...`
  - SPA: `cd <worktree>/spa && npx vitest run <path>`, `pnpm run lint`, `pnpm run build`
  - pnpm, never npm.
- **Commits.** Every commit ends with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`. Parallel subagents in one worktree commit with `git commit --only <files>`.
- **Copy.** User-visible strings are Traditional Chinese, with an English twin. SPA strings go in both `spa/src/locales/en.json` and `spa/src/locales/zh-TW.json`; `locale-completeness.test.ts` enforces identical key sets. Daemon and CLI messages are written as the spec quotes them.
- **Exit codes** (spec §14): 0 ok, 1 runtime/API error, 2 usage error, 10 denied, 11 timed out, 12 cancelled or abandoned, 13 refused by team rules, 14 member did not start or respond, 20 daemon unreachable through the 30 s grace, 21 daemon does not support the route (404).
- **Restart grace** (spec §9.1):
  - backoff 0.25 s → 1 s;
  - grace 30 s;
  - one stderr line `daemon 重啟中，繼續等待…`;
  - deadlines are absolute.
- **Lead-request bounds** (spec §6.1–§6.2): `--wait` default 9 min, cap 10 min; lease 30 s, renewed by each poll; `--max-members` default 3, cap 8.
- **Approval.** U5b (U5a withdrawn): approve and deny are one click on any Purdex.app, for both kinds, with no signature. The layer that stays: no `pdx` approve command, the skill forbids self-approval, and every decision is broadcast and audited (spec §6.5).
- **U14.** Purdex.app is the only client. Add no browser-only path; the existing browser `Notification` fallback is not extended to approvals.
- **Secrets.** Never print a token. Tests use literal fake tokens.

## Review Focus

1. **A statusline payload without `session_id`, or with `used_percentage: null`** (every session's first refresh, measured 2026-10-06): no context is recorded, `pdx peers` shows `-`, and nothing panics. → Task 1.1, Task 1.3.
2. **Two CC panes in one tmux session:** each row shows its own session's usage, not the last writer's. → Task 1.1, Task 1.2.
3. **The daemon restarts while `pdx lead request` is long-polling:** the CLI prints the restart line once, re-polls the **same** request id after the new `boot_id` answers, and the request is still open, with its lease extended at boot. → Task 2.4, Task 2.6, Task 2.7.
4. **A decide racing the deadline sweeper and a cancel:** exactly one close wins. The loser gets `409 already_decided`, carrying the winner's `decided_by`, and one `closed` event is broadcast. → Task 2.2, Task 2.4.
5. **A Purdex.app whose host socket drops while the dialog is open:**
   - the dialog stays, with its buttons disabled;
   - on reconnect, the snapshot neither duplicates the request nor revives one that closed meanwhile;
   - a deny clicked while disconnected is sent once on reconnect.

   → Task 3.2, Task 3.3.

---

## PR P0 — PRODUCT.md vocabulary (spec §4)

### Task 0.1: Rename operator → lead; Role becomes (none) / lead / member

**Files:**
- Modify: `PRODUCT.md:15` (§1), `PRODUCT.md:75-76` (§3.5 Role rows), `PRODUCT.md:148-149` (Law 2 examples), `PRODUCT.md:203-209` (§6.1, §6.2)

**Interfaces:** none (documentation).

- [ ] **Step 1: Edit §1, line 15.** Replace:
  ```
  - 任意 agent 皆可被賦予 **operator 角色**，協助跨 agent 的雜項管理與指揮（長期方向）。
  ```
  with:
  ```
  - 任意 agent 經使用者核准可成為 **lead**，帶領自己開出的 **member** 組成 **team**，協助跨 agent 的分工與指揮。
  ```
- [ ] **Step 2: Edit §3.5, lines 75-76.** Replace the two Role rows with:
  ```
  | **Role** | （無） | 預設：一般 session，執行使用者下達的工作 |
  |  | `lead` | 經使用者核准（需人在場驗證）；可開 member、替 member 安排接力 |
  |  | `member` | 由 lead 開出；接力由 lead 決定 |
  ```
  Then add this line after the table, before "**Mode × Role 正交**":
  ```
  `worker` 是 Nexen 無頭執行的用詞，不作為 Role。
  ```
- [ ] **Step 3: Edit Law 2, lines 148-149.**
  - `為 Operator Agent role 在 agent metadata schema 留 capability / role 欄位` becomes `為 lead / member role 在 agent metadata schema 留 capability / role 欄位`.
  - `為 Operator Agent 提前建 Voice Indicator / Mode Switcher / Activity Log surface` becomes `為 lead 提前建 Voice Indicator / Mode Switcher / Activity Log surface`.
- [ ] **Step 4: Edit §6.1 and §6.2, lines 203-209.** Replace the §6.1 heading and body with:
  ```
  ### 6.1 Lead / team

  任何 agent 經使用者核准可成為 lead，帶領自己開出的 member 組成 team；控制 member 走 daemon 層的 message inject 與 pdx team 指令（spawn / relay / kill）。第一版見 `docs/specs/2026-10-06-lead-team-relay-spec.md`。
  ```
  In §6.2, `與 Operator role 配套；使用者可語音指揮 operator、operator 操作其他 agent` becomes `與 lead 配套；使用者可語音指揮 lead、lead 操作其他 agent`.
- [ ] **Step 5: Verify that no `operator` / `Operator` remains** in PRODUCT.md, except inside the word "operator" in unrelated contexts. There should be none: `grep -n -i operator PRODUCT.md` prints nothing.
- [ ] **Step 6: Commit.**
  ```bash
  git add PRODUCT.md
  git commit -m "docs(product): vocabulary lead / member / team replaces operator

  Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
  ```

---

## PR P1 — Context usage and cwd in peers (spec §8.5 first bullet, §8.6, M3, M4)

**Measured payload** (2026-10-06, Claude Code 2.1.291). The statusline stdin has these top-level keys:

`context_window, cost, cwd, exceeds_200k_tokens, fast_mode, model, output_style, scratchpad_dir, session_id, thinking, transcript_path, version, workspace`

The first refresh has:

`"context_window": {"total_input_tokens":0,"total_output_tokens":0,"context_window_size":200000,"current_usage":null,"used_percentage":null,"remaining_percentage":null}`

### Task 1.1: Agent module records usage per CC session id

**Files:**
- Create: `internal/module/agent/context_usage.go`
- Modify: `internal/module/agent/module.go:57-61` (new field next to `statusSnapshots`), `internal/module/agent/module.go:125` (init), `internal/module/agent/handler.go:1278-1281` (record after caching the snapshot)
- Test: `internal/module/agent/context_usage_test.go`

**Interfaces:**
- Produces:
  ```go
  // ContextUsage is the last context-window reading a CC session's statusline reported.
  type ContextUsage struct {
  	UsedPercentage *float64 // nil until CC reports one (null early in a session)
  	WindowSize     int
  	At             int64 // unix ms when the daemon received it
  }
  // ContextUsageReader is an optional interface on *Module; peers type-asserts it on its OwnerResolver.
  type ContextUsageReader interface {
  	ContextUsage(sessionID string) (ContextUsage, bool)
  }
  func (m *Module) ContextUsage(sessionID string) (ContextUsage, bool)
  ```
- Consumes: the `raw_status` bytes already in `handleAgentStatus`.

- [ ] **Step 1: Write the failing tests.**

```go
package agent

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/module/session"
	"github.com/wake/purdex/internal/tmux"
)

func postStatus(t *testing.T, m *Module, tmuxName, raw string) {
	t.Helper()
	body := `{"tmux_session":"` + tmuxName + `","agent_type":"cc","raw_status":` + raw + `}`
	m.handleAgentStatus(httptest.NewRecorder(), httptest.NewRequest("POST", "/api/agent/status", strings.NewReader(body)))
}

func usageModule(t *testing.T) *Module {
	m := newTestModule(t)
	m.sessions = &fakeSessionProvider{sessions: []session.SessionInfo{{Name: "sess1", Code: "code-1"}}}
	m.core = &core.Core{Events: core.NewEventsBroadcaster(), Tmux: tmux.NewFakeExecutor()}
	return m
}

func TestContextUsage_RecordedPerSessionID(t *testing.T) {
	m := usageModule(t)
	postStatus(t, m, "sess1", `{"session_id":"A","context_window":{"used_percentage":72.4,"context_window_size":1000000}}`)
	postStatus(t, m, "sess1", `{"session_id":"B","context_window":{"used_percentage":10,"context_window_size":200000}}`)
	a, ok := m.ContextUsage("A")
	if !ok || a.UsedPercentage == nil || *a.UsedPercentage != 72.4 || a.WindowSize != 1000000 {
		t.Fatalf("A = %+v ok=%v", a, ok)
	}
	b, ok := m.ContextUsage("B")
	if !ok || *b.UsedPercentage != 10 || b.WindowSize != 200000 {
		t.Fatalf("B = %+v ok=%v (two panes in one tmux session must not overwrite each other)", b, ok)
	}
}

func TestContextUsage_NullPercentageAndMissingSessionID(t *testing.T) {
	m := usageModule(t)
	postStatus(t, m, "sess1", `{"session_id":"A","context_window":{"used_percentage":null,"context_window_size":200000}}`)
	a, ok := m.ContextUsage("A")
	if !ok || a.UsedPercentage != nil || a.WindowSize != 200000 {
		t.Fatalf("null percentage: got %+v ok=%v", a, ok)
	}
	postStatus(t, m, "sess1", `{"context_window":{"used_percentage":50}}`)
	postStatus(t, m, "sess1", `"not an object"`)
	if _, ok := m.ContextUsage(""); ok {
		t.Fatal("a payload without session_id must not be recorded")
	}
}

func TestContextUsage_RecordedEvenForUnknownTmuxSession(t *testing.T) {
	m := usageModule(t)
	postStatus(t, m, "no-such-tmux", `{"session_id":"Z","context_window":{"used_percentage":5,"context_window_size":200000}}`)
	if _, ok := m.ContextUsage("Z"); !ok {
		t.Fatal("usage is keyed by CC session id and must not depend on resolving the tmux name")
	}
}

func TestContextUsage_BoundedMapEvictsOldest(t *testing.T) {
	m := usageModule(t)
	for i := 0; i < contextUsageCap+1; i++ {
		postStatus(t, m, "sess1", fmt.Sprintf(`{"session_id":"s%d","context_window":{"used_percentage":1,"context_window_size":1}}`, i))
	}
	if _, ok := m.ContextUsage("s0"); ok {
		t.Fatal("the oldest entry must be evicted past the cap")
	}
	if _, ok := m.ContextUsage(fmt.Sprintf("s%d", contextUsageCap)); !ok {
		t.Fatal("the newest entry must be kept")
	}
}

var _ ContextUsageReader = (*Module)(nil)
```

- [ ] **Step 2: Run the tests and verify they fail.**
  - Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/lead-team && go test ./internal/module/agent/ -run TestContextUsage -v`
  - Expected: FAIL to compile, with `m.ContextUsage undefined`, `contextUsageCap undefined` and `undefined: ContextUsageReader`.

- [ ] **Step 3: Implement `context_usage.go`.**

```go
package agent

import (
	"encoding/json"
	"time"
)

// contextUsageCap bounds the per-session usage map; the oldest reading is
// evicted first. Sessions come and go and nothing else prunes this map.
const contextUsageCap = 512

// ContextUsage is the last context-window reading a CC session's statusline
// reported. UsedPercentage is nil until CC reports one: it is null early in
// a session (measured on Claude Code 2.1.291).
type ContextUsage struct {
	UsedPercentage *float64
	WindowSize     int
	At             int64 // unix ms when the daemon received it
}

// ContextUsageReader is implemented by *Module. Peers type-asserts it on the
// OwnerResolver it already holds, so no new registry key is needed.
type ContextUsageReader interface {
	ContextUsage(sessionID string) (ContextUsage, bool)
}

type statuslineUsage struct {
	SessionID     string `json:"session_id"`
	ContextWindow *struct {
		UsedPercentage    *float64 `json:"used_percentage"`
		ContextWindowSize int      `json:"context_window_size"`
	} `json:"context_window"`
}

// recordContextUsage parses the CC statusline payload and keeps the reading
// keyed by CC session id. Malformed or id-less payloads are ignored.
func (m *Module) recordContextUsage(raw json.RawMessage) {
	var p statuslineUsage
	if err := json.Unmarshal(raw, &p); err != nil || p.SessionID == "" || p.ContextWindow == nil {
		return
	}
	u := ContextUsage{UsedPercentage: p.ContextWindow.UsedPercentage, WindowSize: p.ContextWindow.ContextWindowSize, At: time.Now().UnixMilli()}
	m.snapshotMu.Lock()
	defer m.snapshotMu.Unlock()
	if _, exists := m.contextUsage[p.SessionID]; !exists && len(m.contextUsage) >= contextUsageCap {
		oldestID, oldestAt := "", int64(0)
		for id, v := range m.contextUsage {
			if oldestID == "" || v.At < oldestAt {
				oldestID, oldestAt = id, v.At
			}
		}
		delete(m.contextUsage, oldestID)
	}
	m.contextUsage[p.SessionID] = u
}

// ContextUsage returns the last reading for a CC session id.
func (m *Module) ContextUsage(sessionID string) (ContextUsage, bool) {
	if sessionID == "" {
		return ContextUsage{}, false
	}
	m.snapshotMu.RLock()
	defer m.snapshotMu.RUnlock()
	u, ok := m.contextUsage[sessionID]
	return u, ok
}
```

**Eviction ties.** Readings in one test can share a millisecond, so the oldest-`At` eviction needs an insertion counter as the tiebreak:
- Add `seq int64` to `ContextUsage` (unexported, not part of the interface contract), plus a `usageSeq int64` field on `Module`.
- Evict by the smallest `(At, seq)`.

`TestContextUsage_BoundedMapEvictsOldest` fails without this, because all 513 posts land in the same millisecond.

**Wiring:**
- In `module.go`, next to `statusSnapshots map[string]statusSnapshot`, add `contextUsage map[string]ContextUsage` and `usageSeq int64`.
- At line 125, add `contextUsage: make(map[string]ContextUsage),`.
- In `handler.go`, call `m.recordContextUsage(payload.RawStatus)` **before** the `code := m.resolveSessionCode(...)` early return, so an unresolved tmux name still records usage. The test-nonce branch at 1262-1271 returns before this point, and must keep doing so.

- [ ] **Step 4: Run the tests and verify they pass.**
  - Run: `go test ./internal/module/agent/ -run 'TestContextUsage|TestHandleAgentStatus' -v`
  - Expected: PASS, including the existing `TestHandleAgentStatus_*` tests.

- [ ] **Step 5: Commit.**
  ```bash
  git add internal/module/agent/context_usage.go internal/module/agent/context_usage_test.go internal/module/agent/module.go internal/module/agent/handler.go
  git commit -m "feat(daemon): record statusline context usage per CC session id

  Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
  ```

### Task 1.2: Peer rows carry context usage and the session's own cwd

**Files:**
- Modify: `internal/peers/record.go:19-28` (`AgentInfo`), `:93-117` (`BuildInput`), `:119-146` (`Build`), `:163-258` (`buildSessionRecord`)
- Modify: `internal/module/peers/module.go:689-696` and `:760` (`localEnvelope` fills `Contexts`)
- Test: `internal/peers/record_test.go`, `internal/module/peers/module_test.go`

**Interfaces:**
- Consumes: `agent.ContextUsageReader` (Task 1.1), via type assertion on `m.owners`.
- Produces:
  ```go
  // in package peers (internal/peers)
  type ContextInfo struct {
  	UsedPercentage *float64 `json:"used_percentage"`
  	Window         int      `json:"window"`
  	At             int64    `json:"at"`
  }
  // AgentInfo gains:  Context *ContextInfo `json:"context,omitempty"`
  // BuildInput gains: Contexts map[string]ContextInfo // by CC session id
  ```

- [ ] **Step 1: Write the failing tests in `internal/peers/record_test.go`.**

```go
func TestBuild_ContextAttachedBySessionID(t *testing.T) {
	in := ccBuildInput("purdex-b0", "sess-x")
	p := 72.0
	in.Contexts = map[string]ContextInfo{"sess-x": {UsedPercentage: &p, Window: 1000000, At: 5}}
	r := Build(in)[0]
	if r.Agent == nil || r.Agent.Context == nil || *r.Agent.Context.UsedPercentage != 72 || r.Agent.Context.Window != 1000000 {
		t.Fatalf("context not attached: %+v", r.Agent)
	}
}

func TestBuild_NoContextWhenUnknown(t *testing.T) {
	r := Build(ccBuildInput("purdex-b0", "sess-x"))[0]
	if r.Agent == nil || r.Agent.Context != nil {
		t.Fatalf("want nil context, got %+v", r.Agent)
	}
	b, _ := json.Marshal(r)
	if strings.Contains(string(b), `"context"`) {
		t.Fatalf("context key must be omitted when unknown: %s", b)
	}
}

func TestBuild_SessionRowCwdPrefersRegistryThenOwnerThenTmux(t *testing.T) {
	in := ccBuildInput("purdex-b0", "sess-x") // tmux Cwd "/w"
	in.Entries[0].Cwd = "/repo/.claude/worktrees/wt"
	owner := in.Owners[in.Sessions[0].Code]
	owner.Cwd = "/repo"
	in.Owners[in.Sessions[0].Code] = owner
	if got := Build(in)[0].Cwd; got != "/repo/.claude/worktrees/wt" {
		t.Fatalf("registry cwd first: got %q", got)
	}
	in.Entries[0].Cwd = ""
	if got := Build(in)[0].Cwd; got != "/repo" {
		t.Fatalf("owner cwd second: got %q", got)
	}
	owner.Cwd = ""
	in.Owners[in.Sessions[0].Code] = owner
	if got := Build(in)[0].Cwd; got != "/w" {
		t.Fatalf("tmux session_path last: got %q", got)
	}
}

func TestBuild_OwnerOnlyRowUsesOwnerCwd(t *testing.T) {
	in := ccBuildInput("purdex-b0", "sess-x")
	in.Entries = nil // owner only → inbox_dead
	owner := in.Owners[in.Sessions[0].Code]
	owner.Cwd = "/repo"
	in.Owners[in.Sessions[0].Code] = owner
	r := Build(in)[0]
	if r.Reason != "inbox_dead" || r.Cwd != "/repo" {
		t.Fatalf("got reason=%q cwd=%q", r.Reason, r.Cwd)
	}
}
```

  Check `ccBuildInput` (`record_test.go:1385`) for the exact shape of `Owners` and `Entries` before writing. If `Owners` is keyed differently, adjust the two owner lines to its key. Keep the assertions unchanged.

- [ ] **Step 2: Run the tests and verify they fail.**
  - Run: `go test ./internal/peers/ -run 'TestBuild_Context|TestBuild_NoContext|TestBuild_SessionRowCwd|TestBuild_OwnerOnlyRowUsesOwnerCwd' -v`
  - Expected: FAIL to compile, with `in.Contexts undefined` and `r.Agent.Context undefined`.

- [ ] **Step 3: Implement.**
  - **`record.go`:**
    - Add the `ContextInfo` type above.
    - Add `Context *ContextInfo \`json:"context,omitempty"\`` as the last field of `AgentInfo`.
    - Add `Contexts map[string]ContextInfo` to `BuildInput`, with the doc comment `// by CC session id; nil means unknown for every row`.
    - At the end of `Build`, before returning, attach the context:
      ```go
      for i := range out {
      	if a := out[i].Agent; a != nil && a.SessionID != "" {
      		if c, ok := in.Contexts[a.SessionID]; ok {
      			c := c
      			a.Context = &c
      		}
      	}
      }
      ```
      Use the slice variable name `Build` already uses. Note that `out[i].Agent` is a pointer, so this mutates in place.
    - Add a helper:
      ```go
      // preferCwd sets rec.Cwd to the first non-empty candidate: the CC session's
      // own cwd (registry, which follows EnterWorktree), then the hook-reported
      // frame cwd, then the tmux session_path already in rec.Cwd (spec §8.6).
      func preferCwd(rec *PeerRecord, candidates ...string) {
      	for _, c := range candidates {
      		if c != "" {
      			rec.Cwd = c
      			return
      		}
      	}
      }
      ```
    - Call `preferCwd` in `buildSessionRecord`:
      - at the two entry-pinned returns (`:231`, `:249`): `preferCwd(&rec, <entry>.Cwd, owner.Cwd)`;
      - at the owner-only exits (`:220`, `:226`, `:244`, `:254`) and in the non-cc branch (`:186-195`): `preferCwd(&rec, owner.Cwd)`.
  - **`internal/module/peers/module.go`, `localEnvelope`:** before the `ipeers.Build(...)` call at `:760`, collect the usage:
    ```go
    var contexts map[string]ipeers.ContextInfo
    if r, ok := m.owners.(agent.ContextUsageReader); ok {
    	contexts = map[string]ipeers.ContextInfo{}
    	add := func(sid string) {
    		if sid == "" {
    			return
    		}
    		if u, ok := r.ContextUsage(sid); ok {
    			contexts[sid] = ipeers.ContextInfo{UsedPercentage: u.UsedPercentage, Window: u.WindowSize, At: u.At}
    		}
    	}
    	for _, o := range owners {
    		add(o.SessionID)
    	}
    	for _, e := range entries {
    		add(e.SessionID)
    	}
    }
    ```
    Then pass `Contexts: contexts` into the `BuildInput`. Use the local variable names `localEnvelope` already uses for the owners map and the registry entries. `m.owners` is nil in tests that build `&Module{}` literally; the type assertion on a nil interface is false, which is safe.
  - Add `var _ agent.ContextUsageReader = (*agent.Module)(nil)` next to the existing assertion at `module.go:453`.

- [ ] **Step 4: Add the module-level test** in `internal/module/peers/module_test.go`. Make a fake owner resolver that also implements `ContextUsage(sid)`: embed the existing `fakeOwners` in a new struct `usageOwners{fakeOwners; usage map[string]agent.ContextUsage}`. Then build the module through `newTestModuleWith(t, fixtureOpts{...})`, using the same fixture as an existing deliverable-cc test. GET `/api/peers` and assert that `peers[0].agent.context.used_percentage == 72` in the JSON.
- [ ] **Step 5: Run.**
  - Run: `go test ./internal/peers/ ./internal/module/peers/ -v -run 'Context|Cwd'`, then the full packages: `go test ./internal/peers/ ./internal/module/peers/`
  - Expected: PASS. The JSON-key pin tests (`TestBuild_JSON_EveryRecordHasCoreKeys`, `TestPeerRecord_JSONKeys`) still pass, because `context` is omitempty.
- [ ] **Step 6: Commit.**
  ```bash
  git commit -m "feat(daemon): peer rows carry context usage and the session's own cwd

  Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>" -- internal/peers/record.go internal/peers/record_test.go internal/module/peers/module.go internal/module/peers/module_test.go
  ```

### Task 1.3: `pdx peers` gains a CTX column

**Files:**
- Modify: `cmd/pdx/peers.go:500-522` (`formatPeersTable`), `:592-636` (`formatPeersAllTable`)
- Test: `cmd/pdx/peers_test.go:25-90` (fixture and `wantPeersTable`), `:746-800` (all-hosts fixture and `wantPeersAllTable`)

**Interfaces:** Consumes `peers.AgentInfo.Context` (Task 1.2).

- [ ] **Step 1: Write the failing test changes.**
  - In `peersTableFixture()`, give the row `sess1` (cc, working) `Context: &peers.ContextInfo{UsedPercentage: ptr(72.4), Window: 1000000}`, and give `sess2` (codex) none. Add `func ptr(f float64) *float64 { return &f }` in the test file if absent.
  - Update `wantPeersTable`. The header becomes `TITLE  ADDRESS  AGENT  STATUS  CTX  DELIVERABLE  TMUX  CWD`, with tabwriter alignment. Rows show `72%` for `sess1` and `-` for the others.
  - Regenerate the expected string by running the test once, then **check every column by eye** against the fixture before pasting it.
  - Do the same for `peersAllTableFixture()` and `wantPeersAllTable`: give one cc row a context, put CTX after STATUS, and keep the HOST, TITLE, ADDRESS order test at ~`:827` passing.
  - Add:
    ```go
    func TestCtxField(t *testing.T) {
    	p := 72.6
    	cases := []struct {
    		rec  peers.PeerRecord
    		want string
    	}{
    		{peers.PeerRecord{}, "-"},
    		{peers.PeerRecord{Agent: &peers.AgentInfo{Type: "cc"}}, "-"},
    		{peers.PeerRecord{Agent: &peers.AgentInfo{Type: "cc", Context: &peers.ContextInfo{}}}, "-"},
    		{peers.PeerRecord{Agent: &peers.AgentInfo{Type: "cc", Context: &peers.ContextInfo{UsedPercentage: &p}}}, "73%"},
    	}
    	for _, c := range cases {
    		if got := ctxField(c.rec); got != c.want {
    			t.Errorf("ctxField(%+v) = %q, want %q", c.rec, got, c.want)
    		}
    	}
    }
    ```
- [ ] **Step 2: Run and see FAIL.**
  - Run: `go test ./cmd/pdx/ -run 'TestFormatPeers|TestCtxField' -v`
  - Expected: FAIL, with `undefined: ctxField` and golden mismatches.
- [ ] **Step 3: Implement.**
  ```go
  // ctxField renders the session's context usage, rounded, or "-" when unknown.
  func ctxField(rec peers.PeerRecord) string {
  	if rec.Agent == nil || rec.Agent.Context == nil || rec.Agent.Context.UsedPercentage == nil {
  		return "-"
  	}
  	return fmt.Sprintf("%.0f%%", *rec.Agent.Context.UsedPercentage)
  }
  ```
  - In both table functions, add `CTX` after `STATUS` in the header.
  - Add one `%s` to each row format, filled by `sanitizeCell(ctxField(rec))` right after `statusField`.
- [ ] **Step 4: Run.**
  - Run: `go test ./cmd/pdx/ -run 'Peers|CtxField'`
  - Expected: PASS.
- [ ] **Step 5: Commit.**
  ```bash
  git commit -m "feat(cli): pdx peers shows each session's context usage

  Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>" -- cmd/pdx/peers.go cmd/pdx/peers_test.go
  ```

### Task 1.4: The peer_not_found hint stops promising a ref never changes (spec M3)

**Files:**
- Modify: `internal/module/peers/send.go:52` (`peerNotFoundHint`)
- Test: `internal/module/peers/send_test.go:1467` (`TestSend_PeerNotFoundTeachesTheV4AddressForms`)

- [ ] **Step 1: Write the failing assertion.** In `TestSend_PeerNotFoundTeachesTheV4AddressForms`, add:
  ```go
  if strings.Contains(detail, "never changes") {
  	t.Errorf("a ref changes on a manual /clear; the hint must not promise otherwise: %q", detail)
  }
  if !strings.Contains(detail, "survives renames") {
  	t.Errorf("hint must say what a ref survives: %q", detail)
  }
  ```
  Use the variable that test already uses for the response detail.
- [ ] **Step 2: Run and see FAIL.**
  - Run: `go test ./internal/module/peers/ -run TestSend_PeerNotFoundTeachesTheV4AddressForms -v`
- [ ] **Step 3: Implement.** In `peerNotFoundHint`, replace `which never changes` with `which survives renames (a manual /clear starts a new ref)`. Leave the long history comment above it as is, and add one line: `// 2026-10-06: a ref changes on /clear (lead-team-relay spec M3).`
- [ ] **Step 4: Run.**
  - Run: `go test ./internal/module/peers/`
  - Expected: PASS.
- [ ] **Step 5: Commit.**
  ```bash
  git commit -m "fix(daemon): peer_not_found hint no longer claims a ref never changes

  Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>" -- internal/module/peers/send.go internal/module/peers/send_test.go
  ```

  Spec §8.4's "and relays" wording waits for P5a, which is when relays exist.



---

## Handoff notes: P2a, P2b and P3 as designed but not yet written (2026-10-07, already adjusted for U5b)

These are the decisions and code facts gathered for the unwritten sections. Re-verify every `file:line` against the current main before using them.

### P2a: the `team` module and approval requests

**Plan decisions:**
- **PD1. Wire types live in a leaf package, `internal/team`** (`wire.go`): kinds, states, ops, error codes, limits, `Approval`, `CreateApprovalRequest`, `DecideRequest`, `APIError{error, detail, approval}` and `EventValue{op, approval, approvals}`. `cmd/pdx` must not import `internal/module/*` (`cmd/pdx/msg.go:26-28`). The daemon module is `internal/module/team` (package `teammod`).
- **PD2. The OnSubscribe snapshot is one event, `{op:"snapshot", approvals:[…]}`.** `approvals` is always present, `[]` when empty: give `EventValue` a custom `MarshalJSON`. The SPA replaces a host's whole set from it, so a request that closed while a client was disconnected disappears.
- **PD3 (U5b). `decide` approve is one click.**
  - Before P4 there is no teams table, so approving a lead request only closes it as `approved`, with `grant` = the payload as the user edited it.
  - P4 adds team creation in the same transaction.
- **PD4. `already_lead` and `member_cannot_lead` arrive with the teams table in P4.** P2 enforces `request_open` only: one open request per origin session and kind.
- **PD5. Sweeper cadence.** It ticks every 1 s for the deadline and the lease. The origin-liveness check runs only every 10th tick, and only while requests are open: reading the CC registry forks `ps` four times per entry (`DefaultLiveness`). A registry read error never abandons a request.
- **PD6. Close `team.db` in `Close()` (`core.Closer`), not `Stop()`.** The shutdown order is `cancel` → `StopModules` → `srv.Shutdown` (in-flight handlers) → `CloseModules` (`cmd/pdx/shutdown.go:161-186`). Long-polls must end on the module's own `stopCtx`, or they eat the 10 s shutdown budget. The nex module is the precedent (`nex/module.go:417-424`).

**Store** (`internal/module/team/store.go`, modelled on `hostconfig/store.go:38-63`):
- DSN `?_pragma=journal_mode(wal)&_pragma=busy_timeout(5000)`; `SetMaxOpenConns(1)` for `:memory:`.
- Table `approval_requests`:
  `id PK, kind, host_id, origin_session_id, origin_json, payload_json, request_hash, state, created_at, deadline_at, lease_until, decided_by_json, decided_at, grant_json`
  plus an index on `(state, created_at)`.
- Methods:
  - `Create` uses `INSERT … ON CONFLICT(id) DO NOTHING`; when it inserts nothing, it returns the stored row and its hash.
  - `CloseIfOpen` is the CAS: `UPDATE … WHERE id=? AND state='open'`, closed when `RowsAffected()==1`.
  - `RenewLease` is `MAX(lease_until, ?)` while open.
  - `ExtendOpenLeases(until)` is the boot grace.
  - `ListOpen` orders by `created_at, id`.
  - `OpenByOrigin(sessionID, kind)`.

**Peers origin resolver** (`internal/module/peers/origin_resolver.go`):
- Register `OriginResolverKey = "peers.origin-resolver"` in peers `Init`.
- `ResolveOrigin(inbox)` = `ipeers.ReadRegistry(m.registryDir, m.liveness)` + `findOriginEntry(entries, m.proxyPIDs(), inbox)` (`titles.go:31`).
- `LiveSession(sessionID)` matches on `SessionID`, `!IsProxy`, and the pid not in `proxyPIDs`.
- Do **not** reuse `m.origin()`: it fails when the title store is nil.
- Origin fields for the wire: `SessionID`, `ipeers.RefID(SessionID)`, `Name`, `PID`, `ProcStart`, `Cwd`, `Tmux` (from the registry `Entry`).

**Module:**
- `Name "team"`, `Dependencies {"peers"}`.
- `Init`: resolver via registry type-assert, `team.db` in `c.Cfg.DataDir`, host id from the same source peers uses.
- `Start`: `ExtendOpenLeases(now+30s)`, `OnSubscribe(sendSnapshot)`, the sweeper goroutine.
- Snapshot send: `TrySend`, and on failure `Events.Remove(sub)`, as in `session/module.go:247-266`.
- Wiring tests in `cmd/pdx` call the real `registerServeModules` + `InitModules` with a TempDir data dir: `monitor_module_test.go:15`, `nex_register_test.go:36,73`, `removed_routes_test.go:41`. `team` `Init` must succeed there.

**Routes** (all behind TokenAuth; note that an empty token lets everything pass):

| Route | Notes |
|---|---|
| `POST /api/team/approvals` | `201` new / `200` same id and same hash; `409 id_conflict`; `409 request_open` (+approval); `400 bad_request` / `origin_unknown`; `503 not_ready` when stopping. P2 accepts kind `lead` only. |
| `GET /api/team/approvals?state=open` | |
| `GET /api/team/approvals/{id}?wait=≤25` | Renews the lease. Waiters are a per-id `[]chan struct{}`, woken on close. It selects on the waiter, a timer, `r.Context()` and `stopCtx`. |
| `DELETE /api/team/approvals/{id}` | Closes as `cancelled`. |
| `POST /api/team/approvals/{id}/decide` | `{decision, grant, client}`. `client.addr` comes from `RemoteAddr`. A closed request gives `409 already_decided` with the row. |

- No shared JSON helper exists; write a local `writeJSON` / `writeErr`.
- The idempotency hash is sha256 of `kind \0 origin_session \0 wait_s \0` plus the normalised payload: roots `filepath.Clean`ed and absolute, defaulting to the origin's cwd; `max_members` 0 → 3, cap 8; `wait_s` 0 → 540, cap 600.

**Tests to keep:**
- one winner under concurrent close (file-backed DB, `-race`);
- the long-poll wakes on cancel and returns on `stopCancel`;
- the lease is renewed by a poll;
- boot grace;
- deadline → `timeout`;
- lease → `abandoned`;
- origin dead → `abandoned` on the 10th tick only;
- decide racing the sweeper: exactly one `closed` event.

### P2b: `daemonclient` and `pdx lead request`

**`cmd/pdx/daemonclient`:**
- **Retryable:** `ECONNREFUSED`, `ECONNRESET`, `io.EOF`, `io.ErrUnexpectedEOF`, any `*net.OpError` that is not a context error, and `503` with `error` equal to `shutting_down` or `not_ready`.
- **Not retryable:** `503 {"reason":"pairing_mode"}` from `PairingGuard`.
- **Backoff and grace:** 250 ms, 500 ms, 1 s, 1 s…, with a 30 s grace from the first failure; then `ErrUnavailable`.
- **The one stderr line** `daemon 重啟中，繼續等待…` per client. On recovery, if `/api/health` `boot_id` changed, `daemon 已重新啟動（boot <id>）` once.
- **404:** a plain-text 404 (Go's mux) → `ErrUnsupported`; a JSON 404 `{"error":"not_found"}` is returned to the caller.
- **Base URL:** `resolveDaemonHost(cfg.Bind)` (`statusline_proxy.go:145`). `msg.go` uses the raw bind, which breaks on a wildcard bind.

**`pdx lead request`** (`cmd/pdx/lead.go`):
- Signature: `runLeadCmd(ctx, args, getenv, stdout, stderr, newID, sleep) int`.
- Usage errors give exit 2 before any config load.
- Origin from `CLAUDE_CODE_MESSAGING_SOCKET` (`msgOriginInbox`, `msg.go:673`).
- The poll uses `?wait=25` with a 35 s client timeout.
- Exit codes: approved 0 (grant JSON on stdout), denied 10, timeout 11, cancelled or abandoned 12, `request_open` 13, unavailable 20, unsupported 21.
- On SIGINT/SIGTERM (`signal.NotifyContext`): `DELETE` with a fresh 3 s context and no retry, then exit 12.
- Test helpers: `writeTestConfig` (`peers_test.go:349`), `fakeGetenv` (`msg_test.go:20`).
- Smoke-test against a throwaway daemon on a spare port and data dir, never mlab's live daemon.

### P3: the approval dialog (SPA)

- **Store** (`useApprovalStore`): entries keyed `hostId\0id`.
  - `applySnapshot` replaces the host's set and returns what vanished, along with any queued deny.
  - Also `applyOpened` (idempotent), `applyClosed`, `queue`, `markSent`.
  - `selectCurrent` = the oldest `created_at` across hosts.
- **Routing:**
  - Add `'approval.request'` to the union in `spa/src/lib/host-events.ts:4-15`.
  - Branch in `useMultiHostEventWs.ts:172-235`, before `isAgentWsEvent`, like `nex-worker-exited`.
  - `hostId` comes from the per-host closure.
- **Dialog:**
  - `ConfirmDialog` only has Cancel + Confirm, with a fixed label, so build a custom panel in the same style.
  - Escape does not dismiss it.
  - Both 核准 and 拒絕 are one click (U5b).
  - Mount it after `<HandoffDialogHost />` (`App.tsx:283`).
  - Show requests from hidden hosts too.
- **While the host is not `connected`:**
  - the buttons queue their decision and show a banner;
  - queued decisions are sent once the snapshot has re-added the request;
  - a vanished one gives the toast `approval.toast.ended_while_away`.
- **The client label** is `Purdex.app @ <hostname>`, from `window.electronAPI.localDaemonStatus?.().hostname` (`electron.d.ts:54-62, :173`), read once.
- **API:** follow `spa/src/lib/nex/handoff-api.ts:129-150` (`postJson`, which keeps the error body) and use `pinnedHostFetch` (`host-api.ts:214`).
- **Notification:**
  - `showNotification` with `action {kind:'open-approval', hostId}` and `broadcastTs: created_at` (Electron dedups on it), sent on `opened` only, never on a snapshot.
  - The click listener in `useNotificationDispatcher.ts:280-294` treats unknown kinds as `open-session`, so add an `open-approval` branch that only calls `focusMyWindow`.
- **Restart confirm:** `RestartDaemonButton.tsx:115-131` gains a line counted from the store (`selectOpenCount`). That is enough until relays exist; P6 adds `GET /api/team/inflight`.
- **i18n:**
  - `useI18nStore` `t(key, params)` with `{{param}}`;
  - keys in both `spa/src/locales/en.json` and `zh-TW.json`, held to the same key set by `locale-completeness.test.ts`;
  - plurals through `pluralKey(base, count)` → `_one` / `_other`.
- **Tests:** the store-driven dialog follows `HandoffDialogHost.test.tsx`; the WS end to end follows `useMultiHostEventWs.worker-exited.test.ts` (`FakeSocket`, `checkHealth` mocked).
