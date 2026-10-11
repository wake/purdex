# No-agent launch panel — resume the last conversation or start a new one from the deck / chat (spec + plan, rev 2)

rev 2 (codex review of rev 1): the daemon owns every rule (last run, project directory, the typed line, the busy check, the
claim); the clients only render its plan and post a choice. rev 1's "no daemon change" did not hold: provenance only knows
a **running** agent (a normal SessionEnd deletes the frame), and `current_command` + send-keys cannot stop a second launch.

## 1. User decisions (2026-10-12, do not reopen)
- A session with **no agent running**, opened in the deck or the chat (Mac and iOS alike), shows a launch panel instead of
  「這個 session 沒有 Claude Code 對話」: the rebuild screen's idea applied to a live session.
- Rows: 「在 tmux 裡」 (disabled — already in tmux), 「切到專案目錄」 (disabled when already there), then two quick buttons
  「接續上次對話」／「開新對話」.
- 「開新對話」 starts **the agent this session last ran** (no picker); with no record, Claude Code.
- Mac and iOS change together. 「切到終端機」 is always reachable from the panel (iOS today hides the mode switch in this
  state — fixed here).

## 2. Daemon (phase NA-0)

### 2.1 Last run per session
- When a root agent frame **ends** (`frame_ops.go`, the SessionEnd claim — before the row is deleted) record
  `last_agent_run` for the tmux session: `{code, tmux_instance, agent_type, session_id, cwd, ended_at}` (one row per code,
  overwritten). Store: a small table in `agent_events.db` with the module's existing migration style; the DB is disposable
  (empty / missing table → no record, start normally).
- Only an ended **root** frame writes it (a subagent / child frame never does).

### 2.2 `GET /api/sessions/{code}/launch-plan`
Answers what the panel shows; the daemon computes it from live data only:
```json
{ "agent_running": false,
  "tmux_instance": "123:1700000000",
  "last": { "agent_type": "cc", "session_id": "…", "cwd": "/…", "ended_at": 1791… } | null,
  "dir": { "path": "/…", "state": "move" | "here" } | null,
  "resume": { "line": "claude --resume …" } | { "line": null, "reason": "no_record" | "no_template" | "empty_command" },
  "fresh":  { "line": "claude" },
  "busy": null | { "command": "npm" } }
```
- `agent_running`: a live root frame exists for the session, or the hook state says an agent is running (any type —
  cc, codex, opencode). True → the clients do **not** show the panel.
- `last`: the live frame if one exists, else `last_agent_run` — **only when its `tmux_instance` equals the session's
  current one and both are non-empty** (a reused code never lends a record).
- `dir`: `last.cwd`; `state = "here"` when it equals the pane's current path (tmux `pane_current_path`, one trailing `/`
  trimmed), else `"move"`. No `last` → `null` (the row is hidden; never an older generation's cwd).
- `resume.line`: the host's resume template for `last.agent_type` (`hostconfig` `resume_templates` over the defaults):
  `exact` with `{id}` = `session_id` **only when the id matches `^[A-Za-z0-9_-]{1,128}$`** (the rebuild composer's rule,
  `spa/src/lib/rebuild/composer.ts`), else `fallback`. The resolved line trimmed empty → `{line:null, reason:"empty_command"}`.
  No `last` → `no_record`; no template for the type → `no_template`.
- `fresh.line`: the start table — cc `claude`, codex `codex`, opencode `opencode`; unknown type or no `last` → `claude`.
- `busy`: the pane's foreground command when it is **not** a shell (`zsh bash fish sh dash ksh tcsh nu pwsh`, a leading
  `-` stripped, case-insensitive); else `null`.
- 404 when the session does not exist; capability `session.launch.v1` advertised.

### 2.3 `POST /api/sessions/{code}/launch`
Body `{ "kind": "resume" | "fresh", "cd": bool, "expected_tmux_instance": "…" }`. The daemon **re-derives** the plan (never
takes a line from the client) and:
- composes `cd <dir> && <line>` when `cd` and `dir.state == "move"`, else `<line>`; `<dir>` shell-quoted (single quotes,
  `'` → `'\''`; `~` never used — the path is absolute);
- **claims** the launch: one in-memory claim per `(code, tmux_instance)`, taken under a per-session lock, released when a
  SessionStart for that session arrives or after 15 s; a second launch while claimed → 409 `already_launching`;
