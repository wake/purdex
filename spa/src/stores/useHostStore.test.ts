import { describe, it, expect, beforeEach } from 'vitest'
import { useHostStore } from './useHostStore'

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
})
