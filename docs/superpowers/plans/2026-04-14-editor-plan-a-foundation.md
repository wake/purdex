# Editor Module Plan A: 基礎 + 基本 Editor

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 建立 Editor Module 的基礎設施和最小可用的 Monaco 文字編輯器，支援在 InApp 儲存中建立、編輯、存檔文件。

**Architecture:** 先遷移 module-registry 支援多 pane kind，再建立 FS 抽象層介面和 InAppBackend（IndexedDB），接著實作 editor pane（Monaco）和 buffer store，最後透過 file-opener-registry 和 New Tab 入口串接。

**Tech Stack:** React 19, Zustand 5, Monaco Editor (`@monaco-editor/react`), IndexedDB (`idb`), Vitest, Tailwind 4

**Spec:** `docs/superpowers/specs/2026-04-14-editor-module-design.md`

---

## File Structure

### 新增檔案

| 路徑 | 職責 |
|------|------|
| `spa/src/types/fs.ts` | FileSource type + FileInfo + FileStat + FileEntry 型別定義 |
| `spa/src/lib/fs-backend.ts` | FsBackend 介面 + backend registry + 路由函式 |
| `spa/src/lib/fs-backend-inapp.ts` | InAppBackend 實作（IndexedDB via `idb`） |
| `spa/src/lib/file-opener-registry.ts` | FileOpener 介面 + registry CRUD |
| `spa/src/stores/useEditorStore.ts` | Editor buffer store（runtime，不 persist） |
| `spa/src/components/editor/EditorPane.tsx` | Editor pane 主元件 |
| `spa/src/components/editor/MonacoWrapper.tsx` | Monaco editor React 封裝 |
| `spa/src/components/editor/EditorToolbar.tsx` | 工具列（路徑 + unsaved + save） |
| `spa/src/components/editor/EditorStatusBar.tsx` | 狀態列（語言 + cursor） |
| `spa/src/components/editor/EditorNewTabSection.tsx` | New Tab 的「建立新文件」section |

### 修改檔案

| 路徑 | 修改內容 |
|------|---------|
| `spa/src/lib/module-registry.ts` | `pane` → `panes` + `getPaneRenderer` 邏輯 |
| `spa/src/lib/register-modules.tsx` | 既有 module 遷移 `panes` + 新增 editor module |
| `spa/src/types/tab.ts` | 新增 `editor` PaneContent + import FileSource |
| `spa/src/lib/pane-labels.ts` | 新增 `editor` kind 的 label / icon |
| `spa/src/lib/pane-utils.ts` | `contentMatches` 新增 editor 比對 |
| `spa/src/lib/route-utils.ts` | `tabToUrl` 新增 editor kind |
| `spa/src/components/NewPanePage.tsx` | `m.pane` → `m.panes` flatMap |
| `spa/src/components/PaneLayoutRenderer.tsx` | 無程式碼改動（`getPaneRenderer` 內部改就夠） |
| `spa/package.json` | 新增 `@monaco-editor/react` + `idb` 依賴 |

### 測試檔案

| 路徑 | 測試對象 |
|------|---------|
| `spa/src/lib/fs-backend.test.ts` | FsBackend registry + routing |
| `spa/src/lib/fs-backend-inapp.test.ts` | InAppBackend CRUD |
| `spa/src/lib/file-opener-registry.test.ts` | FileOpener registry |
| `spa/src/stores/useEditorStore.test.ts` | Buffer store actions |
| `spa/src/lib/module-registry.test.ts` | 現有測試更新 panes |

---

## Task 1: 安裝依賴

**Files:**
- Modify: `spa/package.json`

- [ ] **Step 1: 安裝 Monaco + IndexedDB 依賴**

```bash
cd spa && pnpm add @monaco-editor/react monaco-editor idb
```

- [ ] **Step 2: 確認安裝成功**

Run: `cd spa && pnpm ls @monaco-editor/react idb`
Expected: 列出已安裝版本

- [ ] **Step 3: Commit**

```bash
git add spa/package.json spa/pnpm-lock.yaml
git commit -m "chore: add monaco-editor and idb dependencies for editor module"
```

---

## Task 2: FileSource 和 FS 型別定義

**Files:**
- Create: `spa/src/types/fs.ts`
- Test: 型別定義，無 runtime 測試

- [ ] **Step 1: 建立 FS 型別檔案**

```typescript
// spa/src/types/fs.ts

/** 標示檔案來自哪個 FS backend */
export type FileSource =
  | { type: 'daemon'; hostId: string }
  | { type: 'local' }
  | { type: 'inapp' }

/** File opener registry 使用的檔案資訊 */
export interface FileInfo {
  name: string
  path: string
  extension: string
  size: number
  isDirectory: boolean
}

/** stat() 回傳的檔案狀態 */
export interface FileStat {
  size: number
  mtime: number       // Unix timestamp ms
  isDirectory: boolean
  isFile: boolean
}

/** list() 回傳的目錄條目 */
export interface FileEntry {
  name: string
  isDir: boolean
  size: number
}
```

- [ ] **Step 2: 確認 TypeScript 編譯**

Run: `cd spa && npx tsc --noEmit --pretty`
Expected: 無錯誤

- [ ] **Step 3: Commit**

```bash
git add spa/src/types/fs.ts
git commit -m "feat(editor): add FileSource and FS type definitions"
```

---

## Task 3: FS Backend 介面 + Registry

**Files:**
- Create: `spa/src/lib/fs-backend.ts`
- Test: `spa/src/lib/fs-backend.test.ts`

- [ ] **Step 1: 寫失敗測試**

