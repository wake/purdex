# Session workbook — spec (v2)

> Status: v1 **approved by the user 2026-10-09** (「工作簿 spec 可以」); §10 surfaces decided the same evening. **v2 (2026-10-10)** carries the user's decisions of 2026-10-10 (§2 items 8–14): the summariser call moves into the session's own Purdex mod, a read-only todo list with a done record, a manual refresh, and a full-screen session detail page on iOS. Changed from v1: §2, §3, §4, §5, §6, §9, §10, §12–§14 and the prompt (prompt_ver 2, `docs/specs/2026-10-10-session-workbook-prompt-v2.md`). Unchanged: the conversation key and trigger (§4.1, §4.2), push (§7), secrets (§8). Discussion hosted by the A-line lead (purdex-1f); the result goes to the interface line (88) for the App side. Sub-project 1 of 2: the **session workbook**. The **employee workbook** (named virtual employees, monthly statistics) is a later round built on these records (§11).

## 1. Problem and goal

Every session is closed: unless you are inside it, a slice from outside does not tell what happened. The workbook keeps, for every Claude Code conversation, a short running record that answers three questions (user, Q1 — all three wanted):

- **A.** What is it doing now, and where is it stuck?
- **B.** How was a given thing decided?
- **C.** What got done in a period of time?

And (v2) **D.** What does it still owe — and what of that got done?

It stays readable after the session is gone. And (user extension) **the push notification of a turn prefers that turn's workbook text**.

## 2. User decisions (do not reopen)

2026-10-09:

