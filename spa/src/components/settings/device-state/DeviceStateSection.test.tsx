import { describe, it, expect, beforeEach, vi } from 'vitest'
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { DeviceStateSection } from './DeviceStateSection'
import { useDeviceNameStore } from '../../../stores/useDeviceNameStore'
import { useDeviceStateStore } from '../../../stores/useDeviceStateStore'
import type { DeviceStateStatus } from '../../../stores/useDeviceStateStore'
import { useHostStore } from '../../../stores/useHostStore'
import { useRebuildStore } from '../../../stores/useRebuildStore'
import { STORAGE_KEYS } from '../../../lib/storage'
import * as apiModule from '../../../lib/device-state/api'
import type { DeviceStateRecord, DeviceStateSummary } from '../../../lib/device-state/api'
import * as restoreModule from '../../../lib/device-state/restore'
import type { DeviceStateMergeReport, DeviceStateRestoreReport } from '../../../lib/device-state/restore'
import { RestoreError } from '../../../lib/snapshot/types'
import type { WorkspaceSnapshot } from '../../../lib/snapshot/types'

vi.mock('../../../lib/device-state/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../../lib/device-state/api')>()),
  listDeviceStates: vi.fn(),
  getDeviceState: vi.fn(),
  deleteDeviceState: vi.fn(),
}))
vi.mock('../../../lib/device-state/restore', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../../lib/device-state/restore')>()),
  restoreDeviceStateReplace: vi.fn(),
  restoreDeviceStateMerge: vi.fn(),
}))

const mockedList = vi.mocked(apiModule.listDeviceStates)
const mockedGet = vi.mocked(apiModule.getDeviceState)
const mockedDelete = vi.mocked(apiModule.deleteDeviceState)
const mockedReplace = vi.mocked(restoreModule.restoreDeviceStateReplace)
const mockedMerge = vi.mocked(restoreModule.restoreDeviceStateMerge)

const OWN = 'c_aaaaaaaaaaaa'
const OTHER = 'c_bbbbbbbbbbbb'

function summary(clientId: string, over: Partial<DeviceStateSummary> = {}): DeviceStateSummary {
  return {
    clientId,
    deviceName: clientId === OWN ? 'Mini' : 'Air',
    appVersion: '1.0.0',
    capturedAt: 1,
    updatedAt: Date.now() - 5_000,
    workspaceCount: 1,
    tabCount: 2,
    ...over,
  }
}

const PAYLOAD: WorkspaceSnapshot = {
  version: 1,
  capturedAt: 1,
  tabs: {},
  tabOrder: [],
  activeTabId: null,
  workspaces: [],
  activeWorkspaceId: null,
  sessionMeta: {},
}

const REPORT: DeviceStateRestoreReport = {
  reattached: 2,
  rebuilt: 0,
  failed: 1,
  hostRemoved: 3,
  rebuiltButUnattached: [],
}

function deferred<T>() {
  let resolve!: (v: T) => void
  const promise = new Promise<T>((res) => {
    resolve = res
  })
  return { promise, resolve }
}

function withTarget() {
  useHostStore.setState({ devHostId: 'h1' })
}

function nameInput() {
  return screen.getByTestId('device-state-name') as HTMLInputElement
}

beforeEach(() => {
  useDeviceNameStore.setState({ deviceName: null, defaultDeviceName: 'Chrome · macOS' })
  useDeviceStateStore.setState({ status: { kind: 'idle' } })
  useHostStore.setState({
    hosts: { h1: { id: 'h1', name: 'Mini', ip: '100.64.0.2', port: 7860, order: 0 } },
    hostOrder: ['h1'],
    runtime: {},
    devHostId: null,
  })
  useRebuildStore.setState({ lockedBy: null, lockGrant: null })
  // The own id comes from lib/client-identity, whose truth is this storage key.
  localStorage.setItem(STORAGE_KEYS.CLIENT_IDENTITY, OWN)
  mockedList.mockReset()
  mockedGet.mockReset()
  mockedDelete.mockReset()
  mockedReplace.mockReset()
  mockedMerge.mockReset()
})

