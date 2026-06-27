/**
 * storage-paths — the single source of truth for the In-App storage root and
 * the path helpers used to build/inspect paths beneath it.
 *
 * The In-App filesystem (`InAppBackend`, IndexedDB) keys every entry by its
 * absolute path under `STORAGE_ROOT`. Before this module, callers hardcoded the
 * `/buffer` literal and hand-built paths via template strings, which broke once
 * the storage became a nested tree. Centralizing here keeps the prefix in one
 * place and gives nested-aware helpers (`parentOf`, `relativeToRoot`, …).
 */

/** Root prefix for all In-App storage entries (no migration — stays `/buffer`). */
export const STORAGE_ROOT = '/buffer'

/**
 * Join path segments into a normalized path.
 *
 * - Splits each segment on `/` and drops empty pieces, so trailing/leading
 *   slashes and empty segments never produce `//`.
 * - Preserves a leading slash iff the first segment starts with one (so
 *   `join(STORAGE_ROOT, 'a')` → `/buffer/a`, `join('a', 'b')` → `a/b`).
 */
export function join(...segs: string[]): string {
  const parts: string[] = []
  for (const seg of segs) {
    for (const piece of seg.split('/')) {
      if (piece) parts.push(piece)
    }
  }
  const leading = segs.length > 0 && segs[0].startsWith('/') ? '/' : ''
  return leading + parts.join('/')
}

/**
 * Parent directory of `path`. The root path (`/buffer`) yields the filesystem
 * root `/`. A trailing slash is ignored.
 */
export function parentOf(path: string): string {
  const leading = path.startsWith('/') ? '/' : ''
  const parts = path.split('/').filter(Boolean)
  parts.pop()
  return leading + parts.join('/')
}

/**
 * Last segment of `path` (the file or folder name). A trailing slash is
 * ignored; the filesystem root `/` yields `''`.
 */
export function basename(path: string): string {
  const parts = path.split('/').filter(Boolean)
  return parts.length > 0 ? parts[parts.length - 1] : ''
}

/**
 * `path` with the `STORAGE_ROOT` prefix removed (e.g. `/buffer/a/b.md` →
 * `a/b.md`). The root itself yields `''`; a path not under the root is
 * returned unchanged.
 */
export function relativeToRoot(path: string): string {
  if (path === STORAGE_ROOT) return ''
  const prefix = STORAGE_ROOT + '/'
  if (path.startsWith(prefix)) return path.slice(prefix.length)
  return path
}

/** Whether `path` is the storage root or lives beneath it. */
export function isUnderRoot(path: string): boolean {
  return path === STORAGE_ROOT || path.startsWith(STORAGE_ROOT + '/')
}
