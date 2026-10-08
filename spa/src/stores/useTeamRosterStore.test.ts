// spa/src/stores/useTeamRosterStore.test.ts — plan PL-2b′: replace, forget, never persisted.
import { describe, it, expect, beforeEach } from 'vitest'
import storeSource from './useTeamRosterStore.ts?raw'
import { useTeamRosterStore } from './useTeamRosterStore'
import type { TeamRoster } from '../lib/team/roster'

const team = (id: string): TeamRoster => ({
  id, host_id: 'd', created_at: 1,
  lead: { session_id: `lead-${id}`, ref: '_aaaaaa', address: 'h/a', live: true },
  members: [],
})

beforeEach(() => useTeamRosterStore.getState().reset())

describe('useTeamRosterStore', () => {
  it('apply replaces the host\'s whole list and leaves other hosts alone', () => {
    const s = useTeamRosterStore.getState()
    s.apply('h1', [team('a'), team('b')])
    s.apply('h2', [team('c')])
    s.apply('h1', [team('d')])
    expect(useTeamRosterStore.getState().byHost).toEqual({ h1: [team('d')], h2: [team('c')] })
  })

  it('an empty roster is kept as an empty list (the host answered: no teams)', () => {
    useTeamRosterStore.getState().apply('h1', [team('a')])
    useTeamRosterStore.getState().apply('h1', [])
    expect(useTeamRosterStore.getState().byHost.h1).toEqual([])
  })

  it('forgetHost drops only that host; forgetting an unknown host keeps the state object', () => {
    const s = useTeamRosterStore.getState()
    s.apply('h1', [team('a')])
    s.apply('h2', [team('b')])
    s.forgetHost('h1')
    expect(Object.keys(useTeamRosterStore.getState().byHost)).toEqual(['h2'])
    const before = useTeamRosterStore.getState()
    useTeamRosterStore.getState().forgetHost('nope')
    expect(useTeamRosterStore.getState()).toBe(before)
  })

  it('is not persisted: the store source has no persist middleware', () => {
    expect(storeSource).not.toMatch(/persist\s*\(/)
    expect(storeSource).not.toMatch(/zustand\/middleware/)
  })
})
