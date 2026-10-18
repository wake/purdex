import { describe, it, expect, vi, beforeEach } from 'vitest'

vi.mock('./persona-raster', () => ({ rasterizeAvatar: vi.fn(() => 'data:image/webp;base64,RENDERED'), AVATAR_PX: 128 }))

import { rasterizeAvatar } from './persona-raster'
import {
  DICEBEAR_STYLES, renderDicebearSvg, photoRouteOf, drawRecipe, PersonaDrawError,
} from './persona-draw'

describe('renderDicebearSvg', () => {
  it('draws an SVG for each of the six styles, deterministically from the seed', async () => {
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

describe('photoRouteOf', () => {
  it('routes https URLs of the allowed hosts: Picsum straight, the others through the main process', () => {
    expect(photoRouteOf('https://picsum.photos/seed/hiro/128')).toBe('fetch')
    expect(photoRouteOf('https://placecats.com/128/128')).toBe('ipc')
    expect(photoRouteOf('https://placedog.net/128')).toBe('ipc')
  })

  it('refuses anything else: http, other hosts, look-alike hosts, junk', () => {
    expect(photoRouteOf('http://picsum.photos/x')).toBeNull()
    expect(photoRouteOf('https://example.com/a.png')).toBeNull()
    expect(photoRouteOf('https://picsum.photos.evil.com/x')).toBeNull()
    expect(photoRouteOf('https://evil.com/?u=https://picsum.photos/')).toBeNull()
    expect(photoRouteOf('https://user@picsum.photos/x')).toBeNull()
    expect(photoRouteOf('not a url')).toBeNull()
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
