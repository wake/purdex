// spa/src/hooks/useSendQueueDriver.stages.test.tsx — the queue's stages mode follows the host's capability, but an `unknown` (the probe after a reconnect has not answered)
// is not a `no`: a pending message must not fall back to text matching in that gap (message-stages L7).
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { act, cleanup, renderHook } from '@testing-library/react'
import { useSendQueueDriver } from './useSendQueueDriver'
import { clearAllSendQueues, sendQueueFor, UNDO_MS } from '../lib/conversations/send-queue'
import { draftKey } from '../lib/conversations/draft-memory'
import { useUnattendedStore } from '../stores/useUnattendedStore'
import type { SendOutcome, SendPort } from '../lib/conversations/send'
import type { ConversationItem } from '../lib/conversations/types'

const KEY = draftKey('p', 'h', 's')
const resolvers: Array<(o: SendOutcome) => void> = []
const port: SendPort = {
  submit: () => new Promise<SendOutcome>((resolve) => { resolvers.push(resolve) }),
  interrupt: () => Promise.resolve<SendOutcome>({ kind: 'accepted' }),
}
const twin = (at: number): ConversationItem => ({ type: 'user', id: 'u1', at, index: 0, text: 'hi', source: 'user' }) as ConversationItem

beforeEach(() => { vi.useFakeTimers(); vi.setSystemTime(1_000_000); resolvers.length = 0; useUnattendedStore.getState().reset() })
afterEach(() => { cleanup(); vi.useRealTimers(); clearAllSendQueues(); useUnattendedStore.getState().reset() })

describe('stages mode and the capability probe', () => {
  it('yes → unknown (a reconnect) → an echo by text does not settle the pending message; an explicit no brings the text matching back', async () => {
    const q = sendQueueFor(KEY, () => port)
    const { rerender } = renderHook(({ items }) => useSendQueueDriver('p', 'h', 's', items), { initialProps: { items: [] as ConversationItem[] } })
    act(() => useUnattendedStore.getState().setStagesSupport('h', 'yes'))
    q.enqueue('hi')
    await act(async () => { await vi.advanceTimersByTimeAsync(UNDO_MS) })
    await act(async () => { resolvers[0]({ kind: 'handed', mode: 'fill' }); await vi.advanceTimersByTimeAsync(0) })
    act(() => useUnattendedStore.getState().invalidateSupport('h'))
    rerender({ items: [twin(1_003_000)] })
    expect(q.entries()).toHaveLength(1)
    act(() => useUnattendedStore.getState().setStagesSupport('h', 'no'))
    rerender({ items: [twin(1_003_000), { ...twin(1_003_000), id: 'u2' }] })
    expect(q.entries()).toHaveLength(0)
  })
})
