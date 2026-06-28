import { describe, it, expect, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import { BackupStatusSidebar } from './BackupStatusSidebar'
import { useHostStore } from '../../../stores/useHostStore'
import { useBackupStore } from '../../../stores/useBackupStore'
import { registerBuiltinLocales } from '../../../lib/register-locales'
import type { HostBackupState } from '../../../stores/useBackupStore'

registerBuiltinLocales()

const ACTIVE = 'host-A'

function setHostState(partial: Partial<HostBackupState>) {
  const base: HostBackupState = {
    status: 'idle',
    lastBackupAt: null,
    lastError: null,
    lastSnapshotId: null,
    lastManifestJSON: null,
  }
  useBackupStore.setState({ byHost: { [ACTIVE]: { ...base, ...partial } } })
}

beforeEach(() => {
  useHostStore.setState({ activeHostId: ACTIVE })
  useBackupStore.setState({ byHost: {} })
})

describe('BackupStatusSidebar (Phase 2b)', () => {
  it('keeps the bordered aside shell', () => {
    render(<BackupStatusSidebar />)
    expect(screen.getByTestId('storage-backups-panel')).toBeInTheDocument()
  })

  it('shows a relative last-backup line when lastBackupAt is set', () => {
    setHostState({ status: 'idle', lastBackupAt: Date.now() - 5000 })
    render(<BackupStatusSidebar />)
    expect(screen.getByTestId('backup-last').textContent).toMatch(/ago/i)
    expect(screen.queryByTestId('backup-progress')).toBeNull()
  })

  it('shows the "backing up" state', () => {
    setHostState({ status: 'backing-up' })
    render(<BackupStatusSidebar />)
    expect(screen.getByTestId('backup-progress')).toBeInTheDocument()
  })

  it('surfaces a failed backup as an inline error carrying the message (never silent)', () => {
    setHostState({ status: 'error', lastError: 'backup/blob PUT failed: 413' })
    render(<BackupStatusSidebar />)
    const banner = screen.getByTestId('backup-error')
    expect(banner.textContent).toContain('413')
  })

  it('shows a "not backed up yet" state when idle with no history', () => {
    setHostState({ status: 'idle' })
    render(<BackupStatusSidebar />)
    expect(screen.getByTestId('backup-never')).toBeInTheDocument()
  })

  it('reflects the ACTIVE host’s state, not another host’s', () => {
    useBackupStore.setState({
      byHost: {
        'host-B': {
          status: 'backing-up', lastBackupAt: null, lastError: null,
          lastSnapshotId: null, lastManifestJSON: null,
        },
      },
    })
    render(<BackupStatusSidebar />)
    // Active host (host-A) has no state → "never"; host-B's backing-up must not leak.
    expect(screen.getByTestId('backup-never')).toBeInTheDocument()
    expect(screen.queryByTestId('backup-progress')).toBeNull()
  })
})
