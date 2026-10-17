# Emit slot: read only the session's frames, share the pane snapshot (design draft for #3092 A)

Status: DRAFT for the lead's review. No code yet. Author: A-line member (ed3920-19). Scope: `internal/module/agent` (`hookemitter.go`, `module.go`
`liveFrameProjectionsWithSnapshot`, `frame_ops.go` `projectionForSession`, `pane_snapshot.go`) and `internal/store/frames.go`.

## 0. What costs what today

Every hook frame goes through `emitSlotFrom` (`hookemitter.go:126-130`): inside the global `emit.mu` it calls `projectionForSessionFn` →
`projectionForSession` → `liveFrameProjectionsWithSnapshot` (`module.go:960`), which does, for **every frame of every session**:

1. `frames.ListAll()` — a full table read (SQLite, `database/sql` lock).
2. `takePaneSnapshot()` — one `tmux list-panes -a` fork (`pane_snapshot.go:87`).
3. `filterProjectionFrames` (`frame_ops.go:1231`) — per frame `isPidAliveFn`, `processStartTimeFn`, `pidAncestorIncludesFn` (process-table reads).
4. `BuildSessionProjections` + `applyModOverlay` (`modMu`).
5. `selectSessionProjectionBy(sessionName, …)` — throws away every projection that is not the session's.

Measured by the watcher (#3092 last comment): ≈ 0.4 CPU s per hook, `emit slot held` histogram: none < 10 ms, 68 % 50–250 ms, max 561 ms; hook rate
80–100/min with 18 sessions. The hold time is also the queueing that produced `database is locked` saves and peers inventory timeouts.

Only (5)'s result is used. Everything for other sessions is wasted work done under the global lock.

## 1. Read only this session's frames

`projectionForSession(sessionName)` becomes `projectionForSessionScoped(sessionName)`:

1. **The session's panes** come from the pane snapshot (§2): the placements whose `SessionName == sessionName`, plus the `Ambiguous` panes resolved
   exactly as `snapshot.sessionName` does today (one `PaneSessionNameCtx` per ambiguous pane, as now).
2. **The session's frames**: a new `FramesStore.ListByPanes(paneIDs []string) ([]Frame, error)` — one `SELECT … WHERE pane_id IN (…)` (the table has an
   index on `pane_id` — to be confirmed in `internal/store/frames.go`; `ListByPane` at `frames.go:341` already queries by pane). Same row decoding as
   `ListAll`, same ordering.
3. The existing `filterPaneFramesBy(paneID, frames, snap)`, `BuildSessionProjections`, `applyModOverlay` and `selectSessionProjectionBy` run on that
   subset unchanged.

**Equivalence argument**: `selectSessionProjectionBy` already keeps only panes whose name is `sessionName`; `BuildSessionProjections` and the filter are
per-pane (no cross-pane state). So scoped result = global result by construction.
**Test**: a table-driven equivalence test over generated fixtures (K sessions × P panes × F frames, with dead pids, wrong start times, ambiguous panes,
panes missing from tmux): `scoped(name) == globalSelect(name)` for every session name incl. an unknown one. Mutation: scoped read drops the
`Ambiguous` resolution, or reads one pane fewer → the test fails.
The other callers of `liveFrameProjections*` (replay cache, `liveSessionProjections`, sweep, handler) keep the global read: they want all sessions.

## 2. Shared pane snapshot (TTL + invalidation)

A `paneSnapshotCache` on `Module`: `{snap *paneSnapshot, taken time.Time, gen uint64}`, behind its own small mutex (a leaf: never held across a fork).

