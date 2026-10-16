// The four JSON samples in __fixtures__/ are copied byte for byte from the daemon's testdata
// (internal/module/fleet/testdata, F-1c2 #2982): when the daemon changes its wire format these go red.
import { describe, it, expect } from 'vitest'
import { parseFast, parseFleetAnswer, parseFleetPush } from './parse'
import { wireFast, fast, host } from './__fixtures__/builders'
import primaryJson from './__fixtures__/api-fleet-primary.json'
import standbyJson from './__fixtures__/api-fleet-standby.json'
import offJson from './__fixtures__/api-fleet-off.json'
import changedJson from './__fixtures__/fleet-changed.json'

const wireHost = (id: string, over: Record<string, unknown> = {}) => {
  const h = host(id)
  return { id, kind: 'daemon', alias: id, status: 'ok', fast: wireFast(h.fast!), ...over }
}

describe('the daemon samples', () => {
  it('api-fleet-primary: times become ms, the queue and the offline probe are read', () => {
    const a = parseFleetAnswer(primaryJson)
    if (a?.role !== 'primary') throw new Error('not primary')
    expect(a.term).toBe(3)
    expect(a.at).toBe(Date.parse('2026-10-16T12:00:00Z'))
    expect(a.hosts.map((h) => [h.id, h.kind, h.status])).toEqual([
      ['mlab:abc123', 'daemon', 'ok'], ['air26:def456', 'daemon', 'ok'], ['ab12cd34', 'probe', 'ok'], ['cd34ef56', 'probe', 'offline'],
    ])
    const [mlab, air, a19, mserv] = a.hosts
    expect(mlab.name).toBe('mlab') // a daemon host carries no name: its look comes from the HostConfig
    expect(mlab.fast).toMatchObject({ at: Date.parse('2026-10-16T12:00:00Z'), cpu: 12.5, zone: 'normal', load: 61 })
    expect(air.fast).toMatchObject({ level: 2, page_rate: 61, zone: 'warning' })
    expect(air.slow?.top_cpu[0]).toEqual({ pid: 91, name: 'cc1plus', cpu: 120, rss_mb: 400 })
    expect(air.slow?.queue).toEqual({
      heavy_used: 2, heavy_total: 2, light_used: 1, light_total: 4,
      holders: [
        { class: 'heavy', slot: 1, command: 'go test -race ./internal/module/fleet/', project: 'purdex', session: '9c2f7a10-1b3d-4e55-9a60-7f0e2c8d5b11', since: Date.parse('2026-10-16T11:58:28Z'), pid: 10 },
        { class: 'heavy', slot: 2, command: 'pnpm build', project: 'spa', session: '', since: Date.parse('2026-10-16T11:59:46Z'), pid: 11 },
        { class: 'light', slot: 1, command: 'git status', project: 'purdex', session: '', since: Date.parse('2026-10-16T11:59:58Z'), pid: 13 },
      ],
      waiters: [{ class: 'heavy', place: 1, command: 'go test ./...', project: 'purdex', session: '11111111-aaaa-4bbb-8ccc-222222222222', since: Date.parse('2026-10-16T11:57:30Z') }],
      longest_wait_s: 150, admission_held: true, admission_reason: 'memory pressure level 2', heavy_paused: false, light_paused: false,
    })
    expect(mlab.slow?.queue).toBeUndefined()
    expect(a19).toMatchObject({ name: 'air-2019', icon: 'Laptop', icon_weight: 'fill', os: 'Linux' })
    expect(a19.fast?.level).toBe(0)
    expect(mserv).toMatchObject({ status: 'offline', error: 'unreachable', fast: null, slow: null, last_ok_at: null })
  })

  it('api-fleet-standby and api-fleet-off', () => {
    expect(parseFleetAnswer(standbyJson)).toEqual({ role: 'standby', term: 4, primary: 'air26' })
    expect(parseFleetAnswer(offJson)).toEqual({ role: 'off', reason: 'own alias not in order' })
  })

  it('fleet-changed: every host has status, error, last_ok_at and fast; slow only where it changed; null fast is kept as null', () => {
    const p = parseFleetPush(changedJson)
    expect(p).toMatchObject({ term: 3, primary: 'mlab' })
    expect(p?.hosts.map((h) => h.id)).toEqual(['mlab:abc123', 'air26:def456', 'ab12cd34', 'cd34ef56'])
    const [mlab, air, , mserv] = p!.hosts
    expect(mlab).toMatchObject({ status: 'ok', error: null, last_ok_at: Date.parse('2026-10-16T12:00:01Z') })
    expect(mlab.fast?.cpu).toBe(14)
    expect(mlab.slow).toBeUndefined()
    expect(air.slow?.queue?.heavy_used).toBe(1)
    // an older pdx (no admission object): the daemon rebuilt it, with no reason; the light class is paused
    expect(air.slow?.queue).toMatchObject({ admission_held: true, admission_reason: '', heavy_paused: false, light_paused: true, light_total: 0 })
    expect(air.slow?.queue?.holders).toHaveLength(1)
    expect(mserv).toMatchObject({ status: 'offline', error: 'unreachable', last_ok_at: null, fast: null })
  })
})

