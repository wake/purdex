// spa/src/lib/nex/n2-replay.test.ts — golden replay of a real Nexen N2
// history page (execution 06GBBX0791PP0RQ4WSWDFY5FPM, Read → Edit → Bash, one
// turn, nexen v0.12.0) through applyDurableEvent, plus the raw-only
// equivalence replay that is the mutation guard for the N-rules (P-B3 plan
// Task 4, spec §4.3 "Equivalence of the two paths").
import { describe, it, expect } from 'vitest'
import { applyDurableEvent, defaultExecutionState, type ExecutionState } from './event-reducer'
import type { ToolActivity } from './tool-activity'
import type { EventsPage } from './types'
import fixture from './__fixtures__/n2-tool-events-06GBBX07.json'

const page = fixture as EventsPage

const READ = 'toolu_01Lx7qX6AhKu6WyY8nvCRcKi'
const EDIT = 'toolu_01GaRhSM7bitfwbtCFxeoYTP'
const BASH = 'toolu_01QggSrAB1mvWdYVjNM62jWa'
const HELLO = '/Users/wake/Workspace/tmp-pb3-fixture/hello.txt'

/**
 * Hard-coded on purpose (plan Task 4): the fixture's 24 items are 3
 * lifecycle (delegated / running / terminal) + 6 N2 (3 tool_use + 3
 * tool_result) + 15 provider passthrough (system 5, assistant 5,
 * rate_limit_event 1, user 3, result 1). The passthrough frames become 15
 * messages and `execution.delegated.brief` adds one synthetic user bubble:
 * 16. Counting it with the same filter the reducer uses would hide a
 * swallowed raw frame; a literal fails loudly.
 */
const EXPECTED_MESSAGES = 16
const EXPECTED_TYPE_COUNTS = { user: 4, system: 5, assistant: 5, rate_limit_event: 1, result: 1 }

/** Raw-path timing from the fixture's own frames (assistant / user created_at). */
const RAW_TIMING = {
  [READ]: { startedAt: 1789759067165, endedAt: 1789759067191 },
  [EDIT]: { startedAt: 1789759079370, endedAt: 1789759079394 },
  [BASH]: { startedAt: 1789759082803, endedAt: 1789759083556 },
} as const

function replay(items: EventsPage['items']): ExecutionState {
  let s = defaultExecutionState()
  for (const ev of items) s = applyDurableEvent(s, ev)
  return s
}

function typeCounts(s: ExecutionState): Record<string, number> {
  const counts: Record<string, number> = {}
  for (const m of s.messages) counts[m.type] = (counts[m.type] ?? 0) + 1
  return counts
}

