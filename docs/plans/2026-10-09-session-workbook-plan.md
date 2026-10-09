# Session workbook — plan

Spec: `docs/specs/2026-10-09-session-workbook-spec.md` (approved 2026-10-09). Prompt: `docs/specs/2026-10-09-session-workbook-prompt.md`
(prompt_ver 1). Owner: interface line (88). Daemon PRs by the interface line's second seat (Sonnet member); App PRs
after TI-2b / TI-3. iOS (§10.3) is built in purdex-ios against §9 / §7 of the spec.

Rules for every task: TDD (failing test first), one commit per task, mutation check before release, only the affected
packages; a full `go test ./...` / `vitest run --maxWorkers=3` only before merge, with a slot from purdex-1f. Review per
PR: codex R1 + R2 (attack → critic). Tests that touch tmux use `-L <label>` and `unset TMUX`. No secret, token or raw
transcript text in logs, tests or PR text.

## 0. Facts the plan stands on (main f459521a, read 2026-10-09)

- **Turn end.** `agent.TerminalSessions.SubscribeTurnEnd(fn func(TurnEndEvent)) (unsubscribe func())`
  (`internal/module/agent/terminal_sessions.go:55`; registry key `agent.TerminalSessionsKey`). `TurnEndEvent{SessionID,
  Text, At, Seq}` (`turn_end_hub.go:20`); `Text` ≤ 4096 bytes. Per subscriber: a 64-slot queue, one goroutine, panics
  recovered; a full queue **drops** the event (counted, logged), and a retried hook is published again with a newer
  stamp. So the workbook must be idempotent *and* tolerate a lost turn (the next turn's running state still carries the
  last entries). Published only for `cc` main-turn Stops on the tmux hook path. Pattern consumer: team `last_turn.go:162`
  (narrow interface asserted on the registry service, `turnEndMu` barrier on Stop, `module.go:490-518`).
- **Conversation model.** No in-process accessor: the conversation module keeps `cache *convfeed.Cache` / `resolver`
  private (`internal/module/conversation/module.go:44-47`) and registers no service. Reads go `cache.Acquire(ctx, sid)` →
  refresh (`http.go:286`) → `(*Entry).Window(turns, before, budget)` (`internal/convfeed/window.go:29`) →
  `convmodel.Turn{Items}` with `ItemUser` / `ItemAgentText` / `ItemStep` (`internal/convmodel/item.go`). `convmodel` is
  import-guarded to stdlib (`imports_test.go`), so the accessor lives in the conversation module.
- **Lineage.** `session_lineage(session_id PK, predecessor_session_id, …)` (`internal/module/team/relay_store.go:54`).
  `ChainRoots()` exists but scans the whole table (`relay_store_lineage.go:74`); no per-id `RootSessionOf`. Registry key
  `team.LineageReaderKey` (`internal/team/wire_relay.go:199`); key names are pinned by `wire_relay_contract_test.go:46`.
  Lineage is written only by relays (a plain `/clear` is a new conversation).
- **Push.** The Stop push is **not** driven by `TurnEndEvent`: it comes from the notify hub (`agent.NotifyFeedKey`,
  `notify_hub.go:100`), unordered against the turn-end hub. `onNotify` (`internal/module/push/agent_trigger.go:24-73`) →
  `gate.Decide` (`gate.go:76`; Stop = an `idle` frame with event `Stop`, StopFailure = `error`) → `snd.Enqueue(Job{Make})`
  where `Make` runs per device at send time. `holdSet` (`agent_trigger.go:88-133`): `time.AfterFunc` timers, cap 256,
  fires unconditionally (no early release today). Title today = `name` or `"<host>：<name>"` (`internal/push/agent_content.go:70-115`);
  payload builder `Content.payload` (`internal/push/content.go:223-243`).
