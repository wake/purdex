import { describe, expect, it } from 'vitest'
import { isApproval, isUnknownKindRow, memberRelayPayloadOf, type Approval } from './types'

// RQ-2 §8: `member_relay` is a kind this build knows (the card shows it), the payload is read defensively.

const row = (over: Record<string, unknown> = {}) => ({
  id: 'r1', kind: 'member_relay', host_id: 'd1',
  origin: { session_id: 'S1', ref: '_aaaaaa', name: 'lead', cwd: '/w', tmux: '', pid: 1, proc_start: 'p' },
  payload: { op_id: 'op', team_id: 't', lead_ref: '_a', lead_title: 'L', member_session_id: 'M', member_ref: '_b', member_title: 'M', used_percentage: 50 },
  state: 'open', created_at: 1, deadline_at: 2, lease_until: 2,
  ...over,
})

describe('member_relay on the wire', () => {
  it('is a known kind: the row passes isApproval and is not skipped as a later daemon\'s', () => {
    expect(isApproval(row())).toBe(true)
    expect(isUnknownKindRow(row())).toBe(false)
  })

  it('an unknown kind is still skipped, and a malformed member_relay row is still refused', () => {
    expect(isUnknownKindRow(row({ kind: 'something_new' }))).toBe(true)
    expect(isApproval(row({ kind: 'something_new' }))).toBe(false)
    expect(isApproval(row({ payload: 'x' }))).toBe(false)
    expect(isApproval(row({ origin: null }))).toBe(false)
  })

  it('memberRelayPayloadOf reads the payload as it is', () => {
    expect(memberRelayPayloadOf(row() as unknown as Approval)).toEqual({
      op_id: 'op', team_id: 't', lead_ref: '_a', lead_title: 'L', member_session_id: 'M', member_ref: '_b', member_title: 'M', used_percentage: 50,
    })
  })

  it('and defensively: wrong types become empty strings, a bad percentage 0', () => {
    const p = memberRelayPayloadOf(row({ payload: { op_id: 5, lead_title: null, member_title: {}, used_percentage: 'x' } }) as unknown as Approval)
    expect(p).toEqual({ op_id: '', team_id: '', lead_ref: '', lead_title: '', member_session_id: '', member_ref: '', member_title: '', used_percentage: 0 })
    expect(memberRelayPayloadOf(row({ payload: { used_percentage: Number.POSITIVE_INFINITY } }) as unknown as Approval).used_percentage).toBe(0)
  })
})
