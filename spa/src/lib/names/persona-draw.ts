// spa/src/lib/names/persona-draw.ts — draws a persona's avatar from its recipe into the 128 px raster that is stored
// (name pool v2 spec §9.1). DiceBear is imported only here and only dynamically, so it lives in the persona page's lazy
// chunk and never reaches the main one (plan P3); `persona-draw-guard.test.ts` holds that line.
import { rasterizeAvatar } from './persona-raster'
import type { PersonaAvatarRecipe } from './persona-types'

export const DICEBEAR_STYLES = ['lorelei', 'notionists', 'open-peeps', 'micah', 'shapes', 'clay'] as const
export type DicebearStyle = (typeof DICEBEAR_STYLES)[number]

export type DrawFailure = 'bad-style' | 'render' | 'bad-url' | 'upload-needed' | 'image'

export class PersonaDrawError extends Error {
  readonly code: DrawFailure
  constructor(code: DrawFailure, message: string) {
    super(message)
    this.name = 'PersonaDrawError'
    this.code = code
  }
}

/** One `import()` per style, so a bundler splits each definition (12–373 KB) into its own chunk. */
const STYLE_LOADERS: Record<DicebearStyle, () => Promise<{ default: unknown }>> = {
  lorelei: () => import('@dicebear/styles/lorelei.json'),
  notionists: () => import('@dicebear/styles/notionists.json'),
  'open-peeps': () => import('@dicebear/styles/open-peeps.json'),
  micah: () => import('@dicebear/styles/micah.json'),
  shapes: () => import('@dicebear/styles/shapes.json'),
  clay: () => import('@dicebear/styles/clay.json'),
}

function isStyle(s: string): s is DicebearStyle {
  return (DICEBEAR_STYLES as readonly string[]).includes(s)
}

/** The SVG DiceBear draws for `recipe.source` (a style id), `recipe.seed` and `recipe.options`. */
export async function renderDicebearSvg(recipe: PersonaAvatarRecipe): Promise<string> {
  if (!isStyle(recipe.source)) throw new PersonaDrawError('bad-style', `unknown avatar style: ${recipe.source}`)
  try {
    const [{ Avatar }, definition] = await Promise.all([import('@dicebear/core'), STYLE_LOADERS[recipe.source]()])
    // The size is ours, not the recipe's: the raster is always 128 px.
    const avatar = new Avatar(definition.default as never, { ...recipe.options, seed: recipe.seed ?? '', size: 128 } as never)
    return avatar.toString()
  } catch (e) {
    throw new PersonaDrawError('render', e instanceof Error ? e.message : String(e))
  }
}

const PHOTO_FETCH_HOSTS = new Set(['picsum.photos'])
const PHOTO_IPC_HOSTS = new Set(['placecats.com', 'placedog.net'])

/**
 * How a photo URL may be fetched: `fetch` from the page (Picsum sends CORS headers), `ipc` through the Electron main
 * process (PlaceCats and PlaceDog do not), or `null` when it may not be fetched at all. https only, an allowed host
 * exactly, no credentials or port in the URL. The main process repeats this check; this one only saves a round trip.
 */
export function photoRouteOf(url: string): 'fetch' | 'ipc' | null {
  let u: URL
  try { u = new URL(url) } catch { return null }
  if (u.protocol !== 'https:' || u.username !== '' || u.password !== '' || u.port !== '') return null
  if (PHOTO_FETCH_HOSTS.has(u.hostname)) return 'fetch'
  if (PHOTO_IPC_HOSTS.has(u.hostname)) return 'ipc'
  return null
}

export type DrawableImage = CanvasImageSource & { width: number; height: number }

export interface DrawDeps {
  loadImage?: (src: string) => Promise<DrawableImage>
  /** The main-process fetch (`avatar:fetch`): a data URL. Absent outside the App, or before the IPC exists. */
  ipcFetch?: (url: string) => Promise<string>
  /** The file for a `source: 'upload'` recipe. */
  file?: Blob
}

function loadImageFromUrl(src: string): Promise<DrawableImage> {
  return new Promise((resolve, reject) => {
    const img = new Image()
    img.onload = () => resolve(img)
    img.onerror = () => reject(new PersonaDrawError('image', 'the image could not be decoded'))
    img.src = src
  })
}

function blobToDataUrl(blob: Blob): Promise<string> {
  return new Promise((resolve, reject) => {
    const r = new FileReader()
    r.onload = () => resolve(String(r.result))
    r.onerror = () => reject(new PersonaDrawError('image', 'the file could not be read'))
    r.readAsDataURL(blob)
  })
}

function appIpcFetch(): DrawDeps['ipcFetch'] {
  return (window as { electronAPI?: { fetchAvatar?: (url: string) => Promise<string> } }).electronAPI?.fetchAvatar
}

async function photoDataUrl(url: string, deps: DrawDeps): Promise<string> {
  const route = photoRouteOf(url)
  if (route === null) throw new PersonaDrawError('bad-url', 'this photo address is not allowed')
  if (route === 'fetch') {
    try {
      const res = await fetch(url)
      if (!res.ok) throw new Error(String(res.status))
      return await blobToDataUrl(await res.blob())
    } catch {
      throw new PersonaDrawError('upload-needed', 'the photo could not be fetched; upload a file instead')
    }
  }
  const viaApp = 'ipcFetch' in deps ? deps.ipcFetch : appIpcFetch()
  if (!viaApp) throw new PersonaDrawError('upload-needed', 'this photo source needs the App; upload a file instead')
  try {
    return await viaApp(url)
  } catch {
    throw new PersonaDrawError('upload-needed', 'the photo could not be fetched; upload a file instead')
  }
}

/** The data URL to store: `recipe` drawn at 128 px, WebP (PNG when WebP cannot be encoded), within 32 KB. */
export async function drawRecipe(recipe: PersonaAvatarRecipe, deps: DrawDeps = {}): Promise<string> {
  const load = deps.loadImage ?? loadImageFromUrl
  let src: string
  if (recipe.source === 'upload') {
    if (!deps.file) throw new PersonaDrawError('image', 'no file was chosen')
    src = await blobToDataUrl(deps.file)
  } else if (recipe.source === 'photo') {
    src = await photoDataUrl(recipe.url ?? '', deps)
  } else {
    src = `data:image/svg+xml;charset=utf-8,${encodeURIComponent(await renderDicebearSvg(recipe))}`
  }
  const image = await load(src)
  return rasterizeAvatar(image, image.width, image.height)
}
