# Workbook journal — daemon plan (J-0a … J-1c)

Spec: `docs/specs/2026-10-14-workbook-journal-spec.md` (v3). This plan covers the daemon half (spec §4–§5, §8); the Mac half (J-2a/J-2b) and iOS (J-3) get their own plan once the user has confirmed spec §3 and §9.
**Written on the two §3 proposals.** Q1 changes only `internal/module/journal/text.go` (T-1a.3); Q2 changes nothing here.
Facts below were read from origin/main on 2026-10-14 (paths relative to `internal/`).
Revision 2 (2026-10-14): codex plan review `task-mv7o2g27-y35uad`, 14 findings, all taken — see §10.

## 0. Changes to the spec this plan makes (for the reviewer)

| spec | plan | why |
|---|---|---|
| §11 J-1a (one PR) | J-0a team feed, J-0b workbook feed, J-1a module + workbook/approval ingest, J-1b tasks/reports ingest, J-1c conversation `comm` | one PR was ~1,600 lines; the team change stays alone for 1f |
| §5.1 tasks/reports "every 5 s **and on `team.roster` changes**" | every 5 s only | the roster has no in-process hook (`module/team/roster_publish.go:40` is a WS-only signal); 5 s with an index is cheap |
| §5.2 answer `team_id` | `team_ids` (every team this conversation has led) | the client filters `journal.event` frames by `conv_key`, `other_conv_key` or `team_ids`; a lead that led two teams over its lineage has both |
| §5.1 approvals ingest "opened / closed" | plus a two-way reconcile against the open set every tick | `SubscribeApprovals` drops on a full queue (`module/team/approval_events.go:108`) — a lost `opened` as well as a lost `closed` |
| §4.2 actor "seat titles … at the event's time" | the title known at ingest (live events: the time of the event; backfilled rows: the title stored then, else the ref) | no title history exists anywhere; a first write is kept |
| §4.1 member: tasks it "owns or created" | owns | only a lead creates tasks (`cmd/pdx/task_cmd.go:32`: "only a lead"); the lead sees them through the team |
| §4.3 record | adds `other_conv_key` | a second conversation that sees the event: the sender of a `comm`, the task owner of a report event when the reporter is someone else |

## 1. Facts this plan rests on

