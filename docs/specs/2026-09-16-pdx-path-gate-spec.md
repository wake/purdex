# Spec — `pdx` must be reachable from PATH before the daemon starts

Status: draft v1
Date: 2026-09-16
Branch: `worktree-pdx-path-gate`
Scope: `electron/local-daemon/*`, `spa/src/components/settings/LocalDaemonSection.tsx`

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
following the documentation correctly gets `command not found` and has no way
to know the documentation was written for a machine that was set up by hand.

mlab does not show the problem because it was set up by hand: `~/.local/bin/pdx`
is a symlink to the repo build (`~/Workspace/wake/purdex/bin/pdx`), and
`~/.config/pdx/bin/` does not exist there at all. That symlink is a developer
convenience, not the product behaving correctly — and §3.3 makes sure the app
never overwrites it.

## 2. Decisions (already taken by the user; recorded, not re-opened)

1. After installing, create a symlink `~/.local/bin/pdx` → the managed binary.
   Creating a file inside the user's own directory is fine.
2. **Never edit `.zshrc` or any other rc file.** Not as a fallback, not with a
   prompt, not "just appending one line". The app writes files it owns; it does
   not rewrite the user's shell configuration.
3. If `pdx` cannot be resolved from PATH, **the daemon is not allowed to
   start.** The user is forced to fix it then and there rather than discovering
   it later through an agent's `command not found`.

Decision 3 is the unusual one, so its reasoning is worth recording: a daemon
that runs while its CLI is unreachable is a machine that looks healthy and is
not. The failure it causes surfaces somewhere else entirely — inside an agent,
hours later, as a missing command — and costs far more to diagnose than a
refusal at start time that names the problem and the fix.

## 3. Design

### 3.1 What "reachable" means

The check resolves the literal command name `pdx` against **the PATH the
daemon is about to be launched with** — `buildLaunchEnv`'s result
(`electron/local-daemon/launch-env.ts`), which is the user's login-shell PATH
whenever the shell probe succeeds.

That PATH, not `process.env.PATH`, is the correct one to test, and for a
specific reason: the daemon passes its environment to the tmux panes it
spawns, so it is the PATH the **agents** end up with, and agents are who the
`pdx …` instructions are written for.

Resolution walks the PATH entries in order and returns the first `pdx` that
exists, along with the directory it came from. The result is reported, not
just used: seeing *which* `pdx` wins matters on a machine that has more than
one.

One honest limitation, stated rather than papered over: when the shell probe
fails, `buildLaunchEnv` falls back to `fallbackPath()`, which **injects**
`~/.local/bin`. The gate then passes on a PATH the user's own terminal may not
have. The check reports which of the two PATHs it used
(`source: "shell" | "fallback"`), and the fallback case is surfaced in the UI
as exactly what it is: the app could not read your shell's PATH, so this is a
guess. Making the gate refuse on a fallback PATH would block the daemon on
machines whose only sin is an unusual shell setup, which decision 3 does not
ask for.

### 3.2 The gate

`start`, `restart` and `ensureRunning` resolve `pdx` first and refuse when it
is missing. `install` is deliberately different: it runs the whole install —
download, verify, config, swap, symlink — and only then hits the gate before
its final start step.

Failing an install *after* the binary is in place is intentional. The binary
is what the user asked for and it is now correctly installed; only the start
is refused. The alternative — refusing before the download — would leave the
machine with nothing and still demand the same fix.

The refusal message is the whole point of the feature, so it is specified
rather than left to the implementer:

```
pdx is installed at ~/.config/pdx/bin/pdx but the command `pdx` is not on
your PATH, so agents following CLAUDE.md will get "command not found".

~/.local/bin/pdx → ~/.config/pdx/bin/pdx exists, but ~/.local/bin is not on
your PATH. Add this to your shell config and open a new terminal:

    export PATH="$HOME/.local/bin:$PATH"

Purdex does not edit your shell config for you.
```

The second paragraph varies with what §3.3 found: the symlink exists but its
directory is not on PATH (above); the symlink could not be created because
something else is at that path (name what, and what it points to); or the
symlink was never created (name the error).

