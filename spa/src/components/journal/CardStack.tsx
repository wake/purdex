// spa/src/components/journal/CardStack.tsx — the floating cards of a journal.v1 host (J-2b T-2b.2, spec §11), mounted by the
// shell INSTEAD of TeamPanelArea for the active tab's host (App.tsx). Columns right-aligned under the title bar:
//   [team card, the roadmap under it] [journal card] [tasks card]
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
import { visibleCards, teamColumnWidth } from './card-layout'
import { useJournalTarget } from './useJournalTarget'
import { TeamPanel } from '../team/TeamPanel'
import { TeamPanelArea } from '../team/TeamPanelArea'
import { useTeamDisplay } from '../team/team-display'
import { useTabStore } from '../../stores/useTabStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { useJournalStore } from '../../stores/useJournalStore'
import { cardWidth, useJournalUiStore, type CardKey } from '../../stores/useJournalUiStore'
import { keepFocus } from '../../lib/keep-focus'
import { patchUnitMemo, useViewMemo } from '../../lib/journal/use-view-memo'
import { patchViewMemo, readViewMemo, type ViewMemo } from '../../lib/journal/view-memory'

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

/** What the shell mounts beside the tab content: the card stack on a journal.v1 host, else today's one panel area. */
export function PanelAreas() {
  const journalHost = useJournalTarget().journalHost
  return journalHost ? <CardStack /> : <TeamPanelArea />
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
  const display = useTeamDisplay()
  const activeTabId = useTabStore((s) => s.activeTabId)
  const { team, memoHost, memoKey } = target
  const memo = useViewMemo(memoHost, memoKey)
  const teamKey = team?.teamKey ?? null
  const teamOpen = useJournalUiStore((s) => teamKey !== null && s.teamOpen[teamKey] === true)
  const last = useJournalUiStore((s) => s.lastWidths)
  const rootRef = useRef<HTMLDivElement>(null)
  const available = useContentWidth(rootRef)

  // The open 待確認 detail takes the roadmap's place (a card to the journal's left): it counts as the roadmap for the narrow-window
  // drop, and `roadmapOpen` itself is never touched, so closing the detail brings the roadmap back as it was.
  const unit = target.unit
  const found = memo.journalOpen && unit && memo.detailId !== null ? (unit.entries.find((e) => e.id === memo.detailId) ?? unit.confirms.find((e) => e.id === memo.detailId)) : undefined
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
    if (target.read && detail) void useJournalStore.getState().setConfirm(target.read.hostId, target.read.sessionId, detail.id, idx, done)
  }

  const widths = {
    team: cardWidth('team', null, last),
    roadmap: cardWidth('roadmap', memo.widths.roadmap, last),
    journal: cardWidth('journal', memo.widths.journal, last),
    tasks: cardWidth('tasks', memo.widths.tasks, last),
  }
  const shown = visibleCards({
    open: { team: teamOpen, roadmap: memo.roadmapOpen || detail !== undefined, journal: memo.journalOpen, tasks: memo.tasksOpen },
    widths,
    available,
  })

  const patch = (p: Partial<ViewMemo>) => patchUnitMemo(memoHost, memoKey, p)
  const commit = (card: Exclude<CardKey, 'team'>) => (w: number) => {
    patch({ widths: { ...memo.widths, [card]: w } })
    useJournalUiStore.getState().setLastWidth(card, w)
  }
  const closeDetail = useCallback(() => patchUnitMemo(memoHost, memoKey, { detailId: null }), [memoHost, memoKey])
  const fix = (card: Exclude<CardKey, 'team'>) => ({ fixed: memo.fixed[card], onToggleFixed: () => patch({ fixed: { ...memo.fixed, [card]: !memo.fixed[card] } }) })
  useOutsideClose(memoHost, memoKey)
  const teamColumn = shown.team || shown.roadmap

  return (
    <div ref={rootRef} data-testid="journal-card-stack" className="absolute z-20 top-0 right-3 bottom-0 flex items-start justify-end gap-3 pointer-events-none">
      {teamColumn && (
        <div data-testid="journal-team-column" className="flex flex-col gap-3 items-end min-h-0 max-h-full" style={{ width: teamColumnWidth(shown, widths) }}>
          {shown.team && team && display && (
            <CardFrame testId="journal-team-card" width={widths.team} onCommitWidth={null}>
              <TeamPanel
                card
                team={{ ...team, mode: 'full' }}
                activeTabId={activeTabId}
                onSetMode={() => {}}
                onOpen={(sessionId) => display.onOpenSeat(team.teamKey, sessionId)}
                onReorder={(ids) => display.onReorderMembers(team.teamKey, ids)}
              />
            </CardFrame>
          )}
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
