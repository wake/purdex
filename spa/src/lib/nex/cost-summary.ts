// spa/src/lib/nex/cost-summary.ts
// Pure per-turn cost / token / timing rollup over an execution's `result`
// frames (P-B4 spec §4.1, rules C1–C7 + C3a). The header and the hover /
// panel both read from this one function so they can never disagree.
//
// Three measured facts (spec §3, mlab alpha.405 / nexen v0.12.0 / CC 2.1.27x)
// the rules rest on:
//
//   F4  `total_cost_usd === Σ modelUsage[*].costUSD` on every frame measured
//       (floating-point equality) — so `totalUsd` is Σ total_cost_usd and the
//       per-model fold sums to the same number whenever every turn is split.
//   F5  `usage` is NOT the whole turn when more than one model ran: it
//       reflects one model's messages, `modelUsage` all of them (seq 8:
//       usage.output_tokens 364 vs Σ modelUsage 376). Token breakdowns
//       therefore sum `modelUsage`; `usage` is only the fallback for frames
//       without a usable split (older CC).
//   F6  The parent's `result` already contains any subagent's spend
//       (subagent_stats + cacheRead include it). A subagent-scoped `result`
//       carries `parent_tool_use_id != null`; counting it would double-bill,
//       so only `result` frames with `parent_tool_use_id == null` are turns,
//       and subagent `assistant.message.usage` is never added.
//
// `unsplitTurns` = number of turns whose `costUsd > 0` but whose tokens did
// not come from a valid `modelUsage` entry (usage fallback or `null`). Their
// cost is in `totalUsd` but absent from `models`, so Σ models.costUsd may be
// below `totalUsd` by exactly those turns; the panel notes this when > 0.
//
// Shape tolerance (C6): every read is guarded by type; a hostile or partial
// frame yields zeros / nulls, never NaN, never a throw. `modelUsage` is read
// by own-key iteration only.

import { obj } from './content-blocks'
import type { StreamMessage } from './message-types'

export interface TokenTotals { input: number; output: number; cacheRead: number; cacheWrite: number }

export interface TurnCost {
  /** 1-based among top-level result frames, in seq order. */
  index: number
  /** `total_cost_usd` (0 when absent / invalid). */
  costUsd: number
  /** Σ valid modelUsage entries, else `usage`, else null. */
  tokens: TokenTotals | null
  /** Wall clock (`duration_ms`). */
  durationMs: number | null
  /** `duration_api_ms`. */
  apiMs: number | null
  /** `num_turns` — API round-trips inside this one Nexen turn. */
  rounds: number | null
  /** `'success' | 'error_during_execution' | …`; `''` when absent. */
  subtype: string
  /** `is_error === true || (subtype present && subtype !== 'success')`. */
  isError: boolean
  /** canonicalModel (or modelUsage key) of the valid entries, order of appearance. */
  models: string[]
}

export interface ModelCost { model: string; costUsd: number; tokens: TokenTotals }

export interface CostSummary {
  turns: TurnCost[]
  totalUsd: number
  /** Σ over turns that had tokens. */
  tokens: TokenTotals
  /** Σ (absent → 0). */
  durationMs: number
  apiMs: number
  rounds: number
  /** Aggregated over all turns, sorted by costUsd desc. */
  models: ModelCost[]
  /** Costed turns (costUsd > 0) without a modelUsage split. */
  unsplitTurns: number
}

const nonNeg = (v: unknown): v is number => typeof v === 'number' && Number.isFinite(v) && v >= 0
const str = (v: unknown): v is string => typeof v === 'string'

const zeroTokens = (): TokenTotals => ({ input: 0, output: 0, cacheRead: 0, cacheWrite: 0 })

function addTokens(into: TokenTotals, t: TokenTotals): void {
  into.input += t.input
  into.output += t.output
  into.cacheRead += t.cacheRead
  into.cacheWrite += t.cacheWrite
}

interface ValidEntry { model: string; costUsd: number; tokens: TokenTotals }

