// =============================================================================
// Sync Architecture — SyncEngine Tests
// =============================================================================

import { describe, it, expect, beforeEach } from 'vitest'
import { createSyncEngine } from './engine'
import type {
  SyncContributor,
  SyncProvider,
  SyncBundle,
  FullPayload,
  MergeStrategy,
} from './types'

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

/**
 * Creates a mock SyncContributor with controllable internal state.
 * Tracks serialize/deserialize calls.
 */
function createMockContributor(
  id: string,
  data: Record<string, unknown>,
): SyncContributor & {
  _data: Record<string, unknown>
  _deserializeCalls: Array<{ payload: unknown; merge: MergeStrategy }>
} {
  let internalData = { ...data }
  const deserializeCalls: Array<{ payload: unknown; merge: MergeStrategy }> = []

  return {
    id,
    strategy: 'full' as const,
    _data: internalData,
    _deserializeCalls: deserializeCalls,

    serialize(): FullPayload {
      return { version: 1, data: { ...internalData } }
    },

    deserialize(payload: unknown, merge: MergeStrategy): void {
      deserializeCalls.push({ payload, merge })
      const fp = payload as FullPayload
      internalData = { ...fp.data }
      // Update reference too
      Object.assign(this._data, internalData)
    },

    getVersion(): number {
      return 1
    },
  }
}

/**
 * Creates a mock SyncProvider with configurable pull bundle.
 */
