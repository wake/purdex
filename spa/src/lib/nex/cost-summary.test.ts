// spa/src/lib/nex/cost-summary.test.ts
// P-B4 spec §4.1 C1–C7 + C3a, plan Task 2. Expected numbers were measured
// from the fixtures with a standalone script before these tests were written.
import { describe, expect, it } from 'vitest'

import turnsFixture from './__fixtures__/cost-turns-06GB2ZFD.json'
import subagentFixture from './__fixtures__/cost-subagent-06GBGXTW.json'
import { costSummary, type TokenTotals } from './cost-summary'
import type { StreamMessage } from './message-types'

interface FixtureItem { seq: number; kind: string; payload: unknown }
interface Fixture { items: FixtureItem[] }

/** What the reducer pushes: every non-lifecycle payload, in seq order. */
function payloads(f: Fixture): StreamMessage[] {
  return f.items
    .filter((i) => !i.kind.startsWith('execution.') && !i.kind.startsWith('lease.'))
    .map((i) => i.payload as StreamMessage)
}

const turns = payloads(turnsFixture as Fixture)
const subagent = payloads(subagentFixture as Fixture)

const ZERO: TokenTotals = { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }

const result = (extra: Record<string, unknown>): StreamMessage =>
  ({ type: 'result', ...extra }) as StreamMessage

const validEntry = {
  inputTokens: 10, outputTokens: 20, cacheReadInputTokens: 30, cacheCreationInputTokens: 40,
  costUSD: 0.5, canonicalModel: 'claude-sonnet-5',
}

describe('costSummary — 12-turn fixture (06GB2ZFD)', () => {
  const s = costSummary(turns)

  it('finds 12 top-level result turns indexed 1..12 in seq order', () => {
    expect(s.turns).toHaveLength(12)
    expect(s.turns.map((t) => t.index)).toEqual([1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12])
  })

  // R4 T2.1: seq 974 continues seq 958 (sonnet outputTokens 598 → 701, Δ 103 =
  // 974's usage.output_tokens), so 974 is billed as its delta. Old rule (Σ every
  // total_cost_usd) gave 0.2576558; the evidence rule gives 0.1973346 (−0.0603212,
  // 958's total counted twice). apiMs 65032 → 57618, output 4478 → 3880.
  it('totals: old Σ 0.2576558 → evidence rule 0.1973346; API ms and output tokens are deltas on seq 974', () => {
    expect(s.totalUsd).toBeCloseTo(0.1973346, 7)
    expect(s.apiMs).toBe(57618)
    expect(s.durationMs).toBe(93086)
    expect(s.rounds).toBe(22)
    expect(s.tokens.output).toBe(3880)
  })

  it('ten independent frames plus seq 974 as the continuation of seq 958 (turn 12 = 0.0114)', () => {
    expect(s.turns[10].costUsd).toBeCloseTo(0.0603212, 7)
    expect(s.turns[11].costUsd).toBeCloseTo(0.0717194 - 0.0603212, 7)
    expect(s.turns[11].costUsd).toBeCloseTo(0.0113982, 7)
    // Every other turn is billed at its own total_cost_usd.
    expect(s.turns.slice(0, 11).map((t) => t.costUsd)).toEqual([
      0.0299658, 0.00952, 0.0106372, 0.0090934, 0.010503399999999998, 0, 0.0294018, 0, 0.0207592,
      0.005734400000000001, 0.0603212,
    ])
  })

  it('a model disappearing breaks continuation (seq 8 → 17: haiku gone, sonnet 364 → 438)', () => {
    // 438 ≥ 364 and Δ 74 < 438 anyway, but the missing haiku alone disqualifies it.
    expect(s.turns[1].costUsd).toBe(0.00952)
    expect(s.turns[1].tokens?.output).toBe(438)
  })

  it('turn 1 (seq 8) sums modelUsage, not usage (F5), and lists canonical models', () => {
    const t = s.turns[0]
    expect(t.tokens?.output).toBe(376)
    expect(t.tokens?.input).toBe(911)
    expect(t.models).toEqual(['claude-haiku-4-5', 'claude-sonnet-5'])
    expect(t.isError).toBe(false)
    expect(t.costUsd).toBeCloseTo(0.0299658, 7)
  })

  it('turn 12 (seq 974) tokens and API ms are deltas against seq 958 (cacheRead 80127 − 33726 = 46401)', () => {
    expect(s.turns[11].tokens).toEqual({ input: 4, output: 103, cacheRead: 46401, cacheWrite: 270 })
    expect(s.turns[11].apiMs).toBe(9621 - 7414)
    // num_turns and duration_ms are always per-frame.
    expect(s.turns[11].rounds).toBe(2)
    expect(s.turns[11].durationMs).toBe(9488)
  })

  it('turn 6 (seq 64) is an error turn with zero-token usage fallback (not null)', () => {
    const t = s.turns[5]
    expect(t.subtype).toBe('error_during_execution')
    expect(t.isError).toBe(true)
    expect(t.costUsd).toBe(0)
    expect(t.apiMs).toBe(0)
    expect(t.rounds).toBe(2)
    expect(t.tokens).toEqual(ZERO)
    expect(t.models).toEqual([])
  })

  it('turn 8 (seq 100) is the interrupted turn: cost 0, rounds 0, 10 ms', () => {
    const t = s.turns[7]
    expect(t.costUsd).toBe(0)
    expect(t.rounds).toBe(0)
    expect(t.durationMs).toBe(10)
    expect(t.isError).toBe(false)
  })

  it('models: two entries sorted by cost desc, haiku cost, Σ ≈ totalUsd (F4)', () => {
    expect(s.models.map((m) => m.model)).toEqual(['claude-sonnet-5', 'claude-haiku-4-5'])
    expect(s.models[1].costUsd).toBeCloseTo(0.000969, 7)
    expect(s.models[0].costUsd).toBeGreaterThan(s.models[1].costUsd)
    const sum = s.models.reduce((a, m) => a + m.costUsd, 0)
    expect(sum).toBeCloseTo(s.totalUsd, 7)
    expect(s.models[1].tokens).toEqual({ input: 909, output: 12, cacheRead: 0, cacheWrite: 0 })
  })

  it('no turn on the fixture is unsplit (zero-cost turns without a split do not count)', () => {
    expect(s.unsplitTurns).toBe(0)
  })

  it('is idempotent on the same input (C7)', () => {
    expect(costSummary(turns)).toEqual(s)
  })
})

