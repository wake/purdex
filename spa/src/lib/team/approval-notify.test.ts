// spa/src/lib/team/approval-notify.test.ts — the system notification for a new lead request (lead-team spec §6.3):
// raised through the existing Electron `showNotification` path on `opened` only — never from a snapshot, never twice
// for one request — with `action {kind:'open-approval', hostId}` and `broadcastTs = created_at`. U14: no browser path.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { useApprovalStore } from '../../stores/useApprovalStore'
import { useHostStore } from '../../stores/useHostStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { handleApprovalEvent } from './approval-ws'
import { notifyApprovalOpened } from './approval-notify'
import type { Approval } from './types'

const H = 'h1'
const approval = (over: Partial<Approval> = {}): Approval => ({
  id: 'req-1', kind: 'lead', host_id: 'd1',
  origin: { session_id: 'S1', ref: '_40iueq', name: 'purdex-7c', pid: 1, proc_start: 'p', cwd: '/w/purdex', tmux: '' },
  payload: { reason: '要平行跑三個 PR', max_members: 3, roots: ['/w/purdex'] },
  state: 'open', created_at: 1_696_000_000_000, deadline_at: 1_696_000_540_000, lease_until: 0,
  ...over,
})
const showNotification = vi.fn()
const NotificationCtor = vi.fn()

beforeEach(() => {
  useI18nStore.getState().setLocale('zh-TW')
  useApprovalStore.getState().reset()
  useHostStore.setState({
    hosts: { [H]: { id: H, name: 'mlab', ip: '1.2.3.4', port: 7860, order: 0 } },
    hostOrder: [H], runtime: {}, activeHostId: H,
  })
  showNotification.mockClear()
  NotificationCtor.mockClear()
  Object.defineProperty(window, 'electronAPI', { value: { showNotification }, writable: true, configurable: true })
  vi.stubGlobal('Notification', Object.assign(NotificationCtor, { permission: 'granted' }))
})
afterEach(() => {
  vi.unstubAllGlobals()
  Object.defineProperty(window, 'electronAPI', { value: undefined, writable: true, configurable: true })
  useHostStore.getState().reset()
})

describe('notifyApprovalOpened', () => {
  it('raises the Electron notification: title per spec §6.3, the reason as body, open-approval action, created_at as broadcastTs', () => {
    notifyApprovalOpened(H, approval())
    expect(showNotification).toHaveBeenCalledTimes(1)
    expect(showNotification.mock.calls[0][0]).toEqual({
      title: 'mlab：purdex-7c 申請成為 lead',
      body: '要平行跑三個 PR',
      sessionCode: '',
      eventName: 'ApprovalRequest',
      broadcastTs: 1_696_000_000_000,
      action: { kind: 'open-approval', hostId: H },
    })
    expect(NotificationCtor).not.toHaveBeenCalled()
  })

  it('outside Electron it does nothing — no browser Notification fallback (U14)', () => {
    Object.defineProperty(window, 'electronAPI', { value: undefined, writable: true, configurable: true })
    notifyApprovalOpened(H, approval())
    expect(showNotification).not.toHaveBeenCalled()
    expect(NotificationCtor).not.toHaveBeenCalled()
  })
})

describe('handleApprovalEvent → notification', () => {
  it('fires on `opened` once per request; a duplicate opened does not fire again', () => {
    handleApprovalEvent(H, JSON.stringify({ op: 'opened', approval: approval() }))
    handleApprovalEvent(H, JSON.stringify({ op: 'opened', approval: approval() }))
    expect(showNotification).toHaveBeenCalledTimes(1)
  })

  it('never fires from a snapshot (a reconnect must not re-announce what is already on screen)', () => {
    handleApprovalEvent(H, JSON.stringify({ op: 'snapshot', approvals: [approval(), approval({ id: 'b' })] }))
    expect(showNotification).not.toHaveBeenCalled()
  })

  it('does not fire on closed', () => {
    handleApprovalEvent(H, JSON.stringify({ op: 'opened', approval: approval() }))
    handleApprovalEvent(H, JSON.stringify({ op: 'closed', approval: approval({ state: 'timeout' }) }))
    expect(showNotification).toHaveBeenCalledTimes(1)
  })
})
