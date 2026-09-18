# Plan — P-C.3a: daemon `nex-handoff` / `nex-takeback`

- Spec: `2026-09-18-pc-launch-ui-spec.md` v1.2 §4.4 (daemon parts), §5
  (P-C.3), §6 items 3–6. P-C.3b (SPA) is a separate plan/PR.
- Worktree `.claude/worktrees/pc-launch-ui`, branch `worktree-pc-launch-ui`
  (at alpha.382). One PR, Go only (`internal/`), ≤ 800 lines target.
- Every task: subagent, TDD (`go test ./internal/module/nex/... ./internal/module/stream/... ./internal/module/session/...`
  green per commit), one commit with `git commit --only <files>`. Every
  Bash call prefixed with
  `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/pc-launch-ui && `.
  **After every task commit**: `go build ./... && go vet ./... && go test
  ./...` (whole module — task 1 crosses packages, task 2 changes `Init`
  hard dependencies) and `gofmt -l` empty for touched files.
- **Tasks 1→2→3→4 sequential.** No SPA, no Nexen change.

## Spec corrections carried by this plan (amend spec §4.4 in task 2)

- §4.4 says "the nex module already depends on the session provider" —
  **false**: `internal/module/nex/module.go:91` `Dependencies()` returns
  `nil` (pinned by `TestNameAndDependencies`). Task 2 adds
  `{"session", "agent"}`.
- The engine seam (`module.go:23-27`) keeps only `handler/shutdown/close`;
  `realAssemble` (`:37-47`) discards `sys.Service/Store/Bus`. Task 2 widens
  it.
- `internal/module/nex/imports_test.go` `TestImportBoundary` allows only
  `nexen`, `nexen/api`, `nexen/config`, `nexen/sandbox`. Task 2 adds
  `nexen/execution` and `nexen/store` with a one-line rationale in the test.
- Nexen has no `Service.Get`; the row is read with `System.Store.Get(ctx,
  id)` (`store/execution.go:797`); `session_id` is `Execution.SessionID`,
  `resume_session_id` is `Execution.ResumeSessionID`.

## Measured baseline (2026-09-18, alpha.382)

- `internal/module/nex/module.go` 204 lines: `engine{handler, shutdown,
  close}` 23–27; `realAssemble` 37–47; `Module` 51–72; `Dependencies` 91;
  `Init(c *core.Core)` 119–160 (`buildOptions`, `m.assemble`, `softFail`
  171–180); `RegisterRoutes` 187–193 (`RoutePrefix + "/"` → `StripPrefix` +
  `recoverer`); `unavailableHandler` 197–204 (503 `nex_unavailable`).
  `build_config.go:125-133 principalAuth(hostID) api.Authenticator`
  (principal `pdx:<hostID>` or `pdx:<hostID>/<X-Pdx-Client>`) stored as
  `m.opts.Auth`. `status.go:15-17` ready ⇔ `handler != nil`.
  `module_test.go` 56–104 helpers (`baseConfig`, `newTestCore`,
  `newFakeAssemble`, `noopEngine`), `:499-600` real-assemble tests with
  `fakeClaude(t)`.
- Nexen v0.11.2: `System{Handler, Service *execution.Service, Store
  *store.Store, Bus}` (`assemble.go:171-175`); `execution.Request{PrincipalID,
  Provider, Brief, SandboxProfile, Mounts []Mount{Path, Role, Writable},
  Origin, Labels, ResumeSessionID}` (`service.go:52-84`); `Result{ID, State
  store.State, RejectReason, EffectiveProfile}` (`:90-95`);
  `Delegate(ctx, req) (Result, error)` `:410` (rejection = `Result.State ==
  store.StateRejected`, error only for infra/validation
  `store.ErrInvalidOrigin|ErrInvalidLabels|ErrInvalidResumeSessionID`);
  `AcquireLease(ctx, execID, principal) (store.Lease, error)`
  (`lease.go:38`), `ReleaseLease(ctx, execID, leaseID, principal) error`
  (`:111`), `store.ErrLeaseHeld` (`store/lease.go:44`);
  `Interrupt(ctx, InterruptRequest{ExecutionID, LeaseID, PrincipalID})
  (InterruptResult{TurnID, State, Reason}, error)` (`service.go:919-955`;
  `ErrNoLiveTurn` benign, `ErrInterruptUnconfirmed` → 504,
  `ErrExecutionOrphaned`); `Archive(ctx, ArchiveRequest{ExecutionID,
  PrincipalID, Archived}) error` (`:1129-1172`, `ErrArchiveWhileRunning`);
  `Store.Get(ctx, id) (store.Execution, error)` with `State, SessionID,
  ResumeSessionID, LeaseID, LeasePrincipalID, ArchivedAt, Cwd`; states
  `store.StateQueued/Running/Idle/Rejected/Failed/Terminated`.
  `sandbox.UsableProfiles(policy)` (`sandbox/profile.go:153-168`).
  API-layer validators `store.ValidateOrigin/ValidateBrief/
  ValidateResumeSessionID` (used by `api/handlers.go:113-177`).
