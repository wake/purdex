// spa/src/lib/team/approval-ws.test.ts — the reconnect snapshot and the decisions queued while the host was away
// (lead-team spec §6.3 "During a daemon restart", §9.4): the snapshot re-adds the request → the queued decision
// is sent, once; the snapshot shows it gone → toast `approval.toast.ended_while_away`; a resend that fails on the
// network while the host is still down stays queued for the next snapshot.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { useApprovalStore, approvalKey } from '../../stores/useApprovalStore'
import { useHostStore } from '../../stores/useHostStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { useUndoToast } from '../../stores/useUndoToast'
import { useTabStore } from '../../stores/useTabStore'
import { useWorkspaceStore } from '../../stores/useWorkspaceStore'
import { useSessionStore } from '../../stores/useSessionStore'
import { useShownHostsStore } from '../../stores/useShownHostsStore'
import { usePaneFocusStore } from '../../stores/usePaneFocusStore'
import { ApprovalApiError, decideApproval } from './approval-api'
import { __resetClientDescriptorForTests } from './client-label'
import { handleApprovalEvent, parseApprovalEvent } from './approval-ws'
import type { Approval } from './types'
import type { PaneContent, PaneLayout, Tab } from '../../types/tab'

vi.mock('./approval-api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('./approval-api')>()),
  decideApproval: vi.fn(),
}))
const mockedDecide = vi.mocked(decideApproval)

const H = 'h1'
const approval = (over: Partial<Approval> = {}): Approval => ({
  id: 'req-1', kind: 'lead', host_id: 'd1',
  origin: { session_id: 'S1', ref: '_40iueq', name: 'purdex-7c', pid: 1, proc_start: 'p', cwd: '/w/purdex', tmux: '' },
  payload: { reason: 'r', max_members: 3, roots: ['/w/purdex'] },
  state: 'open', created_at: 1_000, deadline_at: 541_000, lease_until: 31_000,
  ...over,
})
const snapshot = (approvals: Approval[]) => JSON.stringify({ op: 'snapshot', approvals })
const flush = () => new Promise<void>((r) => setTimeout(r, 0))

beforeEach(() => {
  useI18nStore.getState().setLocale('zh-TW')
  useApprovalStore.getState().reset()
  useHostStore.setState({
    hosts: { [H]: { id: H, name: 'mlab', ip: '1.2.3.4', port: 7860, order: 0 } },
    hostOrder: [H],
    runtime: { [H]: { status: 'connected' } },
    activeHostId: H,
  })
  useUndoToast.setState({ toast: null, notice: null })
  mockedDecide.mockReset()
  __resetClientDescriptorForTests()
  Object.defineProperty(window, 'electronAPI', { value: undefined, writable: true, configurable: true })
})
afterEach(() => useHostStore.getState().reset())

