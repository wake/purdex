// spa/src/components/journal/CardStack.tsx — the floating cards of a journal.v1 host (J-2b T-2b.2, spec §11), mounted by the
// shell BESIDE TeamPanelArea (JV-2: the team panel is the docked area on every host again, the team card is gone). Columns
// right-aligned under the title bar, left of the team panel while it is in the pane:
//   [roadmap / 待確認 detail] [journal card] [tasks card]
// Opening a card pushes the others left (the row is right-aligned; widths animate). What is open, the widths, the filter and the
// scroll live OUTSIDE this component (lib/journal/view-memory.ts per unit, useJournalUiStore per team / last widths) — the stack
// is a sibling of TabContent, so a tab switch does not unmount it, but a support toggle or a hide / show does (CLAUDE.md,
// Tab-hosted checklist). The narrow-window drop is visibility only (card-layout.ts): nothing is written back.
// The cards' bodies: JournalCard (J-2c), TasksCard and RoadmapCard (J-2d).
import { useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react'
import { ListChecks, MapTrifold } from '@phosphor-icons/react'
import { CardFrame } from './CardFrame'
import { ConfirmDetail } from './ConfirmDetail'
import { JournalCard, JournalTitle } from './JournalCard'
import { PageError } from './JournalPages'
import { useSingleFlight } from './useSingleFlight'
import { TasksCard } from './TasksCard'
import { RoadmapCard } from './RoadmapCard'
import { CARD_GAP, STACK_MARGIN, visibleCards } from './card-layout'
import { useJournalTarget } from './useJournalTarget'
import { TeamPanelArea } from '../team/TeamPanelArea'
import { useShownPanelWidth } from '../team/useShownPanelWidth'
import { usePersonalTarget } from '../team/usePersonalPanel'
import { useTeamUiStore } from '../../stores/useTeamUiStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { findEntry, useJournalStore } from '../../stores/useJournalStore'
import { cardWidth, useJournalUiStore, type CardKey } from '../../stores/useJournalUiStore'
import { keepFocus } from '../../lib/keep-focus'
import { patchUnitMemo, useViewMemo } from '../../lib/journal/use-view-memo'
import { patchViewMemo, readViewMemo, type ViewMemo } from '../../lib/journal/view-memory'
import { rerouteOnRemote } from '../../lib/journal/journal-loader'

/**
 * The width of the content box the stack floats in (its offset parent: the side bar and the activity bar already took theirs),
 * observed with a ResizeObserver. Before the first measurement, or where there is no ResizeObserver, the window width.
 */
function useContentWidth(root: React.RefObject<HTMLElement | null>): number {
  const [w, setW] = useState<number | null>(null)
  useLayoutEffect(() => {
    const parent = root.current?.parentElement
    if (!parent || typeof ResizeObserver === 'undefined') return
    const ro = new ResizeObserver((entries) => {
      const width = entries[entries.length - 1]?.contentRect.width
      if (typeof width === 'number') setW(width)
    })
    ro.observe(parent)
    return () => ro.disconnect()
  }, [root])
  const [win, setWin] = useState(() => (typeof window === 'undefined' ? 1440 : window.innerWidth))
  useEffect(() => {
    if (typeof ResizeObserver !== 'undefined') return
    const on = () => setWin(window.innerWidth)
    window.addEventListener('resize', on)
    return () => window.removeEventListener('resize', on)
  }, [])
  return w ?? win
}

export function CardStack() {
  const target = useJournalTarget()
  if (!target.journalHost) return null
  return <Stack target={target} />
}

/** What the shell mounts beside the tab content: the team panel area on every host, and the journal cards on a journal.v1 host. */
export function PanelAreas() {
  return (
    <>
      <TeamPanelArea />
      <CardStack />
    </>
  )
}

/**
 * U4: the journal, roadmap and tasks cards float. A pointer-down outside every card closes the unfixed ones; the 工作簿 button
 * (`data-journal-keep`) and every card of the stack, the 待確認 detail among them (`data-journal-card`), are not "outside". A fixed card
 * stays until its X or the 工作簿 button. The 待確認 detail is the journal's companion: it stays while the journal is fixed.
 * Reads the unit's memo at event time (no stale closure), writes it back through the same view memo.
 */
function useOutsideClose(host: string | null, key: string | null) {
  useEffect(() => {
    if (host === null || key === null) return
    const onDown = (e: PointerEvent | MouseEvent) => {
      const el = e.target instanceof Element ? e.target : null
      if (el?.closest('[data-journal-card], [data-journal-keep]')) return
      const m = readViewMemo(host, key)
      const patch: Partial<ViewMemo> = {}
      if (m.journalOpen && !m.fixed.journal) { patch.journalOpen = false; patch.detailId = null }
      if (m.roadmapOpen && !m.fixed.roadmap) patch.roadmapOpen = false
      if (m.tasksOpen && !m.fixed.tasks) patch.tasksOpen = false
      if (m.detailId !== null && !m.fixed.journal) patch.detailId = null
      if (Object.keys(patch).length > 0) patchViewMemo(host, key, patch)
    }
    document.addEventListener('pointerdown', onDown, true)
    return () => document.removeEventListener('pointerdown', onDown, true)
  }, [host, key])
}

function Stack({ target }: { target: ReturnType<typeof useJournalTarget> }) {
  const t = useI18nStore((s) => s.t)
  const { team, memoHost, memoKey } = target
  const memo = useViewMemo(memoHost, memoKey)
  // The docked team panel sits at the right edge while it is in the pane (not in the title bar): the cards float left of it.
  // The width it really draws: the dragged / persisted width is only the cap in one-line mode (user 2026-10-20). A journal.v1 host never drills.
  // A tab of no team with an agent session has the personal panel there, in the one shared mode (task groups spec §7).
  const personal = usePersonalTarget()
  const sharedMode = useTeamUiStore((s) => s.sharedPanelMode)
  const personalInPane = team === null && personal !== null && sharedMode !== 'titlebar'
  const panelWidth = useShownPanelWidth(team, false, personalInPane && sharedMode === 'line')
  const panelInPane = personalInPane || (team !== null && team.mode !== 'titlebar')
  const reserved = panelInPane ? panelWidth + CARD_GAP : 0
  const last = useJournalUiStore((s) => s.lastWidths)
  const rootRef = useRef<HTMLDivElement>(null)
  const available = useContentWidth(rootRef)

  // The open 待確認 detail takes the roadmap's place (a card to the journal's left): it counts as the roadmap for the narrow-window
  // drop, and `roadmapOpen` itself is never touched, so closing the detail brings the roadmap back as it was.
  const unit = target.unit
  const found = memo.journalOpen && unit && memo.detailId !== null ? findEntry(unit, memo.detailId) : undefined
  // Checking the last item takes the entry out of the band; an entry of an older month is not in the stream either, so the detail
  // keeps the last copy it saw (every item answered) instead of vanishing under the pointer.
  // The copy lives in the unit's view memo, outside this component: it survives a remount and is never shared between units.
  const lastDetail = memo.lastDetail
  useEffect(() => {
    if (found && found !== lastDetail) patchUnitMemo(memoHost, memoKey, { lastDetail: found })
  }, [found, lastDetail, memoHost, memoKey])
  const kept = !found && memo.journalOpen && memo.detailId !== null && lastDetail?.id === memo.detailId
    ? { ...lastDetail, items: lastDetail.items.map((it) => ({ ...it, done: true })) }
    : undefined
  const detailEntry = found ?? kept
  const detail = detailEntry?.kind === 'confirm' ? detailEntry : undefined
  const canCheck = useJournalStore((s) => target.read !== null && s.confirmSupport[target.read.hostId] === true)
  const onToggleItem = (idx: number, done: boolean) => {
    if (target.read && detail) void useJournalStore.getState().setConfirm(target.read.hostId, target.read.sessionId, detail.id, idx, done, detail)
  }

  const widths = {
    roadmap: cardWidth('roadmap', memo.widths.roadmap, last),
    journal: cardWidth('journal', memo.widths.journal, last),
    tasks: cardWidth('tasks', memo.widths.tasks, last),
  }
  const shown = visibleCards({
    open: { roadmap: memo.roadmapOpen || detail !== undefined, journal: memo.journalOpen, tasks: memo.tasksOpen },
    widths,
    available,
    reserved,
  })

  const patch = (p: Partial<ViewMemo>) => patchUnitMemo(memoHost, memoKey, p)
  const commit = (card: CardKey) => (w: number) => {
    patch({ widths: { ...memo.widths, [card]: w } })
    useJournalUiStore.getState().setLastWidth(card, w)
  }
  // The first read failed and there is no unit yet: the retry asks that target again, once however many times it is clicked.
  const readHost = target.read?.hostId
  const readSession = target.read?.sessionId
  const retryFirstRead = useSingleFlight(`${readHost}\u0000${readSession}`, useCallback(
    () => (readHost !== undefined && readSession !== undefined ? useJournalStore.getState().loadJournal(readHost, readSession).then((r) => rerouteOnRemote(readHost, readSession, r)) : Promise.resolve()),
    [readHost, readSession],
  ))
  const closeDetail = useCallback(() => patchUnitMemo(memoHost, memoKey, { detailId: null }), [memoHost, memoKey])
  const fix = (card: Exclude<CardKey, 'team'>) => ({ fixed: memo.fixed[card], onToggleFixed: () => patch({ fixed: { ...memo.fixed, [card]: !memo.fixed[card] } }) })
  useOutsideClose(memoHost, memoKey)

  return (
    <div ref={rootRef} data-testid="journal-card-stack" className="absolute z-20 top-0 bottom-0 flex items-start justify-end gap-3 pointer-events-none" style={{ right: STACK_MARGIN + reserved }}>
      {shown.roadmap && (
        <div data-testid="journal-side-column" className="flex flex-col gap-3 items-end min-h-0 max-h-full" style={{ width: widths.roadmap }}>
          {shown.roadmap && detail && unit && (
            <CardFrame testId="journal-detail-card" floating width={widths.roadmap} onCommitWidth={commit('roadmap')} title={t('journal.detail.title')} onClose={closeDetail}>
              <ConfirmDetail entry={detail} personName={unit.personName} onClose={closeDetail} onToggleItem={canCheck ? onToggleItem : undefined} />
            </CardFrame>
          )}
          {shown.roadmap && !detail && (
            <CardFrame testId="journal-roadmap-card" floating width={widths.roadmap} onCommitWidth={commit('roadmap')} title={t('journal.card.roadmap')} onClose={() => patch({ roadmapOpen: false })} {...fix('roadmap')}>
              {unit && <RoadmapCard key={`${target.memoHost}\u0000${target.memoKey}`} target={target} unit={unit} />}
            </CardFrame>
          )}
        </div>
      )}
      {shown.journal && (
        <CardFrame
          testId="journal-card"
          floating
          width={widths.journal}
          onCommitWidth={commit('journal')}
          title={<JournalTitle target={target} />}
          fill
          actions={
            <>
              <HeaderToggle testId="journal-toggle-roadmap" pressed={memo.roadmapOpen} label={t('journal.card.roadmap')} onClick={() => patch(detail ? { detailId: null, roadmapOpen: !memo.roadmapOpen } : { roadmapOpen: !memo.roadmapOpen })}><MapTrifold size={13} /></HeaderToggle>
              <HeaderToggle testId="journal-toggle-tasks" pressed={memo.tasksOpen} label={t('journal.card.tasks')} onClick={() => patch({ tasksOpen: !memo.tasksOpen })}><ListChecks size={13} /></HeaderToggle>
            </>
          }
          onClose={() => patch({ journalOpen: false })}
          {...fix('journal')}
        >
          {unit && <JournalCard key={`${target.memoHost}\u0000${target.memoKey}`} target={target} unit={unit} />}
          {target.loadFailed && !target.unit && <PageError onRetry={retryFirstRead} testId="journal-card-error" />}
          {target.unreachableHost !== null && !target.unit && (
            <div data-testid="journal-unreachable" className="p-3 text-text-secondary">{t('journal.card.unreachable', { host: target.unreachableHost })}</div>
          )}
        </CardFrame>
      )}
      {shown.tasks && (
        <CardFrame testId="journal-tasks-card" floating width={widths.tasks} onCommitWidth={commit('tasks')} title={t('journal.card.tasks')} onClose={() => patch({ tasksOpen: false })} {...fix('tasks')}>
          {unit && <TasksCard key={`${target.memoHost}\u0000${target.memoKey}`} target={target} unit={unit} />}
        </CardFrame>
      )}
    </div>
  )
}

function HeaderToggle({ testId, pressed, label, onClick, children }: { testId: string; pressed: boolean; label: string; onClick: () => void; children: React.ReactNode }) {
  return (
    <button
      type="button"
      data-testid={testId}
      aria-pressed={pressed}
      aria-label={label}
      title={label}
      className={`p-0.5 rounded cursor-pointer ${pressed ? 'text-accent bg-accent-muted' : 'text-text-secondary hover:text-text-primary hover:bg-surface-hover'}`}
      onMouseDown={keepFocus}
      onClick={onClick}
    >
      {children}
    </button>
  )
}
