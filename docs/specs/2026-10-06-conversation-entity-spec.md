# Conversation entity — one conversation, one thing, in one state — spec

Status: **approved by the user 2026-10-06**, including the coordinator's derivations in §4.3, §5, §6, §8, §9 and §11. It goes to codex review together with the plan. Coordinator: `mlab/purdex-9b` (`mlab/_0le0d2`). Implementer: `mlab/purdex-19` (`mlab/_oecdo4`). Origin: the user felt that a worker "never goes away", while a terminal agent has a clear lifecycle: cld-yolo running means it exists, exiting means it is gone, resuming brings it back (2026-10-06).

## 1. Goal

A Claude Code conversation is **one thing** to the user. Terminal and worker are two **states** of it, never two things. Exiting a worker feels like exiting cld-yolo: it leaves the lists, and it can be brought back.

## 2. User decisions (2026-10-06, do not reopen)

| # | Decision |
|---|---|
| E1 | **One conversation (one claude session) is one entity.** `/clear` starts a new entity; `/resume X` switches to entity X. |
| E2 | An entity is in exactly **one state** at a time: in a terminal, in a worker, dormant (its transcript exists but nothing owns it), or gone (the transcript was cleaned up). |
| E3 | An entity in a terminal does **not** appear in worker lists. No execution record is created when a terminal starts. |
| E4 | A worker has **one "exit"** that means what exiting cld-yolo means. After exit the tab shows only a rebuild screen and the worker leaves the lists. It can still be found: §9 "Exited" tab, later search and a dormant list. |
| E5 | A worker can be opened into a tab only while it is not in a terminal and not exited. **Closing a tab is not exiting**, just as closing a tmux tab does not exit the agent. |
| E6 | Every conversation renders from the **full transcript, with Nexen data as supplement**. |
| Q1 | A manual `claude --resume` of a session that a live worker holds **exits that worker**: the conversation has moved to the terminal. |
| Q2 | A failed start is **kept**. Starting means entering a tab pane; on failure the pane shows a "start failed" state that looks like the rebuild / disconnected screen. |
| Q3 | **New Tab**: a "Sessions / Workers" switch in each host's header row, where Workers lists live workers. **Settings → Worker**: tabs, with per-host lists including exited (and later dormant) entities. The tabs must leave room for later "workers inside Aigora", pulling an Aigora chat/worker back under control, and deeplinks. |
| Q4 | Rebuild offers **terminal or worker**, with the **original mode preselected**. Once Nexen is enabled, a closed terminal pane's rebuild offers the same choice. |
| Q5 | The activity bar's worker list (shipped alpha.478) **stays**, listing live workers only, like the New Tab switch. |

## 3. Facts (measured 2026-10-06, origin/main `6cbc29f7` alpha.486, Nexen v0.16.1, Claude Code 2.1.289)

### Nexen

- **Between turns nothing runs.** Nexen spawns `claude -p --resume` per turn (`nexen/adapter/claude.go:281`). An execution is a ledger: an `executions` row plus `turns` (one process each) plus `events` (what the worker pane draws). The conversation itself lives in claude's transcript file.
- States are `queued | running | idle | rejected | failed | terminated`. The last three have no way out (`nexen/store/execution.go:53-57`).
- "Archived" is a separate visibility flag (`archived_at`):
  - terminate never archives;
  - archive is refused while running (409 `archive_while_running`);
  - an archived execution refuses `send` (409 `execution_archived`).
- Nexen sees only executions it created. It never scans for transcripts; it reads a transcript only when given cwd + session id.

### Purdex — terminal side

The terminal side is Purdex's (CC hooks → `internal/module/agent`).

**Hooks** (live probe in a throwaway tmux):

| Action | Hook events |
|---|---|
| Start | `SessionStart{source:"startup", session_id}` |
| `/clear` | `SessionEnd{reason:"clear"}` (old id), then `SessionStart{source:"clear"}` with a **new** id |
| `/exit` | `SessionEnd{reason:"prompt_input_exit"}` |
| Resume | `SessionStart{source:"resume"}` |

