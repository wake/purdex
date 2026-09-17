# Spec — P-B2: exec mode live streaming (typewriter + tool activity)

- Status: v1 draft (2026-09-18)
- Predecessor: `2026-09-15-pb-execution-pane-spec.md` (P-B, shipped
  alpha.350/351/353). Its §4.2.3 transport and §4.2.4 reducer rules stay
  binding; this spec only adds what P-B explicitly deferred to "P-B2".
- Contract source of truth: `nexen/docs/contract/capability-matrix.md`
  (`transient_events`, `stream_snapshot` shape) at the pinned
  `lab.protype.tw/wake/nexen v0.11.2`. **No Nexen-side change** in this
  phase.
- Successors: P-C (launch UI / sidebar / handoff), P-D (rip out Stream mode,
  relay, M0). Server-side tool summaries / diff hunks / cost hover remain
  blocked on Nexen N2 and are **not** here.

## 1. Problem

The exec pane (`{kind:'execution'}`) renders a turn only when its durable
frames land: an `assistant` message appears whole, a running tool is
indistinguishable from a finished one, and an observer tab (not the sender)
sees nothing at all while a turn is in flight. Nexen already streams the
missing information — `stream_event` deltas, a `stream_snapshot` for late
subscribers, and `created_at` on every durable event — but the P-B reducer
drops every frame without an `id:` line
(`spa/src/hooks/useExecutionSubscription.ts:177-190`,
`spa/src/lib/nex/event-reducer.ts:74`), and the renderer has no notion of
"in progress" (`spa/src/components/ConversationMessages.tsx`,
`ToolCallBlock.tsx` — expand/collapse only).

## 2. Goals / non-goals

### Goals

- G1 **Typewriter**: assistant text and thinking render incrementally from
  `stream_event` deltas, with a cursor, and are replaced block-by-block by the
  durable `assistant` frames — never duplicated, never left behind after the
  turn ends. Late subscribers (second tab, reconnect) start from
  `stream_snapshot`.
- G2 **Tool activity**: a `tool_use` whose `tool_result` has not arrived shows
  a running indicator and a ticking elapsed time; a finished one shows its
  duration. While the tool call's input is still streaming it shows the tool
  name and the raw partial input.
- G3 **Turn liveness for observers**: the "thinking" indicator reflects the
  execution's turn state (from the event stream), not only this tab's
  `pendingSend`, so an observer tab sees that a turn is running.
- G4 **Reconnect keeps the buffer honest**: after a reconnect the partial
  buffer is re-seeded by the snapshot or cleared by the replayed turn-ending
  event; no ghost partial text survives an interrupt, orphan, or restart.
- G5 Client-only, pure-reducer design: every rule in §4 is testable without
  React or fetch; Stream mode (`ConversationView`) is untouched.

### Non-goals

- `tool_progress` heartbeats. **Measured absent**: Claude Code 2.1.275
  `-p --output-format stream-json --include-partial-messages` running a
  6-second Bash emits no `tool_progress` frame (29 frames: 19 `stream_event`,
  5 `system`, 2 `assistant`, 1 `user`, 1 `result`, 1 `rate_limit_event`), and
  Nexen v0.11.2 has zero references to it. Purdex's
  `STREAM_JSON_PROTOCOL.md:504-516` shape is historical; §4.2 derives
  activity from `tool_use`/`tool_result` instead.
- Server-side tool summaries, `Update` diff hunks, cost hover (Nexen N2).
- Subagent partials: `stream_event` frames with a non-null
  `parent_tool_use_id` are ignored (their durable `assistant`/`user` frames
  render exactly as today). Nested streaming is a later polish.
- "Load older" / tail window for long histories (still P-B2-later; no
  `before=` in Nexen).
- Renaming `execution` → `exec` in code, routes, or the CLAUDE.md "three
  modes" text (P-D).
- Any change to `nex-sse.ts` reconnect policy. P-B shipped backoff, jitter,
  `Last-Event-ID`, idle timeout, and reconnect-refetch
  (`spa/src/lib/nex/nex-sse.ts:88-96, 165-208`; 18 tests). The old memory
  note "WS never reconnects" described the pre-P-B relay path.

## 3. Measured facts this design rests on

Nexen v0.11.2 (`~/tmp/go/pkg/mod/lab.protype.tw/wake/nexen@v0.11.2`):

