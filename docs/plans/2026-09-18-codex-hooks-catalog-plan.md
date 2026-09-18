# Codex Hooks Catalog Refresh (0.153.x) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Align the daemon's codex hook catalog, installer and status derivation with codex-cli 0.153.4 (retire `Notification`/`StopFailure`, install `PostToolUse`/`Interrupt`, fix the `SessionEnd` timeout and the deprecated feature flag), then freeze 0.153.4 fixtures so future drift is caught by tests.

**Architecture:** All work is in `internal/agent/codex` (catalog `events.go`, derive `status.go`, installer `hooks.go`) plus test-only touches in `internal/agent/drift_test.go` and `internal/module/agent`. The catalog (`codexEventSpecs`) is the single source of truth: the installer, `CheckHooks`, cleanup sets and `SupportedStatuses` all derive from it, so most behaviour changes are catalog edits with the installer following. PR 2 adds frozen `testdata/` fixtures mirroring the opencode 1.14.23 layout and tests that pin the catalog to them.

**Tech Stack:** Go 1.26, `github.com/BurntSushi/toml`, `go test`. No SPA changes.

**Spec:** `docs/specs/2026-09-18-codex-hooks-catalog-spec.md` (issue #1159). Executors read both.

## Global Constraints

- Two PRs: **PR 1** = Tasks 1–5 (catalog, derive, installer, module tests); **PR 2** = Tasks 6–9 (frozen fixtures + version test). PR 1 ≤ 800 lines diff; if exceeded, split feature-flag/timeout (Task 3 steps 9–12) into its own PR first.
- Installable set after PR 1 is exactly 10: `SessionStart, UserPromptSubmit, SubagentStart, SubagentStop, Stop, PermissionRequest, SessionEnd, PreToolUse, PostToolUse, Interrupt`.
- Retired upstream keys: `Notification`, `StopFailure`. They stay in the catalog as `HookHandlingIgnored` with **empty** `EmitsStatus` (existing `TestCodexEventsClassifyCurrentDocs` asserts non-installable ⇒ empty EmitsStatus, matching the opencode convention). `deriveCodexStatus` keeps its cases for them so in-flight / hand-installed payloads still parse.
- `codexHooksSupportedVersion = "0.153.4"`.
- `SessionEnd` hook timeout is 3 s; every other event stays 5 s.
- `features.hooks = true` is written; `features.codex_hooks` is deleted on install. `CheckHooks` treats absent-both as enabled; an explicit `false` on the canonical `hooks` key blocks; if `hooks` is absent, an explicit `false` on legacy `codex_hooks` blocks.
- Never touch `~/.codex/hooks.json` or `~/.codex/config.toml` while capturing fixtures (Task 6) — use a scratch project's `.codex/hooks.json`. DB reads are `mode=ro`.
- Every task ends with `gofmt -l internal/ | grep -v '^$'` printing nothing and `go test ./internal/agent/... ./internal/module/agent/` green, then one commit. Commit messages end with `Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>`.
- Run every command from the worktree root `/Users/wake/Workspace/wake/purdex/.claude/worktrees/codex-hooks-catalog` (prefix each Bash with `cd <root> && `).

### Deviation from spec (recorded for the reviewer)

1. Spec §2.5 says `SupportedStatuses` is unchanged. With `PdxStopFailure` ignored + empty `EmitsStatus`, `error` drops out of the codex `SupportedStatuses` union (`running, waiting, idle, clear` remain). `SupportedStatuses` only feeds `agent.Coverage` (test-only matrix; no runtime consumer — verified by grep). Probe-driven `error` (`onProcessDead`) does not go through `SupportedStatuses`. Task 2 pins the new set explicitly.
2. Spec §PR 2 says the 8 DB-sourced payloads come from `agent_trace_steps.payload_json`. Measured on mlab 2026-09-18: `agent_trace_steps.payload_json` is empty for codex triggers; the raw hook payload is in `agent_trace_chains.root_payload_json` → `.raw_event`. Also codex `PdxPermissionRequest` has **0** rows there (all recent sessions ran `bypassPermissions`), so `PermissionRequest` joins `PostToolUse` and `Interrupt` in the live-capture group (Task 6).

---

## File map

| File | Role | PR |
|---|---|---|
| `internal/agent/codex/events.go` | catalog `codexEventSpecs` | 1 |
| `internal/agent/codex/events_test.go` | catalog pins (installable set, upstream pin, handling, lifecycle, metadata) | 1 |
| `internal/agent/codex/status.go` | `deriveCodexStatus` cases | 1 |
| `internal/agent/codex/status_test.go` | derive tests | 1 |
| `internal/agent/codex/provider_test.go` | `SupportedStatuses` pin | 1 |
| `internal/agent/drift_test.go` | codex fixtures for three-way drift | 1 |
| `internal/agent/codex/hooks.go` | installer / checker / feature flag / timeout table / retired strip | 1 |
| `internal/agent/codex/hooks_test.go` | installer tests | 1 |
| `internal/module/agent/fakes_test.go` | `fakeDefaultEvents` gets PostToolUse + Interrupt | 1 |
| `internal/module/agent/frame_ops_l2_test.go` | two new `applyFrameEvent` cases | 1 |
| `internal/agent/codex/testdata/codex-0.153.4-*` | frozen fixtures | 2 |
| `internal/agent/codex/fixtures_test.go` (new) | frozen manifest + payload contract tests | 2 |
| `internal/agent/codex/hooks_test.go` | fake `codex --version` tests | 2 |

---

### Task 1: Catalog update (`events.go`)

**Files:**
- Modify: `internal/agent/codex/events.go`
- Test: `internal/agent/codex/events_test.go`

**Interfaces:**
- Produces: `codexEventSpecs` with 14 entries; PurdexNames `PdxInterrupt`, `PdxPreCompact`, `PdxPostCompact` new. `codexEventNames()` returns the 10 installable names in catalog order. Later tasks rely on `agent.LookupByPurdexName(p.Events(), "PdxInterrupt").Lifecycle == agent.LifecycleStop`.

- [ ] **Step 1: Rewrite the pinned expectations in `events_test.go`**

Replace the four `var` blocks at the top of the file and `TestCodexEvents_EmitsStatusForNotification`, `TestCodexEventsFutureOnlyFlags`, `expectedCodexLifecycle`, `expectedCodexPreservedMetadata`:

```go
// expectedCodexInstallableEventNames lists the hook events Purdex installs
// for Codex 0.153.x (issue #1159), keyed on PurdexName.
var expectedCodexInstallableEventNames = []string{
	"PdxSessionStart",
	"PdxUserPromptSubmit",
	"PdxSubagentStart",
	"PdxSubagentStop",
	"PdxStop",
	"PdxPermissionRequest",
	"PdxSessionEnd",
	"PdxPreToolUse",
	"PdxPostToolUse",
	"PdxInterrupt",
}

// expectedCodexEventNames is the upstream-key view of the installable set.
var expectedCodexEventNames = []string{
	"SessionStart",
	"UserPromptSubmit",
	"SubagentStart",
	"SubagentStop",
	"Stop",
	"PermissionRequest",
	"SessionEnd",
	"PreToolUse",
	"PostToolUse",
	"Interrupt",
}

// expectedCodexCurrentUpstreamEventNames is pinned to the codex hooks docs
// (https://developers.openai.com/codex/hooks), fetched 2026-09-18 for
// codex-cli 0.153.4. Exactly the 12 upstream hook events.
var expectedCodexCurrentUpstreamEventNames = []string{
	"SessionStart",
	"SessionEnd",
	"SubagentStart",
	"SubagentStop",
	"PreToolUse",
	"PermissionRequest",
	"PostToolUse",
	"PreCompact",
	"PostCompact",
	"UserPromptSubmit",
	"Stop",
	"Interrupt",
}

// expectedCodexRetiredUpstreamEventNames are keys the pre-0.153 installer
// wrote that codex never fired. They stay in the catalog as ignored so
// in-flight payloads still resolve, but are not upstream events.
var expectedCodexRetiredUpstreamEventNames = []string{
	"Notification",
	"StopFailure",
}

var expectedCodexCatalogHandling = map[string]agent.HookHandling{
	"PdxSessionStart":      agent.HookHandlingStatus,
	"PdxUserPromptSubmit":  agent.HookHandlingStatus,
	"PdxSubagentStart":     agent.HookHandlingDetail,
	"PdxSubagentStop":      agent.HookHandlingDetail,
	"PdxStop":              agent.HookHandlingStatus,
	"PdxStopFailure":       agent.HookHandlingIgnored,
	"PdxNotification":      agent.HookHandlingIgnored,
	"PdxPermissionRequest": agent.HookHandlingStatus,
	"PdxSessionEnd":        agent.HookHandlingStatus,
	"PdxPreToolUse":        agent.HookHandlingDetail,
	"PdxPostToolUse":       agent.HookHandlingStatus,
	"PdxInterrupt":         agent.HookHandlingStatus,
	"PdxPreCompact":        agent.HookHandlingIgnored,
	"PdxPostCompact":       agent.HookHandlingIgnored,
}
```

Replace `TestCodexEvents_EmitsStatusForNotification` with:

```go
// TestCodexEvents_RetiredEntriesIgnored asserts Notification / StopFailure
// remain resolvable by PurdexName but are ignored with empty EmitsStatus.
func TestCodexEvents_RetiredEntriesIgnored(t *testing.T) {
	p := codex.NewProvider()
	for _, key := range expectedCodexRetiredUpstreamEventNames {
		spec, ok := agent.LookupByUpstreamKey(p.Events(), key)
		if !ok {
			t.Fatalf("codex catalog missing retired upstream key %q", key)
		}
		if spec.PurdexName != "Pdx"+key {
			t.Errorf("retired %q PurdexName = %q, want %q", key, spec.PurdexName, "Pdx"+key)
		}
		if got := agent.EffectiveHookHandling(spec); got != agent.HookHandlingIgnored {
			t.Errorf("retired %q handling = %q, want ignored", key, got)
		}
		if len(spec.EmitsStatus) != 0 {
			t.Errorf("retired %q EmitsStatus = %v, want empty", key, spec.EmitsStatus)
		}
	}
}

// TestCodexEvents_UpstreamPinBidirectional asserts catalog upstream keys
// minus the retired set equal the pinned 0.153.4 docs list exactly, and
// that no retired key is claimed as current upstream.
func TestCodexEvents_UpstreamPinBidirectional(t *testing.T) {
	p := codex.NewProvider()
	retired := map[string]bool{}
	for _, k := range expectedCodexRetiredUpstreamEventNames {
		retired[k] = true
	}
	pinned := map[string]bool{}
	for _, k := range expectedCodexCurrentUpstreamEventNames {
		if retired[k] {
			t.Fatalf("pinned upstream list contains retired key %q", k)
		}
		pinned[k] = true
	}
	catalog := map[string]bool{}
	for _, e := range p.Events() {
		for _, k := range e.UpstreamKeys {
			if catalog[k] {
				t.Errorf("duplicate upstream key %q in codex catalog", k)
			}
			catalog[k] = true
		}
	}
	for k := range pinned {
		if !catalog[k] {
			t.Errorf("codex catalog missing pinned upstream event %q", k)
		}
	}
	for k := range catalog {
		if !pinned[k] && !retired[k] {
			t.Errorf("codex catalog upstream key %q is neither pinned nor retired", k)
		}
	}
	for k := range retired {
		if !catalog[k] {
			t.Errorf("codex catalog missing retired key %q", k)
		}
	}
}
```

Update `TestCodexEventsFutureOnlyFlags.wantFutureOnly`:

```go
	wantFutureOnly := map[string]bool{
		"PdxSessionStart":      false,
		"PdxUserPromptSubmit":  false,
		"PdxStop":              false,
		"PdxSubagentStart":     true,
		"PdxSubagentStop":      true,
		"PdxStopFailure":       false,
		"PdxNotification":      false,
		"PdxPermissionRequest": false,
		"PdxSessionEnd":        true,
		"PdxPreToolUse":        true,
		"PdxPostToolUse":       false,
		"PdxInterrupt":         false,
		"PdxPreCompact":        false,
		"PdxPostCompact":       false,
	}
```

Update `expectedCodexLifecycle` — add one line (StopFailure keeps `LifecycleStopFailure`; Notification stays `LifecycleNone`):

```go
	"PdxInterrupt": agent.LifecycleStop,
```

Update `expectedCodexPreservedMetadata`:

```go
var expectedCodexPreservedMetadata = map[string]codexLegacyMetadata{
	"PdxSessionStart":      {[]agent.Status{agent.StatusIdle}, "Codex session started", false, ""},
	"PdxUserPromptSubmit":  {[]agent.Status{agent.StatusRunning}, "User submitted a prompt", false, ""},
	"PdxSubagentStart":     {[]agent.Status{}, "Nested sub-agent task dispatched", true, ""},
	"PdxSubagentStop":      {[]agent.Status{}, "Nested sub-agent task completed", true, ""},
	"PdxStop":              {[]agent.Status{agent.StatusIdle}, "Agent finished responding and is idle", false, ""},
	"PdxStopFailure":       {[]agent.Status{}, "Retired: not a codex hook event since 0.153", false, agent.HookHandlingIgnored},
	"PdxNotification":      {[]agent.Status{}, "Retired: not a codex hook event since 0.153", false, agent.HookHandlingIgnored},
	"PdxPermissionRequest": {[]agent.Status{agent.StatusWaiting}, "Tool permission request awaiting user approval", false, ""},
	"PdxSessionEnd":        {[]agent.Status{agent.StatusClear}, "Codex session ended", true, ""},
	"PdxPreToolUse":        {[]agent.Status{}, "Tool call about to execute", true, ""},
	"PdxPostToolUse":       {[]agent.Status{agent.StatusRunning}, "Tool call completed (signals running after permission grant)", false, ""},
	"PdxInterrupt":         {[]agent.Status{agent.StatusIdle}, "Turn interrupted by the user", false, ""},
	"PdxPreCompact":        {[]agent.Status{}, "Context compaction about to start", false, agent.HookHandlingIgnored},
	"PdxPostCompact":       {[]agent.Status{}, "Context compaction completed", false, agent.HookHandlingIgnored},
}
```

Rename `TestCodexEvents_ExpandedTo9` → `TestCodexEvents_MatchesCatalogHandling` (body unchanged).

- [ ] **Step 2: Run the catalog tests to verify they fail**

Run: `go test ./internal/agent/codex/ -run 'TestCodexEvents|TestCodexEventSpecs' 2>&1 | tail -30`
Expected: FAIL — missing `PdxInterrupt`/`PdxPreCompact`/`PdxPostCompact`, handling mismatch for `PdxPostToolUse`/`PdxNotification`/`PdxStopFailure`.

- [ ] **Step 3: Edit `codexEventSpecs` in `events.go`**

Replace the `PdxStopFailure`, `PdxNotification`, `PdxPostToolUse` entries and append three entries; also replace the stale header comment:

```go
// codexEventSpecs is the declarative hook event catalog for Codex, aligned
// with codex-cli 0.153.4 (issue #1159, spec
// docs/specs/2026-09-18-codex-hooks-catalog-spec.md). The 12 upstream hook
// events are all declared; 10 are installable, PreCompact/PostCompact are
// ignored. Notification and StopFailure were written by the pre-0.153
// installer but codex never fired them; they stay as ignored entries so
// LookupByPurdexName / DeriveStatus keep resolving in-flight payloads and
// the installer can strip the stale keys.
var codexEventSpecs = []agent.HookEventSpec{
```

Entries (keep the others exactly as they are):

```go
	{
		PurdexName:   "PdxStopFailure",
		UpstreamKeys: []string{"StopFailure"},
		Lifecycle:    agent.LifecycleStopFailure,
		EmitsStatus:  []agent.Status{},
		Description:  "Retired: not a codex hook event since 0.153",
		Handling:     agent.HookHandlingIgnored,
	},
	{
		PurdexName:   "PdxNotification",
		UpstreamKeys: []string{"Notification"},
		Lifecycle:    agent.LifecycleNone,
		EmitsStatus:  []agent.Status{},
		Description:  "Retired: not a codex hook event since 0.153",
		Handling:     agent.HookHandlingIgnored,
	},
```

```go
	{
		// 0.153: PostToolUse fires after every tool call, including the
		// first one after a granted PermissionRequest, so it is the hook
		// that moves the light waiting → running (mirrors cc W6-1a).
		PurdexName:   "PdxPostToolUse",
		UpstreamKeys: []string{"PostToolUse"},
		Lifecycle:    agent.LifecycleNone,
		EmitsStatus:  []agent.Status{agent.StatusRunning},
		Description:  "Tool call completed (signals running after permission grant)",
	},
	{
		// Interrupt = the user cancelled the turn (Ctrl-C). The turn is
		// over, so it shares LifecycleStop: frame_ops detaches the codex
		// broker proxy ref by turn_id exactly like PdxStop.
		PurdexName:   "PdxInterrupt",
		UpstreamKeys: []string{"Interrupt"},
		Lifecycle:    agent.LifecycleStop,
		EmitsStatus:  []agent.Status{agent.StatusIdle},
		Description:  "Turn interrupted by the user",
	},
	{
		PurdexName:   "PdxPreCompact",
		UpstreamKeys: []string{"PreCompact"},
		Lifecycle:    agent.LifecycleNone,
		EmitsStatus:  []agent.Status{},
		Description:  "Context compaction about to start",
		Handling:     agent.HookHandlingIgnored,
	},
	{
		PurdexName:   "PdxPostCompact",
		UpstreamKeys: []string{"PostCompact"},
		Lifecycle:    agent.LifecycleNone,
		EmitsStatus:  []agent.Status{},
		Description:  "Context compaction completed",
		Handling:     agent.HookHandlingIgnored,
	},
```

- [ ] **Step 4: Run the catalog tests**

Run: `go test ./internal/agent/codex/ -run 'TestCodexEvents|TestCodexEventSpecs' 2>&1 | tail -5`
Expected: PASS. (Other codex tests — hooks/drift — will fail until Tasks 2–3; that is expected, do not fix them here.)

- [ ] **Step 5: Commit**

```bash
git add internal/agent/codex/events.go internal/agent/codex/events_test.go
git commit -m "feat(codex): align hook catalog with codex-cli 0.153.4 (#1159)

Retire Notification/StopFailure to ignored, make PostToolUse installable
(running), add Interrupt (Stop lifecycle, idle) and PreCompact/PostCompact
(ignored). Pin the 12 upstream event names bidirectionally.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---

### Task 2: Status derivation + drift fixtures

**Files:**
- Modify: `internal/agent/codex/status.go`
- Test: `internal/agent/codex/status_test.go`, `internal/agent/drift_test.go`, `internal/agent/codex/provider_test.go`

**Interfaces:**
- Consumes: catalog from Task 1.
- Produces: `deriveCodexStatus("PdxPostToolUse", raw)` → `{Valid:true, Status:running, Detail:{"tool_name": raw["tool_name"]}}`; `deriveCodexStatus("PdxInterrupt", raw)` → `{Valid:true, Status:idle, Detail: DetailStrings(raw,"turn_id")}` (empty map when absent).

- [ ] **Step 1: Write failing derive tests** — append to `status_test.go`:

```go
// TestCodexDeriveStatus_PdxPostToolUse: running + tool_name detail (#1159).
func TestCodexDeriveStatus_PdxPostToolUse(t *testing.T) {
	r := deriveWithRaw("PdxPostToolUse", `{"tool_name":"Bash","turn_id":"t1"}`)
	if !r.Valid || r.Status != agent.StatusRunning {
		t.Fatalf("expected running, got %+v", r)
	}
	if got, _ := r.Detail["tool_name"].(string); got != "Bash" {
		t.Fatalf("Detail[tool_name] = %v, want Bash", r.Detail["tool_name"])
	}
}

// TestCodexDeriveStatus_PdxInterrupt: idle + turn_id detail (#1159).
func TestCodexDeriveStatus_PdxInterrupt(t *testing.T) {
	r := deriveWithRaw("PdxInterrupt", `{"turn_id":"t9","permission_mode":"default"}`)
	if !r.Valid || r.Status != agent.StatusIdle {
		t.Fatalf("expected idle, got %+v", r)
	}
	if got, _ := r.Detail["turn_id"].(string); got != "t9" {
		t.Fatalf("Detail[turn_id] = %v, want t9", r.Detail["turn_id"])
	}
}

func TestCodexDeriveStatus_PdxInterrupt_NoTurnID(t *testing.T) {
	r := deriveViaProvider("PdxInterrupt")
	if !r.Valid || r.Status != agent.StatusIdle {
		t.Fatalf("expected idle, got %+v", r)
	}
	if _, ok := r.Detail["turn_id"]; ok {
		t.Fatalf("Detail should omit absent turn_id, got %+v", r.Detail)
	}
}

// Retired entries keep parsing so an in-flight hook from a pre-0.153
// install still resolves (spec §2.1).
func TestCodexDeriveStatus_RetiredEntriesStillParse(t *testing.T) {
	if r := deriveWithRaw("PdxNotification", `{"notification_type":"permission_prompt"}`); !r.Valid || r.Status != agent.StatusWaiting {
		t.Fatalf("PdxNotification: %+v", r)
	}
	if r := deriveWithRaw("PdxStopFailure", `{"error":"x"}`); !r.Valid || r.Status != agent.StatusError {
		t.Fatalf("PdxStopFailure: %+v", r)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/agent/codex/ -run 'TestCodexDeriveStatus_PdxPostToolUse|TestCodexDeriveStatus_PdxInterrupt' 2>&1 | tail -8`
Expected: FAIL (`Valid=false`).

- [ ] **Step 3: Add the two cases to `deriveCodexStatus` in `status.go`** (before the closing `}` of the switch):

```go
	case "PdxPostToolUse":
		// 0.153: fires after every tool call, including the first one
		// after a granted PermissionRequest — the only hook that can move
		// waiting → running (mirrors cc/status.go W6-1a).
		return agent.DeriveResult{
			Valid:  true,
			Status: agent.StatusRunning,
			Detail: map[string]any{
				"tool_name": raw["tool_name"],
			},
		}

	case "PdxInterrupt":
		// Turn cancelled by the user. The turn is over → idle; turn_id is
		// surfaced so frame_ops can detach the broker proxy ref (it reads
		// RawEvent directly, the detail is for the Inspector).
		return agent.DeriveResult{
			Valid:  true,
			Status: agent.StatusIdle,
			Detail: agent.DetailStrings(raw, "turn_id"),
		}
```

- [ ] **Step 4: Run derive tests**

Run: `go test ./internal/agent/codex/ -run 'TestCodexDeriveStatus' 2>&1 | tail -5`
Expected: PASS.

- [ ] **Step 5: Update drift fixtures** — in `internal/agent/drift_test.go` replace the `"codex"` block:

```go
	"codex": {
		{"PdxSessionStart", `{}`, agent.StatusIdle, true},
		{"PdxUserPromptSubmit", `{}`, agent.StatusRunning, true},
		{"PdxPermissionRequest", `{"tool_name":"Bash"}`, agent.StatusWaiting, true},
		{"PdxPostToolUse", `{"tool_name":"Bash"}`, agent.StatusRunning, true},
		{"PdxStop", `{}`, agent.StatusIdle, true},
		{"PdxInterrupt", `{"turn_id":"t"}`, agent.StatusIdle, true},
		{"PdxSessionEnd", `{}`, agent.StatusClear, true},
		{"PdxSubagentStart", `{"agent_id":"a"}`, "", true},
		{"PdxSubagentStop", `{"agent_id":"a"}`, "", true},
		// L2: PdxPreToolUse is detail-only (Valid=true, Status="") so the
		// new applyFrameEvent LifecycleUserPromptSubmit case can attach the
		// codex broker proxy ref for non-prompt turns (spec §3.3.C).
		{"PdxPreToolUse", `{}`, "", true},
		// PdxNotification / PdxStopFailure are retired (ignored, empty
		// EmitsStatus) since 0.153 (#1159); their parse paths are covered
		// in codex/status_test.go, not here, because the three-way drift
		// test would otherwise see error as emitted-but-undeclared.
	},
```

- [ ] **Step 6: Pin the new `SupportedStatuses` set** — in `provider_test.go` `TestCodexSupportedStatuses`, change `want` and the comment:

```go
// TestCodexSupportedStatuses asserts codex.Provider implements
// StatusSupporter and, post-#1159, declares exactly {running, waiting,
// idle, clear}: error left the union when StopFailure was retired to an
// ignored entry (codex never fired it). Probe-driven error (onProcessDead)
// is not a hook status and is unaffected.
func TestCodexSupportedStatuses(t *testing.T) {
	...
	want := map[agent.Status]bool{
		agent.StatusRunning: true,
		agent.StatusWaiting: true,
		agent.StatusIdle:    true,
		agent.StatusClear:   true,
	}
```

- [ ] **Step 7: Run the agent packages**

Run: `go test ./internal/agent/ ./internal/agent/codex/ 2>&1 | tail -20`
Expected: `internal/agent` PASS (drift green). `internal/agent/codex` still fails only in `hooks_test.go` (installer set) — Task 3 fixes those. Confirm the failing test names all start with `TestCodex.*Hooks|TestCheckHooks|TestMergeCodexHooks|TestCodexOwnedCleanup|TestCodexInstallHooks`.

- [ ] **Step 8: Commit**

```bash
git add internal/agent/codex/status.go internal/agent/codex/status_test.go internal/agent/drift_test.go internal/agent/codex/provider_test.go
git commit -m "feat(codex): derive running from PostToolUse and idle from Interrupt (#1159)

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---

### Task 3: Installer — retired keys, timeout table, feature flag, version pin

**Files:**
- Modify: `internal/agent/codex/hooks.go`
- Test: `internal/agent/codex/hooks_test.go`

**Interfaces:**
- Produces (package-private):
  - `var codexRetiredUpstreamEvents = []string{"Notification", "StopFailure"}`
  - `func codexHookTimeoutSeconds(upstreamKey string) int` — `"SessionEnd"` → 3, else 5
  - `func stripRetiredPdxCodexEntries(hooks map[string]any)` — for each retired key whose value is `[]any`, drop pdx-owned entries; delete the key when nothing remains; leave non-`[]any` values untouched
  - `codexOwnedCleanupEventNames()` additionally contains each retired key and `"Pdx"+key`
  - `setCodexHooksFeature` writes `features.hooks = true`, deletes `features.codex_hooks`
  - `codexHooksFeatureEnabled(path)` semantics per Global Constraints
  - `const codexHooksSupportedVersion = "0.153.4"`

- [ ] **Step 1: Update the shared expectations and rename the 9-event tests** in `hooks_test.go`:

```go
// expectedCodexInstallerNames is the 0.153.4 installer set (#1159).
var expectedCodexInstallerNames = []string{
	"SessionStart",
	"UserPromptSubmit",
	"SubagentStart",
	"SubagentStop",
	"Stop",
	"PermissionRequest",
	"SessionEnd",
	"PreToolUse",
	"PostToolUse",
	"Interrupt",
}
```

Rename `TestCodexInstallHooks_Writes9EventsAfterExpansion` → `TestCodexInstallHooks_Writes10Events` and change its `wantExpanded` to:

```go
	wantExpanded := []string{"SubagentStart", "SubagentStop", "PermissionRequest", "SessionEnd", "PreToolUse", "PostToolUse", "Interrupt"}
```

Also assert in the same test that retired keys are absent:

```go
	for _, retired := range codexRetiredUpstreamEvents {
		if _, ok := hooks[retired]; ok {
			t.Errorf("retired key %q written by installer", retired)
		}
	}
```

Rename `TestCodexCheckHooks_ReportsAll9Events` → `TestCodexCheckHooks_ReportsAll10Events` (body unchanged — it derives from `expectedCodexInstallerNames`).

- [ ] **Step 2: Add the timeout-table test**:

```go
func TestCodexHookTimeoutSeconds_SessionEndClampedTo3(t *testing.T) {
	if got := codexHookTimeoutSeconds("SessionEnd"); got != 3 {
		t.Fatalf("SessionEnd timeout = %d, want 3 (codex clamps SessionEnd to 3s)", got)
	}
	for _, key := range []string{"SessionStart", "Stop", "PostToolUse", "Interrupt", "Unknown"} {
		if got := codexHookTimeoutSeconds(key); got != 5 {
			t.Errorf("%s timeout = %d, want 5", key, got)
		}
	}
}

func TestCodexInstallHooks_WritesPerEventTimeout(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hooks.json")
	if err := mergeCodexHooks(path, "/usr/local/bin/pdx", false); err != nil {
		t.Fatalf("mergeCodexHooks: %v", err)
	}
	hooks := hooksSection(t, readHooksFile(t, path))
	timeoutOf := func(key string) float64 {
		groups := codexMatcherGroups(hooks[key])
		if len(groups) != 1 {
			t.Fatalf("%s: %d matcher groups, want 1", key, len(groups))
		}
		inner := toCodexEntrySlice(groups[0].(map[string]any)["hooks"])
		m, _ := inner[0].(map[string]any)
		v, _ := m["timeout"].(float64)
		return v
	}
	if got := timeoutOf("SessionEnd"); got != 3 {
		t.Errorf("SessionEnd timeout = %v, want 3", got)
	}
	if got := timeoutOf("Stop"); got != 5 {
		t.Errorf("Stop timeout = %v, want 5", got)
	}
}
```

- [ ] **Step 3: Add retired-key strip tests**:

```go
// Install strips pdx-owned entries under retired keys and drops the key
// when it empties (spec §2.3).
func TestCodexInstallHooks_StripsRetiredPdxEntries(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hooks.json")
	data, _ := json.MarshalIndent(map[string]any{
		"hooks": map[string]any{
			"Notification": []any{pdxGroupEntry("PdxNotification")},
			"StopFailure":  []any{pdxGroupEntry("PdxStopFailure")},
			"Stop":         []any{pdxGroupEntry("PdxStop")},
		},
	}, "", "  ")
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := mergeCodexHooks(path, "/usr/local/bin/pdx", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	hooks := hooksSection(t, readHooksFile(t, path))
	for _, key := range []string{"Notification", "StopFailure"} {
		if _, ok := hooks[key]; ok {
			t.Errorf("retired key %q survived install: %v", key, hooks[key])
		}
	}
	if len(hooks) != len(expectedCodexInstallerNames) {
		t.Errorf("hooks has %d keys, want %d: %v", len(hooks), len(expectedCodexInstallerNames), hooks)
	}
}

// A third-party entry under a retired key is preserved and the key kept.
func TestCodexInstallHooks_PreservesNonPdxUnderRetiredKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hooks.json")
	third := map[string]any{"hooks": []any{map[string]any{"type": "command", "command": "/opt/other/notify.sh"}}}
	data, _ := json.MarshalIndent(map[string]any{
		"hooks": map[string]any{
			"Notification": []any{pdxGroupEntry("PdxNotification"), third},
		},
	}, "", "  ")
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := mergeCodexHooks(path, "/usr/local/bin/pdx", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	hooks := hooksSection(t, readHooksFile(t, path))
	groups := codexMatcherGroups(hooks["Notification"])
	if len(groups) != 1 {
		t.Fatalf("Notification groups = %d, want 1 (third-party only): %v", len(groups), hooks["Notification"])
	}
	if findPdxCommandInCodexForEvent(hooks["Notification"], "PdxNotification") != "" {
		t.Fatal("pdx Notification entry survived install")
	}
}

// Remove strips retired pdx entries too (they are pdx-owned for cleanup).
func TestCodexRemoveHooks_StripsRetiredPdxEntries(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeHooksFile(t, home, map[string]any{
		"Notification": []any{pdxGroupEntry("PdxNotification")},
		"StopFailure":  []any{pdxGroupEntry("PdxStopFailure")},
		"Stop":         []any{pdxGroupEntry("PdxStop")},
	})
	status, err := (&Provider{}).CheckHooks()
	if err != nil {
		t.Fatalf("CheckHooks: %v", err)
	}
	if !status.Managed {
		t.Fatal("Managed=false with retired pdx entries present; Remove button would be dead")
	}
	if err := (&Provider{}).RemoveHooks("/usr/local/bin/pdx"); err != nil {
		t.Fatalf("RemoveHooks: %v", err)
	}
	hooks := hooksSection(t, readHooksFile(t, filepath.Join(home, ".codex", "hooks.json")))
	if len(hooks) != 0 {
		t.Fatalf("hooks after remove = %v, want empty", hooks)
	}
}

// A non-array value under a retired key is left alone by install.
func TestCodexInstallHooks_RetiredKeyNonArrayValuePreserved(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hooks.json")
	data, _ := json.MarshalIndent(map[string]any{
		"hooks": map[string]any{"StopFailure": "weird"},
	}, "", "  ")
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := mergeCodexHooks(path, "/usr/local/bin/pdx", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	hooks := hooksSection(t, readHooksFile(t, path))
	if hooks["StopFailure"] != "weird" {
		t.Fatalf("StopFailure = %v, want untouched \"weird\"", hooks["StopFailure"])
	}
}
```

- [ ] **Step 4: Update `TestCodexOwnedCleanupEventNames_TwoSetUnion`** so `want` also includes retired keys:

```go
	for _, key := range codexRetiredUpstreamEvents {
		want[key] = true
		want["Pdx"+key] = true
	}
```

(Insert right after the existing `for _, spec := range codexEventSpecs` loop that builds `want`; update the doc comment to say "installable UpstreamKeys ∪ PurdexName ∪ retired keys ∪ Pdx+retired".)

- [ ] **Step 5: Feature-flag tests** — replace `writeCodexFeatureFlag` and `TestCodexCheckHooks_FeatureFlagMissingOrFalseBlocks`, and update `TestCodexInstallHooks_EnablesFeatureFlagAndPreservesConfig`:

```go
// writeCodexFeatureFlag writes the canonical [features] hooks key.
func writeCodexFeatureFlag(t *testing.T, home string, enabled bool) {
	t.Helper()
	writeCodexConfigText(t, home, fmt.Sprintf("[features]\nhooks = %t\n", enabled))
}

func writeCodexConfigText(t *testing.T, home, text string) {
	t.Helper()
	path := filepath.Join(home, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("mkdir config: %v", err)
	}
	if err := os.WriteFile(path, []byte(text), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

// Absent flag = enabled (upstream default); explicit false on either key
// blocks; canonical `hooks` wins over the deprecated alias when both exist.
func TestCodexCheckHooks_FeatureFlagSemantics(t *testing.T) {
	for _, tt := range []struct {
		name        string
		config      string // "" = no config.toml
		wantBlocked bool
	}{
		{name: "absent config", config: "", wantBlocked: false},
		{name: "absent features", config: "model = \"x\"\n", wantBlocked: false},
		{name: "hooks true", config: "[features]\nhooks = true\n", wantBlocked: false},
		{name: "hooks false", config: "[features]\nhooks = false\n", wantBlocked: true},
		{name: "legacy alias true", config: "[features]\ncodex_hooks = true\n", wantBlocked: false},
		{name: "legacy alias false", config: "[features]\ncodex_hooks = false\n", wantBlocked: true},
		{name: "canonical true beats alias false", config: "[features]\nhooks = true\ncodex_hooks = false\n", wantBlocked: false},
		{name: "canonical false beats alias true", config: "[features]\nhooks = false\ncodex_hooks = true\n", wantBlocked: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			hooksPath := filepath.Join(home, ".codex", "hooks.json")
			if err := mergeCodexHooks(hooksPath, "/usr/local/bin/pdx", false); err != nil {
				t.Fatalf("seed install: %v", err)
			}
			if tt.config != "" {
				writeCodexConfigText(t, home, tt.config)
			}
			status, err := (&Provider{}).CheckHooks()
			if err != nil {
				t.Fatalf("CheckHooks: %v", err)
			}
			if status.Installed == tt.wantBlocked {
				t.Fatalf("Installed=%v, want %v (issues=%v)", status.Installed, !tt.wantBlocked, status.Issues)
			}
			if tt.wantBlocked && !issuesContain(status.Issues, "codex hooks feature flag disabled") {
				t.Fatalf("issues=%v, want feature flag disabled issue", status.Issues)
			}
			if !status.Managed {
				t.Fatal("Managed=false with valid hooks")
			}
		})
	}
}
```

In `TestCodexInstallHooks_EnablesFeatureFlagAndPreservesConfig` change the wanted substrings and add a negative assertion:

```go
	for _, want := range []string{`model = "gpt-5"`, "other = true", "hooks = true"} {
		if !strings.Contains(text, want) {
			t.Fatalf("config.toml missing %q after install:\n%s", want, text)
		}
	}
	if strings.Contains(text, "codex_hooks") {
		t.Fatalf("config.toml still contains deprecated codex_hooks after install:\n%s", text)
	}
```

- [ ] **Step 6: hooks.state round-trip test**:

```go
// The codex approval cache lives under [hooks.state."<path>:<event>:<n>:<m>"]
// with a trusted_hash; install must not lose it or the user re-approves
// every hook on the next start.
func TestCodexInstallHooks_PreservesHooksStateTrustedHash(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	stateKey := filepath.Join(home, ".codex", "hooks.json") + ":stop:0:0"
	writeCodexConfigText(t, home, "[features]\ncodex_hooks = true\n\n[hooks.state]\n\n[hooks.state.\""+stateKey+"\"]\ntrusted_hash = \"abc123\"\n")

	if err := (&Provider{}).InstallHooks("/usr/local/bin/pdx"); err != nil {
		t.Fatalf("InstallHooks: %v", err)
	}
	config, err := readCodexConfig(filepath.Join(home, ".codex", "config.toml"))
	if err != nil {
		t.Fatalf("readCodexConfig: %v", err)
	}
	hooksTbl, _ := config["hooks"].(map[string]any)
	state, _ := hooksTbl["state"].(map[string]any)
	entry, _ := state[stateKey].(map[string]any)
	if got, _ := entry["trusted_hash"].(string); got != "abc123" {
		t.Fatalf("hooks.state[%q].trusted_hash = %q, want abc123 (config=%v)", stateKey, got, config)
	}
}
```

- [ ] **Step 7: Version pin test** (PR 2 adds the fake-binary tests; here only the constant):

```go
func TestCodexHooksSupportedVersion_Pinned(t *testing.T) {
	if codexHooksSupportedVersion != "0.153.4" {
		t.Fatalf("codexHooksSupportedVersion = %q, want 0.153.4", codexHooksSupportedVersion)
	}
}
```

- [ ] **Step 8: Run to verify failures**

Run: `go test ./internal/agent/codex/ 2>&1 | grep -E '^(--- FAIL|FAIL|ok)' | head -30`
Expected: FAIL — compile errors for `codexRetiredUpstreamEvents`, `codexHookTimeoutSeconds`, `writeCodexConfigText`; then the new tests fail.

- [ ] **Step 9: Implement in `hooks.go`** — constants and table near the top:

```go
const codexHooksSupportedVersion = "0.153.4"

// codexRetiredUpstreamEvents are hooks.json keys the pre-0.153 installer
// wrote that codex never fires (#1159). Install and remove strip pdx-owned
// entries under them and drop the key when it empties; third-party entries
// are left alone.
var codexRetiredUpstreamEvents = []string{"Notification", "StopFailure"}

// codexHookTimeouts is the per-event hook timeout in seconds. codex clamps
// SessionEnd to 3 s and warns at every start if the file says more.
var codexHookTimeouts = map[string]int{
	"SessionEnd": 3,
}

const codexHookDefaultTimeout = 5

func codexHookTimeoutSeconds(upstreamKey string) int {
	if t, ok := codexHookTimeouts[upstreamKey]; ok {
		return t
	}
	return codexHookDefaultTimeout
}
```

In `mergeCodexHooksFile`, the install branch: call `stripRetiredPdxCodexEntries(hooks)` right before the `for _, spec := range codexEventSpecs` loop, and use the table for the timeout:

```go
	stripRetiredPdxCodexEntries(hooks)
	for _, spec := range codexEventSpecs {
		...
		entries = append(entries, map[string]any{
			"hooks": []any{
				map[string]any{
					"type":    "command",
					"command": fmt.Sprintf(`"%s" hook --agent codex %s`, pdxPath, spec.PurdexName),
					"timeout": codexHookTimeoutSeconds(key),
				},
			},
		})
```

New helper (place after `mergeCodexHooksFile`):

```go
// stripRetiredPdxCodexEntries removes pdx-owned entries under retired keys
// and deletes the key when nothing else lives there. Non-array values are
// not ours to interpret and are preserved as-is. The remove path does not
// call this: its all-keys loop already strips them because
// codexOwnedCleanupEventNames includes the retired names.
func stripRetiredPdxCodexEntries(hooks map[string]any) {
	for _, key := range codexRetiredUpstreamEvents {
		existing, ok := hooks[key]
		if !ok {
			continue
		}
		if _, isArr := existing.([]any); !isArr {
			continue
		}
		entries := filterOutPdxCodexKnownEvents(existing)
		if len(entries) == 0 {
			delete(hooks, key)
		} else {
			hooks[key] = entries
		}
	}
}
```

Extend `codexOwnedCleanupEventNames` (after the loop):

```go
	for _, key := range codexRetiredUpstreamEvents {
		owned[key] = true
		owned["Pdx"+key] = true
	}
	return owned
```

and update its doc comment: "…∪ retired upstream keys ∪ Pdx+retired (cleanup only; never installed)".

Feature flag:

```go
// setCodexHooksFeature enables the canonical [features].hooks flag and
// drops the deprecated codex_hooks alias (codex 0.153 warns on it at
// every start).
func setCodexHooksFeature(config map[string]any) {
	features, _ := config["features"].(map[string]any)
	if features == nil {
		features = make(map[string]any)
	}
	features["hooks"] = true
	delete(features, "codex_hooks")
	config["features"] = features
}

// codexHooksFeatureEnabled reports whether codex will run hooks. Upstream
// default is enabled, so absent flags mean true. The canonical `hooks` key
// wins; the deprecated `codex_hooks` alias is consulted only when the
// canonical key is absent.
func codexHooksFeatureEnabled(path string) (bool, error) {
	config, err := readCodexConfig(path)
	if err != nil {
		return false, fmt.Errorf("parse %s: %w", path, err)
	}
	features, _ := config["features"].(map[string]any)
	if features == nil {
		return true, nil
	}
	if v, ok := features["hooks"].(bool); ok {
		return v, nil
	}
	if v, ok := features["codex_hooks"].(bool); ok {
		return v, nil
	}
	return true, nil
}
```

In `CheckHooks` change the issue text to `"codex hooks feature flag disabled; run install to enable features.hooks"`.

- [ ] **Step 10: Run the codex package**

Run: `go test ./internal/agent/codex/ 2>&1 | tail -15`
Expected: PASS. If `TestCodexInstallHooks_PreservesHooksStateTrustedHash` fails on the key lookup, print `config` — BurntSushi decodes dotted-quoted table names into nested maps only along unquoted dots; the quoted key `"<path>:stop:0:0"` must appear as a single map key under `state`. Do not change the writer; fix only the test's lookup if the decoded shape differs, and record the actual shape in the test comment.

- [ ] **Step 11: Whole-tree check**

Run: `gofmt -l internal/ ; go vet ./internal/agent/... && go test ./internal/agent/... ./internal/module/agent/ 2>&1 | tail -10`
Expected: gofmt prints nothing; all `ok`.

- [ ] **Step 12: Commit**

```bash
git add internal/agent/codex/hooks.go internal/agent/codex/hooks_test.go
git commit -m "feat(codex): installer strips retired hooks, clamps SessionEnd, writes features.hooks (#1159)

- retire Notification/StopFailure keys on install and remove
- SessionEnd timeout 3s via per-event table
- write features.hooks=true, drop deprecated codex_hooks; absent = enabled
- pin codexHooksSupportedVersion to 0.153.4
- prove [hooks.state] trusted_hash survives install

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---

### Task 4: Module-side regression cases (no production change expected)

**Files:**
- Modify: `internal/module/agent/fakes_test.go` (`fakeDefaultEvents`)
- Test: `internal/module/agent/frame_ops_l2_test.go`

**Interfaces:**
- Consumes: `applyFrameEvent(req EventRequest, result agentpkg.DeriveResult, broadcastTs int64)`, helpers `newProxyTestModule`, `seedFrame`, `seedProxyRef`, `turnAwareEnvAlive`, `rawTurn` (all exist in the test package).

- [ ] **Step 1: Add the two events to `fakeDefaultEvents`** (after the `PdxStop` line):

```go
	// #1159: codex 0.153 catalog. PostToolUse is a plain status event;
	// Interrupt shares LifecycleStop so the codex turn-aware detach runs.
	{PurdexName: "PdxPostToolUse", UpstreamKeys: []string{"PostToolUse"}, Lifecycle: agentpkg.LifecycleNone},
	{PurdexName: "PdxInterrupt", UpstreamKeys: []string{"Interrupt"}, Lifecycle: agentpkg.LifecycleStop},
```

- [ ] **Step 2: Write the two cases** — append to `frame_ops_l2_test.go`:

```go
// #1159 (a): PostToolUse on a codex frame parked at waiting (after a
// PermissionRequest) moves it to running via the generic narrow update.
func TestApplyFrameEvent_CodexPostToolUse_WaitingToRunning(t *testing.T) {
	m := newProxyTestModule(t)
	frame := seedFrame(t, m, "%5", "codex", 42, "t1", 50)
	frame.Status = agentpkg.StatusWaiting
	if _, err := m.frames.Upsert(frame); err != nil {
		t.Fatalf("park frame at waiting: %v", err)
	}
	turnAwareEnvAlive(t, 1, "t-init")

	req := EventRequest{
		TmuxSession: "work", TmuxPaneID: "%5",
		PurdexName: "PdxPostToolUse",
		AgentType:  "codex", SenderPID: 42, SenderStartTime: "t1",
		RawEvent: json.RawMessage(`{"tool_name":"Bash","turn_id":"t_a"}`),
	}
	_, meta, err := m.applyFrameEvent(req, agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusRunning, Detail: map[string]any{"tool_name": "Bash"}}, 200)
	if err != nil {
		t.Fatalf("applyFrameEvent: %v", err)
	}
	if meta.Decision != "updated_frame" {
		t.Fatalf("decision = %q, want updated_frame (meta=%+v)", meta.Decision, meta)
	}
	final, err := m.frames.GetByIdentity("%5", 42, "t1")
	if err != nil || final == nil {
		t.Fatalf("reload: %v / %v", err, final)
	}
	if final.Status != agentpkg.StatusRunning {
		t.Fatalf("status = %q, want running", final.Status)
	}
}

// #1159 (b): Interrupt from a codex broker detaches its proxy ref by
// turn_id exactly like Stop (LifecycleStop path), and on a standalone
// codex frame moves running → idle.
func TestApplyFrameEvent_CodexInterrupt_DetachesProxyByTurn(t *testing.T) {
	m := newProxyTestModule(t)
	seedProxyRef(t, m, "%5", "cc", 100, "t100", 50, []agentpkg.SubagentRef{{
		ID: "proxy:codex:42:t1", Type: "codex", StartedAt: 50,
		SourcePID: 42, SourceStartTime: "t1", IsProxy: true, SourceTurnID: "t_a",
	}})
	turnAwareEnvAlive(t, 100, "t100")

	req := EventRequest{
		TmuxSession: "work", TmuxPaneID: "%5",
		PurdexName: "PdxInterrupt",
		AgentType:  "codex", SenderPID: 42, SenderStartTime: "t1",
		RawEvent: rawTurn("t_a"),
	}
	_, meta, err := m.applyFrameEvent(req, agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusIdle}, 200)
	if err != nil {
		t.Fatalf("applyFrameEvent: %v", err)
	}
	if meta.Reason != "proxy_subagent_detached_on_stop_turn" {
		t.Fatalf("reason = %q, want proxy_subagent_detached_on_stop_turn; meta=%+v", meta.Reason, meta)
	}
	final, _ := m.frames.GetByIdentity("%5", 100, "t100")
	if final == nil || len(final.Subagents) != 0 {
		t.Fatalf("Subagents = %+v, want empty after Interrupt detach", final.Subagents)
	}
}

func TestApplyFrameEvent_CodexInterrupt_StandaloneRunningToIdle(t *testing.T) {
	m := newProxyTestModule(t)
	frame := seedFrame(t, m, "%5", "codex", 42, "t1", 50)
	frame.Status = agentpkg.StatusRunning
	if _, err := m.frames.Upsert(frame); err != nil {
		t.Fatalf("set running: %v", err)
	}
	turnAwareEnvAlive(t, 1, "t-init")

	req := EventRequest{
		TmuxSession: "work", TmuxPaneID: "%5",
		PurdexName: "PdxInterrupt",
		AgentType:  "codex", SenderPID: 42, SenderStartTime: "t1",
		RawEvent: rawTurn("t_a"),
	}
	_, meta, err := m.applyFrameEvent(req, agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusIdle}, 200)
	if err != nil {
		t.Fatalf("applyFrameEvent: %v", err)
	}
	if meta.Decision != "updated_frame" {
		t.Fatalf("decision = %q, want updated_frame (meta=%+v)", meta.Decision, meta)
	}
	final, _ := m.frames.GetByIdentity("%5", 42, "t1")
	if final == nil || final.Status != agentpkg.StatusIdle {
		t.Fatalf("frame = %+v, want idle", final)
	}
}
```

Ensure `encoding/json` is imported in `frame_ops_l2_test.go` (it already is — `rawTurn` uses `json.RawMessage`).

- [ ] **Step 3: Run**

Run: `go test ./internal/module/agent/ -run 'TestApplyFrameEvent_Codex(PostToolUse|Interrupt)' -v 2>&1 | tail -15`
Expected: PASS on first run (the paths already exist). If (a) returns `created_frame` or the status stays waiting, the seeded frame identity does not match the request — check `seedFrame` args (`pid`, `startTime`) equal `SenderPID`/`SenderStartTime`. Do not modify `frame_ops.go`; if a production change seems required, stop and report (spec §2.4 says none is expected).

- [ ] **Step 4: Full module package + commit**

Run: `go test ./internal/module/agent/ 2>&1 | tail -3`
Expected: `ok`.

```bash
git add internal/module/agent/fakes_test.go internal/module/agent/frame_ops_l2_test.go
git commit -m "test(module/agent): cover codex PostToolUse waiting→running and Interrupt detach (#1159)

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---

