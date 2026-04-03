import { describe, it, expect, vi } from 'vitest'
import { getPrimaryPane, findPane, updatePaneInLayout, getLayoutKey, findTabBySessionCode, scanPaneTree } from './pane-tree'
import type { PaneLayout, Pane } from '../types/tab'

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
    expect(findTabBySessionCode({}, 'abc123')).toBeUndefined()
  })

  it('returns tabId when session code matches', () => {
    const tabs = {
      tab1: { layout: { type: 'leaf', pane: paneA } as PaneLayout },
    }
    expect(findTabBySessionCode(tabs, 'abc123')).toBe('tab1')
  })

  it('returns undefined when no session code matches', () => {
    const tabs = {
      tab1: { layout: { type: 'leaf', pane: paneA } as PaneLayout },
    }
    expect(findTabBySessionCode(tabs, 'zzz999')).toBeUndefined()
  })

  it('returns first matching tabId when multiple tabs have different sessions', () => {
    const paneC: Pane = { id: 'cccccc', content: { kind: 'tmux-session', hostId: 'test-host', sessionCode: 'xyz789', mode: 'terminal', cachedName: '', tmuxInstance: '' } }
    const tabs = {
      tab1: { layout: { type: 'leaf', pane: paneA } as PaneLayout },
      tab2: { layout: { type: 'leaf', pane: paneC } as PaneLayout },
    }
    expect(findTabBySessionCode(tabs, 'xyz789')).toBe('tab2')
  })

  it('returns undefined for non-session pane kinds', () => {
    const paneSettings: Pane = { id: 'dddddd', content: { kind: 'settings', scope: 'global' } }
    const paneDashboard: Pane = { id: 'eeeeee', content: { kind: 'dashboard' } }
    const tabs = {
      tab1: { layout: { type: 'leaf', pane: paneSettings } as PaneLayout },
      tab2: { layout: { type: 'leaf', pane: paneDashboard } as PaneLayout },
    }
    expect(findTabBySessionCode(tabs, 'abc123')).toBeUndefined()
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
