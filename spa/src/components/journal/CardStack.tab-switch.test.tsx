// Regression (CLAUDE.md, Tab-hosted checklist): what the person arranged in the journal card stack — which cards are open, their widths,
// the filter, the scroll position of the stream, the opened 待確認 detail — must survive switching to another tab and back, and a
// remount of the whole stack. The alive pool keeps no tab by default (keepAliveCount 0), so a heavy tab's pane really unmounts: this
// mounts the real TabContent beside the stack, like the shell does. The stack is a TabContent sibling, so it is not unmounted by a
// switch; what is is the unit's cards (the next tab is another unit) — and the second test unmounts the stack itself.
// Pattern: components/team/TeamSeatWorkbookView.tab-switch.test.tsx.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { act, cleanup, fireEvent, render, screen, within } from '@testing-library/react'

vi.mock('../team/TeamSeatIcon', () => ({
  TeamSeatIcon: () => <span data-testid="seat-icon" />,
  TeamSeatHostBadge: () => <span data-testid="seat-host" />,
}))

import { CardStack } from './CardStack'
import { TabContent } from '../TabContent'
import { TeamDisplayProvider } from '../team/TeamDisplayProvider'
import { HOST, resetTeamStores, seedScene } from '../../lib/team/__tests__/team-fixture'
import { ccTabOf, seedUnit, setJournalHost } from '../../lib/journal/__tests__/journal-fixture'
import { clearViewMemos, patchViewMemo, readViewMemo } from '../../lib/journal/view-memory'
import { clearModuleRegistry, registerModule } from '../../lib/module-registry'
import { useTabStore } from '../../stores/useTabStore'
import { useJournalStore } from '../../stores/useJournalStore'
import { useJournalUiStore } from '../../stores/useJournalUiStore'
import { useShownHostsStore } from '../../stores/useShownHostsStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { useUISettingsStore } from '../../stores/useUISettingsStore'
import type { JournalEntry } from '../../lib/journal/types'
import type { PaneRendererProps } from '../../lib/module-registry'

// jsdom has no layout: every divider / row is 20px tall, the stream's viewport 100px, and scrollTop clamps like a browser's.
const ROW = 20
const VIEW = 100
const scrollTops = new WeakMap<Element, number>()
const heightOf = (el: Element) => el.querySelectorAll('[data-testid="journal-entry"], [data-testid="journal-day"]').length * ROW
const maxTop = (el: Element) => Math.max(0, heightOf(el) - VIEW)

const AT = new Date(2026, 9, 14, 9, 0).getTime()
const taskEntry = (n: number): JournalEntry => ({ id: `t${String(n).padStart(3, '0')}`, kind: 'task_new', by: '', at: AT + n * 60_000, actor: 'Adam', text: `#${n} task ${n}`, refs: [], items: [], detail: '', pinned: false })
const TASKS = Array.from({ length: 12 }, (_, i) => taskEntry(i + 1))
const CONFIRM: JournalEntry = {
  id: 'c1', kind: 'confirm', by: '', at: AT + 99 * 60_000, actor: 'Kenji → Wake', text: 'decide', refs: [], pinned: true,
  items: [{ text: 'q1', done: false }], detail: 'the original question',
}
const SOLO_A = { kind: 'solo' as const, id: 'a', label: 'Solo A' }
const SOLO_B = { kind: 'solo' as const, id: 'b', label: 'Solo B' }

// A heavy pane kind (tmux-session is not on lib/pane-weight.ts's light list): switching away really unmounts it.
const Probe = ({ pane }: PaneRendererProps) => <div data-testid="pane-probe" data-pane={pane.id} />
const setActive = (id: string) => act(() => { useTabStore.setState({ activeTabId: id }) })
const q = (id: string) => screen.queryByTestId(id)
const stream = () => screen.getByTestId('journal-stream')

