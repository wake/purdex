// spa/src/lib/nex/message-types.test.ts — compile-time shape checks for the
// stream-json message types. The assertions are trivial at runtime; the
// value is that `tsc -p tsconfig.app.json` rejects a literal that drifts
// from the declared shape.
import { describe, it, expect } from 'vitest'
import type { ResultMessage } from './message-types'

describe('ResultMessage cost fields (P-B4 §4.5)', () => {
  it('accepts a literal carrying every optional cost field', () => {
    const full: ResultMessage = {
      type: 'result',
      subtype: 'success',
      total_cost_usd: 0.0299658,
      session_id: 'c191a5a0-da2a-4888-afac-d85f753b5090',
      duration_ms: 6376,
      duration_api_ms: 6791,
      ttft_ms: 6315,
      is_error: false,
      num_turns: 1,
      parent_tool_use_id: null,
      usage: {
        input_tokens: 2,
        output_tokens: 364,
        cache_read_input_tokens: 11244,
        cache_creation_input_tokens: 5776,
      },
      modelUsage: {
        'claude-haiku-4-5-20251001': {
          inputTokens: 909,
          outputTokens: 12,
          cacheReadInputTokens: 0,
          cacheCreationInputTokens: 0,
          costUSD: 0.000969,
          canonicalModel: 'claude-haiku-4-5',
        },
        'claude-sonnet-5': {
          inputTokens: 2,
          outputTokens: 364,
          cacheReadInputTokens: 11244,
          cacheCreationInputTokens: 5776,
          costUSD: 0.0289968,
          canonicalModel: 'claude-sonnet-5',
        },
      },
    }
    expect(full.modelUsage?.['claude-sonnet-5']?.costUSD).toBeCloseTo(0.0289968, 7)
    expect(full.usage?.output_tokens).toBe(364)
    expect(full.parent_tool_use_id).toBeNull()
  })

  it('accepts a subagent result with a string parent_tool_use_id', () => {
    const sub: ResultMessage = { type: 'result', subtype: 'success', parent_tool_use_id: 'toolu_x' }
    expect(sub.parent_tool_use_id).toBe('toolu_x')
  })

  it('still accepts the minimal {type, subtype} literal — every cost field is optional', () => {
    const minimal: ResultMessage = { type: 'result', subtype: 'success' }
    expect(minimal.is_error).toBeUndefined()
    expect(minimal.usage).toBeUndefined()
    expect(minimal.modelUsage).toBeUndefined()
    expect(minimal.num_turns).toBeUndefined()
    expect(minimal.duration_api_ms).toBeUndefined()
    expect(minimal.ttft_ms).toBeUndefined()
  })
})
