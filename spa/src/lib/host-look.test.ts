import { beforeEach, describe, expect, it } from 'vitest'
import { act, renderHook } from '@testing-library/react'
import { useHostStore, type HostConfig } from '../stores/useHostStore'
import { useHostLookStore, type HostLookEntry } from '../stores/useHostLookStore'
import { syncIdOfSync } from './profile/host-identity'
import { hostLabel, hostLookOf, useHostLook, useHostLookResolver, wireKeyMovesOf } from './host-look'

const hostA: HostConfig = {
  id: 'host-a',
  name: 'A',
  ip: '100.64.0.2',
  port: 7860,
  order: 0,
  token: 'secret',
  daemonId: 'mini-lab:278cbm',
  colors: { console: { main: { color: '#3b82f6', alpha: 100 } } },
  color: '#22c55e',
  icon: 'Laptop',
  iconWeight: 'duotone',
}
const hostB: HostConfig = { id: 'host-b', name: 'B', ip: '100.64.0.4', port: 7860, order: 1 }

/** merge-mode setState listing the mutable fields explicitly. */
function seedHosts(...hosts: HostConfig[]) {
  useHostStore.setState((s) => ({
    hosts: { ...s.hosts, ...Object.fromEntries(hosts.map((h) => [h.id, { ...h }])) },
    hostOrder: [...s.hostOrder, ...hosts.map((h) => h.id)],
  }))
}

function resetLooks() {
  useHostLookStore.setState({ looks: {} })
}

describe('hostLookOf — no look entry (HostConfig fallback)', () => {
  beforeEach(() => {
    useHostStore.getState().reset()
    resetLooks()
    seedHosts(hostA, hostB)
  })

  it('a local host → exactly its five look fields', () => {
    expect(hostLookOf('host-a')).toEqual({
      name: 'A',
      colors: { console: { main: { color: '#3b82f6', alpha: 100 } } },
      color: '#22c55e',
      icon: 'Laptop',
      iconWeight: 'duotone',
    })
  })

  it('absent fields stay absent (no undefined-valued keys)', () => {
    const look = hostLookOf('host-b')
    expect(look).toEqual({ name: 'B' })
    expect(Object.keys(look)).toEqual(['name'])
  })

  it('an unknown ref → {} (a d1_ id, a random id, __proto__ and friends)', () => {
    for (const ref of ['nope', 'd1_2u8ajsho6ji7nk6h', '__proto__', 'constructor', 'toString', '']) {
      expect(hostLookOf(ref), ref).toEqual({})
      expect(Object.keys(hostLookOf(ref)), ref).toEqual([])
    }
  })

  it('only OWN keys are hosts: an inherited key or __proto__ resolves to the shared empty look', () => {
    const empty = hostLookOf('nope')
    expect(hostLookOf('__proto__')).toBe(empty)
    const hosts = Object.create({ inherited: { ...hostA, id: 'inherited' } }) as Record<string, HostConfig>
    expect(hostLookOf('inherited', hosts)).toBe(empty)
  })

  it('reads the snapshot it is given, not the store', () => {
    const hosts = { x: { ...hostB, id: 'x', name: 'X', icon: 'Cloud' } }
    expect(hostLookOf('x', hosts)).toEqual({ name: 'X', icon: 'Cloud' })
    expect(hostLookOf('host-a', hosts)).toEqual({})
  })

  it('the same host object → the same look object; a new host object → a new look', () => {
    const first = hostLookOf('host-a')
    expect(hostLookOf('host-a')).toBe(first)
    useHostStore.getState().updateHost('host-a', { name: 'A2' })
    const second = hostLookOf('host-a')
    expect(second).not.toBe(first)
    expect(second.name).toBe('A2')
  })

  it('hostLabel → the look name, else the ref', () => {
    expect(hostLabel('host-a', hostLookOf('host-a'))).toBe('A')
    expect(hostLabel('ghost', hostLookOf('ghost'))).toBe('ghost')
  })
})

