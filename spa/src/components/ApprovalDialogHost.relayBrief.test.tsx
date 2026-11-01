// spa/src/components/ApprovalDialogHost.relayBrief.test.tsx — UP-5a: the relay cards name the session by title beside its id,
// and mark a re-asked request.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, act } from '@testing-library/react'
import { ApprovalDialogHost } from './ApprovalDialogHost'
import { useApprovalStore } from '../stores/useApprovalStore'
import { useHostStore } from '../stores/useHostStore'
import { useI18nStore } from '../stores/useI18nStore'
import { useUndoToast } from '../stores/useUndoToast'
import { decideApproval } from '../lib/team/approval-api'
import { __resetClientDescriptorForTests } from '../lib/team/client-label'
import type { Approval } from '../lib/team/types'

vi.mock('../lib/team/approval-api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../lib/team/approval-api')>()),
  decideApproval: vi.fn(),
}))

const H = 'h1'
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

beforeEach(() => {
  useI18nStore.getState().setLocale('zh-TW')
  useApprovalStore.getState().reset()
  useHostStore.setState({
    hosts: { [H]: { id: H, name: 'mlab', ip: '1.2.3.4', port: 7860, order: 0 } },
    hostOrder: [H], runtime: { [H]: { status: 'connected' } }, activeHostId: H,
  })
  useUndoToast.setState({ toast: null, notice: null })
  vi.mocked(decideApproval).mockReset()
  __resetClientDescriptorForTests()
  Object.defineProperty(window, 'electronAPI', { value: undefined, writable: true, configurable: true })
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
