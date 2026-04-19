# Codex Icon Variant — Design

Date: 2026-04-20
Status: Approved (design phase)
Scope: SPA only — no daemon / Electron changes

## Problem

Codex CLI sessions currently always render the Phosphor `OpenAiLogo` on the
tab (`spa/src/lib/agent-icons.tsx:18-20,35`). Claude Code already exposes a
`bot`/`star` icon choice via `useAgentStore.ccIconVariant` and the
`settings.terminal.cc_icon` setting. We want Codex to offer the same kind of
choice: the existing OpenAI logo, or the dedicated Codex logo.

## Goal

Mirror the CC icon-variant pattern for Codex:

- Two options — `openai` (current behavior) and `codex` (lobehub `codex.svg`)
- Persisted per user in `useAgentStore`
- Live-preview button group in Settings → Terminal, directly below the CC
  icon row
- Defaults to `openai` so existing users see no visual change

Non-goals: adding variants for other agents (gemini etc.); exposing the
colored `codex-color.svg` gradient; any backend work.

## Icon Choice

Use the monochrome `@lobehub/icons-static-svg/icons/codex.svg` imported via
`?react`. It paints with `currentColor`, so it inherits tab theme / state
colors the same way CC's `bot`/`star` SVGs do. The colored `codex-color.svg`
is rejected to keep tab visuals consistent.

## Changes

### 1. Store — `spa/src/stores/useAgentStore.ts`

- Add `export type CodexIconVariant = 'openai' | 'codex'`
- Add state field `codexIconVariant: CodexIconVariant` (default `'openai'`)
- Add action `setCodexIconVariant(variant: CodexIconVariant)` mirroring
  `setCcIconVariant`
- Persist: bump `version` from `4` to `5` and include `codexIconVariant` in
  `partialize`. No migration function — a missing key deserializes to the
  default, which matches existing behavior.

### 2. Icon registry — `spa/src/lib/agent-icons.tsx`

- Import `@lobehub/icons-static-svg/icons/codex.svg?react` as
  `CodexLobeSvg`; wrap with existing `wrapSvg` helper
- Rename the existing local `CodexIcon` component (which currently renders
  `OpenAiLogo`) to `CodexOpenAiIcon` so both variants have parallel names
- Declare an internal `CODEX_VARIANTS` map and re-export it as
  `CODEX_ICON_VARIANTS: Record<CodexIconVariant, AgentIconComponent>` —
  mirrors the existing `CC_VARIANTS` / `CC_ICON_VARIANTS` pair at
  `spa/src/lib/agent-icons.tsx:22-25,40`
- Extend `GetAgentIconOptions` with `codexVariant: CodexIconVariant`
- In `getAgentIcon`, the `'codex'` branch returns
  `CODEX_ICON_VARIANTS[options.codexVariant]`

### 3. Consumers

- `spa/src/hooks/useTabDisplay.ts` — read `codexIconVariant` from
  `useAgentStore` and pass it in the `getAgentIcon` options object alongside
  `ccVariant`
- Tests that seed `useAgentStore` state must include the new field:
  - `spa/src/hooks/useTabDisplay.test.ts`
  - `spa/src/features/workspace/components/InlineTab.test.tsx`
  - `spa/src/components/settings/TerminalSection.test.tsx`

### 4. Settings UI — `spa/src/components/settings/TerminalSection.tsx`

Add a second `SettingItem` row directly below the CC icon row with the same
structure: button group using `CODEX_ICON_VARIANTS` for live preview,
`aria-pressed`, and the same `hidden_hint` shown when
`tabIndicatorStyle === 'dot'`.

### 5. i18n — `spa/src/locales/en.json`, `spa/src/locales/zh-TW.json`

Add keys:

- `settings.terminal.codex_icon.label`
- `settings.terminal.codex_icon.desc`
- `settings.terminal.codex_icon.openai`
- `settings.terminal.codex_icon.codex`
- `settings.terminal.codex_icon.hidden_hint`

Wording parallels the CC icon strings. `spa/src/locales/locale-completeness.test.ts`
fails if a key is added to only one locale, so both files must land together.

### 6. Tests

- `spa/src/lib/agent-icons.test.tsx`
  - codex returns `CODEX_ICON_VARIANTS.openai` when `codexVariant='openai'`
  - codex returns `CODEX_ICON_VARIANTS.codex` when `codexVariant='codex'`
  - Distinct components for the two codex variants
  - Update the existing "returns the codex icon regardless of ccVariant" test
    to pass a `codexVariant` and verify the opposite: codex now ignores
    `ccVariant` but respects `codexVariant`
- `spa/src/components/settings/TerminalSection.test.tsx`
  - Clicking the Codex icon button toggles `codexIconVariant` in the store
  - Initial state setup includes `codexIconVariant: 'openai'`

## Data / Persistence

localStorage key `purdex-agent` (unchanged). Persist payload grows by one
string field. Version bump `4 → 5` isolates this change from older
snapshots; Zustand's `persist` middleware drops state on a version mismatch
when no `migrate` is supplied, which is acceptable since the affected keys
(`tabIndicatorStyle`, `ccIconVariant`, `showOscTitle`) all have safe
defaults. This matches the project's "Alpha 階段不需 persist migration"
policy.

There is a second drop path via `syncManager.register(STORAGE_KEYS.AGENT,
useAgentStore)` at `spa/src/stores/useAgentStore.ts:254` — cross-tab sync
broadcasts carry the version number, and older tabs still on v4 will have
their broadcasts dropped by a tab that has upgraded to v5. Same safe-default
guarantee applies, so no `migrate` hook on `SyncableSpec` is needed.

## Risks / Edge Cases

- **Tab rendering ignores the new variant on dot mode** — same as CC; the
  existing `hidden_hint` handles this.
- **Test fixtures** — any test that calls `useAgentStore.setState({...})`
  with a full slice must add the new field. Grep for `ccIconVariant` to
  catch them all.
- **Persist version bump** resets users' existing CC icon / tab indicator
  choices once. Acceptable trade-off in alpha and consistent with prior
  bumps.

## Out of Scope

- Other agent types (gemini, amp, …)
- Colored `codex-color.svg` variant
- Exposing the setting anywhere outside Settings → Terminal
- Changing defaults based on detected agent version

## Acceptance

- `cd spa && pnpm run lint` clean
- `cd spa && npx vitest run` all green, including the new assertions and
  `locale-completeness.test.ts`
- Manual check in a Codex session: toggling between OpenAI and Codex icons
  in Settings updates the tab icon live; default on a fresh profile is
  OpenAI
- `CHANGELOG.md` entry added per project convention (CLAUDE.md: each
  merged PR bumps `VERSION` + `CHANGELOG.md`)
