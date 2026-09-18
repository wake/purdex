# Host Color Modes — P3 (per-mode tri-color picker UI) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give the Host settings page a Color row where the user picks a mode (Console / Terminal / Execution), sees the three layers (Main / Middle / Light) as swatches, and edits any layer with hue / saturation / lightness / alpha sliders, presets and a hex input — every drag writing the store immediately so tab badges update live.

**Architecture:** `lib/color-space.ts` grows `hexToHsl` / `hslToHex` (pure). `HostColorLayerEditor` is a pure, prop-driven editor (value in, `onChange` out) rendered **inline below the swatches** — the same expand-in-place pattern `HostIconField` uses (no portal, no positioning). `HostColorField` owns mode + open-layer state, reads the raw set (`hosts[id].colors?.[mode]`) for inherit flags and the resolved set (`resolveHostColorSet`) for swatch colors, and writes through `setHostColorLayer` / `clearHostColorMode` from P1.

**Tech Stack:** React 19 / Zustand 5 / Vitest + Testing Library / Tailwind 4 / TypeScript strict. Tests `cd spa && npx vitest run <path>`; lint `pnpm run lint`; typecheck `npx tsc --noEmit -p tsconfig.app.json`.

**Spec:** `docs/specs/2026-09-18-host-color-modes-spec.md` §6 (with the amendment below), §8 items 2–3. P1/P2 are merged (alpha.381 / alpha.383).

## Global Constraints