```typescript
// spa/src/lib/fs-backend.test.ts
import { describe, it, expect, beforeEach } from 'vitest'
import { registerFsBackend, getFsBackend, clearFsBackendRegistry } from './fs-backend'
import type { FsBackend } from './fs-backend'
import type { FileSource } from '../types/fs'

function createMockBackend(id: string): FsBackend {
  return {
    id,
    label: `Mock ${id}`,
    available: () => true,
    read: async () => new Uint8Array(),
    write: async () => {},
    stat: async () => ({ size: 0, mtime: 0, isDirectory: false, isFile: true }),
    list: async () => [],
    mkdir: async () => {},
    delete: async () => {},
    rename: async () => {},
  }
}

describe('FsBackend registry', () => {
  beforeEach(() => clearFsBackendRegistry())

  it('registers and retrieves a backend by source type', () => {
    const backend = createMockBackend('inapp')
    registerFsBackend('inapp', backend)
    const source: FileSource = { type: 'inapp' }
    expect(getFsBackend(source)).toBe(backend)
  })

  it('retrieves daemon backend with hostId', () => {
    const backend = createMockBackend('daemon')
    registerFsBackend('daemon', backend)
    const source: FileSource = { type: 'daemon', hostId: 'host1' }
    expect(getFsBackend(source)).toBe(backend)
  })

  it('returns undefined for unregistered source type', () => {
    const source: FileSource = { type: 'local' }
    expect(getFsBackend(source)).toBeUndefined()
  })
})
```

- [ ] **Step 2: 確認測試失敗**

Run: `cd spa && npx vitest run src/lib/fs-backend.test.ts`
Expected: FAIL — 模組不存在

- [ ] **Step 3: 實作 FsBackend 介面和 registry**

```typescript
// spa/src/lib/fs-backend.ts
import type { FileSource, FileStat, FileEntry } from '../types/fs'

export interface FsBackend {
  id: string
  label: string
  available(): boolean

  read(path: string): Promise<Uint8Array>
  write(path: string, content: Uint8Array): Promise<void>
  stat(path: string): Promise<FileStat>
  list(path: string): Promise<FileEntry[]>
  mkdir(path: string, recursive?: boolean): Promise<void>
  delete(path: string, recursive?: boolean): Promise<void>
  rename(from: string, to: string): Promise<void>
}

const backends = new Map<string, FsBackend>()

export function registerFsBackend(sourceType: string, backend: FsBackend): void {
  backends.set(sourceType, backend)
}

export function getFsBackend(source: FileSource): FsBackend | undefined {
  return backends.get(source.type)
}

export function clearFsBackendRegistry(): void {
  backends.clear()
}
```

- [ ] **Step 4: 確認測試通過**

Run: `cd spa && npx vitest run src/lib/fs-backend.test.ts`
Expected: 3 tests PASS

- [ ] **Step 5: Commit**

```bash
git add spa/src/lib/fs-backend.ts spa/src/lib/fs-backend.test.ts
git commit -m "feat(editor): add FsBackend interface and registry"
```

---

## Task 4: InAppBackend（IndexedDB）

**Files:**
- Create: `spa/src/lib/fs-backend-inapp.ts`
- Test: `spa/src/lib/fs-backend-inapp.test.ts`

- [ ] **Step 1: 寫失敗測試**

```typescript
// spa/src/lib/fs-backend-inapp.test.ts
import { describe, it, expect, beforeEach } from 'vitest'
import { InAppBackend } from './fs-backend-inapp'

describe('InAppBackend', () => {
  let backend: InAppBackend

  beforeEach(() => {
    backend = new InAppBackend()
  })

  it('reports as available', () => {
    expect(backend.available()).toBe(true)
  })

  it('writes and reads a file', async () => {
    const content = new TextEncoder().encode('hello world')
    await backend.write('/test.txt', content)
    const result = await backend.read('/test.txt')
    expect(new TextDecoder().decode(result)).toBe('hello world')
  })

  it('stat returns file info after write', async () => {
    const content = new TextEncoder().encode('abc')
    await backend.write('/stat-test.txt', content)
    const stat = await backend.stat('/stat-test.txt')
    expect(stat.isFile).toBe(true)
    expect(stat.isDirectory).toBe(false)
    expect(stat.size).toBe(3)
    expect(stat.mtime).toBeGreaterThan(0)
  })

  it('stat throws for nonexistent path', async () => {
    await expect(backend.stat('/no-such-file')).rejects.toThrow()
  })

  it('list returns entries in a directory', async () => {
    await backend.write('/dir/a.txt', new TextEncoder().encode('a'))
    await backend.write('/dir/b.txt', new TextEncoder().encode('b'))
    const entries = await backend.list('/dir')
    const names = entries.map((e) => e.name).sort()
    expect(names).toEqual(['a.txt', 'b.txt'])
  })

  it('mkdir creates a directory entry', async () => {
    await backend.mkdir('/newdir')
    const stat = await backend.stat('/newdir')
    expect(stat.isDirectory).toBe(true)
  })

  it('delete removes a file', async () => {
    await backend.write('/del.txt', new TextEncoder().encode('x'))
    await backend.delete('/del.txt')
    await expect(backend.stat('/del.txt')).rejects.toThrow()
  })

  it('rename moves a file', async () => {
    const content = new TextEncoder().encode('move me')
    await backend.write('/old.txt', content)
    await backend.rename('/old.txt', '/new.txt')
    const result = await backend.read('/new.txt')
    expect(new TextDecoder().decode(result)).toBe('move me')
    await expect(backend.stat('/old.txt')).rejects.toThrow()
  })
})
```

