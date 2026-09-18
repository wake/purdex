// spa/src/lib/nex/tool-activity.test.ts — spec §4.2 A1–A4 through applyDurableEvent.
import { describe, it, expect } from 'vitest'
import { applyDurableEvent, defaultExecutionState, type ExecutionState } from './event-reducer'
import { toToolCallActivity } from './tool-activity'
import type { NexEvent } from './types'

describe('applyDurableEvent: tool activity', () => {
  const MSG = 'msg_011Cf9fLxdWgh5jkA2b9MjXt'
  const TOOL = 'toolu_01CyoH2VpvKrWq9hjjeBX6uM'
  const at = (seq: number, kind: string, payload: Record<string, unknown>, created_at = seq * 100): NexEvent =>
    ({ seq, execution_id: 'exc_1', kind, payload, created_at })
  const assistant = (blocks: Record<string, unknown>[], id: string = MSG, parent: string | null = null): Record<string, unknown> =>
    ({ type: 'assistant', message: { id, role: 'assistant', content: blocks, stop_reason: null }, parent_tool_use_id: parent })
  const toolUse = (id: string = TOOL, name = 'Bash'): Record<string, unknown> => ({ type: 'tool_use', id, name, input: {} })
  const toolResult = (tool_use_id: string = TOOL, is_error = false, parent: string | null = null): Record<string, unknown> =>
    ({ type: 'user', message: { role: 'user', content: [{ type: 'tool_result', tool_use_id, content: 'ok', is_error }] }, parent_tool_use_id: parent })
  const running = (id: string, startedAt = 100) => ({ [id]: { name: 'Bash', startedAt, endedAt: null, status: 'running' as const } })

  it('A1: a tool_use block starts a running activity at ev.created_at; first sighting wins', () => {
    let s = applyDurableEvent(defaultExecutionState(), at(1, 'assistant', assistant([toolUse(TOOL, 'Bash')])))
    expect(s.tools[TOOL]).toEqual({ name: 'Bash', startedAt: 100, endedAt: null, status: 'running' })
    s = applyDurableEvent(s, at(2, 'assistant', assistant([toolUse(TOOL, 'Renamed')], 'msg_2')))
    expect(s.tools[TOOL]).toEqual({ name: 'Bash', startedAt: 100, endedAt: null, status: 'running' })
  })

  it('A1/A2: subagent assistant and user frames (non-null parent_tool_use_id) are skipped', () => {
    let s = applyDurableEvent(defaultExecutionState(), at(1, 'assistant', assistant([toolUse('toolu_sub')], MSG, 'toolu_parent')))
    expect(s.tools).toEqual({})
    s = { ...s, tools: running(TOOL) }
    s = applyDurableEvent(s, at(2, 'user', toolResult(TOOL, false, 'toolu_parent')))
    expect(s.tools[TOOL]).toEqual({ name: 'Bash', startedAt: 100, endedAt: null, status: 'running' })
  })

  it('A2: a tool_result ends the activity as done, or error when is_error', () => {
    const base: ExecutionState = { ...defaultExecutionState(), tools: { ...running('toolu_ok'), ...running('toolu_bad') } }
    let s = applyDurableEvent(base, at(4, 'user', toolResult('toolu_ok', false)))
    s = applyDurableEvent(s, at(5, 'user', toolResult('toolu_bad', true)))
    expect(s.tools.toolu_ok).toEqual({ name: 'Bash', startedAt: 100, endedAt: 400, status: 'done' })
    expect(s.tools.toolu_bad).toEqual({ name: 'Bash', startedAt: 100, endedAt: 500, status: 'error' })
  })

  it('A2: a tool_result for an unknown or already-ended (done) tool changes nothing', () => {
    const ended = { name: 'Bash', startedAt: 100, endedAt: 200, status: 'done' as const }
    const base: ExecutionState = { ...defaultExecutionState(), tools: { toolu_x: ended } }
    const s = applyDurableEvent(base, at(4, 'user', toolResult('toolu_x')))
    expect(s.tools.toolu_x).toEqual(ended)
    expect(applyDurableEvent(base, at(5, 'user', toolResult('toolu_never'))).tools).toEqual({ toolu_x: ended })
  })

  it('N5: a raw tool_result never downgrades a denied entry (P-B3)', () => {
    const denied = { name: 'Bash', startedAt: 100, endedAt: 200, status: 'denied' as const, durationMs: 7 }
    const base: ExecutionState = { ...defaultExecutionState(), tools: { toolu_d: denied } }
    expect(applyDurableEvent(base, at(4, 'user', toolResult('toolu_d'))).tools.toolu_d).toEqual(denied)
    expect(applyDurableEvent(base, at(5, 'user', toolResult('toolu_d', true))).tools.toolu_d).toEqual(denied)
  })

  it('F3: a tool_result arriving after the turn-ending result overrides the aborted status with done at its own created_at', () => {
    const fold = (...events: NexEvent[]) => events.reduce(applyDurableEvent, defaultExecutionState())
    const s = fold(
      at(10, 'assistant', assistant([toolUse('toolu_1')])),
      at(11, 'result', { type: 'result', subtype: 'success' }),
      at(12, 'user', toolResult('toolu_1')),
    )
    expect(s.tools.toolu_1).toEqual({ name: 'Bash', startedAt: 1000, endedAt: 1200, status: 'done' })
    const err = fold(
      at(10, 'assistant', assistant([toolUse('toolu_1')])),
      at(11, 'result', { type: 'result', subtype: 'success' }),
      at(12, 'user', toolResult('toolu_1', true)),
    )
    expect(err.tools.toolu_1).toEqual({ name: 'Bash', startedAt: 1000, endedAt: 1200, status: 'error' })
  })

  it('A3: a turn-ending event aborts only running tools; done/error/aborted keep their timestamps', () => {
    const done = { name: 'Bash', startedAt: 100, endedAt: 200, status: 'done' as const }
    const error = { name: 'Bash', startedAt: 100, endedAt: 250, status: 'error' as const }
    const base: ExecutionState = { ...defaultExecutionState(), tools: { toolu_done: done, toolu_err: error, ...running('toolu_run', 300) } }
    const s = applyDurableEvent(base, at(9, 'execution.turn_orphaned', { turn_id: 't' }))
    expect(s.tools.toolu_done).toEqual(done)
    expect(s.tools.toolu_err).toEqual(error)
    expect(s.tools.toolu_run).toEqual({ name: 'Bash', startedAt: 300, endedAt: 900, status: 'aborted' })
  })

  it('A4: created_at 0 (bare frame fallback) is stored as startedAt 0, never Date.now()', () => {
    const s = applyDurableEvent(defaultExecutionState(), at(1, 'assistant', assistant([toolUse()]), 0))
    expect(s.tools[TOOL].startedAt).toBe(0)
    const ended = applyDurableEvent(s, at(2, 'user', toolResult(), 0))
    expect(ended.tools[TOOL]).toEqual({ name: 'Bash', startedAt: 0, endedAt: 0, status: 'done' })
  })

})

