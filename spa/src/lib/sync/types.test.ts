import { describe, it, expect } from 'vitest'
// Runtime import to ensure the module actually exists and exports its symbols.
// The * import will fail at runtime if ./types does not resolve.
import * as SyncTypes from './types'
import type {
  FullPayload,
  ChunkedManifestEntry,
  ChunkedPayload,
  SyncBundle,
  SyncContributor,
  MergeStrategy,
  ResolvedFields,
  ConflictItem,
  SyncProvider,
  SyncState,
  SyncSnapshot,
} from './types'

// ---------------------------------------------------------------------------
// Type-level compile guards
// These assignments confirm that the interfaces are structurally correct.
// If any required field is missing the TypeScript compiler (via Vitest) will
// report an error and the test suite will fail.
// ---------------------------------------------------------------------------

describe('FullPayload', () => {
  it('has required fields: version and data', () => {
    const payload: FullPayload = {
      version: 1,
      data: { key: 'value' },
    }
    expect(payload.version).toBe(1)
    expect(payload.data).toEqual({ key: 'value' })
  })

  it('data is Record<string, unknown>', () => {
    const payload: FullPayload = {
      version: 2,
      data: { num: 42, nested: { a: true }, arr: [1, 2, 3] },
    }
    expect(typeof payload.data['num']).toBe('number')
    expect(payload.data['nested']).toBeDefined()
  })
})

describe('ChunkedManifestEntry', () => {
  it('has id and hash fields', () => {
    const entry: ChunkedManifestEntry = { id: 'chunk-0', hash: 'abc123' }
    expect(entry.id).toBe('chunk-0')
    expect(entry.hash).toBe('abc123')
  })
})

describe('ChunkedPayload', () => {
  it('has version, manifest and chunks', () => {
    const payload: ChunkedPayload = {
      version: 1,
      manifest: [{ id: 'chunk-0', hash: 'abc123' }],
      chunks: { abc123: new Uint8Array([1, 2, 3]) },
    }
    expect(payload.version).toBe(1)
    expect(payload.manifest).toHaveLength(1)
    expect(payload.chunks['abc123']).toBeInstanceOf(Uint8Array)
  })

  it('chunks is keyed by hash string', () => {
    const buf = new Uint8Array([10, 20])
    const payload: ChunkedPayload = {
      version: 3,
      manifest: [],
      chunks: { deadbeef: buf },
    }
    expect(payload.chunks['deadbeef']).toBe(buf)
  })
})

describe('SyncBundle', () => {
  it('has required fields: version, timestamp, device, collections', () => {
    const bundle: SyncBundle = {
      version: 1,
      timestamp: Date.now(),
      device: 'mlab',
      collections: {},
    }
    expect(bundle.version).toBe(1)
    expect(typeof bundle.timestamp).toBe('number')
    expect(bundle.device).toBe('mlab')
    expect(bundle.collections).toBeDefined()
  })

  it('collections maps contributor id to FullPayload', () => {
    const bundle: SyncBundle = {
      version: 1,
      timestamp: 1000,
      device: 'air',
      collections: {
        prefs: { version: 1, data: { theme: 'dark' } },
      },
    }
    const prefs = bundle.collections['prefs'] as FullPayload
    expect(prefs.data['theme']).toBe('dark')
  })

  it('collections maps contributor id to ChunkedPayload', () => {
    const bundle: SyncBundle = {
      version: 1,
      timestamp: 1000,
      device: 'air',
      collections: {
        editor: {
          version: 1,
          manifest: [{ id: 'c0', hash: 'ff00' }],
          chunks: { ff00: new Uint8Array([0xff]) },
        },
      },
    }
    const editor = bundle.collections['editor'] as ChunkedPayload
    expect(editor.manifest[0]?.hash).toBe('ff00')
  })
})

describe('MergeStrategy', () => {
  it('type full-replace is valid', () => {
    const s: MergeStrategy = { type: 'full-replace' }
    expect(s.type).toBe('full-replace')
  })

  it('type field-merge carries ResolvedFields', () => {
    const resolved: ResolvedFields = { theme: 'remote', fontSize: 'local' }
    const s: MergeStrategy = { type: 'field-merge', resolved }
    expect(s.type).toBe('field-merge')
    if (s.type === 'field-merge') {
      expect(s.resolved['theme']).toBe('remote')
      expect(s.resolved['fontSize']).toBe('local')
    }
  })
})

describe('ResolvedFields', () => {
  it('maps field names to local or remote', () => {
    const r: ResolvedFields = { a: 'local', b: 'remote' }
    expect(r['a']).toBe('local')
    expect(r['b']).toBe('remote')
  })
})