- Module shape: `core/core.go:18-25` (`Name`, `Dependencies`, `Init`, `RegisterRoutes`, `Start`, `Stop`; optional `Closer`, `StatusReporter`); registered in `cmd/pdx/main.go:504` (workbook). Capabilities in `core/info_handler.go:106-109` keyed on `moduleReady(name)`; device routes in `cmd/pdx/device_scope.go` (`deviceAllowed`).
- DB open pattern: `module/workbook/store.go:99` (0600 file, WAL, `busy_timeout(5000)`, migrations by position `migrate.go:18-31`, chmod of `-wal`/`-shm`).
- Cross-module reads go through a registry key, registered in the provider's `Init`, read in the consumer's `Start` and tolerated when missing. Most keys sit in a narrow package under `internal/` (`team/seat.go`, `workbooklines/workbooklines.go`); `peers.OriginResolverKey` and its type sit in `internal/module/peers` (`origin_resolver.go:16`), which the team module already imports (`module/team/module.go:442`) — the journal does the same.
- Workbook: `wb_entries.id INTEGER PRIMARY KEY`, never deleted; a final entry never changes (`store.go:203`, `:222-236`); `id` order is creation order, not finish order (`store.go:161`). `Finish` sets `updated_at` under the writer lock `s.wmu`. One observer slot `Store.SetObserver` (`observer.go:28`) held by `m.announce`; fires on `InsertPending` and on `Finish`. Entries carry `conv_key`, `session_id`, `team_id`, `role`, `ref`, `kind (turn|refresh)`, `thing`, `push`, `entry`, `thing_done`, `turn_at`, `updated_at`. Existence check pattern: `Known(convKey)` (`store.go:309-322`).
- Lineage root: `team.LineageRootResolver` (`team.LineageRootKey`). Seat now: `team.SeatReader` (`team.SeatReaderKey`).
- Approvals: `team.ApprovalEvents.SubscribeApprovals(fn func(op string, a team.Approval)) (open []team.Approval, unsubscribe func())` (`team/wire_relay.go:255`, key `team.ApprovalEventsKey`), op `opened|closed`, every kind including `hook_ask` / `hook_permission` (`team/wire_ask.go:17-22`); 256-slot queue, a full queue drops (`module/team/approval_events.go:108`). `team.OpenApprovalsReader.OpenApprovals()` for a fresh read. No "closed since" reader exists.
- Tasks: `tasks (team_id, seq)` PK, `created_at`/`updated_at` unix ms (`module/team/module.go:80`), never deleted (`deleted` is a status), no `updated_at` index (`task_store.go:54-55`); a report insert also updates its task (`report_store.go:160`). Reports: insert-only, `id` TEXT, PK `(team_id, member_key, id)` (`report_store.go:47-68`), the implicit `rowid` grows. `owner_key` / `member_key` = `team_members.spawn_op` (`team_store.go:47`: `session_id`, `ref`, `title`; `session_id` changes on a relay, `spawn_op` does not). Teams: `teams.lead_session_id` changes on a lead relay (`relay_store_report.go:119`) — key on `team_id`. `team_label` column comes from a migration (`modread.go:30`). Only a lead creates tasks (`cmd/pdx/task_cmd.go:32`). No cross-team task/report reader exists.
- Turn end: `agent.TerminalSessions.SubscribeTurnEnd(fn func(agent.TurnEndEvent)) (unsubscribe func())` (key `agent.TerminalSessionsKey`), at-least-once, tmux turn ends and accepted non-tmux Stops (`module/agent/turn_end_hub.go:146`, `nontmux_turn_end_test.go:16`); 64-slot queue per subscriber, a full queue drops (`turn_end_hub.go:33`, `:97`). Transcript: `convturns.Reader.LastTurns(ctx, "claude", sid, n)` (key `convturns.Key`). `convmodel.UserMessage{ID, At, Text, Truncated, Source, From{Kind, Name, Unverified}}`; `From.Name` is the sender's `from-name`, an address such as `mlab/purdex-88-b8` (`module/peers/send_local.go:108`).
- Live sessions: `peers.OriginResolver` — `ResolveOriginBySession`, `ListLiveOrigins()` (`origin_resolver.go:56`, `:169`) → `team.Origin{SessionID, Ref, Title, Address}`; live sessions only.
- Scrub: `redact.String(s)` (`redact/redact.go:28`), idempotent.
- Host events: `core.Events.Broadcast("", type, jsonString)` (`core/events.go:310`); every subscriber, device tokens included, gets every frame.

## 2. Phases

| PR | content | reviewer extra | est. lines |
|---|---|---|---|
| J-0a | `internal/team/journalfeed.go` + team store queries + `tasks_updated` index | **1f** | ~500 |
| J-0b | `internal/workbookfeed` + workbook fan-out + `wb_entries_updated` index | — | ~400 |
| J-1a | module `journal`: store, workbook + approval ingest, backfill, API, `journal.event`, `journal.v1`, device route | — | ~800 |
| J-1b | tasks / reports ingest | — | ~550 |
| J-1c | conversation-derived `comm` (spec §4.4) | — | ~500 |
| D-1 | gap 3: leaked-thinking examples from `workbook.db`, prompt fix if real (88) | — | docs |

J-0a and J-0b are independent; J-1a needs both (team info for scope, the workbook feed); J-1b and J-1c need J-1a. Every PR: TDD, one commit per task, `go test` only for the touched packages (`-race` on them), `make lint`; the full `go test ./...` is 1f's at release. Each test group names a mutation that must turn it red. If J-1a goes past 800 lines, the approval ingest (T-1a.5) moves to its own PR.

## 3. J-0a — team read feed (1f reviews)

**T-0a.1 Interface** `internal/team/journalfeed.go`:

```go
const JournalFeedKey = "team.journal-feed"

type TaskCursor struct{ UpdatedAt int64; TeamID string; Seq int }

type TaskChange struct {
    TeamID string
    Seq int
    Subject, Description, Status string        // status pending|in_progress|completed|deleted
    OwnerKey string                             // team_members.spawn_op; "" when unowned
    OwnerSessionID, OwnerRef, OwnerTitle string // resolved now from team_members; empty when unowned or unknown
    CreatedByRef string
    MetadataPRs []int
    CreatedAt, UpdatedAt int64
}

type ReportRow struct {
    RowID int64
    TeamID, MemberKey, ID string                // MemberKey = spawn_op: with TeamID and ID, the stable identity
    Kind, Summary, Body string                  // kind ack|progress|question|ready|merged|blocked|done
    TaskSeq int
    Needs string                                // fields_json.needs ("" | "user" | "lead")
    PR int
    MemberSessionID, MemberRef, MemberTitle string
    CreatedAt int64
}

type TeamInfo struct{ TeamID, Label, LeadSessionID string; EndedAt int64 }

type JournalFeed interface {
    // Tasks changed after c, ordered (updated_at, team_id, seq) ascending; at most limit.
    TasksChangedAfter(c TaskCursor, limit int) ([]TaskChange, error)
    // Reports with rowid > afterRowID and created_at >= sinceMS, rowid ascending; at most limit.
    // The first-start backfill passes sinceMS = now − 30 days and pages by rowid; afterwards sinceMS = 0.
    ReportsAfter(afterRowID, sinceMS int64, limit int) ([]ReportRow, error)
    // One team's lead (current session) and label; false when the id is unknown.
    TeamInfo(teamID string) (TeamInfo, bool, error)
}
```

**T-0a.2 Store queries** in `module/team` (a new `journalfeed_store.go`), read-only, joining `teams` (label, lead) and `team_members` (owner/member by `spawn_op`). Registered in the team module's `Init` under `team.JournalFeedKey`.
- Index (team migration, `CREATE INDEX IF NOT EXISTS tasks_updated ON tasks (updated_at, team_id, seq)`): deployed DBs get it on start, no data change.
- Tests: a tie on `updated_at` pages by `(team_id, seq)` without loss or repeat (M: order by `updated_at` only → a row is skipped); a status change moves the task past the cursor; an unowned task has empty owner fields; a member that relayed answers the new `session_id` and the same `OwnerKey` / `MemberKey`; `ReportsAfter` pages a backfill larger than `limit` to the end (M: drop `rowid > afterRowID` → the first page repeats); an insert after the cursor is seen once (M: `>=` → a repeat); `fields_json` without `needs`/`pr`; `TeamInfo` after a lead relay answers the new lead session; the index exists after migrating an old-schema DB.

## 4. J-0b — workbook feed

**T-0b.1 Interface** `internal/workbookfeed/workbookfeed.go` (same shape as `workbooklines`):

```go
const Key = "workbook.entry-feed"

type Entry struct {
    ID int64
    ConvKey, SessionID, TeamID, Role, Ref, Kind, State string // kind turn|refresh; state ok|failed|skipped (never pending)
    Thing, Push, Text string                                  // Text = the entry column
    ThingDone bool
    TurnAt, UpdatedAt int64                                   // UpdatedAt = when it became final
}

type Cursor struct{ UpdatedAt, ID int64 }

type Feed interface {
    // Subscribe delivers every entry that becomes final (Finish), on its own goroutine, in commit order; a full queue
    // (256) drops and counts. unsubscribe is idempotent.
    Subscribe(fn func(Entry)) (unsubscribe func())
    // FinalAfter: final entries with (updated_at, id) > c, ordered (updated_at, id) ascending, at most limit —
    // finish order, so a cursor never passes an entry that finishes later.
    FinalAfter(c Cursor, limit int) ([]Entry, error)
}
```

