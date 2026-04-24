# Agent Hooks Hotfix TDD Plan — Catalog + Install Correctness + OpenCode Icon Guard

- **Date**: 2026-04-25
- **Status**: Draft plan
- **Worktree**: `.claude/worktrees/agent-hooks-hotfix-plan`（branch `worktree-agent-hooks-hotfix-plan`）
- **Baseline**: `origin/main @ 75b4d166` (`1.0.0-alpha.224`)
- **Related specs**:
  - `docs/specs/2026-04-23-lights-rebuild-spec.md` §2.4 Architecture Guardrails
  - `docs/specs/2026-04-23-hook-events-declaration-plan.md`
  - `docs/specs/2026-04-23-hook-events-fix-plan.md`
- **Scope**: provider-level hook catalog/install/check correctness + OpenCode icon test coverage
- **Non-goal**: do not change light rebuild runtime frame/projection/probe logic

---

## 0. Spec Reconfirmation

### 0.1 Architecture Guardrails Still Apply

This hotfix keeps the current architecture:

- Hook policy stays distributed per provider (`internal/agent/{cc,codex,opencode}`).
- Shared plumbing (`internal/module/agent/*`) is not touched.
- Build-time declarations are allowed; runtime central FSM / transition registry is not.
- `HookEventSpec` remains the SSoT for Purdex-managed hook declarations.

### 0.2 Why This Is Independent From Lights Rebuild

Lights rebuild handles events **after** Purdex receives a verified hook:

```text
agent hook emits
  -> pdx hook
  -> /api/agent/event
  -> verifyEvent
  -> provider.DeriveStatus
  -> frame/projection/light state
```

This hotfix handles the provider boundary **before** that path:

```text
agent documented hook surface
  -> Purdex hook catalog
  -> installer/checker writes/validates agent config
  -> pdx hook can actually emit into Purdex
```

Therefore this PR must not touch:

- `internal/module/agent/*`
- `internal/agent/probe/*`
- `internal/store/*`
- tab rendering / SubagentDots / light projection UI files

### 0.3 File Conflict Boundary

`lights-phase-3` currently modifies:

- `docs/specs/2026-04-25-lights-rebuild-phase-3-plan.md`
- `internal/agent/probe/liveness.go`
- `internal/agent/probe/liveness_test.go`
- `internal/agent/probe/probe.go`
- `internal/module/agent/frame_ops.go`
- `internal/module/agent/frame_ops_test.go`
- `internal/module/agent/handler_test.go`

This hotfix may modify only:

- `internal/agent/provider.go`
- `internal/agent/supported_statuses_test.go`
- `internal/agent/cc/events.go`
- `internal/agent/cc/events_test.go`
- `internal/agent/cc/hooks.go`
- `internal/agent/cc/hooks_test.go`
- `internal/agent/codex/events.go`
- `internal/agent/codex/events_test.go`
- `internal/agent/codex/hooks.go`
- `internal/agent/codex/hooks_test.go`
- `internal/agent/opencode/events.go`
- `internal/agent/opencode/events_test.go`
- `internal/agent/opencode/hooks.go`
- `internal/agent/opencode/hooks_test.go`
- `internal/agent/opencode/plugin_template.go`
- `internal/agent/opencode/plugin_template_test.go`
- `internal/agent/opencode/status.go`
- `internal/agent/opencode/status_test.go`
- `spa/src/lib/agent-icons.test.tsx`

`spa/src/lib/agent-icons.tsx` is already implemented on `main`; only test coverage is expected.

---

## 1. Problems To Fix

### 1.1 Hook Catalog Means “Known Upstream Hook”, Not Only “Installable Event”

Current `HookEventSpec` records the Purdex-managed installable event subset. That is insufficient for inspection and drift prevention because agents expose hooks that Purdex may intentionally ignore or not install.

Required clarification:

- Every upstream hook event that is relevant for the current supported agent versions must be explicitly classified.
- Classification does not mean every event emits a `Status`.
- Classification must distinguish installable status events, installable detail-only events, ignored upstream events, and unsupported upstream events.
- Existing installer/checker behavior must continue to operate on the **installable subset** only.

### 1.2 Codex Hooks Require Feature Flag

Codex docs for the current CLI require:

