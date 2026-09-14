# Spec — Daemon ensures a UTF-8 locale ("Daemon UTF-8 Locale")

Status: v1.1 (after subagent spec/plan review R1 — §1 table +3 rows, §4.1 tmux.c rationale, §4.3 once-per-call log + CleanOrphans note)
Date: 2026-09-14
Branch: `worktree-daemon-utf8-locale`
Scope: daemon (`cmd/pdx`, `internal/tmux`, `internal/module/session`). No SPA
change. No Electron change (see §3 non-goals).

## 1. Problem

On air-2026 the daemon is started by the Finder-launched Purdex.app
(`electron/local-daemon` → `pdx start` → `pdx serve`). The App's environment
is the bare launchd one:

```
HOME LOGNAME USER SHELL TMPDIR PATH=/usr/bin:/bin:/usr/sbin:/sbin __CFBundleIdentifier …
```

`launch-env.ts` repairs `PATH` from the user's shell but nothing sets a
locale, so the daemon — and every process it spawns — runs with no
`LANG` / `LC_ALL` / `LC_CTYPE`.

tmux sanitises format output per the **client's** locale: without a UTF-8
codeset every non-printable byte is replaced by `_`, including the TAB the
daemon uses as a field separator. Reproduced on air-2026 (tmux 3.7c) and on
mlab (tmux 3.6a) — it is locale-, not version-dependent:

```
env -i HOME=… PATH=… tmux list-sessions -F '#{session_id}\t#{session_name}\t#{session_path}'
$0_probe1_/Users/wake          ← one field
LANG=en_US.UTF-8 … same command
$0<TAB>probe1<TAB>/Users/wake  ← three fields
```

Observed symptoms, all explained by this:

| Symptom | Cause |
|---|---|
| `GET /api/sessions` → `[]` while `tmux ls` shows sessions | `RealExecutor.ListSessions` (`internal/tmux/executor.go:114`) yields one field per line; `EncodeSessionID("$0_probe1_/Users/wake")` fails `Atoi`; `service.go:50` skips it with a bare `continue` |
| `POST /api/sessions` → 500 `session created but not found` although tmux created the session | parsed `Name` is `""`, never equals `req.Name` (`handler.go:122`) |
| `session_meta` stays at 0 rows | `SetMeta` is never reached |
| Nothing in `pdx.log` | both skip paths are silent |
| Monitor pane list errors (`parseTmuxPaneListOutput`) | same tab loss, `monitor/tmux_panes.go:52` |
| **Existing `session_meta` rows get wiped** | the mangled whole-line IDs are handed to `CleanOrphans` (`service.go:44`), which deletes every row not in that bogus set |
| `capture-pane` content has every non-ASCII byte as `_` | same client-side sanitising (`executor.go:401/416`); any probe regex over pane text sees mangled input |
| Purdex terminals render CJK / box-drawing as `_` | the relay's `tmux attach` client (`internal/terminal/relay.go:52`) is spawned by the daemon with the same env |

Secondary effect: the tmux **server** the daemon starts (`new-session`)
inherits the daemon's environment, so on air-2026 every pane shell and every
agent inside Purdex also runs without a UTF-8 locale.

mlab is unaffected only because its daemon is launched from a login shell
that exports `LANG=en_US.UTF-8` / `LC_ALL=en_US.UTF-8`.

## 2. Goals

- G1. `pdx serve` guarantees a UTF-8 character locale is exported before any
  tmux command runs and before the tmux server can be spawned, so the
  parsing contract (TAB separators) holds regardless of how the daemon was
  launched (Finder App, launchd, SSH, `pdx start` from any shell).
- G2. Tab-separated tmux parsers never fail silently: a malformed line is
  logged once per call with a hint pointing at the locale.
- G3. Respect an explicit user locale: an existing UTF-8 locale is left
  untouched; an explicit **non**-UTF-8 choice is logged, not overridden.

## 3. Non-goals

- Changing `electron/local-daemon/launch-env.ts` to forward the shell's
  `LANG`. The daemon-side guarantee (G1) covers every launcher, and pane
  shells are login shells that source the user's own rc files anyway.
  Tracked as a possible follow-up (nice-to-have: propagate the user's
  *preferred* UTF-8 locale, e.g. `zh_TW.UTF-8`, instead of `en_US.UTF-8`).
- Replacing the TAB separator with something printable. Session names and
  paths may contain any printable byte; TAB under a UTF-8 locale is the only
  separator tmux is guaranteed to pass through.
- `peer-proxy` helper (`internal/peers/proxyhelper/client.go:70`) builds its
  own minimal env (`PATH`, `HOME`). It does not call tmux; untouched.

## 4. Design

### 4.1 `internal/locale` — `EnsureUTF8()`

New package `internal/locale` (tiny, no deps beyond `os`/`strings`):

```go
// EnsureUTF8 makes sure the process exports a UTF-8 character locale.
// Returns what it did so the caller can log it.
func EnsureUTF8() Result
```