- Agent module: `internal/module/agent/owner_resolver.go:5-28`
  `OwnerResolverKey = "agent.owner-resolver"`, `OwnerResolver{
  ResolveSessionOwner(ctx, code) (PaneOwner, bool, error)}`; `PaneOwner{
  FrameID, AgentType, SessionID, Cwd, TmuxPaneID, LastSeenAt, Status}`
  (`pane_owner.go:18-26`); prober registered as `"agent.prober"`
  (`*probe.Prober`, `agent/module.go:236`); `provenance_handler.go:49-69`
  samples `TmuxInstance()` before/after (`provenanceTimeout = 5s`).
- CC operator: `internal/agent/cc/interfaces.go:6-11` `CCOperator{Exit,
  Launch, Interrupt, GetStatus}`, key `agentcc.OperatorKey = "cc.operator"`;
  `Interrupt` polls readiness to idle, `Exit` sends `/exit` and polls
  `!IsAliveFor("cc")` (`cc/operator.go`). Prober `IsAliveFor(agentType,
  target) bool` (`probe/liveness.go:148`), `CheckReadiness(agentType,
  target) (ReadinessResult, bool)` (`probe/readiness.go:5`); stream's
  narrow `livenessProber` interface at `stream/module.go:32-35`.
- tmux: `core.Tmux tmux.Executor` (`core/core.go:38`); `SendKeys(target,
  keys)` **appends Enter** (`tmux/executor.go:298-300`);
  `SendKeysIfInstance(sessionID "$N", expectedInstance, keys...) (sent bool,
  err)` (`send_keys_conditional.go:101`, no Enter — send `cmd + "\n"`);
  `tmux.ValidInstance`; `tmux.NewFakeExecutor()` with `SetInstance`,
  `KeysSent()`, `SetPaneCommand`, `FailSendKeys`.
- Session provider: `session.RegistryKey = "session.provider"`,
  `SessionProvider{GetSession(code) (*SessionInfo, error) (nil,nil when
  unknown); TmuxInstance() string; …}`; `SessionInfo{Code, TmuxID "$N", Name,
  TmuxInstance, Cwd, …}`; target convention `sess.Name + ":0"`.
- Lock: `internal/module/stream/locks.go:6-32` `handoffLocks{TryLock(key)
  bool; Unlock(key)}`, `newHandoffLocks()`; tests
  `stream/handler_test.go:391-410`; used `stream/handler.go:295-298`,
  `orchestrator.go:26,156`.
- Registry: `c.Registry.Get(key)` / `MustGet` (`core/registry.go:20-38`);
  consumer pattern `internal/module/peers/module.go:228-256`;
  `cmd/pdx/main.go:269-308` adds nex last, conditionally; `topoSort` orders
  by `Dependencies()`.
- Test patterns: `stream/orchestrator_test.go:27-175` (`fakeCCOperator`,
  `fakeStreamProber` over `tmux.FakeExecutor` + real readiness checker,
  `setupHandoffModule` builds the module struct directly + `RegisterRoutes`
  + `httptest`), `stream/handler_test.go:20-84` `fakeSessionProvider`.

## Task 1 — pure move: per-session lock to `session`

