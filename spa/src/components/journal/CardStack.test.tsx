// spa/src/components/journal/CardStack.test.tsx — the card stack: order, push, the narrow-window drop, resize, and that what the
// person arranged lives outside the component (J-2b T-2b.2 / T-2b.3). Real stores; the pane kinds are not registered (TabContent
// draws the empty fallback), which is all the tab-switch tests need: they are about the stack, not the panes.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { act, cleanup, fireEvent, render, screen, within } from '@testing-library/react'
import { CardStack, PanelAreas } from './CardStack'
import { visibleCards } from './card-layout'
import { TabContent } from '../TabContent'
import { TeamDisplayProvider } from '../team/TeamDisplayProvider'
import { HOST, KEY, resetTeamStores, seedScene } from '../../lib/team/__tests__/team-fixture'
import { ccTabOf, seedUnit, setJournalHost } from '../../lib/journal/__tests__/journal-fixture'
import { clearViewMemos, patchViewMemo, readViewMemo } from '../../lib/journal/view-memory'
import { clearModuleRegistry } from '../../lib/module-registry'
import { useTabStore } from '../../stores/useTabStore'
import { useJournalStore } from '../../stores/useJournalStore'
import { CARD_DEFAULT, useJournalUiStore } from '../../stores/useJournalUiStore'
import { useShownHostsStore } from '../../stores/useShownHostsStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { useUISettingsStore } from '../../stores/useUISettingsStore'
import type { Tab } from '../../types/tab'

vi.mock('../team/TeamSeatIcon', () => ({
  TeamSeatIcon: () => <span data-testid="seat-icon" />,
  TeamSeatHostBadge: () => <span data-testid="seat-host" />,
}))

const SOLO_A = { kind: 'solo' as const, id: 'a', label: 'Solo A' }
const SOLO_B = { kind: 'solo' as const, id: 'b', label: 'Solo B' }
const TEAM_UNIT = { kind: 'team' as const, id: 'tm1', label: 'Journal team' }
const addTab = (tab: Tab) => act(() => { useTabStore.setState((s) => ({ tabs: { ...s.tabs, [tab.id]: tab }, tabOrder: s.tabOrder.includes(tab.id) ? s.tabOrder : [...s.tabOrder, tab.id] })) })
const setActive = (id: string | null) => act(() => { useTabStore.setState({ activeTabId: id }) })
// The stack measures the content box it floats in (ResizeObserver on its parent); `setWidth` resizes both, `fireContent` only the box.
const observers = new Set<(w: number) => void>()
const fireContent = (w: number) => observers.forEach((f) => f(w))
class FakeRO {
  private f: (w: number) => void
  constructor(cb: ResizeObserverCallback) { this.f = (w) => cb([{ contentRect: { width: w } } as ResizeObserverEntry], this as unknown as ResizeObserver) }
  observe() { observers.add(this.f) }
  unobserve() {}
  disconnect() { observers.delete(this.f) }
}
const setWidth = (w: number) => act(() => { Object.defineProperty(window, 'innerWidth', { configurable: true, value: w }); window.dispatchEvent(new Event('resize')); fireContent(w) })
const q = (id: string) => screen.queryByTestId(id)
const openAll = (key: string, host = HOST) => patchViewMemo(host, key, { journalOpen: true, roadmapOpen: true, tasksOpen: true })

beforeEach(() => {
  vi.stubGlobal('ResizeObserver', FakeRO)
  observers.clear()
  cleanup()
  localStorage.clear()
  resetTeamStores()
  useJournalStore.getState().reset()
  useJournalUiStore.setState({ lastWidths: {}, teamOpen: {} })
  clearViewMemos()
  clearModuleRegistry()
  useShownHostsStore.setState({ ids: [HOST] })
  useUISettingsStore.setState({ keepAliveCount: 0 })
  useI18nStore.getState().setLocale('en')
  setWidth(1500)
  seedScene({ members: [['A', 'a-tm']], tabs: [['x', null]], workspaces: [{ id: 'w1', tabs: ['x'] }], activeTabId: 'x' })
  addTab(ccTabOf('a', HOST, 'a-solo', 'SA'))
  addTab(ccTabOf('b', HOST, 'b-solo', 'SB'))
  addTab(ccTabOf('lead', HOST, 'lead-tm', 'SL'))
  setJournalHost(HOST, true)
})
afterEach(() => { cleanup(); vi.unstubAllGlobals(); Object.defineProperty(window, 'innerWidth', { configurable: true, value: 1024 }) })