function Shell({ stack = true }: { stack?: boolean }) {
  const activeTabId = useTabStore((s) => s.activeTabId)
  const tabs = useTabStore((s) => s.tabs)
  return (
    <TeamDisplayProvider>
      <TabContent activeTab={activeTabId ? tabs[activeTabId] : null} allTabs={Object.values(tabs)} />
      {stack && <CardStack />}
    </TeamDisplayProvider>
  )
}

/** Switch to the other tab and back, asserting that the first tab's pane was really torn down in between. */
const awayAndBack = () => {
  setActive('b')
  expect(document.querySelector('[data-pane="p-a"]')).toBeNull() // really gone, not merely hidden
  setActive('a')
  expect(document.querySelector('[data-pane="p-a"]')).not.toBeNull()
}

beforeEach(() => {
  cleanup()
  localStorage.clear()
  resetTeamStores()
  useJournalStore.getState().reset()
  useJournalUiStore.setState({ lastWidths: {} })
  clearViewMemos()
  clearModuleRegistry()
  registerModule({ id: 'probe', name: 'probe', panes: [{ kind: 'tmux-session', component: Probe }] })
  useUISettingsStore.setState({ keepAliveCount: 0 })
  useShownHostsStore.setState({ ids: [HOST] })
  useI18nStore.getState().setLocale('en')
  Object.defineProperty(window, 'innerWidth', { configurable: true, value: 2200 }) // wide enough for every card
  Object.defineProperty(HTMLElement.prototype, 'scrollHeight', { configurable: true, get() { return heightOf(this) } })
  Object.defineProperty(HTMLElement.prototype, 'clientHeight', { configurable: true, get() { return VIEW } })
  Object.defineProperty(HTMLElement.prototype, 'offsetTop', {
    configurable: true,
    get() {
      const all = Array.from(this.closest('[data-testid="journal-stream"]')?.querySelectorAll('[data-testid="journal-entry"], [data-testid="journal-day"]') ?? [])
      return Math.max(0, all.indexOf(this)) * ROW
    },
  })
  Object.defineProperty(HTMLElement.prototype, 'scrollTop', {
    configurable: true,
    get() { return scrollTops.get(this) ?? 0 },
    set(v: number) { scrollTops.set(this, Math.max(0, Math.min(maxTop(this), v))) },
  })
  seedScene({ members: [['A', 'a-tm']], tabs: [['x', null]], workspaces: [{ id: 'w1', tabs: ['x'] }], activeTabId: 'x' })
  act(() => {
    useTabStore.setState((s) => ({
      tabs: { ...s.tabs, a: ccTabOf('a', HOST, 'a-solo', 'SA'), b: ccTabOf('b', HOST, 'b-solo', 'SB') },
      tabOrder: [...s.tabOrder, 'a', 'b'],
    }))
  })
  setJournalHost(HOST, true)
  seedUnit(HOST, 'SA', SOLO_A, { entries: TASKS, pinned: [CONFIRM], openConfirms: 1, tasks: [], roadmap: [] })
  seedUnit(HOST, 'SB', SOLO_B, { entries: TASKS, pinned: [], tasks: [], roadmap: [] })
  patchViewMemo(HOST, 'solo:a', { journalOpen: true, widths: { journal: 480, roadmap: 380, tasks: 400 } })
  setActive('a')
})
afterEach(() => {
  cleanup()
  Object.defineProperty(window, 'innerWidth', { configurable: true, value: 1024 })
  for (const p of ['scrollHeight', 'clientHeight', 'offsetTop', 'scrollTop']) delete (HTMLElement.prototype as unknown as Record<string, unknown>)[p]
})

/** The arrangement the person makes by hand: tasks and roadmap opened, the 任務 filter, the stream scrolled up, a 待確認 detail opened. */
function arrange() {
  fireEvent.click(screen.getByTestId('journal-toggle-tasks'))
  fireEvent.click(screen.getByTestId('journal-toggle-roadmap'))
  fireEvent.click(screen.getByTestId('journal-filter-task'))
  stream().scrollTop = 40
  fireEvent.scroll(stream())
  fireEvent.click(within(screen.getByTestId('journal-pinned-band')).getByTestId('journal-row-open'))
}

