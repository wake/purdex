// spa/src/lib/names/persona-raster.ts — an avatar is stored as a 128 px raster data URL, made when the persona is
// saved (name pool v2 spec §4.1, L3). WebP at 0.85; PNG when the browser cannot encode WebP; each string must fit
// 32 KB, so the quality steps down before giving up.
import { PERSONA_LIMITS } from './persona-limits'

export const AVATAR_PX = 128
const WEBP_QUALITIES = [0.85, 0.6, 0.4]

/** `source` is cropped to its centre square and scaled to 128 px. `sw`/`sh` are the source's pixel size. */
export function rasterizeAvatar(source: CanvasImageSource, sw: number, sh: number): string {
  const canvas = document.createElement('canvas')
  canvas.width = AVATAR_PX
  canvas.height = AVATAR_PX
  const ctx = canvas.getContext('2d')
  if (!ctx) throw new Error('canvas is not available')
  const side = Math.min(sw, sh)
  ctx.drawImage(source, (sw - side) / 2, (sh - side) / 2, side, side, 0, 0, AVATAR_PX, AVATAR_PX)

  const first = canvas.toDataURL('image/webp', WEBP_QUALITIES[0])
  if (!first.startsWith('data:image/webp')) {
    // The browser ignored the type request: PNG, which has no quality knob.
    const png = canvas.toDataURL('image/png')
    if (png.length > PERSONA_LIMITS.avatarData) throw new Error('avatar image is over 32 KB')
    return png
  }
  if (first.length <= PERSONA_LIMITS.avatarData) return first
  for (const q of WEBP_QUALITIES.slice(1)) {
    const lower = canvas.toDataURL('image/webp', q)
    if (lower.length <= PERSONA_LIMITS.avatarData) return lower
  }
  throw new Error('avatar image is over 32 KB')
}
