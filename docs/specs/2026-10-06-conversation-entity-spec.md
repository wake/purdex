# Conversation entity — one conversation, one thing, in one state — spec

Status: **approved by the user 2026-10-06**, including the coordinator's derivations in §4.3, §5, §6, §8, §9 and §11. The implementer's derivations D1–D16 (blocks marked 統籌核准的推導, 2026-10-06) were approved by the coordinator; D16 was approved with the plan. It goes to codex review together with the plan. Coordinator: `mlab/purdex-9b` (`mlab/_0le0d2`), from 2026-10-06 `mlab/purdex-18` (`mlab/_giihrj`). Implementer: `mlab/purdex-19` (`mlab/_oecdo4`), then `mlab/purdex-4a` (`mlab/_e5vtux`), then `mlab/purdex-7c` (`mlab/_40iueq`). §13 (P4) was written by the coordinator `mlab/purdex-18` on 2026-10-06. Origin: the user felt that a worker "never goes away", while a terminal agent has a clear lifecycle: cld-yolo running means it exists, exiting means it is gone, resuming brings it back (2026-10-06).

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
> - **D6 Take-to-terminal means "bring this conversation to a terminal".** It also accepts an exited execution (archived or terminated) and a `rejected` one, which is what P1c's "rebuild as terminal" on a worker pane calls. The owner check runs first: S already in another terminal answers 409 `session_owned {owner: "terminal"}` — the same code family as D7, so the SPA maps one code (this replaces `execution_archived` as the double-submit guard). Only an execution that was live is exited after the resume. `queued` is still refused.
> - **D22 Transfers preempt; they do not borrow (supersedes "borrowed under D4" in D5(a)).**
>   - When another **pdx** client holds the lease, take-to-terminal and take-back release that holder's lease as the holder (`ReleaseLease` with its lease id and principal). They then acquire an **exclusive** lease under the daemon's own principal.
>   - A borrowed lease cannot stop its original holder from sending into the worker between the resume and the exit; an exclusive one can (PR #1586 review).
>   - The preempted tab's next send or renew fails. Its re-attach sees `held_by` until the transfer ends.
>   - When the transfer fails (the resume did not succeed, nothing was exited), the daemon **always releases** the lease it took, so the original tab can re-attach and regain control.
>   - On success the worker has exited and the lease ends with it.
>   - Exit (§5, D4) keeps borrowing, because it ends the worker anyway.
>   - A non-pdx holder still gets 409 `held_by`, and nothing changes.
>   - Also from that review:
>     - the owner check runs once more right before the resume keys;
>     - on success the daemon waits up to 3 s for the verified terminal frame before releasing `sid:<S>`;
>     - a 10 s "just resumed by Purdex" marker makes the owner check treat S as in a terminal.
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
- Exit is idempotent. If terminate succeeded but archive failed, the worker is shown as exited (a terminated execution is not live under §4.2), and archive is retried on the next exit (D16: not on a list refresh).

> **統籌核准的推導（2026-10-06）**
> - **D4 Exit lives in the daemon:** `POST /api/nex/executions/{id}/exit {lease_id?}`, steps as in the table above.
>   - Nexen's terminate always needs the control lease, even when idle, and an open worker pane keeps renewing its lease (TTL 120 s). So when the lease is held by **another Purdex client** (a principal `pdx:<host>` or `pdx:<host>/<client>`), the daemon terminates under that holder's current lease: the daemon mints every pdx principal, and exit must not fail because another tab holds control.
>   - When the holder is **not** a pdx principal (for example a Ploom agent token), exit answers 409 `held_by` with the holder, the UI shows "被 <principal> 控制中", and nothing is forced.
>   - A failed terminate does not block the archive. "Exited" means terminated or archived; neither is live under §4.2.
>   - The running confirmation is UI-only; the daemon takes no flag (Q1 interrupts a running turn on its own).
> - **D16 Archive retry happens on the next exit only, not on a list refresh.** A terminated, unarchived row is already not live: the live lists drop it and the Exited tab counts it. A list refresh that archived every terminated row would also archive the Host → Nex admin table's raw terminates. *(Approved by the coordinator 2026-10-06; the sentence above is rewritten to match.)*
> - **D23 Exit archives under the terminate's lease (Nexen ≥ v0.18.0, issue nexen#113).** When `exitWorker` terminated the execution under a control (the caller's transfer control, a control it took itself, or the one it re-took after a lease loss), the archive that follows carries that same control's `LeaseID` and `PrincipalID`. Nexen then checks the lease and writes the archive in one UPDATE, so a lease that changed hands after the terminate can no longer be archived over. The archive's `principal_id` in the `execution.archived` event is therefore the lease holder's (always a pdx principal, D4), not the HTTP caller's. Every other archive is unchanged and carries no lease: rows that need no terminate (`failed` / `rejected` / already terminated), a terminate that answered `ErrExecutionTerminal`, and the D4 'terminate failed, archive anyway' path. A fenced archive refused with a lease error after a successful terminate leaves the worker exited (terminated, not archived): it is logged, and the archive is retried on the next exit (D16), as any archive failure after a terminate already is.

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

