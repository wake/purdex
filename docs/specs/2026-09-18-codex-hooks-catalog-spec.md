# Codex hooks catalog refresh for codex-cli 0.153.x

Issue: #1159. Branch: `worktree-codex-hooks-catalog`. Status: approved 2026-09-18 (decisions ① Interrupt → Stop semantics, ② two PRs).

## 1. Problem

The daemon's codex hook integration was pinned to codex-cli 0.124.0. codex-cli 0.153.4 (installed on mlab and air26) changed the hook surface:

| Fact | Evidence |
|---|---|
| `Notification` and `StopFailure` are not codex hook events. Codex ignores unknown keys silently. | Official hooks docs list exactly: SessionStart, SessionEnd, SubagentStart, SubagentStop, PreToolUse, PermissionRequest, PostToolUse, PreCompact, PostCompact, UserPromptSubmit, Stop, Interrupt. `~/.codex/config.toml` `[hooks.state]` holds `trusted_hash` for 8 entries; the two missing are these. Three days of mlab logs: 0 `PdxNotification` from codex vs 512 from cc. |
| `PostToolUse` is supported by codex but the catalog marks it `HookHandlingUnsupported`, so it is never written and `deriveCodexStatus` has no case for it. | `internal/agent/codex/events.go:106-113`, `status.go` |
| `Interrupt` (turn cancelled by the user) exists upstream and is not in the catalog. | Official docs; payload `turn_id`, `permission_mode` |
| Codex clamps the `SessionEnd` hook timeout to 3 s; we write 5. | Startup output: `warning: clamping SessionEnd hook timeout to 3s in /Users/wake/.codex/hooks.json` |
| `[features].codex_hooks` is a deprecated alias of `[features].hooks` (default true). We write only the alias and `CheckHooks` reads only the alias. | Startup output: `deprecated: [features].codex_hooks is deprecated. Use [features].hooks instead.`; `hooks.go:399-419` |
| `codexHooksSupportedVersion = "0.124.0"` and no test pins it; codex has no frozen fixtures (opencode has `testdata/opencode-1.14.23-*`). | `hooks.go:15`; `internal/agent/opencode/testdata/` |
| Install/Remove do not run on daemon boot; `hooks.json` is rewritten only on explicit Install (`POST /api/hooks/codex/setup`, `pdx setup`). | `hooks.json` mtime 2026-05-22 despite several deploys |

Consequences today: codex sessions never raise the `Notification`-driven desktop notification (they still notify on `PermissionRequest` and `Stop`); the light cannot move `waiting → running` from a hook after a permission grant (a screen-change probe compensates); after Ctrl-C the light stays `running` until a probe catches up; two warnings print at every codex start.

## 2. Scope

Two PRs. SPA is untouched. Nothing runs on daemon boot; the user reinstalls hooks once from Host › Hooks after deploy (new entries need one `/hooks` approval in codex).

### PR 1 — catalog and installer (`internal/agent/codex`)

**2.1 Catalog (`events.go`)**

| PurdexName | Change |
|---|---|
| `PdxNotification` | `Handling: HookHandlingIgnored`. Stays in the catalog so `LookupByPurdexName` and `DeriveStatus` keep working for any in-flight or hand-installed entry; no longer installable. |
| `PdxStopFailure` | Same as above. |
| `PdxPostToolUse` | Installable. `EmitsStatus: [running]`, `Lifecycle: None`, `FutureOnly: false`, `Description: "Tool call completed (signals running after permission grant)"`. |
| `PdxInterrupt` (new) | `UpstreamKeys: ["Interrupt"]`, `Lifecycle: LifecycleStop`, `EmitsStatus: [idle]`, `FutureOnly: false`. Interrupt means the turn ended; it takes the Stop path so codex broker proxy refs detach by `turn_id` exactly like `PdxStop`. |
| `PdxPreCompact`, `PdxPostCompact` (new) | `Handling: HookHandlingIgnored`, `Lifecycle: None`, `EmitsStatus: []`. Declared so the docs-pin test is complete; never installed. |

Update `expectedCodexCurrentUpstreamEventNames` in `events_test.go` to the 12 upstream names above with the fetch date; the bidirectional check (catalog upstream keys ⇔ pinned list) must pass with the ignored entries excluded from the installable set.

**2.2 Status derivation (`status.go`)**

- `PdxPostToolUse` → `DeriveResult{Valid: true, Status: running, Detail: {tool_name}}` (mirror `internal/agent/cc/status.go:31-42`).
- `PdxInterrupt` → `DeriveResult{Valid: true, Status: idle, Detail: {turn_id}}`. Reuse the `turn_id` extraction `parseCodexTurnID` already used for Stop.

**2.3 Installer (`hooks.go`)**

- Retired list `codexRetiredUpstreamEvents = {"Notification", "StopFailure"}`. `mergeCodexHooksFile` (both install and remove paths) strips pdx-owned entries under retired keys and drops the key when it empties; non-pdx entries under those keys are preserved. Extend `codexOwnedCleanupEventNames()` so the retired keys and their `Pdx*` names stay recognised as pdx-owned for cleanup only.
- Per-event timeout: `SessionEnd` → 3, everything else stays 5. Encode as a small table, not a special case in the loop.
- Feature flag: `setCodexHooksFeature` writes `features.hooks = true` and deletes `features.codex_hooks` if present. `codexHooksFeatureEnabled` returns true when either key is `true`; absent both → true (upstream default is enabled). Keep the existing "flag explicitly false blocks" behaviour for either key.
- `codexHooksSupportedVersion = "0.153.4"`.
- Add a round-trip test proving `[hooks.state."<path>:<event>:<n>:<m>"] trusted_hash` survives `readCodexConfig → setCodexHooksFeature → writeCodexConfig` (currently zero coverage).

