# Session workbook — plan

Spec: `docs/specs/2026-10-09-session-workbook-spec.md` **v2** (v1 approved 2026-10-09; v2 = the user's 2026-10-10
decisions, #2296 / #2298). Prompts: `docs/specs/2026-10-10-session-workbook-prompt-v2.md` (prompt_ver 2: turn, re-write,
refresh); the v1 document stays for the merged v1 constants. Owner: interface line (88). Daemon and mod PRs by the
interface line's second seat (Sonnet member); App PRs by solo. iOS (§10.3) is built in purdex-ios to 88's design.

**Revision 4 (2026-10-10): spec v2.** The `claude -p` runner (old WB-1b.3) is gone; the call runs in the session's mod
through a daemon job queue (spec §5.1). Merged so far: WB-1a-i #2269, WB-1a-ii #2273 / #2274, WB-1b.1 #2279 (setting),
WB-1b.2 #2280 (prompt v1 constants + six-field validation), WB-2 #2286 (API + events + `workbook.v1`). New sections:
WB-1b′-a/b/c, WB-1c, WB-2b-i/ii, WA-1 / WA-2b-1 / WA-2b-2 for v2; D3 / D9 rewritten for jobs; D10–D14 new.

Rules for every task: TDD (failing test first), one commit per task, mutation check before release, only the affected
packages; a full `go test ./...` / `vitest run --maxWorkers=3` only before merge, with a slot from purdex-1f. Review per
PR: codex R1 + R2 (attack → critic). Tests that touch tmux use `-L <label>` and `unset TMUX`. No secret, token or raw
transcript text in logs, tests or PR text.

Revision 2 (2026-10-09 23:xx): codex plan review `task-mv12whuc-yxlm33` (15 findings, all taken; the map is in §9).

## 0. Facts the plan stands on (main f459521a, re-read 2026-10-09)

- **Turn end.** `agent.TerminalSessions.SubscribeTurnEnd(fn func(TurnEndEvent)) (unsubscribe func())`
  (`internal/module/agent/terminal_sessions.go:55`; registry key `agent.TerminalSessionsKey`). `TurnEndEvent{SessionID,
  Text, At, Seq}` (`turn_end_hub.go:20-25`): `At` is **unix ms at the hook's arrival**, `Seq` a per-daemon arrival
  counter; `Text` ≤ 4096 bytes. At-least-once: a hook answered 500 (or whose answer was lost) is retried and published
  **again with a newer stamp** (`turn_end_hub.go:37-39`), so `(session, At, Seq)` does **not** identify a turn. Per
  subscriber a 64-slot queue; a full queue **drops** the event (`turn_end_hub.go:96-107`). Published only for an
  accepted `cc` `PdxStop` with `LifecycleStop` (`publishTurnEnd`, `turn_end_hub.go:145-166`) — **not** for
  `PdxStopFailure` (`LifecycleStopFailure`, `frame_ops.go:519`). Today's only consumer: team `onTurnEnd`
  (`last_turn.go:177-196`), which records `lastTurnSummary(Text)` and returns on an empty summary.
- **Turn identity.** `convmodel.Turn{ID, Index, StartedAt, EndedAt *int64, Outcome}` (`internal/convmodel/model.go:83`);
  `ID` is the id of the row that opened the turn — stable across re-reads of the same transcript; `Outcome` is
  `running | done | interrupted | failed` (`model.go:31-34`).
- **Conversation model.** No in-process accessor: the conversation module keeps `cache *convfeed.Cache` / `resolver`
  private (`internal/module/conversation/module.go:44-47`) and registers no service. Reads go `cache.Acquire(ctx, sid)` →
  refresh (`http.go:286`) → `(*Entry).Window(turns, before, budget)` (`internal/convfeed/window.go:29`). `convmodel` is
  import-guarded to stdlib (`imports_test.go`), so the accessor lives in the conversation module.
- **Lineage.** `session_lineage(session_id PK, predecessor_session_id, …)` (`internal/module/team/relay_store.go:54`).
  `ChainRoots()` scans the whole table (`relay_store_lineage.go:74`); no per-id `RootSessionOf`. Registry key
  `team.LineageReaderKey` (`internal/team/wire_relay.go:199`); key names are pinned by `wire_relay_contract_test.go:46`.
  The team module registers its services at `module.go:406-408`.
- **Team seat.** No per-session reader of team / role. X2a's single role gate `(*Store).SessionRole` (`remote_members_store.go:274`) answers lead / member_local / member_remote / none, without the team id. The roster is built by `(*Module).buildRoster()`
  (`internal/module/team/roster.go:54`). A peer ref is the pure `peers.RefID(sessionID)` (`internal/peers/ref.go:114`).
- **Push.** The Stop push comes from the notify hub (`agent.NotifyFeedKey`), not from `TurnEndEvent`, unordered against
  it. `onNotify` (`internal/module/push/agent_trigger.go:24-73`) → `gate.Decide` → `send()` = `snd.Enqueue(Job{Make})`.
  `AgentEvent.BroadcastTs` is **wall-clock nanoseconds** (`gate.go:24-30`). `holdSet.after` (`agent_trigger.go:95-116`)
  deletes a timer from the set when it fires, before its callback runs, so a callback that blocks is outside the cap
  and outside `stopAll`. Push `Stop` order: unsubscribe → `holds.stopAll()` → `sender.Stop()` → store close
  (`module.go:310-326`). Push `Dependencies()` = `team, agent` (`module.go:89`). Title today = `name` or
  `"<host>：<name>"` (`internal/push/agent_content.go:70-115`).
