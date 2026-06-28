/**
 * storage-actions — the CRUD handlers for the In-App Storage pane, extracted
 * from the old monolithic `EditorBuffersPane` so the pane shell stays a thin
 * view layer.
 *
 * Phase 1a contract (spec §3.5 / plan T5b "CRUD scope in 1a"): rename + delete
 * are **full-path aware for leaf files** — they no longer key on a basename and
 * a flat `/buffer` root. Rename keeps the file in its own directory
 * (`parentOf(fromPath)`), delete removes the exact selected paths. The
 * dirty-pane confirm + locked-tab refusal guards are preserved verbatim.
 *
 * Phase 1b: new-file now goes through the unified eager `createUniqueInAppFile`
 * namer (atomic IDB reservation, #854) and accepts a `targetDir`. Folder mkdir
 * (`createStorageFolder`) and in-place rename of files AND folders
 * (`renameStorageEntry` + the pure `remapPanesUnder` re-point) have landed.
 * The recursive move data layer (`moveStorageEntry`, T1b-6a) reuses that same
 * single-rename + `remapPanesUnder` path; the drag-and-drop wiring is T1b-6b.
 */
import { getFsBackend, supportsCreateUnique, supportsMkdirUnique } from '../../../lib/fs-backend'
import { createUniqueInAppFile } from '../../../lib/inapp-namer'
import { triggerDownload } from '../../../lib/download-file'
import { isQuotaError } from '../../../lib/quota'
import { bufferKey } from '../../../lib/editor-buffer-key'
import { scanPaneTree } from '../../../lib/pane-tree'
import { isFilePaneContent } from '../../../lib/pane-utils'
import { useEditorStore } from '../../../stores/useEditorStore'
import { useTabStore } from '../../../stores/useTabStore'
import { createMetadata } from '../../../lib/editor-language'
import { STORAGE_ROOT, basename, join, parentOf, isUnderRoot } from '../../../lib/storage-paths'
import type { FileSource } from '../../../types/fs'
import type { Pane, Tab } from '../../../types/tab'

export type Translate = (key: string, params?: Record<string, string | number>) => string

/**
 * remapPanesUnder — PURE pane + buffer re-point for a rename/move that has
 * ALREADY happened at the backend layer. It performs NO backend mutation: the
 * single `backend.rename` lives in the caller (`renameStorageEntry`, and the
 * future `moveStorageEntry`) and runs exactly once BEFORE this helper, so file
 * and folder share one code path and there is no double-rename.
 *
 * It enumerates every open file pane — editor + image-preview + pdf-preview —
 * whose `content.filePath` is `from` itself OR a `from/`-prefixed descendant
 * (the trailing slash stops `/buffer/a` from matching `/buffer/ab`, decision 6),
 * then for each affected path:
 *   - re-points the tab layout via `renameEditorPanes(source, oldPath, newPath)`
 *     (which already rewrites all three file-pane kinds), and
 *   - for editor panes only, re-keys the editor-store buffer via
 *     `renameBuffer(oldKey, newKey, metadata)`. The metadata mirrors the old
 *     single-path `performBufferRename` / `EditorPane.handleRenameSubmit`:
 *     preserve a manual language override, otherwise recompute from the new
 *     path so crossing extensions refreshes the language.
 *
 * A single-file rename is the one-iteration case (`from === filePath`, so
 * `newPath = to`); a folder rename iterates every open descendant.
 */
export function remapPanesUnder(source: FileSource, from: string, to: string): void {
  const fromPrefix = from + '/'
  // Collect each affected open path once, tracking whether any pane on that path
  // is an editor (→ it also needs an editor-store buffer re-key, not just a
  // layout re-point).
  const affected = new Map<string, boolean>()
  const { tabs } = useTabStore.getState()
  for (const tab of Object.values(tabs)) {
    scanPaneTree(tab.layout, (pane) => {
      const c = pane.content
      if (!isFilePaneContent(c)) return
      if (c.source.type !== source.type) return
      if (c.source.type === 'daemon' && source.type === 'daemon' && c.source.hostId !== source.hostId) return
      if (c.filePath === from || c.filePath.startsWith(fromPrefix)) {
        affected.set(c.filePath, (affected.get(c.filePath) ?? false) || c.kind === 'editor')
      }
    })
  }

  for (const [oldPath, hasEditor] of affected) {
    const newPath = to + oldPath.slice(from.length)
    useTabStore.getState().renameEditorPanes(source, oldPath, newPath)
    if (!hasEditor) continue
    const oldKey = bufferKey(source, oldPath)
    const newKey = bufferKey(source, newPath)
    const currentBuffer = useEditorStore.getState().buffers[oldKey]
    const nextMetadata = currentBuffer?.languageSource === 'manual'
      ? { language: currentBuffer.language, languageSource: 'manual' as const }
      : createMetadata(source, newPath)
    useEditorStore.getState().renameBuffer(oldKey, newKey, nextMetadata)
  }
}

