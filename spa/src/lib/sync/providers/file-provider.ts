import type { SyncBundle, SyncProvider, SyncSnapshot } from '../types'

// ---------------------------------------------------------------------------
// FileSystemIpc — abstraction over Electron's filesystem IPC
// ---------------------------------------------------------------------------

export interface FileSystemIpc {
  readFile(path: string): Promise<string>
  writeFile(path: string, content: string): Promise<void>
  readdir(path: string): Promise<string[]>
  mkdir(path: string): Promise<void>
}

// ---------------------------------------------------------------------------
// FileProvider — iCloud / Syncthing sync folder
// ---------------------------------------------------------------------------

/**
 * A SyncProvider backed by a local sync folder (e.g. iCloud Drive or
 * Syncthing).  All filesystem operations are performed through a
 * `FileSystemIpc` abstraction so the implementation is testable without a
 * real Electron process.
 *
 * Folder layout:
 *   {syncFolder}/
 *   ├── manifest.json        ← latest bundle
 *   ├── history/             ← per-sync snapshots
 *   │   └── <ISO-ts>.json
 *   └── chunks/              ← content-addressed blobs
 *       └── <hash>.bin
 */
export function createFileProvider(syncFolder: string, fs: FileSystemIpc): SyncProvider {
  const historyDir = `${syncFolder}/history`
  const chunksDir = `${syncFolder}/chunks`
  const manifestPath = `${syncFolder}/manifest.json`

  // -------------------------------------------------------------------------
  // Helpers
  // -------------------------------------------------------------------------

  /**
   * Create the three directories if they don't already exist.
   * mkdir errors (e.g. EEXIST) are silently swallowed.
   */
  async function ensureDirs(): Promise<void> {
    await Promise.all([
      fs.mkdir(syncFolder).catch(() => undefined),
      fs.mkdir(historyDir).catch(() => undefined),
      fs.mkdir(chunksDir).catch(() => undefined),
    ])
  }

  /** Convert a Date to a filename-safe ISO string: colons → hyphens. */
  function toFilename(date: Date): string {
    // e.g. "2026-04-16T12:00:00.000Z" → "2026-04-16T12-00-00-000Z"
    return date.toISOString().replace(/:/g, '-').replace('.', '-')
  }

  // -------------------------------------------------------------------------
  // SyncProvider
  // -------------------------------------------------------------------------

  return {
    id: 'file',

    async push(bundle: SyncBundle): Promise<void> {
      await ensureDirs()

      const json = JSON.stringify(bundle, null, 2)

      // Write latest manifest
      await fs.writeFile(manifestPath, json)

      // Write history snapshot
      const filename = `${toFilename(new Date())}.json`
      await fs.writeFile(`${historyDir}/${filename}`, json)
    },

    async pull(): Promise<SyncBundle | null> {
      try {
        const text = await fs.readFile(manifestPath)
        return JSON.parse(text) as SyncBundle
      } catch (err) {
        if (isEnoent(err)) return null
        throw err
      }
    },

    async pushChunks(chunks: Record<string, Uint8Array>): Promise<void> {
      const hashes = Object.keys(chunks)
      if (hashes.length === 0) return

      await ensureDirs()

      await Promise.all(
        hashes.map((hash) => {
          const b64 = Buffer.from(chunks[hash]).toString('base64')
          return fs.writeFile(`${chunksDir}/${hash}.bin`, b64)
        }),
      )
    },

    async pullChunks(hashes: string[]): Promise<Record<string, Uint8Array>> {
      const result: Record<string, Uint8Array> = {}

      await Promise.all(
        hashes.map(async (hash) => {
          try {
            const b64 = await fs.readFile(`${chunksDir}/${hash}.bin`)
            result[hash] = new Uint8Array(Buffer.from(b64, 'base64'))
          } catch (err) {
            if (isEnoent(err)) return // skip missing chunks
            throw err
          }
        }),
      )

      return result
    },

    async listHistory(limit: number): Promise<SyncSnapshot[]> {
      const entries = await fs.readdir(historyDir)

      const snapshots: SyncSnapshot[] = entries
        .filter((name) => name.endsWith('.json'))
        .sort()
        .reverse()
        .slice(0, limit)
        .map((name) => {
          // Restore ISO timestamp from filename: hyphens in time part → colons
          // "2026-04-16T12-00-00-000Z" → "2026-04-16T12:00:00.000Z"
          const iso = name
            .replace(/\.json$/, '')
            .replace(/T(\d{2})-(\d{2})-(\d{2})-(\d{3})Z$/, 'T$1:$2:$3.$4Z')
          const timestamp = new Date(iso).getTime()
          const bundleRef = `${historyDir}/${name}`

          return {
            id: name,
            timestamp: isNaN(timestamp) ? 0 : timestamp,
            device: '',
            source: 'remote',
            trigger: 'auto',
            bundleRef,
          } satisfies SyncSnapshot
        })

      return snapshots
    },
  }
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

function isEnoent(err: unknown): boolean {
  return (
    typeof err === 'object' &&
    err !== null &&
    'code' in err &&
    (err as { code: unknown }).code === 'ENOENT'
  )
}