describe('costSummary — subagent fixture (06GBGXTW)', () => {
  it('has one top-level turn carrying the whole spend (F6)', () => {
    const s = costSummary(subagent)
    expect(s.turns).toHaveLength(1)
    expect(s.totalUsd).toBeCloseTo(0.0881726, 7)
  })

  it('mutation guard: a subagent-scoped result (parent_tool_use_id set) is ignored (C1)', () => {
    const base = costSummary(subagent)
    const withSub = costSummary([
      ...subagent,
      result({ parent_tool_use_id: 'toolu_x', total_cost_usd: 1, subtype: 'success' }),
    ])
    expect(withSub.turns).toHaveLength(1)
    expect(withSub.totalUsd).toBe(base.totalUsd)
    expect(withSub.turns).toEqual(base.turns)
  })
})

describe('costSummary — C3a isError', () => {
  it('is_error true without subtype → true', () => {
    const [t] = costSummary([result({ is_error: true })]).turns
    expect(t.isError).toBe(true)
    expect(t.subtype).toBe('')
  })

  it("subtype 'success' with is_error false → false", () => {
    const [t] = costSummary([result({ subtype: 'success', is_error: false })]).turns
    expect(t.isError).toBe(false)
  })

  it("non-success subtype alone → true", () => {
    const [t] = costSummary([result({ subtype: 'error_max_turns' })]).turns
    expect(t.isError).toBe(true)
  })
})

describe('costSummary — C3 usage fallback', () => {
  it('usage only (no modelUsage) → tokens from usage, models empty, unsplit when costed', () => {
    const s = costSummary([
      result({
        total_cost_usd: 0.01,
        usage: { input_tokens: 1, output_tokens: 2, cache_read_input_tokens: 3, cache_creation_input_tokens: 4 },
      }),
    ])
    expect(s.turns[0].tokens).toEqual({ input: 1, output: 2, cacheRead: 3, cacheWrite: 4 })
    expect(s.turns[0].models).toEqual([])
    expect(s.tokens).toEqual({ input: 1, output: 2, cacheRead: 3, cacheWrite: 4 })
    expect(s.models).toEqual([])
    expect(s.unsplitTurns).toBe(1)
  })

  it('usage only with cost 0 → not counted as unsplit', () => {
    const s = costSummary([result({ total_cost_usd: 0, usage: { output_tokens: 5 } })])
    expect(s.unsplitTurns).toBe(0)
  })

  it('neither usage nor modelUsage → tokens null; unsplit when costed', () => {
    const s = costSummary([result({ total_cost_usd: 0.02 })])
    expect(s.turns[0].tokens).toBeNull()
    expect(s.turns[0].models).toEqual([])
    expect(s.tokens).toEqual(ZERO)
    expect(s.unsplitTurns).toBe(1)
  })

  it('usage with only output_tokens → the other three are 0', () => {
    const [t] = costSummary([result({ usage: { output_tokens: 7 } })]).turns
    expect(t.tokens).toEqual({ input: 0, output: 7, cacheRead: 0, cacheWrite: 0 })
  })

  it('usage whose fields are all invalid → null', () => {
    const [t] = costSummary([result({ usage: { output_tokens: 'x', input_tokens: -1 } })]).turns
    expect(t.tokens).toBeNull()
  })

  // Codex R2 A2: an ABSENT key is 0, a PRESENT-but-invalid key poisons the whole fallback.
  describe('mixed-shape usage (A2)', () => {
    it('absent keys → 0 (only output_tokens present)', () => {
      const [t] = costSummary([result({ usage: { output_tokens: 10 } })]).turns
      expect(t.tokens).toEqual({ input: 0, output: 10, cacheRead: 0, cacheWrite: 0 })
    })

    it('present but non-numeric key alongside a valid one → null, not 0', () => {
      const [t] = costSummary([result({ usage: { output_tokens: 10, input_tokens: '100' } })]).turns
      expect(t.tokens).toBeNull()
    })

    it.each([[-1], [NaN], [Infinity], [null], ['5']])('sole key output_tokens=%s → null', (v) => {
      const [t] = costSummary([result({ usage: { output_tokens: v } })]).turns
      expect(t.tokens).toBeNull()
    })

    it('all four keys absent ({}) → null', () => {
      const [t] = costSummary([result({ usage: {} })]).turns
      expect(t.tokens).toBeNull()
    })
  })
})

