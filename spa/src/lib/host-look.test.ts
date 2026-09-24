import { beforeEach, describe, expect, it } from 'vitest'
import { act, renderHook } from '@testing-library/react'
import { useHostStore, type HostConfig } from '../stores/useHostStore'
import { hostLabel, hostLookOf, useHostLook, useHostLookResolver } from './host-look'

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

describe('hostLookOf (H2a — HostConfig only)', () => {
  beforeEach(() => {
    useHostStore.getState().reset()
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