### Task 5: PR 1 wrap-up

- [ ] **Step 1: Size guard**

Run: `git diff --stat a171cd7a..HEAD -- internal/ | tail -1`
Expected: ≤ 800 lines, ≤ 20 files. If over, split Task 3 steps 9–12 (feature flag + timeout) into a separate PR first.

- [ ] **Step 2: Full Go suite**

Run: `go build ./... && go test ./... 2>&1 | grep -v '^ok' | head -20`
Expected: nothing but `no test files` lines.

- [ ] **Step 3: Push + PR** (main session does this; subagents stop after Step 2)

```bash
git push -u origin worktree-codex-hooks-catalog
gh pr create --title "feat(codex): hooks catalog refresh for codex-cli 0.153.4 (#1159, PR 1/2)" --body-file - <<'EOF'
Spec: docs/specs/2026-09-18-codex-hooks-catalog-spec.md
Plan: docs/plans/2026-09-18-codex-hooks-catalog-plan.md (Tasks 1–5)

- Catalog: retire Notification/StopFailure (ignored), install PostToolUse + Interrupt, declare PreCompact/PostCompact (ignored); pin 12 upstream names bidirectionally
- DeriveStatus: PostToolUse → running, Interrupt → idle (+turn_id)
- Installer: strip retired keys on install/remove; SessionEnd timeout 3s; features.hooks=true and drop codex_hooks; absent flag = enabled; version pin 0.153.4; hooks.state trusted_hash round-trip test
- Module: two regression cases, no production change

Deviation from spec: codex SupportedStatuses drops `error` (test-only consumer). See plan "Deviation from spec".

Closes nothing yet — #1159 closes with PR 2.
EOF
```

