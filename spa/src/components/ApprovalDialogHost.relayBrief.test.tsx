// spa/src/components/ApprovalDialogHost.relayBrief.test.tsx — UP-5a: the relay cards name the session by title beside its id,
// mark a re-asked request, and show the protagonist's workbook report (last 3 entries + open todos) without ever delaying
// or blocking the decision.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, act, fireEvent } from '@testing-library/react'
import { ApprovalDialogHost } from './ApprovalDialogHost'
import { useApprovalStore } from '../stores/useApprovalStore'
import { useHostStore } from '../stores/useHostStore'
import { useI18nStore } from '../stores/useI18nStore'
import { useUndoToast } from '../stores/useUndoToast'
import { useWorkbookStore } from '../stores/useWorkbookStore'
import { decideApproval } from '../lib/team/approval-api'
import { fetchConversation, fetchTodos } from '../lib/workbook/api'
import { __resetClientDescriptorForTests } from '../lib/team/client-label'
import type { Approval } from '../lib/team/types'
import type { WorkbookEntry, WorkbookTodo } from '../lib/workbook/types'

vi.mock('../lib/team/approval-api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../lib/team/approval-api')>()),
  decideApproval: vi.fn(),
  setSelfRelayPause: vi.fn(),
}))
vi.mock('../lib/workbook/api', () => ({ fetchConversation: vi.fn(), fetchTodos: vi.fn() }))
const mockedConv = vi.mocked(fetchConversation)
const mockedTodos = vi.mocked(fetchTodos)

const H = 'h1'
const entry = (id: number, text: string, over: Partial<WorkbookEntry> = {}): WorkbookEntry => ({
  id, convKey: 'c', sessionId: 'S1', turnId: 't', turnAt: id * 1000, state: 'ok', reason: '', thing: '', push: '', entry: text,
  thingDone: false, createdAt: id * 1000, updatedAt: id * 1000, kind: 'turn',
  usage: { in: 0, out: 0, cacheRead: 0 }, todoChanges: { added: [], done: [], dropped: [] }, ...over,
})
const todo = (id: number, title: string): WorkbookTodo => ({ id, title, detail: '', state: 'open', closedBy: '', createdAt: 1, closedAt: 0, addedEntryId: 0, closedEntryId: 0 })
const page = (entries: WorkbookEntry[]) => ({ kind: 'ok' as const, page: { convKey: 'c', status: '', statusAt: 0, entries, todos: null, refreshAvailable: null } })

const selfRelay = (over: Partial<Approval> = {}, payload: Record<string, unknown> = {}): Approval => ({
  id: 'req-s1', kind: 'self_relay', host_id: 'd1',
  origin: { session_id: 'S1', ref: '_aaaaaa', name: 'purdex-88-b8', pid: 4242, proc_start: 'p', cwd: '/w/purdex', tmux: 'purdex:@1.%2', title: 'purdex-iface-wb', address: 'mlab/purdex-88-b8' },
  payload: { op_id: 'op-1', used_percentage: 71, window: 1000000, ...payload },
  state: 'open', created_at: 1_000, deadline_at: 1_000 + 600_000, lease_until: 1_000 + 600_000, ...over,
})
const memberRelay = (over: Partial<Approval> = {}): Approval => ({
  id: 'req-m1', kind: 'member_relay', host_id: 'd1',
  origin: { session_id: 'LEAD1', ref: '_lllll1', name: 'lead', pid: 1, proc_start: 'p', cwd: '/w', tmux: '', title: 'the-lead' },
  payload: { op_id: 'op-1', team_id: 't-1', lead_ref: '_lllll1', lead_title: 'the-lead', member_session_id: 'M1', member_ref: '_bbbbbb', member_title: 'the-member', used_percentage: 72.4 },
  state: 'open', created_at: 1_000, deadline_at: 1_000 + 600_000, lease_until: 1_000 + 600_000, ...over,
})
const open = (a: Approval) => act(() => { useApprovalStore.getState().applyOpened(H, a) })
const flush = () => act(async () => { await Promise.resolve(); await Promise.resolve() })
const support = (v1: boolean, v2: boolean) => act(() => { useWorkbookStore.getState().setSupport(H, { v1, v2 }) })
const deferred = <T,>() => { let resolve!: (v: T) => void; const promise = new Promise<T>((r) => { resolve = r }); return { promise, resolve } }

beforeEach(() => {
  useI18nStore.getState().setLocale('zh-TW')
  useApprovalStore.getState().reset()
  useHostStore.setState({
    hosts: { [H]: { id: H, name: 'mlab', ip: '1.2.3.4', port: 7860, order: 0 } },
    hostOrder: [H], runtime: { [H]: { status: 'connected' } }, activeHostId: H,
  })
  useUndoToast.setState({ toast: null, notice: null })
  mockedConv.mockReset(); mockedTodos.mockReset()
  mockedConv.mockResolvedValue({ kind: 'not_found' }); mockedTodos.mockResolvedValue({ kind: 'not_found' })
  vi.mocked(decideApproval).mockReset()
  __resetClientDescriptorForTests()
  Object.defineProperty(window, 'electronAPI', { value: undefined, writable: true, configurable: true })
  useWorkbookStore.getState().reset?.()
  support(true, true)
})
afterEach(() => { useHostStore.getState().reset() })

