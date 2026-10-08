// spa/src/components/UnattendedButton.tsx — the title-bar 無人值守模式 toggle (unattended spec D-U23-5, D-U23-6; plan
// PU-2b). One button for every host shown on the current workbench: off (all off), on (all on: the accent and
// 「無人值守中」), partial (mixed, unreachable or too old: a warning ring, the hosts by group in the tooltip), none (no
// shown host: disabled). A press turns every reachable shown host on (from off or partial) or off (from on); a failure
// toasts once, naming the hosts.
//
// Not tab-hosted: rendered by TitleBar, once per window. Everything it shows comes from the stores (host runtime, the
// shown list, `useUnattendedStore`), so a remount shows the same, and another window's press reaches this one through
// the daemon's `changed` event. Pressing never writes the store: the event does.
import { useRef } from 'react'
import { MoonStars } from '@phosphor-icons/react'
import { useHostStore } from '../stores/useHostStore'
import { useI18nStore } from '../stores/useI18nStore'
import { useUndoToast } from '../stores/useUndoToast'
import { useUnattendedStore } from '../stores/useUnattendedStore'
import { useShownRefFilter } from '../lib/shown-hosts'
import { hostLabel, hostLookOf, useHostLookResolver } from '../lib/host-look'
import { keepFocus } from '../lib/keep-focus'
import { aggregateUnattended, type UnattendedAggregate } from '../lib/team/unattended-aggregate'
import { toggleUnattended } from '../lib/team/unattended-toggle'
import { BUTTON, IDLE, PRESSED } from './title-bar-styles'

type T = (key: string, params?: Record<string, string | number>) => string

const HOST_SEPARATOR = ', '

function tooltipOf(t: T, agg: UnattendedAggregate, label: (hostId: string) => string): string {
  if (agg.mode === 'none') return t('unattended.tooltip.none')
  if (agg.mode === 'on') return t('unattended.tooltip.on')
  if (agg.mode === 'off') return t('unattended.tooltip.off')
  const names = (ids: string[]) => ids.map(label).join(HOST_SEPARATOR)
  const lines = [t('unattended.button_partial')]
  if (agg.off.length > 0) lines.push(t('unattended.tooltip.partial_off', { hosts: names(agg.off) }))
  if (agg.unreachable.length > 0) lines.push(t('unattended.tooltip.partial_unreachable', { hosts: names(agg.unreachable) }))
  if (agg.unsupported.length > 0) lines.push(t('unattended.tooltip.partial_unsupported', { hosts: names(agg.unsupported) }))
  return lines.join('\n')
}

export function UnattendedButton() {
  const t = useI18nStore((s) => s.t)
  const hostOrder = useHostStore((s) => s.hostOrder)
  const runtime = useHostStore((s) => s.runtime)
  const byHost = useUnattendedStore((s) => s.byHost)
  const isShown = useShownRefFilter()
  const lookOf = useHostLookResolver()
  // A press in flight: a second click (a double-click) sends nothing more. Not state: nothing on screen depends on it.
  const busy = useRef(false)

  const agg = aggregateUnattended(hostOrder.filter(isShown), runtime, byHost)
  const label = (hostId: string) => hostLabel(hostId, lookOf(hostId))
  const { mode } = agg

  const press = async () => {
    if (busy.current) return
    busy.current = true
    try {
      const { failed } = await toggleUnattended(agg)
      if (failed.length > 0) {
        // Read live: the toast is shown after the PUTs, the names as they are now.
        const tNow = useI18nStore.getState().t
        const hosts = failed.map((f) => `${hostLabel(f.hostId, hostLookOf(f.hostId))} (${f.code})`).join(HOST_SEPARATOR)
        useUndoToast.getState().show(tNow('unattended.toast.failed', { hosts }))
      }
    } finally {
      busy.current = false
    }
  }

  const look = mode === 'on' ? `${PRESSED} flex items-center gap-1` : mode === 'partial' ? `${IDLE} ring-1 ring-status-warning` : IDLE
  const name = mode === 'on' ? 'unattended.button_on' : mode === 'partial' ? 'unattended.button_partial' : 'unattended.button_off'

  return (
    <div
      data-testid="unattended-buttons"
      className="shrink-0 flex items-center gap-0.5 translate-y-[2.5px] mr-1"
      style={{ WebkitAppRegion: 'no-drag' } as React.CSSProperties}
    >
      <button
        data-testid="unattended-toggle"
        data-state={mode}
        aria-pressed={mode === 'on'}
        aria-label={t(name)}
        disabled={mode === 'none'}
        className={`${BUTTON} ${look}`}
        title={tooltipOf(t, agg, label)}
        // A mouse press leaves focus on the pane (shell polish spec §4).
        onMouseDown={keepFocus}
        onClick={() => { void press() }}
      >
        <MoonStars size={14} weight={mode === 'on' ? 'fill' : 'regular'} />
        {mode === 'on' && <span className="text-xs leading-none">{t('unattended.on_label')}</span>}
      </button>
    </div>
  )
}
