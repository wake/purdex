// spa/src/lib/relay/fresh.test.ts — the `relay.fresh` wire and `GET /api/relay/fresh` body (relay v2 contract 5).
import { describe, it, expect, vi, afterEach } from 'vitest'
import { parseRelayFreshBody, parseRelayFreshEntry, parseRelayFreshEvent } from './fresh'

const entry = (over: Record<string, unknown> = {}) => ({ session_id: 's1', ref: '_aaaaaa', op_id: 'op1', path: '/w/h.md', written_at: 1_700_000_000_000, ...over })
const value = (o: unknown) => JSON.stringify(o)

afterEach(() => vi.restoreAllMocks())

describe('parseRelayFreshEvent', () => {
  it('reads a snapshot and a changed; value is a JSON string', () => {
    expect(parseRelayFreshEvent(value({ op: 'snapshot', sessions: [entry()] }))).toEqual({ op: 'snapshot', sessions: [entry()] })
    expect(parseRelayFreshEvent(value({ op: 'changed', sessions: [entry(), entry({ session_id: 's2' })] }))).toMatchObject({ op: 'changed', sessions: [{ session_id: 's1' }, { session_id: 's2' }] })
  })

  it('accepts an empty list', () => {
    expect(parseRelayFreshEvent(value({ op: 'changed', sessions: [] }))).toEqual({ op: 'changed', sessions: [] })
  })

  it('keeps code only as a non-empty string', () => {
    const r = parseRelayFreshEvent(value({ op: 'snapshot', sessions: [entry({ code: 'c1' }), entry({ session_id: 's2', code: '' }), entry({ session_id: 's3', code: 7 }), entry({ session_id: 's4' })] }))
    if (typeof r === 'string') throw new Error(r)
    expect(r.sessions.map((s) => s.code)).toEqual(['c1', undefined, undefined, undefined])
    expect(r.sessions.map((s) => 'code' in s)).toEqual([true, false, false, false])
  })

  it('drops a bad entry alone, with a warning, and keeps the others', () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    const bad = [
      entry({ session_id: undefined }), entry({ ref: 3 }), entry({ op_id: null }), entry({ path: '' }), entry({ path: 9 }),
      entry({ written_at: '1700' }), entry({ written_at: Number.NaN }), entry({ written_at: undefined }), 'junk', null, [],
    ]
    const r = parseRelayFreshEvent(value({ op: 'changed', sessions: [entry({ session_id: 'ok1' }), ...bad, entry({ session_id: 'ok2' })] }))
    if (typeof r === 'string') throw new Error(r)
    expect(r.sessions.map((s) => s.session_id)).toEqual(['ok1', 'ok2'])
    expect(warn).toHaveBeenCalledTimes(1)
  })

  it.each([
    ['not JSON', '{nope'],
    ['a JSON string of a non-object', '"x"'],
    ['an array', '[]'],
    ['an unknown op', value({ op: 'delta', sessions: [] })],
    ['a missing op', value({ sessions: [] })],
    ['sessions not an array', value({ op: 'snapshot', sessions: {} })],
    ['sessions missing', value({ op: 'changed' })],
    ['null', 'null'],
  ])('a bad frame (%s) is a reason string, not a list', (_name, v) => {
    expect(typeof parseRelayFreshEvent(v)).toBe('string')
  })

  it('also reads an already parsed object', () => {
    expect(parseRelayFreshEvent({ op: 'snapshot', sessions: [entry()] })).toEqual({ op: 'snapshot', sessions: [entry()] })
  })
})

describe('parseRelayFreshBody', () => {
  it('reads {sessions: [...]}, dropping a bad entry alone', () => {
    vi.spyOn(console, 'warn').mockImplementation(() => {})
    expect(parseRelayFreshBody({ sessions: [entry(), { nope: 1 }] })).toEqual([entry()])
    expect(parseRelayFreshBody({ sessions: [] })).toEqual([])
  })

  it.each([[null], [[]], ['x'], [{}], [{ sessions: 'a' }]])('%j is not the envelope', (body) => {
    expect(parseRelayFreshBody(body)).toBeNull()
  })
})

describe('parseRelayFreshEntry', () => {
  it('drops unknown extra fields', () => {
    expect(parseRelayFreshEntry({ ...entry(), junk: 1 })).toEqual(entry())
  })
})