- **Mod socket (v2's transport).** `internal/modevents` serves the Unix socket (U1 spec §6.1): peer-uid gate on accept,
  no token, never on the TCP mux; its own `http.Server` with **`WriteTimeout 10 s` / `ReadTimeout 10 s`** — a 15 s
  long-poll answer would be cut, so the `next` route extends its own write deadline (`http.NewResponseController(w)
  .SetWriteDeadline`). Today: `POST /mod/v1/events` (wire v1, U1 spec §6.2: envelope `{v, stream, agent, cc_version,
  mod_version, dropped_total, cwd, interactive, events}`, answer `{ack}`; a 10 s heartbeat batch per stream) and
  `GET /mod/v1/team` (TI-5a). The reporter is `cmd/pdx/plugin/purdex/hooks/events.js` (the interface line's file; it
  owns the socket client); `register.js` is not touched (M-U1-2/3/5); a mod change ships with `pdx setup --agent cc`.
- **Mod model API** (Claude Code 2.1.294, spec §3): `$.model.complete({model, system, prompt, maxTokens, effort,
  timeoutMs}, {signal})` → `{isAnswered: true, text, usage}` / `{isAnswered: false, reason, status?, error?}` (reasons
  `api-error` / `empty-reply` / `aborted`; a refused request rejects); `$.model.fork({prompt})` same shape plus
  `nothing-to-fork`; work started from `$.clock.after` is not cut by a user interrupt.
- **Scrub.** `internal/convmodel/ccnorm/scrub` regexes (`scrub.go:387-419`: Bearer, `sk-`, `gh?_`, `xox?-`, AWS) are the
  right set; it names neither `pdxp_` nor `pdxd_`; importing it drags in the whole normaliser.
- **Host settings.** Pattern `relay_quota.go` (key, registry key, reader interface, strict `normalize*` / lenient
  `read*`, `emptyFor`, `handleGet` field, PUT route, `Init` registration); numeric `resources.go`; reader interface kept
  outside hostconfig (`internal/resources/settings.go:14-21`).
- **New module pattern.** devices / push: soft-fail `Init`, `Status()` for `moduleReady`, `devices.db` 0600 incl.
  `-wal` / `-shm` (`internal/module/devices/store.go:42-110`); capability in `capabilityList()`
  (`internal/core/info_handler.go:84-93`); modules are mounted in `registerServeModules` (`cmd/pdx/main.go:404`).
- **Host events.** `c.Events.Broadcast("", type, value)` with `value` a **JSON string** (profiles, `module.go:70`);
  iOS decodes `value` as a string and ignores unknown types (purdex-ios `Wire.swift:28-46`, `Fleet.swift`); the SPA
  ignores unknown types.
- **Merged workbook code** (main after #2286): `internal/module/workbook` — store (`InsertPending`, `SetPushLine`,
  `Finish`, `SetStatus`, `Conversation`, `Entries`, `RecentForPrompt`, `FailPending`, `NewestTurn`), store-level event
  hooks → `workbook.entry` / `workbook.status`, the API (`api.go`), the v1 prompt constants and six-field validation
  (`validate.go`), the `push_wait_s` setting; `internal/redact`; `conversation.TurnReader` (`LastTurns`, deep copies).
- **SPA.** `TeamPanel` (WA-2a) is merged. A Claude Code tab's per-tab buttons live in the bottom `StatusBar`
  (`components/status/PaneModeButtons.tsx`). Capability gating = a per-host probe of `/api/info`
  (`lib/team/unattended-support.ts:26-44`). Host events are routed in `hooks/useMultiHostEventWs.ts`. Resize: `RegionResize`,
  `useLayoutStore` clamps; device-local UI state in `useTeamUiStore` (never profile-synced).

## 1. Decisions this revision makes (implementation of the approved spec; no user-visible change)

- **D1 Turn identity = the transcript turn id.** An entry is keyed by `UNIQUE(session_id, turn_id)`, `turn_id` =
  `convmodel.Turn.ID`. A retried hook therefore inserts nothing. Transcript busy (`ErrBusy`) → the session mark is
  re-queued up to 3 times, 2 s apart, before the fallback. Transcript unreadable (not found / still busy) → fallback
  `turn_id = "t:" + hex(sha256(Text))[:16] + ":" + (At / 120000)`: a retry (seconds later, same `Text`) lands in the same
  2-minute bucket and dedupes, while two different turns with the same text (「完成」, an empty text) in different
  buckets stay apart. Accepted residue: a retry that crosses a bucket boundary records twice; two same-text turns inside
  one bucket record once.
- **D2 Catch-up instead of trusting every event.** Each turn-end event means "this session has new ended turns". The worker
  reads the session's last 6 turns, drops the `running` ones, and inserts, oldest first, the **ended** turns newer than
  the session's newest recorded turn — at most the newest 3 (the backlog limit; older ones are not recorded). When the
  newest recorded turn is not in the window, "newer" is the whole window, so the newest 3 ended turns are taken. A session
  with no record yet inserts only its newest ended turn (no history backfill). A dropped event is recovered by the next
  one; an event processed late after the next turn also ended records both, in order. The newest inserted turn gets
  `turn_at = event.At`; older caught-up turns get their own `EndedAt` (ms).
- **D9 Stop and restart leave no entry `pending`** *(v2: jobs)*. Workbook `Stop`: every leased job's entry →
  `failed: stopped` (a result that arrives later is dropped: its lease is gone), except an entry already past its push
  line whose re-write job was leased or queued → `Finish(ok)` with `entry` cut at the last sentence end ≤ 150; queued turn
  entries → `skipped: stopped`; a queued / leased refresh → `failed: stopped`; every push waiter is released (false for
  failed / skipped); each transition emits its event. On `Start`, rows still `pending` from a crash become
  `failed: stopped` in one UPDATE (no events: clients refetch on reconnect).
- **D3 Push-ready is separate from `ok`** *(v2: as jobs)*. An entry holds its conversation's queue until it is final: turn
  job → result → validate → **one transaction** writes `thing` + `push` + status + the todo changes (state still
  `pending`) → **release push waiters** → `entry` > 150 → a `rewrite` job for the same entry (same queue place; counts
  toward the hourly cap; at the cap → no re-write, cut) → `Finish(ok)`. The next job of that conversation is handed out
  only after `Finish`, so its input (`previous_status`, `recent_entries`, `open_todos`) never reads a provisional entry;
  the push never waits for the re-write; one `ok` event per entry.
- **D10 Jobs** (spec §5.1). A job = `{id, conv_key, entry_id, kind: turn | rewrite | refresh, attempt, input built at
  hand-out}` — the wire kinds are exactly the spec's three; a retry is the same kind with `attempt` 2, never a new kind.
  In memory (a restart re-derives nothing: D9 fails the rows). `next` hands out the head job of the asking session's
  conversation only when no job of that conversation is leased, and only to an **eligible** session: a `turn` /
  `rewrite` job (no transcript involved, the input is prebuilt) to any capable live session of the conversation,
  preferring the entry's own session; a `refresh` job to a `workbook.refresh`-capable session of the conversation,
  preferring the session it was asked for, else the one whose stream announced `workbook.refresh` most recently (D14) —
  any other session gets 204 for it. A lease lasts `timeout_ms` + 10 s (30 + 10 s); a lease that runs out → the entry `failed: lost` (not
  retried) and the queue moves on; a `result` whose job id is not leased (expired, unknown, twice, or from another
  stream) → 409 `not_leased`, ignored. `next` and `result` carry the mod's **`stream`** id (D11) besides `session_id`:
  one `next` long-poll per stream at a time (a second from the same stream waits for the first to return); a reloaded
  mod's new stream is a new poller; a lease is bound to the stream that took it. The outcome table of spec §5.1 maps the
  result; `empty-reply` / non-JSON → the same job again with `attempt` 2, then `failed: format`.
- **D11 Capability by envelope** (spec §5.1, §10.4 point 4). Wire v1 gains an optional envelope field `caps: [string]`
  (absent = none) — the mod sends `["workbook.v2"]` on every batch from WB-1c, and **`"workbook.refresh"` too from
  WB-2b-ii** (a mod that can run a `refresh` job); the 10 s heartbeat keeps it fresh; the registry keeps `caps` + the time
  per stream, and a session counts as capable (for turns: `workbook.v2`; for a refresh: `workbook.refresh`) when the
  stream whose current sid it is announced it within 30 s. So the daemon of WB-2b-i never hands a refresh to a mod of
  WB-1c (the refresh route answers 409 `not_live` until a `workbook.refresh` mod is live). A turn of an incapable session →
  `skipped: no_mod` at insert (no job). **Refresh availability** is per conversation: `available(conv)` = some session
  of the conversation (by `RootSessionOf`) is the current sid of a stream that announced `workbook.refresh` within 30 s.
  The workbook module owns `lastAvail map[conv_key]bool`; it recomputes the conversations touched by each envelope's
  stream and, in a 10 s sweep, every conversation in `lastAvail` or with a live stream; it emits
  `workbook.refresh_available {conv_key, available}` only when the computed value differs from `lastAvail` (then stores
  it), so repeated sweeps send nothing and one stream lapsing while another still serves the conversation sends nothing.
  The conversation answer carries `refresh_available` (computed on read) (WB-2b-i). The events answer gains
  `workbook: true` when the conversation of any of the stream's sessions has a job waiting that nobody leased (a refresh,
  or a left-over), so the mod asks `next` (b). Both are additive (an older daemon ignores `caps`; an older mod ignores
  `workbook`). The U1 spec §6.2 gets both lines in WB-1b′-c.
