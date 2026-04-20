import { describe, expect, it, beforeEach } from 'vitest'
import { renderHook, waitFor } from '@testing-library/react'
import { useSnapshotDiff } from './useSnapshotDiff'
import { setSnapshotStore } from '../../../../../lib/sync/snapshot-store-instance'
import { __setEngineForTests } from '../../../../../lib/sync/register-sync'
import type { StoredSnapshot } from '../../../../../lib/sync/snapshot-types'

describe('useSnapshotDiff', () => {
  beforeEach(() => {
    setSnapshotStore({
      init: async () => {},
      listLocal: async () => [],
      getLocal: async () => null,
      createSnapshot: async () => ({ id: 'x', timestamp: 0, device: 'd', trigger: 'manual', bundleSize: 0, contributorIds: [], isSessionPristine: false }),
      deleteLocal: async () => {},
      compact: async () => ({ kept: [], evicted: [] }),
      clear: async () => {},
    })
    __setEngineForTests({
      register: () => {},
      getContributors: () => [],
      serialize: () => ({
        version: 1, timestamp: 0, device: 'd',
        collections: { w: { version: 1, data: { x: 1 } } },
      }),
      push: async () => ({ version: 1, timestamp: 0, device: 'd', collections: {} }),
      pull: async () => ({ appliedBundle: null, conflicts: [] }),
    } as never)
  })

  it('computes contributor diff against current state', async () => {
    const snap: StoredSnapshot = {
      id: 's1',
      timestamp: 0,
      device: 'd',
      trigger: 'manual',
      bundleSize: 0,
      contributorIds: ['w'],
      isSessionPristine: false,
      bundle: {
        version: 1,
        timestamp: 0,
        device: 'd',
        collections: { w: { version: 1, data: { x: 2 } } },
      },
    }

    const { result } = renderHook(() => useSnapshotDiff(snap))
    await waitFor(() => expect(result.current).not.toBeNull())
    expect(result.current!.find((d) => d.id === 'w')?.status).toBe('changed')
  })

  it('compares against the full contributor registry (includes disabled modules)', async () => {
    const { useSyncStore } = await import('../../../../../lib/sync/use-sync-store')
    const { __setEngineForTests } = await import('../../../../../lib/sync/register-sync')
    // Engine has two contributors 'w' and 'x'; user has only 'w' enabled.
    // The current-state bundle used for diffing must still include 'x' so a
    // snapshot containing 'x' is not mis-reported as missing-in-current.
    __setEngineForTests({
      register: () => {},
      getContributors: () => [
        { id: 'w', strategy: 'full', getVersion: () => 1, serialize: () => ({ version: 1, data: { x: 1 } }), deserialize: () => {} },
        { id: 'x', strategy: 'full', getVersion: () => 1, serialize: () => ({ version: 1, data: { k: 1 } }), deserialize: () => {} },
      ],
      serialize: (_device, ids: string[]) => ({
        version: 1, timestamp: 0, device: 'd',
        collections: Object.fromEntries(
          ids.map((id) => [id, id === 'w' ? { version: 1, data: { x: 1 } } : { version: 1, data: { k: 1 } }]),
        ),
      }),
      push: async () => ({ version: 1, timestamp: 0, device: 'd', collections: {} }),
      pull: async () => ({ appliedBundle: null, conflicts: [] }),
    } as never)

    // Only 'w' enabled — pre-fix the hook would call serialize(['w']) and
    // report 'x' as missing from current even though it is registered.
    useSyncStore.setState({ enabledModules: ['w'] })

    const snap: StoredSnapshot = {
      id: 's2', timestamp: 0, device: 'd', trigger: 'manual',
      bundleSize: 0, contributorIds: ['w', 'x'], isSessionPristine: false,
      bundle: {
        version: 1, timestamp: 0, device: 'd',
        collections: {
          w: { version: 1, data: { x: 1 } },
          x: { version: 1, data: { k: 1 } },
        },
      },
    }
    const { result } = renderHook(() => useSnapshotDiff(snap))
    await waitFor(() => expect(result.current).not.toBeNull())
    const xDiff = result.current!.find((d) => d.id === 'x')
    expect(xDiff).toBeDefined()
    expect(xDiff!.status).not.toBe('missing-in-current')
    expect(xDiff!.status).toBe('identical')
  })
})
