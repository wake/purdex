# Conversation entity — one conversation, one thing, in one state — spec

Status: **approved by the user 2026-10-06**, including the coordinator's derivations in §4.3, §5, §6, §8, §9 and §11. The implementer's derivations D1–D16 (blocks marked 統籌核准的推導, 2026-10-06) were approved by the coordinator; D16 is reported with the plan. It goes to codex review together with the plan. Coordinator: `mlab/purdex-9b` (`mlab/_0le0d2`). Implementer: `mlab/purdex-19` (`mlab/_oecdo4`). Origin: the user felt that a worker "never goes away", while a terminal agent has a clear lifecycle: cld-yolo running means it exists, exiting means it is gone, resuming brings it back (2026-10-06).

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

> **統籌核准的推導（2026-10-06）**
> - **D1 How the daemon tells the owner.** Terminal: an `agent_frames` root row with agent type `cc`, `session_id = S`, whose pid is alive and still has the recorded start time. Worker: a Nexen execution that is not archived, not `terminated`, and has `session_id = S` or `resume_session_id = S`. Nexen v0.16.1 `List` has no session filter, so the daemon pages through the non-archived rows (500 per page, at most 20 pages). P3a adds a session-id filter to Nexen `List`; the daemon switches to it once pinned.
> - **D9 Entity key.** `session_id || resume_session_id || id`. When several stints share a key, the latest one (largest `created_at`, then `id`) represents the entity.
> - **D13 `transcript_path`.** Stored in a new `agent_frames.transcript_path` column, written together with the session identity (every CC hook event carries it). It is also carried in the provenance envelope and in the `/provenance` response. The SPA starts reading it in P3b / P4.

### 4.3 One state at a time — the owner lock

The old lock ("archived means taken") becomes an **owner check**. A transfer of S may start only when S's current owner is the one being transferred, and nothing else owns S.

- **Terminal → worker (Hand to nex):** CC exits (its frame goes), then the worker starts.
- **Worker → terminal (Take to terminal / take back):**
  1. the terminal resume succeeds;
  2. then the worker **exits** (§5): terminate + archive, instead of today's idle + archived.
  3. If the resume fails, nothing is exited. Today's unarchive-on-failure rollback becomes "never exited in the first place".
- **Manual resume (Q1):** a `SessionStart{source: resume | startup}` for S in any pane, while a worker for S is live **and no Purdex transfer of S is in flight**, exits that worker (§5). Toast: "<name> 已在終端機接續，worker 已退出". Purdex's own transfers mark S as in flight before they resume, so their own hook event is not mistaken for a manual resume.

