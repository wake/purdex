# Spec — Storage: daemon backup/restore snapshot store (subsystem 2)

- **Base**: alpha.302 (`938e5f08`)
- **Scope**: `daemon` (Go: new `backup` module) + `spa` (Storage pane right sidebar)
- **Status**: draft → codex review
- **Memory**: [[kickoff_storage_feature]]
- **Predecessor**: subsystem 1 (In-App nested file manager) shipped alpha.300/301/302. This spec
  fills the **right-sidebar placeholder** reserved in the subsystem 1 spec (§3.1: "備份（即將推出）"
  stub) with a real backup history/viewer/operator.

Add a **daemon-side, content-addressed, append-only snapshot store** that versions the In-App
`/buffer` tree, lets a device back it up automatically (debounced on save) and **explicitly**
restore any snapshot — across devices, with **zero silent overwrite** (fork detection + a
mandatory pre-restore safety snapshot). Conceptually a minimal git-style content-addressed
version store: blobs (deduped by hash) + snapshots (immutable manifests linked by parent).

## 1. Goal & non-overlap with sync

The In-App file manager (subsystem 1) is the source of truth on each device; its data lives in
IndexedDB (`fs-backend-inapp.ts`, store `files`, `path` as keyPath). Subsystem 2 gives that data
a **durable, cross-device version history** on the daemon so a user can:

1. Recover an earlier state of the whole tree (accidental delete / bad edit).
2. Carry the tree to another device (device A backs up, device B restores).
3. Never lose work to a concurrent overwrite (two devices editing the same logical store).

This is **distinct from the existing Sync module** (`internal/module/sync`,
`spa/.../sync/snapshot-store.ts`), which canonicalises **settings bundles**. The In-App tree is
**not** a sync contributor; this is a separate module with its own DB. We reuse sync's *patterns*
(module four-layer shape, SQLite conventions) but not its data path.

## 2. Decisions (user-ratified, 2026-06-29)

