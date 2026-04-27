# P1 — register-modules god file 拆 + Module.fileOpeners + 停用 placeholder

> 對應 SPEC.md `# P1` 段。本檔吸收 PLAN 第二輪 4 份 codex review 共 33 findings 中與 P1 相關的修訂。


PR 結束標準：`register-modules.tsx`（god file）拆成 `register-modules/` 子目錄；file-opener-registry 改 owner-scoped；Editor 模組停用 / 啟用切換生效；既有持久化 panes 不消失，改顯示 placeholder；HMR 不殘留 opener；`module-registry.ts` import graph 不含任何 `components/`。

**Codex review 33 findings 對應修訂**（吸收進本檔對應 task）：
- C1 `module-registry.ts` import component → Task 1.6a 改 `RendererResolution` 回 metadata；component 由 PaneLayoutRenderer 注入
- C2 `register-modules.tsx` god file → 新增 Task 1.0a/b（拆檔）放最前面
- file-opener-registry owner-scoped → 新增 Task 1.0c
- DisabledModulePlaceholder 放 `components/modules/` → 修 Task 1.5
- Task 1.6 caller 是 `PaneLayoutRenderer.tsx:28`（不是 `Pane.tsx`）→ 拆 Task 1.6a / 1.6b
- Task 1.6 不要用 `require()`（Vite ESM 會炸）→ 1.6a 純 metadata 沒這問題
- i18n 路徑 `spa/src/locales/*.json`（不是 `i18n/`）→ 修 Task 1.5
- ModuleDefinition 預留 `disabledComponent?: ComponentType` opt-in → 修 Task 1.1
- Commit message lowercase → 全檔 commit 統一 lowercase
- Spec rev 4 → 修 Task 1.8

## Task 1.0a — file-opener-registry 改 owner-scoped

**Files:**
- Modify: `spa/src/lib/file-opener-registry.ts`
- Test: `spa/src/lib/file-opener-registry.test.ts`（新建）

**動機**：`clearFileOpenerRegistry()` 全清會殺掉外部 owner（防守 review #1）。改成 per-owner registration，`unregisterByOwner(moduleId)` 只清屬於該 module 的 opener；`clearAllForHmr()` 留給 HMR 全清場景。

- [ ] **Step 1: Write failing test**

新建 `spa/src/lib/file-opener-registry.test.ts`：

```ts
import { describe, it, expect, beforeEach } from 'vitest'
import {
  registerFileOpener,
  unregisterByOwner,
  clearAllForHmr,
  getRegisteredOpeners,
  type FileOpener,
} from './file-opener-registry'

const mk = (id: string): FileOpener => ({
  id, label: id, icon: 'File',
  match: () => true, priority: 'default',
  createContent: () => ({ kind: 'editor' } as never),
})

describe('file-opener-registry owner-scoped', () => {
  beforeEach(() => clearAllForHmr())

  it('registers opener with ownerModuleId', () => {
    registerFileOpener({ ...mk('a'), ownerModuleId: 'editor' })
    expect(getRegisteredOpeners()).toHaveLength(1)
  })

  it('unregisterByOwner only removes openers of that owner', () => {
    registerFileOpener({ ...mk('a'), ownerModuleId: 'editor' })
    registerFileOpener({ ...mk('b'), ownerModuleId: 'plugin-x' })
    unregisterByOwner('editor')
    const remaining = getRegisteredOpeners()
    expect(remaining).toHaveLength(1)
    expect(remaining[0].id).toBe('b')
  })

  it('clearAllForHmr removes all', () => {
    registerFileOpener({ ...mk('a'), ownerModuleId: 'editor' })
    registerFileOpener({ ...mk('b'), ownerModuleId: 'plugin-x' })
    clearAllForHmr()
    expect(getRegisteredOpeners()).toHaveLength(0)
  })
})
```

- [ ] **Step 2: Run test, expect FAIL**

```
cd spa && npx vitest run src/lib/file-opener-registry.test.ts
```

Expected：`unregisterByOwner / clearAllForHmr / getRegisteredOpeners` not exported；`registerFileOpener` 缺 `ownerModuleId` field。

- [ ] **Step 3: Implement owner-scoped registry**

改寫 `spa/src/lib/file-opener-registry.ts`：

```ts
export interface FileOpener {
  id: string
  label: string
  icon: string
  match: (file: FileInfo) => boolean
  priority: 'default' | number
  createContent: (source: FileSource, file: FileInfo) => PaneContent
}

interface RegisteredOpener extends FileOpener {
  ownerModuleId: string
}

const openers = new Map<string, RegisteredOpener>()  // key = `${ownerModuleId}:${opener.id}`

export function registerFileOpener(spec: FileOpener & { ownerModuleId: string }): void {
  openers.set(`${spec.ownerModuleId}:${spec.id}`, spec)
}

export function unregisterByOwner(ownerModuleId: string): void {
  for (const key of [...openers.keys()]) {
    if (key.startsWith(`${ownerModuleId}:`)) openers.delete(key)
  }
}

export function clearAllForHmr(): void { openers.clear() }

export function getRegisteredOpeners(): RegisteredOpener[] {
  return [...openers.values()]
}

export function getDefaultOpener(file: FileInfo): RegisteredOpener | null {
  // 既有 priority 排序邏輯，從 openers.values() 取
}
```

