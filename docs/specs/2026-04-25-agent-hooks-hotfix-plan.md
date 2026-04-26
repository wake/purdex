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
- `internal/module/agent/trace.go`

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
- `internal/agent/opencode/testdata/opencode-1.14.23-*`
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

`HookInstaller.Events()` becomes the provider's classified upstream hook catalog. It is no longer synonymous with the installable subset. Installer/checker/template consumers must derive installable events through `IsInstallableHookSpec`.

Proposed extension in `internal/agent/provider.go`:

```go
type HookHandling string

const (
    HookHandlingStatus     HookHandling = "status"      // may emit non-empty Status
    HookHandlingDetail     HookHandling = "detail"      // Valid=true, Status="" detail-only
    HookHandlingIgnored    HookHandling = "ignored"     // current upstream event intentionally irrelevant to status/detail behavior
    HookHandlingUnsupported HookHandling = "unsupported" // current upstream event that may be relevant but is not safely parsed/installed yet
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
- Newly added upstream non-installable specs must set `Handling` explicitly to `ignored` or `unsupported`; they must not rely on the empty-to-`detail` migration default.
- Ignored/unsupported specs must have empty `EmitsStatus`. If a future event should contribute status, it must be promoted to `status` handling with parser/install support.
- `SupportedStatuses()` continues to union only `EmitsStatus`.
- `IsInstallableHookSpec` returns true only for effective `status` and `detail`.
- `InstallHooks` installs only installable events unless a provider explicitly decides otherwise.
- `CheckHooks.Events` contains installable specs only in this hotfix. Ignored/unsupported events remain in `Provider.Events()` for catalog/tests and can be exposed later to Inspector, but must not block install completeness.
- Template/spec parity checks compare template emissions against installable specs only.
- Each provider's `Events()` defensive copy must preserve `Handling`; otherwise ignored/unsupported specs can incorrectly fall back to `detail` and become installable.

Stale ignored/unsupported artifacts already present on disk:

- Install must never add Purdex commands for ignored/unsupported events.
- CheckHooks must not include ignored/unsupported events in install completeness and must not report them as missing or broken.
- `Managed=true` may be based on any Purdex-owned artifact, including stale ignored/unsupported entries, so users can run remove.
- RemoveHooks must remove stale Purdex-owned ignored/unsupported commands while preserving third-party entries.

Purdex ownership for cleanup is provider-local and explicit:

- Remove/managed detection may scan all configured hook keys, including keys not present in the current installable set.
- A command is Purdex-owned only when its tokenized shape targets this provider and its event token is in that provider's owned cleanup set.
- The owned cleanup set includes currently installable events plus any historically installed Purdex events that were later retired.
- Unknown event tokens are preserved by default, even when they use `pdx hook --agent <provider>`, because they may be user-authored, future upstream events, or third-party integrations.
- If a future Purdex release removes an event from installable handling, it must keep the event in the owned cleanup set so uninstall can clean artifacts from older Purdex versions.

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

Add an exact version-pinned upstream catalog table before implementation. The table must include every current documented upstream event name, source URL or runtime/source provenance, `Handling`, `FutureOnly`, normalized Purdex event name when installable, and reason when non-installable. Tests must compare the full event-name set exactly against that table.

Initial Claude Code non-installed candidates to verify and classify include:

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

Add an exact Codex `0.124.0` upstream catalog table before implementation. The table must include every current documented upstream event name, source URL or runtime/source provenance, `Handling`, `FutureOnly`, normalized Purdex event name when installable, and reason when non-installable. Tests must compare the full event-name set exactly against that table.

Initial known ignored/unsupported candidates to verify and classify:

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

Add an exact OpenCode `1.14.23` plugin event table before implementation. The table must include every current documented/runtime plugin event key, source URL or runtime/source provenance, `Handling`, normalized Purdex event name when installable, payload paths consumed by the template, and reason when non-installable. Tests must compare the full event-key set exactly against that table.

Explicitly document all current OpenCode plugin events not installed/mapped as ignored/unsupported.

OpenCode template/spec parity and CheckHooks event installation must use only `IsInstallableHookSpec(spec) == true`. Ignored/unsupported OpenCode upstream events must appear in the catalog but must not be expected in the managed plugin body and must not be marked installed by `CheckHooks`.

### 2.3 Codex Feature Flag Contract

Install:

- Ensure `~/.codex/config.toml` exists.
- Preserve existing TOML semantic keys/values; comments, formatting, and ordering are not guaranteed.
- Preserve an existing `config.toml` file mode; new config files default to owner-readable/writeable (`0600`).
- Ensure `[features].codex_hooks = true`.
- Parse both existing `config.toml` and `hooks.json` before writing either file; missing files are treated as empty config/hook maps.
- If either parse fails, return before writing; both files must remain byte-for-byte unchanged.
- After successful parse, write `hooks.json` before enabling `config.toml`; hook write failure must leave `config.toml` byte-for-byte unchanged so failed install cannot enable `features.codex_hooks` without current Purdex hooks.

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
- The first `pdx` subcommand token must be `hook`; wrapper-like commands such as `pdx exec hook ...` are not Purdex-owned hook commands.
- `--agent cc` must exist.
- Final non-empty token must equal the event key.

`filterOutPdx` should remove only commands that are valid Purdex hook commands for `cc`, not arbitrary `pdx hook --agent codex` commands.

For checker validation, event matching is per-key strict: the final command token must equal the settings event key. For remove/reinstall filtering, remove any well-formed `pdx hook --agent cc <owned-cleanup-event>` entry even if it is currently filed under the wrong Claude event key; this cleans stale Purdex-owned commands while preserving non-cc commands and unknown cc event tokens.

### 2.6 OpenCode Version Status

Add `opencodeHooksSupportedVersion = "1.14.23"` and return `SupportedVersion` / `ExceedsSupport` in all `CheckHooks` paths for parity with cc/codex.

### 2.7 OpenCode Mapping Verification Gate

Before changing `renderManagedPlugin`, capture or verify every OpenCode `1.14.23` event key and payload path consumed by the managed template, including `session.*`, `permission.asked`, `question.asked` if kept, prompt/message events, `tool.execute.before`, and `tool.execute.after`.

The verification commit must add a checked-in provenance artifact under `internal/agent/opencode/testdata/opencode-1.14.23-*` with:

- exact `opencode --version` output
- source/docs URL and commit/tag, or a captured runtime trace
- exact event keys consumed by `renderManagedPlugin`
- minimal JSON payload fixtures containing every field the template reads
- tests that fail if the fixtures no longer support the template mapping

If any template-consumed event lacks stable verification, keep the production template unchanged in this hotfix and limit OpenCode changes to catalog classification, version reporting, and tests that document the uncertainty.

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
| HC2 | `TestNonInstallableHookSpecsMustNotEmitStatus` | provider catalog tests fail if any ignored/unsupported spec has non-empty `EmitsStatus` |
| HC3 | `TestCCEventsClassifyKnownUpstreamHooks` | full documented cc hook set matches the checked-in version-pinned catalog table |
| HC3b | `TestProviderEventsPreserveHandling` | ignored/unsupported specs round-trip through each provider `Events()` defensive copy |
| HC4 | `TestCodexEventsClassifyCurrentDocs` | `PreToolUse`/`PostToolUse` and all current documented non-installed Codex events are explicitly classified |
| HC5 | `TestOpenCodeEventsClassifyPluginSurface` | OpenCode plugin docs events not mapped are explicitly classified |
| HC6 | `TestInstallableEventNamesStayStable` | installable sets remain cc=9, codex=9, opencode=8 after upstream catalog expansion |
| HC6b | `TestInstallableFilteringUsesHandling` | synthetic ignored/unsupported specs are excluded from event-name helpers, checker completeness, and OpenCode template parity |
| HC7 | `TestStaleNonInstallablePdxArtifactsDoNotBlockInstall` | stale Purdex-owned retired entries set `Managed=true`, do not affect `Installed`, and are removed by remove |
| HC7a | `TestUnknownProviderPdxArtifactsArePreserved` | unknown event tokens using `pdx hook --agent <provider>` are not treated as Purdex-owned and survive remove |
| HC7b | `TestOwnedArtifactsUnderUnknownKeysAreRemoved` | known owned event tokens are removable even when filed under an unknown hook key |

### 3.2 Codex Feature Flag Tests

File: `internal/agent/codex/hooks_test.go`

Tests:

| ID | Test | Red Assertion |
|---|---|---|
| CF1 | `TestCodexInstallHooks_EnablesFeatureFlag` | after install, `~/.codex/config.toml` has `[features].codex_hooks = true` |
| CF2 | `TestCodexInstallHooks_PreservesExistingConfig` | unrelated TOML keys survive install |
| CF2b | `TestCodexInstallHooks_MergesExistingFeaturesTable` | existing `[features]` entries survive and `codex_hooks` flips true |
| CF2c | `TestCodexInstallHooks_MalformedConfigReturnsError` | malformed TOML returns error and does not overwrite file |
| CF2d | `TestCodexInstallHooks_ParseFailureDoesNotPartiallyWrite` | malformed `config.toml` or `hooks.json` leaves both files byte-for-byte unchanged |
| CF2e | `TestCodexInstallHooks_PreservesExistingConfigMode` | existing `config.toml` mode, e.g. `0600`, is preserved after install |
| CF2f | `TestCodexInstallHooks_HooksWriteFailureLeavesConfigUnchanged` | hooks write failure returns error and leaves `config.toml` byte-for-byte unchanged |
| CF2g | `TestCodexInstallHooks_NewConfigUsesOwnerOnlyMode` | newly-created `config.toml` is written with `0600` permissions |
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
| CP6 | `TestCodexRemoveHooks_PreservesUnknownEventTokens` | `pdx hook --agent codex Bogus` survives remove and does not set `Managed=true` |
| CP7 | `TestCodexRemoveHooks_RemovesOwnedEventUnderUnknownKey` | `pdx hook --agent codex SessionStart` under an unknown hooks key is removed |
| CP8 | `TestIsPdxCommandCodexForEvent_RequiresHookSubcommand` | `pdx exec hook --agent codex SessionStart` is rejected |

CP2, CP3, and CP5 fixtures must include `features.codex_hooks=true` unless the test is specifically about feature flag handling.

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
| CS6 | `TestMergeClaudeHooks_RemovesWrongEventCcPdxEntry` | remove/reinstall deletes well-formed `--agent cc` commands even when filed under the wrong event key |
| CS7 | `TestCCCheckHooks_ManagedReflectsOwnedPdxUnderUnknownKey` | known owned cc event token under an unknown hook key sets `Managed=true` |
| CS8 | `TestIsPdxCommand_RequiresHookSubcommand` | `pdx exec hook --agent cc SessionStart` is rejected |
| CS9 | `TestMergeClaudeHooks_RemovesOwnedPdxUnderUnknownKey` | remove deletes known owned cc event tokens even when stored under an unknown settings key |

### 3.5 OpenCode Plugin Mapping / Version Tests

Files:

- `internal/agent/opencode/plugin_template_test.go`
- `internal/agent/opencode/hooks_test.go`
- `internal/agent/opencode/events_test.go`

Tests:

| ID | Test | Red Assertion |
|---|---|---|
| OC1 | `TestOpenCodePluginTemplate_UsesVerifiedEvents` | template uses verified current event keys and payload paths, not stale API assumptions |
| OC1a | `TestOpenCodeTemplateEventContractsDocumented` | checked-in provenance fixture names every consumed OpenCode event key and payload field |
| OC2 | `TestTemplateSpecsParity` | emitted template events match installable specs only |
| OC3 | `TestOpenCodeEvents_ClassifiesCurrentPluginEvents` | current documented OpenCode plugin events are classified |
| OC4 | `TestOpenCodeCheckHooks_ReportsSupportedVersion` | table tests every `CheckHooks` return path includes `SupportedVersion=1.14.23` |
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

- HC1-HC1b fail.

Green:

- Add `HookHandling` field/types.
- Add `EffectiveHookHandling` and `IsInstallableHookSpec` in `internal/agent/provider.go`.
- Update `HookInstaller.Events` and `HookEventSpec` comments to say `Events()` returns the classified upstream catalog, not the installable subset.
- Keep existing provider event declarations unchanged.
- Ensure `SupportedStatuses` tests stay green and use effective handling only where classification matters.

Run:

- `go test ./internal/agent/... -count=1`

### Commit 1b — `fix(agent): scope hook installers to installable specs`

Red:

- HC6b fails.
- Synthetic ignored/unsupported specs fail installer-name filtering and OpenCode template parity filtering until consumers use `IsInstallableHookSpec`.

Green:

- Migrate cc/codex/opencode event-name helpers, installers, checkers, and OpenCode template/spec parity to `IsInstallableHookSpec`.
- Split tests into full upstream catalog assertions and installable set assertions.
- Preserve installable sets before provider-specific changes: cc=9, codex=9, opencode=8.
- Do not add upstream ignored/unsupported declarations in this commit.

Run:

- `go test ./internal/agent/... -count=1`

### PR 2 Commit 1 — `feat(agent): classify upstream hook catalog`

Deferred to PR 2. Do not execute this before PR 1's Codex current correctness and explicit cleanup ownership commits; otherwise cleanup tests either cannot pass or risk conflating the full catalog with Purdex ownership.

Red:

- HC2-HC6 fail.

Green:

- Add known upstream ignored/unsupported declarations for cc/codex/opencode.
- Add exact version-pinned provider catalog tables and full catalog set assertions.
- Copy `Handling` through every provider `Events()` defensive copy.
- Assert every newly added non-installable declaration has explicit `ignored`/`unsupported` handling and empty `EmitsStatus`.
- Keep ignored/unsupported entries out of install completeness and OpenCode template parity.
- If classification introduces historically installed retired events, add their tokens to the provider owned cleanup sets introduced in Commit 2b.
- Keep unknown ignored/unsupported upstream event tokens out of cleanup ownership unless they are explicitly documented as historically installed by Purdex.
- Keep runtime status derivation unchanged for installable status/detail events.

Run:

- `go test ./internal/agent/... -count=1`

### Commit 2 — `fix(agent/codex): require hooks feature flag and current PermissionRequest`

Red:

- CF1-CF5 and CF2b-CF2g fail.
- CP1-CP3 fail.

Green:

- Add TOML read/write helpers for `~/.codex/config.toml`.
- Preflight-parse both `config.toml` and `hooks.json`; any parse failure leaves both files byte-for-byte unchanged.
- Preserve existing `config.toml` file mode; new files default to `0600` and are covered by a dedicated test.
- Install writes `hooks.json` before enabling `features.codex_hooks`, so hook write failure leaves config unchanged.
- Install enables `features.codex_hooks` only after hook write succeeds.
- Check blocks when feature flag missing/false.
- Preserve TOML semantic keys while accepting formatting/comment/order rewrites.
- `PermissionRequest.FutureOnly=false`.
- Update `codexHooksSupportedVersion` to `0.124.0`.

Run:

- `go test ./internal/agent/codex ./internal/agent -count=1`

### Commit 2b — `fix(agent): harden hook cleanup ownership`

Red:

- HC7-HC7b fail.
- CP6-CP8 fail.
- CS7-CS9 fail.

Green:

- Add explicit provider-local owned cleanup sets for cc and Codex, separate from the full provider catalog.
- Use owned cleanup sets for `Managed` and remove; unknown event tokens remain user/third-party-owned by default.
- Scan all configured hook keys on remove/managed detection, then decide ownership from tokenized command shape plus owned event token.
- Require `hook` as the first `pdx` subcommand for cc and Codex command recognition.
- Preserve other-provider `pdx hook` entries and unknown same-provider event tokens.

Run:

- `go test ./internal/agent/cc ./internal/agent/codex -count=1`

### Commit 3 — `fix(agent/codex): remove empty hook keys on uninstall`

Red:

- CP4-CP5 fail.

Green:

- `mergeCodexHooks(remove=true)` deletes event key when no entries remain.
- Preserve third-party matcher groups.
- Ensure CP5 fixtures include `features.codex_hooks=true` so the test isolates empty-key behavior.
- Keep CheckHooks strict present-but-empty behavior for manually broken files.

Run:

- `go test ./internal/agent/codex -count=1`

### Commit 4 — `fix(agent/cc): validate hooks by agent and event`

Red:

- CS1-CS6 fail.

Green:

- Add quote-aware tokenizer / per-event validator for cc.
- Use strict validator for `CheckHooks`.
- Use cc-scoped filtering for install/remove.
- Remove stale well-formed `--agent cc` Purdex commands even when filed under the wrong event key.

Run:

- `go test ./internal/agent/cc -count=1`

### Commit 5a — `fix(agent/opencode): report hook support version`

Red:

- OC4-OC5 fail.

Green:

- Add supported version reporting for OpenCode.
- Cover every `CheckHooks` return path: missing plugin, unmanaged plugin, path resolution failure, managed body drift, and fully installed.

Run:

- `go test ./internal/agent/opencode ./internal/agent -count=1`

### Commit 5b — `test(agent/opencode): document template event contracts`

Red:

- OC1a fails until current OpenCode `1.14.23` event keys and required payload fields for every template-consumed event are documented from source/runtime verification.

Green:

- Add checked-in provenance under `internal/agent/opencode/testdata/opencode-1.14.23-*` with version output, source/docs URL or runtime trace, event keys, and minimal payload fixtures.
- Add fixture-driven tests for every event key and payload path consumed by `renderManagedPlugin`.
- Do not change `renderManagedPlugin` unless the verified contract proves `chat.message` is stale.

Run:

- `go test ./internal/agent/opencode -count=1`

### Commit 5c — `fix(agent/opencode): refresh plugin event mapping`

Only needed if Commit 5b proves any existing managed template event key or payload path is stale.

Red:

- OC1 fails.
- OC2 fails if template/spec parity is not scoped to installable specs.

Green:

- Update event mappings to the verified current event and payload contract.
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
- Codex install preflights both config and hooks files; malformed input leaves both files unchanged.
- Codex install preserves existing `config.toml` permissions and does not enable `features.codex_hooks` if hooks write fails.
- Codex `PermissionRequest` is required, not FutureOnly.
- Codex remove does not leave empty event keys that Purdex itself later reports as broken.
- Claude CheckHooks rejects wrong-agent and wrong-event `pdx hook` commands.
- Hook command matching requires `hook` as the first `pdx` subcommand; wrapper-like commands such as `pdx exec hook ...` are not Purdex-owned hook commands.
- Ignored/unsupported hook declarations never enter install completeness.
- Cleanup ownership is provider-local and explicit: owned current/retired event tokens are removable across all configured hook keys, while unknown same-provider tokens are preserved.
- OpenCode plugin event mapping is verified against current OpenCode docs/runtime shape with checked-in provenance fixtures.
- OpenCode CheckHooks reports version support fields like cc/codex.
- OpenCode icon registry has explicit tests.
- No files under `internal/module/agent/*`, `internal/agent/probe/*`, `internal/store/*`, or tab rendering are modified.

---

## 6. Risks

- TOML round-trip may rewrite user formatting in `~/.codex/config.toml`; semantic keys/values are preserved, but comments/order are not guaranteed.
- Codex install writes `hooks.json` before config to avoid feature-flag false enablement. If config write fails after hooks write succeeds, the next check reports repairable state instead of falsely reporting installed hooks as active.
- Full upstream hook catalog classification may grow long. Keep it declarative and provider-local; do not add a central runtime dispatcher.
- Provider owned cleanup allowlists must be maintained when Purdex retires an event. Removing a historically installed event from the installable set must not remove it from the cleanup set unless a migration intentionally abandons cleanup.
- OpenCode event mapping may require runtime verification if docs do not identify stable event keys and payload paths. If uncertain, split into a smaller PR that only adds tests and version reporting first.
- Existing tests may assume exactly 9 cc / 9 codex / 8 opencode events. Update those tests to count installed events separately from known upstream declarations.

---

## 7. Review-Sized PR Slicing

Codex review found that the foundation PR could still ship false-green Codex hook state if Codex current-runtime correctness stayed deferred. The review-sized split is therefore adjusted as follows:

### PR 1 — Hook Foundation + Codex Current Correctness

Include:

- Commit 1a hook handling helpers.
- Commit 1b installable-subset filtering for installers/checkers/template parity.
- Explicit owner-scoped cleanup for cc/codex Purdex hook artifacts so remove scans all configured keys but only deletes provider-owned current/retired event tokens.
- Unknown same-provider event tokens are preserved by default to avoid deleting user-authored, future upstream, or third-party hook entries.
- Codex `features.codex_hooks=true` install/check gating with parse-before-write preflight.
- Codex install preserves config file mode and writes hooks before enabling the feature flag to avoid partial false activation.
- Codex `PermissionRequest` requiredness and supported version update.
- Codex remove empty-key cleanup for owned installable and stale/retired Codex hook entries.

Rationale:

- These changes share the same user-visible contract: `CheckHooks.Installed=true` must mean the agent will actually load and emit Purdex hooks.
- The size is still reviewable because it is limited to `internal/agent/provider.go`, provider hook code/tests, and this plan document.
- It remains outside lights runtime/probe/module files.

### PR 2 — Upstream Catalog Classification

Include:

- Exact version-pinned cc/codex/opencode upstream catalog tables.
- Real ignored/unsupported declarations and full catalog set assertions.
- Provider catalog validation that newly added non-installable upstream entries must explicitly set `Handling` and must not emit statuses.

### PR 3 — Remaining Claude Strictness, If Needed

Include only behavior not already covered by PR 1 owner-scoped cleanup/checker strictness, such as additional reinstall/remove edge cases that are not required for installable-subset safety.

### PR 4 — OpenCode Version + Event Contract

Include:

- OpenCode supported-version reporting.
- Checked-in OpenCode event contract provenance fixtures for every template-consumed event.

### PR 5 — OpenCode Mapping Refresh, Conditional

Only create this PR if PR 4 proves an existing managed template event key or payload path is stale. Keep it separate because it can affect lights frame/projection behavior.

### PR 6 — OpenCode Icon Guard

Coverage-only SPA test. May be bundled with PR 4 if review load is low; otherwise keep as a small test-only PR.
