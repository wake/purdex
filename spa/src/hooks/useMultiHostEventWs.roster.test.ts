// spa/src/hooks/useMultiHostEventWs.roster.test.ts — the `team.roster` host event reaches the per-host roster store
// through the real hook, bound to the connection it arrived on (plan PL-2b′). Harness as the unattended test.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { renderHook, act, waitFor } from '@testing-library/react'
import { useHostStore } from '../stores/useHostStore'
import { useSessionStore } from '../stores/useSessionStore'
import { useTeamRosterStore } from '../stores/useTeamRosterStore'
import type { TeamRoster } from '../lib/team/roster'

vi.mock('../lib/host-connection', () => ({
  checkHealth: vi.fn(async () => ({ daemon: 'connected', latency: 3, ticket: 'tk' })),
}))

const { useMultiHostEventWs } = await import('./useMultiHostEventWs')

const HOST = 'h1'

class FakeSocket {
  static OPEN = 1
  readyState = 0
  url: string
  onopen: (() => void) | null = null
  onclose: (() => void) | null = null
  onmessage: ((e: { data: unknown }) => void) | null = null
  onerror: (() => void) | null = null
  send = vi.fn()
  close = vi.fn(() => { this.readyState = 3 })
  constructor(url: string) { this.url = url; sockets.push(this) }
  emit(data: string) { this.onmessage?.({ data }) }
}

let sockets: FakeSocket[] = []

const team = (id: string): TeamRoster => ({
  id, host_id: 'd', created_at: 1, team_name: '',
  lead: { session_id: `L${id}`, ref: '_aaaaaa', address: 'h/a', live: true },
  members: [],
})
const frame = (value: unknown) => JSON.stringify({ type: 'team.roster', session: '', value: JSON.stringify(value) })
const snapshot = { op: 'snapshot', teams: [team('a')] }
const changed = { op: 'changed', teams: [team('b')] }

beforeEach(() => {
  sockets = []
  vi.stubGlobal('WebSocket', FakeSocket)
  useHostStore.setState({
    hosts: { [HOST]: { id: HOST, name: 'mlab', ip: '1.2.3.4', port: 7860, order: 0 } },
    hostOrder: [HOST],
    runtime: {},
    activeHostId: HOST,
  })
  useSessionStore.setState({ fetchHost: vi.fn(async () => {}), replaceHost: vi.fn() } as never)
  useTeamRosterStore.getState().reset()
})

afterEach(() => {
  vi.unstubAllGlobals()
  useHostStore.getState().reset()
})

describe('useMultiHostEventWs team.roster', () => {
  it('the snapshot and a changed replace this host\'s list in the store', async () => {
    const view = renderHook(() => useMultiHostEventWs())
    await waitFor(() => expect(sockets).toHaveLength(1))
    act(() => { sockets[0].emit(frame(snapshot)) })
    expect(useTeamRosterStore.getState().byHost[HOST]).toEqual(snapshot.teams)
    act(() => { sockets[0].emit(frame(changed)) })
    expect(useTeamRosterStore.getState().byHost[HOST]).toEqual(changed.teams)
    view.unmount()
  })

  it('a roster frame from a socket of a re-pointed host is dropped; the new connection\'s frames apply', async () => {
    const view = renderHook(() => useMultiHostEventWs())
    await waitFor(() => expect(sockets).toHaveLength(1))
    const old = sockets[0]
    act(() => { old.emit(frame(snapshot)) })
    act(() => {
      useHostStore.setState((s) => ({ hosts: { ...s.hosts, [HOST]: { ...s.hosts[HOST], ip: '5.6.7.8' } } }))
      useTeamRosterStore.getState().forgetHost(HOST) // what roster-forget.ts does on the same store change
      old.emit(frame(changed)) // still queued on the old socket, before the effect closes it
    })
    expect(useTeamRosterStore.getState().byHost[HOST]).toBeUndefined()
    await waitFor(() => expect(sockets).toHaveLength(2))
    act(() => { sockets[1].emit(frame(snapshot)) })
    expect(useTeamRosterStore.getState().byHost[HOST]).toEqual(snapshot.teams)
    view.unmount()
  })

  it('a roster frame from a socket of a removed host is dropped, before and after the socket closes', async () => {
    const view = renderHook(() => useMultiHostEventWs())
    await waitFor(() => expect(sockets).toHaveLength(1))
    const old = sockets[0]
    act(() => { old.emit(frame(snapshot)) })
    act(() => {
      useHostStore.setState({ hosts: {}, hostOrder: [] })
      useTeamRosterStore.getState().forgetHost(HOST)
      old.emit(frame(changed))
    })
    expect(useTeamRosterStore.getState().byHost[HOST]).toBeUndefined()
    act(() => { old.emit(frame(changed)) })
    expect(useTeamRosterStore.getState().byHost[HOST]).toBeUndefined()
    view.unmount()
  })

  it('removed and added again under the same id: the old connection\'s frames are dropped, the new one\'s apply', async () => {
    const view = renderHook(() => useMultiHostEventWs())
    await waitFor(() => expect(sockets).toHaveLength(1))
    const old = sockets[0]
    const config = useHostStore.getState().hosts[HOST]
    act(() => { old.emit(frame(snapshot)) })
    act(() => {
      useHostStore.setState({ hosts: {}, hostOrder: [] })
      useTeamRosterStore.getState().forgetHost(HOST)
    })
    act(() => { useHostStore.setState({ hosts: { [HOST]: config }, hostOrder: [HOST] }) })
    await waitFor(() => expect(sockets).toHaveLength(2))
    act(() => { old.emit(frame(changed)) })
    expect(useTeamRosterStore.getState().byHost[HOST]).toBeUndefined()
    act(() => { sockets[1].emit(frame(snapshot)) })
    expect(useTeamRosterStore.getState().byHost[HOST]).toEqual(snapshot.teams)
    view.unmount()
  })
})
