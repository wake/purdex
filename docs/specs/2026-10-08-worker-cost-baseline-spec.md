# Worker cost — what CC's result frames are today, and a baseline rule kept in reserve (#1501)

Status: **findings + decision; rule v2 NOT adopted (kept as a backup design).** 2026-10-08. Author: purdex-aa; decision by the
coordinator (purdex-d3) with the user.
Scope: Purdex (`spa/src/lib/nex/cost-summary.ts`) and Nexen (`store/cost.go`, the list rollup) share one rollup rule word for word.

## 0. Decision

The user chose to keep showing a worker's cost **together with the session's earlier spend** (the first worker turn after a
terminal handoff includes the terminal part, with the 「含交接前歷史」 tooltip — today's behaviour). Under that display:
- Since CC writes and restores `cost-state` (F7, F8), every resumed process starts from the session's running totals, so every
  frame after the first is a running total, and the current rule (v1) classifies each one correctly: totals only grow, and the
  Σ output growth always includes the frame's own `usage.output_tokens` (subagent output only adds to it). On the recording v1
  sums to the session's actual spend, terminal turn included (§6: 0.029030875 = terminal 0.00709449 + the worker's 0.021936385);
  without the two forced resets the recording made, that would also be the last frame's `total_cost_usd`.
- v1 can still be wrong in three cases: (a) a process killed mid-turn without writing `cost-state` and the same execution resumed
  afterwards (a daemon crash or forced kill during a turn); (b) a transcript written by a CC old enough to have no `cost-state`
  (≤ 2.1.27x) — the original #1501 shape; (c) a frame whose `modelUsage` is missing or malformed: v1's rule 1 adds its own
  `total_cost_usd` in full (`cost-summary.ts` 314–316), which is now a running total, so the session's earlier spend is counted
  again (over-count). None was seen in the recording; all are rare.
- **Rule v2 (§3) is not worth a coordinated Nexen + Purdex release for these.** It stays here, with its vectors, as the design to
  pick up if (a) or (c) shows up in practice (e.g. after a daemon restart during a turn), or if the display ever separates the
  worker's own spend from the session's.

#1501 closes with this document; residuals (a)–(c) are recorded in §7.

## 1. The problem (#1501)

