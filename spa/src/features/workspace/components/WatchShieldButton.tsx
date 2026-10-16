import { useMemo } from 'react'
import { ShieldCheck } from '@phosphor-icons/react'
import { useI18nStore } from '../../../stores/useI18nStore'
import { useWatchStatusStore, deriveGuardian } from '../../../lib/watch/status-store'
import { useWorkspaceStore } from '../store'
import { WATCH_WORKSPACE_ID } from '../../../lib/watch/ids'
import { openWatchArea } from '../lib/open-watch-area'

/**
 * The shield beside the Home button (plan T-a3.3): whether a guardian is there, in two looks. Outline and muted when there is
 * none or its status cannot be read; filled and green when one is awake or resting. A click opens the watcher area on the
 * guardian tab. Not in the narrow bar (no HomeRow there): its host cells are the way in.
 */
export function WatchShieldButton() {
  const t = useI18nStore((s) => s.t)
  const byHost = useWatchStatusStore((s) => s.byHost)
  const active = useWorkspaceStore((s) => s.activeWorkspaceId === WATCH_WORKSPACE_ID)
  const { state } = useMemo(() => deriveGuardian(byHost), [byHost])
  const present = state === 'awake' || state === 'resting'

  return (
    <button
      type="button"
      data-testid="watch-shield"
      data-look={present ? 'filled' : 'outline'}
      aria-pressed={active}
      aria-label={t(`watch.shield.${state}`)}
      title={t(`watch.shield.${state}`)}
      onClick={() => openWatchArea({ tab: 'guardian' })}
      className={`mr-1 p-0.5 rounded hover:bg-surface-secondary cursor-pointer focus:outline-none focus-visible:ring-1 focus-visible:ring-border-active ${
        present ? 'text-status-success-text' : 'text-text-muted'
      } ${active ? 'bg-surface-hover' : ''}`}
    >
      <ShieldCheck size={15} weight={present ? 'fill' : 'regular'} />
    </button>
  )
}
