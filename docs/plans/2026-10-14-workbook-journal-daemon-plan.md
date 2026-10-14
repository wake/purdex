# Workbook journal — daemon plan (J-0a … J-1c)

Spec: `docs/specs/2026-10-14-workbook-journal-spec.md` (v3). This plan covers the daemon half (spec §4–§5, §8); the Mac half (J-2a/J-2b) and iOS (J-3) get their own plan once the user has confirmed spec §3 and §9.
**Draft, written on the two §3 proposals.** Q1 changes only `internal/module/journal/text.go` (T-1a.3); Q2 changes nothing here.
Facts below were read from origin/main on 2026-10-14 (paths relative to `internal/`).

## 0. Changes to the spec this plan makes (for the reviewer)

| spec | plan | why |
|---|---|---|
| §11 J-1a (one PR) | J-0a team feed, J-0b workbook feed, J-1a module + workbook/approval ingest, J-1b tasks/reports ingest, J-1c conversation `comm` | one PR was ~1,600 lines; the team change stays alone for 1f |
| §5.1 tasks/reports "every 5 s **and on `team.roster` changes**" | every 5 s only | the roster has no in-process hook (`module/team/roster_publish.go:40` is a WS-only signal); 5 s with an index is cheap |
| §5.2 answer `team_id` | `team_ids` (every team this conversation has led) | the client filters `journal.event` frames by `conv_key` or `team_ids`; a lead that led two teams over its lineage has both |
| §5.2 `404` for a never-seen session | always `200`, `events: []` | the daemon has no cheap "never seen" test; the App needs no distinction |
| §5.1 approvals ingest "opened / closed" | plus a reconcile of pending rows against the open set every tick | `SubscribeApprovals` drops on a full queue (`team/wire_relay.go:244-258`) |

## 1. Facts this plan rests on

- Module shape: `core/core.go:18-25` (`Name`, `Dependencies`, `Init`, `RegisterRoutes`, `Start`, `Stop`; optional `Closer`, `StatusReporter`); registered in `cmd/pdx/main.go:504` (workbook). Capabilities in `core/info_handler.go:106-109` keyed on `moduleReady(name)`; device routes in `cmd/pdx/device_scope.go` (`deviceAllowed`).
- DB open pattern: `module/workbook/store.go:99` (0600 file, WAL, `busy_timeout(5000)`, migrations by position `migrate.go:18-31`, chmod of `-wal`/`-shm`).
- Modules do not import each other: a narrow interface package under `internal/` + a registry key, registered in the provider's `Init`, read in the consumer's `Start` and tolerated when missing (`team/seat.go`, `workbooklines/workbooklines.go`).
- Workbook: `wb_entries.id INTEGER PRIMARY KEY`, never deleted; a final entry never changes (`store.go:203`, `:222-236`); `id` order is creation order, not finish order. One observer slot `Store.SetObserver` (`observer.go:28`) held by `m.announce`; fires on `InsertPending` and on `Finish`. Cross-conversation read `Store.Entries(since, until, thingDone, limit)` (`store.go:334`, `turn_at` range, index on `turn_at`). Entries carry `conv_key`, `session_id`, `team_id`, `role`, `ref`, `kind (turn|refresh)`, `thing`, `push`, `entry`, `thing_done`, `turn_at`.
- Lineage root: `team.LineageRootResolver` (`team.LineageRootKey`). Seat now: `team.SeatReader` (`team.SeatReaderKey`).
- Approvals: `team.ApprovalEvents.SubscribeApprovals(fn func(op string, a team.Approval)) (open []team.Approval, unsubscribe func())` (`team/wire_relay.go:255`, key `team.ApprovalEventsKey`), op `opened|closed`, every kind including `hook_ask` / `hook_permission` (`team/wire_ask.go:17-22`); 256-slot queue, overflow dropped. `team.OpenApprovalsReader.OpenApprovals()` for a fresh read. No "closed since" reader exists.
- Tasks: `tasks (team_id, seq)` PK, `created_at`/`updated_at` unix ms (`module/team/module.go:80`), never deleted (`deleted` is a status), no `updated_at` index (`task_store.go:54-55`). Reports: insert-only, `id` TEXT, PK `(team_id, member_key, id)`, `reports_task (team_id, task_seq, created_at)` (`report_store.go:58`); the implicit `rowid` grows. `owner_key` / `member_key` = `team_members.spawn_op` (`team_store.go:47`: `session_id`, `ref`, `title`). Teams: `teams.lead_session_id` changes on a lead relay (`relay_store_report.go:119`) — key on `team_id`. `team_label` column comes from a migration (`modread.go:30`). No cross-team task/report reader exists.
- Turn end: `agent.TerminalSessions.SubscribeTurnEnd(fn func(agent.TurnEndEvent)) (unsubscribe func())` (key `agent.TerminalSessionsKey`), at-least-once, tmux sessions only. Transcript: `convturns.Reader.LastTurns(ctx, "claude", sid, n)` (key `convturns.Key`). `convmodel.UserMessage{ID, At, Text, Truncated, Source, From{Kind, Name, Unverified}}`; `From.Name` is the sender's `from-name`, an address such as `mlab/purdex-88-b8` (`module/peers/send_local.go:108`).
- Scrub: `redact.String(s)` (`redact/redact.go:28`), idempotent.
- Host events: `core.Events.Broadcast("", type, jsonString)` (`core/events.go:310`); every subscriber, device tokens included, gets every frame.

