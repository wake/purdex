// spa/src/features/workspace/useWorkspaceIndicators.member.test.ts — member spec R2: the workspace number skips a
// member's unread, and a mounted number follows the roster both ways.
import { beforeEach, describe, expect, it } from 'vitest'
import { act, renderHook } from '@testing-library/react'
import { useAgentStore } from '../../stores/useAgentStore'
import { useSessionStore } from '../../stores/useSessionStore'
import { useTabStore } from '../../stores/useTabStore'
import { useTeamRosterStore } from '../../stores/useTeamRosterStore'
import type { TeamRoster } from '../../lib/team/roster'
import type { Tab } from '../../types/tab'
import { useWorkspaceIndicators } from './useWorkspaceIndicators'

const tabOf = (id: string, sessionCode: string, extraCode?: string): Tab => {
  const leaf = (code: string, n: number) => ({
    type: 'leaf' as const,
    pane: { id: `${id}-p${n}`, content: { kind: 'tmux-session' as const, hostId: 'h1', sessionCode: code, mode: 'terminal' as const, cachedName: '', tmuxInstance: '' } },
  })
  return {
    id, pinned: false, locked: false, createdAt: 0,
    layout: extraCode
      ? { type: 'split', id: `${id}-s`, direction: 'h', children: [leaf(sessionCode, 0), leaf(extraCode, 1)], sizes: [1, 1] }
      : leaf(sessionCode, 0),
  }
}
const roster = (members: string[]): TeamRoster => ({
  id: 't1', host_id: 'd', created_at: 1, team_name: '', team_label: '',
  lead: { session_id: 'L', ref: '_L', address: 'a/L', live: true, tmux_session: 'lead' },
  members: members.map((n) => ({
    session_id: n, ref: `_${n}`, address: `a/${n}`, live: true, tmux_session: n, state: 'active', origin: 'spawned', joined_at: 1,
  })),
})
const SESSIONS = { h1: [
  { code: 'aaaaaa', name: 'wa', cwd: '/', mode: 'terminal' as const },
  { code: 'bbbbbb', name: 'wb', cwd: '/', mode: 'terminal' as const },
] }

beforeEach(() => {
  useTeamRosterStore.getState().reset()
  useSessionStore.setState({ sessions: SESSIONS })
  useTabStore.setState({ tabs: { t1: tabOf('t1', 'aaaaaa'), t2: tabOf('t2', 'bbbbbb') } })
  useAgentStore.setState({ unread: { 'h1:aaaaaa': true, 'h1:bbbbbb': true }, statuses: {} })
})

describe('useWorkspaceIndicators — team members (R2)', () => {
  it('a mounted number drops a tab whose session becomes a member, and takes it back when it leaves', () => {
    const { result } = renderHook(() => useWorkspaceIndicators(['t1', 't2']))
    expect(result.current.unreadCount).toBe(2)
    act(() => useTeamRosterStore.getState().apply('h1', [roster(['wa'])]))
    expect(result.current.unreadCount).toBe(1)
    act(() => useTeamRosterStore.getState().apply('h1', [roster([])]))
    expect(result.current.unreadCount).toBe(2)
  })

  it('a split tab counts through its non-member pane', () => {
    useTabStore.setState({ tabs: { t1: tabOf('t1', 'aaaaaa', 'bbbbbb') } })
    const { result } = renderHook(() => useWorkspaceIndicators(['t1']))
    act(() => useTeamRosterStore.getState().apply('h1', [roster(['wa'])]))
    expect(result.current.unreadCount).toBe(1)
    act(() => useTeamRosterStore.getState().apply('h1', [roster(['wa', 'wb'])]))
    expect(result.current.unreadCount).toBe(0)
  })

  it('the status is untouched by membership', () => {
    useAgentStore.setState({ statuses: { 'h1:aaaaaa': 'waiting' } })
    const { result } = renderHook(() => useWorkspaceIndicators(['t1']))
    act(() => useTeamRosterStore.getState().apply('h1', [roster(['wa'])]))
    expect(result.current.aggregatedStatus).toBe('waiting')
  })
})