**Every SessionStart and SessionEnd carries `transcript_path`.** Purdex stores it in the trace (`agent_trace_chains.root_payload_json.raw_event`), but provenance extracts only `session_id` and `cwd`.

**Frames** (`agent_frames`, one row per agent run in a pane):
- `/clear` replaces the pane's frame (new `frame_id`, new `session_id`);
- `/exit` deletes it.

So *a live frame with session id S* already means *S is in a terminal*.

### Purdex — worker side

- **Re-handoff always creates a new execution** (P-C spec Q2). Take-back and take-to-terminal archive the execution as the "already taken" lock.
- Take-to-terminal leaves the execution `idle` + archived, never terminated (`internal/module/nex/take_to_terminal.go:171-182`).
- **Handoff writes a synthetic turn into the conversation.** Delegate needs a brief, so handoff sends `"(handed off from tmux session <name>)"` as turn 1, and the model answers it (`internal/module/nex/handoff.go:231`; P-C acceptance shows the reply `ok`). Every switch adds a fake exchange and costs a model call.
- **The worker list has two display bugs:**
  - it reads `listExecutions({includeArchived:false, limit:100})` (`spa/src/lib/nex/execution-list-effects.ts:112`), but Nexen orders by id, oldest first, and the SPA never pages, so beyond 100 rows the **newest** are missing;
  - the state dot colours differ between the list and the pane header.

### Purdex — the existing rebuild screen

The existing rebuild screen for a terminal pane whose tmux session is gone is `TerminatedPane` + `RebuildActionSet` (session name, cwd and resume-command rows; recreate tmux, then resume the agent).

## 4. The model

### 4.1 Identity

An entity is **(host, provider `claude`, session id)**. Its transcript is located by the hook's `transcript_path` (terminal side) or Nexen's `transcript_path` (worker side). Neither guesses a path from cwd.

### 4.2 States, and how each is known

| State | Known from |
|---|---|
| **terminal** | A live Purdex frame on the host with `session_id = S` |
| **worker** | A Nexen execution on the host for S that is **not archived and not `terminated`** (`queued`, `running`, `idle`, and also `failed` / `rejected` until exited, per Q2) |
| **dormant** | No owner, transcript present |
| **gone** | No owner, transcript absent |

An execution is "for S" when its `session_id` or `resume_session_id` is S. Several executions can be for one S: they are the entity's past worker stints.

### 4.3 One state at a time — the owner lock

The old lock ("archived means taken") becomes an **owner check**. A transfer of S may start only when S's current owner is the one being transferred, and nothing else owns S.

- **Terminal → worker (Hand to nex):** CC exits (its frame goes), then the worker starts.
- **Worker → terminal (Take to terminal / take back):**
  1. the terminal resume succeeds;
  2. then the worker **exits** (§5): terminate + archive, instead of today's idle + archived.
  3. If the resume fails, nothing is exited. Today's unarchive-on-failure rollback becomes "never exited in the first place".
- **Manual resume (Q1):** a `SessionStart{source: resume | startup}` for S in any pane, while a worker for S is live **and no Purdex transfer of S is in flight**, exits that worker (§5). Toast: "<name> 已在終端機接續，worker 已退出". Purdex's own transfers mark S as in flight before they resume, so their own hook event is not mistaken for a manual resume.

## 5. Exit — the one action

- Worker pane header and list rows offer **"退出"**. It replaces "terminate" and "archive" in every worker-facing UI. The Host → Nex admin table keeps its raw controls as an admin view.
- What exit does, by state:

  | State | Steps |
  |---|---|
  | `running` | Confirm ("這一輪會被中斷"), then terminate, then archive |
  | `idle` / `queued` | Terminate, then archive |
  | `failed` / `rejected` | Archive |
- After exit, the worker's open tabs show the **exited screen** (§7) and the worker leaves the lists.
- Exit is idempotent. If terminate succeeded but archive failed, the worker is shown as exited (a terminated execution is not live under §4.2), and archive is retried on the next exit or list refresh.

## 6. Start failure (Q2)

A worker whose execution is `failed` or `rejected` and not exited:
- stays in the lists with a red dot;
- its pane shows a **"啟動失敗"** screen: the reason (`reject_reason` / `terminal_reason`) and the rebuild choices of §7, on the same layout as the rebuild screen.

