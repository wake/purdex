# Spec — Storage: In-App nested file manager (subsystem 1)

- **Base**: alpha.299 (`b8a9c17c`)
- **Scope**: `spa` only (subsystem 1 is pure frontend; daemon backup = subsystem 2, deferred)
- **Status**: draft → codex review
- **Memory**: [[kickoff_storage_feature]]

Upgrade the current "Manage buffers" pane (`EditorBuffersPane`) into a real nested file
manager called **"Storage"**, fixing two observed problems and adding folders, file-type
icons, upload/download. Backed entirely by the existing `InAppBackend` (IndexedDB), which
already supports unlimited nesting at the data layer.

## 1. Problems being fixed (observed)

1. **List is unreadable** (`EditorBuffersPane.tsx:404-430`): rows are bare filename text +
   a far-right `N B`. No file icon, no folders, no word count, weak affordance — it doesn't
   read as a file list (user screenshot: a lone "test.txt").
2. **Open is wrong/unreliable** (`openBufferByName`, `EditorBuffersPane.tsx:275-329`): the
   "smart-open" reuses the first clean in-app editor pane anywhere (active tab → `tabOrder`
   scan → new tab). From the Buffers pane this jumps to / overwrites *another* tab
   unpredictably ("opened in the wrong place").

Structural debt: `EditorBuffersPane` is 462 lines, hardcodes `/buffer` in 7 places, lists
only the flat `/buffer` prefix (dirs filtered out).

## 2. Current capabilities (verified, reused as-is)

- `InAppBackend` (`fs-backend-inapp.ts`): path-as-key (`keyPath: 'path'`), DB `pdx-inapp-fs`
  / store `files`; stores arbitrary `Uint8Array`. `write()` auto-creates missing parent dir
  entries; `list(prefix)` returns direct children; `mkdir` and **recursive** `delete` exist.
  **Unlimited nesting works at the data layer.**
  - ⚠️ **Gap (codex R1 P2-7)**: `rename(from, to)` moves a **single** entry only
    (`fs-backend-inapp.ts:141-154` — `put({...entry, path: to}) + delete(from)`); it does
    **not** re-key descendants. So renaming/moving a **folder** would orphan its children.
    Phase 1b MUST add recursive folder move (re-key every `from/`-prefixed entry) before any
    folder rename/move UI ships.
- Type-routed viewers exist but the **opener pipeline is NOT yet wired into the buffers
  pane** (codex R1 P1-1). Viewers are registered via the file-opener registry
  (`getDefaultOpener`/`createContent`, `register-modules/editor-module.tsx:61`,
  `file-open-bootstrap.ts:194`): `IMAGE_EXTS = png/jpg/jpeg/gif/webp/svg/ico → image-preview`,
  `PDF_EXTS = pdf → pdf-preview`, else `editor`. But `EditorBuffersPane.openBufferByName`
  hardcodes `{ kind: 'editor' }` (`EditorBuffersPane.tsx:277`) and never consults the
  registry — so today opening a png/pdf from the pane wrongly mounts a text editor. Storage
  open MUST route through the registry (see §3.5).
- Phosphor Icons (`@phosphor-icons/react` 2.1.10) ships 84 `File*` icons (FileMd, FileTs,
  FileTsx, FileJs, FileJsx, FileVue, FilePy, FileRs, FileCss, FileHtml, FilePdf, FileImage,
  FilePng, FileJpg, FileSvg, FileCsv, FileDoc, FileXls, FilePpt, FileZip, FileSql,
  FileAudio, FileVideo, …) + `Folder`/`FolderOpen`. No external icon lib needed.

## 3. Architecture

### 3.1 Page layout (two regions)
The Storage pane renders left/main + right sidebar:
- **Left/main** — In-App file tree (this spec).
- **Right sidebar** — daemon backup history/viewer/operator (subsystem 2). In this spec it
  is a **reserved placeholder** region only (collapsed or "備份（即將推出）" stub). No daemon
  work here.

