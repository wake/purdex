import { describe, it, expect } from 'vitest'
import {
  ANCHOR_END, anchorOrdinal, findCurrent, findMatches, firstAtOrAfter, matchIdentity, relocate, SEARCH_MATCH_LIMIT,
  type SearchUnit,
} from './transcript-search'

/** The matches alone (the limit is far away in these tests). */
const find = (list: SearchUnit[], query: string) => findMatches(list, query).matches
/** One unit holding `text`. */
const one = (text: string, id = 'u'): SearchUnit[] => [{ id, text: text.normalize('NFC'), reveal: [] }]

describe('findMatches', () => {
  it('is case insensitive and literal', () => {
    const list = one('Find A.B and axb and a.b')
    expect(find(list, 'a.b').map((m) => m.start)).toEqual([5, 21])
    expect(find(list, 'AXB').map((m) => m.start)).toEqual([13])
  })

  it('lists the fold keys that reveal a match, and numbers the unit it is in', () => {
    const list: SearchUnit[] = [{ id: 'k:0', text: 'nothing', reveal: [] }, { id: 'k:1', text: 'a needle here', reveal: ['k:1:fold'] }]
    expect(find(list, 'needle')).toEqual([{ unitId: 'k:1', start: 2, end: 8, ordinal: 0, reveal: ['k:1:fold'] }])
  })

  it('ignores a one-letter query', () => {
    expect(find(one('a a a'), 'a')).toEqual([])
    expect(find(one('a a a'), '')).toEqual([])
  })

  // A10: a single Han character narrows a transcript as well as two Latin letters.
  it('a single Han character searches', () => {
    const list: SearchUnit[] = [{ id: 'x', text: '有錯誤，檔案沒找到', reveal: [] }, { id: 'y', text: '一切正常', reveal: [] }]
    expect(find(list, '錯').map((m) => [m.unitId, m.start])).toEqual([['x', 1]])
    // Kana and Hangul count as CJK too.
    expect(find(one('ファイル 파일'), 'フ')).toHaveLength(1)
    expect(find(one('ファイル 파일'), '파')).toHaveLength(1)
  })

  // A F8: Bopomofo, and the long-vowel mark (full and half width), are CJK too.
  it('a single Bopomofo letter or long-vowel mark searches', () => {
    expect(find(one('注音 ㄅㄆㄇ'), 'ㄅ')).toHaveLength(1)
    expect(find(one('コーヒー'), 'ー')).toHaveLength(2)
    expect(find(one('ｺｰﾋｰ'), 'ｰ')).toHaveLength(2)
  })

  it('a single Latin letter does not', () => {
    const list = one('a b c')
    expect(find(list, 'a')).toEqual([])
    // Padding does not make it long enough: the query is trimmed first.
    expect(find(list, ' a ')).toEqual([])
    expect(find(list, 'é')).toEqual([])
  })

  it('whitespace-only searches nothing', () => {
    const list = one('a  b\t\tc')
    expect(find(list, '  ')).toEqual([])
    expect(find(list, '\t\t')).toEqual([])
    expect(find(list, '')).toEqual([])
    // Around real text the spaces are trimmed, not searched.
    expect(find(one('xx yy'), '  yy ').map((m) => m.start)).toEqual([3])
  })

  it('NFD input matches NFC text', () => {
    const nfc = 'caf\u00e9'
    const nfd = 'cafe\u0301'
    expect(nfc).not.toBe(nfd)
    // An NFD query over NFC text…
    expect(find(one(`a ${nfc} b`), nfd).map((m) => [m.start, m.end])).toEqual([[2, 6]])
    // …and an NFC query over NFD text: the unit text is NFC at index time, so the offsets are offsets into that NFC text.
    expect(find(one(`a ${nfd} b`), nfc).map((m) => [m.start, m.end])).toEqual([[2, 6]])
  })

  it('keeps list order', () => {
    const list: SearchUnit[] = [
      { id: 'a', text: 'xx one', reveal: [] }, { id: 'b', text: 'xx two xx', reveal: ['b:fold'] }, { id: 'c', text: 'xx three', reveal: [] },
    ]
    expect(find(list, 'xx').map((m) => [m.unitId, m.start])).toEqual([['a', 0], ['b', 0], ['b', 7], ['c', 0]])
  })

  it('finds every occurrence inside one unit', () => {
    expect(find(one('abab ab'), 'ab').map((m) => [m.start, m.end])).toEqual([[0, 2], [2, 4], [5, 7]])
  })

  it('stops at the limit and says so', () => {
    const list: SearchUnit[] = [{ id: 'a', text: 'ab '.repeat(6), reveal: [] }, { id: 'b', text: 'ab', reveal: [] }]
    expect(findMatches(list, 'ab', 4)).toMatchObject({ truncated: true })
    expect(findMatches(list, 'ab', 4).matches).toHaveLength(4)
    // Exactly at the limit is not truncated; the default is 10,000.
    expect(findMatches(list, 'ab', 7)).toMatchObject({ truncated: false })
    expect(findMatches(list, 'ab', 7).matches).toHaveLength(7)
    expect(SEARCH_MATCH_LIMIT).toBe(10_000)
    const many: SearchUnit[] = [{ id: 'm', text: 'ab'.repeat(10_001), reveal: [] }]
    expect(findMatches(many, 'ab')).toMatchObject({ truncated: true })
    expect(findMatches(many, 'ab').matches).toHaveLength(10_000)
  })

  it('numbers each match within its unit', () => {
    const list: SearchUnit[] = [{ id: 'a', text: 'ab ab', reveal: [] }, { id: 'b', text: 'ab', reveal: [] }]
    expect(findMatches(list, 'ab').matches.map((m) => [m.unitId, m.ordinal])).toEqual([['a', 0], ['a', 1], ['b', 0]])
  })

  // User decision (2026-09-27): past the limit, the matches kept are the ones
  // around where the reader is (the anchor unit), not the oldest.
  describe('anchored', () => {
    // Ten units, two matches each: u0#0 u0#1 u1#0 … u9#1.
    const ten: SearchUnit[] = Array.from({ length: 10 }, (_, i) => ({ id: `u${i}`, text: 'ab ab', reveal: [] }))
    const ids = (r: ReturnType<typeof findMatches>) => r.matches.map((m) => `${m.unitId}#${m.ordinal}`)

    it('keeps half the limit before the anchor and the rest from it on', () => {
      const r = findMatches(ten, 'ab', 6, 5)
      expect(ids(r)).toEqual(['u3#1', 'u4#0', 'u4#1', 'u5#0', 'u5#1', 'u6#0'])
      expect(r).toMatchObject({ truncated: true, truncatedBefore: true, truncatedAfter: true })
    })

    it('near the end, gives the unused half to the matches before it', () => {
      const r = findMatches(ten, 'ab', 6, 9)
      expect(ids(r)).toEqual(['u7#0', 'u7#1', 'u8#0', 'u8#1', 'u9#0', 'u9#1'])
      expect(r).toMatchObject({ truncated: true, truncatedBefore: true, truncatedAfter: false })
      // Past the last unit: the newest `limit`.
      expect(ids(findMatches(ten, 'ab', 6, 10))).toEqual(ids(r))
    })

    it('at the start it is the first `limit`, and nothing is cut when all fit', () => {
      expect(ids(findMatches(ten, 'ab', 6, 0))).toEqual(['u0#0', 'u0#1', 'u1#0', 'u1#1', 'u2#0', 'u2#1'])
      expect(findMatches(ten, 'ab', 6, 0)).toMatchObject({ truncatedBefore: false, truncatedAfter: true })
      expect(findMatches(ten, 'ab', 20, 5)).toMatchObject({ truncated: false, truncatedBefore: false, truncatedAfter: false })
      expect(findMatches(ten, 'ab', 20, 5).matches).toHaveLength(20)
    })

    // PR #1495 re-review item 3: the window can be centred on a match inside
    // a unit — its earlier matches then count as before the anchor.
    it('an anchor ordinal splits its unit: earlier matches are before it', () => {
      expect(ids(findMatches(ten, 'ab', 6, 5, 1))).toEqual(['u4#0', 'u4#1', 'u5#0', 'u5#1', 'u6#0', 'u6#1'])
      // Past the unit's last match: the whole unit is before it, as if anchored on the next unit.
      expect(ids(findMatches(ten, 'ab', 6, 5, 9))).toEqual(ids(findMatches(ten, 'ab', 6, 6)))
      expect(ids(findMatches(ten, 'ab', 6, 5, 0))).toEqual(ids(findMatches(ten, 'ab', 6, 5)))
    })

    it('one unit with twice the limit: centred on a match deep inside it, the window moves on', () => {
      const one: SearchUnit[] = [{ id: 'big', text: 'ab '.repeat(2 * SEARCH_MATCH_LIMIT), reveal: [] }]
      // Centred on the unit, the window is its first `limit` matches…
      const start = findMatches(one, 'ab', SEARCH_MATCH_LIMIT, 0)
      expect(start.matches.at(-1)!.ordinal).toBe(SEARCH_MATCH_LIMIT - 1)
      // …and centred on that last one, it holds the ones after it.
      const on = findMatches(one, 'ab', SEARCH_MATCH_LIMIT, 0, SEARCH_MATCH_LIMIT - 1)
      expect(on.matches).toHaveLength(SEARCH_MATCH_LIMIT)
      expect(on.matches[0].ordinal).toBe(SEARCH_MATCH_LIMIT / 2 - 1)
      expect(on.matches.at(-1)!.ordinal).toBe(SEARCH_MATCH_LIMIT * 1.5 - 2)
      expect(on).toMatchObject({ truncatedBefore: true, truncatedAfter: true })
    })

    it('anchorOrdinal holds while its unit is there, else 0', () => {
      const list: SearchUnit[] = [{ id: 'a', text: 'ab', reveal: [] }, { id: 'b', text: 'ab ab', reveal: [] }]
      expect(anchorOrdinal(list, { unitId: 'b', unitPos: 1, ordinal: 1 })).toBe(1)
      expect(anchorOrdinal(list, { unitId: 'b', unitPos: 1 })).toBe(0)
      expect(anchorOrdinal(list, { unitId: 'gone', unitPos: 1, ordinal: 1 })).toBe(0)
      expect(anchorOrdinal(list, ANCHOR_END)).toBe(0)
    })

    it('firstAtOrAfter is the first match in the anchor unit or after it', () => {
      const list: SearchUnit[] = [{ id: 'a', text: 'ab', reveal: [] }, { id: 'b', text: 'x', reveal: [] }, { id: 'c', text: 'ab', reveal: [] }]
      const { matches } = findMatches(list, 'ab')
      expect(firstAtOrAfter(list, matches, 0)).toBe(0)
      expect(firstAtOrAfter(list, matches, 1)).toBe(1)
      expect(firstAtOrAfter(list, matches, 3)).toBe(-1)
    })

    it('a window starting inside a unit keeps the true ordinals', () => {
      const r = findMatches(ten, 'ab', 6, 5)
      // The window opens on u3's second match: its identity says so.
      expect(matchIdentity(ten, r.matches, 0)).toEqual({ unitId: 'u3', ordinal: 1, unitPos: 3 })
      const whole = findMatches(ten, 'ab', 100).matches
      expect(whole[findCurrent(ten, whole, matchIdentity(ten, r.matches, 0))]).toMatchObject({ unitId: 'u3', ordinal: 1 })
    })
  })
})

