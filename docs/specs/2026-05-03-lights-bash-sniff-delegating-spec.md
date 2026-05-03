# Spec — Lights: Bash sniff delegating flag (cc → codex visibility)

**Issue**: [#821](https://github.com/wake/purdex/issues/821)
**Kickoff**: `kickoff_codex_broker_and_lights_governance.md` §6 (user-actionable safety signal)
**Date**: 2026-05-03 (v1 → v2)
**Branch**: `worktree-lights-bash-sniff-delegating`
**Baseline**: origin/main `9374bf88` (alpha.283)
**Scope**: Detect cc-side delegation to codex via cc's own `PreToolUse(Bash)` command sniff, surface visually as orange dot on the cc tab — without violating the existing `IsProxy` invariant, without requiring codex plugin cooperation, and without depending on any governance phase.

**Estimated size**: ~620 production+tests LOC (within AC11 cap of 700), plus ~850 LOC for spec + plan docs. Single PR. One round of standard codex review should suffice for the implementation; spec passed round 2 with 0 blockers (round 1 surfaced 3 blockers + 2 important fixes addressed in v2).

**v2 supersedes v1** — v1 review (`task-moors21a-we2agx`, codex thread `019dea4a-5bc9-73d1-aa66-064b0511a2a1`) found 3 blockers (B1 substring missing canonical quoted command; B2 single ToolUseID can't represent N concurrent codex Bash; B3 `PostToolUseFailure` missed) + 2 important fixes (M1 background Bash lifecycle ambiguous; M2 mutateSubagentsWithRetry race description overstated) + 5 fact corrections + size + AC11 wording contradiction. v2 addresses all of them.

---

## 1. Symptom & motivation

### 1.1 User-reported gap

When the user runs `/codex:rescue` / `/codex:review` / `/codex:adversarial-review` inside a cc session (sessions `purdex-big-plan`, `purdex-sync`), cc's tab shows a **blue native subagent dot** instead of the **orange cross-agent proxy dot** that the lights system reserves for "cc is awaiting another agent."

### 1.2 Root cause (already investigated, not in scope to fix)

`/codex:rescue` routes to a cc-native Task subagent named `codex-rescue` (a forwarder defined at `~/.claude/plugins/cache/openai-codex/codex/1.0.2/agents/codex-rescue.md`). cc fires upstream `SubagentStart` hook → daemon walks `LifecycleSubagentStart` path (`internal/module/agent/frame_ops.go:145-200`) → `IsProxy` is left zero (line 166) → SPA renders blue (`spa/src/components/SubagentDots.tsx:16-20`).

The forwarder then `Bash`-spawns `node "${CLAUDE_PLUGIN_ROOT}/scripts/codex-companion.mjs" task ...` which attaches to a daemonized broker (`app-server-broker.mjs`, `setsid`+detached, `PPID=1`). Broker spawns a `task-worker` (real codex CLI). Codex fires its 9 hooks via `~/.codex/hooks.json` → `pdx hook --agent codex Pdx<Event>`.

But `findProxyParent` (`frame_ops.go:1689-1753`) requires the PPID chain from sender PID upward to reach a frame-registered cross-type ancestor in the same `TmuxPaneID`. The chain breaks at broker (`broker.PPID=1` → line 1699 `if ppid <= 1: return nil`). So the proxy `SubagentRef` attach **never fires** for the codex-via-broker scenario. L2 PR #801 (alpha.283) fixed `Stop`-after-attach detach but does not help here — attach never happens.

### 1.3 Why the colour matters (user-actionable safety signal)

| Colour | Meaning | What the agent knows about it |
|---|---|---|
| Blue (native) | cc is doing the work itself | `SubagentStop` hook fires when child finishes — cc has lifecycle visibility |
| Orange (proxy) | cc is **awaiting** a cross-agent | **No lifecycle visibility** — if codex hangs, cc waits forever |

**Orange dot stuck for too long = cc is hanging on codex, user should intervene**. With everything currently shown as blue, the user has no visual signal to distinguish "cc is thinking hard" from "cc is stuck on a dead codex broker." The user must memorize start time and watch the clock manually. This is the gap to close.

### 1.4 Five alternatives considered, this one chosen

A separate brainstorm round (codex thread `019dea2a-f067-7fd1-a2fb-dedf1b537b53`, plus subagent run) evaluated 6 alternatives beyond Op1/Op2/Op3. Key dismissals:

- **Op1 protocol extension** (codex hook payload carries `originating_cc_session_id+pid`): M complexity, depends on plugin cooperation, ~2-3 weeks behind governance P3.
- **Op2 broker registry redirect**: violates governance P1 read-only inventory design (`internal/codexbroker` plan §1).
- **Op3 cc subagent name whitelist**: pollutes the `IsProxy=true ⟺ verified cross-type PPID identity` invariant; bad dead-detection.
- **Reverse light-up via `pre_tool_without_proxy_parent` skip reason** (`frame_ops.go:232`): structural cross-cc-session pollution risk when broker outlives cc-A and gets adopted by cc-B in the same pane (governance P3 territory).

The chosen approach (this spec): **decouple from codex entirely**. Use cc's own `PreToolUse(Bash)` event stream — which cc already fires reliably and which the daemon already routes (`handler.go:195-202` for path hint extraction) — to detect "cc is currently invoking codex-companion." Surface as a separate **`Delegating` flag** on `SubagentRef`, rendered with the existing orange colour. **`IsProxy` invariant is left untouched**.

---

## 2. Cross-validated facts

### 2.1 cc hook payload schema

[Anthropic Claude Code hooks reference](https://code.claude.com/docs/en/hooks), confirmed 2026-05-03:

| Field | PreToolUse | PostToolUse | PostToolUseFailure |
|---|---|---|---|
| `session_id` | ✅ | ✅ | ✅ |
| `transcript_path` | ✅ | ✅ | ✅ |
| `cwd` | ✅ | ✅ | ✅ |
| `permission_mode` | ✅ | ✅ | ✅ |
| `hook_event_name` | ✅ | ✅ | ✅ |
| `agent_id` | optional (present when running in subagent) | optional (same) | optional (same) |
| `agent_type` | optional (when running in subagent) | optional (same) | optional (same) |
| `tool_name` | ✅ | ✅ | ✅ |
| `tool_input` | ✅ | ✅ | ✅ |
| `tool_use_id` | ✅ | ✅ | ✅ |

Bash tool input schema:
```json
{ "tool_name": "Bash", "tool_input": { "command": "npm test", "description": "...", "timeout": 120000, "run_in_background": false } }
```

`PostToolUseFailure` already in cc events catalog at `internal/agent/cc/events.go:127-133` (Lifecycle: None; Handling defaults to `detail` because `EmitsStatus` is empty — see `EffectiveHookHandling` in `internal/agent/provider.go`). The default `detail` classification is what makes the installer write the hook into `~/.claude/settings.json`, which is required for unmark to fire on Bash failure paths. This spec re-purposes its raw event stream for unmark — Lifecycle classification stays None.

### 2.2 Daemon already extracts cc raw event in PreToolUse / PostToolUse

`internal/module/agent/handler.go:195-202` already gates cc `PdxPreToolUse` / `PdxPostToolUse` for raw event decoding (PathHint extraction). New extractor mirrors the same pattern: `internal/module/agent/path_hint_extractor.go` (`MaxRawEventBytes = 64 KiB` cap, pure function `ExtractPathHint`).

Session resolution must use `resolveSessionCodeFromHook` (handler's existing helper that prefers immutable `tmux_session_id`), not `FindByPanePID` directly — the latter would re-introduce the rename race that handler.go:201-206 explicitly avoids.

### 2.3 SubagentRef extensibility

`internal/agent/subagent.go::SubagentRef` already added `SourceTurnID` for L2 with the same `omitempty` + opaque-blob storage pattern (header comment lines 8-13: "subagents_json is an opaque TEXT blob; see frames.go — No DB migration needed"). New `omitempty` fields (`Delegating bool`, `DelegatingToolUseIDs []string`) follow the same pattern with zero migration cost.

### 2.4 codex-companion invocation pattern (canonical forms)

Confirmed against plugin v1.0.2 in-scope delegation dispatch sites (the read-only `status`/`result`/`cancel`/`setup` subcommands also invoke `codex-companion.mjs` but run at top-level cc context where `agent_id == ""`, so they are naturally filtered by §3.2 step 2):

| Source | Canonical form |
|---|---|
| `commands/rescue.md:21` | `node "${CLAUDE_PLUGIN_ROOT}/scripts/codex-companion.mjs" task ...` |
| `commands/review.md:45` | `node "${CLAUDE_PLUGIN_ROOT}/scripts/codex-companion.mjs" review ...` |
| `commands/adversarial-review.md:50` | `node "${CLAUDE_PLUGIN_ROOT}/scripts/codex-companion.mjs" adversarial-review ...` |
| `agents/codex-rescue.md:21` | `node "${CLAUDE_PLUGIN_ROOT}/scripts/codex-companion.mjs" task ...` |

**Critical observation**: `codex-companion.mjs` is followed by `"` (closing quote) and then space — **not directly by space**. v1's substring `"codex-companion.mjs "` (trailing space) would have **missed every canonical command**. v2 fixes by token-boundary detection — see §3.3.

### 2.5 Concurrency: SubagentDots cap

`spa/src/components/SubagentDots.tsx:33` clamps to 3 dots maximum. `/codex:adversarial-review` triggers up to 3 parallel codex jobs (attack / defense / health). Each parallel dispatch comes via its own forwarder subagent → cc fires 3 `SubagentStart` events → 3 separate `SubagentRef` entries. Each ref independently sets/unsets its own `Delegating` flag based on its own subagent's Bash call. This naturally maps to "3 orange dots" without further wiring.

### 2.6 Background Bash lifecycle (background dispatch flows)

`/codex:review --background` and `/codex:adversarial-review --background` invoke Bash with `run_in_background: true` (plugin `commands/review.md:52-58`, `commands/adversarial-review.md:56-63`). Anthropic's hook spec does **not** publicly document whether `PostToolUse` fires immediately on launch (lifecycle a) or at process exit (lifecycle b).

**Verified via 2026-05-03 mlab fixture (post-ship)**: lifecycle **(a) confirmed** — `PostToolUse` fires within ≤1 s of `PreToolUse` regardless of the background process's actual runtime (sleep-8 fixture: PreToolUse 17:12:01 → PostToolUse 17:12:02 → background exit 17:12:09–10).

Therefore background dispatches show **only an orange flicker**: PreToolUse marks Delegating=true, PostToolUse clears it almost immediately, and the cc-native dot stays blue for the remainder of the background subprocess (which is observed via `BashOutput` polls firing as **new tool uses**, not the original one). Tracking the full background duration would require keying on the original `tool_use_id` across `BashOutput` polls — **out of scope for this spec**, captured as known limitation **L6**.

§4 AC1/AC2 cover foreground dispatches; AC1b/AC2b are scoped to the confirmed flicker-only background behaviour.

---

## 3. Strategy

### 3.1 Identity model

Add two `omitempty` fields to `SubagentRef`:

```go
type SubagentRef struct {
    // ... existing fields ...
    Delegating           bool     `json:"delegating,omitempty"`
    DelegatingToolUseIDs []string `json:"delegating_tool_use_ids,omitempty"`
}
```

`Delegating == true` ⟺ `len(DelegatingToolUseIDs) > 0` (invariant maintained by helpers in §3.5).

`Delegating=true` means **"this cc-native subagent has at least one in-flight Bash call invoking codex-companion"**. The flag is owned by cc's own event stream — it doesn't claim anything about codex's actual liveness. `DelegatingToolUseIDs` records every active Bash invocation that triggered the flag, so multiple concurrent codex calls from the same subagent track independently and stale-tool-id PostToolUse events become idempotent no-ops (B2 fix).

`IsProxy` is **not** touched. SPA renders dots with: `(ref.is_proxy || ref.delegating) ? PROXY_COLOR : NATIVE_COLOR`. The two flags coexist without interference; downstream proxy logic (L2 turn-aware detach, `canonicalizeDescendantsAfterUpsert`, `pruneDeadProxyRefs`) reads only `IsProxy` and is unaffected.

### 3.2 Wiring

Three new mutations on the cc native-subagent path. All gated on `req.AgentType == "cc"` and `tool_name == "Bash"`. All run alongside the existing PathHint extraction in `handler.go:195-202`.

**On `PdxPreToolUse`**:
1. Decode raw payload: `{tool_name, tool_input.command, tool_use_id, agent_id}`.
2. Skip if `tool_name != "Bash"`, `agent_id == ""` (means cc is running at top-level, not inside a Task subagent), `tool_use_id == ""`, or command doesn't match codex-companion token (§3.3).
3. Resolve session via `resolveSessionCodeFromHook` (NOT `FindByPanePID` directly).
4. Find the cc frame for this pane and the `SubagentRef` whose `ID == agent_id`.
5. Append `tool_use_id` to `DelegatingToolUseIDs` (deduped); set `Delegating=true`.
6. Persist via `mutateSubagentsWithRetry` (covers concurrent writes **after the ref already exists**, see M2).

**On `PdxPostToolUse`** and **`PdxPostToolUseFailure`** (B3 fix — same handler path):
1. Decode raw payload: `{tool_name, tool_use_id, agent_id}`. (`tool_input.command` not needed — Pre's match is the ground truth; Post unmark works by `tool_use_id` membership.)
2. Skip if `tool_name != "Bash"`, `agent_id == ""`, or `tool_use_id == ""`.
3. Resolve session via `resolveSessionCodeFromHook`.
4. Find the cc frame and `SubagentRef` whose `ID == agent_id`.
5. Remove `tool_use_id` from `DelegatingToolUseIDs` (no-op if not present — covers PostToolUse arriving for non-codex Bash, or after `SubagentStop`). Update `Delegating` flag: `len(DelegatingToolUseIDs) > 0`.
6. Persist via `mutateSubagentsWithRetry`.

**On `PdxSubagentStop`** (cc native): existing flow removes the ref entirely. `Delegating` flag and `DelegatingToolUseIDs` disappear with the ref. No special handling needed.

**On `PdxPostToolBatch`**: out of scope — parallel tool call batch is a Task tool semantic that doesn't flow through Bash. Spec's `tool_name == "Bash"` gate naturally ignores it.

### 3.3 Match pattern (token detection — B1 fix)

```
required: command contains "codex-companion.mjs" followed by one of: '"', "'", whitespace, EOL
```

Implementation: locate substring `codex-companion.mjs` then inspect the next byte. Reject if next byte is alphanumeric / `.` / `/` / `_` / `-` (i.e. continues a filename token like `codex-companion.mjs.bak` or `codex-companion.mjsx`).

| `tool_input.command` | Match? |
|---|---|
| `node "${CLAUDE_PLUGIN_ROOT}/scripts/codex-companion.mjs" task --background "..."` | ✅ (next byte: `"`) |
| `node ~/.claude/plugins/.../codex-companion.mjs review --base main` | ✅ (next byte: ` `) |
| `bash -c "node /custom/codex-companion.mjs adversarial-review focus"` | ✅ (next byte: ` `) |
| `node 'codex-companion.mjs' task` | ✅ (next byte: `'`) |
| `node $(./find-script).mjs task` | ❌ (does not contain `codex-companion.mjs`) |
| `node codex-companion.mjs.bak task` | ❌ (next byte: `.` — not boundary) |
| `echo "codex-companion.mjs"` | ✅ (next byte: `"` — known false positive, see L1) |
| `cat README \| grep codex-companion.mjs` | ✅ (next byte: EOF — known false positive, see L1) |
| `git log --grep=codex-companion.mjsx` | ❌ (continues token) |

Implementation hint (Go):
```go
func containsCodexCompanionToken(command string) bool {
    needle := "codex-companion.mjs"
    for i := 0; i+len(needle) <= len(command); {
        idx := strings.Index(command[i:], needle)
        if idx < 0 { return false }
        end := i + idx + len(needle)
        if end == len(command) {
            return true
        }
        c := command[end]
        if c == '"' || c == '\'' || c == ' ' || c == '\t' || c == '\n' {
            return true
        }
        // Continues a different token — keep searching.
        i = i + idx + len(needle)
    }
    return false
}
```

The `echo` / `grep` false positives are acknowledged in L1 — full shell parsing is far more expensive than the value of 100% precision. Functional damage is zero (dot just turns orange briefly until matching PostToolUse arrives).

### 3.4 Extractor (pure function)

New file `internal/module/agent/delegation_extractor.go`:

```go
type DelegationHint struct {
    AgentID      string  // hook payload agent_id (cc Task subagent id)
    ToolUseID    string  // hook payload tool_use_id (Bash invocation id)
    IsCodexMark  bool    // PreToolUse with command match → mark
    IsUnmark     bool    // PostToolUse / PostToolUseFailure → unmark by tool_use_id
}

// ExtractDelegationHint is a pure function. Returns hint+true when the raw
// cc PreToolUse / PostToolUse / PostToolUseFailure event represents a Bash
// invocation inside a subagent.
//
// IsCodexMark is true only for PreToolUse with a codex-companion token match.
// IsUnmark is true for PostToolUse / PostToolUseFailure regardless of command
// (caller uses ToolUseID for membership-removal — non-codex Bash unmarks
// naturally as no-op).
//
// Defenses (mirrored from path_hint_extractor.go):
//   - rawEvent must be < MaxRawEventBytes (64 KiB)
//   - command, agent_id, tool_use_id all bounded by ~PATH_MAX-class caps
//   - reject control / NUL chars in agent_id / tool_use_id (WS injection risk)
func ExtractDelegationHint(rawEvent json.RawMessage, eventName string) (DelegationHint, bool)
```

Pattern uses the token detection of §3.3 — no regex, no ReDoS risk.

### 3.5 Frame helpers

Two new helpers in `frame_ops.go`:

```go
// markDelegatingRef appends toolUseID to DelegatingToolUseIDs (deduped) and
// sets Delegating=true on the SubagentRef whose ID matches agentID, on the
// frame at (paneID, frame's PID). Idempotent — re-mark with same ToolUseID
// is a no-op (slice membership check).
//
// Race scope (M2): covers concurrent writes after the SubagentRef already
// exists. If PreToolUse arrives before SubagentStart establishes the ref
// (regression #10), this is a silent no-op — by design, since attaching a
// flag to a non-existent ref has no meaningful semantics.
func (m *Module) markDelegatingRef(paneID, agentID, toolUseID string) error

// unmarkDelegatingRef removes toolUseID from DelegatingToolUseIDs on the ref
// whose ID == agentID, recomputes Delegating = len(remaining) > 0. No-op if
// ref not found, ID not in slice, or whole frame missing (covers PostToolUse
// arriving after SubagentStop, PostToolUseFailure for non-codex Bash, etc.).
func (m *Module) unmarkDelegatingRef(paneID, agentID, toolUseID string) error
```

Both use `mutateSubagentsWithRetry` for optimistic concurrency.

### 3.6 SPA rendering

`spa/src/components/SubagentDots.tsx`:

```tsx
// Dot color: orange if either is_proxy (real cross-agent proxy via PPID
// chain) OR delegating (cc subagent inferred to be invoking codex-companion
// via Bash sniff). Both signals indicate "agent is awaiting downstream";
// they coexist without interference (delegating=true + is_proxy=true
// renders the same orange).
const NATIVE_COLOR = '#60a5fa'  // blue
const PROXY_COLOR = '#f97316'   // orange

const dotStyle = (ref: SubagentRef): CSSProperties => ({
  backgroundColor: (ref.is_proxy || ref.delegating) ? PROXY_COLOR : NATIVE_COLOR,
})
```

Test introspection: **leave `data-is-proxy` unchanged**, add new `data-delegating` attribute (F4 fix). Tests can target either independently.

`useAgentStore.ts` SubagentRef type (`spa/src/stores/useAgentStore.ts:42`) gains:
```ts
type SubagentRef = {
  // ...existing...
  delegating?: boolean
  delegating_tool_use_ids?: string[]
}
```

### 3.7 Out of scope

- **Codex-side actual lifecycle**: this spec doesn't claim to know whether codex is alive, dead, or hung. The orange dot only reflects "cc is currently inside a Bash call invoking codex-companion." If codex hangs and cc's Bash subprocess hangs with it, the dot stays orange — **this is exactly the user-actionable signal we want** (orange too long = user intervenes).
- **Replacing governance P3**: P3 still needs to land separately to clean up dead brokers and to actively detach orange dots when broker death is detected. This spec is the **visual signal half** of the safety story; P3 is the **sweep half**.
- **Real proxy attach for codex**: Op1 (codex hook payload origin extension) remains the long-term solution if/when we want to surface codex's own runtime state. This spec doesn't preclude Op1 — `IsProxy` is left untouched, so Op1 can layer on top later.
- **Detecting codex started outside the companion**: User running `codex` CLI directly in a cc Bash call without the companion wrapper is rare and unsupported here.
- **opencode delegation visibility**: opencode forwarder pattern (if any) is out of scope; spec only covers `agent_type=="cc"`.
- **Background Bash tracked through `BashOutput` polls** (L6): out of scope; if PostToolUse fires on launch (not on exit), background dispatches show only flicker. Not regressing existing behaviour.
- **`PdxPostToolBatch` parallel tool batch**: Task tool semantic, not Bash; naturally filtered by `tool_name == "Bash"` gate.

---

## 4. Acceptance criteria

| AC | Description |
|---|---|
| AC1 | `/codex:rescue` (foreground) inside cc session → cc tab shows orange dot for the duration of the codex-companion Bash call |
| AC1b | `/codex:rescue --background` → orange dot fires at PreToolUse and clears at PostToolUse within ≤1 s (lifecycle a, confirmed §2.6) — visible only as a flicker; no functional regression. |
| AC2 | `/codex:review` (foreground) and `/codex:adversarial-review` (foreground) show orange the same way |
| AC2b | `/codex:review --background` and `/codex:adversarial-review --background` follow AC1b flicker semantics |
| AC3 | `/codex:adversarial-review` running 3 parallel codex jobs (foreground) shows 3 orange dots (one per forwarder subagent), capped at SubagentDots max=3 |
| AC4 | cc subagent running non-codex Bash (`pnpm build`, `git push`, `pytest`, etc.) shows blue dot — no false orange |
| AC5 | `PostToolUse(Bash)` for the same `tool_use_id` removes from `DelegatingToolUseIDs`; if list becomes empty, `Delegating=false` → dot returns to blue |
| AC5b | `PostToolUseFailure(Bash)` for the same `tool_use_id` behaves identically to AC5 (B3 fix) |
| AC5c | Two concurrent codex Bash calls (T1, T2) from same subagent: `Delegating=true` while either is in flight, regardless of completion order. Verifies B2 fix. |
| AC6 | `SubagentStop` fires → ref removed → dot disappears (existing behaviour, unchanged) |
| AC7 | L2 turn-aware detach for real codex proxy refs (when they exist via legitimate PPID-chain attach) is unaffected — `IsProxy` invariant intact |
| AC8 | Native delegating ref + real `IsProxy` proxy ref coexist on same parent: both render orange (capped at 3 dots); detach/prune of one does not affect the other (F5 fix). Daemon test required. |
| AC9 | cc PreToolUse(Bash) at top-level (no `agent_id`, cc not inside a Task subagent) → no marking attempted |
| AC10 | opencode and codex events ignored (gated on `req.AgentType == "cc"`) |
| AC11 | PR diff total ≤ 700 lines for production code + tests, **excluding** `docs/specs/*.md` (this spec, plan, etc.). Documentation has no per-PR cap. |

---

## 5. Regression matrix

| # | Scenario | Expected dot | Notes |
|---|---|---|---|
| 1 | cc top-level (no subagent) running Bash `codex-companion task ...` | None | No agent_id → §3.2 step 2 skip |
| 2 | cc native Task subagent running Bash `pnpm build` | Blue | Token no match → no mark |
| 3 | cc native Task subagent running Bash `node "$ROOT/codex-companion.mjs" task` (canonical quoted form) | Orange | Token boundary `"` matches (B1 fix verified) |
| 4 | Scenario 3 → Bash completes → PostToolUse | Blue | tool_use_id removed from list, list now empty → Delegating=false |
| 5 | Scenario 3 → Bash exits non-zero → PostToolUseFailure | Blue | B3 fix: failure path also unmarks |
| 6 | cc Task subagent runs 2 parallel codex Bash (T1 first, T2 second), T2 completes first | Orange (still) | B2 fix: list has [T1,T2]; remove T2 leaves [T1]; Delegating still true |
| 6b | Continue 6: T1 completes | Blue | List empty → Delegating=false |
| 7 | `/codex:adversarial-review --wait` foreground with 3 parallel forwarder subagents each spawning codex | 3 orange | Each subagent has own agent_id; each ref independently marked |
| 8 | `/codex:adversarial-review --background` (3 parallel background) | 3 flickers (PreToolUse → PostToolUse within ≤1 s, lifecycle a confirmed) | L6 known limitation |
| 9 | codex running in a different pane (independent, not via cc) | Unaffected | codex hook events still go to codex frame projection; no mutation on cc |
| 10 | opencode running anything | Unaffected | `req.AgentType == "cc"` gate |
| 11 | Native delegating ref + legitimate IsProxy ref on same parent (e.g. user runs codex CLI directly in same pane while a cc subagent also delegates) | 2 orange dots | F5 / AC8: both render orange; SubagentDots renders both as separate dots (cap 3); detach paths independent |
| 12 | cc PreToolUse(Bash) with `agent_id` set but matching ref not found yet (race: SubagentStart hook in flight) | No mark (silent no-op) | M2 acknowledged: PreToolUse arriving before ref exists is a known miss; no recovery — by design (rare, recovers on next tool use) |
| 13 | L2 turn-aware sequential codex turns | Unaffected | L2 path independent — tested by existing `frame_ops_l2_test.go` suite |
| 14 | `node codex-companion.mjs.bak task` (token continuation) | Blue | Boundary check rejects |
| 15 | `cat README \| grep codex-companion.mjs` | Orange (false positive — L1) | Dot returns to blue at PostToolUse — functionally harmless |

---

## 6. Test plan

### 6.1 Unit — `delegation_extractor_test.go` (new)

Table-driven, modeled on `path_hint_extractor_test.go`:

| Case | Input | Expected |
|---|---|---|
| Bash + canonical quoted command | PreToolUse + Bash + `command="node \"$ROOT/codex-companion.mjs\" task"` + agent_id=X + tool_use_id=Y | hint{X,Y,IsCodexMark:true, IsUnmark:false}, true |
| Bash + bare path | PreToolUse + `command="node /path/codex-companion.mjs review"` | IsCodexMark:true |
| Bash + single-quoted | PreToolUse + `command="node 'codex-companion.mjs' task"` | IsCodexMark:true |
| Bash + token continuation | PreToolUse + `command="node codex-companion.mjs.bak task"` | IsCodexMark:false (boundary check) |
| Bash + non-codex command | PreToolUse + `command="pnpm build"` | IsCodexMark:false |
| Non-Bash tool (Read) | PreToolUse + Read + ... | _, false |
| No agent_id (top-level cc) | PreToolUse + Bash + canonical command + agent_id="" | _, false |
| No tool_use_id | PreToolUse + Bash + canonical command + tool_use_id="" | _, false |
| PostToolUse + codex command | PostToolUse + Bash + canonical + agent_id=X + tool_use_id=Y | hint{X,Y,IsCodexMark:false, IsUnmark:true}, true |
| PostToolUse + non-codex command | PostToolUse + Bash + `pnpm build` + agent_id=X + tool_use_id=Y | hint{X,Y,IsCodexMark:false, IsUnmark:true}, true (caller no-op on missing tool_use_id) |
| PostToolUseFailure + codex | PostToolUseFailure + Bash + canonical + agent_id=X + tool_use_id=Y | hint{X,Y,IsCodexMark:false, IsUnmark:true}, true |
| Over MaxRawEventBytes | 70KB raw | _, false |
| Invalid JSON | malformed | _, false |
| Control char in agent_id | `\x00` in agent_id | _, false (WS injection guard) |
| `echo "codex-companion.mjs"` (false positive) | command match → IsCodexMark:true | acknowledged L1 |

### 6.2 Unit — `frame_ops_test.go` additions

- `TestMarkDelegatingRef_AppendsToolUseIDOnMatchingRef`
- `TestMarkDelegatingRef_DedupesRepeatedToolUseID`
- `TestMarkDelegatingRef_NoOpWhenAgentIDNotFound` (covers M2 race)
- `TestMarkDelegatingRef_NoOpWhenFrameMissing`
- `TestUnmarkDelegatingRef_RemovesToolUseID`
- `TestUnmarkDelegatingRef_DelegatingFalseWhenListEmpties`
- `TestUnmarkDelegatingRef_DelegatingTrueWhenOthersStillActive` (B2 verification)
- `TestUnmarkDelegatingRef_NoOpWhenToolUseIDNotInList`
- `TestUnmarkDelegatingRef_NoOpAfterSubagentStop`
- `TestSubagentStopRemovesRefIncludingDelegatingFlag` (regression — existing flow unchanged)
- `TestDelegatingNativeRef_CoexistsWith_RealIsProxyRef_OnSameParent` (AC8 / F5 coverage)

### 6.3 Integration — `handler.go` end-to-end

Inject fixture cc PreToolUse(Bash codex command) → daemon round-trip → assert frame.Subagents[i].Delegating == true and tool_use_id in list. Then PostToolUse → assert removed and Delegating=false. Then SubagentStop → assert ref removed.

**Background lifecycle fixture (M1 verification)**: ✅ verified 2026-05-03 post-ship via mlab live fixture — PreToolUse fires at launch and PostToolUse fires within ≤1 s, regardless of background subprocess runtime (sleep-8 fixture: 17:12:01 → 17:12:02 → background exit 17:12:09–10). cc 1.x uses **lifecycle (a)**. Daemon unit tests retain synthetic fixtures of both shapes for forward compatibility — this keeps the implementation neutral if cc semantics change in a future release.

### 6.4 SPA — `SubagentDots.test.tsx`

Add four cases:
- `delegating=true, is_proxy=false` → orange + `data-delegating="true"` + `data-is-proxy="false"`
- `delegating=false, is_proxy=true` → orange + `data-delegating="false"` + `data-is-proxy="true"` (existing)
- `delegating=true, is_proxy=true` → orange + both attrs true
- `delegating=false, is_proxy=false` → blue (existing)

### 6.5 Regression — existing suites must pass unchanged

- `frame_ops_test.go` (full)
- `frame_ops_l2_test.go` (L2 turn-aware)
- `frame_ops_test.go::TestSubagentStart_*`
- `path_hint_extractor_test.go`
- All SPA component tests

---

## 7. Known limitations

| ID | Limitation | Mitigation |
|---|---|---|
| L1 | Token detection allows `echo "codex-companion.mjs"` and similar false positives. False negatives possible if codex plugin renames the script (`codex-companion.mjs` → `codex-runner.mjs`) or user wraps in shell substitution `$(...)` that hides the literal token. | Acceptable: false negatives only lose visibility (no functional regression); false positives clear at PostToolUse and are functionally harmless. Track upstream codex plugin script naming for breakage. |
| L2 | Fixture verification needed: confirm `PostToolUse` (and `PostToolUseFailure`) actually carry `tool_use_id` — if some path drops the field, unmark cannot match. | Implementation gate: integration test in §6.3 must capture real cc fixture. If `tool_use_id` is missing in some path, fall back to `SubagentStop`-time cleanup (existing flow already removes the ref entirely). |
| L3 | Codex actually hung (broker dead, codex_worker zombie) but cc Bash still awaits → dot stays orange "forever" until cc's own Bash timeout fires | **This is the desired signal**, not a bug. User sees orange too long → user intervenes. Pairs with cc's built-in Bash subprocess timeout as system-side backstop. |
| L4 | Manual `codex` CLI invocation outside the companion (e.g. user runs `codex exec` directly in a cc Task subagent's Bash) | Out of scope — `codex-companion.mjs` is the canonical delegation entrypoint; raw CLI invocations are an edge case to revisit only if used in practice. |
| L5 | (covered by B2 fix) — DelegatingToolUseIDs as slice handles concurrent codex Bash calls correctly regardless of completion order. | Addressed in §3.1, AC5c, regression #6/#6b. |
| L6 | Background Bash dispatches (`run_in_background: true`) show only an orange flicker — cc 1.x fires `PostToolUse` within ≤1 s of `PreToolUse` regardless of background subprocess runtime (lifecycle a, confirmed via 2026-05-03 mlab fixture; see §2.6). | Acceptable: foreground dispatches (the common case) are unaffected. If background flicker proves painful, follow-up issue can extend tracking via `BashOutput` polls keyed to original `tool_use_id`. AC1b/AC2b reflect the confirmed flicker semantics. |
| L7 | M2 race: PreToolUse(Bash codex-companion) arriving before matching SubagentStart establishes the ref → silent no-op, dot stays blue. | Acceptable: rare timing window (cc subagent first action before any tool use is unusual); recovers when subagent ends → dot goes away naturally. Non-recovery is by design. |

---

## 8. PR size estimate

| File | LOC delta |
|---|---|
| `internal/agent/subagent.go` | +8 (two fields + comments) |
| `internal/agent/cc/events.go` | +0 (PostToolUseFailure already exists) |
| `internal/module/agent/delegation_extractor.go` | +90 (new file: ExtractDelegationHint + token detection) |
| `internal/module/agent/delegation_extractor_test.go` | +180 (new file: 14 table-driven cases) |
| `internal/module/agent/frame_ops.go` | +70 (markDelegatingRef + unmarkDelegatingRef + slice helpers) |
| `internal/module/agent/frame_ops_test.go` | +130 (11 new test cases) |
| `internal/module/agent/handler.go` | +35 (wiring near :201 PathHint block — Pre + Post + PostFailure) |
| `internal/module/agent/handler_test.go` | +60 (integration: PreToolUse → mark → PostToolUse → unmark; PostToolUseFailure path; B2 concurrent) |
| `spa/src/stores/useAgentStore.ts` | +3 (type fields) |
| `spa/src/components/SubagentDots.tsx` | +4 (renderer condition + comment + data-delegating attr) |
| `spa/src/components/SubagentDots.test.tsx` | +40 (4 colour + attr cases) |
| **Production + tests** | **~620 lines** (over the AC11 cap of 500) |
| `docs/specs/2026-05-03-lights-bash-sniff-delegating-spec.md` | +600 (this file v2) |
| `docs/specs/2026-05-03-lights-bash-sniff-delegating-plan.md` | +250 (next task) |
| **Total PR** | **~1470 lines** |

Production + tests ~620 fits within AC11 cap of 700 (raised from v1's 500 to accommodate B2 slice + B3 failure-path additions). Documentation (~850 lines for spec + plan combined) is not counted toward the cap.

---

## 9. Implementation order (preview — full plan in §3 task)

1. SubagentRef +2 fields + JSON omitempty + slice util
2. delegation_extractor.go pure function with token detection + table-driven tests (TDD)
3. frame_ops.go markDelegatingRef + unmarkDelegatingRef (slice append/remove + Delegating recompute) + tests
4. handler.go wiring near existing PathHint block — three event names (Pre + Post + PostFailure) + integration tests
5. spa SubagentRef type + SubagentDots renderer + data-delegating attr + tests
6. Regression sweep: full Go test suite + spa vitest + lint + build
7. mlab live verify §6.3 background lifecycle observation; update L6 wording with real behaviour

---

## 10. Risks & open questions

| Risk | Mitigation |
|---|---|
| Anthropic CC hook payload changes break `agent_id` semantics | Pin to current schema (2026-05-03 docs); add fixture-based tests for each cc hook event. Future schema changes caught by fixture diff. |
| Codex plugin script renaming | L1 — track upstream; substring match is the reverse (any future name change is a known follow-up issue). |
| Frame mutation race at `markDelegatingRef` collides with concurrent SubagentStart on same agent_id | Use `mutateSubagentsWithRetry` (already proven for L2 turn-aware concurrency); race description in §3.5 acknowledges the M2 boundary. |
| Background Bash lifecycle (a vs b) | §6.3 fixture; AC1b/AC2b conditional; L6 explicit. No code change required regardless of which lifecycle empirically happens. |
| Slice-based DelegatingToolUseIDs grows unbounded if PostToolUse never fires | In practice cc subagent ends → SubagentStop → entire ref deleted, slice freed. Worst case bounded by `MaxConcurrentBashPerSubagent` (cc-side limit, not our concern). Add defensive cap at ~32 entries with log-and-discard if needed. |

---

## 11. Spec freeze checklist

- [x] All cross-validated facts cited (v2)
- [x] Acceptance criteria measurable (v2 — split foreground/background, added AC5b/c, AC8)
- [x] Regression matrix covers 15 scenarios (v2 — added quoted command, PostToolUseFailure, concurrent reverse-order, native+IsProxy coexistence)
- [x] Test plan covers extractor / frame helpers / handler / SPA / regression / background fixture
- [x] Known limitations enumerated (v2 — added L6, L7)
- [x] PR size cap reconciled (raised AC11 to 700 for v2 scope)
- [ ] Codex spec round 2 review with 0 blocker findings (gate before plan write)