describe('the queue, both shapes', () => {
  // the 744 daemon's queue: none of the W5b fields
  const old744 = {
    heavy_used: 1, heavy_total: 2, light_used: 0, light_total: 4, longest_wait_s: 30, admission_held: false,
    waiters: [{ class: 'heavy', place: 1, command: 'go test', project: 'purdex', since: '2026-10-16T11:59:30Z' }],
  }
  const slowOf = (queue: unknown) => ({ id: 'air26:def456', kind: 'daemon', alias: 'air26', status: 'ok', slow: { at: '2026-10-16T12:00:00Z', top_cpu: [], top_mem: [], queue } })
  const queueOf = (queue: unknown) => {
    const p = parseFleetPush({ term: 1, primary: 'air26', hosts: [slowOf(queue)] })
    return p?.hosts[0].slow?.queue
  }

  it('a 744 daemon still parses: holders [], reason "", not paused, the waiter has no session', () => {
    expect(queueOf(old744)).toEqual({
      heavy_used: 1, heavy_total: 2, light_used: 0, light_total: 4, holders: [], longest_wait_s: 30, admission_held: false,
      admission_reason: '', heavy_paused: false, light_paused: false,
      waiters: [{ class: 'heavy', place: 1, command: 'go test', project: 'purdex', session: '', since: Date.parse('2026-10-16T11:59:30Z') }],
    })
  })

  it('the new fields are read, and a holder that does not read is dropped alone', () => {
    const q = queueOf({
      ...old744, admission_held: true, admission_reason: 'disk low', heavy_paused: true, light_paused: false,
      holders: [
        { class: 'heavy', slot: 1, command: 'go test', project: 'purdex', session: 's1', since: '2026-10-16T11:58:00Z', pid: 7 },
        { class: 'heavy', slot: 'x', command: 'bad slot', since: '2026-10-16T11:58:00Z' },
        { class: 'light', slot: 2, command: 'no time' },
        { slot: 3, command: 'sparse', since: '2026-10-16T11:58:30Z' },
      ],
    })
    expect(q).toMatchObject({ admission_held: true, admission_reason: 'disk low', heavy_paused: true, light_paused: false })
    expect(q?.holders).toEqual([
      { class: 'heavy', slot: 1, command: 'go test', project: 'purdex', session: 's1', since: Date.parse('2026-10-16T11:58:00Z'), pid: 7 },
      { class: '', slot: 3, command: 'sparse', project: '', session: '', since: Date.parse('2026-10-16T11:58:30Z'), pid: 0 },
    ])
  })

  // a zero Go time.Time (a holder with no Started, a waiter with no Since) is "0001-01-01T00:00:00Z": parsed as ms it is about
  // -62e12, and the "已跑 / 等" counter would show a time of millions of years. It reads as unknown (null), the entry stays.
  it('a zero or pre-2000 time reads as null, the holder and the waiter are kept; a real time is kept as ms', () => {
    const q = queueOf({
      ...old744,
      holders: [
        { class: 'heavy', slot: 1, command: 'zero', since: '0001-01-01T00:00:00Z', pid: 1 },
        { class: 'heavy', slot: 2, command: 'old', since: '1999-12-31T23:59:59Z', pid: 2 },
        { class: 'heavy', slot: 3, command: 'edge', since: '2000-01-01T00:00:00Z', pid: 3 },
        { class: 'heavy', slot: 4, command: 'real', since: '2026-10-16T11:58:00Z', pid: 4 },
      ],
      waiters: [
        { class: 'heavy', place: 1, command: 'zero', project: 'p', since: '0001-01-01T00:00:00Z' },
        { class: 'heavy', place: 2, command: 'real', project: 'p', since: '2026-10-16T11:59:30Z' },
      ],
    })
    expect(q?.holders.map((h) => [h.command, h.since])).toEqual([
      ['zero', null], ['old', null], ['edge', Date.UTC(2000, 0, 1)], ['real', Date.parse('2026-10-16T11:58:00Z')],
    ])
    expect(q?.waiters.map((w) => [w.command, w.since])).toEqual([['zero', null], ['real', Date.parse('2026-10-16T11:59:30Z')]])
  })

  it('holders that are not a list, and flags that are not true, read as the defaults', () => {
    const q = queueOf({ ...old744, holders: 'nope', admission_reason: 7, heavy_paused: 'yes', light_paused: 1 })
    expect(q).toMatchObject({ holders: [], admission_reason: '', heavy_paused: false, light_paused: false })
  })
})

