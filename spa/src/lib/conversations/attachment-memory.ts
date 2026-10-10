// spa/src/lib/conversations/attachment-memory.ts — the attachments of a session pane's input: the "attached xxx" chips and the
// text an attachment puts in the draft. Kept outside the component like the draft (the tab-hosted rule: a pane the alive pool
// does not keep unmounts on a tab switch). Memory only; keyed like the draft (`draftKey`), freed by `pane-release`.
import { IMAGE_EXT } from './image-sniff'

export interface Attachment {
  id: string
  /** The file's name, for the chip. */
  name: string
  /** Where the daemon saved it. */
  path: string
  /** The exact text this attachment put in the draft (the chip's delete takes it out again). */
  text: string
  /** Whether the file is an image (File.type or extension at attach time); absent: not known to be one. */
  image?: boolean
  mime?: string
  /** Bytes. */
  size?: number
  /** A blob: URL of the local file for the thumbnail; owned by this memory (revoked when the chip goes). Absent after a reload. */
  previewUrl?: string
  /**
   * An image's number in its draft: the draft holds the short `[Image #seq]` (this chip's `text`) instead of the whole marker, and
   * sending turns it into `[Image: source: <path>]` (see image-placeholder). Absent: the chip stands for a whole line of the draft.
   */
  seq?: number
  /** When the draft was first seen without this chip's marker (ms); cleared when it comes back. Set by `reconcileAttachments`. */
  hiddenSince?: number
}

/** How long a chip whose marker has left the draft is kept (a native undo may bring the marker back) before another attach lets it go. */
export const HIDDEN_ATTACHMENT_TTL_MS = 60_000

/** `URL.createObjectURL` where it exists (not in every test environment). */
export function makePreviewUrl(file: Blob): string | undefined {
  return typeof URL.createObjectURL === 'function' ? URL.createObjectURL(file) : undefined
}
export function revokePreviewUrl(url: string | undefined): void {
  if (url && typeof URL.revokeObjectURL === 'function') URL.revokeObjectURL(url)
}

const chips = new Map<string, readonly Attachment[]>()
/**
 * The last image number handed out, ONE count for the whole app: a `[Image #N]` pasted into another pane (or draft) then names no
 * chip there and stays text, instead of expanding into somebody else's picture. Never reset, whatever is sent or released.
 */
let lastSeq = 0

export function nextAttachmentSeq(): number {
  return ++lastSeq
}

export const readAttachments = (key: string): readonly Attachment[] => chips.get(key) ?? []

export function addAttachment(key: string, a: Attachment): void {
  chips.set(key, [...readAttachments(key), a])
}

export function removeAttachment(key: string, id: string): void {
  for (const a of readAttachments(key)) if (a.id === id) revokePreviewUrl(a.previewUrl)
  const rest = readAttachments(key).filter((a) => a.id !== id)
  if (rest.length === 0) chips.delete(key)
  else chips.set(key, rest)
}

/** Uploads in flight, by key: a release cancels them, so one that lands after it cannot rebuild what was freed. */
const uploads = new Map<string, Set<AbortController>>()

/** Registers an upload of `key`; returns its untrack. */
export function trackUpload(key: string, ctl: AbortController): () => void {
  let set = uploads.get(key)
  if (!set) uploads.set(key, (set = new Set()))
  set.add(ctl)
  return () => { set.delete(ctl); if (set.size === 0 && uploads.get(key) === set) uploads.delete(key) }
}

/** Forget every chip list whose key matches (a pane that is gone, or a session it no longer shows), and cancel its uploads. */
export function forgetAttachmentsWhere(match: (key: string) => boolean): void {
  for (const key of [...chips.keys()]) if (match(key)) { for (const a of readAttachments(key)) revokePreviewUrl(a.previewUrl); chips.delete(key) }
  for (const [key, set] of [...uploads]) if (match(key)) { uploads.delete(key); for (const c of set) c.abort() }
}

/**
 * The chips whose marker is still in the draft (the draft is the truth: a line the reader deleted or rewrote takes its chip
 * with it). A path chip needs its own line, identical lines counted one by one; an image's `[Image #N]` may sit anywhere.
 */
export function visibleAttachments(draft: string, list: readonly Attachment[]): readonly Attachment[] {
  const left = new Map<string, number>()
  for (const line of draft.split('\n')) left.set(line, (left.get(line) ?? 0) + 1)
  return list.filter((a) => {
    if (a.seq !== undefined) return draft.includes(a.text) // a short placeholder may sit anywhere in a line
    const n = left.get(a.text) ?? 0
    if (n === 0) return false
    left.set(a.text, n - 1)
    return true
  })
}

/**
 * The draft decides which chips are SHOWN (and expanded on send), but a chip whose marker is gone is NOT destroyed: the browser's
 * undo can bring the text back, and the chip and its picture must come with it. This only keeps the clock: a chip first seen
 * without its marker is stamped, one that is back is cleared; with `sweep`, a chip stamped longer than the TTL ago is let go.
 */
export function reconcileAttachments(key: string, draft: string, opts: { sweep?: boolean; now?: number } = {}): void {
  const list = readAttachments(key)
  if (list.length === 0) return
  const now = opts.now ?? Date.now()
  const shown = new Set(visibleAttachments(draft, list))
  const next: Attachment[] = []
  for (const a of list) {
    if (shown.has(a)) { next.push(a.hiddenSince === undefined ? a : { ...a, hiddenSince: undefined }); continue }
    if (opts.sweep && a.hiddenSince !== undefined && now - a.hiddenSince > HIDDEN_ATTACHMENT_TTL_MS) { revokePreviewUrl(a.previewUrl); continue }
    next.push(a.hiddenSince === undefined ? { ...a, hiddenSince: now } : a)
  }
  if (next.length === 0) chips.delete(key)
  else chips.set(key, next)
}

/** Tests only: module state outlives a test. */
export const clearAllAttachments = (): void => { for (const list of chips.values()) for (const a of list) revokePreviewUrl(a.previewUrl); chips.clear(); lastSeq = 0; uploads.clear() }

/* ─── the draft text an attachment stands for ─── */

export const isImageFile = (f: File): boolean => f.type.startsWith('image/') || IMAGE_EXT.test(f.name)

/** An image is the `[Image: source: <path>]` marker the agent reads; any other file is only its path. */
export const attachmentText = (path: string, image: boolean): string => (image ? `[Image: source: ${path}]` : path)

/** On its own line: straight into an empty draft, after a line break otherwise. */
export function insertAttachmentText(draft: string, text: string): string {
  if (draft === '') return text
  return draft.endsWith('\n') ? `${draft}${text}` : `${draft}\n${text}`
}

/** Takes the attachment's own line out (the first one that is exactly `text`); the rest of the draft is untouched. */
export function removeAttachmentText(draft: string, text: string): string {
  const lines = draft.split('\n')
  const at = lines.indexOf(text)
  if (at < 0) return draft
  lines.splice(at, 1)
  return lines.join('\n')
}