describe('costSummary — C3 partially valid modelUsage', () => {
  it('skips the malformed entry and keeps the valid one', () => {
    const s = costSummary([
      result({
        total_cost_usd: 0.5,
        modelUsage: {
          'claude-sonnet-5': validEntry,
          'claude-haiku-4-5-20251001': { ...validEntry, outputTokens: 'x', canonicalModel: 'claude-haiku-4-5' },
        },
      }),
    ])
    const [t] = s.turns
    expect(t.tokens).toEqual({ input: 10, output: 20, cacheRead: 30, cacheWrite: 40 })
    expect(t.models).toEqual(['claude-sonnet-5'])
    expect(s.models).toEqual([{ model: 'claude-sonnet-5', costUsd: 0.5, tokens: { input: 10, output: 20, cacheRead: 30, cacheWrite: 40 } }])
    expect(s.unsplitTurns).toBe(0)
  })

  it('all entries malformed + usage present → usage fallback and unsplit +1', () => {
    const s = costSummary([
      result({
        total_cost_usd: 0.5,
        modelUsage: { a: { ...validEntry, costUSD: NaN }, b: null, c: 'str', d: { ...validEntry, inputTokens: -1 } },
        usage: { input_tokens: 1, output_tokens: 2, cache_read_input_tokens: 3, cache_creation_input_tokens: 4 },
      }),
    ])
    expect(s.turns[0].tokens).toEqual({ input: 1, output: 2, cacheRead: 3, cacheWrite: 4 })
    expect(s.turns[0].models).toEqual([])
    expect(s.models).toEqual([])
    expect(s.unsplitTurns).toBe(1)
  })

  it('modelUsage: [] behaves as absent', () => {
    const s = costSummary([result({ total_cost_usd: 0.1, modelUsage: [], usage: { output_tokens: 1 } })])
    expect(s.turns[0].tokens).toEqual({ input: 0, output: 1, cacheRead: 0, cacheWrite: 0 })
    expect(s.unsplitTurns).toBe(1)
  })

  it('modelUsage: null behaves as absent', () => {
    const s = costSummary([result({ total_cost_usd: 0.1, modelUsage: null })])
    expect(s.turns[0].tokens).toBeNull()
    expect(s.unsplitTurns).toBe(1)
  })

  it('falls back to the modelUsage key when canonicalModel is missing; merges same canonical across turns', () => {
    const s = costSummary([
      result({ modelUsage: { 'claude-opus-4-20250514': { ...validEntry, canonicalModel: undefined } } }),
      result({ modelUsage: { 'claude-opus-4-20250514': { ...validEntry, canonicalModel: 'claude-opus-4' } } }),
      result({ modelUsage: { 'claude-opus-4-20250601': { ...validEntry, canonicalModel: 'claude-opus-4' } } }),
    ])
    expect(s.turns[0].models).toEqual(['claude-opus-4-20250514'])
    expect(s.models.map((m) => [m.model, m.costUsd])).toEqual([
      ['claude-opus-4', 1],
      ['claude-opus-4-20250514', 0.5],
    ])
    expect(s.models[0].tokens).toEqual({ input: 20, output: 40, cacheRead: 60, cacheWrite: 80 })
  })
})

describe('costSummary — C6 hostile shapes', () => {
  it.each([['x'], [NaN], [-1], [Infinity], [null], [undefined]])('total_cost_usd %s → 0', (v) => {
    const s = costSummary([result({ total_cost_usd: v })])
    expect(s.turns[0].costUsd).toBe(0)
    expect(s.totalUsd).toBe(0)
  })

  it('num_turns 1.5 is kept (finite ≥ 0; integers not required)', () => {
    const s = costSummary([result({ num_turns: 1.5 })])
    expect(s.turns[0].rounds).toBe(1.5)
    expect(s.rounds).toBe(1.5)
  })

  it('duration_ms / duration_api_ms / num_turns invalid → null per turn, 0 in totals', () => {
    const s = costSummary([result({ duration_ms: 'x', duration_api_ms: -5, num_turns: NaN })])
    expect(s.turns[0].durationMs).toBeNull()
    expect(s.turns[0].apiMs).toBeNull()
    expect(s.turns[0].rounds).toBeNull()
    expect(s.durationMs).toBe(0)
    expect(s.apiMs).toBe(0)
    expect(s.rounds).toBe(0)
  })

  it('{type: "result"} alone does not throw and yields zeros / nulls', () => {
    const s = costSummary([result({})])
    expect(s.turns).toHaveLength(1)
    expect(s.turns[0]).toEqual({
      index: 1, costUsd: 0, tokens: null, durationMs: null, apiMs: null, rounds: null,
      subtype: '', isError: false, models: [],
    })
    expect(s.totalUsd).toBe(0)
    expect(Number.isNaN(s.totalUsd)).toBe(false)
  })

  it('modelUsage with a __proto__ key and inherited props: only own keys are read', () => {
    const inherited = Object.create({ ghost: validEntry }) as Record<string, unknown>
    inherited.real = validEntry
    const viaProto = JSON.parse('{"__proto__": {"inputTokens": 1}, "real": ' + JSON.stringify(validEntry) + '}') as unknown
    const s = costSummary([result({ modelUsage: inherited }), result({ modelUsage: viaProto })])
    expect(s.turns[0].models).toEqual(['claude-sonnet-5'])
    expect(s.turns[1].models).toEqual(['claude-sonnet-5'])
    expect(s.models).toEqual([{ model: 'claude-sonnet-5', costUsd: 1, tokens: { input: 20, output: 40, cacheRead: 60, cacheWrite: 80 } }])
  })

  it('usage / modelUsage of primitive type do not throw', () => {
    expect(() => costSummary([result({ usage: 3, modelUsage: 'x', subtype: 5, is_error: 'yes' })])).not.toThrow()
    const [t] = costSummary([result({ usage: 3, modelUsage: 'x', subtype: 5, is_error: 'yes' })]).turns
    expect(t.tokens).toBeNull()
    expect(t.subtype).toBe('')
    expect(t.isError).toBe(false)
  })
})