const mountShell = () => render(<TeamDisplayProvider><CardStack /></TeamDisplayProvider>)

describe('card-layout visibleCards', () => {
  const W = { team: 360, roadmap: 360, journal: 420, tasks: 340 }
  const all = { team: true, roadmap: true, journal: true, tasks: true }
  it('drops tasks, then roadmap, then the team card; the journal stays', () => {
    expect(visibleCards({ open: all, widths: W, available: 1500 })).toEqual(all)
    expect(visibleCards({ open: all, widths: W, available: 1300 })).toEqual({ ...all, tasks: false })
    expect(visibleCards({ open: all, widths: W, available: 1000 })).toEqual({ team: false, roadmap: false, journal: true, tasks: false })
    expect(visibleCards({ open: all, widths: W, available: 300 }).journal).toBe(true)
  })
  it('drops roadmap before the team card when the roadmap is what makes the column wide', () => {
    const wide = { ...W, roadmap: 600 }
    // team 360 + journal 420 + gap + margin = 804 fits 1150; the 600-wide roadmap column does not.
    expect(visibleCards({ open: { team: true, roadmap: true, journal: true, tasks: false }, widths: wide, available: 1150 })).toEqual({ team: true, roadmap: false, journal: true, tasks: false })
  })
})

describe('the stack on a journal.v1 host', () => {
  it('lays the columns out team column (team card, roadmap under it) · journal · tasks', () => {
    const key = seedUnit(HOST, 'SL', TEAM_UNIT)
    openAll(key)
    useJournalUiStore.getState().setTeamOpen(KEY, true)
    setActive('lead')
    mountShell()
    const stack = screen.getByTestId('journal-card-stack')
    expect([...stack.children].map((c) => (c as HTMLElement).dataset.testid)).toEqual(['journal-team-column', 'journal-card', 'journal-tasks-card'])
    expect([...screen.getByTestId('journal-team-column').children].map((c) => (c as HTMLElement).dataset.testid)).toEqual(['journal-team-card', 'journal-roadmap-card'])
  })
  it('the roadmap stands alone on the left without a team', () => {
    const key = seedUnit(HOST, 'SA', SOLO_A)
    openAll(key)
    setActive('a')
    mountShell()
    expect([...screen.getByTestId('journal-card-stack').children].map((c) => (c as HTMLElement).dataset.testid)).toEqual(['journal-team-column', 'journal-card', 'journal-tasks-card'])
    expect(q('journal-team-card')).toBeNull()
    expect(q('journal-roadmap-card')).toBeTruthy()
  })
  it('opening a card keeps the others and adds its own column to the right-aligned row', () => {
    const key = seedUnit(HOST, 'SA', SOLO_A)
    patchViewMemo(HOST, key, { journalOpen: true })
    setActive('a')
    mountShell()
    expect(screen.getByTestId('journal-card-stack').className).toContain('justify-end')
    expect(q('journal-tasks-card')).toBeNull()
    fireEvent.click(screen.getByTestId('journal-toggle-tasks'))
    expect(q('journal-tasks-card')).toBeTruthy()
    expect(q('journal-card')).toBeTruthy()
    fireEvent.click(screen.getByTestId('journal-tasks-card-close'))
    expect(q('journal-tasks-card')).toBeNull()
  })
  it('a pressed header toggle is styled with the theme\'s accent token (themes.css has `accent`, not `accent-base`)', () => {
    const key = seedUnit(HOST, 'SA', SOLO_A)
    patchViewMemo(HOST, key, { journalOpen: true, tasksOpen: true })
    setActive('a')
    mountShell()
    const pressed = screen.getByTestId('journal-toggle-tasks')
    expect(pressed.getAttribute('aria-pressed')).toBe('true')
    expect(pressed.className).toContain('text-accent')
    expect(pressed.className).not.toContain('accent-base')
  })
  it('the team card has no title-bar or enlarge control: it is open or closed and nothing else', () => {
    const key = seedUnit(HOST, 'SL', TEAM_UNIT)
    patchViewMemo(HOST, key, { journalOpen: true })
    useJournalUiStore.getState().setTeamOpen(KEY, true)
    setActive('lead')
    mountShell()
    const card = screen.getByTestId('journal-team-card')
    expect(within(card).getByTestId('team-panel')).toBeTruthy()
    expect(within(card).queryByTestId('team-panel-to-titlebar')).toBeNull()
    expect(within(card).queryByTestId('team-panel-expand')).toBeNull()
  })
  it('hides tasks, then roadmap, then the team card as the window narrows; the journal stays', () => {
    const key = seedUnit(HOST, 'SL', TEAM_UNIT)
    openAll(key)
    useJournalUiStore.getState().setTeamOpen(KEY, true)
    setActive('lead')
    mountShell()
    const present = () => ['journal-team-card', 'journal-roadmap-card', 'journal-card', 'journal-tasks-card'].filter((id) => q(id))
    expect(present()).toEqual(['journal-team-card', 'journal-roadmap-card', 'journal-card', 'journal-tasks-card'])
    setWidth(1300)
    expect(present()).toEqual(['journal-team-card', 'journal-roadmap-card', 'journal-card'])
    setWidth(1000)
    expect(present()).toEqual(['journal-card'])
    setWidth(300)
    expect(present()).toEqual(['journal-card'])
  })
  it('narrow then wide again restores the hidden cards and writes nothing back', () => {
    const key = seedUnit(HOST, 'SL', TEAM_UNIT)
    openAll(key)
    useJournalUiStore.getState().setTeamOpen(KEY, true)
    setActive('lead')
    mountShell()
    const before = { memo: { ...readViewMemo(HOST, key) }, team: { ...useJournalUiStore.getState().teamOpen } }
    setWidth(900)
    expect(q('journal-tasks-card')).toBeNull()
    expect(q('journal-team-card')).toBeNull()
    expect(readViewMemo(HOST, key)).toEqual(before.memo)
    expect(useJournalUiStore.getState().teamOpen).toEqual(before.team)
    setWidth(1500)
    expect(q('journal-tasks-card')).toBeTruthy()
    expect(q('journal-team-card')).toBeTruthy()
    expect(q('journal-roadmap-card')).toBeTruthy()
  })
  it('a resize is a draft until mouseup, then the unit width and the last used width are written', () => {
    const key = seedUnit(HOST, 'SA', SOLO_A)
    patchViewMemo(HOST, key, { journalOpen: true })
    setActive('a')
    mountShell()
    const card = screen.getByTestId('journal-card')
    expect(card.style.width).toBe(`${CARD_DEFAULT.journal}px`)
    fireEvent.mouseDown(within(card).getByTestId('resize-hit'), { clientX: 500 })
    fireEvent.mouseMove(document, { clientX: 450 }) // the left edge moves left: 50 wider
    expect(card.style.width).toBe(`${CARD_DEFAULT.journal + 50}px`)
    expect(readViewMemo(HOST, key).widths.journal).toBeNull()
    fireEvent.mouseUp(document)
    expect(readViewMemo(HOST, key).widths.journal).toBe(CARD_DEFAULT.journal + 50)
    expect(useJournalUiStore.getState().lastWidths.journal).toBe(CARD_DEFAULT.journal + 50)
  })
  it('a unit with no width of its own opens at the last width the person used', () => {
    useJournalUiStore.getState().setLastWidth('journal', 500)
    const key = seedUnit(HOST, 'SA', SOLO_A)
    patchViewMemo(HOST, key, { journalOpen: true })
    setActive('a')
    mountShell()
    expect(screen.getByTestId('journal-card').style.width).toBe('500px')
  })
  it('says where the journal is when the lead host is not connected', () => {
    useJournalStore.getState().setRoute(HOST, 'SA', { kind: 'unreachable', leadHostId: 'lead-daemon', leadSessionId: 'L' })
    patchViewMemo(HOST, 'unreachable:lead-daemon', { journalOpen: true })
    setActive('a')
    mountShell()
    expect(screen.getByTestId('journal-unreachable').textContent).toContain('lead-daemon')
  })
  it('renders nothing for a host that is not journal.v1', () => {
    setJournalHost(HOST, false)
    seedUnit(HOST, 'SA', SOLO_A)
    setActive('a')
    mountShell()
    expect(q('journal-card-stack')).toBeNull()
  })
})

