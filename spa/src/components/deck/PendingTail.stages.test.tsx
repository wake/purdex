// spa/src/components/deck/PendingTail.stages.test.tsx — the marks a pending message wears for the daemon's stages, and one bubble per message across
// the hand-over to Claude Code's queue line and to the transcript (message-stages plan §1.4, MS2-3).
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { act, cleanup, render, screen } from '@testing-library/react'
import { PendingTail } from './PendingTail'
import { QueuedMessages } from './QueuedMessages'
import { clearAllSendQueues, sendQueueFor, UNDO_MS, READ_GRACE_MS, type QueueEntry } from '../../lib/conversations/send-queue'
import { draftKey } from '../../lib/conversations/draft-memory'
import type { SendOutcome, SendPort } from '../../lib/conversations/send'
import type { Stage } from '../../lib/conversations/types'

const KEY = draftKey('p', 'h', 's')
const ROW = { text: 'hi', at: 1_002_000 }
const resolvers: Array<(o: SendOutcome) => void> = []
const port: SendPort = {
  submit: () => new Promise<SendOutcome>((resolve) => { resolvers.push(resolve) }),
  interrupt: () => Promise.resolve<SendOutcome>({ kind: 'accepted' }),
}
const st = (client_msg_id: string, stage: string, over: Partial<Stage> = {}): Stage => ({ client_msg_id, stage, at: 1_003_000, ...over })

beforeEach(() => { vi.useFakeTimers(); vi.setSystemTime(1_000_000); resolvers.length = 0 })
afterEach(() => { cleanup(); vi.useRealTimers(); clearAllSendQueues() })

async function handed() {
  const q = sendQueueFor(KEY, () => port)
  const id = q.enqueue('hi')
  await vi.advanceTimersByTimeAsync(UNDO_MS)
  resolvers[0]({ kind: 'handed', mode: 'fill' })
  await vi.advanceTimersByTimeAsync(0)
  return { q, id }
}
const tail = (q: ReturnType<typeof sendQueueFor>, queued: Array<{ text: string; at: number; source: string }> = []) =>
  <PendingTail paneKey="p" hostId="h" sessionId="s" entries={q.entries()} queued={queued} />

describe('marks', () => {
  it.each([
    ['received', 'At the host'],
    ['filled', 'At the host'],
    ['entered', 'At the host'],
    ['queued', 'Queued'],
  ])('%s → 「%s」', async (stage, text) => {
    const { q, id } = await handed()
    act(() => q.setStages({ [id]: st(id, stage) }))
    render(tail(q))
    expect(screen.getByTestId('stage-mark')).toHaveTextContent(text)
  })
  it('failed names its reason, unknown says it is not sure', async () => {
    const { q, id } = await handed()
    act(() => q.setStages({ [id]: st(id, 'failed', { reason: 'waiting' }) }))
    const { unmount } = render(tail(q))
    expect(screen.getByTestId('stage-mark')).toHaveTextContent('Not sent: waiting')
    unmount()
    act(() => q.setStages({ [id]: st(id, 'unknown', { reason: 'x' }) }))
    render(tail(q))
    expect(screen.getByTestId('stage-mark')).toHaveTextContent('Not sure it arrived')
  })
  it('read has no mark: it is a normal message', async () => {
    const { q, id } = await handed()
    act(() => q.setStages({ [id]: st(id, 'read') }))
    render(tail(q))
    expect(screen.getByTestId('queued-message')).toBeTruthy()
    expect(screen.queryByTestId('stage-mark')).toBeNull()
  })
})

describe('one bubble', () => {
  it('a staged entry that Claude Code\'s queue line also lists shows once: the entry, not the row', async () => {
    const { q, id } = await handed()
    act(() => { q.setStages({ [id]: st(id, 'queued') }); q.setHeaderQueue([ROW]) })
    render(tail(q, [{ ...ROW, source: 'user' }]))
    expect(screen.getAllByTestId('queued-message')).toHaveLength(1)
    expect(screen.queryByTestId('header-queued')).toBeNull()
  })
  it('without stages the row stands for the entry, as before (the fallback)', async () => {
    const { q } = await handed()
    act(() => q.setHeaderQueue([ROW]))
    render(tail(q, [{ ...ROW, source: 'user' }]))
    expect(screen.queryByTestId('queued-message')).toBeNull()
    expect(screen.getAllByTestId('header-queued')).toHaveLength(1)
  })
  it('read hands over with no second copy: the entry goes and the header row it stood for stays hidden until the header lets it go', async () => {
    const { q, id } = await handed()
    act(() => { q.setStages({ [id]: st(id, 'queued') }); q.setHeaderQueue([ROW]) })
    act(() => q.setStages({ [id]: st(id, 'read') }))
    await act(async () => { await vi.advanceTimersByTimeAsync(READ_GRACE_MS) })
    const { rerender } = render(tail(q, [{ ...ROW, source: 'user' }]))
    expect(screen.queryByTestId('queued-message')).toBeNull()
    expect(screen.queryByTestId('header-queued')).toBeNull()
    act(() => q.setHeaderQueue([]))
    rerender(tail(q, []))
    expect(screen.queryByTestId('pending-tail')).toBeNull()
  })
})

describe('QueuedMessages alone', () => {
  it('a matched entry without a stage is still hidden', () => {
    const e = { id: 'a', ids: ['a'], text: 'hi', state: 'sent', mode: 'fill', matched: true, createdAt: 0, undoUntil: 0, startedAt: 0 } as QueueEntry
    render(<QueuedMessages entries={[e]} onUndo={vi.fn()} onResend={vi.fn()} onDismiss={vi.fn()} />)
    expect(screen.queryByTestId('queued-messages')).toBeNull()
  })
})