// Codex R2 A1: sums must stay finite — a contribution that would overflow to
// ±Infinity is dropped and the running total kept.
describe('costSummary — overflow stays finite (A1)', () => {
  const big = Number.MAX_VALUE

  it('overflow clamps at MAX_VALUE regardless of message order (codex re-review P2)', () => {
    const a = result({ total_cost_usd: 1e308 })
    const b = result({ total_cost_usd: Number.MAX_VALUE })
    expect(costSummary([a, b]).totalUsd).toBe(Number.MAX_VALUE)
    expect(costSummary([b, a]).totalUsd).toBe(Number.MAX_VALUE)
  })

  it('two MAX_VALUE total_cost_usd → totalUsd is MAX_VALUE, not Infinity', () => {
    const s = costSummary([result({ total_cost_usd: big }), result({ total_cost_usd: big })])
    expect(Number.isFinite(s.totalUsd)).toBe(true)
    expect(s.totalUsd).toBe(big)
  })

  it('two modelUsage entries each with MAX_VALUE outputTokens → per-turn and total output finite', () => {
    const s = costSummary([
      result({
        modelUsage: {
          a: { ...validEntry, outputTokens: big, canonicalModel: 'a' },
          b: { ...validEntry, outputTokens: big, canonicalModel: 'b' },
        },
      }),
    ])
    expect(Number.isFinite(s.turns[0].tokens?.output)).toBe(true)
    expect(s.turns[0].tokens?.output).toBe(big)
    expect(Number.isFinite(s.tokens.output)).toBe(true)
    expect(s.tokens.output).toBe(big)
  })

  it('same canonical model across two turns with MAX_VALUE costUSD / tokens → models[] finite', () => {
    const e = { ...validEntry, costUSD: big, inputTokens: big, canonicalModel: 'm' }
    const s = costSummary([result({ modelUsage: { m: e } }), result({ modelUsage: { m: e } })])
    expect(s.models).toHaveLength(1)
    expect(s.models[0].costUsd).toBe(big)
    expect(s.models[0].tokens.input).toBe(big)
  })

  it('usage fallback tokens across turns → total finite', () => {
    const s = costSummary([result({ usage: { output_tokens: big } }), result({ usage: { output_tokens: big } })])
    expect(s.tokens.output).toBe(big)
  })

  it('duration_ms / duration_api_ms / num_turns overflow → totals finite', () => {
    const s = costSummary([
      result({ duration_ms: big, duration_api_ms: big, num_turns: big }),
      result({ duration_ms: big, duration_api_ms: big, num_turns: big }),
    ])
    expect(s.durationMs).toBe(big)
    expect(s.apiMs).toBe(big)
    expect(s.rounds).toBe(big)
  })
})

describe('costSummary — non-result messages and empty input', () => {
  it('assistant / user / system frames contribute nothing', () => {
    const s = costSummary([
      { type: 'assistant', message: { role: 'assistant', content: [], stop_reason: null } },
      { type: 'user', message: { role: 'user', content: [], stop_reason: null } },
      { type: 'system', subtype: 'init' },
      { type: 'rate_limit_event', total_cost_usd: 5 } as StreamMessage,
    ])
    expect(s.turns).toEqual([])
    expect(s.totalUsd).toBe(0)
  })

  it('[] → all zeros', () => {
    expect(costSummary([])).toEqual({
      turns: [], totalUsd: 0, tokens: ZERO, durationMs: 0, apiMs: 0, rounds: 0, models: [], unsplitTurns: 0,
    })
  })
})

