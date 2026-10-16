// spa/src/components/journal/TasksCard.test.tsx — the tasks card wired to the real stores (J-2d): the first open reads the tasks, the
// order and the row's parts, and the hover link between a task and its task_new / task_done journal rows, both ways.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { act, cleanup, fireEvent, render, screen, within } from '@testing-library/react'

const getTasks = vi.fn()
vi.mock('../../lib/journal/api', () => ({
  getJournal: vi.fn(),
  getTasks: (...a: unknown[]) => getTasks(...a),
  getRoadmap: vi.fn(),
  setPinned: vi.fn(),
}))
vi.mock('../team/TeamSeatIcon', () => ({
  TeamSeatIcon: () => <span data-testid="seat-icon" />,
  TeamSeatHostBadge: () => <span data-testid="seat-host" />,
}))

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
import type { JournalEntry, JournalTask } from '../../lib/journal/types'

const AT = new Date(2026, 9, 14, 14, 5).getTime()
const entry = (id: string, over: Partial<JournalEntry> = {}): JournalEntry => ({
  id, kind: 'exec', by: '', at: AT, actor: 'Adam', text: `text ${id}`, refs: [], items: [], detail: '', pinned: false, ...over,
})
const task = (number: string, title: string, state: JournalTask['state'], over: Partial<JournalTask> = {}): JournalTask => ({ number, title, state, owner: '', refs: [], detail: '', ...over })

const SOLO = { kind: 'solo' as const, id: 'a', label: 'Solo A' }
const TEAM = { kind: 'team' as const, id: 'tm1', label: 'Journal team' }

const setActive = (id: string) => act(() => { useTabStore.setState({ activeTabId: id }) })
const mount = () => render(<TeamDisplayProvider><CardStack /></TeamDisplayProvider>)
const tasksCard = () => within(screen.getByTestId('journal-tasks-card'))
const taskRow = (key: string) => tasksCard().getAllByTestId('journal-task').find((n) => n.getAttribute('data-task-key') === key)!
const journalRow = (id: string) => within(screen.getByTestId('journal-stream')).getAllByTestId('journal-entry').find((n) => n.getAttribute('data-entry-id') === id)!

beforeEach(() => {
  cleanup()
  localStorage.clear()
  resetTeamStores()
  useJournalStore.getState().reset()
  useJournalUiStore.setState({ lastWidths: {}, teamOpen: {} })
  clearViewMemos()
  clearModuleRegistry()
  getTasks.mockReset()
  Object.defineProperty(window, 'innerWidth', { configurable: true, value: 1800 }) // wide enough for every card
  useShownHostsStore.setState({ ids: [HOST] })
  useI18nStore.getState().setLocale('en')
  seedScene({ members: [['A', 'a-tm']], tabs: [['x', null]], workspaces: [{ id: 'w1', tabs: ['x'] }], activeTabId: 'x' })
  act(() => {
    useTabStore.setState((s) => ({
      tabs: { ...s.tabs, a: ccTabOf('a', HOST, 'a-solo', 'SA'), lead: ccTabOf('lead', HOST, 'lead-tm', 'SL') },
      tabOrder: [...s.tabOrder, 'a', 'lead'],
    }))
  })
  setJournalHost(HOST, true)
})
afterEach(() => { cleanup(); Object.defineProperty(window, 'innerWidth', { configurable: true, value: 1024 }) })

const seedSolo = (tasks: JournalTask[] | null, entries: JournalEntry[] = []) => {
  const key = seedUnit(HOST, 'SA', SOLO, { tasks, entries })
  patchViewMemo(HOST, key, { journalOpen: true, tasksOpen: true })
  setActive('a')
  return key
}

describe('the tasks card: reading', () => {
  it('reads the tasks the first time it opens (tasks === null), once', async () => {
    getTasks.mockResolvedValue({ kind: 'ok', value: [task('1', 'First', 'open')] })
    seedSolo(null)
    mount()
    expect(screen.getByTestId('journal-tasks-loading')).toBeTruthy()
    await act(async () => { await Promise.resolve() })
    expect(getTasks).toHaveBeenCalledTimes(1)
    expect(getTasks).toHaveBeenCalledWith(HOST, 'SA')
    expect(tasksCard().getAllByTestId('journal-task-title').map((n) => n.textContent)).toEqual(['First'])
  })
  it('does not read again when the tasks are held already', async () => {
    seedSolo([task('1', 'First', 'open')])
    mount()
    await act(async () => { await Promise.resolve() })
    expect(getTasks).not.toHaveBeenCalled()
  })
  it('says so when there are no tasks', () => {
    seedSolo([])
    mount()
    expect(screen.getByTestId('journal-tasks-empty').textContent).toBe('No tasks yet')
  })
})

