// spa/src/lib/conversations/image-sniff.ts — what may be shown as a picture. A message can name any path, so a path is read only
// when it looks like an image file and is not a device / process / system node, and what comes back is shown only when its first
// bytes ARE an image (the type of the blob is what the bytes say, never the name).

export const IMAGE_EXT = /\.(png|jpe?g|gif|webp|bmp|heic|heif|tiff?|avif)$/i

export const isImagePath = (path: string): boolean => IMAGE_EXT.test(path)

const UNREADABLE = new Set(['dev', 'proc', 'sys'])

/** False for the device, process and system trees (`..` resolved first); those are never read. */
export function isReadablePath(path: string): boolean {
  const out: string[] = []
  for (const seg of path.split('/')) {
    if (seg === '' || seg === '.') continue
    if (seg === '..') out.pop()
    else out.push(seg)
  }
  return !(path.startsWith('/') && out.length > 0 && UNREADABLE.has(out[0]))
}

const at = (b: Uint8Array, i: number, s: string): boolean => {
  for (let k = 0; k < s.length; k++) if (b[i + k] !== s.charCodeAt(k)) return false
  return true
}

/** The image type the first bytes say, or null when they are not a common image header. */
export function sniffImageMime(b: Uint8Array): string | null {
  if (b.length < 12) return null
  if (b[0] === 0x89 && at(b, 1, 'PNG') && b[4] === 0x0d && b[5] === 0x0a && b[6] === 0x1a && b[7] === 0x0a) return 'image/png'
  if (b[0] === 0xff && b[1] === 0xd8 && b[2] === 0xff) return 'image/jpeg'
  if (at(b, 0, 'GIF87a') || at(b, 0, 'GIF89a')) return 'image/gif'
  if (at(b, 0, 'RIFF') && at(b, 8, 'WEBP')) return 'image/webp'
  if (at(b, 0, 'BM') && b[6] === 0 && b[7] === 0 && b[8] === 0 && b[9] === 0) return 'image/bmp'
  if (at(b, 4, 'ftyp')) {
    const brand = String.fromCharCode(b[8], b[9], b[10], b[11])
    if (brand === 'avif' || brand === 'avis') return 'image/avif'
    if (['heic', 'heix', 'hevc', 'heim', 'heis', 'mif1', 'msf1'].includes(brand)) return 'image/heic'
    return null
  }
  if ((b[0] === 0x49 && b[1] === 0x49 && b[2] === 0x2a && b[3] === 0) || (b[0] === 0x4d && b[1] === 0x4d && b[2] === 0 && b[3] === 0x2a)) return 'image/tiff'
  return null
}

/** The message sources whose `[Image: source: <path>]` is read for a picture: the person's own words. Any other (a peer, a task, a source this build does not know) names paths it chose, and stays text. */
export const OWN_TEXT_SOURCES: ReadonlySet<string> = new Set(['user', 'queued'])
