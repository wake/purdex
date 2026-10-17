// spa/src/hooks/useSessionAgentIndicator.member.test.ts — member spec R3 on a mounted sidebar row / team bead: the unread
// mark is hidden while the session is a member and comes back with the kept unread when it leaves; status is not touched.
import { beforeEach, describe, expect, it } from 'vitest'
import { act, renderHook } from '@testing-library/react'
import { useSessionAgentIndicator } from './useSessionAgentIndicator'
import { useAgentStore } from '../stores/useAgentStore'
import { useSessionStore } from '../stores/useSessionStore'
import { useTeamRosterStore } from '../stores/useTeamRosterStore'
import type { TeamRoster } from '../lib/team/roster'

const roster = (members: string[]): TeamRoster => ({
  id: 't1', host_id: 'd', created_at: 1, team_name: '', team_label: '',
  lead: { session_id: 'L', ref: '_L', address: 'a/L', live: true, tmux_session: 'lead' },
  members: members.map((n) => ({
    session_id: n, ref: `_${n}`, address: `a/${n}`, live: true, tmux_session: n, state: 'active', origin: 'spawned', joined_at: 1,
  })),
})

beforeEach(() => {
  useTeamRosterStore.getState().reset()
  useSessionStore.setState({ sessions: { h1: [{ code: 's1', name: 'worker', cwd: '/', mode: 'terminal' }] } })
  useAgentStore.setState({ statuses: { 'h1:s1': 'waiting' }, agentTypes: {}, subagents: {}, unread: { 'h1:s1': true } })
})

describe('useSessionAgentIndicator — team members (R3)', () => {
  it('hides the unread mark when the session becomes a member and shows it again when it leaves', () => {
    const { result } = renderHook(() => useSessionAgentIndicator('h1', 's1'))
    expect(result.current.isUnread).toBe(true)

    act(() => useTeamRosterStore.getState().apply('h1', [roster(['worker'])]))
    expect(result.current.isUnread).toBe(false)
    expect(result.current.agentStatus).toBe('waiting')
    expect(useAgentStore.getState().unread['h1:s1']).toBe(true) // kept, only hidden

    act(() => useTeamRosterStore.getState().apply('h1', [roster([])]))
    expect(result.current.isUnread).toBe(true)
  })

  it('a lead is not a member: its unread mark shows', () => {
    useTeamRosterStore.getState().apply('h1', [{ ...roster([]), lead: { ...roster([]).lead, tmux_session: 'worker' } }])
    const { result } = renderHook(() => useSessionAgentIndicator('h1', 's1'))
    expect(result.current.isUnread).toBe(true)
  })
})
