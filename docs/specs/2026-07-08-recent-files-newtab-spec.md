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
- **Minimal by design**: the entry deliberately does NOT store the original
  `OpenFileContext` (cwd / workspace / session). Re-open is best-effort (§5.2);
  persisting per-entry open context was considered and rejected as YAGNI.
- `name` is precomputed via `path.split('/').pop()` (POSIX basename). This
  matches the existing repo-wide assumption (`EditorPane.tsx:44`,
  `PdfPreviewPane.tsx:43`); not a new risk, noted for the record.

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
There are **three** user-initiated open paths to hook (codex spec review):
- `defaultTabOpener` (`spa/src/lib/register-modules/file-open-bootstrap.ts:194`)
  — the shared chokepoint for FileTree, terminal-link, and terminal-link's
  direct-buffer fallback (`openFileAsBufferDirect` re-calls it). Daemon + local.
  Call `recordRecentFile(content)` right after `opener.createContent(source, file)`.
- **Popup `onOpenPath`** (`file-open-bootstrap.ts:154`) — when the missing-file
  popup offers candidate paths and the user picks one, the open is done inline
  (`createContent` + `openSingletonTab` + `insertTab`), NOT through
  `defaultTabOpener`. This is a genuine 4th open path; call
  `recordRecentFile(content)` there too (right after its `createContent`).
- `openInAppFile` (`spa/src/lib/open-in-app-file.ts`) — existing in-app storage
  files (StoragePane + editor buffer switch both route here; image/pdf dispatch
  through the same opener registry, not a separate path). Call
  `recordRecentFile(content)` after the opener resolves a `PaneContent` (the
  file-pane branch, not the download branch).

> Deliberately NOT hooked at `useTabStore.openSingletonTab`, because that is also
> used by session/layout restore and non-file tabs — hooking it would pollute the
> list with tabs the user never explicitly opened this session.

### 4.2 New files (record on save, not on creation)
Per product decision: a file created via **New File / New Markdown** must NOT
appear in Recent until it is **saved**.

Reality check (codex plan review): `EditorNewTabSection.createFile` eagerly
reserves a **real** `/buffer/Untitled[-N].<ext>` file via `createUniqueInAppFile`
and opens `{ kind:'editor', source, filePath }` **with no `untitled` metadata**.
Such a file therefore does NOT flow through `saveUntitledBuffer` — that path only
runs when `buf.untitled` is set. It persists through `handleSave`'s **normal-save
branch** instead. So:
- `createFile` does **not** call the recorder (→ "opening doesn't count").
- **Two save hook points in `EditorPane.tsx`, each right after its `markSaved`:**
  1. `handleSave` normal-save branch (`EditorPane.tsx:310`) — covers the eager
     New File's first real save **and** any re-save of an existing named file.
     Record `{ kind:'editor', source, filePath }` (both in scope).
  2. `saveUntitledBuffer` (`EditorPane.tsx:283`, after its `markSaved(nextKey…)`)
     — covers the legacy `untitled:` first-save, keyed by the post-rename
     `nextPath` (at that point `filePath` is still the stale Untitled path).
- Recording on every save is harmless: an existing file already recorded on open
  is simply refreshed to the front (dedup). We do NOT try to distinguish an
  eager-new-file's first save from an existing save — unnecessary and both should
  record.

Net effect: existing files enter Recent on open (§4.1); a New-File-created file
enters Recent only once the user actually saves it (`handleSave` write), keyed by
its real path.

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
- **Row**: kind-based icon (`FileText` for editor, `Image` for image-preview,
  `FilePdf` for pdf-preview — extension-aware `fileIconForPath` is a nice-to-have,
  not required) + basename (single line, `truncate`, `title={path}`) + dim path +
  host badge for `source.type === 'daemon'`.
- **Host badge**: resolve display name from the host/workspace store by
  `source.hostId`; fall back to the raw hostId when the host is unknown/removed.

### 5.2 Click → in-place open, toast on failure
The recent entry stores only `source/path/name/kind` (§3) — it does **not**
persist an `OpenFileContext`. Re-open is **best-effort and in-place**: a row
click reuses the section's existing `onSelect(content)` callback, which replaces
the current new-tab pane with the file pane (identical to how **New File** opens
— `register-modules/index.tsx:69`, `setPaneContent`). No new tab, no workspace
plumbing, no context reconstruction.

A shared helper `openRecentEntry(entry, onSelect)`
(`spa/src/lib/recent-files/open-recent-entry.ts`) does a source-specific
pre-flight, then either opens in place or shows a toast:
- **content** is built directly from the entry:
  `{ kind: entry.kind, source: entry.source, filePath: entry.path } as PaneContent`
  (kind is already known — no opener re-match needed).
