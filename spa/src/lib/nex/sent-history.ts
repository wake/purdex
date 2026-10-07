// spa/src/lib/nex/sent-history.ts — the messages the reader sent in one execution, oldest first,
// for the reply box's ArrowUp/ArrowDown history. Read from the transcript store's user lines (the
// reducer's durable bubbles), plus the optimistic line while its send is unaccepted. Each entry is
// the typed text with the `[file: …]` lines `composeWithAttachments` appended split back off, and
// the native images' metadata (bytes come back through the capability's fetch route).
import type { StreamMessage, UserMessage } from './message-types'
import type { ExecutionState } from './event-reducer'
import { attachmentsOf, type AttachmentMeta } from './attachments'
import { INTERRUPT_TEXT, isOpeningLine } from './turns'
import { splitAttachmentLines } from './worker-upload'

export interface SentEntry {
  text: string
  /** Paths of the `[file: …]` lines: re-attachable without an upload. */
  paths: string[]
  /** Native images the message carried. */
  images: AttachmentMeta[]
}

/** System-injected user turns (a background task's notification) are not something the reader typed. */
const INJECTED = /^\s*<(task-notification|system-reminder)\b/

function entryOf(text: string, images: AttachmentMeta[]): SentEntry | null {
  const split = splitAttachmentLines(text)
  if (!split.text && split.paths.length === 0 && images.length === 0) return null
  return { text: split.text, paths: split.paths, images }
}

const sameEntry = (a: SentEntry, b: SentEntry) =>
  a.text === b.text
  && a.paths.join('\n') === b.paths.join('\n')
  && a.images.map((i) => i.sha256).join() === b.images.map((i) => i.sha256).join()

export function buildSentHistory(
  messages: readonly StreamMessage[],
  pendingLocal: ExecutionState['pendingLocal'],
): SentEntry[] {
  const out: SentEntry[] = []
  const push = (e: SentEntry | null) => {
    if (e && !(out.length > 0 && sameEntry(out[out.length - 1], e))) out.push(e)
  }
  for (const msg of messages) {
    if (!isOpeningLine(msg)) continue
    const text = (msg as UserMessage).message.content
      .filter((b) => b.type === 'text' && b.text !== undefined && b.text !== INTERRUPT_TEXT)
      .map((b) => b.text as string)
      .join('\n')
    if (INJECTED.test(text)) continue
    push(entryOf(text, attachmentsOf(msg) ?? []))
  }
  // The optimistic line: its images are local object URLs, with no hash to fetch by — text and paths only.
  if (pendingLocal) push(entryOf(pendingLocal.text, []))
  return out
}