> **統籌（2026-10-06）完整版**：本節由統籌 `mlab/purdex-9b` 撰寫，取代原 outline；§10 的方案由使用者於 2026-10-06 拍板（早段＝對話檔＋各自 execution 的事件補充；當前 stint＝事件紀錄；不做 transcript-to-now）。

### 8.1 Facts

- Handoff delegates with the placeholder brief `"(handed off from tmux session <name>)"`. Delegate turns the brief into turn 1, so the model answers it (`internal/module/nex/handoff.go:231`; nexen `execution/service.go:658-668`). Every switch adds a fake exchange to the transcript and costs a model call.
- **An empty brief is not "no turn".**
  - Delegate accepts `brief: ""` (nexen `store/execution.go:322-327`) and still creates and launches turn 1 with a `{text:""}` block (`execution/message.go:37-39`; `adapter/claude.go:283-288`).
  - The API is expected to reject an empty text block (`message.go:26-29`, inference).
  - The contract already defines `brief:""` as "this execution truly had no brief" (capability-matrix "what the human said").
  - An old daemon would silently ignore a new field and run that turn anyway.

  So the new behaviour needs **its own explicit field, behind a capability** (fail-closed, capability-matrix §0).
- The launch path needs no change for a first turn that comes from `send`:
  - a `send` on an execution with no turns creates turn idx 1 (`store/turn.go:305-314`);
  - idx 1 already gets the resume branch (`launch.go:317-336`), the prelude measurement (`launch.go:756-771`), the turn-1 fatal rule (`launch.go:31-40`) and the reconcile rule (`reconcile.go:157-170`).
  - `idle → running` is a legal transition, and `ClaimTurn` / `CreateTurn` accept `idle`.

### 8.2 Nexen v0.17 — `start_idle`

**Request:** `POST /v1/executions` takes an optional `start_idle: true`.

**Preconditions**, else 400 `start_idle_conflict` naming the field:

| Field | Requirement |
|---|---|
| `resume_session_id` | Present |
| `provider` | `claude` |
| `brief` | Absent or `""` |
| attachments | Absent (there is no turn to attach them to) |

**Admission is unchanged:**
- cwd allowlist, profile, the resume transcript `stat` and account selection all run as today;
- their rejections produce `rejected` exactly as today.

**Effect:**
- **Starts in `idle`.** The execution is created directly in `idle` (`create(..., StateIdle, ...)`). There is no turn and no advance, and `activity_since = created_at`.
- **`transcript_path` is set at creation.** It is `transcriptPath(provider, canonical cwd, resume_session_id)` when computable, else empty. `session_id` stays empty until the first launch, so "`session_id` is what the CLI reported" keeps its meaning. Titles therefore appear before the first turn (`title.go:91`).
- **The prelude boundary is measured at delegate.**
  - It is measured with `LastLineEnd` under the same 2 s bound and carried on `execution.delegated` as `transcript_prelude_bytes`, next to `start_idle: true` and `brief: ""`.
  - `Service.Prelude` resolves the boundary in this order:
    1. the first `execution.running` with `turn_idx == 1`;
    2. the `execution.delegated` measurement;
    3. the legacy scan.
  - So a `start_idle` execution never needs the legacy full-file scan.

  > **統籌核准的推導（2026-10-06）D19 — supersedes the order above.**
  >
  > `Service.Prelude` resolves the boundary in this order:
  > 1. the `execution.delegated` measurement;
  > 2. the first `execution.running` with `turn_idx == 1`;
  > 3. the legacy scan.
  >
  > Turn-1 launch keeps measuring, with no special case. A delegate-time measurement that failed (timeout or unreadable) is filled in by turn 1 instead of falling back to the full-file scan.
  >
  > Contract wording: "the boundary is the first successful measurement (delegated before turn 1); once set it never moves." This removes the conflict with capability-matrix :915 that the original order had: a later turn-1 measurement would have overridden the delegated one whenever the transcript grew in between.

  > **統籌核准的推導（2026-10-06）D21 — the window before any measurement.**
  > - Applies to a `start_idle` execution whose delegate-time measurement failed and which has no turn 1 yet.
  > - `Prelude` never falls back to the legacy full-file scan in that window.
  > - It answers with a **provisional** boundary: `LastLineEnd`, under the same 2 s bound, computed per request and not cached.
  > - The first successful measurement (turn 1) is the boundary from then on.