The current rule (P-B4 spec §4.1 F4, "R4 T2.1", Nexen v0.13.2 `chainCost`) cannot see whether a `result` frame carries the
session's running totals or only its own spend, so it **guesses** from token growth: a frame is "cumulative" when every counter
of the previous frame grew and Σ output growth ≥ the frame's own `usage.output_tokens`. A parent's `modelUsage` includes its
subagents' output while `usage.output_tokens` does not, so an **independent** frame that ran a subagent right after a short
frame can pass the test and be billed as a delta — an under-count (the issue's example: shown $0.55, true $0.78).

## 2. Measured facts (recording 2026-10-08, CC 2.1.293, haiku, Nexen in Purdex alpha.594)

Recording: one terminal turn, then a worker on the same session (execution `06GHGNSWEVF59KQQRK7R0ZDF48`, 7 turns), with the
transcript's `cost-state` lines removed before turns 3 and 4 to force a run with nothing to restore. The execution is archived on
mlab's Nexen (its events stay readable there); the throwaway transcript and cwd were deleted. §6 holds every field of every frame
the rule reads.

- **F7 — CC persists its running totals in the transcript.** Every process writes a `{"type":"cost-state", "sessionId",
  "totalCostUSD", "totalAPIDuration", "modelUsage": {…}, …}` line when it ends — a normal end **and an interrupt** — equal to its
  last `result` frame's totals. `claude -p` does **not** write `~/.claude.json`'s `lastCost` / `lastModelUsage` (only an
  interactive session does).
- **F8 — a resumed process restores the last `cost-state`.** Its frames report that state **plus** its own spend: turn 1 after the
  terminal handoff reads output 20 = 17 (terminal) + 3; turn 2 reads 164 = 20 + 140 + 4 (subagent); turn 5 reads 2040 = 767 + 1273.
  `duration_api_ms` and `total_cost_usd` follow the same rule; `duration_ms` and `num_turns` are always the frame's own.
- **F9 — nothing to restore ⇒ independent.** With the `cost-state` lines removed, turns 3 and 4 started from zero (output 3;
  output 164). This is also what a transcript written by an older CC (no `cost-state`, e.g. fixture `06GB2ZFD`, CC 2.1.27x) and a
  process killed before it wrote one look like.
- **F10 — one Nexen turn = one process, possibly several results.** Nexen spawns `claude -p --resume` per turn (spawn-per-turn).
  The Agent tool now runs **in the background**: the turn emits a `result` when the main loop stops, then another one when the
  subagent's completion wakes it (turn 2: 6822 then 6826; turn 4: 6850 then 6859). The subagent's output lands in whichever frame
  ends after it finished (in 6822 for a 0.7 s subagent, in 6859 for a 3.7 s one). Results inside one process are always running
  totals of that process (they only grow).
- **F11 — the old discriminator candidates are gone.** A background Agent's `tool_use_result` carries no `usage`;
  `system/task_notification.usage` has only `total_tokens`, `tool_uses`, `duration_ms`. `subagent_stats` is per process.
- **F12 — an interrupted turn** ends with a `result` (`error_during_execution`) whose totals equal the restored state (the streamed
  partial output is not counted) and still writes `cost-state`.

So today's frames are almost always running totals of the **session**, including the terminal part before the handoff — which is
why the pane's first worker turn shows 「含交接前歷史」. The rule's guess is right in the recording, but only because the subagent
output happened to land in a same-process frame; F9 still produces the #1501 shape.

## 3. Rule v2 — subtract a known baseline

**Nexen records, at each turn's spawn, the baseline the process will restore**: it reads the transcript (it already knows
`transcript_path`) backwards to the last line with `"type":"cost-state"` whose `sessionId` equals the session being resumed, and
puts it on that turn's `execution.running` payload:

```json
"cost_baseline": {
  "total_cost_usd": 0.01384295,
  "duration_api_ms": 2418,
  "modelUsage": { "claude-haiku-5-5": { "inputTokens": 1174, "outputTokens": 20, "cacheReadInputTokens": 53455,
                                         "cacheCreationInputTokens": 65905, "costUSD": 0.01384295 } }
}
```

`null` only when Nexen **knows** there is none: a fresh session (no resume), or a transcript it read to the end without finding a
`cost-state` for that session. When it cannot tell — the transcript is missing, unreadable, or its tail cannot be parsed — it
**omits** the field, so the rollup falls back to `legacy` instead of assuming zero (CC may still restore a state Nexen could not
read, and a zero baseline would count the session's earlier spend again). The `modelUsage` keys and fields are copied as stored.
Capability: `capabilities.cost.baseline = true`.

**Rollup (both sides, word for word):** walk events in seq order.
1. `execution.running` starts a process. `base` :=
   - **zero** when `cost_baseline` is `null`;
   - **the baseline** when it is a **valid** object: `total_cost_usd` and `duration_api_ms` finite numbers ≥ 0 (CC's
     `cost-state` always carries both: `totalCostUSD`, `totalAPIDuration`), and `modelUsage` passing rule v1's strict read (C3: an object, non-empty, every entry with the four counters and
     `costUSD` finite numbers ≥ 0; `readModelUsage(…, true)` in `cost-summary.ts`);
   - **`legacy`** otherwise — the field absent (older Nexen) or present but not valid by the line above. Never zero for an invalid
     object (zero would over-count by the whole session), never a partial baseline.
   `legacy` means rule v1 decides this process's frames, as today.
2. A top-level `result` (`parent_tool_use_id == null`):
   - `base` is a baseline (or zero): contribution = this frame − `base`, per field (`total_cost_usd`, each model's four counters
     and `costUSD`, `duration_api_ms`), each clamped at 0. Then `base` := this frame (later results of the same process are
     deltas against it, F10).
   - `base` is `legacy`: today's rule v1, unchanged.
