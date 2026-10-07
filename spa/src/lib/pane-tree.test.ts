import { describe, it, expect, vi, afterEach } from 'vitest'
import { getPrimaryPane, findPane, updatePaneInLayout, getLayoutKey, findTabBySessionCode, findTabAndPaneBySessionCode, paneShowsAgent, scanPaneTree, splitAtPane, removePane, countLeaves, collectLeaves, applyLayoutPattern, currentLayoutPattern, swapPaneContent, remountLeaf, countPanesOnSession } from './pane-tree'
import type { PaneLayout, Pane, PaneContent } from '../types/tab'
import { useHostStore } from '../stores/useHostStore'

const DEFAULT_HOST_ORDER = useHostStore.getState().hostOrder

// ── helpers for new tests ──────────────────────────────────────────────────
const mkLeaf = (id: string, kind: string = 'dashboard'): PaneLayout => ({ type: 'leaf', pane: { id, content: { kind } as PaneContent } })
const mkSplit = (id: string, dir: 'h' | 'v', children: PaneLayout[], sizes?: number[]): PaneLayout => ({ type: 'split', id, direction: dir, children, sizes: sizes ?? children.map(() => 100 / children.length) })

const paneA: Pane = { id: 'aaaaaa', content: { kind: 'tmux-session', hostId: 'test-host', sessionCode: 'abc123', mode: 'terminal', cachedName: '', tmuxInstance: '' } }
const paneB: Pane = { id: 'bbbbbb', content: { kind: 'dashboard' } }

const leaf: PaneLayout = { type: 'leaf', pane: paneA }
const split: PaneLayout = {
  type: 'split', id: 'ssssss', direction: 'h',
  children: [{ type: 'leaf', pane: paneA }, { type: 'leaf', pane: paneB }],
  sizes: [50, 50],
}

describe('getPrimaryPane', () => {
  it('returns pane from leaf layout', () => {
    expect(getPrimaryPane(leaf)).toBe(paneA)
  })

  it('returns first leaf pane from split layout', () => {
    expect(getPrimaryPane(split)).toBe(paneA)
  })

  it('returns placeholder pane for corrupted split with empty children', () => {
    const corrupted: PaneLayout = {
      type: 'split', id: 'broken', direction: 'h',
      children: [], sizes: [],
    }
    const result = getPrimaryPane(corrupted)
    expect(result.id).toBe('corrupted')
    expect(result.content).toEqual({ kind: 'new-tab' })
  })
})

describe('findPane', () => {
  it('finds pane by id in leaf', () => {
    expect(findPane(leaf, 'aaaaaa')).toBe(paneA)
  })

  it('finds pane by id in split', () => {
    expect(findPane(split, 'bbbbbb')).toBe(paneB)
  })

  it('returns undefined for unknown id', () => {
    expect(findPane(leaf, 'zzzzzz')).toBeUndefined()
  })
})

describe('updatePaneInLayout', () => {
  it('updates pane content in leaf', () => {
    const updated = updatePaneInLayout(leaf, 'aaaaaa', { kind: 'history' })
    expect(updated.type).toBe('leaf')
    if (updated.type === 'leaf') {
      expect(updated.pane.content).toEqual({ kind: 'history' })
      expect(updated.pane.id).toBe('aaaaaa')
    }
  })

  it('updates pane content in nested split', () => {
    const updated = updatePaneInLayout(split, 'bbbbbb', { kind: 'history' })
    if (updated.type === 'split') {
      const secondChild = updated.children[1]
      if (secondChild.type === 'leaf') {
        expect(secondChild.pane.content).toEqual({ kind: 'history' })
      }
    }
  })

  it('returns same layout if pane not found', () => {
    const updated = updatePaneInLayout(leaf, 'zzzzzz', { kind: 'history' })
    expect(updated).toBe(leaf) // same reference — no change
  })
})

describe('getLayoutKey', () => {
  it('returns pane id for leaf', () => {
    expect(getLayoutKey(leaf)).toBe('aaaaaa')
  })

  it('returns split id for split', () => {
    expect(getLayoutKey(split)).toBe('ssssss')
  })
})

