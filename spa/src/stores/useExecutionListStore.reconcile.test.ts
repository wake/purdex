// spa/src/stores/useExecutionListStore.reconcile.test.ts — #1866 PR2b: the SPA 120 s safety reconcile (§4.5, §8 R3-1)
// and the "daemon seed absorbed a silent transition" correction (F2).
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { resetExecutionListForTests, startExecutionListInvalidation, useExecutionListStore } from './useExecutionListStore'
import { startNexHostInvalidation, useNexHostStore, type NexHostEntry } from './useNexHostStore'
import { useHostStore } from './useHostStore'
import { subscriptionSlots } from '../lib/nex/subscription-slots'
import type { ExecutionSummary } from '../lib/nex/types'
import type { NexDelta } from '../lib/nex/execution-list-effects'
import * as api from '../lib/nex/nex-api'
import * as sse from '../lib/nex/nex-sse'

vi.mock('../lib/nex/nex-api', () => ({ listExecutions: vi.fn() }))
vi.mock('../lib/nex/nex-sse', () => ({ openNexSse: vi.fn() }))

const A = 'host-a'
const E1 = 'E1'
const SAFETY_MS = 120_000
const GRACE_MS = 1_500
const row = (id: string, extra: Partial<ExecutionSummary> = {}): ExecutionSummary =>
  ({ id, state: 'idle', provider: 'claude', principal_id: 'p', cwd: '/w', mount_kind: 'dev', brief: 'b', labels: {}, created_at: 0, updated_at: 0, duration_ms: null, event_count: 0, observers: 0, archived: false, ...extra }) as ExecutionSummary
const page = (ver: number, rows: ExecutionSummary[], bseq = 0, epoch = E1) =>
  ({ items: rows, next_cursor: '', pdx: { epoch, ver, bseq } }) as never
const nexEntry = (): NexHostEntry =>
  ({ info: { configured: true, mounted: true, ready: true, init_error: '', effective: null }, capabilities: null, phase: 'ready', error: null, fetchedAt: 1, generation: 1, fingerprint: '1:1:' })

const store = () => useExecutionListStore.getState()
const cache = () => store().byHost[A]
const mismatches = () => cache().spaMismatchTotal ?? 0
const stateOf = (id: string) => cache().items.find((i) => i.id === id)?.state
const delta = (bseq: number, id: string, ver: number, r: ExecutionSummary | null): NexDelta => ({ epoch: E1, bseq, id, ver, cause: [], row: r })
const advance = (ms: number) => vi.advanceTimersByTimeAsync(ms)
const listCalls = () => vi.mocked(api.listExecutions).mock.calls.length
const setVisibility = (v: 'visible' | 'hidden') => Object.defineProperty(document, 'visibilityState', { value: v, configurable: true })

let stop: () => void
let warn: ReturnType<typeof vi.spyOn>
beforeEach(() => {
  vi.useFakeTimers()
  setVisibility('visible')
  subscriptionSlots.resetForTests()
  resetExecutionListForTests()
  useExecutionListStore.setState({ byHost: {} })
  useNexHostStore.setState({ byHost: { [A]: nexEntry() } })
  useHostStore.setState({ hosts: { [A]: { id: A, name: 'A', ip: '1', port: 1, token: 't', order: 0 } }, hostOrder: [A], activeHostId: A, runtime: {} })
  vi.mocked(sse.openNexSse).mockReset().mockImplementation(() => ({ close: vi.fn() }))
  vi.mocked(api.listExecutions).mockReset().mockResolvedValue(page(5, [row('a', { state: 'running' })]))
  warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
  const a = startNexHostInvalidation(); const b = startExecutionListInvalidation()
  stop = () => { a(); b() }
})
afterEach(() => { stop(); vi.restoreAllMocks(); vi.useRealTimers() })

/** A delta host whose cache holds `a` running@5, as after a client list read. */
const ready = async () => {
  store().onHello(A, { epoch: E1, bseq: 0 })
  store().subscribe(A)
  await advance(0)
  expect(stateOf('a')).toBe('running')
  expect(cache().rowVers).toEqual({ a: 5 })
}

