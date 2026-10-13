// spa/src/components/ApprovalDialogHost.unanswered.test.tsx — a final relay card past its deadline stays open with `unanswered_at`
// and a sentinel `deadline_at` (relay v2 plan, App contract 3, `relay.unanswered.v1`): the dialog shows no countdown and says
// 「已暫停作業，等你核准」. A card without `unanswered_at` (or with 0) keeps today's countdown.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { render, screen, act } from '@testing-library/react'
import { ApprovalDialogHost } from './ApprovalDialogHost'
import { useApprovalStore } from '../stores/useApprovalStore'
import { useHostStore } from '../stores/useHostStore'
import { useI18nStore } from '../stores/useI18nStore'
import { useUndoToast } from '../stores/useUndoToast'
import { usePausedStore } from '../stores/usePausedStore'
import { useTeamRosterStore } from '../stores/useTeamRosterStore'
import type { PauseCause } from '../lib/team/roster'
import { __resetClientDescriptorForTests } from '../lib/team/client-label'
import { useUnattendedStore } from '../stores/useUnattendedStore'
import * as approvalApi from '../lib/team/approval-api'
import type { Approval, ApprovalKind } from '../lib/team/types'

vi.mock('../lib/team/approval-api', async (orig) => ({ ...(await orig<typeof import('../lib/team/approval-api')>()), fetchApproval: vi.fn() }))
const fetchApproval = vi.mocked(approvalApi.fetchApproval)

const H = 'h1'
const SENTINEL = Date.UTC(9999, 11, 31)
const approval = (kind: ApprovalKind, payload: unknown, extra: Partial<Approval> = {}): Approval => ({
  id: 'req-1', kind, host_id: 'd1',
  origin: { session_id: 'S1', ref: '_40iueq', name: 'purdex-7c', pid: 4242, proc_start: 'p', cwd: '/w/purdex', tmux: 'purdex:@1.%2' },
  payload,
  state: 'open', created_at: 1_000, deadline_at: 541_000, lease_until: 31_000,
  ...extra,
})
const relay = (extra: Partial<Approval> = {}) => approval('self_relay', { op_id: 'op-1', used_percentage: 55, window: 200_000 }, extra)
const open = (a: Approval) => act(() => { useApprovalStore.getState().applyOpened(H, a) })

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
  __resetClientDescriptorForTests()
  Object.defineProperty(window, 'electronAPI', { value: undefined, writable: true, configurable: true })
})
afterEach(() => {
  useHostStore.getState().reset()
})

describe('the last relay card past its deadline', () => {
  // Mutation gate: ignore unanswered_at → the sentinel deadline is counted down → red.
  it('unanswered_at > 0: no countdown, 「已暫停作業，等你核准」', () => {
    render(<ApprovalDialogHost />)
    open(relay({ unanswered_at: 541_500, deadline_at: SENTINEL }))
    expect(screen.queryByTestId('approval-countdown')).toBeNull()
    expect(screen.getByTestId('approval-unanswered').textContent).toBe('已暫停作業，等你核准')
  })

  it('en', () => {
    useI18nStore.getState().setLocale('en')
    render(<ApprovalDialogHost />)
    open(relay({ unanswered_at: 541_500, deadline_at: SENTINEL }))
    expect(screen.getByTestId('approval-unanswered').textContent).toBe('Work is paused; waiting for your approval')
  })

  // Mutation gate: test `!== undefined` instead of `> 0` → the 0 case loses its countdown → red.
  it.each([['absent', undefined], ['0', 0]])('unanswered_at %s: the countdown stays', (_n, at) => {
    render(<ApprovalDialogHost />)
    open(relay(at === undefined ? {} : { unanswered_at: at }))
    expect(screen.getByTestId('approval-countdown')).toBeTruthy()
    expect(screen.queryByTestId('approval-unanswered')).toBeNull()
  })

  it('the 核准 and 拒絕 buttons are still there', () => {
    render(<ApprovalDialogHost />)
    open(relay({ unanswered_at: 541_500, deadline_at: SENTINEL }))
    expect(screen.getByTestId('approval-approve')).toBeTruthy()
    expect(screen.getByTestId('approval-deny')).toBeTruthy()
  })
})

