// electron/photo-allowlist.ts — which photo URLs the main process may fetch for the persona page (name pool v2 plan P2).
// A COPY of spa/src/lib/names/photo-allowlist.ts from the first constant on: this tsconfig's rootDir is `electron/`, so it
// cannot import from `spa/`. `photo-allowlist.parity.test.ts` keeps the two identical. The main process applies it to the
// first URL and to every redirect hop.
//
// The URL is judged on the raw string as well as on the parsed one. `new URL` normalises away things the policy bans
// (an explicit `:443`, backslashes turned into slashes, a trailing dot kept apart), so the authority is read from the
// text before parsing.

const FETCH_HOSTS: ReadonlySet<string> = new Set(['picsum.photos'])
const IPC_HOSTS: ReadonlySet<string> = new Set(['placecats.com', 'placedog.net'])
/** Where Picsum's own redirect lands (it serves the image from its CDN). Only ever a redirect target. */
const REDIRECT_ONLY_HOSTS: ReadonlySet<string> = new Set(['fastly.picsum.photos'])

/**
 * The host of an `https://host/…` string when its authority is plain: no userinfo, no port, no backslash, no
 * whitespace or control characters, and nothing but lowercase letters, digits, dots and hyphens in the host (so no IDN
 * or look-alike character reaches the comparison). `null` otherwise.
 */
function plainHttpsHost(url: string): string | null {
  if (!url.startsWith('https://') && !url.startsWith('HTTPS://') && !/^https:\/\//i.test(url)) return null
  // eslint-disable-next-line no-control-regex
  if (/[\s\\\u0000-\u001f\u007f-\u009f]/.test(url)) return null
  const rest = url.slice('https://'.length)
  const authority = rest.split(/[/?#]/, 1)[0]
  if (authority === '' || /[@:[\]]/.test(authority)) return null
  const host = authority.toLowerCase()
  if (!/^[a-z0-9-]+(\.[a-z0-9-]+)+$/.test(host)) return null // also refuses a trailing dot
  let parsed: URL
  try { parsed = new URL(url) } catch { return null }
  return parsed.hostname === host && parsed.port === '' ? host : null
}

/**
 * How `url` may be fetched: `fetch` from the page (Picsum sends CORS headers), `ipc` through the Electron main
 * process (PlaceCats and PlaceDog do not), or `null` when it may not be fetched at all.
 */
export function photoRouteOf(url: string): 'fetch' | 'ipc' | null {
  const host = plainHttpsHost(url)
  if (host === null) return null
  if (FETCH_HOSTS.has(host)) return 'fetch'
  if (IPC_HOSTS.has(host)) return 'ipc'
  return null
}

/** True for a URL a redirect may land on: the allowlist, plus the CDN host Picsum redirects to. */
export function photoRedirectAllowed(url: string): boolean {
  const host = plainHttpsHost(url)
  return host !== null && (FETCH_HOSTS.has(host) || IPC_HOSTS.has(host) || REDIRECT_ONLY_HOSTS.has(host))
}