- **The first `send` creates turn idx 1** (`input_kind` `message`). Everything turn-1 applies from there: resume, measurement, fatal on a missing transcript → `failed`, reconcile.
- **Lease.** `send` still needs the control lease. A consumer's flow is delegate (`start_idle`) → attach (control) → send.
- **Capability:** `capabilities.delegate.start_idle: true`.

**Also in v0.17 — list by session.** `GET /v1/executions?session_id=<uuid>` (validated and lowercased like `resume_session_id`, 400 on a bad value) matches any of:
- `resume_session_id = S`;
- `session_id = S`;
- any turn with `session_id = S`.

It adds `CREATE INDEX IF NOT EXISTS` on `executions(resume_session_id)`, `executions(session_id)` and `turns(session_id)`, with no schema bump. Capability: `capabilities.list.session_filter: true`.

**Contract docs to update:**

`capability-matrix`:
- §0 `delegate` / `list`;
- §1 verbs;
- §1.8: point 5 and the "transcript gone before launch → failed" row, which now happens at the first `send` for `start_idle`;
- §1.10 titles before turn 1;
- §1.11: boundary order and the delegated measurement;
- §2:
  - #5 (`idle` may mean "never ran a turn");
  - #48 (the resume guarantee is paid at the first send);
  - #65 (legacy is not used for `start_idle`);
- §3 `execution.delegated` keys.

`consumer-guide`:
- §4 table;
- §6.3 the delegate → attach → send flow;
- §9.7;
- the change log.

### 8.3 Purdex

- **Handoff and worker rebuild** use `start_idle` when the host's Nexen advertises it, and the placeholder brief otherwise (today's behaviour). No other part of either flow changes. Owner lock, `sid:<S>`, and rollback stay as in §4.3 / D7 / D11.
- After a `start_idle` handoff, the pane shows the worker **idle with the composer ready**. Its history is the conversation exactly as it was (prelude up to the delegated boundary). The first turn runs when the user sends.
- The handoff confirm text drops "continues headless" wording that implies it starts working on its own. It says the conversation moves to a worker and waits for your next message.
- **Labels.** Until `list.session_filter` is available, Purdex-created executions (handoff, worker rebuild) carry the label `purdex.session_id=<S>`, and Purdex lists an entity's stints with `label.purdex.session_id=`. Once the filter is advertised, Purdex uses it, which also covers stints created by others.

> **統籌核准的推導（2026-10-06）**
> - **D17 Where the `purdex.session_id` label starts.** It is written by every Purdex delegate for S as soon as that code path exists: handoff in P1a-4 (Task 10) and `worker-rebuild` in P1c-1 (Task 19). The key passes Nexen's label rules (only `nex.` is reserved). Executions created from then on are listable by label before v0.17.
> - **D18 Labels are for listing stints, never for owner checks.** A label misses executions created before it existed, and executions created outside Purdex (headless launcher, `pdx nex`, Ploom). So the owner check and Q1 keep the full non-archived scan (D1) until P3b-1 pins v0.17, and then switch to `?session_id=`, which also matches `resume_session_id` and any turn's `session_id` (§8.2). The label is used only by P3b-2's stint list, and only while `list.session_filter` is absent.

## 9. Lists (Q3, Q5)

- **Live list.** "Live" means state worker under §4.2, one row per entity (the latest live stint).
  - Shown in: the activity bar list (kept), and the New Tab per-host Sessions / Workers switch.
  - `failed` / `rejected` rows are red until exited.
- **Settings → Worker** gets tabs, per host:
  - **Workers**: live, same as above.
  - **Exited**: entities whose latest stint was exited, newest first; rebuild from here.
  - Later tabs: **已消失** (gone, §13 P4; the P4 rewrite also widens Exited to every ended conversation); Aigora (out of scope here). The tab bar is a registry, so a later tab plugs in without reshaping the page.
- **Fetching:** follow Nexen's cursor until done, bounded by D9. Over the cap, the list says so: it shows the rows it fetched and a persistent notice. One dot-colour mapping is shared by the list and the pane header.