/** C3: an entry is valid when its four token fields and `costUSD` are all finite numbers ≥ 0. */
function readEntry(key: string, v: unknown): ValidEntry | null {
  const e = obj(v)
  if (!e) return null
  const { inputTokens, outputTokens, cacheReadInputTokens, cacheCreationInputTokens, costUSD } = e
  if (!nonNeg(inputTokens) || !nonNeg(outputTokens) || !nonNeg(cacheReadInputTokens) || !nonNeg(cacheCreationInputTokens) || !nonNeg(costUSD)) return null
  const canonical = e.canonicalModel
  return {
    model: str(canonical) && canonical !== '' ? canonical : key,
    costUsd: costUSD,
    tokens: { input: inputTokens, output: outputTokens, cacheRead: cacheReadInputTokens, cacheWrite: cacheCreationInputTokens },
  }
}

/** C6: own keys only — `{ __proto__: … }` and inherited props are never read. */
function validEntries(modelUsage: unknown): ValidEntry[] {
  const mu = obj(modelUsage)
  if (!mu) return []
  const out: ValidEntry[] = []
  for (const key in mu) {
    if (!Object.hasOwn(mu, key)) continue
    const entry = readEntry(key, mu[key])
    if (entry) out.push(entry)
  }
  return out
}

/** C3 fallback: `usage` snake_case fields; missing ones are 0 when at least one is present, else null. */
function readUsage(usage: unknown): TokenTotals | null {
  const u = obj(usage)
  if (!u) return null
  const fields = [u.input_tokens, u.output_tokens, u.cache_read_input_tokens, u.cache_creation_input_tokens]
  if (!fields.some(nonNeg)) return null
  const pick = (v: unknown): number => (nonNeg(v) ? v : 0)
  return { input: pick(fields[0]), output: pick(fields[1]), cacheRead: pick(fields[2]), cacheWrite: pick(fields[3]) }
}

const optNum = (v: unknown): number | null => (nonNeg(v) ? v : null)

export function costSummary(messages: readonly StreamMessage[]): CostSummary {
  const turns: TurnCost[] = []
  const tokens = zeroTokens()
  const byModel = new Map<string, ModelCost>()
  let totalUsd = 0
  let durationMs = 0
  let apiMs = 0
  let rounds = 0
  let unsplitTurns = 0

  for (const m of messages) {
    const p = obj(m)
    // C1: a turn is a top-level `result` (F6 — subagent-scoped results are already inside the parent's).
    if (!p || p.type !== 'result' || p.parent_tool_use_id != null) continue

    // C2
    const costUsd = nonNeg(p.total_cost_usd) ? p.total_cost_usd : 0
    totalUsd += costUsd

    // C3 / C5
    const entries = validEntries(p.modelUsage)
    let turnTokens: TokenTotals | null
    if (entries.length > 0) {
      turnTokens = zeroTokens()
      for (const e of entries) {
        addTokens(turnTokens, e.tokens)
        const agg = byModel.get(e.model)
        if (agg) {
          agg.costUsd += e.costUsd
          addTokens(agg.tokens, e.tokens)
        } else {
          byModel.set(e.model, { model: e.model, costUsd: e.costUsd, tokens: { ...e.tokens } })
        }
      }
    } else {
      turnTokens = readUsage(p.usage)
      if (costUsd > 0) unsplitTurns++
    }
    if (turnTokens) addTokens(tokens, turnTokens)

    // C3a
    const subtype = str(p.subtype) ? p.subtype : ''
    const isError = p.is_error === true || (subtype !== '' && subtype !== 'success')

    // C4
    const turnDuration = optNum(p.duration_ms)
    const turnApi = optNum(p.duration_api_ms)
    const turnRounds = optNum(p.num_turns)
    durationMs += turnDuration ?? 0
    apiMs += turnApi ?? 0
    rounds += turnRounds ?? 0

    turns.push({
      index: turns.length + 1,
      costUsd,
      tokens: turnTokens,
      durationMs: turnDuration,
      apiMs: turnApi,
      rounds: turnRounds,
      subtype,
      isError,
      models: entries.map((e) => e.model),
    })
  }

  const models = [...byModel.values()].sort((a, b) => b.costUsd - a.costUsd)

  return { turns, totalUsd, tokens, durationMs, apiMs, rounds, models, unsplitTurns }
}
