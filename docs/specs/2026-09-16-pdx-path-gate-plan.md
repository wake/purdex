# Plan — `pdx path` + the PATH gate

Spec: `2026-09-16-pdx-path-gate-spec.md` v3
Date: 2026-09-16
Branch: `worktree-pdx-path-gate`
Baseline: `origin/main` @ `1.0.0-alpha.363`; `go test ./...` and
`cd spa && npx vitest run` green before task 1.

Two phases (spec §9). **P1 is Go-only and ships on its own**; P2 is the
Electron gate plus the panel. Every task is TDD: the tests in its "Tests first"
list are written and seen to fail before the implementation, and each task ends
in its own commit.

## Conventions for every task

- Work in `/Users/wake/Workspace/wake/purdex/.claude/worktrees/worktree-pdx-path-gate`.
  Every `Bash` call is prefixed `cd <that path> && `.
- Commit with `git commit --only <files>` naming exactly the files that task
  touched. Never `git add -A`.
- Go: `go build ./... && go test ./... && go vet ./...` green at the end of
  **each** task. TS: `cd spa && pnpm run lint && npx vitest run`.
- Follow the existing CLI shape (`cmd/pdx/peers.go`): a thin `runPath(args)`
  that calls `os.Exit(runPathCmd(...))`, with all the work in the inner
  function so tests drive it without `os.Exit`. Grammar errors exit 2.
- **Nothing in `cmd/pdx/path*.go` may call `config.Load`, open the store, or
  make an HTTP request** (spec §3). This is the one rule that, if broken,
  breaks the feature's whole reason to exist.

---

# P1 — the `pdx path` command (Go only)

## Task 1 — the environment seam and `pdx path` (report)

**Files:** `cmd/pdx/path.go`, `cmd/pdx/path_test.go`, `cmd/pdx/main.go`

Everything this command touches is injected, so the tests never read the real
`$HOME`, `$PATH` or `$SHELL`:

```go
// pathEnv is everything `pdx path` is allowed to look at (spec §3).
type pathEnv struct {
    self  string              // os.Executable() + filepath.EvalSymlinks, done by the caller
    home  string
    path  string              // raw $PATH
    shell string              // raw $SHELL
    goos  string              // runtime.GOOS; bash's rc file differs by OS
}
```

`runPathCmd(env pathEnv, args []string, stdout, stderr io.Writer) int` is the
testable entry point; `runPath(args)` builds a `pathEnv` from the real process
and calls it. `os.Executable()` is paired with `filepath.EvalSymlinks` exactly
as `cmd/pdx/setup.go` already does — **not** `os.Executable()` alone, or
running through `~/.local/bin/pdx` misidentifies the binary. Copy that file's
error handling too: an `os.Executable()` error fails the command, but an
`EvalSymlinks` error keeps the unresolved executable path and notes it in the
report. "Paired exactly" must not be read as "fail if either fails".

Resolution uses **`exec.LookPath` semantics**: a PATH entry holding a `pdx`
that is not a regular executable file is skipped, because a shell skips it too.
A non-executable file reported as reachable would be precisely the false
"everything is fine" this feature exists to remove.

Report contents and exit codes per spec §3.1. `--json` emits a stable shape;
the human form is a few labelled lines.

Also: add `path` to `main.go`'s `switch` **and** to its hand-written usage
string — that list has no compiler keeping it honest, so the test asserts it.

**Tests first** (`path_test.go`, table-driven over `pathEnv` fixtures with a
`t.TempDir()` home):

1. `pdx` resolves to this binary ⇒ exit 0, report says so.
2. `pdx` resolves to a *different* binary ⇒ exit 1, the other path is **named**.
3. `pdx` does not resolve ⇒ exit 1, and the report names both fixes.
4. invoked through a symlink (temp dir: real binary + a symlink to it) ⇒ the
   report identifies the resolved binary, not the link.
5. `~/.local/bin` absent / present-but-not-on-PATH / present-and-on-PATH ⇒ the
   suggested fix differs in each.
6. an empty PATH, and a PATH entry that does not exist ⇒ no crash.
6b. a PATH entry holding a **non-executable** `pdx` ⇒ skipped, and if a real
    one sits later on PATH, that one wins.