---

### Task 6: Capture 0.153.4 payload fixtures (PR 2)

**Files:**
- Create: `internal/agent/codex/testdata/codex-0.153.4-payloads/<PurdexName>.json` (10 files)
- Create: `internal/agent/codex/testdata/codex-0.153.4-source.md`
- Create: `internal/agent/codex/testdata/codex-0.153.4-version.txt` (content `0.153.4\n`)

**Interfaces:**
- Produces: one JSON object per installable PurdexName, being the raw codex hook stdin payload (what `raw_event` holds), scrubbed.

**Scrub rules** (apply to every fixture; keep every key, replace values):
- `session_id` → `"01a00000-0000-7000-8000-000000000001"`, `turn_id` → `"01a00000-0000-7000-8000-000000000002"`, `agent_id` → `"01a00000-0000-7000-8000-000000000003"`
- `transcript_path` → `"/Users/example/.codex/sessions/2026/09/18/rollout-example.jsonl"`, `cwd` → `"/Users/example/project"`
- `tool_input` → `{"command":"echo hi"}`; `tool_response`/`tool_output` (PostToolUse) → `{"stdout":"hi\n","exit_code":0}` keeping whatever top-level key name codex used
- `last_assistant_message` → `"ok"`, `prompt` → `"say hi"`, `model` → keep, `permission_mode` → keep, `reason`/`source`/`stop_hook_active`/`hook_event_name` → keep

