# Host Color Modes — Spec (host badge v3: per-mode tri-color + agent title marker strip)

Date: 2026-09-18 · Scope: SPA only · Three phases (one PR each) · Base: `3273d803` (alpha.379)

## 1. Goal

After living with the alpha.362 host badge (`docs/specs/2026-09-16-host-badge-spec.md`) the user
found it "not smooth": one host color painted the same way on every row, so the active tab, the
inactive tabs, and shell-only tabs all look alike. This spec replaces the single `color` with a
**per-mode tri-color** (main / middle / light) and adds a setting that strips the `✳` marker
Claude Code writes into the pane title.

Explicitly **not** in scope: project icon by cwd (`project_project_icon_by_cwd` memory), the
v2 lab's "display mode B" (color on the tab icon itself) and equal-width spacer questions.

## 2. Decisions (user-confirmed 2026-09-18, do not reopen)

| # | Decision |
|---|----------|
| D1 | **Modes** = `console` (tab whose primary pane has no live `agentType`), `terminal` (primary pane has an `agentType` **and is not terminated** — the same rule that picks the agent icon in `useTabDisplay`; a terminated agent pane is `console`), `execution` (reserved for the P-C execution tab; enum + settings UI only, nothing resolves to it yet). "console/terminal" is *not* a real pane kind — it is the agentType split. |
| D2 | **Three layers per mode**: `main` = active/hover icon color; `middle` = inactive icon color; `light` = badge background. |
| D3 | `middle` and `light` **inherit `main`'s hue when their own color is unset** and then only carry an alpha. The user can break inheritance and pick a different color. |
| D4 | `terminal` / `execution` **inherit the whole `console` set** when unset. `console.main` unset ⇒ the host has no color (existing "沒設定就沒有" behaviour: no color and no icon ⇒ no badge, no space). |
| D5 | Hover = active: both use `main`. No separate hover color. |
| D6 | The per-surface **icon opacity / background opacity** settings (`hostBadge*LineOpacity`, `hostBadge*BgOpacity`) are **removed** — opacity now lives in each host's color layers. Per-surface `Enabled` / `LineColor` (host | neutral) / `Box` / `Inset` / `Radius` stay. |
| D7 | The remaining numeric fields in Settings get a **visible caption above each input** (the user could not tell what "100 % 22 % 16 px 2 px 4 px" meant). |
| D8 | Host color picking = a **popover** per layer with hue / saturation / lightness / alpha sliders, hex input and live preview; edits write to the store immediately so tab rows update while dragging. No new dependency; `@floating-ui/react` (already a dep) positions the popover. |
| D9 | `✳` strip is a **UI setting** (`stripAgentTitleMarker`, default on). Patterns are keyed by agentType in one table; Codex's marker is added to the same table later when the user supplies a sample (measured 2026-09-18: Codex 0.153.4 sets `pane_title` to the cwd basename only, no marker — so the "animation" the user sees on Codex tabs is probably Purdex's own icon animation, to be confirmed). |
| D10 | No persist migration (alpha). The legacy `HostConfig.color` is honoured **at read time only** as `colors.console.main.color` with default alphas, so already-configured hosts keep their color. Writing through the new UI writes `colors` and deletes `color`. |

## 3. Measured facts (2026-09-18, base `3273d803`)

