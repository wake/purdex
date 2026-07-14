import { describe, it, expect, beforeEach, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { SnapshotSettingsSection } from './SnapshotSettingsSection'
import type { SessionMeta, WorkspaceSnapshot } from '../../lib/snapshot/types'
import type { Session } from '../../lib/host-api'
import type { PaneContent, Tab, Workspace } from '../../types/tab'
import * as storageModule from '../../lib/snapshot/storage'
import * as hostApiModule from '../../lib/host-api'

vi.mock('../../lib/snapshot/storage')
vi.mock('../../lib/host-api')

// ---- fixtures ------------------------------------------------------------

function meta(over: Partial<SessionMeta> & Pick<SessionMeta, 'hostId' | 'sessionCode' | 'name'>): SessionMeta {
  return {
    mode: 'terminal',
    restorable: true,
    ...over,
  }
}

function session(over: Partial<Session> & Pick<Session, 'code' | 'name'>): Session {
  return {
    cwd: '/tmp',
    mode: 'terminal',
    cc_session_id: '',
    cc_model: '',
    has_relay: false,
    ...over,
  }
}

function leafTab(id: string, content: PaneContent): Tab {
  return {
    id,
    pinned: false,
    locked: false,
    createdAt: 0,
    layout: { type: 'leaf', pane: { id: `${id}-pane`, content } },
  }
}

function ws(over: Partial<Workspace> & Pick<Workspace, 'id' | 'name' | 'tabs'>): Workspace {
  return { activeTabId: null, ...over }
}

function makeSnapshot(over: Partial<WorkspaceSnapshot>): WorkspaceSnapshot {
  return {
    version: 1,
    capturedAt: Date.now(),
    tabs: {},
    tabOrder: [],
    activeTabId: null,
    workspaces: [],
    activeWorkspaceId: null,
    sessionMeta: {},
    ...over,
  }
}

const mockedReadSnapshot = vi.mocked(storageModule.readSnapshot)
const mockedReadPrev = vi.mocked(storageModule.readPrevSnapshot)
const mockedListSessions = vi.mocked(hostApiModule.listSessions)

beforeEach(() => {
  vi.clearAllMocks()
  mockedReadPrev.mockReturnValue(null)
  mockedListSessions.mockResolvedValue([])
})

describe('SnapshotSettingsSection — health reconciliation (T8)', () => {
  it('live list missing the captured code → row is dead-rebuildable (red)', async () => {
    mockedReadSnapshot.mockReturnValue(
      makeSnapshot({
        sessionMeta: { h1: { s1: meta({ hostId: 'h1', sessionCode: 's1', name: 'work', cwd: '/x', restorable: true }) } },
      }),
    )
    // Host reachable but returns a DIFFERENT code — no match.
    mockedListSessions.mockResolvedValue([session({ code: 'other', name: 'other' })])

    render(<SnapshotSettingsSection />)
    await waitFor(() => {
      expect(screen.getByTestId('snapshot-health-h1-s1').getAttribute('data-health')).toBe('dead')
    })
  })

  it('live list with matching code AND name → row is live (green)', async () => {
    mockedReadSnapshot.mockReturnValue(
      makeSnapshot({
        sessionMeta: { h1: { s1: meta({ hostId: 'h1', sessionCode: 's1', name: 'work', cwd: '/x' }) } },
      }),
    )
    mockedListSessions.mockResolvedValue([session({ code: 's1', name: 'work' })])

    render(<SnapshotSettingsSection />)
    await waitFor(() => {
      expect(screen.getByTestId('snapshot-health-h1-s1').getAttribute('data-health')).toBe('live')
    })
  })

  it('code matches but name differs → NOT live, falls through to dead (red)', async () => {
    mockedReadSnapshot.mockReturnValue(
      makeSnapshot({
        sessionMeta: { h1: { s1: meta({ hostId: 'h1', sessionCode: 's1', name: 'work', cwd: '/x', restorable: true }) } },
      }),
    )
    // Same code, DIFFERENT name → a reused code after a tmux restart.
    mockedListSessions.mockResolvedValue([session({ code: 's1', name: 'somethingElse' })])

    render(<SnapshotSettingsSection />)
    await waitFor(() => {
      expect(screen.getByTestId('snapshot-health-h1-s1').getAttribute('data-health')).toBe('dead')
    })
  })

  it('restorable:false → structure-only (yellow)', async () => {
    mockedReadSnapshot.mockReturnValue(
      makeSnapshot({
        sessionMeta: {
          h1: { s1: meta({ hostId: 'h1', sessionCode: 's1', name: 'work', restorable: false }) },
        },
      }),
    )
    mockedListSessions.mockResolvedValue([])

    render(<SnapshotSettingsSection />)
    await waitFor(() => {
      expect(screen.getByTestId('snapshot-health-h1-s1').getAttribute('data-health')).toBe('structure')
    })
  })

  it('listSessions rejects → every row for that host is host-offline (gray)', async () => {
    mockedReadSnapshot.mockReturnValue(
      makeSnapshot({
        sessionMeta: {
          h1: {
            s1: meta({ hostId: 'h1', sessionCode: 's1', name: 'a', cwd: '/x' }),
            s2: meta({ hostId: 'h1', sessionCode: 's2', name: 'b', restorable: false }),
          },
        },
      }),
    )
    mockedListSessions.mockRejectedValue(new Error('offline'))

    render(<SnapshotSettingsSection />)
    await waitFor(() => {
      expect(screen.getByTestId('snapshot-health-h1-s1').getAttribute('data-health')).toBe('offline')
    })
    // Even the restorable:false row is offline when the host is unreachable.
    expect(screen.getByTestId('snapshot-health-h1-s2').getAttribute('data-health')).toBe('offline')
  })

  it('calls listSessions exactly once per captured host', async () => {
    mockedReadSnapshot.mockReturnValue(
      makeSnapshot({
        sessionMeta: {
          h1: { s1: meta({ hostId: 'h1', sessionCode: 's1', name: 'a', cwd: '/x' }) },
          h2: { s2: meta({ hostId: 'h2', sessionCode: 's2', name: 'b', cwd: '/y' }) },
        },
      }),
    )
    render(<SnapshotSettingsSection />)
    await waitFor(() => {
      expect(mockedListSessions).toHaveBeenCalledTimes(2)
    })
    expect(mockedListSessions).toHaveBeenCalledWith('h1')
    expect(mockedListSessions).toHaveBeenCalledWith('h2')
  })
})

describe('SnapshotSettingsSection — tabs tree (T8 block 2)', () => {
  it('renders workspace → tab → pane labels (terminal name / editor path / browser url)', () => {
    const tabs: Record<string, Tab> = {
      t1: leafTab('t1', { kind: 'tmux-session', hostId: 'h1', sessionCode: 's1', mode: 'terminal', cachedName: 'my-term', tmuxInstance: 'default' }),
      t2: leafTab('t2', { kind: 'editor', source: { kind: 'local' } as never, filePath: '/repo/main.ts' }),
      t3: leafTab('t3', { kind: 'browser', url: 'https://example.test/page' }),
    }
    mockedReadSnapshot.mockReturnValue(
      makeSnapshot({
        tabs,
        tabOrder: ['t1', 't2', 't3'],
        workspaces: [ws({ id: 'w1', name: 'Alpha', tabs: ['t1', 't2', 't3'] })],
        activeWorkspaceId: 'w1',
      }),
    )

    render(<SnapshotSettingsSection />)
    expect(screen.getByText('Alpha')).toBeTruthy()
    expect(screen.getByText('my-term')).toBeTruthy()
    expect(screen.getByText('/repo/main.ts')).toBeTruthy()
    expect(screen.getByText('https://example.test/page')).toBeTruthy()
  })
})

describe('SnapshotSettingsSection — empty state (T8)', () => {
  it('no snapshot → shows only capture button + empty message, no tables', () => {
    mockedReadSnapshot.mockReturnValue(null)

    render(<SnapshotSettingsSection />)
    expect(screen.getByTestId('snapshot-capture-btn')).toBeTruthy()
    expect(screen.getByTestId('snapshot-empty')).toBeTruthy()
    expect(screen.queryByTestId('snapshot-tmux-block')).toBeNull()
    expect(screen.queryByTestId('snapshot-tabs-block')).toBeNull()
    expect(mockedListSessions).not.toHaveBeenCalled()
  })
})
