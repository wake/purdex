import { describe, expect, it } from 'vitest'
import type { PaneContent, PaneLayout, PaneRebuildRecord, Tab, Workspace } from '../../types/tab'
import { collectLeaves } from '../pane-tree'
import { validateSnapshotConsistency } from '../snapshot/restore'
import { cloneTabWithFreshIds, mergeDeviceState, type TabWorld } from './merge'

// --- builders ---------------------------------------------------------------

function leaf(id: string, content: PaneContent): PaneLayout {
  return { type: 'leaf', pane: { id, content } }
}

function tab(id: string, layout: PaneLayout): Tab {
  return { id, pinned: false, locked: false, createdAt: 1, layout }
}

function tmux(name: string, hostId = 'h1'): PaneContent {
  return { kind: 'tmux-session', hostId, sessionCode: `code-${name}`, mode: 'terminal', cachedName: name, tmuxInstance: 'i1' }
}

function browser(url: string): PaneContent {
  return { kind: 'browser', url }
}

function ws(id: string, name: string, tabs: string[], activeTabId: string | null = null, extra: Partial<Workspace> = {}): Workspace {
  return { id, name, tabs, activeTabId, ...extra }
}

function world(tabs: Tab[], workspaces: Workspace[], extra: Partial<TabWorld> = {}): TabWorld {
  return {
    tabs: Object.fromEntries(tabs.map((t) => [t.id, t])),
    tabOrder: tabs.map((t) => t.id),
    activeTabId: null,
    workspaces,
    activeWorkspaceId: null,
    ...extra,
  }
}

function counter(start = 0): () => string {
  let n = start
  return () => `id${n++}`
}

function splitIds(layout: PaneLayout): string[] {
  if (layout.type === 'leaf') return []
  return [layout.id, ...layout.children.flatMap(splitIds)]
}

function allIds(w: TabWorld): string[] {
  return Object.values(w.tabs).flatMap((t) => [
    t.id,
    ...collectLeaves(t.layout).map((p) => p.id),
    ...splitIds(t.layout),
  ])
}

function contentsOf(w: TabWorld, tabId: string): PaneContent[] {
  return collectLeaves(w.tabs[tabId].layout).map((p) => p.content)
}

function findTabByUrl(w: TabWorld, url: string): Tab[] {
  return Object.values(w.tabs).filter((t) =>
    collectLeaves(t.layout).some((p) => p.content.kind === 'browser' && p.content.url === url),
  )
}

function expectConsistent(next: TabWorld): void {
  expect(validateSnapshotConsistency({ version: 1, capturedAt: 0, sessionMeta: {}, ...next })).toEqual({ ok: true })
}

// --- tests ------------------------------------------------------------------

