# Conversation entity P4 — ended and gone conversations — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Settings → Worker lists every conversation that has ended (terminal or worker) in 已退出 with rebuild, and every conversation whose transcript is gone in a new 已消失 tab. The source is a daemon conversation index fed by a scan of `~/.claude/projects`.

**Architecture:**
- **Daemon.**
  - A `conversation_index` table in `meta.db` keeps one row per top-level transcript ever seen.
  - A new package `internal/conversations` lists the projects root. For each changed transcript it reads the head forward to the first human prompt (at most 16 MB, resumable, kept once found) and the last 256 KB.
  - The nex module joins the index with live terminal frames (a new `LiveSessions` on the agent module's `TerminalSessions`) and every Nexen execution (in-process `List`, archived included, no page cap). It decides running / ended / gone, and serves `GET /api/nex/conversations?state=ended|gone` from a 5 s single-flight snapshot that also runs at start and every 6 h.
- **SPA.**
  - 已退出 reads the endpoint.
  - 已消失 is a new registry tab with disabled rows.
  - Rebuild from a terminal-last row opens a closed-terminal pane (`TerminatedPane`) seeded for S. Rebuild from a worker-last row opens the latest stint's exited screen, as P2-2 does.

**Tech Stack:** Go 1.26 (net/http, modernc sqlite, embedded Nexen v0.17.0); React 19, Zustand 5, Vitest 4 + Testing Library, Tailwind 4, Phosphor Icons.

**Spec:** `docs/specs/2026-10-06-conversation-entity-spec.md` **§13** (user decisions U1–U3, rules §13.2–§13.6, tests §13.9, phases §13.10). Also binding: §4.2 / D1 (who owns S), §7 (the rebuild screen), §9 (the tab registry), D9 (entity key), D11 (`worker-rebuild`).

## Global Constraints

- Worktree: `/Users/wake/Workspace/wake/purdex/.claude/worktrees/conv-entity`. Prefix **every** Bash command with `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/conv-entity && ` (Go) or `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/conv-entity/spa && ` (SPA). Edit/Write paths are absolute and include `.claude/worktrees/conv-entity/`.
- Shell rules enforced in this worktree: no `eval` anywhere in a command; no `$(...)`, loops or variables in the same command as `git`; `sed`/`xargs` arguments must be literal.
- One commit per task, `git add <own files>`; messages `feat(daemon): …` / `feat(spa): …`; trailer `Co-Authored-By: Claude <model> <noreply@anthropic.com>` naming the implementer's model.
- Go: `go build ./...`, `go vet ./...`, `gofmt -l <changed files>` (must print nothing), `go test ./internal/conversations/... ./internal/store/... ./internal/module/nex/... ./internal/module/agent/...`, full `go test ./...` before the PR. Known flake: `TestConsumeSignals_GraceWindowDrop_RearmsAfterTeardown` (#1581) passes alone.
- SPA: `npx vitest run <file>` while iterating; full `npx vitest run`, `pnpm run lint`, `npx tsc --noEmit -p tsconfig.app.json` before committing. `@testing-library/user-event` is not installed: use `fireEvent`. Known flakes: `HookModuleCard.test.tsx`, `MemoryMonitorPage.test.tsx`, one retry-timing test.
- Nexen stays at **v0.17.0**. The nex package imports only the Nexen packages `imports_test.go` allows; the scanner lives in `internal/conversations`, which imports no Nexen package.
- Constants (§13.6 as revised 2026-10-07): head scan **until the first human prompt, at most 16 MB** (`16 << 20`), tail window **256 KB** (`256 << 10`), rescan interval **6 h**, request reuse **5 s**, row cap **2,000** per state, recent-write warning **120 s**.
- Transcripts are append-only. Head fields (cwd, first entrypoint, first prompt), once found, never change; the head is re-read from byte 0 only when the file **shrank** or its **inode changed** (rewritten). A head scan that has not finished (no prompt yet, below 16 MB) **resumes from the byte where it stopped**; it never re-reads from 0 on growth.
- Root: `filepath.Join(os.UserHomeDir(), ".claude", "projects")`. `CLAUDE_CONFIG_DIR` is not honoured. The root itself and slug directories may be symlinks (followed); a transcript that is a symlink or not a regular file is skipped.
- A transcript is `<root>/<slug>/<session>.jsonl` where `<session>` is a lowercase UUID (`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`). Nested files (`<session>/subagents/…`) are not conversations.
- Session ids are compared case-insensitively and stored lowercase (P3b-1 review rule).
- "Live" is `isLiveExecution` (Go) / `isLiveRow` (TS) only. "For S": `session_id == S || resume_session_id == S` (`executionIsFor`).
- User-visible copy in `spa/src/locales/en.json` **and** `zh-TW.json` (`locale-completeness.test.ts`). zh-TW strings are given verbatim below; en is a faithful translation. Icons: Phosphor only.
- Every PR ≤ 800 changed lines and ≤ 20 files (`git diff --stat origin/main...HEAD | tail -1`).
- **Never restart the mlab daemon and never touch `bin/pdx`.** P4-1 PRs are daemon deploys done by the coordinator.

## Measured facts this plan relies on (mlab, 2026-10-07, Claude Code 2.1.291, 389 top-level transcripts)

- Title lines: `{"type":"ai-title","aiTitle":…,"sessionId":…}` (15,509 lines) and `{"type":"custom-title","customTitle":…,"sessionId":…}` (19 lines). Latest wins.
- `entrypoint` and `cwd` are carried on `user`, `assistant`, `system` and `attachment` lines. First entrypoint values: `cli` 291, `sdk-cli` 98.
- Every file name is a UUID; no `user` / `assistant` line carries a `sessionId` different from its file name.
- **First human prompt** under the rule in Task 35 lies within the first 64 KB in only 257 of the 339 files that have one (median offset 22 KB; 82 files later, 20 of them past 4 MB). **The coordinator revised §13.6 (2026-10-07, docs PR from the coordinator):** cwd, the first entrypoint and the first human prompt are read line by line from byte 0 **until the first human prompt, at most 16 MB**, and stored in the index; the tail stays 256 KB.
- **Cost of the revised first scan** (Python prototype of the Task 35 rule, 416 files, 2026-10-07): head reads 368 MB, tail 89 MB; **1.87 s** on the first run, 1.07 s warm. 367 files have a first prompt; **0** hit the 16 MB cap; **49** have none before EOF (their head scan resumes from where it stopped as the file grows). The Go figure is measured again in the acceptance step (end of this plan).
- Skipped system-tag prompts seen: `<task-notification>` 1,269, `<command-name>` 21, `<local-command-stdout>` 15, `<command-message>` 1.

## Rulings (implementer derivations)

Derived from §13 by the implementer. **R-4-1, R-4-3, R-4-4, R-4-5 and R-4-6 are user-visible and were approved by the coordinator (`mlab/purdex-18`) on 2026-10-07; R-4-14 and R-4-15 are the coordinator's own additions.** The rest follow from the spec; the plan review's changes are marked *(review)*.

| # | Ruling | From |
|---|---|---|
| R-4-1 | **Root error.** When the root cannot be listed, the snapshot still lists every unowned in-scope S as **ended** (from the index and the executions), marks nothing gone, and carries `root_error`. The 已退出 tab shows「無法讀取對話檔目錄：{{error}}」above the rows and **disables rebuild** on every row while the error stands. 已消失 shows the same notice and no rows. *(Coordinator-approved.)* | §13.5 |
| R-4-2 | **Nexen list or terminal lookup fails → 503** `conversations_unavailable` with the error; the tab shows its error line and retry. Never a partial list: a partial worker list would show a live worker as ended. | §4.2, §13.6 joins |
| R-4-3 | **The rebuild tab of a terminal-last row is a closed terminal pane** (`tmux-session`, new `terminated: 'conversation-ended'`), seeded with a rebuild record for S (cwd, `agent {type:'cc', sessionId:S}`, a generated session name as take-to-terminal names one, and the host's **current tmux generation**). It is §7's screen: mode choice, `RebuildActionSet`, session picker. Copy: title「對話已結束」, description「{{title}}」. Clicking 重建… again for the same (host, S) selects that existing tab. Afterwards it is an ordinary closed terminal pane, included in Rebuild all. *(Review:)* when the host's generation is unknown at open time (no sessions payload yet), the pane is opened with `tmuxInstance: ''`; a single rebuild still works, and Rebuild all excludes it exactly as it excludes any closed pane without a generation (`batch.ts` `groupForBatch`). *(Coordinator-approved, the review note is a refinement.)* | §13.4, §7 |
| R-4-4 | **The 120 s warning** is a notice on that rebuild screen, in both modes, while `now − last_activity_at < 120 s` (the row's value at open time), with a live seconds count. No extra confirm step. *(Coordinator-approved.)* | §13.4 |
| R-4-5 | **A worker-last row without any stint** (a cli-born S last resumed by a non-Purdex sdk tool) opens the same closed-terminal pane with **worker** preselected. A worker-last row with a stint opens the latest stint's exited screen (§13.4). *(Coordinator-approved.)* | §13.4 preselection |
| R-4-6 | **上次在** *(coordinator-approved 2026-10-07; spec text added in P4-1a)*: the last `entrypoint` in the tail window decides (`cli` → terminal, `sdk-*` → worker). When the tail has none: the first entrypoint. When neither exists: worker if S has a stint, terminal otherwise. An entrypoint value that is neither `cli` nor `sdk-*` counts as terminal. | §13.3 |
| R-4-14 | **In scope** *(coordinator 2026-10-07, amends §13.2 in P4-1a)*: S is in scope when its transcript's first `entrypoint` is **non-empty and does not start with `sdk-`** (born interactively: `cli`, an IDE extension, …), or S has a stint on this host. A transcript with no `entrypoint` at all is in scope only through a stint. | §13.2 as amended |
| R-4-15 | **Unknown owners shown** *(coordinator 2026-10-07)*: when `unknown_owner > 0`, the 已退出 tab ends its list with a muted line「另有 {{n}} 個對話的狀態無法確認，未列出」(the same transparency as the cap notice). 已消失 does not repeat it. | R-4-9 |
| R-4-7 | *(Review.)* **A root that cannot be read** fails the listing (R-4-1). **A slug directory that cannot be read** does not: it is skipped, recorded in `ScanResult.UnreadableDirs` and logged, and any S whose indexed `transcript_path` lies in it counts as **present** (never marked gone from a partial view). | §13.5 |
| R-4-8 | *(Review.)* **Transcript presence:** listed in this scan; or, for an S with a stint, the latest stint's `TranscriptPath` is non-empty and `Lstat`s as a regular file (§13.2 "Purdex stats that path"); or R-4-7's unreadable-dir rule. Last activity for an S without an index row is the latest stint's `UpdatedAt`. | §13.2, §13.6 |
| R-4-9 | *(Review.)* **Running** = a **verified** live root `cc` frame for S (D1: pid alive and start time matching) or a live execution for S. An S whose only live frames are **unverified** (pid alive, start time unreadable) cannot be shown to be unowned: it is listed in **neither** tab (fail closed, as `checkOwners` answers 503 for it), counted in the response's `unknown_owner` and logged. | §4.2 D1, `checkOwners` |
| R-4-10 | *(Review.)* **Title** = custom-title → ai-title → **the latest stint's** Nexen title (`TitleText`; D9's representative stint) → the first line of the first human prompt (trimmed, ≤ 120 runes) → `S[:8]`. The daemon computes it once; the row also carries `first_prompt` (≤ 500 bytes, for search). | §13.3, D9 |
| R-4-11 | **Nexen disabled** → the nex module is not added, so the route answers 404; the tabs show「這台主機沒有啟用 Nexen」. Engine soft-failed or no index wired → 503 `conversations_unavailable`. | §13.6 "the nex module serves it" |
| R-4-12 | *(Review.)* **SPA refresh:** on mount and on retry only. The daemon's 5 s reuse bounds the cost; switching tabs remounts the list. No coupling to the live execution list. | §13.6 |
| R-4-13 | **Human prompt** also excludes `isSidechain` and `isCompactSummary` user lines (neither is written by a human), beyond §13.1's "not meta, not a tool result, text not starting with `<`". | §13.1 |

## File structure

**Daemon**
- Create `internal/store/conversation_index.go` — `ConversationStore`: table DDL, `All`, `UpsertBatch`.
- Modify `internal/store/meta.go` — call `migrateConversationIndex(db)` from `migrateMetaDB`; accessor `(*MetaStore).Conversations()`.
- Create `internal/conversations/list.go` — `ListRoot`.
- Create `internal/conversations/head.go` (`ScanHead`, `openTranscript`), `tail.go` (`ReadTail`), `prompt.go` (the human-prompt rule).
- Create `internal/conversations/scan.go` — `Scan` (listing + per-file decision + upsert).
- Modify `internal/module/agent/terminal_sessions.go` — `LiveSessions`.
- Create `internal/module/nex/conversations.go` — `buildConversations` (pure join) and row types.
- Create `internal/module/nex/conversations_http.go` — handler, snapshot cache, schedule.
- Modify `internal/module/nex/module.go` — fields, `WithConversationIndex`, route, Start/Stop hooks.
- Modify `cmd/pdx/main.go` — `nex.New().WithConversationIndex(meta.Conversations())`.

**SPA**
- Create `spa/src/lib/nex/conversations-api.ts` — `listConversations`, types.
- Create `spa/src/hooks/useConversations.ts`.
- Create `spa/src/lib/nex/open-conversation-rebuild.ts` — open / focus the closed-terminal pane for S.
- Modify `spa/src/features/workspace/lib/open-worker-tab.ts` — extract `focusTabInWorkspace(tabId)`.
- Modify `spa/src/types/tab.ts` — `TerminatedReason` gains `'conversation-ended'`; `tmux-session` gains optional `conversation`.
- Modify `spa/src/components/TerminatedPane.tsx` — reason copy, preselection, 120 s notice.
- Rewrite `spa/src/components/settings/WorkerExitedTab.tsx`; create `spa/src/components/settings/WorkerGoneTab.tsx`; create `spa/src/components/settings/ConversationRow.tsx` (shared row).
- Modify `spa/src/lib/register-modules/index.tsx` — register `gone` (order 30).
- Delete when unused: `spa/src/hooks/useExecutionHistory.ts`, `spa/src/lib/nex/exited-entities.ts`, `spa/src/lib/nex/terminal-session-ids.ts` and their tests.
- Locales: both files.

## PR map

Split up front *(review: P4-1a, P4-1b and P4-2a each risked the 800-line limit)*:

| PR | Tasks | Side | Deploy |
|---|---|---|---|
| P4-1a | 34, 35a | daemon: index table; list, head, tail readers | none (nothing calls it yet) |
| P4-1b | 35b, 36, 37 | daemon: `Scan`; `LiveSessions`; the pure join | none |
| P4-1c | 38 | daemon: endpoint, cache, schedule, wiring | **daemon deploy** (coordinator) |
| P4-2a | 39, 40 | SPA: API + hook; the conversation rebuild pane | SPA |
| P4-2b | 41, 42 | SPA: 已退出 on the endpoint; 已消失 | SPA; needs daemon ≥ P4-1c's version |

If a PR still exceeds 800 lines / 20 files, split it at a task boundary and say so in the PR body. Each PR: controller gate → codex R1 + attacker → critic → fixes → incremental re-review → merge → bump. P4-2a may merge before the daemon deploy (its endpoint use is mocked in tests and nothing calls it until P4-2b). P4-2b must not merge before P4-1c is deployed on mlab; its CHANGELOG states the daemon floor.

## Review Focus

1. **The vault volume is unmounted** (root is a dangling symlink) → every ended conversation is still listed, none is gone, the notice shows, rebuild is disabled (R-4-1). Owner: Task 37 (`TestBuild_RootErrorMarksNothingGone`), Task 38 (handler carries `root_error`), Task 41 (disabled rebuild).
2. **A transcript is being written while it is scanned** (the last line is half-written; size grows between `stat` and read) → the half line is not consumed by the head scan (its offset stays before it) and skipped by the tail, the row is not corrupted, and the next scan continues because (size, mtime) changed. Owner: Tasks 35a (the `ScanHead` partial-last-line test) and 35b (the growth tests).
3. **A conversation is running in a terminal while its file is also listed** → never listed in either tab, also when its frame cannot be verified (R-4-9); and a worker that is `failed` but not exited is running (live), never ended. Owner: Task 37.
4. **Two requests and the 6 h timer at once** → one scan; both requests get the same snapshot; Stop during a scan returns within the bound. Owner: Task 38.
5. **Clicking 重建… twice on a terminal-last row** → one tab, focused the second time (R-4-3). Owner: Task 40.

---

## Phase P4-1a — daemon: conversation index and scanner

### Task 34: `conversation_index` table and `ConversationStore`

**Files:**
- Create: `internal/store/conversation_index.go`
- Modify: `internal/store/meta.go` (`migrateMetaDB` calls `migrateConversationIndex`; add the accessor)
- Test: `internal/store/conversation_index_test.go`

**Interfaces:**
- Produces:
  ```go
  type ConversationIndexRow struct {
      SessionID       string // lowercase UUID, primary key
      TranscriptPath  string
      Cwd             string
      FirstEntrypoint string
      LastEntrypoint  string
      CustomTitle     string
      AITitle         string
      FirstPrompt     string // ≤ 500 bytes, cut on a UTF-8 boundary
      Size            int64
      MtimeMs         int64
      Inode           uint64 // a different inode means the file was rewritten
      HeadOffset      int64  // byte where the head scan stopped (always a line boundary)
      HeadDone        bool   // the first prompt was found, or 16 MB were examined
      FirstSeenAt     int64  // Unix ms
      LastSeenAt      int64  // Unix ms
  }
  type ConversationStore struct{ db *sql.DB }
  func (m *MetaStore) Conversations() *ConversationStore
  func (s *ConversationStore) All(ctx context.Context) ([]ConversationIndexRow, error)
  // UpsertBatch writes rows in one transaction. On conflict it updates every
  // column except first_seen_at, which keeps its original value.
  func (s *ConversationStore) UpsertBatch(ctx context.Context, rows []ConversationIndexRow) error
  ```

- [ ] **Step 1: Write the failing tests** (`t.TempDir()` file DB through `OpenMeta`, testify `require`):
  - fresh DB: `All` is empty; `sqlite_master` has `conversation_index`;
  - `UpsertBatch` of two rows, then `All` returns both with every field (including `Inode`, `HeadOffset`, `HeadDone` true and false, and a `FirstPrompt` with multi-byte text);
  - a second `UpsertBatch` of the same session id with new size / mtime / inode / head state / titles / `LastSeenAt` updates them and keeps `FirstSeenAt`;
  - an `Inode` above 2^63 round-trips (stored as a signed 64-bit integer and converted back);
  - reopening the same file (`OpenMeta` twice) is idempotent and keeps the rows;
  - an empty batch is a no-op.
- [ ] **Step 2: Run** `go test ./internal/store/ -run Conversation -v` → FAIL (undefined).
- [ ] **Step 3: Implement.** DDL (idempotent, called from `migrateMetaDB` after the existing tables):
  ```go
  func migrateConversationIndex(db *sql.DB) error {
      _, err := db.Exec(`
          CREATE TABLE IF NOT EXISTS conversation_index (
              session_id        TEXT PRIMARY KEY,
              transcript_path   TEXT NOT NULL,
              cwd               TEXT NOT NULL DEFAULT '',
              first_entrypoint  TEXT NOT NULL DEFAULT '',
              last_entrypoint   TEXT NOT NULL DEFAULT '',
              custom_title      TEXT NOT NULL DEFAULT '',
              ai_title          TEXT NOT NULL DEFAULT '',
              first_prompt      TEXT NOT NULL DEFAULT '',
              size              INTEGER NOT NULL DEFAULT 0,
              mtime_ms          INTEGER NOT NULL DEFAULT 0,
              inode             INTEGER NOT NULL DEFAULT 0,
              head_offset       INTEGER NOT NULL DEFAULT 0,
              head_done         INTEGER NOT NULL DEFAULT 0,
              first_seen_at     INTEGER NOT NULL,
              last_seen_at      INTEGER NOT NULL
          )`)
      return err
  }
  ```
  Upsert: `INSERT … ON CONFLICT(session_id) DO UPDATE SET transcript_path=excluded.transcript_path, …, last_seen_at=excluded.last_seen_at` (not `first_seen_at`). `All` orders by `session_id`. Rows are never deleted (§13.6).
- [ ] **Step 4: Run** the tests → PASS; `go vet ./internal/store/`.
- [ ] **Step 5: Commit** `feat(daemon): conversation_index table in meta.db (P4)`.

### Task 35a: `internal/conversations` — list the root, read a transcript's head and tail

**Files:**
- Create: `internal/conversations/list.go`, `head.go`, `tail.go`, `prompt.go`
- Test: `internal/conversations/list_test.go`, `head_test.go`, `tail_test.go` (fixture dirs built in `t.TempDir()`; JSONL lines written by a helper in `helpers_test.go`)

**Interfaces:**
- Produces:
  ```go
  const TailWindow = 256 << 10
  var headCap int64 = 16 << 20 // a var only so tests can lower it

  type Entry struct {
      SessionID string // lowercase
      Path      string
      Size      int64
      MtimeMs   int64
      Inode     uint64
  }
  // ListRoot lists <root>/<slug>/<uuid>.jsonl. root and slug dirs are followed
  // when symlinked; a transcript that is a symlink or not a regular file is
  // skipped. A root that cannot be read returns err (R-4-1). A slug dir that
  // cannot be read is skipped and returned in unreadable (R-4-7). When one
  // session id appears twice, the larger MtimeMs wins.
  func ListRoot(root string) (entries []Entry, unreadable []string, err error)

  // Head is what the head scan has found so far.
  type Head struct {
      Cwd, FirstEntrypoint, FirstPrompt string
      Offset int64 // the byte after the last complete line examined
      Done   bool  // FirstPrompt found, or headCap bytes examined
  }
  // ScanHead continues a head scan of f from prev.Offset (prev is the zero
  // Head for a fresh scan). It reads complete lines only: a final line with no
  // '\n' is left for the next scan, so Offset is always a line boundary. It
  // stops at the first human prompt (Done, Offset = the end of that line), at
  // headCap bytes from 0 (Done; a line cut by the cap is skipped), or at the
  // last complete line (not Done). Fields already set in prev are kept; empty
  // ones are filled in file order. n is the number of bytes read.
  func ScanHead(f *os.File, prev Head) (h Head, n int64, err error)

  type Tail struct{ LastEntrypoint, CustomTitle, AITitle string }
  // ReadTail parses the last TailWindow bytes of a file of the given size.
  func ReadTail(f *os.File, size int64) (t Tail, n int64, err error)

  // OpenTranscript opens path with O_RDONLY|O_NOFOLLOW|O_NONBLOCK and requires
  // a regular file (fstat). It returns the file with its size, mtime and inode.
  func OpenTranscript(path string) (*os.File, Entry, error)
  ```

**Line rules:**
- `ScanHead`, in file order: `Cwd` = the first non-empty `cwd`; `FirstEntrypoint` = the first `entrypoint`; `FirstPrompt` = the first **human prompt**. A line that is not a JSON object is skipped. For speed, a line is decoded into a small struct (`type`, `cwd`, `entrypoint`, `isMeta`, `isSidechain`, `isCompactSummary`, `message.content` as `json.RawMessage`), and `message.content` is decoded only for `type == "user"` lines.
- `ReadTail`, over the last `TailWindow` bytes (the first piece is dropped when the window does not start at byte 0; a final piece without `\n` that is not valid JSON is skipped): `LastEntrypoint` = the last `entrypoint`; `CustomTitle` = the last non-empty `customTitle` of a `custom-title` line; `AITitle` = the last non-empty `aiTitle` of an `ai-title` line (both `TrimSpace`d).
- **Human prompt** (§13.1 as amended: "not meta, not a tool result, and whose text does not start with `<`"): `type == "user"`; not `isMeta`; also not `isSidechain` or `isCompactSummary` (R-4-13); `message.content` is a string, or an array with no `tool_result` block, in which case the first `text` block's text; after `TrimSpace` the text is non-empty and **does not start with `<`** (this excludes command wrappers, task notifications and cross-session messages). Stored cut to 500 bytes on a UTF-8 boundary.

- [ ] **Step 1: Write the failing tests.**
  - `ListRoot`: two slug dirs with UUID transcripts → entries with size / mtime / inode; a nested `<sid>/subagents/x.jsonl` → ignored; a non-UUID `notes.jsonl` → ignored; an uppercase-UUID file name → lowercased id; a **symlinked root** → followed; a **symlinked slug dir** → followed; a **symlinked transcript** → skipped; a directory named `<uuid>.jsonl` → skipped; a missing root → `err`; a dangling root symlink → `err`; **an unreadable slug dir** (`chmod 000`, skipped when running as root) → in `unreadable`, the other slug's entries still listed, `err == nil`; the same id in two slugs → the newer mtime wins.
  - `ScanHead`: cwd and `cli` entrypoint on early lines; `isMeta` / `isCompactSummary` / `<command-name>…` / `<task-notification>…` / `<pasted_content …>` / a `tool_result` array are skipped and the next plain prompt is taken; a text-block array with an image counts; a prompt with leading whitespace before plain text counts; **a first prompt beyond 64 KB (after 5 MB of padding lines) is found** (Done, Offset = end of its line); no prompt in a file of 3 complete lines → not Done, Offset = file size; then append a prompt line and resume from that Head → found, the earlier `Cwd` kept, and `n` counts only the appended bytes; **a partial last line** (no `\n`) is not consumed (Offset stops before it); a 600-byte prompt → cut to ≤ 500 bytes on a rune boundary; **no prompt within the cap** (`headCap` lowered to 4 KB) → Done without a prompt, `n` ≤ the cap, and a line straddling the cap is not taken as the prompt.
  - `ReadTail`: two `ai-title`s and one `custom-title` → the last values; last entrypoint `sdk-cli` after `cli` → `sdk-cli`; a title only before the window (≥ 256 KB of padding after it) → empty; the piece straddling the window start → skipped; an empty `aiTitle` after a non-empty one → the non-empty one is kept.
  - `OpenTranscript`: a symlink → error; a FIFO (`syscall.Mkfifo`) → error without blocking; a regular file → size, mtime and inode match `os.Stat`.
- [ ] **Step 2: Run** `go test ./internal/conversations/ -v` → FAIL.
- [ ] **Step 3: Implement.** `ScanHead` seeks to `prev.Offset` and reads with a `bufio.Reader`, appending `ReadSlice('\n')` pieces into a line buffer on `bufio.ErrBufferFull`, so `Offset` advances by whole lines only. `ReadTail` reads `[max(0,size−TailWindow), size)` with `ReadAt`. The inode comes from `fi.Sys().(*syscall.Stat_t).Ino`.
- [ ] **Step 4: Run** the tests → PASS; `go vet ./internal/conversations/`; `gofmt -l internal/conversations`.
- [ ] **Step 5: Commit** `feat(daemon): conversations package — list the projects root, read transcript heads and tails (P4)`.

## Phase P4-1b — daemon: scan, owners, join

### Task 35b: `conversations.Scan` — bring the index up to date

**Files:**
- Create: `internal/conversations/scan.go`
- Test: `internal/conversations/scan_test.go`

**Interfaces:**
- Consumes: Task 34 `store.ConversationIndexRow`; Task 35a `ListRoot`, `OpenTranscript`, `ScanHead`, `ReadTail`.
- Produces:
  ```go
  type Index interface {
      All(ctx context.Context) ([]store.ConversationIndexRow, error)
      UpsertBatch(ctx context.Context, rows []store.ConversationIndexRow) error
  }
  type ScanResult struct {
      Present        map[string]Entry // by session id; nil when RootErr != nil
      UnreadableDirs []string         // slug dirs skipped this round (R-4-7)
      RootErr        error
      ScannedAt      int64 // Unix ms
      Files          int   // entries listed
      Reread         int   // entries whose head or tail was read this round
      BytesRead      int64
  }
  // Scan lists root and brings the index up to date (see "Per-file decision").
  // A root failure returns RootErr and writes nothing. A failure to open or read
  // one file skips that file this round (it stays Present; its row is unchanged).
  // ctx is checked between files. The returned error is only an index error.
  func Scan(ctx context.Context, root string, idx Index, now func() time.Time) (ScanResult, error)
  ```

**Per-file decision** (row = the file's index row, if any):

| Case | Head | Tail |
|---|---|---|
| no row, or `Inode` differs, or `Size` < row's | `ScanHead` from the zero `Head` (all head fields reset) | read |
| `Size` and `MtimeMs` equal to the row's | — | — (only `LastSeenAt` is bumped) |
| grew, row `HeadDone` | — (keep the row's head fields) | read |
| grew, not `HeadDone` | `ScanHead` resumed from the row's `Cwd` / `FirstEntrypoint` / `FirstPrompt` / `HeadOffset` | read |

`Scan` writes, in one `UpsertBatch`, every re-read row plus `LastSeenAt = now` for the unchanged ones. It logs nothing itself; the caller (Task 38) logs `Files` / `Reread` / `BytesRead` / `UnreadableDirs` and the duration.

- [ ] **Step 1: Failing tests** (in-memory fake `Index` recording calls; `Reread` / `BytesRead` asserted):
  - first scan upserts every file with `FirstSeenAt == LastSeenAt == now`;
  - a second scan with no change reads nothing (`Reread` 0, `BytesRead` 0) and only bumps `LastSeenAt`;
  - **append-only growth of a `HeadDone` file reads only the tail** (`BytesRead` ≤ `TailWindow`), and its head fields are unchanged;
  - growth of a not-`HeadDone` file resumes the head from its `HeadOffset` (`BytesRead` ≤ appended bytes + `TailWindow`) and finds a newly appended prompt;
  - **a truncated (smaller) file and a rewritten file (same name, new inode) re-read the head from 0** (a changed cwd is picked up);
  - a removed file is absent from `Present`, and its row is untouched;
  - a missing root → `RootErr`, no `UpsertBatch` call, `Present == nil`;
  - an unreadable slug dir → in `UnreadableDirs`; the rows of its files are untouched; the other files are scanned;
  - a file that fails to open (replaced by a FIFO between list and open) → still in `Present`, no row written for it this round;
  - a cancelled ctx stops between files and returns `ctx.Err()`;
  - an `UpsertBatch` error → returned as the error.
- [ ] **Step 2: Run** → FAIL. **Step 3: Implement.** **Step 4: Run** → PASS; `go vet`, `gofmt -l`.
- [ ] **Step 5: Commit** `feat(daemon): conversations scan — re-read only what changed, resume unfinished heads (P4)`.

### Task 36: `TerminalSessions.LiveSessions`

**Files:**
- Modify: `internal/module/agent/terminal_sessions.go` (interface + method; share the pid / start-time check with `LiveBySessionID` through one unexported helper)
- Modify: `internal/module/nex/handoff_fakes_test.go` (`stubTerminals` gains the method; it returns every session in `live`, and honours `err`)
- Test: `internal/module/agent/terminal_sessions_test.go`

**Interfaces:**
- Produces:
  ```go
  // LiveSessions returns every root frame of agentType ("" = all types) that
  // carries a session id and passes LiveBySessionID's liveness rule: a dead pid
  // or a start-time mismatch is dropped; an unreadable start time is returned
  // with Verified=false, which is NOT an owner (TerminalSession's contract).
  // Callers decide what an unverified frame means for them.
  LiveSessions(ctx context.Context, agentType string) ([]TerminalSession, error)
  ```
  It reads `m.frames.ListAll()` and keeps roots (`ParentFrameID == ""`) with a non-empty `SessionID`.

- [ ] **Step 1: Failing tests** (real frames on `:memory:` via `newTestModule`, seams `isPidAliveFn` / `processStartTimeFn`): two live root cc frames + one dead + one start-time mismatch + one subagent frame + one root frame without a session id + one codex frame → `LiveSessions(ctx,"cc")` returns the two live cc roots with `Verified=true`; `""` also returns the codex one; an unreadable start time → returned with `Verified=false`; a done ctx → its error.
- [ ] **Step 2: Run** → FAIL. **Step 3: Implement** (extract `liveFrame(f store.Frame) (TerminalSession, bool)` used by both methods; `LiveBySessionID`'s behaviour and tests are unchanged). **Step 4: Run** agent + nex tests → PASS (the nex fakes compile with the new method).
- [ ] **Step 5: Commit** `feat(daemon): agent TerminalSessions lists every live terminal session (P4)`.

### Task 37: `buildConversations` — the pure join (states, titles, 上次在)

**Files:**
- Create: `internal/module/nex/conversations.go`
- Test: `internal/module/nex/conversations_test.go`

**Interfaces:**
- Consumes: Task 34 row type; Task 35b `ScanResult`; Task 36 `TerminalSession`; Nexen `store.Execution`; `isLiveExecution`.
- Produces:
  ```go
  type conversationState string
  const (
      conversationEnded conversationState = "ended"
      conversationGone  conversationState = "gone"
  )

  type conversationRow struct {
      SessionID         string `json:"session_id"`
      Title             string `json:"title"`
      TitleSource       string `json:"title_source"` // "custom" | "ai" | "nexen" | "prompt" | "session_id"
      FirstPrompt       string `json:"first_prompt,omitempty"`
      Cwd               string `json:"cwd,omitempty"`
      CwdExists         bool   `json:"cwd_exists"`
      LastActivityAt    int64  `json:"last_activity_at"` // Unix ms
      LastIn            string `json:"last_in"`          // "terminal" | "worker"
      TranscriptPath    string `json:"transcript_path,omitempty"`
      LatestExecutionID string `json:"latest_execution_id,omitempty"`
      EffectiveProfile  string `json:"effective_profile,omitempty"`
  }

  // conversationInputs are materialized rows and results, never services.
  type conversationInputs struct {
      IndexRows []pstore.ConversationIndexRow // Purdex internal/store, imported as pstore
      Scan      conversations.ScanResult
      Execs     []store.Execution             // Nexen store: every execution, archived included
      Terminals []agent.TerminalSession       // LiveSessions(ctx, "cc")
      IsRegular func(path string) bool        // Lstat(path).Mode().IsRegular(); test seam
      DirExists func(path string) bool        // Stat(path).IsDir(); test seam
  }

  type conversationsResult struct {
      Ended, Gone  []conversationRow // each sorted LastActivityAt desc, then SessionID asc; uncapped
      UnknownOwner int               // S skipped because only unverified frames hold them (R-4-9)
  }

  func buildConversations(in conversationInputs) conversationsResult
  ```

**Algorithm** (each step has a test below):
1. Group executions by S = lowercase(`SessionID` || `ResumeSessionID`); skip executions with neither. Per S: `live` = any `isLiveExecution`; `latest` = max (`CreatedAt`, `ID`) (D9).
2. Index rows by lowercase S.
3. Owners (R-4-9): `verified` = lowercase S of every `Terminals` entry with `Verified`; `unverified` = the rest. Running = `verified` ∪ every S with a live execution.
4. Candidates (R-4-14) = { S : index row whose `FirstEntrypoint` is non-empty and does not start with `sdk-` } ∪ { S : has an execution }.
5. For each candidate: running → skip; in `unverified` (and not running) → skip and count `UnknownOwner`. Otherwise:
   - present (R-4-1, R-4-7, R-4-8) = `Scan.RootErr != nil`, or `Scan.Present[S]` exists, or (`latest != nil` and `latest.TranscriptPath != ""` and `IsRegular(latest.TranscriptPath)`), or (the index row's `TranscriptPath` lies under one of `Scan.UnreadableDirs`);
   - state = ended when present, else gone.
6. Fields: `Cwd` = index cwd, else `latest.Cwd`; `CwdExists` = `Cwd != "" && DirExists(Cwd)`; `LastActivityAt` = the listed entry's `MtimeMs` when listed, else the index row's `MtimeMs`, else `latest.UpdatedAt`; `TranscriptPath` = listed path, else index path, else `latest.TranscriptPath`; `LastIn` per R-4-6; title per R-4-10 (Nexen title = `latest.TitleText` only; `firstLine(prompt)` = up to the first `\n`, `TrimSpace`, ≤ 120 runes); `LatestExecutionID` / `EffectiveProfile` from `latest` when there is one.

- [ ] **Step 1: Failing table tests** — the §13.9 state cases as inputs:
  - cli-born, no owner, listed → ended, `LastIn` terminal, title = ai-title;
  - cli-born with a **verified** live frame → not listed; with only an **unverified** live frame → not listed, `UnknownOwner` 1;
  - worker-born (`FirstEntrypoint` `sdk-cli`), archived terminated stint, listed → ended, `LastIn` worker, `LatestExecutionID` = that stint;
  - the same not listed, no index row, `IsRegular(latest.TranscriptPath)` false → gone, `LastActivityAt` = stint `UpdatedAt`, title from `TitleText`;
  - **a stint whose `TranscriptPath` lies outside the listing and exists** (`IsRegular` true) → ended;
  - cli-born indexed, not listed → gone; then listed again → ended (no stored "gone");
  - an indexed S whose path lies in an `UnreadableDirs` entry, not listed → ended;
  - sdk-born, no stint → never listed;
  - **born in an IDE extension** (`FirstEntrypoint` `claude-vscode`), no stint → in scope, ended, `LastIn` terminal (R-4-14); **no entrypoint at all**, no stint → never listed;
  - a `failed` not-archived stint → live → not listed (Review Focus 3);
  - an execution with only `ResumeSessionID` (mixed case) joins the lowercase index row;
  - `DirExists` false → `CwdExists` false; no cwd anywhere → `Cwd ""`, `CwdExists` false;
  - title fallbacks in order: custom > ai > the **latest** stint's `TitleText` (an older stint's title is not used when the latest has none) > prompt first line (a two-line prompt → line 1; a 300-rune line → 120 runes) > `S[:8]`, with `TitleSource` each time;
  - `LastIn` (R-4-6): last `sdk-cli` → worker; last missing, first `cli` → terminal; no entrypoints, has stint → worker; no entrypoints, no stint → terminal; an unknown non-sdk value → terminal;
  - **`TestBuild_RootErrorMarksNothingGone`**: `Scan.RootErr` set, `Present` nil, an indexed cli-born S and a stint-only S → both ended, `Gone` empty;
  - order: `LastActivityAt` desc, ties by `SessionID`.
  - Mutation check (in the report): dropping the running check, counting unverified frames as absent, or treating "not in Present" as gone under `RootErr`, each turns a test red.
- [ ] **Step 2: Run** `go test ./internal/module/nex/ -run Conversation -v` → FAIL.
- [ ] **Step 3: Implement** the function as specified (no I/O except the two seams).
- [ ] **Step 4: Run** → PASS; `go test ./internal/module/nex/` (`TestImportBoundary` checks only `lab.protype.tw/wake/nexen/*` imports; the new imports are `internal/store`, `internal/conversations` and the agent module, which it does not restrict).
- [ ] **Step 5: Commit** `feat(daemon): conversation states — ended and gone from the index, frames and stints (P4)`.

## Phase P4-1c — daemon: the endpoint

### Task 38: `GET /api/nex/conversations`, the snapshot cache and the schedule

**Files:**
- Create: `internal/module/nex/conversations_http.go`
- Modify: `internal/module/nex/module.go` (fields; `WithConversationIndex`; route; Start/Stop)
- Modify: `cmd/pdx/main.go` (`c.AddModule(nex.New().WithConversationIndex(meta.Conversations()))`)
- Test: `internal/module/nex/conversations_http_test.go`

**Interfaces:**
- Consumes: Tasks 35b–37.
- Produces (HTTP, consumed by Task 39):
  ```
  GET /api/nex/conversations?state=ended|gone
  200 {
    "state": "ended",
    "scanned_at": 1759800000000,      // Unix ms of the scan this snapshot used
    "root_error": "…",                // omitted when the root was listed
    "home": "/Users/wake",            // for "~" display
    "total": 2345,                    // rows in this state before the cap
    "truncated": true,                // total > 2000
    "unknown_owner": 0,               // R-4-9
    "conversations": [ conversationRow … ]   // ≤ 2000, newest first
  }
  400 {code:"bad_state"}               // state missing or not ended|gone
  503 {code:"conversations_unavailable", error}   // no index wired, engine soft-failed, the
                                                  // daemon stopping, or the index, the Nexen
                                                  // List or LiveSessions failed (R-4-2)
  ```
  Go:
  ```go
  const conversationRowCap = 2000
  // WithConversationIndex wires the index; nil keeps the feature off. Returns m.
  func (m *Module) WithConversationIndex(idx conversations.Index) *Module
  ```
  Module fields: `convIdx conversations.Index`, `convRoot string` (default `$HOME/.claude/projects`, set in `Init`; tests override), `convHome string`, `convNow func() time.Time`, `convIsRegular`, `convDirExists func(string) bool`, `convScan` (seam, default `conversations.Scan`), `convMu sync.Mutex`, `convCached *convSnapshot`, `convFlight *convFlight`, `convCtx context.Context` + `convCancel context.CancelFunc` (created in `Init`, cancelled in `Stop`), `convWG sync.WaitGroup` (the timer goroutine **and** every flight).

**Snapshot** (`convSnapshot{ at time.Time; scannedAt int64; rootErr string; res conversationsResult }`) — `m.conversationSnapshot(ctx)`:
- a cached snapshot younger than **5 s** (by `convNow`) is returned;
- otherwise join the in-flight one or become it (the `monitor` module's `snapshotFlight` shape: `done chan struct{}`, result, err). A waiter whose ctx ends returns `ctx.Err()`; the flight continues.
- **The flight runs on `m.convCtx` with a 60 s timeout, never on the request's ctx**, and is counted in `convWG`. After `convCtx` is cancelled, no flight starts (the handler answers 503) and a running flight stops at its next file or page boundary.
- The flight: `m.convScan(ctx, m.convRoot, m.convIdx, m.convNow)` → log `Files` / `Reread` / `BytesRead` / `UnreadableDirs` / duration with `m.logf` → `m.convIdx.All` → every execution with `store.ListOptions{IncludeArchived: true, Limit: 500, Cursor: c}` (no `SessionID`, no `State`, no `Labels`) until `NextCursor == ""` or it repeats the previous cursor — **no page cap**; each page under `detachedContext(ctx, m.engineOpTimeout)` → `m.terminals.LiveSessions(ctx, "cc")` → `buildConversations`; log `UnknownOwner` when non-zero. A failure of the index, the List or LiveSessions fails the flight; a root error does not.
- Only a successful flight is cached; a failed one is not (the next request retries).

**Schedule** (`startConversationScan` from `Start`, after the existing work, only when `m.convIdx != nil && m.initErr == nil`): a goroutine that runs `conversationSnapshot(m.convCtx)` once immediately and then on a ticker of `convScanInterval` (`var convScanInterval = 6 * time.Hour`, a test seam), logging failures with `m.logf`.

**Stop**: `convCancel()`, then wait for `convWG` bounded by `ctx` and `convStopWait = 3 * time.Second` (the `stopManualResume` shape), **before** the engine shutdown. If the bound expires, log it; a flight still running then sees a cancelled ctx at its next boundary, and its `List` call, if any, fails against the closed engine and is discarded (never cached, never served).

- [ ] **Step 1: Failing tests** (a small `newConvEnv(t)` building on `newHandoffEnv`: a fixture root under `t.TempDir()`, an in-memory fake `conversations.Index`, `fakeNexStore.listRows`, `stubTerminals`):
  - `state=ended` → 200 with `home`, `scanned_at`, rows from a cli-born fixture file; `state=gone` → the removed one; missing / bad `state` → 400 `bad_state`;
  - **end to end, §13.9 "a title only before the tail window → fallback"**: a fixture transcript whose only `ai-title` is followed by ≥ 256 KB of lines → the row's `title_source` is `prompt` and `title` is the prompt's first line;
  - root removed → 200, `root_error` set, the indexed S under `ended`, `gone` empty;
  - `fakeNexStore.listErr` → 503 `conversations_unavailable`; `stubTerminals.err` → 503; no index wired (`New()` without `WithConversationIndex`) → 503; the 503 is not cached (the next call after clearing the error → 200);
  - **List paging without cap**: 1,203 executions over 3 pages → all joined; `allListOpts` shows every call with `IncludeArchived: true`, `Limit: 500`, empty `SessionID`, `State` and `Labels`, and the three cursors;
  - **a repeated cursor** (the fake returns the same `NextCursor` twice) → the walk stops, no infinite loop, the rows seen are joined;
  - cap: 2,001 ended rows → 2,000 returned, `total` 2001, `truncated` true, newest first;
  - `unknown_owner` reported when an S has only an unverified frame;
  - **cache**: two requests within 5 s → one scan (count `convScan` calls); after advancing `convNow` by 6 s → a new scan;
  - **single flight**: two concurrent requests while the scan blocks on a gate → one scan, both get the same `scanned_at`; a request whose ctx is cancelled while waiting → returns, the flight completes and is cached;
  - **schedule**: with `convScanInterval = 20ms`, `Start` runs a scan immediately and again on the tick;
  - **Stop**: with a scan blocked on a gate, `Stop` returns within `convStopWait` + slack; after `Stop` returns and the gate opens, **no `List` call is made** (the fake records calls after a flag) and a request answers 503;
  - route mounted even when `initErr` is set → 503 `conversations_unavailable`.
- [ ] **Step 2: Run** → FAIL.
- [ ] **Step 3: Implement**; wire `main.go`. Default root / home in `Init` from `os.UserHomeDir()` (an error leaves the feature off with a log line; the endpoint then answers 503).
- [ ] **Step 4: Run** `go test ./internal/module/nex/ ./internal/conversations/ ./internal/store/ ./internal/module/agent/`, then `go build ./... && go vet ./...`, then `go test -race ./internal/module/nex/ -run Conversation -count=3`.
- [ ] **Step 5: Commit** `feat(daemon): GET /api/nex/conversations — ended and gone conversations, scanned at start, every 6 h and on request (P4)`.

## Phase P4-2a — SPA: API, hook, the conversation rebuild pane

### Task 39: `listConversations` and `useConversations`

**Files:**
- Create: `spa/src/lib/nex/conversations-api.ts`, `spa/src/hooks/useConversations.ts`
- Test: `spa/src/lib/nex/conversations-api.test.ts`, `spa/src/hooks/useConversations.test.ts`

**Interfaces:**
- Consumes: Task 38's HTTP shape.
- Produces:
  ```ts
  export type ConversationState = 'ended' | 'gone'
  export interface ConversationRow {
    session_id: string; title: string; title_source: 'custom' | 'ai' | 'nexen' | 'prompt' | 'session_id'
    first_prompt?: string; cwd?: string; cwd_exists: boolean; last_activity_at: number
    last_in: 'terminal' | 'worker'; transcript_path?: string
    latest_execution_id?: string; effective_profile?: string
  }
  export interface ConversationsPage {
    state: ConversationState; scanned_at: number; root_error?: string; home: string
    total: number; truncated: boolean; unknown_owner: number; conversations: ConversationRow[]
  }
  export function listConversations(hostId: string, state: ConversationState): Promise<ConversationsPage>
  // errors: HandoffApiError (same class as the other purdex nex endpoints); 404 → code 'http_404'

  export interface UseConversations {
    page: ConversationsPage | null
    phase: 'loading' | 'ready' | 'error'
    error: string | null      // HandoffApiError code/message
    unavailable: boolean      // true for 404 (Nexen disabled, R-4-11)
    refetch: () => void
  }
  export function useConversations(hostId: string, state: ConversationState): UseConversations
  ```
- `listConversations` is a GET through `hostFetch` with the `X-Pdx-Client` header and the same error mapping as `handoff-api.ts`'s `postJson` (factor a shared `getJson` there, or put a small GET helper next to it; do not duplicate `handoffErrorFromResponse`). Validate the body shape (an object with a `conversations` array); a malformed body is an error.
- `useConversations` (R-4-12): fetch on mount and on `refetch()` only; one request in flight per (host, state) (a `refetch` during one is ignored); `refetch()` flips `error` back to `loading` and keeps the last good `page` visible; a response for a previous host or state is dropped; a failed refetch keeps the last good `page` and sets `error`. No live-list subscription.

- [ ] **Step 1: Failing tests:** API — GET path and query, header, 200 parse, 404 → `http_404`, 503 → `conversations_unavailable`, malformed body → error, unknown host → `host_removed`. Hook — mount fetch; a host change drops the stale response and fetches the new host; `refetch` while one is in flight → still one request; retry after an error; a failed refetch keeps the old page; 404 → `unavailable`.
- [ ] **Step 2: Run** → FAIL. **Step 3: Implement.** **Step 4: Run** → PASS.
- [ ] **Step 5: Commit** `feat(spa): conversations API client and hook (P4)`.

### Task 40: The conversation rebuild pane (R-4-3, R-4-4, R-4-5)

**Files:**
- Modify: `spa/src/types/tab.ts` — `TerminatedReason` adds `'conversation-ended'`; the `tmux-session` member gains `conversation?: { sessionId: string; title: string; lastIn: 'terminal' | 'worker'; lastWriteAt: number }`.
- Create: `spa/src/lib/nex/open-conversation-rebuild.ts`
- Modify: `spa/src/features/workspace/lib/open-worker-tab.ts` (extract `focusTabInWorkspace(tabId)`; `openWorkerTab` keeps its behaviour through it)
- Modify: `spa/src/components/TerminatedPane.tsx`
- Modify: locales.
- Test: `spa/src/lib/nex/open-conversation-rebuild.test.ts`, `spa/src/components/TerminatedPane.test.tsx` (new cases), `spa/src/lib/rebuild/reconcile-host.test.ts` and `spa/src/lib/rebuild/batch.test.ts` (or the existing eligibility test file) (new cases)

**Interfaces:**
- Consumes: Task 39 `ConversationRow`.
- Produces:
  ```ts
  // Opens (or selects, when one exists for this host + session id) a tab whose
  // pane is the closed-terminal rebuild screen for the conversation (R-4-3).
  export async function openConversationRebuild(hostId: string, row: ConversationRow): Promise<string> // tabId
  ```
- Behaviour:
  - **Existing tab (review: `openSingletonTab` cannot do this — `contentMatches` is always false for `tmux-session`, `pane-utils.ts:23-26`, and it scans only primary panes):** walk **every leaf of every tab's layout** in `useTabStore` for a pane with `kind 'tmux-session'`, the same `hostId`, `terminated === 'conversation-ended'` and `conversation.sessionId === row.session_id` (case-insensitive). Found → `focusTabInWorkspace(tabId)` and return; no new tab.
  - **New tab:** content `{ kind: 'tmux-session', hostId, sessionCode: '', mode: 'terminal', cachedName: <name>, tmuxInstance: <gen>, terminated: 'conversation-ended', rebuild: { sessionName: <name>, tmuxInstance: <gen>, cwd: row.cwd, cwdSource: 'user', agent: { type: 'cc', sessionId: row.session_id, updatedAt: row.last_activity_at }, agentExited: { at: row.last_activity_at, reason: 'session-end' }, capturedAt: Date.now() }, conversation: { sessionId, title: row.title, lastIn: row.last_in, lastWriteAt: row.last_activity_at } }`.
    - `<name>` is generated as `takeToTerminal` does (`nextProjectSessionName(slugForCwd(cwd, projects, await hostHomeFor(hostId, projects)), liveNames)`, `spa/src/lib/nex/handoff.ts:282-300`); reuse those helpers rather than copying the logic.
    - `<gen>` is the host's **current tmux generation**: the `tmux_instance` the SPA last received for that host — find the field the session list / `SessionPickerList` selection carries as `tmuxInstance` and read it from the same store. `''` when the SPA has none for the host (R-4-3 review note).
    - Create the tab with `useTabStore.getState().openSingletonTab(content)` (it always creates a new tab for `tmux-session`), then `focusTabInWorkspace(tabId)`.
  - **Reconciliation and Rebuild all (review):** the host reconciler skips panes already `terminated` (`spa/src/lib/rebuild/reconcile-host.ts:72-78`) — pin it: a sessions payload for the host must not change a `conversation-ended` pane's `terminated`, `tmuxInstance`, `rebuild` or `conversation`. `groupForBatch` excludes panes without a generation (`spa/src/lib/rebuild/batch.ts:111-119`) — pin both cases: with `<gen>` set the pane is a Rebuild-all candidate; with `''` it is excluded, as any closed pane without a generation is. Do **not** change `batch.ts`.
  - **Single rebuild:** pin that `rebuildPane` succeeds for this pane (mocked session create / resume): a new tmux session named `<name>` in `row.cwd`, then `claude --resume <S>` from the resume template, then the pane is a live terminal pane and `terminated` / `conversation` are gone.
- `TerminatedPane` changes:
  - `REASON_KEYS['conversation-ended'] = { title: 'terminated.conversation_ended', desc: 'terminated.conversation_ended_desc' }`; the description interpolates `{{title}}` from `content.conversation?.title`.
  - Initial mode: `content.conversation?.lastIn ?? 'terminal'` (R-4-5); the existing fallback when worker is unavailable still applies.
  - The 120 s notice (R-4-4) as `RebuildScreen`'s `detail`, both modes, while `Date.now() - lastWriteAt < 120_000`, re-rendering every second until it expires: `worker.rebuild.recent_write` with `{{n}}` seconds.
- Copy (zh-TW): `terminated.conversation_ended`「對話已結束」; `terminated.conversation_ended_desc`「{{title}}」; `worker.rebuild.recent_write`「這個對話 {{n}} 秒前還有寫入，可能正在 Purdex 以外的地方使用」.

- [ ] **Step 1: Failing tests:**
  - open → new tab with the content above (name and generation from mocked helpers / store); open twice → one tab, selected the second time; the existing pane is a **split leaf** (not the primary pane) → still found and selected; a different S → a second tab;
  - reconciliation, Rebuild all and single rebuild as pinned above;
  - `TerminatedPane` with `conversation-ended`: title / description copy; `lastIn: 'worker'` + Nexen ready → worker preselected, `rebuildAsWorker` called with `{ sessionId, cwd }` and no `replaceExecutionId`; `lastIn: 'terminal'` → terminal preselected and `RebuildActionSet` shown;
  - the 120 s notice, **in both modes**: `lastWriteAt = now - 30s` → the notice with 30 is visible together with the rebuild control of that mode (terminal: `RebuildActionSet`; worker: `terminated-rebuild-worker`), before anything is clicked; it is gone after advancing 90 s (fake timers); `lastWriteAt = now - 200s` → no notice;
  - a pane without `conversation` behaves exactly as before (existing tests stay green).
- [ ] **Step 2: Run** → FAIL. **Step 3: Implement.** **Step 4: Run** focused tests, then the full gate.
- [ ] **Step 5: Commit** `feat(spa): rebuild a terminal-last conversation from a closed-terminal pane (P4)`.

## Phase P4-2b — SPA: the 已退出 and 已消失 tabs

### Task 41: 已退出 on the endpoint

**Files:**
- Rewrite: `spa/src/components/settings/WorkerExitedTab.tsx`
- Create: `spa/src/components/settings/ConversationRow.tsx` (row used by both tabs), `spa/src/lib/nex/conversation-search.ts` (`matchesConversationQuery`, `shortenHome`)
- Modify: locales
- Test: `WorkerExitedTab.test.tsx` (rewrite), `WorkerExitedTab.retry.test.tsx` (adapt), `conversation-search.test.ts`, `ConversationRow.test.tsx`

**Interfaces:**
- Consumes: Task 39 `useConversations`, Task 40 `openConversationRebuild`, existing `openWorkerTab`, `relativeAge`. (The P2 tab's `useHostExecutions` subscription is dropped, R-4-12.)
- Produces: `matchesConversationQuery(row: ConversationRow, q: string, home: string): boolean` (case-insensitive substring over `title`, the displayed cwd **and** the raw cwd, `first_prompt`, `session_id`); `shortenHome(cwd: string, home: string): string` (`home` itself → `~`; `home + '/'` prefix → `~/…`; otherwise unchanged).

**Rows** (`ConversationRow`, `state: 'ended' | 'gone'`, `disabled?: boolean`):
- title; cwd via `shortenHome` (full cwd in `title=`); relative last activity with 「前」 (`settings.worker.conversations.age.*`, bucketed by `relativeAge`); a 上次在 chip `settings.worker.conversations.last_in_terminal`「上次在終端機」/ `last_in_worker`「上次在 Worker」.
- **ended** row action: `!row.cwd` → no button; `!row.cwd_exists` → the text「工作目錄已不存在」and no button; `page.root_error` → button disabled (R-4-1); otherwise 重建… which calls:
  - `row.last_in === 'worker' && row.latest_execution_id` → `openWorkerTab({ kind: 'execution', executionId: row.latest_execution_id, host: hostId })`;
  - else → `openConversationRebuild(hostId, row)`.
- Tab states: `unavailable` →「這台主機沒有啟用 Nexen」; `error` → the existing error line + retry; `root_error` →「無法讀取對話檔目錄：{{error}}」(rows still shown); `truncated` →「超過 2,000 筆，較舊的沒有列出」; loading / empty as P2 (`aria-busy` on a retry with kept rows, as #1630).
- Search box as P2 (`worker-exited-search`), now over the four fields.

- [ ] **Step 1: Failing tests:** rows from a mocked hook (terminal-last and worker-last rows, 上次在 chips, `~` cwd, 「3 小時前」); search hits title / `~/` cwd / raw cwd / first prompt / session id, case-insensitive; rebuild routing (worker-last with stint → `openWorkerTab`; terminal-last → `openConversationRebuild`; worker-last without stint → `openConversationRebuild`); no-cwd and cwd-missing rows have no button; root error → notice + disabled buttons; truncated notice; unavailable; retry with kept rows → loading + `aria-busy`.
- [ ] **Step 2: Run** → FAIL. **Step 3: Implement.** **Step 4: Run** → PASS.
- [ ] **Step 5: Commit** `feat(spa): 已退出 lists every ended conversation from the daemon (P4)`.

### Task 42: 已消失 tab; remove the P2 Exited sources

**Files:**
- Create: `spa/src/components/settings/WorkerGoneTab.tsx`
- Modify: `spa/src/lib/register-modules/index.tsx` (`registerWorkerSettingsTab({ id: 'gone', labelKey: 'settings.worker.tabs.gone', order: 30, hostScoped: true, component: WorkerGoneTab })`)
- Delete (after confirming no other importer with a grep in the step): `spa/src/hooks/useExecutionHistory.ts`, `spa/src/lib/nex/exited-entities.ts`, `spa/src/lib/nex/terminal-session-ids.ts`, and their tests; remove `settings.worker.exited.in_terminal` and other keys no longer used (both locales).
- Test: `WorkerGoneTab.test.tsx`, `WorkerSettingsPage.test.tsx` (tab order 外觀 / Workers / 已退出 / 已消失)

- Rows: `ConversationRow` with `state="gone"`, `disabled`: `aria-disabled="true"` on the row, muted, no button, ending with「對話檔已清除，無法再啟動」(`settings.worker.gone.note`). Tab label `settings.worker.tabs.gone`「已消失」. Same search, notices (root error shows its notice and no rows, R-4-1), cap notice, unavailable state.
- [ ] **Step 1: Failing tests:** registry order and label; disabled rows with the note and no button; search; root error → notice, no rows; truncated; unavailable.
- [ ] **Step 2: Run** → FAIL. **Step 3: Implement**; delete the unused files. **Step 4: Run** the full gate.
- [ ] **Step 5: Commit** `feat(spa): 已消失 tab — conversations whose transcript was cleaned up (P4)`.

## Self-review record

**Spec coverage (§13):**

| Spec item | Task(s) |
|---|---|
| §13.0 U1 three states, U2 one tab per state, U3 disabled gone rows | 37, 41, 42 |
| §13.2 in scope (cli-born or stinted), states, gone sources | 37 |
| §13.3 Workers unchanged; 已退出 rows (title order, cwd ~, last activity, 上次在), order, rebuild; 已消失; search; cap; gating | 37, 39, 41, 42 |
| §13.4 preselection, terminal-last and worker-last routes, cwd missing / unknown, 120 s warning, owner checks at rebuild | 40, 41 (owner checks unchanged: `worker-rebuild` / resume) |
| §13.5 recomputed per scan, gone only after a successful listing, root error | 35a, 35b, 37, 38, 41, 42 |
| §13.6 endpoint and fields, root rule, symlink rules, head to the first prompt (16 MB, resumable, kept once found), tail window, index columns and upkeep, schedule, single flight, joins without page cap | 34–38 |
| §13.7 limits | documented in CHANGELOG at bump |
| §13.9 tests | 35a (readers: prompt beyond 64 KB, cap, cut lines, symlinks), 35b (grown file → tail only, resume, rewrite → head from 0), 37 (states), 38 (end-to-end fallback, index + scheduling + cap), 39–42 (SPA) |

**Placeholder scan:** none intended; every task names files, interfaces, tests and the commit.

**Type consistency:** `ConversationIndexRow` (34) → `conversations.Index` (35b) → `WithConversationIndex` (38); the join (37) takes materialized `IndexRows []pstore.ConversationIndexRow`, never the service; `ScanResult` (35b) → 37/38; `LiveSessions` (36) → 38; `conversationRow` JSON (37) and the page fields (38) = `ConversationRow` / `ConversationsPage` TS (39) field for field.

**Review Focus:** each line has its test in the owning task.

## Plan review record (codex `task-muwwcnvj-m7cuht`, gpt-5.6-sol, with spec §13 from origin/main)

25 findings. Adopted (plan changed): 2 (generation source; Rebuild all excludes an unknown generation as today, pinned), 3 (explicit leaf walk; `openSingletonTab` only creates), 4 / 12 (stat the latest stint's `transcript_path`, R-4-8), 6 (unreadable slug dir no longer fails the listing, R-4-7), 9 (the latest stint's title only, R-4-10), 10 / 11 (reconcile and batch tests), 13 (end-to-end title fallback test), 14 (warning in both modes, before acting), 15 (refresh on mount and retry only, R-4-12), 16 (`LiveSessions` keeps the `Verified` contract), 17 (repeated cursor test), 18 (flights run on a module ctx cancelled in `Stop`; no `List` after `Stop`), 19–21 (PRs split up front: 1a / 1b / 1c / 2a / 2b), 22–24 (wording, full `ListOptions` assertion, import-boundary scope).
Changed differently from the finding: 1 (critical — unverified frames): an unverified-only S is neither running nor listed; it is unknown and reported (`unknown_owner`), the fail-closed reading of D1 that `checkOwners` already applies (R-4-9).
Rejected with evidence: 5 and 8 — R-4-1's disabled rebuild and R-4-5 were approved by the coordinator on 2026-10-07. 25 — kept: 404 is what a host without the nex module answers today (`cmd/pdx/main.go:367-374`), and the plan only maps it to a message.
Sent to the coordinator: 7 (R-4-6's fallbacks are user-visible).

## Acceptance (after P4-1c is deployed on mlab, and again after P4-2b)

1. **Cold index cost** (coordinator, 2026-10-07): the first scan after the P4-1c deploy finds an empty index, so it reads every head and tail. Record from the daemon log line Task 38 writes: files, re-read, bytes read, duration. Expected near the prototype (416 files, 368 MB head + 89 MB tail, 1.9 s Python first run). Then record a second scan (≥ 5 s later, via a request): re-read count and duration (expected: only files written since).
2. `GET /api/nex/conversations?state=ended` and `?state=gone` on mlab: the counts, a sample of terminal-last and worker-last rows, `root_error` absent, `unknown_owner` 0.
3. After P4-2b, in the SPA (`playwright cli -s=conv-entity`, 5174, never put a token in `eval` / `run-code`): 已退出 lists terminal and worker conversations with 上次在; search; one terminal-last rebuild opens the 對話已結束 tab (throwaway conversation created for the test in a throwaway tmux, removed afterwards); 已消失 shows disabled rows (create one by removing a throwaway transcript after it was indexed).