```toml
[features]
codex_hooks = true
```

Current installer writes `~/.codex/hooks.json` only. This can produce a false-green status: Purdex says hooks are installed, but Codex does not load them.

### 1.3 Codex `PermissionRequest` Is No Longer FutureOnly

Local checked version: `codex-cli 0.124.0`.

Current docs list `PermissionRequest` as a current supported hook. Existing code still marks it `FutureOnly: true`, so missing `PermissionRequest` is tolerated when it should block install completeness.

### 1.4 Codex Remove Leaves Empty Hook Keys

`mergeCodexHooks(..., remove=true)` can leave:

```json
{ "hooks": { "PermissionRequest": [] } }
```

`CheckHooks` intentionally treats a present empty array as broken. Purdex remove should not create the same broken state it later reports as a problem.

### 1.5 Claude CheckHooks Is Too Loose

Codex already validates command shape per event:

- binary basename is `pdx`
- command includes `hook`
- `--agent codex`
- final event token matches the config key

Claude currently only finds a command containing `pdx hook`. It can mark a wrong-agent or wrong-event command as installed.

### 1.6 OpenCode Plugin Mapping Needs Current-Docs Guard

OpenCode `1.14.23` docs list plugin events such as:

- `session.created`
- `session.idle`
- `session.error`
- `session.deleted`
- `permission.asked`
- `tool.execute.before`
- `tool.execute.after`
- many message/file/todo/server events

Current Purdex plugin maps a selected subset and includes `chat.message`. The public plugin docs do not list `chat.message` in the current event table, so this must be verified against the current OpenCode runtime or source before changing production code. If `chat.message` is stale, `UserPromptSubmit` / running transitions may not emit.

### 1.7 OpenCode Icon Is Implemented But Untested

`spa/src/lib/agent-icons.tsx` already imports `opencode.svg` and returns it for `agentType === "opencode"`. Missing UI icon reports should be treated as either:

- missing test coverage, or
- `agentTypes[ck]` never becoming `opencode` because hooks are not emitted/installed.

This hotfix adds only icon registry tests. Rendering pipeline changes are out of scope.

---

## 2. Contract Changes

### 2.1 Hook Classification

Add a lightweight classification field to hook declarations without introducing a central runtime dispatcher. Raw `HookEventSpec.Handling` must not be read directly by installer/checker code; consumers use the helper functions below so zero-value legacy specs remain safe.

Proposed extension in `internal/agent/provider.go`:

```go
type HookHandling string

const (
    HookHandlingStatus     HookHandling = "status"      // may emit non-empty Status
    HookHandlingDetail     HookHandling = "detail"      // Valid=true, Status="" detail-only
    HookHandlingIgnored    HookHandling = "ignored"     // known upstream event, intentionally not installed/parsed
    HookHandlingUnsupported HookHandling = "unsupported" // known upstream event not supported by this Purdex version
)

type HookEventSpec struct {
    Name        string
    EmitsStatus []Status
    Description string
    FutureOnly  bool
    Handling    HookHandling
}
```

Required helpers in `internal/agent/provider.go`:

```go
func EffectiveHookHandling(spec HookEventSpec) HookHandling
func IsInstallableHookSpec(spec HookEventSpec) bool
```

Rules:

- `EffectiveHookHandling` maps empty `Handling` to `status` when `EmitsStatus` is non-empty.
- `EffectiveHookHandling` maps empty `Handling` to `detail` when `EmitsStatus` is empty. This preserves existing SubagentStart/Stop behavior during migration.
- Upstream events Purdex chooses not to install use `ignored` or `unsupported`.
- `SupportedStatuses()` continues to union only `EmitsStatus`.
- `IsInstallableHookSpec` returns true only for effective `status` and `detail`.
- `InstallHooks` installs only installable events unless a provider explicitly decides otherwise.
- `CheckHooks` reports installed state only for installable events; ignored/unsupported events can be exposed later to Inspector but must not block install completeness.
- Template/spec parity checks compare template emissions against installable specs only.

Derived predicate table:

