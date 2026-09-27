# tmux "no socket" is "no server" (#1473) — plan

Spec: `docs/superpowers/specs/2026-09-27-tmux-no-socket-design.md`.
One PR, daemon only. TDD per task (test first, see it fail, implement, see it
pass), one commit per task. From the worktree root:
`go test ./internal/tmux/... ./internal/module/session/... ./internal/module/monitor/... ./cmd/pdx/...`.

Plan review (codex, 2026-09-27) findings folded in: self-healing hooks (1),
fake reset/failure injection (2, 7), `os.Stat` not `Lstat` (3), absolute paths
only (4), inventory of fold-all sites (5), per-site negative tests (6),
malformed/EACCES classifier cases (8).

## T1 — `tmux.IsNoServer` classifier (spec D1)

- New `internal/tmux/noserver.go` + `internal/tmux/noserver_test.go`.
- Test first — table, paths built under `t.TempDir()`:
  1. `no server running on /tmp/x` → true
  2. `error connecting to <abs missing> (No such file or directory)` → true
  3. `error connecting to <abs missing> (Aucun fichier ou dossier de ce type)` → true (stat)
  4. `error connecting to <abs existing file> (No such file or directory)` → true (reason)
  5. `error connecting to <abs existing file> (Permission denied)` → false
  6. dangling symlink (`os.Symlink(<abs missing>, <link>)`) + localised reason → true
  7. `error connecting to rel/missing (Aucun fichier…)` → false (relative, never stat'ed)
  8. EACCES: dir `d` with mode 0o000 (restore 0o700 in `t.Cleanup`), path `d/sock`,
     localised reason → false. Skip when `os.Geteuid()==0`.
  9. path containing ` (`: `<tmp>/a (b)/missing (Aucun…)` where `a (b)` exists → true
  10. `error connecting to <abs missing> (No such file or directory` (no trailing `)`) → false
  11. `error connecting to <abs missing>` (no ` (`) → false
  12. two lines, the matching one second → true
  13. `something else` → false; `""` → false
- Implement per spec D1 (scan lines; trim `\r`; phrase check; structural parse;
  reason equality; absolute-only `os.Stat` + `errors.Is(err, fs.ErrNotExist)`).

## T2 — executor sites (spec D2)

- `internal/tmux/executor.go`: `ListSessions`, `HasPane`, `ShowWindowOption`,
  `ShowGlobalOption`, `ShowHooksGlobal`. `ListSessions` keeps `readCtxErr`
  first, then inspects `*exec.ExitError.Stderr` only (plus `no sessions`).
- Tests first in `internal/tmux/executor_test.go`, existing fake-`tmux`-on-PATH
  pattern. A helper writes a script that prints a given stderr and exits 1.
  For **each** of the five methods, two tests:
  - absent-socket stderr (`error connecting to <abs missing tmp path> (No such file or directory)`)
    → benign result (`nil,nil` / `false,nil` / `"",nil`)
  - `error connecting to <abs existing tmp file> (Permission denied)` → non-nil error
  Plus: `HasSessionContext` with absent-socket stderr → `(false, nil)` (locks existing behaviour).
- Existing `no server running` tests stay green.
- Mutation (record result for the PR body): temporarily revert `ListSessions`
  to the old substring checks → its new absent-socket test fails; restore.

## T3 — monitor pane lister

- `internal/module/monitor/tmux_panes.go`: `isNoTmuxPanesOutput(out)` →
  `tmux.IsNoServer(out) || strings.Contains(out, "no sessions")` (import `internal/tmux`;
  check for import cycles with `go build ./...`).
- Test first in `process_test.go` beside the loop at line 72: absent-socket
  output with a missing abs temp path → `ListPanes` `nil, nil`; `Permission denied`
  on an existing path → error.

## T4 — selftest cleanup classifier

- `cmd/pdx/msg_selftest.go` `selftestTmuxNoSession`: add `tmux.IsNoServer(msg)`.
- Test first (existing test file for it, or `msg_selftest_test.go`): build a
  real `*exec.ExitError` by running `sh -c 'printf "error connecting to <abs missing> (No such file or directory)\n" >&2; exit 1'`
  via `exec.Command(...).Output()` → `selftestTmuxNoSession(err)` true.

## T5 — self-healing hooks (spec D3)

- `internal/tmux/fake_executor.go`: `SetHookGlobal` records the event under the
  mutex and returns an injectable error; add `HookSets() []string` (copy),
  `ResetHookSets()`, `SetHookGlobalError(err error)`.
- `internal/module/session/watcher.go` / `hooks.go` / `module.go`: per spec D3
  (`hooksOK`, `hooksInstance` in `watcherState` under its mutex; `ensureHooks`;
  `Start()` seeds `hooksOK`; `tickTmuxDown` alive edge forces false then
  `ensureHooks("")` before the existing ok/wait-for/broadcast sequence;
  `tickNormal` calls `ensureHooks(payloadInstance(sessions))` after a successful
  list and clears `hooksOK` on the alive → down edge). Failure log once per
  failure streak.
- Tests first in `watcher_test.go` (follow `TestWatcherRecoverFromTmuxDown`):
  a. down → alive: `HookSets()` == created, closed, renamed (after `ResetHookSets()` post-Start)
  b. staying down: no hook calls
  c. alive edge with `SetHookGlobalError` set: `tmux: ok` still broadcast and
     sessions broadcast still happens; then clear the error, one `tickNormal`
     → hooks installed; another `tickNormal` → no further calls
  d. installed on instance A; `tickNormal` with a non-empty payload on instance B → reinstalled
  e. empty payload (`payloadInstance == ""`) after a successful install → no reinstall
- Mutation: remove the `tickNormal` `ensureHooks` call → test c fails; restore.

## Verification before PR

- `go test ./...` and `go vet ./...` from the worktree root.
- `make build BIN=<scratch path>` succeeds (never `go build ./cmd/pdx` into the repo root).

## Deploy (after merge + bump)

- mlab: main checkout `make build BIN=bin/pdx.new` → backup
  `~/.config/pdx/bin/pdx-mlab-<prev>` → `./bin/pdx stop` → `rm bin/pdx; mv bin/pdx.new bin/pdx`
  → `env PDX_DEV_MODE=1 ./bin/pdx start` → `/api/health`.
- air26: `scp bin/pdx air26:.config/pdx/bin/pdx.new` → backup `pdx.<prev>` →
  `./pdx stop; rm pdx; mv pdx.new pdx; ./pdx start`.
- Real-host acceptance on air26 per spec §Acceptance. air26 has the user's
  live `test` session; the kill-server step needs the user's OK first.
