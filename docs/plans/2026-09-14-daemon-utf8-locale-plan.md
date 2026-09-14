# Plan — Daemon UTF-8 Locale

Spec: `docs/specs/2026-09-14-daemon-utf8-locale-spec.md`
Branch: `worktree-daemon-utf8-locale`
Worktree: `/Users/wake/Workspace/wake/purdex/.claude/worktrees/daemon-utf8-locale`

TDD throughout: write the failing test first, then the code. One commit per
task. Every `go test` / `go build` runs from the worktree root.

## Task 1 — `internal/locale` package (spec §4.1)

Files: `internal/locale/locale.go`, `internal/locale/locale_test.go` (new).

1. Test first (`t.Setenv` for `LC_ALL`, `LC_CTYPE`, `LANG` — set all three
   explicitly per case, empty string means unset via `os.Unsetenv` after
   `t.Setenv` registered the restore):

   | case | LC_ALL | LC_CTYPE | LANG | expect Action | expect Value | env after |
   |---|---|---|---|---|---|---|
   | nothing set | – | – | – | Set | `en_US.UTF-8` | `LANG=en_US.UTF-8` |
   | LANG utf8 | – | – | `en_US.UTF-8` | Kept | `en_US.UTF-8` | unchanged |
   | LANG lowercase utf8 | – | – | `zh_TW.utf8` | Kept | `zh_TW.utf8` | unchanged |
   | LC_CTYPE macOS form | – | `UTF-8` | – | Kept | `UTF-8` | unchanged |
   | LC_ALL wins over LANG=C | `en_US.UTF-8` | – | `C` | Kept | `en_US.UTF-8` | unchanged |
   | LC_ALL=C explicit | `C` | – | `en_US.UTF-8` | Warned | `C` | unchanged |
   | LANG=C only | – | – | `C` | Warned | `C` | unchanged |

2. Implement `EnsureUTF8() Result`, `Result{Action, Value}`,
   `Action` enum `Kept/Set/Warned` with `String()`, `const DefaultLocale = "en_US.UTF-8"`,
   helper `isUTF8(v string) bool` (upper-cased contains `UTF-8` or `UTF8`).
3. `go test ./internal/locale/` green. Commit:
   `feat(locale): EnsureUTF8 exports LANG=en_US.UTF-8 when no UTF-8 locale is set`.

## Task 2 — wire into `runServe` (spec §4.2)

Files: `cmd/pdx/main.go`.

1. In `runServe`, immediately after `fs.Parse(args)` (`main.go:92`) and
   before the `// 1. Load config` step:

   ```go
   // 0. Locale — tmux sanitises TAB out of -F output under a non-UTF-8
   // client locale, which breaks every tab-separated parser below, and
   // the tmux server we may spawn inherits this env. Must precede any
   // tmux exec (GetTmuxInstance, hook install).
   switch r := locale.EnsureUTF8(); r.Action {
   case locale.Set:
       log.Printf("locale: no UTF-8 locale in environment, exported LANG=%s", r.Value)
   case locale.Warned:
       log.Printf("locale: WARNING LC_ALL/LC_CTYPE/LANG=%q is not UTF-8; tmux output parsing will break", r.Value)
   }
   ```
2. `go build ./... && go vet ./cmd/pdx/`. No unit test (log-only glue); the
   package test in Task 1 is the coverage. Commit:
   `fix(daemon): ensure a UTF-8 locale before the first tmux exec`.

## Task 3 — parser defence in `RealExecutor.ListSessions` (spec §4.3)

Files: `internal/tmux/executor.go`, `internal/tmux/list_sessions_parse_test.go` (new).

`executor_test.go` is `package tmux_test` (external) and cannot see an
unexported helper — put the new test in a **new file with `package tmux`**
(precedent: `metadata_test.go`). Plain `testing`, no testify, matching the
package. `executor.go` has no `"log"` import yet — add it.

1. Test first — `TestParseListSessionsOutput` cases:
   - `"$1\tfoo\t/Users/wake\n$2\tbar baz\t/tmp/x y\n"` → two sessions, names
     and cwd exact (spaces preserved).
   - `"$0_probe1_/Users/wake\n"` (locale-mangled) → zero sessions.
   - `"$3\tonly-two-fields\n"` → zero sessions.
   - `"\n\n$4\tq\t/\n\n"` → one session.
   - `"$0_a_/x\n$1_b_/y\n$2\tc\t/z\n"` → one session (`$2`), and the log
     contains exactly **one** `malformed line` message saying `2 malformed`.
   - Log assertion: redirect `log` output (`log.SetOutput` to a buffer,
     restore in `t.Cleanup`) and assert it contains `malformed line` and
     `UTF-8 locale` for the mangled case; assert `strings.Count(buf, "malformed")==1`
     for the mixed case; assert the buffer is empty for the all-good case.
