# Interface language U1-1 — mod event channel — plan

Spec: `docs/specs/2026-10-08-interface-u1-spec.md` §3 (facts M-U1-1…3), §6 (contract). Two PRs:

- **PR U1-1a** — daemon: `internal/modevents` (socket path, wire, registry, listener, handler), module `internal/module/modevents`, extractor writes `mod_socket` into `pdx.json`.
- **PR U1-1b** — mod: `hooks/events.js` reporter wired into `register.js`; Go guard against duplicate unmatched registrations; daemon read API.

Format: contracts, rules, named tests and mutation gates; implementers write the code (TDD: each named test is written red first). Line numbers are as of `96c94db3`; re-check before editing.

Common rules:
- Go: `make lint` clean (gofmt incl. doc comments; see memory on gofmt quote rewriting — use code blocks, not `''`, in comments). `go test ./...` for the touched packages, `-race` for `internal/modevents`.
- Mod: `claude plugin validate cmd/pdx/plugin/purdex --strict`, `claude plugin test cmd/pdx/plugin/purdex` (runs `hooks/*.test.ts`), `go test ./cmd/pdx/plugin/`. These are manual (not in CI) and must be run and quoted in the PR body.
- Each task is one commit; parallel subagents in one worktree commit with `git commit --only <files>`.

---

## PR U1-1a — daemon side

### Task A1 — `internal/modevents`: socket path and wire types

Files: `internal/modevents/path.go`, `wire.go`, `*_test.go`.

- `func SocketPath(dataDir string) (path string, ok bool)` → `filepath.Join(abs(dataDir), "mod.sock")`, `ok=false` when `len(path) > 100` (spec §6.1). Pure; no I/O.
- Wire (spec §6.2): `type Batch struct { V int; Stream, Agent, CCVersion, ModVersion string; Dropped int; Events []Event }` and `type Event struct { Seq int64; At int64; SID string; Type string; Data json.RawMessage }` with the JSON names of §6.2 (`cc_version`, `mod_version`, `sid`, …).
- `func DecodeBatch(r io.Reader) (Batch, error)` returns an `*WireError{Code}` with the §6.2 codes: `bad_json`, `unsupported_version` (`v ≠ 1`), `bad_stream` (`^[A-Za-z0-9_-]{8,64}$`), `bad_events` (0 or > 500), `bad_seq` (not strictly increasing, or ≤ 0), `bad_sid` (lowercase UUID `^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`). Unknown top-level and event fields are ignored.
- `KnownTypes` = the 14 v1 types of §6.3 (`session.start`, `session.clear`, `session.end`, `turn.start`, `turn.complete`, `tool.check`, `tool.start`, `tool.end`, `agent.spawn`, `compact.start`, `compact.end`, `usage`, `background`, `heartbeat`).

Tests: `TestSocketPath_DefaultFits`, `TestSocketPath_TooLongIsNotOK` (a 120-byte dir), `TestDecodeBatch_Codes` (table: one case per code + a valid batch), `TestDecodeBatch_IgnoresUnknownFields`.
Mutation gates: drop the length check → `TooLongIsNotOK` red; accept equal seqs → the `bad_seq` row red.

### Task A2 — registry and bus

File: `internal/modevents/registry.go`, `registry_test.go`.

- `type Registry` (zero-value unusable; `NewRegistry(now func() time.Time)`), safe for concurrent use.
- `Apply(b Batch) (ack int64)`: under the stream's own mutex — create the stream on first sight; update `agent, cc_version, mod_version, last_seen`, `dropped += b.Dropped`; for each event in order: `seq ≤ last_seq` → skip; `seq > last_seq+1` → `gaps++`; set `last_seq`; `sid` → stream's latest sid; `session.start` → `cwd`, `interactive=true`; `session.end` → `ended=true`, `ended_at`; count by type (`unknown` for types not in `KnownTypes`); append known events to a 256-entry ring; **deliver known events to subscribers synchronously, in order, still under the stream mutex**. Returns the stream's `last_seq`.
- `Reject(stream string)` → `rejected++` (the handler calls it on a 400 whose stream id is valid).
- `Subscribe(fn func(StreamInfo, Event)) (cancel func())`. Subscriber panics are recovered and logged, never break `Apply`.
- `Streams() []StreamInfo` (sorted by `last_seen` desc), `Events(stream string, after int64) ([]Event, bool)`, `BySID(sid string) (StreamInfo, bool)` (the newest stream whose latest sid matches).
- Eviction on every `Apply` and on a 1-minute ticker started by the module: ended streams 30 min after `ended_at`; any stream 2 h after `last_seen`; above 256 streams, oldest `last_seen` first. `now` is injected for tests.

