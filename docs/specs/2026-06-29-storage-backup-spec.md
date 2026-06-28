# Spec — Storage: daemon backup/restore snapshot store (subsystem 2)

- **Base**: alpha.302 (`938e5f08`)
- **Scope**: `daemon` (Go: new `backup` module) + `spa` (Storage pane right sidebar)
- **Status**: draft → codex review **R2** (R1 findings folded in, see §0)
- **Memory**: [[kickoff_storage_feature]]
- **Predecessor**: subsystem 1 (In-App nested file manager) shipped alpha.300/301/302. This spec
  fills the **right-sidebar placeholder** reserved in the subsystem 1 spec (§3.1: "備份（即將推出）"
  stub) with a real backup history/viewer/operator.

Add a **daemon-side, content-addressed, append-only snapshot store** that versions the In-App
`/buffer` tree, lets a device back it up automatically (debounced on save) and **explicitly**
restore any snapshot — across devices, with **zero silent overwrite** (fork detection + a
mandatory pre-restore safety snapshot). Conceptually a minimal git-style content-addressed
version store: blobs (deduped by hash) + snapshots (immutable manifests linked by parent).

## 0. R1 review fixes folded in (codex `task-mqy452nt`)
- **C1**: restore **hard-blocks** when any In-App buffer is dirty (safety snapshot can't capture
  un-flushed editor state) — §4.4, AC-2c.
- **C2**: manifest entries carry `kind: 'file'|'dir'` so **empty directories survive** restore —
  §4.1, §4.4, AC-2a/2c.
- **P1-1**: client's next `parentId` is **its own last `snapshotId`**, never the global head;
  response exposes `currentHeadId` separately — §4.3, AC-2b.
- **P1-2**: unattached blobs get a **GC grace period** so an in-flight upload isn't reaped — §4.5.
- **P1-3**: `POST /snapshot` runs head-read → validate → insert → GC in **one `BEGIN IMMEDIATE`
  transaction** — §4.3, §4.5.
- **P1-4**: **no-op suppression** — an unchanged tree produces no new snapshot — §2, §4.3, AC-2b.
- **P2-1**: GC keep-set includes the **ancestor closure** of survivors — §4.5.
- **P2-2 / P2-3**: `POST /snapshot` does **structural + path-traversal validation** (`400`) — §4.2.
- **P2-4**: blob upload reads `cap+1` → **`413`**; negotiation/snapshot have item-count caps — §4.2.
- **P2-5**: `backup:done` host-event is defined in **2a AC**; 2b refreshes locally and treats WS as
  cross-device-only, never a phase blocker — §4.6, AC-2a/2b.
- **P3-1**: device id = `useSyncStore.getState().getClientId()` (reused, not a Sync dependency) — §3.

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
| **Restore granularity** | **whole-snapshot only** | restore = replace the entire In-App `/buffer` tree with a snapshot's state. No per-file restore (deferred; would need per-file history + richer UI). |
| **Retention / GC** | **latest 100 OR 90 days** (union) **+ ancestor closure + no-op suppression** | a snapshot is kept if it is within the most recent 100 **or** newer than 90 days **or** is an ancestor of a kept snapshot; older ones are GC'd. Blobs are GC'd when refcount hits 0 **and** they are past the upload grace period. **An unchanged tree produces no snapshot at all** (P1-4) — this, not the 100/90 numbers, is what actually bounds growth; the union is a retention window, **not** a hard size cap (a 90-day burst of *distinct* states is allowed to grow). |

## 3. Existing infrastructure (verified, reused)

- **Module shape** (`internal/module/sync/{module,handler,store}.go`): `Name()` / `Dependencies()`
  / `Init()` / `RegisterRoutes(mux)` / `Start()` / `Stop()`. New module `backup` mirrors this.
- **Route registration** (`internal/core/core.go:136-146`): `mux.HandleFunc("METHOD /api/path", h)`;
  JSON via `json.NewEncoder/Decoder`; errors via `http.Error(w, msg, code)`.
- **SQLite** (`internal/store/meta.go:35-67`, `internal/module/sync/store.go:60-94`): own DB file
  (`backup.db`), DSN `?_pragma=journal_mode(wal)&_pragma=busy_timeout(500)`, `:memory:` →
  `SetMaxOpenConns(1)` for tests, file → `SetMaxOpenConns(2)`. Concurrent writers are serialised by
  `BEGIN IMMEDIATE` + `busy_timeout` (§4.3). **No foreign keys** — the parent/manifest references
  are validated in-handler (§4.2), avoiding the #850 FK-pragma-leak footgun entirely
  (`internal/store/agent_event.go:35-56`); if FKs were ever added they'd go in the DSN, not a
  post-Open `Exec`. Schema via `CREATE TABLE IF NOT EXISTS`; parameterised `?` queries only.
