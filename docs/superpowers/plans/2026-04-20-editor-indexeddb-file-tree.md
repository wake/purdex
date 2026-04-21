# Editor IndexedDB File Tree Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 讓 `Editor module` 擁有可持久化的 IndexedDB in-app file tree，並完成 `docId` identity、breadcrumb rename、recent open、delete/orphan/save 流程與 system-scope file tree view。

**Architecture:** 先把 in-app editor 從 path-based identity 轉成 `docId` 驅動，再用 `EditorPathCodec` + repository + coordinator 把 IndexedDB 與樹狀操作收斂到單一資料層。UI 端分成 editor runtime、file tree region view、new tab recent open、settings alias 四塊，最後用 migration 與 integration tests 把舊 persist 狀態接回新模型。

**Tech Stack:** React 19 / Zustand 5 / IndexedDB API / fake-indexeddb / Vitest / @testing-library/react / pnpm

**Execution shape:** 分兩個 implementation checkpoint。Checkpoint A 先完成 data model / persistence / migration；Checkpoint B 再完成 UI 與互動。不要一開始同時改 repository、tab identity、UI。

---

## 檔案結構

### 新增

```text
spa/src/lib/editor-db/
├── db.ts                          — IndexedDB schema/open helper
├── path-codec.ts                  — canonicalize/splitParent/isDescendantOf
├── tree-repository.ts             — editor_nodes CRUD
├── content-repository.ts          — editor_contents CRUD + CAS
└── __tests__/
    ├── path-codec.test.ts
    ├── tree-repository.test.ts
    └── content-repository.test.ts

spa/src/lib/editor-service/
├── coordinator.ts                 — create/rename/delete/save/save-as/recent-open
└── __tests__/
    └── coordinator.test.ts

spa/src/components/editor/
├── EditorFileTreeView.tsx         — system-scope region container
├── EditorFileTreeNode.tsx         — tree row rendering
├── EditorFileTreeCommands.tsx     — rename/create/delete command state
└── __tests__/
    ├── EditorFileTreeView.test.tsx
    ├── EditorToolbar.test.tsx
    └── EditorNewTabSection.test.tsx
```

### 修改

```text
spa/package.json                               — add fake-indexeddb dev dependency
spa/src/types/fs.ts                            — in-app doc identity types
spa/src/types/tab.ts                           — in-app editor PaneContent uses docId
spa/src/lib/fs-backend-inapp.ts                — delegate to coordinator/repositories
spa/src/lib/register-modules.tsx               — register system-scope editor file tree view and new settings section
spa/src/lib/pane-utils.ts                      — in-app singleton equality by docId
spa/src/lib/pane-utils.test.ts                 — docId equality cases
spa/src/lib/route-utils.ts                     — settings alias handling if needed
spa/src/lib/route-utils.test.ts                — /settings/editor-buffers redirect/alias
spa/src/components/SettingsPage.tsx            — canonicalize old editor-buffers route
spa/src/components/SettingsPage.test.tsx       — alias/redirect coverage
spa/src/components/editor/EditorPane.tsx       — resolvePath(docId), save/save-as/deleted state
spa/src/components/editor/EditorToolbar.tsx    — breadcrumb + rename entry
spa/src/components/editor/EditorNewTabSection.tsx — recent open list + create untitled files
spa/src/components/editor/BufferListSection.tsx — retire/replace with empty Editor settings section
spa/src/stores/useEditorStore.ts               — buffer keyed by docId + baseVersion + binding state
spa/src/stores/useEditorStore.test.ts          — docId/runtime state tests
spa/src/stores/useTabStore.ts                  — migrate legacy in-app panes, singleton behavior still works
spa/src/stores/useTabStore.migration.test.ts   — split-layout recursive migration
spa/src/components/SettingsPage.test.tsx       — settings alias
spa/src/locales/en.json / zh-TW.json           — breadcrumb/file-tree/editor-empty-copy
```

### 驗證命令

```bash
cd spa && npx vitest run src/lib/editor-db/__tests__/path-codec.test.ts
cd spa && npx vitest run src/lib/editor-db/__tests__/tree-repository.test.ts src/lib/editor-db/__tests__/content-repository.test.ts
cd spa && npx vitest run src/lib/editor-service/__tests__/coordinator.test.ts
cd spa && npx vitest run src/stores/useTabStore.migration.test.ts src/lib/pane-utils.test.ts src/stores/useEditorStore.test.ts
cd spa && npx vitest run src/components/editor/__tests__/EditorToolbar.test.tsx src/components/editor/__tests__/EditorNewTabSection.test.tsx src/components/editor/__tests__/EditorFileTreeView.test.tsx src/components/SettingsPage.test.tsx
cd spa && pnpm run lint
cd spa && pnpm run build
cd spa && npx vitest run
```