/**
 * Create a new empty markdown file under `targetDir` (defaults to the storage
 * root). Uses the unified eager `createUniqueInAppFile` namer — the atomic IDB
 * `add` reservation that gives a collision-free `Untitled[-N].md` even under a
 * rapid double-click (#854), replacing the old `Date.now()` suffix. A missing
 * backend surfaces as an error (codex R3) so handleNew shows the banner instead
 * of refreshing as if a file were created.
 */
export async function createStorageFile(targetDir: string = STORAGE_ROOT): Promise<{ error?: string }> {
  try {
    await createUniqueInAppFile(targetDir, 'md')
    return {}
  } catch (err) {
    return { error: err instanceof Error ? err.message : String(err) }
  }
}

/**
 * Create a new empty folder under `targetDir` (defaults to the storage root)
 * via the atomic `mkdirUnique` reservation (decision 7), giving a collision-free
 * `New Folder[ N]` even under a rapid double-click. Returns the reserved path or
 * an error (a missing backend is a failure, not a silent success — symmetric
 * with `createStorageFile`).
 */
export async function createStorageFolder(
  targetDir: string = STORAGE_ROOT,
): Promise<{ path: string } | { error: string }> {
  const backend = getFsBackend({ type: 'inapp' })
  // Narrow to the mkdir-unique capability (codex H1) — it lives on the optional
  // `SupportsUniqueCreate`, not the base `FsBackend`.
  if (!supportsMkdirUnique(backend)) return { error: 'InApp backend unavailable' }
  try {
    const path = await backend.mkdirUnique(targetDir)
    return { path }
  } catch (err) {
    return { error: err instanceof Error ? err.message : String(err) }
  }
}

/**
 * Download (export) a single stored file to the OS via the shared
 * `triggerDownload` util (Phase 1c T1c-2). Reads the exact bytes from the
 * backend and hands them off as a `Blob` named by the file's basename, so the
 * saved file is byte-identical to what is stored. A missing backend, a
 * directory path, or a missing file all surface as `{ error }` (never a silent
 * no-op) and never reach `triggerDownload`. Folder downloads are out of scope in
 * 1c — the caller disables the button for a folder selection.
 */
export async function downloadStorageFile(
  path: string,
): Promise<{ ok: true } | { error: string }> {
  const backend = getFsBackend({ type: 'inapp' })
  if (!backend) return { error: 'InApp backend unavailable' }
  try {
    const bytes = await backend.read(path)
    // Re-wrap into a fresh ArrayBuffer-backed view so the Blob part is a plain
    // `Uint8Array<ArrayBuffer>` (the backend's `Uint8Array<ArrayBufferLike>`
    // does not satisfy `BlobPart` under strict TS) — mirrors Image/PdfPreview.
    triggerDownload(new Blob([new Uint8Array(bytes)]), basename(path))
    return { ok: true }
  } catch (err) {
    return { error: err instanceof Error ? err.message : String(err) }
  }
}

/**
 * Split a file's basename into `(baseName, ext)` for `createUnique`, matching
 * the path-forming rule there (`ext === ''` → no trailing dot). The extension is
 * everything after the LAST dot; an ext-less name (`README`) and a dotfile
 * (`.env`, a leading dot at index 0) both yield `ext = ''` so the whole name is
 * preserved. `archive.tar.gz` → (`archive.tar`, `gz`).
 *
 * A name whose LAST dot is the final character (`foo.`) keeps the trailing dot
 * as part of the basename (`{ baseName: 'foo.', ext: '' }`) rather than silently
 * collapsing to `foo` (C1) — the stored name must never be rewritten to a
 * different one behind the user's back.
 */
function splitFileName(fileName: string): { baseName: string; ext: string } {
  const dot = fileName.lastIndexOf('.')
  if (dot <= 0 || dot === fileName.length - 1) return { baseName: fileName, ext: '' }
  return { baseName: fileName.slice(0, dot), ext: fileName.slice(dot + 1) }
}

