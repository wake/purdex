import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import type { TransferChange } from '../lib/host-transfer-plan'
import {
  useHostStore,
  selectDevHostId,
  findHostByEndpoint,
  selectDaemonIdMismatch,
  selectDaemonIdVerified,
  requestAtOf,
  applyTransferLooks,
  lookKeyOf,
  transferLookEntries,
} from './useHostStore'
import { useHostLookStore, type HostLookEntry } from './useHostLookStore'
import { useShownHostsStore } from './useShownHostsStore'
import { hostLookOf } from '../lib/host-look'
import { syncIdOfSync } from '../lib/profile/host-identity'

const NO_ENTRY: HostLookEntry = Object.freeze({}) as HostLookEntry
/** The look store's entry under `key`, `{}` when absent. */
const lookEntry = (key: string): HostLookEntry => useHostLookStore.getState().looks[key] ?? NO_ENTRY
const hasEntry = (key: string) => Object.hasOwn(useHostLookStore.getState().looks, key)

describe('useHostStore', () => {
  beforeEach(() => {
    useHostStore.getState().reset()
  })

  it('has a default host on init', () => {
    const state = useHostStore.getState()
    const hostIds = Object.keys(state.hosts)
    expect(hostIds).toHaveLength(1)

    const defaultId = hostIds[0]
    const host = state.hosts[defaultId]
    expect(host.name).toBe('mlab')
    expect(host.ip).toBe('100.64.0.2')
    expect(host.port).toBe(7860)
    expect(host.order).toBe(0)
    expect(state.activeHostId).toBe(defaultId)
    expect(state.hostOrder).toEqual([defaultId])
  })

  it('addHost creates a new host and returns its id', () => {
    const state = useHostStore.getState()
    const newId = state.addHost({ name: 'remote', ip: '10.0.0.1', port: 8080 })

    const updated = useHostStore.getState()
    expect(updated.hosts[newId]).toBeDefined()
    expect(updated.hosts[newId].name).toBe('remote')
    expect(updated.hosts[newId].ip).toBe('10.0.0.1')
    expect(updated.hosts[newId].port).toBe(8080)
    expect(updated.hostOrder).toContain(newId)
    expect(updated.hosts[newId].order).toBe(1)
  })

  it('removeHost deletes a host', () => {
    const state = useHostStore.getState()
    const newId = state.addHost({ name: 'temp', ip: '10.0.0.2', port: 9090 })

    useHostStore.getState().removeHost(newId)

    const updated = useHostStore.getState()
    expect(updated.hosts[newId]).toBeUndefined()
    expect(updated.hostOrder).not.toContain(newId)
  })

  it('cannot remove the last host', () => {
    const state = useHostStore.getState()
    const defaultId = state.activeHostId!
    state.removeHost(defaultId)

    const updated = useHostStore.getState()
    expect(updated.hosts[defaultId]).toBeDefined()
    expect(Object.keys(updated.hosts)).toHaveLength(1)
  })

  it('updateHost modifies an existing host', () => {
    const state = useHostStore.getState()
    const defaultId = state.activeHostId!
    state.updateHost(defaultId, { name: 'renamed', ip: '192.168.1.1', port: 8080 })

    const updated = useHostStore.getState()
    expect(updated.hosts[defaultId].name).toBe('renamed')
    expect(updated.hosts[defaultId].ip).toBe('192.168.1.1')
    expect(updated.hosts[defaultId].port).toBe(8080)
  })

  // H2c-2: every look write lands in the look store (the default host has no daemonId → its key is its local id);
  // `HostConfig` is only the seed of an absent entry.
  describe('host colors (spec 2026-09-18 §4.1)', () => {
    const id = () => useHostStore.getState().activeHostId!
    const host = () => lookEntry(id())
    const config = () => useHostStore.getState().hosts[id()]

    it('setHostColor writes colors.console.main with alpha 100 and no legacy color key', () => {
      useHostStore.getState().setHostColor(id(), '#3b82f6')
      expect(host().colors).toEqual({ console: { main: { color: '#3b82f6', alpha: 100 } } })
      expect('color' in host()).toBe(false)
    })

    it('setHostColor keeps the existing console main alpha and other layers', () => {
      useHostStore.getState().setHostColorLayer(id(), 'console', 'main', { color: '#3b82f6', alpha: 80 })
      useHostStore.getState().setHostColorLayer(id(), 'console', 'light', { alpha: 30 })
      useHostStore.getState().setHostColor(id(), '#ef4444')
      expect(host().colors?.console).toEqual({ main: { color: '#ef4444', alpha: 80 }, light: { alpha: 30 } })
    })

    it('setHostColor(null) clears the console set only', () => {
      useHostStore.getState().setHostColorLayer(id(), 'console', 'main', { color: '#3b82f6', alpha: 100 })
      useHostStore.getState().setHostColorLayer(id(), 'terminal', 'main', { color: '#ef4444', alpha: 100 })
      useHostStore.getState().setHostColor(id(), null)
      expect(host().colors).toEqual({ terminal: { main: { color: '#ef4444', alpha: 100 } } })
    })

    it('any colors write deletes a legacy color key', () => {
      useHostStore.setState((s) => ({ hosts: { ...s.hosts, [id()]: { ...s.hosts[id()], color: '#22c55e' } } }))
      useHostStore.getState().setHostColorLayer(id(), 'terminal', 'main', { color: '#ef4444', alpha: 100 })
      expect('color' in host()).toBe(false)
      expect(host().colors?.terminal?.main).toEqual({ color: '#ef4444', alpha: 100 })
    })

    it('setHostColorLayer main creates the mode set; middle/light on a missing set are no-ops', () => {
      useHostStore.getState().setHostColorLayer(id(), 'terminal', 'middle', { alpha: 50 })
      expect(host().colors).toBeUndefined()
      useHostStore.getState().setHostColorLayer(id(), 'terminal', 'main', { color: '#ef4444', alpha: 100 })
      useHostStore.getState().setHostColorLayer(id(), 'terminal', 'middle', { alpha: 50 })
      expect(host().colors?.terminal).toEqual({ main: { color: '#ef4444', alpha: 100 }, middle: { alpha: 50 } })
    })

    it('setHostColorLayer clamps a finite alpha and lowercases a valid color', () => {
      useHostStore.getState().setHostColorLayer(id(), 'console', 'main', { color: '#3B82F6', alpha: 250.4 })
      expect(host().colors?.console?.main).toEqual({ color: '#3b82f6', alpha: 100 })
      useHostStore.getState().setHostColorLayer(id(), 'console', 'light', { color: '#000000', alpha: -3 })
      expect(host().colors?.console?.light).toEqual({ color: '#000000', alpha: 0 })
    })

    it.each([
      ['main without color', 'main', { alpha: 100 }],
      ['color not #rrggbb', 'main', { color: 'red', alpha: 100 }],
      ['color without hash (no normalize in the store)', 'main', { color: '3b82f6', alpha: 100 }],
      ['color with padding', 'main', { color: ' #3b82f6 ', alpha: 100 }],
      ['non-string color', 'main', { color: 42, alpha: 100 }],
      ['missing alpha', 'main', { color: '#3b82f6' }],
      ['string alpha', 'main', { color: '#3b82f6', alpha: '50' }],
      ['NaN alpha', 'main', { color: '#3b82f6', alpha: NaN }],
      ['Infinity alpha', 'main', { color: '#3b82f6', alpha: Infinity }],
      ['non-object value', 'main', 'x'],
    ] as const)('setHostColorLayer is a no-op and never throws on %s', (_label, layer, value) => {
      expect(() => useHostStore.getState().setHostColorLayer(id(), 'console', layer, value as never)).not.toThrow()
      expect(host().colors).toBeUndefined()
    })

    it('setHostColorLayer rejects an unknown mode and an unknown layer', () => {
      useHostStore.getState().setHostColorLayer(id(), 'shell' as never, 'main', { color: '#3b82f6', alpha: 100 })
      useHostStore.getState().setHostColorLayer(id(), 'console', 'glow' as never, { color: '#3b82f6', alpha: 100 })
      expect(host().colors).toBeUndefined()
    })

    it('a rejected write leaves a legacy color untouched (no entry written)', () => {
      useHostStore.setState((s) => ({ hosts: { ...s.hosts, [id()]: { ...s.hosts[id()], color: '#22c55e' } } }))
      useHostStore.getState().setHostColorLayer(id(), 'console', 'main', { color: 'red', alpha: 100 })
      useHostStore.getState().setHostColorLayer(id(), 'console', 'middle', { alpha: 50 })   // no set yet → no-op
      expect(hasEntry(id())).toBe(false)
      expect(hostLookOf(id()).color).toBe('#22c55e')
    })

    it('setHostColor(null) on a legacy-only host removes the legacy color (the "No color" button)', () => {
      useHostStore.setState((s) => ({ hosts: { ...s.hosts, [id()]: { ...s.hosts[id()], color: '#22c55e' } } }))
      useHostStore.getState().setHostColor(id(), null)
      expect('color' in host()).toBe(false)
      expect('colors' in host()).toBe(false)
    })

    it('clearHostColorMode on a non-console mode that has no set is a no-op (legacy color kept)', () => {
      useHostStore.setState((s) => ({ hosts: { ...s.hosts, [id()]: { ...s.hosts[id()], color: '#22c55e' } } }))
      useHostStore.getState().clearHostColorMode(id(), 'terminal')
      expect(hasEntry(id())).toBe(false)
      expect(hostLookOf(id()).color).toBe('#22c55e')
    })

    it('setHostColorLayer(null) on middle/light removes the layer; on main clears the mode', () => {
      useHostStore.getState().setHostColorLayer(id(), 'console', 'main', { color: '#3b82f6', alpha: 100 })
      useHostStore.getState().setHostColorLayer(id(), 'console', 'light', { alpha: 5 })
      useHostStore.getState().setHostColorLayer(id(), 'console', 'light', null)
      expect(host().colors?.console).toEqual({ main: { color: '#3b82f6', alpha: 100 } })
      useHostStore.getState().setHostColorLayer(id(), 'console', 'main', null)
      expect(host().colors).toBeUndefined()
    })

    it('clearHostColorMode removes the set and drops colors when empty; unknown host is a no-op', () => {
      useHostStore.getState().setHostColorLayer(id(), 'console', 'main', { color: '#3b82f6', alpha: 100 })
      useHostStore.getState().clearHostColorMode('nope', 'console')
      expect(host().colors?.console).toBeDefined()
      useHostStore.getState().clearHostColorMode(id(), 'console')
      expect('colors' in host()).toBe(false)
    })

    it('setHostColorLayer(null) on an already-absent middle/light layer is a no-op (F1)', () => {
      useHostStore.setState((s) => ({
        hosts: {
          ...s.hosts,
          [id()]: { ...s.hosts[id()], color: '#22c55e', colors: { console: { main: { color: '#3b82f6', alpha: 100 } } } },
        },
      }))
      const before = useHostLookStore.getState().looks
      useHostStore.getState().setHostColorLayer(id(), 'console', 'middle', null)
      expect(useHostLookStore.getState().looks).toBe(before)
      expect(hasEntry(id())).toBe(false)
      expect(hostLookOf(id()).color).toBe('#22c55e')
      expect(hostLookOf(id()).colors).toEqual({ console: { main: { color: '#3b82f6', alpha: 100 } } })
    })

    it('clearHostColorMode(console) on a legacy host with only other mode sets removes the legacy color and keeps the other sets', () => {
      useHostStore.setState((s) => ({
        hosts: {
          ...s.hosts,
          [id()]: { ...s.hosts[id()], color: '#22c55e', colors: { terminal: { main: { color: '#ef4444', alpha: 100 } } } },
        },
      }))
      useHostStore.getState().clearHostColorMode(id(), 'console')
      expect('color' in host()).toBe(false)
      expect(host().colors).toEqual({ terminal: { main: { color: '#ef4444', alpha: 100 } } })
      expect(config().color).toBe('#22c55e') // HostConfig is never written
    })
  })

  it('setHostIcon stores an icon name', () => {
    const id = useHostStore.getState().activeHostId!
    useHostStore.getState().setHostIcon(id, 'Laptop')
    const host = lookEntry(id)
    expect(host.icon).toBe('Laptop')
    expect('iconWeight' in host).toBe(false)
  })

  it('setHostIcon stores an icon name with a weight', () => {
    const id = useHostStore.getState().activeHostId!
    useHostStore.getState().setHostIcon(id, 'Laptop', 'duotone')
    const host = lookEntry(id)
    expect(host.icon).toBe('Laptop')
    expect(host.iconWeight).toBe('duotone')
  })

  it('setHostIcon ignores an invalid weight but still stores the icon', () => {
    const id = useHostStore.getState().activeHostId!
    useHostStore.getState().setHostIcon(id, 'Laptop', 'evil' as never)
    const host = lookEntry(id)
    expect(host.icon).toBe('Laptop')
    expect('iconWeight' in host).toBe(false)
  })

  it('setHostIcon(null) removes both the icon and iconWeight keys', () => {
    const id = useHostStore.getState().activeHostId!
    useHostStore.getState().setHostIcon(id, 'Laptop', 'fill')
    useHostStore.getState().setHostIcon(id, null)
    const host = lookEntry(id)
    expect('icon' in host).toBe(false)
    expect('iconWeight' in host).toBe(false)
    expect(host.name).toBe('mlab')
  })

  it.each(['', '   '])('setHostIcon(%j) is treated as null', (blank) => {
    const id = useHostStore.getState().activeHostId!
    useHostStore.getState().setHostIcon(id, 'Laptop', 'fill')
    useHostStore.getState().setHostIcon(id, blank)
    const host = lookEntry(id)
    expect('icon' in host).toBe(false)
    expect('iconWeight' in host).toBe(false)
  })

  it.each([
    'a'.repeat(200),
    'laptop',
    ' Laptop ',
    'NotARealPhosphorIcon',
    'Laptop; background:url(x)',
  ])('setHostIcon(%j) is rejected and stores nothing', (bad) => {
    const id = useHostStore.getState().activeHostId!
    const before = useHostLookStore.getState().looks
    useHostStore.getState().setHostIcon(id, bad, 'fill')
    expect(useHostLookStore.getState().looks).toBe(before)
    expect(hasEntry(id)).toBe(false)
  })

  it('setHostIcon with a non-Phosphor name leaves an existing icon untouched', () => {
    const id = useHostStore.getState().activeHostId!
    useHostStore.getState().setHostIcon(id, 'Laptop', 'fill')
    useHostStore.getState().setHostIcon(id, 'NotARealPhosphorIcon', 'bold')
    const host = lookEntry(id)
    expect(host.icon).toBe('Laptop')
    expect(host.iconWeight).toBe('fill')
  })

  it('setHostIcon on an unknown host is a no-op', () => {
    const before = useHostStore.getState().hosts
    const looksBefore = useHostLookStore.getState().looks
    useHostStore.getState().setHostIcon('nope', 'Laptop')
    expect(useHostStore.getState().hosts).toBe(before)
    expect(useHostLookStore.getState().looks).toBe(looksBefore)
  })

  it('setHostIcon leaves the host color untouched', () => {
    const id = useHostStore.getState().activeHostId!
    useHostStore.getState().setHostColor(id, '#3b82f6')
    useHostStore.getState().setHostIcon(id, 'Laptop', 'bold')
    const host = lookEntry(id)
    expect(host.colors?.console?.main.color).toBe('#3b82f6')
    expect(host.icon).toBe('Laptop')
  })

  it('setHostIcon replacing an icon without a weight keeps the previous weight', () => {
    const id = useHostStore.getState().activeHostId!
    useHostStore.getState().setHostIcon(id, 'Laptop', 'fill')
    useHostStore.getState().setHostIcon(id, 'Desktop')
    const host = lookEntry(id)
    expect(host.icon).toBe('Desktop')
    expect(host.iconWeight).toBe('fill')
  })

  it('updateHost leaves an existing color untouched', () => {
    const id = useHostStore.getState().activeHostId!
    useHostStore.getState().setHostColor(id, '#ec4899')
    useHostStore.getState().updateHost(id, { name: 'renamed' })
    expect(useHostStore.getState().hosts[id].name).toBe('renamed')
    expect(lookEntry(id).colors?.console?.main.color).toBe('#ec4899')
  })

  describe('look writers → the look store (H2c-2 T2)', () => {
    const DAEMON = 'mini-lab:278cbm'
    const WIRE = syncIdOfSync(DAEMON)
    let hid: string

    beforeEach(() => {
      hid = useHostStore.getState().addHost({ name: 'A', ip: '10.0.0.9', port: 7860 })
      useHostStore.setState((s) => ({
        hosts: {
          ...s.hosts,
          [hid]: { ...s.hosts[hid], daemonId: DAEMON, color: '#22c55e', icon: 'Laptop', iconWeight: 'duotone' },
        },
      }))
    })

    it('every writer lands in looks[wireIdOfHost(host)] and leaves hosts the same object', () => {
      const hosts = useHostStore.getState().hosts
      const s = useHostStore.getState()
      s.setHostColor(hid, '#3b82f6')
      s.setHostColorLayer(hid, 'terminal', 'main', { color: '#ef4444', alpha: 50 })
      s.clearHostColorMode(hid, 'terminal')
      s.setHostIcon(hid, 'Cloud', 'bold')
      s.setHostName(hid, 'Renamed')
      expect(useHostStore.getState().hosts).toBe(hosts)
      expect(Object.keys(useHostLookStore.getState().looks)).toEqual([WIRE])
      expect(lookEntry(WIRE)).toEqual({
        name: 'Renamed',
        colors: { console: { main: { color: '#3b82f6', alpha: 100 } } },
        icon: 'Cloud',
        iconWeight: 'bold',
      })
    })

    it('the first write seeds the entry from HostConfig: set the icon → the old colour and name are kept', () => {
      useHostStore.getState().setHostIcon(hid, 'Cloud')
      expect(lookEntry(WIRE)).toEqual({ name: 'A', color: '#22c55e', icon: 'Cloud', iconWeight: 'duotone' })
    })

    it('a later write builds on the entry, not on HostConfig', () => {
      useHostLookStore.setState({ looks: { [WIRE]: { name: 'W' } } })
      useHostStore.getState().setHostIcon(hid, 'Cloud')
      expect(lookEntry(WIRE)).toEqual({ name: 'W', icon: 'Cloud' })
    })

    it('setHostColor keeps the alpha of the colour the selector shows (the entry)', () => {
      useHostLookStore.setState({ looks: { [WIRE]: { colors: { console: { main: { color: '#000000', alpha: 40 } } } } } })
      useHostStore.getState().setHostColor(hid, '#ef4444')
      expect(lookEntry(WIRE).colors?.console?.main).toEqual({ color: '#ef4444', alpha: 40 })
    })

    it('[A] "No color" on a legacy-colour host removes the colour keys; the selector shows none although HostConfig keeps it', () => {
      useHostStore.getState().setHostColor(hid, null)
      expect(hasEntry(WIRE)).toBe(true)
      expect('color' in lookEntry(WIRE)).toBe(false)
      expect('colors' in lookEntry(WIRE)).toBe(false)
      expect(hostLookOf(hid).color).toBeUndefined()
      expect(hostLookOf(hid).colors).toBeUndefined()
      expect(useHostStore.getState().hosts[hid].color).toBe('#22c55e')
    })

    it('[A] icon reset removes the icon keys; the selector shows the default icon although HostConfig keeps one', () => {
      useHostStore.getState().setHostIcon(hid, null)
      expect(hasEntry(WIRE)).toBe(true)
      expect('icon' in lookEntry(WIRE)).toBe(false)
      expect('iconWeight' in lookEntry(WIRE)).toBe(false)
      expect(hostLookOf(hid).icon).toBeUndefined()
      expect(useHostStore.getState().hosts[hid].icon).toBe('Laptop')
    })

    it('a write that changes nothing writes nothing', () => {
      useHostLookStore.setState({ looks: { [WIRE]: { name: 'W', icon: 'Cloud' } } })
      const looks = useHostLookStore.getState().looks
      const s = useHostStore.getState()
      s.setHostIcon(hid, 'NotAnIcon')
      s.setHostColorLayer(hid, 'console', 'middle', { alpha: 5 }) // no console set
      s.clearHostColorMode(hid, 'console') // nothing to clear in the entry
      s.setHostName(hid, '  W  ')
      expect(useHostLookStore.getState().looks).toBe(looks)
    })

    it('setHostName trims; a blank name and an unknown host are no-ops; HostConfig.name is untouched', () => {
      const looks = useHostLookStore.getState().looks
      useHostStore.getState().setHostName(hid, '   ')
      useHostStore.getState().setHostName('nope', 'X')
      expect(useHostLookStore.getState().looks).toBe(looks)
      useHostStore.getState().setHostName(hid, '  air26  ')
      expect(lookEntry(WIRE).name).toBe('air26')
      expect(hostLookOf(hid).name).toBe('air26')
      expect(useHostStore.getState().hosts[hid].name).toBe('A')
    })

    it('a host without daemonId writes under its local id', () => {
      const other = useHostStore.getState().addHost({ name: 'B', ip: '10.0.0.10', port: 7860 })
      useHostStore.getState().setHostName(other, 'Bee')
      expect(lookEntry(other)).toEqual({ name: 'Bee' })
    })

    // Codex critic review-mufd6v5g-2gh633: the daemonId is known, but the re-resolve pass has not yet moved the
    // local-id entry to the d1_ key (lock busy / stores not hydrated). A write in that window must land in the
    // local-id entry — the pass then moves it intact — never seed a new d1_ entry that would win and drop it.
    describe('the window before the re-key (a local-id entry, no d1_ entry)', () => {
      const RED = { console: { main: { color: '#ef4444', alpha: 60 } } }

      beforeEach(() => {
        useHostLookStore.setState({ looks: { [hid]: { name: 'L', colors: RED } } })
      })

      it('setHostIcon lands in looks[localId], colour kept; no d1_ entry is created', () => {
        useHostStore.getState().setHostIcon(hid, 'Cloud')
        expect(lookEntry(hid)).toEqual({ name: 'L', colors: RED, icon: 'Cloud' })
        expect(hasEntry(WIRE)).toBe(false)
      })

      it('setHostColor builds on the local entry (its alpha), in the local entry', () => {
        useHostStore.getState().setHostColor(hid, '#3b82f6')
        expect(lookEntry(hid).colors?.console?.main).toEqual({ color: '#3b82f6', alpha: 60 })
        expect(hasEntry(WIRE)).toBe(false)
      })

      it('seedHostLook seeds nothing: the local entry is the look', () => {
        const looks = useHostLookStore.getState().looks
        useHostStore.getState().seedHostLook(hid)
        expect(useHostLookStore.getState().looks).toBe(looks)
      })

      it('a d1_ entry present (e.g. synced from another device) wins: the write lands there, the local entry untouched', () => {
        useHostLookStore.setState({ looks: { [hid]: { name: 'L', colors: RED }, [WIRE]: { name: 'W' } } })
        useHostStore.getState().setHostIcon(hid, 'Cloud')
        expect(lookEntry(WIRE)).toEqual({ name: 'W', icon: 'Cloud' })
        expect(lookEntry(hid)).toEqual({ name: 'L', colors: RED })
      })
    })
  })

  describe('registerLocalHost seeds the look (plan §0.19)', () => {
    it('creating a host seeds its entry from the new HostConfig', () => {
      const id = useHostStore.getState().registerLocalHost({ url: 'http://127.0.0.1:7861', token: 't', hostname: 'mini' })
      expect(lookEntry(id)).toEqual({ name: 'mini' })
    })

    it('re-registering the same endpoint seeds nothing', () => {
      const id = useHostStore.getState().registerLocalHost({ url: 'http://127.0.0.1:7861', token: 't', hostname: 'mini' })
      useHostLookStore.setState({ looks: {} })
      expect(useHostStore.getState().registerLocalHost({ url: 'http://127.0.0.1:7861', token: 't', hostname: 'other' })).toBe(id)
      expect(useHostLookStore.getState().looks).toEqual({})
    })

    it('an existing entry under the key is never overwritten', () => {
      useHostLookStore.setState({ looks: { fixed: { name: 'kept' } } })
      useHostStore.getState().seedHostLook('fixed') // unknown host → no-op
      const id = useHostStore.getState().addHost({ id: 'fixed', name: 'x', ip: '10.1.1.1', port: 1 })
      useHostStore.getState().seedHostLook(id)
      expect(lookEntry('fixed')).toEqual({ name: 'kept' })
    })
  })

  it('addHost alone creates no look entry (it is also the undo of a deletion — plan §0.19)', () => {
    useHostStore.getState().addHost({ name: 'x', ip: '10.0.0.3', port: 1 })
    expect(useHostLookStore.getState().looks).toEqual({})
  })

  it('reset() also resets the look store', () => {
    useHostStore.getState().setHostName(useHostStore.getState().activeHostId!, 'named')
    expect(Object.keys(useHostLookStore.getState().looks)).toHaveLength(1)
    useHostStore.getState().reset()
    expect(useHostLookStore.getState().looks).toEqual({})
  })

  it('reset() also resets the shown-hosts store to { ids: [] } (plan §0.18)', () => {
    useShownHostsStore.getState().show('d1_a')
    useHostStore.getState().reset()
    expect(useShownHostsStore.getState().ids).toEqual([])
  })

  // host ownership H2d-1 T1 (user rule 2, plan §0.7): a host added later is hidden in every workbench — an add writes
  // NOTHING to the shown list (no seeding, no migration), in memory or in storage.
  describe('adds write nothing to the shown-hosts store (plan §0.7)', () => {
    const SEEDED = ['d1_unknown', 'd1_other']
    let before: ReturnType<typeof useShownHostsStore.getState>
    let stored: string | null

    beforeEach(() => {
      useShownHostsStore.getState().show(SEEDED[0])
      useShownHostsStore.getState().show(SEEDED[1])
      before = useShownHostsStore.getState()
      stored = localStorage.getItem('purdex-shown-hosts')
    })

    const unchanged = () => {
      expect(useShownHostsStore.getState()).toBe(before)
      expect(useShownHostsStore.getState().ids).toEqual(SEEDED)
      expect(localStorage.getItem('purdex-shown-hosts')).toBe(stored)
    }

    it('addHost', () => {
      useHostStore.getState().addHost({ name: 'x', ip: '10.0.0.3', port: 1 })
      unchanged()
    })

    it('registerLocalHost', () => {
      useHostStore.getState().registerLocalHost({ url: 'http://127.0.0.1:7861', token: 't', hostname: 'mini' })
      unchanged()
    })

    it('applyHostTransfer (a created row with a daemonId)', () => {
      const res = useHostStore.getState().applyHostTransfer({
        create: [{ name: 'air26', ip: '100.64.0.4', port: 7860, token: 'tok-air', daemonId: 'd1_air', look: {} }],
        overwrite: [],
      })
      expect(res.kind).toBe('applied')
      unchanged()
    })
  })


  it('setRuntime updates runtime status for a host', () => {
    const state = useHostStore.getState()
    const defaultId = state.activeHostId!
    state.setRuntime(defaultId, { status: 'reconnecting', latency: 42 })

    const updated = useHostStore.getState()
    expect(updated.runtime[defaultId]).toEqual({ status: 'reconnecting', latency: 42 })
  })

  it('getDaemonBase returns http URL from host ip and port', () => {
    const state = useHostStore.getState()
    const defaultId = state.activeHostId!
    const base = state.getDaemonBase(defaultId)
    expect(base).toBe('http://100.64.0.2:7860')
  })

  it('getWsBase returns ws URL from host ip and port', () => {
    const state = useHostStore.getState()
    const defaultId = state.activeHostId!
    const wsBase = state.getWsBase(defaultId)
    expect(wsBase).toBe('ws://100.64.0.2:7860')
  })

  it('getAuthHeaders returns empty object when no token', () => {
    const state = useHostStore.getState()
    const defaultId = state.activeHostId!
    const headers = state.getAuthHeaders(defaultId)
    expect(headers).toEqual({})
  })

  it('getAuthHeaders returns Bearer token when token is set', () => {
    const state = useHostStore.getState()
    const defaultId = state.activeHostId!
    state.updateHost(defaultId, { token: 'my-secret-token' })

    const headers = useHostStore.getState().getAuthHeaders(defaultId)
    expect(headers).toEqual({ Authorization: 'Bearer my-secret-token' })
  })

  it('addHost with explicit id uses provided id', () => {
    const state = useHostStore.getState()
    const id = state.addHost({ id: 'mlab:abc123', name: 'Test', ip: '1.2.3.4', port: 7860 })

    expect(id).toBe('mlab:abc123')
    expect(useHostStore.getState().hosts['mlab:abc123']).toBeDefined()
    expect(useHostStore.getState().hosts['mlab:abc123'].name).toBe('Test')
  })

  it('addHost with duplicate id returns existing id without adding duplicate', () => {
    const state = useHostStore.getState()
    state.addHost({ id: 'dup:test01', name: 'First', ip: '1.1.1.1', port: 7860 })
    state.addHost({ id: 'dup:test01', name: 'Second', ip: '2.2.2.2', port: 7860 })

    const updated = useHostStore.getState()
    expect(updated.hostOrder.filter(id => id === 'dup:test01')).toHaveLength(1)
    expect(updated.hosts['dup:test01'].name).toBe('First')
  })

  it('setRuntime updates daemonState and tmuxState', () => {
    const state = useHostStore.getState()
    const defaultId = state.activeHostId!
    state.setRuntime(defaultId, {
      status: 'disconnected',
      daemonState: 'refused',
      tmuxState: 'unavailable',
    })

    const updated = useHostStore.getState()
    expect(updated.runtime[defaultId].daemonState).toBe('refused')
    expect(updated.runtime[defaultId].tmuxState).toBe('unavailable')
  })

  it('setRuntime partial update preserves existing fields', () => {
    const state = useHostStore.getState()
    const defaultId = state.activeHostId!
    state.setRuntime(defaultId, { status: 'connected', daemonState: 'connected', tmuxState: 'ok' })
    state.setRuntime(defaultId, { tmuxState: 'unavailable' })

    const updated = useHostStore.getState()
    expect(updated.runtime[defaultId].status).toBe('connected')
    expect(updated.runtime[defaultId].daemonState).toBe('connected')
    expect(updated.runtime[defaultId].tmuxState).toBe('unavailable')
  })
})

