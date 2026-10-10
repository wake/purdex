import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import {
  BACKOFF_MAX_MS, BACKOFF_START_MS, CLOSE_AFTER_MS, NOT_FOUND_RETRY_MS, conversationKey, resetConversationStore, selectConversation,
  useConversationStore,
} from './useConversationStore'
import { ConversationApiError } from '../lib/conversations/api'
import type { ConversationItem, Frame, Increment, Snapshot, Turn } from '../lib/conversations/types'
import type { ConversationSocketOptions } from '../lib/conversations/ws'

const api = vi.hoisted(() => ({ snapshot: vi.fn(), increment: vi.fn(), subagent: vi.fn() }))
vi.mock('../lib/conversations/api', async (orig) => ({
  ...(await orig<typeof import('../lib/conversations/api')>()),
  fetchConversationSnapshot: api.snapshot,
  fetchConversationIncrement: api.increment,
  fetchConversationSubagent: api.subagent,
}))

interface FakeSock { opts: ConversationSocketOptions; closed: boolean }
const socks = vi.hoisted(() => ({ list: [] as FakeSock[] }))
vi.mock('../lib/conversations/ws', () => ({
  openConversationSocket: (opts: ConversationSocketOptions) => {
    const s: FakeSock = { opts, closed: false }
    socks.list.push(s)
    return { close: () => { s.closed = true } }
  },
}))

const H = 'h1'
const S = 'aaaaaaaa-1111-4111-8111-111111111111'

const user = (id: string, index: number): ConversationItem => ({ type: 'user', id, at: 1, index, text: id, source: 'user' }) as ConversationItem
const turn = (idx: number, items: ConversationItem[]): Turn => ({ id: `t${idx}`, index: idx, started_at: idx, outcome: 'done', items })
const snap = (turns: Turn[], over: { cursor?: string; hasMore?: boolean; total?: number; reset?: boolean } = {}): Snapshot => ({
  reset: over.reset,
  conversation: {
    key: { host_id: H, provider: 'claude', session_id: S }, backend: 'terminal', provider: 'claude', title: 'T', status: 'idle',
    capabilities: { source: 'transcript' }, turns,
  },
  header: { title: 'T', status: 'idle', backend: 'terminal', live: true },
  window: { first_index: turns[0]?.index ?? 0, last_index: turns[turns.length - 1]?.index ?? 0, total_turns: over.total ?? (turns[turns.length - 1]?.index ?? -1) + 1, has_more_before: over.hasMore ?? false },
  cursor: over.cursor ?? 'e:1',
})
const incr = (cursor: string, items: ConversationItem[] = []): Increment => ({
  changes: items.length ? [{ turn: { id: 't0', index: 0, started_at: 0, outcome: 'running' }, items }] : [],
  header: { title: 'T', status: 'running', backend: 'terminal', live: true }, cursor,
})
const f = (seq: number, type: string, value: unknown): Frame => ({ type, seq, value }) as Frame

const flush = async () => { for (let i = 0; i < 8; i++) await Promise.resolve() }
const lastSock = () => socks.list[socks.list.length - 1]
const entry = () => selectConversation(H, S)(useConversationStore.getState())
const deferred = <T,>() => { let resolve!: (v: T) => void; let reject!: (e: unknown) => void; const promise = new Promise<T>((a, b) => { resolve = a; reject = b }); return { promise, resolve, reject } }

beforeEach(() => {
  vi.useFakeTimers()
  socks.list = []
  api.snapshot.mockReset().mockImplementation(async () => snap([turn(0, [user('u0', 0)])]))
  api.increment.mockReset().mockImplementation(async (_h, _s, cursor: string) => ({ kind: 'changes', increment: incr(`${cursor}+`) }))
  api.subagent.mockReset()
  resetConversationStore()
})
afterEach(() => {
  resetConversationStore()
  vi.useRealTimers()
})

