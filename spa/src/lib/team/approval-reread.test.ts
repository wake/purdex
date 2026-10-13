// spa/src/lib/team/approval-reread.test.ts — a card the App holds from `opened` that ran out while open (relay v2 App contract 3,
// `relay.unanswered.v1`). The daemon sends no event op for that, and a SOLO session's pause never changes the roster JSON, so
// neither a roster event nor the 30 s poll tells the App. The App reads the card itself: ~2 s after its deadline, if it is still open and
// not yet unanswered, one `GET /api/team/approvals/{id}`; if that answers open without `unanswered_at`, once more 15 s later, then no more.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { startUnansweredReread } from './approval-reread'
import * as api from './approval-api'
import { ApprovalApiError } from './approval-api'
import { approvalKey, useApprovalStore } from '../../stores/useApprovalStore'
import { useUnattendedStore } from '../../stores/useUnattendedStore'
import { usePausedStore } from '../../stores/usePausedStore'
import { useTeamRosterStore } from '../../stores/useTeamRosterStore'
import type { Approval } from './types'

vi.mock('./approval-api', async (orig) => ({ ...(await orig<typeof import('./approval-api')>()), fetchApproval: vi.fn() }))
const fetchApproval = vi.mocked(api.fetchApproval)

const H = 'h1'
const NOW = 1_000_000
const SENTINEL = Date.UTC(9999, 11, 31)
const card = (extra: Partial<Approval> = {}): Approval => ({
  id: 'c1', kind: 'self_relay', host_id: 'd1',
  origin: { session_id: 'S1', ref: '_aaaaaa', name: 'n', pid: 1, proc_start: 'p', cwd: '/w', tmux: '' },
  payload: { op_id: 'o', used_percentage: 50, window: 1 },
  state: 'open', created_at: NOW - 300_000, deadline_at: NOW + 10_000, lease_until: NOW + 30_000,
  ...extra,
})
const held = () => useApprovalStore.getState().entries[approvalKey(H, 'c1')]?.approval
const open = (a: Approval = card()) => useApprovalStore.getState().applyOpened(H, a)
const supported = (v: 'yes' | 'no' | 'unknown' = 'yes') => useUnattendedStore.getState().setUnansweredSupport(H, v)
const tick = (ms: number) => vi.advanceTimersByTimeAsync(ms)
const ranOut = (extra: Partial<Approval> = {}) => card({ ...extra, unanswered_at: NOW + 10_500, deadline_at: SENTINEL })
let stop: () => void

beforeEach(() => {
  vi.useFakeTimers({ now: NOW })
  fetchApproval.mockReset()
  useApprovalStore.getState().reset()
  useUnattendedStore.getState().reset()
  usePausedStore.getState().reset()
  useTeamRosterStore.getState().reset()
  stop = startUnansweredReread()
})
afterEach(() => {
  stop()
  vi.useRealTimers()
})

