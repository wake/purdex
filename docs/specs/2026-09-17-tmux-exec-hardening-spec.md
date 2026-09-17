# tmux exec hardening: the daemon must not depend on how it was started

2026-09-17 · purdex

## 1. Two hazards, one cause

Both were found while diagnosing #1108. Neither causes a failure on either
machine today; both are latent and both bite in environments purdex is headed
for. The cause is the same: **the daemon execs `tmux` with whatever environment
it happened to inherit, and never states what it resolved.**

### 1.1 `tmux` is looked up by bare name

Every daemon-side invocation is `exec.Command("tmux", …)` — 29 in
`internal/tmux/executor.go` plus ~6 more across `session/watcher.go`,
`core/info_handler.go`, `tmux/send_keys_conditional.go`,
`session/service.go` (the attach relay), `monitor/tmux_panes.go`,
`config/hostid.go` and `codexbroker/module.go`. None calls `exec.LookPath`.

A LaunchAgent gets `PATH=/usr/bin:/bin:/usr/sbin:/sbin` — verified on this
machine from the `com.wake.ploom` job — and **macOS ships no `/usr/bin/tmux`**;
it lives in `/opt/homebrew/bin`. Every one of those ~35 sites would then fail
with `exec: "tmux": executable file not found in $PATH`, separately, at
whatever moment each happens to run.

pdx is not a LaunchAgent on either machine yet (mlab: started from a shell;
air-2026: started by the app, which propagates a full PATH), which is why this
has not bitten. `kickoff_daemon_boot_restore` would change that.

### 1.2 The daemon inherits `$TMUX`

The running daemon on mlab carries `TMUX=/private/tmp/tmux-501/default,6901,45`
and `TMUX_PANE=%52`, inherited from the pane `pdx start` was typed in.

`tmux.c`'s `main` uses `$TMUX` as the socket path when neither `-L` nor `-S` is
given:

```c
	if (path == NULL && label == NULL) {
		s = getenv("TMUX");
		if (s != NULL && *s != '\0' && *s != ',') {
			path = xstrdup(s);
			path[strcspn(path, ",")] = '\0';
		}
	}
```

Today that value equals the default socket, so it is invisible. Start the
daemon from a pane on a `-L other` socket — or with a stale `$TMUX` — and every
tmux call silently addresses a different server. The symptom is the worst kind:
the daemon reports success and the user sees nothing.

Nothing on the **daemon's serve path** reads `TMUX` or `TMUX_PANE`. One place
in the repo does: `cmd/pdx/hook.go:127`'s `queryTmuxPaneID()` returns
`os.Getenv("TMUX_PANE")`. That is the `pdx hook` subcommand, which runs inside
a pane as a tmux hook and is asking *which pane am I*; it is a separate process
that never calls `Prepare`, so it keeps what it inherits. That separation is
the reason this fix belongs in the process environment and not in
`RealExecutor`.

## 2. The change

A new package `internal/tmuxenv`, called once at daemon start, that fixes the
**process** environment. Because `exec.Command` inherits it, all ~35 call sites
are covered without editing any of them.

