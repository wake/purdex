// spa/src/components/hosts/SnapshotsSection.records.test.tsx
//
// Task 16 — the per-tab rebuild records table and "Rebuild all" (spec §4.11).
//
// The records block reads `useTabStore` directly rather than the captured
// snapshot: it is a view over the live per-tab records, so it renders with or
// without a snapshot. `runBatchRebuild` / `rebuildPane` are stubbed through
// partial mocks so the grouping and the conflict rendering stay real.
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { render, screen, waitFor, fireEvent, act } from '@testing-library/react'
import { SnapshotsSection } from './SnapshotsSection'
import { useRebuildStore } from '../../stores/useRebuildStore'
import { useHostStore } from '../../stores/useHostStore'
import { emptyHostConfigEntry, useHostConfigStore } from '../../stores/useHostConfigStore'
import { useTabStore } from '../../stores/useTabStore'
import * as storageModule from '../../lib/snapshot/storage'
import * as hostApiModule from '../../lib/host-api'
import * as batchModule from '../../lib/rebuild/batch'
import * as engineModule from '../../lib/rebuild/engine'
import type { Session } from '../../lib/host-api'
import type { WorkspaceSnapshot } from '../../lib/snapshot/types'
import type { Tab, TmuxSessionContent } from '../../types/tab'

vi.mock('../../lib/snapshot/storage', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../lib/snapshot/storage')>()),
  readSnapshot: vi.fn(),
  writeSnapshot: vi.fn(),
  readPrevSnapshot: vi.fn(),
  writePrevSnapshot: vi.fn(),
}))
vi.mock('../../lib/host-api')
vi.mock('../../lib/snapshot/capture')
vi.mock('../../lib/snapshot/restore')
// Partial mocks: only the two actions that talk to a daemon are replaced.
vi.mock('../../lib/rebuild/batch', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../lib/rebuild/batch')>()),
  runBatchRebuild: vi.fn(),
}))
vi.mock('../../lib/rebuild/engine', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../lib/rebuild/engine')>()),
  rebuildPane: vi.fn(),
}))
vi.mock('../settings/device-state/DeviceStateSection', () => ({
  DeviceStateSection: () => <div data-testid="device-state-section" />,
}))

const mockedReadSnapshot = vi.mocked(storageModule.readSnapshot)
const mockedReadPrev = vi.mocked(storageModule.readPrevSnapshot)
const mockedListSessions = vi.mocked(hostApiModule.listSessions)
const mockedRunBatch = vi.mocked(batchModule.runBatchRebuild)
const mockedRebuildPane = vi.mocked(engineModule.rebuildPane)

function session(over: Partial<Session> & Pick<Session, 'code' | 'name'>): Session {
  return { cwd: '/tmp', mode: 'terminal', ...over }
}

function recordTab(tabId: string, paneId: string, over: Partial<TmuxSessionContent> = {}): Tab {
  const base: TmuxSessionContent = {
    kind: 'tmux-session',
    hostId: 'h1',
    sessionCode: 'old111',
    mode: 'terminal',
    cachedName: 'dev',
    tmuxInstance: '111:1000',
    terminated: 'tmux-restarted',
    rebuild: { sessionName: 'dev', tmuxInstance: '111:1000', cwd: '/w', capturedAt: 1 },
  }
  return {
    id: tabId, pinned: false, locked: false, createdAt: 0,
    layout: { type: 'leaf', pane: { id: paneId, content: { ...base, ...over } } },
  }
}

function seedTabs(...tabs: Tab[]) {
  useTabStore.setState({
    tabs: Object.fromEntries(tabs.map((tab) => [tab.id, tab])),
    tabOrder: tabs.map((tab) => tab.id),
    activeTabId: tabs[0]?.id ?? null,
  })
}

/** A captured snapshot, only needed by the legacy-labelling case. */
function snapWithData(): WorkspaceSnapshot {
  return {
    version: 1, capturedAt: Date.now(), tabs: {}, tabOrder: [], activeTabId: null,
    workspaces: [], activeWorkspaceId: null,
    sessionMeta: { h1: { s1: { hostId: 'h1', sessionCode: 's1', name: 'work', mode: 'terminal', restorable: true, cwd: '/x' } } },
  }
}