describe('findTabBySessionCode — worker (execution) tabs (spec §8.2)', () => {
  const execLeaf = (executionId: string, host?: string): { layout: PaneLayout } => ({
    layout: { type: 'leaf', pane: { id: 'px' + executionId, content: { kind: 'execution', executionId, ...(host ? { host } : {}) } } },
  })

  it('matches an execution primary pane by exec-<id> and its host', () => {
    const tabs = { t1: { layout: leaf }, t2: execLeaf('e1', 'h1') }
    expect(findTabBySessionCode(tabs, 'h1', 'exec-e1')).toBe('t2')
    expect(findTabBySessionCode(tabs, 'h2', 'exec-e1')).toBeUndefined()
    expect(findTabBySessionCode(tabs, 'h1', 'exec-e2')).toBeUndefined()
    // the bare execution id is not an agent key
    expect(findTabBySessionCode(tabs, 'h1', 'e1')).toBeUndefined()
  })

  it('a host-less execution pane resolves to the first host, like the projection', () => {
    useHostStore.setState({ hostOrder: ['h9', 'h1'] })
    const tabs = { t1: execLeaf('e1') }
    expect(findTabBySessionCode(tabs, 'h9', 'exec-e1')).toBe('t1')
    expect(findTabBySessionCode(tabs, 'h1', 'exec-e1')).toBeUndefined()
  })

  it('an empty-string host resolves to the first host, same as no host', () => {
    useHostStore.setState({ hostOrder: ['h9', 'h1'] })
    const tabs = { t1: { layout: { type: 'leaf', pane: { id: 'px', content: { kind: 'execution', executionId: 'e1', host: '' } } } } as { layout: PaneLayout } }
    expect(findTabBySessionCode(tabs, 'h9', 'exec-e1')).toBe('t1')
    expect(findTabBySessionCode(tabs, 'h1', 'exec-e1')).toBeUndefined()
  })

  // Regression guard: the two tests above overwrite useHostStore's hostOrder
  // directly (no beforeEach seeds it here) — without a reset that mutation
  // leaks into whichever test runs next in this file.
  it('does not leak the overridden hostOrder into later tests', () => {
    expect(useHostStore.getState().hostOrder).toEqual(DEFAULT_HOST_ORDER)
  })

  afterEach(() => {
    useHostStore.setState({ hostOrder: DEFAULT_HOST_ORDER })
  })
})

describe('findTabBySessionCode', () => {
  it('returns undefined when tabs is empty', () => {
    expect(findTabBySessionCode({}, 'test-host', 'abc123')).toBeUndefined()
  })

  it('returns tabId when hostId and session code both match', () => {
    const tabs = {
      tab1: { layout: { type: 'leaf', pane: paneA } as PaneLayout },
    }
    expect(findTabBySessionCode(tabs, 'test-host', 'abc123')).toBe('tab1')
  })

  it('returns undefined when no session code matches', () => {
    const tabs = {
      tab1: { layout: { type: 'leaf', pane: paneA } as PaneLayout },
    }
    expect(findTabBySessionCode(tabs, 'test-host', 'zzz999')).toBeUndefined()
  })

  it('returns undefined when session code matches but hostId does not', () => {
    const tabs = {
      tab1: { layout: { type: 'leaf', pane: paneA } as PaneLayout },
    }
    expect(findTabBySessionCode(tabs, 'other-host', 'abc123')).toBeUndefined()
  })

  it('returns first matching tabId when multiple tabs have different sessions', () => {
    const paneC: Pane = { id: 'cccccc', content: { kind: 'tmux-session', hostId: 'test-host', sessionCode: 'xyz789', mode: 'terminal', cachedName: '', tmuxInstance: '' } }
    const tabs = {
      tab1: { layout: { type: 'leaf', pane: paneA } as PaneLayout },
      tab2: { layout: { type: 'leaf', pane: paneC } as PaneLayout },
    }
    expect(findTabBySessionCode(tabs, 'test-host', 'xyz789')).toBe('tab2')
  })

  it('disambiguates same session code on different hosts by hostId, regardless of tab order', () => {
    // Session codes are a deterministic encoding of tmux's `$N`, so two hosts
    // routinely produce the same code for unrelated sessions.
    const paneHostA: Pane = { id: 'aaaaa1', content: { kind: 'tmux-session', hostId: 'host-a', sessionCode: 'zk16vd', mode: 'terminal', cachedName: '', tmuxInstance: '' } }
    const paneHostB: Pane = { id: 'bbbbb1', content: { kind: 'tmux-session', hostId: 'host-b', sessionCode: 'zk16vd', mode: 'terminal', cachedName: '', tmuxInstance: '' } }
    const aFirst = {
      tabA: { layout: { type: 'leaf', pane: paneHostA } as PaneLayout },
      tabB: { layout: { type: 'leaf', pane: paneHostB } as PaneLayout },
    }
    expect(findTabBySessionCode(aFirst, 'host-a', 'zk16vd')).toBe('tabA')
    expect(findTabBySessionCode(aFirst, 'host-b', 'zk16vd')).toBe('tabB')

    const bFirst = {
      tabB: { layout: { type: 'leaf', pane: paneHostB } as PaneLayout },
      tabA: { layout: { type: 'leaf', pane: paneHostA } as PaneLayout },
    }
    expect(findTabBySessionCode(bFirst, 'host-a', 'zk16vd')).toBe('tabA')
    expect(findTabBySessionCode(bFirst, 'host-b', 'zk16vd')).toBe('tabB')
  })

  it('returns undefined for non-session pane kinds', () => {
    const paneSettings: Pane = { id: 'dddddd', content: { kind: 'settings', scope: 'global' } }
    const paneDashboard: Pane = { id: 'eeeeee', content: { kind: 'dashboard' } }
    const tabs = {
      tab1: { layout: { type: 'leaf', pane: paneSettings } as PaneLayout },
      tab2: { layout: { type: 'leaf', pane: paneDashboard } as PaneLayout },
    }
    expect(findTabBySessionCode(tabs, 'test-host', 'abc123')).toBeUndefined()
  })
})

