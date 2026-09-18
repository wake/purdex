import { describe, it, expect, vi } from 'vitest'
import { getPrimaryPane, findPane, updatePaneInLayout, getLayoutKey, findTabBySessionCode, scanPaneTree, splitAtPane, removePane, countLeaves, collectLeaves, applyLayoutPattern, swapPaneContent, remountLeaf, countPanesOnSession } from './pane-tree'
import type { PaneLayout, Pane, PaneContent } from '../types/tab'

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