- [ ] **Step 1: Pull the 7 DB-available payloads (read-only)**

```bash
mkdir -p /private/tmp/claude-501/-Users-wake-Workspace-wake-purdex/c3081413-b2f9-417d-9556-bfba81435e92/scratchpad/codex-fixtures
for ev in PdxSessionStart PdxUserPromptSubmit PdxPreToolUse PdxStop PdxSessionEnd PdxSubagentStart PdxSubagentStop; do
  sqlite3 "file:/Users/wake/.config/pdx/agent_events.db?mode=ro" \
    "select json_extract(root_payload_json,'$.raw_event') from agent_trace_chains where root_agent_type='codex' and root_event_name='$ev' order by started_at desc limit 1" \
    > "/private/tmp/claude-501/-Users-wake-Workspace-wake-purdex/c3081413-b2f9-417d-9556-bfba81435e92/scratchpad/codex-fixtures/$ev.raw.json"
done
```

Then, for each raw file, print only its key list (`jq 'keys'`) — never `cat` the whole raw file into the transcript (it contains prompts and paths).

- [ ] **Step 2: Live-capture `PermissionRequest`, `PostToolUse`, `Interrupt`**

```bash
S=/private/tmp/claude-501/-Users-wake-Workspace-wake-purdex/c3081413-b2f9-417d-9556-bfba81435e92/scratchpad/codex-capture
mkdir -p "$S/.codex" && cd "$S" && git init -q .
cat > "$S/.codex/hooks.json" <<'EOF'
{
  "hooks": {
    "PermissionRequest": [{"hooks": [{"type": "command", "command": "cat >> /private/tmp/claude-501/-Users-wake-Workspace-wake-purdex/c3081413-b2f9-417d-9556-bfba81435e92/scratchpad/codex-capture/PermissionRequest.log", "timeout": 5}]}],
    "PostToolUse":       [{"hooks": [{"type": "command", "command": "cat >> /private/tmp/claude-501/-Users-wake-Workspace-wake-purdex/c3081413-b2f9-417d-9556-bfba81435e92/scratchpad/codex-capture/PostToolUse.log", "timeout": 5}]}],
    "Interrupt":         [{"hooks": [{"type": "command", "command": "cat >> /private/tmp/claude-501/-Users-wake-Workspace-wake-purdex/c3081413-b2f9-417d-9556-bfba81435e92/scratchpad/codex-capture/Interrupt.log", "timeout": 5}]}]
  }
}
EOF
```

