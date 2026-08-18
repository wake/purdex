// spa/src/components/editor/hooks/useEditorSaveFlow.ts
//
// Owns everything about GETTING the buffer back onto disk: the ordinary save, the
// first save of an untitled document, the outcome toast every attempt raises
// (T3.3) and the Save-button anchor the keyboard path falls back to.
import { useCallback, useRef } from 'react'
import { useEditorStore } from '../../../stores/useEditorStore'
import { useTabStore } from '../../../stores/useTabStore'
import { useUndoToast } from '../../../stores/useUndoToast'
import { getFsBackend } from '../../../lib/fs-backend'
import { bufferKey } from '../../../lib/editor-buffer-key'
import { recordRecentFile } from '../../../lib/recent-files/record-recent-file'
import { createMetadata, untitledStoragePath, untitledSuggestedName } from '../../../lib/editor-language'
import { fileName, isInvalidRename } from '../editor-pane-naming'
import type { RenamePopoverControls } from './useRenamePopoverState'
import type { FileSource } from '../../../types/fs'
import type { UntitledDocumentState } from '../../../types/tab'

// Spec 3.2 (T3.3): the failure toast has to say *why* the save failed, so the
// reason is extracted from whatever the backend rejected with.
function saveErrorReason(error: unknown): string {
  if (error instanceof Error && error.message) return error.message
  return String(error)
}

/**
 * Post-write stat, demoted to a best-effort refresh of `lastStat`.
 *
 * A successful `write` means the bytes are on disk — the save has happened. The
 * follow-up `stat` only exists to refresh the external-change baseline, so a
 * rejection there (flaky link, host just removed, permissions) must NOT be
 * reported as a failed save: the user would be told nothing was written while
 * the file on disk already carries their edits, and the buffer would stay dirty
 * (or, for an untitled document, stay unnamed and out of the recent list).
 *
 * `undefined` is a safe value for `markSaved`, which falls back to the buffer's
 * existing `lastStat`.
 */
async function readStatAfterWrite(
  backend: { stat: (path: string) => Promise<{ mtime: number; size: number }> },
  path: string,
): Promise<{ mtime: number; size: number } | undefined> {
  try {
    const stat = await backend.stat(path)
    return { mtime: stat.mtime, size: stat.size }
  } catch {
    return undefined
  }
}

export interface EditorSaveFlowArgs {
  key: string
  source: FileSource
  filePath: string
  paneId: string
  untitled?: UntitledDocumentState
  popover: RenamePopoverControls
  t: (key: string, params?: Record<string, string>) => string
}

export interface EditorSaveFlow {
  handleSave: (anchorRect?: DOMRect) => Promise<void>
  /** Exposed for the rename flow, whose `save` mode delegates the first save here. */
  saveUntitledBuffer: (name: string) => Promise<void>
  /**
   * Fallback anchor for the naming popover. Whether the popover is NEEDED is a
   * property of the buffer; WHERE it hangs is a property of the UI, and the two
   * used to be conflated — a save that arrived without a rect (the keyboard
   * path) was dropped entirely instead of anchoring itself to the Save button.
   */
  saveButtonRef: React.MutableRefObject<HTMLButtonElement | null>
}