**2.4 Module side**

No change expected in `internal/module/agent`. `PdxPostToolUse` (LifecycleNone + running) takes the generic narrow-update path; `PdxInterrupt` (LifecycleStop) takes the existing Stop path. Add one `frame_ops_test.go` case per event showing: (a) PostToolUse moves a `waiting` codex frame to `running`; (b) Interrupt moves `running` to `idle` and detaches the proxy ref for that `turn_id`. The error guard (`handler.go:346-371`) is unchanged: `PdxInterrupt` has `LifecycleStop`, so like `PdxStop` it may clear `error` for non-opencode agents.

The `ProbeIntentKindScreenChange` probe stays as is; retiring it is a follow-up once 0.153 is the floor everywhere.

**2.5 Tests to update (known list)**

`events_test.go`: `expectedCodexInstallableEventNames`, `expectedCodexEventNames`, `expectedCodexCatalogHandling`, `expectedCodexPreservedMetadata`, `TestCodexEventsFutureOnlyFlags`, upstream pin list. `hooks_test.go`: `TestCodexInstallHooks_Writes9EventsAfterExpansion` (rename; now 10 installable: SessionStart, UserPromptSubmit, SubagentStart, SubagentStop, Stop, PermissionRequest, SessionEnd, PreToolUse, PostToolUse, Interrupt), `expectedCodexInstallerNames`, `TestCodexInstallHooks_EnablesFeatureFlagAndPreservesConfig` (assert `hooks = true`, `codex_hooks` absent), `TestCodexCheckHooks_FeatureFlagMissingOrFalseBlocks` (absent no longer blocks), `TestCodexOwnedCleanupEventNames_CleansLegacyAndNew`, new retired-cleanup tests (install strips, remove strips, non-pdx preserved, key dropped when empty). `status_test.go`: PostToolUse, Interrupt. `internal/agent/drift_test.go`: add `PdxPostToolUse` and `PdxInterrupt` fixtures. `provider_test.go`: `SupportedStatuses` unchanged (running/idle already present) — assert explicitly.

### PR 2 — frozen fixtures and version test

- `internal/agent/codex/testdata/codex-0.153.4-{version.txt,manifest.json,events.json,source.md}` and `codex-0.153.4-payloads/<PurdexName>.json`, one real payload per installable event, following the opencode layout and manifest fields (`tag`, `version`, `schemaStage`, `payloadFixtureDir`, `catalogSummary{installable,ignored,unsupported}`; `commitSha` may be the codex-cli release tag's commit or `"n/a"` with a note).
- Payload sources: `SessionStart`, `UserPromptSubmit`, `PreToolUse`, `Stop`, `SessionEnd`, `SubagentStart`, `SubagentStop`, `PermissionRequest` from mlab `~/.config/pdx/agent_events.db` `agent_trace_steps.payload_json WHERE kind='trigger'` (field `raw_event`), scrubbed of paths/prompts/tool inputs beyond what the parser reads. `PostToolUse` and `Interrupt` captured by running codex-cli 0.153.4 in a scratch directory with a project-level `<scratch>/.codex/hooks.json` whose command is `tee`/`cat >> file` — never touch `~/.codex`. Record the capture procedure in `source.md`.
- Tests: `TestCodexEvents_ClassifyAgainstFrozenManifest` (catalog ⇔ `events.json`, bidirectional), a payload contract test that runs every fixture through `DeriveStatus` and asserts `Valid` and the expected status, and a `CheckHooks` version test with a fake `codex --version` on PATH (mirror `assertOpenCodeSupportFields` / `fakeOpenCodeVersion` in `internal/agent/opencode/hooks_test.go:525-548`): `0.153.4` → `exceedsSupport=false`, `0.160.0` → true, `0.124.0` → false.

## 3. Acceptance (real machine, mlab)

1. Deploy daemon; Host › Hooks › codex shows `Hook Support Through 0.153.4`, no version warning.
2. Press Install; `~/.codex/hooks.json` has exactly the 10 installable keys with pdx entries, no `Notification`/`StopFailure` keys (unless a non-pdx entry lived there), `SessionEnd` timeout 3; `~/.codex/config.toml` has `[features] hooks = true` and no `codex_hooks`; `[hooks.state]` entries for unchanged events still present.
3. Start codex in a tmux pane: no `deprecated` and no `clamping` line; `/hooks` asks to approve only the new entries (PostToolUse, Interrupt).
4. Run a tool call; `pdx.log` shows `PdxPostToolUse` for that session and the light stays/returns `running`. Ctrl-C mid-turn; log shows `PdxInterrupt` and the light goes `idle` without waiting for a probe.
5. `pdx setup --agent codex --remove` leaves no pdx entries and does not touch `[features]`.

## 4. Non-goals

- SPA changes (notification content for probe-driven `error` on codex is a separate follow-up).
- Retiring `ProbeIntentKindScreenChange`.
- Preserving comments in `config.toml` (BurntSushi round-trip already drops them).
- Writing hooks on daemon boot.
- Changing cc or opencode pins (`2.1.114`, `1.14.23`) — separate issue.

## 5. Size guard

PR 1 ≈ 500 lines including tests; PR 2 ≈ 300 lines, mostly JSON. If PR 1 crosses 800 lines, split the feature-flag/timeout work into its own PR first.
