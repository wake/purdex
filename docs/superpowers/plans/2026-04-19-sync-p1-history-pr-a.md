# Sync P1 History — PR A Implementation Plan (SPA-only)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 讓使用者在 SPA 本地累積 snapshot（manual sync / pre-import / pre-restore 三種觸發）、透過 `/settings/sync/history` subpage 瀏覽、一鍵 restore 還原任一筆到當前 state；restore 走 append-only（只改 local state，下次 sync 自然產生新 history）。

**Architecture:** 純 IndexedDB local store + tiered retention (hourly/daily/weekly/monthly + pre-op pool)；沿用 P0 `/settings/<section>` deep-link infra 並擴充至雙層 subsection；UI Local tab only，Remote tab disabled（PR B 才啟用）。

**Tech Stack:** TypeScript / React 19 / Zustand 5 / Vitest + fake-indexeddb / wouter / `idb` (Jake Archibald)

**Spec：** `docs/superpowers/specs/2026-04-19-sync-p1-history-design.md`

---

## File Structure

**新增檔案（SPA）**：

```
spa/src/lib/storage/idb.ts                                  # idb 封裝
spa/src/lib/sync/snapshot-types.ts                          # metadata / stored / trigger 型別
spa/src/lib/sync/snapshot-store.ts                          # IDB store
spa/src/lib/sync/snapshot-compaction.ts                     # tiered policy 純函式
spa/src/lib/sync/snapshot-diff.ts                           # contributor-level deepEqual diff
spa/src/lib/sync/__tests__/snapshot-store.test.ts
spa/src/lib/sync/__tests__/snapshot-compaction.test.ts
spa/src/lib/sync/__tests__/snapshot-diff.test.ts

spa/src/features/settings/sections/sync-history/
  ├─ SnapshotHistoryPage.tsx
  ├─ SnapshotHistoryPage.test.tsx
  ├─ HistoryTabs.tsx
  ├─ HistoryTabs.test.tsx
  ├─ HistoryList.tsx
  ├─ HistoryList.test.tsx
  ├─ HistoryRow.tsx
  ├─ HistoryRow.test.tsx
  ├─ SnapshotDetail.tsx
  ├─ SnapshotDetail.test.tsx
  ├─ SnapshotRestoreDialog.tsx
  ├─ SnapshotRestoreDialog.test.tsx
  └─ hooks/
     ├─ useLocalHistory.ts
     ├─ useLocalHistory.test.ts
     ├─ useSnapshotDiff.ts
     └─ useSnapshotDiff.test.ts
```

**修改檔案（SPA）**：

```
spa/src/test-setup.ts                                       # 加 fake-indexeddb polyfill
spa/src/lib/sync/three-way-merge.ts                         # export deepEqual
spa/src/lib/sync/use-sync-store.ts                          # 新 actions (createPreOperationSnapshot, restoreFromSnapshot)
spa/src/lib/sync/contributors/hosts.ts                      # deserialize 保留 token
spa/src/lib/route-utils.ts                                  # SETTINGS_SECTION_PATTERN 支援 subsection
spa/src/components/SettingsPage.tsx                         # subsection dispatch
spa/src/components/settings/SyncSection.tsx                 # View History 按鈕 + pre-import wiring + post-sync snapshot + status banner
spa/src/i18n/locales/en.json                                # 新 keys
spa/src/i18n/locales/zh-TW.json                             # 新 keys
spa/package.json                                            # 加 idb + fake-indexeddb
```

---

## Task Overview

| Phase | Tasks | Concern |
|-------|-------|---------|
| Foundations | 1–9 | Deps / 型別 / IDB / compaction / diff / hosts token |
| Store + Routing | 10–12 | useSyncStore actions / route extension |
| UI Components | 13–19 | hooks / list / detail / dialog / page composition |
| Integration | 20–24 | i18n / SyncSection 按鈕 + wiring / session pristine |

---

### Task 1: Install deps + test setup

**Files:**
- Modify: `spa/package.json`
- Modify: `spa/src/test-setup.ts`
- Modify: `pnpm-lock.yaml`（自動）

- [ ] **Step 1: 加入 idb (prod) + fake-indexeddb (dev)**

Run:
```bash
cd spa && pnpm add idb && pnpm add -D fake-indexeddb
```

Expected: 兩個套件加到 `spa/package.json` 的 `dependencies` / `devDependencies`；`pnpm-lock.yaml` 更新。

- [ ] **Step 2: 在 test-setup 加 fake-indexeddb polyfill**

File: `spa/src/test-setup.ts`

在既有 imports 之後、第一個 `beforeAll` 之前加：

```typescript
import 'fake-indexeddb/auto'
```

- [ ] **Step 3: 跑 vitest 確認沒壞既有 tests**

Run: `cd spa && npx vitest run --reporter=dot`
Expected: PASS（與 main 相同結果）

- [ ] **Step 4: Commit**

```bash
git add spa/package.json pnpm-lock.yaml spa/src/test-setup.ts
git commit -m "chore(deps): add idb + fake-indexeddb for snapshot store"
```

---

### Task 2: Export deepEqual from three-way-merge

**Files:**
- Modify: `spa/src/lib/sync/three-way-merge.ts`
- Create: `spa/src/lib/sync/__tests__/deep-equal.test.ts`

- [ ] **Step 1: Write failing test**

File: `spa/src/lib/sync/__tests__/deep-equal.test.ts`

```typescript
import { describe, expect, it } from 'vitest'
import { deepEqual } from '../three-way-merge'

describe('deepEqual (exported)', () => {
  it('handles primitives', () => {
    expect(deepEqual(1, 1)).toBe(true)
    expect(deepEqual('a', 'b')).toBe(false)
  })

  it('handles nested objects', () => {
    expect(deepEqual({ a: { b: 1 } }, { a: { b: 1 } })).toBe(true)
    expect(deepEqual({ a: { b: 1 } }, { a: { b: 2 } })).toBe(false)
  })

  it('handles arrays', () => {
    expect(deepEqual([1, 2], [1, 2])).toBe(true)
    expect(deepEqual([1, 2], [2, 1])).toBe(false)
  })

  it('handles null / undefined asymmetry', () => {
    expect(deepEqual(null, undefined)).toBe(false)
    expect(deepEqual(null, null)).toBe(true)
  })
})
```

- [ ] **Step 2: Run test → fail**

Run: `cd spa && npx vitest run src/lib/sync/__tests__/deep-equal.test.ts`
Expected: FAIL `deepEqual is not exported`

- [ ] **Step 3: Export deepEqual**

File: `spa/src/lib/sync/three-way-merge.ts`

把現有的 `function deepEqual(a: unknown, b: unknown): boolean {` 改為：

```typescript
export function deepEqual(a: unknown, b: unknown): boolean {
```

- [ ] **Step 4: Run test → pass**

Run: `cd spa && npx vitest run src/lib/sync/__tests__/deep-equal.test.ts`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add spa/src/lib/sync/three-way-merge.ts spa/src/lib/sync/__tests__/deep-equal.test.ts
git commit -m "refactor(sync): export deepEqual from three-way-merge"
```

---

### Task 3: idb wrapper (lib/storage/idb.ts)

**Files:**
- Create: `spa/src/lib/storage/idb.ts`
- Create: `spa/src/lib/storage/__tests__/idb.test.ts`

- [ ] **Step 1: Write failing test**

File: `spa/src/lib/storage/__tests__/idb.test.ts`

```typescript
import { describe, expect, it, beforeEach } from 'vitest'
import { openIDB, closeAllIDB } from '../idb'

describe('openIDB', () => {
  beforeEach(async () => {
    await closeAllIDB()
    indexedDB.deleteDatabase('test-db')
  })

  it('creates an objectStore via upgrade callback', async () => {
    const db = await openIDB('test-db', 1, (raw) => {
      raw.createObjectStore('items', { keyPath: 'id' })
    })
    expect(db.objectStoreNames.contains('items')).toBe(true)
  })

  it('reuses the same connection for same name', async () => {
    const a = await openIDB('test-db', 1, (raw) => {
      raw.createObjectStore('items', { keyPath: 'id' })
    })
    const b = await openIDB('test-db', 1, (raw) => {
      raw.createObjectStore('items', { keyPath: 'id' })
    })
    expect(a).toBe(b)
  })
})
```

- [ ] **Step 2: Run → fail**

Run: `cd spa && npx vitest run src/lib/storage/__tests__/idb.test.ts`
Expected: FAIL `Cannot find module '../idb'`

- [ ] **Step 3: Implement idb wrapper**

File: `spa/src/lib/storage/idb.ts`

```typescript
import { openDB, type IDBPDatabase } from 'idb'

const openConnections = new Map<string, Promise<IDBPDatabase>>()

/**
 * Open (or reuse) an IndexedDB database by name.
 *
 * The `upgrade` callback runs only when the version changes; use it to
 * create object stores / indexes. Re-opening the same name returns the
 * shared cached connection.
 */
export function openIDB(
  name: string,
  version: number,
  upgrade: (db: IDBPDatabase) => void,
): Promise<IDBPDatabase> {
  const cached = openConnections.get(name)
  if (cached) return cached

  const p = openDB(name, version, {
    upgrade(db) {
      upgrade(db)
    },
  })
  openConnections.set(name, p)
  return p
}

/** Close all cached connections (used by tests between cases). */
export async function closeAllIDB(): Promise<void> {
  for (const p of openConnections.values()) {
    try {
      const db = await p
      db.close()
    } catch {
      // ignore
    }
  }
  openConnections.clear()
}
```

- [ ] **Step 4: Run → pass**

Run: `cd spa && npx vitest run src/lib/storage/__tests__/idb.test.ts`
Expected: PASS (2/2)

- [ ] **Step 5: Commit**

```bash
git add spa/src/lib/storage/idb.ts spa/src/lib/storage/__tests__/idb.test.ts
git commit -m "feat(storage): add idb wrapper with cached connections"
```

---

### Task 4: Snapshot types

**Files:**
- Create: `spa/src/lib/sync/snapshot-types.ts`

- [ ] **Step 1: Write types file (no test needed — pure types)**

File: `spa/src/lib/sync/snapshot-types.ts`

```typescript
import type { SyncBundle } from './types'

export type SnapshotTrigger =
  | 'auto'         // reserved for future auto-sync feature; P1 does not emit this
  | 'manual'
  | 'pre-import'
  | 'pre-restore'

export interface SnapshotMetadata {
  id: string
  timestamp: number           // ms epoch
  device: string              // SyncBundle.device (top-level)
  trigger: SnapshotTrigger
  bundleSize: number          // bytes (new TextEncoder().encode(json).byteLength)
  contributorIds: string[]    // keys of bundle.collections
  isSessionPristine: boolean  // session-start pre-op snapshot, never evicted
}

export interface StoredSnapshot extends SnapshotMetadata {
  bundle: SyncBundle
}

export interface CompactionResult {
  kept: string[]
  evicted: string[]
}
```

- [ ] **Step 2: Type-check compiles**

Run: `cd spa && npx tsc --noEmit 2>&1 | grep snapshot-types || echo OK`
Expected: `OK` (no TS errors introduced by this file)

- [ ] **Step 3: Commit**

```bash
git add spa/src/lib/sync/snapshot-types.ts
git commit -m "feat(sync): add snapshot metadata + stored types"
```

---

### Task 5: SnapshotStore — init + create + getLocal

**Files:**
- Create: `spa/src/lib/sync/snapshot-store.ts`
- Create: `spa/src/lib/sync/__tests__/snapshot-store.test.ts`

- [ ] **Step 1: Write failing test**

File: `spa/src/lib/sync/__tests__/snapshot-store.test.ts`

```typescript
import { describe, expect, it, beforeEach } from 'vitest'
import { createSnapshotStore } from '../snapshot-store'
import { closeAllIDB } from '../../storage/idb'
import type { SyncBundle } from '../types'

const bundle = (device = 'dev-a'): SyncBundle => ({
  version: 1,
  timestamp: Date.now(),
  device,
  collections: {
    workspaces: { version: 1, data: { list: [{ id: 'w1' }] } },
    hosts: { version: 1, data: { list: [] } },
  },
})

describe('SnapshotStore.createSnapshot + getLocal', () => {
  beforeEach(async () => {
    await closeAllIDB()
    indexedDB.deleteDatabase('purdex-sync-test')
  })

  it('stores a snapshot and retrieves it by id', async () => {
    const store = createSnapshotStore('purdex-sync-test')
    await store.init()

    const meta = await store.createSnapshot(bundle(), 'manual')

    expect(meta.id).toMatch(/^snap_/)
    expect(meta.trigger).toBe('manual')
    expect(meta.device).toBe('dev-a')
    expect(meta.contributorIds).toEqual(['workspaces', 'hosts'])
    expect(meta.bundleSize).toBeGreaterThan(0)
    expect(meta.isSessionPristine).toBe(false)

    const fetched = await store.getLocal(meta.id)
    expect(fetched).not.toBeNull()
    expect(fetched!.bundle.collections.workspaces).toBeDefined()
  })

  it('marks session-pristine when opts set', async () => {
    const store = createSnapshotStore('purdex-sync-test')
    await store.init()
    const meta = await store.createSnapshot(bundle(), 'pre-restore', { isSessionPristine: true })
    expect(meta.isSessionPristine).toBe(true)
  })
})
```

- [ ] **Step 2: Run → fail**

Run: `cd spa && npx vitest run src/lib/sync/__tests__/snapshot-store.test.ts`
Expected: FAIL `Cannot find module '../snapshot-store'`

- [ ] **Step 3: Implement snapshot-store.ts skeleton (init/create/get)**

File: `spa/src/lib/sync/snapshot-store.ts`

```typescript
import { openIDB } from '../storage/idb'
import type { SyncBundle } from './types'
import type { SnapshotMetadata, SnapshotTrigger, StoredSnapshot } from './snapshot-types'

const STORE = 'snapshots'
const DB_VERSION = 1

function genId(): string {
  const bytes = crypto.getRandomValues(new Uint8Array(8))
  const hex = Array.from(bytes).map((b) => b.toString(16).padStart(2, '0')).join('')
  return `snap_${hex}`
}

function computeBundleSize(bundle: SyncBundle): number {
  return new TextEncoder().encode(JSON.stringify(bundle)).byteLength
}