- **D12 Input at hand-out** (spec §5.2). The daemon builds the prompt JSON when it hands the job out (the previous output
  applied), with `open_todos` numbered `n = 1…` (oldest first, ≤ 30) and the job's `n → todo id` map kept on the lease;
  `dropped_titles: []` always; `redact.String` on every string; the system block = the prompt_ver 2 constant, marked
  `cache: true`.
- **D13 Todos** (spec §5.4–§5.5): applied through the job's map in the push-line transaction (D3); unknown `n` ignored;
  a todo no longer open stays; `done` wins over `dropped`; closings apply first, then the adds one by one — the first 2
  adds (10 for a refresh), title cut at 30, detail at the last sentence end ≤ 100 (none → 100), an add equal (trimmed) to
  an open title (including one added earlier in the same batch) ignored, and the 30-open cap re-checked before each add
  (29 open + 2 adds → 30; the rest ignored with one log line); `skip: true` → the entry `skipped: model` and its todo changes still applied. Each change records
  `added_entry_id` / `closed_entry_id` / `closed_by` (`model` | `refresh`).
- **D14 Refresh** (spec §5.6): `POST …/refresh` (Mac) or the mod's `/workbook refresh` → a `refresh` entry
  (`kind: refresh`, `pending`) and job at the queue's tail; only when the conversation has a `workbook.refresh`-capable
  live session (else 409 `not_live`); one at a time (409 `refresh_pending`). **Its row** (the deployed schema keeps
  `session_id` / `turn_id` NOT NULL and `UNIQUE(session_id, turn_id)`): `session_id` = the session it runs in — the
  `/workbook refresh` caller, or for the Mac route the capable session whose stream announced `workbook.refresh` most
  recently — and **re-pointed** (one UPDATE) to the session that actually leases the job (D10 lets another capable
  session of the conversation take it when the first one is gone); `turn_id = "r:" + <request unix ms> + "-" +
  <per-daemon seq>` (unique, so re-pointing never collides); `turn_at` = the request time, `turn_seq` 0. A refresh at the
  queue's head with no capable session for 40 s → `failed: lost` (the turns behind it wait at most that long). Its result: `{status, todos}` validated as D13 (≤ 10 adds, no push,
  no thing) → one transaction writes the status, the todo changes and the entry (`ok`, `thing` = the current thing,
  `entry` = 「重整：完成 a、移除 b、新增 c」) — no push hold involved.