| Effective handling | Installed by installer | Blocks `Installed=true` when missing | Contributes `SupportedStatuses` | Runtime parser required |
|---|---:|---:|---:|---:|
| `status` | yes | yes unless `FutureOnly` tolerated absent | yes | yes |
| `detail` | yes | yes unless `FutureOnly` tolerated absent | no | yes |
| `ignored` | no | no | no | no |
| `unsupported` | no | no | no | no |

### 2.2 Provider Catalog Updates

#### Claude Code

Keep current installed set as-is:

- `SessionStart`
- `UserPromptSubmit`
- `SubagentStart`
- `SubagentStop`
- `Stop`
- `StopFailure`
- `Notification`
- `PermissionRequest`
- `SessionEnd`

Add explicit known-but-not-installed declarations for current documented hook events such as:

- `PreToolUse`
- `PostToolUse`
- `PostToolUseFailure`
- `PermissionDenied`
- `UserPromptExpansion`
- `PostToolBatch`
- `TaskCreated`
- `TaskCompleted`
- `PreCompact`
- `PostCompact`
- `ConfigChange`
- `CwdChanged`
- `FileChanged`
- `WorktreeCreate`
- `WorktreeRemove`
- other documented lifecycle events not currently installed

These must not change current status behavior.

#### Codex

Required set after this hotfix (`Installed=true` blocks when absent/broken):

- `SessionStart`
- `UserPromptSubmit`
- `PermissionRequest`
- `Stop`

FutureOnly tolerated-absent set remains parser-capable and opportunistically installed, but missing entries do not block `Installed=true`:

- `SubagentStart`
- `SubagentStop`
- `StopFailure`
- `Notification`
- `SessionEnd`

Installer behavior remains broad: Purdex writes all installable status/detail Codex events (the 4 required events plus the 5 FutureOnly events). `FutureOnly` changes checker tolerance, not whether reinstall can seed the hook entry.

Add explicit known ignored/unsupported entries for current documented events that Purdex does not install yet:

- `PreToolUse`
- `PostToolUse`

Do not add runtime status behavior for `PreToolUse` / `PostToolUse` in this PR.

#### OpenCode

Keep currently intended normalized events:

- `SessionStart` from `session.created`
- `PermissionRequest` from `permission.asked` / `question.asked` if still valid
- `StopFailure` from `session.error`
- `Stop` from `session.idle`
- `SessionEnd` from `session.deleted`
- `UserPromptSubmit` from the current prompt/message event after verification
- `SubagentStart` / `SubagentStop` from `tool.execute.before/after` task calls

Explicitly document all current OpenCode plugin events not installed/mapped as ignored/unsupported.

OpenCode template/spec parity and CheckHooks event installation must use only `IsInstallableHookSpec(spec) == true`. Ignored/unsupported OpenCode upstream events must appear in the catalog but must not be expected in the managed plugin body and must not be marked installed by `CheckHooks`.

### 2.3 Codex Feature Flag Contract

Install:

- Ensure `~/.codex/config.toml` exists.
- Preserve all existing config content as much as practical.
- Ensure `[features].codex_hooks = true`.
- Then write `~/.codex/hooks.json`.

Check:

- Missing or false `features.codex_hooks` blocks `Installed`.
- Existing valid hooks with missing feature flag should be `Managed=true, Installed=false`.
- Issue message should be explicit: `codex hooks feature flag disabled; run install to enable features.codex_hooks`.
- Accepted TOML preservation contract: semantic keys must survive, but formatting, comments, and key order may be rewritten by the TOML encoder.

Remove:

- Remove Purdex hook entries from `hooks.json`.
- Do not disable `features.codex_hooks`; users may rely on other hooks.

### 2.4 Codex Remove Contract

After remove:

- If an event has no remaining entries, delete that event key from `hooks`.
- If `hooks` becomes empty, keeping `{ "hooks": {} }` is acceptable.
- Third-party matcher groups remain.

### 2.5 Claude Strict Command Contract

Claude checker should mirror Codex per-event validation:

- Tokenize quote-aware.
- First token basename must be `pdx`.
- Later token `hook` must exist.
- `--agent cc` must exist.
- Final non-empty token must equal the event key.

`filterOutPdx` should remove only commands that are valid Purdex hook commands for `cc`, not arbitrary `pdx hook --agent codex` commands.

### 2.6 OpenCode Version Status