- **Device id**: client-supplied. 2b reads the front-end's existing persistent client id via
  **`useSyncStore.getState().getClientId()`** (`spa/src/lib/sync/use-sync-store.ts:166`) — the same
  stable id Sync uses; **reusing the getter is not a runtime dependency on Sync being enabled**.
  The daemon never invents it; `config.EnsureHostID` (`internal/config/hostid.go`) is the **daemon
  host** id and is out of scope.
- **WebSocket push** (`internal/core/events.go:42-208`): `EventsBroadcaster.Broadcast(session,
  type, value)`; new subscribers get `OnSubscribe` snapshots. A `backup:done` host-event rides this
  bus (see §4.6); `backup:done` is **added to the front-end `HostEvent` type union**
  (`spa/src/lib/host-events.ts:3`).
- **Body limits**: JSON bodies use `io.LimitReader`; **raw blob upload reads `cap+1` and returns
  `413` on overflow** (a bare `ReadAll(LimitReader(...))` would silently accept a truncated body —
  unacceptable when the URL carries the content hash; §4.2).
- **Front-end backend** (`spa/src/lib/fs-backend-inapp.ts`): `StoredFile { path, content:
  Uint8Array, isDirectory, mtime }` — **directories are real entries** (`mkdir` writes
  `isDirectory:true`, `:115`). `STORAGE_ROOT = /buffer`. 2b walks this tree (files **and** dirs) to
  build a manifest; 2c writes it back on restore.
- **Path helpers** (`spa/src/lib/storage-paths.ts`, `storage-actions.ts:248`): subsystem 1 already
  rejects `.`/`..`/separator traversal on In-App paths; the backup manifest reuses the same
  discipline (§4.2).
- **Tests**: `:memory:` SQLite helper with `t.Helper()`/`t.Cleanup()`
  (`internal/store/agent_event_test.go:15-23`); `httptest` + testify (`config_handler_test.go:42`).

## 4. Architecture

### 4.1 Data model (`backup.db`)

```sql
CREATE TABLE IF NOT EXISTS backup_blobs (
    hash       TEXT    PRIMARY KEY,   -- sha256(content), 64-char lowercase hex
    content    BLOB    NOT NULL,
    size       INTEGER NOT NULL,      -- == length(content); used to validate manifest sizes
    created_at INTEGER NOT NULL DEFAULT 0  -- upload time; drives GC grace period
);

CREATE TABLE IF NOT EXISTS backup_snapshots (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    store_id   TEXT    NOT NULL,      -- logical store; fixed 'inapp:buffer' for now
    device     TEXT    NOT NULL DEFAULT '',
    parent_id  INTEGER,               -- the snapshot the client based this on (NULL = root)
    is_fork    INTEGER NOT NULL DEFAULT 0,  -- parent_id != store head at append time
    trigger    TEXT    NOT NULL DEFAULT '', -- 'auto' | 'pre-restore' | 'manual'
    created_at INTEGER NOT NULL DEFAULT 0,
    manifest   TEXT    NOT NULL DEFAULT ''  -- JSON [{path,kind,hash,size,words}]
);
CREATE INDEX IF NOT EXISTS idx_snap_store ON backup_snapshots(store_id, id);
```

- **Blobs**: content-addressed, immutable, deduped by `hash`. GC'd when no surviving snapshot
  manifest references the hash **and** `created_at` is older than the upload grace period (§4.5).
- **Snapshots**: append-only, immutable. `manifest` is the full entry list. Each entry:
  `{ path, kind: 'file'|'dir', hash, size, words }`. **Directory entries** (`kind:'dir'`) carry
  `hash:'' , size:0, words:0` and exist so empty dirs round-trip (C2). **File entries** carry the
  blob hash, byte size, and word count (text only; 0 for binary — same metric as subsystem 1 rows).
- **store_id**: a single logical In-App store today → constant `'inapp:buffer'`. Column kept so a
  future multi-store split needs no migration. **Not** a security boundary (single-user daemon).

### 4.2 HTTP API (`/api/backup/...`)

