import { describe, it, expect, beforeEach, vi } from 'vitest'
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { DeviceStateRow } from './DeviceStateRow'
import * as apiModule from '../../../lib/device-state/api'
import type { DeviceStateRecord, DeviceStateSummary } from '../../../lib/device-state/api'
import type { WorkspaceSnapshot } from '../../../lib/snapshot/types'
import type { PaneContent, Tab } from '../../../types/tab'

vi.mock('../../../lib/device-state/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../../lib/device-state/api')>()),
  listDeviceStates: vi.fn(),
  getDeviceState: vi.fn(),
  deleteDeviceState: vi.fn(),
}))

const mockedGet = vi.mocked(apiModule.getDeviceState)

function leafTab(id: string, content: PaneContent): Tab {
  return { id, pinned: false, locked: false, createdAt: 0, layout: { type: 'leaf', pane: { id: `${id}-p`, content } } }
}

const SUMMARY: DeviceStateSummary = {
  clientId: 'c_bbbbbbbbbbbb',
  deviceName: 'Air',
  appVersion: '1.0.0-alpha.354',
  capturedAt: 1,
  updatedAt: Date.now() - 120_000,
  workspaceCount: 2,
  tabCount: 3,
}

const PAYLOAD: WorkspaceSnapshot = {
  version: 1,
  capturedAt: 1,
  tabs: {
    t1: leafTab('t1', { kind: 'tmux-session', hostId: 'h1', sessionCode: 'x1', mode: 'terminal', cachedName: 'dev', tmuxInstance: '' }),
    t2: leafTab('t2', { kind: 'browser', url: 'https://example.com/a' }),
    t3: leafTab('t3', { kind: 'tmux-session', hostId: 'h1', sessionCode: 'x3', mode: 'terminal', cachedName: 'loose', tmuxInstance: '' }),
  },
  tabOrder: ['t1', 't2', 't3'],
  activeTabId: 't1',
  workspaces: [
    { id: 'w1', name: 'Work', tabs: ['t1', 't2'], activeTabId: 't1' },
    { id: 'w2', name: 'Empty', tabs: [], activeTabId: null },
  ],
  activeWorkspaceId: 'w1',
  sessionMeta: {},
}

const RECORD: DeviceStateRecord = { ...SUMMARY, payload: PAYLOAD }

interface Props {
  isOwn?: boolean
  busy?: boolean
  replaceLocked?: boolean
  onReplace?: (load: () => Promise<DeviceStateRecord>) => void
  onDelete?: (clientId: string) => void
}

function renderRow(p: Props = {}) {
  const onReplace = p.onReplace ?? vi.fn()
  const onDelete = p.onDelete ?? vi.fn()
  render(
    <DeviceStateRow
      hostId="h1"
      summary={SUMMARY}
      isOwn={p.isOwn ?? false}
      busy={p.busy ?? false}
      replaceLocked={p.replaceLocked ?? false}
      onReplace={onReplace}
      onDelete={onDelete}
    />,
  )
  return { onReplace, onDelete }
}

const id = SUMMARY.clientId

beforeEach(() => {
  mockedGet.mockReset()
})