describe('approval-ws reconnect (spec §9.4)', () => {
  it('the snapshot re-adds the request → the queued decision is sent once, with its grant; a second snapshot sends nothing', async () => {
    const a = approval()
    useApprovalStore.getState().applyOpened(H, a)
    useApprovalStore.getState().queueDecision(H, a, 'approve', { max_members: 2, roots: ['/w/purdex'] })
    mockedDecide.mockResolvedValueOnce(approval({ state: 'approved' }))
    handleApprovalEvent(H, snapshot([a]))
    await flush()
    expect(mockedDecide).toHaveBeenCalledTimes(1)
    expect(mockedDecide.mock.calls[0]).toEqual([H, 'req-1', { decision: 'approve', grant: { max_members: 2, roots: ['/w/purdex'] }, client: { kind: 'app', label: 'Purdex.app' } }])
    expect(useApprovalStore.getState().entries).toEqual({})
    expect(useApprovalStore.getState().queued).toEqual({})
    expect(useUndoToast.getState().toast).toBeNull()
    handleApprovalEvent(H, snapshot([]))
    await flush()
    expect(mockedDecide).toHaveBeenCalledTimes(1)
  })

  it('the snapshot shows the request gone → toast ended_while_away, nothing sent, the queue is empty', async () => {
    const a = approval()
    useApprovalStore.getState().applyOpened(H, a)
    useApprovalStore.getState().queueDecision(H, a, 'deny')
    handleApprovalEvent(H, snapshot([approval({ id: 'other', created_at: 2_000 })]))
    await flush()
    expect(mockedDecide).not.toHaveBeenCalled()
    expect(useUndoToast.getState().toast?.message).toBe('mlab：purdex-7c 的 lead 申請已在離線期間結束，你的決定未送出')
    expect(useApprovalStore.getState().queued).toEqual({})
    expect(Object.keys(useApprovalStore.getState().entries)).toEqual([approvalKey(H, 'other')])
  })

  it('the resend answers 409 already_decided → closed with the "handled by" toast', async () => {
    const a = approval()
    useApprovalStore.getState().applyOpened(H, a)
    useApprovalStore.getState().queueDecision(H, a, 'deny')
    mockedDecide.mockRejectedValueOnce(new ApprovalApiError(409, 'already_decided', '', approval({ state: 'approved', decided_by: { kind: 'app', label: 'Purdex.app @ air26' } })))
    handleApprovalEvent(H, snapshot([a]))
    await flush()
    expect(useApprovalStore.getState().entries).toEqual({})
    expect(useUndoToast.getState().toast?.message).toBe('mlab：purdex-7c 的 lead 申請 已由 Purdex.app @ air26 核准')
  })

  it('the resend fails on the network while the host is down again → it stays queued for the next snapshot', async () => {
    // submitDecision queues a network failure only while the host is not `connected` (approval-decide.ts): the
    // snapshot arrived and the socket dropped again before the resend answered.
    useHostStore.getState().setRuntime(H, { status: 'reconnecting' })
    const a = approval()
    useApprovalStore.getState().applyOpened(H, a)
    useApprovalStore.getState().queueDecision(H, a, 'deny')
    mockedDecide.mockRejectedValueOnce(new ApprovalApiError(0, 'network', 'ECONNREFUSED'))
    handleApprovalEvent(H, snapshot([a]))
    await flush()
    expect(useApprovalStore.getState().queued[approvalKey(H, 'req-1')]).toMatchObject({ decision: 'deny' })
    mockedDecide.mockResolvedValueOnce(approval({ state: 'denied' }))
    handleApprovalEvent(H, snapshot([a]))
    await flush()
    expect(mockedDecide).toHaveBeenCalledTimes(2)
    expect(useApprovalStore.getState().queued).toEqual({})
  })

  it('the resend fails on the network while the runtime still reads connected → re-queued anyway (F3), once, no toast; the next snapshot resends it', async () => {
    // The daemon died again right after the snapshot, before the host runtime noticed: a decision taken from the
    // queue must never be dropped on a network failure, whatever the runtime says.
    expect(useHostStore.getState().runtime[H]?.status).toBe('connected')
    const a = approval()
    useApprovalStore.getState().applyOpened(H, a)
    useApprovalStore.getState().queueDecision(H, a, 'deny')
    mockedDecide.mockRejectedValueOnce(new ApprovalApiError(0, 'network', 'Failed to fetch'))
    handleApprovalEvent(H, snapshot([a]))
    await flush()
    expect(mockedDecide).toHaveBeenCalledTimes(1)
    expect(useApprovalStore.getState().queued).toEqual({ [approvalKey(H, 'req-1')]: { hostId: H, approval: a, decision: 'deny', grant: undefined } })
    expect(useUndoToast.getState().toast).toBeNull()
    expect(useApprovalStore.getState().decidedHere).toEqual({})
    mockedDecide.mockResolvedValueOnce(approval({ state: 'denied' }))
    handleApprovalEvent(H, snapshot([a]))
    await flush()
    expect(mockedDecide).toHaveBeenCalledTimes(2)
    expect(useApprovalStore.getState().queued).toEqual({})
    expect(useApprovalStore.getState().entries).toEqual({})
  })

  it('another host\'s queue is left alone', async () => {
    const a = approval()
    useApprovalStore.getState().applyOpened('h2', a)
    useApprovalStore.getState().queueDecision('h2', a, 'deny')
    handleApprovalEvent(H, snapshot([]))
    await flush()
    expect(mockedDecide).not.toHaveBeenCalled()
    expect(useApprovalStore.getState().queued[approvalKey('h2', 'req-1')]).toBeDefined()
  })
})

