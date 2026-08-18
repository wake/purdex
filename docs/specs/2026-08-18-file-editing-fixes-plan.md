# Plan — In-Purdex file editing fixes

Spec: `docs/specs/2026-08-18-file-editing-fixes-spec.md` (5 phases, codex-reviewed round 1 — `task-msyaokms-9k2q12`).

**Delivery** (revised after plan review — a single 16-task PR spreads review across daemon FS resolution, editor save/load, Tiptap schema, recent-files, Storage UI and placeholder cleanup): **three sequential PRs**, each branched from the then-current `origin/main`, each getting the two-round codex review before merge.

| PR | Tasks | Theme |
|---|---|---|
| **PR-A** | T1.1–T1.3, T3.1–T3.3 (+ the spec/plan docs commits) | Remote data safety, recent-files remap, save toast |
| **PR-B** | T2.1, T2.2a, T2.2b, T2.3, T2.4 | Live Mode losslessness (spec requires this stay one review unit) |
| **PR-C** | T4.1–T4.3, T5.1–T5.2 | Storage operations + placeholder cleanup (T5 depends on T4's call sites) |

One bump PR after all three land. Every task is TDD — the listed tests are written first and must fail for the right reason before implementation, and each task lands as its own commit.

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
6. **Real-consumer coverage** (required for this task to count as done, per spec's Phase 1 testing): `EditorPane` loading a file whose `source.hostId` is *not* the active host issues its read/stat against that host; `FileTreeView` lists against the source's own host. Image/PDF preview panes get the same assertion where their existing harness allows it.

**Implementation**:
- Add `type FsBackendResolver = (source: FileSource) => FsBackend | undefined`, a module-level `resolvers` map, `registerFsBackendResolver`, and resolver-first lookup in `getFsBackend`. `clearFsBackendRegistry` clears both maps.
- In `registerBuiltinFsBackends`, register a daemon resolver: non-empty `hostId` → `createDaemonBackendForHost(hostId)`; otherwise `undefined` (falls back to the existing active-host proxy, which stays registered).
- `clearFsBackendRegistry` must clear the resolver map too — `test-bootstrap-harness.ts:31` resets the global registry between tests and a leaked resolver would bleed across suites.
- ~~Do **not** memoize per host in this task~~ — **reversed during implementation, with evidence**: the resolver *must* return a stable instance per host. `ImagePreviewPane` compares the backend object during render (its comment states "getFsBackend returns a stable Map-cached instance, so comparing the backend object does not loop") and `PdfPreviewPane` uses it as a `useEffect` dependency. Returning a fresh `DaemonBackend` per call produced a real `Too many re-renders` failure in the preview-pane tests and would re-download the PDF on every render. The resolver therefore caches `Map<hostId, FsBackend>`, with the cache's lifetime tied to the registration (cleared and rebuilt by `clearFsBackendRegistry`). This is safe because `createDaemonBackendForHost` re-reads `useHostStore` on every call, so host address/token changes still take effect immediately, and `DaemonBackend` remains stateless. The stability invariant is pinned by test.

### T1.2 — Load failure becomes an error state, never an empty buffer

**Files**: `spa/src/components/editor/EditorPane.tsx`

**Tests first** (`spa/src/components/editor/__tests__/EditorPane.test.tsx`):
1. `backend.read` rejects → no buffer is created in `useEditorStore`, an error surface renders (stable `data-testid`), and the failure reason is shown.
2. `backend.stat` rejects after a successful `read` → same error state, still no buffer.
3. Retry re-issues `read` and, on success, opens the buffer normally and clears the error.
4. The `untitled` branch is unaffected: an untitled pane still opens an empty buffer without any read attempt.
5. A stale response after the pane switched files does not resurrect the error (existing `stale` guard still honoured).

**Implementation**: replace the catch's `openBuffer(key, '', …)` with `setLoadError({ message })`; render the error surface instead of the editor when `loadError && !buffer`; Retry clears the error and bumps a local attempt counter included in the effect deps. Keep the `stale` flag semantics.

### T1.2b — Unavailable backend surfaces as an error, not a permanent spinner

Added during implementation (surfaced by T1.2, deliberately not folded into it). The load effect's `if (!backend) return` leaves the pane on "Loading…" forever with no explanation — the same silent-failure class T1.2 fixes, reached by a different route (e.g. a `local` source outside Electron, or a daemon source whose host was removed).

**Files**: `spa/src/components/editor/EditorPane.tsx`, locales.

**Tests first**:
1. `getFsBackend` returns undefined → the T1.2 error surface renders with a "no backend" reason; no buffer is created; the spinner is gone.
2. Retry re-resolves the backend and, once available, loads normally.
3. The untitled path is unaffected.

**Implementation**: reuse T1.2's `loadError` state and error surface with a distinct i18n reason; `ImagePreviewPane` / `PdfPreviewPane` already have a "No FS backend" precedent to match in wording.

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

### T2.2a — Schema widening: tables + task lists

**Files**: `spa/src/components/editor/TiptapEditor.tsx`, `spa/package.json` (already pinned at 3.22.3).

**Tests first**:
- In `TiptapEditor.test.tsx` (file-level `@tiptap/react` mock): `useEditor` receives TableKit, TaskList, TaskItem alongside StarterKit and Markdown.
- In a **new** `TiptapEditor.roundtrip.test.ts` that neither imports `TiptapEditor.test.tsx` nor declares its own mocks (file-scoped mocks do not leak across files, confirmed in review) — real editor:
  1. GFM table markdown → `getMarkdown()` preserves every cell value and the alignment row (exact string equality is NOT asserted — padding/blank-line reformatting is accepted per decision 3).
  2. `- [ ] a\n- [x] b` round-trips identically.
  3. Plain prose round-trips identically (no regression from adding extensions).

**Implementation**: add the three extensions to the array. Nothing else.

### T2.2b — Table editing affordance + styling

**Files**: `spa/src/components/editor/TiptapEditor.tsx` (+ a small menu component), editor CSS scope.

**Tests first** (mock-based component tests — driving a real selection into a table in jsdom is disproportionately hard, so the split is: real editor proves round-trip in T2.2a, mocked editor proves menu behaviour here):
1. Menu renders only when the mocked editor reports the selection is inside a table; absent otherwise.
2. Each button invokes the corresponding chain command (add row before/after, delete row, add column before/after, delete column, delete table).
3. Menu is keyboard reachable and does not steal focus from the editable surface.

**Implementation**: a small bubble menu (`@tiptap/extension-bubble-menu`, already present as a StarterKit sibling) rendered only for table selections; minimal table styling in the existing `.tiptap-editor` scope (borders, header emphasis, cell padding) using theme variables only — no hardcoded colours (`feedback_theme_dual_source` convention).

### T2.3 — Mode resolution honours the gate

**Files**: `spa/src/components/editor/EditorPane.tsx`, `spa/src/components/editor/EditorToolbar.tsx`, locales.

**Tests first** (`EditorPane.test.tsx`):
1. Markdown buffer with unsafe content and no explicit user choice → resolves `raw`.
2. Same buffer with explicit `editorMode: 'wysiwyg'` → resolves `wysiwyg` (user choice wins).
3. Markdown buffer with safe content and no choice → `wysiwyg` (existing default preserved).
4. Non-markdown → `raw` regardless.
5. The toolbar shows the blocker reason when the gate forced raw, and shows nothing when raw was the user's choice.
6. The assessment is memoized: re-rendering with an unrelated prop change does not re-run it (spy call count). **This case goes in its own test file** that mocks `round-trip-safety` *before* importing `EditorPane` — the existing `EditorPane.test.tsx` imports the component at the top with a fixed mock set, so a spy-based memoization assertion cannot be retrofitted into it.

**Implementation**: memoize `assessMarkdownRoundTrip(buffer.content)` on `[key, buffer.content]`; insert the gate between "stale paneState → raw" and the markdown default; pass an optional `rawReason` to the toolbar; add i18n keys for each blocker.

### T2.4 — Preserve line endings and trailing newline

**Files**: `spa/src/stores/useEditorStore.ts`, `spa/src/lib/markdown/normalize-serialized.ts` (new), `spa/src/components/editor/EditorPane.tsx`

**Tests first**:
- Store (`useEditorStore.test.ts`): `openBuffer` records `sourceEol` / `sourceTrailingNewline` from the loaded content; `updateContent` does **not** change them (unlike `eol`); `reloadBuffer` re-derives them from the new content; `renameBuffer` carries them across.
- Helper (`normalize-serialized.test.ts`): LF source + LF output → unchanged; CRLF source → output converted to CRLF; source ended with newline → exactly one trailing newline appended (never two); source without trailing newline → none added; empty output handled.
- Integration (`EditorPane.test.tsx`): a CRLF markdown file edited in Live Mode produces CRLF in the buffer; a file that ends with a newline keeps it; opening a canonical file in Live Mode without typing leaves `isDirty` false.

**Implementation**: add the two immutable fields (set in `openBuffer` / `reloadBuffer`, untouched by `updateContent`); apply the pure helper in the Tiptap `onChange` before `updateContent`. Monaco path untouched.

Preservation across `renameBuffer` / `markSaved` relies on their existing partial-merge shape (`{ ...buffer, ...metadata }`, `useEditorStore.ts:164`, `:180`) and every current caller passing only language/untitled metadata — **no special-casing is added to `markSaved`**. A store test pins this so a future metadata caller cannot silently drop the fields.

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
5. **Regression**: clicking an action button does **not** trigger the row's selection `onClick` (`StorageRow.tsx:167` binds the whole row) — the handler stops propagation.

**Implementation**: extend the existing row rendering with a trailing action cluster and row-scoped callbacks; reuse `renameStorageEntry` / `deleteStorageEntries`; no new action layer.

**Shared surface with T4.3**: both tasks modify `StorageRow` structure and props. T4.1 lands *only* the trailing action cluster + row-scoped callbacks; T4.3 then adds the leading checkbox column, header checkbox and action bar. Both must carry the stop-propagation regression test.

### T4.2 — Manual empty-file cleanup

**Files**: `spa/src/components/editor/storage/storage-actions.ts`, `StoragePane.tsx`, locales

**Tests first**:
1. `findEmptyFiles(tree)` returns exactly the 0 B files — no folders, no non-empty files, recursive into subfolders.
2. Confirming deletes exactly the listed paths in one pass; cancelling deletes nothing.
3. Result toast reports the deleted count; zero candidates shows a distinct "nothing to clean" message without a dialog.
4. A delete failure surfaces the error and refreshes the tree; already-deleted entries stay deleted (**partial deletion is the accepted semantics** — see below).

**Implementation**: pure `findEmptyFiles` over the already-loaded tree; a confirmation dialog listing paths; batch delete through the existing `deleteStorageEntries`; i18n in both locales.

**Delete semantics (corrected after review)**: `deleteStorageEntries` (`storage-actions.ts:530-552`) closes panes and then deletes path by path with no transaction, so a mid-way failure leaves earlier paths deleted. This task does **not** introduce an atomic or partial-reporting delete helper (out of scope, and the failure mode is benign for a housekeeping action on 0 B files). The acceptance criterion is therefore "error surfaced + tree refreshed", not "nothing was deleted".

### T4.3 — Visible batch selection

**Files**: `StoragePane.tsx` + row component, locales

**Tests first**:
1. Row checkbox toggles membership in the same `selected` set the modifier-click path uses.
2. Header checkbox selects/clears all visible rows; indeterminate state when partially selected.
3. Action bar appears only when `selected.size > 0`, shows the count, and its Delete removes every selected entry.
4. Existing cmd/shift-click behaviour is unchanged (regression).
5. **Regression**: clicking a checkbox toggles selection **without** firing the row's own `onClick` (no open/navigate side effect).

**Implementation**: checkbox column + header checkbox + selection action bar driven by the existing `selected` state; no state-model change. Builds on T4.1's row structure.

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
6. **Restore invalidation**: after a successful storage restore (`replaceTree`), the registry is cleared. A registry entry minted before the restore must not survive it — otherwise a restored *real* file at the same path would later be auto-deleted as a "placeholder".

**Implementation**: minimal persisted set (`purdexStorage`, same store convention as `useRecentFilesStore`) keyed like `recentKey`; deregistration hooked into the save path, the rename/move remap (same call site as T3.2's `renamePath`, so move is covered), the delete action, and the restore wiring (`storage-backup/restore-wiring.ts`) which clears the whole registry.

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
8. **The decision is made on post-close state**: closing the last pane deletes (the check must not see the closing pane's own `paneState` and conclude the buffer is still referenced).

**Implementation (ordering is the correctness hinge, per review)**: a single helper wraps the detach —

1. call `closePane(paneId, key)` first (it removes this pane's `paneState` and, when it was the last reference, the buffer);
2. **then** read the post-close store state and check: (a) path is in the placeholder registry, (b) source is in-app, (c) no remaining `paneState` references that `bufferKey`;
3. only then delete best-effort and deregister.

Checking before `closePane` would always find the closing pane's own state and never fire. Checking after is also correct for pane moves, because `pane-move.ts:59-71` attaches the destination pane before the source unmounts — so a moved buffer still has a live reference at step 2 and is not deleted.

**Phase gate**: full vitest, lint, build.

---

## Execution notes

- Tasks are dispatched to subagents one at a time in this order; each subagent runs the TDD loop for exactly one task and commits it. Subagent Bash calls must be prefixed with `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/file-editing-fixes && `; file writes use the full worktree-prefixed absolute path.
- Cross-task dependencies: T2.3 needs T2.1; T2.2b needs T2.2a; T5.2 needs T5.1; T5.1's rename/move deregistration shares T3.2's remap call site; T3.2 needs T3.1; T4.3 builds on T4.1's row structure.
- Per PR: open it, run two codex review rounds (standard, then 3-parallel adversarial), triage findings into a table (confidence / relevance / complexity), apply fixes, merge, then branch the next PR from the updated `origin/main`.
- Follow-up issue to file at PR-A time: Storage restore leaves recent-file entries pointing at replaced paths (the placeholder registry gets restore invalidation in T5.1, but recent files do not).

## Plan review outcomes (codex — `task-msyb96ba-s995cw`)

Three blockers, all resolved above: T4.2's failure test was unverifiable against the non-atomic `deleteStorageEntries` (acceptance reworded to partial-delete semantics, no new helper); T5.1 had no restore invalidation for a persisted registry (added — restore clears it); T5.2's "no paneState references the buffer" check had no defined ordering (now explicitly post-`closePane`, with the pane-move attach-before-unmount ordering as the reason it stays correct).

Also adopted: T2.2 split into 2.2a (schema + real-editor round-trip) and 2.2b (menu + styling, mock-based tests); T1.1 gains real-consumer coverage in its definition of done; T2.3's memoization case moves to its own test file; T2.4 documents its reliance on partial metadata merge; T4.1/T4.3 get the shared-surface note and stop-propagation regression tests; delivery split into three PRs.
