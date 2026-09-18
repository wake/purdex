# Host Color Modes — P1 (data layer) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Land the per-mode tri-color data model (`HostConfig.colors`), its validation, the pure resolver, and the `✳` title-marker strip — with **no visible change** to the badge yet.

**Architecture:** All new logic is pure functions in `spa/src/lib/` (`host-color.ts`, `color-space.ts`, `agent-title-marker.ts`) with the Zustand stores as thin writers. `setHostColor` (used by the existing color UI) is re-implemented on top of the new actions so today's UI keeps working and already writes the new shape. `useTabHostBadge` keeps its current return type by reading the resolved console main color — the visual rewrite is P2.

**Tech Stack:** React 19 / Zustand 5 / Vitest / TypeScript strict. Run tests with `cd spa && npx vitest run <path>`; lint with `cd spa && pnpm run lint`; typecheck with `cd spa && npx tsc --noEmit -p tsconfig.app.json` (check the exact tsconfig name with `ls spa/tsconfig*.json` first).

**Spec:** `docs/specs/2026-09-18-host-color-modes-spec.md` — §4 (data model, validation, resolver, settings, marker) and §7 row P1. Read it before starting.

## Global Constraints

- Worktree: `/Users/wake/Workspace/wake/purdex/.claude/worktrees/host-color-modes` — every shell command starts with `cd` into it (or into its `spa/`).
- TDD: the failing test is written and run **before** the implementation in every task.
- One commit per task, `git commit --only <files>` (other sessions may share the index).
- No persist migration; legacy `HostConfig.color` is read-only (spec D10).
- Alpha values are **integers 0–100**; defaults `main 100 / middle 60 / light 22` (spec §4.1).
- Modes are exactly `'console' | 'terminal' | 'execution'` (spec D1). Nothing resolves to `execution` in P1.
- Locale keys must exist in both `spa/src/locales/en.json` and `zh-TW.json` (`locale-completeness.test.ts`).
- P1 must not change what the user sees: badge colors and titles (with the new toggle **on**, `✳` disappears — that is the one intended visible change of P1).

---

### Task 1: Types, guards and alpha defaults (`lib/host-color.ts`)

**Files:**
- Modify: `spa/src/lib/host-color.ts` (append after `normalizeHostColor`)
- Test: `spa/src/lib/host-color.test.ts` (append)

**Interfaces:**
- Produces:
  ```ts
  export type HostColorMode = 'console' | 'terminal' | 'execution'
  export const HOST_COLOR_MODES: readonly HostColorMode[]
  export type HostColorLayerName = 'main' | 'middle' | 'light'
  export interface HostColorLayer { color?: string; alpha: number }
  export interface HostColorSet { main: HostColorLayer & { color: string }; middle?: HostColorLayer; light?: HostColorLayer }
  export const HOST_COLOR_ALPHA_DEFAULTS: Readonly<Record<HostColorLayerName, number>>  // { main: 100, middle: 60, light: 22 }
  export function isHostColorMode(v: unknown): v is HostColorMode
  export function clampHostAlpha(n: number): number            // round, bound 0–100; NaN → 0
  export function isHostColorLayer(v: unknown, opts?: { requireColor?: boolean }): v is HostColorLayer
  export function isHostColorSet(v: unknown): v is HostColorSet
  ```

- [ ] **Step 1: Write the failing tests**

Append to `spa/src/lib/host-color.test.ts` (extend the existing import line from `./host-color` with the new names):

```ts
describe('host color modes — guards', () => {
  it('HOST_COLOR_MODES is exactly console/terminal/execution', () => {
    expect(HOST_COLOR_MODES).toEqual(['console', 'terminal', 'execution'])
  })

  it.each(['console', 'terminal', 'execution'])('isHostColorMode accepts %s', (m) => {
    expect(isHostColorMode(m)).toBe(true)
  })
  it.each(['shell', '', 'Console', 1, null, undefined])('isHostColorMode rejects %j', (m) => {
    expect(isHostColorMode(m)).toBe(false)
  })

  it('HOST_COLOR_ALPHA_DEFAULTS is main 100 / middle 60 / light 22', () => {
    expect(HOST_COLOR_ALPHA_DEFAULTS).toEqual({ main: 100, middle: 60, light: 22 })
  })

  it.each([
    [50, 50], [-5, 0], [150, 100], [33.4, 33], [33.5, 34], [NaN, 0], [Infinity, 100],
  ])('clampHostAlpha(%j) = %j', (input, expected) => {
    expect(clampHostAlpha(input)).toBe(expected)
  })

  it('isHostColorLayer accepts alpha-only and color+alpha layers', () => {
    expect(isHostColorLayer({ alpha: 60 })).toBe(true)
    expect(isHostColorLayer({ color: '#3b82f6', alpha: 100 })).toBe(true)
  })
  it.each([
    { alpha: 101 }, { alpha: -1 }, { alpha: 1.5 }, { alpha: '60' }, {}, { color: '#3b82f6' },
    { color: 'red', alpha: 50 }, { color: '#ABC', alpha: 50 }, null, 'x', [],
  ])('isHostColorLayer rejects %j', (bad) => {
    expect(isHostColorLayer(bad)).toBe(false)
  })
  it('isHostColorLayer with requireColor rejects an alpha-only layer', () => {
    expect(isHostColorLayer({ alpha: 100 }, { requireColor: true })).toBe(false)
    expect(isHostColorLayer({ color: '#3b82f6', alpha: 100 }, { requireColor: true })).toBe(true)
  })

  it('isHostColorSet requires main with a color; middle/light optional', () => {
    expect(isHostColorSet({ main: { color: '#3b82f6', alpha: 100 } })).toBe(true)
    expect(isHostColorSet({ main: { color: '#3b82f6', alpha: 100 }, middle: { alpha: 60 }, light: { color: '#000000', alpha: 10 } })).toBe(true)
  })
  it.each([
    { main: { alpha: 100 } },
    { middle: { alpha: 60 } },
    { main: { color: '#3b82f6', alpha: 100 }, middle: { alpha: 200 } },
    { main: { color: '#3b82f6', alpha: 100 }, light: 'x' },
    null, {}, [],
  ])('isHostColorSet rejects %j', (bad) => {
    expect(isHostColorSet(bad)).toBe(false)
  })
})
```

- [ ] **Step 2: Run to verify failure**

Run: `cd spa && npx vitest run src/lib/host-color.test.ts`
Expected: FAIL — `HOST_COLOR_MODES` etc. are not exported.

