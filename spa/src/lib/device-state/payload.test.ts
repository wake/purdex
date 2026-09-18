import { beforeEach, describe, expect, it, vi } from 'vitest'
import { useTabStore } from '../../stores/useTabStore'
import { useWorkspaceStore } from '../../features/workspace/store'
import { listSessions, fetchSessionCwd } from '../host-api'
import type { Tab, PaneLayout, PaneContent, Workspace, PaneRebuildRecord } from '../../types/tab'
import type { WorkspaceSnapshot } from '../snapshot/types'
import { buildDeviceStatePayload, hashPayload, structuralKey } from './payload'

vi.mock('../host-api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../host-api')>()),
  listSessions: vi.fn(),
  fetchSessionCwd: vi.fn(),
}))

function tmux(
  hostId: string,
  sessionCode: string,
  cachedName: string,
  opts: { mode?: 'terminal'; rebuild?: PaneRebuildRecord } = {},
): PaneContent {
  return {
    kind: 'tmux-session', hostId, sessionCode, mode: opts.mode ?? 'terminal', cachedName, tmuxInstance: '',
    ...(opts.rebuild ? { rebuild: opts.rebuild } : {}),
  }
}

function rebuild(cwd: unknown): PaneRebuildRecord {
  return { sessionName: 's', tmuxInstance: '', cwd: cwd as string | undefined, capturedAt: 1 }
}

function leaf(paneId: string, content: PaneContent): PaneLayout {
  return { type: 'leaf', pane: { id: paneId, content } }
}

function split(id: string, children: PaneLayout[]): PaneLayout {
  return { type: 'split', id, direction: 'h', children, sizes: children.map(() => 100 / children.length) }
}

function tab(id: string, layout: PaneLayout): Tab {
  return { id, pinned: false, locked: false, createdAt: 0, layout }
}

function seed(
  tabs: Record<string, Tab>,
  tabOrder: string[],
  activeTabId: string | null,
  workspaces: Workspace[] = [],
  activeWorkspaceId: string | null = null,
): void {
  useTabStore.setState({ tabs, tabOrder, activeTabId })
  useWorkspaceStore.setState({ workspaces, activeWorkspaceId })
}

describe('buildDeviceStatePayload', () => {
  beforeEach(() => {
    useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null })
    useWorkspaceStore.getState().reset()
    vi.mocked(listSessions).mockReset()
    vi.mocked(fetchSessionCwd).mockReset()
  })

  it('copies tab and workspace store fields with version 1 and capturedAt', () => {
    const t1 = tab('t1', leaf('p1', { kind: 'dashboard' }))
    const ws: Workspace = { id: 'w1', name: 'W', tabs: ['t1'], activeTabId: 't1' }
    seed({ t1 }, ['t1'], 't1', [ws], 'w1')

    const snap = buildDeviceStatePayload(1234)

    expect(snap.version).toBe(1)
    expect(snap.capturedAt).toBe(1234)
    expect(snap.tabs).toEqual({ t1 })
    expect(snap.tabOrder).toEqual(['t1'])
    expect(snap.activeTabId).toBe('t1')
    expect(snap.workspaces).toEqual([ws])
    expect(snap.activeWorkspaceId).toBe('w1')
    expect(snap.sessionMeta).toEqual({})
  })

  it('builds sessionMeta from tmux panes with cachedName, mode terminal, restorable false, cwd only from non-empty rebuild.cwd', () => {
    const tabs = {
      // 'stream' on a live pane cannot happen after the tab-store v3 migration;
      // the cast covers a blob that reached the payload builder unmigrated —
      // the meta must still say 'terminal'.
      a: tab('a', leaf('pa', tmux('h1', 'c1', 'alpha', { mode: 'stream' as unknown as 'terminal', rebuild: rebuild('/work/a') }))),
      b: tab('b', leaf('pb', tmux('h1', 'c2', 'beta'))),
      c: tab('c', leaf('pc', tmux('h2', 'c3', 'gamma', { rebuild: rebuild('') }))),
      d: tab('d', leaf('pd', tmux('h2', 'c4', 'delta', { rebuild: rebuild(undefined) }))),
    }
    seed(tabs, ['a', 'b', 'c', 'd'], 'a')

    const snap = buildDeviceStatePayload(1)

    expect(snap.sessionMeta).toEqual({
      h1: {
        c1: { hostId: 'h1', sessionCode: 'c1', name: 'alpha', mode: 'terminal', restorable: false, cwd: '/work/a' },
        c2: { hostId: 'h1', sessionCode: 'c2', name: 'beta', mode: 'terminal', restorable: false },
      },
      h2: {
        c3: { hostId: 'h2', sessionCode: 'c3', name: 'gamma', mode: 'terminal', restorable: false },
        c4: { hostId: 'h2', sessionCode: 'c4', name: 'delta', mode: 'terminal', restorable: false },
      },
    })
    expect('cwd' in snap.sessionMeta.h1.c2).toBe(false)
    expect('cwd' in snap.sessionMeta.h2.c3).toBe(false)
    expect('captureError' in snap.sessionMeta.h1.c1).toBe(false)
  })

  it('dedupes per (hostId, sessionCode): first pane in tabOrder wins, then tabs not in tabOrder', () => {
    const tabs = {
      late: tab('late', leaf('p0', tmux('h1', 'c1', 'from-orphan', { rebuild: rebuild('/orphan') }))),
      second: tab('second', leaf('p2', tmux('h1', 'c1', 'from-second'))),
      first: tab('first', leaf('p1', tmux('h1', 'c1', 'from-first', { rebuild: rebuild('/first') }))),
      other: tab('other', leaf('p3', tmux('h2', 'c1', 'other-host'))),
      orphanOnly: tab('orphanOnly', leaf('p4', tmux('h1', 'c9', 'orphan-only'))),
    }
    seed(tabs, ['first', 'second', 'other'], 'first')

    const snap = buildDeviceStatePayload(1)

    expect(snap.sessionMeta.h1.c1).toEqual({
      hostId: 'h1', sessionCode: 'c1', name: 'from-first', mode: 'terminal', restorable: false, cwd: '/first',
    })
    expect(snap.sessionMeta.h2.c1.name).toBe('other-host')
    expect(snap.sessionMeta.h1.c9.name).toBe('orphan-only')
  })

  it('excludes non-tmux panes and scans split layouts', () => {
    const layout = split('s1', [
      leaf('p1', { kind: 'browser', url: 'https://x' }),
      split('s2', [leaf('p2', { kind: 'dashboard' }), leaf('p3', tmux('h1', 'deep', 'nested'))]),
    ])
    seed({ t: tab('t', layout), n: tab('n', leaf('pn', { kind: 'new-tab' })) }, ['t', 'n'], 't')

    const snap = buildDeviceStatePayload(1)

    expect(snap.sessionMeta).toEqual({
      h1: { deep: { hostId: 'h1', sessionCode: 'deep', name: 'nested', mode: 'terminal', restorable: false } },
    })
  })

  it('is synchronous and never touches the network', () => {
    seed({ t: tab('t', leaf('p', tmux('h1', 'c1', 'x'))) }, ['t'], 't')

    const snap = buildDeviceStatePayload(1)

    expect(snap).not.toBeInstanceOf(Promise)
    expect(listSessions).not.toHaveBeenCalled()
    expect(fetchSessionCwd).not.toHaveBeenCalled()
  })
})