---

### Task 1: IndexedDB Foundation And Path Canonicalization

**Files:**
- Modify: `spa/package.json`
- Create: `spa/src/lib/editor-db/db.ts`
- Create: `spa/src/lib/editor-db/path-codec.ts`
- Create: `spa/src/lib/editor-db/__tests__/path-codec.test.ts`

- [ ] **Step 1: Add test-only IndexedDB dependency**

Run:
```bash
pnpm --prefix spa add -D fake-indexeddb
```

Expected: `spa/package.json` gains:

```json
{
  "devDependencies": {
    "fake-indexeddb": "^6.0.0"
  }
}
```

- [ ] **Step 2: Write failing path codec tests**

Create `spa/src/lib/editor-db/__tests__/path-codec.test.ts`:

```ts
import { describe, it, expect } from 'vitest'
import { canonicalizePath, splitParentPath, isDescendantPath } from '../path-codec'

describe('canonicalizePath', () => {
  it('normalizes duplicate slashes and strips trailing slash', () => {
    expect(canonicalizePath('//notes//daily///')).toBe('/notes/daily')
  })

  it('preserves root slash', () => {
    expect(canonicalizePath('/')).toBe('/')
  })

  it('rejects relative paths', () => {
    expect(() => canonicalizePath('notes/a.md')).toThrow(/absolute/i)
  })

  it('rejects dot segments', () => {
    expect(() => canonicalizePath('/notes/../a.md')).toThrow(/dot/i)
  })
})

describe('splitParentPath', () => {
  it('splits canonical path into parent and name', () => {
    expect(splitParentPath('/notes/a.md')).toEqual({ parentPath: '/notes', name: 'a.md' })
  })

  it('uses root as parent for top-level files', () => {
    expect(splitParentPath('/untitled.md')).toEqual({ parentPath: '/', name: 'untitled.md' })
  })
})

describe('isDescendantPath', () => {
  it('matches path segments, not raw string prefix', () => {
    expect(isDescendantPath('/notes/a.md', '/notes')).toBe(true)
    expect(isDescendantPath('/notes-old/a.md', '/notes')).toBe(false)
  })
})
```

- [ ] **Step 3: Run test to verify it fails**

Run:
```bash
cd spa && npx vitest run src/lib/editor-db/__tests__/path-codec.test.ts
```

Expected: FAIL with module-not-found for `../path-codec`

- [ ] **Step 4: Implement path codec and DB bootstrap**

Create `spa/src/lib/editor-db/path-codec.ts`:

```ts
export function canonicalizePath(input: string): string {
  if (!input.startsWith('/')) throw new Error('Path must be absolute')
  if (input === '/') return '/'
  const parts = input.split('/').filter(Boolean)
  if (parts.some((part) => part === '.' || part === '..')) {
    throw new Error('Dot segments are not allowed')
  }
  return '/' + parts.join('/')
}

export function splitParentPath(path: string): { parentPath: string; name: string } {
  const canonical = canonicalizePath(path)
  if (canonical === '/') throw new Error('Root has no parent')
  const idx = canonical.lastIndexOf('/')
  const parentPath = idx === 0 ? '/' : canonical.slice(0, idx)
  const name = canonical.slice(idx + 1)
  return { parentPath, name }
}

export function isDescendantPath(candidate: string, base: string): boolean {
  const cc = canonicalizePath(candidate)
  const cb = canonicalizePath(base)
  if (cb === '/') return cc !== '/'
  return cc === cb || cc.startsWith(`${cb}/`)
}
```

Create `spa/src/lib/editor-db/db.ts`:

```ts
export const EDITOR_DB_NAME = 'purdex-editor'
export const EDITOR_DB_VERSION = 1

export const EDITOR_NODES_STORE = 'editor_nodes'
export const EDITOR_CONTENTS_STORE = 'editor_contents'

export function openEditorDb(): Promise<IDBDatabase> {
  return new Promise((resolve, reject) => {
    const request = indexedDB.open(EDITOR_DB_NAME, EDITOR_DB_VERSION)
    request.onupgradeneeded = () => {
      const db = request.result
      const nodes = db.createObjectStore(EDITOR_NODES_STORE, { keyPath: 'id' })
      nodes.createIndex('path', 'path', { unique: true })
      nodes.createIndex('docId', 'docId', { unique: true })
      nodes.createIndex('parentPath', 'parentPath', { unique: false })

      db.createObjectStore(EDITOR_CONTENTS_STORE, { keyPath: 'docId' })
    }
    request.onsuccess = () => resolve(request.result)
    request.onerror = () => reject(request.error ?? new Error('openEditorDb failed'))
  })
}
```