- [ ] **Step 3: Implement**

Append to `spa/src/lib/host-color.ts` after `normalizeHostColor`:

```ts
/* ─── Per-mode tri-color (spec 2026-09-18 host-color-modes §4.1) ─── */

export type HostColorMode = 'console' | 'terminal' | 'execution'
export const HOST_COLOR_MODES: readonly HostColorMode[] = ['console', 'terminal', 'execution']

export type HostColorLayerName = 'main' | 'middle' | 'light'

export interface HostColorLayer {
  /** `#rrggbb`. Absent on middle/light = inherit the mode's main color. Required on main. */
  color?: string
  /** Integer 0–100. */
  alpha: number
}

export interface HostColorSet {
  main: HostColorLayer & { color: string }
  middle?: HostColorLayer
  light?: HostColorLayer
}

/** Alpha used when a layer is absent (light 22 = the alpha.362 background default). */
export const HOST_COLOR_ALPHA_DEFAULTS: Readonly<Record<HostColorLayerName, number>> = {
  main: 100,
  middle: 60,
  light: 22,
}

export function isHostColorMode(v: unknown): v is HostColorMode {
  return typeof v === 'string' && (HOST_COLOR_MODES as readonly string[]).includes(v)
}

/** Rounds and bounds to an integer 0–100. NaN → 0. */
export function clampHostAlpha(n: number): number {
  if (Number.isNaN(n)) return 0
  return Math.min(100, Math.max(0, Math.round(n)))
}

function isPlainObject(v: unknown): v is Record<string, unknown> {
  return typeof v === 'object' && v !== null && !Array.isArray(v)
}

/**
 * Structural guard for a stored layer: `alpha` must already be an integer 0–100
 * (sanitize drops, it does not clamp); `color`, when present, must be `#rrggbb`.
 */
export function isHostColorLayer(v: unknown, opts?: { requireColor?: boolean }): v is HostColorLayer {
  if (!isPlainObject(v)) return false
  const { alpha, color } = v
  if (typeof alpha !== 'number' || !Number.isInteger(alpha) || alpha < 0 || alpha > 100) return false
  if ('color' in v && !isValidHostColor(color)) return false
  if (opts?.requireColor && !('color' in v)) return false
  return true
}

export function isHostColorSet(v: unknown): v is HostColorSet {
  if (!isPlainObject(v)) return false
  if (!isHostColorLayer(v.main, { requireColor: true })) return false
  if ('middle' in v && !isHostColorLayer(v.middle)) return false
  if ('light' in v && !isHostColorLayer(v.light)) return false
  return true
}
```

- [ ] **Step 4: Run to verify pass**

Run: `cd spa && npx vitest run src/lib/host-color.test.ts`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add spa/src/lib/host-color.ts spa/src/lib/host-color.test.ts
git commit --only spa/src/lib/host-color.ts spa/src/lib/host-color.test.ts -m "feat(spa): host color mode types and guards"
```

---

### Task 2: `HostConfig.colors`, sanitize, delete dead `resolveTabHostColor`

**Files:**
- Modify: `spa/src/stores/useHostStore.ts:24-35` (add the `colors` field next to `color`)
- Modify: `spa/src/lib/host-color.ts` (`sanitizeHostConfig`; delete `resolveTabHostColor`)
- Test: `spa/src/lib/host-color.test.ts` (delete the `resolveTabHostColor` describe at ~line 116; extend `sanitizeHostConfig`)

**Interfaces:**
- Consumes: Task 1 guards.
- Produces: `HostConfig.colors?: Partial<Record<HostColorMode, HostColorSet>>`; `sanitizeHostConfig` now also cleans `colors`.

- [ ] **Step 1: Write the failing tests**

In `spa/src/lib/host-color.test.ts` delete the whole `describe('resolveTabHostColor', …)` block and remove `resolveTabHostColor` from the import. Then append inside the existing `describe('sanitizeHostConfig', …)`:

```ts
  const set = (color: string) => ({ main: { color, alpha: 100 } })

  it('keeps a fully valid colors map (same object)', () => {
    const h: HostConfig = { ...base, colors: { console: set('#3b82f6'), terminal: { ...set('#ef4444'), middle: { alpha: 40 } } } }
    expect(sanitizeHostConfig(h)).toBe(h)
  })

  it.each([null, 'x', 42, []])('drops colors when it is not a plain object: %j', (bad) => {
    const out = sanitizeHostConfig({ ...base, colors: bad as never })
    expect('colors' in out).toBe(false)
  })

  it('drops unknown mode keys and keeps the valid ones', () => {
    const out = sanitizeHostConfig({ ...base, colors: { console: set('#3b82f6'), shell: set('#000000') } as never })
    expect(out.colors).toEqual({ console: set('#3b82f6') })
  })

  it('drops a set whose main is invalid', () => {
    const out = sanitizeHostConfig({ ...base, colors: { console: set('#3b82f6'), terminal: { main: { alpha: 100 } } } as never })
    expect(out.colors).toEqual({ console: set('#3b82f6') })
  })

  it('drops an invalid middle/light layer but keeps the rest of the set', () => {
    const out = sanitizeHostConfig({
      ...base,
      colors: { console: { ...set('#3b82f6'), middle: { alpha: 500 }, light: { color: '#000000', alpha: 10 } } } as never,
    })
    expect(out.colors).toEqual({ console: { ...set('#3b82f6'), light: { color: '#000000', alpha: 10 } } })
  })

  it('removes colors entirely when no mode survives', () => {
    const out = sanitizeHostConfig({ ...base, colors: { bogus: set('#3b82f6') } as never })
    expect('colors' in out).toBe(false)
  })

  it('cleans colors and legacy color independently', () => {
    const out = sanitizeHostConfig({ ...base, color: 'red', colors: { console: set('#3b82f6') } } as never)
    expect('color' in out).toBe(false)
    expect(out.colors).toEqual({ console: set('#3b82f6') })
  })
```

- [ ] **Step 2: Run to verify failure**

Run: `cd spa && npx vitest run src/lib/host-color.test.ts`
Expected: the new sanitize tests FAIL (colors passes through untouched); typecheck errors on `colors` are fine at this point.

- [ ] **Step 3: Implement**

`spa/src/stores/useHostStore.ts` — add after the `color?: string` doc block (keep `color`, mark it legacy):

