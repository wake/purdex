# Plan — P-D teardown (P-D.1 / P-D.2 in full, P-D.3 outline)

Spec: `docs/specs/2026-09-18-pd-teardown-spec.md` v1.1. Every line number
below was measured on `fc39dcc5` (alpha.392, 2026-09-18). Codex plan review
`task-mu6xgcjb-7b7n51` applied (v1.1 of this plan): ordering fixed so every
commit builds — **packages are deleted before the config / struct fields
they read are removed**, and SPA consumers are unwired before the files
they import are deleted.

Working rules: one worktree (`worktree-pd-teardown`), one branch per phase
cut from `origin/main` after the previous phase's bump merges; TDD for
every surviving-file edit (the deleting commits carry no tests of their
own — the test is `go build ./... && go test ./...` staying green); each
task is one commit made with `git commit --only <files>`; **`go build
./...` after every commit**; subagents prefix every Bash with
`cd <worktree> &&`.

## P-D.1 — daemon M0 (branch `worktree-pd-teardown`, with the docs commit)

### T1.1 unwire `main.go`

Delete `c.AddModule(execution.New())` / `c.AddModule(dispatch.New())`
(`cmd/pdx/main.go:293-294`) and imports `:24-25`. `go build ./cmd/pdx`
must pass with the packages still on disk (proves nothing else in `cmd/`
references them).

### T1.2 delete packages

`git rm -r internal/module/execution internal/module/dispatch`.
`internal/tmuxenv/tmuxenv.go:135` comment cites
`module/execution/launcher.go:166` — reword to "a legacy launcher" so the
reference does not dangle (comment-only, same commit).
`go build ./... && go vet ./... && go test ./...` green.

### T1.3 config: drop `DispatchConfig` (TDD) — only after T1.2

Test first, in `internal/config/config_test.go`:
- `TestLoadIgnoresUnknownDispatchTable`: write a temp TOML with
  `[dispatch]\nallowed_repo_roots = ["/x"]` plus a known top-level key
  (`port = 7861`), `Load` it, assert no error and `Port == 7861`. (This is
  the compatibility guarantee for existing `config.toml` files.)
- Delete the `Dispatch` assertions in `peers_test.go:169,190,207,243`
  (they test `Clone` deep-copies a field that no longer exists).

Then `config.go`: delete `DispatchConfig` (lines 154–~175, comment block
included), the `Dispatch` field (`:257`), the `Clone` line (`:231`).
`dispatch/module.go:239,252` and `containment_test.go:62` were the only
other readers and are gone since T1.2.

### T1.4 docs banner

Prepend to `docs/specs/m0-ploom-handoff.md`:
`> **Deprecated (2026-09-18).** M0 execution/dispatch were removed in P-D.1
> (spec `2026-09-18-pd-teardown-spec.md`); Ploom's integration point is
> Nexen. Kept for history.`

### T1.5 acceptance (spec §6 daemon list) + PR + review

Per-commit build check, `make build` → restart mlab daemon → `/api/health`
hash → the config route (`internal/core/config_handler.go`) has no
`dispatch` key → hand-to-nex / take-back once. PR → codex R1 → attacker →
critic. Bump PR.

## P-D.2 — daemon Stream / relay / bridge (branch `worktree-pd2-stream`)

Ordered so the tree builds after every commit: the three packages go
first (they are the last readers of `Cfg.Stream.Presets`
— `stream/handler.go:273` — and of `CCSessionID`/`CCModel` —
`stream/handler.go:341`, `stream/orchestrator.go:135`).

### T2.1 unwire `main.go` + delete packages

`main.go`: `case "relay"` (`:49`), `runRelay` (`:310-345`),
`c.AddModule(stream.New())` (`:271`), imports `:33,35`, **and `relay` in
the usage string at `:44`**; drop now-unused `flag`/`signal`/`syscall`
imports only if the compiler says so. Then
`git rm -r internal/module/stream internal/relay internal/bridge`.
`internal/module/session/locks.go:7,14` doc comment → nex only.
`internal/agent/cc/interfaces.go:5` "for use by stream module" → "for use
by the nex module". Leave `core/topo_test.go` and `core/events_test.go`
fixtures alone (spec I4 guards import paths, not vocabulary).

Grep guard: `grep -rn 'internal/module/stream"\|internal/relay"\|internal/bridge"\|internal/module/execution"\|internal/module/dispatch"' internal cmd` → 0.

### T2.2 session module: mode narrows to `terminal` (TDD)

`internal/module/session/handler_test.go`:
- `:484-501` (create with `"mode":"stream"`) → becomes
  `TestCreate_CoercesLegacyStreamMode`: 2xx, stored and returned
  `Mode == "terminal"`; add `TestCreate_RejectsUnknownMode` (`"jsonl"` →
  400, body contains `must be terminal`).
- `:798-810`, `:838` (`/mode` switch tests) → delete;
  add `TestSwitchModeRouteGone`: `POST /api/sessions/{code}/mode` → 404 via
  the module's mux.
- `:35,45,124,181,189,205,220` fixtures with `Mode: "stream"` → `"terminal"`
  (they test listing/meta merge, not the value). `module_test.go:74` same.

`handler.go`: `:93-101` accept `""|"terminal"|"stream"`, normalise to
`terminal` before use (error text for anything else:
`invalid mode: must be terminal`); delete `switchModeRequest`,
`handleSwitchMode` (`:304-352`), route `module.go:90`.