- [ ] **Step 5: Run tests**

Run:
```bash
cd spa && npx vitest run src/lib/editor-db/__tests__/path-codec.test.ts
```

Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add spa/package.json spa/src/lib/editor-db/db.ts spa/src/lib/editor-db/path-codec.ts spa/src/lib/editor-db/__tests__/path-codec.test.ts
git commit -m "feat(editor): add indexeddb bootstrap and path codec"
```

---

### Task 2: Tree/Content Repositories And Coordinator

**Files:**
- Create: `spa/src/lib/editor-db/tree-repository.ts`
- Create: `spa/src/lib/editor-db/content-repository.ts`
- Create: `spa/src/lib/editor-db/__tests__/tree-repository.test.ts`
- Create: `spa/src/lib/editor-db/__tests__/content-repository.test.ts`
- Create: `spa/src/lib/editor-service/coordinator.ts`
- Create: `spa/src/lib/editor-service/__tests__/coordinator.test.ts`
- Modify: `spa/src/lib/fs-backend-inapp.ts`

- [ ] **Step 1: Write failing repository tests**

Create `spa/src/lib/editor-db/__tests__/tree-repository.test.ts`:

```ts
import 'fake-indexeddb/auto'
import { beforeEach, describe, expect, it } from 'vitest'
import { openEditorDb } from '../db'
import { EditorTreeRepository } from '../tree-repository'

describe('EditorTreeRepository', () => {
  beforeEach(async () => {
    indexedDB.deleteDatabase('purdex-editor')
  })

  it('creates a top-level file node', async () => {
    const repo = new EditorTreeRepository(await openEditorDb())
    await repo.createFileNode('/untitled.md', 'doc-1')
    const node = await repo.getNodeByDocId('doc-1')
    expect(node?.path).toBe('/untitled.md')
  })

  it('renames descendants segment-safely', async () => {
    const repo = new EditorTreeRepository(await openEditorDb())
    await repo.createFolderNode('/notes')
    await repo.createFolderNode('/notes-old')
    await repo.createFileNode('/notes/a.md', 'doc-a')
    await repo.createFileNode('/notes-old/b.md', 'doc-b')
    await repo.renameNode('/notes', '/journal')
    expect((await repo.getNodeByDocId('doc-a'))?.path).toBe('/journal/a.md')
    expect((await repo.getNodeByDocId('doc-b'))?.path).toBe('/notes-old/b.md')
  })
})
```

Create `spa/src/lib/editor-db/__tests__/content-repository.test.ts`:

```ts
import 'fake-indexeddb/auto'
import { beforeEach, describe, expect, it } from 'vitest'
import { openEditorDb } from '../db'
import { EditorContentRepository } from '../content-repository'

describe('EditorContentRepository', () => {
  beforeEach(async () => {
    indexedDB.deleteDatabase('purdex-editor')
  })

  it('increments version on successful write', async () => {
    const repo = new EditorContentRepository(await openEditorDb())
    await repo.createDocument('doc-1', 'hello', '/untitled.md')
    const first = await repo.readDocument('doc-1')
    await repo.writeDocument('doc-1', 'world', first!.version)
    const second = await repo.readDocument('doc-1')
    expect(second?.version).toBe(first!.version + 1)
  })

  it('rejects stale compare-and-swap writes', async () => {
    const repo = new EditorContentRepository(await openEditorDb())
    await repo.createDocument('doc-1', 'hello', '/untitled.md')
    await expect(repo.writeDocument('doc-1', 'oops', 999)).rejects.toThrow(/version/i)
  })
})
```

Create `spa/src/lib/editor-service/__tests__/coordinator.test.ts`:

```ts
import 'fake-indexeddb/auto'
import { beforeEach, describe, expect, it } from 'vitest'
import { createEditorCoordinator } from '../coordinator'

