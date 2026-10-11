// spa/src/lib/nex/sent-history.ts — the messages the reader sent in one execution, oldest first,
// for the reply box's ArrowUp/ArrowDown history. Read from the transcript store's user lines (the
// reducer's durable bubbles), plus the optimistic line while its send is unaccepted. Each entry is
// the typed text with the trailing `[file: …]` lines (a message sent with files carries them) split back off, and
// the images' metadata a replayed message carries.
import type { StreamMessage, UserMessage } from './message-types'
import type { ExecutionState } from './event-reducer'
import { attachmentsOf, type AttachmentMeta } from './attachments'
import { INTERRUPT_TEXT, isOpeningLine } from './turns'

export interface SentEntry {
  text: string
  /** Paths of the `[file: …]` lines: re-attachable without an upload. */
  paths: string[]
  /** Images the message carried (replayed metadata). */
  images: AttachmentMeta[]
}

/**
 * The typed text and the paths of the trailing `[file: …]` lines. Only an unbroken run of such lines at the very end counts; text without
 * one is returned as is.
 */
export function splitAttachmentLines(text: string): { text: string; paths: string[] } {
  const lines = text.split('\n')
  const paths: string[] = []
  while (lines.length > 0) {
    const m = /^\[file: (.+)\]$/.exec(lines[lines.length - 1])
    if (!m) break
    paths.unshift(m[1])
    lines.pop()
  }
  if (paths.length === 0) return { text, paths }
  if (lines.length > 0 && lines[lines.length - 1] === '') lines.pop()
  return { text: lines.join('\n'), paths }
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
  // The optimistic line: text and paths only.
  if (pendingLocal) push(entryOf(pendingLocal.text, []))
  return out
}
