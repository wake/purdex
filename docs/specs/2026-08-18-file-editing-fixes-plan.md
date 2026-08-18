# Plan — In-Purdex file editing fixes

Spec: `docs/specs/2026-08-18-file-editing-fixes-spec.md` (5 phases, codex-reviewed round 1 — `task-msyaokms-9k2q12`).

**Delivery**: one branch (`worktree-file-editing-fixes`), one PR, 15 tasks each landing as its own commit in phase order. Every task is TDD — the listed tests are written first and must fail for the right reason before implementation.

**Definition of done per task**: new tests green, `cd spa && npx vitest run` fully green, `pnpm run lint` clean. Build is checked once at the end of each phase, not per task.

---

## Phase 1 — Remote file data safety

### T1.1 — Host-bound FS backend resolution

**Files**: `spa/src/lib/fs-backend.ts`, `spa/src/lib/register-modules/fs-backends.tsx`

**Tests first** (`spa/src/lib/fs-backend.test.ts`, extend `register-modules` tests):
1. `registerFsBackendResolver('daemon', fn)` → `getFsBackend({type:'daemon',hostId:'hostB'})` returns the resolver's backend; the resolver receives the full source.
2. With `activeHostId = 'hostA'` and two hosts registered, resolving for `hostB` produces a backend whose requests target host B's base URL (assert via a fetch spy on `read`).
3. No resolver registered for a type → falls back to the flat registry (`inapp`, `local` unchanged).
4. `getFsBackend({type:'daemon',hostId:''})` still resolves (registration-time probe at `fs-backends.tsx:18` must keep working) — falls through to the active-host proxy.
5. Resolver returning `undefined` falls back to the flat registry rather than yielding undefined.

**Implementation**:
- Add `type FsBackendResolver = (source: FileSource) => FsBackend | undefined`, a module-level `resolvers` map, `registerFsBackendResolver`, and resolver-first lookup in `getFsBackend`. `clearFsBackendRegistry` clears both maps.
- In `registerBuiltinFsBackends`, register a daemon resolver: non-empty `hostId` → `createDaemonBackendForHost(hostId)`; otherwise `undefined` (falls back to the existing active-host proxy, which stays registered).
- Do **not** memoize per host in this task (DaemonBackend is stateless; a comment already documents this).

### T1.2 — Load failure becomes an error state, never an empty buffer

**Files**: `spa/src/components/editor/EditorPane.tsx`

**Tests first** (`spa/src/components/editor/__tests__/EditorPane.test.tsx`):
1. `backend.read` rejects → no buffer is created in `useEditorStore`, an error surface renders (stable `data-testid`), and the failure reason is shown.
2. `backend.stat` rejects after a successful `read` → same error state, still no buffer.
3. Retry re-issues `read` and, on success, opens the buffer normally and clears the error.
4. The `untitled` branch is unaffected: an untitled pane still opens an empty buffer without any read attempt.
5. A stale response after the pane switched files does not resurrect the error (existing `stale` guard still honoured).

**Implementation**: replace the catch's `openBuffer(key, '', …)` with `setLoadError({ message })`; render the error surface instead of the editor when `loadError && !buffer`; Retry clears the error and bumps a local attempt counter included in the effect deps. Keep the `stale` flag semantics.

### T1.3 — `canSave` semantics, dirty dot, Save affordance

**Files**: `spa/src/components/editor/EditorPane.tsx`, `spa/src/components/editor/EditorToolbar.tsx`

**Tests first** (`EditorPane.test.tsx`, `EditorToolbar.test.tsx`):
1. Clean loaded buffer with `lastStat: null` → `canSave` false (Save disabled) and **no** dirty dot.
2. Untitled buffer never saved → Save enabled.
3. Dirty buffer → dot shown, Save enabled, Diff button shown.
4. Toolbar renders the dot from `isDirty` even when `canSave` is true for another reason.
5. Enabled Save carries the accent style; disabled keeps the muted style.

**Implementation**: `canSave = buffer.isDirty || (!!buffer.untitled && !buffer.lastStat)`; toolbar dot binds `isDirty`; Save button gets an accent colour when enabled.