describe('dev host', () => {
  beforeEach(() => { useHostStore.getState().reset() })

  it('defaults to null and selectDevHostId reads null', () => {
    expect(useHostStore.getState().devHostId).toBeNull()
    expect(selectDevHostId(useHostStore.getState())).toBeNull()
  })

  it('setDevHost accepts a known id', () => {
    const id = useHostStore.getState().hostOrder[0]
    useHostStore.getState().setDevHost(id)
    expect(selectDevHostId(useHostStore.getState())).toBe(id)
  })

  it('setDevHost ignores an unknown id', () => {
    useHostStore.getState().setDevHost('nope')
    expect(useHostStore.getState().devHostId).toBeNull()
  })

  it('setDevHost(null) clears', () => {
    const id = useHostStore.getState().hostOrder[0]
    useHostStore.getState().setDevHost(id)
    useHostStore.getState().setDevHost(null)
    expect(useHostStore.getState().devHostId).toBeNull()
  })

  it('removeHost clears devHostId when it removes the dev host', () => {
    const s = useHostStore.getState()
    const extra = s.addHost({ name: 'b', ip: '10.0.0.2', port: 7860 })
    s.setDevHost(extra)
    s.removeHost(extra)
    expect(useHostStore.getState().devHostId).toBeNull()
  })

  it('removeHost of another host keeps devHostId', () => {
    const s = useHostStore.getState()
    const dev = s.hostOrder[0]
    const extra = s.addHost({ name: 'b', ip: '10.0.0.2', port: 7860 })
    s.setDevHost(dev)
    s.removeHost(extra)
    expect(useHostStore.getState().devHostId).toBe(dev)
  })

  it('selectDevHostId returns null once the id is gone from hosts (stale persisted id)', () => {
    const s = useHostStore.getState()
    const dev = s.hostOrder[0]
    s.setDevHost(dev)
    useHostStore.setState({ hosts: { other: { id: 'other', name: 'o', ip: '10.0.0.9', port: 1, order: 0 } }, hostOrder: ['other'] })
    expect(useHostStore.getState().devHostId).toBe(dev) // raw field untouched
    expect(selectDevHostId(useHostStore.getState())).toBeNull()
  })

  it('devHostId is part of the persisted slice', () => {
    const s = useHostStore.getState()
    s.setDevHost(s.hostOrder[0])
    const partialize = useHostStore.persist.getOptions().partialize!
    expect(partialize(useHostStore.getState())).toMatchObject({ devHostId: s.hostOrder[0] })
  })

  it('selectDevHostId follows the same id after an endpoint change', () => {
    const s = useHostStore.getState()
    const dev = s.hostOrder[0]
    s.setDevHost(dev)
    s.updateHost(dev, { ip: '10.9.9.9', port: 4242 })
    const id = selectDevHostId(useHostStore.getState())
    expect(id).toBe(dev)
    expect(useHostStore.getState().getDaemonBase(id!)).toBe('http://10.9.9.9:4242')
  })
})

