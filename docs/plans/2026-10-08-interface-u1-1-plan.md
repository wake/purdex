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
- Wire (spec §6.2): `type Batch struct { V int; Stream, Agent, CCVersion, ModVersion string; DroppedTotal int64; Events []Event }` and `type Event struct { Seq int64; At int64; SID string; Type string; Data json.RawMessage }` with the JSON names of §6.2 (`cc_version`, `mod_version`, `dropped_total`, `sid`, …).
- `func DecodeBatch(r io.Reader) (Batch, error)` returns an `*WireError{Code, Stream}` with the §6.2 codes: `bad_json` (incl. trailing data: after the first `Decode`, a second `Decode` must return `io.EOF`), `unsupported_version` (`v ≠ 1`), `bad_stream` (`^[A-Za-z0-9_-]{8,64}$`), `bad_events` (0 or > 500), `bad_seq` (not strictly increasing, or ≤ 0), `bad_sid` (lowercase UUID `^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`), `bad_event` (`type` not `^[a-z][a-z0-9._-]{0,63}$`, or `data` missing or not a JSON object). The stream is validated as soon as the first object decodes, so a trailing-data `bad_json` also carries it. `WireError.Stream` is set when the JSON parsed and the stream id is valid (so the handler can count the rejection on it); empty otherwise. Unknown top-level and event fields are ignored. `dropped_total < 0` → `bad_events`.
- `KnownTypes` = the 14 v1 types of §6.3 (`session.start`, `session.clear`, `session.end`, `turn.start`, `turn.complete`, `tool.check`, `tool.start`, `tool.end`, `agent.spawn`, `compact.start`, `compact.end`, `usage`, `background`, `heartbeat`).

Tests: `TestSocketPath_DefaultFits`, `TestSocketPath_TooLongIsNotOK` (a 120-byte dir), `TestDecodeBatch_Codes` (table: one case per code + a valid batch + `{valid}{junk}` trailing data → `bad_json`; each 400 row asserts `WireError.Stream` set or empty as specified), `TestDecodeBatch_IgnoresUnknownFields`.
Mutation gates: drop the length check → `TooLongIsNotOK` red; accept equal seqs → the `bad_seq` row red.

### Task A2 — registry and bus

File: `internal/modevents/registry.go`, `registry_test.go`.

- `type Registry` (zero-value unusable; `NewRegistry(now func() time.Time)`), safe for concurrent use.
- `Apply(b Batch) (ack int64)`: under the stream's own mutex — create the stream on first sight with `first_seen = now` (never rewritten); update `agent, cc_version, mod_version, last_seen`, `dropped_total = max(dropped_total, b.DroppedTotal)` (idempotent under resends, spec §6.2); for each event in order: `seq ≤ last_seq` → skip; `seq > last_seq+1` → `gaps++`; set `last_seq`; `sid` → stream's latest sid; `session.start` → `cwd`, `interactive=true`; `session.end` → `ended=true`, `ended_at`; count by type (`unknown` for types not in `KnownTypes`); append known events to a 256-entry ring; **deliver known events to subscribers synchronously, in order, still under the stream mutex**. Returns the stream's `last_seq`.
- `Reject(stream string)` → `rejected++`, creating the stream entry (with `first_seen`) when unknown, subject to the same 256-stream cap (the handler calls it on a 400 whose `WireError.Stream` is set).
- `Subscribe(fn func(StreamInfo, Event)) (cancel func())`. Subscriber panics are recovered and logged, never break `Apply`.
- `Streams() []StreamInfo` (sorted by `last_seen` desc), `Events(stream string, after int64) ([]Event, bool)`, `BySID(sid string) (StreamInfo, bool)` (the newest stream whose latest sid matches).
- Eviction on every `Apply` and on a 1-minute ticker started by the module: ended streams 30 min after `ended_at`; any stream 2 h after `last_seen`; above 256 streams, oldest `last_seen` first. `now` is injected for tests.

