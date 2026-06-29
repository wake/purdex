import { describe, it, expect, beforeEach, vi } from 'vitest'
import { render, screen, waitFor, act } from '@testing-library/react'
import { BackupHistoryList } from './BackupHistoryList'
import { useBackupStore } from '../../../stores/useBackupStore'
import { registerBuiltinLocales } from '../../../lib/register-locales'
import type { SnapshotSummary } from '../../../lib/storage-backup/backup-api'

registerBuiltinLocales()

const getHistory = vi.fn()
vi.mock('../../../lib/storage-backup/backup-api', () => ({
  getHistory: (...args: unknown[]) => getHistory(...args),
}))

function summary(p: Partial<SnapshotSummary> & { id: number }): SnapshotSummary {
  return {
    id: p.id,
    device: p.device ?? 'c_aaa',
    parentId: p.parentId ?? null,
    isFork: p.isFork ?? false,
    trigger: p.trigger ?? 'auto',
    createdAt: p.createdAt ?? Math.floor(Date.now() / 1000) - 30,
    fileCount: p.fileCount ?? 1,
    dirCount: p.dirCount ?? 0,
    totalSize: p.totalSize ?? 10,
  }
}

beforeEach(() => {
  getHistory.mockReset()
  useBackupStore.setState({ byHost: {} })
})

describe('BackupHistoryList (Phase 2c T5)', () => {
  it('renders snapshot rows newest-first with a fork badge', async () => {
    getHistory.mockResolvedValue([
      summary({ id: 3, isFork: true, trigger: 'pre-restore' }),
      summary({ id: 2 }),
      summary({ id: 1 }),
    ])
    render(<BackupHistoryList hostId="host-A" />)
    await waitFor(() => expect(screen.getAllByTestId('backup-history-row')).toHaveLength(3))
    const rows = screen.getAllByTestId('backup-history-row')
    expect(rows[0]).toHaveTextContent(/fork|分支/i)
    expect(getHistory).toHaveBeenCalledWith('host-A', 'inapp:buffer')
  })

  it('shows an empty state when there are no snapshots', async () => {
    getHistory.mockResolvedValue([])
    render(<BackupHistoryList hostId="host-A" />)
    await waitFor(() => expect(screen.getByTestId('backup-history-empty')).toBeInTheDocument())
  })

  it('shows an error state (never silent) when the fetch fails', async () => {
    getHistory.mockRejectedValue(new Error('backup/history failed: 500'))
    render(<BackupHistoryList hostId="host-A" />)
    await waitFor(() => expect(screen.getByTestId('backup-history-error')).toHaveTextContent('500'))
  })

  it('refetches when a cross-device backup:done bumps lastBackupAt', async () => {
    getHistory.mockResolvedValueOnce([summary({ id: 1 })]).mockResolvedValueOnce([summary({ id: 2 }), summary({ id: 1 })])
    render(<BackupHistoryList hostId="host-A" />)
    await waitFor(() => expect(screen.getAllByTestId('backup-history-row')).toHaveLength(1))
    act(() => {
      useBackupStore.getState().applyRemoteBackupDone('host-A', {
        storeId: 'inapp:buffer', snapshotId: 2, currentHeadId: 2,
        device: 'c_bbb', trigger: 'auto', createdAt: Math.floor(Date.now() / 1000),
      })
    })
    await waitFor(() => expect(screen.getAllByTestId('backup-history-row')).toHaveLength(2))
    expect(getHistory).toHaveBeenCalledTimes(2)
  })

  it('hides host A rows the instant hostId switches to B, before B resolves (no stale clickable snapshot, codex R2 H1)', async () => {
    // Deferred created up-front (not inside the mock) so resolving it never races
    // the microtask that invokes getHistory.
    let resolveB!: (v: SnapshotSummary[]) => void
    const bPending = new Promise<SnapshotSummary[]>((r) => { resolveB = r })
    getHistory.mockImplementation((hostId: string) =>
      hostId === 'host-A' ? Promise.resolve([summary({ id: 1, device: 'c_A' })]) : bPending,
    )
    const { rerender } = render(<BackupHistoryList hostId="host-A" />)
    await waitFor(() => expect(screen.getAllByTestId('backup-history-row')).toHaveLength(1))

    // Switch to B while B's fetch is still pending: host A's rows must vanish in
    // this render — they belong to A and must not be clickable under hostId B.
    rerender(<BackupHistoryList hostId="host-B" />)
    expect(screen.queryAllByTestId('backup-history-row')).toHaveLength(0)

    // Once B resolves, B's rows appear.
    await act(async () => { resolveB([summary({ id: 9, device: 'c_B' })]) })
    await waitFor(() => expect(screen.getAllByTestId('backup-history-row')).toHaveLength(1))
    expect(screen.getByTestId('backup-history-row')).toHaveTextContent('c_B')
  })

  it('refetches and shows host B history after switching from host A', async () => {
    getHistory.mockImplementation((hostId: string) =>
      Promise.resolve(hostId === 'host-A' ? [summary({ id: 1, device: 'c_A' })] : [summary({ id: 9, device: 'c_B' }), summary({ id: 8, device: 'c_B' })]),
    )
    const { rerender } = render(<BackupHistoryList hostId="host-A" />)
    await waitFor(() => expect(screen.getAllByTestId('backup-history-row')).toHaveLength(1))
    rerender(<BackupHistoryList hostId="host-B" />)
    await waitFor(() => expect(screen.getAllByTestId('backup-history-row')).toHaveLength(2))
    expect(screen.getAllByTestId('backup-history-row')[0]).toHaveTextContent('c_B')
    expect(getHistory).toHaveBeenLastCalledWith('host-B', 'inapp:buffer')
  })
})
