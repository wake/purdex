# Storage Phase 1b — Detailed Plan (Nested CRUD + recursive folder move)

Base: alpha.300. Worktree `storage-filemanager-1b`. Spec:
`2026-06-27-storage-filemanager-spec.md` §"Phase 1b" + AC-1b.
Parent plan outline: `2026-06-27-storage-filemanager-plan.md` §"Phase 1b".

T1b-1 (backend recursive rename/move) is **already shipped** in this worktree
(`402c05ce`, `InAppBackend.rename` re-keys every `from/`-prefixed descendant in one
IDB tx + full collision scan; 23 backend tests green). This plan covers **T1b-2 … T1b-6**.

## Existing groundwork (from 1a + T1b-1) — reused, not rebuilt

- `InAppBackend` (`spa/src/lib/fs-backend-inapp.ts`): `write` (auto-creates parent dirs in
  one tx), `mkdir`, `delete(path, recursive)` (prefix-sweep), **`rename(from,to)` recursive
  re-key** (T1b-1), `list` (direct children), `stat`.
- `storage-paths.ts`: `STORAGE_ROOT='/buffer'`, `join`, `parentOf`, `basename`,
  `relativeToRoot`, `isUnderRoot`.
- `storage-tree.ts`: `TreeNode {path,name,isDir,size,children?}`, `listTreeUnder`.
- `useStorageTree` hook: `{tree,loading,error,expanded,refresh,toggle}` (full-path identity).
- `StoragePane.tsx` (271): toolbar + `StorageTree` + Backups placeholder; `handleNew`/
  `handleRenameConfirm`/`handleDelete`/`handleOpen`.
- `storage-actions.ts` (183): `createStorageFile`, `renameStorageEntry(from,newName)` (stat
  collision guard :87-92), `deleteStorageEntries(targets,t)` (locked-tab refusal :139 +
  dirty-pane confirm :145-154).
- dnd-kit already a dep (`@dnd-kit/core/sortable/utilities`); pattern in
  `RegionManager.tsx:63-76` (PointerSensor distance:5, DndContext+SortableContext).

## Design decisions (resolve before impl — flag any reversal in review)

1. **#854 reservation is atomic at the IDB layer, not a name scan.** Root cause: EditorPane
   (`EditorPane.tsx:424`) and StoragePane (`storage-actions.ts:58`) both name via
   `Untitled-${Date.now()}.md` → same-ms double-click collides; EditorNewTabSection
   (`EditorNewTabSection.tsx:18-52`) scans only one level. A scan-then-write namer is still
   racy. **Fix:** add `InAppBackend.createUnique(dir, baseName, ext): Promise<string>` that
   loops `store.add()` (IDB `add` throws `ConstraintError` if the key exists) incrementing
   the suffix until the empty file is created, returning the reserved path. `add` (not `put`)
   is the single serialization point, so two concurrent callers from *any* entry point get
   two distinct keys. `createUniqueInAppFile(dir)` in a new `inapp-namer.ts` wraps it.
2. **Consequence: a new untitled file now persists immediately** (empty entry written on
   create) instead of being purely in-memory until first save. This is intended — the file
   is what the tree manages, and it makes the bufferKey real at creation. Editor open flow
   for the 3 entry points opens the reserved path. (Verify no double-write/overwrite of the
   reserved empty file clobbers an immediately-typed buffer — open must `read` not `write`.)
3. **In-place rename only in T1b-4** (basename change within the same parent, files + folders).
   Cross-directory move is exclusively T1b-6 (drag). Keeps rename UI single-axis.
4. **Folder-aware guards in T1b-5**: a folder delete/rename must evaluate locked/dirty against
   **every descendant's** open pane, not just the folder path. Compute the affected path set
   = target + all open panes whose bufferKey is under `target/`.
5. **One PR** (`feat(storage): Phase 1b nested CRUD + folder move`), task-per-commit, TDD.

## Tasks

Order: T1b-2 (namer, foundation) → T1b-3 (mkdir) → T1b-4 (rename) → T1b-5 (delete) →
T1b-6 (drag move). Each: failing tests first, then impl, suite+lint+build green.

