# Storage Phase 1b — Detailed Plan (Nested CRUD + recursive folder move)

Base: alpha.300. Worktree `storage-filemanager-1b`. Spec:
`2026-06-27-storage-filemanager-spec.md` §"Phase 1b" + AC-1b.
Parent plan outline: `2026-06-27-storage-filemanager-plan.md` §"Phase 1b".

T1b-1 (backend recursive rename/move) is **already shipped** in this worktree
(`402c05ce`, `InAppBackend.rename` re-keys every `from/`-prefixed descendant in one
IDB tx + full collision scan; 23 backend tests green). This plan covers **T1b-0, T1b-2 …
T1b-6b**.

> **Revision history**: v1 (initial) → **v2** after codex plan-review round 1
> (`task-mqwtpgw5-fl4lau`, NEEDS-REVISION) + main-session independent verification. v2
> changes: added T1b-0 folder-selection prerequisite (codex P1-1); added bulk descendant
> pane/buffer re-point helper for folder rename/move (codex P1-2); **EditorNewTabSection
> converted to eager per user decision** (codex P2-3 — spec amended below); prefix-match on
> `pane.content.filePath` not `bufferKey` (codex P2-4); folder naming uses `add`-reserve for
> symmetry (codex P3-5); T1b-6 split into pure helper / DnD wiring (codex P3-6).

## Spec amendment (this plan — supersedes spec §Phase 1b #854 wording)

The spec lists three new-file entry points sharing `createUniqueInAppFile`. Implementation
reveals they were **not symmetric**: `EditorPane` new buffer (`EditorPane.tsx:424`) and
`StoragePane` (`storage-actions.ts:58`) already **eagerly write** an empty real file; only
`EditorNewTabSection` (`EditorNewTabSection.tsx:18-70`) used a **lazy in-memory `untitled:`**
buffer (UntitledDocumentState, rename-before-save, `.txt`/`.md` choice) that persists on first
save. **Decision (user): converge all three on the eager reservation model.**
EditorNewTabSection now creates a real reserved file via `createUniqueInAppFile(dir, ext)`; the
`.txt`/`.md` buttons are preserved as the `ext` argument (both eager). The `untitled:` virtual
path is no longer produced by this entry. AC-1b's "rapid double new-file from any of the 3
entry points → no shared bufferKey" is thereby satisfiable by the single atomic namer.

## Existing groundwork (from 1a + T1b-1) — reused, not rebuilt

- `InAppBackend` (`spa/src/lib/fs-backend-inapp.ts`): `write` (auto-creates parent dirs in
  one tx, `put`), `mkdir` (blind `put`), `delete(path, recursive)` (prefix-sweep),
  **`rename(from,to)` recursive re-key** (T1b-1), `list` (direct children), `stat`.
- `storage-paths.ts`: `STORAGE_ROOT='/buffer'`, `join`, `parentOf`, `basename`,
  `relativeToRoot`, `isUnderRoot`.
- `storage-tree.ts`: `TreeNode {path,name,isDir,size,children?}`, `listTreeUnder`.
- `useStorageTree` hook: `{tree,loading,error,expanded,refresh,toggle}` — **selection lives in
  StoragePane, currently leaf-file only** (see T1b-0).
- `StoragePane.tsx` (271): toolbar + `StorageTree` + Backups placeholder; `handleNew`/
  `handleRenameConfirm`/`handleDelete`/`handleOpen`; `selected` state targets a **file** path.
- `storage-actions.ts` (183): `createStorageFile` (`:58` eager write), `renameStorageEntry`
  (stat collision guard `:87-92`, single-path re-point via `performBufferRename` `:33-48`),
  `deleteStorageEntries` (open-pane scan **exact-match** `:132`, locked refusal `:139`,
  dirty confirm `:145`, `backend.delete(path)` **non-recursive** `:180`).
