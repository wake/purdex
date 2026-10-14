import { useRef, useState } from 'react'
import { Prohibit } from '@phosphor-icons/react'
import { useI18nStore } from '../../stores/useI18nStore'
import {
  HOST_COLOR_ALPHA_DEFAULTS,
  HOST_COLOR_MODES,
  HOST_COLOR_PRESETS,
  resolveHostColorSet,
  type HostColorLayer,
  type HostColorLayerName,
  type HostColorMode,
} from '../../lib/host-color'
import type { HostConfig } from '../../stores/useHostStore'
import { rgbaString } from '../../lib/color-space'
import { SegmentControl } from '../settings/SegmentControl'
import { HostColorLayerEditor } from './HostColorLayerEditor'
import { FloatingPanel } from '../FloatingPanel'

const LAYERS: readonly HostColorLayerName[] = ['main', 'middle', 'light']

/**
 * The colour editor's body (spec §6), controlled: mode switch → three layer swatches → inline editor for the open
 * layer. It reads the raw set for inherit flags and the resolved set for what to paint, and reports every edit through
 * `onSetLayer` / `onClearMode` — it knows no host. `HostColorField` binds it to a daemon host's look in the App's
 * store; the probe-only host dialog binds it to a draft.
 */
export function HostColorPicker({
  colors,
  legacyColor,
  mode,
  onModeChange,
  onSetLayer,
  onClearMode,
}: {
  colors: HostConfig['colors']
  /** The deprecated single colour a look may still carry (counts as the console colour). */
  legacyColor?: string
  mode: HostColorMode
  onModeChange: (mode: HostColorMode) => void
  onSetLayer: (mode: HostColorMode, layer: HostColorLayerName, value: HostColorLayer) => void
  onClearMode: (mode: HostColorMode) => void
}) {
  const t = useI18nStore((s) => s.t)
  const [open, setOpen] = useState<HostColorLayerName | null>(null)
  const swatchRowRef = useRef<HTMLDivElement>(null)

  const ownSet = colors?.[mode]
  const resolved = resolveHostColorSet({ colors, color: legacyColor }, mode)
  // A non-console mode with no own set inherits console (spec D4); console itself
  // may still be showing a legacy color, which counts as "has color".
  const inheritsConsole = mode !== 'console' && ownSet === undefined && resolved !== null
  const hasColor = resolved !== null

  const modeOptions = HOST_COLOR_MODES.map((m) => ({ value: m, label: t(`hosts.color.mode.${m}`) }))

  // `resolved` can go null out from under an open editor (a no-op materialise write —
  // e.g. the host vanished — or a remote clear while it's open). Left alone, `open`
  // would keep a swatch pressed with no editor to show for it (spec review PR #1160
  // F2) — and worse, a *derived* mask (`resolved ? open : null`) hides that stale
  // state without clearing it, so if the mode's colors come back later (host
  // re-added, remote re-sync) the editor pops back open on a layer the user never
  // reopened (PR #1160 R2 re-review). So reset the state itself, render-phase, the
  // same "adjust state during render" pattern `HostColorLayerEditor` already uses
  // for its own `synced` re-sync.
  if (!resolved && open !== null) setOpen(null)

  const openLayer = (layer: HostColorLayerName) => {
    if (open === layer) {
      setOpen(null)
      return
    }
    if (inheritsConsole && resolved) {
      // Materialise the mode from console's resolved main so edits stay per-mode.
      onSetLayer(mode, 'main', { color: resolved.main.color, alpha: resolved.main.alpha })
    } else if (!resolved) {
      // No color at all: the store refuses middle/light writes without a set (spec §4.1),
      // so give the mode a main first. The user's click *is* the act of picking a color.
      onSetLayer(mode, 'main', { color: HOST_COLOR_PRESETS[0], alpha: HOST_COLOR_ALPHA_DEFAULTS.main })
    }
    setOpen(layer)
  }

  const clear = () => {
    onClearMode(mode)
    setOpen(null)
  }

  const editorProps = (() => {
    // The render-phase reset above guarantees `open === null` whenever `resolved`
    // is null, so `resolved` is non-null here; the `!resolved` check is belt-and-
    // braces against reordering this block above that reset.
    if (!open || !resolved) return null
    const layer = open
    const set = resolved
    const inherited = layer !== 'main' && ownSet?.[layer]?.color === undefined
    return {
      layer,
      color: set[layer].color,
      alpha: set[layer].alpha,
      inherited,
      onChange: (next: { color?: string; alpha: number }) => onSetLayer(mode, layer, next),
      onClose: () => setOpen(null),
    }
  })()

  return (
    <div className="space-y-2">
      <div data-testid="host-color-mode">
        <SegmentControl
          options={modeOptions}
          value={mode}
          onChange={(m) => {
            onModeChange(m)
            setOpen(null)
          }}
        />
      </div>

      <div ref={swatchRowRef} className="flex flex-wrap items-start gap-2">
        {LAYERS.map((layer) => {
          const l = resolved?.[layer]
          const own = layer === 'main' || ownSet?.[layer]?.color !== undefined
          const caption = !hasColor
            ? t('hosts.color.none')
            : inheritsConsole
              ? t('hosts.color.inherits_console')
              : `${own ? l!.color : t('hosts.color.inherit_short')} ${l!.alpha}%`
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
      </div>

      {editorProps && (
        <FloatingPanel
          title={t(`hosts.color.layer.${editorProps.layer}`)}
          anchorRef={swatchRowRef}
          onClose={() => setOpen(null)}
          width={320}
        >
          <HostColorLayerEditor key={`${mode}:${editorProps.layer}`} {...editorProps} />
        </FloatingPanel>
      )}
    </div>
  )
}