describe('the tasks card: rows', () => {
  it('lists open first, then done, dropped last and struck through', () => {
    seedSolo([task('1', 'Finished', 'done'), task('2', 'Cut', 'dropped'), task('3', 'Working', 'open'), task('4', 'Also finished', 'done')])
    mount()
    expect(tasksCard().getAllByTestId('journal-task-title').map((n) => n.textContent)).toEqual(['Working', 'Finished', 'Also finished', 'Cut'])
    expect(tasksCard().getAllByTestId('journal-task').map((n) => n.getAttribute('data-state'))).toEqual(['open', 'done', 'done', 'dropped'])
    expect(within(taskRow('2')).getByTestId('journal-task-title').className).toContain('line-through')
    expect(within(taskRow('1')).getByTestId('journal-task-title').className).not.toContain('line-through')
  })
  it('a solo task is #n; its sub-title says it is the to-do list', () => {
    seedSolo([task('12', 'Do it', 'open')])
    mount()
    expect(within(taskRow('12')).getByTestId('journal-task-number').textContent).toBe('#12')
    expect(screen.getByTestId('journal-tasks-subtitle').textContent).toBe('To-do (organized by Haiku)')
  })
  it('a team task keeps the file number; its sub-title says team tasks', () => {
    const key = seedUnit(HOST, 'SL', TEAM, { tasks: [task('7bcf08-12', 'Ship', 'open', { owner: 'Adam', refs: ['#2829'] })] })
    patchViewMemo(HOST, key, { journalOpen: true, tasksOpen: true })
    setActive('lead')
    mount()
    const row = taskRow('12')
    expect(within(row).getByTestId('journal-task-number').textContent).toBe('7bcf08-12')
    expect(within(row).getByTestId('journal-task-owner').textContent).toBe('Adam')
    expect(within(row).getByText('#2829')).toBeTruthy()
    expect(screen.getByTestId('journal-tasks-subtitle').textContent).toBe('Team tasks')
  })
  it('shows the state as a dot and a word, with theme tokens only', () => {
    seedSolo([task('1', 'A', 'open'), task('2', 'B', 'done'), task('3', 'C', 'dropped')])
    mount()
    expect(within(taskRow('1')).getByTestId('journal-task-state').textContent).toBe('Open')
    expect(within(taskRow('2')).getByTestId('journal-task-state').textContent).toBe('Done')
    expect(within(taskRow('3')).getByTestId('journal-task-state').textContent).toBe('Dropped')
    expect(within(taskRow('2')).getByTestId('journal-task-state').className).toContain('text-status-success')
    expect(screen.getByTestId('journal-tasks-card').innerHTML).not.toMatch(/#[0-9a-f]{3,8}\b(?![\w-])|rgb\(/i)
  })
  it('highlights the person\'s name in an owner', () => {
    seedSolo([task('1', 'A', 'open', { owner: 'Wake' })])
    mount()
    expect(within(taskRow('1')).getByTestId('journal-person').textContent).toBe('Wake')
  })
})

describe('the hover link between a task and its journal rows', () => {
  const ENTRIES = [
    entry('tn', { kind: 'task_new', text: '#12 Ship it' }),
    entry('ex', { kind: 'exec', text: '#12 Ship it (an exec line, not a task row)' }),
    entry('td', { kind: 'task_done', text: '#12 Ship it', at: AT + 1000 }),
    entry('ghost', { kind: 'task_new', text: '#99 A task the file does not have' }),
  ]
  const seed = () => seedSolo([task('12', 'Ship it', 'open'), task('13', 'Other', 'open')], ENTRIES)
  const linked = (n: HTMLElement) => n.getAttribute('data-linked') === 'true'

  it('hovering a task_new row lights its task, and only that one', () => {
    seed(); mount()
    fireEvent.mouseEnter(journalRow('tn'))
    expect(linked(taskRow('12'))).toBe(true)
    expect(linked(taskRow('13'))).toBe(false)
    fireEvent.mouseLeave(journalRow('tn'))
    expect(linked(taskRow('12'))).toBe(false)
  })
  it('hovering a task_done row lights its task too', () => {
    seed(); mount()
    fireEvent.mouseEnter(journalRow('td'))
    expect(linked(taskRow('12'))).toBe(true)
  })
  it('hovering a task lights both of its journal rows (task_new and task_done), not the others', () => {
    seed(); mount()
    fireEvent.mouseEnter(taskRow('12'))
    expect(linked(journalRow('tn'))).toBe(true)
    expect(linked(journalRow('td'))).toBe(true)
    expect(linked(journalRow('ex'))).toBe(false)
    expect(linked(journalRow('ghost'))).toBe(false)
    fireEvent.mouseLeave(taskRow('12'))
    expect(linked(journalRow('tn'))).toBe(false)
    expect(linked(journalRow('td'))).toBe(false)
  })
  it('a task_new row with no task in the file lights nothing', () => {
    seed(); mount()
    fireEvent.mouseEnter(journalRow('ghost'))
    expect(tasksCard().getAllByTestId('journal-task').some(linked)).toBe(false)
  })
  it('a row that is not a task kind does not trigger the link, even if its text starts with #12', () => {
    seed(); mount()
    fireEvent.mouseEnter(journalRow('ex'))
    expect(tasksCard().getAllByTestId('journal-task').some(linked)).toBe(false)
  })
  it('the hover is momentary: it never reaches the view memo', () => {
    const key = seed(); mount()
    const before = { ...readViewMemo(HOST, key) }
    fireEvent.mouseEnter(taskRow('12'))
    expect(readViewMemo(HOST, key)).toEqual(before)
  })
})
