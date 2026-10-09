// spa/src/components/ApprovalDialogHost.memberRelay.test.tsx — the `member_relay` card (RQ-2 spec §8): a lead's member relay
// waits for a person because the lead's member pool is out. 「<lead> 要幫 member <member> 接力（context NN%）；member 額度
// 用完，要核准嗎？」, one-click 核准 / 拒絕 through the existing decide route, no grant, nothing to remember.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, act, waitFor } from '@testing-library/react'
import { ApprovalDialogHost } from './ApprovalDialogHost'
import { useApprovalStore } from '../stores/useApprovalStore'
import { useHostStore } from '../stores/useHostStore'
import { useI18nStore } from '../stores/useI18nStore'
import { useUndoToast } from '../stores/useUndoToast'
import { decideApproval, setSelfRelayPause } from '../lib/team/approval-api'
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
const memberRelay = (over: Partial<Approval> = {}): Approval => ({
  id: 'req-m1', kind: 'member_relay', host_id: 'd1',
  origin: { session_id: 'S1', ref: '_aaaaaa', name: 'purdex-88-b8', pid: 4242, proc_start: 'p', cwd: '/w/purdex', tmux: 'purdex:@1.%2', title: 'purdex-iface-lead', address: 'mlab/purdex-88-b8' },
  payload: {
    op_id: 'op-1', team_id: 't-1', lead_ref: '_aaaaaa', lead_title: 'purdex-iface-lead',
    member_session_id: 'M1', member_ref: '_bbbbbb', member_title: 'purdex-iface-solo', used_percentage: 72.4,
  },
  state: 'open', created_at: 1_000, deadline_at: 1_000 + 600_000, lease_until: 1_000 + 600_000,
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
  mockedPause.mockReset()
  __resetClientDescriptorForTests()
  Object.defineProperty(window, 'electronAPI', { value: undefined, writable: true, configurable: true })
})
afterEach(() => { useHostStore.getState().reset() })

