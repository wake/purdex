import { describe, it, expect } from 'vitest'
import { isImagePath, isReadablePath, sniffImageMime } from './image-sniff'
import { AVIF_BYTES, BMP_BYTES, GIF_BYTES, HEIC_BYTES, JPEG_BYTES, PNG_BYTES, TEXT_BYTES, TIFF_BYTES, WEBP_BYTES } from './image-fixtures'

describe('sniffImageMime', () => {
  it('knows the common image headers by their first bytes, not by any name', () => {
    expect(sniffImageMime(PNG_BYTES)).toBe('image/png')
    expect(sniffImageMime(JPEG_BYTES)).toBe('image/jpeg')
    expect(sniffImageMime(GIF_BYTES)).toBe('image/gif')
    expect(sniffImageMime(WEBP_BYTES)).toBe('image/webp')
    expect(sniffImageMime(BMP_BYTES)).toBe('image/bmp')
    expect(sniffImageMime(AVIF_BYTES)).toBe('image/avif')
    expect(sniffImageMime(HEIC_BYTES)).toBe('image/heic')
    expect(sniffImageMime(TIFF_BYTES)).toBe('image/tiff')
  })
  it('anything else is not an image (text, html, svg, an empty or tiny body)', () => {
    expect(sniffImageMime(TEXT_BYTES)).toBeNull()
    expect(sniffImageMime(new TextEncoder().encode('<svg xmlns="http://www.w3.org/2000/svg"/>'))).toBeNull()
    expect(sniffImageMime(new Uint8Array(0))).toBeNull()
    expect(sniffImageMime(new Uint8Array([0x89, 0x50]))).toBeNull()
    expect(sniffImageMime(new Uint8Array([82, 73, 70, 70, 0, 0, 0, 0, 87, 65, 86, 69]))).toBeNull() // RIFF WAVE
  })
})

describe('which paths may be read as a picture', () => {
  it('only an image extension counts', () => {
    expect(isImagePath('/up/a.PNG')).toBe(true)
    expect(isImagePath('/up/a.jpeg')).toBe(true)
    expect(isImagePath('/up/a.txt')).toBe(false)
    expect(isImagePath('/etc/passwd')).toBe(false)
    expect(isImagePath('/up/a.png.sh')).toBe(false)
    expect(isImagePath('/up/a.svg')).toBe(false)
  })
  it('device, process and system trees are never read', () => {
    for (const p of ['/dev/zero', '/dev/fd/3', '/proc/self/mem', '/sys/kernel/x', '/dev', '/proc/1/x.png']) expect(isReadablePath(p)).toBe(false)
    for (const p of ['/up/dev001/a.png', '/home/u/devices/a.png', '/Users/w/proc/a.png']) expect(isReadablePath(p)).toBe(true)
  })
})