Files: new `internal/module/session/locks.go` (+ `locks_test.go`),
`internal/module/stream/{locks.go (deleted), handler.go, orchestrator.go,
module.go, handler_test.go}`.

- Move `handoffLocks` → `session.HandoffLocks` (exported type,
  `session.NewHandoffLocks()`, methods `TryLock`/`Unlock` unchanged); stream
  keeps its field but of the new type. Tests move to `session/locks_test.go`.
- Pure-move verification (report): method bodies byte-identical (diff
  against `git show HEAD:internal/module/stream/locks.go` modulo package
  line and exported names); `go test ./internal/module/stream/...
  ./internal/module/session/...` green, same test count.
- Commit: `refactor(session): per-session handoff lock shared by stream and nex (P-C.3a task 1)`.

## Task 2 — nex module gains Service access + dependencies

Files: `internal/module/nex/{module.go, imports_test.go, module_test.go,
status.go}`, new `internal/module/nex/engine_iface.go`, spec §4.4 (the
corrections above).

- `engine` seam gains `service nexService` and `store nexStore` where:
  ```go
  type nexService interface {
    Delegate(ctx, execution.Request) (execution.Result, error)
    AcquireLease(ctx, execID, principal string) (store.Lease, error)
    ReleaseLease(ctx, execID, leaseID, principal string) error
    Interrupt(ctx, execution.InterruptRequest) (execution.InterruptResult, error)
    Archive(ctx, execution.ArchiveRequest) error
  }
  type nexStore interface { Get(ctx, id string) (store.Execution, error) }
  ```
  (`*execution.Service` and `*store.Store` satisfy them; tests use fakes).
  `realAssemble` fills them from `sys.Service`/`sys.Store`; `noopEngine()`
  gets nil fakes.
- `Dependencies()` → `[]string{"session", "agent"}`; `Init` looks up
  `session.RegistryKey` (SessionProvider), `agent.OwnerResolverKey`
  (OwnerResolver), `"agent.prober"` (narrow `livenessProber` interface
  copied from stream: `IsAliveFor`, `CheckReadiness`), `agentcc.OperatorKey`
  (`CCOperator`), and keeps `c.Tmux`. Missing provider → `Init` error
  (hard, like peers) — but keep the nex **engine** soft-fail unchanged.
  `cmd/pdx/main.go` needs no change (nex is added last; topoSort handles
  order) — verify with a test that `Dependencies()` is honoured (update
  `TestNameAndDependencies`).
- `imports_test.go`: allow `lab.protype.tw/wake/nexen/execution` and
  `…/store` with the comment "handoff calls the embedded Service directly
  (spec §4.4)".
- Principal helper `m.principal(r)` = `m.opts.Auth.Authenticate(r)` (same
  as `/api/nex` would derive).
- Spec amendment: replace the "already depends" sentence with the
  dependency list; add the import-boundary note to §4.5.
- Tests: dependencies; Init fails clearly without session/agent providers;
  engine seam carries service/store after real assemble
  (`TestInitRealAssemble…` variant asserting non-nil).
- Commit: `feat(nex): nex module exposes the embedded Service and depends on session + agent (P-C.3a task 2)`.

## Task 3 — `POST /api/sessions/{code}/nex-handoff`

Files: new `internal/module/nex/handoff.go` + `handoff_test.go`,
`module.go` (route + lock field `locks *session.HandoffLocks`).

Implement spec §4.4 "Daemon: nex-handoff" exactly:
- Preconditions (no lock): `m.sys.service == nil` → 503 `nex_unavailable`;
  `handoff ∉ sandbox.UsableProfiles(m.opts.Config.Sandbox)` → 409
  `handoff_unsupported`. `delegate.resume_session_id` is a property of the
  pinned Nexen, not of host config: pin it with a test that serves
  `GET /v1/capabilities` through the real assembled `m.sys.handler`
  (`httptest`, as `TestInitRealAssemble…` does) and asserts
  `delegate.resume_session_id === true` — a future pin bump that drops it
  fails this test rather than silently breaking handoff (spec §4.4
  precondition, amended to say so).
- Body `{expected_tmux_instance, profile?, rollback_command?}`; 400 on
  invalid instance (`tmux.ValidInstance`) or malformed JSON.
