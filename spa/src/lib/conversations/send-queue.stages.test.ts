// spa/src/lib/conversations/send-queue.stages.test.ts — the send queue driven by the daemon's message stages (message-stages plan §1.1, L1, L2; MS2-3):
// one entry per message, keyed by its first client_msg_id, whose stage is the stage of its newest id that has one; `read` hands it over without a
// second copy from Claude Code's queue line; without stages the queue is today's.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { READ_GRACE_MS, SendQueue, UNDO_MS, clearAllSendQueues } from './send-queue'
import type { SendOutcome, SendPort } from './send'
import type { Stage, UserItem } from './types'

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
const st = (client_msg_id: string, stage: string, over: Partial<Stage> = {}): Stage => ({ client_msg_id, stage, at: 1_003_000, ...over })
const byId = (...list: Stage[]): Record<string, Stage> => Object.fromEntries(list.map((s) => [s.client_msg_id, s]))
const user = (id: string, text: string, at: number, extra: Partial<UserItem> = {}): UserItem => ({ id, type: 'user', text, at, index: 0, source: 'user', ...extra })

beforeEach(() => { vi.useFakeTimers(); vi.setSystemTime(1_000_000) })
afterEach(() => { vi.useRealTimers(); clearAllSendQueues() })

/** A message that went out (handed, fill mode) and sits in the queue. */
async function handedOne(q: SendQueue, calls: Call[], text = 'hi'): Promise<string> {
  const id = q.enqueue(text)
  await vi.advanceTimersByTimeAsync(UNDO_MS)
  calls[calls.length - 1].resolve({ kind: 'handed', mode: 'fill' })
  await flush()
  return id
}

describe('the stage of an entry', () => {
  it('a stage for the entry\'s id shows on the entry; one bubble, keyed by the first id', async () => {
    const { calls, port } = fakePort()
    const q = new SendQueue(port)
    const id = await handedOne(q, calls)
    q.setStages(byId(st(id, 'entered', { mode: 'fill' })))
    expect(q.entries()).toHaveLength(1)
    expect(q.entries()[0]).toMatchObject({ id, stage: { stage: 'entered', mode: 'fill' } })
    q.setStages(byId(st(id, 'queued')))
    expect(q.entries()).toHaveLength(1)
    expect(q.entries()[0].stage?.stage).toBe('queued')
  })

  it('a busy retry\'s stage (its newer id) moves the entry of the first id; the first id\'s terminal failed/busy stops counting', async () => {
    const { calls, port } = fakePort()
    const q = new SendQueue(port)
    const id = q.enqueue('hi')
    await vi.advanceTimersByTimeAsync(UNDO_MS)
    calls[0].resolve({ kind: 'refused', reason: 'busy', status: 429 })
    await flush()
    await vi.advanceTimersByTimeAsync(1000) // the retry goes out under a new id
    expect(calls).toHaveLength(2)
    const retryId = calls[1].id
    expect(retryId).not.toBe(id)
    q.setStages(byId(st(id, 'failed', { reason: 'busy' })))
    expect(q.entries()[0].stage).toMatchObject({ stage: 'failed', reason: 'busy' })
    q.setStages(byId(st(id, 'failed', { reason: 'busy' }), st(retryId, 'received')))
    expect(q.entries()).toHaveLength(1)
    expect(q.entries()[0].id).toBe(id)
    expect(q.entries()[0].stage?.stage).toBe('received')
  })

  it('an entry whose ids have no stage keeps today\'s path (no stage set; the header claim hides it)', async () => {
    const { calls, port } = fakePort()
    const q = new SendQueue(port)
    await handedOne(q, calls)
    q.setStages(byId(st('someone-elses-id', 'queued')))
    expect(q.entries()[0].stage).toBeUndefined()
    q.setHeaderQueue([{ text: 'hi', at: 1_002_000 }])
    expect(q.entries()[0].matched).toBe(true)
    expect(q.coveredRows()).toEqual([])
  })

  it('setting the same stages again changes nothing (no emit)', async () => {
    const { calls, port } = fakePort()
    const q = new SendQueue(port)
    const id = await handedOne(q, calls)
    const on = vi.fn()
    q.setStages(byId(st(id, 'queued')))
    q.subscribe(on)
    q.setStages(byId(st(id, 'queued')))
    expect(on).not.toHaveBeenCalled()
  })
})

