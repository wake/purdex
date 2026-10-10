import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { SendQueue, UNDO_MS, clearAllSendQueues, sendQueueFor } from './send-queue'
import type { SendOutcome, SendPort } from './send'
import type { UserItem } from './types'

interface Call { text: string; id: string; resolve: (o: SendOutcome) => void }

function fakePort() {
  const calls: Call[] = []
  const port: SendPort = {
    submit: (text, id) => new Promise<SendOutcome>((resolve) => { calls.push({ text, id, resolve }) }),
    interrupt: vi.fn(() => Promise.resolve<SendOutcome>({ kind: 'accepted' })),
  }
  return { calls, port }
}
const flush = () => vi.advanceTimersByTimeAsync(0)
const user = (id: string, text: string, at: number, extra: Partial<UserItem> = {}): UserItem => ({ id, type: 'user', text, at, index: 0, source: 'user', ...extra })

beforeEach(() => { vi.useFakeTimers(); vi.setSystemTime(1_000_000) })
afterEach(() => { vi.useRealTimers(); clearAllSendQueues() })

describe('undo window', () => {
  it('holds a message for 3 s before it goes out', async () => {
    const { calls, port } = fakePort()
    const q = new SendQueue(port)
    q.enqueue('hello')
    await vi.advanceTimersByTimeAsync(UNDO_MS - 1)
    expect(calls).toHaveLength(0)
    await vi.advanceTimersByTimeAsync(1)
    expect(calls.map((c) => c.text)).toEqual(['hello'])
    expect(q.entries()[0].state).toBe('sending')
  })

  it('undo inside the window returns the text and nothing is sent', async () => {
    const { calls, port } = fakePort()
    const q = new SendQueue(port)
    const id = q.enqueue('oops')
    await vi.advanceTimersByTimeAsync(2000)
    expect(q.undo(id)).toBe('oops')
    await vi.advanceTimersByTimeAsync(5000)
    expect(calls).toHaveLength(0)
    expect(q.entries()).toHaveLength(0)
  })

  it('undo after the message went out does nothing', async () => {
    const { calls, port } = fakePort()
    const q = new SendQueue(port)
    const id = q.enqueue('gone')
    await vi.advanceTimersByTimeAsync(UNDO_MS)
    expect(q.undo(id)).toBeUndefined()
    expect(calls).toHaveLength(1)
  })
})

describe('one serial chain', () => {
  it('two quick sends go out in order, one at a time, with different ids', async () => {
    const { calls, port } = fakePort()
    const q = new SendQueue(port)
    const a = q.enqueue('first')
    const b = q.enqueue('second')
    expect(a).not.toBe(b)
    await vi.advanceTimersByTimeAsync(UNDO_MS)
    expect(calls.map((c) => c.text)).toEqual(['first'])
    calls[0].resolve({ kind: 'accepted' })
    await flush()
    expect(calls.map((c) => c.text)).toEqual(['first', 'second'])
    expect(calls.map((c) => c.id)).toEqual([a, b])
  })

  it('undoing the head lets the next one through', async () => {
    const { calls, port } = fakePort()
    const q = new SendQueue(port)
    const a = q.enqueue('first')
    q.enqueue('second')
    q.undo(a)
    await vi.advanceTimersByTimeAsync(UNDO_MS)
    expect(calls.map((c) => c.text)).toEqual(['second'])
  })

  it('an accepted message waits as 「排隊中」 (sent) until the transcript shows it', async () => {
    const { calls, port } = fakePort()
    const q = new SendQueue(port)
    const id = q.enqueue('hi')
    await vi.advanceTimersByTimeAsync(UNDO_MS)
    calls[0].resolve({ kind: 'accepted' })
    await flush()
    expect(q.entries()[0]).toMatchObject({ id, state: 'sent' })
    q.reconcile([user('u1', 'hi', 1_003_500, { client_msg_id: id })])
    expect(q.entries()).toHaveLength(0)
  })
})

