# Plan — take any execution to a terminal (#1210)

Spec: `docs/specs/2026-09-19-exec-to-terminal-spec.md` v1.0. Anchors
measured on `4b7f4aee` (alpha.402). One PR (daemon + SPA, estimated
≤ 800 lines / ≤ 20 files). TDD per task, one commit per task with
`git commit --only`; subagents prefix every Bash with `cd <worktree> &&`
(Go) or `cd <worktree>/spa &&` (SPA); SPA type check is
`npx tsc --noEmit -p tsconfig.app.json` (bare `--noEmit` is a no-op).

## T1 session module: `CreateSession` on the provider (TDD)

- `internal/module/session/provider.go:7-18` `SessionProvider` gains
  `CreateSession(name, cwd string) (*SessionInfo, error)`. Every fake that
  implements the interface (`grep -rn "TmuxInstance() string" internal
  --include='*_test.go'`) gets the method (return `nil, errors.New("not
  implemented")` where unused).
- Extract the body of `handler.go:70 handleCreate` after decoding —
  name regex (`nameRegex`), `resolveCwd`, the `HasSession → NewSession →
  ListSessions → SetMeta` critical section — into
  `func (m *SessionModule) CreateSession(name, cwd string) (*SessionInfo,
  error)` returning typed errors `ErrInvalidSessionName`,
  `ErrInvalidCwd`, `ErrSessionExists` (sentinels in `provider.go`).
  `handleCreate` maps them to the same HTTP codes/texts it emits today
  (400/400/409), so `handler_test.go` create tests pass unchanged. The
  legacy `mode` coercion (P-D.2) stays in the handler.
- New tests in `handler_test.go`: `TestCreateSession_ReturnsInfo`
  (fake tmux → info with code/name/cwd/tmux_instance),
  `TestCreateSession_Exists` → `ErrSessionExists`,
  `TestCreateSession_BadName` → `ErrInvalidSessionName`.

## T2 nex takeback.go: extract the shared sequence (pure move + tests unchanged)

- `settleForResume(ctx, execID, leaseID, principal string) (exec
  store.Execution, sid string, cleanup func(), herr *handoffError)` from
  `takeback.go:132-210` (lease-if-running → interrupt → re-read → settled →
  session id). `handoffError` = `{status int; code, msg string; detail
  map[string]any}`; `writeHandoffError` grows a `write(w, herr)` helper.
  `cleanup` releases the lease the helper acquired (nil when caller-owned).
- `resumeInWindow(tmuxID, window, expected, resumeCommand, sid string)
  (sent bool, herr *handoffError)` from `:223-239` (send + `waitForCC`).
- `handleNexTakeback` calls both; `takeback_test.go` (34 tests) passes
  without edits — that is the pure-move proof; no codex on this commit.

## T3 nex: `POST /api/nex/executions/{id}/take-to-terminal` (TDD)

- New file `internal/module/nex/take_to_terminal.go`; route at
  `module.go:308` sibling line. Request `takeToTerminalRequest{SessionName,
  ResumeCommand, LeaseID}`.
- Sequence per spec §4.1: lock `"exec:"+id` on `m.locks`
  (`session.HandoffLocks` is string-keyed; add a test that the session
  lock and the exec lock do not collide) → `getExecution` →
  `provider_unsupported` → `settleForResume` → `os.Stat(exec.Cwd)` →
  `cwd_missing` → `m.sessions.CreateSession(name, exec.Cwd)` mapping the
  three sentinels → `resumeInWindow(sess.TmuxID, paneWindow,
  m.sessions.TmuxInstance(), …)` → archive → `200 {session, session_id,
  archived}`.
- Tests `take_to_terminal_test.go` using the existing fakes from
  `handoff_fakes_test.go` / `takeback_test.go` (recordingCCOperator,
  fake store, fake tmux): happy path (session created with exec cwd, keys
  sent to window 0, archived, response shape); running turn → lease +
  interrupt then resume; `provider: codex` → 409; no session id → 409;
  `session_exists` → 409 and **no** engine call; cwd missing → 409 and no
  session created; resume send fails after create → session left alive,
  execution unarchived, detail has `session_code`; lock held → 409.
- `nex/imports_test.go` still green.

## T4 SPA: API + orchestration (TDD)

- `lib/nex/handoff-api.ts`: `NexTakeToTerminalRequest/Result` types,
  `nexTakeToTerminal(hostId, executionId, body)` (`POST
  /api/nex/executions/{id}/take-to-terminal` via the same fetch helper the
  session endpoints use — check `postSessionJson` and add a sibling for
  the execution path).
- `lib/nex/handoff.ts`: `takeToTerminal({hostId, executionId, cwd,
  leaseId, tabId, paneId, forgetLease})` — single-flight
  `takeback:<host>:<exec>` (same key as takeBack so the two cannot
  interleave); `ensureLoaded` host config; `resumeTemplateFor`;
  `session_name = nextProjectSessionName(slugOf(cwd), liveNames)` where
  `slugOf` is whatever the launcher uses (`grep -n slug
  spa/src/lib/session-launch.ts`) and `liveNames` come from
  `useSessionStore.getState().sessions[hostId]`; on `session_exists`
  retry once with the refused name appended; on success `forgetLease()`,
  `trySetPaneContent` to the new tmux-session content,
  `useSessionStore.getState().fetchHost(hostId)` (fire-and-forget).
  `HANDOFF_ERROR_CODES` + locale strings (`en.json`, `zh-TW.json`;
  `locale-completeness.test.ts` enforces both).
- Tests `handoff.test.ts`: request body (name computed, retry on 409 with
  advanced N), pane swap predicate, lease forget, error mapping.

## T5 SPA: header button on every claude execution (TDD)

- `ExecutionView.tsx`: `canTakeToTerminal = !from && summary?.provider ===
  'claude' && !!(summary?.session_id || summary?.resume_session_id)`;
  `onTakeBack` passed when `from || canTakeToTerminal`; `runTakeBack`
  dispatches to `takeBack` / `takeToTerminal` (the confirm-if-running
  dialog and busy handling are shared as they are).
- `ExecutionHeader` unchanged except copy `takeback.button` → "Take to
  terminal" / 「接回終端機」.
- Tests `ExecutionView.test.tsx`: button present for a headless claude
  execution with `session_id`, absent for codex, absent without any
  session id, present with `from` regardless; click → `takeToTerminal`
  called with the summary cwd.

## T6 acceptance + PR

Spec §6 on :5175 (worktree dev server) against mlab daemon rebuilt from
this branch (`make build` → new-inode copy → `stop`/`start`). Fork does
steps 2–6 with playwright `exec-to-terminal`; token via the awk one-liner
only. PR → R1 → attacker → critic → bump.