**Phase gate**: `npx vitest run`, `pnpm run lint`, `pnpm run build`.

---

## Phase 2 — Live Mode losslessness

### T2.1 — Round-trip safety assessment

**Files**: `spa/src/lib/markdown/round-trip-safety.ts` (new), `spa/package.json` (promote `marked` to a direct dependency, matching the version `@tiptap/markdown` resolves).

**Tests first** (`round-trip-safety.test.ts`) — table-driven:
- safe: plain prose; headings; nested lists; fenced + indented code; blockquote; `hr`; links/images; emphasis; `del`; **GFM table**; **task list** (`- [ ]`, `- [x]`); reference-style link definition (`def` whitelisted); a document with a genuine `---` rule followed by a heading (must **not** be flagged as front matter).
- unsafe with the right blocker key: raw HTML block (`html`); inline HTML (`html`); front matter (`frontmatter`); footnote reference + definition (`footnote`).
- default-deny: a synthetic token type outside the whitelist → unsafe.
- pure and synchronous; called twice with the same input gives the same result.

**Implementation**: `assessMarkdownRoundTrip(md): { safe, blockers }`. Regex probes first (front matter: `---` fence on line 1 closed by `---`/`...`; footnote: `[^x]` reference **and** `[^x]:` definition), then `new marked.Lexer().lex(md)` walked recursively (`tokens`, `items`) against the whitelist from the spec. Blockers deduped, order stable.

### T2.2 — Schema widening: tables + task lists

**Files**: `spa/src/components/editor/TiptapEditor.tsx`, table/task-list styles in the editor CSS scope, `spa/package.json` (already pinned at 3.22.3).

**Tests first** (`TiptapEditor.test.tsx` — the suite mocks `@tiptap/react`, so assert on the extension array passed to `useEditor`; plus a real-editor round-trip test with the mocks disabled, in a separate file `TiptapEditor.roundtrip.test.ts`):
1. `useEditor` receives TableKit, TaskList, TaskItem alongside StarterKit and Markdown.
2. Real editor: GFM table markdown → `getMarkdown()` preserves every cell value and the alignment row (exact string equality is NOT asserted — padding/blank-line reformatting is accepted per decision 3).
3. Real editor: `- [ ] a\n- [x] b` round-trips identically.
4. Real editor: plain prose round-trips identically (no regression from adding extensions).
5. Table bubble menu appears only when the selection is inside a table, and its actions (add/remove row, add/remove column, delete table) dispatch the corresponding commands.

**Implementation**: add the three extensions; a small bubble menu (reuse `@tiptap/extension-bubble-menu`, already installed as a StarterKit sibling) rendered only for table selections; minimal table styling consistent with the existing `.tiptap-editor` scope (borders, header emphasis, cell padding) using theme variables only — no hardcoded colours (`feedback_theme_dual_source` convention).

### T2.3 — Mode resolution honours the gate

**Files**: `spa/src/components/editor/EditorPane.tsx`, `spa/src/components/editor/EditorToolbar.tsx`, locales.

**Tests first** (`EditorPane.test.tsx`):
1. Markdown buffer with unsafe content and no explicit user choice → resolves `raw`.
2. Same buffer with explicit `editorMode: 'wysiwyg'` → resolves `wysiwyg` (user choice wins).
3. Markdown buffer with safe content and no choice → `wysiwyg` (existing default preserved).
4. Non-markdown → `raw` regardless.
5. The toolbar shows the blocker reason when the gate forced raw, and shows nothing when raw was the user's choice.
6. The assessment is memoized: changing an unrelated prop does not re-run it (spy call count).

**Implementation**: memoize `assessMarkdownRoundTrip(buffer.content)` on `[key, buffer.content]`; insert the gate between "stale paneState → raw" and the markdown default; pass an optional `rawReason` to the toolbar; add i18n keys for each blocker.

### T2.4 — Preserve line endings and trailing newline

**Files**: `spa/src/stores/useEditorStore.ts`, `spa/src/lib/markdown/normalize-serialized.ts` (new), `spa/src/components/editor/EditorPane.tsx`

