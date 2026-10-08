// #1866 PR2b: the host-events stream opts in to the nex frames (`nex=v1`) and routes the hello / delta to the list store.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { renderHook, act, waitFor } from '@testing-library/react'
import { useHostStore } from '../stores/useHostStore'
import { useSessionStore } from '../stores/useSessionStore'
import { useExecutionListStore, resetExecutionListForTests } from '../stores/useExecutionListStore'
import { useNexHostStore } from '../stores/useNexHostStore'
import * as api from '../lib/nex/nex-api'

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
const onHello = vi.fn()
const applyDelta = vi.fn()
const original = { onHello: useExecutionListStore.getState().onHello, applyDelta: useExecutionListStore.getState().applyDelta }

beforeEach(() => {
  sockets = []
  onHello.mockClear()
  applyDelta.mockClear()
  vi.stubGlobal('WebSocket', FakeSocket)
  useHostStore.setState({
    hosts: { [HOST]: { id: HOST, name: 'Host', ip: '1.2.3.4', port: 7860, order: 0 } },
    hostOrder: [HOST],
    runtime: {},
    activeHostId: HOST,
  })
  useSessionStore.setState({ fetchHost: vi.fn(async () => {}), replaceHost: vi.fn() } as never)
  useExecutionListStore.setState({ byHost: {}, onHello, applyDelta })
})

afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
  useHostStore.getState().reset()
  useExecutionListStore.setState(original)
})

const frame = (type: string, value: unknown) => JSON.stringify({ type, session: '', value: typeof value === 'string' ? value : JSON.stringify(value) })
const open = async () => {
  const view = renderHook(() => useMultiHostEventWs())
  await waitFor(() => expect(sockets).toHaveLength(1))
  return view
}

describe('useMultiHostEventWs nex frames (#1866)', () => {
  it('opts in with nex=v1 next to the ticket', async () => {
    const view = await open()
    const u = new URL(sockets[0].url)
    expect(u.pathname).toBe('/ws/host-events')
    expect(u.searchParams.getAll('nex')).toEqual(['v1'])
    expect(u.searchParams.get('ticket')).toBe('tk')
    view.unmount()
  })

  it('routes the hello and a delta (cause and row passed through) to the list store', async () => {
    const view = await open()
    act(() => { sockets[0].emit(frame('nex.executions.hello', { epoch: 'E1', bseq: 0 })) })
    expect(onHello).toHaveBeenCalledWith(HOST, { epoch: 'E1', bseq: 0 })
    const row = { id: 'x', state: 'idle' }
    act(() => { sockets[0].emit(frame('nex.execution', { epoch: 'E1', bseq: 1, id: 'x', ver: 7, cause: ['execution.terminal'], row })) })
    expect(applyDelta).toHaveBeenCalledTimes(1)
    const [h, d] = applyDelta.mock.calls[0]
    expect(h).toBe(HOST)
    expect(d).toMatchObject({ epoch: 'E1', bseq: 1, id: 'x', ver: 7, cause: ['execution.terminal'] })
    expect(d.row).toMatchObject({ id: 'x', state: 'idle' })
    act(() => { sockets[0].emit(frame('nex.execution', { epoch: 'E1', bseq: 2, id: 'x', ver: 8, cause: [], row: null })) })
    expect(applyDelta.mock.calls[1][1].row).toBeNull()
    view.unmount()
  })

  it('ignores malformed values with a warning and never throws', async () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    const view = await open()
    act(() => {
      sockets[0].emit(frame('nex.executions.hello', 'not json'))
      sockets[0].emit(frame('nex.executions.hello', { epoch: '', bseq: 0 }))
      sockets[0].emit(frame('nex.executions.hello', { epoch: 'E', bseq: -1 }))
      sockets[0].emit(frame('nex.execution', { epoch: 'E', bseq: 1, id: '', ver: 1, cause: [], row: null }))
      sockets[0].emit(frame('nex.execution', { epoch: 'E', bseq: 1, id: 'x', ver: 1, cause: 'no', row: null }))
      sockets[0].emit(frame('nex.execution', { epoch: 'E', bseq: 1, id: 'x', ver: 1, cause: [], row: 5 }))
      sockets[0].emit(frame('nex.execution', { epoch: 'E', bseq: 1, id: 'x', ver: 1, cause: [], row: { state: 'idle' } }))
    })
    expect(onHello).not.toHaveBeenCalled()
    expect(applyDelta).not.toHaveBeenCalled()
    expect(warn).toHaveBeenCalledTimes(7)
    view.unmount()
  })
})

describe('gap and epoch handling through the stream (#1866 §4.4)', () => {
  const row = (id: string, state = 'idle') => ({ id, state, provider: 'claude', principal_id: 'p', cwd: '/w', mount_kind: 'dev', brief: 'b', labels: {}, created_at: 0, updated_at: 0, duration_ms: null, event_count: 0, observers: 0, archived: false })
  const page = (ids: string[]) => ({ items: ids.map((i) => row(i)), next_cursor: '', pdx: { epoch: 'E1', ver: 1, bseq: 0 } }) as never
  const delta = (bseq: number, id: string, ver: number, epoch = 'E1') => frame('nex.execution', { epoch, bseq, id, ver, cause: [], row: row(id) })
  const cache = () => useExecutionListStore.getState().byHost[HOST]
  const ids = () => cache().items.map((i) => i.id)
  let unsub: () => void

  beforeEach(async () => {
    useExecutionListStore.setState(original)
    resetExecutionListForTests()
    useNexHostStore.setState({ byHost: { [HOST]: { info: { configured: true, mounted: true, ready: true, init_error: '', effective: null }, capabilities: null, phase: 'ready', error: null, fetchedAt: 1, generation: 1, fingerprint: '1:1:' } } as never })
    vi.mocked(api.listExecutions).mockReset().mockResolvedValue(page(['a']))
  })
  afterEach(() => { unsub?.(); resetExecutionListForTests() })

  const ready = async () => {
    const view = await open()
    unsub = useExecutionListStore.getState().subscribe(HOST)
    act(() => { sockets[0].emit(frame('nex.executions.hello', { epoch: 'E1', bseq: 0 })) })
    await waitFor(() => expect(ids()).toEqual(['a']))
    return view
  }

  it('applies contiguous deltas, ignores an old epoch, and a bseq gap reconciles', async () => {
    const view = await ready()
    const calls = vi.mocked(api.listExecutions).mock.calls.length
    act(() => { sockets[0].emit(delta(1, 'b', 5)) })
    expect(ids()).toEqual(['a', 'b'])
    act(() => { sockets[0].emit(delta(2, 'z', 9, 'OTHER')) })
    expect(ids()).toEqual(['a', 'b'])
    expect(vi.mocked(api.listExecutions).mock.calls.length).toBe(calls)
    vi.mocked(api.listExecutions).mockResolvedValue(page(['a', 'b', 'c']))
    act(() => { sockets[0].emit(delta(5, 'c', 9)) }) // 2..4 missed
    await waitFor(() => expect(ids()).toEqual(['a', 'b', 'c']))
    expect(vi.mocked(api.listExecutions).mock.calls.length).toBe(calls + 1)
    view.unmount()
  })

  it('a delta before any hello changes nothing', async () => {
    const view = await open()
    unsub = useExecutionListStore.getState().subscribe(HOST)
    await waitFor(() => expect(api.listExecutions).toHaveBeenCalled())
    act(() => { sockets[0].emit(delta(1, 'b', 5)) })
    await waitFor(() => expect(ids()).toEqual(['a']))
    view.unmount()
  })
})
