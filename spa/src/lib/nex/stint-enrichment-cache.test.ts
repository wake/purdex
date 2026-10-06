import { describe, it, expect, vi } from 'vitest'
import { createStintEnrichmentCache } from './stint-enrichment-cache'
import { ENRICHMENT_EVENT_BUDGET } from './stint-enrichment'
import type { EventsPage, NexEvent } from './types'

const ev = (seq: number): NexEvent =>
  ({ seq, execution_id: 'exc_A', kind: 'assistant', payload: { type: 'assistant', message: { role: 'assistant', content: [], stop_reason: null } }, created_at: 1 })
const page = (from: number, n: number, next: number): EventsPage => ({ items: Array.from({ length: n }, (_, i) => ev(from + i)), next_cursor: next })
/** A fetch whose calls settle when the test says so. */
function deferredFetch() {
  const pending: Array<{ resolve: (p: EventsPage) => void; reject: (e: unknown) => void }> = []
  const fn = vi.fn(() => new Promise<EventsPage>((resolve, reject) => { pending.push({ resolve, reject }) }))
  return { fn, pending }
}
const flush = () => new Promise((r) => setTimeout(r, 0))

describe('createStintEnrichmentCache', () => {
  it('two requests for one stint start one fetch', async () => {
    const fetch = vi.fn(async () => page(1, 2, 0))
    const cache = createStintEnrichmentCache(fetch)
    cache.request('h', 'exc_A')
    cache.request('h', 'exc_A')
    await flush()
    cache.request('h', 'exc_A')
    await flush()
    expect(fetch).toHaveBeenCalledTimes(1)
    expect(fetch).toHaveBeenCalledWith('h', 'exc_A', { after: 0, limit: 500, signal: expect.any(AbortSignal) })
  })

  it('get is undefined until the fetch settles, then the enrichment', async () => {
    const { fn, pending } = deferredFetch()
    const cache = createStintEnrichmentCache(fn)
    expect(cache.get('exc_A')).toBeUndefined()
    cache.request('h', 'exc_A')
    expect(cache.get('exc_A')).toBeUndefined()
    pending[0].resolve(page(1, 3, 0))
    await flush()
    const e = cache.get('exc_A')
    expect(e).toMatchObject({ truncated: false })
    expect(e?.messages).toHaveLength(3)
    // The same object on every read: useSyncExternalStore compares by identity.
    expect(cache.get('exc_A')).toBe(e)
    // Another stint is untouched.
    expect(cache.get('exc_B')).toBeUndefined()
  })

  it('a rejected fetch settles null, and later requests in the same pane never refetch it', async () => {
    const fetch = vi.fn(async () => { throw new Error('down') })
    const cache = createStintEnrichmentCache(fetch)
    cache.request('h', 'exc_A')
    await flush()
    expect(cache.get('exc_A')).toBeNull()
    cache.request('h', 'exc_A')
    await flush()
    expect(fetch).toHaveBeenCalledTimes(1)
    expect(cache.get('exc_A')).toBeNull()
  })

  it('revision bumps on each settle, success or failure, and tells every subscriber', async () => {
    const { fn, pending } = deferredFetch()
    const cache = createStintEnrichmentCache(fn)
    const a = vi.fn()
    const b = vi.fn()
    cache.subscribe(a)
    const unsubscribeB = cache.subscribe(b)
    expect(cache.revision()).toBe(0)
    cache.request('h', 'exc_A')
    cache.request('h', 'exc_B')
    expect(cache.revision()).toBe(0)
    pending[0].resolve(page(1, 1, 0))
    await flush()
    expect(cache.revision()).toBe(1)
    expect([a.mock.calls.length, b.mock.calls.length]).toEqual([1, 1])
    unsubscribeB()
    pending[1].reject(new Error('down'))
    await flush()
    expect(cache.revision()).toBe(2)
    expect([a.mock.calls.length, b.mock.calls.length]).toEqual([2, 1])
  })

  it('pages forward from 0 until next_cursor is 0', async () => {
    const fetch = vi.fn(async (_h: string, _id: string, { after }: { after: number }) => (after === 0 ? page(1, 500, 500) : page(501, 20, 0)))
    const cache = createStintEnrichmentCache(fetch)
    cache.request('h', 'exc_A')
    await flush()
    expect(fetch.mock.calls.map((c) => ({ ...c[2], signal: undefined }))).toEqual([{ after: 0, limit: 500 }, { after: 500, limit: 500 }])
    expect(cache.get('exc_A')).toMatchObject({ truncated: false })
    expect(cache.get('exc_A')?.messages).toHaveLength(520)
  })

  it('stops one page past the budget, so it can tell exactly the budget from more', async () => {
    const fetch = vi.fn(async (_h: string, _id: string, { after }: { after: number }) => page(after + 1, 500, after + 500))
    const cache = createStintEnrichmentCache(fetch)
    cache.request('h', 'exc_A')
    await flush()
    // 10 pages hold exactly the budget; the 11th shows there is more.
    expect(fetch).toHaveBeenCalledTimes(ENRICHMENT_EVENT_BUDGET / 500 + 1)
    expect(cache.get('exc_A')).toMatchObject({ truncated: true })
    expect(cache.get('exc_A')?.messages).toHaveLength(ENRICHMENT_EVENT_BUDGET)
  })

  it('a cursor that does not move settles null instead of paging forever', async () => {
    const fetch = vi.fn(async () => ({ items: [], next_cursor: 7 }))
    const cache = createStintEnrichmentCache(fetch)
    cache.request('h', 'exc_A')
    await flush()
    expect(fetch).toHaveBeenCalledTimes(2)
    expect(cache.get('exc_A')).toBeNull()
  })
})

