import { ArrowsClockwise } from '@phosphor-icons/react'
import { useResumeTemplateLookup } from '../../../lib/resume-templates'
import { resolveResumeCommand } from '../../../lib/rebuild/composer'
import { recordsDisagree, type BatchGroupDraft } from '../../../lib/rebuild/batch'
import { recordHealth, type BatchCandidate, type RecordRow } from '../../../lib/rebuild/eligibility'
import { HealthBadge, livenessOf, type HostLive, type TFn } from './shared'

/**
 * The per-tab rebuild records (spec §4.11): one row per terminal tmux pane,
 * the four-state health indicator the page already uses, "Rebuild all" over
 * the grouped dead ones, and — separately — the panes whose generation is
 * unknown, which the batch deliberately refuses to group by code alone.
 * Rendered on a host page, so every row belongs to that host (no host column).
 */
export function RebuildRecordsBlock({
  rows,
  groups,
  excluded,
  liveByHost,
  busy,
  lockedOut,
  onRebuildAll,
  onRebuildOne,
  t,
}: {
  rows: RecordRow[]
  groups: BatchGroupDraft[]
  excluded: BatchCandidate[]
  liveByHost: Record<string, HostLive>
  busy: boolean
  lockedOut: (owner: string) => boolean
  onRebuildAll: () => void
  onRebuildOne: (pane: BatchCandidate) => void
  t: TFn
}) {
  // Only the groups whose members disagree need naming — saying "using p1"
  // when there is nothing to choose between is noise.
  const conflicts = groups.filter((group) => {
    const members = group.paneIds
      .map((paneId) => rows.find((r) => r.paneId === paneId)?.record)
      .filter((record): record is NonNullable<typeof record> => !!record)
    return members.some((record) => recordsDisagree(record, group.record))
  })

  return (
    <div data-testid="rebuild-records-block" className="mt-6">
      <div className="flex items-center justify-between mb-2">
        <h3 className="text-sm text-text-primary">{t('rebuild.batch_title')}</h3>
        <button
          type="button"
          data-testid="record-rebuild-all-btn"
          onClick={onRebuildAll}
          disabled={busy || groups.length === 0}
          className="flex items-center gap-1.5 px-3 py-1.5 rounded-md border border-border-default text-text-secondary text-xs hover:text-text-primary hover:border-border-active disabled:opacity-50 disabled:cursor-not-allowed"
        >
          <ArrowsClockwise size={14} />
          {t('rebuild.batch_run')}
        </button>
      </div>

      {rows.length === 0 ? (
        <p data-testid="rebuild-records-none" className="text-xs text-text-muted">{t('rebuild.batch_none')}</p>
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
              {rows.map((row) => (
                <tr key={row.paneId} className="border-t border-border-default">
                  <td className="py-1 pr-3">{row.record?.sessionName || row.cachedName}</td>
                  <td className="py-1 pr-3 font-mono">{row.record?.cwd || '—'}</td>
                  <RecordCommandCell hostId={row.hostId} record={row.record} />
                  <td className="py-1">
                    <HealthBadge
                      health={recordHealth(row, livenessOf(liveByHost[row.hostId] ?? 'loading'))}
                      testId={`record-health-${row.paneId}`}
                      t={t}
                    />
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {conflicts.map((group) => (
        <p
          key={group.sourcePaneId}
          data-testid="batch-conflict-source"
          className="mt-2 text-[11px] text-status-warning"
        >
          {t('rebuild.batch_conflict_source', {
            name: group.record.sessionName,
            pane: group.sourcePaneId,
            cwd: group.record.cwd ?? '—',
          })}
        </p>
      ))}

      {excluded.length > 0 && (
        <div className="mt-4">
          <h4 className="text-xs text-text-primary">{t('rebuild.batch_needs_attention')}</h4>
          <p className="text-[11px] text-text-muted">{t('rebuild.batch_needs_attention_hint')}</p>
          <ul className="mt-1 flex flex-col gap-1">
            {excluded.map((pane) => (
              <li
                key={pane.paneId}
                data-testid={`record-attention-${pane.paneId}`}
                className="flex items-center justify-between gap-2 text-xs"
              >
                <span className="truncate font-mono text-text-secondary">
                  {pane.record.sessionName}
                </span>
                <button
                  type="button"
                  data-testid={`record-attention-rebuild-${pane.paneId}`}
                  onClick={() => onRebuildOne(pane)}
                  disabled={busy || lockedOut(`rebuild:${pane.paneId}`)}
                  className="shrink-0 rounded-md border border-border-default px-2.5 py-1 text-xs text-text-secondary hover:text-text-primary hover:border-border-active disabled:opacity-50 disabled:cursor-not-allowed"
                >
                  {t('rebuild.button')}
                </button>
              </li>
            ))}
          </ul>
        </div>
      )}
    </div>
  )
}

/**
 * One row's command, composed with that row's host's templates. Subscribed, so
 * the cell repaints when a template is edited — reading the lookup once would
 * render a stale command until a remount.
 */
function RecordCommandCell({ hostId, record }: { hostId: string; record: RecordRow['record'] }) {
  const templates = useResumeTemplateLookup(hostId)
  return <td className="py-1 pr-3 font-mono">{resolveResumeCommand(record, templates) || '—'}</td>
}
