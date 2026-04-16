# Sync Architecture Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Enable cross-device data sync for Purdex via three channels (Manual export/import, Daemon sync, File sync) with pluggable module contributors and three-way merge conflict detection.

**Architecture:** SyncEngine in SPA coordinates serialization/deserialization across registered SyncContributors. Each store module opts-in by implementing a contributor. Three SyncProviders (Manual, Daemon, File) implement a unified push/pull interface. Daemon side adds a Go sync module with SQLite storage, canonical bundle merging, and pairing API.

**Tech Stack:** React 19 / Zustand 5 / TypeScript (SPA), Go / net/http / modernc.org/sqlite (Daemon), Vitest (tests)

**Spec:** `docs/superpowers/specs/2026-04-16-sync-architecture-design.md`

---

## File Structure

### SPA — New Files

```
spa/src/lib/sync/
├── types.ts                    # SyncBundle, SyncContributor, SyncProvider, SyncState, etc.
├── engine.ts                   # SyncEngine — registry, serialize, deserialize, three-way merge
├── three-way-merge.ts          # detectConflict(), mergeCollections() pure functions
├── providers/
│   ├── manual-provider.ts      # Export/import .purdex-sync files
│   ├── daemon-provider.ts      # REST push/pull via hostFetch
│   └── file-provider.ts        # Electron fs.watch based sync folder
├── contributors/
│   ├── workspaces.ts           # SyncContributor for useWorkspaceStore
│   ├── hosts.ts                # SyncContributor for useHostStore (strips tokens)
│   ├── preferences.ts          # SyncContributor for useUISettingsStore + useThemeStore
│   ├── layout.ts               # SyncContributor for useLayoutStore
│   ├── quick-commands.ts       # SyncContributor for useQuickCommandStore
│   ├── i18n.ts                 # SyncContributor for useI18nStore
│   └── notification-settings.ts
├── use-sync-store.ts           # Zustand store for SyncState (persisted)
└── register-sync.ts            # Wire up engine + contributors + settings section

spa/src/components/settings/
├── SyncSection.tsx             # Main sync settings UI
├── SyncConflictBanner.tsx      # Conflict detection banner + resolution UI
├── SyncHistoryDialog.tsx       # Snapshot history browser
└── SyncAddDeviceDialog.tsx     # QR code + pairing code dialog
```

### SPA — Modified Files

```
spa/src/lib/storage/keys.ts             # Add SYNC_STATE, SYNC_CLIENT_ID keys
spa/src/lib/register-modules.tsx        # Wire registerSync() + SyncSection component
spa/src/lib/host-api.ts                 # Add sync API helpers (pushBundle, pullBundle, etc.)
```

### Go Daemon — New Files

```
internal/module/sync/
├── module.go                   # Module interface impl, RegisterRoutes
├── handler.go                  # HTTP handlers (push, pull, history, gc, pair)
├── store.go                    # SyncStore — SQLite tables + CRUD
└── store_test.go               # Store unit tests

internal/module/sync/
└── canonical.go                # Canonical bundle merge logic (field-level LWW)
```

### Go Daemon — Modified Files

```
cmd/pdx/main.go                # Register sync module
```

---

## Task 1: SPA — Core Types

**Files:**
- Create: `spa/src/lib/sync/types.ts`
- Test: `spa/src/lib/sync/types.test.ts`

- [ ] **Step 1: Write type validation tests**

```typescript
// spa/src/lib/sync/types.test.ts
import { describe, it, expect } from 'vitest'
import type {
  SyncBundle,
  SyncContributor,
  SyncProvider,
  SyncState,
  SyncSnapshot,
  ConflictItem,
  MergeStrategy,
  FullPayload,
  ChunkedPayload,
  ResolvedFields,
} from './types'

describe('sync types', () => {
  it('SyncBundle has required fields', () => {
    const bundle: SyncBundle = {
      version: 1,
      timestamp: Date.now(),
      device: 'test-device',
      collections: {},
    }
    expect(bundle.version).toBe(1)
    expect(bundle.collections).toEqual({})
  })

  it('FullPayload has version and data', () => {
    const payload: FullPayload = { version: 1, data: { theme: 'dark' } }
    expect(payload.version).toBe(1)
  })

  it('ChunkedPayload has manifest and chunks', () => {
    const payload: ChunkedPayload = {
      version: 1,
      manifest: [{ id: 'doc1', hash: 'abc123' }],
      chunks: { abc123: new Uint8Array([1, 2, 3]) },
    }
    expect(payload.manifest).toHaveLength(1)
  })

  it('SyncState defaults', () => {
    const state: SyncState = {
      lastSyncedBundle: null,
      lastSyncedAt: null,
      activeProviderId: null,
      enabledModules: [],
    }
    expect(state.lastSyncedBundle).toBeNull()
  })

  it('ConflictItem captures three-way values', () => {
    const conflict: ConflictItem = {
      contributor: 'preferences',
      field: 'theme',
      lastSynced: 'light',
      local: 'dark',
      remote: { value: 'solarized', device: 'iPad' },
    }
    expect(conflict.contributor).toBe('preferences')
  })

  it('SyncSnapshot has source and bundleRef', () => {
    const snapshot: SyncSnapshot = {
      id: 'snap1',
      timestamp: Date.now(),
      device: 'MacBook',
      source: 'local',
      trigger: 'auto',
      bundleRef: 'local:snap1',
    }
    expect(snapshot.source).toBe('local')
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd spa && npx vitest run src/lib/sync/types.test.ts`
Expected: FAIL — module not found

- [ ] **Step 3: Implement types**

```typescript
// spa/src/lib/sync/types.ts

// --- Payloads ---

export interface FullPayload {
  version: number
  data: Record<string, unknown>
}

export interface ChunkedManifestEntry {
  id: string
  hash: string
}

export interface ChunkedPayload {
  version: number
  manifest: ChunkedManifestEntry[]
  chunks: Record<string, Uint8Array>
}

// --- SyncBundle ---

export interface SyncBundle {
  version: number
  timestamp: number
  device: string
  collections: Record<string, FullPayload | ChunkedPayload>
}

// --- SyncContributor ---

export interface SyncContributor {
  id: string
  strategy: 'full' | 'content-addressed'
  serialize(): FullPayload | ChunkedPayload
  deserialize(payload: unknown, merge: MergeStrategy): void
  getVersion(): number
  migrate?(payload: unknown, fromVersion: number): unknown
}

// --- Merge ---

export type MergeStrategy =
  | { type: 'full-replace' }
  | { type: 'field-merge'; resolved: ResolvedFields }

export interface ResolvedFields {
  [field: string]: 'local' | 'remote'
}

export interface ConflictItem {
  contributor: string
  field: string
  lastSynced: unknown
  local: unknown
  remote: { value: unknown; device: string }
}

// --- SyncProvider ---

export interface SyncProvider {
  id: string
  push(bundle: SyncBundle): Promise<void>
  pull(): Promise<SyncBundle | null>
  pushChunks(chunks: Record<string, Uint8Array>): Promise<void>
  pullChunks(hashes: string[]): Promise<Record<string, Uint8Array>>
  listHistory(limit: number): Promise<SyncSnapshot[]>
}

// --- SyncState ---

export interface SyncState {
  lastSyncedBundle: SyncBundle | null
  lastSyncedAt: number | null
  activeProviderId: string | null
  enabledModules: string[]
}

// --- History ---

export interface SyncSnapshot {
  id: string
  timestamp: number
  device: string
  source: 'local' | 'remote'
  trigger: 'auto' | 'manual'
  bundleRef: string
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd spa && npx vitest run src/lib/sync/types.test.ts`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add spa/src/lib/sync/types.ts spa/src/lib/sync/types.test.ts
git commit -m "feat(sync): add core sync type definitions"
```

---

## Task 2: SPA — Three-Way Merge

**Files:**
- Create: `spa/src/lib/sync/three-way-merge.ts`
- Test: `spa/src/lib/sync/three-way-merge.test.ts`

- [ ] **Step 1: Write tests for detectConflict**

```typescript
// spa/src/lib/sync/three-way-merge.test.ts
import { describe, it, expect } from 'vitest'
import { detectConflict, mergeCollection } from './three-way-merge'

describe('detectConflict', () => {
  it('returns no-change when all three are equal', () => {
    expect(detectConflict('dark', 'dark', 'dark')).toBe('no-change')
  })

  it('returns use-local when only local changed', () => {
    expect(detectConflict('light', 'dark', 'light')).toBe('use-local')
  })

  it('returns use-remote when only remote changed', () => {
    expect(detectConflict('light', 'light', 'dark')).toBe('use-remote')
  })

  it('returns both-same when both changed to same value', () => {
    expect(detectConflict('light', 'dark', 'dark')).toBe('both-same')
  })

  it('returns conflict when both changed to different values', () => {
    expect(detectConflict('light', 'dark', 'solarized')).toBe('conflict')
  })

  it('handles nested objects with deep equality', () => {
    const last = { a: 1, b: 2 }
    const local = { a: 1, b: 2 }
    const remote = { a: 1, b: 3 }
    expect(detectConflict(last, local, remote)).toBe('use-remote')
  })

  it('handles null/undefined values', () => {
    expect(detectConflict(null, 'new', null)).toBe('use-local')
    expect(detectConflict(undefined, undefined, 'new')).toBe('use-remote')
  })
})