// The pane is gone (ExecutionView unmounted): no walk keeps paging for nobody.
describe('createStintEnrichmentCache — dispose', () => {
  it('a first page that lands after dispose asks for no second one, and nothing settles or notifies', async () => {
    const { fn, pending } = deferredFetch()
    const cache = createStintEnrichmentCache(fn)
    const listener = vi.fn()
    cache.subscribe(listener)
    cache.request('h', 'exc_A')
    cache.dispose()
    expect(cache.isDisposed()).toBe(true)
    // The fetch ignored its signal: the page lands anyway, with more to come.
    pending[0].resolve(page(1, 500, 500))
    await flush()
    expect(fn).toHaveBeenCalledTimes(1)
    expect(cache.revision()).toBe(0)
    expect(listener).not.toHaveBeenCalled()
    expect(cache.get('exc_A')).toBeUndefined()
  })

  it('aborts the signal every walk in flight was given', async () => {
    const signals: AbortSignal[] = []
    const fetch = vi.fn((_h: string, _id: string, opts: { signal?: AbortSignal }) => {
      signals.push(opts.signal!)
      return new Promise<EventsPage>(() => {})
    })
    const cache = createStintEnrichmentCache(fetch)
    cache.request('h', 'exc_A')
    cache.request('h', 'exc_B')
    expect(signals.map((s) => s.aborted)).toEqual([false, false])
    cache.dispose()
    expect(signals.map((s) => s.aborted)).toEqual([true, true])
  })

  it('an aborted fetch rejecting is silent: no console, no settle', async () => {
    const error = vi.spyOn(console, 'error')
    const warn = vi.spyOn(console, 'warn')
    try {
      const fetch = vi.fn((_h: string, _id: string, opts: { signal?: AbortSignal }) => new Promise<EventsPage>((_, reject) => {
        opts.signal!.addEventListener('abort', () => reject(new DOMException('aborted', 'AbortError')))
      }))
      const cache = createStintEnrichmentCache(fetch)
      const listener = vi.fn()
      cache.subscribe(listener)
      cache.request('h', 'exc_A')
      cache.dispose()
      await flush()
      expect([cache.revision(), listener.mock.calls.length, cache.get('exc_A')]).toEqual([0, 0, undefined])
      expect(error).not.toHaveBeenCalled()
      expect(warn).not.toHaveBeenCalled()
    } finally {
      error.mockRestore()
      warn.mockRestore()
    }
  })

  it('request after dispose makes no request; dispose before any request, or twice, is harmless', async () => {
    const fetch = vi.fn(async () => page(1, 1, 0))
    const cache = createStintEnrichmentCache(fetch)
    cache.dispose()
    cache.dispose()
    cache.request('h', 'exc_A')
    await flush()
    expect(fetch).not.toHaveBeenCalled()
    expect([cache.get('exc_A'), cache.revision()]).toEqual([undefined, 0])
  })
})