export interface SnapshotStore {
  init(): Promise<void>
  listLocal(): Promise<SnapshotMetadata[]>
  getLocal(id: string): Promise<StoredSnapshot | null>
  createSnapshot(
    bundle: SyncBundle,
    trigger: SnapshotTrigger,
    opts?: { isSessionPristine?: boolean },
  ): Promise<SnapshotMetadata>
  deleteLocal(id: string): Promise<void>
  compact(): Promise<{ kept: string[]; evicted: string[] }>
  clear(): Promise<void>
}

export function createSnapshotStore(dbName = 'purdex-sync'): SnapshotStore {
  const dbPromise = openIDB(dbName, DB_VERSION, (db) => {
    if (!db.objectStoreNames.contains(STORE)) {
      const os = db.createObjectStore(STORE, { keyPath: 'id' })
      os.createIndex('by-timestamp', 'timestamp')
    }
  })

  return {
    async init() {
      await dbPromise
    },

    async listLocal() {
      // Implemented in Task 6
      throw new Error('not implemented')
    },

    async getLocal(id) {
      const db = await dbPromise
      const row = await db.get(STORE, id)
      return (row as StoredSnapshot | undefined) ?? null
    },

    async createSnapshot(bundle, trigger, opts) {
      const db = await dbPromise
      const meta: SnapshotMetadata = {
        id: genId(),
        timestamp: Date.now(),
        device: bundle.device,
        trigger,
        bundleSize: computeBundleSize(bundle),
        contributorIds: Object.keys(bundle.collections),
        isSessionPristine: opts?.isSessionPristine ?? false,
      }
      const record: StoredSnapshot = { ...meta, bundle }
      await db.put(STORE, record)
      return meta
    },

    async deleteLocal(id) {
      const db = await dbPromise
      await db.delete(STORE, id)
    },

    async compact() {
      // Implemented in Task 8
      throw new Error('not implemented')
    },

    async clear() {
      const db = await dbPromise
      await db.clear(STORE)
    },
  }
}
```

- [ ] **Step 4: Run → pass**

Run: `cd spa && npx vitest run src/lib/sync/__tests__/snapshot-store.test.ts`
Expected: PASS (2/2)

- [ ] **Step 5: Commit**

```bash
git add spa/src/lib/sync/snapshot-store.ts spa/src/lib/sync/__tests__/snapshot-store.test.ts
git commit -m "feat(sync): SnapshotStore init + createSnapshot + getLocal"
```

---

### Task 6: SnapshotStore — listLocal (metadata only) + deleteLocal + clear

**Files:**
- Modify: `spa/src/lib/sync/snapshot-store.ts`
- Modify: `spa/src/lib/sync/__tests__/snapshot-store.test.ts`

- [ ] **Step 1: Append failing tests**

File: `spa/src/lib/sync/__tests__/snapshot-store.test.ts`（末尾追加 `describe`）

```typescript
describe('SnapshotStore.listLocal', () => {
  beforeEach(async () => {
    await closeAllIDB()
    indexedDB.deleteDatabase('purdex-sync-test')
  })

  it('returns metadata only (no bundle) sorted newest first', async () => {
    const store = createSnapshotStore('purdex-sync-test')
    await store.init()

    const a = await store.createSnapshot(bundle('a'), 'manual')
    await new Promise((r) => setTimeout(r, 5))
    const b = await store.createSnapshot(bundle('b'), 'manual')

    const list = await store.listLocal()
    expect(list).toHaveLength(2)
    expect(list[0].id).toBe(b.id)
    expect(list[1].id).toBe(a.id)
    // bundle 不應該在 metadata 裡
    expect((list[0] as Record<string, unknown>).bundle).toBeUndefined()
  })

  it('deleteLocal removes one', async () => {
    const store = createSnapshotStore('purdex-sync-test')
    await store.init()
    const m = await store.createSnapshot(bundle(), 'manual')
    await store.deleteLocal(m.id)
    expect(await store.getLocal(m.id)).toBeNull()
  })

  it('clear empties store', async () => {
    const store = createSnapshotStore('purdex-sync-test')
    await store.init()
    await store.createSnapshot(bundle(), 'manual')
    await store.createSnapshot(bundle(), 'manual')
    await store.clear()
    expect(await store.listLocal()).toHaveLength(0)
  })
})
```

- [ ] **Step 2: Run → fail**

Run: `cd spa && npx vitest run src/lib/sync/__tests__/snapshot-store.test.ts`
Expected: FAIL `not implemented` (from listLocal)

- [ ] **Step 3: Implement listLocal**

File: `spa/src/lib/sync/snapshot-store.ts`

Replace the `listLocal` placeholder with：

```typescript
async listLocal() {
  const db = await dbPromise
  const all = await db.getAll(STORE) as StoredSnapshot[]
  return all
    .map(({ bundle: _bundle, ...meta }) => meta as SnapshotMetadata)
    .sort((a, b) => b.timestamp - a.timestamp)
},
```

- [ ] **Step 4: Run → pass**

Run: `cd spa && npx vitest run src/lib/sync/__tests__/snapshot-store.test.ts`
Expected: PASS (5/5)

- [ ] **Step 5: Commit**

```bash
git add spa/src/lib/sync/snapshot-store.ts spa/src/lib/sync/__tests__/snapshot-store.test.ts
git commit -m "feat(sync): SnapshotStore listLocal (metadata-only) + delete + clear"
```

---

### Task 7: computeCompaction (tiered policy, pure function)

**Files:**
- Create: `spa/src/lib/sync/snapshot-compaction.ts`
- Create: `spa/src/lib/sync/__tests__/snapshot-compaction.test.ts`

- [ ] **Step 1: Write failing tests**

File: `spa/src/lib/sync/__tests__/snapshot-compaction.test.ts`

```typescript
import { describe, expect, it } from 'vitest'
import { computeCompaction } from '../snapshot-compaction'
import type { SnapshotMetadata } from '../snapshot-types'

function meta(
  id: string,
  trigger: SnapshotMetadata['trigger'],
  offsetMs: number,
  extra: Partial<SnapshotMetadata> = {},
): SnapshotMetadata {
  return {
    id,
    timestamp: Date.now() - offsetMs,
    device: 'dev',
    trigger,
    bundleSize: 1000,
    contributorIds: [],
    isSessionPristine: false,
    ...extra,
  }
}

describe('computeCompaction', () => {
  it('returns empty result for empty input', () => {
    expect(computeCompaction([], Date.now())).toEqual({ kept: [], evicted: [] })
  })

  it('hourly bucket: keeps newest per UTC hour', () => {
    const now = Date.now()
    const HOUR = 60 * 60 * 1000
    // same hour, older + newer
    const a = meta('a', 'manual', HOUR + 1_000)
    const b = meta('b', 'manual', HOUR + 500)
    const result = computeCompaction([a, b], now)
    expect(result.kept).toContain('b')
    expect(result.evicted).toContain('a')
  })

  it('pre-op pool: max 5 (LRU evict) but pristine never evicted', () => {
    const now = Date.now()
    const items: SnapshotMetadata[] = []
    // pristine is oldest
    items.push(meta('pristine', 'pre-restore', 1_000_000, { isSessionPristine: true }))
    // 6 regular pre-op, oldest first
    for (let i = 0; i < 6; i++) {
      items.push(meta(`p${i}`, 'pre-import', 900_000 - i * 10_000))
    }

    const result = computeCompaction(items, now)
    expect(result.kept).toContain('pristine')
    // newest 5 of the 6 regular pre-op kept, oldest evicted
    expect(result.evicted).toEqual(['p0'])
    expect(result.kept).toHaveLength(6) // pristine + 5 pre-op
  })

  it('pre-op pool and time-tier do not interfere', () => {
    const now = Date.now()
    const HOUR = 60 * 60 * 1000
    const items: SnapshotMetadata[] = [
      meta('manual-a', 'manual', 0),
      meta('preop-a', 'pre-import', 100),
      meta('preop-b', 'pre-restore', 200),
    ]
    const result = computeCompaction(items, now)
    // 都在不同池，都該留
    expect(result.evicted).toEqual([])
  })

  it('daily tier: 1-30 days, one per UTC day', () => {
    const now = Date.now()
    const DAY = 24 * 60 * 60 * 1000
    const dayAgo2 = meta('d2a', 'manual', 2 * DAY + 1000)
    const dayAgo2Newer = meta('d2b', 'manual', 2 * DAY - 1000)
    const result = computeCompaction([dayAgo2, dayAgo2Newer], now)
    expect(result.kept).toContain('d2b')
    expect(result.evicted).toContain('d2a')
  })
})
```

- [ ] **Step 2: Run → fail**

Run: `cd spa && npx vitest run src/lib/sync/__tests__/snapshot-compaction.test.ts`
Expected: FAIL `Cannot find module '../snapshot-compaction'`

- [ ] **Step 3: Implement computeCompaction**

File: `spa/src/lib/sync/snapshot-compaction.ts`

```typescript
import type { SnapshotMetadata, CompactionResult } from './snapshot-types'

const HOUR_MS = 60 * 60 * 1000
const DAY_MS = 24 * HOUR_MS

const PRE_OP_MAX = 5

function isPreOp(m: SnapshotMetadata): boolean {
  return m.trigger === 'pre-import' || m.trigger === 'pre-restore'
}

function pad(n: number, w = 2): string {
  return String(n).padStart(w, '0')
}

/** YYYY-MM-DDTHHZ */
function hourKeyUTC(ts: number): string {
  const d = new Date(ts)
  return `${d.getUTCFullYear()}-${pad(d.getUTCMonth() + 1)}-${pad(d.getUTCDate())}T${pad(d.getUTCHours())}Z`
}

/** YYYY-MM-DDZ */
function dayKeyUTC(ts: number): string {
  const d = new Date(ts)
  return `${d.getUTCFullYear()}-${pad(d.getUTCMonth() + 1)}-${pad(d.getUTCDate())}Z`
}

/** ISO week YYYY-Www */
function isoWeekKey(ts: number): string {
  const d = new Date(ts)
  const target = new Date(Date.UTC(d.getUTCFullYear(), d.getUTCMonth(), d.getUTCDate()))
  const dayNum = (target.getUTCDay() + 6) % 7
  target.setUTCDate(target.getUTCDate() - dayNum + 3)
  const firstThursday = new Date(Date.UTC(target.getUTCFullYear(), 0, 4))
  const diff = target.getTime() - firstThursday.getTime()
  const week = 1 + Math.round(diff / (7 * DAY_MS))
  return `${target.getUTCFullYear()}-W${pad(week)}`
}

/** YYYY-MM */
function monthKey(ts: number): string {
  const d = new Date(ts)
  return `${d.getUTCFullYear()}-${pad(d.getUTCMonth() + 1)}`
}

function classifyTier(ageMs: number):
  | { tier: 'hourly'; key: (ts: number) => string }
  | { tier: 'daily'; key: (ts: number) => string }
  | { tier: 'weekly'; key: (ts: number) => string }
  | { tier: 'monthly'; key: (ts: number) => string } {
  if (ageMs < 24 * HOUR_MS) return { tier: 'hourly', key: hourKeyUTC }
  if (ageMs < 30 * DAY_MS) return { tier: 'daily', key: dayKeyUTC }
  if (ageMs < 90 * DAY_MS) return { tier: 'weekly', key: isoWeekKey }
  return { tier: 'monthly', key: monthKey }
}

export function computeCompaction(all: SnapshotMetadata[], now: number): CompactionResult {
  const kept: string[] = []
  const evicted: string[] = []

  const preOp: SnapshotMetadata[] = []
  const timeTier: SnapshotMetadata[] = []

  for (const m of all) {
    if (isPreOp(m)) preOp.push(m)
    else timeTier.push(m)
  }

  // --- pre-op pool: pristine 先 bypass，其餘按時間 desc 留 max 5 ---
  const pristine = preOp.filter((m) => m.isSessionPristine)
  const regularPreOp = preOp
    .filter((m) => !m.isSessionPristine)
    .sort((a, b) => b.timestamp - a.timestamp)

  for (const m of pristine) kept.push(m.id)
  for (let i = 0; i < regularPreOp.length; i++) {
    if (i < PRE_OP_MAX) kept.push(regularPreOp[i].id)
    else evicted.push(regularPreOp[i].id)
  }

  // --- time-tier: 分桶、每桶留 newest ---
  const buckets = new Map<string, SnapshotMetadata[]>()
  for (const m of timeTier) {
    const age = now - m.timestamp
    const cls = classifyTier(age)
    const bucketKey = `${cls.tier}:${cls.key(m.timestamp)}`
    const arr = buckets.get(bucketKey) ?? []
    arr.push(m)
    buckets.set(bucketKey, arr)
  }

  for (const arr of buckets.values()) {
    arr.sort((a, b) => b.timestamp - a.timestamp)
    kept.push(arr[0].id)
    for (let i = 1; i < arr.length; i++) evicted.push(arr[i].id)
  }

  return { kept, evicted }
}
```

- [ ] **Step 4: Run → pass**

Run: `cd spa && npx vitest run src/lib/sync/__tests__/snapshot-compaction.test.ts`
Expected: PASS (5/5)

- [ ] **Step 5: Commit**

```bash
git add spa/src/lib/sync/snapshot-compaction.ts spa/src/lib/sync/__tests__/snapshot-compaction.test.ts
git commit -m "feat(sync): tiered snapshot compaction pure function"
```

---

### Task 8: Wire compact() into SnapshotStore + auto-compact on create

**Files:**
- Modify: `spa/src/lib/sync/snapshot-store.ts`
- Modify: `spa/src/lib/sync/__tests__/snapshot-store.test.ts`

- [ ] **Step 1: Write failing test**

File: `spa/src/lib/sync/__tests__/snapshot-store.test.ts`（末尾追加）

```typescript
describe('SnapshotStore.compact + auto-compact', () => {
  beforeEach(async () => {
    await closeAllIDB()
    indexedDB.deleteDatabase('purdex-sync-test')
  })

  it('compact() removes snapshots that fail policy and returns kept/evicted ids', async () => {
    const store = createSnapshotStore('purdex-sync-test')
    await store.init()

    // 6 pre-import → 5 keep, 1 evict (oldest)
    const ids: string[] = []
    for (let i = 0; i < 6; i++) {
      const m = await store.createSnapshot(bundle(), 'pre-import')
      ids.push(m.id)
      // stagger timestamps — IDB resolution is ms, sleep briefly
      await new Promise((r) => setTimeout(r, 2))
    }

    const result = await store.compact()
    expect(result.kept).toHaveLength(5)
    expect(result.evicted).toHaveLength(1)
    // 最老的被擠掉
    expect(result.evicted[0]).toBe(ids[0])

    const list = await store.listLocal()
    expect(list).toHaveLength(5)
  })

  it('createSnapshot 後自動 compact', async () => {
    const store = createSnapshotStore('purdex-sync-test')
    await store.init()
    for (let i = 0; i < 7; i++) {
      await store.createSnapshot(bundle(), 'pre-import')
      await new Promise((r) => setTimeout(r, 2))
    }
    const list = await store.listLocal()
    expect(list.length).toBeLessThanOrEqual(5)
  })
})
```

- [ ] **Step 2: Run → fail**

Run: `cd spa && npx vitest run src/lib/sync/__tests__/snapshot-store.test.ts`
Expected: FAIL `not implemented`

- [ ] **Step 3: Implement compact + hook into create**

File: `spa/src/lib/sync/snapshot-store.ts`

Imports（頂部加入）：

```typescript
import { computeCompaction } from './snapshot-compaction'
```

替換 `compact` 與 `createSnapshot`：

```typescript
async compact() {
  const db = await dbPromise
  const all = await db.getAll(STORE) as StoredSnapshot[]
  const metas: SnapshotMetadata[] = all.map(({ bundle: _bundle, ...m }) => m as SnapshotMetadata)
  const { kept, evicted } = computeCompaction(metas, Date.now())

  const tx = db.transaction(STORE, 'readwrite')
  await Promise.all(evicted.map((id) => tx.store.delete(id)))
  await tx.done
  return { kept, evicted }
},

