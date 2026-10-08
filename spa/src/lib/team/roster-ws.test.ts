// spa/src/lib/team/roster-ws.test.ts — the `team.roster` host event reaches the roster store (plan PL-2b′).
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { useTeamRosterStore } from '../../stores/useTeamRosterStore'
import { handleRosterEvent } from './roster-ws'
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