Exiting it removes it from the lists.

## 7. Rebuild screens (Q4)

One layout, based on `TerminatedPane`, used for:
- an exited worker pane;
- a failed worker pane;
- a terminal pane whose tmux session is gone.

**The choice.** A mode choice, **terminal** or **worker**, with the original mode preselected.

The worker option is offered only when the host's Nexen is ready and a session id is known:
- a terminal pane takes it from its rebuild record;
- a worker pane takes it from the execution.

**Terminal rebuild** is today's engine: create a tmux session, then resume (`claude --resume S` through the resume templates). For a worker origin:
- cwd = the execution's cwd;
- session name = a generated one, the same way take-to-terminal names one;
- the pane becomes a terminal pane.

**Worker rebuild** starts a new worker stint for S in the same pane:
- same cwd;
- profile from the original handoff / delegate when known, else the handoff default;
- the pane follows the new execution.

## 8. Switching must not write into the conversation

A mode switch changes the owner, not the conversation. So handoff and worker rebuild must **not** send a synthetic prompt.

- **Nexen (new, v0.17):** `delegate` with `resume_session_id` and **no brief** creates the execution in `idle` with no turn. It waits for the first real message.
  - `transcript_prelude` and `session_id` are set from `resume_session_id` at creation.
  - Capability-detected.
- **Purdex:** when the capability is present, handoff and worker rebuild delegate without a brief. When it is absent (an old daemon), today's placeholder brief stays.

## 9. Lists (Q3, Q5)

- **Live list.** "Live" means state worker under §4.2, one row per entity (the latest live stint).
  - Shown in: the activity bar list (kept), and the New Tab per-host Sessions / Workers switch.
  - `failed` / `rejected` rows are red until exited.
- **Settings → Worker** gets tabs, per host:
  - **Workers**: live, same as above.
  - **Exited**: entities whose latest stint was exited, newest first; rebuild from here.
  - Later tabs: **Dormant** (§11 P4); Aigora (out of scope here). The tab bar is a registry, so a later tab plugs in without reshaping the page.
- **Fetching:** follow Nexen's cursor until done (bounded), so the newest rows are never cut off. One dot-colour mapping is shared by the list and the pane header.

## 10. Rendering (E6) — outline, specified in full before P3

- **History** comes from the transcript, from the start to now. It covers terminal and worker stints alike: one timeline, segment markers where the entrypoint changes. That generalises the prelude.
- **The in-flight turn** comes from the live event stream.
- **Nexen data supplements:**
  - tool status / duration by `tool_use_id`;
  - per-turn cost by the user-prompt line;
  - across **all** stints of the entity (earlier executions too).
- **Nexen (v0.17):** a transcript page endpoint bounded by "now" instead of `prelude_end`, with the same item kinds and caps as the prelude.
- **When the transcript is gone** (cleaned up after 30 days), worker stints fall back to their event logs.

## 11. Phases (one phase = one PR ≤ 800 lines / 20 files; split further when larger)

| Phase | Repo | Content |
|---|---|---|
| P1a | Purdex | Owner lock (§4.3) including manual-resume exit (Q1); take-to-terminal / take-back exit the worker; extract `transcript_path` into provenance |
| P1b | Purdex | Exit action (§5); live-list filtering, per-entity dedupe, cursor paging, one dot mapping (§9) |
| P1c | Purdex | Exited and start-failed screens; rebuild mode choice for worker and terminal panes (§6, §7) |
| P2 | Purdex | New Tab Sessions / Workers switch; Settings → Worker tabs (Workers, Exited) |
| P3a | Nexen v0.17 | Delegate without a brief (§8); transcript-to-now endpoint (§10) |
| P3b | Purdex | Pin v0.17; handoff / rebuild without a synthetic turn; transcript-first rendering |
| P4 | Purdex | Dormant entities: daemon scans transcripts' metadata (`ai-title`, cwd, last activity) per host; Dormant tab + search |

## 12. Not in scope

- Aigora integration (only the Settings tab registry is shaped for it).
- Providers other than claude.
- Agents running outside Purdex's tmux.
- Hot-reloading anything.
