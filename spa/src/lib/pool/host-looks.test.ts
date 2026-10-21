// spa/src/lib/pool/host-looks.test.ts — a pool host's look, order and the write path (monitoring tabs MON-2b, A3 / M9 / M13 / W7).
import { describe, it, expect, beforeEach } from 'vitest'
import { renderHook } from '@testing-library/react'
import { indicatorOf, orderPoolHosts, usePoolHostLook, writePoolHostLook } from './host-looks'
import { useHostStore } from '../../stores/useHostStore'
import { useHostLookStore } from '../../stores/useHostLookStore'
import { useNexHostStore } from '../../stores/useNexHostStore'
import { usePoolHostsStore } from '../../stores/usePoolHostsStore'
import { useUISettingsStore } from '../../stores/useUISettingsStore'

const WWD_A = '82b11c521df16c12' // has a daemon (cfg-a)
const WWD_B = '0123456789abcdef' // no daemon
const cfg = { id: 'cfg-a', name: 'mlab', ip: '1', port: 1, token: 't', order: 0 }

beforeEach(() => {
  useHostStore.setState({ hosts: { 'cfg-a': cfg }, hostOrder: ['cfg-a'], runtime: { 'cfg-a': { status: 'connected' } } } as never)
  useNexHostStore.setState({ byHost: { 'cfg-a': { phase: 'ready', fetchedAt: 1000, daemonCapabilities: ['pool.v1'], wwd: { hostId: WWD_A, alias: 'mlab' } } } } as never)
  useHostLookStore.setState({ looks: {} })
  usePoolHostsStore.setState({ order: [], looks: {} })
})

describe('orderPoolHosts (M13)', () => {
  const h = (host_id: string) => ({ host_id })
  it('the order first; the rest in the pool\'s order after them', () => {
    const out = orderPoolHosts([h('mlab'), h('mserv'), h('air26'), h('air19')], ['air26', 'mlab'])
    expect(out.map((x) => x.host_id)).toEqual(['air26', 'mlab', 'mserv', 'air19'])
  })
  it('an id in the order that the pool does not list does not appear', () => {
    expect(orderPoolHosts([h('a')], ['ghost', 'a']).map((x) => x.host_id)).toEqual(['a'])
  })
  it('an empty order keeps the pool\'s order', () => {
    expect(orderPoolHosts([h('b'), h('a')], []).map((x) => x.host_id)).toEqual(['b', 'a'])
  })
})

describe('indicatorOf (W7)', () => {
  it('the stored indicator wins', () => {
    expect(indicatorOf({ indicator: { shape: 'bar', data: ['disk'] } })).toEqual({ shape: 'bar', data: ['disk'] })
  })
  it('a host with none shows the ring on the overall load (M51): there is no global value to take the default from any more', () => {
    expect(indicatorOf(undefined)).toEqual({ shape: 'ring', data: ['load'] })
    expect(indicatorOf({})).toEqual({ shape: 'ring', data: ['load'] })
  })
})

describe('usePoolHostLook', () => {
  it('a global value left in the store by an older build does not decide the default (MON-12c)', () => {
    useUISettingsStore.setState({ fleetCellIndicator: 'bars-memory' } as never)
    const { result } = renderHook(() => usePoolHostLook(WWD_B))
    expect(result.current.indicator).toEqual({ shape: 'ring', data: ['load'] })
  })
  it('a host with a daemon reads the daemon host\'s look (the one the tab badge shows)', () => {
    useHostLookStore.setState({ looks: { 'cfg-a': { colors: { terminal: { bg: '#112233' } } as never, icon: 'HardDrives' } } })
    usePoolHostsStore.setState({ looks: { [WWD_A]: { icon: 'Cube' } } }) // ignored for colour and icon
    const { result } = renderHook(() => usePoolHostLook(WWD_A))
    expect(result.current.icon).toBe('HardDrives')
    expect(result.current.colors).toBeDefined()
  })
  it('a host without a daemon reads the new store', () => {
    usePoolHostsStore.setState({ looks: { [WWD_B]: { icon: 'Cube', iconWeight: 'bold' } } })
    const { result } = renderHook(() => usePoolHostLook(WWD_B))
    expect(result.current.icon).toBe('Cube')
    expect(result.current.iconWeight).toBe('bold')
  })
  it('the indicator always comes from the new store, else the global default', () => {
    usePoolHostsStore.setState({ looks: { [WWD_A]: { indicator: { shape: 'bars', data: ['cpu', 'mem'] } } } })
    expect(renderHook(() => usePoolHostLook(WWD_A)).result.current.indicator).toEqual({ shape: 'bars', data: ['cpu', 'mem'] })
    expect(renderHook(() => usePoolHostLook(WWD_B)).result.current.indicator).toEqual({ shape: 'ring', data: ['load'] })
  })
})

