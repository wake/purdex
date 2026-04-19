import { describe, it, expect, beforeEach } from 'vitest'
import { useSyncStore } from './use-sync-store'
import { setSnapshotStore } from './snapshot-store-instance'
import type { SyncBundle } from './types'
import type { ConflictItem } from './types'

const mockBundle: SyncBundle = {
  version: 1,
  timestamp: 1000,
  device: 'test-device',
  collections: {},
}

describe('useSyncStore', () => {
  beforeEach(() => {
    useSyncStore.getState().reset()
  })

  it('starts with null state', () => {
    const state = useSyncStore.getState()
    expect(state.lastSyncedBundle).toBeNull()
    expect(state.lastSyncedAt).toBeNull()
    expect(state.activeProviderId).toBeNull()
    expect(state.enabledModules).toEqual([])
    expect(state.clientId).toBeNull()
    expect(state.syncHostId).toBeNull()
    expect(state.pendingConflicts).toEqual([])
    expect(state.pendingRemoteBundle).toBeNull()
    expect(state.pendingConflictsAt).toBeNull()
  })

  it('setSyncHostId updates the sync host', () => {
    useSyncStore.getState().setSyncHostId('h_aaa')
    expect(useSyncStore.getState().syncHostId).toBe('h_aaa')
  })

  it('setSyncHostId accepts null', () => {
    useSyncStore.getState().setSyncHostId('h_aaa')
    useSyncStore.getState().setSyncHostId(null)
    expect(useSyncStore.getState().syncHostId).toBeNull()
  })

  it('setActiveProvider updates activeProviderId', () => {
    useSyncStore.getState().setActiveProvider('my-provider')
    expect(useSyncStore.getState().activeProviderId).toBe('my-provider')
  })

  it('setActiveProvider resets lastSyncedBundle and lastSyncedAt to null', () => {
    // First set a bundle
    useSyncStore.getState().setLastSyncedBundle(mockBundle)
    expect(useSyncStore.getState().lastSyncedBundle).not.toBeNull()
    expect(useSyncStore.getState().lastSyncedAt).not.toBeNull()

    // Then switch provider — should reset bundle and timestamp
    useSyncStore.getState().setActiveProvider('new-provider')
    expect(useSyncStore.getState().lastSyncedBundle).toBeNull()
    expect(useSyncStore.getState().lastSyncedAt).toBeNull()
  })

  it('setActiveProvider accepts null', () => {
    useSyncStore.getState().setActiveProvider('my-provider')
    useSyncStore.getState().setActiveProvider(null)
    expect(useSyncStore.getState().activeProviderId).toBeNull()
  })

  it('toggleModule adds module ID if not present', () => {
    useSyncStore.getState().toggleModule('mod-a')
    expect(useSyncStore.getState().enabledModules).toContain('mod-a')
  })

  it('toggleModule removes module ID if already present', () => {
    useSyncStore.getState().toggleModule('mod-a')
    useSyncStore.getState().toggleModule('mod-a')
    expect(useSyncStore.getState().enabledModules).not.toContain('mod-a')
  })

  it('toggleModule manages multiple modules independently', () => {
    useSyncStore.getState().toggleModule('mod-a')
    useSyncStore.getState().toggleModule('mod-b')
    useSyncStore.getState().toggleModule('mod-a')
    const modules = useSyncStore.getState().enabledModules
    expect(modules).not.toContain('mod-a')
    expect(modules).toContain('mod-b')
  })

  it('setLastSyncedBundle updates bundle and timestamp', () => {
    const before = Date.now()
    useSyncStore.getState().setLastSyncedBundle(mockBundle)
    const after = Date.now()

    const state = useSyncStore.getState()
    expect(state.lastSyncedBundle).toEqual(mockBundle)
    expect(state.lastSyncedAt).toBeGreaterThanOrEqual(before)
    expect(state.lastSyncedAt).toBeLessThanOrEqual(after)
  })

  it('getClientId returns stable ID on second call', () => {
    const id1 = useSyncStore.getState().getClientId()
    const id2 = useSyncStore.getState().getClientId()
    expect(id1).toBe(id2)
  })

  it('getClientId matches pattern /^c_[a-z0-9]+$/', () => {
    const id = useSyncStore.getState().getClientId()
    expect(id).toMatch(/^c_[a-z0-9]+$/)
  })

  it('getClientId persists into clientId state field', () => {
    const id = useSyncStore.getState().getClientId()
    expect(useSyncStore.getState().clientId).toBe(id)
  })

  it('reset clears all state back to initial values', () => {
    useSyncStore.getState().setLastSyncedBundle(mockBundle)
    useSyncStore.getState().setActiveProvider('some-provider')
    useSyncStore.getState().toggleModule('mod-x')
    useSyncStore.getState().getClientId()

    useSyncStore.getState().reset()

    const state = useSyncStore.getState()
    expect(state.lastSyncedBundle).toBeNull()
    expect(state.lastSyncedAt).toBeNull()
    expect(state.activeProviderId).toBeNull()
    expect(state.enabledModules).toEqual([])
    expect(state.clientId).toBeNull()
    expect(state.syncHostId).toBeNull()
  })

  it('pendingConflicts default to empty + null', () => {
    const state = useSyncStore.getState()
    expect(state.pendingConflicts).toEqual([])
    expect(state.pendingRemoteBundle).toBeNull()
    expect(state.pendingConflictsAt).toBeNull()
  })

  it('setPendingConflicts stores conflicts, bundle, and timestamp', () => {
    const before = Date.now()
    const conflicts: ConflictItem[] = [
      { contributor: 'prefs', field: 'theme', lastSynced: 'light', local: 'dark', remote: { value: 'x', device: 'A' } },
    ]
    const remoteBundle: SyncBundle = { version: 1, timestamp: 5000, device: 'A', collections: {} }

    useSyncStore.getState().setPendingConflicts(conflicts, remoteBundle)
    const state = useSyncStore.getState()
    expect(state.pendingConflicts).toEqual(conflicts)
    expect(state.pendingRemoteBundle).toEqual(remoteBundle)
    expect(state.pendingConflictsAt).toBeGreaterThanOrEqual(before)
    expect(state.pendingConflictsAt).toBeLessThanOrEqual(Date.now())
  })

  it('clearPendingConflicts resets all three fields', () => {
    const conflicts: ConflictItem[] = [
      { contributor: 'prefs', field: 'theme', lastSynced: 'light', local: 'dark', remote: { value: 'x', device: 'A' } },
    ]
    useSyncStore.getState().setPendingConflicts(conflicts, mockBundle)
    useSyncStore.getState().clearPendingConflicts()
    const state = useSyncStore.getState()
    expect(state.pendingConflicts).toEqual([])
    expect(state.pendingRemoteBundle).toBeNull()
    expect(state.pendingConflictsAt).toBeNull()
  })

  it('reset also clears pending conflict fields', () => {
    const conflicts: ConflictItem[] = [
      { contributor: 'prefs', field: 'theme', lastSynced: 'light', local: 'dark', remote: { value: 'x', device: 'A' } },
    ]
    useSyncStore.getState().setPendingConflicts(conflicts, mockBundle)
    useSyncStore.getState().reset()
    const state = useSyncStore.getState()
    expect(state.pendingConflicts).toEqual([])
    expect(state.pendingRemoteBundle).toBeNull()
    expect(state.pendingConflictsAt).toBeNull()
  })

  it('setPendingConflicts trims pendingRemoteBundle collections to conflicted contributors only', () => {
    const conflicts: ConflictItem[] = [
      { contributor: 'prefs', field: 'theme', lastSynced: 'light', local: 'dark', remote: { value: 'x', device: 'A' } },
    ]
    const fullBundle: SyncBundle = {
      version: 2,
      timestamp: 5000,
      device: 'A',
      collections: {
        prefs: { version: 1, data: { theme: 'x' } },
        editor: { version: 1, data: { huge: 'payload' } },
        hosts: { version: 1, data: { n: 1 } },
      },
    }
    useSyncStore.getState().setPendingConflicts(conflicts, fullBundle)
    const stored = useSyncStore.getState().pendingRemoteBundle
    expect(stored).not.toBeNull()
    expect(Object.keys(stored!.collections)).toEqual(['prefs'])
    expect(stored!.collections.prefs).toEqual({ version: 1, data: { theme: 'x' } })
    expect(stored!.version).toBe(2)
    expect(stored!.timestamp).toBe(5000)
    expect(stored!.device).toBe('A')
  })
})

