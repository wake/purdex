// spa/src/components/settings/SyncSection.test.tsx
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { useSyncStore } from '../../lib/sync/use-sync-store'
import { useHostStore } from '../../stores/useHostStore'
import { SyncSection } from './SyncSection'
import { SettingsRouteContext } from '../SettingsPage'
import * as syncActionsModule from '../../lib/sync/sync-actions'
import type { SyncActionResult } from '../../lib/sync/sync-actions'
import { syncEngine } from '../../lib/sync/register-sync'
import { setSnapshotStore } from '../../lib/sync/snapshot-store-instance'
import type { SnapshotStore } from '../../lib/sync/snapshot-store'
import type { SnapshotMetadata, SnapshotTrigger } from '../../lib/sync/snapshot-types'
import type { SyncBundle, ConflictItem } from '../../lib/sync/types'

function resetStores() {
  useSyncStore.getState().reset()
  useHostStore.setState({ hosts: {}, hostOrder: [] })
}

describe('SyncSection', () => {
  beforeEach(() => {
    resetStores()
    vi.restoreAllMocks()
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

  it('discards syncNow result if provider changes during await', async () => {
    useSyncStore.getState().setActiveProvider('daemon')
    useSyncStore.getState().setSyncHostId('h1')
    useHostStore.setState({
      hosts: { h1: { id: 'h1', name: 'mini', ip: '127.0.0.1', port: 7860 } as never },
      hostOrder: ['h1'],
    })

    let resolveSync!: (r: SyncActionResult) => void
    const pending = new Promise<SyncActionResult>((r) => { resolveSync = r })
    vi.spyOn(syncActionsModule, 'syncNow').mockReturnValue(pending)

    render(<SyncSection />)
    fireEvent.click(screen.getByRole('button', { name: /Sync Now|立即同步/i }))

    // User turns provider off while sync is still awaiting — setActiveProvider
    // also resets lastSyncedBundle to null as a side effect.
    useSyncStore.getState().setActiveProvider(null)

    const staleBundle: SyncBundle = { version: 1, timestamp: 5000, device: 'A', collections: {} }
    resolveSync({ kind: 'ok', appliedBundle: staleBundle })
    await pending
    await new Promise((r) => setTimeout(r, 0))

    // Guard should have dropped the stale result — bundle stays null.
    expect(useSyncStore.getState().lastSyncedBundle).toBeNull()
  })

  it('apply in banner: calls engine.resolveConflicts + clears pending', async () => {
    useSyncStore.getState().setActiveProvider('daemon')
    useSyncStore.getState().setSyncHostId('h1')
    useHostStore.setState({
      hosts: { h1: { id: 'h1', name: 'mini', ip: '127.0.0.1', port: 7860 } as never },
      hostOrder: ['h1'],
    })
    const bundle: SyncBundle = { version: 1, timestamp: 5000, device: 'A', collections: {} }
    useSyncStore.getState().setPendingConflicts(
      [{ contributor: 'prefs', field: 'theme', lastSynced: 'x', local: 'y', remote: { value: 'z', device: 'A' } }],
      bundle,
    )
    // Suppress push side effect for this happy-path assertion.
    vi.spyOn(syncEngine, 'push').mockResolvedValue(bundle)
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

  it('apply in banner (daemon): pushes merged state to provider after resolve', async () => {
    useSyncStore.getState().setActiveProvider('daemon')
    useSyncStore.getState().setSyncHostId('h1')
    useHostStore.setState({
      hosts: { h1: { id: 'h1', name: 'mini', ip: '127.0.0.1', port: 7860 } as never },
      hostOrder: ['h1'],
    })
    const bundle: SyncBundle = { version: 1, timestamp: 5000, device: 'A', collections: {} }
    useSyncStore.getState().setPendingConflicts(
      [{ contributor: 'prefs', field: 'theme', lastSynced: 'x', local: 'y', remote: { value: 'z', device: 'A' } }],
      bundle,
    )
    const pushSpy = vi.spyOn(syncEngine, 'push').mockResolvedValue(bundle)

    render(<SyncSection />)
    fireEvent.click(screen.getByRole('button', { name: /view details|查看詳情/i }))
    fireEvent.click(screen.getAllByRole('radio')[1])
    fireEvent.click(screen.getByRole('button', { name: /apply|套用/i }))

    await waitFor(() => {
      expect(pushSpy).toHaveBeenCalledTimes(1)
    })
  })

  it('apply in banner (file): does NOT push (file provider has no remote)', async () => {
    useSyncStore.getState().setActiveProvider('file')
    const bundle: SyncBundle = { version: 1, timestamp: 5000, device: 'A', collections: {} }
    useSyncStore.getState().setPendingConflicts(
      [{ contributor: 'prefs', field: 'theme', lastSynced: 'x', local: 'y', remote: { value: 'z', device: 'A' } }],
      bundle,
    )
    const pushSpy = vi.spyOn(syncEngine, 'push').mockResolvedValue(bundle)

    render(<SyncSection />)
    fireEvent.click(screen.getByRole('button', { name: /view details|查看詳情/i }))
    fireEvent.click(screen.getAllByRole('radio')[1])
    fireEvent.click(screen.getByRole('button', { name: /apply|套用/i }))

    await waitFor(() => {
      expect(useSyncStore.getState().pendingConflicts).toEqual([])
    })
    expect(pushSpy).not.toHaveBeenCalled()
  })
})

describe('SyncSection subsection routing', () => {
  beforeEach(() => {
    resetStores()
    vi.restoreAllMocks()
  })

  it('renders SnapshotHistoryPage when subsection=history', () => {
    render(
      <SettingsRouteContext.Provider value={{ subsection: 'history', setSubsection: () => {} }}>
        <SyncSection />
      </SettingsRouteContext.Provider>,
    )
    // SnapshotHistoryPage renders HistoryTabs → role=tablist
    expect(screen.getByRole('tablist')).toBeInTheDocument()
  })

  it('renders main Sync section when subsection=null', () => {
    render(
      <SettingsRouteContext.Provider value={{ subsection: null, setSubsection: () => {} }}>
        <SyncSection />
      </SettingsRouteContext.Provider>,
    )
    // Main section shows the provider selector ("settings.sync.provider.*" keys)
    // Any text containing 'provider' will do
    expect(screen.getByText(/provider/i)).toBeInTheDocument()
  })

  it('"View History" button calls setSubsection("history")', () => {
    const fn = vi.fn()
    render(
      <SettingsRouteContext.Provider value={{ subsection: null, setSubsection: fn }}>
        <SyncSection />
      </SettingsRouteContext.Provider>,
    )
    fireEvent.click(screen.getByRole('button', { name: /history/i }))
    expect(fn).toHaveBeenCalledWith('history')
  })
})

// ---------------------------------------------------------------------------
// T23: pre-import + post-sync snapshot wiring
// ---------------------------------------------------------------------------

interface SnapshotCall {
  bundle: SyncBundle
  trigger: SnapshotTrigger
}

/** Build a stub SnapshotStore that records all createSnapshot calls. */
function installSnapshotSpy(): SnapshotCall[] {
  const calls: SnapshotCall[] = []
  const stub: SnapshotStore = {
    init: async () => {},
    listLocal: async () => [],
    getLocal: async () => null,
    createSnapshot: async (bundle, trigger, opts) => {
      calls.push({ bundle, trigger })
      const meta: SnapshotMetadata = {
        id: `snap_${calls.length}`,
        timestamp: Date.now(),
        device: bundle.device,
        trigger,
        bundleSize: 0,
        contributorIds: Object.keys(bundle.collections),
        isSessionPristine: opts?.isSessionPristine ?? false,
      }
      return meta
    },
    deleteLocal: async () => {},
    compact: async () => ({ kept: [], evicted: [] }),
    clear: async () => {},
  }
  setSnapshotStore(stub)
  return calls
}

function getFileInput(): HTMLInputElement {
  const el = document.querySelector('input[type="file"]')
  if (!el) throw new Error('file input not found')
  return el as HTMLInputElement
}

function makeValidBundleJson(overrides?: Partial<SyncBundle>): string {
  const bundle: SyncBundle = {
    version: 1,
    timestamp: 1_000,
    device: 'import-src',
    collections: {},
    ...overrides,
  }
  return JSON.stringify(bundle)
}

describe('SyncSection pre-import snapshot', () => {
  beforeEach(() => {
    resetStores()
    vi.restoreAllMocks()
  })
  afterEach(() => {
    setSnapshotStore(null)
  })

  it('calls createPreOperationSnapshot before applyImport when valid JSON file chosen', async () => {
    const calls = installSnapshotSpy()
    useSyncStore.getState().setActiveProvider('file')

    // Track relative order of snapshot vs applyImport.
    const applyOrder: string[] = []
    const origCreate = useSyncStore.getState().createPreOperationSnapshot
    const wrapped: typeof origCreate = async (trigger) => {
      applyOrder.push(`snapshot:${trigger}`)
      return origCreate(trigger)
    }
    useSyncStore.setState({ createPreOperationSnapshot: wrapped })

    vi.spyOn(syncActionsModule, 'applyImport').mockImplementation(async ({ bundle }) => {
      applyOrder.push('applyImport')
      return { kind: 'ok', appliedBundle: bundle }
    })

    render(<SyncSection />)

    const file = new File([makeValidBundleJson()], 'bundle.purdex-sync', { type: 'application/json' })
    const input = getFileInput()
    Object.defineProperty(input, 'files', { value: [file], configurable: true })
    fireEvent.change(input)

    await waitFor(() => {
      expect(calls.length).toBe(1)
    })
    expect(calls[0].trigger).toBe('pre-import')
    // Snapshot must precede applyImport in the recorded order.
    const snapIdx = applyOrder.indexOf('snapshot:pre-import')
    const applyIdx = applyOrder.indexOf('applyImport')
    expect(snapIdx).toBeGreaterThanOrEqual(0)
    expect(applyIdx).toBeGreaterThan(snapIdx)
  })

  it('does NOT create pre-import snapshot for invalid JSON', async () => {
    const calls = installSnapshotSpy()
    useSyncStore.getState().setActiveProvider('file')

    const applySpy = vi
      .spyOn(syncActionsModule, 'applyImport')
      .mockResolvedValue({ kind: 'ok', appliedBundle: { version: 1, timestamp: 0, device: 'x', collections: {} } })

    render(<SyncSection />)

    const file = new File(['not-json{'], 'broken.purdex-sync', { type: 'application/json' })
    const input = getFileInput()
    Object.defineProperty(input, 'files', { value: [file], configurable: true })
    fireEvent.change(input)

    // Wait for error path to settle (busy flips back off).
    await waitFor(() => {
      expect(useSyncStore.getState()).toBeTruthy()
    })
    // Small tick for async error path to finish.
    await new Promise((r) => setTimeout(r, 0))

    expect(calls).toHaveLength(0)
    expect(applySpy).not.toHaveBeenCalled()
  })

  it('still applies the import when pre-import snapshot fails (G1: no hard dep)', async () => {
    useSyncStore.getState().setActiveProvider('file')

    // Force createPreOperationSnapshot to throw (e.g. IDB unavailable).
    const origCreate = useSyncStore.getState().createPreOperationSnapshot
    useSyncStore.setState({
      createPreOperationSnapshot: (async () => { throw new Error('IDB unavailable') }) as typeof origCreate,
    })

    const applySpy = vi.spyOn(syncActionsModule, 'applyImport').mockResolvedValue({
      kind: 'ok',
      appliedBundle: { version: 1, timestamp: 0, device: 'x', collections: {} },
    })

    render(<SyncSection />)

    const file = new File([makeValidBundleJson()], 'bundle.purdex-sync', { type: 'application/json' })
    const input = getFileInput()
    Object.defineProperty(input, 'files', { value: [file], configurable: true })
    fireEvent.change(input)

    await waitFor(() => {
      expect(applySpy).toHaveBeenCalled()
    })
  })
})

describe('SyncSection post-sync snapshot', () => {
  beforeEach(() => {
    resetStores()
    vi.restoreAllMocks()
  })
  afterEach(() => {
    setSnapshotStore(null)
  })

  it('creates manual snapshot when syncNow succeeds with changed bundle', async () => {
    const calls = installSnapshotSpy()
    useSyncStore.getState().setActiveProvider('daemon')
    useSyncStore.getState().setSyncHostId('h1')
    useHostStore.setState({
      hosts: { h1: { id: 'h1', name: 'mini', ip: '127.0.0.1', port: 7860 } as never },
      hostOrder: ['h1'],
    })

    const bundleA: SyncBundle = {
      version: 1,
      timestamp: 1000,
      device: 'A',
      collections: { prefs: { version: 1, data: { theme: 'light' } } },
    }
    const bundleB: SyncBundle = {
      version: 1,
      timestamp: 2000,
      device: 'A',
      collections: { prefs: { version: 1, data: { theme: 'dark' } } },
    }
    useSyncStore.setState({ lastSyncedBundle: bundleA })

    vi.spyOn(syncActionsModule, 'syncNow').mockResolvedValue({ kind: 'ok', appliedBundle: bundleB })

    render(<SyncSection />)
    fireEvent.click(screen.getByRole('button', { name: /Sync Now|立即同步/i }))

    await waitFor(() => {
      expect(calls.length).toBe(1)
    })
    expect(calls[0].trigger).toBe('manual')
    expect(calls[0].bundle).toEqual(bundleB)
  })

  it('skips snapshot when returned bundle is equalExceptEnvelope to lastSyncedBundle', async () => {
    const calls = installSnapshotSpy()
    useSyncStore.getState().setActiveProvider('daemon')
    useSyncStore.getState().setSyncHostId('h1')
    useHostStore.setState({
      hosts: { h1: { id: 'h1', name: 'mini', ip: '127.0.0.1', port: 7860 } as never },
      hostOrder: ['h1'],
    })

    const bundleA: SyncBundle = {
      version: 1,
      timestamp: 1000,
      device: 'A',
      collections: { prefs: { version: 1, data: { theme: 'light' } } },
    }
    // Same collections, different envelope fields (timestamp + device).
    const bundleAPrime: SyncBundle = {
      version: 1,
      timestamp: 9999,
      device: 'B',
      collections: { prefs: { version: 1, data: { theme: 'light' } } },
    }
    useSyncStore.setState({ lastSyncedBundle: bundleA })

    vi.spyOn(syncActionsModule, 'syncNow').mockResolvedValue({ kind: 'ok', appliedBundle: bundleAPrime })

    render(<SyncSection />)
    fireEvent.click(screen.getByRole('button', { name: /Sync Now|立即同步/i }))

    // Wait for syncNow to complete (lastSyncedBundle updates).
    await waitFor(() => {
      expect(useSyncStore.getState().lastSyncedBundle).toEqual(bundleAPrime)
    })
    // Give fire-and-forget a tick in case it was queued.
    await new Promise((r) => setTimeout(r, 0))

    expect(calls).toHaveLength(0)
  })
})
