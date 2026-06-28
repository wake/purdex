# Storage Subsystem 2 — Phase 2a Detailed Plan (daemon backup module)

Base: alpha.302 (`938e5f08`). Worktree `storage-backup` (branch `worktree-storage-backup`).
Spec: `2026-06-29-storage-backup-spec.md` §5 "Phase 2a" + AC-2a, §4.1–4.6.

**Pure Go daemon, no front-end / Electron changes.** New package
`internal/module/backup/{module,handler,store,validate}.go` + tests. The front-end (2b/2c)
consumes this over HTTP later; 2a defines and asserts the wire contract (incl. the `backup:done`
event) so 2b has a stable target.

## Existing groundwork (reused, not rebuilt)

- **Module four-layer + lifecycle** (`internal/module/sync/module.go`): struct holds `core` +
  `store`; `New()` empty; `Init(c)` opens `filepath.Join(c.Cfg.DataDir, "<name>.db")`;
  `RegisterRoutes(mux)` wires `mux.HandleFunc("METHOD /api/...", h)`; `Start`/`Stop`. Registered in
  `cmd/pdx/main.go:247` via `c.AddModule(syncmod.New())` — backup adds one line beside it.
- **SQLite conventions** (`internal/store/meta.go:35-67`, `module/sync/store.go:60-94`): own db
  file, DSN `?_pragma=journal_mode(wal)&_pragma=busy_timeout(500)`, `:memory:` →
  `SetMaxOpenConns(1)`, file → `SetMaxOpenConns(2)`; `CREATE TABLE IF NOT EXISTS`; parameterised
  `?`. **No FK** (validate references in-handler — avoids the #850 DSN-pragma footgun).
- **WS broadcast** (`internal/core/events.go`): `c.Events.Broadcast(session, type, value)`; envelope
  is `{type, session, value}` (`events.go:13`). `value` is a JSON **string**.
- **Test conventions** (`internal/store/agent_event_test.go:15-23`): `openTestX(t)` helper opening
  `:memory:` with `t.Helper()`+`t.Cleanup(close)`; `httptest.NewRequest/NewRecorder`
  (`config_handler_test.go:42-51`); testify `require`/`assert`.
- **Concurrency/lock tests use a file-backed temp DB**, NOT `:memory:` — a `:memory:` store sets
  `SetMaxOpenConns(1)` (`meta.go:56`), so goroutines just queue on the one connection and never
  exercise real SQLite writer contention / `busy_timeout`. The repo's `BEGIN IMMEDIATE`/busy_timeout
  tests use a `t.TempDir()` file DB on the `MaxOpenConns(2)` path (`agent_event_test.go:183`,
  `pragma_test.go:49`). Provide **two** helpers: `openTestBackupStore(t)` (`:memory:`, normal tests)
  and `openTempBackupStore(t)` (temp-file, the serialisation test only).
- **`backup:done` test** uses the broadcaster's existing `AddTestSubscriber()`
  (`internal/core/events.go:112`) to capture emitted envelopes — no bespoke spy.

## Design decisions

1. **Layering: pure `store` (data) + thin `handler` (HTTP) + `validate` (pure functions).**
   `store.go` never imports `core` or `net/http` → unit-testable with `:memory:`. `handler.go`
   translates HTTP↔store and owns status codes + body limits. `validate.go` is pure manifest
   validation (canonical order, well-formed tree, path/dir/file rules) — no DB, exhaustively
   table-testable, called inside the append transaction.
2. **Injected clock for deterministic GC/created_at.** `BackupStore` holds `now func() int64`
   (Unix seconds), default `time.Now().Unix()`, overridable in tests. Drives `created_at`, the
   90-day age window, and the blob grace period — all testable without sleeping.
3. **`backup:done` lives in the module layer, not `store`.** `store.AppendSnapshot` returns
   `(result, error)` where `result` says **whether a new row was written** (vs no-op-suppressed)
   plus `{snapshotId, currentHeadId, isFork, device, trigger, createdAt, storeID}`. The **handler**
   calls `c.Events.Broadcast` only when a new row was written (no-op broadcasts nothing). Keeps
   `store` free of `core`.
4. **`POST /snapshot` is one `BEGIN IMMEDIATE` transaction** (spec §4.3, P1-3): read head → validate
   (§4.2) → **content-keyed no-op check** (manifest == head's canonical manifest → return head id,
   no row, regardless of `parentId`, R3-Pa) → insert with `is_fork = parentId != head` (only when
   content differs) → GC (§4.5), all in the same tx.
5. **Deterministic serialisation test seam.** A bare two-goroutine race can pass even on a broken
   impl (where read-head and insert are *not* in one `BEGIN IMMEDIATE`) if the scheduler happens to
   not interleave them. So `BackupStore` carries an unexported `afterReadHead func()` hook (nil in
   prod, zero cost). The serialisation test injects a barrier into it that (a) blocks writer-1
   *after* it has read head but *before* insert, (b) launches writer-2 and asserts writer-2 cannot
   commit ahead (it blocks on `busy_timeout` because writer-1 holds the `BEGIN IMMEDIATE` write
   lock), (c) releases writer-1; writer-2 then acquires the write lock (obtainable only after
   writer-1 commits) and **observes the advanced head**, so its `is_fork`/`parent` reflect writer-1's
   row. This forces the race window rather than hoping for it.
   On a broken non-transactional impl the test deterministically fails.
5. **Manifest is stored canonical** (sorted by path). The daemon **rejects** an unsorted manifest
   (`400`), it does not re-sort (R2-Pc) — so the byte-form used for no-op compare is unambiguous.
   No-op compare is on the **canonical JSON manifest string** of head vs incoming.
6. **Blob upload reads `cap+1`** and returns `413` on overflow (R2-P4) — never a silent truncated
   accept; `cap = 30 MB` (> the 25 MB single-file In-App cap). Hash is verified server-side; URL
   `{hash}` must be 64-char lowercase hex.

## TDD tasks (each: failing test → impl → green → independent commit)

> Subagent rule: every Bash prefixed `cd <worktree> &&`; absolute Edit/Write paths carry the
> `.claude/worktrees/storage-backup/` prefix. `go test ./internal/module/backup/...` + `go vet`
> green per task; full `go test ./...` green before PR.

### T2a-0 — module skeleton + store open + schema + wiring
- **Test** (`store_test.go`): `openTestBackupStore(t)` opens `:memory:`; asserts both tables +
  index exist (query `sqlite_master`); a second `OpenBackup` on a temp-file path is idempotent (no
  error, no dup). `module_test.go`: `New().Name()=="backup"`, `Dependencies()==nil`.
- **Impl**: `store.go` `OpenBackup(path)` (DSN/WAL/busy_timeout/conns per Design 2, `now` field) +
  `migrate` (the two `CREATE TABLE IF NOT EXISTS` + index from spec §4.1) + `Close()` + a
  **compile-safe no-op `GC(now)` stub** (replaced with the real impl in T2a-4, so `Start()`'s wiring
  lands here once and stays green). `module.go` skeleton (`Init` opens `backup.db`, holds `core`;
  `RegisterRoutes` empty for now; `Start` logs + calls `store.GC(now)`; `Stop` closes). Wire
  `c.AddModule(backupmod.New())` at `cmd/pdx/main.go:247`-adjacent.
- **AC**: schema present; module registers; `go test ./...` still green.

### T2a-1 — manifest validation (pure functions)
- **Test** (`validate_test.go`): table-driven over `ValidateManifest(entries)`:
  - OK: canonical sorted file+dir tree.
  - `400` causes: unsorted / duplicate path / **empty path `""`** / path with `..`, **`.` (incl.
    `a/./b`)**, leading `/`, backslash, empty segment / `kind` not in {file,dir} / `kind:'dir'` with
    non-empty hash|size|words / `kind:'file'` hash not 64-hex / **prefix-conflict** (`a` is file
    while `a/b` exists) / ancestor-not-derivable.
  - Returns a typed error (or `(bool, reason)`) the handler maps to `400`.
- **Impl**: `validate.go` — pure: canonical-order check, path-rule check, per-kind field check,
  tree well-formedness (build prefix set; no file path is a strict prefix-ancestor of another).
  **No DB, no blob existence** here (blob existence is a tx-time check in T2a-3).
- **AC**: spec §4.2 (b)(c)(e)(f)(g) cases all covered.

### T2a-2 — blob store + negotiation + blob/missing handlers
- **Test**: store `PutBlob(hash, content)` rejects when `sha256(content)!=hash`; stores once;
  re-put same hash → no dup row, `size` correct; `GetBlob` byte-identical; `MissingBlobs(hashes)`
  returns exactly the absent subset. Handler (`httptest`): `PUT /blob/{h}` body-hash-mismatch →
  `400` nothing stored; non-hex `{h}` → `400`; body > `cap` → `413` nothing stored; match → `204`,
  re-PUT → `204` no dup; `GET /blob/{h}` exact bytes, absent → `404`; `POST /missing` returns subset,
  over count cap → `413`.
- **Impl**: store `PutBlob/GetBlob/MissingBlobs` (INSERT OR IGNORE, sha256 verify, `created_at=now`).
  handler `PUT/GET /api/backup/blob/{hash}` (read `cap+1`→413; hex guard; verify), `POST
  /api/backup/missing` (decode `{hashes}`, count cap→413). Register these routes.
- **AC**: AC-2a blob + missing bullets.

### T2a-3 — snapshot append + validation wiring + no-op + fork + serialisation
- **Test**: store `AppendSnapshot(req, now)` inside `BEGIN IMMEDIATE`:
  - valid manifest (blobs present) → new row, `result.written=true`, `is_fork` correct.
  - missing blob → error mapped to `409`, **no row**.
  - validation failures (reuse T2a-1) + `parentId` cross-store / nonexistent + `size`≠blob length →
    `400`, no row.
  - **manifest item count > cap → `413`, no row** (spec §4.2 (h); a handler-level input cap, caught
    before the store call).
  - **no-op (content-keyed)**: manifest == head's canonical manifest → `result.written=false`,
    returns head id, **no row**, *even with a stale `parentId`* (assert the cross-device case). **Two
    explicit trigger cases** (AC-2a / R2-Pf): `trigger:'auto'` head-equal → no row; **`trigger:
    'pre-restore'` head-equal → no row**, returns head id.
  - **fork**: differing content, `parentId==head` → `is_fork=false`; stale `parentId` → `is_fork=true`;
    both rows present, neither overwritten.
  - **serialisation** (uses `openTempBackupStore(t)`, file-backed, `MaxOpenConns(2)` — NOT
    `:memory:`, per groundwork; **deterministic via the `afterReadHead` barrier seam**, Design 5):
    writer-1 blocked after read-head/before-insert; writer-2 (same `parentId`, differing content)
    must block on `busy_timeout` not commit ahead; after releasing writer-1, writer-2 acquires the
    lock and **observes the advanced head** so the two rows are not both `is_fork=false` and no
    `SQLITE_BUSY` is surfaced. A
    broken non-`BEGIN IMMEDIATE` impl fails this deterministically. Run with `-race`.
  - handler (`httptest`) `POST /api/backup/snapshot`: maps the above to `200{snapshotId,isFork,
    currentHeadId}` / `409` / `400`.
- **Impl**: store `AppendSnapshot` (tx: read head row+manifest → `ValidateManifest` → parent/size/
  blob-existence checks → canonical no-op compare → insert or return head → compute `is_fork`).
  handler decodes, maps errors, returns ids. **GC call site present but a no-op stub** until T2a-4.
- **AC**: AC-2a no-op / fork / serialisation / validation-409-400 bullets.

### T2a-4 — GC (grace + ancestor closure, injected clock)
- **Test** (keep-set is a **union, NOT a hard cap** — spec §4.5: `latest 100 by id ∪ within 90 days
  ∪ ancestor closure`), with `now` injected, ≥4 explicit fixtures:
  1. **>100 snapshots, ALL within 90 days → none deleted** (the 90-day arm of the union keeps them;
     count alone must NOT trigger deletion — this is the anti-hard-cap assertion).
  2. **>100 snapshots with some aged past 90 days → only rows in neither the latest-100 nor the
     90-day window (and not an ancestor) are deleted**; rows in any union arm survive.
  3. **ancestor closure**: a survivor's `parent_id` ancestor that is itself past both windows is
     **still retained** (no dangling `parent_id`).
  4. **blob grace**: blobs referenced by no survivor **and** `created_at` past grace → deleted;
     blobs still referenced (incl. **shared across ≥2 snapshots**) or **within grace** → retained.
  Run via both the append tx and `Start()`.
- **Impl**: store `gc(tx, storeID, now)` — compute keep-set (latest 100 by id ∪ within-90d ∪
  ancestor closure via `parent_id` walk), delete others, then delete unreferenced+past-grace blobs.
  Wire into `AppendSnapshot`'s tx (replace the T2a-3 stub) and `Start()`.
- **AC**: AC-2a GC bullet (closure + grace + shared-blob survival).

### T2a-5 — history + get snapshot handlers
- **Test**: seed snapshots (files+dirs) → `GET /api/backup/history?storeId=` newest-first with
  correct `fileCount`/`dirCount`/`totalSize`, **no blob bytes**; `GET /api/backup/snapshot/{id}`
  full manifest incl. `kind:'dir'` entries; nonexistent id → `404`.
- **Impl**: store `ListHistory(storeID)` (summarise manifest server-side), `GetSnapshot(id)`.
  handler routes. Register.
- **AC**: AC-2a history/get bullets.

### T2a-6 — `backup:done` broadcast + payload contract
- **Test**: capture envelopes via the broadcaster's existing `AddTestSubscriber()`
  (`events.go:112`, per groundwork — no bespoke spy); a **committed** snapshot (T2a-3 written=true)
  broadcasts exactly one `backup:done` with `session==""` and `value` = JSON
  `{storeId,snapshotId,currentHeadId,device,trigger,createdAt}`; a **no-op-suppressed** post
  (both `trigger:'auto'` and `trigger:'pre-restore'`, head-equal) broadcasts **nothing**.
- **Impl**: in the snapshot handler, after a `written` append commits, `c.Events.Broadcast("",
  "backup:done", string(json))`. (Front-end `HostEvent` union update is **2b**, not here — 2a just
  emits the documented wire event.)
- **AC**: AC-2a `backup:done` bullet.

## Verification (before PR)
- `cd <worktree> && go test ./... && go vet ./...` green.
- `go test ./internal/module/backup/... -run . -count=1` green (incl. the goroutine serialisation
  test, run with `-race`).
- daemon still builds: `go build ./cmd/pdx`.
- Manual smoke optional (curl the 6 endpoints) — not required for the PR; AC covered by httptest.

## Out of scope (later phases / PRs)
- **2b**: front-end backup engine (walk `/buffer`, WebCrypto sha256, negotiation upload, debounce,
  sidebar status, `getClientId()` device id, add `backup:done` to `HostEvent` union).
- **2c**: front-end restore (history/viewer, dirty-block guard, atomic `replaceTree` behind
  `SupportsReplaceTree`, pane reconciliation incl. preview force-remount, fork UI).
- No GC background timer (best-effort on write + `Start()`, accepted).
