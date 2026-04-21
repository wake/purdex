import { describe, expect, it, beforeEach, vi } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { SnapshotHistoryPage } from './SnapshotHistoryPage'
import { setSnapshotStore } from '../../../../lib/sync/snapshot-store-instance'
import {
  useSyncStore,
  PreOpFailedError,
  SnapshotCoverageError,
  type RestoreOptions,
} from '../../../../lib/sync/use-sync-store'
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
      demoteSessionPristine: async () => {},
      rotateSessionPristine: async () => ({ ...items[0], isSessionPristine: true }),
      compact: async () => ({ kept: [], evicted: [] }),
      clear: async () => {},
    })
    useSyncStore.getState().reset()
  })

  it('renders list → select row → detail shows → click Restore opens dialog', async () => {
    render(<SnapshotHistoryPage />)
    // Wait for a list row to appear (HistoryRow button includes translated "manual" trigger)
    await waitFor(() =>
      expect(screen.getAllByRole('button').some((b) => b.textContent?.includes('manual'))).toBe(true),
    )
    const row = screen.getAllByRole('button').find((b) => b.textContent?.includes('manual'))!
    fireEvent.click(row)
    // Wait for SnapshotDetail's "Restore this snapshot" button to appear after bundle loads
    await waitFor(() => {
      expect(
        screen.queryAllByRole('button').some((b) => b.textContent?.includes('Restore this snapshot')),
      ).toBe(true)
    })
    const restoreBtn = screen
      .getAllByRole('button')
      .find((b) => b.textContent?.includes('Restore this snapshot'))!
    fireEvent.click(restoreBtn)
    expect(screen.getByRole('dialog')).toBeInTheDocument()
  })

  it('accumulates override flags across coverageWarning + preOpFailed retries', async () => {
    // Simulate a restore that first throws SnapshotCoverageError, then
    // PreOpFailedError, then finally succeeds. handleConfirm must accumulate
    // each override so the final call carries both allowMissingContributors
    // and skipPreOp — clearing either would deadlock the dialog.
    const restoreMock = vi
      .fn<(snapshot: StoredSnapshot, source: 'local' | 'remote', options?: RestoreOptions) => Promise<void>>()
      .mockRejectedValueOnce(new SnapshotCoverageError(['stub']))
      .mockRejectedValueOnce(new PreOpFailedError(new Error('quota')))
      .mockResolvedValueOnce(undefined)
    useSyncStore.setState({ restoreFromSnapshot: restoreMock })

    render(<SnapshotHistoryPage />)
    await waitFor(() =>
      expect(screen.getAllByRole('button').some((b) => b.textContent?.includes('manual'))).toBe(true),
    )
    fireEvent.click(screen.getAllByRole('button').find((b) => b.textContent?.includes('manual'))!)
    await waitFor(() =>
      expect(
        screen.queryAllByRole('button').some((b) => b.textContent?.includes('Restore this snapshot')),
      ).toBe(true),
    )
    fireEvent.click(
      screen.getAllByRole('button').find((b) => b.textContent?.includes('Restore this snapshot'))!,
    )

    // Initial confirm → triggers first call (no overrides) → coverageWarning
    const confirmInitial = screen.getAllByRole('button').find((b) => b.textContent?.includes('Restore') && !b.textContent?.includes('this snapshot'))
    fireEvent.click(confirmInitial!)
    await waitFor(() => expect(restoreMock).toHaveBeenCalledTimes(1))
    // First call carries no overrides (empty or undefined)
    const firstOpts = restoreMock.mock.calls[0][2] ?? {}
    expect(firstOpts.allowMissingContributors).toBeFalsy()
    expect(firstOpts.skipPreOp).toBeFalsy()

    // Second confirm on coverageWarning dialog → adds allowMissingContributors → preOpFailed
    await waitFor(() => {
      const btns = screen.getAllByRole('button')
      expect(btns.some((b) => b.textContent?.toLowerCase().includes('continue'))).toBe(true)
    })
    fireEvent.click(
      screen.getAllByRole('button').find((b) => b.textContent?.toLowerCase().includes('continue'))!,
    )
    await waitFor(() => expect(restoreMock).toHaveBeenCalledTimes(2))
    expect(restoreMock.mock.calls[1][2]).toEqual({ allowMissingContributors: true })

    // Third confirm on preOpFailed dialog → adds skipPreOp → merges with prior allowMissingContributors
    await waitFor(() => {
      const btns = screen.getAllByRole('button')
      expect(btns.some((b) => b.textContent?.toLowerCase().includes('continue'))).toBe(true)
    })
    fireEvent.click(
      screen.getAllByRole('button').find((b) => b.textContent?.toLowerCase().includes('continue'))!,
    )
    await waitFor(() => expect(restoreMock).toHaveBeenCalledTimes(3))
    expect(restoreMock.mock.calls[2][2]).toEqual({
      allowMissingContributors: true,
      skipPreOp: true,
    })
  })

  it('disables Cancel button while restore is in flight (Round-4 Adv-1)', async () => {
    // Hold restoreFromSnapshot indefinitely so the UI observes `restoring=true`.
    const restoreMock = vi
      .fn<(snapshot: StoredSnapshot, source: 'local' | 'remote', options?: RestoreOptions) => Promise<void>>()
      .mockImplementation(() => new Promise<void>(() => {}))
    useSyncStore.setState({ restoreFromSnapshot: restoreMock })

    render(<SnapshotHistoryPage />)
    await waitFor(() =>
      expect(screen.getAllByRole('button').some((b) => b.textContent?.includes('manual'))).toBe(true),
    )
    fireEvent.click(screen.getAllByRole('button').find((b) => b.textContent?.includes('manual'))!)
    await waitFor(() =>
      expect(
        screen.queryAllByRole('button').some((b) => b.textContent?.includes('Restore this snapshot')),
      ).toBe(true),
    )
    fireEvent.click(
      screen.getAllByRole('button').find((b) => b.textContent?.includes('Restore this snapshot'))!,
    )
    fireEvent.click(
      screen.getAllByRole('button').find((b) => b.textContent === 'Restore')!,
    )
    await waitFor(() => expect(restoreMock).toHaveBeenCalledTimes(1))

    // Cancel button must now be disabled so the user cannot close the dialog
    // mid-restore and dispatch a second restore in parallel.
    const cancelBtn = screen.getAllByRole('button').find((b) => b.textContent === 'Cancel')!
    expect(cancelBtn).toBeDisabled()
  })

})
