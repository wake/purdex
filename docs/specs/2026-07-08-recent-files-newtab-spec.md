# Spec — Recent Files in Editor New-Tab Section

Date: 2026-07-08
Branch: `worktree-recent-files-newtab`
Status: Draft (pending codex spec review + user approval)

## 1. Motivation

The New-Tab page's Editor section currently offers only two actions: **New File**
and **New Markdown** (`spa/src/components/editor/EditorNewTabSection.tsx`). There
is no way to quickly re-open a file you were recently working on — you must
re-navigate the file tree or re-type a terminal path. This spec adds a
**Recently opened** list directly beneath those two buttons, covering local,
remote (daemon), and in-app files.

## 2. Goals / Non-Goals

### Goals
- Persist a global, de-duplicated, most-recent-first list of opened files
  (cap 50), surviving reloads.
- Render the list in the Editor new-tab section below the New File buttons.
- Support a fixed type filter (All / Text / Image / PDF).
- Show a host badge for remote (daemon) files.
- Clicking a row re-opens the file through the **same** open path it originally
  used, so a missing remote file surfaces the existing file-not-found popup.

### Non-Goals (YAGNI for v1)
- Per-host recent lists (global single list only).
- Manual remove / pin / clear UI (may add later; store exposes `clear` for tests
  and future use only).
- Recording directory opens, settings/dashboard/browser/terminal tabs (file
  panes only).
- Pruning stale entries on failed open (entry simply remains; a later open
  attempt re-surfaces the popup / error).
- Recording brand-new unsaved scratch buffers (see §4.2).

## 3. Data Model — `useRecentFilesStore`

New store `spa/src/stores/useRecentFilesStore.ts`, modeled on
`useBrowserHistoryStore` (zustand + `persist` via `purdexStorage`).

```ts
import type { FileSource } from '../types/fs'

export type RecentFileKind = 'editor' | 'image-preview' | 'pdf-preview'

export interface RecentFileEntry {
  source: FileSource           // { type:'daemon', hostId } | { type:'local' } | { type:'inapp' }
  path: string                 // full path (absolute for daemon/local; storage path for inapp)
  name: string                 // basename, precomputed for render
  kind: RecentFileKind         // drives the type filter + icon
  openedAt: number             // ms epoch; ordering tiebreak + potential future display
}

interface RecentFilesState {
  files: RecentFileEntry[]
  addRecent: (entry: RecentFileEntry) => void
  clear: () => void            // test + future-use only; no v1 UI
}
```

- **Dedup key**: `recentKey(source, path)` where
  `sourceKey = source.type === 'daemon' ? 'daemon:' + source.hostId : source.type`
  joined with the path via a NUL separator. Two files with the same path on
  different hosts are distinct entries.
- **addRecent** semantics (mirrors `useBrowserHistoryStore.addUrl`): drop any
  existing entry with the same key, unshift the new entry, `slice(0, 50)`.
- `MAX_RECENT = 50`. New `STORAGE_KEYS.RECENT_FILES` constant in
  `spa/src/lib/storage.ts`.

## 4. Recording Hooks

A single helper `recordRecentFile(content: PaneContent): void`
(`spa/src/lib/recent-files/record-recent-file.ts`):
- Returns early unless `content.kind` ∈ {`editor`,`image-preview`,`pdf-preview`}.
- For `editor`, skip when `content.untitled` is set AND the file has never been
  saved — but see §4.2: we simply do not call the recorder on new-file creation,
  so the untitled guard is defense-in-depth.
- Derives `name` via `path.split('/').pop()`, builds the `RecentFileEntry`, and
  calls `useRecentFilesStore.getState().addRecent(entry)` with
  `openedAt = Date.now()`.

### 4.1 Existing-file opens (record on open)
- `defaultTabOpener` (`spa/src/lib/register-modules/file-open-bootstrap.ts:194`)
  — the shared chokepoint for FileTree, terminal-link, and popup opens
  (daemon + local). Call `recordRecentFile(content)` right after
  `opener.createContent(source, file)`.
- `openInAppFile` (`spa/src/lib/open-in-app-file.ts`) — existing in-app storage
  files. Call `recordRecentFile(content)` after the opener resolves a
  `PaneContent` (i.e. the file-pane branch, not the download branch).

> Deliberately NOT hooked at `useTabStore.openSingletonTab`, because that is also
> used by session/layout restore and non-file tabs — hooking it would pollute the
> list with tabs the user never explicitly opened this session.

### 4.2 New files (record on save, not on creation)
Per product decision: a file created via **New File / New Markdown** must NOT
appear in Recent until it is **saved**.
- `EditorNewTabSection.createFile` (which reserves `/buffer/Untitled[-N].<ext>`
  and opens it) does **not** call the recorder.
