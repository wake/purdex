// spa/src/components/journal/JournalStream.tsx — the event stream of the journal card: oldest at the top, newest at the bottom, a
// date divider per local day, the 「↓ N 則新事件」 pill while the reader is away from the bottom (J-2c, spec §11).
import { ArrowDown } from '@phosphor-icons/react'
import { JournalRow, type RowContext } from './JournalRow'
import { useStreamScroll } from './useStreamScroll'
import { withDays } from '../../lib/journal/stream'
import { useI18nStore } from '../../stores/useI18nStore'
import { useMemo } from 'react'
import type { JournalEntry } from '../../lib/journal/types'

interface Props {
  host: string | null
  unitKey: string | null
  /** Names the unit the entries belong to; defaults to `host\0unitKey`. A change is a different list, not new events. */
  streamKey?: string
  /** The entries to show (already filtered), oldest first. Keep the array stable between renders. */
  entries: readonly JournalEntry[]
  filter: string
  /** An older month can be loaded. */
  more: boolean
  onLoadOlder: () => Promise<unknown>
  ctx: RowContext
}

export function JournalStream({ host, unitKey, streamKey, entries, filter, more, onLoadOlder, ctx }: Props) {
  const t = useI18nStore((s) => s.t)
  const { ref, below, onScroll, toBottom, older } = useStreamScroll({ host, unitKey, streamKey, shown: entries, filter, more, loadOlder: onLoadOlder })
  const items = useMemo(() => withDays(entries), [entries])
  return (
    <div className="relative flex-1 min-h-0">
      <div ref={ref} onScroll={onScroll} data-testid="journal-stream" className="absolute inset-0 overflow-y-auto px-3 py-2">
        {more && (
          <button type="button" data-testid="journal-load-older" onClick={older} className="block mx-auto mb-1 px-2 py-0.5 rounded text-[10.5px] text-text-muted hover:text-text-primary hover:bg-surface-hover cursor-pointer">
            {t('journal.stream.older')}
          </button>
        )}
        {items.length === 0 && <div data-testid="journal-empty" className="text-text-muted py-6 text-center">{t(filter === 'all' ? 'journal.card.empty' : 'journal.stream.noMatch')}</div>}
        {items.map((it) => it.type === 'day'
          ? (
            <div key={`day-${it.day}`} data-testid="journal-day" className="flex items-center gap-2 my-2 text-[10.5px] text-text-muted select-none">
              <span className="flex-1 h-px bg-border-subtle" />{it.day}<span className="flex-1 h-px bg-border-subtle" />
            </div>
          )
          : <JournalRow key={it.entry.id} entry={it.entry} ctx={ctx} />)}
      </div>
      {below > 0 && (
        <button
          type="button"
          data-testid="journal-new-pill"
          onClick={toBottom}
          className="absolute left-1/2 -translate-x-1/2 bottom-3 flex items-center gap-1 px-3 py-1 rounded-full bg-accent text-text-inverse shadow-lg text-[11px] cursor-pointer"
        >
          <ArrowDown size={11} />{t('journal.stream.newEvents', { count: below })}
        </button>
      )}
    </div>
  )
}
