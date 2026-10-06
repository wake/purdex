// spa/src/lib/team/approval-ws.test.ts — the reconnect snapshot and the decisions queued while the host was away
// (lead-team spec §6.3 "During a daemon restart", §9.4): the snapshot re-adds the request → the queued decision
// is sent, once; the snapshot shows it gone → toast `approval.toast.ended_while_away`; a resend that fails on the
// network while the host is still down stays queued for the next snapshot.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { useApprovalStore, approvalKey } from '../../stores/useApprovalStore'
import { useHostStore } from '../../stores/useHostStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { useUndoToast } from '../../stores/useUndoToast'
import { ApprovalApiError, decideApproval } from './approval-api'
import { __resetClientDescriptorForTests } from './client-label'
import { handleApprovalEvent } from './approval-ws'
import type { Approval } from './types'

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
