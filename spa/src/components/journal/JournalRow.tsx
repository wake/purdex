// spa/src/components/journal/JournalRow.tsx — one entry of the stream or of the pinned band (J-2c, spec §11): time, kind chip,
// actor (`A → B`), text, refs; the person's name highlighted in actor and text. A 待確認 row shows its checklist read-only (Haiku
// ticks it; an answered item is struck through and checked), has NO coloured left edge (the yellow is the chip and the boxes only)
// and opens its detail through a button covering the row. The pin button is always in the DOM but only visible on hover (or while pinned).
import { CheckSquare, PushPin, Square } from '@phosphor-icons/react'
import { KindChip } from './KindChip'
import { PersonMark } from './PersonMark'
import { RefChips } from './RefChips'
import { answered } from '../../lib/journal/filters'
import { timeOf } from '../../lib/journal/stream'
import { useI18nStore } from '../../stores/useI18nStore'
import { keepFocus } from '../../lib/keep-focus'
import type { JournalEntry } from '../../lib/journal/types'

/** How much of the row's text the open-details button's label carries. */
const OPEN_LABEL_CHARS = 40

export interface RowContext {
  personName: string
  remoteUrl: string
  /** The 待確認 whose detail is open. */
  selectedId: string | null
  onToggleDetail: (entryId: string) => void
  onTogglePin: (entry: JournalEntry) => void
}

export function JournalRow({ entry, compact, ctx }: { entry: JournalEntry; compact?: boolean; ctx: RowContext }) {
  const t = useI18nStore((s) => s.t)
  const confirm = entry.kind === 'confirm'
  const { done, total } = answered(entry)
  const resolved = confirm && total > 0 && done === total
  const label = t(entry.pinned ? 'journal.pin.unpin' : 'journal.pin.pin')
  const open = () => ctx.onToggleDetail(entry.id)
  // Every 待確認 row has one of these buttons: the label carries the row's own text, so a screen reader can tell them apart.
  const openLabel = t('journal.row.openDetail', { text: entry.text.length > OPEN_LABEL_CHARS ? `${entry.text.slice(0, OPEN_LABEL_CHARS)}…` : entry.text })
  return (
    <div
      data-testid="journal-entry"
      data-entry-id={entry.id}
      data-kind={entry.kind}
      data-selected={ctx.selectedId === entry.id || undefined}
      className={`group relative flex gap-2 py-1.5 px-1 -mx-1 rounded border-b border-border-subtle/50 last:border-b-0 ${ctx.selectedId === entry.id ? 'bg-surface-hover ring-1 ring-border-active' : ''}`}
    >
      {confirm && (
        // The row is a plain container; this button covers it (under the content, which lets clicks through except on its own
        // links and the pin button), so a button never sits inside another button.
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
      <div className={`relative min-w-0 flex-1 pr-5 ${confirm ? 'pointer-events-none' : ''}`}>
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
        {confirm && !compact && total > 0 && (
          <ul data-testid="journal-checklist" className="mt-1 space-y-0.5">
            {entry.items.map((it, i) => (
              <li key={i} data-done={it.done || undefined} className="flex items-start gap-1.5 leading-[16px]">
                {it.done
                  ? <CheckSquare size={13} weight="fill" className="shrink-0 mt-px text-status-success-text" />
                  : <Square size={13} className="shrink-0 mt-px text-journal-warning-text" />}
                <span className={it.done ? 'text-text-muted line-through' : 'text-text-primary'}>{it.text}</span>
              </li>
            ))}
          </ul>
        )}
        {!compact && <div className="pointer-events-auto"><RefChips refs={entry.refs} remoteUrl={ctx.remoteUrl} /></div>}
      </div>
      <button
        type="button"
        data-testid="journal-pin-toggle"
        aria-label={label}
        aria-pressed={entry.pinned}
        title={label}
        onMouseDown={keepFocus}
        onClick={() => ctx.onTogglePin(entry)}
        className={`absolute top-1 right-0.5 p-0.5 rounded cursor-pointer hover:bg-surface-hover focus:opacity-100 ${entry.pinned ? 'text-text-primary' : 'text-text-muted opacity-0 group-hover:opacity-100'}`}
      >
        <PushPin size={12} weight={entry.pinned ? 'fill' : 'regular'} />
      </button>
    </div>
  )
}
