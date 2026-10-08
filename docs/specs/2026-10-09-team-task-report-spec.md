# Team tasks and reports (spec)

Date: 2026-10-09. Coordinator: purdex-1f (`mlab/_vqnjx1`). Status: **user decisions final (§1)**; plan pending.
Line: lead/member (A). Order (user, 2026-10-08): U23 → host resource lease → **this** → U24 adopt/release.
Research: `docs/research/2026-10-08-agent-teams-and-workflow.md` §2.3–§2.5, §5(b)(c).

## 1. User decisions (do not reopen)

| # | Decision |
|---|---|
| T0 | （2026-10-08）lead／member 要有「上下訊息規格」——這是 session 內部派 subagent 的優點（結構化回傳），要落實到 team。先研究官方 Agent Teams／workflow 再開發（#1966）。 |
| T1 | **派給 member 的任務存在 daemon**：lead 用 `pdx task` 派工，每筆有任務名稱、目標、完成定義、狀態；`pdx team` 看得到每個 member 正在做哪件；接力後延續。欄位命名照官方 Agent Teams。 |
| T2 | **member 每回合結束時只更新狀態、不發訊息**：mod 自動把「這回合最後一句結果」寫進任務狀態（`pdx team` 看得到），不打擾 lead；里程碑（待放行、已合併、卡住、要決定）仍由 member 主動回報。 |
| T3 | **合併前不做系統強制檢查**：審查流程（R1、攻擊方、critic、lead 放行）維持靠 brief 與 skill，mod 不擋 `gh pr merge`。 |

## 2. Facts

- **Official task model** (Agent Teams, research §2.3, measured): one record per task with `id, subject, description, activeForm?, status (pending | in_progress | completed | deleted), owner, blocks[], blockedBy[], metadata`; "blocked" is derived from unfinished `blockedBy`, never stored; claim = write `owner` + `in_progress` under a lock; the official list is deleted when all tasks complete (Purdex keeps history instead).
- **Official messaging**: inbox entries `{from, text, summary (short), timestamp, msg_id, msgV, type}`; protocol messages ride as JSON in `text`; `idle_notification` with the turn's final answer is **sent by the system at turn end** (T2 keeps the automation but not the message).
- **Purdex today**: `pdx spawn --brief-file` sends the brief as the member's first message; members report with free-text `pdx msg send`; `team.db` holds `teams`, `team_members` (with model／effort／context readings), `relay_ops`, `session_lineage`; refs survive relay through lineage (`was _xxxxxx`); the team roster (`GET /api/team/roster`, `team.roster` events, PL-1f′) is read by the Mac App team panel (interface lead `mlab/_b84f5i`).
- **The mod already sees turn ends** (`events.js` `turn.complete`), but the U1-1 event channel carries no conversation text (U1-5 adds content). Getting the turn's final text from inside a member's mod is a plan-time measurement (M-T1).

## 3. Model

**D-1 · Task.** Stored per team in `team.db`:
`{id, team_id, subject (≤ 80 chars), description (markdown brief), done_when[] (short lines), status, owner (member ref), blocks[], blocked_by[], created_by (lead ref), created_at, updated_at, metadata {branch?, prs[]?, shas[]?}, last_report {kind, summary, at}?, last_turn {summary, at}?}`.
- `id`: `<team short id>-<n>` (n per team, monotonic; no `.highwatermark` file).
- `status`: `pending | in_progress | completed | deleted` (official names); `blocked` derived from `blocked_by`.
- `owner` is a ref; after a relay it resolves through lineage, so the task follows the member (`pdx task mine` on the new session lists it).
- Completed and deleted tasks stay (history); `pdx task ls` hides them unless `--all`.

**D-2 · Assignment (down).** `pdx task add --to <ref> --subject … [--brief-file f | --brief text] [--done-when …]… [--blocked-by <id>]…` (lead only; `not_lead` / `not_your_member` refusals as `pdx kill`). The daemon stores the task and sends the member one message: `[pdx task <id>] <subject>` + the brief + the done-when lines + how to report (one line). `pdx spawn` gains `--task-subject` / `--done-when` so a spawn's brief becomes task #1 for that member. A task is `pending` until the member's first report or `pdx task start <id>` (→ `in_progress`).