describe('DeviceStateRow', () => {
  it('renders name, version, counts and relative time without the own badge', () => {
    renderRow()
    const row = screen.getByTestId(`device-state-row-${id}`)
    expect(row).toHaveAttribute('data-own', 'false')
    expect(row.textContent).toContain('Air')
    expect(row.textContent).toContain('1.0.0-alpha.354')
    expect(screen.getByTestId(`device-state-counts-${id}`)).toHaveAttribute('data-workspaces', '2')
    expect(screen.getByTestId(`device-state-counts-${id}`)).toHaveAttribute('data-tabs', '3')
    expect(screen.getByTestId(`device-state-counts-${id}`).textContent).toContain('3')
    expect(screen.getByTestId(`device-state-updated-${id}`).textContent?.trim()).not.toBe('')
    expect(row.textContent).not.toContain('{{')
    expect(screen.queryByTestId(`device-state-own-badge-${id}`)).toBeNull()
  })

  it('shows the own badge and disables Delete on the own row', () => {
    renderRow({ isOwn: true })
    expect(screen.getByTestId(`device-state-own-badge-${id}`)).toBeInTheDocument()
    expect(screen.getByTestId(`device-state-delete-${id}`)).toBeDisabled()
    expect(screen.getByTestId(`device-state-replace-${id}`)).not.toBeDisabled()
  })

  it('expand lazy-loads the record once and renders workspace groups plus the no-workspace group', async () => {
    mockedGet.mockResolvedValue(RECORD)
    renderRow()
    expect(mockedGet).not.toHaveBeenCalled()

    fireEvent.click(screen.getByTestId(`device-state-expand-${id}`))
    await screen.findByTestId('device-state-ws-w1')
    expect(mockedGet).toHaveBeenCalledTimes(1)
    expect(mockedGet).toHaveBeenCalledWith('h1', id)

    expect(screen.getByTestId('device-state-ws-w2')).toBeInTheDocument()
    expect(screen.getByTestId('device-state-tab-t1').textContent).toContain('dev')
    expect(screen.getByTestId('device-state-tab-t2').textContent).toContain('example.com')
    const loose = screen.getByTestId('device-state-no-ws')
    expect(loose.textContent).toContain('loose')
    expect(loose.textContent).not.toContain('dev')

    // collapse + re-expand: no refetch
    fireEvent.click(screen.getByTestId(`device-state-expand-${id}`))
    expect(screen.queryByTestId('device-state-ws-w1')).toBeNull()
    fireEvent.click(screen.getByTestId(`device-state-expand-${id}`))
    expect(screen.getByTestId('device-state-ws-w1')).toBeInTheDocument()
    expect(mockedGet).toHaveBeenCalledTimes(1)
  })

  it('omits the no-workspace group when every tab belongs to a workspace', async () => {
    mockedGet.mockResolvedValue({ ...RECORD, payload: { ...PAYLOAD, tabOrder: ['t1', 't2'] } })
    renderRow()
    fireEvent.click(screen.getByTestId(`device-state-expand-${id}`))
    await screen.findByTestId('device-state-ws-w1')
    expect(screen.queryByTestId('device-state-no-ws')).toBeNull()
  })

  it('shows an inline error when the record cannot be loaded', async () => {
    mockedGet.mockRejectedValue(new Error('boom-404'))
    renderRow()
    fireEvent.click(screen.getByTestId(`device-state-expand-${id}`))
    const err = await screen.findByTestId(`device-state-row-error-${id}`)
    expect(err.textContent).toContain('boom-404')
  })

  it('Replace asks for confirmation; Cancel does not call onReplace', () => {
    const { onReplace } = renderRow()
    fireEvent.click(screen.getByTestId(`device-state-replace-${id}`))
    expect(screen.getByTestId(`device-state-replace-confirm-${id}`)).toBeInTheDocument()
    fireEvent.click(screen.getByTestId(`device-state-cancel-${id}`))
    expect(onReplace).not.toHaveBeenCalled()
    expect(screen.queryByTestId(`device-state-replace-confirm-${id}`)).toBeNull()
  })

  it('Replace Confirm hands a loader that fetches the record', async () => {
    mockedGet.mockResolvedValue(RECORD)
    const { onReplace } = renderRow()
    fireEvent.click(screen.getByTestId(`device-state-replace-${id}`))
    fireEvent.click(screen.getByTestId(`device-state-confirm-${id}`))
    expect(onReplace).toHaveBeenCalledTimes(1)
    const load = vi.mocked(onReplace).mock.calls[0][0]
    let rec: DeviceStateRecord | undefined
    await act(async () => {
      rec = await load()
    })
    expect(rec).toBe(RECORD)
    expect(mockedGet).toHaveBeenCalledWith('h1', id)
  })

  it('Replace never reuses the expand cache: a confirmed Replace fetches a fresh record', async () => {
    const newer: DeviceStateRecord = {
      ...RECORD,
      updatedAt: RECORD.updatedAt + 1000,
      payload: { ...PAYLOAD, workspaces: [{ id: 'wB', name: 'Newer', tabs: [], activeTabId: null }] },
    }
    mockedGet.mockResolvedValueOnce(RECORD).mockResolvedValueOnce(newer)
    const { onReplace } = renderRow()
    fireEvent.click(screen.getByTestId(`device-state-expand-${id}`))
    await screen.findByTestId('device-state-ws-w1')
    expect(mockedGet).toHaveBeenCalledTimes(1)

    fireEvent.click(screen.getByTestId(`device-state-replace-${id}`))
    fireEvent.click(screen.getByTestId(`device-state-confirm-${id}`))
    const load = vi.mocked(onReplace).mock.calls[0][0]
    let rec: DeviceStateRecord | undefined
    await act(async () => {
      rec = await load()
    })
    expect(mockedGet).toHaveBeenCalledTimes(2)
    expect(rec).toBe(newer)
  })

  it('Delete asks for confirmation; Confirm calls onDelete, Cancel does not', () => {
    const { onDelete } = renderRow()
    fireEvent.click(screen.getByTestId(`device-state-delete-${id}`))
    expect(screen.getByTestId(`device-state-delete-confirm-${id}`)).toBeInTheDocument()
    fireEvent.click(screen.getByTestId(`device-state-cancel-${id}`))
    expect(onDelete).not.toHaveBeenCalled()
    fireEvent.click(screen.getByTestId(`device-state-delete-${id}`))
    fireEvent.click(screen.getByTestId(`device-state-confirm-${id}`))
    expect(onDelete).toHaveBeenCalledWith(id)
  })

  it('disables Replace under a foreign lock and both actions while busy', async () => {
    renderRow({ replaceLocked: true })
    expect(screen.getByTestId(`device-state-replace-${id}`)).toBeDisabled()
    expect(screen.getByTestId(`device-state-delete-${id}`)).not.toBeDisabled()
  })

  it('disables both actions while busy', async () => {
    renderRow({ busy: true })
    await waitFor(() => expect(screen.getByTestId(`device-state-replace-${id}`)).toBeDisabled())
    expect(screen.getByTestId(`device-state-delete-${id}`)).toBeDisabled()
  })
})
