# Plan — take any execution to a terminal (#1210)

Spec: `docs/specs/2026-09-19-exec-to-terminal-spec.md` v1.2 (codex plan review `task-mu75q9tz-hn36pf` applied). Anchors
measured on `4b7f4aee` (alpha.402). One PR (daemon + SPA, estimated
≤ 800 lines / ≤ 20 files). TDD per task, one commit per task with
`git commit --only`; subagents prefix every Bash with `cd <worktree> &&`
(Go) or `cd <worktree>/spa &&` (SPA); SPA type check is
`npx tsc --noEmit -p tsconfig.app.json` (bare `--noEmit` is a no-op).

## T1 session module: `CreateSession` on the provider (TDD)

- `internal/module/session/provider.go:7-18` `SessionProvider` gains
  `SessionExists(name string) bool`, `ValidateCwd(cwd string) error` and
  `CreateSession(name, cwd string) (*SessionInfo, error)`. Every fake that
  implements the interface (`grep -rn "TmuxInstance() string" internal
  --include='*_test.go'`) gets the method (return `nil, errors.New("not
  implemented")` where unused).
- Extract the body of `handler.go:70 handleCreate` after decoding —
  name regex (`nameRegex`), `resolveCwd`, the `HasSession → NewSession →
  ListSessions → SetMeta` critical section — into
  `func (m *SessionModule) CreateSession(name, cwd string) (*SessionInfo,
  error)` returning `*CreateError{Stage, Name, Err}` with `Stage ∈
  {invalid_name, invalid_cwd, exists, new_session, list, encode, meta}`
  and `errors.Is` sentinels `ErrInvalidSessionName`, `ErrInvalidCwd`,
  `ErrSessionExists`; `(*CreateError).SessionAlive()` is true for stages
  after `new_session` (the tmux session exists, no code yet).
  `handleCreate` maps them to the same HTTP codes/texts it emits today
  (400/400/409, 500 for the rest), so `handler_test.go` create tests pass
  unchanged. The legacy `mode` coercion (P-D.2) stays in the handler.
  `ValidateCwd` wraps `resolveCwd`; `SessionExists` wraps `tmux.HasSession`.
- New tests in `handler_test.go`: `TestCreateSession_ReturnsInfo`
  (fake tmux → info with code/name/cwd/tmux_instance),
  `TestCreateSession_Exists` → `ErrSessionExists` and `SessionAlive()==false`,
  `TestCreateSession_BadName`, `TestCreateSession_BadCwd`,
  `TestCreateSession_ListFails_SessionAlive` (fake `ListSessions` error
  after `NewSession` → stage `list`, `SessionAlive()==true`), same for a
  `SetMeta` failure.

## T2 nex takeback.go: extract the shared sequence (pure move + tests unchanged)

- `settleForResume(parent context.Context, exec store.Execution, leaseID,
  principal string) (settled store.Execution, sid string, release func(),
  herr *handoffError)` from `takeback.go:150-210` — **the caller has
  already read the row** (`takeback_test.go:323-339` count one `Get`) and
  run its preflights (`boundToSession` at `:148` stays in the handler).
  Lease contract (spec §4.4): helper-acquired lease is released with a
  fresh detached context before any error return; on success `release`
  is returned for the caller to `defer` (runs after archive, as the defer
  at `:168` does today); caller-provided lease → `release` is a no-op and
  never released (`takeback_test.go:371,408,423`). `handoffError` =
  `{status int; code, msg string; detail map[string]any}`;
  `writeHandoffError` grows a `write(w, herr)` helper.
- `resumeInWindow(tmuxID, window, expected, resumeCommand, sid string)
  herr *handoffError` from `:223-239` (send + `waitForCC`; `send_failed`,
  `tmux_instance_mismatch`, `cc_start_timeout`).
- `handleNexTakeback` calls both in its current order; `takeback_test.go`
  (34 tests) passes without edits — the pure-move proof. Add helper-level
  tests (`settle_test.go`) that pin the lease release on each error exit
  (interrupt unconfirmed / failed, re-read error, not settled, no session
  id) and the no-release on a caller lease. No codex on the move commit.

## T3 nex: `POST /api/nex/executions/{id}/take-to-terminal` (TDD)

- New file `internal/module/nex/take_to_terminal.go`; route at
  `module.go:308` sibling line. Request `takeToTerminalRequest{SessionName,
  ResumeCommand, LeaseID}`.
- Sequence exactly as spec §4.1 steps 1–8: lock `"exec:"+id` → body
  validation (four 400 codes) → `getExecution` → provider → state
  (`queued`/`rejected` → `execution_not_settled` before any lease) →
  `SessionExists` → `ValidateCwd` → `settleForResume` (defer `release`) →
  `CreateSession` (map `ErrSessionExists` → 409; any other `*CreateError`
  → 500 `session_create_failed` with `{session_name, session_alive}`) →
  `resumeInWindow`; on its error `KillSession(name)` (log a kill failure,
  detail `session_id`) → archive → 200.