- sends `line + "\n"` with **one tmux invocation that checks, on the server, both the generation and that the pane's
  foreground command is a shell** (extend the `if-shell -F` condition of `SendKeysIfInstance` with
  `#{pane_current_command}` matched against the shell list), so nothing starting between the plan and the send is typed
  into; the claim is released if the send did not go out.
- Answers: 200 `{line}` (what was typed); 409 `{code}` with `generation_conflict` | `busy` | `already_launching` |
  `agent_running`; 400 `bad_request` (unknown kind, `resume` without a line); 404 gone. Each 409 is a JSON body with a
  stable `code` (clients map codes to their own text; they never show an HTTP status text).
- Device tokens (paired phones) may call both routes (same rule as send-keys).

### 2.4 Tests (NA-0)
Last run written at a root SessionEnd and not by a child; restored plan after a daemon restart (same instance) and
ignored after a tmux restart (instance differs); the id rule (empty, spaces, quote, `;`, newline, 129 chars, UUID,
`ses_…`); empty exact / empty fallback / both empty; dir here / move / none; busy for `npm`, not for `-zsh`; agent_running
for a cc frame and for a codex hook state; launch quoting (a path with `'` and spaces); the claim — two concurrent POSTs,
one 200 and one 409 `already_launching`, claim released on SessionStart and after 15 s, released when the send failed;
the server-side condition — a pane that turns busy between plan and send gets 409 `busy` and nothing typed (tmux fake +
one real-tmux test with `-L`). go test -race on the touched packages; mutation: drop the instance match on `last` ⇒ red;
drop the claim ⇒ the concurrency test red.

## 3. Clients (NA-1 Mac, NA-2 iOS) — render the plan, post the choice
- **When**: a live tmux session (not a worker), host advertises `session.launch.v1`, the deck or chat would otherwise show
  "no conversation" — Mac: decided in `SessionPaneContent` (one input for both the deck and the chat; today the deck turns
  `off` into `DeckUnreadable no_session` and the chat renders an empty `ChatView`), iOS: `DeckScreen` and the chat view —
  and `launch-plan.agent_running` is false. A host without the capability keeps today's message (and iOS shows the mode
  switch in it regardless). Other unreadable reasons (`not_found`, `unreachable`, …) keep today's behaviour.
- **Panel**:
  ```
  這個 session 目前沒有在跑 agent
    ☑ 在 tmux 裡                                    (always disabled)
    ☐ 切到專案目錄  /Users/wake/Workspace/tangency/at-inwin-plugin  (main)
    [ 接續上次對話 ]  [ 開新對話 ]
    切到終端機 →
  ```
  - directory row hidden when `dir` is null; checkbox default on for `move`, disabled as 「已在這個目錄」 for `here`;
    the branch (#2707, sessions list) may follow the path.
  - 「接續上次對話」 disabled when `resume.line` is null, its hint by reason: `no_record` 「沒有上一次的對話紀錄」,
    `no_template` 「這種 agent 沒有 resume 指令」, `empty_command` 「resume 指令是空的，請到主機設定填寫」.
  - each button's tooltip (Mac) / accessibility hint and long-press preview (iOS) shows the line the plan says (with the
    `cd … &&` prefix when the box is on).
  - `busy` → both buttons disabled, 「這個 pane 正在執行 {command}，請先到終端機結束它」, 「切到終端機」 emphasised.
  - refreshed on mount, on 「重新整理」, and when the sessions list changes for this session; no animation; never switches
    to the terminal by itself.
- **Launch**: `POST …/launch` with the session's `tmux_instance`; buttons disabled in flight; errors by code:
  `generation_conflict` 「這個 session 已經換了一個，請重新整理」, `busy` (the busy line above, plan refreshed),
  `already_launching` 「正在啟動中」, `agent_running` (refresh — the panel goes away), 404 「這個 session 已經不在了」,
  anything else 「啟動失敗（{status}）」. Success: no message; the agent's hooks switch the view to the conversation.
- **Tests (each client)**: when / when not (live + no agent, codex running, host without capability, worker, other
  reasons); every row state from fixture plans; the hint per reason; busy; in-flight disable; each error code; the
  Mac deck and chat share one decision; iOS mode switch visible in the no-conversation state. Mutation: ignore
  `agent_running` ⇒ red.

## 4. Phases (one PR each, ≤ 800 lines or ≤ 20 files)
- **NA-0 daemon** (§2). Deploy on mlab and air26 before the clients merge.
- **NA-1 Mac** (§3), merge after NA-0 is deployed.
- **NA-2 iOS** (§3), merge after NA-0 is deployed; the "mode switch visible in the no-conversation state" fix may ship
  ahead on its own.
