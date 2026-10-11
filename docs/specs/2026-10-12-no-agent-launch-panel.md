# No-agent launch panel — resume the last conversation or start a new one from the deck / chat (spec + plan, rev 3)

rev 3 (codex review of rev 2): the frame keeps its own generation and a stable root flag; the last-run write shares the
SessionEnd claim-delete transaction; a dedicated tmux method reports generation vs busy from one invocation; the launch
claim carries a nonce; a hostconfig reader service; device scope listed; Mac renders the panel above the deck / chat
split; NA-0 split in two.

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
- **The frame carries what it needs** (rev 3, codex C1/C2): `agent_frames` gains `tmux_instance` (the generation read
  when the root SessionStart creates / takes over the frame) and `is_root` (set at creation, never derived later — the
  nullable parent FK is cleared by `ON DELETE SET NULL` when a parent ends first, `store/frames.go`). Migration in the
  store's existing style; old rows get `''` / their current parent-derived value at migration time.
- When a root frame **ends**, `last_agent_run` for its tmux session is written: `{code, tmux_instance, agent_type,
  session_id, cwd, ended_at}` (one row per code, overwritten) — **in the same write transaction as the SessionEnd
  claim-delete** (a `FramesStore` method: verify the session identity as `claimFrameEnd` does today, check
  `is_root`, upsert the last run, delete the frame; the delete / CAS winning is the only licence to write). The
  `tmux_instance` written is **the frame's own**, never the generation sampled when the SessionEnd arrives; an empty one
  writes nothing (not resumable). Never written for a child frame. Store: a table in `agent_events.db`; the DB is
  disposable (empty / missing table → no record, start normally).

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
- `resume.line`: the host's resume template for `last.agent_type` — read through a new hostconfig registry service
  `ResumeTemplateReader` (registered like the relay / team readers in `hostconfig/module.go`, returning the map after
  the same lenient `readResumeTemplates` the GET uses; the defaults move to / are mirrored in Go and tested against the
  SPA's `DEFAULT_RESUME_TEMPLATES`); the session module never opens `host_config.db` itself:
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
- **claims** the launch: one in-memory claim per `(code, tmux_instance)` holding a never-reused **nonce**, taken under a
  per-session lock; a second launch while claimed → 409 `already_launching`. Every release is `release(key, nonce)` —
  the 15 s timer, a send that did not go out, and a root SessionStart (the callback re-resolves the event's tmux session
  to its current code and instance and releases only a matching claim) — so a late cleanup of launch A can never release
  launch B's claim (rev 3, codex I5);
- sends `line + "\n"` through a **new executor method** (e.g. `SendKeysIfInstanceAndShell`, alongside — not changing —
  `SendKeysIfInstance`): one `if-shell -F` whose condition is the generation match **and** `#{pane_current_command}`
  equal to one of the shells (nested `#{||:#{==:…},…}`, no `m/r` regex), and whose else branch prints a sentinel with the
  generation comparison result and the sanitized `pane_current_command`, like `send_keys_mode.go` reports its facts; Go
  maps that one output to `ErrGenerationMismatch` (checked first) or `ErrPaneBusy`. Nothing starting between the plan and
  the send is typed into; no second tmux call decides the reason. Tested on the deployed tmux versions (mlab 3.6a, air26).
- Answers: 200 `{line}` (what was typed); 409 `{code}` with `generation_conflict` | `busy` | `already_launching` |
  `agent_running`; 400 `bad_request` (unknown kind, `resume` without a line); 404 gone. Each 409 is a JSON body with a
  stable `code` (clients map codes to their own text; they never show an HTTP status text).
- Device tokens (paired phones) may call both routes: add `GET /api/sessions/{code}/launch-plan` and
  `POST /api/sessions/{code}/launch` to the default-deny allow-list in `cmd/pdx/device_scope.go` (+ its test and an
  end-to-end device-bearer test). Capability `session.launch.v1` declared in `internal/core/info_handler.go`.

### 2.4 Tests (NA-0)
Last run written at a root SessionEnd and not by a child; restored plan after a daemon restart (same instance) and
ignored after a tmux restart (instance differs); the id rule (empty, spaces, quote, `;`, newline, 129 chars, UUID,
`ses_…`); empty exact / empty fallback / both empty; dir here / move / none; busy for `npm`, not for `-zsh`; agent_running
for a cc frame and for a codex hook state; launch quoting (a path with `'` and spaces); the claim — two concurrent POSTs,
one 200 and one 409 `already_launching`, claim released on SessionStart and after 15 s, released when the send failed;
the server-side condition — a pane that turns busy between plan and send gets 409 `busy` and nothing typed (tmux fake +
one real-tmux test with `-L`). go test -race on the touched packages; mutation: drop the instance match on `last` ⇒ red;
drop the claim ⇒ the concurrency test red.
rev 3 additions: a SessionEnd of the old tmux generation arriving **after** a tmux restart writes the frame's (old)
generation, so the new generation's plan has no `last`; parent ends first, child's SessionEnd later → no last-run
write; the claim nonce — launch A released by SessionStart, B claims, A's late timer / failed-send cleanup must not
release B; GET / POST with a missing or malformed `expected_tmux_instance` → 400; generation and busy both failing →
`generation_conflict` (precedence); a malformed persisted resume template row read leniently; device-bearer GET and POST
end to end; the shell condition on the deployed tmux versions.

## 3. Clients (NA-1 Mac, NA-2 iOS) — render the plan, post the choice
- **When**: a live tmux session (not a worker), host advertises `session.launch.v1`, the deck or chat would otherwise show
  "no conversation", and `launch-plan.agent_running` is false. **Mac**: `SessionPaneContent` decides and, when the view
  is deck or chat and the panel applies, renders the one `LaunchPanel` itself **before** the split into `DeckPane` /
  `ChatPane` (today the deck turns `off` into `DeckUnreadable no_session` and the chat renders an empty `ChatView` — neither
  branch is reached in this state); `useConversationViewGate` stays conversation-only (rev 3, codex I8). **iOS**:
  `DeckScreen` and the chat view render the same panel component. A host without the capability keeps today's message (and iOS shows the mode
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
- **NA-0a daemon — persistence and read model**: frame `tmux_instance` + `is_root` (migration), the atomic
  claim-delete + last-run upsert in `FramesStore`, the `ResumeTemplateReader` registry service, an agent-running provider
  for the session module; their tests (§2.4 parts).
- **NA-0b daemon — plan and launch**: `GET …/launch-plan`, `POST …/launch`, the tmux `SendKeysIfInstanceAndShell`
  (interface, real, fake), the nonce claim, capability, device scope; their tests. Needs NA-0a. Deploy on mlab and air26
  before the clients merge.
- **NA-1 Mac** (§3), merge after NA-0 is deployed.
- **NA-2 iOS** (§3), merge after NA-0 is deployed; the "mode switch visible in the no-conversation state" fix may ship
  ahead on its own.
