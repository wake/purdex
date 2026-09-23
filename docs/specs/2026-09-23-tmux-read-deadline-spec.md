# Bounded tmux reads for the session list (#1293) — spec + plan (rev 3: subscribe-snapshot recovery, as implemented)

Status: final, rev 3 (2026-09-23) · Owner: mlab/purdex-3b · Coordinator: mlab/purdex-fb
Context: daemon contract `docs/specs/2026-09-23-session-list-fresh-spec.md` (#1292, alpha.423).

## 1. Problem

Building a session list (`SessionModule.ListSessions`, `internal/module/session/service.go`)
runs, with no deadline: `tmux list-sessions`, then for every session `ActivePaneMetadata`
(seven `tmux display-message` calls each), plus meta-DB reads — all through
`exec.Command(...).Output()` (`internal/tmux/executor.go`). One hung tmux client process
blocks the caller forever.

Every versioned path reads under `snapMu` (`versionedList`, versioned.go): `GET
/api/sessions?fresh=1`, the WS subscribe snapshot (`sendSessionsSnapshot`, module.go), the
wait-for push (`broadcastSessions`) and the ticker push (`tickNormal`). So one hung read
stalls all four together — including every new WS connection's first frame, which the SPA
needs to open its attach gate. The plain `GET /api/sessions` does the same under
`listCacheMu`.

## 2. Goals

- G1. Every session-list read has ONE bounded deadline covering the whole chain — tmux list,
  per-session pane metadata, the tmux-instance probe, and the meta-DB reads. A read that
  exceeds it is killed and the list call returns an error (never a partial list).
- G2. `snapMu` is released when the read ends (success, error, or deadline) and a failed or
  timed-out read never consumes a seq and never rotates the epoch (contract §3.3 rules 1, 5).
- G3. A caller waiting to enter a list read — `versionedList` AND the plain cached list — can
  give up when its own context ends instead of queueing behind a stuck read.
- G4. No wire change: success responses are byte-identical; a timed-out read on `?fresh=1`
  (and the plain GET) is a tmux read error → `500` with a text body, as today. The ticker and
  wait-for WS pushes keep their "log and skip" behaviour on error. The WS subscribe snapshot
  does not: a failed one is retried in the background and, if that fails too (or the frame
  cannot be queued), the connection is closed so the client reconnects (§3.5). No new frame
  type or field — the recovery uses the existing sessions frame and a plain WS close.
- G5. A HEALTHY host with many sessions stays well inside the bound (measured, §3.4).

Non-goals: deadlines for tmux mutations (`new-session`, `kill-session`, send-keys, …) and for
reads outside `ListSessions` / `ActivePaneMetadata` (capture-pane etc.).

## 3. Design

### 3.1 Executor (`internal/tmux`)
- `Executor.ListSessions(ctx)` and `Executor.ActivePaneMetadata(ctx, sessionName)` take a
  context; `RealExecutor` runs them with `exec.CommandContext` and `cmd.WaitDelay` (so a child
  holding the pipes cannot keep `Output` waiting). When the context ended, the returned error
  wraps `ctx.Err()` — never a bare `signal: killed` — so `errors.Is(err,
  context.DeadlineExceeded|Canceled)` holds (codex #6).
- `ActivePaneMetadata` makes ONE `tmux display-message` with the seven formats joined by a
  separator that cannot survive `sanitizeTmuxMetadata` (TAB — the per-field sanitiser maps TAB to
  space, so split on TAB first, then sanitise each field). Same fields, same sanitising, same
  error when any field is missing (codex #5; §3.4).
- Every caller of the two changed methods passes a context (codex #4): the session-list chain
  (§3.2), `GetSession`, the create-then-lookup in `create.go`, the name-cache lookup in
  `lookup.go` — each passes the request/operation context it has, else
  `context.WithTimeout(background, listReadTimeout)`. Fakes (`fake_executor.go`, the custom fakes
  in `create_test.go` / `versioned_test.go`, any other) accept the context; the shared fake gains
  an optional blocking hook that honours it.

### 3.2 Module (`internal/module/session`)
- `ListSessionsContext(ctx)` runs the whole chain under `ctx` (capped by `listReadTimeout`):
  - executor calls with `ctx`;
  - `applyActivePaneMetadata(ctx, …)`: a metadata error caused by the context ending (`ctx.Err()
    != nil`) aborts the list with that error; other metadata errors keep today's "skip the fields"
    behaviour (codex #1);
  - `TmuxInstance(ctx)`: `tmuxInstanceFn` takes a context; `config.GetTmuxInstance` gains a
    context variant that uses `min(parent deadline, its own 3 s)` (codex #3);
  - meta DB: `CleanOrphansContext` / `GetMetaContext` on the store using `ExecContext` /
    `QueryRowContext` (codex #3).
- `ListSessions()` stays for callers without a context = `ListSessionsContext` under a fresh
  `listReadTimeout`. Callers that already hold a context/budget switch to `ListSessionsContext`:
  monitor (its `ctx`), peers (its owner-resolution budget) (codex #7). Agent snapshot and hook
  fallback keep `ListSessions()`; a timeout is an error there exactly like today's tmux error
  (skip / fail the lookup) — covered by a test each.
- `snapMu` becomes a one-slot semaphore (`chan struct{}`), acquired with a `select` on
  `ctx.Done()`; seq assignment and epoch rotation stay inside the slot and happen only after a
  successful read (codex #8).
- `listCacheMu` becomes the same kind of context-aware slot for the plain cache: a waiting
  request whose context ends returns `ctx.Err()`; after a holder's read times out, each waiter
  that still wants it performs its own bounded read (no unbounded chain because each waiter is
  bounded by its own request context and the cap) (codex #2).
- Callers: `?fresh=1` and plain GET → `r.Context()` capped; subscribe snapshot, wait-for push,
  ticker push → `context.WithTimeout(background, listReadTimeout)`.

### 3.3 Timeout value
`listReadTimeout = 5 s`, one budget for the whole chain.

### 3.4 Measured (mlab, 2026-09-23, 15 sessions)
Seven separate `display-message` calls ≈ 44 ms per session (≈ 0.66 s for 15; ≈ 4.4 s projected
for 100 — too close to 5 s). One combined call ≈ 10 ms per session (≈ 1 s for 100). Hence the
merge in §3.1 is part of this change, not an optimisation for later.

### 3.5 Subscribe-snapshot recovery (WS)
The SPA keeps a new connection's attach gate shut until it has reconciled a first sessions
frame, and with an unchanged list no push will ever come. So a subscribe snapshot that fails
cannot just be logged and skipped (`sendSessionsSnapshot` / `retrySessionsSnapshot`,
module.go):
- **Retries.** A failed snapshot read is retried in a background goroutine after 1 s, 2 s and
  4 s (three retries). Each retry gets a fresh, full `listReadTimeout` budget — it does not
  inherit what the first read used.
- **Give-up.** If every retry fails, the connection is closed (`Events.Remove`) so the client
  reconnects and gets a new snapshot on the new connection.
- **Frame not queued.** A snapshot is delivered only if its frame is actually queued on the
  subscriber (`EventSubscriber.TrySend`, `internal/core/events.go`; `Send` still drops
  silently). A full send buffer means the client is too slow to drain it: the connection is
  closed at once, first try or retry alike, with no further read (the read was not the
  problem). A subscriber already removed ends the attempt quietly.
- **Cancellation.** The waits and the in-flight read end as soon as the connection ends or the
  module stops (the watcher context); the goroutine then exits without reading again.
- **Ordering.** A retried snapshot may land after a push on the same connection. Every frame is
  a versioned list with its own `seq` in the same epoch, and the SPA orders by it (contract
  `2026-09-23-session-list-fresh-spec.md` §3.3 total order, §3.4 epoch handling), so a late
  snapshot never overrides a newer push.
- **Hung tmux.** While tmux stays stuck, one connection costs 5 s (first read) + 1 + 5 + 2 + 5 +
  4 + 5 ≈ 27 s before it is closed; the client then reconnects and the cycle repeats — about one
  reconnect every 27 s, not a tight loop.
- The ticker and wait-for pushes are unchanged: log and skip, the next push tries again.

## 4. Tasks (TDD, one commit each)

- T1 executor: ctx signatures, `CommandContext` + `WaitDelay`, ctx-error wrapping, single
  combined `display-message` (field order/sanitising unchanged — table test against the old
  per-field output). All callers + fakes updated (compiles, all tests green). Helper-process
  test (`TestHelperProcess` pattern: a fake `tmux` on `PATH` that sleeps): an in-flight read hits
  the deadline → `errors.Is(err, context.DeadlineExceeded)`, returns within deadline+WaitDelay,
  the next read with a working fake succeeds.
- T2 module chain: `ListSessionsContext`, ctx through `applyActivePaneMetadata` (a metadata read
  that times out mid-list → the list returns an error, no payload), `TmuxInstance(ctx)`, meta-DB
  context methods; monitor and peers pass their contexts (tests: peers' budget is not exceeded by
  the list; monitor cancellation stops the list); agent snapshot / hook fallback treat a timeout
  like an error (tests).
- T3 `versionedList(ctx)` semaphore. Tests: stuck read → deadline error, slot free, next success
  is seq N+1; waiter cancelled → `ctx.Err()`, nothing consumed; at `snapSeq == maxSeq` a timed-out
  read does NOT rotate the epoch and the next success is the new epoch with seq 1; a cancelled
  waiter at maxSeq does not rotate; seq ↔ read bijection after a timeout; rewrite the existing
  rotation test that locks `snapMu` directly; the existing concurrency tests stay green.
- T4 callers + plain cache. Tests: `?fresh=1` with a cancelled request while a stuck read holds
  the slot returns without waiting; a timed-out `?fresh=1` → 500; plain GET: holder stuck, a
  waiting request that is cancelled returns immediately; a stuck ticker read times out, then a
  fresh GET and a new subscribe snapshot succeed within the bound.
- Gates: `go test ./...` (`-race` for `internal/module/session`, `internal/tmux`), `go vet ./...`,
  build `bin/pdx`. Mutations: acquire without the ctx select (T3 red); `exec.Command` again in
  ListSessions (T1 red); metadata ctx error swallowed again (T2 red); `listCacheMu` back to a mutex
  (T4 red); rotate before the read (T3 red).

## 5. Rollout
Daemon PR → merge → bump → deploy mlab and air26 (**ask the coordinator first**; `bin/pdx`
swap rm→cp→mv). Real machine: after deploy, `?fresh=1` and WS still work (curl with the token
in a variable, length printed only); a forced hang is covered by tests only.