- Worktree `/Users/wake/Workspace/wake/purdex/.claude/worktrees/host-color-modes`, branch `worktree-host-color-modes-p3`; every command `cd`s there.
- TDD; one commit per task via `git commit --only <files>`.
- **Spec amendment (D8):** the editor is an inline panel under the swatches (like `HostIconField`'s icon picker), not a floating popover — same sliders, presets, hex and live writes. `@floating-ui/react` is not used (it is not used anywhere in the repo today either).
- Store contract (P1, do not change): `setHostColorLayer(hostId, mode, 'main' | 'middle' | 'light', { color?: '#rrggbb', alpha: 0–100 } | null)`; `color` must already be valid lowercase `#rrggbb` (use `normalizeHostColor` on user text first); `middle` / `light` without `color` = inherit main; `null` on `middle`/`light` = back to inherit + default alpha; `clearHostColorMode(hostId, mode)`.
- Resolver (P1): `resolveHostColorSet({ colors, color }, mode) → { main:{color,alpha}, middle:{…}, light:{…} } | null` (hex + alpha, inheritance applied).
- Defaults: `HOST_COLOR_ALPHA_DEFAULTS = { main: 100, middle: 60, light: 22 }`.
- Keep the existing `HostColorField` test ids where they still make sense: `host-color-hex`, `host-color-clear`. Add `host-color-mode`, `host-color-layer-<main|middle|light>`, `host-color-editor`, `host-color-inherit`, `host-color-range-<h|s|l|a>`.
- Locale keys in both `en.json` and `zh-TW.json`.
- Sliders are native `<input type="range">`; the track gradient is an inline `background` on the input (`linear-gradient(to right, …)`), computed from the current HSL so each axis previews its result.
- Diff budget ≤ 800 lines / 20 files.

---

### Task 1: `hexToHsl` / `hslToHex` (`lib/color-space.ts`)

**Files:**
- Modify: `spa/src/lib/color-space.ts`
- Test: `spa/src/lib/color-space.test.ts`

**Interfaces:**
- Produces:
  ```ts
  export interface Hsl { h: number; s: number; l: number }   // h 0–360, s/l 0–100, floats (not rounded)
  export function hexToHsl(hex: string): Hsl | null            // '#rrggbb' only
  export function hslToHex(hsl: Hsl): string                   // lowercase '#rrggbb'; inputs clamped (h wraps mod 360)
  ```

- [ ] **Step 1: Write the failing tests**

Append to `spa/src/lib/color-space.test.ts` (import `HOST_COLOR_PRESETS` from `./host-color`):

```ts
describe('hexToHsl / hslToHex', () => {
  it('converts primaries and greys', () => {
    expect(hexToHsl('#ff0000')).toEqual({ h: 0, s: 100, l: 50 })
    expect(hexToHsl('#00ff00')).toEqual({ h: 120, s: 100, l: 50 })
    expect(hexToHsl('#0000ff')).toEqual({ h: 240, s: 100, l: 50 })
    expect(hexToHsl('#000000')).toEqual({ h: 0, s: 0, l: 0 })
    expect(hexToHsl('#ffffff')).toEqual({ h: 0, s: 0, l: 100 })
    expect(hexToHsl('#808080')?.s).toBe(0)
  })

  it('rejects anything that is not #rrggbb', () => {
    expect(hexToHsl('3b82f6')).toBeNull()
    expect(hexToHsl('#abc')).toBeNull()
  })

  it.each(HOST_COLOR_PRESETS)('round-trips preset %s exactly', (hex) => {
    expect(hslToHex(hexToHsl(hex)!)).toBe(hex)
  })

  it('hslToHex clamps s/l, wraps h, and lowercases', () => {
    expect(hslToHex({ h: 360, s: 100, l: 50 })).toBe('#ff0000')
    expect(hslToHex({ h: -120, s: 100, l: 50 })).toBe('#0000ff')
    expect(hslToHex({ h: 0, s: 150, l: -5 })).toBe('#000000')
    expect(hslToHex({ h: 0, s: 0, l: 200 })).toBe('#ffffff')
  })

  it('rgbaString(hslToHex(x)) stays consistent with hexToRgb', () => {
    const hex = hslToHex({ h: 217.2, s: 91.2, l: 59.8 })
    expect(hexToRgb(hex)).toEqual({ r: 59, g: 130, b: 246 })
  })
})
```

- [ ] **Step 2: Run to verify failure**

Run: `cd spa && npx vitest run src/lib/color-space.test.ts`
Expected: FAIL (not exported).

- [ ] **Step 3: Implement**

Append to `spa/src/lib/color-space.ts`:

```ts
export interface Hsl {
  /** 0–360 */
  h: number
  /** 0–100 */
  s: number
  /** 0–100 */
  l: number
}

const clamp01 = (n: number) => Math.min(1, Math.max(0, n))

/** Floats, not rounded: keeping precision makes hex → hsl → hex exact for every preset. */
export function hexToHsl(hex: string): Hsl | null {
  const rgb = hexToRgb(hex)
  if (!rgb) return null
  const r = rgb.r / 255
  const g = rgb.g / 255
  const b = rgb.b / 255
  const max = Math.max(r, g, b)
  const min = Math.min(r, g, b)
  const l = (max + min) / 2
  if (max === min) return { h: 0, s: 0, l: l * 100 }
  const d = max - min
  const s = l > 0.5 ? d / (2 - max - min) : d / (max + min)
  let h: number
  if (max === r) h = (g - b) / d + (g < b ? 6 : 0)
  else if (max === g) h = (b - r) / d + 2
  else h = (r - g) / d + 4
  return { h: h * 60, s: s * 100, l: l * 100 }
}

function hueToRgb(p: number, q: number, t: number): number {
  if (t < 0) t += 1
  if (t > 1) t -= 1
  if (t < 1 / 6) return p + (q - p) * 6 * t
  if (t < 1 / 2) return q
  if (t < 2 / 3) return p + (q - p) * (2 / 3 - t) * 6
  return p
}

/** Lowercase `#rrggbb`. `h` wraps modulo 360; `s` / `l` clamp to 0–100. */
export function hslToHex({ h, s, l }: Hsl): string {
  const hh = (((h % 360) + 360) % 360) / 360
  const ss = clamp01(s / 100)
  const ll = clamp01(l / 100)
  let r: number, g: number, b: number
  if (ss === 0) {
    r = g = b = ll
  } else {
    const q = ll < 0.5 ? ll * (1 + ss) : ll + ss - ll * ss
    const p = 2 * ll - q
    r = hueToRgb(p, q, hh + 1 / 3)
    g = hueToRgb(p, q, hh)
    b = hueToRgb(p, q, hh - 1 / 3)
  }
  const toHex = (v: number) => Math.round(v * 255).toString(16).padStart(2, '0')
  return `#${toHex(r)}${toHex(g)}${toHex(b)}`
}
```

- [ ] **Step 4: Run to verify pass**

Run: `cd spa && npx vitest run src/lib/color-space.test.ts`
Expected: PASS (if a preset round-trip is off by one in a channel, the bug is in rounding — do not weaken the test).

- [ ] **Step 5: Commit**

```bash
git commit --only spa/src/lib/color-space.ts spa/src/lib/color-space.test.ts -m "feat(spa): hexToHsl/hslToHex for the host color editor"
```

---

### Task 2: `HostColorLayerEditor` — pure editor panel

**Files:**
- Create: `spa/src/components/hosts/HostColorLayerEditor.tsx`
- Create: `spa/src/components/hosts/HostColorLayerEditor.test.tsx`
- Modify: `spa/src/locales/en.json`, `spa/src/locales/zh-TW.json`

**Interfaces:**
- Consumes: Task 1, `HOST_COLOR_PRESETS`, `normalizeHostColor`, `rgbaString`.
- Produces:
  ```ts
  export interface HostColorLayerEditorProps {
    layer: 'main' | 'middle' | 'light'
    /** Effective color of this layer (hex), i.e. own color or the inherited main. */
    color: string
    alpha: number
    /** middle/light only: true when the layer has no own color. Always false for main. */
    inherited: boolean
    /** Live write; `color` undefined = keep inheriting (only legal for middle/light). */
    onChange: (next: { color?: string; alpha: number }) => void
    onClose: () => void
  }
  export function HostColorLayerEditor(props: HostColorLayerEditorProps): JSX.Element
  ```
  DOM: root `data-testid="host-color-editor"` with `role="group"` and `aria-label` = layer name; for `main` a preset row of 8 buttons (`aria-label` = hex, `aria-pressed`); for middle/light a `ToggleSwitch` `data-testid="host-color-inherit"`; ranges `host-color-range-h|s|l` (hidden while inherited), `host-color-range-a` (always); hex input `host-color-hex` (hidden while inherited); a preview swatch `host-color-preview` whose `background` is `rgbaString(color, alpha)`; a close button `host-color-editor-close`.

Locale keys (add to both files, next to `hosts.color.*`):
```json
  "hosts.color.layer.main": "Main",
  "hosts.color.layer.middle": "Middle",
  "hosts.color.layer.light": "Light",
  "hosts.color.layer.main.hint": "Active tab and hover",
  "hosts.color.layer.middle.hint": "Inactive tabs",
  "hosts.color.layer.light.hint": "Badge background",
  "hosts.color.inherit_main": "Inherit main color",
  "hosts.color.hue": "Hue",
  "hosts.color.saturation": "Saturation",
  "hosts.color.lightness": "Lightness",
  "hosts.color.alpha": "Opacity",
  "hosts.color.close": "Done",
```
zh-TW: 主色 / 中色 / 淡色 / 作用中分頁與 hover / 非作用中分頁 / 色塊底色 / 繼承主色 / 色相 / 飽和度 / 明度 / 透明度 / 完成.

- [ ] **Step 1: Write the failing tests**

`spa/src/components/hosts/HostColorLayerEditor.test.tsx`:

```tsx
import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { HostColorLayerEditor } from './HostColorLayerEditor'
import { HOST_COLOR_PRESETS } from '../../lib/host-color'

const base = { layer: 'main' as const, color: '#3b82f6', alpha: 100, inherited: false, onClose: () => {} }

describe('HostColorLayerEditor — main', () => {
  it('shows presets, H/S/L/A ranges, hex and a preview', () => {
    render(<HostColorLayerEditor {...base} onChange={() => {}} />)
    for (const hex of HOST_COLOR_PRESETS) expect(screen.getByRole('button', { name: hex })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '#3b82f6' })).toHaveAttribute('aria-pressed', 'true')
    for (const id of ['h', 's', 'l', 'a']) expect(screen.getByTestId(`host-color-range-${id}`)).toBeInTheDocument()
    expect((screen.getByTestId('host-color-hex') as HTMLInputElement).value).toBe('#3b82f6')
    expect(screen.getByTestId('host-color-preview').style.background).toBe('rgba(59, 130, 246, 1)')
    expect(screen.queryByTestId('host-color-inherit')).toBeNull()
  })

  it('preset click writes that color with the current alpha', () => {
    const onChange = vi.fn()
    render(<HostColorLayerEditor {...base} alpha={80} onChange={onChange} />)
    fireEvent.click(screen.getByRole('button', { name: HOST_COLOR_PRESETS[2] }))
    expect(onChange).toHaveBeenCalledWith({ color: HOST_COLOR_PRESETS[2], alpha: 80 })
  })

  it('dragging hue writes a new hex on every input event, keeping s/l/alpha', () => {
    const onChange = vi.fn()
    render(<HostColorLayerEditor {...base} color="#ff0000" onChange={onChange} />)
    fireEvent.input(screen.getByTestId('host-color-range-h'), { target: { value: '120' } })
    expect(onChange).toHaveBeenLastCalledWith({ color: '#00ff00', alpha: 100 })
  })

  it('dragging alpha writes only alpha', () => {
    const onChange = vi.fn()
    render(<HostColorLayerEditor {...base} onChange={onChange} />)
    fireEvent.input(screen.getByTestId('host-color-range-a'), { target: { value: '35' } })
    expect(onChange).toHaveBeenLastCalledWith({ color: '#3b82f6', alpha: 35 })
  })

  it('hex input commits a normalized value on Enter and blur; invalid shows an alert and does not write', () => {
    const onChange = vi.fn()
    render(<HostColorLayerEditor {...base} onChange={onChange} />)
    const hex = screen.getByTestId('host-color-hex') as HTMLInputElement
    fireEvent.change(hex, { target: { value: 'ABCDEF' } })
    fireEvent.keyDown(hex, { key: 'Enter' })
    expect(onChange).toHaveBeenLastCalledWith({ color: '#abcdef', alpha: 100 })
    fireEvent.change(hex, { target: { value: 'red' } })
    fireEvent.blur(hex)
    expect(screen.getByRole('alert')).toBeInTheDocument()
    expect(onChange).toHaveBeenCalledTimes(1)
  })

  it('close button calls onClose', () => {
    const onClose = vi.fn()
    render(<HostColorLayerEditor {...base} onChange={() => {}} onClose={onClose} />)
    fireEvent.click(screen.getByTestId('host-color-editor-close'))
    expect(onClose).toHaveBeenCalled()
  })
})