describe('DeviceStateSection', () => {
  it('renders a heading', () => {
    render(<DeviceStateSection />)
    expect(screen.getAllByRole('heading', { level: 3 }).length).toBeGreaterThan(0)
  })

  it('renders the device name field', () => {
    render(<DeviceStateSection />)
    expect(nameInput().value).toBe('Chrome · macOS')
    expect(nameInput()).toHaveAccessibleName()
  })

  it('shows "not set" when no dev host is selected', () => {
    render(<DeviceStateSection />)
    const target = screen.getByTestId('device-state-target')
    expect(target).toHaveAttribute('data-target', 'none')
    expect(target.textContent).not.toContain('Mini')
  })

  it('shows the dev host name as the target', () => {
    mockedList.mockResolvedValue([])
    useHostStore.setState({ devHostId: 'h1' })
    render(<DeviceStateSection />)
    const target = screen.getByTestId('device-state-target')
    expect(target).toHaveAttribute('data-target', 'h1')
    expect(target.textContent).toContain('Mini')
  })

  it('treats a dangling dev host id as not set', () => {
    useHostStore.setState({ devHostId: 'gone' })
    render(<DeviceStateSection />)
    expect(screen.getByTestId('device-state-target')).toHaveAttribute('data-target', 'none')
  })

  const kinds: DeviceStateStatus[] = [
    { kind: 'idle' },
    { kind: 'no-target' },
    { kind: 'offline', hostId: 'h1' },
    { kind: 'uploading', hostId: 'h1' },
    { kind: 'ok', at: Date.now(), hostId: 'h1' },
    { kind: 'error', message: 'boom-503', hostId: 'h1' },
  ]
  for (const status of kinds) {
    it(`renders status kind ${status.kind}`, () => {
      useDeviceStateStore.setState({ status })
      render(<DeviceStateSection />)
      const line = screen.getByTestId('device-state-status')
      expect(line).toHaveAttribute('data-kind', status.kind)
      expect(line.textContent?.trim()).not.toBe('')
      // Never leak raw interpolation placeholders.
      expect(line.textContent).not.toContain('{{')
      if (status.kind === 'error') expect(line.textContent).toContain('boom-503')
    })
  }
})