> **統籌核准的推導（2026-10-06）**
> - **D2 In-flight marker.** A per-session-id lock `sid:<S>` in the shared `HandoffLocks`. Handoff, take-back, take-to-terminal and worker rebuild hold it from the moment S is known until they return. The Q1 handler takes the same lock with `TryLock`; when it is held, a Purdex transfer is in flight and the event is skipped.
> - **D3 Q1 trigger.** A `SessionStart` with `source` `startup` or `resume` that was granted a provenance envelope (a verified root frame). The handler runs asynchronously and checks again that S **now** has a live terminal frame, so a late hook from a session that a failed transfer already killed does not exit the worker. It then exits every live worker for S and broadcasts the host event `nex-worker-exited {execution_id, session_id, reason: "manual_resume", tmux_session}`. The SPA toast names the worker by its list label, or the tmux session name when the label is unknown. A worker's own `claude -p` hooks never reach this path: Nexen's child environment is an allowlist (`PATH HOME SHELL TMPDIR LANG`) without `TMUX_PANE`, so the hook request is refused as `schema_invalid`.
> - **D5 Take-to-terminal and take-back order.** Resume first, exit the worker after the resume succeeds. Two things replace the old "archive first" fence:
>   1. the transfer holds the control lease from start to end (the caller's, else acquired, else borrowed under D4), so nobody can send into the worker during the resume;
>   2. the `exec:<id>` and `sid:<S>` locks plus the owner check stop a second resume.
>
>   A failed resume exits nothing; the unarchive rollback is removed. The response gains `exited` and keeps `archived` for compatibility.
> - **D6 Take-to-terminal means "bring this conversation to a terminal".** It also accepts an exited execution (archived or terminated) and a `rejected` one, which is what P1c's "rebuild as terminal" on a worker pane calls. The owner check runs first: S already in another terminal answers 409 `session_in_terminal` (this replaces `execution_archived` as the double-submit guard). Only an execution that was live is exited after the resume. `queued` is still refused.
> - **D7 Handoff.** Before CC is stopped: a live worker for S answers 409 `session_owned {owner: "worker", execution_id}`; S live in a terminal frame of another pane answers 409 `session_owned {owner: "terminal"}`. When the delegate is rejected:
>   - rolled back: the daemon exits (archives) the rejected execution, so S is back to the one state terminal;
>   - not rolled back: the rejected execution is kept (start failed, red in the lists), and the response carries `execution_id` so the SPA turns the pane into that worker's start-failed screen (P1c).

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

> **統籌核准的推導（2026-10-06）**
> - **D4 Exit lives in the daemon:** `POST /api/nex/executions/{id}/exit {lease_id?}`, steps as in the table above.
>   - Nexen's terminate always needs the control lease, even when idle, and an open worker pane keeps renewing its lease (TTL 120 s). So when the lease is held by **another Purdex client** (a principal `pdx:<host>` or `pdx:<host>/<client>`), the daemon terminates under that holder's current lease: the daemon mints every pdx principal, and exit must not fail because another tab holds control.
>   - When the holder is **not** a pdx principal (for example a Ploom agent token), exit answers 409 `held_by` with the holder, the UI shows "被 <principal> 控制中", and nothing is forced.
>   - A failed terminate does not block the archive. "Exited" means terminated or archived; neither is live under §4.2.
>   - The running confirmation is UI-only; the daemon takes no flag (Q1 interrupts a running turn on its own).
> - **D16 Archive retry happens on the next exit only, not on a list refresh.** A terminated, unarchived row is already not live: the live lists drop it and the Exited tab counts it. A list refresh that archived every terminated row would also archive the Host → Nex admin table's raw terminates. *(Narrowing of the sentence above; reported to the coordinator with the plan.)*

## 6. Start failure (Q2)

A worker whose execution is `failed` or `rejected` and not exited:
- stays in the lists with a red dot;
- its pane shows a **"啟動失敗"** screen: the reason (`reject_reason` / `terminal_reason`) and the rebuild choices of §7, on the same layout as the rebuild screen.

Exiting it removes it from the lists.

> **統籌核准的推導（2026-10-06）** A handoff whose delegate was rejected and whose terminal was **not** rolled back lands here: the pane becomes that worker's start-failed screen (§4.3 D7). A rejected handoff that **was** rolled back does not; its execution is exited.

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

> **統籌核准的推導（2026-10-06）**
> - **D6** A worker pane's terminal rebuild calls take-to-terminal, which accepts exited and `rejected` executions (§4.3 D6).
> - **D11 Worker rebuild endpoint:** `POST /api/nex/worker-rebuild {session_id, cwd, profile?, replace_execution_id?}`.
>   1. Take the `sid:<S>` lock.
>   2. Owner check: S must not be in a terminal, and no worker for S may be live except `replace_execution_id`.
>   3. Exit the replaced execution first when it is live (a `failed` / `rejected` one).
>   4. Delegate a resume of S. Until P3b it sends the placeholder brief `(rebuilt as worker)`.
>
>   Profile: the SPA sends the original execution's `effective_profile`; without one the daemon uses `handoff`. A terminal pane's worker rebuild uses the same endpoint with the cwd from its rebuild record. Without a cwd the worker option is not offered.
> - **D12 Preselection** is the pane's current mode: a worker pane preselects worker (a failed handoff's worker pane included), a terminal pane preselects terminal.

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

> **統籌核准的推導（2026-10-06）**
> - **D8 Dot colours** follow the terminal agent badge: running green (`status-success`), queued yellow (`status-warning`), idle grey (`text-muted`), failed / rejected red (`status-error`), terminated grey. So the list's idle moves from amber to grey, and the header's terminated moves from red to grey.
> - **D9 Paging:** the live list asks for `include_archived=false`, 500 rows per page, at most 20 pages.
> - **D10 Exited tab (P2):** entities whose latest stint is exited. When the SPA knows that the session id is running in a terminal on that host (agent records), the row is marked "在終端機中" and offers no rebuild; the daemon's owner check refuses one anyway.

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

> **統籌核准的推導（2026-10-06）**
> - **D14 Finer split** (each ≤ 800 lines / 20 files):
>
>   | PR | Content |
>   |---|---|
>   | P1a-1 | agent module: `transcript_path`, terminal lookup by session id, SessionStart subscription |
>   | P1a-2 | nex exit: live workers for S, `exitWorker`, the `/exit` endpoint |
>   | P1a-3 | transfers: `sid:<S>` lock, owner checks, take-to-terminal / take-back / handoff rewrites, Q1 |
>   | P1b-1 | lists: paging, live-list dedupe, shared dot colours |
>   | P1b-2 | exit UI and the Q1 toast |
>   | P1c-1 | `worker-rebuild` endpoint; the worker pane's exited and start-failed screens |
>   | P1c-2 | the terminal pane's rebuild choice |
>   | P2 | may split in two |
>
>   Every PR is merged and bumped as usual. **Daemon deploys (restarting mlab) are batched:** when a deployable set has accumulated, the implementer reports to the coordinator, who schedules the restart with the user together with the other daemon lines.
> - **D15** This round's plan covers P1a–P2. P3a / P3b / P4 stay an outline until the coordinator completes §8 / §10; then plan v2 goes through one codex round.
> - **P3a scope addition:** a `session_id` filter on Nexen `List` (D1 switches to it once pinned).
> - Measured fact for P3a: in v0.16.1 an empty brief is already accepted, but it still creates turn 1 with an empty user message, and the store has no way to create a row directly in `idle`.

## 12. Not in scope

- Aigora integration (only the Settings tab registry is shaped for it).
- Providers other than claude.
- Agents running outside Purdex's tmux.
- Hot-reloading anything.