- Claude Code (2.1.276) writes `pane_title` = `✳ <summary>` — U+2733 followed by U+0020, **no** U+FE0F. `spa/src/hooks/useTabDisplay.ts:73` concatenates it unmodified: `${paneTitle} - ${baseLabel}`, gated by `dynamicTabName && !isTerminated && !!agentType`.
- `agentType` for a tab = `useAgentStore.agentTypes[compositeKey(hostId, sessionCode)]`, an open string (`'cc' | 'codex' | 'opencode'` today, `AGENT_NAMES` in `spa/src/lib/agent-metadata.ts`). `useTabDisplay` already reads it.
- Badge is rendered by `spa/src/components/HostBadge.tsx` (props: `color, icon, iconWeight, box, inset, radius, lineColor, lineOpacity, bgOpacity`), consumed by `components/SortableTab.tsx` (top bar, `role="tab"` + `aria-selected`) and `features/workspace/components/InlineTab.tsx` (sidebar, `role="button"`, no aria-selected). Both rows carry the Tailwind `group` class and receive `isActive`.
- `useTabHostBadge(tab)` returns `{ color, icon, iconWeight } | null` from three primitive selectors on `useHostStore.hosts[hostId]`.
- `lib/host-color.ts`: `isValidHostColor` (`#rrggbb`), `normalizeHostColor`, `sanitizeHostConfig` (drops invalid `color`/`icon`/`iconWeight`; used by `useHostStore` persist `merge` and `lib/sync/contributors/hosts.ts`), `hasHostBadge`, `getTabHostId`, and `resolveTabHostColor` (**dead** — only its own test references it).
- `useUISettingsStore`: 14 `hostBadge{Sidebar,TabBar}*` fields, mirrored in `lib/sync/contributors/preferences.ts` (`PREFERENCE_KEYS`-style list at lines 23–36) and edited by `components/settings/HostBadgeSetting.tsx` (rendered twice from `TerminalSection.tsx:160,179`). `dynamicTabName` toggle is at `TerminalSection.tsx:200`.
- Host color UI = `components/hosts/HostColorField.tsx` (8 presets + clear + hex input) inside `OverviewSection.tsx:130`, right above `HostIconField`.
- Locales: `spa/src/locales/{en,zh-TW}.json`; `locale-completeness.test.ts` fails on a key present in one and missing in the other.

## 4. Data model

### 4.1 `HostConfig` (`spa/src/stores/useHostStore.ts`)

```ts
export type HostColorMode = 'console' | 'terminal' | 'execution'
export const HOST_COLOR_MODES: readonly HostColorMode[] = ['console', 'terminal', 'execution']

export interface HostColorLayer {
  /** `#rrggbb`. Absent on middle/light = inherit the mode's main color. Required on main. */
  color?: string
  /** 0–100 integer. */
  alpha: number
}
export interface HostColorSet {
  main: HostColorLayer & { color: string }
  middle?: HostColorLayer
  light?: HostColorLayer
}