describe('DeviceStateSection — list', () => {
  it('does not fetch without a storage host', () => {
    render(<DeviceStateSection />)
    expect(mockedList).not.toHaveBeenCalled()
    expect(screen.queryByTestId('device-state-list')).toBeNull()
  })

  it('renders rows with the own badge, counts and delete disabled on the own row', async () => {
    withTarget()
    mockedList.mockResolvedValue([summary(OTHER), summary(OWN, { tabCount: 7 })])
    render(<DeviceStateSection />)
    expect(screen.getByTestId('device-state-list')).toHaveAttribute('data-state', 'loading')
    await screen.findByTestId(`device-state-row-${OWN}`)
    expect(mockedList).toHaveBeenCalledWith('h1')
    expect(screen.getByTestId('device-state-list')).toHaveAttribute('data-state', 'rows')
    expect(screen.getByTestId(`device-state-own-badge-${OWN}`)).toBeInTheDocument()
    expect(screen.queryByTestId(`device-state-own-badge-${OTHER}`)).toBeNull()
    expect(screen.getByTestId(`device-state-counts-${OWN}`)).toHaveAttribute('data-tabs', '7')
    expect(screen.getByTestId(`device-state-delete-${OWN}`)).toBeDisabled()
    expect(screen.getByTestId(`device-state-delete-${OTHER}`)).not.toBeDisabled()
  })

  it('resolves the own client id via getClientId when none is stored yet', async () => {
    withTarget()
    localStorage.removeItem(STORAGE_KEYS.CLIENT_IDENTITY)
    localStorage.removeItem(STORAGE_KEYS.SYNC_STATE)
    let generated: string | null = null
    mockedList.mockImplementation(async () => {
      generated = localStorage.getItem(STORAGE_KEYS.CLIENT_IDENTITY)
      return [summary(OTHER), summary(generated ?? 'missing')]
    })
    render(<DeviceStateSection />)
    await waitFor(() => expect(mockedList).toHaveBeenCalled())
    expect(generated).toBeTruthy()
    const own = generated as unknown as string
    await screen.findByTestId(`device-state-row-${own}`)
    expect(screen.getByTestId(`device-state-own-badge-${own}`)).toBeInTheDocument()
    expect(screen.getByTestId(`device-state-delete-${own}`)).toBeDisabled()
    expect(screen.queryByTestId(`device-state-own-badge-${OTHER}`)).toBeNull()
  })

  it('renders the empty state', async () => {
    withTarget()
    mockedList.mockResolvedValue([])
    render(<DeviceStateSection />)
    await waitFor(() => expect(screen.getByTestId('device-state-list')).toHaveAttribute('data-state', 'empty'))
    expect(screen.getByTestId('device-state-list-empty')).toBeInTheDocument()
  })

  it('shows a list error inline and retries', async () => {
    withTarget()
    mockedList.mockRejectedValueOnce(new Error('boom-500')).mockResolvedValueOnce([summary(OTHER)])
    render(<DeviceStateSection />)
    const err = await screen.findByTestId('device-state-list-error')
    expect(err.textContent).toContain('boom-500')
    expect(err.textContent).not.toContain('{{')
    fireEvent.click(screen.getByTestId('device-state-list-retry'))
    await screen.findByTestId(`device-state-row-${OTHER}`)
    expect(mockedList).toHaveBeenCalledTimes(2)
  })

  it('refetches on Refresh click', async () => {
    withTarget()
    mockedList.mockResolvedValue([])
    render(<DeviceStateSection />)
    await waitFor(() => expect(mockedList).toHaveBeenCalledTimes(1))
    fireEvent.click(screen.getByTestId('device-state-list-refresh'))
    await waitFor(() => expect(mockedList).toHaveBeenCalledTimes(2))
  })

  it('refetches when devHostId changes and ignores the stale response', async () => {
    useHostStore.setState({
      hosts: {
        h1: { id: 'h1', name: 'Mini', ip: '100.64.0.2', port: 7860, order: 0 },
        h2: { id: 'h2', name: 'Air', ip: '100.64.0.4', port: 7860, order: 1 },
      },
      hostOrder: ['h1', 'h2'],
      devHostId: 'h1',
    })
    const first = deferred<DeviceStateSummary[]>()
    mockedList.mockImplementation((hostId) =>
      hostId === 'h1' ? first.promise : Promise.resolve([summary(OWN)]),
    )
    render(<DeviceStateSection />)
    act(() => useHostStore.setState({ devHostId: 'h2' }))
    await screen.findByTestId(`device-state-row-${OWN}`)
    expect(mockedList).toHaveBeenCalledWith('h2')
    await act(async () => {
      first.resolve([summary(OTHER)])
      await first.promise
    })
    expect(screen.queryByTestId(`device-state-row-${OTHER}`)).toBeNull()
    expect(screen.getByTestId(`device-state-row-${OWN}`)).toBeInTheDocument()
  })

  it('refetches when the uploader status becomes ok', async () => {
    withTarget()
    useDeviceStateStore.setState({ status: { kind: 'uploading', hostId: 'h1' } })
    mockedList.mockResolvedValue([])
    render(<DeviceStateSection />)
    await waitFor(() => expect(mockedList).toHaveBeenCalledTimes(1))
    act(() => useDeviceStateStore.setState({ status: { kind: 'ok', at: 123, hostId: 'h1' } }))
    await waitFor(() => expect(mockedList).toHaveBeenCalledTimes(2))
    // leaving ok does not refetch
    act(() => useDeviceStateStore.setState({ status: { kind: 'uploading', hostId: 'h1' } }))
    await act(async () => {})
    expect(mockedList).toHaveBeenCalledTimes(2)
  })

  it('delete confirm calls deleteDeviceState then refetches', async () => {
    withTarget()
    mockedList.mockResolvedValue([summary(OTHER)])
    mockedDelete.mockResolvedValue(undefined)
    render(<DeviceStateSection />)
    await screen.findByTestId(`device-state-row-${OTHER}`)
    fireEvent.click(screen.getByTestId(`device-state-delete-${OTHER}`))
    fireEvent.click(screen.getByTestId(`device-state-confirm-${OTHER}`))
    await waitFor(() => expect(mockedDelete).toHaveBeenCalledWith('h1', OTHER))
    await waitFor(() => expect(mockedList).toHaveBeenCalledTimes(2))
  })

  it('a delete failure is shown inline', async () => {
    withTarget()
    mockedList.mockResolvedValue([summary(OTHER)])
    mockedDelete.mockRejectedValue(new Error('boom-del'))
    render(<DeviceStateSection />)
    await screen.findByTestId(`device-state-row-${OTHER}`)
    fireEvent.click(screen.getByTestId(`device-state-delete-${OTHER}`))
    fireEvent.click(screen.getByTestId(`device-state-confirm-${OTHER}`))
    const line = await screen.findByTestId('device-state-action-status')
    expect(line).toHaveAttribute('data-tone', 'error')
    expect(line.textContent).toContain('boom-del')
  })
})

