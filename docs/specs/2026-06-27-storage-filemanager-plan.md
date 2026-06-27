# Plan — Storage In-App file manager (subsystem 1)

Spec: `2026-06-27-storage-filemanager-spec.md`. Phased 1a → 1b → 1c, each its own PR.
This plan details **Phase 1a** (TDD tasks, ordered); 1b/1c are task outlines, detailed when
their turn comes. Every task: failing test → impl → green; `pnpm run lint` + `build` per PR.

## Phase 1a — Tree foundation + pane rename + open-via-registry

Ordered so pure/unit helpers (T1-T4) land before UI (T5a→T5d, T6, T7). Dependencies (codex
plan-R2 P2): T3→T2; T5b→T5a; T5c→T5b+T2; T5d→T5b+T4; T6→exported `listTreeUnder`(T5a)+T4.
Each task is an independent TDD slice.

### T1 — `storage-paths.ts` (STORAGE_ROOT + helpers)
- New `spa/src/lib/storage-paths.ts`: `STORAGE_ROOT = '/buffer'`; `join(...segs)`,
  `parentOf(path)`, `basename(path)`, `relativeToRoot(path)`, `isUnderRoot(path)`.
- Refactor the §3.2 **production** callers to use these (no behavior change yet):
  `EditorBuffersPane` (×7), `editor-language.ts` `detectLanguageSource`/`untitledStoragePath`
  (`:17`), `EditorToolbar` (`:47`), `BreadcrumbPopover` (`:104`), `EditorPane` new-buffer
  (`:408`), `EditorNewTabSection` (`:33`).
- **Tests**: unit tests for each helper (root, nested, trailing slash, edge cases). Existing
  suites for touched files stay green (pure refactor).
- **AC** (codex plan-R1 P2-1, scoped to production code): no `'/buffer'` literal remains in
  the listed **production** runtime callers — each constructs paths via `STORAGE_ROOT`/helpers.
  Tests, locale JSON (`locales/*.json`), comments, and `fs-backend-inapp.test.ts` fixtures
  legitimately keep `/buffer` and are out of scope.

### T2 — `file-icon.ts` (`fileIconForPath`) — returns an icon NAME (codex plan-R2 P2)
- New `spa/src/lib/file-icon.ts`: `fileIconForPath(path, { isDir?, expanded? })` → an icon
  **name string** (NOT a component), matching the existing `getPaneIcon`→`ICON_MAP` pattern
  (`pane-labels.ts:58`, `useTabDisplay.ts:68`). Extension→name map (md→'FileMd', ts→'FileTs',
  tsx→'FileTsx', js→'FileJs', jsx→'FileJsx', vue→'FileVue', py→'FilePy', rs→'FileRs',
  css→'FileCss', html→'FileHtml', json→'FileCode', csv→'FileCsv', pdf→'FilePdf', png→'FilePng',
  jpg/jpeg→'FileJpg', svg→'FileSvg', gif/webp/ico→'FileImage', doc/docx→'FileDoc',
  xls/xlsx→'FileXls', ppt/pptx→'FilePpt', zip→'FileZip', sql→'FileSql', …); dirs →
  `expanded ? 'FolderOpen' : 'Folder'`; unknown → 'File'.
- **Extend the shared `ICON_MAP`** (name→component, `useTabDisplay.ts`) with the File*/Folder
  components so both tabs and `StorageRow` resolve names the same way.
- **Tests**: mapping table (representative exts + unknown fallback + dir open/closed); each
  returned name exists in `ICON_MAP`.

### T3 — `getPaneIcon` delegates to `fileIconForPath` (depends on T2)
- `pane-labels.ts:79`: for `editor`/`image-preview`/`pdf-preview` panes with an `inapp`
  source, return `fileIconForPath(content.filePath)` (a name) instead of the blanket
  `'TextAlignLeft'`. Non-inapp / other kinds unchanged.
- **Tests**: `getPaneIcon` returns the md/png/pdf icon name for inapp panes; unchanged for
  daemon editor + non-editor kinds. Verify the open tab's icon via `useTabDisplay`/
  `SortableTab` path.

### T4 — `openInAppFile` reusing the CORE open sequence (codex plan-R1 P1, R2 P1)
- Reuse only the **core sequence** (NOT `createOpenFileService` — it is daemon-shaped:
  hostId-required, daemon session/workspace missing-file search + popup,
  `file-open/open-file.ts:11,111`). `openInAppFile(path)` does, against `{type:'inapp'}`:
  `getDefaultOpener(file)` → `createContent(source, file)` →
  `computeClusterInsertTarget(wsId, isFileKind)` → `openSingletonTab(content, {afterTabId})`
  → `insertTab(tabId, wsId, afterTabId)`. This gives registry dispatch + open-or-focus
  (`openSingletonTab` matches exact filePath, `pane-utils.ts:13-15`) + workspace placement +
  cluster insert + `ws.activeTabId` sync — **without** the daemon popup flow. Missing-file
  handling is minimal (stat-gate; the tree only offers existing entries). 1c adds the
  non-previewable-binary download disposition; 1a covers image/pdf/editor.
- **Impl notes (codex plan-R3, non-blocking)**: (1) `getDefaultOpener` needs a full
  `FileInfo` (`name/extension/isDirectory/size`, `types/fs.ts:9`) — build it from the tree
  node's known data or a minimal path parse. (2) `computeClusterInsertTarget`/`insertTab` are
  workspace-scoped — pass the source workspace id from the pane/popover context.