describe('scanPaneTree', () => {
  it('calls fn for leaf pane', () => {
    const fn = vi.fn()
    scanPaneTree(leaf, fn)
    expect(fn).toHaveBeenCalledTimes(1)
    expect(fn).toHaveBeenCalledWith(paneA)
  })

  it('calls fn for all panes in split layout', () => {
    const fn = vi.fn()
    scanPaneTree(split, fn)
    expect(fn).toHaveBeenCalledTimes(2)
    expect(fn).toHaveBeenCalledWith(paneA)
    expect(fn).toHaveBeenCalledWith(paneB)
  })

  it('calls fn for nested split layouts', () => {
    const paneC: Pane = { id: 'cccccc', content: { kind: 'history' } }
    const nested: PaneLayout = {
      type: 'split', id: 'outer', direction: 'v',
      children: [
        split,
        { type: 'leaf', pane: paneC },
      ],
      sizes: [70, 30],
    }
    const fn = vi.fn()
    scanPaneTree(nested, fn)
    expect(fn).toHaveBeenCalledTimes(3)
    expect(fn).toHaveBeenCalledWith(paneC)
  })
})

// ── splitAtPane ─────────────────────────────────────────────────────────────
describe('splitAtPane', () => {
  it('splits a leaf pane into horizontal split', () => {
    const layout = mkLeaf('p1')
    const result = splitAtPane(layout, 'p1', 'h', { kind: 'dashboard' })
    expect(result.type).toBe('split')
    if (result.type === 'split') {
      expect(result.direction).toBe('h')
      expect(result.children).toHaveLength(2)
      expect(result.children[0]).toBe(layout)
      expect(result.children[1].type).toBe('leaf')
      expect(result.sizes).toEqual([50, 50])
    }
  })

  it('splits nested pane by traversing tree', () => {
    const layout = mkSplit('s1', 'h', [mkLeaf('p1'), mkLeaf('p2')])
    const result = splitAtPane(layout, 'p2', 'v', { kind: 'history' })
    expect(result.type).toBe('split')
    if (result.type === 'split') {
      const second = result.children[1]
      expect(second.type).toBe('split')
      if (second.type === 'split') {
        expect(second.direction).toBe('v')
        expect(second.children).toHaveLength(2)
      }
    }
  })

  it('returns layout unchanged when paneId not found', () => {
    const layout = mkLeaf('p1')
    const result = splitAtPane(layout, 'notexist', 'h', { kind: 'dashboard' })
    expect(result).toBe(layout)
  })
})

// ── removePane ───────────────────────────────────────────────────────────────
describe('removePane', () => {
  it('returns null when removing the only leaf', () => {
    expect(removePane(mkLeaf('p1'), 'p1')).toBeNull()
  })

  it('promotes sibling when one child is removed from a 2-child split', () => {
    const layout = mkSplit('s1', 'h', [mkLeaf('p1'), mkLeaf('p2')])
    const result = removePane(layout, 'p1')
    expect(result).toEqual(mkLeaf('p2'))
  })

  it('redistributes sizes for 3-child split after removal', () => {
    const layout = mkSplit('s1', 'h', [mkLeaf('p1'), mkLeaf('p2'), mkLeaf('p3')], [20, 40, 40])
    const result = removePane(layout, 'p1')
    expect(result?.type).toBe('split')
    if (result?.type === 'split') {
      expect(result.children).toHaveLength(2)
      // sizes should be normalized to sum 100
      const total = result.sizes.reduce((a, b) => a + b, 0)
      expect(Math.round(total)).toBe(100)
    }
  })

  it('returns layout unchanged when paneId not found', () => {
    const layout = mkSplit('s1', 'h', [mkLeaf('p1'), mkLeaf('p2')])
    const result = removePane(layout, 'notexist')
    expect(result).toBe(layout)
  })

  it('removes a pane deep in nested split', () => {
    const layout = mkSplit('s1', 'h', [
      mkLeaf('p1'),
      mkSplit('s2', 'v', [mkLeaf('p2'), mkLeaf('p3')]),
    ])
    const result = removePane(layout, 'p3')
    expect(result).not.toBeNull()
    if (!result || result.type !== 'split') throw new Error('expected split')
    expect(result.children).toHaveLength(2)
    // p1 untouched, s2 collapsed to just p2
    expect(result.children[0]).toEqual(mkLeaf('p1'))
    expect(result.children[1]).toEqual(mkLeaf('p2'))
  })
})

