import { describe, expect, it } from 'vitest'
import type { PaneLayout } from '../../types/tab'
import type { HostsPayload } from './types'
import {
  hostSettingsFromWire,
  hostSettingsToWire,
  hostsFromWire,
  hostsToWire,
  identityOfSync,
  layoutFromWire,
  makeWireResolver,
  matchIncomingHosts,
  NEW_HOST,
  presetColumnsFromWire,
  syncIdOfSync,
  type WireHostsPayload,
} from './host-identity'

const MLAB = 'mini-lab:278cbm'
const MLAB_WIRE = syncIdOfSync(MLAB)

describe('a legacy row from another device that this device creates as a NEW host', () => {
  it('tabs / settings / presets naming its foreign key resolve to the created local id (via its daemonId alone)', () => {
    // No local host; the incoming legacy row carries daemonId, no aliases.
    const rows = { frgn01: { id: 'frgn01', name: 'mlab', ip: '100.64.0.2', port: 7860, order: 0, daemonId: MLAB } }
    const match = matchIncomingHosts({}, rows)
    expect(match.byRow.get('frgn01')).toBe(NEW_HOST)
    // The hosts apply creates loc999 for it; `matched` is passed as-is (still NEW_HOST).
    const postApply = identityOfSync({ loc999: { id: 'loc999', daemonId: MLAB } })
    const resolve = makeWireResolver({ identity: postApply, rows, matched: match.byRow })

    const layout: PaneLayout = {
      type: 'leaf',
      pane: { id: 'p', content: { kind: 'tmux-session', hostId: 'frgn01', sessionCode: 's', mode: 'terminal', cachedName: 'n', tmuxInstance: '1' } },
    }
    const back = layoutFromWire(layout, resolve)
    expect(back.type === 'leaf' && back.pane.content).toMatchObject({ hostId: 'loc999' })
    expect(hostSettingsFromWire({ frgn01: { m: 1 } }, resolve)).toEqual({ loc999: { m: 1 } })
    expect(presetColumnsFromWire({ '1col': { enabled: true, columns: [['sessions:frgn01', 'headless:frgn01']] } }, resolve)).toEqual({
      '1col': { enabled: true, columns: [['sessions:loc999', 'headless:loc999']] },
    })
  })
})

