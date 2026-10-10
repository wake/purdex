// spa/src/lib/conversations/fs-image.ts — an image that lives on the HOST (a path the daemon saved an upload at, or one a message
// names) read through the daemon's `POST /api/fs/read` into a blob: URL for a thumbnail. Pinned to the given host (no fallback
// to another one), 10 MB at most (the daemon answers 413 beyond that), and shared: one read per (host, path) however many
// thumbnails show it, kept while any shows it and for a few more after (an LRU of unused ones), then revoked.
import { useEffect, useReducer } from 'react'
import { createDaemonBackendForHost } from '../fs-backend-daemon'
import { isReadablePath, sniffImageMime } from './image-sniff'

export const FS_IMAGE_MAX_BYTES = 10 * 1024 * 1024
const KEEP_UNUSED = 24

export type FsImage = { status: 'idle' } | { status: 'loading' } | { status: 'ready'; url: string } | { status: 'failed' }

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
  // a device, process or system node is never read
  if (!isReadablePath(path)) return set(e, { status: 'failed' })
  set(e, { status: 'loading' })
  try {
    const bytes = await createDaemonBackendForHost(hostId).read(path)
    if (entries.get(key) !== e) return // dropped while the read was on its way
    // shown only when the bytes ARE an image, typed by what they are (not by the name)
    const mime = sniffImageMime(bytes)
    if (bytes.byteLength > FS_IMAGE_MAX_BYTES || !mime || typeof URL.createObjectURL !== 'function') return set(e, { status: 'failed' })
    set(e, { status: 'ready', url: URL.createObjectURL(new Blob([bytes as BlobPart], { type: mime })) })
  } catch {
    if (entries.get(key) === e) set(e, { status: 'failed' })
  }
}

/** Takes a share of the image at (host, path), starting the read if nobody has; `onChange` hears every change. Returns the release. */
function acquire(hostId: string, path: string, onChange: () => void): () => void {
  const key = keyOf(hostId, path)
  const fresh = !entries.has(key)
  const mine = entries.get(key) ?? { snap: IDLE, refs: 0, subs: new Set<() => void>() }
  entries.set(key, mine)
  mine.refs++
  mine.subs.add(onChange)
  unused = unused.filter((k) => k !== key)
  if (fresh) void load(key, mine, hostId, path) // after the subscription: even an instant failure is heard
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
