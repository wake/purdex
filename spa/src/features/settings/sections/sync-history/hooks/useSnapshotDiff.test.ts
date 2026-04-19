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
})
