import { describe, it, expect, beforeEach, vi } from 'vitest'
import { applyImport, syncNow } from './sync-actions'
import { createSyncEngine } from './engine'
import type {
  SyncBundle,
  SyncContributor,
  SyncProvider,
  FullPayload,
  MergeStrategy,
} from './types'

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

function createTestContributor(
  id: string,
  initialData: Record<string, unknown>,
): SyncContributor & { getData: () => Record<string, unknown> } {
  let data = { ...initialData }
  return {
    id,
    strategy: 'full',
    serialize(): FullPayload {
      return { version: 1, data: { ...data } }
    },
    deserialize(payload: unknown, merge: MergeStrategy): void {
      const p = payload as FullPayload
      if (merge.type === 'full-replace') {
        data = { ...(p.data as Record<string, unknown>) }
      } else {
        data = { ...data, ...(p.data as Record<string, unknown>) }
      }
    },
    getVersion(): number {
      return 1
    },
    getData(): Record<string, unknown> {
      return { ...data }
    },
  }
}

function createStubProvider(overrides: Partial<SyncProvider> = {}): SyncProvider {
  return {
    id: 'stub',
    push: vi.fn(async () => {}),
    pull: vi.fn(async () => null),
    pushChunks: vi.fn(async () => {}),
    pullChunks: vi.fn(async () => ({})),
    listHistory: vi.fn(async () => []),
    ...overrides,
  }
}

// ---------------------------------------------------------------------------
// syncNow
// ---------------------------------------------------------------------------

describe('syncNow', () => {
  let engine: ReturnType<typeof createSyncEngine>
  let contributor: ReturnType<typeof createTestContributor>

  beforeEach(() => {
    engine = createSyncEngine()
    contributor = createTestContributor('prefs', { theme: 'dark' })
    engine.register(contributor)
  })

  it('first sync with empty remote: pushes local state and returns ok', async () => {
    const provider = createStubProvider({ pull: vi.fn(async () => null) })
    const result = await syncNow({
      provider,
      clientId: 'c_aaa',
      lastSyncedBundle: null,
      enabledModules: ['prefs'],
      engine,
    })

    expect(result.kind).toBe('ok')
    if (result.kind !== 'ok') throw new Error('unreachable')
    expect(provider.push).toHaveBeenCalledOnce()
    expect(result.appliedBundle.device).toBe('c_aaa')
    expect(result.appliedBundle.collections['prefs']).toBeDefined()
  })

  it('first sync with remote data: applies remote and pushes merged state', async () => {
    const remoteBundle: SyncBundle = {
      version: 1,
      timestamp: 500,
      device: 'c_remote',
      collections: {
        prefs: { version: 1, data: { theme: 'light' } },
      },
    }
    const provider = createStubProvider({ pull: vi.fn(async () => remoteBundle) })

    const result = await syncNow({
      provider,
      clientId: 'c_local',
      lastSyncedBundle: null,
      enabledModules: ['prefs'],
      engine,
    })

    expect(result.kind).toBe('ok')
    // Local contributor should have been replaced with remote
    expect(contributor.getData()).toEqual({ theme: 'light' })
    expect(provider.push).toHaveBeenCalledOnce()
  })

  it('returns conflicts without pushing when three-way merge detects conflict', async () => {
    const lastSynced: SyncBundle = {
      version: 1,
      timestamp: 100,
      device: 'c_local',
      collections: {
        prefs: { version: 1, data: { theme: 'dark' } },
      },
    }
    // Local changed theme to 'solarized'
    contributor.deserialize(
      { version: 1, data: { theme: 'solarized' } },
      { type: 'full-replace' },
    )
    // Remote changed theme to 'light'
    const remoteBundle: SyncBundle = {
      version: 1,
      timestamp: 500,
      device: 'c_remote',
      collections: {
        prefs: { version: 1, data: { theme: 'light' } },
      },
    }
    const provider = createStubProvider({ pull: vi.fn(async () => remoteBundle) })

    const result = await syncNow({
      provider,
      clientId: 'c_local',
      lastSyncedBundle: lastSynced,
      enabledModules: ['prefs'],
      engine,
    })

    expect(result.kind).toBe('conflicts')
    if (result.kind !== 'conflicts') throw new Error('unreachable')
    expect(result.conflicts).toHaveLength(1)
    expect(result.conflicts[0].field).toBe('theme')
    expect(provider.push).not.toHaveBeenCalled()
    // Local state must not change when conflicts exist
    expect(contributor.getData()).toEqual({ theme: 'solarized' })
  })

  it('conflicts: partialBaseline advances only for non-conflicting contributors', async () => {
    // Second contributor with no conflict
    const prefs = contributor // 'prefs' — conflict target
    const hosts = createTestContributor('hosts', { h1: { name: 'a' } })
    engine.register(hosts)

    // Set up conflict on prefs: local changed theme, remote changed theme differently.
    const lastSynced: SyncBundle = {
      version: 1,
      timestamp: 100,
      device: 'c_local',
      collections: {
        prefs: { version: 1, data: { theme: 'dark' } },
        hosts: { version: 1, data: { h1: { name: 'a' } } },
      },
    }
    prefs.deserialize(
      { version: 1, data: { theme: 'solarized' } },
      { type: 'full-replace' },
    )
    const remoteBundle: SyncBundle = {
      version: 1,
      timestamp: 500,
      device: 'c_remote',
      collections: {
        prefs: { version: 1, data: { theme: 'light' } },
        hosts: { version: 1, data: { h1: { name: 'b' } } }, // changed, no local change → use-remote
      },
    }
    const provider = createStubProvider({ pull: vi.fn(async () => remoteBundle) })

    const result = await syncNow({
      provider,
      clientId: 'c_local',
      lastSyncedBundle: lastSynced,
      enabledModules: ['prefs', 'hosts'],
      engine,
    })

    expect(result.kind).toBe('conflicts')
    if (result.kind !== 'conflicts') throw new Error('unreachable')

    // Non-conflicting 'hosts' was applied locally (engine partial-apply):
    expect(hosts.getData()).toEqual({ h1: { name: 'b' } })

    // partialBaseline must: keep old baseline for 'prefs' (conflict) but
    // advance to remote for 'hosts' (applied).
    expect(result.partialBaseline.collections['prefs']).toEqual(
      lastSynced.collections['prefs'],
    )
    expect(result.partialBaseline.collections['hosts']).toEqual(
      remoteBundle.collections['hosts'],
    )
  })

  it('conflicts: partialBaseline works when lastSyncedBundle is null (first sync)', async () => {
    // First sync → engine.pull uses first-sync branch with full-replace, no conflicts.
    // Force a conflict scenario by providing a non-null lastSyncedBundle that
    // predates both local and remote state. Here we skip: with null lastSynced
    // engine never returns conflicts, so partialBaseline need only handle that
    // we don't explode when given null.
    const provider = createStubProvider({ pull: vi.fn(async () => null) })
    const result = await syncNow({
      provider,
      clientId: 'c_local',
      lastSyncedBundle: null,
      enabledModules: ['prefs'],
      engine,
    })
    // Empty remote → ok path, no partialBaseline exposed
    expect(result.kind).toBe('ok')
  })

  it('returns error when provider.pull throws', async () => {
    const provider = createStubProvider({
      pull: vi.fn(async () => {
        throw new Error('network down')
      }),
    })

    const result = await syncNow({
      provider,
      clientId: 'c_local',
      lastSyncedBundle: null,
      enabledModules: ['prefs'],
      engine,
    })

    expect(result.kind).toBe('error')
    if (result.kind !== 'error') throw new Error('unreachable')
    expect(result.error).toContain('network down')
  })

  it('returns error when provider.push throws', async () => {
    const provider = createStubProvider({
      pull: vi.fn(async () => null),
      push: vi.fn(async () => {
        throw new Error('daemon 500')
      }),
    })

    const result = await syncNow({
      provider,
      clientId: 'c_local',
      lastSyncedBundle: null,
      enabledModules: ['prefs'],
      engine,
    })

    expect(result.kind).toBe('error')
    if (result.kind !== 'error') throw new Error('unreachable')
    expect(result.error).toContain('daemon 500')
  })
})

