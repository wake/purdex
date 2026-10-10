// spa/src/lib/team/roster-ws.test.ts — the `team.roster` host event reaches the roster store (plan PL-2b′).
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { useTeamRosterStore } from '../../stores/useTeamRosterStore'
import { handleRosterEvent } from './roster-ws'
import { useTeamUiStore } from '../../stores/useTeamUiStore'
import { useHostStore } from '../../stores/useHostStore'
import { teamKeyOf } from './team-views'
import type { TeamRoster } from './roster'

const team = (id: string): TeamRoster => ({
  id, host_id: 'd', created_at: 1, team_name: '', team_label: '',
  lead: { session_id: `L${id}`, ref: '_aaaaaa', address: 'h/a', live: true },
  members: [],
})
const v = (o: unknown) => JSON.stringify(o)

let warn: ReturnType<typeof vi.spyOn>
beforeEach(() => {
  useTeamRosterStore.getState().reset()
  useTeamUiStore.setState({ memberOrder: {}, collapsed: {}, panelMode: {}, ghostWorkspace: {}, endedSeats: {} })
  warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
})
afterEach(() => warn.mockRestore())

describe('handleRosterEvent', () => {
  it('a snapshot, then a changed, each replace the host\'s list', () => {
    handleRosterEvent('h1', v({ op: 'snapshot', teams: [team('a'), team('b')] }))
    expect(useTeamRosterStore.getState().byHost.h1).toEqual([team('a'), team('b')])
    handleRosterEvent('h1', v({ op: 'changed', teams: [team('b')] }))
    expect(useTeamRosterStore.getState().byHost.h1).toEqual([team('b')])
    handleRosterEvent('h1', v({ op: 'changed', teams: [] }))
    expect(useTeamRosterStore.getState().byHost.h1).toEqual([])
    expect(warn).not.toHaveBeenCalled()
  })

  it('a malformed frame is dropped whole with one warning; the store keeps what it had', () => {
    handleRosterEvent('h1', v({ op: 'snapshot', teams: [team('a')] }))
    handleRosterEvent('h1', v({ op: 'changed', teams: [team('b'), { id: 'broken' }] }))
    expect(useTeamRosterStore.getState().byHost.h1).toEqual([team('a')])
    handleRosterEvent('h2', 'not json')
    expect(useTeamRosterStore.getState().byHost.h2).toBeUndefined()
    expect(warn).toHaveBeenCalledTimes(2)
  })
})

describe('handleRosterEvent prunes the arrangement of ended teams', () => {
  const arrange = (key: string) => {
    useTeamUiStore.getState().setMemberOrder(key, ['x'])
    useTeamUiStore.getState().setCollapsed(key, true)
    useTeamUiStore.getState().setPanelMode(key, 'line')
    useTeamUiStore.getState().setGhostWorkspace(key, 'w1')
  }
  it('the next frame drops the teams it no longer lists (a snapshot after a reconnect included), keeps the listed and other hosts', () => {
    const a = teamKeyOf('h1', 'a'), b = teamKeyOf('h1', 'b'), other = teamKeyOf('h2', 'a')
    for (const k of [a, b, other]) arrange(k)
    handleRosterEvent('h1', v({ op: 'snapshot', teams: [team('a')] }))
    const s = useTeamUiStore.getState()
    for (const slice of [s.memberOrder, s.collapsed, s.panelMode, s.ghostWorkspace]) {
      expect(Object.keys(slice).sort()).toEqual([a, other].sort())
    }
  })
  it('an empty roster drops all of the host\'s teams', () => {
    arrange(teamKeyOf('h1', 'a'))
    handleRosterEvent('h1', v({ op: 'changed', teams: [] }))
    expect(useTeamUiStore.getState().collapsed).toEqual({})
  })
  it('a dropped (malformed) frame prunes nothing', () => {
    const a = teamKeyOf('h1', 'a')
    arrange(a)
    handleRosterEvent('h1', v({ op: 'changed', teams: [{ id: 'broken' }] }))
    expect(useTeamUiStore.getState().collapsed[a]).toBe(true)
  })
})

