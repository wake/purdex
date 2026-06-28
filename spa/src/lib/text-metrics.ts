/**
 * text-metrics — the single source of truth for the In-App word-count metric.
 *
 * Extracted from `StorageRow` (T2b-1) so the Storage tree row AND the backup
 * manifest builder compute identical word counts (R1-P1c). A row reads bytes
 * only when `isWordCountable` (extension allowlist + size cap) is true; the
 * manifest builder already has the bytes (it hashes every file) and calls
 * `wordCountFor`, which re-applies the same gate and returns 0 for binaries /
 * oversized text — the exact metric subsystem-1 rows show (0 for binary).
 */

/**
 * Explicit **allowlist** of extensions treated as word-countable text (spec §4).
 * Only these are decoded; unknown/binary extensions never produce a garbage word
 * count. `.log`, `.env`, `.gitignore` etc. are included so notes-style entries
 * still count.
 */
const TEXT_EXTS = new Set([
  'md', 'markdown', 'txt', 'text',
  'ts', 'tsx', 'js', 'jsx', 'mjs', 'cjs',
  'json', 'css', 'scss', 'html', 'xml',
  'yaml', 'yml', 'toml', 'csv',
  'sh', 'py', 'rs', 'go', 'c', 'h', 'cpp',
  'sql', 'log', 'ini', 'env', 'gitignore',
])

/**
 * Word count decodes the whole file, so it is capped: bytes above this size are
 * never counted (the row shows size only, and the manifest records 0 words).
 */
const WORD_COUNT_MAX_BYTES = 256 * 1024

function extensionOf(path: string): string {
  const base = path.split('/').pop() ?? path
  const dot = base.lastIndexOf('.')
  if (dot < 0) return ''
  // Leading-dot dotfiles (.env, .gitignore) use their whole name as the
  // extension token for allowlist purposes; a bare `dot === 0` would yield ''.
  if (dot === 0) return base.slice(1).toLowerCase()
  return base.slice(dot + 1).toLowerCase()
}

/**
 * Whether a path of `size` bytes should be word-counted: its extension is on
 * the text allowlist AND it is within the size cap. The Storage row uses this
 * as its read gate (a binary / oversized row never reads its bytes).
 */
export function isWordCountable(path: string, size: number): boolean {
  if (size > WORD_COUNT_MAX_BYTES) return false
  return TEXT_EXTS.has(extensionOf(path))
}

/**
 * Word count of `bytes` for `path`, or 0 when the path is not word-countable
 * (non-allowlisted extension or over the size cap). Decodes UTF-8 and counts
 * whitespace-delimited tokens — the same metric as the legacy StorageRow inline
 * count (`TextDecoder + split(/\s+/).filter(Boolean)`).
 */
export function wordCountFor(path: string, bytes: Uint8Array): number {
  if (!isWordCountable(path, bytes.byteLength)) return 0
  const str = new TextDecoder().decode(bytes)
  return str.split(/\s+/).filter(Boolean).length
}
