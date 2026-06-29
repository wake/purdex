import { describe, it, expect, beforeEach, vi } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/react'
import { BackupSnapshotModal } from './BackupSnapshotModal'
import { registerBuiltinLocales } from '../../../lib/register-locales'
import type { SnapshotSummary, SnapshotDetail } from '../../../lib/storage-backup/backup-api'

registerBuiltinLocales()

const getSnapshot = vi.fn()
const getBlob = vi.fn()
vi.mock('../../../lib/storage-backup/backup-api', () => ({
  getSnapshot: (...args: unknown[]) => getSnapshot(...args),
  getBlob: (...args: unknown[]) => getBlob(...args),
}))

const summary: SnapshotSummary = {
  id: 42, device: 'c_abc', parentId: null, isFork: true, trigger: 'pre-restore',
  createdAt: 1_700_000_000, fileCount: 2, dirCount: 1, totalSize: 20,
}

const detail: SnapshotDetail = {
  id: 42, storeId: 'inapp:buffer', device: 'c_abc', parentId: null, isFork: true,
  trigger: 'pre-restore', createdAt: 1_700_000_000,
  manifest: [
    { path: 'empty-dir', kind: 'dir', hash: '', size: 0, words: 0 },
    { path: 'a.md', kind: 'file', hash: 'h1', size: 12, words: 3 },
    { path: 'sub/b.txt', kind: 'file', hash: 'h2', size: 8, words: 1 },
  ],
}

beforeEach(() => {
  getSnapshot.mockReset()
  getBlob.mockReset()
})

describe('BackupSnapshotModal (Phase 2c T6)', () => {
  it('fetches the snapshot and lists files + dirs (incl. empty dir) without downloading blobs', async () => {
    getSnapshot.mockResolvedValue(detail)
    render(<BackupSnapshotModal hostId="host-A" snapshot={summary} onClose={() => {}} />)

    await waitFor(() => expect(screen.getByText('a.md')).toBeInTheDocument())
    expect(screen.getByText('empty-dir')).toBeInTheDocument()
    expect(screen.getByText('sub/b.txt')).toBeInTheDocument()
    expect(getSnapshot).toHaveBeenCalledWith('host-A', 42)
    expect(getBlob).not.toHaveBeenCalled()
  })

  it('shows the header device / trigger / fork badge', async () => {
    getSnapshot.mockResolvedValue(detail)
    render(<BackupSnapshotModal hostId="host-A" snapshot={summary} onClose={() => {}} />)
    await waitFor(() => expect(screen.getByText('a.md')).toBeInTheDocument())
    const modal = screen.getByTestId('backup-snapshot-modal')
    expect(modal).toHaveTextContent('c_abc')
    expect(modal).toHaveTextContent(/fork|分支/i)
  })

  it('surfaces a load error', async () => {
    getSnapshot.mockRejectedValue(new Error('backup/snapshot GET failed: 500'))
    render(<BackupSnapshotModal hostId="host-A" snapshot={summary} onClose={() => {}} />)
    await waitFor(() => expect(screen.getByTestId('backup-snapshot-error')).toHaveTextContent('500'))
  })

  it('calls onClose from the close button', async () => {
    getSnapshot.mockResolvedValue(detail)
    const onClose = vi.fn()
    render(<BackupSnapshotModal hostId="host-A" snapshot={summary} onClose={onClose} />)
    await waitFor(() => expect(screen.getByText('a.md')).toBeInTheDocument())
    fireEvent.click(screen.getByTestId('backup-snapshot-close'))
    expect(onClose).toHaveBeenCalled()
  })

  it('invokes onRestore when the Restore button is clicked', async () => {
    getSnapshot.mockResolvedValue(detail)
    const onRestore = vi.fn()
    render(<BackupSnapshotModal hostId="host-A" snapshot={summary} onClose={() => {}} onRestore={onRestore} />)
    await waitFor(() => expect(screen.getByText('a.md')).toBeInTheDocument())
    fireEvent.click(screen.getByTestId('backup-snapshot-restore'))
    expect(onRestore).toHaveBeenCalled()
  })

  it('disables Restore (and shows restoring label) while busy or disabled', async () => {
    getSnapshot.mockResolvedValue(detail)
    const onRestore = vi.fn()
    render(
      <BackupSnapshotModal hostId="host-A" snapshot={summary} onClose={() => {}} onRestore={onRestore} restoreBusy restoreDisabled />,
    )
    await waitFor(() => expect(screen.getByText('a.md')).toBeInTheDocument())
    const btn = screen.getByTestId('backup-snapshot-restore') as HTMLButtonElement
    expect(btn.disabled).toBe(true)
    expect(btn).toHaveTextContent(/restoring|還原中/i)
    fireEvent.click(btn)
    expect(onRestore).not.toHaveBeenCalled()
  })

  it('renders the blocked conflict list (dirty + locked) without restoring', async () => {
    getSnapshot.mockResolvedValue(detail)
    render(
      <BackupSnapshotModal
        hostId="host-A"
        snapshot={summary}
        onClose={() => {}}
        onRestore={() => {}}
        conflicts={[
          { type: 'dirty', tabId: 't1', filePath: '/buffer/a.md' },
          { type: 'locked', tabId: 't2', filePath: '/buffer/b.md' },
        ]}
      />,
    )
    const block = await screen.findByTestId('backup-restore-conflicts')
    expect(block).toHaveTextContent('/buffer/a.md')
    expect(block).toHaveTextContent('/buffer/b.md')
  })

  it('renders an inline restore error', async () => {
    getSnapshot.mockResolvedValue(detail)
    render(
      <BackupSnapshotModal hostId="host-A" snapshot={summary} onClose={() => {}} onRestore={() => {}} restoreError="no restore-point" />,
    )
    expect(await screen.findByTestId('backup-restore-error')).toHaveTextContent('no restore-point')
  })
})
