# Fresh, versioned session list (#1255, daemon half) — plan

Spec: `docs/specs/2026-09-23-session-list-fresh-spec.md`. One PR, daemon only
(`internal/core`, `internal/module/session`). TDD, one commit per task.
Test command: `go test ./internal/core/... ./internal/module/session/...`
(and `go test ./...` + `go vet ./...` before the PR).

## Task 1 — `core.HostEvent` version fields + `BroadcastEvent`

Files: `internal/core/events.go`, `internal/core/events_test.go`.

- Tests first:
  - `Broadcast("s","hook","v")` frame bytes are exactly
    `{"type":"hook","session":"s","value":"v"}` (no `epoch`/`seq` keys).
  - `BroadcastEvent(HostEvent{Type:"sessions",Value:"[]",Epoch:"e",Seq:3})`
    frame decodes with `epoch=="e"`, `seq==3`.
- Implement: add `Epoch string \`json:"epoch,omitempty"\``,
  `Seq uint64 \`json:"seq,omitempty"\``; add `BroadcastEvent(ev HostEvent)`;
  make `Broadcast` call it.

## Task 2 — versioned read primitive

Files: `internal/module/session/module.go`, new `internal/module/session/versioned.go`,
new `internal/module/session/versioned_test.go`.

- Tests first:
  - two calls → seq 1 then 2, same epoch; epoch matches `^[0-9a-f]{16}$`.
  - two modules → different epochs.
  - tmux list error → error returned, next success still gets seq 1.
  - zero sessions → `Sessions` is non-nil empty slice (JSON `[]`).
  - concurrency: N goroutines call `versionedList`; the fake executor records
    the order of `ListSessions` calls (wrap the executor, append a counter under
    a mutex); assert the seq sequence sorted by read order is strictly
    increasing and all seqs distinct.
- Implement: `type VersionedSessions struct { Epoch string \`json:"epoch"\`; Seq uint64 \`json:"seq"\`; Sessions []SessionInfo \`json:"sessions"\` }`;
  `newEpoch()` via `crypto/rand` 8 bytes → hex (fallback on rand error:
  `fmt.Sprintf("%016x", time.Now().UnixNano())` — never empty);
  fields `epoch`, `snapMu`, `snapSeq`; `versionedList()` per spec §4.1.

## Task 3 — `GET /api/sessions?fresh=1`

Files: `internal/module/session/handler.go`, `internal/module/session/handler_test.go`.

- Tests first:
  - `?fresh=1` → 200, JSON object with `epoch`, `seq ≥ 1`, `sessions` array.
  - warm the cache with a plain GET, add a session to the fake executor within
    the TTL, `?fresh=1` lists it.
  - absent / `fresh=0` / `fresh=true` → body is a JSON array (existing
    `TestHandlerListSessions*` keep passing unchanged).
  - tmux error with `?fresh=1` → 500.
- Implement branch at the top of `handleList`.

## Task 4 — WS frames carry epoch/seq

Files: `internal/module/session/module.go` (OnSubscribe),
`internal/module/session/watcher.go`, `internal/module/session/watcher_test.go`
(or the existing test file that covers the watcher — the subagent locates it).

- Tests first (using `Events.AddTestSubscriber`):
  - `broadcastSessions()` frame has `type=="sessions"`, epoch == module epoch,
    seq ≥ 1; a second call after the debounce window has a larger seq.
  - `tickNormal()` with a changed list broadcasts a frame with epoch/seq;
    unchanged list (only seq would differ) does **not** broadcast.
  - OnSubscribe snapshot frame carries epoch/seq (invoke the registered
    callback against a test subscriber; the subagent finds the existing
    pattern in `module_test.go`).
- Implement: the three sites call `versionedList()` and send via
  `BroadcastEvent` / a marshalled `HostEvent` with the version fields.

- Cross-channel tests (spec §3.3 rules 4–5), in `versioned_test.go`:
  - A test executor wrapper numbers each `ListSessions` call (read ordinal
    `k`) and returns a list whose single session name encodes `k`
    (e.g. `r<k>`). Interleave: `?fresh=1` GET, `broadcastSessions()` (reset
    the debounce between calls), `tickNormal()` (vary the list so it
    broadcasts), OnSubscribe snapshot, `?fresh=1` GET. Collect every
    (seq, list) pair from HTTP bodies and subscriber frames.
  - Assert: seqs all distinct; sorting by seq gives read ordinals strictly
    increasing; each payload's list is the one read under its seq (the name's
    `k` maps 1:1 to seq, monotone).
  - Warm the plain-GET cache with read `k`, then `?fresh=1` → payload's list is
    a newer read, never `r<k>` with a new seq.

## Task 5 — list-cache invalidation on mutation

Files: `internal/module/session/handler.go`, `create.go`, `watcher.go`,
`lookup.go` (or `handler.go`) for `invalidateListCache()`, tests in
`handler_test.go`.

- Tests first: for each of create / rename / delete: plain GET (warms cache) →
  mutation via handler → plain GET within TTL reflects it. And
  `broadcastSessions()` invalidates (warm → mutate fake tmux → broadcast →
  plain GET reflects).
- Also read-your-writes: DELETE then `?fresh=1` does not list the session.
- Implement `invalidateListCache()` (lock `listCacheMu`, zero `listCacheAt`)
  next to each `invalidateNameCache()` call.

## Verification before PR

- `go test ./...`, `go vet ./...`, `gofmt -l` empty.
- Mutation check (deliverable): temporarily (a) drop the `snapMu` lock,
  (b) make `fresh=1` go through the cache, (c) remove one
  `invalidateListCache` call, (d) omit epoch on the subscribe frame — each must
  turn at least one test red; revert.
- Manual smoke on a worktree-built binary on a spare port is **not** needed:
  the shared mlab daemon is only touched at deploy time (coordinator OK first).