async createSnapshot(bundle, trigger, opts) {
  const db = await dbPromise
  const meta: SnapshotMetadata = {
    id: genId(),
    timestamp: Date.now(),
    device: bundle.device,
    trigger,
    bundleSize: computeBundleSize(bundle),
    contributorIds: Object.keys(bundle.collections),
    isSessionPristine: opts?.isSessionPristine ?? false,
  }
  const record: StoredSnapshot = { ...meta, bundle }
  await db.put(STORE, record)
  // auto-compact (serialized inside IDB tx naturally; Promise queue not
  // strictly required here because createSnapshot is the only writer path)
  await this.compact!()
  return meta
},
```

但 `this.compact` 綁定寫法不保證在 factory return object 內，改寫成 closure：

把 `createSnapshotStore` 重構，先宣告 `compactFn`：

```typescript
export function createSnapshotStore(dbName = 'purdex-sync'): SnapshotStore {
  const dbPromise = openIDB(dbName, DB_VERSION, (db) => {
    if (!db.objectStoreNames.contains(STORE)) {
      const os = db.createObjectStore(STORE, { keyPath: 'id' })
      os.createIndex('by-timestamp', 'timestamp')
    }
  })

  async function compactFn(): Promise<{ kept: string[]; evicted: string[] }> {
    const db = await dbPromise
    const all = await db.getAll(STORE) as StoredSnapshot[]
    const metas: SnapshotMetadata[] = all.map(({ bundle: _bundle, ...m }) => m as SnapshotMetadata)
    const { kept, evicted } = computeCompaction(metas, Date.now())
    const tx = db.transaction(STORE, 'readwrite')
    await Promise.all(evicted.map((id) => tx.store.delete(id)))
    await tx.done
    return { kept, evicted }
  }

  return {
    async init() { await dbPromise },

    async listLocal() {
      const db = await dbPromise
      const all = await db.getAll(STORE) as StoredSnapshot[]
      return all
        .map(({ bundle: _bundle, ...meta }) => meta as SnapshotMetadata)
        .sort((a, b) => b.timestamp - a.timestamp)
    },

    async getLocal(id) {
      const db = await dbPromise
      const row = await db.get(STORE, id)
      return (row as StoredSnapshot | undefined) ?? null
    },

    async createSnapshot(bundle, trigger, opts) {
      const db = await dbPromise
      const meta: SnapshotMetadata = {
        id: genId(),
        timestamp: Date.now(),
        device: bundle.device,
        trigger,
        bundleSize: computeBundleSize(bundle),
        contributorIds: Object.keys(bundle.collections),
        isSessionPristine: opts?.isSessionPristine ?? false,
      }
      const record: StoredSnapshot = { ...meta, bundle }
      await db.put(STORE, record)
      await compactFn()
      return meta
    },

    async deleteLocal(id) {
      const db = await dbPromise
      await db.delete(STORE, id)
    },

    async compact() {
      return compactFn()
    },

    async clear() {
      const db = await dbPromise
      await db.clear(STORE)
    },
  }
}
```

- [ ] **Step 4: Run → pass**

Run: `cd spa && npx vitest run src/lib/sync/__tests__/snapshot-store.test.ts`
Expected: PASS (7/7)

- [ ] **Step 5: Commit**

```bash
git add spa/src/lib/sync/snapshot-store.ts spa/src/lib/sync/__tests__/snapshot-store.test.ts
git commit -m "feat(sync): SnapshotStore compact + auto-compact on create"
```

---

### Task 9: Snapshot diff (contributor-level deepEqual)

**Files:**
- Create: `spa/src/lib/sync/snapshot-diff.ts`
- Create: `spa/src/lib/sync/__tests__/snapshot-diff.test.ts`

- [ ] **Step 1: Write failing tests**

File: `spa/src/lib/sync/__tests__/snapshot-diff.test.ts`

```typescript
import { describe, expect, it } from 'vitest'
import { computeSnapshotDiff, equalExceptEnvelope } from '../snapshot-diff'
import type { SyncBundle } from '../types'

function bundle(collections: SyncBundle['collections'], device = 'd1'): SyncBundle {
  return { version: 1, timestamp: 0, device, collections }
}

describe('equalExceptEnvelope', () => {
  it('ignores version / timestamp / device', () => {
    const a = bundle({ w: { version: 1, data: { x: 1 } } }, 'd1')
    const b: SyncBundle = { ...a, timestamp: 999, device: 'd2' }
    expect(equalExceptEnvelope(a, b)).toBe(true)
  })

  it('returns false when a collection differs', () => {
    const a = bundle({ w: { version: 1, data: { x: 1 } } })
    const b = bundle({ w: { version: 1, data: { x: 2 } } })
    expect(equalExceptEnvelope(a, b)).toBe(false)
  })
})

describe('computeSnapshotDiff', () => {
  it('identical collections → all identical', () => {
    const a = bundle({
      w: { version: 1, data: { x: 1 } },
      h: { version: 1, data: {} },
    })
    const result = computeSnapshotDiff(a, a)
    expect(result).toHaveLength(2)
    expect(result.every((r) => r.status === 'identical')).toBe(true)
  })

  it('detects changed / missing-in-snapshot / missing-in-current', () => {
    const snap = bundle({
      w: { version: 1, data: { x: 1 } },            // changed
      h: { version: 1, data: { y: 2 } },            // identical
      onlyInSnap: { version: 1, data: {} },         // missing-in-current
    })
    const curr = bundle({
      w: { version: 1, data: { x: 2 } },
      h: { version: 1, data: { y: 2 } },
      onlyInCurr: { version: 1, data: {} },         // missing-in-snapshot
    })
    const result = computeSnapshotDiff(snap, curr)
    expect(result.find((r) => r.id === 'w')?.status).toBe('changed')
    expect(result.find((r) => r.id === 'h')?.status).toBe('identical')
    expect(result.find((r) => r.id === 'onlyInSnap')?.status).toBe('missing-in-current')
    expect(result.find((r) => r.id === 'onlyInCurr')?.status).toBe('missing-in-snapshot')
  })
})
```

- [ ] **Step 2: Run → fail**

Run: `cd spa && npx vitest run src/lib/sync/__tests__/snapshot-diff.test.ts`
Expected: FAIL `Cannot find module '../snapshot-diff'`

- [ ] **Step 3: Implement**

File: `spa/src/lib/sync/snapshot-diff.ts`

```typescript
import { deepEqual } from './three-way-merge'
import type { SyncBundle } from './types'

export interface ContributorDiff {
  id: string
  status: 'identical' | 'changed' | 'missing-in-snapshot' | 'missing-in-current'
}

/**
 * Deep compare two bundles, ignoring envelope fields (version, timestamp,
 * device). Used for dedup on createSnapshot.
 */
export function equalExceptEnvelope(a: SyncBundle, b: SyncBundle): boolean {
  return deepEqual(a.collections, b.collections)
}

/**
 * Per-contributor diff: for each contributor id present in either bundle,
 * return identical | changed | missing-in-snapshot | missing-in-current.
 * Used by SnapshotDetail to show a diff summary.
 */
export function computeSnapshotDiff(
  snapshot: SyncBundle,
  current: SyncBundle,
): ContributorDiff[] {
  const ids = new Set<string>([
    ...Object.keys(snapshot.collections),
    ...Object.keys(current.collections),
  ])
  return Array.from(ids).map((id) => {
    const s = snapshot.collections[id]
    const c = current.collections[id]
    if (s === undefined) return { id, status: 'missing-in-snapshot' as const }
    if (c === undefined) return { id, status: 'missing-in-current' as const }
    return { id, status: deepEqual(s, c) ? ('identical' as const) : ('changed' as const) }
  })
}
```

- [ ] **Step 4: Run → pass**

Run: `cd spa && npx vitest run src/lib/sync/__tests__/snapshot-diff.test.ts`
Expected: PASS (4/4)

- [ ] **Step 5: Commit**

```bash
git add spa/src/lib/sync/snapshot-diff.ts spa/src/lib/sync/__tests__/snapshot-diff.test.ts
git commit -m "feat(sync): contributor-level snapshot diff + equalExceptEnvelope"
```

---

### Task 10: Hosts contributor — preserve token on deserialize

**Files:**
- Modify: `spa/src/lib/sync/contributors/hosts.ts`
- Modify: `spa/src/lib/sync/contributors/hosts.test.ts`

- [ ] **Step 1: Write failing test**

File: `spa/src/lib/sync/contributors/hosts.test.ts`（在既有 describe 尾端加）

```typescript
describe('hostsContributor.deserialize (full-replace, token preservation)', () => {
  it('preserves token when host id exists in current state', () => {
    useHostStore.setState({
      hosts: {
        h1: { id: 'h1', name: 'A', url: 'u1', token: 'SECRET-A', ...} as never,
      },
      hostOrder: ['h1'],
      activeHostId: 'h1',
    })

    const contributor = createHostsContributor()
    contributor.deserialize(
      {
        version: 1,
        data: {
          hosts: { h1: { id: 'h1', name: 'renamed', url: 'u1' } },
          hostOrder: ['h1'],
          activeHostId: 'h1',
        },
      },
      { type: 'full-replace' },
    )

    const s = useHostStore.getState()
    expect(s.hosts.h1.name).toBe('renamed')
    expect((s.hosts.h1 as unknown as { token?: string }).token).toBe('SECRET-A')
  })

  it('sets token=null for hosts that only exist in snapshot (not locally)', () => {
    useHostStore.setState({ hosts: {}, hostOrder: [], activeHostId: null })

    const contributor = createHostsContributor()
    contributor.deserialize(
      {
        version: 1,
        data: {
          hosts: { hNew: { id: 'hNew', name: 'new', url: 'uN' } },
          hostOrder: ['hNew'],
          activeHostId: null,
        },
      },
      { type: 'full-replace' },
    )

    const s = useHostStore.getState()
    expect((s.hosts.hNew as unknown as { token?: unknown }).token).toBeNull()
  })
})
```

Note: `... as never` only for type acquiescence; actual HostConfig shape should be used with the real fields from useHostStore definition. Adapt to actual HostConfig interface.

- [ ] **Step 2: Adapt the test to real HostConfig shape**

Run: `cd spa && grep -r "interface HostConfig" --include="*.ts" | head -3`
Inspect the real shape and update the test object literal to match every required field.

- [ ] **Step 3: Run → fail**

Run: `cd spa && npx vitest run src/lib/sync/contributors/hosts.test.ts`
Expected: FAIL — token is undefined / overwritten

- [ ] **Step 4: Modify deserialize to preserve token**

File: `spa/src/lib/sync/contributors/hosts.ts`

替換 `deserialize` 的 `full-replace` 分支：

```typescript
if (merge.type === 'full-replace') {
  const current = useHostStore.getState().hosts
  const incomingHosts = (incoming.hosts ?? {}) as Record<string, Record<string, unknown>>
  const mergedHosts: Record<string, Record<string, unknown>> = {}
  for (const [id, host] of Object.entries(incomingHosts)) {
    const currentToken = (current as Record<string, { token?: unknown } | undefined>)[id]?.token
    mergedHosts[id] = {
      ...host,
      token: currentToken ?? null,
    }
  }
  useHostStore.setState({
    hosts: mergedHosts as ReturnType<typeof useHostStore.getState>['hosts'],
    hostOrder: incoming.hostOrder as string[],
    activeHostId: incoming.activeHostId as string | null,
  })
  return
}
```

- [ ] **Step 5: Run → pass**

Run: `cd spa && npx vitest run src/lib/sync/contributors/hosts.test.ts`
Expected: PASS (all tests, including existing ones)

- [ ] **Step 6: Commit**

```bash
git add spa/src/lib/sync/contributors/hosts.ts spa/src/lib/sync/contributors/hosts.test.ts
git commit -m "fix(sync): preserve host tokens during full-replace deserialize"
```

---

### Task 11: SnapshotStore singleton + useSyncStore actions (createPreOperationSnapshot, restoreFromSnapshot)

**Files:**
- Create: `spa/src/lib/sync/snapshot-store-instance.ts`
- Modify: `spa/src/lib/sync/use-sync-store.ts`
- Modify: `spa/src/lib/sync/use-sync-store.test.ts`

- [ ] **Step 1: Create singleton accessor**

File: `spa/src/lib/sync/snapshot-store-instance.ts`

```typescript
import { createSnapshotStore, type SnapshotStore } from './snapshot-store'