describe('useHostLook', () => {
  beforeEach(() => {
    useHostStore.getState().reset()
    resetLooks()
    seedHosts(hostA, hostB)
  })

  function renderCounted(ref: string | null) {
    const counter = { renders: 0 }
    const hook = renderHook(() => {
      counter.renders++
      return useHostLook(ref)
    })
    return { ...hook, counter }
  }

  it('returns the memoised look of the host', () => {
    const { result } = renderCounted('host-a')
    expect(result.current).toBe(hostLookOf('host-a'))
  })

  it('null / unknown / __proto__ → {}', () => {
    expect(renderCounted(null).result.current).toEqual({})
    expect(renderCounted('ghost').result.current).toEqual({})
    expect(renderCounted('__proto__').result.current).toEqual({})
  })

  it('re-renders on that host’s rename, colour and icon writes', () => {
    const { result, counter } = renderCounted('host-a')
    let before = counter.renders
    act(() => useHostStore.getState().updateHost('host-a', { name: 'Renamed' }))
    expect(counter.renders).toBeGreaterThan(before)
    expect(result.current.name).toBe('Renamed')

    before = counter.renders
    act(() => useHostStore.getState().setHostColor('host-a', '#ef4444'))
    expect(counter.renders).toBeGreaterThan(before)
    expect(result.current.colors?.console?.main.color).toBe('#ef4444')

    before = counter.renders
    act(() => useHostStore.getState().setHostIcon('host-a', 'Cloud', 'bold'))
    expect(counter.renders).toBeGreaterThan(before)
    expect(result.current).toMatchObject({ icon: 'Cloud', iconWeight: 'bold' })
  })

  it('does NOT re-render on another host’s write or a runtime write', () => {
    const { counter } = renderCounted('host-a')
    const before = counter.renders
    act(() => useHostStore.getState().updateHost('host-b', { name: 'B2' }))
    act(() => useHostStore.getState().setHostColor('host-b', '#ef4444'))
    act(() => useHostStore.getState().setHostIcon('host-b', 'Cloud'))
    act(() => useHostStore.getState().setRuntime('host-a', { status: 'connected' }))
    expect(counter.renders).toBe(before)
  })

  it('follows a host that appears later', () => {
    const { result } = renderCounted('host-c')
    expect(result.current).toEqual({})
    act(() => seedHosts({ ...hostB, id: 'host-c', name: 'C' }))
    expect(result.current).toEqual({ name: 'C' })
  })
})

describe('useHostLookResolver', () => {
  beforeEach(() => {
    useHostStore.getState().reset()
    resetLooks()
    seedHosts(hostA, hostB)
  })

  it('resolves any ref through the current hosts', () => {
    const { result } = renderHook(() => useHostLookResolver())
    expect(result.current('host-a')).toBe(hostLookOf('host-a'))
    expect(result.current('host-b')).toEqual({ name: 'B' })
    expect(result.current('__proto__')).toEqual({})
  })

  it('the callback is stable across runtime writes and changes on a hosts write', () => {
    const { result } = renderHook(() => useHostLookResolver())
    const first = result.current
    act(() => useHostStore.getState().setRuntime('host-a', { status: 'connected' }))
    expect(result.current).toBe(first)
    act(() => useHostStore.getState().updateHost('host-b', { name: 'B2' }))
    expect(result.current).not.toBe(first)
    expect(result.current('host-b').name).toBe('B2')
  })
})

// === H2c-2 T1: the look store is the SOT (plan §0.5 option A — group fallback) ===

const WIRE_A = syncIdOfSync('mini-lab:278cbm')

function putLooks(looks: Record<string, HostLookEntry>) {
  useHostLookStore.setState({ looks })
}

