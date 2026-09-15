# Host Color Mark — Spec

Date: 2026-09-15 · Scope: SPA only · Single phase

## 1. Goal

Each host can have an optional color. Every tab whose content belongs to that host shows a
color mark, both in the sidebar (`InlineTab`) and in the top tab bar (`SortableTab`), so a
multi-host setup is readable at a glance. No color set → nothing is drawn (zero visual change).

## 2. Decisions (user-confirmed, do not reopen)

| # | Decision |
|---|----------|
| D1 | Color is **per host**, stored on `HostConfig.color`, synced via the existing `hosts` sync contributor. |
| D2 | Color marks are **per tab** (not per workspace row). A tab's host = `hostId` of its **first tmux-session pane** (pre-order). Tabs without a tmux-session pane (editor, browser, settings…) get no mark. |
| D3 | Picker: 8 preset swatches + custom hex input + clear. |
| D4 | **Display style is global and configured separately for sidebar and top tabs.** Both surfaces offer the same styles: `gradient` / `left-line` / `bottom-line` / `none`. |
| D5 | Line styles (`left-line`, `bottom-line`) have an adjustable width, per surface. `gradient` has a fixed fade length (~24px). |
| D6 | Defaults: sidebar = `gradient`, top tabs = `bottom-line`, width = 2px on both. |

## 3. Data model

### 3.1 `HostConfig` (`spa/src/stores/useHostStore.ts`)

```ts
export interface HostConfig {
  // …existing
  /** Optional `#rrggbb`. Absent = no color mark. */
  color?: string
}
```

- `updateHost` accepts `color` (`Pick<…, 'name'|'ip'|'port'|'token'|'color'>`).
  Passing `color: undefined` removes the key (not stored as `undefined`-valued key that
  survives as `null` through JSON).
- No persist migration (alpha, field is optional).
- Sync: `hosts` contributor already spreads every non-token field, so `color` rides along
  with no contributor change. A test pins this.

### 3.2 UI settings (`spa/src/stores/useUISettingsStore.ts`)

```ts
export type HostColorMarkStyle = 'gradient' | 'left-line' | 'bottom-line' | 'none'
export const HOST_COLOR_LINE_WIDTH_MIN = 1
export const HOST_COLOR_LINE_WIDTH_MAX = 6

hostColorSidebarStyle: HostColorMarkStyle      // default 'gradient'
hostColorSidebarWidth: number                  // default 2
hostColorTabBarStyle: HostColorMarkStyle       // default 'bottom-line'
hostColorTabBarWidth: number                   // default 2
setHostColorSidebarStyle / setHostColorSidebarWidth / setHostColorTabBarStyle / setHostColorTabBarWidth
```

- Width setters clamp to `[MIN, MAX]` and round to integer.
- All four fields are added to the `preferences` sync contributor `DATA_FIELDS`, so they sync
  like `tabIndicatorStyle`. No persist version bump (new fields fall back to defaults).

## 4. Color resolution (`spa/src/lib/host-color.ts`, new, pure)

```ts
export const HOST_COLOR_PRESETS: readonly string[]   // 8 × '#rrggbb', legible on dark + light themes
export function isValidHostColor(v: unknown): v is string        // /^#[0-9a-f]{6}$/i
export function normalizeHostColor(v: string): string | null     // trims, accepts 'rrggbb' or '#rrggbb', lowercases; invalid → null
export function getTabHostId(tab: Tab): string | null            // first tmux-session pane hostId (pre-order) or null
export function resolveTabHostColor(tab: Tab, hosts: Record<string, HostConfig>): string | null
```

- `resolveTabHostColor` returns `null` when: no tmux-session pane; host missing from store;
  host has no color; stored color **fails `isValidHostColor`** (defends against a malformed
  value arriving through sync being injected into inline CSS).
- `getTabHostId` reuses `collectTmuxSessionHostIds` from `infer-workspace-host-id.ts` (`[0] ?? null`).

Hook: `useTabHostColor(tab)` in `spa/src/hooks/useTabHostColor.ts` — selects
`s.hosts[getTabHostId(tab)]?.color` from `useHostStore` (primitive selector, no re-render on
unrelated host changes) and validates.

## 5. Rendering — `HostColorMark` (`spa/src/components/HostColorMark.tsx`, new)

```ts
interface Props { color: string | null; style: HostColorMarkStyle; width: number; testId?: string }
```

- Renders `null` when `color === null` or `style === 'none'`.
- Always `position:absolute; pointer-events:none; aria-hidden`. Parent already `relative`.
- `left-line`: `left:0; top:0; bottom:0; width:{width}px; background:color`, inherits the
  parent's left radius (`border-top-left-radius/border-bottom-left-radius: inherit`).
- `bottom-line`: `left:0; right:0; bottom:0; height:{width}px; background:color`, bottom radius inherit.
- `gradient`: `left:0; top:0; bottom:0; width:24px; background: linear-gradient(to right, <color @ ~45% alpha>, transparent)`,
  left radius inherit. Alpha applied by appending hex alpha (`${color}73`) — valid because color is validated `#rrggbb`.
