import { describe, it, expect, beforeEach } from 'vitest'
import { hostListTruncated, isAwaitingApproval, readWorkerSummary, workerTitleOf } from './worker-summary'
import type { ExecutionSummary } from './types'
import { executionKey, useExecutionStore } from '../../stores/useExecutionStore'
import { useExecutionListStore } from '../../stores/useExecutionListStore'
import { useWorkerTitlePrefetchStore } from '../../stores/useWorkerTitlePrefetchStore'
import { defaultExecutionState } from './event-reducer'
import { emptyListCache } from './execution-list-effects'

describe('readWorkerSummary (a worker\'s title: the list row, else the live summary, else the prefetch)', () => {
  const s = (brief: string) => ({ id: 'e1', state: 'idle', provider: 'claude', principal_id: 'p', cwd: '/w', mount_kind: 'dev', brief, labels: {},
    created_at: 1, updated_at: 5, duration_ms: null, event_count: 0, observers: 0, archived: false }) as ExecutionSummary
  const setRow = () => useExecutionListStore.setState({ byHost: { h1: { ...emptyListCache(), phase: 'ready', items: [s('row')] } } })
  const setLive = () => useExecutionStore.setState({ executions: { [executionKey('h1', 'e1')]: { ...defaultExecutionState(), summary: s('live') } } })
  const setPrefetched = () => useWorkerTitlePrefetchStore.setState({ byKey: { [executionKey('h1', 'e1')]: s('prefetched') } })
  beforeEach(() => {
    useExecutionListStore.setState({ byHost: {} })
    useExecutionStore.setState({ executions: {} })
    useWorkerTitlePrefetchStore.setState({ byKey: {} })
  })

  it('the row first, then the live summary, then the prefetch; null with none', () => {
    expect(readWorkerSummary('h1', 'e1')).toBeNull()
    setPrefetched()
    expect(readWorkerSummary('h1', 'e1')?.brief).toBe('prefetched')
    setLive()
    expect(readWorkerSummary('h1', 'e1')?.brief).toBe('live')
    setRow()
    expect(readWorkerSummary('h1', 'e1')?.brief).toBe('row')
  })

  it('hostListTruncated: only a list that hit its page cap', () => {
    expect(hostListTruncated({}, 'h1')).toBe(false)
    expect(hostListTruncated({ h1: { ...emptyListCache(), truncated: false } }, 'h1')).toBe(false)
    expect(hostListTruncated({ h1: { ...emptyListCache(), truncated: true } }, 'h1')).toBe(true)
  })
})

describe('isAwaitingApproval (permission channel PC2: the summary decides, no event stream)', () => {
  it('a pending_permission object → awaiting', () => {
    expect(isAwaitingApproval({ pending_permission: { request_id: 'r1', tool_name: 'Bash', since: 1_700_000_000_000 } })).toBe(true)
  })

  it('pending_permission: null is an answer (nothing pending) → not awaiting', () => {
    expect(isAwaitingApproval({ pending_permission: null })).toBe(false)
  })

  it('the field absent (a daemon older than Nexen v0.19.0) → not awaiting', () => {
    expect(isAwaitingApproval({})).toBe(false)
  })

  it('no summary at all → not awaiting', () => {
    expect(isAwaitingApproval(null)).toBe(false)
    expect(isAwaitingApproval(undefined)).toBe(false)
  })
})

describe('isAwaitingApproval is lifecycle-aware (an ended worker is never waiting)', () => {
  const pending = { request_id: 'r1', tool_name: 'Bash', since: 1 }
  it.each(['queued', 'running', 'idle'])('pending + %s → awaiting', (state) => {
    expect(isAwaitingApproval({ state, pending_permission: pending })).toBe(true)
  })
  it.each(['terminated', 'rejected', 'failed'])('pending + %s → not awaiting', (state) => {
    expect(isAwaitingApproval({ state, pending_permission: pending })).toBe(false)
  })
  it('pending + archived → not awaiting', () => {
    expect(isAwaitingApproval({ state: 'idle', archived: true, pending_permission: pending })).toBe(false)
  })
})

describe('workerTitleOf (spec §8.4; phase E: session_title gated by the host capability)', () => {
  type Summary = Pick<ExecutionSummary, 'brief' | 'cwd' | 'session_title'>
  const summary = (over: Partial<Summary> = {}): Summary => ({ brief: 'Fix the bug', cwd: '/w/repo', ...over })
  const titled = (text: string): Summary['session_title'] => ({ text, source: 'ai' })

  it('titleSupported: session_title wins over the brief', () => {
    expect(workerTitleOf({}, summary({ session_title: titled('Fix login') }), true)).toBe('Fix login - repo')
  })

  it('titleSupported: session_title wins over a pre-handoff title too', () => {
    expect(workerTitleOf({ fromTitle: 'Old terminal' }, summary({ session_title: titled('Fix login') }), true)).toBe('Fix login - repo')
  })

  it('not titleSupported: session_title is ignored, falls back to the brief', () => {
    expect(workerTitleOf({}, summary({ session_title: titled('Fix login') }), false)).toBe('Fix the bug - repo')
  })

  it('not titleSupported: falls back to the pre-handoff title when present', () => {
    expect(workerTitleOf({ fromTitle: 'Old terminal' }, summary({ session_title: titled('Fix login') }), false)).toBe('Old terminal - repo')
  })

  it('no summary at all: null regardless of titleSupported', () => {
    expect(workerTitleOf({}, null, true)).toBeNull()
    expect(workerTitleOf({}, undefined, false)).toBeNull()
  })

  it('a session_title containing markup-looking text passes through as plain data, never interpreted', () => {
    expect(workerTitleOf({}, summary({ session_title: titled('Fix <b>login</b> bug') }), true)).toBe('Fix <b>login</b> bug - repo')
  })
})