describe('HostColorLayerEditor — middle/light', () => {
  it('inherited: only the inherit switch (on) and the alpha range are shown', () => {
    render(<HostColorLayerEditor {...base} layer="middle" alpha={60} inherited onChange={() => {}} />)
    expect(screen.getByTestId('host-color-inherit')).toHaveAttribute('aria-checked', 'true')
    expect(screen.getByTestId('host-color-range-a')).toBeInTheDocument()
    expect(screen.queryByTestId('host-color-range-h')).toBeNull()
    expect(screen.queryByTestId('host-color-hex')).toBeNull()
    expect(screen.queryByRole('button', { name: HOST_COLOR_PRESETS[0] })).toBeNull()
  })

  it('inherited alpha drag writes alpha without a color', () => {
    const onChange = vi.fn()
    render(<HostColorLayerEditor {...base} layer="light" alpha={22} inherited onChange={onChange} />)
    fireEvent.input(screen.getByTestId('host-color-range-a'), { target: { value: '40' } })
    expect(onChange).toHaveBeenLastCalledWith({ alpha: 40 })
  })

  it('turning inherit off writes the inherited color explicitly; turning it on drops the color', () => {
    const onChange = vi.fn()
    const { rerender } = render(<HostColorLayerEditor {...base} layer="middle" alpha={60} inherited onChange={onChange} />)
    fireEvent.click(screen.getByTestId('host-color-inherit'))
    expect(onChange).toHaveBeenLastCalledWith({ color: '#3b82f6', alpha: 60 })
    rerender(<HostColorLayerEditor {...base} layer="middle" alpha={60} inherited={false} onChange={onChange} />)
    expect(screen.getByTestId('host-color-range-h')).toBeInTheDocument()
    fireEvent.click(screen.getByTestId('host-color-inherit'))
    expect(onChange).toHaveBeenLastCalledWith({ alpha: 60 })
  })
})
```

- [ ] **Step 2: Run to verify failure**

Run: `cd spa && npx vitest run src/components/hosts/HostColorLayerEditor.test.tsx`
Expected: FAIL (module missing).

- [ ] **Step 3: Implement**

`spa/src/components/hosts/HostColorLayerEditor.tsx`:

```tsx
import { useState } from 'react'
import { useI18nStore } from '../../stores/useI18nStore'
import { HOST_COLOR_PRESETS, normalizeHostColor, type HostColorLayerName } from '../../lib/host-color'
import { hexToHsl, hslToHex, rgbaString, type Hsl } from '../../lib/color-space'
import { ToggleSwitch } from '../settings/ToggleSwitch'