舊 `clearFileOpenerRegistry` 名稱保留為 `export const clearFileOpenerRegistry = clearAllForHmr` 過渡，避免一次破太多 caller。

- [ ] **Step 4: Run test, expect PASS**

```
cd spa && npx vitest run src/lib/file-opener-registry.test.ts
```

- [ ] **Step 5: Commit**

```bash
git add spa/src/lib/file-opener-registry.ts spa/src/lib/file-opener-registry.test.ts
git commit -m "feat(spa): file-opener registry tracks owner module id"
```

---

## Task 1.0b — 拆 register-modules.tsx 為 register-modules/ 子目錄

**Files:**
- New: `spa/src/lib/register-modules/index.tsx`（orchestrator，< 80 行）
- New: `spa/src/lib/register-modules/editor-module.tsx`（Editor module definition + fileOpeners + settings）
- New: `spa/src/lib/register-modules/fs-backends.tsx`（既有 fs backend 註冊）
- Modify: `spa/src/lib/register-modules.tsx`（縮成 `export * from './register-modules'` 過渡 shim）
- Test: `spa/src/lib/register-modules/__tests__/orchestrator.test.tsx`

**動機**：`register-modules.tsx` 467 行已是 god file。P3（editor settings）/ P5（popup deps）若不先拆，會持續灌大。P1 本來就動 editor 註冊，是最低成本切點（B 決議 (ii) + 體質 review #2）。

- [ ] **Step 1: 先讀現行 register-modules.tsx 找責任邊界**

```bash
cd spa && wc -l src/lib/register-modules.tsx
grep -n "^function\|^export\|^const \|registerFileOpener\|registerFsBackend\|registerModule" src/lib/register-modules.tsx
```

確認三個責任：
- Editor module def + 三個 inline `registerFileOpener`
- fs backend 註冊（讓 `getFsBackend(source)` work）
- 其他 module def + orchestrator

- [ ] **Step 2: 建子目錄 + 移責任**

- `register-modules/editor-module.tsx`：export `editorModuleDefinition: ModuleDefinition`，含原 inline `registerFileOpener` 三段（後續 Task 1.3 會把它們搬進 `editorModuleDefinition.fileOpeners`）
- `register-modules/fs-backends.tsx`：export `registerBuiltinFsBackends()`，搬出 fs backend 註冊邏輯
- `register-modules/index.tsx`：export `registerBuiltinModules()`，呼叫 `registerBuiltinFsBackends() + registerModule(editorModuleDefinition) + registerModule(otherDefs...)`，然後（Task 1.2 之後）呼叫 `applyModuleFileOpeners()`

- [ ] **Step 3: 過渡 shim**

`spa/src/lib/register-modules.tsx` 縮成：

```tsx
export * from './register-modules/index'
```

caller 不需改 import path（向後相容）。

- [ ] **Step 4: 驗證**

```bash
cd spa && npx vitest run && pnpm run lint && pnpm run build
```

Expected：所有既有測試通過、lint pass、build pass。**沒有行為變更**，只是檔案重組。

- [ ] **Step 5: Commit**

```bash
git add spa/src/lib/register-modules/ spa/src/lib/register-modules.tsx
git commit -m "refactor(spa): split register-modules.tsx into register-modules/ subdir"
```

---

## Task 1.0c — `applyModuleFileOpeners` orchestrator

**Files:**
- New: `spa/src/lib/register-modules/module-file-openers.ts`
- Test: `spa/src/lib/register-modules/module-file-openers.test.ts`

**動機**：把「iterate modules → unregister by owner → check enable filter → register」邏輯抽出獨立函式，可單測。

- [ ] **Step 1: Write failing test**

```ts
import { describe, it, expect, beforeEach } from 'vitest'
import { registerModule, unregisterModule } from '../module-registry'
import {
  clearAllForHmr,
  getRegisteredOpeners,
} from '../file-opener-registry'
import { useModuleEnabledStore } from '../../stores/useModuleEnabledStore'
import { applyModuleFileOpeners } from './module-file-openers'

const mkOpener = (id: string) => ({
  id, label: id, icon: 'File',
  match: () => true, priority: 'default' as const,
  createContent: () => ({ kind: 'editor' } as never),
})

beforeEach(() => {
  clearAllForHmr()
  unregisterModule('m1'); unregisterModule('m2')
})

describe('applyModuleFileOpeners', () => {
  it('registers fileOpeners for non-disableable modules', () => {
    registerModule({ id: 'm1', name: 'M1', fileOpeners: [mkOpener('a')] })
    applyModuleFileOpeners()
    expect(getRegisteredOpeners().map((o) => o.id)).toEqual(['a'])
  })

  it('skips fileOpeners for disabled disableable modules', () => {
    useModuleEnabledStore.getState().setEnabled('m1', false)
    registerModule({ id: 'm1', name: 'M1', disableable: true, fileOpeners: [mkOpener('a')] })
    applyModuleFileOpeners()
    expect(getRegisteredOpeners()).toHaveLength(0)
  })

  it('idempotent: repeated apply does not duplicate openers', () => {
    registerModule({ id: 'm1', name: 'M1', fileOpeners: [mkOpener('a')] })
    applyModuleFileOpeners()
    applyModuleFileOpeners()
    expect(getRegisteredOpeners()).toHaveLength(1)
  })

  it('does not affect openers from other owners', () => {
    registerModule({ id: 'm1', name: 'M1', fileOpeners: [mkOpener('a')] })
    registerModule({ id: 'm2', name: 'M2', fileOpeners: [mkOpener('b')] })
    applyModuleFileOpeners()
    useModuleEnabledStore.getState().setEnabled('m1', false)
    // 注意：m1 必須是 disableable 才會被 filter
    unregisterModule('m1')
    registerModule({ id: 'm1', name: 'M1', disableable: true, fileOpeners: [mkOpener('a')] })
    applyModuleFileOpeners()
    expect(getRegisteredOpeners().map((o) => o.id).sort()).toEqual(['b'])
  })
})
```