// HostConfig
colors?: Partial<Record<HostColorMode, HostColorSet>>
/** @deprecated read-only legacy; see D10. Never written by new code. */
color?: string
```

Defaults when a layer is absent: `main.alpha = 100`, `middle = { alpha: 60 }`, `light = { alpha: 22 }`
(22 is the alpha.362 background default).

Store actions (replace `setHostColor`):

```ts
setHostColorLayer(hostId, mode: HostColorMode, layer: 'main' | 'middle' | 'light', value: HostColorLayer | null): void
clearHostColorMode(hostId, mode: HostColorMode): void   // removes the whole set for that mode
```

- `setHostColorLayer(..., 'main', value)` with a valid `color` creates the mode's set when absent
  (`middle`/`light` stay absent = inherit). With `null` on `main` it behaves as `clearHostColorMode`.
- `setHostColorLayer(..., 'middle' | 'light', value)`: `value.color` may be omitted (inherit) but
  when present must pass `isValidHostColor` (the store lowercases but never normalizes — the UI
  normalizes user input first); `alpha` must be a finite number and is clamped to an integer
  0–100; any other shape is a no-op and never throws. `null` removes the layer (back to inherit +
  default alpha). Setting middle/light on a mode whose set does not exist is a no-op (the UI
  creates the set first by writing `main`). Removing a middle/light layer that is already absent
  is a no-op (legacy color untouched).
- `clearHostColorMode` on a mode that has no set is a no-op, except `console` on a host that has a
  legacy `color` and no console set (whether or not other mode sets exist): there it removes the
  legacy `color`, because the resolver shows the legacy color as the console color and "No color"
  must clear what the user sees.
- Any **applied** write to `colors` on a host that still has legacy `color` deletes `color` in
  the same update (D10); a rejected (no-op) write leaves it alone.
- Unknown host / invalid input → no-op, like today.

### 4.2 Validation (`lib/host-color.ts`)

- `isHostColorMode(v)`, `clampHostAlpha(n)` (NaN → default is the caller's job; clamp rounds and
  bounds 0–100), `isHostColorLayer(v, { requireColor })`, `isHostColorSet(v)`.
- `sanitizeHostConfig` additionally: drops `colors` when not a plain object; inside it drops any
  mode key not in `HOST_COLOR_MODES`, any set whose `main` is invalid, and any invalid
  `middle`/`light` layer (keeping the rest of the set). Legacy `color` keeps its existing check.
- `resolveTabHostColor` is deleted with its tests.

### 4.3 Resolver (`lib/host-color.ts`, pure)

```ts
export interface ResolvedHostColors { main: string; middle: string; light: string }  // rgba() strings
export function resolveHostColors(host: HostConfig | undefined, mode: HostColorMode): ResolvedHostColors | null
```

Order: `host.colors?.[mode]` → `host.colors?.console` → legacy `{ main: { color: host.color, alpha: 100 } }`
→ `null`. Within the chosen set, `middle.color ?? main.color`, `light.color ?? main.color`, alphas as
in §4.1 defaults. Output is `rgba(r, g, b, a)` with `a` in 0–1, so callers never touch
`color-mix` again. Invalid data (should not survive sanitize) → `null`, never a throw.

### 4.4 UI settings (`useUISettingsStore`)

Remove `hostBadge{Sidebar,TabBar}{LineOpacity,BgOpacity}` (+ their clamps/constants/setters and
the 4 entries in `preferences.ts`). A stale key must not survive into runtime state: persist
`version` bumps 3 → 4 with a `migrate` that drops the four keys, and `sanitizeHostBadgePrefs`
strips them (covers the sync path). Add:

```ts
stripAgentTitleMarker: boolean   // default true; synced via preferences.ts like dynamicTabName
```

### 4.5 Agent title marker (`lib/agent-title-marker.ts`, pure)

```ts
export const AGENT_TITLE_MARKERS: Readonly<Record<string, RegExp>> = {
  cc: /^✳️?\s*/,   // Claude Code: "✳ " (VS16 tolerated)
}
export function stripAgentTitleMarker(title: string, agentType: string | undefined): string
```

Unknown agentType or no match → title unchanged. Only a **leading** marker is removed; a `✳`
elsewhere in the summary stays. `useTabDisplay` applies it when `stripAgentTitleMarker` is on,
before the `${paneTitle} - ${baseLabel}` join. If the stripped title is empty the tab falls back
to `baseLabel` alone (same as no pane title).

## 5. Rendering

### 5.1 `HostBadge` props (replace `color / lineColor / lineOpacity / bgOpacity`)

```ts
colors: ResolvedHostColors | null   // null = host has icon but no color → neutral box, no background
lineColor: 'host' | 'neutral'
```

The badge sets CSS custom properties on its own span and uses them:

```
--hb-main: <colors.main>; --hb-middle: <colors.middle>;
color: var(--hb-icon, var(--hb-middle));  background: <colors.light>
```

`lineColor === 'neutral'` or `colors === null` ⇒ `color: var(--text-muted)` and no custom
properties (unchanged behaviour). Background is painted only when `colors !== null`.

### 5.2 Active / hover (one CSS rule, `spa/src/index.css` or the existing global stylesheet)

```css
.group:hover [data-host-badge], .group[data-active="true"] [data-host-badge] { --hb-icon: var(--hb-main); }
```

Both rows add `data-active={String(isActive)}` (SortableTab already has `aria-selected`; the data
attribute is used on both so the rule is identical). `data-host-badge` is a marker attribute on
the badge span (keeps `data-testid="host-badge"` as is).

### 5.3 Mode per tab

`useTabHostBadge(tab)` returns `{ colors: ResolvedHostColors | null, icon, iconWeight } | null`. It
needs the tab's `agentType`; it reads it the same way `useTabDisplay` does (`compositeKey` of the
primary tmux pane → `useAgentStore.agentTypes`). Mode = `agentType ? 'terminal' : 'console'`.
Selector discipline stays primitive: select `hosts[hostId]?.colors` (object identity is stable
until written) plus `color`, `icon`, `iconWeight`, `agentType`; resolve with `useMemo` on those.
`hasHostBadge` now checks `colors !== null || icon !== undefined`.

### 5.4 Settings → Terminal section

`HostBadgeSetting` drops the two opacity inputs and renders each remaining number as
`<label>caption<input/></label>` stacked (caption above, 11px muted), captions from new locale
keys `settings.terminal.host_badge.{box,inset,radius}.caption` ("Size" / "Inset" / "Radius");
the existing long `aria-label` strings stay for screen readers. New toggle
`settings.terminal.strip_agent_title_marker.{label,desc}` placed directly after "Dynamic tab
name".

## 6. Host color UI (`components/hosts/HostColorField.tsx` → rewritten)

Layout inside the existing `Field label="Color"`:

1. Row 1 — `SegmentControl` with `Console / Terminal / Execution`. Local state, defaults to `console`.
2. Row 2 — three **layer swatches** (`Main`, `Middle`, `Light`), each a 26×18 button showing the
   resolved rgba on a checkerboard, with the caption below (`Main · #22c55e 100%`,
   `Middle · inherit 60%`). For a non-console mode with no set, all three render dimmed with the
   caption `Inherits Console`; clicking any of them first copies `console`'s resolved set into
   that mode (`setHostColorLayer(main)` with console's main), then opens the popover.
   A trailing `Prohibit` button = `clearHostColorMode(mode)` (for `console` this is the old
   "No color").
3. Popover (`components/hosts/HostColorPopover.tsx`, `@floating-ui/react`, closes on outside
   click / Esc, `role="dialog"` with the layer name as label):
   - `Main`: preset row (the 8 `HOST_COLOR_PRESETS`) → H / S / L sliders → Alpha slider → hex
     input (existing `normalizeHostColor` rules, invalid ⇒ inline error, no write).
   - `Middle` / `Light`: `Inherit main color` toggle (on when `layer.color` is absent). On ⇒ only
     the Alpha slider. Off ⇒ same H / S / L / hex as Main, initialised from the inherited color.
   - Every slider `onInput` writes to the store immediately (D8). Sliders are native
     `<input type="range">` styled with a gradient track computed from the current HSL so the
     hue / saturation / lightness axes show what they will produce.
   - `lib/color-space.ts` (pure): `hexToHsl`, `hslToHex`, `hexToRgb`; round-trips must be stable
     for all 8 presets (test).

`HostIconField` is unchanged.

## 7. Phases

| Phase | PR content | Tests |
|---|---|---|
| **P1 data** | §4.1–4.3 and §4.5 in full, plus the **addition** in §4.4 (`stripAgentTitleMarker` setting, its `preferences.ts` entry, the `useTabDisplay` wiring and the Settings toggle from §5.4's last sentence); the **removal** of the opacity settings in §4.4 is P2. Delete `resolveTabHostColor`. `HostBadge` and its consumers keep compiling via a **temporary** adapter: `useTabHostBadge` still returns `color` = resolved main hex (no visual change yet). | Store actions incl. legacy `color` deletion, sanitize corner cases (mode key junk, bad `main`, bad `middle` kept-set), resolver inheritance table (every row of §4.3), strip-marker table, `useTabDisplay` with toggle on/off and empty-after-strip. |
| **P2 render + settings** | §5 in full; remove the two opacity settings end to end; captions; the temporary adapter from P1 disappears. | `HostBadge` custom properties + neutral path; `useTabHostBadge` mode selection (`agentType` present/absent) and memo identity; both tab rows carry `data-active`; `HostBadgeSetting` renders three captioned inputs and no opacity inputs; locale completeness. |
| **P3 picker** | §6 in full. | `color-space` round-trips; `HostColorField` mode switch, inherit-from-console dimmed state + copy-on-click, clear per mode; popover: preset click writes main, inherit toggle removes/creates `color`, alpha slider clamps, hex invalid ⇒ no write. |

P1 must not change what the user sees; P2 is the visible change; P3 is the UI to tune it. The
user will tune defaults on the live app after P3 and may adjust the §4.1 default alphas then.

## 8. Acceptance (live, after P3, both surfaces)

1. A host with only the legacy `color` shows exactly the alpha.362 look on an active tab, and a
   dimmer (60%) icon on inactive tabs; hovering an inactive tab brings the icon to full color.
2. Setting `terminal.main` to a different hue than `console.main`: agent tabs and plain shell tabs
   of the same host show different badge colors; a tab whose agent exits (agentType cleared)
   flips to the console color without reload.
3. Dragging the Middle alpha slider changes inactive tabs while dragging.
4. Turning `Strip agent title marker` off brings `✳` back on Claude Code tabs immediately.
5. Settings → Terminal shows `Size / Inset / Radius` captions and no percent fields.