### T1b-2 — Unified `createUniqueInAppFile` namer + wire 3 entry points
- **Backend** `fs-backend-inapp.ts`: add `createUnique(dir, baseName='Untitled', ext='md'):
  Promise<string>`. Loop: candidate = `Untitled`, `Untitled-1`, `Untitled-2`…; `store.add({
  path: join(dir, name+'.'+ext), content: empty, mtime })`; on `ConstraintError` increment;
  on success return the path. Bounded retry (e.g. 10_000) → throw if exhausted.
- **lib** new `spa/src/lib/inapp-namer.ts`: `createUniqueInAppFile(dir=STORAGE_ROOT):
  Promise<string>` = `getFsBackend('inapp').createUnique(dir)`. Single namer.
- **Wire** all 3 entry points to call it (replacing local naming):
  - `storage-actions.ts:58` `createStorageFile` → `createUniqueInAppFile(targetDir)`.
  - `EditorPane.tsx:424` new buffer → `createUniqueInAppFile(STORAGE_ROOT)` then open path.
  - `EditorNewTabSection.tsx:18-52` `nextUntitledName` → replaced by `createUniqueInAppFile`.
- **Tests** (`fs-backend-inapp.test.ts` + `inapp-namer.test.ts`):
  - T2-1 `createUnique` returns `/buffer/Untitled.md` on empty dir.
  - T2-2 collision: pre-seed `/buffer/Untitled.md` → returns `/buffer/Untitled-1.md`.
  - T2-3 **#854 race**: `await Promise.all([createUniqueInAppFile(), createUniqueInAppFile()])`
    → two **distinct** paths, two entries in `list`, no shared key.
  - T2-4 reserved file is empty + appears in `listTreeUnder`.
  - T2-5 entry-point parity: storage-actions create + EditorNewTabSection both go through the
    namer (assert no `Date.now()` / local scan remains — grep-level or behavioral).
- **Dep:** none.

### T1b-3 — New Folder (mkdir) UI
- **storage-actions.ts**: `createStorageFolder(dir, t): Promise<{path}|{error}>` — unique
  folder name via a small `createUniqueDirName` (scan `list(dir)` for `New Folder`,
  `New Folder 1`…; mkdir is not an `add`-race surface like files since folders are rarely
  rapid-doubled — a scan + `mkdir` is acceptable; if review wants symmetry, route through an
  `add`-based reserve too).
- **StoragePane.tsx**: toolbar "New Folder" button → `createStorageFolder(selectedDir||ROOT)`
  → `refresh()` → auto-expand + select the new folder. `selectedDir` = selected node's dir
  (if a file is selected, its `parentOf`; if a folder, itself; else ROOT).
- **Tests** (`storage-actions.test.ts` + `StoragePane.test.tsx`):
  - T3-1 mkdir creates a folder that shows in the tree (`isDir`).
  - T3-2 new file created **under** the selected folder lands at `folder/Untitled.md`
    (accepts children — proves dir is real).
  - T3-3 folder name collision increments (`New Folder 1`).
- **Dep:** T1b-2 (uses `createUniqueInAppFile(dir)` to prove children land under it).

### T1b-4 — Rename (in-place) for files + folders, collision pre-check
- **storage-actions.ts** `renameStorageEntry(from, newName)`: already stat-guards file
  collisions. Extend: (a) compute `to = join(parentOf(from), newName)`; (b) **pre-check before
  any mutation** — `stat(to)` exists → `{kind:'exists'}` (folders: also reject if `to` has any
  `to/` descendant, but `rename`'s own scan covers it — keep the early `stat` as the UI-facing
  guard); (c) call `backend.rename(from, to)` (recursive re-key for folders via T1b-1).
- **StoragePane.tsx** `handleRenameConfirm`: works for both file and folder selection;
  surface `exists` as inline error; on ok `refresh()` + re-select renamed node by new path.
- **Tests**:
  - T4-1 file rename to a free name: old path gone, new present, content intact, mtime kept.
  - T4-2 file rename to an **existing** path: refused, **no backend mutation** (assert source
    untouched + target untouched).
  - T4-3 **folder rename moves ≥2 nested descendants** (AC-1b): `a/` with `a/b.md`, `a/c/d.md`
    → rename `a`→`z`: `z/b.md`+`z/c/d.md` present, `a/*` gone, contents intact.
  - T4-4 folder rename onto an existing folder name refused before mutation.
