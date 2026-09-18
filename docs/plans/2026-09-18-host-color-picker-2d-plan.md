# Host Color Picker — 2D saturation/value area — Implementation Plan

**Goal:** Replace the three Hue / Saturation / Lightness range sliders in `HostColorLayerEditor` with the classic picker layout: one big **saturation × value square** for the current hue (white → pure hue left→right, transparent → black top→bottom) with a draggable marker, plus a **hue strip** and the existing **opacity strip** and hex input. Presets and the inherit toggle stay.

**Spec addendum (host-color-modes spec §6.3):** "H / S / L sliders" → "an HSV saturation/value area + hue slider". User feedback 2026-09-18: "我是要一般的一個大方塊有全域色的那個色盤".

**Tech:** React 19 / Vitest + Testing Library / Tailwind 4 / TS strict. Tests `cd spa && npx vitest run <path>`; lint `pnpm run lint`; typecheck `npx tsc --noEmit -p tsconfig.app.json`.

## Global Constraints

- Worktree `/Users/wake/Workspace/wake/purdex/.claude/worktrees/host-color-picker-2d`, branch `worktree-host-color-picker-2d`; every command `cd`s there.
- TDD; commits via `git commit --only`.
- Editor keeps its **prop contract** (`layer, color, alpha, inherited, onChange({color?, alpha}), onClose`) and these test ids: `host-color-editor`, `host-color-preview`, `host-color-inherit`, `host-color-range-a`, `host-color-hex`, `host-color-editor-close`. `host-color-range-h` stays (the hue strip). `host-color-range-s` / `-l` go away; new: `host-color-area` (the square), `host-color-area-marker`.
- Local color model becomes **HSV** (`h` 0–360, `s` 0–100, `v` 0–100, integers) with the same "keep local state, re-derive only when the prop hex no longer matches the local model" rule as today — grey/black/white must not lose hue/saturation between drags.
- The area is keyboard-operable: `tabIndex=0`, `role="slider"` is wrong for 2D — use `role="application"`-free plain `div` with `aria-label` and arrow keys: ←/→ change s by 1, ↑/↓ change v by 1 (Shift = 10).
- Pointer handling: on `pointerdown` capture the pointer (`setPointerCapture`), update on `pointermove` while captured, release on `pointerup`. Position → `s = x / width * 100`, `v = 100 − y / height * 100`, clamped, rounded.
- Locale: remove `hosts.color.saturation` / `hosts.color.lightness` if no longer used; add `hosts.color.area` = "Saturation and brightness" / "飽和度與明度" (aria-label of the square).
- `HostColorField.test.tsx` / `HostColorField.integration.test.tsx` / `OverviewSection.test.tsx` only use `host-color-range-a`, presets, hex and inherit — run them to confirm nothing else depended on `-s` / `-l`.

---

### Task 1: HSV conversions (`lib/color-space.ts`)

**Files:** `spa/src/lib/color-space.ts`, `spa/src/lib/color-space.test.ts`

**Interfaces:**
```ts
export interface Hsv { h: number; s: number; v: number }   // 0–360, 0–100, 0–100, floats
export function hexToHsv(hex: string): Hsv | null
export function hsvToHex(hsv: Hsv): string                   // lowercase #rrggbb; h wraps, s/v clamp
```
(Keep `hexToHsl` / `hslToHex` — still used by nothing after this PR except their tests; delete them and their tests if `rg -n 'hexToHsl|hslToHex' spa/src` shows only the test file after Task 2.)

- [ ] **Step 1: Write the failing tests** (append to `color-space.test.ts`)

```ts
describe('hexToHsv / hsvToHex', () => {
  it('converts primaries, black and white', () => {
    expect(hexToHsv('#ff0000')).toEqual({ h: 0, s: 100, v: 100 })
    expect(hexToHsv('#00ff00')).toEqual({ h: 120, s: 100, v: 100 })
    expect(hexToHsv('#0000ff')).toEqual({ h: 240, s: 100, v: 100 })
    expect(hexToHsv('#000000')).toEqual({ h: 0, s: 0, v: 0 })
    expect(hexToHsv('#ffffff')).toEqual({ h: 0, s: 0, v: 100 })
    expect(hexToHsv('#808080')).toEqual({ h: 0, s: 0, v: (128 / 255) * 100 })
  })
  it('rejects non-#rrggbb', () => {
    expect(hexToHsv('abc')).toBeNull()
  })
  it.each(HOST_COLOR_PRESETS)('round-trips preset %s exactly', (hex) => {
    expect(hsvToHex(hexToHsv(hex)!)).toBe(hex)
  })
  it('hsvToHex: white at s=0,v=100; black at v=0; pure hue at s=100,v=100; wraps hue', () => {
    expect(hsvToHex({ h: 200, s: 0, v: 100 })).toBe('#ffffff')
    expect(hsvToHex({ h: 200, s: 100, v: 0 })).toBe('#000000')
    expect(hsvToHex({ h: 360, s: 100, v: 100 })).toBe('#ff0000')
    expect(hsvToHex({ h: -120, s: 100, v: 100 })).toBe('#0000ff')
    expect(hsvToHex({ h: 120, s: 150, v: 200 })).toBe('#00ff00')
  })
})
```

