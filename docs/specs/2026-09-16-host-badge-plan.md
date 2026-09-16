# Host Badge — Implementation Plan

Spec: `docs/specs/2026-09-16-host-badge-spec.md` · Branch `worktree-host-color-v2` · SPA only.
Replaces the alpha.352 host color mark.

Rules for every task:
- TDD: failing test first, run red, implement, run green.
- Every Bash command prefixed with `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/host-color-v2/spa && `
  (or `.../host-color-v2 && ` for git).
- Tests: `pnpm exec vitest run <files>` (never `npx`). Query by role / aria-label / data-testid.
- Parallel subagents share the worktree: touch only the task's files; `git add` new files, then
  `git commit --only <paths>` as separate commands; never `git add -A`, stash or reset. git writes
  need `dangerouslyDisableSandbox: true`.
- Commit trailer `Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>`.
- Stores in tests: merge-mode `setState`, listing the mutable fields explicitly.
- Lint rule `react-hooks/set-state-in-effect` is enforced.

Waves: **A** = T1, T2 (parallel) → **B** = T3, T4 (parallel) → **C** = T5 → **D** = T6 → **E** = T7.

---

## T1 — host identity data: icon + weight + sanitizer rename

Files: `spa/src/lib/host-color.ts` (+test), `spa/src/stores/useHostStore.ts` (+test),
`spa/src/lib/sync/contributors/hosts.ts` (+ `hosts.test.ts`).

- `export const DEFAULT_HOST_ICON = 'Desktop'`.
- `export function isIconWeight(v: unknown): v is IconWeight` (union from `types/tab.ts`, type-only import).
- Rename `sanitizeHostConfigColor` → `sanitizeHostConfig`; it now also drops `icon` when it is not
  a non-empty string, and `iconWeight` when `!isIconWeight`. Update both call sites
  (`useHostStore` persist `merge`, `hosts` contributor) — no other behaviour change.
- `HostConfig.icon?: string`, `HostConfig.iconWeight?: IconWeight`.
- `setHostIcon(hostId, icon: string | null, weight?: IconWeight)`: `null` → both keys removed;
  non-empty string → stored (weight stored only when `isIconWeight`); unknown host → no-op.
- Tests: spec §6 bullets 1–3 (helpers, store action, sync round-trip + hostile payloads in both merge modes).

Commit: `feat(spa): per-host icon on HostConfig`

## T2 — UI settings: replace host-color-mark prefs with host-badge prefs

Files: `spa/src/stores/useUISettingsStore.ts` (+test), `spa/src/lib/sync/contributors/preferences.ts` (+test).

- Remove every `hostColor*` field/setter/type/helper listed in spec §3.2 (incl. `sanitizeHostColorPrefs`).
- Add the 7 fields × 2 surfaces from spec §3.2 with setters; numeric setters clamp
  (box 12–24, inset 0–5, radius 0–8, lineOpacity 20–100, bgOpacity 0–100, all rounded);
  `LineColor` setter ignores values outside `'host' | 'neutral'`.
- `export function sanitizeHostBadgePrefs(data)` — drops invalid enums and non-finite numbers,
  clamps valid numbers. Used by the preferences contributor (both merge modes, replacing the old
  sanitizer) and by `onRehydrateStorage` (invalid → default, only `setState` when something changed).
- `DATA_FIELDS` in the contributor: drop the 4 old keys, add the 14 new ones. No persist version bump.
- Tests: spec §6 bullets 4–5, plus "removed keys in a persisted payload are ignored".

Commit: `feat(spa): host badge display settings`

## T3 — `HostBadge` component

Files: `spa/src/components/HostBadge.tsx` (+test). Delete `spa/src/components/HostColorMark.tsx`
and `HostColorMark.test.tsx` in T5 (they still compile until then).

- Props exactly as spec §4.1; renders `WorkspaceIcon` from
  `../features/workspace/components/WorkspaceIcon` (cross-feature import is allowed — hosts
  already import from `features/workspace/generated`).
