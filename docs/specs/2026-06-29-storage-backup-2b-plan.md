# Storage Subsystem 2 — Phase 2b Detailed Plan (front-end backup engine)

Base: alpha.303 (`9a22bda4`, Phase 2a daemon shipped). Worktree `storage-2b`.
Spec: `2026-06-29-storage-backup-spec.md` §4.6 (2b) + AC-2b, §4.1/4.2/4.3 (daemon contract).

**Front-end only (`spa/`).** Drives the Phase 2a daemon API. Walks the In-App `/buffer` tree,
hashes each file in the browser, negotiates + uploads missing blobs, posts a snapshot; the Storage
right sidebar shows backup status. No daemon changes (2a is the contract).

## Existing groundwork (verified, reused)

- **Right-sidebar placeholder** (`spa/src/components/editor/storage/StoragePane.tsx:524-529`): an
  `<aside data-testid="storage-backups-placeholder" className="w-48 shrink-0 border-l …">Backups
  (coming soon)</aside>`. 2b replaces its content with the backup status panel (keep the aside
  shell + border).
- **Device id** (`spa/src/lib/sync/use-sync-store.ts:166`): `useSyncStore.getState().getClientId()`
  → persisted `c_<hex>` (generated `:120`). Reuse as `device`; **getter only, not a Sync runtime
  dependency** (spec §3).
- **Tree walk** (`spa/src/lib/storage-tree.ts:40` `listTreeUnder(backend, root)`): recursive,
  includes dirs (and **empty** dirs — `isDir` nodes). `STORAGE_ROOT='/buffer'`
  (`storage-paths.ts:13`). `backend.read(path)→Uint8Array` (`fs-backend-inapp.ts:39`).
- **Daemon fetch** (`spa/src/lib/host-api.ts:31` `hostFetch(hostId, path, init)`): prepends
  `getDaemonBase(hostId)` + `Authorization: Bearer <token>`. Backup targets the **active host**
  (`useHostStore.getState().activeHostId`) — see Design 1. Provider shape reference:
  `sync/providers/daemon-provider.ts:16`.
- **Host events** (`spa/src/lib/host-events.ts:3`): `HostEvent.type` union + `connectHostEvents`.
  2b adds `'backup:done'` to the union and refreshes on it (cross-device only).
- **Status banner** (`StoragePane.tsx:500-505`): `text-red-400` error / `text-amber-400` warning.
  Relative time: `SyncSection.tsx formatRelativeTime(t, ms)` (i18n) — reuse the i18n variant.
- **No existing**: browser sha256 util, In-App mutation event. Both new (Design 2, T2b-0/T2b-4).

## Design decisions

1. **Backup targets the active host's daemon.** The In-App tree is browser-local (not host-scoped),
   but a snapshot store lives on a daemon. `store_id` is the fixed `'inapp:buffer'` (spec §4.1), so
   each daemon holds an independent backup of this client's tree — the natural model for "device A
   backs up to its daemon, device B restores from it". 2b backs up to `useHostStore.activeHostId`;
   switching hosts targets a different store. Local backup state (`lastSnapshotId`, `lastManifest`)
   is **keyed by hostId** so switching hosts doesn't cause a spurious self-fork.
2. **New leaf util `lib/crypto-hash.ts`**: `sha256Hex(bytes: Uint8Array): Promise<string>` via
   `crypto.subtle.digest('SHA-256', …)` → 64-char lowercase hex (matches the daemon's blob hash).
3. **A `useBackupStore` (Zustand) owns reactive backup state + the engine action.** Fields per host:
   `{ status: 'idle'|'backing-up'|'error', lastBackupAt: number|null, lastError: string|null,
   lastSnapshotId: number|null, lastManifestJSON: string|null }`. Action `backupNow(hostId)` runs
   the engine. The sidebar subscribes for status; mutation/debounce calls `backupNow`.
4. **Client-side no-op suppression** (spec §4.6, R3-Pa): the engine builds the canonical manifest,
   and if its JSON equals `lastManifestJSON` for this host, it **skips the entire round-trip** (no
   negotiation, no post). The daemon also suppresses content-equal posts, but skipping client-side
   avoids needless traffic.
5. **`parentId` = this host's own `lastSnapshotId`** (spec P1-1), never the response's
   `currentHeadId`. After a written post, advance `lastSnapshotId` to the returned `snapshotId`.
6. **In-App mutation event** (Design for T2b-4): `InAppBackend` gains a tiny emitter —
   `onMutation(cb): () => void` — fired after every `write`/`delete`/`mkdir`/`rename`/`replaceTree`
   commit. The engine subscribes and debounces (~2 s) before `backupNow`. This is the only change to
   an existing lib file; it is additive (no behaviour change for non-subscribers).
7. **`words` metric**: reuse subsystem 1's text-vs-binary + word-count logic (StorageRow row
   metadata; plan T2b-1 pins the exact util). Binary → `words:0`; dirs → `words:0` (and `hash:''`,
   `size:0`).

## TDD tasks (each: failing test → impl → green → independent commit)

> Subagent: Bash prefixed `cd <worktree> &&`; absolute Edit/Write paths carry the
> `.claude/worktrees/storage-2b/` prefix. Per task: `cd <worktree>/spa && npx vitest run <file>`
> green; before PR: `pnpm run lint` + `pnpm run build` (tsc) + full `npx vitest run` green.
> **Main Claude must `pnpm install` + run vitest/lint/build to verify** (codex sandbox has no net).

