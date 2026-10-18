// electron/avatar-fetch.ts — fetches a persona's photo for the page (name pool v2 plan P2). PlaceCats and PlaceDog send no
// CORS headers, so the renderer cannot read them; the main process can. That makes this the one place where a URL typed
// in the App turns into a request from the user's machine, so it is narrow: https only, three exact hosts (see
// `photo-allowlist.ts`), no credentials, every redirect hop checked by the same rule and never followed blindly, at most
// 2 MB, and only raster image types (an SVG is scriptable and is refused). The answer is a data URL for the page to
// draw into its canvas.
import { photoRedirectAllowed, photoRouteOf } from './photo-allowlist'

export const MAX_AVATAR_BYTES = 2 * 1024 * 1024
export const MAX_HOPS = 3
const TIMEOUT_MS = 15_000
const RASTER = /^image\/(png|jpeg|webp|gif|avif)$/
const REDIRECTS = new Set([301, 302, 303, 307, 308])

export type FetchLike = (
  url: string,
  init: { redirect: 'manual'; credentials: 'omit'; signal?: AbortSignal },
) => Promise<Response>

export interface AvatarFetchDeps {
  /** Electron's `net.fetch` in the App; a stub in tests. */
  fetch: FetchLike
}

export type AvatarFetchFailure = 'bad-url' | 'redirect' | 'status' | 'type' | 'size' | 'network'

export class AvatarFetchError extends Error {
  readonly code: AvatarFetchFailure
  constructor(code: AvatarFetchFailure, message: string) {
    super(message)
    this.name = 'AvatarFetchError'
    this.code = code
  }
}

/** The body, but never more than the cap: stops reading the moment it is over. */
async function readCapped(res: Response): Promise<Uint8Array> {
  const declared = Number(res.headers.get('content-length'))
  if (Number.isFinite(declared) && declared > MAX_AVATAR_BYTES) throw new AvatarFetchError('size', 'the photo is over 2 MB')
  if (!res.body) return new Uint8Array(await res.arrayBuffer()).slice(0, MAX_AVATAR_BYTES + 1)
  const reader = res.body.getReader()
  const chunks: Uint8Array[] = []
  let total = 0
  for (;;) {
    const { done, value } = await reader.read()
    if (done) break
    total += value.byteLength
    if (total > MAX_AVATAR_BYTES) {
      void reader.cancel()
      throw new AvatarFetchError('size', 'the photo is over 2 MB')
    }
    chunks.push(value)
  }
  return Buffer.concat(chunks)
}

export async function fetchAvatar(url: string, deps: AvatarFetchDeps): Promise<string> {
  if (photoRouteOf(url) === null) throw new AvatarFetchError('bad-url', 'this photo address is not allowed')
  let current = url
  for (let hop = 0; hop <= MAX_HOPS; hop++) {
    let res: Response
    try {
      res = await deps.fetch(current, { redirect: 'manual', credentials: 'omit', signal: AbortSignal.timeout(TIMEOUT_MS) })
    } catch {
      throw new AvatarFetchError('network', 'the photo could not be fetched')
    }
    if (REDIRECTS.has(res.status)) {
      void res.body?.cancel()
      const location = res.headers.get('location')
      let next: string
      try { next = new URL(location ?? '', current).toString() } catch { throw new AvatarFetchError('redirect', 'bad redirect') }
      // Each hop is judged like the first URL: a host that is allowed may still send us somewhere that is not.
      if (!location || !photoRedirectAllowed(next)) throw new AvatarFetchError('redirect', 'the photo redirected off the allowed list')
      current = next
      continue
    }
    if (res.status !== 200) throw new AvatarFetchError('status', `the photo host answered ${res.status}`)
    const type = (res.headers.get('content-type') ?? '').split(';')[0].trim().toLowerCase()
    if (!RASTER.test(type)) throw new AvatarFetchError('type', 'the address did not return a png/jpeg/webp/gif/avif image')
    const bytes = await readCapped(res)
    if (bytes.byteLength > MAX_AVATAR_BYTES) throw new AvatarFetchError('size', 'the photo is over 2 MB')
    return `data:${type};base64,${Buffer.from(bytes).toString('base64')}`
  }
  throw new AvatarFetchError('redirect', 'too many redirects')
}

/**
 * What the `avatar:fetch` channel calls. The argument comes from a renderer and is not trusted to be a string. A failure
 * is thrown as a plain `Error` whose message starts with the code: that is what survives `ipcRenderer.invoke`.
 */
export async function handleAvatarFetch(raw: unknown, deps: AvatarFetchDeps): Promise<string> {
  try {
    if (typeof raw !== 'string') throw new AvatarFetchError('bad-url', 'the address must be a string')
    return await fetchAvatar(raw, deps)
  } catch (e) {
    if (e instanceof AvatarFetchError) throw new Error(`avatar-fetch:${e.code}: ${e.message}`)
    throw new Error('avatar-fetch:network: the photo could not be fetched')
  }
}