### 3.2 Path model (codex R1 P1-2)
Root stays `/buffer` (no migration). Centralize into a single `STORAGE_ROOT` constant + path
helpers (`join`, `parentOf`, `basename`, `relativeToRoot`). Scope is **not just the 7
literals inside `EditorBuffersPane`** — the full set of `/buffer`-coupled callers that the
nested model touches:
| Caller | Today (flat) | Needed |
|---|---|---|
| `EditorBuffersPane` (×7) | `/buffer/${name}` basename | full-path via helpers |
| `EditorPane` new buffer (`EditorPane.tsx:399,408`) | `/buffer/Untitled-${Date.now()}.md` | shared create helper (§Phase 1b, #854) |
| `EditorNewTabSection` (`:17,:39`) | root-only name scan | tree-aware scan |
| `EditorToolbar` quick-switch (`:47`) | flat `list('/buffer')` | recursive or path-labeled list |
| `BreadcrumbPopover` (`:104`) | basename → `/buffer/${name}` | full relative path |
| `editor-language.ts` `detectLanguageSource()` / `untitledStoragePath()` (`:17`) | hardcoded `/buffer` | use `STORAGE_ROOT` helpers (codex R2-2) |

The breadcrumb **display** strip (`EditorToolbar.tsx:39`, drops leading `buffer` segment) is
fine and unchanged. What breaks under nesting is the **popover quick-switch / new-file**
basename model. **Scope decision**: Phase 1a converts the Storage pane + the
breadcrumb-popover quick-switch to full relative paths (so a nested file is visible &
switchable). EditorPane/NewTabSection new-file naming is unified in Phase 1b (§#854).

### 3.3 Open dispatch via opener registry (codex R1 P1-1, R2-1, R2-3)
Storage open MUST resolve the pane kind via the registry — `getDefaultOpener(file)` →
`createContent(source, path)` — yielding `image-preview` / `pdf-preview` / `editor` by
extension (Phase 1a). It must NOT hardcode `{kind:'editor'}` (today's bug at
`EditorBuffersPane.openBufferByName:277` AND `EditorPane.onBufferSwitch:377`).

- **Reuse the modern open pipeline (codex plan-R1 P1, supersedes R2-3 framing)**: the
  `defaultTabOpener` pattern (`file-open-bootstrap.ts:194`) already IS "open or focus" +
  workspace placement + cluster insert + `ws.activeTabId` sync:
  `getDefaultOpener(file)` → `createContent(source, file)` →
  `afterTabId = computeClusterInsertTarget(workspaceId, isFileKind)` →
  `tabId = openSingletonTab(content, { afterTabId })` → `insertTab(tabId, workspaceId,
  afterTabId)`. `openSingletonTab` (`useTabStore.ts:185`) dedupes by **exact filePath** for
  `editor`/`image-preview`/`pdf-preview` (`pane-utils.ts:13-15`, inapp has no hostId) — so an
  already-open file is **focused**, a different file gets a **new tab**, and an *unrelated*
  editor pane is **never** reused (kills the old smart-open hijack). This was earlier
  mis-framed (R2-3) as "non-singleton needed" under a literal "always new tab" reading; the
  **open-or-focus** contract the user chose IS singleton-by-path, so we reuse this pipeline.
- **`openInAppFile(path)`** reuses ONLY the **core open sequence** above (it is just five
  calls and works for `{type:'inapp'}`), NOT the daemon-shaped `createOpenFileService`
  (codex plan-R2 P1): that service requires `hostId`, scopes its cache by `hostId/cwd`, and
  its missing-file flow runs daemon session/workspace search + popup
  (`file-open/open-file.ts:11,111`) — wrong for in-app. In-app missing-file handling is
  minimal (the tree only offers existing entries; a stat-gate + silent no-op/refresh
  suffices). It must NOT hand-roll `createTab`/`addTab` without `insertTab` (that skips
  workspace placement + active sync).
- **Inherited open-or-focus edge cases (codex plan-R2 P2, documented not changed)**:
  `openSingletonTab` scans each tab's **primary pane only** — a file already open in a
  *split's secondary pane* won't dedup and may open a duplicate (accepted, inherited). Cross
  workspace: `insertTab` **rehomes** the matched tab into the current workspace
  (`workspace/store.ts:140`), it is not an in-place focus. Both are existing shared-pipeline
  semantics, consistent with `FileTreeView`.
- **All In-App open entry points route through `openInAppFile`**: the Storage tree click AND
  the breadcrumb-popover quick-switch (R2-1 — so switching to a png/pdf opens the right
  viewer, not a text editor).
- **Phase 1c download disposition**: for non-previewable binaries (docx/xlsx/zip…),
  `openInAppFile` triggers a **download** instead of mounting a pane.

### 3.4 Shared icon helper + tab-icon integration (codex R1 P2-6)
`fileIconForPath(path, { isDir, expanded })` → a Phosphor icon component (extension→icon map,
fallback `File`; dirs → `FolderOpen`/`Folder`). Used by tree rows **and** tab icons. The
real tab-icon integration point is **`getPaneIcon()` (`pane-labels.ts:58,79`, today returns
`TextAlignLeft` for all `editor` panes) → `useTabDisplay()` (`:68`) → `SortableTab` (`:40`)**
— NOT the editor module. Phase 1a makes `getPaneIcon` for an in-app `editor`/`image-preview`/
`pdf-preview` pane delegate to `fileIconForPath(content.filePath)`.

### 3.5 Full-path node identity (codex R1 P2-3)
Today selection/rows key on **basename** (`EditorBuffersPane.tsx:55,101,407`), valid only
for a flat list. The nested tree in **Phase 1a** MUST switch selection/expansion/row identity
to **full path**, otherwise Phase 1b CRUD/move would have to rewrite 1a's state model. This
is the load-bearing 1a/1b boundary.

### 3.6 File-health split
Replace the 462-line `EditorBuffersPane` with focused modules: tree container / tree-row /
CRUD action handlers / path helpers / icon helper / open-dispatch. Each independently
testable.

## 4. Phases

### Phase 1a — Tree foundation + pane rename + open-via-registry (fixes both symptoms)
- **Pane rename** (NOT file rename — file rename is 1b): i18n title "Buffers"/"Manage
  buffers" → **"Storage"** (`kind` stays `editor-buffers`; only labels/strings change).
- Two-region layout shell; right sidebar = placeholder.
- **Full-path node identity** (§3.5): selection / expansion / row keys switch from basename
  to full path. Load-bearing for 1b.
- **Nested tree** from recursive `list` (expand/collapse folders, lazy-load children).
- Each row: type icon + name + metadata (**size** always; **word count** for text files).
- Clear hover / selected / focus styling.
- **Open via the `openInAppFile` helper (§3.3)** — open-or-focus, registry-resolved kind:
  replace `openBufferByName`'s hardcoded `{kind:'editor'}` + smart-open reuse so
  png→image-preview, pdf→pdf-preview, text→editor; a new tab is inserted unless the file is
  already open (then focus it). Removes the cross-tab hijack AND the wrong-viewer bug.
- **Tab icon** via `getPaneIcon` delegating to `fileIconForPath` (§3.4).
- **Breadcrumb-popover quick-switch** becomes full-path aware (§3.2) AND routes through
  `openInAppFile` (R2-1) — replacing `EditorPane.onBufferSwitch`'s hardcoded
  `{kind:'editor'}` (`EditorPane.tsx:377`) — so switching to a nested png/pdf opens the right
  viewer, not a text editor.
- File-health split (§3.6).

**AC-1a**
- Opening a file from the tree inserts a **new tab** bound to it (assert `insertTab`, no
  existing editor pane mutated, no cross-tab `setActiveTab` hijack). Covers the 5
  smart-open-bound tests to rewrite: B2-7/8/9/15/18
  (`EditorBuffersPane.test.tsx:337,362,387,592,899`).
- **Open-or-focus** (via the reused pipeline): opening an already-open file focuses its tab
  (`openSingletonTab` matches by exact filePath) rather than duplicating; a different file
  gets a new tab placed via `insertTab` + cluster target; an unrelated editor pane is never
  reused. Cross-workspace behavior is whatever the shared pipeline already does (inherited,
  consistent with `FileTreeView`).
- Opening a `.png` mounts `image-preview`; `.pdf` mounts `pdf-preview`; `.md` mounts editor —
  via the registry, from BOTH the tree click AND the breadcrumb-popover switch (R2-1), not a
  hardcoded editor kind.
- A nested file `/buffer/a/b/x.md` appears under expandable folders `a` → `b`, is selectable
  by full path, and is visible/switchable in the breadcrumb popover.
- Each row + the opened file's tab icon render the correct Phosphor icon by extension
  (md/ts/png/pdf/unknown) via the shared helper; folders render `Folder`/`FolderOpen`.
- Text-file rows show word count; binary rows show size only.
- `EditorBuffersPane` split into ≥3 focused modules; existing 21 tests adapted & green.

### Phase 1b — Nested CRUD + recursive folder move
- **Backend: recursive folder move** (§2 gap P2-7): extend `InAppBackend.rename` (or add a
  `moveDir` helper) to re-key **every** `from/`-prefixed descendant entry in one transaction.
  Folder rename/move UI MUST NOT ship on the single-entry rename.
- **New folder (mkdir)** — currently absent; create under the selected dir.
- **Unified new-file helper** (§#854, codex P2-5): one `createUniqueInAppFile(dir, ext)` used
  by **all three** entry points (`EditorPane.tsx:408`, `EditorBuffersPane.tsx:115`,
  `EditorNewTabSection.tsx:17`), with **atomic IDB `add()` reservation** (not scan-then-write)
  as the single serialization point — so the dup-bufferKey race can't return from another
  entry.
  - **Amendment (1b plan, 2026-06-28)**: the three entries were not symmetric —
    `EditorPane`/`EditorBuffersPane` already eagerly write an empty file, but
    `EditorNewTabSection` used a **lazy in-memory `untitled:`** buffer (UntitledDocumentState,
    rename-before-save, `.txt`/`.md`). **Decision: converge all three on eager reservation.**
    EditorNewTabSection now reserves a real file via `createUniqueInAppFile(dir, ext)`; the
    `.txt`/`.md` choice is preserved as `ext`. The `untitled:` virtual path is no longer
    produced by this entry. See `2026-06-28-storage-filemanager-1b-plan.md` §"Spec amendment".
- File rename (in-place + across dirs); collision pre-check (reuse `stat` guard
  `EditorBuffersPane.tsx:144-153`).
- Recursive delete; preserve locked-tab refusal + dirty-pane confirm guards
  (`EditorBuffersPane.tsx:202-236`).
- **Move via drag-and-drop** across folders (uses the recursive move for folders).

**AC-1b**
- Renaming/moving a **folder** moves all descendants (every child path re-keyed; old paths
  gone, content intact) — explicit test with ≥2 nested descendants.
- mkdir creates a folder that appears in the tree and accepts children.
- File rename to an existing path is refused before any backend mutation.
- Folder delete removes it + all descendants; delete touching a locked tab is refused; delete
  touching a dirty pane confirms first.
- Drag a file into another folder moves it (old path gone, new present, content intact).
- Rapid double new-file from **any** of the 3 entry points does not produce two entries
  sharing a bufferKey (#854) — the shared helper is the only namer.

### Phase 1c — Upload / download
- **Upload**: file picker + drag-drop of OS files onto the tree → `File` → `ArrayBuffer` →
  `backend.write(<targetDir>/<name>, bytes)` → refresh. **Any type accepted.**
- **Open-disposition for non-previewable binaries** (§3.3): register an in-app download
  disposition so opening docx/xlsx/zip/… triggers a **download** rather than an editor mount;
  images/PDFs still route to their preview panes.
- **Download / export**: action to write a file's bytes back to the OS (anchor download in
  browser / save dialog in Electron).
- **Soft size cap** (~25 MB): warn before writing oversized files. **Quota errors**: catch
  `QuotaExceededError` (reuse the detector in `sync/snapshot-store.ts:19`) and surface via
  the pane's existing **inline error banner** (`EditorBuffersPane.tsx:59,395`), never silent.

**AC-1c**
- Uploading an image/pdf stores it and opening it renders in the image/pdf preview pane.
- Uploading a `.docx` stores it; opening it triggers a download (no garbled editor).
- Downloading a stored file yields byte-identical content.
- A file over the soft cap is rejected with a visible inline-banner warning; a simulated
  `QuotaExceededError` surfaces an inline-banner error (not a silent no-op).

## 5. Testing
TDD per phase (RTL + vitest). New/updated suites: nested `list`/tree render; icon mapping;
row metadata (size + word count); new-tab open (replacing smart-open tests); mkdir; rename
with path change (move); recursive delete with locked/dirty guards; drag-move; upload writes
bytes; download round-trip; binary-open routing; size-cap + quota error. Reuse the existing
21 `EditorBuffersPane` tests, adapted to the split modules. `pnpm run lint` + `pnpm run
build` (tsc) green each phase.

## 6. Non-goals
- **Subsystem 2** (daemon backup/restore snapshot store, fork detection) — separate spec.
  Only the right-sidebar *placeholder* region is in scope here.
- No change to the `/buffer` storage prefix on disk (no migration).
- No new in-app viewers for office docs (docx/xlsx/pptx) — store + download only.
- No change to the daemon FS `FileTreeView` (workspace/session daemon browser) — Storage is
  In-App only.

## 7. Known limitations (explicit, not silently inherited)
- **#625** (`openSingletonTab` scans only the primary pane, `useTabStore.ts:185`): opening
  Storage via `onManage()` (`EditorPane.tsx:396`) from a **secondary** pane can still spawn a
  duplicate Storage tab. This is a pre-existing, separately-deferred item
  (`docs/specs/2026-04-24-editor-restructure-pr2-spec.md:1137`). "Open file → new tab"
  removes #625 from the **file-open** path, but the **manage-from-secondary-pane** dup is out
  of scope here and remains tracked by #625. (codex R1 P2-4)
