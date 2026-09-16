# Host Badge — Spec (replaces the alpha.352 host color mark)

Date: 2026-09-16 · Scope: SPA only · Single phase

## 1. Goal

Replace the alpha.352 host color mark (24px left gradient / left line / bottom line) with a
**host badge**: a small tinted square holding the host's own icon, sitting between the terminal
icon and the title group. The gradient version was rejected as unreadable — it smears over the
terminal icon and desaturates on the dark theme (user screenshot, 2026-09-16).

Design settled interactively in `docs/pages/host-color-lab.html` (soft / 淡底色 preset).

## 2. Decisions (user-confirmed, do not reopen)

| # | Decision |
|---|----------|
| D1 | Appearance = **tinted box + host icon** ("淡底色"): background = host color at low opacity, icon strokes = host color at full opacity. |
| D2 | Position = **between the terminal icon and the title group**. The agent (`✳`) icon belongs to the title group and is never separated from it. |
| D3 | The box size **matches the row's text box** (default 16px); the icon is **inset 2px** inside it (icon = box − 2×inset); corner radius default 4px. |
| D4 | Settings expose **two colors + opacity**: icon line color (host color or neutral) with its own opacity, and background opacity. Sidebar and top tabs are configured **separately**. |
| D5 | Each host picks **its own icon**, exactly like a workspace: the existing Phosphor picker, including weight (duotone available). |
| D6 | The old mark styles (`gradient` / `left-line` / `bottom-line`) and their settings are **removed**, not kept as options. |

## 3. Data model

### 3.1 `HostConfig` (`spa/src/stores/useHostStore.ts`)