/**
 * Sanitise an OS-provided `file.name` into a safe single-segment basename, or
 * `null` when the name cannot yield a valid entry (C1 — path-traversal / hidden
 * data). The browser File API hands back whatever the OS reported, which for a
 * dropped folder or a crafted archive entry can contain path separators
 * (`sub/evil.txt`, `..\\x`), a traversal token (`..`), control characters, or be
 * blank — any of which, fed straight into `createUnique` + `join`, could write
 * OUTSIDE the selected dir or to a key the tree never surfaces.
 *
 * Steps: take the BASENAME only (drop every `/`- or `\\`-delimited directory
 * component, so an OS folder structure is flattened into the target dir rather
 * than recreated), strip control characters, trim surrounding whitespace, then
 * reject an empty or dot-only result (`''`, `.`, `..`, `...`).
 */
function sanitizeUploadName(rawName: string): string | null {
  // Basename only: a folder structure in the dropped name is flattened, and a
  // `../` traversal loses its separators so it can never escape `targetDir`.
  const base = rawName.split(/[/\\]/).pop() ?? ''
  // Drop NUL + other C0 control characters that have no place in a file name.
  // eslint-disable-next-line no-control-regex -- intentional C0 control-char strip
  const stripped = base.replace(/[\u0000-\u001f]/g, '').trim()
  // Empty (incl. a pure-whitespace or pure-control name) or a dot-only token
  // (`.` / `..` / `...`) is not a usable file name.
  if (stripped === '' || /^\.+$/.test(stripped)) return null
  return stripped
}

/**
 * Soft upper bound on a single uploaded file (~25 MB, T1c-4 decision 5). A file
 * over the cap is rejected BEFORE any write (no `createUnique`, no byte read into
 * IDB), surfacing as a `tooLarge` result rather than letting a large blob bloat
 * the In-App store. The cap is "soft": it is enforced per-file at ingest, not a
 * hard quota — the genuine IDB quota is caught separately at write time.
 */
export const SOFT_MAX_UPLOAD_BYTES = 25 * 1024 * 1024

/**
 * Outcome of a single `uploadFile`, a TYPED discriminated union (codex R2 C4):
 * the failure variants carry their own `kind` so `uploadFiles` and the banner
 * branch on a tag rather than string-matching a sentinel message. A `too-large`
 * / `quota` failure already names the (sanitised) file; a generic `error`
 * carries its raw message.
 */
export type UploadResult =
  | { path: string }
  | { kind: 'too-large'; name: string; cap: number }
  | { kind: 'quota'; name: string }
  | { kind: 'error'; name: string; message: string }

/**
 * Ingest a single OS file into `targetDir` (Phase 1c; T1c-1 happy path widened
 * by T1c-4, which owns ALL upload guards). Order of guards:
 *   1. Missing/incapable backend → `{ kind: 'error' }` (never a silent no-op).
 *   2. **Sanitise `file.name`** (C1): flatten to a basename, strip control
 *      characters, reject empty / dot-only / traversal names — so an OS-provided
 *      name carrying `/`, `\\`, or `..` can never write outside `targetDir` or to
 *      an invisible key. A rejected name → `{ kind: 'error' }`.
 *   3. **Size cap before any write** (decision 5): `file.size > SOFT_MAX_UPLOAD_BYTES`
 *      → `{ kind: 'too-large' }`. `createUnique` is NOT called and the bytes are
 *      NOT read — the over-cap file never touches the store.
 *   4. Otherwise parse `(baseName, ext)`, defensively re-check the candidate path
 *      stays under the storage root, read bytes, and reserve a unique key via the
 *      atomic `createUnique(dir, base, ext, bytes)` — unique-name reservation AND
 *      byte write on one `add` (no overwrite race; `-N` suffix on collision,
 *      decision 4). A thrown quota exhaustion is caught via the shared
 *      `isQuotaError` and surfaced as `{ kind: 'quota' }` (never silent); any
 *      other throw surfaces as `{ kind: 'error' }`. Any file type is accepted.
 */
