// spa/src/lib/team/relay-quota.test.ts — the confirmed numbers per (host, root) and what a stepper shows (plan RQ-A Task 3).
// The rule everything rests on: a GET row, a host event and a PUT answer each carry the pair and the root's `rev`; the
// App keeps whichever has the NOT SMALLER rev, so no arrival order can roll a number back. A stepper shows its field's
// desired value while that field has a write pending or in flight, the confirmed value otherwise.
import { describe, it, expect, beforeEach } from 'vitest'
import { useRelayQuotaStore, shownValue, quotaKey } from './relay-quota'
import type { RelayQuotaEvent, SessionQuota } from './types'

const H = 'h1'
const row = (over: Partial<SessionQuota> = {}): SessionQuota => ({
  session_id: 's1', root_session_id: 'r1', address: 'mlab/a-xx', is_lead: true, self_left: 3, member_pool_left: 2, rev: 5, ...over,
})
const ev = (over: Partial<RelayQuotaEvent> = {}): RelayQuotaEvent => ({
  op: 'changed', root_session_id: 'r1', self_left: 3, member_pool_left: 2, rev: 5, ...over,
})
const st = () => useRelayQuotaStore.getState()
const confirmed = (root = 'r1') => st().confirmed[quotaKey(H, root)]
const shown = (field: 'self_left' | 'member_pool_left', root = 'r1', fallback = 0) => shownValue(st(), H, root, field, fallback)

beforeEach(() => st().reset())

describe('the not-smaller rev rule', () => {
  it('rows seed the confirmed numbers', () => {
    st().beginGet(H)
    st().endGet(H, [row()])
    expect(confirmed()).toEqual({ self_left: 3, member_pool_left: 2, rev: 5 })
  })

  it('event → answer and answer → event both end at the newest numbers', () => {
    st().applyEvent(H, ev({ rev: 6, self_left: 4 }))
    st().applyAnswer(H, 'r1', { self_left: 5, member_pool_left: 2 }, 7)
    expect(confirmed()).toEqual({ self_left: 5, member_pool_left: 2, rev: 7 })
    st().reset()
    st().applyAnswer(H, 'r1', { self_left: 5, member_pool_left: 2 }, 7)
    st().applyEvent(H, ev({ rev: 6, self_left: 4 })) // arrives later but is older
    expect(confirmed()).toEqual({ self_left: 5, member_pool_left: 2, rev: 7 })
  })

  it('an equal rev replaces (not smaller), a smaller one never does', () => {
    st().applyEvent(H, ev({ rev: 6, self_left: 4 }))
    st().applyEvent(H, ev({ rev: 6, self_left: 9 }))
    expect(confirmed().self_left).toBe(9)
    st().applyEvent(H, ev({ rev: 5, self_left: 1 }))
    expect(confirmed().self_left).toBe(9)
  })

  it('an older GET after an event does not roll back', () => {
    st().applyEvent(H, ev({ rev: 8, self_left: 6 }))
    st().beginGet(H)
    st().endGet(H, [row({ rev: 5, self_left: 3 })])
    expect(confirmed()).toEqual({ self_left: 6, member_pool_left: 2, rev: 8 })
  })

  it('events that arrive while the GET is in flight are kept and applied after it, by the same rule', () => {
    st().beginGet(H)
    st().applyEvent(H, ev({ rev: 9, self_left: 8 })) // newer than the rows
    st().applyEvent(H, ev({ rev: 6, self_left: 1 })) // older than the rows
    expect(confirmed()).toBeUndefined() // nothing applied while the GET is out
    st().endGet(H, [row({ rev: 7, self_left: 3 })])
    expect(confirmed()).toEqual({ self_left: 8, member_pool_left: 2, rev: 9 })
  })

  it('a GET that failed still releases its buffered events', () => {
    st().beginGet(H)
    st().applyEvent(H, ev({ rev: 9, self_left: 8 }))
    st().endGet(H, null)
    expect(confirmed()?.self_left).toBe(8)
  })

  it('two GETs in flight: events wait for the last to end', () => {
    st().beginGet(H)
    st().beginGet(H)
    st().applyEvent(H, ev({ rev: 9 }))
    st().endGet(H, [row({ rev: 7 })])
    expect(confirmed()?.rev).toBe(7) // the event is still buffered
    st().endGet(H, null)
    expect(confirmed()?.rev).toBe(9)
  })

  it('rows of one root (the live session and a stale sibling) keep the newest', () => {
    st().beginGet(H)
    st().endGet(H, [row({ session_id: 'a', rev: 4, self_left: 1 }), row({ session_id: 'b', rev: 6, self_left: 2 })])
    expect(confirmed().self_left).toBe(2)
  })

  it('hosts and roots are separate keys', () => {
    st().applyEvent(H, ev({ rev: 6, self_left: 4 }))
    st().applyEvent('h2', ev({ rev: 2, self_left: 9 }))
    st().applyEvent(H, ev({ root_session_id: 'r2', rev: 1, self_left: 7 }))
    expect(confirmed().self_left).toBe(4)
    expect(st().confirmed[quotaKey('h2', 'r1')].self_left).toBe(9)
    expect(confirmed('r2').self_left).toBe(7)
  })
})