describe('acquire — the first read, then the stream', () => {
  it('reads a snapshot, then opens the stream from its cursor with the window size', async () => {
    useConversationStore.getState().acquire(H, S)
    expect(entry()?.status).toBe('loading')
    await flush()
    expect(api.snapshot).toHaveBeenCalledTimes(1)
    expect(api.snapshot.mock.calls[0][0]).toBe(H)
    expect(entry()?.doc.turns.map((t) => t.id)).toEqual(['t0'])
    expect(socks.list).toHaveLength(1)
    expect(lastSock().opts).toMatchObject({ hostId: H, sessionId: S, cursor: 'e:1', turns: 20 })
    expect(entry()?.status).toBe('loading') // live only when a frame has come
    lastSock().opts.onFrame(f(1, 'conversation.changes', incr('e:2', [user('u1', 1)])))
    expect(entry()?.status).toBe('live')
    expect(entry()?.doc.cursor).toBe('e:2')
  })

  it('applies every frame kind to the document', async () => {
    useConversationStore.getState().acquire(H, S)
    await flush()
    const on = lastSock().opts.onFrame
    on(f(1, 'conversation.snapshot', snap([turn(3, [user('u3', 0)])], { cursor: 'e:5', total: 4 })))
    expect(entry()?.doc.turns.map((t) => t.index)).toEqual([3])
    on(f(2, 'conversation.header', { header: { title: 'New', status: 'idle', backend: 'terminal', live: true }, cursor: 'e:6' }))
    expect(entry()?.doc.header?.title).toBe('New')
    on(f(3, 'conversation.capabilities', { capabilities: { send: 'prompt' } }))
    expect(entry()?.doc.capabilities).toEqual({ send: 'prompt' })
    on(f(4, 'approvals.snapshot', { approvals: [{ id: 'a1' }] }))
    on(f(5, 'approval', { op: 'opened', approval: { id: 'a2' } }))
    on(f(6, 'approval', { op: 'closed', approval: { id: 'a1' } }))
    expect(entry()?.doc.approvals.map((a) => a.id)).toEqual(['a2'])
    on(f(7, 'conversation.reset', {}))
    on(f(8, 'something.new', {})) // a later version's frame: ignored
    expect(entry()?.status).toBe('live')
  })
})

describe('ref-counting and the 30 s close', () => {
  it('two holders share one connection', async () => {
    const { acquire } = useConversationStore.getState()
    acquire(H, S)
    acquire(H, S)
    await flush()
    expect(api.snapshot).toHaveBeenCalledTimes(1)
    expect(socks.list).toHaveLength(1)
  })

  it('closes 30 s after the last holder lets go, and drops the entry', async () => {
    const release = useConversationStore.getState().acquire(H, S)
    await flush()
    release()
    await vi.advanceTimersByTimeAsync(CLOSE_AFTER_MS - 1)
    expect(lastSock().closed).toBe(false)
    expect(entry()).toBeDefined()
    await vi.advanceTimersByTimeAsync(2)
    expect(lastSock().closed).toBe(true)
    expect(entry()).toBeUndefined()
  })

  it('a holder that comes back within the 30 s keeps the connection (a tab switch)', async () => {
    const r1 = useConversationStore.getState().acquire(H, S)
    await flush()
    r1()
    await vi.advanceTimersByTimeAsync(10_000)
    const r2 = useConversationStore.getState().acquire(H, S)
    await vi.advanceTimersByTimeAsync(CLOSE_AFTER_MS * 2)
    expect(lastSock().closed).toBe(false)
    expect(socks.list).toHaveLength(1)
    expect(api.snapshot).toHaveBeenCalledTimes(1)
    r2()
  })

  it('one of two holders letting go closes nothing; a release is idempotent', async () => {
    const r1 = useConversationStore.getState().acquire(H, S)
    useConversationStore.getState().acquire(H, S)
    await flush()
    r1()
    r1()
    await vi.advanceTimersByTimeAsync(CLOSE_AFTER_MS * 2)
    expect(lastSock().closed).toBe(false)
    expect(entry()).toBeDefined()
  })

  it('after the close a new holder starts over with a fresh snapshot', async () => {
    useConversationStore.getState().acquire(H, S)()
    await flush()
    await vi.advanceTimersByTimeAsync(CLOSE_AFTER_MS + 1)
    useConversationStore.getState().acquire(H, S)
    await flush()
    expect(api.snapshot).toHaveBeenCalledTimes(2)
    expect(socks.list).toHaveLength(2)
  })

  it('conversations are kept apart by host and session', async () => {
    useConversationStore.getState().acquire(H, S)
    useConversationStore.getState().acquire('h2', S)
    await flush()
    expect(socks.list).toHaveLength(2)
    expect(Object.keys(useConversationStore.getState().byKey).sort()).toEqual([conversationKey(H, S), conversationKey('h2', S)].sort())
  })
})

