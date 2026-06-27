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
 * Folder mkdir / rename / recursive move / the unified `createUniqueInAppFile`
 * namer are explicitly deferred to Phase 1b — new-file here still lands flat at
 * the storage root (acceptable until 1b introduces folders).
 */
import { getFsBackend } from '../../../lib/fs-backend'
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

// v1.5 G1 fix — mirror EditorPane's rename three-step sync (backend →
// tab-layout → editor-store). Without this, renaming via the storage pane
// leaves the editor store keyed by the old path: a subsequent Save would write
// to the stale filename and a re-open would resurrect a ghost buffer. The
// `renameBuffer` metadata argument also refreshes language + languageSource
// when crossing file extensions (preserve a manual override, otherwise
// recompute from the new path — same contract as `EditorPane.handleRenameSubmit`).
export async function performBufferRename(fromPath: string, targetPath: string) {
  const backend = getFsBackend({ type: 'inapp' })
  if (!backend) throw new Error('InApp backend unavailable')
  await backend.rename(fromPath, targetPath)
  const source: FileSource = { type: 'inapp' }
  useTabStore.getState().renameEditorPanes(source, fromPath, targetPath)
  const oldKey = bufferKey(source, fromPath)
  const newKey = bufferKey(source, targetPath)
  const currentBuffer = useEditorStore.getState().buffers[oldKey]
  const nextMetadata = currentBuffer?.languageSource === 'manual'
    ? { language: currentBuffer.language, languageSource: 'manual' as const }
    : createMetadata(source, targetPath)
  useEditorStore.getState().renameBuffer(oldKey, newKey, nextMetadata)
}

/** Create a new empty markdown file at the storage root. Flat-only in 1a. */
export async function createStorageFile(): Promise<{ error?: string }> {
  const backend = getFsBackend({ type: 'inapp' })
  if (!backend) return {}
  const path = join(STORAGE_ROOT, `Untitled-${Date.now()}.md`)
  try {
    await backend.write(path, new Uint8Array(0))
    return {}
  } catch (err) {
    return { error: err instanceof Error ? err.message : String(err) }
  }
}

export type RenameOutcome =
  | { ok: true }
  | { kind: 'exists' }
  | { kind: 'error'; message: string }

/**
 * Rename a leaf file identified by its **full path**. `newName` is a basename;
 * the file stays in its own directory (`parentOf(fromPath)`). A `stat` pre-check
 * aborts before any backend mutation if the destination already exists (F4 —
 * `InAppBackend.rename` is a blind overwrite).
 */
export async function renameStorageEntry(
  fromPath: string,
  newName: string,
): Promise<RenameOutcome> {
  const backend = getFsBackend({ type: 'inapp' })
  if (!backend) return { ok: true }
  const targetPath = join(parentOf(fromPath), newName)
  if (targetPath !== fromPath) {
    const exists = await backend
      .stat(targetPath)
      .then(() => true)
      .catch(() => false)
    if (exists) return { kind: 'exists' }
  }
  try {
    await performBufferRename(fromPath, targetPath)
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
