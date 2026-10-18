import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'

vi.mock('./persona-raster', () => ({ rasterizeAvatar: vi.fn(() => 'data:image/webp;base64,RENDERED'), AVATAR_PX: 128 }))

import { rasterizeAvatar } from './persona-raster'
import {
  DICEBEAR_STYLES, renderDicebearSvg, drawRecipe, PersonaDrawError,
} from './persona-draw'

describe('renderDicebearSvg', () => {
  // Loads all six style definitions (12–373 KB of JSON each) and draws twice: well over 5 s on a busy machine.
  it('draws an SVG for each of the six styles, deterministically from the seed', { timeout: 30_000 }, async () => {
    for (const style of DICEBEAR_STYLES) {
      const a = await renderDicebearSvg({ source: style, seed: 'Hiro' })
      const b = await renderDicebearSvg({ source: style, seed: 'Hiro' })
      expect(a.startsWith('<svg')).toBe(true)
      expect(a).toBe(b)
    }
  })

  it('draws a different picture for a different seed', async () => {
    const a = await renderDicebearSvg({ source: 'micah', seed: 'Hiro' })
    const b = await renderDicebearSvg({ source: 'micah', seed: 'Emma' })
    expect(a).not.toBe(b)
  })

  it('passes the options object through to the library', async () => {
    const plain = await renderDicebearSvg({ source: 'shapes', seed: 'x' })
    const turned = await renderDicebearSvg({ source: 'shapes', seed: 'x', options: { rotate: 45 } })
    expect(turned).not.toBe(plain)
  })

  it('turns an options object the library rejects into a PersonaDrawError the form can show', async () => {
    const err = await renderDicebearSvg({ source: 'shapes', seed: 'x', options: { flip: true } }).catch((e) => e)
    expect(err).toBeInstanceOf(PersonaDrawError)
    expect((err as PersonaDrawError).code).toBe('render')
  })

  it('refuses a style it does not ship, with a PersonaDrawError', async () => {
    await expect(renderDicebearSvg({ source: 'adventurer', seed: 'x' })).rejects.toBeInstanceOf(PersonaDrawError)
  })
})

describe('a Picsum photo fetched from the page', () => {
  const okBody = () => new Blob([new Uint8Array(8)], { type: 'image/jpeg' })
  const reply = (over: Partial<{ ok: boolean; url: string; type: string; blob: Blob }> = {}) =>
    ({
      ok: over.ok ?? true,
      url: over.url ?? 'https://fastly.picsum.photos/id/1/128/128.jpg',
      headers: { get: () => over.type ?? 'image/jpeg' },
      blob: async () => over.blob ?? okBody(),
    }) as unknown as Response
  const image = { width: 128, height: 128 } as CanvasImageSource & { width: number; height: number }

  beforeEach(() => {
    vi.mocked(rasterizeAvatar).mockClear()
    vi.stubGlobal('fetch', vi.fn(async () => reply()))
  })
  afterEach(() => vi.unstubAllGlobals())

  it('is fetched without credentials or referrer, and drawn', async () => {
    await drawRecipe({ source: 'photo', url: 'https://picsum.photos/seed/hiro/128' }, { loadImage: async () => image })
    expect(fetch).toHaveBeenCalledWith('https://picsum.photos/seed/hiro/128', expect.objectContaining({ credentials: 'omit', referrerPolicy: 'no-referrer' }))
    expect(rasterizeAvatar).toHaveBeenCalled()
  })

  it('is refused when the redirect landed off the list', async () => {
    vi.mocked(fetch).mockResolvedValue(reply({ url: 'https://internal.example/secret.png' }))
    const err = await drawRecipe({ source: 'photo', url: 'https://picsum.photos/x' }, { loadImage: async () => image }).catch((e) => e)
    expect((err as PersonaDrawError).code).toBe('bad-url')
    expect(rasterizeAvatar).not.toHaveBeenCalled()
  })

  it('is refused when it is not an image, or is over 2 MB', async () => {
    vi.mocked(fetch).mockResolvedValue(reply({ type: 'text/html' }))
    const notImage = await drawRecipe({ source: 'photo', url: 'https://picsum.photos/x' }, { loadImage: async () => image }).catch((e) => e)
    expect((notImage as PersonaDrawError).code).toBe('upload-needed')
    vi.mocked(fetch).mockResolvedValue(reply({ blob: new Blob([new Uint8Array(2 * 1024 * 1024 + 1)], { type: 'image/jpeg' }) }))
    const big = await drawRecipe({ source: 'photo', url: 'https://picsum.photos/x' }, { loadImage: async () => image }).catch((e) => e)
    expect((big as PersonaDrawError).code).toBe('upload-needed')
  })

  it('refuses a URL with an explicit port before any request', async () => {
    const err = await drawRecipe({ source: 'photo', url: 'https://picsum.photos:443/x' }, { loadImage: async () => image }).catch((e) => e)
    expect((err as PersonaDrawError).code).toBe('bad-url')
    expect(fetch).not.toHaveBeenCalled()
  })
})

describe('drawRecipe', () => {
  beforeEach(() => vi.mocked(rasterizeAvatar).mockClear())

  it('rasterises a DiceBear recipe at 128 px', async () => {
    const image = { width: 128, height: 128 } as CanvasImageSource & { width: number; height: number }
    const out = await drawRecipe({ source: 'micah', seed: 'Hiro' }, { loadImage: async () => image })
    expect(out).toBe('data:image/webp;base64,RENDERED')
    expect(rasterizeAvatar).toHaveBeenCalledWith(image, 128, 128)
  })

  it('draws an uploaded file', async () => {
    const image = { width: 300, height: 200 } as CanvasImageSource & { width: number; height: number }
    await drawRecipe({ source: 'upload' }, { loadImage: async () => image, file: new Blob(['x'], { type: 'image/png' }) })
    expect(rasterizeAvatar).toHaveBeenCalledWith(image, 300, 200)
  })

  it('asks for an upload when a photo URL cannot be fetched', async () => {
    const err = await drawRecipe({ source: 'photo', url: 'https://placecats.com/128/128' }, { ipcFetch: undefined }).catch((e) => e)
    expect(err).toBeInstanceOf(PersonaDrawError)
    expect((err as PersonaDrawError).code).toBe('upload-needed')
  })

  it('draws a photo that the main process fetched, and refuses a host off the list before any fetch', async () => {
    const image = { width: 128, height: 128 } as CanvasImageSource & { width: number; height: number }
    const ipcFetch = vi.fn(async () => 'data:image/jpeg;base64,/9j/4AAQ')
    await drawRecipe({ source: 'photo', url: 'https://placedog.net/128' }, { loadImage: async () => image, ipcFetch })
    expect(ipcFetch).toHaveBeenCalledWith('https://placedog.net/128')
    const bad = await drawRecipe({ source: 'photo', url: 'https://evil.example/a.png' }, { ipcFetch }).catch((e) => e)
    expect((bad as PersonaDrawError).code).toBe('bad-url')
    expect(ipcFetch).toHaveBeenCalledTimes(1)
  })
})