describe('reconnecting', () => {
  it('a closed stream reconnects with backoff, reading the increment from the held cursor first', async () => {
    useConversationStore.getState().acquire(H, S)
    await flush()
    lastSock().opts.onFrame(f(1, 'conversation.changes', incr('e:2')))
    lastSock().opts.onClose({ gap: false, failed: false })
    expect(entry()?.status).toBe('reconnecting')
    await vi.advanceTimersByTimeAsync(BACKOFF_START_MS - 1)
    expect(api.increment).not.toHaveBeenCalled()
    await vi.advanceTimersByTimeAsync(2)
    expect(api.increment).toHaveBeenCalledTimes(1)
    expect(api.increment.mock.calls[0][2]).toBe('e:2')
    expect(socks.list).toHaveLength(2)
    expect(socks.list[1].opts.cursor).toBe('e:2+') // the catch-up moved it
    expect(api.snapshot).toHaveBeenCalledTimes(1) // not a second snapshot
  })

  it('the backoff doubles up to the cap and a frame resets it', async () => {
    useConversationStore.getState().acquire(H, S)
    await flush()
    const delays: number[] = []
    for (let i = 0; i < 7; i++) {
      const before = socks.list.length
      lastSock().opts.onClose({ gap: false, failed: true })
      let waited = 0
      while (socks.list.length === before && waited < BACKOFF_MAX_MS * 2) {
        await vi.advanceTimersByTimeAsync(500)
        waited += 500
      }
      delays.push(waited)
    }
    expect(delays[0]).toBe(BACKOFF_START_MS)
    expect(delays[1]).toBe(BACKOFF_START_MS * 2)
    expect(delays[2]).toBe(BACKOFF_START_MS * 4)
    expect(Math.max(...delays)).toBe(BACKOFF_MAX_MS)
    lastSock().opts.onFrame(f(1, 'conversation.changes', incr('x')))
    const before = socks.list.length
    lastSock().opts.onClose({ gap: false, failed: false })
    await vi.advanceTimersByTimeAsync(BACKOFF_START_MS + 10)
    expect(socks.list.length).toBe(before + 1)
  })

  it('a seq gap reconnects at once, with no waiting', async () => {
    useConversationStore.getState().acquire(H, S)
    await flush()
    lastSock().opts.onClose({ gap: true, failed: false })
    await vi.advanceTimersByTimeAsync(0)
    expect(socks.list).toHaveLength(2)
  })

  it('a stale cursor is answered with a snapshot, which replaces the document', async () => {
    useConversationStore.getState().acquire(H, S)
    await flush()
    api.increment.mockImplementationOnce(async () => ({ kind: 'snapshot', snapshot: snap([turn(9, [user('u9', 0)])], { cursor: 'f:1', total: 10, reset: true }) }))
    lastSock().opts.onClose({ gap: false, failed: false })
    await vi.advanceTimersByTimeAsync(BACKOFF_START_MS + 1)
    expect(entry()?.doc.turns.map((t) => t.index)).toEqual([9])
    expect(socks.list[1].opts.cursor).toBe('f:1')
  })

  it('a close from an earlier attempt’s socket does not start another reconnect', async () => {
    useConversationStore.getState().acquire(H, S)
    await flush()
    const first = socks.list[0]
    first.opts.onClose({ gap: false, failed: false })
    await vi.advanceTimersByTimeAsync(BACKOFF_START_MS + 1)
    expect(socks.list).toHaveLength(2)
    first.opts.onClose({ gap: false, failed: false }) // late, from the retired socket
    first.opts.onFrame(f(1, 'conversation.header', { header: { title: 'LATE', status: 'idle', backend: '', live: false }, cursor: 'z' }))
    await vi.advanceTimersByTimeAsync(BACKOFF_MAX_MS * 2)
    expect(socks.list).toHaveLength(2)
    expect(entry()?.doc.header?.title).toBe('T')
  })
})

