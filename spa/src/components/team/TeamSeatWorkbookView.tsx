// spa/src/components/team/TeamSeatWorkbookView.tsx — the placeholder workbook view a team panel drills into (WA-2b-1; the
// real view is WA-2b-2): back control, title, the status in one block and the latest few entries as plain text. Reads only
// the workbook store (through `useWorkbookViewing`); which seat it shows lives in `useTeamUiStore.teamDrill`, so the view
// survives the panel unmounting.
import { CaretLeft } from '@phosphor-icons/react'
import { useI18nStore } from '../../stores/useI18nStore'
import { useTeamUiStore } from '../../stores/useTeamUiStore'
import { useWorkbookViewing } from './seat-workbook'

/** How many of the newest entries the placeholder lists. */
export const VIEW_ENTRIES = 5

interface Props { teamKey: string; hostId: string; sessionId: string; title: string }

export function TeamSeatWorkbookView({ teamKey, hostId, sessionId, title }: Props) {
  const t = useI18nStore((s) => s.t)
  const wb = useWorkbookViewing(hostId, sessionId)
  const status = wb.conv?.status.trim() ?? ''
  const entries = (wb.conv?.entries ?? []).filter((e) => e.state !== 'skipped').slice(0, VIEW_ENTRIES)
  const back = t('team.panel.workbook_back')
  return (
    <div data-testid="team-seat-workbook" className="text-xs text-text-primary">
      <div className="flex items-center gap-1.5 px-2 h-8 border-b border-border-subtle">
        <button
          type="button"
          onMouseDown={(e) => e.preventDefault()}
          onClick={() => useTeamUiStore.getState().setTeamDrill(teamKey, null)}
          title={back}
          aria-label={back}
          className="px-1 py-0.5 rounded text-text-secondary hover:text-text-primary hover:bg-surface-hover cursor-pointer flex-shrink-0"
        >
          <CaretLeft size={12} />
        </button>
        <span className="truncate min-w-0 font-medium" title={t('team.workbook.title', { title })}>{t('team.workbook.title', { title })}</span>
      </div>
      <div className="px-3 py-2 flex flex-col gap-2">
        <section>
          <div className="text-[10px] text-text-muted mb-0.5">{t('team.workbook.status')}</div>
          <div data-testid="team-seat-workbook-status" className="whitespace-pre-wrap break-words">{status !== '' ? status : t('team.workbook.no_status')}</div>
        </section>
        <section>
          <div className="text-[10px] text-text-muted mb-0.5">{t('team.workbook.entries')}</div>
          {entries.length === 0 ? (
            <div className="text-text-secondary">{wb.conv?.loading ? t('team.workbook.loading') : t('team.workbook.no_entries')}</div>
          ) : (
            <ul className="flex flex-col gap-1">
              {entries.map((e) => (
                <li key={e.id} data-testid="team-seat-workbook-entry" className="text-text-secondary break-words">{e.entry !== '' ? e.entry : e.thing}</li>
              ))}
            </ul>
          )}
        </section>
      </div>
    </div>
  )
}
