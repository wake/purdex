// spa/src/components/ApprovalDialogHost.recapBrief.test.tsx — plan T14b: the relay cards (self relay, member relay) show the newest recap of the session that is
// about to hand over (headline, 目前, 下一步) where the workbook's report used to be; no recap, no section, and the card never waits for it.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, act } from '@testing-library/react'
import type { Recap, RecapsResponse } from '../lib/dialogue/types'

const fetchRecaps = vi.fn<(h: string, s: string, limit?: number) => Promise<RecapsResponse>>()
vi.mock('../lib/dialogue/api', () => ({
  fetchRecaps: (...a: [string, string, number?]) => fetchRecaps(...a),
  fetchTurnSummaries: vi.fn(async () => []),
  requestRecap: vi.fn(),
}))
vi.mock('../lib/team/approval-api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../lib/team/approval-api')>()),
  decideApproval: vi.fn(),
}))

import { ApprovalDialogHost } from './ApprovalDialogHost'
import { useApprovalStore } from '../stores/useApprovalStore'
import { useDialogueStore } from '../stores/useDialogueStore'
import { useHostStore } from '../stores/useHostStore'
import { useI18nStore } from '../stores/useI18nStore'
import { useUndoToast } from '../stores/useUndoToast'
import { __resetClientDescriptorForTests } from '../lib/team/client-label'
import type { Approval } from '../lib/team/types'

const H = 'h1'
const selfRelay = (): Approval => ({
  id: 'req-s1', kind: 'self_relay', host_id: 'd1',
  origin: { session_id: 'S1', ref: '_aaaaaa', name: 'purdex-88-b8', pid: 4242, proc_start: 'p', cwd: '/w/purdex', tmux: 'purdex:@1.%2', title: 'purdex-iface-wb', address: 'mlab/purdex-88-b8' },
  payload: { op_id: 'op-1', used_percentage: 71, window: 1000000 },
  state: 'open', created_at: 1_000, deadline_at: 1_000 + 600_000, lease_until: 1_000 + 600_000,
})
const memberRelay = (): Approval => ({
  id: 'req-m1', kind: 'member_relay', host_id: 'd1',
  origin: { session_id: 'LEAD1', ref: '_lllll1', name: 'lead', pid: 1, proc_start: 'p', cwd: '/w', tmux: '', title: 'the-lead' },
  payload: { op_id: 'op-1', team_id: 't-1', lead_ref: '_lllll1', lead_title: 'the-lead', member_session_id: 'M1', member_ref: '_bbbbbb', member_title: 'the-member', used_percentage: 72.4 },
  state: 'open', created_at: 1_000, deadline_at: 1_000 + 600_000, lease_until: 1_000 + 600_000,
})
const recap = (id: number, over: Partial<Recap> = {}): Recap =>
  ({ id, at: 1000, from_no: 1, to_no: 30, trigger: 'auto', headline: `headline ${id}`, done: ['finished one'], now: `now ${id}`, decided: [], waiting: [], next: `next ${id}`, ...over })
const resp = (recaps: Recap[]): RecapsResponse => ({ recaps, can_generate: true, newest_turn_no: 30, last_recap_to_no: 30 })
const open = (a: Approval) => act(() => { useApprovalStore.getState().applyOpened(H, a) })
const settle = async () => { await act(async () => { await new Promise((r) => setTimeout(r, 0)) }) }

beforeEach(() => {
  useI18nStore.getState().setLocale('zh-TW')
  useApprovalStore.getState().reset()
  useHostStore.setState({
    hosts: { [H]: { id: H, name: 'mlab', ip: '1.2.3.4', port: 7860, order: 0 } },
    hostOrder: [H], runtime: { [H]: { status: 'connected' } }, activeHostId: H,
  })
  useUndoToast.setState({ toast: null, notice: null })
  __resetClientDescriptorForTests()
  Object.defineProperty(window, 'electronAPI', { value: undefined, writable: true, configurable: true })
  fetchRecaps.mockReset()
  useDialogueStore.getState().reset()
  useDialogueStore.getState().setSupport(H, true)
})
afterEach(() => { useHostStore.getState().reset() })

describe('relay card — the newest recap', () => {
  it('self_relay shows the headline, 目前 and 下一步 of the session\'s newest recap', async () => {
    fetchRecaps.mockResolvedValue(resp([recap(2), recap(1)]))
    render(<ApprovalDialogHost />)
    open(selfRelay())
    await settle()
    expect(fetchRecaps).toHaveBeenCalledWith(H, 'S1', 1)
    const brief = screen.getByTestId('approval-brief')
    expect(brief).toHaveTextContent('headline 2')
    expect(screen.getByTestId('approval-brief-now')).toHaveTextContent('目前')
    expect(screen.getByTestId('approval-brief-now')).toHaveTextContent('now 2')
    expect(screen.getByTestId('approval-brief-next')).toHaveTextContent('下一步')
    expect(screen.getByTestId('approval-brief-next')).toHaveTextContent('next 2')
    expect(brief).not.toHaveTextContent('finished one') // the headline, the present and the next step only
  })

  it('member_relay reads the member\'s session, not the lead\'s', async () => {
    fetchRecaps.mockResolvedValue(resp([recap(1, { headline: 'member headline' })]))
    render(<ApprovalDialogHost />)
    open(memberRelay())
    await settle()
    expect(fetchRecaps).toHaveBeenCalledWith(H, 'M1', 1)
    expect(screen.getByTestId('approval-brief')).toHaveTextContent('member headline')
  })

  it('leaves out an empty field', async () => {
    fetchRecaps.mockResolvedValue(resp([recap(1, { now: '' })]))
    render(<ApprovalDialogHost />)
    open(selfRelay())
    await settle()
    expect(screen.queryByTestId('approval-brief-now')).toBeNull()
    expect(screen.getByTestId('approval-brief-next')).toBeTruthy()
  })

  it('no recap: no section; the card is there all the same', async () => {
    fetchRecaps.mockResolvedValue(resp([]))
    render(<ApprovalDialogHost />)
    open(selfRelay())
    await settle()
    expect(screen.queryByTestId('approval-brief')).toBeNull()
    expect(screen.getByTestId('approval-ref')).toBeTruthy()
  })

  it('a host without dialogue.v1: no section and no request', async () => {
    useDialogueStore.getState().setSupport(H, false)
    render(<ApprovalDialogHost />)
    open(selfRelay())
    await settle()
    expect(screen.queryByTestId('approval-brief')).toBeNull()
    expect(fetchRecaps).not.toHaveBeenCalled()
  })

  it('a failed read leaves the card as it was', async () => {
    fetchRecaps.mockRejectedValue(new Error('down'))
    render(<ApprovalDialogHost />)
    open(selfRelay())
    await settle()
    expect(screen.queryByTestId('approval-brief')).toBeNull()
    expect(screen.getByTestId('approval-ref')).toBeTruthy()
  })
})
