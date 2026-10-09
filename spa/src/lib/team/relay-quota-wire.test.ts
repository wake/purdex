// spa/src/lib/team/relay-quota-wire.test.ts — the relay-quota wire shapes at the trust boundary (plan RQ-A Task 1; daemon
// `internal/team/wire_quota.go` after D1 / D2): rows are accepted whole or rejected whole, values are integers 0-99, every
// carrier of a pair has a `rev`.
import { describe, it, expect } from 'vitest'
import { parseSessionQuotas, parseRelayQuotaView, parseRelayQuotaEvent } from './relay-quota-wire'

const row = (over: Record<string, unknown> = {}) => ({
  session_id: 's1', root_session_id: 'r1', title: 'Lead A', address: 'mlab/lead-a-xx', is_lead: true,
  self_left: 3, member_pool_left: 2, rev: 7, ...over,
})

describe('parseSessionQuotas', () => {
  it('accepts a good array, and the title is optional', () => {
    const { title: _t, ...noTitle } = row({ session_id: 's2' })
    expect(parseSessionQuotas([row(), noTitle])).toEqual([row(), noTitle])
  })

  it('an empty array is a successful read with no session', () => {
    expect(parseSessionQuotas([])).toEqual([])
  })

  it('rejects whole on null, a non-array, or any malformed row (no silent partial list)', () => {
    expect(parseSessionQuotas(null)).toBeNull()
    expect(parseSessionQuotas({})).toBeNull()
    expect(parseSessionQuotas('x')).toBeNull()
    for (const bad of [
      row({ session_id: '' }), row({ session_id: 5 }), row({ root_session_id: undefined }), row({ address: 1 }),
      row({ is_lead: 'yes' }), row({ title: 3 }), row({ self_left: '3' }), row({ member_pool_left: undefined }),
      row({ rev: -1 }), row({ rev: 1.5 }), row({ rev: undefined }),
    ]) {
      expect(parseSessionQuotas([row(), bad])).toBeNull()
    }
  })

  it('rejects values outside 0-99 or not integers', () => {
    for (const v of [-1, 100, 3.5, NaN, Infinity]) {
      expect(parseSessionQuotas([row({ self_left: v })])).toBeNull()
      expect(parseSessionQuotas([row({ member_pool_left: v })])).toBeNull()
    }
    expect(parseSessionQuotas([row({ self_left: 0, member_pool_left: 99 })])).not.toBeNull()
  })
})

describe('parseRelayQuotaView (the PUT answer)', () => {
  const view = (over: Record<string, unknown> = {}) => ({
    session_id: 's1', root_session_id: 'r1', self_left: 3, member_pool_left: 0, rev: 8, updated_at: 123, updated_by: 'Purdex.app @ air26', ...over,
  })
  it('accepts the answer, with pending_lineage and updated_by optional', () => {
    expect(parseRelayQuotaView(view())).toEqual(view())
    const { updated_by: _u, ...rest } = view({ pending_lineage: true })
    expect(parseRelayQuotaView(rest)).toEqual(rest)
  })
  it('rejects a missing rev, a bad value, or a non-object', () => {
    expect(parseRelayQuotaView(view({ rev: undefined }))).toBeNull()
    expect(parseRelayQuotaView(view({ self_left: 100 }))).toBeNull()
    expect(parseRelayQuotaView(view({ pending_lineage: 'yes' }))).toBeNull()
    expect(parseRelayQuotaView(view({ updated_at: 'x' }))).toBeNull()
    expect(parseRelayQuotaView(null)).toBeNull()
    expect(parseRelayQuotaView([])).toBeNull()
  })
})

describe('parseRelayQuotaEvent (the host event value)', () => {
  const ev = { op: 'changed', root_session_id: 'r1', self_left: 2, member_pool_left: 1, rev: 9 }
  it('accepts the object or its JSON text', () => {
    expect(parseRelayQuotaEvent(ev)).toEqual(ev)
    expect(parseRelayQuotaEvent(JSON.stringify(ev))).toEqual(ev)
  })
  it('explains why it rejects (a string), never returns a half event', () => {
    for (const bad of ['{', 'null', '[]', { ...ev, op: 'snapshot' }, { ...ev, root_session_id: '' }, { ...ev, rev: undefined }, { ...ev, self_left: -1 }]) {
      expect(typeof parseRelayQuotaEvent(bad)).toBe('string')
    }
  })
})
