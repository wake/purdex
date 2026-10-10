// spa/src/lib/conversations/attachment-upload.ts — the uploads of a session pane's input, run OUTSIDE the component (the tab-hosted
// rule: the input unmounts on a tab switch, and an upload must not die with it). A finished upload lands in the draft memory and the
// chip memory whether or not an input is mounted; a remounted input reads what is still on its way from here. Only a release of the
// pane (`forgetAttachmentsWhere`, via `pane-release`) aborts an upload, and an aborted one writes nothing back. Keyed like the draft.
import { agentUploadToPath } from '../host-api'
import { addAttachment, attachmentText, insertAttachmentText, isImageFile, trackUpload } from './attachment-memory'
import { readDraft, writeDraft } from './draft-memory'

export interface Uploading { id: string; name: string; percent: number }
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

/** Starts saving `files`; each one's path goes into the draft (on its own line) and a chip when it lands. */
export function startUploads(key: string, hostId: string, sessionCode: string, files: readonly File[]): void {
  for (const file of files) {
    const id = `att-${++seq}`
    const ctl = new AbortController()
    const untrack = trackUpload(key, ctl)
    setRunning(key, [...uploadsOf(key), { id, name: file.name, percent: 0 }])
    const done = () => { untrack(); setRunning(key, uploadsOf(key).filter((x) => x.id !== id)) }
    agentUploadToPath(hostId, file, sessionCode, {
      signal: ctl.signal,
      onProgress: (percent) => { if (!ctl.signal.aborted) setRunning(key, uploadsOf(key).map((x) => (x.id === id ? { ...x, percent } : x))) },
    }).then(({ path }) => {
      // released while the answer was on its way: nothing may be rebuilt
      if (ctl.signal.aborted) return done()
      const text = attachmentText(path, isImageFile(file))
      writeDraft(key, insertAttachmentText(readDraft(key) ?? '', text))
      addAttachment(key, { id, name: file.name, path, text })
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
  for (const key of [...unseen.keys()]) if (match(key)) unseen.delete(key)
  for (const key of [...running.keys()]) if (match(key)) { running.delete(key); notify(key) }
}

/** Tests only: module state outlives a test. */
export function clearAllUploads(): void {
  running.clear(); listeners.clear(); unseen.clear()
}
