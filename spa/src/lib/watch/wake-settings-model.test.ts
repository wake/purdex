// The pure rules of editing the wake settings: walking a field's chain against the host (plan §4.4/§4.5), error paths and words, the times list, the alert-kind rows.
import { describe, it, expect } from 'vitest'
import en from '../../locales/en.json'
import zhTW from '../../locales/zh-TW.json'
import getFull from './__fixtures__/watch_wake_settings_get_full.json'
import { addTime, collapseChain, errorKeyOf, fieldIdOfPath, isAppMismatchCode, kindRows, toggleKind, walkChain, type Link } from './wake-settings-model'

const link = (base: unknown, value: unknown, extra: Partial<Link> = {}): Link => ({ base, value, phase: 'queued', ...extra })

describe('walkChain', () => {
  it('one link: kept when the host has its base, done when the host has its value, a conflict otherwise', () => {
    const l = link(30, 45)
    expect(walkChain(30, [l])).toEqual({ kept: [l], done: [], conflict: null })
    expect(walkChain(45, [l])).toEqual({ kept: [], done: [l], conflict: null })
    const c = walkChain(60, [l])
    expect(c.kept).toEqual([])
    expect(c.conflict).toEqual({ theirs: 60, mine: 45, links: [l] })
  })

  it('0→1 then 1→0 (plan 4.5): host 0 keeps both, host 1 keeps the second, host 5 is one conflict with mine = 0', () => {
    const a = link(0, 1, { phase: 'sent' })
    const b = link(1, 0)
    expect(walkChain(0, [a, b])).toEqual({ kept: [a, b], done: [], conflict: null })
    expect(walkChain(1, [a, b])).toEqual({ kept: [b], done: [a], conflict: null })
    const c = walkChain(5, [a, b])
    expect(c.kept).toEqual([])
    expect(c.conflict).toEqual({ theirs: 5, mine: 0, links: [a, b] })
  })

  it('a link after a kept one is compared with what the kept one expects, not with the host value', () => {
    // host 0; link 1 stands (expects host 0, makes it 1); link 2 expects 1 — true only because of link 1. Against the host value (0) it would be a conflict.
    expect(walkChain(0, [link(0, 1), link(1, 2)]).conflict).toBeNull()
  })

  it('once a link is a conflict, the links after it join the same conflict', () => {
    const a = link(10, 11)
    const b = link(11, 12)
    const c = link(12, 13)
    const w = walkChain(99, [a, b, c])
    expect(w.conflict?.links).toEqual([a, b, c])
    expect(w.conflict?.mine).toBe(13)
    const w2 = walkChain(11, [link(10, 11), link(5, 6), link(6, 7)])
    expect(w2.done).toHaveLength(1)
    expect(w2.conflict?.links).toHaveLength(2)
  })

  it('an array is compared whole, deeply', () => {
    const l = link([], ['09:00'])
    expect(walkChain([], [l]).kept).toEqual([l])
    expect(walkChain(['09:00'], [l]).done).toEqual([l])
    expect(walkChain(['10:00'], [l]).conflict).toEqual({ theirs: ['10:00'], mine: ['09:00'], links: [l] })
  })

  it('a switch never conflicts: the host has its base or its value', () => {
    for (const host of [true, false]) {
      expect(walkChain(host, [link(true, false)]).conflict).toBeNull()
      expect(walkChain(host, [link(false, true)]).conflict).toBeNull()
    }
  })
})

describe('collapseChain', () => {
  it('{0→1, 1→0} not in flight collapses to nothing; {0→1, 1→2} to {0→2}', () => {
    expect(collapseChain([link(0, 1), link(1, 0)])).toEqual([])
    expect(collapseChain([link(0, 1), link(1, 2)])).toEqual([link(0, 2)])
  })
  it('a chain with a link in flight is left as it is', () => {
    const chain = [link(0, 1, { phase: 'sent' }), link(1, 0)]
    expect(collapseChain(chain)).toBe(chain)
  })
  it('a chain whose first link is uncertain is left as it is: the host may already hold 1', () => {
    const chain = [link(0, 1, { phase: 'unsent', uncertain: true, retryable: true }), link(1, 0, { phase: 'unsent' })]
    expect(collapseChain(chain)).toBe(chain)
  })
})

describe('fieldIdOfPath', () => {
  it.each([
    ['enabled', 'enabled'],
    ['rules.patrol.max_gap_hours', 'patrol.max_gap_hours'],
    ['rules.patrol.interval_min.away', 'patrol.interval_min.away'],
    ['rules.patrol.times[0]', 'patrol.times'],
    ['rules.patrol.times', 'patrol.times'],
    ['rules.wwd_alert.kinds_off[3]', 'wwd_alert.kinds_off'],
    ['rules.wwd_alert.levels[1]', 'wwd_alert.levels'],
    ['rules.disk_low.prompt', 'disk_low.prompt'],
    ['rules.patrol.enabled', null],
    ['rules.nothing.here', null],
  ])('%s → %s', (path, id) => expect(fieldIdOfPath(path)).toBe(id))
})

