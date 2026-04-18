import { describe, it, expect, beforeEach } from 'vitest'
import { renderHook } from '@testing-library/react'
import { useAgentStore } from '../stores/useAgentStore'
import { useHostStore } from '../stores/useHostStore'
import { useSessionStore } from '../stores/useSessionStore'
import { useWorkspaceStore } from '../stores/useWorkspaceStore'
import { useI18nStore } from '../stores/useI18nStore'
import { createTab } from '../types/tab'
import type { Tab } from '../types/tab'
import { useTabDisplay } from './useTabDisplay'

function makeTab(
  overrides: Partial<{ hostId: string; sessionCode: string; terminated: boolean; cachedName: string; mode: 'terminal' | 'stream' }> = {},
): Tab {
  const tab = createTab({
    kind: 'tmux-session',
    hostId: overrides.hostId ?? 'h1',
    sessionCode: overrides.sessionCode ?? 'sc1',
    mode: overrides.mode ?? 'terminal',
    cachedName: overrides.cachedName ?? '',
    tmuxInstance: '',
    terminated: overrides.terminated ? 'session-closed' : undefined,
  } as never)
  return { ...tab, id: 't1' }
}

beforeEach(() => {
  useSessionStore.setState({ sessions: {}, activeHostId: null, activeCode: null })
  useWorkspaceStore.setState({ workspaces: [], activeWorkspaceId: null })
  useHostStore.setState({ runtime: {} })
  useAgentStore.setState({
    unread: {},
    statuses: {},
    subagents: {},
    agentTypes: {},
    oscTitles: {},
    tabIndicatorStyle: 'badge',
    ccIconVariant: 'bot',
    showOscTitle: false,
  })
  useI18nStore.setState({ t: (k: string) => k })
})

describe('useTabDisplay — label resolution', () => {
  it('uses session name from session store when available', () => {
    useSessionStore.setState({
      sessions: { h1: [{ code: 'sc1', name: 'my-session' }] as never },
      activeHostId: null,
      activeCode: null,
    })
    const { result } = renderHook(() => useTabDisplay(makeTab()))
    expect(result.current.displayTitle).toBe('my-session')
    expect(result.current.tooltip).toBe('my-session')
  })

  it('falls back to sessionCode when session not found', () => {
    const { result } = renderHook(() => useTabDisplay(makeTab()))
    expect(result.current.displayTitle).toBe('sc1')
  })

  it('falls back to cachedName when session not found and cachedName present', () => {
    const { result } = renderHook(() => useTabDisplay(makeTab({ cachedName: 'cached' })))
    expect(result.current.displayTitle).toBe('cached')
  })

  it('scopes session lookup to the tabs own hostId (no cross-host collisions)', () => {
    useSessionStore.setState({
      sessions: {
        h1: [{ code: 'sc1', name: 'correct' }] as never,
        h2: [{ code: 'sc1', name: 'wrong-host' }] as never,
      },
      activeHostId: null,
      activeCode: null,
    })
    const { result } = renderHook(() => useTabDisplay(makeTab({ hostId: 'h1' })))
    expect(result.current.displayTitle).toBe('correct')
  })
})

describe('useTabDisplay — OSC title override', () => {
  it('uses OSC title when showOscTitle + agentType + oscTitle are all set', () => {
    useSessionStore.setState({
      sessions: { h1: [{ code: 'sc1', name: 'base' }] as never },
      activeHostId: null,
      activeCode: null,
    })
    useAgentStore.setState({
      showOscTitle: true,
      agentTypes: { 'h1:sc1': 'cc' },
      oscTitles: { 'h1:sc1': 'claude' },
    })
    const { result } = renderHook(() => useTabDisplay(makeTab()))
    expect(result.current.displayTitle).toBe('claude')
    expect(result.current.tooltip).toBe('claude - base')
  })

  it('ignores OSC when showOscTitle is off', () => {
    useAgentStore.setState({
      showOscTitle: false,
      agentTypes: { 'h1:sc1': 'cc' },
      oscTitles: { 'h1:sc1': 'claude' },
    })
    const { result } = renderHook(() => useTabDisplay(makeTab({ cachedName: 'fallback' })))
    expect(result.current.displayTitle).toBe('fallback')
  })

  it('ignores OSC on terminated session', () => {
    useAgentStore.setState({
      showOscTitle: true,
      agentTypes: { 'h1:sc1': 'cc' },
      oscTitles: { 'h1:sc1': 'claude' },
    })
    const { result } = renderHook(() => useTabDisplay(makeTab({ terminated: true, cachedName: 'gone' })))
    expect(result.current.displayTitle).toBe('gone（Terminated）')
  })
})

describe('useTabDisplay — icon resolution', () => {
  it('returns agent icon when agentType is present and not terminated', () => {
    useAgentStore.setState({ agentTypes: { 'h1:sc1': 'cc' } })
    const { result } = renderHook(() => useTabDisplay(makeTab()))
    expect(result.current.IconComponent).toBeDefined()
  })

  it('returns pane icon on terminated session regardless of agentType', () => {
    useAgentStore.setState({ agentTypes: { 'h1:sc1': 'cc' } })
    const { result } = renderHook(() => useTabDisplay(makeTab({ terminated: true })))
    expect(result.current.IconComponent).toBeDefined()
    expect(result.current.isTerminated).toBe(true)
  })
})

describe('useTabDisplay — host offline', () => {
  it('flags host offline when runtime status is not connected', () => {
    useHostStore.setState({ runtime: { h1: { status: 'disconnected' } } } as never)
    const { result } = renderHook(() => useTabDisplay(makeTab()))
    expect(result.current.isHostOffline).toBe(true)
  })

  it('does not flag host offline when tab is terminated', () => {
    useHostStore.setState({ runtime: { h1: { status: 'disconnected' } } } as never)
    const { result } = renderHook(() => useTabDisplay(makeTab({ terminated: true })))
    expect(result.current.isHostOffline).toBe(false)
  })

  it('does not flag host offline when host is connected', () => {
    useHostStore.setState({ runtime: { h1: { status: 'connected' } } } as never)
    const { result } = renderHook(() => useTabDisplay(makeTab()))
    expect(result.current.isHostOffline).toBe(false)
  })
})

describe('useTabDisplay — agent store fields', () => {
  it('exposes agentStatus, unread, subagentCount, tabIndicatorStyle', () => {
    useAgentStore.setState({
      statuses: { 'h1:sc1': 'running' },
      unread: { 'h1:sc1': true },
      subagents: { 'h1:sc1': [{ id: 's1' }] as never },
      tabIndicatorStyle: 'dot',
    })
    const { result } = renderHook(() => useTabDisplay(makeTab()))
    expect(result.current.agentStatus).toBe('running')
    expect(result.current.isUnread).toBe(true)
    expect(result.current.subagentCount).toBe(1)
    expect(result.current.tabIndicatorStyle).toBe('dot')
  })
})
