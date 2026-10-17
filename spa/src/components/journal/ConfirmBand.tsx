// spa/src/components/journal/ConfirmBand.tsx — the open 待確認 above the stream, newest first, across every session of the unit (JV-3,
// spec §3.4 / U6). Each row shows its items with checkboxes (clickable with journal.confirm.v1); nothing open, nothing drawn.
import { Question } from '@phosphor-icons/react'
import { JournalRow, type RowContext } from './JournalRow'
import { useI18nStore } from '../../stores/useI18nStore'
import type { JournalEntry } from '../../lib/journal/types'

export function ConfirmBand({ entries, ctx }: { entries: readonly JournalEntry[]; ctx: RowContext }) {
  const t = useI18nStore((s) => s.t)
  if (entries.length === 0) return null
  return (
    <div data-testid="journal-confirm-band" className="shrink-0 max-h-[34%] overflow-y-auto px-3 py-1.5 border-b border-border-default bg-surface-secondary/60">
      <div className="flex items-center gap-1 text-[10.5px] text-text-muted mb-0.5">
        <Question size={10} weight="fill" />{t('journal.band.title', { count: entries.length })}
      </div>
      {entries.map((e) => <JournalRow key={e.id} entry={e} compact ctx={ctx} />)}
    </div>
  )
}
