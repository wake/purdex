# Team tasks and reports (T) — Implementation Plan

> **Status (2026-10-09):** draft for one codex round (plan + spec). Written against origin/main **`532e07bc`** by member β (`mlab/_tyfq9z`) for the coordinator purdex-1f (`mlab/_vqnjx1`).
> **Source:** spec `docs/specs/2026-10-09-team-task-report-spec.md` (T0–T3, D-1…D-7, phases T-1…T-3, M-T1). Research `docs/research/2026-10-08-agent-teams-and-workflow.md` §2.3–§2.5.
> **Format:** the compact format of plan v3 and the U23/U24 plan — contracts, rules, named tests and mutation gates, no full code. The implementer writes the code test-first from these contracts.
> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:subagent-driven-development. Each PR is TDD, one task per commit. Re-verify every `file:line` against main before starting a PR: the resource-lease line (δ), the U24 display line (γ) and the interface line (U1) move fast.

**Goal.** A lead hands work to a member as a task the daemon keeps (`pdx task add`), sees what each member is on (`pdx team` TASK / LAST, the roster), and gets structured reports up (`pdx report <kind>`), each also one peer message. A member's turn end records its last answer on its task silently. Nothing gates a merge (T3).

---

## Global constraints

- Plan v3's constraints apply: ≤ 800 diff lines **or** ≤ 20 files per PR (the coordinator has accepted a test-heavy overshoot case by case); Go `go test ./<pkg>/ -count=1`, `-race` on the touched packages one at a time, `gofmt -l`, `make lint`; `git checkout -- pdx` never needed if nobody runs `go build ./cmd/pdx/` at the root (build with `-o` into a scratch dir).
- **Resource rules** for every subagent brief: affected tests only while developing; the full vitest once before merge with `--maxWorkers=3` after asking the coordinator; `go test -race` per affected package, one at a time.
- **No SPA change in T.** The roster field (T-3) is additive; whether the Mac App shows it is the interface lead's call (spec D-7).
- **No mod change in T** (see M-T1 below and decision D-T4): the last-turn text comes from the existing settings `Stop` hook, daemon side. Interface lead 88 confirmed (2026-10-09) the `Stop` hook and its `last_assistant_message` stay.
- **Older peers stay silent.** Every new wire field is `omitempty`; new routes answer an older CLI nothing it misreads; a newer CLI against an older daemon gets a plain 404 → exit 21 (`unsupported`, `cmd/pdx/exitcodes.go`).
- **Deploy batches:** T-1 (daemon + CLI) ships as one deploy after T-1d merges; T-2 (CLI + `pdx setup` for the skill); T-3 (daemon). The coordinator deploys.

## Measurements (plan time)

