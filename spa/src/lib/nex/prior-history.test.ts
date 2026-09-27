// spa/src/lib/nex/prior-history.test.ts — R4 T2.2 (Q3): when does the cost
// include spend from before a hand-over?
import { describe, expect, it } from 'vitest'

import { costIncludesPriorHistory } from './prior-history'
import type { ExecutionSummary } from './types'

const summary = (extra: Partial<ExecutionSummary> = {}): ExecutionSummary => ({
  id: 'exc_1', state: 'idle', provider: 'claude', principal_id: 'p', cwd: '/r', mount_kind: 'dev', brief: 'b',
  labels: {}, created_at: 0, updated_at: 0, duration_ms: null, event_count: 0, observers: 0, archived: false,
  ...extra,
})

describe('costIncludesPriorHistory', () => {
  it('a successful hand-over (resume_session_id set, turn 1 resumed) → true', () => {
    expect(costIncludesPriorHistory(summary({ resume_session_id: 'c191a5a0', last_turn_reason: 'final_response' }))).toBe(true)
    // Still running turn 1: the resume passed the launch gate.
    expect(costIncludesPriorHistory(summary({ state: 'running', resume_session_id: 'c191a5a0' }))).toBe(true)
  })

  it('no resume_session_id → false', () => {
    expect(costIncludesPriorHistory(summary())).toBe(false)
    expect(costIncludesPriorHistory(summary({ resume_session_id: '' }))).toBe(false)
    expect(costIncludesPriorHistory(null)).toBe(false)
  })

  it('rejected at delegate (preflight: transcript gone) → false', () => {
    expect(costIncludesPriorHistory(summary({
      state: 'rejected', resume_session_id: 'c191a5a0',
      reject_reason: 'resume_session_id c191a5a0: not found (session expired, or not this cwd\'s session)',
    }))).toBe(false)
  })

  it('turn 1 failed its launch-time resume gate (terminal_reason session_expired) → false', () => {
    expect(costIncludesPriorHistory(summary({
      state: 'failed', resume_session_id: 'c191a5a0', terminal_reason: 'session_expired', last_turn_reason: 'session_expired',
    }))).toBe(false)
  })

  it('a LATER turn ending session_expired does not retract turn 1\'s resume (last_turn_reason is not the signal)', () => {
    expect(costIncludesPriorHistory(summary({
      state: 'idle', resume_session_id: 'c191a5a0', last_turn_reason: 'session_expired',
    }))).toBe(true)
  })
})
