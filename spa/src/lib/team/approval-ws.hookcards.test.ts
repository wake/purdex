// spa/src/lib/team/approval-ws.hookcards.test.ts — the hook kinds the Mac App draws no card for are still held, in
// useHookCardStore, so the Dock badge can count them (app-badge spec §4.1). The existing hook handling (nothing
// reaches the approval store, the toast or the notification) is pinned in approval-ws.hooks.test.ts and untouched.
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { useApprovalStore } from '../../stores/useApprovalStore'
import { useHookCardStore } from '../../stores/useHookCardStore'
import { handleApprovalEvent } from './approval-ws'
import type { Approval } from './types'

const origin = { session_id: 'S1', ref: '_x', name: 'n', pid: 1, proc_start: 'p', cwd: '/w', tmux: 'work:@1.%1' }
const row = (id: string, kind: Approval['kind'], over: Partial<Approval> = {}): Approval => ({
  id, kind, host_id: 'd1', origin, payload: kind === 'lead' ? { reason: 'r', max_members: 3, roots: ['/w'] } : {},
  state: 'open', created_at: 1, deadline_at: 2, lease_until: 3, ...over,
})
const held = () => Object.values(useHookCardStore.getState().entries).map((e) => `${e.hostId}/${e.approval.id}`).sort()

beforeEach(() => {
  useApprovalStore.getState().reset()
  useHookCardStore.getState().reset()
  Object.defineProperty(window, 'electronAPI', { value: { showNotification: vi.fn() }, writable: true, configurable: true })
})

describe('approval-ws feeds the hook-card slice', () => {
  it('a snapshot replaces the host\'s hook cards; the non-hook rows go to the approval store only', () => {
    handleApprovalEvent('h1', { op: 'snapshot', approvals: [row('a', 'hook_ask'), row('l', 'lead'), row('p', 'hook_permission')] })
    expect(held()).toEqual(['h1/a', 'h1/p'])
    expect(Object.values(useApprovalStore.getState().entries).map((e) => e.approval.id)).toEqual(['l'])
    handleApprovalEvent('h1', { op: 'snapshot', approvals: [row('p', 'hook_permission')] })
    expect(held()).toEqual(['h1/p'])
  })

  it('a snapshot of one host leaves another host\'s hook cards', () => {
    handleApprovalEvent('h1', { op: 'snapshot', approvals: [row('a', 'hook_ask')] })
    handleApprovalEvent('h2', { op: 'snapshot', approvals: [row('b', 'hook_ask')] })
    handleApprovalEvent('h1', { op: 'snapshot', approvals: [] })
    expect(held()).toEqual(['h2/b'])
  })

  it('opened adds a hook card, closed removes it; a lead request never enters the slice', () => {
    handleApprovalEvent('h1', { op: 'opened', approval: row('a', 'hook_ask') })
    handleApprovalEvent('h1', { op: 'opened', approval: row('l', 'lead') })
    expect(held()).toEqual(['h1/a'])
    handleApprovalEvent('h1', { op: 'closed', approval: row('a', 'hook_ask', { state: 'answered_local' }) })
    expect(held()).toEqual([])
  })

  it('a malformed snapshot is dropped whole and does not wipe the held hook cards', () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    handleApprovalEvent('h1', { op: 'snapshot', approvals: [row('a', 'hook_ask')] })
    handleApprovalEvent('h1', { op: 'snapshot', approvals: 'nope' })
    expect(held()).toEqual(['h1/a'])
    warn.mockRestore()
  })

  it('a hook card raises no notification', () => {
    handleApprovalEvent('h1', { op: 'opened', approval: row('a', 'hook_ask') })
    expect(window.electronAPI?.showNotification).not.toHaveBeenCalled()
  })
})