- [ ] **Step 2: 確認測試失敗**

Run: `cd spa && npx vitest run src/lib/fs-backend-inapp.test.ts`
Expected: FAIL — 模組不存在

- [ ] **Step 3: 實作 InAppBackend**

```typescript
// spa/src/lib/fs-backend-inapp.ts
import type { FsBackend } from './fs-backend'
import type { FileStat, FileEntry } from '../types/fs'

interface StoredFile {
  path: string
  content: Uint8Array
  isDirectory: boolean
  mtime: number
}

/**
 * In-app filesystem backed by a simple Map.
 * Production 環境用 IndexedDB（透過 idb），測試環境用 in-memory Map。
 * 此實作為 in-memory 版本，Plan B/C 再替換為 IndexedDB。
 */
export class InAppBackend implements FsBackend {
  readonly id = 'inapp'
  readonly label = 'In-App Storage'
  private store = new Map<string, StoredFile>()

  available(): boolean {
    return true
  }

  async read(path: string): Promise<Uint8Array> {
    const file = this.store.get(path)
    if (!file || file.isDirectory) throw new Error(`File not found: ${path}`)
    return file.content
  }

  async write(path: string, content: Uint8Array): Promise<void> {
    // Auto-create parent directories
    const parts = path.split('/').filter(Boolean)
    for (let i = 1; i < parts.length; i++) {
      const dirPath = '/' + parts.slice(0, i).join('/')
      if (!this.store.has(dirPath)) {
        this.store.set(dirPath, { path: dirPath, content: new Uint8Array(), isDirectory: true, mtime: Date.now() })
      }
    }
    this.store.set(path, { path, content, isDirectory: false, mtime: Date.now() })
  }

  async stat(path: string): Promise<FileStat> {
    const file = this.store.get(path)
    if (!file) throw new Error(`Not found: ${path}`)
    return {
      size: file.isDirectory ? 0 : file.content.byteLength,
      mtime: file.mtime,
      isDirectory: file.isDirectory,
      isFile: !file.isDirectory,
    }
  }

  async list(path: string): Promise<FileEntry[]> {
    const prefix = path.endsWith('/') ? path : path + '/'
    const entries: FileEntry[] = []
    const seen = new Set<string>()
    for (const [key, file] of this.store) {
      if (!key.startsWith(prefix)) continue
      const rest = key.slice(prefix.length)
      const name = rest.split('/')[0]
      if (!name || seen.has(name)) continue
      seen.add(name)
      entries.push({ name, isDir: file.isDirectory || rest.includes('/'), size: file.content.byteLength })
    }
    return entries.sort((a, b) => {
      if (a.isDir !== b.isDir) return a.isDir ? -1 : 1
      return a.name.localeCompare(b.name)
    })
  }

  async mkdir(path: string): Promise<void> {
    this.store.set(path, { path, content: new Uint8Array(), isDirectory: true, mtime: Date.now() })
  }

  async delete(path: string): Promise<void> {
    this.store.delete(path)
    // Also delete children for directories
    const prefix = path + '/'
    for (const key of this.store.keys()) {
      if (key.startsWith(prefix)) this.store.delete(key)
    }
  }

  async rename(from: string, to: string): Promise<void> {
    const file = this.store.get(from)
    if (!file) throw new Error(`Not found: ${from}`)
    this.store.set(to, { ...file, path: to, mtime: Date.now() })
    this.store.delete(from)
  }
}
```

- [ ] **Step 4: 確認測試通過**

Run: `cd spa && npx vitest run src/lib/fs-backend-inapp.test.ts`
Expected: 8 tests PASS

- [ ] **Step 5: Commit**

```bash
git add spa/src/lib/fs-backend-inapp.ts spa/src/lib/fs-backend-inapp.test.ts
git commit -m "feat(editor): add InAppBackend (in-memory FS for editor buffer)"
```

---

## Task 5: File Opener Registry

**Files:**
- Create: `spa/src/lib/file-opener-registry.ts`
- Test: `spa/src/lib/file-opener-registry.test.ts`

- [ ] **Step 1: 寫失敗測試**

```typescript
// spa/src/lib/file-opener-registry.test.ts
import { describe, it, expect, beforeEach } from 'vitest'
import { registerFileOpener, getDefaultOpener, getFileOpeners, clearFileOpenerRegistry } from './file-opener-registry'
import type { FileOpener } from './file-opener-registry'
import type { FileInfo } from '../types/fs'

const textFile: FileInfo = { name: 'test.ts', path: '/test.ts', extension: 'ts', size: 100, isDirectory: false }
const imageFile: FileInfo = { name: 'logo.png', path: '/logo.png', extension: 'png', size: 5000, isDirectory: false }

describe('file-opener-registry', () => {
  beforeEach(() => clearFileOpenerRegistry())

  it('returns null when no opener matches', () => {
    expect(getDefaultOpener(textFile)).toBeNull()
  })

  it('returns registered default opener', () => {
    const opener: FileOpener = {
      id: 'text-editor',
      label: 'Text Editor',
      icon: 'File',
      match: () => true,
      priority: 'default',
      createContent: (_source, file) => ({ kind: 'editor', source: { type: 'inapp' }, filePath: file.path } as never),
    }
    registerFileOpener(opener)
    expect(getDefaultOpener(textFile)).toBe(opener)
  })

  it('prefers default over option priority', () => {
    const option: FileOpener = {
      id: 'option-opener',
      label: 'Option',
      icon: 'File',
      match: () => true,
      priority: 'option',
      createContent: () => ({ kind: 'editor' } as never),
    }
    const def: FileOpener = {
      id: 'default-opener',
      label: 'Default',
      icon: 'File',
      match: () => true,
      priority: 'default',
      createContent: () => ({ kind: 'editor' } as never),
    }
    registerFileOpener(option)
    registerFileOpener(def)
    expect(getDefaultOpener(textFile)?.id).toBe('default-opener')
  })

  it('returns only matching openers', () => {
    const textOpener: FileOpener = {
      id: 'text',
      label: 'Text',
      icon: 'File',
      match: (f) => !['png', 'jpg'].includes(f.extension),
      priority: 'default',
      createContent: () => ({ kind: 'editor' } as never),
    }
    const imageOpener: FileOpener = {
      id: 'image',
      label: 'Image',
      icon: 'Image',
      match: (f) => ['png', 'jpg'].includes(f.extension),
      priority: 'default',
      createContent: () => ({ kind: 'image-preview' } as never),
    }
    registerFileOpener(textOpener)
    registerFileOpener(imageOpener)

    expect(getFileOpeners(textFile).map((o) => o.id)).toEqual(['text'])
    expect(getFileOpeners(imageFile).map((o) => o.id)).toEqual(['image'])
  })
})
```