describe('DeviceStateSection — replace', () => {
  async function renderWithRow(onRestored = vi.fn()) {
    withTarget()
    mockedList.mockResolvedValue([summary(OTHER)])
    render(<DeviceStateSection onRestored={onRestored} />)
    await screen.findByTestId(`device-state-row-${OTHER}`)
    return onRestored
  }

  function confirmReplace() {
    fireEvent.click(screen.getByTestId(`device-state-replace-${OTHER}`))
    fireEvent.click(screen.getByTestId(`device-state-confirm-${OTHER}`))
  }

  it('confirm fetches the record and restores its payload; success shows counts', async () => {
    const record: DeviceStateRecord = { ...summary(OTHER), payload: PAYLOAD }
    mockedGet.mockResolvedValue(record)
    mockedReplace.mockResolvedValue(REPORT)
    const onRestored = await renderWithRow()
    confirmReplace()
    await waitFor(() => expect(mockedReplace).toHaveBeenCalledWith(PAYLOAD))
    expect(mockedGet).toHaveBeenCalledWith('h1', OTHER)
    await waitFor(() =>
      expect(screen.getByTestId('device-state-action-status')).toHaveAttribute('data-tone', 'success'),
    )
    const line = screen.getByTestId('device-state-action-status')
    expect(line).toHaveAttribute('data-reattached', '2')
    expect(line).toHaveAttribute('data-failed', '1')
    expect(line).toHaveAttribute('data-host-removed', '3')
    expect(line.textContent).not.toContain('{{')
    expect(onRestored).toHaveBeenCalledTimes(1)
  })

  it('after the list refreshes with a newer upload, Replace restores the newer payload', async () => {
    const payloadA: WorkspaceSnapshot = { ...PAYLOAD, workspaces: [{ id: 'wA', name: 'Old', tabs: [], activeTabId: null }] }
    const payloadB: WorkspaceSnapshot = { ...PAYLOAD, workspaces: [{ id: 'wB', name: 'New', tabs: [], activeTabId: null }] }
    const oldSummary = summary(OTHER, { updatedAt: 1_000 })
    const newSummary = summary(OTHER, { updatedAt: 2_000 })
    withTarget()
    mockedList.mockResolvedValueOnce([oldSummary]).mockResolvedValue([newSummary])
    mockedGet.mockResolvedValueOnce({ ...oldSummary, payload: payloadA }).mockResolvedValue({ ...newSummary, payload: payloadB })
    mockedReplace.mockResolvedValue(REPORT)
    render(<DeviceStateSection />)
    await screen.findByTestId(`device-state-row-${OTHER}`)

    fireEvent.click(screen.getByTestId(`device-state-expand-${OTHER}`))
    await screen.findByTestId('device-state-ws-wA')

    fireEvent.click(screen.getByTestId('device-state-list-refresh'))
    await waitFor(() => expect(mockedList).toHaveBeenCalledTimes(2))
    await waitFor(() => expect(screen.queryByTestId('device-state-ws-wA')).toBeNull())

    // The expanded tree for the new version is fetched again.
    fireEvent.click(screen.getByTestId(`device-state-expand-${OTHER}`))
    await screen.findByTestId('device-state-ws-wB')
    expect(mockedGet).toHaveBeenCalledTimes(2)

    confirmReplace()
    await waitFor(() => expect(mockedReplace).toHaveBeenCalledTimes(1))
    expect(mockedReplace).toHaveBeenCalledWith(payloadB)
    expect(mockedGet).toHaveBeenCalledTimes(3)
  })

  it('cancel does not restore', async () => {
    await renderWithRow()
    fireEvent.click(screen.getByTestId(`device-state-replace-${OTHER}`))
    fireEvent.click(screen.getByTestId(`device-state-cancel-${OTHER}`))
    expect(mockedGet).not.toHaveBeenCalled()
    expect(mockedReplace).not.toHaveBeenCalled()
  })

  it('RestoreError → error tone with hostRemoved read from the report; onRestored still called', async () => {
    mockedGet.mockResolvedValue({ ...summary(OTHER), payload: PAYLOAD })
    mockedReplace.mockRejectedValue(new RestoreError(REPORT, new Error('x')))
    const onRestored = await renderWithRow()
    confirmReplace()
    await waitFor(() =>
      expect(screen.getByTestId('device-state-action-status')).toHaveAttribute('data-tone', 'error'),
    )
    const line = screen.getByTestId('device-state-action-status')
    expect(line).toHaveAttribute('data-host-removed', '3')
    expect(line).toHaveAttribute('data-reattached', '2')
    expect(onRestored).toHaveBeenCalledTimes(1)
  })

  it('RestoreError without hostRemoved defaults to 0', async () => {
    mockedGet.mockResolvedValue({ ...summary(OTHER), payload: PAYLOAD })
    mockedReplace.mockRejectedValue(
      new RestoreError({ reattached: 0, rebuilt: 0, failed: 0, rebuiltButUnattached: [] }),
    )
    await renderWithRow()
    confirmReplace()
    await waitFor(() =>
      expect(screen.getByTestId('device-state-action-status')).toHaveAttribute('data-host-removed', '0'),
    )
  })

  it('a plain Error → error tone with its message', async () => {
    mockedGet.mockResolvedValue({ ...summary(OTHER), payload: PAYLOAD })
    mockedReplace.mockRejectedValue(new Error('refused-xyz'))
    const onRestored = await renderWithRow()
    confirmReplace()
    await waitFor(() =>
      expect(screen.getByTestId('device-state-action-status')).toHaveAttribute('data-tone', 'error'),
    )
    expect(screen.getByTestId('device-state-action-status').textContent).toContain('refused-xyz')
    expect(onRestored).toHaveBeenCalledTimes(1)
  })

  it('a record fetch failure → error tone and no restore', async () => {
    mockedGet.mockRejectedValue(new Error('get-404'))
    await renderWithRow()
    confirmReplace()
    await waitFor(() =>
      expect(screen.getByTestId('device-state-action-status')).toHaveAttribute('data-tone', 'error'),
    )
    expect(screen.getByTestId('device-state-action-status').textContent).toContain('get-404')
    expect(mockedReplace).not.toHaveBeenCalled()
  })

  it('single-flight: Replace is disabled while one runs', async () => {
    mockedGet.mockResolvedValue({ ...summary(OTHER), payload: PAYLOAD })
    const run = deferred<DeviceStateRestoreReport>()
    mockedReplace.mockReturnValue(run.promise)
    await renderWithRow()
    confirmReplace()
    await waitFor(() => expect(mockedReplace).toHaveBeenCalledTimes(1))
    expect(screen.getByTestId(`device-state-replace-${OTHER}`)).toBeDisabled()
    await act(async () => {
      run.resolve(REPORT)
      await run.promise
    })
    expect(mockedReplace).toHaveBeenCalledTimes(1)
    expect(screen.getByTestId(`device-state-replace-${OTHER}`)).not.toBeDisabled()
  })

  it('disables Replace under a foreign lock', async () => {
    useRebuildStore.setState({ lockedBy: 'snapshot:restoreAll' })
    await renderWithRow()
    expect(screen.getByTestId(`device-state-replace-${OTHER}`)).toBeDisabled()
    act(() => useRebuildStore.setState({ lockedBy: null }))
    expect(screen.getByTestId(`device-state-replace-${OTHER}`)).not.toBeDisabled()
  })
})