Tests: `TestApply_DedupesRetriedEvents` (same batch twice → delivered once, ack unchanged), `TestApply_DroppedTotalIsIdempotent` (the same batch twice and a lower `dropped_total` later keep the max), `TestApply_FirstSeenFixed` (a later batch does not move `first_seen`), `TestApply_CountsGaps`, `TestApply_UnknownTypeCountedNotDelivered`, `TestApply_DeliveryHoldsTheStream` (deterministic: the subscriber blocks on a channel inside the delivery of stream A's seq 1; a concurrent `Apply` of A's seq 2 is started; after 50 ms seq 2 has **not** been delivered; release; deliveries are exactly [1, 2]; a concurrent `Apply` on stream B is delivered while A is blocked), `TestReject_CountsAndCaps`, `TestSubscribe_PanicIsContained`, `TestEviction_EndedAndIdleAndCap`, `TestBySID_FollowsClear` (a `session.clear` event moves the stream's sid). Run the package with `-race`.
Mutation gates: remove the `seq ≤ last_seq` skip → `DedupesRetriedEvents` red; `dropped_total +=` instead of max → `DroppedTotalIsIdempotent` red; deliver after releasing the stream mutex → `DeliveryHoldsTheStream` red (deterministically, not by scheduling luck).

### Task A3 — listener and handler

Files: `internal/modevents/listen.go`, `peercred_darwin.go`, `peercred_linux.go`, `handler.go`, tests.

- `func Listen(path string) (net.Listener, Status)`; `Status{Enabled bool; Reason string}` with reasons `path_too_long` (caller passes `ok=false`), `unsafe_dir`, `in_use`, `not_a_socket`, `listen_failed`. First the directory: `Lstat(filepath.Dir(path))` must be a directory owned by `os.Geteuid()` with `mode & 0o022 == 0`, else `unsafe_dir` (spec §6.1 — this is what makes the stale check race-free against other users). Existing path: `Lstat`; socket → `net.DialTimeout("unix", path, 200ms)`: success → `in_use` (close the probe conn); failure → `os.Remove` then listen; not a socket → `not_a_socket` and **never removed**. After listen: `os.Chmod(path, 0o600)`; failure → close + `listen_failed`.
- The returned listener wraps `Accept` with the peer-uid check — **the gate** (spec §6.1): `peerUID(conn) (uint32, error)` (darwin `unix.GetsockoptXucred(fd, unix.SOL_LOCAL, unix.LOCAL_PEERCRED)` → `Uid`, linux `unix.GetsockoptUcred(fd, SOL_SOCKET, SO_PEERCRED)` → `Uid`), compared with `uint32(os.Geteuid())`; mismatch or error → close the conn and keep accepting. `peerUID` is a package var (test seam). Because the check wraps `Accept`, a connection made between bind and chmod is checked like any other.
- `func NewHandler(reg *Registry) http.Handler`: only `POST /mod/v1/events` (others 404, wrong method 405); `http.MaxBytesReader` 1 MiB → 413 `{"error":"too_large"}`; `DecodeBatch` error → 400 `{"error":"<code>"}` (and `reg.Reject(stream)` when the stream id itself was valid); success → 200 `{"ack":N}`. Content type of responses `application/json`.
- `func NewServer(h http.Handler) *http.Server` with the §6.1 timeouts.

Tests (socket under a short temp dir, e.g. `os.MkdirTemp("/tmp", "pdxm-")`, removed in cleanup — `t.TempDir()` on macOS is too long for a socket path):
`TestListen_CreatesSocket0600`, `TestListen_RemovesStaleSocket`, `TestListen_StaleAfterUnclosedListener` (a listener whose fd was closed without unlink — the exec-self case — leaves a file the next `Listen` replaces), `TestListen_InUseLeavesItAlone`, `TestListen_RefusesNonSocket` (a regular file at the path survives), `TestListen_UnsafeDir` (dir mode 0o777 → `unsafe_dir`, nothing created or removed), `TestListen_RejectsOtherUID` (seam returns euid+1 → the client sees EOF/reset, handler never runs; then the seam returns euid and the next connection is served), `TestHandler_AcksAndDedupes` (end to end over the socket with `http.Client{Transport: &http.Transport{DialContext: unix dial}}`), `TestHandler_ErrorCodes` (400 codes, 405, 404, 413), `TestHandler_RejectCountsOnValidStream` (a `bad_seq` batch with a valid stream id → `rejected = 1` on that stream; a `bad_stream` batch counts nowhere).
Mutation gates: skip chmod → `CreatesSocket0600` red; skip the uid compare → `RejectsOtherUID` red; remove non-socket guard → `RefusesNonSocket` red; skip the dir check → `UnsafeDir` red.

### Task A4 — module

Files: `internal/module/modevents/module.go`, `module_test.go`; `cmd/pdx/main.go` (`registerServeModules` adds it).

- Name `modevents`, no dependencies. `Init`: `reg := modevents.NewRegistry(time.Now)`; `c.Registry.Register("modevents", reg)`; compute `SocketPath(c.Cfg.DataDir)`.
- `Start(ctx)`: `Listen`; if enabled, serve in a goroutine (`srv.Serve(l)`, `http.ErrServerClosed` ignored, other errors logged); start the eviction ticker goroutine; both goroutines are tracked by one `sync.WaitGroup`; log one line `[modevents] socket <path>` or `[modevents] disabled: <reason>`. Never fails the daemon.
- `Stop(ctx)` (spec §6.1 order): (1) close the listener — stops accepts and unlinks the file; (2) `srv.Shutdown(ctx)` — waits for in-flight requests within the shared shutdown budget, then `srv.Close()` if the context expired; (3) cancel the ticker and `wg.Wait()`. Returns only after all three; idempotent; safe when Start found the channel disabled. The file is gone before `Stop` returns even when `Shutdown` hits its deadline (step 1 already unlinked it).
- `Status() Status` and `SocketPathForInfo() string` for the read API (U1-1b).
- `RegisterRoutes`: none in U1-1a.

Tests: `TestModule_StartServesAndStopUnlinks`, `TestModule_StopUnlinksEvenWhenShutdownTimesOut` (a handler blocked on a channel; `Stop` with an already-expired context → returns, file gone, a new `Listen` on the path succeeds), `TestModule_StopJoinsGoroutines` (a seam counts the serve and ticker goroutines; zero after `Stop`), `TestModule_RestartRebinds` (Start → Stop → new module Start on the same path works), `TestModule_DisabledPathDoesNotFail` (data dir > 100 bytes → Start returns nil, Status reason `path_too_long`).
Restart boundary: `cmd/pdx/shutdown.go` runs `StopModules` (reverse order) before the exec-self path in `main.go`; module `Stop` returning means the socket is unlinked and the goroutines are done. If a restart ever skips `Stop`, Go listener fds are close-on-exec, so the new image only finds a dead file (`TestListen_StaleAfterUnclosedListener`).

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

Module state `ev = { on: false, sock: '', stream: '', seq: 0, sid: '', ccVersion: '', modVersion: '', queue: [], droppedTotal: 0, inflight: false, scheduled: false, backoffMs: 0, turnId: '', asks: new Set(), compacting: false, beat: null }`.

Exports:
- `registerEvents(on)` — registers, without matchers, `tool.call`, `tool.check`, `agent.spawn`, `session.measure`, `session.end`, `classic.Stop`. Each hook awaits `next(e)` (tool.call: enqueue `tool.start` before, `tool.end` after; compact is not here), returns its result unchanged, and has `.catch(($, e, next) => next(e))`. That handler is safe after the hook already ran the tool: in a `.catch` handler `next` is replay-safe — `next.called` ⇒ `next(e)` resolves to the settled result, nothing beneath runs again [d.ts L1158–1170] (codex plan review #1 rebutted with this citation). Hooks do nothing when `ev.on` is false; `enqueue` catches its own errors.
- `tool.call` when the outer `ask.js` hook returns `{result}` with `next` still pending (a remote answer): the engine aborts what runs beneath, so this hook's `await next(e)` rejects — it enqueues `tool.end {error: true}` and rethrows (the `.catch` then replays the outer result unchanged).
- Observers for `register.js` (all no-ops when `ev.on` is false, none throws): `async evSessionStart($, e)` (interactive only: read `pdx.json` → `mod_socket`; absent → stay off; read `VERSION`; `ccVersion = await $.session.version()`; `sid = await $.session.id()`; `stream` = 22 chars from `crypto.getRandomValues` mapped to `[A-Za-z0-9_-]`; enqueue `session.start {cwd, surface}`; start the heartbeat `$.clock.every(10_000, …)`); `evTurnStart(e)` (main turn: `turnId = e.turnId`; enqueue `turn.start`); `evTurnComplete(e)` (enqueue `turn.complete {turn_id, reason, agent_id?, duration_ms, aborted}`; main turn clears `turnId` and `asks`); `async evClear($)` (`prev = sid`, `sid = await $.session.id()`, enqueue `session.clear {prev_sid}`, reset `turnId`/`asks`/`compacting`); `evCompactStart(e)` / `evCompactEnd(ok)`.
- Data per spec §6.3. `tool.check`: enqueue `{tool, tool_use_id?, agent_id?, decision}` from the verdict `next(e)` resolved to; decision `ask` adds `tool_use_id` to `asks` (when present). `tool.end` removes it. `AskUserQuestion` / `ExitPlanMode` `tool.start` also add to `asks`.
- Heartbeat data: `{turn_id?, asks: [...], compacting, agents: (await $.agent.list()).map(a => ({id, status}))}` (on failure `agents: []`).
- Queue / flush exactly as spec §6.5: flush timer 150 ms via `$.clock.after(150, () => flushTick($))`; ≤ 200 events per POST to `http://pdx/mod/v1/events` with `socketPath: ev.sock`, body `{v:1, stream, agent:'cc', cc_version, mod_version, dropped_total, events}`; each POST races a 5 s deadline (`Promise.race` with a promise resolved by `$.clock.after(5000, …)`) — a miss counts as a failure, `inflight` is cleared, the late answer is ignored. 200 → drop `seq ≤ ack`, `backoffMs = 0`, reschedule if non-empty; 400 → drop the batch, `droppedTotal += batch.length`; other status, throw or deadline → `backoffMs = min(max(1000, backoffMs*2), 30000)` and reschedule after it. Queue cap 1000 (drop oldest, `droppedTotal++`). `droppedTotal` is never reset (cumulative, spec §6.2).
- `session.end` hook: enqueue `session.end {reason}` with `sid = e.sessionId`, cancel the heartbeat, then `await finalFlush($)` inside the hook: it ignores `inflight` and backoff and sends one POST with every queued event (they are all unacked; an in-flight batch's events are still in the queue, so its late arrival is all duplicates), the newest 500 at most (older ones → `droppedTotal`). No deadline race inside it (the 1.5 s session.end bound cuts it).
- `$` is passed only to top-level function declarations (M-U1-2); every timer callback is an arrow that only calls a top-level function with `$`. `claude plugin validate --strict` and a live `claude -p --plugin-dir <scratch copy>` smoke run (as in the spec's probes) are part of B1's verification, because only the real loader applies the static check.

Tests (`events.test.ts`, kit; a fake daemon via `on('http.fetch', …)` recording `{url, init}` and answering `{value:{status, ok, headers:{}, text}}`; `mock.clock(on)`; `on('fs.read')` serving `pdx.json` with `mod_socket` and `VERSION`; `on('session.id')`, `on('session.version')`, `on('agent.list')`). Drive through the whole mod (the kit loads `register.js`), so integration with Task B2 is covered:
`posts session.start then turn events in seq order to the socket from pdx.json`, `a headless session reports nothing`, `no mod_socket in pdx.json → no fetch at all`, `a 200 ack drops acked events and the next batch starts after them`, `a failed POST is retried with backoff and nothing is lost`, `a POST that never answers misses its 5 s deadline and is retried`, `a 400 drops the batch and adds its size to dropped_total`, `queue overflow drops the oldest and dropped_total is carried, cumulative, in every later batch` (incl. an overflow while a POST is in flight: the next batch's `dropped_total` covers both), `heartbeat every 10 s carries turn_id, asks and agents`, `tool.check ask then tool.end clears the ask`, `a subagent turn.complete carries agent_id and keeps the main turn_id`, `after /clear events carry the new sid and session.clear names the old one`, `session.end flushes inside the hook` and `session.end flushes even while a POST is in flight and during backoff` (the final POST carries every queued event incl. the in-flight ones), `a reporter that throws after next never re-runs the tool` (a fake tool beneath counts calls: 1), `a fetch that throws never changes the engine's result` (tool.call result passes through unchanged).
AskUserQuestion with the reporter on (the outer `ask.js` hook + this inner one): `native answer: tool.start once, tool.end once, ask flow unchanged`, `remote answer (ask.js returns {result} with next pending): tool.end {error:true}, result is the remote one`, `dismissed: tool.end once, result unchanged`; the tool beneath runs once in every case.
Mutation gates: ack ignored (drop all on 200) → the retry/ack tests red; `droppedTotal = 0` after a 200 → the in-flight overflow test red; heartbeat not started → heartbeat test red; sid not refreshed on clear → clear test red; final flush waits for `inflight` → the in-flight session.end test red.

### Task B2 — wire into `register.js`

File: `cmd/pdx/plugin/purdex/hooks/register.js` (+ `relay.test.ts` only if an existing fake needs `http.fetch`).

- `import { registerEvents, evSessionStart, evTurnStart, evTurnComplete, evClear, evCompactStart, evCompactEnd } from './events.js'`; `registerEvents(on)` right after `registerAsk(on)`.
- `session.start`: in the interactive branch, `await evSessionStart($, e)` after the existing pdx.json read.
- `turn.start`: `evTurnStart(e)` first thing (before the nonce logic, every turn).
- `turn.complete`: after `next(e)` resolves and **before** the subagent skip, `evTurnComplete(e)`.
- `classic.SessionStart`: exactly after `const r = await next(e)` and the existing `if (!s.interactive || e.source !== 'clear') return r` line — i.e. only in the clear branch, after the engine has switched to the new session id — `await evClear($)`, then the existing relay handling unchanged.
- `session.compact`: move the current body into `async function relayCompact($, e, next)` unchanged (same early returns, same `toIdle`/`report` calls, no `.catch` — a throw still lets the compaction run); the hook becomes `evCompactStart(e); let ok = false; try { const r = await relayCompact($, e, next); ok = !(r && r.skip); return r } finally { evCompactEnd(ok) }`. `evCompactStart`/`evCompactEnd` carry `trigger` and `agent_id` (a subagent or precompute compaction is reported with them; the daemon decides what it means).
- No other change to relay logic. `relay.test.ts` / `ask.test.ts` stay green unchanged with the reporter off (their `pdx.json` has no `mod_socket`).
- **Reporter-on regression** (codex plan review #8, #9): `relay.test.ts`'s `world()` gains an option that adds `mod_socket` to `pdx.json` and a fake daemon answering 200 on `http.fetch`. The existing `/clear` tests (user `/clear` while idle / awaiting, the relay's own `/clear` → `cleared --new-session` + seed) and every `session.compact` test (auto-approved skip, manual while approved, awaiting cancel, beginning reset, subagent and precompute early returns) run in both modes via one loop and assert the **same relay outcomes** (argvs, returned values, state); in reporter-on mode they also assert the events: `session.clear {prev_sid}` carrying the new sid equal to `cleared --new-session`'s, and `compact.start`/`compact.end {ok}` with `ok = false` exactly when the hook returned `{skip}`.

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

---

## Plan review fold-in (codex `task-muynikwc-2b9vks`, 2026-10-08)

| # | Finding | Disposition |
|---|---|---|
| 1 | `.catch(… next(e))` re-runs the tool | **Rebutted with evidence**: in a `.catch` handler `next` is replay-safe [d.ts L1158–1170]; citation added to B1, plus the test `a reporter that throws after next never re-runs the tool` |
| 2, 3, 5 | `dropped` double-counts on resend / is zeroed during flight / 400 loss unobservable | Wire field is now cumulative `dropped_total`; daemon keeps the max; 400 adds the batch size (spec §6.2, A2, B1) |
| 4 | `session.end` flush blocked by in-flight / backoff; fetch has no timeout | `finalFlush` ignores both and resends every queued event; normal POSTs race a 5 s deadline (spec §6.5, B1) |
| 6 | Timer wiring vs M-U1-2 | Timer callbacks only call a top-level function with `$`; real-loader verification (validate + live smoke) in B1 |
| 7 | AskUserQuestion stacking untested | Three reporter-on cases in B1 |
| 8, 9 | `/clear` placement; compact wrapper regressions | Exact placement in B2; reporter-on runs of the existing relay `/clear` and compact tests |
| 10, 11 | Restart boundary; goroutine join | Stop order (close → shutdown → join), deadline test, close-on-exec stale test (A3, A4) |
| 12, 13 | Stale-file TOCTOU; bind→chmod window | Directory must be euid-owned and not group/other-writable (`unsafe_dir`); peer-uid check (euid) on every accept is the gate (spec §6.1, A3) |
| 14 | `first_seen` | Set once; test (A2) |
| 15 | `Reject` wiring | `WireError.Stream`; test (A1, A3) |
| 16 | Unreliable ordering gate | Deterministic blocking-subscriber test (A2) |
| 17 | Trailing JSON | `bad_json` row (A1) |
