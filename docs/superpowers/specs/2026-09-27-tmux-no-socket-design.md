# tmux "no socket" is "no server" (#1473) — design

## Problem

tmux reports "there is no server" in two different ways, depending on whether
the socket file exists (measured on mlab and air26, tmux 3.7c):

| State | Socket file | stderr of any client command | exit |
|---|---|---|---|
| server killed, socket left behind | present (stale) | `no server running on <path>` | 1 |
| after reboot (`/tmp` wiped) / never started | **absent** | `error connecting to <path> (No such file or directory)` | 1 |

(tmux `client.c`: ECONNREFUSED prints the first; any other connect errno, ENOENT
included, prints `error connecting to %s (%s)` with `strerror(errno)`.)

The daemon only recognises the first. So on a host that has just rebooted,
`RealExecutor.ListSessions` returns `tmux list-sessions: exit status 1`, every
versioned `sessions` push (subscribe snapshot, wait-for, ticker) fails, the SPA
never receives a `sessions` payload, the terminal attach gate never opens
(`reconcile-host.ts` `openAttachGate`), and panes bound to the host stay on
`connecting...` forever instead of being marked terminated. Observed on air26
2026-09-27 (daemon alpha.456): the subscribe-retry loop logged a failure every
7 s until a session was created.

A second consequence: `installTmuxHooks` runs only in `Start()`. When the
daemon starts with no server, `set-hook` fails and the hooks are never
installed, even after a server appears (air26: 0 purdex hooks; mlab: 3). The
5 s ticker still delivers every change, so this is latency, not correctness.

## Goals

1. Every place that treats "no server running" as a known, benign condition
   treats the absent-socket form the same way.
2. After a server appears (tmux down → alive transition), the session hooks are
   installed.

## Non-goals

- SPA: surfacing "host connected but no sessions payload" as an error instead
  of an endless `connecting...` — follow-up issue (UX decision needed).
- #1108 (create-session gates while tmux is down) — re-evaluated after this
  ships, as that issue's comment says.
- The daemon never broadcasting `tmux: unavailable` when it *starts* with tmux
  down (only on an alive → down transition). Noted in the follow-up.
- `KillSessionIfInstance` / `SendKeysIfInstance`: they already report any
  non-"can't find session" failure as an error meaning "unknown, nothing
  done"; no server is correctly in that bucket. Unchanged.
- Starting a tmux server at daemon boot (Daemon Boot Restore).

## Design

### D1. One classifier in `internal/tmux`

```go
// IsNoServer reports whether a tmux client's stderr says there is no server
// to talk to — either the socket is stale ("no server running on <path>") or
// it does not exist ("error connecting to <path> (No such file or directory)").
func IsNoServer(stderr string) bool
```

Rules:

- `no server running` substring → true (existing behaviour).
- A line of the form `error connecting to <path> (<reason>)`:
  - true when `<path>` does not exist on disk (`os.Lstat` → `fs.ErrNotExist`).
    This is authoritative and locale-independent: `strerror` text can be
    localised on some platforms, the file's absence cannot.
  - true when `<reason>` is `No such file or directory` (covers the race where
    the socket was created between the tmux call and the stat).
  - false otherwise (e.g. `Permission denied`, `File name too long`,
    `Connection refused` on a non-socket) — these are real faults and must stay
    errors.
  - `<path>` is the text between `error connecting to ` and the **last** ` (`
    on that line (paths may contain ` (`).
- Anything else → false.

`IsNoServer` is pure apart from the one `Lstat`; it never runs tmux.

### D2. Call sites switched to `IsNoServer`

| Site | Today | After |
|---|---|---|
| `tmux/executor.go` `ListSessions` | matches `no server running` / `no sessions` | `IsNoServer(stderr)` or `no sessions` → `nil, nil` |
| `tmux/executor.go` `HasPane` | `no server running` → `(false, nil)` | `IsNoServer` |
| `tmux/executor.go` `ShowWindowOption`, `ShowGlobalOption` | `no server running` → `"", nil` | `IsNoServer` |
| `tmux/executor.go` `ShowHooksGlobal` | `no hooks` / `no server running` → `"", nil` | `no hooks` or `IsNoServer` |
| `module/monitor/tmux_panes.go` `isNoTmuxPanesOutput` | `no server running` / `no sessions` on combined output | `IsNoServer(out)` or `no sessions` |
| `cmd/pdx/msg_selftest.go` `selftestTmuxNoSession` | list incl. `no server running` | add `IsNoServer(msg)` |

`ListSessions` today checks `err.Error()` and stdout for the phrase as well as
`ExitError.Stderr`; only stderr can carry it (`Output()` captures stderr into
`ExitError.Stderr` when `cmd.Stderr` is nil), so the new code checks
`ExitError.Stderr` only. The ctx-deadline branch (`readCtxErr`) stays first.

### D3. Install hooks on the down → alive transition

`tickTmuxDown`, when `TmuxAlive()` turns true, calls `installTmuxHooks()`
before broadcasting (failure is logged, as in `Start()`, and does not block the
broadcast). `installTmuxHooks` is idempotent (`set-hook -g` overwrites index 0),
so re-running it after a server restart that the daemon observed is harmless
and also covers a `kill-server` + new server cycle, which drops global hooks.

## Acceptance

- Unit: `IsNoServer` table — stale form, absent form with a path that does not
  exist, absent-socket reason but path exists (true by reason), `error
  connecting` with `Permission denied` on an existing path (false), path with
  ` (` inside, unrelated text (false), empty (false).
- Unit (fake `tmux` on PATH, existing pattern in `executor_test.go`): each
  `RealExecutor` site in D2 returns the benign result for the absent-socket
  stderr and still errors on an unrelated failure.
- Unit: monitor `ListPanes` with the absent-socket combined output → `nil, nil`.
- Unit: `tickTmuxDown` on a down → alive transition calls `SetHookGlobal` for
  all three events (fake executor records it).
- Mutation: reverting the classifier to the old substring makes the new
  `ListSessions` test fail.
- Real host: on air26 after deploy, `tmux kill-server; rm -f
  /private/tmp/tmux-501/default` → daemon log shows no `list-sessions: exit
  status 1`; `GET /api/sessions?fresh=1` returns `sessions: []`; after
  `tmux new -d -s x`, `tmux show-hooks -g` lists the 3 purdex hooks without a
  daemon restart.
