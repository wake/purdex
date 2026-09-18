# Spec — take any execution to a terminal (#1210)

- Status: v1.4 (2026-09-19) — codex attacker on PR #1212 (5 findings): execution lock shared by both take-back paths, archive **before** resume, `KillSessionIfInstance` (by id, under generation) everywhere, `CreateSession` generation check, host-home-based slug; v1.3: codex R1 on PR #1212: `execution_archived`, no kill-by-name on tmux restart; v1.2: codex plan+spec review `task-mu75q9tz-hn36pf` applied (10 findings: helper boundary, lease cleanup contract, name preflight before settle, post-create partial failure, kill-on-failure instead of double writer, project slug, visibility by state); v1.1 adds G4 (ask whether to keep the tmux session on every hand-off, user request 2026-09-19); plan reviewed together with this spec
- Predecessor: P-C.3 (`2026-09-18-pc-launch-ui-spec.md` §4.4 "Take back to
  terminal"), which only covers an execution that a `Hand to nex` created.
- Nexen contract: v0.11.2 `docs/contract/capability-matrix.md` §1.8 (resume
  rules: same cwd, same transcript storage, session gone first, lowercase id).

## 1. Problem

"Take back to terminal" appears only on an execution that came from a tmux
session (`from` on the pane, `handoffOrigin` on the row). An execution
launched from NewTab Headless, `pdx nex delegate` or a future Aigora hook
has no origin session, so the header offers Interrupt / Terminate only and
the conversation cannot be continued interactively. The user's ruling
(2026-09-19): every execution should be takeable into a terminal.

## 2. Measured facts (worktree at alpha.402, nexen v0.11.2)

- The claude subprocess inherits `HOME` (`account/env.go` `BaseEnvKeys`);
  Nexen injects `CLAUDE_CODE_OAUTH_TOKEN`, `CLAUDE_SECURESTORAGE_CONFIG_DIR`
  and the unconditional `CLAUDE_CODE_FORWARD_SUBAGENT_TEXT=1`, and blocks
  `CLAUDE_CODE_CHILD_SESSION`,
  so the transcript is written to `~/.claude/projects/<slug(cwd)>/<sid>.jsonl`
  exactly as an interactive session's. `claude --resume <sid>` from a shell
  in the same cwd finds it. (Today's P-D acceptance proved the reverse
  direction three times; the transcript is the same file.)
- `store.Execution` carries `Cwd`, `Provider`, `SessionID` (set after turn
  1), `ResumeSessionID`, `Origin`, `Labels`; the SPA `ExecutionSummary`
  exposes `cwd`, `provider`, `session_id?`, `resume_session_id?`, `state`.
- `internal/module/nex/takeback.go` `handleNexTakeback` already does:
  lease-if-running → interrupt → settled check → session id →
  `cc_already_running` guard → send resume keys → `waitForCC` → archive.
  It is bound to an existing session by `boundToSession` (label + origin)
  and the session lock `m.locks` keyed by session code.
- `session.SessionProvider` (what the nex module holds) has no create;
  `session/handler.go:70 handleCreate` does `HasSession → NewSession(name,
  cwd) → ListSessions → SetMeta` under its own critical section and
  validates the name with `SESSION_NAME_REGEX` (handler.go:13).
- The SPA names launched sessions `{slug}-{N}` via
  `lib/launch-session-name.ts` `nextProjectSessionName(slug, liveNames)`
  and retries on 409 by appending the refused name. `slug` is the host
  project's configured slug (`HostProject.slug`, `host-config-api.ts:12`),
  not the cwd basename.
- `ExecutionSummary.resume_session_id` is stored at delegate time, so it
  exists while the execution is still `queued`; `session_id` appears only
  once the provider has launched (nexen `execution/launch.go:171,211`).
  `settled` on the daemon is idle / failed / terminated.
- `ExecutionHeader` renders the button iff `onTakeBack` is passed;
  `ExecutionView` passes it iff `from` is set. `lib/nex/handoff.ts`
  `takeBack` swaps the pane to `{kind:'tmux-session', …from}` after the
  daemon call.

## 3. Goals / non-goals

### Goals

- G1 Every **claude** execution that has a session id can be taken to a
  terminal: the daemon creates a fresh tmux session in the execution's cwd,
  resumes Claude Code there, archives the execution, and the pane becomes
  that terminal pane.
- G2 The existing session-bound take-back keeps its behaviour and its
  endpoint; the new path shares the engine sequence (lease → interrupt →
  settle → resume → archive) rather than copying it.
- G3 The header shows one button, "Take to terminal", in both cases; the
  label string stays `takeback.button` (renamed copy, same key) so nothing
  else moves.
- G4 **Every hand-off asks whether to keep the tmux session** — whether
  the terminal was an ordinary tmux session or one that a take-to-terminal
  created. "Keep" is today's behaviour (idle shell stays, `from` recorded,
  the button later returns to that session). "Don't keep" kills the tmux
  session once the execution is running; the pane has no `from`, so the
  button later takes the execution to a **new** terminal (G1). The two
  directions are therefore symmetric: terminal ⇄ headless, with the tmux
  session as an optional anchor.