// ── countLeaves ──────────────────────────────────────────────────────────────
describe('countLeaves', () => {
  it('returns 1 for a leaf', () => {
    expect(countLeaves(mkLeaf('p1'))).toBe(1)
  })

  it('counts leaves in nested splits', () => {
    const layout = mkSplit('s1', 'v', [
      mkSplit('s2', 'h', [mkLeaf('p1'), mkLeaf('p2')]),
      mkLeaf('p3'),
    ])
    expect(countLeaves(layout)).toBe(3)
  })
})

// ── collectLeaves ─────────────────────────────────────────────────────────────
describe('collectLeaves', () => {
  it('collects all leaf panes in order', () => {
    const layout = mkSplit('s1', 'h', [mkLeaf('p1'), mkLeaf('p2'), mkLeaf('p3')])
    const panes = collectLeaves(layout)
    expect(panes.map((p) => p.id)).toEqual(['p1', 'p2', 'p3'])
  })
})

// ── swapPaneContent ───────────────────────────────────────────────────────────
describe('swapPaneContent', () => {
  it('swaps content between two leaf panes', () => {
    const layout = mkSplit('s1', 'h', [mkLeaf('p1', 'dashboard'), mkLeaf('p2', 'history')])
    const result = swapPaneContent(layout, 'p1', 'p2')
    const leaves = collectLeaves(result)
    expect(leaves[0].id).toBe('p1')
    expect(leaves[0].content.kind).toBe('history')
    expect(leaves[1].id).toBe('p2')
    expect(leaves[1].content.kind).toBe('dashboard')
  })

  it('returns same layout if either pane not found', () => {
    const layout = mkSplit('s1', 'h', [mkLeaf('p1', 'dashboard'), mkLeaf('p2', 'history')])
    expect(swapPaneContent(layout, 'p1', 'missing')).toBe(layout)
    expect(swapPaneContent(layout, 'missing', 'p2')).toBe(layout)
  })

  it('works in nested split layouts', () => {
    const layout = mkSplit('s1', 'v', [
      mkSplit('s2', 'h', [mkLeaf('p1', 'dashboard'), mkLeaf('p2', 'history')]),
      mkLeaf('p3', 'hosts'),
    ])
    const result = swapPaneContent(layout, 'p1', 'p3')
    const leaves = collectLeaves(result)
    expect(leaves.find((l) => l.id === 'p1')!.content.kind).toBe('hosts')
    expect(leaves.find((l) => l.id === 'p3')!.content.kind).toBe('dashboard')
  })
})

