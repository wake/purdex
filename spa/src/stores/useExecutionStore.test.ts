// spa/src/stores/useExecutionStore.test.ts
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { useExecutionStore, executionKey, splitExecutionKey } from './useExecutionStore'
import type { NexEvent } from '../lib/nex/types'
import { defaultPreludeState } from '../lib/nex/prelude'

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

  it('applyTasksSnapshot merges a /tasks snapshot into the entry (#83 correction)', () => {
    const s = useExecutionStore.getState()
    const start = (task_id: string) => ({ task_id, turn_id: 'u', kind: 'shell', task_type: 'local_bash', tool_use_id: null, parent_tool_use_id: null, description: task_id, backgrounded: true, started_at: 1 })
    s.applyEvents('h', 'exc_1', [ev(3, 'task_start', start('missed_end')), ev(9, 'task_start', start('newer'))])
    s.applyTasksSnapshot('h', 'exc_1', { items: [], cursor: 5 })
    const st = useExecutionStore.getState().executions['h:exc_1']
    expect(Object.keys(st.tasks)).toEqual(['newer'])
    expect(st.messages).toHaveLength(0)
    // Task events never move lastSeq (the SSE Last-Event-ID high-water mark).
    expect(st.lastSeq).toBe(0)
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

  it('a summary fetch that started before applySummaryPatch is dropped; a later one lands', () => {
    const s = useExecutionStore.getState()
    s.setSummary('h', 'exc_1', { id: 'exc_1', state: 'idle', archived: false } as never)
    const gen = useExecutionStore.getState().executions['h:exc_1'].summaryGen
    s.applySummaryPatch('h', 'exc_1', { state: 'terminated', archived: true })
    s.setSummary('h', 'exc_1', { id: 'exc_1', state: 'idle', archived: false } as never, 0, gen)
    expect(useExecutionStore.getState().executions['h:exc_1'].summary).toMatchObject({ state: 'terminated', archived: true })
    const gen2 = useExecutionStore.getState().executions['h:exc_1'].summaryGen
    s.setSummary('h', 'exc_1', { id: 'exc_1', state: 'terminated', archived: true, brief: 'fresh' } as never, 0, gen2)
    expect(useExecutionStore.getState().executions['h:exc_1'].summary).toMatchObject({ brief: 'fresh' })
  })

  it('setSummary keeps summaryStale AND the existing summary untouched when a newer lifecycle event landed during the refetch', () => {
    // Seed an existing summary first — this is what a stale fetch must not
    // clobber; a null summary is the separate first-fetch case below.
    const s = useExecutionStore.getState()
    s.setSummary('h', 'exc_1', { id: 'exc_1', state: 'idle' } as never)

    // execution.running (seq 10) marks stale; execution.terminal (seq 11)
    // arrives before the summary fetch (snapshotted at seq 10) resolves.
    s.applyEvents('h', 'exc_1', [ev(10, 'execution.running')])
    s.applyEvents('h', 'exc_1', [ev(11, 'execution.terminal', { state: 'idle' })])
    expect(useExecutionStore.getState().executions['h:exc_1'].summaryStale).toBe(true)

    // Fetch snapshotted at seq 10 arrives after seq 11 landed: it is older
    // than the reducer's own state (idle) — must not overwrite it with the
    // stale 'running' snapshot.
    s.setSummary('h', 'exc_1', { id: 'exc_1', state: 'running' } as never, 10)
    let st = useExecutionStore.getState().executions['h:exc_1']
    expect(st.summary?.state).toBe('idle')
    expect(st.summaryStale).toBe(true)

    // Fetch snapshotted at seq 11 is caught up: adopted, stale clears.
    s.setSummary('h', 'exc_1', { id: 'exc_1', state: 'idle' } as never, 11)
    st = useExecutionStore.getState().executions['h:exc_1']
    expect(st.summary?.state).toBe('idle')
    expect(st.summaryStale).toBe(false)

    s.applyEvents('h', 'exc_1', [ev(12, 'execution.running')])
    s.setSummary('h', 'exc_1', { id: 'exc_1', state: 'running' } as never)
    expect(useExecutionStore.getState().executions['h:exc_1'].summaryStale).toBe(false)
  })

  it('setSummary adopts the fetched summary on a stale first fetch (no prior summary to protect)', () => {
    const s = useExecutionStore.getState()
    s.applyEvents('h', 'exc_1', [ev(5, 'execution.running')])
    expect(useExecutionStore.getState().executions['h:exc_1'].summary).toBeNull()

    s.setSummary('h', 'exc_1', { id: 'exc_1', state: 'queued' } as never, 0)
    const st = useExecutionStore.getState().executions['h:exc_1']
    expect(st.summary?.state).toBe('queued')
    expect(st.summaryStale).toBe(true)
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

  it('setSse accepts "paused" (spec v2.1 §4.3.2 step 4 — P-B.2 subscription-slot cap)', () => {
    useExecutionStore.getState().setSse('h', 'exc_1', 'paused')
    expect(useExecutionStore.getState().executions['h:exc_1'].sse).toBe('paused')
  })

  describe('applyTransient (spec §4.3 — one set() per batch)', () => {
    const streamEvent = (event: Record<string, unknown>) =>
      ({ kind: 'stream_event', payload: { type: 'stream_event', event, session_id: 's', parent_tool_use_id: null, uuid: 'u' } })
    const messageStart = (id = 'msg_1') =>
      streamEvent({ type: 'message_start', message: { id, type: 'message', role: 'assistant', content: [] } })
    const textDelta = (index: number, text: string) =>
      streamEvent({ type: 'content_block_delta', index, delta: { type: 'text_delta', text } })

    it('folds message_start + three text deltas from one call into one block with the concatenated text and turnLive', () => {
      useExecutionStore.getState().applyTransient('h', 'exc_1', [
        messageStart(), textDelta(0, 'hel'), textDelta(0, 'lo '), textDelta(0, 'world'),
      ])
      const st = useExecutionStore.getState().executions['h:exc_1']
      expect(st.turnLive).toBe(true)
      expect(st.partial?.messageId).toBe('msg_1')
      expect(Object.keys(st.partial?.blocks ?? {})).toEqual(['0'])
      expect(st.partial?.blocks[0].text).toBe('hello world')
    })

    it('a batch of only lease.renewed frames on an untouched execution materialises no entry', () => {
      useExecutionStore.getState().applyTransient('h', 'exc_ghost', [
        { kind: 'lease.renewed', payload: { lease_id: 'ls', expires_at: 5 } },
        { kind: 'lease.renewed', payload: { lease_id: 'ls', expires_at: 6 } },
      ])
      expect(useExecutionStore.getState().executions['h:exc_ghost']).toBeUndefined()
    })

    it('clearExecution after applyTransient drops the entry including its partial', () => {
      const s = useExecutionStore.getState()
      s.applyTransient('h', 'exc_1', [messageStart(), textDelta(0, 'hi')])
      expect(useExecutionStore.getState().executions['h:exc_1'].partial?.blocks[0].text).toBe('hi')
      s.clearExecution('h', 'exc_1')
      expect(useExecutionStore.getState().executions['h:exc_1']).toBeUndefined()
    })

    it('does not change lastSeq on an execution that already has durable events', () => {
      const s = useExecutionStore.getState()
      s.applyEvents('h', 'exc_1', [ev(7, 'assistant', { type: 'assistant' })])
      s.applyTransient('h', 'exc_1', [messageStart(), textDelta(0, 'hi')])
      const st = useExecutionStore.getState().executions['h:exc_1']
      expect(st.lastSeq).toBe(7)
      expect(st.partial?.blocks[0].text).toBe('hi')
    })
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

describe('useExecutionStore — the optimistic line owns its preview URLs (phase E)', () => {
  const origRevoke = URL.revokeObjectURL
  let revoke: ReturnType<typeof vi.fn>
  const previews = (...urls: string[]) => urls.map((previewUrl) => ({ previewUrl, media_type: 'image/png' }))
  beforeEach(() => {
    useExecutionStore.setState({ executions: {} })
    revoke = vi.fn()
    Object.assign(URL, { revokeObjectURL: revoke })
  })
  afterEach(() => { Object.assign(URL, { revokeObjectURL: origRevoke }) })

  it('revokes the URLs exactly once when pendingLocal clears, not while the same array stays', () => {
    const s = useExecutionStore.getState()
    const p = previews('blob:a', 'blob:b')
    s.setPendingLocal('h', 'exc_1', { text: '', delivery: null, attachments: p })
    s.setPendingLocal('h', 'exc_1', { text: '', delivery: 'queued', attachments: p })
    s.setPendingSend('h', 'exc_1', true)
    expect(revoke).not.toHaveBeenCalled()
    s.setPendingLocal('h', 'exc_1', null)
    expect(revoke.mock.calls).toEqual([['blob:a'], ['blob:b']])
    s.setPendingLocal('h', 'exc_1', null)
    expect(revoke).toHaveBeenCalledTimes(2)
  })

  it('revokes the replaced array when a newer send takes the line', () => {
    const s = useExecutionStore.getState()
    s.setPendingLocal('h', 'exc_1', { text: 'A', delivery: null, attachments: previews('blob:a') })
    s.setPendingLocal('h', 'exc_1', { text: 'B', delivery: null, attachments: previews('blob:b') })
    expect(revoke.mock.calls).toEqual([['blob:a']])
  })

  it('revokes when message_accepted consumes the line, and when the execution or host is cleared', () => {
    const s = useExecutionStore.getState()
    s.setPendingLocal('h', 'exc_1', { text: '', delivery: null, attachments: previews('blob:a') })
    s.applyEvents('h', 'exc_1', [ev(1, 'execution.message_accepted', { turn_id: 't1', text: '' })])
    expect(revoke.mock.calls).toEqual([['blob:a']])
    s.setPendingLocal('h', 'exc_1', { text: '', delivery: null, attachments: previews('blob:b') })
    s.clearExecution('h', 'exc_1')
    s.setPendingLocal('h', 'exc_2', { text: '', delivery: null, attachments: previews('blob:c') })
    s.clearHost('h')
    expect(revoke.mock.calls).toEqual([['blob:a'], ['blob:b'], ['blob:c']])
  })
})

describe('prelude actions', () => {
  beforeEach(() => useExecutionStore.setState({ executions: {} }))

  it('loading → page → never touches messages, lastSeq or tools', () => {
    const s = useExecutionStore.getState()
    s.applyEvents('h', 'e', [{ seq: 1, execution_id: 'e', kind: 'execution.delegated', payload: { brief: 'b' }, created_at: 1 }])
    const before = useExecutionStore.getState().executions[executionKey('h', 'e')]
    s.preludeLoading('h', 'e', 1)
    s.applyPreludePage('h', 'e', { state: 'ok', items: [], prevCursor: null, totalBytes: null }, null, 1)
    const after = useExecutionStore.getState().executions[executionKey('h', 'e')]
    expect(after.prelude).toMatchObject({ status: 'ok', done: true })
    expect(after.messages).toBe(before.messages)
    expect(after.lastSeq).toBe(before.lastSeq)
    expect(after.tools).toBe(before.tools)
  })

  it('preludeHalted is a no-op while loading, and otherwise sets the error and frees the lock, keeping items and cursor', () => {
    const s = useExecutionStore.getState()
    const page = { state: 'ok' as const, items: [{ offset: null, pos: '1', at: 1, kind: 'prelude.segment' as const, entrypoint: 'cli' }], prevCursor: 'c', totalBytes: null }
    s.preludeLoading('h', 'e', 1)
    s.applyPreludePage('h', 'e', page, null, 1)
    s.preludeLoading('h', 'e', 2)
    const loading = useExecutionStore.getState().executions[executionKey('h', 'e')]
    s.preludeHalted('h', 'e', 'ignored')
    expect(useExecutionStore.getState().executions[executionKey('h', 'e')]).toBe(loading)
    s.preludeFailed('h', 'e', 'x', 2)
    s.resetPrelude('h', 'e')
    s.preludeLoading('h', 'e', 3)
    s.applyPreludePage('h', 'e', page, null, 3)
    s.preludeHalted('h', 'e', 'prelude: too many pages')
    const p = useExecutionStore.getState().executions[executionKey('h', 'e')].prelude
    expect(p).toMatchObject({ status: 'error', error: 'prelude: too many pages', request: null, cursor: 'c' })
    expect(p.items).toHaveLength(1)
  })

  it('a stale request id leaves the entry referentially identical (apply and fail)', () => {
    const s = useExecutionStore.getState()
    s.preludeLoading('h', 'e', 2)
    const before = useExecutionStore.getState().executions[executionKey('h', 'e')]
    s.applyPreludePage('h', 'e', { state: 'ok', items: [], prevCursor: null, totalBytes: null }, null, 1)
    expect(useExecutionStore.getState().executions[executionKey('h', 'e')]).toBe(before)
    s.preludeFailed('h', 'e', 'late', 1)
    expect(useExecutionStore.getState().executions[executionKey('h', 'e')]).toBe(before)
  })

  it('resetPrelude restores the default slice and leaves messages untouched', () => {
    const s = useExecutionStore.getState()
    s.applyEvents('h', 'e', [{ seq: 1, execution_id: 'e', kind: 'execution.delegated', payload: { brief: 'b' }, created_at: 1 }])
    const before = useExecutionStore.getState().executions[executionKey('h', 'e')]
    s.preludeLoading('h', 'e', 1)
    s.applyPreludePage('h', 'e', { state: 'ok', items: [{ offset: null, pos: '1', at: 1, kind: 'prelude.segment', entrypoint: 'cli' }], prevCursor: 'c', totalBytes: 5 }, null, 1)
    s.resetPrelude('h', 'e')
    const after = useExecutionStore.getState().executions[executionKey('h', 'e')]
    expect(after.prelude).toEqual(defaultPreludeState())
    expect(after.messages).toBe(before.messages)
  })

  it('preludeFailed with the live request id records the error and frees the lock', () => {
    const s = useExecutionStore.getState()
    s.preludeLoading('h', 'e', 3)
    s.preludeFailed('h', 'e', 'boom', 3)
    expect(useExecutionStore.getState().executions[executionKey('h', 'e')].prelude).toMatchObject({ status: 'error', error: 'boom', request: null })
  })
})
