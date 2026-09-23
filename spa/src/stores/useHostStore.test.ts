import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import type { TransferChange } from '../lib/host-transfer-plan'
import { useHostStore, selectDevHostId, findHostByEndpoint, selectDaemonIdMismatch, selectDaemonIdVerified, requestAtOf } from './useHostStore'

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

  describe('host colors (spec 2026-09-18 §4.1)', () => {
    const id = () => useHostStore.getState().activeHostId!
    const host = () => useHostStore.getState().hosts[id()]

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

    it('a rejected write leaves a legacy color untouched', () => {
      useHostStore.setState((s) => ({ hosts: { ...s.hosts, [id()]: { ...s.hosts[id()], color: '#22c55e' } } }))
      useHostStore.getState().setHostColorLayer(id(), 'console', 'main', { color: 'red', alpha: 100 })
      useHostStore.getState().setHostColorLayer(id(), 'console', 'middle', { alpha: 50 })   // no set yet → no-op
      expect(host().color).toBe('#22c55e')
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
      expect(host().color).toBe('#22c55e')
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
      const before = host()
      useHostStore.getState().setHostColorLayer(id(), 'console', 'middle', null)
      expect(host()).toBe(before)
      expect(host().color).toBe('#22c55e')
      expect(host().colors).toEqual({ console: { main: { color: '#3b82f6', alpha: 100 } } })
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
    })
  })

  it('setHostIcon stores an icon name', () => {
    const id = useHostStore.getState().activeHostId!
    useHostStore.getState().setHostIcon(id, 'Laptop')
    const host = useHostStore.getState().hosts[id]
    expect(host.icon).toBe('Laptop')
    expect('iconWeight' in host).toBe(false)
  })

  it('setHostIcon stores an icon name with a weight', () => {
    const id = useHostStore.getState().activeHostId!
    useHostStore.getState().setHostIcon(id, 'Laptop', 'duotone')
    const host = useHostStore.getState().hosts[id]
    expect(host.icon).toBe('Laptop')
    expect(host.iconWeight).toBe('duotone')
  })

  it('setHostIcon ignores an invalid weight but still stores the icon', () => {
    const id = useHostStore.getState().activeHostId!
    useHostStore.getState().setHostIcon(id, 'Laptop', 'evil' as never)
    const host = useHostStore.getState().hosts[id]
    expect(host.icon).toBe('Laptop')
    expect('iconWeight' in host).toBe(false)
  })

  it('setHostIcon(null) removes both the icon and iconWeight keys', () => {
    const id = useHostStore.getState().activeHostId!
    useHostStore.getState().setHostIcon(id, 'Laptop', 'fill')
    useHostStore.getState().setHostIcon(id, null)
    const host = useHostStore.getState().hosts[id]
    expect('icon' in host).toBe(false)
    expect('iconWeight' in host).toBe(false)
    expect(host.name).toBe('mlab')
  })

  it.each(['', '   '])('setHostIcon(%j) is treated as null', (blank) => {
    const id = useHostStore.getState().activeHostId!
    useHostStore.getState().setHostIcon(id, 'Laptop', 'fill')
    useHostStore.getState().setHostIcon(id, blank)
    const host = useHostStore.getState().hosts[id]
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
    const before = useHostStore.getState().hosts
    useHostStore.getState().setHostIcon(id, bad, 'fill')
    expect(useHostStore.getState().hosts).toBe(before)
    expect('icon' in useHostStore.getState().hosts[id]).toBe(false)
    expect('iconWeight' in useHostStore.getState().hosts[id]).toBe(false)
  })

  it('setHostIcon with a non-Phosphor name leaves an existing icon untouched', () => {
    const id = useHostStore.getState().activeHostId!
    useHostStore.getState().setHostIcon(id, 'Laptop', 'fill')
    useHostStore.getState().setHostIcon(id, 'NotARealPhosphorIcon', 'bold')
    const host = useHostStore.getState().hosts[id]
    expect(host.icon).toBe('Laptop')
    expect(host.iconWeight).toBe('fill')
  })

  it('setHostIcon on an unknown host is a no-op', () => {
    const before = useHostStore.getState().hosts
    useHostStore.getState().setHostIcon('nope', 'Laptop')
    expect(useHostStore.getState().hosts).toBe(before)
    expect(useHostStore.getState().hosts.nope).toBeUndefined()
  })

  it('setHostIcon leaves the host color untouched', () => {
    const id = useHostStore.getState().activeHostId!
    useHostStore.getState().setHostColor(id, '#3b82f6')
    useHostStore.getState().setHostIcon(id, 'Laptop', 'bold')
    const host = useHostStore.getState().hosts[id]
    expect(host.colors?.console?.main.color).toBe('#3b82f6')
    expect(host.icon).toBe('Laptop')
  })

  it('setHostIcon replacing an icon without a weight keeps the previous weight', () => {
    const id = useHostStore.getState().activeHostId!
    useHostStore.getState().setHostIcon(id, 'Laptop', 'fill')
    useHostStore.getState().setHostIcon(id, 'Desktop')
    const host = useHostStore.getState().hosts[id]
    expect(host.icon).toBe('Desktop')
    expect(host.iconWeight).toBe('fill')
  })

  it('updateHost leaves an existing color untouched', () => {
    const id = useHostStore.getState().activeHostId!
    useHostStore.getState().setHostColor(id, '#ec4899')
    useHostStore.getState().updateHost(id, { name: 'renamed' })
    const host = useHostStore.getState().hosts[id]
    expect(host.name).toBe('renamed')
    expect(host.colors?.console?.main.color).toBe('#ec4899')
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

  it('a created row carries the observed daemonId, is verified, and gets the look', () => {
    const res = useHostStore.getState().applyHostTransfer(change({ overwrite: [] }))
    if (res.kind !== 'applied') throw new Error('not applied')
    const [id] = res.created
    const s = useHostStore.getState()
    expect(s.hosts[id]).toMatchObject({
      name: 'air26', ip: '100.64.0.4', port: 7860, token: 'tok-air', daemonId: 'd1_air', icon: 'Laptop', color: '#ff0000', order: 2,
    })
    expect(s.hostOrder).toEqual([defaultId, m, id])
    expect(selectDaemonIdVerified(s, id)).toBe(true)
  })

  it('an overwrite keeps the local id, replaces ip / port / token, writes name and look, and is verified at the new endpoint', () => {
    useHostStore.getState().applyHostTransfer(change({ create: [] }))
    const s = useHostStore.getState()
    expect(s.hosts[m]).toMatchObject({
      id: m,
      name: 'mm',
      ip: '2.2.2.2',
      port: 7860,
      token: 'new',
      daemonId: 'd1_m',
      icon: 'Desktop',
      colors: { console: { main: { color: '#00ff00', alpha: 80 } } },
    })
    expect(s.hostOrder).toEqual([defaultId, m])
    expect(selectDaemonIdVerified(s, m)).toBe(true)
    expect(s.runtime[m].status).toBe('connected')
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

  it('two new rows at one endpoint are refused', () => {
    const one = change().create[0]
    const res = useHostStore.getState().applyHostTransfer({ create: [one, { ...one, daemonId: 'd1_twin' }], overwrite: [] })
    expect(res).toEqual({ kind: 'stale' })
  })
})
