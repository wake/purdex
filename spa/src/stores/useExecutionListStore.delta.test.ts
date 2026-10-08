// spa/src/stores/useExecutionListStore.delta.test.ts — #1866 PR2a: the per-host capability state machine, versioned
// walks and the overlay, driven through the store. Nothing in production calls onHello / applyDelta yet (PR2b).
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { resetExecutionListForTests, startExecutionListInvalidation, useExecutionListStore } from './useExecutionListStore'
import { startNexHostInvalidation, useNexHostStore, type NexHostEntry } from './useNexHostStore'
import { useHostStore } from './useHostStore'
import { subscriptionSlots, capFor } from '../lib/nex/subscription-slots'
import { NexApiError, type ExecutionSummary } from '../lib/nex/types'
import type { NexDelta } from '../lib/nex/execution-list-effects'
import * as api from '../lib/nex/nex-api'
import * as sse from '../lib/nex/nex-sse'

vi.mock('../lib/nex/nex-api', () => ({ listExecutions: vi.fn() }))
vi.mock('../lib/nex/nex-sse', () => ({ openNexSse: vi.fn() }))

const A = 'host-a'
const E1 = 'E1'
const row = (id: string, extra: Partial<ExecutionSummary> = {}): ExecutionSummary =>
  ({ id, state: 'idle', provider: 'claude', principal_id: 'p', cwd: '/w', mount_kind: 'dev', brief: 'b', labels: {}, created_at: 0, updated_at: 0, duration_ms: null, event_count: 0, observers: 0, archived: false, ...extra }) as ExecutionSummary
const stamped = (ver: number, ids: string[], next = '', epoch = E1) =>
  ({ items: ids.map((i) => row(i)), next_cursor: next, pdx: { epoch, ver, bseq: 0 } }) as never
const nexEntry = (ready: boolean): NexHostEntry =>
  ({ info: { configured: true, mounted: true, ready, init_error: '', effective: null }, capabilities: null, phase: ready ? 'ready' : 'unavailable', error: null, fetchedAt: 1, generation: 1, fingerprint: '1:1:' })

let closes: ReturnType<typeof vi.fn<() => void>>[]
const flush = () => vi.advanceTimersByTimeAsync(0)
const cache = () => useExecutionListStore.getState().byHost[A]
const store = () => useExecutionListStore.getState()
const ids = () => cache().items.map((i) => i.id)
const delta = (bseq: number, id: string, ver: number, rowOrNull: ExecutionSummary | null, cause: string[] = [], epoch = E1): NexDelta =>
  ({ epoch, bseq, id, ver, cause, row: rowOrNull })

type Gate = { resolve: (p: never) => void; reject: (e: unknown) => void }
const gate = (): Gate => {
  const g = {} as Gate
  vi.mocked(api.listExecutions).mockImplementationOnce(() => new Promise((resolve, reject) => { g.resolve = resolve as never; g.reject = reject }))
  return g
}