// U22 (a), plan v3 P9b-1 rules 1–2: the queue is per renderer, so a click queued while the daemon was away switches
// this window to the requester when its resend lands; a `closed` from another client never switches. Real tab stores.
describe('approval-ws: back to the requester (U22)', () => {
  const PAYLOADS = {
    lead: { reason: 'r', max_members: 3, roots: ['/w/purdex'] },
    self_relay: { op_id: 'op-1', used_percentage: 71, window: 200_000 },
  }
  const requester = (kind: 'lead' | 'self_relay' = 'lead') =>
    approval({ kind, payload: PAYLOADS[kind], origin: { ...approval().origin, tmux: 'purdex:@1.%2' } })
  const pane = (id: string, content: PaneContent): PaneLayout => ({ type: 'leaf', pane: { id, content } })
  const tab = (id: string, layout: PaneLayout): Tab => ({ id, pinned: false, locked: false, createdAt: 0, layout })

  beforeEach(() => {
    useHostStore.setState({ hostOrder: [H] })
    useShownHostsStore.setState({ ids: [H] })
    useSessionStore.setState({ sessions: { [H]: [{ code: 'c01', name: 'purdex', cwd: '/w/purdex', mode: 'terminal' }] }, activeHostId: null, activeCode: null })
    useWorkspaceStore.getState().reset()
    usePaneFocusStore.setState({ recent: {}, focusRequest: null })
    // The requester's session in tab tS; the person is on tab tO.
    const tS = tab('tS', pane('pS', { kind: 'tmux-session', hostId: H, sessionCode: 'c01', mode: 'terminal', cachedName: 'purdex', tmuxInstance: '' }))
    const tO = tab('tO', pane('pO', { kind: 'new-tab' }))
    useTabStore.setState({ tabs: { tS, tO }, tabOrder: ['tS', 'tO'], activeTabId: 'tO', visitHistory: [] })
  })

  it.each(['lead', 'self_relay'] as const)('a queued decision resent on the reconnect snapshot switches on its 200 (%s)', async (kind) => {
    const a = requester(kind)
    useApprovalStore.getState().applyOpened(H, a)
    useApprovalStore.getState().queueDecision(H, a, 'approve')
    mockedDecide.mockResolvedValueOnce({ ...a, state: 'approved' })
    handleApprovalEvent(H, snapshot([a]))
    await flush()
    expect(mockedDecide).toHaveBeenCalledTimes(1)
    expect(useTabStore.getState().activeTabId).toBe('tS')
    expect(usePaneFocusStore.getState().recent.tS?.[0]).toBe('pS')
    expect(useTabStore.getState().tabOrder).toEqual(['tS', 'tO'])
  })

  it('a closed event from another client switches nothing', () => {
    const a = requester()
    useApprovalStore.getState().applyOpened(H, a)
    const tabsBefore = useTabStore.getState().tabs
    handleApprovalEvent(H, JSON.stringify({ op: 'closed', approval: { ...a, state: 'approved', decided_by: { kind: 'app', label: 'Purdex.app @ air26' }, decided_at: 5 } }))
    expect(useApprovalStore.getState().entries).toEqual({})
    expect(useUndoToast.getState().toast?.message).toBe('mlab：purdex-7c 的 lead 申請 已由 Purdex.app @ air26 核准')
    expect(useTabStore.getState().activeTabId).toBe('tO')
    expect(useTabStore.getState().tabs).toBe(tabsBefore)
    expect(usePaneFocusStore.getState().focusRequest).toBeNull()
  })
})

