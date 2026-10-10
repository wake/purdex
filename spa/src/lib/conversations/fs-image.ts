// spa/src/lib/conversations/fs-image.ts — an image that lives on the HOST (a path the daemon saved an upload at, or one a message
// names) read through the daemon's `POST /api/fs/read` into a blob: URL for a thumbnail. Pinned to the given host (no fallback
// to another one), 10 MB at most (the daemon answers 413 beyond that), and shared: one read per (host, path) however many
// thumbnails show it, kept while any shows it and for a few more after (an LRU of unused ones), then revoked.
import { useEffect, useReducer } from 'react'
import { createDaemonBackendForHost } from '../fs-backend-daemon'

export const FS_IMAGE_MAX_BYTES = 10 * 1024 * 1024
const KEEP_UNUSED = 24

export type FsImage = { status: 'idle' } | { status: 'loading' } | { status: 'ready'; url: string } | { status: 'failed' }

const MIME: Record<string, string> = {
  png: 'image/png', jpg: 'image/jpeg', jpeg: 'image/jpeg', gif: 'image/gif', webp: 'image/webp', bmp: 'image/bmp',
  avif: 'image/avif', tif: 'image/tiff', tiff: 'image/tiff', heic: 'image/heic', heif: 'image/heif', svg: 'image/svg+xml',
}
const mimeOf = (path: string): string => MIME[path.slice(path.lastIndexOf('.') + 1).toLowerCase()] ?? 'application/octet-stream'

interface Entry { snap: FsImage; refs: number; subs: Set<() => void> }
const entries = new Map<string, Entry>()
/** Keys with no user, oldest first. */
let unused: string[] = []
const IDLE: FsImage = { status: 'idle' }

const keyOf = (hostId: string, path: string) => `${hostId}\0${path}`
const set = (e: Entry, snap: FsImage) => { e.snap = snap; for (const f of [...e.subs]) f() }

function drop(key: string): void {
  const e = entries.get(key)
  if (!e) return
  if (e.snap.status === 'ready' && typeof URL.revokeObjectURL === 'function') URL.revokeObjectURL(e.snap.url)
  entries.delete(key)
}

async function load(key: string, e: Entry, hostId: string, path: string): Promise<void> {
  set(e, { status: 'loading' })
  try {
    const bytes = await createDaemonBackendForHost(hostId).read(path)
    if (entries.get(key) !== e) return // dropped while the read was on its way
    if (bytes.byteLength > FS_IMAGE_MAX_BYTES || typeof URL.createObjectURL !== 'function') return set(e, { status: 'failed' })
    set(e, { status: 'ready', url: URL.createObjectURL(new Blob([bytes as BlobPart], { type: mimeOf(path) })) })
  } catch {
    if (entries.get(key) === e) set(e, { status: 'failed' })
  }
}

/** Takes a share of the image at (host, path), starting the read if nobody has; `onChange` hears every change. Returns the release. */
function acquire(hostId: string, path: string, onChange: () => void): () => void {
  const key = keyOf(hostId, path)
  let e = entries.get(key)
  if (!e) {
    e = { snap: IDLE, refs: 0, subs: new Set() }
    entries.set(key, e)
    void load(key, e, hostId, path)
  }
  const mine = e
  mine.refs++
  mine.subs.add(onChange)
  unused = unused.filter((k) => k !== key)
  return () => {
    mine.subs.delete(onChange)
    if (--mine.refs > 0 || entries.get(key) !== mine) return
    unused.push(key)
    while (unused.length > KEEP_UNUSED) drop(unused.shift() as string)
  }
}

const LOADING: FsImage = { status: 'loading' }

/** The image at `path` on `hostId`; `enabled` false reads nothing (idle). */
export function useFsImage(hostId: string, path: string, enabled = true): FsImage {
  const [, bump] = useReducer((n: number) => n + 1, 0)
  useEffect(() => (enabled ? acquire(hostId, path, bump) : undefined), [hostId, path, enabled])
  if (!enabled) return IDLE
  return entries.get(keyOf(hostId, path))?.snap ?? LOADING // the first render comes before the effect starts the read
}

/** Tests only: module state outlives a test. */
export function clearFsImages(): void {
  for (const key of [...entries.keys()]) drop(key)
  unused = []
}