let instance: SnapshotStore | null = null

/** Singleton accessor; tests can override via setSnapshotStore(mock). */
export function getSnapshotStore(): SnapshotStore {
  if (!instance) instance = createSnapshotStore('purdex-sync')
  return instance
}

/** @internal for tests only */
export function setSnapshotStore(store: SnapshotStore | null): void {
  instance = store
}
```

- [ ] **Step 2: Write failing test for useSyncStore actions**

File: `spa/src/lib/sync/use-sync-store.test.ts`（末尾追加）

```typescript
describe('useSyncStore.createPreOperationSnapshot', () => {
  beforeEach(() => {
    useSyncStore.getState().reset()
  })

  it('delegates to SnapshotStore and returns id', async () => {
    const calls: Array<{ trigger: string; opts: unknown }> = []
    setSnapshotStore({
      init: async () => {},
      listLocal: async () => [],
      getLocal: async () => null,
      createSnapshot: async (_b, trigger, opts) => {
        calls.push({ trigger, opts })
        return {
          id: 'stub-id',
          timestamp: 0,
          device: 'd',
          trigger,
          bundleSize: 0,
          contributorIds: [],
          isSessionPristine: opts?.isSessionPristine ?? false,
        }
      },
      deleteLocal: async () => {},
      compact: async () => ({ kept: [], evicted: [] }),
      clear: async () => {},
    })

    const id = await useSyncStore.getState().createPreOperationSnapshot('pre-import')
    expect(id).toBe('stub-id')
    expect(calls[0].trigger).toBe('pre-import')
  })
})

describe('useSyncStore.restoreFromSnapshot', () => {
  beforeEach(() => {
    useSyncStore.getState().reset()
  })

  it('creates pre-restore, clears pendingConflicts, calls contributor deserialize, does NOT touch lastSyncedBundle', async () => {
    const deserializeCalls: string[] = []
    const preOpCalls: string[] = []

    setSnapshotStore({
      init: async () => {},
      listLocal: async () => [],
      getLocal: async () => null,
      createSnapshot: async (_b, trigger) => {
        preOpCalls.push(trigger)
        return {
          id: 'pre-' + trigger,
          timestamp: 0,
          device: 'd',
          trigger,
          bundleSize: 0,
          contributorIds: [],
          isSessionPristine: false,
        }
      },
      deleteLocal: async () => {},
      compact: async () => ({ kept: [], evicted: [] }),
      clear: async () => {},
    })

    // Stub engine + contributor registry via register-sync
    const stubBundle: SyncBundle = {
      version: 1,
      timestamp: 0,
      device: 'snap-dev',
      collections: {
        stub1: { version: 1, data: { x: 1 } },
      },
    }

    // inject a stub engine via __setEngineForTests (exported from register-sync)
    const { __setEngineForTests, syncEngine } = await import('./register-sync')
    const originalContribs = syncEngine.getContributors()
    __setEngineForTests({
      register: () => {},
      getContributors: () => [
        {
          id: 'stub1',
          strategy: 'full',
          getVersion: () => 1,
          serialize: () => ({ version: 1, data: {} }),
          deserialize: (_p, merge) => {
            if (merge.type === 'full-replace') deserializeCalls.push('stub1')
          },
        },
      ],
      serialize: () => stubBundle,
      push: async () => stubBundle,
      pull: async () => ({ appliedBundle: null, conflicts: [] }),
    } as never)

    useSyncStore.setState({
      pendingConflicts: [{ contributor: 'x', field: 'y', lastSynced: null, local: null, remote: { value: null, device: 'r' } }],
      pendingRemoteBundle: stubBundle,
      pendingConflictsAt: Date.now(),
      lastSyncedBundle: stubBundle,
      lastSyncedAt: 12345,
    })

    await useSyncStore.getState().restoreFromSnapshot(
      { ...stubBundle, bundle: stubBundle, id: 'target', trigger: 'manual', bundleSize: 0, contributorIds: ['stub1'], isSessionPristine: false },
      'local',
    )

    expect(preOpCalls).toContain('pre-restore')
    expect(deserializeCalls).toContain('stub1')

    const state = useSyncStore.getState()
    expect(state.pendingConflicts).toEqual([])
    expect(state.pendingRemoteBundle).toBeNull()
    // B1 append-only: lastSyncedBundle 不被動
    expect(state.lastSyncedBundle).toEqual(stubBundle)
    expect(state.lastSyncedAt).toBe(12345)

    // restore the original engine to avoid cross-test pollution
    __setEngineForTests({ ...syncEngine, getContributors: () => originalContribs } as never)
  })
})
```

- [ ] **Step 3: Run → fail**

Run: `cd spa && npx vitest run src/lib/sync/use-sync-store.test.ts`
Expected: FAIL — `createPreOperationSnapshot / restoreFromSnapshot / __setEngineForTests` 不存在

- [ ] **Step 4: Add engine test hook**

File: `spa/src/lib/sync/register-sync.ts`（找既有 `export const syncEngine`）

在該檔尾端加入：

```typescript
// Test-only: override the engine instance so use-sync-store tests can
// inject a minimal contributor set without pulling in real stores.
let _engineForTests: typeof syncEngine | null = null

/** @internal tests only */
export function __setEngineForTests(e: typeof syncEngine | null): void {
  _engineForTests = e
}

/** @internal tests only */
export function __getActiveEngine(): typeof syncEngine {
  return _engineForTests ?? syncEngine
}
```

- [ ] **Step 5: Add actions to useSyncStore**

File: `spa/src/lib/sync/use-sync-store.ts`

Imports 追加：

```typescript
import type { StoredSnapshot } from './snapshot-types'
import { getSnapshotStore } from './snapshot-store-instance'
import { __getActiveEngine } from './register-sync'
```

在 `SyncStoreState` interface 的 Actions 區塊加：

```typescript
createPreOperationSnapshot: (trigger: 'pre-import' | 'pre-restore') => Promise<string>
restoreFromSnapshot: (snapshot: StoredSnapshot, source: 'local' | 'remote') => Promise<void>
```

在 `create<SyncStoreState>()(...)` 的 actions 物件（`reset` 之後）追加：

```typescript
createPreOperationSnapshot: async (trigger) => {
  const engine = __getActiveEngine()
  const device = get().clientId ?? 'unknown'
  const enabled = get().enabledModules.length > 0
    ? get().enabledModules
    : engine.getContributors().map((c) => c.id)
  const currentBundle = engine.serialize(device, enabled)
  const meta = await getSnapshotStore().createSnapshot(currentBundle, trigger)
  return meta.id
},

restoreFromSnapshot: async (snapshot, _source) => {
  // 1. pre-restore safety net
  await get().createPreOperationSnapshot('pre-restore')
  // 2. clear P0 pending state
  set({
    pendingConflicts: [],
    pendingRemoteBundle: null,
    pendingConflictsAt: null,
  })
  // 3. full-replace deserialize per contributor present in snapshot.bundle
  const engine = __getActiveEngine()
  const contribs = new Map(engine.getContributors().map((c) => [c.id, c]))
  for (const [id, payload] of Object.entries(snapshot.bundle.collections)) {
    const c = contribs.get(id)
    if (!c) continue
    try {
      c.deserialize(payload, { type: 'full-replace' })
    } catch (e) {
      console.error(`restore: ${id} deserialize failed`, e)
    }
  }
  // 4. B1 append-only: do NOT touch lastSyncedBundle
},
```

- [ ] **Step 6: Run → pass**

Run: `cd spa && npx vitest run src/lib/sync/use-sync-store.test.ts`
Expected: PASS (所有 — 新 + 既有)

- [ ] **Step 7: Commit**

```bash
git add spa/src/lib/sync/snapshot-store-instance.ts spa/src/lib/sync/use-sync-store.ts spa/src/lib/sync/use-sync-store.test.ts spa/src/lib/sync/register-sync.ts
git commit -m "feat(sync): useSyncStore.createPreOperationSnapshot + restoreFromSnapshot (append-only)"
```

---

### Task 12: Route parser — accept `/settings/<section>/<subsection>`

**Files:**
- Modify: `spa/src/lib/route-utils.ts`
- Modify: `spa/src/lib/route-utils.test.ts` (若不存在則 create)

- [ ] **Step 1: Write failing test**

File: `spa/src/lib/route-utils.test.ts`（append；檢查是否存在再決定 create）

```typescript
import { describe, expect, it } from 'vitest'
import { parseRoute } from './route-utils'

describe('parseRoute settings subsection', () => {
  it('recognises /settings/sync/history as section=sync, subsection=history', () => {
    const r = parseRoute('/settings/sync/history')
    expect(r).toEqual({ kind: 'settings', scope: 'global', section: 'sync', subsection: 'history' })
  })

  it('still parses /settings/sync as section only', () => {
    const r = parseRoute('/settings/sync')
    expect(r).toEqual({ kind: 'settings', scope: 'global', section: 'sync' })
  })

  it('rejects 3-level /settings/a/b/c', () => {
    const r = parseRoute('/settings/a/b/c')
    expect(r).toEqual({ kind: 'settings', scope: 'global' })
  })
})
```

- [ ] **Step 2: Run → fail**

Run: `cd spa && npx vitest run src/lib/route-utils.test.ts`
Expected: FAIL

- [ ] **Step 3: Update route-utils**

File: `spa/src/lib/route-utils.ts`

修改 `ParsedRoute` type 和 `parseRoute`：

```typescript
export type ParsedRoute =
  | { kind: 'history' }
  | { kind: 'hosts' }
  | { kind: 'settings'; scope: 'global'; section?: string; subsection?: string }
  | { kind: 'session-tab'; tabId: string; mode: 'terminal' | 'stream' }
  | { kind: 'workspace'; workspaceId: string }
  | { kind: 'workspace-settings'; workspaceId: string }
  | { kind: 'workspace-session-tab'; workspaceId: string; tabId: string; mode: 'terminal' | 'stream' }
```

將 `SETTINGS_SECTION_PATTERN` 留原樣，改加一個 `SETTINGS_SUBSECTION_PATTERN`：

```typescript
const SETTINGS_SECTION_PATTERN = /^[a-z0-9-]{1,32}$/
const SETTINGS_SUBSECTION_PATTERN = /^[a-z0-9-]{1,32}$/
```

把 `parseRoute` 處理 `/settings/` 的分支改成：

```typescript
if (path.startsWith('/settings/')) {
  const rest = path.slice('/settings/'.length)
  const parts = rest.split('/')
  if (parts.length === 1 && SETTINGS_SECTION_PATTERN.test(parts[0])) {
    return { kind: 'settings', scope: 'global', section: parts[0] }
  }
  if (
    parts.length === 2 &&
    SETTINGS_SECTION_PATTERN.test(parts[0]) &&
    SETTINGS_SUBSECTION_PATTERN.test(parts[1])
  ) {
    return { kind: 'settings', scope: 'global', section: parts[0], subsection: parts[1] }
  }
  return { kind: 'settings', scope: 'global' }
}
```

- [ ] **Step 4: Run → pass**

Run: `cd spa && npx vitest run src/lib/route-utils.test.ts`
Expected: PASS (all)

- [ ] **Step 5: Commit**

```bash
git add spa/src/lib/route-utils.ts spa/src/lib/route-utils.test.ts
git commit -m "feat(route): parse /settings/<section>/<subsection> two-level path"
```

---

### Task 13: SettingsPage — subsection self-heal + dispatch

**Files:**
- Modify: `spa/src/components/SettingsPage.tsx`
- Modify: `spa/src/components/SettingsPage.test.tsx`

- [ ] **Step 1: Write failing test**

檢視既有 `SettingsPage.test.tsx` 找到 wouter memory-location 的用法（P0 測試模式已建立），複製 pattern 並加：

```typescript
describe('SettingsPage subsection', () => {
  beforeEach(() => {
    resetLastSection()
  })

  it('renders /settings/sync/history without self-heal (valid subsection)', async () => {
    // Use existing memoryLocation pattern from P0's settings.test
    const { hook } = memoryLocation({ path: '/settings/sync/history', record: true })
    render(
      <Router hook={hook}>
        <SettingsPage {...mockPaneRendererProps({ kind: 'settings', scope: 'global' })} />
      </Router>,
    )
    // subsection 'history' is accepted (no URL change)
    await waitFor(() => {
      expect(window.location.pathname).not.toBe('/settings/sync')
    })
  })

  it('self-heals /settings/sync/extra/level3 back to /settings/sync', async () => {
    const { hook, history } = memoryLocation({ path: '/settings/sync/extra/level3', record: true })
    render(
      <Router hook={hook}>
        <SettingsPage {...mockPaneRendererProps({ kind: 'settings', scope: 'global' })} />
      </Router>,
    )
    await waitFor(() => {
      expect(history[history.length - 1]).toBe('/settings/sync')
    })
  })
})
```

若 `mockPaneRendererProps` helper 尚未存在，實作：
```typescript
function mockPaneRendererProps(content: PaneContent): PaneRendererProps {
  return { pane: { id: 'p', content } as Pane, isActive: true, workspaceId: null }
}
```

- [ ] **Step 2: Run → fail**

Run: `cd spa && npx vitest run src/components/SettingsPage.test.tsx`
Expected: FAIL — URL 被 self-heal 成 `/settings/sync`

- [ ] **Step 3: Update SettingsPage subsection logic**

File: `spa/src/components/SettingsPage.tsx`

取代 `urlSection` 解析段：

```typescript
const pathAfterSettings = location.startsWith('/settings/')
  ? location.slice('/settings/'.length)
  : null
const parts = pathAfterSettings ? pathAfterSettings.split('/') : []
const urlSection = parts[0] || null
const urlSubsection = parts[1] || null
```

修改 self-heal effect：

```typescript
useEffect(() => {
  if (!urlSection) return
  const sectionValid = sections.some((s) => s.id === urlSection)
  if (!sectionValid) {
    setLocation(`/settings/${activeSection}`, { replace: true })
    return
  }
  // subsection 合法性由 section component 自己處理（或本 effect 放寬）
  if (parts.length > 2) {
    setLocation(`/settings/${urlSection}`, { replace: true })
  }
// eslint-disable-next-line react-hooks/exhaustive-deps
}, [urlSection, urlSubsection, activeSection])
```

把 `urlSubsection` 透過 context 或 prop 傳給 active component。最簡：用一個 React context。

新增：

```typescript
import { createContext, useContext } from 'react'

