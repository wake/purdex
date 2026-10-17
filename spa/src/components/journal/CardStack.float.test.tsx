// spa/src/components/journal/CardStack.float.test.tsx — the journal, roadmap and tasks cards float (JV-3, spec §3.2 / U4): a pointer-down
// outside closes the unfixed ones; the 工作簿 button and the cards (the 待確認 detail among them) are not "outside"; a fixed card stays
// until its X; 固定 is off by default and belongs to the unit.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import { CardStack } from './CardStack'
import { JournalButtons } from './JournalButtons'
import { CardFrame } from './CardFrame'
import { TeamDisplayProvider } from '../team/TeamDisplayProvider'
import { HOST, resetTeamStores, seedScene } from '../../lib/team/__tests__/team-fixture'
import { ccTabOf, seedUnit, setJournalHost } from '../../lib/journal/__tests__/journal-fixture'
import { clearViewMemos, patchViewMemo, readViewMemo } from '../../lib/journal/view-memory'
import { clearModuleRegistry } from '../../lib/module-registry'
import { useTabStore } from '../../stores/useTabStore'
import { useJournalStore } from '../../stores/useJournalStore'
import { useJournalUiStore } from '../../stores/useJournalUiStore'
import { useShownHostsStore } from '../../stores/useShownHostsStore'
import { useI18nStore } from '../../stores/useI18nStore'
import type { JournalEntry } from '../../lib/journal/types'

vi.mock('../team/TeamSeatIcon', () => ({
  TeamSeatIcon: () => <span data-testid="seat-icon" />,
  TeamSeatHostBadge: () => <span data-testid="seat-host" />,
}))

const AT = new Date(2026, 9, 14, 9, 0).getTime()
const CONFIRM: JournalEntry = {
  id: 'c1', kind: 'confirm', by: '', at: AT, actor: 'Kenji → Wake', text: 'decide', refs: [],
  items: [{ text: 'q1', done: false }], detail: 'the original question',
}
const A = { kind: 'solo' as const, id: 'a', label: 'Solo A' }
const B = { kind: 'solo' as const, id: 'b', label: 'Solo B' }
const q = (id: string) => screen.queryByTestId(id)
const setActive = (id: string) => act(() => { useTabStore.setState({ activeTabId: id }) })
const outside = () => fireEvent.pointerDown(document.body)
const CARDS = ['journal-card', 'journal-roadmap-card', 'journal-tasks-card']

function Shell() {
  return <TeamDisplayProvider><JournalButtons /><CardStack /></TeamDisplayProvider>
}

beforeEach(() => {
  cleanup()
  localStorage.clear()
  resetTeamStores()
  useJournalStore.getState().reset()
  useJournalUiStore.setState({ lastWidths: {}, teamOpen: {} })
  clearViewMemos()
  clearModuleRegistry()
  useShownHostsStore.setState({ ids: [HOST] })
  useI18nStore.getState().setLocale('en')
  Object.defineProperty(window, 'innerWidth', { configurable: true, value: 2200 })
  seedScene({ members: [['A', 'a-tm']], tabs: [['x', null]], workspaces: [{ id: 'w1', tabs: ['x'] }], activeTabId: 'x' })
  act(() => {
    useTabStore.setState((s) => ({
      tabs: { ...s.tabs, a: ccTabOf('a', HOST, 'a-solo', 'SA'), b: ccTabOf('b', HOST, 'b-solo', 'SB') },
      tabOrder: [...s.tabOrder, 'a', 'b'],
    }))
  })
  setJournalHost(HOST, true)
  seedUnit(HOST, 'SA', A, { entries: [CONFIRM], confirms: [CONFIRM], openConfirms: 1, tasks: [], roadmap: [] })
  seedUnit(HOST, 'SB', B, { entries: [], tasks: [], roadmap: [] })
  patchViewMemo(HOST, 'solo:a', { journalOpen: true, roadmapOpen: true, tasksOpen: true })
  patchViewMemo(HOST, 'solo:b', { journalOpen: true, roadmapOpen: true, tasksOpen: true })
  setActive('a')
})
afterEach(() => { cleanup(); Object.defineProperty(window, 'innerWidth', { configurable: true, value: 1024 }) })