1. A, B and C are all wanted.
2. Records are coherent and report-like, not loose fragments: every record is written knowing what came before.
3. **No resident process.** Each update is one short-lived Haiku call carrying the running state (current status + the last 3 entries + this turn). Measured: a resident `claude` process is 358 MB on average (max 540 MB, 10 on mlab); one per live session is too much, and the running state keeps the read size fixed. *(v2: the call is now made by the session's mod, item 8; the running-state shape stands.)*
4. Summarising is a **system-side asynchronous event**: the session never waits for it. When it fails or times out, notifications and pushes take today's path.
5. Cost is negligible (measured ≈ US$0.0007 per call); an hourly cap exists only as a runaway guard.
6. **Push text and workbook text are separate, produced by one call**: a very short push line, a medium workbook entry.
7. Workbook entry length: **≈ 120 characters** (option b).

2026-10-10 (with cldotdev/claude-todo-list, MIT, as the reference):

8. **The call goes through the Purdex mod's `$.model.complete`**, in the session itself, instead of a daemon-spawned `claude -p`: the session's own client and credentials; a separate blank request (no tools, no history, nothing added to the conversation's context); the system prompt can be cached. A session without the Purdex mod has no workbook.
9. **A todo list and a done record**: the same call keeps the conversation's todo list; items done stay as a record. Both can be read **mixed into the workbook** or **on their own**.
10. **Refresh (分岔重整)**: rebuilding the status and the todo list from the whole conversation through `$.model.fork` (the main model over the full transcript, many tokens) — **manual only**.

2026-10-10, the surfaces of 8–10 (asked in plain words, answered by the user):

11. **The todos are read-only** in the Apps: no tick, no delete; the model (turns and refreshes) alone changes them.
12. **A refresh is asked from two places**: the 「重整」 control of the Mac workbook view, and `/workbook refresh` typed in the session.
13. **Not in the terminal**: no todo band above the prompt (claude-todo-list's) in this version.
14. **iOS shows everything** — status, todos with their details, the done record, all entries — on a **full-screen session detail page**: the conversation screen's ⓘ opens it, and so does a swipe to the left; the old Tab-info sheet becomes a segment or a button of that page (user, 2026-10-10, §10.3).

## 3. Measurements this stands on (1f; data in the 1f scratchpad `workbook-exp/`)

2026-10-09 (v1):

- **Isolation** (v1's daemon path, now history): `claude -p --safe-mode --model haiku --tools "" --strict-mcp-config --no-session-persistence --disable-slash-commands --output-format json --system-prompt-file <f>` from an empty directory worked with OAuth and fired no hooks; `--bare` refuses OAuth.
- **Prompt rounds 1–5** on 30 real turns (a member session and a lead session):
  - A total character limit is not obeyed by Haiku in Chinese (target 120 → average 218; a second "shorten to N characters" call → average 147).
  - **A sentence shape is obeyed**: "at most three sentences, each ≤ 35 characters: what was done; the one key reason; the result or next step" → entry average 122, 7/30 over 140, max 174.
  - **Push line** (target 25, limit 40): average 27, max 40, none over.
  - Examples-based prompting (good and bad examples) beat rules-only; a fields-then-assemble variant read mechanically and was dropped.
  - Failure patterns fixed by the prompt: details (sha, line counts, review bullet lists) crowding the text; another agent's message attributed to the session itself; "you/I" ambiguity (roles are used instead: lead, member, 使用者, or the peer's name).

2026-10-10 (v2; the prompt document has the round-by-round numbers):

- **Todos, rounds 6–8** on the same 30 turns, one call producing push + entry + status + todos:
  - Round 6 (claude-todo-list's definition of an open item): todos bloat — one turn added 7, items finished later were not closed; the member lane ended with 14 open.
  - Round 7 (**the v2 prompt**): a todo is only what this session itself owes and nothing else tracks; work handed to a phase / PR / issue, decisions, other people's work and routine waits are not todos; at most 2 added per turn; every open todo is checked each turn. Open at the end: member 5, lead 4. Push and entry unchanged (push average 29; entry average 112, 1/30 over 140).
  - Round 8 (round 7 + **`effort: low`**): entry average 120 (2/30 over 150), push average 28 (none over 40), no parse failure; todos close better (member: 9 added, 5 done, 1 dropped, 3 open at the end). Residue: a routine wait ("merge（等 lead 放行）") was listed twice, and closed when it happened.
- **Latency of one call, one lane** (8 turns, through `claude -p`): round-5 prompt API p50 4.3 s; v2 prompt API p50 5.8 s; **v2 prompt with `effort: low` API p50 2.6 s, 7/8 within 8 s**. The `claude -p` start-up (1.4–3.7 s of wall time here) is not on the mod path. With two lanes at once the wall time rose to p50 10–11 s; the mod path is measured end to end in the plan (M4, §13).
- **The mod API** (Claude Code 2.1.294 types):
  - `$.model.complete({model, system, prompt, maxTokens, effort, timeoutMs}, {signal})`: the session's own client; no tools, no history, no system prompt beyond the CLI's identity block and `system`; `model` an alias (`haiku`) resolved and allow-listed like `--model`; `maxTokens` default 1024; `system` / `prompt` blocks can be marked `cache: true` (5-minute prompt cache). Resolves `{isAnswered: true, text, usage}` or `{isAnswered: false, reason}` with `reason` = `api-error` (HTTP `status`, `error` kind such as `rate_limit`, `authentication_failed`) / `empty-reply` / `aborted` (its `timeoutMs`, its signal, or the dispatch that made it aborted); only a request the engine refuses to send (a blocked model, a bad cap) rejects.
  - `$.model.fork({prompt})`: the main thread's last request again — its model, system prompt, tools (all denied) and messages — with one more user message; the prefix comes from the main thread's prompt cache while it is warm. Same result shape plus `reason: 'nothing-to-fork'` (before the first answer, after `/clear`).
  - A call made from a `$.clock.after` timer is not cut when the user interrupts a turn (claude-todo-list and the mod's `events.js` run their work that way).

## 4. Model

```
Claude Code session ──Stop hook──▶ agent module ──TurnEndEvent──▶ workbook module (daemon)
   │                                                                │ catch-up → per-conversation queue → job
   │ Purdex mod: POST /mod/v1/workbook/next ◀── job (input built + redacted, prompt_ver 2)
   │   runs $.model.complete (Haiku, effort low)  /  $.model.fork (refresh)
   └── POST /mod/v1/workbook/result ──────────────────────────────▶ validate + redact
                                                                    ▼
                                      workbook.db (entries, status, todos) ──▶ API / host-events ──▶ App (88)
                                                                           └──▶ push module (Stop push prefers `push`)
```

**The daemon keeps every decision** — what is summarised, the input, the order, the prompt, the validation, the storage, the push — and **the mod only runs the call the daemon hands it**, in the conversation's own live session. That keeps the input the measured one (M3), the redaction in one place (§8), and the prompt version with the validator.

### 4.1 Conversation key

A record belongs to a **conversation**, not a session id: a relay (self or member) continues the same workbook. The key is the **root session id** of the relay lineage: walk `session_lineage.predecessor_session_id` (team module, `relay_store.go:54`) back to a session with no predecessor. A plain `/clear` starts a new conversation (as spec 2026-10-06 line 721 already says: only relays write lineage). The team module exposes a read-only accessor `RootSessionOf(sessionID) (string, error)` (merged with WB-1a-i). The todo list (§5.5) belongs to the conversation too.

### 4.2 Trigger

The workbook module subscribes to the agent module's `TurnEndEvent` (`internal/module/agent/turn_end_hub.go:20`, `SubscribeTurnEnd`): one per accepted Claude Code main-turn `Stop`, at-least-once, tmux sessions only. Consumers must be idempotent.

*Clarified by the plan (2026-10-09, codex plan review):* a retried hook is published again with a **newer** stamp, and a full subscriber queue drops events, so `(session_id, At, Seq)` cannot be the key. A turn is keyed by its **transcript turn id** (`convmodel.Turn.ID`; when the transcript cannot be read, a hash of `Text` plus a 2-minute time bucket, so a retry dedupes and two same-text turns minutes apart do not), and each event is read as "this session has new ended turns": the module records the ended turns newer than the session's newest recorded one (the newest 3 at most, oldest first; a session with no record yet records only its newest). A dropped event is recovered by the next one. An accepted main-turn `StopFailure` also publishes a `TurnEndEvent` (`Failed: true`), so §7's StopFailure push has an entry to wait for; the team module ignores failed events.

The turn's input is read from the conversation module's normalised model (U1-6, `internal/convmodel`): the last turn's `user` item(s), `step` summaries and final `agent_text`. `TurnEndEvent.Text` (the hook's `last_assistant_message`) is the fallback for the assistant text.

*v2:* a turn becomes a job only for a session whose mod announced the workbook capability (§5.1); otherwise it is `skipped: no_mod` at once (nothing waits for a mod that is not there).

### 4.3 What is summarised, what is not

- A turn with no assistant text and no tool step is **not summarised** (no call, no entry). This removes the "a peer's report with no reply" turns where the measured prompts attributed the peer's work to the session.
- Subagent turns, headless (exec / Nexen) sessions, Codex sessions and **sessions without the Purdex mod** are out of scope (§12).

## 5. The summariser

### 5.1 Call — through the session's mod (v2; replaces v1's daemon `claude -p` runner)

**Routes on the mod socket** (the `/mod/v1/` Unix socket of interface U1 §6, which the mod already posts its events to):

- `POST /mod/v1/workbook/next` `{session_id, wait_ms}` (`wait_ms` 0–15 000) → 200 `{job}`, or 204 when no job is ready by the end of the wait. The daemon resolves `session_id` to its conversation (§4.1) and hands out that conversation's next job — after a relay, to the new session.
  - Turn and re-write jobs: `{id, kind: "turn" | "rewrite", complete: {model: "haiku", system: [{text, cache: true}], prompt, max_tokens: 4096, effort: "low", timeout_ms: 30000}}` — the mod passes `complete` to `$.model.complete` as it is (keys renamed to the API's).
  - Refresh jobs: `{id, kind: "refresh", fork: {prompt}}` → `$.model.fork` (§5.6).
- `POST /mod/v1/workbook/result` `{job_id, answered, text?, reason?, status?, error?, usage, latency_ms}` → 200 `{more}`; `more: true` means another job of this conversation is ready and the mod asks `next` again at once.

**When the mod asks:** (a) after every main-thread `turn.complete` whose reason is `answer` or `error` (never a subagent's), with `wait_ms` 15 000 — the daemon's job appears after the Stop hook and the catch-up; (b) when a `/mod/v1/events` answer carries `workbook: true` (a refresh or a left-over job of this conversation waits — a refresh needs no turn to end); (c) after a result answered `more`. Always from a `$.clock.after` timer, never inside a hook; one job at a time per mod; the session never waits for any of it.

**Capability:** the mod announces `workbook.v2` in its event batches (the field is the events wire's, plan); the daemon queues turn jobs only for a session whose mod announced it within the last 30 s, else `skipped: no_mod`.

**Lease:** a handed-out job is leased for `timeout_ms` + 10 s and never handed out twice; a lease that runs out (the mod reloaded, the session ended, the result was lost) → `failed: lost`. Not retried.

**The mod's outcome → the entry:**

| mod outcome | entry |
|---|---|
| answered | §5.4 |
| `api-error`, `error: authentication_failed` | `failed: auth` (one log line, as v1) |
| other `api-error` | `failed: api` (status and kind in the log line) |
| `empty-reply`, or text that is not JSON | one retry as a new job, then `failed: format` (as v1) |
| `aborted` with `latency_ms` ≥ `timeout_ms` | `failed: timeout` |
| other `aborted` | `failed: stopped` |
| the call rejected (model blocked, bad cap) | `failed: refused` |

**Order, backlog, cap:** per conversation strictly in order — a job is handed out only after the previous job's result is applied (the running state of turn n+1 needs turn n's output); more than 3 waiting → the newest 3 kept (older ones `skipped: backlog`); an hourly cap of 300 calls per host (`skipped: cap`, one log line per hour). v1's "at most 2 calls at once on the host" is dropped: no local process is spawned.

**Cost:** one Haiku call per summarised turn on the user's account, as in v1. The call's `usage` (input, output, cache read) is stored with the entry.

### 5.2 Input (the `prompt` of a turn job: JSON)

```
{ "previous_status": "<status of the last entry, or ''>",
  "recent_entries": [ {"thing": "...", "entry": "..."} ... up to 3, oldest first ],
  "open_todos": [ {"n": 1, "title": "...", "detail": "..."} ... the open todos, oldest first, at most 30 ],
  "dropped_titles": [],   -- always empty in v2 (the todos are read-only, §2 item 11); kept so the measured prompt is sent as measured
  "turn": { "user_prompt": "<≤ 1500 chars, cut with …>",
            "assistant_text": "<≤ 3000 chars, cut with …>",
            "tools": ["Bash: gh pr merge 2222", "Edit: cmd/pdx/relay.go", ... ≤ 20] } }
```

Built when the job is handed out (the previous job's output is already applied), not when the turn ends. `n` numbers the open todos for this job only; the daemon keeps the job's `n → todo id` map. Secrets are scrubbed **before** the call and again on the output (§8).

### 5.3 Output contract

```
{ "skip": bool, "thing": "≤ 16", "push": "≤ 40", "entry": "≤ 3 sentences × ≤ 35",
  "status": "≤ 200", "thing_done": bool,
  "todos": { "done": [n…], "dropped": [n…], "add": [ {"title": "≤ 30", "detail": "≤ 100"} … ≤ 2 ] } }
```

- `thing` — the name of the thing being worked on; the same thing keeps the name from `recent_entries`. It is the push title's subject and the grouping key for C.
- `push` — one line for the lock screen: the main result, or what a person must do.
- `entry` — what was done; the one key reason; the result or next step.
- `status` — rewritten whole each time: doing what, how far, stuck where (if stuck), next step. Answers A.
- `thing_done` — the thing ended in this turn (merged, deployed, abandoned). Answers C.
- `skip` — no new progress (greeting, acknowledgement, waiting).
- `todos` — v2: which open todos this turn finished (`done`) or made moot (`dropped`), and what it newly owes (`add`). A todo is what this conversation itself owes and nothing else tracks (the prompt's definition). Answers D.

The prompt is the measured round-7 prompt, run with `effort: low` (round 8), kept verbatim in `docs/specs/2026-10-10-session-workbook-prompt-v2.md` (prompt_ver 2) with the re-write prompt (unchanged) and the refresh prompt (§5.6). It ships in the daemon as versioned constants; the prompt version is stored with each entry. All seven fields are required (the merged validator's six become seven).

### 5.4 Validation and repair (code, not the model)

v1's rules, unchanged:

- Not JSON (after stripping one code fence) → one retry → `failed: format`.
- `push` > 40 → cut at the last punctuation ≤ 40; none → `push` is dropped (the push takes today's body).
- `thing` > 16 → cut at 16.
- `entry` > 150 → **one background re-write** (a `rewrite` job with the re-write prompt; measured average 113, 4/25 over 120); still > 150 → cut at the last sentence end ≤ 150. The push never waits for this step.
- `status` > 200 → cut at the last sentence end ≤ 200.
- *Clarified by the plan:* the call's `thing`, `push` and `status` are written (and the push released) as soon as they are validated, with the entry still `pending`; the re-write holds the conversation's place in its queue, and the entry becomes `ok` only after it. So the next turn's prompt never reads a provisional entry, and there is one `ok` per entry.

v2, the todos:

- `done` / `dropped` numbers are read through the job's map; an unknown number is ignored; a todo no longer open when the result is applied stays as it is; a number in both lists counts as `done`.
- `add`: the first 2 are kept; `title` > 30 → cut; `detail` > 100 → cut at the last sentence end ≤ 100 (none → cut at 100); an add whose trimmed title equals an open todo's title is ignored; with 30 open, adds are ignored (one log line).
- *Clarified (88, 2026-10-10, WB-1b′-a):* "all seven fields are required" means the seven top-level keys, and `todos` must be an object. Inside it a missing `done` / `dropped` / `add` is an empty list; one that is present with the wrong type (e.g. `done` a string, `add` not an array) is a format error like a non-object `todos` (one retry → `failed: format`). An `add` item whose `title` is missing or empty after trimming is ignored (one log line); a missing `detail` is `""`.
- The todo changes are applied in the transaction that writes the push line and the status (they never wait for the re-write).
- `skip: true` → the entry is `skipped: model`, and its `todos` are still applied (a turn with no progress may still answer an open question).

### 5.5 Todos — the list and the done record (v2)

- The list belongs to the conversation (`conv_key`), so it survives relays like the entries; a plain `/clear` starts a new, empty list.
- A todo is `open`, then `done` or `dropped` — by a turn or by a refresh; **nobody edits them by hand** (§2 item 11). A row keeps the entry that added it, the entry that closed it, and when.
- **The done record** is the conversation's `done` todos, newest first. Finished things (`thing_done`) stay in the entries, as in v1.
- A todo the model wrongly keeps open is cleared by a refresh (§5.6); ticking, deleting and adding by hand are out of scope (§12).

### 5.6 Refresh — `$.model.fork`, manual only (v2)

- **Asked by** the Mac App's 「重整」 control (§10) → `POST …/refresh` (§9) → the daemon queues a `refresh` job in the conversation's queue; the live session's mod collects it (≤ 10 s, through the events answer) and runs `$.model.fork({prompt})` with the refresh prompt (third block of the prompt document) filled with the current status, the numbered open todos and the dropped titles (empty in v2). Also typed in the session: **`/workbook refresh`** (the mod registers it; it asks the daemon to queue the same job and runs it). (§2 item 12.)
- Only for a conversation whose session is live and whose mod announced `workbook.v2`; one refresh at a time per conversation (a second → 409 `refresh_pending`); it takes its place in the queue like any job.
- *Clarified by the plan (rev 4, agreed with 1f):* a mod announces **`workbook.refresh`** besides `workbook.v2` once it can run a refresh job; the refresh needs a live session whose mod announced `workbook.refresh` (a mod with `workbook.v2` alone runs turn jobs only), so a daemon that serves refreshes never hands one to an older mod. The refresh runs in a capable session of the conversation (the asking one preferred), and its row records that session.
- **Output** `{status, todos: {done, dropped, add}}`; validated as §5.4 except: up to 10 adds; no push; no thing.
- **Written** as an entry of kind `refresh` (state `ok`, `thing` = the conversation's current thing, `entry` = a line the daemon writes: 「重整：完成 2、移除 1、新增 3」, no push), the status replaced and the todo changes applied, in one transaction. Failures as §5.1, plus `failed: nothing_to_fork` (the session has not answered since it started or since `/clear`).
- **Cost:** the main model over the whole transcript — read mostly from the main thread's prompt cache while it is warm, the whole prefix billed otherwise. The usage is stored with the refresh entry and shown with its result. Never automatic.

## 6. Storage — `workbook.db` (module `workbook`, file mode 0600, the devices/push pattern)

v1 schema (deployed; on mlab 2026-10-10: `wb_entries`, `wb_status`, `schema_version`, no rows):

```
wb_entries(
  id INTEGER PRIMARY KEY,
  conv_key TEXT NOT NULL,          -- root session id (§4.1)
  host_id TEXT NOT NULL, provider TEXT NOT NULL,   -- 'claude'
  session_id TEXT NOT NULL,        -- the session the turn ran in
  turn_id TEXT NOT NULL,           -- transcript turn id (§4.2); the idempotency key with session_id
  turn_at INTEGER NOT NULL, turn_seq INTEGER NOT NULL,   -- unix ms: the event's At for the newest turn, EndedAt for a caught-up one
  state TEXT NOT NULL,             -- pending | ok | failed | skipped
  reason TEXT NOT NULL DEFAULT '', -- failed: timeout|format|exit|auth|stopped ; skipped: no_text|backlog|cap|model|stopped
  thing TEXT, push TEXT, entry TEXT, thing_done INTEGER NOT NULL DEFAULT 0,
  push_ready_at INTEGER NOT NULL DEFAULT 0,   -- when thing/push were final (§5.4); the push hold waits on it
  team_id TEXT, role TEXT, ref TEXT,   -- who the session was at that moment; role lead|member|member_remote|none
  prompt_ver INTEGER NOT NULL, latency_ms INTEGER, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL,
  UNIQUE(session_id, turn_id))
wb_status(conv_key TEXT PRIMARY KEY, status TEXT NOT NULL, entry_id INTEGER NOT NULL, session_id TEXT NOT NULL, updated_at INTEGER NOT NULL)
```

v2 — **a migration** (the schema is deployed, `feedback_deployed_schema_needs_migration`):

```
wb_entries + kind TEXT NOT NULL DEFAULT 'turn'            -- turn | refresh
           + usage_in INTEGER, usage_out INTEGER, usage_cache_read INTEGER   -- tokens of the call(s)
reasons    + failed: api | lost | refused | nothing_to_fork ; skipped: no_mod   ('exit' stays for old rows)
wb_todos(
  id INTEGER PRIMARY KEY,
  conv_key TEXT NOT NULL,
  title TEXT NOT NULL, detail TEXT NOT NULL DEFAULT '',
  state TEXT NOT NULL,                  -- open | done | dropped
  added_entry_id INTEGER NOT NULL,      -- the turn or refresh entry that added it
  closed_entry_id INTEGER,              -- the turn or refresh entry that closed it (NULL while open)
  closed_by TEXT NOT NULL DEFAULT '',   -- model | refresh  ('user' is reserved for a later hand edit, §12)
  created_at INTEGER NOT NULL, closed_at INTEGER NOT NULL DEFAULT 0, updated_at INTEGER NOT NULL)
INDEX wb_todos_conv ON wb_todos(conv_key, state, id)
```

Every time is unix milliseconds.

- Raw prompts and assistant texts are **not** stored (the transcript already holds them); only the outputs.
- Retention: kept (≈ 0.5 KB per entry; a busy day ≈ 1,000 entries ≈ 0.5 MB; todos are smaller). No automatic deletion.

## 7. Push

- Applies to the push of a **Stop** (turn ended: done, or asking the user in text) and **StopFailure** (error). Permission requests and `Notification` keep today's text: they fire in the middle of a turn, before any workbook entry can exist, and their text is already specific ("需要權限：Bash").
- When the push gate (`internal/module/push/gate.go`) has decided to push a Stop, the push module **holds it up to N = 8 s** (a timer, never a sleep in `onNotify`) for that turn's entry. Ready with a `push` → title `{session name}・{thing}` (cut to the title limit), body `push`, and the payload gains `purdex.workbook = {conv_key, entry_id}` so iOS can open that entry (§10.3). Not ready, failed, skipped or no `push` → today's title and body, unchanged.
- N is a host setting (`workbook.push_wait_s`, 0 = never wait). *v2:* the measured basis is §3 (`effort: low`, API p50 2.6 s, no CLI start-up on the mod path); M4 confirms it end to end.
- A present Mac still suppresses the push (gate rule 4); the entry is written either way.
- *Clarified by the plan:* the hold waits for the entry's push line (§5.4), not for `ok`; holds are a capped (256), cancellable set — at the cap the push goes at once, and stopping the module cancels every hold without sending. The frame's `BroadcastTs` is nanoseconds and `turn_at` milliseconds; the match converts.

## 8. Secrets and personal data (daemon's job)

- Scrub before the call and on the output: `Bearer …`, `sk-…`, `ghp_/gho_…`, `xox…`, `pdxp_…`, `pdxd_…`, AWS key ids, and long mixed letter-digit strings (≥ 32) → `[redacted]`.
- *v2:* the daemon builds and scrubs the whole request (§5.1); the mod sends exactly what it was handed and never reads the transcript for the workbook. The refresh is the exception by nature — the fork sends the conversation itself, as the main thread already does — and its output is scrubbed like any other.
- Images and attachments are never sent (only text).
- The App does not filter again; it shows what the daemon stored.

## 9. API and events (capabilities `workbook.v1`, `workbook.v2`)

v1 (`workbook.v1`):

- `GET /api/workbook/conversations/{provider}/{session_id}?limit=&before=` → `{conv_key, status, status_at, entries:[…newest first]}`; `session_id` may be any session of the conversation (resolved to `conv_key`).
- `GET /api/workbook/entries?since=&until=&thing_done=1&limit=` → entries across conversations of this host, newest first (C).
- Host event `workbook.entry` `{conv_key, session_id, entry}` on insert and on every state change; host event `workbook.status` `{conv_key, session_id, status, updated_at}`.
- Errors: 404 `not_found` (no workbook for that session), 400 `bad_request`.
- *Clarified by the plan:* every time is unix ms (`since` / `until` filter `turn_at`); `before=<entry id>` returns entries with `id < before`, newest first; `skipped` entries are returned (the Apps hide them) and `failed` ones carry `reason`; event values are JSON strings; one `ok` event per entry.

v2 (`workbook.v2` — the todos and the refresh):

- Every entry gains `kind` (`turn` | `refresh`), `usage` and `todo_changes: {added: [{id, title}], done: [{id, title}], dropped: [{id, title}]}` — what that entry did to the list (the mixed view, §10).
- The conversation answer gains `todos: {open: [… oldest first], done: [… newest 20]}`. A todo: `{id, title, detail, state, closed_by, created_at, closed_at, added_entry_id, closed_entry_id}`.
- `GET /api/workbook/conversations/{provider}/{session_id}/todos?state=open|done|dropped&limit=&before=` — the list or the done record, paged like the entries (`before=<todo id>`).
- `POST /api/workbook/conversations/{provider}/{session_id}/refresh` → 202 `{entry_id}` (the refresh entry, `pending`); 409 `not_live` (no live session of the conversation whose mod announced `workbook.v2`; *clarified:* `workbook.refresh`, §5.6); 409 `refresh_pending`.
- *Clarified by the plan:* the conversation answer carries `refresh_available` (bool, now), and host event `workbook.refresh_available {conv_key, available}` is sent when it changes — the Mac's 「重整」 control is enabled by it (§10.1).
- No route edits a todo (§2 item 11).
- Host event `workbook.todos` `{conv_key, session_id, todos: [the changed todos]}` on every change (a turn or a refresh). A refresh entry travels as `workbook.entry`.
- Device tokens (iOS): the GET routes (iOS reads everything, §10.3); the refresh is the Mac App's.

## 10. Where it is seen (user decision 2026-10-09, extended 2026-10-10) and what 88 gets

**Three surfaces, no more.** The user chose: the panel beside a conversation, the team panel line, and iOS. **No standalone workbook page on the Mac** (iOS's session detail page, §10.3, opens from a session's conversation screen): reading the workbook of a session that has ended, and the period view for C ("what got done this week"), have no screen yet; the records are kept (§6) and the API serves them (§9), so a later page needs no daemon change.

### 10.1 Mac App — one shared panel: team panel and workbook (user 2026-10-09)

The workbook and the team panel **share one panel area**, and that area can be **enlarged and shrunk** (resize, plus an expanded mode that takes most of the window). Two ways in, one place:

- **From the team panel:** each member row (and the lead's own row) shows 「正在做的任務」 — the **first sentence of the conversation's latest `status`**, one line with an ellipsis, hover for the whole `status`; no workbook yet → no line. **Clicking a row drills into that member's workbook** in the same panel area, with a back control to the team list. A member that has ended (killed, gone, released) still opens its workbook: the records are kept.
- **From a conversation:** a 「工作簿」 toggle (Phosphor `Notebook`) in the toolbar of a Claude Code session's tab (terminal or conversation view) opens the same panel area on that conversation's workbook. Hidden for other agents and for a session with no workbook yet.

The workbook view (either way in):
- Top: 「目前狀況」 — the latest `status`, with its time.
- *v2:* below it, 「待辦」 — the open todos (title; the detail on hover or click), **read-only** (§2 item 11); the count in the section header; hidden when there are none.
- Below: entries **grouped by `thing`**, the most recent thing first. Each group: the thing's name, 進行中 / 完成 (from `thing_done`), and its entries newest first (time + `entry`). Finished things are collapsed; a click expands them.
- *v2, mixed view (the default):* under an entry's text, one short line per todo change it made — 「＋ title」 added, 「✓ title」 done, 「－ title」 dropped. A refresh entry reads 「重整：完成 2、移除 1、新增 3」 with its lines.
- *v2, on their own:* a 「紀錄｜待辦」 switch in the view's header. 「待辦」 shows the open todos and then 「已完成」 — the done record, newest first (time, title, and the thing of the entry that closed it, or 「重整」); clicking a done item shows its entry in 「紀錄」.
- *v2, refresh:* a 「重整」 control in the view's header (Phosphor `ArrowsClockwise`), enabled when the conversation has a live session with a workbook-capable mod; 「重整中…」 while it runs; its tooltip says it reads the whole conversation with the main model; the result arrives as a refresh entry.
- A relayed conversation shows one continuous workbook (one `conv_key`, §4.1).
- **Live:** inserts and state changes arrive through the host events of §9 (`workbook.status` also refreshes the team panel line); no polling.
- **States:** `pending` 「整理中…」, `failed` 「整理失敗」 with the reason on hover, `skipped` not shown.
- **Tab-hosted rule (CLAUDE.md checklist):** the panel's open state, size, which view it shows (team list / which workbook, *v2:* 紀錄 or 待辦) and scroll position live outside the component and survive a tab switch; a regression test switches away and back with the real `TabContent`.

### 10.2 (merged into 10.1)

### 10.3 iOS — the session detail page, and opening a push

Before v2, iOS had no session-detail screen: a conversation screen's ⓘ opened the 「Tab 資訊」 sheet (one section per session: host, state, agent, path, 「在手機隱藏」, 「我的最愛」), and v1's workbook section lived there (purdex-ios 0.6.30, behind `workbook.v1`). v2 (user 2026-10-10; **the iOS interface design is assigned by 88**, the user's points below are fixed):

- **ⓘ opens the session detail page** — a full-screen page, pushed — instead of the sheet; **a swipe to the left on the conversation screen opens it too**.
- *Amended (user 2026-10-10, iOS 0.6.35):* the workbook and the Tab info have **two different icons**. On the conversation screen the button that opens the detail page is a **workbook icon** (SF Symbol `book.closed`, the Mac's `Notebook`; accessibility label 「工作簿」) when the tab's primary session has `workbook.v1` and has not ended, else ⓘ (then the page is the Tab info alone); on the detail page, the button that opens the Tab info sheet stays ⓘ. The detail page's **title is the tab's title** — the same as the conversation screen's — not the session's name; a tab with several sessions keeps the ▾ session switch beside it, its items named by session.
- **The page's segments:** 「目前狀況」 (`status` and its time); 「待辦」 (the open todos, title and detail, read-only); 「已完成」 (the done record, newest first, paged); 「紀錄」 (every entry, grouped as on the Mac, `before=` paging, refresh entries included); and **the old Tab-info content as a segment or a button of the page** (the user allowed either; 88's iOS design decides). Without `workbook.v1` / `workbook.v2` the workbook segments are absent and the page shows the Tab-info content alone.
- A tab that holds more than one session: the page opens on the session the screen shows, with a switch between the tab's sessions (88's iOS design).
- **Tapping a workbook push** opens the detail page on 「紀錄」, scrolled to the entry, highlighted. The push carries `purdex.workbook = {conv_key, entry_id}` (§7). A push without it (today's body, the fallback) opens the session as today.
- No refresh on iOS (§9 device routes are reads).

### 10.4 Contract items for 88

1. **Data contract:** §6 fields as returned by §9; the entry states and reasons; `prompt_ver`; *v2:* the todo fields, `kind`, `todo_changes`.
2. Filtering is the daemon's job (§8); the App shows what the daemon stored.
3. Capability `workbook.v1` gates the v1 surfaces; *v2:* `workbook.v2` gates the todos, the switch and the refresh. An older daemon shows none of them. *Clarified:* the host capability `workbook.v2` shows the 「重整」 control; whether it is enabled for a conversation is `refresh_available` (§9) — that is where the mod-level `workbook.refresh` (§5.6) reaches the App.
4. *v2:* the events answer flag `workbook: true` (§5.1) is a field of the U1 events wire (88's).

## 11. Employee workbook (later round)

Each entry carries `team_id / role / ref` at the moment of the turn; the employee workbook (member name pool = virtual employees, "what did this name do this month") is built on these records. Not in this spec.

## 12. Out of scope

Subagent turns; headless exec / Nexen sessions; Codex sessions; *v2:* sessions without the Purdex mod (`--safe-mode`, a mod that failed to load); cross-host workbooks (each host keeps its own sessions' records); editing an entry by hand; *v2:* editing todos by hand — tick, delete, add (§2 item 11); showing the todos inside the terminal (a band above the prompt, as claude-todo-list does, §2 item 13); a refresh from iOS; an automatic refresh; retention / export.

## 13. Measurements

v1 (1f 2026-10-09):

- **M1 / M2** (the daemon's `claude` on its own `PATH`, keychain auth in a minimal environment; ≈ 288 MB per call): history — v2 spawns no process.
- **M3 — input source.** The bake-off's 30 turns were built from the conversation API (`GET /api/conversations/claude/<sid>`, the U1-6 normalised model), so the measured quality is the quality of the production input. Still true in v2: the daemon builds the input (§4).

v2 (in the plan, before the mod path merges):

- **M4 — the mod path end to end** on real sessions: Stop → push line within 8 s for at least 70% of summarised turns. Below that, the turn job is split in two calls — A (`thing`, `push`, `entry`, `status`), which the push waits for, then B (`todos`), which it does not — and the prompt document gets the two prompts.
- **M5 — the refresh prompt** on two real conversations: parseable JSON, `status` ≤ 200, reasonable todo changes; the usage (cache read against uncached input) recorded.
- **M6 — prompt cache:** whether a second turn job within 5 minutes reads the marked system block from the cache (`usage.cache_read_input_tokens` > 0). Below the model's minimum nothing is cached and the mark is harmless.

## 14. Phases (each ≤ 800 lines / 20 files; the plan splits further where needed)

| PR | Scope |
|---|---|
| WB-1a, WB-1b-1, WB-1b-2 | merged: team accessors, store, setting, prompts v1, validation (v1). |
| WB-1b′ | daemon: subscriber + catch-up + queue (plan WB-1b steps 2–4) and the **mod job routes** (§5.1: next / result, lease, capability, outcome mapping) in place of the `claude -p` runner; prompt_ver 2 constants from the v2 prompt document; the input with todos (§5.2); seven-field validation incl. todos (§5.3, §5.4); the migration (§6). |
| WB-1c | mod: the job executor (`$.model.complete`, timers, one job at a time, the capability announce, the events-answer flag), and `/workbook refresh`; deployed with `pdx setup`. |
| WB-2 | API + events + capability `workbook.v1` (in review). |
| WB-2b | todos and refresh: §9's v2 routes and events, capability `workbook.v2`; the refresh job and `$.model.fork` in the executor (§5.6). |
| WB-3 | push hold and body (§7), unchanged. |
| App | 88's plan, from §10 incl. the v2 items. |