describe('the hand-over to Claude Code\'s queue line and to the transcript', () => {
  it('a staged entry that claimed a header row stays the one bubble: it is shown, its row is covered', async () => {
    const { calls, port } = fakePort()
    const q = new SendQueue(port)
    const id = await handedOne(q, calls)
    q.setStages(byId(st(id, 'queued')))
    q.setHeaderQueue([{ text: 'hi', at: 1_002_000 }])
    expect(q.entries()[0]).toMatchObject({ matched: true, stage: { stage: 'queued' } })
    expect(q.coveredRows()).toEqual([{ text: 'hi', at: 1_002_000 }])
  })

  it('read leaves the entry without a mark for a moment, then it goes; its row stays covered while the header still lists it', async () => {
    const { calls, port } = fakePort()
    const q = new SendQueue(port)
    const id = await handedOne(q, calls)
    q.setStages(byId(st(id, 'queued')))
    q.setHeaderQueue([{ text: 'hi', at: 1_002_000 }])
    q.setStages(byId(st(id, 'read')))
    expect(q.entries()[0].stage?.stage).toBe('read')
    await vi.advanceTimersByTimeAsync(READ_GRACE_MS)
    expect(q.entries()).toHaveLength(0)
    expect(q.coveredRows()).toEqual([{ text: 'hi', at: 1_002_000 }]) // the header has not caught up: no second copy
    q.setHeaderQueue([])
    expect(q.coveredRows()).toEqual([])
  })

  it('the transcript\'s echo of a read entry takes it at once, and its row stays covered', async () => {
    const { calls, port } = fakePort()
    const q = new SendQueue(port)
    const id = await handedOne(q, calls)
    q.setStages(byId(st(id, 'read')))
    q.setHeaderQueue([{ text: 'hi', at: 1_002_000 }])
    q.reconcile([user('u1', 'hi', 1_004_000, { client_msg_id: id })])
    expect(q.entries()).toHaveLength(0)
    expect(q.coveredRows()).toEqual([{ text: 'hi', at: 1_002_000 }])
  })

  it('recalled drops the entry (Claude Code\'s whole queue was taken back by someone)', async () => {
    const { calls, port } = fakePort()
    const q = new SendQueue(port)
    const id = await handedOne(q, calls)
    q.setStages(byId(st(id, 'queued')))
    q.setStages(byId(st(id, 'recalled')))
    expect(q.entries()).toHaveLength(0)
  })
})

describe('a host that reports stages: confirmed by id or by read, never by text', () => {
  it('a terminal-typed twin (same text, no client_msg_id) does not settle the older pending message; without stages the text still does', async () => {
    const { calls, port } = fakePort()
    const q = new SendQueue(port)
    q.setStagesMode(true)
    const id = await handedOne(q, calls)
    q.reconcile([user('u1', 'hi', 1_003_000)])
    expect(q.entries().map((e) => e.id)).toEqual([id])
    q.setStagesMode(false)
    q.reconcile([user('u1', 'hi', 1_003_000)])
    expect(q.entries()).toHaveLength(0)
  })

  it('its own client_msg_id on a transcript item still settles it', async () => {
    const { calls, port } = fakePort()
    const q = new SendQueue(port)
    q.setStagesMode(true)
    const id = await handedOne(q, calls)
    q.reconcile([user('u1', 'hi', 1_003_000, { client_msg_id: id })])
    expect(q.entries()).toHaveLength(0)
  })

  it('stages that disappear from the snapshot (the daemon restarted, the ledger is empty) leave the message 「不確定有沒有送到」, not confirmed', async () => {
    const { calls, port } = fakePort()
    const q = new SendQueue(port)
    q.setStagesMode(true)
    const id = await handedOne(q, calls)
    q.setStages(byId(st(id, 'queued')))
    q.setStages({})
    expect(q.entries()[0]).toMatchObject({ state: 'maybe', stage: { stage: 'unknown', reason: 'ledger_lost' } })
    q.reconcile([user('u1', 'hi', 1_003_000)])
    expect(q.entries()).toHaveLength(1) // and the text does not settle it either
  })

  it('without stages mode a vanished stage list changes nothing', async () => {
    const { calls, port } = fakePort()
    const q = new SendQueue(port)
    const id = await handedOne(q, calls)
    q.setStages(byId(st(id, 'queued')))
    q.setStages({})
    expect(q.entries()[0].stage?.stage).toBe('queued')
  })
})
