// spa/src/components/ApprovalDialogHost.unanswered.test.tsx — a final relay card past its deadline stays open with `unanswered_at`
// and a sentinel `deadline_at` (relay v2 plan, App contract 3, `relay.unanswered.v1`): the dialog shows no countdown and says
// 「已暫停作業，等你核准」. A card without `unanswered_at` (or with 0) keeps today's countdown.
import { describe, it, expect, beforeEach, afterEach } from 'vitest'
import { render, screen, act } from '@testing-library/react'
import { ApprovalDialogHost } from './ApprovalDialogHost'
import { useApprovalStore } from '../stores/useApprovalStore'
import { useHostStore } from '../stores/useHostStore'
import { useI18nStore } from '../stores/useI18nStore'
import { useUndoToast } from '../stores/useUndoToast'
import { __resetClientDescriptorForTests } from '../lib/team/client-label'
import type { Approval, ApprovalKind } from '../lib/team/types'

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
