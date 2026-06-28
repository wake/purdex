# Plan — Storage backup Phase 2c: front-end restore + history/fork UI

Implements spec `2026-06-29-storage-backup-spec.md` §4.4 + §4.7 (2c) + AC-2c. The daemon (2a) and
the front-end backup engine (2b) are shipped; 2c adds **read + restore** on the client.

## What already exists (do not rebuild)

- **Daemon read API** (2a): `GET /api/backup/history?storeId=` → `[]SnapshotSummary`
  (`{id,device,parentId,isFork,trigger,createdAt,fileCount,dirCount,totalSize}`, newest-first);
  `GET /api/backup/snapshot/{id}` → `SnapshotDetail` (`{…,manifest:[{path,kind,hash,size,words}]}`,
  `404` absent); `GET /api/backup/blob/{hash}` → raw bytes, `404` absent.
- **Write client** (2b, `lib/storage-backup/backup-api.ts`): `postMissing` / `putBlob` /
  `postSnapshot`. `SnapshotRequest.trigger` is already a free string.
- **Engine** (2b, `stores/useBackupStore.ts`): `backupNow(hostId)` walks the tree → manifest →
  negotiate → upload → `postSnapshot(trigger:'auto')`, advances per-host lineage. `buildManifest`
  (`lib/storage-backup/manifest.ts`) produces canonical `{entries, blobs}`.
- **Backend** (`lib/fs-backend-inapp.ts` `InAppBackend`): `write/read/delete/mkdir/rename` +
  `SupportsUniqueCreate`; emits `onMutation` on every mutating commit **except** a future
  `replaceTree` (2b deliberately excluded it).
- **Editor/tab plumbing** (verified on main): `useEditorStore.reloadBuffer(key, content, stat?)`
  re-aligns `content/savedContent/isDirty/lastStat` (`:52`); `useEditorStore.isDirty` (`:22`);
  `tab-lifecycle.ts:11-20` scans `tab.locked` + `inapp` `isDirty` on close; `storage-actions.ts:531`
  close-pane-**before**-mutate ordering (G2). Preview panes re-read only on `source/filePath/backend`
  change (`ImagePreviewPane.tsx`, `PdfPreviewPane.tsx`).

## Decisions (user, this session)

1. **Review**: full codex two-round (data-safety: restore atomically clears + rewrites `/buffer`).
2. **PR split**: **2c-1 engine** (headless: read client + `replaceTree` capability + restore
   orchestrator + guard) → **2c-2 UI** (history sidebar list + manifest modal + Restore wiring +
   pane reconciliation).
3. **UI layout**: right sidebar keeps the **compact history list**; clicking a row opens a
   **modal/drawer** with the manifest viewer + Restore. Sidebar width unchanged.

---

## PR 2c-1 — restore engine (headless, vitest + fake-indexeddb + mocked fetch)

Pure data/IDB layer with **no React**. Everything here is unit-testable without RTL.

### T1 — read API client (`backup-api.ts`)
Add to the existing client:
- `getHistory(hostId, storeId): Promise<SnapshotSummary[]>` — `GET /history?storeId=`; throws on
  non-2xx surfacing status.
- `getSnapshot(hostId, id): Promise<SnapshotDetail>` — `GET /snapshot/{id}`; `404` → throw typed
  "not found".
- `getBlob(hostId, hash): Promise<Uint8Array>` — `GET /blob/{hash}`; reads `arrayBuffer()` → bytes;
  `404`/non-2xx → throw surfacing status.
- Export `SnapshotSummary` / `SnapshotDetail` / `ManifestEntry` TS types mirroring the daemon JSON.
- **Tests**: each verb's URL/method, JSON parse, error-status propagation (`404` snapshot, `404`
  blob, `500`).