export interface HostColorLayerEditorProps {
  layer: HostColorLayerName
  /** Effective hex of this layer (own color, or the inherited main). */
  color: string
  alpha: number
  /** middle/light only: the layer has no own color. Always false for main. */
  inherited: boolean
  /** Live write. `color` omitted = keep inheriting (middle/light only). */
  onChange: (next: { color?: string; alpha: number }) => void
  onClose: () => void
}

const RANGE_CLASS = 'w-full h-2 rounded appearance-none cursor-pointer bg-transparent'

function track(stops: string[]): string {
  return `linear-gradient(to right, ${stops.join(', ')})`
}

/**
 * Inline editor for one color layer (spec §6.3, D8 as amended: inline panel, not a
 * popover). Every slider writes through `onChange` on `input`, so the badge on
 * every tab row follows the drag. The HSL floats come from the *prop* color on
 * each render — no local color state — so store round-trips cannot drift.
 */
export function HostColorLayerEditor({ layer, color, alpha, inherited, onChange, onClose }: HostColorLayerEditorProps) {
  const t = useI18nStore((s) => s.t)
  const hsl: Hsl = hexToHsl(color) ?? { h: 0, s: 0, l: 50 }
  const [draft, setDraft] = useState(color)
  const [invalid, setInvalid] = useState(false)
  const [synced, setSynced] = useState(color)
  if (synced !== color) {
    setSynced(color)
    setDraft(color)
    setInvalid(false)
  }

  const write = (next: Partial<Hsl>, nextAlpha = alpha) => {
    onChange({ color: hslToHex({ ...hsl, ...next }), alpha: nextAlpha })
  }
  const writeAlpha = (nextAlpha: number) => {
    if (inherited) onChange({ alpha: nextAlpha })
    else onChange({ color, alpha: nextAlpha })
  }
  const commitHex = () => {
    if (draft.trim() === '') return
    const normalized = normalizeHostColor(draft)
    if (!normalized) {
      setInvalid(true)
      return
    }
    setInvalid(false)
    setDraft(normalized)
    onChange({ color: normalized, alpha })
  }

  const ranges: { id: 'h' | 's' | 'l'; labelKey: string; max: number; value: number; stops: string[] }[] = [
    { id: 'h', labelKey: 'hosts.color.hue', max: 360, value: hsl.h,
      stops: [0, 60, 120, 180, 240, 300, 360].map((h) => hslToHex({ h, s: hsl.s, l: hsl.l })) },
    { id: 's', labelKey: 'hosts.color.saturation', max: 100, value: hsl.s,
      stops: [hslToHex({ ...hsl, s: 0 }), hslToHex({ ...hsl, s: 100 })] },
    { id: 'l', labelKey: 'hosts.color.lightness', max: 100, value: hsl.l,
      stops: [hslToHex({ ...hsl, l: 0 }), hslToHex({ ...hsl, l: 50 }), hslToHex({ ...hsl, l: 100 })] },
  ]

  return (
    <div
      data-testid="host-color-editor"
      role="group"
      aria-label={t(`hosts.color.layer.${layer}`)}
      className="border border-border-default rounded-lg p-3 bg-surface-secondary space-y-3 max-w-sm"
    >
      <div className="flex items-center justify-between gap-2">
        <span className="text-xs text-text-secondary">
          {t(`hosts.color.layer.${layer}`)} · {t(`hosts.color.layer.${layer}.hint`)}
        </span>
        <span
          data-testid="host-color-preview"
          aria-hidden="true"
          className="w-6 h-6 rounded border border-border-default"
          style={{ background: rgbaString(color, alpha) ?? undefined }}
        />
      </div>

      {layer !== 'main' && (
        <div className="flex items-center justify-between">
          <span className="text-xs text-text-secondary">{t('hosts.color.inherit_main')}</span>
          <ToggleSwitch
            testId="host-color-inherit"
            label={t('hosts.color.inherit_main')}
            checked={inherited}
            onChange={(on) => (on ? onChange({ alpha }) : onChange({ color, alpha }))}
          />
        </div>
      )}

      {layer === 'main' && (
        <div className="flex flex-wrap items-center gap-1.5">
          {HOST_COLOR_PRESETS.map((hex) => (
            <button
              key={hex}
              type="button"
              aria-label={hex}
              aria-pressed={color === hex}
              onClick={() => onChange({ color: hex, alpha })}
              className={`w-[18px] h-[18px] rounded cursor-pointer border border-border-default ${
                color === hex ? 'ring-2 ring-offset-1 ring-offset-surface-primary ring-text-primary' : ''
              }`}
              style={{ background: hex }}
            />
          ))}
        </div>
      )}

      {!inherited &&
        ranges.map((r) => (
          <label key={r.id} className="block space-y-1">
            <span className="text-[11px] text-text-muted">{t(r.labelKey)}</span>
            <input
              type="range"
              data-testid={`host-color-range-${r.id}`}
              aria-label={t(r.labelKey)}
              min={0}
              max={r.max}
              step={1}
              value={Math.round(r.value)}
              onInput={(e) => write({ [r.id]: Number((e.target as HTMLInputElement).value) })}
              onChange={() => {}}
              className={RANGE_CLASS}
              style={{ background: track(r.stops) }}
            />
          </label>
        ))}

      <label className="block space-y-1">
        <span className="text-[11px] text-text-muted">{t('hosts.color.alpha')} · {alpha}%</span>
        <input
          type="range"
          data-testid="host-color-range-a"
          aria-label={t('hosts.color.alpha')}
          min={0}
          max={100}
          step={1}
          value={alpha}
          onInput={(e) => writeAlpha(Number((e.target as HTMLInputElement).value))}
          onChange={() => {}}
          className={RANGE_CLASS}
          style={{ background: track([rgbaString(color, 0) ?? color, color]) }}
        />
      </label>

      {!inherited && (
        <div className="space-y-1">
          <input
            type="text"
            aria-label={t('hosts.color.custom_aria')}
            aria-invalid={invalid}
            data-testid="host-color-hex"
            spellCheck={false}
            value={draft}
            onChange={(e) => setDraft(e.target.value)}
            onBlur={commitHex}
            onKeyDown={(e) => {
              if (e.key !== 'Enter' || e.nativeEvent.isComposing) return
              commitHex()
            }}
            className="bg-surface-secondary border border-border-default rounded px-2 py-0.5 text-xs text-text-primary font-mono w-24"
          />
          {invalid && <span role="alert" className="block text-xs text-red-400">{t('hosts.color.invalid')}</span>}
        </div>
      )}

      <div className="flex justify-end">
        <button
          type="button"
          data-testid="host-color-editor-close"
          onClick={onClose}
          className="px-2 py-1 rounded text-xs bg-surface-secondary border border-border-default text-text-secondary hover:text-text-primary cursor-pointer"
        >
          {t('hosts.color.close')}
        </button>
      </div>
    </div>
  )
}
```

`ToggleSwitch` has no test-id prop today: add an optional `testId?: string` prop forwarded as `data-testid` on its `<button role="switch">` (`spa/src/components/settings/ToggleSwitch.tsx`, two lines; include it in this task's commit). The tests query `host-color-inherit` on the switch itself.

`fireEvent.input` on a range in jsdom triggers React's `onInput`; keep the no-op `onChange` so React does not warn about a controlled input without a handler.

- [ ] **Step 4: Run to verify pass**

Run: `cd spa && npx vitest run src/components/hosts/HostColorLayerEditor.test.tsx src/locales && npx tsc --noEmit -p tsconfig.app.json`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git commit --only spa/src/components/hosts/HostColorLayerEditor.tsx spa/src/components/hosts/HostColorLayerEditor.test.tsx spa/src/components/settings/ToggleSwitch.tsx spa/src/locales/en.json spa/src/locales/zh-TW.json -m "feat(spa): HostColorLayerEditor with H/S/L/alpha sliders, presets, hex and inherit toggle"
```

