import { ArrowsClockwise } from '@phosphor-icons/react'
import { EditableCwdCell } from '../EditableCwdCell'
import type { SessionMeta, WorkspaceSnapshot } from '../../../lib/snapshot/types'
import { HealthBadge, computeHealth, type HostLive, type TFn } from './shared'

/**
 * Captured tmux sessions. Receives the HOST-FILTERED snapshot (a host page
 * only ever shows its own host), so there is no host column.
 */
export function TmuxBlock({
  snap,
  liveByHost,
  busy,
  onRebuild,
  onCommitCwd,
  t,
}: {
  snap: WorkspaceSnapshot
  liveByHost: Record<string, HostLive>
  busy: boolean
  onRebuild: () => void
  onCommitCwd: (hostId: string, code: string, value: string) => void
  t: TFn
}) {
  const rows: SessionMeta[] = []
  for (const perHost of Object.values(snap.sessionMeta)) {
    for (const meta of Object.values(perHost)) rows.push(meta)
  }

  return (
    <div data-testid="snapshot-tmux-block" className="mt-6">
      <div className="flex items-center justify-between mb-2">
        <h3 className="text-sm text-text-primary">{t('settings.snapshot.tmux.title')}</h3>
        <button
          type="button"
          data-testid="snapshot-rebuild-btn"
          onClick={onRebuild}
          disabled={busy}
          className="flex items-center gap-1.5 px-3 py-1.5 rounded-md border border-border-default text-text-secondary text-xs hover:text-text-primary hover:border-border-active disabled:opacity-50 disabled:cursor-not-allowed"
        >
          <ArrowsClockwise size={14} />
          {t('settings.snapshot.tmux.rebuildAll')}
        </button>
      </div>

      {rows.length === 0 ? (
        <p className="text-xs text-text-muted">{t('settings.snapshot.tmux.none')}</p>
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full text-xs text-text-secondary">
            <thead>
              <tr className="text-text-muted text-left">
                <th className="py-1 pr-3 font-normal">{t('settings.snapshot.col.name')}</th>
                <th className="py-1 pr-3 font-normal">{t('settings.snapshot.col.cwd')}</th>
                <th className="py-1 pr-3 font-normal">{t('settings.snapshot.col.command')}</th>
                <th className="py-1 font-normal">{t('settings.snapshot.col.health')}</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((meta) => {
                const health = computeHealth(meta, liveByHost[meta.hostId] ?? 'loading')
                return (
                  <tr key={`${meta.hostId}:${meta.sessionCode}`} className="border-t border-border-default">
                    <td className="py-1 pr-3">{meta.name}</td>
                    <td className="py-1 pr-3 font-mono">
                      <EditableCwdCell
                        cwd={meta.cwd}
                        onCommit={(v) => onCommitCwd(meta.hostId, meta.sessionCode, v)}
                        disabled={busy}
                      />
                    </td>
                    <td className="py-1 pr-3 font-mono">{meta.currentCommand ?? '—'}</td>
                    <td className="py-1">
                      <HealthBadge
                        health={health}
                        testId={`snapshot-health-${meta.hostId}-${meta.sessionCode}`}
                        t={t}
                      />
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}