| Method + path | Purpose | Req → Resp |
|---|---|---|
| `POST /api/backup/missing` | negotiation | `{hashes:[...]}` → `{missing:[...]}` (subset daemon lacks). Caps `len(hashes)` (e.g. ≤10k); over → `413`. |
| `PUT  /api/backup/blob/{hash}` | upload one blob | raw body → `204`. Reads `cap+1` (~30 MB); over → **`413`**, nothing stored. Computes sha256; ≠ `{hash}` → **`400`**, nothing stored. `{hash}` must be 64-char lowercase hex else `400`. Idempotent (existing hash → `204`). |
| `POST /api/backup/snapshot` | append snapshot | `{storeId, device, parentId, trigger, manifest:[{path,kind,hash,size,words}]}` → `{snapshotId, isFork, currentHeadId}`. Validation (all → `400` unless noted), in one `BEGIN IMMEDIATE` txn: (a) `parentId` is null **or** an existing snapshot of the **same `storeId`**; (b) every manifest `path` is **root-relative, non-empty, no `.`/`..`/leading-slash/backslash**, and **unique**; (c) `kind∈{file,dir}`; (d) for `kind:'file'`: `hash` is 64-hex, the blob **exists** in `backup_blobs` (else **`409`** — finish blob upload first), and `size` **equals** the stored blob's byte length; (e) manifest item count cap (e.g. ≤50k). **No-op suppression**: if `parentId == head` and the manifest is byte-identical to head's, return head's `snapshotId` with no new row (P1-4). Otherwise insert with `is_fork = (parentId != head)`, then GC (§4.5). |
| `GET  /api/backup/history?storeId=` | list | → `[{id, device, parentId, isFork, trigger, createdAt, fileCount, dirCount, totalSize}]` (summarised, newest first, no blob bytes). |
| `GET  /api/backup/snapshot/{id}` | one manifest | → `{id, storeId, device, parentId, isFork, trigger, createdAt, manifest:[{path,kind,hash,size,words}]}`. |
| `GET  /api/backup/blob/{hash}` | download blob | raw bytes (restore). `404` if absent. |

Auth/IP-whitelist/token middleware apply automatically (shared mux). No new listener.

### 4.3 Fork detection & write serialisation (no silent overwrite)

`head(store_id)` = the snapshot with the max `id` for that store. **`POST /snapshot` performs the
entire sequence — read head → validate (§4.2) → no-op check → insert → GC — inside a single
`BEGIN IMMEDIATE` transaction** (P1-3), so two concurrent posts can't both read the same head and
both record `is_fork:false`; the second blocks on `busy_timeout` then re-reads the now-advanced
head.

- client sends `parentId` = **its own last successful `snapshotId`** for this store (or NULL on
  first backup). It **never** adopts the global head as its base (P1-1).
- daemon appends with `is_fork = (parentId != head)`. A fork is **never rejected, never
  overwrites** — it appends a sibling whose `parent_id` points at the snapshot the client forked
  from. The `parent_id` chain forms a DAG the front-end renders as branches (2c).