describe('relay card — title beside the id', () => {
  it('self_relay shows origin.title next to the ref; the ref stays', () => {
    render(<ApprovalDialogHost />)
    open(selfRelay())
    expect(screen.getByTestId('approval-ref').textContent).toContain('_aaaaaa')
    expect(screen.getByTestId('approval-title').textContent).toBe('purdex-iface-wb')
  })
  it('member_relay shows member_title next to member_ref (not the lead title)', () => {
    render(<ApprovalDialogHost />)
    open(memberRelay())
    expect(screen.getByTestId('approval-member-ref').textContent).toContain('_bbbbbb')
    expect(screen.getByTestId('approval-title').textContent).toBe('the-member')
  })
  it('a blank title shows nothing', () => {
    render(<ApprovalDialogHost />)
    open(selfRelay({ origin: { ...selfRelay().origin, title: '   ' } }))
    expect(screen.queryByTestId('approval-title')).toBeNull()
  })
  it('strips control and direction characters from the title', () => {
    render(<ApprovalDialogHost />)
    open(selfRelay({ origin: { ...selfRelay().origin, title: 'a‮b\nc' } }))
    expect(screen.getByTestId('approval-title').textContent).toBe('abc')
  })
  it('reask=true marks 重新申請; false or absent does not', () => {
    render(<ApprovalDialogHost />)
    open(selfRelay({}, { reask: true }))
    expect(screen.getByTestId('approval-reask').textContent).toBe('重新申請')
    act(() => { useApprovalStore.getState().reset() })
    open(selfRelay({ id: 'r2' }, { reask: false }))
    expect(screen.queryByTestId('approval-reask')).toBeNull()
    act(() => { useApprovalStore.getState().reset() })
    open(selfRelay({ id: 'r3' }))
    expect(screen.queryByTestId('approval-reask')).toBeNull()
  })
})