export function useEditorSaveFlow({
  key,
  source,
  filePath,
  paneId,
  untitled,
  popover,
  t,
}: EditorSaveFlowArgs): EditorSaveFlow {
  const saveButtonRef = useRef<HTMLButtonElement | null>(null)

  // T3.3: one place that turns a save attempt into user-visible feedback. It
  // reuses the existing bottom-centre toast (`useUndoToast` / `GlobalUndoToast`)
  // rather than adding a second notification surface.
  const showSaveToast = useCallback((messageKey: string, params?: Record<string, string>) => {
    useUndoToast.getState().show(t(messageKey, params))
  }, [t])

  const saveUntitledBuffer = useCallback(async (name: string) => {
    const buf = useEditorStore.getState().buffers[key]
    const backend = getFsBackend(source)
    // No buffer / not an untitled pane are preconditions, not outcomes: there is
    // no document to report on.
    if (!buf || !untitled) return
    // A missing backend IS an outcome, and the same one T3.3-4b reports on the
    // ordinary save path. Returning silently here left the user believing the
    // first save of a new document had landed when nothing was written at all.
    if (!backend) {
      showSaveToast('editor.save.failed', { reason: t('editor.load_error.no_backend') })
      return
    }

    const trimmedName = name.trim()
    if (isInvalidRename(trimmedName)) {
      popover.setWarning('Invalid file name')
      return
    }

    const nextPath = untitledStoragePath(trimmedName)
    const nextKey = bufferKey(source, nextPath)
    if (nextKey !== key && useEditorStore.getState().buffers[nextKey]) {
      popover.setWarning('File already exists')
      return
    }
    // An OPEN buffer is not the only way the name can be taken: the file may
    // already sit on the backend with nothing open on it, and the write below
    // is a blind overwrite. Probe the backend the same way `handleRenameSubmit`
    // does — a successful stat means the path is occupied and the first save of
    // this document must NOT clobber it.
    try {
      await backend.stat(nextPath)
      popover.setWarning('File already exists')
      return
    } catch {
      // Missing target is the expected, writable case.
    }

    // The WRITE decides the outcome — see `readStatAfterWrite`.
    try {
      await backend.write(nextPath, new TextEncoder().encode(buf.content))
    } catch (err) {
      showSaveToast('editor.save.failed', { reason: saveErrorReason(err) })
      return
    }

    const newStat = await readStatAfterWrite(backend, nextPath)
    const nextMetadata = buf.languageSource === 'manual'
      ? { language: buf.language, languageSource: 'manual' as const, untitled: undefined }
      : { ...createMetadata(source, nextPath), untitled: undefined }
    useTabStore.getState().renameEditorPanes(source, filePath, nextPath)
    useEditorStore.getState().renameBuffer(key, nextKey, nextMetadata)
    useEditorStore.getState().markSaved(nextKey, newStat)
    recordRecentFile({ kind: 'editor', source, filePath: nextPath })
    useEditorStore.getState().setShowDiff(paneId, false)
    popover.close()
    // Confirming the name IS a save outcome (the file now exists on disk), so
    // it reports like any other save. Only *opening* the popover is silent.
    showSaveToast('editor.save.saved', { name: fileName(nextPath) })
  }, [filePath, key, paneId, popover, showSaveToast, source, t, untitled])

  const handleSave = useCallback(async (anchorRect?: DOMRect) => {
    const buf = useEditorStore.getState().buffers[key]
    // No buffer: the pane is showing the loading / load-error surface, so there
    // is no document and no save attempt to report.
    if (!buf) return
    // T3.3: an untouched, already-saved buffer is an explicit "nothing to do"
    // outcome. This branch used to swallow every ⌘S without a trace, which is
    // what made the key feel dead.
    if (!buf.isDirty && buf.lastStat) {
      showSaveToast('editor.save.unchanged')
      return
    }
    if (buf.untitled) {
      if (!buf.untitled.hasBeenRenamed) {
        // Opening the name popover is not a save outcome — no toast here; the
        // one that follows the user's confirmation comes from saveUntitledBuffer.
        //
        // The editors (Monaco / Tiptap) call `onSave()` with no rect, so falling
        // back to the Save button's own rect is what keeps ⌘S from being a
        // no-op. The button is rendered by this same component whenever a buffer
        // exists — i.e. whenever this branch is reachable — so the ref is set.
        const anchor = anchorRect ?? saveButtonRef.current?.getBoundingClientRect()
        if (!anchor) return
        popover.openSave(anchor, untitledSuggestedName(buf.untitled))
        return
      }
      await saveUntitledBuffer(buf.untitled.name)
      return
    }

    const backend = getFsBackend(source)
    // An unresolvable backend is a failed save, not a no-op: without this the
    // content silently stays unsaved (same silent-failure class as 1.2b).
    if (!backend) {
      showSaveToast('editor.save.failed', { reason: t('editor.load_error.no_backend') })
      return
    }
    // Only the WRITE decides success/failure (see `readStatAfterWrite`).
    try {
      await backend.write(filePath, new TextEncoder().encode(buf.content))
    } catch (err) {
      // Replaces a console.error the user could never see; the buffer stays
      // dirty and unmarked, so the toast is the only signal they get.
      showSaveToast('editor.save.failed', { reason: saveErrorReason(err) })
      return
    }

    const newStat = await readStatAfterWrite(backend, filePath)
    useEditorStore.getState().markSaved(key, newStat)
    recordRecentFile({ kind: 'editor', source, filePath })
    useEditorStore.getState().setShowDiff(paneId, false)
    showSaveToast('editor.save.saved', { name: fileName(filePath) })
  }, [filePath, key, paneId, popover, saveUntitledBuffer, showSaveToast, source, t])

  return { handleSave, saveUntitledBuffer, saveButtonRef }
}