- [ ] **Step 2: Run test, expect FAIL**

`applyModuleFileOpeners` not exported.

- [ ] **Step 3: Implement**

```ts
// spa/src/lib/register-modules/module-file-openers.ts
import { getModules } from '../module-registry'
import {
  registerFileOpener,
  unregisterByOwner,
} from '../file-opener-registry'
import { useModuleEnabledStore } from '../../stores/useModuleEnabledStore'

export function applyModuleFileOpeners(): void {
  for (const m of getModules()) {
    unregisterByOwner(m.id)
    if (!m.fileOpeners) continue
    if (m.disableable && !useModuleEnabledStore.getState().isEnabled(m.id)) continue
    for (const spec of m.fileOpeners) registerFileOpener({ ...spec, ownerModuleId: m.id })
  }
}
```

`register-modules/index.tsx` 在 `registerBuiltinModules()` 尾段呼叫 `applyModuleFileOpeners()`。

- [ ] **Step 4: Run test, expect PASS**

```
cd spa && npx vitest run src/lib/register-modules/module-file-openers.test.ts
```

- [ ] **Step 5: Commit**

```bash
git add spa/src/lib/register-modules/module-file-openers.ts spa/src/lib/register-modules/module-file-openers.test.ts spa/src/lib/register-modules/index.tsx
git commit -m "feat(spa): apply module file openers via owner-scoped helper"
```

---

## Task 1.1 — `ModuleDefinition.fileOpeners` interface

**Files:**
- Modify: `spa/src/lib/module-registry.ts`（既有 `ModuleDefinition` 加欄位）
- Test: `spa/src/lib/module-registry.test.ts`

- [ ] **Step 1: Write failing test**

加入 `spa/src/lib/module-registry.test.ts`（若無檔案則新建）：

```ts
import { describe, it, expect, beforeEach } from 'vitest'
import { registerModule, getModule, unregisterModule, type ModuleDefinition } from './module-registry'
import type { FileOpener } from './file-opener-registry'

describe('ModuleDefinition.fileOpeners', () => {
  beforeEach(() => unregisterModule('test-mod'))

  it('stores fileOpeners on the module definition', () => {
    const opener: FileOpener = {
      id: 'noop', label: 'Noop', icon: 'File',
      match: () => true, priority: 'default',
      createContent: (s, f) => ({ kind: 'editor', source: s, filePath: f.path } as never),
    }
    const def: ModuleDefinition = { id: 'test-mod', name: 'Test', fileOpeners: [opener] }
    registerModule(def)
    expect(getModule('test-mod')?.fileOpeners).toEqual([opener])
  })
})
```

- [ ] **Step 2: Run test, expect FAIL**

```
cd spa && npx vitest run src/lib/module-registry.test.ts
```

Expected：TS 編譯錯誤 `Property 'fileOpeners' does not exist on type 'ModuleDefinition'`。

- [ ] **Step 3: Add fields to ModuleDefinition**

在 `spa/src/lib/module-registry.ts` 既有 `ModuleDefinition` interface 內、`descriptionKey` 後加：

```ts
  /**
   * File openers contributed by this module. Registered into the file-opener
   * registry only when the module is enabled (or always for non-disableable
   * modules). Removed on HMR dispose to prevent stale entries.
   */
  fileOpeners?: import('./file-opener-registry').FileOpener[]

  /**
   * Optional custom component to render when a pane of this module is shown
   * but the module is disabled. If unset, PaneLayoutRenderer falls back to
   * the generic DisabledModulePlaceholder. Use only if the module needs a
   * domain-specific recovery affordance — not the default.
   *
   * NOTE: module-registry MUST NOT import this component. It is held only as
   * a type reference; concrete component is registered by the module owner
   * inside register-modules/<module>.tsx.
   */
  disabledComponent?: import('react').ComponentType<{ moduleId: string; paneKind: string }>
```

- [ ] **Step 4: Run test, expect PASS**

```
cd spa && npx vitest run src/lib/module-registry.test.ts
```

- [ ] **Step 5: Commit**

```bash
git add spa/src/lib/module-registry.ts spa/src/lib/module-registry.test.ts
git commit -m "feat(spa): add fileOpeners field to ModuleDefinition"
```

---

## Task 1.2 — Module-driven opener registration helper

**Files:**
- Modify: `spa/src/lib/register-modules.tsx`（新增 helper function 並導出供測試）
- Test: `spa/src/lib/register-modules.test.tsx`

- [ ] **Step 1: Write failing test**

新增 `spa/src/lib/register-modules.test.tsx`：