**D-3 · Reports (up).** `pdx report <kind> [--task <id>] --summary "<≤ 200 chars>" [kind fields] [--file <md> | --text …]`, member only (a lead may report to nobody). Kinds and required fields:

| kind | meaning | required | task effect |
|---|---|---|---|
| `ack` | received, starting | — | `in_progress` |
| `progress` | a step done | — | — |
| `question` | needs a decision | `--needs lead|user` | — |
| `ready` | PR reviewed, asking to merge | `--pr`, `--reviews "<stage>=<job id>"…`, findings table in `--file` | `metadata.prs += pr` |
| `merged` | merged | `--pr`, `--sha` | `metadata.shas += sha` |
| `blocked` | cannot proceed | `--needs lead|user` | — |
| `done` | done-when met | — | `completed` |

The CLI validates the fields per kind (exit 2 with the missing field named), stores the report, and sends the lead one peer message: header line `[report <kind> <task id>] <summary>` then the body (human-readable; the structured data lives in the daemon, read with `pdx report ls` / `pdx task show`). `--task` defaults to the member's only `in_progress` task. Peer wire is unchanged (no envelope field added).

**D-4 · Automatic last-turn status (T2).** In a **member** session, the mod, at each main `turn.complete`, takes the turn's final assistant text, trims it to one line (≤ 200 chars, first sentence preferred), and records it as `last_turn` of the member's `in_progress` task — through the daemon, **never as a message**. No task in progress → record on the member row instead. Never in lead or solo sessions. Fail-open; at most one write per turn; no fork per turn if the plan can avoid it (M-T1, #1777 lesson).

**D-5 · Lead side.** `pdx task ls [--member <ref>] [--all] [--json]`, `pdx task show <id>` (brief, reports, last turn), `pdx task done|delete <id>`, `pdx task reassign <id> --to <ref>`. `pdx team` gains a **TASK** column (`<id> <status> <subject…>`) and a **LAST** column (age of `last_turn` or last report). `pdx report ls [--task <id>] [--since 1h]`.

**D-6 · Not enforced (T3).** No gate on `gh pr merge`; the `ready` kind's `--reviews` is a record, not a check. The skill keeps the review flow and asks members to send `ready` before merging and to wait for the lead's go-ahead.

**D-7 · Roster.** The roster gains `task {id, subject, status}` per member (optional field) so the Mac App and iOS can show what each member is on. Whether and where the team panel shows it is the interface lead's call; the Haiku-written "current task" sentence (team-interface item 19) is a separate, later discussion — this field is only the lead's own subject line.

## 4. Phases (each PR ≤ 800 lines or ≤ 20 files)

| Phase | Content | Deploy |
|---|---|---|
| **T-1** | team.db `tasks` + `reports`; API; `pdx task add／ls／show／start／done／delete／reassign`; `pdx report` with per-kind validation; message composition; `pdx team` TASK／LAST | daemon + CLI |
| **T-2** | `pdx spawn --task-subject／--done-when` (brief → task #1); `pdx task mine`; skill text (lead assigns with `pdx task`, member reports with `pdx report`, the relay notice lists the member's tasks) | daemon + CLI + `pdx setup` |
| **T-3** | mod automatic `last_turn` for members (D-4) after M-T1; roster `task` field (D-7) | daemon + mod (`pdx setup`) |

## 5. Coordination

- **mod** (T-3): new file, registered with a matcher (`turn.complete{turnId:/^/}` is already taken by `events.js` with that matcher — two registrations with matchers coexist; confirm the guard test), `register.js` gets one line; tell the interface lead (`mlab/_b84f5i`) before touching `register.js` — δ (resource lease P2) and its team-interface item 14 also add one line each.
- **roster** (D-7): γ's PL-1f′ code; additive field.
- **host resource lease**: unrelated tables; no conflict.

## 6. Plan-time measurements

- **M-T1** how a mod gets the main turn's final assistant text at `turn.complete` (candidates: `$.session.messages`, `$.session.turns`, `turn.step` accumulation in the reporter) and its cost; how the mod learns it is a member (the relay `hello` role, already cached by `register.js`).