describe('persist rehydrate (host color sanitize)', () => {
  it('drops invalid stored colors and keeps valid ones', async () => {
    const hosts = {
      bad: { id: 'bad', name: 'bad', ip: '10.0.0.1', port: 7860, order: 0, color: 'url(x)' },
      obj: { id: 'obj', name: 'obj', ip: '10.0.0.2', port: 7860, order: 1, color: {} },
      good: { id: 'good', name: 'good', ip: '10.0.0.3', port: 7860, order: 2, color: '#3b82f6', token: 'T' },
    }
    localStorage.setItem(
      'purdex-hosts',
      JSON.stringify({ state: { hosts, hostOrder: ['bad', 'obj', 'good'], activeHostId: 'good', devHostId: null }, version: 1 }),
    )
    try {
      await useHostStore.persist.rehydrate()
      const s = useHostStore.getState()
      expect('color' in s.hosts.bad).toBe(false)
      expect('color' in s.hosts.obj).toBe(false)
      expect(s.hosts.good.color).toBe('#3b82f6')
      expect(s.hosts.good.token).toBe('T')
      expect(s.hostOrder).toEqual(['bad', 'obj', 'good'])
      expect(s.activeHostId).toBe('good')
      expect(typeof s.setHostColor).toBe('function')
    } finally {
      localStorage.removeItem('purdex-hosts')
      useHostStore.getState().reset()
    }
  })

  it('drops a stored daemonId that fails isValidDaemonId and keeps a valid one (codex attacker)', async () => {
    const hosts = {
      evil: { id: 'evil', name: 'evil', ip: '10.0.0.1', port: 7860, order: 0, daemonId: 'mini\u202e:abc123' },
      good: { id: 'good', name: 'good', ip: '10.0.0.2', port: 7860, order: 1, daemonId: 'mini-lab:278cbm' },
    }
    localStorage.setItem('purdex-hosts', JSON.stringify({ state: { hosts, hostOrder: ['evil', 'good'], activeHostId: 'good', devHostId: null }, version: 1 }))
    try {
      await useHostStore.persist.rehydrate()
      const s = useHostStore.getState()
      expect('daemonId' in s.hosts.evil).toBe(false)
      expect(s.hosts.evil.name).toBe('evil')
      expect(s.hosts.good.daemonId).toBe('mini-lab:278cbm')
    } finally {
      localStorage.removeItem('purdex-hosts')
      useHostStore.getState().reset()
    }
  })
})