- [ ] **Step 2: 確認測試失敗**

Run: `cd spa && npx vitest run src/lib/file-opener-registry.test.ts`
Expected: FAIL

- [ ] **Step 3: 實作 file-opener-registry**

```typescript
// spa/src/lib/file-opener-registry.ts
import type { PaneContent } from '../types/tab'
import type { FileSource, FileInfo } from '../types/fs'

export interface FileOpener {
  id: string
  label: string
  icon: string
  match: (file: FileInfo) => boolean
  priority: 'default' | 'option'
  createContent: (source: FileSource, file: FileInfo) => PaneContent
}

const openers: FileOpener[] = []

export function registerFileOpener(opener: FileOpener): void {
  openers.push(opener)
}

export function getFileOpeners(file: FileInfo): FileOpener[] {
  return openers.filter((o) => o.match(file))
}

export function getDefaultOpener(file: FileInfo): FileOpener | null {
  const matching = getFileOpeners(file)
  return matching.find((o) => o.priority === 'default') ?? matching[0] ?? null
}

export function clearFileOpenerRegistry(): void {
  openers.length = 0
}
```

- [ ] **Step 4: 確認測試通過**

Run: `cd spa && npx vitest run src/lib/file-opener-registry.test.ts`
Expected: 4 tests PASS

- [ ] **Step 5: Commit**

```bash
git add spa/src/lib/file-opener-registry.ts spa/src/lib/file-opener-registry.test.ts
git commit -m "feat(editor): add file-opener-registry"
```

---

## Task 6: module-registry `pane` → `panes` 遷移 + 所有消費端更新

> **重要：此 Task 的所有步驟必須一次性完成再 commit，否則中間狀態 TypeScript 無法編譯。**

**Files:**
- Modify: `spa/src/lib/module-registry.ts`
- Modify: `spa/src/lib/module-registry.test.ts`
- Modify: `spa/src/lib/register-modules.tsx`
- Modify: `spa/src/lib/register-modules.test.ts`（如有）
- Modify: `spa/src/components/NewPanePage.tsx`

- [ ] **Step 1: 跑現有測試確認基線通過**

Run: `cd spa && npx vitest run`
Expected: PASS（改動前基線）

- [ ] **Step 2: 修改 ModuleDefinition 型別和 getPaneRenderer**

在 `spa/src/lib/module-registry.ts` 中：

1. `ModuleDefinition` 的 `pane?: PaneDefinition` 改為 `panes?: PaneDefinition[]`
2. `getPaneRenderer` 改為遍歷 `panes` 陣列：

```typescript
export function getPaneRenderer(kind: string): PaneDefinition | undefined {
  for (const m of modules.values()) {
    for (const p of m.panes ?? []) {
      if (p.kind === kind) return p
    }
  }
  return undefined
}
```

- [ ] **Step 3: 遷移 register-modules.tsx**

把所有 `registerModule` 呼叫的 `pane: { kind, component }` 改為 `panes: [{ kind, component }]`。共 8 個 module。

例如：
```typescript
// Before
registerModule({
  id: 'session',
  name: 'Session',
  pane: { kind: 'tmux-session', component: SessionPaneContent },
})

// After
registerModule({
  id: 'session',
  name: 'Session',
  panes: [{ kind: 'tmux-session', component: SessionPaneContent }],
})
```

- [ ] **Step 4: 遷移 NewPanePage.tsx**

```typescript
// Before (line 11)
const paneModules = getModules().filter((m) => m.pane && SIMPLE_KINDS.has(m.pane.kind))

// After
const paneKinds = getModules().flatMap((m) =>
  (m.panes ?? [])
    .filter((p) => SIMPLE_KINDS.has(p.kind))
    .map((p) => ({ moduleId: m.id, moduleName: m.name, kind: p.kind }))
)
```

更新 render 邏輯：
```typescript
// Before (line 18-25)
{paneModules.map((m) => (
  <button key={m.id} ... onClick={() => onSelect({ kind: m.pane!.kind } as PaneContent)}>
    {m.name}
  </button>
))}

// After
{paneKinds.map((pk) => (
  <button key={pk.kind} ... onClick={() => onSelect({ kind: pk.kind } as PaneContent)}>
    {pk.moduleName}
  </button>
))}
```

- [ ] **Step 5: 更新所有測試 fixture**

