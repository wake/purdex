# Spec — P-B3: exec pane consumes Nexen N2 `tool_use` / `tool_result`

- Status: v1.1 (2026-09-19) — codex plan+spec review `task-mu7ckdhj-wo5ha7` applied (§9)
- Predecessors: `2026-09-15-pb-execution-pane-spec.md` (P-B, §4.2.3
  transport / §4.2.4 reducer rules stay binding) and
  `2026-09-18-pb2-exec-live-stream-spec.md` (P-B2, §4.1 partial assembly
  and §4.2 A1–A4 tool activity from raw frames). This spec **layers** on
  P-B2 §4.2; it does not replace the partial assembly (§4.1), which is the
  typewriter line and is untouched here.
- Contract source of truth: `nexen/docs/contract/capability-matrix.md` at the
  pinned `lab.protype.tw/wake/nexen v0.12.0` — §0 `tool_events`
  (line ~134), §2 #53 (no backfill), §3 "`tool_use`／`tool_result` 的
  payload" (line ~652, the eleven rules), §3.5 stripping table (line ~861).
  **No Nexen-side change** in this phase. Nexen's console rendering spec
  (`2026-08-01-console-rendering-spec.md` R5–R11, R16, R17) is the display
  reference the three clients (React / Swift / Aigora) share.
- Successor candidates (not here): per-turn cost hover (console R13 — a
  `result`-frame concern, not N2), subagent tool tracking, P3 codex adapter.

## 1. Problem

Since alpha.405 the daemon emits a durable `tool_use` next to every raw
`assistant` frame that carries a `tool_use` block and a `tool_result` next
to every raw `user` frame that carries a `tool_result` block. The exec pane
ignores both: `spa/src/lib/nex/event-reducer.ts:70-71,150-156`
(`N2_TOOL_KINDS`) only advances `lastSeq` so the frames do not become
invisible messages. Tool status and timing still come from the P-B2
client-side derivation (`spa/src/lib/nex/tool-activity.ts`
`recordToolStarts` / `recordToolEnds` / `endTurn`), the header summary is
a client-side table (`ToolCallBlock.tsx:23-40` `getSummary`), the result
block shows the first 80 characters of raw content and nothing else
(`ToolResultBlock.tsx`), and there is no diff view for Edit / Write.

The user's standing decision (kickoff memory, decisions #5 / #6): tool
summaries, `Update` line-number diffs and cost hover are must-haves, and
the facts behind them are computed **once, server-side**, so React, Swift
and Aigora render the same numbers. N2 ships those facts; P-B3 is the
React consumer.

## 2. Goals / non-goals

### Goals

- G1 The reducer consumes `tool_use` / `tool_result` into the existing
  per-execution `tools` map (keyed by `tool_use_id`), taking the server's
  facts as **primary** and the P-B2 raw-frame derivation as **fallback**.
