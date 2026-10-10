// spa/src/components/team/TeamSeatWorkbookView.tsx — the workbook view (WA-2b-2): a team panel's drill-in and a tab's own
// conversation workbook share it. 目前狀況 + 紀錄 (v1); v2 adds 待辦, the 紀錄｜待辦 switch and 重整. Data comes only from the
// workbook store (through `useWorkbookViewing`, the one place that touches the view count); which seat a drill shows lives in
// `useTeamUiStore.teamDrill`, and the view's own state (tab, scroll, open groups) in lib/workbook/view-memory.ts.
import type { ReactNode } from 'react'
import { CaretLeft } from '@phosphor-icons/react'
import { useI18nStore } from '../../stores/useI18nStore'
import { useTeamUiStore } from '../../stores/useTeamUiStore'
import { useWorkbookViewing, type SeatWorkbook } from './seat-workbook'
import { useWorkbookViewState } from './useWorkbookViewState'
import { WorkbookStatus } from './WorkbookStatus'
import { WorkbookLog } from './WorkbookLog'

interface Props {
  /** The team whose drill this is: it gets the back control. Absent for a tab's own workbook (no drill, nothing to go back to). */
  teamKey?: string
  hostId: string
  sessionId: string
  title: string
  /** Controls at the header's right end (the own workbook's move-to-title-bar / enlarge). */
  trailing?: ReactNode
}

export function TeamSeatWorkbookView(props: Props) {
  const wb = useWorkbookViewing(props.hostId, props.sessionId)
  // Re-keyed when the conversation first becomes known, so the remembered view state is read for the right conversation.
  return <WorkbookFrame key={wb.convKey ?? ''} {...props} wb={wb} />
}

function WorkbookFrame({ teamKey, hostId, title, trailing, wb }: Props & { wb: SeatWorkbook }) {
  const t = useI18nStore((s) => s.t)
  const vs = useWorkbookViewState(hostId, wb.convKey)
  const conv = wb.conv
  const back = t('team.panel.workbook_back')
  const heading = teamKey === undefined ? t('team.workbook.own_title') : t('team.workbook.title', { title })
  return (
    <div data-testid="team-seat-workbook" className="text-xs text-text-primary">
      <div className="flex items-center gap-1.5 px-2 h-8 border-b border-border-subtle">
        {teamKey !== undefined && (
          <button
            type="button"
            data-testid="team-seat-workbook-back"
            onMouseDown={(e) => e.preventDefault()}
            onClick={() => useTeamUiStore.getState().setTeamDrill(teamKey, null)}
            title={back}
            aria-label={back}
            className="px-1 py-0.5 rounded text-text-secondary hover:text-text-primary hover:bg-surface-hover cursor-pointer flex-shrink-0"
          >
            <CaretLeft size={12} />
          </button>
        )}
        <span className="truncate min-w-0 flex-1 font-medium" title={heading}>{heading}</span>
        {trailing !== undefined && <span className="flex items-center gap-0.5 flex-shrink-0">{trailing}</span>}
      </div>
      <div ref={vs.scrollRef} onScroll={vs.onScroll} data-testid="workbook-body" className="px-3 py-2 flex flex-col gap-2 overflow-y-auto max-h-[70vh]">
        <WorkbookStatus status={conv?.status ?? ''} statusAt={conv?.statusAt ?? 0} loading={!!conv?.loading} />
        <WorkbookLog hostId={hostId} convKey={wb.convKey} conv={conv} openGroups={vs.openGroups} onToggleGroup={vs.toggleGroup} highlightId={null} />
      </div>
    </div>
  )
}
