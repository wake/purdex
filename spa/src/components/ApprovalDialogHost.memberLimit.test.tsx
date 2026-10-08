// spa/src/components/ApprovalDialogHost.memberLimit.test.tsx — U25 / D-U24-7 (dialog half): the lead dialog's member
// limit starts at 3 whatever the lead asked for; the request, when it is not 3, is named beside the field; the person
// may still type 1–8. The real dialog host and store; only the daemon call is mocked.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, act } from '@testing-library/react'
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
const mockedDecide = vi.mocked(decideApproval)

const H = 'h1'
const lead = (payload: Record<string, unknown>): Approval => ({
  id: 'req-1', kind: 'lead', host_id: 'd1',
  origin: { session_id: 'S1', ref: '_40iueq', name: 'purdex-7c', pid: 4242, proc_start: 'p', cwd: '/w/purdex', tmux: 'purdex:@1.%2' },
  payload: { reason: 'r', roots: ['/w/purdex'], ...payload },
  state: 'open', created_at: 1_000, deadline_at: 1_000 + 125_000, lease_until: 31_000,
})
const open = (a: Approval) => act(() => { useApprovalStore.getState().applyOpened(H, a) })
const field = () => screen.getByTestId('approval-max-members') as HTMLInputElement
const note = () => screen.queryByTestId('approval-max-members-requested')

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
afterEach(() => { useHostStore.getState().reset() })

describe('ApprovalDialogHost member limit (U25)', () => {
  it('a request of 5 prefills 3 and shows 「lead 申請 5 個」', () => {
    render(<ApprovalDialogHost />)
    open(lead({ max_members: 5 }))
    expect(field().value).toBe('3')
    expect(note()?.textContent).toBe('lead 申請 5 個')
  })

  it('a request of 1 prefills 3 and shows 「lead 申請 1 個」', () => {
    render(<ApprovalDialogHost />)
    open(lead({ max_members: 1 }))
    expect(field().value).toBe('3')
    expect(note()?.textContent).toBe('lead 申請 1 個')
  })

  it('a request of 3 prefills 3 with no note', () => {
    render(<ApprovalDialogHost />)
    open(lead({ max_members: 3 }))
    expect(field().value).toBe('3')
    expect(note()).toBeNull()
  })

  it('an unspecified request prefills 3 with no note', () => {
    render(<ApprovalDialogHost />)
    open(lead({}))
    expect(field().value).toBe('3')
    expect(note()).toBeNull()
  })

  it('the note reads in English too', () => {
    useI18nStore.getState().setLocale('en')
    render(<ApprovalDialogHost />)
    open(lead({ max_members: 5 }))
    expect(note()?.textContent).toBe('lead asked for 5')
  })

  it('approving untouched sends max_members 3 even when the lead asked for 5', async () => {
    mockedDecide.mockResolvedValueOnce({ ...lead({ max_members: 5 }), state: 'approved' })
    render(<ApprovalDialogHost />)
    open(lead({ max_members: 5 }))
    await act(async () => { fireEvent.click(screen.getByTestId('approval-approve')) })
    expect(mockedDecide.mock.calls[0][2]).toMatchObject({ decision: 'approve', grant: { max_members: 3, roots: ['/w/purdex'] } })
  })

  it('typing 5 sends 5', async () => {
    mockedDecide.mockResolvedValueOnce({ ...lead({ max_members: 5 }), state: 'approved' })
    render(<ApprovalDialogHost />)
    open(lead({ max_members: 5 }))
    fireEvent.change(field(), { target: { value: '5' } })
    await act(async () => { fireEvent.click(screen.getByTestId('approval-approve')) })
    expect(mockedDecide.mock.calls[0][2]).toMatchObject({ decision: 'approve', grant: { max_members: 5 } })
  })

  it('0 and 9 still show the range error and block 核准', () => {
    render(<ApprovalDialogHost />)
    open(lead({ max_members: 5 }))
    for (const bad of ['0', '9']) {
      fireEvent.change(field(), { target: { value: bad } })
      expect(screen.getByTestId('approval-max-members-error')).toBeInTheDocument()
      expect(screen.getByTestId('approval-approve')).toBeDisabled()
    }
    fireEvent.change(field(), { target: { value: '8' } })
    expect(screen.queryByTestId('approval-max-members-error')).toBeNull()
  })
})
