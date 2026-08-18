# Spec — In-Purdex file editing fixes (remote safety, Live Mode losslessness, recent-file remap, Storage operations)

## Goal

Close four user-reported defects in the in-Purdex file editing experience, plus the Storage housekeeping gaps they exposed:

1. **Remote files can silently open empty and then overwrite the real file.** The daemon FS backend ignores the file's own `hostId`, and a failed read/stat silently opens an empty buffer.
2. **Markdown Live Mode destroys content it cannot model** (tables vanish entirely; front matter, raw HTML, task-list checkboxes, footnotes are mangled; the trailing newline is eaten).
3. **Recent Files goes stale on rename/move/delete** — the entry keeps pointing at the old path forever.
4. **Storage has no per-row actions, no way to clear the accumulated 0 B files, and no visible batch-selection affordance** (multi-select exists but is invisible).

Plus a small UX addition requested with them: an explicit save result toast.

## Evidence (measured, not assumed)

- **Live Mode round-trip** — probed with the shipped `@tiptap/starter-kit` + `@tiptap/markdown` 3.22.3 (jsdom, real editor, `parse → getMarkdown`):

  | input | output |
  |---|---|
  | `\| a \| b \|` table | `""` (whole table dropped) |
  | `---\ntitle: x\n---\n\n# Body` | `---\n\n## title: x\n\n# Body` |
  | `<div class="x">raw html</div>` | `raw html` |
  | `- [ ] todo\n- [x] done` | `- todo\n- done` |
  | `text[^1]\n\n[^1]: note` | `text[^1](note)` |
  | `# Title\n\nHello world.\n` | `# Title\n\nHello world.` (trailing `\n` eaten) |
  | `# Title\r\n\r\nbody\r\n` | `# Title\n\nbody` (CRLF → LF) |
  | `_em_ / __strong__` | `*em* / **strong**` |
  | `* one` / `1) one` | `- one` / `1. one` |

  Root cause: `@tiptap/markdown` parses with `marked` (GFM — tables *are* tokenized), but Tiptap's schema from StarterKit has no `table` / `taskList` node, so those tokens are dropped **at parse time**. Serialization merely writes the already-lossy document back.

- **Mount does not emit `onUpdate`** — measured 0 emissions for table / plain / html inputs, and `setContent(..., { emitUpdate: false })` also emits 0. So the damage only reaches the buffer once the user actually edits, and only reaches disk on save.

- **Schema widening works without custom bridges** (Phase 2.2 precondition, verified before writing this revision). With `@tiptap/extension-table@3.22.3` (`TableKit`) + `@tiptap/extension-task-list` + `@tiptap/extension-task-item` added to the extension array and **no** other configuration:

  | input | output |
  |---|---|
  | `\| a \| b \|` table | content **preserved**; cells padded to equal width, wrapped in extra blank lines |
  | `\| :--- \| ---: \|` alignment | preserved as `:----` / `-----:` |
  | `- [ ] todo` / `- [x] done` | **identical** |
  | plain prose | **identical** |

  The extensions ship their own `parseMarkdown` / `renderMarkdown`, which `MarkdownManager` picks up from the extension array — no `Markdown.configure` bridge needed. Table output is reformatted (column padding, alignment markers, surrounding blank lines); that is a style-level rewrite and is accepted under decision 3.

- **`canSave` is wired to the dirty dot** — `EditorToolbar.tsx:127` renders the "Unsaved changes" dot from `saveEnabled`, which is `canSave = buffer.isDirty || !buffer.lastStat` (`EditorPane.tsx:144`). `lastStat` is null only on the silent-empty-buffer catch path (`EditorPane.tsx:196`), which is why a failed remote read shows as "modified" *and* as missing content.

- **Daemon backend is active-host bound** — `register-modules/fs-backends.tsx:20` resolves `hostId = activeHostId ?? hostOrder[0]`; `getFsBackend(source)` (`fs-backend.ts`) keys the registry by `source.type` only, so `source.hostId` never reaches the backend.

