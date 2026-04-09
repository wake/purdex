import { describe, it, expect } from 'vitest'
import type { Tab } from '../../types/tab'
import { getWorkspaceCompositeKeys, aggregateStatus } from './workspace-indicators'

function mockSessionTab(id: string, hostId: string, sessionCode: string): Tab {
  return {
    id,
    pinned: false,
    locked: false,
    createdAt: 0,
    layout: {
      type: 'leaf',
      pane: {
        id: `pane-${id}`,
        content: { kind: 'tmux-session', hostId, sessionCode, mode: 'terminal' as const, cachedName: '', tmuxInstance: '' },
      },
    },
  }
}

function mockDashboardTab(id: string): Tab {
  return {
    id,
    pinned: false,
    locked: false,
    createdAt: 0,
    layout: { type: 'leaf', pane: { id: `pane-${id}`, content: { kind: 'dashboard' } } },
  }
}

describe('getWorkspaceCompositeKeys', () => {
  it('returns compositeKeys for tmux-session tabs', () => {
    const tabs: Record<string, Tab> = {
      t1: mockSessionTab('t1', 'h1', 's1'),
      t2: mockSessionTab('t2', 'h1', 's2'),
    }
    expect(getWorkspaceCompositeKeys(['t1', 't2'], tabs)).toEqual(['h1:s1', 'h1:s2'])
  })

  it('skips non-session tabs', () => {
    const tabs: Record<string, Tab> = {
      t1: mockSessionTab('t1', 'h1', 's1'),
      t2: mockDashboardTab('t2'),
    }
    expect(getWorkspaceCompositeKeys(['t1', 't2'], tabs)).toEqual(['h1:s1'])
  })

  it('skips missing tab IDs', () => {
    const tabs: Record<string, Tab> = {
      t1: mockSessionTab('t1', 'h1', 's1'),
    }
    expect(getWorkspaceCompositeKeys(['t1', 't999'], tabs)).toEqual(['h1:s1'])
  })

  it('returns empty array for empty workspace', () => {
    expect(getWorkspaceCompositeKeys([], {})).toEqual([])
  })
})

describe('aggregateStatus', () => {
  it('returns undefined for empty array', () => {
    expect(aggregateStatus([])).toBeUndefined()
  })

  it('returns undefined for all idle', () => {
    expect(aggregateStatus(['idle', 'idle'])).toBeUndefined()
  })

  it('returns undefined for all undefined', () => {
    expect(aggregateStatus([undefined, undefined])).toBeUndefined()
  })

  it('returns running when highest is running', () => {
    expect(aggregateStatus(['running', 'idle'])).toBe('running')
  })

  it('returns waiting over running', () => {
    expect(aggregateStatus(['running', 'waiting'])).toBe('waiting')
  })

  it('returns error over everything', () => {
    expect(aggregateStatus(['running', 'waiting', 'error'])).toBe('error')
  })

  it('returns running when mixed with undefined', () => {
    expect(aggregateStatus([undefined, 'running', undefined])).toBe('running')
  })
})
