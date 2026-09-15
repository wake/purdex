import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createSession, listSessions } from '../host-api'
import type { Session } from '../host-api'
import type { PaneContent, PaneLayout, Tab } from '../../types/tab'
import type { SessionMeta, WorkspaceSnapshot } from '../snapshot/types'
import { scanPaneTree } from '../pane-tree'
import { markMissingHosts, reattachByName } from './reattach'

vi.mock('../host-api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../host-api')>()),
  listSessions: vi.fn(),
  createSession: vi.fn(),
}))

function tmux(hostId: string, sessionCode: string, extra: Partial<PaneContent> = {}): PaneContent {
  return { kind: 'tmux-session', hostId, sessionCode, mode: 'terminal', cachedName: sessionCode, tmuxInstance: '1:1', ...extra } as PaneContent
}

function leaf(id: string, content: PaneContent): PaneLayout {
  return { type: 'leaf', pane: { id, content } }
}

function tab(id: string, layout: PaneLayout): Tab {
  return { id, pinned: false, locked: false, createdAt: 1, layout }
}

function meta(hostId: string, sessionCode: string, overrides: Partial<SessionMeta> = {}): SessionMeta {
  return { hostId, sessionCode, name: sessionCode, mode: 'terminal', cwd: '/work', restorable: true, ...overrides }
}

function session(overrides: Partial<Session> & { code: string }): Session {
  return {
    name: overrides.code, cwd: '/tmp', mode: 'terminal', cc_session_id: '', cc_model: '', has_relay: false,
    tmux_instance: '9:9', ...overrides,
  }
}

function contents(layout: PaneLayout): Record<string, PaneContent> {
  const out: Record<string, PaneContent> = {}
  scanPaneTree(layout, (p) => { out[p.id] = p.content })
  return out
}

function buildSnap(): WorkspaceSnapshot {
  return {
    version: 1, capturedAt: 1,
    tabs: {
      t1: tab('t1', leaf('p1', tmux('known', 'c1'))),
      t2: tab('t2', leaf('p2', tmux('gone', 'c2'))),
      t3: tab('t3', {
        type: 'split', id: 's1', direction: 'h', sizes: [50, 50],
        children: [
          leaf('p3', tmux('gone', 'c3')),
          {
            type: 'split', id: 's2', direction: 'v', sizes: [50, 50],
            children: [leaf('p4', tmux('known', 'c4')), leaf('p5', { kind: 'new-tab' } as PaneContent)],
          },
        ],
      }),
    },
    tabOrder: ['t1', 't2', 't3'], activeTabId: 't1',
    workspaces: [], activeWorkspaceId: null,
    sessionMeta: {
      known: { c1: meta('known', 'c1'), c4: meta('known', 'c4') },
      gone: { c2: meta('gone', 'c2'), c3: meta('gone', 'c3') },
    },
  }
}

describe('markMissingHosts', () => {
  it('leaves panes on known hosts untouched', () => {
    const { snap } = markMissingHosts(buildSnap(), new Set(['known']))
    expect(contents(snap.tabs.t1.layout).p1).toEqual(tmux('known', 'c1'))
    expect(contents(snap.tabs.t3.layout).p4).toEqual(tmux('known', 'c4'))
    expect(contents(snap.tabs.t3.layout).p5).toEqual({ kind: 'new-tab' })
  })

  it('marks unknown-host panes host-removed, including inside split layouts', () => {
    const { snap } = markMissingHosts(buildSnap(), new Set(['known']))
    expect(contents(snap.tabs.t2.layout).p2).toEqual(tmux('gone', 'c2', { terminated: 'host-removed' }))
    expect(contents(snap.tabs.t3.layout).p3).toEqual(tmux('gone', 'c3', { terminated: 'host-removed' }))
  })

  it('counts host-removed panes', () => {
    expect(markMissingHosts(buildSnap(), new Set(['known'])).hostRemoved).toBe(2)
    expect(markMissingHosts(buildSnap(), new Set(['known', 'gone'])).hostRemoved).toBe(0)
    expect(markMissingHosts(buildSnap(), new Set()).hostRemoved).toBe(4)
  })

  it('removes missing hosts from sessionMeta', () => {
    const { snap } = markMissingHosts(buildSnap(), new Set(['known']))
    expect(Object.keys(snap.sessionMeta)).toEqual(['known'])
    expect(snap.sessionMeta.known).toEqual(buildSnap().sessionMeta.known)
  })

  it('does not mutate the input', () => {
    const input = buildSnap()
    const before = structuredClone(input)
    const { snap } = markMissingHosts(input, new Set(['known']))
    expect(input).toEqual(before)
    expect(snap).not.toBe(input)
  })
})

