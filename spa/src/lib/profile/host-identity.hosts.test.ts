import { describe, expect, it } from 'vitest'
import type { HostConfig } from '../../stores/useHostStore'
import type { HostsPayload } from './types'
import {
  hostsFromWire,
  hostsToWire,
  identityOfSync,
  makeWireResolver,
  MAX_HOST_ALIASES,
  mergeAliases,
  NEW_HOST,
  syncIdOfSync,
  type WireHostsPayload,
} from './host-identity'

const MLAB = 'mini-lab:278cbm'
const MLAB_WIRE = syncIdOfSync(MLAB)
const AIR = 'air-2026:k2k2k2'
const AIR_WIRE = syncIdOfSync(AIR)

function host(id: string, extra: Partial<HostConfig> = {}): HostConfig {
  return { id, name: `n-${id}`, ip: '100.64.0.2', port: 7860, order: 0, ...extra }
}

function payload(...list: HostConfig[]): HostsPayload {
  return { hosts: Object.fromEntries(list.map((h) => [h.id, h])), hostOrder: list.map((h) => h.id) }
}

describe('mergeAliases (spec §11.2)', () => {
  it('appends new aliases after the existing ones, each once', () => {
    expect(mergeAliases(['a1', 'b1'], ['b1', 'c1', 'c1'])).toEqual(['a1', 'b1', 'c1'])
  })

  it(`keeps at most ${MAX_HOST_ALIASES}, dropping the oldest`, () => {
    const existing = Array.from({ length: MAX_HOST_ALIASES }, (_, i) => `old${i}`)
    const merged = mergeAliases(existing, ['new1', 'new2'])
    expect(merged).toHaveLength(MAX_HOST_ALIASES)
    expect(merged[0]).toBe('old2')
    expect(merged.slice(-2)).toEqual(['new1', 'new2'])
    expect(MAX_HOST_ALIASES).toBe(16)
  })

  it('drops non-strings, empty strings and sync ids (an alias is a legacy local-id key)', () => {
    expect(mergeAliases(['a1', 3, '', null], ['d1_x', 'd2_y', 'b1', {}])).toEqual(['a1', 'b1'])
  })

  it('treats a non-array `existing` as empty (wire data)', () => {
    expect(mergeAliases('a1', ['b1'])).toEqual(['b1'])
    expect(mergeAliases(undefined, [])).toEqual([])
  })

  it('does not mutate its inputs', () => {
    const existing = ['a1']
    const added = ['b1']
    mergeAliases(existing, added)
    expect(existing).toEqual(['a1'])
    expect(added).toEqual(['b1'])
  })
})