Rules (POSIX precedence for `LC_CTYPE`: `LC_ALL` > `LC_CTYPE` > `LANG`). This
is deliberately the same test tmux itself performs: `tmux.c` sets
`CLIENT_UTF8` by reading `LC_ALL` → `LC_CTYPE` → `LANG` (empty = unset) and
`strcasestr`-matching `UTF-8` / `UTF8` on the *string* — it never consults
`nl_langinfo`, so the locale need not even be installed. The per-exec
alternative (`tmux -u`) is rejected: it fixes only that one client and leaves
the spawned server — and therefore every pane shell and agent — without a
locale.

1. `effective` = first non-empty of `LC_ALL`, `LC_CTYPE`, `LANG`.
2. If `effective` is UTF-8 (`strings.Contains(strings.ToUpper(v), "UTF-8")`
   or `"UTF8"`) → `Result{Action: Kept, Value: effective}`. No change.
3. If `effective` is empty → `os.Setenv("LANG", DefaultLocale)` where
   `DefaultLocale = "en_US.UTF-8"` → `Result{Action: Set, Value: DefaultLocale}`.
   `LANG` is chosen (not `LC_ALL`) so a pane shell's rc file can still
   override it.
4. If `effective` is non-empty and not UTF-8 (e.g. `LC_ALL=C`) →
   `Result{Action: Warned, Value: effective}`. Not overridden: an explicit
   choice is the user's; the warning tells them tmux parsing will break.

`Result` is `{Action locale.Action; Value string}` with
`Action ∈ {Kept, Set, Warned}` and a `String()` for logging.

### 4.2 Call site — `runServe`

`cmd/pdx/main.go` `runServe`: call `locale.EnsureUTF8()` **before**
`config.Load` (it must precede `config.GetTmuxInstance`, the session module's
hook install, and anything else that execs tmux). Log:

- `Set` → `locale: no UTF-8 locale in environment, exported LANG=en_US.UTF-8`
- `Warned` → `locale: WARNING LC_ALL/LC_CTYPE/LANG=%q is not UTF-8; tmux output parsing will break`
- `Kept` → nothing (normal case on mlab; keep the log quiet).

`pdx start` does not need it: it only execs `pdx serve`, which fixes its own
environment. Other subcommands (`hook`, `msg`, `statusline`) run inside a
pane whose env came from the tmux server, which came from the daemon — fixed
transitively. `hook.go:106` uses `|` as separator and is not affected either
way.

### 4.3 Parser defence

`internal/tmux/executor.go` `RealExecutor.ListSessions`: a line with fewer
than 3 fields is **skipped** (do not fail the whole list — a single bad line
must not take down the sessions API). Malformed lines are counted and
reported **once per call**, not per line — `ListSessions` runs on every
watcher tick and under the locale bug every line is malformed:
`log.Printf("tmux list-sessions: %d malformed line(s), e.g. %q (expected 3 tab-separated fields; is a UTF-8 locale exported?)", n, first)`. The current behaviour of silently filling
`Name`/`Cwd` with `""` goes away: a line with <3 fields is malformed.

`internal/module/session/service.go` `ListSessions`: the
`continue // skip sessions with invalid IDs` gets a `log.Printf` with the
offending ID. `GetSession` (`service.go:82-110`) has no such skip and does
not change.

Skip-not-fill matters beyond diagnostics: with the malformed lines dropped
the ID list handed to `CleanOrphans` is empty and it no-ops
(`store/meta.go:214`), instead of deleting every existing meta row as it does
today.

`internal/module/monitor/tmux_panes.go` already returns an error on a
malformed line; the error text gets the same locale hint appended.

Parsing is extracted so it is unit-testable without tmux:
`parseListSessionsOutput(out string) []TmuxSession` in `executor.go`.

## 5. Acceptance

1. Unit: `locale.EnsureUTF8()` table test covering: nothing set; `LANG=en_US.UTF-8`;
   `LC_ALL=C` (warned, untouched); `LC_CTYPE=UTF-8` (macOS Terminal form, kept);
   `LANG=zh_TW.utf8` (kept, lowercase form); `LANG=C` + `LC_ALL=en_US.UTF-8`
   (kept — LC_ALL wins). Tests use `t.Setenv`.
2. Unit: `parseListSessionsOutput` with a good 3-field line, a `_`-mangled
   line (skipped, logged), a 2-field line (skipped), blank lines.
3. Manual (air-2026): App → start daemon (Finder env) → `GET /api/sessions`
   lists tmux sessions; `POST /api/sessions` returns 201; `pdx.log` shows the
   `exported LANG` line; `tmux show-environment -g LANG` on the daemon-started
   server prints `en_US.UTF-8`; `echo $LANG` inside a Purdex pane is UTF-8;
   `echo 中文 ─┼─` inside a Purdex pane renders correctly (relay client).
4. Manual (mlab): unchanged behaviour, no `locale:` line in the log.