**T-0b.2 Workbook**: `announce` stays the observer; a fan-out list in the module (`entryFeed`) receives the same `Event` when `Kind == entry` and the entry is final, and dispatches to subscribers off the writer's goroutine (one queue + goroutine per subscriber, as `module/team/approval_events.go`). `FinalAfter` is a new store read (`state != 'pending'`) on a new index (workbook migration step 3: `CREATE INDEX IF NOT EXISTS wb_entries_updated ON wb_entries (updated_at, id)`). Registered in `Init` under `workbookfeed.Key`.
- Tests: `InsertPending` delivers nothing, `Finish(ok)` delivers once (M: deliver on insert); a slow subscriber does not block `Finish` (M: call inline → the writer test times out); `announce` still broadcasts `workbook.entry` (regression); `FinalAfter` skips a pending row, and an older id finishing after a newer one is returned after it (M: order by id → the late finisher falls behind the cursor); paging with equal `updated_at`; unsubscribe stops delivery; the migration on a v2 DB.

## 5. J-1a — module `journal`

Package `internal/module/journal`. `Dependencies()`: `agent, team, conversation, workbook, peers`. It imports `module/agent` (for `TurnEndEvent`, as the workbook does) and `module/peers` (for `OriginResolverKey`, as the team module does), nothing else from `internal/module`.

**T-1a.1 Store** `store.go`, `migrate.go` — `journal.db` in `DataDir`, the workbook's open pattern (0600, WAL, busy timeout, sidecar chmod), migrations by position.

```sql
CREATE TABLE journal_events (
  id TEXT PRIMARY KEY,                 -- '<source type>:<source id>'
  conv_key TEXT NOT NULL,              -- the seat's conversation (lineage root)
  other_conv_key TEXT NOT NULL DEFAULT '', -- a second conversation that sees it (§0), '' for none
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
CREATE INDEX journal_other ON journal_events (other_conv_key, at, id) WHERE other_conv_key <> '';
CREATE INDEX journal_team ON journal_events (team_id, at, id) WHERE team_id <> '';
CREATE INDEX journal_pending ON journal_events (pending) WHERE pending = 1;
CREATE TABLE journal_teams (team_id TEXT PRIMARY KEY, lead_conv_key TEXT NOT NULL, label TEXT NOT NULL DEFAULT '', updated_at INTEGER NOT NULL);
CREATE INDEX journal_teams_lead ON journal_teams (lead_conv_key);
CREATE TABLE journal_cursors (name TEXT PRIMARY KEY, value TEXT NOT NULL);
```

- `Add(ev) (added bool)` = `INSERT … ON CONFLICT(id) DO NOTHING`; `SetPending(id, bool) (changed bool)`; `FillBody(id, body) (changed bool)` (only when the stored body is empty — the taskdone summary, §6); `NoteTeam(teamID, leadConv, label)` upsert; `Known(conv)` (a row with `conv_key` or `other_conv_key` = conv, or a `journal_teams` row led by it); cursors get/set. Only a real change is announced (T-1a.6).
- `Page(conv, teamIDs, seatConv, before, limit)`: `(conv_key = ? OR other_conv_key = ? OR team_id IN (…))` [`AND (conv_key = seatConv OR other_conv_key = seatConv)`] `AND (at, id) < before`, `ORDER BY at DESC, id DESC LIMIT limit+1`, returned oldest first, `more` from the extra row. `PendingCount` with the same scope, no `before`.
- Tests: re-adding an id is a no-op (M: upsert overwrites → title changes); scope lead / member / solo / seat — a member's event with the team's id is in its lead's page and not in another lead's (M: drop the `team_id IN` arm), a `comm` the member sent (its conv in `other_conv_key`) is in the member's page (M: drop the `other_conv_key` arm), `seat=` keeps the seat's own and its `other_conv_key` rows; paging with equal `at` (M: compare `at` only → repeat); pending count follows `SetPending`; `FillBody` does not overwrite a body; file mode 0600 including sidecars.

**T-1a.2 Scope helpers** `scope.go`:
- `convOf(sessionID)` via `LineageRootResolver` (error → the session itself, logged once); `seatOf(sessionID)` via `SeatReader` (error → none).
- `titleOf(sessionID, fallbackRef)`: `OriginResolver.ResolveOriginBySession` live title, else the fallback (a stored title or ref), else the first 8 of the id. The first write is kept (§0).
- `noteTeamOf(teamID)`: any event carrying a team id not yet in `journal_teams` → `JournalFeed.TeamInfo(teamID)` → `NoteTeam(teamID, convOf(LeadSessionID), Label)`; re-read when a task change or a lead relay shows a different lead session (the lineage root stays the same, so the row only changes on a real change of lead). A missing feed (old team module) → the team is noted from workbook entries with `role = lead` only.