> **統籌核准的推導（2026-10-06）**
> - **D8 Dot colours** follow the terminal agent badge: running green (`status-success`), queued yellow (`status-warning`), idle grey (`text-muted`), failed / rejected red (`status-error`), terminated grey. So the list's idle moves from amber to grey, and the header's terminated moves from red to grey.
> - **D9 Paging:** the live list asks for `include_archived=false`, 500 rows per page, at most 20 pages.
>   - Nexen v0.16.1 / v0.17 list ids ascending, oldest first, with no tail or reverse option. A host with more than 10,000 non-archived executions therefore loses its newest rows past the cap.
>   - **Over the cap (PR #1592 review, A1, coordinator-approved 2026-10-06):** keep the cap and commit the rows fetched. The worker list shows a persistent notice,「未歸檔的執行紀錄超過 10,000 筆，最新的可能沒有列出」, and a console warning is logged.
>   - A page that is not a list fails the refresh, and the previous rows stay.
>   - A repeated cursor ends the walk. The rows fetched so far are de-duplicated by id and committed, and a console warning is logged.
>   - The over-cap notice is **not** shown for a repeated cursor. It appears only when the walk hits the 20-page cap.
>   - Newest-first paging is a Nexen follow-up (wake/nexen#119).
>   - In this model an exit archives its row, so non-archived rows are roughly the live workers, and the cap is practically out of reach.
> - **D10 Exited tab (P2):** entities in no live state, each shown by its latest stint. An entity with any live stint is a worker under §4.2: it is listed live, never here. When the SPA knows that the session id is running in a terminal on that host (agent records), the row is marked "在終端機中" and offers no rebuild; the daemon's owner check refuses one anyway.

## 10. Rendering — one timeline, earlier segments enriched

> **統籌（2026-10-06）完整版**：本節由統籌 `mlab/purdex-9b` 撰寫，取代原 outline；§10 的方案由使用者於 2026-10-06 拍板（早段＝對話檔＋各自 execution 的事件補充；當前 stint＝事件紀錄；不做 transcript-to-now）。

### 10.1 Facts

- **Each stint is a separate execution** (§4). So a pane's prelude, the transcript `[0, boundary of this stint)`, already holds the **whole** conversation before this stint: terminal segments and earlier worker stints. There is no gap to fill. A gap would only exist if one execution were reused across a terminal interlude, which this design never does.
- **What differs is the look.** Earlier stints render from the transcript: no cost, tool status derived from transcript, image placeholders, no subagent internals. The current stint renders from its event log, which has everything.
- **Join keys, verified on both sides:**
  - `tool_use_id`: Nexen N2 events, and transcript-derived N2 items in the prelude;
  - `message.id`: assistant lines in both.
- **`uuid` is not a join key.** Its equality between stream-json frames and transcript lines is unverified, and the fixtures share none.
- **Human prompt lines have no id** in the event log; the pane synthesises those bubbles.
- **Data only the event log has:**
  - per-turn cost (`result` frames);
  - live tool status;
  - subagent internals (task events by `tool_use_id`);
  - image thumbnails (by execution + sha256).

### 10.2 Layout

Unchanged: the **prelude section** (transcript up to this stint's boundary) above **this stint** (event log, settled and live). There is no transcript-to-now endpoint, no per-turn transcript boundary, and no change to the live / partial / turn machinery.

### 10.3 Which execution a transcript segment belongs to

- Purdex lists the entity's stints: `session_id` filter, or the `purdex.session_id` label before v0.17 (§8.3).
- For each stint `e` it knows the boundary `b(e)` from `GET /prelude` `total_bytes`, or from the delegated / running payload. Stints are ordered by `b(e)`, ties broken by `created_at`.
- **Worker lines** (`entrypoint` starting `sdk-`) at offset `o` belong to the stint with the **largest `b(e) ≤ o`** among the earlier stints (not the current one).
- **Terminal lines** (`cli`) belong to no stint.
- No match (an execution Purdex cannot list) means a plain transcript segment.

> **統籌核准的推導（2026-10-06）D20 — where the offset comes from.**
> - Each prelude item carries an integer `offset`: the start byte of its source transcript line. A segment marker uses the start of the line it belongs to. Nexen adds it in v0.17 (P3a), in the contract and in `capabilities.transcript_prelude.item_offset`.
> - `pos` stays opaque. Purdex never parses it.
> - The "line at offset `o`" above is that field.
> - Each line's entrypoint is the one of the nearest segment marker at or before it.
> - Lines above the oldest loaded marker have an unknown entrypoint until the older page arrives. They render as plain segments until then.

### 10.4 Enrichment of earlier worker segments

Lazy: when a segment's first item renders. Cached per execution for the pane's lifetime.

- Fetch that execution's events, paged (`/events`, archived executions included), and keep only:
  - N2 `tool_result` / `tool_use`: status (`ok` / `error` / `denied` / aborted) and `duration_ms` by `tool_use_id` override the transcript-derived values;
  - task events: subagent progress and inner frames, nested under the Task call by `tool_use_id`, as the live view does;
  - `result` frames: per-turn cost and tokens. A result maps to the turn whose assistant lines carry the `message.id`s of that turn's assistant frames. The segment then shows a compact turn footer under the prompt span it belongs to.
- **Attachments: thumbnails only when unambiguous.**
  - The segment's k-th human prompt line maps to the stint's k-th `delegated` / `message_accepted` with attachments, and only when the counts match exactly.
  - Otherwise placeholders stay.
  - The thumbnails are fetched from that execution.
- **Terminal segments get no enrichment.** No cost, image placeholders, no subagent internals. This is a documented, permanent limit: Nexen never ran them.

### 10.5 When the current stint ends

When a stint is exited and a new one starts in the same pane (a rebuild), the old stint becomes an earlier segment. It is now drawn from transcript + enrichment, and fold / search state for it resets. This happens once per stint switch, never per turn. Accepted.

### 10.6 Failure modes

| Failure | Result |
|---|---|
| Transcript gone (cleanup) | The prelude reports `gone`, with the existing banner; the current stint is unaffected |
| Stint list unavailable | No attribution; plain transcript segments |
| An enrichment fetch fails | That segment renders without enrichment, with no error toast; the retry is the next pane mount |
| Events larger than a budget | Stop enriching that segment at the budget (default 5,000 events per stint) and say so in a muted line |

### 10.7 Not in this design

Opening an exited or dormant entity just to read it: the exited pane shows only the rebuild screen (E4). P4 does not add reading (§13.8). If it is added later, the Purdex daemon reads the transcript itself with the embedded nexen `prelude` package and the hook's `transcript_path`. No Nexen endpoint is needed for that.

### 10.8 Tests

- **Attribution:** a fixture with terminal → worker A → terminal → worker B → current stint C, plus boundaries; each line goes to the right stint, or to none.
- **Joins:** `tool_use_id` status / duration override; `message.id` → cost footer; subagent nesting.
- **Attachment count guard:** a mismatch leaves placeholders.
- **Lazy fetch and cache:** at most one fetch per stint per pane.
- **Stint switch:** the old stint moves into the prelude section.
- **Failure modes** as in §10.6.
- **Mutation:** dropping the `b(e) ≤ o` ordering, or the count guard, turns a test red.

## 11. Phases (one phase = one PR ≤ 800 lines / 20 files; split further when larger)

| Phase | Repo | Content |
|---|---|---|
| P1a | Purdex | Owner lock (§4.3) including manual-resume exit (Q1); take-to-terminal / take-back exit the worker; extract `transcript_path` into provenance |
| P1b | Purdex | Exit action (§5); live-list filtering, per-entity dedupe, cursor paging, one dot mapping (§9) |
| P1c | Purdex | Exited and start-failed screens; rebuild mode choice for worker and terminal panes (§6, §7) |
| P2 | Purdex | New Tab Sessions / Workers switch; Settings → Worker tabs (Workers, Exited) |
| P3a | Nexen v0.17 | `start_idle` delegate (§8.2); list by `session_id` with indexes; contract docs. **No transcript-to-now endpoint.** *(統籌 2026-10-06)* |
| P3b-1 | Purdex | Pin v0.17; handoff / worker rebuild with `start_idle`; switch owner scans and stint lists to the `session_id` filter; confirm-text change *(統籌 2026-10-06)* |
| P3b-2 | Purdex | §10: stint attribution and lazy enrichment of earlier worker segments *(統籌 2026-10-06)* |
| P4 | Purdex | Ended and gone conversations (§13): a daemon conversation index and scan; 已退出 lists every ended conversation (terminal or worker); a 已消失 tab; search |

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

## 13. Ended and gone conversations (P4)

*(Written by the coordinator `mlab/purdex-18` on 2026-10-06 and rewritten 2026-10-07 after the user's decisions U1–U3 below. It replaces the P4 outline of §11. The plan for it goes through codex together with this section.)*

### 13.0 User decisions (2026-10-07, do not reopen)

The user described a terminal conversation's life in three states and asked for a worker to map onto the same states. This is E2 seen from the user's side.

| # | Decision |
|---|---|
| U1 | **Three states, the same for terminal and worker.**<br>(1) Running: cld-yolo is in, or a worker is live.<br>(2) Ended, can be resumed: `/exit`, or the worker's 退出; the transcript is still there.<br>(3) Gone: the transcript is gone, so it can never be started again. |
| U2 | **Settings → Worker has one tab per state.**<br>Workers (state 1) stays as it is.<br>**已退出** lists every state-2 conversation, **whether it last ran in a terminal or in a worker**, and offers rebuild.<br>**已消失** lists state 3. |
| U3 | **State-3 rows are listed but disabled.** Each one ends with「對話檔已清除，無法再啟動」. |

The earlier draft split ownerless conversations by whether they had ever been a worker (Exited vs Dormant). The user found that distinction meaningless, so there is no Dormant tab.

### 13.1 Facts (measured 2026-10-06 on mlab, alpha.505, Claude Code 2.1.291)

- **Where transcripts are.** `~/.claude/projects` on mlab is a **symlink** to `/Volumes/PD1KAVault/claude/projects`, a sparsebundle volume.
  - It holds 79 project dirs and 383 top-level transcripts, `<slug>/<session>.jsonl`.
  - Another 2,241 nested `.jsonl` files sit under `<session>/subagents/…`. They are not conversations of their own.
  - Total 2.6 GB: median 1.9 MB, p95 34 MB, max 122 MB.
- **Retention.** `cleanupPeriodDays` is unset, so Claude Code's default of 30 days applies. 382 of the 383 files were modified within 30 days. Every transcript therefore lives for at least 30 days after its last write.
- **Title.**
  - `{"type":"ai-title","aiTitle":…}` lines repeat through the file; the latest one wins.
  - A `/rename` writes `custom-title` (8 files).
  - 300 of 383 files have an `ai-title`, and in all 300 the last one lies within the final 256 KB.
- **cwd** appears within the first 64 KB in 380 of 383 files. The other 3 carry none.
- **The first human prompt is often far from the start** (measured 2026-10-07; a human prompt here means a user line that is not meta, not a tool result, and whose text does not start with `<`, which excludes command wrappers).
  - 367 files have one.
  - Its line starts beyond 64 KB in 84 files, beyond 1 MB in 46, beyond 4 MB in 20, and beyond 16 MB in none. The maximum is 13.5 MB.
  - Reading every file forward until that line, or to the end when there is none, reads 373 MB in 1.9 s warm (Python probe).
- **Who started it.** The first line that carries an `entrypoint` says who started the session: `cli` in 285 files, `sdk-*` in 98.
  - First → last entrypoint: cli → cli 281, sdk → sdk 95, sdk → cli 3, cli → sdk 2.
- **Most sdk-born sessions are not Purdex workers.**
  - Nexen on mlab has about 10 executions in all.
  - The sdk-born cwds are scratchpads and `/tmp` (33), `nex-acceptance-scratch` (18), `/` (9), Nexen dev checkouts and Aigora work dirs. These are headless runs by other tools and tests.
- **cwd that no longer exists:** 47 files (42 sdk-born, 5 cli-born).
- **Cost.** Reading the first 64 KB and the last 256 KB of all 383 files takes 0.16 s warm and 0.35 s cold (Python probe).
- **Owners outside tmux are invisible.** The CC hooks are global (`~/.claude/settings.json`), but `pdx hook` identifies the pane through tmux (`cmd/pdx/hook.go`). A Claude Code started outside tmux has no Purdex frame, so Purdex does not know it owns its session.
- **Resume needs the original cwd.** `claude --resume S` finds S only under the project dir of the cwd it runs in.
- **The daemon embeds Nexen.** The nex module lists executions in process through `engine.List(store.ListOptions)` (`internal/module/nex/engine_iface.go`), so no HTTP page cap applies.

### 13.2 Which conversations count

A conversation S on a host is in scope when **either** of these holds:
- it was **born interactively**: the first `entrypoint` of its transcript is not `sdk-*` (`cli`, or another interactive entrypoint such as an IDE extension);
- it has **a Nexen stint on this host**: any execution for S, archived or not.

sdk-born sessions without a stint were started by a tool outside Purdex (Aigora, scripts, tests). They are out of scope (§12); Aigora gets its own tab later (Q3).

The state of an in-scope S:

| State | Rule |
|---|---|
| 1 Running | It has an owner under §4.2: a live terminal frame (D1) or a live worker. |
| 2 Ended | No owner, and the transcript exists. |
| 3 Gone | No owner, and the transcript does not exist. |

**Where state 3 can be known from:**
- **A worker-born or stinted S:** the Nexen execution records `transcript_path` and cwd. Purdex stats that path.
- **A terminal-born S without a stint:** Purdex has no record unless it has seen the transcript. P4 therefore keeps a **conversation index** (§13.6).
  - A terminal conversation whose transcript was cleaned up **before P4 shipped** was never seen, and cannot be listed. This is a documented limit.

### 13.3 The tabs

The order stays 外觀 / Workers / 已退出, and P4 adds **已消失** after them through the tab registry (§9).

**Workers (state 1).** Unchanged: live workers. Terminal-running conversations are not listed (E3).

**已退出 (state 2).** Every state-2 conversation on the host, terminal or worker. It replaces the P2 Exited tab's source and rules:
- **A conversation running in a terminal is state 1, not listed.** This supersedes D10's「在終端機中」marker. The daemon knows terminal owners reliably (§13.6), so this is an exclusion, not a guess.
- **Row content:**
  - **Title.** The first of these that exists:
    1. the latest `custom-title`;
    2. the latest `ai-title`;
    3. Nexen's `session_title`;
    4. the first human prompt, cut to one line;
    5. the first 8 characters of S.
  - **cwd**, with the home directory shortened to `~`.
  - **Last activity**: the transcript's mtime, shown relative ("3 小時前").
  - **上次在**: 終端機 or Worker. The last `entrypoint` decides: `cli` means terminal, `sdk-*` means worker. Fallbacks (R-4-6, coordinator 2026-10-07): no `entrypoint` in the tail window → the first one; none at all → Worker when the host has a stint for S, else 終端機; any value other than `cli` / `sdk-*` → 終端機.
- **Order:** last activity, newest first.
- **Rebuild:** each row offers 重建… (§13.4).

**已消失 (state 3).**
- **Row content:** the same as 已退出, but the whole row is **disabled** and ends with「對話檔已清除，無法再啟動」.
- **Last activity** is the last mtime Purdex saw. For a worker-born S without an index row it is the execution's `updated_at`.
- **Order:** newest first.

**Both tabs:**
- **Search.** A search box filters case-insensitively by substring over the title, cwd, first human prompt and session id. This is **metadata only**; full-text search over 2.6 GB of transcripts is out of scope.
- **Cap.** At most 2,000 rows per tab, newest first. Over the cap, the tab shows「超過 2,000 筆，較舊的沒有列出」.
- **Gating.** The tabs follow the per-host gating of Settings → Worker.

### 13.4 Rebuild from 已退出

重建… opens a tab whose pane is the rebuild screen of §7 for S.

**Preselection (Q4, original mode).** The row's 上次在 decides: 終端機 preselects terminal, Worker preselects worker. The worker option is offered only when the host's Nexen is ready (§7).

**Terminal-last rows:**
- **Terminal rebuild** uses today's engine and resume templates, with the transcript's cwd.
- **Worker rebuild** calls `POST /api/nex/worker-rebuild` with that cwd and without `replace_execution_id`.

**Worker-last rows:**
- 重建… opens that execution's exited screen, as P2-2 does.
- From there, the two choices are §7's.

**When rebuild is not offered:**
- **The cwd no longer exists.** The row shows「工作目錄已不存在」and offers no rebuild. A resume from any other cwd would not find S (§13.1).
- **No cwd is known.** The row offers no rebuild either.

**Recently written.** If the transcript was written within the last 120 s, the rebuild screen warns before it acts:「這個對話 N 秒前還有寫入，可能正在 Purdex 以外的地方使用」. A Claude Code outside tmux is not a known owner (§13.1). The daemon's owner checks still run at rebuild, as for every rebuild.

### 13.5 States are recomputed on every scan

- **Nothing is stored as "gone".** A transcript that reappears, for example when the vault volume is mounted again, puts S back in state 2.
- **Gone is decided only after a successful listing.** S is gone only when the projects root was listed successfully, so a missing file really means a missing file.
- **A failed listing marks nothing gone.** When the root is missing or unreadable (the volume is not mounted, for example):
  - the tabs show「無法讀取對話檔目錄」with the error;
  - **no conversation is marked gone**;
  - the lists are never empty or "all gone" because of it.

### 13.6 The daemon side

**Endpoint.** The nex module serves it, next to the owner checks it reuses: for example `GET /api/nex/conversations?state=ended|gone`. The plan fixes the exact shape.
- **Each row carries:**
  - session id and title fields;
  - cwd, and whether that cwd exists;
  - last activity and 上次在;
  - first prompt, for search;
  - `transcript_path`;
  - the latest stint's id and `effective_profile`, when there is a stint.
- **The response also carries** `scanned_at` and a root error, when there is one.

**Root.** `$HOME/.claude/projects`, the same rule as Nexen's `transcriptPath` (`execution/addressing.go`). `CLAUDE_CONFIG_DIR` is not honoured, as in Nexen.
- **A symlinked projects dir is followed** (mlab's case).
- **A transcript that is itself a symlink, or not a regular file, is skipped.** This matches Nexen `prelude.openTranscript`'s `O_NOFOLLOW` / `O_NONBLOCK` rule.

**Per file.** The scan reads two parts of each file.
- **The head:** forward, line by line, to collect cwd, the first `entrypoint` and the first human prompt.
  - It **stops at the first human prompt**, and never reads past 16 MB.
  - With no human prompt within 16 MB, the row has none. This is the known limit, and none was measured.
  - *(Amended 2026-10-07: a 64 KB head missed the first prompt in 84 of 367 files, which would have hurt both the title fallback and search.)*
- **The tail:** the last 256 KB, for the titles and the last `entrypoint`.
- A line cut by the tail window's edge, or by the 16 MB cap, is skipped.
- A title older than the tail window is missed, and the title falls back as in §13.3. This never happened in the measurement.

**Conversation index.** A table in the Purdex store holds one row per top-level transcript ever seen. Each row has:
- session id;
- `transcript_path`;
- cwd;
- first and last `entrypoint`;
- the titles;
- first prompt;
- size and mtime;
- first seen and last seen.

How the index is kept:
- A scan upserts every file it reads.
- A file whose (size, mtime) did not change is not re-read.
- A transcript only ever grows, so the head facts (cwd, first `entrypoint`, first prompt) are kept once found.
  - A file that grew re-reads only its tail.
  - A head scan that has not found the prompt yet resumes from the stored offset instead of starting over.
  - The head is read again from byte 0 only when the file shrank or its inode changed, because that means it was rewritten.
  - So the cost of the head is paid about once per file.
- Rows are never deleted in P4. They are tiny: retention adds about 400 a month on mlab.

**When scans run:**
- at daemon start;
- every 6 hours;
- on request, reusing a result up to 5 s old.

Concurrent requests share one in-flight scan. Transcripts live at least 30 days (§13.1), so the 6-hour scan sees every terminal conversation before it can be cleaned up.

**Joins happen in the daemon:**
- terminal owners from live frames (D1);
- live workers and every stint from an in-process `List` with archived rows included, paged to the end without a page cap.

Consequences:
- **The 已退出 tab no longer pages Nexen from the SPA.** P2's 10,000-row notice (D9) does not apply to it.
- **wake/nexen#119 (newest-first listing) is not needed for P4.** It stays a follow-up for the live lists.

### 13.7 Known limits

- **Owners outside tmux.** A Claude Code running outside Purdex's tmux has no frame, so its session can be listed in 已退出 while in use. The 120 s warning of §13.4 mitigates this but does not prevent it.
- **Gone before P4.** A terminal conversation cleaned up before P4 shipped cannot appear in 已消失 (§13.2).
- **sdk-born conversations Purdex never ran** do not appear in any tab.
- **Old titles.** A title written more than 256 KB before the end of the file is not seen.
- **Late first prompts.** A first human prompt more than 16 MB into the file is not found. The title falls back to the session id and search loses that field. None was measured.

### 13.8 Not in P4

- **Reading** an ended or gone conversation without rebuilding it. E4 keeps those panes rebuild-only, and §10.7 records how reading would be built.
- **Full-text search.**
- **Other providers**, the **Aigora tab** and **deeplinks**.
- **Pruning** the conversation index.

### 13.9 Tests

**Scan and states (daemon).** A fixture projects dir with:
- a terminal-born ended conversation → 已退出, 上次在 終端機;
- a terminal-born conversation in a live terminal → not listed;
- a worker-born one with an archived stint and the transcript present → 已退出, 上次在 Worker;
- the same with the transcript removed → 已消失, with no index row needed;
- a terminal-born one, indexed, then its file removed → 已消失;
- after that, the file restored → back in 已退出;
- an sdk-born one with no stint → never listed;
- a cwd that is gone → listed, with no rebuild;
- a title only before the tail window → fallback;
- a first prompt beyond 64 KB → found;
- no prompt within the 16 MB cap → no prompt, and the scan stops at the cap;
- a grown file → only the tail is re-read;
- a head scan without a prompt yet → it resumes from the stored offset;
- a rewritten file (it shrank, or its inode changed) → the head is read again from byte 0;
- a line cut at each window edge → skipped;
- a symlinked projects dir → followed;
- a symlinked transcript → skipped;
- a missing root → root error, with nothing marked gone and no empty list.

**Index and scheduling:**
- an unchanged file is not re-read;
- concurrent requests share one scan;
- the scan runs at start and on the 6-hour timer;
- the cap notice.

**SPA:**
- the 已退出 tab on the daemon endpoint, with terminal and worker rows and 上次在;
- search over the four fields;
- preselection from 上次在;
- the「工作目錄已不存在」state;
- the 120 s warning;
- the 已消失 tab: disabled rows with the note, in the registry.

### 13.10 Phases

| PR | Content |
|---|---|
| P4-1 | Daemon: the conversation index (migration), the scan with its schedule and cache, the state rules and the endpoint |
| P4-2 | SPA: 已退出 moves to the endpoint (terminal + worker rows, 上次在, search, terminal-row rebuild entry, cwd and 120 s states); the 已消失 tab |

Each PR is ≤ 800 lines / 20 files (§11); P4-2 may split in two. P4-1 is a daemon deploy, done by the coordinator.
