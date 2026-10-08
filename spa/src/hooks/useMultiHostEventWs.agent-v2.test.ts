// U1-3: the daemon `hook` frames reach the agent store through the transition rule (a), ordered by the
// per-connection (epoch, seq) cursor and replaced by the complete `agent.snapshot` (b).
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { renderHook, waitFor, act } from '@testing-library/react'
import { useHostStore } from '../stores/useHostStore'
import { useSessionStore } from '../stores/useSessionStore'
import { useAgentStore } from '../stores/useAgentStore'
import { useExecutionListStore } from '../stores/useExecutionListStore'
import * as healthMod from '../lib/host-connection'

vi.mock('../lib/host-connection', () => ({
  checkHealth: vi.fn(async () => ({ daemon: 'connected', latency: 3, ticket: 'tk' })),
}))
vi.mock('../lib/host-api', async (orig) => ({
  ...(await orig<typeof import('../lib/host-api')>()),
  fetchWsTicket: vi.fn(async () => 'tk2'),
}))
vi.mock('../lib/nex/nex-api', () => ({ listExecutions: vi.fn() }))
vi.mock('../lib/nex/nex-sse', () => ({ openNexSse: vi.fn(() => ({ close: vi.fn() })) }))
const approval = vi.hoisted(() => ({ handleApprovalEvent: vi.fn() }))
vi.mock('../lib/team/approval-ws', () => approval)

const { useMultiHostEventWs } = await import('./useMultiHostEventWs')

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
const onHello = vi.fn()
const applyDelta = vi.fn()
const originalNex = { onHello: useExecutionListStore.getState().onHello, applyDelta: useExecutionListStore.getState().applyDelta }

beforeEach(() => {
  sockets = []
  onHello.mockClear(); applyDelta.mockClear(); approval.handleApprovalEvent.mockClear()
  vi.mocked(healthMod.checkHealth).mockClear()
  vi.spyOn(console, 'warn').mockImplementation(() => {})
  vi.stubGlobal('WebSocket', FakeSocket)
  useHostStore.setState({
    hosts: { [HOST]: { id: HOST, name: 'Host', ip: '1.2.3.4', port: 7860, order: 0 } },
    hostOrder: [HOST],
    runtime: {},
    activeHostId: HOST,
  })
  useSessionStore.setState({ fetchHost: vi.fn(async () => {}), replaceHost: vi.fn() } as never)
  useExecutionListStore.setState({ onHello, applyDelta })
  useAgentStore.setState({ statuses: {}, agentTypes: {}, models: {}, subagents: {}, lastEvents: {}, unread: {}, oscTitles: {}, ccStatus: {} })
})

afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
  useHostStore.getState().reset()
  useExecutionListStore.setState(originalNex)
})

const ev = (status: string, extra: Record<string, unknown> = {}) =>
  ({ agent_type: 'cc', status, raw_event_name: 'PdxStop', broadcast_ts: 1, ...extra })
const hookFrame = (session: string, status: string, epoch?: string, seq?: number) => JSON.stringify({
  type: 'hook',
  session,
  value: JSON.stringify(epoch === undefined ? ev(status) : ev(status, { epoch, seq })),
})
const snapshotFrame = (epoch: string, seq: number, entries: Record<string, string>) => JSON.stringify({
  type: 'agent.snapshot',
  session: '',
  value: JSON.stringify({
    epoch, seq,
    sessions: Object.entries(entries).map(([session, status]) => ({ session, event: ev(status, { raw_event_name: 'replay', epoch, seq, snapshot: true }) })),
  }),
})
const nexFrame = (type: string, value: unknown) => JSON.stringify({ type, session: '', value: JSON.stringify(value) })

/** Mount, wait for the first socket and open it (the hook's onOpen). */
const connect = async () => {
  const view = renderHook(() => useMultiHostEventWs())
  await waitFor(() => expect(sockets).toHaveLength(1))
  act(() => { sockets[0].onopen?.() })
  return view
}

describe('useMultiHostEventWs hook frames (U1-3a)', () => {
  it('a legacy hook frame goes through applyHookEvent (the transition rule), not the worker rule', async () => {
    const view = await connect()
    const k = key('dev')

    // first sight of a code is not news (the worker rule would mark an idle Stop unread)
    act(() => { sockets[0].emit(hookFrame('dev', 'idle')) })
    expect(agent().statuses[k]).toBe('idle')
    expect(agent().unread[k]).toBeUndefined()

    // the same status again is not news either
    act(() => { sockets[0].emit(hookFrame('dev', 'idle')) })
    expect(agent().unread[k]).toBeUndefined()

    // a real transition is
    act(() => { sockets[0].emit(hookFrame('dev', 'running')); sockets[0].emit(hookFrame('dev', 'idle')) })
    expect(agent().unread[k]).toBe(true)
    view.unmount()
  })
})