- Recording for new files happens on save: in `EditorPane.tsx`'s save handler
  (`handleSave`), after a successful save, call `recordRecentFile(paneContent)`.
  This also refreshes an existing file's recency when re-saved (dedup moves it to
  the front), which is desirable.

Net effect: existing files enter Recent on open; freshly-created scratch files
enter Recent only once saved.

## 5. UI — `EditorNewTabSection`

Extend the existing component; keep the two buttons unchanged at the top.

### 5.1 Layout
```
[ New File ]  [ New Markdown ]

Recently opened                         ← only when list non-empty
[全部] [文字] [圖片] [PDF]               ← fixed filter chips
┌─────────────────────────────────────┐
│ <icon> morphy.pre-edit.SOUL.md  [mlab]│ ← host badge only for daemon
│        docs/souls/…                    │ ← dim, truncated path
│ <icon> notes.md                        │
└─────────────────────────────────────┘
```

- Section hidden entirely when `files` is empty (no empty-state text).
- **Fixed chips**: `全部 / 文字 / 圖片 / PDF` mapping to
  `all / editor / image-preview / pdf-preview`. Selected chip filters the list;
  default `all`. Chip selection is component-local state (not persisted).
- **Row**: kind/extension icon (reuse `fileIconForPath` / `getPaneIcon` logic) +
  basename (single line, `truncate`, `title={path}`) + dim relative-ish path +
  host badge for `source.type === 'daemon'`.
- **Host badge**: resolve display name from the host/workspace store by
  `source.hostId`; fall back to the raw hostId when the host is unknown/removed.

### 5.2 Click → re-open
Row click dispatches by source type through the ORIGINAL open path:
- `daemon`: build `FileInfo` + `OpenFileContext` (hostId = `source.hostId`,
  sourceWorkspaceId = active workspace, cwd = file dirname, sessionCode =
  undefined) and call `tryOpenFileForFileTree`
  (stat-gated; missing → existing file-not-found popup). This satisfies the
  "attempt open, fail → popup" requirement for remote files.
- `local`: open directly via `defaultTabOpener`-equivalent (local files do not
  use the daemon stat/popup service). If the file has since vanished the editor
  surfaces its own load error.
- `inapp`: call `openInAppFile(path, activeWorkspaceId)` (already stat-gated;
  returns undefined and no-ops when the storage entry is gone).

> The component receives the open dispatchers via props/registry rather than
> importing bootstrap singletons directly, to keep it testable (see §7).

### 5.3 i18n
New keys under `editor.recent.*`:
`title`, `filter.all`, `filter.text`, `filter.image`, `filter.pdf`.
Add to every locale file the project ships.

## 6. Edge Cases
- Same basename, different source/host → distinct rows (dedup key includes
  source).
- List overflow → cap 50, oldest dropped.
- Removed host: badge shows raw hostId; click attempts open and surfaces the
  real (non-"not found") error per `isNotFoundError` classification.
- Filter yields empty subset → show the chips but an empty list area (no crash);
  acceptable, chips make the reason obvious.
- Persist hydration: read `files` after hydration like other persisted stores;
  the new-tab page already gates on hydration for its layout store — the recent
  list simply renders empty until the store hydrates.

## 7. Testing (TDD)
- `useRecentFilesStore.test.ts`: dedup by key, unshift-to-front on re-add,
  cap 50, distinct entries for same path across hosts, `clear`.
- `record-recent-file.test.ts`: records editor/image/pdf; ignores non-file
  kinds; derives basename; sets kind; dedups via store.
- `EditorNewTabSection.test.tsx`: renders Recently-opened only when non-empty;
  fixed chips render; chip filters rows by kind; host badge shows for daemon and
  not for local/inapp; row click invokes the injected open dispatcher with the
  right args; buttons still create files as before.

## 8. Files Touched
- New: `spa/src/stores/useRecentFilesStore.ts` (+ test)
- New: `spa/src/lib/recent-files/record-recent-file.ts` (+ test)
- Edit: `spa/src/lib/storage.ts` (STORAGE_KEYS.RECENT_FILES)
- Edit: `spa/src/lib/register-modules/file-open-bootstrap.ts` (record on open)
- Edit: `spa/src/lib/open-in-app-file.ts` (record on inapp open)
- Edit: `spa/src/components/editor/EditorPane.tsx` (record on save)
- Edit: `spa/src/components/editor/EditorNewTabSection.tsx` (+ test) — list UI
- Edit: locale files — `editor.recent.*` keys

## 9. Phasing (for the plan)
- **Phase A** — Store + recorder + wiring at the three record points, no UI.
  Verifiable via unit tests (list populates on open/save).
- **Phase B** — `EditorNewTabSection` list UI: rows, fixed chips, host badge,
  click-to-open dispatch, i18n.

Two phases keep each PR-review-sized; A is pure logic/state, B is presentational
+ interaction.
