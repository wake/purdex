import { describe, expect, it, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { SnapshotHistoryPage } from './SnapshotHistoryPage'
import { setSnapshotStore } from '../../../../lib/sync/snapshot-store-instance'
import { useSyncStore } from '../../../../lib/sync/use-sync-store'
import type { SnapshotMetadata, StoredSnapshot } from '../../../../lib/sync/snapshot-types'

const items: SnapshotMetadata[] = [
  { id: 'm1', timestamp: 2, device: 'd', trigger: 'manual', bundleSize: 10, contributorIds: ['w'], isSessionPristine: false },
]
const fullSnap: StoredSnapshot = {
  ...items[0],
  bundle: { version: 1, timestamp: 0, device: 'd', collections: { w: { version: 1, data: {} } } },
}

describe('SnapshotHistoryPage', () => {
  beforeEach(() => {
    setSnapshotStore({
      init: async () => {},
      listLocal: async () => items,
      getLocal: async (id) => (id === 'm1' ? fullSnap : null),
      createSnapshot: async () => items[0],
      deleteLocal: async () => {},
      compact: async () => ({ kept: [], evicted: [] }),
      clear: async () => {},
    })
    useSyncStore.getState().reset()
  })

  it('renders list → select row → detail shows → click Restore opens dialog', async () => {
    render(<SnapshotHistoryPage />)
    await waitFor(() => expect(screen.getAllByRole('button').some((b) => b.textContent?.includes('manual'))).toBe(true))
    const row = screen.getAllByRole('button').find((b) => b.textContent?.includes('manual'))!
    fireEvent.click(row)
    await waitFor(() => {
      const restoreBtn = screen.queryAllByRole('button').find((b) => b.textContent?.includes('detail.restore'))
      expect(restoreBtn).toBeTruthy()
    })
    // Click the SnapshotDetail restore button
    const restoreBtn = screen.getAllByRole('button').find((b) => b.textContent?.includes('detail.restore'))!
    fireEvent.click(restoreBtn)
    expect(screen.getByRole('dialog')).toBeInTheDocument()
  })
})
