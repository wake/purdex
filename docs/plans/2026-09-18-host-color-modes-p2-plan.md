# Host Color Modes — P2 (three-state badge rendering + settings cleanup) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the host badge render per-mode tri-colors — `main` on the active/hovered row, `middle` on inactive rows, `light` as background — and remove the now-redundant per-surface opacity settings, giving the remaining numeric settings visible captions.

**Architecture:** `HostBadge` takes the resolved `{ main, middle, light }` rgba strings and exposes them as CSS custom properties on its own span; one global CSS rule swaps the icon color on `.group:hover` / `[data-active="true"]`, so hover needs no JS. `useTabHostBadge` picks the mode (`terminal` when the primary pane has a live `agentType`, else `console`) and resolves through `resolveHostColors` from P1. The two per-surface opacity settings are deleted end to end (store, sync field list, settings UI, locales).

**Tech Stack:** React 19 / Zustand 5 / Vitest + Testing Library / Tailwind 4 / TypeScript strict. Tests: `cd spa && npx vitest run <path>`; lint `pnpm run lint`; typecheck `npx tsc --noEmit -p tsconfig.app.json`.

**Spec:** `docs/specs/2026-09-18-host-color-modes-spec.md` — §5 (rendering), §4.4 (removal half), §7 row P2, §8 acceptance 1–2 and 5. P1 (merged, alpha.381) provides `resolveHostColors(host, mode): { main, middle, light } | null` (rgba strings) and `ResolvedHostColors` in `spa/src/lib/host-color.ts`.

## Global Constraints

