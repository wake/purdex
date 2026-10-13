// spa/src/lib/read-sync/cursor.test.ts — the hook cursor as a read position (app-badge spec §7.2). T is `(epoch, seq)`,
// not a time: a read covers an unread iff they share the epoch and the unread's seq <= T.seq.
import { describe, expect, it } from 'vitest'
import { covers, cursorOf, maxCursor, parseReadCursor } from './cursor'

describe('cursorOf — the cursor of an event', () => {
  it('a frame from the emit slot carries one', () => {
    expect(cursorOf({ epoch: 'E1', seq: 7 })).toEqual({ epoch: 'E1', seq: 7 })
  })

  it('epoch "" (a frame not from the emit slot) or no epoch / seq is no cursor', () => {
    expect(cursorOf({ epoch: '', seq: 0 })).toBeUndefined()
    expect(cursorOf({ epoch: '', seq: 5 })).toBeUndefined()
    expect(cursorOf({})).toBeUndefined()
    expect(cursorOf({ epoch: 'E1' })).toBeUndefined()
    expect(cursorOf({ seq: 3 })).toBeUndefined()
  })

  it('a seq that is not a non-negative integer is no cursor', () => {
    expect(cursorOf({ epoch: 'E1', seq: -1 })).toBeUndefined()
    expect(cursorOf({ epoch: 'E1', seq: 1.5 })).toBeUndefined()
    expect(cursorOf({ epoch: 'E1', seq: Number.NaN })).toBeUndefined()
    expect(cursorOf({ epoch: 'E1', seq: '3' as unknown as number })).toBeUndefined()
  })
})

describe('covers — the cover rule', () => {
  const T = { epoch: 'E1', seq: 10 }

  it('the same epoch with seq <= T.seq is covered', () => {
    expect(covers({ epoch: 'E1', seq: 10 }, T)).toBe(true)
    expect(covers({ epoch: 'E1', seq: 3 }, T)).toBe(true)
  })

  it('a later seq stays unread', () => {
    expect(covers({ epoch: 'E1', seq: 11 }, T)).toBe(false)
  })

  it('another epoch (a daemon restart) never clears anything, whatever the seq', () => {
    expect(covers({ epoch: 'E0', seq: 1 }, T)).toBe(false)
    expect(covers({ epoch: 'E2', seq: 1 }, T)).toBe(false)
    expect(covers({ epoch: 'E1-1', seq: 1 }, T)).toBe(false) // a counter rotation suffix is another epoch
  })

  it('an unread with no cursor is covered by any read of its code', () => {
    expect(covers(undefined, T)).toBe(true)
    expect(covers(undefined, { epoch: 'Z', seq: 0 })).toBe(true)
  })
})

describe('maxCursor — the larger of two captured cursors', () => {
  it('same epoch: the larger seq, whichever order', () => {
    expect(maxCursor({ epoch: 'E', seq: 3 }, { epoch: 'E', seq: 9 })).toEqual({ epoch: 'E', seq: 9 })
    expect(maxCursor({ epoch: 'E', seq: 9 }, { epoch: 'E', seq: 3 })).toEqual({ epoch: 'E', seq: 9 })
  })

  it('another epoch is incomparable: the later capture wins', () => {
    expect(maxCursor({ epoch: 'E1', seq: 99 }, { epoch: 'E2', seq: 1 })).toEqual({ epoch: 'E2', seq: 1 })
  })

  it('a captured cursor beats none (none would mean "the newest emitted", which can cover what was never seen)', () => {
    expect(maxCursor(undefined, { epoch: 'E', seq: 1 })).toEqual({ epoch: 'E', seq: 1 })
    expect(maxCursor({ epoch: 'E', seq: 1 }, undefined)).toEqual({ epoch: 'E', seq: 1 })
    expect(maxCursor(undefined, undefined)).toBeUndefined()
  })
})

describe('parseReadCursor — the wire `through`', () => {
  it('accepts {epoch, seq}', () => {
    expect(parseReadCursor({ epoch: 'E', seq: 4 })).toEqual({ epoch: 'E', seq: 4 })
  })

  it.each([null, undefined, 'x', 5, [], {}, { epoch: '', seq: 1 }, { epoch: 'E' }, { epoch: 'E', seq: -1 }, { epoch: 3, seq: 1 }])(
    'rejects %j', (bad) => {
      expect(parseReadCursor(bad)).toBeUndefined()
    },
  )

  it('drops unknown extra fields', () => {
    expect(parseReadCursor({ epoch: 'E', seq: 4, extra: 1 })).toEqual({ epoch: 'E', seq: 4 })
  })
})