7. two `pdx` on PATH ⇒ first wins and the report says which.
8. `--json` shape.
9. grammar: unknown subcommand and unknown flag ⇒ exit 2, usage on stderr.
10. `main.go`'s usage string contains `path`.
11. **offline**: the whole suite runs with no config file and no daemon; a test
    greps `cmd/pdx/path.go` for `config.Load` / `http.` and fails on a match.
    (A grep is weak evidence in general — here it is checking for the *absence*
    of an import-level dependency in one small file, which is exactly what it
    can prove.)

**Commit:** `feat(cli): pdx path reports how the CLI is reachable`

---

## Task 2 — `pdx path link`

**Files:** `cmd/pdx/path.go`, `cmd/pdx/path_test.go`

Spec §3.2's table, plus the lockfile. Order inside the lock: acquire →
`Lstat` → classify → act. The `--force` replacement is `Symlink` to a temp name
in the same directory then `Rename` over the destination.

**One lock for both commands** (spec §3.2): `~/.local/bin/.pdx-path.lock`,
`O_CREATE|O_EXCL`, stale after 30s. Task 3 takes the *same* lock — not a second
one beside the rc file — because both commands change the same thing, whether
`pdx` is reachable. Write it once as a small helper with its own tests,
including the acquisition policy the spec leaves open: retry every 50ms up to
5s, then fail with a message naming the lock path; a lockfile whose mtime is
older than 30s is removed and re-acquired.

**Tests first:**

1. every row of spec §3.2's table: empty path; already correct; symlink
   elsewhere (refused, target named); **dangling symlink** (refused, raw target
   printed — `os.Stat` cannot distinguish this from "nothing", `os.Lstat` can);
   regular file (refused); directory (refused).
2. `--force` replaces a symlink; `--force` still **refuses** a regular file and
   a directory.
3. `~/.local/bin` does not exist ⇒ created.
4. two concurrent `runPathCmd` link calls ⇒ one symlink, no error, and the
   lockfile is gone afterwards. The test asserts **pdx-level** serialisation
   only — spec §3.2 states that an external process racing the rename is out of
   scope, and a test claiming otherwise would assert a property the code does
   not have.
5. a stale lockfile (mtime older than 30s) is broken rather than deadlocking;
   a fresh one held by someone else times out after 5s with the lock path in
   the message.
6. a `link` and an `add-to-shell` running concurrently do not interleave their
   decisions — they contend for the same lock.

**Commit:** `feat(cli): pdx path link places the ~/.local/bin symlink`

---

## Task 3 — `pdx path add-to-shell`

**Files:** `cmd/pdx/path.go`, `cmd/pdx/path_test.go`

Spec §3.3 in full. The order matters and is the spec's, not the implementer's:
resolve the rc file (**`Lstat` first**) → acquire the lock → re-read → decide
(marker present? `~/.local/bin` already on PATH?) → back up → write.

The three things v2 got wrong, all of which are about not damaging a file the
user owns:

- **rc file is a symlink**: resolve it and edit the *target*; the link itself
  must still be a link afterwards. A link resolving outside `$HOME` is refused.
- **no trailing newline**: write one before the block.
- **stale read**: every decision is made inside the lock against a fresh read.

**Tests first:**

1. zsh ⇒ `~/.zshrc`; bash on darwin ⇒ `~/.bash_profile`; bash on linux ⇒
   `~/.bashrc` (via `pathEnv.goos`).
2. unknown `$SHELL` (`fish`) and unset `$SHELL` ⇒ refused, exit 1, the block
   printed for manual use, **nothing written**.
3. the marked block is written once; a second run writes nothing and says so.
4. `~/.local/bin` already on PATH ⇒ writes nothing.
5. **rc file is a symlink** ⇒ after the run, the rc path is *still a symlink*,
   the target contains the block, and `<target>.pdx-backup` exists.
6. rc symlink resolving outside `$HOME` ⇒ refused, nothing written.
7. **file with no trailing newline** ⇒ the last original line is intact and the
   marker starts on its own line.
