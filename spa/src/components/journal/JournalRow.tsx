// spa/src/components/journal/JournalRow.tsx — one entry of the stream or of the 待確認 band (J-2c, spec §11): time, kind chip,
// actor (`A → B`), text, refs; the person's name highlighted in actor and text. A 待確認 row shows its checklist (Haiku ticks it; an
// answered item is struck through and checked; in the band the boxes are also clickable with journal.confirm.v1, JV-3), has NO coloured
// left edge (the yellow is the chip and the boxes only) and opens its detail through a button covering the row.
import { memo, useCallback, useEffect, useRef } from 'react'
import { ItemCheck } from './ItemCheck'
import { KindChip } from './KindChip'
import { PersonMark } from './PersonMark'
import { RefChips } from './RefChips'
import { answered } from '../../lib/journal/filters'
import { timeOf } from '../../lib/journal/stream'
import { taskKeyOfEntry, type TaskHover } from '../../lib/journal/tasks'
import { useI18nStore } from '../../stores/useI18nStore'
import { useJournalHoverStore } from '../../stores/useJournalHoverStore'
import type { JournalEntry } from '../../lib/journal/types'

/** How much of the row's text the open-details button's label carries. */
const OPEN_LABEL_CHARS = 40

export interface RowContext {
  personName: string
  remoteUrl: string
  /** The 待確認 whose detail is open. */
  selectedId: string | null
  onToggleDetail: (entryId: string) => void
  /** Check / uncheck item `idx` of a 待確認 by hand (the band's rows); undefined = read-only (no journal.confirm.v1). */
  onToggleItem?: (entry: JournalEntry, idx: number, done: boolean) => void
}

// Memoised: a month of rows is a thousand of them, and a parent render (a new unit object, a filter) must not redraw rows whose entry
// and context did not change. The hover link is not in `ctx`: a task_new / task_done row subscribes to it itself (below).
export const JournalRow = memo(function JournalRow({ entry, compact, ctx }: { entry: JournalEntry; compact?: boolean; ctx: RowContext }) {
  const t = useI18nStore((s) => s.t)
  const confirm = entry.kind === 'confirm'
  const { done, total } = answered(entry)
  const resolved = confirm && total > 0 && done === total
  const open = () => ctx.onToggleDetail(entry.id)
  const taskKey = taskKeyOfEntry(entry)
  // Lit while the pointer is on its task (not while it is on this row: the row has its own hover look). The selector returns a boolean, so
  // a row re-renders only when ITS link flips; a row that is not a task row is always false and never re-renders for the pointer.
  const linked = useJournalHoverStore((s) => taskKey !== null && s.hover?.from === 'task' && s.hover.key === taskKey)
  // What this row said the pointer is on, so that if the row goes away under the pointer (no mouseleave ever fires) it can take it back.
  const said = useRef<TaskHover | null>(null)
  const onEnter = useCallback(() => {
    if (taskKey === null) return
    said.current = { key: taskKey, from: 'row' }
    useJournalHoverStore.getState().setHover(said.current)
  }, [taskKey])
  const onLeave = useCallback(() => {
    said.current = null
    useJournalHoverStore.getState().setHover(null)
  }, [])
  useEffect(() => () => {
    const st = useJournalHoverStore.getState()
    if (said.current !== null && st.hover === said.current) st.setHover(null)
  }, [])
  // Every 待確認 row has one of these buttons: the label carries the row's own text, so a screen reader can tell them apart.
  const openLabel = t('journal.row.openDetail', { text: entry.text.length > OPEN_LABEL_CHARS ? `${entry.text.slice(0, OPEN_LABEL_CHARS)}…` : entry.text })
  return (
    <div
      data-testid="journal-entry"
      data-entry-id={entry.id}
      data-kind={entry.kind}
      data-selected={ctx.selectedId === entry.id || undefined}
      data-linked={linked || undefined}
      onMouseEnter={taskKey !== null ? onEnter : undefined}
      onMouseLeave={taskKey !== null ? onLeave : undefined}
      className={`group relative flex gap-2 py-1.5 px-1 -mx-1 rounded border-b border-border-subtle/50 last:border-b-0 ${ctx.selectedId === entry.id ? 'bg-surface-hover ring-1 ring-border-active' : ''}${linked ? ' bg-accent-muted ring-1 ring-accent' : ''}`}
    >
      {confirm && (
        // The row is a plain container; this button covers it (under the content, which lets clicks through except on its own
        // links and the checkboxes), so a button never sits inside another button.
        <button
          type="button"
          data-testid="journal-row-open"
          aria-label={openLabel}
          title={openLabel}
          onClick={open}
          className="absolute inset-0 rounded cursor-pointer hover:bg-surface-hover focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-border-active"
        />
      )}
      <span className={`relative w-9 shrink-0 tabular-nums text-[10.5px] text-text-muted pt-0.5 ${confirm ? 'pointer-events-none' : ''}`}>{timeOf(entry.at)}</span>
      <div className={`relative min-w-0 flex-1 pr-1 ${confirm ? 'pointer-events-none' : ''}`}>
        <div className="flex items-center gap-1.5 flex-wrap">
          <KindChip kind={entry.kind} />
          <span className="text-[10.5px] text-text-secondary break-words min-w-0"><PersonMark text={entry.actor} name={ctx.personName} /></span>
          {resolved && <span className="text-[10px] px-1 rounded bg-surface-hover text-text-muted">{t('journal.row.resolved')}</span>}
        </div>
        <div className={`mt-0.5 leading-[17px] break-words ${compact ? 'line-clamp-2' : ''} ${resolved ? 'text-text-secondary' : 'text-text-primary'}`}>
          <PersonMark text={entry.text} name={ctx.personName} />
        </div>
        {confirm && compact && total > 0 && (
          <div data-testid="journal-answered" className="mt-0.5 text-[11px] text-journal-warning-text">{t('journal.band.answered', { done, total })}</div>
        )}
        {confirm && total > 0 && (
          // The band's rows (compact) are where the person ticks; a stream row's list is for reading. The boxes take clicks (pointer-events-auto) over the row's open-detail button.
          <ul data-testid="journal-checklist" className="mt-1 space-y-0.5">
            {entry.items.map((it, i) => (
              <li key={i} data-done={it.done || undefined} className="flex items-start gap-1.5 leading-[16px]">
                <ItemCheck done={it.done} size={13} onToggle={compact && ctx.onToggleItem ? (d) => ctx.onToggleItem?.(entry, i, d) : undefined} />
                <span className={it.done ? 'text-text-muted line-through' : 'text-text-primary'}>{it.text}</span>
              </li>
            ))}
          </ul>
        )}
        {!compact && <div className="pointer-events-auto"><RefChips refs={entry.refs} remoteUrl={ctx.remoteUrl} /></div>}
      </div>
    </div>
  )
})
