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

  it('totals cost / timing / rounds / output tokens', () => {
    expect(s.totalUsd).toBeCloseTo(0.2576558, 7)
    expect(s.apiMs).toBe(65032)
    expect(s.durationMs).toBe(93086)
    expect(s.rounds).toBe(22)
    expect(s.tokens.output).toBe(4478)
  })

  it('turn 1 (seq 8) sums modelUsage, not usage (F5), and lists canonical models', () => {
    const t = s.turns[0]
    expect(t.tokens?.output).toBe(376)
    expect(t.tokens?.input).toBe(911)
    expect(t.models).toEqual(['claude-haiku-4-5', 'claude-sonnet-5'])
    expect(t.isError).toBe(false)
    expect(t.costUsd).toBeCloseTo(0.0299658, 7)
  })

  it('turn 12 (seq 974) cacheRead comes from modelUsage (80127, not 46401)', () => {
    expect(s.turns[11].tokens?.cacheRead).toBe(80127)
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
