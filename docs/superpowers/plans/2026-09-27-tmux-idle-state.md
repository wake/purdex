# "No tmux server" is a usable host (#1108, #1474) — plan

Spec: `docs/superpowers/specs/2026-09-27-tmux-idle-state-design.md`.
One PR (daemon + a small SPA change; no wire change, so no deploy ordering).
TDD per task: test first, see it fail, implement, see it pass. One commit per task.

Plan review (codex, 2026-09-27) folded in: no new wire value because of the
Electron bundled renderer (1, 7); D3 ordering mutex (3); D4 race accepted as
self-healing and invisible to the SPA (2); stall message scoped to
not-yet-attached terminals (4); stall test covers the reconnect loop (6);
launcher race disappears with (1) (5); `tmux info` deadline test (7).

## T1 — `tmux.ServerState` (D1)
- `internal/tmux/`: type + constants, `Executor` interface method,
  `RealExecutor.ServerState()`, `TmuxAlive()` = `ServerState() == ServerUp`.
  Make the 5 s deadline a package var so a test can shorten it.
- `FakeExecutor`: state field; `SetAlive(true/false)` → Up/Absent; `SetServerState`.
- Tests first, fake-`tmux`-on-PATH pattern (`executor_test.go`): exit 0 → Up;
  `no server running on …` → Absent; `error connecting to <abs missing> (No such file or directory)` → Absent;
  `something else` → Broken; PATH = empty temp dir → Broken; script `sleep 5`
  with the deadline shortened → Broken.

## T2 — reported value, subscribe frame (D2, D3)
- `internal/module/session/watcher.go`, `module.go`: `broken` in watcherState;
  probes record it; a change broadcasts `ok`/`unavailable` under `statusMu`;
  `OnSubscribe` callback queues the current reported value under `statusMu`
  (build the frame exactly as `Events.Broadcast("", "tmux", v)` does).
- Internal up/down logic, wait-for gate and hooks unchanged.
- Tests first (`watcher_test.go` + module test using the existing harness for
  subscribers): the D2 transition table from spec §Acceptance; subscribe frame
  per state; concurrent state change + subscribe under `-race` — the
  subscriber's last `tmux` frame equals the final reported value.
- Mutation: treat Absent as broken → a D2 test fails; restore.

## T3 — create runs recovery now (D4)
- Factor today's `tickTmuxDown` recovery into `markServerUp()` guarded by the
  `setTmuxAlive(true)` flip; `tickTmuxDown` calls it on Up.
- `create.go`: after the confirmed list, `if !m.wstate.getTmuxAlive() { m.markServerUp() }`.
- Tests first (`create_test.go` / `watcher_test.go`): watcher down, fake create
  makes the server Up → after create returns, hooks were installed and a
  `sessions` frame broadcast, with no tick called; two goroutines calling
  `markServerUp` → one recovery (hook install count 3, one `sessions` broadcast), `-race`.

## T4 — SPA attach-stall message (S1, S2)
- New `spa/src/hooks/useAttachStall.ts` + test. `SessionPaneContent` calls it
  unconditionally (before early returns) and passes
  `connectingMessage={stalled ? t('session.attach_stalled') : undefined}`.
- i18n `session.attach_stalled`: zh-TW 「已連線到主機，但讀不到 tmux session 清單，會自動重試」;
  en "Connected to the host, but the tmux session list can't be read. Retrying automatically."
- Tests first with `vi.useFakeTimers()` per spec §Acceptance, including the
  status-flapping loop. `SessionPaneContent` test: stalled + not attached → text shown.
- From `spa/`: `npx vitest run <files>`; at the end `pnpm run lint`,
  `npx tsc --noEmit -p tsconfig.app.json`, `npx vitest run`.

## Verification before PR
- `go vet ./...`, `go test ./...`, `go test -race ./internal/module/session/... ./internal/tmux/...`,
  `make build BIN=<scratch>/pdx-check`; SPA checks above.

## Deploy
- merge → bump → main checkout pull (SPA dev server) → daemon mlab + air26
  (recipes as in the #1473 plan) → air26 acceptance per spec.
