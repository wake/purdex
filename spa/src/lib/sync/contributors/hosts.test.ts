// =============================================================================
// Sync Architecture — HostsContributor Tests
// =============================================================================

import { describe, it, expect, beforeEach } from 'vitest'
import { createHostsContributor } from './hosts'
import { useHostStore } from '../../../stores/useHostStore'
import type { FullPayload } from '../types'

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function resetStore() {
  useHostStore.getState().reset()
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

describe('createHostsContributor', () => {
  let contributor: ReturnType<typeof createHostsContributor>

  beforeEach(() => {
    resetStore()
    contributor = createHostsContributor()
  })

  // -------------------------------------------------------------------------
  // Identity & strategy
  // -------------------------------------------------------------------------

  it('has id "hosts"', () => {
    expect(contributor.id).toBe('hosts')
  })

  it('has strategy "full"', () => {
    expect(contributor.strategy).toBe('full')
  })

  // -------------------------------------------------------------------------
  // getVersion
  // -------------------------------------------------------------------------

  it('getVersion returns 1', () => {
    expect(contributor.getVersion()).toBe(1)
  })

  // -------------------------------------------------------------------------
  // serialize
  // -------------------------------------------------------------------------

  it('serialize returns FullPayload with version 1', () => {
    const payload = contributor.serialize() as FullPayload
    expect(payload.version).toBe(1)
    expect(payload.data).toBeDefined()
  })

  it('serialize only includes expected data fields (no functions)', () => {
    const payload = contributor.serialize() as FullPayload
    const keys = Object.keys(payload.data)

    expect(keys).toContain('hosts')
    expect(keys).toContain('hostOrder')
    expect(keys).toContain('activeHostId')

    // Must NOT contain runtime (ephemeral) or action functions
    expect(keys).not.toContain('runtime')
    expect(keys).not.toContain('addHost')
    expect(keys).not.toContain('removeHost')
    expect(keys).not.toContain('setRuntime')

    // All values must be non-function
    for (const key of keys) {
      expect(typeof payload.data[key]).not.toBe('function')
    }
  })

  it('serialize does NOT include token field in host configs', () => {
    // Add a host with a token
    const state = useHostStore.getState()
    const id = state.addHost({ name: 'secure-host', ip: '10.0.0.1', port: 9000, token: 'secret-token' })

    const payload = contributor.serialize() as FullPayload
    const hosts = payload.data.hosts as Record<string, Record<string, unknown>>

    expect(hosts[id]).toBeDefined()
    expect(hosts[id].token).toBeUndefined()
    expect(hosts[id].name).toBe('secure-host')
  })

  it('serialize does NOT include runtime field', () => {
    const state = useHostStore.getState()
    const hostId = Object.keys(state.hosts)[0]
    state.setRuntime(hostId, { status: 'connected', latency: 42 })

    const payload = contributor.serialize() as FullPayload
    expect(payload.data.runtime).toBeUndefined()
  })

  it('serialize reflects current store state', () => {
    const state = useHostStore.getState()
    const id = state.addHost({ name: 'remote-box', ip: '192.168.1.100', port: 7860 })

    const payload = contributor.serialize() as FullPayload
    const hosts = payload.data.hosts as Record<string, Record<string, unknown>>
    expect(hosts[id]).toBeDefined()
    expect(hosts[id].name).toBe('remote-box')
    expect(payload.data.hostOrder).toContain(id)
  })

  // -------------------------------------------------------------------------
  // deserialize — full-replace
  // -------------------------------------------------------------------------

  it('deserialize with full-replace overwrites store state', () => {
    const incoming: FullPayload = {
      version: 1,
      data: {
        hosts: {
          'h-1': { id: 'h-1', name: 'remote-host', ip: '10.0.0.1', port: 8080, order: 0 },
        },
        hostOrder: ['h-1'],
        activeHostId: 'h-1',
      },
    }

    contributor.deserialize(incoming, { type: 'full-replace' })

    const state = useHostStore.getState()
    expect(state.hosts['h-1']).toBeDefined()
    expect(state.hosts['h-1'].name).toBe('remote-host')
    expect(state.hostOrder).toEqual(['h-1'])
    expect(state.activeHostId).toBe('h-1')
  })

  // -------------------------------------------------------------------------
  // deserialize — field-merge
  // -------------------------------------------------------------------------

  it('deserialize with field-merge only applies resolved remote fields', () => {
    const localHostId = Object.keys(useHostStore.getState().hosts)[0]

    useHostStore.setState({
      hosts: { [localHostId]: { id: localHostId, name: 'local-host', ip: '127.0.0.1', port: 7860, order: 0 } },
      hostOrder: [localHostId],
      activeHostId: localHostId,
    })

    const incoming: FullPayload = {
      version: 1,
      data: {
        hosts: {
          'h-remote': { id: 'h-remote', name: 'remote-host', ip: '10.0.0.1', port: 8080, order: 0 },
        },
        hostOrder: ['h-remote'],
        activeHostId: 'h-remote',
      },
    }

    // Only apply hostOrder from remote; hosts and activeHostId stay local
    contributor.deserialize(incoming, {
      type: 'field-merge',
      resolved: {
        hosts: 'local',
        hostOrder: 'remote',
        activeHostId: 'local',
      },
    })

    const state = useHostStore.getState()
    // hosts stay local
    expect(state.hosts[localHostId]).toBeDefined()
    expect(state.hosts['h-remote']).toBeUndefined()
    // hostOrder updated from remote
    expect(state.hostOrder).toEqual(['h-remote'])
    // activeHostId stays local
    expect(state.activeHostId).toBe(localHostId)
  })
})