function baseSnap(overrides: Partial<WorkspaceSnapshot> = {}): WorkspaceSnapshot {
  return {
    version: 1,
    capturedAt: 100,
    tabs: { t1: tab('t1', leaf('p1', tmux('h1', 'c1', 'n'))) },
    tabOrder: ['t1', 't2'],
    activeTabId: 't1',
    workspaces: [],
    activeWorkspaceId: null,
    sessionMeta: { h1: { c1: { hostId: 'h1', sessionCode: 'c1', name: 'n', mode: 'terminal', restorable: false } } },
    ...overrides,
  }
}

/** Deeply rebuild an object with its keys inserted in reverse order. */
function reverseKeys<T>(v: T): T {
  if (Array.isArray(v)) return v.map(reverseKeys) as T
  if (v && typeof v === 'object') {
    const out: Record<string, unknown> = {}
    for (const k of Object.keys(v).reverse()) out[k] = reverseKeys((v as Record<string, unknown>)[k])
    return out as T
  }
  return v
}

describe('structuralKey', () => {
  it('ignores capturedAt', () => {
    expect(structuralKey(baseSnap({ capturedAt: 1 }))).toBe(structuralKey(baseSnap({ capturedAt: 2 })))
    expect(structuralKey(baseSnap())).not.toContain('capturedAt')
  })

  it('ignores object key order', () => {
    const a = baseSnap()
    const b = reverseKeys(a)
    expect(JSON.stringify(a)).not.toBe(JSON.stringify(b))
    expect(structuralKey(b)).toBe(structuralKey(a))
  })

  it('respects array order', () => {
    expect(structuralKey(baseSnap({ tabOrder: ['t2', 't1'] }))).not.toBe(structuralKey(baseSnap()))
  })
})

describe('hashPayload', () => {
  it('is equal for key-reordered equal payloads and a 64-char hex string', async () => {
    const a = baseSnap({ capturedAt: 1 })
    const b = reverseKeys(baseSnap({ capturedAt: 999 }))
    const ha = await hashPayload(a)
    expect(ha).toMatch(/^[0-9a-f]{64}$/)
    expect(await hashPayload(b)).toBe(ha)
  })

  it('differs when a tab changes', async () => {
    const a = baseSnap()
    const b = baseSnap({ tabs: { t1: { ...a.tabs.t1, pinned: true } } })
    expect(await hashPayload(b)).not.toBe(await hashPayload(a))
  })
})
