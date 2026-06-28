# Storage Phase 1c — Detailed Plan (Upload / download / binary-disposition / quota)

Base: alpha.301 (1a+1b shipped). Worktree `storage-filemanager-1c`. Spec:
`2026-06-27-storage-filemanager-spec.md` §"Phase 1c" + AC-1c. Parent plan outline:
`2026-06-27-storage-filemanager-plan.md` §"Phase 1c".

Pure front-end (IndexedDB `InAppBackend`), no daemon/Electron-IPC changes — OS download uses
the browser anchor pattern, which works inside the Electron WebContents too.

## Existing groundwork (reused, not rebuilt)

- **Quota detector**: `sync/snapshot-store.ts:19-27` `isQuotaError(err)` (DOMException name
  `QuotaExceededError` + legacy codes 22/1014). Currently **private → must be exported** (or
  lifted to a shared util) for reuse.
- **Backend**: `fs-backend-inapp.ts` `write(path, content: Uint8Array)` (no size check; quota
  surfaces at `tx.done`), `read(path): Uint8Array` (ready for download), `stat`.
- **Open routing**: `open-in-app-file.ts:45-82` `openInAppFile(path, wsId)` → `getDefaultOpener`
  → `createContent` → insert tab. `register-modules/editor-module.tsx:18-90`:
  `IMAGE_EXTS`/`PDF_EXTS`/`BINARY_EXTS(=IMAGE∪PDF)`; openers `image-preview`, `pdf-viewer`,
  `monaco-editor` (matches non-dir **and not BINARY_EXTS** → today a `.docx` still matches
  monaco and mounts as garbled text). `file-opener-registry.ts:55-58` `getDefaultOpener`.
- **Download pattern**: `settings/SyncSection.tsx:43-50` `triggerDownload(blob, filename)`
  (`createObjectURL` + `a.download` + `revokeObjectURL`); file-picker pattern (input ref +
  `.click()` + `file.arrayBuffer()`/`.text()`), `:75,:175-187`.
- **DnD (1b)**: `StoragePane.tsx` `DndContext` + `PointerSensor{distance:5}`,
  `StorageRegionDropZone` (root `useDroppable`, `data-testid="storage-tree-region"`),
  `StorageRow` `useDraggable`+`useDroppable`. dnd-kit is **pointer-event** based.
- **Inline error banner**: `StoragePane.tsx:102` `actionError` state, `:348` render,
  `setActionError(msg)` / `setActionError(null)`.

## Design decisions

1. **Native OS-file drop coexists with dnd-kit, distinguished by `dataTransfer.files`.**
   dnd-kit uses pointer events; OS-file drop uses HTML5 `dragover`/`drop` carrying
   `DataTransfer.files`. Add native `onDragOver`(`preventDefault` to allow drop)/`onDrop` to the
   tree region. In `onDrop`, **only act when `e.dataTransfer.files.length > 0`** (an internal
   node drag has no files) → ingest those files; otherwise ignore (let dnd-kit handle it). No
   conflict because the two systems never both carry the same payload.
2. **Binary open-disposition via an explicit `DOWNLOAD_EXTS` set, not a mounted pane.** A
   download is a side-effect (no tab), so it does **not** fit `FileOpener.createContent`
   (which returns `PaneContent`). `openInAppFile` gains a pre-dispatch branch: if the extension
   is in `DOWNLOAD_EXTS` (office/archive/binary: `doc,docx,xls,xlsx,ppt,pptx,zip,rar,7z,tar,gz,
   bin,exe,dmg,…`), **read bytes → `triggerDownload` → return undefined** (no tab). Images/PDF
   still preview; text/code still monaco. (This also fixes the current `.docx`→monaco-garbled
   path.) Keep the set in `editor-module.tsx` next to IMAGE/PDF for one source of truth.
3. **Download is single-file only.** No folder→zip in 1c. Toolbar `Download` enabled iff
   `singleSelected && !selectedNode.isDir`. Byte-identical: `read` → `Blob([bytes])` →
   `triggerDownload(blob, basename)`.
4. **Upload never overwrites.** On name collision the uploaded basename is suffixed
   (`name (1).ext`, `name (2).ext`) via a `stat`-loop unique-path helper. (Upload is
   user-initiated, not the rapid-double-click vector #854 addressed; a `stat`-loop is enough —
   no atomic `add` reservation required.) **Any file type accepted.**
5. **Soft size cap ~25 MB checked before write.** `SOFT_MAX_UPLOAD_BYTES = 25 * 1024 * 1024`.
   A file over the cap is rejected with an inline-banner **warning** before any write. On the
   write itself, `QuotaExceededError` is caught via the exported `isQuotaError` and surfaced as
   an inline-banner **error** (never silent).
6. **Shared `triggerDownload` util.** Lift `triggerDownload` out of `SyncSection.tsx` into
   `spa/src/lib/download-file.ts` (single impl), consumed by T1c-2 download + T1c-3 disposition;
   update SyncSection to import it (behavior unchanged).
7. **One PR** (`feat(storage): Phase 1c upload/download/binary-disposition/quota`),
   task-per-commit, TDD.

## Tasks

Order & deps: **T1c-2** (download + shared util) → **T1c-3** (open-disposition, reuses util) →
**T1c-1** (upload) → **T1c-4** (size cap + quota guards on upload). Each: failing tests first,
then impl, full suite + lint + build green.

### T1c-2 — Download / export a stored file + shared `triggerDownload` util
- **New** `spa/src/lib/download-file.ts`: `triggerDownload(blob, filename)` (lifted from
  SyncSection; `createObjectURL`+`a.download`+`revokeObjectURL`). Update `SyncSection.tsx` to
  import it (no behavior change).
