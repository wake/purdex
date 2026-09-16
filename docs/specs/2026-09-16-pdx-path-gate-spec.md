# Spec — `pdx path`: make the CLI reachable, and refuse to start until it is

Status: draft v3 (codex review of v2, `task-mu43l14y-1xfmtq`: 3 Blockers /
4 Majors / 4 Minors / 6 omissions, all accepted — §8.1. v2 redesigned on the
user's direction: the app no longer places the symlink itself, two explicit
`pdx path` commands do. v1's review `task-mu3zexp2-2n6khu` is §8)
Date: 2026-09-16
Branch: `worktree-pdx-path-gate`
Scope: `cmd/pdx/` (new command), `electron/local-daemon/*`,
`spa/src/components/settings/LocalDaemonSection.tsx`

## 1. Problem

The app installs the daemon binary to `~/.config/pdx/bin/pdx` and starts it by
**absolute path** (`deps.exec(binPath, ['start'])`,
`electron/local-daemon/index.ts`). Nothing in the install flow touches `PATH`
or creates a symlink anywhere. The daemon runs; the *command* does not exist.

That gap is invisible from inside the app and total from outside it. On
air-2026 the binary is installed and `pdx` in a login shell is
`command not found` — `~/.local/bin` is not even on that machine's PATH. So
every `pdx …` line in `CLAUDE.md` — `pdx msg name`, `pdx msg whoami`,
`pdx peers --all`, the whole peer-addressing workflow agents are instructed to
use — fails on every machine that got its daemon from the app. An agent
following the documentation correctly gets `command not found`, and has no way
to know the documentation was written for a machine that was set up by hand.

mlab does not show the problem because it *was* set up by hand:
`~/.local/bin/pdx` is a symlink to the repo build
(`~/Workspace/wake/purdex/bin/pdx`), and `~/.config/pdx/bin/` does not exist
there at all. That symlink is a developer convenience, not the product working.

## 2. Decisions

Taken by the user; recorded so they are not silently re-litigated.

1. **Two commands, both explicit, the user picks.** `pdx path link` creates the
   symlink; `pdx path add-to-shell` puts its directory on PATH. Neither runs on
   its own as part of install.
2. **`add-to-shell` really edits the rc file** — detect the shell, back the
   file up, append, idempotently.
3. **Both are reachable from the Development panel**, as buttons, alongside the
   exact command for anyone who would rather type it.
4. **The gate stays**: when `pdx` does not resolve on PATH, the daemon is not
   allowed to start, and the refusal names both commands.

### 2.1 On reversing "never touch rc files"

The handoff recorded a hard rule: *絕對不要改 `.zshrc` 等 rc 檔*. Decision 2
reverses it, deliberately, and the distinction is the whole reason:

> The old rule protected the user from the **install flow** rewriting their
> shell configuration behind their back. `pdx path add-to-shell` is not the
> install flow — it is a command the user runs, named for exactly what it
> does, which does nothing until invoked.

An implementer or reviewer who finds the old rule in the handoff and "fixes"
this spec back to printing a line would be undoing a decision, not catching a
mistake. What remains absolutely prohibited: any rc write that happens as a
side effect of installing, starting, updating, or status-polling.

### 2.2 On the gate

A daemon that runs while its CLI is unreachable is a machine that looks healthy
and is not. The failure it causes surfaces somewhere else entirely — inside an
agent, hours later, as a missing command — and costs far more to diagnose than
a refusal at start time that names the problem and the fix. That is why the
gate blocks rather than warns: air-2026 is precisely a machine where a warning
would have been ignored for weeks, because nobody was looking at the panel.

## 3. `pdx path` (new command, `cmd/pdx`)

A top-level command beside `serve` / `start` / `peers` / `msg`, with two
subcommands. The filesystem work lives here, in Go, rather than in the Electron
layer — `os.Lstat` / `os.Readlink` / `os.Symlink` are one line each, where the
Electron `deps.fs` interface has none of them (v1's Blocker 3).

**`pdx path` is an offline repair command.** It must not `config.Load`, must
not open the store, and must not make an HTTP request — not even to check
whether the daemon is alive. It is the command a user reaches for precisely
when the daemon will not start, so anything it depends on is another thing
that can stop it from working. Its only inputs are `os.Executable()`, `$HOME`,
`$PATH`, `$SHELL` and the filesystem.

Every subcommand operates on **the running binary's own path**:
`os.Executable()` followed by `filepath.EvalSymlinks`, the same pairing
`cmd/pdx/setup.go` already uses. `os.Executable()` alone is not enough — when
the user runs `~/.local/bin/pdx path`, it may report the symlink, and every
comparison this command makes is about which *binary* is which. So
`~/.config/pdx/bin/pdx path link` links to the app-managed binary, and
`~/Workspace/wake/purdex/bin/pdx path link` links to the repo build. The
command never guesses which pdx the user meant: it is the one they ran.

### 3.1 `pdx path` — report

Prints, and with `--json` emits, the state the other two act on:

- this binary's own resolved path;
- what `pdx` resolves to on the **current process's** PATH, or that it does
  not resolve. Resolution uses `exec.LookPath` semantics: a PATH entry holding
  a `pdx` that is not a regular executable file is skipped, because a shell
  would skip it too. Reporting a non-executable file as "reachable" would be
  the same lie the whole feature exists to stop telling;
- whether that resolved `pdx` is **this** binary (compared by resolved path) —
  a machine with a stale `pdx` earlier on PATH is a real configuration, and
  silently "working" while pointing at last month's build is worse than not
  working;
- whether `~/.local/bin` exists and whether it is on PATH;
- which of the two fixes, if any, would help.

Exit status is 0 when `pdx` resolves to this binary, 1 otherwise, so the report
is usable from a script.

### 3.2 `pdx path link [--force]`

Ensures `~/.local/bin/pdx` points at this binary. `~/.local/bin` is created if
absent.

| At `~/.local/bin/pdx` | Action | Exit |
|---|---|---|
| nothing | create the symlink | 0 |
| a symlink already resolving to this binary | nothing, say so | 0 |
| a symlink to something else | **refuse**, print the existing target, suggest `--force` | 1 |
| a **dangling** symlink | **refuse**, print the raw target it points at | 1 |
| a regular file or directory | **refuse**, say which it is; `--force` does *not* override this | 1 |
| create fails (permissions, read-only home) | report the error | 1 |

Dangling symlinks get their own row because v1 missed them and the detection is
the reason: `os.Stat` follows the link and reports "not there", which would
look identical to an empty path and lead to a create that then fails with
`EEXIST` and no explanation. `os.Lstat` is what distinguishes the two.

`--force` replaces a *symlink*, never a regular file or directory. Replacing a
real file at that path would destroy data the user put there; replacing a
symlink only re-points a pointer, and the user asked for it. The replacement is
atomic (`symlink` to a temp name in the same directory, then `rename` over the
old one), so an interrupted `--force` never leaves the path empty.

**The limit of that guarantee, stated because v2 overstated it.** `rename(2)`
cannot be made conditional on the destination still being a symlink: between
the `Lstat` that classified the path and the `Rename` that replaces it, another
process could put a regular file there, and the rename would overwrite it.
There is no portable syscall that closes this window.

So the guarantee is scoped honestly rather than claimed absolutely:

- Concurrent runs of **pdx itself** are serialised by **one** lockfile —
  `~/.local/bin/.pdx-path.lock`, `O_CREATE|O_EXCL`, treated as stale after 30s,
  acquired by `link` **and** `add-to-shell` alike, since both change the same
  thing: whether `pdx` is reachable. The path is re-`Lstat`ed inside the lock,
  immediately before the rename.

  One lock, not two. A second lock beside the rc file was considered and
  rejected: it would only ever protect pdx from pdx, which this lock already
  does, and it cannot protect the rc file from the user's editor — no lockfile
  can, because editors do not take it. Claiming otherwise would be another
  guarantee the implementation does not have.
- A **non-pdx process** racing us for that exact path in that exact window is
  out of scope, and §7 says so instead of asserting a safety property the
  implementation cannot hold.

The alternative — `Remove` then `Symlink` — was rejected: it has the same
TOCTOU and additionally leaves a window in which `pdx` does not exist at all,
which is the state this whole feature exists to prevent.

Not overwriting by default matters concretely: mlab's `~/.local/bin/pdx` points
at a repo build its owner uses deliberately, and a link step that silently
re-pointed it would swap out the binary a developer is actively testing with.

### 3.3 `pdx path add-to-shell [--dry-run]`

Puts `~/.local/bin` on PATH by appending to the user's shell rc file.

**Which file**: from `$SHELL`'s basename — `zsh` → `~/.zshrc`; `bash` →
`~/.bash_profile` on macOS, `~/.bashrc` on Linux (the usual login-vs-interactive
split). Any other shell, or an unset `$SHELL`, is **refused** with the line to
add printed for manual use: guessing a config format for a shell we did not
recognise is how a config file gets corrupted, and fish in particular does not
even use `export`.

**What it writes**, as a marked block so it can be recognised later:

```sh
# >>> pdx path >>>
export PATH="$HOME/.local/bin:$PATH"
# <<< pdx path <<<
```

**Idempotent**: if the marked block is already present, the command reports
that and writes nothing. If `~/.local/bin` is already on PATH by some other
means, it says so and writes nothing — the goal is a working PATH, not a block
in a file.

**When the rc file is itself a symlink** — the normal shape under any dotfiles
manager — the temp-file-plus-`rename` below would replace the *symlink* with a
regular file, quietly detaching the user's shell config from the repository
that manages it. So the file is `Lstat`ed first:

- a symlink is resolved with `EvalSymlinks`, and **the target** is backed up
  and edited in place, leaving the link itself untouched;
- a symlink that does not resolve, or resolves outside `$HOME`, is **refused**
  with the block printed for manual use. Editing a file somewhere else in the
  filesystem on the strength of a link is not something this command should
  decide to do on its own.

**Backup**: the file that will actually be written (the symlink's target, if
that is what it is) is copied to `<file>.pdx-backup` before the first write.
An existing backup is not overwritten, so the copy always represents the state
before pdx ever touched the file.

**Trailing newline**: if the file is non-empty and does not end in `\n`, one is
written before the block. Appending straight onto a file whose last line has no
newline would splice the marker comment onto the end of a live shell command.

**Locking and lost updates**: the read-decide-write sequence runs under the
same single lock `link` uses (§3.2), and the marker/PATH checks are re-evaluated
*inside* the lock against a fresh read. Two concurrent `add-to-shell` runs
therefore produce one block, not two, and a run that read the file before the
user saved an edit in their editor cannot write a version that drops it —
because it never uses that earlier read to build what it writes.

**Writing**: append via a temp file in the same directory plus `rename`, so an
interrupted run cannot truncate a shell config. The file's existing mode is
preserved; a file that did not exist is created `0644`.

`--dry-run` prints the file it would edit and the exact block, and writes
nothing — the "I'd rather paste it myself" path, now a first-class flag rather
than a different feature.

**Does not re-exec, does not source anything.** The command says "open a new
terminal" because that is the truth; a command that claimed to have updated the
current shell would be lying — a child process cannot change its parent's
environment.

## 4. The gate (Electron)

### 4.1 What is checked

`pdx` is resolved against **the PATH the daemon is about to be launched with**
— `buildLaunchEnv`'s result (`electron/local-daemon/launch-env.ts`), which is
the user's login-shell PATH whenever the shell probe succeeds.

**The gate does not re-implement the resolution.** It runs the managed binary
with that env and reads its answer:

```ts
deps.exec(binPath, ['path', '--json'], { env: launchEnv })
```

`pdx path` already computes all of it — resolution with `exec.LookPath`
semantics (a `pdx` that is not executable is not a command), whether the winner
is this binary, the symlink's state — and the Electron `deps.fs` interface has
neither `lstat` nor `readlink` to do the same work honestly. Two
implementations of "is `pdx` reachable" would drift, and the TypeScript one
would be the weaker: it would have to treat a dangling symlink and an absent
path alike, which is the distinction §3.2 exists to make.

So the gate is a consumer of §3.1's report, with the daemon's launch PATH
substituted for the CLI's own. One implementation, tested once, in the language
that has the syscalls.

That PATH, not `process.env.PATH`, is the one to test: the daemon passes its
environment to the tmux sessions it creates, so it is the PATH **agents** end
up with, and agents are who the `pdx …` instructions are written for.

**A limitation v1 asserted too strongly** (v1 Major 1, verified): this holds
when the daemon starts a *new* tmux server. When a tmux server already exists —
started earlier from the user's own terminal — `tmux new-session` attaches to
that server and the new pane inherits **its** environment, not the daemon's. So
a passing gate is a necessary condition for agents to reach `pdx`, not a
sufficient one. The spec states this rather than pretending otherwise;
`pdx path` run *inside* a pane is the check that covers the rest, and the
refusal message says so.

### 4.2 The PATH cache, and why the gate must defeat it

`launchEnv()` memoizes `buildLaunchEnv()` in `envPromise`
(`electron/local-daemon/index.ts`), so every later `status` / `start` /
`install` reuses the first probe's PATH for the life of the Electron process.

That is fatal to this feature as v1 specified it (v1 Blocker 1): the user reads
the refusal, runs `pdx path add-to-shell`, opens a new terminal, presses Start —
and the app re-checks a PATH it captured before the fix, refuses again, and
looks broken. Following the instructions correctly would not work.

So the cache is split in two, because "always re-probe" is as wrong as "never":
`resolveShellPath` runs a login shell with two 5s timeouts, and `status` is
polled by the Development panel, so re-probing on every status call would put a
login shell behind a UI refresh.

```ts
cachedLaunchEnv()    // the memoized one; unrelated callers keep using it
refreshLaunchEnv()   // re-probe; on SUCCESS replace the cache, on failure keep it
```

Who gets which:

| Caller | PATH |
|---|---|
| `start`, `restart`, `ensureRunning`, `install`'s final start | **refresh** — these are the gate decisions |
| `status` when the last known `cli.resolved` was non-null | cache |
| `status` when the last known `cli.resolved` was null | **refresh**, at most once per 5s |
| everything else (`device-name`, health, …) | cache |

The asymmetry is the point: a machine that is fine pays nothing, and a machine
that is broken re-checks — which is exactly the machine whose user is running
fix commands in another window and watching the panel for it to clear.

A failed re-probe never overwrites a good cached PATH; it is reported
(`pathSource`, and the probe error) rather than allowed to turn a working
machine into a refusing one.

### 4.3 Which operations refuse

`start`, `restart` and `ensureRunning` resolve first and refuse when `pdx` does
not resolve. `install` is deliberately different: it runs the whole install —
download, verify, config, swap — and only then hits the gate before its final
start step. The binary is what the user asked for and it is now correctly
installed; only the start is refused, and `statusUnlocked` reports it as
installed-and-stopped (`decideOwnership` keys on the binary existing), which is
accurate. The UI must not present it as a failed install.

`ensureRunning` — the app-launch auto-start — gains a `'path-unresolved'`
outcome rather than folding into `'failed'`. It is not a failure to diagnose;
it is a specific, actionable state, and app launch is where a user first meets
it.

### 4.4 When the shell PATH cannot be read

If the shell probe fails, `buildLaunchEnv` falls back to `fallbackPath()`,
which **injects** `~/.local/bin`. The gate then judges a PATH the user's
terminal may not have.

The rule, and it splits the difference v1 got wrong in one direction and v1's
reviewer wanted wrong in the other:

- probe failed **and** `pdx` does not resolve even on the fallback ⇒ **refuse**.
  The machine is definitely broken.
- probe failed **but** `pdx` resolves on the fallback ⇒ **start**, and report
  `pathSource: 'fallback'` prominently. The wording is part of the spec, not
  left to the UI: the fallback proves only that the *daemon's* launch env will
  contain that directory, never that the user's terminal does. The panel says
  so — "the daemon started, but your terminal's PATH could not be verified;
  run `pdx path` in a terminal to check" — and §7 asserts it.

Refusing the second case would hard-block a machine whose only fault is an
unusual `$SHELL`, and — the part that matters — running either fix command
would not clear it, because the probe would still fail. A gate the user cannot
satisfy is worse than a gate that admits what it does not know.

### 4.5 The refusal message

The message is the feature, so it is specified rather than left to the
implementer:

```
Refusing to start: the command `pdx` is not on PATH, so agents following
CLAUDE.md will get "command not found".

The daemon binary is installed at ~/.config/pdx/bin/pdx.

Fix it with either (both are also buttons in Settings → Development):

  ~/.config/pdx/bin/pdx path link           create ~/.local/bin/pdx
  ~/.config/pdx/bin/pdx path add-to-shell   put ~/.local/bin on PATH

Then open a new terminal and run `pdx path` to confirm.
```

Which of the two is listed first varies with what `pdx path` found: a machine
that already has `~/.local/bin` on PATH needs only `link`, and a machine that
already has the symlink needs only `add-to-shell`. Both are always shown —
guessing wrong and hiding the one that was needed is the failure mode to avoid.

### 4.6 Status

`LocalDaemonStatus` gains:

```ts
cli: {
  resolved: string | null          // the pdx PATH finds, if any
  isManagedBinary: boolean         // …and whether it is the one we manage
  pathSource: 'shell' | 'fallback'
  localBinOnPath: boolean
  link: 'ok' | 'missing' | 'conflict' | 'error'
}
```

Every field comes from `pdx path --json` (§4.1) except `pathSource`, which is
the Electron side's own knowledge of how it built the PATH it passed in. When
the binary is not installed there is no `cli` block at all — there is nothing
to ask.

`link` describes what is observed *now*. v1 also had a `'created'` state, which
was wrong (v1 Major 3): status is recomputed on every poll and has no memory of
actions, so "created" could only ever be a lie on the second poll. Whether a
link was just created belongs to the command's own output.

## 5. Development panel

`LocalDaemonSection.tsx` gains a CLI block, always visible (not only on
failure — a user should be able to check it before it bites):

- the resolved `pdx`, with a warning when `isManagedBinary` is false and the
  resolved path is named;
- when `pathSource` is `'fallback'`, the §4.4 caveat;
- two buttons, **Create symlink** and **Add to PATH**, each running the
  corresponding command through the managed binary, each showing the command's
  own output (including its refusals — a conflict must be readable, not
  swallowed);
- both commands as selectable monospace text for anyone who would rather type
  them.

The buttons run the binary the app manages, by absolute path, so they work in
exactly the state that needs them: daemon refusing to start, `pdx` not on PATH.

### 5.1 The wiring the buttons need

None of it exists yet: `electron/main.ts` registers four dev IPC handlers
(`status` / `install` / `start` / `restart`), `electron/preload.ts` exposes the
same four, and `spa/src/types/electron.d.ts` types the same four. The spec is
not done until it names the rest:

- `LocalDaemon` gains `pathCommand(kind: 'link' | 'add-to-shell', opts?: { force?: boolean })`,
  which runs `deps.exec(binPath, ['path', …])` **inside `withLock`** — it
  changes what `start` will decide, so it must not interleave with an install
  or a start.
- two IPC channels, `dev:local-daemon-path-link` and
  `dev:local-daemon-path-add-to-shell`, registered inside the existing dev-mode
  gate, returning `{ code, stdout, stderr }` verbatim.
- matching `preload.ts` exposure, `electron.d.ts` types, and i18n keys for the
  panel's labels.

The command's own stdout/stderr is what the panel shows — including its
refusals. A conflict (`~/.local/bin/pdx` points somewhere else) must be
readable in the panel, not reduced to a red "failed": the whole value of that
refusal is the path it names.

## 6. Out of scope

- Windows. `local-daemon` is macOS/Linux throughout.
- Shells other than zsh and bash: refused with the line printed (§3.3).
- Editing rc files from anywhere except `pdx path add-to-shell` — see §2.1.
- `PATH` for the daemon's child processes beyond what `buildLaunchEnv` does.
- Making the repo-build workflow on mlab change in any way.

## 7. Acceptance

0. `pdx path` is offline: a test runs all three subcommands with no config
   file present, no daemon running, and a fake that fails the test if
   `config.Load` or any HTTP round-trip happens. It also follows the existing
   CLI conventions — `runPathCmd(args, stdout, stderr) int` drivable from a
   test without `os.Exit`, grammar errors exit 2, and `path` appears in
   `main.go`'s top-level usage list (asserted, since that list is hand-written
   and has no compiler keeping it honest).
1. `pdx path` reports: resolves to this binary; resolves to a *different* pdx
   (named, exit 1); does not resolve; `~/.local/bin` present/absent and
   on/off PATH; invoked **through** a symlink and still identifying itself by
   the resolved binary (`EvalSymlinks`). `--json` shape asserted.
2. `pdx path link`: every row of §3.2's table, dangling symlink included.
   `--force` replaces a symlink and **refuses** a regular file. Two concurrent
   runs serialise on the lockfile and produce one symlink. The test asserts
   pdx-level serialisation — **not** immunity to an external process, which
   §3.2 says is out of scope; a test claiming otherwise would be asserting a
   property the implementation does not have.
3. `pdx path add-to-shell`: zsh and bash (macOS and Linux paths); unknown and
   unset `$SHELL` refused with the line printed; the marked block written once
   and recognised on a second run (writes nothing); `~/.local/bin` already on
   PATH ⇒ writes nothing; backup created once and never overwritten;
   `--dry-run` writes nothing; an existing rc file keeps its mode. Plus the
   three cases v2 missed: **the rc file is a symlink** (the link survives, its
   target is what gets edited and backed up); a symlink pointing outside
   `$HOME` is refused; a file **not ending in a newline** gets one before the
   block. And concurrency: two simultaneous runs write one block, and a run
   whose read is stale does not drop a concurrent edit.
4. The gate refuses `start`, `restart` and `ensureRunning`, and `ensureRunning`
   reports `'path-unresolved'`.
5. **The cache tests** (§4.2). Two, and both matter:
   - a fixture whose shell PATH lacks `pdx`, gate refuses; the fixture's PATH
     then changes; the *same* `LocalDaemon` instance starts successfully
     without being recreated. This proves the documented fix actually works.
   - a healthy fixture polled for `status` ten times runs the shell probe
     **once**, and an unhealthy one re-probes but no more than once per 5s.
     This proves the fix did not put a login shell behind every UI refresh.
6. `install` completes the swap and only then refuses at the start step; the
   binary is on disk afterwards and status reads installed-and-stopped.
7. §4.4's two branches: no resolve on fallback ⇒ refuse; resolve on fallback ⇒
   start with `pathSource: 'fallback'`, and the panel string says the
   terminal's PATH is unverified and names `pdx path` as the way to check.
8. The refusal message names the binary path and **both** commands; asserted on
   the string, so it cannot rot into something unactionable.
9. The Electron side never writes to any rc path: asserted by a fake that fails
   the test if `writeFile` / `rename` / `openWrite` / `unlink` is called with a
   path matching `.zshrc` / `.bashrc` / `.bash_profile` / `.zprofile` /
   `.profile`. (v1 proposed grepping the sources for those names, which its
   reviewer correctly called formalism — it cannot distinguish a comment from a
   write, and now the refusal message legitimately mentions them.)

## 8. v1 review disposition (`task-mu3zexp2-2n6khu`)

| # | Severity | Finding | Now |
|---|---|---|---|
| B1 | Blocker | `launchEnv()` memoizes PATH, so a user who follows the fix instructions and retries in the same app process is refused again | **Stands, fixed** — §4.2 requires a re-probe; §7.5 is the test |
| B2 | Blocker | Symlink created only by `install`, so an already-installed machine with no symlink (air-2026) has no button that could ever create one | **Dissolved by the redesign** — the two commands are independent of install and always available |
| B3 | Blocker | `deps.fs` has no `lstat`/`readlink`/`symlink`, and `exists()` reports a dangling symlink as absent | **Dissolved** — the filesystem work moved to Go (§3). The dangling-symlink case it surfaced is now §3.2's own row |
| M1 | Major | "the daemon's PATH is what agents get" holds only for a tmux server the daemon started; an existing server has its own env | **Stands, accepted** — §4.1 states it as a necessary-not-sufficient condition |
| M2 | Major | Passing on the injected fallback PATH turns "unknown" into "fine" | **Stands, split** — §4.4: refuse when it does not resolve even on the fallback, start-and-flag when it does. Refusing outright would create a gate the fix commands cannot clear |
| M3 | Major | `link.state: 'created'` cannot survive a second poll | **Stands, fixed** — §4.6 |
| M4 | Major | Spell out that a refused install is installed-and-stopped, not a rollback | **Stands, fixed** — §4.3 |
| m1 | Minor | Grepping sources for rc filenames is formalism | **Stands, fixed** — §7.9 asserts on the fake's calls |
| — | Omission | `withLock` re-entrancy: new helpers must be `Unlocked` | Implementation note for the plan |
| — | Omission | A different `pdx` earlier on PATH should be visible | **Fixed** — §3.1, §4.6 `isManagedBinary` |
| — | Omission | `$SHELL` may be absent in a packaged app | **Fixed** — §3.3 refuses; §4.4 covers the probe failure |
| — | Omission | `~/.local/bin` conventions differ by OS | **Fixed** — §3.3's bash split; §7.1 |

### 8.1 v2 review disposition (`task-mu43l14y-1xfmtq`)

Every finding accepted. The three blockers are worth naming individually,
because two of them are properties v2 **claimed and could not hold** — the
most expensive kind of spec defect, since a reader trusts them.

| # | Severity | Finding | Fix |
|---|---|---|---|
| B1 | Blocker | "`status` and every gated operation re-probe" puts a login shell (2 × 5s timeouts) behind every UI status poll | §4.2 splits `cachedLaunchEnv()` / `refreshLaunchEnv()` and tables who gets which; §7.5 tests both the correctness *and* the probe count |
| B2 | Blocker | `--force`'s "never replaces a regular file" cannot survive a TOCTOU: `rename` is not conditional on the destination still being a symlink | §3.2 scopes the guarantee to pdx's own concurrent runs (lockfile + re-`Lstat` inside it) and says plainly that an external process racing us is out of scope; §7.2 tests what is actually true |
| B3 | Blocker | If the rc file is a symlink — the normal dotfiles shape — temp+`rename` silently replaces the user's symlink with a regular file | §3.3 `Lstat`s first and edits the *target*, leaving the link alone; refuses a link resolving outside `$HOME`; §7.3 |
| M1 | Major | No locking on the rc write: two runs, or a concurrent editor save, lose an update | §3.3 locks and re-reads inside the lock |
| M2 | Major | A file not ending in `\n` gets the marker spliced onto a live command | §3.3 writes the newline first |
| M3 | Major | The panel buttons have no IPC, preload, or SPA type — none of it exists | §5.1 names all four layers |
| M4 | Major | `pathSource: 'fallback'` needs explicit wording about what it does *not* prove | §4.4, asserted in §7.7 |
| m1 | Minor | `pdx path` must not load config or reach the daemon | §3 opening paragraph, tested in §7.0 |
| m2 | Minor | `os.Executable()` needs `EvalSymlinks` (repo precedent: `setup.go`) | §3, tested in §7.1 |
| m3 | Minor | `main.go`'s hand-written usage list needs `path`, and the CLI conventions (exit 2, testable inner function) apply | §7.0 |
| m4 | Minor | Phase split is fine | — |

## 9. Phases

Two, and they split cleanly because the command is useful on its own:

- **P1 — `pdx path`** (Go only): the three subcommands, tested in isolation.
  Shippable by itself: it fixes air-2026 the moment someone runs it.
- **P2 — the gate and the panel** (Electron + SPA): re-probe, refusal, status
  fields, buttons. Depends on P1 only for the command names in its message.