- F1 Transient split is one rule: `execution/turn.go:296-301` — kind
  `stream_event` → `bus.PublishTransient` only; every other provider kind is
  durable. `api/capabilities.go:96` advertises
  `transient_events = ["stream_event","stream_snapshot","lease.renewed"]`.
- F2 SSE framing (`api/sse.go:299-315`): durable frames carry `id: <seq>`;
  transient frames carry **no `id:`** → `sse-parser.ts` already yields
  `id: null` for them. `Last-Event-ID` names a durable seq only.
- F3 Subscribe order (`api/sse.go:178-193`): `stream_snapshot` frame(s)
  first (only if a block is in progress — otherwise nothing), then durable
  replay from `Last-Event-ID`, then live. `attach(observe).stream_url` has no
  `kind=` filter, so the pane receives all of them.
- F4 `stream_snapshot` payload (`bus/partial.go:37-52`):
  `{message_id, blocks:[{index, type?, text?, thinking?, partial_json?}]}` —
  only not-yet-finalized blocks, sorted by index, exactly one content field
  set; `partial_json` is a raw byte prefix, never parsed.
- F5 Assembly finalization (`bus/partial.go:172-202`): the Nth durable
  `assistant` frame of a message finalizes the Nth block (claude emits **one
  `assistant` frame per content block**, all sharing `message.id`). `result`
  and every turn ending (`ClearAssembly` on interrupt/error/orphan) clear the
  assembly server-side.
- F6 `created_at` on `NexEvent` is unix **milliseconds**
  (`store/execution.go:155-157`, `store/event.go:19`).
- F7 Restart reconcile (`execution/reconcile.go`): a turn still `running`
  with no live handle → `execution.turn_orphaned`, execution settles to
  `idle`; never respawns.

Claude Code 2.1.275 wire sample (scratchpad `tp.jsonl`, 2026-09-18):

- F8 `stream_event.event.type` sequence per message: `message_start`
  (carries `event.message.id`) → `content_block_start` (`index`,
  `content_block.type`, and for `tool_use` also `id` + `name`) →
  `content_block_delta`× (`delta.type` ∈ `text_delta` | `thinking_delta` |
  `input_json_delta` | `signature_delta`; deltas carry `index`, **not** the
  message id) → `content_block_stop` → `message_delta` → `message_stop`.
- F9 The durable `assistant` frame for a block arrives **before** that
  block's `content_block_stop` (`assistant` at line 14, `content_block_stop`
  at 15; again 25/26). Its `message.id` equals the `message_start` id.
- F10 Every frame carries top-level `parent_tool_use_id` (null for the main
  agent).

Purdex today (this worktree, alpha.376):

- F11 `ExecutionState` has no partial buffer (`event-reducer.ts:9-34`);
  `applyDurableEvent` pushes every non-lifecycle payload into `messages`
  (`:110-118`); `frameToEvent` returns null for `id == null` (`:74`).
- F12 `ConversationMessages` renders only `assistant` / `user` messages and
  keys by index; `ToolCallBlock({tool, input})`; `MessageBubble({role,
  content})` runs `ReactMarkdown` on every render; `ThinkingIndicator` is
  driven by `pendingSend` only (`ExecutionView.tsx:75`).
- F13 `AssistantMessage.message` in `spa/src/lib/stream-ws.ts:16-23` has no
  `id` field (the wire has one).

## 4. Design

### 4.1 Partial assembly in the reducer

`spa/src/lib/nex/event-reducer.ts` gains:

```ts
export interface PartialBlock {
  index: number
  type: 'text' | 'thinking' | 'tool_use' | 'unknown'
  text: string            // text_delta accumulation
  thinking: string        // thinking_delta accumulation
  partialJson: string     // input_json_delta accumulation, raw, never parsed
  toolId?: string         // from content_block_start.content_block.id
  toolName?: string       // from content_block_start.content_block.name
}
export interface PartialAssembly {
  messageId: string | null
  /** Blocks with index < watermark are finalized; late deltas for them are dropped. */
  watermark: number
  blocks: Record<number, PartialBlock>   // sparse, only unfinalized indices
}
export interface ExecutionState {
  …existing…
  partial: PartialAssembly | null
  turnLive: boolean
  tools: Record<string, ToolActivity>   // §4.2
}
export function applyTransientFrame(s: ExecutionState, kind: string, payload: Record<string, unknown>): ExecutionState
```

