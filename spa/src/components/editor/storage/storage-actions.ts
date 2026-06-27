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
 * (`renameStorageEntry` + the pure `remapPanesUnder` re-point) have landed;
 * recursive move (drag) lands in T1b-6.
 */
import { getFsBackend } from '../../../lib/fs-backend'
import { createUniqueInAppFile } from '../../../lib/inapp-namer'
import { bufferKey } from '../../../lib/editor-buffer-key'
import { scanPaneTree } from '../../../lib/pane-tree'
import { isFilePaneContent } from '../../../lib/pane-utils'
import { useEditorStore } from '../../../stores/useEditorStore'
import { useTabStore } from '../../../stores/useTabStore'
import { createMetadata } from '../../../lib/editor-language'
import { STORAGE_ROOT, join, parentOf } from '../../../lib/storage-paths'
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
  if (!backend) return { error: 'InApp backend unavailable' }
  try {
    const path = await backend.mkdirUnique(targetDir)
    return { path }
  } catch (err) {
    return { error: err instanceof Error ? err.message : String(err) }
  }
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
  if (targetPath !== fromPath) {
    const exists = await backend
      .stat(targetPath)
      .then(() => true)
      .catch(() => false)
    if (exists) return { kind: 'exists' }
  }
  try {
    // The ONLY backend mutation for rename — exactly once, file and folder alike.
    await backend.rename(fromPath, targetPath)
    remapPanesUnder({ type: 'inapp' }, fromPath, targetPath)
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
 * Delete the given **full paths**. Preserves the v1.4/v1.5 guards:
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

  const { tabs } = useTabStore.getState()
  const openPanes: Array<[string, Pane]> = []
  for (const [tabId, tab] of Object.entries(tabs) as Array<[string, Tab]>) {
    scanPaneTree(tab.layout, (pane) => {
      const c = pane.content
      // Cover editor AND the file-preview kinds (image-preview / pdf-preview)
      // so deleting a png/pdf open in a preview pane fires the locked-tab
      // refusal, closes the pane, and leaves no stale tab behind.
      if (isFilePaneContent(c) && c.source.type === 'inapp' && targets.includes(c.filePath)) {
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
  } else if (targets.length === 1) {
    if (!window.confirm(t('editor.buffers.delete_one_confirm'))) {
      return { status: 'cancelled' }
    }
  } else {
    if (!window.confirm(t('editor.buffers.confirm_delete', { count: targets.length }))) {
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
    // Step 2: delete the files.
    for (const path of targets) {
      await backend.delete(path)
    }
    return { status: 'deleted' }
  } catch (err) {
    return { status: 'error', message: err instanceof Error ? err.message : String(err) }
  }
}