describe('SnapshotsSection — per-tab rebuild records (T16)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    useRebuildStore.setState({ operations: {}, lockedBy: null })
    useHostConfigStore.setState({ byHost: {} })
    useHostStore.setState({
      hosts: {
        h1: { id: 'h1', name: 'mlab', ip: '1.2.3.4', port: 7860, order: 0 },
        h2: { id: 'h2', name: 'air', ip: '5.6.7.8', port: 7860, order: 1 },
      },
      hostOrder: ['h1', 'h2'], devHostId: 'h1', runtime: {},
    })
    mockedReadSnapshot.mockReturnValue(null)
    mockedReadPrev.mockReturnValue(null)
    mockedListSessions.mockResolvedValue([])
    mockedRunBatch.mockResolvedValue({ status: 'ok', groups: [], excluded: [] })
    mockedRebuildPane.mockResolvedValue({
      hostId: 'h1',
      steps: { create: { status: 'ok' }, resume: { status: 'skipped' }, repoint: { status: 'ok' } },
      repointed: true,
    })
    seedTabs()
  })

  it('renders one row per terminal tmux pane, with the section four-state health indicator', async () => {
    seedTabs(
      recordTab('t1', 'p1'),
      recordTab('t2', 'p2', { sessionCode: 'live1', cachedName: 'alive', terminated: undefined,
        rebuild: { sessionName: 'alive', tmuxInstance: '111:1000', cwd: '/w', capturedAt: 1 } }),
    )
    mockedListSessions.mockResolvedValue([session({ code: 'live1', name: 'alive' })])

    render(<SnapshotsSection hostId="h1" />)
    await waitFor(() => {
      expect(screen.getByTestId('record-health-p1').getAttribute('data-health')).toBe('dead')
    })
    expect(screen.getByTestId('record-health-p2').getAttribute('data-health')).toBe('live')
    // The same labels the captured-snapshot table above it uses.
    expect(screen.getByTestId('record-health-p1').textContent).toBe('Rebuildable')
  })

  // The command column is RESOLVED, not stored (spec §4.2). A row that read it
  // off the record through an unsubscribed path would render the right string
  // on first paint and a stale one after the user edits the template.
  it('renders the resolved command, and re-renders it when the template changes', async () => {
    seedTabs(recordTab('t1', 'p1', {
      rebuild: {
        sessionName: 'dev', tmuxInstance: '111:1000', cwd: '/w', capturedAt: 1,
        agent: { type: 'cc', sessionId: 'S1', updatedAt: 1 },
      },
    }))
    render(<SnapshotsSection hostId="h1" />)
    await waitFor(() => expect(screen.getByText('claude --resume S1')).toBeInTheDocument())

    // The row's host (h1) loads its config, then an override is edited there.
    act(() => { useHostConfigStore.setState({ byHost: { h1: emptyHostConfigEntry('ready') } }) })
    expect(screen.getByText('claude --resume S1')).toBeInTheDocument()
    act(() => {
      useHostConfigStore.setState((s) => ({ byHost: { h1: { ...s.byHost.h1, resumeTemplates: {
        cc: { exact: 'cld-yolo --resume {id}', fallback: 'claude -c' },
      } } } }))
    })
    expect(screen.getByText('cld-yolo --resume S1')).toBeInTheDocument()
    expect(screen.queryByText('claude --resume S1')).toBeNull()
  })

  it('an unreachable host greys every record row out, exactly as the snapshot table does', async () => {
    seedTabs(recordTab('t1', 'p1'))
    mockedListSessions.mockRejectedValue(new Error('offline'))

    render(<SnapshotsSection hostId="h1" />)
    await waitFor(() => {
      expect(screen.getByTestId('record-health-p1').getAttribute('data-health')).toBe('offline')
    })
  })

  it('a record with no cwd is structure-only, never rebuildable', async () => {
    seedTabs(recordTab('t1', 'p1', {
      rebuild: { sessionName: 'dev', tmuxInstance: '111:1000', capturedAt: 1 },
    }))
    render(<SnapshotsSection hostId="h1" />)
    await waitFor(() => {
      expect(screen.getByTestId('record-health-p1').getAttribute('data-health')).toBe('structure')
    })
    // ⚠️ and "Rebuild all" must mean the same thing: the batch would otherwise
    // create a session for a record that cannot say where it belongs.
    expect((screen.getByTestId('record-rebuild-all-btn') as HTMLButtonElement).disabled).toBe(true)
    expect(screen.queryByTestId('record-attention-rebuild-p1')).toBeNull()
  })

  it('a dead pane whose code and name were reused by a new session is not live', async () => {
    // The generation moved (111 → 222) but tmux minted the same code for a
    // session the user gave the same name. Comparing code+name alone says
    // 🟢 live while the batch, which reads `terminated`, rebuilds the row.
    seedTabs(recordTab('t1', 'p1'))
    mockedListSessions.mockResolvedValue([
      session({ code: 'old111', name: 'dev', tmux_instance: '222:2000' }),
    ])

    render(<SnapshotsSection hostId="h1" />)
    await waitFor(() => {
      expect(screen.getByTestId('record-health-p1').getAttribute('data-health')).toBe('dead')
    })
    expect((screen.getByTestId('record-rebuild-all-btn') as HTMLButtonElement).disabled).toBe(false)
  })

  it('names the winning pane before running when hand-edits conflict', async () => {
    seedTabs(
      recordTab('t1', 'p1', { rebuild: { sessionName: 'dev', tmuxInstance: '111:1000', cwd: '/a', capturedAt: 1 } }),
      recordTab('t2', 'p2', { rebuild: { sessionName: 'dev', tmuxInstance: '111:1000', cwd: '/b', capturedAt: 9 } }),
    )
    render(<SnapshotsSection hostId="h1" />)
    const conflict = await screen.findByTestId('batch-conflict-source')
    expect(conflict.textContent).toContain('p2')
    expect(conflict.textContent).toContain('/b')
  })

  it('says nothing about conflicts when the group agrees', () => {
    seedTabs(recordTab('t1', 'p1'), recordTab('t2', 'p2'))
    render(<SnapshotsSection hostId="h1" />)
    expect(screen.queryByTestId('batch-conflict-source')).toBeNull()
  })

  it('"Rebuild all" runs the batch once and reports what it did', async () => {
    seedTabs(recordTab('t1', 'p1'), recordTab('t2', 'p2'))
    mockedRunBatch.mockResolvedValue({
      status: 'ok',
      excluded: [],
      groups: [{
        sourcePaneId: 'p1', hostId: 'h1', sessionCode: 'old111', tmuxInstance: '111:1000',
        report: {
          hostId: 'h1',
          created: { code: 'new1', name: 'dev', tmuxInstance: '222:2000' },
          steps: { create: { status: 'ok' }, resume: { status: 'ok' }, repoint: { status: 'ok' } },
          repointed: true,
        },
        members: [{ paneId: 'p2', tabId: 't2', repointed: true }],
      }],
    })

    render(<SnapshotsSection hostId="h1" />)
    fireEvent.click(screen.getByTestId('record-rebuild-all-btn'))

    await waitFor(() => {
      expect(mockedRunBatch).toHaveBeenCalledTimes(1)
    })
    expect(mockedRunBatch).toHaveBeenCalledWith({}, { hostId: 'h1' })
    await waitFor(() => {
      expect(screen.getByTestId('snapshot-status').getAttribute('data-tone')).toBe('success')
    })
  })

  it('lists an unknown-generation pane under "needs attention" with its own Rebuild', async () => {
    seedTabs(recordTab('t1', 'p1'), recordTab('t2', 'p2', { tmuxInstance: '' }))
    render(<SnapshotsSection hostId="h1" />)

    // Excluded from the automatic batch…
    expect(screen.queryByTestId('record-attention-rebuild-p1')).toBeNull()
    // …and offered one at a time instead.
    fireEvent.click(screen.getByTestId('record-attention-rebuild-p2'))
    await waitFor(() => {
      expect(mockedRebuildPane).toHaveBeenCalledTimes(1)
    })
    expect(mockedRebuildPane.mock.calls[0].slice(0, 3)).toEqual(['h1', 't2', 'p2'])
  })

  // The single rebuild plans, then waits for the host's config — exactly the
  // window the batch path guards against by handing the engine the binding it
  // planned from. Without it the engine has nothing to compare the pane
  // against and would rebuild whatever the pane holds when the wait ends.
  it('carries the binding it planned from across the host-config wait', async () => {
    seedTabs(recordTab('t1', 'p1'), recordTab('t2', 'p2', { tmuxInstance: '' }))
    let release!: () => void
    const ensureLoaded = vi.fn(() => new Promise<void>((resolve) => { release = resolve }))
    useHostConfigStore.setState({ ensureLoaded })

    render(<SnapshotsSection hostId="h1" />)
    fireEvent.click(screen.getByTestId('record-attention-rebuild-p2'))
    await waitFor(() => expect(ensureLoaded).toHaveBeenCalledWith('h1'))

    // The pane is re-pointed onto another session while the load is in flight.
    act(() => {
      seedTabs(recordTab('t1', 'p1'), recordTab('t2', 'p2', { tmuxInstance: '', sessionCode: 'new999' }))
    })
    await act(async () => { release() })

    await waitFor(() => expect(mockedRebuildPane).toHaveBeenCalledTimes(1))
    expect(mockedRebuildPane.mock.calls[0][4]).toMatchObject({
      expectedBinding: { hostId: 'h1', sessionCode: 'old111', tmuxInstance: '' },
    })
  })

  it('labels the legacy snapshot actions shell-only', () => {
    mockedReadSnapshot.mockReturnValue(snapWithData())
    render(<SnapshotsSection hostId="h1" />)
    expect(screen.getByTestId('snapshot-legacy-shell-only').textContent).toMatch(/shell/i)
  })

  it('disables "Rebuild all" while another owner holds the operation lock', () => {
    seedTabs(recordTab('t1', 'p1'))
    useRebuildStore.getState().acquireOperationLock('rebuild:p9')
    render(<SnapshotsSection hostId="h1" />)
    expect((screen.getByTestId('record-rebuild-all-btn') as HTMLButtonElement).disabled).toBe(true)
  })

  it('a blocked batch reports who is holding the lock instead of failing silently', async () => {
    seedTabs(recordTab('t1', 'p1'))
    mockedRunBatch.mockResolvedValue({ status: 'blocked', blockedBy: 'rebuild:p9', groups: [], excluded: [] })
    render(<SnapshotsSection hostId="h1" />)
    fireEvent.click(screen.getByTestId('record-rebuild-all-btn'))
    await waitFor(() => {
      expect(screen.getByTestId('snapshot-status').getAttribute('data-tone')).toBe('warn')
    })
    expect(screen.getByTestId('snapshot-status').textContent).toContain('rebuild:p9')
  })

  it('shows only this host\'s record rows', () => {
    seedTabs(recordTab('t1', 'p1'), recordTab('t2', 'p2', { hostId: 'h2' }))
    render(<SnapshotsSection hostId="h1" />)
    expect(screen.getByTestId('record-health-p1')).toBeInTheDocument()
    expect(screen.queryByTestId('record-health-p2')).toBeNull()
  })

  it('lists sessions only for this host even when other hosts have record rows', async () => {
    seedTabs(recordTab('t1', 'p1'), recordTab('t2', 'p2', { hostId: 'h2' }))
    render(<SnapshotsSection hostId="h1" />)
    await waitFor(() => expect(mockedListSessions).toHaveBeenCalledTimes(1))
    expect(mockedListSessions).toHaveBeenCalledWith('h1')
  })
})