function createMockProvider(pullBundle: SyncBundle | null = null): SyncProvider & {
  pushedBundles: SyncBundle[]
  setPullBundle(b: SyncBundle | null): void
} {
  const pushedBundles: SyncBundle[] = []
  let currentPullBundle = pullBundle

  return {
    id: 'mock-provider',
    pushedBundles,

    setPullBundle(b: SyncBundle | null) {
      currentPullBundle = b
    },

    async push(bundle: SyncBundle): Promise<void> {
      pushedBundles.push(bundle)
    },

    async pull(): Promise<SyncBundle | null> {
      return currentPullBundle
    },

    async pushChunks(): Promise<void> {},
    async pullChunks(): Promise<Record<string, Uint8Array>> {
      return {}
    },
    async listHistory() {
      return []
    },
  }
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

describe('createSyncEngine', () => {
  let engine: ReturnType<typeof createSyncEngine>

  beforeEach(() => {
    engine = createSyncEngine()
  })

  // -------------------------------------------------------------------------
  // register / getContributors
  // -------------------------------------------------------------------------

  it('registers contributors and returns them via getContributors', () => {
    const c1 = createMockContributor('settings', { theme: 'dark' })
    const c2 = createMockContributor('workspaces', { list: [] })

    engine.register(c1)
    engine.register(c2)

    const contributors = engine.getContributors()
    expect(contributors).toHaveLength(2)
    expect(contributors.map((c) => c.id)).toContain('settings')
    expect(contributors.map((c) => c.id)).toContain('workspaces')
  })

  it('overwrites contributor with the same id on re-register', () => {
    const c1 = createMockContributor('settings', { theme: 'dark' })
    const c2 = createMockContributor('settings', { theme: 'light' })

    engine.register(c1)
    engine.register(c2)

    expect(engine.getContributors()).toHaveLength(1)
    expect(engine.getContributors()[0]).toBe(c2)
  })

  // -------------------------------------------------------------------------
  // serialize
  // -------------------------------------------------------------------------

  it('serializes all enabled contributors into a SyncBundle', () => {
    const c1 = createMockContributor('settings', { theme: 'dark' })
    const c2 = createMockContributor('workspaces', { list: ['a'] })

    engine.register(c1)
    engine.register(c2)

    const bundle = engine.serialize('device-a', ['settings', 'workspaces'])

    expect(bundle.version).toBe(1)
    expect(bundle.device).toBe('device-a')
    expect(bundle.timestamp).toBeGreaterThan(0)
    expect(bundle.collections['settings']).toEqual({ version: 1, data: { theme: 'dark' } })
    expect(bundle.collections['workspaces']).toEqual({ version: 1, data: { list: ['a'] } })
  })

  it('skips contributors not in enabledModules during serialize', () => {
    const c1 = createMockContributor('settings', { theme: 'dark' })
    const c2 = createMockContributor('workspaces', { list: ['a'] })

    engine.register(c1)
    engine.register(c2)

    const bundle = engine.serialize('device-a', ['settings'])

    expect(bundle.collections).toHaveProperty('settings')
    expect(bundle.collections).not.toHaveProperty('workspaces')
  })

  it('skips contributors not registered when serializing', () => {
    const c1 = createMockContributor('settings', { theme: 'dark' })
    engine.register(c1)

    // 'workspaces' is in enabledModules but not registered
    const bundle = engine.serialize('device-a', ['settings', 'workspaces'])

    expect(bundle.collections).toHaveProperty('settings')
    expect(bundle.collections).not.toHaveProperty('workspaces')
  })

  // -------------------------------------------------------------------------
  // push
  // -------------------------------------------------------------------------

  it('push serializes and sends bundle to provider', async () => {
    const c1 = createMockContributor('settings', { theme: 'dark' })
    engine.register(c1)

    const provider = createMockProvider()
    const bundle = await engine.push(provider, 'device-a', ['settings'])

    expect(provider.pushedBundles).toHaveLength(1)
    expect(provider.pushedBundles[0]).toBe(bundle)
    expect(bundle.device).toBe('device-a')
    expect(bundle.collections['settings']).toBeDefined()
  })

  // -------------------------------------------------------------------------
  // pull — no data from provider
  // -------------------------------------------------------------------------

  it('pull returns null appliedBundle when provider has no data', async () => {
    const provider = createMockProvider(null)
    const result = await engine.pull(provider, null, ['settings'])

    expect(result.appliedBundle).toBeNull()
    expect(result.conflicts).toEqual([])
  })

  // -------------------------------------------------------------------------
  // pull — first sync (lastSynced === null)
  // -------------------------------------------------------------------------

  it('pull with null lastSynced does full-replace on all enabled contributors', async () => {
    const c1 = createMockContributor('settings', { theme: 'dark' })
    engine.register(c1)

    const remoteBundle: SyncBundle = {
      version: 1,
      timestamp: Date.now(),
      device: 'device-b',
      collections: {
        settings: { version: 1, data: { theme: 'light', fontSize: 14 } },
      },
    }

    const provider = createMockProvider(remoteBundle)
    const result = await engine.pull(provider, null, ['settings'])

    expect(result.appliedBundle).toBe(remoteBundle)
    expect(result.conflicts).toEqual([])

    // Contributor should have received full-replace deserialize call
    expect(c1._deserializeCalls).toHaveLength(1)
    expect(c1._deserializeCalls[0].merge).toEqual({ type: 'full-replace' })
    expect(c1._deserializeCalls[0].payload).toEqual({
      version: 1,
      data: { theme: 'light', fontSize: 14 },
    })
  })

  it('pull with null lastSynced skips contributors not in enabledModules', async () => {
    const c1 = createMockContributor('settings', { theme: 'dark' })
    const c2 = createMockContributor('workspaces', { list: [] })
    engine.register(c1)
    engine.register(c2)

    const remoteBundle: SyncBundle = {
      version: 1,
      timestamp: Date.now(),
      device: 'device-b',
      collections: {
        settings: { version: 1, data: { theme: 'light' } },
        workspaces: { version: 1, data: { list: ['x'] } },
      },
    }

    const provider = createMockProvider(remoteBundle)
    await engine.pull(provider, null, ['settings'])

    expect(c1._deserializeCalls).toHaveLength(1)
    expect(c2._deserializeCalls).toHaveLength(0)
  })

  // -------------------------------------------------------------------------
  // pull — conflict detection (lastSynced provided)
  // -------------------------------------------------------------------------

  it('pull detects conflicts when both sides diverged since lastSynced', async () => {
    // Local: theme='dark' (changed from 'system')
    const c1 = createMockContributor('settings', { theme: 'dark', lang: 'en' })
    engine.register(c1)

    // lastSynced: theme='system', lang='en'
    const lastSynced: SyncBundle = {
      version: 1,
      timestamp: Date.now() - 10000,
      device: 'device-a',
      collections: {
        settings: { version: 1, data: { theme: 'system', lang: 'en' } },
      },
    }

    // Remote: theme='light' (also changed from 'system'), lang='en'
    const remoteBundle: SyncBundle = {
      version: 1,
      timestamp: Date.now(),
      device: 'device-b',
      collections: {
        settings: { version: 1, data: { theme: 'light', lang: 'en' } },
      },
    }

    const provider = createMockProvider(remoteBundle)
    const result = await engine.pull(provider, lastSynced, ['settings'])

    expect(result.appliedBundle).toBe(remoteBundle)
    expect(result.conflicts).toHaveLength(1)
    expect(result.conflicts[0]).toMatchObject({
      contributor: 'settings',
      field: 'theme',
      lastSynced: 'system',
      local: 'dark',
      remote: { value: 'light', device: 'device-b' },
    })
  })

  it('pull auto-applies non-conflicting remote changes when lastSynced is provided', async () => {
    // Local: theme='dark', lang='en' (lang unchanged from lastSynced)
    const c1 = createMockContributor('settings', { theme: 'dark', lang: 'en' })
    engine.register(c1)

    // lastSynced: theme='dark', lang='en'
    const lastSynced: SyncBundle = {
      version: 1,
      timestamp: Date.now() - 10000,
      device: 'device-a',
      collections: {
        settings: { version: 1, data: { theme: 'dark', lang: 'en' } },
      },
    }

    // Remote: theme='dark', lang='fr' (only remote changed lang)
    const remoteBundle: SyncBundle = {
      version: 1,
      timestamp: Date.now(),
      device: 'device-b',
      collections: {
        settings: { version: 1, data: { theme: 'dark', lang: 'fr' } },
      },
    }

    const provider = createMockProvider(remoteBundle)
    const result = await engine.pull(provider, lastSynced, ['settings'])

    // No conflicts; deserialize should be called with merged data
    expect(result.conflicts).toHaveLength(0)
    expect(c1._deserializeCalls).toHaveLength(1)
    expect(c1._deserializeCalls[0].merge).toEqual({ type: 'full-replace' })
    // Merged: theme='dark' (local), lang='fr' (remote)
    const applied = c1._deserializeCalls[0].payload as FullPayload
    expect(applied.data).toEqual({ theme: 'dark', lang: 'fr' })
  })

  it('pull skips contributors not in bundle collections during merge', async () => {
    const c1 = createMockContributor('settings', { theme: 'dark' })
    const c2 = createMockContributor('workspaces', { list: [] })
    engine.register(c1)
    engine.register(c2)

    const lastSynced: SyncBundle = {
      version: 1,
      timestamp: Date.now() - 1000,
      device: 'device-a',
      collections: {
        settings: { version: 1, data: { theme: 'dark' } },
      },
    }

    const remoteBundle: SyncBundle = {
      version: 1,
      timestamp: Date.now(),
      device: 'device-b',
      collections: {
        settings: { version: 1, data: { theme: 'light' } },
        // workspaces NOT in remote bundle
      },
    }

    const provider = createMockProvider(remoteBundle)
    await engine.pull(provider, lastSynced, ['settings', 'workspaces'])

    // workspaces contributor not called since no remote collection for it
    expect(c2._deserializeCalls).toHaveLength(0)
  })

  // -------------------------------------------------------------------------
  // resolveConflicts
  // -------------------------------------------------------------------------

  it('resolveConflicts applies user choices via field-merge deserialize', () => {
    const c1 = createMockContributor('settings', { theme: 'dark', lang: 'en' })
    engine.register(c1)

    const remoteBundle: SyncBundle = {
      version: 1,
      timestamp: Date.now(),
      device: 'device-b',
      collections: {
        settings: { version: 1, data: { theme: 'light', lang: 'fr' } },
      },
    }

    const conflicts = [
      {
        contributor: 'settings',
        field: 'theme',
        lastSynced: 'system',
        local: 'dark',
        remote: { value: 'light', device: 'device-b' },
      },
    ]

    // User chooses to keep remote theme
    engine.resolveConflicts(remoteBundle, conflicts, { theme: 'remote' })

    expect(c1._deserializeCalls).toHaveLength(1)
    const call = c1._deserializeCalls[0]
    expect(call.merge).toEqual({ type: 'field-merge', resolved: { theme: 'remote' } })
  })

  it('resolveConflicts groups conflicts by contributor and calls each once', () => {
    const c1 = createMockContributor('settings', { theme: 'dark', lang: 'en' })
    const c2 = createMockContributor('workspaces', { active: 'ws-1' })
    engine.register(c1)
    engine.register(c2)

    const remoteBundle: SyncBundle = {
      version: 1,
      timestamp: Date.now(),
      device: 'device-b',
      collections: {
        settings: { version: 1, data: { theme: 'light', lang: 'fr' } },
        workspaces: { version: 1, data: { active: 'ws-2' } },
      },
    }

    const conflicts = [
      {
        contributor: 'settings',
        field: 'theme',
        lastSynced: 'system',
        local: 'dark',
        remote: { value: 'light', device: 'device-b' },
      },
      {
        contributor: 'workspaces',
        field: 'active',
        lastSynced: 'ws-0',
        local: 'ws-1',
        remote: { value: 'ws-2', device: 'device-b' },
      },
    ]

    engine.resolveConflicts(remoteBundle, conflicts, { theme: 'local', active: 'remote' })

    // Each contributor called exactly once
    expect(c1._deserializeCalls).toHaveLength(1)
    expect(c2._deserializeCalls).toHaveLength(1)

    expect(c1._deserializeCalls[0].merge).toEqual({
      type: 'field-merge',
      resolved: { theme: 'local', active: 'remote' },
    })
    expect(c2._deserializeCalls[0].merge).toEqual({
      type: 'field-merge',
      resolved: { theme: 'local', active: 'remote' },
    })
  })
})