describe('where a look lives does not flip with the connection', () => {
  const drop = () => useHostStore.setState({ runtime: { 'cfg-a': { status: 'reconnecting' } } } as never)

  it('a daemon host that is reconnecting still reads its host look', () => {
    useHostLookStore.setState({ looks: { 'cfg-a': { icon: 'HardDrives' } } })
    drop()
    useNexHostStore.setState({ byHost: { 'cfg-a': { phase: 'ready', fetchedAt: 0, daemonCapabilities: ['pool.v1'], wwd: { hostId: WWD_A, alias: 'mlab' } } } } as never)
    expect(renderHook(() => usePoolHostLook(WWD_A)).result.current.icon).toBe('HardDrives')
  })
  it('a look edited while the daemon is down still goes to the host look store', () => {
    drop()
    writePoolHostLook(WWD_A, { icon: 'Cube' })
    expect(useHostLookStore.getState().looks['cfg-a']).toMatchObject({ icon: 'Cube' })
    expect(usePoolHostsStore.getState().looks[WWD_A]).toBeUndefined()
  })
  it('clears the legacy single colour (`color`) of a daemon host look and of a pool-store look', () => {
    useHostLookStore.setState({ looks: { 'cfg-a': { color: '#112233', icon: 'HardDrives' } } })
    writePoolHostLook(WWD_A, { color: null })
    expect(useHostLookStore.getState().looks['cfg-a']).toEqual({ icon: 'HardDrives' })
    useNexHostStore.setState({ byHost: {} } as never)
    usePoolHostsStore.setState({ order: [], looks: { [WWD_A]: { color: '#445566', icon: 'Cube' } } })
    writePoolHostLook(WWD_A, { color: null })
    expect(usePoolHostsStore.getState().looks[WWD_A]).toEqual({ icon: 'Cube' })
  })
  it('a face written to the pool store before the owner was known shows until the host look has one', () => {
    useNexHostStore.setState({ byHost: {} } as never)
    writePoolHostLook(WWD_A, { icon: 'Cube' })
    expect(usePoolHostsStore.getState().looks[WWD_A]).toMatchObject({ icon: 'Cube' })
    useNexHostStore.setState({ byHost: { 'cfg-a': { phase: 'ready', fetchedAt: 1000, daemonCapabilities: ['pool.v1'], wwd: { hostId: WWD_A, alias: 'mlab' } } } } as never)
    expect(renderHook(() => usePoolHostLook(WWD_A)).result.current.icon).toBe('Cube')
    useHostLookStore.setState({ looks: { 'cfg-a': { icon: 'HardDrives' } } })
    expect(renderHook(() => usePoolHostLook(WWD_A)).result.current.icon).toBe('HardDrives')
  })
  it('once the owner is known, editing its face drops the early pool face (the indicator stays), so a reset to default does not bring it back', () => {
    useNexHostStore.setState({ byHost: {} } as never)
    writePoolHostLook(WWD_A, { icon: 'Cube', indicator: { shape: 'bar', data: ['disk'] } })
    useNexHostStore.setState({ byHost: { 'cfg-a': { phase: 'ready', fetchedAt: 1000, daemonCapabilities: ['pool.v1'], wwd: { hostId: WWD_A, alias: 'mlab' } } } } as never)
    writePoolHostLook(WWD_A, { icon: 'HardDrives' })
    expect(usePoolHostsStore.getState().looks[WWD_A]).toEqual({ indicator: { shape: 'bar', data: ['disk'] } })
    writePoolHostLook(WWD_A, { icon: null })
    expect(renderHook(() => usePoolHostLook(WWD_A)).result.current.icon).toBeUndefined()
  })
})

describe('writePoolHostLook', () => {
  it('colour and icon of a daemon host go to the host look store, not the new one', () => {
    writePoolHostLook(WWD_A, { icon: 'Cube', iconWeight: 'fill' })
    expect(useHostLookStore.getState().looks['cfg-a']).toMatchObject({ icon: 'Cube', iconWeight: 'fill' })
    expect(usePoolHostsStore.getState().looks[WWD_A]).toBeUndefined()
  })
  it('colour and icon of a host without a daemon go to the new store', () => {
    writePoolHostLook(WWD_B, { icon: 'Cube' })
    expect(usePoolHostsStore.getState().looks[WWD_B]).toMatchObject({ icon: 'Cube' })
    expect(useHostLookStore.getState().looks).toEqual({})
  })
  it('the indicator always goes to the new store, even for a daemon host', () => {
    writePoolHostLook(WWD_A, { indicator: { shape: 'bar', data: ['disk'] } })
    expect(usePoolHostsStore.getState().looks[WWD_A]?.indicator).toEqual({ shape: 'bar', data: ['disk'] })
    expect(useHostLookStore.getState().looks).toEqual({})
  })
  it('null clears a field; the other fields stay', () => {
    writePoolHostLook(WWD_B, { icon: 'Cube', iconWeight: 'bold' })
    writePoolHostLook(WWD_B, { icon: null })
    expect(usePoolHostsStore.getState().looks[WWD_B]).toEqual({ iconWeight: 'bold' })
  })
  it('null on the indicator removes the stored override, so the host follows the default again', () => {
    writePoolHostLook(WWD_B, { icon: 'Cube', indicator: { shape: 'bar', data: ['disk'] } })
    writePoolHostLook(WWD_B, { indicator: null })
    expect(usePoolHostsStore.getState().looks[WWD_B]).toEqual({ icon: 'Cube' })
    expect(renderHook(() => usePoolHostLook(WWD_B)).result.current.indicator).toEqual({ shape: 'ring', data: ['load'] })
    writePoolHostLook(WWD_A, { indicator: null }) // nothing stored: nothing to write
    expect(usePoolHostsStore.getState().looks[WWD_A]).toBeUndefined()
  })
  it('an unknown id (not 16 hex) writes nothing', () => {
    writePoolHostLook('mlab', { icon: 'Cube' })
    expect(usePoolHostsStore.getState().looks).toEqual({})
  })
})