// 88's rule (b): the daemon sends no event op when the last card runs out, so a card an App holds from `opened` has no
// `unanswered_at`. Past its deadline, the card's session holding `relay_unanswered` (paused list or roster, same host) is the signal.
describe('a card held since opened, past its deadline', () => {
  const pausedFor = (hostId: string, sessionId: string, causes: PauseCause[]) => act(() => {
    usePausedStore.getState().set(hostId, [{ session_id: sessionId, ref: '_x', title: '', role: 'solo', causes, since: 1, lead_ref: '', bounced: 0 }], 1)
  })
  beforeEach(() => {
    usePausedStore.getState().reset()
    useTeamRosterStore.getState().reset()
  })

  // Mutation gate: drop rule (b) -> the card keeps counting down -> red.
  it('shows no countdown once the pause data adds relay_unanswered', () => {
    render(<ApprovalDialogHost />)
    open(relay()) // deadline_at 541_000 is long past
    expect(screen.getByTestId('approval-countdown')).toBeTruthy()
    pausedFor(H, 'S1', ['relay_unanswered'])
    expect(screen.queryByTestId('approval-countdown')).toBeNull()
    expect(screen.getByTestId('approval-unanswered').textContent).toBe('已暫停作業，等你核准')
  })

  it('a roster row with the cause counts as well', () => {
    render(<ApprovalDialogHost />)
    open(relay())
    act(() => {
      useTeamRosterStore.getState().apply(H, [{
        id: 't', host_id: 'd1', created_at: 1, team_name: '', team_label: '',
        lead: { session_id: 'S1', ref: '_x', address: 'h/x', live: true, paused: { causes: ['relay_unanswered'], since: 1 } }, members: [],
      }])
    })
    expect(screen.getByTestId('approval-unanswered')).toBeTruthy()
  })

  it('past the deadline but no relay_unanswered: the countdown stays', () => {
    render(<ApprovalDialogHost />)
    open(relay())
    pausedFor(H, 'S1', ['quota'])
    expect(screen.getByTestId('approval-countdown')).toBeTruthy()
    expect(screen.queryByTestId('approval-unanswered')).toBeNull()
  })

  it('the same session id paused on another host does not count', () => {
    render(<ApprovalDialogHost />)
    open(relay())
    pausedFor('h2', 'S1', ['relay_unanswered'])
    expect(screen.getByTestId('approval-countdown')).toBeTruthy()
  })

  it('the cause on a card that is not yet past its deadline does not count', () => {
    render(<ApprovalDialogHost />)
    open(relay({ deadline_at: Date.now() + 3_600_000 }))
    pausedFor(H, 'S1', ['relay_unanswered'])
    expect(screen.getByTestId('approval-countdown')).toBeTruthy()
  })

  // The reconnect snapshot is where a card held since `opened` learns it ran out; the pause stores may still be empty then.
  it('a reconnect snapshot with unanswered_at flips the open dialog at once, with no pause data', () => {
    render(<ApprovalDialogHost />)
    open(relay())
    expect(screen.getByTestId('approval-countdown')).toBeTruthy()
    act(() => { useApprovalStore.getState().applySnapshot(H, [relay({ unanswered_at: 541_500, deadline_at: SENTINEL })]) })
    expect(screen.queryByTestId('approval-countdown')).toBeNull()
    expect(screen.getByTestId('approval-unanswered').textContent).toBe('已暫停作業，等你核准')
  })
})

// A SOLO session's pause leaves the roster JSON unchanged and cards exist only with 無人值守 off (no 30 s paused poll), so nothing
// tells the App its card ran out. The host reads the card itself ~2 s after its deadline (approval-reread.ts).
describe('the dialog host reads a card that ran out while open', () => {
  const NOW = 5_000_000
  beforeEach(() => {
    vi.useFakeTimers({ now: NOW })
    fetchApproval.mockReset()
    usePausedStore.getState().reset()
    useTeamRosterStore.getState().reset()
    useUnattendedStore.getState().reset()
  })
  afterEach(() => { vi.useRealTimers() })

  // Mutation gate: take the reread out → the dialog stays on its countdown with no pause data at all → red.
  it('a solo card, no pause data at all: after the deadline the read brings unanswered_at and the dialog says 已暫停作業', async () => {
    useUnattendedStore.getState().setUnansweredSupport(H, 'yes')
    render(<ApprovalDialogHost />)
    open(relay({ created_at: NOW - 300_000, deadline_at: NOW + 10_000 }))
    fetchApproval.mockResolvedValue(relay({ created_at: NOW - 300_000, unanswered_at: NOW + 10_500, deadline_at: SENTINEL }))
    expect(screen.getByTestId('approval-countdown')).toBeTruthy()
    await act(async () => { await vi.advanceTimersByTimeAsync(12_000) })
    expect(fetchApproval).toHaveBeenCalledWith(H, 'req-1')
    expect(screen.queryByTestId('approval-countdown')).toBeNull()
    expect(screen.getByTestId('approval-unanswered').textContent).toBe('已暫停作業，等你核准')
  })

  it('a host without the capability is not read: the dialog stays on its countdown', async () => {
    render(<ApprovalDialogHost />)
    open(relay({ created_at: NOW - 300_000, deadline_at: NOW + 10_000 }))
    await act(async () => { await vi.advanceTimersByTimeAsync(60_000) })
    expect(fetchApproval).not.toHaveBeenCalled()
    expect(screen.getByTestId('approval-countdown')).toBeTruthy()
  })

  it('the read answers open without unanswered_at: the countdown stays', async () => {
    useUnattendedStore.getState().setUnansweredSupport(H, 'yes')
    render(<ApprovalDialogHost />)
    open(relay({ created_at: NOW - 300_000, deadline_at: NOW + 10_000 }))
    fetchApproval.mockResolvedValue(relay({ created_at: NOW - 300_000, deadline_at: NOW + 10_000 }))
    await act(async () => { await vi.advanceTimersByTimeAsync(40_000) })
    expect(fetchApproval).toHaveBeenCalledTimes(2)
    expect(screen.getByTestId('approval-countdown')).toBeTruthy()
  })
})