// R4 T2.1 — the shared continuation rule (agreed with Nexen v0.13.2 chainCost).
describe('costSummary — a frame must prove it continues the previous one', () => {
  /** One-model frame: `out` = modelUsage outputTokens, `own` = usage.output_tokens. */
  const frame = (
    cost: unknown,
    out: number,
    own: number | undefined,
    extra: Record<string, unknown> = {},
    model = 'm',
  ): StreamMessage =>
    result({
      total_cost_usd: cost,
      modelUsage: {
        [model]: {
          inputTokens: 1, outputTokens: out, cacheReadInputTokens: 0, cacheCreationInputTokens: 0,
          costUSD: typeof cost === 'number' && Number.isFinite(cost) && cost >= 0 ? cost : 0, canonicalModel: model,
        },
      },
      ...(own === undefined ? {} : { usage: { output_tokens: own } }),
      ...extra,
    })

  it('Nexen spec §1.2: 0.0528 / 0.0643 / 0.1126 cumulative → turns 0.0528 / 0.0115 / 0.0483, total 0.1126', () => {
    // inputTokens 18 / 36 / 46 as measured. outputTokens are constructed so each
    // frame provably continues the last: 120 → 170 → 260 with usage.output_tokens
    // 120 / 50 / 90 (every Δ equals that frame's own output).
    const mu = (input: number, out: number, cost: number) => ({
      'claude-sonnet-5': {
        inputTokens: input, outputTokens: out, cacheReadInputTokens: 0, cacheCreationInputTokens: 0,
        costUSD: cost, canonicalModel: 'claude-sonnet-5',
      },
    })
    const s = costSummary([
      result({ total_cost_usd: 0.0528, modelUsage: mu(18, 120, 0.0528), usage: { output_tokens: 120 } }),
      result({ total_cost_usd: 0.0643, modelUsage: mu(36, 170, 0.0643), usage: { output_tokens: 50 } }),
      result({ total_cost_usd: 0.1126, modelUsage: mu(46, 260, 0.1126), usage: { output_tokens: 90 } }),
    ])
    expect(s.turns.map((t) => t.costUsd)).toEqual([
      0.0528, expect.closeTo(0.0115, 10), expect.closeTo(0.0483, 10),
    ])
    expect(s.totalUsd).toBeCloseTo(0.1126, 10)
    expect(s.turns.map((t) => t.tokens?.input)).toEqual([18, 18, 10])
    expect(s.turns.map((t) => t.tokens?.output)).toEqual([120, 50, 90])
    expect(s.models).toHaveLength(1)
    expect(s.models[0].costUsd).toBeCloseTo(0.1126, 10)
    expect(s.models[0].tokens).toEqual({ input: 46, output: 260, cacheRead: 0, cacheWrite: 0 })
  })

  it('rule 2: the first frame with modelUsage is independent even if it could be a running total', () => {
    const s = costSummary([frame(0.3, 500, 10)])
    expect(s.totalUsd).toBe(0.3)
  })

  it("rule 3: Σ output growth below this frame's usage.output_tokens → independent", () => {
    const s = costSummary([frame(0.1, 100, 100), frame(0.15, 150, 51)])
    expect(s.turns.map((t) => t.costUsd)).toEqual([0.1, 0.15])
    expect(s.totalUsd).toBeCloseTo(0.25, 10)
  })

  it("rule 3: a new model's output counts toward the growth (prev missing = 0)", () => {
    const two = result({
      total_cost_usd: 0.25,
      modelUsage: {
        m: { ...validEntry, outputTokens: 100, costUSD: 0.1, canonicalModel: 'm' },
        n: { ...validEntry, outputTokens: 30, costUSD: 0.15, canonicalModel: 'n' },
      },
      usage: { output_tokens: 30 },
    })
    const s = costSummary([frame(0.1, 100, 100), two])
    expect(s.turns[1].costUsd).toBeCloseTo(0.15, 10)
    expect(s.turns[1].models).toEqual(['m', 'n'])
  })

  it('rule 3: a model missing from this frame breaks continuation', () => {
    const first = result({
      total_cost_usd: 0.2,
      modelUsage: {
        m: { ...validEntry, outputTokens: 100, costUSD: 0.1, canonicalModel: 'm' },
        h: { ...validEntry, outputTokens: 5, costUSD: 0.1, canonicalModel: 'h' },
      },
      usage: { output_tokens: 100 },
    })
    const s = costSummary([first, frame(0.3, 200, 100)])
    expect(s.turns[1].costUsd).toBe(0.3)
    expect(s.totalUsd).toBeCloseTo(0.5, 10)
  })

  it('rule 1: a costed frame without modelUsage contributes on its own and never becomes prev', () => {
    const s = costSummary([
      frame(0.1, 100, 100),
      result({ total_cost_usd: 0.07, usage: { output_tokens: 3 } }),
      frame(0.3, 150, 50),
    ])
    // The third frame continues the FIRST (Δ 0.2), not the usage-only one.
    expect(s.turns.map((t) => t.costUsd)).toEqual([0.1, 0.07, expect.closeTo(0.2, 10)])
    expect(s.totalUsd).toBeCloseTo(0.37, 10)
  })

  it('rule 1: a zero-cost frame between two cumulative frames does not break continuation (fixture seq 64 shape)', () => {
    const s = costSummary([
      frame(0.1, 100, 100),
      result({ total_cost_usd: 0, modelUsage: {}, usage: { output_tokens: 0 }, subtype: 'error_during_execution' }),
      frame(0.3, 150, 50),
    ])
    expect(s.turns.map((t) => t.costUsd)).toEqual([0.1, 0, expect.closeTo(0.2, 10)])
    expect(s.totalUsd).toBeCloseTo(0.3, 10)
  })

  it.each([[undefined], [null], ['0.5'], [NaN], [Infinity]])(
    'rule 1: modelUsage with total_cost_usd %s contributes nothing and is not prev',
    (bad) => {
      const s = costSummary([frame(0.1, 100, 100), frame(bad, 1000, 900), frame(0.3, 150, 50)])
      // Had the middle frame become prev, 150 < 1000 would make the third independent (0.3).
      expect(s.turns.map((t) => t.costUsd)).toEqual([0.1, 0, expect.closeTo(0.2, 10)])
      expect(s.totalUsd).toBeCloseTo(0.3, 10)
    },
  )

  it('rule 4: token evidence says cumulative but the cost went down → independent, and it becomes prev', () => {
    const s = costSummary([frame(0.5, 100, 100), frame(0.2, 150, 50), frame(0.3, 200, 50)])
    expect(s.turns.map((t) => t.costUsd)).toEqual([0.5, 0.2, expect.closeTo(0.1, 10)])
    expect(s.totalUsd).toBeCloseTo(0.8, 10)
  })

  it("breakdown: a cumulative frame's per-model cost, tokens and API ms are deltas, each clamped at 0", () => {
    const first = result({
      total_cost_usd: 0.3,
      duration_api_ms: 5000,
      duration_ms: 6000,
      num_turns: 3,
      modelUsage: {
        a: { inputTokens: 100, outputTokens: 10, cacheReadInputTokens: 50, cacheCreationInputTokens: 5, costUSD: 0.3, canonicalModel: 'a' },
      },
      usage: { output_tokens: 10 },
    })
    const second = result({
      total_cost_usd: 0.5,
      duration_api_ms: 4000, // went down → clamped at 0
      duration_ms: 2000,
      num_turns: 1,
      modelUsage: {
        // a's costUSD went down → clamped at 0. (Its counters cannot shrink: rule 3 would make the frame independent.)
        a: { inputTokens: 100, outputTokens: 20, cacheReadInputTokens: 70, cacheCreationInputTokens: 5, costUSD: 0.2, canonicalModel: 'a' },
        b: { inputTokens: 7, outputTokens: 5, cacheReadInputTokens: 0, cacheCreationInputTokens: 1, costUSD: 0.3, canonicalModel: 'b' },
      },
      usage: { output_tokens: 15 },
    })
    const s = costSummary([first, second])
    const t = s.turns[1]
    expect(t.costUsd).toBeCloseTo(0.2, 10)
    expect(t.tokens).toEqual({ input: 7, output: 15, cacheRead: 20, cacheWrite: 1 })
    expect(t.apiMs).toBe(0)
    expect(t.durationMs).toBe(2000)
    expect(t.rounds).toBe(1)
    expect(s.apiMs).toBe(5000)
    expect(s.durationMs).toBe(8000)
    expect(s.rounds).toBe(4)
    const byName = Object.fromEntries(s.models.map((m) => [m.model, m]))
    expect(byName.a.costUsd).toBe(0.3)
    expect(byName.a.tokens).toEqual({ input: 100, output: 20, cacheRead: 70, cacheWrite: 5 })
    expect(byName.b.costUsd).toBe(0.3)
    expect(byName.b.tokens).toEqual({ input: 7, output: 5, cacheRead: 0, cacheWrite: 1 })
  })

  it('edge (a): outputTokens compare as plain numbers — 10.5 → 10.25 is a decrease, not "10 → 10"', () => {
    const s = costSummary([frame(0.1, 10.5, 0), frame(0.3, 10.25, 0)])
    expect(s.turns[1].costUsd).toBe(0.3)
    // …and 10.5 → 10.75 with usage 0.25 continues.
    const c = costSummary([frame(0.1, 10.5, 0), frame(0.3, 10.75, 0.25)])
    expect(c.turns[1].costUsd).toBeCloseTo(0.2, 10)
  })

  it.each([
    ['modelUsage not an object', 'x'],
    ['a model value not an object', { m: 5 }],
    ['outputTokens a string', { m: { ...validEntry, outputTokens: '20', canonicalModel: 'm' } }],
    ['outputTokens missing', { m: { ...validEntry, outputTokens: undefined, canonicalModel: 'm' } }],
    ['outputTokens negative', { m: { ...validEntry, outputTokens: -1, canonicalModel: 'm' } }],
    ['outputTokens non-finite', { m: { ...validEntry, outputTokens: Infinity, canonicalModel: 'm' } }],
    ['one of two models malformed', { m: { ...validEntry, outputTokens: 5000, canonicalModel: 'm' }, h: null }],
  ])('edge (b): %s → the frame counts as having no modelUsage (contributes its cost, not prev, never dropped)', (_, mu) => {
    const s = costSummary([
      frame(0.1, 100, 100),
      result({ total_cost_usd: 0.05, modelUsage: mu, usage: { output_tokens: 1 } }),
      frame(0.3, 150, 50),
    ])
    expect(s.turns.map((t) => t.costUsd)).toEqual([0.1, 0.05, expect.closeTo(0.2, 10)])
    expect(s.totalUsd).toBeCloseTo(0.35, 10)
  })

  it('edge (c): modelUsage with a negative total_cost_usd contributes nothing and is not prev', () => {
    const s = costSummary([frame(0.1, 100, 100), frame(-1, 1000, 900), frame(0.3, 150, 50)])
    expect(s.turns.map((t) => t.costUsd)).toEqual([0.1, 0, expect.closeTo(0.2, 10)])
    expect(s.totalUsd).toBeCloseTo(0.3, 10)
  })

  it.each([
    ['missing', undefined],
    ['a string', 'x'],
    ['negative', -5],
    ['NaN', NaN],
  ])('edge (d): usage.output_tokens %s → 0 (no growth still continues)', (_, own) => {
    const second = result({
      total_cost_usd: 0.3,
      modelUsage: { m: { ...validEntry, outputTokens: 100, costUSD: 0.3, canonicalModel: 'm' } },
      ...(own === undefined ? {} : { usage: { output_tokens: own } }),
    })
    const s = costSummary([frame(0.1, 100, 100), second])
    expect(s.turns[1].costUsd).toBeCloseTo(0.2, 10)
    // Control: a real own output of 1 with no growth is independent.
    const ctl = costSummary([frame(0.1, 100, 100), frame(0.3, 100, 1)])
    expect(ctl.turns[1].costUsd).toBe(0.3)
  })

  /** One-model frame with all four counters and its own cost. */
  const full = (
    cost: number,
    c: { input: number; output: number; cacheRead?: number; cacheWrite?: number },
    own: number,
    key = 's',
    canonical: string | undefined = key,
  ): StreamMessage =>
    result({
      total_cost_usd: cost,
      modelUsage: {
        [key]: {
          inputTokens: c.input, outputTokens: c.output, cacheReadInputTokens: c.cacheRead ?? 0,
          cacheCreationInputTokens: c.cacheWrite ?? 0, costUSD: cost,
          ...(canonical === undefined ? {} : { canonicalModel: canonical }),
        },
      },
      usage: { output_tokens: own },
    })

  it.each([
    ['costUSD NaN', { costUSD: NaN }],
    ['inputTokens a string', { inputTokens: '1' }],
    ['cacheReadInputTokens negative', { cacheReadInputTokens: -1 }],
    ['cacheCreationInputTokens missing', { cacheCreationInputTokens: undefined }],
  ])('rule 1 edge: any model with %s → the frame has no modelUsage (not prev, contributes its own cost)', (_, bad) => {
    const s = costSummary([
      frame(0.1, 100, 100),
      result({ total_cost_usd: 0.05, modelUsage: { m: { ...validEntry, outputTokens: 5000, canonicalModel: 'm', ...bad } }, usage: { output_tokens: 1 } }),
      frame(0.3, 150, 50),
    ])
    expect(s.turns.map((t) => t.costUsd)).toEqual([0.1, 0.05, expect.closeTo(0.2, 10)])
    expect(s.totalUsd).toBeCloseTo(0.35, 10)
  })

  it('review #6: a model with outputTokens but an invalid costUSD cannot become prev, so Σ per-model cost never exceeds totalUsd', () => {
    const s = costSummary([
      full(0.05, { input: 1, output: 50 }, 50, 'm'),
      result({
        total_cost_usd: 0.1,
        modelUsage: { m: { inputTokens: 2, outputTokens: 100, cacheReadInputTokens: 0, cacheCreationInputTokens: 0, costUSD: NaN, canonicalModel: 'm' } },
        usage: { output_tokens: 50 },
      }),
      full(0.3, { input: 3, output: 150 }, 50, 'm'),
    ])
    // Third continues the FIRST (Δ 0.25), not the malformed second (which would have left m's raw 0.3 in the breakdown).
    expect(s.turns.map((t) => t.costUsd)).toEqual([0.05, 0.1, expect.closeTo(0.25, 10)])
    expect(s.totalUsd).toBeCloseTo(0.4, 10)
    const perModel = s.models.reduce((a, m) => a + m.costUsd, 0)
    expect(perModel).toBeLessThanOrEqual(s.totalUsd + 1e-12)
    expect(perModel).toBeCloseTo(0.3, 10)
  })

  it('review #4: a $0 all-zero frame between 0.0528 and a 0.1126 running total does not become prev → total 0.1126', () => {
    const s = costSummary([
      full(0.0528, { input: 18, output: 120 }, 120),
      full(0, { input: 0, output: 0 }, 0),
      full(0.1126, { input: 46, output: 260 }, 140),
    ])
    expect(s.turns.map((t) => t.costUsd)).toEqual([0.0528, 0, expect.closeTo(0.0598, 10)])
    expect(s.totalUsd).toBeCloseTo(0.1126, 10)
  })

  it('review #2: an interrupted $0.04 frame with 0 output then an independent $0.10 frame → 0.14', () => {
    const s = costSummary([
      full(0.04, { input: 50, output: 0, cacheRead: 100 }, 0),
      full(0.1, { input: 60, output: 30, cacheRead: 200 }, 30),
    ])
    expect(s.turns.map((t) => t.costUsd)).toEqual([0.04, 0.1])
    expect(s.totalUsd).toBeCloseTo(0.14, 10)
  })

  it('rule 3: a costed 0-output frame still contributes (independent) while prev stays', () => {
    const s = costSummary([
      full(0.1, { input: 10, output: 100 }, 100),
      full(0.03, { input: 40, output: 0 }, 0),
      full(0.3, { input: 30, output: 150 }, 50),
    ])
    // Middle: its output 0 < prev's 100 → independent (0.03); Σ output 0 → not prev, so the third continues the first.
    expect(s.turns.map((t) => t.costUsd)).toEqual([0.1, 0.03, expect.closeTo(0.2, 10)])
    expect(s.totalUsd).toBeCloseTo(0.33, 10)
  })

  it('rule 3 keying: models match by canonicalModel (claude-sonnet-5 → claude-sonnet-5[1m])', () => {
    const s = costSummary([
      full(0.1, { input: 10, output: 100 }, 100, 'claude-sonnet-5', 'claude-sonnet-5'),
      full(0.3, { input: 20, output: 150 }, 50, 'claude-sonnet-5[1m]', 'claude-sonnet-5'),
    ])
    expect(s.turns.map((t) => t.costUsd)).toEqual([0.1, expect.closeTo(0.2, 10)])
    expect(s.models).toHaveLength(1)
    expect(s.models[0].model).toBe('claude-sonnet-5')
    expect(s.models[0].costUsd).toBeCloseTo(0.3, 10)
    expect(s.models[0].tokens).toEqual({ input: 20, output: 150, cacheRead: 0, cacheWrite: 0 })
    // Without canonicalModel the raw keys differ → the model is "missing" → independent.
    const raw = costSummary([
      full(0.1, { input: 10, output: 100 }, 100, 'claude-sonnet-5', undefined),
      full(0.3, { input: 20, output: 150 }, 50, 'claude-sonnet-5[1m]', undefined),
    ])
    expect(raw.turns[1].costUsd).toBe(0.3)
  })

  it('rule 3: all counters growing is not enough — output growth below own output stays independent (53→91, 130→958 style)', () => {
    const s = costSummary([
      full(0.02, { input: 10, output: 53, cacheRead: 1000, cacheWrite: 10 }, 53),
      full(0.03, { input: 20, output: 91, cacheRead: 2000, cacheWrite: 20 }, 91),
      full(0.04, { input: 30, output: 130, cacheRead: 3000, cacheWrite: 30 }, 130),
      full(0.2, { input: 40, output: 958, cacheRead: 4000, cacheWrite: 40 }, 958),
    ])
    expect(s.turns.map((t) => t.costUsd)).toEqual([0.02, 0.03, 0.04, 0.2])
  })

  it.each([
    ['input', { input: 5, output: 150, cacheRead: 100, cacheWrite: 10 }],
    ['cacheRead', { input: 20, output: 150, cacheRead: 50, cacheWrite: 10 }],
    ['cacheWrite', { input: 20, output: 150, cacheRead: 100, cacheWrite: 5 }],
  ])('rule 3: %s shrinking vs prev → independent even with enough output growth', (_, c) => {
    const s = costSummary([
      full(0.1, { input: 10, output: 100, cacheRead: 100, cacheWrite: 10 }, 100),
      full(0.3, c, 50),
    ])
    expect(s.turns[1].costUsd).toBe(0.3)
    // Control: all four ≥ → cumulative.
    const ctl = costSummary([
      full(0.1, { input: 10, output: 100, cacheRead: 100, cacheWrite: 10 }, 100),
      full(0.3, { input: 10, output: 150, cacheRead: 100, cacheWrite: 10 }, 50),
    ])
    expect(ctl.turns[1].costUsd).toBeCloseTo(0.2, 10)
  })

  it('subagent-scoped results never take part in the walk (F6)', () => {
    const s = costSummary([
      frame(0.1, 100, 100),
      frame(0.9, 5000, 1, { parent_tool_use_id: 'toolu_x' }),
      frame(0.3, 150, 50),
    ])
    expect(s.turns).toHaveLength(2)
    expect(s.totalUsd).toBeCloseTo(0.3, 10)
  })
})