describe('reattachByName', () => {
  beforeEach(() => {
    vi.mocked(listSessions).mockReset()
    vi.mocked(createSession).mockReset()
  })

  it('reattaches a same-name session with a different code, adopting the new code', async () => {
    const live = session({ code: 'newc', name: 'work' })
    vi.mocked(listSessions).mockResolvedValue([live])
    const { remap, report } = await reattachByName({ h: { old: meta('h', 'old', { name: 'work' }) } })
    expect(remap.h.old).toEqual({ status: 'reattached', newCode: 'newc', session: live })
    expect(report).toEqual({ reattached: 1, rebuilt: 0, failed: 0 })
    expect(createSession).not.toHaveBeenCalled()
  })

  it('fails a same-code session with a different name', async () => {
    vi.mocked(listSessions).mockResolvedValue([session({ code: 'c1', name: 'other' })])
    const { remap, report } = await reattachByName({ h: { c1: meta('h', 'c1', { name: 'work' }) } })
    expect(remap.h.c1).toEqual({ status: 'failed' })
    expect(report).toEqual({ reattached: 0, rebuilt: 0, failed: 1 })
  })

  it('listSessions throw fails every entry of that host only; one call per host', async () => {
    vi.mocked(listSessions).mockImplementation(async (hostId) => {
      if (hostId === 'down') throw new Error('offline')
      return [session({ code: 'x', name: 'a' })]
    })
    const { remap, report } = await reattachByName({
      down: { d1: meta('down', 'd1', { name: 'a' }), d2: meta('down', 'd2', { name: 'b' }) },
      up: { u1: meta('up', 'u1', { name: 'a' }) },
    })
    expect(listSessions).toHaveBeenCalledTimes(2)
    expect(remap.down).toEqual({ d1: { status: 'failed' }, d2: { status: 'failed' } })
    expect(remap.up.u1.status).toBe('reattached')
    expect(report).toEqual({ reattached: 1, rebuilt: 0, failed: 2 })
    expect(createSession).not.toHaveBeenCalled()
  })

  it.each([
    ['empty code', { code: '' }],
    ['non-string code', { code: 42 as unknown as string }],
    ['empty tmux_instance', { tmux_instance: '' }],
    ['missing tmux_instance', { tmux_instance: undefined }],
    ['non-string tmux_instance', { tmux_instance: 7 as unknown as string }],
  ])('fails when the live session has %s', async (_label, overrides) => {
    vi.mocked(listSessions).mockResolvedValue([session({ code: 'n', name: 'work', ...overrides } as Session)])
    const { remap, report } = await reattachByName({ h: { old: meta('h', 'old', { name: 'work' }) } })
    expect(remap.h.old).toEqual({ status: 'failed' })
    expect(report.failed).toBe(1)
  })

  it('fails a terminal pane against a stream-mode session', async () => {
    vi.mocked(listSessions).mockResolvedValue([session({ code: 'n', name: 'work', mode: 'stream' })])
    const { remap } = await reattachByName({ h: { old: meta('h', 'old', { name: 'work' }) } })
    expect(remap.h.old).toEqual({ status: 'failed' })
  })

  it('reattaches a terminal pane when the session mode is absent', async () => {
    vi.mocked(listSessions).mockResolvedValue([session({ code: 'n', name: 'work', mode: undefined as unknown as string })])
    const { remap } = await reattachByName({ h: { old: meta('h', 'old', { name: 'work' }) } })
    expect(remap.h.old.status).toBe('reattached')
  })

  it('never reattaches a stream pane', async () => {
    vi.mocked(listSessions).mockResolvedValue([
      session({ code: 'n', name: 'work', mode: 'stream' }),
      session({ code: 'm', name: 'work2', mode: 'terminal' }),
    ])
    const { remap } = await reattachByName({
      h: {
        s1: meta('h', 's1', { name: 'work', mode: 'stream' }),
        s2: meta('h', 's2', { name: 'work2', mode: 'stream' }),
      },
    })
    expect(remap.h).toEqual({ s1: { status: 'failed' }, s2: { status: 'failed' } })
  })

  it('counts a mixed report and never calls createSession', async () => {
    vi.mocked(listSessions).mockResolvedValue([
      session({ code: 'a2', name: 'a' }),
      session({ code: 'b2', name: 'b' }),
    ])
    const { report } = await reattachByName({
      h: {
        a1: meta('h', 'a1', { name: 'a' }),
        b1: meta('h', 'b1', { name: 'b' }),
        z1: meta('h', 'z1', { name: 'nope', restorable: true, cwd: '/x' }),
      },
    })
    expect(report).toEqual({ reattached: 2, rebuilt: 0, failed: 1 })
    expect(createSession).not.toHaveBeenCalled()
  })
})
