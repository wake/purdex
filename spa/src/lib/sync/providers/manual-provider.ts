import type { SyncBundle, SyncProvider, SyncSnapshot } from '../types'

// ---------------------------------------------------------------------------
// ManualProvider — Export / Import
// ---------------------------------------------------------------------------

/**
 * A SyncProvider that performs no network or file-system I/O.
 * Its sole purpose is to allow the user to manually export a bundle to a JSON
 * file and import a previously-exported file back into the application.
 *
 * push / pull / pushChunks / pullChunks are intentional no-ops; history is
 * always empty because there is no persistent storage backing this provider.
 */
export interface ManualProvider extends SyncProvider {
  /** Serialise a bundle to a pretty-printed JSON Blob (type: application/json). */
  exportToBlob(bundle: SyncBundle): Blob
  /**
   * Parse a JSON string back into a SyncBundle.
   * Throws if the text is not valid JSON or if required fields are missing /
   * have the wrong type.
   */
  importFromText(text: string): SyncBundle
}

export function createManualProvider(): ManualProvider {
  return {
    id: 'manual',

    // -----------------------------------------------------------------------
    // SyncProvider — all no-ops
    // -----------------------------------------------------------------------

    // eslint-disable-next-line @typescript-eslint/no-unused-vars
    async push(bundle: SyncBundle): Promise<void> {
      // intentional no-op
    },

    async pull(): Promise<SyncBundle | null> {
      return null
    },

    // eslint-disable-next-line @typescript-eslint/no-unused-vars
    async pushChunks(chunks: Record<string, Uint8Array>): Promise<void> {
      // intentional no-op
    },

    // eslint-disable-next-line @typescript-eslint/no-unused-vars
    async pullChunks(hashes: string[]): Promise<Record<string, Uint8Array>> {
      return {}
    },

    // eslint-disable-next-line @typescript-eslint/no-unused-vars
    async listHistory(limit: number): Promise<SyncSnapshot[]> {
      return []
    },

    // -----------------------------------------------------------------------
    // Manual export / import
    // -----------------------------------------------------------------------

    exportToBlob(bundle: SyncBundle): Blob {
      const json = JSON.stringify(bundle, null, 2)
      return new Blob([json], { type: 'application/json' })
    },

    importFromText(text: string): SyncBundle {
      // Will throw a SyntaxError on invalid JSON — let it propagate.
      const parsed: unknown = JSON.parse(text)

      if (typeof parsed !== 'object' || parsed === null || Array.isArray(parsed)) {
        throw new Error('Invalid SyncBundle: expected a JSON object')
      }

      const obj = parsed as Record<string, unknown>

      if (typeof obj['version'] !== 'number') {
        throw new Error('Invalid SyncBundle: "version" must be a number')
      }

      if (typeof obj['timestamp'] !== 'number') {
        throw new Error('Invalid SyncBundle: "timestamp" must be a number')
      }

      if (typeof obj['device'] !== 'string') {
        throw new Error('Invalid SyncBundle: "device" must be a string')
      }

      if (typeof obj['collections'] !== 'object' || obj['collections'] === null || Array.isArray(obj['collections'])) {
        throw new Error('Invalid SyncBundle: "collections" must be an object')
      }

      return obj as unknown as SyncBundle
    },
  }
}