describe('handleRosterEvent records the seats that left a team (WA-2b-1)', () => {
  const mem = (id: string, extra: Partial<TeamRoster['members'][number]> = {}) => ({
    session_id: id, ref: `_${id}`, address: `h/${id}-xx`, title: `title ${id}`, live: true, state: 'active', origin: 'spawned', joined_at: 1, ...extra,
  })
  const withMembers = (ids: Array<string | ReturnType<typeof mem>>): TeamRoster => ({ ...team('a'), members: ids.map((m) => (typeof m === 'string' ? mem(m) : m)) })
  const key = teamKeyOf('h1', 'a')
  const ended = () => useTeamUiStore.getState().endedSeats[key]

  it('a member that is gone from the next frame is recorded once, with its title and host', () => {
    handleRosterEvent('h1', v({ op: 'snapshot', teams: [withMembers(['x', 'y'])] }))
    expect(ended()).toBeUndefined() // the first frame has nothing to compare with
    handleRosterEvent('h1', v({ op: 'changed', teams: [withMembers(['x'])] }))
    expect(ended()).toEqual([expect.objectContaining({ hostId: 'h1', sessionId: 'y', title: 'title y' })])
    handleRosterEvent('h1', v({ op: 'changed', teams: [withMembers(['x'])] })) // the same roster again
    handleRosterEvent('h1', v({ op: 'snapshot', teams: [withMembers(['x'])] }))
    expect(ended()).toHaveLength(1)
  })
  it('a whole team that ended records nothing (its arrangement is pruned instead)', () => {
    handleRosterEvent('h1', v({ op: 'snapshot', teams: [withMembers(['x'])] }))
    handleRosterEvent('h1', v({ op: 'changed', teams: [] }))
    expect(useTeamUiStore.getState().endedSeats).toEqual({})
  })
  it('a malformed frame records nothing', () => {
    handleRosterEvent('h1', v({ op: 'snapshot', teams: [withMembers(['x', 'y'])] }))
    handleRosterEvent('h1', v({ op: 'changed', teams: [{ id: 'broken' }] }))
    expect(useTeamUiStore.getState().endedSeats).toEqual({})
  })
  it('losing the connection records nothing, and the frame after it does not invent endings', () => {
    handleRosterEvent('h1', v({ op: 'snapshot', teams: [withMembers(['x', 'y'])] }))
    useTeamRosterStore.getState().forgetHost('h1')
    handleRosterEvent('h1', v({ op: 'snapshot', teams: [withMembers(['x'])] }))
    expect(useTeamUiStore.getState().endedSeats).toEqual({})
  })
  it('a listed ended seat that comes back leaves the list', () => {
    handleRosterEvent('h1', v({ op: 'snapshot', teams: [withMembers(['x', 'y'])] }))
    handleRosterEvent('h1', v({ op: 'changed', teams: [withMembers(['x'])] }))
    handleRosterEvent('h1', v({ op: 'changed', teams: [withMembers(['x', 'y'])] }))
    expect(ended()).toBeUndefined()
  })
  it('a remote member is recorded on its own host; one whose host this Mac cannot name is not recorded', () => {
    useHostStore.setState({
      hosts: { h2: { id: 'h2', name: 'b26', ip: '10.0.0.2', port: 7860, daemonId: 'dm-b' } } as never,
      runtime: { h2: { daemonIdVerified: { endpoint: '10.0.0.2:7860', daemonId: 'dm-b' } } } as never,
    })
    const remote = mem('r', { host_id: 'dm-b', host_alias: 'b26' })
    const stranger = mem('z', { host_id: 'dm-unknown', host_alias: 'zz' })
    handleRosterEvent('h1', v({ op: 'snapshot', teams: [withMembers([remote, stranger])] }))
    handleRosterEvent('h1', v({ op: 'changed', teams: [withMembers([])] }))
    expect(ended()).toEqual([expect.objectContaining({ hostId: 'h2', sessionId: 'r' })])
  })
})