describe('useMultiHostEventWs agent=v2 (U1-3b)', () => {
  it('the URL carries agent=v2 next to nex=v1 and the ticket', async () => {
    const view = await connect()
    const u = new URL(sockets[0].url)
    expect(u.searchParams.getAll('agent')).toEqual(['v2'])
    expect(u.searchParams.getAll('nex')).toEqual(['v1'])
    expect(u.searchParams.get('ticket')).toBe('tk')
    view.unmount()
  })

  it('hook frames before the snapshot are dropped; the snapshot carries the state', async () => {
    const view = await connect()
    act(() => { sockets[0].emit(hookFrame('dev', 'running', 'E1', 9)) })
    expect(agent().statuses[key('dev')]).toBeUndefined()
    act(() => { sockets[0].emit(snapshotFrame('E1', 10, { dev: 'idle' })) })
    expect(agent().statuses[key('dev')]).toBe('idle')
    view.unmount()
  })

  it('contiguous frames after the snapshot apply in order; a duplicate or old seq is dropped', async () => {
    const view = await connect()
    act(() => { sockets[0].emit(snapshotFrame('E1', 10, { dev: 'idle' })) })
    act(() => { sockets[0].emit(hookFrame('dev', 'running', 'E1', 11)) })
    expect(agent().statuses[key('dev')]).toBe('running')
    act(() => { sockets[0].emit(hookFrame('dev', 'idle', 'E1', 11)) }) // duplicate
    act(() => { sockets[0].emit(hookFrame('dev', 'idle', 'E1', 4)) })  // old
    expect(agent().statuses[key('dev')]).toBe('running')
    act(() => { sockets[0].emit(hookFrame('dev', 'idle', 'E1', 12)) })
    expect(agent().statuses[key('dev')]).toBe('idle')
    view.unmount()
  })

  it('a gap triggers exactly one resync and nothing is applied until the next snapshot', async () => {
    const view = await connect()
    act(() => { sockets[0].emit(snapshotFrame('E1', 10, { dev: 'idle' })) })
    act(() => {
      sockets[0].emit(hookFrame('dev', 'running', 'E1', 12)) // gap: 11 lost
      sockets[0].emit(hookFrame('dev', 'running', 'E1', 14)) // a second gap frame in the same tick
    })
    expect(agent().statuses[key('dev')]).toBe('idle')
    await waitFor(() => expect(sockets).toHaveLength(2))
    expect(sockets[0].close).toHaveBeenCalled()

    // the new connection: frames before its snapshot are dropped, after it they apply again
    act(() => { sockets[1].onopen?.() })
    act(() => { sockets[1].emit(hookFrame('dev', 'running', 'E1', 16)) })
    expect(agent().statuses[key('dev')]).toBe('idle')
    act(() => { sockets[1].emit(snapshotFrame('E1', 20, { dev: 'running' })) })
    act(() => { sockets[1].emit(hookFrame('dev', 'idle', 'E1', 21)) })
    expect(agent().statuses[key('dev')]).toBe('idle')
    expect(sockets).toHaveLength(2)
    view.unmount()
  })

  it('a resync retires the old socket at once: no health check, frames of any type on it are dropped, the gate is closed', async () => {
    const view = await connect()
    useHostStore.getState().setRuntime(HOST, { attachReady: true, status: 'connected' })
    act(() => { sockets[0].emit(snapshotFrame('E1', 10, { dev: 'idle' })) })
    const healthCalls = vi.mocked(healthMod.checkHealth).mock.calls.length

    act(() => { sockets[0].emit(hookFrame('dev', 'running', 'E1', 12)) })
    // synchronously after the gap frame, before any new socket exists
    expect(useHostStore.getState().runtime[HOST]?.attachReady).toBe(false)
    expect(useHostStore.getState().runtime[HOST]?.status).toBe('reconnecting')
    expect(vi.mocked(healthMod.checkHealth).mock.calls.length).toBe(healthCalls)

    // whatever is still queued on the old socket — of any type — is dropped
    act(() => {
      sockets[0].emit(hookFrame('dev', 'running', 'E1', 13))
      sockets[0].emit(JSON.stringify({ type: 'approval.request', session: '', value: '{}' }))
    })
    expect(agent().statuses[key('dev')]).toBe('idle')
    expect(approval.handleApprovalEvent).not.toHaveBeenCalled()
    await waitFor(() => expect(sockets).toHaveLength(2))
    expect(vi.mocked(healthMod.checkHealth).mock.calls.length).toBe(healthCalls)
    view.unmount()
  })

  it('a foreign epoch on a live frame resyncs', async () => {
    const view = await connect()
    act(() => { sockets[0].emit(snapshotFrame('E1', 10, { dev: 'idle' })) })
    act(() => { sockets[0].emit(hookFrame('dev', 'running', 'E2', 11)) })
    expect(agent().statuses[key('dev')]).toBe('idle')
    await waitFor(() => expect(sockets).toHaveLength(2))
    view.unmount()
  })

  it('a legacy daemon (no epoch / seq, no snapshot) keeps working with the transition rule', async () => {
    const view = await connect()
    act(() => { sockets[0].emit(hookFrame('dev', 'running')) })
    act(() => { sockets[0].emit(hookFrame('dev', 'idle')) })
    expect(agent().statuses[key('dev')]).toBe('idle')
    expect(agent().unread[key('dev')]).toBe(true)
    expect(sockets).toHaveLength(1)
    view.unmount()
  })

  it('onClose resets the cursor: frames after a reconnect wait for the new snapshot', async () => {
    const view = await connect()
    act(() => { sockets[0].emit(snapshotFrame('E1', 10, { dev: 'idle' })) })
    act(() => { sockets[0].onclose?.() })
    await waitFor(() => expect(sockets).toHaveLength(2))
    act(() => { sockets[1].onopen?.() })
    act(() => { sockets[1].emit(hookFrame('dev', 'running', 'E1', 11)) })
    expect(agent().statuses[key('dev')]).toBe('idle') // dropped: no snapshot on this connection yet
    act(() => { sockets[1].emit(snapshotFrame('E1', 11, { dev: 'running' })) })
    expect(agent().statuses[key('dev')]).toBe('running')
    view.unmount()
  })

  it('a malformed snapshot is dropped without a reconnect and leaves the cursor closed', async () => {
    const view = await connect()
    act(() => {
      sockets[0].emit(JSON.stringify({ type: 'agent.snapshot', session: '', value: JSON.stringify({ epoch: 'E1', seq: -1, sessions: [] }) }))
      sockets[0].emit(JSON.stringify({ type: 'agent.snapshot', session: '', value: 'not json' }))
      sockets[0].emit(hookFrame('dev', 'running', 'E1', 1))
    })
    expect(agent().statuses[key('dev')]).toBeUndefined()
    expect(sockets).toHaveLength(1)
    view.unmount()
  })

  it('an empty snapshot clears the host\'s agent codes (and spells seq 0)', async () => {
    const view = await connect()
    useAgentStore.setState({ statuses: { [key('dev')]: 'idle', [key('exec-e1')]: 'running' } })
    act(() => { sockets[0].emit(snapshotFrame('E1', 0, {})) })
    expect(agent().statuses[key('dev')]).toBeUndefined()
    expect(agent().statuses[key('exec-e1')]).toBe('running')
    act(() => { sockets[0].emit(hookFrame('dev', 'running', 'E1', 1)) })
    expect(agent().statuses[key('dev')]).toBe('running')
    view.unmount()
  })

  it('a snapshot or hook frame queued on the socket of a re-pointed or removed host is not written', async () => {
    const view = await connect()
    act(() => { sockets[0].emit(snapshotFrame('E1', 10, { dev: 'idle' })) })
    // the host store changes first; the effect that closes the old socket has not run yet (same act)
    act(() => {
      useHostStore.setState({ hosts: { [HOST]: { id: HOST, name: 'Host', ip: '9.9.9.9', port: 7860, order: 0 } } })
      sockets[0].emit(hookFrame('dev', 'running', 'E1', 11))
      sockets[0].emit(snapshotFrame('E1', 50, { other: 'running' }))
    })
    expect(agent().statuses[key('dev')]).toBe('idle')
    expect(agent().statuses[key('other')]).toBeUndefined()
    view.unmount()
  })

  it('the nex cursor is untouched by a hook resync', async () => {
    const view = await connect()
    act(() => { sockets[0].emit(nexFrame('nex.executions.hello', { epoch: 'N1', bseq: 0 })) })
    act(() => { sockets[0].emit(snapshotFrame('E1', 10, { dev: 'idle' })) })
    act(() => { sockets[0].emit(hookFrame('dev', 'running', 'E1', 12)) })
    await waitFor(() => expect(sockets).toHaveLength(2))
    act(() => { sockets[1].onopen?.() })
    act(() => { sockets[1].emit(nexFrame('nex.execution', { epoch: 'N1', bseq: 1, id: 'x', ver: 1, cause: [], row: null })) })
    expect(applyDelta).toHaveBeenCalledTimes(1)
    view.unmount()
  })
})