```ts
  /**
   * Per-mode tri-color for the host badge (spec 2026-09-18 host-color-modes §4.1).
   * Absent mode → inherits `console`; absent `console` → legacy `color`, then "no color".
   */
  colors?: Partial<Record<HostColorMode, HostColorSet>>
```
and change the `color` doc comment's first line to `* @deprecated Legacy single color; read-only (spec D10). New code writes `colors`.`. Add `HostColorMode, HostColorSet` to the type import from `'../lib/host-color'` (it is a value import today — add a separate `import type { HostColorMode, HostColorSet } from '../lib/host-color'`).

`spa/src/lib/host-color.ts` — delete `resolveTabHostColor` and replace `sanitizeHostConfig` with:

```ts
/**
 * Returns a cleaned `colors` map, or `undefined` when nothing valid survives.
 * `same` is true when the input can be kept by reference.
 */
function sanitizeHostColors(v: unknown): { value: HostConfig['colors']; same: boolean } {
  if (!isPlainObject(v)) return { value: undefined, same: false }
  let same = true
  const out: Partial<Record<HostColorMode, HostColorSet>> = {}
  for (const [mode, rawSet] of Object.entries(v)) {
    if (!isHostColorMode(mode) || !isPlainObject(rawSet) || !isHostColorLayer(rawSet.main, { requireColor: true })) {
      same = false
      continue
    }
    const cleaned: HostColorSet = { main: rawSet.main }
    let setSame = true
    for (const layer of ['middle', 'light'] as const) {
      if (!(layer in rawSet)) continue
      if (isHostColorLayer(rawSet[layer])) cleaned[layer] = rawSet[layer]
      else setSame = false
    }
    if (Object.keys(rawSet).some((k) => k !== 'main' && k !== 'middle' && k !== 'light')) setSame = false
    out[mode] = setSame ? (rawSet as HostColorSet) : cleaned
    if (!setSame) same = false
  }
  if (Object.keys(out).length === 0) return { value: undefined, same: false }
  return { value: out, same }
}

/**
 * Drops present-but-invalid identity keys (`color`, `colors`, `icon`, `iconWeight`)
 * from an untrusted host config (sync payload, persisted state). Returns the same
 * object when every present key is valid.
 */
export function sanitizeHostConfig(host: HostConfig): HostConfig {
  const badColor = 'color' in host && !isValidHostColor(host.color)
  const badIcon = 'icon' in host && !isPhosphorIconName(host.icon)
  const badWeight = 'iconWeight' in host && !isIconWeight(host.iconWeight)
  const colors = 'colors' in host ? sanitizeHostColors(host.colors) : null
  const badColors = colors !== null && !colors.same
  if (!badColor && !badIcon && !badWeight && !badColors) return host

  const cleaned = { ...host }
  if (badColor) delete cleaned.color
  if (badIcon) delete cleaned.icon
  if (badWeight) delete cleaned.iconWeight
  if (badColors) {
    if (colors.value === undefined) delete cleaned.colors
    else cleaned.colors = colors.value
  }
  return cleaned
}
```

`isPlainObject` from Task 1 must be declared above this (it is, if Task 1 placed it before the guards — move it up if needed).

- [ ] **Step 4: Run to verify pass + typecheck**

Run: `cd spa && npx vitest run src/lib/host-color.test.ts && npx tsc --noEmit -p tsconfig.app.json`
Expected: PASS, no type errors (search the repo for any other `resolveTabHostColor` usage first: `rg resolveTabHostColor spa/src` must return nothing).

- [ ] **Step 5: Commit**

```bash
git commit --only spa/src/lib/host-color.ts spa/src/lib/host-color.test.ts spa/src/stores/useHostStore.ts -m "feat(spa): HostConfig.colors with sanitize; drop dead resolveTabHostColor"
```

---

### Task 3: Resolver (`resolveHostColorSet` / `resolveHostColors`) + `color-space.ts`

**Files:**
- Create: `spa/src/lib/color-space.ts`
- Create: `spa/src/lib/color-space.test.ts`
- Modify: `spa/src/lib/host-color.ts` (append)
- Test: `spa/src/lib/host-color.test.ts` (append)

**Interfaces:**
- Consumes: Task 1 types, `HostConfig.colors` / `color`.
- Produces:
  ```ts
  // color-space.ts
  export function hexToRgb(hex: string): { r: number; g: number; b: number } | null   // '#rrggbb' only
  export function rgbaString(hex: string, alphaPct: number): string | null           // 'rgba(r, g, b, a)'
  // host-color.ts
  export type HostColorSource = Pick<HostConfig, 'colors' | 'color'>
  export interface ResolvedHostColorSet { main: { color: string; alpha: number }; middle: {…same}; light: {…same} }
  export function resolveHostColorSet(host: HostColorSource | undefined, mode: HostColorMode): ResolvedHostColorSet | null
  export interface ResolvedHostColors { main: string; middle: string; light: string }
  export function resolveHostColors(host: HostColorSource | undefined, mode: HostColorMode): ResolvedHostColors | null
  ```

- [ ] **Step 1: Write the failing tests**

`spa/src/lib/color-space.test.ts`:

```ts
import { describe, it, expect } from 'vitest'
import { hexToRgb, rgbaString } from './color-space'

describe('hexToRgb', () => {
  it('parses #rrggbb (any case)', () => {
    expect(hexToRgb('#3b82f6')).toEqual({ r: 59, g: 130, b: 246 })
    expect(hexToRgb('#3B82F6')).toEqual({ r: 59, g: 130, b: 246 })
  })
  it.each(['3b82f6', '#abc', '#gggggg', '', 'red'])('rejects %j', (bad) => {
    expect(hexToRgb(bad)).toBeNull()
  })
})

describe('rgbaString', () => {
  it('formats alpha percent as a 0–1 fraction', () => {
    expect(rgbaString('#3b82f6', 22)).toBe('rgba(59, 130, 246, 0.22)')
    expect(rgbaString('#3b82f6', 100)).toBe('rgba(59, 130, 246, 1)')
    expect(rgbaString('#3b82f6', 0)).toBe('rgba(59, 130, 246, 0)')
  })
  it('returns null for an invalid hex', () => {
    expect(rgbaString('red', 50)).toBeNull()
  })
})
```

Append to `spa/src/lib/host-color.test.ts`:

```ts
describe('resolveHostColorSet / resolveHostColors', () => {
  const console_ = { main: { color: '#3b82f6', alpha: 100 } }

  it('returns null for undefined host / no colors / no legacy color', () => {
    expect(resolveHostColorSet(undefined, 'console')).toBeNull()
    expect(resolveHostColorSet({}, 'terminal')).toBeNull()
    expect(resolveHostColors({}, 'console')).toBeNull()
  })

  it('fills middle/light from main with default alphas', () => {
    expect(resolveHostColorSet({ colors: { console: console_ } }, 'console')).toEqual({
      main: { color: '#3b82f6', alpha: 100 },
      middle: { color: '#3b82f6', alpha: 60 },
      light: { color: '#3b82f6', alpha: 22 },
    })
  })

  it('keeps explicit middle/light colors and alphas', () => {
    const set = { ...console_, middle: { alpha: 40 }, light: { color: '#000000', alpha: 10 } }
    expect(resolveHostColorSet({ colors: { console: set } }, 'console')).toEqual({
      main: { color: '#3b82f6', alpha: 100 },
      middle: { color: '#3b82f6', alpha: 40 },
      light: { color: '#000000', alpha: 10 },
    })
  })

  it('terminal / execution fall back to console when unset', () => {
    const host = { colors: { console: console_ } }
    expect(resolveHostColorSet(host, 'terminal')?.main.color).toBe('#3b82f6')
    expect(resolveHostColorSet(host, 'execution')?.main.color).toBe('#3b82f6')
  })

  it('a mode with its own set does not inherit console layers', () => {
    const host = { colors: { console: { ...console_, middle: { alpha: 10 } }, terminal: { main: { color: '#ef4444', alpha: 90 } } } }
    expect(resolveHostColorSet(host, 'terminal')).toEqual({
      main: { color: '#ef4444', alpha: 90 },
      middle: { color: '#ef4444', alpha: 60 },
      light: { color: '#ef4444', alpha: 22 },
    })
  })

  it('legacy color acts as console main with default alphas, for every mode', () => {
    expect(resolveHostColorSet({ color: '#22c55e' }, 'terminal')).toEqual({
      main: { color: '#22c55e', alpha: 100 },
      middle: { color: '#22c55e', alpha: 60 },
      light: { color: '#22c55e', alpha: 22 },
    })
  })

  it('colors wins over legacy color', () => {
    expect(resolveHostColorSet({ color: '#22c55e', colors: { console: console_ } }, 'console')?.main.color).toBe('#3b82f6')
  })

  it('an invalid legacy color yields null instead of throwing', () => {
    expect(resolveHostColorSet({ color: 'red' }, 'console')).toBeNull()
  })

  it('resolveHostColors emits rgba strings', () => {
    expect(resolveHostColors({ colors: { console: console_ } }, 'console')).toEqual({
      main: 'rgba(59, 130, 246, 1)',
      middle: 'rgba(59, 130, 246, 0.6)',
      light: 'rgba(59, 130, 246, 0.22)',
    })
  })
})
```

- [ ] **Step 2: Run to verify failure**

Run: `cd spa && npx vitest run src/lib/color-space.test.ts src/lib/host-color.test.ts`
Expected: FAIL (module / exports missing).

- [ ] **Step 3: Implement**

`spa/src/lib/color-space.ts`:

```ts
/** Pure color conversions for the host badge. `#rrggbb` in, numbers out. */

const HEX6_RE = /^#([0-9a-f]{2})([0-9a-f]{2})([0-9a-f]{2})$/i

export function hexToRgb(hex: string): { r: number; g: number; b: number } | null {
  const m = HEX6_RE.exec(hex)
  if (!m) return null
  return { r: parseInt(m[1], 16), g: parseInt(m[2], 16), b: parseInt(m[3], 16) }
}

/** `rgba(r, g, b, a)` with `a` = alphaPct / 100 (0–1). Null for an invalid hex. */
export function rgbaString(hex: string, alphaPct: number): string | null {
  const rgb = hexToRgb(hex)
  if (!rgb) return null
  const a = Math.min(1, Math.max(0, alphaPct / 100))
  return `rgba(${rgb.r}, ${rgb.g}, ${rgb.b}, ${a})`
}
```

Append to `spa/src/lib/host-color.ts` (add `import { rgbaString } from './color-space'` at the top):

```ts
/* ─── Resolver (spec §4.3) ─── */

export type HostColorSource = Pick<HostConfig, 'colors' | 'color'>

export interface ResolvedHostColorLayer { color: string; alpha: number }
export interface ResolvedHostColorSet {
  main: ResolvedHostColorLayer
  middle: ResolvedHostColorLayer
  light: ResolvedHostColorLayer
}

/**
 * Hex-level resolution with every inheritance applied:
 * `colors[mode]` → `colors.console` → legacy `color` → null.
 * Within the chosen set, middle/light take main's color when their own is absent
 * and the §4.1 default alpha when the layer is absent.
 */
export function resolveHostColorSet(host: HostColorSource | undefined, mode: HostColorMode): ResolvedHostColorSet | null {
  if (!host) return null
  const set = host.colors?.[mode] ?? host.colors?.console
  if (set) {
    if (!isHostColorSet(set)) return null
    const main = set.main.color
    return {
      main: { color: main, alpha: set.main.alpha },
      middle: { color: set.middle?.color ?? main, alpha: set.middle?.alpha ?? HOST_COLOR_ALPHA_DEFAULTS.middle },
      light: { color: set.light?.color ?? main, alpha: set.light?.alpha ?? HOST_COLOR_ALPHA_DEFAULTS.light },
    }
  }
  if (!isValidHostColor(host.color)) return null
  return {
    main: { color: host.color, alpha: HOST_COLOR_ALPHA_DEFAULTS.main },
    middle: { color: host.color, alpha: HOST_COLOR_ALPHA_DEFAULTS.middle },
    light: { color: host.color, alpha: HOST_COLOR_ALPHA_DEFAULTS.light },
  }
}

export interface ResolvedHostColors { main: string; middle: string; light: string }

/** `resolveHostColorSet` rendered to `rgba()` strings, ready for inline CSS. */
export function resolveHostColors(host: HostColorSource | undefined, mode: HostColorMode): ResolvedHostColors | null {
  const set = resolveHostColorSet(host, mode)
  if (!set) return null
  const main = rgbaString(set.main.color, set.main.alpha)
  const middle = rgbaString(set.middle.color, set.middle.alpha)
  const light = rgbaString(set.light.color, set.light.alpha)
  if (!main || !middle || !light) return null
  return { main, middle, light }
}
```

- [ ] **Step 4: Run to verify pass**

Run: `cd spa && npx vitest run src/lib/color-space.test.ts src/lib/host-color.test.ts`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git commit --only spa/src/lib/color-space.ts spa/src/lib/color-space.test.ts spa/src/lib/host-color.ts spa/src/lib/host-color.test.ts -m "feat(spa): resolveHostColorSet/resolveHostColors with mode and layer inheritance"
```

