// K-2b review fixes (plan D7): a take-back is matched against the snapshot taken when it started (instances (text, at), one to one),
// and a pane that let the conversation go while the daemon answered gets nothing applied.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { renderHook } from '@testing-library/react'
import { bindAdapter, clearAllAdapters } from './deck-adapter'
import { clearAllDrafts, draftKey, readDraft } from './draft-memory'
import { releasePane, retireStaleSessions } from './pane-release'
import type { RecallOutcome } from './send'
import type { QueueEntry, SendQueue } from './send-queue'
import { HIDE_MS, clearAllTakeBack, isRowHidden, setHeaderUserRows, takeBackQueued, useTakeBackState } from './take-back'

const P = 'p-tbu', H = 'h', S = 's1'
const KEY = draftKey(P, H, S)
const recalls: Array<(o: RecallOutcome) => void> = []
const takeBack = vi.fn(() => new Promise<RecallOutcome>((resolve) => { recalls.push(resolve) }))
const answer = async (o: RecallOutcome) => { recalls.shift()!(o); await vi.advanceTimersByTimeAsync(0) }

const entry = (id: string, text: string, claimAt?: number): QueueEntry =>
  ({ id, ids: [id], text, state: 'sent', mode: 'fill', createdAt: 0, undoUntil: 0, startedAt: 0, ...(claimAt !== undefined ? { claimAt, matched: true } : {}) })
function fakeQueue(list: QueueEntry[]) {
  const dismissed: string[] = []
  const queue = { entries: () => list, dismiss: (id: string) => { dismissed.push(id) } } as unknown as SendQueue
  return { queue, dismissed }
}
const stateOf = () => renderHook(() => useTakeBackState(KEY)).result.current

beforeEach(() => {
  vi.useFakeTimers()
  vi.setSystemTime(1_000_000)
  recalls.length = 0; takeBack.mockClear()
  bindAdapter(KEY, { kind: 'terminal', canSend: () => true, recall: true, port: () => ({ submit: async () => ({ kind: 'accepted' as const }), interrupt: async () => ({ kind: 'accepted' as const }) }), upload: null, takeBack })
})
afterEach(() => { clearAllDrafts(); clearAllAdapters(); clearAllTakeBack(); vi.useRealTimers() })

describe('matching the answer to the snapshot', () => {
  it('a multi-line queued message is recognised and its row hidden', async () => {
    setHeaderUserRows(KEY, [{ text: 'line one\nline two', at: 10 }, { text: 'next', at: 20 }])
    const p = takeBackQueued(KEY, fakeQueue([]).queue)
    await answer({ kind: 'back', text: 'line one\nline two\nnext' })
    await p
    expect(readDraft(KEY)).toBe('line one\nline two\nnext')
    expect(isRowHidden(stateOf(), { text: 'line one\nline two', at: 10 })).toBe(true)
    expect(isRowHidden(stateOf(), { text: 'next', at: 20 })).toBe(true)
  })

  it('two same-text App entries: only the one holding the claim is dismissed', async () => {
    setHeaderUserRows(KEY, [{ text: 'dup', at: 50 }])
    const { queue, dismissed } = fakeQueue([entry('old', 'dup', 5), entry('claimed', 'dup', 50)])
    const p = takeBackQueued(KEY, queue)
    await answer({ kind: 'back', text: 'dup' })
    await p
    expect(dismissed).toEqual(['claimed'])
  })

  it('a new same-text row arriving after the recall (different at) is visible at once', async () => {
    setHeaderUserRows(KEY, [{ text: 'a', at: 10 }])
    const p = takeBackQueued(KEY, fakeQueue([]).queue)
    await answer({ kind: 'back', text: 'a' })
    await p
    expect(isRowHidden(stateOf(), { text: 'a', at: 10 })).toBe(true)
    setHeaderUserRows(KEY, [{ text: 'a', at: 10 }, { text: 'a', at: 99 }])
    expect(isRowHidden(stateOf(), { text: 'a', at: 99 })).toBe(false)
    expect(isRowHidden(stateOf(), { text: 'a', at: 10 })).toBe(true)
    vi.advanceTimersByTime(HIDE_MS)
    expect(isRowHidden(stateOf(), { text: 'a', at: 10 })).toBe(false)
  })

  it('a header that changed between start and answer: draft updated, nothing hidden or dismissed', async () => {
    setHeaderUserRows(KEY, [{ text: 'a', at: 10 }])
    const { queue, dismissed } = fakeQueue([entry('e', 'a', 10)])
    const p = takeBackQueued(KEY, queue)
    setHeaderUserRows(KEY, [{ text: 'a', at: 10 }, { text: 'b', at: 11 }])
    await answer({ kind: 'back', text: 'a\nb' })
    await p
    expect(readDraft(KEY)).toBe('a\nb')
    expect(dismissed).toEqual([])
    expect(stateOf().hide).toBeNull()
    expect(stateOf().busy).toBe(false)
  })
})

describe('a pane that let the conversation go while the daemon answered', () => {
  const inFlight = () => {
    setHeaderUserRows(KEY, [{ text: 'a', at: 10 }])
    const { queue, dismissed } = fakeQueue([entry('e', 'a', 10)])
    return { p: takeBackQueued(KEY, queue), dismissed }
  }
  it('releasePane: no draft write, no dismiss, no timer', async () => {
    const { p, dismissed } = inFlight()
    releasePane(P)
    await answer({ kind: 'back', text: 'a' })
    await p
    expect(readDraft(KEY) ?? '').toBe('')
    expect(dismissed).toEqual([])
    expect(vi.getTimerCount()).toBe(0)
    expect(stateOf().hide).toBeNull()
  })
  it('retireStaleSessions: likewise', async () => {
    const { p, dismissed } = inFlight()
    retireStaleSessions(P, H, 'other')
    await answer({ kind: 'back', text: 'a' })
    await p
    expect(readDraft(KEY) ?? '').toBe('')
    expect(dismissed).toEqual([])
    expect(vi.getTimerCount()).toBe(0)
  })
})
