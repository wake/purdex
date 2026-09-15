# Host Color Mark — Implementation Plan

Spec: `docs/specs/2026-09-15-host-color-mark-spec.md` · Branch `worktree-host-color` · SPA only

Rules for every task:
- TDD: write the failing test first, run it red, implement, run green.
- Every Bash command prefixed with `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/host-color/spa && `.
- Run only the touched test files during the task; T8 runs the full gates.
- One commit per task: `git commit --only <files> -m "<msg>"`, with trailer
  `Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>`.
- Tests use merge-mode `setState` on stores and list every mutable field they touch.

Dependency order: T1 → T2 → T3 → (T4, T5, T6 in any order) → T7 → T8.

---

## T1 — `host-color.ts` pure helpers

Files: `src/lib/host-color.ts`, `src/lib/host-color.test.ts`

Exports (exact):
```ts
export const HOST_COLOR_PRESETS: readonly string[] // 8 lowercase '#rrggbb'
export function isValidHostColor(v: unknown): v is string        // /^#[0-9a-f]{6}$/i
export function normalizeHostColor(v: string): string | null     // trim; optional '#'; 6 hex; lowercase; else null
export function getTabHostId(tab: Tab): string | null            // collectTmuxSessionHostIds(tab.layout)[0] ?? null
export function resolveTabHostColor(tab: Tab, hosts: Record<string, HostConfig>): string | null
```
Presets: `#ef4444 #f97316 #eab308 #22c55e #14b8a6 #3b82f6 #8b5cf6 #ec4899`.

Tests: spec §7 first bullet, all cases verbatim.

Commit: `feat(spa): host color helpers`

## T2 — Host store `color` + sync round-trip

Files: `src/stores/useHostStore.ts`, `src/stores/useHostStore.test.ts`,
`src/lib/sync/contributors/hosts.test.ts`

- `HostConfig.color?: string`.
- New action `setHostColor(hostId: string, color: string | null): void`:
  `null` → rebuild host without the `color` key (`const { color: _c, ...rest } = host`);
  string → stored only if `isValidHostColor` (else no-op). Unknown host → no-op.
- `updateHost` signature unchanged (color goes through `setHostColor` only).
- Tests: set, clear removes key (`'color' in host === false`), invalid ignored, unknown host;
  hosts contributor: `color` survives serialize→deserialize for full-replace and field-merge.

Commit: `feat(spa): per-host color on HostConfig`

## T3 — UI settings fields + preferences sync

Files: `src/stores/useUISettingsStore.ts`, its test (create `useUISettingsStore.test.ts` if absent),
`src/lib/sync/contributors/preferences.ts`, `preferences.test.ts`

- Add `HostColorMarkStyle`, `HOST_COLOR_LINE_WIDTH_MIN=1`, `HOST_COLOR_LINE_WIDTH_MAX=6`,
  `clampHostColorLineWidth(n)` (non-finite → 2; else round then clamp), `isHostColorMarkStyle(v)`.
- Four fields + setters, defaults per spec D6. Width setters use the clamp; style setters ignore invalid.
- Append the four fields to preferences `DATA_FIELDS`; in `deserialize` (both full-replace and
  field-merge) run `sanitizeHostColorPrefs(incoming)`: drop invalid style, drop non-number /
  non-finite width, clamp finite width.
- Tests: spec §7 `useUISettingsStore` and `preferences` bullets verbatim (incl. hostile payloads).

Commit: `feat(spa): host color mark display settings`

## T4 — `HostColorMark` component + `useTabHostColor` hook

Files: `src/components/HostColorMark.tsx` + test, `src/hooks/useTabHostColor.ts` + test

- Component per spec §5 (`data-testid` default `host-color-mark`, `data-style`, `aria-hidden`,
  `pointer-events:none`, radius inherit, gradient `${color}73` over 24px).
- Hook: `const hostId = getTabHostId(tab); const color = useHostStore(s => hostId ? s.hosts[hostId]?.color : undefined); return isValidHostColor(color) ? color : null`.
- Tests: spec §7 `HostColorMark` bullet; hook returns color / null for no-tmux tab / invalid stored value / missing host.

Commit: `feat(spa): HostColorMark component`

## T5 — Wire into `InlineTab` and `SortableTab`

Files: `src/features/workspace/components/InlineTab.tsx` + test, `src/components/SortableTab.tsx` + test

- InlineTab: mark as first child, sidebar style/width settings.
- SortableTab (normal and pinned): mark as last child with `zIndex:1` (after close overlay), tab-bar settings.
- Tests: with host color → mark with expected `data-style`; without color → no mark; setting
  `none` → no mark; width reflected; pinned tab rendered.

Commit: `feat(spa): show host color mark on tabs`

## T6 — `HostColorField` in host Overview

Files: `src/components/hosts/HostColorField.tsx` + test, `src/components/hosts/OverviewSection.tsx`

- 8 swatches (`aria-label` hex, `aria-pressed`), clear button, hex input (commit Enter/blur via
  `normalizeHostColor`; invalid → `role="alert"` error text, no save). Writes via `setHostColor`.
- Rendered in Connection section directly below the Name field.
- Tests: spec §7 `HostColorField` bullet.

Commit: `feat(spa): host color picker in host overview`

## T7 — Display settings in `TerminalSection` + i18n

Files: `src/components/settings/TerminalSection.tsx` + test, `src/locales/en.json`, `src/locales/zh-TW.json`

- Two `SettingItem`s after the tab-indicator item: sidebar and top tabs. Each: `SegmentControl`
  (gradient / left-line / bottom-line / none) and, only for line styles, a `type="number"` input
  `min=1 max=6 step=1` (matches existing numeric inputs in this file) with `aria-label`.
- i18n keys under `settings.terminal.host_color_mark.*` and `hosts.color.*`, both locales
  (locale-completeness test must pass).
- Tests: spec §7 `TerminalSection` bullet.

Commit: `feat(spa): host color mark settings UI`

## T8 — Full gates

`npx vitest run` · `pnpm run lint` · `pnpm run build` — all green; fix anything they surface in a
separate commit `fix(spa): …`.