// ── applyLayoutPattern ────────────────────────────────────────────────────────
describe('applyLayoutPattern', () => {
  it('single flattens to first leaf', () => {
    const layout = mkSplit('s1', 'h', [mkLeaf('p1'), mkLeaf('p2')])
    const result = applyLayoutPattern(layout, 'single')
    expect(result.type).toBe('leaf')
    if (result.type === 'leaf') {
      expect(result.pane.id).toBe('p1')
    }
  })

  it('split-h creates a 2-child horizontal split', () => {
    const result = applyLayoutPattern(mkLeaf('p1'), 'split-h')
    expect(result.type).toBe('split')
    if (result.type === 'split') {
      expect(result.direction).toBe('h')
      expect(result.children).toHaveLength(2)
      expect(result.sizes).toEqual([50, 50])
    }
  })

  it('split-h preserves existing pane ids', () => {
    const layout = mkSplit('s1', 'h', [mkLeaf('p1'), mkLeaf('p2')])
    const result = applyLayoutPattern(layout, 'split-h')
    if (result.type === 'split') {
      const ids = result.children.map((c) => c.type === 'leaf' ? c.pane.id : null)
      expect(ids).toEqual(['p1', 'p2'])
    }
  })

  // ── keepIds: the exact survivor set (shell cleanup spec §10, rule D.1a) ──
  const leafIds = (l: PaneLayout) => collectLeaves(l).map((p) => p.id)

  it('keepIds: single keeps the one listed pane, not the first leaf', () => {
    const layout = mkSplit('s1', 'h', [mkLeaf('p1'), mkLeaf('p2'), mkLeaf('p3')])
    const result = applyLayoutPattern(layout, 'single', ['p2'])
    expect(result.type).toBe('leaf')
    expect(leafIds(result)).toEqual(['p2'])
  })

  it('keepIds: survivors are placed in layout order, whatever order keepIds lists them in', () => {
    const layout = mkSplit('s1', 'v', [mkLeaf('p1'), mkSplit('s2', 'h', [mkLeaf('p2'), mkLeaf('p3')])])
    const result = applyLayoutPattern(layout, 'split-h', ['p3', 'p1'])
    expect(result.type === 'split' && result.direction).toBe('h')
    expect(leafIds(result)).toEqual(['p1', 'p3'])
  })

  it('keepIds: a missing slot is filled with a fresh new-tab pane, and nothing outside keepIds survives', () => {
    const layout = mkSplit('s1', 'h', [mkLeaf('p1'), mkLeaf('p2'), mkLeaf('p3')])
    const result = applyLayoutPattern(layout, 'split-v', ['p3'])
    const leaves = collectLeaves(result)
    expect(leaves).toHaveLength(2)
    expect(leaves[0].id).toBe('p3')
    expect(leaves[1].content).toEqual({ kind: 'new-tab' })
    expect(['p1', 'p2']).not.toContain(leaves[1].id)
  })

  it('keepIds: an empty set leaves only blank panes', () => {
    const result = applyLayoutPattern(mkSplit('s1', 'h', [mkLeaf('p1'), mkLeaf('p2')]), 'single', [])
    expect(result.type === 'leaf' && result.pane.content).toEqual({ kind: 'new-tab' })
    expect(leafIds(result)).not.toContain('p1')
  })

  it('keepIds: ids not in the layout are ignored', () => {
    const result = applyLayoutPattern(mkSplit('s1', 'h', [mkLeaf('p1'), mkLeaf('p2')]), 'split-v', ['gone', 'p2'])
    const leaves = collectLeaves(result)
    expect(leaves[0].id).toBe('p2')
    expect(leaves[1].content).toEqual({ kind: 'new-tab' })
  })

  it('keepIds: survivors keep their pane object (content and id) untouched', () => {
    const layout = mkSplit('s1', 'h', [{ type: 'leaf', pane: paneB }, { type: 'leaf', pane: paneA }])
    const result = applyLayoutPattern(layout, 'single', [paneA.id])
    expect(result.type === 'leaf' && result.pane).toBe(paneA)
  })
})

// ── currentLayoutPattern ──────────────────────────────────────────────────────
describe('currentLayoutPattern', () => {
  it('a leaf → single', () => {
    expect(currentLayoutPattern(mkLeaf('p1'))).toBe('single')
  })

  it('a horizontal split of two leaves → split-h', () => {
    expect(currentLayoutPattern(mkSplit('s1', 'h', [mkLeaf('p1'), mkLeaf('p2')]))).toBe('split-h')
  })

  it('a vertical split of two leaves → split-v', () => {
    expect(currentLayoutPattern(mkSplit('s1', 'v', [mkLeaf('p1'), mkLeaf('p2')]))).toBe('split-v')
  })

  it('a split with three leaves → null', () => {
    expect(currentLayoutPattern(mkSplit('s1', 'h', [mkLeaf('p1'), mkLeaf('p2'), mkLeaf('p3')]))).toBeNull()
  })

  it('a split of two whose child is itself a split → null', () => {
    expect(currentLayoutPattern(mkSplit('s1', 'h', [mkLeaf('p1'), mkSplit('s2', 'v', [mkLeaf('p2'), mkLeaf('p3')])]))).toBeNull()
  })

  it('a split with one leaf child → null', () => {
    expect(currentLayoutPattern(mkSplit('s1', 'v', [mkLeaf('p1')]))).toBeNull()
  })
})

describe('remountLeaf', () => {
  it('assigns a fresh pane id to the target leaf, content unchanged', () => {
    const content: PaneContent = { kind: 'image-preview', source: { type: 'inapp' }, filePath: '/buffer/a.png' }
    const layout: PaneLayout = { type: 'leaf', pane: { id: 'p1', content } }
    const res = remountLeaf(layout, 'p1')
    expect(res).not.toBeNull()
    expect(res!.newPaneId).not.toBe('p1')
    if (res!.layout.type === 'leaf') {
      expect(res!.layout.pane.id).toBe(res!.newPaneId)
      // Content is preserved verbatim (same object reference is fine).
      expect(res!.layout.pane.content).toBe(content)
    }
  })

  it('returns null when the paneId is not present', () => {
    const layout: PaneLayout = { type: 'leaf', pane: { id: 'p1', content: { kind: 'dashboard' } } }
    expect(remountLeaf(layout, 'nope')).toBeNull()
  })

  it('remounts a leaf in place inside a split, leaving the sibling untouched', () => {
    const layout = mkSplit('s1', 'h', [mkLeaf('p1'), mkLeaf('p2')])
    const res = remountLeaf(layout, 'p1')
    expect(res).not.toBeNull()
    const { layout: next, newPaneId } = res!
    if (next.type === 'split' && layout.type === 'split') {
      // Same tree position (index 0), new id.
      const first = next.children[0]
      const second = next.children[1]
      expect(first.type === 'leaf' && first.pane.id).toBe(newPaneId)
      // Sibling is the exact same object (untouched).
      expect(second).toBe(layout.children[1])
      // Split shape preserved.
      expect(next.id).toBe('s1')
      expect(next.sizes).toEqual(layout.sizes)
    }
  })
})