describe('mergeDeviceState', () => {
  it('appends missing tabs to a same-name workspace and leaves its settings untouched', () => {
    const current = world(
      [tab('c1', leaf('cp1', tmux('dev')))],
      [ws('cw1', 'Work', ['c1'], 'c1', { icon: 'Star', moduleConfig: { a: { b: 1 } } })],
      { activeTabId: 'c1', activeWorkspaceId: 'cw1' },
    )
    const incoming = world(
      [tab('i1', leaf('ip1', tmux('dev'))), tab('i2', leaf('ip2', tmux('api')))],
      [ws('iw1', 'Work', ['i1', 'i2'], 'i2', { icon: 'Moon', moduleConfig: { x: { y: 2 } } })],
    )

    const { next, report } = mergeDeviceState(current, incoming, counter())

    expect(next.workspaces).toHaveLength(1)
    const w = next.workspaces[0]
    expect(w.id).toBe('cw1')
    expect(w.icon).toBe('Star')
    expect(w.moduleConfig).toEqual({ a: { b: 1 } })
    expect(w.activeTabId).toBe('c1')
    expect(w.tabs).toHaveLength(2)
    expect(w.tabs[0]).toBe('c1')
    const added = w.tabs[1]
    expect(added).not.toBe('i2')
    expect(contentsOf(next, added)).toEqual([tmux('api')])
    expect(next.tabOrder).toEqual(['c1', added])
    expect(report).toEqual({ addedWorkspaces: 0, addedTabs: 1, skippedTabs: 1 })
    expectConsistent(next)
  })

  it('adds a missing workspace with fresh ids and the clone of its active tab', () => {
    const current = world([tab('c1', leaf('cp1', tmux('dev')))], [ws('cw1', 'Work', ['c1'], 'c1')])
    const incoming = world(
      [tab('i1', leaf('ip1', tmux('a'))), tab('i2', leaf('ip2', tmux('b')))],
      [ws('iw1', 'Play', ['i1', 'i2'], 'i2', { icon: 'Game', iconWeight: 'bold', moduleConfig: { m: { k: true } } })],
    )

    const { next, report } = mergeDeviceState(current, incoming, counter())

    expect(next.workspaces).toHaveLength(2)
    const added = next.workspaces[1]
    expect(added.id).not.toBe('iw1')
    expect(added.id).not.toBe('cw1')
    expect(added.name).toBe('Play')
    expect(added.icon).toBe('Game')
    expect(added.iconWeight).toBe('bold')
    expect(added.moduleConfig).toEqual({ m: { k: true } })
    expect(added.tabs).toHaveLength(2)
    expect(added.tabs).not.toContain('i1')
    expect(added.tabs).not.toContain('i2')
    expect(contentsOf(next, added.tabs[1])).toEqual([tmux('b')])
    expect(added.activeTabId).toBe(added.tabs[1])
    expect(report).toEqual({ addedWorkspaces: 1, addedTabs: 2, skippedTabs: 0 })
    expectConsistent(next)
  })

  it('new workspace falls back to first added tab when the active tab was skipped, and null when none added', () => {
    const current = world([tab('c1', leaf('cp1', tmux('dup')))], [])
    const incoming = world(
      [tab('i1', leaf('ip1', tmux('fresh'))), tab('i2', leaf('ip2', tmux('dup'))), tab('i3', leaf('ip3', tmux('dup')))],
      [ws('iw1', 'A', ['i1', 'i2'], 'i2'), ws('iw2', 'B', ['i3'], 'i3')],
    )

    const { next } = mergeDeviceState(current, incoming, counter())

    const a = next.workspaces.find((w) => w.name === 'A')!
    const b = next.workspaces.find((w) => w.name === 'B')!
    expect(a.tabs).toHaveLength(1)
    expect(a.activeTabId).toBe(a.tabs[0])
    expect(b.tabs).toEqual([])
    expect(b.activeTabId).toBeNull()
    expectConsistent(next)
  })

  it('skips an incoming tab whose duplicate lives in another current workspace or standalone', () => {
    const current = world(
      [tab('c1', leaf('cp1', tmux('dev'))), tab('c2', leaf('cp2', browser('https://x')))],
      [ws('cw1', 'Other', ['c1'], 'c1')],
    )
    const incoming = world(
      [tab('i1', leaf('ip1', tmux('dev'))), tab('i2', leaf('ip2', browser('https://x')))],
      [ws('iw1', 'Work', ['i1', 'i2'], 'i1')],
    )

    const { next, report } = mergeDeviceState(current, incoming, counter())

    expect(report).toEqual({ addedWorkspaces: 1, addedTabs: 0, skippedTabs: 2 })
    expect(Object.keys(next.tabs).sort()).toEqual(['c1', 'c2'])
    expect(next.workspaces[0].tabs).toEqual(['c1'])
  })

  it('does not add the same key twice within one merge', () => {
    const current = world([], [])
    const incoming = world(
      [tab('i1', leaf('ip1', tmux('dev'))), tab('i2', leaf('ip2', tmux('dev')))],
      [ws('iw1', 'A', ['i1']), ws('iw2', 'B', ['i2'])],
    )
    const { report } = mergeDeviceState(current, incoming, counter())
    expect(report).toEqual({ addedWorkspaces: 2, addedTabs: 1, skippedTabs: 1 })
  })

  it('appends standalone incoming tabs to tabOrder only', () => {
    const current = world([tab('c1', leaf('cp1', tmux('dev')))], [ws('cw1', 'Work', ['c1'], 'c1')])
    const incoming = world(
      [tab('i1', leaf('ip1', browser('https://solo'))), tab('i2', leaf('ip2', tmux('dev')))],
      [],
    )

    const { next, report } = mergeDeviceState(current, incoming, counter())

    const [solo] = findTabByUrl(next, 'https://solo')
    expect(solo).toBeDefined()
    expect(next.tabOrder).toEqual(['c1', solo.id])
    expect(next.workspaces).toEqual(current.workspaces)
    expect(report).toEqual({ addedWorkspaces: 0, addedTabs: 1, skippedTabs: 1 })
    expectConsistent(next)
  })

  it('keeps current activeTabId and activeWorkspaceId', () => {
    const current = world([tab('c1', leaf('cp1', tmux('dev')))], [ws('cw1', 'Work', ['c1'], 'c1')], {
      activeTabId: 'c1',
      activeWorkspaceId: 'cw1',
    })
    const incoming = world([tab('i1', leaf('ip1', tmux('x')))], [ws('iw1', 'New', ['i1'], 'i1')], {
      activeTabId: 'i1',
      activeWorkspaceId: 'iw1',
    })
    const { next } = mergeDeviceState(current, incoming, counter())
    expect(next.activeTabId).toBe('c1')
    expect(next.activeWorkspaceId).toBe('cw1')
  })

  it('merges two incoming workspaces with the same name into one local workspace', () => {
    const current = world([], [])
    const incoming = world(
      [tab('i1', leaf('ip1', tmux('a'))), tab('i2', leaf('ip2', tmux('b')))],
      [ws('iw1', 'Dup', ['i1'], 'i1'), ws('iw2', 'Dup', ['i2'], 'i2')],
    )

    const { next, report } = mergeDeviceState(current, incoming, counter())

    expect(next.workspaces).toHaveLength(1)
    const w = next.workspaces[0]
    expect(w.tabs).toHaveLength(2)
    expect(w.activeTabId).toBe(w.tabs[0])
    expect(report).toEqual({ addedWorkspaces: 1, addedTabs: 2, skippedTabs: 0 })
    expectConsistent(next)
  })

  it('matches workspace names after trimming', () => {
    const current = world([], [ws('cw1', '  Work ', [])])
    const incoming = world([tab('i1', leaf('ip1', tmux('a')))], [ws('iw1', 'Work', ['i1'], 'i1')])
    const { next, report } = mergeDeviceState(current, incoming, counter())
    expect(next.workspaces).toHaveLength(1)
    expect(next.workspaces[0].tabs).toHaveLength(1)
    expect(next.workspaces[0].activeTabId).toBeNull()
    expect(report.addedWorkspaces).toBe(0)
  })

  it('skips new-tab-only tabs without counting them', () => {
    const current = world([], [ws('cw1', 'Work', [])])
    const incoming = world(
      [
        tab('i1', leaf('ip1', { kind: 'new-tab' })),
        tab('i2', {
          type: 'split',
          id: 'is1',
          direction: 'h',
          sizes: [50, 50],
          children: [leaf('ip2', { kind: 'new-tab' }), leaf('ip3', { kind: 'new-tab' })],
        }),
        tab('i3', leaf('ip4', { kind: 'new-tab' })),
      ],
      [ws('iw1', 'Work', ['i1', 'i2'])],
    )
    const { next, report } = mergeDeviceState(current, incoming, counter())
    expect(report).toEqual({ addedWorkspaces: 0, addedTabs: 0, skippedTabs: 0 })
    expect(next.tabOrder).toEqual([])
    expect(next.workspaces[0].tabs).toEqual([])
  })

  it('always adds untitled-editor tabs (null key)', () => {
    const untitled: PaneContent = {
      kind: 'editor',
      source: { type: 'inapp' },
      filePath: 'untitled-1',
      untitled: { name: 'Untitled', suggestedExtension: '.txt', hasBeenRenamed: false },
    }
    const current = world([tab('c1', leaf('cp1', untitled))], [ws('cw1', 'Work', ['c1'])])
    const incoming = world([tab('i1', leaf('ip1', untitled))], [ws('iw1', 'Work', ['i1', 'i1b'])])
    incoming.tabs.i1b = tab('i1b', leaf('ip1b', untitled))
    incoming.tabOrder.push('i1b')

    const { next, report } = mergeDeviceState(current, incoming, counter())
    expect(report).toEqual({ addedWorkspaces: 0, addedTabs: 2, skippedTabs: 0 })
    expect(next.workspaces[0].tabs).toHaveLength(3)
    expectConsistent(next)
  })

  describe('settings scope rewrite in cloned tabs', () => {
    function settingsTab(id: string, workspaceId: string): Tab {
      return tab(id, leaf(`p-${id}`, { kind: 'settings', scope: { workspaceId } }))
    }

    function scopeOf(next: TabWorld, tabId: string): unknown {
      const [c] = contentsOf(next, tabId)
      return c.kind === 'settings' ? c.scope : undefined
    }

    it('re-points to a matched workspace id', () => {
      const current = world([], [ws('cw1', 'Work', [])])
      const incoming = world([settingsTab('i1', 'iw1')], [ws('iw1', 'Work', ['i1'])])
      const { next } = mergeDeviceState(current, incoming, counter())
      expect(scopeOf(next, next.workspaces[0].tabs[0])).toEqual({ workspaceId: 'cw1' })
    })

    it('re-points to a newly added workspace that appears later in the incoming list', () => {
      const current = world([], [ws('cw1', 'Work', [])])
      const incoming = world(
        [settingsTab('i1', 'iw2'), tab('i2', leaf('ip2', tmux('x')))],
        [ws('iw1', 'Work', ['i1']), ws('iw2', 'Later', ['i2'])],
      )
      const { next } = mergeDeviceState(current, incoming, counter())
      const later = next.workspaces.find((w) => w.name === 'Later')!
      expect(later.id).not.toBe('iw2')
      expect(scopeOf(next, next.workspaces[0].tabs[0])).toEqual({ workspaceId: later.id })
    })

    it('becomes global when the scope id is unmapped', () => {
      const current = world([], [ws('cw1', 'Work', [])])
      const incoming = world([settingsTab('i1', 'ghost')], [ws('iw1', 'Work', ['i1'])])
      const { next } = mergeDeviceState(current, incoming, counter())
      expect(scopeOf(next, next.workspaces[0].tabs[0])).toBe('global')
    })

    it('rewrites standalone cloned settings tabs too', () => {
      const current = world([], [ws('cw1', 'Work', [])])
      const incoming = world([settingsTab('i1', 'iw1')], [ws('iw1', 'Work', [])])
      const { next } = mergeDeviceState(current, incoming, counter())
      expect(scopeOf(next, next.tabOrder[0])).toEqual({ workspaceId: 'cw1' })
    })
  })

  it('keeps the tmux rebuild record on cloned panes', () => {
    const rebuild: PaneRebuildRecord = {
      sessionName: 'dev',
      tmuxInstance: 'i1',
      cwd: '/w',
      cwdSource: 'agent-session-start',
      agent: { type: 'cc', sessionId: 's1', tmuxPaneId: '%1', updatedAt: 5 },
      capturedAt: 9,
    }
    const content: PaneContent = { ...tmux('dev'), rebuild } as PaneContent
    const incoming = world([tab('i1', leaf('ip1', content))], [])
    const { next } = mergeDeviceState(world([], []), incoming, counter())
    const [c] = contentsOf(next, next.tabOrder[0])
    expect(c.kind).toBe('tmux-session')
    if (c.kind !== 'tmux-session') return
    expect(c.rebuild).toEqual(rebuild)
    expect(c.cachedName).toBe(c.rebuild?.sessionName)
  })

  it('never collides with current ids or reuses incoming ids', () => {
    // Counter starts at id0 — the first generated values equal existing current ids.
    const current = world(
      [
        tab('id0', {
          type: 'split',
          id: 'id1',
          direction: 'v',
          sizes: [50, 50],
          children: [leaf('id2', tmux('a')), leaf('id3', tmux('b'))],
        }),
        tab('id4', leaf('id5', tmux('c'))),
      ],
      [ws('id6', 'Work', ['id0', 'id4'], 'id0')],
    )
    const incoming = world(
      [
        tab('x0', {
          type: 'split',
          id: 'x1',
          direction: 'h',
          sizes: [30, 70],
          children: [
            leaf('x2', tmux('d')),
            { type: 'split', id: 'x3', direction: 'v', sizes: [50, 50], children: [leaf('x4', tmux('e')), leaf('x5', tmux('f'))] },
          ],
        }),
        tab('x6', leaf('x7', tmux('g'))),
      ],
      [ws('x8', 'Work', ['x0']), ws('x9', 'Other', ['x6'], 'x6')],
    )

    const { next } = mergeDeviceState(current, incoming, counter())

    const ids = allIds(next)
    expect(new Set(ids).size).toBe(ids.length)
    const wsIds = next.workspaces.map((w) => w.id)
    expect(new Set([...wsIds, ...ids]).size).toBe(wsIds.length + ids.length)
    const incomingIds = new Set([...allIds(incoming), 'x8', 'x9'])
    for (const id of [...ids, ...wsIds]) expect(incomingIds.has(id)).toBe(false)
    for (const [key, t] of Object.entries(next.tabs)) expect(t.id).toBe(key)
    // current tabs survive untouched
    expect(next.tabs.id0).toEqual(current.tabs.id0)
    expect(next.tabs.id4).toEqual(current.tabs.id4)
    expect(Object.keys(next.tabs)).toHaveLength(4)
    expectConsistent(next)
  })

  it('does not mutate its inputs', () => {
    const current = world(
      [tab('c1', leaf('cp1', tmux('dev')))],
      [ws('cw1', 'Work', ['c1'], 'c1', { moduleConfig: { a: { b: 1 } } })],
      { activeTabId: 'c1', activeWorkspaceId: 'cw1' },
    )
    const incoming = world(
      [
        tab('i1', leaf('ip1', tmux('api'))),
        tab('i2', leaf('ip2', { kind: 'settings', scope: { workspaceId: 'iw2' } })),
        tab('i3', leaf('ip3', browser('https://solo'))),
      ],
      [ws('iw1', 'Work', ['i1', 'i2'], 'i1'), ws('iw2', 'New', [], null, { moduleConfig: { z: {} } })],
    )
    const currentBefore = structuredClone(current)
    const incomingBefore = structuredClone(incoming)

    const { next } = mergeDeviceState(current, incoming, counter())

    expect(current).toEqual(currentBefore)
    expect(incoming).toEqual(incomingBefore)
    expect(next.tabs).not.toBe(current.tabs)
    expect(next.tabOrder).not.toBe(current.tabOrder)
    expect(next.workspaces).not.toBe(current.workspaces)
    // mutating the result must not leak back into incoming
    const added = next.workspaces.find((w) => w.name === 'New')!
    added.moduleConfig!.z.leak = 1
    expect(incoming).toEqual(incomingBefore)
    expectConsistent(next)
  })
})

describe('cloneTabWithFreshIds', () => {
  it('replaces tab, pane and split ids and keeps content and flags', () => {
    const src = tab('t', {
      type: 'split',
      id: 's',
      direction: 'h',
      sizes: [40, 60],
      children: [leaf('p1', tmux('a')), leaf('p2', browser('https://b'))],
    })
    src.pinned = true
    const before = structuredClone(src)
    const clone = cloneTabWithFreshIds(src, counter())

    expect(src).toEqual(before)
    expect(clone.id).not.toBe('t')
    expect(clone.pinned).toBe(true)
    expect(clone.layout.type).toBe('split')
    if (clone.layout.type !== 'split') return
    expect(clone.layout.id).not.toBe('s')
    expect(clone.layout.sizes).toEqual([40, 60])
    expect(clone.layout.direction).toBe('h')
    const leaves = collectLeaves(clone.layout)
    expect(leaves.map((p) => p.content)).toEqual([tmux('a'), browser('https://b')])
    const ids = [clone.id, clone.layout.id, ...leaves.map((p) => p.id)]
    expect(new Set(ids).size).toBe(4)
  })
})