describe('mergeCollection', () => {
  it('auto-merges non-conflicting fields', () => {
    const last = { theme: 'light', locale: 'en' }
    const local = { theme: 'dark', locale: 'en' }
    const remote = { theme: 'light', locale: 'zh-TW' }

    const result = mergeCollection(last, local, remote, 'iPad')
    expect(result.merged).toEqual({ theme: 'dark', locale: 'zh-TW' })
    expect(result.conflicts).toHaveLength(0)
  })

  it('collects conflicts for double-changed fields', () => {
    const last = { theme: 'light' }
    const local = { theme: 'dark' }
    const remote = { theme: 'solarized' }

    const result = mergeCollection(last, local, remote, 'iPad')
    expect(result.conflicts).toHaveLength(1)
    expect(result.conflicts[0]).toEqual({
      contributor: '',
      field: 'theme',
      lastSynced: 'light',
      local: 'dark',
      remote: { value: 'solarized', device: 'iPad' },
    })
    // merged should use local as placeholder until resolved
    expect(result.merged.theme).toBe('dark')
  })

  it('handles new keys added on one side', () => {
    const last = { a: 1 }
    const local = { a: 1, b: 2 }
    const remote = { a: 1 }

    const result = mergeCollection(last, local, remote, 'iPad')
    expect(result.merged).toEqual({ a: 1, b: 2 })
    expect(result.conflicts).toHaveLength(0)
  })

  it('handles keys deleted on one side', () => {
    const last = { a: 1, b: 2 }
    const local = { a: 1 }
    const remote = { a: 1, b: 2 }

    const result = mergeCollection(last, local, remote, 'iPad')
    expect(result.merged).toEqual({ a: 1 })
    expect(result.conflicts).toHaveLength(0)
  })

  it('returns empty conflicts for first sync (null last)', () => {
    const result = mergeCollection(null, { a: 1 }, { a: 2 }, 'iPad')
    // null last = full-replace, no conflict detection
    expect(result.conflicts).toHaveLength(0)
    expect(result.merged).toEqual({ a: 2 })
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd spa && npx vitest run src/lib/sync/three-way-merge.test.ts`
Expected: FAIL — module not found

- [ ] **Step 3: Implement three-way merge**

```typescript
// spa/src/lib/sync/three-way-merge.ts
import type { ConflictItem } from './types'

/**
 * Deep equality check for JSON-serializable values.
 */
function deepEqual(a: unknown, b: unknown): boolean {
  if (a === b) return true
  if (a == null || b == null) return a === b
  if (typeof a !== typeof b) return false
  if (typeof a !== 'object') return false

  const aObj = a as Record<string, unknown>
  const bObj = b as Record<string, unknown>
  const aKeys = Object.keys(aObj)
  const bKeys = Object.keys(bObj)

  if (aKeys.length !== bKeys.length) return false
  return aKeys.every((key) => deepEqual(aObj[key], bObj[key]))
}

export type ConflictResult =
  | 'no-change'
  | 'use-local'
  | 'use-remote'
  | 'both-same'
  | 'conflict'

/**
 * Three-way conflict detection for a single field.
 */
export function detectConflict(
  last: unknown,
  local: unknown,
  remote: unknown,
): ConflictResult {
  const localChanged = !deepEqual(last, local)
  const remoteChanged = !deepEqual(last, remote)

  if (!localChanged && !remoteChanged) return 'no-change'
  if (localChanged && !remoteChanged) return 'use-local'
  if (!localChanged && remoteChanged) return 'use-remote'
  if (deepEqual(local, remote)) return 'both-same'
  return 'conflict'
}

export interface MergeCollectionResult {
  merged: Record<string, unknown>
  conflicts: ConflictItem[]
}

/**
 * Merge two flat data objects using three-way comparison against a common ancestor.
 * Returns merged result + list of unresolved conflicts.
 *
 * When `last` is null (first sync), treats remote as full-replace — no conflicts.
 */
export function mergeCollection(
  last: Record<string, unknown> | null,
  local: Record<string, unknown>,
  remote: Record<string, unknown>,
  remoteDevice: string,
): MergeCollectionResult {
  // First sync: full-replace from remote
  if (last == null) {
    return { merged: { ...remote }, conflicts: [] }
  }

  const allKeys = new Set([
    ...Object.keys(last),
    ...Object.keys(local),
    ...Object.keys(remote),
  ])
  const merged: Record<string, unknown> = {}
  const conflicts: ConflictItem[] = []

  for (const key of allKeys) {
    const lastVal = last[key]
    const localVal = local[key]
    const remoteVal = remote[key]

    const result = detectConflict(lastVal, localVal, remoteVal)

    switch (result) {
      case 'no-change':
      case 'both-same':
        // Use local (identical to remote in both-same case)
        if (key in local) merged[key] = localVal
        break
      case 'use-local':
        if (key in local) merged[key] = localVal
        // key deleted locally: don't add to merged
        break
      case 'use-remote':
        if (key in remote) merged[key] = remoteVal
        break
      case 'conflict':
        // Use local as placeholder; user will resolve
        if (key in local) merged[key] = localVal
        conflicts.push({
          contributor: '', // filled by caller
          field: key,
          lastSynced: lastVal,
          local: localVal,
          remote: { value: remoteVal, device: remoteDevice },
        })
        break
    }
  }

  return { merged, conflicts }
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd spa && npx vitest run src/lib/sync/three-way-merge.test.ts`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add spa/src/lib/sync/three-way-merge.ts spa/src/lib/sync/three-way-merge.test.ts
git commit -m "feat(sync): implement three-way merge with conflict detection"
```

---

## Task 3: SPA — SyncEngine Core

**Files:**
- Create: `spa/src/lib/sync/engine.ts`
- Test: `spa/src/lib/sync/engine.test.ts`

- [ ] **Step 1: Write tests for SyncEngine**

```typescript
// spa/src/lib/sync/engine.test.ts
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { createSyncEngine } from './engine'
import type { SyncContributor, SyncProvider, SyncBundle } from './types'

function createMockContributor(
  id: string,
  data: Record<string, unknown>,
): SyncContributor {
  let currentData = { ...data }
  return {
    id,
    strategy: 'full' as const,
    serialize: () => ({ version: 1, data: { ...currentData } }),
    deserialize: (payload: unknown, _merge) => {
      const p = payload as { data: Record<string, unknown> }
      currentData = { ...p.data }
    },
    getVersion: () => 1,
  }
}

function createMockProvider(): SyncProvider & {
  pushed: SyncBundle[]
  pullBundle: SyncBundle | null
} {
  const provider = {
    id: 'mock',
    pushed: [] as SyncBundle[],
    pullBundle: null as SyncBundle | null,
    push: vi.fn(async (bundle: SyncBundle) => {
      provider.pushed.push(bundle)
    }),
    pull: vi.fn(async () => provider.pullBundle),
    pushChunks: vi.fn(async () => {}),
    pullChunks: vi.fn(async () => ({})),
    listHistory: vi.fn(async () => []),
  }
  return provider
}

describe('SyncEngine', () => {
  let engine: ReturnType<typeof createSyncEngine>

  beforeEach(() => {
    engine = createSyncEngine()
  })

  it('registers contributors', () => {
    const contrib = createMockContributor('prefs', { theme: 'dark' })
    engine.register(contrib)
    expect(engine.getContributors()).toHaveLength(1)
  })

  it('serializes all enabled contributors into a SyncBundle', () => {
    engine.register(createMockContributor('prefs', { theme: 'dark' }))
    engine.register(createMockContributor('layout', { sidebar: 200 }))

    const bundle = engine.serialize('test-device', ['prefs', 'layout'])
    expect(bundle.version).toBe(1)
    expect(bundle.device).toBe('test-device')
    expect(bundle.collections.prefs).toEqual({ version: 1, data: { theme: 'dark' } })
    expect(bundle.collections.layout).toEqual({ version: 1, data: { sidebar: 200 } })
  })

  it('skips disabled contributors during serialize', () => {
    engine.register(createMockContributor('prefs', { theme: 'dark' }))
    engine.register(createMockContributor('layout', { sidebar: 200 }))

    const bundle = engine.serialize('test-device', ['prefs'])
    expect(Object.keys(bundle.collections)).toEqual(['prefs'])
  })

  it('push sends bundle to provider', async () => {
    const provider = createMockProvider()
    engine.register(createMockContributor('prefs', { theme: 'dark' }))

    await engine.push(provider, 'test-device', ['prefs'])
    expect(provider.push).toHaveBeenCalledTimes(1)
    expect(provider.pushed[0].collections.prefs).toBeDefined()
  })

  it('pull with null lastSynced does full-replace', async () => {
    const contrib = createMockContributor('prefs', { theme: 'dark' })
    engine.register(contrib)

    const remoteBundle: SyncBundle = {
      version: 1,
      timestamp: Date.now(),
      device: 'remote-device',
      collections: {
        prefs: { version: 1, data: { theme: 'solarized' } },
      },
    }

    const provider = createMockProvider()
    provider.pullBundle = remoteBundle

    const result = await engine.pull(provider, null, ['prefs'])
    expect(result.conflicts).toHaveLength(0)
    expect(result.appliedBundle).toBe(remoteBundle)
  })

  it('pull detects conflicts with lastSynced', async () => {
    const contrib = createMockContributor('prefs', { theme: 'dark' })
    engine.register(contrib)

    const lastSynced: SyncBundle = {
      version: 1,
      timestamp: Date.now() - 1000,
      device: 'any',
      collections: {
        prefs: { version: 1, data: { theme: 'light' } },
      },
    }

    const remoteBundle: SyncBundle = {
      version: 1,
      timestamp: Date.now(),
      device: 'iPad',
      collections: {
        prefs: { version: 1, data: { theme: 'solarized' } },
      },
    }

    const provider = createMockProvider()
    provider.pullBundle = remoteBundle

    const result = await engine.pull(provider, lastSynced, ['prefs'])
    expect(result.conflicts).toHaveLength(1)
    expect(result.conflicts[0].field).toBe('theme')
    expect(result.conflicts[0].contributor).toBe('prefs')
  })

  it('pull returns null when provider has no data', async () => {
    const provider = createMockProvider()
    provider.pullBundle = null

    const result = await engine.pull(provider, null, ['prefs'])
    expect(result.appliedBundle).toBeNull()
    expect(result.conflicts).toHaveLength(0)
  })

  it('resolveConflicts applies user choices', () => {
    const contrib = createMockContributor('prefs', { theme: 'dark' })
    engine.register(contrib)

    const remoteBundle: SyncBundle = {
      version: 1,
      timestamp: Date.now(),
      device: 'iPad',
      collections: {
        prefs: { version: 1, data: { theme: 'solarized' } },
      },
    }

    engine.resolveConflicts(
      remoteBundle,
      [
        {
          contributor: 'prefs',
          field: 'theme',
          lastSynced: 'light',
          local: 'dark',
          remote: { value: 'solarized', device: 'iPad' },
        },
      ],
      { theme: 'remote' },
    )

    // After resolving with 'remote', contributor should have remote value
    expect(contrib.serialize()).toEqual({
      version: 1,
      data: { theme: 'solarized' },
    })
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd spa && npx vitest run src/lib/sync/engine.test.ts`
Expected: FAIL — module not found

- [ ] **Step 3: Implement SyncEngine**

```typescript
// spa/src/lib/sync/engine.ts
import type {
  SyncBundle,
  SyncContributor,
  SyncProvider,
  ConflictItem,
  FullPayload,
  ResolvedFields,
} from './types'
import { mergeCollection } from './three-way-merge'

export interface PullResult {
  appliedBundle: SyncBundle | null
  conflicts: ConflictItem[]
}

export function createSyncEngine() {
  const contributors = new Map<string, SyncContributor>()

  function register(contributor: SyncContributor): void {
    contributors.set(contributor.id, contributor)
  }

  function getContributors(): SyncContributor[] {
    return [...contributors.values()]
  }

  function serialize(device: string, enabledModules: string[]): SyncBundle {
    const collections: SyncBundle['collections'] = {}

    for (const id of enabledModules) {
      const contrib = contributors.get(id)
      if (!contrib) continue
      collections[id] = contrib.serialize()
    }

    return {
      version: 1,
      timestamp: Date.now(),
      device,
      collections,
    }
  }

  async function push(
    provider: SyncProvider,
    device: string,
    enabledModules: string[],
  ): Promise<SyncBundle> {
    const bundle = serialize(device, enabledModules)
    await provider.push(bundle)
    return bundle
  }

  async function pull(
    provider: SyncProvider,
    lastSynced: SyncBundle | null,
    enabledModules: string[],
  ): Promise<PullResult> {
    const remoteBundle = await provider.pull()
    if (!remoteBundle) {
      return { appliedBundle: null, conflicts: [] }
    }

    // First sync: full-replace
    if (!lastSynced) {
      for (const id of enabledModules) {
        const contrib = contributors.get(id)
        const remotePayload = remoteBundle.collections[id]
        if (contrib && remotePayload) {
          contrib.deserialize(remotePayload, { type: 'full-replace' })
        }
      }
      return { appliedBundle: remoteBundle, conflicts: [] }
    }

    // Three-way merge
    const allConflicts: ConflictItem[] = []

    for (const id of enabledModules) {
      const contrib = contributors.get(id)
      if (!contrib) continue
      if (contrib.strategy !== 'full') continue // content-addressed handled separately

      const localPayload = contrib.serialize() as FullPayload
      const lastPayload = lastSynced.collections[id] as FullPayload | undefined
      const remotePayload = remoteBundle.collections[id] as FullPayload | undefined

      if (!remotePayload) continue

      const lastData = lastPayload?.data ?? null
      const result = mergeCollection(
        lastData,
        localPayload.data,
        remotePayload.data,
        remoteBundle.device,
      )

      // Tag conflicts with contributor ID
      for (const conflict of result.conflicts) {
        conflict.contributor = id
      }
      allConflicts.push(...result.conflicts)

      // Apply non-conflicting merges immediately
      if (result.conflicts.length === 0) {
        contrib.deserialize(
          { version: remotePayload.version, data: result.merged },
          { type: 'field-merge', resolved: {} },
        )
      }
    }

    return { appliedBundle: remoteBundle, conflicts: allConflicts }
  }

  function resolveConflicts(
    remoteBundle: SyncBundle,
    conflicts: ConflictItem[],
    resolved: ResolvedFields,
  ): void {
    // Group conflicts by contributor
    const grouped = new Map<string, ConflictItem[]>()
    for (const conflict of conflicts) {
      const list = grouped.get(conflict.contributor) ?? []
      list.push(conflict)
      grouped.set(conflict.contributor, list)
    }

    for (const [contribId, contribConflicts] of grouped) {
      const contrib = contributors.get(contribId)
      if (!contrib) continue

      const localPayload = contrib.serialize() as FullPayload
      const remotePayload = remoteBundle.collections[contribId] as FullPayload
      const merged = { ...localPayload.data }

      for (const conflict of contribConflicts) {
        const choice = resolved[conflict.field]
        if (choice === 'remote') {
          merged[conflict.field] = conflict.remote.value
        }
        // 'local' = keep current, already in merged
      }

      const resolvedFields: ResolvedFields = {}
      for (const conflict of contribConflicts) {
        resolvedFields[conflict.field] = resolved[conflict.field] ?? 'local'
      }

      contrib.deserialize(
        { version: remotePayload.version, data: merged },
        { type: 'field-merge', resolved: resolvedFields },
      )
    }
  }

  return {
    register,
    getContributors,
    serialize,
    push,
    pull,
    resolveConflicts,
  }
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd spa && npx vitest run src/lib/sync/engine.test.ts`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add spa/src/lib/sync/engine.ts spa/src/lib/sync/engine.test.ts
git commit -m "feat(sync): implement SyncEngine with three-way merge pull/push"
```

---

## Task 4: SPA — SyncState Store

**Files:**
- Create: `spa/src/lib/sync/use-sync-store.ts`
- Modify: `spa/src/lib/storage/keys.ts`
- Test: `spa/src/lib/sync/use-sync-store.test.ts`

- [ ] **Step 1: Add storage keys**

Add to `spa/src/lib/storage/keys.ts`:

```typescript
SYNC_STATE: 'purdex-sync-state',
SYNC_CLIENT_ID: 'purdex-client-id',
```

- [ ] **Step 2: Write store tests**

```typescript
// spa/src/lib/sync/use-sync-store.test.ts
import { describe, it, expect, beforeEach } from 'vitest'
import { useSyncStore } from './use-sync-store'

describe('useSyncStore', () => {
  beforeEach(() => {
    useSyncStore.getState().reset()
  })

  it('starts with null state', () => {
    const state = useSyncStore.getState()
    expect(state.lastSyncedBundle).toBeNull()
    expect(state.lastSyncedAt).toBeNull()
    expect(state.activeProviderId).toBeNull()
    expect(state.enabledModules).toEqual([])
  })

  it('setActiveProvider resets lastSyncedBundle', () => {
    const { setLastSyncedBundle, setActiveProvider } = useSyncStore.getState()
    setLastSyncedBundle({
      version: 1,
      timestamp: 1,
      device: 'test',
      collections: {},
    })
    expect(useSyncStore.getState().lastSyncedBundle).not.toBeNull()

    setActiveProvider('daemon')
    expect(useSyncStore.getState().activeProviderId).toBe('daemon')
    expect(useSyncStore.getState().lastSyncedBundle).toBeNull()
    expect(useSyncStore.getState().lastSyncedAt).toBeNull()
  })

  it('toggleModule adds/removes module IDs', () => {
    const { toggleModule } = useSyncStore.getState()
    toggleModule('workspaces')
    expect(useSyncStore.getState().enabledModules).toContain('workspaces')

    toggleModule('workspaces')
    expect(useSyncStore.getState().enabledModules).not.toContain('workspaces')
  })

  it('setLastSyncedBundle updates bundle and timestamp', () => {
    const bundle = {
      version: 1,
      timestamp: 12345,
      device: 'test',
      collections: {},
    }
    useSyncStore.getState().setLastSyncedBundle(bundle)

    const state = useSyncStore.getState()
    expect(state.lastSyncedBundle).toEqual(bundle)
    expect(state.lastSyncedAt).toBeGreaterThan(0)
  })

  it('getClientId returns stable ID', () => {
    const id1 = useSyncStore.getState().getClientId()
    const id2 = useSyncStore.getState().getClientId()
    expect(id1).toBe(id2)
    expect(id1).toMatch(/^c_[a-z0-9]+$/)
  })
})
```

- [ ] **Step 3: Run test to verify it fails**

Run: `cd spa && npx vitest run src/lib/sync/use-sync-store.test.ts`
Expected: FAIL

- [ ] **Step 4: Implement SyncState store**

```typescript
// spa/src/lib/sync/use-sync-store.ts
import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import { purdexStorage, STORAGE_KEYS, syncManager } from '../storage'
import type { SyncBundle } from './types'

function generateClientId(): string {
  const bytes = crypto.getRandomValues(new Uint8Array(6))
  const hex = Array.from(bytes, (b) => b.toString(16).padStart(2, '0')).join('')
  return `c_${hex}`
}

interface SyncStoreState {
  lastSyncedBundle: SyncBundle | null
  lastSyncedAt: number | null
  activeProviderId: string | null
  enabledModules: string[]
  clientId: string | null

  // Actions
  setLastSyncedBundle: (bundle: SyncBundle) => void
  setActiveProvider: (providerId: string | null) => void
  toggleModule: (moduleId: string) => void
  getClientId: () => string
  reset: () => void
}

const initialState = {
  lastSyncedBundle: null as SyncBundle | null,
  lastSyncedAt: null as number | null,
  activeProviderId: null as string | null,
  enabledModules: [] as string[],
  clientId: null as string | null,
}

export const useSyncStore = create<SyncStoreState>()(
  persist(
    (set, get) => ({
      ...initialState,

      setLastSyncedBundle(bundle: SyncBundle) {
        set({ lastSyncedBundle: bundle, lastSyncedAt: Date.now() })
      },

      setActiveProvider(providerId: string | null) {
        set({
          activeProviderId: providerId,
          lastSyncedBundle: null,
          lastSyncedAt: null,
        })
      },

      toggleModule(moduleId: string) {
        const current = get().enabledModules
        const next = current.includes(moduleId)
          ? current.filter((id) => id !== moduleId)
          : [...current, moduleId]
        set({ enabledModules: next })
      },

      getClientId(): string {
        let id = get().clientId
        if (!id) {
          id = generateClientId()
          set({ clientId: id })
        }
        return id
      },

      reset() {
        set({ ...initialState })
      },
    }),
    {
      name: STORAGE_KEYS.SYNC_STATE,
      storage: purdexStorage,
      partialize: (state) => ({
        lastSyncedBundle: state.lastSyncedBundle,
        lastSyncedAt: state.lastSyncedAt,
        activeProviderId: state.activeProviderId,
        enabledModules: state.enabledModules,
        clientId: state.clientId,
      }),
    },
  ),
)

syncManager.register(STORAGE_KEYS.SYNC_STATE, useSyncStore)
```

- [ ] **Step 5: Run test to verify it passes**

Run: `cd spa && npx vitest run src/lib/sync/use-sync-store.test.ts`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add spa/src/lib/storage/keys.ts spa/src/lib/sync/use-sync-store.ts spa/src/lib/sync/use-sync-store.test.ts
git commit -m "feat(sync): add SyncState Zustand store with client ID generation"
```

---

## Task 5: SPA — Manual Provider (Export/Import)

**Files:**
- Create: `spa/src/lib/sync/providers/manual-provider.ts`
- Test: `spa/src/lib/sync/providers/manual-provider.test.ts`

- [ ] **Step 1: Write tests**

```typescript
// spa/src/lib/sync/providers/manual-provider.test.ts
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { createManualProvider } from './manual-provider'
import type { SyncBundle } from '../types'

describe('ManualProvider', () => {
  let provider: ReturnType<typeof createManualProvider>

  beforeEach(() => {
    provider = createManualProvider()
  })

  it('has id "manual"', () => {
    expect(provider.id).toBe('manual')
  })

  it('exportToBlob serializes bundle to JSON blob', () => {
    const bundle: SyncBundle = {
      version: 1,
      timestamp: Date.now(),
      device: 'test',
      collections: { prefs: { version: 1, data: { theme: 'dark' } } },
    }

    const blob = provider.exportToBlob(bundle)
    expect(blob.type).toBe('application/json')
  })

  it('importFromText parses JSON back to SyncBundle', () => {
    const bundle: SyncBundle = {
      version: 1,
      timestamp: 12345,
      device: 'test',
      collections: { prefs: { version: 1, data: { theme: 'dark' } } },
    }

    const json = JSON.stringify(bundle, null, 2)
    const parsed = provider.importFromText(json)
    expect(parsed.version).toBe(1)
    expect(parsed.device).toBe('test')
    expect(parsed.collections.prefs).toEqual({ version: 1, data: { theme: 'dark' } })
  })

  it('importFromText throws on invalid JSON', () => {
    expect(() => provider.importFromText('not json')).toThrow()
  })

  it('importFromText throws on missing version field', () => {
    expect(() => provider.importFromText('{"collections":{}}')).toThrow(/version/)
  })

  it('listHistory returns empty array', async () => {
    expect(await provider.listHistory(10)).toEqual([])
  })

  it('push/pull are no-ops', async () => {
    const bundle: SyncBundle = {
      version: 1,
      timestamp: 1,
      device: 'test',
      collections: {},
    }
    // These should not throw
    await provider.push(bundle)
    expect(await provider.pull()).toBeNull()
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd spa && npx vitest run src/lib/sync/providers/manual-provider.test.ts`
Expected: FAIL

- [ ] **Step 3: Implement ManualProvider**

```typescript
// spa/src/lib/sync/providers/manual-provider.ts
import type { SyncBundle, SyncProvider, SyncSnapshot } from '../types'

function validateBundle(data: unknown): asserts data is SyncBundle {
  if (!data || typeof data !== 'object') throw new Error('Invalid sync bundle')
  const obj = data as Record<string, unknown>
  if (typeof obj.version !== 'number') throw new Error('Missing version field')
  if (typeof obj.timestamp !== 'number') throw new Error('Missing timestamp field')
  if (typeof obj.device !== 'string') throw new Error('Missing device field')
  if (!obj.collections || typeof obj.collections !== 'object')
    throw new Error('Missing collections field')
}

export function createManualProvider() {
  const provider: SyncProvider & {
    exportToBlob: (bundle: SyncBundle) => Blob
    importFromText: (text: string) => SyncBundle
  } = {
    id: 'manual',

    // Manual provider: push/pull are no-ops — use exportToBlob/importFromText instead
    async push() {},
    async pull() {
      return null
    },
    async pushChunks() {},
    async pullChunks() {
      return {}
    },
    async listHistory(): Promise<SyncSnapshot[]> {
      return []
    },

    exportToBlob(bundle: SyncBundle): Blob {
      const json = JSON.stringify(bundle, null, 2)
      return new Blob([json], { type: 'application/json' })
    },

    importFromText(text: string): SyncBundle {
      const data = JSON.parse(text)
      validateBundle(data)
      return data
    },
  }

  return provider
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd spa && npx vitest run src/lib/sync/providers/manual-provider.test.ts`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add spa/src/lib/sync/providers/manual-provider.ts spa/src/lib/sync/providers/manual-provider.test.ts
git commit -m "feat(sync): add ManualProvider for export/import"
```

---

## Task 6: SPA — First SyncContributor (Preferences)

**Files:**
- Create: `spa/src/lib/sync/contributors/preferences.ts`
- Test: `spa/src/lib/sync/contributors/preferences.test.ts`

This task establishes the contributor pattern. All subsequent contributors follow the same structure.

- [ ] **Step 1: Write tests**

```typescript
// spa/src/lib/sync/contributors/preferences.test.ts
import { describe, it, expect, beforeEach } from 'vitest'
import { createPreferencesContributor } from './preferences'
import { useUISettingsStore } from '../../../stores/useUISettingsStore'
import type { FullPayload } from '../types'

describe('preferencesContributor', () => {
  let contributor: ReturnType<typeof createPreferencesContributor>

  beforeEach(() => {
    useUISettingsStore.getState().reset?.()
    contributor = createPreferencesContributor()
  })

  it('has id "preferences" and strategy "full"', () => {
    expect(contributor.id).toBe('preferences')
    expect(contributor.strategy).toBe('full')
  })

  it('serialize returns current UI settings as FullPayload', () => {
    const payload = contributor.serialize() as FullPayload
    expect(payload.version).toBe(1)
    expect(payload.data).toBeDefined()
    expect(typeof payload.data).toBe('object')
  })

  it('deserialize with full-replace overwrites store', () => {
    const payload: FullPayload = {
      version: 1,
      data: { terminalRevealDelay: 999 },
    }

    contributor.deserialize(payload, { type: 'full-replace' })

    const state = useUISettingsStore.getState()
    expect(state.terminalRevealDelay).toBe(999)
  })

  it('deserialize with field-merge applies resolved fields', () => {
    // Set local state
    useUISettingsStore.getState().setTerminalRevealDelay?.(100)

    const payload: FullPayload = {
      version: 1,
      data: { terminalRevealDelay: 200, rendererType: 'dom' },
    }

    contributor.deserialize(payload, {
      type: 'field-merge',
      resolved: { terminalRevealDelay: 'remote' },
    })

    const state = useUISettingsStore.getState()
    expect(state.terminalRevealDelay).toBe(200)
  })

  it('getVersion returns 1', () => {
    expect(contributor.getVersion()).toBe(1)
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd spa && npx vitest run src/lib/sync/contributors/preferences.test.ts`
Expected: FAIL

- [ ] **Step 3: Implement preferences contributor**

> **Note:** The exact fields depend on the current `useUISettingsStore` shape. Read the store file before implementing. The pattern below is the template — adapt field names to what's actually in the store.

```typescript
// spa/src/lib/sync/contributors/preferences.ts
import type { SyncContributor, FullPayload, MergeStrategy } from '../types'
import { useUISettingsStore } from '../../../stores/useUISettingsStore'

export function createPreferencesContributor(): SyncContributor {
  return {
    id: 'preferences',
    strategy: 'full',

    serialize(): FullPayload {
      const state = useUISettingsStore.getState()
      // Extract only persistable preference fields — exclude actions/functions
      const { reset, ...rest } = state
      const data: Record<string, unknown> = {}
      for (const [key, value] of Object.entries(rest)) {
        if (typeof value !== 'function') {
          data[key] = value
        }
      }
      return { version: 1, data }
    },

    deserialize(payload: unknown, merge: MergeStrategy): void {
      const { data } = payload as FullPayload
      const setState = useUISettingsStore.setState

      if (merge.type === 'full-replace') {
        setState(data)
        return
      }

      // field-merge: only apply fields that resolved to 'remote'
      const updates: Record<string, unknown> = {}
      for (const [field, choice] of Object.entries(merge.resolved)) {
        if (choice === 'remote' && field in data) {
          updates[field] = data[field]
        }
      }
      if (Object.keys(updates).length > 0) {
        setState(updates)
      }
    },

    getVersion: () => 1,
  }
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd spa && npx vitest run src/lib/sync/contributors/preferences.test.ts`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add spa/src/lib/sync/contributors/preferences.ts spa/src/lib/sync/contributors/preferences.test.ts
git commit -m "feat(sync): add preferences SyncContributor"
```

---

## Task 7: SPA — Remaining Contributors

**Files:**
- Create: `spa/src/lib/sync/contributors/workspaces.ts`
- Create: `spa/src/lib/sync/contributors/hosts.ts`
- Create: `spa/src/lib/sync/contributors/layout.ts`
- Create: `spa/src/lib/sync/contributors/quick-commands.ts`
- Create: `spa/src/lib/sync/contributors/i18n.ts`
- Create: `spa/src/lib/sync/contributors/notification-settings.ts`
- Test: one test file per contributor

Each contributor follows the exact same pattern as Task 6. Key differences:

- [ ] **Step 1: Implement workspaces contributor**

Same pattern as preferences. `id: 'workspaces'`, serializes from `useWorkspaceStore`.

- [ ] **Step 2: Implement hosts contributor (strips auth token)**

Critical difference: `serialize()` must strip `authToken` from each host entry.

```typescript
serialize(): FullPayload {
  const state = useHostStore.getState()
  const hosts = state.hosts.map(({ authToken, ...rest }) => rest)
  return { version: 1, data: { hosts } }
}
```

- [ ] **Step 3: Implement layout contributor**

`id: 'layout'`, serializes from `useLayoutStore`.

- [ ] **Step 4: Implement quick-commands contributor**

`id: 'quick-commands'`, serializes from `useQuickCommandStore`.

- [ ] **Step 5: Implement i18n contributor**

`id: 'i18n'`, serializes from `useI18nStore`. Only sync custom translations, not the active locale's built-in strings.

- [ ] **Step 6: Implement notification-settings contributor**

`id: 'notification-settings'`, serializes from `useNotificationSettingsStore`.

- [ ] **Step 7: Write tests for each contributor**

Each test follows the same structure as Task 6:
- Check id and strategy
- serialize returns FullPayload
- deserialize with full-replace overwrites store
- deserialize with field-merge respects resolved choices
- hosts test: verify authToken is stripped from serialize output

- [ ] **Step 8: Run all contributor tests**

Run: `cd spa && npx vitest run src/lib/sync/contributors/`
Expected: ALL PASS

- [ ] **Step 9: Commit**

```bash
git add spa/src/lib/sync/contributors/
git commit -m "feat(sync): add all SyncContributors (workspaces, hosts, layout, quick-commands, i18n, notification-settings)"
```

---

## Task 8: Go Daemon — SyncStore (SQLite)

**Files:**
- Create: `internal/module/sync/store.go`
- Test: `internal/module/sync/store_test.go`

- [ ] **Step 1: Write store tests**

```go
// internal/module/sync/store_test.go
package sync

import (
	"testing"
)

func TestSyncStore_PushAndPullCanonical(t *testing.T) {
	s, err := OpenSyncStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}

	// Create group
	err = s.CreateGroup("g1")
	if err != nil {
		t.Fatal(err)
	}

	// Add client to group
	err = s.AddClientToGroup("g1", "c_aaa", "MacBook")
	if err != nil {
		t.Fatal(err)
	}

	// Push first bundle
	bundle := `{"version":1,"timestamp":1000,"device":"MacBook","collections":{"prefs":{"version":1,"data":{"theme":"dark"}}}}`
	err = s.PushBundle("c_aaa", bundle)
	if err != nil {
		t.Fatal(err)
	}

	// Pull canonical
	canonical, err := s.PullCanonical("c_aaa")
	if err != nil {
		t.Fatal(err)
	}
	if canonical == "" {
		t.Fatal("expected canonical bundle, got empty")
	}
}

func TestSyncStore_GroupIsolation(t *testing.T) {
	s, err := OpenSyncStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}

	s.CreateGroup("g1")
	s.CreateGroup("g2")
	s.AddClientToGroup("g1", "c_aaa", "MacBook")
	s.AddClientToGroup("g2", "c_bbb", "iPad")

	s.PushBundle("c_aaa", `{"version":1,"timestamp":1,"device":"MacBook","collections":{}}`)

	// Client in different group should get empty
	canonical, err := s.PullCanonical("c_bbb")
	if err != nil {
		t.Fatal(err)
	}
	if canonical != "" {
		t.Fatal("expected empty canonical for different group")
	}
}

func TestSyncStore_PairingCode(t *testing.T) {
	s, err := OpenSyncStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}

	s.CreateGroup("g1")
	s.AddClientToGroup("g1", "c_aaa", "MacBook")

	code, err := s.CreatePairingCode("c_aaa")
	if err != nil {
		t.Fatal(err)
	}
	if len(code) != 8 {
		t.Fatalf("expected 8-char code, got %d", len(code))
	}

	// Verify code
	groupID, err := s.VerifyPairingCode(code)
	if err != nil {
		t.Fatal(err)
	}
	if groupID != "g1" {
		t.Fatalf("expected group g1, got %s", groupID)
	}
}

func TestSyncStore_PairingCodeRateLimit(t *testing.T) {
	s, err := OpenSyncStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}

	s.CreateGroup("g1")
	s.AddClientToGroup("g1", "c_aaa", "MacBook")

	code, _ := s.CreatePairingCode("c_aaa")

	// Exhaust attempts with wrong code
	for i := 0; i < 5; i++ {
		s.VerifyPairingCode("WRONG123")
	}

	// Real code should now be expired
	_, err = s.VerifyPairingCode(code)
	if err == nil {
		t.Fatal("expected error after rate limit exhausted")
	}
}

func TestSyncStore_History(t *testing.T) {
	s, err := OpenSyncStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}

	s.CreateGroup("g1")
	s.AddClientToGroup("g1", "c_aaa", "MacBook")

	// Push multiple bundles
	for i := 0; i < 5; i++ {
		s.PushBundle("c_aaa", `{"version":1,"timestamp":`+string(rune('0'+i))+`,"device":"MacBook","collections":{}}`)
	}

	history, err := s.ListHistory("c_aaa", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 3 {
		t.Fatalf("expected 3 history entries, got %d", len(history))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/wake/Workspace/wake/purdex && go test ./internal/module/sync/ -v -run TestSyncStore`
Expected: FAIL — package not found

- [ ] **Step 3: Implement SyncStore**

```go
// internal/module/sync/store.go
package sync

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type SyncStore struct{ db *sql.DB }

type HistoryEntry struct {
	ID        int64
	ClientID  string
	Device    string
	Bundle    string
	Timestamp int64
}

func OpenSyncStore(path string) (*SyncStore, error) {
	dsn := path
	if path != ":memory:" {
		dsn = path + "?_pragma=journal_mode(wal)"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sync db: %w", err)
	}
	if path == ":memory:" {
		db.SetMaxOpenConns(1)
	}
	s := &SyncStore{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *SyncStore) Close() error { return s.db.Close() }

func (s *SyncStore) migrate() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS sync_groups (
			group_id  TEXT NOT NULL,
			client_id TEXT NOT NULL,
			device    TEXT NOT NULL DEFAULT '',
			last_seen INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (group_id, client_id)
		)`,
		`CREATE TABLE IF NOT EXISTS sync_canonical (
			group_id   TEXT PRIMARY KEY,
			updated_at INTEGER NOT NULL DEFAULT 0,
			bundle     TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE TABLE IF NOT EXISTS sync_history (
			id        INTEGER PRIMARY KEY AUTOINCREMENT,
			group_id  TEXT NOT NULL,
			client_id TEXT NOT NULL,
			device    TEXT NOT NULL DEFAULT '',
			bundle    TEXT NOT NULL,
			timestamp INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS sync_pairing (
			code       TEXT PRIMARY KEY,
			group_id   TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			expires_at INTEGER NOT NULL,
			attempts   INTEGER NOT NULL DEFAULT 0
		)`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
	}
	return nil
}

func (s *SyncStore) CreateGroup(groupID string) error {
	_, err := s.db.Exec(
		`INSERT OR IGNORE INTO sync_canonical (group_id, updated_at, bundle) VALUES (?, 0, '')`,
		groupID,
	)
	return err
}

func (s *SyncStore) AddClientToGroup(groupID, clientID, device string) error {
	_, err := s.db.Exec(
		`INSERT OR REPLACE INTO sync_groups (group_id, client_id, device, last_seen)
		 VALUES (?, ?, ?, ?)`,
		groupID, clientID, device, time.Now().Unix(),
	)
	return err
}

func (s *SyncStore) clientGroupID(clientID string) (string, error) {
	var groupID string
	err := s.db.QueryRow(
		`SELECT group_id FROM sync_groups WHERE client_id = ?`, clientID,
	).Scan(&groupID)
	if err != nil {
		return "", fmt.Errorf("client %s not in any group: %w", clientID, err)
	}
	return groupID, nil
}

func (s *SyncStore) PushBundle(clientID, bundle string) error {
	groupID, err := s.clientGroupID(clientID)
	if err != nil {
		return err
	}

	now := time.Now().Unix()

	// Update last_seen
	s.db.Exec(`UPDATE sync_groups SET last_seen = ? WHERE client_id = ?`, now, clientID)

	// Upsert canonical
	_, err = s.db.Exec(
		`INSERT INTO sync_canonical (group_id, updated_at, bundle) VALUES (?, ?, ?)
		 ON CONFLICT(group_id) DO UPDATE SET updated_at = excluded.updated_at, bundle = excluded.bundle`,
		groupID, now, bundle,
	)
	if err != nil {
		return err
	}

	// Insert history
	var device string
	s.db.QueryRow(`SELECT device FROM sync_groups WHERE client_id = ?`, clientID).Scan(&device)

	_, err = s.db.Exec(
		`INSERT INTO sync_history (group_id, client_id, device, bundle, timestamp) VALUES (?, ?, ?, ?, ?)`,
		groupID, clientID, device, bundle, now,
	)
	return err
}

func (s *SyncStore) PullCanonical(clientID string) (string, error) {
	groupID, err := s.clientGroupID(clientID)
	if err != nil {
		return "", err
	}

	s.db.Exec(`UPDATE sync_groups SET last_seen = ? WHERE client_id = ?`, time.Now().Unix(), clientID)

	var bundle string
	err = s.db.QueryRow(
		`SELECT bundle FROM sync_canonical WHERE group_id = ?`, groupID,
	).Scan(&bundle)
	if err != nil {
		return "", nil // No canonical yet
	}
	return bundle, nil
}

func (s *SyncStore) ListHistory(clientID string, limit int) ([]HistoryEntry, error) {
	groupID, err := s.clientGroupID(clientID)
	if err != nil {
		return nil, err
	}

	rows, err := s.db.Query(
		`SELECT id, client_id, device, bundle, timestamp FROM sync_history
		 WHERE group_id = ? ORDER BY timestamp DESC LIMIT ?`,
		groupID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []HistoryEntry
	for rows.Next() {
		var e HistoryEntry
		if err := rows.Scan(&e.ID, &e.ClientID, &e.Device, &e.Bundle, &e.Timestamp); err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	return entries, nil
}

const pairingCodeChars = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789" // no 0/O/1/I for readability

func (s *SyncStore) CreatePairingCode(clientID string) (string, error) {
	groupID, err := s.clientGroupID(clientID)
	if err != nil {
		return "", err
	}

	// Generate 8-char code
	var code strings.Builder
	for i := 0; i < 8; i++ {
		n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(pairingCodeChars))))
		code.WriteByte(pairingCodeChars[n.Int64()])
	}

	now := time.Now().Unix()
	expires := now + 300 // 5 minutes

	// Delete any existing codes for this group
	s.db.Exec(`DELETE FROM sync_pairing WHERE group_id = ?`, groupID)

	_, err = s.db.Exec(
		`INSERT INTO sync_pairing (code, group_id, created_at, expires_at, attempts) VALUES (?, ?, ?, ?, 0)`,
		code.String(), groupID, now, expires,
	)
	if err != nil {
		return "", err
	}
	return code.String(), nil
}

func (s *SyncStore) VerifyPairingCode(code string) (string, error) {
	var groupID string
	var expiresAt int64
	var attempts int

	err := s.db.QueryRow(
		`SELECT group_id, expires_at, attempts FROM sync_pairing WHERE code = ?`, code,
	).Scan(&groupID, &expiresAt, &attempts)
	if err != nil {
		// Increment attempts for any code in the DB (brute-force protection)
		s.db.Exec(`UPDATE sync_pairing SET attempts = attempts + 1`)
		return "", fmt.Errorf("invalid pairing code")
	}

	if attempts >= 5 {
		s.db.Exec(`DELETE FROM sync_pairing WHERE code = ?`, code)
		return "", fmt.Errorf("pairing code exhausted (too many attempts)")
	}

	if time.Now().Unix() > expiresAt {
		s.db.Exec(`DELETE FROM sync_pairing WHERE code = ?`, code)
		return "", fmt.Errorf("pairing code expired")
	}

	// Success — delete the code (one-time use)
	s.db.Exec(`DELETE FROM sync_pairing WHERE code = ?`, code)
	return groupID, nil
}

func generateGroupID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return "g_" + hex.EncodeToString(b)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /Users/wake/Workspace/wake/purdex && go test ./internal/module/sync/ -v -run TestSyncStore`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/module/sync/store.go internal/module/sync/store_test.go
git commit -m "feat(sync): add Go SyncStore with SQLite (groups, canonical, history, pairing)"
```

---

## Task 9: Go Daemon — Sync Module & HTTP Handlers

**Files:**
- Create: `internal/module/sync/module.go`
- Create: `internal/module/sync/handler.go`
- Modify: `cmd/pdx/main.go` (register module)

- [ ] **Step 1: Implement sync module**

```go
// internal/module/sync/module.go
package sync

import (
	"context"
	"net/http"
	"path/filepath"

	"github.com/wake/purdex/internal/core"
)

type SyncModule struct {
	core  *core.Core
	store *SyncStore
}

func New() *SyncModule { return &SyncModule{} }

func (m *SyncModule) Name() string          { return "sync" }
func (m *SyncModule) Dependencies() []string { return nil }

func (m *SyncModule) Init(c *core.Core) error {
	m.core = c
	dbPath := filepath.Join(c.Cfg.DataDir, "sync.db")
	var err error
	m.store, err = OpenSyncStore(dbPath)
	return err
}

func (m *SyncModule) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/sync/push", m.handlePush)
	mux.HandleFunc("GET /api/sync/pull", m.handlePull)
	mux.HandleFunc("GET /api/sync/history", m.handleHistory)
	mux.HandleFunc("POST /api/sync/group/create", m.handleGroupCreate)
	mux.HandleFunc("POST /api/sync/group/join", m.handleGroupJoin)
	mux.HandleFunc("POST /api/sync/pair/create", m.handlePairCreate)
	mux.HandleFunc("POST /api/sync/pair/verify", m.handlePairVerify)
	mux.HandleFunc("GET /api/sync/group/members", m.handleGroupMembers)
	mux.HandleFunc("DELETE /api/sync/group/member", m.handleGroupRemoveMember)
}

func (m *SyncModule) Start(ctx context.Context) error { return nil }

func (m *SyncModule) Stop(ctx context.Context) error {
	if m.store != nil {
		return m.store.Close()
	}
	return nil
}
```

- [ ] **Step 2: Implement HTTP handlers**

```go
// internal/module/sync/handler.go
package sync

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
)

func (m *SyncModule) handlePush(w http.ResponseWriter, r *http.Request) {
	clientID := r.URL.Query().Get("clientId")
	if clientID == "" {
		http.Error(w, "missing clientId", http.StatusBadRequest)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 10<<20)) // 10MB limit
	if err != nil {
		http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
		return
	}

	if err := m.store.PushBundle(clientID, string(body)); err != nil {
		http.Error(w, "push: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (m *SyncModule) handlePull(w http.ResponseWriter, r *http.Request) {
	clientID := r.URL.Query().Get("clientId")
	if clientID == "" {
		http.Error(w, "missing clientId", http.StatusBadRequest)
		return
	}

	bundle, err := m.store.PullCanonical(clientID)
	if err != nil {
		http.Error(w, "pull: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if bundle == "" {
		w.Write([]byte("null"))
		return
	}
	w.Write([]byte(bundle))
}

func (m *SyncModule) handleHistory(w http.ResponseWriter, r *http.Request) {
	clientID := r.URL.Query().Get("clientId")
	if clientID == "" {
		http.Error(w, "missing clientId", http.StatusBadRequest)
		return
	}

	limit := 100
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 && n <= 100 {
			limit = n
		}
	}

	entries, err := m.store.ListHistory(clientID, limit)
	if err != nil {
		http.Error(w, "history: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(entries)
}

func (m *SyncModule) handleGroupCreate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ClientID string `json:"clientId"`
		Device   string `json:"device"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ClientID == "" {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}

	groupID := generateGroupID()
	if err := m.store.CreateGroup(groupID); err != nil {
		http.Error(w, "create group: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if err := m.store.AddClientToGroup(groupID, req.ClientID, req.Device); err != nil {
		http.Error(w, "add client: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"groupId": groupID})
}

func (m *SyncModule) handleGroupJoin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		GroupID  string `json:"groupId"`
		ClientID string `json:"clientId"`
		Device   string `json:"device"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.GroupID == "" || req.ClientID == "" {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}

	if err := m.store.AddClientToGroup(req.GroupID, req.ClientID, req.Device); err != nil {
		http.Error(w, "join group: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (m *SyncModule) handlePairCreate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ClientID string `json:"clientId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ClientID == "" {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}

	code, err := m.store.CreatePairingCode(req.ClientID)
	if err != nil {
		http.Error(w, "create pairing: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"code": code})
}

func (m *SyncModule) handlePairVerify(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Code     string `json:"code"`
		ClientID string `json:"clientId"`
		Device   string `json:"device"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Code == "" || req.ClientID == "" {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}

	groupID, err := m.store.VerifyPairingCode(req.Code)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}

	if err := m.store.AddClientToGroup(groupID, req.ClientID, req.Device); err != nil {
		http.Error(w, "join group: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"groupId": groupID})
}

func (m *SyncModule) handleGroupMembers(w http.ResponseWriter, r *http.Request) {
	clientID := r.URL.Query().Get("clientId")
	if clientID == "" {
		http.Error(w, "missing clientId", http.StatusBadRequest)
		return
	}

	groupID, err := m.store.clientGroupID(clientID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	rows, err := m.store.db.Query(
		`SELECT client_id, device, last_seen FROM sync_groups WHERE group_id = ? ORDER BY last_seen DESC`,
		groupID,
	)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	type Member struct {
		ClientID string `json:"clientId"`
		Device   string `json:"device"`
		LastSeen int64  `json:"lastSeen"`
	}
	var members []Member
	for rows.Next() {
		var m Member
		rows.Scan(&m.ClientID, &m.Device, &m.LastSeen)
		members = append(members, m)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(members)
}

func (m *SyncModule) handleGroupRemoveMember(w http.ResponseWriter, r *http.Request) {
	clientID := r.URL.Query().Get("clientId")
	targetID := r.URL.Query().Get("targetId")
	if clientID == "" || targetID == "" {
		http.Error(w, "missing clientId or targetId", http.StatusBadRequest)
		return
	}

	// Verify requester is in same group
	groupID, err := m.store.clientGroupID(clientID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}

	targetGroup, err := m.store.clientGroupID(targetID)
	if err != nil || targetGroup != groupID {
		http.Error(w, "target not in same group", http.StatusForbidden)
		return
	}

	m.store.db.Exec(`DELETE FROM sync_groups WHERE client_id = ? AND group_id = ?`, targetID, groupID)
	w.WriteHeader(http.StatusNoContent)
}
```

- [ ] **Step 3: Register sync module in main.go**

Find the module registration section in `cmd/pdx/main.go` and add:

```go
syncMod := sync.New()
// Add to modules list alongside session, agent, stream, fs modules
```

- [ ] **Step 4: Run daemon tests**

Run: `cd /Users/wake/Workspace/wake/purdex && go test ./internal/module/sync/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/module/sync/module.go internal/module/sync/handler.go cmd/pdx/main.go
git commit -m "feat(sync): add Go sync module with HTTP handlers and pairing API"
```

---

## Task 10: SPA — Daemon Provider

**Files:**
- Create: `spa/src/lib/sync/providers/daemon-provider.ts`
- Test: `spa/src/lib/sync/providers/daemon-provider.test.ts`

- [ ] **Step 1: Write tests**

```typescript
// spa/src/lib/sync/providers/daemon-provider.test.ts
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { createDaemonProvider } from './daemon-provider'

// Mock hostFetch
const mockFetch = vi.fn()
vi.mock('../../host-api', () => ({
  hostFetch: (...args: unknown[]) => mockFetch(...args),
}))

describe('DaemonProvider', () => {
  let provider: ReturnType<typeof createDaemonProvider>

  beforeEach(() => {
    mockFetch.mockReset()
    provider = createDaemonProvider('host1', 'c_test')
  })

  it('has id "daemon"', () => {
    expect(provider.id).toBe('daemon')
  })

  it('push calls POST /api/sync/push with clientId', async () => {
    mockFetch.mockResolvedValue(new Response(null, { status: 204 }))

    const bundle = {
      version: 1,
      timestamp: 1,
      device: 'test',
      collections: {},
    }

    await provider.push(bundle)

    expect(mockFetch).toHaveBeenCalledWith(
      'host1',
      '/api/sync/push?clientId=c_test',
      expect.objectContaining({
        method: 'POST',
        body: JSON.stringify(bundle),
      }),
    )
  })

  it('pull calls GET /api/sync/pull and parses JSON', async () => {
    const bundle = {
      version: 1,
      timestamp: 1,
      device: 'remote',
      collections: {},
    }
    mockFetch.mockResolvedValue(new Response(JSON.stringify(bundle)))

    const result = await provider.pull()
    expect(result).toEqual(bundle)
  })

  it('pull returns null when daemon has no data', async () => {
    mockFetch.mockResolvedValue(new Response('null'))

    const result = await provider.pull()
    expect(result).toBeNull()
  })

  it('listHistory calls GET /api/sync/history', async () => {
    mockFetch.mockResolvedValue(new Response('[]'))

    const result = await provider.listHistory(10)
    expect(result).toEqual([])
    expect(mockFetch).toHaveBeenCalledWith(
      'host1',
      '/api/sync/history?clientId=c_test&limit=10',
      undefined,
    )
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd spa && npx vitest run src/lib/sync/providers/daemon-provider.test.ts`
Expected: FAIL

- [ ] **Step 3: Implement DaemonProvider**

```typescript
// spa/src/lib/sync/providers/daemon-provider.ts
import type { SyncBundle, SyncProvider, SyncSnapshot } from '../types'
import { hostFetch } from '../../host-api'

export function createDaemonProvider(hostId: string, clientId: string) {
  const provider: SyncProvider = {
    id: 'daemon',

    async push(bundle: SyncBundle): Promise<void> {
      const res = await hostFetch(
        hostId,
        `/api/sync/push?clientId=${clientId}`,
        {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(bundle),
        },
      )
      if (!res.ok) throw new Error(`push failed: ${res.status}`)
    },

    async pull(): Promise<SyncBundle | null> {
      const res = await hostFetch(
        hostId,
        `/api/sync/pull?clientId=${clientId}`,
      )
      if (!res.ok) throw new Error(`pull failed: ${res.status}`)
      const data = await res.json()
      return data ?? null
    },

    async pushChunks(chunks: Record<string, Uint8Array>): Promise<void> {
      // TODO: implement when content-addressed strategy is needed (editor module)
      void chunks
    },

    async pullChunks(hashes: string[]): Promise<Record<string, Uint8Array>> {
      // TODO: implement when content-addressed strategy is needed (editor module)
      void hashes
      return {}
    },

    async listHistory(limit: number): Promise<SyncSnapshot[]> {
      const res = await hostFetch(
        hostId,
        `/api/sync/history?clientId=${clientId}&limit=${limit}`,
      )
      if (!res.ok) throw new Error(`history failed: ${res.status}`)
      return res.json()
    },
  }

  return provider
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd spa && npx vitest run src/lib/sync/providers/daemon-provider.test.ts`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add spa/src/lib/sync/providers/daemon-provider.ts spa/src/lib/sync/providers/daemon-provider.test.ts
git commit -m "feat(sync): add DaemonProvider with REST push/pull"
```

---

## Task 11: SPA — Registration & Wiring

**Files:**
- Create: `spa/src/lib/sync/register-sync.ts`
- Modify: `spa/src/lib/register-modules.tsx`

- [ ] **Step 1: Create register-sync**

```typescript
// spa/src/lib/sync/register-sync.ts
import { createSyncEngine } from './engine'
import { createPreferencesContributor } from './contributors/preferences'
import { createWorkspacesContributor } from './contributors/workspaces'
import { createHostsContributor } from './contributors/hosts'
import { createLayoutContributor } from './contributors/layout'
import { createQuickCommandsContributor } from './contributors/quick-commands'
import { createI18nContributor } from './contributors/i18n'
import { createNotificationSettingsContributor } from './contributors/notification-settings'

export const syncEngine = createSyncEngine()

export function registerSyncContributors(): void {
  syncEngine.register(createPreferencesContributor())
  syncEngine.register(createWorkspacesContributor())
  syncEngine.register(createHostsContributor())
  syncEngine.register(createLayoutContributor())
  syncEngine.register(createQuickCommandsContributor())
  syncEngine.register(createI18nContributor())
  syncEngine.register(createNotificationSettingsContributor())
}
```

- [ ] **Step 2: Wire into register-modules.tsx**

Add to `registerBuiltinModules()` in `spa/src/lib/register-modules.tsx`:

```typescript
import { registerSyncContributors } from './sync/register-sync'
import { SyncSection } from '../components/settings/SyncSection'

// Inside registerBuiltinModules():
registerSyncContributors()

// Replace the reserved sync section:
registerSettingsSection({
  id: 'sync',
  label: 'settings.section.sync',
  order: 11,
  component: SyncSection,
})
```

- [ ] **Step 3: Commit**

```bash
git add spa/src/lib/sync/register-sync.ts spa/src/lib/register-modules.tsx
git commit -m "feat(sync): wire SyncEngine + contributors into module registration"
```

---

## Task 12: SPA — Sync Settings UI

**Files:**
- Create: `spa/src/components/settings/SyncSection.tsx`
- Create: `spa/src/components/settings/SyncAddDeviceDialog.tsx`
- Create: `spa/src/components/settings/SyncConflictBanner.tsx`
- Create: `spa/src/components/settings/SyncHistoryDialog.tsx`

This task builds the Settings → Sync UI from the spec §6 mockup. The UI is mostly standard settings patterns already established in the codebase.

- [ ] **Step 1: Implement SyncSection**

Main sync settings page with:
- Provider selector (Off / Daemon / File)
- Sync host dropdown (when Daemon selected)
- Sync group member list
- Module checkboxes
- Sync status + "Sync Now" button
- Export/Import buttons

Read existing settings components (e.g., `AppearanceSection`, `TerminalSection`) to follow the established pattern for section layout, labels, and i18n.

- [ ] **Step 2: Implement SyncAddDeviceDialog**

Dialog with:
- QR code rendering (use a lightweight QR library like `qrcode-generator` or inline SVG)
- QR content: `https://desk.purdex.app/pair#<base64(host:port)>.<token>`
- 8-char pairing code display
- Countdown timer (5 min)
- "5 次錯誤後失效" notice
- Calls `POST /api/sync/pair/create` on open, `POST /api/sync/pair/verify` from new device

- [ ] **Step 3: Implement SyncConflictBanner**

Non-modal banner at top of Sync section:
- Shows when `conflicts.length > 0`
- "⚠ N 個欄位有衝突 [查看詳情]"
- Expand to show per-field local vs remote comparison
- "保留本地" / "採用遠端" per field + "全部保留本地" / "全部採用遠端" bulk actions

- [ ] **Step 4: Implement SyncHistoryDialog**

Dialog listing snapshots:
- Columns: timestamp, device, source (local/remote), trigger (auto/manual)
- Click to see summary (collection names + change counts)
- "還原" button → confirm → full-replace deserialize

- [ ] **Step 5: Manual integration test**

Start dev server: `cd spa && pnpm run dev`
1. Open Settings → Sync section
2. Verify provider selector renders
3. Toggle modules on/off
4. Click "Export All" → verify `.purdex-sync` file downloads
5. Click "Import" → select the exported file → verify data loads

- [ ] **Step 6: Commit**

```bash
git add spa/src/components/settings/Sync*.tsx
git commit -m "feat(sync): add Sync Settings UI (provider, modules, export/import, pairing, history)"
```

---

## Task 13: SPA — File Provider (Electron Only)

**Files:**
- Create: `spa/src/lib/sync/providers/file-provider.ts`
- Test: `spa/src/lib/sync/providers/file-provider.test.ts`

> This provider uses Electron IPC for filesystem access. It only works in Electron context and is disabled in the web SPA.

- [ ] **Step 1: Write tests (mock Electron IPC)**

```typescript
// spa/src/lib/sync/providers/file-provider.test.ts
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { createFileProvider } from './file-provider'

const mockIpc = {
  readFile: vi.fn(),
  writeFile: vi.fn(),
  readdir: vi.fn(),
  watch: vi.fn(),
  mkdir: vi.fn(),
}

describe('FileProvider', () => {
  let provider: ReturnType<typeof createFileProvider>

  beforeEach(() => {
    vi.resetAllMocks()
    provider = createFileProvider('/sync/folder', mockIpc as any)
  })

  it('has id "file"', () => {
    expect(provider.id).toBe('file')
  })

  it('push writes manifest.json', async () => {
    mockIpc.writeFile.mockResolvedValue(undefined)
    mockIpc.mkdir.mockResolvedValue(undefined)

    const bundle = { version: 1, timestamp: 1, device: 'test', collections: {} }
    await provider.push(bundle)

    expect(mockIpc.writeFile).toHaveBeenCalledWith(
      '/sync/folder/manifest.json',
      expect.any(String),
    )
  })

  it('pull reads manifest.json', async () => {
    const bundle = { version: 1, timestamp: 1, device: 'test', collections: {} }
    mockIpc.readFile.mockResolvedValue(JSON.stringify(bundle))

    const result = await provider.pull()
    expect(result).toEqual(bundle)
  })

  it('pull returns null when manifest missing', async () => {
    mockIpc.readFile.mockRejectedValue(new Error('ENOENT'))

    const result = await provider.pull()
    expect(result).toBeNull()
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd spa && npx vitest run src/lib/sync/providers/file-provider.test.ts`
Expected: FAIL

- [ ] **Step 3: Implement FileProvider**

```typescript
// spa/src/lib/sync/providers/file-provider.ts
import type { SyncBundle, SyncProvider, SyncSnapshot } from '../types'

interface FileSystemIpc {
  readFile(path: string): Promise<string>
  writeFile(path: string, content: string): Promise<void>
  readdir(path: string): Promise<string[]>
  mkdir(path: string): Promise<void>
  watch?(path: string, callback: () => void): () => void
}

export function createFileProvider(syncFolder: string, fs: FileSystemIpc) {
  const manifestPath = `${syncFolder}/manifest.json`
  const historyDir = `${syncFolder}/history`
  const chunksDir = `${syncFolder}/chunks`

  async function ensureDirs(): Promise<void> {
    await fs.mkdir(syncFolder).catch(() => {})
    await fs.mkdir(historyDir).catch(() => {})
    await fs.mkdir(chunksDir).catch(() => {})
  }

  const provider: SyncProvider = {
    id: 'file',

    async push(bundle: SyncBundle): Promise<void> {
      await ensureDirs()

      // Write manifest
      await fs.writeFile(manifestPath, JSON.stringify(bundle, null, 2))

      // Write history snapshot
      const filename = new Date(bundle.timestamp).toISOString().replace(/[:.]/g, '-')
      await fs.writeFile(
        `${historyDir}/${filename}.json`,
        JSON.stringify(bundle),
      )
    },

    async pull(): Promise<SyncBundle | null> {
      try {
        const content = await fs.readFile(manifestPath)
        return JSON.parse(content)
      } catch {
        return null
      }
    },

    async pushChunks(chunks: Record<string, Uint8Array>): Promise<void> {
      await ensureDirs()
      for (const [hash, data] of Object.entries(chunks)) {
        // Encode Uint8Array as base64 string for text-based fs
        const base64 = btoa(String.fromCharCode(...data))
        await fs.writeFile(`${chunksDir}/${hash}.bin`, base64)
      }
    },

    async pullChunks(hashes: string[]): Promise<Record<string, Uint8Array>> {
      const result: Record<string, Uint8Array> = {}
      for (const hash of hashes) {
        try {
          const base64 = await fs.readFile(`${chunksDir}/${hash}.bin`)
          const binary = atob(base64)
          result[hash] = new Uint8Array([...binary].map((c) => c.charCodeAt(0)))
        } catch {
          // Chunk not found — skip
        }
      }
      return result
    },

    async listHistory(limit: number): Promise<SyncSnapshot[]> {
      try {
        const files = await fs.readdir(historyDir)
        const jsonFiles = files.filter((f) => f.endsWith('.json')).sort().reverse().slice(0, limit)

        return jsonFiles.map((filename) => ({
          id: filename.replace('.json', ''),
          timestamp: new Date(filename.replace('.json', '').replace(/-/g, (m, i) =>
            i > 9 ? (i === 10 ? 'T' : i === 13 || i === 16 ? ':' : '.') : m,
          )).getTime() || 0,
          device: '',
          source: 'remote' as const,
          trigger: 'auto' as const,
          bundleRef: `${historyDir}/${filename}`,
        }))
      } catch {
        return []
      }
    },
  }

  return provider
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd spa && npx vitest run src/lib/sync/providers/file-provider.test.ts`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add spa/src/lib/sync/providers/file-provider.ts spa/src/lib/sync/providers/file-provider.test.ts
git commit -m "feat(sync): add FileProvider for iCloud/Syncthing sync folder"
```

---

## Task 14: Integration Test — Full Sync Flow

**Files:**
- Create: `spa/src/lib/sync/sync-flow.test.ts`

End-to-end test covering the complete sync cycle: register → push → pull → conflict → resolve.

- [ ] **Step 1: Write integration test**

```typescript
// spa/src/lib/sync/sync-flow.test.ts
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { createSyncEngine } from './engine'
import type { SyncBundle, SyncContributor, FullPayload, MergeStrategy } from './types'

/**
 * In-memory store simulating a Zustand store.
 * Two instances simulate two devices.
 */
function createTestContributor(id: string, initialData: Record<string, unknown>) {
  let data = { ...initialData }
  const contrib: SyncContributor = {
    id,
    strategy: 'full',
    serialize: () => ({ version: 1, data: { ...data } }),
    deserialize: (payload: unknown, merge: MergeStrategy) => {
      const p = (payload as FullPayload).data
      if (merge.type === 'full-replace') {
        data = { ...p }
        return
      }
      for (const [field, choice] of Object.entries(merge.resolved)) {
        if (choice === 'remote' && field in p) {
          data[field] = p[field]
        }
      }
    },
    getVersion: () => 1,
  }
  return { contrib, getData: () => ({ ...data }), setData: (d: Record<string, unknown>) => { data = { ...d } } }
}

function createInMemoryProvider() {
  let stored: SyncBundle | null = null
  return {
    id: 'memory',
    push: vi.fn(async (bundle: SyncBundle) => { stored = bundle }),
    pull: vi.fn(async () => stored),
    pushChunks: vi.fn(async () => {}),
    pullChunks: vi.fn(async () => ({})),
    listHistory: vi.fn(async () => []),
  }
}

describe('Full sync flow', () => {
  it('Device A pushes, Device B pulls — no conflict', async () => {
    // Device A
    const engineA = createSyncEngine()
    const prefsA = createTestContributor('prefs', { theme: 'dark', locale: 'en' })
    engineA.register(prefsA.contrib)

    const provider = createInMemoryProvider()

    // A pushes
    const bundleA = await engineA.push(provider, 'MacBook', ['prefs'])

    // Device B (fresh — no lastSynced)
    const engineB = createSyncEngine()
    const prefsB = createTestContributor('prefs', { theme: 'light', locale: 'en' })
    engineB.register(prefsB.contrib)

    // B pulls (first sync, full-replace)
    const result = await engineB.pull(provider, null, ['prefs'])
    expect(result.conflicts).toHaveLength(0)
    expect(prefsB.getData().theme).toBe('dark') // Replaced from A
  })

  it('Both devices change different fields — auto-merge', async () => {
    const provider = createInMemoryProvider()

    // Initial sync state (both devices synced this)
    const lastSynced: SyncBundle = {
      version: 1,
      timestamp: 1000,
      device: 'any',
      collections: {
        prefs: { version: 1, data: { theme: 'light', locale: 'en' } },
      },
    }

    // Device A changes theme
    const engineA = createSyncEngine()
    const prefsA = createTestContributor('prefs', { theme: 'dark', locale: 'en' })
    engineA.register(prefsA.contrib)
    await engineA.push(provider, 'MacBook', ['prefs'])

    // Device B changes locale, pulls
    const engineB = createSyncEngine()
    const prefsB = createTestContributor('prefs', { theme: 'light', locale: 'zh-TW' })
    engineB.register(prefsB.contrib)

    const result = await engineB.pull(provider, lastSynced, ['prefs'])
    expect(result.conflicts).toHaveLength(0)
    expect(prefsB.getData().theme).toBe('dark')    // from A
    expect(prefsB.getData().locale).toBe('zh-TW')  // kept B's
  })

  it('Both devices change same field — conflict detected', async () => {
    const provider = createInMemoryProvider()

    const lastSynced: SyncBundle = {
      version: 1,
      timestamp: 1000,
      device: 'any',
      collections: {
        prefs: { version: 1, data: { theme: 'light' } },
      },
    }

    // A changes theme to dark
    const engineA = createSyncEngine()
    const prefsA = createTestContributor('prefs', { theme: 'dark' })
    engineA.register(prefsA.contrib)
    await engineA.push(provider, 'MacBook', ['prefs'])

    // B changes theme to solarized, pulls
    const engineB = createSyncEngine()
    const prefsB = createTestContributor('prefs', { theme: 'solarized' })
    engineB.register(prefsB.contrib)

    const result = await engineB.pull(provider, lastSynced, ['prefs'])
    expect(result.conflicts).toHaveLength(1)
    expect(result.conflicts[0].field).toBe('theme')
    expect(result.conflicts[0].local).toBe('solarized')
    expect(result.conflicts[0].remote.value).toBe('dark')

    // Resolve: user picks remote
    engineB.resolveConflicts(
      result.appliedBundle!,
      result.conflicts,
      { theme: 'remote' },
    )
    expect(prefsB.getData().theme).toBe('dark')
  })
})
```

- [ ] **Step 2: Run integration test**

Run: `cd spa && npx vitest run src/lib/sync/sync-flow.test.ts`
Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add spa/src/lib/sync/sync-flow.test.ts
git commit -m "test(sync): add integration test for full sync flow with conflict resolution"
```

---

## Task 15: Lint, Build, Final Verification

- [ ] **Step 1: Run all sync tests**

Run: `cd spa && npx vitest run src/lib/sync/`
Expected: ALL PASS

- [ ] **Step 2: Run full test suite**

Run: `cd spa && npx vitest run`
Expected: ALL PASS (no regressions)

- [ ] **Step 3: Run lint**

Run: `cd spa && pnpm run lint`
Expected: No errors

- [ ] **Step 4: Run build**

Run: `cd spa && pnpm run build`
Expected: Build succeeds

- [ ] **Step 5: Run Go tests**

Run: `cd /Users/wake/Workspace/wake/purdex && go test ./internal/module/sync/ -v`
Expected: ALL PASS

- [ ] **Step 6: Commit any lint/build fixes**

```bash
git add -A
git commit -m "chore(sync): fix lint and build issues"
```