Add `opencodeHooksSupportedVersion = "1.14.23"` and return `SupportedVersion` / `ExceedsSupport` in all `CheckHooks` paths for parity with cc/codex.

### 2.7 OpenCode Mapping Verification Gate

Before changing `renderManagedPlugin`, capture or verify the current OpenCode `1.14.23` prompt/message event contract. The implementation commit must name the exact event key and payload fields in this plan or a retrospective note before production code changes.

If no stable current prompt event is confirmed, keep the production template unchanged in this hotfix and limit OpenCode changes to catalog classification, version reporting, and tests that document the uncertainty.

---

## 3. TDD Test Matrix

### 3.1 Hook Classification Tests

File candidates:

- `internal/agent/cc/events_test.go`
- `internal/agent/codex/events_test.go`
- `internal/agent/opencode/events_test.go`
- `internal/agent/supported_statuses_test.go`

Tests:

| ID | Test | Red Assertion |
|---|---|---|
| HC1 | `TestEffectiveHookHandlingDefaults` | empty handling + non-empty `EmitsStatus` derives `status`; empty handling + empty `EmitsStatus` derives `detail` |
| HC1b | `TestIsInstallableHookSpec` | `status/detail` are installable; `ignored/unsupported` are not |
| HC2 | `TestSupportedStatusesIgnoresIgnoredHooks` | ignored/unsupported hooks with empty `EmitsStatus` do not affect status set |
| HC3 | `TestCCEventsClassifyKnownUpstreamHooks` | documented non-installed cc hooks exist and are `ignored` / `unsupported` |
| HC4 | `TestCodexEventsClassifyCurrentDocs` | `PermissionRequest` is non-FutureOnly; `PreToolUse`/`PostToolUse` are explicitly classified |
| HC5 | `TestOpenCodeEventsClassifyPluginSurface` | OpenCode plugin docs events not mapped are explicitly classified |
| HC6 | `TestInstallableEventNamesStayStable` | installable sets remain cc=9, codex=9, opencode=8 before provider-specific changes |

### 3.2 Codex Feature Flag Tests

File: `internal/agent/codex/hooks_test.go`

Tests:

| ID | Test | Red Assertion |
|---|---|---|
| CF1 | `TestCodexInstallHooks_EnablesFeatureFlag` | after install, `~/.codex/config.toml` has `[features].codex_hooks = true` |
| CF2 | `TestCodexInstallHooks_PreservesExistingConfig` | unrelated TOML keys survive install |
| CF2b | `TestCodexInstallHooks_MergesExistingFeaturesTable` | existing `[features]` entries survive and `codex_hooks` flips true |
| CF2c | `TestCodexInstallHooks_MalformedConfigReturnsError` | malformed TOML returns error and does not overwrite file |
| CF3 | `TestCodexCheckHooks_FeatureFlagMissingBlocks` | valid hooks.json + missing flag => `Installed=false`, `Managed=true`, issue present |
| CF4 | `TestCodexCheckHooks_FeatureFlagFalseBlocks` | `codex_hooks=false` blocks install completeness |
| CF5 | `TestCodexRemoveHooks_DoesNotDisableFeatureFlag` | remove leaves `codex_hooks=true` unchanged |

### 3.3 Codex FutureOnly / Remove Tests

File: `internal/agent/codex/hooks_test.go`, `internal/agent/codex/events_test.go`

Tests:

| ID | Test | Red Assertion |
|---|---|---|
| CP1 | `TestCodexEventsPermissionRequestRequired` | `PermissionRequest.FutureOnly == false` |
| CP2 | `TestCheckHooks_PermissionRequestMissingBlocks` | missing `PermissionRequest` blocks `Installed` |
| CP3 | `TestCheckHooks_UpgradesAvailableDoesNotIncludePermissionRequest` | legacy hooks list excludes `PermissionRequest` from upgrades and reports issue instead |
| CP4 | `TestMergeCodexHooks_RemoveDeletesEmptyEventKeys` | remove deletes keys with no remaining third-party hooks |
| CP5 | `TestCheckHooks_AfterPurdexRemoveFutureOnlyAbsentDoesNotWarn` | installer remove no longer creates empty-array broken warnings |

### 3.4 Claude Strict Checker Tests