3. Shape tolerance, overflow and summation order are rule v1's (C6, `addFinite`, lexicographic order).
4. **A malformed result in baseline mode** — the cost total stays exact; the breakdown (tokens, per-model rows, API time) is given
   up for that frame **and the next valid one**, never double-counted:
   - `total_cost_usd` valid, `modelUsage` failing rule v1's strict read: contribution = `total_cost_usd − base.total_cost_usd`
     (clamped at 0), counted as an unsplit turn (no tokens, no per-model rows, no API time — like v1's usage fallback). Then
     `base` := **total-only** (`total_cost_usd` = this frame's; no `modelUsage`, no `duration_api_ms`).
   - A valid result while `base` is total-only: contribution = `total_cost_usd − base.total_cost_usd` (clamped at 0), also
     unsplit — its counters still include the malformed frame's tokens, which no row may count twice. Then `base` := this frame
     (full), and exact breakdown resumes from the next frame.
   - `total_cost_usd` not a finite number ≥ 0: contribution 0; `base` unchanged.
   Baseline mode never reads or writes rule v1's `prev`; `legacy` always means rule v1 with its **global** `prev` — the last frame
   v1 accepted as `prev` anywhere in the event stream, as today (`cost-summary.ts` 296, 319–327). Vector: one process, baseline B →
   valid R1 → malformed R2 (total only) → valid R3 → valid R4: contributions R1 − B (split), R2.total − R1.total (unsplit),
   R3.total − R2.total (unsplit), R4 − R3 (split); Σ cost = R4.total − B.total, Σ per-model costUSD = (R1 − B) + (R4 − R3).

Properties: exact for every case in §2, including the #1501 shape (an independent subagent frame after a short one: `base` is
zero, nothing is subtracted); a killed process is never double-counted (its successor's baseline is the older `cost-state`, which
is also what the successor restores); no dependence on subagents, models or token growth.

## 4. What v2 would change for the user (why it was not adopted)

- The first worker turn after a terminal handoff would no longer include the terminal's spend (v2 subtracts the restored
  baseline). The user prefers the combined figure (§0), so a v2 adoption would have to add the baseline back for display —
  i.e. v2 buys exactness only in the rare cases of §0 (a)–(c), and for (c) only in a process with a valid baseline.
- Old executions (events written before a Nexen release with `cost_baseline`) would keep v1.

## 5. Delivery (if ever picked up)

1. Nexen: `cost_baseline` on `execution.running` (+ capability), and `store/cost.go` rollup v2 with the vectors below.
2. Purdex: pin the Nexen release; `cost-summary.ts` rollup v2 with the same vectors; display the session's combined figure as
   the user decided (§0): turn 1's footer = baseline + its delta, with the existing tooltip.
3. Both in one release pair; until then nothing changes (the field is absent ⇒ `legacy`).

## 6. Shared test vectors