- **D4 StopFailure gets a turn-end event.** `TurnEndEvent` gains `Failed bool`; `publishTurnEnd` also publishes an accepted
  main-turn `PdxStopFailure` with `Failed: true` (subagent StopFailures stay excluded). Team `onTurnEnd` ignores
  `Failed` events (today's behaviour kept). The workbook summarises a failed turn like any other.
- **D5 Holds are a cancellable, capped set of goroutines** in the push module (not `holdSet` timers): `Await` takes a
  context; `Stop` cancels and waits for every hold before stopping the sender; a cancelled hold never enqueues.
- **D6 Units and paging (§9 clarified).** Every time on the API and in events is **unix ms**. `before=<entry id>` returns
  entries with `id < before`, newest first (`ORDER BY id DESC`); `since` / `until` are ms on `turn_at`. Event values are
  JSON strings. `workbook.status` also carries `session_id` (the session whose turn produced it). Entries in state
  `skipped` are returned (the App hides them); `failed` carries `reason`.
- **D7 No `enabled` switch.** The only setting is the spec's `push_wait_s`.
- **D8 Redaction follows spec §8 literally**: any run of ≥ 32 letters and digits that mixes both is redacted — a 40-char
  hex sha is redacted too (harmless: the prompt drops shas anyway).

## WB-1a-i daemon (A-line files, PR shown to purdex-1f): team accessors and the failure turn-end

1. **`RootSessionOf`.** `relay_store_lineage.go`: per-id walk of `predecessor_session_id` with a `seen` guard (mirroring
   `rootOf`); a session absent from lineage is its own root. Narrow interface `team.LineageRootResolver
   { RootSessionOf(sessionID string) (string, error) }` in `internal/team`, registered on the same store under a new key
   `team.lineage-root` (contract test updated). Tests: no lineage → itself; a 3-hop chain → the root; a cycle (raw insert)
   → stops, returns the last unseen; DB error surfaces.
2. **`SeatOf`** (purdex-1f's requirement). `team.SeatReader { SeatOf(sessionID string) (Seat{TeamID, Role string},
   error) }` under a new key `team.seat-reader`, **built on X2a's single role gate** `(*Store).SessionRole`
   (`remote_members_store.go:274`, `sessionRoleIn` / `roleChecks`, merged 717dc40d) — no separate query over `teams` /
   `team_members` — plus the team id of the row that role came from: `lead` → `teams.id` of the live team it leads,
   `member_local` → `team_members.team_id`, `member_remote` → `remote_members.team_id` (the team id on the lead's
   host). `Role` on the wire is `lead | member | member_remote | none`. An ended member returns what the store holds now
   (the workbook keeps its own snapshot from insert time). Tests: lead, local member, remote member (its lead-host team
   id), solo (`none`), unknown session (`none`, nil), DB error. Measure one call on a copy of mlab's `team.db` and state
   it in the PR (it runs once per inserted entry, never inside a lock).
3. **Failure turn-end (D4).** `TurnEndEvent.Failed`; `publishTurnEnd` accepts `PdxStopFailure` + `LifecycleStopFailure`
   for a main turn; team `onTurnEnd` returns on `Failed`. Tests: a main StopFailure publishes `Failed: true`; a subagent
   StopFailure does not; team records nothing for a failed event; the existing Stop path is unchanged.

Size ~350 lines incl. tests.

## WB-1a-ii daemon: module, store, last-turn reader, redaction, mount

1. **Module + store.** `internal/module/workbook`: `New()`, `Name() "workbook"`, `Dependencies() {agent, team,
   conversation, hostconfig}`, soft-fail `Init` opening `<data>/workbook.db` (0600 incl. `-wal` / `-shm`, the devices
   helpers), `Status() {ready, init_error}`. Schema = spec §6 as clarified (§8 below: `turn_id`, `push_ready_at`), a
   `schema_version` row. Store API: `InsertPending(e) (id, inserted bool)` (idempotent on `(session_id, turn_id)`),
   `NewestTurn(sessionID) (turnID string, turnAt int64, ok bool)`, `SetPushLine(id, thing, push)` (sets
   `push_ready_at`; `push` may be `""` when validation dropped it), `Finish(id, state,
   reason, out)`, `SetStatus(conv, status, entryID, sessionID)`, `Conversation(conv, limit, beforeID)`, `Entries(since,
   until, thingDone, limit)`, `RecentForPrompt(conv, 3)` (only `ok` entries). Tests: idempotent insert (same turn id
   twice → one row); state transitions; `SetPushLine` keeps `pending`; ordering newest first by id; `beforeID` paging;
   file modes; a broken DB path → soft fail.
2. **Mount.** Add the module to `registerServeModules` (`cmd/pdx/main.go:404`); a test that the serve module list
   contains `workbook` and that its `Status()` is reachable through `moduleReady`.
3. **Last turns reader.** `internal/module/conversation`: `LastTurns(ctx, provider, sessionID, n) ([]convmodel.Turn,
   error)` = `Acquire` → refresh → `Window(n, -1, always-true)` → release, oldest first; interface
   `conversation.TurnReader` in a small exported package registered at `Init` under `conversation.turns`. Errors
   (`ErrNotFound`, `ErrBusy`, resolve) returned as is. Tests: a fixture transcript → the last 4 turns with ids, outcomes,
   user / steps / agent text; a running last turn is reported as `running`; not found; busy.
4. **Redaction.** New `internal/redact`: `String(s string) string` replacing Bearer tokens, `sk-…`, `gh[pousr]_…`,
   `xox[a-z]-…`, AWS key ids, `pdxp_…`, `pdxd_…`, and runs of ≥ 32 letters+digits that mix both (D8) with `[redacted]`.
   Table tests per pattern incl. a 40-char hex sha → redacted, a negative set (ordinary words, paths, a 31-char run, a
   pure-digit run), idempotence.

Size ~650 lines incl. tests.

## Merged on the v1 path (kept)

WB-1b.1 #2279 (`workbook.push_wait_s`), WB-1b.2 #2280 (v1 prompt constants, prompt files written at Start, six-field
validation + repair), WB-2 #2286 (v1 API, `workbook.entry` / `workbook.status` from store hooks, `workbook.v1`,
`deviceAllowed`). The v1 `claude -p` runner (old WB-1b.3) is dropped; the old WB-1b.4 subscriber / catch-up / queue
returns as WB-1b′-b with jobs instead of runner calls.

## WB-1b′-a daemon: migration v2, prompt v2, seven-field validation, input v2

1. **Migration** (spec §6 v2; `workbook.db` is deployed — `schema_version` 1 → 2 in one transaction, re-runnable):
   `wb_entries` + `kind TEXT NOT NULL DEFAULT 'turn'`, `usage_in`, `usage_out`, `usage_cache_read` (INTEGER NULL);
   `wb_todos` + `INDEX wb_todos_conv(conv_key, state, id)`. New reasons are values only (`failed: api | lost | refused |
   nothing_to_fork`, `skipped: no_mod`). Tests: a v1 file with rows migrates and keeps them; a v2 file is untouched; a
   half-done migration (forced failure) leaves version 1 and retries cleanly; file modes stay 0600.
2. **Todo store**: `OpenTodos(conv, 30)` (oldest first), `Todos(conv, state, limit, beforeID)`, `ApplyTodoChanges(tx,
   conv, entryID, done, dropped []int64, adds []TodoAdd, by string)` (D13; returns the changed rows for the event), and
   the push-line transaction `SetPushLineV2(entryID, out, todoChanges)` = thing + push + status + todo changes + usage in
   one transaction (D3). Tests: D13's rules one by one, incl. 29 open + 2 adds → 30 and a duplicate title inside one batch; the done record order; a todo closed by a refresh keeps
   `closed_by: refresh`.
3. **Prompt v2 constants**: turn, re-write, refresh — `PromptVersion = 2`; a test asserts each equals its fenced block in
   `docs/specs/2026-10-10-session-workbook-prompt-v2.md` byte for byte (the v1 test stays for the v1 file). Start writes
   `prompt-v2.txt` like v1.
4. **Validation v2**: the six-field validator gains `todos` (all seven required, spec §5.3); the refresh output
   `{status, todos}` has its own entry point (≤ 10 adds, no push / thing). `skip: true` keeps its todos (D13). Tests:
   each rule of spec §5.4 v2 with Chinese text; a missing `todos` → format path; the refresh shape.
5. **Input v2** (D12): `BuildTurnInput(entry, turn, conv)` → the prompt JSON of spec §5.2 + the `n → id` map; refresh
   input = the refresh prompt filled with status + numbered open todos + `[]`. Tests: numbering oldest first; 30 cap;
   `dropped_titles` always `[]`; redaction applied to every string; byte-stable output for a fixture turn.

Size ~750 lines incl. tests.

## WB-1b′-b daemon: subscriber, catch-up, queue, jobs (no transport yet)

1. **Subscriber + catch-up** — old WB-1b.2 / .3 as written (D1, D2), plus: the session not capable (D11) → the inserted
   turn is `skipped: no_mod` and gets no job. Tests as old WB-1b.2 / .3 + `no_mod`.
2. **Queue + jobs** (D3, D10): per conversation FIFO of entries, at most 3 waiting (older `skipped: backlog`); hourly
   cap 300 (`skipped: cap`, one log line per hour); **no host-wide concurrency limit** (spec §5.1: nothing is spawned).
   A `JobSource` interface — `Next(ctx, stream, sessionID, wait) (Job, bool)`, `Result(stream, jobID, Result) (more bool, err)` (the lease records its stream; a result from another stream → `ErrNotLeased`) — that
   WB-1b′-c puts on the socket; leases (40 s) with a clock seam; the outcome table; `retry` once; re-write as a job;
   `Finish`; D9 at Stop / Start. Tests with a fake clock and a fake mod: order within a conversation; the next job only
   after `Finish`; two conversations independent; a relay hands the job to the new session (same `conv_key`); lease
   expiry → `failed: lost` and the queue moves on; a late result → `not_leased`; each outcome row of spec §5.1; format →
   retry → `failed: format`; re-write path keeps the place; push waiters released at the push line, before the re-write;
   cap; backlog; Stop / Start (D9).

Size ~750 lines incl. tests.

## WB-1b′-c daemon: the mod socket routes and the events wire

1. `POST /mod/v1/workbook/next` `{stream, session_id, wait_ms}` (0–15 000; out of range, a bad stream id or session id →
   400) → 200 `{job}` / 204; the handler extends its write deadline to `wait_ms` + 5 s (the server's 10 s
   `WriteTimeout`, §0) and returns at once on the request context's end. `POST /mod/v1/workbook/result` `{stream, job_id,
   answered, text?, reason?, status?, error?, usage, latency_ms}` → 200 `{more}` / 409 `not_leased` (incl. a lease held by
   another stream) / 400. Same peer-uid gate; never on the TCP mux; body cap as the socket's.
2. Wire v1 additions (D11): envelope `caps` recorded per stream; answer `workbook: true`; the U1 spec §6.1 / §6.2 lines.
3. Tests: a 15 s long-poll answered at 14 s is delivered whole (the write deadline); 204 at the end of the wait; a client
   that goes away ends the wait; one long-poll per stream, and a reloaded mod (new stream, same session) polls alongside
   the old stream until it ends without either getting the same job; a result from another stream → 409; `caps`
   freshness (30 s) gates jobs; turn jobs go to any capable session of the conversation (old and new session of a relay
   both polling → one of them gets it once); the answer flag appears for a waiting refresh and not otherwise; an older mod
   (no `caps`) → `no_mod`; the TCP mux does not serve the routes.

Size ~500 lines incl. tests.

## WB-1c mod: the job executor

In `cmd/pdx/plugin/purdex/hooks/` — a `workbook.js` module required by `events.js` (which owns the socket client);
`register.js` untouched. Plan-authoring per the `plugin-authoring` skill.
1. Announce `caps: ["workbook.v2"]` on every events batch.
2. Ask `next` (spec §5.1 when-to-ask): after a main-thread `turn.complete` with reason `answer` or `error` (wait 15 000);
   when an events answer has `workbook: true`; after a result answered `more`. Always from a `$.clock.after(0)` timer,
   never inside a hook; one job at a time (a flag; a second trigger while busy only marks "ask again after").
3. Run: `turn` / `rewrite` → `$.model.complete` with the job's `complete` (keys mapped to the API's:
   `max_tokens` → `maxTokens`, `timeout_ms` → `timeoutMs`, `cache` kept); a refused call (rejection) → result
   `{answered: false, reason: "refused"}`; post the result with `stream`, `usage` and `latency_ms`; on a socket failure
   drop the job (the lease runs out → `failed: lost`). A `refresh` job never reaches this mod (it does not announce
   `workbook.refresh`, D11); if one did, it answers `refused`.
4. Tests in the mod's test harness (the one `events.js` uses): the trigger set (subagent `turn.complete` ignored); one at a
   time; the key mapping; each outcome mapped; nothing runs inside a hook; the caps field on every batch.
5. **Real-session gate** (before merge): one disposable tmux session (`-L`, `unset TMUX`) with the built mod: a turn → an
   `ok` entry with push / entry / status / todos; screenshot of `GET /api/workbook/...` (redacted ids). Deploy needs
   `pdx setup --agent cc` (1f, settings.json compared before / after).

Size ~450 lines incl. tests.

## WB-2b-i daemon: v2 API and events, refresh route, `workbook.v2`

1. Entries gain `kind`, `usage {in, out, cache_read}`, `todo_changes {added, done, dropped: [{id, title}]}` (from the
   todo rows by entry id); the conversation answer gains `todos {open, done (newest 20)}`; `GET …/todos?state=&limit=&
   before=`; `POST …/refresh` → 202 `{entry_id}` / 409 `not_live` / 409 `refresh_pending` (D14: the refresh row's
   `session_id` / `turn_id`, re-pointing at lease, 40 s head timeout); **the socket twin** `POST
   /mod/v1/workbook/refresh {stream, session_id}` on the mod socket (same handler, the caller's session preferred) for
   the mod's `/workbook refresh` (WB-2b-ii only calls it); the conversation answer gains `refresh_available` and host event
   `workbook.refresh_available {conv_key, available}` on a change (D11); host event `workbook.todos` `{conv_key,
   session_id, todos}` from the store hook that applies todo changes; capability `workbook.v2` when the module is ready;
   `deviceAllowed` gains the todos GET (not the refresh).
2. Tests: shapes; paging by todo id; refresh 202 / 409s on both routes; two refresh rows of one session never collide
   (`r:` ids); a session with only `workbook.v2` (a WB-1c mod) → 409 `not_live`; the asked session gone → another capable
   session of the conversation leases the refresh and the row is re-pointed; no capable session for 40 s at the head →
   `failed: lost` and the next turn job goes out; `refresh_available` per conversation: one stream appears → one event;
   two streams, one lapses while the other is fresh → no event; both lapse → one event; ten sweeps in a row → no
   duplicate event; the todos event per change; device scope pinned list (the refresh routes are not device routes).
Size ~550 lines.

## WB-2b-ii mod: refresh

`workbook.js`: announce `"workbook.refresh"` in `caps` (D11); a `refresh` job → `$.model.fork({prompt})`
(`nothing-to-fork` → result reason, daemon maps it); the
`/workbook refresh` slash command (registered from `events.js` / `workbook.js`, not `register.js`) asks the daemon through
the socket (`POST /mod/v1/workbook/refresh {stream, session_id}`, the daemon route added in WB-2b-i) and then
asks `next`. Tests: fork mapping; the command's three answers (202 / not_live / pending) as a one-line notice in the
session. **M5** (below) before merge.
Size ~300 lines.

## Measurements before the related merges (spec §13)

- **M4** (gates WB-3): after WB-1c is deployed, 20 real summarised turns on two live sessions — Stop → push line within
  8 s for ≥ 70%. Below → split the turn job into A (`thing`, `push`, `entry`, `status`) and B (`todos`) as spec §13
  says, and the prompt document gains the two prompts (88 asks 1f / the user first: it changes the measured prompt).
- **M5** (gates WB-2b-ii): the refresh prompt on two real conversations — parseable, `status` ≤ 200, sensible todo
  changes; usage with cache read vs uncached recorded in the PR.
- **M6** (recorded with M4): `usage.cache_read` > 0 on a second turn job within 5 minutes, or "below the cache minimum".

## WB-3 daemon: push hold and body

1. **Waiter.** `workbook.PushLines` (registry key `workbook.push-lines`): `Await(ctx, sessionID string, sinceMs int64,
   deadline time.Time) (Line, bool)` with `Line{Thing, Push, ConvKey, EntryID}`; returns as soon as an entry of that
   session with `turn_at ≥ sinceMs − 2000` gets its push line (D3), `false` at once when that entry reaches failed /
   skipped or has no push, `false` at the deadline or on `ctx.Done()`. It first checks the store (`push_ready_at > 0`:
   the line may already be there when the hold starts), then waits on a per-session waiter list notified by
   `SetPushLine` / `Finish` (no polling); the check and the registration happen under the same mutex as the notify, so
   no line is missed in between. Tests: line already written before `Await` → returns at once; ready before deadline;
   failed → false at once; deadline; ctx cancel →
   returns at once; an older turn's entry does not satisfy a newer Stop; a caught-up older turn (turn_at = its EndedAt)
   does not satisfy it either.
2. **Hold (D5).** In `onNotify`, after `gate.Decide` accepted a `Stop` or `StopFailure`, `push_wait_s > 0`, and the
   registry yields `workbook.push-lines` **at this moment** (looked up per decision, so start order does not matter; no
   new dependency): start a hold goroutine in a `wbHolds` set `{mu, stopped, n, ctx, cancel, wg}` with cap 256. Admission
   and Stop are linearised by `mu`: admission = under `mu`, if `stopped` → refuse (the caller sends at once onto the
   sender, which is harmless after Stop, as today), if `n == 256` → refuse (send at once), else `n++`, `wg.Add(1)`, start
   the goroutine; Stop = under `mu`, `stopped = true`, `cancel()`; then `wg.Wait()` outside `mu`. So no `Add` can follow
   the `Wait`, and no hold admitted before Stop outlives it. The hold calls `Await(ctx, sessionID, BroadcastTs/1e6,
   now + push_wait_s)` — **ns → ms** — then enqueues the job with the line (or without it) only if `ctx` is not
   cancelled; on exit `n--` under `mu`, `wg.Done()`. Push `Stop`: unsubscribe → `wbHolds.stop()` → `holds.stopAll()` →
   `sender.Stop()`. Tests: a real ns `BroadcastTs` matches an entry whose `turn_at` is the same instant in ms; 256
   outstanding holds, the 257th sends at once; `Stop` with 256 outstanding holds returns in < 200 ms and enqueues nothing;
   `onNotify` racing `Stop` (`-race`, 1,000 iterations) never panics and never enqueues from a hold after `Stop` returned;
   no workbook in the registry → no hold; workbook registered after push `Start` → holds work.
3. **Body and title.** `push.AgentInput` gains an optional `Workbook *WorkbookLine`. When present: body = `Push`, title =
   today's title with `・{Thing}` appended (`"<host>：<name>・<thing>"` or `"<name>・<thing>"`), cut to the title limit by
   cutting `thing` first; payload `purdex.workbook = {"conv_key": …, "entry_id": …}`. Absent → today's title and body
   (also every Notification / permission push, spec §7). Tests: payload shape with and without; title cutting keeps the
   session name; a present Mac still suppresses (gate rule 4 runs first).

Note for 1f (user-visible, decided here unless the user objects): the spec's title `{session name}・{thing}` keeps today's
`<host>：` prefix when the push carries one, so multi-host titles stay distinguishable.

Size ~500 lines.

## WA-2a SPA: panel area + team view (the team part first; replaces TI-4)

Lands right after TI-3, **without** the workbook (so the team interface finishes first); the workbook parts come in
WA-1 / WA-2b behind `workbook.v1`.

1. **Panel area.** One shell-mounted panel. State in `useTeamUiStore` (device-local, never synced): `panel: {width,
   expanded}` (one pair for the area), `teamDrill: Record<teamKey, {hostId, sessionId}>`, `workbookTabs: Record<tabId,
   true>` (the last two unused until WA-2b, but the selector is final). One pure selector `panelView(activeTabId)`
   implements TI spec §4.4: the active tab's toggle on → `{kind:'workbook', from:'tab'}`; else the active tab's team →
   `teamDrill[teamKey]` ? `{kind:'workbook', from:'team'}` : `{kind:'team'}`; else `null`. Resize by a `RegionResize`
   edge with draft-then-commit (the `ActivityBarWide` pattern), width clamped 280–720, `expanded` = most of the content
   area, a header control toggles it.
2. **Team view** = TI-4's full / one-line panel (TI plan TI-4 bullets and tests), full and one-line taking the area's
   width; row / cell click = `openTeamSeat` (no workbook yet).
3. **Tab-hosted rule**: panel state survives a reload and a tab switch (real `TabContent` test).
Tests (beyond TI-4's): `panelView` table (toggle beats team; team tab with / without a drill; non-team tab → null;
switch away and back → same view); resize clamps and persists; expanded toggles. Screenshot gate (zh-TW, dark): full,
one-line, expanded, narrow, small and large team.
Size ~650 lines.

## WA-1 SPA: workbook data layer

1. `lib/workbook/types.ts`, `api.ts` (`fetchConversation(hostId, provider, sessionId, {limit, before})`, `fetchEntries`),
   strict parsers (times in ms; drop a malformed entry whole, warn once).
2. `stores/useWorkbookStore.ts`: `byHost[hostId].byConv[convKey] = {status, statusAt, entries (newest first, deduped by
   id), oldestId, loading, missing}` + `convOfSession[hostId][sessionId] → convKey`; not persisted.
3. **Loading rules** (codex #9): fetches happen only on (a) a seat's first appearance in the team view and (b) a host
   reconnect (connection key change) — `fetchConversation(limit: 1)` once per seat per connection generation, on a
   `workbook.v1` host — and (c) opening a workbook view (drill, toolbar, ended list): `limit: 20`, then 「更多」 with
   `before = oldestId`. A 404 marks the session `missing` for that generation. **Events never trigger a fetch**: a
   `workbook.entry` carries the entry, so it upserts by `entry.id` and learns `session_id → conv_key` (clearing
   `missing`); `workbook.status` carries `session_id`, so it lands even after a reload; a status for a `conv_key` no seat
   maps to yet is kept and shown once one does. (d) host removal / re-point forgets the host (the `roster-forget.ts` hook).
   So the fetch count is bounded by seats × connection generations + views opened.
4. Events added to `lib/host-events.ts`, routed in `useMultiHostEventWs.ts` with the connection-key guard.
5. Capabilities `workbook.v1` and `workbook.v2` probed with the unattended probe (sibling flags sharing its generation
   guard); `selectWorkbookSupport(hostId) → {v1, v2}`.
6. **v2** (spec §9 v2): entry `kind`, `usage`, `todo_changes`; the conversation's `todos {open, done}`;
   `fetchTodos(hostId, sessionId, {state, limit, before})` for the done record's paging; `workbook.todos` events upsert
   todos by id; `postRefresh(hostId, sessionId)` → 202 `{entry_id}` / 409 `not_live` / 409 `refresh_pending` (returned as
   typed results, not thrown); the store keeps `byConv[...].todos {open (oldest first), done (newest first), doneOldestId}`
   and `refreshAvailable` (from the conversation answer and `workbook.refresh_available` events). **Refresh pending is
   derived, not stored**: true while the conversation holds an entry with `kind: refresh` and `state: pending` (the 202's
   `entry_id` is upserted as such at once), so it ends with that entry's terminal event or the next refetch, and a
   reconnect cannot leave it stuck.
7. **Load to an entry** (codex v4 #8–#9): `loadUntil(hostId, convKey, entryId)` pages `before=` from the oldest loaded
   entry until the entry is loaded, at most 5 pages (100 entries); not found → `false` (the caller shows 「找不到這筆紀錄」
   and stays at the top).
Tests: parsers (ms units, v2 fields optional on a v1 daemon); upsert / dedupe / ordering; first-appearance fetch once per
seat; reconnect refetch once per seat; 404 → missing → an entry event fills it **without** a fetch (fetch count unchanged
after 50 entry events); status event after a reload maps through `session_id`; todos event moves an item open → done;
refresh results typed; refresh pending true after the 202 and false after the entry's `ok` / `failed` event and after a
refetch that finds it terminal; `refreshAvailable` follows its event; `loadUntil` finds an entry on page 3, gives up
after 5; host forget; capability off → no fetch at all.
Size ~650 lines.

## WA-2b-1 SPA: team panel rows to the prototype, task line, drill-in, ended list, toolbar

0. **Rows to the prototype** (user 2026-10-10, deferred to here): line 1 = subagent dots → bot + light → host chip →
   title → lead / 未開 → **the context ring at the right end** (model shape inside, no percent); line 2 = the task line
   only; model, effort and context % move to the row's tooltip. TI spec §4.4's line-2 wording is amended in the same PR.
1. **Task line** (TI spec §4.4 line 3 → now line 2): first sentence of the latest `status`, ellipsis, hover = whole
   status; none without a workbook or `workbook.v1` (the row is then one line).
2. **Click** (TI spec §4.4 amended): on a `workbook.v1` host a full row → `teamDrill[teamKey] = seat`, the row's bot icon →
   `openTeamSeat`; one-line cells and non-`workbook.v1` hosts keep `openTeamSeat`.
3. **Ended list** (TI spec §4.4 amended, codex #8): `endedSeats: Record<teamKey, {hostId, sessionId, title, endedAt}[]>`
   in `useTeamUiStore` — a seat that leaves the team's roster on a frame from its connected host is recorded (newest
   first, cap 20, pruned with the team); full mode shows a collapsed 「已結束 (N)」 group at the bottom; a click drills
   into that seat's workbook. A drilled seat that ends keeps its view until back.
4. **Toolbar entry**: a `Notebook` toggle in the `StatusBar` per-tab buttons for a `cc` tmux-session pane on a
   `workbook.v1` host whose session has a workbook (`convOfSession` known, or a `limit: 1` probe returning 200) →
   `workbookTabs[tabId] = true`; toggling again deletes it; closing the tab prunes it. The workbook view itself is a
   placeholder until WA-2b-2 (status + the latest entries, plain).
Tests: rows to the prototype (ring position, no percent, tooltip carries model / effort / %); task line present / absent;
row click drills, bot icon opens the tab, back returns; non-`workbook.v1` row click opens the tab; ended seat recorded
once, listed, drill works, pruned with the team, cap 20; toolbar toggle on a team tab (back to team view) and a plain
tab (closes); tab close prunes.
Screenshot gate (zh-TW, dark): team view with task lines and an ended group next to the prototype's p01.
Size ~650 lines.

## WA-2b-2 SPA: the workbook view (v1 + v2)

1. 「目前狀況」 + time; v2: 「待辦」 (open todos, title, detail on hover / click, read-only, count in the header,
   hidden when none).
2. 「紀錄」: entries grouped by `thing` (most recent thing first; 進行中 / 完成; finished groups collapsed); under each
   entry its todo changes 「＋ title」 / 「✓ title」 / 「－ title」; a refresh entry reads 「重整：完成 a、移除 b、新增 c」;
   `pending` 「整理中…」, `failed` 「整理失敗」 + reason on hover, `skipped` hidden; 「更多」 pages.
3. v2: a 「紀錄｜待辦」 switch in the header — 「待辦」 = open todos, then 「已完成」 (done record, newest first: time,
   title, the closing entry's thing or 「重整」; a click switches to 「紀錄」 and `loadUntil(closed_entry_id)` → scroll and
   highlight, or 「找不到這筆紀錄」).
4. v2: 「重整」 (`ArrowsClockwise`) in the header, enabled iff `refreshAvailable` (WA-1.6; disabled tooltip 「這段對話目前
   沒有可重整的 session」); 「重整中…」 while refresh pending is derived true (WA-1.6); a 409 `not_live` refetches the
   conversation (the flag corrects itself), `refresh_pending` shows 「重整中…」; tooltip when enabled:
   「用主模型讀完整段對話重整狀況與待辦（會用較多 token）」.
5. Back control when `from:'team'`. Tab-hosted rule: view (紀錄 / 待辦) and scroll per `convKey` in module-level memos
   (`transcript-scroll-memory.ts` pattern); a real `TabContent` switch-away-and-back test.
Tests: grouping and collapse; todo change lines; refresh entry text; the switch and the done record's jump into 紀錄
(loaded, on a later page, not found); refresh enabled / disabled / pending → done; v1 daemon → no 待辦, no switch, no 重整;
scroll and view restored after a real tab switch.
Screenshot gate (zh-TW, dark): 紀錄 with todo lines and a refresh entry, 待辦 + 已完成, 重整中…, expanded mode.
Size ~700 lines.

## iOS (purdex-ios, from spec §10.3)

Data layer done (#61, follows D6); v1 workbook section in the Tab-info sheet (0.6.30). **v2 detail page — 88's design,
sent 2026-10-10:** ⓘ and a left swipe push a full-screen page; nav title = the session (a ▾ switch for a tab with several
sessions); a 「資訊」 button opens the old Tab-info content (a host without `workbook.v1` shows that content as the page
itself); a pinned 「目前狀況」 card; a segmented 「紀錄｜待辦｜已完成」 (default 紀錄, remembered on the device); 紀錄
grouped as on the Mac with todo change lines and refresh entries; 待辦 title + detail (two lines, tap to expand), read-only;
已完成 newest first, a tap jumps into 紀錄; a workbook push opens 紀錄 at the entry, highlighted; no refresh; v2 parts
behind `workbook.v2` on fixtures until WB-2b-i deploys. Shipped in purdex-ios 0.6.32 (#68). **Paging rules** (codex v4
#8): 紀錄 and 已完成 page with `before=` (entry id / todo id); a push or a done item pointing at an entry not loaded pages
back at most 5 pages (100 entries), then shows 「找不到這筆紀錄」 at the top. **iOS acceptance** (88's screenshot gate,
then on a real device after WB-2b-i): the seven screenshots of #68 redone with real data; a push opened from the lock
screen lands on its entry; a done item older than the first page is found.

## 7. Order

Daemon + mod (second seat): WB-1b′-a → WB-1b′-b → WB-1b′-c → WB-1c (+ M4 / M6) → WB-3 → WB-2b-i → WB-2b-ii (+ M5).
App (solo): TI-7 → X5-App → WA-1 → WA-2b-1 → WA-2b-2.
Deploy: WB-1b′-* are inert until WB-1c (no mod announces `workbook.v2`, so every turn is `skipped: no_mod`); WB-1c needs
`pdx setup`; WA-1 / WA-2b need WB-2 (v1) and WB-2b-i (v2) deployed for real data.

## 8. Spec notes

**Revision 4 against spec v2** (no spec edit in this PR; recorded here for the review):
- `/workbook refresh` moves from WB-1c to WB-2b-ii: it needs the refresh job and a socket route that only exist there
  (spec §14 lists it under WB-1c).
- The wire of spec §5.1 / §10.4 point 4 is fixed by D11 (`caps` on the envelope, `workbook: true` on the answer); the
  U1 spec §6.1 / §6.2 lines land with WB-1b′-c.
- The socket's 10 s `WriteTimeout` vs the 15 s long-poll (§0) is handled per request (WB-1b′-c.1).
- A result for a job that is not leased → 409 `not_leased` (spec §5.1 is silent).
- The TI spec §4.4 line-2 wording (model / effort / context) is amended to the prototype in WA-2b-1 (the user deferred
  it to the task line, 2026-10-10).
- From the v4 plan review (`task-mv18hq0h-fz5f1l`): a second mod capability `workbook.refresh` (a refresh goes only to a
  mod that can run it, so WB-2b-i may deploy before WB-2b-ii); the refresh row's `session_id` / `turn_id = "r:…"`
  (spec §6's NOT NULL / UNIQUE kept); `refresh_available` on the conversation answer + host event
  `workbook.refresh_available` (the Mac needs it to enable 「重整」, spec §10.1); `stream` on `next` / `result`; turn
  jobs may go to any capable session of the conversation, a refresh to any `workbook.refresh`-capable session of the
  conversation, preferring the session it was asked for (rev 4.2, D10 / D14 — rev 4.1 had bound it to that one
  session); wire job kinds stay
  `turn | rewrite | refresh` (a retry is `attempt` 2).

Earlier revisions:

`docs/specs/2026-10-09-session-workbook-spec.md`: §4.2 (turn id identity, catch-up, StopFailure through `Failed`
turn-end events), §5.4 (the push line is final before the re-write; the entry turns `ok` after it), §6 (`turn_id`,
`UNIQUE(session_id, turn_id)`, `push_ready_at`, times in ms), §7 (holds are cancellable; StopFailure covered by D4), §9
(ms, `before` = id cursor, JSON-string values, `session_id` on `workbook.status`, skipped returned).
`docs/specs/2026-10-09-team-interface-spec.md`: the panel-area amendment (§4.4 display rule, row click / bot icon, ended
list), and from the TI-3 screenshot gate: §4.3 label capsule above the lead row in the left list, the P9 collapse line on
a rounded plate, the tick stays muted (§4.1's "the tick's team accents" removed).
`docs/plans/2026-10-09-team-interface-plan.md`: TI-4 points to WA-2a.

## 9. Review map (codex plan review, 15 findings)

1 retry idempotency → D1, WB-1b.3 · 2 ns vs ms → WB-3.2 · 3 hold cancel / cap → D5, WB-3.1–2 · 4 re-write vs order vs
push → D3, WB-1b.4 / .8, WB-2.2 · 5 lost events → D2, WB-1b.3 · 6 StopFailure → D4, WB-1a-i.3 · 7 team snapshot →
WB-1a-i.2, WB-1b.3 · 8 ended members → WA-2b.3, TI spec §4.4 · 9 App loading → WA-1.3 · 10 sha → D8, WB-1a-ii.4 ·
11 mount → WB-1a-ii.2 · 12 push dependency → WB-3.2 (per-decision lookup) · 13 envelope / isolation → WB-1b.6 ·
14 `enabled` → D7 · 15 prompt file → WB-1b.7.

Incremental re-review `task-mv141hip-0501fg` (no critical; 10 resolved, 5 partial + 5 new important, all taken):
catch-up window (cursor outside the window, running turns) → D2, WB-1b.3 · same-text fallback ids → D1 (2-minute
bucket, busy re-queue) · hold admission vs Stop → WB-3.2 (`mu` + `stopped`) · Stop / restart terminal states → D9,
WB-1b.4, spec §6 reason `stopped` · event-triggered refetch loop → WA-1.3 (events never fetch).