This mirrors `internal/locale`, which already exists for exactly this shape of
problem ("guarantees the daemon process exports a UTF-8 character locale before
it execs tmux") and is called from the same place.

### 2.1 `tmuxenv.Prepare() Result`

**(a) Make `tmux` reachable.** `exec.LookPath("tmux")`. On success, nothing is
changed — the inherited PATH already works. On failure, probe a fixed list of
well-known locations and, if one holds an executable `tmux`, **append that
directory to `PATH`** rather than remembering an absolute path:

- it covers every call site with no edits, and
- the tmux server the daemon spawns inherits the same PATH, so its panes can
  find tmux too.

Probe order: `/opt/homebrew/bin`, `/usr/local/bin`, `$HOME/.local/bin`. A fixed
list, not a config knob: it exists to find one specific binary, not to express
policy — `config.NexConfig.PathPrepend` is the knob and answers a different
question.

The order deliberately differs from `DefaultNexConfig().PathPrepend`, which
leads with `~/.local/bin`. That list is about user-installed tooling in
general; this one is about where **tmux** actually is, and on both machines in
this tailnet that is Homebrew. Leading with `~/.local/bin` would let a stray
personal shim outrank the real install.

#### 🔴 Appended, never prepended

`PATH` is process-wide, so whatever is added is inherited by **every** later
exec, not just tmux — the dev module's build tooling
(`module/dev/module.go:202`, `module/dev/build.go:113`), the terminal relay
(`terminal/relay.go:50`), agent and execution spawns — and, through the tmux
server, the panes themselves.

That last one decides the direction. Panes are sent **bare `pdx relay …`**
(`module/execution/launcher.go:166`, `module/stream/orchestrator.go:120`), so a
directory that won precedence could hand them a different `pdx` than the one
the daemon is running — a version mismatch that surfaces as an agent timeout,
nowhere near tmux. In the LaunchAgent case this is not hypothetical: the daemon
may be `~/.config/pdx/bin/pdx` while something unrelated sits in
`/opt/homebrew/bin`.

**So the directory is appended.** Appending cannot change the rank of anything,
because this branch is only reached once `LookPath("tmux")` has already failed:
nothing resolvable today moves, and tmux — resolvable nowhere — is found either
way. In the `Found` case (every host today) `PATH` is not touched at all.

#### Version-mismatch risk

A probed `tmux` need not be the one the operator's shell uses. Two different
tmux builds talking to one server produce a protocol version mismatch. The
mitigation is diagnostic rather than preventive: the `Appended` log names the
exact binary, so a mismatch is one line away from being explained instead of
being a mystery. (Prevention would mean resolving the user's login-shell PATH,
which is what `docs/specs/2026-09-14-local-daemon-install-spec.md` already does
for the app-installed daemon — the better long-term answer, and out of scope
here.)

**(b) Take the socket back.** `os.Unsetenv("TMUX")` and
`os.Unsetenv("TMUX_PANE")`, unconditionally. The daemon addresses the default
socket for its UID, never whichever server the operator's shell was attached
to.

`Result.DroppedTMUX` is keyed on `TMUX` alone, not on either variable. `TMUX`
is the one that changes behaviour — it is what `tmux.c` reads as a socket path.
`TMUX_PANE` is cleared alongside it because leaving half a pane identity behind
is incoherent, but its presence is not separately reported: the log line exists
to say "this daemon was addressing someone else's server", and that is a
statement about `TMUX`.

**Behaviour change worth stating plainly:** a daemon started from a pane on a
non-default socket (`tmux -L other`) currently manages *that* server. After
this change it manages the default one. The terminal attach relay is affected
too — `session/service.go:179` builds `terminal.NewRelay("tmux", …)` and
`terminal/relay.go:52` inherits the daemon environment — which is the point:
attach should reach the server the rest of the daemon is talking about. Nobody
is known to run purdex against a named socket, and there is no config for it,
so this converts an undocumented accident into a stated rule.

### 2.2 Result and logging

`Result` reports enough for one honest log line: what happened to the lookup
(`Found` / `Appended` / `NotFound`), the resolved path, the directory added if
any, and whether `TMUX` was actually present.

`runServe` logs:

- `Appended` — at INFO, naming the directory and the resolved binary. A PATH
  that did not contain tmux is worth knowing about even when recovered.
- `NotFound` — as a prominent error naming the directories probed. This is the
  line that has to exist: #1108's complaint is *"直接噴錯而我無法分辨為什麼"*,
  and ~35 separate `executable file not found` errors is that complaint.
- `TMUX` was set — at INFO, naming the value dropped, so a daemon started from
  a pane says so once instead of behaving oddly forever.

### 2.3 Not a startup failure

`NotFound` logs and continues. A tmux-less host still serves files, peers,
storage and nex; bricking all of it over a missing binary trades one confusing
failure for a worse one. The startup line is the fix for "can't tell why".

This matches the precedent that actually applies:
`docs/specs/2026-09-14-local-daemon-install-spec.md` already treats a missing
tmux as a **warning that blocks nothing** (`tools.tmux === null`). It is
deliberately *not* alpha.364's PATH gate, which refuses to start — that gate
guards the `pdx` binary itself, whose absence breaks the agent, hook and CLI
ecosystem wholesale, whereas a missing tmux degrades terminal and session
features while leaving the rest serving.

### 2.4 Where it is called, and why that slot

`cmd/pdx/main.go` `runServe`, immediately after step 0 (locale) and before
step 1 (config load). Two constraints pin it there.

**It must precede every tmux exec.** The earliest are reached through module
init and the first `/api/info` — `config.GetTmuxInstance` is called from
`core/info_handler.go:69` and `module/session/module.go:62`. (It is *not*
reached by `config.Load`, which only reads and validates the file.)

**It must precede nex's PATH policy.** `module/nex/module.go:131` snapshots
`PATH` into `m.origPath` at Init, `:133` applies `path_prepend`, and `:171`
restores the snapshot on soft-fail. Running first means the snapshot already
contains this repair, so a nex soft-fail restores *to* the fixed PATH rather
than undoing it. A nex that initialises successfully then re-orders `PATH`
according to its own `path_prepend`; both lists contain the same Homebrew
directories, so tmux stays resolvable either way.

**Only `runServe`.** `cmd/pdx/hook.go` runs *inside* a pane as a tmux hook and
asks "which session am I in"; it must keep its inherited `$TMUX`. Being a
separate process, it is unaffected — which is precisely why this belongs in the
process environment rather than in `RealExecutor`.

## 3. Out of scope

- Surfacing the resolved path in `/api/info` for the UI. Worth doing, but a
  separate concern from making the daemon work.
- #1108 (the SPA gate that blocks creating a session when no tmux is running).
  Deliberately deferred; this spec does not touch the SPA.
- Routing the ~35 sites through a single executor. Fixing the environment makes
  it unnecessary, and a 35-site refactor would bury this change's intent.

## 4. Acceptance

1. `tmux` already on PATH → `Found`, `PATH` byte-identical afterwards.
2. `tmux` not on PATH but executable in a probed directory → `Appended`, that
   directory is the **last** `PATH` element, and the rest of `PATH` is unchanged
   and still in order. The position is asserted, not merely the membership: a
   change back to prepending has to fail this test rather than an agent weeks
   later.
3. `tmux` in none of them → `NotFound`, `PATH` unchanged, no panic.
4. A non-executable file named `tmux` in a probed directory is not accepted:
   the probe continues to the next directory.
5. `TMUX` and `TMUX_PANE` are unset after `Prepare()` in every case above,
   and `Result.DroppedTMUX` reports whether `TMUX` had been set — including the
   negative: it is false when `TMUX` was never there, so the log line cannot
   cry wolf on an ordinary start.
6. `Prepare()` is idempotent: a second call does not add a second copy, and
   `PATH` is byte-identical afterwards. (The second call reports `Found`, not
   `Appended` — the first one put it on `PATH`.)
7. Probe order is honoured — with `tmux` in two probed directories, the earlier
   one wins.
8. `runServe` calls `Prepare()` before `config.Load`.

## 5. Testing

Go only; no SPA change. New `internal/tmuxenv/tmuxenv_test.go`.

**The seam is required, not optional.** The probe list is absolute paths, so
`t.TempDir()` cannot reach acceptance items 2, 4 and 7 without one. The package
therefore splits into an exported `Prepare()` that supplies
`defaultProbeDirs()` and an unexported `prepare(probe []string)` that the
in-package tests call with temp directories. `defaultProbeDirs()` gets its own
test, driven by `t.Setenv("HOME", …)`, asserting the list's contents and order
without asserting anything about this machine's filesystem.

With that seam every case is reachable through `t.Setenv` and `t.TempDir()`,
touching neither the real `PATH` nor the operator's tmux.

Item 8 is a structural claim rather than a behavioural one; it is covered by
reading the call order in `runServe` and stated here so a later edit that moves
the call has something to contradict.

TDD: each acceptance item gets a failing test before the implementation.