```ts
color?: string        // existing `#rrggbb`
icon?: string         // NEW: Phosphor icon name, e.g. 'Laptop'; absent → DEFAULT_HOST_ICON
iconWeight?: IconWeight  // NEW: same union as Workspace; absent → 'regular'
```
- New action `setHostIcon(hostId, icon: string | null, weight?: IconWeight)`: `null` removes both
  keys; a non-empty name stores it (and the weight when given); unknown host → no-op.
- `DEFAULT_HOST_ICON = 'Desktop'` (`spa/src/lib/host-color.ts`).
- Sync: `hosts` contributor already carries every non-token field, so `icon` / `iconWeight` ride
  along. `sanitizeHostConfigColor` gains a sibling check: a non-string `icon` or an
  `iconWeight` outside the union is dropped at the sync/rehydrate boundary
  (same trust-boundary rule as `color`).

### 3.2 UI settings (`spa/src/stores/useUISettingsStore.ts`)

Remove: `hostColorSidebarStyle`, `hostColorSidebarWidth`, `hostColorTabBarStyle`,
`hostColorTabBarWidth`, `HostColorMarkStyle`, `isHostColorMarkStyle`,
`clampHostColorLineWidth`, `HOST_COLOR_LINE_WIDTH_*`.

Add, per surface (`Sidebar` / `TabBar`):

```ts
hostBadge{Sidebar,TabBar}Enabled: boolean   // default true
hostBadge{Sidebar,TabBar}LineColor: 'host' | 'neutral'   // default 'host'
hostBadge{Sidebar,TabBar}LineOpacity: number  // 20–100, default 100
hostBadge{Sidebar,TabBar}BgOpacity: number    // 0–100, default 22
hostBadge{Sidebar,TabBar}Box: number          // 12–24 px, default 16
hostBadge{Sidebar,TabBar}Inset: number        // 0–5 px, default 2
hostBadge{Sidebar,TabBar}Radius: number       // 0–8 px, default 4
```
with a setter each. Numeric setters clamp; `LineColor` setter ignores values outside the union.
Shared `sanitizeHostBadgePrefs(data)` (exported) drops invalid enums / non-finite numbers and
clamps the rest; used by the `preferences` sync contributor (both merge modes) **and**
`onRehydrateStorage`, exactly as the current `sanitizeHostColorPrefs` does. No persist version
bump (alpha convention); removed keys are inert.

## 4. Rendering

### 4.1 `HostBadge` (`spa/src/components/HostBadge.tsx`, replaces `HostColorMark.tsx`)

```ts
interface HostBadgeProps {
  color: string | null          // validated #rrggbb, or null
  icon: string | undefined      // Phosphor name
  iconWeight: IconWeight | undefined
  box: number; inset: number; radius: number
  lineColor: 'host' | 'neutral'; lineOpacity: number; bgOpacity: number
  testId?: string
}
```
- Renders an inline-flex `<span>` of `box`×`box`, `flex-shrink: 0`, `aria-hidden`, containing
  `<WorkspaceIcon icon={icon ?? DEFAULT_HOST_ICON} name="" size={box - inset*2} weight={iconWeight ?? 'regular'} />`.
- `color === null` → no background; icon color `var(--text-muted)` (a host with an icon but no
  color still shows its icon, in neutral grey).
- `color` set → `background: color-mix(in srgb, <color> <bgOpacity>%, transparent)`,
  `border-radius: radius`, icon color = `neutral` → `var(--text-muted)`, `host` →
  `color-mix(in srgb, <color> <lineOpacity>%, transparent)`.
- `data-testid` default `host-badge`, plus `data-has-color`.
- NOT absolutely positioned (unlike the old mark): it is a real flex child, so it shifts the
  title instead of overlaying it.

### 4.2 Placement

| Surface | File | Placement |
|---|---|---|
| Sidebar row | `features/workspace/components/InlineTab.tsx` | directly after the terminal-icon slot, **before** `renderInlineTabIcon`'s agent icon + title |
| Top tab (normal and pinned) | `components/SortableTab.tsx` | same position: after `TabIcon`, before the title |

Gap handling: the badge participates in the row's existing `gap-1.5`, so no extra margins.

### 4.3 When is it shown

- Only for tabs whose first tmux-session pane resolves a host (`getTabHostId`) **and** the
  surface's `Enabled` is true.
- A tab with no tmux pane (editor / browser / settings) renders **nothing and reserves no
  space** — titles on those rows start further left. (Chosen default; a "reserve space" toggle is
  explicitly out of scope for this change.)
- `useTabHostBadge(tab)` (replaces `useTabHostColor`) returns
  `{ color, icon, iconWeight } | null` with primitive selectors, so a row re-renders only when
  its own host's badge inputs change.

## 5. Settings UI

### 5.1 Per host (`components/hosts/HostColorField.tsx` + new `HostIconField.tsx`)

- The existing color field is unchanged.
- New "Icon" field below it: current icon preview button → opens the existing
  `WorkspaceIconPicker` (reused as-is; it already offers search, categories and the six weights
  including duotone), plus a "use default" button clearing back to `DEFAULT_HOST_ICON`.
- Writes through `setHostIcon`.

### 5.2 Per surface (`components/settings/HostBadgeSetting.tsx`, replaces `HostColorMarkSetting.tsx`)

Two `SettingItem` groups in `TerminalSection`, where the old ones were (Settings > Terminal,
after the tab-indicator row): **Sidebar host badge** and **Top tab host badge**. Each group:

- a toggle (`Enabled`),
- `SegmentControl` for line color (`Host color` / `Neutral`),
- number inputs (existing numeric-input styling) for: line opacity %, background opacity %,
  box px, inset px, radius px — each with `aria-label` prefixed by the group label and a
  `data-testid` of `host-badge-{sidebar,tabbar}-{line-opacity,bg-opacity,box,inset,radius}`.
- Controls other than the toggle are disabled while `Enabled` is false.

i18n: replace the `settings.terminal.host_color_mark.*` keys with
`settings.terminal.host_badge.*` (both locales); add `hosts.icon.*` for the per-host field.

## 6. Testing

- `host-color.ts`: `DEFAULT_HOST_ICON`; `sanitizeHostConfig` drops invalid `icon` / `iconWeight`
  while keeping valid ones and the existing color behaviour.
- `useHostStore`: `setHostIcon` sets name and weight; `null` removes both keys; unknown host no-op;
  invalid weight ignored.
- `hosts` sync contributor: `icon` / `iconWeight` round-trip; hostile payloads (`icon: 42`,
  `iconWeight: 'evil'`) dropped in both merge modes.
- `useUISettingsStore`: defaults; clamps (box 8→12, 40→24; inset −1→0, 9→5; opacities 0/120);
  invalid line color ignored; `sanitizeHostBadgePrefs` unit cases; rehydrate of a corrupt payload
  resets to defaults.
- `preferences` contributor: the new fields serialize; hostile payloads sanitized in both modes.
- `HostBadge`: renders nothing extra when `color === null` except the neutral icon; background
  uses `bgOpacity`; icon size = `box − 2×inset`; radius applied; neutral line color path;
  `data-has-color`.
- `useTabHostBadge`: returns host icon/color; null for a tab with no tmux pane; reacts to the
  host's icon change; ignores an invalid stored color.
- `InlineTab` / `SortableTab` (incl. pinned): badge present with a host, absent when the surface is
  disabled, absent for a non-tmux tab; sits between the terminal icon and the title.
- `HostIconField`: opens the picker, selecting an icon calls `setHostIcon`, default button clears.
- `HostBadgeSetting` + `TerminalSection`: each control writes its own store field; controls
  disabled when the toggle is off; both surfaces independent.
- Gates: `pnpm exec vitest run`, `pnpm run lint`, `pnpm run build`.

## 7. Out of scope

- Reserving badge space for tabs without a host.
- Host badges anywhere other than sidebar rows and top tabs (workspace rows, status bar).
- A second independent background color (background stays the host color at low opacity).
