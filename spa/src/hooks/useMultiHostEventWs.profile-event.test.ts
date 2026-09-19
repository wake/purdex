// spa/src/hooks/useMultiHostEventWs.profile-event.test.ts — a `profile` host
// event is handed to the profile dispatch seam with the hostId of the socket it
// arrived on (the event itself carries no host).
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { renderHook, act, waitFor } from '@testing-library/react'
import { useHostStore } from '../stores/useHostStore'
import { useSessionStore } from '../stores/useSessionStore'
import {
  __resetProfileEventsForTest,
  subscribeProfileEvents,
  type ProfileRemoteEvent,
} from '../lib/profile/profile-ws-dispatch'

vi.mock('../lib/host-connection', () => ({
  checkHealth: vi.fn(async () => ({ daemon: 'connected', latency: 3, ticket: 'tk' })),
}))

const { useMultiHostEventWs } = await import('./useMultiHostEventWs')

class FakeSocket {
  static OPEN = 1
  readyState = 0
  binaryType = ''
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
let fetchHost: ReturnType<typeof vi.fn>

beforeEach(() => {
  sockets = []
  fetchHost = vi.fn(async () => {})
  vi.stubGlobal('WebSocket', FakeSocket)
  useHostStore.setState({
    hosts: {
      h1: { id: 'h1', name: 'One', ip: '1.2.3.4', port: 7860, order: 0 },
      h2: { id: 'h2', name: 'Two', ip: '5.6.7.8', port: 7860, order: 1 },
    },
    hostOrder: ['h1', 'h2'],
    runtime: {},
    activeHostId: 'h1',
  })
  useSessionStore.setState({ fetchHost, replaceHost: vi.fn() } as never)
})

afterEach(() => {
  __resetProfileEventsForTest()
  vi.unstubAllGlobals()
  useHostStore.getState().reset()
})

describe('useMultiHostEventWs profile events', () => {
  it('forwards a profile event to the dispatch seam with the socket’s hostId', async () => {
    const received: ProfileRemoteEvent[] = []
    subscribeProfileEvents((e) => { received.push(e) })

    const view = renderHook(() => useMultiHostEventWs())
    await waitFor(() => expect(sockets).toHaveLength(2))
    const h2Socket = sockets.find((s) => s.url.includes('5.6.7.8'))
    expect(h2Socket).toBeDefined()
    fetchHost.mockClear()

    const hash = 'b'.repeat(64)
    act(() => {
      h2Socket!.emit(JSON.stringify({
        type: 'profile',
        session: '',
        value: JSON.stringify({
          profileId: 'p_1', section: 'tabs', rev: 3, hash, writerClientId: 'c_0123456789ab',
        }),
      }))
    })

    expect(received).toEqual([{
      hostId: 'h2', profileId: 'p_1', section: 'tabs', rev: 3, hash,
      writerClientId: 'c_0123456789ab',
    }])
    // A profile event is not a session event: no refetch rides along.
    expect(fetchHost).not.toHaveBeenCalled()

    view.unmount()
  })
})
