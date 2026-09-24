import { describe, it, expect, beforeEach } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import { useTabHostBadge } from './useTabHostBadge'
import { useHostStore, type HostConfig } from '../stores/useHostStore'
import { useAgentStore } from '../stores/useAgentStore'
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

/**
 * Split tab whose FIRST leaf is not a tmux pane (`new-tab`) and whose SECOND leaf
 * is a tmux-session pane of `hostId` — the P2 Task 1 attacker-finding regression
 * shape: `getTabHostId`'s pre-order host must agree with the pane the mode is read
 * from, or a split tab like this resolves host-a's colors under the wrong mode.
 */
function splitTab(hostId: string, opts: { terminated?: string } = {}): Tab {
  const layout: PaneLayout = {
    type: 'split',
    id: 'split1',
    direction: 'h',
    sizes: [50, 50],
    children: [
      { type: 'leaf', pane: { id: 'p-new', content: { kind: 'new-tab' } } },
      {
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
            ...(opts.terminated ? { terminated: opts.terminated as never } : {}),
          },
        },
      },
    ],
  }
  return { id: 't-split', pinned: false, locked: false, createdAt: 0, layout }
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

  it('returns colors, icon and weight for a tmux tab', () => {
    useHostStore.getState().setHostColor('host-a', '#3b82f6')
    useHostStore.getState().setHostIcon('host-a', 'Laptop', 'duotone')
    const { result } = renderHook(() => useTabHostBadge(tmuxTab('host-a')))
    expect(result.current).toEqual({
      colors: { main: 'rgba(59, 130, 246, 1)', middle: 'rgba(59, 130, 246, 0.6)', light: 'rgba(59, 130, 246, 0.22)' },
      icon: 'Laptop',
      iconWeight: 'duotone',
    })
  })

  it('returns null for a tab without a tmux-session pane', () => {
    useHostStore.getState().setHostColor('host-a', '#3b82f6')
    const { result } = renderHook(() => useTabHostBadge(plainTab()))
    expect(result.current).toBeNull()
  })

  it('returns null colors when the stored color is malformed', () => {
    useHostStore.setState((s) => ({
      hosts: { ...s.hosts, 'host-a': { ...hostA, color: 'red; background:url(x)', icon: 'Laptop' } },
    }))
    const { result } = renderHook(() => useTabHostBadge(tmuxTab('host-a')))
    expect(result.current).toEqual({ colors: null, icon: 'Laptop', iconWeight: undefined })
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
    expect(result.current).toEqual({ colors: null, icon: 'Laptop', iconWeight: undefined })
  })

  it('updates when that host icon changes', () => {
    const tab = tmuxTab('host-a')
    const { result } = renderHook(() => useTabHostBadge(tab))
    expect(result.current?.icon).toBeUndefined()

    act(() => useHostStore.getState().setHostIcon('host-a', 'Cloud', 'bold'))
    expect(result.current).toEqual({ colors: null, icon: 'Cloud', iconWeight: 'bold' })

    act(() => useHostStore.getState().setHostIcon('host-a', null))
    expect(result.current).toEqual({ colors: null, icon: undefined, iconWeight: undefined })
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
    expect(result.current?.colors?.main).toBe('rgba(34, 197, 94, 1)')
  })

  it('a pane naming an unresolvable d1_ wire id → no colour, default icon, no throw', () => {
    const { result } = renderHook(() => useTabHostBadge(tmuxTab('d1_2u8ajsho6ji7nk6h')))
    expect(result.current).toEqual({ colors: null, icon: undefined, iconWeight: undefined })
  })

  it('returns empty fields when the host is missing from the store', () => {
    const { result } = renderHook(() => useTabHostBadge(tmuxTab('ghost')))
    expect(result.current).toEqual({ colors: null, icon: undefined, iconWeight: undefined })
  })

  it('reads the console main color from colors, preferring it over legacy color', () => {
    seedHosts({ ...hostA, color: '#22c55e', colors: { console: { main: { color: '#3b82f6', alpha: 100 } } } })
    const { result } = renderHook(() => useTabHostBadge(tmuxTab('host-a')))
    expect(result.current?.colors?.main).toBe('rgba(59, 130, 246, 1)')
  })

  it('still honours a legacy-only color', () => {
    seedHosts({ ...hostA, color: '#22c55e' })
    const { result } = renderHook(() => useTabHostBadge(tmuxTab('host-a')))
    expect(result.current?.colors?.main).toBe('rgba(34, 197, 94, 1)')
  })

  describe('mode', () => {
    const RED = { main: { color: '#ef4444', alpha: 100 } }
    const BLUE = { main: { color: '#3b82f6', alpha: 100 } }

    beforeEach(() => {
      useAgentStore.setState({ agentTypes: {} })
    })

    it('uses console colors when the primary pane has no agentType', () => {
      seedHosts({ ...hostA, colors: { console: BLUE, terminal: RED } })
      const { result } = renderHook(() => useTabHostBadge(tmuxTab('host-a')))
      expect(result.current?.colors?.main).toBe('rgba(59, 130, 246, 1)')
    })

    it('uses terminal colors when the primary pane has an agentType', () => {
      seedHosts({ ...hostA, colors: { console: BLUE, terminal: RED } })
      useAgentStore.setState({ agentTypes: { 'host-a:sess': 'cc' } })
      const { result } = renderHook(() => useTabHostBadge(tmuxTab('host-a')))
      expect(result.current?.colors?.main).toBe('rgba(239, 68, 68, 1)')
    })

    it('falls back to console colors for an agent tab whose host has no terminal set', () => {
      seedHosts({ ...hostA, colors: { console: BLUE } })
      useAgentStore.setState({ agentTypes: { 'host-a:sess': 'cc' } })
      const { result } = renderHook(() => useTabHostBadge(tmuxTab('host-a')))
      expect(result.current?.colors?.main).toBe('rgba(59, 130, 246, 1)')
    })

    it('treats a terminated agent pane as console', () => {
      seedHosts({ ...hostA, colors: { console: BLUE, terminal: RED } })
      useAgentStore.setState({ agentTypes: { 'host-a:sess': 'cc' } })
      const tab = tmuxTab('host-a')
      const pane = (tab.layout as { pane: { content: { terminated?: string } } }).pane
      pane.content.terminated = 'session-closed'
      const { result } = renderHook(() => useTabHostBadge(tab))
      expect(result.current?.colors?.main).toBe('rgba(59, 130, 246, 1)')
    })

    it('re-resolves when the agentType appears without a host write', () => {
      seedHosts({ ...hostA, colors: { console: BLUE, terminal: RED } })
      const { result } = renderHook(() => useTabHostBadge(tmuxTab('host-a')))
      expect(result.current?.colors?.main).toBe('rgba(59, 130, 246, 1)')
      act(() => useAgentStore.setState({ agentTypes: { 'host-a:sess': 'cc' } }))
      expect(result.current?.colors?.main).toBe('rgba(239, 68, 68, 1)')
    })

    it('returns the same colors object across re-renders when nothing changed', () => {
      seedHosts({ ...hostA, colors: { console: BLUE } })
      const { result, rerender } = renderHook(() => useTabHostBadge(tmuxTab('host-a')))
      const first = result.current?.colors
      rerender()
      expect(result.current?.colors).toBe(first)
    })

    it('on a split tab, reads mode from the SAME pane getTabHostId resolved (not the primary pane)', () => {
      seedHosts({ ...hostA, colors: { console: BLUE, terminal: RED } })
      useAgentStore.setState({ agentTypes: { 'host-a:sess': 'cc' } })
      const { result } = renderHook(() => useTabHostBadge(splitTab('host-a')))
      expect(result.current?.colors?.main).toBe('rgba(239, 68, 68, 1)')
    })

    it('on a split tab, treats the badge pane terminated as console even with a live agentType', () => {
      seedHosts({ ...hostA, colors: { console: BLUE, terminal: RED } })
      useAgentStore.setState({ agentTypes: { 'host-a:sess': 'cc' } })
      const { result } = renderHook(() =>
        useTabHostBadge(splitTab('host-a', { terminated: 'session-closed' })),
      )
      expect(result.current?.colors?.main).toBe('rgba(59, 130, 246, 1)')
    })
  })
})