- **storage-actions.ts** `downloadStorageFile(path): Promise<{ok}|{error}>`: `backend.read(path)`
  → `new Blob([bytes])` → `triggerDownload(blob, basename(path))`.
- **StoragePane.tsx**: toolbar `Download` button, enabled `singleSelected && !selectedNode?.isDir`;
  errors via `setActionError`.
- **Tests**: T2-1 download yields byte-identical content (assert `triggerDownload` called with a
  Blob whose bytes equal the stored file; mock the anchor/URL); T2-2 download disabled/guarded
  for a folder selection; T2-3 SyncSection still exports via the shared util (regression).
- **Dep:** none.

### T1c-3 — Binary open-disposition (non-previewable → download, not editor)
- **editor-module.tsx**: add `DOWNLOAD_EXTS` (office/archive/binary set, decision 2) next to
  IMAGE/PDF.
- **open-in-app-file.ts**: before opener dispatch, if `extension ∈ DOWNLOAD_EXTS` →
  `backend.read(path)` → `triggerDownload(Blob, name)` → `return undefined` (no tab). Images/PDF
  → preview pane (unchanged); text/code → monaco (unchanged).
- **Tests**: T3-1 opening a `.docx` triggers download + opens **no** tab; T3-2 opening a `.png`
  still mounts the image-preview pane; T3-3 opening a `.md` still mounts monaco; T3-4 `.zip`
  triggers download.
- **Dep:** T1c-2 (shared `triggerDownload`).

### T1c-1 — Upload (file picker + OS-file drag-drop)
- **storage-actions.ts** `uploadFile(targetDir, file: File): Promise<{path}|{error}|{tooLarge}>`:
  size-cap check (decision 5; returns `tooLarge` with the cap) → compute a non-colliding path
  via a `stat`-loop unique-namer on `file.name` (decision 4) → `file.arrayBuffer()` →
  `new Uint8Array(buf)` → `backend.write(path, bytes)` (quota caught here, decision 5) →
  `{path}`. **Any type accepted.** Support multiple files (loop, aggregate first error).
- **StoragePane.tsx**:
  - Toolbar `Upload` button → hidden `<input type=file multiple>` ref + `.click()` → on change
    upload each into `targetDir` (T1b-0) → `refresh()`; clear the input value after.
  - Native OS-file drop on the tree region (decision 1): `onDragOver` preventDefault,
    `onDrop` guarded by `dataTransfer.files.length > 0` → upload each into `targetDir` →
    `refresh()`. Must not interfere with dnd-kit node move.
- **Tests**: T1-1 upload an image stores it and it appears in the tree; opening it renders in the
  image-preview pane (AC-1c #1); T1-2 upload a `.docx` stores it; opening triggers download
  (ties to T1c-3) (AC-1c #2); T1-3 name collision → suffixed (`name (1).ext`), original intact;
  T1-4 native `drop` with `dataTransfer.files` ingests; a `drop` with **no** files is ignored
  (dnd-kit path untouched); T1-5 multiple-file upload writes all.
- **Dep:** T1b-0 (`targetDir`), T1c-2 (for the T1-2 open→download assertion path).

### T1c-4 — Soft size cap + quota error banner
- **lib**: export `isQuotaError` from `sync/snapshot-store.ts` (or lift to
  `spa/src/lib/quota.ts` and re-import in snapshot-store to avoid a second copy — prefer the
  lift). Add `SOFT_MAX_UPLOAD_BYTES` constant (storage-paths.ts or storage-actions.ts).
- **storage-actions.ts** `uploadFile`: enforce the cap before write (→ `{tooLarge, cap}`); wrap
  `backend.write` so a thrown `QuotaExceededError` (via `isQuotaError`) returns `{error}` with a
  quota message (never silent).
- **StoragePane.tsx**: map `tooLarge` → inline-banner **warning** (file name + cap); `error`
  (quota) → inline-banner **error**. i18n keys (EN + zh-TW).
- **Tests**: T4-1 a file over the cap is rejected with a visible inline-banner warning, **no
  write** (AC-1c #4a); T4-2 a simulated `QuotaExceededError` from `write` surfaces an
  inline-banner error, not a silent no-op (AC-1c #4b); T4-3 `isQuotaError` unit cases
  (DOMException name + codes 22/1014 + negative).
- **Dep:** T1c-1 (guards layer onto its upload path).

## Verification per task
`cd spa && npx vitest run` (full suite green; alpha.301 baseline + new), `pnpm run lint`,
`pnpm run build` (tsc + vite — run build every task; T1b-5 once shipped a tsc-only break that
vitest alone missed). Codex sandbox has no network → main session runs install/test/lint/build.

## AC-1c coverage map
| AC-1c clause | Task / tests |
|---|---|
| Upload image/pdf stores it; opening renders in preview pane | T1c-1 T1-1 |
| Upload `.docx` stores it; opening triggers download (no garbled editor) | T1c-1 T1-2 + T1c-3 T3-1 |
| Downloading a stored file yields byte-identical content | T1c-2 T2-1 |
| Over-cap file rejected with visible warning; simulated quota surfaces inline error | T1c-4 T4-1/T4-2 |

## Out of scope
Folder→zip download, Electron native save dialog (anchor download suffices), content-addressed
chunking (Sync P5), subsystem 2 (daemon backup/restore). Upload is OS-file ingest; intra-tree
node move is 1b.

## Follow-ups to watch
- 1b deferred file-health issues #872-#875 (storage-actions/StoragePane/StorageRow/test splits)
  will grow with 1c's additions — note in PR if a new method materially worsens them, but do
  **not** fold the splits into this PR.