/** Everything `arrange` made, as the screen shows it. */
function expectArranged() {
  expect(q('journal-card')).toBeTruthy()
  expect(screen.getByTestId('journal-card').style.width).toBe('480px')
  expect(q('journal-tasks-card')).toBeTruthy()
  expect(screen.getByTestId('journal-tasks-card').style.width).toBe('400px')
  expect(screen.getByTestId('journal-toggle-tasks').getAttribute('aria-pressed')).toBe('true')
  expect(screen.getByTestId('journal-toggle-roadmap').getAttribute('aria-pressed')).toBe('true')
  expect(screen.getByTestId('journal-filter-task').getAttribute('aria-pressed')).toBe('true')
  expect(screen.getByTestId('journal-filter-all').getAttribute('aria-pressed')).toBe('false')
  expect(within(stream()).getAllByTestId('journal-entry').length).toBe(12) // the filter really narrows the stream, too
  expect(stream().scrollTop).toBe(40)
  expect(q('journal-detail-card')).toBeTruthy() // the opened 待確認 detail takes the roadmap's place
  expect(screen.getByTestId('journal-detail-card').style.width).toBe('380px')
  expect(screen.getByTestId('journal-detail-card').textContent).toContain('the original question')
  expect(q('journal-roadmap-card')).toBeNull()
}

describe('the card stack across tab switches', () => {
  it('switching away and back restores the open cards, widths, filter, scroll and the opened detail', () => {
    render(<Shell />)
    arrange()
    awayAndBack()
    expectArranged()
  })
  it('closing the detail brings back the roadmap that was open under it, after a switch too', () => {
    render(<Shell />)
    arrange()
    awayAndBack()
    fireEvent.click(screen.getByTestId('journal-detail-card-close'))
    expect(q('journal-detail-card')).toBeNull()
    expect(q('journal-roadmap-card')).toBeTruthy()
    expect(screen.getByTestId('journal-roadmap-card').style.width).toBe('380px')
  })
  it('another unit\'s tab does not inherit any of it, and gets its own arrangement back too', () => {
    render(<Shell />)
    arrange()
    setActive('b')
    expect(q('journal-card')).toBeNull() // B has nothing open
    expect(readViewMemo(HOST, 'solo:b')).toMatchObject({ journalOpen: false, tasksOpen: false, roadmapOpen: false, filter: 'all', detailId: null, scroll: 0 })
    act(() => patchViewMemo(HOST, 'solo:b', { journalOpen: true }))
    expect(screen.getByTestId('journal-card').style.width).not.toBe('480px')
    expect(screen.getByTestId('journal-filter-all').getAttribute('aria-pressed')).toBe('true')
    expect(q('journal-tasks-card')).toBeNull()
    expect(q('journal-detail-card')).toBeNull()
    fireEvent.click(screen.getByTestId('journal-filter-comm'))
    setActive('a')
    expectArranged()
    setActive('b')
    expect(screen.getByTestId('journal-filter-comm').getAttribute('aria-pressed')).toBe('true')
  })
})

describe('the card stack unmounted and mounted again', () => {
  it('hiding the whole stack and showing it again restores the same arrangement', () => {
    const r = render(<Shell />)
    arrange()
    r.rerender(<Shell stack={false} />)
    expect(q('journal-card-stack')).toBeNull() // really unmounted
    r.rerender(<Shell stack />)
    expectArranged()
  })
  it('the stack remounting while the tab is away restores it as well', () => {
    const r = render(<Shell />)
    arrange()
    setActive('b')
    r.rerender(<Shell stack={false} />)
    r.rerender(<Shell stack />)
    setActive('a')
    expectArranged()
  })
})
