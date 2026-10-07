# Permission channel — a worker asks, the user decides — spec

Status: **user decisions PC1–PC4 approved 2026-10-07**; the rest is the coordinator's derivation (`mlab/purdex-18`). Plan(s) go to codex together with this spec. Nexen's part is designed and built in the Nexen repo, against §4 below; Purdex's part follows §5.

Facts come from nexen-47's probe report, *Nexen P4 permission channel — facts* (Claude Code 2.1.291, 47 runs). It is cited as **[F §x]**, and its experiment IDs (E2, H6, S2, …) are quoted as they appear there. Source: `/private/tmp/claude-501/-Users-wake-Workspace-wake-nexen/22acb797-fd0b-4df7-a2a6-96f506441ffe/scratchpad/p4-permission-channel-facts.md`. The Nexen implementer copies it into the Nexen repo next to its own spec, so the evidence survives the scratchpad.

## 1. Goal

Today a handed-off conversation runs fully trusted: the `handoff` profile is `bypassPermissions`, so a worker never asks. This adds a second mode in which a worker stops on an action that needs approval, shows the request in Purdex, and continues once the user allows or denies it.

## 2. User decisions (2026-10-07, do not reopen)

| # | Decision |
|---|---|
| PC1 | **Build it.** A handoff offers a second mode, **「需要核准」**. **「完全放行」 stays the default.** |
| PC2 | The user answers **in the worker pane**, with 同意 / 拒絕. The **tab label and the activity-bar list show 「等待核准」**. ~~A phone works by opening Purdex in its browser.~~ **Amended 2026-10-07 (user):** Purdex ships only as the **Mac App** and an **iOS App**; the standalone web version is withdrawn. So v1 answers only in the Mac App, and the phone answers once the iOS App exists, using the same Nexen API. ~~**No push notifications.**~~ **Amended 2026-10-07 (user):** 等待核准與 agent ask 同層級，走同一套桌面通知規則（含相同的前景／設定例外）。 |
| PC3 | **By default an unanswered request waits forever**, and the UI keeps showing 「等待核准中」. An **optional setting** denies a request automatically after N minutes. |
| PC4 | **No "always allow this" in v1.** |

## 3. Facts this design rests on

