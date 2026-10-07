// spa/src/lib/nex/worker-title-prefetch.test.ts — #1557: after a reload, an unopened worker tab whose execution has
// neither a live summary nor a list row (an archived worker: not on the host's default list page) is titled from
// one fetched summary instead of the pane label 「執行」.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { renderHook, act } from '@testing-library/react'

vi.mock('./nex-api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('./nex-api')>()),
  getExecution: vi.fn(),
}))

import { getExecution } from './nex-api'
import { resetWorkerTitlePrefetchForTests, startWorkerTitlePrefetch } from './worker-title-prefetch'
import { emptyListCache, type HostListPhase } from './execution-list-effects'
import { defaultExecutionState } from './event-reducer'
import { NexApiError, type ExecutionSummary } from './types'
import { useTabDisplay } from '../../hooks/useTabDisplay'
import { useWorkerTitlePrefetchStore } from '../../stores/useWorkerTitlePrefetchStore'
import { useExecutionStore, executionKey } from '../../stores/useExecutionStore'
import { useExecutionListStore } from '../../stores/useExecutionListStore'
import { useNexHostStore } from '../../stores/useNexHostStore'
import { useHostStore } from '../../stores/useHostStore'
import { useTabStore } from '../../stores/useTabStore'
import { useI18nStore } from '../../stores/useI18nStore'
import type { Tab } from '../../types/tab'

const getExecutionMock = vi.mocked(getExecution)

const H = 'h1'
const H2 = 'h2'

const summary = (id: string, over: Partial<ExecutionSummary> = {}): ExecutionSummary =>
  ({ id, state: 'terminated', provider: 'claude', principal_id: 'p', cwd: '/w/repo', mount_kind: 'dev', brief: '',
    labels: {}, created_at: 1, updated_at: 5, duration_ms: null, event_count: 0, observers: 0, archived: true, ...over }) as ExecutionSummary

const execTab = (id: string, executionId: string, host = H): Tab => ({
  id, pinned: false, locked: false, createdAt: 0,
  layout: { type: 'leaf', pane: { id: `p-${id}`, content: { kind: 'execution', executionId, host } } },
})

const openTabs = (...tabs: Tab[]) =>
  act(() => { useTabStore.setState({ tabs: Object.fromEntries(tabs.map((t) => [t.id, t])), tabOrder: tabs.map((t) => t.id), activeTabId: null }) })

const hostConfig = (id: string, ip = '10.0.0.1') => ({ id, name: id, ip, port: 7860, order: 0 })

const setHosts = (...configs: ReturnType<typeof hostConfig>[]) =>
  act(() => {
    useHostStore.setState({ hosts: Object.fromEntries(configs.map((c) => [c.id, c])) as never, hostOrder: configs.map((c) => c.id) })
  })

const nexReady = (hostId: string, ready = true) =>
  act(() => {
    useNexHostStore.setState((s) => ({
      byHost: {
        ...s.byHost,
        [hostId]: {
          info: null, error: null, fetchedAt: 0, generation: 0, fingerprint: '',
          phase: ready ? 'ready' : 'loading',
          capabilities: { session_title: { sources: ['ai'], max_bytes: 200 } } as never,
        },
      },
    }))
  })

const listAnswered = (hostId: string, items: ExecutionSummary[] = [], phase: HostListPhase = 'ready') =>
  act(() => { useExecutionListStore.setState((s) => ({ byHost: { ...s.byHost, [hostId]: { ...emptyListCache(), phase, items } } })) })

const setLive = (hostId: string, executionId: string, s: ExecutionSummary) =>
  act(() => {
    useExecutionStore.setState((st) => ({
      executions: { ...st.executions, [executionKey(hostId, executionId)]: { ...defaultExecutionState(), summary: s } },
    }))
  })

const flush = () => act(async () => { await new Promise((r) => setTimeout(r, 0)) })

interface Deferred<T> { promise: Promise<T>; resolve: (v: T) => void; reject: (e: unknown) => void }
function deferred<T>(): Deferred<T> {
  let resolve!: (v: T) => void
  let reject!: (e: unknown) => void
  const promise = new Promise<T>((res, rej) => { resolve = res; reject = rej })
  return { promise, resolve, reject }
}

const stops: (() => void)[] = []
const start = () => { stops.push(startWorkerTitlePrefetch()) }

beforeEach(() => {
  resetWorkerTitlePrefetchForTests()
  getExecutionMock.mockReset()
  // A stray call answers like an unknown execution; each test that expects a call sets its own answer.
  getExecutionMock.mockRejectedValue(new NexApiError(404, 'execution_not_found', 'not found'))
  useHostStore.setState({ hosts: { [H]: hostConfig(H), [H2]: hostConfig(H2) } as never, hostOrder: [H, H2] })
  useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null })
  useExecutionStore.setState({ executions: {} })
  useExecutionListStore.setState({ byHost: {} })
  useNexHostStore.setState({ byHost: {} })
  useWorkerTitlePrefetchStore.setState({ byKey: {} })
  useI18nStore.setState({ t: (k: string) => k })
})