**Tests first**:
- Store (`useEditorStore.test.ts`): `openBuffer` records `sourceEol` / `sourceTrailingNewline` from the loaded content; `updateContent` does **not** change them (unlike `eol`); `reloadBuffer` re-derives them from the new content; `renameBuffer` carries them across.
- Helper (`normalize-serialized.test.ts`): LF source + LF output → unchanged; CRLF source → output converted to CRLF; source ended with newline → exactly one trailing newline appended (never two); source without trailing newline → none added; empty output handled.
- Integration (`EditorPane.test.tsx`): a CRLF markdown file edited in Live Mode produces CRLF in the buffer; a file that ends with a newline keeps it; opening a canonical file in Live Mode without typing leaves `isDirty` false.

**Implementation**: add the two immutable fields (set in `openBuffer` / `reloadBuffer`, untouched by `updateContent`, carried by `renameBuffer` / `markSaved`); apply the pure helper in the Tiptap `onChange` before `updateContent`. Monaco path untouched.

**Phase gate**: full vitest, lint, build.

---

## Phase 3 — Recent files remap + save toast

### T3.1 — Recent-files store mutations

**Files**: `spa/src/stores/useRecentFilesStore.ts`

**Tests first** (`useRecentFilesStore.test.ts`):
1. `renamePath` on an exact match updates `path` and `name`, keeps `kind` / `openedAt`.
2. Folder rename remaps every `from/`-prefixed descendant; `/buffer/ab` is not matched by a `/buffer/a` rename (trailing-slash boundary).
3. Cross-source isolation: same path under a different source type, and same path on a different daemon `hostId`, are untouched.
4. Collision: destination already present → single merged entry at the destination with the **newer** `openedAt`; no duplicates.
5. `removePath` removes the entry and its descendants; unrelated entries survive.
6. Both mutations are no-ops when nothing matches (same array identity not required, but no content change).

**Implementation**: add `renamePath` / `removePath` mirroring `recentKey` identity rules; keep the `MAX_RECENT` cap logic untouched.

### T3.2 — Wire the remap into every mutation path

**Files**: `spa/src/components/editor/storage/storage-actions.ts`, `spa/src/components/editor/EditorPane.tsx`

