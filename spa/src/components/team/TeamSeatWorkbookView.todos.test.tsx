// spa/src/components/team/TeamSeatWorkbookView.todos.test.tsx — the workbook view's v2 parts 1 and 3: 待辦, the 紀錄｜待辦 switch, the
// done record and its jump into 紀錄.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { TeamSeatWorkbookView } from './TeamSeatWorkbookView'
import { entry, seedWorkbook } from '../../lib/team/__tests__/workbook-fixture'
import { useWorkbookStore, type ConvState } from '../../stores/useWorkbookStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { emptyTodos } from '../../lib/workbook/merge'
import { clearViewMemos } from '../../lib/workbook/view-memory'
import type { WorkbookTodo } from '../../lib/workbook/types'

const fetchConversation = vi.hoisted(() => vi.fn())
vi.mock('../../lib/workbook/api', async (orig) => ({ ...(await orig<typeof import('../../lib/workbook/api')>()), fetchConversation }))

const todo = (id: number, over: Partial<WorkbookTodo> = {}): WorkbookTodo => ({
  id, title: `todo ${id}`, detail: '', state: 'open', closedBy: '', createdAt: 1_760_000_000_000, closedAt: 0, addedEntryId: 1, closedEntryId: 0, ...over,
})
const doneTodo = (id: number, closedEntryId: number, over: Partial<WorkbookTodo> = {}) =>
  todo(id, { state: 'done', closedAt: 1_760_000_100_000, closedEntryId, ...over })
const seed = (over: Partial<ConvState> = {}, v2 = true) => seedWorkbook('h1', 'S', { status: 's', ...over }, { v2 })
const book = (b: Partial<ReturnType<typeof emptyTodos>>) => ({ ...emptyTodos(), ...b })
const mount = () => render(<TeamSeatWorkbookView hostId="h1" sessionId="S" title="T" />)
const settle = () => act(async () => { await Promise.resolve() })
const tab = (name: string) => screen.getByRole('button', { name: new RegExp(`^${name}`) })

beforeEach(() => {
  cleanup()
  useWorkbookStore.getState().reset()
  clearViewMemos()
  fetchConversation.mockReset()
  fetchConversation.mockRejectedValue(new Error('offline'))
  useI18nStore.getState().setLocale('zh-TW')
  Element.prototype.scrollIntoView = vi.fn()
})
afterEach(() => cleanup())

describe('v1 daemon', () => {
  it('has no switch, no 待辦', async () => {
    seed({ entries: [entry(2)] }, false); mount(); await settle()
    expect(screen.queryByTestId('workbook-tabs')).toBeNull()
    expect(screen.queryByText(/待辦/)).toBeNull()
    expect(screen.getAllByTestId('team-seat-workbook-entry')).toHaveLength(1)
  })
})

describe('待辦', () => {
  it('opens on 紀錄; the switch shows open todos with the count in the section header; the detail is the tooltip', async () => {
    seed({ entries: [entry(2)], todos: book({ open: [todo(1, { detail: 'why' }), todo(2)] }) }); mount(); await settle()
    expect(screen.getByTestId('workbook-log')).toBeTruthy()
    fireEvent.click(tab('待辦'))
    expect(screen.queryByTestId('workbook-log')).toBeNull()
    expect(screen.getByText('待辦（2）')).toBeTruthy()
    const row = screen.getAllByTestId('workbook-todo')[0]
    expect(row.textContent).toContain('todo 1')
    expect(row.getAttribute('title')).toBe('why')
  })
  it('a click shows the detail in the row (read only)', async () => {
    seed({ todos: book({ open: [todo(1, { detail: 'the why' })] }) }); mount(); await settle()
    fireEvent.click(tab('待辦'))
    expect(screen.queryByText('the why')).toBeNull()
    fireEvent.click(screen.getAllByTestId('workbook-todo')[0])
    expect(screen.getByText('the why')).toBeTruthy()
  })
  it('hides the open section when there is none', async () => {
    seed({ todos: book({ done: [doneTodo(1, 2)] }) }); mount(); await settle()
    fireEvent.click(tab('待辦'))
    expect(screen.queryByTestId('workbook-open-todos')).toBeNull()
    expect(screen.getByTestId('workbook-done-todos')).toBeTruthy()
  })
  it('says 沒有待辦 when there is nothing at all', async () => {
    seed({}); mount(); await settle()
    fireEvent.click(tab('待辦'))
    expect(screen.getByText('沒有待辦')).toBeTruthy()
  })
  it('says when the lists were cut (openCapped, doneCapped)', async () => {
    seed({ todos: book({ open: [todo(1)], done: [doneTodo(2, 3)], openCapped: true, doneCapped: true }) }); mount(); await settle()
    fireEvent.click(tab('待辦'))
    expect(screen.getByText('待辦太多，只顯示最新的部分。')).toBeTruthy()
    expect(screen.getByText('更早的已完成紀錄未保留。')).toBeTruthy()
  })
  it('is not capped: no notices', async () => {
    seed({ todos: book({ open: [todo(1)], done: [doneTodo(2, 3)] }) }); mount(); await settle()
    fireEvent.click(tab('待辦'))
    expect(screen.queryByText(/只顯示最新/)).toBeNull()
    expect(screen.queryByText(/未保留/)).toBeNull()
  })
})

