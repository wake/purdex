// spa/src/components/hosts/SnapshotsSection.records.test.tsx
//
// Task 16 — the per-tab rebuild records table and "Rebuild all" (spec §4.11).
//
// The records block reads `useTabStore` directly: it is a view over the live
// per-tab records. `runBatchRebuild` / `rebuildPane` are stubbed through
// partial mocks so the grouping and the conflict rendering stay real.
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { render, screen, waitFor, fireEvent, act } from '@testing-library/react'
import { SnapshotsSection } from './SnapshotsSection'
import { useRebuildStore } from '../../stores/useRebuildStore'
import { useHostStore } from '../../stores/useHostStore'
import { emptyHostConfigEntry, useHostConfigStore } from '../../stores/useHostConfigStore'
import { useTabStore } from '../../stores/useTabStore'
import * as hostApiModule from '../../lib/host-api'
import * as batchModule from '../../lib/rebuild/batch'
import * as engineModule from '../../lib/rebuild/engine'
import type { Session } from '../../lib/host-api'
import type { Tab, TmuxSessionContent } from '../../types/tab'

vi.mock('../../lib/host-api')
// Partial mocks: only the two actions that talk to a daemon are replaced.
vi.mock('../../lib/rebuild/batch', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../lib/rebuild/batch')>()),
  runBatchRebuild: vi.fn(),
}))
vi.mock('../../lib/rebuild/engine', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../lib/rebuild/engine')>()),
  rebuildPane: vi.fn(),
}))

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

  it('an unreachable host greys every record row out', async () => {
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

  // Both clicks land inside one act, before the re-render that would disable the
  // button, so only the single-flight guard (`busyRef`) can stop the second one.
  it('busy guard: two synchronous clicks on "Rebuild all" run the batch once', async () => {
    seedTabs(recordTab('t1', 'p1'))
    mockedRunBatch.mockReturnValue(new Promise(() => {}))
    render(<SnapshotsSection hostId="h1" />)
    const btn = screen.getByTestId('record-rebuild-all-btn')
    act(() => { btn.click(); btn.click() })
    await waitFor(() => expect(mockedRunBatch).toHaveBeenCalledTimes(1))
    await act(async () => {})
    expect(mockedRunBatch).toHaveBeenCalledTimes(1)
  })

  it('busy guard: two synchronous clicks on a single-row Rebuild run it once', async () => {
    seedTabs(recordTab('t1', 'p1'), recordTab('t2', 'p2', { tmuxInstance: '' }))
    const ensureLoaded = vi.fn(() => new Promise<void>(() => {}))
    useHostConfigStore.setState({ ensureLoaded })
    render(<SnapshotsSection hostId="h1" />)
    const btn = screen.getByTestId('record-attention-rebuild-p2')
    act(() => { btn.click(); btn.click() })
    await waitFor(() => expect(ensureLoaded).toHaveBeenCalledTimes(1))
    await act(async () => {})
    expect(ensureLoaded).toHaveBeenCalledTimes(1)
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

  // The lookup is keyed on the host plus a counter a rebuild bumps: a rebuild
  // creates sessions, so the health it showed before is stale once it ends.
  it('looks the host\'s sessions up again once a rebuild has run', async () => {
    seedTabs(recordTab('t1', 'p1'), recordTab('t2', 'p2', { tmuxInstance: '' }))
    // `beforeEach` resets `byHost` only; a case above leaves a never-settling `ensureLoaded`.
    useHostConfigStore.setState({ ensureLoaded: vi.fn(async () => {}) })
    render(<SnapshotsSection hostId="h1" />)
    await waitFor(() => expect(mockedListSessions).toHaveBeenCalledTimes(1))

    fireEvent.click(screen.getByTestId('record-rebuild-all-btn'))
    await waitFor(() => expect(mockedListSessions).toHaveBeenCalledTimes(2))
    // Rows read as loading until the new answer lands, and act only after it.
    await waitFor(() => {
      expect(screen.getByTestId('record-health-p1').getAttribute('data-health')).toBe('dead')
    })

    fireEvent.click(screen.getByTestId('record-attention-rebuild-p2'))
    await waitFor(() => expect(mockedListSessions).toHaveBeenCalledTimes(3))
    expect(mockedListSessions.mock.calls.every(([h]) => h === 'h1')).toBe(true)
  })

  // Same host, same refresh count — but the rows went away and came back, so a
  // new lookup started and the old answer is not the answer to it.
  it('rows that come back read as loading until their own lookup answers', async () => {
    seedTabs(recordTab('t1', 'p1'))
    mockedListSessions.mockRejectedValueOnce(new Error('offline'))
    render(<SnapshotsSection hostId="h1" />)
    await waitFor(() => {
      expect(screen.getByTestId('record-health-p1').getAttribute('data-health')).toBe('offline')
    })

    act(() => { seedTabs() })
    expect(screen.queryByTestId('record-health-p1')).toBeNull()

    mockedListSessions.mockReturnValue(new Promise<Session[]>(() => {}))
    act(() => { seedTabs(recordTab('t1', 'p1')) })
    await waitFor(() => expect(mockedListSessions).toHaveBeenCalledTimes(2))
    expect(screen.getByTestId('record-health-p1').getAttribute('data-health')).toBe('loading')
  })

  it('another host\'s answer reads as loading until this host\'s own arrives', async () => {
    seedTabs(recordTab('t1', 'p1'), recordTab('t2', 'p2', { hostId: 'h2' }))
    const { rerender } = render(<SnapshotsSection hostId="h1" />)
    await waitFor(() => {
      expect(screen.getByTestId('record-health-p1').getAttribute('data-health')).toBe('dead')
    })

    mockedListSessions.mockReturnValue(new Promise<Session[]>(() => {}))
    rerender(<SnapshotsSection hostId="h2" />)
    expect(screen.getByTestId('record-health-p2').getAttribute('data-health')).toBe('loading')
  })
})