---

### Task 3: `HostColorField` — mode switch, three layer swatches, inline editor

**Files:**
- Modify (rewrite): `spa/src/components/hosts/HostColorField.tsx`
- Modify (rewrite): `spa/src/components/hosts/HostColorField.test.tsx`
- Modify: `spa/src/locales/en.json`, `spa/src/locales/zh-TW.json`
- Test: `spa/src/components/hosts/OverviewSection.test.tsx` (only if it queries the old preset row directly — check with `rg -n 'host-color|aria-pressed' spa/src/components/hosts/OverviewSection.test.tsx`)

**Interfaces:**
- Consumes: Task 2 editor; store `setHostColorLayer` / `clearHostColorMode`; `resolveHostColorSet`; `HOST_COLOR_MODES`, `HOST_COLOR_ALPHA_DEFAULTS`.
- Produces: `HostColorField({ hostId })` unchanged signature. DOM: `host-color-mode` (SegmentControl wrapper), `host-color-layer-main|middle|light` buttons (`aria-pressed` = editor open for that layer; `data-inherits-console="true"` on all three when the mode has no own set), `host-color-clear`, and the Task 2 editor when a layer is open.

Locale keys (both files):
```json
  "hosts.color.mode.console": "Console",
  "hosts.color.mode.terminal": "Terminal",
  "hosts.color.mode.execution": "Execution",
  "hosts.color.inherits_console": "Inherits Console",
  "hosts.color.none": "No color",
  "hosts.color.inherit_short": "inherit",
  "hosts.color.clear_mode": "Clear this mode",
```
zh-TW: Console / Terminal / Execution（保留英文，UI 其他處也用英文 mode 名）/ 繼承 Console / 無顏色 / 繼承 / 清除此 mode.