### T2 — `replaceTree` capability (`fs-backend.ts` + `fs-backend-inapp.ts`)
- `fs-backend.ts`: add `interface SupportsReplaceTree { replaceTree(root: string, entries:
  ReplaceEntry[]): Promise<void> }` and `supportsReplaceTree(backend): backend is FsBackend &
  SupportsReplaceTree` guard — mirroring `SupportsUniqueCreate` (R3-Pc, **no type cast**).
  `ReplaceEntry = { path: string; isDir: boolean; bytes?: Uint8Array }` (full path under root).
- `InAppBackend.replaceTree(root, entries)`: **one** IDB `readwrite` txn — (a) delete every key
  under `root` (the `STORAGE_ROOT` subtree, prefix match like the recursive `delete`); (b) write
  entries **dirs-first** (so empty dirs survive, C2), each `StoredFile` with `isDirectory`
  accordingly; (c) `await tx.done`. **Does NOT call `emitMutation`** (restore must not trigger an
  auto-backup; consistent with 2b's "non replaceTree" note). Re-validate each path client-side
  (root-relative join, no `..`/leading-slash/backslash) before writing (defence-in-depth, §4.2).
- **Tests** (fake-indexeddb): replaces a populated tree to match a target exactly (added/removed/
  changed/empty-dir); single-txn (a thrown error mid-write leaves the *prior* tree — use a spy/abort
  to assert no partial commit); `onMutation` is **not** fired by `replaceTree`.

### T3 — restore orchestrator (`lib/storage-backup/restore.ts`)
`restoreSnapshot(deps)` — dependency-injected, no store/React imports, so 2c-1 tests it with spies
and 2c-2 supplies live deps. Order **exactly** per §4.4:
1. **Guard** (T4) — if blocked, return `{ status:'blocked', conflicts }` **without** any network/IDB.
2. **Pre-restore snapshot** — build current manifest (`buildManifest`), negotiate+upload missing
   blobs, `postSnapshot(trigger:'pre-restore', parentId: own last id)`; record returned id as the
   restore-point (content-keyed no-op → head id, R2-Pf). Reuse the 2b engine path (see T3a).
3. **Fetch + verify all blobs** — `getSnapshot(S)`; for every `kind:'file'` entry `getBlob(hash)`,
   assert sha256(bytes)==hash **and** bytes.length==size, into an in-memory `Map`. Any failure →
   **throw before any IDB mutation** (R2-Pa atomicity); tree untouched.
4. **`replaceTree`** — via `supportsReplaceTree` guard; build `ReplaceEntry[]` from the manifest
   (dirs + files with fetched bytes).
5. **Return** `{ status:'done', restorePointId, changed:{added,removed,modified} }` computed by
   diffing the pre-restore manifest vs the restored manifest (paths + content hash) — 2c-2 uses this
   for pane reconciliation. **No reconciliation here** (UI-coupled → 2c-2).
- **T3a — generalise pre-restore backup**: extend `useBackupStore.backupNow` to accept
  `{ trigger?: string }` (default `'auto'`) **and return the resulting `snapshotId`** (currently
  `void`), so restore can post `trigger:'pre-restore'` and capture the restore-point through the same
  lineage-converging path. Keep the no-op-suppression + per-host lineage semantics intact (existing
  2b tests must stay green).
- **Tests**: order asserted (pre-restore posted **before** any `getBlob`); content-equal current
  tree → pre-restore writes no new row, head recorded as restore-point; a `getBlob` `404` mid-flow →
  `replaceTree` **never called**, tree unchanged (atomic rollback); happy path → `replaceTree` called
  with the exact manifest, `changed` diff correct.

### T4 — dirty/locked guard (`lib/storage-backup/restore-guard.ts`)
Pure predicate over `useEditorStore` + `useTabStore` state (injected getters): returns the list of
conflicting buffers/tabs = **any `inapp` editor buffer with `isDirty`**, or **any `locked` tab whose
panes include an `inapp` editor pane** (`source.type==='inapp'`) — the same surface as
`tab-lifecycle.ts`. Empty list ⇒ restore may proceed.
- **Tests**: dirty inapp buffer blocks; locked tab with inapp pane blocks; clean unlocked ⇒ allowed;
  a dirty **non-inapp** buffer does **not** block.

**2c-1 done-criteria**: `go test` untouched/green; `npx vitest run` green (new headless suites);
`pnpm run lint` + `pnpm run build` green. No UI yet.

---

## PR 2c-2 — history / viewer / restore UI (RTL)

Wires 2c-1 into the Storage pane right sidebar + modal.

### T5 — history list (sidebar, compact)
Extend `BackupStatusSidebar` (or a sibling `BackupHistoryList`): below the status line, render
`getHistory(activeHost, 'inapp:buffer')` rows newest-first — device, relative time (reuse
`formatRelativeTime`), trigger, **fork badge** when `isFork`. Loading / error / empty states.
**Refresh** on: panel mount, own `backupNow` completion, and a cross-device `backup:done`
(`useBackupStore` already gets the event; expose a `historyNonce`/subscription so the list refetches).
- **Tests** (RTL): rows render newest-first with badges; empty/error states; refetch on
  `backup:done`.

### T6 — manifest viewer modal
Clicking a history row opens a modal/drawer: `getSnapshot(id)` → file list (path, kind, size, words),
**no blob download**; header shows device/time/trigger/fork; **Restore** + close buttons. Reuse the
app's existing modal/portal pattern (match `RenamePopover`/existing dialogs).
- **Tests** (RTL): opens with manifest from `getSnapshot`; lists dirs + files incl. empty dirs;
  close; no `getBlob` call on open.

### T7 — Restore wiring
Restore button → `restoreSnapshot` (live deps). On `status:'blocked'`: show a **Save/Discard** prompt
naming the conflicts (no "continue anyway", C1). On `status:'done'`: run **pane reconciliation** (T8),
close the modal, refresh history, surface success; on throw: inline error, tree already guaranteed
untouched/rolled-back.
- **Tests** (RTL): blocked path shows prompt and performs **no** restore; happy path calls
  reconciliation then refreshes.

### T8 — pane reconciliation (R3-Pb)
Using `restoreSnapshot`'s `changed` diff, after `replaceTree` (dirty buffers already refused, so all
open `inapp` panes are clean): (a) **close** `inapp` editor panes whose path was **removed**; (b)
**reloadBuffer** clean `inapp` editors whose content **changed** (new bytes + `lastStat`); (c) for
`image-preview`/`pdf-preview`: **close** removed-path panes, **force-remount (close+reopen)**
changed-path panes (R4-P2 — in-place refresh insufficient). Reuse `storage-actions.ts` close-pane
ordering and `scanPaneTree` to enumerate open inapp panes across tabs.
- **Tests** (RTL): removed-path editor closed; changed clean editor reloaded — **`savedContent`/
  `isDirty:false`/`lastStat` all updated** so a subsequent save does **not** clobber restored bytes;
  changed-path preview force-remounted (close+reopen), removed-path preview closed.

**2c-2 done-criteria**: full `npx vitest run` green; `pnpm run lint` + `pnpm run build` green.
RTL covers the AC-2c restore/reconciliation/atomic-rollback/fork assertions.

---

## Risks / notes
- **`backupNow` signature change (T3a)** touches shipped 2b code — keep all 2b tests green; the
  `trigger`/return additions are additive.
- **`replaceTree` must not emit `onMutation`** — otherwise restore self-triggers an auto-backup loop.
  Explicit test.
- **Atomicity boundary**: the only IDB write is `replaceTree`, entered **after** all blobs verified.
  Pre-restore snapshot (network) happens first by design; if it fails, restore aborts before fetch.
- **Restore memory peak** (spec §8 R3-Pd): all blobs buffered in memory before the txn — accepted,
  bounded by the ~25 MB file cap + small trees.
- **Multi-host**: history/restore target the **active** host (`activeHostId ?? hostOrder[0]`),
  consistent with 2b; `storeId` fixed `'inapp:buffer'`.