// A5: the current match is remembered as (unit, ordinal within the unit), not
// as a position in the list, which shifts whenever a match lands before it.
describe('matchIdentity / findCurrent', () => {
  const U = (id: string, text: string): SearchUnit => ({ id, text, reveal: [] })

  it('keeps the current match by identity when others land before it', () => {
    const before = [U('a', 'ab'), U('c', 'ab ab')]
    const { matches: m1 } = findMatches(before, 'ab')
    const id = matchIdentity(before, m1, 2) // the second `ab` of c
    expect(id).toEqual({ unitId: 'c', ordinal: 1, unitPos: 1 })
    const after = [U('a', 'ab'), U('b', 'ab ab'), U('c', 'ab ab')]
    const { matches: m2 } = findMatches(after, 'ab')
    expect(findCurrent(after, m2, id)).toBe(4)
    // Two units landing before it push it past its old position.
    const more = [U('a', 'ab'), U('b', 'ab'), U('b2', 'ab'), U('c', 'ab ab')]
    expect(findCurrent(more, findMatches(more, 'ab').matches, id)).toBe(4)
  })

  it('falls to the nearest following match when it is gone, else the last', () => {
    const units1 = [U('a', 'ab'), U('b', 'ab ab'), U('c', 'ab')]
    const { matches } = findMatches(units1, 'ab')
    const id = matchIdentity(units1, matches, 2) // b's second
    // b now holds one occurrence: the next one after it is c's.
    const units2 = [U('a', 'ab'), U('b', 'ab'), U('c', 'ab')]
    expect(findCurrent(units2, findMatches(units2, 'ab').matches, id)).toBe(2)
    // Nothing follows it: the last.
    const units3 = [U('a', 'ab'), U('b', 'ab'), U('c', 'x')]
    expect(findCurrent(units3, findMatches(units3, 'ab').matches, id)).toBe(1)
  })

  // R1-4: the unit that moves up into the gone unit's position follows it.
  it('a gone unit hands over to the unit now in its place', () => {
    const units1 = [U('a', 'ab'), U('b', 'ab'), U('c', 'ab'), U('d', 'ab')]
    const id = matchIdentity(units1, findMatches(units1, 'ab').matches, 1) // b's
    const units2 = [U('a', 'ab'), U('c', 'ab'), U('d', 'ab')]
    const m2 = findMatches(units2, 'ab').matches
    expect(m2[findCurrent(units2, m2, id)].unitId).toBe('c')
  })

  // R1-1: room ⇄ chat. The room's thinking has no chat unit; the next unit
  // chat draws takes over, at chat's position for it.
  it('relocate re-seats onto another list, handing a missing unit to its successor', () => {
    const room = [U('t0', 'x'), U('a', 'ab'), U('t1', 'ab'), U('b', 'ab')]
    const chat = [U('a', 'ab'), U('b', 'ab')]
    const id = { unitId: 't1', ordinal: 0, unitPos: 2 }
    const moved = relocate(room, chat, id)
    expect(moved).toEqual({ unitId: 't1', ordinal: 0, unitPos: 1 })
    const m = findMatches(chat, 'ab').matches
    expect(m[findCurrent(chat, m, moved)].unitId).toBe('b')
    // Re-seated again on a copy that also lacks it: the stored place stands.
    expect(relocate(chat, [...chat], moved)).toBe(moved)
    // Back in the room it is found by id; nothing follows it → the end.
    expect(relocate(chat, room, moved).unitPos).toBe(2)
    expect(relocate(room, [U('a', 'ab')], { unitId: 'b', ordinal: 0, unitPos: 3 }).unitPos).toBe(1)
    // A bare position is left alone.
    expect(relocate(room, chat, ANCHOR_END)).toBe(ANCHOR_END)
  })

  it('starts at the first match and is -1 with none', () => {
    const units = [U('a', 'ab')]
    expect(findCurrent(units, findMatches(units, 'ab').matches, null)).toBe(0)
    expect(findCurrent(units, [], null)).toBe(-1)
    expect(matchIdentity(units, [], 0)).toBeNull()
  })
})

// R4 T3.2: the dock's "inspect" reveals the call that started a task the way
// search reveals a match — same unit ids, same reveal keys.