8. backup created once; a second run does not overwrite it.
9. `--dry-run` prints the file and the block, writes nothing, creates no backup.
10. an existing file keeps its mode (0600 stays 0600); a new file is 0644.
11. two concurrent runs ⇒ exactly one block.
11b. **stale read** (spec §7.3): a test seam rewrites the rc file to
    `original\nuser-edit\n` *after* the command's first read but *before* it
    takes the lock; the result must contain `user-edit` and the block. Test 11
    only proves marker de-duplication — it cannot prove the write is built from
    the in-lock read, which is the property that stops a lost update.
12. `$HOME` containing a space works throughout (the written line uses
    `$HOME`, so this is about the *paths we open*, not the text we write).

**Commit:** `feat(cli): pdx path add-to-shell edits the shell rc file`

---

## Task 4 — P1 verification and docs

**Files:** `CLAUDE.md`, plus whatever Task 1–3 review turns up

0. **Offline, all three subcommands** (spec §7.0): one test drives `path`,
   `path link` and `path add-to-shell --dry-run` with no config file, no daemon
   and a guard that fails on a `config.Load` or an HTTP round-trip. Task 1's
   version of this could only cover `path`, because the other two did not exist
   yet.
1. Full Go suite, vet, and a real end-to-end run **on this machine** against a
   throwaway `HOME` (never the real one):
   `HOME=$TMP ./bin/pdx path`, `… path link`, `… path add-to-shell --dry-run`,
   then `… path` again to see the state change. Paste the transcript into the
   PR.
2. `CLAUDE.md`'s peer-address section tells agents to run `pdx …`; it gains one
   line saying what to do when that is `command not found` — run
   `~/.config/pdx/bin/pdx path` (or the repo build) and follow it. Agents read
   that file; this is the entry point that makes the command discoverable to
   them at the moment they need it.

**Commit:** `docs: tell agents what to do when pdx is not on PATH`

---

# P2 — the gate and the panel

## Task 5 — the launch-env split, and asking the binary

**Files:** `electron/local-daemon/launch-env.ts`, `launch-env.test.ts`,
`electron/local-daemon/index.ts`, `index.test.ts`

Two pieces, both prerequisites of the gate.

**(a) `buildLaunchEnv` must say how it got the PATH.** It currently returns a
bare `ProcessEnv`, which hides whether the shell probe worked — and §4.4's
whole decision turns on that. It returns `{ env, pathSource, probeError? }`
instead; `index.ts` caches that structure. Without this, Task 6 would have to
re-probe or guess.

**(b) `cachedLaunchEnv()` / `refreshLaunchEnv()`** per spec §4.2, with the
caller table. `refreshLaunchEnv` replaces the cache **only on success**.

**(c) `readCliState()`**: run `deps.exec(binPath, ['path', '--json'], { env })`
and parse it. This is where the gate's answer comes from (spec §4.1) — the
TypeScript side never walks PATH itself. A non-zero exit is data, not an
error: `pdx path` exits 1 precisely when `pdx` is not reachable, which is the
case the gate cares most about. Unparseable output, a missing binary, or an
exec failure are distinct from "not reachable" and are reported as such.

**Tests first:**

1. `buildLaunchEnv` reports `pathSource: 'shell'` on a successful probe and
   `'fallback'` + `probeError` when both probe forms fail.
2. `refreshLaunchEnv` success replaces the cache; failure leaves the previous
   good cache in place and does not turn a working machine into a refusing one.
3. `readCliState`: exit 0 parsed; exit 1 parsed (this is the normal
   not-reachable answer); malformed JSON; exec failure; binary absent — each a
   distinct outcome, none collapsed into the others.
4. a healthy fixture polled for `status` ten times probes the shell **once**.
5. an unhealthy fixture re-probes on `status` but **at most once per 5s**,
   asserted with the existing injectable `now()` (`deps.now`, already used by
   the test fakes).

**Commit:** `refactor(local-daemon): launch env reports its source; read cli state`

---

## Task 6 — the gate

**Files:** `electron/local-daemon/index.ts`, `types.ts`, `index.test.ts`,
`electron/main.ts`

`ensureRunning`'s return type gains a member, so every caller and every test
expecting the old union must be updated in the same commit or the TypeScript
build breaks — `electron/main.ts` logs the result, and `index.test.ts` asserts
the outcomes.