```tsx
import { describe, it, expect, beforeEach } from 'vitest'
import {
  registerModule,
  unregisterModule,
  type ModuleDefinition,
} from './module-registry'
import {
  clearFileOpenerRegistry,
  getFileOpeners,
} from './file-opener-registry'
import { useModuleEnabledStore } from '../stores/useModuleEnabledStore'
import { applyModuleFileOpeners } from './register-modules'
import type { FileInfo } from '../types/fs'

const mkOpener = (id: string) => ({
  id, label: id, icon: 'File',
  match: (_: FileInfo) => true, priority: 'default' as const,
  createContent: (s: never, f: never) => ({ kind: 'editor', source: s, filePath: (f as { path: string }).path } as never),
})

describe('applyModuleFileOpeners', () => {
  beforeEach(() => {
    clearFileOpenerRegistry()
    unregisterModule('always-on')
    unregisterModule('toggleable')
  })

  it('registers openers from non-disableable modules unconditionally', () => {
    const def: ModuleDefinition = { id: 'always-on', name: 'AO', fileOpeners: [mkOpener('ao-opener')] }
    registerModule(def)
    applyModuleFileOpeners()
    const ids = getFileOpeners({ path: '/x', name: 'x', extension: '', isDirectory: false } as FileInfo).map((o) => o.id)
    expect(ids).toContain('ao-opener')
  })

  it('skips openers from disableable modules when disabled', () => {
    const def: ModuleDefinition = { id: 'toggleable', name: 'T', disableable: true, fileOpeners: [mkOpener('t-opener')] }
    registerModule(def)
    useModuleEnabledStore.getState().setEnabled('toggleable', false)
    applyModuleFileOpeners()
    const ids = getFileOpeners({ path: '/x', name: 'x', extension: '', isDirectory: false } as FileInfo).map((o) => o.id)
    expect(ids).not.toContain('t-opener')
  })

  it('registers openers from disableable modules when enabled', () => {
    const def: ModuleDefinition = { id: 'toggleable', name: 'T', disableable: true, fileOpeners: [mkOpener('t-opener')] }
    registerModule(def)
    useModuleEnabledStore.getState().setEnabled('toggleable', true)
    applyModuleFileOpeners()
    const ids = getFileOpeners({ path: '/x', name: 'x', extension: '', isDirectory: false } as FileInfo).map((o) => o.id)
    expect(ids).toContain('t-opener')
  })
})
```

- [ ] **Step 2: Run test, expect FAIL**

`applyModuleFileOpeners` 尚未 export — TS error `has no exported member 'applyModuleFileOpeners'`。

- [ ] **Step 3: Add helper to register-modules.tsx**

在 `spa/src/lib/register-modules.tsx` 加 export（`registerBuiltinModules` 上方適合的位置）：

```tsx
import { clearFileOpenerRegistry as _clearFileOpenerRegistry } from './file-opener-registry'

/**
 * Walk all registered modules and register their fileOpeners into the
 * file-opener registry, respecting disableable + useModuleEnabledStore.
 *
 * Idempotent within a session: clears the registry before re-applying so
 * HMR dispose + re-run leaves the registry clean.
 */
export function applyModuleFileOpeners(): void {
  _clearFileOpenerRegistry()
  for (const m of getModules()) {
    if (!m.fileOpeners) continue
    if (m.disableable && !useModuleEnabledStore.getState().isEnabled(m.id)) continue
    for (const opener of m.fileOpeners) registerFileOpener(opener)
  }
}
```

(`getModules` / `useModuleEnabledStore` / `registerFileOpener` 都已在檔案內 import；若缺則補。)

- [ ] **Step 4: Run test, expect PASS**

```
cd spa && npx vitest run src/lib/register-modules.test.tsx
```

- [ ] **Step 5: Commit**

```bash
git add spa/src/lib/register-modules.tsx spa/src/lib/register-modules.test.tsx
git commit -m "feat(spa): apply module-declared fileOpeners with enable filter"
```

---

## Task 1.3 — Editor module 收編三個 file opener

**Files:**
- Modify: `spa/src/lib/register-modules.tsx`

- [ ] **Step 1: Write failing test**

擴 `spa/src/lib/register-modules.test.tsx`，加 case：

```tsx
import { registerBuiltinModules } from './register-modules'
import { getModule } from './module-registry'

it('editor module declares its three file openers via fileOpeners field', () => {
  registerBuiltinModules()
  const editor = getModule('editor')
  expect(editor?.fileOpeners?.map((o) => o.id).sort()).toEqual(
    ['image-preview', 'monaco-editor', 'pdf-viewer'],
  )
})
```

- [ ] **Step 2: Run test, expect FAIL**

Editor 模組目前無 `fileOpeners` 欄位 → `editor?.fileOpeners` 是 undefined。

- [ ] **Step 3: Move three openers into Editor module definition**

在 `spa/src/lib/register-modules.tsx` 找 Editor module `registerModule({ id: 'editor', ... })`：

把 inline `registerFileOpener(...)` 三處（image-preview / pdf-viewer / monaco-editor）原本實作搬到 Editor module 定義的 `fileOpeners` 欄位內：