File: `internal/agent/cc/hooks_test.go`

Tests:

| ID | Test | Red Assertion |
|---|---|---|
| CS1 | `TestCCCheckHooks_WrongAgentCommandNotInstalled` | `pdx hook --agent codex SessionStart` under cc key is not installed |
| CS2 | `TestCCCheckHooks_WrongEventCommandNotInstalled` | `pdx hook --agent cc Stop` under `SessionStart` key is not installed |
| CS3 | `TestCCCheckHooks_QuotedPathWithSpacesValid` | `"/Applications/Purdex Beta/pdx" hook --agent cc Stop` passes |
| CS4 | `TestMergeClaudeHooks_DoesNotRemoveOtherAgentPdxHook` | remove keeps `--agent codex` command in Claude settings |
| CS5 | `TestMergeClaudeHooks_ReplacesOnlyCcPdxEntries` | reinstall replaces old cc pdx path but preserves third-party and non-cc pdx hooks |

### 3.5 OpenCode Plugin Mapping / Version Tests

Files:

- `internal/agent/opencode/plugin_template_test.go`
- `internal/agent/opencode/hooks_test.go`
- `internal/agent/opencode/events_test.go`

Tests:

| ID | Test | Red Assertion |
|---|---|---|
| OC1 | `TestOpenCodePluginTemplate_UsesCurrentPromptEvent` | template uses the verified current event for prompt/running transition, not stale API |
| OC1a | `TestOpenCodePromptEventContractDocumented` | test fixture names the current verified prompt/message event and required payload fields |
| OC2 | `TestTemplateSpecsParity` | emitted template events match installable specs only |
| OC3 | `TestOpenCodeEvents_ClassifiesCurrentPluginEvents` | current documented OpenCode plugin events are classified |
| OC4 | `TestOpenCodeCheckHooks_ReportsSupportedVersion` | status includes `SupportedVersion=1.14.23` |
| OC5 | `TestOpenCodeCheckHooks_ExceedsSupport` | version comparison warning works when detected version is greater |

### 3.6 OpenCode Icon Guard Test

File: `spa/src/lib/agent-icons.test.tsx`

Tests:

| ID | Test | Red Assertion |
|---|---|---|
| OI1 | `returns opencode icon for opencode agent type` | `getAgentIcon('opencode', ...)` returns a defined component |
| OI2 | `opencode icon is independent of cc/codex variants` | component identity stable across cc/codex variant combinations |

OI1/OI2 are coverage guards on the current baseline; they are allowed to pass immediately because production icon support already exists.

---

## 4. TDD Commit Plan

### Commit 1a — `feat(agent): add hook handling helpers`

Red:

- HC1-HC2 fail.

Green:

- Add `HookHandling` field/types.
- Add `EffectiveHookHandling` and `IsInstallableHookSpec` in `internal/agent/provider.go`.
- Keep existing provider event declarations unchanged.
- Ensure `SupportedStatuses` tests stay green and use effective handling only where classification matters.

Run:

- `go test ./internal/agent/... -count=1`

### Commit 1b — `fix(agent): scope hook installers to installable specs`

Red:

- HC6 fails.
- Existing exact catalog/template/checker tests fail if they read raw catalog size after ignored/unsupported entries are introduced.

Green:

- Migrate cc/codex/opencode event-name helpers, installers, checkers, and OpenCode template/spec parity to `IsInstallableHookSpec`.
- Split tests into full upstream catalog assertions and installable set assertions.
- Preserve installable sets before provider-specific changes: cc=9, codex=9, opencode=8.

Run:

- `go test ./internal/agent/... -count=1`

### Commit 1c — `feat(agent): classify upstream hook catalog`

Red:

- HC3-HC5 fail.

Green:

- Add known upstream ignored/unsupported declarations for cc/codex/opencode.
- Keep ignored/unsupported entries out of install completeness and OpenCode template parity.
- Keep runtime status derivation unchanged for installable status/detail events.

Run:

- `go test ./internal/agent/... -count=1`

### Commit 2 — `fix(agent/codex): require hooks feature flag and current PermissionRequest`

Red:

- CF1-CF5 and CF2b-CF2c fail.
- CP1-CP3 fail.

