// spa/src/lib/team/approval-ws.hooks.test.ts — the 分流 kinds on the wire (lead-team spec §6.6, U19 (b)): a
// hook_ask / hook_permission row is a valid approval (a snapshot carrying one is not malformed and must not be
// dropped whole), but the Mac App draws no card for it — it never reaches the store, the toast or the notification.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { useApprovalStore, selectOpenCountFor } from '../../stores/useApprovalStore'
import { useHostStore } from '../../stores/useHostStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { useUndoToast } from '../../stores/useUndoToast'
import { handleApprovalEvent, parseApprovalEvent } from './approval-ws'
import { isApproval, isHookKind, type Approval } from './types'

const H = 'h1'
const lead = (over: Partial<Approval> = {}): Approval => ({
  id: 'req-1', kind: 'lead', host_id: 'd1',
  origin: { session_id: 'S1', ref: '_40iueq', name: 'purdex-7c', pid: 1, proc_start: 'p', cwd: '/w/purdex', tmux: '' },
  payload: { reason: 'r', max_members: 3, roots: ['/w/purdex'] },
  state: 'open', created_at: 1_000, deadline_at: 541_000, lease_until: 31_000,
  ...over,
})
const ask = (over: Partial<Approval> = {}): Approval => lead({
  id: 'ask-1', kind: 'hook_ask',
  payload: { tool_use_id: 'toolu_1', questions: [{ question: '紅還是藍？', header: '顏色', options: [{ label: '紅' }, { label: '藍' }], multiSelect: false }] },
  deadline_at: 32503680000000,
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
  Object.defineProperty(window, 'electronAPI', { value: { showNotification: vi.fn() }, writable: true, configurable: true })
})
afterEach(() => useHostStore.getState().reset())

describe('approval-ws: 分流 kinds (spec §6.6, U19 (b))', () => {
  it('the wire shape accepts the two kinds, the three close states, decided_by terminal and `hook`', () => {
    expect(isHookKind('hook_ask') && isHookKind('hook_permission') && !isHookKind('lead')).toBe(true)
    expect(isApproval(ask())).toBe(true)
    expect(isApproval(ask({ kind: 'hook_permission', payload: { tool_use_id: '', tool_name: 'Bash', tool_input: {} } }))).toBe(true)
    for (const state of ['answered_local', 'terminal_override', 'dismissed'] as const) {
      expect(isApproval(ask({ state, decided_by: { kind: 'terminal', label: 'terminal' }, decided_at: 5, hook: { answers: { '紅還是藍？': '紅' } } }))).toBe(true)
    }
    expect(isApproval(ask({ hook: 'x' as unknown as Record<string, unknown> }))).toBe(false)
  })

  it('a snapshot with a hook_ask beside a lead request is NOT dropped whole: the lead request is held, the hook row is not', () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    handleApprovalEvent(H, JSON.stringify({ op: 'snapshot', approvals: [ask(), lead()] }))
    expect(warn).not.toHaveBeenCalled()
    warn.mockRestore()
    const entries = Object.values(useApprovalStore.getState().entries)
    expect(entries.map((e) => e.approval.id)).toEqual(['req-1'])
    expect(selectOpenCountFor(H)(useApprovalStore.getState())).toBe(1)
  })

  it('parseApprovalEvent still returns the hook rows (the boundary validates them); handleApprovalEvent ignores opened and closed for them', () => {
    expect(parseApprovalEvent(JSON.stringify({ op: 'opened', approval: ask() }))).toEqual({ op: 'opened', approval: ask() })
    handleApprovalEvent(H, JSON.stringify({ op: 'opened', approval: ask() }))
    expect(useApprovalStore.getState().entries).toEqual({})
    expect(window.electronAPI?.showNotification).not.toHaveBeenCalled()
    const closed = ask({ state: 'answered_local', decided_by: { kind: 'terminal', label: 'terminal' }, decided_at: 5, hook: { answers: { '紅還是藍？': '紅' } } })
    handleApprovalEvent(H, JSON.stringify({ op: 'closed', approval: closed }))
    expect(useUndoToast.getState().toast).toBeNull()
    const remote = ask({ state: 'approved', decided_by: { kind: 'app', label: 'Purdex iOS @ phone' }, decided_at: 5, hook: { answers: { '紅還是藍？': '藍' } } })
    handleApprovalEvent(H, JSON.stringify({ op: 'closed', approval: remote }))
    expect(useUndoToast.getState().toast).toBeNull()
    expect(useApprovalStore.getState().closedIds[H]).toBeUndefined()
  })

  it('a kind this build does not know (a later daemon) is skipped row by row: the snapshot still applies, opened/closed of it are ignored', () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    const future = { ...lead({ id: 'fut-1' }), kind: 'something_new' }
    handleApprovalEvent(H, JSON.stringify({ op: 'snapshot', approvals: [future, lead()] }))
    expect(Object.values(useApprovalStore.getState().entries).map((e) => e.approval.id)).toEqual(['req-1'])
    handleApprovalEvent(H, JSON.stringify({ op: 'opened', approval: future }))
    expect(Object.keys(useApprovalStore.getState().entries)).toHaveLength(1)
    expect(warn).not.toHaveBeenCalled()
    // a malformed row of a KNOWN kind still rejects the whole frame (P3's whole-frame validation)
    const bad = { ...lead({ id: 'bad-1' }), payload: 'x' }
    expect(parseApprovalEvent(JSON.stringify({ op: 'snapshot', approvals: [bad, lead()] }))).toBeNull()
    warn.mockRestore()
  })
})
