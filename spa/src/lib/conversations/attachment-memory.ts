// spa/src/lib/conversations/attachment-memory.ts — the attachments of a session pane's input: the "attached xxx" chips and the
// text an attachment puts in the draft. Kept outside the component like the draft (the tab-hosted rule: a pane the alive pool
// does not keep unmounts on a tab switch). Memory only; keyed like the draft (`draftKey`), freed by `pane-release`.

export interface Attachment {
  id: string
  /** The file's name, for the chip. */
  name: string
  /** Where the daemon saved it. */
  path: string
  /** The exact text this attachment put in the draft (the chip's delete takes it out again). */
  text: string
}

const chips = new Map<string, readonly Attachment[]>()

export const readAttachments = (key: string): readonly Attachment[] => chips.get(key) ?? []

export function addAttachment(key: string, a: Attachment): void {
  chips.set(key, [...readAttachments(key), a])
}

export function removeAttachment(key: string, id: string): void {
  const rest = readAttachments(key).filter((a) => a.id !== id)
  if (rest.length === 0) chips.delete(key)
  else chips.set(key, rest)
}

/** Forget every chip list whose key matches (a pane that is gone, or a session it no longer shows). */
export function forgetAttachmentsWhere(match: (key: string) => boolean): void {
  for (const key of [...chips.keys()]) if (match(key)) chips.delete(key)
}

/** Tests only: module state outlives a test. */
export const clearAllAttachments = (): void => { chips.clear() }

/* ─── the draft text an attachment stands for ─── */

const IMAGE_EXT = /\.(png|jpe?g|gif|webp|bmp|heic|heif|tiff?|avif)$/i

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