## 2. Phases

| PR | content | reviewer extra | est. lines |
|---|---|---|---|
| J-0a | `internal/team/journalfeed.go` + team store queries + `tasks_updated` index | **1f** | ~450 |
| J-0b | `internal/workbookfeed` + workbook fan-out | — | ~350 |
| J-1a | module `journal`: store, workbook + approval ingest, backfill, API, `journal.event`, `journal.v1`, device route | — | ~780 |
| J-1b | tasks / reports ingest | — | ~500 |
| J-1c | conversation-derived `comm` (spec §4.4) | — | ~450 |
| D-1 | gap 3: leaked-thinking examples from `workbook.db`, prompt fix if real (88) | — | docs |

J-0a and J-0b are independent; J-1a needs J-0b; J-1b needs J-0a and J-1a; J-1c needs J-1a. Every PR: TDD, one commit per task, `go test` only for the touched packages (`-race` on the touched packages), `make lint`; the full `go test ./...` is 1f's at release. Each test group lists a mutation that must turn it red.

## 3. J-0a — team read feed (1f reviews)

**T-0a.1 Interface** `internal/team/journalfeed.go`:

```go
const JournalFeedKey = "team.journal-feed"

type TaskCursor struct{ UpdatedAt int64; TeamID string; Seq int }

type TaskChange struct {
    TeamID, TeamLabel, LeadSessionID string
    Seq int
    Subject, Description, Status string        // status pending|in_progress|completed|deleted
    OwnerSessionID, OwnerRef, OwnerTitle string // from team_members by owner_key; empty when unowned or unknown
    CreatedByRef string
    MetadataPRs []int
    CreatedAt, UpdatedAt int64
}

type ReportRow struct {
    RowID int64
    TeamID, ID, Kind, Summary, Body string      // kind ack|progress|question|ready|merged|blocked|done
    TaskSeq int
    Needs string                                // fields_json.needs ("" | "user" | "lead")
    PR int
    MemberSessionID, MemberRef, MemberTitle string
    CreatedAt int64
}

type JournalFeed interface {
    // Tasks changed after c, ordered (updated_at, team_id, seq) ascending; at most limit.
    TasksChangedAfter(c TaskCursor, limit int) ([]TaskChange, error)
    // Reports with rowid > afterRowID, rowid ascending; at most limit.
    ReportsAfter(afterRowID int64, limit int) ([]ReportRow, error)
    // Reports created at or after sinceMS, rowid ascending (the first-start backfill).
    ReportsSince(sinceMS int64, limit int) ([]ReportRow, error)
}
```

**T-0a.2 Store queries** in `module/team` (a new `journalfeed_store.go`), read-only, joining `teams` (label, lead) and `team_members` (owner/member by `spawn_op`). Registered in the team module's `Init` under `team.JournalFeedKey`.
- Index (team migration, `CREATE INDEX IF NOT EXISTS tasks_updated ON tasks (updated_at, team_id, seq)`): deployed DBs get it on start, no data change.
- Tests: tie on `updated_at` pages by `(team_id, seq)` without loss or repeat (M: order by `updated_at` only → a row is skipped); a status change moves the task past the cursor; an unowned task has empty owner fields; a member that relayed (new `session_id` in `team_members`) answers the new one; `ReportsAfter` sees an insert made after the cursor (M: `>=` instead of `>` → a repeat); `fields_json` without `needs`/`pr`; the index exists after migrating an old-schema DB.

## 4. J-0b — workbook feed

**T-0b.1 Interface** `internal/workbookfeed/workbookfeed.go` (same shape as `workbooklines`):

