// The four JSON samples in __fixtures__/ are copied byte for byte from the daemon's testdata
// (internal/module/fleet/testdata, F-1c2 #2982): when the daemon changes its wire format these go red.
import { describe, it, expect } from 'vitest'
import { parseFleetAnswer, parseFleetPush } from './parse'
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
      heavy_used: 2, heavy_total: 2, light_used: 0, light_total: 4,
      waiters: [{ class: 'heavy', place: 1, command: 'go test ./...', project: 'purdex', since: Date.parse('2026-10-16T11:57:30Z') }],
      longest_wait_s: 150, admission_held: false,
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
    expect(mserv).toMatchObject({ status: 'offline', error: 'unreachable', last_ok_at: null, fast: null })
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

describe('parseFleetPush', () => {
  it('drops a row without an id and refuses a payload without a term', () => {
    const p = parseFleetPush({ term: 4, primary: 'air26', hosts: [{ id: 'a', status: 'ok', error: null, last_ok_at: null, fast: wireFast(fast({ cpu: 9 })) }, { status: 'ok' }] })
    expect(p?.hosts.map((h) => h.id)).toEqual(['a'])
    expect(p?.hosts[0].fast?.cpu).toBe(9)
    expect(parseFleetPush({ primary: 'a', hosts: [] })).toBeNull()
    expect(parseFleetPush('x')).toBeNull()
  })
})