Run codex interactively in a tmux window from `$S` (main session drives this; the user's `~/.codex` is untouched because project-level hooks live in `$S/.codex/hooks.json`):

```bash
tmux new-window -d -n codex-capture -c "$S" "codex --ask-for-approval untrusted 'run: ls -la'"
# approve at /hooks when prompted (first run of project hooks needs approval), approve the shell command
# → PermissionRequest.log + PostToolUse.log
# then send a long prompt ("count to 200 slowly, one per line") and press Ctrl-C mid-turn → Interrupt.log
```

Each log holds one JSON object per line. Take the first line of each into `<PurdexName>.raw.json` in the fixtures scratch dir.

If `--ask-for-approval untrusted` does not exist in 0.153.4, use `codex --help` to find the flag that forces prompting (`-a`/`--ask-for-approval` with `untrusted` or `on-request`).

- [ ] **Step 3: Scrub and write fixtures**

Write a jq scrub filter to `$scratch/scrub.jq`:

```jq
def scrub:
  with_entries(
    if .key == "session_id" then .value = "01a00000-0000-7000-8000-000000000001"
    elif .key == "turn_id" then .value = "01a00000-0000-7000-8000-000000000002"
    elif .key == "agent_id" then .value = "01a00000-0000-7000-8000-000000000003"
    elif .key == "transcript_path" then .value = "/Users/example/.codex/sessions/2026/09/18/rollout-example.jsonl"
    elif .key == "cwd" then .value = "/Users/example/project"
    elif .key == "tool_input" then .value = {"command":"echo hi"}
    elif (.key == "tool_response" or .key == "tool_output") then .value = {"stdout":"hi\n","exit_code":0}
    elif .key == "last_assistant_message" then .value = "ok"
    elif .key == "prompt" then .value = "say hi"
    else . end);
scrub
```

```bash
OUT=internal/agent/codex/testdata/codex-0.153.4-payloads; mkdir -p "$OUT"
for f in <scratch>/codex-fixtures/*.raw.json; do n=$(basename "$f" .raw.json); jq -S -f <scratch>/scrub.jq "$f" > "$OUT/$n.json"; done
printf '0.153.4\n' > internal/agent/codex/testdata/codex-0.153.4-version.txt
grep -rl '/Users/wake' "$OUT" && echo "LEAK" || echo "clean"
```

Expected: `clean`; 10 files.

- [ ] **Step 4: Write `codex-0.153.4-source.md`** — record: codex-cli version (`codex --version` → `codex-cli 0.153.4`), docs URL + fetch date, the DB query (Step 1) with the note that payloads live in `agent_trace_chains.root_payload_json.raw_event`, the live-capture procedure (Step 2, including that `~/.codex` was never modified), the scrub rules table, and a fixture-by-fixture table with columns `Fixture | Class (runtime-trace) | Source (db / live) | Keys DeriveStatus reads`.

- [ ] **Step 5: Commit**

```bash
git add internal/agent/codex/testdata/
git commit -m "test(codex): freeze codex-cli 0.153.4 hook payload fixtures (#1159)

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---

### Task 7: Frozen manifest + events.json + classification test

**Files:**
- Create: `internal/agent/codex/testdata/codex-0.153.4-manifest.json`, `codex-0.153.4-events.json`
- Create: `internal/agent/codex/fixtures_test.go`

**Interfaces:**
- Produces: `loadCodexFrozenEvents(t) codexFrozenEvents`, `loadCodexFrozenManifest(t) codexFrozenManifest` (test helpers used by Task 8).

- [ ] **Step 1: Write the test first** — `fixtures_test.go`:

```go
package codex_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/agent/codex"
)

