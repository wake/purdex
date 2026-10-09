# Session workbook — plan

Spec: `docs/specs/2026-10-09-session-workbook-spec.md` (approved 2026-10-09; §4.2, §5.4, §6, §7, §9 clarified in this PR,
see §8 below). Prompt: `docs/specs/2026-10-09-session-workbook-prompt.md` (prompt_ver 1). Owner: interface line (88).
Daemon PRs by the interface line's second seat (Sonnet member); App PRs by solo after TI-3. iOS (§10.3) is built in
purdex-ios against §9 / §7 of the spec (its data layer, purdex-ios #61, already follows §9 as clarified here).

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
- **Runner output.** `claude -p --output-format json` prints one envelope (1f's measurement,
  `workbook-exp/lat-1.json`): `{"type":"result","subtype":"success","is_error":false,"api_error_status":null,
  "terminal_reason":"completed","result":"<the model's text>",…}`; the model's JSON is the **string** `result`.
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
- **Runner env.** No in-process `claude -p` runner exists. Minimal-env pattern `internal/peers/proxyhelper/client.go:69-72`
  (explicit `cmd.Env`, `Setpgid`, `WaitDelay`). `internal/claudeenv` keeps `CLAUDE_CODE_PLUGIN_DIRS` on purpose, so the
  runner builds its own environment.
- **SPA.** No `TeamPanel` yet. A Claude Code tab's per-tab buttons live in the bottom `StatusBar`
  (`components/status/PaneModeButtons.tsx`). Capability gating = a per-host probe of `/api/info`
  (`lib/team/unattended-support.ts:26-44`). Host events are routed in `hooks/useMultiHostEventWs.ts`. Resize: `RegionResize`,
  `useLayoutStore` clamps; device-local UI state in `useTeamUiStore` (never profile-synced).

## 1. Decisions this revision makes (implementation of the approved spec; no user-visible change)

- **D1 Turn identity = the transcript turn id.** An entry is keyed by `UNIQUE(session_id, turn_id)`, `turn_id` =
  `convmodel.Turn.ID`. When the transcript cannot be read, `turn_id = "t:" + hex(sha256(Text))[:16]` (a retry carries the
  same `Text`). A retried hook therefore inserts nothing.
- **D2 Catch-up instead of trusting every event.** Each turn-end event means "this session has new ended turns". The worker
  reads the session's last 4 turns and inserts, oldest first, every **ended** turn (`Outcome != running`) newer than the
  session's newest recorded turn, at most 3 (the backlog limit). A session with no record yet inserts only its newest
  ended turn (no history backfill). A dropped event is recovered by the next one; an event processed late after the next
  turn also ended records both, in order. The newest inserted turn gets `turn_at = event.At`; older caught-up turns get
  their own `EndedAt` (ms).
- **D3 Push-ready is separate from `ok`.** One job per entry holds the conversation's slot until the entry is final:
  call → validate → write `thing` + `push` + status (state still `pending`) → **release push waiters** → re-write if
  `entry` > 150 (same slot, counts toward cap and concurrency; at the cap → no re-write, cut) → `Finish(ok)`. The next turn
  of that conversation starts only after `Finish`, so `RecentForPrompt` never sees a provisional entry; the push never
  waits for the re-write; there is one `ok` event per entry.
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

## WB-1b daemon: subscriber, catch-up, queue, runner, validation

1. **Setting.** hostconfig key `workbook` `{"push_wait_s": 8}`; strict normalize (int 0–30), lenient read, `emptyFor`,
   `handleGet` field `workbook`, PUT `/api/hostconfig/workbook`; reader `workbook.SettingsReader { WorkbookSettings()
   (Settings, error) }` in a small package outside hostconfig, registered at hostconfig `Init`. No `enabled` (D7). Tests
   per the `relay_quota` / `resources` suites.
2. **Subscriber.** `Start` asserts `agent.TerminalSessionsKey` → `SubscribeTurnEnd`; the callback only enqueues an
   in-memory "session has news" mark (coalesced per session, never blocks). Stop: unsubscribe, then a barrier like team's
   `turnEndMu`. Tests: the callback never blocks with a stuck worker (1,000 events in < 50 ms); Stop after unsubscribe
   writes nothing.
3. **Catch-up (D1, D2).** For a marked session: `RootSessionOf` → `LastTurns(4)` → the ended turns newer than
   `NewestTurn(session)` (match by turn id; when the newest recorded id is not among the 4, take only the newest ended
   turn) → `InsertPending` each, oldest first, with `team_id / role` from `SeatOf` and `ref = peers.RefID(session)`;
   transcript unreadable → one entry keyed `t:<sha>` from the event `Text` (no tools). A turn with no agent text and no
   step → `skipped: no_text` without a call (spec §4.3). Tests: a retried event (same turn, newer stamp) inserts
   nothing; a dropped event is recovered by the next one (two entries, in order); an event handled after the next turn
   ended inserts both; a running last turn is not inserted; a new session inserts only its newest ended turn; the team
   snapshot is stored and survives the member ending; the unreadable path dedupes a retry.
4. **Queue.** Per conversation FIFO of entry jobs, at most 3 waiting (older → `skipped: backlog`); host-wide at most 2
   running calls (a re-write counts); hourly cap 300 (`skipped: cap`, one log line per hour). A job holds its
   conversation until `Finish` (D3). Tests with a fake runner: order within a conversation; two conversations in
   parallel; the next turn does not start before the previous entry's re-write finished; backlog trimming; cap; Stop
   drains without starting new calls.
5. **Input.** Spec §5.2's JSON (user ≤ 1500, assistant ≤ 3000, tools ≤ 20 as `"<Tool>: <summary>"`), `redact.String` on
   every string, `previous_status` + `RecentForPrompt`.
6. **Runner.** `exec.LookPath("claude")` on the daemon's `PATH`; argv exactly spec §3 + `--system-prompt-file
   <data>/workbook/prompt-v1.txt`; stdin = the JSON; cwd = an empty `<data>/workbook/run/`; `cmd.Env` = exactly
   `HOME, USER, LOGNAME, TMPDIR, LANG, PATH` from the daemon's env; `Setpgid`, a 30 s context, `WaitDelay` 2 s, the
   process group killed on timeout. **Envelope**: parse stdout as the `type:"result"` envelope; `is_error: true` or
   `subtype != "success"` → `failed: auth` when `api_error_status` is 401/403 or the text mentions login/OAuth/auth
   (one log line, `reason auth`), else `failed: exit`; then the string `result` → model JSON (§5.4). Exit ≠ 0 with no
   envelope → `failed: exit`; timeout → `failed: timeout`. `latency_ms` stored. Tests: a fake `claude` on a temp `PATH`
   that records argv / env / cwd and prints a fixture envelope — fixtures: 1f's real `lat-1.json` with `session_id` /
   `uuid` replaced (ok), `is_error` with 401 (auth), `is_error` other (exit), not JSON (format path), exit 1, sleep past
   the timeout. Assert argv equals spec §3 + the prompt file; env equals the allowlist exactly (a sentinel `WB_SECRET=1`,
   `CLAUDE_CODE_PLUGIN_DIRS`, `PDX_TOKEN` in the test's env are absent); cwd is the empty run dir.
7. **Prompt.** `prompt.go`: the system prompt and the re-write prompt as constants, `PromptVersion = 1`; a test asserts each
   equals its fenced `text` block in `docs/specs/2026-10-09-session-workbook-prompt.md` byte for byte. `Start` writes
   `prompt-v1.txt` (0600, overwriting a stale file); a test reads the written file back byte for byte and checks the mode.
8. **Validation + repair** (spec §5.4, D3): strip one code fence → JSON; non-JSON → one retry → `failed: format`; `push` >
   40 cut at the last punctuation ≤ 40, none → dropped; `thing` > 16 → cut; `status` > 200 → cut at the last sentence end
   ≤ 200; `skip=true` → `skipped: model`. Then `SetPushLine` + `SetStatus` and the push waiters are released; then `entry`
   > 150 → one re-write (the re-write prompt) → still > 150 → cut at the last sentence end ≤ 150; then `Finish(ok)`.
   `redact.String` on every output field. Tests: each repair rule with Chinese text; push waiters released before the
   re-write starts; one `ok` transition per entry; redaction of an output that echoes a token.

Size ~800 lines incl. tests (split 1b-i setting + subscriber + catch-up + queue / 1b-ii runner + validation if over).

## WB-2 daemon: API, events, capability

1. **Routes** (spec §9 + D6): `GET /api/workbook/conversations/{provider}/{session_id}?limit=&before=` (resolve any
   session of the conversation via `RootSessionOf`; `{conv_key, status, status_at, entries}`; 404 `not_found` when no
   entry and no status), `GET /api/workbook/entries?since=&until=&thing_done=1&limit=` (limit 1–200, default 50). Entries
   carry every §6 field (times in ms). `skipped` entries are returned. Tests: resolution through a relay chain; `before`
   paging by id; filters; units are ms; 400s.
2. **Events.** `workbook.entry` `{conv_key, session_id, entry}` on insert (`pending`) and on `Finish` (`ok` / `failed` /
   `skipped`) — one `ok` per entry (D3); `workbook.status` `{conv_key, session_id, status, updated_at}` when the status is
   written; `Broadcast("", type, jsonString)`. Tests: insert → `pending` event; push line → no entry event but a status
   event; finish → `ok` event; a re-write adds no second `ok`; values are JSON strings.
3. **Capability** `workbook.v1` via `moduleReady("workbook")` in `capabilityList()`. Device scope: add the two GET routes
   to `deviceAllowed` with the pinned-list test (QR spec §3.3). iOS already confirmed it ignores unknown event types.

Size ~450 lines.

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
   new dependency): start a hold goroutine in a `wbHolds` set `{ctx, cancel, wg, n atomic}` with cap 256 (at the cap →
   send at once). The hold calls `Await(ctx, sessionID, BroadcastTs/1e6, now + push_wait_s)` — **ns → ms** — then, if
   `ctx` is not cancelled, enqueues the job with the line (or without it). `Stop`: cancel → `wg.Wait()` → `holds.stopAll()`
   → `sender.Stop()`. Tests: a real ns `BroadcastTs` matches an entry whose `turn_at` is the same instant in ms; 256
   outstanding holds, the 257th sends at once; `Stop` with 256 outstanding holds returns in < 200 ms and enqueues nothing;
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
3. **Loading rules** (codex #9): (a) team view: for each seat on a `workbook.v1` host, `fetchConversation(limit: 1)` when
   the seat first appears and again after the host reconnects (connection key change); a 404 marks the session
   `missing` until a `workbook.entry` for that session arrives, which triggers one refetch; (b) a workbook view (drill,
   toolbar, ended list) fetches `limit: 20` on open and pages with `before = oldestId`; (c) events: `workbook.entry`
   upserts by `entry.id` (and learns `session_id → conv_key`); `workbook.status` carries `session_id`, so it maps even
   after a reload; a status for an unknown conversation is stored by `conv_key` and shown once a fetch maps a seat to it;
   (d) host removal / re-point forgets the host (the `roster-forget.ts` hook).
4. Events added to `lib/host-events.ts`, routed in `useMultiHostEventWs.ts` with the connection-key guard.
5. Capability `workbook.v1` probed with the unattended probe (a sibling flag sharing its generation guard);
   `selectWorkbookSupport(hostId)`.
Tests: parsers (ms units); upsert / dedupe / ordering; first-appearance fetch once per seat; reconnect refetch; 404 →
missing → entry event → refetch; status event after a reload maps through `session_id`; host forget; capability off →
no fetch at all.
Size ~550 lines.

## WA-2b SPA: workbook view, task line, drill-in, ended list, toolbar

1. **Task line** (TI spec §4.4 line 3): first sentence of the latest `status`, ellipsis, hover = whole status; none
   without a workbook or `workbook.v1`.
2. **Click** (TI spec §4.4 amended): on a `workbook.v1` host a full row → `teamDrill[teamKey] = seat`, the row's bot icon →
   `openTeamSeat`; one-line cells and non-`workbook.v1` hosts keep `openTeamSeat`.
3. **Ended list** (TI spec §4.4 amended, codex #8): `endedSeats: Record<teamKey, {hostId, sessionId, title, endedAt}[]>`
   in `useTeamUiStore` — a seat that leaves the team's roster on a frame from its connected host is recorded (newest
   first, cap 20, pruned with the team); full mode shows a collapsed 「已結束 (N)」 group at the bottom; a click drills
   into that seat's workbook. A drilled seat that ends keeps its view until back.
4. **Workbook view**: 「目前狀況」 + time; entries grouped by `thing` (most recent thing first; 進行中 / 完成; finished
   groups collapsed); `pending` 「整理中…」, `failed` 「整理失敗」 + reason on hover, `skipped` hidden; back control when
   `from:'team'`; 「更多」 pages. Scroll per `convKey` in a module-level memo (`transcript-scroll-memory.ts` pattern).
5. **Toolbar entry**: a `Notebook` toggle in the `StatusBar` per-tab buttons for a `cc` tmux-session pane on a
   `workbook.v1` host whose session has a workbook (`convOfSession` known, or a `limit: 1` probe returning 200) →
   `workbookTabs[tabId] = true`; toggling again deletes it; closing the tab prunes it.
Tests: task line present / absent; row click drills, bot icon opens the tab, back returns; non-`workbook.v1` row click
opens the tab; ended seat recorded once, listed, drill works, pruned with the team, cap 20; grouping and collapse;
state rendering; toolbar toggle on a team tab (back to team view) and a plain tab (closes); tab close prunes; scroll
restored after a real tab switch.
Screenshot gate (zh-TW, dark): team view with task lines and an ended group, workbook view (groups, a pending and a
failed entry), expanded mode.
Size ~650 lines.

## iOS (purdex-ios, from spec §10.3)

Data layer done (purdex-ios #61, follows D6). Screens (session detail: status + last 3 entries + 「更多」; a push with
`purdex.workbook` opens that entry highlighted) after WB-2 is deployed; 88 sends the final contract when WB-2 merges.

## 7. Order

Daemon (second seat): WB-1a-i → WB-1a-ii → WB-1b → WB-2 → WB-3.
App (solo): TI-3 → **WA-2a** → TI-5a / TI-5b → WA-1 → WA-2b.
Deploy: WB-* have no ordering gate on the App (capability-gated); WA-1 / WA-2b need WB-2 deployed for real data.

## 8. Spec clarifications made in this PR

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
