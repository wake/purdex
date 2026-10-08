// U1-3: the daemon `hook` frames reach the agent store through the transition rule (applyHookEvent).
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { renderHook, waitFor } from '@testing-library/react'
import { useHostStore } from '../stores/useHostStore'
import { useSessionStore } from '../stores/useSessionStore'
import { useAgentStore } from '../stores/useAgentStore'

vi.mock('../lib/host-connection', () => ({
  checkHealth: vi.fn(async () => ({ daemon: 'connected', latency: 3, ticket: 'tk' })),
}))
vi.mock('../lib/nex/nex-api', () => ({ listExecutions: vi.fn() }))
vi.mock('../lib/nex/nex-sse', () => ({ openNexSse: vi.fn(() => ({ close: vi.fn() })) }))

const { useMultiHostEventWs } = await import('./useMultiHostEventWs')

const HOST = 'h1'

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

beforeEach(() => {
  sockets = []
  vi.stubGlobal('WebSocket', FakeSocket)
  useHostStore.setState({
    hosts: { [HOST]: { id: HOST, name: 'Host', ip: '1.2.3.4', port: 7860, order: 0 } },
    hostOrder: [HOST],
    runtime: {},
    activeHostId: HOST,
  })
  useSessionStore.setState({ fetchHost: vi.fn(async () => {}), replaceHost: vi.fn() } as never)
  useAgentStore.setState({ statuses: {}, agentTypes: {}, models: {}, subagents: {}, lastEvents: {}, unread: {} })
})

afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
  useHostStore.getState().reset()
})

const hookFrame = (session: string, status: string) => JSON.stringify({
  type: 'hook',
  session,
  value: JSON.stringify({ agent_type: 'cc', status, raw_event_name: 'PdxStop', broadcast_ts: 1 }),
})

describe('useMultiHostEventWs hook frames (U1-3a)', () => {
  it('a hook frame goes through applyHookEvent (the transition rule), not the worker rule', async () => {
    const view = renderHook(() => useMultiHostEventWs())
    await waitFor(() => expect(sockets).toHaveLength(1))
    const key = `${HOST}:dev`

    // first sight of a code is not news (the worker rule would mark an idle Stop unread)
    sockets[0].emit(hookFrame('dev', 'idle'))
    expect(useAgentStore.getState().statuses[key]).toBe('idle')
    expect(useAgentStore.getState().unread[key]).toBeUndefined()

    // the same status again is not news either
    sockets[0].emit(hookFrame('dev', 'idle'))
    expect(useAgentStore.getState().unread[key]).toBeUndefined()

    // a real transition is
    sockets[0].emit(hookFrame('dev', 'running'))
    sockets[0].emit(hookFrame('dev', 'idle'))
    expect(useAgentStore.getState().unread[key]).toBe(true)
    view.unmount()
  })
})
