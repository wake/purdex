# Interface language U1 — daemon conversation model for Claude Code in tmux — spec

Status: **design decisions approved by the user 2026-10-07/08** (design doc v0.5, §17 all settled); this spec is the coordinator's derivation (`mlab/purdex-73`, ref `mlab/_3fj93m`). It is split into phases of one PR each (≤ 800 lines diff, ≤ 20 files). The plan for the first phase goes to codex together with this spec; later phases get their own plans.

Sources:
- Design doc v0.5: `docs/pages/interface-language.html` (served at `https://pages.mlab.host/wake/purdex/interface-language.html`), cited as **[D §x]**. Research: `docs/pages/interface-unification.html`.
- Lights research (16 inaccurate cases, root causes, file:line): [D §12.1].
- iOS scope check (2026-10-08, `air26/_0g6g1e`): the seven API gaps G1–G7 in [D §14]. Raw CC 2.1.292 samples: purdex-ios repo `docs/samples/2026-10-07-cc-2.1.292/`.
- Mod API: Claude Code 2.1.29x plugin types (`plugin-authoring/types/claude-code.d.ts`, cited as **[d.ts Lnnnn]** for 2.1.292).

## 1. Goal

One daemon-side, normalized **conversation model** for Claude Code sessions that run in tmux (the *terminal* underlay), served to the Mac App and the iOS App, plus **lights v2** (accurate per-session status) and **usage** (context, 5 h / 7 d, cost) as first-class data. The Purdex mod is the primary source; transcript + settings hooks + statusline are the fallback when a session has no mod.

Out of U1 (later phases of the interface line, [D §16]): iOS switches to the model (U2), Mac App deck/dock/status-bar buttons (U3), executions (U4), Codex (U5), chat (U6), the communication line (C1/C2, last), P8b (not brought forward).

## 2. User decisions (2026-10-07/08, do not reopen)

| # | Decision |
|---|---|
| N1 | Nouns: underlay = **terminal / execution**; control mode = **terminal / deck (指揮台) / chat**. *worker* leaves the vocabulary; program keys (`worker`, `mode:'room'`) change only with a wire revision. |
| N2 | **Unified interface, capabilities decide.** Both underlays use the same nouns and components; a component appears when its capability exists, no degraded variants. |
| N3 | **The Purdex mod is Claude Code's primary data source** (terminal underlay now, execution underlay in U4). Transcript / hooks / statusline are the no-mod fallback. |
| N4 | **Lights v2 ships entirely inside U1**, including the parts that need no mod; no separate quick-fix PR. |
| N5 | Light vocabulary: **yellow = waiting for your answer** (questions, plans and approvals are not split); **no "to review" state** (that is the existing unread mark); **no "lost contact" state** (the heartbeat only reconciles). Priority **error > waiting > running > idle**; a tab with several panes shows the highest; the unread mark overlays and does not rank. The existing worker hand icon (alpha.566) stays. |
| N6 | Background work: **background shell commands show nothing**. **workflow / monitor / scheduled wake-up** draw one small symbol at the **top-left of the tab's agent icon**, only one at a time, priority **workflow (TreeStructure) > monitor (Eye) > schedule (Clock)**; static, icon colour, hidden when lights are off; in the dot-only style (no icon) it sits at the dot's top-left (design doc §12.2); the main light is not changed. Blue dots count only subagents the main agent started; workflow agents are represented by the symbol. |
| N7 | Mod rollout: mlab already runs the mod (alpha.586+); **a26 installs it when U1 ships**; P8b is not brought forward; whether Nexen loads the mod is decided at U4. |
| N8 | Phasing: U1 → U2 ∥ U3 → U4 → U5 → U6, communication line last. |
| N9 | iOS adapts to the main design (the design leads, not what iOS already built). |

## 3. Facts (measured)

- **M-U1-1 `$.http.fetch` over a Unix socket works** (Claude Code 2.1.293, `claude -p --plugin-dir`, 2026-10-08): `POST` with headers and body reaches a Unix-socket HTTP server; the request's `Host` is the URL host, `User-Agent: Bun/1.4.3`; `Date.now()`, `$.clock.now()` and `$.session.id()` all work; a fetch from `$.clock.after(0, …)` works; inside `session.end` two sequential fetches completed in 2 ms. Probe: session scratchpad `sockprobe/`.
- **M-U1-2 a mod's static check**: `$` may only be passed to a function declared at the top level of the module (or a `const` bound to one); passing it to an inner arrow makes the module fail to load. Measured with the same probe.
- **M-U1-3 one registration per event without a matcher** (2.1.293): registering the same event twice *without* a matcher makes the whole module fail to load ("on("turn.complete") is registered twice without a matcher"). One registration *with* a matcher and one without, even from two imported files, load and both run, the first registered outermost. Measured: `tool.call{tool:'Bash'}` in the entry file plus `tool.call` in an imported file; `tool.check` (decision `allow`, `rule: "Bash(echo:*)"`) fires **inside** the `tool.call` chain, between its start and its end; `classic.Stop` reaches a mod with `background_tasks: []` and `session_crons: []`; `session.measure` gave `{context{tokens, window, percent}, rateLimits[{kind, percentUsed, resetsAt}], cost{usd}, changed}`; a `tool.call` result has keys `ref, result, text, isReadOnly`.
- **M-U1-4 interactive-mode event order** (Claude Code 2.1.293, interactive `claude` in tmux with the U1-1b reporter, 2026-10-08), as the daemon received it: `session.start → usage → heartbeat → turn.start → heartbeat(turn_id) → tool.start → tool.check(allow) → tool.end → background → turn.complete → usage`. **`background` (from `classic.Stop`) arrives before `turn.complete`.** Heartbeats came 10.00–10.01 s apart and did not break across `/clear`. `/clear` gave `session.end{reason:clear}` (old sid), then about 660 ms later `session.switch` (new sid, same stream). `/exit` gave `session.end{reason:prompt_input_exit}`, sent from inside the hook. `dropped_total` stayed 0.
- **M-U1-5 `$` does not cross an import** (2.1.293): the static check follows `$` "only into a function declared in this same file, never across an import" — a module that passes `$` to a function imported from another file fails to load. So `register.js` cannot hand `$` to observer functions exported by `events.js`; the reporter registers its own hooks instead (§6.5).
- **M-U1-6 the approval signal** (Claude Code 2.1.294, interactive `claude` in tmux, default permission mode, a Bash call that needs approval, a throwaway probe mod on `ui.render` {Spinner, ToolProgress, ToolUse} + `tool.check` / `tool.call`, 2026-10-08): `tool.call` starts, `tool.check` returns `ask`, the dialog opens; the `ToolUse` row of that `tool_use_id` is drawn with `isRunning: false` while the dialog is open and with `isRunning: true` **16 ms** after the person's Enter on "Yes"; `ToolProgress{kind: background_hint}` follows **3.5 s** later (Bash only); `Spinner.mode` stays `tool-use` throughout (no signal). Refusal and Esc were not measured (they end in `tool.end` with an error / `turn.complete`). The mod reports the flip as `tool.approved` (§6.3); it works in any terminal, including a plain `tmux attach`.
- **M-U1-7 the Claude Code transcript format** (census of mlab `~/.claude/projects`, 2026-10-08: 1,460 files of the last 14 days, 491 k rows, CC 2.1.263–2.1.294, plus the three iOS samples; script and counts in the U1-4 plan, nothing but key names, enum values and counts printed):
  - Row `type`s that carry conversation content: `user`, `assistant`, `system`, `attachment`, `queue-operation`; the other 18 types (`last-prompt`, `ai-title`, `mode`, `permission-mode`, `file-history-*`, `cost-state`, …) are metadata. `ai-title` / `custom-title` carry the title.
  - An assistant row holds **one content block** (159,859 of 159,862); the rows of one API message share `message.id` and are ordered by `apiBlockIndex`. Every row has a `uuid`; no uuid repeats within a file.
  - A prompt row (a `user` row that is not a tool result) carries `turnPosition {promptIndex, turnIndex}` from 2.1.284, `turnOrigin` (`human` | `peer` | `task_notification` | `sdk` | `scheduled` | `system` | `auto_continuation`) from 2.1.277, and `origin.kind` (`human` | `peer` | `task-notification` | `coordinator` | `plugin` | `auto-continuation`) and `promptSource` (`typed` | `queued` | `system` | `sdk` | `suggestion_accepted`) on every version seen. `turnIndex` counts every turn, `promptIndex` only human prompts.
  - **Turn end**: a turn that reaches Stop writes `system/stop_hook_summary` then `system/turn_duration {durationMs}` — also a turn ended by a permission refusal. Esc while the model writes produces `[Request interrupted by user]` (a `user` text row with `interruptedMessageId`; the aborted assistant row may carry `isAbortedMidStream`) and **no** `turn_duration`. Esc at a permission dialog *and* Esc during a running tool give a tool result with `toolDenialKind: "user-rejected"`, `is_error: true`, then `[Request interrupted by user for tool use]`, then `turn_duration`.
  - `toolDenialKind` ∈ `user-rejected` | `permission-rule` | `interrupted` | `cancelled` (all seen from 2.1.263 on); without it, other `is_error` results are tool failures (`Exit code N…`, `<tool_use_error>…`, hook blocks).
  - **Queued prompts**: a prompt sent during a turn is written as `queue-operation enqueue`; when the running turn absorbs it, it becomes an `attachment {type: queued_command, prompt, origin, commandMode}` inside that turn (`queue-operation remove`, reason `absorbed_mid_turn`); when it waits for the turn to end it becomes the next turn's prompt row with `promptSource: "queued"`. Peer messages and task notifications take the same two paths with their own `origin`.
  - **API errors**: an assistant row with `model: "<synthetic>"`, `isApiErrorMessage: true`, `error` ∈ `rate_limit` | `server_error` | `authentication_failed` | `invalid_request`, followed by `turn_duration`.
  - Slash commands: a command that reaches the model is a prompt row whose text is `<command-name>…<command-args>…` (its expansion is an `isMeta` row); a local command (`/model`, `/usage`) is a pair of `system/local_command` rows (`<command-name>` then `<local-command-stdout>`). Bash mode (`!cmd`) is a `<bash-input>` row then a `<bash-stdout>…<bash-stderr>` prompt row, and the model answers it.
  - Compaction: `system/compact_boundary {compactMetadata}`; the summary is a `user` row with `isCompactSummary`.
  - Subagents live in their own file `<sid>/subagents/agent-<agentId>.jsonl` (every row `isSidechain: true`); the Agent tool's `toolUseResult.agentId` names it (2,449 of 2,449 resolved). Main files hold no sidechain rows.
  - Every `tool_result` row holds exactly one `tool_result` block (95,310 rows of the last 14 days, none with more; `toolDenialKind` sits on 83 of them), so the row-level `toolDenialKind` / `toolUseResult` belong to that one result; the normalizer reads them only on such rows (2026-10-09 recount).
  - Thinking text is empty in 94.6 % of thinking blocks; `thinkingDurationMs` sits on the thinking row when known.
  - Tool results embed images (3,157 blocks, 700 MB of base64 in 14 days); 227 rows exceed 1 MB (max 2.2 MB). Edit / Write results carry `toolUseResult.structuredPatch` (hunks); Bash results carry `stdout`, `stderr`, `interrupted`, and `backgroundTaskId` when backgrounded.
  - `--resume` keeps writing the same file (no file held rows of another `sessionId`); 41 of 538 main files hold more than one `session_context` attachment — the candidate restart marker, to be confirmed (U1-4 plan M-U1-4-a).