describe('EditorCoordinator', () => {
  beforeEach(async () => {
    indexedDB.deleteDatabase('purdex-editor')
  })

  it('creates untitled markdown files under root', async () => {
    const coordinator = await createEditorCoordinator()
    const doc = await coordinator.createFile('/untitled.md', '')
    expect(doc.path).toBe('/untitled.md')
  })

  it('rejects moving folder into its own descendant', async () => {
    const coordinator = await createEditorCoordinator()
    await coordinator.createFolder('/notes')
    await expect(coordinator.renameNode('/notes', '/notes/archive')).rejects.toThrow(/descendant/i)
  })
})
```

- [ ] **Step 2: Run tests to verify they fail**

Run:
```bash
cd spa && npx vitest run src/lib/editor-db/__tests__/tree-repository.test.ts src/lib/editor-db/__tests__/content-repository.test.ts src/lib/editor-service/__tests__/coordinator.test.ts
```

Expected: FAIL because repositories/coordinator do not exist

- [ ] **Step 3: Implement repositories and coordinator**

Create `spa/src/lib/editor-db/tree-repository.ts` with the core shape:

```ts
import { canonicalizePath, isDescendantPath, splitParentPath } from './path-codec'
import { EDITOR_NODES_STORE } from './db'

export class EditorTreeRepository {
  constructor(private db: IDBDatabase) {}

  async getNodeByPath(path: string) { /* index('path') lookup */ }
  async getNodeByDocId(docId: string) { /* index('docId') lookup */ }
  async listChildren(path: string) { /* index('parentPath') lookup */ }
  async createFileNode(path: string, docId: string) { /* put canonicalized file row */ }
  async createFolderNode(path: string) { /* put canonicalized folder row */ }
  async renameNode(fromPath: string, toPath: string) { /* transaction + segment-aware descendant rewrite */ }
  async markDeleted(path: string) { /* mark file/folder rows deleted/orphaned */ }
}
```

Create `spa/src/lib/editor-db/content-repository.ts` with the core shape:

```ts
import { EDITOR_CONTENTS_STORE } from './db'

export class EditorContentRepository {
  constructor(private db: IDBDatabase) {}

  async createDocument(docId: string, text: string, basePath: string) {
    /* { docId, text, savedAt: Date.now(), version: 1, basePath, tombstone: false } */
  }

  async readDocument(docId: string) { /* get(docId) */ }

  async writeDocument(docId: string, text: string, expectedVersion: number) {
    /* read current -> compare version -> write version + 1 or throw */
  }
}
```

Create `spa/src/lib/editor-service/coordinator.ts`:

```ts
import { openEditorDb } from '../editor-db/db'
import { canonicalizePath } from '../editor-db/path-codec'
import { EditorTreeRepository } from '../editor-db/tree-repository'
import { EditorContentRepository } from '../editor-db/content-repository'
import { generateId } from '../id'

export async function createEditorCoordinator() {
  const db = await openEditorDb()
  const tree = new EditorTreeRepository(db)
  const contents = new EditorContentRepository(db)

  return {
    async createFile(path: string, initialContent: string) {
      const docId = generateId()
      const canonical = canonicalizePath(path)
      await tree.createFileNode(canonical, docId)
      await contents.createDocument(docId, initialContent, canonical)
      return { docId, path: canonical }
    },
    async createFolder(path: string) { /* canonicalize + create folder */ },
    async resolvePath(docId: string) { /* tree.getNodeByDocId(docId) */ },
    async renameNode(fromPath: string, toPath: string) { /* guard self-descendant + transaction */ },
    async saveDocument(docId: string, text: string, expectedVersion: number) { /* resolvePath + CAS */ },
    async saveDocumentAs(docId: string, newPath: string, text: string, expectedVersion: number) { /* path conflict guard + rebind */ },
    async listRecentOpened(limit: number) { /* order by lastOpenedAt desc */ },
  }
}
```

Modify `spa/src/lib/fs-backend-inapp.ts` to stop using `Map` and delegate to a singleton coordinator:

```ts
let coordinatorPromise: ReturnType<typeof createEditorCoordinator> | null = null
function getCoordinator() {
  coordinatorPromise ??= createEditorCoordinator()
  return coordinatorPromise
}
```

Then implement `read/write/stat/list/mkdir/delete/rename` using the coordinator/repositories instead of process memory.

- [ ] **Step 4: Run repository/coordinator tests**

Run:
```bash
cd spa && npx vitest run src/lib/editor-db/__tests__/tree-repository.test.ts src/lib/editor-db/__tests__/content-repository.test.ts src/lib/editor-service/__tests__/coordinator.test.ts
```

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add spa/src/lib/editor-db spa/src/lib/editor-service spa/src/lib/fs-backend-inapp.ts
git commit -m "feat(editor): add indexeddb repositories and coordinator"
```

