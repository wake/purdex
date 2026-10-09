// spa/src/lib/team/roster-ws.test.ts — the `team.roster` host event reaches the roster store (plan PL-2b′).
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { useTeamRosterStore } from '../../stores/useTeamRosterStore'
import { handleRosterEvent } from './roster-ws'
import { useTeamUiStore } from '../../stores/useTeamUiStore'
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
  useTeamUiStore.setState({ memberOrder: {}, collapsed: {}, panelMode: {}, ghostWorkspace: {} })
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
