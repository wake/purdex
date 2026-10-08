// spa/src/lib/team/approval-decide.epoch.test.ts — a decision in flight when its host is forgotten (#1978 review): the
// host was removed or re-pointed while the HTTP call was out, so whatever the answer, it belongs to the old daemon and
// must not touch the host id's new generation. The new daemon may already have sent a request with the SAME id.
// The daemon call is mocked; the stores are real.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { approvalKey, useApprovalStore } from '../../stores/useApprovalStore'
import { useHostStore } from '../../stores/useHostStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { useUndoToast } from '../../stores/useUndoToast'
import { ApprovalApiError, decideApproval } from './approval-api'
import { gotoRequester } from './approval-goto'
import { submitDecision } from './approval-decide'
import { __resetClientDescriptorForTests } from './client-label'
import type { Approval } from './types'

vi.mock('./approval-api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('./approval-api')>()),
  decideApproval: vi.fn(),
  setSelfRelayPause: vi.fn(),
}))
vi.mock('./approval-goto', async (importOriginal) => ({
  ...(await importOriginal<typeof import('./approval-goto')>()),
  gotoRequester: vi.fn(() => 'none'),
}))
const mockedDecide = vi.mocked(decideApproval)
const mockedGoto = vi.mocked(gotoRequester)

const H = 'h1'
const approval = (over: Partial<Approval> = {}): Approval => ({
  id: 'req-1', kind: 'lead', host_id: 'd1',
  origin: { session_id: 'S1', ref: '_40iueq', name: 'purdex-7c', pid: 1, proc_start: 'p', cwd: '/w/purdex', tmux: 'purdex:@1.%2' },
  payload: { reason: 'r', max_members: 3, roots: ['/w/purdex'] },
  state: 'open', created_at: 1_000, deadline_at: 541_000, lease_until: 31_000,
  ...over,
})

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

/** Click, let the send go out, then re-point the host and let the NEW daemon's same-id request arrive. */
async function sendThenRepoint(opts: { fromQueue?: boolean } = {}) {
  const old = approval()
  useApprovalStore.getState().applyOpened(H, old)
  let settle!: { resolve: (a: Approval) => void; reject: (e: unknown) => void }
  mockedDecide.mockImplementationOnce(() => new Promise<Approval>((resolve, reject) => { settle = { resolve, reject } }))
  const p = submitDecision(H, old, 'approve', undefined, opts)
  await vi.waitFor(() => expect(mockedDecide).toHaveBeenCalledTimes(1))
  useApprovalStore.getState().forgetHost(H)
  const fresh = approval({ origin: { ...old.origin, session_id: 'S-new' } })
  expect(useApprovalStore.getState().applyOpened(H, fresh)).toBe(true)
  return { p, settle, fresh }
}

function expectNewGenerationUntouched(fresh: Approval) {
  const st = useApprovalStore.getState()
  expect(st.entries[approvalKey(H, fresh.id)]?.approval).toBe(fresh)
  expect(st.closedIds[H]).toBeUndefined()
  expect(st.queued).toEqual({})
  expect(useUndoToast.getState().toast).toBeNull()
  expect(mockedGoto).not.toHaveBeenCalled()
}

describe('submitDecision when the host is forgotten while the decide is in flight', () => {
  it('200: no applyClosed (the new same-id request stays, no tombstone), no goto', async () => {
    const { p, settle, fresh } = await sendThenRepoint()
    settle.resolve(approval({ state: 'approved' }))
    await p
    expectNewGenerationUntouched(fresh)
  })

  it('409 already_decided: no close, no toast, no tombstone', async () => {
    const { p, settle, fresh } = await sendThenRepoint()
    settle.reject(new ApprovalApiError(409, 'already_decided', '', approval({ state: 'denied', decided_by: { kind: 'app', label: 'Purdex.app @ air26' } })))
    await p
    expectNewGenerationUntouched(fresh)
  })

  it('404: no close, no failure toast', async () => {
    const { p, settle, fresh } = await sendThenRepoint()
    settle.reject(new ApprovalApiError(404, 'not_found', ''))
    await p
    expectNewGenerationUntouched(fresh)
  })

  it('network error from the queue: not re-queued (the new daemon snapshot must not resend an old decision)', async () => {
    const { p, settle, fresh } = await sendThenRepoint({ fromQueue: true })
    settle.reject(new ApprovalApiError(0, 'network', 'ECONNREFUSED'))
    await p
    expectNewGenerationUntouched(fresh)
  })

  it('network error with the host not connected: not queued either', async () => {
    const { p, settle, fresh } = await sendThenRepoint()
    useHostStore.getState().setRuntime(H, { status: 'reconnecting' })
    settle.reject(new ApprovalApiError(0, 'network', 'ECONNREFUSED'))
    await p
    expectNewGenerationUntouched(fresh)
  })

  it('a forget during the epoch the caller captured earlier (the dialog\'s pause await) also voids the send', async () => {
    const old = approval()
    useApprovalStore.getState().applyOpened(H, old)
    const epoch = useApprovalStore.getState().hostEpoch[H] ?? 0
    useApprovalStore.getState().forgetHost(H)
    const fresh = approval({ origin: { ...old.origin, session_id: 'S-new' } })
    useApprovalStore.getState().applyOpened(H, fresh)

    await submitDecision(H, old, 'approve', undefined, { epoch })

    expect(mockedDecide).not.toHaveBeenCalled()
    expectNewGenerationUntouched(fresh)
    expect(useApprovalStore.getState().decidedHere).toEqual({})
  })

  it('control: with no forget in between, the same 200 closes the request', async () => {
    const old = approval()
    useApprovalStore.getState().applyOpened(H, old)
    mockedDecide.mockResolvedValueOnce(approval({ state: 'approved' }))
    await expect(submitDecision(H, old, 'approve')).resolves.toBe('closed')
    expect(useApprovalStore.getState().entries).toEqual({})
    expect(mockedGoto).toHaveBeenCalledTimes(1)
  })
})