```tsx
  // === existing constants kept intact ===
  const IMAGE_EXTS = new Set(['png', 'jpg', 'jpeg', 'gif', 'webp', 'svg', 'ico'])
  const PDF_EXTS = new Set(['pdf'])
  const BINARY_EXTS = new Set([...IMAGE_EXTS, ...PDF_EXTS])

  registerModule({
    id: 'editor',
    name: 'Editor',
    disableable: true,
    descriptionKey: 'modules.editor.description',
    panes: [
      { kind: 'editor', component: EditorPane },
      { kind: 'editor-buffers', component: EditorBuffersPane },
      { kind: 'image-preview', component: ImagePreviewPane },
      { kind: 'pdf-preview', component: PdfPreviewPane },
    ],
    fileOpeners: [
      {
        id: 'image-preview',
        label: 'Image Preview',
        icon: 'Image',
        match: (file) => IMAGE_EXTS.has(file.extension.toLowerCase()),
        priority: 'default',
        createContent: (source, file) => ({ kind: 'image-preview', source, filePath: file.path }) as PaneContent,
      },
      {
        id: 'pdf-viewer',
        label: 'PDF Viewer',
        icon: 'FilePdf',
        match: (file) => PDF_EXTS.has(file.extension.toLowerCase()),
        priority: 'default',
        createContent: (source, file) => ({ kind: 'pdf-preview', source, filePath: file.path }) as PaneContent,
      },
      {
        id: 'monaco-editor',
        label: 'Text Editor',
        icon: 'File',
        match: (file) => !file.isDirectory && !BINARY_EXTS.has(file.extension.toLowerCase()),
        priority: 'default',
        createContent: (source, file) => ({ kind: 'editor', source, filePath: file.path }) as PaneContent,
      },
    ],
    settings: [
      // ... existing settings list unchanged
    ],
  })
```

刪除原本 inline 的三段 `registerFileOpener({ ... })` 呼叫；`IMAGE_EXTS` / `PDF_EXTS` / `BINARY_EXTS` 常數保留（仍會被 Editor 定義使用）。

- [ ] **Step 4: Wire `applyModuleFileOpeners()` into bootstrap**

在 `registerBuiltinModules()` **末尾**（所有 `registerModule(...)` / `setHostBuiltinSections(...)` 之後、`captureBaseline` 之前）加一行：

```tsx
  applyModuleFileOpeners()
```

- [ ] **Step 5: Run all related tests, expect PASS**

```
cd spa && npx vitest run src/lib/register-modules.test.tsx src/lib/file-opener-registry.test.ts
```

- [ ] **Step 6: Commit**

```bash
git add spa/src/lib/register-modules.tsx spa/src/lib/register-modules.test.tsx
git commit -m "refactor(spa): editor module owns its three file openers"
```

---

## Task 1.4 — HMR dispose 串接 file-opener registry

**Files:**
- Modify: `spa/src/lib/register-modules.tsx`（既有 `import.meta.hot.dispose` block）

- [ ] **Step 1: Write failing test**

在 `spa/src/lib/register-modules.test.tsx` 加 case：

```tsx
import { getFileOpeners as _getOpeners } from './file-opener-registry'

it('HMR dispose helper clears file opener registry', async () => {
  const { resetFileOpenerRegistryForHmr } = await import('./register-modules')
  // populate
  registerBuiltinModules()
  expect(_getOpeners({ path: '/x.txt', name: 'x.txt', extension: 'txt', isDirectory: false } as never).length).toBeGreaterThan(0)
  // dispose
  resetFileOpenerRegistryForHmr()
  expect(_getOpeners({ path: '/x.txt', name: 'x.txt', extension: 'txt', isDirectory: false } as never).length).toBe(0)
})
```

- [ ] **Step 2: Run test, expect FAIL**

`resetFileOpenerRegistryForHmr` 尚未 export。

- [ ] **Step 3: Add export + wire HMR dispose**

在 `register-modules.tsx` 加 export：

```tsx
export function resetFileOpenerRegistryForHmr(): void {
  _clearFileOpenerRegistry()
}
```

擴 既有 HMR dispose hook：

```tsx
if (import.meta.hot) {
  import.meta.hot.dispose(() => {
    resetSettingsContributionsForHmr()
    resetFileOpenerRegistryForHmr()  // ← new
  })
}
```

- [ ] **Step 4: Run test, expect PASS**

```
cd spa && npx vitest run src/lib/register-modules.test.tsx
```

- [ ] **Step 5: Commit**

```bash
git add spa/src/lib/register-modules.tsx spa/src/lib/register-modules.test.tsx
git commit -m "feat(spa): clear file-opener registry on HMR dispose"
```

---

## Task 1.5 — `DisabledModulePlaceholder` 元件

**Files:**
- Create: `spa/src/components/modules/DisabledModulePlaceholder.tsx`
- Test: `spa/src/components/modules/DisabledModulePlaceholder.test.tsx`

- [ ] **Step 1: Write failing test**

新建 `spa/src/components/modules/DisabledModulePlaceholder.test.tsx`：

```tsx
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { DisabledModulePlaceholder } from './DisabledModulePlaceholder'
import { useModuleEnabledStore } from '../../stores/useModuleEnabledStore'

describe('DisabledModulePlaceholder', () => {
  beforeEach(() => {
    useModuleEnabledStore.setState({ enabledById: {} } as never, false)
  })

  it('renders module id and pane kind', () => {
    render(<DisabledModulePlaceholder moduleId="editor" paneKind="editor" />)
    expect(screen.getByText(/editor/i)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /enable/i })).toBeInTheDocument()
  })

  it('clicking enable button calls setEnabled with true', () => {
    const setEnabled = vi.spyOn(useModuleEnabledStore.getState(), 'setEnabled')
    render(<DisabledModulePlaceholder moduleId="editor" paneKind="editor" />)
    fireEvent.click(screen.getByRole('button', { name: /enable/i }))
    expect(setEnabled).toHaveBeenCalledWith('editor', true)
  })
})
```