describe('what a stepper shows', () => {
  it('the confirmed value, else the row\'s own as the fallback', () => {
    expect(shown('self_left', 'r1', 4)).toBe(4)
    st().applyEvent(H, ev({ self_left: 2 }))
    expect(shown('self_left', 'r1', 4)).toBe(2)
  })

  it('the desired value while a write is pending, the in-flight one after it was sent, the confirmed one after it settled', () => {
    st().applyEvent(H, ev({ self_left: 2 }))
    st().setWrite(H, 'r1', 'self_left', { desired: 7 })
    expect(shown('self_left')).toBe(7)
    st().setWrite(H, 'r1', 'self_left', { desired: undefined, inflight: 7 })
    expect(shown('self_left')).toBe(7)
    st().setWrite(H, 'r1', 'self_left', { desired: 8, inflight: 7 }) // clicked again during the flight
    expect(shown('self_left')).toBe(8)
    st().clearWrite(H, 'r1', 'self_left')
    expect(shown('self_left')).toBe(2)
  })

  it('an answer or event about the pair never changes the other field\'s stepper while it has a write', () => {
    st().applyEvent(H, ev({ self_left: 2, member_pool_left: 2 }))
    st().setWrite(H, 'r1', 'self_left', { desired: 7 })
    st().applyAnswer(H, 'r1', { self_left: 2, member_pool_left: 5 }, 9) // our pool write's answer, say
    expect(shown('self_left')).toBe(7) // still our desired value
    expect(shown('member_pool_left')).toBe(5) // the other field follows the daemon
  })

  it('another window\'s newer event while our write is in flight is kept as the confirmed value', () => {
    st().applyEvent(H, ev({ rev: 5, self_left: 2 }))
    st().setWrite(H, 'r1', 'self_left', { desired: undefined, inflight: 7 })
    st().applyEvent(H, ev({ rev: 8, self_left: 1 })) // someone else wrote 1
    expect(shown('self_left')).toBe(7)
    st().clearWrite(H, 'r1', 'self_left')
    expect(shown('self_left')).toBe(1)
  })

  it('two rows of one root show the same numbers (the key is the root)', () => {
    st().applyEvent(H, ev({ root_session_id: 'chain', self_left: 6 }))
    expect(shown('self_left', 'chain')).toBe(6)
    expect(shown('self_left', 'chain')).toBe(shown('self_left', 'chain'))
  })
})

describe('forgetting', () => {
  it('forgetHost drops that host\'s numbers, writes and buffered events only', () => {
    st().applyEvent(H, ev({ self_left: 2 }))
    st().applyEvent('h2', ev({ self_left: 9 }))
    st().setWrite(H, 'r1', 'self_left', { desired: 7 })
    st().beginGet(H)
    st().forgetHost(H)
    expect(confirmed()).toBeUndefined()
    expect(st().writes).toEqual({})
    expect(st().confirmed[quotaKey('h2', 'r1')].self_left).toBe(9)
    st().applyEvent(H, ev({ rev: 1, self_left: 3 }))
    expect(confirmed().self_left).toBe(3) // not buffered by the forgotten GET
  })
})