describe('hostsToWire / hostsFromWire', () => {
  const local = payload(host('loc001', { daemonId: MLAB }), host('loc002'), host('loc003', { daemonId: AIR }))
  const identity = identityOfSync(local.hosts)

  it('re-keys records, `.id` and hostOrder to wire ids', () => {
    const wire = hostsToWire(local, identity)
    expect(Object.keys(wire.hosts)).toEqual([MLAB_WIRE, 'loc002', AIR_WIRE])
    expect(wire.hosts[MLAB_WIRE].id).toBe(MLAB_WIRE)
    expect(wire.hosts[MLAB_WIRE].name).toBe('n-loc001')
    expect(wire.hosts.loc002.id).toBe('loc002')
    expect(wire.hostOrder).toEqual([MLAB_WIRE, 'loc002', AIR_WIRE])
  })

  it('does not mutate its input', () => {
    const snapshot = structuredClone(local)
    hostsToWire(local, identity)
    expect(local).toEqual(snapshot)
  })

  it('passes an id the identity does not know through unchanged', () => {
    const wire = hostsToWire({ hosts: { zzz999: host('zzz999') }, hostOrder: ['zzz999', 'ghost1'] }, identity)
    expect(Object.keys(wire.hosts)).toEqual(['zzz999'])
    expect(wire.hostOrder).toEqual(['zzz999', 'ghost1'])
  })

  it('puts aliases on canonical rows only, merged and capped', () => {
    const aliasesOf = (localId: string) => (localId === 'loc002' ? ['x'] : ['frgn01', 'frgn01', 'd1_no'])
    const wire = hostsToWire(local, identity, aliasesOf)
    expect(wire.hosts[MLAB_WIRE].aliases).toEqual(['frgn01'])
    expect(wire.hosts.loc002).not.toHaveProperty('aliases')
  })

  it('ignores an aliasesOf result that is not an array (e.g. a string is not iterated as characters)', () => {
    const wire = hostsToWire(local, identity, () => 'abc' as unknown as string[])
    expect(wire.hosts[MLAB_WIRE]).not.toHaveProperty('aliases')
  })

  it('omits an empty aliases list', () => {
    const wire = hostsToWire(local, identity, () => [])
    expect(wire.hosts[MLAB_WIRE]).not.toHaveProperty('aliases')
  })

  it('round-trips: fromWire(toWire(x)) equals x', () => {
    const resolve = makeWireResolver({ identity })
    expect(hostsFromWire(hostsToWire(local, identity), resolve)).toEqual({ hosts: local, aliasesByLocal: {} })
  })

  it('fromWire keeps `aliases` out of the payload and hands them back per local id', () => {
    const resolve = makeWireResolver({ identity })
    const back = hostsFromWire(hostsToWire(local, identity, (id) => (id === 'loc001' ? ['frgn01'] : undefined)), resolve)
    expect(back.hosts).toEqual(local)
    expect(back.aliasesByLocal).toEqual({ loc001: ['frgn01'] })
  })

  it('fromWire sanitises aliases like mergeAliases and omits an empty list', () => {
    const resolve = makeWireResolver({ identity })
    const wire = {
      hosts: {
        [MLAB_WIRE]: { ...host(MLAB_WIRE, { daemonId: MLAB }), aliases: ['a1', 'a1', 'd1_x', 3, ''] },
        [AIR_WIRE]: { ...host(AIR_WIRE, { daemonId: AIR }), aliases: 'nope' },
        loc002: { ...host('loc002'), aliases: [] },
      },
      hostOrder: [MLAB_WIRE, AIR_WIRE, 'loc002'],
    } as unknown as WireHostsPayload
    const back = hostsFromWire(wire, resolve)
    expect(back.aliasesByLocal).toEqual({ loc001: ['a1'] })
    for (const row of Object.values(back.hosts)) expect(row).not.toHaveProperty('aliases')
  })

  it('round-trips aliases: aliasesOf fed from aliasesByLocal reproduces the same wire rows', () => {
    const resolve = makeWireResolver({ identity })
    const wire = hostsToWire(local, identity, (id) => (id === 'loc003' ? ['frgn01', 'frgn02'] : undefined))
    const back = hostsFromWire(wire, resolve)
    expect(hostsToWire(back.hosts, identity, (id) => back.aliasesByLocal[id])).toEqual(wire)
  })

  it('fromWire is total on garbage', () => {
    const resolve = makeWireResolver({ identity })
    expect(hostsFromWire({ hosts: { a: null, b: 7 }, hostOrder: [1, 'a'] } as unknown as WireHostsPayload, resolve)).toEqual({
      hosts: { hosts: { a: null, b: 7 }, hostOrder: [1, 'a'] },
      aliasesByLocal: {},
    })
  })
})