- Tests `take_to_terminal_test.go` using the existing fakes from
  `handoff_fakes_test.go` / `takeback_test.go` (recordingCCOperator,
  fake store, fake tmux), full matrix: happy path (session created with
  the exec cwd, keys to window 0, archived, response shape); running turn
  → lease + interrupt then resume, lease released after archive;
  caller-provided `lease_id` → no acquire, no release; malformed body /
  missing name / bad name / missing resume_command → 400 and **no store
  read**; `provider: codex` → 409; `queued` → 409 and no lease; no
  session id → 409; `session_exists` → 409 and **no engine call, no
  interrupt** (fake store asserts zero lease/interrupt calls); cwd missing
  → 409 and no session created, no interrupt; `CreateSession` list-stage
  failure → 500 with `session_alive:true`, execution unarchived; send
  fails / `waitForCC` times out → session killed (fake records
  `KillSession(name)`), execution unarchived, detail `session_id`;
  archive fails → 200 `archived:false`; lock held → 409; lock keys
  `exec:<id>` vs a session code never collide (unit test on
  `HandoffLocks`).
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
  `slug` per spec §4.2: `useHostConfigStore` projects (`HostProject.slug`,
  `host-config-api.ts:12`; `session-launch.ts:73,85` shows how the
  launcher reads it) matched by `path` equal to or the nearest ancestor of
  the execution cwd; fallback `fallbackSlugFor(cwd)` (basename, chars
  outside `[a-zA-Z0-9_-]` → `-`, collapse, trim, `nex` if empty) in a new
  `lib/nex/session-slug.ts` with its own tests (matching, nearest
  ancestor, special chars, empty). `session_name =
  nextProjectSessionName(slug, liveNames)`, `liveNames` from
  `useSessionStore.getState().sessions[hostId]`; on `session_exists` retry
  once with the refused name appended; on success `forgetLease()`,
  `trySetPaneContent` to the new tmux-session content,
  `useSessionStore.getState().fetchHost(hostId)` (fire-and-forget); on
  `session_create_failed` with `session_alive` also `fetchHost` before
  rethrowing.
  `HANDOFF_ERROR_CODES` + locale strings (`en.json`, `zh-TW.json`;
  `locale-completeness.test.ts` enforces both).
- Tests `handoff.test.ts`: request body (name computed, retry on 409 with
  advanced N), pane swap predicate, lease forget, error mapping.

## T5 SPA: header button on every claude execution (TDD)

- `ExecutionView.tsx`: `canTakeToTerminal = !from && summary?.provider ===
  'claude' && !!(summary?.session_id || summary?.resume_session_id) &&
  TAKEABLE_STATES.has(summary.state)` with `TAKEABLE_STATES = {running,
  idle, failed, terminated}`;
  `onTakeBack` passed when `from || canTakeToTerminal`; `runTakeBack`
  dispatches to `takeBack` / `takeToTerminal` (the confirm-if-running
  dialog and busy handling are shared as they are).
- `ExecutionHeader` unchanged except copy `takeback.button` → "Take to
  terminal" / 「接回終端機」.
- Tests `ExecutionView.test.tsx`: visibility matrix state × id source
  (queued+resume_session_id → hidden; running+session_id → shown;
  idle+resume_session_id only → shown; rejected → hidden), absent for
  codex, present with `from` regardless; click → `takeToTerminal` called
  with the summary cwd.

## T6 daemon hand-off: `keep_session` (TDD)

- `handoff.go:95` `handoffRequest` gains `KeepSession *bool`
  (`json:"keep_session,omitempty"`; nil → true). After the point where the
  execution is confirmed running and `connected` is broadcast (find the
  `writeJSON(w, 200 …)` and the step before it), when `!keep`:
  `m.tmux.KillSession(sess.Name)`; success → response `session_kept:false`;
  error → logged, `session_kept:true`. Response struct gains `SessionKept
  bool json:"session_kept"`.
- Tests in `handoff_test.go` (fakes already there; `FakeExecutor.KillSession`
  exists at `internal/tmux/fake_executor.go:241`): absent field → no kill,
  `session_kept:true`; `false` → kill called once with the session name
  **after** delegate succeeded, `session_kept:false`; kill error → no
  failure, `session_kept:true`; delegate rejected → no kill.

## T7 SPA hand-off dialog + `from` (TDD)

- `HandoffConfirmDialog.tsx`: checkbox `keep-session` (label
  `handoff.keep_session`, default checked on every open, not persisted
  anywhere); when the
  session has other panes (count via `useTabStore` panes bound to the same
  host+code, minus this one) the body appends `handoff.other_panes` with N.
- `handoff-api.ts` request type `keep_session?: boolean`, result
  `session_kept: boolean`; `handoff.ts` `handToNex` takes `keepSession`,
  sends it, and builds `from` only when `result.session_kept`.
- Tests: dialog defaults to checked on every mount (no persistence), N-panes warning, `handToNex`
  omits `from` on `session_kept:false`, includes it on `true`; ExecutionView
  then shows Take to terminal routed to `takeToTerminal` for that pane.

## T8 acceptance + PR

Spec §6 (incl. 7–8) on :5175 (worktree dev server) against mlab daemon rebuilt from
this branch (`make build` → new-inode copy → `stop`/`start`). Fork does
steps 2–6 with playwright `exec-to-terminal`; token via the awk one-liner
only. PR → R1 → attacker → critic → bump.