describe('toToolCallActivity: durable ToolActivity → ToolCallBlock activity prop', () => {
  it('running → { status: running, startedAt, now }', () => {
    expect(toToolCallActivity({ name: 'Bash', startedAt: 1_000, endedAt: null, status: 'running' }, 13_400))
      .toEqual({ status: 'running', startedAt: 1_000, now: 13_400 })
  })

  it('done / error with an endedAt → { status, startedAt, endedAt } (now not carried)', () => {
    expect(toToolCallActivity({ name: 'Bash', startedAt: 1_000, endedAt: 7_200, status: 'done' }, 99_999))
      .toEqual({ status: 'done', startedAt: 1_000, endedAt: 7_200 })
    expect(toToolCallActivity({ name: 'Bash', startedAt: 1_000, endedAt: 7_200, status: 'error' }, 99_999))
      .toEqual({ status: 'error', startedAt: 1_000, endedAt: 7_200 })
  })

  it('done / error with endedAt null (malformed) → undefined, so the block renders plain', () => {
    expect(toToolCallActivity({ name: 'Bash', startedAt: 1_000, endedAt: null, status: 'done' }, 5)).toBeUndefined()
    expect(toToolCallActivity({ name: 'Bash', startedAt: 1_000, endedAt: null, status: 'error' }, 5)).toBeUndefined()
  })

  it('P-B3: denied with an endedAt → { status: denied, startedAt, endedAt, durationMs }', () => {
    expect(toToolCallActivity({ name: 'Bash', startedAt: 100, endedAt: 200, status: 'denied', durationMs: 7 }, 0))
      .toEqual({ status: 'denied', startedAt: 100, endedAt: 200, durationMs: 7 })
    expect(toToolCallActivity({ name: 'Bash', startedAt: 100, endedAt: null, status: 'denied' }, 0)).toBeUndefined()
  })

  it('P-B3: done with durationMs 26 carries it through to the finished variant', () => {
    expect(toToolCallActivity({ name: 'Read', startedAt: 1_000, endedAt: 7_200, status: 'done', durationMs: 26 }, 99_999))
      .toEqual({ status: 'done', startedAt: 1_000, endedAt: 7_200, durationMs: 26 })
    expect(toToolCallActivity({ name: 'Read', startedAt: 1_000, endedAt: 7_200, status: 'error', durationMs: null }, 99_999))
      .toEqual({ status: 'error', startedAt: 1_000, endedAt: 7_200, durationMs: null })
  })

  it('P-B3: done without durationMs → the variant has no durationMs property at all', () => {
    const v = toToolCallActivity({ name: 'Bash', startedAt: 1_000, endedAt: 7_200, status: 'done' }, 99_999)
    expect(v).toEqual({ status: 'done', startedAt: 1_000, endedAt: 7_200 })
    expect(v && 'durationMs' in v).toBe(false)
  })

  it('aborted → { status: aborted } with no timing', () => {
    expect(toToolCallActivity({ name: 'Bash', startedAt: 1_000, endedAt: 9_000, status: 'aborted' }, 5)).toEqual({ status: 'aborted' })
  })

  it('startedAt 0 (unknown) passes through untouched — the renderer decides to hide the badge', () => {
    expect(toToolCallActivity({ name: 'Bash', startedAt: 0, endedAt: null, status: 'running' }, 5)).toEqual({ status: 'running', startedAt: 0, now: 5 })
    expect(toToolCallActivity({ name: 'Bash', startedAt: 0, endedAt: 0, status: 'done' }, 5)).toEqual({ status: 'done', startedAt: 0, endedAt: 0 })
  })
})
