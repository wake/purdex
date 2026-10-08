// spa/src/hooks/useMultiHostEventWs.unattended.test.ts — the `team.unattended` host event reaches the per-host store
// through the real hook (plan PU-2a). Harness as useMultiHostEventWs.approval.test.ts.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { renderHook, act, waitFor } from '@testing-library/react'
import { useHostStore } from '../stores/useHostStore'
import { useSessionStore } from '../stores/useSessionStore'
import { useUnattendedStore } from '../stores/useUnattendedStore'

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

const frame = (value: unknown) => JSON.stringify({ type: 'team.unattended', session: '', value: JSON.stringify(value) })

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
  useUnattendedStore.getState().reset()
})

afterEach(() => {
  vi.unstubAllGlobals()
  useHostStore.getState().reset()
})

describe('useMultiHostEventWs team.unattended', () => {
  it('the snapshot and a changed reach this host\'s entry in the store', async () => {
    const view = renderHook(() => useMultiHostEventWs())
    await waitFor(() => expect(sockets).toHaveLength(1))
    const ws = sockets[0]
    act(() => { ws.emit(frame({ op: 'snapshot', state: { on: false, since: 0, changed_at: 0 } })) })
    expect(useUnattendedStore.getState().byHost[HOST]).toEqual({ support: 'yes', state: { on: false, since: 0, changed_at: 0 } })
    const on = { on: true, since: 5_000, changed_at: 5_000, changed_by: { kind: 'app', label: 'Purdex.app @ air26', addr: '100.64.0.4:1' } }
    act(() => { ws.emit(frame({ op: 'changed', state: on })) })
    expect(useUnattendedStore.getState().byHost[HOST]?.state).toEqual(on)
    view.unmount()
  })
})