describe('reading a card that ran out while open', () => {
  // Mutation gate: never read → the card stays at its old deadline, no unanswered_at → red.
  it('reads once ~2 s after the deadline and takes unanswered_at and the sentinel deadline from the answer', async () => {
    supported()
    open()
    fetchApproval.mockResolvedValue(ranOut())
    await tick(11_999)
    expect(fetchApproval).not.toHaveBeenCalled()
    await tick(1)
    expect(fetchApproval).toHaveBeenCalledTimes(1)
    expect(fetchApproval).toHaveBeenCalledWith(H, 'c1')
    expect(held().unanswered_at).toBe(NOW + 10_500)
    expect(held().deadline_at).toBe(SENTINEL)
    await tick(60_000)
    expect(fetchApproval).toHaveBeenCalledTimes(1)
  })

  it('answers open without unanswered_at: reads once more 15 s later, then stops, and the card is left as it was', async () => {
    supported()
    open()
    fetchApproval.mockResolvedValue(card())
    await tick(12_000)
    expect(fetchApproval).toHaveBeenCalledTimes(1)
    await tick(14_999)
    expect(fetchApproval).toHaveBeenCalledTimes(1)
    await tick(1)
    expect(fetchApproval).toHaveBeenCalledTimes(2)
    await tick(300_000)
    expect(fetchApproval).toHaveBeenCalledTimes(2)
    expect(held().unanswered_at).toBeUndefined()
    expect(held().deadline_at).toBe(NOW + 10_000)
  })

  it('the second read can still catch it', async () => {
    supported()
    open()
    fetchApproval.mockResolvedValueOnce(card()).mockResolvedValueOnce(ranOut())
    await tick(12_000 + 15_000)
    expect(fetchApproval).toHaveBeenCalledTimes(2)
    expect(held().unanswered_at).toBe(NOW + 10_500)
  })

  it('a host without relay.unanswered.v1 (or not yet probed) is never read', async () => {
    open()
    await tick(60_000) // unknown
    supported('no')
    await tick(60_000)
    expect(fetchApproval).not.toHaveBeenCalled()
  })

  it('the capability learned after the deadline: read as soon as it is known', async () => {
    open()
    fetchApproval.mockResolvedValue(ranOut())
    await tick(60_000)
    expect(fetchApproval).not.toHaveBeenCalled()
    supported()
    await tick(0)
    expect(fetchApproval).toHaveBeenCalledTimes(1)
    expect(held().unanswered_at).toBe(NOW + 10_500)
  })

  it('a card closed before the read is not read', async () => {
    supported()
    open()
    await tick(5_000)
    useApprovalStore.getState().applyClosed(H, card({ state: 'approved' }))
    await tick(60_000)
    expect(fetchApproval).not.toHaveBeenCalled()
  })

  it('stopping cancels the pending read', async () => {
    supported()
    open()
    stop()
    await tick(60_000)
    expect(fetchApproval).not.toHaveBeenCalled()
  })

  it('a card that already has unanswered_at is not read', async () => {
    supported()
    open(ranOut())
    await tick(60_000)
    expect(fetchApproval).not.toHaveBeenCalled()
  })

  // The pause data (rule (b)) and the read are two roads to the same answer: whichever comes first counts.
  it('the pause data got there first: no read', async () => {
    supported()
    open()
    await tick(5_000)
    usePausedStore.getState().set(H, [{ session_id: 'S1', ref: '_x', title: '', role: 'solo', causes: ['relay_unanswered'], since: 1, lead_ref: '', bounced: 0 }], 1)
    await tick(60_000)
    expect(fetchApproval).not.toHaveBeenCalled()
  })

  it('a card whose deadline is long past when the app learns of it is read at once', async () => {
    supported()
    fetchApproval.mockResolvedValue(ranOut())
    open(card({ deadline_at: NOW - 60_000 }))
    await tick(0)
    expect(fetchApproval).toHaveBeenCalledTimes(1)
  })

  it('each card has its own reads', async () => {
    supported()
    open()
    open(card({ id: 'c2', deadline_at: NOW + 40_000 }))
    fetchApproval.mockResolvedValue(card())
    await tick(12_000)
    expect(fetchApproval.mock.calls.map((c) => c[1])).toEqual(['c1'])
    await tick(30_000)
    expect(fetchApproval.mock.calls.map((c) => c[1])).toEqual(['c1', 'c1', 'c2'])
  })

  describe('answers that are not an unanswered card are dropped quietly', () => {
    it('closed: no second read, card untouched here (the closed event or a snapshot removes it)', async () => {
      supported()
      open()
      fetchApproval.mockResolvedValue(card({ state: 'approved' }))
      await tick(120_000)
      expect(fetchApproval).toHaveBeenCalledTimes(1)
      expect(held().unanswered_at).toBeUndefined()
    })
    it('404: no second read', async () => {
      supported()
      open()
      fetchApproval.mockRejectedValue(new ApprovalApiError(404, 'not_found'))
      await tick(120_000)
      expect(fetchApproval).toHaveBeenCalledTimes(1)
    })
    it('a network failure: no throw, one retry like an open answer, then stop', async () => {
      supported()
      open()
      fetchApproval.mockRejectedValue(new ApprovalApiError(0, 'network', 'down'))
      await tick(120_000)
      expect(fetchApproval).toHaveBeenCalledTimes(2)
      expect(held()).toBeDefined()
    })
  })
})