| Decision | Choice | Consequence |
|---|---|---|
| **Upload / dedup protocol** | **per-blob negotiation** (git-style) | client asks daemon which blob hashes are missing (`POST /missing`) → uploads only those. Real dedup; a multi-file tree where one file changed uploads one blob. One extra round-trip per backup. |
| **Restore granularity** | **whole-snapshot only** | restore = replace the entire In-App `/buffer` tree with a snapshot's state. No per-file restore (deferred; would need per-file history queries + richer UI). |
| **Retention / GC** | **latest 100 OR 90 days** (union) | a snapshot is kept if it is within the most recent 100 **or** newer than 90 days; older snapshots are GC'd. Blobs are GC'd when their refcount (snapshots referencing them) hits 0. Bounded growth (memory: #850 FK-pragma leak grew a DB to 18 GB — we want an explicit cap). |

## 3. Existing infrastructure (verified, reused)

- **Module shape** (`internal/module/sync/{module,handler,store}.go`): `Name()` / `Dependencies()`
  / `Init()` / `RegisterRoutes(mux)` / `Start()` / `Stop()`. New module `backup` mirrors this.
- **Route registration** (`internal/core/core.go:136-146`): `mux.HandleFunc("METHOD /api/path", h)`;
  JSON via `json.NewEncoder/Decoder`; errors via `http.Error(w, msg, code)`. Body size guarded with
  `io.LimitReader` (sync uses `10<<20`; `handler.go:24`).
- **SQLite** (`internal/store/meta.go:35-67`, `internal/module/sync/store.go:60-94`): own DB file
  (`backup.db`), DSN `?_pragma=journal_mode(wal)&_pragma=busy_timeout(500)`, `:memory:` →
  `SetMaxOpenConns(1)` for tests, file → `SetMaxOpenConns(2)`. **FK pragma — if used — MUST be in
  the DSN** (`_pragma=foreign_keys(1)`), not a post-Open `db.Exec`, per the #850 fix
  (`internal/store/agent_event.go:35-56`). Schema via `CREATE TABLE IF NOT EXISTS`; UPSERT via
  `ON CONFLICT … DO UPDATE`. Prepared/parameterised `?` queries only.
- **Device id**: client-supplied (a daemon serves multiple client devices, so the **client**'s
  stable id is the "device", not `config.HostID`). 2b reuses the front-end's existing persistent
  client id (the same id Sync uses as `clientId`); plan pins the exact source. daemon never
  invents it; `config.EnsureHostID` (`internal/config/hostid.go`) is the **daemon host** id and is
  out of scope here.
- **WebSocket push** (`internal/core/events.go:42-208`): `EventsBroadcaster.Broadcast(session,
  type, value)`; new subscribers get `OnSubscribe` snapshots. Backup-complete notifications to the
  right sidebar ride this bus (a `backup:done` host-event), no new socket.
- **Front-end backend** (`spa/src/lib/fs-backend-inapp.ts`): `StoredFile { path, content:
  Uint8Array, isDirectory, mtime }`; `STORAGE_ROOT = /buffer` (subsystem 1). 2b walks this tree to
  build a manifest; 2c writes it back on restore.
- **Tests**: `:memory:` SQLite helper with `t.Helper()`/`t.Cleanup()`
  (`internal/store/agent_event_test.go:15-23`); `httptest.NewRequest`/`NewRecorder` +
  testify `require`/`assert` (`internal/core/config_handler_test.go:42-51`).

## 4. Architecture

### 4.1 Data model (`backup.db`)

```sql
CREATE TABLE IF NOT EXISTS backup_blobs (
    hash       TEXT    PRIMARY KEY,   -- sha256(content), lowercase hex
    content    BLOB    NOT NULL,
    size       INTEGER NOT NULL,
    created_at INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS backup_snapshots (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    store_id   TEXT    NOT NULL,      -- logical store; fixed 'inapp:buffer' for now
    device     TEXT    NOT NULL DEFAULT '',
    parent_id  INTEGER,               -- the snapshot the client believed was head (NULL = root)
    is_fork    INTEGER NOT NULL DEFAULT 0,  -- parent_id != store head at append time
    trigger    TEXT    NOT NULL DEFAULT '', -- 'auto' | 'pre-restore' | 'manual'
    created_at INTEGER NOT NULL DEFAULT 0,
    manifest   TEXT    NOT NULL DEFAULT ''  -- JSON [{path,hash,size,words}]
);
CREATE INDEX IF NOT EXISTS idx_snap_store ON backup_snapshots(store_id, id);
```

- **Blobs**: content-addressed, immutable, deduped by `hash`. Written only via negotiation. GC'd
  when no live snapshot manifest references the hash.
- **Snapshots**: append-only, immutable. `manifest` is the full file list with per-file
  `{path, hash, size, words}` (words = word count for text, 0 for binary — same metric as the
  subsystem 1 row metadata). The manifest is the unit of restore.
- **store_id**: a single logical In-App store today → constant `'inapp:buffer'`. Column kept so a
  future multi-store split needs no migration. **Not** a security boundary (single-user daemon).

### 4.2 HTTP API (`/api/backup/...`)

| Method + path | Purpose | Req → Resp |
|---|---|---|
| `POST /api/backup/missing` | negotiation | `{hashes:[...]}` → `{missing:[...]}` (subset daemon lacks) |
| `PUT  /api/backup/blob/{hash}` | upload one blob | raw body bytes → `204`. daemon computes sha256, **rejects `400` if it ≠ `{hash}`**. Idempotent (existing hash → `204`). `io.LimitReader` cap (~30 MB, > the 25 MB single-file In-App cap). |
| `POST /api/backup/snapshot` | append snapshot | `{storeId, device, parentId, trigger, manifest:[{path,hash,size,words}]}` → `{snapshotId, isFork, head}`. **Rejects `409` if any manifest hash is not yet in `backup_blobs`** (client must finish blob upload first). Sets `is_fork = (parentId != current head)`. Runs GC after append. |
| `GET  /api/backup/history?storeId=` | list | → `[{id, device, parentId, isFork, trigger, createdAt, fileCount, totalSize}]` (manifest summarised, newest first; no blob content). |
| `GET  /api/backup/snapshot/{id}` | one manifest | → `{id, storeId, device, parentId, isFork, trigger, createdAt, manifest:[{path,hash,size,words}]}` (full manifest, no blob bytes). |
| `GET  /api/backup/blob/{hash}` | download blob | raw bytes (restore). `404` if absent. |

Auth/IP-whitelist/token middleware apply automatically (registered on the shared mux). Bodies use
`io.LimitReader`. All under the existing daemon, no new listener.

### 4.3 Fork detection (no silent overwrite)

`head(store_id)` = the snapshot with the max `id` for that store. On `POST /snapshot`:

- client sends `parentId` = the snapshot id it last observed as head (or NULL on first backup).
- daemon appends the new snapshot with `is_fork = (parentId != head(store_id))`.
- A fork is **never rejected and never overwrites** — it appends a sibling whose `parent_id`
  points at the (older) snapshot the client forked from. The `parent_id` chain forms a DAG the
  front-end renders as branches (2c). The response returns the **new** `head` so the client can
  advance its local head pointer.

This realises the user's three concurrency defences: (1) append-only immutable content-addressed
store, (2) explicit restore + mandatory pre-restore snapshot (§4.4), (3) parent links + fork flag
+ branch UI.

### 4.4 Restore flow (client-orchestrated, 2c)

Restore is **explicit** (a button on a chosen history row) and **always preceded by an automatic
safety backup**:

1. User picks snapshot **S** in the history list and confirms restore.
2. Client backs up the **current** tree first → `POST /snapshot` with `trigger:'pre-restore'`,
   `parentId:` current head. This is the safety restore-point — restore is 100% reversible.
3. Client fetches `GET /snapshot/{S}` (manifest) → for each entry `GET /blob/{hash}` → **replaces**
   the In-App `/buffer` tree (clear under `STORAGE_ROOT`, then write every manifest entry).
4. The next debounced auto-backup of the now-restored tree links `parentId:` the pre-restore
   snapshot (timeline stays continuous; the restored content re-enters history as a normal
   snapshot). Restore itself writes **nothing** new to the daemon beyond step 2.

Guards: restoring while In-App tabs are open/dirty reuses subsystem 1's dirty/locked confirm
semantics before clobbering the tree (plan pins the exact guard reuse).

### 4.5 GC (retention)

After each `POST /snapshot` (and on `Start()`), per `store_id`: keep snapshots that are within the
**latest 100 by id** OR have `created_at` within **90 days**; delete the rest. Then delete blobs
whose hash appears in **no** surviving snapshot manifest (refcount 0). GC is one transaction;
counts logged. Numbers are constants (`maxSnapshots=100`, `maxAgeDays=90`), trivially tunable.

### 4.6 Front-end (Storage pane right sidebar)

Subsystem 1 reserved the right sidebar as a collapsed "備份（即將推出）" placeholder. Here it
becomes:

- **2b** — backup engine + status: a debounced (e.g. ~2 s after last In-App write) auto-backup that
  walks `/buffer`, hashes each file (sha256 in the browser via WebCrypto), runs negotiation, uploads
  missing blobs, posts the snapshot; sidebar shows "上次備份 {relative time}" / "備份中…" / error
  banner. Listens for the `backup:done` host-event to refresh.
- **2c** — history/viewer/operator: a list of snapshots (device, time, trigger, fork badge), a
  manifest viewer (file list + size/words, no download needed to inspect), an explicit **Restore**
  button (with the §4.4 pre-restore + confirm flow), and a fork/branch indicator when `parent_id`
  chains diverge.

## 5. Phases (each: own TDD + PR + codex two-round review)

### Phase 2a — daemon backup module (pure Go, no front-end)
`internal/module/backup/{module,handler,store}.go`. Blob store with hash-verified dedup; snapshot
append with fork flag; `missing` negotiation; history/get/blob endpoints; GC. Registered like the
sync module. **No front-end changes.**

**AC-2a**
- `PUT /blob/{hash}` with body whose sha256 ≠ `{hash}` → `400`, nothing stored; matching hash →
  stored once, re-`PUT` of same hash → `204` no duplicate row.
- `POST /missing` returns exactly the subset of input hashes the daemon lacks.
- `POST /snapshot` referencing a hash not yet uploaded → `409`, no snapshot row written.
- `POST /snapshot` with `parentId` == current head → `is_fork:false`; with a stale `parentId` →
  `is_fork:true`, both rows present, neither overwritten, `parent_id` preserved.
- `GET /history` returns snapshots newest-first with correct `fileCount`/`totalSize` summary and no
  blob bytes; `GET /snapshot/{id}` returns the full manifest; `GET /blob/{hash}` returns exact
  bytes; absent hash → `404`.
- GC: after exceeding 100 snapshots (or with an aged row past 90 days, time injected), surplus
  snapshots are deleted and blobs referenced by no surviving snapshot are removed; blobs still
  referenced survive. Asserted with ≥2 snapshots sharing a blob.
- All via `:memory:` SQLite + `httptest`; suite green, `go vet`/`go test ./...` clean.

### Phase 2b — front-end backup engine + status
Walk `/buffer`, browser-side sha256, negotiation upload, snapshot post; debounced trigger on
In-App writes; right-sidebar status (last/loading/error); `backup:done` host-event refresh; device
id from the existing client-id source.

**AC-2b**
- Editing an In-App file triggers (after debounce) exactly one backup: negotiation runs, only
  missing blobs upload, one snapshot posts; an unchanged second backup uploads **zero** blobs
  (pure dedup) and still posts a snapshot referencing existing hashes.
- Browser sha256 of a file equals the daemon's verification (round-trip: upload then
  `GET /blob/{hash}` is byte-identical).