describe('ConflictItem', () => {
  it('captures three-way values: lastSynced, local, remote', () => {
    const conflict: ConflictItem = {
      contributor: 'prefs',
      field: 'theme',
      lastSynced: 'light',
      local: 'dark',
      remote: { value: 'system', device: 'air' },
    }
    expect(conflict.contributor).toBe('prefs')
    expect(conflict.field).toBe('theme')
    expect(conflict.lastSynced).toBe('light')
    expect(conflict.local).toBe('dark')
    expect(conflict.remote.value).toBe('system')
    expect(conflict.remote.device).toBe('air')
  })

  it('values are typed as unknown (accepts any)', () => {
    const conflict: ConflictItem = {
      contributor: 'state',
      field: 'count',
      lastSynced: 0,
      local: 42,
      remote: { value: { nested: true }, device: 'mlab' },
    }
    expect(conflict.local).toBe(42)
    expect(conflict.remote.value).toEqual({ nested: true })
  })
})

describe('SyncState', () => {
  it('has nullable fields that default to null', () => {
    const state: SyncState = {
      lastSyncedBundle: null,
      lastSyncedAt: null,
      activeProviderId: null,
      enabledModules: [],
    }
    expect(state.lastSyncedBundle).toBeNull()
    expect(state.lastSyncedAt).toBeNull()
    expect(state.activeProviderId).toBeNull()
    expect(state.enabledModules).toHaveLength(0)
  })

  it('holds a bundle when synced', () => {
    const bundle: SyncBundle = {
      version: 1,
      timestamp: 9999,
      device: 'mlab',
      collections: {},
    }
    const state: SyncState = {
      lastSyncedBundle: bundle,
      lastSyncedAt: 9999,
      activeProviderId: 'manual',
      enabledModules: ['prefs', 'editor'],
    }
    expect(state.lastSyncedBundle).toBe(bundle)
    expect(state.enabledModules).toContain('prefs')
  })
})

describe('SyncSnapshot', () => {
  it('has required fields including source and bundleRef', () => {
    const snap: SyncSnapshot = {
      id: 'snap-1',
      timestamp: 1000,
      device: 'mlab',
      source: 'local',
      trigger: 'manual',
      bundleRef: 'bundle-abc',
    }
    expect(snap.source).toBe('local')
    expect(snap.bundleRef).toBe('bundle-abc')
  })

  it('source can be remote', () => {
    const snap: SyncSnapshot = {
      id: 'snap-2',
      timestamp: 2000,
      device: 'air',
      source: 'remote',
      trigger: 'auto',
      bundleRef: 'bundle-xyz',
    }
    expect(snap.source).toBe('remote')
    expect(snap.trigger).toBe('auto')
  })
})

describe('SyncContributor interface shape', () => {
  it('can be implemented with required methods', () => {
    const contributor: SyncContributor = {
      id: 'prefs',
      strategy: 'full',
      serialize(): FullPayload {
        return { version: 1, data: {} }
      },
      deserialize(): void {},
      getVersion(): number {
        return 1
      },
    }
    expect(contributor.id).toBe('prefs')
    expect(contributor.strategy).toBe('full')
    expect(contributor.getVersion()).toBe(1)
    const payload = contributor.serialize() as FullPayload
    expect(payload.version).toBe(1)
  })

  it('migrate is optional', () => {
    const withMigrate: SyncContributor = {
      id: 'editor',
      strategy: 'content-addressed',
      serialize(): ChunkedPayload {
        return { version: 2, manifest: [], chunks: {} }
      },
      deserialize(): void {},
      getVersion(): number {
        return 2
      },
      migrate(payload: unknown): unknown {
        return payload
      },
    }
    expect(withMigrate.migrate).toBeDefined()
  })
})

describe('module exports', () => {
  it('types module is importable (runtime smoke test)', () => {
    // If ./types does not exist this import * will throw at module load time
    // and every test in this file will fail.  This assertion confirms the
    // module resolved successfully.
    expect(SyncTypes).toBeDefined()
  })
})

describe('SyncProvider interface shape', () => {
  it('has required async methods', async () => {
    const bundle: SyncBundle = {
      version: 1,
      timestamp: 0,
      device: 'test',
      collections: {},
    }
    const provider: SyncProvider = {
      id: 'test-provider',
      async push(): Promise<void> {},
      async pull(): Promise<SyncBundle | null> {
        return bundle
      },
      async pushChunks(): Promise<void> {},
      async pullChunks(): Promise<Record<string, Uint8Array>> {
        return {}
      },
      async listHistory(): Promise<SyncSnapshot[]> {
        return []
      },
    }
    expect(provider.id).toBe('test-provider')
    const result = await provider.pull()
    expect(result).toBe(bundle)
    const history = await provider.listHistory(10)
    expect(history).toHaveLength(0)
  })
})