describe('parseFleetAnswer', () => {
  it('refuses an envelope it cannot read', () => {
    expect(parseFleetAnswer(null)).toBeNull()
    expect(parseFleetAnswer({ role: 'boss' })).toBeNull()
    expect(parseFleetAnswer({ role: 'primary', term: 'x', hosts: [] })).toBeNull()
    expect(parseFleetAnswer({ role: 'standby', term: 1 })).toBeNull()
  })

  it('drops a host row without an id or kind, and nulls an unreadable fast part instead of inventing numbers', () => {
    const a = parseFleetAnswer({
      role: 'primary', term: 1, at: '2026-10-16T12:00:00Z',
      hosts: [wireHost('a'), wireHost('b', { id: '' }), wireHost('c', { kind: 'cloud' }), wireHost('d', { fast: { ...wireFast(fast()), cpu: 'high' } })],
    })
    if (a?.role !== 'primary') throw new Error('x')
    expect(a.hosts.map((h) => h.id)).toEqual(['a', 'd'])
    expect(a.hosts[1].fast).toBeNull()
  })

  it('a time that is not RFC3339 (an epoch number) makes the reading unreadable', () => {
    const a = parseFleetAnswer({ role: 'primary', term: 1, at: 5, hosts: [wireHost('a', { fast: { ...wireFast(fast()), at: 1_000_000 } })] })
    if (a?.role !== 'primary') throw new Error('x')
    expect(a.hosts[0].fast).toBeNull()
  })

  it('reads an unknown error code as parse, and an unknown status as an error, never ok', () => {
    const a = parseFleetAnswer({ role: 'primary', term: 1, at: '2026-10-16T12:00:00Z', hosts: [
      wireHost('a', { status: 'error', error: 'brand_new' }), wireHost('b', { status: 'weird' }), wireHost('c', { status: 'offline', error: 'timeout' }),
    ] })
    if (a?.role !== 'primary') throw new Error('x')
    expect(a.hosts[0]).toMatchObject({ status: 'error', error: 'parse' })
    expect(a.hosts[1]).toMatchObject({ status: 'error', error: 'parse' })
    expect(a.hosts[2]).toMatchObject({ status: 'offline', error: 'timeout' })
  })
})

describe('negative numbers (F-2a1 review)', () => {
  it.each(['cpu', 'mem', 'disk', 'load', 'level'])('a negative %s makes the reading unreadable', (k) => {
    expect(parseFast({ ...wireFast(fast()), [k]: -1 })).toBeNull()
  })
  it('a negative page_rate (a counter difference) is read as 0, not a lost reading', () => {
    expect(parseFast({ ...wireFast(fast()), page_rate: -7 })).toMatchObject({ page_rate: 0, cpu: 20 })
    expect(parseFast({ ...wireFast(fast()), page_rate: 'x' })).toBeNull()
  })
  it.each(['disk_free_gb', 'load1', 'ncpu'])('a negative %s is treated like an invalid optional value (0)', (k) => {
    expect(parseFast({ ...wireFast(fast()), [k]: -3 })).toMatchObject({ [k]: 0 })
  })
})

describe('parseFleetPush', () => {
  it('a malformed fast makes the row fast null, a missing one stays absent', () => {
    const p = parseFleetPush({ term: 1, primary: 'a', hosts: [{ id: 'a', fast: { cpu: 'x' } }, { id: 'b' }] })
    expect(p?.hosts[0].fast).toBeNull()
    expect('fast' in p!.hosts[1]).toBe(false)
  })

  it('drops a row without an id and refuses a payload without a term', () => {
    const p = parseFleetPush({ term: 4, primary: 'air26', hosts: [{ id: 'a', status: 'ok', error: null, last_ok_at: null, fast: wireFast(fast({ cpu: 9 })) }, { status: 'ok' }] })
    expect(p?.hosts.map((h) => h.id)).toEqual(['a'])
    expect(p?.hosts[0].fast?.cpu).toBe(9)
    expect(parseFleetPush({ primary: 'a', hosts: [] })).toBeNull()
    expect(parseFleetPush('x')).toBeNull()
  })
})