export async function uploadFile(targetDir: string, file: File): Promise<UploadResult> {
  const backend = getFsBackend({ type: 'inapp' })
  // Narrow to the unique-create capability (codex H1) — it lives on the optional
  // `SupportsUniqueCreate`, not the base `FsBackend`.
  if (!supportsCreateUnique(backend)) {
    return { kind: 'error', name: file.name, message: 'InApp backend unavailable' }
  }
  // C1 (security / data-loss): sanitise the OS-provided name BEFORE it forms a
  // key — a name with path separators or a traversal token must never escape the
  // selected dir or land on a key the tree cannot show.
  const safeName = sanitizeUploadName(file.name)
  if (safeName == null) {
    return { kind: 'error', name: file.name, message: `Invalid file name: ${file.name}` }
  }
  // Size cap is checked BEFORE the read+write (decision 5): an over-cap file is
  // rejected without ever reading its bytes or reserving a name.
  if (file.size > SOFT_MAX_UPLOAD_BYTES) {
    return { kind: 'too-large', name: safeName, cap: SOFT_MAX_UPLOAD_BYTES }
  }
  const { baseName, ext } = splitFileName(safeName)
  // Defense-in-depth (C1): every `createUnique` candidate lives directly under
  // `targetDir`, so verifying the un-suffixed path stays under the storage root
  // catches a non-root `targetDir` before any byte is read or written. `join`
  // keeps `..`/`.` as literal segments and `isUnderRoot` is a prefix check, so a
  // `targetDir` carrying an un-normalized traversal token (e.g. `/buffer/..`)
  // could otherwise slip a `/buffer/../x` candidate past the root guard — reject
  // any traversal segment explicitly (codex 1c fix-wave confirm, C1b).
  const candidate = join(targetDir, ext === '' ? baseName : `${baseName}.${ext}`)
  const hasTraversal = candidate.split('/').some((seg) => seg === '..' || seg === '.')
  if (hasTraversal || !isUnderRoot(candidate)) {
    return { kind: 'error', name: file.name, message: `Invalid file name: ${file.name}` }
  }
  try {
    const bytes = new Uint8Array(await file.arrayBuffer())
    const path = await backend.createUnique(targetDir, baseName, ext, bytes)
    return { path }
  } catch (err) {
    // A full store throws a quota DOMException at write time; tag it `quota` so
    // the caller can show a distinct "storage full" banner.
    if (isQuotaError(err)) return { kind: 'quota', name: safeName }
    return { kind: 'error', name: safeName, message: err instanceof Error ? err.message : String(err) }
  }
}

/** Why a single upload failed — lets the UI pick the right banner (T1c-4). */
export type UploadFailureKind = 'too-large' | 'quota' | 'other'

export interface UploadFailure {
  name: string
  reason: string
  kind: UploadFailureKind
  /** The soft cap in bytes — present only for `kind: 'too-large'`. */
  cap?: number
}

export interface UploadSummary {
  uploaded: string[]
  failed: UploadFailure[]
}

/**
 * Ingest multiple OS files into `targetDir`, reporting PARTIAL success (codex R1
 * F5): every uploaded path lands in `uploaded`, and each failure lands in
 * `failed` with a TYPED reason (T1c-4) so the caller can render a too-large
 * warning, a quota error, or a generic partial message — all in one banner
 * rather than aborting on the first error. Files are processed sequentially so
 * the atomic `-N` suffixing stays deterministic for same-named drops.
 */
export async function uploadFiles(targetDir: string, files: File[]): Promise<UploadSummary> {
  const uploaded: string[] = []
  const failed: UploadFailure[] = []
  for (const file of files) {
    const res = await uploadFile(targetDir, file)
    if ('path' in res) {
      uploaded.push(res.path)
    } else if (res.kind === 'too-large') {
      // Read the typed kind straight off the result (codex R2 C4) — no string
      // sentinel round-trip.
      failed.push({ name: res.name, reason: 'too-large', kind: 'too-large', cap: res.cap })
    } else if (res.kind === 'quota') {
      failed.push({ name: res.name, reason: 'quota', kind: 'quota' })
    } else {
      failed.push({ name: res.name, reason: res.message, kind: 'other' })
    }
  }
  return { uploaded, failed }
}

export type RenameOutcome =
  | { ok: true }
  | { kind: 'exists' }
  | { kind: 'error'; message: string }

/**
 * In-place rename of a **file or folder** identified by its full path —
 * one uniform path, no branch, no double-rename (T1b-4). `newName` is a
 * basename; the entry stays in its own directory (`parentOf(fromPath)`). A
 * `stat` pre-check aborts before any backend mutation if the destination
 * already exists (F4). The lone backend mutation is a single
 * `backend.rename` (recursive re-key of folder descendants, T1b-1), followed
 * by the pure `remapPanesUnder` re-point — so a folder rename moves every
 * descendant and re-points every open descendant pane in one shot.
 */
