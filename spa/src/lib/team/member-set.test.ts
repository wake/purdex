// spa/src/lib/team/member-set.test.ts — the member set is computed ONCE for the App (member spec §3.2, MQ-2 review A-2).
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { act, renderHook } from '@testing-library/react'
import type { TeamRoster } from './roster'

const joinCalls = vi.hoisted(() => ({ n: 0 }))
vi.mock('./member-keys', async () => {
  const actual = await vi.importActual<typeof import('./member-keys')>('./member-keys')
  return { ...actual, activeMemberKeys: (i: Parameters<typeof actual.activeMemberKeys>[0]) => { joinCalls.n++; return actual.activeMemberKeys(i) } }
})

const { useActiveMemberKeys } = await import('../../hooks/useActiveMemberKeys')
const { currentMemberKeys } = await import('./member-set')
const { useSessionStore } = await import('../../stores/useSessionStore')
const { useTeamRosterStore } = await import('../../stores/useTeamRosterStore')

const rosterWith = (members: string[]): TeamRoster => ({
  id: 't1', host_id: 'd', created_at: 1, team_name: '', team_label: '',
  lead: { session_id: 'L', ref: '_L', address: 'a/L', live: true, tmux_session: 'lead' },
  members: members.map((n) => ({
    session_id: n, ref: `_${n}`, address: `a/${n}`, live: true, tmux_session: n, state: 'active', origin: 'spawned', joined_at: 1,
  })),
})
const row = (code: string, name: string) => ({ code, name, cwd: '/', mode: 'terminal' as const })

beforeEach(() => {
  useTeamRosterStore.getState().reset()
  useSessionStore.setState({ sessions: { h1: [row('aaaaaa', 'lead'), row('bbbbbb', 'w')] } })
  useTeamRosterStore.getState().markSessionsLive('h1')
})

describe('the shared member set', () => {
  it('N mounted consumers + one roster update → the join runs once', () => {
    const renders = Array.from({ length: 8 }, () => renderHook(() => useActiveMemberKeys()))
    const before = joinCalls.n
    act(() => useTeamRosterStore.getState().apply('h1', [rosterWith(['w'])]))
    expect(joinCalls.n - before).toBe(1)
    for (const r of renders) expect([...r.result.current]).toEqual(['h1:bbbbbb'])
    expect([...currentMemberKeys()]).toEqual(['h1:bbbbbb'])
  })

  it('a session-list change that leaves membership alone keeps the same Set and re-renders no consumer', () => {
    useTeamRosterStore.getState().apply('h1', [rosterWith(['w'])])
    let renders = 0
    const { result } = renderHook(() => { renders++; return useActiveMemberKeys() })
    const first = result.current
    const rendersBefore = renders
    act(() => useSessionStore.setState({ sessions: { h1: [row('aaaaaa', 'lead'), row('bbbbbb', 'w'), row('cccccc', 'other')] } }))
    expect(result.current).toBe(first)
    expect(renders).toBe(rendersBefore)
    expect(currentMemberKeys()).toBe(first)
  })

  it('MQ-2b: a session row whose tmux_instance changes recomputes the set', () => {
    const r = rosterWith(['w'])
    r.members[0].tmux_instance = '1:1'
    act(() => useTeamRosterStore.getState().apply('h1', [r]))
    expect(currentMemberKeys().size).toBe(0) // rows carry no instance yet
    act(() => useSessionStore.setState({ sessions: { h1: [row('aaaaaa', 'lead'), { ...row('bbbbbb', 'w'), tmux_instance: '1:1' }] } }))
    expect([...currentMemberKeys()]).toEqual(['h1:bbbbbb'])
    act(() => useSessionStore.setState({ sessions: { h1: [row('aaaaaa', 'lead'), { ...row('bbbbbb', 'w'), tmux_instance: '2:2' }] } }))
    expect(currentMemberKeys().size).toBe(0)
  })
})