- [ ] **Step 1: Write the failing tests**

Rewrite `spa/src/components/hosts/HostColorField.test.tsx` (keep `HOST_ID`, `host()` and the `beforeEach` seed):

```tsx
import { describe, it, expect, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { HostColorField } from './HostColorField'
import { useHostStore } from '../../stores/useHostStore'
import { HOST_COLOR_PRESETS } from '../../lib/host-color'

const HOST_ID = 'h1'
const host = () => useHostStore.getState().hosts[HOST_ID]
const layerBtn = (l: 'main' | 'middle' | 'light') => screen.getByTestId(`host-color-layer-${l}`)
const modeBtn = (name: string) => screen.getByRole('button', { name })
const BLUE = { color: '#3b82f6', alpha: 100 }

beforeEach(() => {
  useHostStore.setState({
    hosts: { [HOST_ID]: { id: HOST_ID, name: 'H', ip: '1.2.3.4', port: 7860, order: 0 } },
    hostOrder: [HOST_ID],
    runtime: {},
  })
})

describe('HostColorField — layout', () => {
  it('shows the mode switch on Console and three layer swatches, no editor', () => {
    render(<HostColorField hostId={HOST_ID} />)
    expect(screen.getByTestId('host-color-mode')).toBeInTheDocument()
    for (const l of ['main', 'middle', 'light'] as const) expect(layerBtn(l)).toHaveAttribute('aria-pressed', 'false')
    expect(screen.queryByTestId('host-color-editor')).toBeNull()
  })

  it('uncolored host: swatches read "No color" and clicking Main opens the editor with the first preset pre-filled but nothing written', () => {
    render(<HostColorField hostId={HOST_ID} />)
    expect(layerBtn('main').textContent).toContain('No color')
    fireEvent.click(layerBtn('main'))
    expect(screen.getByTestId('host-color-editor')).toBeInTheDocument()
    expect(host().colors).toBeUndefined()
    fireEvent.click(screen.getByRole('button', { name: HOST_COLOR_PRESETS[3] }))
    expect(host().colors?.console?.main).toEqual({ color: HOST_COLOR_PRESETS[3], alpha: 100 })
  })

  it('legacy color shows as the console main swatch', () => {
    useHostStore.setState((s) => ({ hosts: { ...s.hosts, [HOST_ID]: { ...s.hosts[HOST_ID], color: '#22c55e' } } }))
    render(<HostColorField hostId={HOST_ID} />)
    expect(layerBtn('main').textContent).toContain('#22c55e')
    expect(layerBtn('middle').textContent).toContain('inherit')
    expect(layerBtn('middle').textContent).toContain('60%')
  })
})

describe('HostColorField — editing', () => {
  beforeEach(() => useHostStore.getState().setHostColorLayer(HOST_ID, 'console', 'main', BLUE))

  it('opening Main and dragging alpha writes console.main live', () => {
    render(<HostColorField hostId={HOST_ID} />)
    fireEvent.click(layerBtn('main'))
    fireEvent.input(screen.getByTestId('host-color-range-a'), { target: { value: '70' } })
    expect(host().colors?.console?.main).toEqual({ color: '#3b82f6', alpha: 70 })
  })

  it('Middle starts inherited; alpha drag writes { alpha } only; inherit off writes the color', () => {
    render(<HostColorField hostId={HOST_ID} />)
    fireEvent.click(layerBtn('middle'))
    expect(screen.getByTestId('host-color-inherit')).toHaveAttribute('aria-checked', 'true')
    fireEvent.input(screen.getByTestId('host-color-range-a'), { target: { value: '45' } })
    expect(host().colors?.console?.middle).toEqual({ alpha: 45 })
    fireEvent.click(screen.getByTestId('host-color-inherit'))
    expect(host().colors?.console?.middle).toEqual({ color: '#3b82f6', alpha: 45 })
    fireEvent.click(screen.getByTestId('host-color-inherit'))
    expect(host().colors?.console?.middle).toEqual({ alpha: 45 })
  })

  it('clicking the open layer again closes the editor; Done closes it too', () => {
    render(<HostColorField hostId={HOST_ID} />)
    fireEvent.click(layerBtn('light'))
    expect(layerBtn('light')).toHaveAttribute('aria-pressed', 'true')
    fireEvent.click(layerBtn('light'))
    expect(screen.queryByTestId('host-color-editor')).toBeNull()
    fireEvent.click(layerBtn('light'))
    fireEvent.click(screen.getByTestId('host-color-editor-close'))
    expect(screen.queryByTestId('host-color-editor')).toBeNull()
  })

  it('clear removes the console set and closes the editor', () => {
    render(<HostColorField hostId={HOST_ID} />)
    fireEvent.click(layerBtn('main'))
    fireEvent.click(screen.getByTestId('host-color-clear'))
    expect(host().colors).toBeUndefined()
    expect(screen.queryByTestId('host-color-editor')).toBeNull()
  })
})

describe('HostColorField — modes', () => {
  beforeEach(() => useHostStore.getState().setHostColorLayer(HOST_ID, 'console', 'main', BLUE))

  it('Terminal without its own set shows dimmed swatches that say it inherits Console', () => {
    render(<HostColorField hostId={HOST_ID} />)
    fireEvent.click(modeBtn('Terminal'))
    for (const l of ['main', 'middle', 'light'] as const) {
      expect(layerBtn(l)).toHaveAttribute('data-inherits-console', 'true')
    }
    expect(screen.getByText('Inherits Console')).toBeInTheDocument()
  })

  it('clicking a swatch on an inheriting mode copies console main into that mode and opens the editor', () => {
    render(<HostColorField hostId={HOST_ID} />)
    fireEvent.click(modeBtn('Terminal'))
    fireEvent.click(layerBtn('light'))
    expect(host().colors?.terminal).toEqual({ main: BLUE })
    expect(screen.getByTestId('host-color-editor')).toHaveAttribute('aria-label', 'Light')
    expect(layerBtn('main')).toHaveAttribute('data-inherits-console', 'false')
  })

  it('edits under Terminal never touch the console set', () => {
    render(<HostColorField hostId={HOST_ID} />)
    fireEvent.click(modeBtn('Terminal'))
    fireEvent.click(layerBtn('main'))
    fireEvent.click(screen.getByRole('button', { name: HOST_COLOR_PRESETS[0] }))
    expect(host().colors?.terminal?.main.color).toBe(HOST_COLOR_PRESETS[0])
    expect(host().colors?.console?.main).toEqual(BLUE)
  })

  it('switching mode closes any open editor', () => {
    render(<HostColorField hostId={HOST_ID} />)
    fireEvent.click(layerBtn('main'))
    fireEvent.click(modeBtn('Execution'))
    expect(screen.queryByTestId('host-color-editor')).toBeNull()
  })

  it('clear on Terminal removes only the terminal set', () => {
    useHostStore.getState().setHostColorLayer(HOST_ID, 'terminal', 'main', { color: '#ef4444', alpha: 100 })
    render(<HostColorField hostId={HOST_ID} />)
    fireEvent.click(modeBtn('Terminal'))
    fireEvent.click(screen.getByTestId('host-color-clear'))
    expect(host().colors).toEqual({ console: { main: BLUE } })
  })
})
```

