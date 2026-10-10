// Tests only: the first bytes of real image files (what a magic-number sniff reads).
const pad = (head: number[], n = 32): Uint8Array => { const u = new Uint8Array(n); u.set(head); return u }
const ascii = (s: string): number[] => [...s].map((c) => c.charCodeAt(0))

export const PNG_BYTES = pad([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a])
export const JPEG_BYTES = pad([0xff, 0xd8, 0xff, 0xe0])
export const GIF_BYTES = pad(ascii('GIF89a'))
export const WEBP_BYTES = pad([...ascii('RIFF'), 0, 0, 0, 0, ...ascii('WEBP')])
export const BMP_BYTES = pad(ascii('BM'))
export const AVIF_BYTES = pad([0, 0, 0, 0x1c, ...ascii('ftypavif')])
export const HEIC_BYTES = pad([0, 0, 0, 0x18, ...ascii('ftypheic')])
export const TIFF_BYTES = pad([0x49, 0x49, 0x2a, 0x00])
export const TEXT_BYTES = pad(ascii('<html>not an image'))