export async function renameStorageEntry(
  fromPath: string,
  newName: string,
): Promise<RenameOutcome> {
  const backend = getFsBackend({ type: 'inapp' })
  // Missing backend = failure, not success (codex R3): returning ok:true would close
  // the rename popover and clear selection as if the rename happened.
  if (!backend) return { kind: 'error', message: 'InApp backend unavailable' }
  const targetPath = join(parentOf(fromPath), newName)
  // Same-name rename is a no-op success (codex B4): if the new basename equals
  // the current one, `targetPath === fromPath`. Short-circuit BEFORE the stat /
  // rename — otherwise `backend.rename(from, from)` runs its collision scan and
  // sees the source itself as a same-path collision, rejecting a confirmation
  // the user meant as "keep the name". Returning ok closes the popover cleanly.
  if (targetPath === fromPath) return { ok: true }
  const exists = await backend
    .stat(targetPath)
    .then(() => true)
    .catch(() => false)
  if (exists) return { kind: 'exists' }
  try {
    // The ONLY backend mutation for rename — exactly once, file and folder alike.
    await backend.rename(fromPath, targetPath)
    remapPanesUnder({ type: 'inapp' }, fromPath, targetPath)
    return { ok: true }
  } catch (err) {
    return { kind: 'error', message: err instanceof Error ? err.message : String(err) }
  }
}

export type MoveOutcome =
  | { ok: true }
  | { kind: 'noop' }
  | { kind: 'exists' }
  | { kind: 'error'; message: string }

/**
 * Move a **file or folder** (`from`) into `targetDir`, keeping its basename —
 * the data layer behind drag-and-drop (T1b-6a; the DnD wiring is T1b-6b). It
 * shares the exact rename machinery: the destination is `targetDir/basename`,
 * and a successful move is one recursive `backend.rename` (folder descendants
 * re-keyed by T1b-1) followed by the pure `remapPanesUnder` re-point (T1b-4) —
 * so a folder move re-points every open descendant pane in one shot.
 *
 * Three branches diverge BEFORE any mutation, so `backend.rename` fires exactly
 * once on the success path and zero times otherwise:
 *   - **no-op** (`{kind:'noop'}`, no `stat`, no mutation) when the move is inert:
 *     dropping onto the current parent (`targetDir === parentOf(from)`, which
 *     also covers `to === from`), onto the entry itself (`targetDir === from`),
 *     or into its own descendant (`targetDir` is a `from/`-prefixed path) — the
 *     last guard stops a folder being moved inside itself (decision 6).
 *   - **collision** (`{kind:'exists'}`) when `stat(to)` resolves — a same-named
 *     entry already lives in the target dir; refused before mutation (F4).
 *   - otherwise the single `backend.rename(from, to)` + `remapPanesUnder`.
 */
export async function moveStorageEntry(
  from: string,
  targetDir: string,
): Promise<MoveOutcome> {
  const backend = getFsBackend({ type: 'inapp' })
  // Missing backend = failure, not success (codex R3): a fake ok would clear the
  // drag selection as if the move had happened.
  if (!backend) return { kind: 'error', message: 'InApp backend unavailable' }

  const to = join(targetDir, basename(from))

  // No-op guards (no stat, no mutation): already there, onto self, or into own
  // descendant. `targetDir === parentOf(from)` subsumes the `to === from` case.
  if (
    targetDir === parentOf(from) ||
    to === from ||
    targetDir === from ||
    targetDir.startsWith(from + '/')
  ) {
    return { kind: 'noop' }
  }

  // Collision pre-check before any backend mutation (F4): a same-named sibling
  // already in the target dir refuses the move.
  const exists = await backend
    .stat(to)
    .then(() => true)
    .catch(() => false)
  if (exists) return { kind: 'exists' }

  try {
    // The ONLY backend mutation for move — exactly once, file and folder alike.
    await backend.rename(from, to)
    remapPanesUnder({ type: 'inapp' }, from, to)
    return { ok: true }
  } catch (err) {
    return { kind: 'error', message: err instanceof Error ? err.message : String(err) }
  }
}

export type DeleteOutcome =
  | { status: 'deleted' }
  | { status: 'cancelled' }
  | { status: 'refused'; message: string }
  | { status: 'error'; message: string }

/**
 * True when `filePath` is one of the deleted `targets` OR a descendant of any
 * target folder. The trailing slash stops `/buffer/a` from matching the sibling
 * `/buffer/ab` (decision 6); the exact-equality branch keeps a file target
 * matching its own open pane. A folder delete therefore drags every open
 * descendant pane through the same locked/dirty guards + close-before-delete
 * sequence as an exact hit.
 */
