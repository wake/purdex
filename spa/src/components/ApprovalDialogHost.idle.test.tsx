// spa/src/components/ApprovalDialogHost.idle.test.tsx — an idle-relay ask (spec 2026-10-11 §6) says why it is asked now,
// one line above the usage; nothing else changes. Only well-formed payloads and only the self_relay kind.
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
const approval = (kind: ApprovalKind, payload: unknown): Approval => ({
  id: 'req-1', kind, host_id: 'd1',
  origin: { session_id: 'S1', ref: '_40iueq', name: 'purdex-7c', pid: 4242, proc_start: 'p', cwd: '/w/purdex', tmux: 'purdex:@1.%2' },
  payload,
  state: 'open', created_at: 1_000, deadline_at: 541_000, lease_until: 31_000,
})
const relay = (extra: Record<string, unknown>) => approval('self_relay', { op_id: 'op-1', used_percentage: 55, window: 200_000, ...extra })
const open = (a: Approval) => act(() => { useApprovalStore.getState().applyOpened(H, a) })
const note = () => screen.queryByTestId('approval-idle-note')

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

describe('idle relay line on the self-relay dialog', () => {
  // Mutation gate: drop the reason check → the other-reason case shows the line → red.
  it('reason idle with valid minutes: shows the line with the minutes, above the usage', () => {
    render(<ApprovalDialogHost />)
    open(relay({ reason: 'idle', idle_minutes: 40 }))
    expect(note()?.textContent).toBe('閒置 40 分鐘，快取快過期；趁現在接力最省。')
    const usage = screen.getByTestId('approval-usage')
    expect(note()!.compareDocumentPosition(usage) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
  })

  it('en', () => {
    useI18nStore.getState().setLocale('en')
    render(<ApprovalDialogHost />)
    open(relay({ reason: 'idle', idle_minutes: 40 }))
    expect(note()?.textContent).toBe('Idle 40 min; the cache expires soon — relaying now is cheapest.')
  })

  it.each([
    ['no reason', { idle_minutes: 40 }],
    ['another reason', { reason: 'threshold', idle_minutes: 40 }],
    ['no minutes', { reason: 'idle' }],
    ['0 minutes', { reason: 'idle', idle_minutes: 0 }],
    ['60 minutes', { reason: 'idle', idle_minutes: 60 }],
    ['fractional minutes', { reason: 'idle', idle_minutes: 1.5 }],
    ['string minutes', { reason: 'idle', idle_minutes: '40' }],
    ['non-string reason', { reason: 7, idle_minutes: 40 }],
    ['an ordinary ask', {}],
  ])('%s: no line, the usage still shows', (_name, extra) => {
    render(<ApprovalDialogHost />)
    open(relay(extra))
    expect(note()).toBeNull()
    expect(screen.getByTestId('approval-usage').textContent).toBe('已用 55%')
  })

  it.each<ApprovalKind>(['lead', 'member_relay', 'adopt'])('%s: never shows the line, even with idle fields in the payload', (kind) => {
    render(<ApprovalDialogHost />)
    open(approval(kind, { reason: 'idle', idle_minutes: 40, member_ref: '_m', used_percentage: 90, window: 200_000, max_members: 3, roots: ['/w'] }))
    expect(note()).toBeNull()
  })
})
