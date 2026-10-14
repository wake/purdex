import { describe, it, expect } from 'vitest'
import { parseFleetPayload, parseProbeHostDef, parseVersionedProbeHosts } from './parse'

const NUMS = { cpu: 210, mem: 72, disk: 64, load1: 2.1, ncpu: 10, zone: 'normal', trend: 'rising', sampled_at: 1760000000 }
const PROBE = {
  id: 'p1', name: 'mserv', ssh_alias: 'mserv', icon: 'Desktop', colors: { console: { main: { color: '#112233', alpha: 100 } } },
  icon_weight: 'bold', os: 'Linux', status: 'ok', ...NUMS, last_ok_at: 1760000000, error: '',
}

describe('parseFleetPayload', () => {
  it('reads self and a probe row', () => {
    const p = parseFleetPayload({ self: NUMS, probes: [PROBE] })!
    expect(p.self).toMatchObject({ cpu: 210, zone: 'normal', trend: 'rising' })
    expect(p.probes[0]).toMatchObject({ id: 'p1', name: 'mserv', status: 'ok', error: null, icon_weight: 'bold', last_ok_at: 1760000000 })
    expect(p.probes[0].colors?.console?.main.color).toBe('#112233')
  })

  it.each(['ok', 'offline', 'error'])('keeps status %s', (status) => {
    expect(parseFleetPayload({ self: NUMS, probes: [{ ...PROBE, status }] })!.probes[0].status).toBe(status)
  })

  it.each(['timeout', 'unreachable', 'host_key', 'auth', 'remote_exit', 'parse', 'alias_gone'])('keeps error code %s', (error) => {
    expect(parseFleetPayload({ self: NUMS, probes: [{ ...PROBE, status: 'error', error }] })!.probes[0].error).toBe(error)
  })

  it('maps an unknown error code to parse and an unknown status to an error row', () => {
    const p = parseFleetPayload({ self: NUMS, probes: [{ ...PROBE, status: 'error', error: 'Permission denied (user@host)' }, { ...PROBE, id: 'p2', status: 'weird' }] })!
    expect(p.probes[0].error).toBe('parse')
    expect(p.probes[1]).toMatchObject({ status: 'error', error: 'parse' })
  })

  it('turns unknown zone / trend values and non-numbers into unknown / null', () => {
    const p = parseFleetPayload({ self: { ...NUMS, zone: 'purple', trend: 7, cpu: 'x' }, probes: [] })!
    expect(p.self).toMatchObject({ zone: 'unknown', trend: 'unknown', cpu: null })
  })

  it('drops a probe row without an id or name, and rejects a body that is not the shape', () => {
    expect(parseFleetPayload({ self: NUMS, probes: [{ ...PROBE, id: '' }, { ...PROBE, name: 3 }] })!.probes).toEqual([])
    expect(parseFleetPayload(null)).toBeNull()
    expect(parseFleetPayload({ self: NUMS })).toBeNull()
    expect(parseFleetPayload('x')).toBeNull()
  })

  it('self may be absent (null)', () => {
    expect(parseFleetPayload({ self: null, probes: [] })).toEqual({ self: null, probes: [] })
  })
})

describe('probe host definitions', () => {
  it('reads a row and drops invalid look fields', () => {
    const d = parseProbeHostDef({ id: 'a', name: 'n', ssh_alias: 's', icon: 'Desktop', icon_weight: 'nope', colors: { console: { main: { color: 'red', alpha: 5 } } } })!
    expect(d).toEqual({ id: 'a', name: 'n', ssh_alias: 's', icon: 'Desktop' })
  })
  it('reads the versioned collection and refuses a malformed one', () => {
    expect(parseVersionedProbeHosts({ items: [{ id: 'a', name: 'n', ssh_alias: 's' }, { bad: 1 }], revision: 3 })).toEqual({ items: [{ id: 'a', name: 'n', ssh_alias: 's' }], revision: 3 })
    expect(parseVersionedProbeHosts({ items: 'x', revision: 1 })).toBeNull()
    expect(parseVersionedProbeHosts({ items: [], revision: 'x' })).toBeNull()
  })
})