Tests: `TestApply_DedupesRetriedEvents` (same batch twice → delivered once, ack unchanged), `TestApply_CountsGaps`, `TestApply_UnknownTypeCountedNotDelivered`, `TestApply_OrderPerStreamUnderConcurrency` (two goroutines × 2 streams, `-race`, subscriber records per-stream seqs strictly increasing), `TestSubscribe_PanicIsContained`, `TestEviction_EndedAndIdleAndCap`, `TestBySID_FollowsClear` (a `session.clear` event moves the stream's sid).
Mutation gates: remove the `seq ≤ last_seq` skip → `DedupesRetriedEvents` red; deliver outside the mutex → `OrderPerStreamUnderConcurrency` flaky-red under `-race -count=50` (record the run).

### Task A3 — listener and handler

Files: `internal/modevents/listen.go`, `peercred_darwin.go`, `peercred_linux.go`, `handler.go`, tests.

- `func Listen(path string) (net.Listener, Status)`; `Status{Enabled bool; Reason string}` with reasons `path_too_long` (caller passes `ok=false`), `in_use`, `not_a_socket`, `listen_failed`. Existing path: `Lstat`; socket → `net.DialTimeout("unix", path, 200ms)`: success → `in_use` (close the probe conn); failure → `os.Remove` then listen; not a socket → `not_a_socket` and **never removed**. After listen: `os.Chmod(path, 0o600)`; failure → close + `listen_failed`.
- The returned listener wraps accepts with a peer-uid check: `peerUID(conn) (uint32, error)` (darwin `unix.GetsockoptXucred(fd, SOL_LOCAL, LOCAL_PEERCRED)`, linux `unix.GetsockoptUcred(fd, SOL_SOCKET, SO_PEERCRED)`); mismatch or error → close the conn and keep accepting. `peerUID` is a package var (test seam).
- `func NewHandler(reg *Registry) http.Handler`: only `POST /mod/v1/events` (others 404, wrong method 405); `http.MaxBytesReader` 1 MiB → 413 `{"error":"too_large"}`; `DecodeBatch` error → 400 `{"error":"<code>"}` (and `reg.Reject(stream)` when the stream id itself was valid); success → 200 `{"ack":N}`. Content type of responses `application/json`.
- `func NewServer(h http.Handler) *http.Server` with the §6.1 timeouts.

Tests (socket under a short temp dir, e.g. `os.MkdirTemp("/tmp", "pdxm-")`, removed in cleanup — `t.TempDir()` on macOS is too long for a socket path):
`TestListen_CreatesSocket0600`, `TestListen_RemovesStaleSocket`, `TestListen_InUseLeavesItAlone`, `TestListen_RefusesNonSocket` (a regular file at the path survives), `TestListen_RejectsOtherUID` (seam returns uid+1 → the client sees EOF/reset, handler never runs), `TestHandler_AcksAndDedupes` (end to end over the socket with `http.Client{Transport: &http.Transport{DialContext: unix dial}}`), `TestHandler_ErrorCodes` (400 codes, 405, 404, 413).
Mutation gates: skip chmod → `CreatesSocket0600` red; skip the uid compare → `RejectsOtherUID` red; remove non-socket guard → `RefusesNonSocket` red.

### Task A4 — module

Files: `internal/module/modevents/module.go`, `module_test.go`; `cmd/pdx/main.go` (`registerServeModules` adds it).

- Name `modevents`, no dependencies. `Init`: `reg := modevents.NewRegistry(time.Now)`; `c.Registry.Register("modevents", reg)`; compute `SocketPath(c.Cfg.DataDir)`.
- `Start(ctx)`: `Listen`; if enabled, serve in a goroutine (`srv.Serve(l)`, `http.ErrServerClosed` ignored, other errors logged); start the eviction ticker; log one line `[modevents] socket <path>` or `[modevents] disabled: <reason>`. Never fails the daemon.
- `Stop(ctx)`: `srv.Shutdown(ctx)` then close the listener (unlinks the file); stop the ticker. Idempotent.
- `Status() Status` and `SocketPathForInfo() string` for the read API (U1-1b).
- `RegisterRoutes`: none in U1-1a.

Tests: `TestModule_StartServesAndStopUnlinks`, `TestModule_RestartRebinds` (Start → Stop → new module Start on the same path works), `TestModule_DisabledPathDoesNotFail` (data dir > 100 bytes → Start returns nil, Status reason `path_too_long`).

### Task A5 — extractor writes `mod_socket`

Files: `internal/agent/cc/plugin.go` (`writePdxJSON`), `plugin_test.go`.

- `writePdxJSON` adds `"mod_socket": <path>` when `modevents.SocketPath(dataDir)` is ok; omits it otherwise. No other field changes.
- Tests: extend the existing pdx.json test: `TestExtractPlugin_PdxJSONHasModSocket`; `TestExtractPlugin_PdxJSONOmitsModSocketWhenTooLong`.
- Import direction: `internal/agent/cc` → `internal/modevents` (modevents imports nothing from `internal/agent`). Add a compile-time check if a cycle appears.

PR U1-1a acceptance (before PR): unit tests above; `make build`; run the built binary against a throwaway `--config` (data dir under `/tmp/pdxu1a-*`) and `curl --unix-socket <path> -d '<batch>' http://pdx/mod/v1/events` → `{"ack":…}`, socket mode `srw-------`.

---

## PR U1-1b — mod reporter and read API

### Task B1 — `hooks/events.js`

Files: `cmd/pdx/plugin/purdex/hooks/events.js`, `hooks/events.test.ts`.

Module state `ev = { on: false, sock: '', stream: '', seq: 0, sid: '', ccVersion: '', modVersion: '', queue: [], dropped: 0, inflight: false, scheduled: false, backoffMs: 0, turnId: '', asks: new Set(), compacting: false, beat: null }`.

Exports:
- `registerEvents(on)` — registers, without matchers, `tool.call`, `tool.check`, `agent.spawn`, `session.measure`, `session.end`, `classic.Stop`. Each hook awaits `next(e)` (tool.call: enqueue `tool.start` before, `tool.end` after; compact is not here), returns its result unchanged, and has `.catch((_$, e, next) => next(e))`-style pass-through so a reporter failure never alters the engine. Hooks do nothing when `ev.on` is false.
- Observers for `register.js` (all no-ops when `ev.on` is false, none throws): `async evSessionStart($, e)` (interactive only: read `pdx.json` → `mod_socket`; absent → stay off; read `VERSION`; `ccVersion = await $.session.version()`; `sid = await $.session.id()`; `stream` = 22 chars from `crypto.getRandomValues` mapped to `[A-Za-z0-9_-]`; enqueue `session.start {cwd, surface}`; start the heartbeat `$.clock.every(10_000, …)`); `evTurnStart(e)` (main turn: `turnId = e.turnId`; enqueue `turn.start`); `evTurnComplete(e)` (enqueue `turn.complete {turn_id, reason, agent_id?, duration_ms, aborted}`; main turn clears `turnId` and `asks`); `async evClear($)` (`prev = sid`, `sid = await $.session.id()`, enqueue `session.clear {prev_sid}`, reset `turnId`/`asks`/`compacting`); `evCompactStart(e)` / `evCompactEnd(ok)`.
- Data per spec §6.3. `tool.check`: enqueue `{tool, tool_use_id?, agent_id?, decision}` from the verdict `next(e)` resolved to; decision `ask` adds `tool_use_id` to `asks` (when present). `tool.end` removes it. `AskUserQuestion` / `ExitPlanMode` `tool.start` also add to `asks`.
- Heartbeat data: `{turn_id?, asks: [...], compacting, agents: (await $.agent.list()).map(a => ({id, status}))}` (on failure `agents: []`).
- Queue / flush exactly as spec §6.5: flush timer 150 ms via `$.clock.after`; ≤ 200 events per POST to `http://pdx/mod/v1/events` with `socketPath: ev.sock`, body `{v:1, stream, agent:'cc', cc_version, mod_version, dropped, events}`; 200 → drop `seq ≤ ack`, `dropped = 0`, `backoffMs = 0`, reschedule if non-empty; 400 → drop the batch; other status or throw → `backoffMs = min(max(1000, backoffMs*2), 30000)` and reschedule after it. Queue cap 1000 (drop oldest, `dropped++`).
- `session.end` hook: enqueue `session.end {reason}` with `sid = e.sessionId`, then `await flush($, {final: true})` inside the hook; cancel the heartbeat.
- `$` is passed only to top-level function declarations (M-U1-2).

Tests (`events.test.ts`, kit; a fake daemon via `on('http.fetch', …)` recording `{url, init}` and answering `{value:{status, ok, headers:{}, text}}`; `mock.clock(on)`; `on('fs.read')` serving `pdx.json` with `mod_socket` and `VERSION`; `on('session.id')`, `on('session.version')`, `on('agent.list')`). Drive through the whole mod (the kit loads `register.js`), so integration with Task B2 is covered:
`posts session.start then turn events in seq order to the socket from pdx.json`, `a headless session reports nothing`, `no mod_socket in pdx.json → no fetch at all`, `a 200 ack drops acked events and the next batch starts after them`, `a failed POST is retried with backoff and nothing is lost`, `a 400 drops the batch`, `queue overflow drops the oldest and reports dropped`, `heartbeat every 10 s carries turn_id, asks and agents`, `tool.check ask then tool.end clears the ask`, `a subagent turn.complete carries agent_id and keeps the main turn_id`, `after /clear events carry the new sid and session.clear names the old one`, `session.end flushes inside the hook`, `a fetch that throws never changes the engine's result` (tool.call result passes through unchanged).
Mutation gates: ack ignored (drop all on 200) → the retry/ack tests red; heartbeat not started → heartbeat test red; sid not refreshed on clear → clear test red.

### Task B2 — wire into `register.js`

File: `cmd/pdx/plugin/purdex/hooks/register.js` (+ `relay.test.ts` only if an existing fake needs `http.fetch`).

- `import { registerEvents, evSessionStart, evTurnStart, evTurnComplete, evClear, evCompactStart, evCompactEnd } from './events.js'`; `registerEvents(on)` right after `registerAsk(on)`.
- `session.start`: in the interactive branch, `await evSessionStart($, e)` after the existing pdx.json read.
- `turn.start`: `evTurnStart(e)` first thing (before the nonce logic, every turn).
- `turn.complete`: after `next(e)` resolves and **before** the subagent skip, `evTurnComplete(e)`.
- `classic.SessionStart`: when `e.source === 'clear'`, `await evClear($)` before the relay handling.
- `session.compact`: move the current body into `async function relayCompact($, e, next)` unchanged; the hook becomes `evCompactStart(e); let ok = false; try { const r = await relayCompact($, e, next); ok = !(r && r.skip); return r } finally { evCompactEnd(ok) }`.
- No other change to relay logic. `relay.test.ts` / `ask.test.ts` stay green unchanged (their `pdx.json` has no `mod_socket`, so the reporter is off).

### Task B3 — guard test and embed list

File: `cmd/pdx/plugin/embed_test.go`.

- File list gains `hooks/events.js`; pin `import { registerEvents` … `from './events.js'` and `registerEvents(on)` in `register.js`.
- `TestHooks_NoEventRegisteredTwiceWithoutMatcher`: scan every `hooks/*.js` for `on('<event>', ` where the second argument does not start with `{`; a repeated event name across all files → fail naming both files. Also fail if a file registers the same event with the same matcher literal twice.
- Mutation gate: add a second unmatched `on('turn.complete'` to `events.js` → red.

### Task B4 — read API

Files: `internal/module/modevents/module.go` (`RegisterRoutes`), `api.go`, `api_test.go`.

- `GET /api/mod/streams` → spec §6.6 body; times as RFC 3339 UTC; `counts` as an object.
- `GET /api/mod/streams/{stream}/events?after=<seq>` → `{"events": [...]}` (ring after `seq`, default 0); unknown stream 404 `{"error":"no_stream"}`; bad `after` 400.
- Routes sit on the normal mux, so they get TokenAuth like every `/api/*` route.
- Tests: `TestAPI_StreamsListsSocketAndStreams`, `TestAPI_EventsAfter`, `TestAPI_UnknownStream404`.

### Acceptance and deploy (after U1-1b merges and is bumped)

1. Ask `mlab/_z9ruk0` (deploy coordinator) for the go-ahead and check for running workers; stop if any are running.
2. Build origin/main detached; swap `bin/pdx` in the main checkout (rm → cp → mv, new inode); restart the daemon; confirm `GET /api/health` and the `[modevents] socket …` log line.
3. Back up `~/.claude/settings.json` (0600, scratchpad, never printed); `pdx setup --agent cc`; compare key paths (only `env.CLAUDE_CODE_PLUGIN_DIRS` may differ) and confirm `pdx.json` has `mod_socket`.
4. Throwaway tmux session `pdx-u1-accept` running `claude` (interactive, model haiku) in a scratch dir: one prompt with a Bash call, `/clear`, one more prompt, `/exit`. Check `GET /api/mod/streams` and `/events`: order `session.start → turn.start → tool.start → tool.check → tool.end → turn.complete → background → usage …`, heartbeats ~10 s apart, `session.clear` with the new sid, `session.end`; `gaps = 0`, `rejected = 0`. Record interactive-mode order and latency into the spec (§3, M-U1-4).
5. Restart the daemon during a second throwaway session's idle period: the stream reappears within one heartbeat.
6. Kill the throwaway tmux session; record the results in the PR / memory.