describe('useSyncStore.createPreOperationSnapshot', () => {
  beforeEach(() => {
    useSyncStore.getState().reset()
  })

  it('delegates to SnapshotStore and returns id', async () => {
    const calls: Array<{ trigger: string; opts: unknown }> = []
    setSnapshotStore({
      init: async () => {},
      listLocal: async () => [],
      getLocal: async () => null,
      createSnapshot: async (_b, trigger, opts) => {
        calls.push({ trigger, opts })
        return {
          id: 'stub-id',
          timestamp: 0,
          device: 'd',
          trigger,
          bundleSize: 0,
          contributorIds: [],
          isSessionPristine: opts?.isSessionPristine ?? false,
        }
      },
      deleteLocal: async () => {},
      compact: async () => ({ kept: [], evicted: [] }),
      clear: async () => {},
    })

    const id = await useSyncStore.getState().createPreOperationSnapshot('pre-import')
    expect(id).toBe('stub-id')
    expect(calls[0].trigger).toBe('pre-import')
  })
})

describe('useSyncStore.restoreFromSnapshot', () => {
  beforeEach(() => {
    useSyncStore.getState().reset()
  })

  it('creates pre-restore, clears pendingConflicts, calls contributor deserialize, does NOT touch lastSyncedBundle', async () => {
    const deserializeCalls: string[] = []
    const preOpCalls: string[] = []

    setSnapshotStore({
      init: async () => {},
      listLocal: async () => [],
      getLocal: async () => null,
      createSnapshot: async (_b, trigger) => {
        preOpCalls.push(trigger)
        return {
          id: 'pre-' + trigger,
          timestamp: 0,
          device: 'd',
          trigger,
          bundleSize: 0,
          contributorIds: [],
          isSessionPristine: false,
        }
      },
      deleteLocal: async () => {},
      compact: async () => ({ kept: [], evicted: [] }),
      clear: async () => {},
    })

    const stubBundle: SyncBundle = {
      version: 1,
      timestamp: 0,
      device: 'snap-dev',
      collections: {
        stub1: { version: 1, data: { x: 1 } },
      },
    }

    const { __setEngineForTests, syncEngine } = await import('./register-sync')
    const originalContribs = syncEngine.getContributors()
    __setEngineForTests({
      register: () => {},
      getContributors: () => [
        {
          id: 'stub1',
          strategy: 'full',
          getVersion: () => 1,
          serialize: () => ({ version: 1, data: {} }),
          deserialize: (_p, merge) => {
            if (merge.type === 'full-replace') deserializeCalls.push('stub1')
          },
        },
      ],
      serialize: () => stubBundle,
      push: async () => stubBundle,
      pull: async () => ({ appliedBundle: null, conflicts: [] }),
    } as never)

    useSyncStore.setState({
      pendingConflicts: [{ contributor: 'x', field: 'y', lastSynced: null, local: null, remote: { value: null, device: 'r' } }],
      pendingRemoteBundle: stubBundle,
      pendingConflictsAt: Date.now(),
      lastSyncedBundle: stubBundle,
      lastSyncedAt: 12345,
    })

    await useSyncStore.getState().restoreFromSnapshot(
      { ...stubBundle, bundle: stubBundle, id: 'target', trigger: 'manual', bundleSize: 0, contributorIds: ['stub1'], isSessionPristine: false },
      'local',
    )

    expect(preOpCalls).toContain('pre-restore')
    expect(deserializeCalls).toContain('stub1')

    const state = useSyncStore.getState()
    expect(state.pendingConflicts).toEqual([])
    expect(state.pendingRemoteBundle).toBeNull()
    expect(state.lastSyncedBundle).toEqual(stubBundle)
    expect(state.lastSyncedAt).toBe(12345)

    __setEngineForTests({ ...syncEngine, getContributors: () => originalContribs } as never)
  })
})
