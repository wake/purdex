// spa/src/components/team/TeamSeatWorkbookView.tsx — the workbook view (WA-2b-2): a team panel's drill-in and a tab's own
// conversation workbook share it. 目前狀況 + 紀錄 (v1); v2 adds 待辦, the 紀錄｜待辦 switch and 重整. Data comes only from the
// workbook store (through `useWorkbookViewing`, the one place that touches the view count); which seat a drill shows lives in
// `useTeamUiStore.teamDrill`, and the view's own state (tab, scroll, open groups) in lib/workbook/view-memory.ts.
import { useEffect, useRef, useState, type ReactNode } from 'react'
import { CaretLeft } from '@phosphor-icons/react'
import { useI18nStore } from '../../stores/useI18nStore'
import { useTeamUiStore } from '../../stores/useTeamUiStore'
import { useWorkbookStore } from '../../stores/useWorkbookStore'
import type { WorkbookTab } from '../../lib/workbook/view-memory'
import type { WorkbookTodo } from '../../lib/workbook/types'
import { WorkbookToolbar } from './WorkbookToolbar'
import { OpenTodoSection, WorkbookTodos } from './WorkbookTodos'
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

function WorkbookFrame({ teamKey, hostId, sessionId, title, trailing, wb }: Props & { wb: SeatWorkbook }) {
  const t = useI18nStore((s) => s.t)
  const { tab: savedTab, setTab, openGroups, toggleGroup, bindBox, onScroll, find } = useWorkbookViewState(hostId, wb.convKey)
  const conv = wb.conv
  const v2 = useWorkbookStore((s) => s.support[hostId]?.v2 === true)
  const tab = v2 ? savedTab : 'log' // a v1 daemon has no 待辦 (and a remembered 待辦 does not outlive the capability)
  const [highlightId, setHighlightId] = useState<number | null>(null)
  const [notFound, setNotFound] = useState(false)
  // Navigation intent: every tab switch and every jump takes a new number, and a jump's answer lands only if its number is still
  // the current one (a later click, a tab switch, or the view going away all make it stale). A new conversation re-keys the frame.
  const intent = useRef(0)
  useEffect(() => () => { intent.current++ }, [])
  const switchTab = (next: WorkbookTab) => { intent.current++; setHighlightId(null); setNotFound(false); setTab(next) }
  /** A done todo → the entry that closed it: 紀錄, paged in if need be, scrolled to and marked; else say it is not there. */
  async function jump(todo: WorkbookTodo) {
    const id = todo.closedEntryId
    switchTab('log')
    if (wb.convKey === null || !(id > 0)) { setNotFound(true); return } // 0 = no closing entry (an open todo's)
    const mine = intent.current
    const ok = await useWorkbookStore.getState().loadUntil(hostId, wb.convKey, id)
    if (intent.current !== mine) return
    if (ok) setHighlightId(id)
    else setNotFound(true)
  }
  const entryCount = conv?.entries.length ?? 0
  useEffect(() => {
    if (highlightId === null || tab !== 'log') return
    find(`[data-entry-id="${highlightId}"]`)?.scrollIntoView?.({ block: 'center' })
  }, [highlightId, tab, entryCount, find])
  const back = t('team.panel.workbook_back')
  const heading = teamKey === undefined ? t('team.workbook.own_title') : t('team.workbook.title', { title })
  return (
    <div data-testid="team-seat-workbook" className="text-xs text-text-primary flex flex-col flex-1 min-h-0">
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
        <WorkbookToolbar v2={v2} tab={tab} onTab={switchTab} hostId={hostId} sessionId={sessionId} convKey={wb.convKey} conv={conv} />
        {trailing !== undefined && <span className="flex items-center gap-0.5 flex-shrink-0">{trailing}</span>}
      </div>
      <div ref={bindBox} onScroll={onScroll} data-testid="workbook-body" className="px-3 py-2 flex flex-col gap-2 flex-1 min-h-0 overflow-y-auto">
        <WorkbookStatus status={conv?.status ?? ''} statusAt={conv?.statusAt ?? 0} loading={!!conv?.loading} />
        {tab === 'todos' ? (
          <WorkbookTodos hostId={hostId} convKey={wb.convKey} conv={conv} onJump={(x) => { void jump(x) }} />
        ) : (
          <>
            {v2 && <OpenTodoSection conv={conv} />}
            {notFound &&<div role="status" className="text-[11px] text-text-muted">{t('team.workbook.entry_not_found')}</div>}
            <WorkbookLog hostId={hostId} convKey={wb.convKey} conv={conv} openGroups={openGroups} onToggleGroup={toggleGroup} highlightId={highlightId} />
          </>
        )}
      </div>
    </div>
  )
}