- **M-U1-8 mod events reach the daemon late** (alpha.609 smoke, interactive `claude` in tmux, Haiku, short turns): the time from an event happening (its `at`) to the daemon receiving it is typically about 1.2 s and at most about 3 s, far more than the mod's own 150 ms batch. Observed: the Stop hook was received at 13.93 s while the `turn.start` that happened at 12.75 s was received at 13.95 s, so a receive-order comparison cannot tell which came first; the old edge then read the turn.start as the mod catching up, and the pane went `idle(hook)` → `running(mod)` → `idle(mod)`. The cause of the lag is a separate investigation (issue #2025). Hook against mod ordering therefore uses event time (§6.2, §7).
- Events the mod's `register.js` already registers without a matcher (2026-10-08): `session.start`, `turn.start`, `turn.complete`, `classic.SessionStart`, `prompt.submit`, `session.compact`. With matchers: `tool.check{Write}`, `tool.check{Edit}`, `command.run{relay}`; `ask.js`: `tool.call{AskUserQuestion}`.
- [d.ts L4412–4434] `turn.start {text, turnId}` (observe only; subagent runs raise none); `turn.complete {reason: answer|aborted|refusal|error, turnId, agentId?, durationMs, isAborted, usage?}`.
- [d.ts L3916–3941] `tool.call {tool, tool_use_id, agentId?, …args}` (no timing — time it around `await next(e)`); `tool.check {tool, input, tool_use_id?, agentId?} → {decision: allow|ask|deny, rule?}`.
- [d.ts L4374–4385, L11019] `session.measure {context{tokens?, window, percent?}, rateLimits[{kind, percentUsed, resetsAt?}], cost?{usd}, changed[]}`, fired after each main turn and when a rate-limit window moves a point.
- [d.ts L4386–4397, L10965–11009] `session.end {reason, sessionId, resume}`; one 1.5 s wall-clock bound covers every hook and its `$` waits.
- [d.ts L4050–4063] `agent.spawn {tool_use_id, background, subagentType, workflow?{runId, agentIndex}, …}`; `next` settles with `{agentId}`; a workflow's agents are not in `$.agent.list()`.
- Classic events reachable from a mod include `classic.Stop` with `background_tasks[{id, type: shell|subagent|monitor|workflow…, status}]` and `session_crons[]` [d.ts L793, L12047].
- **No engine event carries a session id** except `session.end`; use `$.session.id()`; it changes after `/clear` (`classic.SessionStart{source:'clear'}`, no `session.start`). **No event carries a sequence number or timestamp**; the mod stamps its own.
- `$.http.fetch` has no timeout option; `$` calls pause the 10 s hook budget; `$.clock` waits do not.
- Today the mod reaches the daemon only by running `pdx` (`$.process.run`); the daemon listens on TCP only (`cmd/pdx/main.go`), so the event channel is new.
- Today's light pipeline: no sequence numbers, last-writer-wins, the tab light reads only the tab's primary pane, the per-tmux-session projection picks the most recently *started* pane, the WS snapshot never sends clears, `is_interrupt` / `background_tasks` / `session_crons` are never read, a subagent `StopFailure` turns the main frame red. Full map with file:line: §9.
- **Claude Code 2.1.294 as a working environment (measured while recording the U1-4d goldens):** it has no `Grep`, `Glob` or `MultiEdit` tool (search and edit go through `Bash` and `Edit`); an `Agent` call is moved to the background automatically, its result arriving later as a notification; and a foreground `sleep` is refused (wait with a monitor or an until-loop instead). Briefs for sub-agents and recording scripts must not assume the missing tools or a blocking `Agent` call.

## 4. Architecture

```
CC TUI (tmux pane) ─┬─ Purdex mod ── events (Unix socket, batched) ──┐
                    └─ settings hooks · transcript · statusline ──────┤  (fallback)
                                                                       ▼
            daemon: modevents (ingest, per-stream order)  →  agent (lights v2)
                                                          →  conversation model (U1-4..)
                                                          →  usage
            ── HTTP snapshot + cursors / WS events ──►  Mac App · iOS App
```

- The daemon owns normalization [D §3]; clients only render.
- Every mod event is stamped by the mod with a per-load **stream id** and a strictly increasing **seq**; the daemon drops duplicates and counts gaps. A 10 s **heartbeat** carries the full live state so a restarted daemon or a lost batch converges within one beat.
- A pane is joined to its mod stream by **session id** (frames already map pane → session id).

## 5. Phases

| Phase | PR content | Depends on |
|---|---|---|
| **U1-1a** | daemon: Unix-socket listener, `POST /mod/v1/events` ingest, stream registry + in-process bus, `pdx.json` gains `mod_socket` | — |
| **U1-1b** | mod: `hooks/events.js` reporter (queue, batch, retry, heartbeat, final flush); daemon read API `GET /api/mod/streams` | U1-1a |
| U1-2a | daemon lights v2 core: per-pane status from mod events, hook-fallback fixes, background symbol kind | U1-1b |
| U1-2b | daemon lights v2 wire: per-tmux-session priority aggregation, `(epoch, seq)` on `hook` events, complete snapshot with clears | U1-2a |
| U1-3 | SPA lights v2: snapshot replace, ordering, replay not unread, tab aggregation over all panes, corner symbol | U1-2b |
| U1-4 | conversation model types + transcript normalizer (no-mod source) + golden fixtures | — (parallel to U1-2) |
| U1-5 | mod live source: `turn.step` / `tool.call` / `session.append` → items and streaming deltas, merged with the transcript by row uuid | U1-1b, U1-4 |
| U1-6 | conversation API: snapshot with turn limit, `before` / `after` cursors, truncation; WS stream with catch-up incl. `approval.closed` | U1-4 |
| U1-7 | usage: mod `usage` events, statusline fallback, host-level account quota, per-session list summary | U1-1b |
| U1-8 | capabilities object; pane → current conversation key; conversation-switched notification | U1-6 |
| U1-9 | writes: send / interrupt / steer, `client_msg_id`, daemon-side typing when there is no mod | U1-6, U1-8 |

U1-1 (a + b) is the first development segment; it ends with a deploy and a live acceptance (§6.7). Each later phase is planned when it starts; a phase that grows past the limit splits again.

## 6. U1-1 — the mod event channel (detailed contract)

### 6.1 Socket

