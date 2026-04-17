// spa/src/components/settings/SyncSection.test.tsx
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { useSyncStore } from '../../lib/sync/use-sync-store'
import { useHostStore } from '../../stores/useHostStore'
import { SyncSection } from './SyncSection'
import * as syncActionsModule from '../../lib/sync/sync-actions'
import type { SyncBundle, ConflictItem } from '../../lib/sync/types'

function resetStores() {
  useSyncStore.getState().reset()
  useHostStore.setState({ hosts: {}, hostOrder: [] })
}

describe('SyncSection', () => {
  beforeEach(() => {
    resetStores()
  })

  it('renders provider selector', () => {
    render(<SyncSection />)
    expect(screen.getByText(/Off/i)).toBeTruthy()
    expect(screen.getByText(/Daemon/i)).toBeTruthy()
    expect(screen.getByText(/File/i)).toBeTruthy()
  })

  it('hides daemon/file UI when provider is off', () => {
    render(<SyncSection />)
    expect(screen.queryByText(/Sync Host/i)).toBeNull()
  })

  it('shows host selector when provider = daemon', () => {
    useSyncStore.getState().setActiveProvider('daemon')
    useHostStore.setState({
      hosts: { h1: { id: 'h1', name: 'mini', ip: '127.0.0.1', port: 7860 } as never },
      hostOrder: ['h1'],
    })
    render(<SyncSection />)
    expect(screen.getByText(/Sync Host/i)).toBeTruthy()
  })

  it('syncNow conflict result: writes pendingConflicts to store', async () => {
    useSyncStore.getState().setActiveProvider('daemon')
    useSyncStore.getState().setSyncHostId('h1')
    useHostStore.setState({
      hosts: { h1: { id: 'h1', name: 'mini', ip: '127.0.0.1', port: 7860 } as never },
      hostOrder: ['h1'],
    })

    const bundle: SyncBundle = { version: 1, timestamp: 5000, device: 'A', collections: {} }
    const conflicts: ConflictItem[] = [
      { contributor: 'prefs', field: 'theme', lastSynced: 'light', local: 'dark', remote: { value: 'x', device: 'A' } },
    ]
    vi.spyOn(syncActionsModule, 'syncNow').mockResolvedValue({
      kind: 'conflicts',
      conflicts,
      remoteBundle: bundle,
      partialBaseline: bundle,
    })

    render(<SyncSection />)
    fireEvent.click(screen.getByRole('button', { name: /Sync Now|立即同步/i }))

    await waitFor(() => {
      expect(useSyncStore.getState().pendingConflicts.length).toBe(1)
      expect(useSyncStore.getState().pendingRemoteBundle).toEqual(bundle)
    })
  })

  it('renders ConflictBanner when pendingConflicts non-empty', () => {
    useSyncStore.getState().setActiveProvider('daemon')
    const bundle: SyncBundle = { version: 1, timestamp: 5000, device: 'A', collections: {} }
    useSyncStore.getState().setPendingConflicts(
      [{ contributor: 'prefs', field: 'theme', lastSynced: 'x', local: 'y', remote: { value: 'z', device: 'A' } }],
      bundle,
    )
    render(<SyncSection />)
    expect(screen.getByText(/1/)).toBeTruthy()
    expect(screen.getByRole('button', { name: /view details|查看詳情/i })).toBeTruthy()
  })

  it('apply in banner: calls engine.resolveConflicts + clears pending', async () => {
    useSyncStore.getState().setActiveProvider('daemon')
    const bundle: SyncBundle = { version: 1, timestamp: 5000, device: 'A', collections: {} }
    useSyncStore.getState().setPendingConflicts(
      [{ contributor: 'prefs', field: 'theme', lastSynced: 'x', local: 'y', remote: { value: 'z', device: 'A' } }],
      bundle,
    )
    render(<SyncSection />)
    fireEvent.click(screen.getByRole('button', { name: /view details|查看詳情/i }))
    const radios = screen.getAllByRole('radio')
    fireEvent.click(radios[1])
    fireEvent.click(screen.getByRole('button', { name: /apply|套用/i }))

    await waitFor(() => {
      const s = useSyncStore.getState()
      expect(s.pendingConflicts).toEqual([])
      expect(s.lastSyncedBundle).toEqual(bundle)
    })
  })
})
