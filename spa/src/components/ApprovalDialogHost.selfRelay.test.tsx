// spa/src/components/ApprovalDialogHost.selfRelay.test.tsx — the `self_relay` body (lead-team spec §8.7 (b)): host,
// session (title / address / ref / cwd), `已用 72%`, the note, one-click 核准 / 拒絕 with no grant, and
// 「這個 session 不再詢問」, which POSTs the pause before the decision.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, act, waitFor } from '@testing-library/react'
import { ApprovalDialogHost } from './ApprovalDialogHost'
import { useApprovalStore } from '../stores/useApprovalStore'
import { useHostStore } from '../stores/useHostStore'
import { useI18nStore } from '../stores/useI18nStore'
import { useUndoToast } from '../stores/useUndoToast'
import { ApprovalApiError, decideApproval, setSelfRelayPause } from '../lib/team/approval-api'
import { __resetClientDescriptorForTests } from '../lib/team/client-label'
import type { Approval } from '../lib/team/types'

vi.mock('../lib/team/approval-api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../lib/team/approval-api')>()),
  decideApproval: vi.fn(),
  setSelfRelayPause: vi.fn(),
}))
const mockedDecide = vi.mocked(decideApproval)
const mockedPause = vi.mocked(setSelfRelayPause)

const H = 'h1'
const relay = (over: Partial<Approval> = {}): Approval => ({
  id: 'req-9', kind: 'self_relay', host_id: 'd1',
  origin: { session_id: 'S9', ref: '_b1xxxx', name: 'purdex-b0', pid: 4242, proc_start: 'p', cwd: '/w/purdex', tmux: 'purdex:@1.%2', title: 'purdex-tester', address: 'mlab/purdex-b0' },
  payload: { op_id: 'op-1', used_percentage: 72.4, window: 1_000_000, model_id: 'claude-opus-5-5', effort: 'high' },
  state: 'open', created_at: 1_000, deadline_at: 1_000 + 600_000, lease_until: 31_000,
  ...over,
})

const open = (a: Approval) => act(() => { useApprovalStore.getState().applyOpened(H, a) })

beforeEach(() => {
  useI18nStore.getState().setLocale('zh-TW')
  useApprovalStore.getState().reset()
  useHostStore.setState({
    hosts: { [H]: { id: H, name: 'mlab', ip: '1.2.3.4', port: 7860, order: 0 } },
    hostOrder: [H], runtime: { [H]: { status: 'connected' } }, activeHostId: H,
  })
  useUndoToast.setState({ toast: null, notice: null })
  mockedDecide.mockReset()
  mockedPause.mockReset().mockResolvedValue({ self_relay: 'paused', host_switch: true, member: false })
  __resetClientDescriptorForTests()
  Object.defineProperty(window, 'electronAPI', { value: undefined, writable: true, configurable: true })
})
afterEach(() => { useHostStore.getState().reset() })