describe('unreadable and failing reads', () => {
  it('not_found is unreadable and asked again every few seconds; it recovers when the transcript appears', async () => {
    api.snapshot.mockRejectedValueOnce(new ConversationApiError(404, 'not_found'))
    useConversationStore.getState().acquire(H, S)
    await flush()
    expect(entry()).toMatchObject({ status: 'unreadable', reason: 'not_found' })
    expect(socks.list).toHaveLength(0)
    await vi.advanceTimersByTimeAsync(NOT_FOUND_RETRY_MS + 1)
    expect(api.snapshot).toHaveBeenCalledTimes(2)
    expect(socks.list).toHaveLength(1)
    lastSock().opts.onFrame(f(1, 'conversation.changes', incr('e:2')))
    expect(entry()).toMatchObject({ status: 'live', reason: '' })
  })

  it('provider_unsupported is unreadable for good: no retry', async () => {
    api.snapshot.mockRejectedValue(new ConversationApiError(404, 'provider_unsupported'))
    useConversationStore.getState().acquire(H, S)
    await flush()
    await vi.advanceTimersByTimeAsync(NOT_FOUND_RETRY_MS * 10)
    expect(entry()).toMatchObject({ status: 'unreadable', reason: 'provider_unsupported' })
    expect(api.snapshot).toHaveBeenCalledTimes(1)
  })

  it('a busy or unreachable host is an error that retries with backoff', async () => {
    api.snapshot.mockRejectedValueOnce(new ConversationApiError(503, 'busy')).mockRejectedValueOnce(new Error('network'))
    useConversationStore.getState().acquire(H, S)
    await flush()
    expect(entry()?.status).toBe('reconnecting')
    await vi.advanceTimersByTimeAsync(BACKOFF_START_MS + 1)
    expect(api.snapshot).toHaveBeenCalledTimes(2)
    await vi.advanceTimersByTimeAsync(BACKOFF_START_MS * 2 + 1)
    expect(api.snapshot).toHaveBeenCalledTimes(3)
    expect(socks.list).toHaveLength(1)
  })

  it('a bad_cursor starts over from a snapshot', async () => {
    useConversationStore.getState().acquire(H, S)
    await flush()
    api.increment.mockRejectedValueOnce(new ConversationApiError(400, 'bad_cursor'))
    lastSock().opts.onClose({ gap: false, failed: false })
    await vi.advanceTimersByTimeAsync(BACKOFF_START_MS + 1)
    await vi.advanceTimersByTimeAsync(BACKOFF_START_MS * 2 + 1)
    expect(api.snapshot).toHaveBeenCalledTimes(2)
  })

  it('letting go while the first read is out opens no stream', async () => {
    const gate = deferred<Snapshot>()
    api.snapshot.mockImplementationOnce(() => gate.promise)
    const release = useConversationStore.getState().acquire(H, S)
    release()
    await vi.advanceTimersByTimeAsync(CLOSE_AFTER_MS + 1)
    gate.resolve(snap([turn(0, [])]))
    await flush()
    expect(socks.list).toHaveLength(0)
  })
})

