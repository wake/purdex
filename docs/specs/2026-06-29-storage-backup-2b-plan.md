# Storage Subsystem 2 — Phase 2b Detailed Plan (front-end backup engine)

Base: alpha.303 (`9a22bda4`, Phase 2a daemon shipped). Worktree `storage-2b`.
Spec: `2026-06-29-storage-backup-spec.md` §4.6 (2b) + AC-2b, §4.1/4.2/4.3 (daemon contract).
Status: codex plan review R2 (R1's 1 CRITICAL + 4 P1 + 4 P2 + 2 P3 folded in, see §0).

**Front-end only (`spa/`).** Drives the Phase 2a daemon API. Walks the In-App `/buffer` tree,
hashes each file in the browser, negotiates + uploads missing blobs, posts a snapshot; the Storage
right sidebar shows backup status. No daemon changes (2a is the contract).

## 0. R1 plan-review fixes folded in (codex `task-mqy8n0d3`)
- **R1-C1**: auto-backup runs from a **persistent layer**, not `StoragePane` lifecycle — editing an
  In-App file in `EditorPane` with the Storage pane closed still backs up — T2b-6, AC.
- **R1-P1a**: `backup:done` refresh uses `applyRemoteBackupDone` that updates **only UI/status
  fields**, never `lastSnapshotId`/`lastManifestJSON` — T2b-3, T2b-4.
- **R1-P1b**: same-bytes change (rename/move) → `missing=[]`, **0 PUT, 1 POST** — T2b-3.
- **R1-P1c**: `words` is a **shared util** extracted from `StorageRow`, used by both — T2b-1.
- **R1-P1d**: host switch with a pending debounce — the timer **captures the mutation-time hostId**
  (and a host switch cancels a stale pending backup) — T2b-6.
- **R1-P2a**: task order fixed — `backup:done` (T2b-4) precedes the sidebar (T2b-5).
- **R1-P2b**: manifest builder does a **final independent byte-lexicographic sort of relative
  paths**, not `listTreeUnder`'s dirs-first/`localeCompare` order — T2b-1.
- **R1-P2c**: 2b mutation emitter covers `write/delete/mkdir/rename` only; `replaceTree` is 2c.
- **R1-P2d**: wiring pinned — `host-events.ts` extends the union, `useMultiHostEventWs.ts:99`
  parses + dispatches — T2b-4.
- **R1-P3a**: intra-snapshot duplicate-content blobs PUT once — T2b-1/T2b-3.
- **R1-P3b**: debounce timer is cleared on unmount/dispose — T2b-6.

## Existing groundwork (verified, reused)

- **Right-sidebar placeholder** (`StoragePane.tsx:524-529`): `<aside
  data-testid="storage-backups-placeholder" className="w-48 shrink-0 border-l …">Backups (coming
  soon)</aside>`. 2b replaces its content (keep aside shell + border).
- **Device id** (`use-sync-store.ts:166`): `useSyncStore.getState().getClientId()` → persisted
  `c_<hex>`. Reuse as `device` (getter only, not a Sync dependency).
- **Tree walk** (`storage-tree.ts:40` `listTreeUnder`): recursive, includes **empty** dirs.
  `STORAGE_ROOT='/buffer'` (`storage-paths.ts:13`); `relativeToRoot` helper there.
  `backend.read(path)→Uint8Array` (`fs-backend-inapp.ts:39`).
- **Word count source** (`StorageRow.tsx:18`): private extension allowlist + 256 KB cap +
  `TextDecoder + split(/\s+/)`. **T2b-1 extracts this to a shared util** (R1-P1c) so the row and the
  manifest builder share one SOT.
- **Daemon fetch** (`host-api.ts:31` `hostFetch(hostId, path, init)`): base + `Bearer`. Active host
  `useHostStore.getState().activeHostId`. Provider shape: `sync/providers/daemon-provider.ts:16`.
- **Host-event dispatch** (`host-events.ts:3` union; **per-host parse/dispatch in
  `hooks/useMultiHostEventWs.ts:99`**): 2b extends the union and routes `backup:done` in the hook.
- **Status banner** (`StoragePane.tsx:500-505`): `text-red-400` / `text-amber-400`. Relative time:
  `SyncSection.tsx formatRelativeTime(t, ms)` (i18n).
- **No existing**: browser sha256, In-App mutation event (both new — T2b-0/T2b-6).

## Design decisions

1. **Backup targets the active host's daemon.** The In-App tree is browser-local; a snapshot store
   lives on a daemon. `store_id` fixed `'inapp:buffer'` (spec §4.1) → each daemon holds an
   independent backup of this client's tree (the device-A-backs-up / device-B-restores model). Back
   up to `useHostStore.activeHostId`. **Local backup state is keyed by hostId**
   (`Record<hostId, HostBackupState>`) so switching hosts never causes a spurious self-fork.
2. **`lib/crypto-hash.ts`**: `sha256Hex(bytes): Promise<string>` via `crypto.subtle.digest`,
   64-char lowercase hex (matches daemon blob hash).
3. **`useBackupStore` (Zustand)** holds `byHost: Record<hostId, {status:'idle'|'backing-up'|'error',
   lastBackupAt:number|null, lastError:string|null, lastSnapshotId:number|null,
   lastManifestJSON:string|null}>`. Actions: `backupNow(hostId)` (the engine, advances lineage on a
   written post); **`applyRemoteBackupDone(hostId, payload)`** (R1-P1a) — updates **only**
   `lastBackupAt`/status for a cross-device refresh, and **must never** touch `lastSnapshotId` /
   `lastManifestJSON` (those are own-lineage and only `backupNow` may advance them).
4. **Client-side no-op suppression** (spec §4.6, R3-Pa): build canonical manifest; if its JSON equals
   this host's `lastManifestJSON`, skip the whole round-trip.
5. **`parentId` = this host's own `lastSnapshotId`** (spec P1-1), never the response's
   `currentHeadId`. After a written post, advance `lastSnapshotId` to the returned `snapshotId`.
6. **In-App mutation event** (T2b-6): `InAppBackend` gains `onMutation(cb): () => void`, fired after
   every `write`/`delete`/`mkdir`/`rename` commit (NOT `replaceTree` — that's 2c, R1-P2c). Additive,
   no behaviour change for non-subscribers.

## TDD tasks (each: failing test → impl → green → independent commit)

> Subagent: Bash prefixed `cd <worktree> &&`; absolute Edit/Write paths carry the
> `.claude/worktrees/storage-2b/` prefix. Per task `cd <worktree>/spa && npx vitest run <file>`
> green; before PR `pnpm run lint` + `pnpm run build` (tsc) + full `npx vitest run`.
> **Main Claude runs `pnpm install` + vitest/lint/build to verify (codex sandbox has no net).**

### T2b-0 — `lib/crypto-hash.ts` browser sha256
- **Test**: known vectors (empty → `e3b0c442…`, "abc" → `ba7816bf…`); 64-char lowercase hex.
- **Impl**: `crypto.subtle.digest('SHA-256', bytes)` → hex.

### T2b-1 — shared word-count util + manifest builder
- **Test** (fake-indexeddb + `InAppBackend`):
  - **word util** (extracted from `StorageRow`): a `.txt`/`.md`/`.env`/`.gitignore` text file →
    word count; a binary file → 0; a text file over the 256 KB cap → 0 (or capped per existing
    rule); `StorageRow` still renders the same counts (no regression).
  - **builder**: seed a nested tree incl. an **empty dir**, two files with **identical bytes**, a
    binary file. `buildManifest(backend)` → `{ entries, blobs: Map<hash,bytes> }` where entries are
    root-relative, **byte-lexicographically sorted by `path`** (assert a case that breaks
    dirs-first/`localeCompare` — e.g. a file `a.txt` vs dir `B/` must sort by raw path, R1-P2b),
    dirs `kind:'dir' hash:'' size:0 words:0`, files carry sha256+size+words; the **empty dir is
    present**; the two identical-byte files share one hash and `blobs` has **one** entry for it
    (R1-P3a).
- **Impl**: extract `lib/text-metrics.ts` (`wordCountFor(path, bytes)`) shared by `StorageRow` +
  builder. `buildManifest` walks `listTreeUnder`, reads files, hashes (T2b-0), then **sorts entries
  by relative path** as the final step; dedups blobs by hash.

### T2b-2 — backup API client
- **Test** (mocked `hostFetch`): `postMissing(host, hashes)` → `{missing}`; `putBlob(host, hash,
  bytes)` raw PUT, throws on non-204; `postSnapshot(host, req)` → `{snapshotId,isFork,currentHeadId}`,
  throws on non-2xx surfacing status (e.g. 409/400/413).
- **Impl**: `lib/storage-backup/backup-api.ts` over `hostFetch` for `/api/backup/missing|blob/{hash}
  |snapshot`.

### T2b-3 — backup engine + `useBackupStore`
- **Test** (mocked api + fake-idb):
  - first `backupNow(host)`: builds manifest, all hashes missing, each blob PUT once, posts, advances
    `lastSnapshotId` to returned id, sets `lastManifestJSON`, `status:'idle'`, `lastBackupAt`.
  - **no-op** (manifest == `lastManifestJSON`): skips negotiation + post.
  - **dedup**: one file's content changed → only the new blob PUT, posts once.
  - **same-bytes rename** (R1-P1b): `/buffer/a.txt`→`/buffer/b.txt`, identical bytes → `missing=[]`,
    **0 PUT**, **1 POST** (manifest differs by path so it's not a no-op).
  - **intra-snapshot dup** (R1-P3a): two files same bytes in one backup → **one** PUT.
  - `parentId` sent = prior `lastSnapshotId`, never `currentHeadId` (R1-P1a).
  - **`applyRemoteBackupDone`** (R1-P1a): a remote `backup:done` updates `lastBackupAt`/status but
    leaves `lastSnapshotId`/`lastManifestJSON` unchanged → the **next** `backupNow` still posts with
    the own prior `parentId` (remote head never pollutes local lineage).
  - failed upload → `status:'error'`+`lastError`, no lineage advance.
  - **per-host**: switching hostId uses that host's own state.
- **Impl**: `useBackupStore` (Design 3) + `backupNow(hostId)` orchestrating
  build→suppress→missing→putBlob(missing, deduped)→postSnapshot→advance; `device` from
  `getClientId()`; `applyRemoteBackupDone` (status-only).

### T2b-4 — `backup:done` HostEvent union + dispatch wiring
- **Test**: `HostEvent.type` includes `'backup:done'`; `useMultiHostEventWs` parses a `backup:done`
  event's `value` JSON `{storeId,snapshotId,currentHeadId,device,…}` and calls
  `applyRemoteBackupDone(hostId, payload)` — **only when `device !== getClientId()`** (own events
  ignored, R1-P1a). Asserts own-device event does not refresh.
- **Impl**: add `'backup:done'` to the union (`host-events.ts:3`); route it in
  `useMultiHostEventWs.ts:99` (the real per-host dispatch seam, R1-P2d) to `useBackupStore`.

### T2b-5 — right-sidebar status panel
- **Test** (RTL): renders "上次備份 {relative}" when `lastBackupAt` set, "備份中…" when
  `status:'backing-up'`, inline error banner when `status:'error'` (never silent); reflects the
  active host's state.
- **Impl**: replace the placeholder content (`StoragePane.tsx:524`) with a panel bound to
  `useBackupStore` (active host); reuse banner classes + `formatRelativeTime`. (Cross-device refresh
  is delivered via T2b-4's `applyRemoteBackupDone`; the panel just reflects store state.)

### T2b-6 — In-App mutation emitter + persistent debounced auto-backup
- **Test**:
  - `InAppBackend.onMutation(cb)` fires after `write`/`delete`/`mkdir`/`rename` (NOT `replaceTree`,
    R1-P2c); unsubscribe stops it.
  - **persistent trigger** (R1-C1): editing an In-App file via `EditorPane` **with the Storage pane
    NOT mounted** still schedules a backup (the trigger lives in a persistent layer, not
    `StoragePane`).
  - debounce coalesces rapid mutations into one `backupNow` after ~2 s (fake timers).
  - **host capture** (R1-P1d): the scheduled backup uses the hostId **at mutation time**; switching
    active host before the timer fires cancels/redirects so the tree isn't backed up to the wrong
    daemon (assert with fake timers + host switch).
  - **unmount cleanup** (R1-P3b): disposing the trigger clears a pending timer (no late
    `backupNow`).
- **Impl**: add the emitter to `InAppBackend` (additive); a persistent `backupAutoTrigger`
  (initialised at app/editor-module bootstrap, NOT in `StoragePane`) that subscribes, debounces
  ~2 s capturing the mutation-time hostId, calls `backupNow`, and exposes `dispose()`.

## Verification (before PR)
- `cd <worktree>/spa && pnpm install` (main Claude) then `npx vitest run` green, `pnpm run lint`
  clean, `pnpm run build` (tsc) OK.

## Out of scope (2c / later)
- **2c**: restore (history, manifest viewer, dirty-block guard, atomic `replaceTree` via
  `SupportsReplaceTree`, pane reconciliation incl. preview force-remount, fork UI).
- No daemon work (2a is the contract). No multi-store UI (`store_id` fixed `'inapp:buffer'`).