function isAffectedByTargets(filePath: string, targets: string[]): boolean {
  return targets.some((target) => filePath === target || filePath.startsWith(target + '/'))
}

/**
 * Delete the given **full paths** (files or folders). Preserves the v1.4/v1.5
 * guards, now folder-aware — a folder target sweeps every descendant
 * (recursive) and the guards consider any open DESCENDANT pane affected, not
 * just an exact path match (decision 6, T1b-5):
 *   - F2: refuse outright if any affected editor pane lives in a locked tab.
 *   - F5/F6: dirty-specific confirm wins over single / multi confirm.
 *   - G2: close every affected pane AND drop its editor-store buffer + paneState
 *     BEFORE deleting the underlying file, so a post-delete write can't
 *     resurrect a stale background buffer.
 */
export async function deleteStorageEntries(
  targets: string[],
  t: Translate,
): Promise<DeleteOutcome> {
  if (targets.length === 0) return { status: 'cancelled' }
  const backend = getFsBackend({ type: 'inapp' })
  if (!backend) return { status: 'cancelled' }

  // Normalize the selection (codex B2): drop any target already covered by an
  // ancestor target (`t` is under some other `o`). Selecting a folder AND one of
  // its descendants would otherwise recursively delete the folder first, then
  // re-delete the now-missing descendant — a not-found error AFTER the panes are
  // already closed and the folder partially gone (half-deleted state). The
  // pane-close scan, the confirm count and the delete loop all operate on this
  // normalized set so each surviving target is deleted exactly once.
  const normalized = targets.filter(
    (t) => !targets.some((o) => o !== t && t.startsWith(o + '/')),
  )

  const { tabs } = useTabStore.getState()
  const openPanes: Array<[string, Pane]> = []
  for (const [tabId, tab] of Object.entries(tabs) as Array<[string, Tab]>) {
    scanPaneTree(tab.layout, (pane) => {
      const c = pane.content
      // Cover editor AND the file-preview kinds (image-preview / pdf-preview)
      // so deleting a png/pdf open in a preview pane — or anything nested under
      // a deleted folder — fires the locked-tab refusal, closes the pane, and
      // leaves no stale tab behind.
      if (isFilePaneContent(c) && c.source.type === 'inapp' && isAffectedByTargets(c.filePath, normalized)) {
        openPanes.push([tabId, pane])
      }
    })
  }

  // F2: locked-tab refusal before any mutation.
  const lockedHit = openPanes.some(([tabId]) => tabs[tabId]?.locked)
  if (lockedHit) {
    return { status: 'refused', message: t('editor.buffers.delete_locked_refused') }
  }

  // F5: dirty-specific confirm wins over generic.
  const dirtyHits = openPanes.filter(([, pane]) => {
    const content = pane.content
    if (content.kind !== 'editor') return false
    const key = bufferKey(content.source, content.filePath)
    return useEditorStore.getState().buffers[key]?.isDirty === true
  })

  if (dirtyHits.length > 0) {
    if (!window.confirm(t('editor.buffers.delete_dirty_confirm', { count: dirtyHits.length }))) {
      return { status: 'cancelled' }
    }
  } else if (normalized.length === 1) {
    if (!window.confirm(t('editor.buffers.delete_one_confirm'))) {
      return { status: 'cancelled' }
    }
  } else {
    if (!window.confirm(t('editor.buffers.confirm_delete', { count: normalized.length }))) {
      return { status: 'cancelled' }
    }
  }

  try {
    // Step 1: close every affected pane BEFORE deleting the file (G2).
    for (const [tabId, pane] of openPanes) {
      useTabStore.getState().closePane(tabId, pane.id)
      if (pane.content.kind === 'editor') {
        const key = bufferKey(pane.content.source, pane.content.filePath)
        useEditorStore.getState().closePane(pane.id, key)
      }
    }
    // Step 2: delete each NORMALIZED target. Folders go through the recursive
    // sweep (prefix-delete of every descendant); files use the plain
    // non-recursive delete. A stat failure (e.g. the entry vanished under a
    // concurrent op) is treated as a non-folder and still attempted.
    for (const path of normalized) {
      const isDir = await backend
        .stat(path)
        .then((s) => s.isDirectory)
        .catch(() => false)
      await backend.delete(path, isDir)
    }
    return { status: 'deleted' }
  } catch (err) {
    return { status: 'error', message: err instanceof Error ? err.message : String(err) }
  }
}