- [ ] **Step 2: Run test, expect FAIL**

`DisabledModulePlaceholder` 尚未存在。

- [ ] **Step 3: Implement component**

新建 `spa/src/components/modules/DisabledModulePlaceholder.tsx`：

```tsx
import { useModuleEnabledStore } from '../../stores/useModuleEnabledStore'
import { useI18nStore } from '../../stores/useI18nStore'

interface Props {
  moduleId: string
  paneKind: string
}

export function DisabledModulePlaceholder({ moduleId, paneKind }: Props) {
  const setEnabled = useModuleEnabledStore((s) => s.setEnabled)
  const t = useI18nStore((s) => s.t)
  const handleEnable = () => {
    setEnabled(moduleId, true)
  }
  return (
    <div className="flex flex-col items-center justify-center h-full p-6 gap-3 text-sm text-text-secondary">
      <h3 className="text-base text-text-primary">
        {t('module.disabled.title', { module: moduleId })}
      </h3>
      <p>{t('module.disabled.body', { paneKind })}</p>
      <button
        type="button"
        onClick={handleEnable}
        className="px-3 py-1 bg-accent rounded text-text-primary hover:opacity-90"
        aria-label={`Enable ${moduleId} module`}
      >
        {t('module.disabled.enable')}
      </button>
      <p className="text-xs text-text-muted">{t('module.disabled.reload_hint')}</p>
    </div>
  )
}
```

- [ ] **Step 4: Add i18n keys**

在 `spa/src/locales/zh-TW.json` 加（**注意路徑是 `locales/` 不是 `i18n/`**；通用 review D2）：

```json
"module": {
  "disabled": {
    "title": "{{module}} 模組目前已停用",
    "body": "啟用後重載即可恢復這個 {{paneKind}} 分頁。",
    "enable": "啟用",
    "reload_hint": "啟用後請手動重載頁面"
  }
}
```

`spa/src/locales/en.json` 加：

```json
"module": {
  "disabled": {
    "title": "{{module}} module is disabled",
    "body": "Enable it and reload to restore this {{paneKind}} pane.",
    "enable": "Enable",
    "reload_hint": "Reload the page after enabling"
  }
}
```

- [ ] **Step 5: Run test, expect PASS**

```
cd spa && npx vitest run src/components/modules/DisabledModulePlaceholder.test.tsx src/locales/locale-completeness.test.ts
```

- [ ] **Step 6: Commit**

```bash
git add spa/src/components/modules/ spa/src/locales/zh-TW.json spa/src/locales/en.json
git commit -m "feat(spa): add disabled module placeholder component"
```

---

## Task 1.6a — `resolvePaneRenderer` API（純 metadata，不 import component）

**Files:**
- Modify: `spa/src/lib/module-registry.ts`（新 `resolvePaneRenderer` + 型別定義）
- Test: `spa/src/lib/module-registry.test.ts`（擴測）

**動機**：原 plan 的 `resolvePaneRenderer` 在 `module-registry.ts` 內 import `DisabledModulePlaceholder`，違反 lib → UI 反向依賴鐵則（攻擊 / 體質 critical C1）。`require()` 黑魔法在 Vite ESM 還會 runtime 炸（攻擊 review #1）。改成回 `RendererResolution` discriminated union，純 metadata；component 由 PaneLayoutRenderer 在 render 層注入。

- [ ] **Step 1: Write failing test**

擴 `spa/src/lib/module-registry.test.ts`：

```ts
import { resolvePaneRenderer } from './module-registry'
import { useModuleEnabledStore } from '../stores/useModuleEnabledStore'

describe('resolvePaneRenderer', () => {
  beforeEach(() => unregisterModule('rt-mod'))

  it('returns kind=render with component when module is enabled', () => {
    const Comp = () => null
    registerModule({
      id: 'rt-mod', name: 'RT', disableable: true,
      panes: [{ kind: 'rt-pane', component: Comp }],
    })
    useModuleEnabledStore.getState().setEnabled('rt-mod', true)
    const r = resolvePaneRenderer('rt-pane')
    expect(r.kind).toBe('render')
    if (r.kind === 'render') expect(r.component).toBe(Comp)
  })

  it('returns kind=disabled when disableable module is disabled', () => {
    const Comp = () => null
    registerModule({
      id: 'rt-mod', name: 'RT', disableable: true,
      panes: [{ kind: 'rt-pane', component: Comp }],
    })
    useModuleEnabledStore.getState().setEnabled('rt-mod', false)
    const r = resolvePaneRenderer('rt-pane')
    expect(r.kind).toBe('disabled')
    if (r.kind === 'disabled') {
      expect(r.moduleId).toBe('rt-mod')
      expect(r.paneKind).toBe('rt-pane')
      expect(r.customComponent).toBeUndefined()  // module 沒宣告 disabledComponent
    }
  })

  it('passes through customComponent from module.disabledComponent', () => {
    const Comp = () => null
    const Custom = () => null
    registerModule({
      id: 'rt-mod', name: 'RT', disableable: true,
      panes: [{ kind: 'rt-pane', component: Comp }],
      disabledComponent: Custom,
    })
    useModuleEnabledStore.getState().setEnabled('rt-mod', false)
    const r = resolvePaneRenderer('rt-pane')
    if (r.kind === 'disabled') expect(r.customComponent).toBe(Custom)
  })

  it('returns kind=unknown when no module owns the kind', () => {
    const r = resolvePaneRenderer('does-not-exist')
    expect(r.kind).toBe('unknown')
  })
})
```