- the response returns `snapshotId` (the row just created — the client's next `parentId`) and
  `currentHeadId` (the global head **for display / divergence hints only** — must not become the
  client's base).

This realises the user's three concurrency defences: (1) append-only immutable content-addressed
store, (2) explicit restore + mandatory pre-restore snapshot (§4.4), (3) parent links + fork flag
+ branch UI.

### 4.4 Restore flow (client-orchestrated, 2c)

Restore is **explicit** (a button on a chosen history row) and **always preceded by an automatic
safety backup**. Because un-flushed editor state lives in Zustand, not IndexedDB
(`useEditorStore`, `EditorPane.tsx:274`), restore **hard-blocks on any dirty/locked In-App buffer**
(C1):

1. **Pre-flight guard**: if any In-App editor buffer is dirty or locked, **refuse** the restore and
   prompt the user to Save or Discard first. (A safety snapshot of `/buffer` could not capture the
   un-flushed buffer, so "continue anyway" is **not** offered — that would break reversibility.)
2. Client backs up the **current** tree → `POST /snapshot` with `trigger:'pre-restore'`,
   `parentId:` its own last snapshotId. This is the safety restore-point — fully reversible.
3. Client fetches `GET /snapshot/{S}` → first creates every `kind:'dir'` entry (so empty dirs
   survive, C2), then for each `kind:'file'` entry `GET /blob/{hash}` and writes it → **replaces**
   the tree under `STORAGE_ROOT` (clear root, then write every manifest entry). Paths are
   re-validated client-side before any write (§4.2 path rules, defence-in-depth).
4. The next debounced auto-backup of the restored tree links `parentId:` the pre-restore snapshot
   (timeline stays continuous). Restore itself writes **nothing** to the daemon beyond step 2.

### 4.5 GC (retention)

Inside the same `POST /snapshot` transaction (and on `Start()`), per `store_id`:

1. **keep-set** = (snapshots within the latest 100 by `id`) ∪ (snapshots with `created_at` within
   90 days) ∪ the **ancestor closure** of that set (walk `parent_id` up; P2-1 — so the branch DAG
   never loses an ancestor and the 2c fork UI never dangles).
2. Delete snapshots **not** in keep-set.
3. Delete blobs whose hash appears in **no** surviving snapshot manifest **and** whose `created_at`
   is older than the **upload grace period** (e.g. 1 h) — so a blob `PUT` in flight before its
   `POST /snapshot` is never reaped (P1-2).

One transaction; counts logged. Constants (`maxSnapshots=100`, `maxAgeDays=90`, `blobGrace=1h`),
trivially tunable.

### 4.6 Front-end (Storage pane right sidebar)

Subsystem 1 reserved the right sidebar as a collapsed "備份（即將推出）" placeholder. Here it
becomes:

- **2b** — backup engine + status: a debounced (~2 s after last In-App write) auto-backup that
  walks `/buffer` (files **and** dirs), hashes each file (WebCrypto sha256), runs negotiation,
  uploads missing blobs, posts the snapshot; advances its local `parentId` to the returned
  `snapshotId`. **No-op suppression client-side too**: if the manifest equals the last successfully
  posted manifest, skip the whole round-trip. Sidebar shows "上次備份 {relative time}" / "備份中…"
  / inline error banner. **Refresh is local-mutation-driven** (after its own post); the
  `backup:done` host-event only matters for *another* device's update — WS is **never a blocker**
  for 2b's own flow (P2-5).
- **2c** — history/viewer/operator: a list of snapshots (device, time, trigger, fork badge) from
  `GET /history`, a manifest viewer (file list + size/words, no blob download to inspect), an
  explicit **Restore** button (the §4.4 guard + pre-restore + replace flow), and a fork/branch
  indicator when `parent_id` chains diverge.

## 5. Phases (each: own TDD + PR + codex two-round review)

### Phase 2a — daemon backup module (pure Go, no front-end)
`internal/module/backup/{module,handler,store}.go`. Blob store with hash-verified dedup + `413`
overflow; snapshot append with full §4.2 validation, no-op suppression, fork flag, `BEGIN
IMMEDIATE` serialisation; `missing` negotiation; history/get/blob endpoints; GC with grace period +
ancestor closure; `backup:done` broadcast. Registered like the sync module. **No front-end code**,
but the `backup:done` event contract (payload, empty `session` field, broadcast-after-commit
timing) is **defined and asserted here** so 2b has a stable contract.

**AC-2a**
- `PUT /blob/{hash}`: body sha256 ≠ `{hash}` → `400` nothing stored; non-hex `{hash}` → `400`; body
  over `cap` → `413` nothing stored; matching hash → stored once, re-`PUT` → `204` no duplicate row.
- `POST /missing` returns exactly the input subset the daemon lacks; over the count cap → `413`.
- `POST /snapshot` validation: missing blob → `409` no row; `parentId` of another `storeId` or a
  non-existent id → `400`; duplicate manifest path → `400`; a path containing `..`/leading-slash →
  `400`; `size` ≠ blob length → `400`; bad `kind` → `400`. None write a row.
- **No-op suppression**: posting a manifest byte-identical to head with `parentId==head` returns
  head's id and writes **no** new row.
- **Fork**: `parentId == head` → `is_fork:false`; stale `parentId` → `is_fork:true`; both rows
  present, neither overwritten, `parent_id` preserved; response carries `currentHeadId`.
- **Serialisation**: two concurrent `POST /snapshot` with the same `parentId` don't both yield
  `is_fork:false` (one observes the other's row) — asserted via a `BEGIN IMMEDIATE` path.
- **History/get/blob**: `GET /history` newest-first with correct `fileCount`/`dirCount`/`totalSize`,
  no blob bytes; `GET /snapshot/{id}` full manifest incl. `kind:'dir'` entries; `GET /blob/{hash}`
  exact bytes; absent → `404`.
- **GC**: exceeding 100 (or an aged row past 90 days, time injected) deletes surplus snapshots but
  **keeps ancestor closure** of survivors; blobs referenced by no survivor **and** past grace are
  removed, blobs still referenced (incl. shared across ≥2 snapshots) survive; an unattached blob
  within grace is **not** reaped.
- `backup:done` is broadcast after a committed snapshot with the documented payload.
- `:memory:` SQLite + `httptest`; `go test ./...` + `go vet` clean.

### Phase 2b — front-end backup engine + status
Walk `/buffer` (files+dirs), WebCrypto sha256, negotiation upload, snapshot post; debounced trigger
on In-App writes; local `parentId` advance from `snapshotId`; client-side no-op suppression;
right-sidebar status; device id from `getClientId()`. `backup:done` added to `HostEvent` union for
cross-device refresh only.

**AC-2b**
- Editing an In-App file triggers (after debounce) one backup: negotiation runs, only missing blobs
  upload, one snapshot posts. A **truly unchanged** tree is suppressed client-side (no negotiation,
  no post); a tree where only metadata-but-not-content changed still dedups blobs to **zero
  uploads** but, if the manifest differs, posts once.
- Browser sha256 round-trips: upload then `GET /blob/{hash}` is byte-identical.
- After a post, local `parentId` becomes the returned `snapshotId` (next backup is **not** a
  spurious self-fork even if `currentHeadId` differs).
- Empty directories appear in the manifest as `kind:'dir'`.
- Sidebar shows relative last-backup time, a "備份中…" state, and surfaces a failed upload as an
  inline error (never silent).
- Device id equals `useSyncStore.getState().getClientId()`.
- vitest + fake-indexeddb + mocked fetch; lint + `tsc` build green.

### Phase 2c — front-end restore + history/fork UI
History list, manifest viewer, explicit restore with dirty/locked **hard-block** + mandatory
pre-restore snapshot + dir-then-file replace, fork/branch indicator.

**AC-2c**
- History renders snapshots (device, relative time, trigger, fork badge) newest-first.
- Selecting a snapshot shows its manifest (paths + kind + size + words) without downloading blobs.
- **Restore guard**: with any dirty/locked In-App buffer, restore is **refused** with a Save/Discard
  prompt (no "continue anyway").
- **Restore**: a pre-restore snapshot is posted **first** (assert order), then the tree is replaced
  to exactly match the chosen manifest — every `kind:'dir'` recreated (incl. **empty** dirs), every
  file byte-identical, paths not in the manifest removed.
- A snapshot whose `parentId` is not the prior head renders a fork/branch indicator.
- vitest + RTL; lint + build green.

## 6. Testing
TDD per phase. Daemon: `:memory:` SQLite, `httptest`, testify; hash-verify, `413`/`400`/`409`,
no-op suppression, fork + `BEGIN IMMEDIATE` serialisation, GC with shared blobs + ancestor closure +
grace. Front-end: fake-indexeddb + mocked fetch (2b: dedup, no-op, parentId advance, dir manifest),
RTL + restore round-trip incl. empty-dir + dirty-block (2c). `go test ./...` and `pnpm run lint` +
`pnpm run build` + `npx vitest run` green each phase.

## 7. Non-goals
- **Per-file restore / per-file history** — whole-snapshot restore only (user decision §2).
- **Real-time multi-device merge / CRDT** — fork detection surfaces divergence; resolution is the
  user explicitly restoring one branch. No automatic merge.
- **Backing up anything other than the In-App `/buffer` tree** — daemon FS / workspace files
  (`FileTreeView`) are out of scope.
- **Encryption at rest** — blobs stored as-is in `backup.db` (single-user local daemon; consistent
  with existing `meta.db`/`sync.db`).
- **Reusing the Sync module's tables or data path** — separate `backup.db`, separate module. (Only
  the `getClientId()` getter is shared, not a runtime dependency.)
- **A hard size cap** — retention is a window (latest 100 / 90 days) bounded in practice by no-op
  suppression; a 90-day burst of genuinely distinct states is allowed to grow.

## 8. Known limitations (explicit)
- A device that never reconnects keeps its fork branch until GC ages it out; the UI shows it, there
  is no auto-prune of "abandoned" branches.
- `store_id` is fixed `'inapp:buffer'`; multiple logical stores are a future extension (column
  present, no migration).
- GC is best-effort on write + `Start()`, not a background timer.
- The upload grace period means a blob from an **abandoned** upload (PUT but never referenced) lives
  up to `blobGrace` before reaping — a bounded, intended cost of the in-flight-safety guarantee.
