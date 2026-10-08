import { describe, it, expect, vi, beforeEach } from 'vitest'
import { decideHookFrame, parseAgentSnapshot, type HookCursor } from './hook-cursor'

const cur = (epoch = 'E1', last = 10): HookCursor => ({ epoch, last })

beforeEach(() => { vi.spyOn(console, 'warn').mockImplementation(() => {}) })

describe('decideHookFrame', () => {
  it('no epoch and no seq, no cursor → legacy', () => {
    expect(decideHookFrame(null, {})).toBe('legacy')
  })
  it('no epoch and no seq, with a cursor → drop (a v2 daemon always stamps both)', () => {
    expect(decideHookFrame(cur(), {})).toBe('drop')
  })
  it.each([
    ['empty epoch', { epoch: '', seq: 1 }],
    ['numeric epoch', { epoch: 5, seq: 1 }],
    ['epoch without seq', { epoch: 'E1' }],
    ['seq without epoch', { seq: 1 }],
    ['negative seq', { epoch: 'E1', seq: -1 }],
    ['float seq', { epoch: 'E1', seq: 1.5 }],
    ['seq above 2^53', { epoch: 'E1', seq: 2 ** 53 }],
    ['string seq', { epoch: 'E1', seq: '11' }],
  ])('malformed (%s) → drop', (_, value) => {
    expect(decideHookFrame(cur(), value)).toBe('drop')
    expect(decideHookFrame(null, value)).toBe('drop')
  })
  it('before this connection\'s snapshot → drop', () => {
    expect(decideHookFrame(null, { epoch: 'E1', seq: 11 })).toBe('drop')
  })
  it('a foreign epoch → resync', () => {
    expect(decideHookFrame(cur('E1', 10), { epoch: 'E2', seq: 11 })).toBe('resync')
    expect(decideHookFrame(cur('E1', 10), { epoch: 'E1-1', seq: 1 })).toBe('resync') // counter rotation
  })
  it('a duplicate or old seq → drop', () => {
    expect(decideHookFrame(cur('E1', 10), { epoch: 'E1', seq: 10 })).toBe('drop')
    expect(decideHookFrame(cur('E1', 10), { epoch: 'E1', seq: 3 })).toBe('drop')
  })
  it('a gap → resync', () => {
    expect(decideHookFrame(cur('E1', 10), { epoch: 'E1', seq: 12 })).toBe('resync')
  })
  it('the next seq → apply', () => {
    expect(decideHookFrame(cur('E1', 10), { epoch: 'E1', seq: 11 })).toBe('apply')
  })
  it('seq 0 snapshot cursor accepts seq 1', () => {
    expect(decideHookFrame(cur('E1', 0), { epoch: 'E1', seq: 1 })).toBe('apply')
  })
})

describe('parseAgentSnapshot', () => {
  const ev = { agent_type: 'cc', status: 'idle', raw_event_name: 'replay', broadcast_ts: 1 }
  it('a valid snapshot', () => {
    expect(parseAgentSnapshot({ epoch: 'E1', seq: 7, sessions: [{ session: 'a', event: ev }] }))
      .toEqual({ epoch: 'E1', seq: 7, sessions: [{ session: 'a', event: ev }] })
  })
  it('an empty host with seq 0', () => {
    expect(parseAgentSnapshot({ epoch: 'E1', seq: 0, sessions: [] })).toEqual({ epoch: 'E1', seq: 0, sessions: [] })
  })
  it.each([
    ['not an object', 'x'],
    ['null', null],
    ['no epoch', { seq: 1, sessions: [] }],
    ['empty epoch', { epoch: '', seq: 1, sessions: [] }],
    ['no seq', { epoch: 'E1', sessions: [] }],
    ['negative seq', { epoch: 'E1', seq: -1, sessions: [] }],
    ['sessions not an array', { epoch: 'E1', seq: 1, sessions: {} }],
    ['an entry that is not an object', { epoch: 'E1', seq: 1, sessions: ['a'] }],
    ['an entry with an empty code', { epoch: 'E1', seq: 1, sessions: [{ session: '', event: ev }] }],
    ['an entry without an event', { epoch: 'E1', seq: 1, sessions: [{ session: 'a' }] }],
  ])('malformed (%s) → null', (_, value) => {
    expect(parseAgentSnapshot(value)).toBeNull()
  })
})