```go
const Key = "workbook.entry-feed"

type Entry struct {
    ID int64
    ConvKey, SessionID, TeamID, Role, Ref, Kind, State string // kind turn|refresh; state ok|failed|skipped (never pending)
    Thing, Push, Text string                                  // Text = the entry column
    ThingDone bool
    TurnAt, UpdatedAt int64
}

type Feed interface {
    // Subscribe delivers every entry that becomes final (Finish), on its own goroutine, in commit order; a full queue
    // (256) drops and counts. unsubscribe is idempotent.
    Subscribe(fn func(Entry)) (unsubscribe func())
    // FinalSince: final entries with turn_at >= sinceMS and id > afterID, id ascending, at most limit.
    FinalSince(sinceMS, afterID int64, limit int) ([]Entry, error)
}
```

**T-0b.2 Workbook**: `announce` stays the observer; a fan-out list in the module (`entryFeed`) receives the same `Event` when `Kind == entry` and the entry is final, and dispatches to subscribers off the writer's goroutine (one queue + goroutine per subscriber, as `team/approval_events.go`). `FinalSince` is a new store read (`state != 'pending'`, the `turn_at` index). Registered in `Init` under `workbookfeed.Key`.
- Tests: `InsertPending` delivers nothing, `Finish(ok)` delivers once (M: deliver on insert); a slow subscriber does not block `Finish` (M: call inline → the writer test times out); `announce` still broadcasts `workbook.entry` (regression); `FinalSince` skips a pending row and pages by id; unsubscribe stops delivery.

## 5. J-1a — module `journal`

Package `internal/module/journal`. `Dependencies()`: `agent, team, conversation, workbook, peers` (all read through registry keys; the module imports none of them except `module/agent` for `TurnEndEvent`, as the workbook does).

**T-1a.1 Store** `store.go`, `migrate.go` — `journal.db` in `DataDir`, the workbook's open pattern (0600, WAL, busy timeout, sidecar chmod), migrations by position.

```sql
CREATE TABLE journal_events (
  id TEXT PRIMARY KEY,                 -- '<source type>:<source id>'
  conv_key TEXT NOT NULL,              -- the seat's conversation (lineage root)
  team_id TEXT NOT NULL DEFAULT '',    -- the team the seat was in at the event, '' for none
  seat_session_id TEXT NOT NULL,
  at INTEGER NOT NULL,                 -- unix ms of the event itself
  kind TEXT NOT NULL CHECK (kind IN ('exec','done','task','taskdone','comm','req')),
  actor TEXT NOT NULL, title TEXT NOT NULL, body TEXT NOT NULL DEFAULT '',
  refs_json TEXT NOT NULL DEFAULT '[]',
  pending INTEGER NOT NULL DEFAULT 0,
  source_type TEXT NOT NULL, source_id TEXT NOT NULL,
  created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL);
CREATE INDEX journal_conv ON journal_events (conv_key, at, id);
CREATE INDEX journal_team ON journal_events (team_id, at, id) WHERE team_id <> '';
CREATE INDEX journal_pending ON journal_events (pending) WHERE pending = 1;
CREATE TABLE journal_teams (team_id TEXT PRIMARY KEY, lead_conv_key TEXT NOT NULL, label TEXT NOT NULL DEFAULT '', updated_at INTEGER NOT NULL);
CREATE INDEX journal_teams_lead ON journal_teams (lead_conv_key);
CREATE TABLE journal_cursors (name TEXT PRIMARY KEY, value TEXT NOT NULL);
```