describe('F2: the daemon seed absorbed a silent transition; the next reconcile corrects the cache by ver', () => {
  it('(a) a hello-triggered reconcile', async () => {
    await ready()
    vi.mocked(api.listExecutions).mockResolvedValue(page(9, [row('a', { state: 'idle' })])) // idle@9, and no delta was ever received
    store().onHello(A, { epoch: E1, bseq: 0 })
    await advance(0)
    expect(stateOf('a')).toBe('idle')
    expect(cache().rowVers).toEqual({ a: 9 })
  })

  it('(b) the 120 s safety reconcile corrects the cache, and the mismatch is counted after the grace', async () => {
    await ready()
    vi.mocked(api.listExecutions).mockResolvedValue(page(9, [row('a', { state: 'idle' })]))
    await advance(SAFETY_MS)
    expect(stateOf('a')).toBe('idle')
    expect(cache().rowVers).toEqual({ a: 9 })
    expect(mismatches()).toBe(0) // still inside the grace
    await advance(GRACE_MS - 1)
    expect(mismatches()).toBe(0)
    await advance(1)
    expect(mismatches()).toBe(1)
    expect(warn).toHaveBeenCalledWith('nex-delta: spa mismatch', expect.objectContaining({ hostId: A, id: 'a', field: 'state', cached: 'running', fetched: 'idle', total: 1 }))
  })
})

describe('the safety reconcile runs only while visible, subscribed and in delta mode', () => {
  it('runs every 120 s', async () => {
    await ready()
    const n = listCalls()
    await advance(SAFETY_MS)
    expect(listCalls()).toBe(n + 1)
    await advance(SAFETY_MS)
    expect(listCalls()).toBe(n + 2)
  })

  it('a hidden document skips the tick', async () => {
    await ready()
    const n = listCalls()
    setVisibility('hidden')
    await advance(SAFETY_MS)
    expect(listCalls()).toBe(n)
    setVisibility('visible')
    await advance(SAFETY_MS)
    expect(listCalls()).toBe(n + 1)
  })

  it('a legacy (unknown) host never schedules one', async () => {
    store().subscribe(A)
    await advance(0)
    const n = listCalls()
    await advance(SAFETY_MS * 3)
    expect(listCalls()).toBe(n)
  })

  it('a late hello (subscribed first, legacy) starts it', async () => {
    store().subscribe(A)
    await advance(0)
    store().onHello(A, { epoch: E1, bseq: 0 })
    await advance(0)
    const n = listCalls()
    await advance(SAFETY_MS)
    expect(listCalls()).toBe(n + 1)
  })

  it('skips a tick while a walk is still in flight', async () => {
    await ready()
    vi.mocked(api.listExecutions).mockReset().mockImplementation(() => new Promise(() => {}))
    await advance(SAFETY_MS)
    const n = listCalls()
    expect(n).toBe(1)
    await advance(SAFETY_MS)
    expect(listCalls()).toBe(n)
  })

  it('a hello with nobody subscribed schedules nothing', async () => {
    store().onHello(A, { epoch: E1, bseq: 0 })
    expect(vi.getTimerCount()).toBe(0)
  })
})