describe('busy', () => {
  it('stays in the App and is resent with the SAME client_msg_id when the agent turns idle', async () => {
    const { calls, port } = fakePort()
    const q = new SendQueue(port)
    q.setIdle(false)
    const id = q.enqueue('later')
    await vi.advanceTimersByTimeAsync(UNDO_MS)
    calls[0].resolve({ kind: 'busy' })
    await flush()
    expect(q.entries()[0].state).toBe('waiting')
    await vi.advanceTimersByTimeAsync(60_000)
    expect(calls).toHaveLength(1)
    q.setIdle(true)
    await flush()
    expect(calls).toHaveLength(2)
    expect(calls[1]).toMatchObject({ text: 'later', id })
    calls[1].resolve({ kind: 'accepted' })
    await flush()
    expect(q.entries()[0].state).toBe('sent')
  })

  it('a message behind a busy one waits for it (order is kept)', async () => {
    const { calls, port } = fakePort()
    const q = new SendQueue(port)
    q.setIdle(false)
    q.enqueue('one')
    q.enqueue('two')
    await vi.advanceTimersByTimeAsync(UNDO_MS)
    calls[0].resolve({ kind: 'busy' })
    await vi.advanceTimersByTimeAsync(10_000)
    expect(calls).toHaveLength(1)
    q.setIdle(true)
    await flush()
    expect(calls.map((c) => c.text)).toEqual(['one', 'one'])
  })

  it('an idle observed while the request is in flight resends once on the busy answer; a stale header cannot loop it', async () => {
    const { calls, port } = fakePort()
    const q = new SendQueue(port)
    q.setIdle(true)
    q.enqueue('x')
    await vi.advanceTimersByTimeAsync(UNDO_MS)
    q.setIdle(true) // observed while the request is in flight
    calls[0].resolve({ kind: 'busy' })
    await flush()
    await vi.advanceTimersByTimeAsync(10_000)
    expect(calls).toHaveLength(2)
    calls[1].resolve({ kind: 'busy' })
    await flush()
    await vi.advanceTimersByTimeAsync(10_000)
    expect(calls).toHaveLength(2) // the observation is spent: only a NEW one resends again
  })

  it('busy settled while the pane was unmounted: the idle the remount reports resends exactly once', async () => {
    const { calls, port } = fakePort()
    const q = new SendQueue(port)
    q.setIdle(true) // recorded before the pane went away
    const id = q.enqueue('x')
    await vi.advanceTimersByTimeAsync(UNDO_MS)
    calls[0].resolve({ kind: 'busy' }) // pane unmounted: nothing observes
    await flush()
    expect(calls).toHaveLength(1)
    q.setIdle(true) // remount: idle is still true, no false -> true edge
    await flush()
    expect(calls).toHaveLength(2)
    expect(calls[1].id).toBe(id)
    calls[1].resolve({ kind: 'accepted' })
    await flush()
    q.setIdle(true)
    await vi.advanceTimersByTimeAsync(10_000)
    expect(calls).toHaveLength(2)
  })

  it('a second busy waits for a newer idle observation again', async () => {
    const { calls, port } = fakePort()
    const q = new SendQueue(port)
    q.enqueue('x')
    await vi.advanceTimersByTimeAsync(UNDO_MS)
    calls[0].resolve({ kind: 'busy' })
    await flush()
    q.setIdle(true)
    await flush()
    calls[1].resolve({ kind: 'busy' })
    await flush()
    q.setIdle(false) // only an idle observation resends
    await vi.advanceTimersByTimeAsync(10_000)
    expect(calls).toHaveLength(2)
    q.setIdle(true)
    await flush()
    expect(calls).toHaveLength(3)
  })

  it('a waiting message can be taken back', async () => {
    const { calls, port } = fakePort()
    const q = new SendQueue(port)
    const id = q.enqueue('x')
    await vi.advanceTimersByTimeAsync(UNDO_MS)
    calls[0].resolve({ kind: 'busy' })
    await flush()
    expect(q.undo(id)).toBe('x')
  })
})

