// The watcher's launch panel (plan §1, T-a3.4 / T-a3.5): what the guardian tab shows where a session tab would show its launch /
// rebuild panel. It has the look of the stock `LaunchPanel` (same block, spacing, type sizes and theme tokens) and lists what
// would run — host, tmux session, working directory, command and, resting, the conversation it resumes. No button in W5a3;
// W5e adds 啟動守門員 and 叫醒守門員 here.
import { useEffect, useMemo, useState } from 'react'
import { useI18nStore } from '../../stores/useI18nStore'
import { useHostStore } from '../../stores/useHostStore'
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

export function WatchLaunchPanel({ title, hint }: Props) {
  const t = useI18nStore((s) => s.t)
  const byHost = useWatchStatusStore((s) => s.byHost)
  const g = useMemo(() => deriveGuardian(byHost), [byHost])
  const hostName = useHostStore((s) => (g.appHostId ? s.hosts[g.appHostId]?.name : undefined))

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

  return (
    <div data-testid="watch-launch-panel" className="flex h-full items-center justify-center p-6 text-sm text-text-secondary">
      <div className="flex max-w-full flex-col items-start gap-3 text-left">
        <p data-testid="watch-launch-title" className="text-text-primary">{title}</p>
        {hint && <p data-testid="watch-launch-hint" className="text-xs text-text-muted">{hint}</p>}
        {holder && (
          <>
            <Row testId="watch-launch-host" label={t('watch.launch.host')} value={hostName ?? g.alias ?? holder} />
            <Row testId="watch-launch-session" label={t('watch.launch.session')} value={g.tmuxSession} />
            {row?.workdir && <Row testId="watch-launch-workdir" label={t('watch.launch.workdir')} value={row.workdir} />}
            {row?.agentCommand && <Row testId="watch-launch-command" label={t('watch.launch.command')} value={row.agentCommand} />}
            {g.state === 'resting' && g.sessionId && (
              <Row testId="watch-launch-conversation" label={t('watch.launch.conversation')} value={g.sessionId} />
            )}
          </>
        )}
      </div>
    </div>
  )
}
