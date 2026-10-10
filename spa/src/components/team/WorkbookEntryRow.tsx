// spa/src/components/team/WorkbookEntryRow.tsx — one entry of the 紀錄 (WA-2b-2): time, its text (or its state), and the todo
// changes it made (＋ added / ✓ done / － dropped).
import { useI18nStore } from '../../stores/useI18nStore'
import { entryTime, formatWhen, refreshCounts } from '../../lib/workbook/view-model'
import type { WorkbookEntry } from '../../lib/workbook/types'

type T = (key: string, params?: Record<string, string | number>) => string

function refreshLine(e: WorkbookEntry, t: T): string {
  const c = refreshCounts(e)
  const parts: string[] = []
  if (c.done > 0) parts.push(t('team.workbook.refresh_done', { n: c.done }))
  if (c.dropped > 0) parts.push(t('team.workbook.refresh_dropped', { n: c.dropped }))
  if (c.added > 0) parts.push(t('team.workbook.refresh_added', { n: c.added }))
  return t('team.workbook.refresh_text', { parts: parts.length ? parts.join(t('team.workbook.list_sep')) : t('team.workbook.refresh_none') })
}

function text(e: WorkbookEntry, t: T): { body: string; hover?: string } {
  if (e.state === 'pending') return { body: t('team.workbook.pending') }
  if (e.state === 'failed') return { body: t('team.workbook.failed'), hover: e.reason }
  if (e.kind === 'refresh') return { body: refreshLine(e, t) }
  return { body: e.entry !== '' ? e.entry : e.thing }
}

export function WorkbookEntryRow({ entry, highlighted }: { entry: WorkbookEntry; highlighted: boolean }) {
  const t = useI18nStore((s) => s.t) as T
  const { body, hover } = text(entry, t)
  const lines: [string, string][] = [
    ...entry.todoChanges.added.map((x): [string, string] => ['team.workbook.todo_added', x.title]),
    ...entry.todoChanges.done.map((x): [string, string] => ['team.workbook.todo_done', x.title]),
    ...entry.todoChanges.dropped.map((x): [string, string] => ['team.workbook.todo_dropped', x.title]),
  ]
  return (
    <li
      data-testid="team-seat-workbook-entry"
      data-entry-id={entry.id}
      data-highlighted={highlighted ? 'true' : undefined}
      className={`rounded px-1 py-0.5 text-text-secondary break-words ${highlighted ? 'bg-surface-hover ring-1 ring-border-default' : ''}`}
    >
      <div title={hover || undefined}>
        <span className="text-[10px] text-text-muted mr-1.5">{formatWhen(entryTime(entry))}</span>
        <span>{body}</span>
      </div>
      {lines.map(([key, title], i) => (
        <div key={i} className="pl-3 text-[11px] text-text-muted">{t(key, { title })}</div>
      ))}
    </li>
  )
}
