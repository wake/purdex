import { describe, it, expect } from 'vitest'
import { findInsertTarget } from './find-insert-target'
import type { Tab } from '../../types/tab'

function makeTab(id: string, kind: string): Tab {
  const content = kind === 'browser'
    ? { kind: 'browser', url: 'https://example.com' }
    : kind === 'editor'
      ? { kind: 'editor', source: { type: 'inapp' } as never, filePath: `/${id}.ts` }
      : kind === 'image-preview'
        ? { kind: 'image-preview', source: { type: 'inapp' } as never, filePath: `/${id}.png` }
        : kind === 'pdf-preview'
          ? { kind: 'pdf-preview', source: { type: 'inapp' } as never, filePath: `/${id}.pdf` }
          : { kind: 'tmux-session', hostId: 'h', sessionCode: 's', mode: 'terminal', cachedName: '', tmuxInstance: '' }
  return {
    id,
    pinned: false,
    locked: false,
    createdAt: 0,
    layout: { type: 'leaf', pane: { id: `p-${id}`, content: content as never } },
  }
}

function makeTabs(...specs: [string, string][]): Record<string, Tab> {
  const result: Record<string, Tab> = {}
  for (const [id, kind] of specs) {
    result[id] = makeTab(id, kind)
  }
  return result
}

describe('findInsertTarget', () => {
  const isBrowser = (c: { kind: string }) => c.kind === 'browser'
  const isFileKind = (c: { kind: string }) =>
    c.kind === 'editor' || c.kind === 'image-preview' || c.kind === 'pdf-preview'

  it('returns nearest browser tab to the right of active', () => {
    const tabs = makeTabs(['t1', 'terminal'], ['t2', 'terminal'], ['b1', 'browser'], ['t3', 'terminal'])
    expect(findInsertTarget(['t1', 't2', 'b1', 't3'], 't1', tabs, isBrowser)).toBe('b1')
  })

  it('skips non-browser tabs when scanning right', () => {
    const tabs = makeTabs(['t1', 'terminal'], ['t2', 'terminal'], ['t3', 'terminal'], ['b1', 'browser'])
    expect(findInsertTarget(['t1', 't2', 't3', 'b1'], 't1', tabs, isBrowser)).toBe('b1')
  })

  it('returns activeTabId when no matching tab to the right', () => {
    const tabs = makeTabs(['b1', 'browser'], ['t1', 'terminal'], ['t2', 'terminal'])
    expect(findInsertTarget(['b1', 't1', 't2'], 't1', tabs, isBrowser)).toBe('t1')
  })

  it('returns activeTabId when active tab is last', () => {
    const tabs = makeTabs(['t1', 'terminal'], ['t2', 'terminal'])
    expect(findInsertTarget(['t1', 't2'], 't2', tabs, isBrowser)).toBe('t2')
  })

  it('returns activeTabId when not found in order', () => {
    const tabs = makeTabs(['t1', 'terminal'])
    expect(findInsertTarget(['t1'], 'nonexistent', tabs, isBrowser)).toBe('nonexistent')
  })

  it('picks the nearest (first) browser tab among multiple', () => {
    const tabs = makeTabs(['t1', 'terminal'], ['b1', 'browser'], ['b2', 'browser'], ['t2', 'terminal'])
    expect(findInsertTarget(['t1', 'b1', 'b2', 't2'], 't1', tabs, isBrowser)).toBe('b1')
  })

  it('works when active is a browser and there is another browser to the right', () => {
    const tabs = makeTabs(['b1', 'browser'], ['t1', 'terminal'], ['b2', 'browser'])
    expect(findInsertTarget(['b1', 't1', 'b2'], 'b1', tabs, isBrowser)).toBe('b2')
  })

  it('falls back to active when active is a browser but no browser to the right', () => {
    const tabs = makeTabs(['b1', 'browser'], ['t1', 'terminal'])
    expect(findInsertTarget(['b1', 't1'], 'b1', tabs, isBrowser)).toBe('b1')
  })

  it('handles empty order array', () => {
    expect(findInsertTarget([], 't1', {}, isBrowser)).toBe('t1')
  })

  it('predicate determines kind family — file kinds aggregate together', () => {
    const tabs = makeTabs(
      ['a', 'terminal'],
      ['b', 'editor'],
      ['c', 'terminal'],
      ['d', 'image-preview'],
    )
    expect(findInsertTarget(['a', 'b', 'c', 'd'], 'a', tabs, isFileKind)).toBe('b')
    expect(findInsertTarget(['a', 'b', 'c', 'd'], 'c', tabs, isFileKind)).toBe('d')
  })
})