### Non-goals

- Non-claude providers (codex has no `--resume` equivalence yet; the
  button is hidden when `provider !== 'claude'`).
- Choosing the tmux session name or cwd in a dialog; the name follows the
  launcher rule, the cwd is the execution's.
- Taking an execution into an **existing** unrelated session.
- Executions on another host than the pane's daemon (executions live where
  they were delegated; the new session is created there).

## 4. Design

### 4.1 Daemon — `POST /api/nex/executions/{id}/take-to-terminal`

Registered by the nex module next to the `/api/nex/…` proxy routes (it is
purdex orchestration, not a Nexen route). Body:

```json
{ "session_name": "purdex-3", "resume_command": "claude --resume {id}",
  "lease_id": "…" }
```

- `session_name`: required, must pass the session module's name regex;
  `409 session_exists` if `HasSession` (the SPA retries with the next
  `{slug}-{N}` exactly as the launcher does).
- `resume_command`: required, `{id}` placeholder, same as take-back.
- `lease_id`: optional, same semantics as take-back.

Sequence (engine steps shared with `handleNexTakeback`, see 4.4):

1. Lock `m.locks.TryLock("exec:" + id)` → `409 takeback_in_progress`.
   (`HandoffLocks` is string-keyed; session codes are 6 base36 chars, so
   the `exec:` prefix cannot collide.) **The session-bound take-back takes
   the same execution lock** after its session lock and `boundToSession`
   check (order session → exec; this endpoint never takes a session lock,
   so no deadlock) — two clients cannot resume one execution into two
   panes (codex attacker F1).
2. Body: `session_name` missing → `400 missing_session_name`; fails the
   session name regex → `400 invalid_session_name`; `resume_command`
   missing → `400 missing_resume_command`; malformed JSON → `400
   malformed_body`.
3. `getExecution` → `404 execution_not_found`; `Provider != "claude"` →
   `409 provider_unsupported`; `ArchivedAt != 0` → `409 execution_archived`
   (a retried 200 or a second click must not put a second writer on the
   transcript — codex R1); state `queued` or `rejected` → `409
   execution_not_settled` (before any lease).
