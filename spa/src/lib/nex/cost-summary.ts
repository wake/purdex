// spa/src/lib/nex/cost-summary.ts
// Pure per-turn cost / token / timing rollup over an execution's `result`
// frames (P-B4 spec §4.1, rules C1–C7 + C3a). The header and the hover /
// panel both read from this one function so they can never disagree.
//
// Three measured facts (spec §3, mlab alpha.405 / nexen v0.12.0 / CC 2.1.27x)
// the rules rest on:
//
//   F4  `total_cost_usd === Σ modelUsage[*].costUSD` on every frame measured
//       (floating-point equality), and SOME results are running totals, not
//       per-turn figures. Two measured sources: Nexen spec §1.2 (cumulative
//       within one `-p` and across `--resume`: 0.0528 → 0.0643 → 0.1126,
//       inputTokens 18 → 36 → 46) and fixture 06GB2ZFD (CC 2.1.27x, one
//       session): ten results are independent per resumed turn, but seq 974
//       continues seq 958 (sonnet outputTokens 598 → 701, Δ 103 = 974's own
//       `usage.output_tokens`). `modelUsage[*].costUSD` / tokens and
//       `duration_api_ms` follow the same pattern; `num_turns` and
//       `duration_ms` are always per-frame. Neither "always add" nor "always
//       take the latest" is right, and session ids / `resumed_from` do not
//       predict it — so a frame must PROVE it continues the previous one with
//       token evidence (R4 T2.1; the rule below is shared word for word with
//       Nexen v0.13.2's rollup so the list and the pane show one number).
//
//       Walk top-level results in seq order:
//       1. A frame without a usable `modelUsage` never becomes `prev`; it
//          contributes its own `total_cost_usd` if finite and > 0, else
//          nothing. A frame WITH `modelUsage` but a missing / non-finite /
//          negative `total_cost_usd` contributes nothing and is not `prev`.
//       2. The first frame with `modelUsage` is independent.
//       3. After that a frame is cumulative ⇔ every model in prev.modelUsage
//          is present here with ALL FOUR counters (inputTokens, outputTokens,
//          cacheReadInputTokens, cacheCreationInputTokens) ≥ prev's (missing
//          here ⇒ not cumulative) AND Σ over this frame's models of
//          (this outputTokens − prev's, prev missing = 0) ≥ this frame's
//          `usage.output_tokens`. Models are matched by `canonicalModel` when
//          it is a non-empty string, else by the modelUsage key (entries of
//          one frame sharing a model are summed).
//       4. Cumulative → contribution = this total − prev total; negative ⇒
//          independent. Independent → contribution = this total.
//       5. totalUsd = Σ contributions (saturating, see Overflow).
//       Edge cases: counters compare as plain numbers (no integer coercion);
//       a malformed modelUsage (not an object, empty, a model value not an
//       object, or any of inputTokens / outputTokens / cacheReadInputTokens /
//       cacheCreationInputTokens / costUSD not a finite number ≥ 0) makes the
//       whole frame count as having NO modelUsage (rule 1, never dropped) —
//       so every model of `prev` has a valid entry and a cumulative frame's
//       breakdown never shows a raw running total; `usage.output_tokens`
//       missing / malformed → 0. The frame (rules 2–4) then becomes `prev`,
//       UNLESS its Σ modelUsage outputTokens is 0 (an interrupted / empty
//       frame): then `prev` stays, though the frame still contributes per its
//       own classification. A cumulative frame's per-model cost, tokens and
//       `duration_api_ms` are deltas against `prev`, each clamped at 0.
//       Summation order (so both sides get the same float bits): entries
//       sharing a model fold in lexicographic order of their original
//       modelUsage key; every sum across models (the growth sum, Σ
//       outputTokens, per-frame cost / token totals) runs in lexicographic
//       order of the canonical key (plain `a < b`; keys are ASCII).
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
//
// Overflow (codex R2 A1): every running total goes through `addFinite` (clamps
// at Number.MAX_VALUE), so a
// hostile frame carrying `Number.MAX_VALUE` can never push a sum to ±Infinity
// (which `formatUsd` / `formatTokens` would render as '$—' / '—' and the
// header would render as '$Infinity'). The contribution that would overflow
// is dropped and the total kept as it was — a saturating add, not a NaN.

import { obj } from './content-blocks'
import type { StreamMessage } from './message-types'

export interface TokenTotals { input: number; output: number; cacheRead: number; cacheWrite: number }