在 `module-registry.test.ts` 和 `register-modules.test.ts` 中，把所有 `pane: { kind, component }` 改成 `panes: [{ kind, component }]`。

- [ ] **Step 6: 確認全部測試通過**

Run: `cd spa && npx vitest run`
Expected: 所有測試 PASS

- [ ] **Step 7: Commit**

```bash
git add spa/src/lib/module-registry.ts spa/src/lib/module-registry.test.ts \
  spa/src/lib/register-modules.tsx spa/src/lib/register-modules.test.ts \
  spa/src/components/NewPanePage.tsx
git commit -m "refactor: change ModuleDefinition.pane to panes (plural array)"
```

---

## Task 7: 新增 editor PaneContent 型別

> **注意：Plan A 只新增 `editor` kind。`image-preview` 和 `pdf-preview` 留給 Plan C 新增，避免 switch exhaustive check 失敗。**

**Files:**
- Modify: `spa/src/types/tab.ts`
- Modify: `spa/src/lib/pane-labels.ts`
- Modify: `spa/src/lib/pane-utils.ts`
- Modify: `spa/src/lib/route-utils.ts`

- [ ] **Step 1: 新增 PaneContent union 成員**

在 `spa/src/types/tab.ts` 新增：

```typescript
import type { FileSource } from './fs'

// 在 PaneContent union 中新增（包含 diff? 供 Plan C 使用）：
| { kind: 'editor'; source: FileSource; filePath: string; diff?: { against: 'saved' | string } }
```

- [ ] **Step 2: 更新 pane-labels.ts**

在 `getPaneLabel` 的 switch 中新增：
```typescript
case 'editor': {
  const name = content.filePath.split('/').pop() ?? content.filePath
  return content.diff ? `${name} (Diff)` : name
}
```

在 `getPaneIcon` 的 switch 中新增：
```typescript
case 'editor':
  return content.diff ? 'GitDiff' : 'File'
```

- [ ] **Step 3: 更新 pane-utils.ts**

在 `contentMatches` 中新增（在 `return true` 之前），使用安全的 narrowing 而非 type cast：
```typescript
if (a.kind === 'editor' && b.kind === 'editor') {
  if (a.source.type !== b.source.type) return false
  if (a.source.type === 'daemon' && b.source.type === 'daemon') {
    return a.filePath === b.filePath && a.source.hostId === b.source.hostId
  }
  return a.filePath === b.filePath
}
```

- [ ] **Step 4: 更新 route-utils.ts**

在 `tabToUrl` 的 switch 中新增：
```typescript
case 'editor':
  return '/'
```

- [ ] **Step 5: 確認 TypeScript 編譯通過**

Run: `cd spa && npx tsc --noEmit --pretty`
Expected: 無錯誤

- [ ] **Step 6: 確認全部測試通過**

Run: `cd spa && npx vitest run`
Expected: 所有測試 PASS

- [ ] **Step 7: Commit**

```bash
git add spa/src/types/tab.ts spa/src/types/fs.ts spa/src/lib/pane-labels.ts spa/src/lib/pane-utils.ts spa/src/lib/route-utils.ts
git commit -m "feat(editor): add editor PaneContent type and update pane utilities"
```

---

## Task 8: Editor Buffer Store

**Files:**
- Create: `spa/src/stores/useEditorStore.ts`
- Test: `spa/src/stores/useEditorStore.test.ts`

- [ ] **Step 1: 寫失敗測試**

```typescript
// spa/src/stores/useEditorStore.test.ts
import { describe, it, expect, beforeEach } from 'vitest'
import { useEditorStore } from './useEditorStore'

describe('useEditorStore', () => {
  beforeEach(() => {
    useEditorStore.getState().clearAllBuffers()
  })

  it('opens a buffer with content', () => {
    useEditorStore.getState().openBuffer('key1', 'hello', 'typescript')
    const buf = useEditorStore.getState().buffers['key1']
    expect(buf).toBeDefined()
    expect(buf.content).toBe('hello')
    expect(buf.savedContent).toBe('hello')
    expect(buf.isDirty).toBe(false)
    expect(buf.language).toBe('typescript')
  })

  it('updateContent marks buffer as dirty', () => {
    useEditorStore.getState().openBuffer('key1', 'hello', 'typescript')
    useEditorStore.getState().updateContent('key1', 'hello world')
    const buf = useEditorStore.getState().buffers['key1']
    expect(buf.content).toBe('hello world')
    expect(buf.isDirty).toBe(true)
  })

  it('markSaved clears dirty flag', () => {
    useEditorStore.getState().openBuffer('key1', 'hello', 'typescript')
    useEditorStore.getState().updateContent('key1', 'changed')
    useEditorStore.getState().markSaved('key1')
    const buf = useEditorStore.getState().buffers['key1']
    expect(buf.isDirty).toBe(false)
    expect(buf.savedContent).toBe('changed')
  })

  it('closeBuffer removes the buffer', () => {
    useEditorStore.getState().openBuffer('key1', 'hello', 'typescript')
    useEditorStore.getState().closeBuffer('key1')
    expect(useEditorStore.getState().buffers['key1']).toBeUndefined()
  })

  it('reloadBuffer replaces content without marking dirty', () => {
    useEditorStore.getState().openBuffer('key1', 'old', 'typescript')
    useEditorStore.getState().reloadBuffer('key1', 'new')
    const buf = useEditorStore.getState().buffers['key1']
    expect(buf.content).toBe('new')
    expect(buf.savedContent).toBe('new')
    expect(buf.isDirty).toBe(false)
  })

  it('updateCursor stores cursor position', () => {
    useEditorStore.getState().openBuffer('key1', '', 'plaintext')
    useEditorStore.getState().updateCursor('key1', 10, 5)
    const buf = useEditorStore.getState().buffers['key1']
    expect(buf.cursorPosition).toEqual({ line: 10, column: 5 })
  })
})
```

