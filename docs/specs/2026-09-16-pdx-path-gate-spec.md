# Spec — `pdx path`: make the CLI reachable, and refuse to start until it is

Status: draft v2 (redesigned on the user's direction: the app no longer places
the symlink itself — two explicit `pdx path` commands do, and the user picks.
v1's codex review `task-mu3zexp2-2n6khu` returned 3 Blockers / 4 Majors /
1 Minor / 4 omissions; §8 records which survived the redesign and which it
dissolved)
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

Every subcommand operates on **the running binary's own path**
(`os.Executable()`, resolved through symlinks). So
`~/.config/pdx/bin/pdx path link` links to the app-managed binary, and
`~/Workspace/wake/purdex/bin/pdx path link` links to the repo build. The
command never guesses which pdx the user meant: it is the one they ran.

### 3.1 `pdx path` — report

Prints, and with `--json` emits, the state the other two act on:

- this binary's own resolved path;
- what `pdx` resolves to on the **current process's** PATH, or that it does
  not resolve;
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

**Backup**: the rc file is copied to `<file>.pdx-backup` before the first write.
An existing backup is not overwritten, so the copy always represents the state
before pdx ever touched the file.

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

So: **the gate re-probes.** `status` and every gated operation resolve `pdx`
against a freshly built launch env, and a successful re-probe replaces the
cached one. The cache stays for what it was for — not re-running a login shell
on every unrelated call — but it may never be the thing a refusal is based on.

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
  `pathSource: 'fallback'` prominently: the app could not read your shell's
  PATH, so this is a guess; run `pdx path` in your own terminal to confirm.

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

## 6. Out of scope

- Windows. `local-daemon` is macOS/Linux throughout.
- Shells other than zsh and bash: refused with the line printed (§3.3).
- Editing rc files from anywhere except `pdx path add-to-shell` — see §2.1.
- `PATH` for the daemon's child processes beyond what `buildLaunchEnv` does.
- Making the repo-build workflow on mlab change in any way.

## 7. Acceptance

1. `pdx path` reports: resolves to this binary; resolves to a *different* pdx
   (named, exit 1); does not resolve; `~/.local/bin` present/absent and
   on/off PATH. `--json` shape asserted.
2. `pdx path link`: every row of §3.2's table, dangling symlink included.
   `--force` replaces a symlink and **refuses** a regular file. The replacement
   is atomic (a test asserts the path is never absent mid-operation, by
   inspecting the implementation's rename, not by racing it).
3. `pdx path add-to-shell`: zsh and bash (macOS and Linux paths); unknown and
   unset `$SHELL` refused with the line printed; the marked block written once
   and recognised on a second run (writes nothing); `~/.local/bin` already on
   PATH ⇒ writes nothing; backup created once and never overwritten;
   `--dry-run` writes nothing; an existing rc file keeps its mode.
4. The gate refuses `start`, `restart` and `ensureRunning`, and `ensureRunning`
   reports `'path-unresolved'`.
5. **The cache test** (§4.2): a fixture whose shell PATH lacks `pdx`, gate
   refuses; the fixture's PATH then changes; the *same* `LocalDaemon` instance
   starts successfully without being recreated. This is the test that proves
   the documented fix actually works.
6. `install` completes the swap and only then refuses at the start step; the
   binary is on disk afterwards and status reads installed-and-stopped.
7. §4.4's two branches: no resolve on fallback ⇒ refuse; resolve on fallback ⇒
   start with `pathSource: 'fallback'`.
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

## 9. Phases

Two, and they split cleanly because the command is useful on its own:

- **P1 — `pdx path`** (Go only): the three subcommands, tested in isolation.
  Shippable by itself: it fixes air-2026 the moment someone runs it.
- **P2 — the gate and the panel** (Electron + SPA): re-probe, refusal, status
  fields, buttons. Depends on P1 only for the command names in its message.
