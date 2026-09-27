# tmux "no socket" is "no server" (#1473) — plan

Spec: `docs/superpowers/specs/2026-09-27-tmux-no-socket-design.md`.
One PR, daemon only. TDD per task, one commit per task.
Run from the worktree root; `go test ./internal/tmux/... ./internal/module/session/... ./internal/module/monitor/... ./cmd/pdx/...`.

## T1 — `tmux.IsNoServer` classifier

- New file `internal/tmux/noserver.go` + `internal/tmux/noserver_test.go`.
- Test first (table, uses `t.TempDir()` for existing / non-existing paths):
  1. `no server running on /tmp/x` → true
  2. `error connecting to <tmp>/missing (No such file or directory)` → true
  3. `error connecting to <tmp>/missing (Aucun fichier ou dossier de ce type)` (localised reason, path absent) → true
  4. `error connecting to <tmp>/exists (No such file or directory)` (path exists) → true (by reason)
  5. `error connecting to <tmp>/exists (Permission denied)` → false
  6. `error connecting to <tmp>/a (b)/missing (No such file or directory)` → true, and path parsing uses the last ` (` (assert via case with `(b)` dir existing but `missing` absent and localised reason → true)
  7. `something else` → false; `""` → false
  8. multi-line stderr where the matching line is not the first → true
- Implement: scan lines; phrase check; for `error connecting to ` lines, cut the
  path at the last ` (` and require the line to end with `)`; `os.Lstat` →
  `errors.Is(err, fs.ErrNotExist)` → true; reason == `No such file or directory` → true.

## T2 — executor sites use `IsNoServer`

- `internal/tmux/executor.go`: `ListSessions`, `HasPane`, `ShowWindowOption`,
  `ShowGlobalOption`, `ShowHooksGlobal` (spec D2 table). `ListSessions` keeps
  `readCtxErr` first, then inspects `*exec.ExitError` `Stderr` only.
- Tests first in `internal/tmux/executor_test.go` using the existing fake-`tmux`-on-PATH
  pattern (script prints `error connecting to <nonexistent tmp path> (No such file or directory)` to stderr, exit 1):
  - `ListSessions` → `nil, nil`
  - `HasPane("%5")` → `false, nil`
  - `ShowWindowOption`, `ShowGlobalOption` → `"", nil`
  - `ShowHooksGlobal` → `"", nil`
  - `ListSessions` with `error connecting to <existing file> (Permission denied)` → error
- Existing `no server running` tests must stay green.
- Mutation (record in PR body): swap `IsNoServer` back to the old substring in
  `ListSessions` → the new `ListSessions` test fails.

## T3 — monitor pane lister

- `internal/module/monitor/tmux_panes.go`: `isNoTmuxPanesOutput(out)` →
  `tmux.IsNoServer(out) || strings.Contains(out, "no sessions")`.
- Test first in `process_test.go` next to the existing loop at line 72: add the
  absent-socket output (nonexistent temp path) → `ListPanes` returns `nil, nil`.

## T4 — selftest cleanup classifier

- `cmd/pdx/msg_selftest.go` `selftestTmuxNoSession`: add `tmux.IsNoServer(msg)`.
- Test first: find the existing test for `selftestTmuxNoSession` (or add one in
  `msg_selftest_test.go`) with an `*exec.ExitError` whose `Stderr` is the
  absent-socket form → true.

## T5 — reinstall hooks on down → alive

- `internal/tmux/fake_executor.go`: `SetHookGlobal` records `(event, command)`
  under the mutex; add `HookSets() []string` (events, in call order) accessor.
- `internal/module/session/watcher.go` `tickTmuxDown`: on alive, call
  `m.installTmuxHooks()` (log on error, continue) before
  `broadcastTmuxStatus("ok")`.
- Test first in `watcher_test.go` next to `TestWatcherRecoverFromTmuxDown`:
  module started with fake alive=false, clear recorded hook sets, `SetAlive(true)`,
  one `checkAndBroadcast()` → recorded events are exactly
  `session-created, session-closed, session-renamed`. And a tick while still
  down records nothing.

## Verification before PR

- `go test ./...` and `go vet ./...` from the worktree root.
- `make build` succeeds (do not `go build ./cmd/pdx` into the repo root).

## Deploy (after merge + bump)

- mlab: main checkout `make build BIN=bin/pdx.new` → backup
  `~/.config/pdx/bin/pdx-mlab-<prev>` → `./bin/pdx stop` → `rm bin/pdx; mv bin/pdx.new bin/pdx`
  → `env PDX_DEV_MODE=1 ./bin/pdx start` → `/api/health`.
- air26: `scp bin/pdx air26:.config/pdx/bin/pdx.new` → backup `pdx.<prev>` →
  `./pdx stop; rm pdx; mv pdx.new pdx; ./pdx start`.
- Real-host acceptance on air26 per spec §Acceptance. air26 has live sessions
  (`test`) created by the user; the kill-server step needs the user's OK first.
