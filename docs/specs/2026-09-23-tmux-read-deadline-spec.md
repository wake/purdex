# Bounded tmux reads for the session list (#1293) — spec + plan

Status: draft (2026-09-23) · Owner: mlab/purdex-3b · Coordinator: mlab/purdex-fb
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

- G1. Every session-list read has a bounded deadline; a read that exceeds it is killed and
  returns an error.
- G2. `snapMu` is released when the read ends (success, error, or deadline) and a failed read
  never consumes a seq (already true for errors; must hold for deadlines).
- G3. A caller waiting to enter `versionedList` can give up when its own context ends (an
  HTTP client that went away, a subscribe that timed out) instead of queueing behind a stuck
  read.
- G4. No wire change: success responses are byte-identical; a timed-out read on `?fresh=1`
  is a tmux read error → `500` with a text body, as the contract says today. WS paths keep
  their "log and skip" behaviour on error.

Non-goals: deadlines for tmux mutations (`new-session`, `kill-session`, …) and for reads
outside the session-list chain (capture-pane, etc.) — follow-up issue if wanted.

## 3. Design

### 3.1 Executor (`internal/tmux`)
- `Executor.ListSessions(ctx context.Context)` and `Executor.ActivePaneMetadata(ctx, sessionName)`
  take a context; `RealExecutor` uses `exec.CommandContext` for every tmux call they make, so a
  context deadline kills the tmux client process. `FakeExecutor` and the test fakes follow
  (ctx accepted; the fake gains an optional blocking hook for tests).
- A context error is returned wrapped so callers can tell (`errors.Is(err, context.DeadlineExceeded)`).

### 3.2 Module (`internal/module/session`)
- `ListSessionsContext(ctx)` does the whole chain under `ctx`; `ListSessions()` (still used by
  monitor / agent / peers / the plain cache) becomes `ListSessionsContext` with a default
  deadline `listReadTimeout` (5 s — a healthy list with dozens of sessions takes well under 1 s).
- `snapMu` becomes a one-slot semaphore (`chan struct{}`): `versionedList(ctx)` acquires it
  with `select { case sem <- struct{}{}: … case <-ctx.Done(): return ctx.Err() }`, reads with
  `ctx` capped by `listReadTimeout`, releases in `defer`. Seq assignment and the epoch rotation
  stay exactly as today, inside the slot.
- Callers:
  - `?fresh=1` → `r.Context()` (the read is also capped by `listReadTimeout`);
  - subscribe snapshot, wait-for push, ticker push → `context.WithTimeout(background, listReadTimeout)`.
- Plain `GET /api/sessions` (`cachedListSessions`) → reads with `r.Context()` capped the same way.

## 4. Tasks (TDD, one commit each)

- T1 executor: ctx signatures + `CommandContext` in `ListSessions` / `ActivePaneMetadata`; fakes
  updated; unit test with a fake command runner or a context already cancelled → returns a
  context error without running forever.
- T2 module: `ListSessionsContext`, default deadline for `ListSessions()`.
- T3 `versionedList(ctx)` with the semaphore. Tests (blocking fake executor that waits on ctx):
  - a stuck read hits the deadline → error, the slot is free again, the next call gets seq N+1
    where N is the last successful seq (no seq consumed by the timeout);
  - a second caller waiting for the slot whose ctx is cancelled returns `ctx.Err()` promptly
    and consumes nothing;
  - concurrency invariants of versioned_test.go (strict order, seq ↔ read bijection) still hold.
- T4 callers: `?fresh=1` passes the request context (test: a cancelled request while a stuck
  read holds the slot returns without waiting for the read); snapshot / wait-for / ticker use
  bounded contexts (test: a stuck ticker read times out, then a fresh GET and a new subscribe
  snapshot succeed within the bound; a timed-out read on `?fresh=1` → 500).
- Gates: `go test ./...` (with `-race` for the session package), `go vet`, `make`/build of `bin/pdx`.
  Mutations: remove the ctx select on acquire (T3 cancel case red); use `exec.Command` again in
  ListSessions (T1 red); drop the cap on the request context (T4 red).

## 5. Rollout
Daemon PR → merge → bump → deploy mlab and air26 (**ask the coordinator first**; `bin/pdx`
swap rm→cp→mv). Real machine: after deploy, `?fresh=1` and WS still work (curl with the token
in a variable, length printed only); a forced hang is covered by tests only.
