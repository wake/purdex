// spa/src/lib/nex/worker-upload.ts — files attached to a worker message by
// path (worker-pane theme spec §9.1). Each file is uploaded into the
// execution's cwd as soon as it is added and shows as a chip; on send the done
// chips become `[file: <absolute path>]` lines under the typed text. A chip
// still uploading or failed blocks the send until it finishes or is removed.
import { NexApiError } from './types'

export interface Chip {
  key: string
  name: string
  status: 'uploading' | 'done' | 'failed'
  /** Absolute path on the daemon host, once done. */
  path?: string
  /** The daemon's error code when failed (absent for an unstructured failure); see uploadErrorKey. */
  error?: string
  /** Object URL for an image thumbnail; revoked when the chip goes away. */
  previewUrl?: string
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

/** Daemon upload codes (internal/module/nex/upload.go) with their own copy, plus `network`. */
const KNOWN_UPLOAD_ERRORS = new Set([
  'missing_file', 'invalid_execution_id', 'upload_dir_outside_cwd', 'execution_not_found',
  'execution_ended', 'cwd_unavailable', 'file_too_large', 'nex_unavailable', 'network',
])

/** The locale key for a failed chip's reason: a daemon code, or the error itself. */
export function uploadErrorKey(err: unknown): string {
  const code = err instanceof NexApiError ? err.code : typeof err === 'string' ? err : undefined
  return code && KNOWN_UPLOAD_ERRORS.has(code) ? `worker.upload.error.${code}` : 'worker.upload.error.generic'
}
