# Spec — take any execution to a terminal (#1210)

- Status: v1.0 (2026-09-19) — draft, plan reviewed together with this spec
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
  Nexen injects only `CLAUDE_CODE_OAUTH_TOKEN` and
  `CLAUDE_SECURESTORAGE_CONFIG_DIR` and blocks `CLAUDE_CODE_CHILD_SESSION`,
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
  and retries on 409 by appending the refused name.
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

Sequence (one function shared with `handleNexTakeback`, see 4.3):

1. Lock `m.locks.TryLock("exec:" + id)` → `409 takeback_in_progress`.
2. `getExecution` → 404; `Provider != "claude"` → `409 provider_unsupported`.
3. Lease-if-running → interrupt → re-read → `settled` check (existing code).
4. Session id (`SessionID` else `ResumeSessionID`) → `409 no_session_id`.
5. **Create the session** via a new `SessionProvider.CreateSession(name,
   cwd string) (*SessionInfo, error)` — the session module extracts the
   body of `handleCreate` into it (mode is always terminal; the handler
   becomes a thin wrapper). Errors map to `409 session_exists` /
   `400 invalid_session_name` / `500 session_create_failed`. cwd must exist
   (`tmux new-session -c` silently falls back to `$HOME`, see memory
   `reference_tmux_new_session_cwd_silent_fallback`): stat it first →
   `409 cwd_missing` with the path.
6. Send the resume keys to the new session's window 0 with
   `SendKeysIfInstanceTarget`, `waitForCC` (existing), archive (existing;
   failure logged, `archived:false`).
7. Response `200 { "session": <SessionInfo>, "session_id": sid,
   "archived": bool }`. On a failure after the session was created the
   session is **left alive** (a shell in the right cwd is useful; the
   error detail carries `session_code` and `session_id` so the SPA can
   offer the manual `claude --resume` hint, as take-back does today).

### 4.2 SPA

- `lib/nex/handoff-api.ts`: `nexTakeToTerminal(hostId, executionId, body)`.
- `lib/nex/handoff.ts`: `takeToTerminal(args)` — single-flight per
  execution; loads the host config for the resume template; computes
  `session_name = nextProjectSessionName(basename(summary.cwd), liveNames)`
  from `useSessionStore` (retry once on `session_exists` with the refused
  name appended, like `session-launch.ts`); calls the endpoint; `forgetLease`;
  swaps the pane to `{kind:'tmux-session', hostId, sessionCode:
  session.code, mode:'terminal', cachedName: session.name, tmuxInstance:
  session.tmux_instance}` with the same `trySetPaneContent` predicate;
  `useSessionStore.fetchHost(hostId)` so the sidebar lists the new session.
- `ExecutionView`: `onTakeBack` is passed when `from` is set **or**
  (`summary.provider === 'claude'` and (`summary.session_id ||
  summary.resume_session_id`)); it dispatches to `takeBack` or
  `takeToTerminal`. The running-turn confirm dialog is shared.
- `HANDOFF_ERROR_CODES` gains `session_exists`, `invalid_session_name`,
  `session_create_failed`, `cwd_missing`, `provider_unsupported`,
  `takeback_in_progress` with locale strings (en + zh-Hant, the two files
  that exist).
- Copy: `takeback.button` → "Take to terminal"; success toast unchanged.

### 4.3 Shared engine sequence

`takeback.go` splits into `settleForResume(ctx, execID, leaseID,
principal) (exec, sid, release func, *takebackError)` (steps 3–4 of 4.1,
today's lines ~132–210) and `resumeInPane(sess, expected, keys)` (send +
wait). `handleNexTakeback` and the new handler both call them; the
session-bound handler keeps its `boundToSession` and `cc_already_running`
preflights. Pure move for the extracted parts — proved by the existing
take-back tests passing unchanged.

## 5. Invariants

- I1 Session-bound take-back: same request, same response, same error
  codes, same tests (`takeback_test.go` untouched except imports if the
  helper moves).
- I2 The new endpoint never touches a session it did not create.
- I3 A failure before session creation leaves the execution as it was
  (settled, unarchived); a failure after leaves the session alive and the
  execution unarchived, and the response says which.
- I4 `nex/imports_test.go` boundary holds; `Dependencies()` stays
  `{"session","agent"}`.

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
   still there; name collision → second attempt succeeds with `-N+1`.
6. `pdx nex delegate` from the CLI → open it from the sidebar → Take to
   terminal works the same (no `from`).

## 7. Review

Plan + spec one codex round (`gpt-5.6-sol`); PR R1 → attacker → critic;
incremental re-review only.
