// spa/src/hooks/useMultiHostEventWs.fleet.test.ts — the `fleet.changed` host event reaches the fleet store through the real
// hook (fleet plan T-2a.2). Harness as useMultiHostEventWs.relay-quota.test.ts.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { renderHook, act, waitFor } from '@testing-library/react'
import { useHostStore } from '../stores/useHostStore'
import { useSessionStore } from '../stores/useSessionStore'
import { useFleetStore } from '../lib/fleet/store'

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

const nums = { cpu: 1, mem: 2, disk: 3, load1: 0, ncpu: 4, zone: 'normal', trend: 'flat', sampled_at: 1 }
const body = (cpu: number) => ({ self: { ...nums, cpu }, probes: [] })
const frame = (value: unknown) => JSON.stringify({ type: 'fleet.changed', session: '', value: JSON.stringify(value) })
const entry = () => useFleetStore.getState().byHost[HOST]

beforeEach(() => {
  sockets = []
  vi.stubGlobal('WebSocket', FakeSocket)
  useHostStore.setState({
    hosts: { [HOST]: { id: HOST, name: 'mlab', ip: '1.2.3.4', port: 7860, order: 0 } },
    hostOrder: [HOST], runtime: {}, activeHostId: HOST,
  })
  useSessionStore.setState({ fetchHost: vi.fn(async () => {}), replaceHost: vi.fn() } as never)
  useFleetStore.getState().reset()
})
afterEach(() => { vi.unstubAllGlobals(); useHostStore.getState().reset() })

describe('useMultiHostEventWs fleet.changed', () => {
  it('a frame replaces the host picture once fleet.v1 is confirmed, and never before', async () => {
    const view = renderHook(() => useMultiHostEventWs())
    await waitFor(() => expect(sockets).toHaveLength(1))
    act(() => { sockets[0].emit(frame(body(5))) })
    expect(entry()?.self ?? null).toBeNull()
    const gen = useFleetStore.getState().begin(HOST)
    useFleetStore.getState().setSupport(HOST, gen, 'yes')
    act(() => { sockets[0].emit(frame(body(6))) })
    expect(entry().self?.cpu).toBe(6)
    view.unmount()
  })

  it('a malformed frame is dropped', async () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    const view = renderHook(() => useMultiHostEventWs())
    await waitFor(() => expect(sockets).toHaveLength(1))
    const gen = useFleetStore.getState().begin(HOST)
    useFleetStore.getState().setSupport(HOST, gen, 'yes')
    act(() => { sockets[0].emit(frame({ nope: 1 })) })
    expect(entry().self).toBeNull()
    expect(warn).toHaveBeenCalled()
    warn.mockRestore()
    view.unmount()
  })

  it('after a re-point a frame queued on the old socket does not write', async () => {
    const view = renderHook(() => useMultiHostEventWs())
    await waitFor(() => expect(sockets).toHaveLength(1))
    const old = sockets[0]
    const gen = useFleetStore.getState().begin(HOST)
    useFleetStore.getState().setSupport(HOST, gen, 'yes')
    act(() => {
      useHostStore.setState((s) => ({ hosts: { ...s.hosts, [HOST]: { ...s.hosts[HOST], ip: '5.6.7.8' } } }))
      old.emit(frame(body(9)))
    })
    expect(entry().self).toBeNull()
    view.unmount()
  })
})