- **0 B files** — the New File buttons eagerly reserve a real empty file (`EditorNewTabSection.tsx:47`, `EditorPane.tsx:446`, `storage-actions.ts:100`, the #854 atomic-namer design). Nothing ever cleans one up if the user never types.

## Decisions (user-approved, do not relitigate)

1. **Live Mode stays the default for markdown.** The user reads documents in Live Mode; forcing raw as the default is rejected.
2. **Route C**: safety gate (do not enter Live Mode when the content cannot survive a round-trip) **and** schema widening (add table + task list) — both, this cycle.
3. **Style-level rewrites are accepted** (`_em_`→`*em*`, `*`→`-` bullets, `1)`→`1.`, setext→ATX, indented code→fenced). They lose no meaning and suppressing them would mean replacing the serializer.
4. **Line endings and trailing newline are NOT style** — they must be preserved.
5. **0 B cleanup: manual and automatic.** Storage gets a "clean empty files" action, *and* closing an untouched, never-written 0 B file removes it.
6. **Save feedback reuses the existing toast** (`useUndoToast` / `GlobalUndoToast`, bottom-center). No second toast system, no separate bottom-right stack.
7. **The daemon backend binds to the file's own host.** Active-host fallback survives only for sources that carry no `hostId`.

## Scope

- **In**: editor file load/save path, daemon FS backend resolution, Live Mode gating + markdown schema, recent-files store remapping, Storage pane row/batch actions and empty-file cleanup, save toast, i18n for all new strings (en + zh-TW).
- **Out**: front-matter / raw-HTML passthrough nodes (rejected as too costly this cycle — they are handled by the safety gate instead); footnote support; a markdown serializer replacement; local (Electron) backend changes; daemon Go changes; the 10 MB remote read cap.

---

## Phase 1 — Remote file data safety

### 1.1 Host-bound daemon backend

`getFsBackend(source)` must return a backend bound to `source.hostId` for daemon sources.

Add a resolver layer to the registry (`spa/src/lib/fs-backend.ts`):

```ts
type FsBackendResolver = (source: FileSource) => FsBackend | undefined
export function registerFsBackendResolver(sourceType: string, resolver: FsBackendResolver): void
```

`getFsBackend(source)` consults the resolver for `source.type` first and falls back to the flat `backends` map (so `inapp` / `local` and every existing caller are untouched).

`registerBuiltinFsBackends` (`register-modules/fs-backends.tsx`) registers a daemon resolver returning `createDaemonBackendForHost(source.hostId)` when `hostId` is non-empty, and the existing active-host proxy otherwise (registration-time probes such as `getFsBackend({ type: 'daemon', hostId: '' })` keep working).

`createDaemonBackendForHost` already exists (`fs-backend-daemon.ts:80`) and is already the pattern used by `openRecentEntry` to dodge this exact wrong-host fallback.

### 1.2 No silent empty buffer

`EditorPane`'s load effect (`EditorPane.tsx:173-201`) must not turn a read/stat failure into an empty buffer. On failure it records a **load error** for the pane and renders an error state instead of an editor surface:

- Error state shows the failure reason and a **Retry** button; no buffer is created, so nothing can be saved over the real file.
- **Only the `untitled` branch may open an empty buffer.** Every non-untitled load failure — including "not found" — becomes a load error. (Revised after review: there is no create-intent signal on a non-untitled pane to distinguish "a new file the user asked for" from "the file is gone / unreachable". New in-app files are eagerly reserved as real 0 B files before the pane opens (`EditorPane.tsx:446`, `EditorNewTabSection.tsx:47`), so they always exist and read successfully; nothing legitimately depends on the not-found → empty-buffer path.)
- A recent-file entry pointing at a deleted path therefore shows the error state instead of silently opening a blank editor — which is the desired behaviour and complements Phase 3's `removePath`.
- Load-error state is per-pane local state (no store change needed); Retry clears it and re-runs the effect.

### 1.3 Dirty dot and `canSave` semantics

- The toolbar dot renders from **`isDirty` only** (`EditorToolbar.tsx:127`).
- `canSave` (`EditorPane.tsx:144`) becomes `buffer.isDirty || (!!buffer.untitled && !buffer.lastStat)` — i.e. a never-saved untitled buffer stays savable, but a loaded file with a missing stat does not masquerade as modified. With 1.2 in place `lastStat` is null only for untitled buffers anyway; the narrower predicate keeps it true by construction.
- The Save button gets a clearer resting state (it is currently a 14 px floppy at `opacity-30` and effectively invisible): keep the icon, but give the enabled state an accent colour so "savable" is legible at a glance.

### Testing (Phase 1)

- `getFsBackend({ type: 'daemon', hostId: 'hostB' })` returns a backend whose base URL is host B's, while `activeHostId` is host A.
- Existing `inapp` / `local` resolution is unchanged; the `hostId: ''` probe still resolves (registration-time, `fs-backends.tsx:18`).
- Host-bound resolution is exercised through the real consumers, not just the registry: `EditorPane` (load / external-change / save / rename), `ImagePreviewPane.tsx:20` and `PdfPreviewPane.tsx:17`, `FileTreeView.tsx:35`.
- A read rejection leaves **no buffer** in the store, renders the error state, and Retry re-issues the read.
- A stat rejection after a successful read behaves the same.
- `canSave` is false for a clean loaded buffer with `lastStat === null`; true for an untitled buffer; the dot follows `isDirty` and not `canSave`.

---

## Phase 2 — Live Mode losslessness (route C)

### 2.1 Round-trip safety gate

New pure module `spa/src/lib/markdown/round-trip-safety.ts`:

```ts
export interface RoundTripVerdict {
  safe: boolean
  blockers: string[] // stable keys, e.g. 'html', 'frontmatter', 'footnote'
}
export function assessMarkdownRoundTrip(md: string): RoundTripVerdict
```

Two complementary detectors (revised after review — token types alone cannot see front matter or footnotes):

**(a) Token whitelist over `marked`'s lexer** (default-deny). `marked` is already a transitive dependency via `@tiptap/markdown`; promote it to a direct dependency and lex with the same `marked.Lexer` the markdown manager uses, then walk the token tree comparing each token `type` against the whitelist.

Whitelist (marked v17 token names, valid once 2.2 lands): `space`, `paragraph`, `text`, `heading`, `list`, `list_item`, `checkbox`, `code`, `codespan`, `blockquote`, `hr`, `table`, `link`, `image`, `strong`, `em`, `del`, `br`, `escape`, `def`.
Notes:
- Task items are **not** a distinct marked token — they are `list_item`s carrying `task: boolean` (the `taskList` / `taskItem` names are Tiptap's internal intermediate types, synthesized by `MarkdownManager`). The whitelist must not invent them.
- **`checkbox` must be whitelisted** (found during implementation): marked additionally unshifts a `{ type: 'checkbox', checked }` token into a task item's children — directly on `list_item.tokens` for a tight list, inside the inner `paragraph.tokens` for a loose one. Omitting it makes *every* task list unsafe, contradicting the requirement that task lists round-trip.
- `def` (reference-style link definitions) is whitelisted, not blocked: the round-trip rewrites them to inline links, which is a style-level change under decision 3. Blocking them would push a large share of ordinary documents into raw.
- Blockers: `html` and anything unrecognised (default-deny, so a future syntax fails closed).
- **Traversal must cover table cells** (found during implementation): a table's inline tokens live in `header[].tokens` and `rows[][].tokens`, not in `tokens`/`items`. Walking only the latter two lets `| <b>1</b> | 2 |` pass as safe and then have its markup stripped in Live Mode — exactly what the gate exists to prevent. Follow marked's own `walkTokens` shape.
- An unrecognised token contributes its own `type` as the blocker key, so `html` falls out naturally. Consumers rendering the reason must have a fallback for keys they don't have copy for.

**(b) Pre-lex regex probes** for constructs `marked` silently degrades rather than tokenizing distinctly:
- **Front matter** — a `---` fence in the first line closed by a later `---`/`...` line, **whose body reads as a YAML mapping** (every non-blank line is a `key:` / `key: value`, an indented continuation of one, a `- item` under a key, or a `#` comment — and at least one real key is present). (Do **not** infer it from an `hr` + `heading` token pair: legitimate documents contain that sequence and would be misclassified.)

  Scope narrowed during implementation, after a false positive on `---\nhello\n---`: only **mapping-style** front matter is detected. A sequence-only (`---\n- a\n- b\n---`) or comment-only fence is treated as ordinary markdown, because in a markdown file that byte sequence is overwhelmingly more likely to be "thematic break, list, thematic break" than a YAML sequence document used as front matter. Widening the probe to catch those would misclassify common documents, and the cost of a false positive is a perfectly good file being locked out of Live Mode.
- **Footnotes** — a `[^label]` reference together with a `[^label]:` definition line.
- **Mixed line endings** (`mixed-eol`) — a file containing both CRLF and LF lines. `sourceEol` is a single value and cannot restore a per-line mixture: `a\r\nb\nc\r\n` was written back as `a\r\nb\r\nc\r\n`, changing a line the user never touched. Such files stay in raw, where Monaco is byte-faithful.

**(c) HTML entities** (`html-entity`) — `&#169;` / `&#x41;` / `&copy;` and friends. Tiptap has no node for them: marked leaves the reference as literal text and the serializer escapes its ampersand, so `&#169;` → `&amp;#169;` and a rendered `©` becomes the visible string `&#169;`. That is semantic corruption, not a style rewrite.

Two refinements found during implementation, both measured against the real editor:
- The detector runs over the **leaf `text` tokens of the lexer**, not as a pre-lex regex over the source. An entity inside a code span or code block is literal text on both sides of the round trip (measured), and a document *explaining* HTML entities is exactly the document a source-wide scan would wrongly lock out of Live Mode.
- `&amp;` / `&lt;` / `&gt;` are **exempt**: they come back byte-identical because they are the escapes the serializer emits itself. This is not a convenience — the serializer rewrites a bare `A & B` to `A &amp; B`, so blocking `&amp;` would mean every file Live Mode saves locks itself out of Live Mode on the next open. A bare `&` (`A & B`, `Q&A`, a query string) is not an entity reference and is not blocked; its rewrite to `&amp;` renders identically and is a style change under decision 3.

`blockers` carries stable keys (`html`, `frontmatter`, `footnote`, `html-entity`, `mixed-eol`, `image-in-mark`, `bracketed-url`) for the toolbar message; the function stays pure and synchronous.

### 2.2 Schema widening

Add `@tiptap/extension-table` (`TableKit`, which bundles table/row/cell/header) and `@tiptap/extension-task-list` + `@tiptap/extension-task-item`, **pinned to 3.22.3** to match the installed `@tiptap/*` line (3.30.x installs an incompatible `@tiptap/extension-list` peer), to the editor extension set, so tables and task lists survive parse **and** are editable.

The markdown bridge requirement raised in review is **verified, not assumed** — see the Evidence section: the extensions carry their own `parseMarkdown` / `renderMarkdown` and `MarkdownManager` registers them straight from the extension array. No custom tokenizer or handler work is in scope.

Minimum table editing affordance in Live Mode: a small contextual control (bubble menu on a table selection) offering add/remove row, add/remove column, delete table. Without it a table renders but cannot be maintained, which is worse than raw. Tab/Shift-Tab cell navigation comes from the extension.

**Images** (added after adversarial review measured the failure): StarterKit carries no image node, so `![alt](a.png)` round-trips to the bare text `alt` — the URL is gone. Since the whitelist declares `image` safe, this was a silent-corruption hole. The schema must therefore also include an image extension; whatever an image extension cannot represent losslessly becomes a gate blocker instead. Dropping `image` from the whitelist without adding the node was rejected: images are common enough in markdown that it would push a large share of documents out of Live Mode, defeating decision 1.

Measured with `@tiptap/extension-image@3.22.3` added to the extension array, real editor, `parse → getMarkdown`:

| input | outcome |
|---|---|
| `![alt](a.png)` | identical |
| `![alt](a.png "title")` | identical — the **title attribute survives** |
| `![](a.png)` | identical |
| image mid-paragraph / in a list item / in a table cell / in a blockquote / in a heading | identical |
| `![alt](a.png 'title')` | `![alt](a.png "title")` — quote style only, accepted under decision 3 |
| `![alt][ref]` + `[ref]: a.png` | inlined to `![alt](a.png)`, same as `def` for links — accepted |
| `[![Build](b.svg)](https://ci)` | `![Build](b.svg)` — **the link is gone** |
| `[text ![a](a.png)](u)` | `[text ](u)![a](a.png)[` — **broken output** |
| `**![alt](a.png)**`, `*…*`, `~~…~~` | the mark is dropped |
| `![alt](<a b.png>)` | `![alt](a b.png)` — **no longer an image** to a CommonMark renderer |
| `![alt](<a.png>)` | `![alt](a.png)` — brackets dropped, but nothing changes without a space |

The two failing families become blockers rather than silent rewrites:
- **`image-in-mark`** — Tiptap models link / strong / em / del as ProseMirror *marks*, and a mark cannot wrap a node, so an image under one loses it.
- **`bracketed-url`** — an angle-bracketed destination whose URL contains a space. Links share the defect (`[text](<a b.html>)` → `[text](a b.html)`), so the rule covers both; a bracketed URL without a space is not blocked, because unwrapping it changes nothing.

Task items render as real checkboxes and round-trip as `- [ ]` / `- [x]`.

### 2.3 Mode resolution with the gate

`EditorPane`'s mode resolution (`EditorPane.tsx:140-147`) gains one rule ahead of the markdown default:

- Markdown + `assessMarkdownRoundTrip(content).safe === false` → resolve **raw**, and surface why in the toolbar (a small badge naming the blocker, e.g. "raw — contains raw HTML").
- The user may still switch to Live Mode manually; that choice is explicit (`paneState.editorMode`) and wins, exactly as today.
- The assessment runs on the loaded content once per buffer load (memoized on buffer key + content identity), not per render.

### 2.4 Line endings and trailing newline

Correction from review: the existing `eol` field is **not** a record of the original file — `normalizeMetadata` recomputes it from the current content on every `updateContent` / `reloadBuffer` (`useEditorStore.ts:148`, `:242`), so it cannot serve as the restore target.

The buffer therefore gains two **immutable-after-load** fields, set only by `openBuffer` / `reloadBuffer` (never by `updateContent`):

```ts
sourceEol: 'lf' | 'crlf'          // detected from the bytes as loaded
sourceTrailingNewline: boolean    // did the loaded content end with a newline
```

A pure helper re-applies them to markdown coming out of Tiptap:

- `sourceEol === 'crlf'` → convert serialized `\n` back to `\r\n`.
- `sourceTrailingNewline` → ensure exactly one trailing newline; if false, ensure none.
- **Leading newline** (found while measuring 2.2a): a document that starts with a table serializes with a leading `\n`. The source never had it, so strip a leading blank line that the source did not have — the trailing-newline rule alone does not cover this.
- **Blank-only files** (found by the adversarial review): a file made of nothing but newlines serializes to the empty string, so `sourceLeadingBlankLines` + `sourceTrailingNewline` are the whole of what can rebuild it. Reporting 0 leading blank lines for every such file — the original behaviour — collapsed `\n\n` to `\n` and `\r\n\r\n` to `\r\n`. The count there is **one short of the newlines present**, because the last one is what `sourceTrailingNewline` already stands for.

Applied in the Tiptap `onChange` path (`EditorPane.tsx:495`) before `updateContent`, so `isDirty` compares like with like and an untouched round-trip of an already-canonical file produces **no** spurious diff.

**Scope limit (explicit)**: this normalization covers the Live Mode path only. Monaco is not a markdown serializer — it hands back exactly the text in its model, preserving both CRLF and the trailing newline — so the raw path needs no normalization and none is specified. Save (`TextEncoder().encode(buf.content)`) stays a byte-faithful write of whatever the buffer holds; the fix belongs at the point where the serializer produces text, not at the write.

### Testing (Phase 2)

- `assessMarkdownRoundTrip` unit table: tables/task lists → safe (after 2.2); raw HTML, front matter, footnote → unsafe with the right blocker key; plain prose → safe; unknown/new syntax → unsafe (default-deny).
- Regression guards for the review findings: a document containing a legitimate `---` horizontal rule followed by a heading is **safe** (not misread as front matter); a reference-style link definition is **safe** (`def` whitelisted); a task list is safe via `list_item.task`, not via a non-existent `taskItem` token.
- A markdown buffer whose content has raw HTML opens in **raw** mode even with no explicit user choice; an explicit `editorMode: 'wysiwyg'` still wins.
- Table markdown survives `parse → serialize` with the table extension present (the exact case that returned `""` today).
- Task list `- [ ]` / `- [x]` round-trips.
- CRLF file: serialized output is CRLF; LF file stays LF; trailing-newline presence/absence is preserved in both directions.
- A file that is already in canonical form and is opened in Live Mode without editing produces no dirty state.

---

## Phase 3 — Recent files remap + save toast

### 3.1 Recent-files remapping

`useRecentFilesStore` gains two mutations:

```ts
renamePath(source: FileSource, from: string, to: string): void  // entry at `from`, and every `from/`-prefixed descendant
removePath(source: FileSource, path: string): void              // the entry and its descendants
```

Semantics mirror `remapPanesUnder` (`storage-actions.ts:57`): same source identity (including `hostId` for daemon), exact match or `from/` prefix, `name` recomputed from the new basename.

Collision rule (made explicit after review): when the destination path already has an entry, the two are merged into one entry at the destination keeping **the newer `openedAt` of the two** — the destination genuinely was visited at that time, and a rename must not resurrect a stale entry above more recent ones. The renamed entry's own `openedAt` is otherwise carried over unchanged (a rename is not a visit). The merged entry keeps the **renamed** entry's other fields (`kind`, `source`) — after the rename it is the file living at that path.

List order is the array's own order (`addRecent` prepends), not a re-derivation from `openedAt`; a merge therefore keeps the earlier — i.e. more recent — of the two slots. (Corrected during implementation: an earlier draft of this spec claimed order was derived from `openedAt`, which does not match the store.)

Call sites:
- `remapPanesUnder` (covers Storage rename **and** move, file and folder — `renameStorageEntry:344`, `moveStorageEntry:399`).
- `EditorPane.handleRenameSubmit` (in-editor rename), which also covers remote renames since it goes through the resolved backend.
- Storage delete (`deleteStorageEntries:468`) → `removePath` per deleted path.
- `saveUntitledBuffer` needs no remap: an unsaved untitled buffer never entered the list (`record-recent-file.ts:21`); its first save records the real path directly.

### 3.2 Save result toast

`handleSave` (`EditorPane.tsx:290`) currently returns silently when there is nothing to save and only `console.error`s on failure. It gains three outcomes, all via `useUndoToast.show`:

| outcome | message |
|---|---|
| saved | `editor.save.saved` — "Saved <name>" |
| nothing to save | `editor.save.unchanged` — "No changes to save" |
| failed | `editor.save.failed` — "Save failed: <reason>" |

Triggered identically by ⌘S and the toolbar button. The untitled → rename-popover path is not a save outcome and shows no toast. i18n keys added to `en.json` and `zh-TW.json`.

### Testing (Phase 3)

- Rename a file: the recent entry follows to the new path and name; opening it works.
- Rename a folder: every recent entry under it is remapped.
- Cross-host isolation: an entry for the same path on another host is untouched.
- Delete removes the entry (and descendants for a folder).
- Save on a dirty buffer → saved toast; save on a clean buffer → unchanged toast (and no write); a rejected write → failed toast carrying the reason.

---

## Phase 4 — Storage operations

### 4.1 Per-row hover actions

Each Storage row exposes, on hover/focus, icon buttons: **Open** (files only), **Rename**, **Delete**. They act on that row regardless of the current selection, and are keyboard reachable (visible on `:focus-within`, not hover-only). They reuse the existing `renameStorageEntry` / `deleteStorageEntries` actions and the existing rename popover.

### 4.2 Manual empty-file cleanup

A toolbar action that scans the in-app tree for 0 B files, shows the list in a confirmation dialog, and deletes the confirmed set in one pass. Folders and non-empty files are never candidates. Reports the count in a toast. This is what clears the existing backlog.

(Automatic cleanup moved to its own phase — see Phase 5 — because it carries deletion semantics the current state model cannot express safely.)

### 4.3 Visible batch selection

Multi-select already exists (`StoragePane.tsx:114`, cmd/ctrl/shift) but is undiscoverable. Add:

- A checkbox column (per row) plus a header select-all checkbox, kept in sync with the existing `selected` set — the modifier-click behaviour is preserved, not replaced.
- A selection action bar shown when `selected.size > 0`: "N selected" plus **Delete** and **Clear selection**, wired to the existing batch `deleteStorageEntries`.

### Testing (Phase 4)

- Row actions operate on the hovered row even when a different row is selected; rename opens the popover anchored to that row; delete removes only that entry.
- Empty-file scan lists exactly the 0 B files (no folders, no non-empty files), deletes only what was confirmed, and reports the count.
- Checkbox selection and modifier-click selection converge on the same `selected` set; select-all selects every visible row; the action bar's Delete removes all selected entries.

---

## Phase 5 — Automatic placeholder cleanup

Split out of Phase 4 after review: deleting files automatically needs a durable "this is an untouched placeholder" fact, which the buffer state does not currently carry.

### 5.1 Explicit placeholder marking

The eager reservation paths that mint a real empty file (`EditorNewTabSection.createFile`, `EditorPane`'s new-buffer action, `createStorageFile`) record the reserved path in a small persisted **placeholder registry** (in-app source only). An entry is removed — permanently, the file is now the user's — on the **first** of:

- any successful write to that path (save, including a save of empty content),
- a rename or move of that path,
- an explicit user delete (the entry is simply dropped with the file).

Rejected alternative (from review): inferring "never written" from `savedContent === '' && !isDirty && lastStat.size === 0`. `markSaved` overwrites `savedContent` with whatever was saved, so a file that had content, was deliberately emptied, and saved would satisfy that predicate and be deleted. The registry records the fact instead of guessing it.

### 5.2 Cleanup trigger

A placeholder is deleted when it is both **still a placeholder** and **no longer open anywhere**:

- The check runs after a pane detaches from the buffer, but the deciding condition is that **no `paneState` in the editor store still references that `bufferKey`** — not that a particular component unmounted. `EditorPane`'s cleanup fires on any unmount of that leaf, including pane moves and content swaps where another pane still holds the same buffer (`pane-move.test.ts:296` shows the source pane unmounting first), so unmount alone must never authorize a delete.
- Only in-app sources are eligible. Remote and local files are never auto-deleted, ever.
- Deletion failures are swallowed (best-effort housekeeping); a leftover file remains reachable through the manual cleanup in 4.2.

### Testing (Phase 5)

- A newly reserved file that is opened and closed without typing is deleted; the registry entry goes with it.
- A reserved file that received any save — **including saving empty content** — is never auto-deleted.
- A reserved file that was renamed is never auto-deleted (under either name).
- Pane move / content swap: the buffer stays referenced by another pane → no deletion; once the last reference goes, deletion proceeds.
- Two panes open on the same placeholder: closing one deletes nothing; closing the second deletes.
- Remote and local files with identical characteristics are never touched.

---

## Review outcomes (codex, spec round 1 — `task-msyaokms-9k2q12`)

All five blockers are resolved in the text above: 1.2 narrowed to untitled-only empty buffers; 2.1 given regex detectors for front matter/footnote plus corrected marked v17 token names; 2.2's markdown-bridge precondition empirically verified and version-pinned; 2.4 given immutable `sourceEol` / `sourceTrailingNewline` fields after confirming `eol` is recomputed on every update; 4.2's automatic half rewritten as Phase 5 on an explicit placeholder registry.

Also adopted: Phase 2 stays a single review/PR unit (its four parts are mutually dependent); Phase 3's collision rule now names which `openedAt` survives; testing for Phase 1 covers the preview panes, not just the editor.

Deferred to a follow-up issue: a Storage **restore** rewrites the tree wholesale and can leave recent-file entries pointing at paths that no longer exist. Out of scope here (restore has no per-path rename/delete events to hook); tracked separately.

## Non-goals / accepted tradeoffs

- Style-level markdown rewrites remain (decision 3). Files that were authored with `_em_` or `*` bullets will show a whole-file diff after their first Live Mode edit; tables are re-padded and gain surrounding blank lines.
- Front matter and raw HTML keep documents out of Live Mode rather than being modelled. If that proves too restrictive in practice, passthrough nodes are the follow-up.
- The 10 MB remote read cap stays; it now surfaces as a load error instead of an empty buffer, which is the actual fix.
- Auto-cleanup acts only on files it created and the user never touched; an empty file the user explicitly saved is the user's file and is preserved.
- Scope of the EOL/trailing-newline fix is the Live Mode serializer only; the raw (Monaco) path is byte-faithful already.
