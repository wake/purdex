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
  it('holds a message for 1.5 s before it goes out', async () => {
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
    await vi.advanceTimersByTimeAsync(1000)
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

describe('busy: retried with a NEW id each time (nothing was sent)', () => {
  const busy: SendOutcome = { kind: 'refused', reason: 'busy', status: 429 }

  it('retries after 1 s, 2 s, 4 s with three different new ids, then the entry fails with the busy refusal', async () => {
    const { calls, port } = fakePort()
    const q = new SendQueue(port)
    q.enqueue('later')
    await vi.advanceTimersByTimeAsync(UNDO_MS)
    expect(calls).toHaveLength(1)
    calls[0].resolve(busy); await flush()
    expect(q.entries()[0].state).toBe('sending') // still pending, not failed, not waiting for anything
    await vi.advanceTimersByTimeAsync(999)
    expect(calls).toHaveLength(1)
    await vi.advanceTimersByTimeAsync(1)
    expect(calls).toHaveLength(2)
    calls[1].resolve(busy); await flush()
    await vi.advanceTimersByTimeAsync(1999)
    expect(calls).toHaveLength(2)
    await vi.advanceTimersByTimeAsync(1)
    expect(calls).toHaveLength(3)
    calls[2].resolve(busy); await flush()
    await vi.advanceTimersByTimeAsync(3999)
    expect(calls).toHaveLength(3)
    await vi.advanceTimersByTimeAsync(1)
    expect(calls).toHaveLength(4)
    expect(new Set(calls.map((c) => c.id)).size).toBe(4) // four attempts, four different ids
    calls[3].resolve(busy); await flush()
    expect(q.entries()[0]).toMatchObject({ state: 'failed', outcome: busy })
    await vi.advanceTimersByTimeAsync(60_000)
    expect(calls).toHaveLength(4)
    expect(vi.getTimerCount()).toBe(0)
  })

  it('the entry keeps its identity and every id it used; an echo carrying an EARLIER id settles it', async () => {
    const { calls, port } = fakePort()
    const q = new SendQueue(port)
    const id = q.enqueue('later')
    await vi.advanceTimersByTimeAsync(UNDO_MS)
    calls[0].resolve(busy); await flush()
    await vi.advanceTimersByTimeAsync(1000)
    expect(q.entries()).toHaveLength(1)
    expect(q.entries()[0].id).toBe(id)
    expect(q.entries()[0].ids).toEqual([calls[0].id, calls[1].id])
    calls[1].resolve({ kind: 'handed', mode: 'fill' }); await flush()
    q.reconcile([user('u1', 'later', Date.now(), { client_msg_id: calls[0].id })]) // the echo of the FIRST attempt
    expect(q.entries()).toHaveLength(0)
  })

  it('a retry answered unknown / network is not resent; refused text_left_in_terminal is maybe', async () => {
    for (const outcome of [{ kind: 'unknown' }, { kind: 'network' }, { kind: 'refused', reason: 'text_left_in_terminal', status: 409 }] as SendOutcome[]) {
      const { calls, port } = fakePort()
      const q = new SendQueue(port)
      q.enqueue('x')
      await vi.advanceTimersByTimeAsync(UNDO_MS)
      calls[0].resolve(busy); await flush()
      await vi.advanceTimersByTimeAsync(1000)
      calls[1].resolve(outcome); await flush()
      await vi.advanceTimersByTimeAsync(120_000)
      expect(calls).toHaveLength(2)
      expect(q.entries()[0].state).toBe('maybe')
      q.dispose()
    }
  })

  it('a message behind a retrying one waits for it (order is kept)', async () => {
    const { calls, port } = fakePort()
    const q = new SendQueue(port)
    q.enqueue('a')
    q.enqueue('b')
    await vi.advanceTimersByTimeAsync(UNDO_MS)
    calls[0].resolve(busy); await flush()
    expect(calls.map((c) => c.text)).toEqual(['a'])
    await vi.advanceTimersByTimeAsync(1000)
    calls[1].resolve({ kind: 'handed', mode: 'fill' }); await flush()
    expect(calls.map((c) => c.text)).toEqual(['a', 'a', 'b'])
  })

  it('every other refusal fails at once, with no retry', async () => {
    for (const reason of ['input_not_empty', 'dialog_open', 'pane_busy', 'waiting', 'generation', 'no_owner_yet']) {
      const { calls, port } = fakePort()
      const q = new SendQueue(port)
      q.enqueue('x')
      await vi.advanceTimersByTimeAsync(UNDO_MS)
      calls[0].resolve({ kind: 'refused', reason, status: 409 }); await flush()
      expect(q.entries()[0].state).toBe('failed')
      await vi.advanceTimersByTimeAsync(60_000)
      expect(calls).toHaveLength(1)
      q.dispose()
    }
  })

  it('an echo while the retry waits removes the entry and its timer; undo / dispose / retire leave no timer', async () => {
    const { calls, port } = fakePort()
    const q = new SendQueue(port)
    q.enqueue('x')
    await vi.advanceTimersByTimeAsync(UNDO_MS)
    calls[0].resolve(busy); await flush()
    q.reconcile([user('u1', 'x', Date.now(), { client_msg_id: calls[0].id })])
    expect(q.entries()).toHaveLength(0)
    await vi.advanceTimersByTimeAsync(10_000)
    expect(calls).toHaveLength(1)
    expect(vi.getTimerCount()).toBe(0)
  })

  it('retire drops an entry that waits to retry (no request in flight) and frees the queue', async () => {
    const { calls, port } = fakePort()
    const q = new SendQueue(port)
    const done = vi.fn()
    q.enqueue('x')
    await vi.advanceTimersByTimeAsync(UNDO_MS)
    calls[0].resolve(busy); await flush()
    q.retire(done)
    expect(done).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(10_000)
    expect(calls).toHaveLength(1)
    expect(vi.getTimerCount()).toBe(0)
  })

  it('dispose while a retry waits sends nothing', async () => {
    const { calls, port } = fakePort()
    const q = new SendQueue(port)
    q.enqueue('x')
    await vi.advanceTimersByTimeAsync(UNDO_MS)
    calls[0].resolve(busy); await flush()
    q.dispose()
    await vi.advanceTimersByTimeAsync(10_000)
    expect(calls).toHaveLength(1)
    expect(vi.getTimerCount()).toBe(0)
  })
})

describe('handed: seen on Claude Code\'s queue line, or echoed, or maybe after 10 s', () => {
  const handed: SendOutcome = { kind: 'handed', mode: 'fill' }
  const sendHanded = async (q: SendQueue, calls: Call[], text = 'hi') => {
    q.enqueue(text)
    await vi.advanceTimersByTimeAsync(UNDO_MS)
    calls[calls.length - 1].resolve(handed); await flush()
  }

  it('handed + 10 s with neither a rendered line nor an echo -> maybe (never resent)', async () => {
    const { calls, port } = fakePort()
    const q = new SendQueue(port)
    await sendHanded(q, calls)
    expect(q.entries()[0]).toMatchObject({ state: 'sent', mode: 'fill' })
    await vi.advanceTimersByTimeAsync(9999)
    expect(q.entries()[0].state).toBe('sent')
    await vi.advanceTimersByTimeAsync(1)
    expect(q.entries()[0].state).toBe('maybe')
    await vi.advanceTimersByTimeAsync(60_000)
    expect(calls).toHaveLength(1)
  })

  it('an echo within 10 s settles it and takes the timer', async () => {
    const { calls, port } = fakePort()
    const q = new SendQueue(port)
    await sendHanded(q, calls)
    q.reconcile([user('u1', 'hi', Date.now(), { client_msg_id: calls[0].id })])
    expect(q.entries()).toHaveLength(0)
    expect(vi.getTimerCount()).toBe(0)
  })

  it('a rendered line with the same text (trailing whitespace trimmed) hides the entry; it is not maybe at 10 s while the line stands', async () => {
    const { calls, port } = fakePort()
    const q = new SendQueue(port)
    await sendHanded(q, calls)
    q.setHeaderQueue(['hi  \n'])
    expect(q.entries()[0].matched).toBe(true)
    await vi.advanceTimersByTimeAsync(60_000)
    expect(q.entries()[0]).toMatchObject({ state: 'sent', matched: true })
  })

  it('the line stops being rendered without an echo: the entry is shown again, and it is maybe at once when past 10 s', async () => {
    const { calls, port } = fakePort()
    const q = new SendQueue(port)
    await sendHanded(q, calls)
    q.setHeaderQueue(['hi'])
    await vi.advanceTimersByTimeAsync(2000)
    q.setHeaderQueue([]) // e.g. the conversation went idle: the header line is not rendered
    expect(q.entries()[0]).toMatchObject({ state: 'sent', matched: false })
    q.setHeaderQueue(['hi'])
    await vi.advanceTimersByTimeAsync(20_000)
    expect(q.entries()[0].matched).toBe(true)
    q.setHeaderQueue([])
    expect(q.entries()[0]).toMatchObject({ state: 'maybe', matched: false })
  })

  it('lines pair one to one: two identical entries need two lines', async () => {
    const { calls, port } = fakePort()
    const q = new SendQueue(port)
    await sendHanded(q, calls, 'same')
    await sendHanded(q, calls, 'same')
    q.setHeaderQueue(['same'])
    expect(q.entries().map((e) => !!e.matched)).toEqual([true, false])
    q.setHeaderQueue(['same', 'same'])
    expect(q.entries().map((e) => e.matched)).toEqual([true, true])
  })

  it('only a handed entry is matched: one still in flight, in the buffer or accepted by a worker is not', async () => {
    const { calls, port } = fakePort()
    const q = new SendQueue(port)
    q.enqueue('buffered')
    q.setHeaderQueue(['buffered'])
    expect(q.entries()[0].matched).toBeFalsy()
    await vi.advanceTimersByTimeAsync(UNDO_MS)
    q.setHeaderQueue(['buffered'])
    expect(q.entries()[0]).toMatchObject({ state: 'sending' })
    expect(q.entries()[0].matched).toBeFalsy()
    calls[0].resolve({ kind: 'accepted' }); await flush()
    q.setHeaderQueue(['buffered'])
    expect(q.entries()[0].matched).toBeFalsy()
    await vi.advanceTimersByTimeAsync(60_000)
    expect(q.entries()[0].state).toBe('sent') // a worker's accepted has no 10 s rule
  })

  it('setHeaderQueue is idempotent: the same lines again emit nothing', async () => {
    const { calls, port } = fakePort()
    const q = new SendQueue(port)
    await sendHanded(q, calls)
    q.setHeaderQueue(['hi'])
    const seen = vi.fn()
    q.subscribe(seen)
    q.setHeaderQueue(['hi'])
    q.setHeaderQueue(['hi'])
    expect(seen).not.toHaveBeenCalled()
  })

  it('keys mode is kept on the entry', async () => {
    const { calls, port } = fakePort()
    const q = new SendQueue(port)
    q.enqueue('k')
    await vi.advanceTimersByTimeAsync(UNDO_MS)
    calls[0].resolve({ kind: 'handed', mode: 'keys' }); await flush()
    expect(q.entries()[0]).toMatchObject({ state: 'sent', mode: 'keys' })
  })
})

describe('unknown: at most once', () => {
  it.each<SendOutcome>([{ kind: 'unknown', reason: 'no_result' }, { kind: 'network' }, { kind: 'refused', reason: 'text_left_in_terminal', status: 409 }])('%j is never resent by itself', async (outcome) => {
    const { calls, port } = fakePort()
    const q = new SendQueue(port)
    q.enqueue('maybe sent')
    await vi.advanceTimersByTimeAsync(UNDO_MS)
    calls[0].resolve(outcome)
    await flush()
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
    await vi.advanceTimersByTimeAsync(UNDO_MS) // startedAt = 1_001_500
    calls[0].resolve({ kind: 'unknown' })
    await flush()
    q.reconcile([user('early', 'window', 1_001_500 - 3000)])
    expect(q.entries()[0].state).toBe('maybe')
    q.reconcile([user('late', 'window', 1_001_500 + 41_000)])
    expect(q.entries()[0].state).toBe('maybe')
    q.reconcile([user('ok', 'window', 1_001_500 - 1500)])
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
    q.enqueue('same') // started 1_001_500
    await vi.advanceTimersByTimeAsync(UNDO_MS)
    calls[0].resolve({ kind: 'accepted' })
    await flush()
    await vi.advanceTimersByTimeAsync(10_000)
    q.enqueue('same')
    await vi.advanceTimersByTimeAsync(UNDO_MS) // started 1_013_000
    calls[1].resolve({ kind: 'accepted' })
    await flush()
    q.reconcile([user('b', 'same', 1_013_500)]) // nearer to the second
    expect(q.entries().map((e) => e.startedAt)).toEqual([1_001_500])
    q.reconcile([user('b', 'same', 1_013_500), user('a', 'same', 1_001_900)])
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

  it('a refused message is resent under a new id and really calls submit again (the ledger would replay the refusal for the old id)', async () => {
    const { calls, port } = fakePort()
    const q = new SendQueue(port)
    const id = q.enqueue('again')
    await vi.advanceTimersByTimeAsync(UNDO_MS)
    calls[0].resolve({ kind: 'refused', reason: 'dialog_open', status: 409 })
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
  it('a refusal ends the entry as failed, the next message still goes', async () => {
    const { calls, port } = fakePort()
    const q = new SendQueue(port)
    q.enqueue('a')
    q.enqueue('b')
    await vi.advanceTimersByTimeAsync(UNDO_MS)
    calls[0].resolve({ kind: 'refused', reason: 'input_not_empty', status: 409 })
    await flush()
    expect(q.entries()[0]).toMatchObject({ state: 'failed', outcome: { kind: 'refused', reason: 'input_not_empty' } })
    expect(calls.map((c) => c.text)).toEqual(['a', 'b'])
  })

  it('a failure of one message does not fail the ones behind it', async () => {
    const { calls, port } = fakePort()
    const q = new SendQueue(port)
    q.enqueue('a')
    q.enqueue('b')
    await vi.advanceTimersByTimeAsync(UNDO_MS)
    calls[0].resolve({ kind: 'refused', reason: 'waiting', status: 409 })
    await flush()
    expect(q.entries().map((e) => e.state)).toEqual(['failed', 'sending'])
    expect(calls.map((c) => c.text)).toEqual(['a', 'b'])
  })

  it('interrupt goes to the port', async () => {
    const { port } = fakePort()
    await new SendQueue(port).interrupt()
    expect(port.interrupt).toHaveBeenCalledTimes(1)
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
    await vi.advanceTimersByTimeAsync(UNDO_MS * 2)
    expect(calls).toHaveLength(0)
    expect(vi.getTimerCount()).toBe(0)
  })

  it('retire: send-buffer entries are dropped and never sent; nothing in flight frees the queue at once', async () => {
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

  it('retire: a message in flight runs to its end, the one behind it is dropped, then the queue frees itself (no retry of a busy answer)', async () => {
    const { calls, port } = fakePort()
    const q = new SendQueue(port)
    const done = vi.fn()
    q.enqueue('first')
    await vi.advanceTimersByTimeAsync(UNDO_MS)
    q.enqueue('second')
    q.retire(done)
    expect(done).not.toHaveBeenCalled()
    calls[0].resolve({ kind: 'refused', reason: 'busy', status: 429 })
    await vi.advanceTimersByTimeAsync(0)
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