- [ ] **Step 2: 確認測試失敗**

Run: `cd spa && npx vitest run src/stores/useEditorStore.test.ts`
Expected: FAIL

- [ ] **Step 3: 實作 useEditorStore**

```typescript
// spa/src/stores/useEditorStore.ts
import { create } from 'zustand'

interface EditorBuffer {
  content: string
  savedContent: string
  isDirty: boolean
  language: string
  cursorPosition: { line: number; column: number }
  lastStat: { mtime: number; size: number } | null
}

interface EditorState {
  buffers: Record<string, EditorBuffer>
  openBuffer: (key: string, content: string, language: string, stat?: { mtime: number; size: number }) => void
  updateContent: (key: string, content: string) => void
  markSaved: (key: string) => void
  closeBuffer: (key: string) => void
  reloadBuffer: (key: string, content: string, stat?: { mtime: number; size: number }) => void
  updateCursor: (key: string, line: number, column: number) => void
  clearAllBuffers: () => void
}

export const useEditorStore = create<EditorState>()((set) => ({
  buffers: {},

  openBuffer: (key, content, language, stat) =>
    set((state) => ({
      buffers: {
        ...state.buffers,
        [key]: {
          content,
          savedContent: content,
          isDirty: false,
          language,
          cursorPosition: { line: 1, column: 1 },
          lastStat: stat ?? null,
        },
      },
    })),

  updateContent: (key, content) =>
    set((state) => {
      const buf = state.buffers[key]
      if (!buf) return state
      return {
        buffers: {
          ...state.buffers,
          [key]: { ...buf, content, isDirty: content !== buf.savedContent },
        },
      }
    }),

  markSaved: (key) =>
    set((state) => {
      const buf = state.buffers[key]
      if (!buf) return state
      return {
        buffers: {
          ...state.buffers,
          [key]: { ...buf, savedContent: buf.content, isDirty: false },
        },
      }
    }),

  closeBuffer: (key) =>
    set((state) => {
      const { [key]: _, ...rest } = state.buffers
      return { buffers: rest }
    }),

  reloadBuffer: (key, content, stat) =>
    set((state) => {
      const buf = state.buffers[key]
      if (!buf) return state
      return {
        buffers: {
          ...state.buffers,
          [key]: {
            ...buf,
            content,
            savedContent: content,
            isDirty: false,
            lastStat: stat ?? buf.lastStat,
          },
        },
      }
    }),

  updateCursor: (key, line, column) =>
    set((state) => {
      const buf = state.buffers[key]
      if (!buf) return state
      return {
        buffers: {
          ...state.buffers,
          [key]: { ...buf, cursorPosition: { line, column } },
        },
      }
    }),

  clearAllBuffers: () => set({ buffers: {} }),
}))
```

- [ ] **Step 4: 確認測試通過**

Run: `cd spa && npx vitest run src/stores/useEditorStore.test.ts`
Expected: 6 tests PASS

- [ ] **Step 5: Commit**

```bash
git add spa/src/stores/useEditorStore.ts spa/src/stores/useEditorStore.test.ts
git commit -m "feat(editor): add useEditorStore buffer store"
```

---

## Task 9: Monaco Editor 封裝元件

**Files:**
- Create: `spa/src/components/editor/MonacoWrapper.tsx`
- Create: `spa/src/components/editor/EditorToolbar.tsx`
- Create: `spa/src/components/editor/EditorStatusBar.tsx`

- [ ] **Step 1: 建立 MonacoWrapper**

```typescript
// spa/src/components/editor/MonacoWrapper.tsx
import Editor, { type OnMount } from '@monaco-editor/react'
import { useCallback, useRef } from 'react'
import type { editor, KeyMod, KeyCode } from 'monaco-editor'

interface Props {
  content: string
  language: string
  onChange: (value: string) => void
  onCursorChange: (line: number, column: number) => void
  onSave: () => void
}

export function MonacoWrapper({ content, language, onChange, onCursorChange, onSave }: Props) {
  const editorRef = useRef<editor.IStandaloneCodeEditor | null>(null)

  const handleMount: OnMount = useCallback((ed, monaco) => {
    editorRef.current = ed
    ed.addAction({
      id: 'purdex-save',
      label: 'Save',
      keybindings: [monaco.KeyMod.CtrlCmd | monaco.KeyCode.KeyS],
      run: () => onSave(),
    })
    ed.onDidChangeCursorPosition((e) => {
      onCursorChange(e.position.lineNumber, e.position.column)
    })
  }, [onSave, onCursorChange])

  return (
    <Editor
      value={content}
      language={language}
      theme="vs-dark"
      onChange={(value) => onChange(value ?? '')}
      onMount={handleMount}
      options={{
        minimap: { enabled: true },
        fontSize: 13,
        lineNumbers: 'on',
        wordWrap: 'on',
        scrollBeyondLastLine: false,
        automaticLayout: true,
      }}
    />
  )
}
```

- [ ] **Step 2: 建立 EditorToolbar**