describe('unknown: at most once', () => {
  it.each<SendOutcome>([{ kind: 'unknown', reason: 'no_result' }, { kind: 'network' }, { kind: 'not_owner' }])('%j is never resent by itself', async (outcome) => {
    const { calls, port } = fakePort()
    const q = new SendQueue(port)
    q.setIdle(false)
    q.enqueue('maybe sent')
    await vi.advanceTimersByTimeAsync(UNDO_MS)
    calls[0].resolve(outcome)
    await flush()
    q.setIdle(true)
    q.setIdle(false)
    q.setIdle(true)
    await vi.advanceTimersByTimeAsync(120_000)
    expect(calls).toHaveLength(1)
    expect(q.entries()[0].state).toBe('maybe')
  })

  it('is settled by the transcript: client_msg_id echo', async () => {
    const { calls, port } = fakePort()
    const q = new SendQueue(port)
    const id = q.enqueue('hello there')
    await vi.advanceTimersByTimeAsync(UNDO_MS)
    calls[0].resolve({ kind: 'unknown' })
    await flush()
    q.reconcile([user('u9', 'something else', 1_004_000), user('u10', 'hello there', 1_004_100, { client_msg_id: id })])
    expect(q.entries()).toHaveLength(0)
  })

  it('is settled by the same text within 30 s, and not by one outside or by a message with another id', async () => {
    const { calls, port } = fakePort()
    const q = new SendQueue(port)
    q.enqueue('same words')
    await vi.advanceTimersByTimeAsync(UNDO_MS)
    calls[0].resolve({ kind: 'network' })
    await flush()
    q.reconcile([user('old', 'same words', 1_003_000 - 31_000), user('other', 'same words', 1_005_000, { client_msg_id: 'someone-else' })])
    expect(q.entries()[0].state).toBe('maybe')
    q.reconcile([user('near', 'same words', 1_003_000 + 29_000)])
    expect(q.entries()).toHaveLength(0)
  })

  it('the text fallback never settles a message still in flight (sending), only its own id can', async () => {
    const { calls, port } = fakePort()
    const q = new SendQueue(port)
    const id = q.enqueue('in flight')
    q.enqueue('behind')
    await vi.advanceTimersByTimeAsync(UNDO_MS)
    q.reconcile([user('u1', 'in flight', 1_003_100)])
    expect(q.entries().map((e) => e.state)).toEqual(['sending', 'undo'])
    expect(calls).toHaveLength(1) // and the chain did not advance
    q.reconcile([user('u1', 'in flight', 1_003_100, { client_msg_id: id })])
    expect(q.entries().map((e) => e.text)).toEqual(['behind'])
  })

  it('the window is 2 s before the request and 30 s after: earlier same text is not an echo', async () => {
    const { calls, port } = fakePort()
    const q = new SendQueue(port)
    q.enqueue('window')
    await vi.advanceTimersByTimeAsync(UNDO_MS) // startedAt = 1_003_000
    calls[0].resolve({ kind: 'unknown' })
    await flush()
    q.reconcile([user('early', 'window', 1_003_000 - 3000)])
    expect(q.entries()[0].state).toBe('maybe')
    q.reconcile([user('late', 'window', 1_003_000 + 41_000)])
    expect(q.entries()[0].state).toBe('maybe')
    q.reconcile([user('ok', 'window', 1_003_000 - 1500)])
    expect(q.entries()).toHaveLength(0)
  })

  it('an id-carrying item is paired by id only, never by its text', async () => {
    const { calls, port } = fakePort()
    const q = new SendQueue(port)
    q.enqueue('words')
    await vi.advanceTimersByTimeAsync(UNDO_MS)
    calls[0].resolve({ kind: 'unknown' })
    await flush()
    q.reconcile([user('u1', 'words', 1_003_500, { client_msg_id: 'other-id' })])
    expect(q.entries()[0].state).toBe('maybe')
  })

  it('two identical messages sent apart each pair with their own echo (the nearest)', async () => {
    const { calls, port } = fakePort()
    const q = new SendQueue(port)
    q.enqueue('same') // started 1_003_000
    await vi.advanceTimersByTimeAsync(UNDO_MS)
    calls[0].resolve({ kind: 'accepted' })
    await flush()
    await vi.advanceTimersByTimeAsync(10_000)
    q.enqueue('same')
    await vi.advanceTimersByTimeAsync(UNDO_MS) // started 1_016_000
    calls[1].resolve({ kind: 'accepted' })
    await flush()
    q.reconcile([user('b', 'same', 1_016_500)]) // nearer to the second
    expect(q.entries().map((e) => e.startedAt)).toEqual([1_003_000])
    q.reconcile([user('b', 'same', 1_016_500), user('a', 'same', 1_003_400)])
    expect(q.entries()).toHaveLength(0)
  })

  it('one transcript message settles one entry, not two with the same text', async () => {
    const { calls, port } = fakePort()
    const q = new SendQueue(port)
    q.enqueue('dup')
    q.enqueue('dup')
    await vi.advanceTimersByTimeAsync(UNDO_MS)
    calls[0].resolve({ kind: 'accepted' })
    await flush()
    calls[1].resolve({ kind: 'accepted' })
    await flush()
    q.reconcile([user('u1', 'dup', 1_003_500)])
    expect(q.entries()).toHaveLength(1)
    q.reconcile([user('u1', 'dup', 1_003_500)])
    expect(q.entries()).toHaveLength(1)
  })

  it('a manual resend of a maybe-sent message is a NEW message: new client_msg_id, the old entry kept as superseded', async () => {
    const { calls, port } = fakePort()
    const q = new SendQueue(port)
    const id = q.enqueue('restart me')
    await vi.advanceTimersByTimeAsync(UNDO_MS)
    calls[0].resolve({ kind: 'network' })
    await flush()
    const next = q.resend(id)
    await flush()
    expect(next).toBeDefined()
    expect(next).not.toBe(id)
    expect(calls).toHaveLength(2)
    expect(calls[1]).toMatchObject({ id: next, text: 'restart me' })
    expect(q.entries().find((e) => e.id === id)).toMatchObject({ state: 'superseded', supersededBy: next })
    calls[1].resolve({ kind: 'accepted' })
    await flush()
    expect(q.entries().find((e) => e.id === next)?.state).toBe('sent')
  })

  it('a dropped message is resent under a new id and really calls submit again (the ledger would replay the drop for the old id)', async () => {
    const { calls, port } = fakePort()
    const q = new SendQueue(port)
    const id = q.enqueue('again')
    await vi.advanceTimersByTimeAsync(UNDO_MS)
    calls[0].resolve({ kind: 'dropped', reason: 'session_changed' })
    await flush()
    const next = q.resend(id)
    await flush()
    expect(calls).toHaveLength(2)
    expect(calls[1].id).toBe(next)
    expect(calls[1].id).not.toBe(calls[0].id)
  })

  it('only a maybe or failed entry can be resent', async () => {
    const { calls, port } = fakePort()
    const q = new SendQueue(port)
    const id = q.enqueue('x')
    expect(q.resend(id)).toBeUndefined() // still in its undo window
    await vi.advanceTimersByTimeAsync(UNDO_MS)
    calls[0].resolve({ kind: 'accepted' })
    await flush()
    expect(q.resend(id)).toBeUndefined()
  })
})

