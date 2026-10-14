// spa/src/hooks/useMultiHostEventWs.journal.test.ts — `journal.changed` reaches the store through the real hook, only marks
// stale, never requests, and is bound to its connection (J-2a T-2a.4). Harness as the workbook test.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { renderHook, act, waitFor } from '@testing-library/react'
import { useHostStore } from '../stores/useHostStore'
import { useSessionStore } from '../stores/useSessionStore'
import { isStale, selectUnit, useJournalStore } from '../stores/useJournalStore'
import { parseJournalPage } from '../lib/journal/parse'
import { wirePage } from '../lib/journal/fixtures'

vi.mock('../lib/host-connection', () => ({
  checkHealth: vi.fn(async () => ({ daemon: 'connected', latency: 3, ticket: 'tk' })),
}))
const getJournal = vi.fn()
vi.mock('../lib/journal/api', () => ({
  getJournal: (...a: unknown[]) => getJournal(...a),
  getTasks: vi.fn(),
  getRoadmap: vi.fn(),
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

const frame = (type: string, value: unknown) => JSON.stringify({ type, session: '', value: JSON.stringify(value) })
const changed = (what: string, unit: unknown = { kind: 'team', id: 'tm1' }) => frame('journal.changed', { unit, project: 'p', what })

beforeEach(async () => {
  sockets = []
  getJournal.mockReset()
  vi.stubGlobal('WebSocket', FakeSocket)
  useHostStore.setState({
    hosts: { [HOST]: { id: HOST, name: 'mlab', ip: '1.2.3.4', port: 7860, order: 0 } },
    hostOrder: [HOST], runtime: {}, activeHostId: HOST,
  })
  useSessionStore.setState({ fetchHost: vi.fn(async () => {}), replaceHost: vi.fn() } as never)
  useJournalStore.getState().reset()
  getJournal.mockResolvedValue({ kind: 'ok', value: parseJournalPage(wirePage()) })
  await useJournalStore.getState().loadJournal(HOST, 's1')
  getJournal.mockClear()
})
afterEach(() => {
  vi.unstubAllGlobals()
  useHostStore.getState().reset()
})

describe('useMultiHostEventWs journal events', () => {
  it('marks the named resource of the unit stale, 50 frames in a burst, with no request', async () => {
    const view = renderHook(() => useMultiHostEventWs())
    await waitFor(() => expect(sockets).toHaveLength(1))
    act(() => { for (let i = 0; i < 50; i++) sockets[0].emit(changed('tasks')) })
    const u = selectUnit(useJournalStore.getState(), HOST, 's1')!
    expect([isStale(u, 'entries'), isStale(u, 'tasks')]).toEqual([false, true])
    expect(getJournal).not.toHaveBeenCalled()
    view.unmount()
  })

  it('a malformed frame is dropped without throwing', async () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    const view = renderHook(() => useMultiHostEventWs())
    await waitFor(() => expect(sockets).toHaveLength(1))
    act(() => { sockets[0].emit(changed('everything')); sockets[0].emit(changed('entries', 'tm1')); sockets[0].emit(JSON.stringify({ type: 'journal.changed', session: '', value: 'not json' })) })
    const u = selectUnit(useJournalStore.getState(), HOST, 's1')!
    expect(isStale(u, 'entries')).toBe(false)
    warn.mockRestore()
    view.unmount()
  })

  it('a frame queued on the socket of a re-pointed host is dropped', async () => {
    const view = renderHook(() => useMultiHostEventWs())
    await waitFor(() => expect(sockets).toHaveLength(1))
    const old = sockets[0]
    act(() => {
      useHostStore.setState((s) => ({ hosts: { ...s.hosts, [HOST]: { ...s.hosts[HOST], ip: '5.6.7.8' } } }))
      old.emit(changed('entries'))
    })
    expect(isStale(selectUnit(useJournalStore.getState(), HOST, 's1')!, 'entries')).toBe(false)
    await waitFor(() => expect(sockets).toHaveLength(2))
    act(() => { sockets[1].emit(changed('entries')) })
    expect(isStale(selectUnit(useJournalStore.getState(), HOST, 's1')!, 'entries')).toBe(true)
    view.unmount()
  })
})
