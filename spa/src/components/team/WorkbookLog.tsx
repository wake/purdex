// spa/src/components/team/WorkbookLog.tsx — the 紀錄: entries grouped by thing, the most recent thing first; a finished
// thing is collapsed until opened (WA-2b-2). Reads what it is given; 「更多」 asks the store for the next page.
import { CaretDown, CaretRight } from '@phosphor-icons/react'
import { useI18nStore } from '../../stores/useI18nStore'
import { useWorkbookStore, type ConvState } from '../../stores/useWorkbookStore'
import { groupEntries } from '../../lib/workbook/view-model'
import { WorkbookEntryRow } from './WorkbookEntryRow'

interface Props {
  hostId: string
  convKey: string | null
  conv: ConvState | undefined
  openGroups: readonly string[]
  onToggleGroup: (key: string) => void
  /** The entry a jump from the done record points at: its group opens, the row is marked. */
  highlightId: number | null
}

export function WorkbookLog({ hostId, convKey, conv, openGroups, onToggleGroup, highlightId }: Props) {
  const t = useI18nStore((s) => s.t)
  const groups = groupEntries(conv?.entries ?? [])
  const more = !!conv && convKey !== null && !conv.exhausted && conv.oldestId !== null
  return (
    <section data-testid="workbook-log">
      <div className="text-[10px] text-text-muted mb-0.5">{t('team.workbook.entries')}</div>
      {groups.length === 0 ? (
        <div className="text-text-secondary">{conv?.loading ? t('team.workbook.loading') : t('team.workbook.no_entries')}</div>
      ) : (
        <div className="flex flex-col gap-1.5">
          {groups.map((g) => {
            const collapsible = g.thing !== '' && g.done
            const open = !collapsible || openGroups.includes(g.key) || (highlightId !== null && g.entries.some((e) => e.id === highlightId))
            return (
              <div key={g.key} data-testid="workbook-group" data-thing={g.thing} data-collapsed={open ? undefined : 'true'}>
                {g.thing !== '' && (
                  <button
                    type="button"
                    disabled={!collapsible}
                    aria-expanded={open}
                    onMouseDown={(e) => e.preventDefault()}
                    onClick={() => onToggleGroup(g.key)}
                    className={`flex items-center gap-1 w-full text-left font-medium ${collapsible ? 'cursor-pointer hover:text-text-primary' : 'cursor-default'}`}
                  >
                    {collapsible ? (open ? <CaretDown size={10} /> : <CaretRight size={10} />) : <span className="w-[10px]" />}
                    <span className="truncate min-w-0 flex-1" title={g.thing}>{g.thing}</span>
                    <span className="text-[10px] font-normal text-text-muted flex-shrink-0">{g.done ? t('team.workbook.thing_done') : t('team.workbook.thing_open')}</span>
                  </button>
                )}
                {open && (
                  <ul className="flex flex-col gap-0.5 pl-3.5">
                    {g.entries.map((e) => <WorkbookEntryRow key={e.id} entry={e} highlighted={e.id === highlightId} />)}
                  </ul>
                )}
              </div>
            )
          })}
        </div>
      )}
      {more && (
        <button
          type="button"
          disabled={conv.loading}
          onMouseDown={(e) => e.preventDefault()}
          onClick={() => { void useWorkbookStore.getState().loadMore(hostId, convKey) }}
          className="mt-1.5 text-[11px] text-text-secondary hover:text-text-primary cursor-pointer disabled:opacity-50"
        >
          {t('team.workbook.more')}
        </button>
      )}
    </section>
  )
}