describe('suspects', () => {
  it('a delta with ver > V inside the grace means the change was in flight: benign', async () => {
    await ready()
    vi.mocked(api.listExecutions).mockResolvedValue(page(9, [row('a', { state: 'idle' })]))
    await advance(SAFETY_MS)
    store().applyDelta(A, delta(1, 'a', 10, row('a', { state: 'running' })))
    await advance(GRACE_MS + 10)
    expect(mismatches()).toBe(0)
  })

  it('R3-1: a delta with ver <= V and the listed digest, delivered late, is observed and benign', async () => {
    await ready()
    // The page was read after delta 1 was enqueued (H = 1) but the socket delivers it only now.
    vi.mocked(api.listExecutions).mockResolvedValue(page(9, [row('a', { state: 'idle' })], 1))
    await advance(SAFETY_MS)
    await advance(SAFETY_MS - 1) // the suspect stays pending while lastBseq < H, however long that takes
    expect(mismatches()).toBe(0)
    store().applyDelta(A, delta(1, 'a', 8, row('a', { state: 'idle' }))) // D <= V: not applied, but it carries the listed state
    await advance(GRACE_MS + 10)
    expect(mismatches()).toBe(0)
  })

  it('R3-1: with H not yet reached the suspect waits; once reached and no matching delta arrives it is a mismatch', async () => {
    await ready()
    vi.mocked(api.listExecutions).mockResolvedValue(page(9, [row('a', { state: 'idle' })], 2))
    await advance(SAFETY_MS)
    await advance(10_000)
    expect(mismatches()).toBe(0) // lastBseq 0 < H 2
    store().applyDelta(A, delta(1, 'b', 3, row('b')))
    await advance(GRACE_MS + 10)
    expect(mismatches()).toBe(0) // lastBseq 1 < H 2
    store().applyDelta(A, delta(2, 'c', 4, row('c')))
    await advance(GRACE_MS - 1)
    expect(mismatches()).toBe(0)
    await advance(1)
    expect(mismatches()).toBe(1)
  })

  it('a row only on one side is a suspect; the commit repairs it', async () => {
    await ready()
    vi.mocked(api.listExecutions).mockResolvedValue(page(9, [row('a', { state: 'running' }), row('n')]))
    await advance(SAFETY_MS)
    expect(cache().items.map((i) => i.id)).toEqual(['a', 'n'])
    await advance(GRACE_MS)
    expect(mismatches()).toBe(1)
    vi.mocked(api.listExecutions).mockResolvedValue(page(12, [row('a', { state: 'running' })]))
    await advance(SAFETY_MS)
    expect(cache().items.map((i) => i.id)).toEqual(['a'])
    await advance(GRACE_MS)
    expect(mismatches()).toBe(2)
  })

  it('a clean reconcile counts nothing', async () => {
    await ready()
    vi.mocked(api.listExecutions).mockResolvedValue(page(9, [row('a', { state: 'running' })]))
    await advance(SAFETY_MS + GRACE_MS * 2)
    expect(mismatches()).toBe(0)
    expect(warn).not.toHaveBeenCalledWith('nex-delta: spa mismatch', expect.anything())
  })

  it('a bseq gap cancels the pending suspects (the reconcile that follows re-evaluates)', async () => {
    await ready()
    vi.mocked(api.listExecutions).mockResolvedValue(page(9, [row('a', { state: 'idle' })]))
    await advance(SAFETY_MS)
    store().applyDelta(A, delta(7, 'x', 11, row('x'))) // gap
    await advance(GRACE_MS * 3)
    expect(mismatches()).toBe(0)
  })

  it('a hello cancels the pending suspects', async () => {
    await ready()
    vi.mocked(api.listExecutions).mockResolvedValue(page(9, [row('a', { state: 'idle' })], 4))
    await advance(SAFETY_MS)
    store().onHello(A, { epoch: 'E2', bseq: 6 })
    await advance(0)
    store().applyDelta(A, { epoch: 'E2', bseq: 7, id: 'q', ver: 30, cause: [], row: row('q') }) // would arm the old suspect (6 >= H 4)
    await advance(GRACE_MS * 3)
    expect(mismatches()).toBe(0)
  })
})

describe('timers are cleaned up', () => {
  it('clearHost stops the reconcile and drops pending suspects (no mismatch is ever counted)', async () => {
    await ready()
    vi.mocked(api.listExecutions).mockResolvedValue(page(9, [row('a', { state: 'idle' })]))
    await advance(SAFETY_MS) // one suspect is now inside its grace
    store().clearHost(A)
    expect(vi.getTimerCount()).toBe(0)
    const n = listCalls()
    await advance(SAFETY_MS * 2)
    expect(listCalls()).toBe(n)
    expect(store().byHost[A]).toBeUndefined()
  })

  it('the last unsubscribe drops pending suspects too', async () => {
    store().onHello(A, { epoch: E1, bseq: 0 })
    const unsub = store().subscribe(A)
    await advance(0)
    vi.mocked(api.listExecutions).mockResolvedValue(page(9, [row('a', { state: 'idle' })]))
    await advance(SAFETY_MS)
    unsub()
    expect(vi.getTimerCount()).toBe(0)
    await advance(GRACE_MS * 2)
    expect(mismatches()).toBe(0)
  })

  it('the last unsubscribe leaves no timer', async () => {
    store().onHello(A, { epoch: E1, bseq: 0 })
    const unsub = store().subscribe(A)
    await advance(0)
    expect(vi.getTimerCount()).toBeGreaterThan(0)
    unsub()
    expect(vi.getTimerCount()).toBe(0)
  })

  it('resetting the runtimes leaves no timer behind', async () => {
    await ready()
    expect(vi.getTimerCount()).toBeGreaterThan(0)
    resetExecutionListForTests()
    expect(vi.getTimerCount()).toBe(0)
  })

  it('a fingerprint change (mode back to unknown) stops it', async () => {
    await ready()
    useHostStore.setState({ hosts: { [A]: { id: A, name: 'A', ip: '9', port: 9, token: 't', order: 0 } } })
    await advance(0)
    const n = listCalls()
    await advance(SAFETY_MS * 2)
    expect(listCalls()).toBe(n)
  })

  it('a readiness flap re-arms it exactly once', async () => {
    await ready()
    useNexHostStore.setState({ byHost: { [A]: { ...nexEntry(), info: { ...nexEntry().info!, ready: false } } } })
    useNexHostStore.setState({ byHost: { [A]: nexEntry() } })
    await advance(0)
    const n = listCalls()
    await advance(SAFETY_MS)
    expect(listCalls()).toBe(n + 1)
  })
})