interface SettingsRouteCtx {
  subsection: string | null
  setSubsection: (sub: string | null) => void
}
const SettingsRouteContext = createContext<SettingsRouteCtx>({ subsection: null, setSubsection: () => {} })
export function useSettingsRoute() { return useContext(SettingsRouteContext) }
```

在 `GlobalSettingsPage` return 包一層 Provider：

```typescript
return (
  <SettingsRouteContext.Provider value={{
    subsection: urlSubsection,
    setSubsection: (sub) => setLocation(
      sub ? `/settings/${activeSection}/${sub}` : `/settings/${activeSection}`,
      { replace: true },
    ),
  }}>
    <div className="flex h-full">
      <SettingsSidebar activeSection={activeSection} onSelectSection={handleSelectSection} />
      <div className="flex-1 overflow-y-auto p-6">
        {ActiveComponent && <ActiveComponent />}
      </div>
    </div>
  </SettingsRouteContext.Provider>
)
```

- [ ] **Step 4: Run → pass**

Run: `cd spa && npx vitest run src/components/SettingsPage.test.tsx`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add spa/src/components/SettingsPage.tsx spa/src/components/SettingsPage.test.tsx
git commit -m "feat(settings): subsection routing via SettingsRouteContext"
```

---

### Task 14: useLocalHistory hook

**Files:**
- Create: `spa/src/features/settings/sections/sync-history/hooks/useLocalHistory.ts`
- Create: `spa/src/features/settings/sections/sync-history/hooks/useLocalHistory.test.ts`

- [ ] **Step 1: Write failing test**

File: `.../hooks/useLocalHistory.test.ts`

```typescript
import { describe, expect, it, beforeEach } from 'vitest'
import { renderHook, waitFor } from '@testing-library/react'
import { useLocalHistory } from './useLocalHistory'
import { setSnapshotStore } from '../../../../../lib/sync/snapshot-store-instance'
import type { SnapshotMetadata } from '../../../../../lib/sync/snapshot-types'

describe('useLocalHistory', () => {
  beforeEach(() => {
    const items: SnapshotMetadata[] = [
      { id: 'a', timestamp: 1, device: 'd', trigger: 'manual', bundleSize: 10, contributorIds: [], isSessionPristine: false },
    ]
    setSnapshotStore({
      init: async () => {},
      listLocal: async () => items,
      getLocal: async () => null,
      createSnapshot: async () => items[0],
      deleteLocal: async () => {},
      compact: async () => ({ kept: [], evicted: [] }),
      clear: async () => {},
    })
  })

  it('returns metadata list and loading state', async () => {
    const { result } = renderHook(() => useLocalHistory())
    expect(result.current.loading).toBe(true)
    await waitFor(() => expect(result.current.loading).toBe(false))
    expect(result.current.items).toHaveLength(1)
    expect(result.current.items[0].id).toBe('a')
  })
})
```

- [ ] **Step 2: Run → fail**

Run: `cd spa && npx vitest run src/features/settings/sections/sync-history/hooks/useLocalHistory.test.ts`
Expected: FAIL

- [ ] **Step 3: Implement**

File: `.../hooks/useLocalHistory.ts`

```typescript
import { useCallback, useEffect, useState } from 'react'
import { getSnapshotStore } from '../../../../../lib/sync/snapshot-store-instance'
import type { SnapshotMetadata } from '../../../../../lib/sync/snapshot-types'

export interface UseLocalHistoryResult {
  items: SnapshotMetadata[]
  loading: boolean
  error: Error | null
  refresh: () => Promise<void>
}

export function useLocalHistory(): UseLocalHistoryResult {
  const [items, setItems] = useState<SnapshotMetadata[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<Error | null>(null)

  const refresh = useCallback(async () => {
    setLoading(true)
    try {
      const store = getSnapshotStore()
      await store.init()
      const list = await store.listLocal()
      setItems(list)
      setError(null)
    } catch (e) {
      setError(e as Error)
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    void refresh()
  }, [refresh])

  return { items, loading, error, refresh }
}
```

- [ ] **Step 4: Run → pass**

Run: `cd spa && npx vitest run src/features/settings/sections/sync-history/hooks/useLocalHistory.test.ts`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add spa/src/features/settings/sections/sync-history/hooks/useLocalHistory.ts spa/src/features/settings/sections/sync-history/hooks/useLocalHistory.test.ts
git commit -m "feat(sync-history): useLocalHistory hook"
```

---

### Task 15: useSnapshotDiff hook

**Files:**
- Create: `.../hooks/useSnapshotDiff.ts`
- Create: `.../hooks/useSnapshotDiff.test.ts`

- [ ] **Step 1: Write failing test**

File: `.../hooks/useSnapshotDiff.test.ts`

```typescript
import { describe, expect, it, beforeEach } from 'vitest'
import { renderHook, waitFor } from '@testing-library/react'
import { useSnapshotDiff } from './useSnapshotDiff'
import { setSnapshotStore } from '../../../../../lib/sync/snapshot-store-instance'
import { __setEngineForTests } from '../../../../../lib/sync/register-sync'
import type { StoredSnapshot } from '../../../../../lib/sync/snapshot-types'

describe('useSnapshotDiff', () => {
  beforeEach(() => {
    setSnapshotStore({
      init: async () => {},
      listLocal: async () => [],
      getLocal: async () => null,
      createSnapshot: async () => ({ id: 'x', timestamp: 0, device: 'd', trigger: 'manual', bundleSize: 0, contributorIds: [], isSessionPristine: false }),
      deleteLocal: async () => {},
      compact: async () => ({ kept: [], evicted: [] }),
      clear: async () => {},
    })
    __setEngineForTests({
      register: () => {},
      getContributors: () => [],
      serialize: () => ({
        version: 1, timestamp: 0, device: 'd',
        collections: { w: { version: 1, data: { x: 1 } } },
      }),
      push: async () => ({ version: 1, timestamp: 0, device: 'd', collections: {} }),
      pull: async () => ({ appliedBundle: null, conflicts: [] }),
    } as never)
  })

  it('computes contributor diff against current state', async () => {
    const snap: StoredSnapshot = {
      id: 's1',
      timestamp: 0,
      device: 'd',
      trigger: 'manual',
      bundleSize: 0,
      contributorIds: ['w'],
      isSessionPristine: false,
      bundle: {
        version: 1,
        timestamp: 0,
        device: 'd',
        collections: { w: { version: 1, data: { x: 2 } } },
      },
    }

    const { result } = renderHook(() => useSnapshotDiff(snap))
    await waitFor(() => expect(result.current).not.toBeNull())
    expect(result.current!.find((d) => d.id === 'w')?.status).toBe('changed')
  })
})
```

- [ ] **Step 2: Run → fail**

Run: `cd spa && npx vitest run src/features/settings/sections/sync-history/hooks/useSnapshotDiff.test.ts`
Expected: FAIL

- [ ] **Step 3: Implement**

File: `.../hooks/useSnapshotDiff.ts`

```typescript
import { useMemo } from 'react'
import { useSyncStore } from '../../../../../lib/sync/use-sync-store'
import { __getActiveEngine } from '../../../../../lib/sync/register-sync'
import { computeSnapshotDiff, type ContributorDiff } from '../../../../../lib/sync/snapshot-diff'
import type { StoredSnapshot } from '../../../../../lib/sync/snapshot-types'

export function useSnapshotDiff(snapshot: StoredSnapshot | null): ContributorDiff[] | null {
  const clientId = useSyncStore((s) => s.clientId) ?? 'unknown'
  const enabledModules = useSyncStore((s) => s.enabledModules)

  return useMemo(() => {
    if (!snapshot) return null
    const engine = __getActiveEngine()
    const enabled = enabledModules.length > 0
      ? enabledModules
      : engine.getContributors().map((c) => c.id)
    const current = engine.serialize(clientId, enabled)
    return computeSnapshotDiff(snapshot.bundle, current)
  }, [snapshot, clientId, enabledModules])
}
```

- [ ] **Step 4: Run → pass**

Run: `cd spa && npx vitest run src/features/settings/sections/sync-history/hooks/useSnapshotDiff.test.ts`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add spa/src/features/settings/sections/sync-history/hooks/useSnapshotDiff.ts spa/src/features/settings/sections/sync-history/hooks/useSnapshotDiff.test.ts
git commit -m "feat(sync-history): useSnapshotDiff hook"
```

---

### Task 16: HistoryRow component

**Files:**
- Create: `.../HistoryRow.tsx`
- Create: `.../HistoryRow.test.tsx`

- [ ] **Step 1: Write failing test**

File: `.../HistoryRow.test.tsx`

```typescript
import { describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
import { HistoryRow } from './HistoryRow'
import type { SnapshotMetadata } from '../../../../lib/sync/snapshot-types'

const base: SnapshotMetadata = {
  id: 'r1',
  timestamp: Date.now(),
  device: 'mlab',
  trigger: 'manual',
  bundleSize: 1234,
  contributorIds: ['workspaces', 'hosts'],
  isSessionPristine: false,
}

describe('HistoryRow', () => {
  it('renders trigger tag and device', () => {
    render(<HistoryRow meta={base} selected={false} onSelect={() => {}} />)
    expect(screen.getByText(/manual/i)).toBeInTheDocument()
    expect(screen.getByText('mlab')).toBeInTheDocument()
  })

  it('highlights when selected', () => {
    const { container } = render(<HistoryRow meta={base} selected onSelect={() => {}} />)
    expect(container.firstChild).toHaveAttribute('data-selected', 'true')
  })

  it('shows pristine badge when isSessionPristine', () => {
    render(<HistoryRow meta={{ ...base, isSessionPristine: true, trigger: 'pre-restore' }} selected={false} onSelect={() => {}} />)
    expect(screen.getByTestId('pristine-badge')).toBeInTheDocument()
  })
})
```

- [ ] **Step 2: Run → fail**

Run: `cd spa && npx vitest run src/features/settings/sections/sync-history/HistoryRow.test.tsx`
Expected: FAIL

- [ ] **Step 3: Implement HistoryRow**

File: `.../HistoryRow.tsx`

```typescript
import { ClockClockwise, FloppyDisk, ShieldCheck, Upload } from '@phosphor-icons/react'
import { useI18nStore } from '../../../../stores/useI18nStore'
import type { SnapshotMetadata } from '../../../../lib/sync/snapshot-types'

const TRIGGER_ICON: Record<SnapshotMetadata['trigger'], typeof FloppyDisk> = {
  auto: ClockClockwise,
  manual: FloppyDisk,
  'pre-import': Upload,
  'pre-restore': ShieldCheck,
}

function formatRelative(ms: number): string {
  const diff = Date.now() - ms
  const minutes = Math.floor(diff / 60000)
  if (minutes < 1) return 'just now'
  if (minutes < 60) return `${minutes}m`
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return `${hours}h`
  const days = Math.floor(hours / 24)
  return `${days}d`
}

export interface HistoryRowProps {
  meta: SnapshotMetadata
  selected: boolean
  onSelect: () => void
}

export function HistoryRow({ meta, selected, onSelect }: HistoryRowProps) {
  const t = useI18nStore((s) => s.t)
  const Icon = TRIGGER_ICON[meta.trigger]
  return (
    <button
      type="button"
      data-selected={selected}
      onClick={onSelect}
      className={[
        'w-full px-3 py-2 flex items-center gap-2 text-left',
        'border-b border-text-subtle/10',
        selected ? 'bg-accent-muted' : 'hover:bg-text-subtle/5',
      ].join(' ')}
    >
      <Icon size={16} className="text-text-muted" />
      <div className="flex-1">
        <div className="text-sm text-text-primary">
          {t(`settings.sync.history.trigger.${meta.trigger === 'pre-import' ? 'preImport' : meta.trigger === 'pre-restore' ? 'preRestore' : meta.trigger}`)}
          <span className="ml-2 text-xs text-text-muted">{formatRelative(meta.timestamp)}</span>
        </div>
        <div className="text-xs text-text-muted">{meta.device}</div>
      </div>
      {meta.isSessionPristine && (
        <span data-testid="pristine-badge" className="text-xs px-1 rounded bg-accent-muted text-accent-base">
          {t('settings.sync.history.trigger.sessionPristine')}
        </span>
      )}
    </button>
  )
}
```

- [ ] **Step 4: Run → pass**