---

### Task 4: Store actions `setHostColorLayer` / `clearHostColorMode`; `setHostColor` re-implemented on top

**Files:**
- Modify: `spa/src/stores/useHostStore.ts:75-76` (interface) and `:156-166` (implementation)
- Test: `spa/src/stores/useHostStore.test.ts` (replace the two `setHostColor` tests at ~lines 69–80, append new ones)

**Interfaces:**
- Consumes: Task 1 guards (`isHostColorMode`, `isHostColorLayer`, `clampHostAlpha`, `isValidHostColor`), `HostColorLayerName`.
- Produces:
  ```ts
  setHostColorLayer: (hostId: string, mode: HostColorMode, layer: HostColorLayerName, value: HostColorLayer | null) => void
  clearHostColorMode: (hostId: string, mode: HostColorMode) => void
  setHostColor: (hostId: string, color: string | null) => void   // unchanged signature; now writes colors.console.main
  ```

- [ ] **Step 1: Write the failing tests**

In `spa/src/stores/useHostStore.test.ts` replace the two existing `setHostColor` tests with:

```ts
  describe('host colors (spec 2026-09-18 §4.1)', () => {
    const id = () => useHostStore.getState().activeHostId!
    const host = () => useHostStore.getState().hosts[id()]

    it('setHostColor writes colors.console.main with alpha 100 and no legacy color key', () => {
      useHostStore.getState().setHostColor(id(), '#3b82f6')
      expect(host().colors).toEqual({ console: { main: { color: '#3b82f6', alpha: 100 } } })
      expect('color' in host()).toBe(false)
    })

    it('setHostColor keeps the existing console main alpha and other layers', () => {
      useHostStore.getState().setHostColorLayer(id(), 'console', 'main', { color: '#3b82f6', alpha: 80 })
      useHostStore.getState().setHostColorLayer(id(), 'console', 'light', { alpha: 30 })
      useHostStore.getState().setHostColor(id(), '#ef4444')
      expect(host().colors?.console).toEqual({ main: { color: '#ef4444', alpha: 80 }, light: { alpha: 30 } })
    })

    it('setHostColor(null) clears the console set only', () => {
      useHostStore.getState().setHostColorLayer(id(), 'console', 'main', { color: '#3b82f6', alpha: 100 })
      useHostStore.getState().setHostColorLayer(id(), 'terminal', 'main', { color: '#ef4444', alpha: 100 })
      useHostStore.getState().setHostColor(id(), null)
      expect(host().colors).toEqual({ terminal: { main: { color: '#ef4444', alpha: 100 } } })
    })

    it('any colors write deletes a legacy color key', () => {
      useHostStore.setState((s) => ({ hosts: { ...s.hosts, [id()]: { ...s.hosts[id()], color: '#22c55e' } } }))
      useHostStore.getState().setHostColorLayer(id(), 'terminal', 'main', { color: '#ef4444', alpha: 100 })
      expect('color' in host()).toBe(false)
      expect(host().colors?.terminal?.main).toEqual({ color: '#ef4444', alpha: 100 })
    })

    it('setHostColorLayer main creates the mode set; middle/light on a missing set are no-ops', () => {
      useHostStore.getState().setHostColorLayer(id(), 'terminal', 'middle', { alpha: 50 })
      expect(host().colors).toBeUndefined()
      useHostStore.getState().setHostColorLayer(id(), 'terminal', 'main', { color: '#ef4444', alpha: 100 })
      useHostStore.getState().setHostColorLayer(id(), 'terminal', 'middle', { alpha: 50 })
      expect(host().colors?.terminal).toEqual({ main: { color: '#ef4444', alpha: 100 }, middle: { alpha: 50 } })
    })

    it('setHostColorLayer clamps alpha and lowercases color', () => {
      useHostStore.getState().setHostColorLayer(id(), 'console', 'main', { color: '#3B82F6', alpha: 250.4 })
      expect(host().colors?.console?.main).toEqual({ color: '#3b82f6', alpha: 100 })
      useHostStore.getState().setHostColorLayer(id(), 'console', 'light', { color: '#000000', alpha: -3 })
      expect(host().colors?.console?.light).toEqual({ color: '#000000', alpha: 0 })
    })

    it('setHostColorLayer rejects a main without color, an invalid color, or an unknown mode', () => {
      useHostStore.getState().setHostColorLayer(id(), 'console', 'main', { alpha: 100 } as never)
      useHostStore.getState().setHostColorLayer(id(), 'console', 'main', { color: 'red', alpha: 100 })
      useHostStore.getState().setHostColorLayer(id(), 'shell' as never, 'main', { color: '#3b82f6', alpha: 100 })
      expect(host().colors).toBeUndefined()
    })

    it('setHostColorLayer(null) on middle/light removes the layer; on main clears the mode', () => {
      useHostStore.getState().setHostColorLayer(id(), 'console', 'main', { color: '#3b82f6', alpha: 100 })
      useHostStore.getState().setHostColorLayer(id(), 'console', 'light', { alpha: 5 })
      useHostStore.getState().setHostColorLayer(id(), 'console', 'light', null)
      expect(host().colors?.console).toEqual({ main: { color: '#3b82f6', alpha: 100 } })
      useHostStore.getState().setHostColorLayer(id(), 'console', 'main', null)
      expect(host().colors).toBeUndefined()
    })

    it('clearHostColorMode removes the set and drops colors when empty; unknown host is a no-op', () => {
      useHostStore.getState().setHostColorLayer(id(), 'console', 'main', { color: '#3b82f6', alpha: 100 })
      useHostStore.getState().clearHostColorMode('nope', 'console')
      expect(host().colors?.console).toBeDefined()
      useHostStore.getState().clearHostColorMode(id(), 'console')
      expect('colors' in host()).toBe(false)
    })
  })
```

- [ ] **Step 2: Run to verify failure**

Run: `cd spa && npx vitest run src/stores/useHostStore.test.ts`
Expected: FAIL (`setHostColorLayer` is not a function; `setHostColor` still writes `color`).

- [ ] **Step 3: Implement**

