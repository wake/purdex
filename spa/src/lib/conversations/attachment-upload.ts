// spa/src/lib/conversations/attachment-upload.ts — the uploads of a session pane's input, run OUTSIDE the component (the tab-hosted
// rule: the input unmounts on a tab switch, and an upload must not die with it). A finished upload lands in the draft memory and the
// chip memory whether or not an input is mounted; a remounted input reads what is still on its way from here. Only a release of the
// pane (`forgetAttachmentsWhere`, via `pane-release`) aborts an upload, and an aborted one writes nothing back. Keyed like the draft.
import { useCallback, useSyncExternalStore } from 'react'
import { addAttachment, attachmentText, isImageFile, makePreviewUrl, nextAttachmentSeq, reconcileAttachments, revokePreviewUrl, trackUpload } from './attachment-memory'
import { imagePlaceholder } from './image-placeholder'
import { closeAnchor, forgetAnchorsWhere, landAnchor, openAnchors } from './insertion-anchor'
import { readDraft, writeDraft } from './draft-memory'
import { discardCleared } from './draft-clear'
import type { UploadFn } from './deck-adapter'

export interface Uploading { id: string; name: string; percent: number; image: boolean; size: number; /** The local file's blob: URL for the thumbnail (images only); handed to the chip when the upload lands, revoked otherwise. */ previewUrl?: string }
export interface UploadFailure { name: string; error: unknown }

const EMPTY: readonly Uploading[] = []
const running = new Map<string, readonly Uploading[]>()
const listeners = new Map<string, Set<() => void>>()
/** Failures not shown yet: a mounted input takes them at once, a remounted one when it comes back. */
const unseen = new Map<string, UploadFailure[]>()
let seq = 0

const notify = (key: string): void => { for (const l of [...(listeners.get(key) ?? [])]) l() }

export const uploadsOf = (key: string): readonly Uploading[] => running.get(key) ?? EMPTY

/** Called on every change of this key's uploads, and when one has landed in the memories. */
export function subscribeUploads(key: string, cb: () => void): () => void {
  let set = listeners.get(key)
  if (!set) listeners.set(key, (set = new Set()))
  set.add(cb)
  return () => { set.delete(cb); if (set.size === 0 && listeners.get(key) === set) listeners.delete(key) }
}

/** The uploads of `key` in flight, live. */
export function useUploads(key: string): readonly Uploading[] {
  return useSyncExternalStore(useCallback((cb: () => void) => subscribeUploads(key, cb), [key]), () => uploadsOf(key))
}

export function takeUnseenFailures(key: string): UploadFailure[] {
  const list = unseen.get(key) ?? []
  unseen.delete(key)
  return list
}

function setRunning(key: string, next: readonly Uploading[]): void {
  if (next.length === 0) running.delete(key)
  else running.set(key, next)
  notify(key)
}

/**
 * Starts saving `files`; each one's placeholder (an image) or path (any other file) goes into the draft and a chip when it lands.
 * `caret` is where the files came in (the input's caret, at the drop / paste): they land there, in the files' order, with the
 * point following the edits made meanwhile (insertion-anchor). Without it they land at the end of the draft.
 */
export function startUploads(key: string, upload: UploadFn, files: readonly File[], caret?: number): void {
  const anchorIds = caret === undefined ? [] : openAnchors(key, caret, files.length)
  for (const [i, file] of files.entries()) {
    const anchorId = anchorIds[i] ?? ''
    const id = `att-${++seq}`
    const ctl = new AbortController()
    const untrack = trackUpload(key, ctl)
    const image = isImageFile(file)
    const previewUrl = image ? makePreviewUrl(file) : undefined
    let handedOver = false // the chip owns the URL from then on
    setRunning(key, [...uploadsOf(key), { id, name: file.name, percent: 0, image, size: file.size, previewUrl }])
    const done = () => {
      untrack()
      closeAnchor(key, anchorId)
      if (!handedOver) revokePreviewUrl(previewUrl)
      setRunning(key, uploadsOf(key).filter((x) => x.id !== id))
    }
    upload(file, {
      signal: ctl.signal,
      onProgress: (percent) => { if (!ctl.signal.aborted) setRunning(key, uploadsOf(key).map((x) => (x.id === id ? { ...x, percent } : x))) },
    }).then(({ path }) => {
      // released while the answer was on its way: nothing may be rebuilt
      if (ctl.signal.aborted) return done()
      // a chip whose marker has been gone from the draft for long enough is let go now (see reconcileAttachments)
      reconcileAttachments(key, readDraft(key) ?? '', { sweep: true })
      // an image leaves a short `[Image #N]` (sending expands it); any other file its path on a line of its own
      const seq = image ? nextAttachmentSeq() : undefined
      const text = seq === undefined ? attachmentText(path, false) : imagePlaceholder(seq)
      discardCleared(key) // something new lands in the draft: an earlier clear can no longer be undone over it
      landAnchor(key, anchorId, readDraft(key) ?? '', text, seq === undefined ? 'line' : 'inline', (next) => writeDraft(key, next))
      handedOver = true
      addAttachment(key, { id, name: file.name, path, text, image, mime: file.type, size: file.size, previewUrl, seq })
      done() // notifies after the memories are written
    }, (error) => {
      done()
      if ((error as { kind?: string })?.kind === 'aborted' || ctl.signal.aborted) return
      unseen.set(key, [...(unseen.get(key) ?? []), { name: file.name, error }])
      notify(key) // a mounted input takes it at once; otherwise the next one to mount does
    })
  }
}

/** A released pane / session: its unseen failures and running entries go (the uploads themselves are aborted by `forgetAttachmentsWhere`). */
export function forgetUploadsWhere(match: (key: string) => boolean): void {
  forgetAnchorsWhere(match)
  for (const key of [...unseen.keys()]) if (match(key)) unseen.delete(key)
  for (const key of [...running.keys()]) if (match(key)) { for (const u of running.get(key) ?? []) revokePreviewUrl(u.previewUrl); running.delete(key); notify(key) }
}

/** Tests only: module state outlives a test. */
export function clearAllUploads(): void {
  for (const list of running.values()) for (const u of list) revokePreviewUrl(u.previewUrl)
  running.clear(); listeners.clear(); unseen.clear()
}