- `performBufferRename` (`storage-actions.ts:33-48`): single-path pane+buffer re-point —
  `useTabStore.renameEditorPanes(source, from, to)` (`useTabStore.ts:82` — **exact-match**
  `content.filePath === oldPath`) + `useEditorStore.renameBuffer(oldKey, newKey, meta)`
  (`useEditorStore.ts:177` — single key). **No bulk/prefix variant exists** → folder
  rename/move needs T1b-4's new helper.
- `editor-buffer-key.ts`: `bufferKey(source, path)` → `inapp:/buffer/...` (NOT the bare path).
- dnd-kit is a dep (`@dnd-kit/core/sortable/utilities`); flat-reorder patterns in
  `RegionManager.tsx:63-76`, `TabBar.tsx`. **No nested-tree DnD precedent in repo.**

## Design decisions (resolve before impl — flag any reversal in review)

1. **#854 reservation is atomic at the IDB layer.** Add `InAppBackend.createUnique(dir,
   baseName, ext): Promise<string>` looping `store.add()` (IDB `add` throws `ConstraintError`
   if the key exists, unlike `put`) incrementing the suffix until the empty file is created;
   returns the reserved path. `add` is the single serialization point, so concurrent callers
   from any entry point get distinct keys. Confirmed sound + non-conflicting with existing
   `write`/`put` by codex review. `createUniqueInAppFile(dir, ext)` in `inapp-namer.ts` wraps
   it. **`ext` contract: `'md' | 'txt'` (bare, no leading dot)**; backend forms the path as
   `join(dir, name + '.' + ext)`. UI buttons map label → bare ext in their click handler (never
   pass `'.md'`) — single normalization point, avoids `Untitled..md`.
2. **Immediate persist does not clobber typed content** (codex-verified): `EditorPane` opens a
   real path via `read`→`openBuffer` with no redundant `write` on open. All three new-file
   entries now reserve eagerly (see Spec amendment).
3. **Folder selection (NEW — codex P1-1).** The 1a tree selects **leaf files only**; clicking a
   folder row toggles expand/collapse and does not select it. T1b-3/4/5/6 need a folder as the
   action target. T1b-0 adds a folder-selectable model: `selectedNode {path, isDir}`; clicking a
   folder **name** selects it, the caret still toggles expand. `targetDir` = the folder itself
   if a folder is selected, `parentOf(path)` if a file is selected, else `STORAGE_ROOT`.
4. **In-place rename only in T1b-4**; cross-directory move is exclusively T1b-6 (drag).
5. **Folder rename/move re-points every descendant open pane** (codex P1-2 / verified). New
   **pure** helper `remapPanesUnder(source, from, to)` — **does NOT touch the backend**: collect
   every open pane whose `pane.content.filePath === from || .startsWith(from + '/')` across
   **editor + image-preview + pdf-preview** kinds, then for each call
   `renameEditorPanes(source, oldPath, newPath)` + (editor only) `renameBuffer(oldKey, newKey,
   metadata)`. A single file = one iteration. The lone `backend.rename` lives **only** in the
   caller (`renameStorageEntry` / `moveStorageEntry`), called **exactly once** before
   `remapPanesUnder` — so file and folder share one path and there is no double-rename. This
   replaces the old `performBufferRename` (`storage-actions.ts:33-48`), whose embedded
   `backend.rename` is hoisted into the caller during the refactor.
6. **Descendant guard matches on `pane.content.filePath`, NOT `bufferKey`** (codex P2-4):
   `p === target || p.startsWith(target + '/')` (the trailing slash avoids `/buffer/a` matching
   `/buffer/ab`). Derive `bufferKey` from `filePath` only when looking up a dirty buffer.
7. **Folder naming uses `add`-reserve too** (codex P3-5): `InAppBackend.mkdirUnique(dir,
   baseName)` via `store.add()` on the dir key — atomic, symmetric with files, no double-click
   same-name folder race.
8. **One PR** (`feat(storage): Phase 1b nested CRUD + folder move`), task-per-commit, TDD.

## Tasks

Order & deps: **T1b-0** (folder selection, prerequisite) → T1b-2 (namer) → T1b-3 (mkdir) →
T1b-4 (rename + bulk re-point) → T1b-5 (delete) → **T1b-6a** (pure move helper) → **T1b-6b**
(DnD wiring). Each: failing tests first, then impl, suite+lint+build green.

### T1b-0 — Folder-selectable tree + target model (prerequisite, codex P1-1)
- `StoragePane.tsx`: change `selected` from a file path to `selectedNode { path, isDir }`
  (or keep path + derive isDir from tree). Clicking a folder **name** selects it; the caret /
  double-click still toggles expand (`StorageRow.tsx:107,115`). Derive `targetDir` per
  decision 3.
- `StorageRow`/`StorageTree`: render selected state for folders too; keep open-on-double-click
  for files.
- **Tests** (`StoragePane.test.tsx`, `StorageRow.test.tsx`):
  - T0-1 clicking a folder name selects it (selected style), caret toggles expand independently.
  - T0-2 `targetDir`: folder selected → folder; file selected → its parent; none → ROOT.
  - T0-3 selecting a folder does not open a tab (only files open).
- **Dep:** none. **Blocks:** T1b-3/4/5/6.

### T1b-2 — Unified eager namer `createUnique`(+ext) + wire all 3 entry points
- **Backend** `fs-backend-inapp.ts`: `createUnique(dir, baseName='Untitled', ext): Promise<
  string>`. Loop candidate `Untitled`,`Untitled-1`…; `store.add({path: join(dir,
  name+'.'+ext), content: empty, mtime})`; on `ConstraintError` increment; success → return
  path; bounded retry (10_000) → throw.
- **lib** `spa/src/lib/inapp-namer.ts`: `createUniqueInAppFile(dir, ext): Promise<string>`.
- **Wire** (all eager):
  - `storage-actions.ts:58` `createStorageFile(targetDir)` → `createUniqueInAppFile(targetDir,
    'md')`.
  - `EditorPane.tsx:424` new buffer → `createUniqueInAppFile(STORAGE_ROOT, 'md')` then open.
  - `EditorNewTabSection.tsx:18-70` → `.txt`/`.md` buttons map to bare ext and call
    `createUniqueInAppFile(STORAGE_ROOT, ext)`, open the **real** path (no `untitled:`). Remove
    only the `nextUntitledName` **producer**.
    - **MUST NOT touch the `untitled:` runtime contract** (codex R2 P2-3): `EditorPane` still
      loads/renames/saves existing `untitled:` panes (`EditorPane.tsx:161,236,330`) and
      `useTabStore` **persists** pane content (`useTabStore.ts:432`), so users may already have
      persisted `untitled:` tabs. Those must keep working. `UntitledDocumentState` /
      `renameEditorPanes` `options.untitled` / `renameEditorPanesInLayout`'s untitled branch
      stay intact. Any dead-code removal is a **separate issue** requiring a persisted-tab
      migration or explicit compat strategy — out of scope for this PR.
    - Pre-change grep to scope the producer vs consumers: `rg -n "nextUntitledName|
      UntitledDocumentState|untitledStoragePath|untitledSuggestedName|hasBeenRenamed|untitled:"
      spa/src/{components,lib,stores,types}`.
- **Tests** (`fs-backend-inapp.test.ts`, `inapp-namer.test.ts`, `EditorNewTabSection.test.tsx`):
  - T2-1 `createUnique` → `/buffer/Untitled.md` on empty dir; `.txt` ext honored.
  - T2-2 collision: seed `/buffer/Untitled.md` → `/buffer/Untitled-1.md`.
  - T2-3 **#854 race**: `Promise.all([createUniqueInAppFile(d,'md'), …])` → distinct paths,
    two `list` entries, no shared key.
  - T2-4 reserved file empty + appears in `listTreeUnder`.
  - T2-5 EditorNewTabSection `.txt`/`.md` each create a real reserved path (behavioral —
    pane content `filePath` is `/buffer/…`, **not** `untitled:`), no shared key on rapid click.
- **Dep:** none.

### T1b-3 — New Folder (mkdir) UI
- **Backend** `mkdirUnique(dir, baseName='New Folder'): Promise<string>` via `add`-reserve
  (decision 7).
- **storage-actions.ts** `createStorageFolder(targetDir)` → `mkdirUnique` → `{path}|{error}`.
- **StoragePane.tsx** toolbar "New Folder" button → `createStorageFolder(targetDir)` (T1b-0)
  → `refresh()` → auto-expand + select new folder.
- **Tests**: T3-1 folder appears in tree (`isDir`); T3-2 a new file created with `targetDir`=new
  folder lands at `folder/Untitled.md` (folder is real / accepts children); T3-3 name collision
  increments (`New Folder 1`) — concurrent double-create → two distinct folders.
- **Dep:** T1b-0, T1b-2.

### T1b-4 — In-place rename (file + folder) + bulk descendant re-point
- **storage-actions.ts** `renameStorageEntry(from, newName)` — **one uniform path for file and
  folder** (no branch, no double-rename): (1) `to = join(parentOf(from), newName)`; (2)
  pre-check `stat(to)` before any mutation → `{kind:'exists'}`; (3) **exactly one**
  `await backend.rename(from, to)` (recursive re-key via T1b-1); (4) `remapPanesUnder(source,
  from, to)`.
- **Refactor** the existing `performBufferRename` (`storage-actions.ts:33-48`) into the **pure**
  `remapPanesUnder(source, from, to)` of decision 5 — it performs only pane+buffer remap and
  **no** `backend.rename` (that call is hoisted up into step 3). For a single file this is one
  iteration carrying the language metadata as before; for a folder it iterates every descendant
  pane (`filePath === from || startsWith(from+'/')`, editor/image/pdf). This is the **only**
  remaining caller-side backend mutation point for rename.
- **StoragePane.tsx** `handleRenameConfirm`: works for file or folder selection; surface
  `exists` inline; on ok `refresh()` + re-select by new path.
- **Tests**:
  - T4-1 file rename: old gone, new present, content + mtime intact.
  - T4-2 file rename onto existing path: refused, **no mutation** (source + target untouched).
  - T4-3 **folder rename moves ≥2 nested descendants** (AC-1b): `a/b.md`+`a/c/d.md` → rename
    `a`→`z`: `z/b.md`+`z/c/d.md` present, `a/*` gone, contents intact.
  - T4-4 folder rename onto existing folder name refused before mutation.
  - T4-5 **open descendant pane re-pointed**: `a/b.md` open in an editor pane; rename `a`→`z`
    → that pane's `filePath` becomes `z/b.md`, buffer key remapped, no stale/orphan buffer.
    Repeat for an image-preview pane (`a/p.png`).
- **Dep:** T1b-0, T1b-1.

### T1b-5 — Recursive delete with folder-aware locked/dirty guards
- **storage-actions.ts** `deleteStorageEntries(targets, t)`: extend the affected-pane scan
  (`:132`) to descendants — a pane is affected if `filePath === t || startsWith(t+'/')` for
  any target `t` (decision 6). Locked descendant → refuse; dirty descendant → confirm. Close
  all affected panes + drop buffers BEFORE delete (existing G2 order). `backend.delete(path,
  true)` for folders / `delete(path)` for files.
- **Tests**:
  - T5-1 folder delete removes folder + all descendants (`list` empty under it).
  - T5-2 folder whose descendant is open in a **locked** tab → refused, nothing deleted.
  - T5-3 folder whose descendant is **dirty** → confirm fires; confirm deletes, cancel no-ops.
  - T5-4 file delete (1a regression) still guarded + works.
- **Dep:** T1b-0.

### T1b-6a — Pure move helper + guards + tests (codex P3-6, data layer)
- **storage-actions.ts** `moveStorageEntry(from, targetDir): Promise<MoveOutcome>`:
  `to = join(targetDir, basename(from))`; reject (no-op) if `targetDir === parentOf(from)`,
  `to === from`, or `targetDir === from || targetDir.startsWith(from+'/')` (into self/own
  descendant); `stat(to)` exists → `{kind:'exists'}`; else `backend.rename(from, to)` +
  `remapPanesUnder` (reuses T1b-4 helper; recursive for folders).
- **Tests** (no DnD):
  - T6-1 move file into folder: old gone, new under folder, content intact.
  - T6-2 move folder into folder: all descendants re-keyed (AC-1b move).
  - T6-3 move onto self / own descendant / current parent → no-op (no mutation).
  - T6-4 move where target name exists → `exists`, no mutation.
  - T6-5 moving a folder containing an open pane re-points it (no stale buffer / resurrection).
- **Dep:** T1b-1, T1b-4 (shares collision + `remapPanesUnder`).

### T1b-6b — Drag-and-drop wiring + UI (codex P3-6, interaction layer)
- **StoragePane/StorageTree/StorageRow**: `DndContext` (`PointerSensor {distance:5}`, mirror
  `RegionManager.tsx:63-76`); rows as drag sources (`useDraggable`/`useSortable`), folder rows
  + root as drop targets (`useDroppable`). Drag handle / drop-target highlight; preserve
  click-select + double-click-open coexistence. On drop → `moveStorageEntry(activePath,
  dropTargetDir)` (T1b-6a) → inline `exists` error / `refresh()`.
- **Tests** (UI-level, RTL + dnd-kit harness):
  - T6b-1 drag a file onto a folder → `moveStorageEntry` called with that folder; tree updates.
  - T6b-2 drop onto root moves to `STORAGE_ROOT`.
  - T6b-3 drop onto self / own descendant → no move (guard surfaces).
  - T6b-4 click still selects, double-click still opens (no DnD regression).
- **Dep:** T1b-6a.

## Verification per task
`cd spa && npx vitest run` (full suite green; 1a baseline 3397 + new), `pnpm run lint`,
`pnpm run build` (tsc). Codex sandbox has no network → main session runs install/test/lint/
build manually (feedback_codex_sandbox_no_install).

## AC-1b coverage map
| AC-1b clause | Task / tests |
|---|---|
| Folder rename/move re-keys all descendants (≥2 nested) | T1b-4 T4-3, T1b-6a T6-2 |
| mkdir creates folder that accepts children | T1b-3 T3-1/T3-2 |
| File rename to existing path refused before mutation | T1b-4 T4-2 |
| Folder delete removes descendants; locked refused; dirty confirms | T1b-5 T5-1/T5-2/T5-3 |
| Drag a file into another folder moves it (old gone/new present/intact) | T1b-6a T6-1 + T1b-6b T6b-1 |
| Rapid double new-file from any of 3 entry points → no shared bufferKey | T1b-2 T2-3/T2-5 |
| (enabler) folder is a selectable action target | T1b-0 T0-1/T0-2 |
| (enabler) folder move re-points open descendant panes | T1b-4 T4-5, T1b-6a T6-5 |

## Out of scope (Phase 1c)
Upload (picker + OS-file drag-drop), download/export, binary open-disposition, size cap +
quota banner. Drag-and-drop in 1b is **intra-tree move only**, not OS-file ingest.

## Follow-ups to watch (from 1a review)
- onBufferSwitch dirty-guard now moot under open-or-focus (1a follow-up a) — if a test touches
  it during rename/move, prefer removing the redundant confirm over patching.
- #625 manage-from-secondary-pane duplicate tab (pre-existing, spec §7) — do not regress.
- If `UntitledDocumentState`/`untitled:` becomes fully dead after T1b-2, open a cleanup
  commit/issue rather than mixing removal into the namer change.