`spa/src/stores/useHostStore.ts` — interface (replace the `setHostColor` doc + line):

```ts
  /** Legacy entry point kept for the current color UI: writes `colors.console.main` (alpha preserved, default 100); `null` clears the console set. */
  setHostColor: (hostId: string, color: string | null) => void
  /**
   * Write one layer of one mode (spec §4.1). `main` with a valid color creates the
   * set when absent; `null` on `main` clears the mode. `middle` / `light` require an
   * existing set (no-op otherwise); `null` removes just that layer. Alpha is clamped,
   * color lowercased; invalid input / unknown host / unknown mode are no-ops.
   * Every write deletes the legacy `color` key.
   */
  setHostColorLayer: (hostId: string, mode: HostColorMode, layer: HostColorLayerName, value: HostColorLayer | null) => void
  /** Remove the whole set for a mode; drops `colors` when it becomes empty. */
  clearHostColorMode: (hostId: string, mode: HostColorMode) => void
```

Implementation (replace the `setHostColor` body; add the two new actions right after it). Also extend the value import from `'../lib/host-color'` with `clampHostAlpha, isHostColorMode, normalizeHostColor` and the type import with `HostColorLayer, HostColorLayerName`:

```ts
      setHostColor: (hostId, color) => {
        const { setHostColorLayer, clearHostColorMode, hosts } = get()
        if (color === null) {
          clearHostColorMode(hostId, 'console')
          return
        }
        const alpha = hosts[hostId]?.colors?.console?.main.alpha ?? HOST_COLOR_ALPHA_DEFAULTS.main
        setHostColorLayer(hostId, 'console', 'main', { color, alpha })
      },

      setHostColorLayer: (hostId, mode, layer, value) =>
        set((state) => {
          const host = state.hosts[hostId]
          if (!host || !isHostColorMode(mode)) return state
          const { color: _legacy, ...hostSansLegacy } = host
          const colors = { ...host.colors }
          const existing = colors[mode]

          if (value === null) {
            if (layer === 'main') delete colors[mode]
            else if (existing) {
              const { [layer]: _dropped, ...rest } = existing
              colors[mode] = rest as HostColorSet
            } else return state
          } else {
            if (typeof value !== 'object' || value === null) return state
            const alpha = clampHostAlpha(Number(value.alpha))
            const color = value.color === undefined ? undefined : normalizeHostColor(value.color)
            if (value.color !== undefined && color === null) return state
            if (layer === 'main') {
              if (!color) return state
              colors[mode] = { ...existing, main: { color, alpha } }
            } else {
              if (!existing) return state
              colors[mode] = { ...existing, [layer]: color ? { color, alpha } : { alpha } }
            }
          }

          const next: HostConfig = { ...hostSansLegacy }
          if (Object.keys(colors).length > 0) next.colors = colors
          return { hosts: { ...state.hosts, [hostId]: next } }
        }),

      clearHostColorMode: (hostId, mode) => get().setHostColorLayer(hostId, mode, 'main', null),
```

Import `HOST_COLOR_ALPHA_DEFAULTS` and `HostColorSet` as well. The store's `create` callback must expose `get` — check the existing signature `(set, get) =>`; it already uses `get()` in `getDaemonBase`.

- [ ] **Step 4: Run to verify pass + full store tests + typecheck**

Run: `cd spa && npx vitest run src/stores/useHostStore.test.ts src/stores/useHostStore.registerLocalHost.test.ts src/lib/sync/contributors/hosts.test.ts && npx tsc --noEmit -p tsconfig.app.json`
Expected: PASS. If `hosts.test.ts` asserted the legacy `color` round-trip, keep it — legacy `color` still passes sanitize and is still carried by the contributor.

- [ ] **Step 5: Commit**

```bash
git commit --only spa/src/stores/useHostStore.ts spa/src/stores/useHostStore.test.ts -m "feat(spa): setHostColorLayer/clearHostColorMode; setHostColor writes colors.console.main"
```

---

### Task 5: Readers switch to the resolver (no visual change)

**Files:**
- Modify: `spa/src/hooks/useTabHostBadge.ts`
- Modify: `spa/src/components/hosts/HostColorField.tsx:10-12`
- Test: `spa/src/hooks/useTabHostBadge.test.ts` (append), `spa/src/components/hosts/HostColorField.test.tsx` (append)

**Interfaces:**
- Consumes: `resolveHostColorSet(host, mode)` from Task 3.
- Produces: `useTabHostBadge` return type unchanged (`{ color, icon, iconWeight } | null`); `color` = resolved **console** main hex (P2 replaces this with per-mode rgba).

- [ ] **Step 1: Write the failing tests**

Append to `spa/src/hooks/useTabHostBadge.test.ts` — the file already defines `tmuxTab(hostId)`, `hostA` (`id: 'host-a'`) and `seedHosts(...hosts: HostConfig[])`:

```ts
  it('reads the console main color from colors, preferring it over legacy color', () => {
    seedHosts({ ...hostA, color: '#22c55e', colors: { console: { main: { color: '#3b82f6', alpha: 100 } } } })
    const { result } = renderHook(() => useTabHostBadge(tmuxTab('host-a')))
    expect(result.current?.color).toBe('#3b82f6')
  })

  it('still honours a legacy-only color', () => {
    seedHosts({ ...hostA, color: '#22c55e' })
    const { result } = renderHook(() => useTabHostBadge(tmuxTab('host-a')))
    expect(result.current?.color).toBe('#22c55e')
  })
```

Append to `spa/src/components/hosts/HostColorField.test.tsx`:

```ts
  it('marks the preset pressed from colors.console.main', () => {
    useHostStore.getState().setHostColorLayer(HOST_ID, 'console', 'main', { color: '#3b82f6', alpha: 70 })
    render(<HostColorField hostId={HOST_ID} />)
    expect(screen.getByRole('button', { name: '#3b82f6' })).toHaveAttribute('aria-pressed', 'true')
  })
```
(`HOST_ID = 'h1'` is the constant the file's `beforeEach` already seeds.)

- [ ] **Step 2: Run to verify failure**

Run: `cd spa && npx vitest run src/hooks/useTabHostBadge.test.ts src/components/hosts/HostColorField.test.tsx`
Expected: the first new test in each file FAILS (legacy `color` wins / nothing pressed).

- [ ] **Step 3: Implement**

`spa/src/hooks/useTabHostBadge.ts` — replace the `color` selector and the return:

```ts
  const colors = useHostStore((s) => (hostId ? s.hosts[hostId]?.colors : undefined))
  const legacyColor = useHostStore((s) => (hostId ? s.hosts[hostId]?.color : undefined))
  …
  if (!hostId) return null
  // P1 adapter: the badge still takes one hex; per-mode rgba arrives with P2.
  const resolved = resolveHostColorSet({ colors, color: legacyColor }, 'console')
  return {
    color: resolved?.main.color ?? null,
    icon: …unchanged…,
    iconWeight: …unchanged…,
  }
```
Import `resolveHostColorSet` from `'../lib/host-color'`; drop `isValidHostColor` if now unused. Update the hook's doc comment ("three primitive selectors" → "primitive selectors plus the `colors` object, whose identity only changes on a write").

`spa/src/components/hosts/HostColorField.tsx`:

```ts
  const colors = useHostStore((s) => s.hosts[hostId]?.colors)
  const legacy = useHostStore((s) => s.hosts[hostId]?.color)
  const setHostColor = useHostStore((s) => s.setHostColor)
  // Resolver already validates; an unresolvable host is "no color".
  const current = resolveHostColorSet({ colors, color: legacy }, 'console')?.main.color ?? ''
```
Replace the `isValidHostColor` import with `resolveHostColorSet`.

- [ ] **Step 4: Run to verify pass**

Run: `cd spa && npx vitest run src/hooks src/components/hosts/HostColorField.test.tsx src/components/SortableTab.test.tsx src/features/workspace && npx tsc --noEmit -p tsconfig.app.json`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git commit --only spa/src/hooks/useTabHostBadge.ts spa/src/hooks/useTabHostBadge.test.ts spa/src/components/hosts/HostColorField.tsx spa/src/components/hosts/HostColorField.test.tsx -m "refactor(spa): badge hook and color field read through resolveHostColorSet"
```

---

### Task 6: `stripAgentTitleMarker` (pure)

**Files:**
- Create: `spa/src/lib/agent-title-marker.ts`
- Create: `spa/src/lib/agent-title-marker.test.ts`

**Interfaces:**
- Produces:
  ```ts
  export const AGENT_TITLE_MARKERS: Readonly<Record<string, RegExp>>
  export function stripAgentTitleMarker(title: string, agentType: string | undefined): string
  ```

- [ ] **Step 1: Write the failing tests**

```ts
import { describe, it, expect } from 'vitest'
import { AGENT_TITLE_MARKERS, stripAgentTitleMarker } from './agent-title-marker'

describe('stripAgentTitleMarker', () => {
  it('removes the leading "✳ " Claude Code writes (measured 2026-09-18: U+2733 + space)', () => {
    expect(stripAgentTitleMarker('✳ Host color lab', 'cc')).toBe('Host color lab')
  })
  it('tolerates the emoji presentation selector and extra spaces', () => {
    expect(stripAgentTitleMarker('✳️  plan review', 'cc')).toBe('plan review')
  })
  it('removes only a leading marker', () => {
    expect(stripAgentTitleMarker('fix ✳ later', 'cc')).toBe('fix ✳ later')
  })
  it('returns an empty string when the title is only the marker', () => {
    expect(stripAgentTitleMarker('✳ ', 'cc')).toBe('')
  })
  it('leaves other agents and unknown types alone', () => {
    expect(stripAgentTitleMarker('✳ x', 'codex')).toBe('✳ x')
    expect(stripAgentTitleMarker('✳ x', undefined)).toBe('✳ x')
  })
  it('only cc has a marker today', () => {
    expect(Object.keys(AGENT_TITLE_MARKERS)).toEqual(['cc'])
  })
})
```

- [ ] **Step 2: Run to verify failure**

Run: `cd spa && npx vitest run src/lib/agent-title-marker.test.ts`
Expected: FAIL (module not found).

- [ ] **Step 3: Implement**

```ts
/**
 * Leading markers agents write into the tmux pane title, keyed by agentType
 * (`AGENT_NAMES` keys). Claude Code 2.1.276 sets `✳ <summary>` (U+2733 + U+0020,
 * measured 2026-09-18). Codex sets the cwd basename only (no marker) — a Codex
 * entry is added here once the user supplies a title sample (spec D9).
 */
export const AGENT_TITLE_MARKERS: Readonly<Record<string, RegExp>> = {
  cc: /^✳️?\s*/,
}

/** Strips one leading marker for the given agent; anything else passes through unchanged. */
export function stripAgentTitleMarker(title: string, agentType: string | undefined): string {
  if (!agentType) return title
  const re = AGENT_TITLE_MARKERS[agentType]
  return re ? title.replace(re, '') : title
}
```

- [ ] **Step 4: Run to verify pass**

Run: `cd spa && npx vitest run src/lib/agent-title-marker.test.ts`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git commit --only spa/src/lib/agent-title-marker.ts spa/src/lib/agent-title-marker.test.ts -m "feat(spa): stripAgentTitleMarker table for agent pane-title markers"
```

---

### Task 7: `stripAgentTitleMarker` setting → store, sync, `useTabDisplay`, Settings toggle

**Files:**
- Modify: `spa/src/stores/useUISettingsStore.ts` (interface near `dynamicTabName` ~line 168; defaults ~line 237)
- Modify: `spa/src/lib/sync/contributors/preferences.ts:12-37` (`DATA_FIELDS`)
- Modify: `spa/src/hooks/useTabDisplay.ts:72-73`
- Modify: `spa/src/components/settings/TerminalSection.tsx` (after the "Dynamic tab name" `SettingItem`, ~line 200)
- Modify: `spa/src/locales/en.json`, `spa/src/locales/zh-TW.json`
- Test: `spa/src/hooks/useTabDisplay.test.ts` (append in the "agent title override" describe), `spa/src/lib/sync/contributors/preferences.test.ts` (the field-list assertion ~line 241), `spa/src/stores/useUISettingsStore.test.ts` (defaults)

**Interfaces:**
- Consumes: `stripAgentTitleMarker` from Task 6.
- Produces: `UISettings.stripAgentTitleMarker: boolean` (default `true`), `setStripAgentTitleMarker(v: boolean)`; locale keys `settings.terminal.strip_agent_title_marker.label` / `.desc`.

- [ ] **Step 1: Write the failing tests**

`spa/src/hooks/useTabDisplay.test.ts`, inside `describe('useTabDisplay — agent title override', …)`:

```ts
  it('strips the cc marker from pane_title by default', () => {
    useSessionStore.setState({
      sessions: { h1: [{ code: 'sc1', name: 'base', pane_title: '✳ plan review' }] as never },
      activeHostId: null,
      activeCode: null,
    })
    useUISettingsStore.setState({ dynamicTabName: true })
    useAgentStore.setState({ agentTypes: { 'h1:sc1': 'cc' } })
    const { result } = renderHook(() => useTabDisplay(makeTab()))
    expect(result.current.displayTitle).toBe('plan review - base')
  })

  it('keeps the marker when stripAgentTitleMarker is off', () => {
    useSessionStore.setState({
      sessions: { h1: [{ code: 'sc1', name: 'base', pane_title: '✳ plan review' }] as never },
      activeHostId: null,
      activeCode: null,
    })
    useUISettingsStore.setState({ dynamicTabName: true, stripAgentTitleMarker: false })
    useAgentStore.setState({ agentTypes: { 'h1:sc1': 'cc' } })
    const { result } = renderHook(() => useTabDisplay(makeTab()))
    expect(result.current.displayTitle).toBe('✳ plan review - base')
  })

  it('falls back to the base label when the title is only the marker', () => {
    useSessionStore.setState({
      sessions: { h1: [{ code: 'sc1', name: 'base', pane_title: '✳ ' }] as never },
      activeHostId: null,
      activeCode: null,
    })
    useUISettingsStore.setState({ dynamicTabName: true })
    useAgentStore.setState({ agentTypes: { 'h1:sc1': 'cc' } })
    const { result } = renderHook(() => useTabDisplay(makeTab()))
    expect(result.current.displayTitle).toBe('base')
  })
```
Check the file's `beforeEach`: it must reset `stripAgentTitleMarker` (add `useUISettingsStore.setState({ dynamicTabName: false, stripAgentTitleMarker: true })` there if it only resets `dynamicTabName`).

`spa/src/lib/sync/contributors/preferences.test.ts`: add `'stripAgentTitleMarker'` to the expected field list (after `'showAgentTitleInStatusBar'`).

`spa/src/stores/useUISettingsStore.test.ts`: add

```ts
  it('stripAgentTitleMarker defaults to true', () => {
    expect(useUISettingsStore.getState().stripAgentTitleMarker).toBe(true)
  })
```

- [ ] **Step 2: Run to verify failure**

Run: `cd spa && npx vitest run src/hooks/useTabDisplay.test.ts src/lib/sync/contributors/preferences.test.ts src/stores/useUISettingsStore.test.ts`
Expected: the new tests FAIL.

- [ ] **Step 3: Implement**

`useUISettingsStore.ts` — interface, next to `dynamicTabName`:
```ts
  /** Strip the marker an agent writes at the start of its pane title (e.g. Claude Code's `✳`). */
  stripAgentTitleMarker: boolean
  setStripAgentTitleMarker: (v: boolean) => void
```
defaults, next to `dynamicTabName: false`:
```ts
      stripAgentTitleMarker: true,
      setStripAgentTitleMarker: (v) => set({ stripAgentTitleMarker: v }),
```

`preferences.ts` `DATA_FIELDS`: add `'stripAgentTitleMarker',` after `'showAgentTitleInStatusBar',`.

`useTabDisplay.ts`:
```ts
  const dynamicTabName = useUISettingsStore((s) => s.dynamicTabName)
  const stripMarker = useUISettingsStore((s) => s.stripAgentTitleMarker)
  …
  const rawPaneTitle = dynamicTabName && !isTerminated && !!agentType ? session?.pane_title : undefined
  const paneTitle = rawPaneTitle && stripMarker ? stripAgentTitleMarker(rawPaneTitle, agentType) : rawPaneTitle
  const displayTitle = paneTitle ? `${paneTitle} - ${baseLabel}` : baseLabel
```
Import `stripAgentTitleMarker` from `'../lib/agent-title-marker'`.

`TerminalSection.tsx`: read `stripAgentTitleMarker` / `setStripAgentTitleMarker` from the store like `dynamicTabName`, and after the "Dynamic tab name" `SettingItem` add:
```tsx
      <SettingItem
        label={t('settings.terminal.strip_agent_title_marker.label')}
        description={t('settings.terminal.strip_agent_title_marker.desc')}
      >
        <ToggleSwitch
          label={t('settings.terminal.strip_agent_title_marker.label')}
          checked={stripAgentTitleMarker}
          onChange={setStripAgentTitleMarker}
        />
      </SettingItem>
```
(Mirror the exact prop shape of the existing Dynamic-tab-name `SettingItem`.)

Locales — `en.json` next to the `settings.terminal.dynamic_tab_name.*` keys:
```json
  "settings.terminal.strip_agent_title_marker.label": "Strip agent title marker",
  "settings.terminal.strip_agent_title_marker.desc": "Remove the marker an agent prefixes to its pane title (Claude Code's ✳)",
```
`zh-TW.json`:
```json
  "settings.terminal.strip_agent_title_marker.label": "移除 agent 標題符號",
  "settings.terminal.strip_agent_title_marker.desc": "移除 agent 寫在 pane 標題開頭的符號（Claude Code 的 ✳）",
```

- [ ] **Step 4: Run to verify pass + full suite + lint**

Run: `cd spa && npx vitest run && pnpm run lint && npx tsc --noEmit -p tsconfig.app.json`
Expected: all green (≈450 files). If `locale-completeness.test.ts` or a TerminalSection snapshot/test lists settings, update it.

- [ ] **Step 5: Commit**

```bash
git commit --only spa/src/stores/useUISettingsStore.ts spa/src/stores/useUISettingsStore.test.ts spa/src/lib/sync/contributors/preferences.ts spa/src/lib/sync/contributors/preferences.test.ts spa/src/hooks/useTabDisplay.ts spa/src/hooks/useTabDisplay.test.ts spa/src/components/settings/TerminalSection.tsx spa/src/locales/en.json spa/src/locales/zh-TW.json -m "feat(spa): stripAgentTitleMarker setting removes Claude Code's ✳ from tab titles"
```

---

## Done criteria (P1)

- `cd spa && npx vitest run && pnpm run lint && pnpm run build` green.
- `rg resolveTabHostColor spa/src` → nothing.
- Live at `:5174`: badges look exactly as before; Claude Code tabs no longer start with `✳`; Settings → Terminal has the new toggle and turning it off brings `✳` back.
- Diff budget: well under 800 lines / 20 files (expected ≈ 12 files).