- Sidebar shows relative last-backup time, a "備份中…" state during upload, and surfaces a failed
  upload as an inline error (never silent).
- The local head pointer advances to the returned `head` so the next backup's `parentId` is
  correct (no spurious self-fork).
- Tests: vitest + fake IndexedDB + mocked fetch; lint + `tsc` build green.

### Phase 2c — front-end restore + history/fork UI
History list, manifest viewer, explicit restore with mandatory pre-restore safety snapshot and
dirty/locked guard, fork/branch indicator.

**AC-2c**
- History renders snapshots (device, relative time, trigger, fork badge) newest-first from
  `GET /history`.
- Selecting a snapshot shows its manifest (paths + size + words) without downloading blobs.
- Restore: a pre-restore snapshot is posted **first** (assert order), then the tree is replaced to
  exactly match the chosen manifest (every path present with byte-identical content, paths not in
  the manifest removed); restoring with a dirty/locked In-App tab confirms/refuses per subsystem
  1's guard.
- A snapshot whose `parentId` is not the prior head renders a fork/branch indicator.
- Tests: vitest + RTL; lint + build green.

## 6. Testing
TDD per phase. Daemon: `:memory:` SQLite, `httptest`, testify; hash-verify, negotiation, fork,
409/400 rejections, GC with shared blobs. Front-end: fake-indexeddb + mocked fetch (2b), RTL +
restore round-trip (2c). `go test ./...` (daemon) and `pnpm run lint` + `pnpm run build` +
`npx vitest run` (spa) green each phase.

## 7. Non-goals
- **Per-file restore / per-file history** — whole-snapshot restore only (user decision §2).
- **Real-time multi-device merge / CRDT** — fork detection surfaces divergence; resolution is the
  user explicitly restoring one branch. No automatic merge.
- **Backing up anything other than the In-App `/buffer` tree** — daemon FS / workspace files
  (`FileTreeView`) are out of scope.
- **Encryption at rest** — blobs stored as-is in `backup.db` (single-user local daemon; consistent
  with existing `meta.db`/`sync.db`).
- **Reusing the Sync module's tables or data path** — separate `backup.db`, separate module.

## 8. Known limitations (explicit)
- A device that never reconnects keeps its fork branch indefinitely (until GC ages it out). The UI
  shows it; there is no auto-prune of "abandoned" branches.
- `store_id` is fixed `'inapp:buffer'`; multiple logical stores are a future extension (column
  already present, no migration needed).
- GC is best-effort on write + `Start()`; it is not a background timer. A daemon that only ever
  receives backups (never restarts, never crosses the 100/90 threshold) holds at the cap, which is
  the intended bound.
