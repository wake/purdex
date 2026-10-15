import { describe, it, expect } from 'vitest'
import { parseFleetAnswer, parseFleetPush } from './parse'
import { fast, host } from './fixtures'

describe('parseFleetAnswer', () => {
  it('reads a primary answer with hosts, fast, slow and a queue', () => {
    const wire = {
      role: 'primary', term: 3, at: 5,
      hosts: [{
        ...host('d1'), kind: 'daemon',
        slow: { at: 9, top_cpu: [{ pid: 1, name: 'node', cpu: 80, rss_mb: 300 }, { pid: 'x' }], top_mem: [],
          queue: { heavy: { used: 1, total: 2 }, light: { used: 0, total: 4 }, waiters: [{ place: 1, command: 'vitest', project: 'p', since: 7 }], longest_wait_ms: 90000, admission_held: true } },
      }],
    }
    const a = parseFleetAnswer(wire)
    expect(a?.role).toBe('primary')
    if (a?.role !== 'primary') throw new Error('x')
    expect(a.term).toBe(3)
    expect(a.hosts[0].slow?.top_cpu).toHaveLength(1) // the malformed process row is dropped
    expect(a.hosts[0].slow?.queue?.admission_held).toBe(true)
    expect(a.hosts[0].slow?.queue?.waiters[0].command).toBe('vitest')
  })

  it('reads standby and off answers', () => {
    expect(parseFleetAnswer({ role: 'standby', term: 2, primary: 'air26' })).toEqual({ role: 'standby', term: 2, primary: 'air26' })
    expect(parseFleetAnswer({ role: 'off', reason: 'no alias' })).toEqual({ role: 'off', reason: 'no alias' })
  })

  it('refuses an envelope it cannot read', () => {
    expect(parseFleetAnswer(null)).toBeNull()
    expect(parseFleetAnswer({ role: 'boss' })).toBeNull()
    expect(parseFleetAnswer({ role: 'primary', term: 'x', hosts: [] })).toBeNull()
    expect(parseFleetAnswer({ role: 'standby', term: 1 })).toBeNull()
  })

  it('drops a host row without an id or kind, and nulls an unreadable fast part instead of inventing numbers', () => {
    const a = parseFleetAnswer({
      role: 'primary', term: 1, at: 1,
      hosts: [{ ...host('a') }, { ...host('b'), id: '' }, { ...host('c'), kind: 'cloud' }, { ...host('d'), fast: { ...fast(), cpu: 'high' } }],
    })
    if (a?.role !== 'primary') throw new Error('x')
    expect(a.hosts.map((h) => h.id)).toEqual(['a', 'd'])
    expect(a.hosts[1].fast).toBeNull()
  })

  it('reads an unknown error code as parse, and an unknown status as an error, never ok', () => {
    const a = parseFleetAnswer({ role: 'primary', term: 1, at: 1, hosts: [
      { ...host('a'), status: 'error', error: 'brand_new' }, { ...host('b'), status: 'weird' }, { ...host('c'), status: 'offline', error: 'timeout' },
    ] })
    if (a?.role !== 'primary') throw new Error('x')
    expect(a.hosts[0]).toMatchObject({ status: 'error', error: 'parse' })
    expect(a.hosts[1]).toMatchObject({ status: 'error', error: 'parse' })
    expect(a.hosts[2]).toMatchObject({ status: 'offline', error: 'timeout' })
  })
})

describe('parseFleetPush', () => {
  it('reads term, primary and per-host parts', () => {
    const p = parseFleetPush({ term: 4, primary: 'air26', hosts: [{ id: 'a', fast: fast({ cpu: 9 }) }, { id: 'b', fast: fast(), status: 'offline', error: 'timeout' }, { fast: fast() }] })
    expect(p?.term).toBe(4)
    expect(p?.hosts.map((h) => h.id)).toEqual(['a', 'b'])
    expect(p?.hosts[0].fast?.cpu).toBe(9)
    expect(p?.hosts[0].slow).toBeUndefined()
    expect(p?.hosts[1]).toMatchObject({ status: 'offline', error: 'timeout' })
  })
  it('refuses a payload without a term', () => {
    expect(parseFleetPush({ primary: 'a', hosts: [] })).toBeNull()
    expect(parseFleetPush('x')).toBeNull()
  })
})
