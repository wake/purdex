import { beforeEach, describe, expect, it, vi } from 'vitest'
import { useTabStore } from '../../stores/useTabStore'
import { useWorkspaceStore } from '../../features/workspace/store'
import { listSessions } from '../host-api'
import type { Session } from '../host-api'
import type { Tab, PaneLayout, PaneContent } from '../../types/tab'
import { readSnapshot, writeSnapshot } from './storage'
import type { WorkspaceSnapshot } from './types'
import { buildSnapshot, captureSnapshot } from './capture'

vi.mock('../host-api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../host-api')>()),
  listSessions: vi.fn(),
}))

function tmuxContent(
  hostId: string,
  sessionCode: string,
  cachedName: string,
  mode: 'terminal' | 'stream' = 'terminal',
): PaneContent {
  return { kind: 'tmux-session', hostId, sessionCode, mode, cachedName, tmuxInstance: '' }
}

function leaf(paneId: string, content: PaneContent): PaneLayout {
  return { type: 'leaf', pane: { id: paneId, content } }
}

function split(id: string, children: PaneLayout[]): PaneLayout {
  const sizes = children.map(() => 100 / children.length)
  return { type: 'split', id, direction: 'h', children, sizes }
}

function tab(id: string, layout: PaneLayout): Tab {
  return { id, pinned: false, locked: false, createdAt: 0, layout }
}

function session(overrides: Partial<Session> & { code: string }): Session {
  return {
    name: overrides.name ?? overrides.code,
    cwd: overrides.cwd ?? '/tmp',
    mode: overrides.mode ?? 'terminal',
    cc_session_id: '',
    cc_model: '',
    has_relay: false,
    ...overrides,
  }
}

function seedStores(tabs: Record<string, Tab>, tabOrder: string[], activeTabId: string | null): void {
  useTabStore.setState({ tabs, tabOrder, activeTabId })
  useWorkspaceStore.getState().reset()
}