describe('findHostByEndpoint', () => {
  const hosts = {
    a: { id: 'a', name: 'ts', ip: '100.64.0.4', port: 7860, order: 0 },
    b: { id: 'b', name: 'lo', ip: '127.0.0.1', port: 7860, order: 1 },
  }
  it('matches exact ip and port', () => {
    expect(findHostByEndpoint(hosts, '100.64.0.4', 7860)?.id).toBe('a')
    expect(findHostByEndpoint(hosts, '100.64.0.4', 7861)).toBeUndefined()
  })
  it('treats loopback and Tailscale IP as distinct endpoints', () => {
    expect(findHostByEndpoint(hosts, '127.0.0.1', 7860)?.id).toBe('b')
    expect(findHostByEndpoint({ a: hosts.a }, '127.0.0.1', 7860)).toBeUndefined()
  })
})

describe('daemonId (spec 2026-09-23 D1–D3)', () => {
  const ID = 'mini-lab:abc123'
  const OTHER = 'mini-lab:zzz999'
  let hostId: string
  const ep = () => { const h = useHostStore.getState().hosts[hostId]; return `${h.ip}:${h.port}` }
  const at = () => requestAtOf(useHostStore.getState().hosts[hostId])
  const flag = () => selectDaemonIdMismatch(useHostStore.getState(), hostId)
  // A stored claim as sync would write it (updateHost no longer accepts daemonId).
  const seed = (daemonId: string) =>
    useHostStore.setState((s) => ({ hosts: { ...s.hosts, [hostId]: { ...s.hosts[hostId], daemonId } } }))

  beforeEach(() => {
    useHostStore.getState().reset()
    hostId = useHostStore.getState().addHost({ name: 'h', ip: '10.0.0.1', port: 7860, token: 't' })
  })
  afterEach(() => { vi.restoreAllMocks() })

  it('absent stored value → observe writes it', () => {
    useHostStore.getState().observeDaemonId(hostId, ID, at())
    expect(useHostStore.getState().hosts[hostId].daemonId).toBe(ID)
    expect(flag()).toBeUndefined()
  })

  it('an empty observed id is never written and flags nothing', () => {
    useHostStore.getState().observeDaemonId(hostId, '', at())
    expect('daemonId' in useHostStore.getState().hosts[hostId]).toBe(false)
    useHostStore.getState().observeDaemonId(hostId, ID, at())
    useHostStore.getState().observeDaemonId(hostId, '', at())
    expect(useHostStore.getState().hosts[hostId].daemonId).toBe(ID)
    expect(flag()).toBeUndefined()
  })

  it('an observed id that fails isValidDaemonId is ignored like "" — never written, never flagged, never verified (codex attacker)', () => {
    const evil = ['a'.repeat(513), 'mini:abc123\n', 'mini:abc\u0000123', 'mini:abc123\t', 'mini\u202e:abc123', 'mini:abc\u200b']
    for (const bad of evil) useHostStore.getState().observeDaemonId(hostId, bad, at())
    expect('daemonId' in useHostStore.getState().hosts[hostId]).toBe(false)
    expect(useHostStore.getState().runtime[hostId]?.daemonIdVerified).toBeUndefined()
    seed(ID)
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    for (const bad of evil) useHostStore.getState().observeDaemonId(hostId, bad, at())
    expect(useHostStore.getState().hosts[hostId].daemonId).toBe(ID)
    expect(flag()).toBeUndefined()
    expect(warn).not.toHaveBeenCalled()
  })

  it('updateHost never writes daemonId — only observeDaemonId does (review #4)', () => {
    const update = useHostStore.getState().updateHost as (id: string, u: Record<string, unknown>) => void
    update(hostId, { daemonId: ID })
    expect('daemonId' in useHostStore.getState().hosts[hostId]).toBe(false)
    seed(ID)
    update(hostId, { daemonId: '' })
    expect(useHostStore.getState().hosts[hostId].daemonId).toBe(ID)
  })

  it('addHost never writes daemonId', () => {
    const add = useHostStore.getState().addHost as (o: Record<string, unknown>) => string
    const other = add({ name: 'o', ip: '10.0.0.2', port: 7860, daemonId: ID })
    expect('daemonId' in useHostStore.getState().hosts[other]).toBe(false)
  })

  it('a re-point clears daemonId unconditionally, even with an explicit daemonId in the same update (review #4)', () => {
    seed(ID)
    const update = useHostStore.getState().updateHost as (id: string, u: Record<string, unknown>) => void
    update(hostId, { ip: '10.0.0.7', daemonId: ID })
    expect('daemonId' in useHostStore.getState().hosts[hostId]).toBe(false)
  })

  it('different observed value → no write, mismatch flag, one warn per (host, observed)', () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    seed(ID)
    useHostStore.getState().observeDaemonId(hostId, OTHER, at())
    useHostStore.getState().observeDaemonId(hostId, OTHER, at())
    expect(useHostStore.getState().hosts[hostId].daemonId).toBe(ID)
    expect(flag()).toEqual({ stored: ID, observed: OTHER, endpoint: ep() })
    expect(warn).toHaveBeenCalledTimes(1)
  })

  it('equal observed value clears the mismatch flag', () => {
    vi.spyOn(console, 'warn').mockImplementation(() => {})
    seed(ID)
    useHostStore.getState().observeDaemonId(hostId, OTHER, at())
    expect(flag()).toBeDefined()
    useHostStore.getState().observeDaemonId(hostId, ID, at())
    expect(flag()).toBeUndefined()
    expect(useHostStore.getState().runtime[hostId]?.daemonIdMismatch).toBeUndefined()
  })

  it('an answer for a stale endpoint is dropped', () => {
    useHostStore.getState().observeDaemonId(hostId, ID, { endpoint: '10.0.0.9:7860', token: 't' })
    expect('daemonId' in useHostStore.getState().hosts[hostId]).toBe(false)
    seed(ID)
    useHostStore.getState().observeDaemonId(hostId, OTHER, { endpoint: '10.0.0.9:7860', token: 't' })
    expect(flag()).toBeUndefined()
  })

  it('an answer for a token that has since changed is dropped (review #2)', () => {
    const before = at()
    useHostStore.getState().updateHost(hostId, { token: 'rotated' })
    useHostStore.getState().observeDaemonId(hostId, ID, before)
    expect('daemonId' in useHostStore.getState().hosts[hostId]).toBe(false)
  })

  it('an answer for a deleted host is dropped', () => {
    useHostStore.getState().removeHost(hostId)
    useHostStore.getState().observeDaemonId(hostId, ID, { endpoint: '10.0.0.1:7860', token: 't' })
    expect(useHostStore.getState().hosts[hostId]).toBeUndefined()
    expect(useHostStore.getState().runtime[hostId]).toBeUndefined()
  })

  it('a local re-point clears daemonId and the flag', () => {
    vi.spyOn(console, 'warn').mockImplementation(() => {})
    seed(ID)
    useHostStore.getState().observeDaemonId(hostId, OTHER, at())
    useHostStore.getState().updateHost(hostId, { port: 7861 })
    expect('daemonId' in useHostStore.getState().hosts[hostId]).toBe(false)
    expect(flag()).toBeUndefined()
    expect(useHostStore.getState().runtime[hostId]?.daemonIdMismatch).toBeUndefined()
  })

  it('updateHost without an endpoint change keeps daemonId', () => {
    seed(ID)
    useHostStore.getState().updateHost(hostId, { name: 'x', token: 'u', ip: '10.0.0.1', port: 7860 })
    expect(useHostStore.getState().hosts[hostId].daemonId).toBe(ID)
  })

  it('a flag is ignored after a store replace re-points the host or changes its stored value (sync)', () => {
    vi.spyOn(console, 'warn').mockImplementation(() => {})
    seed(ID)
    useHostStore.getState().observeDaemonId(hostId, OTHER, at())
    const h = useHostStore.getState().hosts[hostId]
    useHostStore.setState({ hosts: { [hostId]: { ...h, ip: '10.0.0.5' } } })
    expect(flag()).toBeUndefined()
    useHostStore.setState({ hosts: { [hostId]: { ...h, daemonId: OTHER } } })
    expect(flag()).toBeUndefined()
    useHostStore.setState({ hosts: { [hostId]: h } })
    expect(flag()).toBeDefined()
  })

  it('the mismatch flag is not persisted', () => {
    vi.spyOn(console, 'warn').mockImplementation(() => {})
    seed(ID)
    useHostStore.getState().observeDaemonId(hostId, OTHER, at())
    const partialize = useHostStore.persist.getOptions().partialize!
    expect(JSON.stringify(partialize(useHostStore.getState()))).not.toContain(OTHER)
  })

  describe('verified marker (PR review #1)', () => {
    const verified = () => selectDaemonIdVerified(useHostStore.getState(), hostId)

    it('is false until an answer is observed', () => {
      seed(ID)
      expect(verified()).toBe(false)
    })

    it('is set by a learned answer and by an equal answer', () => {
      useHostStore.getState().observeDaemonId(hostId, ID, at())
      expect(verified()).toBe(true)
      useHostStore.getState().reset()
      hostId = useHostStore.getState().addHost({ name: 'h', ip: '10.0.0.1', port: 7860, token: 't' })
      seed(ID)
      useHostStore.getState().observeDaemonId(hostId, ID, at())
      expect(verified()).toBe(true)
    })

    it('is cleared by a mismatching answer', () => {
      vi.spyOn(console, 'warn').mockImplementation(() => {})
      useHostStore.getState().observeDaemonId(hostId, ID, at())
      useHostStore.getState().observeDaemonId(hostId, OTHER, at())
      expect(verified()).toBe(false)
    })

    it('is token-agnostic', () => {
      useHostStore.getState().observeDaemonId(hostId, ID, at())
      useHostStore.getState().updateHost(hostId, { token: 'rotated' })
      expect(verified()).toBe(true)
    })

    it('is dropped by a local re-point and ignored after a store replace changes endpoint or stored value', () => {
      useHostStore.getState().observeDaemonId(hostId, ID, at())
      const h = useHostStore.getState().hosts[hostId]
      useHostStore.setState({ hosts: { [hostId]: { ...h, daemonId: OTHER } } })
      expect(verified()).toBe(false)
      useHostStore.setState({ hosts: { [hostId]: { ...h, ip: '10.0.0.5' } } })
      expect(verified()).toBe(false)
      useHostStore.setState({ hosts: { [hostId]: h } })
      expect(verified()).toBe(true)
      useHostStore.getState().updateHost(hostId, { port: 7861 })
      useHostStore.getState().updateHost(hostId, { port: 7860 })
      seed(ID)
      expect(verified()).toBe(false)
    })

    it('is not persisted', () => {
      useHostStore.getState().observeDaemonId(hostId, ID, at())
      const partialize = useHostStore.persist.getOptions().partialize!
      expect(JSON.stringify(partialize(useHostStore.getState()))).not.toContain('daemonIdVerified')
    })
  })
})