### T2b-0 — `lib/crypto-hash.ts` browser sha256
- **Test**: `sha256Hex(new Uint8Array([...]))` equals known SHA-256 vectors (e.g. empty input →
  `e3b0c442…`, "abc" bytes → `ba7816bf…`); output is 64-char lowercase hex; matches what the daemon
  computes (same algorithm).
- **Impl**: `crypto.subtle.digest('SHA-256', bytes)` → hex. Pure leaf util.

### T2b-1 — manifest builder
- **Test** (fake-indexeddb + `InAppBackend`): seed a nested tree incl. an **empty dir**, a text file,
  a binary file; `buildManifest(backend)` returns entries `{path,kind,hash,size,words}` that are:
  root-relative to `/buffer`, **canonically sorted** (matches daemon §4.2 order), dirs as
  `kind:'dir' hash:'' size:0 words:0`, files with sha256 hash + byte size + word count (text) / 0
  (binary), empty dir present. Returns `{ entries, blobs: Map<hash, Uint8Array> }`.
- **Impl**: `buildManifest(backend)` walks via `listTreeUnder`, reads each file, hashes (T2b-0),
  computes words (reused util, Design 7), emits canonical-sorted entries + a hash→bytes map for
  upload. Path = `relativeToRoot(STORAGE_ROOT, fullPath)`.

### T2b-2 — backup API client
- **Test** (mocked `hostFetch`/`fetch`): `postMissing(hostId, hashes)` → posts `{hashes}`, returns
  `missing[]`; `putBlob(hostId, hash, bytes)` → PUT raw body, throws on non-204; `postSnapshot(hostId,
  req)` → returns `{snapshotId, isFork, currentHeadId}`, throws on non-2xx with status surfaced.
- **Impl**: `lib/storage-backup/backup-api.ts` thin wrappers over `hostFetch` for
  `/api/backup/missing|blob/{hash}|snapshot`. JSON for missing/snapshot, raw body for blob.

### T2b-3 — backup engine + `useBackupStore`
- **Test** (mocked api + fake-idb): (a) first `backupNow(host)`: builds manifest, negotiation returns
  all hashes missing, uploads each blob once, posts snapshot, advances `lastSnapshotId` to returned
  id, sets `lastManifestJSON`, `status:'idle'`, `lastBackupAt` set. (b) **no-op**: an unchanged tree
  (manifest == `lastManifestJSON`) skips negotiation+post entirely. (c) **dedup**: a tree where one
  file changed uploads only the new blob, posts once. (d) `parentId` sent = prior `lastSnapshotId`
  (not `currentHeadId`). (e) a failed upload sets `status:'error'` + `lastError`, doesn't advance
  state. (f) state is **per-host** (switching hostId uses that host's lastSnapshotId).
- **Impl**: `useBackupStore` (Design 3) + `backupNow(hostId)` orchestrating
  build→suppress→missing→putBlob(missing)→postSnapshot→advance. `device` from `getClientId()`.

### T2b-4 — In-App mutation emitter + debounced auto-backup
- **Test**: (a) `InAppBackend.onMutation(cb)` fires after write/delete/mkdir/rename/replaceTree, and
  the unsubscribe stops it. (b) a debounce wrapper coalesces rapid mutations into a single
  `backupNow` after ~2 s (fake timers); (c) no mutation → no backup.
- **Impl**: add an emitter to `InAppBackend` (additive); a `useBackupAutoTrigger` hook/util that
  subscribes, debounces (~2 s), and calls `backupNow(activeHostId)`. Wire it where the Storage
  app mounts (plan-time: alongside the Storage pane lifecycle).

### T2b-5 — right-sidebar status panel
- **Test** (RTL): the panel renders "上次備份 {relative}" when `lastBackupAt` set, a "備份中…"
  state when `status:'backing-up'`, an inline error banner when `status:'error'` (never silent); a
  `backup:done` host-event **from a different device** triggers a refresh, one from **this** device
  does not double-fire.
- **Impl**: replace the placeholder content (`StoragePane.tsx:524`) with a panel bound to
  `useBackupStore`; subscribe to `backup:done` (T2b-6) for cross-device refresh. Reuse the
  error/warning banner classes + `formatRelativeTime`.

### T2b-6 — `backup:done` in `HostEvent` union + wiring
- **Test**: `HostEvent.type` includes `'backup:done'`; the host-events handler routes a
  `backup:done` event (parsing `value` JSON `{storeId,snapshotId,currentHeadId,device,…}`) to the
  backup store's refresh, ignoring events whose `device` == own `getClientId()`.
- **Impl**: add `'backup:done'` to the union (`host-events.ts:3`); route it in the existing host-event
  dispatch to `useBackupStore`.

## Verification (before PR)
- `cd <worktree>/spa && pnpm install` (main Claude, has net) then `npx vitest run` green,
  `pnpm run lint` clean, `pnpm run build` (tsc) OK.
- New suites: crypto-hash vectors, manifest canonical/empty-dir/words, api client, engine
  no-op/dedup/parentId/per-host/error, emitter+debounce, sidebar states, backup:done routing.

## Out of scope (2c / later)
- **2c**: restore (history list, manifest viewer, dirty-block guard, atomic `replaceTree` via
  `SupportsReplaceTree`, pane reconciliation incl. preview force-remount, fork/branch UI).
- No new daemon work (2a is the contract). No multi-store UI (`store_id` fixed `'inapp:buffer'`).