**T-1a.3 Text** `text.go` — Q1 lives here only. Pure functions, table-tested:
- `exec`: title `push` (empty → the first sentence of `entry`, ≤ 80 runes), body `entry`. `done`: title 「完成：」+ `thing`, body `entry`.
- `req` by approval kind (title; body):
  - `lead`: 「申請成為 lead」; the reason.
  - `self_relay`: 「申請接力（已用 N%）」; —.
  - `member_relay`: 「申請幫 <member> 接力（已用 N%）」; —.
  - `adopt`: 「申請納入 <target>」; —.
  - `hook_permission`: 「工具權限：<tool_name>」; `tool_input.command` when present (Bash), else the compact `tool_input` JSON, first line, ≤ 120 runes.
  - `hook_ask`: the first question's first sentence (≤ 80 runes); its options, one line.
  - unknown kind: 「<kind> 申請」.
- `firstSentence(s, n)`: up to the first 。！？.!? followed by space/end or a newline, cut at n runes with 「…」. `firstLines(s, 2)`.
- `refs(texts…, prs…)`: `PR #N` from PR fields, `#N` (3–6 digits) from text, task ids `[0-9a-z]{6}-\d+`; deduplicated, in order.
- Every stored string passes `redact.String` (workbook text already did; the rest did not).
- Payload fields: read from the existing structs in `team/wire*.go` (`LeadPayload`, `SelfRelayPayload`, `MemberRelayPayload`, `AdoptPayload`, `HookAskPayload`, `HookPermissionPayload` in `wire_ask.go`); a payload that does not parse falls back to the unknown-kind line.

**T-1a.4 Workbook ingest** `ingest_workbook.go`:
- Live: `workbookfeed.Feed.Subscribe`. A final entry with `state = ok`, `kind = turn` → `done` if `ThingDone` else `exec`; id `workbook:<id>`, `at = TurnAt`, `conv_key = ConvKey`, `team_id = TeamID`, actor `titleOf(SessionID, Ref)`, refs from the text; `noteTeamOf(TeamID)`.
- Catch-up (subscribe first, then scan): `FinalAfter(cursor, 500)` until empty; the cursor `wb` (`{updated_at, id}`) is stored after each page — safe because the order is finish order. First start: `{now − 30 days, 0}`.
- Every 60 s the same `FinalAfter` from the stored cursor (covers dropped deliveries; idempotent).
- Tests: ok turn → exec; thing_done → done with 「完成：」; failed / skipped / refresh → nothing (M: accept every state); a delivery dropped (subscriber not called) is recovered by the 60 s scan (M: no scan → missing); a crash between two catch-up pages resumes without loss or repeat (M: store the max `TurnAt` instead → a late finisher is lost); first start backfills 30 days and not 31 (M: no bound).

**T-1a.5 Approval ingest** `ingest_approvals.go`:
- `SubscribeApprovals`: the returned open set (first start: only rows with `CreatedAt ≥ now − 30 days`; later starts: all) and each `opened` → `req` with `pending = 1`, id `approval:<id>`, `at = CreatedAt`, `conv_key = convOf(Origin.SessionID)`, `team_id = seatOf(Origin.SessionID).TeamID`, actor 「<titleOf(origin)> → 使用者」, text by kind (T-1a.3). `closed` → add if absent (with `pending = 0`), else `SetPending(false)`.
- Reconcile every 5 s with `OpenApprovals()`, both ways: an open approval with no row → add it pending (a lost `opened`); a pending `approval:` row not in the set → `SetPending(false)` (a lost `closed`).
- Closed approvals from before the deploy are not read (no reader exists; spec §5.1 backfills open ones only).
- Tests: opened → pending req; closed → update (M: ignore closed → stays pending); a lost `closed` is fixed by the reconcile (M: drop the second direction); a lost `opened` is added by the reconcile (M: drop the first direction); each kind's title; a hook kind is included; an approval first seen closed is added with pending 0; first start skips an open approval older than 30 days.

