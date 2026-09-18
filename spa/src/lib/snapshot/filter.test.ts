import { afterEach, beforeEach, describe, it, expect, vi } from 'vitest'
import { createSession, listSessions } from '../host-api'
import type { Session } from '../host-api'
import { useTabStore } from '../../stores/useTabStore'
import { useRebuildStore } from '../../stores/useRebuildStore'
import { filterSnapshotByHost, selectSnapshotClientHostId } from './filter'
import { rebuildAllSessions } from './restore'
import type { WorkspaceSnapshot } from './types'

vi.mock('../host-api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../host-api')>()),
  listSessions: vi.fn(),
  createSession: vi.fn(),
}))

const snap: WorkspaceSnapshot = {
  version: 1, capturedAt: 5, tabs: {}, tabOrder: [], activeTabId: null, workspaces: [], activeWorkspaceId: null,
  sessionMeta: {
    h1: { s1: { hostId: 'h1', sessionCode: 's1', name: 'a', mode: 'terminal', restorable: true, cwd: '/a' } },
    h2: { s2: { hostId: 'h2', sessionCode: 's2', name: 'b', mode: 'terminal', restorable: true, cwd: '/b' } },
  },
}

describe('filterSnapshotByHost', () => {
  it('keeps only that host\'s session meta and never mutates the original', () => {
    const out = filterSnapshotByHost(snap, 'h1')
    expect(Object.keys(out.sessionMeta)).toEqual(['h1'])
    expect(out.sessionMeta.h1).toBe(snap.sessionMeta.h1)
    expect(out.tabs).toBe(snap.tabs)
    expect(Object.keys(snap.sessionMeta)).toEqual(['h1', 'h2'])
    expect(out).not.toBe(snap)
  })

  it('a host with nothing captured yields an empty map', () => {
    expect(filterSnapshotByHost(snap, 'h9').sessionMeta).toEqual({})
  })

  it('does not treat inherited object keys as hosts', () => {
    expect(filterSnapshotByHost(snap, 'toString').sessionMeta).toEqual({})
  })
})

describe('selectSnapshotClientHostId', () => {
  const hosts = { h1: {}, h2: {} }
  it('prefers the dev host', () => {
    expect(selectSnapshotClientHostId({ devHostId: 'h2', hosts, hostOrder: ['h1', 'h2'] })).toBe('h2')
  })
  it('falls back to the first existing host in order when no (valid) dev host', () => {
    expect(selectSnapshotClientHostId({ devHostId: null, hosts, hostOrder: ['gone', 'h1', 'h2'] })).toBe('h1')
    expect(selectSnapshotClientHostId({ devHostId: 'gone', hosts, hostOrder: ['h2', 'h1'] })).toBe('h2')
    expect(selectSnapshotClientHostId({ devHostId: null, hosts: {}, hostOrder: [] })).toBeNull()
  })
})

// Amendment A5: the REAL rebuildAllSessions over the host-filtered copy can
// never create a session on another host.
describe('rebuildAllSessions over filterSnapshotByHost', () => {
  const reset = () => {
    useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, visitHistory: [] })
    useRebuildStore.setState({ operations: {}, lockedBy: null })
  }
  beforeEach(() => {
    localStorage.clear()
    vi.mocked(listSessions).mockReset()
    vi.mocked(createSession).mockReset()
    reset()
  })
  afterEach(() => {
    reset()
    localStorage.clear()
  })

  it('creates sessions on h1 only, never on h2', async () => {
    vi.mocked(listSessions).mockResolvedValue([])
    vi.mocked(createSession).mockImplementation(async (_h, name): Promise<Session> => ({
      code: `new-${name}`, name, cwd: '/tmp', mode: 'terminal',
    }))

    const report = await rebuildAllSessions(filterSnapshotByHost(snap, 'h1'))

    expect(report.rebuilt).toBe(1)
    expect(vi.mocked(createSession).mock.calls.length).toBeGreaterThan(0)
    for (const call of vi.mocked(createSession).mock.calls) expect(call[0]).toBe('h1')
    for (const call of vi.mocked(listSessions).mock.calls) expect(call[0]).not.toBe('h2')
  })
})