4. **Preflights that must not cost an interrupt**: `SessionProvider.
   SessionExists(name)` → `409 session_exists`; `SessionProvider.
   ValidateCwd(exec.Cwd)` (the module's `resolveCwd`) → `409 cwd_missing`
   with the path.
5. `settleForResume(exec, leaseID, principal)` — lease-if-running →
   interrupt → re-read → settled → session id (`SessionID` else
   `ResumeSessionID`, else `409 no_session_id`). See 4.4 for the lease
   contract.
6. `SessionProvider.CreateSession(name, cwd)`; a race with step 4 →
   `409 session_exists`; failure **after** `tmux new-session` succeeded
   (list / id / meta) → `500 session_create_failed` with detail
   `{session_name, session_alive: true}` — the tmux session exists but has
   no code yet; the SPA refreshes the session list so it shows up.
   `CreateSession` samples the tmux generation before `new-session` and
   after `list-sessions`; a change in between is `generation_changed`
   (the created session died with the old server; cold start with no
   server is the one allowed exception). The returned `SessionInfo.
   TmuxInstance` is the generation the session was created under.
7. **Archive first** (`SetArchived(true)`): from here on a second client
   gets `409 execution_archived`, so there is never a window in which two
   resumes can start (codex attacker F2). Archive failure → kill the
   created session (`KillSessionIfInstance(info.TmuxID, info.TmuxInstance)`)
   and answer `500 archive_failed {session_id, session_name,
   session_killed}`; the execution is untouched and the call can be
   retried.
8. `resumeInWindow` (send keys to window 0, `waitForCC`). On `send_failed`
   / `cc_start_timeout` / `tmux_instance_mismatch` the daemon kills the
   session **by id under the generation it was created in**
   (`KillSessionIfInstance`; a moved generation refuses, so a stranger's
   same-named session in a new server is never touched — codex R1/F4) and
   **unarchives** the execution (`SetArchived(false)`, failure logged).
   Detail: `session_id`, `session_name`, `session_killed`, `unarchived`.
9. Response `200 { "session": <SessionInfo>, "session_id": sid,
   "archived": true }` — `archived:false` no longer exists.

### 4.2 SPA

- `lib/nex/handoff-api.ts`: `nexTakeToTerminal(hostId, executionId, body)`.
- `lib/nex/handoff.ts`: `takeToTerminal(args)` — single-flight per
  execution; loads the host config for the resume template **and the
  projects list**; `slug` = the slug of the host project whose `path`
  equals the execution cwd (or is its nearest ancestor), else a fallback
  built from the cwd basename with every character outside
  `[a-zA-Z0-9_-]` replaced by `-`, collapsed and trimmed, `nex` if empty;
  `session_name = nextProjectSessionName(slug, liveNames)` from
  `useSessionStore` (retry once on `session_exists` with the refused name
  appended, like `session-launch.ts`); calls the endpoint; `forgetLease`;
  swaps the pane to `{kind:'tmux-session', hostId, sessionCode:
  session.code, mode:'terminal', cachedName: session.name, tmuxInstance:
  session.tmux_instance}` with the same `trySetPaneContent` predicate;
  `useSessionStore.fetchHost(hostId)` so the sidebar lists the new session.
- `ExecutionView`: `onTakeBack` is passed when `from` is set **or**
  `canTakeToTerminal` = `summary.provider === 'claude'` ∧ ¬`archived` ∧
  (`session_id` ∨ `resume_session_id`) ∧ `state ∈ {running, idle, failed,
  terminated}` —
  never on `queued` (the daemon would answer `execution_not_settled`) or
  `rejected`; it dispatches to `takeBack` or `takeToTerminal`. The
  running-turn confirm dialog is shared.
- Error handling: `session_create_failed` with `session_alive` → toast +
  `fetchHost` so the orphan session is visible; `cc_start_timeout` /
  `send_failed` → the existing manual-resume hint from `session_id` (the
  session was killed, nothing else to point at).
- `HANDOFF_ERROR_CODES` gains `session_exists`, `missing_session_name`,
  `invalid_session_name`, `session_create_failed`, `cwd_missing`,
  `provider_unsupported`, `takeback_in_progress`, `execution_archived`,
  `archive_failed` with locale strings (en + zh-TW).
- Session name slug: the host project matching the cwd. A project stored
  as `~/…` is expanded with the host home obtained from
  `checkHostPath(hostId, '~').resolved` (asked only when such a project
  exists; on failure `~/…` projects are skipped, never guessed by suffix
  — codex attacker F5), else the cleaned cwd basename.
- Copy: `takeback.button` → "Take to terminal"; success toast unchanged.

### 4.3 Hand-off: `keep_session`

- `POST /api/sessions/{code}/nex-handoff` body gains `keep_session: bool`
  (default `true` when absent — an old SPA keeps today's behaviour).
- Daemon (`handoff.go`): unchanged through delegate. With
  `keep_session:false`, once `Delegate` reports the execution `running`
  the daemon kills the tmux session **by id under the generation the
  request verified** (`KillSessionIfInstance(sess.TmuxID, expected)`; the
  shell is idle — CC has already exited — so nothing is lost) and answers
  `session_kept: false`. A refused kill (generation moved during the
  delegate) or a kill failure is logged and answers `session_kept: true`
  (the session is still there, the SPA keeps `from`). The execution's
  `origin`/labels are written as today; `boundToSession` on a later
  session-bound take-back simply fails with `session_missing` if anyone
  tries — the SPA never does, because `from` is absent.
- SPA: `HandoffConfirmDialog` gains a checkbox "Keep the tmux session",
  **default checked on every open, never remembered** (user ruling
  2026-09-19: 預設保留、每次都問). `handToNex` sends
  `keep_session` and sets `from` only when the response says
  `session_kept: true`. The tab's `cachedName` for the execution pane
  stays the session name either way (it is the tab title).
- Sidebar / session store: the killed session disappears through the
  normal session-list refresh; panes of **other** tabs bound to it are
  marked terminated by the existing path (the dialog body says so when the
  session has other panes: "N other panes use this session").

### 4.4 Shared engine sequence

`takeback.go` splits into two helpers; both handlers read the execution
**once** themselves (the existing tests count store reads) and run their
own preflights (`boundToSession`, `cc_already_running` for the
session-bound path; name / cwd / provider / state for the new one) before
calling:

- `settleForResume(parent ctx, exec store.Execution, leaseID, principal)
  (settled store.Execution, sid string, release func(), herr
  *handoffError)` — takes the already-read row; lease-if-running →
  interrupt → re-read → settled → session id. **Lease contract**: if the
  helper acquired the lease, it releases it (fresh detached context)
  before returning any error, and on success returns `release` for the
  caller to `defer` (so it runs after archive, as today); a caller-provided
  lease is never released (`release` is a no-op). Helper-level tests pin
  every error exit.
- `resumeInWindow(tmuxID, window, expected, resumeCommand, sid)
  (herr *handoffError)` — send keys + `waitForCC`, the same two error
  codes as today.

`handleNexTakeback` keeps its order and `takeback_test.go` passes
unchanged — the pure-move proof. The new handler adds kill-on-failure
around `resumeInWindow` (4.1 step 7).

## 5. Invariants

- I1 Session-bound take-back: same request, same response, same error
  codes, same tests (`takeback_test.go` untouched except imports if the
  helper moves).
- I2 The new endpoint never touches a session it did not create.
- I3 A failure before session creation leaves the execution as it was
  (settled, unarchived) and costs no interrupt unless the failure came
  from the engine itself. At most one `claude --resume` is ever started
  per call, and the execution is archived before it starts; a failure in
  `resumeInWindow` kills the created session (by id, under its
  generation) and unarchives. The only way a session outlives a failed
  call is `session_create_failed` after `tmux new-session`, and then the
  detail says `session_alive: true`.
- I6 No kill by name anywhere in these paths: every kill is
  `KillSessionIfInstance(id, generation)`.
- I4 `nex/imports_test.go` boundary holds; `Dependencies()` stays
  `{"session","agent"}`.
- I5 With `keep_session` absent or `true` the hand-off request/response is
  byte-identical to alpha.402 except the added `session_kept: true` field.

## 6. Acceptance (mlab, worktree dev server :5175, playwright `exec-to-terminal`)

1. `go test ./...`, `cd spa && npx tsc --noEmit -p tsconfig.app.json &&
   npx vitest run && pnpm run lint && pnpm run build`.
2. NewTab Headless → delegate a claude execution in a repo root → one
   turn → header shows **Take to terminal** → click → pane becomes a
   terminal named `<slug>-N`, `claude` is up with the conversation
   visible; sidebar lists the session; execution is archived.
3. Same from a **running** turn: confirm dialog → interrupt → resume; the
   partial turn is not lost (transcript has it).
4. Session-bound path unchanged: Hand to nex → Take to terminal returns to
   the original session (P-C §6 steps 3–4).
5. Negative: cwd deleted after delegate → `cwd_missing` toast, execution
   still there and **not interrupted** (do it on a running turn); name
   collision → second attempt succeeds with `-N+1`; execution still
   `queued` (delegate then click within the first second) → no button.
6. `pdx nex delegate` from the CLI → open it from the sidebar → Take to
   terminal works the same (no `from`).
7. Hand to nex with **Keep** unchecked: tmux session gone (`tmux ls`),
   pane is the execution, header shows Take to terminal → click → a new
   `<slug>-N` session appears with the conversation. With Keep checked:
   today's behaviour. Re-open the dialog after either choice: the box is
   checked again.
8. A session with two panes, hand-off from one with Keep unchecked: the
   dialog warns; after confirm the other pane shows terminated.

## 7. Review

Plan + spec one codex round (`gpt-5.6-sol`); PR R1 → attacker → critic;
incremental re-review only.