describe('DeviceStateSection — merge', () => {
  const MERGE_REPORT: DeviceStateMergeReport = { ...REPORT, addedWorkspaces: 4, addedTabs: 5, skippedTabs: 6 }

  async function renderWithRow(onRestored = vi.fn()) {
    withTarget()
    mockedList.mockResolvedValue([summary(OTHER)])
    render(<DeviceStateSection onRestored={onRestored} />)
    await screen.findByTestId(`device-state-row-${OTHER}`)
    return onRestored
  }

  function confirmMerge() {
    fireEvent.click(screen.getByTestId(`device-state-merge-${OTHER}`))
    fireEvent.click(screen.getByTestId(`device-state-confirm-${OTHER}`))
  }

  it('confirm fetches the record after confirming and merges its payload; success shows counts', async () => {
    const record: DeviceStateRecord = { ...summary(OTHER), payload: PAYLOAD }
    mockedGet.mockResolvedValue(record)
    mockedMerge.mockResolvedValue(MERGE_REPORT)
    const onRestored = await renderWithRow()
    fireEvent.click(screen.getByTestId(`device-state-merge-${OTHER}`))
    expect(mockedGet).not.toHaveBeenCalled()
    fireEvent.click(screen.getByTestId(`device-state-confirm-${OTHER}`))
    await waitFor(() => expect(mockedMerge).toHaveBeenCalledWith(PAYLOAD))
    expect(mockedGet).toHaveBeenCalledWith('h1', OTHER)
    expect(mockedReplace).not.toHaveBeenCalled()
    await waitFor(() =>
      expect(screen.getByTestId('device-state-action-status')).toHaveAttribute('data-tone', 'success'),
    )
    const line = screen.getByTestId('device-state-action-status')
    expect(line).toHaveAttribute('data-added-workspaces', '4')
    expect(line).toHaveAttribute('data-added-tabs', '5')
    expect(line).toHaveAttribute('data-skipped-tabs', '6')
    expect(line).toHaveAttribute('data-reattached', '2')
    expect(line).toHaveAttribute('data-failed', '1')
    expect(line).toHaveAttribute('data-host-removed', '3')
    expect(line.textContent).toContain('5')
    expect(line.textContent).not.toContain('{{')
    expect(onRestored).toHaveBeenCalledTimes(1)
  })

  it('cancel does not merge', async () => {
    await renderWithRow()
    fireEvent.click(screen.getByTestId(`device-state-merge-${OTHER}`))
    fireEvent.click(screen.getByTestId(`device-state-cancel-${OTHER}`))
    expect(mockedGet).not.toHaveBeenCalled()
    expect(mockedMerge).not.toHaveBeenCalled()
  })

  it('RestoreError → error tone; onRestored still called', async () => {
    mockedGet.mockResolvedValue({ ...summary(OTHER), payload: PAYLOAD })
    mockedMerge.mockRejectedValue(new RestoreError(REPORT, new Error('merge-broke')))
    const onRestored = await renderWithRow()
    confirmMerge()
    await waitFor(() =>
      expect(screen.getByTestId('device-state-action-status')).toHaveAttribute('data-tone', 'error'),
    )
    const line = screen.getByTestId('device-state-action-status')
    expect(line.textContent).toContain('merge-broke')
    expect(line).toHaveAttribute('data-host-removed', '3')
    expect(line).toHaveAttribute('data-added-tabs', '0')
    expect(onRestored).toHaveBeenCalledTimes(1)
  })

  it('a record fetch failure → error tone, no merge, no onRestored', async () => {
    mockedGet.mockRejectedValue(new Error('get-404'))
    const onRestored = await renderWithRow()
    confirmMerge()
    await waitFor(() =>
      expect(screen.getByTestId('device-state-action-status')).toHaveAttribute('data-tone', 'error'),
    )
    expect(mockedMerge).not.toHaveBeenCalled()
    expect(onRestored).not.toHaveBeenCalled()
  })

  it('disables Merge under a foreign lock (including the Replace owner)', async () => {
    useRebuildStore.setState({ lockedBy: 'snapshot:restoreAll' })
    await renderWithRow()
    expect(screen.getByTestId(`device-state-merge-${OTHER}`)).toBeDisabled()
    act(() => useRebuildStore.setState({ lockedBy: restoreModule.DEVICE_STATE_LOCK_OWNER.replace }))
    expect(screen.getByTestId(`device-state-merge-${OTHER}`)).toBeDisabled()
    act(() => useRebuildStore.setState({ lockedBy: null }))
    expect(screen.getByTestId(`device-state-merge-${OTHER}`)).not.toBeDisabled()
  })

  it('Replace and Merge share single-flight: Merge is disabled while Replace runs', async () => {
    mockedGet.mockResolvedValue({ ...summary(OTHER), payload: PAYLOAD })
    const run = deferred<DeviceStateRestoreReport>()
    mockedReplace.mockReturnValue(run.promise)
    mockedMerge.mockResolvedValue(MERGE_REPORT)
    await renderWithRow()
    fireEvent.click(screen.getByTestId(`device-state-replace-${OTHER}`))
    fireEvent.click(screen.getByTestId(`device-state-confirm-${OTHER}`))
    await waitFor(() => expect(mockedReplace).toHaveBeenCalledTimes(1))
    expect(screen.getByTestId(`device-state-merge-${OTHER}`)).toBeDisabled()
    fireEvent.click(screen.getByTestId(`device-state-merge-${OTHER}`))
    expect(screen.queryByTestId(`device-state-merge-confirm-${OTHER}`)).toBeNull()
    await act(async () => {
      run.resolve(REPORT)
      await run.promise
    })
    expect(mockedMerge).not.toHaveBeenCalled()
    expect(screen.getByTestId(`device-state-merge-${OTHER}`)).not.toBeDisabled()
  })

  it('Replace and Merge share single-flight: a Replace confirm while Merge runs is ignored', async () => {
    mockedGet.mockResolvedValue({ ...summary(OTHER), payload: PAYLOAD })
    const run = deferred<DeviceStateMergeReport>()
    mockedMerge.mockReturnValue(run.promise)
    await renderWithRow()
    fireEvent.click(screen.getByTestId(`device-state-merge-${OTHER}`))
    fireEvent.click(screen.getByTestId(`device-state-confirm-${OTHER}`))
    await waitFor(() => expect(mockedMerge).toHaveBeenCalledTimes(1))
    expect(screen.getByTestId(`device-state-replace-${OTHER}`)).toBeDisabled()
    await act(async () => {
      run.resolve(MERGE_REPORT)
      await run.promise
    })
    expect(mockedReplace).not.toHaveBeenCalled()
  })
})
