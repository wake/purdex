// spa/src/components/journal/JournalDetail.test.tsx — the 待確認 detail card (J-2c, spec §11): it opens to the LEFT of the journal card in
// the roadmap's place, one at a time, Esc / a second click closes it; the roadmap's own open memory is never touched, and the narrow
// window drops it with the roadmap (visibility only: the open detail id stays).
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { act, cleanup, fireEvent, render, screen, within } from '@testing-library/react'
import { CardStack } from './CardStack'
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

const UNIT = { kind: 'solo' as const, id: 'a', label: 'Solo A' }
const AT = new Date(2026, 9, 14, 14, 5).getTime()
const entry = (id: string, over: Partial<JournalEntry> = {}): JournalEntry => ({
  id, kind: 'exec', by: '', at: AT, actor: 'Adam', text: `text ${id}`, refs: [], items: [], detail: '', pinned: false, ...over,
})
const confirm = (id: string, over: Partial<JournalEntry> = {}) => entry(id, {
  kind: 'confirm', actor: 'Kenji → Wake', pinned: true, detail: `original question of ${id}\nsecond line`,
  items: [{ text: `${id} one`, done: true }, { text: `${id} two`, done: false }], ...over,
})
const C1 = confirm('c1', { at: AT + 1000 })
const C2 = confirm('c2', { at: AT + 2000, pinned: false })

const observers = new Set<(w: number) => void>()
class FakeRO {
  private f: (w: number) => void
  constructor(cb: ResizeObserverCallback) { this.f = (w) => cb([{ contentRect: { width: w } } as ResizeObserverEntry], this as unknown as ResizeObserver) }
  observe() { observers.add(this.f) }
  unobserve() {}
  disconnect() { observers.delete(this.f) }
}
const setWidth = (w: number) => act(() => { Object.defineProperty(window, 'innerWidth', { configurable: true, value: w }); window.dispatchEvent(new Event('resize')); observers.forEach((f) => f(w)) })
const q = (id: string) => screen.queryByTestId(id)
const rowOf = (id: string, area: 'journal-stream' | 'journal-pinned-band' = 'journal-stream') =>
  within(screen.getByTestId(area)).getAllByTestId('journal-entry').find((n) => n.getAttribute('data-entry-id') === id)!

let key = ''
const mount = (over: { roadmapOpen?: boolean } = {}) => {
  key = seedUnit(HOST, 'SA', UNIT, { entries: [entry('e1'), C1, C2], pinned: [C1], openConfirms: 3 })
  patchViewMemo(HOST, key, { journalOpen: true, ...over })
  act(() => { useTabStore.setState({ activeTabId: 'a' }) })
  return render(<TeamDisplayProvider><CardStack /></TeamDisplayProvider>)
}

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
  useI18nStore.getState().setLocale('en')
  setWidth(1500)
  seedScene({ members: [['A', 'a-tm']], tabs: [['x', null]], workspaces: [{ id: 'w1', tabs: ['x'] }], activeTabId: 'x' })
  act(() => { useTabStore.setState((s) => ({ tabs: { ...s.tabs, a: ccTabOf('a', HOST, 'a-solo', 'SA') }, tabOrder: [...s.tabOrder, 'a'] })) })
  setJournalHost(HOST, true)
})
afterEach(() => { cleanup(); vi.unstubAllGlobals(); Object.defineProperty(window, 'innerWidth', { configurable: true, value: 1024 }) })

