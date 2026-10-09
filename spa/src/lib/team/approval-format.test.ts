// spa/src/lib/team/approval-format.test.ts — the pdx address the dialog prints for the requesting session, including
// an origin an unexpected daemon left incomplete: the formatter must not throw on a missing `ref` (F2).
import { describe, it, expect } from 'vitest'
import { approvalKindLabel, approvalSessionLabel, closedToastText, formatOriginAddress } from './approval-format'
import type { Origin } from './types'

const origin = (over: Partial<Origin> = {}): Origin => ({
  session_id: 'S1', ref: '_40iueq', name: 'purdex-7c', pid: 1, proc_start: 'p', cwd: '/w/purdex', tmux: '',
  ...over,
})

describe('formatOriginAddress', () => {
  it('prints `<host>/<name> [<ref6>]` for a routable name and `<host>/_<ref6>` without one; the daemon\'s address wins', () => {
    expect(formatOriginAddress('mlab', origin())).toBe('mlab/purdex-7c [40iueq]')
    expect(formatOriginAddress('mlab', origin({ name: '' }))).toBe('mlab/_40iueq')
    expect(formatOriginAddress('mlab', origin({ ref: '40iueq' }))).toBe('mlab/purdex-7c [40iueq]')
    expect(formatOriginAddress('mlab', origin({ address: 'air26/purdex-7c' }))).toBe('air26/purdex-7c')
  })

  it('does not throw when `ref` is not a string: falls back to the name, else `?`', () => {
    const noRef = origin({ ref: undefined as unknown as string })
    expect(() => formatOriginAddress('mlab', noRef)).not.toThrow()
    expect(formatOriginAddress('mlab', noRef)).toBe('mlab/purdex-7c')
    expect(formatOriginAddress('mlab', origin({ ref: null as unknown as string, name: '' }))).toBe('mlab/?')
    expect(() => approvalSessionLabel(noRef)).not.toThrow()
  })
})

describe('adopt (U24)', () => {
  const t = (k: string, vars?: Record<string, unknown>) => (vars ? `${k} ${JSON.stringify(vars)}` : k)
  it('has its own kind label', () => {
    expect(approvalKindLabel(t, 'adopt')).toBe('approval.kind.adopt')
    expect(approvalKindLabel(t, 'self_relay')).toBe('approval.kind.self_relay')
    expect(approvalKindLabel(t, 'lead')).toBe('approval.kind.lead')
  })
  it('a cancelled adopt toasts its state, naming the kind', () => {
    const a = {
      id: 'r', kind: 'adopt', host_id: 'd', origin: origin(), payload: {}, state: 'cancelled', close_reason: 'adopt_target_is_lead',
      created_at: 1, deadline_at: 2, lease_until: 3,
    } as const
    expect(closedToastText(t, 'mlab', a)).toContain('approval.kind.adopt')
    expect(closedToastText(t, 'mlab', a)).toContain('approval.state.cancelled')
  })
})
