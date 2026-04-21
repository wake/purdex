import { describe, expect, it, beforeEach } from 'vitest'
import { renderHook, waitFor } from '@testing-library/react'
import { useLocalHistory } from './useLocalHistory'
import { setSnapshotStore } from '../../../../../lib/sync/snapshot-store-instance'
import type { SnapshotMetadata } from '../../../../../lib/sync/snapshot-types'

describe('useLocalHistory', () => {
  beforeEach(() => {
    const items: SnapshotMetadata[] = [
      { id: 'a', timestamp: 1, device: 'd', trigger: 'manual', bundleSize: 10, contributorIds: [], isSessionPristine: false },
    ]
    setSnapshotStore({
      init: async () => {},
      listLocal: async () => items,
      getLocal: async () => null,
      createSnapshot: async () => items[0],
      deleteLocal: async () => {},
      demoteSessionPristine: async () => {},
      rotateSessionPristine: async () => ({ ...items[0], isSessionPristine: true }),
      compact: async () => ({ kept: [], evicted: [] }),
      clear: async () => {},
    })
  })

  it('returns metadata list and loading state', async () => {
    const { result } = renderHook(() => useLocalHistory())
    expect(result.current.loading).toBe(true)
    await waitFor(() => expect(result.current.loading).toBe(false))
    expect(result.current.items).toHaveLength(1)
    expect(result.current.items[0].id).toBe('a')
  })
})