// Shared rule with Nexen: float sums run in a fixed order so both sides land
// on the same bits. Entries sharing a model fold in lexicographic order of
// their original modelUsage key; every sum across models runs in
// lexicographic order of the canonical key. 1e16 + 1 rounds back to 1e16,
// so (1e16 + 1) + 1 = 1e16 but (1 + 1) + 1e16 = 1e16 + 2.
describe('costSummary — deterministic summation order', () => {
  type Entry = ReturnType<typeof entry>
  const entry = (out: number, canonicalModel: string, cost = 0) => ({
    inputTokens: out, outputTokens: out, cacheReadInputTokens: 0, cacheCreationInputTokens: 0, costUSD: cost, canonicalModel,
  })
  /** modelUsage with its keys inserted in the given order. */
  const mu = (pairs: [string, Entry][]) => {
    const o: Record<string, unknown> = {}
    for (const [k, v] of pairs) o[k] = v
    return o
  }

  it('entries sharing a model fold in order of their original key', () => {
    const e: [string, Entry][] = [['k1', entry(1e16, 'm', 1e16)], ['k2', entry(1, 'm', 1)], ['k3', entry(1, 'm', 1)]]
    const sorted = costSummary([result({ total_cost_usd: 1, modelUsage: mu(e) })])
    const reversed = costSummary([result({ total_cost_usd: 1, modelUsage: mu([...e].reverse()) })])
    expect(sorted.turns[0].tokens!.output).toBe(1e16)
    expect(reversed.turns[0].tokens).toEqual(sorted.turns[0].tokens)
    expect(reversed.models).toEqual(sorted.models)
    expect(reversed.models[0].costUsd).toBe(1e16)
  })

  it('per-frame totals sum models in order of the canonical key, not the original key', () => {
    // Original keys sort x < y < z; canonical keys sort a < b < c the other way round.
    const e: [string, Entry][] = [['z', entry(1e16, 'a')], ['y', entry(1, 'b')], ['x', entry(1, 'c')]]
    for (const order of [e, [...e].reverse()]) {
      const s = costSummary([result({ total_cost_usd: 1, modelUsage: mu(order) })])
      expect(s.turns[0].tokens!.output).toBe(1e16)
      expect(s.tokens.input).toBe(1e16)
      expect(s.turns[0].models).toEqual(['a', 'b', 'c'])
    }
  })

  it('the growth sum runs in canonical order: reverse insertion gives the same classification and total', () => {
    // prev a:0 b:1 c:0 (Σ 1 > 0, so it becomes prev); this frame's deltas are
    // a 1e16, b 1, c 1 → growth 1e16 in canonical order, below its own
    // output 1e16 + 2 → independent (contribution = its whole total).
    const prevE: [string, Entry][] = [['a', entry(0, 'a')], ['b', entry(1, 'b')], ['c', entry(0, 'c')]]
    const curE: [string, Entry][] = [['a', entry(1e16, 'a')], ['b', entry(2, 'b')], ['c', entry(1, 'c')]]
    const run = (reverse: boolean) => {
      const o = (x: [string, Entry][]) => mu(reverse ? [...x].reverse() : x)
      return costSummary([
        result({ total_cost_usd: 0.25, modelUsage: o(prevE), usage: { output_tokens: 1 } }),
        result({ total_cost_usd: 0.5, modelUsage: o(curE), usage: { output_tokens: 1e16 + 2 } }),
      ])
    }
    const sorted = run(false)
    const reversed = run(true)
    expect(sorted.turns[1].costUsd).toBe(0.5)
    expect(sorted.totalUsd).toBe(0.75)
    expect(reversed.turns.map((t) => t.costUsd)).toEqual(sorted.turns.map((t) => t.costUsd))
    expect(reversed.totalUsd).toBe(sorted.totalUsd)
    expect(reversed.tokens).toEqual(sorted.tokens)
  })
})
