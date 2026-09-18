import { useEffect, useRef, useState, type KeyboardEvent, type PointerEvent as ReactPointerEvent } from 'react'
import { useI18nStore } from '../../stores/useI18nStore'
import { HOST_COLOR_PRESETS, normalizeHostColor, type HostColorLayerName } from '../../lib/host-color'
import { hexToHsv, hsvToHex, rgbaString, type Hsv } from '../../lib/color-space'
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
 * popover). Every control writes through `onChange` live, so the badge on
 * every tab row follows the drag.
 *
 * HSV lives in local state: a hex cannot carry hue at s=0 or hue+saturation at
 * v=0, so re-deriving from the prop on every render would snap the picker back
 * (grey → hue drag → still grey → hue lost). The local HSV is replaced only
 * when the prop color no longer matches what the local HSV renders to — i.e.
 * the parent changed the color some other way (preset click, store sync).
 */
/**
 * The hue strip is integer-stepped and the area is picked/nudged in integer
 * steps too, so the local HSV is kept rounded to whole degrees/percent —
 * otherwise a hex whose channel isn't an exact multiple of 255 (e.g. `#808080`
 * → v≈50.196) leaves a fractional remainder sitting in `v` that a later,
 * unrelated saturation/hue edit then bakes into the result.
 */
const roundHsv = ({ h, s, v }: Hsv): Hsv => ({ h: Math.round(h), s: Math.round(s), v: Math.round(v) })
const fallbackHsv: Hsv = { h: 0, s: 0, v: 50 }

export function HostColorLayerEditor({ layer, color, alpha, inherited, onChange, onClose }: HostColorLayerEditorProps) {
  const t = useI18nStore((s) => s.t)
  const [hsv, setHsv] = useState<Hsv>(() => roundHsv(hexToHsv(color) ?? fallbackHsv))
  const [draft, setDraft] = useState(color)
  const [invalid, setInvalid] = useState(false)
  const [synced, setSynced] = useState(color)
  if (synced !== color) {
    // Render-phase adjust (no effect): external color change.
    setSynced(color)
    setDraft(color)
    setInvalid(false)
    if (hsvToHex(hsv) !== color) setHsv(roundHsv(hexToHsv(color) ?? fallbackHsv))
  }

  const write = (next: Partial<Hsv>, nextAlpha = alpha) => {
    const merged = { ...hsv, ...next }
    setHsv(merged)
    onChange({ color: hsvToHex(merged), alpha: nextAlpha })
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

  const areaRef = useRef<HTMLDivElement>(null)
  const activePointer = useRef<number | null>(null)
  const fallbackCleanupRef = useRef<(() => void) | null>(null)
  const fallbackActive = useRef(false)
  const pick = (e: { clientX: number; clientY: number }) => {
    const el = areaRef.current
    if (!el) return
    const r = el.getBoundingClientRect()
    if (r.width === 0 || r.height === 0) return
    const s = Math.round(Math.min(100, Math.max(0, ((e.clientX - r.left) / r.width) * 100)))
    const v = Math.round(Math.min(100, Math.max(0, 100 - ((e.clientY - r.top) / r.height) * 100)))
    write({ s, v })
  }
  // Some environments (older WebViews, jsdom) don't implement
  // `setPointerCapture`, and even when they do, capture can be lost without a
  // matching pointerup (e.g. the pointer leaves the window). Either way we
  // still need to keep tracking the drag: fall back to window-level listeners
  // filtered by pointerId, torn down on pointerup/cancel or unmount.
  //
  // A pointermove that lands on the area itself still bubbles up to `window`,
  // so while the fallback is attached the element's own onPointerMove/onPointerUp
  // must no-op — otherwise both the element handler and the window listener
  // would `pick()` the same event.
  const attachWindowPointerFallback = (pointerId: number) => {
    fallbackActive.current = true
    const onMove = (e: PointerEvent) => {
      if (e.pointerId !== pointerId) return
      pick(e)
    }
    const onEnd = (e: PointerEvent) => {
      if (e.pointerId !== pointerId) return
      detach()
    }
    const detach = () => {
      window.removeEventListener('pointermove', onMove)
      window.removeEventListener('pointerup', onEnd)
      window.removeEventListener('pointercancel', onEnd)
      fallbackActive.current = false
      if (activePointer.current === pointerId) activePointer.current = null
      fallbackCleanupRef.current = null
    }
    window.addEventListener('pointermove', onMove)
    window.addEventListener('pointerup', onEnd)
    window.addEventListener('pointercancel', onEnd)
    fallbackCleanupRef.current = detach
  }
  useEffect(() => () => fallbackCleanupRef.current?.(), [])
  const nudge = (e: KeyboardEvent<HTMLDivElement>) => {
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
  const areaLabel = `${t('hosts.color.area')} — ${t('hosts.color.area_value', { s: hsv.s, v: hsv.v })}`

  return (
    <div
      data-testid="host-color-editor"
      role="group"
      aria-label={t(`hosts.color.layer.${layer}`)}
      className="space-y-3"
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

      {!inherited && (
        <>
          <div
            ref={areaRef}
            data-testid="host-color-area"
            aria-roledescription="color area"
            aria-label={areaLabel}
            tabIndex={0}
            onKeyDown={nudge}
            onPointerDown={(e: ReactPointerEvent<HTMLDivElement>) => {
              if (e.button !== 0) return
              if (activePointer.current !== null) return
              e.preventDefault()
              activePointer.current = e.pointerId
              let captured = false
              if (typeof e.currentTarget.setPointerCapture === 'function') {
                try {
                  e.currentTarget.setPointerCapture(e.pointerId)
                  captured = true
                } catch {
                  captured = false
                }
              }
              if (!captured) attachWindowPointerFallback(e.pointerId)
              pick(e)
            }}
            onPointerMove={(e: ReactPointerEvent<HTMLDivElement>) => {
              if (fallbackActive.current) return // the window fallback listener already handled this (it bubbled there too)
              if (e.pointerId === activePointer.current) pick(e)
            }}
            onPointerUp={(e: ReactPointerEvent<HTMLDivElement>) => {
              if (fallbackActive.current) return // the window fallback listener already handled this
              if (e.pointerId !== activePointer.current) return
              activePointer.current = null
              if (typeof e.currentTarget.releasePointerCapture === 'function') {
                e.currentTarget.releasePointerCapture(e.pointerId)
              }
            }}
            onPointerCancel={(e: ReactPointerEvent<HTMLDivElement>) => {
              if (e.pointerId !== activePointer.current) return
              activePointer.current = null
              if (typeof e.currentTarget.releasePointerCapture === 'function') {
                e.currentTarget.releasePointerCapture(e.pointerId)
              }
            }}
            onLostPointerCapture={() => {
              fallbackCleanupRef.current?.()
              activePointer.current = null
            }}
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
              type="range"
              data-testid="host-color-range-h"
              aria-label={t('hosts.color.hue')}
              min={0}
              max={360}
              step={1}
              value={hsv.h}
              onInput={(e) => write({ h: Number((e.target as HTMLInputElement).value) })}
              onChange={() => {}}
              className={RANGE_CLASS}
              style={{ background: track(['#ff0000', '#ffff00', '#00ff00', '#00ffff', '#0000ff', '#ff00ff', '#ff0000']) }}
            />
          </label>
        </>
      )}

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
