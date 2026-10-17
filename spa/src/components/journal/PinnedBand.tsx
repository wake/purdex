// spa/src/components/journal/PinnedBand.tsx — the pinned entries above the stream, newest first (J-2c, spec §11). A 待確認 shows
// 「已答 n／m 項」; nothing pinned, nothing drawn.
import { PushPin } from '@phosphor-icons/react'
import { JournalRow, type RowContext } from './JournalRow'
import { useI18nStore } from '../../stores/useI18nStore'
import type { JournalEntry } from '../../lib/journal/types'

export function PinnedBand({ pinned, ctx }: { pinned: readonly JournalEntry[]; ctx: RowContext }) {
  const t = useI18nStore((s) => s.t)
  if (pinned.length === 0) return null
  return (
    <div data-testid="journal-pinned-band" className="shrink-0 max-h-[34%] overflow-y-auto px-3 py-1.5 border-b border-border-default bg-surface-secondary/60">
      <div className="flex items-center gap-1 text-[10.5px] text-text-muted mb-0.5">
        <PushPin size={10} weight="fill" />{t('journal.band.title', { count: pinned.length })}
      </div>
      {pinned.map((e) => <JournalRow key={e.id} entry={e.pinned ? e : { ...e, pinned: true }} compact ctx={ctx} />)}
    </div>
  )
}
