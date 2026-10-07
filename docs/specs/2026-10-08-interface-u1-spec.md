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
| N6 | Background work: **background shell commands show nothing**. **workflow / monitor / scheduled wake-up** draw one small symbol at the **top-left of the tab's agent icon**, only one at a time, priority **workflow (TreeStructure) > monitor (Eye) > schedule (Clock)**; static, icon colour, hidden when lights are off; the main light is not changed. Blue dots count only subagents the main agent started; workflow agents are represented by the symbol. |
| N7 | Mod rollout: mlab already runs the mod (alpha.586+); **a26 installs it when U1 ships**; P8b is not brought forward; whether Nexen loads the mod is decided at U4. |
| N8 | Phasing: U1 → U2 ∥ U3 → U4 → U5 → U6, communication line last. |
| N9 | iOS adapts to the main design (the design leads, not what iOS already built). |

## 3. Facts (measured)

- **M-U1-1 `$.http.fetch` over a Unix socket works** (Claude Code 2.1.293, `claude -p --plugin-dir`, 2026-10-08): `POST` with headers and body reaches a Unix-socket HTTP server; the request's `Host` is the URL host, `User-Agent: Bun/1.4.3`; `Date.now()`, `$.clock.now()` and `$.session.id()` all work; a fetch from `$.clock.after(0, …)` works; inside `session.end` two sequential fetches completed in 2 ms. Probe: session scratchpad `sockprobe/`.
- **M-U1-2 a mod's static check**: `$` may only be passed to a function declared at the top level of the module (or a `const` bound to one); passing it to an inner arrow makes the module fail to load. Measured with the same probe.
- **M-U1-3 one registration per event without a matcher** (2.1.293): registering the same event twice *without* a matcher makes the whole module fail to load ("on("turn.complete") is registered twice without a matcher"). One registration *with* a matcher and one without, even from two imported files, load and both run, the first registered outermost. Measured: `tool.call{tool:'Bash'}` in the entry file plus `tool.call` in an imported file; `tool.check` (decision `allow`, `rule: "Bash(echo:*)"`) fires **inside** the `tool.call` chain, between its start and its end; `classic.Stop` reaches a mod with `background_tasks: []` and `session_crons: []`; `session.measure` gave `{context{tokens, window, percent}, rateLimits[{kind, percentUsed, resetsAt}], cost{usd}, changed}`; a `tool.call` result has keys `ref, result, text, isReadOnly`.
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
  "events": [ { "seq": 1, "at": 1791409762960, "sid": "<lowercase uuid>",
                "type": "turn.start", "data": { … } } ] }