Values are from the recording; every field either rule reads is here. One model, `claude-haiku-5-5` (its `costUSD` equals the
frame's `total_cost_usd`); counters are `[in, out, cacheRead, cacheWrite]`; `own` is `usage.output_tokens`. `running` lines are
`execution.running` with their `cost_baseline` (`none` = `null`). All results are top-level (`parent_tool_use_id: null`) with
subtype `success` unless noted.

| seq | event | total_cost_usd | counters | own | api_ms | v2 contribution | v1 contribution (current code) |
|---|---|---|---|---|---|---|---|
| 6794 | running T1, baseline = terminal state: 0.00709449, out 17, api 1517 | | | | | | |
| 6800 | result | 0.01384295 | [1174, 20, 53455, 65905] | 3 | 2418 | 0.00674846 | 0.01384295 (first frame) |
| 6804 | running T2, baseline = 6800 | | | | | | |
| 6822 | result (main loop stopped; 0.7 s subagent already in) | 0.022536355 | [1180, 164, 141993, 108022] | 140 | 4710 | 0.008693405 | 0.008693405 |
| 6826 | result (woken by the subagent) | 0.023251055 | [1184, 167, 202993, 108536] | 3 | 5393 | 0.0007147 | 0.0007147 |
| 6829 | running T3, baseline none | | | | | | |
| 6834 | result | 0.00062464 | [2, 3, 61514, 39] | 3 | 695 | 0.00062464 | 0.00062464 (counters fell) |
| 6837 | running T4, baseline none | | | | | | |
| 6850 | result (subagent still running) | 0.0014538 | [4, 164, 123180, 698] | 164 | 1546 | 0.0014538 | 0.0014538 (growth 161 < own 164) |
| 6859 | result (subagent's 600 output in) | 0.00307255 | [10, 767, 191290, 5111] | 3 | 5971 | 0.00161875 | 0.00161875 |
| 6863 | running T5, baseline = 6859 | | | | | | |
| 6871 | result | 0.00435256 | [12, 2040, 254621, 5161] | 1273 | 10051 | 0.00128001 | 0.00128001 |
| 6875 | running T6 (interrupted), baseline = 6871 | | | | | | |
| 6885 | result `error_during_execution` | 0.00435256 | [12, 2040, 254621, 5161] | 0 | 10051 | 0 | 0 |
| 6889 | running T7, baseline = 6885 | | | | | | |
| 6897 | result | 0.00515518 | [14, 2112, 319323, 5758] | 72 | 11112 | 0.00080262 | 0.00080262 |

Total v2 = 0.021936385 (the worker's own spend; per process: last frame − baseline). Total v1 = **0.029030875**, computed by
running the current `costSummary` over these nine frames (2026-10-08): the same classification as v2 for every frame after the
first; the difference, 0.00709449, is the terminal turn, which v1 counts into turn 1.

Synthetic vectors to add on both sides (no recording needed):
- **#1501 shape under v2:** running (baseline `null`) → short result (out 3, $0.03) → running (baseline `null`) → result with a
  subagent (out 140 + sub 600, $0.25): contributions $0.03 and $0.25 (v1 would show $0.22 for the second).
- **Killed process:** running (baseline A) → result R1 → [process killed, no cost-state] → running (baseline A again) → result R2:
  contributions R1 − A and R2 − A.
- **Legacy:** events without `cost_baseline` → identical to rule v1 (existing fixtures `06GB2ZFD`, `06GBGXTW` unchanged).
- **Malformed `cost_baseline`** (not an object and not `null`; a `modelUsage` entry failing rule v1's C3 entry check; a
  non-finite or negative `total_cost_usd`): that process is `legacy` — never zero, which would over-count by the whole session.

## 7. Residuals of rule v1 (recorded with #1501's closure)

1. **A process killed mid-turn, then the same execution resumed.** The killed process wrote no `cost-state`, so the next process
   restores an older state while `prev` is the killed process's last frame. If the new frame's counters are below `prev`'s, v1 counts
   it independent and adds the whole restored session total again (over-count); if they are above, it subtracts the killed frame and
   under-counts by the killed process's spend. Reachable by a daemon crash or a forced kill during a turn; an interrupt is graceful
   (F12) and does not cause it.
2. **Transcripts without `cost-state`** (CC ≤ 2.1.27x): frames are independent, and the #1501 shape (an independent frame that ran a
   subagent right after a short frame) can still be under-counted. Shrinks as old sessions age out.
3. **A frame with a missing or malformed `modelUsage`.** v1's rule 1 adds the frame's own `total_cost_usd` in full and never makes
   it `prev` (`cost-summary.ts` 314–316). That was right while frames were per-turn; now that the total is the session's running
   total, it counts the session's earlier spend again (over-count by everything before it). Not seen in the recording.
4. Neither the Workers list nor the pane can tell these cases apart today; both show the same (possibly wrong) number, because
   both run v1. Rule v2 (§3) fixes 1 and 2 if they ever matter, and fixes 3 for every process that has a valid baseline (rule 4:
   the cost delta comes from `total_cost_usd`); a process in `legacy` keeps residual 3.
