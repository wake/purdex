// spa/src/components/ApprovalDialogHost.approveAndView.test.tsx — 核准 only approves; 「核准並檢視」 also goes to the
// requester's tab; the focus lands on 核准 when the dialog shows; a keyboard-triggered click in the first 500 ms is
// ignored (the Enter that was meant for the terminal). Only the daemon call and the navigation are mocked.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, act } from '@testing-library/react'
import { ApprovalDialogHost } from './ApprovalDialogHost'
import { useApprovalStore } from '../stores/useApprovalStore'
import { useHostStore } from '../stores/useHostStore'
import { useI18nStore } from '../stores/useI18nStore'
import { useUndoToast } from '../stores/useUndoToast'
import { decideApproval } from '../lib/team/approval-api'
import { keyboardGuard } from './approval-dialog-helpers'
import { gotoRequester } from '../lib/team/approval-goto'
import { __resetClientDescriptorForTests } from '../lib/team/client-label'
import type { Approval, ApprovalKind } from '../lib/team/types'

vi.mock('../lib/team/approval-api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../lib/team/approval-api')>()),
  decideApproval: vi.fn(),
}))
vi.mock('../lib/team/approval-goto', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../lib/team/approval-goto')>()),
  gotoRequester: vi.fn(() => 'none'),
}))
const mockedDecide = vi.mocked(decideApproval)
const mockedGoto = vi.mocked(gotoRequester)

const H = 'h1'
const PAYLOADS: Record<string, unknown> = {
  lead: { reason: 'r', max_members: 3, roots: ['/w/purdex'] },
  self_relay: { op_id: 'op-1', used_percentage: 71, window: 200_000 },
  member_relay: { member_ref: '_mem001', member_title: 'm', member_name: 'm-1', used_percentage: 90, window: 200_000 },
  adopt: { target_host_id: '', target_ref: '_t00001', target_session_id: 'TS', target_address: 'mlab/t', target_cwd: '/w', target_tmux: 't:@1.%1', team_id: 'team-1' },
}
const approval = (kind: ApprovalKind = 'lead', over: Partial<Approval> = {}): Approval => ({
  id: 'req-1', kind, host_id: 'd1',
  origin: { session_id: 'S1', ref: '_40iueq', name: 'purdex-7c', pid: 4242, proc_start: 'p', cwd: '/w/purdex', tmux: 'purdex:@1.%2' },
  payload: PAYLOADS[kind],
  state: 'open', created_at: 1_000, deadline_at: 541_000, lease_until: 31_000,
  ...over,
})
const noTmux = (kind: ApprovalKind = 'lead') => approval(kind, { origin: { ...approval().origin, tmux: '' } })

const open = (a: Approval) => act(() => { useApprovalStore.getState().applyOpened(H, a) })
const approveBtn = () => screen.getByTestId('approval-approve')
const viewBtn = () => screen.queryByTestId('approval-approve-view')
/** A keyboard-triggered click (Enter / Space on a focused button): `detail` is 0. */
const keyClick = (el: HTMLElement) => fireEvent.click(el, { detail: 0 })
/** A mouse click: `detail` is 1. */
const mouseClick = (el: HTMLElement) => fireEvent.click(el, { detail: 1 })
const settle = () => act(async () => { await Promise.resolve(); await Promise.resolve() })

beforeEach(() => {
  keyboardGuard.ms = 500
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
  mockedGoto.mockReset()
  mockedGoto.mockReturnValue('none')
  __resetClientDescriptorForTests()
  Object.defineProperty(window, 'electronAPI', { value: undefined, writable: true, configurable: true })
})
afterEach(() => {
  vi.useRealTimers()
  useHostStore.getState().reset()
})

