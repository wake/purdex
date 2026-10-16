// spa/src/components/journal/ConfirmDetail.tsx — the detail of one 待確認: the original question as written in a scrollable block, the
// per-item state below it (read-only: Haiku ticks them). It opens to the left of the journal card, in the roadmap's place
// (CardStack); Esc closes it (J-2c, spec §11).
import { useEffect } from 'react'
import { CheckSquare, Square } from '@phosphor-icons/react'
import { KindChip } from './KindChip'
import { PersonMark } from './PersonMark'
import { answered } from '../../lib/journal/filters'
import { dayKey, timeOf } from '../../lib/journal/stream'
import { useI18nStore } from '../../stores/useI18nStore'
import type { JournalEntry } from '../../lib/journal/types'

interface Props { entry: JournalEntry; personName: string; onClose: () => void }

/** Esc typed into the terminal (vim, a prompt) or into any text field belongs to that field, not to the detail. */
const OWN_ESC = '.xterm, input, textarea, select, [contenteditable=""], [contenteditable="true"]'
const isOwnEsc = (target: EventTarget | null): boolean => target instanceof Element && target.closest(OWN_ESC) !== null

export function ConfirmDetail({ entry, personName, onClose }: Props) {
  const t = useI18nStore((s) => s.t)
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape' && !e.defaultPrevented && !isOwnEsc(e.target)) onClose() }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [onClose])
  const { done, total } = answered(entry)
  const resolved = total > 0 && done === total
  return (
    <div data-testid="journal-detail" className="h-full flex flex-col">
      <div className="px-3 py-2 border-b border-border-subtle shrink-0 flex items-center gap-1.5 flex-wrap text-[11px] text-text-muted">
        <KindChip kind={entry.kind} />
        <span className="tabular-nums">{dayKey(entry.at)} {timeOf(entry.at)}</span>
        <span className="text-text-secondary"><PersonMark text={entry.actor} name={personName} /></span>
        <span data-testid="journal-detail-state" className={`px-1 rounded ${resolved ? 'bg-surface-hover' : 'bg-status-warning/20 text-status-warning-text'}`}>
          {t(resolved ? 'journal.detail.resolved' : entry.pinned ? 'journal.detail.waitingPinned' : 'journal.detail.waiting')}
        </span>
      </div>
      <div className="flex-1 min-h-0 flex flex-col px-3 py-2.5 gap-1">
        <div className="text-[10.5px] text-text-muted shrink-0">{t('journal.detail.original')}</div>
        <pre data-testid="journal-detail-text" className="flex-1 min-h-0 max-h-[50vh] overflow-y-auto whitespace-pre-wrap break-words font-sans text-[12px] leading-[19px] text-text-primary rounded border border-border-subtle bg-surface-secondary/60 p-2.5">
          {entry.detail || t('journal.detail.noText')}
        </pre>
        <div className="text-[10.5px] text-text-muted mt-2 shrink-0">{t('journal.detail.items')}</div>
        <ul data-testid="journal-detail-items" className="space-y-1 shrink-0 max-h-[40%] overflow-y-auto">
          {entry.items.map((it, i) => (
            <li key={i} data-done={it.done || undefined} className="flex items-start gap-1.5 leading-[17px]">
              {it.done
                ? <CheckSquare size={14} weight="fill" className="shrink-0 text-status-success" />
                : <Square size={14} className="shrink-0 text-status-warning-text" />}
              <span className={it.done ? 'text-text-muted line-through' : ''}>{it.text}</span>
            </li>
          ))}
        </ul>
      </div>
    </div>
  )
}