- **Fresh enough** = `age <= paneSnapTTL` (proposal 2 s) **and** `gen == m.tmuxGen`.
- **`tmuxGen`** is bumped by anything that can move a pane between sessions or create/destroy one *in this daemon's view*: a verified `SessionStart` /
  `SessionEnd` hook, adopt / release, spawn and kill paths (the daemon's own `tmux new-session` / `kill-pane` calls), the pid sweep, and a
  `SessionStart` subscription event. Outside changes (the person's own `tmux move-pane`) are bounded by the TTL.
- **Miss forces one refresh**: if the hook's pane or session name is not in the cached snapshot, refresh once (bypassing the TTL) before concluding "no such
  session" — so a brand-new pane never reads as absent for up to the TTL.
- **Single flight**: concurrent takers share one `list-panes` fork (the refresh runs outside `emit.mu`; callers inside the slot wait on the flight, not on a
  fork they start).
- **Failure**: a failed or timed-out refresh is not cached; the next caller retries. `expired()` semantics stay: a snapshot whose per-pane lookups timed
  out is a failure for that read, never a "no session".
- The replay path (`replayProjectionCache`, #1767) keeps taking a fresh snapshot per round (it wants a consistent view for a whole replay).

Cost: forks per hook go from 1 (`tmux list-panes`) + process-table reads for all frames to ~0.05 (refresh rate ≈ one per TTL) + process reads for the
session's frames only.

## 3. What can leave `emit.mu`, and why ordering survives

The slot's invariant (header of `hookemitter.go`): read → build → stamp `(epoch, seq)` → broadcast → record baseline are one critical section, so two
emits for one session cannot be read in one order and broadcast in the other, and the mod worker cannot send a projection older than a hook frame that
went out in between. **That stays exactly as is**: the projection read still happens inside the slot, from the frame store (the source of truth), so
read order = broadcast order.

Moved out of the slot (no ordering meaning):
- **The pane snapshot refresh** (the fork) — §2; the slot only reads the cache or waits on the single flight.
- **Optional phase A2 — process liveness verdicts**: `isPidAlive`/`processStartTime`/`pidAncestorIncludes` for a frame are a property of the process, not of
  the emit order. A per-frame verdict cache `{frameID, pid, start} → alive, checkedAt` (TTL ≈ 2 s; positive verdicts only; invalidated on frame delete /
  `SessionEnd` / sweep) removes the process-table reads from the slot. Risk: a process that died without `SessionEnd` stays "alive" in the projection
  for ≤ TTL — today the same frame survives until the next sweep (minutes), so this is not a regression. **Recommendation: do A1 (§1 + §2) first,
  measure, and do A2 only if the p95 hold is still above target.**

Stays in the slot: store read, `applyModOverlay` (`modMu`), build, `stamp`, broadcast, `recordEmittedLights`, `publishNotifyFrom` (all in-memory).

CAS / sequence: unchanged — `stamp` is under `e.mu`, `undo` on a failed send, epoch rotation at `hookSeqMax`. No new lock is taken inside the slot except
the cache's leaf mutex (never held while forking) and the single-flight wait; lock order `hookEmitter.mu → Module.mu → modMu` keeps the cache mutex as
a leaf below `hookEmitter.mu`. Nothing in the cache calls back into the module.

## 4. Measuring

- **Forks per hook**: `GET /api/debug/execstat` deltas over a fixed window divided by `execstat.Event("hook.*")` counts (the denominator already exists,
  `handler.go:231`): `tmux` and `ps` tags. Baseline from the watcher now; target ≤ 0.1 `tmux list-panes` per hook mean.
- **Hold time**: the existing `emit slot held` histogram (`hookemitter_hold.go`, buckets <10/<50/<250/≥250 ms) — target p95 < 10 ms, 0 holds ≥ 250 ms
  at the same hook rate. Add per-kind bucket counters (hook vs probe vs sweep) so the hook share is visible.
- **New counters** (execstat-style, read through `/api/debug/execstat` or the dev summary line): snapshot cache `hit / miss / refresh by reason (ttl, gen,
  miss)`, `rows read per projection` (scoped vs the old ListAll size), `single-flight waiters`.
- **Offline**: `BenchmarkEmitSlot` with a fake tmux (N panes) and a temp store (M frames), 200 frames / 18 sessions, hook bursts of 100/min: ns/op and
  allocations, before/after; plus the existing hookemitter ordering tests under `-race` (concurrent emits for one session keep read order = seq order).
- **Live**: the watcher's profile capture before/after: `devices.ConnRegistry.Track` cum % (21 % → target < 5 %), `agent.handleEvent`.

## 5. Tests (TDD order)

1. Equivalence test (§1) — red first against a stub that returns a wrong subset.
2. `ListByPanes` store test: the right rows, empty list → no query, 1000 panes (chunk under the SQLite variable limit).
3. Cache: fresh within TTL (no second fork), TTL expiry, `gen` bump, miss → one forced refresh, failed refresh not cached, single flight (N goroutines → 1 fork).
4. Ordering: concurrent hook emits + a mod-worker emit for one session under `-race`: seq strictly increasing, each frame's content never older than the one before.
5. Mutation: drop the miss-refresh (new pane reads absent), drop the `gen` check, drop the Ambiguous resolution, read from the cache outside the slot.

## 6. Out of scope / follow-ups

- #3092 B (the `ps` forks in `verifyEvent` / `applyFrameEvent`) and C (team sweeper `LeadPresence` registry reads) — separate PRs per the order B, C, A.
- The sweep's and `owner_pass`'s `ListAll` (they need all frames).

## 7. Open questions for the lead

1. TTL 2 s acceptable? (A shorter TTL costs forks; a longer one lengthens the window in which a person's own `tmux move-pane` is mis-attributed.)
2. A1 first, A2 only on evidence — agreed?
3. Is bumping `tmuxGen` from the daemon's own spawn/kill/adopt paths enough, or should the SessionStart subscription also bump it?