afterEach(() => {
  for (const stop of stops.splice(0)) stop()
})

describe('worker title prefetch (#1557)', () => {
  it('an unopened worker with neither a live summary nor a list row is titled from one fetched summary', async () => {
    nexReady(H)
    listAnswered(H)
    const tab = execTab('t1', 'e1')
    openTabs(tab)
    getExecutionMock.mockResolvedValue(summary('e1', { session_title: { text: 'Fix login', source: 'ai' } }))
    const { result } = renderHook(() => useTabDisplay(tab))
    expect(result.current.displayTitle).toBe('page.pane.execution')

    start()
    await flush()

    expect(getExecutionMock).toHaveBeenCalledTimes(1)
    expect(getExecutionMock).toHaveBeenCalledWith(H, 'e1')
    expect(result.current.displayTitle).toBe('Fix login - repo')
  })

  it.each([
    ['a list row', () => listAnswered(H, [summary('e1', { archived: false, state: 'idle' })])],
    ['a live summary', () => { listAnswered(H); setLive(H, 'e1', summary('e1')) }],
  ])('a worker with %s is not fetched', async (_, arrange) => {
    nexReady(H)
    arrange()
    openTabs(execTab('t1', 'e1'))
    start()
    await flush()
    expect(getExecutionMock).not.toHaveBeenCalled()
  })

  it('fetches each (host, execution) once, however often the stores change or however many tabs show it', async () => {
    nexReady(H)
    listAnswered(H)
    getExecutionMock.mockResolvedValue(summary('e1', { brief: 'Brief' }))
    openTabs(execTab('t1', 'e1'), execTab('t2', 'e1'))
    start()
    await flush()
    openTabs(execTab('t1', 'e1'), execTab('t2', 'e1'), execTab('t3', 'e1'))
    listAnswered(H)
    nexReady(H)
    await flush()
    expect(getExecutionMock).toHaveBeenCalledTimes(1)
  })

  it.each([
    ['404', new NexApiError(404, 'execution_not_found', 'not found')],
    ['network failure', new NexApiError(0, 'network', 'offline')],
  ])('a %s keeps the fallback label and is not retried', async (_, err) => {
    nexReady(H)
    listAnswered(H)
    getExecutionMock.mockRejectedValue(err)
    const tab = execTab('t1', 'e1')
    openTabs(tab)
    const { result } = renderHook(() => useTabDisplay(tab))
    start()
    await flush()
    for (let i = 0; i < 5; i++) {
      listAnswered(H)
      openTabs(tab)
      nexReady(H)
      await flush()
    }
    expect(getExecutionMock).toHaveBeenCalledTimes(1)
    expect(result.current.displayTitle).toBe('page.pane.execution')
    expect(useWorkerTitlePrefetchStore.getState().byKey).toEqual({})
  })

  it.each([
    ['the host has no Nexen entry', () => listAnswered(H)],
    ['Nexen is not ready', () => { nexReady(H, false); listAnswered(H) }],
    ['the list has not answered yet', () => { nexReady(H); listAnswered(H, [], 'loading') }],
    ['the list failed', () => { nexReady(H); listAnswered(H, [], 'error') }],
    ['the host is not configured', () => {
      nexReady(H)
      listAnswered(H)
      act(() => { useHostStore.setState({ hosts: { [H2]: hostConfig(H2) } as never, hostOrder: [H2] }) })
    }],
  ])('no fetch while %s', async (_, arrange) => {
    arrange()
    openTabs(execTab('t1', 'e1'))
    start()
    await flush()
    expect(getExecutionMock).not.toHaveBeenCalled()
  })

  it('fetches once the host is ready: Nexen first, then the list answering', async () => {
    getExecutionMock.mockResolvedValue(summary('e1', { brief: 'Brief' }))
    openTabs(execTab('t1', 'e1'))
    start()
    await flush()
    nexReady(H)
    await flush()
    expect(getExecutionMock).not.toHaveBeenCalled()
    listAnswered(H)
    await flush()
    expect(getExecutionMock).toHaveBeenCalledTimes(1)
    expect(useWorkerTitlePrefetchStore.getState().byKey[executionKey(H, 'e1')]?.brief).toBe('Brief')
  })

  it('a worker whose row leaves the list (archived elsewhere) is fetched then', async () => {
    nexReady(H)
    listAnswered(H, [summary('e1', { archived: false, state: 'idle' })])
    getExecutionMock.mockResolvedValue(summary('e1', { brief: 'Brief' }))
    openTabs(execTab('t1', 'e1'))
    start()
    await flush()
    expect(getExecutionMock).not.toHaveBeenCalled()
    listAnswered(H, [])
    await flush()
    expect(getExecutionMock).toHaveBeenCalledTimes(1)
  })

  it('an answer landing after a live summary turned up is dropped: the live title stays', async () => {
    nexReady(H)
    listAnswered(H)
    const d = deferred<ExecutionSummary>()
    getExecutionMock.mockReturnValue(d.promise)
    const tab = execTab('t1', 'e1')
    openTabs(tab)
    const { result } = renderHook(() => useTabDisplay(tab))
    start()
    await flush()
    setLive(H, 'e1', summary('e1', { session_title: { text: 'Live', source: 'ai' } }))
    d.resolve(summary('e1', { session_title: { text: 'Stale', source: 'ai' } }))
    await flush()
    expect(result.current.displayTitle).toBe('Live - repo')
    expect(useWorkerTitlePrefetchStore.getState().byKey).toEqual({})
  })

  it('one request in flight per host: the next worker of that host waits, after a success or a failure', async () => {
    nexReady(H)
    nexReady(H2)
    listAnswered(H)
    listAnswered(H2)
    const pending = new Map<string, Deferred<ExecutionSummary>>()
    getExecutionMock.mockImplementation((hostId, id) => {
      const d = deferred<ExecutionSummary>()
      pending.set(`${hostId}/${id}`, d)
      return d.promise
    })
    openTabs(execTab('t1', 'e1'), execTab('t2', 'e2'), execTab('t3', 'e3'), execTab('t4', 'e1', H2))
    start()
    await flush()
    expect(getExecutionMock.mock.calls).toEqual([[H, 'e1'], [H2, 'e1']])

    pending.get(`${H}/e1`)!.resolve(summary('e1'))
    await flush()
    expect(getExecutionMock.mock.calls).toEqual([[H, 'e1'], [H2, 'e1'], [H, 'e2']])

    pending.get(`${H}/e2`)!.reject(new NexApiError(0, 'network', 'offline'))
    await flush()
    expect(getExecutionMock.mock.calls).toEqual([[H, 'e1'], [H2, 'e1'], [H, 'e2'], [H, 'e3']])
  })

  it('an answer for a host removed meanwhile is dropped', async () => {
    nexReady(H)
    listAnswered(H)
    const d = deferred<ExecutionSummary>()
    getExecutionMock.mockReturnValue(d.promise)
    openTabs(execTab('t1', 'e1'))
    start()
    await flush()
    act(() => { useHostStore.setState({ hosts: { [H2]: hostConfig(H2) } as never, hostOrder: [H2] }) })
    d.resolve(summary('e1', { brief: 'Brief' }))
    await flush()
    expect(useWorkerTitlePrefetchStore.getState().byKey).toEqual({})
  })

  it('re-pointing a host drops its titles, and its new daemon is asked again', async () => {
    nexReady(H)
    listAnswered(H)
    getExecutionMock.mockResolvedValueOnce(summary('e1', { brief: 'Old daemon' }))
    getExecutionMock.mockResolvedValueOnce(summary('e1', { brief: 'New daemon' }))
    openTabs(execTab('t1', 'e1'))
    start()
    await flush()
    expect(useWorkerTitlePrefetchStore.getState().byKey[executionKey(H, 'e1')]?.brief).toBe('Old daemon')

    act(() => { useHostStore.setState({ hosts: { [H]: hostConfig(H, '10.0.0.9'), [H2]: hostConfig(H2) } as never }) })
    expect(useWorkerTitlePrefetchStore.getState().byKey).toEqual({})
    await flush()
    expect(getExecutionMock).toHaveBeenCalledTimes(2)
    expect(useWorkerTitlePrefetchStore.getState().byKey[executionKey(H, 'e1')]?.brief).toBe('New daemon')
  })

  // A1: the same id, ip, port and token after the host was forgotten — the fingerprint alone cannot tell the
  // incarnations apart, so the answer of the old one must not be written as the new one's.
  it.each([
    ['removed and re-added', () => { setHosts(hostConfig(H2)); setHosts(hostConfig(H), hostConfig(H2)) }, 2],
    ['re-pointed and pointed back', () => {
      setHosts(hostConfig(H, '10.0.0.9'), hostConfig(H2))
      setHosts(hostConfig(H), hostConfig(H2))
    }, 3],
  ])('a host %s with the same endpoint: the old request\'s answer is dropped, the current one is asked and kept', async (_, arrange, calls) => {
    nexReady(H)
    listAnswered(H)
    const pending: Deferred<ExecutionSummary>[] = []
    getExecutionMock.mockImplementation(() => {
      const d = deferred<ExecutionSummary>()
      pending.push(d)
      return d.promise
    })
    const tab = execTab('t1', 'e1')
    openTabs(tab)
    const { result } = renderHook(() => useTabDisplay(tab))
    start()
    await flush()
    expect(getExecutionMock).toHaveBeenCalledTimes(1)

    arrange()
    await flush()
    // The forgotten incarnation's request is still out, yet the current one is asked.
    expect(getExecutionMock).toHaveBeenCalledTimes(calls)

    for (const d of pending.slice(0, -1)) d.resolve(summary('e1', { session_title: { text: 'Old', source: 'ai' } }))
    await flush()
    expect(useWorkerTitlePrefetchStore.getState().byKey).toEqual({})
    expect(result.current.displayTitle).toBe('page.pane.execution')
    expect(getExecutionMock).toHaveBeenCalledTimes(calls)

    pending[pending.length - 1].resolve(summary('e1', { session_title: { text: 'Current', source: 'ai' } }))
    await flush()
    expect(result.current.displayTitle).toBe('Current - repo')
  })

  // A2: hostFetch has no timeout, so a request to the forgotten incarnation may never settle.
  it('a request to a forgotten host that never settles does not hold back the host re-added under its id', async () => {
    nexReady(H)
    listAnswered(H)
    getExecutionMock.mockReturnValueOnce(new Promise<ExecutionSummary>(() => {}))
    getExecutionMock.mockResolvedValueOnce(summary('e1', { brief: 'Brief' }))
    openTabs(execTab('t1', 'e1'))
    start()
    await flush()
    setHosts(hostConfig(H2))
    setHosts(hostConfig(H), hostConfig(H2))
    await flush()
    expect(getExecutionMock).toHaveBeenCalledTimes(2)
    expect(useWorkerTitlePrefetchStore.getState().byKey[executionKey(H, 'e1')]?.brief).toBe('Brief')
  })

  it('the forgotten request settling late does not free the host while the current request is in flight', async () => {
    nexReady(H)
    listAnswered(H)
    const pending: Deferred<ExecutionSummary>[] = []
    getExecutionMock.mockImplementation(() => {
      const d = deferred<ExecutionSummary>()
      pending.push(d)
      return d.promise
    })
    openTabs(execTab('t1', 'e1'), execTab('t2', 'e2'))
    start()
    await flush()
    expect(getExecutionMock.mock.calls).toEqual([[H, 'e1']])

    setHosts(hostConfig(H2))
    setHosts(hostConfig(H), hostConfig(H2))
    await flush()
    expect(getExecutionMock.mock.calls).toEqual([[H, 'e1'], [H, 'e1']])

    pending[0].reject(new NexApiError(0, 'network', 'offline'))
    await flush()
    // Still one request in flight for the host: e2 waits for the current one.
    expect(getExecutionMock.mock.calls).toEqual([[H, 'e1'], [H, 'e1']])

    pending[1].resolve(summary('e1'))
    await flush()
    expect(getExecutionMock.mock.calls).toEqual([[H, 'e1'], [H, 'e1'], [H, 'e2']])
  })

  // A3: the row that won over the answer can leave again — the worker archived since, which is #1557 itself.
  it('an answer dropped for a list row is asked again once that row leaves the list (archived)', async () => {
    nexReady(H)
    listAnswered(H)
    const d = deferred<ExecutionSummary>()
    getExecutionMock.mockReturnValueOnce(d.promise)
    getExecutionMock.mockResolvedValueOnce(summary('e1', { session_title: { text: 'Archived', source: 'ai' } }))
    const tab = execTab('t1', 'e1')
    openTabs(tab)
    const { result } = renderHook(() => useTabDisplay(tab))
    start()
    await flush()
    expect(getExecutionMock).toHaveBeenCalledTimes(1)

    listAnswered(H, [summary('e1', { archived: false, state: 'idle', session_title: { text: 'Row', source: 'ai' } })])
    await flush()
    expect(result.current.displayTitle).toBe('Row - repo')

    d.resolve(summary('e1', { session_title: { text: 'Stale', source: 'ai' } }))
    await flush()
    expect(useWorkerTitlePrefetchStore.getState().byKey).toEqual({})
    expect(getExecutionMock).toHaveBeenCalledTimes(1)

    listAnswered(H, [])
    await flush()
    expect(getExecutionMock).toHaveBeenCalledTimes(2)
    expect(useWorkerTitlePrefetchStore.getState().byKey[executionKey(H, 'e1')]?.session_title?.text).toBe('Archived')
    expect(result.current.displayTitle).toBe('Archived - repo')
  })

  it('stopped: no more fetches', async () => {
    nexReady(H)
    listAnswered(H)
    start()
    for (const stop of stops.splice(0)) stop()
    openTabs(execTab('t1', 'e1'))
    await flush()
    expect(getExecutionMock).not.toHaveBeenCalled()
  })
})
