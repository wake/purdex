// spa/src/lib/team/roster-forget.test.ts — a removed or re-pointed host's roster is forgotten (plan PL-2b′): what the
// old daemon said about its teams is not the new one's. Same triggers as unattended-support.ts.
import { describe, it, expect, beforeEach } from 'vitest'
import { useHostStore } from '../../stores/useHostStore'
import { useTeamRosterStore } from '../../stores/useTeamRosterStore'
import { startRosterForget } from './roster-forget'
import { useTeamUiStore } from '../../stores/useTeamUiStore'
import { teamKeyOf } from './team-views'

const host = (id: string, ip = '1.2.3.4', token: string | null = null) => ({ id, name: id, ip, port: 7860, order: 0, token })

let stop: (() => void) | undefined
beforeEach(() => {
  stop?.()
  useHostStore.setState({ hosts: { h1: host('h1'), h2: host('h2') }, hostOrder: ['h1', 'h2'], runtime: {}, activeHostId: 'h1' })
  useTeamRosterStore.getState().reset()
  useTeamRosterStore.getState().apply('h1', [])
  useTeamRosterStore.getState().apply('h2', [])
  stop = startRosterForget()
})

describe('startRosterForget', () => {
  it('forgets a removed host and keeps the others', () => {
    useHostStore.setState({ hosts: { h2: host('h2') }, hostOrder: ['h2'] })
    expect(Object.keys(useTeamRosterStore.getState().byHost)).toEqual(['h2'])
  })

  it('forgets a host whose endpoint or token changed', () => {
    useHostStore.setState((s) => ({ hosts: { ...s.hosts, h1: host('h1', '5.6.7.8') } }))
    expect(Object.keys(useTeamRosterStore.getState().byHost)).toEqual(['h2'])
    useHostStore.setState((s) => ({ hosts: { ...s.hosts, h2: host('h2', '1.2.3.4', 'tok') } }))
    expect(useTeamRosterStore.getState().byHost).toEqual({})
  })

  it('tells apart a re-point whose endpoint and token read the same when joined with a colon', () => {
    // `${ip}:${port}:${token}` is `h:1:2:x` for both configurations
    useHostStore.setState((s) => ({ hosts: { ...s.hosts, h1: { ...s.hosts.h1, ip: 'h', port: 1, token: '2:x' } } }))
    expect(Object.keys(useTeamRosterStore.getState().byHost)).toEqual(['h2'])
    useTeamRosterStore.getState().apply('h1', [])
    useHostStore.setState((s) => ({ hosts: { ...s.hosts, h1: { ...s.hosts.h1, ip: 'h:1', port: 2, token: 'x' } } }))
    expect(Object.keys(useTeamRosterStore.getState().byHost)).toEqual(['h2'])
  })

  it('leaves everyone alone on a change that is neither (a rename, a runtime update)', () => {
    useHostStore.setState((s) => ({ hosts: { ...s.hosts, h1: { ...s.hosts.h1, name: 'renamed' } } }))
    useHostStore.setState({ runtime: { h1: { status: 'connected' } as never } })
    expect(Object.keys(useTeamRosterStore.getState().byHost).sort()).toEqual(['h1', 'h2'])
  })

  it('stops when the returned function is called', () => {
    stop?.()
    stop = undefined
    useHostStore.setState({ hosts: {}, hostOrder: [] })
    expect(Object.keys(useTeamRosterStore.getState().byHost).sort()).toEqual(['h1', 'h2'])
  })
})

describe('the arrangement outlives the roster (plan review #5)', () => {
  it('is kept when the host is re-pointed at another endpoint or token (forgetHost does not prune it)', () => {
    const key = teamKeyOf('h1', 't1')
    useTeamUiStore.setState({ memberOrder: { [key]: ['a'] }, collapsed: { [key]: true }, panelMode: {}, ghostWorkspace: {} })
    useHostStore.setState((s) => ({ hosts: { ...s.hosts, h1: host('h1', '5.6.7.8') } }))
    expect(Object.keys(useTeamRosterStore.getState().byHost)).toEqual(['h2']) // the roster went
    expect(useTeamUiStore.getState().collapsed[key]).toBe(true) // the arrangement did not
    expect(useTeamUiStore.getState().memberOrder[key]).toEqual(['a'])
  })
  it('is kept on a disconnect (the roster store forgetting a host)', () => {
    const key = teamKeyOf('h1', 't1')
    useTeamUiStore.setState({ memberOrder: {}, collapsed: { [key]: true }, panelMode: {}, ghostWorkspace: {} })
    useTeamRosterStore.getState().forgetHost('h1')
    expect(useTeamUiStore.getState().collapsed[key]).toBe(true)
  })
})
