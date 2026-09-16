import { describe, it, expect, beforeEach } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import { useTabHostBadge } from './useTabHostBadge'
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
const hostB: HostConfig = { id: 'host-b', name: 'B', ip: '100.64.0.4', port: 7860, order: 1 }

/** merge-mode setState listing the mutable fields explicitly. */
function seedHosts(...hosts: HostConfig[]) {
  useHostStore.setState((s) => ({
    hosts: { ...s.hosts, ...Object.fromEntries(hosts.map((h) => [h.id, { ...h }])) },
    hostOrder: [...s.hostOrder, ...hosts.map((h) => h.id)],
  }))
}

describe('useTabHostBadge', () => {
  beforeEach(() => {
    useHostStore.getState().reset()
    seedHosts(hostA, hostB)
  })

  it('returns color, icon and weight for a tmux tab', () => {
    useHostStore.getState().setHostColor('host-a', '#3b82f6')
    useHostStore.getState().setHostIcon('host-a', 'Laptop', 'duotone')
    const { result } = renderHook(() => useTabHostBadge(tmuxTab('host-a')))
    expect(result.current).toEqual({ color: '#3b82f6', icon: 'Laptop', iconWeight: 'duotone' })
  })

  it('returns null for a tab without a tmux-session pane', () => {
    useHostStore.getState().setHostColor('host-a', '#3b82f6')
    const { result } = renderHook(() => useTabHostBadge(plainTab()))
    expect(result.current).toBeNull()
  })

  it('returns a null color when the stored color is malformed', () => {
    useHostStore.setState((s) => ({
      hosts: { ...s.hosts, 'host-a': { ...hostA, color: 'red; background:url(x)', icon: 'Laptop' } },
    }))
    const { result } = renderHook(() => useTabHostBadge(tmuxTab('host-a')))
    expect(result.current).toEqual({ color: null, icon: 'Laptop', iconWeight: undefined })
  })

  it('returns an undefined icon when the stored icon is not a non-empty string', () => {
    useHostStore.setState((s) => ({
      hosts: { ...s.hosts, 'host-a': { ...hostA, icon: '   ' } },
    }))
    const { result: blank } = renderHook(() => useTabHostBadge(tmuxTab('host-a')))
    expect(blank.current?.icon).toBeUndefined()

    useHostStore.setState((s) => ({
      hosts: { ...s.hosts, 'host-a': { ...hostA, icon: 42 as unknown as string } },
    }))
    const { result: wrongType } = renderHook(() => useTabHostBadge(tmuxTab('host-a')))
    expect(wrongType.current?.icon).toBeUndefined()
  })

  it.each([
    'a'.repeat(200),
    'laptop',
    ' Laptop ',
    'NotARealPhosphorIcon',
    'Laptop; background:url(x)',
  ])('returns an undefined icon for the pre-existing non-Phosphor value %j', (bad) => {
    // setState on purpose: a value written before the guard existed bypasses setHostIcon.
    useHostStore.setState((s) => ({
      hosts: { ...s.hosts, 'host-a': { ...hostA, icon: bad } },
    }))
    const { result } = renderHook(() => useTabHostBadge(tmuxTab('host-a')))
    expect(result.current?.icon).toBeUndefined()
  })

  it('returns an undefined weight when the stored weight is invalid', () => {
    useHostStore.setState((s) => ({
      hosts: {
        ...s.hosts,
        'host-a': { ...hostA, icon: 'Laptop', iconWeight: 'evil' as unknown as never },
      },
    }))
    const { result } = renderHook(() => useTabHostBadge(tmuxTab('host-a')))
    expect(result.current).toEqual({ color: null, icon: 'Laptop', iconWeight: undefined })
  })

  it('updates when that host icon changes', () => {
    const tab = tmuxTab('host-a')
    const { result } = renderHook(() => useTabHostBadge(tab))
    expect(result.current?.icon).toBeUndefined()

    act(() => useHostStore.getState().setHostIcon('host-a', 'Cloud', 'bold'))
    expect(result.current).toEqual({ color: null, icon: 'Cloud', iconWeight: 'bold' })

    act(() => useHostStore.getState().setHostIcon('host-a', null))
    expect(result.current).toEqual({ color: null, icon: undefined, iconWeight: undefined })
  })

  it('does not re-render when an unrelated host changes', () => {
    const tab = tmuxTab('host-a')
    let renders = 0
    const { result } = renderHook(() => {
      renders++
      return useTabHostBadge(tab)
    })
    const baseline = renders

    act(() => useHostStore.getState().setHostColor('host-b', '#ef4444'))
    act(() => useHostStore.getState().setHostIcon('host-b', 'Cloud', 'bold'))
    expect(renders).toBe(baseline)

    act(() => useHostStore.getState().setHostColor('host-a', '#22c55e'))
    expect(renders).toBeGreaterThan(baseline)
    expect(result.current?.color).toBe('#22c55e')
  })

  it('returns empty fields when the host is missing from the store', () => {
    const { result } = renderHook(() => useTabHostBadge(tmuxTab('ghost')))
    expect(result.current).toEqual({ color: null, icon: undefined, iconWeight: undefined })
  })
})
