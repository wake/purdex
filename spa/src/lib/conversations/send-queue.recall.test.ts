// c9d9c7-59 pt 1: what the App still holds (the undo window, waiting for idle) can be taken back, all at once and in sending order, with the
// box it came from; what the driver has handed out cannot. The check and the removal are one synchronous step, so a message is taken OR
// handed out, never both. The previews live as long as the App holds the message.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { SendQueue, UNDO_MS, clearAllSendQueues } from './send-queue'
import type { SendOutcome, SendPort } from './send'
import type { Attachment } from './attachment-memory'

interface Call { text: string; id: string; resolve: (o: SendOutcome) => void }
function fakePort() {
  const calls: Call[] = []
  const port: SendPort = {
    submit: (text, id) => new Promise<SendOutcome>((resolve) => { calls.push({ text, id, resolve }) }),
    interrupt: vi.fn(() => Promise.resolve<SendOutcome>({ kind: 'accepted' })),
  }
  return { calls, port }
}
const chip = (id: string, seq: number): Attachment => ({ id, name: `${id}.png`, path: `/tmp/${id}.png`, text: `[Image #${seq}]`, image: true, previewUrl: `blob:${id}`, size: 1, seq })
const revoke = vi.fn()

beforeEach(() => { vi.useFakeTimers(); vi.setSystemTime(1_000_000); revoke.mockReset(); Object.assign(URL, { revokeObjectURL: revoke }) })
afterEach(() => { vi.useRealTimers(); clearAllSendQueues() })

describe('taking back what the App holds', () => {
  it('takes every held message, oldest first, with the box each came from', () => {
    const q = new SendQueue(fakePort().port)
    q.enqueue('one', { draft: 'one [Image #1]', chips: [chip('a', 1)] })
    q.enqueue('two', { draft: 'two', chips: [] })
    expect(q.heldCount()).toBe(2)
    const taken = q.takeAllHeld()
    expect(taken.map((x) => x.text)).toEqual(['one', 'two'])
    expect(taken[0].held).toEqual({ draft: 'one [Image #1]', chips: [chip('a', 1)] })
    expect(q.entries()).toEqual([])
    expect(q.heldCount()).toBe(0)
  })

  it('a message taken back is never sent afterwards (the undo timer goes with it)', async () => {
    const { calls, port } = fakePort()
    const q = new SendQueue(port)
    q.enqueue('gone')
    q.takeAllHeld()
    await vi.advanceTimersByTimeAsync(UNDO_MS * 3)
    expect(calls).toEqual([])
  })

  it('a message already handed out cannot be taken, and a held one behind it can', async () => {
    const { calls, port } = fakePort()
    const q = new SendQueue(port)
    q.enqueue('first')
    await vi.advanceTimersByTimeAsync(UNDO_MS) // handed out
    q.enqueue('second')
    expect(calls.map((c) => c.text)).toEqual(['first'])
    expect(q.takeAllHeld().map((x) => x.text)).toEqual(['second'])
    expect(q.entries().map((e) => [e.text, e.state])).toEqual([['first', 'sending']])
    expect(q.takeAllHeld()).toEqual([])
  })

  it('a message the mod refused as busy (waiting for idle) is still the App\'s and can be taken', async () => {
    const { calls, port } = fakePort()
    const q = new SendQueue(port)
    q.enqueue('busy one')
    await vi.advanceTimersByTimeAsync(UNDO_MS)
    calls[0].resolve({ kind: 'busy' })
    await vi.advanceTimersByTimeAsync(0)
    expect(q.entries()[0].state).toBe('waiting')
    expect(q.takeAllHeld().map((x) => x.text)).toEqual(['busy one'])
  })

  it('accepted (waiting for the transcript) and maybe / failed entries are not taken', async () => {
    const { calls, port } = fakePort()
    const q = new SendQueue(port)
    q.enqueue('a')
    await vi.advanceTimersByTimeAsync(UNDO_MS)
    calls[0].resolve({ kind: 'accepted' })
    await vi.advanceTimersByTimeAsync(0)
    q.enqueue('b')
    await vi.advanceTimersByTimeAsync(UNDO_MS)
    calls[1].resolve({ kind: 'unknown' })
    await vi.advanceTimersByTimeAsync(0)
    expect(q.entries().map((e) => e.state)).toEqual(['sent', 'maybe'])
    expect(q.takeAllHeld()).toEqual([])
  })

  it('the single Undo takes one entry only', () => {
    const q = new SendQueue(fakePort().port)
    const a = q.enqueue('a')
    q.enqueue('b')
    expect(q.takeBack(a)?.text).toBe('a')
    expect(q.entries().map((e) => e.text)).toEqual(['b'])
    expect(q.takeBack(a)).toBeUndefined()
  })
})