- `daemon`: **guard host existence first** — if
  `useHostStore.getState().hosts` no longer contains `source.hostId`, show
  `useUndoToast.show(t('editor.recent.host_gone', { host }))` and abort. This is
  load-bearing: `createDaemonBackendForHost` → `getDaemonBase(hostId)` silently
  falls back to the active/first host for an unknown id, which would otherwise
  stat/open the **wrong host's** same-path file (codex safety finding). When the
  host exists, `stat(path)` via `createDaemonBackendForHost(source.hostId)`;
  resolves to a **file** (`stat.isFile`, not a directory) → `onSelect(content)`;
  not-found / not-a-file / error → `useUndoToast.show(t('editor.recent.open_failed', { name }))`.
- `local`: `stat` via `getFsBackend({ type: 'local' })` when present; file →
  `onSelect(content)`; missing / not-a-file / error → toast. If no local backend
  is registered (non-Electron), fall back to `onSelect(content)` (best-effort).
- `inapp`: `stat` via `getFsBackend({ type: 'inapp' })`; file →
  `onSelect(content)`; missing / not-a-file → toast.

All `stat` calls are wrapped in `try/catch`; any throw → the open-failed toast
(no unhandled rejection). `openRecentEntry` is `async`; the row `onClick` calls
it as `void openRecentEntry(entry, onSelect)`.

### 5.3 Testability
`openRecentEntry` is a standalone module tested in isolation (host-guard,
stat-exists → onSelect, stat-missing → toast, per source type) with faked
backends / stores. `EditorNewTabSection` is tested by mocking `openRecentEntry`
and asserting a row click calls it with the right entry + the section's
`onSelect`. The component keeps its single existing `onSelect` prop — no
NewTabProvider contract change.

### 5.4 i18n
New keys under `editor.recent.*`:
`title`, `filter.all`, `filter.text`, `filter.image`, `filter.pdf`,
`open_failed` (params: `{ name }`), `host_gone` (params: `{ host }`).
Add to every locale file the project ships.

## 6. Edge Cases
- Same basename, different source/host → distinct rows (dedup key includes
  source).
- List overflow → cap 50, oldest dropped.
- Removed host: badge shows raw hostId. Click is **guarded** — when
  `useHostStore` no longer knows the hostId, we abort with a toast BEFORE
  building a daemon backend, because `getDaemonBase` would otherwise silently
  fall back to another host and open/stat the wrong machine's file (§5.2). The
  entry is left in place (no auto-prune in v1).
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
- `open-recent-entry.test.ts`: daemon host present + stat exists → calls
  `onSelect` with `{kind, source, filePath}`; daemon host **absent** → toast,
  no `onSelect`, no daemon backend built (wrong-host guard); daemon stat 404 →
  toast; inapp stat missing → toast; inapp exists → onSelect; local with no
  backend → onSelect (best-effort); any thrown stat → toast (no rejection).
- `EditorNewTabSection.test.tsx`: renders Recently-opened only when non-empty;
  fixed chips render; chip filters rows by kind; host badge shows for daemon and
  not for local/inapp; row click calls `openRecentEntry(entry, onSelect)`
  (mocked) with the right entry; buttons still create files as before.
- `saveUntitledBuffer` record coverage: saving a fresh untitled buffer records
  the entry keyed by the **new** `nextPath` (not the stale Untitled path); an
  already-named existing file is NOT double-recorded on save.

## 8. Files Touched
- New: `spa/src/stores/useRecentFilesStore.ts` (+ test)
- New: `spa/src/lib/recent-files/record-recent-file.ts` (+ test)
- New: `spa/src/lib/recent-files/open-recent-entry.ts` (+ test) — host-guarded
  stat + in-place open / toast
- Edit: `spa/src/lib/storage/keys.ts` (STORAGE_KEYS.RECENT_FILES)
- Edit: `spa/src/lib/register-modules/file-open-bootstrap.ts` (record in
  `defaultTabOpener` AND popup `onOpenPath`)
- Edit: `spa/src/lib/open-in-app-file.ts` (record on inapp open)
- Edit: `spa/src/components/editor/EditorPane.tsx` (record inside
  `saveUntitledBuffer`, keyed by `nextPath`)
- Edit: `spa/src/components/editor/EditorNewTabSection.tsx` (+ test) — list UI +
  chips + host badge + row click → `openRecentEntry`
- Edit: locale files — `editor.recent.*` keys (incl. `open_failed`, `host_gone`)

> No `NewTabProvider` / `NewTabPage` contract change — the section keeps its
> single `onSelect` prop.

## 9. Phasing (for the plan)
- **Phase A** — Store + recorder + wiring at the three record points, no UI.
  Verifiable via unit tests (list populates on open/save).
- **Phase B** — `EditorNewTabSection` list UI: rows, fixed chips, host badge,
  click-to-open dispatch, i18n.

Two phases keep each PR-review-sized; A is pure logic/state, B is presentational
+ interaction.
