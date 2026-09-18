// spa/src/lib/nex/tool-activity.test.ts — spec §4.2 A1–A4 through applyDurableEvent.
import { describe, it, expect } from 'vitest'
import { applyDurableEvent, defaultExecutionState, type ExecutionState } from './event-reducer'
import { DIFF_LINES_SANITY_CAP, recordN2ToolResult, recordN2ToolUse, toToolCallActivity, type ToolActivity } from './tool-activity'
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

  describe('codex R2 A1: tool ids that collide with Object.prototype keys are own-key lookups', () => {
    for (const id of ['constructor', '__proto__', 'toString']) {
      it(`raw A1/A2 with id "${id}" → a complete entry, created and then ended`, () => {
        const started = applyDurableEvent(defaultExecutionState(), at(1, 'assistant', assistant([toolUse(id, 'Bash')])))
        expect(Object.hasOwn(started.tools, id)).toBe(true)
        expect(Object.getPrototypeOf(started.tools)).toBe(Object.prototype)
        expect(started.tools[id]).toEqual({ name: 'Bash', startedAt: 100, endedAt: null, status: 'running' })
        const ended = applyDurableEvent(started, at(2, 'user', toolResult(id, true)))
        expect(ended.tools[id]).toEqual({ name: 'Bash', startedAt: 100, endedAt: 200, status: 'error' })
        expect(Object.getPrototypeOf(ended.tools)).toBe(Object.prototype)
      })

      it(`A3 with a running id "${id}" → aborted as an own key, prototype untouched`, () => {
        const started = applyDurableEvent(defaultExecutionState(), at(1, 'assistant', assistant([toolUse(id, 'Bash')])))
        const s = applyDurableEvent(started, at(9, 'execution.turn_orphaned', { turn_id: 't' }))
        expect(Object.keys(s.tools)).toEqual([id])
        expect(Object.getPrototypeOf(s.tools)).toBe(Object.prototype)
        expect(s.tools[id]).toEqual({ name: 'Bash', startedAt: 100, endedAt: 900, status: 'aborted' })
      })

      it(`raw A2 with id "${id}" on an empty state changes nothing (no phantom entry)`, () => {
        const s = applyDurableEvent(defaultExecutionState(), at(2, 'user', toolResult(id)))
        expect(Object.hasOwn(s.tools, id)).toBe(false)
        expect(Object.keys(s.tools)).toEqual([])
      })
    }
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

describe('recordN2ToolUse / recordN2ToolResult (P-B3 N1/N2/N6/N7)', () => {
  const ID = 'toolu_01Lx7qX6AhKu6WyY8nvCRcKi'
  const MSG = 'msg_011CfBSemfCnZfsaB7SFy4ow'
  const PATH = '/Users/wake/Workspace/tmp-pb3-fixture/hello.txt'
  const base = (tools: Record<string, ToolActivity> = {}): ExecutionState => ({ ...defaultExecutionState(), tools })
  const running = (name = 'Bash', startedAt = 100): ToolActivity => ({ name, startedAt, endedAt: null, status: 'running' })
  // Wire shapes copied from __fixtures__/n2-tool-events-06GBBX07.json (seq 915 / 923).
  const useP = (over: Record<string, unknown> = {}): Record<string, unknown> => ({
    tool_use_id: ID, turn_id: '06GBBX0795J58V0NJEAJHFV5DW', parent_tool_use_id: null, message_id: MSG, block_index: 1,
    name: 'Read', input: { file_path: PATH }, primary_arg: { key: 'file_path', value: PATH }, known: true, ...over,
  })
  const OUTPUT = { text: '1\thello\n2\tworld\n', total_lines: 4, total_bytes: 26, truncated: false, has_non_text: false }
  const HUNK = { old_start: 1, old_lines: 3, new_start: 1, new_lines: 3, lines: [' hello', '-world', '+nexen'] }
  const DIFF = { path: PATH, added: 1, removed: 1, hunks: [HUNK], truncated: false }
  const resultP = (over: Record<string, unknown> = {}): Record<string, unknown> => ({
    tool_use_id: ID, turn_id: '06GBBX0795J58V0NJEAJHFV5DW', parent_tool_use_id: null, message_id: MSG, block_index: 1,
    name: 'Read', status: 'ok', duration_ms: 26, output: OUTPUT, file: { path: PATH, lines: 4 }, diff: DIFF, ...over,
  })
  const expectedFacts = {
    durationMs: 26,
    output: { totalLines: 4, totalBytes: 26, truncated: false, hasNonText: false },
    file: { path: PATH, lines: 4 },
    diff: { path: PATH, added: 1, removed: 1, truncated: false, hunks: [{ oldStart: 1, oldLines: 3, newStart: 1, newLines: 3, lines: [' hello', '-world', '+nexen'] }] },
  }
  const without = (p: Record<string, unknown>, key: string): Record<string, unknown> => {
    const { [key]: _dropped, ...rest } = p
    void _dropped
    return rest
  }

  describe('N1 tool_use', () => {
    it('unseen → running entry at `at` with primaryArg + known (fail-safe when A1 never fired)', () => {
      const s = recordN2ToolUse(base(), useP(), 500)
      expect(s.tools[ID]).toEqual({ name: 'Read', startedAt: 500, endedAt: null, status: 'running', primaryArg: { key: 'file_path', value: PATH }, known: true })
    })

    it('unseen with name null → name ""', () => {
      expect(recordN2ToolUse(base(), useP({ name: null }), 500).tools[ID].name).toBe('')
    })

    it('seen (from A1) → startedAt / endedAt / status untouched; primaryArg + known set; non-empty name kept', () => {
      const s = recordN2ToolUse(base({ [ID]: running('Bash', 100) }), useP({ name: 'Read' }), 500)
      expect(s.tools[ID]).toEqual({ name: 'Bash', startedAt: 100, endedAt: null, status: 'running', primaryArg: { key: 'file_path', value: PATH }, known: true })
      const done: ToolActivity = { name: 'Bash', startedAt: 100, endedAt: 200, status: 'done' }
      expect(recordN2ToolUse(base({ [ID]: done }), useP(), 500).tools[ID]).toEqual({ ...done, primaryArg: { key: 'file_path', value: PATH }, known: true })
    })

    it('seen with an empty name → name filled from the payload', () => {
      expect(recordN2ToolUse(base({ [ID]: running('', 100) }), useP(), 500).tools[ID].name).toBe('Read')
    })

    it('primary_arg: null → primaryArg null (F9); key absent → property absent; malformed → absent', () => {
      const nul = recordN2ToolUse(base(), useP({ primary_arg: null }), 500).tools[ID]
      expect(nul.primaryArg).toBeNull()
      expect('primaryArg' in nul).toBe(true)
      expect('primaryArg' in recordN2ToolUse(base(), without(useP(), 'primary_arg'), 500).tools[ID]).toBe(false)
      expect('primaryArg' in recordN2ToolUse(base(), useP({ primary_arg: { key: 'file_path', value: 42 } }), 500).tools[ID]).toBe(false)
      expect('primaryArg' in recordN2ToolUse(base(), useP({ primary_arg: { value: PATH } }), 500).tools[ID]).toBe(false)
      expect('primaryArg' in recordN2ToolUse(base(), useP({ primary_arg: 'file_path' }), 500).tools[ID]).toBe(false)
    })

    it('known: non-boolean → property absent; absent → absent; false is copied', () => {
      expect('known' in recordN2ToolUse(base(), useP({ known: 1 }), 500).tools[ID]).toBe(false)
      expect('known' in recordN2ToolUse(base(), useP({ known: null }), 500).tools[ID]).toBe(false)
      expect('known' in recordN2ToolUse(base(), without(useP(), 'known'), 500).tools[ID]).toBe(false)
      expect(recordN2ToolUse(base(), useP({ known: false }), 500).tools[ID].known).toBe(false)
    })

    it('N6: missing / non-string tool_use_id → the same state object', () => {
      const s = base({ [ID]: running() })
      expect(recordN2ToolUse(s, useP({ tool_use_id: 42 }), 500)).toBe(s)
      expect(recordN2ToolUse(s, without(useP(), 'tool_use_id'), 500)).toBe(s)
    })

    it('N3: the same tool_use twice → deep-equal entry; a no-op application returns the same state object', () => {
      const once = recordN2ToolUse(base({ [ID]: running() }), useP(), 500)
      const twice = recordN2ToolUse(once, useP(), 900)
      expect(twice.tools[ID]).toEqual(once.tools[ID])
      expect(twice).toBe(once)
    })
  })

  describe('N2 tool_result', () => {
    it('seen running + status ok → done, endedAt at, durationMs 26, output facts without text, file, diff', () => {
      const s = recordN2ToolResult(base({ [ID]: running('Read', 100) }), resultP(), 700)
      expect(s.tools[ID]).toEqual({ name: 'Read', startedAt: 100, endedAt: 700, status: 'done', ...expectedFacts })
      expect(s.tools[ID].output && 'text' in s.tools[ID].output).toBe(false)
    })

    it('status error → error; denied → denied even when the entry is already done', () => {
      expect(recordN2ToolResult(base({ [ID]: running() }), resultP({ status: 'error' }), 700).tools[ID].status).toBe('error')
      const done: ToolActivity = { name: 'Bash', startedAt: 100, endedAt: 200, status: 'done' }
      const s = recordN2ToolResult(base({ [ID]: done }), resultP({ status: 'denied' }), 700)
      expect(s.tools[ID]).toEqual({ name: 'Bash', startedAt: 100, endedAt: 200, status: 'denied', ...expectedFacts })
    })

    it('unknown status string → status unchanged, facts still copied', () => {
      const s = recordN2ToolResult(base({ [ID]: running('Bash', 100) }), resultP({ status: 'weird' }), 700)
      expect(s.tools[ID]).toEqual({ name: 'Bash', startedAt: 100, endedAt: 700, status: 'running', ...expectedFacts })
      expect(recordN2ToolResult(base({ [ID]: running() }), resultP({ status: 7 }), 700).tools[ID].status).toBe('running')
      // prototype keys must not leak through the mapping
      expect(recordN2ToolResult(base({ [ID]: running() }), resultP({ status: 'constructor' }), 700).tools[ID].status).toBe('running')
    })

    it('unseen → created with startedAt 0, endedAt at, mapped status; name from payload or "" when null', () => {
      const s = recordN2ToolResult(base(), resultP({ status: 'error' }), 700)
      expect(s.tools[ID]).toEqual({ name: 'Read', startedAt: 0, endedAt: 700, status: 'error', ...expectedFacts })
      expect(recordN2ToolResult(base(), resultP({ name: null }), 700).tools[ID].name).toBe('')
    })

    it('unseen with an unknown status string → conservative "done" (no prior status to leave as is)', () => {
      expect(recordN2ToolResult(base(), resultP({ status: 'weird' }), 700).tools[ID].status).toBe('done')
    })

    it('N7: unmatched duplicate id (name/message_id/block_index/duration_ms null, status ok) → done, durationMs null, name kept', () => {
      const p = { tool_use_id: ID, turn_id: 't', parent_tool_use_id: null, name: null, message_id: null, block_index: null, duration_ms: null, status: 'ok' }
      const s = recordN2ToolResult(base({ [ID]: running('Bash', 100) }), p, 700)
      expect(s.tools[ID]).toEqual({ name: 'Bash', startedAt: 100, endedAt: 700, status: 'done', durationMs: null })
      expect(toToolCallActivity(s.tools[ID], 0)).toEqual({ status: 'done', startedAt: 100, endedAt: 700, durationMs: null })
    })

    it('after A3 aborted → corrected to the mapped status; endedAt (already set by A3) kept', () => {
      const aborted: ToolActivity = { name: 'Bash', startedAt: 100, endedAt: 300, status: 'aborted' }
      const s = recordN2ToolResult(base({ [ID]: aborted }), resultP({ status: 'ok' }), 700)
      expect(s.tools[ID]).toEqual({ name: 'Bash', startedAt: 100, endedAt: 300, status: 'done', ...expectedFacts })
    })

    it('seen with endedAt already set (A2 ran first) → endedAt kept, not overwritten', () => {
      const done: ToolActivity = { name: 'Bash', startedAt: 100, endedAt: 200, status: 'done' }
      expect(recordN2ToolResult(base({ [ID]: done }), resultP(), 700).tools[ID].endedAt).toBe(200)
    })

    it('N6: missing / non-string tool_use_id → the same state object', () => {
      const s = base({ [ID]: running() })
      expect(recordN2ToolResult(s, resultP({ tool_use_id: null }), 700)).toBe(s)
      expect(recordN2ToolResult(s, without(resultP(), 'tool_use_id'), 700)).toBe(s)
    })

    it('N6: absent fact keys leave the properties absent (absent ≠ null)', () => {
      const s = recordN2ToolResult(base({ [ID]: running() }), { tool_use_id: ID, status: 'ok' }, 700)
      expect(s.tools[ID]).toEqual({ name: 'Bash', startedAt: 100, endedAt: 700, status: 'done' })
      for (const k of ['durationMs', 'output', 'file', 'diff']) expect(k in s.tools[ID], k).toBe(false)
    })

    it('N6: duration_ms null → durationMs null; non-number → absent', () => {
      expect(recordN2ToolResult(base({ [ID]: running() }), resultP({ duration_ms: null }), 700).tools[ID].durationMs).toBeNull()
      expect('durationMs' in recordN2ToolResult(base({ [ID]: running() }), resultP({ duration_ms: '26' }), 700).tools[ID]).toBe(false)
    })

    it('N6: output with non-numeric total_lines / non-boolean flags / non-object → no output property', () => {
      const bads = [{ ...OUTPUT, total_lines: '4' }, { ...OUTPUT, total_bytes: null }, { ...OUTPUT, truncated: 'no' }, { ...OUTPUT, has_non_text: null }, 'text', null, [OUTPUT]]
      for (const output of bads) {
        expect('output' in recordN2ToolResult(base({ [ID]: running() }), resultP({ output }), 700).tools[ID], JSON.stringify(output)).toBe(false)
      }
    })

    it('N6: file with non-string path / non-number lines / non-object → no file property', () => {
      for (const file of [{ path: PATH, lines: '4' }, { path: null, lines: 4 }, { lines: 4 }, PATH, null]) {
        expect('file' in recordN2ToolResult(base({ [ID]: running() }), resultP({ file }), 700).tools[ID], JSON.stringify(file)).toBe(false)
      }
    })

    it('N6: diff with non-array hunks, non-numeric counts, non-boolean truncated, or one bad hunk → whole diff dropped', () => {
      const bads = [
        { ...DIFF, hunks: 'nope' },
        { ...DIFF, hunks: null },
        { ...DIFF, added: '1' },
        { ...DIFF, removed: null },
        { ...DIFF, truncated: 'false' },
        { ...DIFF, path: 7 },
        { ...DIFF, hunks: [HUNK, { ...HUNK, old_start: '1' }] },
        { ...DIFF, hunks: [HUNK, { ...HUNK, new_lines: null }] },
        { ...DIFF, hunks: [HUNK, { ...HUNK, lines: [' hello', 3] }] },
        { ...DIFF, hunks: [HUNK, { ...HUNK, lines: 'x' }] },
        { ...DIFF, hunks: [HUNK, null] },
        [DIFF],
      ]
      for (const diff of bads) {
        const t = recordN2ToolResult(base({ [ID]: running() }), resultP({ diff }), 700).tools[ID]
        expect('diff' in t, JSON.stringify(diff)).toBe(false)
        // the other facts are still copied — only the malformed one is dropped
        expect(t.durationMs).toBe(26)
        expect(t.file).toEqual({ path: PATH, lines: 4 })
      }
      // an empty hunks array is well-formed
      expect(recordN2ToolResult(base({ [ID]: running() }), resultP({ diff: { ...DIFF, hunks: [] } }), 700).tools[ID].diff)
        .toEqual({ path: PATH, added: 1, removed: 1, truncated: false, hunks: [] })
    })

    it('N3: the same tool_result twice → deep-equal entry and the same state object', () => {
      const once = recordN2ToolResult(base({ [ID]: running('Read', 100) }), resultP(), 700)
      const twice = recordN2ToolResult(once, resultP(), 900)
      expect(twice.tools[ID]).toEqual(once.tools[ID])
      expect(twice).toBe(once)
    })

    it('does not touch other entries or the rest of the state', () => {
      const other: ToolActivity = running('Grep', 50)
      const s = base({ [ID]: running(), toolu_other: other })
      const next = recordN2ToolResult(s, resultP(), 700)
      expect(next.tools.toolu_other).toBe(other)
      expect(next.messages).toBe(s.messages)
      expect(next.lastSeq).toBe(s.lastSeq)
    })
  })

  describe('codex R2 A1: tool_use_id colliding with Object.prototype keys', () => {
    for (const id of ['constructor', '__proto__', 'toString']) {
      it(`N1 with id "${id}" on an empty state → a complete running entry (own key, prototype untouched)`, () => {
        const s = recordN2ToolUse(base(), useP({ tool_use_id: id }), 500)
        expect(Object.hasOwn(s.tools, id)).toBe(true)
        expect(Object.getPrototypeOf(s.tools)).toBe(Object.prototype)
        expect(s.tools[id]).toEqual({ name: 'Read', startedAt: 500, endedAt: null, status: 'running', primaryArg: { key: 'file_path', value: PATH }, known: true })
      })

      it(`N2 with id "${id}" on an empty state → a complete done entry (startedAt 0, endedAt at)`, () => {
        const s = recordN2ToolResult(base(), resultP({ tool_use_id: id }), 700)
        expect(Object.hasOwn(s.tools, id)).toBe(true)
        expect(Object.getPrototypeOf(s.tools)).toBe(Object.prototype)
        expect(s.tools[id]).toEqual({ name: 'Read', startedAt: 0, endedAt: 700, status: 'done', ...expectedFacts })
      })

      it(`N1 then N2 with id "${id}" → the same entry is overlaid, still one own key`, () => {
        const s = recordN2ToolResult(recordN2ToolUse(base(), useP({ tool_use_id: id }), 500), resultP({ tool_use_id: id }), 700)
        expect(Object.keys(s.tools)).toEqual([id])
        expect(s.tools[id]).toEqual({ name: 'Read', startedAt: 500, endedAt: 700, status: 'done', primaryArg: { key: 'file_path', value: PATH }, known: true, ...expectedFacts })
      })
    }
  })

  describe('codex R2 A2: numeric bounds and the diff sanity cap', () => {
    const facts = (over: Record<string, unknown>) => recordN2ToolResult(base({ [ID]: running() }), resultP(over), 700).tools[ID]

    it('duration_ms: 0 → 0; negative / NaN / Infinity → durationMs absent; null still → null', () => {
      expect(facts({ duration_ms: 0 }).durationMs).toBe(0)
      expect(facts({ duration_ms: 1.5 }).durationMs).toBe(1.5)
      for (const duration_ms of [-1, Number.NaN, Number.POSITIVE_INFINITY, Number.NEGATIVE_INFINITY]) {
        expect('durationMs' in facts({ duration_ms }), String(duration_ms)).toBe(false)
      }
      expect(facts({ duration_ms: null }).durationMs).toBeNull()
    })

    it('output: total_lines / total_bytes must be non-negative integers', () => {
      for (const v of [-1, 1.5, Number.NaN, Number.POSITIVE_INFINITY]) {
        expect('output' in facts({ output: { ...OUTPUT, total_lines: v } }), `total_lines ${v}`).toBe(false)
        expect('output' in facts({ output: { ...OUTPUT, total_bytes: v } }), `total_bytes ${v}`).toBe(false)
      }
      expect(facts({ output: { ...OUTPUT, total_lines: 0, total_bytes: 0 } }).output).toEqual({ totalLines: 0, totalBytes: 0, truncated: false, hasNonText: false })
    })

    it('file.lines must be a non-negative integer', () => {
      for (const lines of [-1, 1.5, Number.NaN, Number.POSITIVE_INFINITY]) {
        expect('file' in facts({ file: { path: PATH, lines } }), `lines ${lines}`).toBe(false)
      }
      expect(facts({ file: { path: PATH, lines: 0 } }).file).toEqual({ path: PATH, lines: 0 })
    })

    it('diff.added / removed and every hunk number must be non-negative integers → otherwise whole diff dropped', () => {
      for (const v of [-1, 1.5, Number.NaN, Number.POSITIVE_INFINITY]) {
        expect('diff' in facts({ diff: { ...DIFF, added: v } }), `added ${v}`).toBe(false)
        expect('diff' in facts({ diff: { ...DIFF, removed: v } }), `removed ${v}`).toBe(false)
        for (const k of ['old_start', 'old_lines', 'new_start', 'new_lines']) {
          const t = facts({ diff: { ...DIFF, hunks: [HUNK, { ...HUNK, [k]: v }] } })
          expect('diff' in t, `${k} ${v}`).toBe(false)
          expect(t.durationMs).toBe(26)
        }
      }
      expect(facts({ diff: { ...DIFF, added: 0, removed: 0, hunks: [{ ...HUNK, old_start: 0, old_lines: 0, new_start: 0, new_lines: 0 }] } }).diff)
        .toEqual({ path: PATH, added: 0, removed: 0, truncated: false, hunks: [{ oldStart: 0, oldLines: 0, newStart: 0, newLines: 0, lines: [' hello', '-world', '+nexen'] }] })
    })

    it('codex R2 A3: counts must be safe integers — 2**53 drops the diff / output / file, 2**53 - 1 is kept', () => {
      const unsafe = 2 ** 53
      const max = Number.MAX_SAFE_INTEGER
      expect(Number.isInteger(unsafe)).toBe(true) // the pre-fix guard would have let it through
      const dropped = facts({ diff: { ...DIFF, hunks: [{ ...HUNK, old_start: unsafe }] } })
      expect('diff' in dropped).toBe(false)
      expect(dropped.durationMs).toBe(26)
      expect(facts({ diff: { ...DIFF, hunks: [{ ...HUNK, old_start: max }] } }).diff?.hunks[0].oldStart).toBe(max)
      expect('output' in facts({ output: { ...OUTPUT, total_lines: unsafe } })).toBe(false)
      expect('file' in facts({ file: { path: PATH, lines: unsafe } })).toBe(false)
    })

    it(`hunks totalling more than DIFF_LINES_SANITY_CAP (${DIFF_LINES_SANITY_CAP}) lines → no diff; exactly the cap → kept`, () => {
      expect(DIFF_LINES_SANITY_CAP).toBe(10_000)
      const hunkOf = (n: number) => ({ old_start: 1, old_lines: n, new_start: 1, new_lines: n, lines: Array.from({ length: n }, () => ' x') })
      const over = facts({ diff: { ...DIFF, hunks: [hunkOf(6_000), hunkOf(4_001)] } })
      expect('diff' in over).toBe(false)
      expect(over.durationMs).toBe(26)
      expect(over.file).toEqual({ path: PATH, lines: 4 })
      const atCap = facts({ diff: { ...DIFF, hunks: [hunkOf(6_000), hunkOf(4_000)] } })
      expect(atCap.diff?.hunks.map((h) => h.lines.length)).toEqual([6_000, 4_000])
    })
  })
})
