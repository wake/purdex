// spa/src/components/journal/JournalFilters.tsx — the filter chips under the journal card's header (J-2c, spec Q-C). The chosen
// filter is the unit's view memo (it survives a tab switch); 待確認 carries the count of open items.
import { FILTER_IDS, nextFilter } from '../../lib/journal/filters'
import { patchUnitMemo, useViewMemo } from '../../lib/journal/use-view-memo'
import { useI18nStore } from '../../stores/useI18nStore'

interface Props { host: string | null; unitKey: string | null; openCount: number }

export function JournalFilters({ host, unitKey, openCount }: Props) {
  const t = useI18nStore((s) => s.t)
  const { filter } = useViewMemo(host, unitKey)
  return (
    <div className="flex flex-wrap gap-1 px-3 pb-2" role="group" aria-label={t('journal.filter.label')}>
      {FILTER_IDS.map((id) => {
        const on = filter === id
        return (
          <button
            key={id}
            type="button"
            data-testid={`journal-filter-${id}`}
            aria-pressed={on}
            onClick={() => patchUnitMemo(host, unitKey, { filter: nextFilter(filter, id) })}
            className={`px-2 py-0.5 rounded-full border text-[11px] cursor-pointer ${on ? 'border-accent text-text-primary bg-accent-muted' : 'border-border-default text-text-secondary hover:text-text-primary hover:bg-surface-hover'}`}
          >
            {t(`journal.filter.${id}`)}
            {id === 'confirm' && openCount > 0 && (
              <span data-testid="journal-filter-confirm-count" className="ml-1 px-1 rounded-full text-[10px] font-medium bg-status-warning/20 text-journal-warning-text tabular-nums">{openCount}</span>
            )}
          </button>
        )
      })}
    </div>
  )
}