describe('ApprovalDialogHost — self_relay', () => {
  it('shows the spec §8.7 body: host, session, address, ref, cwd, 已用 72% and the note; no grant fields', () => {
    render(<ApprovalDialogHost />)
    open(relay())
    expect(screen.getByTestId('approval-dialog').dataset.kind).toBe('self_relay')
    expect(screen.getByText('mlab：purdex-tester 申請接力')).toBeTruthy()
    expect(screen.getByTestId('approval-host').textContent).toBe('mlab')
    expect(screen.getByTestId('approval-session').textContent).toBe('purdex-tester')
    expect(screen.getByTestId('approval-address').textContent).toBe('mlab/purdex-b0')
    expect(screen.getByTestId('approval-ref').textContent).toBe('_b1xxxx')
    expect(screen.getByTestId('approval-cwd').textContent).toBe('/w/purdex')
    expect(screen.getByTestId('approval-usage').textContent).toBe('已用 72%')
    expect(screen.getByTestId('approval-self-relay-note').textContent).toBe('核准後這個 session 會寫接力檔、清空並在原處接手（約 1 分鐘）')
    expect(screen.getByText('這個 session 不再詢問')).toBeTruthy()
    expect(screen.queryByTestId('approval-max-members')).toBeNull()
    expect(screen.queryByTestId('approval-roots')).toBeNull()
    expect(screen.queryByTestId('approval-reason')).toBeNull()
  })

  it('核准 is one click with no grant (U13a); the dialog closes on the 200', async () => {
    mockedDecide.mockResolvedValueOnce(relay({ state: 'approved', decided_by: { kind: 'app', label: 'Purdex.app' } }))
    render(<ApprovalDialogHost />)
    open(relay())
    fireEvent.click(screen.getByTestId('approval-approve'))
    await waitFor(() => expect(mockedDecide).toHaveBeenCalledTimes(1))
    expect(mockedDecide.mock.calls[0][2]).toEqual({ decision: 'approve', client: { kind: 'app', label: 'Purdex.app' } })
    expect(mockedPause).not.toHaveBeenCalled()
    await waitFor(() => expect(screen.queryByTestId('approval-dialog')).toBeNull())
  })

  it('「這個 session 不再詢問」 POSTs the pause for the origin session before the decision', async () => {
    mockedDecide.mockResolvedValueOnce(relay({ state: 'denied', decided_by: { kind: 'app', label: 'Purdex.app' } }))
    render(<ApprovalDialogHost />)
    open(relay())
    fireEvent.click(screen.getByTestId('approval-no-more-asking'))
    fireEvent.click(screen.getByTestId('approval-deny'))
    await waitFor(() => expect(mockedDecide).toHaveBeenCalledTimes(1))
    expect(mockedPause).toHaveBeenCalledWith(H, 'S9', 'off')
    expect(mockedPause.mock.invocationCallOrder[0]).toBeLessThan(mockedDecide.mock.invocationCallOrder[0])
  })

  it('while disconnected the decision is queued WITH the pause, nothing is sent now (PR #1742 R1)', async () => {
    render(<ApprovalDialogHost />)
    open(relay())
    act(() => { useHostStore.getState().setRuntime(H, { status: 'reconnecting' }) })
    fireEvent.click(screen.getByTestId('approval-no-more-asking'))
    fireEvent.click(screen.getByTestId('approval-deny'))
    await waitFor(() => expect(Object.keys(useApprovalStore.getState().queued)).toHaveLength(1))
    expect(mockedPause).not.toHaveBeenCalled()
    expect(mockedDecide).not.toHaveBeenCalled()
    const q = Object.values(useApprovalStore.getState().queued)[0]
    expect(q.decision).toBe('deny')
    expect(q.grant).toBeUndefined()
    expect(q.pauseSession).toBe('S9')
  })

  it('a queued pause is sent before its queued decision on reconnect; a network failure keeps both queued', async () => {
    const { submitDecision } = await import('../lib/team/approval-decide')
    mockedPause.mockRejectedValueOnce(new ApprovalApiError(0, 'network', 'Failed to fetch'))
    mockedDecide.mockRejectedValueOnce(new ApprovalApiError(0, 'network', 'Failed to fetch'))
    await submitDecision(H, relay(), 'deny', undefined, { fromQueue: true, pauseSession: 'S9' })
    const q = Object.values(useApprovalStore.getState().queued)[0]
    expect(q?.pauseSession).toBe('S9') // kept with the re-queued decision
    mockedDecide.mockResolvedValueOnce(relay({ state: 'denied', decided_by: { kind: 'app', label: 'Purdex.app' } }))
    await submitDecision(H, relay(), 'deny', undefined, { fromQueue: true, pauseSession: 'S9' })
    expect(mockedPause).toHaveBeenLastCalledWith(H, 'S9', 'off')
    expect(mockedPause.mock.invocationCallOrder.at(-1)!).toBeLessThan(mockedDecide.mock.invocationCallOrder.at(-1)!)
  })

  it('a pause that fails toasts and the decision still goes out', async () => {
    mockedPause.mockRejectedValueOnce(new ApprovalApiError(0, 'network', 'Failed to fetch'))
    mockedDecide.mockResolvedValueOnce(relay({ state: 'denied', decided_by: { kind: 'app', label: 'Purdex.app' } }))
    render(<ApprovalDialogHost />)
    open(relay())
    fireEvent.click(screen.getByTestId('approval-no-more-asking'))
    fireEvent.click(screen.getByTestId('approval-deny'))
    await waitFor(() => expect(mockedDecide).toHaveBeenCalledTimes(1))
    expect(useUndoToast.getState().toast?.message).toContain('無法暫停')
  })

  it('a lead request still renders the lead body (the kind switch is per request)', () => {
    render(<ApprovalDialogHost />)
    open({ ...relay(), id: 'req-lead', kind: 'lead', payload: { reason: 'r', max_members: 3, roots: ['/w'] } })
    expect(screen.getByTestId('approval-dialog').dataset.kind).toBe('lead')
    expect(screen.getByTestId('approval-max-members')).toBeTruthy()
    expect(screen.queryByTestId('approval-usage')).toBeNull()
  })
})