describe('floating cards', () => {
  it('a pointer-down on a panel that is not one of the floating cards (the team panel\'s frame) closes the unfixed ones', () => {
    render(<><Shell /><CardFrame testId="team-frame" width={300} onCommitWidth={null}><button data-testid="team-seat">seat</button></CardFrame></>)
    expect(screen.getByTestId('team-frame').hasAttribute('data-journal-card')).toBe(false)
    fireEvent.pointerDown(screen.getByTestId('team-seat'))
    for (const id of CARDS) expect(q(id)).toBeNull()
  })
  it('only the journal, roadmap, tasks and detail cards carry the marker', () => {
    act(() => patchViewMemo(HOST, 'solo:a', { detailId: 'c1' }))
    render(<Shell />)
    for (const id of [...CARDS.filter((c) => c !== 'journal-roadmap-card'), 'journal-detail-card']) expect(screen.getByTestId(id).hasAttribute('data-journal-card')).toBe(true)
  })
  it('start unfixed, and a click outside closes all three', () => {
    render(<Shell />)
    expect(readViewMemo(HOST, 'solo:a').fixed).toEqual({ journal: false, roadmap: false, tasks: false })
    for (const id of CARDS) expect(q(id)).toBeTruthy()
    outside()
    for (const id of CARDS) expect(q(id)).toBeNull()
  })
  it('a pointer-down inside a card does not close anything', () => {
    render(<Shell />)
    fireEvent.pointerDown(screen.getByTestId('journal-tasks-card'))
    fireEvent.pointerDown(screen.getByTestId('journal-card-body'))
    for (const id of CARDS) expect(q(id)).toBeTruthy()
  })
  it('the 工作簿 button is not outside (its own click toggles the card instead)', () => {
    render(<Shell />)
    fireEvent.pointerDown(screen.getByTestId('journal-journal-button'))
    for (const id of CARDS) expect(q(id)).toBeTruthy()
    fireEvent.click(screen.getByTestId('journal-journal-button'))
    expect(q('journal-card')).toBeNull()
  })
  it('the 待確認 detail is not outside, and an outside click closes it with the journal', () => {
    patchViewMemo(HOST, 'solo:a', { detailId: 'c1' })
    render(<Shell />)
    expect(q('journal-detail-card')).toBeTruthy()
    fireEvent.pointerDown(screen.getByTestId('journal-detail'))
    expect(q('journal-detail-card')).toBeTruthy()
    outside()
    expect(q('journal-detail-card')).toBeNull()
    expect(readViewMemo(HOST, 'solo:a').detailId).toBeNull()
  })
  it('fixing one card keeps only that card on an outside click; each card is independent', () => {
    render(<Shell />)
    fireEvent.click(screen.getByTestId('journal-tasks-card-fix'))
    expect(screen.getByTestId('journal-tasks-card-fix').getAttribute('aria-pressed')).toBe('true')
    outside()
    expect(q('journal-tasks-card')).toBeTruthy()
    expect(q('journal-card')).toBeNull()
    expect(q('journal-roadmap-card')).toBeNull()
  })
  it('the roadmap and the journal each hold on their own 固定', () => {
    render(<Shell />)
    fireEvent.click(screen.getByTestId('journal-roadmap-card-fix'))
    outside()
    expect(q('journal-roadmap-card')).toBeTruthy()
    expect(q('journal-card')).toBeNull()
    expect(q('journal-tasks-card')).toBeNull()
  })
  it('a fixed card still closes by its X, and by the 工作簿 button (the journal)', () => {
    render(<Shell />)
    fireEvent.click(screen.getByTestId('journal-card-fix'))
    fireEvent.click(screen.getByTestId('journal-tasks-card-fix'))
    outside()
    expect(q('journal-card')).toBeTruthy()
    fireEvent.click(screen.getByTestId('journal-tasks-card-close'))
    expect(q('journal-tasks-card')).toBeNull()
    fireEvent.click(screen.getByTestId('journal-journal-button'))
    expect(q('journal-card')).toBeNull()
  })
  it('toggling 固定 again lets an outside click close the card', () => {
    render(<Shell />)
    fireEvent.click(screen.getByTestId('journal-card-fix'))
    fireEvent.click(screen.getByTestId('journal-card-fix'))
    outside()
    expect(q('journal-card')).toBeNull()
  })
  it('a fixed journal keeps its open detail on an outside click', () => {
    patchViewMemo(HOST, 'solo:a', { detailId: 'c1' })
    render(<Shell />)
    fireEvent.click(screen.getByTestId('journal-card-fix'))
    outside()
    expect(q('journal-detail-card')).toBeTruthy()
  })
  it('unit A fixing a card does not fix unit B\'s', () => {
    render(<Shell />)
    fireEvent.click(screen.getByTestId('journal-card-fix'))
    expect(readViewMemo(HOST, 'solo:a').fixed.journal).toBe(true)
    expect(readViewMemo(HOST, 'solo:b').fixed.journal).toBe(false)
    setActive('b')
    outside()
    expect(q('journal-card')).toBeNull()
    setActive('a')
    expect(q('journal-card')).toBeTruthy()
  })
})