describe('makeWireResolver (spec §11.2)', () => {
  const identity = identityOfSync({ loc001: { id: 'loc001', daemonId: MLAB }, loc002: { id: 'loc002' } })

  it('sync id → local through the identity', () => {
    expect(makeWireResolver({ identity })(MLAB_WIRE)).toBe('loc001')
  })

  it('an unknown sync id (any version) stays unchanged', () => {
    const r = makeWireResolver({ identity })
    expect(r(AIR_WIRE)).toBe(AIR_WIRE)
    expect(r('d2_zzz')).toBe('d2_zzz')
  })

  it('a legacy id that is an alias of a canonical row → that row\'s daemon → local', () => {
    const rows = { [MLAB_WIRE]: { id: MLAB_WIRE, daemonId: MLAB, aliases: ['frgn01'] } }
    expect(makeWireResolver({ identity, rows })('frgn01')).toBe('loc001')
  })

  it('an alias row keyed by an unknown sync id still resolves through its daemonId', () => {
    const renamedIdentity = identityOfSync({ loc001: { id: 'loc001', daemonId: MLAB } }, { hash: () => 'd1_other' })
    const rows = { [MLAB_WIRE]: { daemonId: MLAB, aliases: ['frgn01'] } }
    expect(makeWireResolver({ identity: renamedIdentity, rows, hash: () => 'd1_other' })('frgn01')).toBe('loc001')
  })

  it('an alias whose row maps to no local host stays unchanged', () => {
    const rows = { [AIR_WIRE]: { daemonId: AIR, aliases: ['frgn01'] } }
    expect(makeWireResolver({ identity, rows })('frgn01')).toBe('frgn01')
  })

  it('an alias claimed by rows resolving to different local hosts is ambiguous → unchanged', () => {
    const two = identityOfSync({ loc001: { id: 'loc001', daemonId: MLAB }, loc003: { id: 'loc003', daemonId: AIR } })
    const rows = {
      [MLAB_WIRE]: { daemonId: MLAB, aliases: ['frgn01'] },
      [AIR_WIRE]: { daemonId: AIR, aliases: ['frgn01'] },
    }
    expect(makeWireResolver({ identity: two, rows })('frgn01')).toBe('frgn01')
  })

  it('aliases are only consulted for legacy ids, never for sync ids', () => {
    const rows = { [MLAB_WIRE]: { daemonId: MLAB, aliases: [AIR_WIRE] } }
    expect(makeWireResolver({ identity, rows })(AIR_WIRE)).toBe(AIR_WIRE)
  })

  it('a row key this apply matched → the local host it matched (legacy row from another device)', () => {
    const matched = new Map([['frgn02', 'loc009']])
    expect(makeWireResolver({ identity, matched })('frgn02')).toBe('loc009')
  })

  it('a NEW_HOST entry in `matched` is not a local id', () => {
    const matched = new Map([['frgn02', NEW_HOST]])
    expect(makeWireResolver({ identity, matched })('frgn02')).toBe('frgn02')
  })

  it('an exact legacy row created as a NEW host resolves through its daemonId (no aliases, matched says NEW_HOST)', () => {
    const rows = { frgn01: { id: 'frgn01', daemonId: MLAB } }
    const matched = new Map([['frgn01', NEW_HOST]])
    const postApply = identityOfSync({ loc999: { id: 'loc999', daemonId: MLAB } })
    expect(makeWireResolver({ identity: postApply, rows, matched })('frgn01')).toBe('loc999')
    expect(makeWireResolver({ identity: postApply, rows })('frgn01')).toBe('loc999')
  })

  it('an exact legacy row with an invalid or unknown daemonId stays unchanged', () => {
    const postApply = identityOfSync({ loc999: { id: 'loc999', daemonId: MLAB } })
    expect(makeWireResolver({ identity: postApply, rows: { frgn01: { daemonId: '' } } })('frgn01')).toBe('frgn01')
    expect(makeWireResolver({ identity: postApply, rows: { frgn01: { daemonId: AIR } } })('frgn01')).toBe('frgn01')
  })

  it('an exact legacy row\'s daemonId wins over an alias naming the same key', () => {
    const two = identityOfSync({ loc001: { id: 'loc001', daemonId: MLAB }, loc003: { id: 'loc003', daemonId: AIR } })
    const rows = { frgn01: { daemonId: AIR }, [MLAB_WIRE]: { daemonId: MLAB, aliases: ['frgn01'] } }
    expect(makeWireResolver({ identity: two, rows })('frgn01')).toBe('loc003')
  })

  it('an exact row match wins over an alias', () => {
    const rows = { [MLAB_WIRE]: { daemonId: MLAB, aliases: ['frgn02'] } }
    const matched = new Map([['frgn02', 'loc009']])
    expect(makeWireResolver({ identity, rows, matched })('frgn02')).toBe('loc009')
  })

  it('a legacy id nobody knows stays unchanged (this device\'s own legacy id included)', () => {
    const r = makeWireResolver({ identity })
    expect(r('loc002')).toBe('loc002')
    expect(r('nobody')).toBe('nobody')
  })

  it('is total on garbage rows', () => {
    const rows = { a: null, b: { aliases: 'frgn01' }, c: { aliases: [5] } } as Record<string, unknown>
    expect(makeWireResolver({ identity, rows })('frgn01')).toBe('frgn01')
  })
})
