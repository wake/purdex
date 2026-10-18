import { describe, it, expect, vi, beforeEach } from 'vitest'
import { rasterizeAvatar, AVATAR_PX } from './persona-raster'

function fakeCanvas(results: string[]) {
  const toDataURL = vi.fn((type?: string, quality?: number) => {
    void type; void quality
    return results.shift() ?? ''
  })
  const drawImage = vi.fn()
  const canvas = { width: 0, height: 0, getContext: () => ({ drawImage }), toDataURL }
  vi.spyOn(document, 'createElement').mockReturnValue(canvas as never)
  return { canvas, toDataURL, drawImage }
}

describe('rasterizeAvatar', () => {
  beforeEach(() => vi.restoreAllMocks())

  it('draws at 128 px and returns a WebP data URL', () => {
    const { canvas, toDataURL, drawImage } = fakeCanvas(['data:image/webp;base64,AAAA'])
    expect(rasterizeAvatar({} as CanvasImageSource, 300, 200)).toBe('data:image/webp;base64,AAAA')
    expect(canvas.width).toBe(AVATAR_PX)
    expect(canvas.height).toBe(AVATAR_PX)
    expect(toDataURL).toHaveBeenCalledWith('image/webp', 0.85)
    // a 300x200 source is cropped to its centre square, then scaled to 128
    expect(drawImage).toHaveBeenCalledWith({}, 50, 0, 200, 200, 0, 0, AVATAR_PX, AVATAR_PX)
  })

  it('falls back to PNG when the browser cannot encode WebP', () => {
    const { toDataURL } = fakeCanvas(['data:image/png;base64,AAAA', 'data:image/png;base64,BBBB'])
    expect(rasterizeAvatar({} as CanvasImageSource, 128, 128)).toBe('data:image/png;base64,BBBB')
    expect(toDataURL).toHaveBeenLastCalledWith('image/png')
  })

  it('lowers the quality until the string fits 32 KB, then gives up', () => {
    const big = 'data:image/webp;base64,' + 'A'.repeat(40_000)
    const ok = 'data:image/webp;base64,AAAA'
    const { toDataURL } = fakeCanvas([big, ok])
    expect(rasterizeAvatar({} as CanvasImageSource, 128, 128)).toBe(ok)
    expect(toDataURL.mock.calls.map((c) => c[1])).toEqual([0.85, 0.6])

    fakeCanvas([big, big, big, big])
    expect(() => rasterizeAvatar({} as CanvasImageSource, 128, 128)).toThrow(/32 KB/)
  })
})
