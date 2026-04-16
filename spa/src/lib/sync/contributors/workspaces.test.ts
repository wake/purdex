// =============================================================================
// Sync Architecture — WorkspacesContributor Tests
// =============================================================================

import { describe, it, expect, beforeEach } from 'vitest'
import { createWorkspacesContributor } from './workspaces'
import { useWorkspaceStore } from '../../../features/workspace/store'
import type { FullPayload } from '../types'

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function resetStore() {
  useWorkspaceStore.getState().reset()
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

describe('createWorkspacesContributor', () => {
  let contributor: ReturnType<typeof createWorkspacesContributor>

  beforeEach(() => {
    resetStore()
    contributor = createWorkspacesContributor()
  })

  // -------------------------------------------------------------------------
  // Identity & strategy
  // -------------------------------------------------------------------------

  it('has id "workspaces"', () => {
    expect(contributor.id).toBe('workspaces')
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

    expect(keys).toContain('workspaces')
    expect(keys).toContain('activeWorkspaceId')

    // Must NOT contain action functions
    expect(keys).not.toContain('addWorkspace')
    expect(keys).not.toContain('removeWorkspace')
    expect(keys).not.toContain('setActiveWorkspace')
    expect(keys).not.toContain('reset')

    // All values must be non-function
    for (const key of keys) {
      expect(typeof payload.data[key]).not.toBe('function')
    }
  })

  it('serialize reflects current store state', () => {
    useWorkspaceStore.getState().addWorkspace('TestWS')
    const payload = contributor.serialize() as FullPayload
    const workspaces = payload.data.workspaces as Array<{ name: string }>
    expect(workspaces.some((w) => w.name === 'TestWS')).toBe(true)
  })

  // -------------------------------------------------------------------------
  // deserialize — full-replace
  // -------------------------------------------------------------------------

  it('deserialize with full-replace overwrites store state', () => {
    const incoming: FullPayload = {
      version: 1,
      data: {
        workspaces: [{ id: 'ws-1', name: 'Remote WS', tabs: [], activeTabId: null }],
        activeWorkspaceId: 'ws-1',
      },
    }

    contributor.deserialize(incoming, { type: 'full-replace' })

    const state = useWorkspaceStore.getState()
    expect(state.workspaces).toHaveLength(1)
    expect(state.workspaces[0].name).toBe('Remote WS')
    expect(state.activeWorkspaceId).toBe('ws-1')
  })

  // -------------------------------------------------------------------------
  // deserialize — field-merge
  // -------------------------------------------------------------------------

  it('deserialize with field-merge only applies resolved remote fields', () => {
    useWorkspaceStore.setState({
      workspaces: [{ id: 'local-ws', name: 'Local WS', tabs: [], activeTabId: null }],
      activeWorkspaceId: 'local-ws',
    })

    const incoming: FullPayload = {
      version: 1,
      data: {
        workspaces: [{ id: 'remote-ws', name: 'Remote WS', tabs: [], activeTabId: null }],
        activeWorkspaceId: 'remote-ws',
      },
    }

    // Only apply activeWorkspaceId from remote
    contributor.deserialize(incoming, {
      type: 'field-merge',
      resolved: {
        workspaces: 'local',
        activeWorkspaceId: 'remote',
      },
    })

    const state = useWorkspaceStore.getState()
    // workspaces stays local
    expect(state.workspaces[0].name).toBe('Local WS')
    // activeWorkspaceId updated from remote
    expect(state.activeWorkspaceId).toBe('remote-ws')
  })

  it('deserialize with field-merge ignores fields not present in resolved', () => {
    useWorkspaceStore.setState({
      workspaces: [],
      activeWorkspaceId: 'local-id',
    })

    const incoming: FullPayload = {
      version: 1,
      data: {
        workspaces: [],
        activeWorkspaceId: 'remote-id',
      },
    }

    // Only activeWorkspaceId=remote; workspaces not mentioned
    contributor.deserialize(incoming, {
      type: 'field-merge',
      resolved: { activeWorkspaceId: 'remote' },
    })

    const state = useWorkspaceStore.getState()
    expect(state.activeWorkspaceId).toBe('remote-id')
  })
})