- [ ] **Step 2: Run test, expect FAIL**

`resolvePaneRenderer` not exported / `RendererResolution` undefined.

- [ ] **Step 3: Implement metadata API**

在 `spa/src/lib/module-registry.ts` 加：

```ts
import { useModuleEnabledStore } from '../stores/useModuleEnabledStore'

export type RendererResolution =
  | { kind: 'render'; component: React.ComponentType<PaneRendererProps> }
  | {
      kind: 'disabled'
      moduleId: string
      paneKind: string
      customComponent?: React.ComponentType<{ moduleId: string; paneKind: string }>
    }
  | { kind: 'unknown'; paneKind: string }

export function resolvePaneRenderer(paneKind: string): RendererResolution {
  for (const m of modules.values()) {
    for (const p of m.panes ?? []) {
      if (p.kind !== paneKind) continue
      if (m.disableable && !useModuleEnabledStore.getState().isEnabled(m.id)) {
        return {
          kind: 'disabled',
          moduleId: m.id,
          paneKind,
          customComponent: m.disabledComponent,
        }
      }
      return { kind: 'render', component: p.component }
    }
  }
  return { kind: 'unknown', paneKind }
}
```

**沒有 `require()` / 沒有 component import**。`disabledComponent` 是從 module def 來的 type 引用，registry 不持有實體。

- [ ] **Step 4: Run test, expect PASS**

```
cd spa && npx vitest run src/lib/module-registry.test.ts
```

- [ ] **Step 5: Commit**

```bash
git add spa/src/lib/module-registry.ts spa/src/lib/module-registry.test.ts
git commit -m "feat(spa): pane renderer resolver returns metadata"
```

---

## Task 1.6b — PaneLayoutRenderer fallback wiring

**Files:**
- Modify: `spa/src/components/PaneLayoutRenderer.tsx`（line 28 附近的 renderer caller，由通用 review #1 檢出）
- Test: `spa/src/components/PaneLayoutRenderer.test.tsx`（若無則新建；DOM render 實測）

**動機**：把 fallback 的 component 注入留在 render 層，避免 lib → UI 反向依賴。注意實際 caller 是 `PaneLayoutRenderer.tsx:28`，**不是 `Pane.tsx`**（通用 review A1）。

- [ ] **Step 1: 確認實際 caller 位置**

```bash
cd spa && grep -n "getPaneRenderer\|resolvePaneRenderer\|paneRenderer\b" src/components/PaneLayoutRenderer.tsx
```

對照 line 28 附近，確認改動點。

- [ ] **Step 2: Write failing test**

新建 `spa/src/components/PaneLayoutRenderer.test.tsx`，加 case：

```tsx
import { describe, it, expect, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import { PaneLayoutRenderer } from './PaneLayoutRenderer'
import {
  registerModule,
  unregisterModule,
} from '../lib/module-registry'
import { useModuleEnabledStore } from '../stores/useModuleEnabledStore'

const Comp = () => <div data-testid="real-pane">real</div>

describe('PaneLayoutRenderer disabled fallback', () => {
  beforeEach(() => {
    unregisterModule('test-mod')
    registerModule({
      id: 'test-mod', name: 'Test', disableable: true,
      panes: [{ kind: 'test-pane', component: Comp }],
    })
  })

  it('renders the actual component when enabled', () => {
    useModuleEnabledStore.getState().setEnabled('test-mod', true)
    const layout = { type: 'leaf', pane: { id: 'p1', content: { kind: 'test-pane' } } }
    render(<PaneLayoutRenderer layout={layout as never} /* …其他 props… */ />)
    expect(screen.getByTestId('real-pane')).toBeInTheDocument()
  })

  it('renders DisabledModulePlaceholder when disabled', () => {
    useModuleEnabledStore.getState().setEnabled('test-mod', false)
    const layout = { type: 'leaf', pane: { id: 'p1', content: { kind: 'test-pane' } } }
    render(<PaneLayoutRenderer layout={layout as never} /* …其他 props… */ />)
    expect(screen.queryByTestId('real-pane')).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: /enable/i })).toBeInTheDocument()
  })
})
```

> Layout fixture **必須是 `{type: 'leaf', pane: {id, content}}`**（通用 review B2），不是 `{id, content}`。

- [ ] **Step 3: Run test, expect FAIL**

PaneLayoutRenderer 還用既有 `getPaneRenderer` (沒 disable case)。

- [ ] **Step 4: Implement consumer migration**

在 `spa/src/components/PaneLayoutRenderer.tsx` 找 line 28 附近的 renderer 呼叫處，改成：

