// spa/src/lib/team/max-members-range.test.ts — the cap's range (name pool v2 spec §5.1): 1-8 against a daemon with `team.max_members.v1`, 1-9 against one that also lists `team.max_members.v2`.
import { describe, it, expect } from 'vitest'
import { isCapPair, maxMembersMax, MAX_MEMBERS_MAX_V1, MAX_MEMBERS_MAX_V2, leadPayloadOf, type Approval } from './types'

describe('maxMembersMax', () => {
  it('8 for a v1 daemon, 9 for a v2 one', () => {
    expect(MAX_MEMBERS_MAX_V1).toBe(8)
    expect(MAX_MEMBERS_MAX_V2).toBe(9)
    expect(maxMembersMax(false)).toBe(8)
    expect(maxMembersMax(true)).toBe(9)
  })
})

describe('what a daemon reports', () => {
  it('a cap of 9 with its usage is a truthful pair (only a v2 daemon says it); 10 is not', () => {
    expect(isCapPair(9, 9)).toBe(true)
    expect(isCapPair(9, 10)).toBe(false)
    expect(isCapPair(10, 1)).toBe(false)
  })
  it('a lead request keeps the ceiling it is given: 8 by default, 9 for a v2 host', () => {
    const ask = (n: number) => ({ payload: { max_members: n }, origin: { cwd: '/x' } }) as unknown as Approval
    expect(leadPayloadOf(ask(9)).max_members).toBe(8)
    expect(leadPayloadOf(ask(9), maxMembersMax(true)).max_members).toBe(9)
    expect(leadPayloadOf(ask(12), maxMembersMax(true)).max_members).toBe(9)
  })
})
