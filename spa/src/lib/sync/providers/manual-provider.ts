import type { SyncBundle, SyncProvider, SyncSnapshot } from '../types'
import { objectDepth } from '../../object-depth'

// ---------------------------------------------------------------------------
// ImportError — typed errors from importFromText so UI can translate per code
// ---------------------------------------------------------------------------

export type ImportErrorCode = 'too-large' | 'too-deep' | 'invalid-json' | 'invalid-shape'

export class ImportError extends Error {
  code: ImportErrorCode
  constructor(code: ImportErrorCode, message: string) {
    super(message)
    this.name = 'ImportError'
    this.code = code
  }
}

const MAX_BYTES = 5 * 1024 * 1024
const MAX_DEPTH = 32

// ---------------------------------------------------------------------------
// ManualProvider — Export / Import
// ---------------------------------------------------------------------------

export interface ManualProvider extends SyncProvider {
  exportToBlob(bundle: SyncBundle): Blob
  importFromText(text: string): SyncBundle
}

export function createManualProvider(): ManualProvider {
  return {
    id: 'manual',

    // eslint-disable-next-line @typescript-eslint/no-unused-vars
    async push(bundle: SyncBundle): Promise<void> {},
    async pull(): Promise<SyncBundle | null> { return null },
    // eslint-disable-next-line @typescript-eslint/no-unused-vars
    async pushChunks(chunks: Record<string, Uint8Array>): Promise<void> {},
    // eslint-disable-next-line @typescript-eslint/no-unused-vars
    async pullChunks(hashes: string[]): Promise<Record<string, Uint8Array>> { return {} },
    // eslint-disable-next-line @typescript-eslint/no-unused-vars
    async listHistory(limit: number): Promise<SyncSnapshot[]> { return [] },

    exportToBlob(bundle: SyncBundle): Blob {
      const json = JSON.stringify(bundle, null, 2)
      return new Blob([json], { type: 'application/json' })
    },

    importFromText(text: string): SyncBundle {
      if (text.length > MAX_BYTES) {
        throw new ImportError('too-large', `bundle too large (${text.length} bytes > ${MAX_BYTES})`)
      }

      let parsed: unknown
      try {
        parsed = JSON.parse(text)
      } catch (e) {
        throw new ImportError('invalid-json', (e as Error).message)
      }

      try {
        objectDepth(parsed, MAX_DEPTH)
      } catch {
        throw new ImportError('too-deep', `bundle depth exceeds ${MAX_DEPTH}`)
      }

      if (typeof parsed !== 'object' || parsed === null || Array.isArray(parsed)) {
        throw new ImportError('invalid-shape', 'Invalid SyncBundle: expected a JSON object')
      }

      const obj = parsed as Record<string, unknown>

      if (typeof obj['version'] !== 'number') {
        throw new ImportError('invalid-shape', 'Invalid SyncBundle: "version" must be a number')
      }
      if (typeof obj['timestamp'] !== 'number') {
        throw new ImportError('invalid-shape', 'Invalid SyncBundle: "timestamp" must be a number')
      }
      if (typeof obj['device'] !== 'string') {
        throw new ImportError('invalid-shape', 'Invalid SyncBundle: "device" must be a string')
      }
      if (typeof obj['collections'] !== 'object' || obj['collections'] === null || Array.isArray(obj['collections'])) {
        throw new ImportError('invalid-shape', 'Invalid SyncBundle: "collections" must be an object')
      }

      return obj as unknown as SyncBundle
    },
  }
}
