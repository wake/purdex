// spa/src/lib/team/approval-notify.test.ts — the system notification for a new lead request (lead-team spec §6.3):
// raised through the existing Electron `showNotification` path on `opened` only — never from a snapshot, never twice
// for one request — with `action {kind:'open-approval', hostId, requestId}` and a `broadcastTs` that identifies the request (host id +
// request id hashed), not its `created_at`: two requests born in the same millisecond must both be announced. U14: no browser path.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { useApprovalStore } from '../../stores/useApprovalStore'
import { useHostStore } from '../../stores/useHostStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { handleApprovalEvent } from './approval-ws'
import { approvalBroadcastTs, notifyApprovalOpened } from './approval-notify'
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

describe('notifyApprovalOpened — adopt', () => {
  it('an adopt request names the lead and the target, and its body is the target’s cwd', () => {
    notifyApprovalOpened(H, approval({
      kind: 'adopt',
      payload: { team_id: 'T', lead_session_id: 'S1', target_ref: '_tgt001', target_session_id: 'S2', title: '', target_name: 'doc-writer', target_address: '', target_cwd: '/w/docs', target_tmux: '' },
    }))
    expect(showNotification.mock.calls[0][0]).toMatchObject({ title: 'mlab：purdex-7c 想納入 doc-writer', body: '/w/docs', eventName: 'ApprovalRequest' })
  })

  it('the target’s own label in the title is cleaned of direction marks and clipped', () => {
    notifyApprovalOpened(H, approval({
      kind: 'adopt',
      payload: { team_id: 'T', lead_session_id: 'S1', target_ref: '_t', target_session_id: 'S2', title: '\u202e' + 'x'.repeat(200), target_name: '', target_address: '', target_cwd: '/w', target_tmux: '' },
    }))
    const title = showNotification.mock.calls[0][0].title as string
    expect(title).not.toContain('\u202e')
    expect(title.endsWith('…')).toBe(true)
    expect(title.length).toBeLessThan(100)
  })
})

describe('notifyApprovalOpened', () => {
  it('raises the Electron notification: title per spec §6.3, the reason as body, open-approval action, a per-request broadcastTs', () => {
    notifyApprovalOpened(H, approval())
    expect(showNotification).toHaveBeenCalledTimes(1)
    expect(showNotification.mock.calls[0][0]).toEqual({
      title: 'mlab：purdex-7c 申請成為 lead',
      body: '要平行跑三個 PR',
      sessionCode: '',
      eventName: 'ApprovalRequest',
      broadcastTs: approvalBroadcastTs(H, 'req-1'),
      action: { kind: 'open-approval', hostId: H, requestId: 'req-1' },
    })
    expect(NotificationCtor).not.toHaveBeenCalled()
  })

  it('a self_relay request is titled with the rounded usage and carries the spec §8.7 note as body (P5a-3b)', () => {
    notifyApprovalOpened(H, approval({ kind: 'self_relay', payload: { op_id: 'op-1', used_percentage: 72.6, window: 1_000_000 } }))
    expect(showNotification).toHaveBeenCalledTimes(1)
    expect(showNotification.mock.calls[0][0]).toMatchObject({
      title: 'mlab：purdex-7c 申請接力（已用 73%）',
      body: '核准後這個 session 會寫接力檔、清空並在原處接手（約 1 分鐘）',
      action: { kind: 'open-approval', hostId: H, requestId: 'req-1' },
    })
  })

  it('a member_relay request names the lead, the member and the usage, and says the member quota is out', () => {
    notifyApprovalOpened(H, approval({
      kind: 'member_relay',
      payload: { op_id: 'op-1', team_id: 't-1', lead_ref: '_a', lead_title: 'iface-lead', member_session_id: 'M1', member_ref: '_b', member_title: 'iface-solo', used_percentage: 72.6 },
    }))
    expect(showNotification).toHaveBeenCalledTimes(1)
    expect(showNotification.mock.calls[0][0]).toMatchObject({
      title: 'mlab：iface-lead 要幫 member iface-solo 接力（context 73%）',
      body: 'member 額度用完，要核准嗎？',
      action: { kind: 'open-approval', hostId: H, requestId: 'req-1' },
    })
  })

  it('a member_relay whose aliases are only direction marks still names the lead and the member', () => {
    notifyApprovalOpened(H, approval({
      kind: 'member_relay',
      payload: { op_id: 'op-1', team_id: 't-1', lead_ref: '_a', lead_title: '\u202e', member_session_id: 'M1', member_ref: '_bbbbbb', member_title: '\u200b', used_percentage: 10 },
    }))
    const title = (showNotification.mock.calls[0][0] as { title: string }).title
    expect(title).toBe('mlab：purdex-7c 要幫 member _bbbbbb 接力（context 10%）')
  })

  it('broadcastTs is the request\'s identity, not its created_at: two requests born in the same millisecond on two hosts get two keys (F4)', () => {
    const a = approval({ id: 'same-ms-1', created_at: 1_696_000_000_000 })
    const b = approval({ id: 'same-ms-2', created_at: 1_696_000_000_000 })
    notifyApprovalOpened('h1', a)
    notifyApprovalOpened('h2', b)
    expect(showNotification).toHaveBeenCalledTimes(2)
    const [ts1, ts2] = showNotification.mock.calls.map((c) => (c[0] as { broadcastTs: number }).broadcastTs)
    expect(ts1).not.toBe(ts2)
    expect(ts1).not.toBe(1_696_000_000_000)
    for (const ts of [ts1, ts2]) {
      expect(Number.isSafeInteger(ts)).toBe(true)
      expect(ts).toBeGreaterThanOrEqual(0)
    }
    // The same request id on two hosts is two requests too.
    expect(approvalBroadcastTs('h1', 'req-1')).not.toBe(approvalBroadcastTs('h2', 'req-1'))
  })

  it('the same request announced twice (two windows of one device) gets the same broadcastTs, so Electron dedups it', () => {
    notifyApprovalOpened(H, approval())
    notifyApprovalOpened(H, approval())
    const [ts1, ts2] = showNotification.mock.calls.map((c) => (c[0] as { broadcastTs: number }).broadcastTs)
    expect(ts1).toBe(ts2)
    expect(approvalBroadcastTs(H, 'req-1')).toBe(ts1)
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