**M-T1 — the member's last turn text and its cost.**
1. **The daemon already receives it.** Claude Code's settings `Stop` hook (main loop only; subagents raise `SubagentStop`, `internal/agent/cc/events.go:56-64`) is mapped to `PdxStop`, and the provider keeps `last_assistant_message` in the event detail (`internal/agent/cc/status.go:74-82`). The hook already runs once per main turn for every session that has the Purdex hooks, so recording it adds **no fork and no mod code** — only an in-process fan-out in the daemon.
2. **The mod could too** (`turn.complete`'s `e.answer` is "the assistant's final visible text this turn", CC 2.1.294 types, `TurnCompleteFields.answer`; main loop = `agentId` absent), but the only mod → daemon channel is the interface line's `mod.sock` event stream, whose event types are a closed set carrying no conversation text (`internal/modevents/wire.go:41-59`); interface lead 88 asked us **not** to add a type (U1-5's content events are item streams, not per-turn summaries).
3. **The two known gaps of the hook path** (88, 2026-10-09), accepted by this plan: (a) a turn interrupted with Esc raises no `Stop`, so that turn records nothing; (b) a hook delivery that fails is not retried (2 s timeout, no retry; turns during a daemon restart are lost). D-4 is a convenience status, "occasionally one turn stale" is acceptable; if it is not, the later path is the interface line's conversation model (U1-4 `AgentText` + turn outcome), not a new event type.
4. **How the daemon knows the session is a member:** `Store.ActiveMemberInLiveTeam(sessionID)` (`internal/module/team/team_store.go:158`) on the hook's session id — no role cache needed in the mod.
5. **Scope rule from 88 (2026-10-09):** the user is planning a separate "session workbook" (Haiku summaries per session). T stores **the raw text, truncated** — no summarising, no classification — so the two never compete.

**Measured facts the plan rests on** (survey of main, 2026-10-09):
- `team.db` schema: constants run by `OpenStore` (`internal/module/team/store.go:46-100`); new columns via `ensureColumn` (`migrate.go:29`) called from a `migrateX` after `migrateUsage` (`store.go:95`).
- **No team short id exists**: a team id is the approving lead request's UUID v4 (`team_store.go:29-31`).
- `team_members` is keyed by `spawn_op` (`team_store.go:46`); a relay's `cleared` report moves the row's `session_id` / `ref` in place (`moveTeamRoles`, `relay_store_report.go:79-108`) and resets the usage columns (`resetMemberUsage`, `:75`). So **a task owned by `spawn_op` follows the member through relays with no lineage lookup.** An adopted member (U24) has `spawn_op = <adopt request id>` (U24 deviation 5), so the key holds for both origins.
- Caller identity: `origin_inbox` → `ResolveOrigin` (trusted, `team_handler.go:41-67`); lead = `callerTeam` → 409 `not_lead`; member = `ActiveMemberInLiveTeam`.
- **The daemon has no in-process peer send.** Every message today is sent by the CLI through `POST /api/peers/send` with the caller's own inbox as `OriginInbox` (`sendBrief`, `cmd/pdx/team_cmd.go:356`); a failed brief is exit 1 with the JSON on stdout.
- CLI plumbing: `teamSetup` (`team_cmd.go:128`), `teamReportErr` + `teamRefusalCodes` (`:145`, `:72`; refusals → exit 13), `parseTeamFlags` (`:111`), `pdx team` table (`:462-479`), dispatcher + usage line (`cmd/pdx/main.go:46-96`).
- Roster: `RosterMember` (`internal/team/wire_roster.go:35-40`), built in `buildRoster` (`internal/module/team/roster.go:51-95`); `rosterChanged()` (`roster_publish.go:40`) re-hashes and broadcasts strictly only on a change.
- Agent module fan-out precedent: `sessionStartHub` + `SubscribeSessionStart` (`internal/module/agent/terminal_sessions.go:84-218`, published at `handler.go:658`); the team module already looks up `agent.TerminalSessionsKey` (`internal/module/team/spawn_register.go:35`).
- Skill tests pin exact substrings, including the spawn grammar `pdx spawn [--cwd <dir>] … [--brief-file <f> | --brief <text>]` (`cmd/pdx/plugin/embed_test.go:477`); new spawn flags are appended after it, never inserted.

---

## Shared contracts

**Tables (T-1a), in `team.db`:**

```
tasks(
  id TEXT PRIMARY KEY,            -- "<team6>-<seq>", team6 = first 6 hex chars of the team UUID
  team_id TEXT NOT NULL, seq INTEGER NOT NULL,          -- UNIQUE(team_id, seq); seq = MAX(seq)+1 in the insert tx
  subject TEXT NOT NULL, description TEXT NOT NULL DEFAULT '',
  done_when_json TEXT NOT NULL DEFAULT '[]',
  status TEXT NOT NULL,           -- pending | in_progress | completed | deleted
  owner_member TEXT NOT NULL,     -- team_members.spawn_op (stable across relays)
  blocked_by_json TEXT NOT NULL DEFAULT '[]',           -- task ids of the same team
  created_by_ref TEXT NOT NULL,   -- the lead's ref at creation
  metadata_json TEXT NOT NULL DEFAULT '{}',             -- {branch?, prs[]?, shas[]?}
  last_report_kind TEXT NOT NULL DEFAULT '', last_report_summary TEXT NOT NULL DEFAULT '', last_report_at INTEGER NOT NULL DEFAULT 0,
  last_turn_summary TEXT NOT NULL DEFAULT '', last_turn_at INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL)
reports(
  id TEXT PRIMARY KEY,            -- UUID v4 from the CLI (idempotency key)
  team_id TEXT NOT NULL, task_id TEXT NOT NULL DEFAULT '',  -- '' = no task
  member TEXT NOT NULL,           -- spawn_op
  kind TEXT NOT NULL, summary TEXT NOT NULL,
  fields_json TEXT NOT NULL DEFAULT '{}',               -- needs, pr, reviews[], sha
  body TEXT NOT NULL DEFAULT '', created_at INTEGER NOT NULL)
team_members + last_turn_summary TEXT, last_turn_at INTEGER (ensureColumn; T-3; never reset on relay)
```

**Wire (`internal/team/wire_task.go`, T-1a):**
- `Task {ID, TeamID, Subject, Description, DoneWhen []string, Status TaskStatus, Owner TaskOwner{Ref, Address, Title, SpawnOp}, Blocks []string (derived), BlockedBy []string, Blocked bool (derived: some blocked_by not completed/deleted), CreatedBy string, CreatedAt, UpdatedAt int64, Metadata TaskMetadata{Branch string; PRs []int; SHAs []string}, LastReport *TaskReportStamp{Kind, Summary, At}, LastTurn *TaskTurnStamp{Summary, At}}` — `Owner.Ref` is the member's **current** ref (resolved at read).
- `TaskStatus`: `pending | in_progress | completed | deleted` (official names; `blocked` is never stored).
- `Report {ID, TaskID, Kind ReportKind, Summary, Needs, PR int, Reviews []string ("stage=job"), SHA, Body, Member TaskOwner, CreatedAt}`; `ReportKind`: `ack | progress | question | ready | merged | blocked | done`.
- Limits: `subject` 1–80 chars (runes), no control chars; `done_when` ≤ 10 lines × ≤ 200 chars; `summary` 1–200 chars, one line; `description` / `body` ≤ 32 KiB (a peer message is ≤ 64 KiB, `internal/peers/wire.go:14`); `last_turn_summary` ≤ 200 chars.
- Refusal codes (409, CLI exit 13 — added to `teamRefusalCodes`): `not_lead`, `not_your_member` (existing), `not_member` (a non-member reports or asks `mine`), `task_not_found` (no such id **in the caller's team**), `not_task_owner` (a member touches another's task), `bad_task_transition`, `blocked_by_unknown`, `blocked_by_cycle`. Field errors: 400 `bad_request` (CLI exit 2 before any call when it can tell).

**Routes (T-1b), all under the existing admin-token chain:**

| Route | Who | Answers |
|---|---|---|
| `POST /api/team/tasks` | lead | 201 `Task` (+ `owner.address` for the CLI's message) · 400 · 409 `not_lead` / `not_your_member` / `blocked_by_*` |
| `GET /api/team/tasks?member=<ref>&all=1` | lead: its team; member: its own | 200 `{tasks: []}` (never null) |
| `GET /api/team/tasks/{id}` | lead or owner | 200 `{task, reports: []}` · 409 `task_not_found` |
| `POST /api/team/tasks/{id}/status` `{status}` | lead (any); owner: `in_progress`, `completed` | 200 `Task` · 409 `bad_task_transition` / `not_task_owner` |
| `POST /api/team/tasks/{id}/reassign` `{to}` | lead | 200 `Task` (status back to `pending`) |
| `POST /api/team/reports` | member | 201 `{report, task?, lead: {ref, address}}` (+ replay 200) · 400 · 409 `not_member` / `task_not_found` / `not_task_owner` |
| `GET /api/team/reports?task=<id>&since=<ms>` | lead or the reporting member | 200 `{reports: []}` newest first, ≤ 200 |

Every request carries `origin_inbox`. Transitions: `pending → in_progress → completed`; `pending → completed` allowed (a lead closing); any non-deleted → `deleted` (lead only); `completed`/`deleted` are final except the lead may move `completed → in_progress` (reopen). Report effects (D-3): `ack` → `in_progress` (if `pending`); `ready` → `metadata.prs += pr`; `merged` → `metadata.shas += sha`; `done` → `completed`; every report sets `last_report_*`. Capability `team.tasks.v1` appended to `/api/info` (T-1b).

**Messages (CLI-composed, sent with `POST /api/peers/send` from the caller's inbox; peer wire unchanged):**
- Down (`pdx task add`, `reassign`): `[pdx task <id>] <subject>` / blank / description / `完成定義：` + `- <line>`… / `回報：pdx report ack|progress|ready|done --task <id> --summary "…"（見 pdx-team skill）`.
- Up (`pdx report`): `[report <kind> <task id|->] <summary>` then kind fields one per line (`needs: user`, `pr: #123`, `reviews: R1=…`, `sha: …`) then the body.
- Both prefixes are new literals in `internal/team/wire_task.go`; neither starts with `[pdx team]` or `[pdx-relay` (the skill's notice and the relay tags keep their meaning).

**Last turn (T-3a):** the agent module publishes `TurnEndEvent{SessionID, Text string, At int64}` for every `PdxStop` whose raw event carries a `session_id`, through a hub like `sessionStartHub` (own consumer goroutine; the hook path never waits). The team module subscribes: `ActiveMemberInLiveTeam(sid)` → truncate (first line, ≤ 200 runes, `…` when cut; **raw text only**) → write `last_turn_*` on the member's `in_progress` task (newest `updated_at` if several), else on the member row → `rosterChanged()` if a task changed. Lead and solo sessions: nothing written. At most one write per event; a write is guarded `last_turn_at < ?`.

---

## PR table

| PR | Content | Needs | Est. lines / files | Deploy |
|---|---|---|---|---|
| **T-1a** | `tasks` + `reports` schema, store methods, wire types + validation | main | 650 / 6 | — (with T-1d) |
| **T-1b** | team HTTP routes for tasks and reports, capability | T-1a | 700 / 7 | — (with T-1d) |
| **T-1c** | `pdx task add / ls / show / start / done / delete / reassign`, down message | T-1b | 650 / 5 | — (with T-1d) |
| **T-1d** | `pdx report <kind>` + `pdx report ls`, up message; `pdx team` TASK / LAST | T-1c | 650 / 6 | **daemon + CLI** |
| **T-2** | `pdx spawn --task-subject / --done-when`, `pdx task mine`, skill text | T-1d | 450 / 5 | CLI + `pdx setup` |
| **T-3a** | agent `TurnEnd` hub; team subscriber writes `last_turn` (task or member row) | T-1a | 500 / 7 | daemon |
| **T-3b** | roster `task {id, subject, status}` per member; `rosterChanged()` on task writes | T-1b, γ's roster | 250 / 4 | daemon |

T-3a and T-3b need only T-1a/T-1b and may run in parallel with T-1c…T-2.

---

## PR T-1a — tasks and reports in team.db

**Files.** Create `internal/team/wire_task.go` (+ `_test`); create `internal/module/team/task_store.go` (+ `_test`); modify `internal/module/team/store.go` (run `taskSchema` after `spawnSchema`).

**Interfaces.**
- `ValidTaskSubject(s) error`, `ValidDoneWhen([]string) error`, `ValidReportSummary(s) error`, `ValidateReport(r ReportRequest) error` (per-kind required fields of spec D-3; `needs ∈ {lead,user}` for `question`/`blocked`; `ready` needs `pr > 0` and ≥ 1 `reviews` entry of the form `stage=job`; `merged` needs `pr` and a 7–40 hex `sha`).
- `TaskShortTeam(teamID) string` (first 6 hex chars without dashes, lower case).
- Store: `CreateTask(t TaskRow) (TaskRow, error)` — one tx: `seq = COALESCE(MAX(seq),0)+1 WHERE team_id`, `id = team6-seq`, insert; checks every `blocked_by` exists in the team (`ErrBlockedByUnknown`) and that it closes no cycle (`ErrBlockedByCycle`, DFS over the team's edges). `GetTask(teamID, id)`, `ListTasks(teamID, owner string, all bool)` (all=false hides completed/deleted), `SetTaskStatus(teamID, id, to, by Role) (TaskRow, error)` with the transition table, `ReassignTask(teamID, id, toMember)`, `InsertReport(r ReportRow) (ReportRow, replay bool, error)` (id conflict with an identical row → replay; with a different row → error) — **in the same tx** applies the report's task effect, `ListReports(teamID, taskID, member string, since int64, limit int)`.

**Behaviour rules.**
1. Task ids never repeat in a team (`UNIQUE(team_id, seq)`), even after deletes (no `MAX` over a filtered set).
2. A report and its task effect commit together; a report on a `completed` or `deleted` task is stored but changes no status (`done` twice is harmless).
3. Rows are never deleted; `deleted` is a status (history, spec D-1).

**Tests.** `TestCreateTask_IDsArePerTeamAndMonotonic`; `TestCreateTask_BlockedByUnknownAndCycle`; `TestSetTaskStatus_TransitionTable` (table of from × to × role); `TestInsertReport_EffectsPerKind` (ack/ready/merged/done); `TestInsertReport_ReplayAndConflict`; `TestListTasks_HidesFinishedUnlessAll`; `TestValidateReport_PerKindRequiredFields` (each missing field named in the error); `TestTaskShortTeam`.

**Mutation gates.** Allocate `seq` outside the tx → a concurrent-create test red; drop the cycle check → `…Cycle` red; apply a `done` effect without the tx → a crash-between test (seam) red.

**Size.** ≈ 650 / 6. **Cut point:** reports (`InsertReport`, `ListReports` + tests) → T-1a2.

## PR T-1b — the routes

**Files.** Create `internal/module/team/task_handler.go` (+ `_test`); modify `internal/module/team/module.go` (routes beside `:325-352`); modify `internal/team/wire_task.go` (request/response types); modify `internal/core/info_handler.go` (+ test) to append `team.tasks.v1`.

**Interfaces.** Handlers per the routes table. Caller resolution: `callerTeam` for lead routes (existing `not_lead`); for member routes `ResolveOrigin` → `ActiveMemberInLiveTeam` (none → 409 `not_member`). `to` / `member` accept a ref and resolve through `matchMember` (current ref, then lineage — `team_handler.go:246,299`). Views fill `Owner` from the member row and the origin resolver (address).

**Behaviour rules.** (1) A lead sees and changes only its own team's tasks (`task_not_found` for another team's id, never 403 — same as `not_your_member`). (2) A member reads only its own tasks and reports only on them. (3) `POST /api/team/reports` with no `task_id`: the member's only `in_progress` task; none or several → 400 `bad_request` naming the choice (the CLI asks for `--task`). (4) Log lines `[team] task <id> …` / `[team] report <kind> <task> by <ref>` via `m.logf`.

**Tests.** One test per route for the happy path and each refusal code; `TestTasks_LeadSeesOnlyItsTeam`; `TestReports_DefaultTaskIsTheOnlyInProgress`; `TestTaskOwner_FollowsTheMemberAcrossARelay` (create, run the existing `cleared` report path, `GET` shows the new ref); `TestHandleInfo_Capabilities` updated.

**Mutation gates.** Resolve the task without the team filter → `…OnlyItsTeam` red; key the owner by ref instead of `spawn_op` → `…AcrossARelay` red.

**Size.** ≈ 700 / 7. **Cut point:** report routes → T-1b2.

## PR T-1c — `pdx task`

**Files.** Create `cmd/pdx/task_cmd.go` (+ `_test`); modify `cmd/pdx/main.go` (dispatcher + usage line), `cmd/pdx/team_cmd.go` (`teamRefusalCodes` gains the new codes).

**Interfaces.** `pdx task add --to <ref> --subject <s> [--brief-file <f> | --brief <text>] [--done-when <line>]… [--blocked-by <id>]… [--json]`; `pdx task ls [--member <ref>] [--all] [--json]`; `pdx task show <id> [--json]`; `pdx task start|done|delete <id>`; `pdx task reassign <id> --to <ref>`. Flags in any order (`parseTeamFlags`). Field checks before any call (exit 2, the field named). `add` / `reassign` then send the down message to `owner.address` from the lead's inbox with `client.Once` (never replayed); a failed send → exit 1 with the task JSON on stdout and `pdx task add: task <id> stored, message not sent; send it with pdx msg send <address>`.

**Behaviour rules.** (1) `--brief-file` reuses `readBriefFile` (bounded, FIFO-safe) and the 32 KiB description limit (the whole message must fit 64 KiB with header and done-when). (2) `ls` table: `ID  STATUS  OWNER  SUBJECT  LAST` (`blocked` shown as `pending (blocked)`); `--json` prints the daemon's answer unchanged. (3) Exit codes per `teamReportErr`.

**Tests.** `TestTaskAdd_SendsTheDownMessageOnce` (a fake daemon + a fake send; the exact text); `TestTaskAdd_SendFailureIsExit1WithJSON`; `TestTaskAdd_FieldErrorsAreExit2BeforeAnyCall`; `TestTaskLs_TableAndJSON`; `TestTask_RefusalsAreExit13`; `TestMainUsage_ListsTaskAndReport`.

**Mutation gates.** Send with the replaying client → `…Once` red; check `subject` only on the daemon → `…BeforeAnyCall` red.

**Size.** ≈ 650 / 5. **Cut point:** `reassign` + `delete` → T-1c2.

## PR T-1d — `pdx report` and `pdx team` columns

**Files.** Create `cmd/pdx/report_cmd.go` (+ `_test`); modify `cmd/pdx/main.go`, `cmd/pdx/team_cmd.go` (TASK / LAST columns), `internal/team/wire_team.go` (`Member.Task *MemberTask{ID, Subject, Status}`, `Member.LastAt int64`, both `omitempty`), `internal/module/team/team_handler.go` (`memberView` fills them: the member's `in_progress` task, else its newest `pending`; `LastAt = max(task.last_turn_at, task.last_report_at, member.last_turn_at)`).

**Interfaces.** `pdx report <kind> [--task <id>] --summary "<…>" [--needs lead|user] [--pr <n>] [--reviews <stage>=<job>]… [--sha <sha>] [--file <md> | --text <t>] [--json]`; `pdx report ls [--task <id>] [--since <dur>] [--json]`. The CLI mints the report id (UUID v4), posts, then sends the up message to `lead.address` from the member's inbox (`Once`); a failed send → exit 1 with the report JSON, as `task add`. `pdx team` header becomes `ADDRESS REF TITLE STATE CTX MODEL EFFORT TASK LAST CWD TMUX`; TASK = `<id> <status> <subject…>` (subject cut to 30 runes), LAST = age (`45s`, `12m`, `3h`, `-`).

**Behaviour rules.** (1) Per-kind validation in the CLI mirrors `ValidateReport` (one Go function shared: the CLI imports `internal/team`). (2) A lead running `pdx report` → 409 `not_member` → exit 13. (3) `--since` accepts Go durations.

**Tests.** `TestReport_PerKindRequiredFieldsExit2` (table over the seven kinds); `TestReport_SendsTheUpMessage` (exact text); `TestReport_DefaultTaskAskedWhenAmbiguous`; `TestReport_LeadIsRefused`; `TestTeamTable_TaskAndLastColumns`.

**Mutation gates.** Skip the `ready` reviews check → the table row red; print TASK before CTX → the header test red.

**Size.** ≈ 650 / 6. **Cut point:** `pdx report ls` → T-1d2. **Deploy (T-1 batch):** daemon + CLI. **Acceptance** (mlab, a throwaway lead with one spawned member): `pdx task add` → the member gets the message; `pdx report ack` from the member → the lead gets `[report ack …]`, `pdx task ls` shows `in_progress`; `pdx report ready --pr … --reviews R1=…` with a findings file; `pdx report done` → `completed`; `pdx team` shows TASK / LAST; a relay of the member keeps the task (`pdx task show` owner = new ref).

## PR T-2 — spawn tasks, `pdx task mine`, the skill

**Files.** Modify `cmd/pdx/team_cmd.go` (`--task-subject`, `--done-when`), `cmd/pdx/task_cmd.go` (`mine`), `cmd/pdx/plugin/purdex/skills/pdx-team/SKILL.md`, `cmd/pdx/plugin/embed_test.go`.

**Interfaces.** `pdx spawn … [--task-subject <s> [--done-when <line>]…]`: after the member is up and **before** the brief is sent, the CLI creates task #n for it (`description` = the brief, `subject` = the flag); the brief message then carries the task header between the member prefix and the brief (`[pdx task <id>] <subject>` + done-when + the report line). A task-create failure → the brief is still sent without the header and the CLI exits 1 naming it. `pdx task mine [--all] [--json]` (member only; `not_member` → exit 13).

**Skill (both sections, one line each, appended):** lead — "Hand each piece of work to a member as a task: `pdx task add --to <ref> --subject … --brief-file …` (or `pdx spawn … --task-subject …`); `pdx task ls` and `pdx team` show what each member is on." Member — "Report with `pdx report <kind>` (ack when you start, ready before any merge and wait for the lead's go-ahead, merged, done; question / blocked with `--needs`), not free text; after a relay run `pdx task mine`." The spawn grammar line gains ` [--task-subject <s> [--done-when <line>]…]` **after** the pinned substring.

**Tests.** `TestSpawn_TaskSubjectCreatesTaskBeforeTheBrief` (order of calls; the brief text); `TestSpawn_TaskCreateFailureStillSendsTheBrief`; `TestTaskMine_MemberOnly`; `TestSkill_SaysWhatSpec10Requires` extended (the two lines, `pdx task mine`, the unchanged pinned grammar substring).

**Mutation gates.** Send the brief before creating the task → the order test red; drop the skill line → the skill test red.

**Size.** ≈ 450 / 5. **Deploy:** CLI + `pdx setup --agent cc`.

## PR T-3a — the member's last turn (D-4, daemon side)

**Files.** Modify `internal/module/agent/terminal_sessions.go` (a `turnEndHub` beside `sessionStartHub`, `SubscribeTurnEnd(fn func(TurnEndEvent)) func()`, the interface `TerminalSessions` gains it), `internal/module/agent/handler.go` (publish after the `PdxStop` event is accepted, beside `:658`), create `internal/module/team/last_turn.go` (+ `_test`), modify `internal/module/team/spawn_register.go` (subscribe in `Start`, unsubscribe in `Stop`), `internal/module/team/migrate.go` (`team_members.last_turn_*`), `internal/module/team/task_store.go` (`SetTaskLastTurn`, `SetMemberLastTurn`).

**Behaviour rules.**
1. **The hook path never waits:** publish is the hub's non-blocking enqueue; a full queue drops the event (the next turn overwrites anyway) and counts it.
2. **Raw text only** (88's scope rule): first non-empty line of `last_assistant_message`, whitespace collapsed, ≤ 200 runes, `…` when cut. No model call, no summary.
3. **Members only:** `ActiveMemberInLiveTeam(sid)`; otherwise nothing is written and nothing is logged per turn.
4. **Target:** the member's `in_progress` task with the newest `updated_at`; none → the member row (`team_members.last_turn_*`, kept across relays — `resetMemberUsage` does not touch it).
5. Guarded write `WHERE … AND last_turn_at < ?`; a task write calls `rosterChanged()`.
6. **Known gaps (accepted, M-T1.3):** an Esc-interrupted turn and a lost hook delivery leave the previous `last_turn` in place.

**Tests.** `TestTurnEndHub_DeliversAndNeverBlocksThePublisher` (a stuck subscriber; publish returns); `TestLastTurn_WritesTheMembersInProgressTask`; `TestLastTurn_NoTaskWritesTheMemberRow`; `TestLastTurn_LeadAndSoloWriteNothing`; `TestLastTurn_TruncatesRawTextOnly` (multi-line, long, empty); `TestLastTurn_SubagentStopIsIgnored`; `TestLastTurn_SurvivesARelayOnTheMemberRow`.

**Mutation gates.** Publish synchronously → the never-blocks test red; skip the member check → `…LeadAndSoloWriteNothing` red; reset `last_turn_*` in `resetMemberUsage` → `…SurvivesARelay` red.

**Size.** ≈ 500 / 7. **Cut point:** the member-row fallback → T-3a2. **Deploy:** daemon. **Acceptance:** a throwaway member answers two turns → `pdx task show` / `pdx team` LAST show the second turn's first line; an Esc-interrupted turn leaves it unchanged.

## PR T-3b — the roster's task field (D-7)

**Files.** Modify `internal/team/wire_roster.go` (`RosterMember.Task *RosterTask{ID, Subject, Status}` `omitempty`), `internal/module/team/roster.go` (one `SELECT` per team before the member loop `:82-91`), `internal/module/team/task_handler.go` and `last_turn.go` (`rosterChanged()` after every status / subject / owner change), tests.

**Behaviour rules.** The field is the member's `in_progress` task, else its newest `pending`; absent otherwise. Subject only — never `last_turn` (the Haiku "current task" sentence is a later discussion, spec D-7). Hash-based change detection already suppresses no-op broadcasts.

**Tests.** `TestRoster_MemberCarriesItsCurrentTask`; `TestRoster_ChangedOnTaskStatus`; `TestRoster_NoTaskNoField` (JSON has no `task`).

**Size.** ≈ 250 / 4. **Deploy:** daemon. Tell the interface lead (`mlab/_b84f5i`) and γ when it merges.

---

## Decisions this plan takes (for the coordinator to confirm)

- **D-T1 · Owner key = `spawn_op`, not a ref.** The member row already follows relays in place; a ref would need lineage on every read. The wire still shows the current ref (spec D-1 satisfied in behaviour).
- **D-T2 · Task id = `<first 6 hex of the team UUID>-<seq>`.** The spec's "team short id" does not exist; this is deterministic and needs no new column. Collisions across teams cannot happen inside one team, and lookups are always scoped to the caller's team.
- **D-T3 · The CLI sends the messages, not the daemon.** The daemon has no in-process peer send; spawn's brief already works this way. Spec D-2 says "the daemon … sends" — a deviation with the same user-visible result, and the failure mode spawn already has (task stored, exit 1, send it yourself).
- **D-T4 · Last turn from the `Stop` hook, daemon side; no mod change.** M-T1; confirmed with interface lead 88. Spec §4 lists T-3 as "daemon + mod" and §5 plans a mod file — both drop.
- **D-T5 · `blocks[]` is derived** from the team's `blocked_by` edges (one source of truth); the wire carries both, as the official model.
- **D-T6 · `pdx team` TASK shows the `in_progress` task, else the newest `pending`.** A member with several in progress shows the newest.
- **D-T7 · Reopen:** the lead may move `completed → in_progress`; nothing else leaves a final state.

## Open questions

1. **The relay notice and tasks (spec T-2 "the relay notice lists the member's tasks").** There is no daemon-sent relay notice on main; the new session sees the mod's seed prompt (`internal/team/relay_prompts.go:81-125`, generated into `hooks/prompts.js`). Options: (a) the skill line "after a relay run `pdx task mine`" only (this plan's default — no mod touch); (b) a `{{tasks}}` seed variable (changes the generated mod file and the U21 variable list). **Recommendation: (a).**
2. **Reports without a task.** A member may report with no task (`--task -`)? Default: allowed for `question` / `blocked` only (a member stuck before it has a task); other kinds need a task.
3. **Lead-only `pdx report`?** Spec D-3: "a lead may report to nobody" — read as "a lead does not report"; this plan refuses it (`not_member`). Confirm.
4. **History cap.** `reports` grows forever (≈ hundreds of rows a day at most). Default: no cap in T; revisit with the session-workbook discussion.
</content>
</invoke>