- [ ] **Step 2: Run to verify failure**

Run: `cd spa && npx vitest run src/components/hosts/HostColorField.test.tsx`
Expected: FAIL.

- [ ] **Step 3: Implement**

`spa/src/components/hosts/HostColorField.tsx`:

```tsx
import { useState } from 'react'
import { Prohibit } from '@phosphor-icons/react'
import { useHostStore } from '../../stores/useHostStore'
import { useI18nStore } from '../../stores/useI18nStore'
import {
  HOST_COLOR_ALPHA_DEFAULTS,
  HOST_COLOR_MODES,
  HOST_COLOR_PRESETS,
  resolveHostColorSet,
  type HostColorLayerName,
  type HostColorMode,
} from '../../lib/host-color'
import { rgbaString } from '../../lib/color-space'
import { SegmentControl } from '../settings/SegmentControl'
import { Field } from './form-fields'
import { HostColorLayerEditor } from './HostColorLayerEditor'

const LAYERS: readonly HostColorLayerName[] = ['main', 'middle', 'light']

/**
 * Per-host color row (spec §6): mode switch → three layer swatches → inline
 * editor for the open layer. Reads the raw set for inherit flags and the
 * resolved set for what to paint; every edit goes straight to the store so the
 * badge on every tab row follows.
 */
export function HostColorField({ hostId }: { hostId: string }) {
  const t = useI18nStore((s) => s.t)
  const colors = useHostStore((s) => s.hosts[hostId]?.colors)
  const legacy = useHostStore((s) => s.hosts[hostId]?.color)
  const setHostColorLayer = useHostStore((s) => s.setHostColorLayer)
  const clearHostColorMode = useHostStore((s) => s.clearHostColorMode)

  const [mode, setMode] = useState<HostColorMode>('console')
  const [open, setOpen] = useState<HostColorLayerName | null>(null)

  const ownSet = colors?.[mode]
  const resolved = resolveHostColorSet({ colors, color: legacy }, mode)
  // A non-console mode with no own set inherits console (spec D4); console itself
  // may still be showing a legacy color, which counts as "has color".
  const inheritsConsole = mode !== 'console' && ownSet === undefined && resolved !== null
  const hasColor = resolved !== null

  const modeOptions = HOST_COLOR_MODES.map((m) => ({ value: m, label: t(`hosts.color.mode.${m}`) }))

  const openLayer = (layer: HostColorLayerName) => {
    if (open === layer) {
      setOpen(null)
      return
    }
    if (inheritsConsole && resolved) {
      // Materialise the mode from console's resolved main so edits stay per-mode.
      setHostColorLayer(hostId, mode, 'main', { color: resolved.main.color, alpha: resolved.main.alpha })
    }
    setOpen(layer)
  }

  const clear = () => {
    clearHostColorMode(hostId, mode)
    setOpen(null)
  }

  const editorProps = (() => {
    if (!open) return null
    // No color yet: the editor previews the first preset; nothing is written until the user acts.
    const set = resolved ?? {
      main: { color: HOST_COLOR_PRESETS[0], alpha: HOST_COLOR_ALPHA_DEFAULTS.main },
      middle: { color: HOST_COLOR_PRESETS[0], alpha: HOST_COLOR_ALPHA_DEFAULTS.middle },
      light: { color: HOST_COLOR_PRESETS[0], alpha: HOST_COLOR_ALPHA_DEFAULTS.light },
    }
    const inherited = open !== 'main' && ownSet?.[open]?.color === undefined
    return {
      layer: open,
      color: set[open].color,
      alpha: set[open].alpha,
      inherited,
      onChange: (next: { color?: string; alpha: number }) => setHostColorLayer(hostId, mode, open, next),
      onClose: () => setOpen(null),
    }
  })()

  return (
    <Field label={t('hosts.color.label')}>
      <div className="space-y-2">
        <div data-testid="host-color-mode">
          <SegmentControl
            options={modeOptions}
            value={mode}
            onChange={(m) => {
              setMode(m)
              setOpen(null)
            }}
          />
        </div>

        <div className="flex flex-wrap items-start gap-2">
          {LAYERS.map((layer) => {
            const l = resolved?.[layer]
            const own = layer === 'main' || ownSet?.[layer]?.color !== undefined
            const caption = !hasColor
              ? t('hosts.color.none')
              : `${own && !inheritsConsole ? l!.color : t('hosts.color.inherit_short')} ${l!.alpha}%`
            return (
              <button
                key={layer}
                type="button"
                data-testid={`host-color-layer-${layer}`}
                data-inherits-console={String(inheritsConsole)}
                aria-pressed={open === layer}
                onClick={() => openLayer(layer)}
                className={`flex flex-col items-start gap-1 rounded p-1 cursor-pointer border ${
                  open === layer ? 'border-border-active' : 'border-transparent hover:border-border-default'
                } ${inheritsConsole ? 'opacity-50' : ''}`}
              >
                <span
                  aria-hidden="true"
                  className="w-[26px] h-[18px] rounded border border-border-default"
                  style={{
                    background: l ? (rgbaString(l.color, l.alpha) ?? undefined) : undefined,
                    backgroundImage: l ? undefined : 'repeating-linear-gradient(45deg, transparent 0 4px, var(--border-default) 4px 5px)',
                  }}
                />
                <span className="text-[11px] leading-none text-text-secondary">{t(`hosts.color.layer.${layer}`)}</span>
                <span className="text-[10px] leading-none text-text-muted font-mono">{caption}</span>
              </button>
            )
          })}
          <button
            type="button"
            aria-label={t('hosts.color.clear_mode')}
            title={t('hosts.color.clear_mode')}
            data-testid="host-color-clear"
            onClick={clear}
            className="w-[26px] h-[18px] mt-1 rounded cursor-pointer border border-border-default flex items-center justify-center text-text-muted hover:text-text-secondary"
          >
            <Prohibit size={12} />
          </button>
          {inheritsConsole && <span className="text-[11px] text-text-muted self-center">{t('hosts.color.inherits_console')}</span>}
        </div>

        {editorProps && <HostColorLayerEditor {...editorProps} />}
      </div>
    </Field>
  )
}
```

