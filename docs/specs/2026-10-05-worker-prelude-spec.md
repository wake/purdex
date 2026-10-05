# Worker prelude — show the conversation before a handoff — spec

- Date: 2026-10-05
- Status: v1.2 (codex plan+spec review `task-muv2l1fz-k2vjlu`: 13 findings applied; nexen-85 contract batch: 21 items applied, #9 replaces codex #10's cursor binding)
- Scope: Nexen (new read-only endpoint, delegated to `mlab/nexen-85 [tezrjt]`) + Purdex SPA (worker pane) + Purdex pin bump
- Coordinator: `mlab/purdex-b3 [iqjvew]` (title `purdex-prelude`)
- Prior art: `docs/specs/2026-09-20-worker-pane-views-spec.md` (room / chat), `docs/specs/2026-09-18-pc-launch-ui-spec.md` (Hand to nex), Nexen `docs/specs/2026-09-15-resume-session-id-spec.md` (#65)

## 1. Goal

Taking a worker back to a terminal (`claude --resume`) shows the whole conversation, because the interactive CLI replays the transcript file and the worker's `-p` turns were appended to that same file. The other direction does not: a worker created by **Hand to nex** shows only its own turns, because the pane reads only the execution's Nexen event log, which starts at turn 1.

After this change a handed-off worker shows the **entire earlier conversation** above its first turn, the way the terminal would.

## 2. User decisions (2026-10-05, do not reopen)

| # | Decision |
|---|---|
| U1 | The earlier conversation is **drawn directly**, not hidden behind a one-line "N earlier messages" toggle. The pane still opens at the bottom; reaching the top loads older content automatically. |
| U2 | Show **everything** in the transcript before this worker took over, however many terminal ↔ worker round trips it contains (interactive turns and earlier workers' turns alike). "Every time I can see the full context." |

Derived by the coordinator from U1/U2 and earlier decisions (restated to the user 2026-10-05; not separately asked):

| # | Rule | Derived from |
|---|---|---|
| D1 | The prelude uses the worker pane's own room / chat rendering and fold rules (R1 Q4 thresholds). "Drawn directly" means the section is not collapsed. It does **not** mean that every tool output is force-expanded. | worker-pane-views §2 (one look); U1 |
| D2 | A thin marker line appears wherever the transcript switches between interactive (`cli`) and headless (`sdk-*`) turns, and where the conversation was compacted. | U2 ("see the context"): the reader must be able to tell where each part ran |
| D3 | The prelude carries **no per-turn cost and no typewriter**. Header cost is unchanged (R4 rules, Q3 hover). | R4; interactive turns have no `result` frame |
| D4 | Workers that were not handed off (NewTab, `pdx nex delegate` without resume) have no prelude and look exactly as they do today. | scope |
| D5 | If the transcript file no longer exists (Claude Code's `cleanupPeriodDays`, default 30), cannot be read, or cannot be addressed from this daemon, the top of the pane says so in one line. The worker's own turns are unaffected. | honesty |
| D6 | **Images** in the prelude render as a placeholder (`[image]`, with type and size), and **subagent internals** are not shown: the Task call and its final result are. Both match what `claude --resume` shows in the terminal. Follow-ups track the fuller forms (§9). | U2 measured against the terminal (the stated reference) |
| D7 | Transcript search (Cmd+F) covers the prelude that has been loaded. When older prelude pages are still unloaded, the search bar says so and offers a one-click "load all". | R3 Q4 ("search includes folded content"), adapted to paging |

## 3. Facts this design rests on (measured 2026-10-05, mlab)

**Transcript (CLI 2.1.28x)**

- Handoff appends to the same file under the same session id. The only handed-off execution on mlab is `06GGP7CD…` (`resume_session_id` = `session_id` = `9d6f29ac…`). Its 1.6 MB transcript holds **218 interactive message lines (`entrypoint: "cli"`) followed by 3 worker lines (`"sdk-cli"`)**, and the pane shows only the 3. Nexen's resume probe (`docs/research/2026-07-29-p1a-provider-probes.md:19-21`) agrees.
- Message lines are `{type: "user"|"assistant", message, uuid, parentUuid, sessionId, timestamp (ISO), entrypoint, isSidechain, isMeta?, origin?, toolUseResult?, toolDenialKind?, apiBlockIndex?, …}`. `.message` has the same API-message shape as stream-json frames: **one content block per assistant line**, with lines sharing `message.id`. 40 of the 42 messages in `9d6f29ac…` span 2–6 lines, and `apiBlockIndex` equals the per-message block index.
- Line types across 245 transcripts (last 20 days): assistant 61k, attachment 41k, user 37k, then non-message records (`atis-latch`, `last-prompt`, `mode`, `permission-mode`, `ai-title`, `queue-operation`, `relocated`, `worktree-state`, `pr-link`, `file-history-*`, `cost-state`, `bridge-session`, `agent-name`, `custom-title`, `continued-in`, …), and `system` 8.8k. The `system` subtypes are `stop_hook_summary`, `turn_duration`, `scheduled_task_fire`, `local_command`, `away_summary`, `informational`, `agents_killed`, `compact_boundary` and `api_error`.
- Entrypoints seen: `cli` (97k lines) and `sdk-cli` (1.1k). No other value was observed.
- **Prompts typed while Claude is busy exist only as attachments.** These are `attachment.type == "queued_command"` with `attachment.origin.kind == "human"` and `attachment.prompt` as a string. All 94 sampled had **no** matching `user` line, so dropping attachments would drop things the user said.
- User lines carry `origin.kind` ∈ `human` / `peer` / `task-notification`. Peer messages (pdx) are `isMeta: true` with `origin.kind: "peer"`. Background-task notices are string content starting `<task-notification>`. Slash commands are `<command-name>…</command-name>` (+ `<command-message>`, `<command-args>`), with output in `<local-command-stdout>` and a `<local-command-caveat>` preamble. Bash mode uses `<bash-input>`, `<bash-stdout>` and `<bash-stderr>`.
- `/compact` keeps the earlier messages in the same file. The sampled file has 887 messages, then a `system`/`compact_boundary` line (`compactMetadata.trigger`), then an `isCompactSummary` user line, then 904 messages.
- Subagent conversations live in `<sid>/subagents/agent-<id>.jsonl` (143 sessions have the directory). Main transcripts had no `isSidechain: true` lines.
- Large tool outputs are externalized to `<sid>/tool-results/<id>.txt`. The `tool_result` text then holds a preview (`persistedOutputPath` and `persistedOutputSize` in `toolUseResult`).
- Images are inline base64 (`source.type: "base64"`, 308 blocks sampled). A single transcript line reached **2.2 MB**.
- `continued-in` (`continuedInSessionId`) links forward to a new session file that starts fresh and has no back-pointer. `claude --resume X` shows only file X. **The prelude is bounded to the resume target's file** (§4.4).
- Transcript lines differ from stream-json frames in three ways: `toolUseResult` vs `tool_use_result`, no `tool_result_meta` (denial is top-level `toolDenialKind: "user-rejected"`), and no `parent_tool_use_id`.

**Nexen (main `ae27127`, v0.15.0)**

- Turn 1 resume: `launch.go:308-327` sets `resumeID = exec.ResumeSessionID`. The spawn is `Provider.Start` at `launch.go:540-547`. Between those two points `prepareTurnEnv` can be slow (credentials).
- `execution.running` is emitted at `launch.go:211-219` with `{turn_id, turn_idx, pid, session_id, resumed_from, capabilities, transcript_path}`. Payloads are JSON in a TEXT column, so additive keys need **no schema bump** (precedent: `resumed_from`, `attachments` on `execution.delegated`).
- `transcriptPath(provider, cwd, sid)` is at `addressing.go:78-89`.
- N2 `toolTracker` (`toolevents.go`) has no store access and an injected clock. It reads `tool_use_result` and `tool_result_meta`. N3 derives nothing from a transcript (no `system` `task_*` lines).
- `GET /v1/executions/{id}/events` pages forward only (`after`, `limit` ≤ 500).
- `sessiontitle.Scan` is the only transcript reader. It is read-only, bounded per call (`MaxScanBytes`) and consumes complete lines only.

**Purdex SPA (main, alpha.475)**

- History is loaded forward from seq 0 in `useExecutionSubscription.ts:185-204`, then the SSE opens at `lastSeq` (an order contract). Nothing loads earlier history.
- The reducer appends every non-lifecycle frame to `messages`. `turnStarts` are absolute indexes, and React, fold and search keys are positional (`${i}:${j}`). `finalizedFor` counts assistant frames by `message.id`, and `costSummary(st.messages)` walks `result` frames. **Prelude frames therefore must not enter `messages`.**
- Several components call array methods on `message.content` (`MessageRow.tsx:160,186,190`, `ChatTranscript.tsx:100,125`, `turns.ts:36`). A string `content` would throw.
- `useTranscriptScroll` has no prepend anchoring. The search index is built from data (`buildSearchUnits`), not from the DOM, and `highlightSearch` keeps the first element per id. Prelude ids must be namespaced.

## 4. Nexen contract — `transcript_prelude` (delegated)

### 4.1 Boundary capture

On **turn 1 of an execution with a `resume_session_id`**, Nexen stats `transcriptPath(provider, cwd, resume_session_id)` **immediately before** `Provider.Start` (`launch.go:540`), after `prepareTurnEnv`. It records `prelude_end` = **the end of the last complete line** at that moment: the file size, stepped back to just after the last `\n` if the file does not end in one. Every cursor and the first page's start are therefore line boundaries.

- `prelude_end` is added to that turn's `execution.running` payload as the additive key `transcript_prelude_bytes` (integer). The key is absent when there is no resume or the stat failed.
- **Wiring.** The stat happens inside `startAndRegisterTurn`, just before `Start` (`launch.go:540`). `execution.running` is emitted later, after that function returns (`launch.go:208-219`). So the value must travel out through `startedTurn` (or an equivalent launch-phase result); changing the emit's map alone is not enough. When `Start` or the init handshake fails, there is no `execution.running` and so no key; the legacy fallback below covers that execution.
- **Legacy fallback** applies to executions whose turn-1 `execution.running` lacks the key (created before this release, or the best-effort emit failed). The boundary is then the **start of the first line whose `timestamp` is after** the execution's `execution.delegated.created_at`. Both are in ms, and the comparison is strict, so a line sharing the delegate's millisecond stays in the prelude; the worker's first line is written only after a process spawn. It is defined as "the first line after", not "the last line before", because CLI timestamps are not strictly monotonic (a 1 ms step back was measured), and a step back inside the worker's lines must not pull them into the prelude. This is sound because the consumer contract (#65 rule ④) has the interactive session exit before the delegate. The scan may be slow on very large files, and Nexen may cache the result in memory per execution.

### 4.2 Endpoint

`GET /v1/executions/{id}/prelude?before=<pos>&limit=<n>`. It is authenticated like every route, passes during drain (it is a GET), and is read-only on the transcript (same rule as `sessiontitle`, spec §0.3).

- `before` is optional. When absent, reading starts at `prelude_end`. Otherwise it is an opaque cursor taken from a previous `prev_cursor` (≤ 256 bytes). Every page's `prev_cursor` points strictly older than its `before`, so a client can always make progress.
- **Cursor checks.** A cursor is a line-start offset into this execution's transcript. Nexen checks three things:
  1. Format. A malformed cursor returns 400 `malformed_parameter`.
  2. Range, `0 < offset ≤ prelude_end`. Out of range returns 400 `malformed_parameter`.
  3. That it lands on a line start (the byte before it is `\n`). One that does not means the file was rewritten, so it returns `gone`.

  It is not bound to the execution or a parser version. The caller is in the same trust domain (the cursor carries no security meaning), and lines older than a cursor were never served, so a parser change cannot clash with what a client already holds. A client that gets the 400 for an older page restarts from the first page (no `before`). With the same `limit`, the same cursor returns the same page; adjacent pages never share a `pos`.
- `limit` is the number of **items**: default 200, max 500. A malformed value returns 400 `malformed_parameter`.
- Unknown execution: 404, same as `/events`.

Response:

```jsonc
{
  "state": "ok" | "none" | "gone",
  "items": [ PreludeItem, … ],     // oldest → newest within the page
  "prev_cursor": "…" | null,       // null = this page reached the start of the file
  "total_bytes": 1649190            // prelude_end; lets a client show progress (present on `ok` only)
}
```

- `none`: the execution has no `resume_session_id`, or the provider is not claude. `items` is `[]`, and the SPA never asks again.
- `gone`: the file is missing, unreadable, shorter than `prelude_end`, or cannot be addressed (`transcriptPath` is empty: a non-ASCII cwd or an unknown home, a case #65 still lets resume try). `items` is `[]`. The SPA shows D5, whose copy covers "removed or not readable".
- `ok`: items follow. **Only `prev_cursor == null` means the start was reached.** A page with fewer items than `limit`, or none, but a non-null `prev_cursor` is legal and common: omitted lines (attachments, file-history snapshots, …) cost no payload, so Nexen caps the **raw bytes scanned per request** (about 32 MiB) and may stop with few items. Clients must never use the item count to decide that paging is over.
- `none` is only ever the answer to the first page; it depends only on the execution row. `gone` can also answer an older page, when the file vanished or shrank between requests. The client then keeps what it has and shows the D5 line above it, because that is literally true: the earlier part is no longer available. A `none` on an older page is a contract violation, and the client treats it as an error.

### 4.3 Items

```jsonc
{ "pos": "<string>", "kind": "<kind>", "payload": { … }, "at": 1759651200123 }
```

- `pos` is **stable and unique within the execution's prelude**: it is derived from the source line's byte offset plus a sub-index for items derived from the same line. It matches `^[A-Za-z0-9._-]{1,64}$`; it never contains `:`, because clients splice it into colon-separated keys. Clients treat it as opaque. Clients key React, fold and search state by it. Items are ordered by `pos` (offset, then sub-index), and derived items immediately follow their raw frame (the same order as the durable log).
- `at` is the source line's `timestamp` in unix ms (0 if absent).
- **Lines are atomic.** All items derived from one line (its segment marker, the raw frame, its N2 items) are on the same page.
- **N2 across pages.** `block_index` comes from the line's `apiBlockIndex`; only without it does Nexen fall back to counting, because a page edge can split one message's lines. When a call and its result are on different pages, the `tool_result` item follows the existing N2 unpaired rule (echo fields and `duration_ms` null). So a `pos` is stable and unique, but the payload behind the `pos` of a `tool_result` may differ depending on whether its pair shared the page. With the same `limit` the page sequence is deterministic. A client keeps the first copy it received (dedupe by `pos`).

| `kind` | payload | from |
|---|---|---|
| `assistant` | stream-json-shaped: `{type:"assistant", message, parent_tool_use_id: null, session_id, uuid}`. `thinking` blocks lose their `signature`, and `redacted_thinking` blocks lose their `data`, because interactive transcripts carry 1–2 KB of signature next to an empty thought. A line left with only an empty thinking block is omitted. | assistant lines, except `message.model == "<synthetic>"` |
| `user` | stream-json-shaped: `{type:"user", message:{role:"user", content:[…]}, parent_tool_use_id: null, session_id, uuid}` with **`content` always an array** (a string becomes `[{type:"text",text}]`). **No `tool_use_result` / `tool_result_meta`**: they are inputs to Nexen's N2 derivation (a single Read result measured 680 KB), and clients take tool facts from the N2 items | human prompts (non-meta user lines with `origin.kind` `human` or absent); `queued_command` attachments with `origin.kind == "human"` (synthesized, `uuid` = the attachment line's uuid; the prompt may be a string or a content array with images); tool_result lines; slash-command lines rewritten to the text **the value of `<command-name>` (it already starts with `/`), plus a space and `<command-args>` when non-empty**, with each value read by its tag in whatever order the tags appear (`<command-message>` may come first). **Since v0.16.1 also `system`/`local_command` lines carrying `<command-name>`** (CLI 2.1.289 records commands such as `/model` this way), with the same rewrite |
| `tool_use`, `tool_result` | **same schema as the durable N2 events** (capability-matrix §3), with `turn_id` absent | derived from the two kinds above by the N2 derivation. The clock comes from line timestamps (so `duration_ms` = result line − use line), `toolUseResult` maps to `tool_use_result`, and `toolDenialKind: "user-rejected"` maps to status `denied`. **This needs a seam:** `toolTracker` and its payload structs are unexported in package `execution`; they read snake_case `tool_use_result` / `tool_result_meta`; and their `turn_id` has no `omitempty`. Nexen chooses the seam (a transcript→frame adapter in front of the tracker, an exported constructor with an injected clock, an encoder that omits `turn_id`), as long as the output schema is identical |
| `prelude.segment` | `{entrypoint: "<raw entrypoint>"}` | emitted **before the first item-producing line of every run of equal `entrypoint`**, including the first run in the file. Runs are counted over lines that produce items, so a stretch of omitted lines never yields a marker of its own (no two adjacent markers). To decide, Nexen looks back for the previous item-producing line, across page edges, with a budget; if the budget runs out, it emits the marker anyway (worst case: one extra marker) |
| `prelude.compaction` | `{trigger: "auto"|"manual"|"", pre_tokens?: int}` | `system`/`compact_boundary`. The following `isCompactSummary` user line is **omitted** |
| `prelude.note` | `{source, text, truncated?: bool, total_bytes?: int, stream?: "stdout"\|"stderr"}`. `source` ∈ `command_output`, `bash_input`, `bash_output`, `task_notification`, `peer_message`; it is an open set, and unknown sources render as a generic note. `total_bytes` is present when `truncated`, as on blocks. `stream` appears on `bash_output` only. | `<local-command-stdout>` / `<bash-input>` user strings (tags stripped), and **since v0.16.1** the `<local-command-stdout>` of `system`/`local_command` lines; `<bash-stdout>` and `<bash-stderr>`, which may share one line: **one `bash_output` note per non-empty stream** (since v0.16.1 a `<persisted-output>` wrapper inside a stream, written when the output was too large, is stripped too, keeping its text); `<task-notification>` (the `<summary>` text when present, else tags stripped); `isMeta` lines with `origin.kind == "peer"`; **`queued_command` attachments from a peer (`peer_message`) or a task notification (`task_notification`)**, which, like human ones, exist only as attachments (nexen-85 sampled 645: 128 peer, 370 task-notification) |

**Omitted:** every other `isMeta` line, `<local-command-caveat>`, `isCompactSummary`, all `attachment` lines except `queued_command` (and of those, the `coordinator` origin: `isMeta`, like other meta lines), all non-message record types, and every `system` subtype except `compact_boundary` (and, since v0.16.1, `local_command` lines carrying `<command-name>` or `<local-command-stdout>`).

**Size caps** (advertised in capabilities):

- Every text-bearing content block (`text`, `thinking`, `tool_result` string/text parts, `tool_use.input` serialized) is cut at `max_block_bytes` (65536) on a UTF-8 boundary. A cut block gets `"truncated": true` and `"total_bytes": n` on the block.
- Image blocks and `document` blocks (PDF), at the top level of `content` **and inside `tool_result.content`** (Read of an image file), become `{type:"image"|"document", source:{type:"omitted", media_type, bytes}}`. `bytes` is the **decoded** size, as with v0.14.0 attachments.
- A page stops when it holds `limit` items or `page_max_bytes` (1 MiB of payload), or its raw scan budget runs out. A single item larger than that is a page by itself. **`page_max_items` / `page_max_bytes` are where snapping starts, not hard caps.** Snapping counts payload bytes, not items, so a page can reach 5× `page_max_bytes` and has no fixed item maximum. Clients must not validate pages against these numbers.
- **Edge snapping extends outward.** Having hit its stop, a page keeps reading **older** lines until its oldest line is a human-prompt line, so a page rarely splits a tool call from its result. If no human-prompt line turns up within 4× `page_max_bytes`, the page is cut **at its original stop**, not at the 4× point; the next request searches again. If a page does split a call from its result, the client already tolerates an unpaired result (orphan) and an unpaired call (status from facts).

### 4.4 Scope rules

- Only the file of `resume_session_id` is read, bounded to `[0, prelude_end)`. `continued-in` chains and `subagents/` / `tool-results/` / `images/` directories are not followed (D6).
- Lines written after `prelude_end` (the worker's own turns) are never returned. They are in the event log.

### 4.5 Capability

```jsonc
"transcript_prelude": {
  "route": { "method": "GET", "path": "<PublicPrefix>/v1/executions/{id}/prelude" },
  "page_max_items": 500,
  "page_max_bytes": 1048576,
  "max_block_bytes": 65536
}
```

Presence is the feature detect. Values come from exported Go constants.

### 4.6 Delivery

- Docs: capability-matrix §0, a new §1.x, §2 honesty rows (bounded to one file, no subagents or images, depends on CLI transcript retention, legacy fallback is timestamp-based, an in-place rewrite of the file is not detected, and #62 corrected: `-p` resume rewrites an existing `ai-title` but never creates one), §3 (`execution.running` additive key), §4 pull API; consumer-guide §4 endpoint table, change-log row, and a new §9.x usage section (feature-detect → first page → older pages → `gone`).
- Release **v0.16.0**: no schema bump, upgrading the pin needs no DB delete, no new SSE kind.
- Tests: golden fixtures cut from real transcripts (interactive + handoff + compaction + queued prompt + slash command + bash + peer + task-notification + denied tool + image + an over-cap line), backward paging that is stable across pages (`pos` never repeats or changes), the edge snapping, `gone` / `none`, the legacy fallback (including a line in the same millisecond as `execution.delegated`), cursor binding (another execution's cursor, a cursor from before a boundary change, a replayed cursor), and the boundary captured before the spawn (a `-p` resume line must never appear; a failed `Start` emits no key).
- **Early golden page.** As soon as the endpoint answers on Nexen's branch, before merge, send the coordinator one real `/prelude` response over the golden transcript (every kind in §4.3 present). Purdex P1–P3 test against it, so a wire drift surfaces before the pin bump, not after.
- Side note for Nexen: a `-p` resume transcript was seen with `ai-title` lines. Honesty #62 says `-p` never writes them, so re-check that row when touching §2.

## 5. Purdex

### 5.1 Pin

Bump `lab.protype.tw/wake/nexen` to v0.16.0, then rebuild and redeploy the mlab daemon (`make build` → rm/cp/mv the binary → `pdx stop`/`start` → `/api/health`). No `nex.db` delete. air26 runs with `[nex] enabled = false`, so it is unaffected.

### 5.2 Data layer (SPA)

- Types: `PreludeItem`, `PreludePage`, and `NexCapabilities.transcript_prelude`. Add `selectTranscriptPrelude(hostId)` following the `selectWorkerRollup` pattern.
- `fetchExecutionPrelude(hostId, id, {before?, limit?})` goes through `nexFetch`. A sanitizer at the API boundary drops items with an unknown or ill-shaped `kind`/`pos`, normalizes string `content` defensively even though Nexen promises arrays, and **cleans every content block** so nothing downstream can throw on it:
  - a block without a string `type` is dropped;
  - non-string `text` / `thinking` are dropped from the block;
  - a non-object `tool_use.input` becomes `{}`;
  - a `tool_result.content` that is neither a string nor an array of objects becomes `''`;
  - a non-object image `source` drops the block;
  - `truncated` must be boolean and `total_bytes` a safe non-negative integer.
- Store: add `prelude: { status: 'idle'|'loading'|'ok'|'none'|'gone'|'error', items, cursor, done, error, request }` to `ExecutionState`. It is written only through pure functions:
  - `applyPreludePage(state, page, …)` prepends and dedupes by `pos`.
  - `request` is the id of the page request in flight. A response or failure lands **only if its request id is still the one recorded**, so a late answer can never land on an entry that was cleared and recreated in between (two panes, a handoff swap, host remove and undo).
  - The view's `tools` overlay is derived from the items through the existing N2 functions.
  - None of this **ever** touches `messages`, `lastSeq`, `turnStarts`, `partial`, `tools` or cost.
- A `useExecutionPrelude(hostId, id)` hook, separate from `useExecutionSubscription`, runs only when the capability is present, the summary has `resume_session_id`, and `historyLoaded`. It loads the first page immediately (U1) and exposes `loadOlder()`, `loadAll()` and `retry()`. It does not refetch on SSE reconnect, because the prelude is immutable. A 400 `malformed_parameter` on an older page (§4.2: the cursor's parser version or boundary changed, e.g. the daemon was upgraded) resets the prelude and reloads from the first page.

### 5.3 Rendering

- A `TranscriptPrelude` section is placed first inside the transcript scroll box (`RoomTranscript.tsx` before the turn loop, `ChatTranscript.tsx` likewise), so turn 1 (the handoff brief) follows it.
- It reuses `renderMessage`/`MessageRow` (room) and the chat line components (chat) with a **render namespace**. Messages are named `p<pos>` instead of by index, so React, fold and search ids become `p<pos>:<block>:…`. A number never starts with `p`, so these never collide with the live list's `<index>:<block>`. Operation-index lookups stay local to the prelude list.
- Chat groups prelude operations per human-prompt span, computed client-side, the way chat groups per turn.
- Prelude spans are not `RoomTurnGroup`s, so they get no `data-turn-index` and no hover strip. Scroll memory's `firstTurn` keeps referring to worker turns.
- Markers (D2):
  - `prelude.segment` draws a centered muted rule: `cli` → 「在終端機」 / "In the terminal"; `sdk-*` → 「Headless（worker）」 / "Headless (worker)"; anything else → the raw entrypoint.
  - `prelude.compaction` draws 「對話在此壓縮（自動／手動）」 / "Conversation compacted here (auto / manual)".
  - The section ends with a 「Headless（worker）」 rule right above turn 1 whenever it holds any item: the handoff into this worker is itself a switch (D2), and Nexen never sends a segment for the worker's own run.
- Notes:
  - `command_output` / `bash_output` render as an output block under the preceding command line, using the same fold plan as tool output. A `bash_output` with `stream: "stderr"` uses the error tone.
  - `bash_input` renders as a mono `! cmd` line.
  - `task_notification` renders as a muted one-liner.
  - `peer_message` renders as a labelled block (「Peer 訊息」 / "Peer message") whose body is drawn like agent prose (`RoomProse`: markdown, not folded — prose is never folded in the room).
  - Unknown sources render as a muted generic note.
- An image placeholder shows `[image · png · 120 KB]`, in user and assistant content alike. **Every** block the daemon cut (`truncated: true` on `text`, `thinking`, `tool_use`, or `tool_result`), and every truncated note, shows one hint line after it with the shown and total sizes — **wherever the view draws that block**. A block the view does not draw at all gets no hint (chat hides thinking entirely, D1, so a cut thought shows no hint there). In chat, an operation's hints appear with whatever line represents it (tools line, edited line, failed line).
- The top of the section shows a loading row while a page is in flight, an error row with retry, the D5 line for `gone`, and nothing once done.

### 5.4 Scroll

- Opening the pane works as today (memory, else bottom). The first prelude page arriving while the reader is at the bottom keeps them at the bottom.
- A top sentinel (`IntersectionObserver`) calls `loadOlder()`. It is mounted only while the prelude is `ok` and not done, so an `error` stops automatic paging. It re-arms after every page, so content shorter than the viewport keeps loading until the viewport fills or the prelude is done.
- **Keeping the reader's place.** The prelude section's own height is snapshotted right before the commit that grows it (`getSnapshotBeforeUpdate`), and `scrollTop` is shifted by exactly that section's growth afterwards. Measuring the section rather than the whole box keeps a live message or a typewriter frame landing in the **same** commit out of the correction. The box opts out of the browser's own anchoring (`overflow-anchor: none`), so this is the only correction.
- **Scroll memory inside the prelude** (#1534; the live acceptance showed the earlier wording here — "keeps working because the prelude lives in the store" — false for a reader inside the prelude):
  - Every drawn prelude element carries the stable position it starts at, `data-prelude-pos` (its first entry's `pos`). A chat span also lists every message it draws in `data-prelude-poses` (space-separated), so a room row's `pos` can be found inside the span that holds it. The closing handoff marker carries none.
  - The memo records an **anchor** besides `scrollTop`, `atBottom` and `firstTurn`: the first element still on screen — a prelude element (by `pos`) or a turn (by `data-turn-index`) — and its offset from the box's top.
  - Same view: the restore puts the anchor back at its offset. A page that landed while the pane was unmounted therefore no longer shifts the reader. Raw `scrollTop` is the fallback when the anchor is not drawn.
  - Other view: a prelude anchor is brought to the top (heights differ), found by `data-prelude-pos` or inside a span's `data-prelude-poses`; a turn anchor keeps today's `firstTurn` rule.
  - At the bottom, nothing changes: the reader opens at the bottom.

### 5.5 Search (D7)

- `buildSearchUnits` walks the loaded prelude first, in drawing order, with namespaced ids and reveal keys. The room × chat anchor parity test is extended to cover it.
- While `!done`, the search bar shows 「更早的內容尚未全部載入 · 全部載入」 / "Earlier content not fully loaded · Load all". The action pages until done and keeps showing progress.

### 5.6 Unchanged

Reducer, seq guard, SSE order contract, `costSummary`, header cost and Q3 hover, the worker list and rollup, Hand to nex, Take to terminal.

## 6. Phases

| Phase | Repo | Content | Review |
|---|---|---|---|
| N | Nexen (nexen-85) | §4 in full → v0.16.0 | Nexen's own flow (plan + codex, R1 + R2) |
| P1 | Purdex SPA | §5.2 data layer, capability-gated, driven by a contract sample page now and by Nexen's early golden page (§4.6) as soon as it exists (no pin needed) | R1 + attacker + critic |
| P2 | Purdex SPA | §5.3 room rendering, markers and notes, §5.4 scroll | R1 + attacker + critic |
| P3 | Purdex SPA | chat rendering, §5.5 search | R1 (+ attacker if R1 finds P1+) |
| P4 | Purdex | §5.1 pin v0.16.0 + deploy + a replay test over a **real captured** `/prelude` page + live acceptance (§7) | R1 |

P1–P3 can proceed in parallel with N against this contract. P4 waits for v0.16.0. Each PR stays within 800 lines / 20 files; the plan splits further if needed.

## 7. Acceptance (mlab, live)

1. Open the existing handed-off worker `06GGP7CD…` (legacy fallback). All 218 interactive lines appear above the brief, with a 「在終端機」 marker at the top of that run. Slash commands, queued prompts and tool calls render. No `-p` line is duplicated.
2. Make a fresh handoff: interactive claude with at least one tool call, one `!` bash command, one slash command and one prompt typed while busy → Hand to nex → open the worker. Everything appears, and the boundary sits exactly above the brief.
3. Round trip: Take to terminal → a few interactive turns → Hand to nex again. The new worker shows interactive → headless → interactive with a marker at each switch.
4. Long transcript (> 1 MiB): the pane opens at the bottom, scrolling up loads older pages without the view jumping, and Cmd+F shows "Load all" until done.
5. Room ⇄ chat both render the prelude, and switching keeps the scroll position.
6. Rename or move the transcript file → the pane shows the D5 line, and the worker's own turns still render.
7. A NewTab worker shows no prelude and no extra request after the first `none`.

## 8. Risks

- **Transcript format drift.** The parser follows CLI-internal records. Nexen's classification table (§4.3) is the single place to adjust, and unknown line types are omitted rather than failing.
- **Privacy.** The prelude exposes the interactive session's content to anyone who can read the execution. That is the same access rule as `GET /v1/executions/{id}/events` (Nexen v1 has one trust domain and no per-execution ACL; `/events` already carries the worker's full conversation), and the content is not on any SSE stream (no site-wide strip needed).
- **Large files.** Backward reads are bounded per page, and the 2.2 MB lines are handled by the block caps. The legacy fallback scan is a one-off per execution.

## 9. Follow-ups (not in this spec)

- Subagent internals in the prelude (`subagents/agent-*.jsonl`, linked to the Task call).
- Prelude images via a blob route.
- Full text of externalized tool outputs (`tool-results/`).
- Following `continued-in` chains, if the terminal ever starts showing them.
- A worker taken to a terminal, chatted with there, then sent another turn (resuming the same file): those terminal turns lie after `prelude_end` and are not in the event log, so that worker does not show them. Acceptance 3 covers the supported path (a fresh handoff creates a new worker).
- A transcript replaced in place while still at least `prelude_end` long would be read silently. Not defended: the CLI only appends. Recorded as an honesty row in the Nexen contract.