describe('countPanesOnSession (exec-to-terminal spec §4.3)', () => {
  const sess = (id: string, hostId: string, sessionCode: string): PaneLayout =>
    ({ type: 'leaf', pane: { id, content: { kind: 'tmux-session', hostId, sessionCode, mode: 'terminal', cachedName: '', tmuxInstance: '' } } })

  it('counts every pane on the host+code across tabs and nested splits, minus the excluded one', () => {
    const tabs = {
      t1: { layout: mkSplit('s1', 'h', [sess('p1', 'h', 'abc123'), mkSplit('s2', 'v', [sess('p2', 'h', 'abc123'), mkLeaf('p3')])]) },
      t2: { layout: sess('p4', 'h', 'abc123') },
      t3: { layout: sess('p5', 'h', 'other1') },
      t4: { layout: sess('p6', 'h2', 'abc123') },
    }
    expect(countPanesOnSession(tabs, 'h', 'abc123')).toBe(3)
    expect(countPanesOnSession(tabs, 'h', 'abc123', 'p1')).toBe(2)
    expect(countPanesOnSession(tabs, 'h', 'abc123', 'p6')).toBe(3)
    expect(countPanesOnSession(tabs, 'h', 'zzz')).toBe(0)
    expect(countPanesOnSession({}, 'h', 'abc123')).toBe(0)
  })
})