**Tests first**:
1. `remapPanesUnder` (or its callers `renameStorageEntry` / `moveStorageEntry`) also calls `renamePath` with the same `(source, from, to)`.
2. Folder move remaps recent entries for descendants.
3. `deleteStorageEntries` calls `removePath` for each deleted path (including a folder's descendants).
4. `EditorPane.handleRenameSubmit` on a saved file remaps the recent entry (remote source included — same code path).
5. Untitled save path does not attempt a remap (no prior entry).

**Implementation**: single call added inside `remapPanesUnder` (covers rename + move), one in the delete action, one in the editor rename. No new abstraction.

### T3.3 — Save result toast

**Files**: `spa/src/components/editor/EditorPane.tsx`, `spa/src/locales/en.json`, `spa/src/locales/zh-TW.json`

**Tests first** (`EditorPane.test.tsx`):
1. Save with a dirty buffer → `write` called, `useUndoToast.show` called with the saved message including the file name.
2. Save with a clean saved buffer → **no** `write`, toast shows the "no changes" message.
3. `write` rejects → toast shows the failure message carrying the reason; buffer stays dirty; no `markSaved`.
4. ⌘S and the toolbar button produce identical outcomes.
5. The untitled → rename-popover path shows no toast.

**Implementation**: restructure `handleSave` so the early return becomes an explicit "unchanged" outcome; wrap the write in try/catch and surface the reason; add the three i18n keys to both locales.

**Phase gate**: full vitest, lint, build.

---

## Phase 4 — Storage operations

### T4.1 — Per-row hover actions

**Files**: `spa/src/components/editor/storage/` (row component + `StoragePane.tsx`)

**Tests first**:
1. Hovering/focusing a row reveals Open (files only) / Rename / Delete; they are in the DOM and keyboard reachable (visible on `:focus-within`).
2. Each action targets **that** row even when a different row is selected.
3. Rename opens the existing popover anchored to that row; Delete removes only that entry.
4. A folder row offers Rename/Delete but not Open.

**Implementation**: extend the existing row rendering with an action cluster; reuse `renameStorageEntry` / `deleteStorageEntries`; no new action layer.

### T4.2 — Manual empty-file cleanup

**Files**: `spa/src/components/editor/storage/storage-actions.ts`, `StoragePane.tsx`, locales

**Tests first**:
1. `findEmptyFiles(tree)` returns exactly the 0 B files — no folders, no non-empty files, recursive into subfolders.
2. Confirming deletes exactly the listed paths in one pass; cancelling deletes nothing.
3. Result toast reports the deleted count; zero candidates shows a distinct "nothing to clean" message without a dialog.
4. A delete failure surfaces an error and leaves the remaining entries intact.

**Implementation**: pure `findEmptyFiles` over the already-loaded tree; a confirmation dialog listing paths; batch delete through the existing action; i18n in both locales.

### T4.3 — Visible batch selection

**Files**: `StoragePane.tsx` + row component, locales

**Tests first**:
1. Row checkbox toggles membership in the same `selected` set the modifier-click path uses.
2. Header checkbox selects/clears all visible rows; indeterminate state when partially selected.
3. Action bar appears only when `selected.size > 0`, shows the count, and its Delete removes every selected entry.
4. Existing cmd/shift-click behaviour is unchanged (regression).

**Implementation**: checkbox column + header checkbox + selection action bar driven by the existing `selected` state; no state-model change.

**Phase gate**: full vitest, lint, build.

---

## Phase 5 — Automatic placeholder cleanup

### T5.1 — Placeholder registry

**Files**: `spa/src/stores/usePlaceholderFilesStore.ts` (new, persisted), reservation call sites (`EditorNewTabSection.tsx`, `EditorPane.tsx` new-buffer action, `storage-actions.createStorageFile`)

**Tests first**:
1. Each reservation path registers the reserved path (in-app source only).
2. A successful write to a registered path deregisters it — **including a save of empty content**.
3. Rename / move deregisters (both the old and the new path end up unregistered).
4. Explicit delete drops the entry.
5. Registry survives a store rehydrate (persisted) and never records remote/local paths.

**Implementation**: minimal persisted set keyed like `recentKey`; deregistration hooked into the save path, the rename remap, and the delete action.

### T5.2 — Cleanup trigger

**Files**: `spa/src/components/editor/EditorPane.tsx` (or a small helper in `spa/src/lib/`), `spa/src/stores/useEditorStore.ts` (read-only use)

**Tests first**:
1. Open a registered placeholder, close it without typing → file deleted, registry entry gone.
2. Placeholder saved (even empty) → not deleted.
3. Placeholder renamed → not deleted under either name.
4. Two panes on the same placeholder: closing one deletes nothing; closing the last deletes.
5. Pane move / content swap where another pane still references the buffer → no deletion (drive through the `pane-move` path, not a bare unmount).
6. Remote and local files with identical shape are never deleted.
7. Delete failure is swallowed and does not throw into the unmount path.

**Implementation**: on pane detach, check (a) path is in the registry, (b) source is in-app, (c) **no `paneState` in the editor store references that `bufferKey`**; only then delete best-effort and deregister.

**Phase gate**: full vitest, lint, build.

---

## Execution notes

- Tasks are dispatched to subagents one at a time in this order; each subagent runs the TDD loop for exactly one task and commits it. Subagent Bash calls must be prefixed with `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/file-editing-fixes && `; file writes use the full worktree-prefixed absolute path.
- Cross-task dependencies: T2.3 needs T2.1; T2.2 must land before T2.1's whitelist claims about tables/task lists are true in practice (T2.1's tests assert the whitelist, not editor behaviour, so the order 2.1 → 2.2 is fine); T5.2 needs T5.1; T3.2 needs T3.1.
- After all phases: PR, then two codex review rounds (standard, then 3-parallel adversarial), findings triaged into a table (confidence / relevance / complexity), fixes applied, then merge + a separate bump PR.
- Follow-up issue to file at PR time: Storage restore leaves recent-file entries pointing at replaced paths.
