// spa/src/lib/conversations/attachment-format.ts — how an attachment is named and sized on screen (thumbnails, hover preview).

/** How long the pointer rests on a thumbnail before the larger preview floats. */
export const HOVER_PREVIEW_DELAY_MS = 400

export function formatBytes(n: number | undefined): string {
  if (n === undefined || !Number.isFinite(n) || n < 0) return ''
  if (n < 1024) return `${n} B`
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(n < 10 * 1024 ? 1 : 0)} KB`
  return `${(n / (1024 * 1024)).toFixed(n < 10 * 1024 * 1024 ? 1 : 0)} MB`
}

/** A long name cut in the middle, keeping the extension: `screenshot-2….png`. */
export function shortName(name: string, max = 14): string {
  if (name.length <= max) return name
  const dot = name.lastIndexOf('.')
  const ext = dot > 0 && name.length - dot <= 6 ? name.slice(dot) : ''
  const head = Math.max(1, max - ext.length - 1)
  return `${name.slice(0, head)}…${ext}`
}

export const baseName = (path: string): string => path.slice(path.lastIndexOf('/') + 1) || path
