# Session workbook — spec (v1)

> Status: **approved by the user 2026-10-09** (「工作簿 spec 可以」); §10 surfaces decided the same evening. Discussion hosted by the A-line lead (purdex-1f); the result goes to the interface line (88) for the App side. Sub-project 1 of 2: the **session workbook**. The **employee workbook** (named virtual employees, monthly statistics) is a later round built on these records (§11).

## 1. Problem and goal

Every session is closed: unless you are inside it, a slice from outside does not tell what happened. The workbook keeps, for every Claude Code conversation, a short running record that answers three questions (user, Q1 — all three wanted):

- **A.** What is it doing now, and where is it stuck?
- **B.** How was a given thing decided?
- **C.** What got done in a period of time?

It stays readable after the session is gone. And (user extension) **the push notification of a turn prefers that turn's workbook text**.

## 2. User decisions (2026-10-09 — do not reopen)

1. A, B and C are all wanted.
2. Records are coherent and report-like, not loose fragments: every record is written knowing what came before.
3. **No resident `claude -p`.** Each update is one short-lived Haiku call carrying the running state (current status + the last 3 entries + this turn). Measured: a resident `claude` process is 358 MB on average (max 540 MB, 10 on mlab); one per live session is too much, and the running state keeps the read size fixed.
4. Summarising is a **system-side asynchronous event**: the session never waits for it. When it fails or times out, notifications and pushes take today's path.
5. Cost is negligible (measured ≈ US$0.0007 per call); an hourly cap exists only as a runaway guard.
6. **Push text and workbook text are separate, produced by one call**: a very short push line, a medium workbook entry.
7. Workbook entry length: **≈ 120 characters** (option b).

## 3. Measurements this stands on (1f, 2026-10-09; data in the 1f scratchpad `workbook-exp/`)

- **Isolation.** `claude -p --safe-mode --model haiku --tools "" --strict-mcp-config --no-session-persistence --disable-slash-commands --output-format json --system-prompt-file <f>`, run from an empty directory with `CLAUDE_CODE_PLUGIN_DIRS` unset: OAuth works; no hooks fire, no Purdex session appears, no push is sent. (`--bare` is unusable: it refuses OAuth.)
- **Latency.** One call: p50 ≈ 4.2 s, p90 ≈ 10 s with the final prompt (push + entry + status out); start-up ≈ 1.4 s of it.
- **Prompt rounds** on 30 real turns (a member session and a lead session):
  - A total character limit is not obeyed by Haiku in Chinese (target 120 → average 218; a second "shorten to N characters" call → average 147).
  - **A sentence shape is obeyed**: "at most three sentences, each ≤ 35 characters: what was done; the one key reason; the result or next step" → entry average 122, 7/30 over 140, max 174.
  - **Push line** (target 25, limit 40): average 27, max 40, none over.
  - Examples-based prompting (good and bad examples) beat rules-only; a fields-then-assemble variant read mechanically and was dropped.
  - Failure patterns fixed by the prompt: details (sha, line counts, review bullet lists) crowding the text; another agent's message attributed to the session itself; "you/I" ambiguity (roles are used instead: lead, member, 使用者, or the peer's name).

## 4. Model

```
Claude Code session ──Stop hook──▶ agent module ──TurnEndEvent──▶ workbook module
                                                                    │ queue (per conversation, FIFO)
                                                                    ▼
                                         summariser: claude -p (Haiku, isolated)
                                                                    │ {thing, push, entry, status, thing_done, skip}
                                                                    ▼
                                         workbook.db  ──▶ API / host-events ──▶ App (88)
                                                     └──▶ push module (Stop push prefers `push`)
```

### 4.1 Conversation key

A record belongs to a **conversation**, not a session id: a relay (self or member) continues the same workbook. The key is the **root session id** of the relay lineage: walk `session_lineage.predecessor_session_id` (team module, `relay_store.go:54`) back to a session with no predecessor. A plain `/clear` starts a new conversation (as spec 2026-10-06 line 721 already says: only relays write lineage). The team module exposes a new read-only accessor `RootSessionOf(sessionID) (string, error)` (no such accessor exists today; `PreviousRefs` returns refs, not session ids).

### 4.2 Trigger