- **The mechanism is the stdio control protocol** [F §1.1]. With the hidden flag `--permission-prompt-tool stdio`, the CLI writes `control_request{subtype:"can_use_tool", request_id, tool_name, input, tool_use_id, description?, decision_reason?, decision_reason_type?, permission_suggestions?, agent_id?}` to stdout. The host answers on the same stdin with `control_response{subtype:"success", request_id, response:{behavior:"allow", updatedInput} | {behavior:"deny", message, interrupt}}`.
  - The CLI never times out: 25 minutes were measured [F §6]. The process sleeps at about 0 % CPU and 127–260 MB RSS, and writes nothing while it waits.
  - An interrupt while a request is pending makes the CLI send `control_cancel_request{request_id}` first, then end the turn (`aborted_tools`) [F §1.1.4, E4_interrupt].
  - A host deny gives `non_execution_kind: "permission-rule"`, which Nexen ≥ v0.17.1 reports as `denied` (#122).
  - Requests come **one at a time**, even for parallel tool calls in one message (E5). A foreground subagent's request carries `agent_id` (S1).
- **Rejected mechanisms:**
  - **The MCP prompt tool** leaves nothing on the stream while a request waits [F §1.2].
  - **The PermissionRequest hook** has a 600 s default timeout and no `tool_use_id`, and it would have to be injected through `--settings` [F §1.3].
  - **PreToolUse `defer`** exits the process. Resuming then waits for the deferred tool before `system/init`, which breaks Nexen's 30 s Init timeout, and it leaves a dangling `tool_use` for a terminal resume [F §1.5, §3.5].
- **Which permission mode asks.** `acceptEdits` asks for Bash and for writes outside the cwd, and auto-accepts edits inside it (E2, B2). `bypassPermissions` never asks (B1); `dontAsk` never asks (D1).
- **The user's own PermissionRequest hook.** `~/.claude/settings.json` has `pdx hook --agent cc PdxPermissionRequest`. `pdx hook` writes nothing to stdout, so it makes no decision, and a request with no hook decision falls through to the stdio host (H6). A handoff inherits user settings, so this hook runs in the worker too.
- **Nexen today** [F §2]:
  - it closes stdin at the first `result`;
  - it stores `control_request` / `control_cancel_request` as plain frames and answers neither;
  - it has no silence watchdog, and `turn_timeout` defaults to 0;
  - the lease does not interrupt turns;
  - `reconcile` SIGKILLs a live pid at startup.
- **Background subagents** that ask after the first `result` find stdin closed. Their request fails with no signal: no `tool_result_meta`, and nothing in `permission_denials` (S2, nexen #123).
- **A daemon crash while a request is pending** denies the request, and the agent finishes the turn on its own (C1). A SIGKILL leaves a dangling `tool_use`, which the next resume fills with "outcome unknown"; the request is never asked again (E9).
- **Q1 race** [F §1.7, §3.6]: a terminal resume while a request is pending forks the transcript. An approval that arrives afterwards still runs the tool, and the resumed conversation never learns of it.

## 4. Nexen (next minor, e.g. v0.19.0)

Names below are placeholders; the Nexen implementer proposes the final ones, and the coordinator approves them with the contract diff.

> **Final names (Nexen spec, nexen-47, approved by the coordinator 2026-10-07).** The Nexen spec is `docs/specs/2026-10-07-permission-channel-spec.md` in the Nexen repo; it governs where this section is less precise.
> - **Profile:** `handoff_ask`, rank 3; `handoff` moves to rank 4. Profiles are stored by name, so the rank change does not touch existing rows.
> - **Events:** `permission.requested` and `permission.resolved`.
>   - `outcome` is one of `allowed`, `denied`, `cancelled`, `expired`.
>   - A `cancelled` carries a `reason`: `interrupt` (with `interrupt_source`), `cli_cancelled`, `turn_ended`, or `daemon_restart`.
>   - A `resolved` event can arrive after the tool's `tool_use` / `tool_result` events. Correlate by `request_id`, never by `seq`.
> - **Answer:** `POST /v1/executions/{id}/permissions/{request_id}` with `{decision: "allow"|"deny", message?, lease_id}`.
>   - Errors, in check order: `malformed_body` → execution 404 → `invalid_permission_answer` 400 → lease 409 → `permission_not_found` 404 → `permission_not_pending` 409.
> - **Delegate:** `permission_timeout_s`, 0 to 86400.
>   - With a profile that has no channel, it answers 400 `invalid_permission_timeout`.
>   - A `handoff_ask` request that clamps to a different profile is **rejected**, never silently downgraded.
> - **Summary field:** `pending_permission: {request_id, tool_name, since} | null`.
>   - `since` is **Unix milliseconds**, like `activity.tool.since` (not RFC 3339).
>   - `null` is an answer (nothing pending); an absent field means a daemon older than v0.19.0.
> - **Capability:** `capabilities.permissions: {profiles: ["handoff_ask"], answer: {method, path}, timeout: {max_s}}`.
>   - The `timeout` object appears one PR later than the rest. **Purdex sends `permission_timeout_s` only when `capabilities.permissions.timeout` exists.**
> - **N6 is feasible** (measured): stdin stays open past a `result` while a background task is open, for `handoff_ask` only.

**N1 — the asking profile.** Add a sandbox profile, for example `handoff_ask`. It is the `handoff` profile in every respect (all tools, user settings inherited) except two:
- `--permission-mode acceptEdits` instead of `bypassPermissions`;
- the extra arguments `--permission-prompt-tool stdio`.

Its rank sits **below `handoff`**, so a host whose `max_profile` is `handoff` allows it. It is listed in `capabilities.sandbox_profiles` like any other profile.

**N2 — requests are events.** A `can_use_tool` frame becomes a durable event, for example `permission.requested`. The payload carries:
- `request_id`;
- `turn_id`;
- `tool_use_id`;
- `agent_id`, when there is one;
- `tool_name`, and `display_name` when present;
- `description`;
- `decision_reason` and `decision_reason_type`;
- `blocked_path`, when present;
- `input`, bounded the way N2 bounds `tool_use.input`.

`permission_suggestions` is dropped, because "always allow" is out of v1 (PC4).

Each request ends in exactly one terminal event, for example `permission.resolved`. Its payload carries:
- `request_id`;
- `outcome` — `allowed`, `denied`, `cancelled` or `expired`;
- `principal_id`, for an answer;
- `reason`:
  - for `cancelled`: `interrupt`, `cli_cancelled`, `turn_ended`, or `daemon_restart`;
  - for `expired`: the timeout.

A `control_cancel_request` from the CLI ends the request as `cancelled`.

**N3 — answering.** An endpoint, for example `POST /v1/executions/{id}/permissions/{request_id}`, takes `{"behavior":"allow"|"deny", "message"?: string, "lease_id": …}`.
- It **requires the control lease**, the same rule as `send` and `interrupt`. This is also what narrows the Q1 race (§5.4 states what it guarantees and what remains).
- `allow` writes `control_response{behavior:"allow", updatedInput:<the request's input>}`.
- `deny` writes `{behavior:"deny", message: <message, or a default such as "The user denied this action.">, interrupt:false}`. Stopping the whole turn stays the job of the existing interrupt.
- The answer is written to **that turn's stdin**. The request must still be pending; otherwise the endpoint returns 409 with a new code, for example `permission_not_pending`. Once a request has ended (answered, cancelled or expired), every later answer gets that 409. The check and the write are atomic per request: two concurrent answers produce exactly one write.

**N4 — "waiting" is readable without replaying events.** `GET /v1/executions/{id}` and the list rows carry a pending indicator, for example `pending_permission: {request_id, tool_name, since} | null`. Purdex's tab label, activity bar and pane header read it. The SSE stream carries N2's events live.

**N5 — optional expiry (PC3).** The delegate request takes an optional `permission_timeout_s`, where 0 or absent means wait forever (the default).
- When it is set and a request stays pending that long, Nexen denies it with a message stating the timeout, and ends the request as `expired`.
- The value is stored on the execution, so a resume (each `send`) keeps it.

**N6 — stdin stays open while an answer is still possible.** A `result` no longer closes stdin while a background task of the same process is still open (Nexen tracks `task_start` / `task_end`). Stdin closes when a `result` has been seen **and** no task is open.
- This lets background subagents ask instead of failing silently (S2).
- If this proves unsafe — for example the CLI keeps the process alive waiting for input after the last task ends — the Nexen implementer reports back before shipping. The fallback is to keep today's behaviour, document S2 as a known limit of the asking profile, and have Purdex say so in the pane.

**N7 — lifecycle edges.**
- **Interrupt or terminate while a request is pending:** the CLI cancels the request, and N2 records `cancelled` / `interrupt`.
- **Turn ends with a request still pending** (should not happen, but be defensive): the request ends as `cancelled` / `turn_ended`.
- **Startup reconcile:** any request with no terminal event ends as `cancelled` / `daemon_restart`. Its turn is orphaned as today.

**N8 — feature detection.** A capability object, for example `capabilities.permissions: {channel: true, timeout: true}`. A consumer offers the asking mode only when it is present.

**N9 — contract.** The capability matrix and the consumer guide document N1–N8. The contract also carries these known limits:
- an answer that arrives after a crash is lost, and the CLI already treated the request as denied (C1);
- the transcript-fork hazard when a client answers without holding the lease does not exist, because the lease is required;
- waiting past the prompt-cache TTL roughly doubles the next call's cost.

## 5. Purdex

**5.1 Pin and capability.** Pin the Nexen version that ships §4. The daemon includes the asking profile in its handoff profile check only when the capability is present. The SPA offers the mode only when the host reports it. The timeout setting (§5.5) is offered, and `permission_timeout_s` sent, only when `capabilities.permissions.timeout` exists. A handoff answered `rejected` because the host's `max_profile` does not allow `handoff_ask` shows the reject reason; it is never retried as 完全放行.

**5.2 Choosing the mode (PC1).** The handoff confirm dialog gets a two-way choice: **完全放行（預設）** and **需要核准**.
- The daemon's `POST …/handoff` already takes `profile`. The asking choice sends the asking profile, plus `permission_timeout_s` from §5.5.
- **Rebuilds keep the mode:** a worker rebuild resends the original `effective_profile`, which already happens today.
- Nothing else picks a profile in v1.

**5.3 Answering in the pane (PC2).** While N4's indicator is set, the worker pane shows a request card above the composer. The card shows:
- the tool name and description;
- the command or input, as a bounded preview with an expand control;
- `decision_reason`;
- which agent asks, when `agent_id` is set (for example 「subagent: <description>」);
- how long it has waited.

Its actions:
- **同意**;
- **拒絕**, with an optional one-line note. The note becomes the deny `message`; an empty note sends the default.

How it behaves:
- The card answers through the existing control lease of the pane, the same path `send` uses.
- After an answer, or on any N2 terminal event, the card goes away.
- A 409 `permission_not_pending` closes the card quietly, because the request has already ended.

The card targets the Mac App window. No phone-browser layout is required (PC2 as amended).

**5.4 Showing 「等待核准」 (PC2), and the Q1 race.**
- **Where it shows:**
  - the tab label of a worker tab with a pending request reads 「等待核准」;
  - the activity-bar worker list and the New Tab Workers list show the same state, with the pane header's status dot.
  - The dot colour reuses the warning token, with a distinct icon so it is not read as "queued".
- **Desktop notification** (PC2 as amended 2026-10-07): a new pending request is an event of the same level as an agent ask (a terminal agent waiting for the user, such as Claude Code's permission prompt), so it goes through the same desktop-notification rules:
  - it is the same event and setting as a terminal agent's ask: `PermissionRequest` under the worker's agent type (`cc` for a Claude worker);
  - there is no notification while the App is in the foreground **and** that tab is the current tab, nor when the user turned the event off; otherwise it notifies;
  - the title is the worker title, as on the tab, and the body names the tool;
  - each request notifies once, told apart by its `request_id` (not its `since`: two requests can share a millisecond, and both notify). A refetch, a reconnect, a list-row / live-summary switch or a tab switch does not notify again, and a second request while the worker is still waiting notifies once more;
  - across an App reload it follows the same baseline as an agent ask: a worker never seen on this client only establishes a baseline on its first snapshot (no notification); for a known worker, a request not seen yet — including one that arrived while the App was closed — notifies once; a request already seen never notifies again.
  - **Known residuals of the single source** (documented, not tested away). A worker's status — its light, 「等待核准」 and every notification above — comes only from its row in the host's execution list, like a terminal agent's comes from the daemon. The list refetches on a 500 ms trailing debounce of the host's site stream, so a change that starts and ends inside one debounce window is never seen as a row. Each of these is to be narrowed by [#1866](https://github.com/wake/purdex/issues/1866) (state deltas on the site stream):
    1. A terminate and an archive inside one window: the row leaves the list without ever reading `terminated`, so the light is only cleared. There is no `WorkerTerminated` notification.
    2. Nexen settles a worker idle (`SettleIdle`) between two queued turns. A refetch that lands in that gap reads `idle`, and can surface one extra Stop (Nexen [#162](https://lab.protype.tw/wake/nexen/issues/162)).
    3. A full turn completed inside one window (running → idle → running between two refreshes) yields no Stop. The row reads `running` both times, only with a higher `turn_count`; the count is recorded, but `running` is not dispatched again, because that would clear an unread the user has not seen.
    4. A permission request asked and answered elsewhere inside one window is never shown or notified: by the next refresh it is already decided.
- **Q1 race — what the ending paths guarantee** (coordinator ruling R-PC-1, 2026-10-07):
  - Answers need the control lease (N3), so the pane's lease now gates answers as well as sends.
  - Every path that ends a worker while a request could be pending **preempts** the pane's lease before it interrupts or terminates (conversation entity D22): it releases a pdx holder's lease as the holder, then acquires an exclusive one under the daemon's own principal. The paths:
    - Q1;
    - exit;
    - take-to-terminal and take-back;
    - worker-rebuild with a replaced row.

    Until R-PC-1 only take-to-terminal and take-back preempted. Exit, Q1 and worker-rebuild borrowed the holder's lease, so the original pane could keep writing until the terminate landed. A non-pdx holder still answers `held_by`, and nothing changes (D4). Exit still never fails on contention (D4): when the preempt keeps losing the lease for a bounded number of passes, it falls back to borrowing, as before, and the pane keeps its lease until the terminate lands.
  - **Guarantees** (each pinned by a test):
    1. After a preempt, an answer that carries the previous holder's `lease_id` gets 409 `lease_mismatch`.
    2. After an interrupt or terminate took effect, any answer gets 409 `permission_not_pending`.
    3. An ending path never turns a `denied`, `cancelled` or `expired` request into `allowed`.
  - **Known residuals** (documented, not tested away):
    1. Nexen re-checks the lease right before it writes an answer, but it does not fence the write. An answer whose check passed just before the preempt can still be written.
    2. Q1 detection latency. An allow pressed after the user's terminal resume, and before Purdex detects that resume and preempts, still reaches the worker. The transcript-fork risk that follows is the consumer's (Nexen capability matrix #84).

**5.5 The optional timeout (PC3).** Add a setting under Settings → Worker:
- label 「等待核准逾時」;
- values 不逾時 (default), 5, 15, 30 or 60 分鐘.

It is a per-client SPA setting, sent as `permission_timeout_s` at handoff. A change applies to new handoffs only, and the setting says so.

When a request expires, the pane shows a muted line:「已逾時自動拒絕（N 分鐘）」.

**5.6 What is not in v1.**
- "Always allow" (PC4).
- ~~Push notifications (PC2).~~ Amended 2026-10-07 (user): a pending request notifies like an agent ask (§5.4).
- The asking mode for New Tab or other non-handoff starts.
- Showing or editing `updatedInput`; an allow always sends the request's own input.
- Answering from a phone. That comes with the iOS App, which consumes the same Nexen endpoints (§4 N3) and capability (N8).

## 6. Tests (both repos, each in its own plan)

**Nexen.** With a fixture provider emitting `can_use_tool`:
- event shape;
- allow and deny writes on stdin;
- the 409 on a second answer, with concurrent answers producing exactly one write;
- the lease checks;
- expiry;
- interrupt → `cancelled`;
- reconcile → `cancelled`;
- the N6 stdin lifecycle, with a background task asking after `result`;
- the asking profile's argv and rank, and the capability.

Plus one gated real-CLI test, mirroring E2.

**Purdex.**
- the dialog choice and the profile sent;
- the card from N4 and N2 events;
- answer success and 409 handling;
- the tab-label and list state;
- the timeout setting reaching the handoff body;
- the Q1 / exit race tests of §5.4;

**Acceptance on mlab.** Hand off a throwaway session in 需要核准 mode, then:
- ask it to run a Bash command that is **not read-only**, for example `python3 -c "print(1)"` or one that writes a file outside the cwd. `acceptEdits` (the asking profile's mode) lets Claude Code auto-allow read-only commands such as `echo` and `date`, so they never ask;
- approve it in the Mac App;
- deny a second one with a note, and confirm the model sees the note;
- set a 5-minute timeout on a third, and confirm it expires.

Haiku, a throwaway tmux, cleaned up afterwards.

## 7. Phases

| PR | Repo | Content |
|---|---|---|
| N-a | Nexen | N1 profile + N2 events + N3 answer + N4 indicator + N8 capability |
| N-b | Nexen | N5 expiry + N6 stdin lifecycle + N7 edges + N9 contract |
| P-1 | Purdex | Pin, the daemon's profile check, the handoff body (profile + timeout); the Q1 / exit race tests |
| P-2 | Purdex | Dialog choice, request card, 「等待核准」 states, timeout setting |

Each PR is ≤ 800 lines / 20 files. N-a and N-b ship as one Nexen minor. Purdex P-1 is a daemon deploy (coordinator).