describe('failures', () => {
  it('dropped / timeout end the entry as failed, the next message still goes', async () => {
    const { calls, port } = fakePort()
    const q = new SendQueue(port)
    q.enqueue('a')
    q.enqueue('b')
    await vi.advanceTimersByTimeAsync(UNDO_MS)
    calls[0].resolve({ kind: 'dropped', reason: 'session_changed' })
    await flush()
    expect(q.entries()[0]).toMatchObject({ state: 'failed', outcome: { kind: 'dropped', reason: 'session_changed' } })
    expect(calls.map((c) => c.text)).toEqual(['a', 'b'])
  })

  it('no_mod fails the message and what is waiting behind it, and leaves nothing sticky: the next message is tried', async () => {
    const { calls, port } = fakePort()
    const q = new SendQueue(port)
    q.enqueue('a')
    q.enqueue('b')
    await vi.advanceTimersByTimeAsync(UNDO_MS)
    calls[0].resolve({ kind: 'no_mod' })
    await flush()
    expect(q.entries().map((e) => e.state)).toEqual(['failed', 'failed'])
    expect(calls).toHaveLength(1)
    q.enqueue('c') // the mod is back
    await vi.advanceTimersByTimeAsync(UNDO_MS)
    expect(calls.map((c) => c.text)).toEqual(['a', 'c'])
  })

  it('interrupt goes to the port', async () => {
    const { port } = fakePort()
    await new SendQueue(port).interrupt()
    expect(port.interrupt).toHaveBeenCalledTimes(1)
  })
})

