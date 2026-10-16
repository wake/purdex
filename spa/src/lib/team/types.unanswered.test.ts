// spa/src/lib/team/types.unanswered.test.ts — `unanswered_at` (unix ms) on an Approval (relay v2 `relay.unanswered.v1`): the
// validator accepts it as a number, rejects any other type, and `isUnanswered` is true only for a positive value.
import { describe, it, expect } from 'vitest'
import { isApproval, isUnanswered, type Approval } from './types'

const base = {
  id: 'a1', kind: 'self_relay', state: 'open', host_id: 'd1',
  origin: { session_id: 'S1', ref: '_aaaaaa', name: 'n', pid: 1, proc_start: 'p', cwd: '/w', tmux: '' },
  payload: { op_id: 'o', used_percentage: 50, window: 1 },
  created_at: 1, deadline_at: 2, lease_until: 3,
}

describe('isApproval: unanswered_at', () => {
  it.each([[undefined], [0], [1_700_000_000_000]])('accepts %s', (v) => {
    expect(isApproval(v === undefined ? base : { ...base, unanswered_at: v })).toBe(true)
  })
  it.each([['x'], [null], [true], [{}], [[]]])('rejects %j', (v) => {
    expect(isApproval({ ...base, unanswered_at: v })).toBe(false)
  })
})

describe('isUnanswered', () => {
  const a = (extra: Partial<Approval>) => ({ ...base, ...extra }) as unknown as Approval
  it('true for a positive unanswered_at only', () => {
    expect(isUnanswered(a({ unanswered_at: 5 }))).toBe(true)
    expect(isUnanswered(a({ unanswered_at: 0 }))).toBe(false)
    expect(isUnanswered(a({}))).toBe(false)
  })
})