describe('the 待確認 detail', () => {
  it('a click on a 待確認 row opens its detail to the left of the journal card, with the original text and the items', () => {
    mount()
    expect(q('journal-detail-card')).toBeNull()
    fireEvent.click(rowOf('c1'))
    const stack = screen.getByTestId('journal-card-stack')
    expect([...stack.children].map((c) => (c as HTMLElement).dataset.testid)).toEqual(['journal-team-column', 'journal-card'])
    expect(within(screen.getByTestId('journal-team-column')).getByTestId('journal-detail-card')).toBeTruthy()
    expect(screen.getByTestId('journal-detail-text').textContent).toBe('original question of c1\nsecond line')
    expect(within(screen.getByTestId('journal-detail-items')).getAllByRole('listitem').map((li) => li.textContent)).toEqual(['c1 one', 'c1 two'])
    expect(screen.queryAllByRole('checkbox')).toEqual([])
    expect(readViewMemo(HOST, key).detailId).toBe('c1')
  })
  it('the original sits in its own scrollable block', () => {
    mount()
    fireEvent.click(rowOf('c1'))
    expect(screen.getByTestId('journal-detail-text').className).toContain('overflow-y-auto')
  })
  it('a second click on the same row closes it', () => {
    mount()
    fireEvent.click(rowOf('c1'))
    fireEvent.click(rowOf('c1'))
    expect(q('journal-detail-card')).toBeNull()
    expect(readViewMemo(HOST, key).detailId).toBeNull()
  })
  it('Esc closes it, and the close button does too', () => {
    mount()
    fireEvent.click(rowOf('c1'))
    fireEvent.keyDown(window, { key: 'Escape' })
    expect(q('journal-detail-card')).toBeNull()
    fireEvent.click(rowOf('c1'))
    fireEvent.click(screen.getByTestId('journal-detail-card-close'))
    expect(q('journal-detail-card')).toBeNull()
    expect(readViewMemo(HOST, key).detailId).toBeNull()
  })
  it('Esc typed into the terminal or a text field is that field\'s, not the detail\'s (M: a bare window listener)', () => {
    mount()
    fireEvent.click(rowOf('c1'))
    const term = document.createElement('div')
    term.className = 'xterm'
    const area = document.createElement('textarea')
    term.appendChild(area)
    const input = document.createElement('input')
    document.body.append(term, input)
    fireEvent.keyDown(area, { key: 'Escape' })
    fireEvent.keyDown(input, { key: 'Escape' })
    expect(q('journal-detail-card')).toBeTruthy()
    fireEvent.keyDown(document.body, { key: 'Escape' })
    expect(q('journal-detail-card')).toBeNull()
    term.remove()
    input.remove()
  })
  it('only one is open at a time: another row replaces it', () => {
    mount()
    fireEvent.click(rowOf('c1'))
    fireEvent.click(rowOf('c2'))
    expect(screen.getAllByTestId('journal-detail')).toHaveLength(1)
    expect(screen.getByTestId('journal-detail-text').textContent).toContain('original question of c2')
    expect(readViewMemo(HOST, key).detailId).toBe('c2')
  })
  it('a row of the pinned band opens it too', () => {
    mount()
    fireEvent.click(rowOf('c1', 'journal-pinned-band'))
    expect(screen.getByTestId('journal-detail-text').textContent).toContain('original question of c1')
  })
  it('it takes the roadmap place; the roadmap memory is untouched and the roadmap is back once it closes (M: closing the roadmap)', () => {
    mount({ roadmapOpen: true })
    expect(q('journal-roadmap-card')).toBeTruthy()
    fireEvent.click(rowOf('c1'))
    expect(q('journal-roadmap-card')).toBeNull()
    expect(q('journal-detail-card')).toBeTruthy()
    expect(readViewMemo(HOST, key).roadmapOpen).toBe(true)
    fireEvent.keyDown(window, { key: 'Escape' })
    expect(q('journal-detail-card')).toBeNull()
    expect(q('journal-roadmap-card')).toBeTruthy()
    expect(readViewMemo(HOST, key).roadmapOpen).toBe(true)
  })
  it('with the roadmap closed, closing the detail leaves no roadmap behind', () => {
    mount({ roadmapOpen: false })
    fireEvent.click(rowOf('c1'))
    fireEvent.click(rowOf('c1'))
    expect(q('journal-roadmap-card')).toBeNull()
    expect(readViewMemo(HOST, key).roadmapOpen).toBe(false)
  })
  it('a narrow window hides it with the roadmap and brings it back when wide again; the detail id never changes (M: write the hide back)', () => {
    mount()
    fireEvent.click(rowOf('c1'))
    expect(q('journal-detail-card')).toBeTruthy()
    setWidth(800)
    expect(q('journal-detail-card')).toBeNull()
    expect(q('journal-card')).toBeTruthy()
    expect(readViewMemo(HOST, key).detailId).toBe('c1')
    setWidth(1500)
    expect(q('journal-detail-card')).toBeTruthy()
    expect(readViewMemo(HOST, key).detailId).toBe('c1')
  })
  it('a detail id of an entry the unit does not hold shows nothing', () => {
    mount()
    patchViewMemo(HOST, key, { detailId: 'gone' })
    expect(q('journal-detail-card')).toBeNull()
    expect(q('journal-card')).toBeTruthy()
  })
  it('a closed journal card shows no detail', () => {
    mount()
    patchViewMemo(HOST, key, { detailId: 'c1', journalOpen: false })
    expect(q('journal-detail-card')).toBeNull()
  })
})