describe('an idle observed while a request is in flight (the remount race)', () => {
  const busy = { kind: 'busy' } as SendOutcome

  it('is used once the busy answer lands: resent at once with the same id, not stuck in waiting', async () => {
    const { calls, port } = fakePort()
    const q = new SendQueue(port)
    const id = q.enqueue('hi')
    await vi.advanceTimersByTimeAsync(UNDO_MS)
    q.setIdle(true) // the remounted pane reports idle while the request is still out: setIdle has nothing waiting to resend
    calls[0].resolve(busy)
    await vi.advanceTimersByTimeAsync(0)
    expect(calls).toHaveLength(2)
    expect(calls[1].id).toBe(id)
  })

  it('is spent by that one resend: a second busy waits for a NEW observation (no loop)', async () => {
    const { calls, port } = fakePort()
    const q = new SendQueue(port)
    q.enqueue('hi')
    await vi.advanceTimersByTimeAsync(UNDO_MS)
    q.setIdle(true)
    calls[0].resolve(busy)
    await vi.advanceTimersByTimeAsync(0)
    calls[1].resolve(busy)
    await vi.advanceTimersByTimeAsync(UNDO_MS * 3)
    expect(calls).toHaveLength(2)
    expect(q.entries()[0].state).toBe('waiting')
    q.setIdle(true)
    await vi.advanceTimersByTimeAsync(0)
    expect(calls).toHaveLength(3)
  })

  it('an idle seen BEFORE the request started does not count, nor does one followed by not-idle, nor a settled one', async () => {
    const { calls, port } = fakePort()
    const q = new SendQueue(port)
    q.setIdle(true) // nothing in flight yet
    q.enqueue('a')
    await vi.advanceTimersByTimeAsync(UNDO_MS)
    calls[0].resolve(busy)
    await vi.advanceTimersByTimeAsync(0)
    expect(q.entries()[0].state).toBe('waiting')
    expect(calls).toHaveLength(1)
    const q2 = new SendQueue(port)
    q2.enqueue('b')
    await vi.advanceTimersByTimeAsync(UNDO_MS)
    q2.setIdle(true); q2.setIdle(false) // the header went busy again
    calls[calls.length - 1].resolve(busy)
    await vi.advanceTimersByTimeAsync(0)
    expect(q2.entries()[0].state).toBe('waiting')
  })

  it('the observation belongs to ITS entry: A in flight, idle seen, A echoed by the transcript, B goes out, A\'s answer lands, B answers busy -> B waits (no extra submit)', async () => {
    const { calls, port } = fakePort()
    const q = new SendQueue(port)
    const a = q.enqueue('A')
    await vi.advanceTimersByTimeAsync(UNDO_MS)
    q.enqueue('B')
    q.setIdle(true) // seen while A is in flight
    q.reconcile([user('ua', 'A', 1_000_000, { client_msg_id: a })]) // the echo settles A early; B is pumped (undo window 3 s)
    await vi.advanceTimersByTimeAsync(UNDO_MS)
    expect(calls.map((c) => c.text)).toEqual(['A', 'B'])
    calls[0].resolve({ kind: 'accepted' }) // A's late answer
    await vi.advanceTimersByTimeAsync(0)
    calls[1].resolve(busy) // B refused because A is running
    await vi.advanceTimersByTimeAsync(UNDO_MS * 3)
    expect(calls).toHaveLength(2)
    expect(q.entries().find((e) => e.text === 'B')!.state).toBe('waiting')
  })

  it('an observation does not leak into a later request after an accepted answer', async () => {
    const { calls, port } = fakePort()
    const q = new SendQueue(port)
    q.enqueue('a')
    await vi.advanceTimersByTimeAsync(UNDO_MS)
    q.setIdle(true)
    calls[0].resolve({ kind: 'accepted' })
    await vi.advanceTimersByTimeAsync(0)
    q.enqueue('b')
    await vi.advanceTimersByTimeAsync(UNDO_MS)
    calls[1].resolve(busy)
    await vi.advanceTimersByTimeAsync(0)
    expect(calls).toHaveLength(2)
    expect(q.entries().find((e) => e.text === 'b')!.state).toBe('waiting')
  })
})