- Path: `ModSocketPath(dataDir) = <dataDir>/mod.sock`, made absolute with the directory's symlinks resolved — where the socket is bound (next point). The daemon module and the `pdx.json` writer compute it with the same function, so `mod_socket` names the bound socket and is omitted whenever the daemon would refuse the path. If that path is longer than 100 bytes the channel is **disabled** (logged once, reported by the read API as `reason: "path_too_long"`); there is no `/tmp` fallback. Default mlab path `/Users/wake/.config/pdx/mod.sock` (31 bytes).
- The socket's directory is resolved with `EvalSymlinks`, and the socket is bound at `<resolved dir>/mod.sock`. The resolved directory must be owned by the daemon's effective uid and not group- or other-writable (mlab: `~/.config/pdx` is `drwx------`), and **every ancestor up to `/`** must be owned by that uid or by root and be either not group/other-writable or sticky (like `/private/tmp`, `drwxrwxrwt`); otherwise disabled (`reason: "unsafe_dir"`). Like OpenSSH's secure-path check, this means no other user can rename or replace any component of the path, so the stale-file check, bind and chmod below are race-free against other users.
- Start (module `Start`): if the path exists, `Lstat`: a socket that accepts a connection within 200 ms → another daemon owns it → disabled (`reason: "in_use"`); a socket that refuses → removed and rebound; anything that is not a socket → disabled (`reason: "not_a_socket"`), never removed. After bind, `chmod 0600` (defence in depth).
- **The peer-uid check is the gate**: every accepted connection's peer uid (`LOCAL_PEERCRED` on darwin / `SO_PEERCRED` on linux, `golang.org/x/sys/unix`) must equal the daemon's **effective** uid; otherwise the connection is closed before any byte is read. It wraps `Accept`, so a connection made in the window between bind and chmod is checked like any other. No token.
- Stop (module `Stop`): close the listener first (stops accepts, unlinks the file), then shut the server down within the shutdown budget, then join the module's goroutines; `Stop` returns only after all three. Listener fds are close-on-exec, so an exec-self restart that skipped `Stop` leaves only a dead socket file, which the new image's stale check removes.
- Its own `http.Server`: `ReadHeaderTimeout 5 s`, `ReadTimeout 10 s`, `WriteTimeout 10 s`, `MaxHeaderBytes 16 KiB`. Only `POST /mod/v1/events`; any other method or path → 404/405. Body cap 1 MiB → 413.

### 6.2 Wire v1

```
POST /mod/v1/events
{ "v": 1,
  "stream": "<8–64 chars [A-Za-z0-9_-], minted per mod load>",
  "agent": "cc",
  "cc_version": "2.1.293",             // $.session.version()
  "mod_version": "1.0.0-alpha.596",    // the extracted VERSION file; "" if unreadable
  "dropped_total": 0,                  // events this stream lost so far (queue overflow + 400-rejected batches), cumulative
  "cwd": "/Users/wake/Workspace/x",    // where the session runs (session.start's cwd, kept across /clear and resume)
  "interactive": true,                 // the reporter runs only for interactive sessions (U4 may send false)
  "events": [ { "seq": 1, "at": 1791409762960, "sid": "<lowercase uuid>",
                "type": "turn.start", "data": { … } } ] }
```

- 200 `{"ack": N}` — N is the highest seq of this stream the daemon has applied (including earlier batches). The mod removes every queued event with `seq ≤ N`.
- 400 `{"error": "<code>"}` for: `v` ≠ 1 (`unsupported_version`), bad JSON or trailing data after the object (`bad_json`), bad stream id (`bad_stream`), 0 or > 500 events (`bad_events`), seq not strictly increasing within the batch (`bad_seq`), a bad `sid` (`bad_sid`), an event whose `type` does not match `^[a-z][a-z0-9._-]{0,63}$` or whose `data` is missing or not a JSON object (`bad_event`). The mod drops a batch answered 400 (no poison loop) and adds its event count to `dropped_total`; the daemon counts the rejection on the stream when the stream id itself was valid.
- 503 `{"error": "registry_full"}` when the registry already holds 256 streams, none can be evicted, and the batch is for a stream it does not know; the mod treats it like any non-400 failure (backoff and resend).
- `dropped_total` is cumulative and never reset by the mod; the daemon keeps `max(stored, received)`, so a resent batch (a 200 whose response was lost) or a batch in flight while more events are lost cannot double-count or erase a loss.
- Events whose `seq ≤` the stream's last applied seq are skipped (retries). `seq > last + 1` increments the stream's `gaps` and is applied — except the first batch of a stream the registry has never seen (after a daemon restart a live stream resumes at its current seq; that is not a gap).
- Unknown `type`s are accepted, counted under `unknown`, and not delivered (a newer mod against an older daemon).
- `at` is the mod's `Date.now()` in ms; the daemon orders only by seq **inside one stream**. Across sources (a hook against the mod) ordering uses the event's `at`, which is the mod's `Date.now()` and comparable with the daemon's clock because the mod reaches the daemon only through a Unix socket on the same host (one wall clock); the daemon clamps it to the time it received the event (a missing `at` or one later than that counts as now) and does not believe one that is more than 30 s (`atSkewWindow`, = `LiveWindow`) away from it, either way (a Unix second sent as milliseconds, a version mismatch, a wrong mod clock): that event counts as happening on arrival and the stream counts it in `AtRejected`.
- `cwd` and `interactive` (U1-2a-1) are on every batch, so a daemon that restarts under a live stream — which never sees that stream's `session.start` again — knows both from its first batch. The registry copies a non-empty `cwd` and a `true` `interactive` from every envelope; a batch without them (an older mod) leaves what `session.start` set. Both are optional on the wire (absent → `""` / `false`).

### 6.3 Event types v1

| type | when (mod hook) | data |
|---|---|---|
| `session.start` | `session.start` (interactive only) | `{cwd, surface}` |
| `session.switch` | `classic.SessionStart{source:'clear'|'resume'}` (event `sid` = the new id) | `{prev_sid, source}` |
| `session.end` | `session.end` (also fires on `/clear` and `/resume`, reason `clear` / `resume`, before the switch) | `{reason}` |
| `turn.start` | `turn.start` | `{turn_id}` |
| `turn.complete` | `turn.complete` | `{turn_id, reason, agent_id?, duration_ms, aborted}` |
| `tool.check` | `tool.check` | `{tool, tool_use_id?, agent_id?, decision}` |
| `tool.start` / `tool.end` | `tool.call` around `await next(e)` | start `{tool, tool_use_id, agent_id?}`; end `{tool_use_id, agent_id?, ms, error}` |
| `tool.approved` | `ui.render{component:'ToolUse'}` (U1-2a-1), before `next(e)`, whose answer it returns unchanged | `{tool_use_id}` — once per ask, when the row of a **permission** ask (opened by a `tool.check` decision `ask` on a tool other than AskUserQuestion / ExitPlanMode) is drawn with `isRunning: true` (false while the dialog is open, true about 16 ms after the person approves); the ask then leaves the mirror. A question (AskUserQuestion / ExitPlanMode) is never approved here; it leaves at its `tool.end` |
| `agent.spawn` | `agent.spawn` after `next` | `{agent_id, tool_use_id, background, subagent_type, workflow_run_id?}` |
| `compact.start` / `compact.end` | `session.compact` around `next` | `{trigger, agent_id?}` / `{trigger, agent_id?, ok}` |
| `usage` | `session.measure` | `{context{tokens?, window, percent?}, rate_limits[{kind, percent_used, resets_at?}], cost_usd?, changed[]}` |
| `background` | `classic.Stop` | `{tasks[{id, type, status}], crons}` (shell tasks included; the daemon decides what to show) |
| `heartbeat` | `$.clock.every(10 000)` | `{turn_id?, asks[tool_use_id], compacting, agents?[{id, status}], error, background?}` (`agents` from `$.agent.list()`; **omitted** when that call throws, since `[]` would clear every dot until the next beat). `error` (always present, U1-2a-1): the last main `turn.complete` had `reason: "error"`; cleared by the next main `turn.start`, a `session.switch` and a new `session.start`; a subagent turn never sets it. `background` (U1-2a-1, when a `classic.Stop` has been seen): the same `{tasks, crons}` the last `background` event carried; kept across a `session.switch` (same process), forgotten by a new `session.start`. Together they restore, after a daemon restart, the parts of the live state no later event repeats |

Conversation content (`turn.step`, prompt text, `session.append`) is **not** in v1; U1-5 adds it as new types.

### 6.4 Daemon registry and bus (U1-1a)