- [ ] **Step 2: Run** `cd spa && npx vitest run src/lib/color-space.test.ts` → FAIL.

- [ ] **Step 3: Implement** (append to `color-space.ts`; reuse `hexToRgb`, `clamp01`)

```ts
export interface Hsv {
  /** 0–360 */
  h: number
  /** 0–100 */
  s: number
  /** 0–100 */
  v: number
}

export function hexToHsv(hex: string): Hsv | null {
  const rgb = hexToRgb(hex)
  if (!rgb) return null
  const r = rgb.r / 255, g = rgb.g / 255, b = rgb.b / 255
  const max = Math.max(r, g, b), min = Math.min(r, g, b)
  const d = max - min
  const v = max
  const s = max === 0 ? 0 : d / max
  let h = 0
  if (d !== 0) {
    if (max === r) h = ((g - b) / d) % 6
    else if (max === g) h = (b - r) / d + 2
    else h = (r - g) / d + 4
    h *= 60
    if (h < 0) h += 360
  }
  return { h, s: s * 100, v: v * 100 }
}

export function hsvToHex({ h, s, v }: Hsv): string {
  const hh = ((h % 360) + 360) % 360
  const ss = clamp01(s / 100), vv = clamp01(v / 100)
  const c = vv * ss
  const x = c * (1 - Math.abs(((hh / 60) % 2) - 1))
  const m = vv - c
  let r = 0, g = 0, b = 0
  if (hh < 60) [r, g, b] = [c, x, 0]
  else if (hh < 120) [r, g, b] = [x, c, 0]
  else if (hh < 180) [r, g, b] = [0, c, x]
  else if (hh < 240) [r, g, b] = [0, x, c]
  else if (hh < 300) [r, g, b] = [x, 0, c]
  else [r, g, b] = [c, 0, x]
  const toHex = (n: number) => Math.round((n + m) * 255).toString(16).padStart(2, '0')
  return `#${toHex(r)}${toHex(g)}${toHex(b)}`
}
```

- [ ] **Step 4: Run** → PASS. **Step 5: Commit** `git commit --only spa/src/lib/color-space.ts spa/src/lib/color-space.test.ts -m "feat(spa): hexToHsv/hsvToHex for the 2D color picker"`

---

### Task 2: `HostColorLayerEditor` — saturation/value area + hue strip

**Files:** `spa/src/components/hosts/HostColorLayerEditor.tsx`, `spa/src/components/hosts/HostColorLayerEditor.test.tsx`, `spa/src/locales/en.json`, `spa/src/locales/zh-TW.json`; (maybe) `spa/src/lib/color-space.ts` + test if the HSL helpers are removed.

- [ ] **Step 1: Rewrite the affected tests**

In `HostColorLayerEditor.test.tsx`: replace the three HSL tests (`dragging hue…`, `keeps hue while the color is grey…`, `keeps saturation and hue while black or white…`, `re-derives HSL…`) and the `H/S/L/A ranges` expectation with:

```tsx
function areaRect(el: HTMLElement) {
  el.getBoundingClientRect = () => ({ left: 0, top: 0, width: 200, height: 200, right: 200, bottom: 200, x: 0, y: 0, toJSON() {} }) as DOMRect
}
function pointAt(el: HTMLElement, x: number, y: number) {
  el.setPointerCapture = () => {}
  el.releasePointerCapture = () => {}
  fireEvent.pointerDown(el, { clientX: x, clientY: y, pointerId: 1, button: 0 })
}

it('shows presets, the area, hue + alpha strips, hex and a preview', () => {
  render(<HostColorLayerEditor {...base} onChange={() => {}} />)
  expect(screen.getByTestId('host-color-area')).toBeInTheDocument()
  expect(screen.getByTestId('host-color-area-marker')).toBeInTheDocument()
  expect(screen.getByTestId('host-color-range-h')).toBeInTheDocument()
  expect(screen.getByTestId('host-color-range-a')).toBeInTheDocument()
  expect(screen.queryByTestId('host-color-range-s')).toBeNull()
  expect(screen.queryByTestId('host-color-range-l')).toBeNull()
})

it('the area is painted with the current hue and the marker sits at (s, 100−v)', () => {
  render(<HostColorLayerEditor {...base} color="#ff0000" onChange={() => {}} />)
  const area = screen.getByTestId('host-color-area')
  expect(area.style.background).toContain('#ff0000')
  const marker = screen.getByTestId('host-color-area-marker')
  expect(marker.style.left).toBe('100%')
  expect(marker.style.top).toBe('0%')
})