describe('dispose (the pane is gone for good)', () => {
  it('drops what waits, clears the timer, and the answer of a message in flight starts nothing', async () => {
    const { calls, port } = fakePort()
    const q = new SendQueue(port)
    q.enqueue('first')
    await vi.advanceTimersByTimeAsync(UNDO_MS)
    q.enqueue('second')
    expect(calls).toHaveLength(1)
    q.dispose()
    expect(q.entries()).toEqual([])
    calls[0].resolve({ kind: 'accepted' })
    await vi.advanceTimersByTimeAsync(UNDO_MS * 3)
    expect(calls).toHaveLength(1)
    expect(vi.getTimerCount()).toBe(0)
  })

  it('a disposed queue sends nothing even if something still calls it', async () => {
    const { calls, port } = fakePort()
    const q = new SendQueue(port)
    q.dispose()
    q.enqueue('late')
    q.setIdle(true)
    await vi.advanceTimersByTimeAsync(UNDO_MS * 2)
    expect(calls).toHaveLength(0)
    expect(vi.getTimerCount()).toBe(0)
  })

  it('retire: undo and waiting entries are dropped and never sent; nothing in flight frees the queue at once', async () => {
    const { calls, port } = fakePort()
    const q = new SendQueue(port)
    const done = vi.fn()
    q.enqueue('in the undo window')
    q.retire(done)
    expect(done).toHaveBeenCalledTimes(1)
    expect(q.entries()).toEqual([])
    await vi.advanceTimersByTimeAsync(UNDO_MS * 3)
    expect(calls).toHaveLength(0)
    expect(vi.getTimerCount()).toBe(0)
  })

  it('retire: a message in flight runs to its end, the one behind it is dropped, then the queue frees itself (no resend of a busy answer)', async () => {
    const { calls, port } = fakePort()
    const q = new SendQueue(port)
    const done = vi.fn()
    q.enqueue('first')
    await vi.advanceTimersByTimeAsync(UNDO_MS)
    q.enqueue('second')
    q.retire(done)
    expect(done).not.toHaveBeenCalled()
    calls[0].resolve({ kind: 'busy' } as SendOutcome)
    await vi.advanceTimersByTimeAsync(0)
    q.setIdle(true)
    await vi.advanceTimersByTimeAsync(UNDO_MS * 3)
    expect(done).toHaveBeenCalledTimes(1)
    expect(calls).toHaveLength(1)
    expect(vi.getTimerCount()).toBe(0)
  })
})

describe('registry', () => {
  it('one queue per pane key, kept across calls', () => {
    const { port } = fakePort()
    expect(sendQueueFor('p', () => port)).toBe(sendQueueFor('p', () => port))
    expect(sendQueueFor('p', () => port)).not.toBe(sendQueueFor('q', () => port))
  })
})
