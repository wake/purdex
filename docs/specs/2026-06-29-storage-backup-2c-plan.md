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
  **Path contract (codex P2-a — pick one, written down):** `ReplaceEntry = { relPath: string;
  isDir: boolean; bytes?: Uint8Array }` where `relPath` is **always root-relative** (e.g. `a/b.md`,
  **never** leading-slash, `STORAGE_ROOT`-prefixed, or absolute). The backend computes the stored
  key as `join(root, relPath)`. This removes the "full path under root" vs "no leading-slash"
  contradiction the reviewer flagged.
- `InAppBackend.replaceTree(root, entries)`: **one** IDB `readwrite` txn — (a) delete every key
  under `root` (the `STORAGE_ROOT` subtree, prefix match like the recursive `delete`); (b) write
  entries **dirs-first** (so empty dirs survive, C2), each `StoredFile` with `isDirectory`
  accordingly; (c) `await tx.done`. **Does NOT call `emitMutation`** (restore must not trigger an
  auto-backup; consistent with 2b's "non replaceTree" note). Re-validate each `relPath` client-side
  (root-relative, non-empty, no `..`/leading-slash/backslash, unique, no prefix-conflict) **before**
  opening the txn (defence-in-depth, §4.2). Because clear+write share **one** txn, a write failure
  aborts the whole txn → IDB rolls back, so the prior tree is never left half-cleared (assert).
- **Tests** (fake-indexeddb): replaces a populated tree to match a target exactly (added/removed/
  changed/empty-dir); single-txn (a thrown error mid-write leaves the *prior* tree — use a spy/abort
  to assert no partial commit); `onMutation` is **not** fired by `replaceTree`.

### T3 — restore orchestrator (`lib/storage-backup/restore.ts`)
`restoreSnapshot(deps)` — dependency-injected, no store/React imports, so 2c-1 tests it with spies
and 2c-2 supplies live deps. Order **exactly** per §4.4:
0. **Acquire the per-host backup lock** (T3a single-flight) — wait for any in-flight auto-backup on
   the same host to finish, and hold the lock through step 2 so a debounced auto-backup cannot
   interleave the pre-restore (codex P1-a). Released after step 2 (the actual restore writes nothing
   to the daemon).
1. **Guard** (T4) — if blocked, return `{ status:'blocked', conflicts }` **without** any network/IDB
   (and without taking the lock effects).
2. **Pre-restore snapshot** — build current manifest (`buildManifest`), negotiate+upload missing
   blobs, `postSnapshot(trigger:'pre-restore', parentId: own last id)`; record returned id as the
   restore-point. **Must always reach the daemon (codex C1):** call the 2b engine with
   `forcePost:true` so the *client-side* no-op suppression is bypassed — otherwise a tree equal to
   `lastManifestJSON` would short-circuit and never POST, leaving no restore-point. The daemon's
   *content-keyed* no-op still returns the existing head id (R2-Pf/R3-Pa); that head **is** the
   restore-point. Record the returned id (new or head).
3. **Fetch + verify all blobs** — `getSnapshot(S)`; for every `kind:'file'` entry `getBlob(hash)`,
   assert sha256(bytes)==hash **and** bytes.length==size, into an in-memory `Map`. Any failure →
   **throw before any IDB mutation** (R2-Pa atomicity); tree untouched.
4. **`replaceTree`** — via `supportsReplaceTree` guard; build `ReplaceEntry[]` from the manifest
   (dirs + files with fetched bytes).
5. **Return** `{ status:'done', restorePointId, changed:{added,removed,modified} }` computed by
   diffing the pre-restore manifest vs the restored manifest (paths + content hash) — 2c-2 uses this
   for pane reconciliation. **No reconciliation here** (UI-coupled → 2c-2).
- **T3a — generalise the backup engine + add per-host single-flight**:
  - Extend `useBackupStore.backupNow(hostId, opts?)` with `opts = { trigger?: string (default
    'auto'), forcePost?: boolean (default false) }` **and return the resulting `snapshotId | null`**
    (currently `void`). `forcePost:true` (pre-restore) **bypasses the client-side no-op suppression**
    so the post always reaches the daemon; `trigger:'auto'` keeps the existing client no-op + lineage
    semantics unchanged (codex C1).
  - **Per-host single-flight** (codex P1-a): serialize all daemon-writing work per host through a
    `byHost[host].inFlight` promise so an auto-backup and a pre-restore (or two auto-backups) never
    overlap and corrupt `parentId`/produce a spurious fork. `backupNow` coalesces/awaits the in-flight
    promise; restore (step 0) awaits it then runs the pre-restore inside the same serialization.
  - **Tests**: `forcePost` posts even when the manifest equals `lastManifestJSON`; default `auto`
    still suppresses client-side; a concurrent `backupNow`+pre-restore on one host run **strictly
    sequentially** (no interleaved POST); return value is the daemon `snapshotId`. Existing 2b tests
    stay green (additive opts).
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
**Refresh** on: panel mount, **active-host switch** (codex P2-b — `hostId` is an explicit fetch
dependency; the sidebar reacts to `activeHostId` but does not unmount on switch, so without this it
would show the previous host's history), own `backupNow` completion, and a cross-device `backup:done`
(`useBackupStore` already gets the event; expose a `historyNonce`/subscription so the list refetches).
- **Tests** (RTL): rows render newest-first with badges; empty/error states; refetch on
  `backup:done`; **switching host A→B refetches and shows B's history** (codex P2-b).

### T6 — manifest viewer modal
Clicking a history row opens a modal/drawer: `getSnapshot(id)` → file list (path, kind, size, words),
**no blob download**; header shows device/time/trigger/fork; **Restore** + close buttons. Reuse the
app's existing modal/portal pattern (match `RenamePopover`/existing dialogs).
- **Tests** (RTL): opens with manifest from `getSnapshot`; lists dirs + files incl. empty dirs;
  close; no `getBlob` call on open.

### T7 — Restore wiring
Restore button → `restoreSnapshot` (live deps). Restore is **disabled while the host's status is
`backing-up`** (codex P1-a UI guard), complementing the engine single-flight. On `status:'blocked'`:
show a prompt that **only lists the conflicting buffers/tabs and aborts** the restore (codex P3 — it
does **not** implicitly batch-save or batch-discard; the user resolves them via the existing
editor/tab Save/Discard actions, then re-runs Restore). No "continue anyway" (C1). On `status:'done'`:
run **pane reconciliation** (T8), close the modal, refresh history, surface success; on throw: inline
error, tree already guaranteed untouched/rolled-back.
- **Tests** (RTL): Restore disabled during `backing-up`; blocked path shows the conflict list and
  performs **no** restore and **no** implicit save/discard; happy path calls reconciliation then
  refreshes.

### T8 — pane reconciliation (R3-Pb) + concrete remount primitive (codex P1-b)
**Remount primitive (codex P1-b — the plan now names a landing mechanism).** Verified: the layout
renderer keys each leaf by `getLayoutKey(child)` = `pane.id` (`PaneLayoutRenderer.tsx:167/180/210`,
`pane-tree.ts:49`). So **assigning a fresh `pane.id` to a leaf in place** changes its React key →
forces unmount+remount → the preview re-runs its `[identity, backend]` read effect
(`ImagePreviewPane.tsx:74`) and shows the new bytes — **without** moving the leaf or touching the
split layout. `openInAppFile` (singleton focus) and `setPaneContent` (same id, no remount) are both
insufficient and must NOT be used for this.
- Add pure `pane-tree.ts` helper `remountLeaf(layout, paneId): { layout, newPaneId } | null` —
  clones the target leaf at the **same tree position** with a fresh id, content unchanged.
- Add `useTabStore.remountPane(tabId, paneId)` — applies `remountLeaf`; if the remounted pane was
  `activePaneId`, migrate it to `newPaneId`. (Preview panes hold no `useEditorStore` buffer, so no
  buffer/paneState migration is needed beyond active-pane.)
- Both are headless-unit-testable (pane-tree pure; store action with the zustand harness).

Using `restoreSnapshot`'s `changed` diff, after `replaceTree` (dirty buffers already refused, so all
open `inapp` panes are clean): (a) **close** `inapp` editor panes whose path was **removed**; (b)
**reloadBuffer** clean `inapp` editors whose content **changed** (new bytes + `lastStat`); (c) for
`image-preview`/`pdf-preview`: **close** removed-path panes, and for **changed**-path panes call
`remountPane` (R4-P2 — in-place refresh insufficient). Reuse `storage-actions.ts:531` close-pane
ordering and `scanPaneTree` to enumerate open `inapp` panes across **all** tabs.
- **Tests** (RTL + harness): removed-path editor closed; changed clean editor reloaded —
  **`savedContent`/`isDirty:false`/`lastStat` all updated** so a subsequent save does **not** clobber
  restored bytes; changed-path preview **remounted via a new pane id at the same layout position**
  (assert id changed, position preserved, re-read fired), removed-path preview closed; **a
  split-layout case** confirms the sibling pane is untouched (codex P1-b).

**2c-2 done-criteria**: full `npx vitest run` green; `pnpm run lint` + `pnpm run build` green.
RTL covers the AC-2c restore/reconciliation/atomic-rollback/fork assertions.

---

## Risks / notes
- **`backupNow` signature change (T3a)** touches shipped 2b code — keep all 2b tests green; the
  `trigger`/`forcePost`/return additions are additive.
- **pre-restore must bypass the client no-op (C1)** — `forcePost:true`; without it a tree equal to
  `lastManifestJSON` never posts and there is no restore-point. Engine + restore tests assert it.
- **restore ⇄ auto-backup serialization (P1-a)** — a per-host single-flight in the backup layer is a
  prerequisite, not optional; restore acquires it before the pre-restore. UI also disables Restore
  while `backing-up`.
- **preview remount (P1-b)** — done by swapping the leaf's `pane.id` (new React key), NOT
  `openInAppFile`/`setPaneContent`; preserves layout position. Verified against
  `PaneLayoutRenderer`/`pane-tree` keying.
- **`replaceTree` must not emit `onMutation`** — otherwise restore self-triggers an auto-backup loop.
  Explicit test.
- **Atomicity boundary**: the only IDB write is `replaceTree`, entered **after** all blobs verified.
  Pre-restore snapshot (network) happens first by design; if it fails, restore aborts before fetch.
- **Restore memory peak** (spec §8 R3-Pd): all blobs buffered in memory before the txn — accepted,
  bounded by the ~25 MB file cap + small trees.
- **Multi-host**: history/restore target the **active** host (`activeHostId ?? hostOrder[0]`),
  consistent with 2b; `storeId` fixed `'inapp:buffer'`.