- **Scrub.** `internal/convmodel/ccnorm/scrub` is a fixture tool (stream API over transcript rows); its credential regexes
  (`scrub.go:387-419`: Bearer, `sk-`, `gh?_`, `xox?-`, AWS) are the right set, its identity/path rewrites are not. It
  names neither `pdxp_` nor `pdxd_`. Importing it drags in the whole normaliser.
- **Host settings.** One row per key in `host_config.db`; pattern `relay_quota.go` (key, registry key, reader interface,
  strict `normalize*` / lenient `read*`, `emptyFor`, `handleGet` field, `readers`, PUT route, `Init` registration). Numeric
  pattern: `resources.go` (`resourcesInt` rejects null / strings / fractions); reader interface kept outside hostconfig
  (`internal/resources/settings.go:14-21`).
- **New module pattern.** devices / push: soft-fail `Init` (`ready`, `init_error`), `Status()` for `moduleReady`,
  `devices.db` 0600 incl. `-wal` / `-shm` (`internal/module/devices/store.go:42-110`); capability appended in
  `capabilityList()` (`internal/core/info_handler.go:84-93`); module added in `registerServeModules` (`cmd/pdx/main.go:402-492`).
- **Host events.** `c.Events.Broadcast("", type, jsonString)` (profiles `announce`, `handler_sections.go:57-67`); every
  connected client gets a new type; the SPA ignores unknown types.
- **Runner.** No in-process `claude -p` runner exists. Minimal-env pattern: `internal/peers/proxyhelper/client.go:69-72`
  (explicit `cmd.Env`, `Setpgid`, `WaitDelay`). `internal/claudeenv` keeps `CLAUDE_CODE_PLUGIN_DIRS` on purpose, so the
  runner builds its own environment.
- **SPA.** No `TeamPanel` yet (TI-4 not built); its plan (`docs/plans/2026-10-09-team-interface-plan.md:111-121`) and
  spec (`docs/specs/2026-10-09-team-interface-spec.md` §4.4) fix the width (P5), forbid a close control (P3) and leave the
  "current task" line out (R19) — the workbook spec §10.1 (user, later the same day) overrides these three; §7 below
  amends the TI spec. No conversation view in the SPA; a Claude Code tab's per-tab buttons live in the bottom
  `StatusBar` (`components/status/PaneModeButtons.tsx`). Capability gating = a per-host probe of `/api/info`
  (`lib/team/unattended-support.ts:26-44`, `useUnattendedStore`). Host events are routed in
  `hooks/useMultiHostEventWs.ts` to `lib/team/*-ws.ts` handlers. Resize building blocks: `RegionResize`, `PaneSplitter`,
  `useLayoutStore` clamps; device-local UI state goes in `useTeamUiStore` (never profile-synced).

## WB-1a daemon: store, lineage root, last-turn reader, redaction

1. **Module skeleton + store.** `internal/module/workbook`: `New()`, `Name() "workbook"`, `Dependencies() {agent, team,
   conversation, hostconfig}`, soft-fail `Init` opening `<data>/workbook.db` (0600 incl. `-wal` / `-shm`, the devices
   helpers), `Status() {ready, init_error}`. Schema exactly spec §6 (`wb_entries`, `wb_status`, `UNIQUE(session_id,
   turn_at, turn_seq)`), a `schema_version` row for later migrations. Store API: `InsertPending(e) (id, inserted bool)`
   (idempotent on the unique key), `Finish(id, state, reason, out)`, `SetStatus(conv, status, entryID)`,
   `Conversation(conv, limit, before)`, `Entries(since, until, thingDone, limit)`, `RecentForPrompt(conv, 3)`,
   `PendingForSession(sessionID, sinceMs)`. Tests: idempotent insert; state transitions; ordering newest first; paging;
   file modes; a broken DB path → soft fail, `Status().ready=false`.
