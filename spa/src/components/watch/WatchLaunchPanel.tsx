// The watcher's launch panel (plan §1, T-a3.4 / T-a3.5): what the guardian tab shows where a session tab would show its launch /
// rebuild panel. It has the look of the stock `LaunchPanel` (same block, spacing, type sizes and theme tokens) and lists what
// would run — host, tmux session, working directory, command and, resting, the conversation it resumes. W5e adds the button:
// 啟動守門員 when no watcher is registered, 叫醒守門員 when it rests. Pressed, never run on opening; the busy state is
// `lib/watch/launch.ts`'s (it must outlive this panel's unmount).
import { useEffect, useMemo, useState } from 'react'
import { useI18nStore } from '../../stores/useI18nStore'
import { useHostLook, useHostLookResolver } from '../../lib/host-look'
import { useHostStore } from '../../stores/useHostStore'
import { formatHandoffTime } from '../../lib/relay/handoff-time'
import { isLaunchBusy, requestLaunch, startTarget, useWatchLaunchStore, type LaunchSlot } from '../../lib/watch/launch'
import { useWatchStatusStore, deriveGuardian } from '../../lib/watch/status-store'
import { readWatchRow, type WatchRow } from '../../lib/watch/watch-row'

interface Props {
  title: string
  hint?: string
}

function Row({ testId, label, value }: { testId: string; label: string; value: string }) {
  return (
    <p data-testid={testId} className="flex max-w-full items-baseline gap-2">
      <span className="shrink-0 text-xs text-text-muted">{label}</span>
      <span className="min-w-0 truncate font-mono text-xs">{value}</span>
    </p>
  )
}

const btn = 'cursor-pointer rounded border border-border-subtle px-3 py-1 hover:bg-surface-secondary disabled:cursor-not-allowed disabled:opacity-50'

export function WatchLaunchPanel({ title, hint }: Props) {
  const t = useI18nStore((s) => s.t)
  const byHost = useWatchStatusStore((s) => s.byHost)
  const g = useMemo(() => deriveGuardian(byHost), [byHost])
  const look = useHostLook(g.appHostId ?? null)

  // The row is read when the holder changes (and not on every status read).
  const holder = g.appHostId
  const [read, setRead] = useState<{ host: string; row: WatchRow | null } | null>(null)
  useEffect(() => {
    if (!holder) return
    let live = true
    void readWatchRow(holder).then((row) => { if (live) setRead({ host: holder, row }) })
    return () => { live = false }
  }, [holder])
  const row = holder && read?.host === holder ? read.row : null

  // The button: a start goes to any reachable watch host, a wake to the host that holds the resting watcher.
  const slot = useWatchLaunchStore((s) => s.slot)
  const hosts = useHostStore((s) => s.hosts)
  const hostOrder = useHostStore((s) => s.hostOrder)
  const resolveLook = useHostLookResolver()
  const kind = g.state === 'none' ? 'start' : 'wake'
  const target = g.state === 'none'
    ? startTarget(byHost, hostOrder)
    : g.state === 'resting' && g.reachable ? g.appHostId : undefined
  const busy = isLaunchBusy(slot)

  const note = (s: LaunchSlot): { text: string; alert: boolean } | null => {
    switch (s.phase) {
      case 'sending': return null
      case 'busy': {
        const on = s.startingOn
        if (!on) return null
        const app = on.daemonId === '' ? undefined : Object.values(hosts).find((h) => h.daemonId === on.daemonId)
        return { text: t('watch.launch.starting_on', { host: (app && resolveLook(app.id).name) ?? on.alias }), alert: false }
      }
      case 'timeout': return { text: t('watch.launch.timeout'), alert: true }
      case 'awake_already': return { text: t('watch.launch.awake_already'), alert: false }
      case 'cooldown': {
        const time = Number.isNaN(s.until) ? '' : formatHandoffTime(s.until, s.at)
        const key = s.kind === 'start' ? 'watch.launch.cooldown_start' : 'watch.launch.cooldown_wake'
        return { text: time === '' ? t('watch.launch.cooldown_unknown') : t(key, { time }), alert: true }
      }
      case 'error':
        return { text: s.detail ?? (s.network ? t('watch.launch.err_network') : t('watch.launch.err_generic', { status: s.status ?? 0 })), alert: true }
    }
  }
  const shown = slot ? note(slot) : null

  return (
    <div data-testid="watch-launch-panel" className="flex h-full items-center justify-center p-6 text-sm text-text-secondary">
      <div className="flex max-w-full flex-col items-start gap-3 text-left">
        <p data-testid="watch-launch-title" className="text-text-primary">{title}</p>
        {hint && <p data-testid="watch-launch-hint" className="text-xs text-text-muted">{hint}</p>}
        {holder && (
          <>
            <Row testId="watch-launch-host" label={t('watch.launch.host')} value={look.name ?? g.alias ?? holder} />
            <Row testId="watch-launch-session" label={t('watch.launch.session')} value={g.tmuxSession} />
            {row?.workdir && <Row testId="watch-launch-workdir" label={t('watch.launch.workdir')} value={row.workdir} />}
            {row?.agentCommand && <Row testId="watch-launch-command" label={t('watch.launch.command')} value={row.agentCommand} />}
            {g.state === 'resting' && g.sessionId && (
              <Row testId="watch-launch-conversation" label={t('watch.launch.conversation')} value={g.sessionId} />
            )}
          </>
        )}
        {target && (
          <button
            type="button"
            data-testid={kind === 'start' ? 'watch-launch-start' : 'watch-launch-wake'}
            disabled={busy}
            onClick={() => void requestLaunch(kind, target)}
            className={btn}
          >
            {busy
              ? t(slot?.kind === 'start' ? 'watch.launch.starting' : 'watch.launch.waking')
              : t(kind === 'start' ? 'watch.launch.start' : 'watch.launch.wake')}
          </button>
        )}
        {shown && (
          <p data-testid="watch-launch-note" role={shown.alert ? 'alert' : 'status'} className={`text-xs ${shown.alert ? 'text-status-error' : 'text-text-muted'}`}>{shown.text}</p>
        )}
      </div>
    </div>
  )
}
