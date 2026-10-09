// The adopt card for a REMOTE target (cross-host spec §4.3, X3c-App): a host line, and an approve that is consent, not
// "done" — the card waits for the member host's answer (long poll on the lead host) and shows the outcome. A local target
// closes at the approve as before. Real host + stores; only decideApproval, fetchAdoption and the navigation are mocked.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, act } from '@testing-library/react'
import { ApprovalDialogHost } from './ApprovalDialogHost'
import { useApprovalStore } from '../stores/useApprovalStore'
import { useHostStore } from '../stores/useHostStore'
import { useI18nStore } from '../stores/useI18nStore'
import { useUndoToast } from '../stores/useUndoToast'
import { decideApproval, fetchAdoption } from '../lib/team/approval-api'
import { resetAdoptionWaitForTests } from '../lib/team/adoption-wait'
import { __resetClientDescriptorForTests } from '../lib/team/client-label'
import type { Approval } from '../lib/team/types'

vi.mock('../lib/team/approval-api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../lib/team/approval-api')>()),
  decideApproval: vi.fn(),
  fetchAdoption: vi.fn(),
}))
vi.mock('../lib/team/approval-goto', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../lib/team/approval-goto')>()),
  gotoRequester: vi.fn(() => 'none'),
}))
const mockedDecide = vi.mocked(decideApproval)
const mockedPoll = vi.mocked(fetchAdoption)

const H = 'h1'
const REMOTE = { target_host_id: 'hid-b', target_host_alias: 'air26' }
const adopt = (payload: Record<string, unknown> = {}): Approval => ({
  id: 'req-1', kind: 'adopt', host_id: 'd1',
  origin: { session_id: 'S-LEAD', ref: '_40iueq', name: 'purdex-7c', pid: 4242, proc_start: 'p', cwd: '/w/purdex', tmux: 'purdex:@1.%2' },
  payload: {
    team_id: '8f2c0f8e-3b1a', lead_session_id: 'S-LEAD', target_ref: '_tgt001', target_session_id: 'S-TGT', title: '寫文件的那個',
    target_name: 'doc-writer', target_address: 'doc-writer', target_cwd: '/w/docs', target_tmux: '', ...payload,
  },
  state: 'open', created_at: 1_000, deadline_at: 1_000 + 125_000, lease_until: 31_000,
})
const open = (a: Approval) => act(() => { useApprovalStore.getState().applyOpened(H, a) })
const click = async (id: string) => { await act(async () => { fireEvent.click(screen.getByTestId(id)) }) }
const text = (id: string) => screen.getByTestId(id).textContent
let resolvePoll: (v: { approval_id: string; state: string; code?: string }) => void = () => {}
const answer = async (state: string, code?: string) => { await act(async () => { resolvePoll({ approval_id: 'req-1', state, code }) }) }

beforeEach(() => {
  useI18nStore.getState().setLocale('zh-TW')
  useApprovalStore.getState().reset()
  resetAdoptionWaitForTests()
  useHostStore.setState({
    hosts: { [H]: { id: H, name: 'mlab', ip: '1.2.3.4', port: 7860, order: 0 } },
    hostOrder: [H], runtime: { [H]: { status: 'connected' } }, activeHostId: H,
  })
  useUndoToast.setState({ toast: null, notice: null })
  mockedDecide.mockReset()
  mockedPoll.mockReset()
  mockedPoll.mockImplementation(() => new Promise((r) => { resolvePoll = r }))
  __resetClientDescriptorForTests()
  Object.defineProperty(window, 'electronAPI', { value: undefined, writable: true, configurable: true })
})
afterEach(() => { resetAdoptionWaitForTests(); useHostStore.getState().reset() })