it('pointer down on the area writes s/v from the position, keeping hue and alpha', () => {
  const onChange = vi.fn()
  render(<HostColorLayerEditor {...base} color="#ff0000" alpha={80} onChange={onChange} />)
  const area = screen.getByTestId('host-color-area')
  areaRect(area)
  pointAt(area, 0, 0)      // s=0, v=100 → white
  expect(onChange).toHaveBeenLastCalledWith({ color: '#ffffff', alpha: 80 })
  pointAt(area, 200, 200)  // s=100, v=0 → black
  expect(onChange).toHaveBeenLastCalledWith({ color: '#000000', alpha: 80 })
  pointAt(area, 200, 0)    // s=100, v=100 → pure hue
  expect(onChange).toHaveBeenLastCalledWith({ color: '#ff0000', alpha: 80 })
})

it('pointer move while captured keeps writing; positions are clamped to the area', () => {
  const onChange = vi.fn()
  render(<HostColorLayerEditor {...base} color="#ff0000" onChange={onChange} />)
  const area = screen.getByTestId('host-color-area')
  areaRect(area)
  pointAt(area, 100, 100)
  fireEvent.pointerMove(area, { clientX: 500, clientY: -50, pointerId: 1 })
  expect(onChange).toHaveBeenLastCalledWith({ color: '#ff0000', alpha: 100 })
  fireEvent.pointerUp(area, { pointerId: 1 })
  const calls = onChange.mock.calls.length
  fireEvent.pointerMove(area, { clientX: 10, clientY: 10, pointerId: 1 })
  expect(onChange.mock.calls.length).toBe(calls)
})

it('hue strip changes hue only; the area repaints with the new hue', () => {
  const onChange = vi.fn()
  const { rerender } = render(<HostColorLayerEditor {...base} color="#ff0000" onChange={onChange} />)
  fireEvent.input(screen.getByTestId('host-color-range-h'), { target: { value: '120' } })
  expect(onChange).toHaveBeenLastCalledWith({ color: '#00ff00', alpha: 100 })
  rerender(<HostColorLayerEditor {...base} color="#00ff00" onChange={onChange} />)
  expect(screen.getByTestId('host-color-area').style.background).toContain('#00ff00')
})

it('keeps hue while the color is grey/black/white across drags', () => {
  const onChange = vi.fn()
  const { rerender } = render(<HostColorLayerEditor {...base} color="#000000" onChange={onChange} />)
  fireEvent.input(screen.getByTestId('host-color-range-h'), { target: { value: '240' } })   // still black
  rerender(<HostColorLayerEditor {...base} color="#000000" onChange={onChange} />)
  const area = screen.getByTestId('host-color-area')
  areaRect(area)
  pointAt(area, 200, 0)   // s=100 v=100 with the remembered hue 240
  expect(onChange).toHaveBeenLastCalledWith({ color: '#0000ff', alpha: 100 })
})

it('arrow keys nudge s/v (Shift ×10)', () => {
  const onChange = vi.fn()
  render(<HostColorLayerEditor {...base} color="#ff0000" onChange={onChange} />)
  const area = screen.getByTestId('host-color-area')
  fireEvent.keyDown(area, { key: 'ArrowLeft' })
  expect(onChange).toHaveBeenLastCalledWith({ color: hsvToHex({ h: 0, s: 99, v: 100 }), alpha: 100 })
  fireEvent.keyDown(area, { key: 'ArrowDown', shiftKey: true })
  expect(onChange).toHaveBeenLastCalledWith({ color: hsvToHex({ h: 0, s: 99, v: 90 }), alpha: 100 })
})

it('re-derives HSV when the parent hands in a different color', () => {
  const { rerender } = render(<HostColorLayerEditor {...base} color="#ff0000" onChange={() => {}} />)
  rerender(<HostColorLayerEditor {...base} color="#0000ff" onChange={() => {}} />)
  expect((screen.getByTestId('host-color-range-h') as HTMLInputElement).value).toBe('240')
})
```
(import `hsvToHex` from `'../../lib/color-space'`.) Keep the middle/light describe as is (inherited → no area, no hue strip, alpha only — add `expect(screen.queryByTestId('host-color-area')).toBeNull()` to the "inherited" test).

- [ ] **Step 2: Run** `cd spa && npx vitest run src/components/hosts/HostColorLayerEditor.test.tsx` → FAIL.

- [ ] **Step 3: Implement**

Replace the HSL state with HSV and the `ranges` block with the area + hue strip:

```tsx
const roundHsv = ({ h, s, v }: Hsv): Hsv => ({ h: Math.round(h), s: Math.round(s), v: Math.round(v) })
const fallbackHsv: Hsv = { h: 0, s: 0, v: 50 }