---

### Task 3: Switch In-App Pane Identity From filePath To docId

**Files:**
- Modify: `spa/src/types/tab.ts`
- Modify: `spa/src/lib/pane-utils.ts`
- Modify: `spa/src/lib/pane-utils.test.ts`
- Modify: `spa/src/stores/useTabStore.ts`
- Modify: `spa/src/stores/useTabStore.migration.test.ts`
- Modify: `spa/src/stores/useEditorStore.ts`
- Modify: `spa/src/stores/useEditorStore.test.ts`
- Modify: `spa/src/types/fs.ts`

- [ ] **Step 1: Write failing identity/migration tests**

Append to `spa/src/lib/pane-utils.test.ts`:

```ts
it('returns true for inapp editor panes with same docId even if filePath cache differs', () => {
  const a: PaneContent = { kind: 'editor', source: { type: 'inapp' }, docId: 'doc-1', filePath: '/old.md' }
  const b: PaneContent = { kind: 'editor', source: { type: 'inapp' }, docId: 'doc-1', filePath: '/new.md' }
  expect(contentMatches(a, b)).toBe(true)
})
```

Append to `spa/src/stores/useTabStore.migration.test.ts`:

```ts
it('recursively migrates split-layout inapp editor panes to docId identity', () => {
  const v2State = {
    tabs: {
      tab1: {
        id: 'tab1', pinned: false, locked: false, createdAt: 1000,
        layout: {
          type: 'split' as const, id: 'split1', direction: 'h' as const,
          children: [
            { type: 'leaf' as const, pane: { id: 'p1', content: { kind: 'editor', source: { type: 'inapp' }, filePath: '/untitled.md' } } },
            { type: 'leaf' as const, pane: { id: 'p2', content: { kind: 'dashboard' } } },
          ],
          sizes: [50, 50],
        },
      },
    },
    tabOrder: ['tab1'],
    activeTabId: 'tab1',
  }
  const migrated = migrateTabStore(v2State, 2)
  const editorLeaf = migrated.tabs.tab1.layout.children[0]
  expect(editorLeaf.pane.content.docId).toBeTruthy()
})
```

Append to `spa/src/stores/useEditorStore.test.ts`:

```ts
it('keeps buffers keyed by docId and preserves baseVersion', () => {
  useEditorStore.getState().openBuffer('doc-1', 'hello', 'markdown', { mtime: 1, size: 5 }, 3)
  const buf = useEditorStore.getState().buffers['doc-1']
  expect(buf.baseVersion).toBe(3)
})
```

- [ ] **Step 2: Run tests to verify they fail**

Run:
```bash
cd spa && npx vitest run src/lib/pane-utils.test.ts src/stores/useTabStore.migration.test.ts src/stores/useEditorStore.test.ts
```

Expected: FAIL because types/store signatures still assume path-only in-app editors

- [ ] **Step 3: Implement docId identity**

Update `spa/src/types/tab.ts`:

```ts
export type PaneContent =
  | { kind: 'editor'; source: { type: 'inapp' }; docId: string; filePath?: string; diff?: { against: 'saved' | string } }
  | { kind: 'editor'; source: { type: 'daemon'; hostId: string } | { type: 'local' }; filePath: string; diff?: { against: 'saved' | string } }
  // ...
```

Update `spa/src/lib/pane-utils.ts`:

```ts
if (a.kind === 'editor' && b.kind === 'editor') {
  if (a.source.type !== b.source.type) return false
  if (a.source.type === 'inapp' && b.source.type === 'inapp') {
    return a.docId === b.docId
  }
  if (a.source.type === 'daemon' && b.source.type === 'daemon') {
    return a.filePath === b.filePath && a.source.hostId === b.source.hostId
  }
  return a.filePath === b.filePath
}
```

Update `spa/src/stores/useEditorStore.ts` to add `baseVersion` and `bindingStatus`:

```ts
export interface EditorBuffer {
  content: string
  savedContent: string
  isDirty: boolean
  language: string
  cursorPosition: { line: number; column: number }
  lastStat: { mtime: number; size: number } | null
  baseVersion: number
  bindingStatus: 'active' | 'deleted' | 'orphaned'
}
```

Update `migrateTabStore` in `spa/src/stores/useTabStore.ts` to recursively rewrite in-app editor leaves:

```ts
function migrateEditorPane(content: any) {
  if (content.kind === 'editor' && content.source?.type === 'inapp' && !content.docId) {
    return { ...content, docId: `legacy:${content.filePath}` }
  }
  return content
}
```