export interface TurnCost {
  /** 1-based among top-level result frames, in seq order. */
  index: number
  /** This frame's contribution (F4 rules): its `total_cost_usd`, or the delta
   *  against the frame it continues; 0 when absent / invalid. */
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
  /** canonicalModel (or modelUsage key) of the valid entries, in lexicographic order. */
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

/**
 * C3a — a top-level `result` frame reports a failed turn. Shared with the
 * reducer's turn outcome (worker-pane theme spec §7.1) so the cost table and
 * the turn footer can never disagree about which turns failed.
 */
export function isResultError(p: Record<string, unknown>): boolean {
  const subtype = str(p.subtype) ? p.subtype : ''
  return p.is_error === true || (subtype !== '' && subtype !== 'success')
}

const zeroTokens = (): TokenTotals => ({ input: 0, output: 0, cacheRead: 0, cacheWrite: 0 })

/**
 * Saturating add: `a + b` when that is finite, else `Number.MAX_VALUE`. Inputs
 * are always finite ≥ 0 (guarded by `nonNeg`), so the only way to leave the
 * finite range is overflow to +Infinity — clamping to the largest finite value
 * keeps the total finite, order-independent (codex re-review: dropping the
 * addend made `[1e308, MAX]` and `[MAX, 1e308]` disagree) and monotonic.
 */
function addFinite(a: number, b: number): number {
  const s = a + b
  return Number.isFinite(s) ? s : Number.MAX_VALUE
}

function addTokens(into: TokenTotals, t: TokenTotals): void {
  into.input = addFinite(into.input, t.input)
  into.output = addFinite(into.output, t.output)
  into.cacheRead = addFinite(into.cacheRead, t.cacheRead)
  into.cacheWrite = addFinite(into.cacheWrite, t.cacheWrite)
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

/** Plain string order (`a < b`) — the summation order shared with Nexen. */
const byString = (a: string, b: string): number => (a < b ? -1 : a > b ? 1 : 0)

/**
 * Entries of one frame that share a model (rule 3 keying) are summed. The
 * caller hands them in order of their original modelUsage key; the result is
 * in order of the canonical key, which every cross-model sum then follows.
 */
function mergeByModel(entries: ValidEntry[]): ValidEntry[] {
  const out = new Map<string, ValidEntry>()
  for (const e of entries) {
    const had = out.get(e.model)
    if (!had) { out.set(e.model, { model: e.model, costUsd: e.costUsd, tokens: { ...e.tokens } }); continue }
    had.costUsd = addFinite(had.costUsd, e.costUsd)
    addTokens(had.tokens, e.tokens)
  }
  return [...out.values()].sort((a, b) => byString(a.model, b.model))
}

/**
 * C6: own keys only — `{ __proto__: … }` and inherited props are never read.
 * `strict` = rule 1's edge: ANY malformed model value makes the whole result
 * `null` (the frame has no usable modelUsage); otherwise malformed entries are
 * skipped (C3, display only). Empty → `[]` / `null`.
 */
function readModelUsage(modelUsage: unknown, strict: true): ValidEntry[] | null
function readModelUsage(modelUsage: unknown, strict: false): ValidEntry[]
function readModelUsage(modelUsage: unknown, strict: boolean): ValidEntry[] | null {
  const mu = obj(modelUsage)
  if (!mu) return strict ? null : []
  const out: ValidEntry[] = []
  const keys: string[] = []
  for (const key in mu) if (Object.hasOwn(mu, key)) keys.push(key)
  for (const key of keys.sort(byString)) {
    const entry = readEntry(key, mu[key])
    if (entry) out.push(entry)
    else if (strict) return null
  }
  if (strict && out.length === 0) return null
  return mergeByModel(out)
}

const USAGE_KEYS = ['input_tokens', 'output_tokens', 'cache_read_input_tokens', 'cache_creation_input_tokens'] as const

/**
 * C3 fallback: `usage` snake_case fields. A key that is ABSENT counts as 0
 * (older CC omits the cache fields); a key that is PRESENT but not a finite
 * number ≥ 0 makes the whole fallback `null` — a half-garbled frame must not
 * be summed as if the garbled field were 0 (codex R2 A2). All four absent → null.
 */
function readUsage(usage: unknown): TokenTotals | null {
  const u = obj(usage)
  if (!u) return null
  const vals: number[] = []
  let present = 0
  for (const k of USAGE_KEYS) {
    if (!Object.hasOwn(u, k)) { vals.push(0); continue }
    const v = u[k]
    if (!nonNeg(v)) return null
    vals.push(v)
    present++
  }
  if (present === 0) return null
  return { input: vals[0], output: vals[1], cacheRead: vals[2], cacheWrite: vals[3] }
}

const optNum = (v: unknown): number | null => (nonNeg(v) ? v : null)

/** Rule 3: every prev model present here with all four counters ≥, and enough output growth. Plain numbers (edge a). */
function continues(prev: Map<string, ValidEntry>, cur: Map<string, ValidEntry>, ownOutput: number): boolean {
  for (const [model, before] of prev) {
    const now = cur.get(model)
    if (!now) return false
    const a = now.tokens
    const b = before.tokens
    if (a.input < b.input || a.output < b.output || a.cacheRead < b.cacheRead || a.cacheWrite < b.cacheWrite) return false
  }
  let growth = 0
  for (const model of [...cur.keys()].sort(byString)) {
    const now = cur.get(model)!
    growth += now.tokens.output - (prev.get(model)?.tokens.output ?? 0)
  }
  return growth >= ownOutput
}

interface Prev {
  costUsd: number
  /** Raw (not delta) entries of that frame, by model — every one valid (rule 1 edge). */
  entries: Map<string, ValidEntry>
  apiMs: number | null
}

const clampDelta = (now: number, before: number): number => Math.max(0, now - before)

function deltaEntry(e: ValidEntry, before: ValidEntry | undefined): ValidEntry {
  if (!before) return e
  return {
    model: e.model,
    costUsd: clampDelta(e.costUsd, before.costUsd),
    tokens: {
      input: clampDelta(e.tokens.input, before.tokens.input),
      output: clampDelta(e.tokens.output, before.tokens.output),
      cacheRead: clampDelta(e.tokens.cacheRead, before.tokens.cacheRead),
      cacheWrite: clampDelta(e.tokens.cacheWrite, before.tokens.cacheWrite),
    },
  }
}

export function costSummary(messages: readonly StreamMessage[]): CostSummary {
  const turns: TurnCost[] = []
  const tokens = zeroTokens()
  const byModel = new Map<string, ModelCost>()
  let totalUsd = 0
  let durationMs = 0
  let apiMs = 0
  let rounds = 0
  let unsplitTurns = 0
  let prev: Prev | null = null

  for (const m of messages) {
    const p = obj(m)
    // C1: a turn is a top-level `result` (F6 — subagent-scoped results are already inside the parent's).
    if (!p || p.type !== 'result' || p.parent_tool_use_id != null) continue

    // C2 + F4 rules 1–5: this frame's contribution, and the frame it continues (if any).
    const rawCost = p.total_cost_usd
    const costOk = nonNeg(rawCost)
    const strict = readModelUsage(p.modelUsage, true)
    const evidence = strict ? new Map(strict.map((e) => [e.model, e])) : null
    const usageOut = obj(p.usage)?.output_tokens
    const ownOutput = nonNeg(usageOut) ? usageOut : 0 // edge (d)
    const entries = strict ?? readModelUsage(p.modelUsage, false)
    const turnApiRaw = optNum(p.duration_api_ms)
    let costUsd = 0
    let base: Prev | null = null
    if (!evidence) {
      // Rule 1: contributes its own cost (0 when absent / invalid), never `prev`.
      costUsd = costOk ? rawCost : 0
    } else if (costOk) {
      costUsd = rawCost
      if (prev && continues(prev.entries, evidence, ownOutput) && rawCost - prev.costUsd >= 0) {
        costUsd = rawCost - prev.costUsd // rule 4
        base = prev
      }
      // Rule 3: a frame with no output (interrupted / empty) never becomes prev.
      let outSum = 0
      for (const e of evidence.values()) outSum += e.tokens.output // canonical order: built from mergeByModel
      if (outSum > 0) prev = { costUsd: rawCost, entries: evidence, apiMs: turnApiRaw }
    }
    // else: modelUsage but no usable total_cost_usd (rule 1 / edge c) — contributes nothing, not `prev`.
    totalUsd = addFinite(totalUsd, costUsd)

    // C3 / C5 — a cumulative frame's breakdown is its delta against `base`.
    const turnEntries = base ? entries.map((e) => deltaEntry(e, base.entries.get(e.model))) : entries
    let turnTokens: TokenTotals | null
    if (turnEntries.length > 0) {
      turnTokens = zeroTokens()
      for (const e of turnEntries) {
        addTokens(turnTokens, e.tokens)
        const agg = byModel.get(e.model)
        if (agg) {
          agg.costUsd = addFinite(agg.costUsd, e.costUsd)
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
    const isError = isResultError(p)

    // C4 — `duration_api_ms` is a running total on a cumulative frame; the other two never are.
    const turnDuration = optNum(p.duration_ms)
    const turnApi = base && turnApiRaw !== null ? clampDelta(turnApiRaw, base.apiMs ?? 0) : turnApiRaw
    const turnRounds = optNum(p.num_turns)
    durationMs = addFinite(durationMs, turnDuration ?? 0)
    apiMs = addFinite(apiMs, turnApi ?? 0)
    rounds = addFinite(rounds, turnRounds ?? 0)

    turns.push({
      index: turns.length + 1,
      costUsd,
      tokens: turnTokens,
      durationMs: turnDuration,
      apiMs: turnApi,
      rounds: turnRounds,
      subtype,
      isError,
      models: turnEntries.map((e) => e.model),
    })
  }

  const models = [...byModel.values()].sort((a, b) => b.costUsd - a.costUsd)

  return { turns, totalUsd, tokens, durationMs, apiMs, rounds, models, unsplitTurns }
}
