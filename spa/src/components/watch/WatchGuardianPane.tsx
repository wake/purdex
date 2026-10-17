// The `watch-guardian` pane: the panel the guardian tab holds when there is no session to show (plan T-a3.4). While the
// holder's `pdx-watch` can be shown, the reconcile puts a real `tmux-session` here instead and this component is not used.
import { useMemo } from 'react'
import { useI18nStore } from '../../stores/useI18nStore'
import { useHostLook } from '../../lib/host-look'
import { useWatchStatusStore, deriveGuardian } from '../../lib/watch/status-store'
import { WatchLaunchPanel } from './WatchLaunchPanel'
import type { PaneRendererProps } from '../../lib/module-registry'

export function WatchGuardianPane(_props: PaneRendererProps) {
  const t = useI18nStore((s) => s.t)
  const byHost = useWatchStatusStore((s) => s.byHost)
  const g = useMemo(() => deriveGuardian(byHost), [byHost])
  const look = useHostLook(g.appHostId ?? null)

  if (g.state === 'unknown') return <WatchLaunchPanel title={t('watch.launch.unknown_title')} />
  if (g.state === 'none') return <WatchLaunchPanel title={t('watch.launch.none_title')} hint={t('watch.launch.none_hint')} />
  if (!g.reachable || !g.appHostId) {
    return <WatchLaunchPanel title={t('watch.launch.far_title', { alias: g.alias ?? g.hostId ?? '' })} />
  }
  return <WatchLaunchPanel title={t('watch.launch.no_session_title', { alias: look.name ?? g.alias ?? g.appHostId })} />
}