Then call it inside `migrateLayout()` for every leaf.

- [ ] **Step 4: Run tests**

Run:
```bash
cd spa && npx vitest run src/lib/pane-utils.test.ts src/stores/useTabStore.migration.test.ts src/stores/useEditorStore.test.ts
```

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add spa/src/types/tab.ts spa/src/lib/pane-utils.ts spa/src/lib/pane-utils.test.ts spa/src/stores/useTabStore.ts spa/src/stores/useTabStore.migration.test.ts spa/src/stores/useEditorStore.ts spa/src/stores/useEditorStore.test.ts spa/src/types/fs.ts
git commit -m "feat(editor): switch inapp pane identity to docId"
```

---

### Task 4: Editor Runtime, Breadcrumb Rename, And Deleted/Orphaned Save Flow

**Files:**
- Modify: `spa/src/components/editor/EditorPane.tsx`
- Modify: `spa/src/components/editor/EditorToolbar.tsx`
- Create: `spa/src/components/editor/__tests__/EditorToolbar.test.tsx`

- [ ] **Step 1: Write failing UI/runtime tests**

Create `spa/src/components/editor/__tests__/EditorToolbar.test.tsx`:

```tsx
import { render, screen } from '@testing-library/react'
import { EditorToolbar } from '../EditorToolbar'

describe('EditorToolbar', () => {
  it('renders breadcrumb segments for absolute path', () => {
    render(
      <EditorToolbar
        filePath="/notes/daily/2026-04-20.md"
        isDirty={false}
        isMarkdown
        editorMode="raw"
        onSave={() => {}}
      />,
    )
    expect(screen.getByText('notes')).toBeTruthy()
    expect(screen.getByText('daily')).toBeTruthy()
    expect(screen.getByText('2026-04-20.md')).toBeTruthy()
  })
})
```

Add runtime behavior test near `EditorPane` tests or create one if missing:

```tsx
it('resolves latest path by docId before save', async () => {
  // mount in-app editor pane with docId
  // simulate rename in coordinator from /old.md -> /new.md
  // save should target /new.md, not stale filePath cache
})
```

- [ ] **Step 2: Run tests to verify failure**

Run:
```bash
cd spa && npx vitest run src/components/editor/__tests__/EditorToolbar.test.tsx
```

Expected: FAIL because toolbar still renders a single filename

- [ ] **Step 3: Implement breadcrumb + runtime path resolution**

Update `spa/src/components/editor/EditorToolbar.tsx`:

```tsx
function getSegments(filePath: string): string[] {
  return filePath.split('/').filter(Boolean)
}

export function EditorToolbar(/* props */) {
  const segments = getSegments(filePath)
  return (
    <div className="flex items-center justify-between px-3 py-1 border-b border-border-subtle bg-surface-secondary">
      <div className="flex items-center gap-1 min-w-0 text-xs text-text-secondary">
        <span className="shrink-0">/</span>
        {segments.map((segment, idx) => (
          <span key={`${segment}-${idx}`} className="truncate">
            {idx > 0 && <span className="mx-1 text-text-muted">/</span>}
            {segment}
          </span>
        ))}
        {isDirty && <span className="text-accent-base">●</span>}
      </div>
      {/* existing controls */}
    </div>
  )
}
```

Update `spa/src/components/editor/EditorPane.tsx`:

```tsx
const content = pane.content
if (content.kind !== 'editor') return null
const key = content.source.type === 'inapp' ? content.docId : bufferKey(content.source, content.filePath)
```

And before every in-app read/save:

```ts
const resolvedPath = source.type === 'inapp'
  ? await getCoordinator().resolvePath(docId)
  : filePath
```

For deleted/orphaned saves:

```ts
if (source.type === 'inapp' && buffer.bindingStatus !== 'active') {
  const canSaveOriginal = await getCoordinator().canSaveToOriginal(docId)
  if (!canSaveOriginal) {
    useEditorStore.getState().setBindingStatus(docId, 'orphaned')
    return
  }
}
```

- [ ] **Step 4: Run targeted tests**

Run:
```bash
cd spa && npx vitest run src/components/editor/__tests__/EditorToolbar.test.tsx src/stores/useEditorStore.test.ts
```

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add spa/src/components/editor/EditorPane.tsx spa/src/components/editor/EditorToolbar.tsx spa/src/components/editor/__tests__/EditorToolbar.test.tsx
git commit -m "feat(editor): resolve inapp docs by docId and render breadcrumbs"
```