- `<span>` with `display:inline-flex; align-items:center; justify-content:center; flex-shrink:0`,
  `width/height = box`, `border-radius = radius`, `aria-hidden`, `data-testid` (default
  `host-badge`), `data-has-color`.
- Colors per spec §4.1 via `color-mix`. Icon size = `Math.max(8, box - inset*2)`.
- Tests: spec §6 `HostBadge` bullet.

Commit: `feat(spa): HostBadge component`

## T4 — `useTabHostBadge` hook

Files: `spa/src/hooks/useTabHostBadge.ts` (+test). `useTabHostColor.ts` is deleted in T5.

```ts
export interface TabHostBadge { color: string | null; icon: string | undefined; iconWeight: IconWeight | undefined }
export function useTabHostBadge(tab: Tab): TabHostBadge | null
```
- `getTabHostId(tab)`; null → return null.
- Three separate primitive selectors (`color`, `icon`, `iconWeight`), then assemble; `color`
  validated with `isValidHostColor` (else null), `iconWeight` with `isIconWeight` (else undefined).
- Tests: spec §6 `useTabHostBadge` bullet + "changing an unrelated host does not change the result".

Commit: `feat(spa): useTabHostBadge hook`

## T5 — wire into tabs, delete the old mark

Files: `spa/src/features/workspace/components/InlineTab.tsx` (+test),
`spa/src/components/SortableTab.tsx` (+test); delete `HostColorMark.tsx`, `HostColorMark.test.tsx`,
`hooks/useTabHostColor.ts`, `useTabHostColor.test.ts`.

- InlineTab: badge right after the terminal-icon slot, before `renderInlineTabIcon`'s output;
  reads the Sidebar prefs; renders nothing when `Enabled` is false or the hook returns null.
- SortableTab: same for the **normal** branch only; the **pinned** branch renders no badge and
  keeps `w-9`.
- Remove every `HostColorMark` / `useTabHostColor` import and the old host-color test cases.
- Tests: spec §6 `InlineTab` / `SortableTab` bullet (incl. the pinned-tab assertion and the
  existing suites staying green).

Commit: `feat(spa): show host badge on tabs`

## T6 — settings UI (per host + per surface) and i18n

Files: `spa/src/components/hosts/HostIconField.tsx` (+test),
`spa/src/components/hosts/OverviewSection.tsx`,
`spa/src/components/settings/HostBadgeSetting.tsx` (+test),
`spa/src/components/settings/TerminalSection.tsx` (+test),
`spa/src/locales/en.json`, `spa/src/locales/zh-TW.json`;
delete `spa/src/components/settings/HostColorMarkSetting.tsx` (+test).

- `HostIconField` per spec §5.1 (preview button, inline `WorkspaceIconPicker`, weight passthrough,
  `''` → `setHostIcon(hostId, null)`, "use default" button). Add `title?: string` to
  `WorkspaceIconPicker` only if its own header shows in `inline` mode — check first; if added,
  default to today's key so workspace behaviour is unchanged.
- `HostBadgeSetting` per spec §5.2 (toggle + `SegmentControl` + 5 numeric inputs, disabled when off),
  rendered twice in `TerminalSection` where the old rows were.
- Locale keys: drop `settings.terminal.host_color_mark.*`; add `settings.terminal.host_badge.*`
  (`sidebar.label/desc`, `tab_bar.label/desc`, `enabled`, `line_color.host`, `line_color.neutral`,
  `line_opacity`, `bg_opacity`, `box`, `inset`, `radius`, `px`) and `hosts.icon.label`,
  `hosts.icon.change`, `hosts.icon.default` — both locales, `locale-completeness` green.
- Tests: spec §6 `HostIconField`, `HostBadgeSetting`, `TerminalSection` bullets.

Commit: `feat(spa): host badge settings UI`

## T7 — gates

`pnpm exec vitest run` · `pnpm run lint` · `pnpm run build` — all green. Also grep the tree for
`HostColorMark|useTabHostColor|hostColorSidebar|hostColorTabBar|host_color_mark` and assert no hits.
Fixes in their own commits.