```tsx
import { resolvePaneRenderer } from '../lib/module-registry'
import { DisabledModulePlaceholder } from './modules/DisabledModulePlaceholder'

// …
const r = resolvePaneRenderer(content.kind)
if (r.kind === 'render') return <r.component pane={pane} isActive={isActive} />
if (r.kind === 'disabled') {
  const Cmp = r.customComponent ?? DisabledModulePlaceholder
  return <Cmp moduleId={r.moduleId} paneKind={r.paneKind} />
}
// r.kind === 'unknown'
return <UnknownPaneFallback paneKind={r.paneKind} />  // 既有 fallback
```

`DisabledModulePlaceholder` import 在 PaneLayoutRenderer（components 層 import components 層；OK）。`module-registry.ts` 內**仍不 import 任何 component**。

- [ ] **Step 5: Run test, expect PASS + 全測**

```
cd spa && npx vitest run
```

- [ ] **Step 6: Commit**

```bash
git add spa/src/components/PaneLayoutRenderer.tsx spa/src/components/PaneLayoutRenderer.test.tsx
git commit -m "feat(spa): pane layout renderer falls back to disabled placeholder"
```

---

## Task 1.7 — End-to-end disable scenarios

**Files:**
- Test: `spa/src/lib/__tests__/editor-disable-flow.test.tsx`（新整合測試）

- [ ] **Step 1: Write integration test**

新建檔案：

```tsx
import { describe, it, expect, beforeEach } from 'vitest'
import { registerBuiltinModules } from '../register-modules'
import { useModuleEnabledStore } from '../../stores/useModuleEnabledStore'
import { getDefaultOpener } from '../file-opener-registry'
import { resolvePaneRenderer } from '../module-registry'

const txtFile = { path: '/a.txt', name: 'a.txt', extension: 'txt', isDirectory: false }

describe('Editor disable flow', () => {
  beforeEach(() => {
    useModuleEnabledStore.setState({ enabledById: { editor: true } } as never, false)
    registerBuiltinModules()
  })

  it('text file opens via monaco-editor when editor enabled', () => {
    expect(getDefaultOpener(txtFile as never)?.id).toBe('monaco-editor')
  })

  it('disabling editor leaves no opener for text files', () => {
    useModuleEnabledStore.getState().setEnabled('editor', false)
    registerBuiltinModules()  // simulates reload
    expect(getDefaultOpener(txtFile as never)).toBeNull()
  })

  it('disabling editor returns placeholder renderer for editor pane kind', () => {
    useModuleEnabledStore.getState().setEnabled('editor', false)
    registerBuiltinModules()
    const Renderer = resolvePaneRenderer('editor')
    expect(Renderer?.displayName).toMatch(/DisabledPlaceholder/)
  })
})
```

- [ ] **Step 2: Run test, expect PASS（已實作完整流程）**

```
cd spa && npx vitest run src/lib/__tests__/editor-disable-flow.test.tsx
```

- [ ] **Step 3: Commit**

```bash
git add spa/src/lib/__tests__/editor-disable-flow.test.tsx
git commit -m "test(spa): integration test for editor module disable flow"
```

---

## Task 1.8 — Phase 1 verification

- [ ] **Step 1: Full test + lint + build**

```bash
cd spa && npx vitest run && pnpm run lint && pnpm run build
```

Expected：全綠。

- [ ] **Step 2: 開 PR**

```bash
git push -u origin worktree-worktree-editor-self-contained
gh pr create --title "feat(spa): editor module owns file openers + disabled placeholder" --body "$(cat <<'EOF'
## Summary

- 拆 register-modules.tsx (467 行 god file) 為 register-modules/ 子目錄 (orchestrator + editor-module + fs-backends + module-file-openers)
- file-opener-registry 改 owner-scoped (registerFileOpener 帶 ownerModuleId; unregisterByOwner 只清該 owner)
- ModuleDefinition 加 fileOpeners 欄位 + 預留 disabledComponent opt-in (lib 不持有 component 實體)
- Editor module 收編 image-preview / pdf-viewer / monaco-editor 三個 opener
- 新增 DisabledModulePlaceholder 元件 (components/modules/) — Editor 停用後既有 panes 顯示 placeholder
- resolvePaneRenderer 改回 RendererResolution metadata (lib → UI 反向依賴杜絕)
- PaneLayoutRenderer 在 render 層注入 disabled fallback component
- HMR dispose 全清 file-opener registry

## Test plan

- [ ] cd spa && pnpm install && npx vitest run && pnpm run lint && pnpm run build
- [ ] go test ./...
- [ ] 手動：停用 Editor → 重載 → 既有 editor tab 顯示 placeholder
- [ ] 手動：停用 Editor → terminal link 點 .txt 沒反應（無 opener）
- [ ] 手動：placeholder 內按 Enable → useModuleEnabledStore 切換生效
- [ ] 手動：HMR (改 editor-module.tsx 一個字) → opener 無重複 / 無 zombie

Spec: SPEC.md (rev 4, P1)
EOF
)"
```

- [ ] **Step 3: 委派 codex 兩輪 review（per CLAUDE.md）**

```bash
node "${CLAUDE_PLUGIN_ROOT:-$HOME/.claude/plugins/cache/openai-codex/codex/1.0.2}/scripts/codex-companion.mjs" review --background
node "${CLAUDE_PLUGIN_ROOT:-$HOME/.claude/plugins/cache/openai-codex/codex/1.0.2}/scripts/codex-companion.mjs" adversarial-review --background "P1 Editor self-containment"
```

依「Review 問題彙整」表格規則處理 finding，merge 後進 P2。

---

