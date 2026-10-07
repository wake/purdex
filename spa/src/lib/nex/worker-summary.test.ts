import { describe, it, expect } from 'vitest'
import { isAwaitingApproval, workerTitleOf } from './worker-summary'
import type { ExecutionSummary } from './types'

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