2. **`RootSessionOf`.** `internal/module/team/relay_store_lineage.go`: per-id walk of `predecessor_session_id` with a
   `seen` guard (mirroring `rootOf`), a session absent from lineage is its own root. A **separate narrow interface**
   `team.LineageRootResolver { RootSessionOf(sessionID string) (string, error) }` in `internal/team`, registered on the
   same store under a new key `team.lineage-root` (contract test updated). A-line file: the PR is shown to 1f. Tests:
   no lineage → itself; a 3-hop chain → the root; a cycle (forced by a raw insert) → stops, returns the last unseen;
   DB error surfaces.
3. **Last-turn reader.** `internal/module/conversation`: `LastTurn(ctx, provider, sessionID) (convmodel.Turn, bool,
   error)` = `Acquire` → refresh → `Window(1, -1, always-true)` → release; interface `conversation.LastTurnReader` in a
   small exported package (e.g. `internal/convfeed` or `internal/module/conversation/convapi`) registered at `Init` under
   `conversation.last-turn`. Errors (`ErrNotFound`, `ErrBusy`, resolve) returned as is. Tests: a fixture transcript →
   the last turn's user / steps / agent text; not found; busy.
4. **Redaction.** New `internal/redact`: `String(s string) string` replacing Bearer tokens, `sk-…`, `gh[pousr]_…`,
   `xox[a-z]-…`, AWS key ids, `pdxp_…`, `pdxd_…`, and long mixed letter-digit runs (≥ 32, the `LooksLikeToken` rule
   re-implemented, not imported) with `[redacted]`. Table tests per pattern, a negative set (ordinary words, paths, a
   40-char hex git sha is **kept** — it is not a secret and the prompt drops it anyway — confirm against the spec's
   "long mixed letter-digit strings (≥ 32)": a sha is hex-only, letters a–f + digits, so it *does* mix; decide in the PR
   with a test either way and note it), idempotence.

Size estimate ~700 lines incl. tests.

## WB-1b daemon: subscriber, queue, runner, validation

1. **Settings.** hostconfig key `workbook` `{"push_wait_s": 8, "enabled": true}`; strict normalize (int 0–30, bool),
   lenient read, `emptyFor`, `handleGet` field `workbook`, PUT `/api/hostconfig/workbook`; reader interface
   `workbook.SettingsReader { WorkbookSettings() (Settings, error) }` in a small package outside hostconfig (the
   `internal/resources/settings.go` pattern), registered at hostconfig `Init`. `enabled=false` stops new Haiku calls
   (entries keep their history). Tests per the `relay_quota` / `resources` suites.
2. **Subscriber.** `Start` asserts `agent.TerminalSessionsKey` → `SubscribeTurnEnd`; the callback only enqueues (never
   blocks: the hub's 64-slot queue is the only buffer before us). Stop: unsubscribe, then a barrier like team's
   `turnEndMu`. Each event → `RootSessionOf` → `InsertPending` (the unique key drops a repeat; the entry is `pending`
   from here, which is what WB-3 waits on). Drops by the hub are logged by the hub; the workbook needs nothing more.
3. **Queue.** Per conversation FIFO, at most 3 waiting (older → `skipped: backlog`); host-wide at most 2 running; hourly
   cap 300 (`skipped: cap`, one log line per hour). One worker goroutine per running call, a dispatcher picks the next
   conversation whose head is runnable (strict order within a conversation). Tests with a fake runner: order within a
   conversation; two conversations in parallel; backlog trimming; cap; Stop drains without starting new calls.
4. **Input.** `LastTurn` (fallback: `TurnEndEvent.Text` as `assistant_text`, no tools); a turn with no agent text and no
   step → `skipped: no_text` without a call (spec §4.3). Build spec §5.2's JSON (user ≤ 1500, assistant ≤ 3000, tools
   ≤ 20 as `"<Tool>: <summary>"`), `redact.String` on every string, `previous_status` + `RecentForPrompt`.
