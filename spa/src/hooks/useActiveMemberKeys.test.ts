// spa/src/hooks/useActiveMemberKeys.test.ts — the member set follows the roster and the session lists, both ways, and
// keeps its identity while the membership is unchanged (member spec §3.2).
import { beforeEach, describe, expect, it } from 'vitest'
import { act, renderHook } from '@testing-library/react'
import { useActiveMemberKeys } from './useActiveMemberKeys'
import { useSessionStore } from '../stores/useSessionStore'
import { useTeamRosterStore } from '../stores/useTeamRosterStore'
import type { TeamRoster } from '../lib/team/roster'

const rosterWith = (members: string[]): TeamRoster => ({
  id: 't1', host_id: 'd', created_at: 1, team_name: '', team_label: '',
  lead: { session_id: 'L', ref: '_L', address: 'a/L', live: true, tmux_session: 'lead' },
  members: members.map((n) => ({
    session_id: n, ref: `_${n}`, address: `a/${n}`, live: true, tmux_session: n, state: 'active', origin: 'spawned', joined_at: 1,
  })),
})
const sessions = { h1: [{ code: 'aaaaaa', name: 'lead', cwd: '/', mode: 'terminal' as const }, { code: 'bbbbbb', name: 'w', cwd: '/', mode: 'terminal' as const }] }

beforeEach(() => {
  useTeamRosterStore.getState().reset()
  useSessionStore.setState({ sessions })
})

describe('useActiveMemberKeys', () => {
  it('follows the roster: joining a team adds the key, leaving removes it', () => {
    const { result } = renderHook(() => useActiveMemberKeys())
    expect(result.current.size).toBe(0)
    act(() => useTeamRosterStore.getState().apply('h1', [rosterWith(['w'])]))
    expect([...result.current]).toEqual(['h1:bbbbbb'])
    act(() => useTeamRosterStore.getState().apply('h1', [rosterWith([])]))
    expect(result.current.size).toBe(0)
  })

  it('follows the session list (a seat appears once its session is listed)', () => {
    useSessionStore.setState({ sessions: {} })
    useTeamRosterStore.getState().apply('h1', [rosterWith(['w'])])
    const { result } = renderHook(() => useActiveMemberKeys())
    expect(result.current.size).toBe(0)
    act(() => useSessionStore.setState({ sessions }))
    expect([...result.current]).toEqual(['h1:bbbbbb'])
  })

  it('keeps the same Set while membership is unchanged', () => {
    useTeamRosterStore.getState().apply('h1', [rosterWith(['w'])])
    const { result } = renderHook(() => useActiveMemberKeys())
    const first = result.current
    act(() => useTeamRosterStore.getState().apply('h1', [rosterWith(['w'])]))
    act(() => useSessionStore.setState({ sessions: { ...sessions } }))
    expect(result.current).toBe(first)
  })

  it('R4 reconnect: a close keeps the roster but none of its seats are members; the session list alone does not bring them back; the snapshot does', () => {
    useTeamRosterStore.getState().apply('h1', [rosterWith(['w'])])
    const { result } = renderHook(() => useActiveMemberKeys())
    expect([...result.current]).toEqual(['h1:bbbbbb'])
    act(() => useTeamRosterStore.getState().dropConnection('h1')) // socket closed
    expect(useTeamRosterStore.getState().byHost.h1).toHaveLength(1) // beads / quotas still read it
    expect(result.current.size).toBe(0)
    // reopen: the session list is fetched first (a new session may have reused the name), the roster snapshot not yet
    act(() => useSessionStore.setState({ sessions: { h1: [...sessions.h1] } }))
    expect(result.current.size).toBe(0)
    act(() => useTeamRosterStore.getState().apply('h1', [rosterWith(['w'])])) // this connection's snapshot
    expect([...result.current]).toEqual(['h1:bbbbbb'])
  })
})
