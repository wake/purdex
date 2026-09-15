import { describe, it, expect, beforeEach } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import { useTabHostColor } from './useTabHostColor'
import { useHostStore, type HostConfig } from '../stores/useHostStore'
import type { PaneLayout, Tab } from '../types/tab'

function tmuxTab(hostId: string): Tab {
  const layout: PaneLayout = {
    type: 'leaf',
    pane: {
      id: `pane-${hostId}`,
      content: {
        kind: 'tmux-session',
        hostId,
        sessionCode: 'sess',
        mode: 'terminal',
        cachedName: 'x',
        tmuxInstance: 'default',
      },
    },
  }
  return { id: 't1', pinned: false, locked: false, createdAt: 0, layout }
}

function plainTab(): Tab {
  return {
    id: 't2',
    pinned: false,
    locked: false,
    createdAt: 0,
    layout: { type: 'leaf', pane: { id: 'p', content: { kind: 'new-tab' } } },
  }
}

const hostA: HostConfig = { id: 'host-a', name: 'A', ip: '100.64.0.2', port: 7860, order: 0 }

describe('useTabHostColor', () => {
  beforeEach(() => {
    useHostStore.getState().reset()
    useHostStore.setState((s) => ({ hosts: { ...s.hosts, [hostA.id]: { ...hostA } } }))
  })

  it('returns the host color for a tmux tab', () => {
    useHostStore.getState().setHostColor('host-a', '#3b82f6')
    const { result } = renderHook(() => useTabHostColor(tmuxTab('host-a')))
    expect(result.current).toBe('#3b82f6')
  })

  it('returns null for a tab without tmux panes', () => {
    useHostStore.getState().setHostColor('host-a', '#3b82f6')
    const { result } = renderHook(() => useTabHostColor(plainTab()))
    expect(result.current).toBeNull()
  })

  it('returns null when host has no color', () => {
    const { result } = renderHook(() => useTabHostColor(tmuxTab('host-a')))
    expect(result.current).toBeNull()
  })

  it('returns null for an invalid stored value', () => {
    useHostStore.setState((s) => ({
      hosts: { ...s.hosts, 'host-a': { ...hostA, color: 'red; background:url(x)' } },
    }))
    const { result } = renderHook(() => useTabHostColor(tmuxTab('host-a')))
    expect(result.current).toBeNull()
  })

  it('returns null for a missing host', () => {
    const { result } = renderHook(() => useTabHostColor(tmuxTab('ghost')))
    expect(result.current).toBeNull()
  })

  it('re-renders when the host color changes', () => {
    const { result } = renderHook(() => useTabHostColor(tmuxTab('host-a')))
    expect(result.current).toBeNull()
    act(() => useHostStore.getState().setHostColor('host-a', '#ef4444'))
    expect(result.current).toBe('#ef4444')
    act(() => useHostStore.getState().setHostColor('host-a', null))
    expect(result.current).toBeNull()
  })
})