- **Dep:** T1b-1 (folder recursive rename).

### T1b-5 — Recursive delete with folder-aware locked/dirty guards
- **storage-actions.ts** `deleteStorageEntries(targets, t)`: today checks open panes whose
  bufferKey == a target. Extend the affected-pane computation to **descendants**: a pane is
  affected if its bufferKey === target **or** startsWith `target + '/'`. Locked-descendant →
  refuse; dirty-descendant → confirm (reuse `delete_dirty_confirm`). Then
  `backend.delete(path, true)` for folders (prefix-sweep) / `delete(path)` for files.
- **StoragePane.tsx** `handleDelete`: unchanged call site; close any open panes for the
  deleted path set after success (no resurrection — pair with `useTabStore.closePane` like 1a's
  guard) + `refresh()`.
- **Tests**:
  - T5-1 folder delete removes folder + all descendants (`list` empty under it).
  - T5-2 delete a folder whose descendant is open in a **locked** tab → refused, nothing
    deleted.
  - T5-3 delete a folder whose descendant is **dirty** → confirm prompt fires; on confirm
    deletes, on cancel no-op.
  - T5-4 file delete (1a regression) still guarded + works.
- **Dep:** none (extends 1a; independent of T1b-2/3/4).

### T1b-6 — Drag-and-drop move across folders
- **StorageTree/StorageRow**: wrap rows with dnd-kit (`useSortable`/`useDraggable` for source,
  `useDroppable` for folder rows + the root). `DndContext` in `StoragePane` with
  `PointerSensor {distance:5}` (mirror `RegionManager`). Drop a node onto a **folder** (or root):
  `to = join(targetDir, basename(from))`; reject drop onto self / own descendant / same parent
  (no-op); collision pre-check `stat(to)` → inline error; else `backend.rename(from, to)`
  (recursive for folders via T1b-1) → `refresh()`.
- **Move helper** `moveStorageEntry(from, targetDir)` in storage-actions (shares the rename
  collision + guard path; reuses T1b-5's descendant logic for moving a folder that contains
  open panes — moving re-keys, so open panes' bufferKeys must be updated like 1a rename did,
  via `renameEditorPanes`/`renameBuffer`).
- **Tests**:
  - T6-1 drag file into a folder: old path gone, new present under folder, content intact.
  - T6-2 drag folder into another folder: all descendants re-keyed (AC-1b move).
  - T6-3 drop onto self / own descendant / current parent → no-op (no mutation).
  - T6-4 drop where target name exists → refused (inline error), no mutation.
  - T6-5 moving a folder containing an open pane re-points the pane's bufferKey (no stale
    buffer / no resurrection).
- **Dep:** T1b-1 (move), T1b-4 (collision pre-check shared), T1b-5 (descendant pane logic).

## Verification per task
`cd spa && npx vitest run` (full suite green; 1a baseline 3397 + new), `pnpm run lint`,
`pnpm run build` (tsc). Codex sandbox has no network → main session runs install/test/lint/
build manually (feedback_codex_sandbox_no_install).

## AC-1b coverage map
| AC-1b clause | Task / tests |
|---|---|
| Folder rename/move re-keys all descendants (≥2 nested) | T1b-4 T4-3, T1b-6 T6-2 |
| mkdir creates folder that accepts children | T1b-3 T3-1/T3-2 |
| File rename to existing path refused before mutation | T1b-4 T4-2 |
| Folder delete removes descendants; locked refused; dirty confirms | T1b-5 T5-1/T5-2/T5-3 |
| Drag file into folder moves it (old gone/new present/intact) | T1b-6 T6-1 |
| Rapid double new-file from any of 3 entry points → no shared bufferKey | T1b-2 T2-3/T2-5 |

## Out of scope (Phase 1c)
Upload (picker + OS-file drag-drop), download/export, binary open-disposition, size cap +
quota banner. Drag-and-drop in 1b is **intra-tree move only**, not OS-file ingest.

## Follow-ups to watch (from 1a review, may resurface)
- onBufferSwitch dirty-guard semantics now moot under open-or-focus (1a follow-up a) — if a
  test touches it during rename/move, prefer removing the redundant confirm over patching.
- #625 manage-from-secondary-pane duplicate tab (pre-existing, spec §7) — do not regress.
