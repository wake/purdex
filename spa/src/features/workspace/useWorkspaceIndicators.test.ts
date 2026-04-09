import { describe, it, expect, beforeEach } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import { useTabStore } from '../../stores/useTabStore'
import { useAgentStore } from '../../stores/useAgentStore'
import { useWorkspaceIndicators } from './useWorkspaceIndicators'
import type { Tab } from '../../types/tab'

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

describe('useWorkspaceIndicators', () => {
  beforeEach(() => {
    useTabStore.setState({ tabs: {} })
    useAgentStore.setState({ unread: {}, statuses: {} })
  })

  it('returns zero unread for empty workspace', () => {
    const { result } = renderHook(() => useWorkspaceIndicators([]))
    expect(result.current.unreadCount).toBe(0)
    expect(result.current.aggregatedStatus).toBeUndefined()
  })

  it('counts unread tabs', () => {
    useTabStore.setState({
      tabs: {
        t1: mockSessionTab('t1', 'h1', 's1'),
        t2: mockSessionTab('t2', 'h1', 's2'),
        t3: mockSessionTab('t3', 'h1', 's3'),
      },
    })
    useAgentStore.setState({ unread: { 'h1:s1': true, 'h1:s2': false, 'h1:s3': true } })

    const { result } = renderHook(() => useWorkspaceIndicators(['t1', 't2', 't3']))
    expect(result.current.unreadCount).toBe(2)
  })

  it('aggregates status with priority', () => {
    useTabStore.setState({
      tabs: {
        t1: mockSessionTab('t1', 'h1', 's1'),
        t2: mockSessionTab('t2', 'h1', 's2'),
      },
    })
    useAgentStore.setState({ statuses: { 'h1:s1': 'running', 'h1:s2': 'waiting' } })

    const { result } = renderHook(() => useWorkspaceIndicators(['t1', 't2']))
    expect(result.current.aggregatedStatus).toBe('waiting')
  })

  it('reacts to unread store updates', () => {
    useTabStore.setState({ tabs: { t1: mockSessionTab('t1', 'h1', 's1') } })
    const { result } = renderHook(() => useWorkspaceIndicators(['t1']))
    expect(result.current.unreadCount).toBe(0)

    act(() => {
      useAgentStore.setState({ unread: { 'h1:s1': true } })
    })
    expect(result.current.unreadCount).toBe(1)
  })

  it('returns undefined status for all-idle workspace', () => {
    useTabStore.setState({ tabs: { t1: mockSessionTab('t1', 'h1', 's1') } })
    useAgentStore.setState({ statuses: { 'h1:s1': 'idle' } })

    const { result } = renderHook(() => useWorkspaceIndicators(['t1']))
    expect(result.current.aggregatedStatus).toBeUndefined()
  })
})