- `TryLock(code)` → 409 `handoff_in_progress`; `defer Unlock`.
- Session: `GetSession(code)` nil → 404 `session_missing`. Generation
  sample `TmuxInstance()` ≠ expected → 409 `tmux_instance_mismatch`.
- Identity: `ResolveSessionOwner(ctx 5 s, code)`; `!found || AgentType !=
  "cc" || SessionID == ""` → 409 `no_identity`.
- `IsAliveFor("cc", name+":0")` false → 409 `no_cc`.
- **Last generation sample before any key is sent** (spec step 1's
  re-sample comes *after* step 3's liveness check): `TmuxInstance()` ≠
  expected → 409 `tmux_instance_mismatch` (nothing sent yet).
- `CheckReadiness` ≠ idle → `Interrupt(ctx 10 s)`; then `Exit(ctx 10 s)`;
  error → 504 `cc_exit_timeout` (`{step: "interrupt"|"exit"}`).
- Re-sample generation after exit → mismatch → 409 `tmux_instance_mismatch`
  `{after_exit: true, rolled_back: false, session_id}`: **no rollback** —
  a changed generation means a different tmux server; sending a resume
  command into it is not the pane we exited (spec §4.4 step 6 amended to
  state this; the SPA shows the session id so the user can resume by hand).
- Delegate via `m.sys.service.Delegate` with `Request{PrincipalID:
  m.principal(r), Provider: "claude", Brief: "(handed off from tmux session
  <name>)", SandboxProfile: profile ?? "handoff", Mounts: [{Path: cwd,
  Role: "cwd", Writable: true}], ResumeSessionID: sid, Labels: {source:
  purdex, handoff_session: code}, Origin: "purdex://host/<hostID>/session/<code>"}`.
- `Result.State != Rejected` → 200 `{execution_id, state, effective_profile,
  session_id, cwd}`.
- Rejected or `Delegate` error → rollback: if `rollback_command != ""`,
  `SendKeysIfInstance(TmuxID, expected, strings.ReplaceAll(cmd, "{id}",
  sid) + "\n")` (a send error or `!sent` → `rolled_back: false`), then
  poll `IsAliveFor("cc")` ≤ 15 s; respond 409 `delegate_rejected`
  `{reject_reason, rolled_back bool, session_id}` — for a `Delegate` infra
  error `reject_reason` is the error text and the response additionally
  carries `infra_error: true` (same status/code, spec §4.4 step 6).
- Tests (fake session provider / owner resolver / prober over
  `tmux.FakeExecutor` / `fakeCCOperator` / fake service; `httptest`):
  lock 409 (second concurrent request while the first is parked in `Exit`);
  400 bad instance; 404 session missing; generation mismatch before
  identity → nothing called on the operator; `no_identity` → operator never
  called; `no_cc`; generation mismatch after liveness but before
  interrupt → operator never called; busy → interrupt then exit ordering
  (call log); **interrupt timeout** → 504 `{step: interrupt}`, no exit, no
  delegate; **exit timeout** → 504 `{step: exit}`, no delegate; generation
  mismatch after exit → 409 `after_exit`, no delegate, no keys sent;
  success body and the exact `Request` the fake service received (labels,
  origin, profile default `handoff`, mount); rejected with rollback → keys
  sent contain the substituted id + newline, `rolled_back: true` when the
  prober flips alive, `false` on timeout; **rollback send error / `!sent`**
  → `rolled_back: false`; rejected without rollback command →
  `rolled_back: false`, no keys; **`Delegate` infra error** → 409
  `delegate_rejected` with `infra_error: true` and rollback attempted; 503
  when service nil; 409 `handoff_unsupported` when policy max is `trusted`;
  capabilities pin test (`delegate.resume_session_id === true` through the
  real handler).
- Commit: `feat(nex): POST /api/sessions/{code}/nex-handoff — hand an interactive CC session to the engine (P-C.3a task 3)`.

## Task 4 — `POST /api/sessions/{code}/nex-takeback`

Files: `handoff.go` + test (same lock).

- Body `{expected_tmux_instance, execution_id, resume_command, lease_id?}`;
  400 on missing `execution_id`/`resume_command` or bad instance.
- `TryLock(code)` → 409 `handoff_in_progress`.
- Preflight in this order, **before touching the execution**: `GetSession`
  nil → 404 `session_missing`; generation mismatch → 409
  `tmux_instance_mismatch`; `IsAliveFor("cc", target)` → 409
  `cc_already_running`.
- `store.Get(execution_id)`: `store.ErrNotFound` (measure the exact
  sentinel in `store/execution.go`) → 404 `execution_not_found`; any other
  error → 500 `store_error`. If `State == Running`: lease = given
  `lease_id` (validated by Nexen at interrupt) or `AcquireLease(principal)`
  — **an acquired lease is released by `defer` on every exit path** from
  this point (unconfirmed, orphaned, store error, context error, success);
  a caller-provided lease is never released. `ErrLeaseHeld` → 409
  `held_by` `{principal: exec.LeasePrincipalID}`; `Interrupt` —
  `ErrNoLiveTurn` is fine; `ErrInterruptUnconfirmed` → 504
  `interrupt_unconfirmed` (nothing else done); any other error → 500
  `interrupt_failed`. Re-`Get` (error → 500 `store_error`); state ∉ {Idle,
  Failed, Terminated} → 409 `execution_not_settled` `{state}`.
- `sid = exec.SessionID`, fallback `exec.ResumeSessionID`; empty → 409
  `no_session_id`.
- `SendKeysIfInstance(TmuxID, expected, replace(resume_command, "{id}", sid)
  + "\n")`; `!sent` → 409 `tmux_instance_mismatch`; poll `IsAliveFor("cc")`
  ≤ 15 s else 504 `cc_start_timeout` `{session_id}` (execution already
  settled; SPA offers retry).
- `Archive{Archived: true}`; error logged → `archived: false`. 200
  `{session_id, archived}`.
- Tests: preflight order (session missing → service never called;
  generation mismatch → never called; cc alive → never called); `Get`
  not-found → 404 vs other error → 500; running → acquire → interrupt →
  release call log; caller lease → no acquire/release; `held_by`;
  `ErrNoLiveTurn` tolerated; unconfirmed → 504 + acquired lease released;
  **generic interrupt error → 500 + acquired lease released**; re-`Get`
  error → 500 + lease released; not settled → 409; `session_id` preferred
  over `resume_session_id`; keys sent with substituted id;
  `SendKeysIfInstance` returns error → 500 `send_failed`, **no archive**;
  `!sent` → 409, no archive; cc never appears → 504 `cc_start_timeout`,
  **no archive**; archive success / archive failure → `archived: false`
  still 200.
- Commit: `feat(nex): POST /api/sessions/{code}/nex-takeback — resume the session in its tmux pane and archive the execution (P-C.3a task 4)`.

## Task 5 — PR

`go build ./... && go vet ./... && go test ./...` + `gofmt`; PR
"feat(nex): daemon handoff endpoints — hand to nex / take back (P-C.3a)";
codex R1 (`--model gpt-5.6-sol`), R2 attack → critic. Real-machine
acceptance for this PR is CLI-level only (spec §6 items 3–6 need the SPA):
`make build`, restart mlab daemon (`pdx stop`/`pdx start`), then `curl`
the two endpoints against a scratch tmux session running `claude` (token
in a variable) and observe `pdx nex show`. Deploy note: this PR changes
the daemon — mlab needs `make build` + restart; air26 stays behind until
its App updates.

## Review log

- codex `task-mu6om8rv-beojrl` (gpt-5.6-sol), 9 findings, all applied: (1)
  second generation sample moved after the liveness check; (2) after-exit
  generation change → no rollback, spec amended with the rationale; (3)
  resume-support precondition pinned by a capabilities test through the
  real handler; (4) delegate infra error → 409 `delegate_rejected` +
  rollback (`infra_error: true`); (5) acquired lease released by `defer` on
  every exit path; (6) `store.ErrNotFound` → 404, other → 500; (7) test
  matrix extended (interrupt vs exit timeout, rollback send error, infra
  error rollback, store errors, no-archive on every take-back failure);
  (8) `go test ./...` after every task commit; (9) spec §6 item references
  fixed.