- `Add(ev) (added bool)` = `INSERT … ON CONFLICT(id) DO NOTHING`; `SetPending(id, false) (changed bool)`; `NoteTeam(teamID, leadConv, label)` upsert; cursors get/set. Only a real change is announced (T-1a.6).
- `Page(conv, teamIDs, seatConv, before, limit)`: `(conv_key = ? OR team_id IN (…))` [`AND conv_key = seatConv`] `AND (at, id) < before`, `ORDER BY at DESC, id DESC LIMIT limit+1`, returned oldest first, `more` from the extra row. `PendingCount` with the same scope, no `before`.
- Tests: re-adding an id is a no-op (M: upsert overwrites → title changes); scope lead/member/solo/seat (a member's event with the team's id is in the lead's page, not in another team's lead's; M: drop the `team_id IN` arm); paging with equal `at` (M: compare `at` only → repeat); pending count follows `SetPending`; file mode 0600 including sidecars.

**T-1a.2 Scope helpers** `scope.go`: `convOf(sessionID)` via `LineageRootResolver` (error → the session itself, logged once); `seatOf(sessionID)` via `SeatReader` (error → none); `titleOf(sessionID, ref)` via `peers.OriginResolver.ResolveOriginBySession` (live title, else `ref`, else the first 8 of the id). A lead's teams: `journal_teams` rows whose `lead_conv_key` = the conversation; rows are noted from workbook entries with `role = lead` and from J-1b's task feed (`LeadSessionID` → conv).

**T-1a.3 Text** `text.go` — Q1 lives here only. Pure functions, table-tested:
- `exec`: title `push` (empty → the first sentence of `entry`, ≤ 80 runes), body `entry`. `done`: title 「完成：」+ `thing`, body `entry`.
- `req` by approval kind (title; body):
  - `lead`: 「申請成為 lead」; the reason.
  - `self_relay`: 「申請接力（已用 N%）」; —.
  - `member_relay`: 「申請幫 <member> 接力（已用 N%）」; —.
  - `adopt`: 「申請納入 <target>」; —.
  - `hook_permission`: 「工具權限：<tool_name>」; `tool_input.command` when present (Bash), else the compact `tool_input` JSON, first line, ≤ 120 runes.
  - `hook_ask`: the question's first sentence (≤ 80 runes); the options, one line.
  - unknown kind: 「<kind> 申請」.
- `firstSentence(s, n)`: up to the first 。！？.!? followed by space/end or a newline, cut at n runes with 「…」. `firstLines(s, 2)`.
- `refs(texts…, prs…)`: `PR #N` from PR fields, `#N` (3–6 digits) from text, task ids `[0-9a-z]{6}-\d+`; deduplicated, in order.
- Every stored string passes `redact.String` (workbook text already did; the rest did not).
- Payload fields: read from the existing structs in `team/wire*.go` (`LeadPayload`, `SelfRelayPayload`, `MemberRelayPayload`, `AdoptPayload`, `HookAskPayload`, `HookPermissionPayload` in `wire_ask.go`); a payload that does not parse falls back to the unknown-kind line.

**T-1a.4 Workbook ingest** `ingest_workbook.go`:
- Live: `workbookfeed.Feed.Subscribe`. A final entry with `state = ok`, `kind = turn` → `done` if `ThingDone` else `exec`; id `workbook:<id>`, `at = TurnAt`, `conv_key = ConvKey`, `team_id = TeamID`, actor `titleOf(SessionID, Ref)`, refs from the text. `role = lead` with a team id also `NoteTeam(TeamID, ConvKey, "")`.
- Catch-up (subscribe first, then scan): `FinalSince(cursor.turn_at − 15 min, 0, 500)` paged by id; the cursor `wb_turn_at` advances to the newest `TurnAt` seen. First start: `now − 30 days`.
- Every 60 s a rescan of the last 15 min (covers dropped deliveries; idempotent).
- Tests: ok turn → exec; thing_done → done with 「完成：」; failed / skipped / refresh → nothing (M: accept every state); a delivery dropped (subscriber not called) is recovered by the rescan (M: no rescan → missing); first start backfills 30 days and not 31 (M: no bound).

**T-1a.5 Approval ingest** `ingest_approvals.go`:
- `SubscribeApprovals`: the returned open set and each `opened` → `req` with `pending = 1`, id `approval:<id>`, `at = CreatedAt`, `conv_key = convOf(Origin.SessionID)`, `team_id = seatOf(Origin.SessionID).TeamID`, actor 「<titleOf(origin)> → 使用者」, text by kind (T-1a.3). `closed` → add if absent (with `pending = 0`), else `SetPending(false)`.
- Reconcile every 5 s: pending `approval:` rows not in `OpenApprovals()` → `SetPending(false)`.
- Backfill: the open set only (spec §5.1); closed approvals before deploy are not read (no reader exists).
- Tests: opened → pending req; closed → update (M: ignore closed → stays pending); a missed `closed` is fixed by the reconcile (M: no reconcile); each kind's title; a hook kind is included; an approval first seen closed is added with pending 0.

**T-1a.6 API and event** `api.go`:
- `GET /api/journal/conversations/claude/{session_id}?before=<at>:<id>&limit=<1..200, default 50>[&seat=<session_id>]` → `{"conv_key","team_ids":[…],"events":[…oldest first],"more":bool,"pending":N}`; bad `before`/`limit` → 400 `bad_request`; a provider other than `claude` → 404. Event JSON = spec §4.3 (`refs` an array, `pending` a bool, `source {type, id}`).
- `journal.event` `{"conv_key","team_ids":[team_id] or [],"op":"add"|"update","event":{…}}` via `Events.Broadcast`, only when `Add` / `SetPending` changed a row.
- `core/info_handler.go`: `journal.v1` when `moduleReady("journal")`. `cmd/pdx/device_scope.go`: allow the GET. `Status()`: `ready`, `init_error`, counts of dropped deliveries.
- Soft-fail like the workbook: an open failure leaves the module not ready (no capability, routes answer 503).
- Tests: the handler pages and scopes (lead sees a member's event); `before` parsing; the frame on add and on a pending flip, none on a duplicate add (M: broadcast every call); `journal.v1` present when ready, absent after a failed open; the device token reaches the GET (M: drop the allowlist line → 403).

## 6. J-1b — tasks and reports

`ingest_team.go`, reading `team.JournalFeed` every 5 s; cursors `task` (`TaskCursor` JSON) and `report_rowid`. First start: tasks with `updated_at ≥ now − 30 days` and `ReportsSince(now − 30 days)`.
- Each `TaskChange`: `NoteTeam(TeamID, convOf(LeadSessionID), TeamLabel)`. First sight → `task` id `task:<team>:<seq>`, `at = CreatedAt`, `conv_key = convOf(OwnerSessionID)` (unowned → the lead's), title `Subject`, body `firstLines(Description, 2)`, actor `OwnerTitle` (else the lead's title), refs from `MetadataPRs` + text. Status `completed` → `taskdone` id `taskdone:<team>:<seq>` at `UpdatedAt`, body = the summary of the task's latest `merged` / `done` report if one was seen. Leaving `pending`/`in_progress` also clears the task's pending report requests.
- Each `ReportRow`: `merged` → `taskdone` with the same id (first one wins, spec §8 gap 2), body `Summary`, refs `PR #N`. `question` / `blocked` with `Needs = user` → `req` pending, id `report:<team>:<member key>:<id>`, title `Summary`, body `firstLines(Body, 2)`, actor 「<MemberTitle> → 使用者」. Any later report on the same `(team, task_seq)` clears earlier pending report requests of that task. `ack`, `progress`, `ready`, `done`, and `needs = lead` → nothing (except feeding the taskdone body).
- Tests: created → task; completed → one taskdone; merged then completed → still one (M: distinct ids → two); needs=user question → pending, the next report clears it (M: never clear), task completion clears it; needs=lead → nothing; a lead relay (new `LeadSessionID`, same lineage) keeps one `journal_teams` row; restart resumes from the cursors without repeats.

## 7. J-1c — conversation-derived `comm` (spec §4.4)

`ingest_conv.go`:
- `SubscribeTurnEnd`; per event (at-least-once, so idempotent): `LastTurns(ctx, "claude", SessionID, 2)`; for each user item with `Source = user` or `peer`: id `<source>:<conv_key>:<item ID>`, `at = item.At`, `conv_key = convOf(SessionID)` (the recipient), `team_id = seatOf(SessionID).TeamID`, title `firstSentence(text, 80)`, body the rest cut to 400 runes, both through `redact.String`; actor 「使用者 → <recipient title>」 or 「<sender> → <recipient title>」, `<sender>` = the name part of `From.Name` (after the host alias), `Unverified` marked 「（未驗證）」.
- Skipped: other sources; peer texts that start with `[pdx ` (daemon notices relayed through a peer socket — relays, pauses and compactions already appear as requests or not at all); images and attachments (text only); a `Truncated` text is stored as cut.
- `ErrBusy` → one retry after 2 s; `ErrNotFound` → nothing.
- Not covered (spec §4.4, §10): a recipient that never ends a turn yet, remote seats, headless sessions (no tmux turn-end).
- Tests: a peer item → comm with the sender's name (M: skip peer); a user item → 「使用者 → …」; a slash / bash / queued item → nothing; a duplicated turn end → one row (M: id without item ID → duplicates); a secret in the text is masked (M: no redact); a `[pdx team]` notice → nothing.

## 8. D-1 — gap 3 (88)

Read `workbook.db` (read-only copy) for `ok` entries whose `entry` reads like the summariser's own reasoning (English planning phrases, "I need to", "Let me", self-reference to the transcript); list examples with ids in the J-1a PR. Real → amend the v2 prompt doc and `prompts/system-v2.txt` in a small separate PR; none → note it and close the gap.

## 9. Size measurement (for spec §5.1 retention)

J-1a's PR reports `journal.db` size after the 30-day backfill on mlab (copy of the live DBs, not the live files) and rows per day; no deletion is planned unless that is over 50 MB/year.
