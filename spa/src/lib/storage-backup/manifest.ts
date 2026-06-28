/**
 * manifest — builds the canonical backup manifest of the In-App `/buffer` tree.
 *
 * Walks the whole tree (files AND dirs, incl. empty dirs), hashes each file in
 * the browser (`sha256Hex`), records the subsystem-1 word metric (`wordCountFor`),
 * and produces:
 *   - `entries`: root-relative `{ path, kind, hash, size, words }`, sorted by the
 *     **UTF-8 bytes of `path`** (matching Go's string byte order — the daemon
 *     rejects any other order with `400 unsorted`, spec §4.1, R2-P2b).
 *   - `blobs`: a `hash → bytes` map of every distinct file content (deduped, so
 *     two identical-byte files upload their shared blob once, R1-P3a).
 *
 * Directory entries carry `kind:'dir', hash:'', size:0, words:0` so empty dirs
 * round-trip. The whole structure is the engine's input: its JSON is the no-op
 * key, its `blobs` drive negotiation + upload, its `entries` are the snapshot.
 */
import type { FsBackend } from '../fs-backend'
import { listTreeUnder, type TreeNode } from '../storage-tree'
import { STORAGE_ROOT, relativeToRoot } from '../storage-paths'
import { wordCountFor } from '../text-metrics'
import { sha256Hex } from '../crypto-hash'

export interface ManifestEntry {
  /** Root-relative path (e.g. `dir/b.md`), the entry identity. */
  path: string
  kind: 'file' | 'dir'
  /** sha256 hex for files; `''` for dirs. */
  hash: string
  /** Byte size for files; `0` for dirs. */
  size: number
  /** Word count for text files; `0` for binary / dirs. */
  words: number
}

export interface BuiltManifest {
  entries: ManifestEntry[]
  /** Distinct file contents keyed by hash (deduped across the tree). */
  blobs: Map<string, Uint8Array>
}

const encoder = new TextEncoder()

/**
 * Compare two paths by their UTF-8 byte sequences (Go string byte order). Used
 * as the manifest's canonical sort so a non-ASCII path lands exactly where the
 * daemon expects — a JS `localeCompare`/`<` (UTF-16) sort would pass client-side
 * but get a daemon `400 unsorted`.
 */
function byteCompare(a: string, b: string): number {
  const ba = encoder.encode(a)
  const bb = encoder.encode(b)
  const len = Math.min(ba.length, bb.length)
  for (let i = 0; i < len; i++) {
    if (ba[i] !== bb[i]) return ba[i] - bb[i]
  }
  return ba.length - bb.length
}

/** Build the canonical manifest + deduped blob set for the In-App tree. */
export async function buildManifest(backend: FsBackend): Promise<BuiltManifest> {
  const tree = await listTreeUnder(backend, STORAGE_ROOT)
  const entries: ManifestEntry[] = []
  const blobs = new Map<string, Uint8Array>()

  // Depth-first flatten; reading + hashing each file as we go.
  const visit = async (nodes: TreeNode[]): Promise<void> => {
    for (const node of nodes) {
      const path = relativeToRoot(node.path)
      if (node.isDir) {
        entries.push({ path, kind: 'dir', hash: '', size: 0, words: 0 })
        if (node.children) await visit(node.children)
      } else {
        const bytes = await backend.read(node.path)
        const hash = await sha256Hex(bytes)
        entries.push({
          path,
          kind: 'file',
          hash,
          size: bytes.byteLength,
          words: wordCountFor(node.path, bytes),
        })
        if (!blobs.has(hash)) blobs.set(hash, bytes)
      }
    }
  }
  await visit(tree)

  // Final independent canonical sort by UTF-8 bytes (NOT listTreeUnder's
  // dirs-first/localeCompare order) — the daemon's normal form (R2-P2b).
  entries.sort((a, b) => byteCompare(a.path, b.path))

  return { entries, blobs }
}