- Exposes `data-style` for tests.

Integration:

| Surface | File | Settings used | Notes |
|---|---|---|---|
| Sidebar tab row | `features/workspace/components/InlineTab.tsx` | `hostColorSidebarStyle/Width` | Row is `relative rounded-md`; mark is first child so content paints over it. |
| Top tab (normal + pinned) | `components/SortableTab.tsx` | `hostColorTabBarStyle/Width` | Mark rendered **after** the close-button overlay with `z-index:1` so `bottom-line`/`left-line` are not covered by the opaque close-button background; the unread pip keeps `z-20`. |

No layout box changes: marks are absolute, so tab widths, drag hit areas and truncation are
unchanged.

## 6. Settings UI

### 6.1 Host color picker — `OverviewSection` (per host)

New `Field` "Color" in the Connection section (below Name):
- 8 preset swatch buttons (`aria-label` = hex, `aria-pressed` for current), a "clear" button,
  and a hex text input (commit on Enter/blur; invalid → inline error, not saved).
- Writes through `updateHost(hostId, { color })`.
- Component: `HostColorField` in `components/hosts/HostColorField.tsx` (keeps `OverviewSection` from growing).

### 6.2 Display settings — Appearance > Interface area

Placed in `TerminalSection` next to the existing tab-indicator control (that is where tab
visuals are configured today). New group "Host color mark" with two rows:

- **Sidebar**: `SegmentControl` (Gradient / Left line / Bottom line / Off) + width slider
  `1–6px` shown only when style is a line style.
- **Top tabs**: same.

i18n keys added for `en` and `zh-TW` (all locales the repo ships).

## 7. Testing (TDD)

- `host-color.test.ts`: presets all valid; `isValidHostColor` / `normalizeHostColor` cases
  (`#ABCDEF`, `abcdef`, `  #abc123 `, `#abc`, `red`, `url(x)`, `''`); `getTabHostId` for
  single leaf, split with mixed kinds (first tmux-session wins in pre-order), no tmux pane;
  `resolveTabHostColor` null paths incl. invalid stored color.
- `useHostStore` test: `updateHost` sets/clears `color`; clearing removes the key.
- `hosts` contributor test: `color` survives serialize → deserialize (full-replace and field-merge).
- `useUISettingsStore` test: defaults; width clamp (0→1, 9→6, 2.6→3).
- `preferences` contributor test: four new fields serialized.
- `HostColorMark.test.tsx`: null color / `none` → renders nothing; each style → correct
  `data-style` and width/height px; gradient uses alpha color.
- `InlineTab` / `SortableTab` tests: mark present with color, absent without, style/width follow the per-surface setting; pinned tab too.
- `HostColorField.test.tsx`: preset click saves; valid hex saves normalized; invalid hex not saved + error; clear removes.
- `TerminalSection` test: width control hidden for `gradient`/`none`, visible for line styles; setters called.
- Gates: `npx vitest run`, `pnpm run lint`, `pnpm run build`.

## 8. Out of scope

- Workspace-row coloring, activity bar, status bar.
- Per-host style overrides (style is global by design).
- Coloring non-tmux tabs by any other host inference.
