// spa/src/lib/team/approval-decide.goto.test.ts — only this window's own successful decide goes back to the requester
// (lead-team spec §2 "How this spec reads U22" (a) (c), §15; plan v3 P9b-1 behaviour rule 1): `submitDecision`'s 200
// calls `gotoRequester` once, with the closed approval, for both dialog kinds and both answers; a 409, 404,
// host_removed or a network failure (queued or not) never does; and a navigation that throws never turns a decision
// that was sent into a failure. The daemon call and the navigation are mocked; the store is real.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { useApprovalStore, type Decision } from '../../stores/useApprovalStore'
import { useHostStore } from '../../stores/useHostStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { useUndoToast } from '../../stores/useUndoToast'
import { ApprovalApiError, decideApproval } from './approval-api'
import { gotoRequester } from './approval-goto'
import { submitDecision } from './approval-decide'
import { __resetClientDescriptorForTests } from './client-label'
import type { Approval, ApprovalKind } from './types'

vi.mock('./approval-api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('./approval-api')>()),
  decideApproval: vi.fn(),
}))
vi.mock('./approval-goto', async (importOriginal) => ({
  ...(await importOriginal<typeof import('./approval-goto')>()),
  gotoRequester: vi.fn(() => 'none'),
}))
const mockedDecide = vi.mocked(decideApproval)
const mockedGoto = vi.mocked(gotoRequester)

const H = 'h1'
/** The two kinds the Mac App draws a dialog for (spec U22 (c)); hook kinds never reach the store. */
type DialogKind = Extract<ApprovalKind, 'lead' | 'self_relay'>
const PAYLOADS: Record<DialogKind, unknown> = {
  lead: { reason: 'r', max_members: 3, roots: ['/w/purdex'] },
  self_relay: { op_id: 'op-1', used_percentage: 71, window: 200_000 },
}
const approval = (kind: DialogKind = 'lead', over: Partial<Approval> = {}): Approval => ({
  id: 'req-1', kind, host_id: 'd1',
  origin: { session_id: 'S1', ref: '_40iueq', name: 'purdex-7c', pid: 1, proc_start: 'p', cwd: '/w/purdex', tmux: 'purdex:@1.%2' },
  payload: PAYLOADS[kind],
  state: 'open', created_at: 1_000, deadline_at: 541_000, lease_until: 31_000,
  ...over,
})
const air26 = { kind: 'app' as const, label: 'Purdex.app @ air26' }

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
  mockedGoto.mockReset()
  mockedGoto.mockReturnValue('none')
  __resetClientDescriptorForTests()
  Object.defineProperty(window, 'electronAPI', { value: undefined, writable: true, configurable: true })
})
afterEach(() => useHostStore.getState().reset())

describe('submitDecision → gotoRequester (U22 (a))', () => {
  it.each<[DialogKind, Decision]>([
    ['lead', 'approve'],
    ['lead', 'deny'],
    ['self_relay', 'approve'],
    ['self_relay', 'deny'],
  ])('approve 200 and deny 200 each switch once, with the closed approval (%s, %s)', async (kind, decision) => {
    const a = approval(kind)
    useApprovalStore.getState().applyOpened(H, a)
    const closed = approval(kind, { state: decision === 'approve' ? 'approved' : 'denied', decided_by: { kind: 'app', label: 'Purdex.app' }, decided_at: 5 })
    mockedDecide.mockResolvedValueOnce(closed)
    // The switch runs after the request left the store, so the next dialog is already up in front of it (rule 3).
    let entriesAtSwitch: unknown = 'not called'
    mockedGoto.mockImplementationOnce(() => {
      entriesAtSwitch = useApprovalStore.getState().entries
      return 'activated'
    })

    await expect(submitDecision(H, a, decision)).resolves.toBe('closed')

    expect(mockedGoto).toHaveBeenCalledTimes(1)
    expect(mockedGoto).toHaveBeenCalledWith(H, closed)
    expect(entriesAtSwitch).toEqual({})
  })

  it.each<[string, () => void, ApprovalApiError, string]>([
    ['409 already_decided', () => {}, new ApprovalApiError(409, 'already_decided', '', approval('lead', { state: 'approved', decided_by: air26 })), 'decided_elsewhere'],
    ['409 member_relay_is_leads (no approval in the body, as the daemon sends it)', () => {}, new ApprovalApiError(409, 'member_relay_is_leads', 'member 的接力由 lead 安排; the request is cancelled'), 'failed'],
    ['409 member_relay_is_leads (with the cancelled approval)', () => {}, new ApprovalApiError(409, 'member_relay_is_leads', '', approval('self_relay', { state: 'cancelled' })), 'decided_elsewhere'],
    ['404', () => {}, new ApprovalApiError(404, 'not_found', ''), 'failed'],
    ['host_removed', () => {}, new ApprovalApiError(0, 'host_removed', ''), 'failed'],
    ['network (queued)', () => useHostStore.getState().setRuntime(H, { status: 'reconnecting' }), new ApprovalApiError(0, 'network', 'ECONNREFUSED'), 'queued'],
    ['network (failed)', () => {}, new ApprovalApiError(0, 'network', 'Failed to fetch'), 'failed'],
  ])('%s switches nothing', async (_label, arrange, err, outcome) => {
    arrange()
    const a = approval(err.code === 'member_relay_is_leads' ? 'self_relay' : 'lead')
    useApprovalStore.getState().applyOpened(H, a)
    mockedDecide.mockRejectedValueOnce(err)

    await expect(submitDecision(H, a, 'approve')).resolves.toBe(outcome)

    expect(mockedGoto).not.toHaveBeenCalled()
  })

  it('a gotoRequester that throws still returns \'closed\': the decision went through', async () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    try {
      const a = approval()
      useApprovalStore.getState().applyOpened(H, a)
      mockedDecide.mockResolvedValueOnce(approval('lead', { state: 'approved' }))
      mockedGoto.mockImplementationOnce(() => { throw new Error('tab store exploded') })

      await expect(submitDecision(H, a, 'approve')).resolves.toBe('closed')

      expect(mockedGoto).toHaveBeenCalledTimes(1)
      expect(useApprovalStore.getState().entries).toEqual({})
      expect(useApprovalStore.getState().queued).toEqual({})
      expect(useUndoToast.getState().toast).toBeNull()
      expect(warn).toHaveBeenCalledTimes(1)
    } finally {
      warn.mockRestore()
    }
  })
})

// U23 (unattended spec D-U23-6; plan PU-2b, ruling 36): "no toast" covers only a close nobody pressed (the WS `closed`
// of a daemon approval). A person's own approve or deny that loses to the daemon still hears who decided.
describe('submitDecision: a click that loses to the unattended daemon', () => {
  it.each<Decision>(['approve', 'deny'])('409 already_decided with decided_by unattended (%s) closes the dialog and toasts who decided', async (decision) => {
    const a = approval('lead')
    useApprovalStore.getState().applyOpened(H, a)
    const lost = approval('lead', { state: 'approved', decided_by: { kind: 'unattended', label: '無人值守模式' }, decided_at: 5 })
    mockedDecide.mockRejectedValueOnce(new ApprovalApiError(409, 'already_decided', '', lost))

    await expect(submitDecision(H, a, decision)).resolves.toBe('decided_elsewhere')

    expect(useApprovalStore.getState().entries).toEqual({})
    expect(useUndoToast.getState().toast?.message).toBe('mlab：purdex-7c 的 lead 申請 已由 無人值守模式 核准')
    expect(mockedGoto).not.toHaveBeenCalled()
  })
})