Green:

- Add TOML read/write helpers for `~/.codex/config.toml`.
- Install enables `features.codex_hooks`.
- Check blocks when feature flag missing/false.
- Preserve TOML semantic keys while accepting formatting/comment/order rewrites.
- `PermissionRequest.FutureOnly=false`.
- Update `codexHooksSupportedVersion` to `0.124.0`.

Run:

- `go test ./internal/agent/codex ./internal/agent -count=1`

### Commit 3 — `fix(agent/codex): remove empty hook keys on uninstall`

Red:

- CP4-CP5 fail.

Green:

- `mergeCodexHooks(remove=true)` deletes event key when no entries remain.
- Preserve third-party matcher groups.
- Keep CheckHooks strict present-but-empty behavior for manually broken files.

Run:

- `go test ./internal/agent/codex -count=1`

### Commit 4 — `fix(agent/cc): validate hooks by agent and event`

Red:

- CS1-CS5 fail.

Green:

- Add quote-aware tokenizer / per-event validator for cc.
- Use strict validator for `CheckHooks`.
- Use cc-scoped filtering for install/remove.

Run:

- `go test ./internal/agent/cc -count=1`

### Commit 5a — `fix(agent/opencode): report hook support version`

Red:

- OC4-OC5 fail.

Green:

- Add supported version reporting for OpenCode.

Run:

- `go test ./internal/agent/opencode ./internal/agent -count=1`

### Commit 5b — `test(agent/opencode): document prompt event contract`

Red:

- OC1a fails until a current OpenCode `1.14.23` prompt/message event key and required payload fields are documented from source/runtime verification.

Green:

- Add a fixture or test note naming the verified event contract.
- Do not change `renderManagedPlugin` unless the verified contract proves `chat.message` is stale.

Run:

- `go test ./internal/agent/opencode -count=1`

### Commit 5c — `fix(agent/opencode): refresh plugin prompt mapping`

Only needed if Commit 5b proves the existing `chat.message` mapping is stale.

Red:

- OC1 fails.
- OC2 fails if template/spec parity is not scoped to installable specs.

Green:

- Update prompt/running event mapping to the verified current event and payload contract.
- Keep template/spec parity scoped to installable specs.

Run:

- `go test ./internal/agent/opencode ./internal/agent -count=1`

### Commit 6 — `test(spa): guard opencode agent icon`

Coverage baseline:

- OI1-OI2 may already pass on the current baseline because OpenCode production icon support exists.

Implementation:

- Add coverage-only tests for OpenCode icon lookup and identity behavior.
- Do not touch tab rendering.

Run:

- `pnpm --prefix spa exec vitest run src/lib/agent-icons.test.tsx`

### Final Verification

Run:

- `go test ./internal/agent/... -count=1`
- `pnpm --prefix spa exec vitest run src/lib/agent-icons.test.tsx`
- Optional full checks before PR: `make test`, `pnpm --prefix spa run lint`, `pnpm --prefix spa run build`

---

## 5. Acceptance Criteria

- Codex install/check cannot report green unless `features.codex_hooks=true` and required current events are valid.
- Codex `PermissionRequest` is required, not FutureOnly.
- Codex remove does not leave empty event keys that Purdex itself later reports as broken.
- Claude CheckHooks rejects wrong-agent and wrong-event `pdx hook` commands.
- OpenCode plugin event mapping is verified against current OpenCode docs/runtime shape.
- OpenCode CheckHooks reports version support fields like cc/codex.
- OpenCode icon registry has explicit tests.
- No files under `internal/module/agent/*`, `internal/agent/probe/*`, `internal/store/*`, or tab rendering are modified.

---

## 6. Risks

- TOML round-trip may rewrite user formatting in `~/.codex/config.toml`. Keep helper minimal and document this if unavoidable.
- Full upstream hook catalog classification may grow long. Keep it declarative and provider-local; do not add a central runtime dispatcher.
- OpenCode prompt event mapping may require runtime verification if docs do not identify a direct replacement for `chat.message`. If uncertain, split into a smaller PR that only adds tests and version reporting first.
- Existing tests may assume exactly 9 cc / 9 codex / 8 opencode events. Update those tests to count installed events separately from known upstream declarations.
