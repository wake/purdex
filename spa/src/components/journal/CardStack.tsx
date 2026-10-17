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
import { TasksCard } from './TasksCard'
import { RoadmapCard } from './RoadmapCard'
import { CARD_GAP, STACK_MARGIN, visibleCards } from './card-layout'
import { useJournalTarget } from './useJournalTarget'
import { TeamPanelArea } from '../team/TeamPanelArea'
import { useTeamUiStore } from '../../stores/useTeamUiStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { cardWidth, useJournalUiStore, type CardKey } from '../../stores/useJournalUiStore'
import { keepFocus } from '../../lib/keep-focus'
import { patchUnitMemo, useViewMemo } from '../../lib/journal/use-view-memo'
import type { ViewMemo } from '../../lib/journal/view-memory'

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

function Stack({ target }: { target: ReturnType<typeof useJournalTarget> }) {
  const t = useI18nStore((s) => s.t)
  const { team, memoHost, memoKey } = target
  const memo = useViewMemo(memoHost, memoKey)
  // The docked team panel sits at the right edge while it is in the pane (not in the title bar): the cards float left of it.
  const panelWidth = useTeamUiStore((s) => s.panel.width)
  const panelInPane = team !== null && team.mode !== 'titlebar'
  const reserved = panelInPane ? panelWidth + CARD_GAP : 0
  const last = useJournalUiStore((s) => s.lastWidths)
  const rootRef = useRef<HTMLDivElement>(null)
  const available = useContentWidth(rootRef)

  // The open 待確認 detail takes the roadmap's place (a card to the journal's left): it counts as the roadmap for the narrow-window
  // drop, and `roadmapOpen` itself is never touched, so closing the detail brings the roadmap back as it was.
  const unit = target.unit
  const detailEntry = memo.journalOpen && unit && memo.detailId !== null ? (unit.entries.find((e) => e.id === memo.detailId) ?? unit.pinned.find((e) => e.id === memo.detailId)) : undefined
  const detail = detailEntry?.kind === 'confirm' ? detailEntry : undefined

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
  const closeDetail = useCallback(() => patchUnitMemo(memoHost, memoKey, { detailId: null }), [memoHost, memoKey])

  return (
    <div ref={rootRef} data-testid="journal-card-stack" className="absolute z-20 top-0 bottom-0 flex items-start justify-end gap-3 pointer-events-none" style={{ right: STACK_MARGIN + reserved }}>
      {shown.roadmap && (
        <div data-testid="journal-side-column" className="flex flex-col gap-3 items-end min-h-0 max-h-full" style={{ width: widths.roadmap }}>
          {shown.roadmap && detail && unit && (
            <CardFrame testId="journal-detail-card" width={widths.roadmap} onCommitWidth={commit('roadmap')} title={t('journal.detail.title')} onClose={closeDetail}>
              <ConfirmDetail entry={detail} personName={unit.personName} onClose={closeDetail} />
            </CardFrame>
          )}
          {shown.roadmap && !detail && (
            <CardFrame testId="journal-roadmap-card" width={widths.roadmap} onCommitWidth={commit('roadmap')} title={t('journal.card.roadmap')} onClose={() => patch({ roadmapOpen: false })}>
              {unit && <RoadmapCard key={`${target.memoHost}\u0000${target.memoKey}`} target={target} unit={unit} />}
            </CardFrame>
          )}
        </div>
      )}
      {shown.journal && (
        <CardFrame
          testId="journal-card"
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
        >
          {unit && <JournalCard key={`${target.memoHost}\u0000${target.memoKey}`} target={target} unit={unit} />}
          {target.unreachableHost !== null && !target.unit && (
            <div data-testid="journal-unreachable" className="p-3 text-text-secondary">{t('journal.card.unreachable', { host: target.unreachableHost })}</div>
          )}
        </CardFrame>
      )}
      {shown.tasks && (
        <CardFrame testId="journal-tasks-card" width={widths.tasks} onCommitWidth={commit('tasks')} title={t('journal.card.tasks')} onClose={() => patch({ tasksOpen: false })}>
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