`ensureRunning` — the app-launch auto-start — gains a
`'path-unresolved'` outcome rather than folding into `'failed'`. It is not a
failure to diagnose; it is a specific, actionable state, and the app-launch
path is exactly where a user will first meet it.

### 3.3 The symlink

After a successful binary swap, `install` ensures `~/.local/bin/pdx` points at
the managed binary. `~/.local/bin` is created if absent (`mkdir -p`
semantics). The outcome is one of:

| At `~/.local/bin/pdx` | Action | Reported as |
|---|---|---|
| nothing | create the symlink | `created` |
| a symlink to the managed binary | nothing | `ok` |
| a symlink somewhere else | **leave it alone** | `conflict`, with the existing target |
| a regular file or directory | **leave it alone** | `conflict`, with what it is |
| create failed (permissions, read-only home) | leave it | `error`, with the message |

Never overwriting is not caution for its own sake: mlab's `~/.local/bin/pdx`
points at a repo build that its owner uses deliberately, and an install that
silently repointed it would swap out the binary a developer is actively
testing with. A conflict is reported and the gate then judges the *result* —
if that other `pdx` resolves on PATH, the machine is fine and the daemon
starts. The app's job is that `pdx` works, not that `pdx` is its own copy.

A symlink is created **only by `install`**. `start` and `restart` check but do
not create: they are not the operation that owns the binary's placement, and a
start that silently creates files is a start that does something it was not
asked to do.

### 3.4 Status

`LocalDaemonStatus` gains one field:

```ts
cli: {
  resolved: string | null        // the pdx that PATH finds, if any
  pathSource: 'shell' | 'fallback'
  link: { state: 'ok' | 'created' | 'missing' | 'conflict' | 'error', path: string, target?: string, message?: string }
}
```

`LocalDaemonSection.tsx` renders it in the Development page: a green line with
the resolved path when it is fine, and when it is not, the refusal text from
§3.2 with the `export PATH=…` line in a selectable monospace block — the user
must be able to copy it, since the app will not apply it for them.

## 4. Out of scope

- Editing rc files, offering to edit them, or detecting which one to edit.
- Windows. This whole feature is macOS/Linux, like the rest of `local-daemon`.
- `PATH` for the daemon's own child processes beyond what `buildLaunchEnv`
  already does.
- The repo-build workflow on mlab. It keeps working untouched, and §3.3 is
  what guarantees that.

## 5. Acceptance

1. Resolution unit tests: found in the first PATH entry; found in a later one;
   absent; an empty PATH; a PATH entry that does not exist; two `pdx` on PATH
   (first wins, and the reported path says which).
2. The gate refuses `start`, `restart` and `ensureRunning` when `pdx` does not
   resolve, and `ensureRunning` reports `'path-unresolved'`, not `'failed'`.
3. `install` completes the swap **and** the symlink and only then refuses at
   the start step; a second `install` or `start` after PATH is fixed succeeds.
   A test asserts the binary is present on disk after the refused install.
4. Symlink: each row of §3.3's table, including the mlab shape — an existing
   symlink to a *different* binary is left untouched, reported as `conflict`,
   and the daemon still starts when that other `pdx` is on PATH.
5. The refusal message names the binary path, the reason, and the exact
   `export PATH=…` line; a test asserts all three are present, so the message
   cannot rot into something unactionable.
6. `pathSource: 'fallback'` is reported when the shell probe fails, and the
   gate still passes on the injected `~/.local/bin` (§3.1's stated limitation),
   with the UI saying the PATH could not be read from the shell.
7. No test, and no code path, writes to any rc file. A test greps the
   local-daemon sources for `zshrc`/`bashrc`/`profile` and fails on a match —
   decision 2 is load-bearing enough to be enforced mechanically.

## 6. Phases

One phase. The gate without the symlink would refuse to start on every
app-installed machine with no way to fix it from the app; the symlink without
the gate is the silent-failure status quo with one more file in it.