describe('已完成', () => {
  it('lists the done record newest first with the time, title and the closing entry (its thing, or 重整)', async () => {
    seed({
      entries: [entry(9, { thing: '登入頁' }), entry(8, { kind: 'refresh', thing: '登入頁' })],
      todos: book({ done: [doneTodo(5, 9), doneTodo(4, 8), doneTodo(3, 7)] }),
    }); mount(); await settle()
    fireEvent.click(tab('待辦'))
    const rows = screen.getAllByTestId('workbook-done-todo')
    expect(rows.map((r) => r.getAttribute('data-todo-id'))).toEqual(['5', '4', '3'])
    expect(rows[0].textContent).toContain('todo 5')
    expect(rows[0].textContent).toContain('登入頁')
    expect(rows[1].textContent).toContain('重整')
    expect(within(rows[0]).getByTestId('workbook-done-time').textContent).not.toBe('')
  })
})

describe('a done todo jumps into 紀錄', () => {
  const seedDone = (closed: number, entries = [entry(9, { thing: 'T9' })]) =>
    seed({ entries, oldestId: Math.min(...entries.map((e) => e.id)), todos: book({ done: [doneTodo(1, closed)] }) })

  it('an entry already loaded: switches to 紀錄, scrolls to it and marks it', async () => {
    seedDone(9)
    const loadUntil = vi.fn().mockResolvedValue(true)
    useWorkbookStore.setState({ loadUntil })
    mount(); await settle()
    fireEvent.click(tab('待辦'))
    await act(async () => { fireEvent.click(screen.getByTestId('workbook-done-todo')) })
    expect(loadUntil).toHaveBeenCalledWith('h1', 'c-S', 9)
    expect(screen.getByTestId('workbook-log')).toBeTruthy()
    const row = document.querySelector('[data-entry-id="9"]') as HTMLElement
    expect(row.getAttribute('data-highlighted')).toBe('true')
    expect(Element.prototype.scrollIntoView).toHaveBeenCalled()
  })
  it('an entry on a later page: loadUntil brings it in, the finished group opens, then it is marked', async () => {
    seed({
      entries: [entry(30, { thing: 'new' })], oldestId: 30,
      todos: book({ done: [doneTodo(1, 4)] }),
    })
    useWorkbookStore.setState({
      loadUntil: vi.fn(async (h: string, k: string) => {
        useWorkbookStore.getState().applyEntry(h, { convKey: k, sessionId: 'S', entry: entry(4, { thing: 'old', thingDone: true }) } as never)
        return true
      }),
    })
    mount(); await settle()
    fireEvent.click(tab('待辦'))
    await act(async () => { fireEvent.click(screen.getByTestId('workbook-done-todo')) })
    await waitFor(() => expect(document.querySelector('[data-entry-id="4"]')?.getAttribute('data-highlighted')).toBe('true'))
  })
  it('not found: stays at 紀錄 and says 找不到這筆紀錄', async () => {
    seedDone(5)
    useWorkbookStore.setState({ loadUntil: vi.fn().mockResolvedValue(false) })
    mount(); await settle()
    fireEvent.click(tab('待辦'))
    await act(async () => { fireEvent.click(screen.getByTestId('workbook-done-todo')) })
    expect(screen.getByTestId('workbook-log')).toBeTruthy()
    expect(screen.getByText('找不到這筆紀錄')).toBeTruthy()
  })
  it('a closing entry id of 0 (an open todo\'s) never calls loadUntil', async () => {
    seedDone(0)
    const loadUntil = vi.fn().mockResolvedValue(true)
    useWorkbookStore.setState({ loadUntil })
    mount(); await settle()
    fireEvent.click(tab('待辦'))
    await act(async () => { fireEvent.click(screen.getByTestId('workbook-done-todo')) })
    expect(loadUntil).not.toHaveBeenCalled()
    expect(screen.getByText('找不到這筆紀錄')).toBeTruthy()
  })
})