describe('the available width is the content box, not the window', () => {
  it('hides tasks, then roadmap, then the team card by the container width (the window stays wide)', () => {
    const key = seedUnit(HOST, 'SL', TEAM_UNIT)
    openAll(key)
    useJournalUiStore.getState().setTeamOpen(KEY, true)
    setActive('lead')
    setWidth(2000)
    render(<div><TeamDisplayProvider><CardStack /></TeamDisplayProvider></div>)
    const present = () => ['journal-team-card', 'journal-roadmap-card', 'journal-card', 'journal-tasks-card'].filter((id) => q(id))
    act(() => fireContent(1500))
    expect(present()).toEqual(['journal-team-card', 'journal-roadmap-card', 'journal-card', 'journal-tasks-card'])
    act(() => fireContent(1300))
    expect(present()).toEqual(['journal-team-card', 'journal-roadmap-card', 'journal-card'])
    act(() => fireContent(1000))
    expect(present()).toEqual(['journal-card'])
    act(() => fireContent(1500))
    expect(present().length).toBe(4)
  })
})

// The stack is a sibling of TabContent, so a tab switch alone does not unmount it; the state still must not live in it.
describe('view state survives tab switches and a remount of the stack', () => {
  const Shell = ({ stack = true }: { stack?: boolean }) => {
    const activeTabId = useTabStore((s) => s.activeTabId)
    const tabs = useTabStore((s) => s.tabs)
    const tab = activeTabId ? tabs[activeTabId] : null
    return (
      <TeamDisplayProvider>
        <TabContent activeTab={tab ?? null} allTabs={Object.values(tabs)} />
        {stack && <CardStack />}
      </TeamDisplayProvider>
    )
  }
  const seedTwo = () => {
    const ka = seedUnit(HOST, 'SA', SOLO_A)
    const kb = seedUnit(HOST, 'SB', SOLO_B)
    return { ka, kb }
  }
  it('switching A -> B -> A restores the open cards, widths and filter of A, and B keeps its own', () => {
    const { ka, kb } = seedTwo()
    patchViewMemo(HOST, ka, { journalOpen: true, tasksOpen: true, filter: 'task', widths: { journal: 480, roadmap: null, tasks: 400 } })
    setActive('a')
    render(<Shell />)
    expect(screen.getByTestId('journal-card').style.width).toBe('480px')
    expect(q('journal-tasks-card')).toBeTruthy()
    setActive('b')
    expect(q('journal-card')).toBeNull() // B has nothing open
    act(() => patchViewMemo(HOST, kb, { journalOpen: true }))
    expect(screen.getByTestId('journal-card').style.width).toBe(`${CARD_DEFAULT.journal}px`)
    expect(q('journal-tasks-card')).toBeNull()
    setActive('a')
    expect(screen.getByTestId('journal-card').style.width).toBe('480px')
    expect(q('journal-tasks-card')).toBeTruthy()
    expect(readViewMemo(HOST, ka).filter).toBe('task')
  })
  it('hiding and showing the stack again restores the same arrangement', () => {
    const { ka } = seedTwo()
    patchViewMemo(HOST, ka, { journalOpen: true, roadmapOpen: true, widths: { journal: 500, roadmap: 380, tasks: null }, filter: 'confirm', scroll: 120 })
    setActive('a')
    const r = render(<Shell />)
    r.rerender(<Shell stack={false} />)
    expect(q('journal-card-stack')).toBeNull() // really unmounted
    r.rerender(<Shell stack />)
    expect(screen.getByTestId('journal-card').style.width).toBe('500px')
    expect(screen.getByTestId('journal-roadmap-card').style.width).toBe('380px')
    expect(readViewMemo(HOST, ka)).toMatchObject({ filter: 'confirm', scroll: 120 })
  })
  it('toggling journal support off and on remounts the panel areas without losing the arrangement', () => {
    const { ka } = seedTwo()
    patchViewMemo(HOST, ka, { journalOpen: true, widths: { journal: 520, roadmap: null, tasks: null } })
    setActive('a')
    render(<TeamDisplayProvider><PanelAreas /></TeamDisplayProvider>)
    expect(screen.getByTestId('journal-card').style.width).toBe('520px')
    act(() => setJournalHost(HOST, false))
    expect(q('journal-card-stack')).toBeNull()
    act(() => setJournalHost(HOST, true))
    expect(screen.getByTestId('journal-card').style.width).toBe('520px')
  })
})