describe('adopt card — remote target', () => {
  it('shows the host line "在 <alias> 上"; a local card has none', () => {
    render(<ApprovalDialogHost />)
    open(adopt(REMOTE))
    expect(text('approval-adopt-host')).toBe('在 air26 上')
    act(() => { useApprovalStore.getState().reset() })
    open(adopt())
    expect(screen.queryByTestId('approval-adopt-host')).toBeNull()
  })

  it('clips a long alias like the other payload strings', () => {
    render(<ApprovalDialogHost />)
    open(adopt({ target_host_id: 'hid-b', target_host_alias: 'x'.repeat(300) }))
    expect(text('approval-adopt-host')).toBe(`在 ${'x'.repeat(60)}… 上`)
  })

  it('approve is consent: the card waits for the member host instead of finishing', async () => {
    mockedDecide.mockResolvedValue({ ...adopt(REMOTE), state: 'approved' })
    render(<ApprovalDialogHost />)
    open(adopt(REMOTE))
    await click('approval-approve')
    expect(screen.queryByTestId('approval-dialog')).toBeNull()
    expect(text('adoption-wait-text')).toBe('等待 air26 回覆…')
    expect(mockedPoll).toHaveBeenCalledWith(H, 'req-1', 30)
  })

  it('active → 已納入, then the card closes by itself', async () => {
    vi.useFakeTimers()
    try {
      mockedDecide.mockResolvedValue({ ...adopt(REMOTE), state: 'approved' })
      render(<ApprovalDialogHost />)
      open(adopt(REMOTE))
      await click('approval-approve')
      await answer('active')
      expect(text('adoption-wait-text')).toBe('已納入')
      await act(async () => { await vi.advanceTimersByTimeAsync(2_000) })
      expect(screen.queryByTestId('adoption-wait-card')).toBeNull()
    } finally { vi.useRealTimers() }
  })

  it('failed → 納入失敗 with the code', async () => {
    mockedDecide.mockResolvedValue({ ...adopt(REMOTE), state: 'approved' })
    render(<ApprovalDialogHost />)
    open(adopt(REMOTE))
    await click('approval-approve')
    await answer('failed', 'dir_missing')
    expect(text('adoption-wait-text')).toBe('納入失敗（dir_missing）')
  })

  it('void → says the member host did not answer for ten minutes', async () => {
    mockedDecide.mockResolvedValue({ ...adopt(REMOTE), state: 'approved' })
    render(<ApprovalDialogHost />)
    open(adopt(REMOTE))
    await click('approval-approve')
    await answer('void')
    expect(text('adoption-wait-text')).toBe('air26 十分鐘沒有回應，這次納入已作廢')
  })

  // Mutation gate: keep the wait in component state (lost on unmount) → no toast → red.
  it('closed early, even with the host unmounted, the outcome still arrives as a toast', async () => {
    mockedDecide.mockResolvedValue({ ...adopt(REMOTE), state: 'approved' })
    const view = render(<ApprovalDialogHost />)
    open(adopt(REMOTE))
    await click('approval-approve')
    await click('adoption-wait-close')
    expect(screen.queryByTestId('adoption-wait-card')).toBeNull()
    view.unmount()
    await answer('failed', 'dir_missing')
    expect(useUndoToast.getState().toast?.message).toBe('寫文件的那個：納入失敗（dir_missing）')
  })

  it('deny is unchanged: no wait, no poll', async () => {
    mockedDecide.mockResolvedValue({ ...adopt(REMOTE), state: 'denied' })
    render(<ApprovalDialogHost />)
    open(adopt(REMOTE))
    await click('approval-deny')
    expect(mockedPoll).not.toHaveBeenCalled()
    expect(screen.queryByTestId('adoption-wait-card')).toBeNull()
  })
})

describe('adopt card — local target (regression)', () => {
  it('approve finishes at once: no wait card, no poll', async () => {
    mockedDecide.mockResolvedValue({ ...adopt(), state: 'approved' })
    render(<ApprovalDialogHost />)
    open(adopt())
    await click('approval-approve')
    expect(screen.queryByTestId('approval-dialog')).toBeNull()
    expect(screen.queryByTestId('adoption-wait-card')).toBeNull()
    expect(mockedPoll).not.toHaveBeenCalled()
  })
})
