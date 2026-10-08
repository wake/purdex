// spa/src/hooks/useTeamViews.test.ts — the hook joins the real stores and does not loop or churn (plan PL-2b′).
import { describe, it, expect, beforeEach } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import { useTeamViews } from './useTeamViews'
import { useTeamRosterStore } from '../stores/useTeamRosterStore'
import { useTabStore } from '../stores/useTabStore'
import { useWorkspaceStore } from '../features/workspace/store'
import { useSessionStore } from '../stores/useSessionStore'
import { useHostStore } from '../stores/useHostStore'
import type { TeamRoster } from '../lib/team/roster'
import type { Tab } from '../types/tab'

const roster: TeamRoster[] = [{
  id: 't1', host_id: 'd', created_at: 1, team_name: '', team_label: '',
  lead: { session_id: 'L', ref: '_aaaaaa', address: 'mlab/lead-aa', live: true, tmux_session: 'lead-tm' },
  members: [{ session_id: 'A', ref: '_bbbbbb', address: 'mlab/a-bb', live: true, state: 'active', origin: 'spawned', joined_at: 2 }],
}]
const leadTab: Tab = {
  id: 'tab1', pinned: false, locked: false, createdAt: 0,
  layout: { type: 'leaf', pane: { id: 'p1', content: { kind: 'tmux-session', hostId: 'h1', sessionCode: 'c1', mode: 'terminal', cachedName: 'lead-tm', tmuxInstance: 'i' } } },
}

beforeEach(() => {
  useTeamRosterStore.getState().reset()
  useTabStore.setState({ tabs: {} })
  useWorkspaceStore.setState({ workspaces: [{ id: 'w1', name: 'W', tabs: [], activeTabId: null }], activeWorkspaceId: 'w1' })
  useSessionStore.setState({ sessions: {} })
})

describe('useTeamViews', () => {
  it('is empty before any roster, and follows the roster and the tabs', () => {
    const { result } = renderHook(() => useTeamViews())
    expect(result.current).toEqual([])
    act(() => { useTeamRosterStore.getState().apply('h1', roster) })
    expect(result.current).toHaveLength(1)
    expect(result.current[0].lead.tabId).toBeNull()
    act(() => {
      useTabStore.setState({ tabs: { tab1: leadTab } })
      useWorkspaceStore.setState({ workspaces: [{ id: 'w1', name: 'W', tabs: ['tab1'], activeTabId: null }] })
    })
    expect(result.current[0].lead.tabId).toBe('tab1')
  })

  it('renders once per real input change, and keeps the same array across writes that touch none of its inputs', () => {
    let renders = 0
    const { result } = renderHook(() => { renders++; return useTeamViews() })
    act(() => { useTeamRosterStore.getState().apply('h1', roster) })
    const settled = renders
    const views = result.current
    act(() => {
      useHostStore.setState((s) => ({ runtime: { ...s.runtime, h1: { ...s.runtime.h1, latency: 3 } as never } })) // not an input
      useSessionStore.setState({ activeCode: 'zz' }) // not an input (sessions map is the same object)
    })
    expect(renders).toBe(settled)
    expect(result.current).toBe(views)
    act(() => { useTeamRosterStore.getState().apply('h1', roster) }) // same content, new list: a real input change
    expect(renders).toBe(settled + 1)
    expect(result.current).not.toBe(views)
  })

  it('an unchanged memberOrder reference does not rebuild; a new one does', () => {
    const order = { [`h1\u0000t1`]: ['A'] }
    act(() => { useTeamRosterStore.getState().apply('h1', roster) })
    const { result, rerender } = renderHook(({ o }) => useTeamViews(o), { initialProps: { o: order } })
    const first = result.current
    rerender({ o: order })
    expect(result.current).toBe(first)
    rerender({ o: { ...order } })
    expect(result.current).not.toBe(first)
  })
})