The workbook module subscribes to the agent module's `TurnEndEvent` (`internal/module/agent/turn_end_hub.go:20`, `SubscribeTurnEnd`): one per accepted Claude Code main-turn `Stop`, at-least-once, tmux sessions only. Consumers must be idempotent: the module keys a turn by `(session_id, At, Seq)` and drops a repeat.

The turn's input is read from the conversation module's normalised model (U1-6, `internal/convmodel`): the last turn's `user` item(s), `step` summaries and final `agent_text`. If the model is not available in process, the plan adds a read accessor; `TurnEndEvent.Text` (the hook's `last_assistant_message`) is the fallback for the assistant text.

### 4.3 What is summarised, what is not

- A turn with no assistant text and no tool step is **not summarised** (no Haiku call, no entry). This removes the "a peer's report with no reply" turns where the measured prompts attributed the peer's work to the session.
- Subagent turns, headless (exec / Nexen) sessions and Codex sessions are out of scope for v1 (§12).

## 5. The summariser

### 5.1 Call

- Exactly the isolated command of §3, from an empty directory under the data dir, `CLAUDE_CODE_PLUGIN_DIRS` removed from the environment, a 30 s timeout. The system prompt is a file the module writes at start (versioned; §5.3).
- **Concurrency:** at most 2 calls at once on the host; per conversation strictly in order (the running state of turn n+1 needs turn n's output). A conversation whose queue holds more than 3 turns keeps only the newest 3 (older ones become `skipped: backlog`).
- **Hourly cap:** 300 calls per host per hour (peak measured ≈ 100 turns/hour). Over the cap a turn is `skipped: cap`; one log line per hour.

### 5.2 Input (JSON on stdin)

```
{ "previous_status": "<status of the last entry, or ''>",
  "recent_entries": [ {"thing": "...", "entry": "..."} ... up to 3, oldest first ],
  "turn": { "user_prompt": "<≤ 1500 chars, cut with …>",
            "assistant_text": "<≤ 3000 chars, cut with …>",
            "tools": ["Bash: gh pr merge 2222", "Edit: cmd/pdx/relay.go", ... ≤ 20] } }
```

Secrets are scrubbed **before** the call and again on the output (§8).

### 5.3 Output contract

```
{ "skip": bool, "thing": "≤ 16", "push": "≤ 40", "entry": "≤ 3 sentences × ≤ 35",
  "status": "≤ 200", "thing_done": bool }
```

- `thing` — the name of the thing being worked on; the same thing keeps the name from `recent_entries`. It is the push title's subject and the grouping key for C.
- `push` — one line for the lock screen: the main result, or what a person must do.
- `entry` — what was done; the one key reason; the result or next step.
- `status` — rewritten whole each time: doing what, how far, stuck where (if stuck), next step. Answers A.
- `thing_done` — the thing ended in this turn (merged, deployed, abandoned). Answers C.
- `skip` — no new progress (greeting, acknowledgement, waiting).

The prompt is the measured round-5 prompt, kept verbatim in `docs/specs/2026-10-09-session-workbook-prompt.md`: rules + good/bad examples, roles instead of pronouns, "a message that starts with `[甲 → 乙]`, `[report …]`, `Agent "…" finished` is someone else's, not this session's work". It ships in the daemon as a versioned constant; the prompt version is stored with each entry.

### 5.4 Validation and repair (code, not the model)

- Not JSON (after stripping one code fence) → one retry → `failed: format`.
- `push` > 40 → cut at the last punctuation ≤ 40; none → `push` is dropped (the push takes today's body).
- `thing` > 16 → cut at 16.
- `entry` > 150 → **one background re-write** with the sentence-shape prompt (the re-write prompt in the same file; measured average 113, 4/25 over 120); still > 150 → cut at the last sentence end ≤ 150. The push never waits for this step.
- `status` > 200 → cut at the last sentence end ≤ 200.

## 6. Storage — `workbook.db` (new module `workbook`, file mode 0600, the devices/push pattern)

```
wb_entries(
  id INTEGER PRIMARY KEY,
  conv_key TEXT NOT NULL,          -- root session id (§4.1)
  host_id TEXT NOT NULL, provider TEXT NOT NULL,   -- 'claude'
  session_id TEXT NOT NULL,        -- the session the turn ran in
  turn_at INTEGER NOT NULL, turn_seq INTEGER NOT NULL,   -- TurnEndEvent At / Seq (idempotency key with session_id)
  state TEXT NOT NULL,             -- pending | ok | failed | skipped
  reason TEXT NOT NULL DEFAULT '', -- failed: timeout|format|exit|auth ; skipped: no_text|backlog|cap|model
  thing TEXT, push TEXT, entry TEXT, thing_done INTEGER NOT NULL DEFAULT 0,
  team_id TEXT, role TEXT, ref TEXT,   -- who the session was at that moment (employee workbook later)
  prompt_ver INTEGER NOT NULL, latency_ms INTEGER, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL,
  UNIQUE(session_id, turn_at, turn_seq))
wb_status(conv_key TEXT PRIMARY KEY, status TEXT NOT NULL, entry_id INTEGER NOT NULL, updated_at INTEGER NOT NULL)
```

- Raw prompts and assistant texts are **not** stored (the transcript already holds them); only the outputs.
- Retention: kept (≈ 0.5 KB per entry; a busy day ≈ 1,000 entries ≈ 0.5 MB). No automatic deletion in v1.
- Schema rule (deployed tables): any later change is a migration (`feedback_deployed_schema_needs_migration`).

## 7. Push

- Applies to the push of a **Stop** (turn ended: done, or asking the user in text) and **StopFailure** (error). Permission requests and `Notification` keep today's text: they fire in the middle of a turn, before any workbook entry can exist, and their text is already specific ("需要權限：Bash").
- When the push gate (`internal/module/push/gate.go`) has decided to push a Stop, the push module **holds it up to N = 8 s** (a timer, the `holdSet` pattern of `agent_trigger.go:101`; never a sleep in `onNotify`) for that turn's entry. Ready with a `push` → title `{session name}・{thing}` (cut to the title limit), body `push`, and the payload gains `purdex.workbook = {conv_key, entry_id}` so iOS can open that entry (§10.3). Not ready, failed, skipped or no `push` → today's title and body, unchanged.
- Covered by the hold: the measured p90 ≈ 10 s means roughly 8 in 10 Stop pushes get the workbook line. N is a host setting (`workbook.push_wait_s`, 0 = never wait).
- A present Mac still suppresses the push (gate rule 4); the entry is written either way.

## 8. Secrets and personal data (daemon's job)

- Scrub before the call and on the output: `Bearer …`, `sk-…`, `ghp_/gho_…`, `xox…`, `pdxp_…`, `pdxd_…`, AWS key ids, and long mixed letter-digit strings (≥ 32) → `[redacted]`. The regex set of `internal/convmodel/ccnorm/scrub` is the starting point; the plan decides whether to export it or copy the runtime subset.
- Images and attachments are never sent (only text).
- The App does not filter again; it shows what the daemon stored.

## 9. API and events (capability `workbook.v1`)

- `GET /api/workbook/conversations/{provider}/{session_id}?limit=&before=` → `{conv_key, status, status_at, entries:[…newest first]}`; `session_id` may be any session of the conversation (resolved to `conv_key`).
- `GET /api/workbook/entries?since=&until=&thing_done=1&limit=` → entries across conversations of this host, newest first (C: "what got done in this period").
- Host event `workbook.entry` `{conv_key, session_id, entry}` on insert and on every state change (pending → ok / failed / skipped, re-write); host event `workbook.status` `{conv_key, status, updated_at}`.
- Errors: 404 `not_found` (no workbook for that session), 400 `bad_request`.

## 10. Where it is seen (user decision 2026-10-09) and what 88 gets

**Three surfaces, no more in v1.** The user chose: the panel beside a conversation, the team panel line, and iOS. **No standalone workbook page in v1**: reading the workbook of a session that has ended, and the period view for C ("what got done this week"), have no screen yet; the records are kept (§6) and the API serves them (§9), so a later page needs no daemon change.

### 10.1 Mac App — one shared panel: team panel and workbook (user 2026-10-09)

The workbook and the team panel **share one panel area**, and that area can be **enlarged and shrunk** (resize, plus an expanded mode that takes most of the window). Two ways in, one place:

- **From the team panel:** each member row (and the lead's own row) shows 「正在做的任務」 — the **first sentence of the conversation's latest `status`**, one line with an ellipsis, hover for the whole `status`; no workbook yet → no line. **Clicking a row drills into that member's workbook** in the same panel area, with a back control to the team list. A member that has ended (killed, gone, released) still opens its workbook: the records are kept.
- **From a conversation:** a 「工作簿」 toggle (Phosphor `Notebook`) in the toolbar of a Claude Code session's tab (terminal or conversation view) opens the same panel area on that conversation's workbook. Hidden for other agents and for a session with no workbook yet.

The workbook view (either way in):
- Top: 「目前狀況」 — the latest `status`, with its time.
- Below: entries **grouped by `thing`**, the most recent thing first. Each group: the thing's name, 進行中 / 完成 (from `thing_done`), and its entries newest first (time + `entry`). Finished things are collapsed; a click expands them.
- A relayed conversation shows one continuous workbook (one `conv_key`, §4.1).
- **Live:** inserts and state changes arrive through the host events of §9 (`workbook.status` also refreshes the team panel line); no polling.
- **States:** `pending` 「整理中…」, `failed` 「整理失敗」 with the reason on hover, `skipped` not shown.
- **Tab-hosted rule (CLAUDE.md checklist):** the panel's open state, size, which view it shows (team list / which workbook) and scroll position live outside the component and survive a tab switch; a regression test switches away and back with the real `TabContent`.

### 10.2 (merged into 10.1)

### 10.3 iOS — session detail, and opening a push

- **Session detail:** 「目前狀況」 (`status`) and the **last 3 entries** (time, `thing`, `entry`); 「更多」 loads older ones (`before=` paging, §9).
- **Tapping a workbook push** opens that session's detail scrolled to the entry, highlighted. The push carries `purdex.workbook = {conv_key, entry_id}` (§7). A push without it (today's body, the fallback) opens the session as today.

### 10.4 Contract items for 88

1. **Data contract:** §6 fields as returned by §9; the entry states and reasons; `prompt_ver`.
2. Filtering is the daemon's job (§8); the App shows what the daemon stored.
3. Capability `workbook.v1` gates every surface: an older daemon shows none of them.

## 11. Employee workbook (later round)

Each entry carries `team_id / role / ref` at the moment of the turn; the employee workbook (member name pool = virtual employees, "what did this name do this month") is built on these records. Not in this spec.

## 12. Out of scope (v1)

Subagent turns; headless exec / Nexen sessions; Codex sessions; cross-host workbooks (each host keeps its own sessions' records); editing an entry by hand; retention / export.

## 13. Measurements (done before the plan, 1f 2026-10-09)

- **M1 — daemon context.** `claude` resolves on the daemon's own `PATH` (`~/.local/bin` first; the daemon is `pdx serve`, ppid 1). The isolated call of §3 run with a minimal environment (`env -i` with only `HOME`, `USER`, `LOGNAME`, `TMPDIR`, `LANG` and the daemon's `PATH`) authenticates through the keychain and answers (`is_error=false`). WB-1 still logs one line per failed call with the reason `auth` so a booter-started daemon that cannot reach the keychain shows at once.
- **M2 — resources.** One call: max RSS ≈ 288 MB, CPU ≈ 0.5 s user + 0.15 s sys; two at once ≈ 575 MB transient. Hence the concurrency limit of 2 (§5.1).
- **M3 — input source.** The bake-off's 30 turns were already built from the conversation API (`GET /api/conversations/claude/<sid>`, the U1-6 normalised model), so the measured quality is the quality of the production input.

## 14. Phases (each ≤ 800 lines / 20 files)

| PR | Scope |
|---|---|
| WB-1 | `workbook` module: store (§6), `RootSessionOf` accessor in team, `TurnEndEvent` subscriber + idempotency, per-conversation queue, summariser runner (§5.1) with the prompt constant, validation (§5.4), scrub (§8). No API, no push. |
| WB-2 | API + host events + capability (§9). |
| WB-3 | Push hold and body (§7). |
| App | 88's plan, from §10. |