```typescript
// spa/src/components/editor/EditorToolbar.tsx
import { FloppyDisk } from '@phosphor-icons/react'

interface Props {
  filePath: string
  isDirty: boolean
  onSave: () => void
}

export function EditorToolbar({ filePath, isDirty, onSave }: Props) {
  const fileName = filePath.split('/').pop() ?? filePath

  return (
    <div className="flex items-center justify-between px-3 py-1 border-b border-border-subtle bg-surface-secondary">
      <div className="flex items-center gap-2 text-xs text-text-secondary truncate">
        <span className="truncate" title={filePath}>{fileName}</span>
        {isDirty && <span className="text-accent-base" title="Unsaved changes">●</span>}
      </div>
      <button
        onClick={onSave}
        disabled={!isDirty}
        className="p-1 rounded hover:bg-surface-hover text-text-secondary disabled:opacity-30 transition-colors"
        title="Save (⌘S)"
      >
        <FloppyDisk size={14} />
      </button>
    </div>
  )
}
```

- [ ] **Step 3: 建立 EditorStatusBar**

```typescript
// spa/src/components/editor/EditorStatusBar.tsx
interface Props {
  language: string
  line: number
  column: number
}

export function EditorStatusBar({ language, line, column }: Props) {
  return (
    <div className="flex items-center justify-between px-3 py-0.5 border-t border-border-subtle bg-surface-secondary text-[10px] text-text-muted">
      <span>{language}</span>
      <span>Ln {line}, Col {column}</span>
    </div>
  )
}
```

- [ ] **Step 4: 確認 TypeScript 編譯通過**

Run: `cd spa && npx tsc --noEmit --pretty`
Expected: 無錯誤

- [ ] **Step 5: Commit**

```bash
git add spa/src/components/editor/
git commit -m "feat(editor): add MonacoWrapper, EditorToolbar, EditorStatusBar components"
```

---

## Task 10: EditorPane 主元件

**Files:**
- Create: `spa/src/components/editor/EditorPane.tsx`

- [ ] **Step 1: 實作 EditorPane**

```typescript
// spa/src/components/editor/EditorPane.tsx
import { useEffect, useCallback } from 'react'
import type { PaneRendererProps } from '../../lib/module-registry'
import { useEditorStore } from '../../stores/useEditorStore'
import { getFsBackend } from '../../lib/fs-backend'
import { MonacoWrapper } from './MonacoWrapper'
import { EditorToolbar } from './EditorToolbar'
import { EditorStatusBar } from './EditorStatusBar'
import type { FileSource } from '../../types/fs'

function bufferKey(source: FileSource, filePath: string): string {
  if (source.type === 'daemon') return `daemon:${source.hostId}:${filePath}`
  return `${source.type}:${filePath}`
}

function detectLanguage(filePath: string): string {
  const ext = filePath.split('.').pop()?.toLowerCase() ?? ''
  const map: Record<string, string> = {
    ts: 'typescript', tsx: 'typescriptreact', js: 'javascript', jsx: 'javascriptreact',
    json: 'json', md: 'markdown', css: 'css', html: 'html', go: 'go',
    py: 'python', rs: 'rust', sh: 'shell', yml: 'yaml', yaml: 'yaml',
    sql: 'sql', php: 'php', rb: 'ruby', swift: 'swift', kt: 'kotlin',
    java: 'java', c: 'c', cpp: 'cpp', h: 'c', hpp: 'cpp',
  }
  return map[ext] ?? 'plaintext'
}

// 外層元件只做 kind guard，避免 early return 後呼叫 Hook（React Rules of Hooks）
export function EditorPane({ pane, isActive }: PaneRendererProps) {
  const content = pane.content
  if (content.kind !== 'editor') return null
  return <EditorPaneInner source={content.source} filePath={content.filePath} isActive={isActive} />
}

function EditorPaneInner({ source, filePath, isActive }: { source: FileSource; filePath: string; isActive: boolean }) {
  const key = bufferKey(source, filePath)
  const buffer = useEditorStore((s) => s.buffers[key])

  // Load file on mount
  useEffect(() => {
    if (useEditorStore.getState().buffers[key]) return // already loaded
    const backend = getFsBackend(source)
    if (!backend) return

    backend.read(filePath)
      .then((data) => {
        const text = new TextDecoder().decode(data)
        const lang = detectLanguage(filePath)
        return backend.stat(filePath).then((stat) => {
          useEditorStore.getState().openBuffer(key, text, lang, { mtime: stat.mtime, size: stat.size })
        })
      })
      .catch(() => {
        // New file — open empty buffer
        useEditorStore.getState().openBuffer(key, '', detectLanguage(filePath))
      })
  }, [key, source, filePath])

  const handleSave = useCallback(async () => {
    const buf = useEditorStore.getState().buffers[key]
    if (!buf || !buf.isDirty) return
    const backend = getFsBackend(source)
    if (!backend) return
    try {
      const encoded = new TextEncoder().encode(buf.content)
      await backend.write(filePath, encoded)
      useEditorStore.getState().markSaved(key)
    } catch (err) {
      console.error('[editor] Save failed:', err)
      // TODO: show error notification
    }
  }, [key, source, filePath])

  if (!buffer) {
    return <div className="flex-1 flex items-center justify-center text-text-muted text-xs">Loading...</div>
  }

  return (
    <div className="flex-1 flex flex-col overflow-hidden">
      <EditorToolbar filePath={filePath} isDirty={buffer.isDirty} onSave={handleSave} />
      <div className="flex-1 overflow-hidden">
        <MonacoWrapper
          content={buffer.content}
          language={buffer.language}
          onChange={(value) => useEditorStore.getState().updateContent(key, value)}
          onCursorChange={(line, col) => useEditorStore.getState().updateCursor(key, line, col)}
          onSave={handleSave}
        />
      </div>
      <EditorStatusBar
        language={buffer.language}
        line={buffer.cursorPosition.line}
        column={buffer.cursorPosition.column}
      />
    </div>
  )
}
```

