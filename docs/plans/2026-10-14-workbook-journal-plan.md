# Workbook journal v4 — plan, daemon (J-0a … J-1e)

Spec: `docs/specs/2026-10-14-workbook-journal-spec.md` (v4, all of §3 decided). Prompt: `docs/specs/2026-10-14-workbook-journal-prompt-v3.md` (being measured, J-P). The Mac half (J-2) gets its own plan once J-1e's API is settled; iOS (J-3) later.
Facts read from origin/main on 2026-10-14 (paths from `internal/` unless stated); lead notes `journal-v4-facts-2026-10-14.md`.
Revision 2 (2026-10-14): codex plan review `task-mv7ugqpb-75vrid`, 16 findings, all taken — §10.

## 1. Facts

- **Seat per turn**: the workbook reads `team.SeatReader` once per turn-end and stores `entry.TeamID`, `entry.Role` (`lead | member | member_remote | none`) (`module/workbook/intake.go:112-117`, `engine.go:47`); `entry.PromptVer` is set from the global `PromptVersion` **before** the seat is read (`intake.go:109`). Journal job = `lead` or `none`.
- **Prompt**: `prompt.go:10-18` (`PromptVersion = 2`, `//go:embed prompts/system-v2.txt`, the prompt files written once by `WritePromptFiles` `prompt.go:57`); a shipped prompt file is never edited. System block set in `queue.go:384`; `buildComplete` `queue.go:361`. The mod refuses `max_tokens` > 4096 (`cmd/pdx/plugin/purdex/hooks/workbook.js:26`).
- **Output path**: `validate.go:36` `Summary`, `ParseModelJSON` `:102` (exactly 7 keys required, extra top-level keys ignored, duplicate members refused `:53`). `Engine.apply` `outcome.go:6`: skip → `FinishSkippedV2` (`todos.go:274`); else `SetPushLineV2` (`todos.go:234`: push line, status and todo changes in one transaction; its `TodoResult` is only logged today) → `finishOK` (`outcome.go:67`) or a re-write job (`outcome.go:48-63`; the `job` struct `queue.go:93` carries `entry`, v2 `Output`, usage, latency) → `applyRewrite` → `finishCut` (`queue.go:204`). `finishRow` (`settle.go:6`) drops `Store.Finish`'s bool; a failed finish becomes an orphan later set `failed/store` (`settle.go:25`). `Store.Finish` (`store.go:212`) is the only pending → ok/failed step.
- **Todos** `wb_todos` (`migrate.go:170-182`), inserted in `todos.go:205`; no per-conversation number.
- **No project / cwd concept** in the workbook; a live session's cwd: `peers.OriginResolver.ResolveOriginBySession` → `team.Origin.Cwd` (`team/wire.go:69`). The daemon's only git use is `module/dev` (`git log`, 3 s timeout); **no daemon code pushes** — credentials from the daemon's launch context are unverified (T-1d.0).
- **Team feed** (J-0a, PR #2835): `team.JournalFeed{TasksChangedAfter (not gap-free: rewind 10 s / 1 h and dedupe, journalfeed.go:49), ReportsAfter (exact rowid, :58), TeamInfo}`.
- Migrations by position (`migrate.go:13`), several openers supported; API / events / capability: `module/workbook/api.go:152`, `announce` `:441`, `core/info_handler.go:106-108`, `cmd/pdx/device_scope.go`.

## 2. Design choices (for the reviewer)

- **Durable outbox, then files.** The journal never writes files on the result path. The transaction that applies a journal job's result (`SetPushLineV2` / `FinishSkippedV2`) also inserts one `wb_journal_outbox` row carrying everything the job produced — events, confirm ticks, roadmap changes, the todo changes (`TodoResult`) — so a re-write job, a crash or an orphan retry loses nothing. A drainer writes a row to the files **once its entry is no longer pending**: `ok` → with the exec / exec_done entry (the final entry text); `skipped` / `failed` → without it. The markdown writer is idempotent by entry id, so a drain repeated after a crash appends nothing twice. Team task changes go through the same outbox (cursor advanced in the same transaction as their outbox rows).
- **A registered git worktree owned by the daemon** (spec §3 Q-A, §8): `git worktree add` of `purdex/journal` at `<DataDir>/journal/<project id>/wt`. Git itself refuses a second checkout of that branch anywhere else, so the branch never moves under another checkout; commits are ordinary commits in that worktree; the main checkout and the agents' worktrees are untouched. The lead agent edits `roadmap.md` there (`pdx journal path`).
- **Machine data in the metadata, a rendering for people**: each entry's data is a JSON object in its `<!-- j {…} -->` comment (HTML-escaped by `encoding/json`, and every `--` written as `--`, so the comment can never close early); the visible heading, text, checklist and `<details>` are a rendering of that JSON, regenerated whenever the entry changes. The parser reads only the JSON; hand edits of the visible text are not read back (spec §8 is amended to say so). Text is rendered safely (a line that would start a heading, list or comment is prefixed with `\`; `detail` goes in a fence longer than any backtick run in it).
- **Unit registry**: `wb_journal_units(unit_key PK, kind team|solo, project_id, wt_path, label, lead_conv, created_at)` is written the first time a unit writes, so the API resolves a unit (and its project) even after its lead relayed or the team ended; a team's project is its **lead's** cwd, never a member's.
- **Solo task numbers**: `wb_todos.seq` per conversation, `UNIQUE(conv_key, seq)` for `seq > 0`.

## 3. Phases and order

| PR | content | depends on | est. lines |
|---|---|---|---|
| J-0a | #2835 team feed — finish (last `-race` run, PR body, ready) | — | (done) |
| J-1a | `internal/journal`: project resolution, entry model, JSON-in-comment markdown writer / parser, tasks file, roadmap table | — | ~700 |
| J-1b1 | workbook: prompt v3 per entry, journal input, output parse + validation | J-1a, J-P | ~600 |
| J-1b2 | workbook: outbox (schema, insert in the result transactions, drainer), `wb_journal_units`, `wb_todos.seq`, solo task entries | J-1b1 | ~700 |
| J-1c | team task mirror through the outbox (`wb_journal_cursors`) | J-0a, J-1b2 | ~450 |
| J-1d | git worktree, commit / push loop, `journal.push_projects`, `pdx journal path` | J-1a | ~550 |
| J-1e | API, pin, `journal.changed`, `journal.v1`, device routes | J-1b2, J-1c | ~500 |

Every PR: TDD, one commit per task, `go test -race` only for the touched packages, `make lint`; never the full `go test ./...` (1f's at release), never vitest. Each test group names a mutation. codex R1 → R2 attacker → critic. J-1b* and J-1c touch the workbook result path and the team feed → 1f reviews.

## 4. J-1a — `internal/journal` (pure package)

**T-1a.1 Project** `ProjectOf(ctx, cwd)`: `git -C <cwd> rev-parse --path-format=absolute --git-common-dir` (3 s, `exec.CommandContext`, no shell) → `Project{ID (first 16 hex of sha256 of the common dir), CommonDir, Root}`; not a repo → `{ID: hash(cwd), CommonDir: "", Root: cwd}`. Cached per cwd for 10 min. Tests on temp repos: main checkout and a linked worktree → same ID (M: `--show-toplevel` → two IDs); non-repo; timeout.
**T-1a.2 Model** `Entry{ID, Kind, By (user|leader|member, request only), At, Actor, Text, Refs, Items []{Text, Done}, Detail, Pinned}`; kinds `request | task_new | task_done | exec | exec_done | comm | confirm`; labels from spec §5.1 (`request` → 「<by 的中文>請求」).
**T-1a.3 Writer / parser** (`markdown.go`): `## <date>` dividers; per entry the rendering + `<!-- j {json} -->`. Operations: `Upsert(file, entry)` (append if the id is new, else re-render that block), `Tick(file, id, idx)`, `SetPinned(file, id, bool)`, `FillText(file, id, text)` (only if empty) — atomic temp + rename; month file by `At`. `Parse(file)` reads the JSON comments only; a comment that does not parse is skipped and logged once with its line.
- Tests: round trip of every kind with hostile text — newlines, `-->`, `<!-- j {`, `###`, `- [ ]`, backtick runs, `=` and spaces in refs (M: no `--` escaping → the next parse loses the block); upsert of an existing id changes only its block and appends nothing (M: append always → a duplicate after a re-drain); tick / pin / fill change only their block; a hand-edited visible line does not change the parsed entry.
**T-1a.4 Tasks file** `tasks.md`: a table per state (進行中 / 未開始 / 完成 / 放棄), rows `#<n>` or id, title, owner, refs; written whole; parsed back from per-row JSON comments.
**T-1a.5 Roadmap** parse `## <feature>` + table 階段 | 說明 | 狀態; `SetStatus(feature, stage, status) (changed bool)` rewrites only that cell; unknown feature / stage / status → no change; an unparsable table → untouched, error. Tests: a cell change keeps every other byte (M: re-render → spacing changes); unknown status refused.

## 5. J-1b — the workbook writes the journal

**J-1b1**
- **T-1b1.1 Prompt per entry**: `prompts/system-v3.txt` (v2 + the measured journal block); intake reads the seat **first**, then sets `entry.PromptVer` = 3 for `lead` / `none`, 2 for `member*`; refresh entries keep their own version (`refresh.go`). `buildComplete` picks the system text by the entry's `PromptVer`; `WritePromptFiles` writes both versions. Tests: lead and solo entries record 3 and send v3, member records 2 and sends v2, refresh unchanged (M: global version → member records 3).
- **T-1b1.2 Input**: journal jobs add `unit_role`, `self_name` (the session's virtual name), `open_confirms` (the unit's unticked 待確認 items as `{n, asked, idx, text}` per the prompt doc §2, ≤ 30; the `n → (entry id, item)` map kept in the lease beside the todo map), `roadmap_rows` (the unit's project roadmap, ≤ 40, features the unit touched first). Read from the unit's files through `internal/journal`; an unreadable file → empty lists, logged.
- **T-1b1.3 Output**: `ParseModelJSON` keeps the 7 required keys; on journal jobs `events`, `confirm_done`, `roadmap` are optional and parsed strictly: a present field of the wrong type, a `null` array item, a non-object item, or a duplicate key inside an item → format failure (retry once, as v2); an item with an unknown `kind` or missing `text` / `feature` / `stage` / `status` → that item dropped and logged. **Code limits (spec §9 is the authority; §5.2 is amended to match)**: `request` / `comm` text ≤ 80 (cut at the last punctuation; the prompt asks ≤ 60), `confirm` `items` ≤ 10 × ≤ 30, `detail` cut by the daemon from `assistant_text` at `detail_start` (prompt doc §2; not found → the last 1 500 chars), scrubbed, ≤ 4 000, `confirm_done` unknown numbers ignored, roadmap status outside the five values dropped.
- Tests: the extras absent → v2 behaviour unchanged (M: require them); each malformed shape above → its outcome (M: lenient struct unmarshal → a zero-value event is written); boundary lengths at 80 / 81, 30 / 31, 10 / 11 items.

**J-1b2**
- **T-1b2.1 Schema** (one migration step): `wb_journal_outbox(id INTEGER PK, entry_id INTEGER, unit_key TEXT, kind TEXT, payload TEXT, created_at, done_at INTEGER NOT NULL DEFAULT 0)`, index on `done_at = 0`; `wb_journal_units` (§2); `wb_journal_cursors(name PK, value)`; `wb_todos.seq INTEGER NOT NULL DEFAULT 0` + backfill per `conv_key` by `id` + `CREATE UNIQUE INDEX wb_todos_conv_seq ON wb_todos(conv_key, seq) WHERE seq > 0`.
- **T-1b2.2 Insert**: `SetPushLineV2` and `FinishSkippedV2`, for journal jobs, insert the outbox row (events, ticks, roadmap, the `TodoResult` of this transaction) in their transaction; new todos get `seq = MAX(seq)+1` for their conversation in the same transaction (the store's write transaction serialises openers).
- **T-1b2.3 Drainer**: a goroutine (and a pass at start) takes undone rows whose entry is not `pending`; per row, under the unit's project mutex: resolve / register the unit (`wb_journal_units`; a team unit's project from the lead's cwd at first write), write in order — solo task entries (`task_new` 「#seq title」 for adds, `task_done` for dones; dropped → none; `tasks.md` rewritten from the conversation's todos), the exec / exec_done entry when the entry is `ok`, the events, the ticks (all ticked → unpinned), the roadmap statuses — then `done_at`. A file error leaves the row undone (retried with backoff, logged once per hour); the workbook entry is never affected.
- Tests: a crash between finish and drain (row left undone) is written on the next pass, once (M: no outbox → lost); a re-write turn writes its events after the re-write finishes, with the final entry text (M: drain while pending → the pre-rewrite text); an orphan finish that later succeeds still drains; a `failed/store` entry drains its events without an exec entry; a skipped turn writes its events and ticks; concurrent todo inserts in one conversation get distinct `seq` (M: `MAX+1` outside the transaction → a unique violation); the migration backfills `seq` 1..n per conversation on a v2 DB.

## 6. J-1c — team task mirror

`ingest_tasks.go`: every 5 s for teams whose lead is on this host, `ReportsAfter` then `TasksChangedAfter` (rewound per the feed's contract); per page, in **one transaction**: the outbox rows (task_new / task_done / a tasks-file refresh) and the cursor update. First start: no entries for the past, but `tasks.md` mirrors the team's open tasks and those completed in the last 30 days.
- Dedupe: entry ids `task:<team>:<seq>:new` / `:done` — the writer's upsert makes a re-read row harmless; the outbox row is only inserted when the task's state differs from what `wb_journal_units`' last mirror recorded (`wb_journal_cursors` holds the per-team last state map as JSON).
- **Summary arriving late**: completed first → `task_done` written with the subject only; a later `merged` / `done` report on that task → `FillText` (only when the closing line is empty). Merged first → `task_done` with the summary; the later completion finds the id and writes nothing.
- Tests: created → one task_new; merged → completed and completed → merged each end with one task_done carrying the summary (M: append-only → no summary in the second order); a rewound re-read writes no second entry and no second outbox row (M: no state check → duplicate outbox rows); the cursor and the outbox rows commit together (M: cursor first → a crash loses the page); a lead relay keeps the team's unit; a team led on another host is skipped.

## 7. J-1d — git

- **T-1d.0 Measure first** (PR body; from the daemon's real launch context on mlab and air26): `git ls-remote origin` and `git push --dry-run origin refs/heads/purdex/journal` run by the daemon process (a dev-only endpoint or a test binary started the same way). No working credentials → the push half stops and waits for a decision; commits still land locally.
- **T-1d.1 Worktree**: first write of a project with git: if `refs/remotes/origin/purdex/journal` exists (after `git fetch origin purdex/journal`, 30 s) → `git worktree add --track -b purdex/journal <wt> origin/purdex/journal`; else `git worktree add --orphan -b purdex/journal <wt>` (git ≥ 2.42; checked once, else an error logged and files-only mode). An existing registration whose folder is gone → `git worktree prune` then add. A project without git → files only, no commits.
- **T-1d.2 Commit loop**: every 10 minutes per project with changes: `git -C <wt> add -A .purdex` + `git -C <wt> commit -m "journal: <n> entries"` with author / committer `Purdex journal <journal@purdex.local>` (env), under the project mutex.
- **T-1d.3 Push** (only for projects allowed by `journal.push_projects`, T-1d.4): `git -C <wt> push origin purdex/journal` (60 s). Rejected as non-fast-forward → `git -C <wt> pull --rebase origin purdex/journal`; a conflict → `git rebase --abort`, local commits kept, retried next round, logged once per hour. Never force.
- **T-1d.4 Config** `[journal] push_projects = []` in the daemon config: entries are `host/owner/repo` matched against the normalised `remote.origin.url` (ssh and https forms); read at every push round (a change applies without restart); default empty → nothing is pushed. Tests: empty → no push (M: default allow), a matching entry pushes, ssh / https forms match, a reload takes effect.
- **T-1d.5 `pdx journal path`**: prints the worktree's `.purdex` folder for the current directory's project.
- Tests on temp repos with a bare remote: the commit lands on `purdex/journal`, the main checkout's HEAD / index / tree untouched (M: commit in the main checkout); a second `git worktree add` of the branch elsewhere is refused by git (regression); first use adopts a remote branch with its files; a non-fast-forward is rebased and pushed; a rebase conflict aborts and keeps local commits.

## 8. J-1e — API

- `GET /api/journal?session_id=…&before=<month>:<id>&limit=50`: unit from the session — `lead` / `none` → its own unit; `member` → its team's unit (project from `wb_journal_units`, i.e. the lead's); `member_remote` → `409 {lead_host}` (the App asks the lead's host); an ended team → the unit stays readable; no unit yet → `404`.
- `GET /api/journal/tasks?session_id=…`, `GET /api/journal/roadmap?session_id=…`, `PUT /api/journal/pin {session_id, entry_id, pinned}`.
- `journal.changed {unit, project, what}` after every drained write; `journal.v1` when ready; device routes allowed.
- Tests: member with a different cwd than its lead reads the lead's project (M: member's cwd → wrong file); member_remote → 409 with the lead host; after a lead relay and after the team ended the unit still resolves; paging across months; pin round trip; 404; the frame per write; device token reaches the GETs and the PUT.

## 9. Needs the user (before J-1d's push)

`journal.push_projects` default empty means no project pushes until the user lists one; asked: start with `github.com/wake/purdex`?

## 10. Review log

codex `task-mv7ugqpb-75vrid` (16): 1, 2 worktree → registered worktree (§2, T-1d.1); 3, 4, 5 durable outbox drained after finish, carrying the re-write turn's data (§2, J-1b2); 6 prompt version per entry (T-1b1.1); 7 `By` field (T-1a.2); 8 JSON metadata + safe rendering (§2, T-1a.3); 9 late summary → `FillText` (§6); 10 cursor + outbox in one transaction, state-based dedupe (§6, T-1b2.1); 11 unique `seq` in the insert transaction (T-1b2.1–2); 12 unit registry, lead's project, member_remote (§2, §8); 13 adoption via `worktree add --track` (T-1d.1); 14 config task (T-1d.4); 15 strict nested parsing tests (T-1b1.3); 16 code limits fixed, spec §5.2 amended (T-1b1.3).