describe('the previews of a held box', () => {
  it('stay alive while the App holds the message and while it is taken back (the caller owns them then)', () => {
    const q = new SendQueue(fakePort().port)
    q.enqueue('x', { draft: '[Image #1]', chips: [chip('a', 1)] })
    expect(revoke).not.toHaveBeenCalled()
    q.takeAllHeld()
    expect(revoke).not.toHaveBeenCalled()
  })

  it('stay alive while the message is in flight and waiting for idle (it can still come back whole), and go once the mod takes it', async () => {
    const { calls, port } = fakePort()
    const q = new SendQueue(port)
    q.enqueue('x', { draft: '[Image #1]', chips: [chip('a', 1)] })
    await vi.advanceTimersByTimeAsync(UNDO_MS)
    expect(q.entries()[0].state).toBe('sending')
    expect(revoke).not.toHaveBeenCalled()
    calls[0].resolve({ kind: 'busy' })
    await vi.advanceTimersByTimeAsync(0)
    expect(q.entries()[0].state).toBe('waiting')
    expect(revoke).not.toHaveBeenCalled()
    const taken = q.takeAllHeld() // a recall of a waiting message brings the chips back too
    expect(taken[0].held?.chips.map((a) => a.id)).toEqual(['a'])
    expect(revoke).not.toHaveBeenCalled()
  })

  it('are let go exactly once when the mod accepts the message', async () => {
    const { calls, port } = fakePort()
    const q = new SendQueue(port)
    q.enqueue('x', { draft: '[Image #1]', chips: [chip('a', 1)] })
    await vi.advanceTimersByTimeAsync(UNDO_MS)
    calls[0].resolve({ kind: 'accepted' })
    await vi.advanceTimersByTimeAsync(0)
    expect(revoke).toHaveBeenCalledTimes(1)
    expect(revoke).toHaveBeenCalledWith('blob:a')
    expect(q.entries()[0].held).toBeUndefined()
  })

  it('are let go exactly once when the transcript echoes a message that is still in flight', async () => {
    const { port } = fakePort()
    const q = new SendQueue(port)
    const id = q.enqueue('x', { draft: '[Image #1]', chips: [chip('a', 1)] })
    await vi.advanceTimersByTimeAsync(UNDO_MS)
    q.reconcile([{ id: 'u1', type: 'user', text: 'x', at: Date.now(), index: 0, source: 'user', client_msg_id: id }])
    expect(revoke).toHaveBeenCalledTimes(1)
  })

  it('are let go exactly once when the queue is retired with the message held', () => {
    const q = new SendQueue(fakePort().port)
    q.enqueue('x', { draft: '[Image #1]', chips: [chip('a', 1)] })
    q.retire(() => {})
    expect(revoke).toHaveBeenCalledTimes(1)
  })

  it('are let go when the queue is disposed with the message still held', () => {
    const q = new SendQueue(fakePort().port)
    q.enqueue('x', { draft: '[Image #1]', chips: [chip('a', 1)] })
    q.dispose()
    expect(revoke).toHaveBeenCalledTimes(1)
    expect(revoke).toHaveBeenCalledWith('blob:a')
  })

  it('a caller that only wants the text (undo) lets them go', () => {
    const q = new SendQueue(fakePort().port)
    const id = q.enqueue('x', { draft: '[Image #1]', chips: [chip('a', 1)] })
    expect(q.undo(id)).toBe('x')
    expect(revoke).toHaveBeenCalledWith('blob:a')
  })
})

describe('recallHeld without an input to take it', () => {
  it('leaves the message where it is (no listener for the key: nothing is taken)', async () => {
    const { recallHeld, subscribeRecall } = await import('./draft-recall')
    const q = new SendQueue(fakePort().port)
    const id = q.enqueue('stay')
    expect(recallHeld('k', q, id)).toBe(false)
    expect(q.heldCount()).toBe(1)
    const off = subscribeRecall('k', () => {})
    expect(recallHeld('k', q, id)).toBe(true)
    expect(q.heldCount()).toBe(0)
    off()
  })
})
