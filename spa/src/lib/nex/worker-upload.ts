// spa/src/lib/nex/worker-upload.ts — files attached to a worker message by
// path (worker-pane theme spec §9.1). Each file is uploaded into the
// execution's cwd as soon as it is added and shows as a chip; on send the done
// chips become `[file: <absolute path>]` lines under the typed text. A chip
// still uploading or failed blocks the send until it finishes or is removed.
//
// Phase E (spec §9.2, nexen contract §1.9): when the host accepts images for
// the execution's provider, an image that fits the capability's limits is not
// uploaded — it stays local (`kind: 'image'`) and goes out as a native
// base64 attachment on send. Everything else, and every image on an older
// daemon or for a provider without image input, keeps the path behaviour.
import { NexApiError, type ImageAttachmentCaps } from './types'

export interface Chip {
  key: string
  /** `image`: sent as a native attachment (never uploaded, no `path`); `path`: uploaded and sent as a `[file:]` line. */
  kind: 'path' | 'image'
  name: string
  status: 'uploading' | 'done' | 'failed'
  /** Absolute path on the daemon host, once done. */
  path?: string
  /** The daemon's error code when failed (absent for an unstructured failure); see uploadErrorKey. */
  error?: string
  /** Object URL for an image thumbnail; revoked when the chip goes away. */
  previewUrl?: string
  /** A locale key under `worker.upload.` explaining the chip, e.g. an image that did not fit and went by path. */
  note?: 'image_as_path'
}

/** One image on the send wire (contract §1.9): standard padded base64, no `data:` prefix. */
export type WireImageAttachment = { type: 'image'; media_type: string; data: string }

/** `selectImageAttachments`'s result: the image capability for this execution's provider, or null (fail-closed). */
export type ImageAttachmentPlanCaps = (ImageAttachmentCaps & { maxRequestBytes: number }) | null

/**
 * A generous upper bound for a lease id on the wire: nexen mints it with
 * `newULID()` (store/lease.go:110 → store/execution.go:1105), a 26-char
 * Crockford base32 ULID, so 64 leaves headroom for a format change.
 */
const LEASE_ID_BOUND = 64

/**
 * The exact UTF-8 byte length of a send body (`sendMessage`'s key order)
 * carrying `text` and one image per entry of the given decoded size — the
 * number the daemon compares with `send.max_request_bytes`. The base64
 * payloads are counted arithmetically (4 per started 3 bytes, padded) so no
 * image has to be encoded to measure it.
 */
export function requestBytes(text: string, images: readonly { size: number; type: string }[], leaseIdLength = LEASE_ID_BOUND): number {
  const skeleton = JSON.stringify({
    lease_id: 'x'.repeat(leaseIdLength),
    text,
    attachments: images.map((i) => ({ type: 'image', media_type: i.type, data: '' })),
  })
  let bytes = new TextEncoder().encode(skeleton).length
  for (const i of images) bytes += 4 * Math.ceil(i.size / 3)
  return bytes
}

/**
 * Which files go as native attachments and which by path, in order. A file
 * goes native only when the capability exists (non-null: the host accepts
 * images for this provider), its type is accepted, it fits `max_bytes`, the
 * native count is under `max_count`, the running total stays within
 * `max_total_bytes`, and the whole request body — the draft text plus every
 * native image so far plus this one — stays within `maxRequestBytes`. A file
 * that fails any of these goes by path; nothing is dropped.
 */
export function planAttachments(
  files: readonly { key: string; size: number; type: string }[],
  caps: ImageAttachmentPlanCaps,
  text: string,
): { native: string[]; path: string[] } {
  const native: string[] = []
  const path: string[] = []
  const chosen: { size: number; type: string }[] = []
  let total = 0
  for (const f of files) {
    const fits = !!caps
      && caps.media_types.includes(f.type)
      && f.size <= caps.max_bytes
      && chosen.length < caps.max_count
      && total + f.size <= caps.max_total_bytes
      && requestBytes(text, [...chosen, f]) <= caps.maxRequestBytes
    if (fits) {
      native.push(f.key)
      chosen.push(f)
      total += f.size
    } else {
      path.push(f.key)
    }
  }
  return { native, path }
}

/** Standard base64 of a file's bytes (FileReader's data URL, prefix stripped). */
export function encodeImage(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => {
      const url = typeof reader.result === 'string' ? reader.result : ''
      const comma = url.indexOf(',')
      if (!url.startsWith('data:') || comma < 0) reject(new Error('encodeImage: unexpected reader result'))
      else resolve(url.slice(comma + 1))
    }
    reader.onerror = () => reject(reader.error ?? new Error('encodeImage: read failed'))
    reader.readAsDataURL(file)
  })
}

export function composeWithAttachments(text: string, chips: readonly Chip[]): string {
  const lines = chips.filter((c) => c.status === 'done' && c.path).map((c) => `[file: ${c.path}]`)
  if (lines.length === 0) return text
  return text ? `${text}\n\n${lines.join('\n')}` : lines.join('\n')
}

export type CanSend = { ok: true } | { ok: false; reason: 'uploading' | 'failed' }

/**
 * A failed chip wins over an uploading one: it is the one that needs the
 * reader to act (remove it), an upload finishes by itself.
 */
export function canSend(chips: readonly Chip[]): CanSend {
  if (chips.some((c) => c.status === 'failed')) return { ok: false, reason: 'failed' }
  if (chips.some((c) => c.status === 'uploading')) return { ok: false, reason: 'uploading' }
  return { ok: true }
}

/**
 * Daemon upload codes (internal/module/nex/upload.go) with their own copy,
 * plus `network`, plus Nexen's send-with-attachments codes (contract §1.9)
 * and `request_too_large` — a native image chip fails with those.
 */
const ATTACHMENT_SEND_ERRORS: ReadonlySet<string> = new Set([
  'attachments_unsupported', 'too_many_attachments', 'invalid_attachment', 'attachment_type_unsupported',
  'attachment_too_large', 'attachments_too_large', 'request_too_large',
])
const KNOWN_UPLOAD_ERRORS = new Set([
  'missing_file', 'invalid_execution_id', 'upload_dir_outside_cwd', 'execution_not_found',
  'execution_ended', 'cwd_unavailable', 'file_too_large', 'nex_unavailable', 'network',
  ...ATTACHMENT_SEND_ERRORS,
])

/** Nexen's per-image send codes: their body carries `attachment_index` (contract §1.9). */
export const PER_IMAGE_ERRORS: ReadonlySet<string> = new Set(['invalid_attachment', 'attachment_type_unsupported', 'attachment_too_large'])

/** A send failure caused by the attachments (per-image or whole-batch), shown with the chip copy. */
export function isAttachmentError(code: string): boolean {
  return ATTACHMENT_SEND_ERRORS.has(code)
}

/** The locale key for a failed chip's reason: a daemon code, or the error itself. */
export function uploadErrorKey(err: unknown): string {
  const code = err instanceof NexApiError ? err.code : typeof err === 'string' ? err : undefined
  return code && KNOWN_UPLOAD_ERRORS.has(code) ? `worker.upload.error.${code}` : 'worker.upload.error.generic'
}
