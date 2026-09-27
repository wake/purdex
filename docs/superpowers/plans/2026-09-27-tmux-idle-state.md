# "No tmux server" is an idle host (#1108, #1474) — plan

Spec: `docs/superpowers/specs/2026-09-27-tmux-idle-state-design.md`.
Two PRs, in order. PR-1 must be merged **and** live (SPA dev server on mlab
serves main) before PR-2's daemon is deployed. TDD per task, one commit per task.

## PR-1 — SPA (Phase 1)

Checks from `spa/`: `npx vitest run <files>`, then at the end `pnpm run lint`,
`npx tsc --noEmit -p tsconfig.app.json`, `npx vitest run`.

### T1 — `idle` in the store and the event mapping (S1)
- `spa/src/stores/useHostStore.ts`: `tmuxState?: 'ok' | 'idle' | 'unavailable'`.
- `spa/src/hooks/useMultiHostEventWs.ts:195-199`: map `ok`/`idle`, else `unavailable`.
- Tests first: find the existing test file for the `tmux` event handling in
  `useMultiHostEventWs` (grep `tmuxState` in `*.test.ts*`); add cases
  `idle` → `idle`, `ok` → `ok`, `unavailable` → `unavailable`, `bogus` → `unavailable`.

### T2 — readers render `idle` as normal; notification rule (S2)
- Code change only in `spa/src/hooks/useNotificationDispatcher.ts:317`:
  notify when `rt.tmuxState === 'unavailable'` and `prev.tmux` is `ok` or `idle`.
- Tests first (lock behaviour, most will pass already — that is expected and
  fine; the notification ones must fail first):
  - `useNotificationDispatcher`: ok→idle no notification; idle→unavailable
    notifies; ok→unavailable still notifies; idle→ok no notification.
  - `connectionErrorMessage(idle)` → null; `isHostLive` true for idle.
  - `SessionSection`: create button enabled with `tmuxState: 'idle'`.
  - `SessionsSection`: not offline with `idle`.
  - `HostSidebar` `StatusIcon`, `StatusBar` status segment, `OverviewSection`
    status: green / no `hosts.error_tmux_down` text with `idle`.
  Use existing test files for each component (grep); add to them.

### T3 — attach-stall hook and pane message (S3, S4)
- New `spa/src/hooks/useAttachStall.ts` (+ `.test.ts`): `useAttachStall(hostId: string, ms = 10_000): boolean`.
  Stalled = gate closed (`runtime[hostId]?.attachReady !== true`) and
  `runtime[hostId]?.daemonState === 'connected'`, continuously for `ms`.
  Empty `hostId` → never stalled. Timer resets when either condition breaks;
  cleared on unmount.
- `spa/src/components/SessionPaneContent.tsx`: `const stalled = useAttachStall(hostId)`;
  pass `connectingMessage={stalled ? t('session.attach_stalled') : undefined}` to `TerminalView`.
  (Hook called unconditionally before the early returns — rules of hooks.)
- i18n `session.attach_stalled`: zh-TW 「已連線到主機，但讀不到 tmux session 清單，會自動重試」;
  en "Connected to the host, but the tmux session list can't be read. Retrying automatically."
- Tests first with `vi.useFakeTimers()`: 9.9 s → false; 10 s → true; gate
  opens → false immediately; daemon `unreachable` → false and timer restarts;
  gate closes again → needs a fresh 10 s. `SessionPaneContent` test: stalled
  state renders the message text (find the existing test file).

## PR-2 — daemon (Phase 2)

Checks: `go test ./internal/tmux/... ./internal/module/session/... ./internal/core/...`,
at the end `go vet ./...`, `go test ./...`, `go test -race ./internal/module/session/...`,
`make build BIN=<scratch>/pdx-check`.

### T4 — `tmux.ServerState` (D1)
- `internal/tmux/executor.go` (or new `server_state.go`): type + constants,
  `Executor` interface method, `RealExecutor.ServerState()`; `TmuxAlive()` =
  `ServerState() == ServerUp`. `FakeExecutor`: state field, `SetAlive` maps,
  `SetServerState`.
- Tests first (fake tmux on PATH pattern from `executor_test.go`): exit 0 →
  Up; stale stderr → Absent; absent-socket stderr → Absent; `some other error`
  → Broken; PATH without tmux (`t.Setenv("PATH", t.TempDir())`) → Broken.

### T5 — watcher down kind, subscribe frame, guarded recovery (D2, D3, D4)
- `internal/module/session/watcher.go`, `module.go`.
- Tests first in `watcher_test.go` (+ module test for subscribe):
  - Start with Absent → a new subscriber's first `tmux` frame is `idle`;
    with Broken → `unavailable`; with Up → `ok`. (Subscribe via
    `core.Events` in the existing test harness; inspect queued frames.)
  - ok → (Absent) tick broadcasts `idle`; ok → (Broken) broadcasts `unavailable`;
    idle → Broken while down broadcasts `unavailable`; repeated ticks in the
    same kind broadcast nothing.
  - `markServerUp` called concurrently from two goroutines → exactly one `ok`
    broadcast and one hooks install (`-race`).
- Mutation: map `ServerAbsent` → `unavailable` → a transition test fails.

### T6 — create brings the watcher up (D5)
- `internal/module/session/create.go`: after the successful list/confirm,
  `if !m.wstate.getTmuxAlive() { m.markServerUp() }`.
- Test first (`create_test.go`): watcher down (fake Absent, then the fake's
  create makes it Up) → after create returns, an `ok` frame and a `sessions`
  frame were broadcast without calling any tick.

## Deploy

1. Merge PR-1 → bump → main checkout pull (SPA dev server picks it up; Air .app
   loads it). Verify nothing changes with the current daemon.
2. Merge PR-2 → bump → deploy daemon mlab + air26 (recipes as in #1473 plan).
3. air26 acceptance per spec (kill-server + rm socket; host green, create works).