describe('paging and jumping', () => {
  const live = async (over: Parameters<typeof snap>[1] = {}) => {
    api.snapshot.mockImplementationOnce(async () => snap([turn(5, [user('u5', 0)]), turn(6, [user('u6', 0)])], { hasMore: true, total: 7, ...over }))
    useConversationStore.getState().acquire(H, S)
    await flush()
  }

  it('loadBefore asks for the turns before the first held and prepends them; the live cursor stays', async () => {
    await live({ cursor: 'e:9' })
    api.snapshot.mockImplementationOnce(async () => snap([turn(3, [user('u3', 0)]), turn(4, [user('u4', 0)])], { hasMore: false, cursor: 'PAGE', total: 7 }))
    await useConversationStore.getState().loadBefore(H, S)
    expect(api.snapshot.mock.calls[1][2]).toMatchObject({ before: 5, turns: 20 })
    expect(entry()?.doc.turns.map((t) => t.index)).toEqual([3, 4, 5, 6])
    expect(entry()?.doc.cursor).toBe('e:9')
    expect(entry()?.doc.hasMoreBefore).toBe(false)
    expect(entry()?.paging).toBe(false)
  })

  it('does nothing when there is nothing older, or a page is already being read', async () => {
    await live({ hasMore: false })
    await useConversationStore.getState().loadBefore(H, S)
    expect(api.snapshot).toHaveBeenCalledTimes(1)
  })

  it('a second loadBefore while one is out is not sent', async () => {
    await live()
    const gate = deferred<Snapshot>()
    api.snapshot.mockImplementationOnce(() => gate.promise)
    const first = useConversationStore.getState().loadBefore(H, S)
    expect(entry()?.paging).toBe(true)
    await useConversationStore.getState().loadBefore(H, S)
    expect(api.snapshot).toHaveBeenCalledTimes(2)
    gate.resolve(snap([turn(4, [user('u4', 0)])], { hasMore: true, total: 7 }))
    await first
    expect(entry()?.doc.turns.map((t) => t.index)).toEqual([4, 5, 6])
  })

  it('a page that comes back after a reset snapshot replaced the document is dropped', async () => {
    await live()
    const gate = deferred<Snapshot>()
    api.snapshot.mockImplementationOnce(() => gate.promise)
    const page = useConversationStore.getState().loadBefore(H, S)
    lastSock().opts.onFrame(f(1, 'conversation.snapshot', snap([turn(9, [user('u9', 0)])], { reset: true, cursor: 'f:1', total: 10 })))
    gate.resolve(snap([turn(4, [user('stale4', 0)])], { hasMore: true, total: 7 }))
    await page
    expect(entry()?.doc.turns.map((t) => t.index)).toEqual([9])
  })

  it('a jump while a page is out aborts the page, resets paging, and the late page changes nothing', async () => {
    await live()
    const gate = deferred<Snapshot>()
    api.snapshot.mockImplementationOnce(() => gate.promise)
    const page = useConversationStore.getState().loadBefore(H, S)
    api.snapshot.mockImplementationOnce(async () => snap([turn(1, [user('u1', 0)]), turn(2, [user('u2', 0)])], { hasMore: true, total: 7 }))
    const outcome = await useConversationStore.getState().jumpTo(H, S, 'u1')
    expect(outcome).toBe('ok')
    expect(api.snapshot.mock.calls[2][2]).toMatchObject({ around: 'u1' })
    expect((api.snapshot.mock.calls[1][2] as { signal: AbortSignal }).signal.aborted).toBe(true)
    gate.resolve(snap([turn(4, [user('late', 0)])], { hasMore: true, total: 7 }))
    await page
    expect(entry()?.doc.turns.map((t) => t.index)).toEqual([1, 2])
    expect(entry()?.doc.detached).toBe(true)
    expect(entry()?.paging).toBe(false)
  })

  it('two jumps: the earlier one is aborted and reports superseded, the later one wins', async () => {
    await live()
    const gate = deferred<Snapshot>()
    api.snapshot.mockImplementationOnce(() => gate.promise)
    const first = useConversationStore.getState().jumpTo(H, S, 'old')
    api.snapshot.mockImplementationOnce(async () => snap([turn(2, [user('new', 0)])], { total: 7 }))
    const second = await useConversationStore.getState().jumpTo(H, S, 'new')
    gate.resolve(snap([turn(0, [user('old', 0)])], { total: 7 }))
    expect(await first).toBe('superseded')
    expect(second).toBe('ok')
    expect(entry()?.doc.turns.map((t) => t.id)).toEqual(['t2'])
  })

  it('a jump the daemon refuses returns its code and leaves the document alone', async () => {
    await live()
    api.snapshot.mockRejectedValueOnce(new ConversationApiError(404, 'item_not_found'))
    expect(await useConversationStore.getState().jumpTo(H, S, 'nope')).toBe('item_not_found')
    api.snapshot.mockRejectedValueOnce(new ConversationApiError(422, 'item_not_shown'))
    expect(await useConversationStore.getState().jumpTo(H, S, 'big')).toBe('item_not_shown')
    expect(entry()?.doc.turns.map((t) => t.index)).toEqual([5, 6])
    expect(await useConversationStore.getState().jumpTo(H, 'unheld', 'x')).toBe('not_open')
  })

  it('returnToLive reads the newest window again and ends the detachment', async () => {
    await live({ total: 10 })
    api.snapshot.mockImplementationOnce(async () => snap([turn(2, [user('u2', 0)])], { hasMore: true, total: 10 }))
    await useConversationStore.getState().jumpTo(H, S, 'u2')
    expect(entry()?.doc.detached).toBe(true)
    api.snapshot.mockImplementationOnce(async () => snap([turn(8, [user('u8', 0)]), turn(9, [user('u9', 0)])], { hasMore: true, total: 10, cursor: 'e:20' }))
    await useConversationStore.getState().returnToLive(H, S)
    expect(entry()?.doc.detached).toBe(false)
    expect(entry()?.doc.turns.map((t) => t.index)).toEqual([8, 9])
  })

  it('nothing is done for a conversation nobody holds', async () => {
    await useConversationStore.getState().loadBefore(H, S)
    await useConversationStore.getState().returnToLive(H, S)
    expect(api.snapshot).not.toHaveBeenCalled()
  })
})

describe('subagents on demand', () => {
  it('loads once, keeps the answer, and a failure is a state to retry from', async () => {
    useConversationStore.getState().acquire(H, S)
    await flush()
    api.subagent.mockResolvedValueOnce({ items: [{ type: 'agent_text', id: 'x', at: 1, markdown: 'hi' }], partial: false })
    await useConversationStore.getState().loadSubagent(H, S, 'ag1')
    expect(entry()?.subagents.ag1).toMatchObject({ state: 'ready' })
    await useConversationStore.getState().loadSubagent(H, S, 'ag1')
    expect(api.subagent).toHaveBeenCalledTimes(1)
    api.subagent.mockRejectedValueOnce(new ConversationApiError(404, 'not_found'))
    await useConversationStore.getState().loadSubagent(H, S, 'ag2')
    expect(entry()?.subagents.ag2).toMatchObject({ state: 'error' })
    api.subagent.mockResolvedValueOnce({ items: [], partial: true })
    await useConversationStore.getState().loadSubagent(H, S, 'ag2')
    expect(entry()?.subagents.ag2).toMatchObject({ state: 'ready', answer: { partial: true } })
  })
})