describe('relay card — workbook brief', () => {
  it('while loading nothing extra is in the DOM and 核准 is pressable', () => {
    mockedConv.mockReturnValue(new Promise(() => {})); mockedTodos.mockReturnValue(new Promise(() => {}))
    render(<ApprovalDialogHost />)
    open(selfRelay())
    expect(screen.queryByTestId('approval-brief')).toBeNull()
    expect((screen.getByTestId('approval-approve') as HTMLButtonElement).disabled).toBe(false)
  })
  it('shows the newest 3 entries first and the open todos; fetches 10 (shows 3) / open todos on provider claude', async () => {
    mockedConv.mockResolvedValue(page([entry(1, 'old'), entry(3, 'newest'), entry(2, 'mid')]))
    mockedTodos.mockResolvedValue({ kind: 'ok', todos: [todo(1, '寫測試'), todo(2, '開 PR')] })
    render(<ApprovalDialogHost />)
    open(selfRelay())
    await flush()
    expect(mockedConv).toHaveBeenCalledWith(H, 'claude', 'S1', { limit: 10 })
    expect(mockedTodos).toHaveBeenCalledWith(H, 'S1', { state: 'open', limit: 20 })
    const done = screen.getAllByTestId('approval-brief-entry').map((e) => e.textContent)
    expect(done).toEqual(['newest', 'mid', 'old'])
    expect(screen.getAllByTestId('approval-brief-todo').map((e) => e.textContent)).toEqual(['寫測試', '開 PR'])
  })
  it('skips pending/failed/skipped/refresh entries and caps at 3', async () => {
    mockedConv.mockImplementation(async (_h, _p, _s, q) => page([entry(5, 'p', { state: 'pending' }), entry(4, 'f', { state: 'failed' }), entry(3, 'r', { kind: 'refresh' }), entry(2, 'a'), entry(1, 'b'), entry(0, 'c', { thing: 'T' }), entry(-1, 'd')].slice(0, q?.limit)))
    render(<ApprovalDialogHost />)
    open(selfRelay())
    await flush()
    expect(screen.getAllByTestId('approval-brief-entry').map((e) => e.textContent)).toEqual(['a', 'b', 'c'])
  })
  it('only entries: no todos section; only todos: no entries section', async () => {
    mockedConv.mockResolvedValue(page([entry(1, 'did')]))
    render(<ApprovalDialogHost />)
    open(selfRelay())
    await flush()
    expect(screen.getByTestId('approval-brief-entries')).toBeTruthy()
    expect(screen.queryByTestId('approval-brief-todos')).toBeNull()
    act(() => { useApprovalStore.getState().reset() })
    mockedConv.mockResolvedValue(page([])); mockedTodos.mockResolvedValue({ kind: 'ok', todos: [todo(1, 'next')] })
    open(selfRelay({ id: 'r2' }))
    await flush()
    expect(screen.queryByTestId('approval-brief-entries')).toBeNull()
    expect(screen.getByTestId('approval-brief-todos')).toBeTruthy()
  })
  it('errors and 404 show no brief and leave the decision alone', async () => {
    mockedConv.mockRejectedValue(new Error('boom')); mockedTodos.mockRejectedValue(new Error('boom'))
    render(<ApprovalDialogHost />)
    open(selfRelay())
    await flush()
    expect(screen.queryByTestId('approval-brief')).toBeNull()
    expect((screen.getByTestId('approval-approve') as HTMLButtonElement).disabled).toBe(false)
  })
  it('the newest three raw rows all unusable: an older finished turn still shows (the fetch is wider than the quota)', async () => {
    mockedConv.mockImplementation(async (_h, _p, _s, q) => page([entry(9, 'p', { state: 'pending' }), entry(8, 'f', { state: 'failed' }), entry(7, 'r', { kind: 'refresh' }), entry(6, 'older')].slice(0, q?.limit)))
    render(<ApprovalDialogHost />)
    open(selfRelay())
    await flush()
    expect(screen.getAllByTestId('approval-brief-entry').map((e) => e.textContent)).toEqual(['older'])
  })
  it('a half that never answers does not hide the other', async () => {
    mockedConv.mockReturnValue(new Promise(() => {})); mockedTodos.mockResolvedValue({ kind: 'ok', todos: [todo(1, 'next')] })
    render(<ApprovalDialogHost />)
    open(selfRelay())
    await flush()
    expect(screen.getByTestId('approval-brief-todos')).toBeTruthy()
  })
  it('a long todo list is capped in height and scrolls, so 核准 stays reachable', async () => {
    mockedTodos.mockResolvedValue({ kind: 'ok', todos: Array.from({ length: 50 }, (_, i) => todo(i + 1, `t${i}`)) })
    render(<ApprovalDialogHost />)
    open(selfRelay())
    await flush()
    expect(screen.getByTestId('approval-brief').className).toMatch(/max-h-\d+/)
    expect(screen.getByTestId('approval-brief').className).toContain('overflow-y-auto')
    expect(screen.getAllByTestId('approval-brief-todo').length).toBe(20)
  })
  it('one failing half does not hide the other', async () => {
    mockedConv.mockRejectedValue(new Error('boom')); mockedTodos.mockResolvedValue({ kind: 'ok', todos: [todo(1, 'next')] })
    render(<ApprovalDialogHost />)
    open(selfRelay())
    await flush()
    expect(screen.getByTestId('approval-brief-todos')).toBeTruthy()
  })
  it('a host without workbook.v2 never asks for todos; without v1 never for the conversation', async () => {
    support(true, false)
    mockedConv.mockResolvedValue(page([entry(1, 'did')]))
    render(<ApprovalDialogHost />)
    open(selfRelay())
    await flush()
    expect(mockedTodos).not.toHaveBeenCalled()
    expect(screen.getByTestId('approval-brief-entries')).toBeTruthy()
    act(() => { useApprovalStore.getState().reset() })
    mockedConv.mockClear()
    support(false, false)
    open(selfRelay({ id: 'r2' }))
    await flush()
    expect(mockedConv).not.toHaveBeenCalled()
  })
  it('member_relay reads the member, not the lead', async () => {
    mockedConv.mockResolvedValue(page([entry(1, 'did')]))
    render(<ApprovalDialogHost />)
    open(memberRelay())
    await flush()
    expect(mockedConv).toHaveBeenCalledWith(H, 'claude', 'M1', { limit: 10 })
    expect(mockedTodos).toHaveBeenCalledWith(H, 'M1', { state: 'open', limit: 20 })
  })
  it('a late answer for the previous card never lands on the next one', async () => {
    const first = deferred<ReturnType<typeof page>>()
    mockedConv.mockReturnValueOnce(first.promise)
    render(<ApprovalDialogHost />)
    open(selfRelay())
    await flush()
    act(() => { useApprovalStore.getState().reset() })
    mockedConv.mockResolvedValueOnce(page([entry(1, 'second')]))
    open(selfRelay({ id: 'req-s2', origin: { ...selfRelay().origin, session_id: 'S2' } }))
    await flush()
    await act(async () => { first.resolve(page([entry(9, 'STALE')])); await Promise.resolve() })
    expect(screen.queryByText('STALE')).toBeNull()
    expect(screen.getAllByTestId('approval-brief-entry').map((e) => e.textContent)).toEqual(['second'])
  })
  it('minimize and restore keeps the report', async () => {
    mockedConv.mockResolvedValue(page([entry(1, 'did')]))
    render(<ApprovalDialogHost />)
    open(selfRelay())
    await flush()
    fireEvent.click(screen.getByTestId('approval-minimize'))
    act(() => { useApprovalStore.getState().setMinimized(false) })
    expect(screen.getByTestId('approval-brief-entries')).toBeTruthy()
  })
})