```

- 200 `{"ack": N}` — N is the highest seq of this stream the daemon has applied (including earlier batches). The mod removes every queued event with `seq ≤ N`.
- 400 `{"error": "<code>"}` for: `v` ≠ 1 (`unsupported_version`), bad JSON or trailing data after the object (`bad_json`), bad stream id (`bad_stream`), 0 or > 500 events (`bad_events`), seq not strictly increasing within the batch (`bad_seq`), a bad `sid` (`bad_sid`), an event whose `type` does not match `^[a-z][a-z0-9._-]{0,63}$` or whose `data` is missing or not a JSON object (`bad_event`). The mod drops a batch answered 400 (no poison loop) and adds its event count to `dropped_total`; the daemon counts the rejection on the stream when the stream id itself was valid.
- 503 `{"error": "registry_full"}` when the registry already holds 256 streams, none can be evicted, and the batch is for a stream it does not know; the mod treats it like any non-400 failure (backoff and resend).
- `dropped_total` is cumulative and never reset by the mod; the daemon keeps `max(stored, received)`, so a resent batch (a 200 whose response was lost) or a batch in flight while more events are lost cannot double-count or erase a loss.
- Events whose `seq ≤` the stream's last applied seq are skipped (retries). `seq > last + 1` increments the stream's `gaps` and is applied — except the first batch of a stream the registry has never seen (after a daemon restart a live stream resumes at its current seq; that is not a gap).
- Unknown `type`s are accepted, counted under `unknown`, and not delivered (a newer mod against an older daemon).
- `at` is the mod's `Date.now()` in ms; the daemon keeps it but orders only by seq.

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
| `agent.spawn` | `agent.spawn` after `next` | `{agent_id, tool_use_id, background, subagent_type, workflow_run_id?}` |
| `compact.start` / `compact.end` | `session.compact` around `next` | `{trigger}` / `{ok}` |
| `usage` | `session.measure` | `{context{tokens?, window, percent?}, rate_limits[{kind, percent_used, resets_at?}], cost_usd?, changed[]}` |
| `background` | `classic.Stop` | `{tasks[{id, type, status}], crons}` (shell tasks included; the daemon decides what to show) |
| `heartbeat` | `$.clock.every(10 000)` | `{turn_id?, asks[tool_use_id], compacting, agents[{id, status}]}` (`agents` from `$.agent.list()`) |

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
- Because of M-U1-3, `events.js` registers only events no other mod file registers without a matcher: `tool.call`, `tool.check`, `agent.spawn`, `session.measure`, `session.end`, `classic.Stop`. For the events `register.js` already owns (`session.start`, `turn.start`, `turn.complete`, `classic.SessionStart`, `session.compact`) it exports plain observer functions that `register.js`'s existing hooks call — one call each; `session.compact`'s relay body moves into a named function wrapped by a hook that reports `compact.start` / `compact.end` around it.
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

- Deploy: ask the deploy coordinator (`mlab/_z9ruk0`) first and check running workers; swap `bin/pdx` (rm → cp → mv) and restart; re-run `pdx setup --agent cc` with `~/.claude/settings.json` backed up (0600, never printed) and compared key by key except `env.CLAUDE_CODE_PLUGIN_DIRS`.
- Live acceptance on mlab with a fresh interactive `claude` in a throwaway tmux session: the stream appears in `/api/mod/streams` with the right sid and cwd; `turn.start` / `turn.complete` arrive in order; a heartbeat every 10 s; `/clear` gives `session.end{reason:clear}` then `session.switch` with the new sid on the same stream; exit gives `session.end`; a daemon restart in the middle loses nothing (`gaps` stays 0, `ack` catches up) or, for events lost, the next heartbeat restores the state.

## 7. Lights v2 (U1-2, U1-3) — contract

Wire statuses stay `running | waiting | idle | error | clear`.

**Per-pane derivation, mod present** (a live stream — last event ≤ 30 s — whose sid is the pane's frame session id):

| status | entered by | left by |
|---|---|---|
| running | main `turn.start`; `compact.start` | main `turn.complete`; `compact.end` |
| waiting | `tool.check` decision `ask`; `tool.start` of `AskUserQuestion` or `ExitPlanMode` (main or subagent) | `tool.end` of that tool_use_id; main `turn.complete` |
| idle | main `turn.complete` reason `answer`, `refusal`, `aborted` | — |
| error | main `turn.complete` reason `error` | main `turn.start`; `session.start`; `session.end` |
| clear | `session.end` (reason ≠ `clear` / `resume`); pid death (sweep) | — |

- While a pane's stream is live, hook-derived *status* for that pane is ignored (hooks still feed identity, provenance, exit records, delegation).
- Subagent dots: `agent.spawn` without `workflow_run_id` adds; that agent's `turn.complete` removes; the heartbeat's `agents` list reconciles (agents not listed and not workflow-spawned are removed).
- Background symbol from the last `background` event: any `workflow` task → `workflow`; else any `monitor` → `monitor`; else `crons > 0` → `schedule`; else none. Cleared by `session.end`.
- Heartbeat reconciliation: `turn_id` present → running unless waiting (`asks` non-empty) or error; absent → idle unless error. This repairs lost events (e.g. Esc without `turn.complete`).

**No mod (hook fallback), fixes:**
- `PostToolUseFailure` with `is_interrupt: true` → idle.
- `StopFailure` carrying `agent_id` does not change the main status (it only affects that subagent's dot).
- `Stop` with `background_tasks` / `session_crons` sets the background symbol by the same rule.

**Aggregation and wire:**
- A tmux session's status = highest priority over its panes (error > waiting > running > idle); not "most recently started pane".
- Every `hook` event and every snapshot item carries `epoch` (daemon boot id) and `seq` (per session code, monotonic). The connect snapshot is the **complete** list of session codes with agents on this host, flagged `snapshot: true`.
- New fields on `NormalizedEvent`: `background: "workflow" | "monitor" | "schedule" | ""`, `source: "mod" | "hook"`.

**SPA (U1-3):** a snapshot replaces that host's agent state (codes absent → cleared); events with `(epoch, seq) ≤` the last applied are dropped; replayed state never sets unread; the tab light is the highest priority over **all** its panes; the corner symbol renders per N6; dots exclude workflow agents (the daemon never sends them).

## 8. Later phases — contracts to hold

- **Conversation model (U1-4, U1-5)** — [D §14] types: `Conversation{key: host_id+provider+session_id, backend, provider, title, status, capabilities, usage, turns[]}`, `Turn{id, started_at, ended_at?, outcome}`, items `UserMessage{…, client_msg_id?}`, `AgentText{…, streaming?}`, `Thinking{duration_ms, text?}`, `Step{kind ∈ edit|execute|read|search|fetch|task|other, status ∈ running|done|failed|denied, …, children?}`, `System`. The transcript normalizer evaluates reusing Nexen `prelude` classification (needs an import-boundary allowance in `internal/module/nex/imports_test.go` or an exported deriver) against an own parser; golden fixtures come from the iOS samples and from recorded mod streams. Mod items merge with transcript rows by row `uuid` (`session.append` carries it).
- **API (U1-6)** — `GET /api/conversations/{key}` snapshot capped to the last N turns with `before` for older turns and `after` for increments; per-step output capped with `truncated`; images replaced by placeholders; a dedicated WS per conversation (not the host-wide broadcast) with `after` catch-up that also replays `approval.request` / `approval.closed`.
- **Usage (U1-7)** — per session from `usage` events, statusline fallback (also parse `rate_limits` and `cost`); account quota at host level (newest reading across sessions); a list summary per session code: `status, epoch, seq, context_percent, background, unread_basis`.
- **Capabilities (U1-8)** — [D §13] graded, fail-closed, named reasons; permanent `provider_unsupported` hides, temporary reasons disable with text; unknown = loading. Pane → current conversation key in the sessions list and a `conversation.switched` event, aligned with the conversation-entity identity (`docs/specs/2026-10-06-conversation-entity-spec.md` §4.1, D9).
- **Writes (U1-9)** — send `{text, client_msg_id}` → `{state: sent|queued|rejected, reason?}`; interrupt; steer. With the mod: a daemon → mod command channel over the same socket (long poll from a mod timer) → `$.prompt.submit`, `$.turn.abort`, `$.session.append`. Without the mod: the daemon types (atomic text + Enter, refuse in copy mode, at most one Esc per turn — two Esc on an idle prompt opens Rewind). Answers use the existing P8a decide; with no mod every ask is terminal-only [D §9].

## 9. Current-state map (for U1-2 / U1-3 planning)

- Hook ingestion: `cmd/pdx/hook.go` `postHookEvent` (one attempt, 2 s timeout); `internal/module/agent/handler.go` `handleEvent` (error guard :384–409; detail-only CC `PreToolUse`/`PostToolUseFailure` :436–479).
- Derivation: `internal/agent/cc/status.go` (`is_interrupt` unread; `StopFailure` → error incl. subagents).
- Frames: `internal/store/frames.go` (`agent_frames`, unique `(pane_id, pid, process_start_time)`); projection `internal/module/agent/projection.go` and `frame_ops.go` `selectSessionProjectionBy` (:1453, latest `StartedAt`), `buildProjectionNormalized` (:1376).
- Sweep: `internal/module/agent/sweep.go` (2 s; pid death / reuse; proxy pruning).
- WS: `internal/core/events.go` (`HostEvent{type, session, value, epoch?, seq?}`, 64-message buffer drops on overflow); snapshot `internal/module/agent/module.go` `sendSnapshot` (:590; no clears, `raw_event_name: "replay"`).
- SPA: `spa/src/stores/useAgentStore.ts` `handleNormalizedEvent` (:231, no ordering, unread rules :283–305); `spa/src/hooks/useMultiHostEventWs.ts` (no reset on reconnect); `spa/src/hooks/useTabDisplay.ts:52` (primary pane only); `TabIcon.tsx`, `TabStatusIndicator.tsx`, `SubagentDots.tsx`.
- Tests that pin today's behaviour and will change: `handler_test.go` error-guard tests, `frame_ops_test.go` `TestSendSnapshot_*` / `TestReplay_*` / `TestStopFailure_*`, `useAgentStore.test.ts`, `useTabDisplay.test.ts`, `TabIcon.test.tsx`.

## 10. Coordination and risks

- **The mod is shared with the lead/team line** (plan v3 P6-3c, P6-6, P7-2 rewrite `register.js` `turn.start` / `turn.complete` / `session.compact` and bump its relay `VERSION`). U1 keeps its hooks in `events.js` and touches `register.js` only with the import, `registerEvents(on)`, one observer call in each of its `session.start` / `turn.start` / `turn.complete` / `classic.SessionStart` hooks, and the `session.compact` wrapper (§6.5); the event channel's `v` is independent of the relay protocol `VERSION`. From U1-1b on, `tool.call`, `tool.check`, `agent.spawn`, `session.measure`, `session.end` and `classic.Stop` are registered without a matcher by `events.js`, so another file must use a matcher or call into `events.js` (M-U1-3; the Go guard test enforces it). The lead/team owner is told before U1-1b merges.
- A `session.end` flush that hangs (no fetch timeout) is cut by the 1.5 s bound; other flushes run from timers and never hold a turn.
- An old mod (pre-U1-1b) or a session started before the mod was installed or with `--safe-mode` reports nothing → hook fallback.
- Interactive-mode event order and latency are re-measured in the U1-1b acceptance (M-U1-1 was `claude -p`).
- The conversation-entity line owns conversation identity; U1-8 aligns with it rather than inventing a second key.