const codexFrozenVersion = "0.153.4"

type codexFrozenManifest struct {
	Tag               string `json:"tag"`
	CommitSha         string `json:"commitSha"`
	Version           string `json:"version"`
	SchemaStage       string `json:"schemaStage"`
	PayloadFixtureDir string `json:"payloadFixtureDir"`
	CatalogSummary    struct {
		Installable int `json:"installable"`
		Ignored     int `json:"ignored"`
		Unsupported int `json:"unsupported"`
	} `json:"catalogSummary"`
}

type codexFrozenEvents struct {
	Version string `json:"version"`
	Events  []struct {
		UpstreamKey string `json:"upstreamKey"`
		Purdex      struct {
			Kind            string `json:"kind"` // installable | ignored | retired
			PurdexEventName string `json:"purdexEventName"`
			Status          string `json:"status,omitempty"` // expected DeriveStatus status for the payload fixture ("" = detail-only)
		} `json:"purdex"`
	} `json:"events"`
}

func loadCodexFrozenManifest(t *testing.T) codexFrozenManifest {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "codex-"+codexFrozenVersion+"-manifest.json"))
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	var m codexFrozenManifest
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("parse manifest: %v", err)
	}
	return m
}

func loadCodexFrozenEvents(t *testing.T) codexFrozenEvents {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "codex-"+codexFrozenVersion+"-events.json"))
	if err != nil {
		t.Fatalf("load events.json: %v", err)
	}
	var e codexFrozenEvents
	if err := json.Unmarshal(raw, &e); err != nil {
		t.Fatalf("parse events.json: %v", err)
	}
	return e
}

