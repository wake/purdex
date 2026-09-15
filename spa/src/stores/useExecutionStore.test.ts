// spa/src/stores/useExecutionStore.test.ts
import { describe, it, expect, beforeEach } from 'vitest'
import { useExecutionStore, executionKey, splitExecutionKey } from './useExecutionStore'
import type { NexEvent } from '../lib/nex/types'

const ev = (seq: number, kind: string, payload: Record<string, unknown> = {}): NexEvent =>
  ({ seq, execution_id: 'exc_1', kind, payload, created_at: 0 })

describe('useExecutionStore', () => {
  beforeEach(() => useExecutionStore.setState({ executions: {} }))

  it('keys by (hostId, executionId) and splits on the last colon', () => {
    expect(executionKey('h:1', 'exc_1')).toBe('h:1:exc_1')
    expect(splitExecutionKey('h:1:exc_1')).toEqual({ hostId: 'h:1', executionId: 'exc_1' })
  })

  it('applyEvents creates the entry lazily and reduces in order', () => {
    useExecutionStore.getState().applyEvents('h', 'exc_1', [ev(1, 'assistant', { type: 'assistant' }), ev(2, 'result', { type: 'result' })])
    const st = useExecutionStore.getState().executions['h:exc_1']
    expect(st.messages).toHaveLength(2)
    expect(st.lastSeq).toBe(2)
  })

  it('applyEvents with only already-seen seqs does not create a new object', () => {
    const s = useExecutionStore.getState()
    s.applyEvents('h', 'exc_1', [ev(1, 'assistant', { type: 'assistant' })])
    const before = useExecutionStore.getState().executions['h:exc_1']
    s.applyEvents('h', 'exc_1', [ev(1, 'assistant', { type: 'assistant' })])
    expect(useExecutionStore.getState().executions['h:exc_1']).toBe(before)
  })

  it('applyEvents with nothing applicable does not create a phantom entry', () => {
    useExecutionStore.getState().applyEvents('h', 'exc_ghost', [{ ...ev(0, 'assistant'), seq: Number.NaN }])
    expect(useExecutionStore.getState().executions['h:exc_ghost']).toBeUndefined()
  })

  it('applyEvents assumes ascending history-before-live order; callers must buffer live frames until historyLoaded', () => {
    // Documents the contract the P-B.2 subscription hook must honour:
    // attach(observe) → apply history pages ascending → THEN open SSE with
    // Last-Event-ID = lastSeq. A live frame applied first raises the high-water
    // mark and every older history event is (correctly) dropped as a duplicate.
    const s = useExecutionStore.getState()
    s.applyEvents('h', 'exc_1', [ev(20, 'assistant', { type: 'assistant', n: 20 })])
    s.applyEvents('h', 'exc_1', [ev(1, 'assistant', { type: 'assistant', n: 1 }), ev(2, 'assistant', { type: 'assistant', n: 2 })])
    expect(useExecutionStore.getState().executions['h:exc_1'].messages).toHaveLength(1)
  })

  it('setSummary stores the summary and clears summaryStale', () => {
    const s = useExecutionStore.getState()
    s.applyEvents('h', 'exc_1', [ev(1, 'execution.running')])
    expect(useExecutionStore.getState().executions['h:exc_1'].summaryStale).toBe(true)
    s.setSummary('h', 'exc_1', { id: 'exc_1', state: 'running' } as never)
    const st = useExecutionStore.getState().executions['h:exc_1']
    expect(st.summary?.state).toBe('running')
    expect(st.summaryStale).toBe(false)
  })

  it('setters update their field only', () => {
    const s = useExecutionStore.getState()
    s.setSse('h', 'exc_1', 'reconnecting', 'boom')
    s.setLease('h', 'exc_1', { leaseId: 'ls', expiresAt: 5 })
    s.setLeaseError('h', 'exc_1', { code: 'lease_held', heldBy: 'p' })
    s.setPendingSend('h', 'exc_1', true)
    s.setPendingLocal('h', 'exc_1', { text: 'x', delivery: null })
    s.setSendError('h', 'exc_1', { code: 'invalid_text', message: 'too long' })
    s.setLastTurn('h', 'exc_1', { turnId: 't', delivery: 'queued' })
    s.setHistoryLoaded('h', 'exc_1', true)
    const st = useExecutionStore.getState().executions['h:exc_1']
    expect(st).toMatchObject({
      sse: 'reconnecting', sseError: 'boom', lease: { leaseId: 'ls', expiresAt: 5 },
      leaseError: { code: 'lease_held', heldBy: 'p' }, pendingSend: true,
      pendingLocal: { text: 'x', delivery: null }, sendError: { code: 'invalid_text', message: 'too long' },
      lastTurn: { turnId: 't', delivery: 'queued' }, historyLoaded: true, messages: [],
    })
    s.setSse('h', 'exc_1', 'open')
    expect(useExecutionStore.getState().executions['h:exc_1'].sseError).toBeNull()
  })

  it('clearExecution removes one entry; clearHost removes only that host', () => {
    const s = useExecutionStore.getState()
    s.applyEvents('h1', 'exc_1', [ev(1, 'assistant', { type: 'assistant' })])
    s.applyEvents('h1', 'exc_2', [ev(1, 'assistant', { type: 'assistant' })])
    s.applyEvents('h10', 'exc_1', [ev(1, 'assistant', { type: 'assistant' })])
    s.clearExecution('h1', 'exc_2')
    // Default string sort: ':' (0x3A) sorts after '0' (0x30), so 'h10:exc_1' < 'h1:exc_1'.
    expect(Object.keys(useExecutionStore.getState().executions).sort()).toEqual(['h10:exc_1', 'h1:exc_1'])
    s.clearHost('h1')
    expect(Object.keys(useExecutionStore.getState().executions)).toEqual(['h10:exc_1'])
  })
})