`start` / `restart` / `ensureRunning` / `install`'s final start resolve against
a **refreshed** env and refuse when `pdx` does not resolve. `ensureRunning`
gains `'path-unresolved'`. `LocalDaemonStatus` gains the `cli` block (spec
§4.6) — `link` is `ok | missing | conflict | error`, never `created`.

§4.4's split: probe failed **and** no resolve even on the fallback ⇒ refuse;
probe failed **but** resolves ⇒ start, `pathSource: 'fallback'`.

The refusal message is spec §4.5 verbatim, including both commands.

**Tests first:**

1. `start`, `restart`, `ensureRunning` refuse when `pdx` does not resolve;
   `ensureRunning` returns `'path-unresolved'`, not `'failed'`.
2. **the cache test** (spec §7.5): gate refuses, the fixture's shell PATH then
   changes, and the *same instance* starts — no re-construction. This is the
   test that proves a user who follows the instructions is not stuck.
3. `install` completes the swap and only then refuses; the binary is on disk
   afterwards and `status` reads installed-and-stopped, not a rollback.
4. both §4.4 branches.
5. the refusal string names the binary path and **both** commands.
6. `cli.isManagedBinary === false` when a different `pdx` wins — taken from
   the binary's own JSON, not recomputed in TypeScript.
7. no rc writes from the Electron side: a fake that fails the test if
   `writeFile`/`rename`/`openWrite`/`unlink` is called with a path matching
   `.zshrc` / `.bashrc` / `.bash_profile` / `.zprofile` / `.profile`.

**Commit:** `feat(local-daemon): refuse to start while pdx is off PATH`

---

## Task 7 — panel buttons and their wiring

**Files:** `electron/local-daemon/index.ts`, `types.ts`, `electron/main.ts`,
`electron/preload.ts`, `spa/src/types/electron.d.ts`,
`spa/src/components/settings/LocalDaemonSection.tsx`, i18n files, tests

Spec §5.1. `LocalDaemon.pathCommand(kind, opts)` runs
`deps.exec(binPath, ['path', …])` **inside `withLock`** — a new public method
must never call another public method, or it queues behind its own lock
(`withLock`, `index.ts`). Two IPC channels inside the existing dev-mode gate,
returning `{ code, stdout, stderr }` verbatim; matching preload exposure and
SPA types.

The panel's CLI block is always visible: resolved path, a warning when it is
not the managed binary, the §4.4 caveat when `pathSource === 'fallback'`, two
buttons, and both commands as selectable text. The command's own output —
**including refusals** — is shown; a conflict's value is the path it names.

**Tests first:**

1. `pathCommand` runs the managed binary with the right argv and returns the
   exec result verbatim (non-zero exit is **not** thrown away).
2. it takes the lock: a `pathCommand` issued while an install is in flight runs
   after it, not during.
3. `main.ts` registers both channels **inside** the dev gate (the existing
   `devmode.test.ts` pattern asserts this by reading the source).
4. preload exposes both; `electron.d.ts` types both.
5. `LocalDaemonSection` renders: resolved path; the not-managed warning; the
   fallback caveat; both buttons; a refusal's stderr text.

**Commit:** `feat(settings): CLI section with link and add-to-shell buttons`

---

## Verification before the PR

```
go build ./... && go test ./... && go vet ./...
cd spa && pnpm run lint && npx vitest run && pnpm run build
```

Plus the P1 transcript from Task 4 (throwaway `HOME`), pasted into the PR.

**Not verifiable here, and the PR says so:** the gate's real effect on
air-2026. This machine's `pdx` resolves (to the repo build), so the refusal
path can only be exercised against fixtures, and air's daemon is updated
through the app's dev-update flow, not from here.

## Risks

| Risk | Mitigation |
|---|---|
| A test writes to the real `~/.zshrc` | Every rc test uses a `t.TempDir()` home through `pathEnv`; no test may read the process environment |
| The probe debounce makes the panel feel stuck | Task 5 test 4 pins the interval; 5s is short enough to feel live while a user fixes PATH in another window |
| `pathCommand` deadlocks on `withLock` | Task 7 test 2; the rule is in the plan text because the codebase has the `Unlocked` convention for exactly this |
| P1 merges and P2 slips | P1 is useful alone — it is the command that fixes air-2026 by hand. P2 adds the enforcement |