describe('approval-ws trust boundary (F1 / F2)', () => {
  let warn: ReturnType<typeof vi.spyOn>
  beforeEach(() => {
    warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
  })
  afterEach(() => warn.mockRestore())

  it.each([
    ['approvals: null', { op: 'snapshot', approvals: null }],
    ['approvals: "x"', { op: 'snapshot', approvals: 'x' }],
    ['approvals missing', { op: 'snapshot' }],
    ['one bad element', { op: 'snapshot', approvals: [approval(), { id: 'bad', state: 'open', origin: {}, created_at: 1 }] }],
  ])('a malformed snapshot (%s) is ignored as a whole: entries and the queue stay, no toast, no resend, one warning', async (_label, frame) => {
    const a = approval()
    useApprovalStore.getState().applyOpened(H, a)
    useApprovalStore.getState().queueDecision(H, a, 'deny')
    const before = useApprovalStore.getState()
    handleApprovalEvent(H, JSON.stringify(frame))
    await flush()
    expect(useApprovalStore.getState().entries).toBe(before.entries)
    expect(useApprovalStore.getState().queued).toBe(before.queued)
    expect(useApprovalStore.getState().queued[approvalKey(H, 'req-1')]).toMatchObject({ decision: 'deny' })
    expect(mockedDecide).not.toHaveBeenCalled()
    expect(useUndoToast.getState().toast).toBeNull()
    expect(warn).toHaveBeenCalledTimes(1)
  })

  it('a well-formed snapshot still parses to its approvals; an empty array is an authoritative empty set', () => {
    const a = approval()
    expect(parseApprovalEvent(snapshot([a]))).toEqual({ op: 'snapshot', approvals: [a] })
    expect(parseApprovalEvent(snapshot([]))).toEqual({ op: 'snapshot', approvals: [] })
    expect(warn).not.toHaveBeenCalled()
  })

  it('an `opened` whose approval only has id/state/origin/created_at is rejected: the store stays empty', () => {
    const value = JSON.stringify({ op: 'opened', approval: { id: 'x', state: 'open', origin: {}, created_at: 1 } })
    expect(parseApprovalEvent(value)).toBeNull()
    handleApprovalEvent(H, value)
    expect(useApprovalStore.getState().entries).toEqual({})
    expect(warn).toHaveBeenCalledTimes(2)
  })

  it.each<[string, (a: Approval) => unknown]>([
    ['id empty', (a) => ({ ...a, id: '' })],
    ['kind unknown', (a) => ({ ...a, kind: 'boss' })],
    ['state unknown', (a) => ({ ...a, state: 'pending' })],
    ['host_id not a string', (a) => ({ ...a, host_id: 7 })],
    ['origin missing', (a) => ({ ...a, origin: undefined })],
    ['origin.ref not a string', (a) => ({ ...a, origin: { ...a.origin, ref: undefined } })],
    ['origin.name not a string', (a) => ({ ...a, origin: { ...a.origin, name: null } })],
    ['origin.cwd not a string', (a) => ({ ...a, origin: { ...a.origin, cwd: 1 } })],
    ['origin.tmux not a string', (a) => ({ ...a, origin: { ...a.origin, tmux: undefined } })],
    ['origin.session_id not a string', (a) => ({ ...a, origin: { ...a.origin, session_id: 1 } })],
    ['origin.pid not a number', (a) => ({ ...a, origin: { ...a.origin, pid: '1' } })],
    ['origin.title not a string', (a) => ({ ...a, origin: { ...a.origin, title: 1 } })],
    ['origin.address not a string', (a) => ({ ...a, origin: { ...a.origin, address: {} } })],
    ['payload not an object', (a) => ({ ...a, payload: 'r' })],
    ['created_at missing', (a) => ({ ...a, created_at: undefined })],
    ['deadline_at not a number', (a) => ({ ...a, deadline_at: '541000' })],
    ['lease_until missing', (a) => ({ ...a, lease_until: undefined })],
    ['decided_by not an object', (a) => ({ ...a, decided_by: 'me' })],
    ['decided_at not a number', (a) => ({ ...a, decided_at: 'now' })],
    ['grant not an object', (a) => ({ ...a, grant: [] })],
  ])('rejects an approval with %s (opened and closed)', (_label, mutate) => {
    const bad = mutate(approval())
    expect(parseApprovalEvent(JSON.stringify({ op: 'opened', approval: bad }))).toBeNull()
    expect(parseApprovalEvent(JSON.stringify({ op: 'closed', approval: bad }))).toBeNull()
  })

  it('accepts the full wire shape, with and without the optional fields', () => {
    const a = approval()
    expect(parseApprovalEvent(JSON.stringify({ op: 'opened', approval: a }))).toEqual({ op: 'opened', approval: a })
    const closed = approval({ state: 'approved', decided_by: { kind: 'app', label: 'Purdex.app @ air26' }, decided_at: 5, grant: { max_members: 2, roots: ['/w'] } })
    expect(parseApprovalEvent(JSON.stringify({ op: 'closed', approval: closed }))).toEqual({ op: 'closed', approval: closed })
    const titled = approval({ origin: { ...a.origin, name: '', title: '修 #1450', address: 'mlab/_40iueq' } })
    expect(parseApprovalEvent(JSON.stringify({ op: 'opened', approval: titled }))).toEqual({ op: 'opened', approval: titled })
    expect(warn).not.toHaveBeenCalled()
  })
})

// U23 (unattended spec D-U23-6; plan PU-2b, Open question 1): what the daemon approved by itself while 無人值守模式 was
// on closes the dialog on every window, but no one pressed anything — no "handled by" toast.
describe('approval-ws: a close decided by unattended', () => {
  const closedBy = (decided_by: Approval['decided_by']) =>
    JSON.stringify({ op: 'closed', approval: { ...approval(), state: 'approved', decided_by, decided_at: 5 } })

  it('a closed decided by unattended closes the dialog and shows no toast', () => {
    useApprovalStore.getState().applyOpened(H, approval())
    handleApprovalEvent(H, closedBy({ kind: 'unattended', label: '無人值守模式' }))
    expect(useApprovalStore.getState().entries).toEqual({})
    expect(useUndoToast.getState().toast).toBeNull()
  })

  it('a closed decided by an app still toasts', () => {
    useApprovalStore.getState().applyOpened(H, approval())
    handleApprovalEvent(H, closedBy({ kind: 'app', label: 'Purdex.app @ air26' }))
    expect(useApprovalStore.getState().entries).toEqual({})
    expect(useUndoToast.getState().toast?.message).toBe('mlab：purdex-7c 的 lead 申請 已由 Purdex.app @ air26 核准')
  })
})