5. **Runner.** `exec.LookPath("claude")` on the daemon's `PATH`; the exact isolated command of spec §3 with
   `--system-prompt-file <data>/workbook/prompt-v1.txt` (written at Start), stdin = the JSON, cwd = an empty
   `<data>/workbook/run/`; `cmd.Env` = only `HOME, USER, LOGNAME, TMPDIR, LANG, PATH` (spec §13 M1); `Setpgid`, a 30 s
   context, `WaitDelay` 2 s, the process group killed on timeout. Result classification: exit ≠ 0 → `failed: exit`, a
   stderr/JSON error mentioning auth/login → `failed: auth` + one log line (`reason auth`), timeout → `failed: timeout`.
   `latency_ms` stored. Tests: a fake `claude` script on a temp `PATH` (echo fixed JSON / sleep past the timeout / exit 1
   / print an auth error); the environment it sees contains none of `CLAUDE_*`, `CLAUDE_CODE_PLUGIN_DIRS`, `PDX_*`.
6. **Prompt constant.** `prompt.go`: the system prompt and the re-write prompt as Go constants, `PromptVersion = 1`. A
   test reads `docs/specs/2026-10-09-session-workbook-prompt.md` (relative path from the package) and asserts each
   constant equals its fenced `text` block byte for byte, so the doc and the code cannot drift.
7. **Validation + repair** (spec §5.4): strip one code fence → JSON; non-JSON → one retry → `failed: format`; `push` > 40
   cut at the last punctuation ≤ 40, none → dropped; `thing` > 16 → cut; `entry` > 150 → one background re-write (the
   re-write prompt, same runner, counts toward the cap and concurrency), still > 150 → cut at the last sentence end ≤
   150; `status` > 200 → cut at the last sentence end ≤ 200; `skip=true` → `skipped: model`. `redact.String` on every
   output field. On `ok`: `Finish` + `SetStatus` (unless skipped). The entry's push line is final before the re-write
   starts (WB-3 never waits for it). Tests: each repair rule with Chinese text; the re-write path; redaction of an output
   that echoes a token.

Size estimate ~800 lines incl. tests (split 1b-i settings + subscriber + queue / 1b-ii runner + validation if over).

## WB-2 daemon: API, events, capability

1. **Routes** (spec §9): `GET /api/workbook/conversations/{provider}/{session_id}?limit=&before=` (resolve any session of
   the conversation via `RootSessionOf`; `{conv_key, status, status_at, entries}`; 404 `not_found` when no entry and no
   status), `GET /api/workbook/entries?since=&until=&thing_done=1&limit=` (bounds: limit 1–200, default 50). Entries carry
   every §6 field except the raw prompt (none is stored). `skipped` entries are returned (the App hides them). Tests:
   resolution through a relay chain; paging; filters; 400s.
2. **Events.** `workbook.entry` `{conv_key, session_id, entry}` on insert and every state change; `workbook.status`
   `{conv_key, status, updated_at}`; plain `Broadcast("", …)` (the profiles pattern). purdex-ios confirms it ignores
   unknown event types before this merges (asked by 88). Tests: an insert → `pending` event; finish → `ok` event +
   status event; re-write → a second `ok` event with the new entry.
3. **Capability** `workbook.v1` via `moduleReady("workbook")` in `capabilityList()`. Device scope: add the two GET routes
   to `deviceAllowed` (iOS reads them, spec §10.3) with the pinned-list test (QR spec §3.3).

Size ~450 lines.

## WB-3 daemon: push hold and body

1. **Waiter in the workbook module.** `workbook.PushLines` interface (registry key `workbook.push-lines`):
   `Await(sessionID string, sinceMs int64, deadline time.Time) (Line, bool)` where `Line{Thing, Push, ConvKey, EntryID}`;
   it returns as soon as a `pending` entry of that session with `turn_at ≥ sinceMs − 2000` reaches `ok` with a non-empty
   `push`, or immediately `false` when it reaches failed / skipped, or `false` at the deadline. Implemented with a per-
   session waiter list notified by `Finish` (no polling). Tests: ready before deadline; failed → false at once; deadline;
   an older turn's entry does not satisfy a newer Stop.
