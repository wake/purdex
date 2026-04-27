import { describe, it, expect } from 'vitest'
import { inferWorkspaceHostId } from './infer-workspace-host-id'
import type { Tab, Workspace, PaneLayout } from '../types/tab'

function tmuxLeaf(hostId: string, sessionCode = 'sess'): PaneLayout {
  return {
    type: 'leaf',
    pane: {
      id: `pane-${hostId}-${sessionCode}`,
      content: {
        kind: 'tmux-session',
        hostId,
        sessionCode,
        mode: 'terminal',
        cachedName: 'x',
        tmuxInstance: 'default',
      },
    },
  }
}

function newTabLeaf(): PaneLayout {
  return { type: 'leaf', pane: { id: 'p-newtab', content: { kind: 'new-tab' } } }
}

function splitH(...children: PaneLayout[]): PaneLayout {
  return { type: 'split', id: 's', direction: 'h', children, sizes: children.map(() => 1 / children.length) }
}

function tab(id: string, layout: PaneLayout): Tab {
  return { id, pinned: false, locked: false, createdAt: 0, layout }
}

function ws(opts: { id?: string; tabs: string[]; activeTabId?: string | null }): Workspace {
  return {
    id: opts.id ?? 'w1',
    name: 'W',
    tabs: opts.tabs,
    activeTabId: opts.activeTabId ?? null,
    moduleConfig: {},
  }
}

describe('inferWorkspaceHostId', () => {
  it('returns the hostId of a single tmux-session tab', () => {
    const tabs = { t1: tab('t1', tmuxLeaf('h1')) }
    const w = ws({ tabs: ['t1'], activeTabId: 't1' })
    expect(inferWorkspaceHostId(w, tabs)).toBe('h1')
  })

  it('returns the only candidate when multiple tabs share the same host', () => {
    const tabs = {
      t1: tab('t1', tmuxLeaf('h1')),
      t2: tab('t2', tmuxLeaf('h1')),
      t3: tab('t3', tmuxLeaf('h1')),
    }
    const w = ws({ tabs: ['t1', 't2', 't3'], activeTabId: 't1' })
    expect(inferWorkspaceHostId(w, tabs)).toBe('h1')
  })

  it('returns the majority host when one host clearly dominates', () => {
    const tabs = {
      t1: tab('t1', tmuxLeaf('h1')),
      t2: tab('t2', tmuxLeaf('h1')),
      t3: tab('t3', tmuxLeaf('h2')),
    }
    const w = ws({ tabs: ['t1', 't2', 't3'], activeTabId: 't3' })
    expect(inferWorkspaceHostId(w, tabs)).toBe('h1')
  })

  it('on tie, prefers the active tab hostId when active is tmux-session and in the winners set', () => {
    const tabs = {
      t1: tab('t1', tmuxLeaf('h1')),
      t2: tab('t2', tmuxLeaf('h2')),
    }
    const w = ws({ tabs: ['t1', 't2'], activeTabId: 't2' })
    expect(inferWorkspaceHostId(w, tabs)).toBe('h2')
  })

  it('on tie, falls back to first winner in tabs order when active tab host is NOT in winners', () => {
    const cleanTie = {
      t1: tab('t1', tmuxLeaf('h1')),
      t2: tab('t2', tmuxLeaf('h2')),
      t3: tab('t3', newTabLeaf()),
    }
    const w3 = ws({ tabs: ['t1', 't2', 't3'], activeTabId: 't3' })
    expect(inferWorkspaceHostId(w3, cleanTie)).toBe('h1')
  })

  // codex round-1 B1 — minority-active fixture
  it('on tie, IGNORES active tab hostId when active is a tmux-session of a minority host', () => {
    // h1 count=2 (t1 + t2 split), h2 count=2 (t3 + t2 split), h3 count=1 (t4)
    // → tie between h1 and h2; h3 is minority and NOT in winners.
    // active tab t4 is h3 (minority). Tie-break A must NOT pick h3 just because
    // it is the active tab's host; instead it falls through to Tie-break B
    // (first winner in tabs order) → h1.
    const tabs = {
      t1: tab('t1', tmuxLeaf('h1')),
      t2: tab('t2', splitH(tmuxLeaf('h1'), tmuxLeaf('h2'))),
      t3: tab('t3', tmuxLeaf('h2')),
      t4: tab('t4', tmuxLeaf('h3')),
    }
    const w = ws({ tabs: ['t1', 't2', 't3', 't4'], activeTabId: 't4' })
    expect(inferWorkspaceHostId(w, tabs)).toBe('h1')
  })

  it('returns null when workspace has no tmux-session tabs', () => {
    const tabs = {
      t1: tab('t1', newTabLeaf()),
      t2: tab('t2', { type: 'leaf', pane: { id: 'p2', content: { kind: 'dashboard' } } }),
    }
    const w = ws({ tabs: ['t1', 't2'], activeTabId: 't1' })
    expect(inferWorkspaceHostId(w, tabs)).toBeNull()
  })

  it('returns null when workspace.tabs is empty', () => {
    const w = ws({ tabs: [], activeTabId: null })
    expect(inferWorkspaceHostId(w, {})).toBeNull()
  })

  it('skips missing tab ids that are not present in the tabs map', () => {
    const tabs = { t1: tab('t1', tmuxLeaf('h1')) }
    const w = ws({ tabs: ['t1', 't-missing'], activeTabId: 't1' })
    expect(inferWorkspaceHostId(w, tabs)).toBe('h1')
  })

  it('recursively collects hostIds from nested split layouts', () => {
    const tabs = {
      t1: tab('t1', splitH(tmuxLeaf('h1'), splitH(tmuxLeaf('h1'), tmuxLeaf('h2')))),
    }
    const w = ws({ tabs: ['t1'], activeTabId: 't1' })
    // h1 count=2, h2 count=1 → h1 wins
    expect(inferWorkspaceHostId(w, tabs)).toBe('h1')
  })
})
