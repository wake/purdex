// spa/src/lib/rebuild/session-version.test.ts — the per-host held session-list
// version and the apply/stale decision (#1255 SPA spec §3.1; daemon contract
// docs/specs/2026-09-23-session-list-fresh-spec.md §3.3–§3.4).
import { describe, it, expect, beforeEach } from 'vitest'
import {
  __resetForTests, clearHeld, connectionClosed, connectionOpened, currentConn, decide,
  forgetHost, heldVersion, note, parseVersion,
} from './session-version'

const H = 'h1'
const E1 = '9f3c1a0b7d2e4c61'
const E2 = '0123456789abcdef'

beforeEach(() => __resetForTests())

describe('parseVersion', () => {
  it('accepts a 16-lowercase-hex epoch and a safe integer seq ≥ 1', () => {
    expect(parseVersion({ epoch: E1, seq: 1 })).toEqual({ epoch: E1, seq: 1 })
    expect(parseVersion({ epoch: E1, seq: Number.MAX_SAFE_INTEGER, extra: 'x' }))
      .toEqual({ epoch: E1, seq: Number.MAX_SAFE_INTEGER })
  })

  it.each([
    ['short epoch', { epoch: '9f3c1a0b7d2e4c6', seq: 1 }],
    ['long epoch', { epoch: '9f3c1a0b7d2e4c611', seq: 1 }],
    ['upper-case epoch', { epoch: '9F3C1A0B7D2E4C61', seq: 1 }],
    ['non-hex epoch', { epoch: '9f3c1a0b7d2e4c6g', seq: 1 }],
    ['numeric epoch', { epoch: 1234567890123456, seq: 1 }],
    ['seq 0', { epoch: E1, seq: 0 }],
    ['negative seq', { epoch: E1, seq: -3 }],
    ['float seq', { epoch: E1, seq: 1.5 }],
    ['seq above 2^53−1', { epoch: E1, seq: 2 ** 53 }],
    ['string seq', { epoch: E1, seq: '4' }],
    ['no seq', { epoch: E1 }],
    ['no epoch', { seq: 4 }],
  ])('rejects %s', (_label, x) => {
    expect(parseVersion(x)).toBeNull()
  })

  it.each([null, undefined, 'x', 3, [E1, 1]])('rejects the non-object %j', (x) => {
    expect(parseVersion(x)).toBeNull()
  })
})

describe('decide', () => {
  it('applies anything while nothing is held', () => {
    expect(decide(H, { epoch: E1, seq: 5 }, { kind: 'ws' })).toBe('apply')
    expect(decide(H, { epoch: E1, seq: 5 }, { kind: 'fetch', conn: currentConn(H) - 7 })).toBe('apply')
  })

  it('same epoch: applies only a strictly newer seq, on either channel', () => {
    note(H, { epoch: E1, seq: 10 })
    const conn = currentConn(H)
    for (const origin of [{ kind: 'ws' } as const, { kind: 'fetch', conn } as const]) {
      expect(decide(H, { epoch: E1, seq: 11 }, origin)).toBe('apply')
      expect(decide(H, { epoch: E1, seq: 10 }, origin)).toBe('stale')
      expect(decide(H, { epoch: E1, seq: 9 }, origin)).toBe('stale')
    }
  })

  it('same epoch and newer seq applies even from a fetch sent on an older connection', () => {
    note(H, { epoch: E1, seq: 10 })
    const conn = currentConn(H)
    connectionClosed(H)
    connectionOpened(H)
    expect(decide(H, { epoch: E1, seq: 11 }, { kind: 'fetch', conn })).toBe('apply')
  })

  it('different epoch via the current socket: applies (it speaks for the running process)', () => {
    note(H, { epoch: E1, seq: 10 })
    expect(decide(H, { epoch: E2, seq: 1 }, { kind: 'ws' })).toBe('apply')
  })

  it('different epoch via fetch: applies only when sent on the current connection', () => {
    note(H, { epoch: E1, seq: 10 })
    const conn = currentConn(H)
    expect(decide(H, { epoch: E2, seq: 1 }, { kind: 'fetch', conn })).toBe('apply')
    connectionClosed(H)
    expect(decide(H, { epoch: E2, seq: 1 }, { kind: 'fetch', conn })).toBe('stale')
  })

  it('a fetch captured before the connection\'s onOpen that returns a different epoch after it is stale (codex #8)', () => {
    note(H, { epoch: E1, seq: 10 })
    const conn = currentConn(H) // captured while the new socket is still opening
    connectionOpened(H)
    expect(decide(H, { epoch: E2, seq: 50 }, { kind: 'fetch', conn })).toBe('stale')
  })

  it('does not change what is held', () => {
    note(H, { epoch: E1, seq: 10 })
    decide(H, { epoch: E1, seq: 11 }, { kind: 'ws' })
    expect(heldVersion(H)).toEqual({ epoch: E1, seq: 10 })
  })
})

describe('held / conn bookkeeping', () => {
  it('connectionOpened / connectionClosed each bump conn', () => {
    const c0 = currentConn(H)
    connectionOpened(H)
    expect(currentConn(H)).toBe(c0 + 1)
    connectionClosed(H)
    expect(currentConn(H)).toBe(c0 + 2)
  })

  it('conn is per host', () => {
    const other = currentConn('h2')
    connectionOpened(H)
    expect(currentConn('h2')).toBe(other)
  })

  it('note records the version', () => {
    expect(heldVersion(H)).toBeNull()
    note(H, { epoch: E1, seq: 3 })
    expect(heldVersion(H)).toEqual({ epoch: E1, seq: 3 })
    note(H, { epoch: E2, seq: 1 })
    expect(heldVersion(H)).toEqual({ epoch: E2, seq: 1 })
  })

  it('forgetHost (entry teardown) clears held and bumps conn', () => {
    note(H, { epoch: E1, seq: 3 })
    const conn = currentConn(H)
    forgetHost(H)
    expect(heldVersion(H)).toBeNull()
    expect(currentConn(H)).not.toBe(conn)
  })

  it('an unversioned frame (clearHeld) makes the next versioned list apply', () => {
    note(H, { epoch: E1, seq: 30 })
    clearHeld(H)
    expect(heldVersion(H)).toBeNull()
    expect(decide(H, { epoch: E1, seq: 2 }, { kind: 'ws' })).toBe('apply')
  })

  it('__resetForTests forgets everything', () => {
    note(H, { epoch: E1, seq: 3 })
    connectionOpened(H)
    __resetForTests()
    expect(heldVersion(H)).toBeNull()
    expect(currentConn(H)).toBe(0)
  })
})