**T-1a.6 API and event** `api.go`:
- `GET /api/journal/conversations/claude/{session_id}?before=<at>:<id>&limit=<1..200, default 50>[&seat=<session_id>]` → `{"conv_key","team_ids":[…],"events":[…oldest first],"more":bool,"pending":N}`. `404 not_found` when `Known(conv)` is false (spec §5.2); a known conversation with no event in scope → `events: []`. Bad `before`/`limit` → 400 `bad_request`; a provider other than `claude` → 404. Event JSON = spec §4.3 plus `other_conv_key` (`refs` an array, `pending` a bool, `source {type, id}`).
- `journal.event` `{"conv_key","other_conv_key","team_ids":[team_id] or [],"op":"add"|"update","event":{…}}` via `Events.Broadcast`, only when `Add` / `SetPending` / `FillBody` changed a row.
- `core/info_handler.go`: `journal.v1` when `moduleReady("journal")`. `cmd/pdx/device_scope.go`: allow the GET. `Status()`: `ready`, `init_error`, counts of dropped deliveries per feed.
- Soft-fail like the workbook: an open failure leaves the module not ready (no capability, routes answer 503).
- Tests: the handler pages and scopes (lead sees a member's event); never-seen → 404, known but empty → 200 `[]` (M: always 200); `before` parsing; the frame on add, on a pending flip and on a filled body, none on a duplicate add (M: broadcast every call); `journal.v1` present when ready, absent after a failed open; the device token reaches the GET (M: drop the allowlist line → 403).

## 6. J-1b — tasks and reports

`ingest_team.go`, reading `team.JournalFeed` every 5 s, **reports before tasks** in each tick; cursors `report_rowid` and `task` (`TaskCursor` JSON), each stored after its page. First start: `ReportsAfter(0, now − 30 days, …)` paged to the end, and tasks from `{now − 30 days, "", 0}`.
- Each `TaskChange`: `noteTeamOf(TeamID)`. First sight → `task` id `task:<team>:<seq>`, `at = CreatedAt`, `conv_key = convOf(OwnerSessionID)` (unowned → the team's lead conv), `team_id`, `seat_session_id` = the owner (or lead), title `Subject`, body `firstLines(Description, 2)`, actor `OwnerTitle` (else the lead's title), refs from `MetadataPRs` + text. Status `completed` → `taskdone` id `taskdone:<team>:<seq>` at `UpdatedAt`, same scope, body = the summary of the task's latest `merged` / `done` report seen so far. Leaving `pending`/`in_progress` also clears the task's pending report requests.
- Each `ReportRow` (scope: `conv_key = convOf(MemberSessionID)` — the reporter; `other_conv_key` = the task owner's conv when it differs; `team_id`; `seat_session_id` = the reporter; `at = CreatedAt`):
  - `merged` → `taskdone` with the task's id (spec §8 gap 2), body `Summary`, refs `PR #N`; if the row exists already, `FillBody(Summary)` (whichever arrives first, the summary ends up in the body).
  - `done` → nothing of its own; `FillBody` on the task's `taskdone` if it exists.
  - `question` / `blocked` with `Needs = user` → `req` pending, id `report:<team>:<MemberKey>:<id>`, title `Summary`, body `firstLines(Body, 2)`, actor 「<MemberTitle> → 使用者」.
  - Any later report on the same `(team, task_seq)` clears earlier pending report requests of that task.
  - `ack`, `progress`, `ready`, and `needs = lead` → nothing.
- Tests: created → task; completed → one taskdone; merged then completed, and completed then merged, each → one taskdone with the summary in its body (M: `Add` only → the second order loses the summary; M: distinct ids → two); a needs=user question → pending, the next report clears it (M: never clear), task completion clears it; needs=lead → nothing; the reporter and the owner differ → the event is in both members' pages and `seat=` of either; a lead relay keeps one `journal_teams` row; a report row identity does not change when the member relays (M: id from the session → a duplicate after the relay); restart resumes from the cursors without repeats.

## 7. J-1c — conversation-derived `comm` (spec §4.4)

`ingest_conv.go`:
- `SubscribeTurnEnd` with a callback that only marks the session dirty (a set under a mutex) and signals a worker — it never blocks, so the hub's 64-slot queue does not fill. The worker drains the set: per session, `LastTurns(ctx, "claude", SessionID, 5)` and every user item newer than the session's cursor `conv:<conv_key>` (the newest item `At` + ID seen); the cursor is stored after the session's items are added. `ErrBusy` → back in the set, retried after 2 s; `ErrNotFound` → dropped.
- For each user item with `Source = user` or `peer`: id `<source>:<conv_key>:<item ID>`, `at = item.At`, `conv_key = convOf(SessionID)` (the recipient), `team_id = seatOf(SessionID).TeamID`, `seat_session_id` = the recipient, title `firstSentence(text, 80)`, body the rest cut to 400 runes, both through `redact.String`.
  - `user`: actor 「使用者 → <recipient title>」.
  - `peer`: the sender is resolved from `From.Name` against `ListLiveOrigins()` (`Address` equal); found → `other_conv_key = convOf(sender SessionID)`, actor 「<sender title> → <recipient title>」; not found → actor 「<name part of From.Name> → <recipient title>」, no `other_conv_key`. `Unverified` adds 「（未驗證）」.
- Skipped: other sources; peer texts that start with `[pdx ` (daemon notices relayed through a peer socket — relays, pauses and compactions already appear as requests or not at all); images and attachments (text only); a `Truncated` text is stored as cut.
- Not covered (spec §4.4, §10): a recipient that has not ended a turn yet, remote seats, more than 5 turns between two drains of one session.
- Tests: a peer item → comm with the sender's title and `other_conv_key` (M: skip peer); a sender not live → the address name, no `other_conv_key`; a user item → 「使用者 → …」; slash / bash / queued → nothing; a duplicated turn end → one row (M: id without item ID → duplicates); three turn ends while the worker is blocked → one drain, all items (M: read one turn → items lost); a non-tmux Stop session is covered; a secret is masked (M: no redact); a `[pdx team]` notice → nothing.

## 8. D-1 — gap 3 (88)

Read `workbook.db` (read-only copy) for `ok` entries whose `entry` reads like the summariser's own reasoning (English planning phrases, "I need to", "Let me", self-reference to the transcript); list examples with ids in the J-1a PR. Real → amend the v2 prompt doc and `prompts/system-v2.txt` in a small separate PR; none → note it and close the gap.

## 9. Size measurement (for spec §5.1 retention)

J-1a's PR reports `journal.db` size after the 30-day backfill on mlab (copy of the live DBs, not the live files) and rows per day; no deletion is planned unless that is over 50 MB/year.

## 10. Review log

Codex plan review `task-mv7o2g27-y35uad` (gpt-5.6-sol, low), 14 findings → all taken:
1 lost `opened` → two-way reconcile (T-1a.5). 2 `ReportsSince` could not page → `ReportsAfter(afterRowID, sinceMS, limit)` (T-0a.1). 3 `MemberKey` missing from the report identity → added (T-0a.1, §6). 4 tasks a member created → only a lead creates tasks (§0). 5 taskdone order → reports before tasks, `FillBody` (§6). 6 workbook cursor by `TurnAt` could lose a late finisher → finish-ordered `(updated_at, id)` cursor and index (T-0b, T-1a.4). 7 turn-end queue overflow → non-blocking dirty set, 5 turns, per-session cursor (§7). 8 teams seen only through member events → `TeamInfo` (T-0a.1, T-1a.2). 9 404 restored with `Known` (T-1a.6). 10 actor title at the event's time → first write at ingest, stated in §0. 11 report scope unspecified → reporter + owner via `other_conv_key` (§6). 12 non-tmux Stops are in the turn-end feed → fact fixed. 13 `OriginResolverKey` lives in `module/peers` → the journal imports it as the team module does (§1, §5). 14 30-day bound on the open set at first start → T-1a.5.