let stop: () => void
describe('execution list: delta capability, versioned walks, overlay (#1866 PR2a)', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    closes = []
    subscriptionSlots.resetForTests()
    resetExecutionListForTests()
    useExecutionListStore.setState({ byHost: {} })
    useNexHostStore.setState({ byHost: { [A]: nexEntry(true) } })
    useHostStore.setState({ hosts: { [A]: { id: A, name: 'A', ip: '1', port: 1, token: 't', order: 0 } }, hostOrder: [A], activeHostId: A, runtime: {} })
    vi.mocked(sse.openNexSse).mockReset().mockImplementation(() => { const close = vi.fn<() => void>(); closes.push(close); return { close } })
    vi.mocked(api.listExecutions).mockReset().mockResolvedValue(stamped(1, ['a']))
    vi.spyOn(subscriptionSlots, 'reserve')
    vi.spyOn(subscriptionSlots, 'unreserve')
    const a = startNexHostInvalidation(); const b = startExecutionListInvalidation()
    stop = () => { a(); b() }
  })
  afterEach(() => { stop(); vi.restoreAllMocks(); vi.useRealTimers() })

  describe('capability', () => {
    it('unknown: a subscribe behaves as before (lane, SSE, legacy walk)', async () => {
      store().subscribe(A)
      await flush()
      expect(subscriptionSlots.reserve).toHaveBeenCalledTimes(1)
      expect(sse.openNexSse).toHaveBeenCalledTimes(1)
      expect(api.listExecutions).toHaveBeenCalledWith(A, { includeArchived: false, limit: 500 })
      expect(cache().rowVers).toEqual({ a: 0 })
    })

    it('a late hello closes the SSE, unreserves the lane and reconciles with a versioned walk', async () => {
      store().subscribe(A)
      await flush()
      store().onHello(A, { epoch: E1, bseq: 0 })
      expect(closes[0]).toHaveBeenCalledTimes(1)
      expect(subscriptionSlots.unreserve).toHaveBeenCalledTimes(1)
      expect(capFor(A)).toBe(4)
      await flush()
      expect(api.listExecutions).toHaveBeenLastCalledWith(A, { includeArchived: false, limit: 100, pdxRetry: true })
      expect(cache().rowVers).toEqual({ a: 1 })
    })

    it('delta: a subscribe reserves nothing, opens no SSE and walks versioned; an explicit refetch never reopens the SSE', async () => {
      store().onHello(A, { epoch: E1, bseq: 0 })
      expect(api.listExecutions).not.toHaveBeenCalled() // no subscriber: capability and baseline only
      store().subscribe(A)
      await flush()
      expect(subscriptionSlots.reserve).not.toHaveBeenCalled()
      expect(sse.openNexSse).not.toHaveBeenCalled()
      expect(api.listExecutions).toHaveBeenCalledTimes(1)
      store().refetch(A)
      await flush()
      expect(api.listExecutions).toHaveBeenCalledTimes(2)
      expect(sse.openNexSse).not.toHaveBeenCalled()
    })

    it('stays delta across a readiness flap; a fingerprint change or host removal resets it', async () => {
      store().onHello(A, { epoch: E1, bseq: 0 })
      const unsub = store().subscribe(A)
      await flush()
      useNexHostStore.setState({ byHost: { [A]: nexEntry(false) } })
      useNexHostStore.setState({ byHost: { [A]: nexEntry(true) } })
      await flush()
      expect(sse.openNexSse).not.toHaveBeenCalled()
      useHostStore.setState({ hosts: { [A]: { id: A, name: 'A', ip: '9', port: 9, token: 't', order: 0 } } })
      await flush()
      useNexHostStore.setState({ byHost: { [A]: nexEntry(true) } }) // the new daemon is ready
      await flush()
      expect(sse.openNexSse).toHaveBeenCalledTimes(1) // unknown again: legacy path
      unsub()
      store().onHello(A, { epoch: E1, bseq: 0 })
      store().clearHost(A) // host removed
      store().subscribe(A)
      await flush()
      expect(sse.openNexSse).toHaveBeenCalledTimes(2)
    })
  })

  describe('delta handling', () => {
    const ready = async () => {
      store().onHello(A, { epoch: E1, bseq: 0 })
      store().subscribe(A)
      await flush()
    }

    it('is ignored before a hello', async () => {
      store().subscribe(A)
      await flush()
      store().applyDelta(A, delta(1, 'b', 5, row('b')))
      expect(ids()).toEqual(['a'])
    })

    it('upserts, tombstones, drops ver <= row.ver and ignores another epoch', async () => {
      await ready()
      store().applyDelta(A, delta(1, 'b', 5, row('b')))
      expect(ids()).toEqual(['a', 'b'])
      store().applyDelta(A, delta(2, 'b', 5, row('b', { brief: 'same ver' })))
      expect(cache().items.find((i) => i.id === 'b')!.brief).toBe('b')
      store().applyDelta(A, delta(3, 'b', 6, row('b', { brief: 'newer' })))
      expect(cache().items.find((i) => i.id === 'b')!.brief).toBe('newer')
      store().applyDelta(A, delta(4, 'a', 7, null))
      expect(ids()).toEqual(['b'])
      store().applyDelta(A, delta(5, 'z', 8, row('z'), [], 'OTHER'))
      expect(ids()).toEqual(['b'])
    })

    it('a bseq gap reconciles; the next contiguous delta applies', async () => {
      await ready()
      const calls = vi.mocked(api.listExecutions).mock.calls.length
      store().applyDelta(A, delta(5, 'b', 5, row('b')))
      await flush()
      expect(vi.mocked(api.listExecutions).mock.calls.length).toBe(calls + 1)
      expect(ids()).toEqual(['a']) // the gapped delta itself is not applied; the walk repairs
      store().applyDelta(A, delta(6, 'b', 9, row('b')))
      expect(ids()).toEqual(['a', 'b'])
    })

    it('#1963: a stale upsert for an id the walk dropped is rejected by the covering page ver; a newer one is accepted', async () => {
      vi.mocked(api.listExecutions).mockReset()
        .mockResolvedValueOnce(stamped(10, ['a'], 'b')).mockResolvedValueOnce(stamped(20, ['c']))
      await ready()
      store().applyDelta(A, delta(1, 'b', 9, row('b')))  // b sorts in page 1 (ver 10): older than the page that omitted it
      store().applyDelta(A, delta(2, 'd', 19, row('d'))) // d is covered by the final page (ver 20)
      expect(ids()).toEqual(['a', 'c'])
      store().applyDelta(A, delta(3, 'b', 11, row('b')))
      store().applyDelta(A, delta(4, 'd', 21, row('d')))
      expect(ids()).toEqual(['a', 'b', 'c', 'd'])
    })

    it('archivedRevision moves only for archive-membership deltas and committed reconciles', async () => {
      await ready()
      const rev = () => cache().archivedRevision ?? 0
      const r0 = rev()
      store().applyDelta(A, delta(1, 'a', 5, row('a', { brief: 'x' }), ['execution.title_changed']))
      expect(rev()).toBe(r0)
      store().applyDelta(A, delta(2, 'a', 6, row('a'), ['execution.archived']))
      expect(rev()).toBe(r0 + 1)
      store().applyDelta(A, delta(3, 'a', 7, row('a', { archived: true })))
      expect(rev()).toBe(r0 + 2)
      store().applyDelta(A, delta(4, 'a', 8, null))
      expect(rev()).toBe(r0 + 3)
      store().refetch(A)
      await flush()
      expect(rev()).toBe(r0 + 4)
    })
  })

  describe('overlay during a walk', () => {
    it('keeps a delta that arrives mid-walk over the older page, drops one the page already reflects, and honours a tombstone', async () => {
      store().onHello(A, { epoch: E1, bseq: 0 })
      const p1 = gate(); const p2 = gate()
      store().subscribe(A)
      store().applyDelta(A, delta(1, 'a', 12, row('a', { brief: 'fresh' }))) // before the first page lands
      p1.resolve(stamped(10, ['a', 'b'], 'b'))
      await flush()
      store().applyDelta(A, delta(2, 'b', 13, null)) // between pages: b was already read by page 1 (ver 10)
      store().applyDelta(A, delta(3, 'c', 5, row('c', { brief: 'old' }))) // older than the final page (ver 20)
      p2.resolve(stamped(20, ['c', 'd']))
      await flush()
      expect(ids()).toEqual(['a', 'c', 'd'])
      expect(cache().items.find((i) => i.id === 'a')!.brief).toBe('fresh')
      expect(cache().rowVers).toEqual({ a: 12, c: 20, d: 20 })
    })

    it('stays open through nex_busy retries and applies deltas that arrived meanwhile', async () => {
      store().onHello(A, { epoch: E1, bseq: 0 })
      vi.mocked(api.listExecutions).mockRejectedValueOnce(new NexApiError(503, 'nex_busy', 'busy')).mockRejectedValueOnce(new NexApiError(503, 'nex_busy', 'busy'))
      store().subscribe(A)
      await flush()
      store().applyDelta(A, delta(1, 'n', 50, row('n')))
      await vi.advanceTimersByTimeAsync(1000)
      expect(ids()).toEqual(['a', 'n'])
    })

    it('six busy answers put the cache in error and keep the previous rows', async () => {
      store().onHello(A, { epoch: E1, bseq: 0 })
      store().subscribe(A)
      await flush()
      vi.mocked(api.listExecutions).mockRejectedValue(new NexApiError(503, 'nex_busy', 'busy'))
      store().refetch(A)
      await vi.advanceTimersByTimeAsync(10_000)
      expect(cache().phase).toBe('error')
      expect(cache().error).toBe('nex_busy')
      expect(ids()).toEqual(['a'])
    })

    it('a walk whose epoch is not the hello baseline is discarded', async () => {
      store().onHello(A, { epoch: E1, bseq: 0 })
      vi.mocked(api.listExecutions).mockResolvedValue(stamped(1, ['x'], '', 'E2'))
      vi.spyOn(console, 'warn').mockImplementation(() => {})
      store().subscribe(A)
      await flush()
      expect(ids()).toEqual([])
      expect(cache().phase).toBe('loading')
    })

    it('a truncated delta walk leaves complete false, and deltas between walks do not change complete', async () => {
      store().onHello(A, { epoch: E1, bseq: 0 })
      vi.spyOn(console, 'warn').mockImplementation(() => {})
      let n = 0
      vi.mocked(api.listExecutions).mockImplementation(async () => stamped(1, [`r${n}`], `c${n++}`))
      store().subscribe(A)
      await flush()
      expect(cache().truncated).toBe(true)
      expect(cache().complete).toBe(false)
      vi.mocked(api.listExecutions).mockReset().mockResolvedValue(stamped(2, ['a']))
      store().refetch(A)
      await flush()
      expect(cache().complete).toBe(true)
      store().applyDelta(A, delta(1, 'b', 9, row('b')))
      store().applyDelta(A, delta(2, 'b', 10, null))
      expect(cache().complete).toBe(true)
    })
  })
})