- Worktree: `/Users/wake/Workspace/wake/purdex/.claude/worktrees/host-color-modes`, branch `worktree-host-color-modes-p2` — every shell command starts with `cd` into it (or its `spa/`).
- TDD: failing test first, run it, implement, run again. One commit per task via `git commit --only <files>`.
- Hover = active = `main`; inactive = `middle`; background = `light` in every state (spec D2/D5).
- `lineColor === 'neutral'` keeps painting the icon `var(--text-muted)` but still paints the `light` background (existing behaviour, spec §5.1).
- A host with an icon but no color (`colors === null`) renders a neutral box with **no** background (unchanged).
- Mode: `terminal` iff the primary tmux pane has an `agentType` in `useAgentStore.agentTypes` **and** the pane is not terminated (matches `useTabDisplay`'s icon rule); otherwise `console`. Nothing resolves to `execution` yet (spec D1).
- The four settings `hostBadge{Sidebar,TabBar}{LineOpacity,BgOpacity}` and every constant / clamp / setter / locale key / sync entry that exists only for them are deleted (spec D6). No persist migration: a stale persisted key is simply ignored by zustand's shallow merge.
- Captions: `settings.terminal.host_badge.box.caption` = "Size", `.inset.caption` = "Inset", `.radius.caption` = "Radius" (zh-TW: "大小" / "內縮" / "圓角"); existing long `aria-label` strings stay (spec D7).
- Locale keys must exist in both `spa/src/locales/en.json` and `zh-TW.json`.
- Diff budget ≤ 800 lines / 20 files. **Every commit must typecheck**: Task 1 changes `HostBadge`'s props, the hook and both consumers together and lands as ONE commit (its three parts 1a/1b/1c are TDD sub-steps, not commits).
- Mode for a **terminated** agent pane is `console` (spec D1 as amended 2026-09-18: matches the tab-icon rule in `useTabDisplay`).

---

### Task 1: Three-state badge rendering (one commit: `HostBadge` + hook + both rows)

Task 1 has three TDD parts. Run each part's tests as you go; the whole-app typecheck and the single commit happen at the end of part 1c.

#### Part 1a: `HostBadge` takes resolved colors and exposes CSS custom properties

**Files:**
- Modify: `spa/src/components/HostBadge.tsx`
- Modify: `spa/src/index.css` (append one rule)
- Test: `spa/src/components/HostBadge.test.tsx` (rewrite the `base` fixture and the two color describes)
- Test: `spa/src/index.css.test.ts` (new — guards the hover/active rule)

**Interfaces:**
- Consumes: `ResolvedHostColors` from `spa/src/lib/host-color.ts`.
- Produces:
  ```ts
  export interface HostBadgeProps {
    colors: ResolvedHostColors | null   // rgba strings; null = host has no color
    icon: string | undefined
    iconWeight: IconWeight | undefined
    box: number
    inset: number
    radius: number
    lineColor: 'host' | 'neutral'
    testId?: string
  }
  ```
  Rendered span: `data-host-badge=""`, `data-has-color`; when `colors !== null` the inline style paints `background: <light>` regardless of `lineColor`; when additionally `lineColor === 'host'` it sets `--hb-main` / `--hb-middle` and `color: var(--hb-icon, var(--hb-middle))`, otherwise `color: var(--text-muted)` and no custom properties.

- [ ] **Step 1: Write the failing tests**

Replace the `base` fixture and the `color === null` / `color set` describes in `spa/src/components/HostBadge.test.tsx` (keep the icon / radius / testId tests, changing their `color="#3b82f6"` prop to `colors={COLORS}`):

```tsx
const COLORS = {
  main: 'rgba(59, 130, 246, 1)',
  middle: 'rgba(59, 130, 246, 0.6)',
  light: 'rgba(59, 130, 246, 0.22)',
}

const base = {
  colors: COLORS,
  icon: undefined,
  iconWeight: undefined,
  box: 16,
  inset: 2,
  radius: 4,
  lineColor: 'host' as const,
}

describe('colors === null', () => {
  it('renders no background, a muted icon color, no custom properties, data-has-color false', () => {
    render(<HostBadge {...base} colors={null} />)
    const el = screen.getByTestId('host-badge')
    expect(el.dataset.hasColor).toBe('false')
    expect(el.style.background).toBe('')
    expect(el.style.color).toBe('var(--text-muted)')
    expect(el.style.getPropertyValue('--hb-main')).toBe('')
    expect(el.style.getPropertyValue('--hb-middle')).toBe('')
  })

  it('still renders the host icon', () => {
    render(<HostBadge {...base} colors={null} />)
    expect(screen.getByTestId('host-badge').querySelector('svg')).not.toBeNull()
  })
})

describe('colors set', () => {
  it('paints light as background, exposes main/middle as custom properties, icon falls back to middle', () => {
    render(<HostBadge {...base} />)
    const el = screen.getByTestId('host-badge')
    expect(el.dataset.hasColor).toBe('true')
    expect(el).toHaveAttribute('data-host-badge')
    expect(el.style.background).toBe(COLORS.light)
    expect(el.style.getPropertyValue('--hb-main')).toBe(COLORS.main)
    expect(el.style.getPropertyValue('--hb-middle')).toBe(COLORS.middle)
    expect(el.style.color).toBe('var(--hb-icon, var(--hb-middle))')
  })

  it('uses the muted color for the icon when lineColor is neutral, keeping the tinted background', () => {
    render(<HostBadge {...base} lineColor="neutral" />)
    const el = screen.getByTestId('host-badge')
    expect(el.style.color).toBe('var(--text-muted)')
    expect(el.style.background).toBe(COLORS.light)
    expect(el.style.getPropertyValue('--hb-main')).toBe('')
  })
})
```

Delete the old `colorMix` helper if it becomes unused.

jsdom cannot evaluate the cascade, so the hover/active rule is guarded at the source level. Create `spa/src/index.css.test.ts`:

```ts
import { describe, it, expect } from 'vitest'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'

describe('index.css — host badge state rule', () => {
  const css = readFileSync(resolve(__dirname, 'index.css'), 'utf8')

  it('switches the badge icon to --hb-main on a hovered or active .group row', () => {
    const rule = /\.group:hover \[data-host-badge\],\s*\.group\[data-active="true"\] \[data-host-badge\]\s*\{\s*--hb-icon:\s*var\(--hb-main\);\s*\}/
    expect(css).toMatch(rule)
  })

  it('keeps the rule outside any @layer so utilities cannot outrank it', () => {
    const idx = css.indexOf('[data-host-badge]')
    const before = css.slice(0, idx)
    const opened = (before.match(/@layer\s+[a-z]+\s*\{/g) ?? []).length
    const closed = 0 // Tailwind 4 `@import` lines open no block; a hand-written @layer block would.
    expect(opened).toBe(closed)
  })
})
```
(If `__dirname` is unavailable under the project's vitest ESM config, use `new URL('./index.css', import.meta.url)` with `fileURLToPath`.) Live acceptance (§8.1) remains the real check for hover.

- [ ] **Step 2: Run to verify failure**

Run: `cd spa && npx vitest run src/components/HostBadge.test.tsx src/index.css.test.ts`
Expected: FAIL (prop `colors` unknown / old props required; CSS rule absent).

- [ ] **Step 3: Implement**

`spa/src/components/HostBadge.tsx`:

```tsx
import type { CSSProperties } from 'react'
import type { IconWeight } from '../types/tab'
import type { ResolvedHostColors } from '../lib/host-color'
import { DEFAULT_HOST_ICON } from '../lib/host-color'
import { WorkspaceIcon } from '../features/workspace/components/WorkspaceIcon'

export interface HostBadgeProps {
  /** Resolved per-mode colors (rgba), or null when the host has no color. */
  colors: ResolvedHostColors | null
  /** Phosphor icon name; falls back to `DEFAULT_HOST_ICON`. */
  icon: string | undefined
  /** Phosphor weight; falls back to `regular`. */
  iconWeight: IconWeight | undefined
  /** Outer box size in px. */
  box: number
  /** Padding between the box edge and the icon, in px (icon = box − 2×inset). */
  inset: number
  /** Corner radius in px. */
  radius: number
  lineColor: 'host' | 'neutral'
  testId?: string
}

/**
 * Small tinted square holding the host's own icon. A real flex child, not an
 * overlay: it shifts the title instead of covering it.
 *
 * Three states, no JS: the span publishes `--hb-main` / `--hb-middle`; its icon
 * color reads `--hb-icon` and falls back to `--hb-middle` (inactive). The global
 * rule in `index.css` sets `--hb-icon: var(--hb-main)` on a hovered `.group` row or
 * a `[data-active="true"]` row. Background is always the `light` layer.
 */
export function HostBadge({ colors, icon, iconWeight, box, inset, radius, lineColor, testId }: HostBadgeProps) {
  const hostColored = colors !== null && lineColor === 'host'

  const style: CSSProperties & Record<`--hb-${string}`, string> = {
    display: 'inline-flex',
    alignItems: 'center',
    justifyContent: 'center',
    flexShrink: 0,
    width: `${box}px`,
    height: `${box}px`,
    borderRadius: `${radius}px`,
    color: hostColored ? 'var(--hb-icon, var(--hb-middle))' : 'var(--text-muted)',
  }
  if (colors !== null) style.background = colors.light
  if (hostColored) {
    style['--hb-main'] = colors.main
    style['--hb-middle'] = colors.middle
  }

  return (
    <span
      data-testid={testId ?? 'host-badge'}
      data-host-badge=""
      data-has-color={String(colors !== null)}
      aria-hidden="true"
      style={style}
    >
      <WorkspaceIcon icon={icon ?? DEFAULT_HOST_ICON} name="" size={box - inset * 2} weight={iconWeight ?? 'regular'} />
    </span>
  )
}
```

If TypeScript rejects the `--hb-*` keys on `CSSProperties`, build the object as `Record<string, string | number>` and cast once at the `style=` site (`style={style as CSSProperties}`).

`spa/src/index.css` — append after the imports:

```css
/* Host badge (components/HostBadge.tsx): the icon uses `--hb-middle` on an inactive
   row and `--hb-main` when the row is hovered or active. Rows are `.group` elements
   carrying `data-active`. */
.group:hover [data-host-badge],
.group[data-active="true"] [data-host-badge] {
  --hb-icon: var(--hb-main);
}
```

- [ ] **Step 4: Run to verify pass**

Run: `cd spa && npx vitest run src/components/HostBadge.test.tsx src/index.css.test.ts`
Expected: PASS. (Whole-app `tsc` fails in `SortableTab.tsx` / `InlineTab.tsx` until part 1c — expected; do not commit yet.)

#### Part 1b: `useTabHostBadge` resolves per-mode colors; `hasHostBadge` follows

**Files:**
- Modify: `spa/src/hooks/useTabHostBadge.ts`
- Modify: `spa/src/lib/host-color.ts` (`hasHostBadge` generic)
- Test: `spa/src/hooks/useTabHostBadge.test.ts`, `spa/src/lib/host-color.test.ts` (`hasHostBadge` describe)

**Interfaces:**
- Consumes: `resolveHostColors`, `ResolvedHostColors` (P1); `useAgentStore.agentTypes`; `compositeKey`; `getPrimaryPane`.
- Produces:
  ```ts
  export interface TabHostBadge {
    colors: ResolvedHostColors | null
    icon: string | undefined
    iconWeight: IconWeight | undefined
  }
  export function useTabHostBadge(tab: Tab): TabHostBadge | null
  export function hasHostBadge<T extends { colors: ResolvedHostColors | null; icon?: string | undefined }>(badge: T | null): badge is T
  ```

- [ ] **Step 1: Write the failing tests**

`spa/src/hooks/useTabHostBadge.test.ts` — update the existing assertions from `.color` to `.colors` (`'#3b82f6'` → `{ main: 'rgba(59, 130, 246, 1)', middle: 'rgba(59, 130, 246, 0.6)', light: 'rgba(59, 130, 246, 0.22)' }`, `null` stays `null`) and add (import `useAgentStore` from `'../stores/useAgentStore'`; the file's `tmuxTab(hostId)` uses `sessionCode: 'sess'`, so the composite key is `` `${hostId}:sess` ``):

```ts
  describe('mode', () => {
    const RED = { main: { color: '#ef4444', alpha: 100 } }
    const BLUE = { main: { color: '#3b82f6', alpha: 100 } }

    beforeEach(() => {
      useAgentStore.setState({ agentTypes: {} })
    })

    it('uses console colors when the primary pane has no agentType', () => {
      seedHosts({ ...hostA, colors: { console: BLUE, terminal: RED } })
      const { result } = renderHook(() => useTabHostBadge(tmuxTab('host-a')))
      expect(result.current?.colors?.main).toBe('rgba(59, 130, 246, 1)')
    })

    it('uses terminal colors when the primary pane has an agentType', () => {
      seedHosts({ ...hostA, colors: { console: BLUE, terminal: RED } })
      useAgentStore.setState({ agentTypes: { 'host-a:sess': 'cc' } })
      const { result } = renderHook(() => useTabHostBadge(tmuxTab('host-a')))
      expect(result.current?.colors?.main).toBe('rgba(239, 68, 68, 1)')
    })

    it('falls back to console colors for an agent tab whose host has no terminal set', () => {
      seedHosts({ ...hostA, colors: { console: BLUE } })
      useAgentStore.setState({ agentTypes: { 'host-a:sess': 'cc' } })
      const { result } = renderHook(() => useTabHostBadge(tmuxTab('host-a')))
      expect(result.current?.colors?.main).toBe('rgba(59, 130, 246, 1)')
    })

    it('treats a terminated agent pane as console', () => {
      seedHosts({ ...hostA, colors: { console: BLUE, terminal: RED } })
      useAgentStore.setState({ agentTypes: { 'host-a:sess': 'cc' } })
      const tab = tmuxTab('host-a')
      const pane = (tab.layout as { pane: { content: { terminated?: string } } }).pane
      pane.content.terminated = 'session-closed'
      const { result } = renderHook(() => useTabHostBadge(tab))
      expect(result.current?.colors?.main).toBe('rgba(59, 130, 246, 1)')
    })

    it('re-resolves when the agentType appears without a host write', () => {
      seedHosts({ ...hostA, colors: { console: BLUE, terminal: RED } })
      const { result } = renderHook(() => useTabHostBadge(tmuxTab('host-a')))
      expect(result.current?.colors?.main).toBe('rgba(59, 130, 246, 1)')
      act(() => useAgentStore.setState({ agentTypes: { 'host-a:sess': 'cc' } }))
      expect(result.current?.colors?.main).toBe('rgba(239, 68, 68, 1)')
    })

    it('returns the same colors object across re-renders when nothing changed', () => {
      seedHosts({ ...hostA, colors: { console: BLUE } })
      const { result, rerender } = renderHook(() => useTabHostBadge(tmuxTab('host-a')))
      const first = result.current?.colors
      rerender()
      expect(result.current?.colors).toBe(first)
    })
  })
```

(If `useAgentStore.setState({ agentTypes })` needs sibling fields for its merge-mode harness, copy the `beforeEach` block from `hooks/useTabDisplay.test.ts`.)

`spa/src/lib/host-color.test.ts` `hasHostBadge` describe: replace `color: '#3b82f6'` with `colors: { main: 'x', middle: 'y', light: 'z' }` and `color: null` with `colors: null`, keeping each case's expectation (colors only → true; icon only → true; neither → false; null → false).

- [ ] **Step 2: Run to verify failure**

Run: `cd spa && npx vitest run src/hooks/useTabHostBadge.test.ts src/lib/host-color.test.ts`
Expected: FAIL.

- [ ] **Step 3: Implement**

`spa/src/lib/host-color.ts` — `hasHostBadge`:

```ts
export function hasHostBadge<T extends { colors: ResolvedHostColors | null; icon?: string | undefined }>(
  badge: T | null,
): badge is T {
  return badge !== null && (badge.colors !== null || badge.icon !== undefined)
}
```
(update its doc comment: "color only" → "colors only").

`spa/src/hooks/useTabHostBadge.ts`:

```ts
import { useMemo } from 'react'
import type { IconWeight, Tab } from '../types/tab'
import { useHostStore } from '../stores/useHostStore'
import { useAgentStore } from '../stores/useAgentStore'
import { compositeKey } from '../lib/composite-key'
import { getPrimaryPane } from '../lib/pane-tree'
import {
  getTabHostId,
  isIconWeight,
  isPhosphorIconName,
  resolveHostColors,
  type HostColorMode,
  type ResolvedHostColors,
} from '../lib/host-color'

/** Resolved badge identity for a tab. `colors` null = host has no color; `icon` undefined = default icon. */
export interface TabHostBadge {
  colors: ResolvedHostColors | null
  icon: string | undefined
  iconWeight: IconWeight | undefined
}

/**
 * Badge identity of the tab's host (first tmux pane), or `null` when the tab has
 * no tmux pane. Mode (spec D1): `terminal` when the primary pane has a live
 * agentType, otherwise `console`; `execution` is not wired yet.
 *
 * Primitive selectors plus the `colors` object (identity changes only on a write),
 * resolved under `useMemo` so tab rows do not re-render on unrelated store writes.
 */
export function useTabHostBadge(tab: Tab): TabHostBadge | null {
  const hostId = getTabHostId(tab)
  const primary = getPrimaryPane(tab.layout).content
  const ck =
    primary.kind === 'tmux-session' && primary.hostId && primary.sessionCode && !primary.terminated
      ? compositeKey(primary.hostId, primary.sessionCode)
      : undefined
  // Hooks run unconditionally (Rules of Hooks); the `hostId` bail-out happens after.
  const colors = useHostStore((s) => (hostId ? s.hosts[hostId]?.colors : undefined))
  const legacyColor = useHostStore((s) => (hostId ? s.hosts[hostId]?.color : undefined))
  const icon = useHostStore((s) => (hostId ? s.hosts[hostId]?.icon : undefined))
  const iconWeight = useHostStore((s) => (hostId ? s.hosts[hostId]?.iconWeight : undefined))
  const agentType = useAgentStore((s) => (ck ? s.agentTypes[ck] : undefined))
  const mode: HostColorMode = agentType ? 'terminal' : 'console'

  const resolved = useMemo(
    () => resolveHostColors({ colors, color: legacyColor }, mode),
    [colors, legacyColor, mode],
  )

  if (!hostId) return null
  return {
    colors: resolved,
    // Last line of defence: a value stored before the guard existed (or written by
    // a stranger) must never reach `WorkspaceIcon`, which renders an unknown name
    // as literal text inside the badge.
    icon: isPhosphorIconName(icon) ? icon : undefined,
    iconWeight: isIconWeight(iconWeight) ? iconWeight : undefined,
  }
}
```

- [ ] **Step 4: Run to verify pass**

Run: `cd spa && npx vitest run src/hooks/useTabHostBadge.test.ts src/lib/host-color.test.ts`
Expected: PASS. (Still no commit.)

#### Part 1c: Tab rows pass `colors`, carry `data-active`, drop opacity props

**Files:**
- Modify: `spa/src/components/SortableTab.tsx` (badge selectors + `<HostBadge>` + root `data-active`)
- Modify: `spa/src/features/workspace/components/InlineTab.tsx` (same)
- Test: `spa/src/components/SortableTab.test.tsx`, `spa/src/features/workspace/components/InlineTab.test.tsx`

**Interfaces:**
- Consumes: Task 1 `HostBadgeProps`, Task 2 `TabHostBadge`.
- Produces: both row roots have `data-active="true" | "false"`.

- [ ] **Step 1: Write the failing tests**

In both test files, remove `hostBadge*LineOpacity` / `hostBadge*BgOpacity` from every `useUISettingsStore.setState({...})` fixture. Replace the InlineTab test `passes the sidebar store settings through to the badge` assertion `expect(badge.style.background).toContain('40%')` with `expect(badge.style.background).toBe('rgba(59, 130, 246, 0.22)')` and drop `hostBadgeSidebarBgOpacity: 40` from its `setState`. Do the equivalent in `SortableTab.test.tsx` around line 189 (`hostBadgeTabBarBgOpacity: 40`). Then add to each file's host-badge describe:

```tsx
  it('marks the row data-active and lets the badge read main on the active row', () => {
    setH1Color('#3b82f6')
    renderInline(undefined, { isActive: true })     // InlineTab: use that file's render helper + isActive option
    const row = screen.getByTestId('inline-tab-row')
    expect(row).toHaveAttribute('data-active', 'true')
    const badge = screen.getByTestId('host-badge')
    expect(badge.style.getPropertyValue('--hb-main')).toBe('rgba(59, 130, 246, 1)')
    expect(badge.style.getPropertyValue('--hb-middle')).toBe('rgba(59, 130, 246, 0.6)')
  })

  it('marks an inactive row data-active=false', () => {
    setH1Color('#3b82f6')
    renderInline(undefined, { isActive: false })
    expect(screen.getByTestId('inline-tab-row')).toHaveAttribute('data-active', 'false')
  })
```
For `SortableTab.test.tsx` the row is `screen.getByRole('tab')` and the render helper takes `isActive` — read the file's existing helper signature and pass it the same way. If the InlineTab render helper has no `isActive` option, add one (default `false`) that forwards to the `isActive` prop.

`SortableTab` has a second root for **pinned** tabs (the `w-9` icon-only `<button>` branch, which renders no badge). Add to `SortableTab.test.tsx`:

```tsx
  it('pinned tab root also carries data-active', () => {
    renderSortable({ pinned: true, isActive: true })      // use the file's helper; pass pinned + isActive the way it expects
    expect(screen.getByRole('button')).toHaveAttribute('data-active', 'true')
    renderSortable({ pinned: true, isActive: false })
    expect(screen.getAllByRole('button').at(-1)).toHaveAttribute('data-active', 'false')
  })
```
(Adjust the role query to whatever the pinned branch actually renders — read `SortableTab.tsx` lines ~95–115 first; if it is a `div role="tab"` too, query by `getAllByRole('tab')`.)

- [ ] **Step 2: Run to verify failure**

Run: `cd spa && npx vitest run src/components/SortableTab.test.tsx src/features/workspace/components/InlineTab.test.tsx`
Expected: FAIL (`data-active` missing; badge still receives old props → tsc/runtime errors).

- [ ] **Step 3: Implement**

In both files:
- delete the `badgeLineOpacity` / `badgeBgOpacity` selectors;
- `<HostBadge colors={hostBadge.colors} icon={hostBadge.icon} iconWeight={hostBadge.iconWeight} box={badgeBox} inset={badgeInset} radius={badgeRadius} lineColor={badgeLineColor} />`;
- add `data-active={String(isActive)}` on the row root (`SortableTab.tsx` — the `<div role="tab" …>` (the non-pinned branch) **and** the pinned `w-9` branch if it is a separate root, for consistency; `InlineTab.tsx` — the `<div data-testid="inline-tab-row" …>`).

- [ ] **Step 4: Run to verify pass + whole-app typecheck**

Run: `cd spa && npx vitest run src/components/SortableTab.test.tsx src/features/workspace/components/InlineTab.test.tsx src/components/HostBadge.test.tsx src/index.css.test.ts src/hooks src/lib/host-color.test.ts && npx tsc --noEmit -p tsconfig.app.json && pnpm run lint`
Expected: PASS; tsc green (the opacity settings still exist in the store until Task 2, and `TerminalSection`/`HostBadgeSetting` still use them — that is fine).

- [ ] **Step 5: Commit (the only commit of Task 1)**

```bash
git commit --only spa/src/components/HostBadge.tsx spa/src/components/HostBadge.test.tsx spa/src/index.css spa/src/index.css.test.ts spa/src/hooks/useTabHostBadge.ts spa/src/hooks/useTabHostBadge.test.ts spa/src/lib/host-color.ts spa/src/lib/host-color.test.ts spa/src/components/SortableTab.tsx spa/src/components/SortableTab.test.tsx spa/src/features/workspace/components/InlineTab.tsx spa/src/features/workspace/components/InlineTab.test.tsx -m "feat(spa): host badge renders per-mode tri-colors (main on active/hover, middle inactive, light background)"
```

---

### Task 2: Remove the per-surface opacity settings; caption the remaining inputs

**Files:**
- Modify: `spa/src/stores/useUISettingsStore.ts` (constants `HOST_BADGE_{LINE,BG}_OPACITY_*`, `clampHostBadge{Line,Bg}Opacity`, the two entries per surface in `HOST_BADGE_NUMERIC_CLAMPS`, `HOST_BADGE_DEFAULTS`, the interface fields + setters, the initial state + setter implementations; update the "14 host badge fields" comment to 10)
- Modify: `spa/src/lib/sync/contributors/preferences.ts` (`DATA_FIELDS`: remove the 4 entries)
- Modify: `spa/src/components/settings/HostBadgeSetting.tsx` (props, `numbers` list, captioned layout)
- Modify: `spa/src/components/settings/TerminalSection.tsx` (drop the 8 opacity selectors/props)
- Modify: `spa/src/locales/en.json`, `spa/src/locales/zh-TW.json` (remove `settings.terminal.host_badge.line_opacity` / `.bg_opacity`; add `.box.caption` / `.inset.caption` / `.radius.caption`)
- Test: `spa/src/stores/useUISettingsStore.test.ts`, `spa/src/lib/sync/contributors/preferences.test.ts`, `spa/src/components/settings/HostBadgeSetting.test.tsx`, `spa/src/components/settings/TerminalSection.test.tsx`

**Interfaces:**
- Produces: `HostBadgeSettingProps` without `lineOpacity / bgOpacity / onLineOpacityChange / onBgOpacityChange`; `UISettings` without the 4 fields and 4 setters; `HOST_BADGE_DEFAULTS` with 10 keys.

- [ ] **Step 1: Write the failing tests**

- `useUISettingsStore.test.ts`: first `rg -n 'LineOpacity|BgOpacity' spa/src/stores/useUISettingsStore.test.ts` and handle **every** hit: remove the `clampHostBadgeLineOpacity` / `clampHostBadgeBgOpacity` imports and their assertions (~lines 337–343); remove the `LineOpacity` / `BgOpacity` expectations (~288–289) and list entries (~301–309); in the **rehydrate** tests that seed the four keys into persisted state and assert them back, delete those seeds/assertions. Then add:
  ```ts
  const REMOVED = ['hostBadgeSidebarLineOpacity', 'hostBadgeSidebarBgOpacity', 'hostBadgeTabBarLineOpacity', 'hostBadgeTabBarBgOpacity'] as const

  it('has no per-surface opacity fields any more', () => {
    const s = useUISettingsStore.getState() as Record<string, unknown>
    for (const k of REMOVED) expect(k in s).toBe(false)
    expect(Object.keys(HOST_BADGE_DEFAULTS)).toHaveLength(10)
  })

  it('sanitizeHostBadgePrefs strips the removed opacity keys', () => {
    expect(sanitizeHostBadgePrefs({ hostBadgeSidebarBgOpacity: 40, hostBadgeSidebarBox: 16 })).toEqual({ hostBadgeSidebarBox: 16 })
  })

  it('rehydrating v3 persisted state with the removed keys does not carry them into the store', async () => {
    // Follow the file's existing rehydrate harness (it writes a JSON blob under STORAGE_KEYS.UI_SETTINGS
    // and calls useUISettingsStore.persist.rehydrate()); seed { version: 3, state: { ...defaults, hostBadgeSidebarBgOpacity: 40 } }.
    await rehydrateWith({ hostBadgeSidebarBgOpacity: 40 })
    const s = useUISettingsStore.getState() as Record<string, unknown>
    for (const k of REMOVED) expect(k in s).toBe(false)
  })
  ```
  Implementation for the last test (in `useUISettingsStore.ts`): export `HOST_BADGE_REMOVED_FIELDS = REMOVED`; `sanitizeHostBadgePrefs` deletes them; bump persist `version: 3 → 4` and, in `migrate`, when `version < 4` delete the four keys from the incoming state (keep the existing v1/v2 handling intact). A `migrate` step is the only hook that can *remove* keys — `onRehydrateStorage` + `setState` can only add or overwrite. In `preferences.ts` `normalizeIncoming` already runs `sanitizeHostBadgePrefs`, so a stale remote key is stripped on the sync path too.
- `preferences.test.ts`: `rg -n 'LineOpacity|BgOpacity' spa/src/lib/sync/contributors/preferences.test.ts` — remove the four names from the expected field list(s) AND from every valid/hostile deserialize fixture and assertion; add one test: deserializing a payload that still carries `hostBadgeSidebarBgOpacity: 40` leaves `'hostBadgeSidebarBgOpacity' in useUISettingsStore.getState()` false.
- `HostBadgeSetting.test.tsx`: remove props/tests for `line-opacity` / `bg-opacity`; add
  ```tsx
  it('shows a caption above each numeric input', () => {
    render(<HostBadgeSetting {...base} />)
    for (const [id, caption] of [['box', 'Size'], ['inset', 'Inset'], ['radius', 'Radius']] as const) {
      const input = screen.getByTestId(`${base.testIdPrefix}-${id}`)
      const label = input.closest('label')
      expect(label).not.toBeNull()
      expect(label!.textContent).toContain(caption)
    }
    expect(screen.queryByTestId(`${base.testIdPrefix}-line-opacity`)).toBeNull()
    expect(screen.queryByTestId(`${base.testIdPrefix}-bg-opacity`)).toBeNull()
  })
  ```
  (`base` = that file's existing props fixture minus the removed props.)
- `TerminalSection.test.tsx`: in the loop at ~line 204 change the id list to `['box', 'inset', 'radius']`; delete the two `line-opacity` / `bg-opacity` `fireEvent.change` blocks (~219–225).

- [ ] **Step 2: Run to verify failure**

Run: `cd spa && npx vitest run src/stores/useUISettingsStore.test.ts src/lib/sync/contributors/preferences.test.ts src/components/settings`
Expected: FAIL (fields still present; captions missing).

- [ ] **Step 3: Implement**

`HostBadgeSetting.tsx` — `numbers` keeps only `box` / `inset` / `radius`, each gaining `captionKey: 'settings.terminal.host_badge.box.caption'` etc.; render:

```tsx
        <div className="flex flex-wrap items-start justify-end gap-3">
          {numbers.map((n) => (
            <label key={n.id} className="flex flex-col items-start gap-0.5">
              <span className="text-[11px] leading-none text-text-muted">{t(n.captionKey)}</span>
              <span className="flex items-center gap-1">
                <input
                  type="number"
                  data-testid={`${testIdPrefix}-${n.id}`}
                  aria-label={`${label}: ${t(n.labelKey)}`}
                  min={n.min}
                  max={n.max}
                  step={1}
                  disabled={!enabled}
                  value={n.value}
                  onChange={(e) => { if (enabled) n.onChange(n.clamp(Number(e.target.value))) }}
                  className={INPUT_CLASS}
                />
                <span className="text-xs text-text-muted">{t('settings.terminal.host_badge.px')}</span>
              </span>
            </label>
          ))}
        </div>
```
(`suffix` field and the `%` branch go away.)

Locales (`en.json`, next to the existing `settings.terminal.host_badge.*` keys; remove `line_opacity` / `bg_opacity`):
```json
  "settings.terminal.host_badge.box.caption": "Size",
  "settings.terminal.host_badge.inset.caption": "Inset",
  "settings.terminal.host_badge.radius.caption": "Radius",
```
`zh-TW.json`: `"大小"`, `"內縮"`, `"圓角"`.

Store / sync / TerminalSection: delete as listed under **Files**, plus the `HOST_BADGE_REMOVED_FIELDS` strip in `sanitizeHostBadgePrefs` and the v4 `migrate` step described in Step 1. Grep afterwards: `rg -n 'LineOpacity|BgOpacity|LINE_OPACITY|BG_OPACITY|line_opacity|bg_opacity' spa/src` must return only the `HOST_BADGE_REMOVED_FIELDS` list and its tests.

- [ ] **Step 4: Run to verify pass + full suite**

Run: `cd spa && npx vitest run && pnpm run lint && npx tsc --noEmit -p tsconfig.app.json`
Expected: all green.

- [ ] **Step 5: Commit**

```bash
git commit --only spa/src/stores/useUISettingsStore.ts spa/src/stores/useUISettingsStore.test.ts spa/src/lib/sync/contributors/preferences.ts spa/src/lib/sync/contributors/preferences.test.ts spa/src/components/settings/HostBadgeSetting.tsx spa/src/components/settings/HostBadgeSetting.test.tsx spa/src/components/settings/TerminalSection.tsx spa/src/components/settings/TerminalSection.test.tsx spa/src/locales/en.json spa/src/locales/zh-TW.json -m "feat(spa): drop per-surface badge opacity settings; caption the remaining inputs"
```

---

## Done criteria (P2)

- Full suite, lint, tsc green; `rg 'LineOpacity|BgOpacity|line_opacity|bg_opacity' spa/src` hits only `HOST_BADGE_REMOVED_FIELDS` and its tests.
- Live (`:5174`, after merge): spec §8 items 1, 2, 5 — legacy-colored host looks like alpha.362 on the active tab, dimmer (60%) on inactive tabs, full on hover; Settings → Terminal shows Size / Inset / Radius captions and no percent fields.
- ≤ 20 files / ≤ 800 lines (expected ≈ 18 files).