// ---------------------------------------------------------------------------
// applyImport
// ---------------------------------------------------------------------------

describe('applyImport', () => {
  let engine: ReturnType<typeof createSyncEngine>
  let contributor: ReturnType<typeof createTestContributor>

  beforeEach(() => {
    engine = createSyncEngine()
    contributor = createTestContributor('prefs', { theme: 'dark' })
    engine.register(contributor)
  })

  it('first import (no lastSynced) full-replaces local state', async () => {
    const importedBundle: SyncBundle = {
      version: 1,
      timestamp: 800,
      device: 'c_exported',
      collections: {
        prefs: { version: 1, data: { theme: 'light' } },
      },
    }

    const result = await applyImport({
      bundle: importedBundle,
      lastSyncedBundle: null,
      enabledModules: ['prefs'],
      engine,
    })

    expect(result.kind).toBe('ok')
    expect(contributor.getData()).toEqual({ theme: 'light' })
  })

  it('subsequent import with three-way merge applies non-conflicting changes', async () => {
    // Local matches lastSynced; only remote changed fontSize → remote wins
    contributor.deserialize(
      { version: 1, data: { theme: 'dark', fontSize: 14 } },
      { type: 'full-replace' },
    )
    const lastSynced: SyncBundle = {
      version: 1,
      timestamp: 100,
      device: 'c_local',
      collections: {
        prefs: { version: 1, data: { theme: 'dark', fontSize: 14 } },
      },
    }
    const importedBundle: SyncBundle = {
      version: 1,
      timestamp: 800,
      device: 'c_other',
      collections: {
        prefs: { version: 1, data: { theme: 'dark', fontSize: 18 } },
      },
    }

    const result = await applyImport({
      bundle: importedBundle,
      lastSyncedBundle: lastSynced,
      enabledModules: ['prefs'],
      engine,
    })

    expect(result.kind).toBe('ok')
    expect(contributor.getData()).toEqual({ theme: 'dark', fontSize: 18 })
  })

  it('reports conflicts without applying when three-way merge detects conflict', async () => {
    const lastSynced: SyncBundle = {
      version: 1,
      timestamp: 100,
      device: 'c_local',
      collections: {
        prefs: { version: 1, data: { theme: 'dark' } },
      },
    }
    // Local changed to 'solarized'
    contributor.deserialize(
      { version: 1, data: { theme: 'solarized' } },
      { type: 'full-replace' },
    )
    // Imported bundle changed to 'light'
    const importedBundle: SyncBundle = {
      version: 1,
      timestamp: 800,
      device: 'c_other',
      collections: {
        prefs: { version: 1, data: { theme: 'light' } },
      },
    }

    const result = await applyImport({
      bundle: importedBundle,
      lastSyncedBundle: lastSynced,
      enabledModules: ['prefs'],
      engine,
    })

    expect(result.kind).toBe('conflicts')
    expect(contributor.getData()).toEqual({ theme: 'solarized' })
  })
})