describe('error words', () => {
  const cases: [string, string, string][] = [
    ['missing_field', 'rules.patrol', 'app_mismatch'],
    ['unknown_field', 'rules.patrol.enabled', 'app_mismatch'],
    ['bad_type', 'rules.patrol.times', 'app_mismatch'],
    ['bad_version', 'version', 'app_mismatch'],
    ['out_of_range', 'rules.patrol.interval_min.busy', 'interval'],
    ['out_of_range', 'rules.patrol.max_gap_hours', 'max_gap'],
    ['out_of_range', 'rules.disk_low.below_gb', 'below_gb'],
    ['out_of_range', 'rules.user_back.away_min', 'away_min'],
    ['out_of_range', 'rules.something.else', 'range'],
    ['bad_time', 'rules.patrol.times[0]', 'bad_time'],
    ['duplicate_time', 'rules.patrol.times[1]', 'duplicate_time'],
    ['too_many', 'rules.patrol.times', 'too_many_times'],
    ['too_many', 'rules.wwd_alert.kinds_off', 'too_many_kinds'],
    ['bad_level', 'rules.wwd_alert.levels[0]', 'bad_level'],
    ['bad_kind', 'rules.wwd_alert.kinds_off[2]', 'bad_kind'],
    ['prompt_too_long', 'rules.patrol.prompt', 'prompt_too_long'],
    ['prompt_bad_char', 'rules.disk_low.prompt', 'prompt_bad_char'],
    ['bad_tz', 'rules.patrol.tz', 'bad_tz'],
    ['brand_new_code', 'rules.patrol.tz', 'other'],
  ]
  it.each(cases)('%s at %s → watch.wake.err.%s, a key both languages have', (code, path, name) => {
    const key = errorKeyOf(code, path)
    expect(key).toBe(`watch.wake.err.${name}`)
    expect(Object.keys(en)).toContain(key)
    expect(Object.keys(zhTW)).toContain(key)
  })
  it('the keys the screens use on their own are in both languages', () => {
    expect(Object.keys(en)).toContain('watch.wake.err.not_int')
    expect(Object.keys(zhTW)).toContain('watch.wake.err.not_int')
  })
  it('only the four format codes mean the App and the host disagree', () => {
    expect(['missing_field', 'unknown_field', 'bad_type', 'bad_version'].every(isAppMismatchCode)).toBe(true)
    expect(['out_of_range', 'bad_time', 'prompt_too_long'].some(isAppMismatchCode)).toBe(false)
  })
})

describe('addTime', () => {
  it('adds in order', () => {
    expect(addTime(['10:00'], '09:00')).toEqual({ ok: true, times: ['09:00', '10:00'] })
    expect(addTime([], ' 23:59 ')).toEqual({ ok: true, times: ['23:59'] })
  })
  it.each(['9:00', '24:00', '09:60', 'abc', ''])('%j is refused as a bad time', (raw) => expect(addTime([], raw)).toEqual({ ok: false, error: 'bad_time' }))
  it('a repeat and a 25th time are refused here', () => {
    expect(addTime(['09:00'], '09:00')).toEqual({ ok: false, error: 'duplicate_time' })
    const full = Array.from({ length: 24 }, (_, i) => `${String(i).padStart(2, '0')}:00`)
    expect(addTime(full, '05:30')).toEqual({ ok: false, error: 'too_many_times' })
  })
})

describe('kindRows / toggleKind', () => {
  const known = getFull.known_kinds
  it('known kinds follow kinds_off; a value this App does not know stays as its own row', () => {
    const rows = kindRows(known, ['swap_growth', 'future_kind'], ['push', 'digest', 'watch'])
    expect(rows.find((r) => r.kind === 'swap_growth')).toMatchObject({ on: false, known: true })
    expect(rows.find((r) => r.kind === 'orphans')).toMatchObject({ on: true, label: '孤兒行程' })
    expect(rows.at(-1)).toMatchObject({ kind: 'future_kind', label: 'future_kind', known: false, on: false })
    expect(rows).toHaveLength(known.length + 1)
  })
  it('levelOff is true when none of a kind\'s levels is on; the kind stays on (nothing is locked)', () => {
    const rows = kindRows(known, [], ['watch'])
    expect(rows.find((r) => r.kind === 'orphans')).toMatchObject({ levelOff: true, on: true }) // digest, push both off
    expect(rows.find((r) => r.kind === 'swap_growth')?.levelOff).toBe(false)
    expect(kindRows(known, [], []).every((r) => r.levelOff)).toBe(true)
  })
  it('toggleKind gives the whole new array and keeps the values it does not know', () => {
    expect(toggleKind(['future_kind'], 'orphans', false)).toEqual(['future_kind', 'orphans'])
    expect(toggleKind(['future_kind', 'orphans'], 'orphans', true)).toEqual(['future_kind'])
    expect(toggleKind(['orphans'], 'orphans', false)).toEqual(['orphans'])
  })
})