// state
const [hsv, setHsv] = useState<Hsv>(() => roundHsv(hexToHsv(color) ?? fallbackHsv))
// in the synced block: if (hsvToHex(hsv) !== color) setHsv(roundHsv(hexToHsv(color) ?? fallbackHsv))
const write = (next: Partial<Hsv>, nextAlpha = alpha) => {
  const merged = { ...hsv, ...next }
  setHsv(merged)
  onChange({ color: hsvToHex(merged), alpha: nextAlpha })
}

// area
const areaRef = useRef<HTMLDivElement>(null)
const dragging = useRef(false)
const pick = (e: { clientX: number; clientY: number }) => {
  const el = areaRef.current
  if (!el) return
  const r = el.getBoundingClientRect()
  if (r.width === 0 || r.height === 0) return
  const s = Math.round(Math.min(100, Math.max(0, ((e.clientX - r.left) / r.width) * 100)))
  const v = Math.round(Math.min(100, Math.max(0, 100 - ((e.clientY - r.top) / r.height) * 100)))
  write({ s, v })
}
const nudge = (e: React.KeyboardEvent) => {
  const step = e.shiftKey ? 10 : 1
  const delta: Record<string, Partial<Hsv>> = {
    ArrowLeft: { s: Math.max(0, hsv.s - step) },
    ArrowRight: { s: Math.min(100, hsv.s + step) },
    ArrowUp: { v: Math.min(100, hsv.v + step) },
    ArrowDown: { v: Math.max(0, hsv.v - step) },
  }
  const next = delta[e.key]
  if (!next) return
  e.preventDefault()
  write(next)
}
const hueHex = hsvToHex({ h: hsv.h, s: 100, v: 100 })
```

JSX (in place of the three range labels, still gated by `!inherited`):

```tsx
<div
  ref={areaRef}
  data-testid="host-color-area"
  role="img"
  aria-label={t('hosts.color.area')}
  tabIndex={0}
  onKeyDown={nudge}
  onPointerDown={(e) => {
    if (e.button !== 0) return
    e.preventDefault()
    dragging.current = true
    e.currentTarget.setPointerCapture(e.pointerId)
    pick(e)
  }}
  onPointerMove={(e) => { if (dragging.current) pick(e) }}
  onPointerUp={(e) => {
    dragging.current = false
    e.currentTarget.releasePointerCapture(e.pointerId)
  }}
  onPointerCancel={() => { dragging.current = false }}
  className="relative w-full h-40 rounded cursor-crosshair touch-none select-none outline-none focus-visible:ring-2 focus-visible:ring-border-active"
  style={{ background: `linear-gradient(to top, #000000, transparent), linear-gradient(to right, #ffffff, ${hueHex})` }}
>
  <span
    data-testid="host-color-area-marker"
    aria-hidden="true"
    className="absolute w-3 h-3 -ml-1.5 -mt-1.5 rounded-full border-2 border-white shadow-[0_0_0_1px_rgba(0,0,0,.6)] pointer-events-none"
    style={{ left: `${hsv.s}%`, top: `${100 - hsv.v}%`, background: color }}
  />
</div>

<label className="block space-y-1">
  <span className="text-[11px] text-text-muted">{t('hosts.color.hue')}</span>
  <input
    type="range" data-testid="host-color-range-h" aria-label={t('hosts.color.hue')}
    min={0} max={360} step={1} value={hsv.h}
    onInput={(e) => write({ h: Number((e.target as HTMLInputElement).value) })}
    onChange={() => {}}
    className={RANGE_CLASS}
    style={{ background: track(['#ff0000', '#ffff00', '#00ff00', '#00ffff', '#0000ff', '#ff00ff', '#ff0000']) }}
  />
</label>
```
(`role="img"` + `aria-label` keeps axe quiet for a focusable div that is not a form control; the arrow keys are documented in the aria-label string if you prefer: "Saturation and brightness — arrow keys to adjust".) Remove `hexToHsl` / `hslToHex` imports; the alpha strip and hex input stay as they are.

Locales: add `hosts.color.area`; remove `hosts.color.saturation` / `hosts.color.lightness` (both files) after confirming with `rg` they have no other users.

- [ ] **Step 4: Run** `cd spa && npx vitest run src/components/hosts src/locales && npx tsc --noEmit -p tsconfig.app.json && pnpm run lint && npx vitest run` → all green.

- [ ] **Step 5: Commit** `git commit --only <touched files> -m "feat(spa): host color editor uses a saturation/value area + hue strip instead of H/S/L sliders"`

---

## Done criteria

- Full suite / lint / tsc green.
- Live: the editor shows a big square (white→hue, fading to black), a hue strip, opacity strip, hex; dragging in the square updates tab badges and the preview live; grey/black/white keep their hue between drags.