Run: `cd spa && npx vitest run src/features/settings/sections/sync-history/HistoryRow.test.tsx`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add spa/src/features/settings/sections/sync-history/HistoryRow.tsx spa/src/features/settings/sections/sync-history/HistoryRow.test.tsx
git commit -m "feat(sync-history): HistoryRow with trigger icon + pristine badge"
```

---

### Task 17: HistoryList component

**Files:**
- Create: `.../HistoryList.tsx`
- Create: `.../HistoryList.test.tsx`

- [ ] **Step 1: Write failing test**

File: `.../HistoryList.test.tsx`

```typescript
import { describe, expect, it, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { HistoryList } from './HistoryList'
import type { SnapshotMetadata } from '../../../../lib/sync/snapshot-types'

const items: SnapshotMetadata[] = [
  { id: 'a', timestamp: 2, device: 'd', trigger: 'manual', bundleSize: 1, contributorIds: [], isSessionPristine: false },
  { id: 'b', timestamp: 1, device: 'd', trigger: 'pre-import', bundleSize: 1, contributorIds: [], isSessionPristine: false },
]

describe('HistoryList', () => {
  it('renders empty state when no items', () => {
    render(<HistoryList items={[]} loading={false} error={null} selectedId={null} onSelect={() => {}} />)
    expect(screen.getByText(/no/i)).toBeInTheDocument()
  })

  it('renders rows and dispatches onSelect', () => {
    const fn = vi.fn()
    render(<HistoryList items={items} loading={false} error={null} selectedId="a" onSelect={fn} />)
    fireEvent.click(screen.getAllByRole('button')[1])
    expect(fn).toHaveBeenCalledWith('b')
  })

  it('shows loading spinner', () => {
    render(<HistoryList items={[]} loading={true} error={null} selectedId={null} onSelect={() => {}} />)
    expect(screen.getByTestId('loading')).toBeInTheDocument()
  })

  it('shows error + Retry', () => {
    const retry = vi.fn()
    render(<HistoryList items={[]} loading={false} error={new Error('boom')} selectedId={null} onSelect={() => {}} onRetry={retry} />)
    fireEvent.click(screen.getByRole('button', { name: /retry/i }))
    expect(retry).toHaveBeenCalled()
  })
})
```

- [ ] **Step 2: Run → fail**

Run: `cd spa && npx vitest run src/features/settings/sections/sync-history/HistoryList.test.tsx`
Expected: FAIL

- [ ] **Step 3: Implement**

File: `.../HistoryList.tsx`

```typescript
import { CircleNotch } from '@phosphor-icons/react'
import { HistoryRow } from './HistoryRow'
import { useI18nStore } from '../../../../stores/useI18nStore'
import type { SnapshotMetadata } from '../../../../lib/sync/snapshot-types'

export interface HistoryListProps {
  items: SnapshotMetadata[]
  loading: boolean
  error: Error | null
  selectedId: string | null
  onSelect: (id: string) => void
  onRetry?: () => void
}

export function HistoryList(props: HistoryListProps) {
  const t = useI18nStore((s) => s.t)

  if (props.loading) {
    return (
      <div className="flex items-center justify-center p-6" data-testid="loading">
        <CircleNotch className="animate-spin text-text-muted" size={18} />
      </div>
    )
  }

  if (props.error) {
    return (
      <div className="p-6 text-sm text-text-muted">
        {t('settings.sync.history.error.loadList')}
        {props.onRetry && (
          <button type="button" onClick={props.onRetry} className="ml-2 text-accent-base">
            {t('settings.sync.history.retry')}
          </button>
        )}
      </div>
    )
  }

  if (props.items.length === 0) {
    return <div className="p-6 text-sm text-text-muted">{t('settings.sync.history.empty.local')}</div>
  }

  return (
    <div className="flex flex-col">
      {props.items.map((m) => (
        <HistoryRow
          key={m.id}
          meta={m}
          selected={m.id === props.selectedId}
          onSelect={() => props.onSelect(m.id)}
        />
      ))}
    </div>
  )
}
```

- [ ] **Step 4: Run → pass**

Run: `cd spa && npx vitest run src/features/settings/sections/sync-history/HistoryList.test.tsx`
Expected: PASS (4/4)

- [ ] **Step 5: Commit**

```bash
git add spa/src/features/settings/sections/sync-history/HistoryList.tsx spa/src/features/settings/sections/sync-history/HistoryList.test.tsx
git commit -m "feat(sync-history): HistoryList with loading/error/empty"
```

---

### Task 18: SnapshotDetail component

**Files:**
- Create: `.../SnapshotDetail.tsx`
- Create: `.../SnapshotDetail.test.tsx`

- [ ] **Step 1: Write failing test**

File: `.../SnapshotDetail.test.tsx`

```typescript
import { describe, expect, it, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { SnapshotDetail } from './SnapshotDetail'
import type { StoredSnapshot } from '../../../../lib/sync/snapshot-types'

const snap: StoredSnapshot = {
  id: 's1',
  timestamp: Date.now(),
  device: 'd',
  trigger: 'manual',
  bundleSize: 2048,
  contributorIds: ['w', 'h'],
  isSessionPristine: false,
  bundle: { version: 1, timestamp: 0, device: 'd', collections: {} },
}

describe('SnapshotDetail', () => {
  it('shows empty placeholder when no snapshot selected', () => {
    render(<SnapshotDetail snapshot={null} diff={null} onRestore={() => {}} restoring={false} />)
    expect(screen.getByText(/select/i)).toBeInTheDocument()
  })

  it('shows metadata when snapshot given', () => {
    render(<SnapshotDetail snapshot={snap} diff={null} onRestore={() => {}} restoring={false} />)
    expect(screen.getByText(/2048/)).toBeInTheDocument()
  })

  it('shows diff list from contributors', () => {
    render(
      <SnapshotDetail
        snapshot={snap}
        diff={[
          { id: 'w', status: 'changed' },
          { id: 'h', status: 'identical' },
        ]}
        onRestore={() => {}}
        restoring={false}
      />,
    )
    expect(screen.getByText('w')).toBeInTheDocument()
    expect(screen.getByText('h')).toBeInTheDocument()
  })

  it('disables Restore when restoring', () => {
    const fn = vi.fn()
    render(<SnapshotDetail snapshot={snap} diff={[]} onRestore={fn} restoring={true} />)
    const btn = screen.getByRole('button', { name: /restore/i })
    expect(btn).toBeDisabled()
  })
})
```

- [ ] **Step 2: Run → fail**

Run: `cd spa && npx vitest run src/features/settings/sections/sync-history/SnapshotDetail.test.tsx`
Expected: FAIL

- [ ] **Step 3: Implement**

File: `.../SnapshotDetail.tsx`

```typescript
import { useI18nStore } from '../../../../stores/useI18nStore'
import type { ContributorDiff } from '../../../../lib/sync/snapshot-diff'
import type { StoredSnapshot } from '../../../../lib/sync/snapshot-types'

export interface SnapshotDetailProps {
  snapshot: StoredSnapshot | null
  diff: ContributorDiff[] | null
  onRestore: () => void
  restoring: boolean
}

function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`
  return `${(n / 1024 / 1024).toFixed(2)} MB`
}

export function SnapshotDetail({ snapshot, diff, onRestore, restoring }: SnapshotDetailProps) {
  const t = useI18nStore((s) => s.t)
  if (!snapshot) {
    return (
      <div className="p-6 text-sm text-text-muted">
        {t('settings.sync.history.detail.selectPrompt')}
      </div>
    )
  }
  return (
    <div className="p-6 flex flex-col gap-4">
      <section>
        <h3 className="text-sm font-semibold text-text-primary">{t('settings.sync.history.detail.metadata')}</h3>
        <dl className="mt-2 text-sm text-text-muted space-y-1">
          <div><dt className="inline">{t('settings.sync.history.detail.timestamp')}:</dt> <dd className="inline">{new Date(snapshot.timestamp).toLocaleString()}</dd></div>
          <div><dt className="inline">{t('settings.sync.history.detail.device')}:</dt> <dd className="inline">{snapshot.device}</dd></div>
          <div><dt className="inline">{t('settings.sync.history.detail.size')}:</dt> <dd className="inline">{formatBytes(snapshot.bundleSize)}</dd></div>
        </dl>
      </section>
      {diff && diff.length > 0 && (
        <section>
          <h3 className="text-sm font-semibold text-text-primary">{t('settings.sync.history.detail.diff.title')}</h3>
          <ul className="mt-2 text-sm space-y-1">
            {diff.map((d) => (
              <li key={d.id} className="flex items-center gap-2">
                <span className="text-text-primary">{d.id}</span>
                <span className="text-text-muted text-xs">{t(`settings.sync.history.detail.diff.${d.status === 'missing-in-snapshot' ? 'missingInSnapshot' : d.status === 'missing-in-current' ? 'missingInCurrent' : d.status}`)}</span>
              </li>
            ))}
          </ul>
        </section>
      )}
      <div>
        <button
          type="button"
          onClick={onRestore}
          disabled={restoring}
          className="px-4 py-2 bg-accent-base text-white rounded disabled:opacity-50"
        >
          {t('settings.sync.history.detail.restore')}
        </button>
      </div>
    </div>
  )
}
```

- [ ] **Step 4: Run → pass**

Run: `cd spa && npx vitest run src/features/settings/sections/sync-history/SnapshotDetail.test.tsx`
Expected: PASS (4/4)

- [ ] **Step 5: Commit**

```bash
git add spa/src/features/settings/sections/sync-history/SnapshotDetail.tsx spa/src/features/settings/sections/sync-history/SnapshotDetail.test.tsx
git commit -m "feat(sync-history): SnapshotDetail with metadata + diff + restore button"
```

---

### Task 19: SnapshotRestoreDialog

**Files:**
- Create: `.../SnapshotRestoreDialog.tsx`
- Create: `.../SnapshotRestoreDialog.test.tsx`

- [ ] **Step 1: Write failing test**

File: `.../SnapshotRestoreDialog.test.tsx`

```typescript
import { describe, expect, it, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { SnapshotRestoreDialog } from './SnapshotRestoreDialog'

describe('SnapshotRestoreDialog', () => {
  it('dispatches onConfirm when Restore clicked', () => {
    const onConfirm = vi.fn()
    render(<SnapshotRestoreDialog open pendingConflictCount={0} onCancel={() => {}} onConfirm={onConfirm} restoring={false} />)
    fireEvent.click(screen.getByRole('button', { name: /restore/i }))
    expect(onConfirm).toHaveBeenCalled()
  })

  it('shows pending conflicts warning when count > 0', () => {
    render(<SnapshotRestoreDialog open pendingConflictCount={3} onCancel={() => {}} onConfirm={() => {}} restoring={false} />)
    expect(screen.getByText(/3/)).toBeInTheDocument()
  })

  it('disables Restore while restoring', () => {
    render(<SnapshotRestoreDialog open pendingConflictCount={0} onCancel={() => {}} onConfirm={() => {}} restoring={true} />)
    expect(screen.getByRole('button', { name: /restore/i })).toBeDisabled()
  })
})
```

- [ ] **Step 2: Run → fail**

Expected: FAIL

- [ ] **Step 3: Implement**

File: `.../SnapshotRestoreDialog.tsx`

```typescript
import { useI18nStore } from '../../../../stores/useI18nStore'
import { pluralKey } from '../../../../lib/plural'

export interface SnapshotRestoreDialogProps {
  open: boolean
  pendingConflictCount: number
  onCancel: () => void
  onConfirm: () => void
  restoring: boolean
}

export function SnapshotRestoreDialog(props: SnapshotRestoreDialogProps) {
  const t = useI18nStore((s) => s.t)
  if (!props.open) return null
  return (
    <div role="dialog" aria-modal="true" className="fixed inset-0 flex items-center justify-center bg-black/40 z-50">
      <div className="bg-bg-surface border border-text-subtle/20 rounded-md p-6 max-w-md">
        <h2 className="text-lg font-semibold text-text-primary">{t('settings.sync.history.restore.confirmTitle')}</h2>
        <p className="mt-2 text-sm text-text-muted">{t('settings.sync.history.restore.confirmBody')}</p>
        {props.pendingConflictCount > 0 && (
          <p className="mt-2 text-sm text-status-warn-text">
            {t(pluralKey('settings.sync.history.restore.confirmPendingConflicts', props.pendingConflictCount), { n: props.pendingConflictCount })}
          </p>
        )}
        <div className="mt-4 flex justify-end gap-2">
          <button type="button" onClick={props.onCancel} className="px-3 py-1 text-sm text-text-muted">
            {t('settings.sync.history.restore.cancel')}
          </button>
          <button
            type="button"
            onClick={props.onConfirm}
            disabled={props.restoring}
            className="px-3 py-1 text-sm bg-accent-base text-white rounded disabled:opacity-50"
          >
            {t('settings.sync.history.restore.proceed')}
          </button>
        </div>
      </div>
    </div>
  )
}
```

- [ ] **Step 4: Run → pass**

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add spa/src/features/settings/sections/sync-history/SnapshotRestoreDialog.tsx spa/src/features/settings/sections/sync-history/SnapshotRestoreDialog.test.tsx
git commit -m "feat(sync-history): SnapshotRestoreDialog with conflict warning"
```

---

### Task 20: HistoryTabs component

**Files:**
- Create: `.../HistoryTabs.tsx`
- Create: `.../HistoryTabs.test.tsx`

- [ ] **Step 1: Write failing test**

```typescript
import { describe, expect, it, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { HistoryTabs } from './HistoryTabs'

describe('HistoryTabs', () => {
  it('renders Local selected', () => {
    render(<HistoryTabs active="local" remoteAvailable={false} onChange={() => {}} />)
    expect(screen.getByRole('tab', { name: /local/i })).toHaveAttribute('aria-selected', 'true')
  })

  it('Remote tab disabled when remoteAvailable=false', () => {
    render(<HistoryTabs active="local" remoteAvailable={false} onChange={() => {}} />)
    expect(screen.getByRole('tab', { name: /remote/i })).toBeDisabled()
  })

  it('dispatches onChange', () => {
    const fn = vi.fn()
    render(<HistoryTabs active="local" remoteAvailable={true} onChange={fn} />)
    fireEvent.click(screen.getByRole('tab', { name: /remote/i }))
    expect(fn).toHaveBeenCalledWith('remote')
  })
})
```

- [ ] **Step 2: Run → fail**

Expected: FAIL

- [ ] **Step 3: Implement**

```typescript
import { useI18nStore } from '../../../../stores/useI18nStore'

export interface HistoryTabsProps {
  active: 'local' | 'remote'
  remoteAvailable: boolean
  onChange: (tab: 'local' | 'remote') => void
}

export function HistoryTabs(props: HistoryTabsProps) {
  const t = useI18nStore((s) => s.t)
  return (
    <div role="tablist" className="flex border-b border-text-subtle/20">
      <button
        role="tab"
        aria-selected={props.active === 'local'}
        onClick={() => props.onChange('local')}
        className={[
          'px-4 py-2 text-sm',
          props.active === 'local' ? 'border-b-2 border-accent-base text-accent-base' : 'text-text-muted',
        ].join(' ')}
      >
        {t('settings.sync.history.tabs.local')}
      </button>
      <button
        role="tab"
        aria-selected={props.active === 'remote'}
        disabled={!props.remoteAvailable}
        onClick={() => props.onChange('remote')}
        title={!props.remoteAvailable ? t('settings.sync.history.tabs.remoteDaemonOnly') : undefined}
        className={[
          'px-4 py-2 text-sm',
          props.active === 'remote' ? 'border-b-2 border-accent-base text-accent-base' : 'text-text-muted',
          !props.remoteAvailable ? 'opacity-50 cursor-not-allowed' : '',
        ].join(' ')}
      >
        {t('settings.sync.history.tabs.remote')}
      </button>
    </div>
  )
}
```

- [ ] **Step 4: Run → pass**

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add spa/src/features/settings/sections/sync-history/HistoryTabs.tsx spa/src/features/settings/sections/sync-history/HistoryTabs.test.tsx
git commit -m "feat(sync-history): HistoryTabs with Local/Remote (Remote disabled in PR A)"
```

---

### Task 21: SnapshotHistoryPage composition

**Files:**
- Create: `.../SnapshotHistoryPage.tsx`
- Create: `.../SnapshotHistoryPage.test.tsx`

- [ ] **Step 1: Write failing test**

File: `.../SnapshotHistoryPage.test.tsx`

```typescript
import { describe, expect, it, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { SnapshotHistoryPage } from './SnapshotHistoryPage'
import { setSnapshotStore } from '../../../../lib/sync/snapshot-store-instance'
import { useSyncStore } from '../../../../lib/sync/use-sync-store'
import type { SnapshotMetadata, StoredSnapshot } from '../../../../lib/sync/snapshot-types'

const items: SnapshotMetadata[] = [
  { id: 'm1', timestamp: 2, device: 'd', trigger: 'manual', bundleSize: 10, contributorIds: ['w'], isSessionPristine: false },
]
const fullSnap: StoredSnapshot = {
  ...items[0],
  bundle: { version: 1, timestamp: 0, device: 'd', collections: { w: { version: 1, data: {} } } },
}

describe('SnapshotHistoryPage', () => {
  beforeEach(() => {
    setSnapshotStore({
      init: async () => {},
      listLocal: async () => items,
      getLocal: async (id) => (id === 'm1' ? fullSnap : null),
      createSnapshot: async () => items[0],
      deleteLocal: async () => {},
      compact: async () => ({ kept: [], evicted: [] }),
      clear: async () => {},
    })
    useSyncStore.getState().reset()
  })

  it('renders list → select row → detail shows → click Restore opens dialog', async () => {
    render(<SnapshotHistoryPage />)
    await waitFor(() => expect(screen.getAllByRole('button').some((b) => b.textContent?.includes('manual'))).toBe(true))
    // Select row
    const row = screen.getAllByRole('button').find((b) => b.textContent?.includes('manual'))!
    fireEvent.click(row)
    await waitFor(() => expect(screen.getByRole('button', { name: /restore/i })).toBeInTheDocument())
    // Click Restore → Dialog opens
    fireEvent.click(screen.getByRole('button', { name: /restore/i }))
    expect(screen.getByRole('dialog')).toBeInTheDocument()
  })
})
```

- [ ] **Step 2: Run → fail**

Expected: FAIL

- [ ] **Step 3: Implement**

File: `.../SnapshotHistoryPage.tsx`

```typescript
import { useEffect, useState } from 'react'
import { HistoryTabs } from './HistoryTabs'
import { HistoryList } from './HistoryList'
import { SnapshotDetail } from './SnapshotDetail'
import { SnapshotRestoreDialog } from './SnapshotRestoreDialog'
import { useLocalHistory } from './hooks/useLocalHistory'
import { useSnapshotDiff } from './hooks/useSnapshotDiff'
import { useSyncStore } from '../../../../lib/sync/use-sync-store'
import { getSnapshotStore } from '../../../../lib/sync/snapshot-store-instance'
import type { StoredSnapshot } from '../../../../lib/sync/snapshot-types'

export function SnapshotHistoryPage() {
  const { items, loading, error, refresh } = useLocalHistory()
  const [activeTab, setActiveTab] = useState<'local' | 'remote'>('local')
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const [selectedSnap, setSelectedSnap] = useState<StoredSnapshot | null>(null)
  const [dialogOpen, setDialogOpen] = useState(false)
  const [restoring, setRestoring] = useState(false)

  const activeProviderId = useSyncStore((s) => s.activeProviderId)
  const pendingConflicts = useSyncStore((s) => s.pendingConflicts)
  const restoreFromSnapshot = useSyncStore((s) => s.restoreFromSnapshot)

  const diff = useSnapshotDiff(selectedSnap)

  // Load full snapshot bundle when selection changes
  useEffect(() => {
    if (!selectedId) {
      setSelectedSnap(null)
      return
    }
    let cancelled = false
    void getSnapshotStore().getLocal(selectedId).then((snap) => {
      if (!cancelled) setSelectedSnap(snap)
    })
    return () => { cancelled = true }
  }, [selectedId])

  async function handleRestore() {
    if (!selectedSnap) return
    setRestoring(true)
    try {
      await restoreFromSnapshot(selectedSnap, 'local')
      setDialogOpen(false)
      // Refresh list (pre-restore snapshot appears)
      await refresh()
    } finally {
      setRestoring(false)
    }
  }

  return (
    <div className="flex h-full flex-col">
      <HistoryTabs
        active={activeTab}
        remoteAvailable={activeProviderId === 'daemon'}
        onChange={setActiveTab}
      />
      <div className="flex flex-1 overflow-hidden">
        <div className="w-80 overflow-y-auto border-r border-text-subtle/20">
          {activeTab === 'local' ? (
            <HistoryList
              items={items}
              loading={loading}
              error={error}
              selectedId={selectedId}
              onSelect={setSelectedId}
              onRetry={refresh}
            />
          ) : (
            // PR B will wire remote tab
            <div className="p-6 text-sm text-text-muted">Remote tab: PR B</div>
          )}
        </div>
        <div className="flex-1 overflow-y-auto">
          <SnapshotDetail
            snapshot={selectedSnap}
            diff={diff}
            onRestore={() => setDialogOpen(true)}
            restoring={restoring}
          />
        </div>
      </div>
      <SnapshotRestoreDialog
        open={dialogOpen}
        pendingConflictCount={pendingConflicts.length}
        onCancel={() => setDialogOpen(false)}
        onConfirm={handleRestore}
        restoring={restoring}
      />
    </div>
  )
}
```

- [ ] **Step 4: Run → pass**

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add spa/src/features/settings/sections/sync-history/SnapshotHistoryPage.tsx spa/src/features/settings/sections/sync-history/SnapshotHistoryPage.test.tsx
git commit -m "feat(sync-history): SnapshotHistoryPage composition"
```

---

### Task 22: Wire SnapshotHistoryPage into SyncSection via subsection route

**Files:**
- Modify: `spa/src/components/settings/SyncSection.tsx`
- Modify: `spa/src/components/settings/SyncSection.test.tsx`

- [ ] **Step 1: Export SettingsRouteContext from SettingsPage**

File: `spa/src/components/SettingsPage.tsx`

將 Task 13 建立的 context 明確 export：

```typescript
export { SettingsRouteContext }
export { useSettingsRoute }
```

- [ ] **Step 2: Write failing test**

在 `SyncSection.test.tsx` 追加：

```typescript
import { SettingsRouteContext } from '../SettingsPage'

describe('SyncSection subsection routing', () => {
  it('renders SnapshotHistoryPage when subsection=history', () => {
    render(
      <SettingsRouteContext.Provider value={{ subsection: 'history', setSubsection: () => {} }}>
        <SyncSection />
      </SettingsRouteContext.Provider>,
    )
    // SnapshotHistoryPage 會渲染 Tabs，用 role=tablist 驗證
    expect(screen.getByRole('tablist')).toBeInTheDocument()
  })

  it('renders main Sync section when subsection=null', () => {
    render(
      <SettingsRouteContext.Provider value={{ subsection: null, setSubsection: () => {} }}>
        <SyncSection />
      </SettingsRouteContext.Provider>,
    )
    // 主 section 的 provider selector label 才會出現
    expect(screen.getByText(/provider/i)).toBeInTheDocument()
  })

  it('"View History" button calls setSubsection("history")', () => {
    const fn = vi.fn()
    render(
      <SettingsRouteContext.Provider value={{ subsection: null, setSubsection: fn }}>
        <SyncSection />
      </SettingsRouteContext.Provider>,
    )
    fireEvent.click(screen.getByRole('button', { name: /history/i }))
    expect(fn).toHaveBeenCalledWith('history')
  })
})
```

- [ ] **Step 2: Run → fail**

Expected: FAIL — subsection not handled

- [ ] **Step 3: Update SyncSection**

File: `spa/src/components/settings/SyncSection.tsx`

頂部 import：

```typescript
import { useSettingsRoute } from '../SettingsPage'
import { SnapshotHistoryPage } from '../../features/settings/sections/sync-history/SnapshotHistoryPage'
```

在 function body 開頭：

```typescript
const { subsection, setSubsection } = useSettingsRoute()
if (subsection === 'history') {
  return <SnapshotHistoryPage />
}
```

並在「Sync Now」區塊附近加一個新按鈕（找到顯示 last-sync 的區塊，在合理位置加）：

```typescript
<button
  type="button"
  onClick={() => setSubsection('history')}
  className="px-3 py-1 text-sm text-accent-base"
>
  {t('settings.sync.history.viewLink')}
</button>
```

- [ ] **Step 4: Run → pass**

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add spa/src/components/settings/SyncSection.tsx spa/src/components/settings/SyncSection.test.tsx
git commit -m "feat(sync): wire SnapshotHistoryPage subsection + View History button"
```

---

### Task 23: Wire pre-import + post-sync snapshot into SyncSection + status banner

**Files:**
- Modify: `spa/src/components/settings/SyncSection.tsx`
- Modify: `spa/src/components/settings/SyncSection.test.tsx`

- [ ] **Step 1: Write failing tests**

在 SyncSection.test.tsx 加：

```typescript
describe('SyncSection pre-import snapshot', () => {
  it('calls createPreOperationSnapshot before applyImport when file chosen', async () => {
    // mock applyImport + spy on createPreOperationSnapshot
    // load a valid JSON file via input
    // assert snapshot called before applyImport
  })

  it('does NOT create pre-import snapshot if file read fails before validation', async () => {
    // feed invalid JSON → expect no snapshot
  })
})

describe('SyncSection post-sync snapshot', () => {
  it('creates manual snapshot when syncNow succeeds with new bundle', async () => {
    // mock syncNow to return ok
    // spy SnapshotStore.createSnapshot
    // assert trigger='manual'
  })

  it('dedups: if equalExceptEnvelope(newBundle, lastSyncedBundle) true → skip snapshot', async () => {
    // supply lastSyncedBundle and returned bundle with same collections
  })
})

describe('SyncSection restore status banner', () => {
  it('displays message when restore succeeds', () => {
    // trigger restoreFromSnapshot via mock; assert banner text appears
  })
})
```

- [ ] **Step 2: Run → fail**

Expected: FAIL

- [ ] **Step 3: Implement wiring**

File: `spa/src/components/settings/SyncSection.tsx`

找 `handleFileChange`（既有）。在 `applyImport(text, {...})` 呼叫之前加：

```typescript
// P1: pre-import safety-net snapshot (after validation passes inside applyImport)
```

這題的順序要小心：spec §3 說「applyImport validate → 通過後才建 pre-import」。`applyImport` 本身會 validate；所以改成：

```typescript
try {
  const result = await applyImport(text, { ... })
  if (result.kind === 'ok') {
    // validation passed → create pre-import snapshot of current (pre-import-but-already-applied?)
    // NB: spec says snapshot 在 deserialize 之前、validate 之後
  }
} catch ...
```

**實作步驟：**

**Step 3a:** File: `spa/src/lib/sync/providers/manual-provider.ts`

把既有 `importFromText` 的 validation 前半（parse + size 檢查 + depth 檢查）抽出 export：

```typescript
export function validateImportText(text: string): SyncBundle {
  if (text.length > 5 * 1024 * 1024) {
    throw new ImportError('too-large', `Import exceeds 5MB limit`)
  }
  let parsed: unknown
  try {
    parsed = JSON.parse(text)
  } catch {
    throw new ImportError('invalid-json', 'Not valid JSON')
  }
  if (objectDepth(parsed) > 32) {
    throw new ImportError('too-deep', 'Nesting exceeds 32 levels')
  }
  // shape check
  if (!parsed || typeof parsed !== 'object' || !('collections' in parsed)) {
    throw new ImportError('invalid-shape', 'Missing collections')
  }
  return parsed as SyncBundle
}
```

然後讓 `importFromText` 改成：

```typescript
async function importFromText(text: string): Promise<SyncBundle> {
  return validateImportText(text)
}
```

**Step 3b:** File: `spa/src/components/settings/SyncSection.tsx`

import：
```typescript
import { validateImportText } from '../../lib/sync/providers/manual-provider'
```

修改 `handleFileChange`：

```typescript
const handleFileChange = async (e: ChangeEvent<HTMLInputElement>) => {
  const file = e.target.files?.[0]
  if (!file) return
  const text = await file.text()
  try {
    // P1: validate first — if this throws, NO pre-import snapshot is created
    validateImportText(text)
  } catch (err) {
    // existing error handling (ImportError → toast etc.)
    return
  }
  // Validation passed → take safety snapshot of current state
  await useSyncStore.getState().createPreOperationSnapshot('pre-import')
  // Existing applyImport path continues
  const result = await applyImport(text, { engine: syncEngine, enabledModules, lastSyncedBundle })
  // ... existing result handling
}
```

找 `handleSyncNow`，在 `result.kind === 'ok'` 分支內加：

```typescript
// P1: create manual snapshot if bundle changed
const prev = lastSyncedBundle
const newBundle = result.appliedBundle
if (!prev || !equalExceptEnvelope(prev, newBundle)) {
  void getSnapshotStore().createSnapshot(newBundle, 'manual')
}
```

Import 頂部追加：

```typescript
import { equalExceptEnvelope } from '../../lib/sync/snapshot-diff'
import { getSnapshotStore } from '../../lib/sync/snapshot-store-instance'
```

- [ ] **Step 4: Run → pass**

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add spa/src/components/settings/SyncSection.tsx spa/src/lib/sync/providers/manual-provider.ts spa/src/components/settings/SyncSection.test.tsx
git commit -m "feat(sync): pre-import + post-sync snapshot wiring + restore status banner"
```

---

### Task 24: Session-pristine pinning on init

**Files:**
- Modify: `spa/src/lib/sync/register-sync.ts`
- Create: `spa/src/lib/sync/__tests__/session-pristine.test.ts`

- [ ] **Step 1: Write failing test**

File: `spa/src/lib/sync/__tests__/session-pristine.test.ts`

```typescript
import { describe, expect, it, beforeEach, vi } from 'vitest'
import { ensureSessionPristine } from '../register-sync'
import { setSnapshotStore } from '../snapshot-store-instance'
import { __setEngineForTests, syncEngine } from '../register-sync'

describe('ensureSessionPristine', () => {
  beforeEach(() => {
    __setEngineForTests({
      register: () => {},
      getContributors: () => [],
      serialize: () => ({
        version: 1, timestamp: 0, device: 'd',
        collections: { x: { version: 1, data: {} } },
      }),
      push: async () => ({ version: 1, timestamp: 0, device: 'd', collections: {} }),
      pull: async () => ({ appliedBundle: null, conflicts: [] }),
    } as never)
  })

  it('creates a session-pristine snapshot if none exists', async () => {
    const created: Array<{ trigger: string; isPristine: boolean }> = []
    setSnapshotStore({
      init: async () => {},
      listLocal: async () => [],
      getLocal: async () => null,
      createSnapshot: async (_b, trigger, opts) => {
        created.push({ trigger, isPristine: opts?.isSessionPristine ?? false })
        return { id: 'p', timestamp: 0, device: 'd', trigger, bundleSize: 0, contributorIds: [], isSessionPristine: opts?.isSessionPristine ?? false }
      },
      deleteLocal: async () => {},
      compact: async () => ({ kept: [], evicted: [] }),
      clear: async () => {},
    })

    await ensureSessionPristine()
    expect(created).toHaveLength(1)
    expect(created[0].isPristine).toBe(true)
    expect(created[0].trigger).toBe('pre-restore')
  })

  it('does not create duplicate pristine if one already exists', async () => {
    const created: Array<unknown> = []
    setSnapshotStore({
      init: async () => {},
      listLocal: async () => [
        { id: 'existing', timestamp: 0, device: 'd', trigger: 'pre-restore', bundleSize: 0, contributorIds: [], isSessionPristine: true },
      ],
      getLocal: async () => null,
      createSnapshot: async (...args) => { created.push(args); return { id: 'x', timestamp: 0, device: 'd', trigger: 'manual', bundleSize: 0, contributorIds: [], isSessionPristine: false } },
      deleteLocal: async () => {},
      compact: async () => ({ kept: [], evicted: [] }),
      clear: async () => {},
    })
    await ensureSessionPristine()
    expect(created).toHaveLength(0)
  })
})
```

- [ ] **Step 2: Run → fail**

Expected: FAIL

- [ ] **Step 3: Implement**

File: `spa/src/lib/sync/register-sync.ts`（append）

```typescript
import { getSnapshotStore } from './snapshot-store-instance'
import { useSyncStore } from './use-sync-store'

/**
 * Ensure there is a session-pristine snapshot so the user can always return
 * to "state at session start" regardless of how many restores they perform.
 *
 * Called once during app bootstrap (after stores are hydrated).
 */
export async function ensureSessionPristine(): Promise<void> {
  const store = getSnapshotStore()
  await store.init()
  const existing = await store.listLocal()
  if (existing.some((m) => m.isSessionPristine)) return

  const engine = __getActiveEngine()
  const state = useSyncStore.getState()
  const device = state.clientId ?? 'unknown'
  const enabled = state.enabledModules.length > 0
    ? state.enabledModules
    : engine.getContributors().map((c) => c.id)
  const currentBundle = engine.serialize(device, enabled)
  await store.createSnapshot(currentBundle, 'pre-restore', { isSessionPristine: true })
}
```

將 `ensureSessionPristine()` 呼叫安插在 app bootstrap 入口（`App.tsx` 或現有 `register-sync` 的初始化 effect）：

File: `spa/src/App.tsx`（找到初始化 effect）

```typescript
useEffect(() => {
  void ensureSessionPristine()
}, [])
```

- [ ] **Step 4: Run → pass**

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add spa/src/lib/sync/register-sync.ts spa/src/lib/sync/__tests__/session-pristine.test.ts spa/src/App.tsx
git commit -m "feat(sync): ensureSessionPristine on bootstrap"
```

---

### Task 25: i18n keys (en + zh-TW)

**Files:**
- Modify: `spa/src/i18n/locales/en.json`
- Modify: `spa/src/i18n/locales/zh-TW.json`

- [ ] **Step 1: Write a failing snapshot test**

File: `spa/src/i18n/__tests__/history-keys.test.ts`

```typescript
import { describe, expect, it } from 'vitest'
import en from '../locales/en.json'
import zhTW from '../locales/zh-TW.json'

const REQUIRED_KEYS = [
  'settings.sync.history.title',
  'settings.sync.history.viewLink',
  'settings.sync.history.tabs.local',
  'settings.sync.history.tabs.remote',
  'settings.sync.history.tabs.remoteDaemonOnly',
  'settings.sync.history.empty.local',
  'settings.sync.history.retry',
  'settings.sync.history.error.loadList',
  'settings.sync.history.trigger.auto',
  'settings.sync.history.trigger.manual',
  'settings.sync.history.trigger.preImport',
  'settings.sync.history.trigger.preRestore',
  'settings.sync.history.trigger.sessionPristine',
  'settings.sync.history.detail.metadata',
  'settings.sync.history.detail.timestamp',
  'settings.sync.history.detail.device',
  'settings.sync.history.detail.size',
  'settings.sync.history.detail.diff.title',
  'settings.sync.history.detail.diff.identical',
  'settings.sync.history.detail.diff.changed',
  'settings.sync.history.detail.diff.missingInSnapshot',
  'settings.sync.history.detail.diff.missingInCurrent',
  'settings.sync.history.detail.restore',
  'settings.sync.history.detail.selectPrompt',
  'settings.sync.history.restore.confirmTitle',
  'settings.sync.history.restore.confirmBody',
  'settings.sync.history.restore.confirmPendingConflicts_one',
  'settings.sync.history.restore.confirmPendingConflicts_other',
  'settings.sync.history.restore.cancel',
  'settings.sync.history.restore.proceed',
  'settings.sync.history.restore.success',
]

function flatten(obj: unknown, prefix = ''): Record<string, unknown> {
  const out: Record<string, unknown> = {}
  if (typeof obj !== 'object' || obj === null) return out
  for (const [k, v] of Object.entries(obj)) {
    const key = prefix ? `${prefix}.${k}` : k
    if (typeof v === 'object' && v !== null && !Array.isArray(v)) {
      Object.assign(out, flatten(v, key))
    } else {
      out[key] = v
    }
  }
  return out
}

describe('history i18n keys', () => {
  it('en has all required keys', () => {
    const flat = flatten(en)
    for (const k of REQUIRED_KEYS) expect(flat[k]).toBeDefined()
  })

  it('zh-TW has all required keys', () => {
    const flat = flatten(zhTW)
    for (const k of REQUIRED_KEYS) expect(flat[k]).toBeDefined()
  })
})
```

- [ ] **Step 2: Run → fail**

Expected: FAIL

- [ ] **Step 3: Add keys to both locales**

File: `spa/src/i18n/locales/en.json`（在 `settings.sync.*` 底下加 `history` block）

```json
"history": {
  "title": "Sync History",
  "viewLink": "View Sync History",
  "tabs": {
    "local": "Local",
    "remote": "Remote",
    "remoteDaemonOnly": "Remote history is only available when using the daemon provider."
  },
  "empty": {
    "local": "No snapshots yet. Sync to create one.",
    "remote": "No remote history yet."
  },
  "retry": "Retry",
  "error": {
    "loadList": "Failed to load snapshots.",
    "loadBundle": "Failed to load snapshot.",
    "notFound": "This snapshot no longer exists.",
    "noAccess": "You no longer have access to this group's history.",
    "corrupted": "Snapshot data is corrupted."
  },
  "notSupported": "Snapshot history is not available in this browser.",
  "trigger": {
    "auto": "auto",
    "manual": "manual",
    "preImport": "pre-import",
    "preRestore": "pre-restore",
    "sessionPristine": "pristine"
  },
  "bucket": {
    "hourly": "hourly",
    "daily": "daily",
    "weekly": "weekly",
    "monthly": "monthly",
    "preOp": "safety"
  },
  "detail": {
    "metadata": "Metadata",
    "timestamp": "Created",
    "device": "Device",
    "size": "Size",
    "selectPrompt": "Select a snapshot to see details.",
    "restore": "Restore this snapshot",
    "diff": {
      "title": "Changes if restored",
      "identical": "identical",
      "changed": "changed",
      "missingInSnapshot": "missing in snapshot",
      "missingInCurrent": "not in current state"
    }
  },
  "restore": {
    "confirmTitle": "Restore this snapshot?",
    "confirmBody": "Your current state will be overwritten on this device. A pre-restore backup will be created automatically.",
    "confirmPendingConflicts_one": "This will discard {{n}} pending conflict.",
    "confirmPendingConflicts_other": "This will discard {{n}} pending conflicts.",
    "preOpFailed": "Failed to create backup. Continue anyway?",
    "continueAnyway": "Continue anyway",
    "cancel": "Cancel",
    "proceed": "Restore",
    "success": "Restored from {{device}} · {{time}}",
    "warnings": "Restored with warnings: {{names}}"
  }
}
```

File: `spa/src/i18n/locales/zh-TW.json`（同結構，中文翻譯）

```json
"history": {
  "title": "同步歷史",
  "viewLink": "查看同步歷史",
  "tabs": {
    "local": "本機",
    "remote": "遠端",
    "remoteDaemonOnly": "遠端歷史僅在使用 daemon provider 時可用。"
  },
  "empty": {
    "local": "還沒有快照。執行同步後會自動建立。",
    "remote": "尚無遠端歷史。"
  },
  "retry": "重試",
  "error": {
    "loadList": "讀取快照失敗。",
    "loadBundle": "讀取快照內容失敗。",
    "notFound": "此快照已不存在。",
    "noAccess": "你已無法存取此群組的歷史。",
    "corrupted": "快照資料毀損。"
  },
  "notSupported": "此瀏覽器不支援快照歷史。",
  "trigger": {
    "auto": "自動",
    "manual": "手動",
    "preImport": "匯入前備份",
    "preRestore": "還原前備份",
    "sessionPristine": "起始"
  },
  "bucket": {
    "hourly": "小時",
    "daily": "日",
    "weekly": "週",
    "monthly": "月",
    "preOp": "備份"
  },
  "detail": {
    "metadata": "資訊",
    "timestamp": "建立時間",
    "device": "裝置",
    "size": "大小",
    "selectPrompt": "選取一筆快照以查看詳情。",
    "restore": "還原此快照",
    "diff": {
      "title": "還原後的變動",
      "identical": "相同",
      "changed": "有差異",
      "missingInSnapshot": "快照中缺少",
      "missingInCurrent": "當前狀態缺少"
    }
  },
  "restore": {
    "confirmTitle": "還原此快照？",
    "confirmBody": "此裝置目前的狀態會被覆寫。系統會自動建立一份還原前的備份。",
    "confirmPendingConflicts_one": "將清掉 {{n}} 項待處理衝突。",
    "confirmPendingConflicts_other": "將清掉 {{n}} 項待處理衝突。",
    "preOpFailed": "建立備份失敗，仍要繼續？",
    "continueAnyway": "仍要繼續",
    "cancel": "取消",
    "proceed": "還原",
    "success": "已從 {{device}} · {{time}} 還原",
    "warnings": "還原完成，但 {{names}} 失敗"
  }
}
```

- [ ] **Step 4: Run → pass**

Run: `cd spa && npx vitest run src/i18n/__tests__/history-keys.test.ts`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add spa/src/i18n/locales/en.json spa/src/i18n/locales/zh-TW.json spa/src/i18n/__tests__/history-keys.test.ts
git commit -m "feat(i18n): sync history keys (en + zh-TW)"
```

---

### Task 26: Full regression + lint

- [ ] **Step 1: Run full test suite**

Run: `cd spa && npx vitest run`
Expected: All tests pass (既有 + 新增，應為 2100+ passing)

- [ ] **Step 2: Run lint**

Run: `cd spa && pnpm run lint`
Expected: 9 pre-existing errors only（zero new），否則 fix before commit

- [ ] **Step 3: Run typecheck**

Run: `cd spa && npx tsc --noEmit`
Expected: 58 pre-existing errors only（zero new）

- [ ] **Step 4: Manual smoke test**

啟動 dev server + Electron，走一次 golden path：
- 進 Settings → Sync → 按 "View Sync History"
- 觸發 manual sync 幾次、觀察清單出現
- 點一筆 snapshot → 詳情顯示 → 按 Restore → Dialog 確認 → 還原完成後再 sync 確認 append-only 生成新 history
- 匯入一個測試 `.purdex-sync` 檔 → 前後對照確認 pre-import snapshot 出現在清單中
- Session restart → 確認 session-pristine snapshot 存在且顯示 pristine tag

- [ ] **Step 5: Final commit (if anything found)**

```bash
git commit -m "fix(sync-history): regression / lint cleanup"
```

---

## Self-Review Checklist

- [ ] Spec §1 架構 → 對應 Task 3-8, 11-13
- [ ] Spec §2 Components → Task 14-21
- [ ] Spec §3 Data flow → Task 11, 22-24
- [ ] Spec §4 Error handling → Task 17-19（spinner、error banner、quota bypass；quota busy loop 的 fail-fast 交給 SnapshotStore compact 不需 retry）
- [ ] Spec §5 Testing → Task 1-25 每個都含 test
- [ ] Spec §6 依賴 → Task 1（idb + fake-indexeddb）
- [ ] Spec §7 i18n → Task 25
- [ ] Spec §8 PR A 交付清單 → Task 1-26 完整 cover
- [ ] Spec §9 YAGNI → PR A 不做 Remote tab 的實際資料接線（Remote tab 顯示 "Remote tab: PR B" 占位）
- [ ] Task 命名一致：SnapshotStore / SnapshotHistoryPage / SnapshotRestoreDialog 全檔一致
- [ ] `equalExceptEnvelope` 命名在 Task 9 定義，於 Task 23 使用，一致

---

## Out-of-scope for PR A（PR B 處理）

- Daemon `GET /api/sync/history/:id/bundle` endpoint
- Daemon `compactor.go` + migration
- `DaemonProvider.getSnapshotBundle`
- Remote tab 真正的資料接線（PR A 中該 tab 顯示 disabled / 占位）
- Daemon handleHistory limit cap 放寬
- Daemon 時間戳 wire 單位轉換
