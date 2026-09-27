// spa/src/lib/nex/prior-history.test.ts — R4 T2.2 (Q3): when does the cost
// include spend from before a hand-over?
import { describe, expect, it } from 'vitest'

import { costSummary } from './cost-summary'
import type { StreamMessage } from './message-types'
import { costIncludesPriorHistory } from './prior-history'
import type { ExecutionSummary } from './types'

const summary = (extra: Partial<ExecutionSummary> = {}): ExecutionSummary => ({
  id: 'exc_1', state: 'idle', provider: 'claude', principal_id: 'p', cwd: '/r', mount_kind: 'dev', brief: 'b',
  labels: {}, created_at: 0, updated_at: 0, duration_ms: null, event_count: 0, observers: 0, archived: false,
  ...extra,
})

const result = (extra: Record<string, unknown>): StreamMessage => ({ type: 'result', ...extra }) as StreamMessage
/** A hand-over's turn 1 that resumed and billed something. */
const costed = costSummary([result({ total_cost_usd: 0.42, usage: { output_tokens: 10 } })])
const noResult = costSummary([])

describe('costIncludesPriorHistory', () => {
  it('a successful hand-over (resume_session_id set, turn 1 resumed, a costed result) → true', () => {
    expect(costIncludesPriorHistory(summary({ resume_session_id: 'c191a5a0', last_turn_reason: 'final_response' }), costed)).toBe(true)
  })

  it('no resume_session_id → false', () => {
    expect(costIncludesPriorHistory(summary(), costed)).toBe(false)
    expect(costIncludesPriorHistory(summary({ resume_session_id: '' }), costed)).toBe(false)
    expect(costIncludesPriorHistory(null, costed)).toBe(false)
  })

  it('review #7: no top-level result yet (turn 1 still running, or history not loaded) → false', () => {
    expect(costIncludesPriorHistory(summary({ state: 'running', resume_session_id: 'c191a5a0' }), noResult)).toBe(false)
    expect(costIncludesPriorHistory(summary({ state: 'running', resume_session_id: 'c191a5a0' }), null)).toBe(false)
  })

  it('review #7: only zero-cost or subagent-scoped results → false', () => {
    const zero = costSummary([
      result({ total_cost_usd: 0, subtype: 'error_during_execution' }),
      result({ total_cost_usd: 0.3, parent_tool_use_id: 'toolu_x' }),
    ])
    expect(costIncludesPriorHistory(summary({ resume_session_id: 'c191a5a0' }), zero)).toBe(false)
  })

  it('review #7: turn 1 failed fatally without a result (non-resume reason) → false', () => {
    expect(costIncludesPriorHistory(summary({
      state: 'failed', resume_session_id: 'c191a5a0', terminal_reason: 'spawn_failed', last_turn_reason: 'spawn_failed',
    }), noResult)).toBe(false)
  })

  it('review #7: terminated before any result → false', () => {
    expect(costIncludesPriorHistory(summary({
      state: 'terminated', resume_session_id: 'c191a5a0', terminal_reason: 'terminated',
    }), noResult)).toBe(false)
  })

  it('rejected at delegate (preflight: transcript gone) → false', () => {
    expect(costIncludesPriorHistory(summary({
      state: 'rejected', resume_session_id: 'c191a5a0',
      reject_reason: 'resume_session_id c191a5a0: not found (session expired, or not this cwd\'s session)',
    }), noResult)).toBe(false)
  })

  it('turn 1 failed its launch-time resume gate (terminal_reason session_expired) → false', () => {
    expect(costIncludesPriorHistory(summary({
      state: 'failed', resume_session_id: 'c191a5a0', terminal_reason: 'session_expired', last_turn_reason: 'session_expired',
    }), costed)).toBe(false)
  })

  it('a LATER turn ending session_expired does not retract turn 1\'s resume (last_turn_reason is not the signal)', () => {
    expect(costIncludesPriorHistory(summary({
      state: 'idle', resume_session_id: 'c191a5a0', last_turn_reason: 'session_expired',
    }), costed)).toBe(true)
  })
})
