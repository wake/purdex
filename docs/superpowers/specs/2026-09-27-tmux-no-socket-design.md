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
- A line of the form `error connecting to <path> (<reason>)` — the line must
  end with `)`; `<path>` is the text between `error connecting to ` and the
  **last** ` (` on that line (paths may contain ` (`); a line without that
  structure is not a match:
  - true when `<reason>` is exactly `No such file or directory`.
  - true when `<path>` is **absolute** and `os.Stat(<path>)` (follows symlinks,
    so a dangling symlink counts as absent) returns an error satisfying
    `errors.Is(err, fs.ErrNotExist)`. This covers a localised `strerror`.
    Relative paths are never stat'ed — the daemon's cwd is not tmux's
    resolution base — and fall back to the reason check alone. Any other stat
    error (e.g. `EACCES`) → not a match.
  - false otherwise (e.g. `Permission denied`, `File name too long`,
    `Connection refused` on a non-socket) — these are real faults and must stay
    errors.
- Anything else → false.

`IsNoServer` is pure apart from the one `Stat`; it never runs tmux. The daemon
never passes `-S`, so in practice the path is tmux's default absolute socket.

### D2. Call sites switched to `IsNoServer`

| Site | Today | After |
|---|---|---|
| `tmux/executor.go` `ListSessions` | matches `no server running` / `no sessions` | `IsNoServer(stderr)` or `no sessions` → `nil, nil` |
| `tmux/executor.go` `HasPane` | `no server running` → `(false, nil)` | `IsNoServer` |
| `tmux/executor.go` `ShowWindowOption`, `ShowGlobalOption` | `no server running` → `"", nil` | `IsNoServer` |
| `tmux/executor.go` `ShowHooksGlobal` | `no hooks` / `no server running` → `"", nil` | `no hooks` or `IsNoServer` |
| `module/monitor/tmux_panes.go` `isNoTmuxPanesOutput` | `no server running` / `no sessions` on combined output | `IsNoServer(out)` or `no sessions` |
| `cmd/pdx/msg_selftest.go` `selftestTmuxNoSession` | list incl. `no server running` | add `IsNoServer(msg)` |

Already correct for the absent socket, unchanged, but locked by a test:
`HasSession` (any error → false), `HasSessionContext` (any non-ctx error →
`(false, nil)`), `KillSession` (any error → `ErrNoSession`). They fold every
failure, so they need no classifier.

`ListSessions` today checks `err.Error()` and stdout for the phrase as well as
`ExitError.Stderr`; only stderr can carry it (`Output()` captures stderr into
`ExitError.Stderr` when `cmd.Stderr` is nil), so the new code checks
`ExitError.Stderr` only. The ctx-deadline branch (`readCtxErr`) stays first.

### D3. Self-healing hook installation

Global hooks live in the server's memory: a daemon that started without a
server never installs them, and a `kill-server` + new server drops them. A
single install attempt on the down → alive edge is not enough (the server can
vanish between the probe and `set-hook`, or one of the three can fail), so the
watcher tracks whether the hooks are known to be on the current server and
retries until they are.

- `watcherState` gains `hooksOK bool` and `hooksInstance string` (the tmux
  instance the hooks were installed on, `""` if unknown).
- `ensureHooks(instance string)`: if `hooksOK` and (`instance == ""` or
  `instance == hooksInstance`) → nothing. Otherwise call `installTmuxHooks()`;
  on success `hooksOK = true, hooksInstance = instance`; on failure
  `hooksOK = false` and log (rate: once per failure streak, not every tick).
  `installTmuxHooks` sets all three every time (`set-hook -g` overwrites
  index 0), so a retry also repairs a partial install.
- `Start()`: the existing install sets `hooksOK` from its result
  (`hooksInstance = ""`).
- `tickTmuxDown`, when `TmuxAlive()` turns true: `ensureHooks("")` with
  `hooksOK` forced false first, then the existing `tmux: ok` broadcast,
  wait-for resume and sessions broadcast — in that order, and a hook failure
  never skips them.
- `tickNormal`, after a successful list: `ensureHooks(payloadInstance(sessions))`.
  This retries a failed install every 5 s and reinstalls when a non-empty
  payload shows the server generation changed since the install (a restart
  between two ticks that the down/alive edge never saw).
- Alive → down transition in `tickNormal`: `hooksOK = false`.

## Acceptance

- Unit: `IsNoServer` table — stale form; absent form with a missing absolute
  path; localised reason + missing absolute path (true by stat); ENOENT reason
  + existing path (true by reason); dangling symlink + localised reason (true);
  `Permission denied` on an existing path (false); relative missing path +
  localised reason (false, never stat'ed); stat failing with `EACCES` +
  localised reason (false); path containing ` (`; missing trailing `)` / no
  ` (` delimiter (false); matching line not first (true); unrelated text and
  empty (false).
- Unit (fake `tmux` on PATH, existing pattern in `executor_test.go`): **each**
  `RealExecutor` site in D2 returns the benign result for the absent-socket
  stderr **and** still errors on `Permission denied` for an existing path;
  `HasSessionContext` absent-socket → `(false, nil)` is locked.
- Unit: monitor `ListPanes` with the absent-socket combined output → `nil, nil`.
- Unit (fake executor with hook-call recording, reset and failure injection):
  down → alive installs all three hooks; a failing install still emits
  `tmux: ok`, resumes wait-for and broadcasts sessions, and the next
  `tickNormal` retries until it succeeds; a successful install is not repeated
  on later ticks with the same instance; a changed non-empty instance
  reinstalls; staying down installs nothing.
- Mutation: reverting the classifier to the old substring makes the new
  `ListSessions` test fail; removing the `tickNormal` retry makes the retry
  test fail.
- Real host: on air26 after deploy, `tmux kill-server; rm -f
  /private/tmp/tmux-501/default` → daemon log shows no `list-sessions: exit
  status 1`; `GET /api/sessions?fresh=1` returns `sessions: []`; after
  `tmux new -d -s x`, `tmux show-hooks -g` lists the 3 purdex hooks without a
  daemon restart.
