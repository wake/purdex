import { useMemo } from 'react'
import { ShieldCheck } from '@phosphor-icons/react'
import { useI18nStore } from '../../../stores/useI18nStore'
import { useWatchStatusStore, deriveGuardian } from '../../../lib/watch/status-store'
import { usePoolAlertCount } from '../../../lib/pool/alerts-store'
import { CountBadge } from '../../../components/fleet/CountBadge'
import { useWorkspaceStore } from '../store'
import { WATCH_WORKSPACE_ID } from '../../../lib/watch/ids'
import { openWatchArea } from '../lib/open-watch-area'

/**
 * The shield beside the Home button (plan T-a3.3): whether a guardian is there, in two looks. Outline and muted when there is
 * none or its status cannot be read; filled and green when one is awake or resting. A click opens the watcher area on the
 * guardian tab, or on 事件 while push-level alerts are active (M36, M40: the count badge says how many). Not in the narrow bar (no HomeRow there): its host cells are the way in.
 */
export function WatchShieldButton() {
  const t = useI18nStore((s) => s.t)
  const byHost = useWatchStatusStore((s) => s.byHost)
  const active = useWorkspaceStore((s) => s.activeWorkspaceId === WATCH_WORKSPACE_ID)
  const { state } = useMemo(() => deriveGuardian(byHost), [byHost])
  const present = state === 'awake' || state === 'resting'
  // M36: the active push-level alerts (plus what the guardian still has open, MON-11), not the old watch events.
  const waiting = usePoolAlertCount()

  const label = waiting > 0 ? `${t(`watch.shield.${state}`)} · ${t('watch.shield.events', { n: waiting })}` : t(`watch.shield.${state}`)

  return (
    <button
      type="button"
      data-testid="watch-shield"
      data-look={present ? 'filled' : 'outline'}
      aria-pressed={active}
      aria-label={label}
      title={label}
      onClick={() => openWatchArea({ tab: waiting > 0 ? 'events' : 'guardian' })}
      className={`relative mr-1 p-0.5 rounded hover:bg-surface-secondary cursor-pointer focus:outline-none focus-visible:ring-1 focus-visible:ring-border-active ${
        present ? 'text-status-success-text' : 'text-text-muted'
      } ${active ? 'bg-surface-hover' : ''}`}
    >
      <ShieldCheck size={15} weight={present ? 'fill' : 'regular'} />
      {waiting > 0 && <CountBadge n={waiting} className="bg-status-warning text-text-inverse" testId="watch-shield-count" />}
    </button>
  )
}