2. **Hold in the push module.** In `onNotify`, after `gate.Decide` accepted a `Stop` / `StopFailure` and
   `push_wait_s > 0` and the reader exists: start a hold goroutine (bounded by the `holdSet` cap of 256; at the cap → send
   at once as today) that calls `Await(sessionID, frameTs, now + push_wait_s)` and then enqueues the job. `Stop()` cancels
   every hold (a context shared by the holds, cancelled before `snd.Stop()`). The notify goroutine never waits.
3. **Body and title.** `push.AgentInput` gains an optional `Workbook *WorkbookLine`. When present: body = `Push`, title =
   today's title with `・{Thing}` appended (`"<host>：<name>・<thing>"` or `"<name>・<thing>"`), cut to the title limit by
   cutting `thing` first; payload `purdex.workbook = {"conv_key": …, "entry_id": …}`. Absent → today's title and body,
   unchanged (also every Notification / permission push, spec §7). Tests: payload shape with and without; title cutting
   keeps the session name; a present Mac still suppresses (gate rule 4 runs first); a hold at the cap sends at once.

Note for 1f (user-visible, decided here unless the user objects): the spec's title `{session name}・{thing}` keeps today's
`<host>：` prefix when the push carries one, so multi-host titles stay distinguishable.

Size ~450 lines.

## WA-1 SPA: workbook data layer

1. `lib/workbook/types.ts` (entry, status, states, reasons), `api.ts` (`fetchConversation(hostId, provider, sessionId,
   {limit, before})`, `fetchEntries`), strict parsers (drop a malformed entry whole, warn once).
2. `stores/useWorkbookStore.ts`: `byHost[hostId].byConv[convKey] = {status, statusAt, entries (newest first, deduped by
   id), loadedBefore, loading}` + `convOfSession[hostId][sessionId] → convKey`; not persisted (re-fetched).
3. Events: add `workbook.entry` / `workbook.status` to `lib/host-events.ts`; route in `useMultiHostEventWs.ts` with the
   connection-key guard to `lib/workbook/workbook-ws.ts` (`handleWorkbookEvent`): upsert by `entry.id`, status replace if
   newer; forget on host removal / re-point (the `roster-forget.ts` hook).
4. Capability: `workbook.v1` probed with the unattended probe (`lib/team/unattended-support.ts` gains a third flag, or a
   sibling probe sharing its generation guard); `selectWorkbookSupport(hostId)`.
Tests: parsers; upsert / dedupe / ordering; a state change replaces the pending entry; status event updates the line;
host forget; capability gating returns nothing for an older daemon.
Size ~500 lines.

## WA-2 SPA: shared panel (TI-4 amended) + workbook view + entry points

Lands **as TI-4** (the team panel PR grows into the shared panel; TI spec amended in §7 below), after TI-2b / TI-3.

1. **Panel area.** One shell-mounted panel (TI-4's mount point). State in `useTeamUiStore` (device-local, never
   synced): `panel: {width, expanded}` (one pair for the area), `teamDrill: Record<teamKey, {hostId, sessionId}>`,
   `workbookTabs: Record<tabId, true>`. What it shows = the TI spec §4.4 rule, as one pure selector
   `panelView(activeTabId)`: the active tab's toggle on → `{kind:'workbook', from:'tab'}` for that tab's session; else the
   active tab's team → `teamDrill[teamKey]` ? `{kind:'workbook', from:'team'}` : `{kind:'team'}`; else `null`. Resize by a
   `RegionResize` edge with draft-then-commit (the `ActivityBarWide` pattern), width clamped 280–720, `expanded` = most
   of the content area; a header control toggles expanded. Pruning: `teamDrill` with the team's other entries (TI spec
   §3) and by the back control; `workbookTabs` when the tab closes.
