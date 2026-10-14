# Workbook journal v4 — plan, daemon (J-0a … J-1e)

Spec: `docs/specs/2026-10-14-workbook-journal-spec.md` (v4, all of §3 decided). Prompt: `docs/specs/2026-10-14-workbook-journal-prompt-v3.md` (being measured, J-P). The Mac half (J-2) gets its own plan once J-1e's API is settled; iOS (J-3) later.
Facts read from origin/main on 2026-10-14 (paths from `internal/` unless stated); lead notes `journal-v4-facts-2026-10-14.md`.

## 1. Facts

- **Seat per turn**: the workbook already reads `team.SeatReader` once per turn-end and stores `entry.TeamID`, `entry.Role` (`lead | member | member_remote | none`) (`module/workbook/intake.go:112-117`, `engine.go:47`). Journal job = `lead` or `none`; `member*` stays v2.
- **Prompt**: `prompt.go:12` `PromptVersion = 2`, `:18` `//go:embed prompts/system-v2.txt`; a shipped prompt file is never edited, a new version adds a file. The job's system block is set in `queue.go:384` (`Cache: true`); `buildComplete` `queue.go:361` (haiku, 4096, low). The mod refuses `max_tokens` > 4096 (`cmd/pdx/plugin/purdex/hooks/workbook.js:26`). Model text ≤ 64 KiB (`validate.go:20`).
- **Input** `input.go:20-33` (`TurnSource`, `turnInput`), `BuildTurnInput` `:56` (everything through `redact.String`); per-job todo numbers `numberTodos` `:104`.
- **Output**: `validate.go:36` `Summary` (7 keys, `ParseModelJSON` `:102` requires exactly them, ignores extras), `Repair` `:235`. `Engine.apply` `outcome.go:6`: skip → `FinishSkippedV2` (`todos.go:274`); else `SetPushLineV2` (`todos.go:234`, entry still pending, todo changes applied, `res.Changed`) → `finishOK` (`outcome.go:67`) or a re-write job → `applyRewrite` (`:75`) → `finishCut` (`queue.go:205`). `Store.Finish` is the only pending → ok step.
- **Todos** `wb_todos` (`migrate.go:170-182`): global `id`, `conv_key`, `title`, `detail`, `state`, `added_entry_id`, `closed_entry_id`, …; no per-conversation number.
- **No project / cwd concept** in the workbook. A live session's cwd: `peers.OriginResolver.ResolveOriginBySession` → `team.Origin.Cwd` (`team/wire.go:69`). The daemon never runs `git rev-parse`; its only git use is `module/dev` (`exec` `git log`, 3 s timeout). **No daemon code pushes**: credentials from the daemon's launch context are unverified (T-1d.0 measures it).
- **Team feed** (J-0a, PR #2835, branch `feat/journal-j0a-team-feed`): `team.JournalFeed{TasksChangedAfter (rewind 10 s / 1 h, dedupe), ReportsAfter (exact rowid), TeamInfo}` — reviewed by codex R1 + R2, not yet merged.
- API / events / capability patterns: `module/workbook/api.go:152`, `announce` `api.go:441`, `core/info_handler.go:106-108`, device allowlist `cmd/pdx/device_scope.go`.

## 2. Design choices (for the reviewer)

- **Git without touching any checkout**: the journal branch is written with plumbing against the project's common git dir — a private index (`GIT_INDEX_FILE`) over the daemon's files folder, `git write-tree`, `git commit-tree -p <tip>`, `git update-ref refs/heads/purdex/journal <new> <old>` (compare-and-set) — so no worktree is registered in the repository and no HEAD moves. Then `git push origin purdex/journal`.
- **Files live in the daemon's folder** `<DataDir>/journal/<project id>/files/.purdex/…` (0700 dirs, 0600 files); `<project id>` = first 16 hex of sha256(common git dir). The lead agent edits `roadmap.md` there (`pdx journal path` prints it).
- **One writer per file in the daemon** (a per-project mutex); the roadmap's stages are the agent's, the daemon only rewrites 狀態 cells it can match.
- **Markdown is the source of truth**; the API parses it (cache by mtime). Entry ids are stable (`wb:<entry id>:<n>`, `task:<team>:<seq>:new|done`, `todo:<conv>:<seq>:new|done`).
- **Solo task numbers**: a per-conversation `seq` column on `wb_todos` (migration with backfill by `id` within each conversation).

## 3. Phases and order

| PR | content | depends on | est. lines |
|---|---|---|---|
| J-0a | #2835 team feed — finish (the last `-race` run, PR body, ready) | — | (done, ~600) |
| J-1a | `internal/journal`: project resolution, entry model, markdown writer + parser, roadmap table parse / status update | — | ~650 |
| J-1b | workbook: prompt v3 file + version, journal input fields, output parse (`events`, `confirm_done`, `roadmap`), apply into J-1a; `wb_todos.seq`; solo task entries | J-1a, J-P | ~750 |
| J-1c | team task mirror: `tasks.md` + `task_new` / `task_done` from `team.JournalFeed` | J-0a, J-1a | ~450 |
| J-1d | git: branch commit / push loop, `pdx journal path` | J-1a | ~500 |
| J-1e | API, pin, `journal.changed`, `journal.v1`, device routes | J-1a…J-1c | ~500 |

Every PR: TDD, one commit per task, `go test -race` only for the touched packages, `make lint`; never the full `go test ./...` (1f's at release), never vitest. Each test group names a mutation. codex R1 → R2 attacker → critic. J-1b and J-1c touch the workbook / team readers → 1f reviews.

## 4. J-1a — `internal/journal` (pure package, no daemon wiring)

**T-1a.1 Project**: `ProjectOf(ctx, cwd) (Project{ID, CommonDir, Root}, error)`: `git -C <cwd> rev-parse --path-format=absolute --git-common-dir` (3 s timeout, `exec.CommandContext`, no shell); `Root` = the common dir's parent for a normal repo; not a repo → `Project{ID: hash(cwd), CommonDir: "", Root: cwd}` (no git). Cached per cwd for 10 min. Tests on temp repos: a main checkout and a linked worktree answer the same ID (M: use `--show-toplevel` → two IDs); a non-repo; a timeout.
**T-1a.2 Model** (`entry.go`): `Entry{ID, Kind, At, Actor, Text, Refs []string, Items []Item{Text, Done}, Detail, Pinned}`; kinds `request(by) | task_new | task_done | exec | exec_done | comm | confirm`; Chinese labels from spec §5.1.
**T-1a.3 Writer / parser** (`markdown.go`): the spec §8 block format — `## <date>` dividers, `### HH:MM · <label> · <actor>[ 📌]`, body, checklist, `<details>`, `<!-- j id=… kind=… by=… refs=… pinned=… -->`. `Append(file, entries)`, `Tick(file, id, itemIdx)`, `SetPinned(file, id, bool)` rewrite in place (atomic temp + rename); `Parse(file) []Entry` tolerant of hand edits (a block without a valid `<!-- j -->` comment is skipped, logged once). Month file by `At`. Tests: round trip of every kind incl. 待確認 with ticks and a `<details>` containing `###` and `<!--` text (escaped; M: no escaping → the parse splits the block); append keeps earlier bytes; tick / pin change only their block; a hand-edited heading keeps the entry (id from the comment).
**T-1a.4 Tasks file** (`tasks.go`): `tasks.md` — a table per state (進行中 / 未開始 / 完成 / 放棄), rows `#<n>` / id, title, owner, refs; written whole from a task list; parsed back for the API.
**T-1a.5 Roadmap** (`roadmap.go`): parse `## <feature>` + table 階段 | 說明 | 狀態; `SetStatus(feature, stage, status) (changed bool)` rewrites only that cell; unknown feature / stage / status → no change; a table it cannot parse → the file is untouched and an error is returned. Tests: a cell change keeps every other byte (M: re-render the table → spacing changes); an unknown status refused.

## 5. J-1b — the workbook writes the journal

**T-1b.1 Prompt v3**: `prompts/system-v3.txt` = v2 text + the journal block (prompt doc §1, as measured by J-P), `PromptVersion` stays per job: journal jobs record 3, member jobs 2. The system block is chosen per job from the entry's role.
**T-1b.2 Input**: journal jobs add `unit_role`, `open_confirms` (the unit's unticked 待確認 items, numbered per job, ≤ 30, map kept in the lease like todos), `roadmap_rows` (the project's roadmap rows, ≤ 40; features the unit has touched first). The unit: `role == lead` → the team (`team_id`), `none` → the conversation.
**T-1b.3 Output**: `ParseModelJSON` accepts the 7 v2 keys plus optional `events`, `confirm_done`, `roadmap` on journal jobs (still exactly 7 required; the three extras validated if present; a wrong type → format failure as v2). Validation per spec §9.
**T-1b.4 Apply**: after `Store.Finish` succeeds (`finishOK` / `finishCut`) and for skipped entries after `FinishSkippedV2`: the exec entry (`exec`, or `exec_done` when `thing_done`; none when skipped), the events, the ticks (`confirm_done` through the job's map; all ticked → unpinned), the roadmap statuses, and the solo task entries (T-1b.5) — one journal write per job, in the order: tasks, exec, events. A journal write failure is logged and never fails the workbook entry.
**T-1b.5 Solo tasks**: migration adds `wb_todos.seq INTEGER NOT NULL DEFAULT 0` and backfills `seq` per `conv_key` by `id`; new todos get `MAX(seq)+1` in their conversation inside the insert's transaction. A solo unit's todo add → `task_new` 「#seq title」, done → `task_done`; dropped → no entry; `tasks.md` rewritten from the conversation's todos.
- Tests: a lead turn and a solo turn get the v3 system block, a member turn v2 (M: v3 for all); the extras are optional and validated (M: require them → v2 output fails); a confirm event then a `confirm_done` ticks the right item and unpins when complete; a skipped turn still writes its events; an exec_done replaces exec; the migration on a v2 DB backfills `seq` 1..n per conversation (M: global numbering); a journal write error leaves the entry `ok`.

## 6. J-1c — team task mirror

`ingest_tasks.go` in the workbook module: every 5 s, `ReportsAfter` then `TasksChangedAfter` (rewound per the feed's contract), for teams whose lead is on this host: task first seen → `task_new` 「#<seq> <subject>」 (id `task:<team>:<seq>:new`, actor the owner's title), `completed` or a `merged` report → `task_done` (id `…:done`, text + the report summary when there is one); `tasks.md` of the team rewritten on any change (state, owner, refs from subject + `MetadataPRs` + report PR). Cursors in `workbook.db` (`wb_journal_cursors`).
- Tests: created → one task_new; merged then completed, and completed then merged → one task_done with the summary (M: two ids); a re-read row makes no second entry (M: no dedupe); a lead relay keeps the same team file; a team led on another host is skipped.

## 7. J-1d — git

**T-1d.0 Measure first** (in the PR body, from the daemon's real launch context on mlab and air26): `git ls-remote origin` and `git push --dry-run origin <scratch ref>` run by the daemon process (a temporary dev-only endpoint or a test binary started the same way) — does the daemon have working credentials (ssh key / agent / helper)? If not, stop and report: the push half waits for a decision; commits still land locally.
**T-1d.1 Commit loop**: per project with files changed since the last commit: under the project mutex, `GIT_INDEX_FILE=<project dir>/index git --git-dir=<common dir> --work-tree=<files dir> add -A .purdex`, `write-tree`, `commit-tree -p <tip of refs/heads/purdex/journal or none> -m "journal: <n> entries"` (author `Purdex journal <journal@purdex.local>`), `update-ref refs/heads/purdex/journal <new> <old>`; every 10 minutes; a project without git (`CommonDir == ""`) has files only.
**T-1d.2 Push**: after a commit, `git --git-dir=<common dir> push origin refs/heads/purdex/journal:refs/heads/purdex/journal` (60 s timeout); failure → logged with the remote's message, retried next round, never blocks writing. **Push only for projects in the daemon config list `journal.push_projects`** (default: empty → commits stay local) — see §9.
**T-1d.3 First use**: when the remote has `purdex/journal` and the local ref does not, fetch it first and start from its tip (its files copied into the files folder); otherwise an orphan first commit.
**T-1d.4 `pdx journal path`**: prints the files folder of the current directory's project (for the lead agent to edit `roadmap.md`).
- Tests on temp repos with a bare remote: the commit lands on `purdex/journal` and the checkout's HEAD, index and working tree are untouched (M: plain `git commit` → HEAD moves); a concurrent `update-ref` race loses cleanly and retries (M: no old value → a commit is lost); push failure retried; first use adopts the remote branch.

## 8. J-1e — API

- `GET /api/journal?session_id=…&before=<month>:<id>&limit=50` → spec §10 shape; a member resolves to its team; `404` when the session's unit has no file and no team.
- `GET /api/journal/tasks?session_id=…`, `GET /api/journal/roadmap?session_id=…`, `PUT /api/journal/pin {session_id, entry_id, pinned}`.
- `journal.changed {unit, project, what}` after every write; `journal.v1` when ready; device routes allowed.
- Tests: scope (member → team), paging across months, pin round trip, 404, the frame per write, device token reaches the GETs and the PUT.

## 9. Needs the user (before J-1d's push lands)

`git push` from the daemon goes to every project's remote — company repositories on `lab.protype.tw` included — the moment a session there writes a journal. Proposed: **commits always local; push only for projects the user lists** (start with `wake/purdex`). To be asked before J-1d is dispatched.
