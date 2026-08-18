// spa/src/components/editor/hooks/useEditorPaneLoadState.ts
//
// Owns everything about GETTING a buffer into the store for this pane: the load
// effect, the pane-local failure surface it produces (spec 1.2 / 1.2b) and the
// retry that re-runs it.
import { useCallback, useEffect, useState } from 'react'
import { useEditorStore } from '../../../stores/useEditorStore'
import { getFsBackend } from '../../../lib/fs-backend'
import { createMetadata } from '../../../lib/editor-language'
import type { FileSource } from '../../../types/fs'
import type { UntitledDocumentState } from '../../../types/tab'

// Spec 1.2: a load failure must never degrade into an empty buffer. Extract the
// most specific reason we can so the error surface tells the user *why* the file
// could not be read (empty string → the generic i18n fallback).
function loadErrorMessage(error: unknown): string {
  if (error instanceof Error) return error.message
  if (typeof error === 'string') return error
  return ''
}

/**
 * A pane-local load failure. Two shapes, because the two sources of failure carry
 * different text: `message` is a raw reason handed to us by the backend (already
 * a sentence, not translatable); `messageKey` is an i18n key for failures we
 * diagnose ourselves (spec 1.2b: no FS backend resolved at all). Keeping the key
 * unresolved until render means a locale switch retranslates the surface.
 */
export type LoadError = { message: string; messageKey?: undefined } | { messageKey: string; message?: undefined }

export interface EditorPaneLoadStateArgs {
  key: string
  source: FileSource
  sourceId: string
  filePath: string
  untitled?: UntitledDocumentState
  isUntitled: boolean
}

export interface EditorPaneLoadState {
  /** The failure produced by THIS buffer key, or null. */
  activeLoadError: LoadError | null
  retryLoad: () => void
}

export function useEditorPaneLoadState({
  key,
  source,
  sourceId,
  filePath,
  untitled,
  isUntitled,
}: EditorPaneLoadStateArgs): EditorPaneLoadState {
  // Per-pane load failure (spec 1.2). Local state on purpose: the store never
  // learns about a failed load because no buffer is created for it.
  //
  // Tagged with the buffer key that produced it: a pane outlives a file switch,
  // and the load effect only clears the error AFTER the commit — so an untagged
  // error painted over the next file for a frame, offering a Retry button that
  // would actually retry that next file.
  const [loadError, setLoadError] = useState<{ key: string; error: LoadError } | null>(null)
  const activeLoadError = loadError?.key === key ? loadError.error : null
  const [loadAttempt, setLoadAttempt] = useState(0)

  // Load file on mount, cleanup buffer on unmount.
  // `loadAttempt` is a deps-only retry trigger (see retryLoad).
  // NOTE: the synchronous `setLoadError` calls below are the behaviour EditorPane
  // has always had; the rule only started firing once the effect moved out of the
  // large component the react-hooks compiler bailed on. The refactor preserves it
  // verbatim rather than restructuring the load surface.
  useEffect(() => {
    let stale = false
    // eslint-disable-next-line react-hooks/set-state-in-effect -- preserved verbatim from EditorPane
    setLoadError(null)
    if (useEditorStore.getState().buffers[key]) return // already loaded
    // Only an untitled pane may open an empty buffer (spec 1.2): it has no file
    // behind it yet. Everything else — including "not found" — becomes a load
    // error, because a buffer opened over an unreadable file would silently
    // truncate that file on the next save.
    if (isUntitled) {
      useEditorStore.getState().openBuffer(key, '', createMetadata(source, filePath, untitled))
      return
    }

    // Spec 1.2b: no backend at all (a `local` source outside Electron, a daemon
    // source whose host was removed) used to bail out silently and leave the pane
    // spinning on "Loading…" forever. Same silent-failure class as 1.2, reached by
    // a different route — so it surfaces through the same retryable error state.
    const backend = getFsBackend(source)
    if (!backend) {
      setLoadError({ key, error: { messageKey: 'editor.load_error.no_backend' } })
      return
    }

    backend.read(filePath)
      .then((data) => {
        if (stale) return
        const text = new TextDecoder().decode(data)
        const metadata = createMetadata(source, filePath, untitled)
        return backend.stat(filePath).then((stat) => {
          if (stale) return
          useEditorStore.getState().openBuffer(key, text, metadata, { mtime: stat.mtime, size: stat.size })
        })
      })
      .catch((error: unknown) => {
        if (stale) return
        setLoadError({ key, error: { message: loadErrorMessage(error) } })
      })

    return () => { stale = true }
  }, [filePath, isUntitled, key, sourceId, source, untitled, loadAttempt])

  const retryLoad = useCallback(() => {
    setLoadError(null)
    setLoadAttempt((attempt) => attempt + 1)
  }, [])

  return { activeLoadError, retryLoad }
}