- G2 Old executions (pre-N2), old daemons (no `capabilities.tool_events`)
  and **mixed** executions (old turns raw-only, new turns raw + N2 — the
  case §2 #53 warns about) all render correctly with **no tool call counted
  or shown twice**.
- G3 Header summary from `primary_arg` (console R5), `known: false`
  fallback listing the first input keys (R10), status `denied` rendered as
  a struck-through call (R17), duration from `duration_ms`.
- G4 Result facts line: Read line count (`file.lines`), Edit / Write
  `+N −M` (`diff.added` / `removed`), output `total_lines`, truncation and
  non-text markers (console R6 / R9 / R11 first line).
- G5 Unified diff view with line numbers for `tool_result.diff.hunks`
  (console R11).
- G6 `NexCapabilities.tool_events` typed (contract mirror).

### Non-goals

- Cost / usage hover per turn (console R13): those facts live on the raw
  `result` frame, not on N2 events; separate phase.
- Subagent tools (`parent_tool_use_id != null`): P-B2 A1/A2 skip them and
  N2 rules do the same so both paths stay equivalent; a follow-up issue
  tracks lifting the skip for both at once.
- Streaming (`stream_event` / `stream_snapshot`), the `(message_id,
  block_index)` convergence key between partial blocks and `tool_use`: the
  partial is discarded when the durable `assistant` lands (P-B2 D1) so
  nothing needs converging in the SPA today.
- Re-reading the full output when `output.truncated` is true: the raw
  `user` frame is already in `messages` with the full content
  (contract rule 6), the renderer keeps showing it. N2 `output.text` is
  therefore **not stored** (it would be a second copy of up to 8 KB per
  tool call).
- Error results expanded by default (console R16), side-by-side diff,
  theme tokens for the hard-coded colours (existing TODOs).
- Daemon changes. This phase is SPA-only; no `bin/pdx` redeploy.

## 3. Measured facts this design rests on

Measured on mlab daemon `cf11a7f3` (alpha.405, nexen v0.12.0) on
2026-09-19 with execution `06GBBX0791PP0RQ4WSWDFY5FPM` (Read → Edit → Bash,
one turn, delegated for this purpose; events saved as the P-B3.1 fixture).

- F1 `GET /api/nex/v1/capabilities` returns
  `tool_events: {output_max_bytes: 8192, diff_max_lines: 2000}` and
  `events` contains `tool_use`, `tool_result`.
- F2 Seq order inside one turn is exactly the contract's
  `assistant(914), tool_use(915), …, user(917), tool_result(918)`; the
  derived event's `created_at` **equals** its raw frame's `created_at`
  (915 = 914 = 1789759067165; 918 = 917 = 1789759067191).
- F3 `tool_result.duration_ms` (26 / 24 / 752) equals the raw
  `user.created_at − assistant.created_at` for the history path. The P-B2
  timing and the N2 timing agree on history; on the live path P-B2 stamps
  client arrival time (A4) while `duration_ms` is the daemon's observation,
  so N2 is the better number there.
- F4 Payload shapes match the contract verbatim (rules 7 / 11): Read carries
  `file: {path, lines: 4}` and no `diff`; Edit carries
  `diff: {path, added: 1, removed: 1, hunks: [{old_start, old_lines,
  new_start, new_lines, lines: [" hello", "-world", "+nexen", " three"]}],
  truncated: false}`; Bash carries neither key. `input` is the full
  object (Bash also has `description`); `primary_arg` is `{key, value}`;
  `known: true` for all three.
- F5 The raw `user` frame's block is `{type: 'tool_result', tool_use_id,
  is_error: null, content: "<string>"}` and the frame carries
  `tool_use_result` (provider object) and `tool_result_meta: null` —
  i.e. `denied` is **not** derivable from `is_error` alone, only from N2
  `status` (contract rule 2).
- F6 The pane's stream is execution-scoped: `attachObserve` returns
  `stream_url = <prefix>/v1/events?execution_id=<id>`
  (nexen `api/interact.go:155`) and history comes from
  `GET /v1/executions/{id}/events` — both carry the content fields
  (§3.5 table). The site-wide stripping never applies to the pane, but
  the reducer stays shape-tolerant (rule N6 below) because the payload
  types are shared with any future site-wide consumer.
- F7 Pre-N2 executions exist on the same daemon:
  `06GB2ZFDHNCW2ZWQ33EG9D1ZXM` has 7 raw `tool_use` blocks and zero
  `tool_use` events (created 2026-09-18, before the deploy). Sending a
  follow-up turn to it produces the **mixed** execution of G2.
- F8 The reducer today: `tools: Record<string, ToolActivity>` with
  `{name, startedAt, endedAt, status: running|done|error|aborted}`;
  `ToolUseBlock` looks up `tools[block.id]`; `ToolResultBlock` receives
  only `content` + `isError`; `ConversationMessages` renders raw `user`
  `tool_result` blocks straight from `messages`.
- F9 Nexen's primary-arg table (`execution/toolargs.go`) has `TodoWrite`,
  `ExitPlanMode`, `update_plan` with **no** primary key → `known: true`
  and `primary_arg: null`. That is distinct from `known: false` (MCP /
  custom tools).

## 4. Design

### 4.1 Overlay, not a switch

The brief proposed "use N2 when `capabilities.tool_events` exists and the
execution has tool events, else fall back to P-B2". That switch has no
correct granularity: a pre-N2 execution that receives a new turn on a new
daemon (F7) has raw-only turns **and** raw + N2 turns in one event log,
and §2 #53 forbids reading "no `tool_use` events" as "no tools". The
capability object also cannot be consulted from a pure reducer.

So the switch is **per tool call, decided by the events themselves**: the
raw rules (P-B2 A1–A4) keep running for every execution and create the
entry; the N2 rules **overlay** the server's facts onto the same entry.
Where both paths carry a field the N2 value wins; where only raw exists
the P-B2 value stays. A tool call is one map entry keyed by `tool_use_id`
whichever events arrive, so it can never be counted twice, and
`messages` is still built from raw frames only (N2 kinds are never
appended — the alpha.405 guard becomes the consumer).

`capabilities.tool_events` is typed (G6) but **not consulted at runtime**
by the pane: the overlay is self-describing. It remains the right check
for a client that wants to *promise* N2 facts (e.g. a list view that
would otherwise open a scoped stream) — documented on the type.

### 4.2 `ToolActivity` v2

```ts
export interface ToolActivity {
  name: string
  startedAt: number                 // P-B2: ev.created_at of the raw assistant frame (0 = unknown)
  endedAt: number | null            // P-B2: ev.created_at of the raw user frame
  status: 'running' | 'done' | 'error' | 'denied' | 'aborted'
  // ---- N2 overlay (all optional; absent = not (yet) seen from N2) ----
  primaryArg?: { key: string; value: string } | null   // null = known tool with no primary key (F9)
  known?: boolean
  durationMs?: number | null        // null = unmatched (contract rule 9)
  output?: { totalLines: number; totalBytes: number; truncated: boolean; hasNonText: boolean }
  file?: { path: string; lines: number }
  diff?: { path: string; added: number; removed: number; hunks: DiffHunk[]; truncated: boolean }
}
export interface DiffHunk { oldStart: number; oldLines: number; newStart: number; newLines: number; lines: string[] }
```

`denied` is a new status value: `toToolCallActivity` maps it to a new
renderer variant (§4.4 R3); `endTurn` only touches `running` so it is
unaffected; `recordToolEnds` treats `denied` like `done` / `error`
(final, not overwritten by a later raw result).

### 4.3 Reducer rules (N1–N6), after the seq guard

Run inside `applyTurnRules` for `ev.kind === 'tool_use' | 'tool_result'`;
the kinds are then **not** appended to `messages` and do not touch
`partial`, `turnLive`, `pendingSend` or the summary.

- N0 Subagent frames (`payload.parent_tool_use_id != null`) are ignored,
  exactly like raw A1/A2. (The existing early return in `applyTurnRules`
  already does this for non-lifecycle kinds.)
- N1 `tool_use` with string `tool_use_id`:
  - unseen → `tools[id] = {name, startedAt: ev.created_at, endedAt: null,
    status: 'running', primaryArg, known}`. The raw `assistant` always
    arrives first (F2; the raw + derived batch is published in order by
    the pump goroutine — nexen issue #83's reorder is between the API
    goroutine's `Emit` and the pump, never inside one batch), so A1 has
    already created the entry and this branch is a **fail-safe only**;
    when it does fire, A1's "first sighting wins" then skips the entry.
  - seen → set `name` (if the entry's is empty), `primaryArg`, `known`;
    **never** reset `startedAt`, `endedAt` or `status`.
  - `primaryArg` is copied only when the `primary_arg` key is present in
    the payload (`null` is a value — F9 — and is copied as `null`); an
    absent key (stripped stream) leaves the field untouched. `known` is
    copied only when boolean.
- N2 `tool_result` with string `tool_use_id`:
  - unseen → create `{name: name ?? '', startedAt: 0, endedAt:
    ev.created_at, status: <mapped>}` (contract rule 9 ①: a result
    echoing a `tool_use` the store never got; the renderer has no
    `assistant` block to attach it to, but the raw `user` result block
    still finds the facts by `tool_use_id`).
  - seen → `status = mapped` (**overrides** whatever raw A2 or A3 set —
    `denied` is only knowable here, and a result arriving after A3's
    `aborted` corrects it like A2 does), `endedAt = endedAt ??
    ev.created_at`.
  - mapping: `ok → 'done'`, `error → 'error'`, `denied → 'denied'`; any
    other string → leave `status` as is (closed set; fail-safe).
  - copy `durationMs` (number or null), `output` facts (without `text`),
    `file`, `diff` — each only when the payload key is present and
    well-formed (`obj()` guards as in `content-blocks.ts`); absent keys
    leave the fields absent (rule 7: absent ≠ null).
- N3 Idempotence: applying the same N2 event twice (cannot happen past the
  seq guard, but replay tests do it) yields the same entry.
- N4 `endTurn` (A3) unchanged: at a turn-ending event every `running`
  entry becomes `aborted`. Because N2 never writes `running`, an entry
  the N2 `tool_result` has finished can never be aborted afterwards.
- N5 Raw A2 must not downgrade an N2 status: `recordToolEnds` skips
  entries whose status is `done | error | denied` (today: `done | error`).
  Ordering per F2 is raw-then-N2, so in practice A2 runs first; N5 covers
  the reverse order.
- N6 Shape tolerance: a `tool_use` / `tool_result` without a string
  `tool_use_id` only advances `lastSeq` (as today). Payload fields are
  taken by type check, never by trust; a malformed `diff` (non-array
  hunks, non-numeric counts) is dropped as a whole rather than partially
  copied.
- N7 Duplicate `tool_use_id` within a turn (contract rule 9 ②): the
  daemon still emits every `tool_use` but never matches the later
  `tool_result`s (echo fields `name` / `message_id` / `block_index` and
  `duration_ms` all `null`, `status` still set). The SPA keeps **one**
  entry per id — exactly what P-B2 A1 ("first sighting wins") already
  does for the raw blocks — so both DOM blocks decorate from the same
  entry: N1 on the second sighting leaves `startedAt` / `status` alone;
  N2 with `name: null` keeps the entry's name, sets the mapped `status`,
  `durationMs: null`, and R2 then falls back to `endedAt − startedAt`.
  This is "寧可不配，不配錯" at the display level: nothing is invented,
  and the id is never split into two entries (which is what "counted
  twice" would mean).

#### Equivalence of the two paths (what the user sees per field)

| Rendered field | raw-only entry (P-B2) | raw + N2 entry | rule |
|---|---|---|---|
| tool name | `block.name` | same (`tools.name` is only a fallback for the orphan-result case) | — |
| header summary | `getSummary(tool, input)` client table | `primaryArg.value`; `known === false` → first 3 input keys; `primaryArg === null` (F9) → `getSummary` | R1 |
| running elapsed | `now − startedAt` (ticker) | same (N2 has no running state) | — |
| finished duration | `endedAt − startedAt` | `durationMs` when a number, else P-B2 value | R2 |
| status glyph | done / error / aborted | + `denied` (struck through) | R3 |
| result facts line | — | `file.lines`, `diff.added/removed`, `output.totalLines`, truncated / non-text markers | R4 |
| result body | raw content string | raw content string, plus the diff view when `diff` exists | R5 |

A tool call appears exactly once in the DOM in both columns because the
DOM is driven by the raw `assistant` / `user` blocks (unchanged) and the
`tools` map only decorates them.

### 4.4 Rendering

- R1 `ToolCallBlock` receives `summary` resolved by a pure helper
  `toolSummary(tool, input, entry?)` in `lib/nex/tool-summary.ts`:
  `entry?.primaryArg?.value` → else `entry?.known === false` → R10
  fallback `key: value` pairs for the first three `input` keys (values
  stringified, scalars verbatim, objects as JSON) → else the existing
  `getSummary` table (moved into the same module). Truncation to
  `SUMMARY_MAX` stays in the renderer (client display decision, contract
  rule 11). `getSummary`'s own table shrinks to nothing new: the N2 table
  is the source of truth, the client table is only the pre-N2 fallback.
- R2 `TimingBadge` for `done | error | denied`: `formatDuration(durationMs)`
  when `durationMs` is a number, else the P-B2 `endedAt − startedAt`
  (guarded by `> 0` as today). `ToolCallActivity` gains
  `durationMs?: number | null` on the finished variant and a new
  `{ status: 'denied'; durationMs?; startedAt; endedAt }` variant. The
  badge's `switch` becomes **exhaustive** (`default` replaced by a
  `never` check) so a future variant cannot silently render nothing.
- R3 `denied`: tool name rendered `line-through text-text-muted`, badge
  text `t('execution.tool.denied')` in the warning colour, wrench icon
  (not the error glyph — a denial is not a failure, contract rule 2).
  The **result** block of a denied call carries `is_error: true` on the
  raw frame (rule 2: claude flags denials as errors too); with
  `facts.status === 'denied'` `ToolResultBlock` renders the neutral
  (non-error) colours, the `Prohibit` icon and the same `denied` badge,
  overriding `isError`.
- R4 `ToolResultBlock` gains an optional `facts?: ToolResultFacts` prop
  (`Partial<Pick<ToolActivity, 'output' | 'file' | 'diff' | 'status'>>` —
  `Partial` because `status` is required on the entry but a caller may
  hand over only the facts it has).
  `ConversationMessages` passes `tools?.[block.tool_use_id]` for raw
  `user` `tool_result` blocks. Header, after the existing 80-char summary:
  a muted, tabular-nums facts span built by a pure helper
  `toolResultFacts(facts, t)` in `lib/nex/tool-result-facts.ts`:
  - `file.lines` → `t('execution.tool.lines', { n })` with the `{{n}}` placeholder ("4 lines");
  - `diff` → `+N −M` (always, `+0 −0` included — `hunks: []` is normal,
    contract rule 7);
  - else `output.totalLines > 1` → `N lines`;
  - `output.truncated` → `t('execution.tool.truncated')` marker
    ("truncated") — the body below still shows the raw frame's full
    content, so no byte counts are needed;
  - `output.hasNonText` → `t('execution.tool.non_text')` marker.
  Absent `facts` → today's header byte-for-byte. `ToolResultBlock` has
  no snapshot today, so P-B3.2 **first** lands a snapshot of the
  unchanged component (collapsed + expanded, ok + error) in its own
  commit; every later renderer task must keep those snapshots
  byte-identical (a same-version self-comparison proves nothing).
- R5 Diff view (`components/ToolDiffView.tsx`, pure presentational): when
  `facts.diff` exists and `hunks.length > 0`, the expanded body renders it
  **above** the raw content: per hunk a header row
  `@@ -old_start,old_lines +new_start,new_lines @@`, then one row per
  line with two right-aligned tabular-nums number columns (old / new;
  blank on the side that has no line), a 2ch sign column, and the text;
  `+` rows get a green-tinted full-row background, `-` rows a red-tinted
  one, `\` (no-newline marker) rows render muted and italic with no
  numbers. Line numbers are computed from `old_start` / `new_start` by
  walking the lines (` ` advances both, `-` old, `+` new, `\` neither).
  `diff.truncated` → trailing muted row `t('execution.tool.diff_truncated')`.
  Long lines wrap (`whitespace-pre-wrap break-all`), unified only.
  Colours are hard-coded like the neighbouring blocks with the same TODO
  comment; no theme-token work in this phase.
- R6 i18n keys (en + zh-TW): `execution.tool.denied`,
  `execution.tool.lines`, `execution.tool.truncated`,
  `execution.tool.non_text`, `execution.tool.diff_truncated`.

### 4.5 Types

`NexCapabilities.tool_events?: { output_max_bytes: number; diff_max_lines:
number }` in `lib/nex/types.ts`, with the doc comment stating: presence =
daemon emits N2 events (contract §0); the exec pane does not branch on it
(§4.1); the numbers are the daemon's actual caps and must not be
hard-coded by any client that shows "showing X of Y".

### 4.6 What does not change

- `useExecutionSubscription`, `nex-sse.ts`, the transient queue, the
  partial assembly (P-B2 §4.1 T-rules and D-rules), `endTurn`,
  `ThinkingIndicator` truth table (R3 of P-B2), auto-scroll.
- The seq guard and the "N2 kinds are never messages" behaviour of
  alpha.405 — only the early return moves into the per-kind rules.
- Daemon, `bin/pdx`, nexen pin.

## 5. Phases

Each phase is one PR (≤ 800 lines diff, ≤ 20 files), TDD by subagent,
independent commits per task, codex R1 + adversarial R2 per PR.

### P-B3.1 — reducer + types (no visible change on existing data)

- `types.ts`: `tool_events` (§4.5).
- `tool-activity.ts`: `ToolActivity` v2, `DiffHunk`, `denied` status,
  `recordToolEnds` N5, new `recordN2ToolUse` / `recordN2ToolResult`
  (N1 / N2 / N6) with the `obj()` guards from `content-blocks.ts`.
- `event-reducer.ts`: route the two kinds through `applyTurnRules` (N0)
  to the new rules; keep them out of `messages`; delete `N2_TOOL_KINDS`.
- `toToolCallActivity`: `denied` variant, `durationMs` passthrough.
  `TimingBadge` loses its `default` branch (exhaustive `never` check) and
  gains the minimal `denied` badge + i18n key in the same commit — the
  only renderer touch in this PR, needed so the widened union cannot fall
  through silently. `durationMs` itself is consumed in P-B3.2.
  Also: the daemon-side `status` triple (`ok`/`error`/`denied`) is a
  closed set, so no other status value can reach the union.
- Fixture `lib/nex/__fixtures__/n2-tool-events-06GBBX07.json`: the 24
  events of `06GBBX0791PP0RQ4WSWDFY5FPM` (seq 909–932) as returned by
  history, verbatim. Tests: history replay of the fixture yields three
  entries with `durationMs` 26 / 24 / 752, Read `file.lines 4`, Edit
  `diff.added 1 / removed 1 / 1 hunk`, Bash no `file` / `diff`; the same
  fixture with the N2 events **filtered out** yields the P-B2 result
  (status / timing identical, no overlay fields) — the equivalence table;
  a synthetic sequence where the N2 event has the **lower** seq (N1
  fail-safe) yields one entry **and** the raw `assistant` frame still in
  `messages`; unmatched result creates an entry; duplicate-id result with
  null echo fields (N7) keeps the name and sets `durationMs: null`;
  `denied` overrides A2's `done`; malformed `diff` dropped;
  subagent N2 ignored; N2 kinds never in `messages`; `endTurn` after an
  N2-finished entry leaves it finished.
- Mutation check (feedback memory): the equivalence test must fail when
  the N2 rules are stubbed out (assert on `durationMs`, not only on
  status).

### P-B3.2 — header summary, status, duration, facts line

- Baseline snapshots of the unchanged `ToolResultBlock` (R4) — first
  commit of the PR.
- `tool-summary.ts` (R1) + tests (primary arg / known:false fallback /
  F9 null → client table / no entry → client table / truncation stays in
  renderer).
- `ToolCallBlock` R1–R3; `ToolUseBlock` passes the entry.
- `tool-result-facts.ts` + `ToolResultBlock.facts` (R4) including the
  `denied` override of `isError` (R3); `ConversationMessages` passes
  `tools?.[tool_use_id]`.
- i18n keys (R6). Snapshot guard: no-`facts` / no-entry rendering
  byte-identical to the baseline snapshots.

### P-B3.3 — diff view

- `ToolDiffView.tsx` (R5) + line-number walker as a pure helper
  (`lib/nex/diff-lines.ts`, tested on the fixture hunk and on a synthetic
  multi-hunk + `\ No newline` case).
- Mounted in `ToolResultBlock`'s expanded body.

## 6. Acceptance (real machine, mlab, worktree dev server :5175)

Daemon already at alpha.405 / nexen v0.12.0; no deploy. Open the worktree
SPA (`pnpm dev --port 5175 --host 100.64.0.2 --strictPort`) against the
mlab host and:

1. **History, N2** — open execution `06GBBX0791PP0RQ4WSWDFY5FPM`: Read
   header shows the file path from `primary_arg`, badge `26ms`
   (`formatDuration` of `duration_ms`), result facts `4 lines`; Edit
   badge `24ms`, facts `+1 −1`, expanded result shows the hunk with
   numbers `1 1  hello / 2    -world / 2 +nexen / 3 3  three`; Bash
   `752ms`, facts none (single-line output).
2. **History, raw-only** — open `06GB2ZFDHNCW2ZWQ33EG9D1ZXM`: seven tool
   calls render exactly as on alpha.405 (client summaries, `endedAt −
   startedAt` badges, no facts spans, no diff).
3. **Mixed** — send one small turn to the execution of step 2 that uses
   Read once: the old seven calls unchanged; the new call shows the N2
   facts; total tool blocks = 8 (no duplicates), `tools` map size 8 in
   the store devtools.
4. **Live** — while step 3's turn runs: spinner + elapsed ticker on the
   running Read, then the badge switches to the `duration_ms` value (not
   the client-clock delta).
5. **Denied** — delegate with `sandbox_profile: readonly` and a brief that
   tries `Write`: the Write call renders struck through with the `denied`
   badge; its result block is not shown as an error.
6. Archive the executions created for acceptance.

## 7. Risks

- Issue #83 (nexen): the live reorder is between the API goroutine's
  `Emit` (lifecycle / lease kinds) and the pump (raw + derived batches);
  a raw + derived batch is never split. Under a real reorder the
  reducer's seq guard (P-B §4.2.4, `ev.seq <= lastSeq`) **drops the
  lower-seq event for good** — a reconnect with `Last-Event-ID` cannot
  bring it back. That is a pre-existing gap of the whole event path,
  not of this phase; N1's fail-safe cannot fix it (the raw frame would
  be lost before it reaches the reducer). Tracked as a follow-up issue
  (SPA: tolerate a bounded backwards seq window, or refetch history on
  detection); root fix is nexen #83.
- Store growth: facts per entry are a few hundred bytes plus hunks (≤ 2000
  lines by the daemon cap); the raw frames already dwarf this.
- `denied` widening the status union: `toToolCallActivity` is an
  exhaustive switch, `TimingBadge` becomes one in P-B3.1 (it has a
  `default` today, so the compiler alone would not have caught it).
- Snapshot churn: every renderer change is guarded by "no entry / no
  facts → byte-identical" tests, so the Stream-mode-era snapshots stay.

## 8. Open questions

None blocking. Recorded for the plan: whether `tool-summary.ts` should
keep the client table at all once P-B2-era executions age out — kept for
now (F7 proves they exist).

## 9. Review log

- 2026-09-19 codex plan + spec review `task-mu7ckdhj-wo5ha7`
  (gpt-5.6-sol), six findings: (1) #83 reorder claim — partly agreed:
  the N1 rationale was wrong (the reorder never splits a raw + derived
  batch) and the planned swap test would have been masked by the seq
  guard; rewritten (N1, §7, plan Task 4) and the seq-guard gap filed as
  a follow-up; (2) denied result block still red — agreed, R3 extended
  and plan Task 8; (3) duplicate `tool_use_id` — agreed as pre-existing
  P-B2 behaviour, documented as N7 with a test; (4) self-comparing
  snapshot — agreed, baseline snapshot task added; (5) `TimingBadge`
  `default` branch — agreed, exhaustive in P-B3.1; (6) `Pick` types —
  half: `Pick` keeps optionality so `primaryArg`/`known` are fine,
  `status` is required so `ToolResultFacts` became `Partial<Pick<…>>`.
