// BG-5: read sync on the host-events socket (app-badge spec §7.2, §7.3): the URL opts in with reads=v1, and the two read
// frames (`session.reads` on connect, `session.read` per accepted read) clear unreads by the cover rule — without ever
// sending anything back.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { renderHook, waitFor, act } from '@testing-library/react'
import { useHostStore } from '../stores/useHostStore'
import { useSessionStore } from '../stores/useSessionStore'
import { useAgentStore } from '../stores/useAgentStore'
import { useExecutionListStore } from '../stores/useExecutionListStore'

vi.mock('../lib/host-connection', () => ({
  checkHealth: vi.fn(async () => ({ daemon: 'connected', latency: 3, ticket: 'tk' })),
}))
vi.mock('../lib/host-api', async (orig) => ({
  ...(await orig<typeof import('../lib/host-api')>()),
  fetchWsTicket: vi.fn(async () => 'tk2'),
  pinnedHostFetch: vi.fn(),
}))
vi.mock('../lib/nex/nex-api', () => ({ listExecutions: vi.fn() }))
vi.mock('../lib/nex/nex-sse', () => ({ openNexSse: vi.fn(() => ({ close: vi.fn() })) }))
vi.mock('../lib/team/approval-ws', () => ({ handleApprovalEvent: vi.fn() }))

const { useMultiHostEventWs } = await import('./useMultiHostEventWs')
const { pinnedHostFetch } = await import('../lib/host-api')

const HOST = 'h1'
const key = (code: string) => `${HOST}:${code}`
const agent = () => useAgentStore.getState()

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
  vi.spyOn(console, 'warn').mockImplementation(() => {})
  vi.stubGlobal('WebSocket', FakeSocket)
  vi.mocked(pinnedHostFetch).mockReset()
  useHostStore.setState({
    hosts: { [HOST]: { id: HOST, name: 'Host', ip: '1.2.3.4', port: 7860, order: 0 } },
    hostOrder: [HOST],
    runtime: {},
    activeHostId: HOST,
  })
  useSessionStore.setState({ fetchHost: vi.fn(async () => {}), replaceHost: vi.fn() } as never)
  useExecutionListStore.setState({ onHello: vi.fn(), applyDelta: vi.fn() })
  useAgentStore.setState({ statuses: {}, agentTypes: {}, models: {}, subagents: {}, lastEvents: {}, unread: {}, unreadCursor: {}, oscTitles: {}, ccStatus: {} })
})

afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
  useHostStore.getState().reset()
})

const frame = (type: string, session: string, value: unknown) => JSON.stringify({ type, session, value: JSON.stringify(value) })
/** The hook cursor of this connection: the snapshot opens it at seq 10, every hook frame after it is the next seq. */
let seq = 10
const ev = (status: string, s: number) => ({ agent_type: 'cc', status, raw_event_name: 'PdxStop', broadcast_ts: 1, epoch: 'E1', seq: s })
const connect = async () => {
  seq = 10
  const view = renderHook(() => useMultiHostEventWs())
  await waitFor(() => expect(sockets).toHaveLength(1))
  act(() => {
    sockets[0].onopen?.()
    sockets[0].emit(frame('agent.snapshot', '', { epoch: 'E1', seq, sessions: [] }))
  })
  return view
}
/** An unread of `code` set through the real frames (running, then idle); returns the seq of the idle that set it. */
function unreadBy(code: string): number {
  act(() => {
    sockets[0].emit(frame('hook', code, ev('running', ++seq)))
    sockets[0].emit(frame('hook', code, ev('idle', ++seq)))
  })
  return seq
}

describe('useMultiHostEventWs reads=v1 (BG-5)', () => {
  it('the URL opts in with reads=v1, next to agent=v2, nex=v1 and the ticket', async () => {
    const view = await connect()
    const u = new URL(sockets[0].url)
    expect(u.searchParams.getAll('reads')).toEqual(['v1'])
    expect(u.searchParams.getAll('agent')).toEqual(['v2'])
    expect(u.searchParams.getAll('nex')).toEqual(['v1'])
    expect(u.searchParams.get('ticket')).toBe('tk')
    view.unmount()
  })

  it('a session.reads snapshot clears what it covers and leaves a later event unread', async () => {
    const view = await connect()
    const a = unreadBy('aaaaaa')
    const b = unreadBy('bbbbbb')
    expect(agent().unread[key('aaaaaa')]).toBe(true)
    expect(agent().unread[key('bbbbbb')]).toBe(true)
    act(() => {
      sockets[0].emit(frame('session.reads', '', { reads: [
        { code: 'aaaaaa', through: { epoch: 'E1', seq: a } },
        { code: 'bbbbbb', through: { epoch: 'E1', seq: b - 1 } },
      ] }))
    })
    expect(agent().unread[key('aaaaaa')]).toBeUndefined()
    expect(agent().unread[key('bbbbbb')]).toBe(true)
    view.unmount()
  })

  it('a session.read frame clears its session', async () => {
    const view = await connect()
    const a = unreadBy('aaaaaa')
    act(() => { sockets[0].emit(frame('session.read', 'aaaaaa', { code: 'aaaaaa', through: { epoch: 'E1', seq: a } })) })
    expect(agent().unread[key('aaaaaa')]).toBeUndefined()
    view.unmount()
  })

  it('a read of another epoch clears nothing (a daemon restart makes the cursors incomparable)', async () => {
    const view = await connect()
    unreadBy('aaaaaa')
    act(() => { sockets[0].emit(frame('session.read', 'aaaaaa', { code: 'aaaaaa', through: { epoch: 'E2', seq: 99 } })) })
    expect(agent().unread[key('aaaaaa')]).toBe(true)
    view.unmount()
  })

  it('applying received reads sends nothing back', async () => {
    const view = await connect()
    const a = unreadBy('aaaaaa')
    act(() => {
      sockets[0].emit(frame('session.read', 'aaaaaa', { code: 'aaaaaa', through: { epoch: 'E1', seq: a } }))
      sockets[0].emit(frame('session.reads', '', { reads: [{ code: 'aaaaaa', through: { epoch: 'E1', seq: a } }] }))
    })
    expect(pinnedHostFetch).not.toHaveBeenCalled()
    view.unmount()
  })

  it('a malformed read frame is ignored and the hook keeps working', async () => {
    const view = await connect()
    const a = unreadBy('aaaaaa')
    act(() => {
      sockets[0].emit(JSON.stringify({ type: 'session.read', session: 'aaaaaa', value: 'not json' }))
      sockets[0].emit(JSON.stringify({ type: 'session.reads', session: '', value: '{"reads":5}' }))
    })
    expect(agent().unread[key('aaaaaa')]).toBe(true)
    act(() => { sockets[0].emit(frame('session.read', 'aaaaaa', { code: 'aaaaaa', through: { epoch: 'E1', seq: a } })) })
    expect(agent().unread[key('aaaaaa')]).toBeUndefined()
    view.unmount()
  })
})