- **Tests**: opens a new tab for md/png/pdf with the registry-dispatched kind
  (image-preview/pdf-preview/editor); focuses the existing tab when the file is already open
  in a primary pane (asserts `openSingletonTab` reuse, no duplicate); never reuses an
  unrelated editor pane; new tab placed via `insertTab` (`ws.activeTabId` updated).

> **CRUD scope in 1a (codex plan-R1 P2-3, plan-R2 P1)**: 1a renders nested rows, so it MUST
> handle **leaf-file** rename/delete on **full paths** (not the old basename model) — moving
> CRUD verbatim would break rename/delete on nested selections. So 1a: (a) re-homes the
> existing new/rename/delete actions into `storage-actions.ts` AND makes them **full-path
> aware for leaf files** (rename within the same dir, delete by full path); (b) keeps the
> dirty/locked guards. **Deferred to 1b**: mkdir, folder rename/move, drag-move, recursive
> folder ops, the unified `createUniqueInAppFile` namer. New-file in 1a may stay flat
> (root-level) until 1b's namer; this is acceptable since folders don't exist until mkdir (1b).

### T5a — `useStorageTree` hook + `listTreeUnder` (full recursive)
- `listTreeUnder(root)` **full recursive** enumeration (one pass — the in-app store is small,
  no lazy loading; simpler hook boundary than per-dir lazy). Exported, shared by T5b tree +
  T6 popover. `useStorageTree` state: full-path node identity, expand/collapse (UI-only, not
  data-fetch), refresh.
- **Tests**: nested `/buffer/a/b/x.md` enumerates under folders `a`→`b`; expand/collapse;
  full-path identity (two same-basename files in different dirs are distinct nodes).

### T5b — `EditorBuffersPane` split + leaf CRUD full-path aware (depends on T5a)
- Split the 462-line pane into `StoragePane` (shell + two-region layout, right placeholder),
  `StorageTree` (consumes T5a), `StorageRow`, `storage-actions.ts`. Move new/rename/delete in
  and make rename/delete operate on the **selected node's full path** (T5a identity); keep
  dirty/locked guards (`EditorBuffersPane.tsx:202-236`).
- **Tests**: the ~16 non-smart-open existing tests adapted & green; PLUS rename/delete of a
  **nested** leaf file works on its full path (old basename-only behavior would fail this).

### T5c — `StorageRow` metadata (icon + size + word count)
- Row renders `fileIconForPath` (T2) + name + size (always) + word count (text files: decode
  + split; binary: omit).
- **Tests**: correct icon per ext + folder; word count for text, size-only for binary.

### T5d — Open via `openInAppFile` + rewrite smart-open tests
- Tree click routes through `openInAppFile` (T4); remove `openBufferByName` smart-open.
- **Tests**: **rewrite the 5 smart-open tests** B2-7/8/9/15/18
  (`EditorBuffersPane.test.tsx:337,362,387,592,899`) to the new-tab / open-or-focus / no-hijack
  contract; png→image-preview, pdf→pdf-preview, md→editor.

### T6 — Breadcrumb popover full-path + routes through `openInAppFile`
- **Depends on T5a's `listTreeUnder`** (codex plan-R1 P2-4): the popover source is today a
  flat `backend.list('/buffer')` (`EditorToolbar.tsx:45`); to show nested files it must use
  the recursive helper, not just relabel.
- Popover lists full relative paths; selecting routes through `openInAppFile` (T4), replacing
  `EditorPane.onBufferSwitch`'s hardcoded `{kind:'editor'}` (`EditorPane.tsx:377`).
- **Tests**: nested files appear in the popover; switching to a nested `.png`/`.pdf` via
  popover opens image/pdf preview (not text editor).

### T7 — Pane rename Buffers → "Storage" + i18n
- Rename i18n strings/title `editor.buffers.*` display label → "Storage" (`kind` stays
  `editor-buffers`). Update `ManageBuffersNewTabCard` / new-tab card label.
- **Tests**: pane title renders "Storage"; new-tab card opens the pane.

**Phase 1a PR exit**: AC-1a (spec §4) all green; full `vitest` (no new failures beyond the
4 known pre-existing baseline — already fixed in #674, so suite should be fully green);
lint + build pass. 2-round codex PR review.

## Phase 1b — Nested CRUD + recursive folder move (outline)
- T1b-1 **Backend recursive move**: extend `InAppBackend.rename`/add `moveDir` to re-key all
  `from/`-prefixed descendants in one tx. Tests: move folder with ≥2 descendants.
- T1b-2 **`createUniqueInAppFile(dir)`** shared namer; wire all 3 entry points
  (`EditorPane:408`, `StoragePane`, `EditorNewTabSection:17`). Tests: #854 race from each.
- T1b-3 mkdir UI; T1b-4 file rename (collision pre-check); T1b-5 recursive delete (locked/dirty
  guards preserved); T1b-6 drag-and-drop move (uses recursive move).
- Detailed plan written before 1b impl.

## Phase 1c — Upload / download (outline)
- T1c-1 upload (picker + drag-drop → `write`); T1c-2 download/export; T1c-3 download
  disposition in `openInAppFile` for non-previewable binaries; T1c-4 soft size cap + quota
  error (reuse `snapshot-store.ts:19` detector → inline banner).
- Detailed plan written before 1c impl.

## Execution
- Dev tasks (test + impl) delegated to subagents per task (feedback_subagent_tdd_priority);
  main session does integration + review.
- Subagent Bash calls prefixed `cd <worktree> &&`; Edit/Write use the worktree-prefixed
  absolute path (feedback_worktree_absolute_path / feedback_subagent_cwd_enforcement).