describe('applyHostTransfer (host transfer H4b, spec §6.4.5, plan R2/R4)', () => {
  let m: string
  let defaultId: string

  beforeEach(() => {
    useHostStore.getState().reset()
    defaultId = useHostStore.getState().hostOrder[0]
    m = useHostStore.getState().addHost({ name: 'm', ip: '1.1.1.1', port: 1, token: 'old' })
    const s = useHostStore.getState()
    s.observeDaemonId(m, 'd1_m', requestAtOf(s.hosts[m]))
    s.setRuntime(m, { status: 'connected' })
    s.setDevHost(m)
  })

  // Before the next test's reset(): a spy left on the look store's state would be copied into every later state.
  afterEach(() => vi.restoreAllMocks())

  function change(over: Partial<TransferChange> = {}): TransferChange {
    return {
      create: [{ name: 'air26', ip: '100.64.0.4', port: 7860, token: 'tok-air', daemonId: 'd1_air', look: { icon: 'Laptop', color: '#ff0000' } }],
      overwrite: [
        {
          hostId: m,
          expect: { endpoint: '1.1.1.1:1', token: 'old', daemonId: 'd1_m' },
          name: 'mm',
          ip: '2.2.2.2',
          port: 7860,
          token: 'new',
          look: { icon: 'Desktop', colors: { console: { main: { color: '#00ff00', alpha: 80 } } } },
        },
      ],
      ...over,
    }
  }

  it('adds and overwrites in ONE set (a subscriber sees one change)', () => {
    const seen = vi.fn()
    const unsub = useHostStore.subscribe(seen)
    const res = useHostStore.getState().applyHostTransfer(change())
    unsub()
    expect(seen).toHaveBeenCalledTimes(1)
    expect(res.kind).toBe('applied')
  })

  it('a created row carries the observed daemonId, is verified; HostConfig gets the name only, the look store name + look', () => {
    const res = useHostStore.getState().applyHostTransfer(change({ overwrite: [] }))
    if (res.kind !== 'applied') throw new Error('not applied')
    expect(res.looks).toBe('ok')
    const [id] = res.created
    const s = useHostStore.getState()
    expect(s.hosts[id]).toEqual({ id, name: 'air26', ip: '100.64.0.4', port: 7860, token: 'tok-air', daemonId: 'd1_air', order: 2 })
    expect(s.hostOrder).toEqual([defaultId, m, id])
    expect(selectDaemonIdVerified(s, id)).toBe(true)
    expect(lookEntry(syncIdOfSync('d1_air'))).toEqual({ name: 'air26', icon: 'Laptop', color: '#ff0000' })
  })

  it('the received entry key (syncIdOfSync(daemonId)) is the key the selector reads for that host afterwards', () => {
    const res = useHostStore.getState().applyHostTransfer(change())
    if (res.kind !== 'applied') throw new Error('not applied')
    const { hosts } = useHostStore.getState()
    const looks = useHostLookStore.getState().looks
    expect(lookKeyOf(hosts[res.created[0]], looks)).toBe(syncIdOfSync('d1_air'))
    expect(lookKeyOf(hosts[m], looks)).toBe(syncIdOfSync('d1_m'))
    expect(hostLookOf(res.created[0])).toEqual({ name: 'air26', icon: 'Laptop', color: '#ff0000' })
    expect(hostLookOf(m)).toEqual({ name: 'mm', icon: 'Desktop', colors: { console: { main: { color: '#00ff00', alpha: 80 } } } })
  })

  it('an overwrite keeps the local id, replaces name / ip / port / token, leaves the HostConfig look fields, and is verified at the new endpoint', () => {
    useHostStore.setState((st) => ({ hosts: { ...st.hosts, [m]: { ...st.hosts[m], icon: 'Cpu' } } }))
    useHostStore.getState().applyHostTransfer(change({ create: [] }))
    const s = useHostStore.getState()
    expect(s.hosts[m]).toEqual({ id: m, name: 'mm', ip: '2.2.2.2', port: 7860, token: 'new', daemonId: 'd1_m', order: 1, icon: 'Cpu' })
    expect(s.hostOrder).toEqual([defaultId, m])
    expect(selectDaemonIdVerified(s, m)).toBe(true)
    // no entry before → the received look is written
    expect(lookEntry(syncIdOfSync('d1_m'))).toEqual({ name: 'mm', icon: 'Desktop', colors: { console: { main: { color: '#00ff00', alpha: 80 } } } })
  })

  it('transferLookEntries: one entry per created / overwritten row under syncIdOfSync(daemonId), { name, ...look }; pure', () => {
    const c = change()
    expect(transferLookEntries(c)).toEqual({
      [syncIdOfSync('d1_air')]: { name: 'air26', icon: 'Laptop', color: '#ff0000' },
      [syncIdOfSync('d1_m')]: { name: 'mm', icon: 'Desktop', colors: { console: { main: { color: '#00ff00', alpha: 80 } } } },
    })
    expect(c).toEqual(change())
    expect(useHostLookStore.getState().looks).toEqual({})
  })

  it('a row without a look still gives the entry its name', () => {
    const [c] = change().create
    useHostStore.getState().applyHostTransfer({ create: [{ ...c, look: undefined }], overwrite: [] })
    expect(lookEntry(syncIdOfSync('d1_air'))).toEqual({ name: 'air26' })
  })

  it('an existing d1_ entry is never changed by a received look (the workbench wins); HostConfig.name is still replaced', () => {
    const mine = { name: 'mine', icon: 'Cpu' }
    useHostLookStore.setState({ looks: { [syncIdOfSync('d1_m')]: mine } })
    const res = useHostStore.getState().applyHostTransfer(change({ create: [] }))
    expect(res).toMatchObject({ kind: 'applied', looks: 'ok' })
    expect(useHostLookStore.getState().looks).toEqual({ [syncIdOfSync('d1_m')]: mine })
    expect(useHostStore.getState().hosts[m].name).toBe('mm')
    expect(hostLookOf(m)).toEqual(mine)
  })

  it('a local-id entry still awaiting the re-key is the look: no d1_ entry is written over it', () => {
    useHostLookStore.setState({ looks: { [m]: { name: 'mine-local' } } })
    useHostStore.getState().applyHostTransfer(change({ create: [] }))
    expect(useHostLookStore.getState().looks).toEqual({ [m]: { name: 'mine-local' } })
    expect(hostLookOf(m).name).toBe('mine-local')
  })

  it('a stale step 1 never runs step 2: host store and look store untouched', () => {
    useHostStore.getState().removeHost(m)
    const put = vi.spyOn(useHostLookStore.getState(), 'putLooksIfAbsent')
    const looksBefore = useHostLookStore.getState().looks
    expect(useHostStore.getState().applyHostTransfer(change())).toEqual({ kind: 'stale' })
    expect(put).not.toHaveBeenCalled()
    expect(useHostLookStore.getState().looks).toBe(looksBefore)
  })

  it('a throw in step 1 is stale and never runs step 2', () => {
    const before = useHostStore.getState().hosts
    const put = vi.spyOn(useHostLookStore.getState(), 'putLooksIfAbsent')
    const boom = { ...change().create[0], get ip(): string { throw new Error('boom') } }
    expect(useHostStore.getState().applyHostTransfer({ create: [boom], overwrite: [] })).toEqual({ kind: 'stale' })
    expect(useHostStore.getState().hosts).toBe(before)
    expect(put).not.toHaveBeenCalled()
    expect(useHostLookStore.getState().looks).toEqual({})
  })

  describe('step 2 fails (plan §0.20)', () => {
    it('the hosts are committed as on success, the look store is unchanged, looks: failed; the Retry writes the entries', () => {
      vi.spyOn(useHostLookStore.getState(), 'putLooksIfAbsent').mockImplementation(() => {
        throw new Error('quota')
      })
      const looksBefore = useHostLookStore.getState().looks
      const seen = vi.fn()
      const unsub = useHostLookStore.subscribe(seen)
      const res = useHostStore.getState().applyHostTransfer(change())
      unsub()
      if (res.kind !== 'applied') throw new Error('not applied')
      expect(res.looks).toBe('failed')
      expect(res.overwritten).toEqual([m])
      const s = useHostStore.getState()
      expect(s.hosts[res.created[0]]).toEqual({ id: res.created[0], name: 'air26', ip: '100.64.0.4', port: 7860, token: 'tok-air', daemonId: 'd1_air', order: 2 })
      expect(s.hosts[m]).toMatchObject({ name: 'mm', ip: '2.2.2.2', token: 'new' })
      expect(selectDaemonIdVerified(s, res.created[0])).toBe(true)
      expect(useHostLookStore.getState().looks).toBe(looksBefore)
      expect(seen).not.toHaveBeenCalled()

      vi.restoreAllMocks()
      expect(applyTransferLooks(transferLookEntries(change()))).toBe('ok')
      expect(lookEntry(syncIdOfSync('d1_air'))).toEqual({ name: 'air26', icon: 'Laptop', color: '#ff0000' })
      expect(lookEntry(syncIdOfSync('d1_m'))).toMatchObject({ name: 'mm', icon: 'Desktop' })
    })

    it('a Retry after another device’s look arrived for that key leaves it untouched', () => {
      vi.spyOn(useHostLookStore.getState(), 'putLooksIfAbsent').mockImplementation(() => {
        throw new Error('quota')
      })
      useHostStore.getState().applyHostTransfer(change())
      vi.restoreAllMocks()
      const synced = { name: 'from-another-device' }
      useHostLookStore.setState({ looks: { [syncIdOfSync('d1_air')]: synced } })
      expect(applyTransferLooks(transferLookEntries(change()))).toBe('ok')
      expect(lookEntry(syncIdOfSync('d1_air'))).toBe(synced)
      expect(lookEntry(syncIdOfSync('d1_m'))).toMatchObject({ name: 'mm' })
    })
  })

  it('an overwrite to a new endpoint starts the runtime over: nothing of the old connection, only daemonIdVerified', () => {
    const retry = vi.fn()
    useHostStore.getState().setRuntime(m, {
      status: 'connected', latency: 12, attachReady: true, daemonState: 'connected', tmuxState: 'ok', manualRetry: retry,
    })
    useHostStore.getState().applyHostTransfer(change({ create: [] }))
    const rt = useHostStore.getState().runtime[m]
    expect(rt.status).not.toBe('connected')
    expect(rt.attachReady).toBeUndefined()
    expect(rt).toEqual({ daemonIdVerified: { endpoint: '2.2.2.2:7860', daemonId: 'd1_m' } })
  })

  it('an overwrite that only changes the token also starts the runtime over', () => {
    useHostStore.getState().setRuntime(m, { status: 'connected', attachReady: true })
    const [o] = change().overwrite
    useHostStore.getState().applyHostTransfer({ create: [], overwrite: [{ ...o, ip: '1.1.1.1', port: 1, token: 'rotated' }] })
    expect(useHostStore.getState().runtime[m]).toEqual({ daemonIdVerified: { endpoint: '1.1.1.1:1', daemonId: 'd1_m' } })
  })

  it('an overwrite that keeps endpoint and token (look / name only) leaves the runtime as it was', () => {
    useHostStore.getState().setRuntime(m, { status: 'connected', attachReady: true, latency: 12 })
    const before = useHostStore.getState().runtime[m]
    const [o] = change().overwrite
    const res = useHostStore.getState().applyHostTransfer({ create: [], overwrite: [{ ...o, ip: '1.1.1.1', port: 1, token: 'old' }] })
    expect(res.kind).toBe('applied')
    const s = useHostStore.getState()
    expect(s.hosts[m]).toMatchObject({ name: 'mm' })
    expect(s.hosts[m].icon).toBeUndefined()
    expect(lookEntry(syncIdOfSync('d1_m'))).toMatchObject({ name: 'mm', icon: 'Desktop' })
    expect(s.runtime[m]).toEqual(before)
    expect(s.runtime[m]).toMatchObject({ status: 'connected', attachReady: true, latency: 12 })
  })

  it.each<[string, () => void]>([
    ['the overwrite target was deleted', () => useHostStore.getState().removeHost(m)],
    [
      "the target's daemonId changed",
      () => useHostStore.setState((st) => ({ hosts: { ...st.hosts, [m]: { ...st.hosts[m], daemonId: 'd1_other' } } })),
    ],
    ["the target's token changed", () => useHostStore.getState().updateHost(m, { token: 'rotated' })],
    ["the target's endpoint changed", () => useHostStore.getState().updateHost(m, { port: 2 })],
    ["a new row's endpoint is now taken", () => { useHostStore.getState().addHost({ name: 'x', ip: '100.64.0.4', port: 7860 }) }],
    ["the overwrite's new endpoint is now taken", () => { useHostStore.getState().addHost({ name: 'x', ip: '2.2.2.2', port: 7860 }) }],
    [
      "a new row's daemonId is now claimed locally",
      () => useHostStore.setState((st) => ({ hosts: { ...st.hosts, [defaultId]: { ...st.hosts[defaultId], daemonId: 'd1_air' } } })),
    ],
  ])('a stale plan (%s) is refused whole: the store is untouched', (_label, drift) => {
    drift()
    const before = useHostStore.getState()
    const seen = vi.fn()
    const unsub = useHostStore.subscribe(seen)
    const res = useHostStore.getState().applyHostTransfer(change())
    unsub()
    const after = useHostStore.getState()
    expect(res).toEqual({ kind: 'stale' })
    expect(seen).not.toHaveBeenCalled()
    expect(after.hosts).toBe(before.hosts)
    expect(after.hostOrder).toBe(before.hostOrder)
    expect(after.activeHostId).toBe(before.activeHostId)
    expect(after.devHostId).toBe(before.devHostId)
    expect(after.runtime).toBe(before.runtime)
  })

  // H4b PR #1397 critic: a created row's ip is canonical (parseTransferRows), a local row's may be any spelling the
  // add-host dialog stored — the uniqueness check compares canonical endpoints.
  it.each([
    ['[0:0:0:0:0:0:0:1]', '[::1]'],
    ['[::ffff:100.64.0.2]', '[::ffff:6440:2]'],
    ['MLAB.example', 'mlab.example'],
  ])('a new row at %s-equivalent %s is refused whole', (localIp, newIp) => {
    useHostStore.getState().addHost({ name: 'x', ip: localIp, port: 7860 })
    const before = useHostStore.getState()
    const res = useHostStore.getState().applyHostTransfer(change({ create: [{ ...change().create[0], ip: newIp }], overwrite: [] }))
    expect(res).toEqual({ kind: 'stale' })
    expect(useHostStore.getState().hosts).toBe(before.hosts)
  })

  it('two new rows at one endpoint are refused', () => {
    const one = change().create[0]
    const res = useHostStore.getState().applyHostTransfer({ create: [one, { ...one, daemonId: 'd1_twin' }], overwrite: [] })
    expect(res).toEqual({ kind: 'stale' })
  })
})