// TestCodexEvents_ClassifyAgainstFrozenManifest: catalog ⇔ events.json,
// bidirectional. kind=installable ⇒ Handling status/detail; kind=ignored
// or retired ⇒ Handling ignored. Every catalog entry must appear exactly
// once in events.json and vice versa.
func TestCodexEvents_ClassifyAgainstFrozenManifest(t *testing.T) {
	specs := codex.NewProvider().Events()
	byUpstream := map[string]agent.HookEventSpec{}
	for _, s := range specs {
		for _, k := range s.UpstreamKeys {
			byUpstream[k] = s
		}
	}
	frozen := loadCodexFrozenEvents(t)
	if frozen.Version != codexFrozenVersion {
		t.Fatalf("events.json version = %q, want %q", frozen.Version, codexFrozenVersion)
	}
	seen := map[string]int{}
	counts := map[string]int{}
	for _, e := range frozen.Events {
		spec, ok := byUpstream[e.UpstreamKey]
		if !ok {
			t.Errorf("events.json[%s] has no catalog entry", e.UpstreamKey)
			continue
		}
		seen[spec.PurdexName]++
		counts[e.Purdex.Kind]++
		if spec.PurdexName != e.Purdex.PurdexEventName {
			t.Errorf("events.json[%s] purdexEventName=%q, catalog=%q", e.UpstreamKey, e.Purdex.PurdexEventName, spec.PurdexName)
		}
		h := agent.EffectiveHookHandling(spec)
		switch e.Purdex.Kind {
		case "installable":
			if h != agent.HookHandlingStatus && h != agent.HookHandlingDetail {
				t.Errorf("events.json[%s] kind=installable but catalog handling=%q", e.UpstreamKey, h)
			}
		case "ignored", "retired":
			if h != agent.HookHandlingIgnored {
				t.Errorf("events.json[%s] kind=%s but catalog handling=%q", e.UpstreamKey, e.Purdex.Kind, h)
			}
		default:
			t.Errorf("events.json[%s] unknown kind %q", e.UpstreamKey, e.Purdex.Kind)
		}
	}
	for _, s := range specs {
		if seen[s.PurdexName] != 1 {
			t.Errorf("catalog %q appears %d times in events.json, want 1", s.PurdexName, seen[s.PurdexName])
		}
	}
	m := loadCodexFrozenManifest(t)
	if m.Version != codexFrozenVersion || m.Tag != "rust-v"+codexFrozenVersion {
		t.Errorf("manifest version/tag = %q/%q", m.Version, m.Tag)
	}
	if m.CatalogSummary.Installable != counts["installable"] {
		t.Errorf("manifest installable=%d, events.json=%d", m.CatalogSummary.Installable, counts["installable"])
	}
	if m.CatalogSummary.Ignored != counts["ignored"]+counts["retired"] {
		t.Errorf("manifest ignored=%d, events.json ignored+retired=%d", m.CatalogSummary.Ignored, counts["ignored"]+counts["retired"])
	}
	if m.CatalogSummary.Unsupported != 0 {
		t.Errorf("manifest unsupported=%d, want 0", m.CatalogSummary.Unsupported)
	}
	if m.PayloadFixtureDir != "internal/agent/codex/testdata/codex-"+codexFrozenVersion+"-payloads/" {
		t.Errorf("manifest payloadFixtureDir = %q", m.PayloadFixtureDir)
	}
	ver, err := os.ReadFile(filepath.Join("testdata", "codex-"+codexFrozenVersion+"-version.txt"))
	if err != nil || string(ver) != codexFrozenVersion+"\n" {
		t.Errorf("version.txt = %q / %v", ver, err)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/agent/codex/ -run TestCodexEvents_ClassifyAgainstFrozenManifest 2>&1 | tail -5`
Expected: FAIL — `load events.json: no such file`.

- [ ] **Step 3: Write `codex-0.153.4-events.json`** (14 entries):

```json
{
  "version": "0.153.4",
  "tag": "rust-v0.153.4",
  "sourceDocs": "https://developers.openai.com/codex/hooks",
  "fetchedAt": "2026-09-18",
  "events": [
    {"upstreamKey": "SessionStart",      "purdex": {"kind": "installable", "purdexEventName": "PdxSessionStart",      "status": "idle"}},
    {"upstreamKey": "SessionEnd",        "purdex": {"kind": "installable", "purdexEventName": "PdxSessionEnd",        "status": "clear"}},
    {"upstreamKey": "SubagentStart",     "purdex": {"kind": "installable", "purdexEventName": "PdxSubagentStart"}},
    {"upstreamKey": "SubagentStop",      "purdex": {"kind": "installable", "purdexEventName": "PdxSubagentStop"}},
    {"upstreamKey": "PreToolUse",        "purdex": {"kind": "installable", "purdexEventName": "PdxPreToolUse"}},
    {"upstreamKey": "PermissionRequest", "purdex": {"kind": "installable", "purdexEventName": "PdxPermissionRequest", "status": "waiting"}},
    {"upstreamKey": "PostToolUse",       "purdex": {"kind": "installable", "purdexEventName": "PdxPostToolUse",       "status": "running"}},
    {"upstreamKey": "PreCompact",        "purdex": {"kind": "ignored",     "purdexEventName": "PdxPreCompact"}},
    {"upstreamKey": "PostCompact",       "purdex": {"kind": "ignored",     "purdexEventName": "PdxPostCompact"}},
    {"upstreamKey": "UserPromptSubmit",  "purdex": {"kind": "installable", "purdexEventName": "PdxUserPromptSubmit",  "status": "running"}},
    {"upstreamKey": "Stop",              "purdex": {"kind": "installable", "purdexEventName": "PdxStop",              "status": "idle"}},
    {"upstreamKey": "Interrupt",         "purdex": {"kind": "installable", "purdexEventName": "PdxInterrupt",         "status": "idle"}},
    {"upstreamKey": "Notification",      "purdex": {"kind": "retired",     "purdexEventName": "PdxNotification",      "reason": "never fired by codex; written by pre-0.153 installer"}},
    {"upstreamKey": "StopFailure",       "purdex": {"kind": "retired",     "purdexEventName": "PdxStopFailure",       "reason": "never fired by codex; written by pre-0.153 installer"}}
  ]
}
```

(Status values are the `agent.Status` strings: check `internal/agent/status.go` constants — `running`, `waiting`, `idle`, `clear` — and use exactly those.)

- [ ] **Step 4: Write `codex-0.153.4-manifest.json`**:

```json
{
  "tag": "rust-v0.153.4",
  "commitSha": "n/a",
  "commitShaNote": "codex-cli is consumed as the npm @openai/codex binary; the hook surface is pinned to the published docs + live capture, not to a source commit",
  "version": "0.153.4",
  "normalizedVersion": "0.153.4",
  "auditedAt": "2026-09-18",
  "sourceDocs": "https://developers.openai.com/codex/hooks",
  "schemaStage": "full",
  "payloadFixtureDir": "internal/agent/codex/testdata/codex-0.153.4-payloads/",
  "catalogSummary": {
    "upstreamEvents": 12,
    "installable": 10,
    "ignored": 4,
    "unsupported": 0
  }
}
```

(`ignored` = 2 ignored + 2 retired.)

- [ ] **Step 5: Run**

Run: `go test ./internal/agent/codex/ -run TestCodexEvents_ClassifyAgainstFrozenManifest 2>&1 | tail -5`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/agent/codex/testdata/codex-0.153.4-manifest.json internal/agent/codex/testdata/codex-0.153.4-events.json internal/agent/codex/fixtures_test.go
git commit -m "test(codex): pin catalog to frozen 0.153.4 manifest (#1159)

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---

### Task 8: Payload contract test

**Files:**
- Modify: `internal/agent/codex/fixtures_test.go`

**Interfaces:**
- Consumes: fixtures from Task 6, `events.json` `status` field from Task 7.

- [ ] **Step 1: Append the test**

```go
// TestCodexPayloadFixtures_DeriveStatusContract runs every installable
// fixture through DeriveStatus and asserts Valid plus the status pinned in
// events.json. Also asserts a fixture exists for every installable entry
// and that no fixture leaks a real home path.
func TestCodexPayloadFixtures_DeriveStatusContract(t *testing.T) {
	p := codex.NewProvider()
	frozen := loadCodexFrozenEvents(t)
	dir := filepath.Join("testdata", "codex-"+codexFrozenVersion+"-payloads")
	for _, e := range frozen.Events {
		if e.Purdex.Kind != "installable" {
			continue
		}
		e := e
		t.Run(e.Purdex.PurdexEventName, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join(dir, e.Purdex.PurdexEventName+".json"))
			if err != nil {
				t.Fatalf("missing payload fixture: %v", err)
			}
			if bytesContain(raw, "/Users/wake") {
				t.Fatalf("fixture leaks a real home path")
			}
			var probe map[string]any
			if err := json.Unmarshal(raw, &probe); err != nil {
				t.Fatalf("fixture is not a JSON object: %v", err)
			}
			if got, _ := probe["hook_event_name"].(string); got != e.UpstreamKey {
				t.Fatalf("fixture hook_event_name = %q, want %q", got, e.UpstreamKey)
			}
			r := p.DeriveStatus(e.Purdex.PurdexEventName, json.RawMessage(raw))
			if !r.Valid {
				t.Fatalf("DeriveStatus Valid=false: %+v", r)
			}
			if string(r.Status) != e.Purdex.Status {
				t.Fatalf("DeriveStatus status = %q, want %q", r.Status, e.Purdex.Status)
			}
		})
	}
}