Notes for the implementer:
- `clear` on `console` for a legacy-only host goes through `clearHostColorMode(hostId, 'console')`, which P1 defined to drop the legacy color — keep the old test intent ("clear button removes the color key") in the new suite if you like; `setHostColor` is no longer called from this file.
- Remove `setHostColor` from the store **only if** nothing else uses it (`rg -n 'setHostColor\\b' spa/src` — tests in `useHostStore.test.ts` still do; leave the action in place, it is cheap).
- If `SegmentControl`'s buttons have no `type="button"` and live inside a form somewhere, add `type="button"` there (one line, include in the commit).
- `OverviewSection.test.tsx`: run it; if it asserted on the old preset row, update to open Main first.

- [ ] **Step 4: Run to verify pass + full suite**

Run: `cd spa && npx vitest run && pnpm run lint && npx tsc --noEmit -p tsconfig.app.json`
Expected: all green.

- [ ] **Step 5: Commit**

```bash
git commit --only spa/src/components/hosts/HostColorField.tsx spa/src/components/hosts/HostColorField.test.tsx spa/src/locales/en.json spa/src/locales/zh-TW.json -m "feat(spa): per-mode tri-color picker on the host settings page"
```
(add `OverviewSection.test.tsx` / `SegmentControl.tsx` to `--only` if touched.)

---

## Done criteria (P3)

- Full suite, lint, tsc green; ≤ 20 files / ≤ 800 lines.
- Live (`:5174` after merge), spec §8: (2) set Terminal main to a different hue → agent tabs and shell tabs of the same host differ; a tab whose agent exits flips to console without reload; (3) dragging Middle alpha changes inactive tabs while dragging; Main hue drag changes the active tab live.
- The user then tunes the default alphas (100/60/22) by eye; a follow-up PR may adjust `HOST_COLOR_ALPHA_DEFAULTS`.
