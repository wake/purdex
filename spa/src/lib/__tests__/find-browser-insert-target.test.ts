import { describe, it, expect } from 'vitest'
import { findBrowserInsertTarget } from '../find-browser-insert-target'
import type { Tab } from '../../types/tab'

function makeTab(id: string, kind: string): Tab {
  return {
    id,
    pinned: false,
    locked: false,
    createdAt: 0,
    layout: { type: 'leaf', pane: { id: `p-${id}`, content: kind === 'browser' ? { kind: 'browser', url: 'https://example.com' } : { kind: 'tmux-session', hostId: 'h', sessionCode: 's', mode: 'terminal', cachedName: '', tmuxInstance: '' } } },
  }
}

function makeTabs(...specs: [string, string][]): Record<string, Tab> {
  const result: Record<string, Tab> = {}
  for (const [id, kind] of specs) {
    result[id] = makeTab(id, kind)
  }
  return result
}

describe('findBrowserInsertTarget', () => {
  it('returns nearest browser tab to the right', () => {
    const tabs = makeTabs(['t1', 'terminal'], ['t2', 'terminal'], ['b1', 'browser'], ['t3', 'terminal'])
    const result = findBrowserInsertTarget(['t1', 't2', 'b1', 't3'], 't1', tabs)
    expect(result).toBe('b1')
  })

  it('skips non-browser tabs when scanning right', () => {
    const tabs = makeTabs(['t1', 'terminal'], ['t2', 'terminal'], ['t3', 'terminal'], ['b1', 'browser'])
    const result = findBrowserInsertTarget(['t1', 't2', 't3', 'b1'], 't1', tabs)
    expect(result).toBe('b1')
  })

  it('returns activeTabId when no browser tab to the right', () => {
    const tabs = makeTabs(['b1', 'browser'], ['t1', 'terminal'], ['t2', 'terminal'])
    const result = findBrowserInsertTarget(['b1', 't1', 't2'], 't1', tabs)
    expect(result).toBe('t1')
  })

  it('returns activeTabId when active tab is last', () => {
    const tabs = makeTabs(['t1', 'terminal'], ['t2', 'terminal'])
    const result = findBrowserInsertTarget(['t1', 't2'], 't2', tabs)
    expect(result).toBe('t2')
  })

  it('returns activeTabId when not found in order', () => {
    const tabs = makeTabs(['t1', 'terminal'])
    const result = findBrowserInsertTarget(['t1'], 'nonexistent', tabs)
    expect(result).toBe('nonexistent')
  })

  it('picks the nearest (first) browser tab among multiple', () => {
    const tabs = makeTabs(['t1', 'terminal'], ['b1', 'browser'], ['b2', 'browser'], ['t2', 'terminal'])
    const result = findBrowserInsertTarget(['t1', 'b1', 'b2', 't2'], 't1', tabs)
    expect(result).toBe('b1')
  })

  it('works when active tab is a browser tab — scans right for next browser', () => {
    const tabs = makeTabs(['b1', 'browser'], ['t1', 'terminal'], ['b2', 'browser'])
    const result = findBrowserInsertTarget(['b1', 't1', 'b2'], 'b1', tabs)
    expect(result).toBe('b2')
  })

  it('works when active tab is a browser tab and no browser to the right', () => {
    const tabs = makeTabs(['b1', 'browser'], ['t1', 'terminal'])
    const result = findBrowserInsertTarget(['b1', 't1'], 'b1', tabs)
    expect(result).toBe('b1')
  })

  it('handles empty order array', () => {
    const result = findBrowserInsertTarget([], 't1', {})
    expect(result).toBe('t1')
  })
})