describe('核准 / 核准並檢視', () => {
  it.each<ApprovalKind>(['lead', 'self_relay', 'member_relay', 'adopt'])('%s: 核准 approves and does not switch tabs', async (kind) => {
    mockedDecide.mockResolvedValueOnce(approval(kind, { state: 'approved' }))
    render(<ApprovalDialogHost />)
    open(approval(kind))
    mouseClick(approveBtn())
    await settle()
    expect(mockedDecide).toHaveBeenCalledTimes(1)
    expect(mockedDecide.mock.calls[0][2]).toMatchObject({ decision: 'approve' })
    expect(mockedGoto).not.toHaveBeenCalled()
  })

  it.each<ApprovalKind>(['lead', 'self_relay', 'member_relay', 'adopt'])('%s: 核准並檢視 approves and switches to the requester', async (kind) => {
    const closed = approval(kind, { state: 'approved' })
    mockedDecide.mockResolvedValueOnce(closed)
    render(<ApprovalDialogHost />)
    open(approval(kind))
    expect(viewBtn()).not.toBeNull()
    expect(viewBtn()!.textContent).toBe('核准並檢視')
    mouseClick(viewBtn()!)
    await settle()
    expect(mockedDecide.mock.calls[0][2]).toMatchObject({ decision: 'approve' })
    expect(mockedGoto).toHaveBeenCalledTimes(1)
    expect(mockedGoto).toHaveBeenCalledWith(H, closed)
  })

  it('拒絕 does not switch tabs', async () => {
    mockedDecide.mockResolvedValueOnce(approval('lead', { state: 'denied' }))
    render(<ApprovalDialogHost />)
    open(approval())
    mouseClick(screen.getByTestId('approval-deny'))
    await settle()
    expect(mockedDecide.mock.calls[0][2]).toMatchObject({ decision: 'deny' })
    expect(mockedGoto).not.toHaveBeenCalled()
  })

  it.each<ApprovalKind>(['lead', 'self_relay', 'member_relay', 'adopt'])('%s: no tmux origin → no 核准並檢視 button', (kind) => {
    render(<ApprovalDialogHost />)
    open(noTmux(kind))
    expect(viewBtn()).toBeNull()
    expect(approveBtn()).toBeInTheDocument()
  })

  it('sits between 拒絕 and 核准, which stays last', () => {
    render(<ApprovalDialogHost />)
    open(approval())
    const ids = Array.from(screen.getByTestId('approval-panel').querySelectorAll('button')).map((b) => b.getAttribute('data-testid'))
    expect(ids.slice(-3)).toEqual(['approval-deny', 'approval-approve-view', 'approval-approve'])
  })

  it('is disabled with 核准 when the lead form does not validate', () => {
    render(<ApprovalDialogHost />)
    open(approval())
    fireEvent.change(screen.getByTestId('approval-roots'), { target: { value: '' } })
    expect(approveBtn()).toBeDisabled()
    expect(viewBtn()).toBeDisabled()
  })

  it('a 核准並檢視 clicked while the daemon is down is queued and switches nothing, then or on the resend', async () => {
    render(<ApprovalDialogHost />)
    open(approval())
    act(() => { useHostStore.getState().setRuntime(H, { status: 'reconnecting' }) })
    mouseClick(viewBtn()!)
    await settle()
    expect(mockedDecide).not.toHaveBeenCalled()
    const q = Object.values(useApprovalStore.getState().queued)
    expect(q).toHaveLength(1)
    expect(q[0].decision).toBe('approve')
    expect(mockedGoto).not.toHaveBeenCalled()
  })
})

describe('focus on 核准', () => {
  it('opening puts the focus on 核准', () => {
    render(<ApprovalDialogHost />)
    open(approval())
    expect(document.activeElement).toBe(approveBtn())
  })

  it('restoring from the pill puts it back on 核准', () => {
    render(<ApprovalDialogHost />)
    open(approval())
    act(() => { useApprovalStore.getState().setMinimized(true) })
    expect(document.activeElement).not.toBe(approveBtn())
    act(() => { useApprovalStore.getState().setMinimized(false) })
    expect(document.activeElement).toBe(approveBtn())
  })

  it('the decision is queued after 核准 had the focus: the focus stays in the dialog, not on the body', () => {
    render(<ApprovalDialogHost />)
    const a = approval()
    open(a)
    expect(document.activeElement).toBe(approveBtn())
    act(() => { useApprovalStore.getState().queueDecision(H, a, 'approve') })
    expect(approveBtn()).toBeDisabled()
    expect(document.activeElement).toBe(screen.getByTestId('approval-panel'))
  })

  it('核准 disabled (here: a decision already queued): the focus falls back to the panel', () => {
    render(<ApprovalDialogHost />)
    const a = approval()
    open(a)
    act(() => { useApprovalStore.getState().queueDecision(H, a, 'approve') })
    act(() => { useApprovalStore.getState().setMinimized(true) })
    expect(approveBtn()).toBeDisabled()
    act(() => { useApprovalStore.getState().setMinimized(false) })
    expect(document.activeElement).toBe(screen.getByTestId('approval-panel'))
  })
})

describe('the first 500 ms: a keyboard click is ignored', () => {
  it('Enter on the focused 核准 right after the dialog shows sends nothing; a mouse click does', async () => {
    vi.useFakeTimers()
    mockedDecide.mockResolvedValue(approval('lead', { state: 'approved' }))
    render(<ApprovalDialogHost />)
    open(approval())
    act(() => { vi.advanceTimersByTime(499) })
    keyClick(approveBtn())
    keyClick(viewBtn()!)
    keyClick(screen.getByTestId('approval-deny'))
    await settle()
    expect(mockedDecide).not.toHaveBeenCalled()
    mouseClick(approveBtn())
    await settle()
    expect(mockedDecide).toHaveBeenCalledTimes(1)
  })

  it('at 500 ms the keyboard works', async () => {
    vi.useFakeTimers()
    mockedDecide.mockResolvedValue(approval('lead', { state: 'approved' }))
    render(<ApprovalDialogHost />)
    open(approval())
    act(() => { vi.advanceTimersByTime(500) })
    keyClick(approveBtn())
    await settle()
    expect(mockedDecide).toHaveBeenCalledTimes(1)
  })

  it('restoring from the pill starts the 500 ms again', async () => {
    vi.useFakeTimers()
    mockedDecide.mockResolvedValue(approval('lead', { state: 'approved' }))
    render(<ApprovalDialogHost />)
    open(approval())
    act(() => { vi.advanceTimersByTime(2000) })
    act(() => { useApprovalStore.getState().setMinimized(true) })
    act(() => { useApprovalStore.getState().setMinimized(false) })
    act(() => { vi.advanceTimersByTime(100) })
    keyClick(approveBtn())
    await settle()
    expect(mockedDecide).not.toHaveBeenCalled()
    act(() => { vi.advanceTimersByTime(400) })
    keyClick(approveBtn())
    await settle()
    expect(mockedDecide).toHaveBeenCalledTimes(1)
  })
})
