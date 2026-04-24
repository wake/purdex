# Agent Title Metadata TDD Plan

- **Date**: 2026-04-24
- **Spec**: `docs/specs/2026-04-24-agent-title-metadata-spec.md`
- **Baseline**: latest `origin/main`

## Coordination Notes

`lights-phase-2b` is active. Avoid touching `SubagentDots.tsx`, `TabIcon.tsx`, `SortableTab.tsx`, `InlineTab.tsx`, and `renderInlineTabIcon.tsx` unless a merge conflict forces it.

In `useTabDisplay.ts`, add title resolution while preserving `subagentCount` and future `subagentRefs`. In `useAgentStore.ts`, only remove `setCcStatus()` title mirroring and adjust related cleanup behavior.

## Commit 1 — `feat(session): expose tmux pane titles`

### Scope

- `internal/tmux/executor.go`
- `internal/module/session/provider.go`
- `internal/module/session/service.go`
- focused session/tmux tests
- SPA session type file if needed

### TDD sequence

1. Write failing tests first:
   - `TestListSessionsIncludesPaneTitleMetadata`
   - `TestListSessionsContinuesWhenPaneMetadataFails`
   - `TestListSessionsUsesActivePaneTitleForMultiPaneSession`
   - `TestHashSessionsChangesWhenPaneTitleChanges`
   - executor/helper test for title sanitization with tab/newline/control chars
2. Run focused Go tests and confirm failure.
3. Implement minimal tmux active pane metadata path.
4. Re-run focused Go tests until green.

### Implementation details

- Add `TmuxPaneMetadata` or equivalent.
- Add `ActivePaneMetadata(sessionName string)` or equivalent.
- Use target `=<session_name>:` and individual `display-message` calls.
- Add `SessionInfo.PaneTitle` / `WindowName`; populate `CurrentCommand`.
- Metadata failures are non-fatal when `ListSessions()` succeeds.
- Sanitize metadata strings before exposing them.

## Commit 2 — `feat(spa): resolve agent titles from tmux metadata`

### Scope

- `spa/src/hooks/useTabDisplay.ts`
- `spa/src/hooks/useTabDisplay.test.ts`
- `spa/src/components/StatusBar.tsx`
- `spa/src/components/StatusBar.test.tsx`
- `spa/src/stores/useAgentStore.ts`
- `spa/src/stores/useAgentStore.test.ts`
- `spa/src/stores/useUISettingsStore.ts`
- `spa/src/stores/useUISettingsStore.test.ts`
- `spa/src/components/settings/TerminalSection.tsx`
- `spa/src/components/settings/TerminalSection.test.tsx`
- `spa/src/lib/sync/contributors/preferences.ts`
- locales

### TDD sequence

1. Write failing tests first:
   - `useTabDisplay` uses `pane_title - sessionLabel` when `dynamicTabName=true` and agent type exists.
   - `useTabDisplay` ignores `oscTitles` when `pane_title` is absent.
   - `useTabDisplay` preserves plain `sessionLabel` when `dynamicTabName=false`.
   - Existing tab consumer tests use `dynamicTabName` instead of `showOscTitle` without touching icon/subagent assertions.
   - `StatusBar` shows title left of the terminal/stream switch when `showAgentTitleInStatusBar=true`.
   - `StatusBar` hides title when `showAgentTitleInStatusBar=false`.
   - `setCcStatus` stores `ccStatus` without setting `oscTitles`.
   - `clearHostAgentStatus` clears only `ccStatus` and does not delete terminal `oscTitles`.
   - UI settings migration maps old `showOscTitle` to both new flags.
   - Terminal settings renders `Dynamic tab name` and `Show in status bar` controls.
2. Run focused Vitest and confirm failure.
3. Implement minimal SPA changes.
4. Re-run focused Vitest until green.

### Implementation details

- Use `session.pane_title || null` as the resolved title when agent type exists.
- Keep `oscTitles` and `setOscTitle()` plumbing for now, but no UI resolution uses it.
- Replace `showOscTitle` with `dynamicTabName` and `showAgentTitleInStatusBar`.
- Bump `useUISettingsStore` persist version.
- Update sync preferences to the new keys.

## Commit 3 — `feat(hosts): show agent title integration checks`

### Scope

- `internal/module/agent/handler.go` / tests or focused helper files
- `internal/tmux/executor.go` if tmux option/config helpers live there
- `spa/src/components/hosts/AgentsSection.tsx`
- new host components if useful
- `spa/src/components/hosts/AgentsSection.test.tsx`
- locales

### TDD sequence

1. Write failing backend tests first:
   - `GET /api/agent/title/status` returns `allow_set_title`, `installed`, `runtime_applied`, and `managed_config_path`.
   - `POST /api/agent/title/setup` install writes only the Purdex marker block and runtime-applies `allow-set-title`.
   - `POST /api/agent/title/setup` remove deletes only the marker block and does not force runtime rollback to off.
   - agent title capability response covers Claude env, Codex missing/disabled/configured/unparsable config, and OpenCode unknown.
2. Write failing SPA tests first:
   - top `Agent title` block renders before agent cards.
   - statusline extension UI is hidden for installed CC.
   - installed Claude/Codex/OpenCode cards show dynamic title capability rows.
   - uninstalled cards do not show dynamic title rows.
3. Run focused Go/Vitest and confirm failure.
4. Implement minimal backend endpoints/helpers.
5. Implement SPA components and wire into `AgentsSection`.
6. Re-run focused tests until green.

### Implementation details

- Endpoints:
  - `GET /api/agent/title/status`
  - `POST /api/agent/title/setup`
- POST body: `{ "action": "install" }` or `{ "action": "remove" }`.
- Response shape:
  - `allow_set_title`
  - `installed`
  - `runtime_applied`
  - `managed_config_path`
  - `error`
- Install writes/updates only the Purdex marker block in `~/.tmux.conf` and runtime-applies `allow-set-title`.
- Remove deletes only the marker block and reports if runtime remains on.
- Keep `set-titles` out of the contract.
- Per-agent rows are read-only and best-effort.

## Verification

Focused checks:

- `go test ./internal/tmux ./internal/module/session -count=1`
- `go test ./internal/module/agent -count=1` when commit 3 touches agent module
- `pnpm --prefix spa exec vitest run src/hooks/useTabDisplay.test.ts src/components/StatusBar.test.ts src/stores/useAgentStore.test.ts src/stores/useUISettingsStore.test.ts`
- `pnpm --prefix spa exec vitest run src/components/hosts/AgentsSection.test.ts src/components/settings/TerminalSection.test.ts`

Final checks:

- `make test`
- `pnpm --prefix spa run lint`
- `pnpm --prefix spa run build`

## Review Focus

- `SessionInfo.pane_title` is the title SOT.
- `setCcStatus()` and `clearHostAgentStatus()` do not write/delete `oscTitles`.
- `allow-set-title` install/remove does not touch `set-titles` or `set-titles-string`.
- lights-phase-2b subagent schema is not regressed.
- statusline UI is hidden without deleting backend code.