---

### Task 5: File Tree View, New Tab Recent Open, And Settings Alias

**Files:**
- Create: `spa/src/components/editor/EditorFileTreeView.tsx`
- Create: `spa/src/components/editor/EditorFileTreeNode.tsx`
- Create: `spa/src/components/editor/EditorFileTreeCommands.tsx`
- Create: `spa/src/components/editor/__tests__/EditorFileTreeView.test.tsx`
- Create: `spa/src/components/editor/__tests__/EditorNewTabSection.test.tsx`
- Modify: `spa/src/components/editor/EditorNewTabSection.tsx`
- Modify: `spa/src/components/SettingsPage.tsx`
- Modify: `spa/src/components/SettingsPage.test.tsx`
- Modify: `spa/src/lib/register-modules.tsx`
- Modify: `spa/src/lib/route-utils.ts`
- Modify: `spa/src/lib/route-utils.test.ts`
- Modify: `spa/src/locales/en.json`
- Modify: `spa/src/locales/zh-TW.json`

- [ ] **Step 1: Write failing UI tests**

Create `spa/src/components/editor/__tests__/EditorNewTabSection.test.tsx`:

```tsx
import { render, screen } from '@testing-library/react'
import { EditorNewTabSection } from '../EditorNewTabSection'

describe('EditorNewTabSection', () => {
  it('shows recent opened docs below create actions', async () => {
    render(<EditorNewTabSection onSelect={() => {}} />)
    expect(screen.getByText(/recent/i)).toBeTruthy()
  })
})
```

Add Settings alias test to `spa/src/components/SettingsPage.test.tsx`:

```tsx
it('redirects /settings/editor-buffers to new editor section', async () => {
  const { history } = renderWithLocation('/settings/editor-buffers')
  await waitFor(() => {
    expect(history[history.length - 1]).toBe('/settings/editor')
  })
})
```

Create `spa/src/components/editor/__tests__/EditorFileTreeView.test.tsx`:

```tsx
import { render, screen } from '@testing-library/react'
import { EditorFileTreeView } from '../EditorFileTreeView'

describe('EditorFileTreeView', () => {
  it('renders a system-scope root tree', () => {
    render(<EditorFileTreeView isActive region="primary-sidebar" />)
    expect(screen.getByText('/')).toBeTruthy()
  })
})
```

- [ ] **Step 2: Run tests to verify failure**

Run:
```bash
cd spa && npx vitest run src/components/editor/__tests__/EditorNewTabSection.test.tsx src/components/editor/__tests__/EditorFileTreeView.test.tsx src/components/SettingsPage.test.tsx
```

Expected: FAIL because recent list / file tree view / alias are not implemented

- [ ] **Step 3: Implement system-scope editor UI**

Create `spa/src/components/editor/EditorFileTreeView.tsx`:

```tsx
import { useEffect, useState } from 'react'
import type { ViewProps } from '../../lib/module-registry'
import { getEditorCoordinator } from '../../lib/editor-service/coordinator'
import { EditorFileTreeNode } from './EditorFileTreeNode'
import { EditorFileTreeCommands } from './EditorFileTreeCommands'

export function EditorFileTreeView({ isActive }: ViewProps) {
  const [nodes, setNodes] = useState([])
  useEffect(() => {
    if (!isActive) return
    void getEditorCoordinator().listChildren('/').then(setNodes)
  }, [isActive])
  return (
    <div className="flex h-full flex-col">
      <EditorFileTreeCommands rootPath="/" />
      <div className="flex-1 overflow-auto">
        <div className="px-2 py-1 text-xs text-text-muted">/</div>
        {nodes.map((node) => <EditorFileTreeNode key={node.id} node={node} depth={0} />)}
      </div>
    </div>
  )
}
```

Update `spa/src/components/editor/EditorNewTabSection.tsx`:

```tsx
const [recent, setRecent] = useState<Array<{ docId: string; path: string }>>([])
useEffect(() => {
  void getEditorCoordinator().listRecentOpened(8).then(setRecent)
}, [])
```

Render:

```tsx
<div className="mt-4 space-y-1">
  <div className="text-xs text-text-muted">Recent</div>
  {recent.map((doc) => (
    <button key={doc.docId} onClick={() => onSelect({ kind: 'editor', source: { type: 'inapp' }, docId: doc.docId, filePath: doc.path })}>
      {doc.path}
    </button>
  ))}
</div>
```

Update `spa/src/lib/register-modules.tsx`:

