// spa/src/hooks/useMultiHostEventWs.fetchgen.test.ts — the hook's own teardown paths (host removed, re-pointed, unmount)
// close the socket without `onClose`, so each must retire the connection itself: a session fetch in flight is discarded.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { renderHook, act, waitFor } from '@testing-library/react'
import * as hostApi from '../lib/host-api'
import { useHostStore } from '../stores/useHostStore'
import { useSessionStore } from '../stores/useSessionStore'
import { useTeamRosterStore } from '../stores/useTeamRosterStore'

vi.mock('../lib/host-connection', () => ({
  checkHealth: vi.fn(async () => ({ daemon: 'connected', latency: 3, ticket: 'tk' })),
}))
const { useMultiHostEventWs } = await import('./useMultiHostEventWs')

const HOST = 'h1'
class FakeSocket {
  static OPEN = 1
  readyState = 0
  onopen: (() => void) | null = null
  onclose: (() => void) | null = null
  onmessage: ((e: { data: unknown }) => void) | null = null
  onerror: (() => void) | null = null
  send = vi.fn()
  close = vi.fn(() => { this.readyState = 3 })
  constructor() { sockets.push(this) }
}
let sockets: FakeSocket[] = []
const realFetchHost = useSessionStore.getState().fetchHost

beforeEach(() => {
  sockets = []
  vi.stubGlobal('WebSocket', FakeSocket)
  useHostStore.setState({ hosts: { [HOST]: { id: HOST, name: 'mlab', ip: '1.2.3.4', port: 7860, order: 0 } }, hostOrder: [HOST], runtime: {}, activeHostId: HOST })
  useSessionStore.setState({ sessions: {} })
  useTeamRosterStore.getState().reset()
})
afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
  useHostStore.getState().reset()
})

async function fetchAcross(retire: () => void) {
  let resolve!: (v: never[]) => void
  vi.spyOn(hostApi, 'listSessions').mockReturnValue(new Promise((r) => { resolve = r as typeof resolve }))
  const p = realFetchHost(HOST)
  act(retire)
  resolve([])
  await p
  expect(useTeamRosterStore.getState().liveSessionHosts).toEqual({})
  expect(useSessionStore.getState().sessions[HOST]).toBeUndefined()
}

describe('useMultiHostEventWs retires the connection itself', () => {
  it('host removed from hostOrder', async () => {
    const view = renderHook(() => useMultiHostEventWs())
    await waitFor(() => expect(sockets).toHaveLength(1))
    await fetchAcross(() => useHostStore.setState({ hosts: {}, hostOrder: [] }))
    view.unmount()
  })
  it('host re-pointed (config key changed)', async () => {
    const view = renderHook(() => useMultiHostEventWs())
    await waitFor(() => expect(sockets).toHaveLength(1))
    await fetchAcross(() => useHostStore.setState((s) => ({ hosts: { ...s.hosts, [HOST]: { ...s.hosts[HOST], ip: '5.6.7.8' } } })))
    view.unmount()
  })
  it('unmount', async () => {
    const view = renderHook(() => useMultiHostEventWs())
    await waitFor(() => expect(sockets).toHaveLength(1))
    await fetchAcross(() => view.unmount())
  })
})