// #1840: the notification dispatcher's lookups walk every pane of a tab, not just its primary one.
describe('findTabAndPaneBySessionCode / paneShowsAgent (#1840)', () => {
  const sess = (id: string, hostId: string, sessionCode: string): PaneLayout =>
    ({ type: 'leaf', pane: { id, content: { kind: 'tmux-session', hostId, sessionCode, mode: 'terminal', cachedName: '', tmuxInstance: '' } } })
  const exec = (id: string, executionId: string, host?: string): PaneLayout =>
    ({ type: 'leaf', pane: { id, content: { kind: 'execution', executionId, ...(host !== undefined ? { host } : {}) } } })

  afterEach(() => {
    useHostStore.setState({ hostOrder: DEFAULT_HOST_ORDER })
  })

  it('returns undefined for no tabs and for tabs that do not show the key', () => {
    expect(findTabAndPaneBySessionCode({}, 'h', 'abc123')).toBeUndefined()
    const tabs = { t1: { layout: mkSplit('s1', 'h', [mkLeaf('p1'), sess('p2', 'h', 'other1')]) } }
    expect(findTabAndPaneBySessionCode(tabs, 'h', 'abc123')).toBeUndefined()
  })

  it('a primary leaf matches with its pane id', () => {
    const tabs = { t1: { layout: sess('p1', 'h', 'abc123') } }
    expect(findTabAndPaneBySessionCode(tabs, 'h', 'abc123')).toEqual({ tabId: 't1', paneId: 'p1' })
  })

  it('finds a tmux agent in the SECONDARY pane of a split', () => {
    const tabs = { t1: { layout: mkSplit('s1', 'h', [mkLeaf('p1'), sess('p2', 'h', 'abc123')]) } }
    expect(findTabAndPaneBySessionCode(tabs, 'h', 'abc123')).toEqual({ tabId: 't1', paneId: 'p2' })
  })

  it('finds an agent at any depth of nested splits', () => {
    const tabs = {
      t1: { layout: mkSplit('s1', 'h', [mkLeaf('p1'), mkSplit('s2', 'v', [mkLeaf('p2'), mkSplit('s3', 'h', [mkLeaf('p3'), sess('p4', 'h', 'abc123')])])]) },
    }
    expect(findTabAndPaneBySessionCode(tabs, 'h', 'abc123')).toEqual({ tabId: 't1', paneId: 'p4' })
  })

  it('prefers the primary pane when the same tab shows the key twice', () => {
    const tabs = { t1: { layout: mkSplit('s1', 'h', [sess('p1', 'h', 'abc123'), sess('p2', 'h', 'abc123')]) } }
    expect(findTabAndPaneBySessionCode(tabs, 'h', 'abc123')).toEqual({ tabId: 't1', paneId: 'p1' })
  })

  it('prefers a tab whose PRIMARY pane shows the key over an earlier tab that shows it in a secondary pane', () => {
    const tabs = {
      t1: { layout: mkSplit('s1', 'h', [mkLeaf('p1'), sess('p2', 'h', 'abc123')]) },
      t2: { layout: mkSplit('s2', 'v', [sess('p3', 'h', 'abc123'), mkLeaf('p4')]) },
    }
    expect(findTabAndPaneBySessionCode(tabs, 'h', 'abc123')).toEqual({ tabId: 't2', paneId: 'p3' })
  })

  it('without a primary match, the first secondary match in tab order wins', () => {
    const tabs = {
      t1: { layout: mkSplit('s1', 'h', [mkLeaf('p1'), sess('p2', 'h', 'abc123')]) },
      t2: { layout: mkSplit('s2', 'v', [mkLeaf('p3'), sess('p4', 'h', 'abc123')]) },
    }
    expect(findTabAndPaneBySessionCode(tabs, 'h', 'abc123')).toEqual({ tabId: 't1', paneId: 'p2' })
  })

  it('disambiguates the same code on two hosts by hostId, in secondary panes, regardless of tab order', () => {
    const a = { layout: mkSplit('sa', 'h', [mkLeaf('pa1'), sess('pa2', 'host-a', 'zk16vd')]) }
    const b = { layout: mkSplit('sb', 'h', [mkLeaf('pb1'), sess('pb2', 'host-b', 'zk16vd')]) }
    for (const tabs of [{ tA: a, tB: b }, { tB: b, tA: a }]) {
      expect(findTabAndPaneBySessionCode(tabs, 'host-a', 'zk16vd')).toEqual({ tabId: 'tA', paneId: 'pa2' })
      expect(findTabAndPaneBySessionCode(tabs, 'host-b', 'zk16vd')).toEqual({ tabId: 'tB', paneId: 'pb2' })
      expect(findTabAndPaneBySessionCode(tabs, 'host-c', 'zk16vd')).toBeUndefined()
    }
  })

  it('a worker in a secondary pane matches exec-<id> on its host hint only', () => {
    const tabs = { t1: { layout: mkSplit('s1', 'h', [mkLeaf('p1'), exec('p2', 'e1', 'h2')]) } }
    expect(findTabAndPaneBySessionCode(tabs, 'h2', 'exec-e1')).toEqual({ tabId: 't1', paneId: 'p2' })
    expect(findTabAndPaneBySessionCode(tabs, 'h1', 'exec-e1')).toBeUndefined()
    expect(findTabAndPaneBySessionCode(tabs, 'h2', 'exec-e2')).toBeUndefined()
    // the bare execution id is not an agent key
    expect(findTabAndPaneBySessionCode(tabs, 'h2', 'e1')).toBeUndefined()
  })

  it('a host-less or empty-host worker in a secondary pane resolves to the first host, like the projection', () => {
    useHostStore.setState({ hostOrder: ['h9', 'h1'] })
    for (const host of [undefined, '']) {
      const tabs = { t1: { layout: mkSplit('s1', 'h', [mkLeaf('p1'), exec('p2', 'e1', host)]) } }
      expect(findTabAndPaneBySessionCode(tabs, 'h9', 'exec-e1')).toEqual({ tabId: 't1', paneId: 'p2' })
      expect(findTabAndPaneBySessionCode(tabs, 'h1', 'exec-e1')).toBeUndefined()
    }
  })

  it('a tmux pane on code e1 is not the worker exec-e1, and a worker abc123 is not the tmux code abc123', () => {
    const tabs = { t1: { layout: mkSplit('s1', 'h', [sess('p1', 'h', 'e1'), exec('p2', 'abc123', 'h')]) } }
    expect(findTabAndPaneBySessionCode(tabs, 'h', 'exec-e1')).toBeUndefined()
    expect(findTabAndPaneBySessionCode(tabs, 'h', 'abc123')).toBeUndefined()
    expect(findTabAndPaneBySessionCode(tabs, 'h', 'exec-abc123')).toEqual({ tabId: 't1', paneId: 'p2' })
  })

  it('a corrupted (childless) split matches nothing and does not throw', () => {
    const tabs = { t1: { layout: { type: 'split', id: 'broken', direction: 'h', children: [], sizes: [] } as PaneLayout } }
    expect(findTabAndPaneBySessionCode(tabs, 'h', 'abc123')).toBeUndefined()
  })

  it('agrees with findTabBySessionCode on every single-leaf tab (same matching rules)', () => {
    useHostStore.setState({ hostOrder: ['h9', 'h1'] })
    const tabs = {
      tA: { layout: sess('pA', 'host-a', 'zk16vd') },
      tB: { layout: sess('pB', 'host-b', 'zk16vd') },
      tE: { layout: exec('pE', 'e1', 'h2') },
      tN: { layout: exec('pN', 'e2') },
      tZ: { layout: exec('pZ', 'e3', '') },
      tD: { layout: mkLeaf('pD') },
    }
    const probes: Array<[string, string]> = [
      ['host-a', 'zk16vd'], ['host-b', 'zk16vd'], ['host-c', 'zk16vd'],
      ['h2', 'exec-e1'], ['h1', 'exec-e1'], ['h2', 'e1'],
      ['h9', 'exec-e2'], ['h1', 'exec-e2'], ['h9', 'exec-e3'], ['h1', 'exec-e3'], ['h', 'abc123'],
    ]
    for (const [hostId, code] of probes) {
      expect(findTabAndPaneBySessionCode(tabs, hostId, code)?.tabId).toBe(findTabBySessionCode(tabs, hostId, code))
    }
  })

  it('paneShowsAgent applies the same rules to one pane content', () => {
    useHostStore.setState({ hostOrder: ['h9'] })
    const tmux: PaneContent = { kind: 'tmux-session', hostId: 'h', sessionCode: 'abc123', mode: 'terminal', cachedName: '', tmuxInstance: '' }
    expect(paneShowsAgent(tmux, 'h', 'abc123')).toBe(true)
    expect(paneShowsAgent(tmux, 'h2', 'abc123')).toBe(false)
    expect(paneShowsAgent(tmux, 'h', 'abc124')).toBe(false)
    expect(paneShowsAgent({ kind: 'execution', executionId: 'e1', host: 'h2' }, 'h2', 'exec-e1')).toBe(true)
    expect(paneShowsAgent({ kind: 'execution', executionId: 'e1', host: 'h2' }, 'h9', 'exec-e1')).toBe(false)
    expect(paneShowsAgent({ kind: 'execution', executionId: 'e1' }, 'h9', 'exec-e1')).toBe(true)
    expect(paneShowsAgent({ kind: 'dashboard' }, 'h', 'abc123')).toBe(false)
  })
})