describe('N2 replay (fixture 06GBBX07)', () => {
  it('fixture is the verbatim last history page: 24 items, seq 909–932, next_cursor 0', () => {
    expect(page.items).toHaveLength(24)
    expect(page.items[0].seq).toBe(909)
    expect(page.items[23].seq).toBe(932)
    expect(page.next_cursor).toBe(0)
    expect(page.items.every((ev) => ev.execution_id === '06GBBX0791PP0RQ4WSWDFY5FPM')).toBe(true)
  })

  describe('golden: full page (raw + N2)', () => {
    const s = replay(page.items)

    it('tools has exactly the three ids', () => {
      expect(Object.keys(s.tools).sort()).toEqual([READ, EDIT, BASH].sort())
    })

    it('Read: full v2 entry with durationMs 26, output + file facts, no diff', () => {
      const read = s.tools[READ]
      expect(read).toEqual({
        name: 'Read',
        startedAt: 1789759067165,
        endedAt: 1789759067191,
        status: 'done',
        primaryArg: { key: 'file_path', value: HELLO },
        known: true,
        durationMs: 26,
        output: { totalLines: 4, totalBytes: 26, truncated: false, hasNonText: false },
        file: { path: HELLO, lines: 4 },
      })
      expect('diff' in read).toBe(false)
    })

    it('Edit: durationMs 24 and the one-hunk diff (+1 −1)', () => {
      const edit = s.tools[EDIT]
      expect(edit).toEqual({
        name: 'Edit',
        startedAt: 1789759079370,
        endedAt: 1789759079394,
        status: 'done',
        primaryArg: { key: 'file_path', value: HELLO },
        known: true,
        durationMs: 24,
        output: { totalLines: 1, totalBytes: 155, truncated: false, hasNonText: false },
        diff: {
          path: HELLO,
          added: 1,
          removed: 1,
          hunks: [{ oldStart: 1, oldLines: 3, newStart: 1, newLines: 3, lines: [' hello', '-world', '+nexen', ' three'] }],
          truncated: false,
        },
      })
      expect('file' in edit).toBe(false)
    })

    it('Bash: durationMs 752, output facts, neither file nor diff', () => {
      const bash = s.tools[BASH]
      expect(bash).toEqual({
        name: 'Bash',
        startedAt: 1789759082803,
        endedAt: 1789759083556,
        status: 'done',
        primaryArg: { key: 'command', value: 'wc -l hello.txt' },
        known: true,
        durationMs: 752,
        output: { totalLines: 1, totalBytes: 18, truncated: false, hasNonText: false },
      })
      expect('file' in bash).toBe(false)
      expect('diff' in bash).toBe(false)
    })

    it('N2 kinds never reach messages: exactly the raw frames + the brief bubble', () => {
      expect(s.messages).toHaveLength(EXPECTED_MESSAGES)
      expect(typeCounts(s)).toEqual(EXPECTED_TYPE_COUNTS)
      // The brief bubble is first (execution.delegated is seq 909).
      expect(s.messages[0]).toMatchObject({ type: 'user', message: { role: 'user' } })
      for (const m of s.messages) {
        const top = m as unknown as Record<string, unknown>
        expect(top.type).not.toBe('tool_use')
        expect(top.type).not.toBe('tool_result')
        expect('kind' in top).toBe(false)
        expect('tool_use_id' in top).toBe(false)
      }
    })

    it('turn state after execution.terminal', () => {
      expect(s.lastSeq).toBe(932)
      expect(s.turnLive).toBe(false)
      expect(s.pendingSend).toBe(false)
      expect(s.partial).toBeNull()
      // No summary was ever loaded, so patchSummary keeps it null but still marks it stale.
      expect(s.summary).toBeNull()
      expect(s.summaryStale).toBe(true)
    })
  })

  describe('equivalence: raw-only replay (tool_use / tool_result filtered out) = P-B2 shape', () => {
    const rawOnly = page.items.filter((ev) => ev.kind !== 'tool_use' && ev.kind !== 'tool_result')
    const golden = replay(page.items)
    const s = replay(rawOnly)

    it('filters exactly the six N2 events', () => {
      expect(rawOnly).toHaveLength(18)
    })

    it('each entry is exactly the P-B2 shape — no overlay keys at all', () => {
      // toEqual, not toMatchObject: an extra durationMs / primaryArg / known /
      // output / file / diff key must fail here. This is the mutation guard.
      expect(s.tools[READ]).toEqual({ name: 'Read', ...RAW_TIMING[READ], status: 'done' })
      expect(s.tools[EDIT]).toEqual({ name: 'Edit', ...RAW_TIMING[EDIT], status: 'done' })
      expect(s.tools[BASH]).toEqual({ name: 'Bash', ...RAW_TIMING[BASH], status: 'done' })
      for (const id of [READ, EDIT, BASH]) {
        expect(Object.keys(s.tools[id]).sort()).toEqual(['endedAt', 'name', 'startedAt', 'status'])
      }
    })

    it('F3: raw endedAt − startedAt agrees with the N2 duration_ms (Bash to within the 1 ms the fixture shows)', () => {
      const elapsed = (t: ToolActivity) => (t.endedAt as number) - t.startedAt
      expect(elapsed(s.tools[READ])).toBe(26)
      expect(elapsed(s.tools[EDIT])).toBe(24)
      // Measured: user 1789759083556 − assistant 1789759082803 = 753, while the
      // daemon's own duration_ms is 752 — sub-ms rounding between the two
      // clocks, which is exactly why R2 prefers durationMs when present.
      expect(elapsed(s.tools[BASH])).toBe(753)
      expect(golden.tools[BASH].durationMs).toBe(752)
      expect(Math.abs(elapsed(s.tools[BASH]) - (golden.tools[BASH].durationMs as number))).toBeLessThanOrEqual(1)
    })

    it('messages are identical to the golden replay (same literal count, same frames)', () => {
      expect(s.messages).toHaveLength(EXPECTED_MESSAGES)
      expect(typeCounts(s)).toEqual(EXPECTED_TYPE_COUNTS)
      expect(s.messages).toEqual(golden.messages)
    })

    it('both paths agree per id on status / startedAt / endedAt', () => {
      expect(Object.keys(s.tools).sort()).toEqual(Object.keys(golden.tools).sort())
      for (const id of [READ, EDIT, BASH]) {
        const a = s.tools[id]
        const b = golden.tools[id]
        expect({ status: a.status, startedAt: a.startedAt, endedAt: a.endedAt }).toEqual({
          status: b.status,
          startedAt: b.startedAt,
          endedAt: b.endedAt,
        })
      }
      expect(s.lastSeq).toBe(golden.lastSeq)
      expect(s.turnLive).toBe(false)
      expect(s.pendingSend).toBe(false)
    })
  })
})