describe('prototype-named wire keys (__proto__, constructor, prototype)', () => {
  const NAMES = ['__proto__', 'constructor', 'prototype'] as const

  function wireHosts(): WireHostsPayload {
    // JSON.parse creates `__proto__` as an OWN property, exactly as wire data arrives.
    return JSON.parse(
      JSON.stringify({
        hosts: {
          __proto__PLACEHOLDER: { id: '__proto__', name: 'a', ip: 'x', port: 1, order: 0 },
          constructor: { id: 'constructor', name: 'b', ip: 'x', port: 1, order: 1 },
          prototype: { id: 'prototype', name: 'c', ip: 'x', port: 1, order: 2 },
          [MLAB_WIRE]: { id: MLAB_WIRE, name: 'm', ip: 'x', port: 1, order: 3, daemonId: MLAB, aliases: ['__proto__'] },
        },
        hostOrder: ['__proto__', 'constructor', 'prototype', MLAB_WIRE],
      }).replace('__proto__PLACEHOLDER', '__proto__'),
    )
  }

  function expectOwnKeys(record: object, keys: readonly string[]) {
    expect(Object.getPrototypeOf(record)).toBe(Object.prototype)
    for (const k of keys) expect(Object.hasOwn(record, k), k).toBe(true)
    expect(Object.keys(record).sort()).toEqual([...keys].sort())
  }

  it('hostsFromWire keeps them as own entries; hostOrder stays consistent', () => {
    const identity = identityOfSync({})
    const wire = wireHosts()
    const { hosts, aliasesByLocal } = hostsFromWire(wire, makeWireResolver({ identity }))
    expectOwnKeys(hosts.hosts, [...NAMES, MLAB_WIRE])
    expect(hosts.hosts['__proto__' as string].name).toBe('a')
    expect(hosts.hostOrder).toEqual(['__proto__', 'constructor', 'prototype', MLAB_WIRE])
    for (const id of hosts.hostOrder) expect(Object.hasOwn(hosts.hosts, id), id).toBe(true)
    expectOwnKeys(aliasesByLocal, [MLAB_WIRE])
    expect(JSON.parse(JSON.stringify(hosts.hosts))).toEqual(JSON.parse(JSON.stringify(wire.hosts, (k, v) => (k === 'aliases' ? undefined : v))))
  })

  it('hosts round-trip: toWire(fromWire(x)) equals x', () => {
    const identity = identityOfSync({})
    const wire = wireHosts()
    const back = hostsFromWire(wire, makeWireResolver({ identity }))
    const again = hostsToWire(back.hosts, identity, (id) => back.aliasesByLocal[id])
    expectOwnKeys(again.hosts, [...NAMES, MLAB_WIRE])
    expect(JSON.stringify(again)).toBe(JSON.stringify(wire))
  })

  it('an unresolved local id named __proto__ in aliasesByLocal is an own entry', () => {
    const wire = JSON.parse('{"hosts":{"__proto__":{"id":"__proto__","aliases":["frgn01"]}},"hostOrder":["__proto__"]}') as WireHostsPayload
    // Not a sync id, so `aliases` on it are still handed back under its (unchanged) key.
    const { aliasesByLocal } = hostsFromWire(wire, makeWireResolver({ identity: identityOfSync({}) }))
    expectOwnKeys(aliasesByLocal, ['__proto__'])
    expect(aliasesByLocal['__proto__' as string]).toEqual(['frgn01'])
  })

  it('hostsToWire keeps a local __proto__ key as an own entry', () => {
    const local = JSON.parse('{"hosts":{"__proto__":{"id":"__proto__","name":"a","ip":"x","port":1,"order":0}},"hostOrder":["__proto__"]}') as HostsPayload
    const wire = hostsToWire(local, identityOfSync(local.hosts))
    expectOwnKeys(wire.hosts, ['__proto__'])
    expect(wire.hostOrder).toEqual(['__proto__'])
  })

  it('host-settings round-trip both ways with prototype-named keys', () => {
    const identity = identityOfSync({})
    const resolve = makeWireResolver({ identity })
    const record = JSON.parse('{"__proto__":{"a":1},"constructor":{"b":2},"prototype":{"c":3}}') as Record<string, unknown>
    const wire = hostSettingsToWire(record, identity)
    expectOwnKeys(wire, NAMES)
    const back = hostSettingsFromWire(wire, resolve)
    expectOwnKeys(back, NAMES)
    expect(JSON.stringify(back)).toBe(JSON.stringify(record))
  })

  it('a non-record value under __proto__ is kept as an own entry too (hosts both ways, presets)', () => {
    const identity = identityOfSync({})
    const wire = JSON.parse('{"hosts":{"__proto__":5},"hostOrder":[]}') as WireHostsPayload
    expectOwnKeys(hostsFromWire(wire, makeWireResolver({ identity })).hosts.hosts, ['__proto__'])
    const local = JSON.parse('{"hosts":{"__proto__":null},"hostOrder":[]}') as HostsPayload
    expectOwnKeys(hostsToWire(local, identity).hosts, ['__proto__'])
    const presets = JSON.parse('{"__proto__":null}') as Record<string, unknown>
    expectOwnKeys(presetColumnsFromWire(presets, makeWireResolver({ identity })), ['__proto__'])
  })

  it('presets with a __proto__ key keep it as an own entry', () => {
    const presets = JSON.parse('{"__proto__":{"enabled":true,"columns":[["sessions:x"]]}}') as Record<string, unknown>
    const out = presetColumnsFromWire(presets, makeWireResolver({ identity: identityOfSync({}) }))
    expectOwnKeys(out, ['__proto__'])
  })
})