// #1840 review A2: session codes encode tmux's `$N`, which a restarted tmux server hands out again, so an ENDED pane's
// code can be a NEW live session's. An ended (terminated) pane shows no agent: it never matches.
describe('an ended tmux pane shows no agent (#1840 A2)', () => {
  const REASONS = ['session-closed', 'tmux-restarted', 'host-removed', 'conversation-ended'] as const
  const ended = (id: string, code: string, reason: (typeof REASONS)[number] = 'tmux-restarted'): PaneLayout =>
    ({ type: 'leaf', pane: { id, content: { kind: 'tmux-session', hostId: 'h', sessionCode: code, mode: 'terminal', cachedName: '', tmuxInstance: 'old', terminated: reason } } })
  const live = (id: string, code: string): PaneLayout =>
    ({ type: 'leaf', pane: { id, content: { kind: 'tmux-session', hostId: 'h', sessionCode: code, mode: 'terminal', cachedName: '', tmuxInstance: 'new' } } })

  it('paneShowsAgent is false for a terminated tmux pane, whatever the reason', () => {
    for (const reason of REASONS) {
      const layout = ended('p1', 'abc123', reason)
      expect(layout.type === 'leaf' && paneShowsAgent(layout.pane.content, 'h', 'abc123')).toBe(false)
    }
  })

  it('a terminated primary and a live secondary on the same code → the live pane', () => {
    const tabs = { t1: { layout: mkSplit('s1', 'h', [ended('p1', 'abc123'), live('p2', 'abc123')]) } }
    expect(findTabAndPaneBySessionCode(tabs, 'h', 'abc123')).toEqual({ tabId: 't1', paneId: 'p2' })
  })

  it('a terminated primary in an earlier tab does not win over a live secondary in a later tab', () => {
    const tabs = {
      t1: { layout: ended('p1', 'abc123') },
      t2: { layout: mkSplit('s2', 'h', [mkLeaf('p2'), live('p3', 'abc123')]) },
    }
    expect(findTabAndPaneBySessionCode(tabs, 'h', 'abc123')).toEqual({ tabId: 't2', paneId: 'p3' })
  })

  it('a terminated pane alone → not found, as primary or as a secondary pane', () => {
    expect(findTabAndPaneBySessionCode({ t1: { layout: ended('p1', 'abc123') } }, 'h', 'abc123')).toBeUndefined()
    expect(findTabAndPaneBySessionCode({ t1: { layout: mkSplit('s1', 'h', [mkLeaf('p1'), ended('p2', 'abc123')]) } }, 'h', 'abc123')).toBeUndefined()
  })
})
