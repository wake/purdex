# Codex Hooks Catalog Refresh (0.153.x) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Align the daemon's codex hook catalog, installer and status derivation with codex-cli 0.153.4 (retire `Notification`/`StopFailure`, install `PostToolUse`/`Interrupt`, fix the `SessionEnd` timeout and the deprecated feature flag), then freeze 0.153.4 fixtures so future drift is caught by tests.

**Architecture:** All work is in `internal/agent/codex` (catalog `events.go`, derive `status.go`, installer `hooks.go`) plus test-only touches in `internal/agent/drift_test.go` and `internal/module/agent`. The catalog (`codexEventSpecs`) is the single source of truth: the installer, `CheckHooks`, cleanup sets and `SupportedStatuses` all derive from it, so most behaviour changes are catalog edits with the installer following. PR 2 adds frozen `testdata/` fixtures mirroring the opencode 1.14.23 layout and tests that pin the catalog to them.

**Tech Stack:** Go 1.26, `github.com/BurntSushi/toml`, `go test`. No SPA changes.

**Spec:** `docs/specs/2026-09-18-codex-hooks-catalog-spec.md` (issue #1159). Executors read both.

## Global Constraints

- Two PRs, sequential: **PR 1** = Tasks 1–6 on `worktree-codex-hooks-catalog`; **PR 2** = Tasks 7–10 on a new branch cut from `origin/main` **after PR 1 merges** (Task 7 Step 0). Task 11 is the on-machine acceptance after the bump/deploy. PR 1 ≤ 800 lines diff under `internal/`; Task 6 Step 1 has the executable split if it is not.
- Installable set after PR 1 is exactly 10: `SessionStart, UserPromptSubmit, SubagentStart, SubagentStop, Stop, PermissionRequest, SessionEnd, PreToolUse, PostToolUse, Interrupt`.
- Retired upstream keys: `Notification`, `StopFailure`. They stay in the catalog with explicit `Handling: HookHandlingIgnored` (so `IsInstallableHookSpec` is false) but **keep their `EmitsStatus`** (`{waiting, idle}` / `{error}`), and `deriveCodexStatus` keeps their cases. This is what makes spec §2.1 ("DeriveStatus keeps working") and §2.5 ("SupportedStatuses unchanged") both hold: `SupportedStatuses` unions `EmitsStatus` over every catalog entry, and the three-way drift test keeps its Notification/StopFailure fixtures. The codex-only assertion "non-installable ⇒ empty EmitsStatus" is narrowed to exempt exactly the retired list (opencode's own HC5c test is untouched).
- `codexHooksSupportedVersion = "0.153.4"`.
- `SessionEnd` hook timeout is 3 s; every other event stays 5 s.
- `features.hooks = true` is written; `features.codex_hooks` is deleted on install. `CheckHooks` treats absent-both as enabled; an explicit `false` on the canonical `hooks` key blocks; if `hooks` is absent, an explicit `false` on legacy `codex_hooks` blocks.
- Never touch `~/.codex/hooks.json` or `~/.codex/config.toml` while capturing fixtures (Task 6) — use a scratch project's `.codex/hooks.json`. DB reads are `mode=ro`.
- Every task ends with `gofmt -l internal/ | grep -v '^$'` printing nothing and `go test ./internal/agent/... ./internal/module/agent/` green, then one commit. Commit messages end with `Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>`.
- Run every command from the worktree root `/Users/wake/Workspace/wake/purdex/.claude/worktrees/codex-hooks-catalog` (prefix each Bash with `cd <root> && `).

### Deviations from spec (recorded for the reviewer)

1. Spec §2.2 says Interrupt should "reuse the `turn_id` extraction `parseCodexTurnID` already used for Stop". That helper is unexported in `internal/module/agent/raw_codex_event.go:26` and cannot be called from `internal/agent/codex`. The reuse happens where it already exists: `PdxInterrupt` carries `LifecycleStop`, so `frame_ops.go`'s Stop case calls `parseCodexTurnID(req.RawEvent)` for it unchanged (Task 4 proves the detach). `status.go` only surfaces `turn_id` as Inspector detail via `agent.DetailStrings`, the same primitive `PdxSessionStart` uses.
2. Spec §PR 2 says the 8 DB-sourced payloads come from `agent_trace_steps.payload_json`. Measured on mlab 2026-09-18: `agent_trace_steps.payload_json` is empty for codex triggers; the raw hook payload is in `agent_trace_chains.root_payload_json` → `.raw_event`. Also codex `PdxPermissionRequest` has **0** rows there (all recent sessions ran `bypassPermissions`), so `PermissionRequest` joins `PostToolUse` and `Interrupt` in the live-capture group (Task 7).
3. codex-cli 0.153.4 `--ask-for-approval` accepts only `on-request` / `never` (no `untrusted`); live capture uses `-a on-request -s read-only` and a write outside the sandbox to force a `PermissionRequest`.

---

## File map

| File | Role | PR |
|---|---|---|
| `internal/agent/codex/events.go` | catalog `codexEventSpecs` | 1 |
| `internal/agent/codex/events_test.go` | catalog pins (installable set, upstream pin, handling, lifecycle, metadata) | 1 |
| `internal/agent/codex/status.go` | `deriveCodexStatus` cases | 1 |
| `internal/agent/codex/status_test.go` | derive tests | 1 |
| `internal/agent/codex/provider_test.go` | `SupportedStatuses` explicit pin (unchanged set) | 1 |
| `internal/agent/drift_test.go` | codex fixtures for three-way drift (+PostToolUse, +Interrupt) | 1 |
| `internal/agent/codex/hooks.go` | Task 3: retired strip + cleanup names + version pin; Task 5: timeout table + feature flag | 1 |
| `internal/agent/codex/hooks_test.go` | installer tests (Tasks 3, 5) | 1 |
| `internal/module/agent/fakes_test.go` | `fakeDefaultEvents` gets PostToolUse + Interrupt | 1 |
| `internal/module/agent/frame_ops_l2_test.go` | three new `applyFrameEvent` cases | 1 |
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
// expectedCodexRetiredEmitsStatus pins the parser-retained EmitsStatus of
// the retired entries: they are not installable (explicit Ignored) but
// DeriveStatus still handles them (spec §2.1), so SupportedStatuses keeps
// error/waiting/idle from them (spec §2.5).
var expectedCodexRetiredEmitsStatus = map[string][]agent.Status{
	"PdxNotification": {agent.StatusWaiting, agent.StatusIdle},
	"PdxStopFailure":  {agent.StatusError},
}

// TestCodexEvents_RetiredEntriesIgnored asserts Notification / StopFailure
// remain resolvable, are explicitly ignored (never installed), and keep
// their parser-retained EmitsStatus.
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
		if spec.Handling != agent.HookHandlingIgnored {
			t.Errorf("retired %q Handling = %q, want explicit ignored", key, spec.Handling)
		}
		if agent.IsInstallableHookSpec(spec) {
			t.Errorf("retired %q is installable", key)
		}
		want := expectedCodexRetiredEmitsStatus[spec.PurdexName]
		if len(spec.EmitsStatus) != len(want) {
			t.Errorf("retired %q EmitsStatus = %v, want %v", key, spec.EmitsStatus, want)
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
	"PdxStopFailure":       {[]agent.Status{agent.StatusError}, "Retired: not a codex hook event since 0.153", false, agent.HookHandlingIgnored},
	"PdxNotification":      {[]agent.Status{agent.StatusWaiting, agent.StatusIdle}, "Retired: not a codex hook event since 0.153", false, agent.HookHandlingIgnored},
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

Narrow the empty-EmitsStatus assertion in `TestCodexEventsClassifyCurrentDocs` so it exempts exactly the retired entries (they are the only ignored entries with a DeriveStatus case):

```go
		if !agent.IsInstallableHookSpec(e) && len(e.EmitsStatus) != 0 {
			if _, retired := expectedCodexRetiredEmitsStatus[e.PurdexName]; !retired {
				t.Errorf("codex non-installable %s EmitsStatus = %v, want empty", e.PurdexName, e.EmitsStatus)
			}
		}
```

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
// installer but codex never fired them; they stay as explicitly ignored
// entries (never installed, stripped on install/remove) that keep their
// EmitsStatus and DeriveStatus cases so in-flight payloads still resolve
// and SupportedStatuses is unchanged (spec §2.1 / §2.5).
var codexEventSpecs = []agent.HookEventSpec{
```

Entries (keep the others exactly as they are):

```go
	{
		// Retired (#1159): codex never fires StopFailure. Handling is
		// explicit so the installer skips it; EmitsStatus + the
		// DeriveStatus case are retained (spec §2.1 / §2.5).
		PurdexName:   "PdxStopFailure",
		UpstreamKeys: []string{"StopFailure"},
		Lifecycle:    agent.LifecycleStopFailure,
		EmitsStatus:  []agent.Status{agent.StatusError},
		Description:  "Retired: not a codex hook event since 0.153",
		Handling:     agent.HookHandlingIgnored,
	},
	{
		// Retired (#1159): codex never fires Notification. Same policy
		// as PdxStopFailure above.
		PurdexName:   "PdxNotification",
		UpstreamKeys: []string{"Notification"},
		Lifecycle:    agent.LifecycleNone,
		EmitsStatus:  []agent.Status{agent.StatusWaiting, agent.StatusIdle},
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

- [ ] **Step 5: Add drift fixtures** — in `internal/agent/drift_test.go` `"codex"` block, insert after the `PdxPermissionRequest` line and after the `PdxStop` line respectively (keep every existing codex fixture, including the four `PdxNotification` and the `PdxStopFailure` rows — retired entries keep their EmitsStatus so the three-way sets stay equal):

```go
		{"PdxPostToolUse", `{"tool_name":"Bash"}`, agent.StatusRunning, true},
```

```go
		{"PdxInterrupt", `{"turn_id":"t"}`, agent.StatusIdle, true},
```

- [ ] **Step 6: Assert `SupportedStatuses` is unchanged** — spec §2.5. `TestCodexSupportedStatuses` in `provider_test.go` already pins `{running, waiting, idle, error, clear}`; add this comment above `want` so the intent survives the retirement:

```go
	// #1159: retired Notification/StopFailure keep their EmitsStatus, so
	// this set is unchanged by the 0.153 catalog refresh (spec §2.5).
```

- [ ] **Step 7: Run the agent packages**

Run: `go test ./internal/agent/ ./internal/agent/codex/ 2>&1 | tail -20`
Expected: `internal/agent` PASS (three-way + per-event drift green; `TestDriftFixtureCoversAllEvents` green because PostToolUse/Interrupt now have fixtures). `internal/agent/codex` still fails only in `hooks_test.go` (installer set) — Task 3 fixes those. Confirm the failing test names all match `TestCodex.*Hooks|TestCheckHooks|TestMergeCodexHooks|TestCodexOwnedCleanup|TestCodexInstallHooks`.

- [ ] **Step 8: Commit**

```bash
git add internal/agent/codex/status.go internal/agent/codex/status_test.go internal/agent/drift_test.go internal/agent/codex/provider_test.go
git commit -m "feat(codex): derive running from PostToolUse and idle from Interrupt (#1159)

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---

### Task 3: Installer A — retired keys, cleanup set, version pin

**Files:**
- Modify: `internal/agent/codex/hooks.go`
- Test: `internal/agent/codex/hooks_test.go`

**Interfaces:**
- Produces (package-private):
  - `var codexRetiredUpstreamEvents = []string{"Notification", "StopFailure"}`
  - `func stripRetiredPdxCodexEntries(hooks map[string]any)` — for each retired key whose value is `[]any`, drop pdx-owned entries; delete the key when nothing remains; leave non-`[]any` values untouched
  - `codexOwnedCleanupEventNames()` additionally contains each retired key and `"Pdx"+key`
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

- [ ] **Step 2: Add retired-key strip tests**:

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

- [ ] **Step 3: Update `TestCodexOwnedCleanupEventNames_TwoSetUnion`** so `want` also includes retired keys — insert right after the existing `for _, spec := range codexEventSpecs` loop that builds `want`, and update the doc comment to say "installable UpstreamKeys ∪ PurdexName ∪ retired keys ∪ Pdx+retired":

```go
	for _, key := range codexRetiredUpstreamEvents {
		want[key] = true
		want["Pdx"+key] = true
	}
```

- [ ] **Step 4: Version pin test**:

```go
func TestCodexHooksSupportedVersion_Pinned(t *testing.T) {
	if codexHooksSupportedVersion != "0.153.4" {
		t.Fatalf("codexHooksSupportedVersion = %q, want 0.153.4", codexHooksSupportedVersion)
	}
}
```

- [ ] **Step 5: Run to verify failures**

Run: `go test ./internal/agent/codex/ 2>&1 | grep -E '^(--- FAIL|FAIL|ok)' | head -30`
Expected: FAIL — compile error for `codexRetiredUpstreamEvents`; after that the new tests fail.

- [ ] **Step 6: Implement in `hooks.go`** — constants near the top:

```go
const codexHooksSupportedVersion = "0.153.4"

// codexRetiredUpstreamEvents are hooks.json keys the pre-0.153 installer
// wrote that codex never fires (#1159). Install and remove strip pdx-owned
// entries under them and drop the key when it empties; third-party entries
// are left alone.
var codexRetiredUpstreamEvents = []string{"Notification", "StopFailure"}
```

In `mergeCodexHooksFile`, the install branch: call `stripRetiredPdxCodexEntries(hooks)` right before the `for _, spec := range codexEventSpecs` loop. New helper (place after `mergeCodexHooksFile`):

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

Extend `codexOwnedCleanupEventNames` (after its loop, before `return owned`) and update its doc comment to "…∪ retired upstream keys ∪ Pdx+retired (cleanup only; never installed)":

```go
	for _, key := range codexRetiredUpstreamEvents {
		owned[key] = true
		owned["Pdx"+key] = true
	}
```

- [ ] **Step 7: Run the codex package**

Run: `go test ./internal/agent/codex/ 2>&1 | tail -15`
Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/agent/codex/hooks.go internal/agent/codex/hooks_test.go
git commit -m "feat(codex): installer strips retired Notification/StopFailure, pins 0.153.4 (#1159)

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

- [ ] **Step 2: Write the three cases** — append to `frame_ops_l2_test.go`:

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
// turn_id exactly like Stop (LifecycleStop path).
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

// #1159 (b'): Interrupt on a standalone codex frame moves running → idle.
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

`encoding/json` is already imported in `frame_ops_l2_test.go` (`rawTurn` uses `json.RawMessage`).

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

### Task 5: Installer B — SessionEnd timeout, feature flag, hooks.state round-trip

This task is last in PR 1 on purpose: it is the one Task 6 Step 1 peels off into its own PR if the size guard trips. It must compile and pass on its own on top of Task 4.

**Files:**
- Modify: `internal/agent/codex/hooks.go`
- Test: `internal/agent/codex/hooks_test.go`

**Interfaces:**
- Produces (package-private):
  - `func codexHookTimeoutSeconds(upstreamKey string) int` — `"SessionEnd"` → 3, else 5
  - `setCodexHooksFeature` writes `features.hooks = true`, deletes `features.codex_hooks`
  - `codexHooksFeatureEnabled(path)` — canonical `hooks` wins; else legacy `codex_hooks`; else true
  - test helper `writeCodexConfigText(t, home, text string)`

- [ ] **Step 1: Timeout tests**:

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

- [ ] **Step 2: Feature-flag tests** — replace `writeCodexFeatureFlag` and `TestCodexCheckHooks_FeatureFlagMissingOrFalseBlocks`, and update `TestCodexInstallHooks_EnablesFeatureFlagAndPreservesConfig`:

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

- [ ] **Step 3: hooks.state round-trip test**:

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

- [ ] **Step 4: Run to verify failures**

Run: `go test ./internal/agent/codex/ 2>&1 | grep -E '^(--- FAIL|FAIL|ok)' | head -30`
Expected: FAIL — compile errors for `codexHookTimeoutSeconds`, `writeCodexConfigText`; then the new tests fail.

- [ ] **Step 5: Implement in `hooks.go`** — timeout table near the top:

```go
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

In `mergeCodexHooksFile`'s install loop replace `"timeout": 5,` with `"timeout": codexHookTimeoutSeconds(key),`.

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

- [ ] **Step 6: Run the codex package**

Run: `go test ./internal/agent/codex/ 2>&1 | tail -15`
Expected: PASS. If `TestCodexInstallHooks_PreservesHooksStateTrustedHash` fails on the key lookup, print `config` — BurntSushi decodes dotted-quoted table names into nested maps only along unquoted dots; the quoted key `"<path>:stop:0:0"` must appear as a single map key under `state`. Do not change the writer; fix only the test's lookup if the decoded shape differs, and record the actual shape in the test comment.

- [ ] **Step 7: Whole-tree check**

Run: `gofmt -l internal/ ; go vet ./internal/agent/... && go test ./internal/agent/... ./internal/module/agent/ 2>&1 | tail -10`
Expected: gofmt prints nothing; all `ok`.

- [ ] **Step 8: Commit**

```bash
git add internal/agent/codex/hooks.go internal/agent/codex/hooks_test.go
git commit -m "feat(codex): SessionEnd hook timeout 3s, write features.hooks and drop codex_hooks (#1159)

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---

### Task 6: PR 1 wrap-up (main session)

- [ ] **Step 1: Size guard**

Run: `git diff --stat a171cd7a..HEAD -- internal/ | tail -1`
Expected: ≤ 800 lines, ≤ 20 files. If over, peel Task 5 (the last commit) into its own PR — every earlier commit compiles and passes on its own:

```bash
git branch worktree-codex-hooks-flag HEAD          # keeps the Task 5 commit
git reset --hard HEAD~1                             # PR 1 = Tasks 1–4
go test ./internal/agent/... ./internal/module/agent/ 2>&1 | tail -6   # must be all ok
# open PR 1 (Step 3). After it merges: git checkout worktree-codex-hooks-flag && git rebase origin/main && push → PR 1b, R1 only.
```

- [ ] **Step 2: Full Go suite**

Run: `go build ./... && go test ./... 2>&1 | grep -v '^ok' | head -20`
Expected: nothing but `no test files` lines.

- [ ] **Step 3: Push + PR**

```bash
git push -u origin worktree-codex-hooks-catalog
gh pr create --title "feat(codex): hooks catalog refresh for codex-cli 0.153.4 (#1159, PR 1/2)" --body-file - <<'EOF'
Spec: docs/specs/2026-09-18-codex-hooks-catalog-spec.md
Plan: docs/plans/2026-09-18-codex-hooks-catalog-plan.md (Tasks 1–6)

- Catalog: retire Notification/StopFailure (explicit ignored, EmitsStatus + parser retained), install PostToolUse + Interrupt, declare PreCompact/PostCompact (ignored); pin 12 upstream names bidirectionally
- DeriveStatus: PostToolUse → running, Interrupt → idle (+turn_id)
- Installer: strip retired keys on install/remove; SessionEnd timeout 3s; features.hooks=true and drop codex_hooks; absent flag = enabled; version pin 0.153.4; hooks.state trusted_hash round-trip test
- Module: three regression cases, no production change

Deviations from spec: see plan "Deviations from spec" (parseCodexTurnID reuse happens at the module layer).

#1159 closes with PR 2.
EOF
```

- [ ] **Step 4: Reviews** — R1 → R2 attack → R2 critic per project CLAUDE.md; fix; incremental re-review; merge; bump PR (`VERSION` + `CHANGELOG.md`, no codex).

---

### Task 7: Capture 0.153.4 payload fixtures (PR 2)

**Files:**
- Create: `internal/agent/codex/testdata/codex-0.153.4-payloads/<PurdexName>.json` (10 files)
- Create: `internal/agent/codex/testdata/codex-0.153.4-source.md`
- Create: `internal/agent/codex/testdata/codex-0.153.4-version.txt` (content `0.153.4\n`)

**Interfaces:**
- Produces: one JSON object per installable PurdexName, being the raw codex hook stdin payload (what `raw_event` holds), scrubbed.

- [ ] **Step 0: Branch for PR 2 (after PR 1 and its bump have merged)**

```bash
git fetch origin
git status --short            # must be clean
git checkout -b worktree-codex-hooks-fixtures origin/main
git log --oneline -1          # must show the bump commit that followed PR 1
```

**Scrub rules** (apply to every fixture; keep every key, replace values):
- `session_id` → `"01a00000-0000-7000-8000-000000000001"`, `turn_id` → `"01a00000-0000-7000-8000-000000000002"`, `agent_id` → `"01a00000-0000-7000-8000-000000000003"`
- `transcript_path` → `"/Users/example/.codex/sessions/2026/09/18/rollout-example.jsonl"`, `cwd` → `"/Users/example/project"`
- `tool_input` → `{"command":"echo hi"}`; `tool_response`/`tool_output` (PostToolUse) → `{"stdout":"hi\n","exit_code":0}` keeping whatever top-level key name codex used
- `last_assistant_message` → `"ok"`, `prompt` → `"say hi"`, `model` → keep, `permission_mode` → keep, `reason`/`source`/`stop_hook_active`/`hook_event_name` → keep

- [ ] **Step 1: Pull the 7 DB-available payloads (read-only)**

```bash
S=/private/tmp/claude-501/-Users-wake-Workspace-wake-purdex/c3081413-b2f9-417d-9556-bfba81435e92/scratchpad
mkdir -p "$S/codex-fixtures"
for ev in PdxSessionStart PdxUserPromptSubmit PdxPreToolUse PdxStop PdxSessionEnd PdxSubagentStart PdxSubagentStop; do
  sqlite3 "file:/Users/wake/.config/pdx/agent_events.db?mode=ro" \
    "select json_extract(root_payload_json,'$.raw_event') from agent_trace_chains where root_agent_type='codex' and root_event_name='$ev' order by started_at desc limit 1" \
    > "$S/codex-fixtures/$ev.raw.json"
done
```

Then, for each raw file, print only its key list (`jq 'keys'`) — never `cat` the whole raw file into the transcript (it contains prompts and paths).

- [ ] **Step 2: Live-capture `PermissionRequest`, `PostToolUse`, `Interrupt`**

```bash
C=/private/tmp/claude-501/-Users-wake-Workspace-wake-purdex/c3081413-b2f9-417d-9556-bfba81435e92/scratchpad/codex-capture
mkdir -p "$C/.codex" && cd "$C" && git init -q . && printf 'scratch\n' > README.md && git add -A && git commit -qm init
cat > "$C/.codex/hooks.json" <<EOF
{
  "hooks": {
    "PermissionRequest": [{"hooks": [{"type": "command", "command": "cat >> $C/PermissionRequest.log", "timeout": 5}]}],
    "PostToolUse":       [{"hooks": [{"type": "command", "command": "cat >> $C/PostToolUse.log", "timeout": 5}]}],
    "Interrupt":         [{"hooks": [{"type": "command", "command": "cat >> $C/Interrupt.log", "timeout": 5}]}]
  }
}
EOF
```

Run codex interactively in a tmux window from `$C` (main session drives it; the user's `~/.codex` is untouched because these are project-level hooks). 0.153.4 has only `-a on-request|never`, so force a prompt with a read-only sandbox plus a command that must write:

```bash
tmux new-window -d -n codex-capture -c "$C" "codex -a on-request -s read-only"
# 1. /hooks → approve the three project hooks (first run of project-level hooks)
# 2. prompt: "Run exactly this shell command and nothing else: echo hi > /tmp/codex-capture-probe.txt"
#    → codex asks for approval (sandbox is read-only) → PermissionRequest.log; approve → PostToolUse.log
# 3. prompt: "count from 1 to 500, one number per line" and press Ctrl-C while it streams → Interrupt.log
# 4. /quit
```

Each log holds one JSON object per line. Copy the first line of each into `$S/codex-fixtures/<PurdexName>.raw.json` (`PdxPermissionRequest`, `PdxPostToolUse`, `PdxInterrupt`). If a log is empty, run `codex --help | grep -A6 ask-for-approval` and adjust; do not edit `~/.codex/*`.

- [ ] **Step 3: Scrub and write fixtures**

Write `$S/scrub.jq`:

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
for f in "$S"/codex-fixtures/*.raw.json; do n=$(basename "$f" .raw.json); jq -S -f "$S/scrub.jq" "$f" > "$OUT/$n.json"; done
printf '0.153.4\n' > internal/agent/codex/testdata/codex-0.153.4-version.txt
grep -rl '/Users/wake' "$OUT" && echo "LEAK" || echo "clean"
ls "$OUT" | wc -l
```

Expected: `clean`; `10`.

- [ ] **Step 4: Write `codex-0.153.4-source.md`** — record: codex-cli version (`codex --version` → `codex-cli 0.153.4`), docs URL + fetch date, the DB query (Step 1) with the note that payloads live in `agent_trace_chains.root_payload_json.raw_event`, the live-capture procedure (Step 2, including that `~/.codex` was never modified), the scrub rules table, and a fixture-by-fixture table with columns `Fixture | Class (runtime-trace) | Source (db / live) | Keys DeriveStatus reads`.

- [ ] **Step 5: Commit**

```bash
git add internal/agent/codex/testdata/
git commit -m "test(codex): freeze codex-cli 0.153.4 hook payload fixtures (#1159)

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---

### Task 8: Frozen manifest + events.json + classification test

**Files:**
- Create: `internal/agent/codex/testdata/codex-0.153.4-manifest.json`, `codex-0.153.4-events.json`
- Create: `internal/agent/codex/fixtures_test.go`

**Interfaces:**
- Produces: `loadCodexFrozenEvents(t) codexFrozenEvents`, `loadCodexFrozenManifest(t) codexFrozenManifest` (test helpers used by Task 9).

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

- [ ] **Step 3: Write `codex-0.153.4-events.json`** (14 entries; `status` values are the `agent.Status` strings `running|waiting|idle|clear`):

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

- [ ] **Step 4: Write `codex-0.153.4-manifest.json`** (`ignored` = 2 ignored + 2 retired):

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

### Task 9: Payload contract test

**Files:**
- Modify: `internal/agent/codex/fixtures_test.go`

**Interfaces:**
- Consumes: fixtures from Task 7, `events.json` `status` field from Task 8.

- [ ] **Step 1: Append the test** (add `"strings"` to the imports):

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
			if strings.Contains(string(raw), "/Users/wake") {
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
```

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

### Task 10: CheckHooks version test with a fake `codex` on PATH

**Files:**
- Modify: `internal/agent/codex/hooks_test.go`

**Interfaces:**
- Consumes: `agent.ResetHookAgentVersionCache()`, `agent.DetectHookAgentVersion("codex", "--version")` (already used by `CheckHooks`).

- [ ] **Step 1: Write the test** (mirror `fakeOpenCodeVersion`; `hooks_test.go` is `package codex` and already imports `agent`):

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
git push -u origin worktree-codex-hooks-fixtures
gh pr create --title "test(codex): freeze codex-cli 0.153.4 hook fixtures and version pin (#1159, PR 2/2)" --body "Spec: docs/specs/2026-09-18-codex-hooks-catalog-spec.md §PR 2. Plan Tasks 7–10. Fixtures: 7 from agent_trace_chains.root_payload_json (scrubbed), 3 live-captured (PermissionRequest, PostToolUse, Interrupt) via a scratch project .codex/hooks.json — ~/.codex untouched. Closes #1159."
```

Then R1 review only (test-only PR, no R2 unless R1 raises a critical), merge, bump PR.

---

### Task 11: Deploy + spec §3 acceptance on mlab (main session, after the PR 1 bump is on origin/main)

Do this once after PR 1's bump (the SPA/daemon behaviour is all in PR 1); PR 2 needs no acceptance beyond its tests.

- [ ] **Step 1: Deploy daemon**

```bash
git -C /Users/wake/Workspace/wake/purdex pull --ff-only origin main   # main checkout
cd /Users/wake/Workspace/wake/purdex && go build -o bin/pdx ./cmd/pdx && pdx stop; pdx start
sleep 3 && curl -s http://100.64.0.2:7860/api/version ; pdx status | head -5
```

Expected: daemon reports the bumped version.

- [ ] **Step 2: §3.1 — Host › Hooks › codex shows `Hook Support Through 0.153.4`, no version warning.** Open the SPA (`http://100.64.0.2:5174`) → Host › Hooks → codex. Also: `curl -s http://100.64.0.2:7860/api/hooks/codex/status | jq '{supportedVersion, agentVersion, exceedsSupport, installed, issues}'` → `supportedVersion:"0.153.4"`, `exceedsSupport:false`.

- [ ] **Step 3: §3.2 — Install.** Press Install (or `curl -s -X POST http://100.64.0.2:7860/api/hooks/codex/setup`), then:

```bash
jq -r '.hooks | keys[]' ~/.codex/hooks.json | sort | tr '\n' ' '; echo
# → Interrupt PermissionRequest PostToolUse PreToolUse SessionEnd SessionStart Stop SubagentStart SubagentStop UserPromptSubmit
jq '.hooks.SessionEnd[0].hooks[0].timeout, .hooks.Stop[0].hooks[0].timeout' ~/.codex/hooks.json      # 3, 5
jq '.hooks | has("Notification"), has("StopFailure")' ~/.codex/hooks.json                             # false false
grep -nE '^\[features\]|^hooks = |codex_hooks' ~/.codex/config.toml                                    # hooks = true, no codex_hooks
grep -c 'trusted_hash' ~/.codex/config.toml                                                            # ≥ 8 (unchanged events keep their state)
```

- [ ] **Step 4: §3.3 — Start codex in a tmux pane.** `tmux new-window -n codex-accept codex`; the first lines must contain no `deprecated:` and no `warning: clamping`. `/hooks` asks approval only for `PostToolUse` and `Interrupt`; approve.

- [ ] **Step 5: §3.4 — Runtime.** Ask codex to run a tool (`ls`), then Ctrl-C mid-turn on a long answer:

```bash
grep -E 'purdex_name=Pdx(PostToolUse|Interrupt)' ~/.config/pdx/logs/pdx.log | tail -4
```

Expected: a `PdxPostToolUse` line with `status=running` after the tool call; a `PdxInterrupt` line with `status=idle` right after Ctrl-C, and the tab light goes idle without waiting for a probe (watch the SPA tab).

- [ ] **Step 6: §3.5 — Remove.** `pdx setup --agent codex --remove` (or the SPA Remove button) →

```bash
jq '.hooks | length' ~/.codex/hooks.json          # 0 (or only third-party keys)
grep -n '^hooks = ' ~/.codex/config.toml          # still "hooks = true" — features untouched
```

Then reinstall (Step 3) so the user's daily setup is back.

- [ ] **Step 7: Report** to `mini-lab/_mrdx8h` via `pdx msg send`: PR numbers, bump versions, and the §3.1–§3.5 results.

---

## Self-review

- **Spec coverage:** §2.1 catalog → Task 1; §2.2 derive → Task 2; §2.3 installer: retired strip + cleanup + version → Task 3, timeout + feature flag + hooks.state → Task 5; §2.4 module cases → Task 4; §2.5 test list → Tasks 1–3, 5 (`drift_test` + `provider_test` in Task 2, SupportedStatuses unchanged); PR 2 fixtures/manifest/tests → Tasks 7–10 with the branch cut in Task 7 Step 0; §3 acceptance → Task 11; §5 size guard → Task 6 Step 1 (executable: Task 5 is the last commit and stands alone).
- **Placeholders:** none — every step carries code or an exact command; Task 7 Step 2's fallback is explicit.
- **Type consistency:** `codexRetiredUpstreamEvents` (slice) defined in Task 3 and used in Tasks 3/5/8; `codexHookTimeoutSeconds(string) int` in Task 5; `writeCodexConfigText(t, home, text)` defined in Task 5 and used only there; frozen structs `codexFrozenEvents`/`codexFrozenManifest` defined in Task 8 and consumed in Task 9; `fakeCodexVersion` defined in Task 10 only.