```tsx
registerModule({
  id: 'editor',
  name: 'Editor',
  panes: [/* existing panes */],
  views: [
    {
      id: 'editor-file-tree',
      label: 'editor.file_tree',
      icon: FolderOpen,
      scope: 'system',
      component: EditorFileTreeView,
    },
  ],
})

registerSettingsSection({ id: 'editor', label: 'settings.section.editor', order: 9, component: BufferListSection })
```

Update `spa/src/components/SettingsPage.tsx` or `route-utils.ts` to self-heal old alias:

```ts
if (urlSection === 'editor-buffers') {
  setLocation('/settings/editor', { replace: true })
  return
}
```

- [ ] **Step 4: Run targeted tests**

Run:
```bash
cd spa && npx vitest run src/components/editor/__tests__/EditorNewTabSection.test.tsx src/components/editor/__tests__/EditorFileTreeView.test.tsx src/components/SettingsPage.test.tsx src/lib/route-utils.test.ts
```

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add spa/src/components/editor/EditorFileTreeView.tsx spa/src/components/editor/EditorFileTreeNode.tsx spa/src/components/editor/EditorFileTreeCommands.tsx spa/src/components/editor/__tests__/EditorFileTreeView.test.tsx spa/src/components/editor/__tests__/EditorNewTabSection.test.tsx spa/src/components/editor/EditorNewTabSection.tsx spa/src/components/SettingsPage.tsx spa/src/components/SettingsPage.test.tsx spa/src/lib/register-modules.tsx spa/src/lib/route-utils.ts spa/src/lib/route-utils.test.ts spa/src/locales/en.json spa/src/locales/zh-TW.json
git commit -m "feat(editor): add file tree view recent opens and settings alias"
```

---

### Task 6: Regression Verification And Full Suite

**Files:**
- Modify: `spa/src/components/editor/BufferListSection.tsx`
- Modify: `spa/src/lib/register-modules.test.ts`
- Modify: `spa/src/components/editor/EditorPane.tsx` (only if final save-state polish remains)

- [ ] **Step 1: Replace old buffer-settings content with editor empty state**

Update `spa/src/components/editor/BufferListSection.tsx` to stop pretending settings is file management:

```tsx
export function BufferListSection() {
  return (
    <div className="space-y-2">
      <h2 className="text-sm font-medium text-text-primary">Editor</h2>
      <p className="text-xs text-text-muted">
        Editor preferences will appear here in a later phase.
      </p>
    </div>
  )
}
```

- [ ] **Step 2: Add module registration regression test**

Append to `spa/src/lib/register-modules.test.ts`:

```ts
it('registers editor system view and editor settings section', () => {
  registerBuiltinModules()
  const editorModule = getModule('editor')
  expect(editorModule?.views?.some((view) => view.id === 'editor-file-tree')).toBe(true)
  expect(getSettingsSections().some((section) => section.id === 'editor')).toBe(true)
})
```

- [ ] **Step 3: Run focused regression commands**

Run:
```bash
cd spa && npx vitest run src/lib/register-modules.test.ts src/stores/useTabStore.migration.test.ts src/lib/pane-utils.test.ts src/stores/useEditorStore.test.ts src/components/SettingsPage.test.tsx
```

Expected: PASS

- [ ] **Step 4: Run lint, build, full test suite**

Run:
```bash
cd spa && pnpm run lint
cd spa && pnpm run build
cd spa && npx vitest run
```

Expected:
- `pnpm run lint`: exit 0
- `pnpm run build`: exit 0
- `npx vitest run`: all tests pass

- [ ] **Step 5: Commit**

```bash
git add spa/src/components/editor/BufferListSection.tsx spa/src/lib/register-modules.test.ts
git commit -m "test(editor): verify indexeddb file tree integration"
```

---

## Self-Review

### Spec coverage

- IndexedDB persistence: Task 1-2
- `docId` identity + singleton equality + migration: Task 3
- breadcrumb rename + runtime path resolution + deleted/orphaned save semantics: Task 4
- system-scope file tree + recent open + settings alias: Task 5
- final verification and settings empty state: Task 6

No spec section is left without a task. Sync contributor implementation intentionally remains out of scope.

### Placeholder scan

- No `TODO` / `TBD`
- All commands are concrete
- Each task lists exact files and at least one concrete test or implementation snippet

### Type consistency

- In-app editor identity is always `docId`
- `resolvePath(docId)` is the only authoritative path lookup in runtime code
- `expectedVersion` is used consistently for CAS writes

