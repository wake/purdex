// spa/src/hooks/useMultiHostEventWs.dialogue.test.ts — the `dialogue.recap` host event reaches the dialogue store through the real hook,
// bound to the connection it arrived on (as relay.fresh): it re-reads the recaps of a cached session; a frame from the socket of a
// re-pointed host is dropped. Harness as the relay-fresh test.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { renderHook, act, waitFor } from '@testing-library/react'
import { useHostStore } from '../stores/useHostStore'
import { useSessionStore } from '../stores/useSessionStore'
import { useNexHostStore } from '../stores/useNexHostStore'
import { useDialogueStore } from '../stores/useDialogueStore'

vi.mock('../lib/host-connection', () => ({
  checkHealth: vi.fn(async () => ({ daemon: 'connected', latency: 3, ticket: 'tk' })),
}))
const fetchRecaps = vi.fn()
vi.mock('../lib/dialogue/api', () => ({
  fetchRecaps: (...a: unknown[]) => fetchRecaps(...a),
  fetchTurnSummaries: vi.fn(async () => []),
  requestRecap: vi.fn(),
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

const answer = (id: number) => ({ recaps: [{ id }], can_generate: true, newest_turn_no: 1, last_recap_to_no: 1 })
const frame = (sid: string) => JSON.stringify({ type: 'dialogue.recap', session: '', value: JSON.stringify({ session_id: sid, recap_id: 2 }) })
const flush = () => new Promise((r) => setTimeout(r, 0))

beforeEach(() => {
  sockets = []
  fetchRecaps.mockReset()
  vi.stubGlobal('WebSocket', FakeSocket)
  useHostStore.setState({
    hosts: { [HOST]: { id: HOST, name: 'mlab', ip: '1.2.3.4', port: 7860, token: 't', order: 0 } },
    hostOrder: [HOST],
    runtime: {},
    activeHostId: HOST,
  })
  useSessionStore.setState({ fetchHost: vi.fn(async () => {}), replaceHost: vi.fn() } as never)
  useNexHostStore.setState({ byHost: {} })
  useDialogueStore.getState().reset()
  useDialogueStore.getState().setSupport(HOST, true)
})

afterEach(() => {
  vi.unstubAllGlobals()
  useHostStore.getState().reset()
  useNexHostStore.setState({ byHost: {} })
  useDialogueStore.getState().reset()
})

describe('useMultiHostEventWs dialogue.recap', () => {
  it('re-reads the recaps of a session the store holds', async () => {
    fetchRecaps.mockResolvedValue(answer(1))
    await useDialogueStore.getState().loadRecaps(HOST, 'S1')
    fetchRecaps.mockClear()
    fetchRecaps.mockResolvedValue(answer(2))
    const view = renderHook(() => useMultiHostEventWs())
    await waitFor(() => expect(sockets).toHaveLength(1))
    act(() => { sockets[0].emit(frame('S1')) })
    await waitFor(() => expect(fetchRecaps).toHaveBeenCalledTimes(1))
    await waitFor(() => expect(useDialogueStore.getState().byHost[HOST].S1.recaps?.recaps[0].id).toBe(2))
    view.unmount()
  })

  it('does not fetch for a session the store does not hold', async () => {
    const view = renderHook(() => useMultiHostEventWs())
    await waitFor(() => expect(sockets).toHaveLength(1))
    act(() => { sockets[0].emit(frame('unknown')) })
    await flush()
    expect(fetchRecaps).not.toHaveBeenCalled()
    view.unmount()
  })

  it('a frame from the socket of a re-pointed host is dropped', async () => {
    fetchRecaps.mockResolvedValue(answer(1))
    await useDialogueStore.getState().loadRecaps(HOST, 'S1')
    fetchRecaps.mockClear()
    const view = renderHook(() => useMultiHostEventWs())
    await waitFor(() => expect(sockets).toHaveLength(1))
    const old = sockets[0]
    act(() => {
      useHostStore.setState((s) => ({ hosts: { ...s.hosts, [HOST]: { ...s.hosts[HOST], ip: '5.6.7.8' } } }))
      old.emit(frame('S1')) // still queued on the old socket, before the effect closes it
    })
    await flush()
    expect(fetchRecaps).not.toHaveBeenCalled()
    view.unmount()
  })
})
