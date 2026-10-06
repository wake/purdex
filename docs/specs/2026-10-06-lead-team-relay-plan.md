# Lead / member / team and context relay — Implementation Plan (v1)

> **Status (2026-10-07):** resumed. Written by `mlab/_v3o1ps` (header, P0, P1), continued by `mlab/_81nu3d` (P2a, P2b, P3). The branch is rebased onto origin/main `fb9fcbd8` (alpha.508); every `file:line` below was re-verified against that commit. First codex plan round done 2026-10-07; its five test/contract findings (F4 inflight route and SPA fetch, F5 two-pane usage test, F6 `pdx lead request` restart end-to-end test, F7 three-way CAS race, F8 closed-before-opened tombstones) are applied in this version, and every Go block of P2a/P2b plus the TS of Tasks 3.1, 3.2 and 3.7 was re-compiled and re-run in a scratch copy.

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
- PRs **P0, P1, P2a-1, P2a-2, P2a-3, P2b-1, P2b-2, P2b-3, P3a-1, P3a-2, P3b**, in that order (the splits are decided in each section's "Coordinator decisions").
- The rest gets **plan v2**, with one codex round, after P3 merges:
  - P5a/P5b, self relay — next per spec §12's suggested order;
  - P4, team and spawn; then P4b host selection and P4c cross-host execution (U15, spec §7.4);
  - P6, member relay;
  - P7, notices.

## Global Constraints

- **PR size.** One PR ≤ 800 lines of diff or ≤ 20 files. Every task is its own commit, test first (TDD).
- **Tests and builds:**
  - Go: `cd <worktree> && go test ./<pkg>/...`
  - SPA: `cd <worktree>/spa && npx vitest run <path>`, `pnpm run lint`, `pnpm run build`
  - pnpm, never npm.
- **Commits.** Every commit ends with `Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>`. Parallel subagents in one worktree commit with `git commit --only <files>`.
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
3. **The daemon restarts while `pdx lead request` is long-polling:** the CLI prints the restart line once, re-polls the **same** request id after the new `boot_id` answers, and the request is still open, with its lease extended at boot. → Task 2.4 (`TestGet_LongPollReturnsOnStopAndOnTimer`), Task 2.6 (`TestStart_ExtendsLeasesAndSnapshotsOpenRequests`), Task 2b.1 (`TestDo_RefusedThenNewBootIDThenSucceeds`), Task 2b.3 (`TestLeadRequest_SurvivesDaemonRestartMidPoll`: end to end through `lead.go`, two fake daemons on one port).
4. **A decide racing the deadline sweeper and a cancel:** exactly one close wins. The losing decide gets `409 already_decided` carrying the winner's row (`decided_by` included when a decide won), the losing `DELETE` gets that same row as its `200` body, and one `closed` event is broadcast. → Task 2.2 (`TestStore_CloseIfOpenExactlyOneWinner`, timeout / denied / cancelled), Task 2.4 (`closeAs`), Task 2.5 (`TestTick_DecideSweeperAndCancelCloseOnce`).
5. **A Purdex.app whose host socket drops while the dialog is open:**
   - the dialog stays, with its buttons disabled;
   - on reconnect, the snapshot neither duplicates the request nor revives one that closed meanwhile;
   - a deny clicked while disconnected is sent once on reconnect.

   → Task 3.4 (dialog stays, buttons `aria-disabled`), Task 3.2 and Task 3.3 (snapshot neither duplicates nor revives), Task 3.5 (queued deny sent once).

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
  |  | `lead` | 經使用者核准（任一 Purdex.app 按一下）；可開 member、替 member 安排接力 |
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

  Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
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
- Modify: `internal/module/agent/module.go:57-61` (new field next to `statusSnapshots`), `internal/module/agent/module.go:125` (init), `internal/module/agent/handler.go:1273` (record right before the `resolveSessionCode` early return, so an unresolved tmux name still records usage; the snapshot cache at :1278-1280 stays as is)
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
- The statusline `remove` action (`handler.go:978-980`) rebuilds `statusSnapshots`; it **also clears `contextUsage`** in the same critical section, so a removed statusline leaves no stale `CTX` on the peer rows (codex R2 finding, 2026-10-07; pinned by `TestContextUsage_ClearedByStatuslineRemove`).

- [ ] **Step 4: Run the tests and verify they pass.**
  - Run: `go test ./internal/module/agent/ -run 'TestContextUsage|TestHandleAgentStatus' -v`
  - Expected: PASS, including the existing `TestHandleAgentStatus_*` tests.

- [ ] **Step 5: Commit.**
  ```bash
  git add internal/module/agent/context_usage.go internal/module/agent/context_usage_test.go internal/module/agent/module.go internal/module/agent/handler.go
  git commit -m "feat(daemon): record statusline context usage per CC session id

  Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
  ```

### Task 1.2: Peer rows carry context usage and the session's own cwd

**Files:**
- Modify: `internal/peers/record.go:19-28` (`AgentInfo`), `:93-115` (`BuildInput`), `:119-146` (`Build`; its slice is named `records`), `:163-259` (`buildSessionRecord`; the tmux cwd is the struct-literal field `Cwd: s.Cwd,` at `:173`)
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

  `ccBuildInput` (`record_test.go:1385`) keys `Owners` by session code (`"s1"` = `Sessions[0].Code`), so `in.Owners[in.Sessions[0].Code]` is right; `Entries[0].Cwd` is `/w`, the same as the tmux cwd, and the owner `Cwd` is empty. `encoding/json` and `strings` are already imported there.

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
      `Build`'s slice is named `records`, so write `records[i]`. `Agent` is a pointer, so this mutates in place. Entry rows are appended before the return, so the loop covers them too.
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
      - at the two entry-pinned returns (`:234`, `:252`; the `rec.Agent = agentInfoFromEntry(...)` lines just above are `:231`, `:249`): `preferCwd(&rec, <entry>.Cwd, owner.Cwd)`;
      - at the owner-only exits (returns at `:223`, `:229`, `:247`, `:257`, each right after a `rec.Agent = ownerFallbackAgent` line) and in the non-cc branch (`:186-194`): `preferCwd(&rec, owner.Cwd)`.
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

- [ ] **Step 4: Add the module-level test** in `internal/module/peers/module_test.go`. It wraps the existing `fakeOwners` (`internal/module/peers/fakes_test.go:142`; pointer receiver) so the module's `m.owners` also satisfies `agent.ContextUsageReader`, builds the module with the same shape as the deliverable-cc test at `module_test.go:322-376`, and reads `/api/peers` through `doGetPeers`:

```go
// usageOwners is fakeOwners plus the ContextUsageReader the P1 peers
// module type-asserts on m.owners.
type usageOwners struct {
	*fakeOwners
	usage map[string]agent.ContextUsage
}

func (u *usageOwners) ContextUsage(sessionID string) (agent.ContextUsage, bool) {
	c, ok := u.usage[sessionID]
	return c, ok
}

var _ agent.ContextUsageReader = (*usageOwners)(nil)

// Review Focus 2: two CC panes in one tmux instance (inst1). mt1 has a
// registry entry (fixture 76973) and is at 72 %; mt2 is owner-only (no
// registry entry, so its row is inbox_dead) and is at 10 %. Each row must
// carry its own session's usage, never the last writer's.
func TestLocalEnvelope_AttachesContextUsageBySessionID(t *testing.T) {
	dir := t.TempDir()
	writeRegistryFixture(t, dir, "76973.json", fixture76973)
	sessions := &fakeSessions{sessions: []session.SessionInfo{
		{Code: "mt1code", Name: "mt1", Cwd: "/Users/wake/Workspace/wake/purdex", TmuxInstance: "inst1"},
		{Code: "mt2code", Name: "mt2", Cwd: "/Users/wake/Workspace/wake/purdex", TmuxInstance: "inst1"},
	}}
	pct1, pct2 := 72.0, 10.0
	owners := &usageOwners{
		fakeOwners: &fakeOwners{owners: map[string]agent.PaneOwner{
			"mt1code": {AgentType: "cc", SessionID: "fa5d4c07-d9d9-4184-9e13-e491f2f4bf7c", Cwd: "/Users/wake/Workspace/wake/purdex", TmuxPaneID: "%10", LastSeenAt: 1789314156000, Status: "busy"},
			"mt2code": {AgentType: "cc", SessionID: "second-session-id", Cwd: "/Users/wake/Workspace/wake/purdex", TmuxPaneID: "%11", LastSeenAt: 1789314156000, Status: "idle"},
		}},
		usage: map[string]agent.ContextUsage{
			"fa5d4c07-d9d9-4184-9e13-e491f2f4bf7c": {UsedPercentage: &pct1, WindowSize: 1000000, At: 5},
			"second-session-id":                    {UsedPercentage: &pct2, WindowSize: 200000, At: 6},
		},
	}
	clock := &fakeClock{times: []time.Time{time.Unix(0, 0)}}
	c := newTestCore(t, "mlab:abc123", "mlab")
	m := newTestModule(t, c, sessions, owners, dir, allLiveLiveness(fixture76973ProcStart), clock, 2*time.Second)

	rr := doGetPeers(t, m, "/api/peers")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", rr.Code, rr.Body.String())
	}
	var got struct {
		Peers []struct {
			SessionCode string `json:"session_code"`
			Agent       *struct {
				SessionID string `json:"session_id"`
				Context   *struct {
					UsedPercentage *float64 `json:"used_percentage"`
					Window         int      `json:"window"`
				} `json:"context"`
			} `json:"agent"`
		} `json:"peers"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v; body=%s", err, rr.Body.String())
	}
	type usage struct {
		sessionID string
		pct       *float64
		window    int
	}
	seen := map[string]usage{} // by session code
	for _, p := range got.Peers {
		if p.Agent == nil || p.Agent.Context == nil {
			continue
		}
		seen[p.SessionCode] = usage{p.Agent.SessionID, p.Agent.Context.UsedPercentage, p.Agent.Context.Window}
	}
	want := map[string]usage{
		"mt1code": {sessionID: "fa5d4c07-d9d9-4184-9e13-e491f2f4bf7c", pct: &pct1, window: 1000000},
		"mt2code": {sessionID: "second-session-id", pct: &pct2, window: 200000},
	}
	for code, w := range want {
		g, ok := seen[code]
		if !ok || g.pct == nil || *g.pct != *w.pct || g.window != w.window || g.sessionID != w.sessionID {
			t.Fatalf("%s agent.context = %+v, want used %v window %d for session %s (two CC panes in one tmux instance must each carry their own usage); body=%s",
				code, g, *w.pct, w.window, w.sessionID, rr.Body.String())
		}
	}
	if len(seen) != 2 {
		t.Fatalf("rows with context = %d, want exactly mt1code and mt2code; body=%s", len(seen), rr.Body.String())
	}
}
```

  If `session_code` is not the JSON key `PeerRecord` uses for `SessionCode`, read it from `internal/peers/record.go` and adjust the one tag; the assertions stay. `AgentInfo.SessionID` is `json:"session_id,omitempty"` (`record.go:21`). The mt2 row has no registry entry, so it is the owner-only `inbox_dead` shape of `TestBuild_OwnerOnlyRowUsesOwnerCwd`; `ownerFallbackAgent` (`record.go:303-306`) carries `owner.SessionID`, which is what `Build`'s context loop keys on — so an owner-only pane shows its usage too. Dropping the per-session-id lookup in favour of "the last reading posted" turns this test red on whichever of the two rows got the other's number (mutation deliverable for Review Focus 2).

- [ ] **Step 5: Run.**
  - Run: `go test ./internal/peers/ ./internal/module/peers/ -v -run 'Context|Cwd'`, then the full packages: `go test ./internal/peers/ ./internal/module/peers/`
  - Expected: PASS. The JSON-key pin tests (`TestBuild_JSON_EveryRecordHasCoreKeys`, `TestPeerRecord_JSONKeys`) still pass, because `context` is omitempty.
- [ ] **Step 6: Commit.**
  ```bash
  git commit -m "feat(daemon): peer rows carry context usage and the session's own cwd

  Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>" -- internal/peers/record.go internal/peers/record_test.go internal/module/peers/module.go internal/module/peers/module_test.go
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
  - Do the same for `peersAllTableFixture()` and `wantPeersAllTable`: give one cc row a context, put CTX after STATUS, and keep `TestFormatPeersAllTable_HostThenTitleThenAddress` (`:815`, assertion `:829`) passing.
  - **Two column-pin tests break on purpose and must be updated in the same commit:** `TestFormatPeersTable_V4Columns` (`peers_test.go:1510`, `wantCols` at `:1515`) and `TestFormatPeersAllTable_V4Columns` (`:1603`, `wantCols` at `:1609`) list the exact header columns; insert `CTX` after `STATUS` in both `wantCols`. `TestFormatPeersTable_TmuxColumn` (`:1557`) counts from the right and is unaffected.
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

  Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>" -- cmd/pdx/peers.go cmd/pdx/peers_test.go
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
  The response detail variable in that test is `ae.Detail`.
- [ ] **Step 2: Run and see FAIL.**
  - Run: `go test ./internal/module/peers/ -run TestSend_PeerNotFoundTeachesTheV4AddressForms -v`
- [ ] **Step 3: Implement.** In `peerNotFoundHint`, replace `which never changes` with `which survives renames (a manual /clear starts a new ref)`. Leave the long history comment above it as is, and add one line: `// 2026-10-06: a ref changes on /clear (lead-team-relay spec M3).`
- [ ] **Step 4: Run.**
  - Run: `go test ./internal/module/peers/`
  - Expected: PASS.
- [ ] **Step 5: Commit.**
  ```bash
  git commit -m "fix(daemon): peer_not_found hint no longer claims a ref never changes

  Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>" -- internal/module/peers/send.go internal/module/peers/send_test.go
  ```

  Spec §8.4's "and relays" wording waits for P5a, which is when relays exist.

---

## PR P2a — daemon: `team` module, `team.db`, approval requests (spec §6.2, §6.5, §9.2, §15)

**Scope.** The daemon half of the lead request: a leaf wire package (`internal/team`), a new daemon module `internal/module/team` (package `teammod`) that owns `team.db` and the `approval_requests` table — one state machine, closed by compare-and-set — and serves the five `/api/team/approvals` routes and `GET /api/team/inflight` (spec §9.5) behind `TokenAuth`; an exported origin resolver in the peers module that attributes a caller's `origin_inbox` to a live CC session; the `approval.request` host event (`opened` / `closed` / `snapshot`); a 1 s sweeper (deadline → `timeout`, lease → `abandoned`, origin gone → `abandoned` every 10th tick); the boot lease grace; and the module's registration in `cmd/pdx`. Nothing in `cmd/pdx/lead.go`, nothing in the SPA (P2b, P3). Approving a lead request only closes it as `approved` with a `grant` (PD3); the team itself arrives in P4. `self_relay` is refused with `400 unsupported_kind` until P5a.

**Every line below was compiled and run** (2026-10-07) in a scratch copy of the worktree at `d39886e8` (Go code identical to origin/main `fb9fcbd8`); the "Expected" outputs are the measured ones. Go 1.26.0, `modernc.org/sqlite v1.54.0`, `gorilla/websocket v1.5.3`, `testify v1.11.1`.

**Verified code facts (worktree line numbers):**
- `core.Module` / `core.Closer`: `internal/core/core.go:17-30`; `InitModules` topo-sorts by `Dependencies()` (`:155-169`); `CloseModules` runs after the HTTP server stopped (`:202-214`). Registry: `Register`/`Get` on `*core.ServiceRegistry` (`internal/core/registry.go:20-32`), `c.Registry` is always non-nil after `core.New` (`core.go:99-102`).
- Events: `EventSubscriber.TrySend` (`internal/core/events.go:56-68`), `Done()` (`:72`), `EventsBroadcaster.Remove` (`:148-158`), `BroadcastEvent` (`:172-185`), `OnSubscribe` (`:218-222`); the callbacks run **only** inside `HandleHostEvents` (`:243-250`), never for `AddTestSubscriber` (`:191-197`) — so the snapshot test dials a real WebSocket. `HostEvent{Type, Session, Value}` (`:14-22`).
- Store template: `internal/module/hostconfig/store.go:38-63` (DSN `?_pragma=journal_mode(wal)&_pragma=busy_timeout(5000)`, `SetMaxOpenConns(1)` for `:memory:`, `_ "modernc.org/sqlite"`), `module.go:27-32` (`filepath.Join(c.Cfg.DataDir, …)`). `RowsAffected` as the verdict: `internal/module/profiles/store.go:205-215`.
- Lifecycle template: `internal/module/nex/module.go:162-166` (Name/Dependencies), `:409-424` (Stop drains, `Close` releases the store = `core.Closer`); peers' `stopCtx`/`stopCancel` (`internal/module/peers/module.go:247-253`, `lifecycle.go:24-65`).
- Peers: `registryDir`, `liveness` (`internal/module/peers/module.go:192-193`), `Init` (`:306-362`, ends `return nil` at `:361`), `proxyPIDs()` and `findOriginEntry` (`titles.go:31-47`), `m.origin()` fails on a nil title store (`titles.go:96-98`) so it is not reused, host id read as `m.core.Cfg.HostID` under `CfgMu.RLock` (`:178-184`). `ipeers.Entry` (`internal/peers/registry.go:49-61`), `ReadRegistry(dir, live) (entries, skipped, err)` (`:234-237`; a missing dir is an empty registry, a non-dir is an error, `:258-265`), `ipeers.RefID` (`internal/peers/ref.go:114-124`). Test helpers reused by the resolver test (same package): `writeRegistryFixture`, `allLiveLiveness` (`internal/module/peers/module_test.go:43-57`), `fixture76973ProcStart` (`:40`), `targetProcStart` (`deliver_test.go:46`).
- Routes: Go 1.22+ method patterns with `{id}` and `r.PathValue` are the house style (`peers/module.go:365-381`, `hosts.go:434`). `/api/team/*` falls under the `general` chain = `TokenAuth` (`cmd/pdx/http_chain.go:26-35`; `internal/middleware/middleware.go:71-96`, an empty token lets everything pass). No shared JSON helper exists; hostconfig has a local one (`hostconfig/handler.go:19-25`).
- Registration: `cmd/pdx/main.go:341-376` (`registerServeModules`; `codexbroker.New()` at `:365`, the nex block `:367-375`), imports `:16-36`. Wiring tests that call it with a TempDir and `nil` stores: `monitor_module_test.go:15-29`, `nex_register_test.go:24-60, 66-82`, `removed_routes_test.go:33-86`; helpers `newTestCore` / `doRequest` (`http_chain_test.go:45-62`).
- Shutdown order (PD6): `cancel()` → `StopModules` → `srv.Shutdown` → `CloseModules` (`cmd/pdx/shutdown.go:161-187`), one `ShutdownBudget` of 10 s (`core.go:34`).
- Snapshot-send precedent: `internal/module/session/module.go:247-266` (`TrySend`, else `Events.Remove(sub)` unless `sub.Done()`).
- `cmd/pdx` must not import `internal/module/*` (`cmd/pdx/msg.go:26-28`); `internal/team` is the shared leaf.

**Review Focus mapping.** Focus 3 (restart while long-polling) → Task 2.4 (long-poll ends on `stopCtx`, create answers `503 not_ready`), Task 2.6 (boot lease grace). Focus 4 (decide racing sweeper and cancel) → Task 2.2 (CAS, raced three ways), Task 2.4 (`closeAs` broadcasts for the winner only), Task 2.5 (`TestTick_DecideSweeperAndCancelCloseOnce`: decide, tick and DELETE concurrently).

---

### Task 2.1: The wire contract — `internal/team/wire.go`

**Files:**
- Create: `internal/team/wire.go`
- Test: `internal/team/wire_test.go`

**Interfaces:**
- Produces: exactly the preamble's `package team` (constants `EventType`, `Kind*`, `State*`, `Err*`, limits; types `Origin`, `LeadPayload`, `Grant`, `Client`, `Approval`, `CreateApprovalRequest`, `DecideRequest`, `APIError`, `InflightResponse`, `EventValue`) plus
  ```go
  func (v EventValue) MarshalJSON() ([]byte, error) // op "snapshot" → {"op":"snapshot","approvals":[...]}, [] when empty; other ops keep the struct tags
  ```
- Consumes: `encoding/json` only. No import of `internal/module/*` (P2b imports this package from `cmd/pdx`).

- [ ] **Step 1: Write the failing test.**

```go
package team

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestEventValue_SnapshotEmitsEmptyArray(t *testing.T) {
	got, err := json.Marshal(EventValue{Op: "snapshot"})
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"op":"snapshot","approvals":[]}` {
		t.Fatalf("snapshot = %s", got)
	}
	got, err = json.Marshal(EventValue{Op: "closed", Approval: &Approval{ID: "a"}})
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(got, &m); err != nil {
		t.Fatal(err)
	}
	if _, has := m["approvals"]; has {
		t.Fatalf("closed must not carry approvals: %s", got)
	}
	if string(m["op"]) != `"closed"` || !json.Valid(m["approval"]) {
		t.Fatalf("closed = %s", got)
	}
}

func TestApproval_JSONKeysAndRoundTrip(t *testing.T) {
	payload, _ := json.Marshal(LeadPayload{Reason: "split the work", MaxMembers: 3, Roots: []string{"/w"}})
	in := Approval{
		ID: "11111111-1111-4111-8111-111111111111", Kind: KindLead, HostID: "h:1",
		Origin:  Origin{SessionID: "sid-1", Ref: "_abc123", Name: "n10", PID: 10, ProcStart: "Sun Sep 13 15:22:36 2026", Cwd: "/w", Tmux: "mt0:@1.%1", Title: "lead-team", Address: "mlab/n10"},
		Payload: payload, State: StateApproved, CreatedAt: 1000, DeadlineAt: 541000, LeaseUntil: 31000,
		DecidedBy: &Client{Kind: "app", Label: "Purdex.app @ air26", Addr: "100.64.0.4:5"}, DecidedAt: 2000,
		Grant: &Grant{MaxMembers: 2, Roots: []string{"/w"}},
	}
	raw, err := json.Marshal(in)
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
	want := []string{"created_at", "deadline_at", "decided_at", "decided_by", "grant", "host_id", "id", "kind", "lease_until", "origin", "payload", "state"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("keys = %v, want %v", got, want)
	}
	if !strings.Contains(string(keys["origin"]), `"title":"lead-team"`) || !strings.Contains(string(keys["origin"]), `"address":"mlab/n10"`) {
		t.Fatalf("origin must carry title and address: %s", keys["origin"])
	}
	if bare, _ := json.Marshal(Origin{SessionID: "x"}); strings.Contains(string(bare), `"title"`) || strings.Contains(string(bare), `"address"`) {
		t.Fatalf("empty title/address must be omitted: %s", bare)
	}
	var out Approval
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	// Payload is RawMessage: compare it as JSON, the rest structurally.
	var pIn, pOut LeadPayload
	_ = json.Unmarshal(in.Payload, &pIn)
	_ = json.Unmarshal(out.Payload, &pOut)
	in.Payload, out.Payload = nil, nil
	if !reflect.DeepEqual(in, out) || !reflect.DeepEqual(pIn, pOut) {
		t.Fatalf("round trip changed the value:\n in=%+v\nout=%+v", in, out)
	}
	open, _ := json.Marshal(Approval{State: StateOpen})
	keys = nil // Unmarshal into a non-nil map keeps old keys
	if err := json.Unmarshal(open, &keys); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"decided_by", "decided_at", "grant"} {
		if _, has := keys[k]; has {
			t.Fatalf("open approval must omit %s: %s", k, open)
		}
	}
}

// The restart confirm (spec §9.5) reads both counts; a zero must still be
// on the wire, so neither field is omitempty.
func TestInflightResponse_JSONKeys(t *testing.T) {
	got, err := json.Marshal(InflightResponse{ApprovalsOpen: 2})
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"approvals_open":2,"relays_active":0}` {
		t.Fatalf("inflight = %s, want both keys with relays_active 0", got)
	}
}
```

- [ ] **Step 2: Run it and see it fail.**
  - Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/lead-team && go test ./internal/team/ -v`
  - Expected: build failure —
    ```
    internal/team/wire_test.go:12:27: undefined: EventValue
    internal/team/wire_test.go:19:62: undefined: Approval
    internal/team/wire_test.go:36:29: undefined: LeadPayload
    ```
    (first lines of ten; the compiler stops after ten errors, so `undefined: InflightResponse` at line 94 is not printed until the earlier ones are fixed).

- [ ] **Step 3: Implement `wire.go`.** This is the preamble's contract verbatim (gofmt-aligned comments), plus the doc comment and `MarshalJSON`.

```go
// Package team is the wire contract of the lead/team feature (spec §6, §9):
// the approval request, its states and error codes, the request and
// response bodies of /api/team/*, and the approval.request host event. It
// is a leaf: cmd/pdx and the daemon module both import it, it imports
// nothing of theirs.
package team

import "encoding/json"

const EventType = "approval.request" // HostEvent.Type

type Kind string

const (
	KindLead      Kind = "lead"
	KindSelfRelay Kind = "self_relay" // accepted from P5a on; P2 answers 400 unsupported_kind
)

type State string

const (
	StateOpen      State = "open"
	StateApproved  State = "approved"
	StateDenied    State = "denied"
	StateTimeout   State = "timeout"   // U7: counts as a denial
	StateCancelled State = "cancelled" // requester DELETEd
	StateAbandoned State = "abandoned" // lease ran out, or origin session gone
)

// Error codes (APIError.Error)
const (
	ErrBadRequest       = "bad_request"
	ErrOriginUnknown    = "origin_unknown"
	ErrUnsupportedKind  = "unsupported_kind"
	ErrIDConflict       = "id_conflict"        // same id, different hash
	ErrRequestOpen      = "request_open"       // 409, carries the open Approval
	ErrAlreadyLead      = "already_lead"       // 409, enforced from P4 (needs the teams table)
	ErrMemberCannotLead = "member_cannot_lead" // 409, enforced from P4
	ErrAlreadyDecided   = "already_decided"    // 409, carries the closed Approval
	ErrNotFound         = "not_found"
	ErrNotReady         = "not_ready" // 503 while stopping
)

// Limits (spec §6.1, §6.2, §9.1)
const (
	DefaultMaxMembers = 3
	MaxMaxMembers     = 8
	DefaultWaitS      = 540 // 9 min
	MaxWaitS          = 600 // 10 min
	LeaseS            = 30
	MaxPollWaitS      = 25
	BootGraceS        = 30
)

// Origin is the requesting CC session, attributed by inbox (spec §6.2).
type Origin struct {
	SessionID string `json:"session_id"`
	Ref       string `json:"ref"`  // "_xxxxxx"
	Name      string `json:"name"` // registry name, may be ""
	PID       int    `json:"pid"`
	ProcStart string `json:"proc_start"`
	Cwd       string `json:"cwd"`
	Tmux      string `json:"tmux"`              // "<session>:@<win>.%<pane>" or ""
	Title     string `json:"title,omitempty"`   // the session's title (pdx msg name), "" when none
	Address   string `json:"address,omitempty"` // "<alias>/<name>" for a routable name, else "<alias>/_<ref>"
}

// LeadPayload is Approval.Payload for KindLead.
type LeadPayload struct {
	Reason     string   `json:"reason"`
	MaxMembers int      `json:"max_members"` // normalised: 0→3, cap 8
	Roots      []string `json:"roots"`       // normalised: absolute, Clean; default [origin.Cwd]
}

// Grant is what the user approved (edited in the dialog). P4 turns it into a team.
type Grant struct {
	MaxMembers int      `json:"max_members"`
	Roots      []string `json:"roots"`
}

// Client is the audit label of whoever decided (spec §6.5). Addr is set by the daemon from RemoteAddr.
type Client struct {
	Kind  string `json:"kind"`  // "app"
	Label string `json:"label"` // "Purdex.app @ air26"
	Addr  string `json:"addr,omitempty"`
}

type Approval struct {
	ID         string          `json:"id"`
	Kind       Kind            `json:"kind"`
	HostID     string          `json:"host_id"`
	Origin     Origin          `json:"origin"`
	Payload    json.RawMessage `json:"payload"` // LeadPayload for lead
	State      State           `json:"state"`
	CreatedAt  int64           `json:"created_at"`           // unix ms
	DeadlineAt int64           `json:"deadline_at"`          // unix ms, absolute
	LeaseUntil int64           `json:"lease_until"`          // unix ms
	DecidedBy  *Client         `json:"decided_by,omitempty"` // approved / denied only
	DecidedAt  int64           `json:"decided_at,omitempty"` // any close
	Grant      *Grant          `json:"grant,omitempty"`      // approved only
}

// CreateApprovalRequest is POST /api/team/approvals.
type CreateApprovalRequest struct {
	ID          string   `json:"id"` // UUID v4 from the CLI; idempotency key
	Kind        Kind     `json:"kind"`
	OriginInbox string   `json:"origin_inbox"` // CLAUDE_CODE_MESSAGING_SOCKET of the caller
	Reason      string   `json:"reason"`
	MaxMembers  int      `json:"max_members,omitempty"`
	Roots       []string `json:"roots,omitempty"`
	WaitS       int      `json:"wait_s,omitempty"` // 0→540, cap 600
}

// DecideRequest is POST /api/team/approvals/{id}/decide.
type DecideRequest struct {
	Decision string `json:"decision"`        // "approve" | "deny"
	Grant    *Grant `json:"grant,omitempty"` // approve only; nil → the payload's values
	Client   Client `json:"client"`
}

// APIError is every non-2xx body on /api/team/*.
type APIError struct {
	Error    string    `json:"error"`
	Detail   string    `json:"detail,omitempty"`
	Approval *Approval `json:"approval,omitempty"` // request_open, already_decided
}

// InflightResponse is GET /api/team/inflight (spec §9.5): what a restart of
// this daemon would interrupt. Neither field is omitempty — the restart
// confirm reads a zero too. RelaysActive is a literal 0 until P6.
type InflightResponse struct {
	ApprovalsOpen int `json:"approvals_open"`
	RelaysActive  int `json:"relays_active"`
}

// EventValue is HostEvent.Value (JSON string) for EventType.
//
//	{op:"opened", approval}            on create
//	{op:"closed", approval}            on every close
//	{op:"snapshot", approvals:[...]}   to each new subscriber (OnSubscribe); approvals is [] when empty, never null
type EventValue struct {
	Op        string     `json:"op"`
	Approval  *Approval  `json:"approval,omitempty"`
	Approvals []Approval `json:"approvals,omitempty"` // MarshalJSON emits [] for snapshot
}

// MarshalJSON keeps the struct tags' shape for opened/closed and makes a
// snapshot's approvals an explicit array: a nil slice would be dropped by
// omitempty (or printed as null without it), and the SPA replaces a host's
// whole set from the snapshot, so "no open requests" must arrive as [].
func (v EventValue) MarshalJSON() ([]byte, error) {
	type plain EventValue // no methods: avoids recursion
	if v.Op != "snapshot" {
		return json.Marshal(plain(v))
	}
	approvals := v.Approvals
	if approvals == nil {
		approvals = []Approval{}
	}
	return json.Marshal(struct {
		Op        string     `json:"op"`
		Approvals []Approval `json:"approvals"`
	}{Op: v.Op, Approvals: approvals})
}
```

- [ ] **Step 4: Run and see it pass.**
  - Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/lead-team && go test ./internal/team/ -v`
  - Expected:
    ```
    --- PASS: TestEventValue_SnapshotEmitsEmptyArray (0.00s)
    --- PASS: TestApproval_JSONKeysAndRoundTrip (0.00s)
    --- PASS: TestInflightResponse_JSONKeys (0.00s)
    ok  	github.com/wake/purdex/internal/team
    ```

- [ ] **Step 5: Commit.**
  ```bash
  git add internal/team/wire.go internal/team/wire_test.go
  git commit -m "feat(team): wire contract for approval requests (internal/team)

  Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
  ```

---

### Task 2.2: The store — `internal/module/team/store.go`

**Files:**
- Create: `internal/module/team/store.go`
- Test: `internal/module/team/store_test.go`

**Interfaces:**
- Produces (package `teammod`):
  ```go
  var ErrNoSuchApproval = errors.New("no such approval")
  type Store struct { db *sql.DB }
  func OpenStore(path string) (*Store, error)            // ":memory:" for tests; WAL + busy_timeout(5000) otherwise
  func (s *Store) Close() error
  func (s *Store) Get(id string) (team.Approval, bool, error)
  func (s *Store) Create(a team.Approval, hash string) (stored team.Approval, storedHash string, inserted bool, err error) // INSERT … ON CONFLICT(id) DO NOTHING
  type Close struct { State team.State; DecidedAt int64; DecidedBy *team.Client; Grant *team.Grant }
  func (s *Store) CloseIfOpen(id string, c Close) (after team.Approval, won bool, err error)  // CAS: UPDATE … WHERE id=? AND state='open'
  func (s *Store) RenewLease(id string, until int64) error                 // MAX(lease_until, ?) while open
  func (s *Store) ExtendOpenLeases(until int64) (int64, error)              // boot grace; rows changed
  func (s *Store) ListOpen() ([]team.Approval, error)                       // ORDER BY created_at, id; never nil
  func (s *Store) OpenByOrigin(sessionID string, kind team.Kind) (team.Approval, bool, error)
  ```
- Consumes: `internal/team`, `modernc.org/sqlite`.
- Table `approval_requests(id PK, kind, host_id, origin_session_id, origin_json, payload_json, request_hash, state, created_at, deadline_at, lease_until, decided_by_json, decided_at, grant_json)` + index `(state, created_at)`. Times are unix ms.

- [ ] **Step 1: Write the failing test.**

```go
package teammod

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	"github.com/wake/purdex/internal/team"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := OpenStore(filepath.Join(t.TempDir(), "team.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func openApproval(id, sid string, createdAt int64) team.Approval {
	payload, _ := json.Marshal(team.LeadPayload{Reason: "r", MaxMembers: 3, Roots: []string{"/w"}})
	return team.Approval{
		ID: id, Kind: team.KindLead, HostID: "h:1",
		Origin:  team.Origin{SessionID: sid, Ref: "_abc123", PID: 10, Cwd: "/w"},
		Payload: payload, State: team.StateOpen,
		CreatedAt: createdAt, DeadlineAt: createdAt + 540_000, LeaseUntil: createdAt + 30_000,
	}
}

func TestStore_CreateIsIdempotentAndReportsHashMismatch(t *testing.T) {
	s := openTestStore(t)
	a := openApproval("id-1", "sid-1", 1000)
	stored, hash, inserted, err := s.Create(a, "h1")
	if err != nil || !inserted || hash != "h1" || stored.State != team.StateOpen || stored.LeaseUntil != 31000 {
		t.Fatalf("first create: stored=%+v hash=%q inserted=%v err=%v", stored, hash, inserted, err)
	}
	retry := a
	retry.LeaseUntil = 99 // must NOT overwrite the stored row
	stored2, hash2, inserted2, err := s.Create(retry, "h1")
	if err != nil || inserted2 || hash2 != "h1" || stored2.LeaseUntil != 31000 {
		t.Fatalf("retry: stored=%+v hash=%q inserted=%v err=%v", stored2, hash2, inserted2, err)
	}
	_, hash3, inserted3, err := s.Create(a, "h2") // same id, different request
	if err != nil || inserted3 || hash3 != "h1" {
		t.Fatalf("conflict: hash=%q inserted=%v err=%v (caller compares h2 != h1 → id_conflict)", hash3, inserted3, err)
	}
	got, ok, err := s.Get("id-1")
	if err != nil || !ok || got.Origin.SessionID != "sid-1" || string(got.Payload) != string(a.Payload) {
		t.Fatalf("get: %+v ok=%v err=%v", got, ok, err)
	}
	if _, ok, err := s.Get("nope"); err != nil || ok {
		t.Fatalf("get unknown: ok=%v err=%v", ok, err)
	}
}

func TestStore_CloseIfOpenExactlyOneWinner(t *testing.T) {
	s := openTestStore(t)
	if _, _, _, err := s.Create(openApproval("id-1", "sid-1", 1000), "h1"); err != nil {
		t.Fatal(err)
	}
	const n = 16
	var wg sync.WaitGroup
	wins := make(chan team.State, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// The three ways a request closes under contention (spec §15):
			// a decision, the sweeper's timeout, the requester's cancel.
			var c Close
			switch i % 3 {
			case 0:
				c = Close{State: team.StateTimeout, DecidedAt: int64(2000 + i)}
			case 1:
				c = Close{State: team.StateDenied, DecidedAt: int64(2000 + i), DecidedBy: &team.Client{Kind: "app", Label: fmt.Sprintf("app-%d", i)}}
			case 2:
				c = Close{State: team.StateCancelled, DecidedAt: int64(2000 + i)}
			}
			after, won, err := s.CloseIfOpen("id-1", c)
			if err != nil {
				t.Errorf("close %d: %v", i, err)
				return
			}
			if after.State == team.StateOpen {
				t.Errorf("close %d: row still open after the attempt", i)
			}
			if won {
				wins <- after.State
			}
		}(i)
	}
	wg.Wait()
	close(wins)
	var winners []team.State
	for st := range wins {
		winners = append(winners, st)
	}
	if len(winners) != 1 {
		t.Fatalf("winners = %v, want exactly one", winners)
	}
	final, _, _ := s.Get("id-1")
	if final.State != winners[0] {
		t.Fatalf("final state %s, winner %s", final.State, winners[0])
	}
	if final.State == team.StateDenied && (final.DecidedBy == nil || final.DecidedBy.Kind != "app") {
		t.Fatalf("denied without decided_by: %+v", final)
	}
	if final.State != team.StateDenied && final.DecidedBy != nil {
		t.Fatalf("%s must not carry decided_by: %+v", final.State, final)
	}
	if _, _, err := s.CloseIfOpen("nope", Close{State: team.StateCancelled}); err != ErrNoSuchApproval {
		t.Fatalf("unknown id: err=%v, want ErrNoSuchApproval", err)
	}
}

func TestStore_LeasesAndListing(t *testing.T) {
	s := openTestStore(t)
	for _, a := range []team.Approval{openApproval("b", "sid-2", 2000), openApproval("a", "sid-1", 1000), openApproval("c", "sid-1", 1000)} {
		if _, _, _, err := s.Create(a, "h"); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.RenewLease("a", 50_000); err != nil {
		t.Fatal(err)
	}
	if err := s.RenewLease("a", 40_000); err != nil { // never backwards
		t.Fatal(err)
	}
	if a, _, _ := s.Get("a"); a.LeaseUntil != 50_000 {
		t.Fatalf("lease after renew = %d, want 50000", a.LeaseUntil)
	}
	n, err := s.ExtendOpenLeases(45_000) // boot grace: a stays at 50000, b and c move up
	if err != nil || n != 2 {
		t.Fatalf("extend: n=%d err=%v", n, err)
	}
	if b, _, _ := s.Get("b"); b.LeaseUntil != 45_000 {
		t.Fatalf("b lease = %d", b.LeaseUntil)
	}
	open, err := s.ListOpen()
	if err != nil {
		t.Fatal(err)
	}
	if len(open) != 3 || open[0].ID != "a" || open[1].ID != "c" || open[2].ID != "b" {
		t.Fatalf("ListOpen order = %v, want a c b (created_at, id)", open)
	}
	if _, _, err := s.CloseIfOpen("a", Close{State: team.StateCancelled, DecidedAt: 3000}); err != nil {
		t.Fatal(err)
	}
	if err := s.RenewLease("a", 99_000); err != nil { // closed: ignored
		t.Fatal(err)
	}
	if a, _, _ := s.Get("a"); a.LeaseUntil != 50_000 || a.DecidedAt != 3000 {
		t.Fatalf("closed row changed: %+v", a)
	}
	got, ok, err := s.OpenByOrigin("sid-1", team.KindLead)
	if err != nil || !ok || got.ID != "c" {
		t.Fatalf("OpenByOrigin sid-1 = %+v ok=%v err=%v, want c", got, ok, err)
	}
	if _, ok, _ := s.OpenByOrigin("sid-1", team.KindSelfRelay); ok {
		t.Fatal("OpenByOrigin must filter by kind")
	}
	if open, _ := s.ListOpen(); len(open) != 2 {
		t.Fatalf("ListOpen after close = %d rows", len(open))
	}
}
```

- [ ] **Step 2: Run it and see it fail.**
  - Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/lead-team && go test ./internal/module/team/ -run TestStore -v`
  - Expected: build failure —
    ```
    internal/module/team/store_test.go:13:35: undefined: Store
    internal/module/team/store_test.go:15:12: undefined: OpenStore
    internal/module/team/store_test.go:71:9: undefined: Close
    ```

- [ ] **Step 3: Implement `store.go`.**

```go
// Package teammod is the daemon's team module (spec §6, §9): it owns
// team.db and the approval_requests table — one state machine for every
// approval kind, closed by compare-and-set — and serves /api/team/*.
package teammod

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	_ "modernc.org/sqlite"

	"github.com/wake/purdex/internal/team"
)

// ErrNoSuchApproval is returned by the per-id methods for an unknown id.
var ErrNoSuchApproval = errors.New("no such approval")

// Store is the SQLite persistence of approval requests.
type Store struct {
	db *sql.DB
}

// OpenStore opens (or creates) team.db at path. ":memory:" is for tests.
func OpenStore(path string) (*Store, error) {
	dsn := path
	if path != ":memory:" {
		// busy_timeout: a concurrent writer waits instead of failing with SQLITE_BUSY.
		dsn = path + "?_pragma=journal_mode(wal)&_pragma=busy_timeout(5000)"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open team db: %w", err)
	}
	if path == ":memory:" {
		db.SetMaxOpenConns(1)
	}
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS approval_requests (
			id                TEXT PRIMARY KEY,
			kind              TEXT    NOT NULL,
			host_id           TEXT    NOT NULL,
			origin_session_id TEXT    NOT NULL,
			origin_json       TEXT    NOT NULL,
			payload_json      TEXT    NOT NULL,
			request_hash      TEXT    NOT NULL,
			state             TEXT    NOT NULL,
			created_at        INTEGER NOT NULL,
			deadline_at       INTEGER NOT NULL,
			lease_until       INTEGER NOT NULL,
			decided_by_json   TEXT,
			decided_at        INTEGER NOT NULL DEFAULT 0,
			grant_json        TEXT
		);
		CREATE INDEX IF NOT EXISTS approval_requests_state_created
			ON approval_requests (state, created_at);`); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate team db: %w", err)
	}
	return &Store{db: db}, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

const selectCols = `id, kind, host_id, origin_json, payload_json, request_hash, state,
	created_at, deadline_at, lease_until, decided_by_json, decided_at, grant_json`

type rowScanner interface{ Scan(dest ...any) error }

// scanRow decodes one approval_requests row and its request hash.
func scanRow(r rowScanner) (team.Approval, string, error) {
	var a team.Approval
	var hash, originJSON, payloadJSON string
	var decidedBy, grant sql.NullString
	if err := r.Scan(&a.ID, &a.Kind, &a.HostID, &originJSON, &payloadJSON, &hash, &a.State,
		&a.CreatedAt, &a.DeadlineAt, &a.LeaseUntil, &decidedBy, &a.DecidedAt, &grant); err != nil {
		return team.Approval{}, "", err
	}
	if err := json.Unmarshal([]byte(originJSON), &a.Origin); err != nil {
		return team.Approval{}, "", fmt.Errorf("decode origin of %s: %w", a.ID, err)
	}
	a.Payload = json.RawMessage(payloadJSON)
	if decidedBy.Valid {
		a.DecidedBy = new(team.Client)
		if err := json.Unmarshal([]byte(decidedBy.String), a.DecidedBy); err != nil {
			return team.Approval{}, "", fmt.Errorf("decode decided_by of %s: %w", a.ID, err)
		}
	}
	if grant.Valid {
		a.Grant = new(team.Grant)
		if err := json.Unmarshal([]byte(grant.String), a.Grant); err != nil {
			return team.Approval{}, "", fmt.Errorf("decode grant of %s: %w", a.ID, err)
		}
	}
	return a, hash, nil
}

// getRow reads one row with its hash; ErrNoSuchApproval when absent.
func (s *Store) getRow(id string) (team.Approval, string, error) {
	a, hash, err := scanRow(s.db.QueryRow(`SELECT `+selectCols+` FROM approval_requests WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return team.Approval{}, "", ErrNoSuchApproval
	}
	if err != nil {
		return team.Approval{}, "", fmt.Errorf("get approval %s: %w", id, err)
	}
	return a, hash, nil
}

// Get returns the approval with id; ok is false when there is none.
func (s *Store) Get(id string) (team.Approval, bool, error) {
	a, _, err := s.getRow(id)
	if errors.Is(err, ErrNoSuchApproval) {
		return team.Approval{}, false, nil
	}
	return a, err == nil, err
}

// Create inserts a (state open) if its id is new and returns it with
// inserted=true. When the id exists it inserts nothing and returns the
// stored row and the hash it was stored with, so the caller can tell an
// idempotent retry (same hash) from a conflicting reuse of the id.
func (s *Store) Create(a team.Approval, hash string) (stored team.Approval, storedHash string, inserted bool, err error) {
	originJSON, err := json.Marshal(a.Origin)
	if err != nil {
		return team.Approval{}, "", false, fmt.Errorf("encode origin: %w", err)
	}
	res, err := s.db.Exec(`
		INSERT INTO approval_requests
			(id, kind, host_id, origin_session_id, origin_json, payload_json, request_hash, state, created_at, deadline_at, lease_until)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO NOTHING`,
		a.ID, string(a.Kind), a.HostID, a.Origin.SessionID, string(originJSON), string(a.Payload), hash,
		string(team.StateOpen), a.CreatedAt, a.DeadlineAt, a.LeaseUntil)
	if err != nil {
		return team.Approval{}, "", false, fmt.Errorf("insert approval %s: %w", a.ID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return team.Approval{}, "", false, fmt.Errorf("insert approval %s rows affected: %w", a.ID, err)
	}
	stored, storedHash, err = s.getRow(a.ID)
	if err != nil {
		return team.Approval{}, "", false, err
	}
	return stored, storedHash, n == 1, nil
}

// Close is how a request leaves the open state. DecidedBy is set for
// approved/denied only, Grant for approved only.
type Close struct {
	State     team.State
	DecidedAt int64
	DecidedBy *team.Client
	Grant     *team.Grant
}

// CloseIfOpen is the compare-and-set every close goes through: the UPDATE
// is guarded by state='open', so of any number of concurrent closes
// exactly one sees RowsAffected()==1 and won. It returns the row as it is
// after the attempt (the winner's close, for a loser too) and
// ErrNoSuchApproval for an unknown id.
func (s *Store) CloseIfOpen(id string, c Close) (team.Approval, bool, error) {
	var decidedBy, grant any // NULL unless set
	if c.DecidedBy != nil {
		b, err := json.Marshal(c.DecidedBy)
		if err != nil {
			return team.Approval{}, false, fmt.Errorf("encode decided_by: %w", err)
		}
		decidedBy = string(b)
	}
	if c.Grant != nil {
		b, err := json.Marshal(c.Grant)
		if err != nil {
			return team.Approval{}, false, fmt.Errorf("encode grant: %w", err)
		}
		grant = string(b)
	}
	res, err := s.db.Exec(`
		UPDATE approval_requests
		SET state = ?, decided_at = ?, decided_by_json = ?, grant_json = ?
		WHERE id = ? AND state = 'open'`,
		string(c.State), c.DecidedAt, decidedBy, grant, id)
	if err != nil {
		return team.Approval{}, false, fmt.Errorf("close approval %s: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return team.Approval{}, false, fmt.Errorf("close approval %s rows affected: %w", id, err)
	}
	a, _, err := s.getRow(id)
	if err != nil {
		return team.Approval{}, false, err
	}
	return a, n == 1, nil
}

// RenewLease moves an open request's lease forward to until (never back).
// A closed or unknown id is left alone and is not an error.
func (s *Store) RenewLease(id string, until int64) error {
	if _, err := s.db.Exec(`
		UPDATE approval_requests SET lease_until = MAX(lease_until, ?)
		WHERE id = ? AND state = 'open'`, until, id); err != nil {
		return fmt.Errorf("renew lease %s: %w", id, err)
	}
	return nil
}

// ExtendOpenLeases is the boot grace (spec §9.2): every open request's
// lease becomes max(lease_until, until). It returns how many rows changed.
func (s *Store) ExtendOpenLeases(until int64) (int64, error) {
	res, err := s.db.Exec(`
		UPDATE approval_requests SET lease_until = ?
		WHERE state = 'open' AND lease_until < ?`, until, until)
	if err != nil {
		return 0, fmt.Errorf("extend open leases: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("extend open leases rows affected: %w", err)
	}
	return n, nil
}

// ListOpen returns every open request, oldest first (created_at, id).
func (s *Store) ListOpen() ([]team.Approval, error) {
	rows, err := s.db.Query(`SELECT ` + selectCols + ` FROM approval_requests WHERE state = 'open' ORDER BY created_at, id`)
	if err != nil {
		return nil, fmt.Errorf("list open approvals: %w", err)
	}
	defer rows.Close()
	out := []team.Approval{}
	for rows.Next() {
		a, _, err := scanRow(rows)
		if err != nil {
			return nil, fmt.Errorf("list open approvals: %w", err)
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list open approvals: %w", err)
	}
	return out, nil
}

// OpenByOrigin returns the open request of kind for the origin session, if any.
func (s *Store) OpenByOrigin(sessionID string, kind team.Kind) (team.Approval, bool, error) {
	a, _, err := scanRow(s.db.QueryRow(`SELECT `+selectCols+` FROM approval_requests
		WHERE origin_session_id = ? AND kind = ? AND state = 'open' ORDER BY created_at, id LIMIT 1`, sessionID, string(kind)))
	if errors.Is(err, sql.ErrNoRows) {
		return team.Approval{}, false, nil
	}
	if err != nil {
		return team.Approval{}, false, fmt.Errorf("open approval by origin %s: %w", sessionID, err)
	}
	return a, true, nil
}
```

- [ ] **Step 4: Run and see it pass (with the race detector — the CAS test is 16 goroutines on a file-backed WAL database).**
  - Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/lead-team && go test ./internal/module/team/ -run TestStore -race -count=1 -v`
  - Expected:
    ```
    --- PASS: TestStore_CreateIsIdempotentAndReportsHashMismatch (0.01s)
    --- PASS: TestStore_CloseIfOpenExactlyOneWinner (0.02s)
    --- PASS: TestStore_LeasesAndListing (0.01s)
    ok  	github.com/wake/purdex/internal/module/team
    ```
  - Mutation check (spec §15 "dropping the CAS lets two decisions win → red"): change the UPDATE's `AND state = 'open'` to nothing and rerun — `TestStore_CloseIfOpenExactlyOneWinner` fails with `winners = [... ...], want exactly one`. Put it back. The 16 goroutines cycle through timeout / denied / cancelled, so the mutation is caught whichever pair of close kinds lands together.

- [ ] **Step 5: Commit.**
  ```bash
  git add internal/module/team/store.go internal/module/team/store_test.go
  git commit -m "feat(team): approval_requests store in team.db with CAS close

  Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
  ```

---

### Task 2.3: The peers origin resolver — `internal/module/peers/origin_resolver.go`

**Files:**
- Create: `internal/module/peers/origin_resolver.go`
- Modify: `internal/module/peers/module.go:361` (register the resolver at the end of `Init`, before `return nil`)
- Test: `internal/module/peers/origin_resolver_test.go`

**Interfaces:**
- Produces (package `peers`):
  ```go
  const OriginResolverKey = "peers.origin-resolver"            // registry key, set in Init
  type OriginResolver struct{ m *Module }                       // exported view over the module's registry dir, liveness and proxy pids
  func (r *OriginResolver) ResolveOrigin(inbox string) (team.Origin, bool)
  func (r *OriginResolver) LiveSession(sessionID string) bool    // true on a registry read error (unknown ≠ dead, PD5)
  ```
- Consumes: `ipeers.ReadRegistry(m.registryDir, m.liveness)`, `findOriginEntry(entries, m.proxyPIDs(), inbox)` (`titles.go:31`), `ipeers.RefID`, `internal/team.Origin`. The team module type-asserts the registry value to its own `OriginResolver` interface (Task 2.4), so peers never imports `internal/module/team`.

- [ ] **Step 1: Write the failing test.** (`writeRegistryFixture`, `allLiveLiveness`, `fixture76973ProcStart` and `targetProcStart` already exist in this test package.)

```go
package peers

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	iagent "github.com/wake/purdex/internal/agent"
	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/store"
)

// resolverFixture is a registry dir with two live entries (pid 10 in tmux,
// pid 20 without) and a bare module pointed at it; liveness is a fake, so
// nothing forks ps.
func resolverFixture(t *testing.T, live ipeers.Liveness) (*OriginResolver, string) {
	t.Helper()
	dir := t.TempDir()
	writeRegistryFixture(t, dir, "10.json", `{"pid":10,"sessionId":"sid-1","cwd":"/w","procStart":"`+targetProcStart+`","version":"2.1.270","tmux":"mt0:@1.%1","messagingSocketPath":"`+dir+`/10.sock","name":"n10","status":"idle"}`)
	writeRegistryFixture(t, dir, "20.json", `{"pid":20,"sessionId":"sid-2","cwd":"/w2","procStart":"`+targetProcStart+`","version":"2.1.270","messagingSocketPath":"`+dir+`/20.sock","name":"","status":"idle"}`)
	m := &Module{
		core:        newTestCore(t, "mlab:abc123", "mlab"), // alias "mlab" for Address
		registryDir: dir,
		liveness:    live,
		titles:      fakeTitles{"sid-1": "lead-team"},
		logf:        func(string, ...any) {},
	}
	return &OriginResolver{m: m}, dir
}

// fakeTitles is a TitleStore whose Snapshot lists one label per entry;
// Claim and Release are never reached by the resolver.
type fakeTitles map[string]string

func (f fakeTitles) Snapshot() ([]store.PeerLabel, error) {
	out := make([]store.PeerLabel, 0, len(f))
	for sid, label := range f {
		out = append(out, store.PeerLabel{SessionID: sid, Label: label, Rev: 1})
	}
	return out, nil
}
func (fakeTitles) Claim(string, string, time.Time) (store.PeerLabel, error) {
	return store.PeerLabel{}, errors.New("not used")
}
func (fakeTitles) Release(string, time.Time) (store.PeerLabel, bool, error) {
	return store.PeerLabel{}, false, errors.New("not used")
}

func TestOriginResolver_ResolveOrigin(t *testing.T) {
	r, dir := resolverFixture(t, allLiveLiveness(fixture76973ProcStart))
	o, ok := r.ResolveOrigin(dir + "/10.sock")
	if !ok {
		t.Fatal("pid 10 must resolve")
	}
	if o.SessionID != "sid-1" || o.Ref != ipeers.RefID("sid-1") || o.Name != "n10" || o.PID != 10 ||
		o.ProcStart != targetProcStart || o.Cwd != "/w" || o.Tmux != "mt0:@1.%1" {
		t.Fatalf("origin = %+v", o)
	}
	if o.Title != "lead-team" || o.Address != "mlab/n10" {
		t.Fatalf("title/address = %q/%q, want lead-team / mlab/n10", o.Title, o.Address)
	}
	if o2, ok := r.ResolveOrigin(dir + "/20.sock"); !ok || o2.Tmux != "" || o2.Name != "" || o2.Cwd != "/w2" ||
		o2.Title != "" || o2.Address != "mlab/"+ipeers.RefID("sid-2") {
		t.Fatalf("pid 20 = %+v ok=%v (no title; address falls back to the ref)", o2, ok)
	}
	r.m.titles = nil
	if o3, ok := r.ResolveOrigin(dir + "/10.sock"); !ok || o3.Title != "" || o3.Address != "mlab/n10" {
		t.Fatalf("nil title store must give an empty title, not a panic: %+v ok=%v", o3, ok)
	}
	r.m.titles = failingTitles{}
	if o4, ok := r.ResolveOrigin(dir + "/10.sock"); !ok || o4.Title != "" {
		t.Fatalf("a failing title store must give an empty title: %+v ok=%v", o4, ok)
	}
	if _, ok := r.ResolveOrigin(""); ok {
		t.Fatal("empty inbox must not resolve")
	}
	if _, ok := r.ResolveOrigin(dir + "/99.sock"); ok {
		t.Fatal("unknown inbox must not resolve")
	}
}

func TestOriginResolver_DeadAndProxyEntriesDoNotResolve(t *testing.T) {
	live := allLiveLiveness(fixture76973ProcStart)
	live.PidAlive = func(pid int) bool { return pid != 10 }
	r, dir := resolverFixture(t, live)
	if _, ok := r.ResolveOrigin(dir + "/10.sock"); ok {
		t.Fatal("a dead pid must not resolve")
	}
	if !r.LiveSession("sid-2") || r.LiveSession("sid-1") || r.LiveSession("") {
		t.Fatal("LiveSession must follow registry liveness")
	}

	// A peer-proxy helper (D9 classification through Liveness.Info) is not a session.
	proxy := allLiveLiveness(fixture76973ProcStart)
	proxy.Info = func(pid int) (iagent.ProcessInfo, error) {
		argv := []string{"claude"}
		if pid == 20 {
			argv = []string{"pdx", "peer-proxy"}
		}
		return iagent.ProcessInfo{PID: pid, Argv: argv, StartTime: fixture76973ProcStart}, nil
	}
	r2, dir2 := resolverFixture(t, proxy)
	if _, ok := r2.ResolveOrigin(dir2 + "/20.sock"); ok {
		t.Fatal("a proxy helper must not resolve as an origin")
	}
	if r2.LiveSession("sid-2") || !r2.LiveSession("sid-1") {
		t.Fatal("a proxy helper's session id must not count as live")
	}
}

func TestOriginResolver_RegistryReadErrorIsUnknownNotDead(t *testing.T) {
	r, _ := resolverFixture(t, allLiveLiveness(fixture76973ProcStart))
	// A regular file where the dir should be makes ReadRegistry fail
	// (a missing dir would read as an empty registry instead).
	file := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	r.m.registryDir = file
	if !r.LiveSession("sid-1") {
		t.Fatal("a registry read error must not report the session dead")
	}
	if _, ok := r.ResolveOrigin("/any.sock"); ok {
		t.Fatal("a registry read error cannot attribute an origin")
	}
}
```

- [ ] **Step 2: Run it and see it fail.**
  - Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/lead-team && go test ./internal/module/peers/ -run TestOriginResolver -v`
  - Expected: build failure —
    ```
    internal/module/peers/origin_resolver_test.go:15:60: undefined: OriginResolver
    internal/module/peers/origin_resolver_test.go:21:10: undefined: OriginResolver
    ```

- [ ] **Step 3: Implement.** Create `origin_resolver.go`:

```go
package peers

import (
	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/team"
)

// OriginResolverKey is the service-registry key under which Init publishes
// the module's *OriginResolver, for the team module (lead-team spec §6.2:
// "the existing findOrigin attributes it by inbox").
const OriginResolverKey = "peers.origin-resolver"

// OriginResolver attributes an approval request's caller to a live Claude
// Code session from the registry. It is a thin view over the peers module
// — the same registry dir, liveness and proxy-pid set GET /api/peers uses
// — and deliberately not m.origin(): that one is a self-route verb that
// fails when the title store is nil.
type OriginResolver struct{ m *Module }

// ResolveOrigin returns the live, non-proxy registry entry whose inbox is
// inbox, as a team.Origin. ok is false for an empty inbox, an unknown or
// dead one, a proxy helper, and a registry read error (logged): none of
// those can be attributed to a session.
func (r *OriginResolver) ResolveOrigin(inbox string) (team.Origin, bool) {
	if inbox == "" {
		return team.Origin{}, false
	}
	entries, _, err := ipeers.ReadRegistry(r.m.registryDir, r.m.liveness)
	if err != nil {
		r.m.logf("peers: origin resolver: read registry: %v", err)
		return team.Origin{}, false
	}
	e, found := findOriginEntry(entries, r.m.proxyPIDs(), inbox)
	if !found {
		return team.Origin{}, false
	}
	ref := ipeers.RefID(e.SessionID)
	alias := r.m.configSnapshot().alias
	addr := alias + "/" + ref
	if ipeers.RoutableName(e.Name) { // the rule GET /api/peers uses (internal/peers/record.go:294-297)
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
	}, true
}

// titleOf is the session's title from the title store, or "" when there is
// no store, the read fails, or the session has none. The dialog shows the
// title or falls back to the name, so a missing title is never an error.
func (r *OriginResolver) titleOf(sessionID string) string {
	if r.m.titles == nil {
		return ""
	}
	rows, err := r.m.titles.Snapshot()
	if err != nil {
		return ""
	}
	for _, row := range rows {
		if row.SessionID == sessionID {
			return row.Label
		}
	}
	return ""
}

// LiveSession reports whether a live, non-proxy registry entry has this
// CC session id. A registry read error answers true: "unknown" must never
// abandon an open request (PD5), only a registry that was read and does
// not list the session does.
func (r *OriginResolver) LiveSession(sessionID string) bool {
	if sessionID == "" {
		return false
	}
	entries, _, err := ipeers.ReadRegistry(r.m.registryDir, r.m.liveness)
	if err != nil {
		r.m.logf("peers: origin resolver: read registry: %v", err)
		return true
	}
	proxies := r.m.proxyPIDs()
	for _, e := range entries {
		if e.SessionID == sessionID && !e.IsProxy && !proxies[e.PID] {
			return true
		}
	}
	return false
}
```

  Then in `internal/module/peers/module.go`, `Init` currently ends (lines 356-362):
  ```go
  		OnFrame: m.handleReplyFrame,
  		Log:     m.logf,
  	})

  	return nil
  }
  ```
  Insert two lines before that `return nil`, so it reads:
  ```go
  		OnFrame: m.handleReplyFrame,
  		Log:     m.logf,
  	})

  	// The team module attributes approval requests through this view.
  	c.Registry.Register(OriginResolverKey, &OriginResolver{m: m})

  	return nil
  }
  ```

- [ ] **Step 4: Run and see it pass — the new tests and the whole peers package (its `Init` changed).**
  - Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/lead-team && go test ./internal/module/peers/ -run TestOriginResolver -count=1 -v`
  - Expected:
    ```
    --- PASS: TestOriginResolver_ResolveOrigin (0.00s)
    --- PASS: TestOriginResolver_DeadAndProxyEntriesDoNotResolve (0.00s)
    --- PASS: TestOriginResolver_RegistryReadErrorIsUnknownNotDead (0.00s)
    ok  	github.com/wake/purdex/internal/module/peers
    ```
  - Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/lead-team && go test ./internal/module/peers/ -count=1`
  - Expected: `ok  	github.com/wake/purdex/internal/module/peers	15.5s` (measured 15.458 s).

- [ ] **Step 5: Commit.**
  ```bash
  git add internal/module/peers/origin_resolver.go internal/module/peers/origin_resolver_test.go internal/module/peers/module.go
  git commit -m "feat(peers): export an origin resolver for the team module

  Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
  ```

---

### Task 2.4: The module skeleton and the six routes

**Files:**
- Create: `internal/module/team/module.go`, `internal/module/team/handler.go`
- Test: `internal/module/team/handler_test.go`

**Interfaces:**
- Produces (package `teammod`):
  ```go
  type OriginResolver interface {
  	ResolveOrigin(inbox string) (team.Origin, bool)
  	LiveSession(sessionID string) bool
  }
  type Module struct { /* core, store, origins, now func() int64, logf, stopCtx/stopCancel, sweepWG, tickN, createMu, mu, waiters map[string][]chan struct{} */ }
  func New() *Module
  func (m *Module) Name() string           // "team"
  func (m *Module) Dependencies() []string // {"peers"}
  func (m *Module) Init(c *core.Core) error // resolver from c.Registry.Get(peersmod.OriginResolverKey); OpenStore(<DataDir>/team.db)
  func (m *Module) RegisterRoutes(mux *http.ServeMux)
  func (m *Module) Start(context.Context) error // stub here; Task 2.6
  func (m *Module) Stop(context.Context) error  // stopCancel(); sweepWG.Wait()
  func (m *Module) Close() error                // store.Close() — core.Closer, PD6
  func (m *Module) closeAs(id string, c Close) (team.Approval, bool, error) // CAS, then for the winner only: broadcast "closed" + wake long-polls
  ```
  Routes (Go method patterns): `POST /api/team/approvals`, `GET /api/team/approvals`, `GET /api/team/approvals/{id}`, `DELETE /api/team/approvals/{id}`, `POST /api/team/approvals/{id}/decide`, and `GET /api/team/inflight` → `200 team.InflightResponse{ApprovalsOpen: len(ListOpen()), RelaysActive: 0}` (spec §9.5; `relays_active` is a literal 0 until P6). Non-2xx bodies are `team.APIError`; a `team.db` failure is `500 {"error":"storage_error"}` (a code outside the wire constants; clients treat it as a plain error).
- Consumes: `Store` (Task 2.2), `peersmod.OriginResolverKey` (Task 2.3), `core.Events.BroadcastEvent`, `core.HostEvent`, `r.PathValue`.
- Behaviour pinned by the tests: idempotency hash = `sha256(kind \0 origin_session \0 wait_s \0 payload_json)`; normalisation `max_members 0→3 cap 8`, `wait_s 0→540 cap 600`, roots `Clean`ed and absolutised against `origin.Cwd`, default `[origin.Cwd]`; `reason` required; one open lead request per origin session (`409 request_open` carrying it, serialised by `createMu`); long-poll registers its waiter **before** reading the row, renews the lease on every GET by id, and selects on waiter / timer (≤ 25 s) / `r.Context()` / `stopCtx`, answering the row as it then is; `DELETE` answers the row as it now is (cancelled, or closed before as it was); `decide` stamps `client.addr = RemoteAddr`, builds the grant from the payload when `grant` is nil, and a late decide gets `409 already_decided` carrying the winner; `POST` create answers `503 not_ready` once `Stop` ran.

- [ ] **Step 1: Write the failing test.**

```go
package teammod

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/core"
	peersmod "github.com/wake/purdex/internal/module/peers"
	"github.com/wake/purdex/internal/team"
)

// fakeOrigins is the OriginResolver of these tests: inbox "/tmp/10.sock" is
// session sid-1 (cwd /w, in tmux), "/tmp/20.sock" is sid-2; dead marks a
// session gone for LiveSession.
type fakeOrigins struct {
	mu   sync.Mutex
	dead map[string]bool
}

var fixtureOrigins = map[string]team.Origin{
	"/tmp/10.sock": {SessionID: "sid-1", Ref: "_abc123", Name: "n10", PID: 10, ProcStart: "Sun Sep 13 15:22:36 2026", Cwd: "/w", Tmux: "mt0:@1.%1"},
	"/tmp/20.sock": {SessionID: "sid-2", Ref: "_def456", PID: 20, ProcStart: "Sun Sep 13 15:22:36 2026", Cwd: "/w2"},
}

func (f *fakeOrigins) ResolveOrigin(inbox string) (team.Origin, bool) {
	o, ok := fixtureOrigins[inbox]
	return o, ok
}

func (f *fakeOrigins) LiveSession(sid string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return !f.dead[sid]
}

func (f *fakeOrigins) markDead(sid string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.dead == nil {
		f.dead = map[string]bool{}
	}
	f.dead[sid] = true
}

type fixture struct {
	t       *testing.T
	m       *Module
	mux     *http.ServeMux
	core    *core.Core
	clock   atomic.Int64 // unix ms
	origins *fakeOrigins
	sub     *core.EventSubscriber
}

// newFixture builds the module through Init (a fake resolver in the
// registry, team.db in a TempDir), its routes on a fresh mux, and one test
// subscriber that collects every broadcast.
func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{t: t, origins: &fakeOrigins{}}
	f.clock.Store(1_000_000)
	f.core = core.New(core.CoreDeps{Config: &config.Config{HostID: "h:1", DataDir: t.TempDir()}})
	f.core.Registry.Register(peersmod.OriginResolverKey, f.origins)
	f.m = New()
	f.m.logf = func(string, ...any) {}
	f.m.now = func() int64 { return f.clock.Load() }
	if err := f.m.Init(f.core); err != nil {
		t.Fatal(err)
	}
	f.mux = http.NewServeMux()
	f.m.RegisterRoutes(f.mux)
	f.sub = f.core.Events.AddTestSubscriber()
	t.Cleanup(func() {
		f.core.Events.RemoveTestSubscriber(f.sub)
		_ = f.m.Stop(context.Background())
		_ = f.m.Close()
	})
	return f
}

func (f *fixture) do(method, path string, body any) (int, []byte) {
	f.t.Helper()
	var rd *bytes.Reader
	if s, ok := body.(string); ok {
		rd = bytes.NewReader([]byte(s))
	} else {
		raw, err := json.Marshal(body)
		if err != nil {
			f.t.Fatal(err)
		}
		rd = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, path, rd)
	req.RemoteAddr = "100.64.0.4:51234"
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, req)
	return rec.Code, rec.Body.Bytes()
}

func (f *fixture) createReq(id string) team.CreateApprovalRequest {
	return team.CreateApprovalRequest{ID: id, Kind: team.KindLead, OriginInbox: "/tmp/10.sock", Reason: "split the work"}
}

func (f *fixture) create(id string) team.Approval {
	f.t.Helper()
	code, body := f.do(http.MethodPost, "/api/team/approvals", f.createReq(id))
	if code != http.StatusCreated {
		f.t.Fatalf("create %s: %d %s", id, code, body)
	}
	return decodeApproval(f.t, body)
}

func decodeApproval(t *testing.T, body []byte) team.Approval {
	t.Helper()
	var a team.Approval
	if err := json.Unmarshal(body, &a); err != nil {
		t.Fatalf("decode approval: %v; body=%s", err, body)
	}
	return a
}

func decodeErr(t *testing.T, body []byte) team.APIError {
	t.Helper()
	var e team.APIError
	if err := json.Unmarshal(body, &e); err != nil {
		t.Fatalf("decode APIError: %v; body=%s", err, body)
	}
	return e
}

// events drains everything broadcast so far, decoded to EventValue, in order.
func (f *fixture) events() []team.EventValue {
	f.t.Helper()
	var out []team.EventValue
	for {
		select {
		case raw := <-f.sub.SendCh():
			var ev core.HostEvent
			if err := json.Unmarshal(raw, &ev); err != nil {
				f.t.Fatalf("decode HostEvent: %v", err)
			}
			if ev.Type != team.EventType || ev.Session != "" {
				f.t.Fatalf("event = %+v", ev)
			}
			var v team.EventValue
			if err := json.Unmarshal([]byte(ev.Value), &v); err != nil {
				f.t.Fatalf("decode EventValue: %v", err)
			}
			out = append(out, v)
		default:
			return out
		}
	}
}

func (f *fixture) countOps(op string) int {
	n := 0
	for _, ev := range f.events() {
		if ev.Op == op {
			n++
		}
	}
	return n
}

func TestCreate_NewThenIdempotentThenConflict(t *testing.T) {
	f := newFixture(t)
	a := f.create("id-1")
	if a.State != team.StateOpen || a.HostID != "h:1" || a.Kind != team.KindLead ||
		a.Origin.SessionID != "sid-1" || a.Origin.Ref != "_abc123" || a.Origin.Tmux != "mt0:@1.%1" ||
		a.CreatedAt != 1_000_000 || a.DeadlineAt != 1_000_000+540_000 || a.LeaseUntil != 1_000_000+30_000 {
		t.Fatalf("approval = %+v", a)
	}
	var p team.LeadPayload
	if err := json.Unmarshal(a.Payload, &p); err != nil || p.Reason != "split the work" || p.MaxMembers != 3 || len(p.Roots) != 1 || p.Roots[0] != "/w" {
		t.Fatalf("payload = %+v err=%v (defaults: max_members 3, roots [cwd])", p, err)
	}
	evs := f.events()
	if len(evs) != 1 || evs[0].Op != "opened" || evs[0].Approval == nil || evs[0].Approval.ID != "id-1" {
		t.Fatalf("events after create = %+v", evs)
	}

	code, body := f.do(http.MethodPost, "/api/team/approvals", f.createReq("id-1")) // same request again
	if code != http.StatusOK || decodeApproval(t, body).ID != "id-1" || len(f.events()) != 0 {
		t.Fatalf("retry: %d %s (must be 200, same row, no new event)", code, body)
	}
	other := f.createReq("id-1")
	other.Reason = "something else"
	code, body = f.do(http.MethodPost, "/api/team/approvals", other)
	if code != http.StatusConflict || decodeErr(t, body).Error != team.ErrIDConflict {
		t.Fatalf("id reuse: %d %s", code, body)
	}
}

func TestCreate_Rejections(t *testing.T) {
	f := newFixture(t)
	cases := []struct {
		name   string
		body   any
		status int
		code   string
	}{
		{"bad json", `{`, 400, team.ErrBadRequest},
		{"no id", team.CreateApprovalRequest{Kind: team.KindLead, OriginInbox: "/tmp/10.sock", Reason: "r"}, 400, team.ErrBadRequest},
		{"no reason", team.CreateApprovalRequest{ID: "x", Kind: team.KindLead, OriginInbox: "/tmp/10.sock", Reason: "  "}, 400, team.ErrBadRequest},
		{"bad kind", team.CreateApprovalRequest{ID: "x", Kind: "boss", OriginInbox: "/tmp/10.sock", Reason: "r"}, 400, team.ErrBadRequest},
		{"self_relay", team.CreateApprovalRequest{ID: "x", Kind: team.KindSelfRelay, OriginInbox: "/tmp/10.sock", Reason: "r"}, 400, team.ErrUnsupportedKind},
		{"unknown inbox", team.CreateApprovalRequest{ID: "x", Kind: team.KindLead, OriginInbox: "/tmp/99.sock", Reason: "r"}, 400, team.ErrOriginUnknown},
		{"negative wait", team.CreateApprovalRequest{ID: "x", Kind: team.KindLead, OriginInbox: "/tmp/10.sock", Reason: "r", WaitS: -1}, 400, team.ErrBadRequest},
	}
	for _, tc := range cases {
		code, body := f.do(http.MethodPost, "/api/team/approvals", tc.body)
		if code != tc.status || decodeErr(t, body).Error != tc.code {
			t.Errorf("%s: %d %s, want %d %s", tc.name, code, body, tc.status, tc.code)
		}
	}
	if n := len(f.events()); n != 0 {
		t.Fatalf("%d events after rejected creates", n)
	}

	// Caps: max_members 20 → 8, wait_s 999 → 600; relative roots resolve against cwd.
	capped := f.createReq("id-cap")
	capped.MaxMembers, capped.WaitS, capped.Roots = 20, 999, []string{"sub/../a", "/abs/b/"}
	code, body := f.do(http.MethodPost, "/api/team/approvals", capped)
	a := decodeApproval(t, body)
	var p team.LeadPayload
	_ = json.Unmarshal(a.Payload, &p)
	if code != 201 || p.MaxMembers != 8 || a.DeadlineAt != 1_000_000+600_000 || len(p.Roots) != 2 || p.Roots[0] != "/w/a" || p.Roots[1] != "/abs/b" {
		t.Fatalf("capped: %d %+v payload=%+v", code, a, p)
	}

	// One open lead request per origin session: a second id is request_open, carrying the first.
	code, body = f.do(http.MethodPost, "/api/team/approvals", f.createReq("id-2"))
	e := decodeErr(t, body)
	if code != http.StatusConflict || e.Error != team.ErrRequestOpen || e.Approval == nil || e.Approval.ID != "id-cap" {
		t.Fatalf("request_open: %d %s", code, body)
	}
	// Another session is not blocked.
	second := f.createReq("id-3")
	second.OriginInbox = "/tmp/20.sock"
	if code, body := f.do(http.MethodPost, "/api/team/approvals", second); code != 201 {
		t.Fatalf("second origin: %d %s", code, body)
	}

	// Stopping: create answers 503 not_ready.
	_ = f.m.Stop(context.Background())
	code, body = f.do(http.MethodPost, "/api/team/approvals", f.createReq("id-4"))
	if code != http.StatusServiceUnavailable || decodeErr(t, body).Error != team.ErrNotReady {
		t.Fatalf("while stopping: %d %s", code, body)
	}
}

func TestGet_RenewsLeaseAndWakesOnDelete(t *testing.T) {
	f := newFixture(t)
	f.create("id-1")
	f.events()
	f.clock.Add(10_000)
	code, body := f.do(http.MethodGet, "/api/team/approvals/id-1?wait=0", nil)
	if a := decodeApproval(t, body); code != 200 || a.LeaseUntil != 1_010_000+30_000 {
		t.Fatalf("poll must renew the lease: %d %+v", code, a)
	}
	if code, body := f.do(http.MethodGet, "/api/team/approvals/nope", nil); code != 404 || decodeErr(t, body).Error != team.ErrNotFound {
		t.Fatalf("unknown id: %d %s", code, body)
	}
	if code, _ := f.do(http.MethodGet, "/api/team/approvals/id-1?wait=abc", nil); code != 400 {
		t.Fatalf("bad wait: %d", code)
	}

	done := make(chan team.Approval, 1)
	go func() {
		_, body := f.do(http.MethodGet, "/api/team/approvals/id-1?wait=20", nil)
		done <- decodeApproval(t, body)
	}()
	time.Sleep(50 * time.Millisecond)
	start := time.Now()
	code, body = f.do(http.MethodDelete, "/api/team/approvals/id-1", nil)
	if a := decodeApproval(t, body); code != 200 || a.State != team.StateCancelled || a.DecidedAt != 1_010_000 {
		t.Fatalf("delete: %d %+v", code, a)
	}
	select {
	case a := <-done:
		if a.State != team.StateCancelled || time.Since(start) > 5*time.Second {
			t.Fatalf("long-poll woke with %+v after %s", a, time.Since(start))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("long-poll did not wake on cancel")
	}
	if n := f.countOps("closed"); n != 1 {
		t.Fatalf("closed events = %d, want 1", n)
	}
	// A second DELETE answers the row as it is and emits nothing.
	code, body = f.do(http.MethodDelete, "/api/team/approvals/id-1", nil)
	if code != 200 || decodeApproval(t, body).State != team.StateCancelled || len(f.events()) != 0 {
		t.Fatalf("second delete: %d %s", code, body)
	}
	if code, _ := f.do(http.MethodDelete, "/api/team/approvals/nope", nil); code != 404 {
		t.Fatalf("delete unknown: %d", code)
	}
}

func TestGet_LongPollReturnsOnStopAndOnTimer(t *testing.T) {
	f := newFixture(t)
	f.create("id-1")
	start := time.Now()
	if code, body := f.do(http.MethodGet, "/api/team/approvals/id-1?wait=1", nil); code != 200 || decodeApproval(t, body).State != team.StateOpen {
		t.Fatalf("timer expiry: %d %s", code, body)
	}
	if d := time.Since(start); d < 900*time.Millisecond || d > 5*time.Second {
		t.Fatalf("wait=1 returned after %s", d)
	}

	done := make(chan int, 1)
	go func() {
		code, _ := f.do(http.MethodGet, "/api/team/approvals/id-1?wait=20", nil)
		done <- code
	}()
	time.Sleep(50 * time.Millisecond)
	start = time.Now()
	_ = f.m.Stop(context.Background())
	select {
	case code := <-done:
		if code != 200 || time.Since(start) > 5*time.Second {
			t.Fatalf("on stop: %d after %s", code, time.Since(start))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("long-poll did not return on Stop")
	}
}

func TestDecide_ApproveDenyAlreadyDecided(t *testing.T) {
	f := newFixture(t)
	f.create("id-1")
	second := f.createReq("id-2")
	second.OriginInbox = "/tmp/20.sock"
	if code, _ := f.do(http.MethodPost, "/api/team/approvals", second); code != 201 {
		t.Fatal("second create")
	}
	f.events()
	client := team.Client{Kind: "app", Label: "Purdex.app @ air26"}

	code, body := f.do(http.MethodPost, "/api/team/approvals/id-1/decide",
		team.DecideRequest{Decision: "approve", Grant: &team.Grant{MaxMembers: 2, Roots: []string{"x", "/y/"}}, Client: client})
	a := decodeApproval(t, body)
	if code != 200 || a.State != team.StateApproved || a.Grant == nil || a.Grant.MaxMembers != 2 ||
		len(a.Grant.Roots) != 2 || a.Grant.Roots[0] != "/w/x" || a.Grant.Roots[1] != "/y" ||
		a.DecidedBy == nil || a.DecidedBy.Label != client.Label || a.DecidedBy.Addr != "100.64.0.4:51234" || a.DecidedAt != 1_000_000 {
		t.Fatalf("approve: %d %+v grant=%+v by=%+v", code, a, a.Grant, a.DecidedBy)
	}
	evs := f.events()
	if len(evs) != 1 || evs[0].Op != "closed" || evs[0].Approval.State != team.StateApproved || evs[0].Approval.DecidedBy.Label != client.Label {
		t.Fatalf("events after approve = %+v", evs)
	}
	code, body = f.do(http.MethodPost, "/api/team/approvals/id-1/decide", team.DecideRequest{Decision: "deny", Client: team.Client{Kind: "app", Label: "Purdex.app @ a19"}})
	e := decodeErr(t, body)
	if code != http.StatusConflict || e.Error != team.ErrAlreadyDecided || e.Approval == nil || e.Approval.DecidedBy == nil || e.Approval.DecidedBy.Label != client.Label {
		t.Fatalf("late decide: %d %s (409 must carry who handled it)", code, body)
	}

	// Deny without a grant edit; approve with a nil grant takes the payload's values.
	code, body = f.do(http.MethodPost, "/api/team/approvals/id-2/decide", team.DecideRequest{Decision: "deny", Client: client})
	if a := decodeApproval(t, body); code != 200 || a.State != team.StateDenied || a.Grant != nil || a.DecidedBy == nil {
		t.Fatalf("deny: %d %+v", code, a)
	}
	third := f.createReq("id-3")
	third.OriginInbox = "/tmp/20.sock"
	if code, _ := f.do(http.MethodPost, "/api/team/approvals", third); code != 201 {
		t.Fatal("third create")
	}
	code, body = f.do(http.MethodPost, "/api/team/approvals/id-3/decide", team.DecideRequest{Decision: "approve", Client: client})
	if a := decodeApproval(t, body); code != 200 || a.Grant == nil || a.Grant.MaxMembers != 3 || len(a.Grant.Roots) != 1 || a.Grant.Roots[0] != "/w2" {
		t.Fatalf("approve with nil grant: %d %+v grant=%+v", code, a, a.Grant)
	}

	for name, req := range map[string]any{
		"bad decision": team.DecideRequest{Decision: "maybe", Client: client},
		"no client":    team.DecideRequest{Decision: "approve"},
		"bad json":     `{`,
	} {
		if code, body := f.do(http.MethodPost, "/api/team/approvals/id-1/decide", req); code != 400 || decodeErr(t, body).Error != team.ErrBadRequest {
			t.Errorf("%s: %d %s", name, code, body)
		}
	}
	if code, body := f.do(http.MethodPost, "/api/team/approvals/nope/decide", team.DecideRequest{Decision: "deny", Client: client}); code != 404 || decodeErr(t, body).Error != team.ErrNotFound {
		t.Fatalf("unknown id: %d %s", code, body)
	}
}

func TestList_OpenOnly(t *testing.T) {
	f := newFixture(t)
	f.create("id-1")
	f.clock.Add(1)
	second := f.createReq("id-2")
	second.OriginInbox = "/tmp/20.sock"
	f.do(http.MethodPost, "/api/team/approvals", second)
	f.do(http.MethodDelete, "/api/team/approvals/id-1", nil)
	code, body := f.do(http.MethodGet, "/api/team/approvals?state=open", nil)
	var out struct {
		Approvals []team.Approval `json:"approvals"`
	}
	if err := json.Unmarshal(body, &out); err != nil || code != 200 || len(out.Approvals) != 1 || out.Approvals[0].ID != "id-2" {
		t.Fatalf("list: %d %s err=%v", code, body, err)
	}
	if code, _ := f.do(http.MethodGet, "/api/team/approvals?state=closed", nil); code != 400 {
		t.Fatalf("state=closed: %d", code)
	}
}

// GET /api/team/inflight (spec §9.5) feeds the restart confirm: open
// requests only, and relays_active is on the wire as 0 until P6.
func TestInflight_CountsOpenApprovals(t *testing.T) {
	f := newFixture(t)
	f.create("id-1")
	second := f.createReq("id-2")
	second.OriginInbox = "/tmp/20.sock"
	if code, _ := f.do(http.MethodPost, "/api/team/approvals", second); code != 201 {
		t.Fatal("second create")
	}
	if code, body := f.do(http.MethodGet, "/api/team/inflight", nil); code != 200 || !bytes.Contains(body, []byte(`"approvals_open":2`)) {
		t.Fatalf("two open: %d %s", code, body)
	}
	f.do(http.MethodDelete, "/api/team/approvals/id-1", nil)
	code, body := f.do(http.MethodGet, "/api/team/inflight", nil)
	var got team.InflightResponse
	if err := json.Unmarshal(body, &got); err != nil || code != 200 || got.ApprovalsOpen != 1 || got.RelaysActive != 0 {
		t.Fatalf("one open: %d %s err=%v (want approvals_open 1, relays_active 0)", code, body, err)
	}
	if !bytes.Contains(body, []byte(`"relays_active":0`)) {
		t.Fatalf("relays_active must be on the wire at zero: %s", body)
	}
}
```

- [ ] **Step 2: Run it and see it fail.**
  - Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/lead-team && go test ./internal/module/team/ -run 'TestCreate|TestGet|TestDecide|TestList|TestInflight' -v`
  - Expected: build failure —
    ```
    internal/module/team/handler_test.go:55:11: undefined: Module
    internal/module/team/handler_test.go:72:8: undefined: New
    ```

- [ ] **Step 3: Implement.** First `module.go` (with a `Start` stub that Task 2.6 fills in):

```go
package teammod

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"path/filepath"
	"sync"
	"time"

	"github.com/wake/purdex/internal/core"
	peersmod "github.com/wake/purdex/internal/module/peers"
	"github.com/wake/purdex/internal/team"
)

// OriginResolver attributes a request's caller to a live CC session and
// answers whether a session is still live. The peers module's
// *OriginResolver is the production value (registry key
// peersmod.OriginResolverKey); tests inject a fake.
type OriginResolver interface {
	ResolveOrigin(inbox string) (team.Origin, bool)
	LiveSession(sessionID string) bool
}

// Module owns team.db and serves /api/team/*.
type Module struct {
	core    *core.Core
	store   *Store
	origins OriginResolver
	now     func() int64 // unix ms; injectable for tests
	logf    func(format string, args ...any)

	// stopCtx is cancelled first in Stop: long-polls return, the sweeper
	// exits and POST create answers 503 not_ready. The DB stays open until
	// Close (PD6): in-flight handlers still read it during srv.Shutdown.
	stopCtx    context.Context
	stopCancel context.CancelFunc
	sweepWG    sync.WaitGroup
	tickN      int // sweeper ticks so far; only the sweeper goroutine (or a test) touches it

	createMu sync.Mutex // serialises the request_open check with the insert

	mu      sync.Mutex
	waiters map[string][]chan struct{} // long-polls per approval id; closed when it closes
}

// New returns a Module with production defaults.
func New() *Module {
	stopCtx, stopCancel := context.WithCancel(context.Background())
	return &Module{
		now:        func() int64 { return time.Now().UnixMilli() },
		logf:       log.Printf,
		stopCtx:    stopCtx,
		stopCancel: stopCancel,
		waiters:    map[string][]chan struct{}{},
	}
}

func (m *Module) Name() string           { return "team" }
func (m *Module) Dependencies() []string { return []string{"peers"} }

// Init resolves the origin resolver peers registered and opens team.db in
// the data dir. Both are hard errors: without either the module cannot
// attribute or persist a single request.
func (m *Module) Init(c *core.Core) error {
	m.core = c
	svc, ok := c.Registry.Get(peersmod.OriginResolverKey)
	if !ok {
		return fmt.Errorf("team: service %q not registered", peersmod.OriginResolverKey)
	}
	origins, ok := svc.(OriginResolver)
	if !ok {
		return fmt.Errorf("team: service %q does not implement OriginResolver (%T)", peersmod.OriginResolverKey, svc)
	}
	m.origins = origins
	store, err := OpenStore(filepath.Join(c.Cfg.DataDir, "team.db"))
	if err != nil {
		return fmt.Errorf("team: %w", err)
	}
	m.store = store
	return nil
}

func (m *Module) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/team/approvals", m.handleCreate)
	mux.HandleFunc("GET /api/team/approvals", m.handleList)
	mux.HandleFunc("GET /api/team/approvals/{id}", m.handleGet)
	mux.HandleFunc("DELETE /api/team/approvals/{id}", m.handleDelete)
	mux.HandleFunc("POST /api/team/approvals/{id}/decide", m.handleDecide)
	mux.HandleFunc("GET /api/team/inflight", m.handleInflight)
}

// Start is filled in by Task 2.6.
func (m *Module) Start(context.Context) error { return nil }

// Stop cancels stopCtx (long-polls return, create answers not_ready) and
// joins the sweeper. Idempotent. The DB is closed in Close.
func (m *Module) Stop(context.Context) error {
	m.stopCancel()
	m.sweepWG.Wait()
	return nil
}

// Close closes team.db, after the HTTP server has stopped (core.Closer).
func (m *Module) Close() error {
	if m.store != nil {
		return m.store.Close()
	}
	return nil
}

func (m *Module) stopping() bool {
	select {
	case <-m.stopCtx.Done():
		return true
	default:
		return false
	}
}

func (m *Module) hostID() string {
	m.core.CfgMu.RLock()
	defer m.core.CfgMu.RUnlock()
	return m.core.Cfg.HostID
}

// closeAs is the one close path: the store's CAS, then — for the winner
// only — the closed broadcast and the long-poll wake-up. So every close
// produces exactly one closed event, whoever raced for it.
func (m *Module) closeAs(id string, c Close) (team.Approval, bool, error) {
	after, won, err := m.store.CloseIfOpen(id, c)
	if err != nil {
		return team.Approval{}, false, err
	}
	if won {
		m.broadcast("closed", &after)
		m.wake(id)
	}
	return after, won, nil
}

func (m *Module) broadcast(op string, a *team.Approval) {
	v, err := json.Marshal(team.EventValue{Op: op, Approval: a})
	if err != nil {
		m.logf("[team] encode %s event: %v", op, err)
		return
	}
	m.core.Events.BroadcastEvent(core.HostEvent{Type: team.EventType, Value: string(v)})
}

// addWaiter registers a long-poll on id; the channel is closed by wake.
func (m *Module) addWaiter(id string) chan struct{} {
	ch := make(chan struct{})
	m.mu.Lock()
	m.waiters[id] = append(m.waiters[id], ch)
	m.mu.Unlock()
	return ch
}

func (m *Module) removeWaiter(id string, ch chan struct{}) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ws := m.waiters[id]
	for i, w := range ws {
		if w == ch {
			ws = append(ws[:i], ws[i+1:]...)
			break
		}
	}
	if len(ws) == 0 {
		delete(m.waiters, id)
	} else {
		m.waiters[id] = ws
	}
}

// wake releases every long-poll on id.
func (m *Module) wake(id string) {
	m.mu.Lock()
	ws := m.waiters[id]
	delete(m.waiters, id)
	m.mu.Unlock()
	for _, ch := range ws {
		close(ch)
	}
}
```

  Then `handler.go`:

```go
package teammod

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/wake/purdex/internal/team"
)

// bodyCap bounds request bodies; a request is never larger than a few KB.
const bodyCap = 1 << 20

// errStorage is the 500 body's error code: a team.db failure, outside the
// wire contract's codes (clients treat any unlisted code as a plain error).
const errStorage = "storage_error"

func (m *Module) writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		m.logf("[team] encode response: %v", err)
	}
}

func (m *Module) writeErr(w http.ResponseWriter, status int, code, detail string, a *team.Approval) {
	m.writeJSON(w, status, team.APIError{Error: code, Detail: detail, Approval: a})
}

// decodeBody decodes a JSON body into v; false means a 400 was written.
func (m *Module) decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	body, err := io.ReadAll(io.LimitReader(r.Body, bodyCap+1))
	if err != nil || len(body) > bodyCap {
		m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, "body unreadable or over 1 MiB", nil)
		return false
	}
	if err := json.Unmarshal(body, v); err != nil {
		m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, "invalid JSON: "+err.Error(), nil)
		return false
	}
	return true
}

// normaliseRoots cleans and absolutises roots against the origin's cwd;
// empty means [cwd]. An error names a root that cannot be made absolute.
func normaliseRoots(roots []string, cwd string) ([]string, error) {
	out := make([]string, 0, len(roots))
	for _, r := range roots {
		r = strings.TrimSpace(r)
		if r == "" {
			continue
		}
		if !filepath.IsAbs(r) {
			r = filepath.Join(cwd, r)
		}
		if !filepath.IsAbs(r) {
			return nil, fmt.Errorf("root %q is not absolute and the origin has no cwd", r)
		}
		out = append(out, filepath.Clean(r))
	}
	if len(out) == 0 {
		if !filepath.IsAbs(cwd) {
			return nil, errors.New("roots are required: the origin has no cwd")
		}
		out = append(out, filepath.Clean(cwd))
	}
	return out, nil
}

// normaliseMaxMembers applies 0→3, cap 8 (spec §6.1); def is the fallback for 0.
func normaliseMaxMembers(n, def int) int {
	if n <= 0 {
		return def
	}
	if n > team.MaxMaxMembers {
		return team.MaxMaxMembers
	}
	return n
}

// requestHash is the idempotency key's fingerprint: the fields that make
// two requests "the same request" (PD, handoff notes).
func requestHash(kind team.Kind, sessionID string, waitS int, payload []byte) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s\x00%s\x00%d\x00", kind, sessionID, waitS)
	h.Write(payload)
	return hex.EncodeToString(h.Sum(nil))
}

// handleCreate is POST /api/team/approvals.
func (m *Module) handleCreate(w http.ResponseWriter, r *http.Request) {
	if m.stopping() {
		m.writeErr(w, http.StatusServiceUnavailable, team.ErrNotReady, "daemon is stopping", nil)
		return
	}
	var req team.CreateApprovalRequest
	if !m.decodeBody(w, r, &req) {
		return
	}
	if req.ID == "" || len(req.ID) > 128 {
		m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, "id is required (UUID v4) and at most 128 bytes", nil)
		return
	}
	switch req.Kind {
	case team.KindLead:
	case team.KindSelfRelay:
		m.writeErr(w, http.StatusBadRequest, team.ErrUnsupportedKind, "kind self_relay is not supported by this daemon yet", nil)
		return
	default:
		m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, "kind must be lead", nil)
		return
	}
	if req.WaitS < 0 || req.MaxMembers < 0 {
		m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, "wait_s and max_members must not be negative", nil)
		return
	}
	reason := strings.TrimSpace(req.Reason)
	if reason == "" {
		m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, "reason is required", nil)
		return
	}
	origin, ok := m.origins.ResolveOrigin(req.OriginInbox)
	if !ok {
		m.writeErr(w, http.StatusBadRequest, team.ErrOriginUnknown, "origin_inbox is not a live Claude Code session on this host", nil)
		return
	}
	roots, err := normaliseRoots(req.Roots, origin.Cwd)
	if err != nil {
		m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, err.Error(), nil)
		return
	}
	waitS := req.WaitS
	if waitS == 0 {
		waitS = team.DefaultWaitS
	}
	if waitS > team.MaxWaitS {
		waitS = team.MaxWaitS
	}
	payload, err := json.Marshal(team.LeadPayload{Reason: reason, MaxMembers: normaliseMaxMembers(req.MaxMembers, team.DefaultMaxMembers), Roots: roots})
	if err != nil {
		m.writeErr(w, http.StatusInternalServerError, errStorage, "encode payload: "+err.Error(), nil)
		return
	}
	hash := requestHash(req.Kind, origin.SessionID, waitS, payload)

	m.createMu.Lock()
	defer m.createMu.Unlock()
	if open, found, err := m.store.OpenByOrigin(origin.SessionID, req.Kind); err != nil {
		m.logf("[team] create %s: %v", req.ID, err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
		return
	} else if found && open.ID != req.ID {
		m.writeErr(w, http.StatusConflict, team.ErrRequestOpen, "this session already has an open lead request", &open)
		return
	}
	now := m.now()
	stored, storedHash, inserted, err := m.store.Create(team.Approval{
		ID: req.ID, Kind: req.Kind, HostID: m.hostID(), Origin: origin, Payload: payload, State: team.StateOpen,
		CreatedAt: now, DeadlineAt: now + int64(waitS)*1000, LeaseUntil: now + team.LeaseS*1000,
	}, hash)
	if err != nil {
		m.logf("[team] create %s: %v", req.ID, err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
		return
	}
	if !inserted {
		if storedHash != hash {
			m.writeErr(w, http.StatusConflict, team.ErrIDConflict, "id already used by a different request", nil)
			return
		}
		m.writeJSON(w, http.StatusOK, stored)
		return
	}
	m.logf("[team] approval %s opened: kind=%s origin=%s (%s) reason=%q", stored.ID, stored.Kind, origin.Ref, origin.SessionID, reason)
	m.broadcast("opened", &stored)
	m.writeJSON(w, http.StatusCreated, stored)
}

// handleList is GET /api/team/approvals?state=open.
func (m *Module) handleList(w http.ResponseWriter, r *http.Request) {
	if s := r.URL.Query().Get("state"); s != "" && s != string(team.StateOpen) {
		m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, "only state=open is supported", nil)
		return
	}
	open, err := m.store.ListOpen()
	if err != nil {
		m.logf("[team] list: %v", err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
		return
	}
	m.writeJSON(w, http.StatusOK, map[string]any{"approvals": open})
}

// handleInflight is GET /api/team/inflight (spec §9.5): what a restart of
// this daemon would interrupt, for the App's restart confirm. Open requests
// survive a restart (boot lease grace), so the count informs, it does not
// block. relays_active is 0 until P6 adds relays.
func (m *Module) handleInflight(w http.ResponseWriter, r *http.Request) {
	open, err := m.store.ListOpen()
	if err != nil {
		m.logf("[team] inflight: %v", err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
		return
	}
	m.writeJSON(w, http.StatusOK, team.InflightResponse{ApprovalsOpen: len(open), RelaysActive: 0})
}

// handleGet is GET /api/team/approvals/{id}?wait=N: it renews the lease
// and, while the request is open and N > 0, waits for the close, N
// seconds (≤ 25), the client going away, or Stop — whichever is first —
// then answers the row as it is.
func (m *Module) handleGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	wait := 0
	if s := r.URL.Query().Get("wait"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 0 {
			m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, "wait must be a non-negative number of seconds", nil)
			return
		}
		wait = min(n, team.MaxPollWaitS)
	}
	// Register before the read, so a close between the read and the
	// select cannot be missed.
	ch := m.addWaiter(id)
	defer m.removeWaiter(id, ch)
	if err := m.store.RenewLease(id, m.now()+team.LeaseS*1000); err != nil {
		m.logf("[team] get %s: %v", id, err)
	}
	a, ok, err := m.store.Get(id)
	if err != nil {
		m.logf("[team] get %s: %v", id, err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
		return
	}
	if !ok {
		m.writeErr(w, http.StatusNotFound, team.ErrNotFound, "no such approval request", nil)
		return
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
			return
		}
	}
	m.writeJSON(w, http.StatusOK, a)
}

// handleDelete is DELETE /api/team/approvals/{id}: the requester gives up.
// It answers the row as it now is — cancelled, or closed before as it was.
func (m *Module) handleDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	after, won, err := m.closeAs(id, Close{State: team.StateCancelled, DecidedAt: m.now()})
	if errors.Is(err, ErrNoSuchApproval) {
		m.writeErr(w, http.StatusNotFound, team.ErrNotFound, "no such approval request", nil)
		return
	}
	if err != nil {
		m.logf("[team] delete %s: %v", id, err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
		return
	}
	if won {
		m.logf("[team] approval %s cancelled by its requester", id)
	}
	m.writeJSON(w, http.StatusOK, after)
}

// handleDecide is POST /api/team/approvals/{id}/decide (spec §6.5): one
// click on any App; the client label and remote address are audit, and
// every decision is one daemon log line.
func (m *Module) handleDecide(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req team.DecideRequest
	if !m.decodeBody(w, r, &req) {
		return
	}
	var state team.State
	switch req.Decision {
	case "approve":
		state = team.StateApproved
	case "deny":
		state = team.StateDenied
	default:
		m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, `decision must be "approve" or "deny"`, nil)
		return
	}
	if strings.TrimSpace(req.Client.Kind) == "" || strings.TrimSpace(req.Client.Label) == "" {
		m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, "client.kind and client.label are required", nil)
		return
	}
	client := req.Client
	client.Addr = r.RemoteAddr
	a, ok, err := m.store.Get(id)
	if err != nil {
		m.logf("[team] decide %s: %v", id, err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
		return
	}
	if !ok {
		m.writeErr(w, http.StatusNotFound, team.ErrNotFound, "no such approval request", nil)
		return
	}
	if a.State != team.StateOpen {
		m.writeErr(w, http.StatusConflict, team.ErrAlreadyDecided, "this request is already closed", &a)
		return
	}
	var grant *team.Grant
	if state == team.StateApproved {
		var payload team.LeadPayload
		if err := json.Unmarshal(a.Payload, &payload); err != nil {
			m.logf("[team] decide %s: decode payload: %v", id, err)
			m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
			return
		}
		g := team.Grant{MaxMembers: payload.MaxMembers, Roots: payload.Roots}
		if req.Grant != nil {
			g.MaxMembers = normaliseMaxMembers(req.Grant.MaxMembers, payload.MaxMembers)
			if len(req.Grant.Roots) > 0 {
				roots, err := normaliseRoots(req.Grant.Roots, a.Origin.Cwd)
				if err != nil {
					m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, err.Error(), nil)
					return
				}
				g.Roots = roots
			}
		}
		grant = &g
	}
	after, won, err := m.closeAs(id, Close{State: state, DecidedAt: m.now(), DecidedBy: &client, Grant: grant})
	if errors.Is(err, ErrNoSuchApproval) {
		m.writeErr(w, http.StatusNotFound, team.ErrNotFound, "no such approval request", nil)
		return
	}
	if err != nil {
		m.logf("[team] decide %s: %v", id, err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
		return
	}
	if !won {
		m.writeErr(w, http.StatusConflict, team.ErrAlreadyDecided, "this request was closed first by someone else", &after)
		return
	}
	m.logf("[team] approval %s %s by %s %q from %s (origin %s)", id, after.State, client.Kind, client.Label, client.Addr, after.Origin.Ref)
	m.writeJSON(w, http.StatusOK, after)
}
```

- [ ] **Step 4: Run and see it pass (with the race detector; `TestGet_LongPollReturnsOnStopAndOnTimer` takes 1 s by design — it waits out `wait=1`).**
  - Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/lead-team && go test ./internal/module/team/ -race -count=1 -v`
  - Expected:
    ```
    --- PASS: TestCreate_NewThenIdempotentThenConflict (0.01s)
    --- PASS: TestCreate_Rejections (0.01s)
    --- PASS: TestGet_RenewsLeaseAndWakesOnDelete (0.06s)
    --- PASS: TestGet_LongPollReturnsOnStopAndOnTimer (1.07s)
    --- PASS: TestDecide_ApproveDenyAlreadyDecided (0.03s)
    --- PASS: TestList_OpenOnly (0.01s)
    --- PASS: TestInflight_CountsOpenApprovals (0.01s)
    --- PASS: TestStore_CreateIsIdempotentAndReportsHashMismatch (0.01s)
    --- PASS: TestStore_CloseIfOpenExactlyOneWinner (0.02s)
    --- PASS: TestStore_LeasesAndListing (0.01s)
    ok  	github.com/wake/purdex/internal/module/team	2.5s
    ```
  - Also: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/lead-team && go vet ./internal/module/team/` → no output.

- [ ] **Step 5: Commit — in two parts, because P2a-2 and P2a-3 are separate PRs.**
  - **P2a-2 commit:** `module.go` (skeleton, `RegisterRoutes` registering only `POST /api/team/approvals` and `GET /api/team/approvals`), `handler.go` with `handleCreate`, `handleList`, `writeJSON`, `writeErr`, the normaliser and the hash, and `handler_test.go` with the fixture and the `TestCreate_*` tests. `TestList_OpenOnly` closes a row through `DELETE`, so it travels with P2a-3.
    ```bash
    git add internal/module/team/module.go internal/module/team/handler.go internal/module/team/handler_test.go
    git commit -m "feat(team): team module skeleton; POST and GET /api/team/approvals (create, list)

    Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
    ```
  - **P2a-3 commit:** `handleGet` (long-poll, lease renewal, waiters), `handleDelete`, `handleDecide`, `handleInflight`, the four routes added to `RegisterRoutes`, and `TestList_OpenOnly`, `TestGet_*`, `TestDelete_*`, `TestDecide_*`, `TestInflight_CountsOpenApprovals`.
    ```bash
    git add internal/module/team/module.go internal/module/team/handler.go internal/module/team/handler_test.go
    git commit -m "feat(team): GET /api/team/approvals/{id} long-poll, DELETE, POST decide, GET inflight

    Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
    ```

---

### Task 2.5: The sweeper

**Files:**
- Create: `internal/module/team/sweeper.go`
- Test: `internal/module/team/sweeper_test.go`

**Interfaces:**
- Produces (package `teammod`):
  ```go
  const sweepInterval = time.Second
  const livenessEvery = 10
  func (m *Module) runSweeper()  // ticks until stopCtx; started by Start (Task 2.6); sweepWG.Done on exit
  func (m *Module) tick()        // one pass: deadline → timeout, lease → abandoned, every 10th tick origin gone → abandoned; each through closeAs
  ```
- Consumes: `Store.ListOpen`, `m.closeAs`, `m.origins.LiveSession`, `m.now`. Tests call `tick()` directly and move the injected clock; nothing sleeps. A registry read error never abandons: the resolver answers "live" for it (Task 2.3), so `tick` has no error branch to get wrong.

- [ ] **Step 1: Write the failing test.** (`newFixture`, `countOps`, `decodeErr` are Task 2.4's.)

```go
package teammod

import (
	"net/http"
	"sync"
	"testing"

	"github.com/wake/purdex/internal/team"
)

func TestTick_DeadlineBecomesTimeout(t *testing.T) {
	f := newFixture(t)
	req := f.createReq("id-1")
	req.WaitS = 60
	if code, _ := f.do(http.MethodPost, "/api/team/approvals", req); code != 201 {
		t.Fatal("create")
	}
	f.events()
	f.clock.Add(59_999)
	f.do(http.MethodGet, "/api/team/approvals/id-1?wait=0", nil) // the CLI's poll renews the 30 s lease
	f.m.tick()
	if a, _, _ := f.m.store.Get("id-1"); a.State != team.StateOpen {
		t.Fatalf("closed before the deadline: %s", a.State)
	}
	f.clock.Add(1)
	f.m.tick()
	a, _, _ := f.m.store.Get("id-1")
	if a.State != team.StateTimeout || a.DecidedAt != 1_060_000 || a.DecidedBy != nil {
		t.Fatalf("after deadline: %+v", a)
	}
	if n := f.countOps("closed"); n != 1 {
		t.Fatalf("closed events = %d", n)
	}
	f.m.tick() // nothing open: no second event
	if n := f.countOps("closed"); n != 0 {
		t.Fatalf("extra closed events = %d", n)
	}
}

func TestTick_LeaseExpiryBecomesAbandoned(t *testing.T) {
	f := newFixture(t)
	f.create("id-1") // lease now+30 s, deadline now+540 s
	f.events()
	f.clock.Add(30_000)
	f.m.tick()
	if a, _, _ := f.m.store.Get("id-1"); a.State != team.StateAbandoned {
		t.Fatalf("after lease expiry: %+v", a)
	}
	if n := f.countOps("closed"); n != 1 {
		t.Fatalf("closed events = %d", n)
	}
}

func TestTick_OriginGoneIsCheckedEveryTenthTick(t *testing.T) {
	f := newFixture(t)
	f.create("id-1")
	f.events()
	f.origins.markDead("sid-1")
	for i := 1; i <= 9; i++ {
		f.m.tick()
		if a, _, _ := f.m.store.Get("id-1"); a.State != team.StateOpen {
			t.Fatalf("tick %d: liveness must only run on the 10th tick, got %s", i, a.State)
		}
	}
	f.m.tick()
	if a, _, _ := f.m.store.Get("id-1"); a.State != team.StateAbandoned {
		t.Fatalf("10th tick: %+v", a)
	}
	if n := f.countOps("closed"); n != 1 {
		t.Fatalf("closed events = %d", n)
	}
}

// Review Focus 4: a decide, the sweeper past the deadline and the
// requester's DELETE all race for one close. Exactly one wins, the row
// says which, every loser is answered with the winner's row (decided_by
// included when a decide won), and exactly one closed event is broadcast.
// Run with -race.
func TestTick_DecideSweeperAndCancelCloseOnce(t *testing.T) {
	f := newFixture(t)
	req := f.createReq("id-1")
	req.WaitS = 60
	if code, _ := f.do(http.MethodPost, "/api/team/approvals", req); code != 201 {
		t.Fatal("create")
	}
	f.events()
	f.clock.Add(60_000)
	f.do(http.MethodGet, "/api/team/approvals/id-1?wait=0", nil) // keep the lease alive: the race is deadline vs decide vs cancel
	client := team.Client{Kind: "app", Label: "Purdex.app @ air26"}
	var wg sync.WaitGroup
	var decideCode, deleteCode int
	var decideBody, deleteBody []byte
	wg.Add(3)
	go func() { defer wg.Done(); f.m.tick() }()
	go func() {
		defer wg.Done()
		decideCode, decideBody = f.do(http.MethodPost, "/api/team/approvals/id-1/decide",
			team.DecideRequest{Decision: "approve", Client: client})
	}()
	go func() {
		defer wg.Done()
		deleteCode, deleteBody = f.do(http.MethodDelete, "/api/team/approvals/id-1", nil)
	}()
	wg.Wait()

	final, _, _ := f.m.store.Get("id-1")
	if final.State == team.StateOpen || final.DecidedAt != 1_060_000 {
		t.Fatalf("row after three closes: %+v", final)
	}
	// Each competitor's own answer says whether it won; exactly one may.
	decideWon := decideCode == 200
	sweeperWon := final.State == team.StateTimeout
	if deleteCode != 200 {
		t.Fatalf("delete: %d %s (DELETE answers the row as it is, won or lost)", deleteCode, deleteBody)
	}
	deleted := decodeApproval(t, deleteBody)
	cancelWon := deleted.State == team.StateCancelled
	wins := 0
	for _, w := range []bool{decideWon, sweeperWon, cancelWon} {
		if w {
			wins++
		}
	}
	if wins != 1 {
		t.Fatalf("winners = %d (decide=%v sweeper=%v cancel=%v), want exactly one; row %s", wins, decideWon, sweeperWon, cancelWon, final.State)
	}
	switch {
	case decideWon && (final.State != team.StateApproved || final.DecidedBy == nil || final.DecidedBy.Label != client.Label):
		t.Fatalf("decide won but row is %+v", final)
	case cancelWon && (final.State != team.StateCancelled || final.DecidedBy != nil):
		t.Fatalf("cancel won but row is %+v", final)
	case sweeperWon && final.DecidedBy != nil:
		t.Fatalf("sweeper won but row carries decided_by: %+v", final)
	}
	// Losers carry the full winner row: the decide loser inside its 409
	// already_decided, the DELETE loser as its 200 body.
	if !decideWon {
		e := decodeErr(t, decideBody)
		if decideCode != 409 || e.Error != team.ErrAlreadyDecided || e.Approval == nil ||
			e.Approval.ID != "id-1" || e.Approval.State != final.State || e.Approval.DecidedAt != final.DecidedAt {
			t.Fatalf("decide lost: %d %s, want 409 already_decided carrying the %s row", decideCode, decideBody, final.State)
		}
	}
	if !cancelWon {
		if deleted.ID != "id-1" || deleted.State != final.State || deleted.DecidedAt != final.DecidedAt {
			t.Fatalf("delete lost: %s, want the %s row", deleteBody, final.State)
		}
		if decideWon && (deleted.DecidedBy == nil || deleted.DecidedBy.Label != client.Label) {
			t.Fatalf("delete lost to a decide but its body has no decided_by: %s", deleteBody)
		}
	}
	if n := f.countOps("closed"); n != 1 {
		t.Fatalf("closed events = %d, want exactly 1 (final state %s)", n, final.State)
	}
}

// The loser shapes, deterministically (the race above shows one ordering
// per run — measured over 50 runs the in-process tick wins almost always):
// a DELETE after a decide answers 200 with the approved row, decided_by and
// grant included; a decide after a DELETE answers 409 already_decided with
// the cancelled row; the sweeper past both deadlines changes neither; and
// each request broadcast closed exactly once.
func TestClose_LosersCarryTheWinnerRow(t *testing.T) {
	f := newFixture(t)
	f.create("id-1")
	second := f.createReq("id-2")
	second.OriginInbox = "/tmp/20.sock"
	if code, _ := f.do(http.MethodPost, "/api/team/approvals", second); code != 201 {
		t.Fatal("second create")
	}
	f.events()
	client := team.Client{Kind: "app", Label: "Purdex.app @ air26"}

	// id-1: decide wins, DELETE loses.
	if code, _ := f.do(http.MethodPost, "/api/team/approvals/id-1/decide", team.DecideRequest{Decision: "approve", Client: client}); code != 200 {
		t.Fatalf("decide id-1: %d", code)
	}
	code, body := f.do(http.MethodDelete, "/api/team/approvals/id-1", nil)
	if a := decodeApproval(t, body); code != 200 || a.State != team.StateApproved || a.DecidedBy == nil || a.DecidedBy.Label != client.Label || a.Grant == nil {
		t.Fatalf("delete after decide: %d %s (want the approved row with decided_by and grant)", code, body)
	}

	// id-2: DELETE wins, decide loses.
	if code, body := f.do(http.MethodDelete, "/api/team/approvals/id-2", nil); code != 200 || decodeApproval(t, body).State != team.StateCancelled {
		t.Fatalf("delete id-2: %d %s", code, body)
	}
	code, body = f.do(http.MethodPost, "/api/team/approvals/id-2/decide", team.DecideRequest{Decision: "deny", Client: client})
	e := decodeErr(t, body)
	if code != 409 || e.Error != team.ErrAlreadyDecided || e.Approval == nil || e.Approval.ID != "id-2" || e.Approval.State != team.StateCancelled || e.Approval.DecidedBy != nil {
		t.Fatalf("decide after delete: %d %s (want 409 already_decided carrying the cancelled row)", code, body)
	}

	// The sweeper past both deadlines (540 s) finds nothing open.
	f.clock.Add(600_000)
	f.m.tick()
	if a, _, _ := f.m.store.Get("id-1"); a.State != team.StateApproved {
		t.Fatalf("sweeper changed a closed row: %+v", a)
	}
	if a, _, _ := f.m.store.Get("id-2"); a.State != team.StateCancelled {
		t.Fatalf("sweeper changed a closed row: %+v", a)
	}
	if n := f.countOps("closed"); n != 2 {
		t.Fatalf("closed events = %d, want exactly 2 (one per request)", n)
	}
}
```

- [ ] **Step 2: Run it and see it fail.**
  - Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/lead-team && go test ./internal/module/team/ -run 'TestTick|TestClose_' -v`
  - Expected: build failure —
    ```
    internal/module/team/sweeper_test.go:21:6: f.m.tick undefined (type *Module has no field or method tick)
    ```

- [ ] **Step 3: Implement `sweeper.go`.**

```go
package teammod

import (
	"time"

	"github.com/wake/purdex/internal/team"
)

// sweepInterval is the sweeper's tick; livenessEvery is the tick multiple
// on which it also asks the origin resolver whether each requesting
// session is still alive (PD5: a registry read forks ps per entry, so not
// every second).
const (
	sweepInterval = time.Second
	livenessEvery = 10
)

// runSweeper ticks until Stop.
func (m *Module) runSweeper() {
	defer m.sweepWG.Done()
	ticker := time.NewTicker(sweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-m.stopCtx.Done():
			return
		case <-ticker.C:
			m.tick()
		}
	}
}

// tick closes what is overdue: a passed deadline is a timeout (U7), an
// expired lease is an abandonment, and — every livenessEvery-th tick, only
// while something is open — a vanished origin session is one too. A
// resolver that cannot read the registry answers "live" (origin_resolver.go),
// so a read error never abandons anything. Each close goes through closeAs,
// so it competes fairly with decide and cancel and broadcasts once.
func (m *Module) tick() {
	m.tickN++
	open, err := m.store.ListOpen()
	if err != nil {
		m.logf("[team] sweep: %v", err)
		return
	}
	if len(open) == 0 {
		return
	}
	now := m.now()
	checkLive := m.tickN%livenessEvery == 0
	for _, a := range open {
		var state team.State
		switch {
		case a.DeadlineAt <= now:
			state = team.StateTimeout
		case a.LeaseUntil <= now:
			state = team.StateAbandoned
		case checkLive && !m.origins.LiveSession(a.Origin.SessionID):
			state = team.StateAbandoned
		default:
			continue
		}
		after, won, err := m.closeAs(a.ID, Close{State: state, DecidedAt: now})
		if err != nil {
			m.logf("[team] sweep %s: %v", a.ID, err)
			continue
		}
		if won {
			m.logf("[team] approval %s %s by the sweeper (origin %s)", a.ID, after.State, a.Origin.Ref)
		}
	}
}
```

- [ ] **Step 4: Run and see it pass (race detector on: `TestTick_DecideSweeperAndCancelCloseOnce` runs `tick`, `decide` and `DELETE` concurrently).**
  - Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/lead-team && go test ./internal/module/team/ -run 'TestTick|TestClose_' -race -count=1 -v`
  - Expected:
    ```
    --- PASS: TestTick_DeadlineBecomesTimeout (0.01s)
    --- PASS: TestTick_LeaseExpiryBecomesAbandoned (0.01s)
    --- PASS: TestTick_OriginGoneIsCheckedEveryTenthTick (0.02s)
    --- PASS: TestTick_DecideSweeperAndCancelCloseOnce (0.01s)
    --- PASS: TestClose_LosersCarryTheWinnerRow (0.02s)
    ok  	github.com/wake/purdex/internal/module/team
    ```
  - Run the race fifty times: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/lead-team && go test ./internal/module/team/ -run TestTick_DecideSweeperAndCancelCloseOnce -race -count=50` → `ok`. (Measured 2026-10-07 in the scratch build: the in-process `tick` won 49 of 50, the DELETE once; that is why the loser shapes are also pinned sequentially in `TestClose_LosersCarryTheWinnerRow`.)
  - Mutation check (Review Focus 4): in `closeAs`, broadcast and wake **outside** the `if won` guard and rerun — `TestTick_DecideSweeperAndCancelCloseOnce` fails with `closed events = 2, want exactly 1` (the decide loser returns 409 before `closeAs`, so two of the three reach the broadcast). Put it back.

- [ ] **Step 5: Commit.**
  ```bash
  git add internal/module/team/sweeper.go internal/module/team/sweeper_test.go
  git commit -m "feat(team): sweeper closes overdue approval requests (timeout, abandoned)

  Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
  ```

---

### Task 2.6: `Start` (boot grace, snapshot, sweeper), `Close`, and the `cmd/pdx` registration

**Files:**
- Modify: `internal/module/team/module.go` (the `Start` stub from Task 2.4 → full body; add `sendSnapshot` after `broadcast`)
- Modify: `cmd/pdx/main.go:33-34` (import), `cmd/pdx/main.go:365-366` (`c.AddModule(teammod.New())` after `codexbroker.New()`)
- Test: `internal/module/team/module_test.go`, `cmd/pdx/team_register_test.go`

**Interfaces:**
- Produces:
  ```go
  func (m *Module) Start(context.Context) error               // ExtendOpenLeases(now+30s); Events.OnSubscribe(m.sendSnapshot); go runSweeper
  func (m *Module) sendSnapshot(sub *core.EventSubscriber)    // {op:"snapshot", approvals:[...]} via TrySend; on a full buffer Events.Remove(sub) unless sub.Done()
  ```
- Consumes: `team.BootGraceS`, `core.EventsBroadcaster.OnSubscribe`, `HandleHostEvents` (the test dials a real WebSocket, since OnSubscribe callbacks run only there — `events.go:243-250`).

- [ ] **Step 1: Write the failing tests.** `internal/module/team/module_test.go`:

```go
package teammod

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/team"
)

// readSnapshot connects a real WS subscriber to the fixture's broadcaster
// and returns the first approval.request frame it receives: OnSubscribe
// callbacks only run through HandleHostEvents, never for a test subscriber.
func readSnapshot(t *testing.T, f *fixture) team.EventValue {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(f.core.Events.HandleHostEvents))
	defer srv.Close()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("read snapshot: %v", err)
		}
		var ev core.HostEvent
		if err := json.Unmarshal(raw, &ev); err != nil {
			t.Fatal(err)
		}
		if ev.Type != team.EventType {
			continue
		}
		var v team.EventValue
		if err := json.Unmarshal([]byte(ev.Value), &v); err != nil {
			t.Fatal(err)
		}
		if v.Op == "snapshot" && !strings.Contains(ev.Value, `"approvals":[`) {
			t.Fatalf("snapshot must carry an array: %s", ev.Value)
		}
		return v
	}
}

func TestStart_ExtendsLeasesAndSnapshotsOpenRequests(t *testing.T) {
	f := newFixture(t)
	f.create("id-1") // lease 1_030_000
	f.events()
	f.clock.Add(25_000) // boot at 1_025_000: grace → 1_055_000 > 1_030_000
	if err := f.m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	a, _, _ := f.m.store.Get("id-1")
	if a.LeaseUntil != 1_055_000 {
		t.Fatalf("lease after boot = %d, want 1055000 (boot + 30 s)", a.LeaseUntil)
	}
	snap := readSnapshot(t, f)
	if snap.Op != "snapshot" || len(snap.Approvals) != 1 || snap.Approvals[0].ID != "id-1" || snap.Approvals[0].LeaseUntil != 1_055_000 {
		t.Fatalf("snapshot = %+v", snap)
	}
	f.do(http.MethodDelete, "/api/team/approvals/id-1", nil)
	if snap := readSnapshot(t, f); snap.Op != "snapshot" || len(snap.Approvals) != 0 || snap.Approvals == nil {
		t.Fatalf("empty snapshot = %+v (approvals must be [] not null)", snap)
	}
	if err := f.m.Stop(context.Background()); err != nil { // joins the sweeper
		t.Fatal(err)
	}
}

func TestSendSnapshot_FullBufferClosesSubscriber(t *testing.T) {
	f := newFixture(t)
	sub := f.core.Events.AddTestSubscriber()
	for i := 0; i < 64; i++ { // the send buffer is 64 deep
		sub.TrySend([]byte("x"))
	}
	f.m.sendSnapshot(sub)
	select {
	case <-sub.Done():
	default:
		t.Fatal("a subscriber that cannot take the snapshot must be removed so the client reconnects")
	}
}
```

  and `cmd/pdx/team_register_test.go` (same shape as `monitor_module_test.go` and `removed_routes_test.go`; `newTestCore`/`doRequest` are in `http_chain_test.go:45-62`):

```go
package main

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/config"
)

// TestRegisterServeModules_MountsTeam: the team module is mounted, its Init
// succeeds under the real wiring (peers registers the origin resolver
// before it, by dependency order), team.db is created in the data dir and
// its routes are live through the real outer chain.
func TestRegisterServeModules_MountsTeam(t *testing.T) {
	dataDir := t.TempDir()
	c := newTestCore(&config.Config{DataDir: dataDir, Token: "t"})
	require.NoError(t, registerServeModules(c, nil, nil))
	assert.True(t, c.Mounted("team"))
	require.NoError(t, c.InitModules())

	mux := http.NewServeMux()
	c.RegisterCoreRoutes(mux)
	c.RegisterRoutes(mux)
	outer := newOuterHandler(c, mux, nil)

	res := doRequest(t, outer, http.MethodGet, "/api/team/approvals?state=open", "t")
	assert.Equal(t, http.StatusOK, res.Code, res.Body.String())
	assert.JSONEq(t, `{"approvals":[]}`, res.Body.String())
	assert.Equal(t, http.StatusUnauthorized, doRequest(t, outer, http.MethodGet, "/api/team/approvals", "").Code)
	_, err := os.Stat(filepath.Join(dataDir, "team.db"))
	assert.NoError(t, err, "team.db must be created in the data dir")
	require.NoError(t, c.CloseModules())
}
```

- [ ] **Step 2: Run them and see them fail.**
  - Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/lead-team && go test ./internal/module/team/ -run 'TestStart|TestSendSnapshot' -v`
  - Expected: build failure —
    ```
    internal/module/team/module_test.go:85:6: f.m.sendSnapshot undefined (type *Module has no field or method sendSnapshot)
    ```
  - Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/lead-team && go test ./cmd/pdx/ -run TestRegisterServeModules_MountsTeam -v`
  - Expected: `--- FAIL: TestRegisterServeModules_MountsTeam` with `team_register_test.go:23: Should be true` and `team_register_test.go:32: Not equal: expected: 200 actual: 404`.

- [ ] **Step 3: Implement.** In `internal/module/team/module.go`, replace the two lines
  ```go
  // Start is filled in by Task 2.6.
  func (m *Module) Start(context.Context) error { return nil }
  ```
  with:

```go
// Start applies the boot lease grace (spec §9.2), registers the snapshot
// for new subscribers and starts the sweeper.
func (m *Module) Start(context.Context) error {
	n, err := m.store.ExtendOpenLeases(m.now() + team.BootGraceS*1000)
	if err != nil {
		return fmt.Errorf("team: %w", err)
	}
	if n > 0 {
		m.logf("[team] boot: extended the lease of %d open approval request(s) by %ds", n, team.BootGraceS)
	}
	m.core.Events.OnSubscribe(m.sendSnapshot)
	m.sweepWG.Add(1)
	go m.runSweeper()
	m.logf("[team] endpoints enabled")
	return nil
}
```

  Then add, directly after `func (m *Module) broadcast(...)` and before `// addWaiter registers`:

```go
// sendSnapshot queues {op:"snapshot", approvals:[…]} to a new subscriber.
// A subscriber whose buffer is already full is closed so it reconnects
// (as session/module.go does); one already removed is left alone.
func (m *Module) sendSnapshot(sub *core.EventSubscriber) {
	open, err := m.store.ListOpen()
	if err != nil {
		m.logf("[team] OnSubscribe list error: %v", err)
		return
	}
	v, err := json.Marshal(team.EventValue{Op: "snapshot", Approvals: open})
	if err != nil {
		m.logf("[team] encode snapshot: %v", err)
		return
	}
	data, err := json.Marshal(core.HostEvent{Type: team.EventType, Value: string(v)})
	if err != nil {
		m.logf("[team] encode snapshot event: %v", err)
		return
	}
	if sub.TrySend(data) {
		return
	}
	select {
	case <-sub.Done():
	default:
		m.logf("[team] OnSubscribe snapshot could not be queued (send buffer full); closing the connection so the client reconnects")
		m.core.Events.Remove(sub)
	}
}
```

  In `cmd/pdx/main.go`, the import block (lines 33-34) currently reads
  ```go
  	"github.com/wake/purdex/internal/module/session"
  	"github.com/wake/purdex/internal/store"
  ```
  — add the alias import between them:
  ```go
  	"github.com/wake/purdex/internal/module/session"
  	teammod "github.com/wake/purdex/internal/module/team"
  	"github.com/wake/purdex/internal/store"
  ```
  and in `registerServeModules` (line 365) after `c.AddModule(codexbroker.New())` add
  ```go
  	c.AddModule(teammod.New())
  ```
  (before the `c.CfgMu.RLock()` / nex block; order is irrelevant — `InitModules` topo-sorts, and `team` depends on `peers`, which is mounted above).

- [ ] **Step 4: Run and see it pass — the module, the new wiring test, the three existing wiring tests, the whole `cmd/pdx` package, `go vet`, `go build`.**
  - Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/lead-team && go test ./internal/module/team/ -race -count=1 -v`
  - Expected: all 17 tests `PASS` (`TestStart_ExtendsLeasesAndSnapshotsOpenRequests (0.01s)`, `TestSendSnapshot_FullBufferClosesSubscriber (0.01s)` among them), `ok  	github.com/wake/purdex/internal/module/team	2.6s`.
  - Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/lead-team && go test ./cmd/pdx/ -run 'TestRegisterServeModules|TestRemovedRoutesAre404' -count=1 -v`
  - Expected:
    ```
    --- PASS: TestRegisterServeModules_IncludesMonitorRoutes (0.01s)
    --- PASS: TestRegisterServeModules_NexDisabled (0.00s)
    --- PASS: TestRegisterServeModules_NexEnabled (0.00s)
    --- PASS: TestRemovedRoutesAre404 (0.00s)
    --- PASS: TestRegisterServeModules_MountsTeam (0.00s)
    ok  	github.com/wake/purdex/cmd/pdx
    ```
  - Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/lead-team && go test ./cmd/pdx/ -count=1` → `ok  	github.com/wake/purdex/cmd/pdx	8.5s` (measured 8.473 s).
  - Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/lead-team && go vet ./internal/team/ ./internal/module/team/ ./internal/module/peers/ ./cmd/pdx/ && go build ./... && gofmt -l internal/team internal/module/team internal/module/peers/origin_resolver.go cmd/pdx/main.go cmd/pdx/team_register_test.go` → no output.

- [ ] **Step 5: Commit.**
  ```bash
  git add internal/module/team/module.go internal/module/team/module_test.go cmd/pdx/main.go cmd/pdx/team_register_test.go
  git commit -m "feat(team): boot lease grace, OnSubscribe snapshot, sweeper start; mount the team module

  Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
  ```

---

### Deviations from the handoff notes

1. **`wire.go` is the preamble's text gofmt-aligned** (three `Approval` comment columns and one in `DecideRequest` moved). Whitespace only; the JSON names and types are unchanged.
2. **The resolver is a concrete exported type in peers, `*peers.OriginResolver`; the interface `teammod.OriginResolver` lives on the consumer side.** peers therefore imports only the leaf `internal/team` (for `team.Origin`) and never `internal/module/team` — no cycle, and the team module's tests inject a fake through the registry key.
3. **PD5's "a registry read error never abandons" lives in the resolver, not the sweeper:** `LiveSession` is `bool` by contract, so it answers `true` on a read error (pinned by `TestOriginResolver_RegistryReadErrorIsUnknownNotDead`). A missing registry dir is an empty registry (`ReadRegistry`), so it does report sessions gone — correct: no registry means no live CC session.
4. **`ResolveOrigin` answers `false` → `400 origin_unknown` on a registry read error**, where peers' own `origin()` answers `503 not_ready`. The `(team.Origin, bool)` signature has no error channel; the CLI exits 1 either way, and the error is logged.
5. **Store signatures are richer than the notes' one-liners:** `Create` returns `(stored, storedHash, inserted, err)`; `CloseIfOpen` takes a `Close{State, DecidedAt, DecidedBy, Grant}` and returns the row *after* the attempt for losers too (that row is what `409 already_decided` carries); `ExtendOpenLeases` returns the count (logged at boot); `ListOpen` never returns nil (the snapshot and list bodies need `[]`).
6. **`500` bodies are JSON `{"error":"storage_error"}`**, a code outside `wire.go` (the contract says every non-2xx body is an `APIError`; it lists no 500 code). P2b should treat any unlisted code as exit 1.
7. **`reason` is required (`400 bad_request` when blank).** The notes do not say; spec §6.1 makes `--reason` a mandatory CLI flag and §6.3 shows it in the dialog.
8. **`decide` requires `client.kind` and `client.label`** (`400` otherwise). The notes do not say; spec §6.5 makes them the audit label that is broadcast and logged, so an empty one would defeat the layer that stays. P3 always sends both.
9. **A long-poll cut by `Stop` answers `200` with the row as it is (still `open`)**, not `503`. The notes say only "returns on `stopCancel`". The CLI then re-polls, meets the refused connection and enters its 30 s grace (spec §9.1); a `503` would reach the same place one step earlier but needs a second response shape in `handleGet`. Only `POST` create answers `503 not_ready` while stopping, as the routes table says.
10. **`createMu` serialises the `request_open` check with the insert.** Not in the notes; without it two different ids from one origin, posted at the same instant, both pass `OpenByOrigin` and both open.
11. **Long-poll waiters are registered before the row is read**, so a close between the read and the `select` cannot be missed. The notes describe the per-id `[]chan struct{}` but not the ordering.
12. **The sweeper tests renew the lease (a `GET ?wait=0`) before advancing to the deadline**, because a 30 s lease expires long before a 60 s deadline — exactly what `pdx lead request`'s polling does. First draft forgot this and the deadline test saw `abandoned`; the fix is in the tests, not the code.
13. **Added `cmd/pdx/team_register_test.go`** (the notes only said to run the three existing wiring tests): it pins `Mounted("team")`, `Init` under the real wiring, `team.db` in the data dir, `200 {"approvals":[]}` with the token and `401` without.
14. **`titles.go:31` (`findOriginEntry`) and `nex/module.go:417-424` from the notes are still correct**; `shutdown.go:161-186` is now `:161-187`; `session/module.go:247-266` still correct; `hostconfig/store.go:38-63` still correct. The notes' `OriginResolverKey` registration goes at `peers/module.go:361`.
15. **The sweeper's `tickN` increments on every tick even when nothing is open** (PD5's "only while requests are open" refers to the liveness *check*, which `tick` skips by returning early). So the 10th-tick cadence is wall-clock, not "10 ticks with something open".

### Size estimate

Measured in the scratch build (`wc -l`), new files + edits:

| Task | Files | Lines |
|---|---|---|
| 2.1 | `internal/team/wire.go` 164, `wire_test.go` 101 | 265 |
| 2.2 | `store.go` 259, `store_test.go` 168 | 427 |
| 2.3 | `origin_resolver.go` 68, `origin_resolver_test.go` 89, `peers/module.go` +3 | 160 |
| 2.4 | `module.go` (stub Start) ≈ 196, `handler.go` 363, `handler_test.go` 438 | ≈ 997 |
| 2.5 | `sweeper.go` 72, `sweeper_test.go` 205 | 277 |
| 2.6 | `module.go` +≈40, `module_test.go` 91, `team_register_test.go` 38, `cmd/pdx/main.go` +2 | ≈ 171 |
| **Total** | 13 new files, 2 modified | **≈ 2 300 lines** (re-measured 2026-10-07 after the codex plan round: inflight route and test, three-way CAS tests) |

That is 2.6× the 800-line budget, so **P2a must be split**. Tests are ≈ 45 % of it and are not optional (Review Focus 3 and 4, spec §15's mutation deliverable). Proposed split, each PR independently green and reviewable:

- **P2a-1 — foundation (Tasks 2.1, 2.2, 2.3): ≈ 811 lines, 7 files + 1 edit.** Wire contract, store, resolver. No routes, no behaviour change for a running daemon (the resolver is only registered). Borderline on lines (41 % tests); if the reviewer insists, move Task 2.3 (160 lines) to P2a-2.
- **P2a-2 — routes (Task 2.4): ≈ 958 lines, 3 files.** This is the one that cannot fit: the five handlers and their tests are a single behavioural unit. The clean seam, if it must be cut, is **create/list** (`handleCreate`, `handleList`, the fixture, `TestCreate_*`, `TestList_OpenOnly`, ≈ 600 lines) versus **get/delete/decide** (`handleGet`, `handleDelete`, `handleDecide`, `TestGet_*`, `TestDecide_*`, ≈ 360 lines) — the latter would then travel with P2a-3.
- **P2a-3 — sweeper, start, registration (Tasks 2.5, 2.6): ≈ 356 lines, 4 files + 1 edit** (≈ 716 if get/delete/decide move here).

Recommendation: three PRs — P2a-1 (2.1–2.3), P2a-2 (2.4 create/list + skeleton), P2a-3 (2.4 get/delete/decide + 2.5 + 2.6). All three stay under 800 lines; the module is only mounted in `cmd/pdx` by the last one, so a half-built route set never ships. If the coordinator prefers two PRs, take P2a-1 as is and accept P2a-2 = Tasks 2.4–2.6 at ≈ 1 310 lines.

### Open questions for the coordinator

None that the code or the spec cannot answer. Two decisions above are judgement calls P2b/P3 should know about rather than questions: deviation 9 (a `Stop`-cut long-poll answers `200 open`, not `503`) and deviation 6 (`500 storage_error` is outside `wire.go`). Overrule either in the handoff to P2b if you want a different shape; both are one-line changes in `handler.go`.

### Coordinator decisions on P2a (2026-10-07, `mlab/_81nu3d`)

- **Codex finding 1 (approval without a team) is a staged deferral, kept:** P2 closes a lead request as `approved` and stores the grant; P4 adds team creation in the same transaction, `already_lead` / `member_cannot_lead`, and `team_id` in `pdx lead request`'s stdout. Until P4 nothing advertises `pdx lead request` (the skill ships in P5b and tells agents when to use it), so no session can believe it is a lead. The wire already carries the two P4 error codes so the contract does not move again.
- **Split into three PRs**, each under 800 lines: **P2a-1** = Tasks 2.1, 2.2 (wire, store; ≈ 670 lines); **P2a-2** = Tasks 2.3 and 2.4 create/list with the module skeleton (≈ 760); **P2a-3** = Task 2.4 get/delete/decide/inflight, 2.5, 2.6 (≈ 790; the inflight route, its handler test and the three-way CAS race test from the codex plan round are counted). The module is mounted in `cmd/pdx` only by P2a-3, so no half-built route set ships.
- **Amendment from P3 (open question 1): `team.Origin` gains two optional fields**, `Title string \`json:"title,omitempty"\`` and `Address string \`json:"address,omitempty"\``, after `Tmux`. Applied in Task 2.1's `wire.go` and round-trip test, and in Task 2.3's fixture, test and `ResolveOrigin` (`titleOf`); `Title` comes from the peers title store (`m.titles.Snapshot()`, `titles.go:123`; `""` when `m.titles` is nil) and `Address` with the same formatter `GET /api/peers` uses for `PeerRecordWire.address` (`<alias>/<name>` for a routable name, else `<alias>/_<ref>`), and its test asserts both for the fixture's `n10` entry. The dialog (P3 Task 3.4) prefers them and falls back to `name` → `ref` when absent.
- **Deviations 1–15 are accepted** as written. Two of them bind the neighbours: a `Stop`-cut long-poll answers `200` with the row still `open` (P2b re-polls and meets the refused connection), and `500 storage_error` is outside `wire.go` (P2b maps any unlisted code to exit 1).

---

## PR P2b — CLI: `cmd/pdx/daemonclient` and `pdx lead request` (spec §6.1, §6.4, §9.1, §14)

One restart-aware HTTP client shared by every new CLI command, the exit-code constants of spec §14, and the first command on top of them: `pdx lead request`, which creates a lead approval request, long-polls it (renewing its lease on every poll) and maps the closed state to an exit code. Nothing in `internal/module/*` and nothing in the SPA. The daemon is stubbed with `httptest.Server`s speaking the preamble's routes.

**Precondition.** This PR consumes `internal/team/wire.go` (P2a Task 2.1). Branch it from main **after P2a merges** (or rebase onto P2a's branch); until then `go test ./cmd/pdx/...` fails to compile on `team.*`.

**Code facts verified on the worktree at `d39886e8` (go1.26.0, `go build ./...` clean):**

| Fact | Where |
|---|---|
| Subcommands are a `switch os.Args[1]` in `main()`; `msg` is `case "msg": runMsg(os.Args[2:])`, and `runMsg` is `os.Exit(runMsgCmd(args, os.Getenv, os.Stdout, os.Stderr))` | `cmd/pdx/main.go:45-81`, `cmd/pdx/msg.go:61-63` |
| The usage line lists commands as one string | `cmd/pdx/main.go:42` |
| `resolveDaemonHost(bind)` maps `""`, `0.0.0.0`, `::`, `[::]` → `127.0.0.1` | `cmd/pdx/statusline_proxy.go:145-151` |
| Token header is `Authorization: Bearer <cfg.Token>`; JSON bodies get `Content-Type: application/json` | `cmd/pdx/peers.go:1267-1270` |
| `config.Load(path)` falls back to `<DataDir>/config.toml`; `Config{Bind, Port, Token, DataDir}`; defaults `127.0.0.1:7860` | `internal/config/config.go:261-267, 283-285, 301-306` |
| `writeTestConfig(t, addr, token) string` accepts `srv.URL` (via `splitHostPort`) | `cmd/pdx/peers_test.go:349-377` |
| `fakeGetenv(kv map[string]string) func(string) string` | `cmd/pdx/msg_test.go:20-22` |
| Grammar-rejection test style: table, `--config cfgPath` appended, assert exit 2, stderr non-empty, stdout empty, zero requests via `atomic` counter | `cmd/pdx/msg_test.go:26-91` |
| `msgOriginInbox` prints through `renderMsgAPIError`, whose prefix is `pdx msg:` — not reusable for `lead` | `cmd/pdx/msg.go:377-387, 673-683` |
| The import rule as written in code: "cmd/pdx must not import that daemon package; the wire types themselves are shared via internal/peers" (`main.go` and `http_chain.go` do import `internal/module/*` for `serve`) | `cmd/pdx/msg.go:26-28`, `cmd/pdx/http_chain.go:9` |
| No import-architecture test exists in `cmd/pdx` or `internal/*` | `grep` for `go/parser`, `must not import` in `*_test.go`: none |
| `PairingGuard` answers `503` `{"reason":"pairing_mode"}` with `Content-Type: application/json` | `internal/middleware/pairing_guard.go:24-26` |
| `GET /api/health` is outside TokenAuth and PairingGuard; body `{"ok","mode","version","hash","boot_id"}` | `cmd/pdx/http_chain.go:32`, `internal/core/info_handler.go:20-29` |
| Restart handler: `503` `{"error":"shutting_down"}` | `internal/core/restart.go:71-72` |
| Peers send: `503` `ipeers.APIError{Error:"not_ready",Detail}`, i.e. `{"error":"not_ready","detail":…}` | `internal/module/peers/send.go:294`, `internal/peers/wire.go:186` |
| `TokenAuth` 401 and `IPWhitelist` 403 are `http.Error` plain text (`unauthorized\n`, `forbidden\n`) | `internal/middleware/middleware.go:32, 47, 93` |
| Unknown route through the general chain reaches the inner `http.ServeMux` → `404`, `text/plain; charset=utf-8`, body `"404 page not found\n"`; a method mismatch is `405` `"Method Not Allowed\n"` | measured with a scratch program on go1.26.0 |
| `http.Client` error chains: closed port → `errors.Is(err, syscall.ECONNREFUSED)` and `errors.As(&net.OpError)` both true; hijack-and-close → `errors.Is(err, io.EOF)` true, `*net.OpError` false; per-request deadline → `errors.Is(err, context.DeadlineExceeded)` true, `*net.OpError` false; cancelled → `context.Canceled` | measured, same program |
| An `httptest.Server` can be closed and a new one started on the same `127.0.0.1:<port>` (`net.Listen` succeeds at once) | measured, same program |
| `signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)` precedent | `cmd/pdx/msg_selftest.go:211-212` |
| `github.com/google/uuid v1.6.0` is a direct dependency | `go.mod:23` |
| `sanitizeCell(s string) string` escapes non-printable runes for the terminal | `cmd/pdx/peers.go:54` |
| `pdx start [--config <path>]` forks `pdx serve <same args>`; `serve` has `--config`, `--bind`, `--port`, `--quick`; pid file and logs live under `cfg.DataDir` | `cmd/pdx/daemon.go:240-300`, `cmd/pdx/main.go:97-102` |

### Task 2b.1: `cmd/pdx/daemonclient` — the restart-aware client

**Files:**
- Create: `cmd/pdx/daemonclient/client.go`
- Test: `cmd/pdx/daemonclient/client_test.go`

**Interfaces:**
- Consumes (from `internal/team/wire.go`, P2a): `team.APIError`, `team.ErrNotReady`, `team.BootGraceS`.
- Produces:
  ```go
  package daemonclient

  // Grace is how long Do keeps retrying after the first failure (spec §9.1).
  const Grace = time.Duration(team.BootGraceS) * time.Second

  // MsgRestarting is printed once per Client, on the first retryable failure.
  const MsgRestarting = "daemon 重啟中，繼續等待…"

  var (
  	ErrUnavailable = errors.New("daemon_unavailable") // the grace ran out
  	ErrUnsupported = errors.New("unsupported")        // plain-text 404: an older daemon
  )

  // StatusError is a non-2xx answer that is not a retry signal.
  type StatusError struct {
  	Status int
  	API    team.APIError // decoded when the body was JSON with an "error" field
  	Body   []byte        // the raw body, bounded, for messages when API.Error == ""
  }
  func (e *StatusError) Error() string

  type Option func(*Client)
  func WithHTTPClient(h *http.Client) Option
  func WithClock(now func() time.Time, sleep func(context.Context, time.Duration) error) Option
  func WithStderr(w io.Writer) Option

  type Client struct { /* unexported */ }
  func New(baseURL, token string, opts ...Option) *Client

  // Do sends one request and retries it through a daemon restart.
  // body (nil or any JSON-encodable value) is sent as application/json; a 2xx
  // body is decoded into out when out != nil.
  func (c *Client) Do(ctx context.Context, method, path string, body, out any) (status int, err error)

  // Once sends exactly one request with no retry and no restart bookkeeping.
  func (c *Client) Once(ctx context.Context, method, path string, body, out any) (status int, err error)
  ```

**Behaviour of `Do`** (spec §9.1, handoff notes):
- Retryable: `ECONNREFUSED`, `ECONNRESET`, `io.EOF`, `io.ErrUnexpectedEOF`, any `*net.OpError` whose chain is not a context error, and `503` whose JSON `error` is `shutting_down` or `not_ready`.
- Not retryable: `503 {"reason":"pairing_mode"}` (no `error` field), every other status, context errors, and decode errors.
- Before the first request of a Client's life, and before every retry, `Do` asks `GET /api/health` for `boot_id`. The first answer is the baseline. A later different answer prints `daemon 已重新啟動（boot <id>）` once per Client. A health probe that fails retryably counts as a failure like any other; a non-200 health answer is ignored (daemon up, boot unknown).
- Backoff 250 ms, 500 ms, then 1 s repeated. Grace: 30 s from the first failure, measured with the injected clock; when a failure lands at or past the grace, `Do` returns `ErrUnavailable`. `MsgRestarting` is printed once per Client, on the first failure.
- A plain-text 404 (`text/plain`, body `404 page not found`) is `ErrUnsupported`; a JSON 404 is `(404, *StatusError{API.Error:"not_found"})`.

- [ ] **Step 1: Write the failing tests.**

```go
package daemonclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wake/purdex/internal/team"
)

// fakeClock advances only when the client sleeps, so no test waits for real
// time. onSleep (optional) runs after each sleep with its 1-based index.
type fakeClock struct {
	mu      sync.Mutex
	t       time.Time
	sleeps  []time.Duration
	onSleep func(n int)
}

func newFakeClock() *fakeClock {
	return &fakeClock{t: time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)}
}

func (f *fakeClock) now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.t
}

func (f *fakeClock) sleep(ctx context.Context, d time.Duration) error {
	f.mu.Lock()
	f.t = f.t.Add(d)
	f.sleeps = append(f.sleeps, d)
	n := len(f.sleeps)
	hook := f.onSleep
	f.mu.Unlock()
	if hook != nil {
		hook(n)
	}
	return ctx.Err()
}

func (f *fakeClock) elapsed(since time.Time) time.Duration { return f.now().Sub(since) }

// fakeDaemon answers /api/health with bootID and everything else with next.
// It counts hits per path.
type fakeDaemon struct {
	mu     sync.Mutex
	bootID string
	hits   map[string]int
	next   http.HandlerFunc
}

func newFakeDaemon(bootID string, next http.HandlerFunc) *fakeDaemon {
	return &fakeDaemon{bootID: bootID, hits: map[string]int{}, next: next}
}

func (f *fakeDaemon) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.hits[r.URL.Path]++
	boot := f.bootID
	f.mu.Unlock()
	if r.URL.Path == "/api/health" {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "boot_id": boot})
		return
	}
	f.next(w, r)
}

func (f *fakeDaemon) hitsFor(path string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.hits[path]
}

// serveOn starts an httptest.Server on a fixed address so a test can close
// it and start another on the same port (a daemon restart).
func serveOn(t *testing.T, addr string, h http.Handler) *httptest.Server {
	t.Helper()
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("listen %s: %v", addr, err)
	}
	srv := httptest.NewUnstartedServer(h)
	srv.Listener.Close()
	srv.Listener = ln
	srv.Start()
	return srv
}

func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	return addr
}

func okJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func newTestClient(base string, clock *fakeClock, stderr io.Writer) *Client {
	return New(base, "tok", WithClock(clock.now, clock.sleep), WithStderr(stderr),
		WithHTTPClient(&http.Client{Transport: &http.Transport{}}))
}

func TestDo_SendsAuthAndJSONAndDecodes(t *testing.T) {
	var gotAuth, gotCT string
	var gotBody []byte
	d := newFakeDaemon("b1", func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotCT = r.Header.Get("Content-Type")
		gotBody, _ = io.ReadAll(r.Body)
		okJSON(w, http.StatusCreated, team.Approval{ID: "r1", State: team.StateOpen})
	})
	srv := httptest.NewServer(d)
	defer srv.Close()
	clock := newFakeClock()
	var stderr bytes.Buffer
	c := newTestClient(srv.URL, clock, &stderr)

	var ap team.Approval
	status, err := c.Do(context.Background(), http.MethodPost, "/api/team/approvals",
		team.CreateApprovalRequest{ID: "r1", Kind: team.KindLead}, &ap)
	if err != nil || status != http.StatusCreated {
		t.Fatalf("status=%d err=%v", status, err)
	}
	if gotAuth != "Bearer tok" || gotCT != "application/json" {
		t.Errorf("auth=%q ct=%q", gotAuth, gotCT)
	}
	if !strings.Contains(string(gotBody), `"id":"r1"`) || !strings.Contains(string(gotBody), `"kind":"lead"`) {
		t.Errorf("body = %s", gotBody)
	}
	if ap.ID != "r1" || ap.State != team.StateOpen {
		t.Errorf("decoded = %+v", ap)
	}
	if d.hitsFor("/api/health") != 1 {
		t.Errorf("health probes = %d, want 1 (baseline boot id)", d.hitsFor("/api/health"))
	}
	if len(clock.sleeps) != 0 || stderr.Len() != 0 {
		t.Errorf("no failure: sleeps=%v stderr=%q", clock.sleeps, stderr.String())
	}
}

func TestDo_RefusedThenNewBootIDThenSucceeds(t *testing.T) {
	addr := freeAddr(t)
	first := serveOn(t, addr, newFakeDaemon("b1", func(w http.ResponseWriter, r *http.Request) {
		okJSON(w, http.StatusOK, team.Approval{ID: "r1", State: team.StateOpen})
	}))
	clock := newFakeClock()
	var stderr bytes.Buffer
	c := newTestClient("http://"+addr, clock, &stderr)

	var ap team.Approval
	if _, err := c.Do(context.Background(), http.MethodGet, "/api/team/approvals/r1?wait=25", nil, &ap); err != nil {
		t.Fatalf("first call: %v", err)
	}
	first.Close() // the daemon goes down

	var second *httptest.Server
	var once sync.Once
	clock.onSleep = func(n int) {
		if n == 2 {
			once.Do(func() {
				second = serveOn(t, addr, newFakeDaemon("b2", func(w http.ResponseWriter, r *http.Request) {
					okJSON(w, http.StatusOK, team.Approval{ID: "r1", State: team.StateApproved})
				}))
			})
		}
	}
	status, err := c.Do(context.Background(), http.MethodGet, "/api/team/approvals/r1?wait=25", nil, &ap)
	if second != nil {
		defer second.Close()
	}
	if err != nil || status != http.StatusOK || ap.State != team.StateApproved {
		t.Fatalf("status=%d err=%v ap=%+v", status, err, ap)
	}
	want := []time.Duration{250 * time.Millisecond, 500 * time.Millisecond}
	if len(clock.sleeps) != len(want) || clock.sleeps[0] != want[0] || clock.sleeps[1] != want[1] {
		t.Errorf("sleeps = %v, want %v", clock.sleeps, want)
	}
	out := stderr.String()
	if strings.Count(out, MsgRestarting) != 1 {
		t.Errorf("restart line count = %d in %q", strings.Count(out, MsgRestarting), out)
	}
	if strings.Count(out, "daemon 已重新啟動（boot b2）") != 1 {
		t.Errorf("restarted line missing or repeated: %q", out)
	}
}

func TestDo_503ShuttingDownAndNotReadyAreRetried(t *testing.T) {
	var calls int
	var mu sync.Mutex
	d := newFakeDaemon("b1", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		n := calls
		mu.Unlock()
		switch n {
		case 1:
			okJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "shutting_down"})
		case 2:
			okJSON(w, http.StatusServiceUnavailable, team.APIError{Error: team.ErrNotReady, Detail: "stopping"})
		default:
			okJSON(w, http.StatusOK, team.Approval{ID: "r1", State: team.StateDenied})
		}
	})
	srv := httptest.NewServer(d)
	defer srv.Close()
	clock := newFakeClock()
	var stderr bytes.Buffer
	c := newTestClient(srv.URL, clock, &stderr)

	var ap team.Approval
	status, err := c.Do(context.Background(), http.MethodGet, "/api/team/approvals/r1", nil, &ap)
	if err != nil || status != http.StatusOK || ap.State != team.StateDenied {
		t.Fatalf("status=%d err=%v ap=%+v", status, err, ap)
	}
	if len(clock.sleeps) != 2 {
		t.Errorf("sleeps = %v, want two", clock.sleeps)
	}
	if strings.Count(stderr.String(), MsgRestarting) != 1 {
		t.Errorf("stderr = %q", stderr.String())
	}
	if strings.Contains(stderr.String(), "已重新啟動") {
		t.Errorf("boot id did not change, must not print restarted: %q", stderr.String())
	}
}

func TestDo_PairingModeIsNotRetried(t *testing.T) {
	d := newFakeDaemon("b1", func(w http.ResponseWriter, r *http.Request) {
		okJSON(w, http.StatusServiceUnavailable, map[string]string{"reason": "pairing_mode"})
	})
	srv := httptest.NewServer(d)
	defer srv.Close()
	clock := newFakeClock()
	var stderr bytes.Buffer
	c := newTestClient(srv.URL, clock, &stderr)

	status, err := c.Do(context.Background(), http.MethodGet, "/api/team/approvals/r1", nil, nil)
	var se *StatusError
	if status != http.StatusServiceUnavailable || !errors.As(err, &se) || se.API.Error != "" {
		t.Fatalf("status=%d err=%v", status, err)
	}
	if !strings.Contains(string(se.Body), "pairing_mode") {
		t.Errorf("body = %s", se.Body)
	}
	if d.hitsFor("/api/team/approvals/r1") != 1 || len(clock.sleeps) != 0 || stderr.Len() != 0 {
		t.Errorf("retried: hits=%d sleeps=%v stderr=%q", d.hitsFor("/api/team/approvals/r1"), clock.sleeps, stderr.String())
	}
}

func TestDo_GraceExpiresIntoErrUnavailable(t *testing.T) {
	addr := freeAddr(t) // nothing listens
	clock := newFakeClock()
	start := clock.now()
	var stderr bytes.Buffer
	c := newTestClient("http://"+addr, clock, &stderr)

	status, err := c.Do(context.Background(), http.MethodGet, "/api/team/approvals/r1", nil, nil)
	if !errors.Is(err, ErrUnavailable) || status != 0 {
		t.Fatalf("status=%d err=%v, want ErrUnavailable", status, err)
	}
	if clock.elapsed(start) < Grace {
		t.Errorf("gave up after %v, want >= %v", clock.elapsed(start), Grace)
	}
	if len(clock.sleeps) < 3 || clock.sleeps[0] != 250*time.Millisecond || clock.sleeps[1] != 500*time.Millisecond {
		t.Fatalf("sleeps = %v", clock.sleeps)
	}
	for i, d := range clock.sleeps[2:] {
		if d != time.Second {
			t.Errorf("sleep %d = %v, want 1s", i+2, d)
		}
	}
	if strings.Count(stderr.String(), MsgRestarting) != 1 {
		t.Errorf("stderr = %q", stderr.String())
	}
}

func TestDo_ContextCancelWinsOverGrace(t *testing.T) {
	addr := freeAddr(t)
	clock := newFakeClock()
	ctx, cancel := context.WithCancel(context.Background())
	clock.onSleep = func(n int) {
		if n == 1 {
			cancel()
		}
	}
	c := newTestClient("http://"+addr, clock, io.Discard)

	_, err := c.Do(ctx, http.MethodGet, "/api/team/approvals/r1", nil, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if len(clock.sleeps) != 1 {
		t.Errorf("sleeps = %v, want one", clock.sleeps)
	}
}

func TestDo_PlainNotFoundIsUnsupported(t *testing.T) {
	mux := http.NewServeMux() // an older daemon: health only, no /api/team
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		okJSON(w, http.StatusOK, map[string]any{"ok": true, "boot_id": "b1"})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	clock := newFakeClock()
	c := newTestClient(srv.URL, clock, io.Discard)

	status, err := c.Do(context.Background(), http.MethodPost, "/api/team/approvals", map[string]string{"id": "r1"}, nil)
	if !errors.Is(err, ErrUnsupported) || status != http.StatusNotFound {
		t.Fatalf("status=%d err=%v, want ErrUnsupported", status, err)
	}
	if len(clock.sleeps) != 0 {
		t.Errorf("a 404 must not be retried: %v", clock.sleeps)
	}
}

func TestDo_JSONNotFoundPassesThrough(t *testing.T) {
	d := newFakeDaemon("b1", func(w http.ResponseWriter, r *http.Request) {
		okJSON(w, http.StatusNotFound, team.APIError{Error: team.ErrNotFound})
	})
	srv := httptest.NewServer(d)
	defer srv.Close()
	c := newTestClient(srv.URL, newFakeClock(), io.Discard)

	status, err := c.Do(context.Background(), http.MethodGet, "/api/team/approvals/nope", nil, nil)
	var se *StatusError
	if status != http.StatusNotFound || !errors.As(err, &se) || se.API.Error != team.ErrNotFound {
		t.Fatalf("status=%d err=%v", status, err)
	}
	if errors.Is(err, ErrUnsupported) {
		t.Error("a JSON not_found is the daemon's answer, not an unsupported route")
	}
}

func TestDo_RequestOpenCarriesApproval(t *testing.T) {
	d := newFakeDaemon("b1", func(w http.ResponseWriter, r *http.Request) {
		okJSON(w, http.StatusConflict, team.APIError{Error: team.ErrRequestOpen, Approval: &team.Approval{ID: "open-1", State: team.StateOpen}})
	})
	srv := httptest.NewServer(d)
	defer srv.Close()
	c := newTestClient(srv.URL, newFakeClock(), io.Discard)

	status, err := c.Do(context.Background(), http.MethodPost, "/api/team/approvals", map[string]string{"id": "r2"}, nil)
	var se *StatusError
	if status != http.StatusConflict || !errors.As(err, &se) || se.API.Approval == nil || se.API.Approval.ID != "open-1" {
		t.Fatalf("status=%d err=%v", status, err)
	}
}

func TestDo_EOFIsRetried(t *testing.T) {
	var calls int
	var mu sync.Mutex
	d := newFakeDaemon("b1", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		n := calls
		mu.Unlock()
		if n == 1 {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				conn.Close() // the client reads EOF
			}
			return
		}
		okJSON(w, http.StatusOK, team.Approval{ID: "r1", State: team.StateTimeout})
	})
	srv := httptest.NewServer(d)
	defer srv.Close()
	clock := newFakeClock()
	// No keep-alive: on a REUSED connection Go's Transport retries an EOF'd
	// GET by itself and this client would never see the failure. The daemon
	// closing a fresh connection is what reaches Do (measured 2026-10-07).
	c := New(srv.URL, "tok", WithClock(clock.now, clock.sleep), WithStderr(io.Discard),
		WithHTTPClient(&http.Client{Transport: &http.Transport{DisableKeepAlives: true}}))

	var ap team.Approval
	status, err := c.Do(context.Background(), http.MethodGet, "/api/team/approvals/r1", nil, &ap)
	if err != nil || status != http.StatusOK || ap.State != team.StateTimeout {
		t.Fatalf("status=%d err=%v ap=%+v", status, err, ap)
	}
	if len(clock.sleeps) != 1 {
		t.Errorf("sleeps = %v, want one", clock.sleeps)
	}
}

func TestOnce_DoesNotRetry(t *testing.T) {
	addr := freeAddr(t)
	clock := newFakeClock()
	var stderr bytes.Buffer
	c := newTestClient("http://"+addr, clock, &stderr)

	_, err := c.Once(context.Background(), http.MethodDelete, "/api/team/approvals/r1", nil, nil)
	if err == nil || errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v, want the raw transport error", err)
	}
	if len(clock.sleeps) != 0 || stderr.Len() != 0 {
		t.Errorf("Once must not wait or print: sleeps=%v stderr=%q", clock.sleeps, stderr.String())
	}
}

func TestStatusError_Message(t *testing.T) {
	withAPI := &StatusError{Status: 400, API: team.APIError{Error: team.ErrBadRequest, Detail: "reason is required"}}
	if got := withAPI.Error(); got != "HTTP 400 bad_request: reason is required" {
		t.Errorf("got %q", got)
	}
	plain := &StatusError{Status: 401, Body: []byte("unauthorized\n")}
	if got := plain.Error(); got != "HTTP 401: unauthorized" {
		t.Errorf("got %q", got)
	}
}
```

- [ ] **Step 2: Run the tests and verify they fail.**
  - Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/lead-team && go test ./cmd/pdx/daemonclient/ -run 'TestDo_|TestOnce_|TestStatusError_' -v`
  - Expected: the package has no non-test file, so compilation fails with `undefined: New`, `undefined: WithClock`, `undefined: StatusError`, `undefined: MsgRestarting`, `undefined: ErrUnavailable`, `undefined: ErrUnsupported`, `undefined: Grace`.

- [ ] **Step 3: Implement `client.go`.**

```go
// Package daemonclient is the one HTTP client every new pdx command uses to
// talk to the local daemon (spec §9.1). It hides a daemon restart: refused,
// reset and half-open connections and the daemon's own 503 shutting_down /
// not_ready answers are retried with backoff for a 30 s grace, with one
// stderr line, and the boot id from /api/health tells the caller when the
// daemon actually came back as a new process.
//
// cmd/pdx must not import a daemon module package; the wire types come
// from the leaf package internal/team.
package daemonclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/wake/purdex/internal/team"
)

// Grace is how long Do keeps retrying after the first failure (spec §9.1:
// three times the usual 5–10 s restart).
const Grace = time.Duration(team.BootGraceS) * time.Second

// MsgRestarting is printed once per Client, on the first retryable failure.
const MsgRestarting = "daemon 重啟中，繼續等待…"

// msgRestartedFmt is printed once per Client when /api/health answers with a
// boot_id different from the first one seen.
const msgRestartedFmt = "daemon 已重新啟動（boot %s）\n"

// errShuttingDown is the daemon's 503 body while it is restarting
// (internal/core/restart.go). It has no wire constant of its own.
const errShuttingDown = "shutting_down"

// maxBodyBytes bounds every response body read; approvals are small.
const maxBodyBytes = 1 << 20

const healthPath = "/api/health"

// backoffs is the retry schedule; the last entry repeats.
var backoffs = []time.Duration{250 * time.Millisecond, 500 * time.Millisecond, time.Second}

var (
	// ErrUnavailable means the daemon did not answer within Grace (exit 20).
	ErrUnavailable = errors.New("daemon_unavailable")
	// ErrUnsupported means the route does not exist on this daemon: Go's
	// default mux answered a plain-text 404 (exit 21).
	ErrUnsupported = errors.New("unsupported")
)

// StatusError is a non-2xx answer that is not a retry signal. API is filled
// when the body decoded as a team.APIError with a non-empty error code;
// otherwise Body holds what the daemon sent (plain text, or a JSON object
// of another shape such as PairingGuard's {"reason":"pairing_mode"}).
type StatusError struct {
	Status int
	API    team.APIError
	Body   []byte
}

func (e *StatusError) Error() string {
	if e.API.Error != "" {
		if e.API.Detail != "" {
			return fmt.Sprintf("HTTP %d %s: %s", e.Status, e.API.Error, e.API.Detail)
		}
		return fmt.Sprintf("HTTP %d %s", e.Status, e.API.Error)
	}
	return fmt.Sprintf("HTTP %d: %s", e.Status, strings.TrimSpace(string(e.Body)))
}

// Option configures a Client.
type Option func(*Client)

// WithHTTPClient replaces the underlying http.Client (tests; a custom transport).
func WithHTTPClient(h *http.Client) Option { return func(c *Client) { c.http = h } }

// WithClock injects the clock Do measures the grace with and the sleep it
// backs off with. sleep must return ctx.Err() when ctx ends first.
func WithClock(now func() time.Time, sleep func(context.Context, time.Duration) error) Option {
	return func(c *Client) { c.now, c.sleep = now, sleep }
}

// WithStderr sets where the restart lines go; the default discards them.
func WithStderr(w io.Writer) Option { return func(c *Client) { c.stderr = w } }

// Client talks to one daemon. It is safe for concurrent use.
type Client struct {
	base  string
	token string
	http  *http.Client
	now   func() time.Time
	sleep func(context.Context, time.Duration) error

	mu              sync.Mutex
	stderr          io.Writer
	bootID          string
	bootKnown       bool
	restartingNoted bool
	restartedNoted  bool
}

// New returns a Client for baseURL (scheme://host:port, no trailing slash
// needed) presenting token as the bearer token.
func New(baseURL, token string, opts ...Option) *Client {
	c := &Client{
		base:   strings.TrimRight(baseURL, "/"),
		token:  token,
		http:   &http.Client{},
		now:    time.Now,
		sleep:  sleepCtx,
		stderr: io.Discard,
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// outage is one Do call's view of a failure run: when it began and how many
// failures it has seen. The grace is measured from first.
type outage struct {
	first    time.Time
	failures int
}

// Do sends method path with body (JSON, nil for none) and decodes a 2xx
// body into out when out != nil. It returns the HTTP status with a nil
// error on 2xx, a *StatusError on any other settled answer, ErrUnsupported
// on a plain-text 404, ErrUnavailable when the daemon stayed unreachable
// through Grace, or ctx.Err() when ctx ended first.
//
// Before this Client's first request, and before every retry, Do reads
// /api/health for the daemon's boot_id; the first answer is the baseline
// and a different later answer is reported once on stderr.
func (c *Client) Do(ctx context.Context, method, path string, body, out any) (int, error) {
	var o outage
	for {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		if o.failures > 0 || !c.bootSeen() {
			if err := c.probeBoot(ctx); err != nil {
				if !retryable(err) {
					return 0, err
				}
				if werr := c.wait(ctx, &o); werr != nil {
					return 0, werr
				}
				continue
			}
		}
		status, err := c.Once(ctx, method, path, body, out)
		if !retryable(err) {
			return status, err
		}
		if werr := c.wait(ctx, &o); werr != nil {
			return 0, werr
		}
	}
}

// Once sends exactly one request: no retry, no health probe, no stderr.
// The DELETE a cancelled `pdx lead request` sends uses it (spec §6.1 step
// 5: best effort, 3 s, no retry). Status and error follow Do's contract
// minus ErrUnavailable; a transport error is returned as is.
func (c *Client) Once(ctx context.Context, method, path string, body, out any) (int, error) {
	var rd io.Reader
	var payload []byte
	if body != nil {
		var err error
		payload, err = json.Marshal(body)
		if err != nil {
			return 0, fmt.Errorf("encode request: %w", err)
		}
		rd = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rd)
	if err != nil {
		return 0, err
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return resp.StatusCode, err
	}

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		if out != nil && len(bytes.TrimSpace(raw)) > 0 {
			if err := json.Unmarshal(raw, out); err != nil {
				return resp.StatusCode, fmt.Errorf("decode response: %w", err)
			}
		}
		return resp.StatusCode, nil
	}
	if resp.StatusCode == http.StatusNotFound && isPlainNotFound(resp.Header.Get("Content-Type"), raw) {
		return resp.StatusCode, ErrUnsupported
	}
	se := &StatusError{Status: resp.StatusCode, Body: raw}
	_ = json.Unmarshal(raw, &se.API) // best effort: a non-JSON or other-shaped body leaves API empty
	return resp.StatusCode, se
}

// isPlainNotFound recognises http.NotFound's answer — what Go's ServeMux
// writes for a route this daemon never registered — as opposed to a JSON
// 404 a team handler wrote on purpose.
func isPlainNotFound(contentType string, body []byte) bool {
	return strings.HasPrefix(contentType, "text/plain") &&
		strings.TrimSpace(string(body)) == "404 page not found"
}

// retryable reports whether err is a restart signal (spec §9.1): a refused,
// reset or half-closed connection, any other net.OpError that is not a
// context error, or the daemon's own 503 shutting_down / not_ready. A
// PairingGuard 503 has no "error" field and is not retried.
func retryable(err error) bool {
	if err == nil {
		return false
	}
	var se *StatusError
	if errors.As(err, &se) {
		return se.Status == http.StatusServiceUnavailable &&
			(se.API.Error == errShuttingDown || se.API.Error == team.ErrNotReady)
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	if errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	var op *net.OpError
	return errors.As(err, &op)
}

// wait records one failure, prints MsgRestarting on the Client's first,
// gives up with ErrUnavailable once the grace has run out, and otherwise
// sleeps the next backoff step. It returns ctx.Err() when ctx ends first.
func (c *Client) wait(ctx context.Context, o *outage) error {
	now := c.now()
	if o.failures == 0 {
		o.first = now
		c.noteRestarting()
	}
	o.failures++
	if now.Sub(o.first) >= Grace {
		return ErrUnavailable
	}
	step := o.failures - 1
	if step >= len(backoffs) {
		step = len(backoffs) - 1
	}
	return c.sleep(ctx, backoffs[step])
}

func (c *Client) noteRestarting() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.restartingNoted {
		return
	}
	c.restartingNoted = true
	fmt.Fprintln(c.stderr, MsgRestarting)
}

func (c *Client) bootSeen() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.bootKnown
}

// probeBoot reads /api/health (outside TokenAuth and PairingGuard, so it
// answers whenever the process is up). A transport error is returned for
// retryable() to classify. A non-200 or unreadable answer is not an error:
// the daemon is up, its boot is just unknown.
func (c *Client) probeBoot(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+healthPath, nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	var h struct {
		BootID string `json:"boot_id"`
	}
	if json.Unmarshal(raw, &h) != nil || h.BootID == "" {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	switch {
	case !c.bootKnown:
		c.bootID, c.bootKnown = h.BootID, true
	case h.BootID != c.bootID:
		c.bootID = h.BootID
		if !c.restartedNoted {
			c.restartedNoted = true
			fmt.Fprintf(c.stderr, msgRestartedFmt, h.BootID)
		}
	}
	return nil
}
```

- [ ] **Step 4: Run the tests and verify they pass.**
  - Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/lead-team && go test ./cmd/pdx/daemonclient/ -race -v`
  - Expected: PASS for all 12 tests; total run time well under 2 s (the fake clock does the waiting).

- [ ] **Step 5: Commit.**
  ```bash
  git add cmd/pdx/daemonclient/client.go cmd/pdx/daemonclient/client_test.go
  git commit -m "feat(cli): daemonclient retries through a daemon restart with a 30 s grace

  Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
  ```

### Task 2b.2: Exit code constants (spec §14)

**Files:**
- Create: `cmd/pdx/exitcodes.go`
- Test: `cmd/pdx/exitcodes_test.go`

**Interfaces:**
- Produces:
  ```go
  package main

  const (
  	ExitOK           = 0
  	ExitError        = 1
  	ExitUsage        = 2
  	ExitDenied       = 10
  	ExitTimeout      = 11
  	ExitCancelled    = 12
  	ExitRefused      = 13
  	ExitMemberFailed = 14
  	ExitUnavailable  = 20
  	ExitUnsupported  = 21
  )
  ```
- Consumes: nothing. The existing commands keep their literal `0`/`1`/`2`; these constants agree with them and are not retrofitted (a pure rename churn outside this PR's scope).

- [ ] **Step 1: Write the failing test.**

```go
package main

import "testing"

// TestExitCodes_PinnedToSpec14 pins the process exit codes to spec §14. A
// skill and a mod read these numbers; renumbering them is a wire change.
func TestExitCodes_PinnedToSpec14(t *testing.T) {
	cases := []struct {
		name string
		got  int
		want int
	}{
		{"ExitOK", ExitOK, 0},
		{"ExitError", ExitError, 1},
		{"ExitUsage", ExitUsage, 2},
		{"ExitDenied", ExitDenied, 10},
		{"ExitTimeout", ExitTimeout, 11},
		{"ExitCancelled", ExitCancelled, 12},
		{"ExitRefused", ExitRefused, 13},
		{"ExitMemberFailed", ExitMemberFailed, 14},
		{"ExitUnavailable", ExitUnavailable, 20},
		{"ExitUnsupported", ExitUnsupported, 21},
	}
	for _, tc := range cases {
		if tc.got != tc.want {
			t.Errorf("%s = %d, want %d (spec §14)", tc.name, tc.got, tc.want)
		}
	}
}
```

- [ ] **Step 2: Run the test and verify it fails.**
  - Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/lead-team && go test ./cmd/pdx/ -run TestExitCodes_PinnedToSpec14 -v`
  - Expected: compile failure `undefined: ExitOK` (and the other nine names).

- [ ] **Step 3: Implement `exitcodes.go`.**

```go
// cmd/pdx/exitcodes.go
package main

// Process exit codes for the team commands (spec §14). 0, 1 and 2 are the
// existing convention every pdx command already follows; 10–14 report what
// the daemon decided, 20–21 report that it could not be asked. A skill and
// the relay mod branch on these numbers, so they are a wire contract.
const (
	ExitOK           = 0  // approved / done / accepted
	ExitError        = 1  // other runtime or API error
	ExitUsage        = 2  // usage error, before any config load
	ExitDenied       = 10 // the user denied
	ExitTimeout      = 11 // the request timed out (U7: counts as a denial)
	ExitCancelled    = 12 // cancelled by the requester, or abandoned
	ExitRefused      = 13 // refused by team rules: request_open, later already_lead …
	ExitMemberFailed = 14 // the member did not start or did not respond (P4+)
	ExitUnavailable  = 20 // the daemon stayed unreachable through the 30 s grace
	ExitUnsupported  = 21 // the daemon does not have this route (plain 404)
)
```

- [ ] **Step 4: Run the test and verify it passes.**
  - Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/lead-team && go test ./cmd/pdx/ -run TestExitCodes_PinnedToSpec14 -v`
  - Expected: PASS.

- [ ] **Step 5: Commit.**
  ```bash
  git add cmd/pdx/exitcodes.go cmd/pdx/exitcodes_test.go
  git commit -m "feat(cli): exit code constants for the team commands (spec §14)

  Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
  ```

### Task 2b.3: `pdx lead request`

**Files:**
- Create: `cmd/pdx/lead.go`
- Test: `cmd/pdx/lead_test.go`

**Interfaces:**
- Consumes:
  - `daemonclient.New / Do / Once / WithStderr / ErrUnavailable / ErrUnsupported / StatusError` (Task 2b.1);
  - `team.CreateApprovalRequest`, `team.Approval`, `team.Grant`, `team.LeadPayload`, `team.KindLead`, `team.State*`, `team.ErrRequestOpen`, `team.DefaultWaitS`, `team.MaxWaitS`, `team.MaxMaxMembers`, `team.MaxPollWaitS` (P2a);
  - `config.Load` (`internal/config/config.go:301`), `resolveDaemonHost` (`cmd/pdx/statusline_proxy.go:145`), `sanitizeCell` (`cmd/pdx/peers.go:54`), the exit constants (Task 2b.2), `uuid.NewString` (`github.com/google/uuid`).
- Produces:
  ```go
  package main

  // runLead is the `pdx lead` switch target (Task 2b.4 wires it).
  func runLead(args []string)

  // runLeadCmd implements `pdx lead request` and returns the exit code.
  // newID makes the request id (uuid.NewString in production). clientOpts
  // are appended to the daemonclient options (tests inject a fake clock).
  func runLeadCmd(ctx context.Context, args []string, getenv func(string) string,
  	stdout, stderr io.Writer, newID func() string, clientOpts ...daemonclient.Option) int
  ```

**Behaviour** (spec §6.1, §6.4, §9.1, §14):
- Grammar: `pdx lead request --reason <text> [--max-members N] [--root <dir>]... [--wait 9m] [--config <path>]`. Any other verb, a missing `--reason`, `--max-members` outside `0..8` (0 means the daemon default 3), `--wait` outside `(0, 10m]`, an unknown flag, or a leftover positional → usage on stderr, exit 2, **before** `config.Load` and before any request.
- `CLAUDE_CODE_MESSAGING_SOCKET` empty → `pdx lead: CLAUDE_CODE_MESSAGING_SOCKET is unset — run inside a Claude Code session`, exit 1.
- Base URL `http://<resolveDaemonHost(cfg.Bind)>:<cfg.Port>`; client built with `WithStderr(stderr)` plus `clientOpts`.
- stderr: `申請 lead 中（<id>），請在 Purdex 介面核准；這個呼叫必須在前景等待（Bash timeout 600000）`.
- `POST /api/team/approvals` with `{id, kind:"lead", origin_inbox, reason, max_members, roots, wait_s}` under a 35 s context; `--root` values are made absolute and `Clean`ed; no `--root` sends none (the daemon defaults to the origin's cwd).
- While `state == open`: `GET /api/team/approvals/<id>?wait=25` under a 35 s context. A poll whose own 35 s runs out while the parent is alive is counted; three in a row → `pdx lead: daemon 沒有回應`, exit 20.
- Closed: `approved` → stdout `{"request_id":"<id>","grant":{"max_members":N,"roots":[…]}}` (grant from the Approval; when the daemon left it nil, from the payload), exit 0; `denied` → 10; `timeout` → 11; `cancelled` / `abandoned` → 12.
- Errors: `409 request_open` → `pdx lead: request_open — 已有一筆申請等待核准（<open id>）`, exit 13; `ErrUnavailable` → 20; `ErrUnsupported` → 21; any other `*StatusError` or error → `pdx lead: <err>`, exit 1.
- Parent ctx done (SIGINT/SIGTERM, or during the POST) → `DELETE /api/team/approvals/<id>` with `Once` under a fresh 3 s `context.Background()` child, then `pdx lead: 已取消申請（<id>）`, exit 12.

- [ ] **Step 1: Write the failing tests.**

```go
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wake/purdex/cmd/pdx/daemonclient"
	"github.com/wake/purdex/internal/team"
)

// leadClock is a fake clock for daemonclient.WithClock: time advances only
// when the client sleeps, so the 30 s grace costs no real time. onSleep
// (optional) runs after each sleep with its 1-based index; the restart test
// brings the new daemon up from it.
type leadClock struct {
	mu      sync.Mutex
	t       time.Time
	n       int
	onSleep func(n int)
}

func newLeadClock() *leadClock {
	return &leadClock{t: time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)}
}

func (c *leadClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *leadClock) sleep(ctx context.Context, d time.Duration) error {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.n++
	n := c.n
	hook := c.onSleep
	c.mu.Unlock()
	if hook != nil {
		hook(n)
	}
	return ctx.Err()
}

func (c *leadClock) sleeps() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n
}

func (c *leadClock) opt() daemonclient.Option { return daemonclient.WithClock(c.now, c.sleep) }

func leadClockOpt() daemonclient.Option { return newLeadClock().opt() }

// fakeTeamDaemon speaks the P2 routes of spec §6.2 for one request id:
// POST creates (createStatus 201, or a 409 request_open carrying openID),
// the first GET answers open and the second answers final, DELETE records
// the id and answers cancelled. When hold is set, GET blocks until the
// client goes away and signals pollStarted on its first arrival. Health
// answers bootID. When onFirstPoll is set, the first GET runs it and
// answers `open` with `Connection: close` — a long-poll cut by Stop
// (deviation 9 of P2a) on a daemon that is going down.
type fakeTeamDaemon struct {
	mu           sync.Mutex
	creates      []team.CreateApprovalRequest
	auths        []string
	polls        []string
	deletes      []string
	createStatus int
	openID       string
	final        team.Approval
	hold         bool
	pollStarted  chan struct{}
	startOnce    sync.Once
	bootID       string
	onFirstPoll  func()
}

func newFakeTeamDaemon(final team.Approval) *fakeTeamDaemon {
	return &fakeTeamDaemon{createStatus: http.StatusCreated, final: final, pollStarted: make(chan struct{}), bootID: "b1"}
}

func (f *fakeTeamDaemon) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.URL.Path == "/api/health" {
		f.mu.Lock()
		boot := f.bootID
		f.mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "boot_id": boot})
		return
	}
	f.mu.Lock()
	f.auths = append(f.auths, r.Header.Get("Authorization"))
	f.mu.Unlock()
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/api/team/approvals":
		var req team.CreateApprovalRequest
		json.NewDecoder(r.Body).Decode(&req)
		f.mu.Lock()
		f.creates = append(f.creates, req)
		status, openID := f.createStatus, f.openID
		f.mu.Unlock()
		if status == http.StatusConflict {
			w.WriteHeader(status)
			json.NewEncoder(w).Encode(team.APIError{Error: team.ErrRequestOpen, Approval: &team.Approval{ID: openID, State: team.StateOpen}})
			return
		}
		if status >= 400 {
			w.WriteHeader(status)
			json.NewEncoder(w).Encode(team.APIError{Error: team.ErrBadRequest, Detail: "reason is required"})
			return
		}
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(team.Approval{ID: req.ID, Kind: req.Kind, State: team.StateOpen})
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/team/approvals/"):
		f.mu.Lock()
		f.polls = append(f.polls, r.URL.RequestURI())
		n := len(f.polls)
		hold := f.hold
		onFirstPoll := f.onFirstPoll
		f.mu.Unlock()
		if hold {
			f.startOnce.Do(func() { close(f.pollStarted) })
			<-r.Context().Done()
			return
		}
		id := strings.TrimPrefix(r.URL.Path, "/api/team/approvals/")
		if n == 1 && onFirstPoll != nil {
			onFirstPoll()
			w.Header().Set("Connection", "close") // the client must not reuse this connection to a daemon that is gone
		}
		if n == 1 {
			json.NewEncoder(w).Encode(team.Approval{ID: id, State: team.StateOpen})
			return
		}
		ap := f.final
		ap.ID = id
		json.NewEncoder(w).Encode(ap)
	case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/api/team/approvals/"):
		id := strings.TrimPrefix(r.URL.Path, "/api/team/approvals/")
		f.mu.Lock()
		f.deletes = append(f.deletes, id)
		f.mu.Unlock()
		json.NewEncoder(w).Encode(team.Approval{ID: id, State: team.StateCancelled})
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeTeamDaemon) snapshot() (creates []team.CreateApprovalRequest, polls, deletes, auths []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]team.CreateApprovalRequest{}, f.creates...), append([]string{}, f.polls...),
		append([]string{}, f.deletes...), append([]string{}, f.auths...)
}

func leadEnv() func(string) string {
	return fakeGetenv(map[string]string{"CLAUDE_CODE_MESSAGING_SOCKET": "/tmp/cc-socks/1.sock"})
}

func fixedID() func() string { return func() string { return "11111111-2222-4333-8444-555555555555" } }

// driveLead drives runLeadCmd against d with a fake clock and returns the
// exit code and both streams. (Not runLead: that name is the production
// switch target in lead.go, same package.)
func driveLead(t *testing.T, ctx context.Context, d http.Handler, args ...string) (int, string, string) {
	t.Helper()
	srv := httptest.NewServer(d)
	defer srv.Close()
	cfgPath := writeTestConfig(t, srv.URL, "admin-tok")
	var stdout, stderr bytes.Buffer
	full := append(append([]string{"request"}, args...), "--config", cfgPath)
	code := runLeadCmd(ctx, full, leadEnv(), &stdout, &stderr, fixedID(), leadClockOpt())
	return code, stdout.String(), stderr.String()
}

// leadFreeAddr reserves and releases a 127.0.0.1 port, so two servers can
// take it in turn (a daemon restart keeps its port).
func leadFreeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	return addr
}

// leadServeOn starts an httptest.Server on a fixed address (Task 2b.1
// measured that a closed port can be re-listened at once).
func leadServeOn(t *testing.T, addr string, h http.Handler) *httptest.Server {
	t.Helper()
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("listen %s: %v", addr, err)
	}
	srv := httptest.NewUnstartedServer(h)
	srv.Listener.Close()
	srv.Listener = ln
	srv.Start()
	return srv
}

func TestRunLeadCmd_UsageErrorsExit2BeforeConfig(t *testing.T) {
	var reqCount int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&reqCount, 1)
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	cfgPath := writeTestConfig(t, srv.URL, "admin-tok")

	cases := []struct {
		name string
		args []string
	}{
		{"no verb", []string{}},
		{"unknown verb", []string{"approve"}},
		{"missing reason", []string{"request"}},
		{"empty reason", []string{"request", "--reason", "  "}},
		{"max-members too large", []string{"request", "--reason", "x", "--max-members", "9"}},
		{"max-members negative", []string{"request", "--reason", "x", "--max-members", "-1"}},
		{"wait zero", []string{"request", "--reason", "x", "--wait", "0"}},
		{"wait over cap", []string{"request", "--reason", "x", "--wait", "11m"}},
		{"unknown flag", []string{"request", "--reason", "x", "--bogus"}},
		{"leftover positional", []string{"request", "--reason", "x", "extra"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := atomic.LoadInt64(&reqCount)
			args := append(append([]string{}, tc.args...), "--config", cfgPath)
			var stdout, stderr bytes.Buffer
			code := runLeadCmd(context.Background(), args, leadEnv(), &stdout, &stderr, fixedID(), leadClockOpt())
			if code != ExitUsage {
				t.Errorf("exit code = %d, want %d; stderr=%q", code, ExitUsage, stderr.String())
			}
			if stderr.String() == "" {
				t.Errorf("stderr is empty, want a usage/error message")
			}
			if stdout.String() != "" {
				t.Errorf("stdout = %q, want empty", stdout.String())
			}
			if after := atomic.LoadInt64(&reqCount); after != before {
				t.Errorf("server saw %d request(s), want 0", after-before)
			}
		})
	}
}

func TestRunLeadCmd_NoInboxExit1(t *testing.T) {
	var reqCount int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&reqCount, 1)
	}))
	defer srv.Close()
	cfgPath := writeTestConfig(t, srv.URL, "admin-tok")
	var stdout, stderr bytes.Buffer
	code := runLeadCmd(context.Background(), []string{"request", "--reason", "x", "--config", cfgPath},
		fakeGetenv(nil), &stdout, &stderr, fixedID(), leadClockOpt())
	if code != ExitError || !strings.Contains(stderr.String(), "CLAUDE_CODE_MESSAGING_SOCKET") {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
	if atomic.LoadInt64(&reqCount) != 0 {
		t.Errorf("server saw %d request(s), want 0", reqCount)
	}
}

func TestRunLeadCmd_ApprovedExit0PrintsGrant(t *testing.T) {
	d := newFakeTeamDaemon(team.Approval{
		State: team.StateApproved,
		Grant: &team.Grant{MaxMembers: 2, Roots: []string{"/Users/wake/Workspace/wake/purdex"}},
	})
	code, stdout, stderr := driveLead(t, context.Background(), d,
		"--reason", "split the P3 dialog work", "--max-members", "2", "--root", "/Users/wake/Workspace/wake/purdex")
	if code != ExitOK {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
	var out struct {
		RequestID string     `json:"request_id"`
		Grant     team.Grant `json:"grant"`
	}
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("stdout %q: %v", stdout, err)
	}
	if out.RequestID != fixedID()() || out.Grant.MaxMembers != 2 || len(out.Grant.Roots) != 1 {
		t.Errorf("stdout = %q", stdout)
	}
	want := "申請 lead 中（" + fixedID()() + "），請在 Purdex 介面核准；這個呼叫必須在前景等待（Bash timeout 600000）"
	if !strings.Contains(stderr, want) {
		t.Errorf("stderr = %q, want %q", stderr, want)
	}

	creates, polls, deletes, auths := d.snapshot()
	if len(creates) != 1 {
		t.Fatalf("creates = %+v", creates)
	}
	c := creates[0]
	if c.ID != fixedID()() || c.Kind != team.KindLead || c.OriginInbox != "/tmp/cc-socks/1.sock" ||
		c.Reason != "split the P3 dialog work" || c.MaxMembers != 2 || c.WaitS != team.DefaultWaitS ||
		len(c.Roots) != 1 || c.Roots[0] != "/Users/wake/Workspace/wake/purdex" {
		t.Errorf("create body = %+v", c)
	}
	if len(polls) != 2 || !strings.HasSuffix(polls[0], "/api/team/approvals/"+fixedID()()+"?wait=25") {
		t.Errorf("polls = %v", polls)
	}
	if len(deletes) != 0 {
		t.Errorf("deletes = %v, want none", deletes)
	}
	for _, a := range auths {
		if a != "Bearer admin-tok" {
			t.Errorf("auth = %q", a)
		}
	}
}

func TestRunLeadCmd_ApprovedWithoutGrantFallsBackToPayload(t *testing.T) {
	payload, _ := json.Marshal(team.LeadPayload{Reason: "r", MaxMembers: 3, Roots: []string{"/w"}})
	d := newFakeTeamDaemon(team.Approval{State: team.StateApproved, Payload: payload})
	code, stdout, _ := driveLead(t, context.Background(), d, "--reason", "r")
	if code != ExitOK || !strings.Contains(stdout, `"max_members":3`) || !strings.Contains(stdout, `"/w"`) {
		t.Fatalf("code=%d stdout=%q", code, stdout)
	}
}

func TestRunLeadCmd_DeniedExit10(t *testing.T) {
	d := newFakeTeamDaemon(team.Approval{State: team.StateDenied, DecidedBy: &team.Client{Kind: "app", Label: "Purdex.app @ air26"}})
	code, stdout, stderr := driveLead(t, context.Background(), d, "--reason", "r")
	if code != ExitDenied || stdout != "" || !strings.Contains(stderr, "Purdex.app @ air26") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestRunLeadCmd_TimeoutExit11(t *testing.T) {
	d := newFakeTeamDaemon(team.Approval{State: team.StateTimeout})
	if code, _, stderr := driveLead(t, context.Background(), d, "--reason", "r"); code != ExitTimeout {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
}

func TestRunLeadCmd_AbandonedAndCancelledExit12(t *testing.T) {
	for _, st := range []team.State{team.StateAbandoned, team.StateCancelled} {
		d := newFakeTeamDaemon(team.Approval{State: st})
		if code, _, stderr := driveLead(t, context.Background(), d, "--reason", "r"); code != ExitCancelled {
			t.Errorf("%s: code=%d stderr=%q", st, code, stderr)
		}
	}
}

func TestRunLeadCmd_RequestOpenExit13(t *testing.T) {
	d := newFakeTeamDaemon(team.Approval{})
	d.createStatus, d.openID = http.StatusConflict, "open-1"
	code, stdout, stderr := driveLead(t, context.Background(), d, "--reason", "r")
	if code != ExitRefused || stdout != "" || !strings.Contains(stderr, "open-1") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if _, polls, _, _ := d.snapshot(); len(polls) != 0 {
		t.Errorf("polled a refused request: %v", polls)
	}
}

func TestRunLeadCmd_BadRequestExit1(t *testing.T) {
	d := newFakeTeamDaemon(team.Approval{})
	d.createStatus = http.StatusBadRequest
	if code, _, stderr := driveLead(t, context.Background(), d, "--reason", "r"); code != ExitError || !strings.Contains(stderr, "bad_request") {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
}

func TestRunLeadCmd_CancelOnCtxSendsDelete(t *testing.T) {
	d := newFakeTeamDaemon(team.Approval{})
	d.hold = true
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-d.pollStarted
		cancel()
	}()
	code, stdout, stderr := driveLead(t, ctx, d, "--reason", "r")
	if code != ExitCancelled || stdout != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	_, _, deletes, _ := d.snapshot()
	if len(deletes) != 1 || deletes[0] != fixedID()() {
		t.Errorf("deletes = %v, want the request id", deletes)
	}
	if !strings.Contains(stderr, "已取消申請") {
		t.Errorf("stderr = %q", stderr)
	}
}

func TestRunLeadCmd_PlainNotFoundExit21(t *testing.T) {
	mux := http.NewServeMux() // an older daemon: no /api/team routes
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"ok":true,"boot_id":"b1"}`))
	})
	if code, _, stderr := driveLead(t, context.Background(), mux, "--reason", "r"); code != ExitUnsupported {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
}

func TestRunLeadCmd_RefusedConnectionExit20(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	cfgPath := writeTestConfig(t, srv.URL, "admin-tok")
	srv.Close() // the port is now closed; the fake clock makes the 30 s grace instant

	var stdout, stderr bytes.Buffer
	code := runLeadCmd(context.Background(), []string{"request", "--reason", "r", "--config", cfgPath},
		leadEnv(), &stdout, &stderr, fixedID(), leadClockOpt())
	if code != ExitUnavailable || stdout.Len() != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if strings.Count(stderr.String(), daemonclient.MsgRestarting) != 1 {
		t.Errorf("restart line count != 1: %q", stderr.String())
	}
}

// Review Focus 3, end to end through lead.go: the daemon restarts while
// `pdx lead request` is long-polling. The first daemon (boot b1) accepts
// the create; its first poll is cut by Stop (answers `open`, connection
// closed) and its listener is gone before the client re-polls. The
// re-poll is refused, the client prints the restart line once, backs off
// once, and the new daemon (boot b2, same port) answers the health probe:
// the restarted line is printed once, the SAME request id is polled again,
// found still open, then approved. No real time passes (fake clock).
func TestLeadRequest_SurvivesDaemonRestartMidPoll(t *testing.T) {
	addr := leadFreeAddr(t)
	first := newFakeTeamDaemon(team.Approval{})
	firstSrv := leadServeOn(t, addr, first)
	first.onFirstPoll = func() { firstSrv.Listener.Close() } // the daemon is going down: no new connection is accepted
	defer firstSrv.Close()

	second := newFakeTeamDaemon(team.Approval{
		State: team.StateApproved,
		Grant: &team.Grant{MaxMembers: 3, Roots: []string{"/w"}},
	})
	second.bootID = "b2"
	var secondSrv *httptest.Server
	var once sync.Once
	clock := newLeadClock()
	clock.onSleep = func(n int) {
		if n == 1 {
			once.Do(func() { secondSrv = leadServeOn(t, addr, second) })
		}
	}
	defer func() {
		if secondSrv != nil {
			secondSrv.Close()
		}
	}()

	cfgPath := writeTestConfig(t, "http://"+addr, "admin-tok")
	var stdout, stderr bytes.Buffer
	code := runLeadCmd(context.Background(), []string{"request", "--reason", "r", "--config", cfgPath},
		leadEnv(), &stdout, &stderr, fixedID(), clock.opt())
	if code != ExitOK {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	var out struct {
		RequestID string     `json:"request_id"`
		Grant     team.Grant `json:"grant"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil || out.RequestID != fixedID()() || out.Grant.MaxMembers != 3 {
		t.Fatalf("stdout %q: %v", stdout.String(), err)
	}
	errText := stderr.String()
	if n := strings.Count(errText, daemonclient.MsgRestarting); n != 1 {
		t.Errorf("restart line count = %d, want 1: %q", n, errText)
	}
	if n := strings.Count(errText, "daemon 已重新啟動（boot b2）"); n != 1 {
		t.Errorf("restarted line count = %d, want 1: %q", n, errText)
	}
	if clock.sleeps() != 1 {
		t.Errorf("sleeps = %d, want exactly one backoff (the new daemon was up at the first retry)", clock.sleeps())
	}

	// One create on the first daemon, none on the second: the request id is
	// never re-created, only re-polled.
	creates1, polls1, deletes1, _ := first.snapshot()
	creates2, polls2, deletes2, _ := second.snapshot()
	if len(creates1) != 1 || creates1[0].ID != fixedID()() || len(creates2) != 0 {
		t.Errorf("creates: first=%+v second=%+v", creates1, creates2)
	}
	if len(polls1) != 1 || len(polls2) != 2 {
		t.Errorf("polls: first=%v second=%v, want 1 then 2", polls1, polls2)
	}
	wantPoll := "/api/team/approvals/" + fixedID()() + "?wait=25"
	for _, p := range append(append([]string{}, polls1...), polls2...) {
		if !strings.HasSuffix(p, wantPoll) {
			t.Errorf("poll %q is not the same request id (%s)", p, wantPoll)
		}
	}
	if len(deletes1)+len(deletes2) != 0 {
		t.Errorf("a restart must not cancel the request: deletes=%v %v", deletes1, deletes2)
	}
}
```

  The test adds `"net"` to the file's imports. `leadFreeAddr` / `leadServeOn` repeat `daemonclient`'s `freeAddr` / `serveOn` because that package's test helpers are not importable from `package main`.

- [ ] **Step 2: Run the tests and verify they fail.**
  - Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/lead-team && go test ./cmd/pdx/ -run 'TestRunLeadCmd_|TestLeadRequest_' -v`
  - Expected: compile failure `undefined: runLeadCmd`.

- [ ] **Step 3: Implement `lead.go`.**

```go
// cmd/pdx/lead.go
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
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"

	"github.com/wake/purdex/cmd/pdx/daemonclient"
	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/team"
)

// leadUsage is the grammar-rejection message for `pdx lead` (exit 2).
const leadUsage = "usage: pdx lead request --reason <text> [--max-members N] [--root <dir>]... [--wait 9m] [--config <path>]\n" +
	"       (--max-members 1..8, default 3; --wait up to 10m, default 9m so the call fits one Bash timeout)"

const (
	// leadRequestTimeout bounds every single request: 25 s of daemon-side
	// long-poll (team.MaxPollWaitS) plus room, and above daemonclient.Grace
	// so a restart inside one poll ends as exit 20, not as a hung poll.
	leadRequestTimeout = 35 * time.Second
	// leadCancelTimeout is the best-effort DELETE's budget (spec §6.1 step 5).
	leadCancelTimeout = 3 * time.Second
	// leadMaxHungPolls is how many consecutive polls may run out their own
	// timeout with no answer before the daemon counts as unavailable.
	leadMaxHungPolls = 3
)

// runLead is the `pdx lead` switch target. SIGINT and SIGTERM cancel ctx,
// which runLeadCmd turns into a DELETE and exit 12.
func runLead(args []string) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(runLeadCmd(ctx, args, os.Getenv, os.Stdout, os.Stderr, uuid.NewString))
}

// leadRequestArgs is the parsed, validated `pdx lead request` invocation.
type leadRequestArgs struct {
	cfgPath    string
	reason     string
	maxMembers int
	roots      []string
	wait       time.Duration
}

// stringList is a repeatable string flag (--root a --root b).
type stringList []string

func (s *stringList) String() string     { return strings.Join(*s, ",") }
func (s *stringList) Set(v string) error { *s = append(*s, v); return nil }

// parseLeadRequestArgs validates the grammar. ok=false means a usage line
// was written to stderr and the caller must exit 2 without loading config.
func parseLeadRequestArgs(args []string, stderr io.Writer) (leadRequestArgs, bool) {
	fs := flag.NewFlagSet("pdx lead request", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var a leadRequestArgs
	var roots stringList
	fs.StringVar(&a.cfgPath, "config", "", "")
	fs.StringVar(&a.reason, "reason", "", "")
	fs.IntVar(&a.maxMembers, "max-members", 0, "")
	fs.Var(&roots, "root", "")
	fs.DurationVar(&a.wait, "wait", time.Duration(team.DefaultWaitS)*time.Second, "")
	reject := func(msg string) (leadRequestArgs, bool) {
		fmt.Fprintf(stderr, "pdx lead: %s\n%s\n", msg, leadUsage)
		return a, false
	}
	if err := fs.Parse(args); err != nil {
		return reject(err.Error())
	}
	if fs.NArg() != 0 {
		return reject(fmt.Sprintf("unexpected argument %q", fs.Arg(0)))
	}
	if strings.TrimSpace(a.reason) == "" {
		return reject("--reason 不能為空")
	}
	if a.maxMembers < 0 || a.maxMembers > team.MaxMaxMembers {
		return reject(fmt.Sprintf("--max-members 必須在 1 到 %d 之間", team.MaxMaxMembers))
	}
	if a.wait <= 0 || a.wait > time.Duration(team.MaxWaitS)*time.Second {
		return reject(fmt.Sprintf("--wait 必須大於 0 且不超過 %ds", team.MaxWaitS))
	}
	for _, r := range roots {
		abs, err := filepath.Abs(r)
		if err != nil {
			return reject(fmt.Sprintf("--root %q: %v", r, err))
		}
		a.roots = append(a.roots, filepath.Clean(abs))
	}
	return a, true
}

// runLeadCmd implements `pdx lead request` (spec §6.1) and returns the exit
// code (spec §14). Grammar rejections return 2 before any config load or
// request. newID makes the request id; clientOpts are appended to the
// daemonclient options so tests can inject a fake clock.
func runLeadCmd(ctx context.Context, args []string, getenv func(string) string, stdout, stderr io.Writer,
	newID func() string, clientOpts ...daemonclient.Option) int {
	if len(args) == 0 || args[0] != "request" {
		fmt.Fprintln(stderr, leadUsage)
		return ExitUsage
	}
	a, ok := parseLeadRequestArgs(args[1:], stderr)
	if !ok {
		return ExitUsage
	}

	inbox := getenv("CLAUDE_CODE_MESSAGING_SOCKET")
	if inbox == "" {
		fmt.Fprintln(stderr, "pdx lead: CLAUDE_CODE_MESSAGING_SOCKET is unset — run inside a Claude Code session")
		return ExitError
	}
	cfg, err := config.Load(a.cfgPath)
	if err != nil {
		fmt.Fprintf(stderr, "pdx lead: %v\n", err)
		return ExitError
	}
	base := fmt.Sprintf("http://%s:%d", resolveDaemonHost(cfg.Bind), cfg.Port)
	opts := append([]daemonclient.Option{daemonclient.WithStderr(stderr)}, clientOpts...)
	client := daemonclient.New(base, cfg.Token, opts...)

	id := newID()
	fmt.Fprintf(stderr, "申請 lead 中（%s），請在 Purdex 介面核准；這個呼叫必須在前景等待（Bash timeout 600000）\n", id)

	create := team.CreateApprovalRequest{
		ID:          id,
		Kind:        team.KindLead,
		OriginInbox: inbox,
		Reason:      a.reason,
		MaxMembers:  a.maxMembers,
		Roots:       a.roots,
		WaitS:       int(a.wait / time.Second),
	}
	var ap team.Approval
	cctx, cancel := context.WithTimeout(ctx, leadRequestTimeout)
	_, err = client.Do(cctx, http.MethodPost, "/api/team/approvals", create, &ap)
	cancel()
	if err != nil {
		if ctx.Err() != nil {
			// The signal arrived mid-create; the row may exist, so cancel it.
			return leadCancel(client, id, stderr)
		}
		return leadReportErr(err, stderr)
	}

	hung := 0
	for ap.State == team.StateOpen {
		if ctx.Err() != nil {
			return leadCancel(client, id, stderr)
		}
		pctx, cancel := context.WithTimeout(ctx, leadRequestTimeout)
		var polled team.Approval
		_, err := client.Do(pctx, http.MethodGet,
			fmt.Sprintf("/api/team/approvals/%s?wait=%d", id, team.MaxPollWaitS), nil, &polled)
		cancel()
		if err != nil {
			if ctx.Err() != nil {
				return leadCancel(client, id, stderr)
			}
			if errors.Is(err, context.DeadlineExceeded) {
				// The poll's own 35 s ran out with no answer at all.
				hung++
				if hung >= leadMaxHungPolls {
					fmt.Fprintln(stderr, "pdx lead: daemon 沒有回應")
					return ExitUnavailable
				}
				continue
			}
			return leadReportErr(err, stderr)
		}
		hung = 0
		ap = polled
	}
	return leadFinish(ap, stdout, stderr)
}

// leadCancel is spec §6.1 step 5: best-effort DELETE under a fresh 3 s
// context with no retry, then exit 12. The parent ctx is already done, so
// the DELETE gets its own.
func leadCancel(client *daemonclient.Client, id string, stderr io.Writer) int {
	dctx, cancel := context.WithTimeout(context.Background(), leadCancelTimeout)
	defer cancel()
	if _, err := client.Once(dctx, http.MethodDelete, "/api/team/approvals/"+id, nil, nil); err != nil {
		fmt.Fprintf(stderr, "pdx lead: 取消申請時 daemon 回應：%v\n", err)
	}
	fmt.Fprintf(stderr, "pdx lead: 已取消申請（%s）\n", id)
	return ExitCancelled
}

// leadReportErr maps a client error to stderr text and an exit code.
func leadReportErr(err error, stderr io.Writer) int {
	var se *daemonclient.StatusError
	switch {
	case errors.Is(err, daemonclient.ErrUnavailable):
		fmt.Fprintln(stderr, "pdx lead: daemon_unavailable — 等了 30 秒 daemon 仍沒有回應")
		return ExitUnavailable
	case errors.Is(err, daemonclient.ErrUnsupported):
		fmt.Fprintln(stderr, "pdx lead: unsupported — 這個 daemon 沒有 /api/team 路由，請先更新 daemon")
		return ExitUnsupported
	case errors.As(err, &se) && se.API.Error == team.ErrRequestOpen:
		openID := ""
		if se.API.Approval != nil {
			openID = sanitizeCell(se.API.Approval.ID)
		}
		fmt.Fprintf(stderr, "pdx lead: request_open — 已有一筆申請等待核准（%s）\n", openID)
		return ExitRefused
	default:
		fmt.Fprintf(stderr, "pdx lead: %v\n", err)
		return ExitError
	}
}

// leadGrantOutput is what an approved request prints on stdout. Before P4
// there is no team yet, so the grant is reported under the request id.
type leadGrantOutput struct {
	RequestID string      `json:"request_id"`
	Grant     *team.Grant `json:"grant"`
}

// leadFinish maps a closed Approval to output and exit code (spec §14).
func leadFinish(ap team.Approval, stdout, stderr io.Writer) int {
	switch ap.State {
	case team.StateApproved:
		grant := ap.Grant
		if grant == nil {
			var p team.LeadPayload
			if json.Unmarshal(ap.Payload, &p) == nil {
				grant = &team.Grant{MaxMembers: p.MaxMembers, Roots: p.Roots}
			}
		}
		out, err := json.Marshal(leadGrantOutput{RequestID: ap.ID, Grant: grant})
		if err != nil {
			fmt.Fprintf(stderr, "pdx lead: %v\n", err)
			return ExitError
		}
		fmt.Fprintln(stdout, string(out))
		return ExitOK
	case team.StateDenied:
		fmt.Fprintf(stderr, "pdx lead: 申請已被拒絕%s\n", leadDecidedBy(ap))
		return ExitDenied
	case team.StateTimeout:
		fmt.Fprintln(stderr, "pdx lead: 申請逾時，視同拒絕")
		return ExitTimeout
	case team.StateCancelled:
		fmt.Fprintln(stderr, "pdx lead: 申請已取消")
		return ExitCancelled
	case team.StateAbandoned:
		fmt.Fprintln(stderr, "pdx lead: 申請已失效（lease 到期或來源 session 已結束）")
		return ExitCancelled
	default:
		fmt.Fprintf(stderr, "pdx lead: 未知狀態 %q\n", sanitizeCell(string(ap.State)))
		return ExitError
	}
}

// leadDecidedBy renders "（由 <label> 處理）" when the daemon says who decided.
func leadDecidedBy(ap team.Approval) string {
	if ap.DecidedBy == nil || ap.DecidedBy.Label == "" {
		return ""
	}
	return fmt.Sprintf("（由 %s 處理）", sanitizeCell(ap.DecidedBy.Label))
}
```

- [ ] **Step 4: Run the tests and verify they pass.**
  - Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/lead-team && go test ./cmd/pdx/ -run 'TestRunLeadCmd_|TestLeadRequest_|TestExitCodes_' -race -v`
  - Expected: PASS for all 12 `TestRunLeadCmd_*` functions (and their subtests), `TestLeadRequest_SurvivesDaemonRestartMidPoll`, and `TestExitCodes_PinnedToSpec14`. `TestRunLeadCmd_RefusedConnectionExit20` and the restart test finish in well under a second (fake clock).
  - Mutation check (Review Focus 3): in `runLeadCmd`'s poll loop, poll with `client.Once` instead of `client.Do` (a client that does not ride out the restart) and rerun — `TestLeadRequest_SurvivesDaemonRestartMidPoll` fails with `code=1 … connection refused` (the re-poll meets the closed port and `leadReportErr` exits 1). Put it back.

- [ ] **Step 5: Commit.**
  ```bash
  git add cmd/pdx/lead.go cmd/pdx/lead_test.go
  git commit -m "feat(cli): pdx lead request creates and long-polls a lead approval

  Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
  ```

### Task 2b.4: Wire `lead` into the dispatch; build, vet, full package test

**Files:**
- Modify: `cmd/pdx/main.go:42` (usage line), `cmd/pdx/main.go:67-68` (new `case` after `msg`)
- Test: none new; the gate is the whole `cmd/pdx` suite.

**Interfaces:**
- Consumes: `runLead(args []string)` (Task 2b.3).
- Produces: `pdx lead …` reaches `runLead`; `pdx` with no arguments lists `lead` among the commands.

- [ ] **Step 1: Confirm the gap.**
  - Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/lead-team && go build -o /tmp/pdx-p2b ./cmd/pdx && /tmp/pdx-p2b lead request --reason x; echo "exit=$?"`
  - Expected: `unknown command: lead` on stderr, `exit=1`.

- [ ] **Step 2: Edit `cmd/pdx/main.go`.**

  Line 42, replace
  ```go
  		fmt.Fprintf(os.Stderr, "Commands: serve, start, stop, status, statusline-proxy, hook, setup, token, peers, msg, nex, path, version\n")
  ```
  with
  ```go
  		fmt.Fprintf(os.Stderr, "Commands: serve, start, stop, status, statusline-proxy, hook, setup, token, peers, msg, lead, nex, path, version\n")
  ```

  After lines 67-68
  ```go
  	case "msg":
  		runMsg(os.Args[2:])
  ```
  add
  ```go
  	case "lead":
  		runLead(os.Args[2:])
  ```

- [ ] **Step 3: Build, vet, run the whole package.**
  - Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/lead-team && go build ./... && go vet ./cmd/pdx/... && go test ./cmd/pdx/... -race`
  - Expected: build clean; vet clean; `ok github.com/wake/purdex/cmd/pdx` and `ok github.com/wake/purdex/cmd/pdx/daemonclient`.
  - Then: `go build -o /tmp/pdx-p2b ./cmd/pdx && /tmp/pdx-p2b lead; echo "exit=$?"` → the usage block, `exit=2`; and `/tmp/pdx-p2b 2>&1 | head -2` lists `lead`.


- [ ] **Step 4: Commit.**
  ```bash
  git add cmd/pdx/main.go
  git commit -m "feat(cli): register pdx lead in the command dispatch

  Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
  ```

### Manual smoke test (not a gate; never against mlab's live daemon)

mlab's daemon on `100.64.0.2:7860` is live (`~/.config/pdx/config.toml`, launchd via `pdx start`). Use a throwaway daemon on a spare port and data dir:

```bash
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/lead-team
go build -o /tmp/pdx-p2b ./cmd/pdx
D=$(mktemp -d /tmp/pdx-smoke.XXXX)
printf 'bind = "127.0.0.1"\nport = 7999\ntoken = "smoke-tok"\ndata_dir = "%s"\n' "$D" > "$D/config.toml"
/tmp/pdx-p2b serve --config "$D/config.toml" > "$D/serve.log" 2>&1 &
SERVE=$!
sleep 1; curl -s http://127.0.0.1:7999/api/health   # expect {"ok":true,…,"boot_id":"…"}
```

Then, from inside a Claude Code session (so `CLAUDE_CODE_MESSAGING_SOCKET` is set and the registry has a live entry for it), with `timeout: 600000`:

```bash
/tmp/pdx-p2b lead request --reason "smoke" --wait 1m --config "$D/config.toml"; echo "exit=$?"
```

- With P2a running in that daemon: the stderr line appears, the request shows in `curl -s -H 'Authorization: Bearer smoke-tok' http://127.0.0.1:7999/api/team/approvals?state=open`; `kill -INT` the pdx → exit 12 and the request is `cancelled`; run again and let `--wait 1m` elapse → exit 11; `kill -TERM $SERVE` mid-poll, restart `serve` within 10 s → one `daemon 重啟中，繼續等待…`, one `daemon 已重新啟動（boot …）`, and the poll resumes on the same id; stop the daemon and leave it down → exit 20 after ~30 s.
- Against a daemon built from main **before** P2a (no `/api/team` routes): exit 21.
- Finish: `kill $SERVE; rm -rf "$D" /tmp/pdx-p2b`.

`serve` is used directly rather than `pdx start` so the pid file and logs stay inside `$D` and nothing touches `~/.config/pdx`.

### Deviations from the handoff notes

1. **`runLeadCmd` takes `clientOpts ...daemonclient.Option`, not `sleep`.** The note's signature was `runLeadCmd(ctx, args, getenv, stdout, stderr, newID, sleep)`. The sleep lives in the client (with the clock), so tests inject `WithClock` through the options; this also keeps `lead.go` from owning a second clock.
2. **`msgOriginInbox` is not reused.** It renders through `renderMsgAPIError`, whose every line starts with `pdx msg:` (`cmd/pdx/msg.go:377-387`). `lead.go` does the same three-line check with a `pdx lead:` prefix.
3. **The import rule is narrower than the note says.** `cmd/pdx/main.go` and `cmd/pdx/http_chain.go` do import `internal/module/*` (for `serve`). The rule the code states (`msg.go:26-28`) is that command files take wire types from leaf packages. P2b complies: `daemonclient` and `lead.go` import `internal/team` only. No import-architecture test exists to extend.
4. **Base URL for `lead` uses `resolveDaemonHost(cfg.Bind)`** as the note says; `msg.go`/`peers.go` still use the raw bind (`msg.go:320`). Retrofitting them is out of scope here.
5. **Boot-id baseline costs one `GET /api/health` per client life**, before the first request. The note did not say when "the one first seen" is seen; without a baseline the restarted line can never fire. Tests assert exactly one probe on the happy path.
6. **A client-side hung poll is bounded.** Spec §9.1 and the note only cover transport failures and 503s. A daemon that accepts the connection but never answers would loop forever, so three consecutive 35 s polls with no answer exit 20 (`pdx lead: daemon 沒有回應`). Not spec text; flagged below.
7. **Exit constants are not retrofitted** into `msg.go`/`peers.go` (they keep literal 0/1/2); the test pins the values instead.
8. **Handoff line numbers re-verified:** `resolveDaemonHost` is still `statusline_proxy.go:145`; `writeTestConfig` still `peers_test.go:349`; `fakeGetenv` still `msg_test.go:20`; `msgOriginInbox` still `msg.go:673`. The `msg.go:26-28` import comment is unchanged.

### Size estimate

| File | Lines (approx.) |
|---|---|
| `cmd/pdx/daemonclient/client.go` | 340 |
| `cmd/pdx/daemonclient/client_test.go` | 428 |
| `cmd/pdx/exitcodes.go` | 20 |
| `cmd/pdx/exitcodes_test.go` | 30 |
| `cmd/pdx/lead.go` | 275 |
| `cmd/pdx/lead_test.go` | 504 |
| `cmd/pdx/main.go` | +3 / −1 |
| **Total** | **~1 600 lines, 7 files** (re-measured 2026-10-07 in the scratch build; the restart end-to-end test is counted) |

Seven files is well inside the 20-file bound, but the line count is over 800. **Recommended split if the reviewer applies the line bound:**
- **PR P2b-1:** Tasks 2b.1 + 2b.2 (`daemonclient` and exit codes) — ~660 lines, 4 files, no behaviour change for users.
- **PR P2b-2:** Tasks 2b.3 + 2b.4 (`lead.go`, dispatch) — ~615 lines, 3 files.

Both halves are independently green: P2b-1 adds a package nothing calls yet; P2b-2 depends on P2b-1 merged.

### Open questions for the coordinator

1. **Hung-poll cap (deviation 6).** Is "three consecutive 35 s polls with no answer → exit 20" acceptable, or should an unanswered-but-connected daemon wait until the request's absolute `deadline_at` (which makes the bound up to 10 min + 35 s)? The implementation is three lines either way.
2. **`--max-members 0`.** The CLI accepts 0 as "daemon default (3)", matching the wire's `0→3` normalisation. If the coordinator prefers the CLI to reject 0 as usage, change the check to `a.maxMembers < 1 && flag was set`, which needs `fs.Visit` — slightly more code.
3. **PR split.** Confirm whether the 800-line bound or the 20-file bound governs (the preamble says "≤ 800 lines of diff or ≤ 20 files"). The section is written so Tasks 2b.1–2b.2 and 2b.3–2b.4 can ship as two PRs without changes.

### Coordinator decisions on P2b (2026-10-07, `mlab/_81nu3d`)

- **Split into three PRs (re-measured after the codex round; the 800-line bound governs, 20 files is the other bound, not an alternative):** **P2b-1** = Task 2b.1 (`daemonclient` and its tests; ≈ 770 lines, nothing calls it yet); **P2b-2** = Tasks 2b.2, 2b.3 without `TestLeadRequest_SurvivesDaemonRestartMidPoll` and its two helpers (exit codes, `pdx lead request`; ≈ 680); **P2b-3** = that restart end-to-end test plus Task 2b.4 (dispatch, build and vet gates; ≈ 170). Each is green on its own: P2b-3 only adds a test and the dispatch line on top of P2b-2.
- **Hung-poll cap stays as written:** three consecutive 35 s polls with no answer exit 20 with `pdx lead: daemon 沒有回應`. A connected daemon that never answers is broken, and 105 s is enough to tell. Spec §9.1 gains one line for it when P2b-2 ships.
- **`--max-members 0` means the daemon default (3)**, matching the wire's normalisation. No `fs.Visit`.
- **Deviations 1–8 are accepted.** `msg.go` / `peers.go` are not retrofitted to the exit constants or to `resolveDaemonHost` in this PR.
- **After P2b-1's review (PR #1669, 2026-10-07), the shipped `daemonclient` differs from Task 2b.1's text in four ways that Tasks 2b.3/2b.4 must follow:**
  1. **Retryable errors are an allowlist**, not "any `*net.OpError`": `ECONNREFUSED` and `503 shutting_down|not_ready` (before send), `ECONNRESET` / `io.EOF` / `io.ErrUnexpectedEOF` (after send). DNS, `EHOSTUNREACH`, `ENETUNREACH`, TLS, pairing-mode 503 and context errors return at once.
  2. **The 30 s grace is a hard bound**: retries, probes and sleeps run under `firstFailure + 30 s`, sleeps are truncated; a caller ctx without a deadline gets a per-attempt timeout (`DefaultAttemptTimeout` 60 s, `WithAttemptTimeout`), and a daemon that accepts the connection but never answers returns `ErrNoAnswer` without retrying — `pdx lead request` keeps its own "three consecutive polls without an answer → exit 20 `daemon 沒有回應`" count on top of it.
  3. **A 404 is `ErrUnsupported` unless its body decodes to a `team.APIError` with a code**; a JSON `not_found` is a `*StatusError`.
  4. **Writes replay only when marked**: `Do(ctx, method, path, body, out, daemonclient.Idempotent())`. **Task 2b.3's create `POST` must pass `Idempotent()`** (its id is a client UUID, so a replay is safe); the long-poll `GET` needs nothing; the SIGINT `DELETE` goes through `Once`. An unmarked write that fails after sending returns `ErrSentNoResponse` (the transport error stays on the chain).


---

## PR P3 — SPA: approval dialog host, store, event branch, one-click approve/deny, reconnect queue, notification, restart-confirm line

The Purdex.app side of spec §6.3 / §6.5 / §9.4 / §9.5 (U5b, U6, U13a, U14). A new `approval.request` branch in `useMultiHostEventWs` feeds `useApprovalStore` (entries keyed `hostId\0id`, never persisted); one global `ApprovalDialogHost`, mounted after `HandoffDialogHost`, renders the oldest open request across hosts with 核准 / 拒絕 as one click each; a decision clicked while the host is not `connected` is queued and sent once the reconnect snapshot re-adds the request; `closed` from elsewhere closes the dialog with the "已由 <client> 核准／拒絕" toast; `opened` raises one Electron notification whose click only focuses the window; the daemon-restart confirm gains `N 個申請等待核准`. Nothing in Go. All line numbers below were read on the worktree at `d39886e8` (origin/main `fb9fcbd8` + docs commits).

**Measured facts this section rests on** (worktree, 2026-10-07):
- `spa/src/lib/host-events.ts:4-15` is the `HostEvent['type']` union; `nex-worker-exited` is its last member (line 15). `spa/src/lib/agent-ws/index.ts:14` constrains `AGENT_WS_EVENT_TYPES` with `satisfies readonly HostEvent['type'][]`, so a new union member compiles without touching it.
- `spa/src/hooks/useMultiHostEventWs.ts:169-236` is the per-host `onEvent` closure; `hostId` is the loop variable of line 110; the `nex-worker-exited` branch is lines 213-217 and delegates to a lib function (`handleWorkerExited(hostId, event.value)`, `spa/src/lib/nex/worker-exited-event.ts:42-50`); `isAgentWsEvent` follows at line 224. Imports end at line 21.
- The WS test harness is `spa/src/hooks/useMultiHostEventWs.worker-exited.test.ts`: `vi.mock('../lib/host-connection', …checkHealth…)` before a dynamic import of the hook, `FakeSocket` with `emit(data)`, `useHostStore.setState({hosts, hostOrder, runtime: {}, activeHostId})`, `useSessionStore.setState({ fetchHost: vi.fn(async () => {}), replaceHost: vi.fn() } as never)`.
- `HandoffDialogHost` (`spa/src/components/HandoffDialogHost.tsx:31-36`) renders from a non-persisted zustand store (`useHandoffDialogStore.ts:29-36`, plain `create`, no `persist`) and keys the open dialog so a new target is a fresh mount. Its test seeds stores with `setState` and asserts through `data-testid`.
- `ConfirmDialog` (`spa/src/components/ConfirmDialog.tsx:91-99`) binds Escape → `onCancel` in the capture phase and the backdrop click → `onCancel` (line 111); it has only Cancel + Confirm. The approval dialog must not dismiss on Escape, so it is a custom panel reusing the same classes (`fixed inset-0 z-50 … bg-black/50`, panel `rounded-lg border border-border-default bg-surface-primary shadow-lg outline-none`, buttons at lines 126-142) and the same focus-on-open + Tab trap (lines 50-86), without the Escape effect.
- `App.tsx:34` imports `HandoffDialogHost`; `App.tsx:282-283` mounts it (`{/* The one "Hand to nex" dialog … */}` then `<HandoffDialogHost />`).
- Host connection state: `useHostStore` `runtime[hostId].status` is `'connected' | 'disconnected' | 'reconnecting' | 'auth-error'` (`useHostStore.ts:82-83`); the WS hook sets `reconnecting` on socket close (`useMultiHostEventWs.ts:245`) and `connected` on open (`:251-254`). Tests flip it with `useHostStore.getState().setRuntime(H, { status })` (`useNexHostStore.test.ts:428-429`).
- API helpers: `pinnedHostFetch(hostId, path, init)` (`host-api.ts:214-219`) rejects an unconfigured host and otherwise adds the Bearer header via `hostFetch`; `postJson` in `handoff-api.ts:129-150` keeps the decoded error body on a typed error (`HandoffApiError`, lines 18-30) — the approval API copies that shape. The handoff-api test stubs `fetch` with `vi.stubGlobal('fetch', vi.fn())` and seeds `useHostStore.getState().addHost({ id: 'host-mlab', name: 'mlab', ip: '100.64.0.2', port: 7860, token: 'tok-1' })` (`handoff-api.test.ts:33-37`).
- Notifications: `window.electronAPI.showNotification({ title, body, sessionCode, eventName, broadcastTs, action?: { kind: string; hostId: string; sessionCode?: string } })` (`electron.d.ts:135`); Electron main dedups on `broadcastTs` alone, 5 s window (`electron/main.ts:155-165`), and forwards `action` unchanged on click (`:174-183`). The SPA click listener (`useNotificationDispatcher.ts:278-295`) routes `open-host` and treats every other kind as `open-session`; `NotificationAction` is the union at lines 113-115; `handleNotificationClick` is the switch at 349-411. `getPlatformCapabilities().canNotification` is `!!window.electronAPI` (`platform.ts:14-27`). Tests stub Electron with `Object.defineProperty(window, 'electronAPI', { value: {...}, writable: true, configurable: true })` (`useNotificationDispatcher.test.ts:170, 208-215`).
- `window.electronAPI.localDaemonStatus?: () => Promise<ElectronLocalDaemonStatus>` (`electron.d.ts:173`) — a **Promise**, with `hostname: string` (`:62`). `device-name.ts:63-79` already reads it once with a 1.5 s race; the client label copies that pattern.
- `RestartDaemonButton.tsx:115-131` renders the confirm `ConfirmDialog` with a children `<p data-testid={`${testId}-workers`}>` at 124-130; its test sets `useI18nStore.getState().setLocale('zh-TW')` and reads `restart-daemon-workers` (`RestartDaemonButton.test.tsx:11-16, 36-51`).
- i18n: `t(key, params?)` replaces `{{param}}` and falls back to `en`, then to the key (`useI18nStore.ts:41-52`); `pluralKey(base, count)` → `_one`/`_other` (`plural.ts:7-9`; zh-TW has no plural, so this section does not use it: the spec strings are count-agnostic). `locale-completeness.test.ts:5-107` pins identical key sets and has per-namespace blocks (`peer.`, `hosts.transfer.`). Both locale files are 1897 lines; the last key is `"worker.exit.after_transfer_failed"` at line 1896, `}` at 1897. Neighbouring features name keys `<feature>.<area>.<name>` (`hosts.restart.confirm_workers`, `worker.exit.manual_resume`).
- Toast: `useUndoToast.getState().show(message, action?, actionLabel?, opts?)` (`useUndoToast.ts:24, 34-35`); features read it back in tests as `useUndoToast.getState().toast?.message`.
- Host display name in non-hook code: `hostLabel(hostId, hostLookOf(hostId))` (`host-look.ts:116-123, 149-151`); in components `hostLabel(hostId, useHostLook(hostId))` (`RestartDaemonButton.tsx:44`). With no look entry it is the host's configured `name` (`host-look.ts:42-47`).
- `tsconfig.app.json` has `strict`, `noUnusedLocals`, `noUnusedParameters`, `verbatimModuleSyntax`, `erasableSyntaxOnly` (no `enum`). ESLint runs `react-refresh/only-export-components`, so `.tsx` component files export components only; helpers live in `lib/`.
- Vitest 4.1 / `@testing-library/react` 16.3 / jsdom; `src/test-setup.ts` registers the built-in locales and auto-cleans up.

**Split.** P3 is two PRs (see `## Size estimate`): **P3a** = Tasks 3.1–3.4 (types + API, store, WS branch, dialog host); **P3b** = Tasks 3.5–3.8 (reconnect resend, notification, restart line, i18n consolidation). Each stays ≤ 20 files. Tasks add their own i18n keys as they need them (so their tests assert real strings); Task 3.8 is the consolidated key table, the namespace completeness block, lint and build.

### Task 3.1: Wire types and the approval API client

**Files:**
- Create: `spa/src/lib/team/types.ts`
- Create: `spa/src/lib/team/approval-api.ts`
- Test: `spa/src/lib/team/approval-api.test.ts`

**Interfaces:**
- Consumes: `pinnedHostFetch(hostId: string, path: string, init?: RequestInit): Promise<Response>` (`spa/src/lib/host-api.ts:214`); `useHostStore.getState().hosts` (`useHostStore.ts:122`).
- Produces (TypeScript mirror of `internal/team/wire.go`, JSON names are the contract):
  ```ts
  export const APPROVAL_EVENT_TYPE = 'approval.request'
  export type ApprovalKind = 'lead' | 'self_relay'
  export type ApprovalState = 'open' | 'approved' | 'denied' | 'timeout' | 'cancelled' | 'abandoned'
  export const DEFAULT_MAX_MEMBERS = 3
  export const MAX_MAX_MEMBERS = 8
  export interface Origin { session_id: string; ref: string; name: string; pid: number; proc_start: string; cwd: string; tmux: string; title?: string; address?: string }
  export interface LeadPayload { reason: string; max_members: number; roots: string[] }
  export interface Grant { max_members: number; roots: string[] }
  export interface Client { kind: 'app'; label: string; addr?: string }
  export interface Approval { id: string; kind: ApprovalKind; host_id: string; origin: Origin; payload: unknown; state: ApprovalState; created_at: number; deadline_at: number; lease_until: number; decided_by?: Client; decided_at?: number; grant?: Grant }
  export interface DecideRequest { decision: 'approve' | 'deny'; grant?: Grant; client: Client }
  export interface APIError { error: string; detail?: string; approval?: Approval }
  export type ApprovalEventValue = { op: 'opened' | 'closed'; approval: Approval } | { op: 'snapshot'; approvals: Approval[] }
  export interface InflightResponse { approvals_open: number; relays_active: number }   // GET /api/team/inflight (spec §9.5)
  export function leadPayloadOf(a: Approval): LeadPayload
  export class ApprovalApiError extends Error { readonly status: number; readonly code: string; readonly detail: string; readonly approval: Approval | null }
  export function decideApproval(hostId: string, id: string, body: DecideRequest): Promise<Approval>
  export function listOpenApprovals(hostId: string): Promise<Approval[]>
  export const INFLIGHT_TIMEOUT_MS = 3_000
  export function fetchInflight(hostId: string, timeoutMs?: number): Promise<InflightResponse>   // aborted after timeoutMs → code 'network'
  ```
  `Origin.title` and `Origin.address` are **optional additions** the wire does not carry yet (see `## Open questions`); the SPA falls back when they are absent.

- [ ] **Step 1: Write the failing test.**

```ts
// spa/src/lib/team/approval-api.test.ts — the SPA client for POST /api/team/approvals/{id}/decide and
// GET /api/team/approvals?state=open (lead-team spec §6.2, plan preamble "Routes"). A non-2xx body is
// `{error, detail, approval}`; the 409s carry the Approval, so the caller can say who handled it.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { useHostStore } from '../../stores/useHostStore'
import { ApprovalApiError, decideApproval, fetchInflight, listOpenApprovals } from './approval-api'
import { leadPayloadOf, type Approval } from './types'

const testGlobal = globalThis as typeof globalThis & { fetch: ReturnType<typeof vi.fn> }

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

async function rejection(p: Promise<unknown>): Promise<ApprovalApiError> {
  try {
    await p
  } catch (e) {
    expect(e).toBeInstanceOf(ApprovalApiError)
    return e as ApprovalApiError
  }
  throw new Error('expected rejection')
}

const approval = (over: Partial<Approval> = {}): Approval => ({
  id: 'req-1', kind: 'lead', host_id: 'd1',
  origin: { session_id: 'S1', ref: '_40iueq', name: 'purdex-7c', pid: 4242, proc_start: 'Tue Oct  7 10:00:00 2026', cwd: '/w/purdex', tmux: 'purdex:@1.%2' },
  payload: { reason: '要平行跑三個 PR', max_members: 3, roots: ['/w/purdex'] },
  state: 'open', created_at: 1_000, deadline_at: 541_000, lease_until: 31_000,
  ...over,
})
const client = { kind: 'app' as const, label: 'Purdex.app @ mlab' }

describe('approval-api', () => {
  let hostId: string

  beforeEach(() => {
    useHostStore.getState().reset()
    hostId = useHostStore.getState().addHost({ id: 'host-mlab', name: 'mlab', ip: '100.64.0.2', port: 7860, token: 'tok-1' })
    vi.stubGlobal('fetch', vi.fn())
  })
  afterEach(() => vi.unstubAllGlobals())

  describe('decideApproval', () => {
    it('POSTs the DecideRequest as JSON to /api/team/approvals/{id}/decide with the Bearer token', async () => {
      testGlobal.fetch.mockResolvedValueOnce(json(approval({ state: 'approved', decided_by: client, decided_at: 2_000, grant: { max_members: 3, roots: ['/w/purdex'] } })))
      const r = await decideApproval(hostId, 'req-1', { decision: 'approve', grant: { max_members: 3, roots: ['/w/purdex'] }, client })
      expect(testGlobal.fetch).toHaveBeenCalledTimes(1)
      const [url, init] = testGlobal.fetch.mock.calls[0]
      expect(url).toBe('http://100.64.0.2:7860/api/team/approvals/req-1/decide')
      expect(init.method).toBe('POST')
      expect(JSON.parse(init.body)).toEqual({ decision: 'approve', grant: { max_members: 3, roots: ['/w/purdex'] }, client })
      const h = new Headers(init.headers)
      expect(h.get('Content-Type')).toBe('application/json')
      expect(h.get('Authorization')).toBe('Bearer tok-1')
      expect(r.state).toBe('approved')
      expect(r.decided_by?.label).toBe('Purdex.app @ mlab')
    })

    it('URL-encodes the id', async () => {
      testGlobal.fetch.mockResolvedValueOnce(json(approval({ id: 'a/b c', state: 'denied' })))
      await decideApproval(hostId, 'a/b c', { decision: 'deny', client })
      expect(testGlobal.fetch.mock.calls[0][0]).toBe('http://100.64.0.2:7860/api/team/approvals/a%2Fb%20c/decide')
    })

    it('a 409 already_decided is surfaced as a typed error carrying the closed approval', async () => {
      const closed = approval({ state: 'denied', decided_by: { kind: 'app', label: 'Purdex.app @ air26', addr: '100.64.0.4:51234' }, decided_at: 3_000 })
      testGlobal.fetch.mockResolvedValueOnce(json({ error: 'already_decided', detail: 'closed at 3000', approval: closed }, 409))
      const err = await rejection(decideApproval(hostId, 'req-1', { decision: 'approve', client }))
      expect(err.status).toBe(409)
      expect(err.code).toBe('already_decided')
      expect(err.detail).toBe('closed at 3000')
      expect(err.approval?.state).toBe('denied')
      expect(err.approval?.decided_by?.label).toBe('Purdex.app @ air26')
    })

    it('a JSON 404 not_found keeps the daemon code; a plain-text 404 (old daemon, no route) is `unsupported`', async () => {
      testGlobal.fetch.mockResolvedValueOnce(json({ error: 'not_found' }, 404))
      const a = await rejection(decideApproval(hostId, 'gone', { decision: 'deny', client }))
      expect(a.code).toBe('not_found')
      expect(a.approval).toBeNull()
      testGlobal.fetch.mockResolvedValueOnce(new Response('404 page not found\n', { status: 404 }))
      const b = await rejection(decideApproval(hostId, 'gone', { decision: 'deny', client }))
      expect(b.status).toBe(404)
      expect(b.code).toBe('unsupported')
    })

    it('a non-JSON 5xx falls back to http_<status>', async () => {
      testGlobal.fetch.mockResolvedValueOnce(new Response('<html>boom</html>', { status: 502 }))
      const err = await rejection(decideApproval(hostId, 'req-1', { decision: 'deny', client }))
      expect(err.code).toBe('http_502')
    })

    it('a fetch rejection (socket refused mid-restart) is code `network`, status 0', async () => {
      testGlobal.fetch.mockRejectedValueOnce(new TypeError('Failed to fetch'))
      const err = await rejection(decideApproval(hostId, 'req-1', { decision: 'deny', client }))
      expect(err.status).toBe(0)
      expect(err.code).toBe('network')
      expect(err.detail).toBe('Failed to fetch')
    })

    it('an unconfigured host is refused before any request (`host_removed`)', async () => {
      const err = await rejection(decideApproval('no-such-host', 'req-1', { decision: 'deny', client }))
      expect(err.code).toBe('host_removed')
      expect(testGlobal.fetch).not.toHaveBeenCalled()
    })
  })

  describe('listOpenApprovals', () => {
    it('GETs /api/team/approvals?state=open and returns the list', async () => {
      testGlobal.fetch.mockResolvedValueOnce(json({ approvals: [approval(), approval({ id: 'req-2' })] }))
      const list = await listOpenApprovals(hostId)
      const [url, init] = testGlobal.fetch.mock.calls[0]
      expect(url).toBe('http://100.64.0.2:7860/api/team/approvals?state=open')
      expect(init.method).toBe('GET')
      expect(list.map((a) => a.id)).toEqual(['req-1', 'req-2'])
    })

    it('a null or missing `approvals` is an empty list', async () => {
      testGlobal.fetch.mockResolvedValueOnce(json({ approvals: null }))
      expect(await listOpenApprovals(hostId)).toEqual([])
      testGlobal.fetch.mockResolvedValueOnce(json({}))
      expect(await listOpenApprovals(hostId)).toEqual([])
    })
  })

  // GET /api/team/inflight (spec §9.5) feeds the restart confirm, which has a 3 s budget and falls back to its
  // own store on any rejection — so the call must bound itself and must reject (never hang) past the budget.
  describe('fetchInflight', () => {
    it('GETs /api/team/inflight with the Bearer token and an abort signal, and returns both counts', async () => {
      testGlobal.fetch.mockResolvedValueOnce(json({ approvals_open: 2, relays_active: 0 }))
      expect(await fetchInflight(hostId)).toEqual({ approvals_open: 2, relays_active: 0 })
      const [url, init] = testGlobal.fetch.mock.calls[0]
      expect(url).toBe('http://100.64.0.2:7860/api/team/inflight')
      expect(init.method).toBe('GET')
      expect(new Headers(init.headers).get('Authorization')).toBe('Bearer tok-1')
      expect(init.signal).toBeInstanceOf(AbortSignal)
    })

    it('a missing or malformed count reads as 0', async () => {
      testGlobal.fetch.mockResolvedValueOnce(json({ approvals_open: 'two' }))
      expect(await fetchInflight(hostId)).toEqual({ approvals_open: 0, relays_active: 0 })
    })

    it('an older daemon (plain-text 404) is `unsupported`', async () => {
      testGlobal.fetch.mockResolvedValueOnce(new Response('404 page not found\n', { status: 404 }))
      expect((await rejection(fetchInflight(hostId))).code).toBe('unsupported')
    })

    it('gives up after its budget: the fetch is aborted and the rejection is code `network`', async () => {
      vi.useFakeTimers()
      try {
        testGlobal.fetch.mockImplementationOnce((_url: string, init: RequestInit) => new Promise<Response>((_resolve, reject) => {
          init.signal?.addEventListener('abort', () => reject(new DOMException('The operation was aborted.', 'AbortError')))
        }))
        const settled = rejection(fetchInflight(hostId, 50))
        await vi.advanceTimersByTimeAsync(50)
        expect((await settled).code).toBe('network')
      } finally {
        vi.useRealTimers()
      }
    })
  })

  describe('leadPayloadOf', () => {
    it('reads the lead payload and normalises it like the daemon does (0 → 3, cap 8, roots default [cwd])', () => {
      expect(leadPayloadOf(approval())).toEqual({ reason: '要平行跑三個 PR', max_members: 3, roots: ['/w/purdex'] })
      expect(leadPayloadOf(approval({ payload: { reason: 'r', max_members: 0, roots: [] } }))).toEqual({ reason: 'r', max_members: 3, roots: ['/w/purdex'] })
      expect(leadPayloadOf(approval({ payload: { reason: 'r', max_members: 99, roots: ['/a', 7, ''] } }))).toEqual({ reason: 'r', max_members: 8, roots: ['/a'] })
      expect(leadPayloadOf(approval({ payload: 'garbage' }))).toEqual({ reason: '', max_members: 3, roots: ['/w/purdex'] })
    })
  })
})
```

- [ ] **Step 2: Run the test and verify it fails.**
  - Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/lead-team/spa && npx vitest run src/lib/team/approval-api.test.ts`
  - Expected: FAIL — `Failed to resolve import "./approval-api"` (the module does not exist).

- [ ] **Step 3: Implement `types.ts` and `approval-api.ts`.**

```ts
// spa/src/lib/team/types.ts — TypeScript mirror of the daemon's `internal/team/wire.go` (lead-team plan
// preamble "The wire contract"). JSON names are the contract; nothing here is persisted. Time fields are
// unix milliseconds. `payload` stays `unknown` (it is `json.RawMessage` on the wire and differs per kind);
// `leadPayloadOf` reads the lead shape with the daemon's normalisation, so a payload an older daemon left
// un-normalised still renders.

/** `HostEvent.type` of every approval event. */
export const APPROVAL_EVENT_TYPE = 'approval.request'

export type ApprovalKind = 'lead' | 'self_relay'

export type ApprovalState = 'open' | 'approved' | 'denied' | 'timeout' | 'cancelled' | 'abandoned'

/** Spec §6.1–§6.2 limits (`team.DefaultMaxMembers`, `team.MaxMaxMembers`). */
export const DEFAULT_MAX_MEMBERS = 3
export const MAX_MAX_MEMBERS = 8

/**
 * The requesting CC session, attributed by inbox (spec §6.2). `ref` is `_xxxxxx`; `name` may be ''.
 * `title` and `address` are not on the wire yet (P2a may add them); the SPA falls back to `name` / `ref`
 * and to `<host>/<name> [<ref>]` built locally (approval-format.ts) when they are absent.
 */
export interface Origin {
  session_id: string
  ref: string
  name: string
  pid: number
  proc_start: string
  cwd: string
  /** `<session>:@<win>.%<pane>` or ''. */
  tmux: string
  title?: string
  address?: string
}

/** `Approval.payload` for kind `lead`. */
export interface LeadPayload {
  reason: string
  max_members: number
  roots: string[]
}

/** What the user approved, as edited in the dialog. */
export interface Grant {
  max_members: number
  roots: string[]
}

/** The audit label of whoever decided (spec §6.5). `addr` is set by the daemon from RemoteAddr. */
export interface Client {
  kind: 'app'
  label: string
  addr?: string
}

export interface Approval {
  id: string
  kind: ApprovalKind
  host_id: string
  origin: Origin
  payload: unknown
  state: ApprovalState
  created_at: number
  deadline_at: number
  lease_until: number
  decided_by?: Client
  decided_at?: number
  grant?: Grant
}

/** `POST /api/team/approvals/{id}/decide`. `grant` is approve-only; absent → the payload's values. */
export interface DecideRequest {
  decision: 'approve' | 'deny'
  grant?: Grant
  client: Client
}

/** Every non-2xx body on `/api/team/*`. */
export interface APIError {
  error: string
  detail?: string
  approval?: Approval
}

/** `HostEvent.value` (JSON text) of an `approval.request` event. */
export type ApprovalEventValue =
  | { op: 'opened' | 'closed'; approval: Approval }
  | { op: 'snapshot'; approvals: Approval[] }

/** `GET /api/team/inflight` (spec §9.5): what a restart of that daemon would interrupt. `relays_active` is 0 until P6. */
export interface InflightResponse {
  approvals_open: number
  relays_active: number
}

function isRecord(v: unknown): v is Record<string, unknown> {
  return typeof v === 'object' && v !== null && !Array.isArray(v)
}

/** The lead payload, normalised as the daemon normalises it: `max_members` 0 → 3, cap 8; roots default `[origin.cwd]`. */
export function leadPayloadOf(a: Approval): LeadPayload {
  const p = isRecord(a.payload) ? a.payload : {}
  const reason = typeof p.reason === 'string' ? p.reason : ''
  const rawMembers = typeof p.max_members === 'number' && Number.isFinite(p.max_members) ? Math.trunc(p.max_members) : 0
  const max_members = rawMembers <= 0 ? DEFAULT_MAX_MEMBERS : Math.min(rawMembers, MAX_MAX_MEMBERS)
  const roots = Array.isArray(p.roots) ? p.roots.filter((r): r is string => typeof r === 'string' && r !== '') : []
  return { reason, max_members, roots: roots.length > 0 ? roots : [a.origin.cwd] }
}
```

```ts
// spa/src/lib/team/approval-api.ts — SPA wrappers for the daemon's approval routes (lead-team spec §6.2,
// plan preamble "Routes"): decide one request, list the open ones. Pinned to a host THIS device has
// (`pinnedHostFetch`), never the active-host fallback — a request names a specific daemon.
//
// Errors: every non-2xx body is `{error, detail, approval}`; the two 409s (`request_open`,
// `already_decided`) carry the Approval, so the whole body is kept on the error instead of being
// flattened. A plain-text 404 (Go's mux, no such route) means an older daemon: code `unsupported`.
// A rejected fetch (refused, reset — the daemon is restarting) is code `network`, status 0: the dialog
// queues the decision on that code alone.
import { pinnedHostFetch } from '../host-api'
import { useHostStore } from '../../stores/useHostStore'
import type { APIError, Approval, DecideRequest, InflightResponse } from './types'

/** `fetchInflight`'s budget: the restart confirm's own (daemon-restart.ts `WORKER_COUNT_TIMEOUT_MS`). */
export const INFLIGHT_TIMEOUT_MS = 3_000

export class ApprovalApiError extends Error {
  readonly status: number
  readonly code: string
  readonly detail: string
  readonly approval: Approval | null

  constructor(status: number, code: string, detail = '', approval: Approval | null = null) {
    super(detail !== '' ? `approval: ${code}: ${detail}` : `approval: ${code} (HTTP ${status})`)
    this.name = 'ApprovalApiError'
    this.status = status
    this.code = code
    this.detail = detail
    this.approval = approval
  }
}

function isRecord(v: unknown): v is Record<string, unknown> {
  return typeof v === 'object' && v !== null && !Array.isArray(v)
}

async function errorFromResponse(res: Response): Promise<ApprovalApiError> {
  const fallback = `http_${res.status}`
  let text = ''
  try {
    text = await res.text()
  } catch {
    return new ApprovalApiError(res.status, fallback)
  }
  let parsed: unknown
  try {
    parsed = JSON.parse(text)
  } catch {
    // Not our JSON: Go's mux answers a missing route with text — an older daemon without the team module.
    return new ApprovalApiError(res.status, res.status === 404 ? 'unsupported' : fallback)
  }
  if (!isRecord(parsed)) return new ApprovalApiError(res.status, fallback)
  const body = parsed as Partial<APIError>
  const code = typeof body.error === 'string' && body.error !== '' ? body.error : fallback
  const detail = typeof body.detail === 'string' ? body.detail : ''
  const approval = isRecord(body.approval) ? (body.approval as unknown as Approval) : null
  return new ApprovalApiError(res.status, code, detail, approval)
}

async function send<T>(hostId: string, path: string, init: RequestInit): Promise<T> {
  if (!Object.hasOwn(useHostStore.getState().hosts, hostId)) throw new ApprovalApiError(0, 'host_removed')
  let res: Response
  try {
    res = await pinnedHostFetch(hostId, path, init)
  } catch (e: unknown) {
    throw new ApprovalApiError(0, 'network', e instanceof Error ? e.message : String(e))
  }
  if (!res.ok) throw await errorFromResponse(res)
  return (await res.json()) as T
}

/** `POST /api/team/approvals/{id}/decide` — one click (U5b). 200 → the closed Approval; 409 `already_decided` → error with `approval`. */
export function decideApproval(hostId: string, id: string, body: DecideRequest): Promise<Approval> {
  return send<Approval>(hostId, `/api/team/approvals/${encodeURIComponent(id)}/decide`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  })
}

/** `GET /api/team/approvals?state=open`. `approvals` is `[]` from the daemon, but null is tolerated. */
export async function listOpenApprovals(hostId: string): Promise<Approval[]> {
  const r = await send<{ approvals?: Approval[] | null }>(hostId, '/api/team/approvals?state=open', { method: 'GET' })
  return Array.isArray(r.approvals) ? r.approvals : []
}

const count = (v: unknown): number => (typeof v === 'number' && Number.isFinite(v) && v > 0 ? Math.trunc(v) : 0)

/**
 * `GET /api/team/inflight` (spec §9.5), for the daemon-restart confirm. Bounded by its own AbortController so the
 * dialog's 3 s budget holds even when the daemon accepts and never answers; an abort rejects as code `network`,
 * like any other transport failure, and the caller falls back to its store. A missing count reads as 0.
 */
export async function fetchInflight(hostId: string, timeoutMs = INFLIGHT_TIMEOUT_MS): Promise<InflightResponse> {
  const ctl = new AbortController()
  const timer = setTimeout(() => ctl.abort(), timeoutMs)
  try {
    const r = await send<Partial<InflightResponse>>(hostId, '/api/team/inflight', { method: 'GET', signal: ctl.signal })
    return { approvals_open: count(r.approvals_open), relays_active: count(r.relays_active) }
  } finally {
    clearTimeout(timer)
  }
}
```

- [ ] **Step 4: Run the test and verify it passes.**
  - Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/lead-team/spa && npx vitest run src/lib/team/approval-api.test.ts`
  - Expected: PASS, 14 tests.

- [ ] **Step 5: Commit.**
  ```bash
  git add spa/src/lib/team/types.ts spa/src/lib/team/approval-api.ts spa/src/lib/team/approval-api.test.ts
  git commit -m "feat(spa): approval wire types and API client for /api/team/approvals

  Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
  ```

### Task 3.2: `useApprovalStore` — open requests per host, queued decisions, oldest-first selector

**Files:**
- Create: `spa/src/stores/useApprovalStore.ts`
- Test: `spa/src/stores/useApprovalStore.test.ts`

**Interfaces:**
- Consumes: `Approval`, `Grant` from `spa/src/lib/team/types.ts` (Task 3.1).
- Produces:
  ```ts
  export type Decision = 'approve' | 'deny'
  export interface ApprovalEntry { hostId: string; approval: Approval }
  export interface QueuedDecision { hostId: string; approval: Approval; decision: Decision; grant?: Grant }
  export function approvalKey(hostId: string, id: string): string            // `${hostId}\u0000${id}`
  interface ApprovalStoreState {
    entries: Record<string, ApprovalEntry>        // open requests only
    queued: Record<string, QueuedDecision>        // decisions clicked while the host was not connected
    decidedHere: Record<string, true>             // ids this app sent a decide for (no "elsewhere" toast)
    closedIds: Record<string, string[]>           // per host: ids closed in this socket generation, oldest first, ≤ 256 (tombstones)
    applySnapshot: (hostId: string, approvals: Approval[]) => string[]   // replaces the host's set and clears its tombstones; returns the ids that vanished
    applyOpened: (hostId: string, approval: Approval) => boolean          // idempotent; true when it added; false for a tombstoned id
    applyClosed: (hostId: string, approval: Approval) => 'absent' | 'ours' | 'elsewhere'   // always records the tombstone, 'absent' included
    markDecidedHere: (hostId: string, id: string) => void
    unmarkDecidedHere: (hostId: string, id: string) => void
    queueDecision: (hostId: string, approval: Approval, decision: Decision, grant?: Grant) => void
    takeQueued: (hostId: string) => QueuedDecision[]                      // removes and returns the host's queue
    reset: () => void
  }
  export const useApprovalStore: UseBoundStore<StoreApi<ApprovalStoreState>>
  export const selectCurrent: (s: ApprovalStoreState) => ApprovalEntry | null     // oldest created_at across hosts
  export const selectOpenCount: (s: ApprovalStoreState) => number
  export const selectOpenCountFor: (hostId: string) => (s: ApprovalStoreState) => number
  ```
  Never persisted (plain `create`, as `useHandoffDialogStore`): it is what is on screen right now; the daemon's snapshot rebuilds it on every connection.

- [ ] **Step 1: Write the failing test.**

```ts
// spa/src/stores/useApprovalStore.test.ts — the open approval requests this app shows (lead-team spec §6.3),
// per host, oldest first, with the decisions clicked while a host was not connected (spec §9.4).
import { beforeEach, describe, expect, it } from 'vitest'
import { approvalKey, selectCurrent, selectOpenCount, selectOpenCountFor, useApprovalStore } from './useApprovalStore'
import type { Approval } from '../lib/team/types'

const approval = (over: Partial<Approval> = {}): Approval => ({
  id: 'req-1', kind: 'lead', host_id: 'd1',
  origin: { session_id: 'S1', ref: '_40iueq', name: 'purdex-7c', pid: 1, proc_start: 'p', cwd: '/w', tmux: '' },
  payload: { reason: 'r', max_members: 3, roots: ['/w'] },
  state: 'open', created_at: 1_000, deadline_at: 541_000, lease_until: 31_000,
  ...over,
})
const s = () => useApprovalStore.getState()
const ids = () => Object.values(s().entries).map((e) => `${e.hostId}:${e.approval.id}`).sort()

beforeEach(() => s().reset())

describe('useApprovalStore', () => {
  it('keys entries by hostId NUL id, so the same request id on two hosts never collides', () => {
    expect(approvalKey('h1', 'req-1')).toBe('h1\u0000req-1')
    s().applyOpened('h1', approval())
    s().applyOpened('h2', approval())
    expect(ids()).toEqual(['h1:req-1', 'h2:req-1'])
    expect(selectOpenCount(s())).toBe(2)
    expect(selectOpenCountFor('h1')(s())).toBe(1)
  })

  describe('applySnapshot', () => {
    it('replaces that host\'s set only and returns the ids that vanished', () => {
      s().applyOpened('h1', approval({ id: 'a' }))
      s().applyOpened('h1', approval({ id: 'b' }))
      s().applyOpened('h2', approval({ id: 'c' }))
      const vanished = s().applySnapshot('h1', [approval({ id: 'b' }), approval({ id: 'd' })])
      expect(vanished).toEqual(['a'])
      expect(ids()).toEqual(['h1:b', 'h1:d', 'h2:c'])
    })

    it('an empty snapshot clears the host (a request that closed while disconnected disappears)', () => {
      s().applyOpened('h1', approval({ id: 'a' }))
      expect(s().applySnapshot('h1', [])).toEqual(['a'])
      expect(ids()).toEqual([])
    })

    it('does not duplicate a request already held, and keeps only open ones', () => {
      s().applyOpened('h1', approval({ id: 'a' }))
      s().applySnapshot('h1', [approval({ id: 'a' }), approval({ id: 'z', state: 'denied' })])
      expect(ids()).toEqual(['h1:a'])
    })
  })

  describe('applyOpened', () => {
    it('adds once: a second opened for the same request is ignored and reports false', () => {
      expect(s().applyOpened('h1', approval())).toBe(true)
      expect(s().applyOpened('h1', approval({ created_at: 999 }))).toBe(false)
      expect(s().entries[approvalKey('h1', 'req-1')].approval.created_at).toBe(1_000)
    })

    it('ignores a non-open approval', () => {
      expect(s().applyOpened('h1', approval({ state: 'timeout' }))).toBe(false)
      expect(ids()).toEqual([])
    })
  })

  describe('applyClosed', () => {
    it('removes the entry and tells whether the decision was ours, elsewhere, or for an unknown request', () => {
      s().applyOpened('h1', approval({ id: 'a' }))
      s().applyOpened('h1', approval({ id: 'b' }))
      s().markDecidedHere('h1', 'a')
      expect(s().applyClosed('h1', approval({ id: 'a', state: 'approved' }))).toBe('ours')
      expect(s().applyClosed('h1', approval({ id: 'b', state: 'denied' }))).toBe('elsewhere')
      expect(s().applyClosed('h1', approval({ id: 'b', state: 'denied' }))).toBe('absent')
      expect(ids()).toEqual([])
      expect(s().decidedHere).toEqual({})
    })

    it('drops a queued decision for the closed request too', () => {
      s().applyOpened('h1', approval({ id: 'a' }))
      s().queueDecision('h1', approval({ id: 'a' }), 'deny')
      s().applyClosed('h1', approval({ id: 'a', state: 'timeout' }))
      expect(s().takeQueued('h1')).toEqual([])
    })

    it('unmarkDecidedHere undoes the mark (a send that never reached the daemon)', () => {
      s().applyOpened('h1', approval({ id: 'a' }))
      s().markDecidedHere('h1', 'a')
      s().unmarkDecidedHere('h1', 'a')
      expect(s().applyClosed('h1', approval({ id: 'a', state: 'approved' }))).toBe('elsewhere')
    })
  })

  // A `closed` that reaches this app before its `opened` (a socket that connected between the two): the late
  // `opened` must not revive a request the daemon already closed. The snapshot is authoritative and clears the marks.
  describe('a closed arriving before its opened (tombstones)', () => {
    it('applyClosed on an unknown id is `absent` and still tombstones it; the late applyOpened is ignored', () => {
      expect(s().applyClosed('h1', approval({ id: 'a', state: 'denied' }))).toBe('absent')
      expect(s().applyOpened('h1', approval({ id: 'a' }))).toBe(false)
      expect(ids()).toEqual([])
      expect(selectOpenCountFor('h1')(s())).toBe(0)
    })

    it('the snapshot clears the host\'s tombstones: a request the daemon lists as open is shown again', () => {
      s().applyClosed('h1', approval({ id: 'a', state: 'denied' }))
      s().applySnapshot('h1', [approval({ id: 'a' })])
      expect(ids()).toEqual(['h1:a'])
      expect(s().closedIds).toEqual({})
    })

    it('a close of a held request tombstones it too, so a duplicate opened after the close is ignored', () => {
      s().applyOpened('h1', approval({ id: 'a' }))
      s().applyClosed('h1', approval({ id: 'a', state: 'approved' }))
      expect(s().applyOpened('h1', approval({ id: 'a' }))).toBe(false)
      expect(ids()).toEqual([])
    })

    it('tombstones are per host', () => {
      s().applyClosed('h1', approval({ id: 'a', state: 'denied' }))
      expect(s().applyOpened('h2', approval({ id: 'a' }))).toBe(true)
      expect(ids()).toEqual(['h2:a'])
    })

    it('keeps at most 256 ids per host, dropping the oldest', () => {
      for (let i = 0; i < 257; i++) s().applyClosed('h1', approval({ id: `c${i}`, state: 'timeout' }))
      expect(s().closedIds.h1).toHaveLength(256)
      expect(s().applyOpened('h1', approval({ id: 'c0' }))).toBe(true) // evicted: oldest first
      expect(s().applyOpened('h1', approval({ id: 'c1' }))).toBe(false)
      expect(s().applyOpened('h1', approval({ id: 'c256' }))).toBe(false)
    })

    it('reset clears them', () => {
      s().applyClosed('h1', approval({ id: 'a', state: 'denied' }))
      s().reset()
      expect(s().closedIds).toEqual({})
      expect(s().applyOpened('h1', approval({ id: 'a' }))).toBe(true)
    })
  })

  describe('queued decisions', () => {
    it('queueDecision keeps one decision per request (the last click wins); takeQueued removes and returns that host\'s', () => {
      const a = approval({ id: 'a' })
      s().applyOpened('h1', a)
      s().applyOpened('h2', approval({ id: 'c' }))
      s().queueDecision('h1', a, 'approve', { max_members: 2, roots: ['/w'] })
      s().queueDecision('h1', a, 'deny')
      s().queueDecision('h2', approval({ id: 'c' }), 'approve')
      expect(s().takeQueued('h1')).toEqual([{ hostId: 'h1', approval: a, decision: 'deny', grant: undefined }])
      expect(s().takeQueued('h1')).toEqual([])
      expect(s().takeQueued('h2')).toHaveLength(1)
    })
  })

  describe('selectCurrent', () => {
    it('is the oldest created_at across hosts, ties broken by id; null when empty', () => {
      expect(selectCurrent(s())).toBeNull()
      s().applyOpened('h2', approval({ id: 'late', created_at: 3_000 }))
      s().applyOpened('h1', approval({ id: 'b', created_at: 2_000 }))
      s().applyOpened('h3', approval({ id: 'a', created_at: 2_000 }))
      expect(selectCurrent(s())).toMatchObject({ hostId: 'h3', approval: { id: 'a' } })
      s().applyClosed('h3', approval({ id: 'a', state: 'denied' }))
      expect(selectCurrent(s())).toMatchObject({ hostId: 'h1', approval: { id: 'b' } })
    })

    it('returns the stored entry object itself, so a zustand selector sees a stable reference', () => {
      s().applyOpened('h1', approval())
      expect(selectCurrent(s())).toBe(s().entries[approvalKey('h1', 'req-1')])
    })
  })
})
```

- [ ] **Step 2: Run the test and verify it fails.**
  - Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/lead-team/spa && npx vitest run src/stores/useApprovalStore.test.ts`
  - Expected: FAIL — `Failed to resolve import "./useApprovalStore"`.

- [ ] **Step 3: Implement the store.**

```ts
// spa/src/stores/useApprovalStore.ts — the open approval requests this app shows (lead-team spec §6.3), one
// entry per (host, request id), fed by the `approval.request` WS branch and the daemon's OnSubscribe snapshot.
// Never persisted: it is what is on screen right now, and every new connection replays the open set.
//
// `decidedHere` marks the requests this app sent a decide for, so the `closed` that follows our own 200 does
// not toast "已由 … 核准" at the person who clicked. `queued` holds a decision clicked while the host was not
// connected (spec §9.4); the snapshot that arrives on reconnect either re-adds the request (the decision is
// sent then, once) or shows it gone (toast). `closedIds` are per-host tombstones: a `closed` that arrives
// before its `opened` (a socket that connected between the two) must not let the late `opened` revive a
// request the daemon already closed; the snapshot is authoritative and clears them. The store is pure data;
// the sending lives in lib/team.
import { create } from 'zustand'
import type { Approval, Grant } from '../lib/team/types'

/** Tombstones kept per host; past this the oldest is dropped (a request id is a UUID, 256 is hours of closes). */
export const TOMBSTONES_PER_HOST = 256

export type Decision = 'approve' | 'deny'

export interface ApprovalEntry {
  hostId: string
  approval: Approval
}

export interface QueuedDecision {
  hostId: string
  approval: Approval
  decision: Decision
  grant?: Grant
}

/** NUL-joined, as the pane-keyed dialogs do: a host id or a request id may contain any printable separator. */
export const approvalKey = (hostId: string, id: string): string => `${hostId}\u0000${id}`

interface ApprovalStoreState {
  entries: Record<string, ApprovalEntry>
  queued: Record<string, QueuedDecision>
  decidedHere: Record<string, true>
  /** Per host, the ids closed in this socket generation, oldest first, at most TOMBSTONES_PER_HOST. */
  closedIds: Record<string, string[]>
  /** Replace the host's whole open set from a snapshot and clear its tombstones; returns the ids held before that are not in it. */
  applySnapshot: (hostId: string, approvals: Approval[]) => string[]
  /** Add an opened request; false when it was already held, is not open, or was closed before (tombstoned). */
  applyOpened: (hostId: string, approval: Approval) => boolean
  /** Remove a request on any close and tombstone its id (absent or not). Tells whether the decision was ours, elsewhere, or the request unknown. */
  applyClosed: (hostId: string, approval: Approval) => 'absent' | 'ours' | 'elsewhere'
  markDecidedHere: (hostId: string, id: string) => void
  unmarkDecidedHere: (hostId: string, id: string) => void
  queueDecision: (hostId: string, approval: Approval, decision: Decision, grant?: Grant) => void
  /** Remove and return the host's queued decisions (each is sent at most once). */
  takeQueued: (hostId: string) => QueuedDecision[]
  reset: () => void
}

function without<T>(record: Record<string, T>, key: string): Record<string, T> {
  if (!Object.hasOwn(record, key)) return record
  const next = { ...record }
  delete next[key]
  return next
}

/** `closedIds` with `id` appended for `hostId` (once), the oldest dropped past the cap. */
function tombstone(closedIds: Record<string, string[]>, hostId: string, id: string): Record<string, string[]> {
  const had = closedIds[hostId] ?? []
  if (had.includes(id)) return closedIds
  const next = had.length >= TOMBSTONES_PER_HOST ? had.slice(had.length - TOMBSTONES_PER_HOST + 1) : had
  return { ...closedIds, [hostId]: [...next, id] }
}

export const useApprovalStore = create<ApprovalStoreState>()((set, get) => ({
  entries: {},
  queued: {},
  decidedHere: {},
  closedIds: {},

  applySnapshot: (hostId, approvals) => {
    const open = approvals.filter((a) => a.state === 'open')
    const keep = new Set(open.map((a) => approvalKey(hostId, a.id)))
    const next: Record<string, ApprovalEntry> = {}
    const vanished: string[] = []
    for (const [key, entry] of Object.entries(get().entries)) {
      if (entry.hostId !== hostId) {
        next[key] = entry
      } else if (!keep.has(key)) {
        vanished.push(entry.approval.id)
      }
    }
    for (const a of open) {
      const key = approvalKey(hostId, a.id)
      // Keep the held object when the same request is still open: the dialog is keyed on it.
      next[key] = get().entries[key] ?? { hostId, approval: a }
    }
    // The daemon's snapshot is authoritative: whatever it lists is open now, tombstones or not.
    set({ entries: next, closedIds: without(get().closedIds, hostId) })
    return vanished
  },

  applyOpened: (hostId, approval) => {
    if (approval.state !== 'open') return false
    const key = approvalKey(hostId, approval.id)
    const { entries, closedIds } = get()
    if (Object.hasOwn(entries, key)) return false
    // Its `closed` already came through this socket: the daemon closed it, this `opened` is late.
    if (closedIds[hostId]?.includes(approval.id)) return false
    set((s) => ({ entries: { ...s.entries, [key]: { hostId, approval } } }))
    return true
  },

  applyClosed: (hostId, approval) => {
    const key = approvalKey(hostId, approval.id)
    const { entries, queued, decidedHere, closedIds } = get()
    const had = Object.hasOwn(entries, key)
    const ours = Object.hasOwn(decidedHere, key)
    set({
      entries: without(entries, key),
      queued: without(queued, key),
      decidedHere: without(decidedHere, key),
      closedIds: tombstone(closedIds, hostId, approval.id),
    })
    if (!had) return 'absent'
    return ours ? 'ours' : 'elsewhere'
  },

  markDecidedHere: (hostId, id) => set((s) => ({ decidedHere: { ...s.decidedHere, [approvalKey(hostId, id)]: true } })),
  unmarkDecidedHere: (hostId, id) => set((s) => ({ decidedHere: without(s.decidedHere, approvalKey(hostId, id)) })),

  queueDecision: (hostId, approval, decision, grant) =>
    set((s) => ({ queued: { ...s.queued, [approvalKey(hostId, approval.id)]: { hostId, approval, decision, grant } } })),

  takeQueued: (hostId) => {
    const taken: QueuedDecision[] = []
    const rest: Record<string, QueuedDecision> = {}
    for (const [key, q] of Object.entries(get().queued)) {
      if (q.hostId === hostId) taken.push(q)
      else rest[key] = q
    }
    if (taken.length > 0) set({ queued: rest })
    return taken
  },

  reset: () => set({ entries: {}, queued: {}, decidedHere: {}, closedIds: {} }),
}))

/** The request the dialog shows: the oldest `created_at` across hosts (spec §6.3 "oldest first"); ties by id, then host. */
export const selectCurrent = (s: ApprovalStoreState): ApprovalEntry | null => {
  let best: ApprovalEntry | null = null
  for (const e of Object.values(s.entries)) {
    if (best === null) { best = e; continue }
    const a = e.approval, b = best.approval
    if (a.created_at < b.created_at || (a.created_at === b.created_at && (a.id < b.id || (a.id === b.id && e.hostId < best.hostId)))) best = e
  }
  return best
}

export const selectOpenCount = (s: ApprovalStoreState): number => Object.keys(s.entries).length

export const selectOpenCountFor = (hostId: string) => (s: ApprovalStoreState): number => {
  let n = 0
  for (const e of Object.values(s.entries)) if (e.hostId === hostId) n++
  return n
}
```

- [ ] **Step 4: Run the test and verify it passes.**
  - Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/lead-team/spa && npx vitest run src/stores/useApprovalStore.test.ts`
  - Expected: PASS, 18 tests.
  - Mutation check (Review Focus 5, "nor revives one that closed meanwhile"; measured 2026-10-07 in the scratch build): delete the `closedIds[hostId]?.includes(...)` line in `applyOpened` and rerun — three tombstone tests fail with `expected true to be false` (`applyClosed on an unknown id …`, `a close of a held request …`, `keeps at most 256 …`), 15 pass. Put it back.

- [ ] **Step 5: Commit.**
  ```bash
  git add spa/src/stores/useApprovalStore.ts spa/src/stores/useApprovalStore.test.ts
  git commit -m "feat(spa): useApprovalStore — open approval requests per host, queued decisions

  Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
  ```

### Task 3.3: The `approval.request` WS branch — snapshot / opened / closed into the store, "closed elsewhere" toast

**Files:**
- Create: `spa/src/lib/team/approval-format.ts`
- Create: `spa/src/lib/team/approval-ws.ts`
- Modify: `spa/src/lib/host-events.ts:15` (union member after `| 'nex-worker-exited'`)
- Modify: `spa/src/hooks/useMultiHostEventWs.ts:21` (import), `spa/src/hooks/useMultiHostEventWs.ts:213-217` (branch inserted right after the `nex-worker-exited` branch, before the `handoff` / `relay` comment at 218)
- Modify: `spa/src/locales/en.json:1896-1897`, `spa/src/locales/zh-TW.json:1896-1897` (keys appended after `worker.exit.after_transfer_failed`)
- Test: `spa/src/hooks/useMultiHostEventWs.approval.test.ts`

**Interfaces:**
- Consumes: `connectHostEvents`'s `onEvent` closure (`useMultiHostEventWs.ts:171`), `useApprovalStore` (Task 3.2), `useUndoToast.getState().show`, `useI18nStore.getState().t`, `hostLabel` / `hostLookOf` (`host-look.ts:116, 149`).
- Produces:
  ```ts
  // approval-format.ts
  export type T = (key: string, params?: Record<string, string | number>) => string
  export function approvalSessionLabel(o: Origin): string                 // title, else name, else ref
  export function formatOriginAddress(host: string, o: Origin): string    // o.address, else `<host>/<name> [<ref6>]`, else `<host>/_<ref6>`
  export function approvalKindLabel(t: T, kind: ApprovalKind): string     // 'lead 申請' / '接力申請'
  export function closedToastText(t: T, host: string, a: Approval): string
  export function formatCountdown(ms: number): string                     // 'm:ss', floored at 0:00
  // approval-ws.ts
  export function parseApprovalEvent(value: unknown): ApprovalEventValue | null
  export function toastClosed(hostId: string, approval: Approval): void
  export function handleApprovalEvent(hostId: string, value: unknown): void
  ```
  `host-events.ts`: `HostEvent['type']` gains `'approval.request'`.

- [ ] **Step 1: Write the failing test.**

```ts
// spa/src/hooks/useMultiHostEventWs.approval.test.ts — the `approval.request` host event (lead-team spec §6.2–§6.3):
// the snapshot a new subscriber gets populates the host's open set, `opened` adds, `closed` removes — and a close
// decided on another client toasts who handled it (U6). Harness as useMultiHostEventWs.worker-exited.test.ts.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { renderHook, act, waitFor } from '@testing-library/react'
import { useHostStore } from '../stores/useHostStore'
import { useSessionStore } from '../stores/useSessionStore'
import { useApprovalStore, approvalKey } from '../stores/useApprovalStore'
import { useUndoToast } from '../stores/useUndoToast'
import { useI18nStore } from '../stores/useI18nStore'
import type { Approval } from '../lib/team/types'

vi.mock('../lib/host-connection', () => ({
  checkHealth: vi.fn(async () => ({ daemon: 'connected', latency: 3, ticket: 'tk' })),
}))

const { useMultiHostEventWs } = await import('./useMultiHostEventWs')

const HOST = 'h1'

class FakeSocket {
  static OPEN = 1
  readyState = 0
  binaryType = ''
  url: string
  onopen: (() => void) | null = null
  onclose: (() => void) | null = null
  onmessage: ((e: { data: unknown }) => void) | null = null
  onerror: (() => void) | null = null
  send = vi.fn()
  close = vi.fn(() => { this.readyState = 3 })
  constructor(url: string) { this.url = url; sockets.push(this) }
  emit(data: string) { this.onmessage?.({ data }) }
}

let sockets: FakeSocket[] = []

const approval = (over: Partial<Approval> = {}): Approval => ({
  id: 'req-1', kind: 'lead', host_id: 'd1',
  origin: { session_id: 'S1', ref: '_40iueq', name: 'purdex-7c', pid: 1, proc_start: 'p', cwd: '/w/purdex', tmux: 'purdex:@1.%2' },
  payload: { reason: '要平行跑三個 PR', max_members: 3, roots: ['/w/purdex'] },
  state: 'open', created_at: 1_000, deadline_at: 541_000, lease_until: 31_000,
  ...over,
})
const frame = (value: unknown) => JSON.stringify({ type: 'approval.request', session: '', value: typeof value === 'string' ? value : JSON.stringify(value) })
const held = () => Object.values(useApprovalStore.getState().entries).map((e) => e.approval.id).sort()

beforeEach(() => {
  sockets = []
  vi.stubGlobal('WebSocket', FakeSocket)
  useI18nStore.getState().setLocale('zh-TW')
  useHostStore.setState({
    hosts: { [HOST]: { id: HOST, name: 'mlab', ip: '1.2.3.4', port: 7860, order: 0 } },
    hostOrder: [HOST],
    runtime: {},
    activeHostId: HOST,
  })
  useSessionStore.setState({ fetchHost: vi.fn(async () => {}), replaceHost: vi.fn() } as never)
  useApprovalStore.getState().reset()
  useUndoToast.setState({ toast: null, notice: null })
})

afterEach(() => {
  vi.unstubAllGlobals()
  useHostStore.getState().reset()
})

async function connected() {
  const view = renderHook(() => useMultiHostEventWs())
  await waitFor(() => expect(sockets).toHaveLength(1))
  return { view, ws: sockets[0] }
}

describe('useMultiHostEventWs approval.request', () => {
  it('snapshot populates the host\'s set, keyed by this host id; opened adds; closed removes', async () => {
    const { view, ws } = await connected()
    act(() => { ws.emit(frame({ op: 'snapshot', approvals: [approval({ id: 'a' }), approval({ id: 'b', created_at: 2_000 })] })) })
    expect(held()).toEqual(['a', 'b'])
    expect(useApprovalStore.getState().entries[approvalKey(HOST, 'a')]).toMatchObject({ hostId: HOST, approval: { id: 'a', host_id: 'd1' } })
    act(() => { ws.emit(frame({ op: 'opened', approval: approval({ id: 'c', created_at: 3_000 }) })) })
    expect(held()).toEqual(['a', 'b', 'c'])
    act(() => { ws.emit(frame({ op: 'closed', approval: approval({ id: 'b', state: 'timeout', decided_at: 4_000 }) })) })
    expect(held()).toEqual(['a', 'c'])
    view.unmount()
  })

  it('a snapshot replaces the set: a request closed while this client was away disappears, and nothing is duplicated', async () => {
    const { view, ws } = await connected()
    act(() => { ws.emit(frame({ op: 'snapshot', approvals: [approval({ id: 'a' }), approval({ id: 'gone' })] })) })
    act(() => { ws.emit(frame({ op: 'snapshot', approvals: [approval({ id: 'a' })] })) })
    expect(held()).toEqual(['a'])
    act(() => { ws.emit(frame({ op: 'snapshot', approvals: [] })) })
    expect(held()).toEqual([])
    view.unmount()
  })

  it('a duplicate opened is ignored', async () => {
    const { view, ws } = await connected()
    act(() => { ws.emit(frame({ op: 'opened', approval: approval() })) })
    act(() => { ws.emit(frame({ op: 'opened', approval: approval() })) })
    expect(held()).toEqual(['req-1'])
    view.unmount()
  })

  it('unknown ops, malformed approvals and non-JSON values are ignored', async () => {
    const { view, ws } = await connected()
    act(() => { ws.emit(frame({ op: 'nope', approval: approval() })) })
    act(() => { ws.emit(frame({ op: 'opened', approval: { id: '' } })) })
    act(() => { ws.emit(frame({ op: 'opened' })) })
    act(() => { ws.emit(frame('not json')) })
    act(() => { ws.emit(frame({ op: 'snapshot', approvals: null })) })
    expect(held()).toEqual([])
    expect(useUndoToast.getState().toast).toBeNull()
    view.unmount()
  })

  it('closed by another client → toast "<主機>：<session> 的 lead 申請 已由 <client> 核准"', async () => {
    const { view, ws } = await connected()
    act(() => { ws.emit(frame({ op: 'opened', approval: approval() })) })
    act(() => {
      ws.emit(frame({ op: 'closed', approval: approval({ state: 'approved', decided_by: { kind: 'app', label: 'Purdex.app @ air26', addr: '100.64.0.4:5' }, decided_at: 5_000 }) }))
    })
    expect(held()).toEqual([])
    expect(useUndoToast.getState().toast?.message).toBe('mlab：purdex-7c 的 lead 申請 已由 Purdex.app @ air26 核准')
    view.unmount()
  })

  it('denied elsewhere says 拒絕; a timeout says it ended; a close for a request never shown toasts nothing', async () => {
    const { view, ws } = await connected()
    act(() => { ws.emit(frame({ op: 'opened', approval: approval({ id: 'a' }) })) })
    act(() => { ws.emit(frame({ op: 'closed', approval: approval({ id: 'a', state: 'denied', decided_by: { kind: 'app', label: 'Purdex.app @ a19' } }) })) })
    expect(useUndoToast.getState().toast?.message).toBe('mlab：purdex-7c 的 lead 申請 已由 Purdex.app @ a19 拒絕')
    act(() => { ws.emit(frame({ op: 'opened', approval: approval({ id: 'b' }) })) })
    act(() => { ws.emit(frame({ op: 'closed', approval: approval({ id: 'b', state: 'timeout' }) })) })
    expect(useUndoToast.getState().toast?.message).toBe('mlab：purdex-7c 的 lead 申請已結束（逾時）')
    useUndoToast.setState({ toast: null })
    act(() => { ws.emit(frame({ op: 'closed', approval: approval({ id: 'never-held', state: 'approved', decided_by: { kind: 'app', label: 'x' } }) })) })
    expect(useUndoToast.getState().toast).toBeNull()
    view.unmount()
  })

  it('a close for a request this app decided itself does not toast', async () => {
    const { view, ws } = await connected()
    act(() => { ws.emit(frame({ op: 'opened', approval: approval() })) })
    useApprovalStore.getState().markDecidedHere(HOST, 'req-1')
    act(() => { ws.emit(frame({ op: 'closed', approval: approval({ state: 'approved', decided_by: { kind: 'app', label: 'Purdex.app @ mlab' } }) })) })
    expect(held()).toEqual([])
    expect(useUndoToast.getState().toast).toBeNull()
    view.unmount()
  })
})
```

- [ ] **Step 2: Run the test and verify it fails.**
  - Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/lead-team/spa && npx vitest run src/hooks/useMultiHostEventWs.approval.test.ts`
  - Expected: FAIL — the first test's `expect(held()).toEqual(['a', 'b'])` receives `[]` (the hook has no branch for the type; the frame falls through `isAgentWsEvent` untouched).

- [ ] **Step 3: Implement.**

`spa/src/lib/team/approval-format.ts`:

```ts
// spa/src/lib/team/approval-format.ts — the strings the approval dialog and its toasts are built from
// (lead-team spec §6.3, §6.5). Pure; the i18n `t` is passed in so lib code and tests do not depend on the
// store's locale.
import type { Approval, ApprovalKind, Origin } from './types'

export type T = (key: string, params?: Record<string, string | number>) => string

/** What to call the requesting session: its title (not on the wire yet), else its registry name, else its ref. */
export function approvalSessionLabel(o: Origin): string {
  const title = o.title?.trim() ?? ''
  if (title !== '') return title
  return o.name !== '' ? o.name : o.ref
}

/**
 * The pdx address, as `pdx peers` prints it: `<host>/<name> [<ref>]` with the ref's underscore dropped inside the
 * brackets, or `<host>/_<ref>` for a session without a routable name. The daemon's own `address` wins when present.
 */
export function formatOriginAddress(host: string, o: Origin): string {
  if (o.address) return o.address
  const ref6 = o.ref.startsWith('_') ? o.ref.slice(1) : o.ref
  return o.name !== '' ? `${host}/${o.name} [${ref6}]` : `${host}/_${ref6}`
}

export function approvalKindLabel(t: T, kind: ApprovalKind): string {
  return t(kind === 'self_relay' ? 'approval.kind.self_relay' : 'approval.kind.lead')
}

/**
 * The toast for a request closed by someone else (spec §6.3 / §6.5): `<主機>：<session> 的 <kind> 已由 <client> 核准／拒絕`;
 * a timeout, cancel or abandonment names the state instead.
 */
export function closedToastText(t: T, host: string, a: Approval): string {
  const session = approvalSessionLabel(a.origin)
  const kind = approvalKindLabel(t, a.kind)
  if ((a.state === 'approved' || a.state === 'denied') && a.decided_by) {
    const decision = t(a.state === 'approved' ? 'approval.decision.approved' : 'approval.decision.denied')
    return t('approval.toast.decided_elsewhere', { host, session, kind, client: a.decided_by.label, decision })
  }
  return t('approval.toast.ended', { host, session, kind, state: t(`approval.state.${a.state}`) })
}

function pad2(n: number): string {
  return n < 10 ? `0${n}` : String(n)
}

/** `m:ss` to the deadline, never below `0:00`; ceil so the last second shows `0:01`, not `0:00` early. */
export function formatCountdown(ms: number): string {
  const s = Number.isFinite(ms) && ms > 0 ? Math.ceil(ms / 1000) : 0
  return `${Math.floor(s / 60)}:${pad2(s % 60)}`
}
```

`spa/src/lib/team/approval-ws.ts`:

```ts
// spa/src/lib/team/approval-ws.ts — the daemon's `approval.request` host event (lead-team spec §6.2):
//   {op:"snapshot", approvals:[…]}  to each new subscriber — replaces this host's open set (PD2);
//   {op:"opened", approval}         on create;
//   {op:"closed", approval}         on every close, carrying decided_by / decided_at.
// Called from useMultiHostEventWs with the per-host closure's hostId. Store first, then the side effects.
import { useApprovalStore } from '../../stores/useApprovalStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { useUndoToast } from '../../stores/useUndoToast'
import { hostLabel, hostLookOf } from '../host-look'
import { closedToastText } from './approval-format'
import type { Approval, ApprovalEventValue } from './types'

function isRecord(v: unknown): v is Record<string, unknown> {
  return typeof v === 'object' && v !== null && !Array.isArray(v)
}

function isApproval(v: unknown): v is Approval {
  return isRecord(v)
    && typeof v.id === 'string' && v.id !== ''
    && typeof v.state === 'string'
    && isRecord(v.origin)
    && typeof v.created_at === 'number'
}

/** The event's `value`: JSON text or an object; null when it is not an approval event we understand. */
export function parseApprovalEvent(value: unknown): ApprovalEventValue | null {
  let o: unknown = value
  if (typeof value === 'string') {
    try {
      o = JSON.parse(value)
    } catch {
      return null
    }
  }
  if (!isRecord(o)) return null
  if ((o.op === 'opened' || o.op === 'closed') && isApproval(o.approval)) return { op: o.op, approval: o.approval }
  if (o.op === 'snapshot') return { op: 'snapshot', approvals: Array.isArray(o.approvals) ? o.approvals.filter(isApproval) : [] }
  return null
}

/** The "handled elsewhere" toast (spec §6.3): who closed it, on which host, for which session. */
export function toastClosed(hostId: string, approval: Approval): void {
  const t = useI18nStore.getState().t
  useUndoToast.getState().show(closedToastText(t, hostLabel(hostId, hostLookOf(hostId)), approval))
}

export function handleApprovalEvent(hostId: string, value: unknown): void {
  const ev = parseApprovalEvent(value)
  if (!ev) return
  const store = useApprovalStore.getState()
  if (ev.op === 'snapshot') {
    store.applySnapshot(hostId, ev.approvals)
    return
  }
  if (ev.op === 'opened') {
    store.applyOpened(hostId, ev.approval)
    return
  }
  // closed: the dialog closes everywhere; only a decision made elsewhere is announced.
  if (store.applyClosed(hostId, ev.approval) === 'elsewhere') toastClosed(hostId, ev.approval)
}
```

`spa/src/lib/host-events.ts` — line 15 becomes two lines:

```ts
    | 'nex-worker-exited'
    | 'approval.request'
```

`spa/src/hooks/useMultiHostEventWs.ts` — after line 21 (`import { handleWorkerExited } from '../lib/nex/worker-exited-event'`) add:

```ts
import { handleApprovalEvent } from '../lib/team/approval-ws'
```

and after line 217 (the closing `}` of the `nex-worker-exited` branch), before the `// \`handoff\` / \`relay\` events:` comment, add:

```ts
          if (event.type === 'approval.request') {
            // Lead / self-relay approval requests (lead-team spec §6.2): snapshot on
            // subscribe, opened, closed. `session` is empty; the value carries the host id.
            handleApprovalEvent(hostId, event.value)
            return
          }
```

`spa/src/locales/en.json` — replace line 1896-1897:

```json
  "worker.exit.after_transfer_failed": "Resumed in the terminal, but the worker could not exit; exit it manually from the list.",
  "approval.kind.lead": "lead request",
  "approval.kind.self_relay": "relay request",
  "approval.decision.approved": "approved",
  "approval.decision.denied": "denied",
  "approval.state.approved": "approved",
  "approval.state.denied": "denied",
  "approval.state.timeout": "timed out",
  "approval.state.cancelled": "cancelled",
  "approval.state.abandoned": "abandoned",
  "approval.toast.decided_elsewhere": "{{host}}: {{client}} {{decision}} the {{kind}} from {{session}}",
  "approval.toast.ended": "{{host}}: the {{kind}} from {{session}} ended ({{state}})"
}
```

`spa/src/locales/zh-TW.json` — replace line 1896-1897:

```json
  "worker.exit.after_transfer_failed": "已在終端機接續，但 worker 未能退出，請到清單手動退出",
  "approval.kind.lead": "lead 申請",
  "approval.kind.self_relay": "接力申請",
  "approval.decision.approved": "核准",
  "approval.decision.denied": "拒絕",
  "approval.state.approved": "已核准",
  "approval.state.denied": "已拒絕",
  "approval.state.timeout": "逾時",
  "approval.state.cancelled": "已取消",
  "approval.state.abandoned": "已放棄",
  "approval.toast.decided_elsewhere": "{{host}}：{{session}} 的 {{kind}} 已由 {{client}} {{decision}}",
  "approval.toast.ended": "{{host}}：{{session}} 的 {{kind}}已結束（{{state}}）"
}
```

(`approval.toast.decided_elsewhere` with kind `lead 申請` renders exactly the spec's `<主機>：<session> 的 lead 申請 已由 <client> 核准／拒絕`.)

- [ ] **Step 4: Run the tests and verify they pass.**
  - Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/lead-team/spa && npx vitest run src/hooks/useMultiHostEventWs.approval.test.ts src/hooks/useMultiHostEventWs.worker-exited.test.ts src/lib/host-events.test.ts src/locales/locale-completeness.test.ts`
  - Expected: PASS (7 new tests; the neighbouring WS tests and locale completeness still green).

- [ ] **Step 5: Commit.**
  ```bash
  git add spa/src/lib/team/approval-format.ts spa/src/lib/team/approval-ws.ts spa/src/lib/host-events.ts spa/src/hooks/useMultiHostEventWs.ts spa/src/hooks/useMultiHostEventWs.approval.test.ts spa/src/locales/en.json spa/src/locales/zh-TW.json
  git commit -m "feat(spa): route approval.request host events into useApprovalStore; toast a close decided elsewhere

  Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
  ```

### Task 3.4: `ApprovalDialogHost` — one dialog, oldest first, one-click 核准 / 拒絕, disconnected banner, queued click

**Files:**
- Create: `spa/src/lib/team/client-label.ts`
- Create: `spa/src/lib/team/approval-decide.ts`
- Create: `spa/src/components/ApprovalDialogHost.tsx`
- Modify: `spa/src/App.tsx:34` (import after `HandoffDialogHost`), `spa/src/App.tsx:282-283` (mount after `<HandoffDialogHost />`)
- Modify: `spa/src/locales/en.json`, `spa/src/locales/zh-TW.json` (keys appended after `approval.toast.ended`, the last key from Task 3.3)
- Test: `spa/src/components/ApprovalDialogHost.test.tsx`

**Interfaces:**
- Consumes: `useApprovalStore` + `selectCurrent` + `approvalKey` (Task 3.2); `decideApproval` / `ApprovalApiError` (Task 3.1); `toastClosed`, `approvalSessionLabel`, `formatOriginAddress`, `formatCountdown`, `approvalKindLabel` (Task 3.3); `useHostStore` `runtime[hostId].status`; `hostLabel` / `useHostLook`; `window.electronAPI?.localDaemonStatus?.()` (`electron.d.ts:173`).
- Produces:
  ```ts
  // client-label.ts
  export function clientDescriptor(): Promise<Client>            // { kind:'app', label:'Purdex.app @ <hostname>' } or 'Purdex.app'; resolved once
  export function __resetClientDescriptorForTests(): void
  // approval-decide.ts
  export type DecideOutcome = 'closed' | 'decided_elsewhere' | 'queued' | 'failed'
  export function submitDecision(hostId: string, approval: Approval, decision: Decision, grant?: Grant): Promise<DecideOutcome>
  // ApprovalDialogHost.tsx
  export function ApprovalDialogHost(): JSX.Element | null
  ```
  Test ids: `approval-dialog` (backdrop), `approval-panel`, `approval-host`, `approval-session`, `approval-address`, `approval-cwd`, `approval-tmux`, `approval-reason`, `approval-countdown`, `approval-max-members`, `approval-roots`, `approval-max-members-error`, `approval-roots-error`, `approval-disconnected`, `approval-queued`, `approval-more`, `approval-approve`, `approval-deny`.

**Behaviour decided here (from spec §6.3 / §9.4 and the handoff notes):**
- The dialog renders `selectCurrent` (oldest `created_at` across hosts, hidden hosts included); it is keyed by `approvalKey`, so the next request is a fresh mount with the payload's defaults.
- No Escape handler and no backdrop dismiss: a request can only end by a decision or by the daemon closing it. Focus moves onto the panel on open and Tab stays inside (as `ConfirmDialog` does).
- 核准 sends `grant` = the edited max members (integer 1–8) and roots (one per line, trimmed, blanks dropped; at least one). An invalid grant disables 核准 only; 拒絕 never needs the grant.
- While `runtime[hostId].status !== 'connected'`: the banner `daemon 重啟中…`; both buttons are `aria-disabled` (dimmed, same classes as `RestartDaemonButton`'s counting state) but still clickable — a click **queues** the decision (`queueDecision`) and does not send. Once a decision is queued the buttons are really `disabled` (one click each), and the line `已記下「核准／拒絕」，恢復連線後送出` shows. The resend is Task 3.5.
- A send that fails with code `network` (the socket dropped between the status flip and the click) queues the same way.
- A 200 closes the dialog (store `applyClosed`, no toast — it was ours). A 409 with an approval closes it and toasts "已由 <client> …" unless that client label is this app's own (a response lost on the wire and resent). A 404 closes it with the failure toast (the daemon no longer knows the request). Any other error toasts and re-enables the buttons.

- [ ] **Step 1: Write the failing test.**

```tsx
// spa/src/components/ApprovalDialogHost.test.tsx — the one app-level approval dialog (lead-team spec §6.3, §6.5, §9.4):
// it renders the oldest open request from `useApprovalStore`, 核准 / 拒絕 are one click each (U5b), it never dismisses
// on Escape, it queues a click while the host is not connected, and it closes with the "handled by" toast on a close
// from elsewhere or a 409. Only the daemon call is mocked.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, act } from '@testing-library/react'
import { ApprovalDialogHost } from './ApprovalDialogHost'
import { approvalKey, useApprovalStore } from '../stores/useApprovalStore'
import { useHostStore } from '../stores/useHostStore'
import { useI18nStore } from '../stores/useI18nStore'
import { useUndoToast } from '../stores/useUndoToast'
import { ApprovalApiError, decideApproval } from '../lib/team/approval-api'
import { handleApprovalEvent } from '../lib/team/approval-ws'
import { __resetClientDescriptorForTests } from '../lib/team/client-label'
import type { Approval } from '../lib/team/types'

vi.mock('../lib/team/approval-api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../lib/team/approval-api')>()),
  decideApproval: vi.fn(),
}))
const mockedDecide = vi.mocked(decideApproval)

const H = 'h1'
const approval = (over: Partial<Approval> = {}): Approval => ({
  id: 'req-1', kind: 'lead', host_id: 'd1',
  origin: { session_id: 'S1', ref: '_40iueq', name: 'purdex-7c', pid: 4242, proc_start: 'p', cwd: '/w/purdex', tmux: 'purdex:@1.%2' },
  payload: { reason: '要平行跑三個 PR', max_members: 3, roots: ['/w/purdex'] },
  state: 'open', created_at: 1_000, deadline_at: 1_000 + 125_000, lease_until: 31_000,
  ...over,
})
const air26 = { kind: 'app' as const, label: 'Purdex.app @ air26' }

const dialog = () => screen.queryByTestId('approval-dialog')
const open = (a: Approval, hostId = H) => act(() => { useApprovalStore.getState().applyOpened(hostId, a) })
const setStatus = (status: 'connected' | 'reconnecting' | 'disconnected') => act(() => { useHostStore.getState().setRuntime(H, { status }) })

beforeEach(() => {
  useI18nStore.getState().setLocale('zh-TW')
  useApprovalStore.getState().reset()
  useHostStore.setState({
    hosts: {
      [H]: { id: H, name: 'mlab', ip: '1.2.3.4', port: 7860, order: 0 },
      h2: { id: 'h2', name: 'air26', ip: '1.2.3.5', port: 7860, order: 1 },
    },
    hostOrder: [H, 'h2'],
    runtime: { [H]: { status: 'connected' }, h2: { status: 'connected' } },
    activeHostId: H,
  })
  useUndoToast.setState({ toast: null, notice: null })
  mockedDecide.mockReset()
  __resetClientDescriptorForTests()
  Object.defineProperty(window, 'electronAPI', { value: undefined, writable: true, configurable: true })
})
afterEach(() => {
  vi.useRealTimers()
  useHostStore.getState().reset()
})

describe('ApprovalDialogHost', () => {
  it('renders nothing until a request is open', () => {
    render(<ApprovalDialogHost />)
    expect(dialog()).toBeNull()
  })

  it('shows host, session, address, cwd, tmux and reason; the countdown ticks toward deadline_at', () => {
    vi.useFakeTimers({ now: 1_000 })
    render(<ApprovalDialogHost />)
    open(approval())
    expect(dialog()).toBeInTheDocument()
    expect(screen.getByTestId('approval-host').textContent).toBe('mlab')
    expect(screen.getByTestId('approval-session').textContent).toBe('purdex-7c')
    expect(screen.getByTestId('approval-address').textContent).toBe('mlab/purdex-7c [40iueq]')
    expect(screen.getByTestId('approval-cwd').textContent).toBe('/w/purdex')
    expect(screen.getByTestId('approval-tmux').textContent).toBe('purdex:@1.%2')
    expect(screen.getByTestId('approval-reason').textContent).toBe('要平行跑三個 PR')
    expect(screen.getByTestId('approval-countdown').textContent).toBe('2:05')
    act(() => { vi.advanceTimersByTime(60_000) })
    expect(screen.getByTestId('approval-countdown').textContent).toBe('1:05')
    act(() => { vi.advanceTimersByTime(120_000) })
    expect(screen.getByTestId('approval-countdown').textContent).toBe('0:00')
    expect(screen.getByTestId('approval-approve')).not.toBeDisabled()
  })

  it('a session without a routable name shows the ref as address; a title (when the wire carries one) wins as the session label', () => {
    render(<ApprovalDialogHost />)
    open(approval({ origin: { ...approval().origin, name: '', title: '修 #1450 的側欄' } }))
    expect(screen.getByTestId('approval-session').textContent).toBe('修 #1450 的側欄')
    expect(screen.getByTestId('approval-address').textContent).toBe('mlab/_40iueq')
    expect(screen.getByTestId('approval-tmux').textContent).toBe('purdex:@1.%2')
  })

  it('核准 is one click: POSTs approve with the payload\'s grant and the app client, then closes without a toast', async () => {
    mockedDecide.mockResolvedValueOnce(approval({ state: 'approved', decided_by: { kind: 'app', label: 'Purdex.app' }, grant: { max_members: 3, roots: ['/w/purdex'] } }))
    render(<ApprovalDialogHost />)
    open(approval())
    await act(async () => { fireEvent.click(screen.getByTestId('approval-approve')) })
    expect(mockedDecide).toHaveBeenCalledTimes(1)
    expect(mockedDecide.mock.calls[0]).toEqual([H, 'req-1', {
      decision: 'approve',
      grant: { max_members: 3, roots: ['/w/purdex'] },
      client: { kind: 'app', label: 'Purdex.app' },
    }])
    expect(dialog()).toBeNull()
    expect(useApprovalStore.getState().entries).toEqual({})
    expect(useUndoToast.getState().toast).toBeNull()
  })

  it('the grant is editable: max members and roots (one per line) go out as typed', async () => {
    mockedDecide.mockResolvedValueOnce(approval({ state: 'approved' }))
    render(<ApprovalDialogHost />)
    open(approval())
    fireEvent.change(screen.getByTestId('approval-max-members'), { target: { value: '5' } })
    fireEvent.change(screen.getByTestId('approval-roots'), { target: { value: '/w/purdex\n\n  /w/ploom  \n' } })
    await act(async () => { fireEvent.click(screen.getByTestId('approval-approve')) })
    expect(mockedDecide.mock.calls[0][2]).toMatchObject({ decision: 'approve', grant: { max_members: 5, roots: ['/w/purdex', '/w/ploom'] } })
  })

  it('an invalid grant disables 核准 only (0, 9, 2.5, blank roots); 拒絕 stays live', () => {
    render(<ApprovalDialogHost />)
    open(approval())
    for (const bad of ['0', '9', '2.5', '']) {
      fireEvent.change(screen.getByTestId('approval-max-members'), { target: { value: bad } })
      expect(screen.getByTestId('approval-approve')).toBeDisabled()
      expect(screen.getByTestId('approval-max-members-error')).toBeInTheDocument()
      expect(screen.getByTestId('approval-deny')).not.toBeDisabled()
    }
    fireEvent.change(screen.getByTestId('approval-max-members'), { target: { value: '8' } })
    expect(screen.getByTestId('approval-approve')).not.toBeDisabled()
    fireEvent.change(screen.getByTestId('approval-roots'), { target: { value: ' \n' } })
    expect(screen.getByTestId('approval-approve')).toBeDisabled()
    expect(screen.getByTestId('approval-roots-error')).toBeInTheDocument()
    expect(mockedDecide).not.toHaveBeenCalled()
  })

  it('拒絕 is one click: POSTs deny with no grant, then closes', async () => {
    mockedDecide.mockResolvedValueOnce(approval({ state: 'denied' }))
    render(<ApprovalDialogHost />)
    open(approval())
    await act(async () => { fireEvent.click(screen.getByTestId('approval-deny')) })
    expect(mockedDecide.mock.calls[0][2]).toEqual({ decision: 'deny', client: { kind: 'app', label: 'Purdex.app' } })
    expect(dialog()).toBeNull()
  })

  it('a double click sends once', async () => {
    let resolve!: (a: Approval) => void
    mockedDecide.mockReturnValueOnce(new Promise<Approval>((r) => { resolve = r }))
    render(<ApprovalDialogHost />)
    open(approval())
    await act(async () => {
      fireEvent.click(screen.getByTestId('approval-deny'))
      fireEvent.click(screen.getByTestId('approval-deny'))
    })
    expect(mockedDecide).toHaveBeenCalledTimes(1)
    await act(async () => { resolve(approval({ state: 'denied' })) })
    expect(dialog()).toBeNull()
  })

  it('several requests queue, oldest first, across hosts; the next one shows after the first closes', async () => {
    mockedDecide.mockResolvedValueOnce(approval({ id: 'old', state: 'denied' }))
    render(<ApprovalDialogHost />)
    open(approval({ id: 'newer', created_at: 5_000 }))
    open(approval({ id: 'old', created_at: 2_000, origin: { ...approval().origin, name: 'nexen-c1' } }), 'h2')
    expect(screen.getByTestId('approval-session').textContent).toBe('nexen-c1')
    expect(screen.getByTestId('approval-host').textContent).toBe('air26')
    expect(screen.getByTestId('approval-more').textContent).toBe('還有 1 個申請排隊中')
    await act(async () => { fireEvent.click(screen.getByTestId('approval-deny')) })
    expect(mockedDecide.mock.calls[0].slice(0, 2)).toEqual(['h2', 'old'])
    expect(screen.getByTestId('approval-session').textContent).toBe('purdex-7c')
    expect(screen.queryByTestId('approval-more')).toBeNull()
  })

  it('Escape and a backdrop click do not dismiss it', () => {
    render(<ApprovalDialogHost />)
    open(approval())
    fireEvent.keyDown(document, { key: 'Escape' })
    expect(dialog()).toBeInTheDocument()
    fireEvent.click(screen.getByTestId('approval-dialog'))
    expect(dialog()).toBeInTheDocument()
    expect(mockedDecide).not.toHaveBeenCalled()
  })

  it('takes focus onto the panel when it opens', () => {
    render(<ApprovalDialogHost />)
    open(approval())
    expect(document.activeElement).toBe(screen.getByTestId('approval-panel'))
  })

  describe('while the host is not connected (spec §9.4)', () => {
    it('shows `daemon 重啟中…`, dims the buttons, and a click is queued instead of sent', () => {
      render(<ApprovalDialogHost />)
      open(approval())
      setStatus('reconnecting')
      expect(screen.getByTestId('approval-disconnected').textContent).toBe('daemon 重啟中…')
      expect(screen.getByTestId('approval-deny').getAttribute('aria-disabled')).toBe('true')
      expect(screen.getByTestId('approval-approve').getAttribute('aria-disabled')).toBe('true')
      fireEvent.click(screen.getByTestId('approval-deny'))
      expect(mockedDecide).not.toHaveBeenCalled()
      expect(useApprovalStore.getState().queued[approvalKey(H, 'req-1')]).toMatchObject({ hostId: H, decision: 'deny', approval: { id: 'req-1' } })
      expect(dialog()).toBeInTheDocument()
      expect(screen.getByTestId('approval-queued').textContent).toBe('已記下「拒絕」，恢復連線後送出')
      expect(screen.getByTestId('approval-deny')).toBeDisabled()
      expect(screen.getByTestId('approval-approve')).toBeDisabled()
    })

    it('a queued approve carries the edited grant', () => {
      render(<ApprovalDialogHost />)
      open(approval())
      fireEvent.change(screen.getByTestId('approval-max-members'), { target: { value: '2' } })
      setStatus('disconnected')
      fireEvent.click(screen.getByTestId('approval-approve'))
      expect(useApprovalStore.getState().queued[approvalKey(H, 'req-1')]).toMatchObject({ decision: 'approve', grant: { max_members: 2, roots: ['/w/purdex'] } })
    })

    it('the banner goes away when the host is connected again; the queued decision stays locked in', () => {
      render(<ApprovalDialogHost />)
      open(approval())
      setStatus('reconnecting')
      fireEvent.click(screen.getByTestId('approval-deny'))
      setStatus('connected')
      expect(screen.queryByTestId('approval-disconnected')).toBeNull()
      expect(screen.getByTestId('approval-queued')).toBeInTheDocument()
      expect(screen.getByTestId('approval-deny')).toBeDisabled()
    })

    it('a send that fails on the network (the socket dropped mid-click) is queued the same way', async () => {
      mockedDecide.mockRejectedValueOnce(new ApprovalApiError(0, 'network', 'Failed to fetch'))
      render(<ApprovalDialogHost />)
      open(approval())
      await act(async () => { fireEvent.click(screen.getByTestId('approval-approve')) })
      expect(dialog()).toBeInTheDocument()
      expect(useApprovalStore.getState().queued[approvalKey(H, 'req-1')]).toMatchObject({ decision: 'approve' })
      expect(screen.getByTestId('approval-queued')).toBeInTheDocument()
      expect(useApprovalStore.getState().decidedHere).toEqual({})
    })
  })

  describe('closed elsewhere (U6)', () => {
    it('a `closed` event from another client closes it with the toast', () => {
      render(<ApprovalDialogHost />)
      open(approval())
      act(() => {
        handleApprovalEvent(H, JSON.stringify({ op: 'closed', approval: approval({ state: 'approved', decided_by: air26, decided_at: 9_000 }) }))
      })
      expect(dialog()).toBeNull()
      expect(useUndoToast.getState().toast?.message).toBe('mlab：purdex-7c 的 lead 申請 已由 Purdex.app @ air26 核准')
    })

    it('a 409 already_decided closes it with the same toast', async () => {
      mockedDecide.mockRejectedValueOnce(new ApprovalApiError(409, 'already_decided', '', approval({ state: 'denied', decided_by: air26 })))
      render(<ApprovalDialogHost />)
      open(approval())
      await act(async () => { fireEvent.click(screen.getByTestId('approval-approve')) })
      expect(dialog()).toBeNull()
      expect(useApprovalStore.getState().entries).toEqual({})
      expect(useUndoToast.getState().toast?.message).toBe('mlab：purdex-7c 的 lead 申請 已由 Purdex.app @ air26 拒絕')
    })

    it('a 409 whose decided_by is this very app (the first answer was lost) closes it silently', async () => {
      mockedDecide.mockRejectedValueOnce(new ApprovalApiError(409, 'already_decided', '', approval({ state: 'approved', decided_by: { kind: 'app', label: 'Purdex.app' } })))
      render(<ApprovalDialogHost />)
      open(approval())
      await act(async () => { fireEvent.click(screen.getByTestId('approval-approve')) })
      expect(dialog()).toBeNull()
      expect(useUndoToast.getState().toast).toBeNull()
    })
  })

  it('a 404 closes it with the failure toast; another error toasts and re-enables the buttons', async () => {
    mockedDecide.mockRejectedValueOnce(new ApprovalApiError(400, 'bad_request', 'roots must be absolute'))
    render(<ApprovalDialogHost />)
    open(approval())
    await act(async () => { fireEvent.click(screen.getByTestId('approval-approve')) })
    expect(dialog()).toBeInTheDocument()
    expect(screen.getByTestId('approval-approve')).not.toBeDisabled()
    expect(useUndoToast.getState().toast?.message).toBe('送出決定失敗（bad_request）')
    expect(useApprovalStore.getState().decidedHere).toEqual({})
    mockedDecide.mockRejectedValueOnce(new ApprovalApiError(404, 'not_found'))
    await act(async () => { fireEvent.click(screen.getByTestId('approval-deny')) })
    expect(dialog()).toBeNull()
    expect(useUndoToast.getState().toast?.message).toBe('送出決定失敗（not_found）')
  })

  it('the client label is `Purdex.app @ <hostname>` from localDaemonStatus, read once', async () => {
    const localDaemonStatus = vi.fn(async () => ({ hostname: 'mlab' }))
    Object.defineProperty(window, 'electronAPI', { value: { localDaemonStatus }, writable: true, configurable: true })
    mockedDecide.mockResolvedValue(approval({ state: 'denied' }))
    render(<ApprovalDialogHost />)
    open(approval({ id: 'a' }))
    await act(async () => { fireEvent.click(screen.getByTestId('approval-deny')) })
    open(approval({ id: 'b', created_at: 2_000 }))
    await act(async () => { fireEvent.click(screen.getByTestId('approval-deny')) })
    expect(mockedDecide.mock.calls[0][2]).toMatchObject({ client: { kind: 'app', label: 'Purdex.app @ mlab' } })
    expect(mockedDecide.mock.calls[1][2]).toMatchObject({ client: { kind: 'app', label: 'Purdex.app @ mlab' } })
    expect(localDaemonStatus).toHaveBeenCalledTimes(1)
  })
})
```

- [ ] **Step 2: Run the test and verify it fails.**
  - Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/lead-team/spa && npx vitest run src/components/ApprovalDialogHost.test.tsx`
  - Expected: FAIL — `Failed to resolve import "./ApprovalDialogHost"`.

- [ ] **Step 3: Implement.**

`spa/src/lib/team/client-label.ts`:

```ts
// spa/src/lib/team/client-label.ts — the audit label this app signs its decisions with (lead-team spec §6.5):
// `Purdex.app @ <hostname>`, the hostname from Electron's local-daemon status (device-name.ts reads the same
// field the same way), else `Purdex.app`. Resolved once per renderer: the daemon adds the remote address.
// U14: there is no browser label — the App is the only client.
import type { Client } from './types'

const HOSTNAME_TIMEOUT_MS = 1500
const APP = 'Purdex.app'

let cached: Promise<Client> | null = null

async function resolve(): Promise<Client> {
  const status = window.electronAPI?.localDaemonStatus
  if (!status) return { kind: 'app', label: APP }
  let timer: ReturnType<typeof setTimeout> | undefined
  const timeout = new Promise<null>((done) => {
    timer = setTimeout(() => done(null), HOSTNAME_TIMEOUT_MS)
  })
  try {
    const result = await Promise.race([Promise.resolve().then(() => status()), timeout])
    const hostname: unknown = result?.hostname
    const name = typeof hostname === 'string' ? hostname.trim() : ''
    return { kind: 'app', label: name !== '' ? `${APP} @ ${name}` : APP }
  } catch {
    return { kind: 'app', label: APP }
  } finally {
    clearTimeout(timer)
  }
}

/** This app's `Client` descriptor, resolved on first use and then reused. */
export function clientDescriptor(): Promise<Client> {
  if (cached === null) cached = resolve()
  return cached
}

export function __resetClientDescriptorForTests(): void {
  cached = null
}
```

`spa/src/lib/team/approval-decide.ts`:

```ts
// spa/src/lib/team/approval-decide.ts — one decision, sent once (lead-team spec §6.3, §6.5, §9.4). Shared by the
// dialog's click and by the reconnect resend, so both agree on what each answer means:
//   200                      → closed (ours: no toast);
//   409 + approval           → someone else got there first: close, toast who (unless it was this app's own lost answer);
//   network (status 0)       → the daemon is restarting: keep the decision for the reconnect snapshot;
//   404                      → the daemon no longer knows the request: close, toast the failure;
//   anything else            → toast the failure, leave the request open.
import { useApprovalStore, type Decision } from '../../stores/useApprovalStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { useUndoToast } from '../../stores/useUndoToast'
import { ApprovalApiError, decideApproval } from './approval-api'
import { toastClosed } from './approval-ws'
import { clientDescriptor } from './client-label'
import type { Approval, Grant } from './types'

export type DecideOutcome = 'closed' | 'decided_elsewhere' | 'queued' | 'failed'

export async function submitDecision(hostId: string, approval: Approval, decision: Decision, grant?: Grant): Promise<DecideOutcome> {
  const client = await clientDescriptor()
  useApprovalStore.getState().markDecidedHere(hostId, approval.id)
  try {
    const closed = await decideApproval(hostId, approval.id, {
      decision,
      ...(decision === 'approve' && grant ? { grant } : {}),
      client,
    })
    useApprovalStore.getState().applyClosed(hostId, closed)
    return 'closed'
  } catch (e: unknown) {
    const err = e instanceof ApprovalApiError ? e : new ApprovalApiError(0, 'unknown', e instanceof Error ? e.message : String(e))
    const store = useApprovalStore.getState()
    if (err.code === 'network') {
      // Never reached the daemon (or the answer was lost): a `closed` that arrives meanwhile is someone else's.
      store.unmarkDecidedHere(hostId, approval.id)
      store.queueDecision(hostId, approval, decision, grant)
      return 'queued'
    }
    if (err.status === 409 && err.approval) {
      store.applyClosed(hostId, err.approval)
      if (err.approval.decided_by?.label !== client.label) toastClosed(hostId, err.approval)
      return 'decided_elsewhere'
    }
    if (err.status === 404) {
      store.applyClosed(hostId, approval)
    } else {
      store.unmarkDecidedHere(hostId, approval.id)
    }
    const t = useI18nStore.getState().t
    useUndoToast.getState().show(t('approval.toast.failed', { code: err.code }))
    return 'failed'
  }
}
```

`spa/src/components/ApprovalDialogHost.tsx`:

```tsx
// spa/src/components/ApprovalDialogHost.tsx — the one approval dialog (lead-team spec §6.3), mounted once with the
// app-level overlays, next to HandoffDialogHost. It renders the oldest open request across hosts from
// `useApprovalStore`, which the `approval.request` WS branch and the daemon's snapshot feed; several requests queue,
// one dialog at a time. The body here is the `lead` kind (self relay arrives with P5).
//
// It cannot be dismissed: no Escape, no backdrop click. A request ends by a decision — 核准 / 拒絕, one click each on any
// Purdex.app (U5b) — or by the daemon closing it (decided elsewhere, timeout, cancel), which removes it from the store
// and unmounts this. While the host is not connected (spec §9.4) the buttons dim under `daemon 重啟中…`; a click then is
// queued in the store and sent when the reconnect snapshot re-adds the request (lib/team/approval-ws.ts).
//
// Focus: the panel takes focus on open and Tab stays inside, as ConfirmDialog does, so a stray keystroke never
// reaches the pane behind. The i18n strings are the spec's (§6.3).
import { useEffect, useRef, useState } from 'react'
import { ArrowsClockwise } from '@phosphor-icons/react'
import { useI18nStore } from '../stores/useI18nStore'
import { useHostStore } from '../stores/useHostStore'
import { approvalKey, selectCurrent, selectOpenCount, useApprovalStore, type ApprovalEntry, type Decision } from '../stores/useApprovalStore'
import { hostLabel, useHostLook } from '../lib/host-look'
import { leadPayloadOf, MAX_MAX_MEMBERS, type Grant } from '../lib/team/types'
import { approvalSessionLabel, formatCountdown, formatOriginAddress } from '../lib/team/approval-format'
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
  const payload = leadPayloadOf(approval)
  const [maxMembers, setMaxMembers] = useState(String(payload.max_members))
  const [rootsText, setRootsText] = useState(payload.roots.join('\n'))
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

  // Tab stays inside the panel (ConfirmDialog's rule). No Escape handler, on purpose: nothing dismisses this dialog.
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
  const grantOk = membersOk && rootsOk
  const locked = busy || queued !== undefined
  const session = approvalSessionLabel(approval.origin)

  const decide = async (decision: Decision) => {
    if (inFlight.current || locked) return
    if (decision === 'approve' && !grantOk) return
    const grant: Grant | undefined = decision === 'approve' ? { max_members: members, roots } : undefined
    if (!connected) {
      // Spec §9.4: kept locally, sent on reconnect (the snapshot re-adds the request, or shows it gone).
      useApprovalStore.getState().queueDecision(hostId, approval, decision, grant)
      return
    }
    inFlight.current = true
    setBusy(true)
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
          <h3 id={titleId} className="text-sm font-medium text-text-primary">{t('approval.dialog.title_lead', { host: hostName, session })}</h3>
          <dl className="mt-2 grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 text-xs">
            <dt className="text-text-muted">{t('approval.dialog.host')}</dt>
            <dd data-testid="approval-host" className="text-text-primary">{hostName}</dd>
            <dt className="text-text-muted">{t('approval.dialog.session')}</dt>
            <dd data-testid="approval-session" className="text-text-primary">{session}</dd>
            <dt className="text-text-muted">{t('approval.dialog.address')}</dt>
            <dd data-testid="approval-address" className="font-mono text-text-primary">{formatOriginAddress(hostName, approval.origin)}</dd>
            <dt className="text-text-muted">{t('approval.dialog.cwd')}</dt>
            <dd data-testid="approval-cwd" className="font-mono break-all text-text-primary">{approval.origin.cwd}</dd>
            <dt className="text-text-muted">{t('approval.dialog.tmux')}</dt>
            <dd data-testid="approval-tmux" className="font-mono text-text-primary">{approval.origin.tmux !== '' ? approval.origin.tmux : '—'}</dd>
            <dt className="text-text-muted">{t('approval.dialog.reason')}</dt>
            <dd data-testid="approval-reason" className="whitespace-pre-wrap text-text-primary">{payload.reason}</dd>
            <dt className="text-text-muted">{t('approval.dialog.deadline')}</dt>
            <dd data-testid="approval-countdown" className="font-mono text-text-primary">{formatCountdown(approval.deadline_at - now)}</dd>
          </dl>
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
          {!connected && (
            <p data-testid="approval-disconnected" className="mt-2 text-xs text-amber-400">{t('approval.dialog.daemon_restarting')}</p>
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

`spa/src/App.tsx` — after line 34 (`import { HandoffDialogHost } from './components/HandoffDialogHost'`) add:

```tsx
import { ApprovalDialogHost } from './components/ApprovalDialogHost'
```

and after line 283 (`        <HandoffDialogHost />`) add:

```tsx
        {/* The one approval dialog (lead-team spec §6.3): fed by the approval.request WS branch and the daemon's snapshot. */}
        <ApprovalDialogHost />
```

`spa/src/locales/en.json` — after `"approval.toast.ended": …` (the last key) add:

```json
  "approval.toast.failed": "Could not send the decision ({{code}})",
  "approval.dialog.title_lead": "{{host}}: {{session}} requests to become lead",
  "approval.dialog.host": "Host",
  "approval.dialog.session": "Session",
  "approval.dialog.address": "Address",
  "approval.dialog.cwd": "Directory",
  "approval.dialog.tmux": "tmux",
  "approval.dialog.reason": "Reason",
  "approval.dialog.deadline": "Time left",
  "approval.dialog.max_members": "Max members (1–8)",
  "approval.dialog.max_members_range": "Enter a whole number from 1 to {{max}}",
  "approval.dialog.roots": "Allowed roots (one per line)",
  "approval.dialog.roots_required": "At least one root is required",
  "approval.dialog.daemon_restarting": "daemon restarting…",
  "approval.dialog.queued": "“{{decision}}” noted; it is sent once the connection is back",
  "approval.dialog.more_pending": "{{count}} more request(s) waiting",
  "approval.dialog.approve": "Approve",
  "approval.dialog.deny": "Deny"
```

`spa/src/locales/zh-TW.json` — after `"approval.toast.ended": …` add:

```json
  "approval.toast.failed": "送出決定失敗（{{code}}）",
  "approval.dialog.title_lead": "{{host}}：{{session}} 申請成為 lead",
  "approval.dialog.host": "主機",
  "approval.dialog.session": "Session",
  "approval.dialog.address": "位址",
  "approval.dialog.cwd": "目錄",
  "approval.dialog.tmux": "tmux",
  "approval.dialog.reason": "理由",
  "approval.dialog.deadline": "剩餘時間",
  "approval.dialog.max_members": "member 上限（1–8）",
  "approval.dialog.max_members_range": "請輸入 1 到 {{max}} 的整數",
  "approval.dialog.roots": "允許的目錄（一行一個）",
  "approval.dialog.roots_required": "至少要有一個目錄",
  "approval.dialog.daemon_restarting": "daemon 重啟中…",
  "approval.dialog.queued": "已記下「{{decision}}」，恢復連線後送出",
  "approval.dialog.more_pending": "還有 {{count}} 個申請排隊中",
  "approval.dialog.approve": "核准",
  "approval.dialog.deny": "拒絕"
```

(Keep the previous last line's trailing comma and the file's closing `}`; `locale-completeness.test.ts` fails on a key present in one file only.)

- [ ] **Step 4: Run the tests and verify they pass.**
  - Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/lead-team/spa && npx vitest run src/components/ApprovalDialogHost.test.tsx src/components/HandoffDialogHost.test.tsx src/locales/locale-completeness.test.ts && pnpm run lint && pnpm run build`
  - Expected: PASS, 22 new tests; lint clean (no non-component export in the `.tsx`); `tsc -b` clean.

- [ ] **Step 5: Commit.**
  ```bash
  git add spa/src/lib/team/client-label.ts spa/src/lib/team/approval-decide.ts spa/src/components/ApprovalDialogHost.tsx spa/src/components/ApprovalDialogHost.test.tsx spa/src/App.tsx spa/src/locales/en.json spa/src/locales/zh-TW.json
  git commit -m "feat(spa): ApprovalDialogHost — one-click 核准/拒絕 for lead requests, queued while the daemon restarts

  Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
  ```

**P3a ends here: open the PR, run R1 / R2 per the project CLAUDE.md, merge, then start P3b from main.**

### Task 3.5: Reconnect — the snapshot re-sends a queued decision once, or says the request ended while away

**Files:**
- Modify: `spa/src/lib/team/approval-ws.ts` (the `snapshot` branch of `handleApprovalEvent`; imports)
- Modify: `spa/src/locales/en.json`, `spa/src/locales/zh-TW.json` (one key after `approval.dialog.deny`)
- Test: `spa/src/lib/team/approval-ws.test.ts`

**Interfaces:**
- Consumes: `useApprovalStore.takeQueued` / `applySnapshot` (Task 3.2), `submitDecision` (Task 3.4).
- Produces: `handleApprovalEvent` unchanged in signature; new exported helper for tests and reuse:
  ```ts
  export function toastEndedWhileAway(hostId: string, approval: Approval): void
  ```

**Why the snapshot is the trigger.** The daemon sends `{op:"snapshot"}` to each new subscriber (`OnSubscribe`, PD2), and the SPA opens a new socket exactly when a host returns to `connected` (`useMultiHostEventWs.ts:150-163`). So "the host is connected again AND the daemon's current open set is known" is one event, and the queue is consumed there: `takeQueued` empties it, so a later snapshot (the next restart) sends nothing twice. A resend that fails on the network again re-queues itself inside `submitDecision`, for the snapshot after that.

- [ ] **Step 1: Write the failing test.**

```ts
// spa/src/lib/team/approval-ws.test.ts — the reconnect snapshot and the decisions queued while the host was away
// (lead-team spec §6.3 "During a daemon restart", §9.4): the snapshot re-adds the request → the queued decision
// is sent, once; the snapshot shows it gone → toast `approval.toast.ended_while_away`; a resend that fails on the
// network stays queued for the next snapshot.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { useApprovalStore, approvalKey } from '../../stores/useApprovalStore'
import { useHostStore } from '../../stores/useHostStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { useUndoToast } from '../../stores/useUndoToast'
import { ApprovalApiError, decideApproval } from './approval-api'
import { __resetClientDescriptorForTests } from './client-label'
import { handleApprovalEvent } from './approval-ws'
import type { Approval } from './types'

vi.mock('./approval-api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('./approval-api')>()),
  decideApproval: vi.fn(),
}))
const mockedDecide = vi.mocked(decideApproval)

const H = 'h1'
const approval = (over: Partial<Approval> = {}): Approval => ({
  id: 'req-1', kind: 'lead', host_id: 'd1',
  origin: { session_id: 'S1', ref: '_40iueq', name: 'purdex-7c', pid: 1, proc_start: 'p', cwd: '/w/purdex', tmux: '' },
  payload: { reason: 'r', max_members: 3, roots: ['/w/purdex'] },
  state: 'open', created_at: 1_000, deadline_at: 541_000, lease_until: 31_000,
  ...over,
})
const snapshot = (approvals: Approval[]) => JSON.stringify({ op: 'snapshot', approvals })
const flush = () => new Promise<void>((r) => setTimeout(r, 0))

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
  mockedDecide.mockReset()
  __resetClientDescriptorForTests()
  Object.defineProperty(window, 'electronAPI', { value: undefined, writable: true, configurable: true })
})
afterEach(() => useHostStore.getState().reset())

describe('approval-ws reconnect (spec §9.4)', () => {
  it('the snapshot re-adds the request → the queued decision is sent once, with its grant; a second snapshot sends nothing', async () => {
    const a = approval()
    useApprovalStore.getState().applyOpened(H, a)
    useApprovalStore.getState().queueDecision(H, a, 'approve', { max_members: 2, roots: ['/w/purdex'] })
    mockedDecide.mockResolvedValueOnce(approval({ state: 'approved' }))
    handleApprovalEvent(H, snapshot([a]))
    await flush()
    expect(mockedDecide).toHaveBeenCalledTimes(1)
    expect(mockedDecide.mock.calls[0]).toEqual([H, 'req-1', { decision: 'approve', grant: { max_members: 2, roots: ['/w/purdex'] }, client: { kind: 'app', label: 'Purdex.app' } }])
    expect(useApprovalStore.getState().entries).toEqual({})
    expect(useApprovalStore.getState().queued).toEqual({})
    expect(useUndoToast.getState().toast).toBeNull()
    handleApprovalEvent(H, snapshot([]))
    await flush()
    expect(mockedDecide).toHaveBeenCalledTimes(1)
  })

  it('the snapshot shows the request gone → toast ended_while_away, nothing sent, the queue is empty', async () => {
    const a = approval()
    useApprovalStore.getState().applyOpened(H, a)
    useApprovalStore.getState().queueDecision(H, a, 'deny')
    handleApprovalEvent(H, snapshot([approval({ id: 'other', created_at: 2_000 })]))
    await flush()
    expect(mockedDecide).not.toHaveBeenCalled()
    expect(useUndoToast.getState().toast?.message).toBe('mlab：purdex-7c 的 lead 申請已在離線期間結束，你的決定未送出')
    expect(useApprovalStore.getState().queued).toEqual({})
    expect(Object.keys(useApprovalStore.getState().entries)).toEqual([approvalKey(H, 'other')])
  })

  it('the resend answers 409 already_decided → closed with the "handled by" toast', async () => {
    const a = approval()
    useApprovalStore.getState().applyOpened(H, a)
    useApprovalStore.getState().queueDecision(H, a, 'deny')
    mockedDecide.mockRejectedValueOnce(new ApprovalApiError(409, 'already_decided', '', approval({ state: 'approved', decided_by: { kind: 'app', label: 'Purdex.app @ air26' } })))
    handleApprovalEvent(H, snapshot([a]))
    await flush()
    expect(useApprovalStore.getState().entries).toEqual({})
    expect(useUndoToast.getState().toast?.message).toBe('mlab：purdex-7c 的 lead 申請 已由 Purdex.app @ air26 核准')
  })

  it('the resend fails on the network again → it stays queued for the next snapshot', async () => {
    const a = approval()
    useApprovalStore.getState().applyOpened(H, a)
    useApprovalStore.getState().queueDecision(H, a, 'deny')
    mockedDecide.mockRejectedValueOnce(new ApprovalApiError(0, 'network', 'ECONNREFUSED'))
    handleApprovalEvent(H, snapshot([a]))
    await flush()
    expect(useApprovalStore.getState().queued[approvalKey(H, 'req-1')]).toMatchObject({ decision: 'deny' })
    mockedDecide.mockResolvedValueOnce(approval({ state: 'denied' }))
    handleApprovalEvent(H, snapshot([a]))
    await flush()
    expect(mockedDecide).toHaveBeenCalledTimes(2)
    expect(useApprovalStore.getState().queued).toEqual({})
  })

  it('another host\'s queue is left alone', async () => {
    const a = approval()
    useApprovalStore.getState().applyOpened('h2', a)
    useApprovalStore.getState().queueDecision('h2', a, 'deny')
    handleApprovalEvent(H, snapshot([]))
    await flush()
    expect(mockedDecide).not.toHaveBeenCalled()
    expect(useApprovalStore.getState().queued[approvalKey('h2', 'req-1')]).toBeDefined()
  })
})
```

- [ ] **Step 2: Run the test and verify it fails.**
  - Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/lead-team/spa && npx vitest run src/lib/team/approval-ws.test.ts`
  - Expected: FAIL — first test: `expect(mockedDecide).toHaveBeenCalledTimes(1)` receives 0 (the snapshot only replaces the set).

- [ ] **Step 3: Implement.**

In `spa/src/lib/team/approval-ws.ts`, extend the imports:

```ts
import { approvalKindLabel, approvalSessionLabel, closedToastText } from './approval-format'
import { submitDecision } from './approval-decide'
```

add after `toastClosed`:

```ts
/** The request a decision was queued for is gone from the reconnect snapshot (spec §9.4): say so, send nothing. */
export function toastEndedWhileAway(hostId: string, approval: Approval): void {
  const t = useI18nStore.getState().t
  useUndoToast.getState().show(t('approval.toast.ended_while_away', {
    host: hostLabel(hostId, hostLookOf(hostId)),
    session: approvalSessionLabel(approval.origin),
    kind: approvalKindLabel(t, approval.kind),
  }))
}
```

and replace the `snapshot` branch of `handleApprovalEvent`:

```ts
  if (ev.op === 'snapshot') {
    // A new connection (the daemon came back): the queue was filled while it was gone. Take it BEFORE the
    // snapshot replaces the set, so each queued decision is sent at most once per reconnect.
    const queued = store.takeQueued(hostId)
    const vanished = new Set(store.applySnapshot(hostId, ev.approvals))
    for (const q of queued) {
      if (vanished.has(q.approval.id)) toastEndedWhileAway(hostId, q.approval)
      else void submitDecision(hostId, q.approval, q.decision, q.grant)
    }
    return
  }
```

(`approval-decide.ts` imports `toastClosed` from this file and this file now imports `submitDecision` from it: both are function-level uses, resolved at call time, so the cycle is harmless under ESM; the lint has no `import/no-cycle` rule.)

`spa/src/locales/en.json` — after `"approval.dialog.deny": "Deny"` add (with the comma on the previous line):

```json
  "approval.toast.ended_while_away": "{{host}}: the {{kind}} from {{session}} ended while this app was disconnected; your decision was not sent"
```

`spa/src/locales/zh-TW.json`:

```json
  "approval.toast.ended_while_away": "{{host}}：{{session}} 的 {{kind}}已在離線期間結束，你的決定未送出"
```

- [ ] **Step 4: Run the tests and verify they pass.**
  - Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/lead-team/spa && npx vitest run src/lib/team/approval-ws.test.ts src/hooks/useMultiHostEventWs.approval.test.ts src/components/ApprovalDialogHost.test.tsx src/locales/locale-completeness.test.ts`
  - Expected: PASS (5 new).

- [ ] **Step 5: Commit.**
  ```bash
  git add spa/src/lib/team/approval-ws.ts spa/src/lib/team/approval-ws.test.ts spa/src/locales/en.json spa/src/locales/zh-TW.json
  git commit -m "feat(spa): re-send a decision queued during a daemon restart once the snapshot re-adds the request

  Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
  ```

### Task 3.6: Notification on `opened` — `<主機>：<session> 申請成為 lead`; its click only focuses the window

**Files:**
- Create: `spa/src/lib/team/approval-notify.ts`
- Modify: `spa/src/lib/team/approval-ws.ts` (the `opened` branch)
- Modify: `spa/src/hooks/useNotificationDispatcher.ts:113-115` (`NotificationAction` union), `:280-294` (click listener), `:402-409` (new `case` after `open-host`)
- Modify: `spa/src/locales/en.json`, `spa/src/locales/zh-TW.json` (one key after `approval.toast.ended_while_away`)
- Test: `spa/src/lib/team/approval-notify.test.ts`, `spa/src/hooks/useNotificationDispatcher.approval.test.ts`

**Interfaces:**
- Consumes: `window.electronAPI.showNotification` (`electron.d.ts:135`), `getPlatformCapabilities().canNotification` (`platform.ts:14`), `window.electronAPI.onNotificationClicked` / `focusMyWindow` (`electron.d.ts:136-137`).
- Produces:
  ```ts
  // approval-notify.ts
  export function notifyApprovalOpened(hostId: string, approval: Approval): void
  // useNotificationDispatcher.ts
  export type NotificationAction =
    | { kind: 'open-session'; hostId: string; sessionCode: string }
    | { kind: 'open-host'; hostId: string }
    | { kind: 'open-approval'; hostId: string }
  ```
  **U14:** no browser `Notification` fallback for approvals. `broadcastTs` is `approval.created_at` (Electron main dedups across this device's windows on it, `electron/main.ts:163-165`). The body is the reason.

- [ ] **Step 1: Write the failing tests.**

```ts
// spa/src/lib/team/approval-notify.test.ts — the system notification for a new lead request (lead-team spec §6.3):
// raised through the existing Electron `showNotification` path on `opened` only — never from a snapshot, never twice
// for one request — with `action {kind:'open-approval', hostId}` and `broadcastTs = created_at`. U14: no browser path.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { useApprovalStore } from '../../stores/useApprovalStore'
import { useHostStore } from '../../stores/useHostStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { handleApprovalEvent } from './approval-ws'
import { notifyApprovalOpened } from './approval-notify'
import type { Approval } from './types'

const H = 'h1'
const approval = (over: Partial<Approval> = {}): Approval => ({
  id: 'req-1', kind: 'lead', host_id: 'd1',
  origin: { session_id: 'S1', ref: '_40iueq', name: 'purdex-7c', pid: 1, proc_start: 'p', cwd: '/w/purdex', tmux: '' },
  payload: { reason: '要平行跑三個 PR', max_members: 3, roots: ['/w/purdex'] },
  state: 'open', created_at: 1_696_000_000_000, deadline_at: 1_696_000_540_000, lease_until: 0,
  ...over,
})
const showNotification = vi.fn()
const NotificationCtor = vi.fn()

beforeEach(() => {
  useI18nStore.getState().setLocale('zh-TW')
  useApprovalStore.getState().reset()
  useHostStore.setState({
    hosts: { [H]: { id: H, name: 'mlab', ip: '1.2.3.4', port: 7860, order: 0 } },
    hostOrder: [H], runtime: {}, activeHostId: H,
  })
  showNotification.mockClear()
  NotificationCtor.mockClear()
  Object.defineProperty(window, 'electronAPI', { value: { showNotification }, writable: true, configurable: true })
  vi.stubGlobal('Notification', Object.assign(NotificationCtor, { permission: 'granted' }))
})
afterEach(() => {
  vi.unstubAllGlobals()
  Object.defineProperty(window, 'electronAPI', { value: undefined, writable: true, configurable: true })
  useHostStore.getState().reset()
})

describe('notifyApprovalOpened', () => {
  it('raises the Electron notification: title per spec §6.3, the reason as body, open-approval action, created_at as broadcastTs', () => {
    notifyApprovalOpened(H, approval())
    expect(showNotification).toHaveBeenCalledTimes(1)
    expect(showNotification.mock.calls[0][0]).toEqual({
      title: 'mlab：purdex-7c 申請成為 lead',
      body: '要平行跑三個 PR',
      sessionCode: '',
      eventName: 'ApprovalRequest',
      broadcastTs: 1_696_000_000_000,
      action: { kind: 'open-approval', hostId: H },
    })
    expect(NotificationCtor).not.toHaveBeenCalled()
  })

  it('outside Electron it does nothing — no browser Notification fallback (U14)', () => {
    Object.defineProperty(window, 'electronAPI', { value: undefined, writable: true, configurable: true })
    notifyApprovalOpened(H, approval())
    expect(showNotification).not.toHaveBeenCalled()
    expect(NotificationCtor).not.toHaveBeenCalled()
  })
})

describe('handleApprovalEvent → notification', () => {
  it('fires on `opened` once per request; a duplicate opened does not fire again', () => {
    handleApprovalEvent(H, JSON.stringify({ op: 'opened', approval: approval() }))
    handleApprovalEvent(H, JSON.stringify({ op: 'opened', approval: approval() }))
    expect(showNotification).toHaveBeenCalledTimes(1)
  })

  it('never fires from a snapshot (a reconnect must not re-announce what is already on screen)', () => {
    handleApprovalEvent(H, JSON.stringify({ op: 'snapshot', approvals: [approval(), approval({ id: 'b' })] }))
    expect(showNotification).not.toHaveBeenCalled()
  })

  it('does not fire on closed', () => {
    handleApprovalEvent(H, JSON.stringify({ op: 'opened', approval: approval() }))
    handleApprovalEvent(H, JSON.stringify({ op: 'closed', approval: approval({ state: 'timeout' }) }))
    expect(showNotification).toHaveBeenCalledTimes(1)
  })
})
```

```ts
// spa/src/hooks/useNotificationDispatcher.approval.test.ts — the click on an approval notification (lead-team spec
// §6.3): it only focuses the window, where the dialog already is. It must not fall into the open-session path
// (no session code to route to) nor open the Hosts page.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { renderHook } from '@testing-library/react'
import { handleNotificationClick, useNotificationDispatcher } from './useNotificationDispatcher'
import { useTabStore } from '../stores/useTabStore'
import { useHostStore } from '../stores/useHostStore'
import { useShownHostsStore } from '../stores/useShownHostsStore'

type ClickPayload = { sessionCode: string; action?: { kind: string; hostId: string; sessionCode?: string } }

describe('useNotificationDispatcher open-approval', () => {
  let clickHandler: ((payload: ClickPayload) => void) | null
  const focusMyWindow = vi.fn()

  beforeEach(() => {
    clickHandler = null
    focusMyWindow.mockClear()
    useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, visitHistory: [] })
    useHostStore.setState({ hostOrder: ['host-a', 'host-b'], activeHostId: 'host-a' })
    // Both shown: the open-session fallback's "hidden host → Hosts page" branch must not be what makes this pass.
    useShownHostsStore.setState({ ids: ['host-a', 'host-b'] })
    Object.defineProperty(window, 'electronAPI', {
      value: {
        onNotificationClicked: (cb: (payload: ClickPayload) => void) => { clickHandler = cb; return () => { clickHandler = null } },
        focusMyWindow,
      },
      writable: true,
      configurable: true,
    })
  })

  afterEach(() => {
    Object.defineProperty(window, 'electronAPI', { value: undefined, writable: true, configurable: true })
  })

  it('a click with action open-approval focuses the window and nothing else', () => {
    const { unmount } = renderHook(() => useNotificationDispatcher())
    expect(clickHandler).not.toBeNull()
    clickHandler!({ sessionCode: '', action: { kind: 'open-approval', hostId: 'host-b' } })
    expect(focusMyWindow).toHaveBeenCalledTimes(1)
    expect(useTabStore.getState().tabOrder).toEqual([])
    expect(useHostStore.getState().activeHostId).toBe('host-a')
    unmount()
  })

  it('handleNotificationClick({kind: open-approval}) is the same no-op-plus-focus', () => {
    handleNotificationClick({ kind: 'open-approval', hostId: 'host-b' })
    expect(focusMyWindow).toHaveBeenCalledTimes(1)
    expect(useTabStore.getState().tabOrder).toEqual([])
  })
})
```

- [ ] **Step 2: Run the tests and verify they fail.**
  - Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/lead-team/spa && npx vitest run src/lib/team/approval-notify.test.ts src/hooks/useNotificationDispatcher.approval.test.ts`
  - Expected: FAIL — `Failed to resolve import "./approval-notify"`; and in the dispatcher test the click with `open-approval` falls into the `open-session` branch: with both hosts shown and no tab, `handled` stays false and `focusMyWindow` is never called (`expected 1, received 0`). `handleNotificationClick({ kind: 'open-approval', … })` is also rejected by `tsc -b` (not in the union) — vitest does not type-check, so that one shows up in `pnpm run build`.

- [ ] **Step 3: Implement.**

`spa/src/lib/team/approval-notify.ts`:

```ts
// spa/src/lib/team/approval-notify.ts — the system notification for a new approval request (lead-team spec §6.3):
// `<主機>：<session> 申請成為 lead` through the existing Electron `showNotification` IPC. Raised on `opened` only; a
// snapshot (reconnect) re-shows the dialog but must not re-announce. `broadcastTs` is the request's `created_at`:
// Electron main dedups on it across this device's windows. U14: the App is the only client — no browser fallback.
import { getPlatformCapabilities } from '../platform'
import { hostLabel, hostLookOf } from '../host-look'
import { useI18nStore } from '../../stores/useI18nStore'
import { approvalSessionLabel } from './approval-format'
import { leadPayloadOf, type Approval } from './types'

export function notifyApprovalOpened(hostId: string, approval: Approval): void {
  if (!getPlatformCapabilities().canNotification || !window.electronAPI?.showNotification) return
  const t = useI18nStore.getState().t
  window.electronAPI.showNotification({
    title: t('approval.notify.title', { host: hostLabel(hostId, hostLookOf(hostId)), session: approvalSessionLabel(approval.origin) }),
    body: leadPayloadOf(approval).reason,
    sessionCode: '',
    eventName: 'ApprovalRequest',
    broadcastTs: approval.created_at,
    action: { kind: 'open-approval', hostId },
  })
}
```

`spa/src/lib/team/approval-ws.ts` — add the import and change the `opened` branch:

```ts
import { notifyApprovalOpened } from './approval-notify'
```

```ts
  if (ev.op === 'opened') {
    // Announce only what was actually added: a duplicate opened (two sockets, a replay) is silent.
    if (store.applyOpened(hostId, ev.approval)) notifyApprovalOpened(hostId, ev.approval)
    return
  }
```

`spa/src/hooks/useNotificationDispatcher.ts` — lines 113-115 become:

```ts
export type NotificationAction =
  | { kind: 'open-session'; hostId: string; sessionCode: string }
  | { kind: 'open-host'; hostId: string }
  /** An approval request (lead-team spec §6.3): the dialog is already on screen; the click only focuses the window. */
  | { kind: 'open-approval'; hostId: string }
```

lines 284-293 (inside the `onNotificationClicked` callback) become:

```ts
      if (!payload.action) return
      if (payload.action.kind === 'open-host') {
        handleNotificationClick({ kind: 'open-host', hostId: payload.action.hostId })
      } else if (payload.action.kind === 'open-approval') {
        handleNotificationClick({ kind: 'open-approval', hostId: payload.action.hostId })
      } else {
        handleNotificationClick({
          kind: 'open-session',
          hostId: payload.action.hostId,
          sessionCode: payload.action.sessionCode ?? payload.sessionCode,
        })
      }
```

and after the `case 'open-host': { … break }` block (line 409) add:

```ts
    case 'open-approval': {
      // The dialog is global and already shows the oldest open request; there is no tab to open or host to switch.
      if (window.electronAPI?.focusMyWindow) {
        window.electronAPI.focusMyWindow()
      }
      break
    }
```

`spa/src/locales/en.json` — after `approval.toast.ended_while_away`:

```json
  "approval.notify.title": "{{host}}: {{session}} requests to become lead"
```

`spa/src/locales/zh-TW.json`:

```json
  "approval.notify.title": "{{host}}：{{session}} 申請成為 lead"
```

- [ ] **Step 4: Run the tests and verify they pass.**
  - Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/lead-team/spa && npx vitest run src/lib/team/approval-notify.test.ts src/hooks/useNotificationDispatcher.approval.test.ts src/hooks/useNotificationDispatcher.test.ts src/hooks/useMultiHostEventWs.approval.test.ts src/locales/locale-completeness.test.ts`
  - Expected: PASS (7 new; the existing dispatcher suite unchanged).

- [ ] **Step 5: Commit.**
  ```bash
  git add spa/src/lib/team/approval-notify.ts spa/src/lib/team/approval-notify.test.ts spa/src/lib/team/approval-ws.ts spa/src/hooks/useNotificationDispatcher.ts spa/src/hooks/useNotificationDispatcher.approval.test.ts spa/src/locales/en.json spa/src/locales/zh-TW.json
  git commit -m "feat(spa): system notification on a new lead request; its click only focuses the window

  Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
  ```

### Task 3.7: The daemon-restart confirm gains `N 個申請等待核准` (only when N > 0)

**Files:**
- Modify: `spa/src/components/hosts/RestartDaemonButton.tsx:13-17` (imports), `:31-36` (`PendingConfirm` gains `approvals`), `:42-47` (one more store read), `:80-90` (`open()` fetches inflight beside the worker count), `:124-130` (a second line under the workers line)
- Modify: `spa/src/locales/en.json`, `spa/src/locales/zh-TW.json` (one key after `approval.notify.title`)
- Test: `spa/src/components/hosts/RestartDaemonButton.test.tsx:7-16` (mock `fetchInflight`, reset the approval store in `beforeEach`), plus five new tests appended to the top-level `describe`.

**Interfaces:**
- Consumes: `fetchInflight(hostId)` (Task 3.1; `GET /api/team/inflight`, self-bounded to 3 s); `useApprovalStore` + `selectOpenCountFor(hostId)` (Task 3.2) as the fallback.
- Produces: `<p data-testid={`${testId}-approvals`}>` in the confirm dialog, rendered only when the **restarting host** has open requests. The count is per host, not global — the other hosts' requests do not survive or suffer this restart.

Spec §9.5: the count comes from `GET /api/team/inflight`, fetched while the dialog counts running workers (both inside the same 3 s budget: `countRunningWorkers` races its own timer, `fetchInflight` aborts its own request), and when that call fails or times out the dialog falls back to the open requests its own store holds for that host. The daemon's answer wins over the store when both exist: the store lags the socket, the daemon is the source.

- [ ] **Step 1: Write the failing tests.**

In `spa/src/components/hosts/RestartDaemonButton.test.tsx`, add the imports and the module mock next to the existing `daemon-restart` mock (line 7), and the reset in `beforeEach`:

```ts
import { useApprovalStore } from '../../stores/useApprovalStore'
import type { Approval } from '../../lib/team/types'
import * as approvalApi from '../../lib/team/approval-api'

vi.mock('../../lib/team/approval-api', () => ({ fetchInflight: vi.fn() }))
```

```ts
beforeEach(() => {
  useI18nStore.getState().setLocale('zh-TW')
  restart.mockClear()
  useDaemonRestartStore.setState({ restarting: {}, settled: {}, restart })
  vi.mocked(restartLib.countRunningWorkers).mockReset()
  // Default: the inflight call fails (an older daemon, or one mid-restart); tests that want a daemon answer override it.
  vi.mocked(approvalApi.fetchInflight).mockReset()
  vi.mocked(approvalApi.fetchInflight).mockRejectedValue(new Error('inflight unavailable'))
  useApprovalStore.getState().reset()
})
```

and append inside `describe('RestartDaemonButton', …)`:

```ts
  describe('open approval requests (lead-team spec §9.5)', () => {
    const approval = (id: string): Approval => ({
      id, kind: 'lead', host_id: 'd1',
      origin: { session_id: 'S', ref: '_abcdef', name: 'n', pid: 1, proc_start: 'p', cwd: '/w', tmux: '' },
      payload: { reason: 'r', max_members: 3, roots: ['/w'] },
      state: 'open', created_at: 1, deadline_at: 2, lease_until: 3,
    })
    const daemonSays = (approvals_open: number) =>
      vi.mocked(approvalApi.fetchInflight).mockResolvedValueOnce({ approvals_open, relays_active: 0 })

    it('asks GET /api/team/inflight for THIS host and names its open requests', async () => {
      // Dropping the fetch, or reading the wrong field, turns this red (mutation deliverable).
      daemonSays(2)
      await openConfirm(0)
      expect(approvalApi.fetchInflight).toHaveBeenCalledTimes(1)
      expect(approvalApi.fetchInflight).toHaveBeenCalledWith('h1')
      expect(screen.getByTestId('restart-daemon-approvals').textContent).toBe('2 個申請等待核准')
    })

    it('the daemon\'s count wins over the store\'s when both exist', async () => {
      useApprovalStore.getState().applyOpened('h1', approval('a'))
      daemonSays(3)
      await openConfirm(0)
      expect(screen.getByTestId('restart-daemon-approvals').textContent).toBe('3 個申請等待核准')
    })

    it('when the inflight call rejects it falls back to the store\'s count for this host only', async () => {
      // beforeEach leaves fetchInflight rejecting.
      useApprovalStore.getState().applyOpened('h1', approval('a'))
      useApprovalStore.getState().applyOpened('h1', approval('b'))
      useApprovalStore.getState().applyOpened('h2', approval('c'))
      await openConfirm(0)
      expect(approvalApi.fetchInflight).toHaveBeenCalledWith('h1')
      expect(screen.getByTestId('restart-daemon-approvals').textContent).toBe('2 個申請等待核准')
    })

    it('no line when the daemon says none and the store has none for this host, even if another host does', async () => {
      daemonSays(0)
      useApprovalStore.getState().applyOpened('h2', approval('c'))
      await openConfirm(0)
      expect(screen.queryByTestId('restart-daemon-approvals')).toBeNull()
    })

    it('shows both lines when workers run and requests wait', async () => {
      daemonSays(1)
      await openConfirm(2)
      expect(screen.getByTestId('restart-daemon-workers')).toBeInTheDocument()
      expect(screen.getByTestId('restart-daemon-approvals').textContent).toBe('1 個申請等待核准')
    })
  })
```

  The existing tests keep passing: with the default rejection the fallback is a store that `beforeEach` emptied, so no `-approvals` line appears, and `openConfirm` still resolves because `Promise.all` settles once both the worker count and the (rejected → `null`) inflight call do.

- [ ] **Step 2: Run the test and verify it fails.**
  - Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/lead-team/spa && npx vitest run src/components/hosts/RestartDaemonButton.test.tsx`
  - Expected: FAIL — `Unable to find an element by: [data-testid="restart-daemon-approvals"]` in four of the five new tests and `expected "spy" to be called 1 times, but got 0 times` in the first; the "no line" one passes vacuously.

- [ ] **Step 3: Implement.**

`spa/src/components/hosts/RestartDaemonButton.tsx` — after line 14 (`import { useDaemonRestartStore } …`) add:

```ts
import { selectOpenCountFor, useApprovalStore } from '../../stores/useApprovalStore'
import { fetchInflight } from '../../lib/team/approval-api'
```

in `PendingConfirm` (lines 30-36), after the `workers` field add:

```ts
  // GET /api/team/inflight's approvals_open (lead-team spec §9.5); null = the call failed or timed out, and the
  // line falls back to the store's count for this host.
  approvals: number | null
```

after line 47 (`const restart = useDaemonRestartStore((s) => s.restart)`) add:

```ts
  // Fallback for the inflight count (lead-team spec §9.5): the open requests the WS snapshot keeps for THIS host.
  const storeOpenApprovals = useApprovalStore(selectOpenCountFor(hostId))
```

in `open()`, replace

```ts
    const workers = await countRunningWorkers(target)
    // Invalidated meanwhile: a newer state owns `countingFor`, touch nothing.
    if (!mounted.current || requestRef.current !== mine) return
    setCountingFor(null)
    if (stillValid(target, gen)) setConfirm({ hostId: target, gen, workers })
```

with

```ts
    // Both inside the dialog's 3 s budget: countRunningWorkers races its own timer, fetchInflight aborts its own
    // request (approval-api.ts INFLIGHT_TIMEOUT_MS). Any inflight failure is null → the store's count below.
    const [workers, approvals] = await Promise.all([
      countRunningWorkers(target),
      fetchInflight(target).then((r) => r.approvals_open, () => null),
    ])
    // Invalidated meanwhile: a newer state owns `countingFor`, touch nothing.
    if (!mounted.current || requestRef.current !== mine) return
    setCountingFor(null)
    if (stillValid(target, gen)) setConfirm({ hostId: target, gen, workers, approvals })
```

before the `return (` add:

```ts
  // Open approval requests on THIS host (lead-team spec §9.5): they survive the restart (leases are extended at
  // boot), so the line informs, it does not block. The daemon's answer first; the store when it could not be asked.
  const openApprovals = confirm === null ? 0 : (confirm.approvals ?? storeOpenApprovals)
```

and replace lines 124-130 (the children of `ConfirmDialog`) with:

```tsx
          {confirm.workers !== 0 && (
            <p data-testid={`${testId}-workers`} className="mt-2 text-xs text-amber-400">
              {confirm.workers === null
                ? t('hosts.restart.confirm_workers_unknown')
                : t('hosts.restart.confirm_workers', { count: confirm.workers })}
            </p>
          )}
          {openApprovals > 0 && (
            <p data-testid={`${testId}-approvals`} className="mt-1 text-xs text-amber-400">
              {t('approval.restart.pending', { count: openApprovals })}
            </p>
          )}
```

`spa/src/locales/en.json` — after `approval.notify.title`:

```json
  "approval.restart.pending": "{{count}} request(s) awaiting approval"
```

`spa/src/locales/zh-TW.json`:

```json
  "approval.restart.pending": "{{count}} 個申請等待核准"
```

- [ ] **Step 4: Run the tests and verify they pass.**
  - Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/lead-team/spa && npx vitest run src/components/hosts/RestartDaemonButton.test.tsx src/locales/locale-completeness.test.ts`
  - Expected: PASS (the whole `RestartDaemonButton` suite, 5 new).
  - Mutation check (spec §9.5 fallback; measured 2026-10-07 in the scratch build): make `open()` ignore the inflight answer (`approvals: null` always) and rerun — three go red (`asks GET /api/team/inflight …`, `the daemon's count wins …`, `shows both lines …`) and the fallback test stays green. Then make it ignore the store (`confirm.approvals ?? 0`) — exactly one goes red, the fallback test. Put both back.

- [ ] **Step 5: Commit.**
  ```bash
  git add spa/src/components/hosts/RestartDaemonButton.tsx spa/src/components/hosts/RestartDaemonButton.test.tsx spa/src/locales/en.json spa/src/locales/zh-TW.json
  git commit -m "feat(spa): restart confirm lists the host's approval requests awaiting a decision

  Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
  ```

### Task 3.8: i18n — the `approval.` namespace pinned, every key listed, lint and build

**Files:**
- Modify: `spa/src/locales/locale-completeness.test.ts:93` (a new `describe` block inserted before the Live Mode test at line 94-106)
- Verify: `spa/src/locales/en.json`, `spa/src/locales/zh-TW.json` carry exactly the keys below (added by Tasks 3.3–3.7; nothing new is added here unless a key is found missing).

**Interfaces:** none new. This task is the consolidated contract for the strings.

**Every `approval.*` key (both files; zh-TW strings are the spec's where the spec quotes one):**

| Key | en | zh-TW | Added in |
|---|---|---|---|
| `approval.kind.lead` | lead request | lead 申請 | 3.3 |
| `approval.kind.self_relay` | relay request | 接力申請 | 3.3 |
| `approval.decision.approved` | approved | 核准 | 3.3 |
| `approval.decision.denied` | denied | 拒絕 | 3.3 |
| `approval.state.approved` | approved | 已核准 | 3.3 |
| `approval.state.denied` | denied | 已拒絕 | 3.3 |
| `approval.state.timeout` | timed out | 逾時 | 3.3 |
| `approval.state.cancelled` | cancelled | 已取消 | 3.3 |
| `approval.state.abandoned` | abandoned | 已放棄 | 3.3 |
| `approval.toast.decided_elsewhere` | {{host}}: {{client}} {{decision}} the {{kind}} from {{session}} | {{host}}：{{session}} 的 {{kind}} 已由 {{client}} {{decision}} | 3.3 |
| `approval.toast.ended` | {{host}}: the {{kind}} from {{session}} ended ({{state}}) | {{host}}：{{session}} 的 {{kind}}已結束（{{state}}） | 3.3 |
| `approval.toast.failed` | Could not send the decision ({{code}}) | 送出決定失敗（{{code}}） | 3.4 |
| `approval.dialog.title_lead` | {{host}}: {{session}} requests to become lead | {{host}}：{{session}} 申請成為 lead | 3.4 |
| `approval.dialog.host` | Host | 主機 | 3.4 |
| `approval.dialog.session` | Session | Session | 3.4 |
| `approval.dialog.address` | Address | 位址 | 3.4 |
| `approval.dialog.cwd` | Directory | 目錄 | 3.4 |
| `approval.dialog.tmux` | tmux | tmux | 3.4 |
| `approval.dialog.reason` | Reason | 理由 | 3.4 |
| `approval.dialog.deadline` | Time left | 剩餘時間 | 3.4 |
| `approval.dialog.max_members` | Max members (1–8) | member 上限（1–8） | 3.4 |
| `approval.dialog.max_members_range` | Enter a whole number from 1 to {{max}} | 請輸入 1 到 {{max}} 的整數 | 3.4 |
| `approval.dialog.roots` | Allowed roots (one per line) | 允許的目錄（一行一個） | 3.4 |
| `approval.dialog.roots_required` | At least one root is required | 至少要有一個目錄 | 3.4 |
| `approval.dialog.daemon_restarting` | daemon restarting… | daemon 重啟中… | 3.4 |
| `approval.dialog.queued` | “{{decision}}” noted; it is sent once the connection is back | 已記下「{{decision}}」，恢復連線後送出 | 3.4 |
| `approval.dialog.more_pending` | {{count}} more request(s) waiting | 還有 {{count}} 個申請排隊中 | 3.4 |
| `approval.dialog.approve` | Approve | 核准 | 3.4 |
| `approval.dialog.deny` | Deny | 拒絕 | 3.4 |
| `approval.toast.ended_while_away` | {{host}}: the {{kind}} from {{session}} ended while this app was disconnected; your decision was not sent | {{host}}：{{session}} 的 {{kind}}已在離線期間結束，你的決定未送出 | 3.5 |
| `approval.notify.title` | {{host}}: {{session}} requests to become lead | {{host}}：{{session}} 申請成為 lead | 3.6 |
| `approval.restart.pending` | {{count}} request(s) awaiting approval | {{count}} 個申請等待核准 | 3.7 |

Spec quotes honoured verbatim: `daemon 重啟中…` (§6.3), `<主機>：<session> 的 lead 申請 已由 <client> 核准／拒絕` (§6.3, via `decided_elsewhere` with kind `lead 申請`), `<主機>：<session> 申請成為 lead` (§6.3), `N 個申請等待核准` (§9.5). No `pluralKey` here: zh-TW has no plural forms and the spec strings are count-agnostic; the en strings use "(s)" like `hosts.restart.confirm_workers`.

- [ ] **Step 1: Write the failing test.**

Insert before line 94 (`  // The Live Mode gate explains …`) of `spa/src/locales/locale-completeness.test.ts`:

```ts
  // The approval namespace (lead-team spec §6.3, §9.5). The four strings the spec fixes word for word are pinned in
  // zh-TW, and a translation that dropped a `{{client}}` would hide WHO approved — the one fact U6's toast exists for.
  describe('the approval namespace', () => {
    const approvalKeys = (o: Record<string, string>) => Object.keys(o).filter((k) => k.startsWith('approval.')).sort()
    const enA = approvalKeys(en as Record<string, string>)
    const zhA = approvalKeys(zhTW as Record<string, string>)

    it('exists with identical key sets', () => {
      expect(enA.length).toBeGreaterThan(0)
      expect(zhA).toEqual(enA)
    })

    it('keeps every placeholder in the translation', () => {
      const placeholders = (v: string) => (v.match(/\{\{\w+\}\}/g) ?? []).sort()
      for (const key of enA) {
        expect(placeholders((zhTW as Record<string, string>)[key]), key).toEqual(placeholders((en as Record<string, string>)[key]))
      }
    })

    it('carries the spec §6.3 / §9.5 strings in zh-TW', () => {
      const zh = zhTW as Record<string, string>
      expect(zh['approval.dialog.daemon_restarting']).toBe('daemon 重啟中…')
      expect(zh['approval.toast.decided_elsewhere']).toBe('{{host}}：{{session}} 的 {{kind}} 已由 {{client}} {{decision}}')
      expect(zh['approval.kind.lead']).toBe('lead 申請')
      expect(zh['approval.kind.self_relay']).toBe('接力申請')
      expect(zh['approval.notify.title']).toBe('{{host}}：{{session}} 申請成為 lead')
      expect(zh['approval.restart.pending']).toBe('{{count}} 個申請等待核准')
    })

    it('has a state label for every closed state', () => {
      for (const s of ['approved', 'denied', 'timeout', 'cancelled', 'abandoned']) expect(enA, s).toContain(`approval.state.${s}`)
    })
  })

```

- [ ] **Step 2: Run the test and verify it fails if any key is missing.**
  - Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/lead-team/spa && npx vitest run src/locales/locale-completeness.test.ts`
  - Expected: PASS when Tasks 3.3–3.7 added every key above. To see the block bite before trusting it, temporarily delete `"approval.state.abandoned"` from `zh-TW.json`, run again (expected: `approval.* keys differ` and `has a state label …` red), then restore it. This is the mutation check for the namespace block.

- [ ] **Step 3: Implement.** Nothing beyond the test block, unless Step 2 showed a missing or misspelled key — then fix it in both locale files to match the table.

- [ ] **Step 4: Run everything that P3 touched, then lint and build.**
  - Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/lead-team/spa && npx vitest run src/lib/team src/stores/useApprovalStore.test.ts src/hooks/useMultiHostEventWs.approval.test.ts src/hooks/useMultiHostEventWs.worker-exited.test.ts src/hooks/useNotificationDispatcher.test.ts src/hooks/useNotificationDispatcher.approval.test.ts src/components/ApprovalDialogHost.test.tsx src/components/HandoffDialogHost.test.tsx src/components/hosts/RestartDaemonButton.test.tsx src/locales/locale-completeness.test.ts && pnpm run lint && pnpm run build`
  - Expected: all PASS; `eslint .` reports nothing; `tsc -b && vite build` succeeds.

- [ ] **Step 5: Commit.**
  ```bash
  git add spa/src/locales/locale-completeness.test.ts
  git commit -m "test(spa): pin the approval i18n namespace and the spec's zh-TW strings

  Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
  ```

### Deviations from the handoff notes

1. **`applySnapshot` returns only the vanished ids, not "along with any queued deny".** The notes had the snapshot return the queue too; here the store stays pure: `takeQueued(hostId)` removes and returns the host's queue, and `handleApprovalEvent`'s snapshot branch (Task 3.5) takes the queue first, applies the snapshot, then matches the two. Same behaviour, two calls instead of one compound return.
2. **`queueDecision` takes the `Approval`, not the id** (`queueDecision(hostId, approval, decision, grant?)`). The `ended_while_away` toast needs the session and kind after the snapshot has already dropped the entry, so the queued record carries the approval. `markSent` from the notes does not exist: the resend path is `takeQueued` (consumed on take), and a resend that fails on the network re-queues itself inside `submitDecision`.
3. **A `decidedHere` mark replaces an implicit "ours" check.** The notes did not say how the WS `closed` after our own 200 avoids toasting at the person who clicked. `markDecidedHere` before the send, `applyClosed` returns `'ours' | 'elsewhere' | 'absent'`. A 409 whose `decided_by.label` equals this app's label is also treated as ours (a lost answer, resent).
4. **"Buttons disabled while disconnected" is `aria-disabled`, not `disabled`.** The task text asks for disabled buttons *and* for a click to be queued; a `disabled` button cannot be clicked. The dialog dims both buttons with `aria-disabled` (the same classes `RestartDaemonButton` uses for its counting state) and queues the click; once a decision is queued they become really `disabled`. The banner text is the spec's `daemon 重啟中…` exactly; the queued state adds a second line.
5. **Per-host count in the restart confirm.** The task names `selectOpenCount`; the dialog uses `selectOpenCountFor(hostId)`, because the other hosts' requests are not affected by this host's restart. `selectOpenCount` (global) exists too and is what the dialog's "還有 N 個申請排隊中" line uses. Spec §9.5's `GET /api/team/inflight` is called first (P2a ships it with `relays_active: 0`; `fetchInflight`, Task 3.1, bounds itself to the dialog's 3 s); the store's per-host count is the fallback when that call fails or times out (codex plan round, F4 — the handoff notes had deferred the call to P6).
6. **`Origin` gains two optional fields in TS only: `title?` and `address?`.** Spec §6.3 wants "the session's title or name" and "address and ref"; the wire `Origin` has neither a title nor the formatted address. The SPA falls back to `name` → `ref` and builds `<host>/<name> [<ref6>]` locally with the SPA's host label as `<host>`. See `## Open questions`.
7. **No browser `Notification` fallback, and no `shouldDispatch` localStorage dedup** for approvals: the request id already makes `applyOpened` idempotent, and Electron main dedups on `broadcastTs`. The existing `sendConnectionNotification` fallback is not extended (U14).
8. **Line numbers re-verified.** The notes' `host-events.ts:4-15`, `useMultiHostEventWs.ts:172-235`, `App.tsx:283`, `electron.d.ts:54-62, :173`, `handoff-api.ts:129-150`, `host-api.ts:214`, `useNotificationDispatcher.ts:280-294`, `RestartDaemonButton.tsx:115-131` all still hold on `d39886e8`; only the inner line of the `nex-worker-exited` branch (213-217) and the import line (21) were added above.
9. **i18n keys are added task by task, not all in 3.8.** Each task's test asserts the real zh-TW string, so the keys must exist when that task's test runs; Task 3.8 is the consolidated table plus the namespace completeness block, lint and build.

### Size estimate

Counted from the code blocks above (new files whole; modified files by lines added).

**P3a (Tasks 3.1–3.4)** — 17 files:

| File | Lines |
|---|---|
| `lib/team/types.ts` | ~105 |
| `lib/team/approval-api.ts` | ~110 |
| `lib/team/approval-api.test.ts` | ~165 |
| `stores/useApprovalStore.ts` | ~165 |
| `stores/useApprovalStore.test.ts` | ~180 |
| `lib/team/approval-format.ts` | ~60 |
| `lib/team/approval-ws.ts` | ~65 |
| `lib/host-events.ts` | +1 |
| `hooks/useMultiHostEventWs.ts` | +7 |
| `hooks/useMultiHostEventWs.approval.test.ts` | ~130 |
| `lib/team/client-label.ts` | ~40 |
| `lib/team/approval-decide.ts` | ~55 |
| `components/ApprovalDialogHost.tsx` | ~200 |
| `components/ApprovalDialogHost.test.tsx` | ~280 |
| `App.tsx` | +3 |
| `locales/en.json` | +29 |
| `locales/zh-TW.json` | +29 |
| **Total** | **≈ 1 620 lines, 17 files** (re-counted 2026-10-07 after the codex plan round: `fetchInflight` and its tests, the tombstones and their tests) |

**P3b (Tasks 3.5–3.8)** — 11 files:

| File | Lines |
|---|---|
| `lib/team/approval-ws.ts` | +25 |
| `lib/team/approval-ws.test.ts` | ~110 |
| `lib/team/approval-notify.ts` | ~25 |
| `lib/team/approval-notify.test.ts` | ~85 |
| `hooks/useNotificationDispatcher.ts` | +12 |
| `hooks/useNotificationDispatcher.approval.test.ts` | ~50 |
| `components/hosts/RestartDaemonButton.tsx` | +20 |
| `components/hosts/RestartDaemonButton.test.tsx` | +65 |
| `locales/en.json` / `zh-TW.json` | +3 each |
| `locales/locale-completeness.test.ts` | +38 |
| **Total** | **≈ 435 lines, 11 files** (re-counted 2026-10-07: the inflight fetch in the restart confirm and its five tests) |

The Global Constraint is "≤ 800 lines **or** ≤ 20 files"; both PRs meet the file bound. P3a exceeds the line bound (about 55 % of it is test code). If the coordinator wants the line bound as well, split P3a once more at the natural seam: **P3a-1** = Tasks 3.1–3.3 (types, API, store, WS branch, toasts; ≈ 830 lines, 10 files) and **P3a-2** = Task 3.4 (dialog host, client label, decide path; ≈ 640 lines, 7 files). P3a-1 ships nothing user-visible except the "closed elsewhere" toast, which is harmless without the dialog.

### Open questions for the coordinator

1. **Two `Origin` fields the dialog wants that the wire does not carry.** Spec §6.3 lists "the session's title or name" and "address and ref". `team.Origin` has `name` and `ref` but no `title` and no pre-formatted `address`; the peers module already computes both for `GET /api/peers` (`PeerRecordWire.title`, `.address`, `host-api.ts:87-108`). Proposal for P2a, zero cost on this side: add `Title string \`json:"title,omitempty"\`` and `Address string \`json:"address,omitempty"\`` to `team.Origin`, filled by the origin resolver. The TS type already has them optional and prefers them when present; without them the SPA shows `name`→`ref` and builds `<SPA host label>/<name> [<ref6>]`, which differs from pdx's `<daemon host name>/…` only when the user renamed the host in the App. Not a blocker for P3.
2. **Multi-window focus on the notification click.** Electron broadcasts `notification:clicked` to every window (`electron/main.ts:178-182`); the `open-approval` handler calls `focusMyWindow` in each, exactly as `open-host` does today. Acceptable for v1 (same as the existing behaviour); flag if a single-window focus is wanted.
3. **Electron's `broadcastTs` dedup is a bare `Set<number>` of timestamps** (`main.ts:155-165`). Two approvals created in the same millisecond on two hosts would dedup into one notification on this device. Negligible; noted so nobody "fixes" it by changing `broadcastTs` away from `created_at`, which is what makes two windows of one device dedup correctly.
4. **A dialog opened by a `closed` racing a late `opened`.** If the daemon's `closed` for a request reaches a client before that client ever saw `opened` (a socket that connected between the two), `applyClosed` returns `'absent'` and `applyOpened` later adds a request that is already closed on the daemon; it stays until the next snapshot or until a click answers 409/404 (both close it). P2a could make `opened` carry nothing new; the SPA could call `listOpenApprovals` after each `opened` to confirm — not done here to keep the event path single-source. Flagging as a known, self-healing edge.

### Coordinator decisions on P3 (2026-10-07, `mlab/_81nu3d`)

- **Split into three PRs, each under 800 lines (codex finding 9):** **P3a-1** = Tasks 3.1, 3.2 (types, API client, store; ≈ 725 lines after the codex plan round added `fetchInflight` and the tombstones with their tests); **P3a-2** = Task 3.4 (dialog host, client label, decide path; ≈ 640 — the dialog is store-driven, so it is tested without the WS branch); **P3b** = Tasks 3.3, 3.5–3.8 (WS branch and toasts, reconnect resend, notification, restart line with the inflight fetch, i18n block; ≈ 695). The dialog shows nothing until P3b lands; daemon deploys are batched anyway.
- **Open question 1 is taken into P2a:** `team.Origin` gains `Title string \`json:"title,omitempty"\`` and `Address string \`json:"address,omitempty"\`` (see "Coordinator decisions on P2a", amended). The TS `Origin.title?` / `address?` in Task 3.1 therefore match the wire, and the local fallback stays for an older daemon.
- **Open questions 2–3 are accepted as v1 behaviour** (every window focuses on the notification click, as `open-host` does; `broadcastTs` stays `created_at`); **open question 4 is closed by the store's per-host tombstones (Task 3.2, codex plan round F8):** a `closed` that arrives before its `opened` is recorded even when the entry is absent, the late `opened` is ignored, and the next snapshot — the daemon's authoritative list — clears the tombstones. Each is noted in the task that owns it.
- **Deviations 1–9 are accepted.** Deviation 4 (`aria-disabled` plus queue, then `disabled` once queued) is the reading of spec §6.3 that lets "a click while disconnected is kept locally" work at all.
- **After P3a-2 shipped (PR #1686, 2026-10-07), P3b's tasks build on what landed, not on the plan's pre-split text:**
  1. `toastClosed` lives in `spa/src/lib/team/approval-decide.ts` (shipped with the dialog). Task 3.3's `approval-ws.ts` **imports** it from there; do not define a second copy.
  2. `spa/src/lib/team/approval-format.ts` and these 12 locale keys already exist: `approval.kind.lead|self_relay`, `approval.decision.approved|denied`, `approval.state.approved|denied|timeout|cancelled|abandoned`, `approval.toast.decided_elsewhere|ended`. Task 3.3 adds only the WS-branch keys it still needs; Task 3.8's table lists all keys but adds none of these again.
  3. The dialog treats `network` as "queue" regardless of host status, and `host_removed` as "drop"; Task 3.5's resend path consumes the queue through `takeQueued` on the snapshot.

