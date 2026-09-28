// spa/src/lib/nex/attachments.ts — image attachment metadata on replayed
// user lines (worker-pane theme phase E, nexen contract §1.9). The durable
// `execution.message_accepted` / `execution.delegated` events carry only
// `attachments: [{media_type, bytes, sha256}]`; the bytes come back from the
// capability's `fetch` route. The reducer keeps that list on the synthetic
// user message as the side field `purdex_attachments` — not a content block,
// so everything that reads Claude-shaped content (search, pairing, fold keys)
// is untouched.
import type { StreamMessage } from './message-types'

export interface AttachmentMeta {
  media_type: string
  /** Decoded size in bytes. */
  bytes: number
  /** Lowercase hex sha256 of the decoded bytes: the fetch route's `{sha256}`. */
  sha256: string
}

const SHA256_HEX = /^[0-9a-f]{64}$/

/**
 * The valid entries of an event's `attachments` value, in order. Anything
 * that is not a list yields `[]`; an entry that is not `{media_type: string,
 * bytes: finite ≥ 0, sha256: 64 lowercase hex}` is dropped (the fetch route
 * would refuse it with `malformed_parameter` anyway).
 */
export function parseAttachmentMeta(v: unknown): AttachmentMeta[] {
  if (!Array.isArray(v)) return []
  const out: AttachmentMeta[] = []
  for (const a of v) {
    if (typeof a !== 'object' || a === null) continue
    const { media_type, bytes, sha256 } = a as Record<string, unknown>
    if (typeof media_type !== 'string' || typeof sha256 !== 'string' || !SHA256_HEX.test(sha256)) continue
    if (typeof bytes !== 'number' || !Number.isFinite(bytes) || bytes < 0) continue
    out.push({ media_type, bytes, sha256 })
  }
  return out
}

/** The attachments the reducer stored on a user message; undefined when it has none. */
export function attachmentsOf(msg: StreamMessage): AttachmentMeta[] | undefined {
  const a = (msg as { purdex_attachments?: unknown }).purdex_attachments
  return Array.isArray(a) && a.length > 0 ? (a as AttachmentMeta[]) : undefined
}