describe('hostLookOf — the look store (option A)', () => {
  beforeEach(() => {
    useHostStore.getState().reset()
    resetLooks()
    seedHosts(hostA, hostB)
  })

  it('a host with a daemonId reads the entry under its d1_ key: the entry name is shown', () => {
    putLooks({ [WIRE_A]: { name: 'Workbench A', icon: 'Cloud' } })
    expect(hostLookOf('host-a')).toEqual({ name: 'Workbench A', icon: 'Cloud' })
  })

  it('an entry without a name → the HostConfig name (name falls back field by field)', () => {
    putLooks({ [WIRE_A]: { icon: 'Cloud' } })
    expect(hostLookOf('host-a')).toEqual({ name: 'A', icon: 'Cloud' })
  })

  it('[A] an entry without colour → no colour, although HostConfig has one (group, not field by field)', () => {
    putLooks({ [WIRE_A]: { name: 'W', icon: 'Cloud' } })
    const look = hostLookOf('host-a')
    expect(look.colors).toBeUndefined()
    expect(look.color).toBeUndefined()
    expect(Object.keys(look).sort()).toEqual(['icon', 'name'])
  })

  it('[A] an entry without icon → the default icon (no icon / weight), although HostConfig has one', () => {
    putLooks({ [WIRE_A]: { colors: { console: { main: { color: '#ef4444', alpha: 50 } } } } })
    expect(hostLookOf('host-a')).toEqual({ name: 'A', colors: { console: { main: { color: '#ef4444', alpha: 50 } } } })
  })

  it('[A] an entry holding only the legacy colour keeps it (the colour group is colors + color)', () => {
    putLooks({ [WIRE_A]: { color: '#123456' } })
    expect(hostLookOf('host-a')).toEqual({ name: 'A', color: '#123456' })
  })

  it('no entry → every field from HostConfig', () => {
    putLooks({ 'some-other-key': { name: 'X' } })
    expect(hostLookOf('host-a')).toEqual({
      name: 'A',
      colors: { console: { main: { color: '#3b82f6', alpha: 100 } } },
      color: '#22c55e',
      icon: 'Laptop',
      iconWeight: 'duotone',
    })
  })

  it('a host WITHOUT daemonId reads the entry under its local id', () => {
    putLooks({ 'host-b': { name: 'Bee', icon: 'Cloud' } })
    expect(hostLookOf('host-b')).toEqual({ name: 'Bee', icon: 'Cloud' })
  })

  it('a host with a daemonId does NOT read an entry under its local id', () => {
    putLooks({ 'host-a': { name: 'stale local' } })
    expect(hostLookOf('host-a').name).toBe('A')
  })

  it('an unresolvable d1_ ref → its entry’s fields; no entry → {}', () => {
    putLooks({ d1_0000000000000xyz: { name: 'air26', icon: 'Cloud' } })
    expect(hostLookOf('d1_0000000000000xyz')).toEqual({ name: 'air26', icon: 'Cloud' })
    expect(hostLookOf('d1_000000000000none')).toEqual({})
    expect(hostLookOf('__proto__')).toEqual({})
  })

  it('two rows claiming one daemon (conflict) both read the one d1_ entry', () => {
    seedHosts({ ...hostB, id: 'host-a2', name: 'A-dup', daemonId: hostA.daemonId })
    putLooks({ [WIRE_A]: { name: 'Shared' } })
    expect(hostLookOf('host-a').name).toBe('Shared')
    expect(hostLookOf('host-a2').name).toBe('Shared')
  })

  it('reads the looks snapshot it is given', () => {
    putLooks({ [WIRE_A]: { name: 'store' } })
    expect(hostLookOf('host-a', useHostStore.getState().hosts, { [WIRE_A]: { name: 'given' } }).name).toBe('given')
  })

  it('the same host and entry objects → the same look object', () => {
    putLooks({ [WIRE_A]: { name: 'W' } })
    expect(hostLookOf('host-a')).toBe(hostLookOf('host-a'))
    expect(hostLookOf('d1_0000000000000xyz')).toBe(hostLookOf('d1_0000000000000xyz'))
  })
})

describe('useHostLook / useHostLookResolver — the look store', () => {
  beforeEach(() => {
    useHostStore.getState().reset()
    resetLooks()
    seedHosts(hostA, hostB)
  })

  it('the hook re-renders on a write to its own entry, not on another entry', () => {
    let renders = 0
    const { result } = renderHook(() => {
      renders++
      return useHostLook('host-a')
    })
    expect(result.current.name).toBe('A')
    let before = renders
    act(() => useHostLookStore.getState().putLook('host-b', { name: 'B!' }))
    act(() => useHostLookStore.getState().putLook('d1_0000000000000xyz', { name: 'far' }))
    expect(renders).toBe(before)
    before = renders
    act(() => useHostLookStore.getState().putLook(WIRE_A, { name: 'Mine' }))
    expect(renders).toBeGreaterThan(before)
    expect(result.current).toEqual({ name: 'Mine' })
  })

  it('the hook follows the key when the host learns its daemonId', () => {
    putLooks({ [WIRE_A]: { name: 'by wire' } })
    const { result } = renderHook(() => useHostLook('host-b'))
    expect(result.current.name).toBe('B')
    act(() => useHostStore.setState((s) => ({ hosts: { ...s.hosts, 'host-b': { ...s.hosts['host-b'], daemonId: hostA.daemonId } } })))
    expect(result.current.name).toBe('by wire')
  })

  it('the hook on an unresolvable ref reads that ref’s entry', () => {
    const { result } = renderHook(() => useHostLook('d1_0000000000000xyz'))
    expect(result.current).toEqual({})
    act(() => useHostLookStore.getState().putLook('d1_0000000000000xyz', { name: 'air26' }))
    expect(result.current).toEqual({ name: 'air26' })
  })

  it('the resolver changes on a looks write and reads it', () => {
    const { result } = renderHook(() => useHostLookResolver())
    const first = result.current
    act(() => useHostLookStore.getState().putLook(WIRE_A, { name: 'W' }))
    expect(result.current).not.toBe(first)
    expect(result.current('host-a')).toEqual({ name: 'W' })
  })
})

describe('wireKeyMovesOf (the re-key moves, plan §0.12)', () => {
  it('local id → d1_ for a host with a valid daemonId; nothing for a host without one', () => {
    expect(wireKeyMovesOf({ 'host-a': hostA, 'host-b': hostB })).toEqual([['host-a', WIRE_A]])
  })

  it('two rows claiming one daemon (conflict) move nothing', () => {
    expect(wireKeyMovesOf({ 'host-a': hostA, dup: { ...hostB, id: 'dup', daemonId: hostA.daemonId } })).toEqual([])
  })
})