- `modevents.Registry`, in memory: per stream `{stream, agent, sid (latest), cwd, interactive, cc_version, mod_version, first_seen (set once), last_seen, last_seq, gaps, dropped_total, rejected, counts{type→n}, ended}` and a ring of its last 256 applied events.
- Delivery: `Subscribe(func(Event)) (cancel)`; events are delivered synchronously in seq order per stream (one mutex per stream), across streams concurrently. Subscribers must not block and **must not call `Apply` on the same registry, directly or indirectly** (the stream's mutex is held during delivery and is not re-entrant); each subscriber is reviewed and tested for this when it is wired. Event `data` is copied on receipt and again for each subscriber and each read, so no caller can alter the stored history.
- A stream is **ended** only by a `session.end` whose reason is not `clear` / `resume` (those are followed by `session.switch` on the same stream); any later event reopens an ended stream.
- Eviction: a stream is removed 30 min after it ended, or after 2 h without events; at most 256 streams — a hard admission limit: a new stream evicts the oldest `last_seen` that is not in the middle of `Apply`, and is refused (503 above) when none can be evicted.
- Not persisted: a daemon restart starts empty, and the next heartbeat (≤ 10 s) repopulates every live stream.
- The registry is published in the core `ServiceRegistry` so the agent module (U1-2) can subscribe and look streams up by sid.

### 6.5 Mod reporter (U1-1b) — `cmd/pdx/plugin/purdex/hooks/events.js`

- One new module imported by `register.js` (`hooks.json` takes one module); `register` calls `registerEvents(on)` once. All `$`-taking helpers are top-level function declarations (M-U1-2).
- Because of M-U1-3, `events.js` registers **without** a matcher only events no other mod file registers without one: `tool.call`, `tool.check`, `agent.spawn`, `session.measure`, `session.end`, `classic.Stop`. The events `register.js` already owns it registers **with** a matcher that every event of interest satisfies, so both hooks load and run (M-U1-3): `session.start{isInteractive:true}`, `turn.start{turnId:/^/}`, `turn.complete{turnId:/^/}`, `classic.SessionStart{source:['clear','resume']}`, `session.compact{trigger:/^/}`. From U1-2a-1 it also registers `ui.render{component:'ToolUse'}` (registered nowhere else; it only observes and returns `next(e)` unchanged, M-U1-6). (Observer functions called from `register.js`'s hooks would need `$`, which cannot cross an import — M-U1-5.) `register` calls `registerEvents(on)` before registering its own hooks, so the reporter's hooks are outermost and see what the relay hooks return; `register.js` gains only the import and that call. `compact.start` / `compact.end` are reported by the reporter's own `session.compact` hook around `next(e)`.
- A Go test over the embedded JS fails when any event is registered without a matcher in more than one place across `register.js`, `ask.js` and `events.js`, so a later change cannot break module loading unnoticed.
- Reports only interactive sessions (`session.start.isInteractive`); headless runs report nothing until U4.
- Socket path from `pdx.json` `mod_socket` (written by the extractor, U1-1a). Absent → the reporter stays off (an install older than U1-1a).
- Hooks only observe: each calls `next(e)` and returns its result unchanged; a reporter failure never changes the engine's behaviour. Every registration has `.catch(($, e, next) => next(e))`: inside a `.catch` handler `next` is replay-safe — when the hook had already called it, `next(e)` resolves to that call's settled result and nothing beneath runs again [d.ts L1158–1170] — so a reporter that throws after the tool ran neither re-runs the tool nor changes its result. Enqueueing itself never throws (guarded internally).
- Timer callbacks are arrows that only call a top-level function with `$` (`$.clock.after(150, () => flushTick($))`), the form M-U1-2's check accepts (the probe's `$.clock.after(0, () => afterStart($, res))` loaded).
- Queue: enqueue stamps `{seq: ++seq, at: Date.now(), sid: current sid}`; a flush is scheduled 150 ms later with `$.clock.after` (never awaited inside a hook). One request in flight at a time; ≤ 200 events per request; each request races a 5 s deadline (a request that misses it counts as failed and its late answer is ignored — resent events are deduplicated by seq). 200 → drop `seq ≤ ack`, reset backoff; failure → retry with backoff 1 s, 2 s, 4 s … capped at 30 s; 400 → drop the batch and add its size to `dropped_total`. Queue cap 1 000 events: overflow drops the oldest and adds to `dropped_total`.
- Heartbeat every 10 s from `$.clock.every`; it is queued like any event.
- `session.end`: queue the event, then a **flush inside the hook** that ignores backoff and any request in flight: one POST of every unacked event from the lowest queued seq (so a slower in-flight batch arriving later is all duplicates), at most the newest 500 (older ones add to `dropped_total`). Awaited; the 1.5 s bound cuts it if the daemon does not answer. The heartbeat is cancelled only when the reason is not `clear` / `resume` (the process goes on after those).
- Session id: read with `$.session.id()` at `session.start` and on `classic.SessionStart` with source `clear` or `resume` (→ `session.switch`); `session.end` uses its own `sessionId`.
- The reporter keeps the mirror state the heartbeat needs (running main turn id, open asks by tool_use_id, compacting); it derives no status.

### 6.6 Read API (U1-1b)

`GET /api/mod/streams` (token auth, TCP): `{socket: {path, enabled, reason?}, streams: [ {stream, agent, sid, cwd, interactive, cc_version, mod_version, first_seen, last_seen, last_seq, gaps, dropped_total, rejected, ended, counts} ]}`.
`GET /api/mod/streams/{stream}/events?after=<seq>`: that stream's ring after `seq` (acceptance and fixture capture).

### 6.7 Acceptance and deploy (end of U1-1b)

- Deploy: ask the deploy coordinator first and check running workers; swap `bin/pdx` (rm → cp → mv) and restart; re-run `pdx setup --agent cc` with `~/.claude/settings.json` backed up (0600, never printed) and compared key by key except `env.CLAUDE_CODE_PLUGIN_DIRS`.
- Live acceptance on mlab with a fresh interactive `claude` in a throwaway tmux session: the stream appears in `/api/mod/streams` with the right sid and cwd; `turn.start` / `turn.complete` arrive in order; a heartbeat every 10 s; `/clear` gives `session.end{reason:clear}` then `session.switch` with the new sid on the same stream; exit gives `session.end`; a daemon restart in the middle loses nothing (`gaps` stays 0, `ack` catches up) or, for events lost, the next heartbeat restores the state.

## 7. Lights v2 (U1-2, U1-3) — contract

Wire statuses stay `running | waiting | idle | error | clear`.

**Per-pane derivation, mod present** (a live stream — last event ≤ 30 s — whose sid is the pane's frame session id):

| status | entered by | left by |
|---|---|---|
| running | main `turn.start`; `compact.start` | main `turn.complete`; `compact.end` |
| waiting | `tool.check` decision `ask`; `tool.start` of `AskUserQuestion` or `ExitPlanMode` (main or subagent) | `tool.end` of that tool_use_id; **`tool.approved` of that tool_use_id** (a permission ask, M-U1-6); main `turn.complete`. Waiting holds while any other ask is open; a new ask enters it again |
| idle | main `turn.complete` reason `answer`, `refusal`, `aborted` | — |
| error | main `turn.complete` reason `error` | main `turn.start`; `session.start`; `session.end` |
| clear | `session.end` (reason ≠ `clear` / `resume`); pid death (sweep) | — |

- While a pane's stream is live, hook-derived *status* for that pane is ignored (hooks still feed identity, provenance, exit records, delegation), with one exception, the hook turn edge below.
- **Hook turn edge** (the mod reports a turn boundary late, so a hook is allowed to beat it at exactly two boundaries): a root `cc` frame's `UserPromptSubmit` that made it running, and its `Stop` that made it idle, are remembered as an edge `{status, at}` whose `at` is when the hook reached the daemon. While the edge wins, the pane shows the hook's status with `source: "hook"`; every other hook (`Notification`, `PermissionRequest`, tool hooks) stays below the mod.
  - *Boundary rule:* the edge wins while it is the newer side, `edge.at > StatusEventAt`, and younger than the TTL. `StatusEventAt` is **when the last light-moving mod event happened**, that is the event's own `at` (millisecond precision) clamped to the time the daemon received it, and it does not move back, with one exception (clock rollback, below). Light-moving events are the ones that touch the inputs of the status (turn start / complete, asks, compaction, session boundaries) and a heartbeat that actually changed the status; usage, background, `agent.spawn` and a heartbeat that repeats the state do not move it.
  - *Time basis = event time, not receive time.* Mod events reach the daemon late (§3, M-U1-8), so a `turn.start` that happened before a `Stop` can be received after it; compared by receive time it would read as the mod having caught up and show a running that is already over. Compared by event time it is older than the edge and changes nothing. A mod event takes the edge over (source back to `mod`, the pane is re-emitted even when the status is the same) only if it moved `StatusEventAt` and happened at or after the hook's arrival; a late event that happened before the hook neither takes it over nor dirties the pane.
  - *Millisecond tie.* `at` has millisecond precision and the hook's arrival nanoseconds, so the edge's time is compared truncated to whole milliseconds and a tie goes to the mod: an event that happened in the millisecond the hook arrived counts as at or after it (the mod has caught up).
  - *Clock rollback.* `StatusEventAt` is clamped to the receive time it was set at, so a value later than a newer event's receive time means the wall clock was set back (NTP, a wake from sleep). Any event, light-moving or not, then starts it over from zero before its own value is merged; otherwise a mark left in the future would make every later edge lose. Until that event arrives (a heartbeat comes every 10 s) the edge's readers, which compare against it, treat a mark later than the clock as zero, so a hook in that gap still builds an edge and wins.
  - *TTL 5 s.* An edge the mod never confirms (a slash command submits a prompt and starts no turn) expires after 5 s and the worker re-emits the mod's light; the measured delivery lag is up to about 3 s. A mod that is later than the TTL shows its old light until its events land, or until its stream goes stale (30 s).
- Subagent dots: `agent.spawn` without `workflow_run_id` adds; that agent's `turn.complete` removes; the heartbeat's `agents` list reconciles (agents not listed and not workflow-spawned are removed).
- Background symbol from the last `background` event: any `workflow` task → `workflow`; else any `monitor` → `monitor`; else `crons > 0` → `schedule`; else none. Cleared by `session.end`.
  - **What is a `monitor` task.** Claude Code's `BackgroundTaskSummary.type` `monitor` is an MCP watch task; the **Monitor tool** starts an ordinary background shell task, which `classic.Stop` lists as `type: "shell"` (measured on 2.1.295: `{id, type: "shell", status, description, command}`). The Monitor tool's `tool.call` result carries the id (`{ref, result: {taskId, timeoutMs, persistent}, text}`, `taskId` equal to the listed `id`), so the mod remembers those ids (module level, cleared at `session.end` and `session.switch`) and sends the task as `type: "monitor"` in its `background` event and in the heartbeat mirror — on a copy, never touching Claude Code's object. The daemon rule above is unchanged.
  - **Known limits.** Without the mod (the hook fallback) a Monitor tool task is a shell task and draws no symbol; after a mod reload, a Monitor task that is still running stays `shell` until Monitor is called again, because the id set is only rebuilt from new calls.
- Heartbeat reconciliation: `turn_id` present → running unless waiting (`asks` non-empty) or error; absent → idle unless error. This repairs lost events (e.g. Esc without `turn.complete`).

**No mod (hook fallback), fixes:**
- `PostToolUseFailure` with `is_interrupt: true` → idle.
- `StopFailure` carrying `agent_id` does not change the main status (it only affects that subagent's dot).
- `Stop` with `background_tasks` / `session_crons` sets the background symbol by the same rule.

**Aggregation and wire:**
- A tmux session's status = highest priority over its panes (error > waiting > running > idle); not "most recently started pane". Dots, agent type and model come from that highest-priority pane only (known limitation in U1: a second agent pane's dots are not merged).
- Every `hook` frame carries, inside its `value`, `epoch` (the daemon's `boot_id`) and `seq`: **one counter for all `hook` frames of the daemon process**, taken and broadcast inside one emit slot (U1-2b-2), so it is contiguous on a connection and therefore also monotonic per session code. Both keys are always present (not the top-level, `omitempty` `HostEvent.Seq`). A client checks gaps per connection: `seq ≠ last + 1` means it lost a frame.
- **Two unrelated counters on `/ws/host-events`:** `hook` frames' `(epoch, seq)` and the `nex.*` frames' `(epoch, bseq)` (#1866) are separate counters, each meaningful only within its own frame type. The `nex` epoch rotates whenever its projector resubscribes to the bus; the `hook` epoch changes only when the daemon boots. A client keeps one cursor per family and never compares across them. (`sessions` frames carry yet another top-level `epoch` / `seq`, the list version.)
- The connect snapshot is the **complete** list of session codes with agents on this host. A client that connects with `?agent=v2` gets it as **one** frame, `{type: "agent.snapshot", value: {epoch, seq: H, sessions: [{session, event}]}}` (an empty host sends `sessions: []`, `seq` spelled out), and is strict: a frame that does not fit its buffer ends the connection, and the reconnect brings a new snapshot. Every frame broadcast before the snapshot has `seq ≤ H`, every later one `> H`. Other clients keep today's per-code replay frames, which now carry `epoch`, `seq: H` and `snapshot: true`.
- Approval: a permission ask leaves waiting on the mod's `tool.approved` (M-U1-6), about 16 ms after the person approves. Known gaps: a subagent's tool row may not be drawn, so its approval is not seen and its ask leaves at `tool.end`; without the mod (hook fallback) waiting lasts until the tool ends, as before U1.
- New fields on `NormalizedEvent`: `background: "workflow" | "monitor" | "schedule" | ""`, `source: "mod" | "hook"`.

**SPA (U1-3):** connects with `?agent=v2`; the `agent.snapshot` replaces that host's agent state (codes absent → cleared); `hook` frames that arrive on a connection before its snapshot are ignored, and so are frames with `(epoch, seq) ≤` the last applied; a different epoch is taken only from a snapshot; a seq gap forces a reconnect; unread follows one rule for live frames and snapshot entries alike — it is set only when the code's previous status is known and different (a snapshot never marks a first-seen or unchanged code, so a reconnect cannot flood unread, but a status that changed while disconnected is a real transition; U1-3 plan ruling 2); the tab light is the highest priority over **all** its panes; the corner symbol renders per N6; dots exclude workflow agents (the daemon never sends them).

**U1-3 notes (from the lights-3 smoke work; the SPA must not mark unread for these).** Unread is set only on a real status *transition*, not on every idle / waiting / error frame (today `useAgentStore.ts` ~294–311 marks every one). The daemon legitimately sends the following same-status frames, none of which may set unread:

1. A change of representative pane or model with the status unchanged (the a-3b digest includes the representative frame id and agent type).
2. A frame whose only change is `background` (one pane already emits it, #607; with several panes a non-representative pane's background change does too, b-1).
3. The hook → mod hand-over (#2015 on): `running (hook)` → `running (mod)` at the start of a turn, `idle (hook)` → `idle (mod)` at its end — same colour, new `source`, two idles in a row.
4. A subagent's hooks repeating idle (a session with subagents emits on every `PdxPreToolUse` / `PostToolUse`; about 25 frames in 65 s were seen).
5. A `PdxSubagentStop` idle of unknown origin (seen once, from a session that started no subagent, with an `agent_id`).
6. The reconnect replay: the `agent.snapshot` of `agent=v2` replaces the stopgap the legacy replay frames needed.

Other facts for U1-3: `(epoch, seq)` and `agent.snapshot` exist only from b-2 / b-3 on (earlier frames carry neither); in a multi-pane session dots / agent type / model come from the representative pane and `background` is the highest across panes (known limit); wire `model` is present only when the hook came from the representative pane, and the SPA keeps its previous model on an empty one; a permission box's `waiting` comes from the mod's `tool.check` (a hook's Notification / PermissionRequest must not override the mod) and arrives about 1 s after the hook; payloads not yet seen in a live run: the Stop `monitor` / `workflow` task types, the raw `session_crons` array, and a `PostToolUseFailure` carrying `is_interrupt` (smoke and mod streams only showed subagent / shell tasks and the cron count).

**Known limit of the snapshot's non-tmux part.** `nonTmuxLast` is written by the emit slot in slot order, not request order. A SessionEnd that overtakes an older event of the same session could leave a stale non-tmux entry in the snapshot; a CC command hook waits for the previous hook, so one session's hooks reach the daemon in order, and a stray entry is dropped after 2 h (and the table is capped at 1024 codes).

## 8. Later phases — contracts to hold

- **Conversation model (U1-4, U1-5)** — [D §14] types: `Conversation{key: host_id+provider+session_id, backend, provider, title, status, capabilities, usage, turns[]}`, `Turn{id, started_at, ended_at?, outcome}`, items `UserMessage{…, client_msg_id?}`, `AgentText{…, streaming?}`, `Thinking{duration_ms, text?}`, `Step{kind ∈ edit|execute|read|search|fetch|task|other, status ∈ running|done|failed|denied, …, children?}`, `System`. The transcript normalizer evaluates reusing Nexen `prelude` classification (needs an import-boundary allowance in `internal/module/nex/imports_test.go` or an exported deriver) against an own parser; golden fixtures come from the iOS samples and from recorded mod streams. Mod items merge with transcript rows by row `uuid` (`session.append` carries it). **U1-4's detailed contract is §8.1.**
- **API (U1-6)** — `GET /api/conversations/{key}` snapshot capped to the last N turns with `before` for older turns and `after` for increments; per-step output capped with `truncated`; images replaced by placeholders; a dedicated WS per conversation (not the host-wide broadcast) with `after` catch-up that also replays `approval.request` / `approval.closed`. **U1-6's detailed contract is §8.2.**
- **Usage (U1-7)** — per session from `usage` events, statusline fallback (also parse `rate_limits` and `cost`); account quota at host level (newest reading across sessions); a list summary per session code: `status, epoch, seq, context_percent, background, unread_basis`.
- **Capabilities (U1-8)** — [D §13] graded, fail-closed, named reasons; permanent `provider_unsupported` hides, temporary reasons disable with text; unknown = loading. Pane → current conversation key in the sessions list and a `conversation.switched` event, aligned with the conversation-entity identity (`docs/specs/2026-10-06-conversation-entity-spec.md` §4.1, D9).
- **Writes (U1-9)** — send `{text, client_msg_id}` → `{state: sent|queued|rejected, reason?}`; interrupt; steer. With the mod: a daemon → mod command channel over the same socket (long poll from a mod timer) → `$.prompt.submit`, `$.turn.abort`, `$.session.append`. Without the mod: the daemon types (atomic text + Enter, refuse in copy mode, at most one Esc per turn — two Esc on an idle prompt opens Rewind). Answers use the existing P8a decide; with no mod every ask is terminal-only [D §9].

### 8.1 U1-4 — conversation model and transcript normalizer (detailed contract)

Scope: the model's Go types and their JSON form; a normalizer from a Claude Code transcript (the no-mod source, and the history every source starts from) to turns and items; golden fixtures shared with both Apps. Not in U1-4: the API (U1-6), the mod live source (U1-5), usage beyond model / effort (U1-7), capabilities beyond the transcript's own part and the pane → conversation key (U1-8), writes (U1-9). Facts: M-U1-7.

**Packages.** `internal/convmodel` (types, JSON, `Validate`) and `internal/convmodel/ccnorm` (the Claude Code transcript normalizer). Both import only the standard library (and `ccnorm` imports `convmodel`); no file I/O, no clock — callers pass bytes and the liveness flag. An import-boundary test keeps it so (U1-5 and U4 reuse them from other modules). The name avoids `internal/conversations` (the existing title / first-prompt index).

**Wire form.** JSON, `snake_case`, times as **integer milliseconds since the epoch**, optional fields omitted when empty. Items are one array with a `type` discriminator.

Additive evolution only: later versions may add item types, fields and enum values (source, kind, status, denial, system.kind, outcome); they never rename or remove one. Clients ignore an unknown item type (skip the item) and treat an unknown enum value as unknown, never failing the whole document. (The Go decoder follows the same rule: an unknown `type` decodes to an `Item` whose `Type` is the raw string and whose variants are all nil, without an error; `Validate` and `Marshal` only constrain what the daemon itself produces.)

```
Conversation { key{host_id, provider, session_id}, backend, provider, title, status,
               capabilities, usage?, turns[] }
Usage        { model?, effort?, context?{tokens?, window?, percent?},
               rate_limits?[{kind, percent_used, resets_at?}], cost_usd?, at? }
Turn         { id, index, started_at, ended_at?, outcome: done|interrupted|failed|running,
               error?{kind, message}, items[] }
item user        { id, at, text, truncated?, source, from?{kind, name?}, images?[{media_type, bytes}],
                   client_msg_id? }
item agent_text  { id, at, markdown, truncated?, streaming? }
item thinking    { id, at, text?, truncated?, duration_ms? }
item step        { id, at, kind, tool, status: running|done|failed|denied, denial?, summary,
                   started_at, duration_ms?, input, input_truncated?, input_partial?,
                   output?{text, total_lines, total_bytes, truncated, keep?: head|tail (only when truncated), images?[…]},
                   diff?{path, added, removed, exact, hunks[{old_start, old_lines, new_start, new_lines, lines[]}], truncated?},
                   command?{text, description?, exit_code?, background_task_id?},
                   subagent?{agent_id, description?, type?, async?}, children? }
item system      { id, at, kind: interrupted|compacted|handoff|model_changed|resumed|command_output, detail? }
```

- `key` is the three fields, not a joined string: the conversation-entity line owns the identity and U1-8 settles any string form (§8 Capabilities). U1-4 fills `key.session_id` and `provider: "claude"`; `host_id`, `backend` and `status` are filled by the API (U1-6 / U1-8) and are empty in the normalizer's output and in fixtures.
- Fields marked for later phases exist in the types now and stay empty in U1-4: `client_msg_id` (U1-9, G2), `streaming` / `input_partial` (U1-5), `children` inlining (U1-6 decides; U1-4 can normalize a subagent file on request), `usage.context` / `rate_limits` / `cost_usd` (U1-7).
- `title`: the last `custom-title`, else the last `ai-title`, else empty.
- `usage.model` / `usage.effort`: the last main-thread assistant row's `message.model` (never `<synthetic>`) and `perTurnEffort` (else `effort`).

**Ids** (stable across re-normalization and shared with U1-5): a turn is its opening row's `uuid`; `user`, `agent_text`, `thinking`, `system` items are their row's `uuid` (`<uuid>#<n>` for the n-th further item from one row, n ≥ 1, which only pre-2.1.29x multi-block rows produce); a `step` is its `tool_use_id`; a derived `system` item (handoff, model change) is `<turn id>#handoff` / `<turn id>#model`. `Turn.index` is the 0-based ordinal of the turn in the conversation; together with the turn id it is the stable order key `before` / `after` cursors build on (U1-6, G4). Ids are unique **per namespace**: turn ids among turns, item ids among all items of the conversation; a turn and its opening `user` item share the row's `uuid` on purpose. **Positions** (not on the wire, exported to Go callers for U1-6): each turn and item has `created` (byte offset of the row that created it) and `updated` (offset of the last row that changed it — e.g. a late tool result changes an older step); a feed's change list carries the offset of the row that caused each change.

**Rows.** A line that is not a JSON object is skipped and counted. Sidechain rows in a main file are skipped. Metadata types are read only for the title. `isMeta` rows are skipped except a peer message (`origin.kind: "peer"`). `isCompactSummary` rows are skipped (the `compacted` item stands for them). Unknown row types, attachment types and origins are skipped and counted (`Stats`), never an error. Rows before the first turn opening (a file that starts mid-conversation) form a turn with no `user` item, id = the first such row's `uuid`.

**Turns.** A turn opens at:
1. a prompt row: a `user` row with `turnPosition` (2.1.284+), or — older rows — a non-meta `user` text row that is not a tool result, not an interrupt marker, not `<local-command-caveat>`, `<local-command-stdout>` or `<bash-stdout>` continuation of a command already open;
2. a local command (`system/local_command` with `<command-name>`), and a bash-mode `<bash-input>` row (its `<bash-stdout>` prompt row then belongs to that turn instead of opening another).

Everything until the next opening belongs to the turn. An absorbed queued prompt (`queued_command` attachment) is an item **inside** the running turn and does not open one. A `compact_boundary` outside a turn joins the previous turn (or opens a turn with no `user` item when there is none).

**Outcome.** `failed` when the turn's last assistant row is an API error (`isApiErrorMessage`); `error {kind: <error field>, message: <its text>}`, and that synthetic text is not an `agent_text`. Else `interrupted` when the turn holds an interrupt marker (`[Request interrupted by user` prefix, or `interruptedMessageId`), or when it is not the last turn and has no `turn_duration` (a killed process). Else `done` when it has a `turn_duration`, or it is not the last turn, or the session is not live (`SetLive(false)`, see Incremental). Else `running`. `ended_at` is the `turn_duration` row's time, else the interrupt marker's, else the last row's of a closed turn.

**User message `source`** (first match wins). Spellings are normalized first: `turnOrigin` `task_notification` ≡ `origin.kind` `task-notification`, `auto_continuation` ≡ `auto-continuation`; the row's kind is `origin.kind` when present, else `turnOrigin`, else `promptSource` (`system` ≡ an automated row of unknown kind):

| Row | source | from |
|---|---|---|
| `<command-name>` prompt row or local command | `slash` (text `/name args`) | — |
| `<bash-input>` | `bash` (text = the command) | — |
| `origin.kind: "peer"` (prompt row, `isMeta` row or `queued_command`) | `peer` (text = the message body, the `<cross-session-message>` wrapper removed) | `{kind: "peer", name: from-name}` |
| `origin.kind: "task-notification"` (or `queued_command` with `commandMode: "task-notification"`) | `task` (text = the `<summary>`, else the text without tags) | — |
| `turnOrigin: "scheduled"` | `scheduled` | — |
| `promptSource: "queued"` or an absorbed `queued_command` from a human (origin `human` or none, `commandMode: "prompt"`) | `queued` | — |
| `origin.kind` `human` or none, `promptSource` `typed` / `suggestion_accepted` / `sdk` / none | `user` | — |
| any other kind (`coordinator`, `plugin`, `auto-continuation`, `system` without a recognised `origin`) | skipped and counted; such a row opens **no** turn | — |

User text keeps `[Image #n]` markers; image blocks become `images[{media_type, bytes}]` (the decoded size; the base64 is never kept). A bash-mode turn's output and a local command's `<local-command-stdout>` become one `system {kind: command_output, detail}`.

**Agent text and thinking.** Each non-empty `text` block is one `agent_text` (`markdown` verbatim). Each `thinking` block is one `thinking` item with `text` when non-empty and `duration_ms` from `thinkingDurationMs`; a block with neither is dropped. `<synthetic>` rows are never agent text.

**System items.** `interrupted` at the interrupt marker (the marker text itself is not a user item); `compacted` at `compact_boundary` (`detail.trigger`); `handoff` at the first turn after a change of the rows' `entrypoint` between `cli` and `sdk-cli` (`detail.to: "execution" | "terminal"`); `model_changed` at the first turn whose main-thread model differs from the previous turn's (`detail.model`); `resumed` only when M-U1-4-a confirms a restart marker (else U1-5 brings it from the mod's `session.switch`); `command_output` as above.

**Steps.** One `step` per `tool_use` block, `id` = `tool_use_id`, paired with its `tool_result` by id wherever the result appears (a result with no known step is skipped and counted).
- `kind` by tool name: `Edit` `MultiEdit` `Write` `NotebookEdit` `apply_patch` → `edit`; `Bash` `exec_command` `Monitor` → `execute`; `Read` → `read`; `Grep` `Glob` `WebSearch` → `search`; `WebFetch` → `fetch`; `Agent` `Task` `spawn_agent` → `task`; anything else (incl. `ToolSearch`, `AskUserQuestion`, `ExitPlanMode`, `Skill`, `mcp__*`) → `other`.
- `summary`: `Bash` → `command`; `Read` `Edit` `MultiEdit` `Write` `NotebookEdit` → the basename of `file_path` (`notebook_path`); `Grep` `Glob` → `pattern`; `WebFetch` → `url`; `WebSearch` → `query`; `Agent` `Task` `Monitor` → `description`; `Skill` → `skill`; `AskUserQuestion` → the first question's `question`; `mcp__<server>__<tool>` → `<server> · <tool>`; else the first string input value in key order; always the first line only.
- `status`, first match wins: `toolDenialKind` present (any value, with or without `is_error`) → `denied`, `denial` = that value; `is_error` and the text contains `doesn't want to proceed`, `[Request interrupted by user` or `was rejected` (older versions) → `denied`, `denial: "user-rejected"`; any other `is_error` (`Exit code N`, `<tool_use_error>`, hook blocks) → `failed`; a result → `done`; no result → `running` while the turn is `running`, else `denied` with `denial: "interrupted"`. The model only says `denied` + `denial`; whether a client shows 已拒絕 or 已中斷 for `denial: "interrupted"` is U3's call (lead ruling D4).
- `started_at` = the tool_use row's time; `duration_ms` = result row time − that (U1-5 replaces it with the mod's measured time).
- `input`: the tool input object with every string value capped at 4 KiB (head) and the whole at 16 KiB; `input_truncated` whenever the stored input is not the complete tool input (a string cut, the whole-input cap, nesting deeper than 32 levels, a dropped member). A tool input that is not a JSON object is stored as `{}` without the flag.
- `output` (when there is a result): the text of the result content (string, or the `text` blocks joined by `\n`; a `<persisted-output>` wrapper is unwrapped as Nexen prelude does), `total_lines` / `total_bytes` of the whole text; text kept up to **16 KiB**, cut on a line boundary — the **tail** for `execute`, the **head** otherwise (`keep`, present only when `truncated`: an uncut output has no `keep`), `truncated` when cut. Image blocks become `output.images[{media_type, bytes}]` and a line `[image]` in the text; the `[image]` lines are part of the text and of both totals. Lines are split on `\n` only (`\r` stays in the text); `total_lines` of an empty text is 0, and a trailing `\n` does not start a further line. A single line longer than the cap is cut by bytes on a UTF-8 boundary.
- `diff` (kind `edit` with a result carrying `structuredPatch`): `path` = `file_path`, hunks as given, `added` / `removed` counted from the `+` / `-` lines, `exact: true`; without a patch (denied, failed, older rows) the diff is built from `old_string` / `new_string` / `content` (`MultiEdit` edits concatenated) with `exact: false`. Hunk lines capped at 400 in total (`truncated`).
- `command` (kind `execute`): `text` = `command`, `description`, `exit_code` from a result text starting `Exit code N`, `background_task_id` from `toolUseResult.backgroundTaskId`.
- `subagent` (kind `task`): `agent_id` from `toolUseResult.agentId`, `description`, `type` = `subagent_type`, `async` = `toolUseResult.isAsync`. The normalizer can turn that agent's file into the items of one pseudo-turn (`children`), with the same rules.

**Text caps.** `user.text`, `agent_text.markdown` and `thinking.text` are kept up to 64 KiB (head) with `truncated`.

**Incremental.** A normalizer covers one transcript file **from offset 0**, fed complete lines (no `\n`) with their byte offsets, contiguously: a line whose offset is below the next expected offset is a replay and is ignored (no change reported); a line beyond it (a gap) is refused with an error and changes nothing. A truncated or replaced file (the transcript API's `reset`, a new `transcript_id`) means a new normalizer. Feeding a file in any split of whole lines gives the same model as feeding it at once. Each feed returns the changed turns and items with the causing offset. **Liveness** is separate state: `SetLive(false)` — called only once the caller has fed to the end of the file and the session is no longer live — closes the last open turn (`done`, its result-less steps `denied{interrupted}`); `SetLive(true)` reopens it if it has no end marker; both return the changes. The default is live.

**Capabilities from the transcript** (U1-4's part; U1-8 completes the object): `source: "transcript"`, `text_streaming: "message"`, `thinking: "duration"`, `subagent: "partial"`. Every other capability is **omitted** — [D §13]: an undeclared capability is unsupported (some, like `send` and `interrupt`, have no `none` value) — and listed in `reasons` with `not_wired` (fail-closed). A missing `capabilities` object (not an incomplete one) means "still loading".

**Golden fixtures (v1).** Directory `testdata/conversation/v1/` at the repo root (Go ignores `testdata`): `MANIFEST.json` `{version, cases[{name, source: "cc-transcript", cc_version, description, input, expected, sha256{input, expected, facts}}]}`; per case `cc-transcript/<name>/input.jsonl` (scrubbed) + `expected.json` (`{"conversation": <wire form above>, "live": bool}`, pretty-printed, stable key order) + `facts.json` (hand-written, never regenerated: turn count, each turn's id / outcome / user source, each step's id / kind / status / denial, output totals, and `keep` for a cut output only) + `README.md` (how it was recorded, what it covers). The daemon test normalizes `input.jsonl` and compares with `expected.json` (`-update` regenerates); a second test checks `facts.json` against the normalizer independently of `expected.json`; the Apps fetch only `MANIFEST.json`, `expected.json` and `facts.json` (never `input.jsonl`, which is the daemon's test input), decode `expected.json` into their model and pin the copy by `MANIFEST.json` sha256 at a named commit. A guard test fails when a fixture contains a home path, a tailnet address, an e-mail address or a secret-shaped string. `mod-events/<name>/` is reserved for U1-5.

### 8.2 U1-6 — conversation API (detailed contract)

Scope: read one Claude Code conversation by its key, page through it, follow it live, and see its open approvals. Not in U1-6: the mod live source (U1-5 — the follower here reads the transcript file), usage beyond model / effort (U1-7), the pane → key map and capabilities beyond the transcript's (U1-8), writes (U1-9), Codex / execution conversations (U4 / U5).

**Key and addressing.** The host is the daemon being asked (`key.host_id` = its `host_id`); the path carries the other two parts: `/api/conversations/{provider}/{session_id}` with `provider = claude` (anything else → 404 `provider_unsupported`) and `session_id` a lowercase CC session UUID (else 400 `bad_session_id`, before any file access). U1-8 may add a string form; these paths stay.

**Where the transcript comes from** — the resolver returns an **open file** (never a bare path), opened by the descriptor-relative `openat(O_NOFOLLOW)` walk under the symlink-resolved projects root (`internal/module/agent/transcript_path.go`):
1. a **live pane** running the session: frames whose session id matches, each confirmed by the existing owner resolution (process generation, pane membership, the second confirmation) with `owner.SessionID == session_id`; several confirmed panes → the one seen last; its hook-reported transcript path;
2. else the **conversation index** row for the session id; a candidate that is missing or fails containment is skipped (not an error);
3. else a **bounded lookup** of `<slug>/<session_id>.jsonl` across the root's slug directories (depth 1 only, at most 2000 directories, at most 1 s, cancelled with the request; unreadable directories skipped);
none → 404 `not_found` (also for a containment failure — never an error that leaks the path). An owner lookup that errors does not fail the request: the conversation is served with `live: false, status: "unknown"`.

**Normalizer cache.** One `ccnorm.Normalizer` per conversation (the same entry for concurrent requests), fed in byte order from its open file; at most 16 cached, pinned while a request or a WebSocket uses them, evicted when unpinned and idle for 10 min, oldest first; all 16 pinned → 503 `busy`. Each instance has a random **epoch**. A new instance (new epoch) starts when: the file shrank; the path now resolves to a different file (re-resolved on every HTTP request and every 10 s on a WebSocket); the 64 bytes before the last fed offset changed (a same-inode truncate-and-rewrite); or the normalizer reported a gap. Liveness: `live` = a confirmed live pane runs the session (`SetLive`). Lines over 8 MiB are skipped without reading them into memory (`ccnorm` gains `Skip(offset, length)`, counted as `line:oversize`).

**Revisions and cursor.** Every feed and every `SetLive` that changes the model bumps the entry's **revision**; each turn and item records the revision that last changed it (from the normalizer's change list, so liveness-only changes are included). `cursor = "<epoch>:<revision>"` (opaque to clients). A cursor whose epoch is not the entry's current one is **stale**: the answer carries `reset: true` and a fresh window instead of changes (a daemon restart or an eviction also makes it stale — one snapshot, nothing lost).

**Header.** `header = {title, status, backend, usage: {model, effort}, live}` — `status` is the pane's light (`running | waiting | idle | error`), `ended` without a pane, `unknown` when the owner lookup failed; `backend` is `"terminal"` while a pane runs it, `""` otherwise. Every snapshot, increment and WebSocket change frame carries the current header (conversation-level fields such as a new title or model are never lost by a cursor).

**Snapshot** `GET /api/conversations/claude/{session_id}?turns=N&before=I`:
- `turns` default 20, range 1–200; `before` = a `Turn.index` — the window is the last `N` turns with `index < before` (absent = the newest `N`); or `around=<item_id>` (not with `before`) — the `N` turns that hold that item's turn, as close to the middle as the ends allow; an unknown item id → 404 `item_not_found` (a client jumping back to a remembered reading position, iOS review 10-09).
- Response `{conversation, header, window: {first_index, last_index, total_turns, has_more_before}, cursor}`; `conversation` is the §8.1 wire form with `key.host_id` filled and only the window's turns.
- **Size: the encoded response body is at most 4 MiB.** Older turns are dropped from the window first (`has_more_before` stays true, `first_index` moves) down to one turn; if that one turn alone is still too large, its **oldest items** are dropped and the turn carries `omitted_items: N` (additive field; the Apps show "N earlier steps not shown"; v1 has no paging inside a turn). Per-step output and input stay the §8.1 caps (`output.truncated` + `keep`, `input_truncated`); images are §8.1 placeholders.

**Increments** `GET /api/conversations/claude/{session_id}?after=<cursor>`: `{changes, header, cursor}`, or for a stale cursor `{reset: true, …snapshot fields}`. `changes = [{turn: <turn header: id, index, started_at, ended_at, outcome, error, omitted_items?>, items: [<items changed after the cursor's revision, in turn order>]}]`, oldest turn first; a turn appears when it or any of its items changed; the same 4 MiB body cap (a catch-up that would exceed it answers `reset: true` with a snapshot instead). **Client rules:** upsert the turn header by `id`; upsert each item by `id` within its turn; an item id not seen before is appended at the end of that turn (the normalizer only appends items; later rows update existing ones). Every change carries the item's **full current state**, so applying one twice is harmless. A change for a turn older than the client's loaded `first_index` may be ignored (the client gets it again when it pages back). A client that follows the WebSocket keeps the WebSocket's cursor; the cursor in a `before=` / `around=` page answer is ignored (taking it could skip changes the client never applied).

**Subagents** `GET /api/conversations/claude/{session_id}/subagents/{agent_id}`: `agent_id` validated with the same rule `ccnorm` accepts (up to 128 characters of its allowed set) before any file access; the file `<session_id>/subagents/agent-<agent_id>.jsonl` next to the transcript, opened by the same descriptor-relative walk; `{items, partial}` from `ccnorm.NormalizeSubagent`; a read error after some items → `partial: true` with what was read; before any → 500 `read_failed`; missing → 404 `not_found`. Snapshots never inline `children` (the client fetches on demand).

**Live stream** `GET /ws/conversations/claude/{session_id}?after=<cursor>&turns=N` — the same auth middleware as every route and as `/ws/host-events` (`Authorization: Bearer <token>`, or a one-time `ticket=` from `POST /api/ws-ticket` for clients that cannot set headers); `turns` applies to **every** snapshot on that connection, including the one after a `conversation.reset`. Validation and cache acquisition happen **before** the upgrade (so 400 / 404 / 503 `busy` are plain HTTP answers; an upgrade failure releases the entry). Frames `{type, seq, value}` with `seq` starting at 1 and contiguous **per connection**:
- first: `conversation.snapshot` (the snapshot response) when there is no `after` or it is stale, else `conversation.changes` (the increment response) catching up from `after`;
- then `conversation.changes` `{changes, header, cursor}` whenever a refresh changed the model (the follower re-stats every 500 ms while the connection lives); `conversation.reset` followed by a fresh `conversation.snapshot` when a new epoch starts; `conversation.header` `{header, cursor}` when only the header changed;
- approvals: `approvals.snapshot` `{approvals: [open approvals whose origin.session_id is this session]}` and then `approval` `{op: "opened" | "closed", approval}`; the snapshot and the subscription are taken **atomically with respect to the team module's broadcasts** (the same ordering the host-wide `approval.request` snapshot has), so an approval closed during the connect is never shown open. **Client rule:** the snapshot replaces the conversation's approval set. (This is the spec's "replays `approval.request` / `approval.closed`": the host-wide event type is `approval.request` with `op`.) A failed approvals read closes the connection (the client reconnects) rather than sending an empty set;
- a conversation WebSocket **counts as a responder** for terminal-only approvals (`hook_permission` / `hook_ask` with `terminal_only`) while it is open, like a host-events subscriber;
- **back-pressure** (iOS review 10-09: a phone on a mobile network): `conversation.changes` frames are built from the revision **last sent** (`ChangesSince(sent)`), and only while the send queue (64 frames) is under half full — a slow reader gets fewer, larger frames instead of a disconnect; a changes frame that would exceed 4 MiB becomes `conversation.reset` + a snapshot; only a queue that still overflows (e.g. a flood of small frames) ends the connection, and the client reconnects with its last `cursor`. A `seq` gap on the client side → reconnect.

**Approval input.** An approval carries the hook's full `tool_input` (team payload, verbatim today). If the daemon ever caps it, the payload sets `input_truncated: true` and both Apps disable "allow" (they never approve an input they could not show whole). The conversation's `Step.input` is the §8.1-capped copy and is never used to decide.

**Errors** `{error: code}`: 400 `bad_session_id` / `bad_turns` / `bad_before` / `bad_around` / `before_and_around` / `bad_cursor` / `bad_agent_id` / `turns_and_after`; 404 `provider_unsupported` / `not_found` / `item_not_found`; 500 `read_failed`; 503 `busy`.

**Finding the key before U1-8** (informative): a client that only knows a pane's tmux session code reads the session id from the pane's provenance and re-reads it when the pane's agent events say the session changed (`/clear`, relay); the old conversation's stream then reports `live: false`.

**Capability** `conversations.v1` in `/api/info` from the PR that ships the snapshot.

## 9. Current-state map (for U1-2 / U1-3 planning)

- Hook ingestion: `cmd/pdx/hook.go` `postHookEvent` (one attempt, 2 s timeout); `internal/module/agent/handler.go` `handleEvent` (error guard :384–409; detail-only CC `PreToolUse`/`PostToolUseFailure` :436–479).
- Derivation: `internal/agent/cc/status.go` (`is_interrupt` unread; `StopFailure` → error incl. subagents).
- Frames: `internal/store/frames.go` (`agent_frames`, unique `(pane_id, pid, process_start_time)`); projection `internal/module/agent/projection.go` and `frame_ops.go` `selectSessionProjectionBy` (:1453, latest `StartedAt`), `buildProjectionNormalized` (:1376).
- Sweep: `internal/module/agent/sweep.go` (2 s; pid death / reuse; proxy pruning).
- WS: `internal/core/events.go` (`HostEvent{type, session, value, epoch?, seq?}`, 64-message buffer drops on overflow); snapshot `internal/module/agent/module.go` `sendSnapshot` (:590; no clears, `raw_event_name: "replay"`).
- SPA: `spa/src/stores/useAgentStore.ts` `handleNormalizedEvent` (:231, no ordering, unread rules :283–305); `spa/src/hooks/useMultiHostEventWs.ts` (no reset on reconnect); `spa/src/hooks/useTabDisplay.ts:52` (primary pane only); `TabIcon.tsx`, `TabStatusIndicator.tsx`, `SubagentDots.tsx`.
- Tests that pin today's behaviour and will change: `handler_test.go` error-guard tests, `frame_ops_test.go` `TestSendSnapshot_*` / `TestReplay_*` / `TestStopFailure_*`, `useAgentStore.test.ts`, `useTabDisplay.test.ts`, `TabIcon.test.tsx`.

## 10. Coordination and risks

- **The mod is shared with the lead/team line** (plan v3 P6-3c, P6-6, P7-2 rewrite `register.js` `turn.start` / `turn.complete` / `session.compact` and bump its relay `VERSION`). U1 keeps its hooks in `events.js` and touches `register.js` only with the import and `registerEvents(on)`, called before the relay's own registrations (§6.5); the relay's `session.start` / `turn.start` / `turn.complete` / `classic.SessionStart` / `session.compact` hooks are unchanged and run inside the reporter's matcher-carrying hooks on the same events. The event channel's `v` is independent of the relay protocol `VERSION`. From U1-1b on, `tool.call`, `tool.check`, `agent.spawn`, `session.measure`, `session.end` and `classic.Stop` are registered without a matcher by `events.js`, so another file must register them with a matcher (M-U1-3; the Go guard test enforces it) — and since `$` cannot cross an import (M-U1-5), "calling into `events.js`" is not an option for hooks that need `$`. The lead/team owner is told before U1-1b merges.
- A `session.end` flush that hangs (no fetch timeout) is cut by the 1.5 s bound; other flushes run from timers and never hold a turn.
- An old mod (pre-U1-1b) or a session started before the mod was installed or with `--safe-mode` reports nothing → hook fallback.
- Interactive-mode event order: measured during U1-1b implementation (M-U1-4); the U1-1b acceptance re-checks it on the deployed daemon.
- The conversation-entity line owns conversation identity; U1-8 aligns with it rather than inventing a second key.