describe('ApprovalDialogHost — member_relay', () => {
  it('titles the card with the lead, the member and the context usage, and says why it waits', () => {
    render(<ApprovalDialogHost />)
    open(memberRelay())
    expect(screen.getByTestId('approval-dialog').dataset.kind).toBe('member_relay')
    expect(screen.getByText('purdex-iface-lead 要幫 member purdex-iface-solo 接力（context 72%）；member 額度用完，要核准嗎？')).toBeTruthy()
  })

  it('shows host, the lead\'s session, address and cwd, the member and its ref, the usage and the note; no grant, no reason, no 「不再詢問」', () => {
    render(<ApprovalDialogHost />)
    open(memberRelay())
    expect(screen.getByTestId('approval-host').textContent).toBe('mlab')
    expect(screen.getByTestId('approval-session').textContent).toBe('purdex-iface-lead')
    expect(screen.getByTestId('approval-address').textContent).toBe('mlab/purdex-88-b8')
    expect(screen.getByTestId('approval-cwd').textContent).toBe('/w/purdex')
    expect(screen.getByTestId('approval-member').textContent).toBe('purdex-iface-solo')
    expect(screen.getByTestId('approval-member-ref').textContent).toBe('_bbbbbb')
    expect(screen.getByTestId('approval-usage').textContent).toBe('已用 72%')
    expect(screen.getByTestId('approval-member-relay-note').textContent).toContain('核准後這個 member 會寫接力檔')
    expect(screen.queryByTestId('approval-max-members')).toBeNull()
    expect(screen.queryByTestId('approval-roots')).toBeNull()
    expect(screen.queryByTestId('approval-reason')).toBeNull()
    expect(screen.queryByTestId('approval-no-more-asking')).toBeNull()
    expect(screen.queryByTestId('approval-team-name')).toBeNull()
    expect(screen.queryByTestId('approval-ref')).toBeNull() // the lead's own ref row is a self_relay thing
  })

  it('falls back to the origin\'s label and the member ref when the payload has no titles', () => {
    render(<ApprovalDialogHost />)
    open(memberRelay({ payload: { op_id: 'op-1', team_id: 't-1', lead_ref: '_aaaaaa', lead_title: '', member_session_id: 'M1', member_ref: '_bbbbbb', member_title: '  ', used_percentage: 10 } }))
    expect(screen.getByText('purdex-iface-lead 要幫 member _bbbbbb 接力（context 10%）；member 額度用完，要核准嗎？')).toBeTruthy()
  })

  it('aliases made only of direction marks do not blank the heading', () => {
    render(<ApprovalDialogHost />)
    open(memberRelay({ payload: { op_id: 'o', team_id: 't', lead_ref: '_a', lead_title: '\u202e', member_session_id: 'M', member_ref: '_bbbbbb', member_title: '\u200b', used_percentage: 30 } }))
    expect(screen.getByText('purdex-iface-lead 要幫 member _bbbbbb 接力（context 30%）；member 額度用完，要核准嗎？')).toBeTruthy()
    expect(screen.getByTestId('approval-member').textContent).toBe('_bbbbbb')
  })

  it('a payload that is not the wire shape does not blank the screen', () => {
    render(<ApprovalDialogHost />)
    open(memberRelay({ payload: { used_percentage: 'lots', member_title: 7 } as never }))
    expect(screen.getByTestId('approval-dialog').dataset.kind).toBe('member_relay')
    expect(screen.getByTestId('approval-usage').textContent).toBe('已用 0%')
  })

  it('核准 is one click with no grant, through the existing decide route; the dialog closes on the 200', async () => {
    mockedDecide.mockResolvedValueOnce(memberRelay({ state: 'approved', decided_by: { kind: 'app', label: 'Purdex.app' } }))
    render(<ApprovalDialogHost />)
    open(memberRelay())
    fireEvent.click(screen.getByTestId('approval-approve'))
    await waitFor(() => expect(mockedDecide).toHaveBeenCalledTimes(1))
    expect(mockedDecide.mock.calls[0][1]).toBe('req-m1')
    expect(mockedDecide.mock.calls[0][2]).toEqual({ decision: 'approve', client: { kind: 'app', label: 'Purdex.app' } })
    expect(mockedPause).not.toHaveBeenCalled()
    await waitFor(() => expect(screen.queryByTestId('approval-dialog')).toBeNull())
  })

  it('拒絕 is one click too', async () => {
    mockedDecide.mockResolvedValueOnce(memberRelay({ state: 'denied', decided_by: { kind: 'app', label: 'Purdex.app' } }))
    render(<ApprovalDialogHost />)
    open(memberRelay())
    fireEvent.click(screen.getByTestId('approval-deny'))
    await waitFor(() => expect(mockedDecide).toHaveBeenCalledTimes(1))
    expect(mockedDecide.mock.calls[0][2]).toEqual({ decision: 'deny', client: { kind: 'app', label: 'Purdex.app' } })
  })

  it('while disconnected the decision is queued with no grant and no pause (nothing to pause: this is the lead\'s member, not the session)', async () => {
    render(<ApprovalDialogHost />)
    open(memberRelay())
    act(() => { useHostStore.getState().setRuntime(H, { status: 'reconnecting' }) })
    fireEvent.click(screen.getByTestId('approval-approve'))
    await waitFor(() => expect(Object.keys(useApprovalStore.getState().queued)).toHaveLength(1))
    const q = Object.values(useApprovalStore.getState().queued)[0]
    expect(q.decision).toBe('approve')
    expect(q.grant).toBeUndefined()
    expect(q.pauseSession).toBeUndefined()
    expect(mockedDecide).not.toHaveBeenCalled()
  })

  it('the English card says the same', () => {
    useI18nStore.getState().setLocale('en')
    render(<ApprovalDialogHost />)
    open(memberRelay())
    expect(screen.getByText('purdex-iface-lead wants to relay member purdex-iface-solo (context 72%); the member quota is used up. Approve?')).toBeTruthy()
  })

  it('an over-long title is clipped, not allowed to push the card out', () => {
    render(<ApprovalDialogHost />)
    open(memberRelay({ payload: { op_id: 'o', team_id: 't', lead_ref: '_a', lead_title: 'L'.repeat(500), member_session_id: 'M', member_ref: '_b', member_title: 'M'.repeat(500), used_percentage: 5 } }))
    const title = screen.getByRole('heading').textContent ?? ''
    expect(title.length).toBeLessThan(260)
    expect(title).toContain('…')
  })
})
