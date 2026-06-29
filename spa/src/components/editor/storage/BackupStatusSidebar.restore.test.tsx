import { describe, it, expect, beforeEach, vi } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/react'
import { BackupStatusSidebar } from './BackupStatusSidebar'
import { useHostStore } from '../../../stores/useHostStore'
import { useBackupStore } from '../../../stores/useBackupStore'
import { useEditorStore } from '../../../stores/useEditorStore'
import { registerBuiltinLocales } from '../../../lib/register-locales'
import type { HostBackupState } from '../../../stores/useBackupStore'
import type { SnapshotSummary, SnapshotDetail } from '../../../lib/storage-backup/backup-api'
import type { RestoreResult } from '../../../lib/storage-backup/restore'

registerBuiltinLocales()

const getHistory = vi.fn()
const getSnapshot = vi.fn()
vi.mock('../../../lib/storage-backup/backup-api', () => ({
  getHistory: (...a: unknown[]) => getHistory(...a),
  getSnapshot: (...a: unknown[]) => getSnapshot(...a),
  getBlob: vi.fn(),
}))

const runRestore = vi.fn<(hostId: string, snapshotId: number) => Promise<RestoreResult>>()
vi.mock('../../../lib/storage-backup/restore-wiring', () => ({
  runRestore: (hostId: string, snapshotId: number) => runRestore(hostId, snapshotId),
}))

const ACTIVE = 'host-A'
const row: SnapshotSummary = {
  id: 7, device: 'c_abc', parentId: null, isFork: false, trigger: 'auto',
  createdAt: Math.floor(Date.now() / 1000) - 10, fileCount: 1, dirCount: 0, totalSize: 5,
}
const detail: SnapshotDetail = {
  id: 7, storeId: 'inapp:buffer', device: 'c_abc', parentId: null, isFork: false,
  trigger: 'auto', createdAt: row.createdAt,
  manifest: [{ path: 'a.md', kind: 'file', hash: 'h1', size: 5, words: 1 }],
}

function setHostState(partial: Partial<HostBackupState>) {
  const base: HostBackupState = {
    status: 'idle', lastBackupAt: null, lastError: null, lastSnapshotId: null, lastManifestJSON: null,
  }
  useBackupStore.setState({ byHost: { [ACTIVE]: { ...base, ...partial } } })
}

beforeEach(() => {
  getHistory.mockReset().mockResolvedValue([row])
  getSnapshot.mockReset().mockResolvedValue(detail)
  runRestore.mockReset()
  useHostStore.setState({ activeHostId: ACTIVE })
  useBackupStore.setState({ byHost: {} })
  useEditorStore.setState({ buffers: {}, paneStates: {} })
})

async function openModal() {
  render(<BackupStatusSidebar />)
  const r = await screen.findByTestId('backup-history-row')
  fireEvent.click(r)
  await screen.findByTestId('backup-snapshot-modal')
}

describe('BackupStatusSidebar restore flow (Phase 2c T7)', () => {
  it('disables Restore while the host is backing up', async () => {
    setHostState({ status: 'backing-up' })
    await openModal()
    const btn = screen.getByTestId('backup-snapshot-restore') as HTMLButtonElement
    expect(btn.disabled).toBe(true)
  })

  it('blocked: shows the conflict list, keeps the modal open, and does NOT restore or save', async () => {
    setHostState({ status: 'idle' })
    // A dirty buffer that must remain dirty (no implicit save/discard).
    useEditorStore.setState({ buffers: { 'inapp:/buffer/a.md': { isDirty: true } as never } })
    runRestore.mockResolvedValue({
      status: 'blocked',
      conflicts: [{ type: 'dirty', tabId: 't1', filePath: '/buffer/a.md' }],
    })
    await openModal()
    fireEvent.click(screen.getByTestId('backup-snapshot-restore'))

    await waitFor(() => expect(screen.getByTestId('backup-restore-conflicts')).toHaveTextContent('/buffer/a.md'))
    expect(screen.getByTestId('backup-snapshot-modal')).toBeInTheDocument() // stays open
    expect(runRestore).toHaveBeenCalledTimes(1)
    // No implicit save/discard: the dirty buffer is untouched.
    expect(useEditorStore.getState().buffers['inapp:/buffer/a.md'].isDirty).toBe(true)
  })

  it('done: closes the modal and surfaces success', async () => {
    setHostState({ status: 'idle' })
    runRestore.mockResolvedValue({ status: 'done', restorePointId: 3, changed: { added: [], removed: [], modified: [] } })
    await openModal()
    fireEvent.click(screen.getByTestId('backup-snapshot-restore'))

    await waitFor(() => expect(screen.queryByTestId('backup-snapshot-modal')).toBeNull())
    expect(screen.getByTestId('backup-restore-success')).toBeInTheDocument()
  })

  it('throw: shows an inline restore error and keeps the modal open', async () => {
    setHostState({ status: 'idle' })
    runRestore.mockRejectedValue(new Error('pre-restore safety snapshot failed'))
    await openModal()
    fireEvent.click(screen.getByTestId('backup-snapshot-restore'))

    await waitFor(() => expect(screen.getByTestId('backup-restore-error')).toHaveTextContent('pre-restore'))
    expect(screen.getByTestId('backup-snapshot-modal')).toBeInTheDocument()
  })
})