2. Extract `parseListSessionsOutput(out string) []TmuxSession`; `ListSessions`
   calls it after the existing error handling. Lines with `len(parts) < 3`
   are counted and skipped; **one** `log.Printf` per call with the count and
   the first offending line (spec §4.3).
3. `go test ./internal/tmux/` green. Commit:
   `fix(tmux): log and skip malformed list-sessions lines instead of mis-parsing them`.

## Task 4 — log the silent skips (spec §4.3)

Files: `internal/module/session/service.go`,
`internal/module/monitor/tmux_panes.go`, `internal/module/monitor/process_test.go`
(the pane-parser test lives there — `process_test.go:53` asserts only
`Contains(err, "malformed tmux pane line 1")`, so appending the hint is safe).

1. `service.go` `ListSessions` only (`GetSession` has no skip): replace the
   bare `continue` with
   `log.Printf("session: skipping tmux session with invalid id %q: %v", s.ID, err); continue`.
2. `tmux_panes.go` `parseTmuxPaneListOutput`: append
   ` (is a UTF-8 locale exported?)` to the "expected 4 fields" error. Update
   the existing test expectation if one asserts the exact message.
3. `go test ./internal/module/session/ ./internal/module/monitor/` green.
   Commit: `fix(session,monitor): surface malformed tmux lines in the log`.

## Task 5 — verify

1. `go build ./... && go vet ./... && go test ./...` from the worktree root.
2. Local repro (mlab, no App needed), fully isolated from the live daemon
   — **own tmux server via `TMUX_TMPDIR`, own data dir via `--config`**, so
   the live daemon's tmux hooks / DBs are never touched.

   ⚠️ **This shell runs inside a tmux pane, so `$TMUX` is set. tmux resolves
   the socket from `$TMUX` *before* `TMUX_TMPDIR`; a bare `tmux kill-server`
   here kills the production mlab server.** Every host-side tmux call below
   therefore uses `env -u TMUX TMUX_TMPDIR=$SCR/tmux tmux …`. The daemon is
   safe because `env -i` drops `TMUX`.

   - `SCR=<scratchpad>/utf8-smoke; mkdir -p $SCR/data $SCR/tmux $SCR/upload`
   - write `$SCR/config.toml`: `bind="127.0.0.1"`, `port=7899`,
     `token="smoke"`, `data_dir="$SCR/data"`, `upload_dir="$SCR/upload"`,
     `host_id="smoke:000000"` (keys per `config.go:207-212`).
   - `T() { env -u TMUX TMUX_TMPDIR=$SCR/tmux tmux "$@"; }`
   - **Old binary first** (prove the repro): the main checkout's
     `/Users/wake/Workspace/wake/purdex/bin/pdx` is a pre-fix build — copy it
     read-only: `cp /Users/wake/Workspace/wake/purdex/bin/pdx $SCR/pdx-old`
     (no VCS commands, no cd into the main checkout). Run
     `env -i HOME=$HOME USER=$USER TMUX_TMPDIR=$SCR/tmux PATH=/opt/homebrew/bin:/usr/bin:/bin $SCR/pdx-old serve --config $SCR/config.toml > $SCR/serve-old.log 2>&1 &`,
     `curl -H 'Authorization: Bearer smoke' -d '{"name":"smoke1","cwd":"'$HOME'"}' http://127.0.0.1:7899/api/sessions`
     → expect 500 `session created but not found`; `GET` → `[]`.
     Then kill that daemon; **`T kill-server`** — the server `new-session`
     created has no `LANG` and must not survive into the next run.
   - **New binary**: `go build -o bin/pdx ./cmd/pdx` in the worktree, same
     `env -i … ./bin/pdx serve --config $SCR/config.toml > $SCR/serve.log 2>&1 &`
     (`pdx serve` logs to stdout/stderr only; `logs/pdx.log` exists only via
     `pdx start`). `POST` → 201 with `"name":"smoke1"`; `GET` → one entry;
     `grep 'exported LANG=en_US.UTF-8' $SCR/serve.log`;
     `T show-environment -g LANG` → `LANG=en_US.UTF-8`.
   - Teardown: kill the daemon, `T kill-server`, `rm -rf $SCR`.
3. Air-2026 acceptance (spec §5.3) after the App picks up the new binary —
   done by the user / main session, not by the subagent.

## Out of scope / follow-up issues to open after merge

- `launch-env.ts` forwarding the shell's own `LANG` (spec §3).