- [ ] **Step 2: 確認 TypeScript 編譯通過**

Run: `cd spa && npx tsc --noEmit --pretty`
Expected: 無錯誤

- [ ] **Step 3: Commit**

```bash
git add spa/src/components/editor/EditorPane.tsx
git commit -m "feat(editor): add EditorPane main component with Monaco + buffer integration"
```

---

## Task 11: 註冊 Editor Module + New Tab 入口

**Files:**
- Create: `spa/src/components/editor/EditorNewTabSection.tsx`
- Modify: `spa/src/lib/register-modules.tsx`

- [ ] **Step 1: 建立 EditorNewTabSection**

```typescript
// spa/src/components/editor/EditorNewTabSection.tsx
import { useCallback } from 'react'
import { FilePlus, FileText } from '@phosphor-icons/react'
import { generateId } from '../../lib/id'
import { InAppBackend } from '../../lib/fs-backend-inapp'
import { getFsBackend } from '../../lib/fs-backend'
import type { PaneContent } from '../../types/tab'
import type { FileSource } from '../../types/fs'

interface Props {
  onSelect: (content: PaneContent) => void
}

export function EditorNewTabSection({ onSelect }: Props) {
  const createFile = useCallback(async (ext: string) => {
    const id = generateId()
    const filePath = `/buffer/${id}.${ext}`
    const source: FileSource = { type: 'inapp' }

    const backend = getFsBackend(source)
    if (!backend) {
      console.error('[editor] InApp backend not available')
      return
    }
    try {
      await backend.write(filePath, new TextEncoder().encode(''))
    } catch (err) {
      console.error('[editor] Failed to create file:', err)
      return
    }

    onSelect({ kind: 'editor', source, filePath } as PaneContent)
  }, [onSelect])

  return (
    <div className="flex gap-2">
      <button
        className="flex items-center gap-2 px-4 py-2 rounded-lg border border-border-subtle bg-surface-secondary hover:bg-surface-hover text-text-primary text-sm transition-colors"
        onClick={() => createFile('txt')}
      >
        <FilePlus size={16} />
        New File
      </button>
      <button
        className="flex items-center gap-2 px-4 py-2 rounded-lg border border-border-subtle bg-surface-secondary hover:bg-surface-hover text-text-primary text-sm transition-colors"
        onClick={() => createFile('md')}
      >
        <FileText size={16} />
        New Markdown
      </button>
    </div>
  )
}
```

- [ ] **Step 2: 在 register-modules.tsx 註冊 editor module 和 new tab provider**

新增 import：
```typescript
import { EditorPane } from '../components/editor/EditorPane'
import { EditorNewTabSection } from '../components/editor/EditorNewTabSection'
import { InAppBackend } from '../lib/fs-backend-inapp'
import { registerFsBackend } from '../lib/fs-backend'
import { registerFileOpener } from '../lib/file-opener-registry'
```

在 `registerBuiltinModules()` 函式內新增：

```typescript
// Editor module
registerModule({
  id: 'editor',
  name: 'Editor',
  panes: [{ kind: 'editor', component: EditorPane }],
})

// Register InApp FS backend (singleton — 避免熱重載時資料遺失)
const inAppBackend = new InAppBackend()
registerFsBackend('inapp', inAppBackend)

// Register file opener for text files
registerFileOpener({
  id: 'monaco-editor',
  label: 'Text Editor',
  icon: 'File',
  match: (file) => !file.isDirectory,
  priority: 'default',
  createContent: (source, file) => ({ kind: 'editor', source, filePath: file.path } as PaneContent),
})

// New tab provider for editor
registerNewTabProvider({
  id: 'editor',
  label: 'editor.provider_label',
  icon: 'File',
  order: 5,
  component: EditorNewTabSection,
})
```

- [ ] **Step 3: 確認 TypeScript 編譯通過**

Run: `cd spa && npx tsc --noEmit --pretty`
Expected: 無錯誤

- [ ] **Step 4: 確認全部測試通過**

Run: `cd spa && npx vitest run`
Expected: 所有測試 PASS

- [ ] **Step 5: Commit**

```bash
git add spa/src/components/editor/EditorNewTabSection.tsx spa/src/lib/register-modules.tsx
git commit -m "feat(editor): register editor module, InApp backend, and new tab provider"
```

---

## Task 12: 端到端驗證

**Files:** 無新增，驗證整體流程

- [ ] **Step 1: 啟動 dev server**

Run: `cd spa && pnpm run dev`

- [ ] **Step 2: 在瀏覽器中驗證**

1. 開啟 `http://100.64.0.2:5174`
2. 點擊「+」開新 tab → 應該看到 New Tab 頁面有「New File」和「New Markdown」按鈕
3. 點「New File」→ 應該開啟 Monaco editor，空白內容
4. 輸入文字 → toolbar 應顯示 unsaved 標記 `●`
5. 按 `⌘S` → `●` 消失（已存檔到 InApp storage）
6. 重新整理頁面 → tab 還在（PaneContent persist），重新載入內容（從 InAppBackend 讀取）

- [ ] **Step 3: 修正任何 UI 問題**

若有 layout、styling、或 Monaco 初始化問題，在此步驟修正。

- [ ] **Step 4: Lint 確認**

Run: `cd spa && pnpm run lint`
Expected: 無錯誤

- [ ] **Step 5: 最終 commit**

```bash
git add -A
git commit -m "fix(editor): address UI issues from integration testing"
```

（若無修正則跳過此 commit）