describe('buildSnapshot / captureSnapshot', () => {
  beforeEach(() => {
    localStorage.clear()
    useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null })
    useWorkspaceStore.getState().reset()
    vi.mocked(listSessions).mockReset()
  })

  it('1. single host, two live tmux panes → nested sessionMeta + resolved=2', async () => {
    const layout = split('s1', [
      leaf('p1', tmuxContent('hostA', 'code1', 'cached1')),
      leaf('p2', tmuxContent('hostA', 'code2', 'cached2')),
    ])
    seedStores({ t1: tab('t1', layout) }, ['t1'], 't1')

    vi.mocked(listSessions).mockResolvedValue([
      session({ code: 'code1', name: 'live1', cwd: '/a', current_command: 'vim' }),
      session({ code: 'code2', name: 'live2', cwd: '/b', current_command: 'top' }),
    ])

    const result = await captureSnapshot(1000)

    expect(listSessions).toHaveBeenCalledTimes(1)
    expect(listSessions).toHaveBeenCalledWith('hostA')

    const stored = readSnapshot()
    expect(stored).not.toBeNull()
    expect(stored!.capturedAt).toBe(1000)
    expect(stored!.sessionMeta.hostA.code1).toEqual({
      hostId: 'hostA', sessionCode: 'code1', name: 'live1', mode: 'terminal',
      cwd: '/a', currentCommand: 'vim', restorable: true,
    })
    expect(stored!.sessionMeta.hostA.code2).toEqual({
      hostId: 'hostA', sessionCode: 'code2', name: 'live2', mode: 'terminal',
      cwd: '/b', currentCommand: 'top', restorable: true,
    })
    expect(result).toEqual({ total: 2, resolved: 2, unresolved: 0 })
  })

  it('2. cross-host same sessionCode literal stays independent (composite key)', async () => {
    const tabs: Record<string, Tab> = {
      t1: tab('t1', leaf('p1', tmuxContent('hostA', 'shared', 'cachedA'))),
      t2: tab('t2', leaf('p2', tmuxContent('hostB', 'shared', 'cachedB'))),
    }
    seedStores(tabs, ['t1', 't2'], 't1')

    vi.mocked(listSessions).mockImplementation(async (hostId: string) => {
      if (hostId === 'hostA') return [session({ code: 'shared', name: 'a-name', cwd: '/host-a' })]
      if (hostId === 'hostB') return [session({ code: 'shared', name: 'b-name', cwd: '/host-b' })]
      throw new Error(`unexpected host ${hostId}`)
    })

    const snap = await buildSnapshot(2000)

    expect(listSessions).toHaveBeenCalledTimes(2)
    expect(snap.sessionMeta.hostA.shared.cwd).toBe('/host-a')
    expect(snap.sessionMeta.hostB.shared.cwd).toBe('/host-b')
    expect(snap.sessionMeta.hostA.shared).not.toEqual(snap.sessionMeta.hostB.shared)
  })

  it('3. one host unreachable does not interrupt the other host', async () => {
    const tabs: Record<string, Tab> = {
      t1: tab('t1', leaf('p1', tmuxContent('hostA', 'codeA', 'cachedA'))),
      t2: tab('t2', leaf('p2', tmuxContent('hostB', 'codeB', 'cachedB'))),
    }
    seedStores(tabs, ['t1', 't2'], 't1')

    vi.mocked(listSessions).mockImplementation(async (hostId: string) => {
      if (hostId === 'hostA') return [session({ code: 'codeA', name: 'live-a', cwd: '/a' })]
      throw new Error('host B unreachable')
    })

    const result = await captureSnapshot(3000)
    const stored = readSnapshot()!

    expect(stored.sessionMeta.hostA.codeA).toMatchObject({ restorable: true, cwd: '/a' })
    expect(stored.sessionMeta.hostB.codeB).toEqual({
      hostId: 'hostB', sessionCode: 'codeB', name: 'cachedB', mode: 'terminal',
      cwd: undefined, restorable: false, captureError: 'host-unreachable',
    })
    expect(result).toEqual({ total: 2, resolved: 1, unresolved: 1 })
  })

  it('4. pane code missing from live list → session-dead-at-capture', async () => {
    seedStores(
      { t1: tab('t1', leaf('p1', tmuxContent('hostA', 'deadcode', 'cachedDead'))) },
      ['t1'],
      't1',
    )
    vi.mocked(listSessions).mockResolvedValue([session({ code: 'other-code' })])

    const snap = await buildSnapshot(4000)

    expect(snap.sessionMeta.hostA.deadcode).toEqual({
      hostId: 'hostA', sessionCode: 'deadcode', name: 'cachedDead', mode: 'terminal',
      cwd: undefined, restorable: false, captureError: 'session-dead-at-capture',
    })
  })

  it('7. live session with empty cwd → not restorable, cwd stays undefined (not "")', async () => {
    seedStores(
      { t1: tab('t1', leaf('p1', tmuxContent('hostA', 'code1', 'cached1'))) },
      ['t1'],
      't1',
    )
    vi.mocked(listSessions).mockResolvedValue([
      session({ code: 'code1', name: 'live-name', cwd: '', current_command: 'bash' }),
    ])

    const result = await captureSnapshot(7000)
    const stored = readSnapshot()!

    expect(stored.sessionMeta.hostA.code1).toEqual({
      hostId: 'hostA', sessionCode: 'code1', name: 'live-name', mode: 'terminal',
      cwd: undefined, currentCommand: 'bash', restorable: false,
    })
    expect(stored.sessionMeta.hostA.code1.captureError).toBeUndefined()
    expect(result).toEqual({ total: 1, resolved: 0, unresolved: 1 })
  })

  it('5. no tmux panes → total=0, sessionMeta={}, still writes a snapshot', async () => {
    seedStores(
      { t1: tab('t1', leaf('p1', { kind: 'editor', source: { type: 'local' }, filePath: '/tmp/a.md' })) },
      ['t1'],
      't1',
    )

    const result = await captureSnapshot(5000)

    expect(listSessions).not.toHaveBeenCalled()
    expect(result).toEqual({ total: 0, resolved: 0, unresolved: 0 })
    const stored = readSnapshot()
    expect(stored).not.toBeNull()
    expect(stored!.sessionMeta).toEqual({})
  })

  it('6. buildSnapshot never writes storage (B1) — only captureSnapshot writes the primary key', async () => {
    const oldSnap: WorkspaceSnapshot = {
      version: 1, capturedAt: -1, tabs: {}, tabOrder: [], activeTabId: null,
      workspaces: [], activeWorkspaceId: null, sessionMeta: {},
    }
    writeSnapshot(oldSnap)

    seedStores(
      { t1: tab('t1', leaf('p1', tmuxContent('hostA', 'code1', 'cached1'))) },
      ['t1'],
      't1',
    )
    vi.mocked(listSessions).mockResolvedValue([session({ code: 'code1', cwd: '/x' })])

    const built = await buildSnapshot(6000)
    expect(built.capturedAt).toBe(6000)
    expect(readSnapshot()).toEqual(oldSnap) // still the old snapshot — buildSnapshot did not write

    await captureSnapshot(6000)
    expect(readSnapshot()!.capturedAt).toBe(6000) // now overwritten by captureSnapshot
  })
})