Rules (mirroring Nexen's console reducer, `internal/webui/static/ui/reducer.mjs:317-435`,
adapted to Purdex's "durable list + one trailing partial" render model):

- T1 Frames with non-null top-level `parent_tool_use_id` → return `s`.
- T2 `stream_event` / `message_start` → `partial = {messageId: event.message.id,
  watermark: 0, blocks: {}}`, `turnLive = true`.
- T3 `stream_event` / `content_block_start` → if `index < watermark` drop;
  else create `blocks[index]` with `type` from `content_block.type`
  (`tool_use` also records `toolId`/`toolName`). If `partial` is null (no
  `message_start` seen, e.g. joined mid-message with an empty snapshot),
  create it with `messageId: null`.
- T4 `stream_event` / `content_block_delta` → if `partial` null, create as in
  T3; if `index < watermark` drop; if no block at `index`, create one with
  `type` inferred from `delta.type` (`text_delta`→text,
  `thinking_delta`→thinking, `input_json_delta`→tool_use, else unknown).
  Append `delta.text` / `delta.thinking` / `delta.partial_json` to the
  matching field. `signature_delta` and unknown delta types are absorbed
  (no change).
- T5 `content_block_stop`, `message_delta`, `message_stop` → no state
  change (F9: the durable frame is what finalizes, and it has already
  arrived by `content_block_stop`).
- T6 `stream_snapshot` → `partial = {messageId: payload.message_id,
  watermark: min(block.index) or 0, blocks: from payload}` (assignment,
  not merge — the server's view wins), `turnLive = true`. A malformed
  snapshot (no `message_id` string / `blocks` not an array) → return `s`.
- T7 Any other transient kind (`lease.renewed`, unknown) → return `s`.
- T8 `applyTransientFrame` never touches `lastSeq`, `messages`, or the
  lease fields.

Durable interactions, added to `applyDurableEvent` (seq-idempotency rule
unchanged — a replayed frame with `seq <= lastSeq` still returns `s` before
any of this runs):

- D1 `assistant` with null `parent_tool_use_id` and `partial != null`:
  - if `message.id === partial.messageId` (or `partial.messageId` is null):
    delete the **lowest-index** block in `partial.blocks` and set
    `watermark = thatIndex + 1`; if `blocks` becomes empty keep the
    assembly (more blocks of the same message may follow) — it is cleared
    by D3.
  - if `message.id` differs: `partial = null` (a newer durable message
    supersedes whatever was buffering; fail-safe against stale text).
  Rationale for "lowest index" over a counter: a snapshot may start at
  index > 0 (F4), and F5 guarantees finalization order, so the lowest
  remaining index is always the one being finalized.
- D2 `execution.running`, `execution.message_accepted` → `turnLive = true`.
- D3 `result`, `execution.terminal`, `execution.error`,
  `execution.rejected`, `execution.terminated`, `execution.interrupted`,
  `execution.turn_orphaned`, `execution.turn_stalled` → `partial = null`,
  `turnLive = false`, and §4.2 A3.
- D4 `execution.archived` → as D3 (an archived execution cannot be live).

`clearExecution` / `clearHost` / `defaultExecutionState` include the new
fields; `historyLoaded` semantics unchanged (history replay drives
`turnLive` and `tools` to the right value before the SSE opens, because the
replayed `execution.running` / `result` pairs run through D2/D3).

### 4.2 Tool activity from durable frames

```ts
export interface ToolActivity {
  name: string
  startedAt: number          // ev.created_at (unix ms, server clock)
  endedAt: number | null
  status: 'running' | 'done' | 'error' | 'aborted'
}
```

- A1 `assistant` (any `parent_tool_use_id`) → for each `tool_use` block
  with an `id`: if unseen, `tools[id] = {name, startedAt: ev.created_at,
  endedAt: null, status: 'running'}`. First sighting wins (history replay
  and live never disagree because seq idempotency already dedups).
- A2 `user` → for each `tool_result` block with `tool_use_id` in `tools`
  and `endedAt == null`: `endedAt = ev.created_at`, `status = is_error ?
  'error' : 'done'`.
- A3 On D3 events: every `tools[*]` still `running` → `endedAt =
  ev.created_at`, `status = 'aborted'`.
- A4 `created_at` of 0 (the `frameToEvent` fallback for a frame without a
  wrapper) → `startedAt = Date.now()` is **not** used in the reducer (pure);
  instead `startedAt = 0` and the renderer treats `0` as "unknown, show no
  timer". Nexen always sends the wrapper, so this is a guard, not a path.

Elapsed time is computed in the renderer as `max(0, now - startedAt)` with
`now` from a 1 s ticker that runs only while at least one tool is
`running` (`useElapsedTicker(active: boolean)`); server/client clock skew
across the tailnet is milliseconds and negatives clamp to 0.

### 4.3 Hook wiring and delta coalescing

`useExecutionSubscription.ts` `onFrame`:

```ts
if (frame.id == null) { pending.push(frame); scheduleFlush(); return }   // transient
```

- Transient frames are parsed (`JSON.parse(frame.data)`; failure → drop
  silently, no cursor involved) and applied through a new store action
  `applyTransient(hostId, executionId, frames: {kind, payload}[])` that
  folds them with `applyTransientFrame` in one `set()`.
- **Coalescing**: transient frames are queued and flushed at most once per
  animation frame (`requestAnimationFrame`, falling back to `setTimeout(…,
  16)` in tests / non-browser). Durable frames flush the transient queue
  first, then apply, so ordering between a delta and its finalizing
  `assistant` frame is preserved. `close()` / unmount drops the queue.
  Rationale: claude emits tens of deltas per second; one store write per
  delta re-renders `ReactMarkdown` for every keystroke, which Nexen's
  console also avoids by throttling to one rAF (`detail.mjs:1169-1183`).
- `onStatus('reconnecting')` leaves `partial` untouched (G4: the snapshot
  or the replayed turn-ending event corrects it — F3, F5, F7).

### 4.4 Rendering

`ConversationMessages` gains optional props; Stream mode passes none and
renders exactly as today (its snapshot test is the guard):

```ts
partial?: PartialAssembly | null
tools?: Record<string, ToolActivity>
now?: number                 // ticker value for running tools
```

- R1 After the durable `messages` list and before `children`, if `partial`
  has any block, render one trailing assistant group (key
  `${keyPrefix}-partial`) with blocks in ascending index:
  - `text` → `MessageBubble role="assistant" content={text} streaming` —
    `streaming` appends a blinking cursor (`▌`, CSS animation, token-based
    colour) after the markdown body. Empty `text` renders nothing.
  - `thinking` → `ThinkingBlock content={thinking} streaming` (same cursor).
  - `tool_use` → `ToolCallBlock tool={toolName ?? '…'} input={{}}
    status="streaming" rawInput={partialJson}` — header shows the name and
    the first 80 chars of `partialJson`; expanded shows the raw prefix.
  - `unknown` → nothing.
- R2 Durable `tool_use` blocks pass `status` and timing from
  `tools[block.id]`: `running` → spinning `CircleNotch` (Phosphor) replaces
  the wrench and an `elapsed` badge (`12s`, `1m 05s`) ticks; `done` →
  duration badge (`6.2s`); `error` → duration in the error colour;
  `aborted` → muted "aborted". Missing entry (older history, subagent) →
  today's rendering.
- R3 `ThinkingIndicator` visibility from `ExecutionView`:
  `showThinking = (st.turnLive || (st.pendingSend && st.pendingLocal?.delivery !== 'queued')) && !partialHasVisibleContent`
  where `partialHasVisibleContent` is any block with non-empty text /
  thinking / partialJson. Observers therefore see the dots while the model
  is thinking and the typewriter once tokens flow.
- R4 Auto-scroll: `ConversationMessages`'s scroll effect also depends on a
  cheap `partialVersion` counter (length sum of partial fields) so the view
  follows the typewriter without depending on the whole object identity.
- R5 `AssistantMessage.message` gains `id?: string` (F13);
  `ContentBlock.id` already exists.
- i18n keys (en + zh-TW): `execution.tool.running`, `execution.tool.aborted`,
  `execution.tool.elapsed` (with `{time}`), `execution.tool.streaming`.

### 4.5 What does not change

- `nex-sse.ts`, `sse-parser.ts`, `nex-api.ts`, lease handling, `pendingSend`,
  `pendingLocal`, `lastSeq` semantics, `frameToEvent` (still returns null
  for transient frames — the hook branches **before** calling it).
- Daemon: none. `internal/module/nex` forwards Nexen's handler verbatim
  (`module.go:187-193`); `grep stream_event internal/` is empty and stays so.
- Nexen: none. The pin stays v0.11.2.

## 5. Phases

Two PRs on this worktree branch, second based on the first's merge.

### P-B2.1 — reducer, store, hook (no visible change)

- §4.1 `PartialAssembly` + `applyTransientFrame` + D1–D4 in
  `applyDurableEvent`; §4.2 `ToolActivity` + A1–A4; store `applyTransient`;
  hook branch + rAF coalescing (§4.3); `AssistantMessage.message.id` type.
- Tests: `event-reducer.test.ts` (T1–T8, D1–D4, A1–A4 including the
  snapshot-then-replay sequence from F3, late-delta drop, differing
  message id, subagent frames ignored, turn-ending clears), a golden test
  replaying the 29-frame `tp.jsonl` sample through the reducer end-to-end
  asserting final `messages` equals the P-B result and `partial == null`,
  `useExecutionStore.test.ts` (`applyTransient` fold, clear paths),
  `useExecutionSubscription.test.ts` (transient frames now reach the store;
  coalescing order durable-after-transient; queue dropped on close).
  The existing "drops transient ones" assertion (`:71-83`) flips to
  "applies transient ones without moving `lastSeq`".
- Mutation check (per project feedback): temporarily break D1's
  lowest-index rule and T4's watermark drop and confirm the suite fails.

### P-B2.2 — renderer

- §4.4 R1–R5, `useElapsedTicker`, `ToolCallBlock` status/timing props,
  `MessageBubble` / `ThinkingBlock` `streaming` cursor, `ExecutionView`
  wiring, i18n.
- Tests: `ConversationMessages.test.tsx` (partial group renders after
  durable list, ordering by index, empty-text block renders nothing, no
  partial → DOM identical to today via the existing snapshot),
  `ToolCallBlock.test.tsx` (four statuses, elapsed formatting, `0`
  startedAt shows no timer), `ExecutionView.test.tsx` (R3 truth table:
  observer with `turnLive` sees dots; dots hide once partial text exists;
  `aborted` after `turn_orphaned`).

## 6. Acceptance (real machine, mlab, after P-B2.2 merge + `pnpm run build`)

1. Open an exec pane, send "write a 200-word paragraph": text appears
   incrementally with a cursor, then the cursor disappears when the durable
   frame lands and the paragraph does not duplicate or flicker.
2. Send "run `sleep 8 && echo ok`": the Bash block shows the spinner and an
   elapsed counter reaching ~8s, then a `8.xs` duration badge; the result
   block follows.
3. Open the same execution in a second tab (observer, no lease) **during**
   step 2: it shows the running tool and the elapsed timer; during step 1 it
   shows the typewriter from the snapshot onward.
4. Interrupt mid-paragraph: partial text vanishes, `turnLive` off, no
   trailing cursor, durable history intact.
5. `pdx stop` / `pdx start` during a long tool: after reconnect the tool
   shows "aborted" (from `execution.turn_orphaned`), no ghost partial, input
   re-enabled (execution idle).
6. Stream-mode pane (`ConversationView`) renders unchanged.

Each turn costs real Claude usage; keep prompts small.

## 7. Risks

- **Render cost of markdown per delta**: mitigated by rAF coalescing (§4.3)
  and by the partial group being a separate subtree keyed
  `${keyPrefix}-partial`; the durable list's props do not change on a
  delta. If long paragraphs still stutter, the follow-up is a plain-text
  fast path for the streaming bubble (markdown only on finalize) — noted,
  not built.
- **Wrong block finalized** if claude ever emits `assistant` frames out of
  block order. F5/F9 say it does not; D1's fail-safe (different message id →
  drop the whole partial) bounds the damage to a missing typewriter, never a
  duplicated or misplaced block, because durable rendering is unaffected.
- **Clock skew** makes elapsed timers off by the mlab↔client offset
  (milliseconds on the tailnet); clamped at 0. Duration badges use two
  server timestamps and are skew-free.
- **Stream mode regression** through shared components: guarded by the
  `ConversationView` snapshot test and by all new props being optional.

## 8. Open questions

None blocking. Decisions taken 2026-09-18 (do not reopen without new
evidence): no `tool_progress` (measured absent); subagent partials ignored;
no Nexen change; rAF coalescing in the hook rather than a throttled store.
