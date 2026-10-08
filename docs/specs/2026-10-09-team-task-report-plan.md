# Team tasks and reports (T) — Implementation Plan

> **Status (2026-10-09):** revised after one codex round (plan + spec, thread `01a11c69-1b0f`: 2 critical / 13 important / 3 minor). The section **"Codex review of this plan"** at the end records every disposition. Written against origin/main **`532e07bc`** by member β (`mlab/_tyfq9z`) for the coordinator purdex-1f (`mlab/_vqnjx1`).
> **Source:** spec `docs/specs/2026-10-09-team-task-report-spec.md` (T0–T3, D-1…D-7, phases T-1…T-3, M-T1). Research `docs/research/2026-10-08-agent-teams-and-workflow.md` §2.3–§2.5. U24 plan `docs/specs/2026-10-08-unattended-adopt-plan.md` (deviation 5: an adopted member's row key).
> **Format:** the compact format of plan v3 and the U23/U24 plan — contracts, rules, named tests and mutation gates, no full code. The implementer writes the code test-first from these contracts.
> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:subagent-driven-development. Each PR is TDD, one task per commit. Re-verify every `file:line` against main before starting a PR: the resource-lease line (δ), the U24 display line (γ) and the interface line (U1, its lights member `mlab/_l540z1` in `internal/module/agent`) move fast.

**Goal.** A lead hands work to a member as a task the daemon keeps (`pdx task add`), sees what each member is on (`pdx team` TASK / LAST, the roster), and gets structured reports up (`pdx report <kind>`), each also one peer message. A member's turn end records its last answer on its task, silently. Nothing gates a merge (T3).

---

## Global constraints

- Plan v3's constraints apply: ≤ 800 diff lines **or** ≤ 20 files per PR (the coordinator accepts a test-heavy overshoot case by case); Go `go test ./<pkg>/ -count=1`, `-race` on the touched packages one at a time, `gofmt -l`, `make lint`; build `pdx` only with `-o` into a scratch dir (never `go build ./cmd/pdx/` at the root).
- **Resource rules** for every subagent brief: affected tests only while developing; the full vitest once before merge with `--maxWorkers=3` after asking the coordinator; `go test -race` per affected package, one at a time.
- **No SPA change in T.** The roster field (T-3b) is additive; whether the Mac App shows it is the interface lead's call (spec D-7).
- **No mod hook change for the last turn** (M-T1, decision D-T4, **pending the coordinator's / user's confirmation**). The one mod change in T is T-2's seed variable (the relay notice's task list, spec T-2), coordinated with the interface lead (`mlab/_b84f5i`) before `register.js` is touched.
- **`internal/module/agent` is the interface line's lights member's ground** (88, 2026-10-09): T-3a1 adds a new file there, `handler.go` gets **one** call placed after the frame write and outside every emit path, no subscriber runs under `m.mu` or `modMu` (lock order `emitMu → m.mu → modMu`); rebase onto the newest main before the PR, tell 88 before merging.
- **Older peers stay silent.** Every new wire field is `omitempty`; a newer CLI against an older daemon gets a plain 404 → exit 21 (`unsupported`, `cmd/pdx/exitcodes.go`).
- **Text is peer-safe both sides.** Every stored text that later becomes a peer message (subject, description, done-when lines, summary, body) passes `ipeers.ValidateText` (the brief's check, `cmd/pdx/team_cmd.go:224-225`) in the CLI **and** in the daemon (400 `bad_request`), so nothing is stored that cannot be sent.
- **Deploy batches:** T-1 (daemon + CLI) once T-1d merges; T-2 (daemon + CLI + `pdx setup --agent cc`: skill, seed tail); T-3a (daemon) and T-3b (daemon). The coordinator deploys.

## Measurements (plan time)

**M-T1 — the member's last turn text and its cost.**
1. **The daemon already receives it.** Claude Code's settings `Stop` hook (main loop only; subagents raise `SubagentStop`, `internal/agent/cc/events.go:56-64`) is mapped to `PdxStop`, and the provider keeps `last_assistant_message` in the event detail (`internal/agent/cc/status.go:74-82`). The session id is **not** in that detail: it is at the top level of every cc hook payload and read by `IdentifyEvent` → `agent.ExtractSessionIdentity(rawEvent)` (`internal/agent/cc/provider.go:61-65`); `EventRequest.SessionID` is not filled on the tmux path (`internal/module/agent/handler.go:97-103,186-193`). The hook already runs once per main turn for every session with the Purdex hooks: recording it adds **no fork and no mod code**, only an in-process fan-out.
2. **The mod could too** (`turn.complete`'s `e.answer`, "the assistant's final visible text this turn", CC 2.1.294 types `TurnCompleteFields.answer`; main loop = `agentId` absent), but the only mod → daemon channel is the interface line's `mod.sock` stream, whose event types are a closed set without conversation text (`internal/modevents/wire.go:41-59`); the interface lead asked us **not** to add a type (U1-5's content events are item streams, not per-turn summaries).
3. **Known gaps of the hook path** (88, 2026-10-09): (a) a turn interrupted with Esc raises no `Stop` — that turn records nothing; (b) a failed hook delivery is not retried (2 s timeout; turns during a daemon restart are lost). The previous `last_turn` stays. If the coordinator / user does not accept that, the later path is the interface line's conversation model (U1-4 `AgentText` + turn outcome), not a new event type.
4. **Member check:** `Store.ActiveMemberInLiveTeam(sessionID)` (`internal/module/team/team_store.go:158`) on the hook's session id; no role cache in the mod.
5. **Scope rule (88, 2026-10-09):** a separate "session workbook" (Haiku summaries per session) is being planned by the user. T stores the turn's **own words, cut** — no model call, no rewording, no classification.

**Facts the plan rests on** (survey of main, 2026-10-09):
- `team.db` schema constants run by `OpenStore` (`internal/module/team/store.go:46-100`, WAL, `busy_timeout(5000)`); new columns via `ensureColumn` (`migrate.go:29`) from a `migrateX` after `migrateUsage` (`store.go:95`).
- **No team short id exists**: a team id is the approving lead request's UUID v4 (`team_store.go:27-45`).
- `team_members` has `spawn_op TEXT PRIMARY KEY` (`team_store.go:46-47`), `InsertMember` refuses an empty one (`:136-149`). A relay's `cleared` report moves the row's `session_id` / `ref` in place (`moveTeamRoles`, `relay_store_report.go:79-108`) and resets only the usage columns (`resetMemberUsage`, `:75`). **The U24 plan stores an adopted member's row key — the adopt request id — in that same `spawn_op` column** (U24 plan deviation 5; the wire then shows `spawn_op: ""` and `adopt_request`, `internal/team/wire_team.go:180-181`). So the column value is a stable per-membership key for both origins; this plan calls it the **member key**.
- Caller identity: `origin_inbox` → `ResolveOrigin` (trusted, `team_handler.go:41-67`); lead = `callerTeam` → 409 `not_lead`; member = `ActiveMemberInLiveTeam`. `matchMember` scopes to `MembersOf(team)` first, then current ref, then lineage (`team_handler.go:246-261,297-318`).
- **The daemon has no in-process peer send.** Every message today is sent by the CLI with `POST /api/peers/send` from the caller's inbox (`sendBrief`, `cmd/pdx/team_cmd.go:351-365`, `client.Once`: it may have arrived, it is never replayed); a failed brief is exit 1 with the JSON on stdout.
- CLI plumbing: `teamSetup` (`team_cmd.go:128`), `teamReportErr` + `teamRefusalCodes` (`:145`, `:72`; refusals → exit 13), `parseTeamFlags` (`:111`), `pdx team` table (`:462-479`), dispatcher + usage line (`cmd/pdx/main.go:46-96`).
- Spawn: `SpawnRequest` (`internal/team/wire_team.go:187`) carries no brief; the member row is inserted when the member registers (`internal/module/team/spawn_register.go`).
- Roster: `RosterMember` (`internal/team/wire_roster.go:35-40`), built in `buildRoster` (`internal/module/team/roster.go:51-95`); `rosterChanged()` (`roster_publish.go:40`) broadcasts strictly only when the JSON changed.
- Agent fan-out precedent: `sessionStartHub` + `SubscribeSessionStart` (`internal/module/agent/terminal_sessions.go:84-218`; per-session coalescing, overflow re-check, publish under a short mutex) published from the SessionStart grant at `handler.go:647-659`; concurrent hook handling is versioned by `identitySeq` (`handler.go:109-114,205-221`). The team module looks up `agent.TerminalSessionsKey` (`internal/module/team/spawn_register.go:18-36`); its lifecycle is `Start` / `Stop` in `internal/module/team/module.go:354-410`.
- Relay notice: the new session sees the mod's seed prompt — body `DefaultRelayPromptBodies.Seed` and fixed `RelayPromptFixedParts.Seed.Head` (`internal/team/relay_prompts.go:81-88,123-125`), variables `RelayPromptVariables` (`:29`), generated into `cmd/pdx/plugin/purdex/hooks/prompts.js`, composed by `register.js` (`compose`, `:299-310`; seed sent at `:689-700`).
- Skill tests pin exact substrings, including the spawn grammar `pdx spawn [--cwd <dir>] … [--brief-file <f> | --brief <text>]` (`cmd/pdx/plugin/embed_test.go:477`); new spawn flags are appended after it.

---

## Shared contracts

**Tables, in `team.db`:**

```
tasks(                            -- T-1a1
  team_id TEXT NOT NULL, seq INTEGER NOT NULL,          -- PRIMARY KEY (team_id, seq)
  subject TEXT NOT NULL, description TEXT NOT NULL DEFAULT '',
  done_when_json TEXT NOT NULL DEFAULT '[]',
  status TEXT NOT NULL,           -- pending | in_progress | completed | deleted
  owner_key TEXT NOT NULL,        -- team_members.spawn_op value: the member key (spawned or adopted)
  blocked_by_json TEXT NOT NULL DEFAULT '[]',           -- seqs of the same team
  created_by_ref TEXT NOT NULL,   -- the lead's ref at creation
  spawn_op TEXT NOT NULL DEFAULT '',                    -- T-2: the spawn that created it (UNIQUE when not '')
  metadata_json TEXT NOT NULL DEFAULT '{}',             -- {branch?, prs[]?, shas[]?}
  last_report_kind TEXT NOT NULL DEFAULT '', last_report_summary TEXT NOT NULL DEFAULT '', last_report_at INTEGER NOT NULL DEFAULT 0,
  last_turn_summary TEXT NOT NULL DEFAULT '', last_turn_at INTEGER NOT NULL DEFAULT 0, last_turn_seq INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL)
reports(                          -- T-1a2
  id TEXT PRIMARY KEY,            -- UUID v4 from the CLI (idempotency key; --id on a retry)
  team_id TEXT NOT NULL, task_seq INTEGER NOT NULL,     -- always a task (spec D-3: --task defaults to the only in_progress one)
  member_key TEXT NOT NULL, kind TEXT NOT NULL, summary TEXT NOT NULL,
  fields_json TEXT NOT NULL DEFAULT '{}',               -- needs, pr, reviews[], sha
  body TEXT NOT NULL DEFAULT '', created_at INTEGER NOT NULL)
team_members + last_turn_summary, last_turn_at, last_turn_seq  -- T-3a2, ensureColumn; never reset on relay
```

**Task id on the wire:** `<team6>-<seq>` (team6 = first 6 hex chars of the team UUID, no dashes, lower case). It is a **display id**: the daemon parses the `seq` and always looks it up **with the caller's team id**, so two teams with the same prefix never meet (the primary key is `(team_id, seq)`). A malformed id or a prefix that is not the caller's team's → `task_not_found`.

**Wire (`internal/team/wire_task.go`):**
- `Task {ID, TeamID, Subject, Description, DoneWhen []string, Status TaskStatus, Owner TaskOwner{Ref, Address, Title, State}, Blocks []string (derived), BlockedBy []string, Blocked bool (derived: some blocked_by neither completed nor deleted), CreatedBy, CreatedAt, UpdatedAt, Metadata TaskMetadata{Branch string; PRs []int; SHAs []string}, LastReport *TaskReportStamp{Kind, Summary, At}, LastTurn *TaskTurnStamp{Summary, At}}`. `Owner.Ref` is the member's **current** ref, read at view time; `Owner.State` the member row's state (`active`, `killed`, `gone`, `released`).
- `TaskStatus`: `pending | in_progress | completed | deleted` (official names; `blocked` is never stored).
- `Report {ID, Task string, Kind ReportKind, Summary, Needs, PR int, Reviews []string ("stage=job"), SHA, Body, Member TaskOwner, CreatedAt}`; `ReportKind`: `ack | progress | question | ready | merged | blocked | done`.
- Limits: `subject` 1–80 runes, one line; `done_when` ≤ 10 lines × ≤ 200 runes; `summary` 1–200 runes, one line; `description` / `body` ≤ 32 KiB; every text also `ipeers.ValidateText`.
- Refusal codes (409; CLI exit 13 via `teamRefusalCodes`): `not_lead`, `not_your_member` (existing), `not_member` (a non-member reports or asks `mine`), `task_not_found` (no such task **in the caller's scope** — also what another team's id or another member's task answers, so existence never leaks), `not_task_owner` (a member changes a task it can see but does not own — only reachable by a lead-only verb), `bad_task_transition`, `blocked_by_unknown`, `blocked_by_cycle`, `owner_not_active` (assigning to a member that is not active). Field errors: 400 `bad_request` (the CLI exits 2 before any call when it can tell).

**Routes (T-1b), admin-token chain, every request carries `origin_inbox`:**

| Route | Who | Answers |
|---|---|---|
| `POST /api/team/tasks` | lead | 201 `Task` · 400 · 409 `not_lead` / `not_your_member` / `owner_not_active` / `blocked_by_*` |
| `GET /api/team/tasks?member=<ref>&all=1` | lead: its team; member: its own | 200 `{tasks: []}` (never null) |
| `GET /api/team/tasks/{id}` | lead (its team) or owner | 200 `{task, reports: []}` · 409 `task_not_found` |
| `POST /api/team/tasks/{id}/status` `{status}` | lead: any allowed; owner: `in_progress`, `completed` | 200 `Task` · 409 `bad_task_transition` / `task_not_found` |
| `POST /api/team/tasks/{id}/reassign` `{to}` | lead | 200 `Task` (status back to `pending`) · 409 `owner_not_active` |
| `POST /api/team/reports` | member, own task | 201 `{report, task, lead: {ref, address}}` · 200 replay (same id, same content) · 400 · 409 `not_member` / `task_not_found` |
| `GET /api/team/reports?task=<id>&since=<ms>` | lead (its team) or the task's owner | 200 `{reports: []}` newest first, ≤ 200 |

Transitions: `pending → in_progress → completed`; `pending → completed` (a lead closing); any non-deleted → `deleted` (lead only); `completed → in_progress` (lead reopen, decision D-T7); nothing else leaves `completed` / `deleted`. Report effects (spec D-3), **in the report's insert transaction**: `ack` → `in_progress` (from `pending`); `ready` → `metadata.prs += pr`; `merged` → `metadata.shas += sha`; `done` → `completed`; every report sets `last_report_*`. A report on a `completed` / `deleted` task is stored and changes no status. `POST /api/team/reports` with no task: the member's only `in_progress` task; none or several → 400 naming the choice. Capability `team.tasks.v1` appended to `/api/info` (T-1b1).

**Messages (CLI-composed, `POST /api/peers/send` from the caller's inbox with `client.Once`; peer wire unchanged):**
- Down (`pdx task add`, `reassign`): `[pdx task <id>] <subject>` / blank / description / `完成定義：` + `- <line>`… / `回報：pdx report ack|progress|ready|done --task <id> --summary "…"（見 pdx-team skill）`.
- Up (`pdx report`): `[report <kind> <task id>] <summary>` then kind fields one per line (`needs: user`, `pr: #123`, `reviews: R1=…`, `sha: …`) then the body.
- New literals in `internal/team/wire_task.go`; neither starts with `[pdx team]` or `[pdx-relay`.
- **A send that fails** (refused, timed out — it may have arrived — or daemon unavailable): the record stays stored, the CLI exits 1, prints the record's JSON on stdout and on stderr the exact command to send it by hand (`pdx msg send <address> "$(pdx task show <id> --message)"` / `pdx report show <rid> --message`). `pdx report … --id <rid>` retries with the same id (a replay stores nothing new and applies no effect twice) and sends again.

**Last turn (T-3a):**
- Agent side (T-3a1): `TurnEndEvent{SessionID string; Text string; At int64; Seq int64}`. `At` and `Seq` are stamped **at handler entry** (`At` = receipt time, `Seq` = the handler's arrival counter, the one `identitySeq` uses or one like it), so an earlier `Stop` that finishes later still carries the earlier stamp. Published for a `PdxStop` of the cc provider that was accepted (verify and frame apply succeeded), whose raw event yields a session id through `IdentifyEvent`; never for `PdxSubagentStop`, another provider, a rejected or degraded event. The hub is a **fixed-capacity channel per subscriber**: publish never waits for a subscriber or the DB (a full channel drops the event and counts it; the next turn overwrites anyway).
- Team side (T-3a2): `ActiveMemberInLiveTeam(sid)` → text rule → write on the member's `in_progress` task (newest `updated_at`, then highest `seq`), else on the member row, guarded `WHERE … AND (last_turn_at, last_turn_seq) < (?, ?)` → `rosterChanged()` when a task changed. Lead and solo sessions: nothing written.
- **Text rule (spec D-4, the turn's own words only):** take the first non-empty line, collapse runs of whitespace; if a sentence ends within 200 runes (`。！？` or `.!?` followed by a space or the end), cut there (first sentence preferred); otherwise cut at 200 runes and add `…`. Empty text writes nothing.

---

## PR table

| PR | Content | Needs | Est. lines / files | Deploy |
|---|---|---|---|---|
| **T-1a1** | `tasks` schema, store (create with seq, transitions, blocked-by), wire types + validation | main | 600 / 5 | — (T-1 batch) |
| **T-1a2** | `reports` schema, report insert with its task effect in one tx, replay | T-1a1 | 450 / 4 | — (T-1 batch) |
| **T-1b1** | task routes, capability | T-1a1 | 650 / 7 | — (T-1 batch) |
| **T-1b2** | report routes | T-1a2, T-1b1 | 450 / 4 | — (T-1 batch) |
| **T-1c** | `pdx task add / ls / show / start / done / delete / reassign`, down message | T-1b1 | 700 / 5 | — (T-1 batch) |
| **T-1d** | `pdx report <kind>` + `ls` + `show`, up message; `pdx team` TASK / LAST | T-1b2, T-1c | 700 / 6 | **daemon + CLI** |
| **T-2** | spawn creates task #1 in the daemon; `pdx task mine`; seed tail `{{tasks}}`; skill | T-1d | 700 / 10 | daemon + CLI + `pdx setup` |
| **T-3a1** | agent `TurnEnd` hub (new file) + one publish call | main | 450 / 5 | daemon |
| **T-3a2** | team subscriber: `last_turn` on the task or the member row | T-1a1, T-3a1 | 500 / 6 | daemon |
| **T-3b** | roster `task {id, subject, status}`; `rosterChanged()` on task writes | T-1b1, γ's roster | 250 / 4 | daemon |

T-3a1 needs only main and may start any time (tell 88 first); T-3a2 / T-3b run in parallel with T-1c…T-2.

---

## PR T-1a1 — tasks in team.db

**Files.** Create `internal/team/wire_task.go` (+ `_test`); create `internal/module/team/task_store.go` (+ `_test`); modify `internal/module/team/store.go` (run `taskSchema` after `spawnSchema`).

**Interfaces.**
- `ValidTaskSubject(s) error`, `ValidDoneWhen([]string) error` (both include `ipeers.ValidateText`), `ParseTaskID(id, teamID) (seq int, ok bool)`, `TaskDisplayID(teamID, seq) string`.
- Store: `CreateTask(t TaskRow) (TaskRow, error)` — **`BEGIN IMMEDIATE`** (the write lock first, so two creates never read the same `MAX(seq)`; if the driver cannot, a bounded retry of 3 on the primary-key conflict), checks every `blocked_by` exists in the team (`ErrBlockedByUnknown`) and closes no cycle (`ErrBlockedByCycle`, DFS over the team's edges), checks the owner row is `active` (`ErrOwnerNotActive`). `GetTask(teamID, seq)`, `ListTasks(teamID, ownerKey string, all bool)` (all=false hides completed/deleted; order `updated_at DESC, seq DESC`), `SetTaskStatus(teamID, seq, to, by Role)`, `ReassignTask(teamID, seq, toKey)`.

**Behaviour rules.** (1) `seq` never repeats in a team, deletes included. (2) Rows are never deleted; `deleted` is a status. (3) The transition table above, one function, used by routes and reports.

**Tests.** `TestCreateTask_SeqIsPerTeamAndMonotonic`; `TestCreateTask_TwoConnectionsNeverShareASeq` (a file-backed DB, two `*sql.DB`, a barrier right after the read); `TestCreateTask_SamePrefixTeamsDoNotCollide` (two team ids sharing their first 6 hex chars); `TestCreateTask_BlockedByUnknownAndCycle`; `TestCreateTask_OwnerNotActive`; `TestSetTaskStatus_TransitionTable` (from × to × role); `TestListTasks_HidesFinishedUnlessAll`; `TestValidTaskSubject_PeerSafeText` (control chars, invalid UTF-8, 81 runes).

**Mutation gates.** A deferred transaction for the seq → the two-connection test red; drop the cycle check → `…Cycle` red; key the primary key on the display id → `…SamePrefix…` red.

**Size.** ≈ 600 / 5. **Cut point:** blocked-by (validation, cycle, derived `blocks`) → T-1a1b.

## PR T-1a2 — reports in team.db

**Files.** Modify `internal/team/wire_task.go` (`ValidateReport`), `internal/module/team/task_store.go` (`reportSchema`, `InsertReport`, `ListReports`, `GetReport`) and their tests.

**Interfaces.** `ValidateReport(r ReportRequest) error` — per-kind required fields of spec D-3: `question` / `blocked` need `needs ∈ {lead, user}`; `ready` needs `pr > 0` and ≥ 1 `reviews` entry `stage=job`; `merged` needs `pr > 0` and a 7–40 hex `sha`; `summary` and `body` peer-safe. `InsertReport(r ReportRow) (ReportRow, TaskRow, replay bool, error)` — one tx: id conflict with an identical row → replay (no effect); with a different row → `ErrReportIDReused`; else insert + the task effect + `last_report_*`.

**Tests.** `TestValidateReport_PerKindRequiredFields` (each missing field named); `TestInsertReport_EffectsPerKind`; `TestInsertReport_ReplayAppliesNothingTwice`; `TestInsertReport_EffectAndRowCommitTogether` (a seam fails after the insert → neither the row nor the status changed); `TestInsertReport_FinishedTaskStoresNoStatusChange`.

**Mutation gates.** Apply the effect outside the tx → `…CommitTogether` red; apply the effect on a replay → `…NothingTwice` red.

**Size.** ≈ 450 / 4.

## PR T-1b1 — task routes

**Files.** Create `internal/module/team/task_handler.go` (+ `_test`); modify `internal/module/team/module.go` (routes beside `:325-352`); modify `internal/team/wire_task.go` (requests / responses); modify `internal/core/info_handler.go` (+ test) to append `team.tasks.v1`.

**Interfaces.** Lead routes: `callerTeam` (existing `not_lead`), targets through `matchMember` (team-scoped, then current ref, then lineage). Member routes: `ResolveOrigin` → `ActiveMemberInLiveTeam` (none → `not_member`); a member sees only tasks whose `owner_key` is its member key. Views fill `Owner` from the member row (any state) and the origin resolver (address).

**Behaviour rules.** (1) Every lookup carries the caller's team id; another team's id, a malformed id, or (for a member) another member's task all answer `task_not_found`. (2) A lead may not assign or reassign to a non-active member (`owner_not_active`); tasks of a killed / gone / released member stay, visible to the lead, reassignable. (3) Log lines `[team] task <id> <verb> by <ref>` via `m.logf`.

**Tests.** Per route: happy path and each refusal. **Isolation matrix** `TestTasks_Isolation` (table): another team's lead knows the id; a member reads / changes another member's task; a member of team A with team B's id; a reassigned-away member; the owner after a relay asked by its **old** ref (lead side, lineage) and acting from its **new** session; a killed member's tasks listed for the lead; a released member's tasks. `TestTaskOwner_FollowsTheMemberAcrossARelay` (create, run the real `cleared` report path, `GET` shows the new ref); `TestHandleInfo_Capabilities` updated.

**Mutation gates.** Look up a task without the team id → the isolation matrix red; key ownership by ref → `…AcrossARelay` red; answer a member's foreign task with 403 → the matrix red (existence leaks).

**Size.** ≈ 650 / 7. **Cut point:** `reassign` + the isolation matrix's reassign rows → T-1b1b.

## PR T-1b2 — report routes

**Files.** Modify `internal/module/team/task_handler.go` (+ test), `internal/module/team/module.go` (routes).

**Behaviour rules.** (1) Member only; a lead → `not_member`. (2) The task must be the member's own (`task_not_found` otherwise). (3) No task given → the only `in_progress` one; none or several → 400 naming the choice. (4) The answer carries the lead's current ref and address (`rosterLead`, `roster.go:103`). (5) `GET /api/team/reports`: lead (its team) or the task's owner.

**Tests.** `TestReports_DefaultTaskIsTheOnlyInProgress`; `TestReports_LeadIsRefused`; `TestReports_ForeignTaskIsNotFound`; `TestReports_ReplayAnswers200`; `TestReports_ListScopedToTeamAndOwner`.

**Size.** ≈ 450 / 4.

## PR T-1c — `pdx task`

**Files.** Create `cmd/pdx/task_cmd.go` (+ `_test`); modify `cmd/pdx/main.go` (dispatcher + usage line), `cmd/pdx/team_cmd.go` (`teamRefusalCodes` gains the new codes).

**Interfaces.** `pdx task add --to <ref> --subject <s> [--brief-file <f> | --brief <text>] [--done-when <line>]… [--blocked-by <id>]… [--json]`; `pdx task ls [--member <ref>] [--all] [--json]`; `pdx task show <id> [--json | --message]`; `pdx task start|done|delete <id>`; `pdx task reassign <id> --to <ref>`. Flags in any order (`parseTeamFlags`); field checks before any call (exit 2, the field named; the same Go validators the daemon uses). `add` / `reassign` then send the down message (Messages contract, failure rule included).

**Behaviour rules.** (1) `--brief-file` reuses `readBriefFile` (bounded, FIFO-safe); the whole down message must fit `ipeers.MaxTextBytes`. (2) `ls` table: `ID  STATUS  OWNER  SUBJECT  LAST` (`blocked` shown as `pending (blocked)`, a non-active owner as `<ref> (gone)`); `--json` prints the daemon's answer unchanged. (3) `show --message` prints the down message the task would send (for a manual send).

**Tests.** `TestTaskAdd_SendsTheDownMessageOnce` (fake daemon + fake send; exact text); `TestTaskAdd_SendFailures` (refused / timeout / daemon unavailable → exit 1, JSON on stdout, the manual command on stderr); `TestTaskReassign_SendFailure`; `TestTaskAdd_FieldErrorsAreExit2BeforeAnyCall`; `TestTaskLs_TableAndJSON`; `TestTask_RefusalsAreExit13`; `TestMainUsage_ListsTaskAndReport`.

**Mutation gates.** Send with the replaying client → `…Once` red; check `subject` only in the daemon → `…BeforeAnyCall` red.

**Size.** ≈ 700 / 5. **Cut point:** `reassign` + `delete` → T-1c2.

## PR T-1d — `pdx report` and `pdx team` columns

**Files.** Create `cmd/pdx/report_cmd.go` (+ `_test`); modify `cmd/pdx/main.go`, `cmd/pdx/team_cmd.go` (TASK / LAST), `internal/team/wire_team.go` (`Member.Task *MemberTask{ID, Subject, Status}`, `Member.LastAt int64`, both `omitempty`), `internal/module/team/team_handler.go` (`memberView` fills them: the member's `in_progress` task, newest first, else its newest `pending`; `LastAt = max(task.last_turn_at, task.last_report_at, member.last_turn_at)`).

**Interfaces.** `pdx report <kind> [--task <id>] --summary "<…>" [--needs lead|user] [--pr <n>] [--reviews <stage>=<job>]… [--sha <sha>] [--file <md> | --text <t>] [--id <rid>] [--json]`; `pdx report ls [--task <id>] [--since <dur>] [--json]`; `pdx report show <rid> [--message]`. The CLI mints the report id unless `--id`, posts, then sends the up message to `lead.address` (Messages contract). `pdx team` header becomes `ADDRESS REF TITLE STATE CTX MODEL EFFORT TASK LAST CWD TMUX`; TASK = `<id> <status> <subject…>` (subject cut to 30 runes), LAST = age (`45s`, `12m`, `3h`, `-`).

**Behaviour rules.** (1) Per-kind validation in the CLI is the daemon's `ValidateReport` (shared Go). (2) A lead running `pdx report` → `not_member` → exit 13. (3) `--since` takes Go durations.

**Tests.** `TestReport_PerKindRequiredFieldsExit2` (table over the seven kinds); `TestReport_SendsTheUpMessage` (exact text); `TestReport_SendFailures` (as T-1c, plus the `--id` retry stores nothing new and sends again); `TestReport_DefaultTaskAskedWhenAmbiguous`; `TestReport_LeadIsRefused`; `TestTeamTable_TaskAndLastColumns`.

**Mutation gates.** Skip the `ready` reviews check → the table row red; mint a new id on `--id` → the retry test red.

**Size.** ≈ 700 / 6. **Cut point:** `pdx report ls` / `show` → T-1d2. **Deploy (T-1 batch):** daemon + CLI. **Acceptance** (mlab, a throwaway lead with one spawned member): `pdx task add` → the member gets the message; `pdx report ack` → the lead gets `[report ack …]`, `pdx task ls` shows `in_progress`; `pdx report ready --pr … --reviews R1=…` with a findings file; `pdx report done` → `completed`; `pdx team` shows TASK / LAST; a relay of the member keeps the task (`pdx task show` owner = new ref); a send failure (lead session closed) prints the manual command.

## PR T-2 — spawn task #1, `pdx task mine`, the relay notice, the skill

**Files.** Modify `internal/team/wire_team.go` (`SpawnRequest.Task *SpawnTask{Subject, Description, DoneWhen}` `omitempty`; `SpawnOp.TaskID` `omitempty`), `internal/module/team/spawn_store.go` / `spawn_register.go` (store the spawn's task on the spawn op; **create the task in the member-insert transaction**), `cmd/pdx/team_cmd.go` (`--task-subject`, `--done-when`; the brief header), `cmd/pdx/task_cmd.go` (`mine`), `internal/team/relay_prompts.go` (seed fixed **Tail** `{{tasks}}`, variable `tasks`) + regenerated `cmd/pdx/plugin/purdex/hooks/prompts.js`, `cmd/pdx/plugin/purdex/hooks/register.js` (one line: fill `tasks` at seed time), `cmd/pdx/plugin/purdex/skills/pdx-team/SKILL.md`, `cmd/pdx/plugin/embed_test.go`, tests.

**Interfaces.**
- `pdx spawn … [--task-subject <s> [--done-when <line>]…]`: the brief becomes the task's description; the spawn request carries it; the daemon creates task #n **in the transaction that inserts the member row** (so a member never exists without its task), keyed `UNIQUE(spawn_op)` (a spawn replay creates nothing twice); `SpawnOp.TaskID` returns its display id. The brief message then carries the task header between the member prefix and the brief (`[pdx task <id>] <subject>` + done-when + the report line). Without `--task-subject` nothing changes.
- `pdx task mine [--all] [--json | --seed]` (member only; `not_member` → exit 13). `--seed` prints the relay notice lines (`你手上的任務：` + `- <id> <status> <subject>`…) or nothing when the session has none.
- **Relay notice (spec T-2):** the seed prompt's new fixed Tail is `{{tasks}}`; at seed time the mod runs `pdx task mine --seed` once (one fork per relay, bounded 8 s like `whoami`) and fills it; empty → the Tail is omitted. A fixed part, not the editable body, so a user-edited seed (U21) still lists the tasks.

**Skill (appended, one line each):** lead section — "Hand each piece of work to a member as a task: `pdx task add --to <ref> --subject … --brief-file …` (or `pdx spawn … --task-subject …`); `pdx task ls` and `pdx team` show what each member is on." Member section — "Report with `pdx report <kind>` (`ack` when you start, `ready` before any merge and wait for the lead's go-ahead, `merged`, `done`; `question` / `blocked` with `--needs`), not free text; after a relay, the notice lists your tasks (`pdx task mine`)." The spawn grammar line gains ` [--task-subject <s> [--done-when <line>]…]` **after** the pinned substring.

**Coordination.** Tell the interface lead (`mlab/_b84f5i`) before touching `register.js` (δ and the team-interface work also add lines there).

**Tests.** `TestSpawn_TaskCreatedWithTheMemberRow` (a seam between insert and commit: neither exists; after: both); `TestSpawn_ReplayCreatesNoSecondTask`; `TestSpawn_BriefCarriesTheTaskHeader` (exact text); `TestSpawn_WithoutTaskSubjectIsUnchanged`; `TestTaskMine_MemberOnlyAndSeedFormat`; `TestRelayPrompts_SeedTailHoldsTasks` (Go) and the plugin test for the seed (filled / omitted); `TestHooks_NoEventRegisteredTwiceWithoutMatcher` still green; `TestSkill_SaysWhatSpec10Requires` extended (both lines, the unchanged pinned grammar).

**Mutation gates.** Create the task after the member commit → `…WithTheMemberRow` red; put `{{tasks}}` in the editable body → a custom-body seed test red; drop the skill line → the skill test red.

**Size.** ≈ 700 / 10. **Cut point:** the relay-notice half (`relay_prompts.go`, `prompts.js`, `register.js`, `--seed`) → T-2b, same deploy. **Deploy:** daemon + CLI + `pdx setup --agent cc`.

## PR T-3a1 — the agent `TurnEnd` hub

**Files.** Create `internal/module/agent/turn_end_hub.go` (+ `_test`); modify `internal/module/agent/terminal_sessions.go` (interface `TerminalSessions` gains `SubscribeTurnEnd`), `internal/module/agent/handler.go` (**one** call, after the frame write, outside the emit path, no lock held), `internal/module/agent/module.go` (the hub field), and every fake of `TerminalSessions` in other packages' tests.

**Behaviour rules.** (1) Stamp `At` / `Seq` at handler entry (Last turn contract). (2) Publish only an accepted cc `PdxStop` with a session id from `IdentifyEvent`. (3) Per-subscriber fixed-capacity channel (64); publish = non-blocking send; full → drop + an atomic counter; subscriber callbacks run on the subscriber's goroutine; unsubscribe closes it; publish after unsubscribe is a no-op. (4) No subscriber is called with `emitMu`, `m.mu` or `modMu` held.

**Tests.** `TestTurnEndHub_PublishNeverWaits` (a stuck subscriber; 1000 publishes return promptly; the drop counter counts); `TestTurnEndHub_UnsubscribeRace` (`-race`); `TestHandler_PublishesTurnEndForAcceptedStop` (a real Stop raw payload from `testdata`); `…_NotForSubagentStop`, `…_NotWithoutSessionID`, `…_NotForARejectedEvent`, `…_NotForAnotherProvider`; `TestHandler_TurnEndStampedAtEntry` (two Stops finishing out of order keep their entry order).

**Mutation gates.** Blocking send → `…NeverWaits` red; stamp at publish → `…StampedAtEntry` red; read the session id from `result.Detail` → `…AcceptedStop` red.

**Size.** ≈ 450 / 5. **Coordination:** rebase onto the newest main first; tell 88 before merging (the lights member rebases after). **Deploy:** daemon (no behaviour change until T-3a2).

## PR T-3a2 — the member's last turn (D-4, daemon side)

**Files.** Create `internal/module/team/last_turn.go` (+ `_test`); modify `internal/module/team/module.go` (subscribe in `Start`, unsubscribe in `Stop`; the `TerminalSessions` lookup gains the method), `internal/module/team/migrate.go` (`team_members.last_turn_*`), `internal/module/team/task_store.go` (`SetTaskLastTurn`, `SetMemberLastTurn`), the team module's registry fakes.

**Behaviour rules.** (1) Members only (`ActiveMemberInLiveTeam`); otherwise nothing written, nothing logged per turn. (2) The text rule (Last turn contract). (3) Target: the member's `in_progress` task (newest `updated_at`, then highest `seq`), else the member row — kept across relays (`resetMemberUsage` does not touch it). (4) Guarded write on `(last_turn_at, last_turn_seq)`. (5) A task write calls `rosterChanged()`. (6) The accepted gaps (M-T1.3) leave the previous value.

**Tests.** `TestLastTurn_WritesTheMembersInProgressTask`; `TestLastTurn_TieBreakNewestThenSeq`; `TestLastTurn_NoTaskWritesTheMemberRow`; `TestLastTurn_LeadAndSoloWriteNothing`; `TestLastTurn_TextRule` (table: one line, first sentence within 200 runes — CJK and Latin, two sentences on one line, no sentence end, multi-line, 300 runes, whitespace only); `TestLastTurn_OlderEventNeverOverwritesNewer`; `TestLastTurn_SurvivesARelayOnTheMemberRow`.

**Mutation gates.** Skip the member check → `…LeadAndSoloWriteNothing` red; guard on `at` only → `…OlderEventNeverOverwritesNewer` red (same-ms stamps); reset `last_turn_*` in `resetMemberUsage` → `…SurvivesARelay` red.

**Size.** ≈ 500 / 6. **Deploy:** daemon. **Acceptance:** a throwaway member answers two turns → `pdx task show` / `pdx team` LAST show the second turn's first sentence; an Esc-interrupted turn leaves it unchanged.

## PR T-3b — the roster's task field (D-7)

**Files.** Modify `internal/team/wire_roster.go` (`RosterMember.Task *RosterTask{ID, Subject, Status}` `omitempty`), `internal/module/team/roster.go` (one `SELECT` per team before the member loop `:82-91`), `internal/module/team/task_handler.go` and `last_turn.go` (`rosterChanged()` after every status / subject / owner change), tests.

**Behaviour rules.** The member's `in_progress` task (same tie-break), else its newest `pending`; absent otherwise. Subject only — never `last_turn` (spec D-7). Hash-based change detection already suppresses no-op broadcasts.

**Tests.** `TestRoster_MemberCarriesItsCurrentTask`; `TestRoster_ChangedOnTaskStatus`; `TestRoster_NoTaskNoField` (JSON has no `task`).

**Size.** ≈ 250 / 4. **Deploy:** daemon. Tell the interface lead and γ when it merges.

---

## Decisions for the coordinator

- **D-T1 · The owner is the member key** (`team_members.spawn_op` value: the spawn op, or for an adopted member the adopt request id — U24 plan deviation 5), not a ref. The row already follows relays in place; the wire shows the current ref.
- **D-T2 · The task id is a display id `<team6>-<seq>`; the key is `(team_id, seq)`.** Lookups always carry the caller's team id.
- **D-T3 · The CLI sends the messages, not the daemon** (spec D-2 / D-3 say "the daemon … sends"). The daemon has no in-process peer send; spawn's brief already works this way. Same user-visible result; the failure mode is spawn's (record stored, exit 1, the manual command printed). **Needs confirmation (spec wording).**
- **D-T4 · The last turn comes from the settings `Stop` hook, daemon side** (spec T2 / D-4 say "the mod"). No fork, no mod hook; interface lead 88 confirmed the hook stays and asked for no new mod event type. Accepted gaps: an Esc-interrupted turn and a lost hook delivery leave the previous value. **Needs the coordinator's — and, since T2 names the mod, the user's — confirmation.** If refused, T-3a1 / T-3a2 are replaced by a mod `turn.complete` hook (`e.answer`) plus a write channel the interface lead must define.
- **D-T5 · `blocks[]` is derived** from the team's `blocked_by` edges.
- **D-T6 · One current task per member for display** (`pdx team` TASK, roster, last-turn target): the `in_progress` task with the newest `updated_at`, then the highest `seq`; else the newest `pending`.
- **D-T7 · Reopen:** the lead may move `completed → in_progress`; nothing else leaves a final state. Not in the spec; harmless; drop it if unwanted.
- **D-T8 · Spawn task #1 is created by the daemon in the member-insert transaction** (spec D-2 "a spawn's brief becomes task #1"), so a member never exists without it.

## Open questions

1. **History cap.** `reports` grows forever (≈ hundreds of rows a day at most). Default: no cap in T; revisit with the session-workbook discussion.

---

## Codex review of this plan

One round, plan + spec (thread `01a11c69-1b0f-7251-b377-bebc5dd870ff`): 2 critical / 13 important / 3 minor.

| # | Severity · confidence | Finding | Disposition |
|---|---|---|---|
| 1 | critical · 1.00 | The owner key `spawn_op` cannot hold an adopted member (D-U24-2 "no spawn op"; wire `SpawnOp=""`). | **Rebutted with evidence, contract clarified:** the U24 plan stores an adopted member's row key (the adopt request id) in the `spawn_op` **column** (deviation 5); the wire field is what shows `""`. The plan now calls the column value the member key (`owner_key`) and cites deviation 5. |
| 2 | critical · 1.00 | `<team6>-<seq>` as a table-wide primary key collides across teams. | **Adopted:** key `(team_id, seq)`; the display id is parsed and looked up with the caller's team; a same-prefix test and a mutation gate. |
| 3 | important · 1.00 | The relay notice must list the member's tasks (spec T-2); the open question defaulted to a skill line. | **Adopted:** a fixed seed Tail `{{tasks}}` filled by the mod with `pdx task mine --seed` (T-2; cut point T-2b). |
| 4 | important · 0.98 | D-T4 (Stop hook instead of the mod) deviates from the user decision T2 and accepts data loss. | **Escalated:** D-T4 marked as needing the coordinator's and the user's confirmation, with the fallback named. |
| 5 | important · 0.97 | The session id is not in `PdxStop`'s detail; read it from the raw event. | **Adopted:** `IdentifyEvent` on the raw event; tests for a real payload, no id, another provider, a rejected event. |
| 6 | important · 0.91 | An earlier Stop finishing later can overwrite a newer turn. | **Adopted:** stamp `At` + `Seq` at handler entry; guard on both; an out-of-order test. |
| 7 | important · 0.88 | "Never blocks" is not what the existing hub does; the publish point is the SessionStart site. | **Adopted:** a per-subscriber fixed channel in a new file, one call after the frame write outside emit (88's placement rule), drop counter, race tests. |
| 8 | important · 0.99 | The text rule ignores "first sentence preferred". | **Adopted:** first sentence within 200 runes, else 200 + `…`; a table test with CJK. |
| 9 | important · 0.97 | A failed task create still sends the brief; spec wants the brief to be task #1. | **Adopted:** the daemon creates the task in the member-insert transaction (D-T8); idempotent per spawn op. |
| 10 | important · 0.96 | Message partial failure: no contract for reports, no retry without a duplicate. | **Adopted:** one failure contract for every send (record kept, exit 1, manual command), `pdx report --id` replays, tests for refused / timeout / unavailable. |
| 11 | important · 0.94 | `MAX(seq)+1` races under a deferred SQLite transaction. | **Adopted:** `BEGIN IMMEDIATE` (or a bounded retry on the key conflict) and a two-connection barrier test. |
| 12 | important · 0.90 | Reports without a task widen the spec. | **Adopted:** a report always has a task; the open question is withdrawn. |
| 13 | important · 0.95 | No isolation matrix (adopted, relayed, released, reassigned, other team). | **Adopted:** `TestTasks_Isolation` in T-1b1; every foreign task answers `task_not_found`. |
| 14 | important · 0.92 | Stored text is not checked as peer-safe. | **Adopted:** `ipeers.ValidateText` in the CLI and the daemon (Global constraints). |
| 15 | important · 0.93 | T-3a's files and size are incomplete. | **Adopted:** split into T-3a1 (agent hub, 5 files) and T-3a2 (team consumer, 6 files) with the module lifecycle and fakes listed. |
| 16 | minor · 0.86 | T-1a's estimate is optimistic. | **Adopted:** pre-split T-1a1 / T-1a2 (and T-1b1 / T-1b2 to match). |
| 17 | minor · 1.00 | Stale references; stray tags at the end of the file. | **Adopted:** references corrected (lifecycle in `module.go`, the SessionStart publish site), the stray tags removed. |
| 18 | minor · 0.89 | Reopen and the multi-in-progress choice are unconfirmed and untied. | **Adopted:** listed as D-T6 / D-T7 for confirmation; tie-break `updated_at DESC, seq DESC` with a test. |
