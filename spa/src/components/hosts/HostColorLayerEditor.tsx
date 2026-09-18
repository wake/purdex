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
 * every tab row follows the drag.
 *
 * HSL lives in local state: a hex cannot carry hue at s=0 or hue+saturation at
 * l=0/100, so re-deriving from the prop on every render would snap the sliders
 * back (grey → hue drag → still grey → hue lost). The local HSL is replaced only
 * when the prop color no longer matches what the local HSL renders to — i.e. the
 * parent changed the color some other way (preset click, store sync).
 */
/**
 * Every H/S/L range is integer-stepped, so the local HSL is kept rounded to
 * whole degrees/percent — otherwise a hex whose channel isn't an exact multiple
 * of 255 (e.g. `#808080` → l≈50.196) leaves a fractional remainder sitting in
 * `l` that a later, unrelated saturation/hue edit then bakes into the result
 * (grey → hue 120° → saturation 100% landed on `#01ff01`, not `#00ff00`).
 */
const roundHsl = ({ h, s, l }: Hsl): Hsl => ({ h: Math.round(h), s: Math.round(s), l: Math.round(l) })

export function HostColorLayerEditor({ layer, color, alpha, inherited, onChange, onClose }: HostColorLayerEditorProps) {
  const t = useI18nStore((s) => s.t)
  const [hsl, setHsl] = useState<Hsl>(() => roundHsl(hexToHsl(color) ?? { h: 0, s: 0, l: 50 }))
  const [draft, setDraft] = useState(color)
  const [invalid, setInvalid] = useState(false)
  const [synced, setSynced] = useState(color)
  if (synced !== color) {
    // Render-phase adjust (no effect): external color change.
    setSynced(color)
    setDraft(color)
    setInvalid(false)
    if (hslToHex(hsl) !== color) setHsl(roundHsl(hexToHsl(color) ?? { h: 0, s: 0, l: 50 }))
  }

  const write = (next: Partial<Hsl>, nextAlpha = alpha) => {
    const merged = { ...hsl, ...next }
    setHsl(merged)
    onChange({ color: hslToHex(merged), alpha: nextAlpha })
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