2. **Team view** = TI-4's full / one-line panel; each full row gains line 3, 「正在做的任務」 (first sentence of the
   conversation's latest `status`, ellipsis, hover = whole status; hidden without a workbook or without `workbook.v1`);
   on a `workbook.v1` host clicking a full row → `teamDrill[teamKey] = seat`, the row's bot icon → `openTeamSeat`
   (R3 + R10); without `workbook.v1`, and for one-line cells, the row / cell click stays `openTeamSeat`. A drilled seat
   that leaves the roster keeps its workbook view until back (records are kept); an ended member without a tab has no
   way in in v1 (spec §10).
3. **Workbook view**: 「目前狀況」 + time; entries grouped by `thing` (most recent thing first; 進行中 / 完成; finished
   groups collapsed); states (`pending` 「整理中…」, `failed` 「整理失敗」 + reason on hover, `skipped` hidden); back
   control to the team view when `from:'team'`; paging 「更多」 (`before=`). Live via the store.
4. **Toolbar entry**: a `Notebook` toggle in the `StatusBar` per-tab buttons for a `cc` tmux-session pane with a workbook
   (`convOfSession` known or a probe GET returning 200) → `workbookTabs[tabId] = true`; toggling it again deletes the
   entry, so `panelView` falls back to the team view on a team tab and to nothing elsewhere.
5. **Tab-hosted rule**: scroll position per `convKey` in a module-level memo (the `transcript-scroll-memory.ts`
   pattern); a real-`TabContent` switch-away-and-back test (the `ExecutionView.tab-switch.test.tsx` pattern) for both
   views; panel state survives a reload (store).
Tests (beyond TI-4's): `panelView` table (toggle on beats team; team tab with / without a drill; non-team tab without
toggle → null; switching tabs away and back returns the same view); resize clamps and persists; expanded toggles; row
click drills in, bot icon opens the tab, back returns; a non-`workbook.v1` host keeps row click = open tab; one-line
cell click opens the tab; a drilled seat leaving the roster keeps the view; toolbar toggle open / close on a team tab and
a plain tab; closing a tab prunes its toggle; grouping and collapse; pending / failed / skipped rendering; capability off
→ no line, no toggle; scroll restored after a tab switch.
Screenshot gate (zh-TW, dark): team view with task lines, workbook view (groups, a pending and a failed entry),
expanded mode, narrow mode.
Size: TI-4 ~550 + ~600 → split WA-2a (panel area + team view + task line) / WA-2b (workbook view + toolbar + memo).

## iOS (purdex-ios, from spec §10.3)

Session detail: status + last 3 entries + 「更多」; a push with `purdex.workbook` opens that entry highlighted; capability
`workbook.v1`; ignore unknown host-event types (confirm now). 88 sends the contract when WB-2 merges.

## 7. TI spec amendment (in the same docs PR)

`docs/specs/2026-10-09-team-interface-spec.md` (done in this PR): R16 / R19 / P3 / P5, §3, §4.4, §4.11, §5, §6
updated to the workbook spec §10.1 decision — the panel area is resizable with an expanded mode (P5's "same width" now
means full and one-line share the area's current width), full-mode rows carry the 「正在做的任務」 line, a row click
drills into the seat's workbook with a back control while the row's bot icon keeps R16's open / switch (88's ruling,
shown to the user), P3 stays for the team view while a workbook opened from a tab closes with its toggle, and §4.4 states
one display rule (tab toggle → team drill → team view → nothing). `docs/plans/2026-10-09-team-interface-plan.md` TI-4
points here.

## Order

WB-1a → WB-1b → WB-2 → WB-3 (daemon, second seat) ∥ TI-2b → TI-3 → WA-1 → WA-2a (TI-4) → WA-2b → TI-5a/5b (solo).
Deploy: WB-1a/1b/2/3 have no ordering gate on the App (capability-gated); WA-* need WB-2 deployed for real data.