### T2.3 session/store: drop `cc_session_id`, `cc_model`, `has_relay` (TDD)

`internal/store/meta_test.go`: remove `CCSessionID` from `:35,39,77,78`;
add `TestMetaStore_OldSchemaWithExtraColumns`: create the table by hand
with the alpha.392 columns (`cc_session_id`, `cc_model`), open a
`MetaStore` on it, `SetMeta` + `UpdateMeta` + `GetMeta` + list succeed.
`internal/module/session/handler_test.go:33` builds `SessionMeta.CCModel`
and `:46` asserts `SessionInfo.CCModel` — remove both.

`internal/store/meta.go`: remove the two fields from `SessionMeta` (`:17`)
and `MetaUpdate` (`:25`), the two columns from `CREATE TABLE` (`:74-75`),
`INSERT … ON CONFLICT` (`:220-227`), both `SELECT`s (`:235-237`,
`:250-260`) and the `UpdateMeta` clauses (`:277-279` and the `CCModel`
sibling).

`internal/module/session/provider.go`: remove `CCSessionID`/`CCModel`
(`:45-46`) and `HasRelay` (`:50`) from `SessionInfo`; `CCSessionID`/`CCModel`
from `MetaUpdate` (`:56-57`). `service.go:72-73,114-115,146-147` follow.
`agent/handler.go:1228` still reads `sess.CCSessionID` at this point —
**T2.3 and T2.4 are one commit** (or T2.4 goes first); pick T2.4-first.

### T2.4 agent: drop the history endpoint (TDD) — commit before T2.3

`internal/module/agent`: grep `/history` in `*_test.go`; if a test exists,
flip it to `TestHistoryRouteGone` → 404, else add it. Delete
`handleHistory` (`handler.go:1174-1240`), the route (`module.go:278`),
`agent.HistoryProvider` (`internal/agent/provider.go:254-257`) and
`StreamCapable` (`:259-263`, reserved-never-implemented),
`internal/agent/cc/history.go`, `CCHistoryProvider` + `HistoryKey`
(`cc/interfaces.go:13-20`) and `registry.Register(HistoryKey, …)`
(`cc/provider.go:86`).

### T2.5 config: drop `StreamConfig` / `Preset` (TDD)

Tests: `config_test.go:62-105` (`TestLoadConfigWithPresets`, default
preset) → replace with `TestLoadIgnoresUnknownStreamTable` (same shape as
T1.3's). `peers_test.go:167,189,204,240` Stream lines → delete.
`internal/core/config_handler_test.go` uses `stream.presets` as *the*
PATCH payload (`:26,148-168,264-273,525-552`) — switch those to another
patchable field that `config_handler.go:20-100` already accepts (e.g.
`Detect.PollInterval`); the rollback test at `:525` keeps its intent on
the new field.

Code: `config.go:23-25` `StreamConfig`, `Preset` (find its def), field
`:252`, default `:269-274`, `Clone` `:227`. `core/config_handler.go:26`
`Stream` patch field and `:92-94`.

### T2.6 composed-mux 404 regression test (TDD, cmd/pdx)

`cmd/pdx/http_chain_test.go` already has `newTestCore` + `doRequest`
helpers. Add `TestRemovedRoutesAre404` that registers the real serve
modules (`registerServeModules` with nil stores — if a module refuses nil
stores in tests, register only `session` + `agent` + `nex` and say so in
the test comment) and asserts 404 with an admin Bearer for:
`POST /api/sessions/x/handoff`, `POST /api/sessions/x/mode`,
`GET /api/sessions/x/history`, `GET /ws/cli-bridge/x`,
`GET /ws/cli-bridge-sub/x`, and one M0 route each from execution and
dispatch (take the paths from the deleted `module.go` files via
`git show fc39dcc5:internal/module/execution/module.go`).

### T2.7 CLAUDE.md line + acceptance + PR

`CLAUDE.md` overview sentence per spec §3. Acceptance = spec §6 daemon
list incl. per-commit build, item 7 (scratch daemon on a copy of live
`meta.db`), item 8 (persisted stream pane → error state, no crash) and the
`pdx` subcommand list vs usage string. PR → R1 → attacker → critic → bump.

## P-D.3 — SPA (outline; firmed into its own plan after P-D.2 merges)

Commit order fixed by spec §3 v1.1 — `tsc --noEmit` after every commit:
(1) pure type move `lib/stream-ws.ts` → `lib/nex/message-types.ts` with
byte-comparison proof; (2) unwire every consumer listed in the spec
(including `route-utils.ts`, `snapshot/{types,restore}.ts`, device-state
restore, `useNotificationDispatcher.ts:355`); (3) delete the Stream
family; (4) `PaneContent.mode` literal `'terminal'` + persist version 3 +
`migrateTabStore` `< 3` step + test; (5) `Session` type purge iff
mechanical. Measurements to take first: who calls `switchMode`
(`host-api.ts:294`) besides the dropdown; the device-state restore path's
`mode` source; whether `SessionPaneContent`'s `key={…}-${mode}` still
needs the mode suffix.

## Deploy / bump per phase

`make build` → `./bin/pdx stop` → `env PDX_DEV_MODE=1 ./bin/pdx start` →
`/api/health`. Bump = `VERSION` + `package.json` + `spa/package.json` +
CHANGELOG (繁中 narrative). Before each bump `git show origin/main:VERSION`
(parallel sessions: peer-pairing-d4, host-floating-panels).
air26 stays on 391 unless the user says otherwise.