func bytesContain(b []byte, s string) bool {
	return len(s) > 0 && len(b) >= len(s) && strings.Contains(string(b), s)
}
```

Add `"strings"` to the imports.

- [ ] **Step 2: Run**

Run: `go test ./internal/agent/codex/ -run TestCodexPayloadFixtures_DeriveStatusContract -v 2>&1 | tail -15`
Expected: PASS for all 10 sub-tests. A failure on `hook_event_name` means a fixture was written to the wrong file name; a `Valid=false` means the fixture name is not a catalog PurdexName.

- [ ] **Step 3: Commit**

```bash
git add internal/agent/codex/fixtures_test.go
git commit -m "test(codex): payload contract over frozen 0.153.4 fixtures (#1159)

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---

### Task 9: CheckHooks version test with a fake `codex` on PATH

**Files:**
- Modify: `internal/agent/codex/hooks_test.go`

**Interfaces:**
- Consumes: `agent.ResetHookAgentVersionCache()`, `agent.DetectHookAgentVersion("codex", "--version")` (already used by `CheckHooks`).

- [ ] **Step 1: Write the test** (mirror `fakeOpenCodeVersion`):

```go
func fakeCodexVersion(t *testing.T, output string) {
	t.Helper()
	agent.ResetHookAgentVersionCache()
	t.Cleanup(agent.ResetHookAgentVersionCache)
	dir := t.TempDir()
	path := filepath.Join(dir, "codex")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nprintf '%s\\n' '"+output+"'\n"), 0755); err != nil {
		t.Fatalf("write fake codex: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestCodexCheckHooks_ExceedsSupportAgainstPin(t *testing.T) {
	for _, tc := range []struct {
		versionOut  string
		wantVersion string
		wantExceeds bool
	}{
		{"codex-cli 0.153.4", "0.153.4", false},
		{"codex-cli 0.160.0", "0.160.0", true},
		{"codex-cli 0.124.0", "0.124.0", false},
	} {
		t.Run(tc.versionOut, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			fakeCodexVersion(t, tc.versionOut)
			status, err := (&Provider{}).CheckHooks()
			if err != nil {
				t.Fatalf("CheckHooks: %v", err)
			}
			if status.AgentVersion != tc.wantVersion {
				t.Fatalf("AgentVersion = %q, want %q", status.AgentVersion, tc.wantVersion)
			}
			if status.SupportedVersion != "0.153.4" {
				t.Fatalf("SupportedVersion = %q, want 0.153.4", status.SupportedVersion)
			}
			if status.ExceedsSupport != tc.wantExceeds {
				t.Fatalf("ExceedsSupport = %v, want %v", status.ExceedsSupport, tc.wantExceeds)
			}
		})
	}
}
```

`hooks_test.go` is `package codex` (internal) — confirm the file's package clause and that `agent` is imported; add the import if missing.

- [ ] **Step 2: Run**

Run: `go test ./internal/agent/codex/ -run TestCodexCheckHooks_ExceedsSupportAgainstPin -v 2>&1 | tail -10`
Expected: PASS ×3. (No hooks.json exists in the temp HOME, so `CheckHooks` takes the early `hooks.json not found` return, which still carries the version fields.)

- [ ] **Step 3: Full suite + commit**

Run: `gofmt -l internal/ ; go test ./internal/agent/... ./internal/module/agent/ 2>&1 | tail -8`
Expected: gofmt silent; all `ok`.

```bash
git add internal/agent/codex/hooks_test.go
git commit -m "test(codex): CheckHooks version pin against fake codex --version (#1159)

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

- [ ] **Step 4: PR 2** (main session)

```bash
gh pr create --title "test(codex): freeze codex-cli 0.153.4 hook fixtures and version pin (#1159, PR 2/2)" --body "Spec: docs/specs/2026-09-18-codex-hooks-catalog-spec.md §PR 2. Plan Tasks 6–9. Fixtures: 7 from agent_trace_chains.root_payload_json (scrubbed), 3 live-captured (PermissionRequest, PostToolUse, Interrupt) via a scratch project .codex/hooks.json — ~/.codex untouched. Closes #1159."
```

---

## Self-review

- **Spec coverage:** §2.1 catalog → Task 1; §2.2 derive → Task 2; §2.3 installer (retired strip, timeout, feature flag, version, hooks.state test) → Task 3; §2.4 module cases → Task 4; §2.5 test list → Tasks 1–3 (each named test is renamed/updated where listed; `drift_test` in Task 2; `provider_test` in Task 2 with the recorded deviation); PR 2 fixtures/manifest/tests → Tasks 6–9; §3 acceptance is a main-session step after deploy (not a plan task); §5 size guard → Task 5.
- **Placeholders:** none — every step carries code or an exact command. Task 6 Step 2 depends on the live codex flag name; the fallback instruction is explicit.
- **Type consistency:** `codexRetiredUpstreamEvents` (slice) used in Tasks 3 and 7 consistently; `codexHookTimeoutSeconds(string) int`; `writeCodexConfigText(t, home, text)`; frozen structs `codexFrozenEvents`/`codexFrozenManifest` defined in Task 7 and consumed in Task 8; `fakeCodexVersion` defined in Task 9 only.
