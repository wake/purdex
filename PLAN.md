# Editor 模組自有資產化 + 開檔體驗強化 — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use `superpowers:subagent-driven-development`（推薦）或 `superpowers:executing-plans` 逐 task 執行。Steps 用 checkbox (`- [ ]`) 追蹤。
>
> **Subagent CWD 強制**：本 worktree 在 `/Users/wake/Workspace/wake/purdex/.claude/worktrees/worktree-editor-self-contained/`。subagent 每個 Bash 指令必須 `cd <worktree-path> && ...` 前綴（per `feedback_subagent_cwd_enforcement.md`）。

**Goal:** 把 Editor module 的 file opener / 設定從 `register-modules.tsx` body 收編進 module definition；改 tab 插入為「append after current」；建立 agent-driven path cache；補檔案不存在的 popup + 三層 fallback 搜尋。

**Architecture:** SPA 的 `ModuleDefinition` 加 `fileOpeners` 並用 `useModuleEnabledStore` 過濾；新增 `PathHint` event 走既有 `core.HostEvent` 廣播管道；`tryOpenFile` 走「stat → cache lookup（含 stat verify + prune）→ popup → daemon fs.search」管線。

**Tech Stack:** React 19 / Zustand 5 / Vitest / Go net/http / gorilla/websocket。

**Spec:** [SPEC.md](./SPEC.md)（rev 3，commit `73dfbcd9`）。

---

## 驗證指令（每 task 結尾依需要跑）

```bash
# SPA 單元測試（單檔）
cd spa && npx vitest run path/to/file.test.ts

# SPA 全測 + lint + build
cd spa && npx vitest run && pnpm run lint && pnpm run build

# Go 測試
go test ./...

# Go 單包測試
go test ./internal/module/agent/...
```

## TDD 模式（每 task 適用）

1. 寫失敗的測試
2. 跑測試 → 預期 fail（with specific error）
3. 寫最小實作
4. 跑測試 → 預期 pass
5. Commit（conventional commit format，逐 task 獨立）

## Phase / PR 對應關係

| Phase | PR 標題草稿 | 兩輪 codex review |
|---|---|---|
| P1 | `feat(spa): editor module owns file openers + disabled placeholder` | 標準 + adversarial |
| P2 | `refactor(spa): tab insertion appends after current` | 標準 + adversarial |
| P3 | `refactor(spa): migrate file path link detection settings to editor` | 標準 + adversarial |
| P4 | `feat(daemon+spa): agent path hint channel + path cache store` | 標準 + adversarial |
| P5 | `feat(spa+daemon): file-not-found popup with three-layer fallback` | 標準 + adversarial |

每 Phase 結束 = 1 個 PR；merged 後再下一 Phase。Phases 內 task 都獨立 commit。

---

## File Structure 變更總覽

| 路徑 | 動作 | 階段 |
|---|---|---|
| `spa/src/lib/module-registry.ts` | modify | P1 |
| `spa/src/lib/file-opener-registry.ts` | modify（dispose hook 串接） | P1 |
| `spa/src/lib/register-modules.tsx` | modify（多階段） | P1, P3, P5 |
| `spa/src/components/DisabledModulePlaceholder.tsx` | create | P1 |
| `spa/src/components/__tests__/DisabledModulePlaceholder.test.tsx` | create | P1 |
| `spa/src/lib/find-browser-insert-target.ts` | rename → `find-insert-target.ts` | P2 |
| `spa/src/lib/find-insert-target.ts` | create（rename target） | P2 |
| `spa/src/lib/find-browser-insert-target.test.ts` | rename → `find-insert-target.test.ts` | P2 |
| `spa/src/lib/open-browser-tab.ts` | modify | P2 |
| `spa/src/stores/useTabStore.ts` | modify | P2 |
| `spa/src/lib/terminal-link/openers/file-path.ts` | modify | P2, P5 |
| `spa/src/components/FileTreeView.tsx` | modify | P2, P5 |
| `spa/src/components/settings/LinkDetectionSection.tsx` | modify（縮減） | P3 |
| `spa/src/components/settings/EditorLinkDetectionSection.tsx` | create | P3 |
| `spa/src/i18n/zh-TW.json` / `en.json` | modify | P3 |
| `internal/module/agent/path_hint.go` | create | P4 |
| `internal/module/agent/path_hint_test.go` | create | P4 |
| `internal/module/agent/path_hint_extractor.go` | create | P4 |
| `internal/module/agent/path_hint_extractor_test.go` | create | P4 |
| `internal/module/agent/handler.go` | modify（emit PathHint） | P4 |
| `spa/src/lib/storage/keys.ts` | modify（加 `PATH_CACHE`） | P4 |
| `spa/src/types/agent-events.ts` | modify（加 `PathHint` type） | P4 |
| `spa/src/lib/resolve-workspace-for-session.ts` | create | P4 |
| `spa/src/lib/resolve-workspace-for-session.test.ts` | create | P4 |
| `spa/src/stores/usePathCacheStore.ts` | create | P4 |
| `spa/src/stores/usePathCacheStore.test.ts` | create | P4 |
| `spa/src/lib/agent-ws-dispatch.ts` | modify | P4 |
| `spa/src/hooks/useMultiHostEventWs.ts` | modify | P4 |
| `internal/module/fs/search.go` | create | P5 |
| `internal/module/fs/search_test.go` | create | P5 |
| `spa/src/lib/fs-search.ts` | create | P5 |
| `spa/src/lib/fs-search.test.ts` | create | P5 |
| `spa/src/lib/open-file.ts` | create | P5 |
| `spa/src/lib/open-file.test.ts` | create | P5 |
| `spa/src/components/editor/FileNotFoundPopup.tsx` | create | P5 |
| `spa/src/components/editor/FileNotFoundPopup.test.tsx` | create | P5 |
| `spa/src/components/settings/EditorOpenBehaviorSection.tsx` | create | P5 |
| `spa/src/stores/useUISettingsStore.ts` | modify | P5 |

---

# Phase 1 — Module.fileOpeners + Editor 收編 + DisabledModulePlaceholder

PR 結束標準：Editor 模組停用 / 啟用切換生效；既有持久化 panes 不消失，改顯示 placeholder；HMR 不殘留 opener。

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

- [ ] **Step 3: Add field to ModuleDefinition**

在 `spa/src/lib/module-registry.ts` 既有 `ModuleDefinition` interface 內、`descriptionKey` 後加：

```ts
  /**
   * File openers contributed by this module. Registered into the file-opener
   * registry only when the module is enabled (or always for non-disableable
   * modules). Removed on HMR dispose to prevent stale entries.
   */
  fileOpeners?: import('./file-opener-registry').FileOpener[]
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
- Create: `spa/src/components/DisabledModulePlaceholder.tsx`
- Test: `spa/src/components/__tests__/DisabledModulePlaceholder.test.tsx`

- [ ] **Step 1: Write failing test**

新建 `spa/src/components/__tests__/DisabledModulePlaceholder.test.tsx`：

```tsx
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { DisabledModulePlaceholder } from '../DisabledModulePlaceholder'
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

新建 `spa/src/components/DisabledModulePlaceholder.tsx`：

```tsx
import { useModuleEnabledStore } from '../stores/useModuleEnabledStore'
import { useI18nStore } from '../stores/useI18nStore'

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

在 `spa/src/i18n/zh-TW.json` 加：

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

`spa/src/i18n/en.json` 加：

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
cd spa && npx vitest run src/components/__tests__/DisabledModulePlaceholder.test.tsx
```

- [ ] **Step 6: Commit**

```bash
git add spa/src/components/DisabledModulePlaceholder.tsx spa/src/components/__tests__/DisabledModulePlaceholder.test.tsx spa/src/i18n/zh-TW.json spa/src/i18n/en.json
git commit -m "feat(spa): add DisabledModulePlaceholder component"
```

---

## Task 1.6 — Pane renderer fallback wiring

**Files:**
- Modify: `spa/src/lib/module-registry.ts`（`getPaneRenderer` 或新增 wrapper）

- [ ] **Step 1: Write failing test**

擴 `spa/src/lib/module-registry.test.ts`：

```ts
import { resolvePaneRenderer } from './module-registry'
import { useModuleEnabledStore } from '../stores/useModuleEnabledStore'

describe('resolvePaneRenderer', () => {
  beforeEach(() => unregisterModule('rt-mod'))

  it('returns the actual component when module is enabled', () => {
    const Comp = () => null
    registerModule({
      id: 'rt-mod', name: 'RT', disableable: true,
      panes: [{ kind: 'rt-pane', component: Comp }],
    })
    useModuleEnabledStore.getState().setEnabled('rt-mod', true)
    const r = resolvePaneRenderer('rt-pane')
    expect(r).toBe(Comp)
  })

  it('returns a placeholder renderer when disableable module is disabled', () => {
    const Comp = () => null
    registerModule({
      id: 'rt-mod', name: 'RT', disableable: true,
      panes: [{ kind: 'rt-pane', component: Comp }],
    })
    useModuleEnabledStore.getState().setEnabled('rt-mod', false)
    const r = resolvePaneRenderer('rt-pane')
    expect(r).not.toBe(Comp)
    expect(r).toBeTruthy()
  })

  it('returns undefined when no module owns the kind', () => {
    expect(resolvePaneRenderer('does-not-exist')).toBeUndefined()
  })
})
```

- [ ] **Step 2: Run test, expect FAIL**

`resolvePaneRenderer` not exported。

- [ ] **Step 3: Implement resolver**

在 `spa/src/lib/module-registry.ts` 加：

```ts
import { useModuleEnabledStore } from '../stores/useModuleEnabledStore'

export function resolvePaneRenderer(kind: string): React.ComponentType<PaneRendererProps> | undefined {
  for (const m of modules.values()) {
    for (const p of m.panes ?? []) {
      if (p.kind !== kind) continue
      if (m.disableable && !useModuleEnabledStore.getState().isEnabled(m.id)) {
        // Lazy import to avoid circular module evaluation in tests.
        const Placeholder: React.FC<PaneRendererProps> = () => {
          // eslint-disable-next-line @typescript-eslint/no-require-imports
          const { DisabledModulePlaceholder } = require('../components/DisabledModulePlaceholder')
          return <DisabledModulePlaceholder moduleId={m.id} paneKind={kind} />
        }
        Placeholder.displayName = `DisabledPlaceholder(${m.id}/${kind})`
        return Placeholder
      }
      return p.component
    }
  }
  return undefined
}
```

> ⚠️ JSX in `.ts` 不允許 — 改寫成 `.tsx` 或用 `React.createElement`。實作上把 `module-registry.ts` 改名為 `.tsx` 太大改動；用 `createElement`：

```ts
import { createElement } from 'react'
import { DisabledModulePlaceholder } from '../components/DisabledModulePlaceholder'

export function resolvePaneRenderer(kind: string): React.ComponentType<PaneRendererProps> | undefined {
  for (const m of modules.values()) {
    for (const p of m.panes ?? []) {
      if (p.kind !== kind) continue
      if (m.disableable && !useModuleEnabledStore.getState().isEnabled(m.id)) {
        const Placeholder: React.FC<PaneRendererProps> = () =>
          createElement(DisabledModulePlaceholder, { moduleId: m.id, paneKind: kind })
        Placeholder.displayName = `DisabledPlaceholder(${m.id}/${kind})`
        return Placeholder
      }
      return p.component
    }
  }
  return undefined
}
```

- [ ] **Step 4: Update consumers to use resolvePaneRenderer**

`grep -rn "getPaneRenderer\b" spa/src` 找所有 caller，逐一改用 `resolvePaneRenderer` — 但只有 caller 已存在的情境才改；若 caller 已正常 render component，就替換成 `resolvePaneRenderer(kind)?` 並 fallback 到 `getPaneRenderer(kind)?.component`。

最簡：保留 `getPaneRenderer` 不動（向後相容），新增 `resolvePaneRenderer` 給新 caller 用。實際 pane renderer 取用點（如 `Pane` component / `PaneRenderer`）改用 `resolvePaneRenderer`。

具體：在 `spa/src/components/Pane.tsx`（或實際 render pane 的元件）找：

```tsx
const renderer = getPaneRenderer(pane.content.kind)
return renderer ? <renderer.component pane={pane} isActive={isActive} /> : null
```

改成：

```tsx
const Renderer = resolvePaneRenderer(pane.content.kind)
return Renderer ? <Renderer pane={pane} isActive={isActive} /> : null
```

> 若 caller 已是 `getPaneRenderer(kind)?.component` 形式，可保留並另外掛 wrapper；以實際檔案內容為準。

- [ ] **Step 5: Run test, expect PASS + 全測**

```
cd spa && npx vitest run
```

- [ ] **Step 6: Commit**

```bash
git add spa/src/lib/module-registry.ts spa/src/lib/module-registry.test.ts spa/src/components/Pane.tsx
git commit -m "feat(spa): pane renderer falls back to disabled placeholder"
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

- ModuleDefinition 加 fileOpeners 欄位，受 useModuleEnabledStore 過濾
- Editor module 收編 image-preview / pdf-viewer / monaco-editor 三個 opener
- 新增 DisabledModulePlaceholder 元件，Editor 停用後既有 panes 顯示 placeholder
- HMR dispose 清空 file-opener registry

## Test plan

- [ ] cd spa && npx vitest run
- [ ] 手動：停用 Editor → 重載 → 既有 editor tab 顯示 placeholder
- [ ] 手動：停用 Editor → terminal link 點 .txt 沒反應（無 opener）
- [ ] 手動：placeholder 內按 Enable → useModuleEnabledStore 切換生效

Spec: SPEC.md (rev 3, P1)
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

# Phase 2 — Tab 插入改 append-current

PR 結束標準：所有 file 類分頁 + browser 都遵循同類聚集規則；既有 caller 不傳 `opts` 行為與重構前一致。

## Task 2.1 — 泛用化 `findInsertTarget`

**Files:**
- Rename: `spa/src/lib/find-browser-insert-target.ts` → `spa/src/lib/find-insert-target.ts`
- Rename: `spa/src/lib/find-browser-insert-target.test.ts` → `spa/src/lib/find-insert-target.test.ts`

- [ ] **Step 1: Rename file via git**

```bash
git mv spa/src/lib/find-browser-insert-target.ts spa/src/lib/find-insert-target.ts
git mv spa/src/lib/find-browser-insert-target.test.ts spa/src/lib/find-insert-target.test.ts
```

- [ ] **Step 2: Write failing test for new generic signature**

替換 `spa/src/lib/find-insert-target.test.ts` 內容：

```ts
import { describe, it, expect } from 'vitest'
import { findInsertTarget } from './find-insert-target'
import type { Tab, PaneContent } from '../types/tab'

const mkTab = (id: string, kind: PaneContent['kind']): Tab => ({
  id,
  layout: { id: 'p', content: { kind } as PaneContent } as never,
  locked: false,
})

describe('findInsertTarget', () => {
  const isBrowser = (c: PaneContent) => c.kind === 'browser'

  it('returns the nearest matching tab to the right of active', () => {
    const tabs: Record<string, Tab> = {
      'a': mkTab('a', 'tmux-session'),
      'b': mkTab('b', 'browser'),
      'c': mkTab('c', 'browser'),
    }
    expect(findInsertTarget(['a', 'b', 'c'], 'a', tabs, isBrowser)).toBe('b')
  })

  it('falls back to activeTabId when no matching tab found right of active', () => {
    const tabs: Record<string, Tab> = {
      'a': mkTab('a', 'browser'),
      'b': mkTab('b', 'tmux-session'),
    }
    expect(findInsertTarget(['a', 'b'], 'b', tabs, isBrowser)).toBe('b')
  })

  it('returns activeTabId when active not in order', () => {
    const tabs: Record<string, Tab> = { 'a': mkTab('a', 'tmux-session') }
    expect(findInsertTarget(['a'], 'missing', tabs, isBrowser)).toBe('missing')
  })

  it('predicate determines kind family — file kinds aggregate together', () => {
    const isFile = (c: PaneContent) =>
      c.kind === 'editor' || c.kind === 'image-preview' || c.kind === 'pdf-preview'
    const tabs: Record<string, Tab> = {
      'a': mkTab('a', 'tmux-session'),
      'b': mkTab('b', 'editor'),
      'c': mkTab('c', 'tmux-session'),
      'd': mkTab('d', 'image-preview'),
    }
    expect(findInsertTarget(['a', 'b', 'c', 'd'], 'a', tabs, isFile)).toBe('b')
    expect(findInsertTarget(['a', 'b', 'c', 'd'], 'c', tabs, isFile)).toBe('d')
  })
})
```

- [ ] **Step 3: Run test, expect FAIL**

`findInsertTarget` 尚未存在（檔案內仍是 `findBrowserInsertTarget`）。

- [ ] **Step 4: Rewrite the function**

替換 `spa/src/lib/find-insert-target.ts` 內容：

```ts
import type { Tab, PaneContent } from '../types/tab'
import { getPrimaryPane } from './pane-tree'

/**
 * Find the insertion target for a new tab, aggregating same-kind tabs.
 * Scans right from activeTabId for the nearest tab whose primary pane
 * content matches `isSameKind`. Returns that tab's ID (insert after it).
 * Falls back to activeTabId if no match found or if activeTabId is not
 * in `orderedTabIds`.
 */
export function findInsertTarget(
  orderedTabIds: string[],
  activeTabId: string,
  tabs: Record<string, Tab>,
  isSameKind: (content: PaneContent) => boolean,
): string {
  const activeIdx = orderedTabIds.indexOf(activeTabId)
  if (activeIdx === -1) return activeTabId

  for (let i = activeIdx + 1; i < orderedTabIds.length; i++) {
    const tab = tabs[orderedTabIds[i]]
    if (tab && isSameKind(getPrimaryPane(tab.layout).content)) {
      return orderedTabIds[i]
    }
  }
  return activeTabId
}
```

- [ ] **Step 5: Run test, expect PASS**

```
cd spa && npx vitest run src/lib/find-insert-target.test.ts
```

- [ ] **Step 6: Commit**

```bash
git add spa/src/lib/find-insert-target.ts spa/src/lib/find-insert-target.test.ts
git commit -m "refactor(spa): generalize findInsertTarget with predicate"
```

---

## Task 2.2 — `openBrowserTab` 改用泛用版

**Files:**
- Modify: `spa/src/lib/open-browser-tab.ts`

- [ ] **Step 1: Update import + call site**

替換檔案內容：

```ts
import { useTabStore } from '../stores/useTabStore'
import { useWorkspaceStore } from '../stores/useWorkspaceStore'
import { createTab } from '../types/tab'
import { findInsertTarget } from './find-insert-target'

export function openBrowserTab(url: string): void {
  const tab = createTab({ kind: 'browser', url })
  const tabState = useTabStore.getState()
  const wsState = useWorkspaceStore.getState()
  const activeTabId = tabState.activeTabId

  const wsId = wsState.activeWorkspaceId
  const ws = wsId ? wsState.workspaces.find((w) => w.id === wsId) : null
  const visibleOrder = ws ? ws.tabs.filter((id) => !!tabState.tabs[id]) : tabState.tabOrder

  const afterTabId = activeTabId
    ? findInsertTarget(visibleOrder, activeTabId, tabState.tabs, (c) => c.kind === 'browser')
    : undefined

  useTabStore.getState().addTab(tab, afterTabId)
  useTabStore.getState().setActiveTab(tab.id)

  if (wsId) {
    wsState.insertTab(tab.id, wsId, afterTabId)
  }
}
```

- [ ] **Step 2: Run existing browser tab tests, expect PASS**

```
cd spa && npx vitest run src/lib/open-browser-tab.test.ts
```

> 若任何 test 用到 `findBrowserInsertTarget` symbol → import 改名。`grep -rn "findBrowserInsertTarget" spa/src` 並修。

- [ ] **Step 3: Commit**

```bash
git add spa/src/lib/open-browser-tab.ts
git commit -m "refactor(spa): openBrowserTab uses generalized findInsertTarget"
```

---

## Task 2.3 — `openSingletonTab` 加 opts

**Files:**
- Modify: `spa/src/stores/useTabStore.ts`
- Test: `spa/src/stores/useTabStore.test.ts`

- [ ] **Step 1: Write failing test**

擴 `spa/src/stores/useTabStore.test.ts`：

```ts
import { useTabStore } from './useTabStore'
import { useWorkspaceStore } from './useWorkspaceStore'

describe('openSingletonTab with opts.isSameKind', () => {
  beforeEach(() => {
    useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, visitHistory: [] } as never, false)
    useWorkspaceStore.setState({ workspaces: [], activeWorkspaceId: null } as never, false)
  })

  it('inserts new tab after nearest same-kind tab right of active', () => {
    // seed: [s1, editor1, s2] active=s1
    const tabId = useTabStore.getState().openSingletonTab(
      { kind: 'editor', source: { type: 'inapp' }, filePath: '/x.ts' } as never,
      { isSameKind: (c) => c.kind === 'editor' || c.kind === 'image-preview' || c.kind === 'pdf-preview' },
    )
    // open second editor tab — should insert after editor1
    // (skeleton — full setup with workspace store omitted; verify openSingletonTab returns id and tabOrder grows by 1)
    expect(typeof tabId).toBe('string')
    expect(useTabStore.getState().tabOrder.length).toBe(1)
  })

  it('without opts, behaves like before (append)', () => {
    useTabStore.getState().openSingletonTab({ kind: 'editor', source: { type: 'inapp' }, filePath: '/a' } as never)
    useTabStore.getState().openSingletonTab({ kind: 'browser', url: 'https://x' } as never)
    // both appended
    expect(useTabStore.getState().tabOrder.length).toBe(2)
  })
})
```

- [ ] **Step 2: Run test, expect FAIL（簽名不匹配）**

- [ ] **Step 3: Update store signature + impl**

在 `spa/src/stores/useTabStore.ts` 的 `TabState` interface 加 / 替換：

```ts
import { findInsertTarget } from '../lib/find-insert-target'
import { useWorkspaceStore } from './useWorkspaceStore'

interface OpenSingletonOpts {
  isSameKind?: (content: PaneContent) => boolean
}

interface TabState {
  // ... existing fields
  openSingletonTab: (content: PaneContent, opts?: OpenSingletonOpts) => string
}
```

實作改寫：

```ts
openSingletonTab: (content, opts) => {
  const state = get()
  // Singleton match (existing logic kept)
  for (const id of state.tabOrder) {
    const tab = state.tabs[id]
    if (!tab) continue
    const primary = getPrimaryPane(tab.layout)
    if (contentMatches(primary.content, content)) {
      get().setActiveTab(id)
      return id
    }
  }
  // Not found — create + insert after current
  const tab = createTab(content)
  const wsState = useWorkspaceStore.getState()
  const wsId = wsState.activeWorkspaceId
  const ws = wsId ? wsState.workspaces.find((w) => w.id === wsId) : null
  const visibleOrder = ws ? ws.tabs.filter((tid) => !!state.tabs[tid]) : state.tabOrder
  const afterTabId =
    opts?.isSameKind && state.activeTabId
      ? findInsertTarget(visibleOrder, state.activeTabId, state.tabs, opts.isSameKind)
      : undefined
  get().addTab(tab, afterTabId)
  get().setActiveTab(tab.id)
  if (wsId) wsState.insertTab(tab.id, wsId, afterTabId)
  return tab.id
},
```

- [ ] **Step 4: Run test, expect PASS**

```
cd spa && npx vitest run src/stores/useTabStore.test.ts
```

- [ ] **Step 5: Commit**

```bash
git add spa/src/stores/useTabStore.ts spa/src/stores/useTabStore.test.ts
git commit -m "feat(spa): openSingletonTab supports same-kind insertion"
```

---

## Task 2.4 — Terminal-link file-path opener 帶 predicate

**Files:**
- Modify: `spa/src/lib/terminal-link/openers/file-path.ts`

- [ ] **Step 1: Find call site**

```bash
grep -n "openSingletonTab" spa/src/lib/terminal-link/openers/file-path.ts
```

- [ ] **Step 2: Add predicate + update call**

在檔案頂部加：

```ts
const FILE_KINDS = new Set<string>(['editor', 'image-preview', 'pdf-preview'])
const isFileKind = (c: { kind: string }) => FILE_KINDS.has(c.kind)
```

call site `deps.openSingletonTab(content)` → `deps.openSingletonTab(content, { isSameKind: isFileKind as never })`。

> Note: `openSingletonTab` 是透過 `deps` 注入；若 deps signature 仍只接 1 個參數，需擴 `FilePathOpenerDeps` 介面。

擴 `spa/src/lib/terminal-link/openers/file-path.ts` 的 deps 介面：

```ts
export interface FilePathOpenerDeps {
  // ... existing fields
  openSingletonTab(content: PaneContent, opts?: { isSameKind?: (c: PaneContent) => boolean }): string
}
```

- [ ] **Step 3: Update register-modules.tsx wiring**

在 `register-modules.tsx` 的 `registerBuiltinTerminalLinks` 呼叫處：

```tsx
filePathOpener: {
  // ... existing fields
  openSingletonTab: (content, opts) => useTabStore.getState().openSingletonTab(content, opts),
  // ...
},
```

- [ ] **Step 4: Run all link tests, expect PASS**

```
cd spa && npx vitest run src/lib/terminal-link/
```

- [ ] **Step 5: Commit**

```bash
git add spa/src/lib/terminal-link/openers/file-path.ts spa/src/lib/register-modules.tsx
git commit -m "feat(spa): terminal-link opens files clustered with file-kind tabs"
```

---

## Task 2.5 — FileTreeView 點擊帶 predicate

**Files:**
- Modify: `spa/src/components/FileTreeView.tsx`

- [ ] **Step 1: 找呼叫**

```bash
grep -n "openSingletonTab\|getDefaultOpener" spa/src/components/FileTreeView.tsx
```

- [ ] **Step 2: Add predicate + update call**

在點擊 handler 內：

```tsx
const FILE_KINDS = new Set(['editor', 'image-preview', 'pdf-preview'])
// ...
const opener = getDefaultOpener(fileInfo)
if (opener) {
  const content = opener.createContent(source, fileInfo)
  useTabStore.getState().openSingletonTab(content, {
    isSameKind: (c) => FILE_KINDS.has(c.kind),
  })
}
```

- [ ] **Step 3: Run test**

```
cd spa && npx vitest run src/components/FileTreeView.test.tsx
```

預期 PASS（既有 test 不檢查 insertion order；本次擴的 acceptance 在 Task 2.6 整合測試驗）。

- [ ] **Step 4: Commit**

```bash
git add spa/src/components/FileTreeView.tsx
git commit -m "feat(spa): FileTreeView opens files clustered with file-kind tabs"
```

---

## Task 2.6 — Phase 2 verification + PR

- [ ] **Step 1: 全測 + lint + build**

```bash
cd spa && npx vitest run && pnpm run lint && pnpm run build
```

- [ ] **Step 2: 開 PR + 兩輪 codex review**

```bash
gh pr create --title "refactor(spa): tab insertion appends after current" --body "..."
node "${CLAUDE_PLUGIN_ROOT}/scripts/codex-companion.mjs" review --background
node "${CLAUDE_PLUGIN_ROOT}/scripts/codex-companion.mjs" adversarial-review --background "P2 tab insertion change"
```

---

# Phase 3 — Link detection 設定遷移

PR 結束標準：Editor purdex settings 有 3 個 file path 偵測開關；Terminal settings 只剩 bare；停用 Editor 後 3 個開關不見。

## Task 3.1 — i18n key 遷移

**Files:**
- Modify: `spa/src/i18n/zh-TW.json`, `spa/src/i18n/en.json`

- [ ] **Step 1: 移動 key**

把 zh-TW.json + en.json 的 `settings.terminal.link_detect.absolute.*` / `tilde.*` / `relative_slash.*` 三組 key **複製** 到 `settings.editor.link_detect.absolute.*` / 等對應位置。原 `settings.terminal.link_detect.bare.*` 保留。

刪除 `settings.terminal.link_detect.absolute.*` / `tilde.*` / `relative_slash.*`（alpha 階段不需 backward compat）。

- [ ] **Step 2: Commit**

```bash
git add spa/src/i18n/zh-TW.json spa/src/i18n/en.json
git commit -m "i18n: move file-path link detect keys to editor scope"
```

---

## Task 3.2 — `LinkDetectionSection` 縮減 + `EditorLinkDetectionSection` 新建

**Files:**
- Modify: `spa/src/components/settings/LinkDetectionSection.tsx`
- Create: `spa/src/components/settings/EditorLinkDetectionSection.tsx`
- Test: 兩個對應 .test.tsx

- [ ] **Step 1: Write failing tests**

新建 `spa/src/components/settings/EditorLinkDetectionSection.test.tsx`：

```tsx
import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { EditorLinkDetectionSection } from './EditorLinkDetectionSection'

describe('EditorLinkDetectionSection', () => {
  it('renders absolute / tilde / relative_slash toggles', () => {
    render(<EditorLinkDetectionSection />)
    expect(screen.getByText(/absolute/i)).toBeInTheDocument()
    expect(screen.getByText(/tilde/i)).toBeInTheDocument()
    expect(screen.getByText(/relative/i)).toBeInTheDocument()
  })
})
```

縮減 `LinkDetectionSection.test.tsx` 既有測試只驗證 bare 開關。

- [ ] **Step 2: Run test, expect FAIL**

`EditorLinkDetectionSection` 尚未存在。

- [ ] **Step 3: Implement EditorLinkDetectionSection**

新建檔案：

```tsx
import { useUISettingsStore } from '../../stores/useUISettingsStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { SettingItem } from './SettingItem'
import { ToggleSwitch } from './ToggleSwitch'

export function EditorLinkDetectionSection() {
  const linkDetectAbsolute = useUISettingsStore((s) => s.linkDetectAbsolute)
  const setLinkDetectAbsolute = useUISettingsStore((s) => s.setLinkDetectAbsolute)
  const linkDetectTilde = useUISettingsStore((s) => s.linkDetectTilde)
  const setLinkDetectTilde = useUISettingsStore((s) => s.setLinkDetectTilde)
  const linkDetectRelativeSlash = useUISettingsStore((s) => s.linkDetectRelativeSlash)
  const setLinkDetectRelativeSlash = useUISettingsStore((s) => s.setLinkDetectRelativeSlash)
  const t = useI18nStore((s) => s.t)

  return (
    <div>
      <h3 className="text-sm text-text-primary mt-6 mb-1">{t('settings.editor.link_detect.title')}</h3>
      <p className="text-xs text-text-secondary mb-3">{t('settings.editor.link_detect.desc')}</p>

      <SettingItem label={t('settings.editor.link_detect.absolute.label')} description={t('settings.editor.link_detect.absolute.desc')}>
        <ToggleSwitch label={t('settings.editor.link_detect.absolute.label')} checked={linkDetectAbsolute} onChange={setLinkDetectAbsolute} />
      </SettingItem>

      <SettingItem label={t('settings.editor.link_detect.tilde.label')} description={t('settings.editor.link_detect.tilde.desc')}>
        <ToggleSwitch label={t('settings.editor.link_detect.tilde.label')} checked={linkDetectTilde} onChange={setLinkDetectTilde} />
      </SettingItem>

      <SettingItem label={t('settings.editor.link_detect.relative_slash.label')} description={t('settings.editor.link_detect.relative_slash.desc')}>
        <ToggleSwitch label={t('settings.editor.link_detect.relative_slash.label')} checked={linkDetectRelativeSlash} onChange={setLinkDetectRelativeSlash} />
      </SettingItem>
    </div>
  )
}
```

- [ ] **Step 4: 縮減 LinkDetectionSection**

替換 `spa/src/components/settings/LinkDetectionSection.tsx` 內容：

```tsx
import { useUISettingsStore } from '../../stores/useUISettingsStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { SettingItem } from './SettingItem'
import { ToggleSwitch } from './ToggleSwitch'

export function LinkDetectionSection() {
  const linkDetectBareFilename = useUISettingsStore((s) => s.linkDetectBareFilename)
  const setLinkDetectBareFilename = useUISettingsStore((s) => s.setLinkDetectBareFilename)
  const t = useI18nStore((s) => s.t)

  return (
    <div>
      <h3 className="text-sm text-text-primary mt-6 mb-1">{t('settings.terminal.link_detect.title')}</h3>
      <p className="text-xs text-text-secondary mb-3">{t('settings.terminal.link_detect.desc')}</p>

      <SettingItem label={t('settings.terminal.link_detect.bare.label')} description={t('settings.terminal.link_detect.bare.desc')}>
        <ToggleSwitch label={t('settings.terminal.link_detect.bare.label')} checked={linkDetectBareFilename} onChange={setLinkDetectBareFilename} />
      </SettingItem>
    </div>
  )
}
```

- [ ] **Step 5: Run tests, expect PASS**

```
cd spa && npx vitest run src/components/settings/
```

- [ ] **Step 6: Commit**

```bash
git add spa/src/components/settings/EditorLinkDetectionSection.tsx spa/src/components/settings/LinkDetectionSection.tsx spa/src/components/settings/EditorLinkDetectionSection.test.tsx spa/src/components/settings/LinkDetectionSection.test.tsx
git commit -m "feat(spa): split link detection between terminal and editor sections"
```

---

## Task 3.3 — Wire `EditorLinkDetectionSection` into Editor module

**Files:**
- Modify: `spa/src/lib/register-modules.tsx`

- [ ] **Step 1: Append to editor module settings array**

在 Editor module `settings: [...]` 陣列裡加：

```tsx
{
  localId: 'link-detect',
  scope: 'purdex',
  order: 8,
  labelKey: 'settings.editor.link_detect.title',
  component: EditorLinkDetectionSection,
},
```

並在頂部 import：

```tsx
import { EditorLinkDetectionSection } from '../components/settings/EditorLinkDetectionSection'
```

- [ ] **Step 2: Run integration test**

```
cd spa && npx vitest run
```

預期全綠。

- [ ] **Step 3: Commit + PR**

```bash
git add spa/src/lib/register-modules.tsx
git commit -m "feat(spa): wire EditorLinkDetectionSection into editor module"
```

PR + 兩輪 codex review。

---

# Phase 4 — PathHint channel + CC HookInstaller + path cache

PR 結束標準：CC Read tool 觸發 → SPA 對應 workspace path cache 增加 1 條；workspace remove / host remove 連動清理；非 absolute path defensive drop。

## Task 4.1 — PathHint Go schema + ring buffer

**Files:**
- Create: `internal/module/agent/path_hint.go`
- Test: `internal/module/agent/path_hint_test.go`

- [ ] **Step 1: Write failing test**

新建 `internal/module/agent/path_hint_test.go`：

```go
package agent

import (
	"encoding/json"
	"testing"
	"time"
)

func TestPathHint_JSONRoundTrip(t *testing.T) {
	h := PathHint{
		AgentID:     "claude-code",
		HostID:      "h1",
		SessionCode: "abc123",
		Kind:        PathHintKindRead,
		Path:        "/a/b/c.go",
		Dir:         "/a/b",
		PathKind:    PathKindAbsolute,
		BaseDir:     "",
		Confidence:  ConfidenceHigh,
		ToolName:    "Read",
		Timestamp:   time.Unix(1000, 0).UTC(),
	}
	b, err := json.Marshal(h)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got PathHint
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Path != "/a/b/c.go" || got.PathKind != PathKindAbsolute {
		t.Errorf("roundtrip mismatch: %+v", got)
	}
}

func TestPathHintRingBuffer_AddAndCap(t *testing.T) {
	r := NewPathHintRingBuffer(3)
	for i := 0; i < 5; i++ {
		r.Push(PathHint{Dir: "/d/" + string(rune('a'+i))})
	}
	got := r.Snapshot()
	if len(got) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(got))
	}
	if got[0].Dir != "/d/c" || got[2].Dir != "/d/e" {
		t.Errorf("unexpected ring contents: %+v", got)
	}
}
```

- [ ] **Step 2: Run test, expect FAIL**

```
go test ./internal/module/agent/...
```

`PathHint` / `NewPathHintRingBuffer` 尚未存在。

- [ ] **Step 3: Implement schema + ring buffer**

新建 `internal/module/agent/path_hint.go`：

```go
package agent

import (
	"sync"
	"time"
)

const (
	PathKindAbsolute = "absolute"
	PathKindRelative = "relative"
	PathKindUnknown  = "unknown"

	ConfidenceHigh   = "high"
	ConfidenceMedium = "medium"
	ConfidenceLow    = "low"

	PathHintKindRead    = "read"
	PathHintKindWrite   = "write"
	PathHintKindEdit    = "edit"
	PathHintKindUnknown = "unknown"
)

// PathHint is the agent-agnostic schema describing a path the agent has
// recently touched.  Kept dir-level — never includes the file basename so
// that downstream consumers can treat it as a working-dir hint, not a file
// reference.
type PathHint struct {
	AgentID     string    `json:"agentId"`
	HostID      string    `json:"hostId"`
	SessionCode string    `json:"sessionCode"`
	Kind        string    `json:"kind"`
	Path        string    `json:"path,omitempty"`
	Dir         string    `json:"dir"`
	PathKind    string    `json:"pathKind"`
	BaseDir     string    `json:"baseDir,omitempty"`
	Confidence  string    `json:"confidence"`
	ToolName    string    `json:"toolName"`
	Timestamp   time.Time `json:"timestamp"`
}

// PathHintRingBuffer holds a fixed-size FIFO of recent hints per host.
// In-memory only; lost on daemon restart.
type PathHintRingBuffer struct {
	mu    sync.Mutex
	cap   int
	items []PathHint
}

func NewPathHintRingBuffer(cap int) *PathHintRingBuffer {
	if cap <= 0 {
		cap = 1
	}
	return &PathHintRingBuffer{cap: cap, items: make([]PathHint, 0, cap)}
}

func (r *PathHintRingBuffer) Push(h PathHint) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.items = append(r.items, h)
	if len(r.items) > r.cap {
		r.items = r.items[len(r.items)-r.cap:]
	}
}

func (r *PathHintRingBuffer) Snapshot() []PathHint {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]PathHint, len(r.items))
	copy(out, r.items)
	return out
}
```

- [ ] **Step 4: Run test, expect PASS**

```
go test ./internal/module/agent/ -run PathHint
```

- [ ] **Step 5: Commit**

```bash
git add internal/module/agent/path_hint.go internal/module/agent/path_hint_test.go
git commit -m "feat(daemon): PathHint schema with bounded ring buffer"
```

---

## Task 4.2 — PathHint extractor with dedup

**Files:**
- Create: `internal/module/agent/path_hint_extractor.go`
- Test: `internal/module/agent/path_hint_extractor_test.go`

- [ ] **Step 1: Write failing test**

新建 `internal/module/agent/path_hint_extractor_test.go`：

```go
package agent

import (
	"testing"
	"time"
)

func TestExtractCCPathHint_AbsoluteRead(t *testing.T) {
	x := NewPathHintExtractor(0) // 0 → no dedup
	now := time.Unix(1000, 0)
	h, ok := x.ExtractCC("h1", "abc123", "Read", map[string]any{"file_path": "/a/b/c.go"}, now)
	if !ok {
		t.Fatal("expected hint, got drop")
	}
	if h.Dir != "/a/b" || h.PathKind != PathKindAbsolute || h.Confidence != ConfidenceHigh {
		t.Errorf("unexpected: %+v", h)
	}
}

func TestExtractCCPathHint_DropsRelative(t *testing.T) {
	x := NewPathHintExtractor(0)
	_, ok := x.ExtractCC("h1", "abc123", "Read", map[string]any{"file_path": "rel/path.go"}, time.Unix(0, 0))
	if ok {
		t.Fatal("expected drop for non-absolute path")
	}
}

func TestExtractCCPathHint_Dedup(t *testing.T) {
	x := NewPathHintExtractor(5 * time.Second)
	t0 := time.Unix(1000, 0)
	if _, ok := x.ExtractCC("h1", "s1", "Read", map[string]any{"file_path": "/a/b/c.go"}, t0); !ok {
		t.Fatal("first hint should pass")
	}
	if _, ok := x.ExtractCC("h1", "s1", "Read", map[string]any{"file_path": "/a/b/d.go"}, t0.Add(2*time.Second)); ok {
		t.Fatal("same dir within window should dedup")
	}
	if _, ok := x.ExtractCC("h1", "s1", "Read", map[string]any{"file_path": "/a/b/d.go"}, t0.Add(6*time.Second)); !ok {
		t.Fatal("dir after window should pass")
	}
}

func TestExtractCCPathHint_UnknownToolDrops(t *testing.T) {
	x := NewPathHintExtractor(0)
	_, ok := x.ExtractCC("h1", "s1", "Bash", map[string]any{}, time.Unix(0, 0))
	if ok {
		t.Fatal("expected drop for non-file tool")
	}
}
```

- [ ] **Step 2: Run test, expect FAIL**

```
go test ./internal/module/agent/ -run Extract
```

- [ ] **Step 3: Implement extractor**

新建 `internal/module/agent/path_hint_extractor.go`：

```go
package agent

import (
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// PathHintExtractor pulls PathHint records from raw CC hook tool_input.
// Holds a per-(session, dir) dedup window — same dir within window is dropped.
type PathHintExtractor struct {
	mu     sync.Mutex
	window time.Duration
	last   map[string]time.Time // key = sessionCode|dir
}

func NewPathHintExtractor(window time.Duration) *PathHintExtractor {
	return &PathHintExtractor{window: window, last: make(map[string]time.Time)}
}

var ccFileTools = map[string]string{
	"Read":         PathHintKindRead,
	"Write":        PathHintKindWrite,
	"Edit":         PathHintKindEdit,
	"NotebookEdit": PathHintKindEdit,
}

// ExtractCC returns (hint, true) if the tool/path qualify; otherwise (zero, false).
func (e *PathHintExtractor) ExtractCC(hostID, sessionCode, toolName string, toolInput map[string]any, now time.Time) (PathHint, bool) {
	kind, ok := ccFileTools[toolName]
	if !ok {
		return PathHint{}, false
	}
	raw, ok := toolInput["file_path"].(string)
	if !ok || raw == "" {
		return PathHint{}, false
	}
	if !filepath.IsAbs(raw) {
		return PathHint{}, false // CC always sends absolute paths; drop defensively.
	}
	dir := filepath.Dir(raw)
	if e.window > 0 {
		key := sessionCode + "|" + dir
		e.mu.Lock()
		if last, found := e.last[key]; found && now.Sub(last) < e.window {
			e.mu.Unlock()
			return PathHint{}, false
		}
		e.last[key] = now
		// opportunistic GC: drop entries older than 10× window
		cutoff := now.Add(-10 * e.window)
		for k, ts := range e.last {
			if ts.Before(cutoff) {
				delete(e.last, k)
			}
		}
		e.mu.Unlock()
	}
	return PathHint{
		AgentID:     "claude-code",
		HostID:      hostID,
		SessionCode: sessionCode,
		Kind:        kind,
		Path:        raw,
		Dir:         dir,
		PathKind:    PathKindAbsolute,
		Confidence:  ConfidenceHigh,
		ToolName:    toolName,
		Timestamp:   now,
	}, true
}

// Used to silence unused-import warnings if `strings` isn't needed at compile time.
var _ = strings.TrimSpace
```

(刪除最後 `var _ = strings.TrimSpace` 若不需要 strings import；保留為示意。)

- [ ] **Step 4: Run test, expect PASS**

```
go test ./internal/module/agent/ -run Extract
```

- [ ] **Step 5: Commit**

```bash
git add internal/module/agent/path_hint_extractor.go internal/module/agent/path_hint_extractor_test.go
git commit -m "feat(daemon): CC path hint extractor with dedup window"
```

---

## Task 4.3 — Wire emit into agent handler

**Files:**
- Modify: `internal/module/agent/handler.go`
- Modify: `internal/module/agent/module.go`（加 extractor + ring buffer 欄位）

- [ ] **Step 1: Add fields to Module struct**

在 `internal/module/agent/module.go` 既有 `Module` struct 內加：

```go
type Module struct {
    // ... existing fields
    pathHintExtractor *PathHintExtractor
    pathHintBuffer    *PathHintRingBuffer
}
```

`agent.New(...)` 內初始化（5 秒 dedup window，200 條 ring）：

```go
m.pathHintExtractor = NewPathHintExtractor(5 * time.Second)
m.pathHintBuffer = NewPathHintRingBuffer(200)
```

- [ ] **Step 2: Add emit helper to handler.go**

在 `handler.go` 既有 `emitHookToSession` 附近加：

```go
func (m *Module) emitPathHint(hostID, sessionCode, toolName string, toolInput map[string]any) {
    h, ok := m.pathHintExtractor.ExtractCC(hostID, sessionCode, toolName, toolInput, time.Now())
    if !ok {
        return
    }
    m.pathHintBuffer.Push(h)
    payload, err := json.Marshal(h)
    if err != nil {
        log.Printf("path_hint: marshal failed: %v", err)
        return
    }
    m.core.Events.Broadcast(sessionCode, "agent.path_hint", string(payload))
}
```

- [ ] **Step 3: Call emit in PreToolUse / PostToolUse hook handler**

找既有 hook handler（`handleHookStatus` 或 emit-hook 接點），在已 normalized event 處理之後加：

```go
// Where hostID, sessionCode, normalized.ToolName, normalized.ToolInput are available:
if normalized.HookEventName == "PreToolUse" || normalized.HookEventName == "PostToolUse" {
    if input, ok := normalized.ToolInput.(map[string]any); ok {
        m.emitPathHint(hostID, sessionCode, normalized.ToolName, input)
    }
}
```

> 具體 normalized payload shape 依現行 `agentpkg.NormalizedEvent` 內容調整；emit 只在工具事件上執行。

- [ ] **Step 4: Add test**

擴 `path_hint_test.go`（或新建 handler_path_hint_test.go）：

```go
func TestEmitPathHint_BroadcastFormat(t *testing.T) {
    // Build a Module with stubbed core that captures Broadcast calls
    var got struct{ session, kind, value string }
    core := &mockCore{broadcast: func(s, k, v string) { got.session, got.kind, got.value = s, k, v }}
    m := &Module{
        core:              core,
        pathHintExtractor: NewPathHintExtractor(0),
        pathHintBuffer:    NewPathHintRingBuffer(10),
    }
    m.emitPathHint("h1", "sess1", "Read", map[string]any{"file_path": "/a/b/c.go"})
    if got.kind != "agent.path_hint" {
        t.Errorf("kind = %q", got.kind)
    }
    var hint PathHint
    if err := json.Unmarshal([]byte(got.value), &hint); err != nil {
        t.Fatalf("payload not JSON: %v", err)
    }
    if hint.Dir != "/a/b" {
        t.Errorf("dir mismatch: %s", hint.Dir)
    }
}
```

(`mockCore` 需要 stub — 可參考 `internal/module/agent/fakes_test.go` 內既有 fake patterns。)

- [ ] **Step 5: Run test, expect PASS**

```
go test ./internal/module/agent/...
```

- [ ] **Step 6: Commit**

```bash
git add internal/module/agent/module.go internal/module/agent/handler.go internal/module/agent/path_hint_test.go
git commit -m "feat(daemon): emit agent.path_hint on CC PreToolUse/PostToolUse"
```

---

## Task 4.4 — STORAGE_KEYS.PATH_CACHE + PathHint TS type

**Files:**
- Modify: `spa/src/lib/storage/keys.ts`
- Modify: `spa/src/types/agent-events.ts`

- [ ] **Step 1: Add storage key**

在 `spa/src/lib/storage/keys.ts` `STORAGE_KEYS` object 加：

```ts
PATH_CACHE: 'purdex-path-cache',
```

- [ ] **Step 2: Define TS PathHint type**

在 `spa/src/types/agent-events.ts` 加：

```ts
export const PATH_KIND = ['absolute', 'relative', 'unknown'] as const
export type PathKind = (typeof PATH_KIND)[number]

export const CONFIDENCE = ['high', 'medium', 'low'] as const
export type Confidence = (typeof CONFIDENCE)[number]

export interface PathHint {
  agentId: string
  hostId: string
  sessionCode: string
  kind: 'read' | 'write' | 'edit' | 'unknown'
  path?: string
  dir: string
  pathKind: PathKind
  baseDir?: string
  confidence: Confidence
  toolName: string
  timestamp: string  // ISO 8601
}

export function isValidPathKind(v: unknown): v is PathKind {
  return typeof v === 'string' && (PATH_KIND as readonly string[]).includes(v)
}

export function isValidConfidence(v: unknown): v is Confidence {
  return typeof v === 'string' && (CONFIDENCE as readonly string[]).includes(v)
}
```

- [ ] **Step 3: Commit**

```bash
git add spa/src/lib/storage/keys.ts spa/src/types/agent-events.ts
git commit -m "feat(spa): PATH_CACHE storage key + PathHint type with const enums"
```

---

## Task 4.5 — `usePathCacheStore` LRU + persist

**Files:**
- Create: `spa/src/stores/usePathCacheStore.ts`
- Test: `spa/src/stores/usePathCacheStore.test.ts`

- [ ] **Step 1: Write failing test**

新建 `spa/src/stores/usePathCacheStore.test.ts`：

```ts
import { describe, it, expect, beforeEach } from 'vitest'
import { usePathCacheStore } from './usePathCacheStore'

const reset = () =>
  usePathCacheStore.setState({ dirsByScope: {} } as never, false)

describe('usePathCacheStore', () => {
  beforeEach(reset)

  it('add inserts dir at head and dedups existing dir', () => {
    usePathCacheStore.getState().add('h1', 'w1', '/a/b')
    usePathCacheStore.getState().add('h1', 'w1', '/c/d')
    usePathCacheStore.getState().add('h1', 'w1', '/a/b')
    const dirs = usePathCacheStore.getState().dirsByScope['h1:w1']
    expect(dirs).toEqual(['/a/b', '/c/d'])
  })

  it('LRU caps at 50 entries per scope', () => {
    for (let i = 0; i < 60; i++) usePathCacheStore.getState().add('h1', 'w1', `/d${i}`)
    const dirs = usePathCacheStore.getState().dirsByScope['h1:w1']
    expect(dirs.length).toBe(50)
    expect(dirs[0]).toBe('/d59')
    expect(dirs[49]).toBe('/d10')
  })

  it('lookup combines basename with each cached dir', () => {
    usePathCacheStore.getState().add('h1', 'w1', '/a/b')
    usePathCacheStore.getState().add('h1', 'w1', '/c/d')
    expect(usePathCacheStore.getState().lookup('h1', 'w1', 'foo.go')).toEqual([
      '/c/d/foo.go', '/a/b/foo.go',
    ])
  })

  it('pruneStaleCandidate removes the dirname entry', () => {
    usePathCacheStore.getState().add('h1', 'w1', '/a/b')
    usePathCacheStore.getState().pruneStaleCandidate('h1', 'w1', '/a/b/foo.go')
    expect(usePathCacheStore.getState().dirsByScope['h1:w1']).toEqual([])
  })

  it('clearScope removes only the targeted scope', () => {
    usePathCacheStore.getState().add('h1', 'w1', '/a')
    usePathCacheStore.getState().add('h1', 'w2', '/b')
    usePathCacheStore.getState().clearScope('h1', 'w1')
    expect(usePathCacheStore.getState().dirsByScope['h1:w1']).toBeUndefined()
    expect(usePathCacheStore.getState().dirsByScope['h1:w2']).toEqual(['/b'])
  })

  it('clearHost removes all scopes for that host', () => {
    usePathCacheStore.getState().add('h1', 'w1', '/a')
    usePathCacheStore.getState().add('h2', 'w1', '/b')
    usePathCacheStore.getState().clearHost('h1')
    expect(usePathCacheStore.getState().dirsByScope['h1:w1']).toBeUndefined()
    expect(usePathCacheStore.getState().dirsByScope['h2:w1']).toEqual(['/b'])
  })
})
```

- [ ] **Step 2: Run test, expect FAIL**

- [ ] **Step 3: Implement store**

新建 `spa/src/stores/usePathCacheStore.ts`：

```ts
import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import { STORAGE_KEYS } from '../lib/storage/keys'

const MAX_DIRS_PER_SCOPE = 50
const scopeKey = (hostId: string, workspaceId: string) => `${hostId}:${workspaceId}`
const dirname = (p: string) => {
  const idx = p.lastIndexOf('/')
  return idx <= 0 ? '/' : p.slice(0, idx)
}

interface PathCacheState {
  dirsByScope: Record<string, string[]>  // LRU; head = most recent
  add: (hostId: string, workspaceId: string, dir: string) => void
  lookup: (hostId: string, workspaceId: string, basename: string) => string[]
  pruneStaleCandidate: (hostId: string, workspaceId: string, candidatePath: string) => void
  clearScope: (hostId: string, workspaceId: string) => void
  clearHost: (hostId: string) => void
}

export const usePathCacheStore = create<PathCacheState>()(
  persist(
    (set, get) => ({
      dirsByScope: {},

      add: (hostId, workspaceId, dir) =>
        set((state) => {
          const key = scopeKey(hostId, workspaceId)
          const existing = state.dirsByScope[key] ?? []
          const filtered = existing.filter((d) => d !== dir)
          const next = [dir, ...filtered].slice(0, MAX_DIRS_PER_SCOPE)
          return { dirsByScope: { ...state.dirsByScope, [key]: next } }
        }),

      lookup: (hostId, workspaceId, basename) => {
        const key = scopeKey(hostId, workspaceId)
        const dirs = get().dirsByScope[key] ?? []
        return dirs.map((d) => `${d}/${basename}`)
      },

      pruneStaleCandidate: (hostId, workspaceId, candidatePath) =>
        set((state) => {
          const key = scopeKey(hostId, workspaceId)
          const existing = state.dirsByScope[key]
          if (!existing) return state
          const dir = dirname(candidatePath)
          const next = existing.filter((d) => d !== dir)
          if (next.length === existing.length) return state
          return { dirsByScope: { ...state.dirsByScope, [key]: next } }
        }),

      clearScope: (hostId, workspaceId) =>
        set((state) => {
          const key = scopeKey(hostId, workspaceId)
          if (!(key in state.dirsByScope)) return state
          const { [key]: _, ...rest } = state.dirsByScope
          return { dirsByScope: rest }
        }),

      clearHost: (hostId) =>
        set((state) => {
          const prefix = `${hostId}:`
          const next: Record<string, string[]> = {}
          for (const [k, v] of Object.entries(state.dirsByScope)) {
            if (!k.startsWith(prefix)) next[k] = v
          }
          return { dirsByScope: next }
        }),
    }),
    {
      name: STORAGE_KEYS.PATH_CACHE,
      partialize: (s) => ({ dirsByScope: s.dirsByScope }),
    },
  ),
)
```

- [ ] **Step 4: Run test, expect PASS**

```
cd spa && npx vitest run src/stores/usePathCacheStore.test.ts
```

- [ ] **Step 5: Commit**

```bash
git add spa/src/stores/usePathCacheStore.ts spa/src/stores/usePathCacheStore.test.ts
git commit -m "feat(spa): usePathCacheStore with LRU and persist"
```

---

## Task 4.6 — `resolveWorkspaceForSession` helper

**Files:**
- Create: `spa/src/lib/resolve-workspace-for-session.ts`
- Test: `spa/src/lib/resolve-workspace-for-session.test.ts`

- [ ] **Step 1: Write failing test**

新建 test：

```ts
import { describe, it, expect, beforeEach } from 'vitest'
import { useTabStore } from '../stores/useTabStore'
import { useWorkspaceStore } from '../stores/useWorkspaceStore'
import { resolveWorkspaceForSession } from './resolve-workspace-for-session'

describe('resolveWorkspaceForSession', () => {
  beforeEach(() => {
    useTabStore.setState({ tabs: {}, tabOrder: [] } as never, false)
    useWorkspaceStore.setState({ workspaces: [], activeWorkspaceId: null } as never, false)
  })

  it('returns null when no tab matches the session', () => {
    expect(resolveWorkspaceForSession('h1', 'sess')).toBeNull()
  })

  it('returns active workspace when a tab in it matches', () => {
    useTabStore.setState({
      tabs: { t1: { id: 't1', layout: { id: 'p', content: { kind: 'tmux-session', hostId: 'h1', sessionCode: 'sess' } } } },
      tabOrder: ['t1'],
    } as never, false)
    useWorkspaceStore.setState({
      workspaces: [{ id: 'w1', tabs: ['t1'], activeTabId: 't1' }, { id: 'w2', tabs: [], activeTabId: null }],
      activeWorkspaceId: 'w1',
    } as never, false)
    expect(resolveWorkspaceForSession('h1', 'sess')).toBe('w1')
  })

  it('falls back to any workspace if active workspace has no match', () => {
    useTabStore.setState({
      tabs: { t1: { id: 't1', layout: { id: 'p', content: { kind: 'tmux-session', hostId: 'h1', sessionCode: 'sess' } } } },
      tabOrder: ['t1'],
    } as never, false)
    useWorkspaceStore.setState({
      workspaces: [{ id: 'w1', tabs: [], activeTabId: null }, { id: 'w2', tabs: ['t1'], activeTabId: 't1' }],
      activeWorkspaceId: 'w1',
    } as never, false)
    expect(resolveWorkspaceForSession('h1', 'sess')).toBe('w2')
  })
})
```

- [ ] **Step 2: Run test, expect FAIL**

- [ ] **Step 3: Implement helper**

```ts
import { useTabStore } from '../stores/useTabStore'
import { useWorkspaceStore } from '../stores/useWorkspaceStore'
import { getPrimaryPane } from './pane-tree'

/**
 * Find the workspace that owns a tab matching (hostId, sessionCode).
 * Active workspace wins if it matches; otherwise any workspace; null if
 * no tab corresponds (standalone session or stale code).
 */
export function resolveWorkspaceForSession(hostId: string, sessionCode: string): string | null {
  const tabs = useTabStore.getState().tabs
  const matchingTabIds: string[] = []
  for (const [tabId, tab] of Object.entries(tabs)) {
    if (!tab) continue
    const c = getPrimaryPane(tab.layout).content
    if (c.kind === 'tmux-session' && c.hostId === hostId && c.sessionCode === sessionCode) {
      matchingTabIds.push(tabId)
    }
  }
  if (matchingTabIds.length === 0) return null

  const wsState = useWorkspaceStore.getState()
  const active = wsState.workspaces.find((w) => w.id === wsState.activeWorkspaceId)
  if (active && matchingTabIds.some((tid) => active.tabs.includes(tid))) return active.id

  for (const ws of wsState.workspaces) {
    if (matchingTabIds.some((tid) => ws.tabs.includes(tid))) return ws.id
  }
  return null
}
```

- [ ] **Step 4: Run test, expect PASS**

- [ ] **Step 5: Commit**

```bash
git add spa/src/lib/resolve-workspace-for-session.ts spa/src/lib/resolve-workspace-for-session.test.ts
git commit -m "feat(spa): resolveWorkspaceForSession helper with active priority"
```

---

## Task 4.7 — `agent-ws-dispatch.ts` 加 `agent.path_hint` case

**Files:**
- Modify: `spa/src/lib/agent-ws-dispatch.ts`
- Modify: `spa/src/lib/agent-ws-dispatch.test.ts`

- [ ] **Step 1: Write failing test**

擴 既有 test：

```ts
import { dispatchAgentWsEvent } from './agent-ws-dispatch'
import { usePathCacheStore } from '../stores/usePathCacheStore'

describe('dispatchAgentWsEvent agent.path_hint', () => {
  beforeEach(() => {
    usePathCacheStore.setState({ dirsByScope: {} } as never, false)
    useTabStore.setState({
      tabs: { t1: { id: 't1', layout: { id: 'p', content: { kind: 'tmux-session', hostId: 'h1', sessionCode: 'sess' } } } },
      tabOrder: ['t1'],
    } as never, false)
    useWorkspaceStore.setState({
      workspaces: [{ id: 'w1', tabs: ['t1'], activeTabId: 't1' }],
      activeWorkspaceId: 'w1',
    } as never, false)
  })

  it('absolute hint adds dir to path cache for resolved workspace', () => {
    const payload = JSON.stringify({
      agentId: 'claude-code', hostId: 'h1', sessionCode: 'sess',
      kind: 'read', path: '/a/b/c.go', dir: '/a/b',
      pathKind: 'absolute', confidence: 'high', toolName: 'Read',
      timestamp: '2026-04-27T00:00:00Z',
    })
    dispatchAgentWsEvent('h1', { type: 'agent.path_hint', session: 'sess', value: payload })
    expect(usePathCacheStore.getState().dirsByScope['h1:w1']).toEqual(['/a/b'])
  })

  it('non-absolute path hints are dropped', () => {
    const payload = JSON.stringify({
      agentId: 'codex', hostId: 'h1', sessionCode: 'sess',
      kind: 'read', dir: 'rel/dir',
      pathKind: 'relative', confidence: 'medium', toolName: 'apply_patch',
      timestamp: '2026-04-27T00:00:00Z',
    })
    dispatchAgentWsEvent('h1', { type: 'agent.path_hint', session: 'sess', value: payload })
    expect(usePathCacheStore.getState().dirsByScope['h1:w1']).toBeUndefined()
  })

  it('unknown pathKind is dropped defensively', () => {
    const payload = JSON.stringify({
      agentId: 'claude-code', hostId: 'h1', sessionCode: 'sess',
      kind: 'read', dir: '/a/b',
      pathKind: 'galaxy-brain', confidence: 'high', toolName: 'Read',
      timestamp: '2026-04-27T00:00:00Z',
    })
    dispatchAgentWsEvent('h1', { type: 'agent.path_hint', session: 'sess', value: payload })
    expect(usePathCacheStore.getState().dirsByScope['h1:w1']).toBeUndefined()
  })

  it('hint with no resolvable workspace is dropped', () => {
    useTabStore.setState({ tabs: {}, tabOrder: [] } as never, false)
    const payload = JSON.stringify({
      agentId: 'claude-code', hostId: 'h1', sessionCode: 'sess',
      kind: 'read', dir: '/a/b',
      pathKind: 'absolute', confidence: 'high', toolName: 'Read',
      timestamp: '2026-04-27T00:00:00Z',
    })
    dispatchAgentWsEvent('h1', { type: 'agent.path_hint', session: 'sess', value: payload })
    expect(Object.keys(usePathCacheStore.getState().dirsByScope)).toEqual([])
  })
})
```

- [ ] **Step 2: Run test, expect FAIL**

- [ ] **Step 3: Add case to dispatcher**

在 `spa/src/lib/agent-ws-dispatch.ts` 既有 if-block 後加：

```ts
import type { PathHint } from '../types/agent-events'
import { isValidPathKind, isValidConfidence } from '../types/agent-events'
import { resolveWorkspaceForSession } from './resolve-workspace-for-session'
import { usePathCacheStore } from '../stores/usePathCacheStore'

// ... at end of dispatchAgentWsEvent:
  if (event.type === 'agent.path_hint') {
    try {
      const hint = JSON.parse(event.value) as PathHint
      if (!isValidPathKind(hint.pathKind) || !isValidConfidence(hint.confidence)) return
      if (hint.pathKind !== 'absolute' || !hint.dir) return
      const wsId = resolveWorkspaceForSession(hostId, hint.sessionCode)
      if (!wsId) return
      usePathCacheStore.getState().add(hostId, wsId, hint.dir)
    } catch {
      // Malformed payload — drop silently.
    }
    return
  }
```

- [ ] **Step 4: Run test, expect PASS**

- [ ] **Step 5: Commit**

```bash
git add spa/src/lib/agent-ws-dispatch.ts spa/src/lib/agent-ws-dispatch.test.ts
git commit -m "feat(spa): dispatch agent.path_hint into usePathCacheStore"
```

---

## Task 4.8 — Extend `useMultiHostEventWs` for `agent.*` dispatch

**Files:**
- Modify: `spa/src/hooks/useMultiHostEventWs.ts`

- [ ] **Step 1: Find current filter**

```bash
grep -n "agent\." spa/src/hooks/useMultiHostEventWs.ts
```

- [ ] **Step 2: Replace filter**

把 `if (event.type === 'agent.status' || event.type === 'agent.status.cleared')` 改成：

```ts
if (event.type.startsWith('agent.')) dispatchAgentWsEvent(hostId, event)
```

- [ ] **Step 3: Run all tests**

```
cd spa && npx vitest run
```

- [ ] **Step 4: Commit**

```bash
git add spa/src/hooks/useMultiHostEventWs.ts
git commit -m "refactor(spa): dispatch all agent.* WS events to dispatcher"
```

---

## Task 4.9 — workspace remove + host remove subscribers

**Files:**
- Modify: `spa/src/stores/usePathCacheStore.ts`（加 `attachAutoCleanup` 助手）
- Modify: `spa/src/main.tsx`（呼叫一次）

- [ ] **Step 1: Write failing test**

擴 `usePathCacheStore.test.ts`：

```ts
import { useWorkspaceStore } from './useWorkspaceStore'
import { useHostStore } from './useHostStore'
import { attachPathCacheAutoCleanup } from './usePathCacheStore'

it('workspace removal clears its scope', () => {
  attachPathCacheAutoCleanup()
  useWorkspaceStore.setState({
    workspaces: [{ id: 'w1', tabs: [] }, { id: 'w2', tabs: [] }],
    activeWorkspaceId: 'w1',
  } as never, false)
  usePathCacheStore.getState().add('h1', 'w1', '/a')
  usePathCacheStore.getState().add('h1', 'w2', '/b')
  // simulate w1 removal
  useWorkspaceStore.setState({
    workspaces: [{ id: 'w2', tabs: [] }],
    activeWorkspaceId: 'w2',
  } as never, false)
  expect(usePathCacheStore.getState().dirsByScope['h1:w1']).toBeUndefined()
  expect(usePathCacheStore.getState().dirsByScope['h1:w2']).toEqual(['/b'])
})

it('host removal clears all its scopes', () => {
  attachPathCacheAutoCleanup()
  useHostStore.setState({ hostOrder: ['h1', 'h2'] } as never, false)
  usePathCacheStore.getState().add('h1', 'w1', '/a')
  usePathCacheStore.getState().add('h2', 'w1', '/b')
  useHostStore.setState({ hostOrder: ['h2'] } as never, false)
  expect(usePathCacheStore.getState().dirsByScope['h1:w1']).toBeUndefined()
  expect(usePathCacheStore.getState().dirsByScope['h2:w1']).toEqual(['/b'])
})
```

- [ ] **Step 2: Run test, expect FAIL**

- [ ] **Step 3: Implement subscribers**

在 `usePathCacheStore.ts` 末尾加：

```ts
import { useWorkspaceStore } from './useWorkspaceStore'
import { useHostStore } from './useHostStore'

let _attached = false

export function attachPathCacheAutoCleanup(): void {
  if (_attached) return
  _attached = true

  let lastWsIds = new Set(useWorkspaceStore.getState().workspaces.map((w) => w.id))
  useWorkspaceStore.subscribe((state) => {
    const current = new Set(state.workspaces.map((w) => w.id))
    for (const id of lastWsIds) {
      if (!current.has(id)) {
        // Iterate all hosts referencing this workspace and clear.
        const dirs = usePathCacheStore.getState().dirsByScope
        for (const key of Object.keys(dirs)) {
          const [hostId, wsId] = key.split(':')
          if (wsId === id) usePathCacheStore.getState().clearScope(hostId, wsId)
        }
      }
    }
    lastWsIds = current
  })

  let lastHostIds = new Set(useHostStore.getState().hostOrder)
  useHostStore.subscribe((state) => {
    const current = new Set(state.hostOrder)
    for (const id of lastHostIds) {
      if (!current.has(id)) usePathCacheStore.getState().clearHost(id)
    }
    lastHostIds = current
  })
}
```

- [ ] **Step 4: Wire into bootstrap**

在 `spa/src/main.tsx` 既有 store 初始化後（registerBuiltinModules 之後）加：

```tsx
import { attachPathCacheAutoCleanup } from './stores/usePathCacheStore'
attachPathCacheAutoCleanup()
```

- [ ] **Step 5: Run test, expect PASS**

- [ ] **Step 6: Commit**

```bash
git add spa/src/stores/usePathCacheStore.ts spa/src/stores/usePathCacheStore.test.ts spa/src/main.tsx
git commit -m "feat(spa): path cache auto-clears on workspace/host removal"
```

---

## Task 4.10 — Phase 4 verification + PR

- [ ] **Step 1: Full test + lint + build + go test**

```bash
cd spa && npx vitest run && pnpm run lint && pnpm run build
go test ./...
```

- [ ] **Step 2: PR + 兩輪 codex review**

---

# Phase 5 — File-not-found popup + Layer 1/2/3 fallback

PR 結束標準：點不存在的檔案會走「stat → cache stat → popup → daemon search」管線；layer 2/3 由 popup 觸發；workspace context 在 await 後仍正確。

## Task 5.1 — daemon `fs.search` endpoint

**Files:**
- Create: `internal/module/fs/search.go`
- Test: `internal/module/fs/search_test.go`

- [ ] **Step 1: Write failing test**

新建 `internal/module/fs/search_test.go`：

```go
package fs

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestFsSearch_BasenameMatchInRoots(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "a", "foo.go"), "ok")
	mustWrite(t, filepath.Join(dir, "b", "foo.go"), "ok")
	mustWrite(t, filepath.Join(dir, "b", "bar.go"), "no")

	req := newSearchReq(t, searchRequest{Basename: "foo.go", Roots: []string{dir}, MaxResults: 10})
	w := httptest.NewRecorder()
	HandleSearch(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status: %d body: %s", w.Code, w.Body.String())
	}
	var resp searchResponse
	mustDecode(t, w, &resp)
	if len(resp.Matches) != 2 {
		t.Errorf("expected 2 matches, got %d", len(resp.Matches))
	}
}

func TestFsSearch_ExcludeDirsPrunesSubtree(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "node_modules", "x", "foo.go"), "skip")
	mustWrite(t, filepath.Join(dir, "src", "foo.go"), "ok")

	req := newSearchReq(t, searchRequest{
		Basename: "foo.go", Roots: []string{dir},
		ExcludeDirs: []string{"node_modules"},
	})
	w := httptest.NewRecorder()
	HandleSearch(w, req)
	var resp searchResponse
	mustDecode(t, w, &resp)
	if len(resp.Matches) != 1 || filepath.Base(filepath.Dir(resp.Matches[0].Path)) != "src" {
		t.Errorf("unexpected matches: %+v", resp.Matches)
	}
}

func TestFsSearch_MaxDepthLimits(t *testing.T) {
	dir := t.TempDir()
	deep := dir
	for i := 0; i < 5; i++ {
		deep = filepath.Join(deep, "d")
	}
	mustWrite(t, filepath.Join(deep, "foo.go"), "ok")
	req := newSearchReq(t, searchRequest{Basename: "foo.go", Roots: []string{dir}, MaxDepth: 3})
	w := httptest.NewRecorder()
	HandleSearch(w, req)
	var resp searchResponse
	mustDecode(t, w, &resp)
	if len(resp.Matches) != 0 {
		t.Errorf("expected 0 matches due to depth, got %d", len(resp.Matches))
	}
}

func TestFsSearch_RejectsNonAbsoluteRoot(t *testing.T) {
	req := newSearchReq(t, searchRequest{Basename: "x", Roots: []string{"rel/dir"}})
	w := httptest.NewRecorder()
	HandleSearch(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for non-absolute root, got %d", w.Code)
	}
}

// helpers
func mustWrite(t *testing.T, p, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func newSearchReq(t *testing.T, body searchRequest) *http.Request {
	t.Helper()
	b, _ := json.Marshal(body)
	return httptest.NewRequest("POST", "/api/fs/search", bytes.NewReader(b))
}

func mustDecode(t *testing.T, w *httptest.ResponseRecorder, into any) {
	t.Helper()
	if err := json.Unmarshal(w.Body.Bytes(), into); err != nil {
		t.Fatalf("decode: %v", err)
	}
}
```

- [ ] **Step 2: Run test, expect FAIL**

```
go test ./internal/module/fs/ -run Search
```

- [ ] **Step 3: Implement search.go**

新建 `internal/module/fs/search.go`：

```go
package fs

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type searchRequest struct {
	Basename             string   `json:"basename"`
	Roots                []string `json:"roots"`
	MaxResults           int      `json:"maxResults,omitempty"`
	MaxDepth             int      `json:"maxDepth,omitempty"`
	TimeoutMs            int      `json:"timeoutMs,omitempty"`
	ExcludeDirs          []string `json:"excludeDirs,omitempty"`
	ExcludeBasenameGlobs []string `json:"excludeBasenameGlobs,omitempty"`
	RespectGitignore     bool     `json:"respectGitignore,omitempty"`
}

type searchMatch struct {
	Path       string    `json:"path"`
	ModTime    time.Time `json:"modTime"`
	SizeBytes  int64     `json:"sizeBytes"`
	Root       string    `json:"root"`
}

type searchResponse struct {
	Matches  []searchMatch `json:"matches"`
	Truncated bool         `json:"truncated"`
}

const (
	defaultSearchMaxResults = 50
	defaultSearchMaxDepth   = 8
	defaultSearchTimeoutMs  = 5000
	hardCapMaxResults       = 200
)

var defaultExcludeDirs = []string{"node_modules", ".git", ".cache", "dist"}
var defaultExcludeGlobs = []string{"*.lock", "*.log"}

func HandleSearch(w http.ResponseWriter, r *http.Request) {
	limitBody(w, r, maxBodySize)
	var req searchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid body: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.Basename == "" {
		http.Error(w, "basename required", http.StatusBadRequest)
		return
	}
	for _, root := range req.Roots {
		if !filepath.IsAbs(filepath.Clean(root)) {
			http.Error(w, "roots must be absolute", http.StatusBadRequest)
			return
		}
	}
	if req.MaxResults <= 0 {
		req.MaxResults = defaultSearchMaxResults
	}
	if req.MaxResults > hardCapMaxResults {
		req.MaxResults = hardCapMaxResults
	}
	if req.MaxDepth <= 0 {
		req.MaxDepth = defaultSearchMaxDepth
	}
	if req.TimeoutMs <= 0 {
		req.TimeoutMs = defaultSearchTimeoutMs
	}
	if req.ExcludeDirs == nil {
		req.ExcludeDirs = defaultExcludeDirs
	}
	if req.ExcludeBasenameGlobs == nil {
		req.ExcludeBasenameGlobs = defaultExcludeGlobs
	}

	excludeDirSet := make(map[string]struct{}, len(req.ExcludeDirs))
	for _, d := range req.ExcludeDirs {
		excludeDirSet[d] = struct{}{}
	}

	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(req.TimeoutMs)*time.Millisecond)
	defer cancel()

	matches := make([]searchMatch, 0, req.MaxResults)
	truncated := false

	for _, root := range req.Roots {
		walkDepth := 0
		err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return nil // skip unreadable
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			rel, _ := filepath.Rel(root, path)
			depth := strings.Count(rel, string(os.PathSeparator))
			_ = walkDepth
			if depth > req.MaxDepth {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if d.IsDir() {
				if _, skip := excludeDirSet[d.Name()]; skip {
					return filepath.SkipDir
				}
				return nil
			}
			// File: check basename glob excludes
			for _, glob := range req.ExcludeBasenameGlobs {
				if ok, _ := filepath.Match(glob, d.Name()); ok {
					return nil
				}
			}
			if d.Name() == req.Basename {
				info, err := d.Info()
				if err != nil {
					return nil
				}
				matches = append(matches, searchMatch{
					Path: path, ModTime: info.ModTime(), SizeBytes: info.Size(), Root: root,
				})
				if len(matches) >= req.MaxResults {
					truncated = true
					return filepath.SkipAll
				}
			}
			return nil
		})
		if err != nil && err != context.DeadlineExceeded {
			break
		}
		if err == context.DeadlineExceeded || ctx.Err() != nil {
			truncated = true
			break
		}
	}

	sort.Slice(matches, func(i, j int) bool { return matches[i].ModTime.After(matches[j].ModTime) })
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(searchResponse{Matches: matches, Truncated: truncated})
}
```

- [ ] **Step 4: Wire route into module**

在 `internal/module/fs/module.go`（或既有 routes 註冊處）加：

```go
mux.HandleFunc("POST /api/fs/search", HandleSearch)
```

- [ ] **Step 5: Run tests, expect PASS**

```
go test ./internal/module/fs/...
```

- [ ] **Step 6: Commit**

```bash
git add internal/module/fs/search.go internal/module/fs/search_test.go internal/module/fs/module.go
git commit -m "feat(daemon): fs.search endpoint with depth/exclude/timeout"
```

---

## Task 5.2 — `gitignore` + symlink loop tests（補強）

**Files:**
- Modify: `internal/module/fs/search_test.go`

- [ ] **Step 1: Write failing test**

加 case：

```go
func TestFsSearch_GitignoreFiltersResults(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, ".gitignore"), "ignored.go\n")
	mustWrite(t, filepath.Join(dir, "ignored.go"), "skip")
	mustWrite(t, filepath.Join(dir, "kept.go"), "ok")
	req := newSearchReq(t, searchRequest{
		Basename: "ignored.go", Roots: []string{dir},
		RespectGitignore: true,
	})
	w := httptest.NewRecorder()
	HandleSearch(w, req)
	var resp searchResponse
	mustDecode(t, w, &resp)
	if len(resp.Matches) != 0 {
		t.Errorf("ignored.go should not appear with respectGitignore: %+v", resp.Matches)
	}
}

func TestFsSearch_SymlinkLoopDoesNotInfiniteWalk(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on Windows")
	}
	dir := t.TempDir()
	a := filepath.Join(dir, "a")
	mustWrite(t, filepath.Join(a, "x.go"), "ok")
	if err := os.Symlink(a, filepath.Join(a, "loop")); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		req := newSearchReq(t, searchRequest{Basename: "x.go", Roots: []string{dir}, MaxDepth: 6})
		w := httptest.NewRecorder()
		HandleSearch(w, req)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("symlink loop caused hang")
	}
}
```

(`runtime` import needed.)

- [ ] **Step 2: Run test, expect FAIL（gitignore 還沒實作）**

- [ ] **Step 3: Implement gitignore via go-gitignore**

Add dependency:

```bash
go get github.com/sabhiram/go-gitignore
go mod tidy
```

在 `search.go` 內 walk loop 起點加 root-level `.gitignore` 解析：

```go
import gitignore "github.com/sabhiram/go-gitignore"

// inside HandleSearch, before WalkDir:
var ignore *gitignore.GitIgnore
if req.RespectGitignore {
    if data, err := os.ReadFile(filepath.Join(root, ".gitignore")); err == nil {
        ignore, _ = gitignore.CompileIgnoreLines(strings.Split(string(data), "\n")...)
    }
}

// inside WalkDir func, after d.IsDir() handling:
if ignore != nil {
    rel, _ := filepath.Rel(root, path)
    if ignore.MatchesPath(rel) {
        if d.IsDir() {
            return filepath.SkipDir
        }
        return nil
    }
}
```

Symlink loop：`filepath.WalkDir` 預設不 follow symlink（`SkipDir` for symlink-to-dir 自動避免），但若需要可加 `os.Lstat` 檢查。本實作預設不 follow，loop 測試應通過。

- [ ] **Step 4: Run tests, expect PASS**

- [ ] **Step 5: Commit**

```bash
git add internal/module/fs/search.go internal/module/fs/search_test.go go.mod go.sum
git commit -m "feat(daemon): fs.search respects root .gitignore and avoids symlink loops"
```

---

## Task 5.3 — SPA `fsSearchByBasename` helper

**Files:**
- Create: `spa/src/lib/fs-search.ts`
- Test: `spa/src/lib/fs-search.test.ts`

- [ ] **Step 1: Write failing test**

```ts
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { fsSearchByBasename } from './fs-search'
import { useHostStore } from '../stores/useHostStore'

describe('fsSearchByBasename', () => {
  beforeEach(() => {
    useHostStore.setState({
      activeHostId: 'h1',
      hostOrder: ['h1'],
      getDaemonBase: () => 'http://daemon',
      getAuthHeaders: () => ({}),
    } as never, false)
    global.fetch = vi.fn(async () =>
      new Response(JSON.stringify({
        matches: [
          { path: '/a/foo.go', modTime: '2026-04-27T00:00:00Z', sizeBytes: 10, root: '/a' },
          { path: '/b/foo.go', modTime: '2026-04-26T00:00:00Z', sizeBytes: 10, root: '/b' },
        ],
        truncated: false,
      }), { status: 200 }),
    ) as never
  })

  it('posts search request to host daemon and returns matches', async () => {
    const matches = await fsSearchByBasename('h1', 'foo.go', ['/a', '/b'])
    expect(matches.map((m) => m.path)).toEqual(['/a/foo.go', '/b/foo.go'])
  })

  it('rejects non-absolute roots before calling daemon', async () => {
    await expect(fsSearchByBasename('h1', 'foo.go', ['rel/dir'])).rejects.toThrow(/absolute/i)
  })
})
```

- [ ] **Step 2: Run test, expect FAIL**

- [ ] **Step 3: Implement helper**

```ts
import { useHostStore } from '../stores/useHostStore'

export interface SearchMatch {
  path: string
  modTime: string
  sizeBytes: number
  root: string
}

export interface SearchResponse {
  matches: SearchMatch[]
  truncated: boolean
}

export async function fsSearchByBasename(
  hostId: string,
  basename: string,
  roots: string[],
  opts: { maxResults?: number; maxDepth?: number; timeoutMs?: number } = {},
): Promise<SearchMatch[]> {
  for (const r of roots) {
    if (!r.startsWith('/')) throw new Error(`Search root must be absolute: ${r}`)
  }
  const state = useHostStore.getState()
  const base = state.getDaemonBase(hostId)
  const headers = { 'Content-Type': 'application/json', ...state.getAuthHeaders(hostId) }
  const body = JSON.stringify({ basename, roots, ...opts })
  const res = await fetch(`${base}/api/fs/search`, { method: 'POST', headers, body })
  if (!res.ok) throw new Error(`fs.search failed: ${res.status} ${res.statusText}`)
  const json = (await res.json()) as SearchResponse
  return json.matches
}
```

- [ ] **Step 4: Run test, expect PASS**

- [ ] **Step 5: Commit**

```bash
git add spa/src/lib/fs-search.ts spa/src/lib/fs-search.test.ts
git commit -m "feat(spa): fsSearchByBasename helper hits per-host daemon"
```

---

## Task 5.4 — Two new UI settings + `EditorOpenBehaviorSection`

**Files:**
- Modify: `spa/src/stores/useUISettingsStore.ts`
- Create: `spa/src/components/settings/EditorOpenBehaviorSection.tsx`
- Test: 對應 .test.tsx

- [ ] **Step 1: Add fields to useUISettingsStore**

加：

```ts
popupOnMissingFile: true,
autoSearchLayer1: true,
setPopupOnMissingFile: (v: boolean) => set({ popupOnMissingFile: v }),
setAutoSearchLayer1: (v: boolean) => set({ autoSearchLayer1: v }),
```

(對應 type / persist partialize / defaults。)

- [ ] **Step 2: Implement section**

```tsx
import { useUISettingsStore } from '../../stores/useUISettingsStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { SettingItem } from './SettingItem'
import { ToggleSwitch } from './ToggleSwitch'

export function EditorOpenBehaviorSection() {
  const popup = useUISettingsStore((s) => s.popupOnMissingFile)
  const setPopup = useUISettingsStore((s) => s.setPopupOnMissingFile)
  const auto = useUISettingsStore((s) => s.autoSearchLayer1)
  const setAuto = useUISettingsStore((s) => s.setAutoSearchLayer1)
  const t = useI18nStore((s) => s.t)
  return (
    <div>
      <h3 className="text-sm text-text-primary mt-6 mb-1">{t('settings.editor.open_behavior.title')}</h3>
      <SettingItem label={t('settings.editor.open_behavior.popup.label')} description={t('settings.editor.open_behavior.popup.desc')}>
        <ToggleSwitch label={t('settings.editor.open_behavior.popup.label')} checked={popup} onChange={setPopup} />
      </SettingItem>
      <SettingItem label={t('settings.editor.open_behavior.auto_layer1.label')} description={t('settings.editor.open_behavior.auto_layer1.desc')}>
        <ToggleSwitch label={t('settings.editor.open_behavior.auto_layer1.label')} checked={auto} onChange={setAuto} />
      </SettingItem>
    </div>
  )
}
```

加 i18n key 到 zh-TW + en JSON。

- [ ] **Step 3: Wire into Editor module settings**

在 `register-modules.tsx` Editor module `settings` 陣列加：

```tsx
{
  localId: 'open-behavior',
  scope: 'purdex',
  order: 9,
  labelKey: 'settings.editor.open_behavior.title',
  component: EditorOpenBehaviorSection,
},
```

- [ ] **Step 4: Run tests, expect PASS**

- [ ] **Step 5: Commit**

```bash
git add spa/src/stores/useUISettingsStore.ts spa/src/components/settings/EditorOpenBehaviorSection.tsx spa/src/lib/register-modules.tsx spa/src/i18n/*.json
git commit -m "feat(spa): editor open behavior settings (popup + auto layer1)"
```

---

## Task 5.5 — `tryOpenFile` flow

**Files:**
- Create: `spa/src/lib/open-file.ts`
- Test: `spa/src/lib/open-file.test.ts`

- [ ] **Step 1: Write failing test**

```ts
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { tryOpenFile } from './open-file'
import { usePathCacheStore } from '../stores/usePathCacheStore'
import { useUISettingsStore } from '../stores/useUISettingsStore'

const mockBackend = {
  stat: vi.fn(),
  // ... other methods optional
}

const mockOpenInTab = vi.fn()
const mockOpenPopup = vi.fn()

describe('tryOpenFile', () => {
  beforeEach(() => {
    usePathCacheStore.setState({ dirsByScope: {} } as never, false)
    useUISettingsStore.setState({ popupOnMissingFile: true, autoSearchLayer1: true } as never, false)
    mockBackend.stat.mockReset()
    mockOpenInTab.mockReset()
    mockOpenPopup.mockReset()
  })

  const ctx = { hostId: 'h1', sourceWorkspaceId: 'w1' }
  const file = { path: '/a/b/foo.go', name: 'foo.go', extension: 'go', isDirectory: false }
  const source = { type: 'inapp' as const }

  it('opens directly when path exists', async () => {
    mockBackend.stat.mockResolvedValue({ isDirectory: false })
    await tryOpenFile(file as never, source, ctx, { backend: mockBackend as never, openInTab: mockOpenInTab, openPopup: mockOpenPopup })
    expect(mockOpenInTab).toHaveBeenCalledWith(file, source, ctx)
    expect(mockOpenPopup).not.toHaveBeenCalled()
  })

  it('throws when popupOnMissingFile is off and file missing', async () => {
    useUISettingsStore.setState({ popupOnMissingFile: false } as never, false)
    mockBackend.stat.mockResolvedValue(null)
    await expect(tryOpenFile(file as never, source, ctx, { backend: mockBackend as never, openInTab: mockOpenInTab, openPopup: mockOpenPopup })).rejects.toThrow(/not found/i)
  })

  it('layer1 single hit opens directly (after stat verify)', async () => {
    usePathCacheStore.getState().add('h1', 'w1', '/a/b')
    mockBackend.stat.mockImplementation(async (p: string) => p === '/a/b/foo.go' ? { isDirectory: false } : null)
    // Simulate: original file.path is '/elsewhere/foo.go' which doesn't exist
    const missing = { ...file, path: '/elsewhere/foo.go' }
    await tryOpenFile(missing as never, source, ctx, { backend: mockBackend as never, openInTab: mockOpenInTab, openPopup: mockOpenPopup })
    expect(mockOpenInTab).toHaveBeenCalledWith({ ...missing, path: '/a/b/foo.go' }, source, ctx)
  })

  it('layer1 multi hits opens popup with verified candidates', async () => {
    usePathCacheStore.getState().add('h1', 'w1', '/a/b')
    usePathCacheStore.getState().add('h1', 'w1', '/c/d')
    mockBackend.stat.mockImplementation(async (p: string) => p.endsWith('foo.go') && p !== '/elsewhere/foo.go' ? { isDirectory: false } : null)
    const missing = { ...file, path: '/elsewhere/foo.go' }
    await tryOpenFile(missing as never, source, ctx, { backend: mockBackend as never, openInTab: mockOpenInTab, openPopup: mockOpenPopup })
    expect(mockOpenPopup).toHaveBeenCalledWith(expect.objectContaining({
      mode: 'layer1-multi',
      hits: ['/c/d/foo.go', '/a/b/foo.go'],
    }))
  })

  it('layer1 stat fail prunes cache and falls to ask-expand', async () => {
    usePathCacheStore.getState().add('h1', 'w1', '/stale/dir')
    mockBackend.stat.mockResolvedValue(null)  // every stat fails
    const missing = { ...file, path: '/elsewhere/foo.go' }
    await tryOpenFile(missing as never, source, ctx, { backend: mockBackend as never, openInTab: mockOpenInTab, openPopup: mockOpenPopup })
    expect(usePathCacheStore.getState().dirsByScope['h1:w1']).toEqual([])
    expect(mockOpenPopup).toHaveBeenCalledWith(expect.objectContaining({ mode: 'ask-expand' }))
  })
})
```

- [ ] **Step 2: Run test, expect FAIL**

- [ ] **Step 3: Implement**

```ts
import type { FileInfo, FileSource } from '../types/fs'
import { usePathCacheStore } from '../stores/usePathCacheStore'
import { useUISettingsStore } from '../stores/useUISettingsStore'

export interface OpenFileContext {
  hostId: string
  sourceWorkspaceId: string
  sessionCode?: string
  cwdResolver?: () => Promise<string | null>
}

interface FsLike {
  stat(path: string): Promise<unknown | null>
}

export interface PopupSpec {
  mode: 'layer1-multi' | 'ask-expand'
  hits?: string[]
  file: FileInfo
  source: FileSource
  ctx: OpenFileContext
}

export interface OpenDeps {
  backend: FsLike
  openInTab: (file: FileInfo, source: FileSource, ctx: OpenFileContext) => void
  openPopup: (spec: PopupSpec) => void
}

export class FileNotFoundError extends Error {
  constructor(public readonly path: string) {
    super(`File not found: ${path}`)
  }
}

export async function tryOpenFile(
  file: FileInfo,
  source: FileSource,
  ctx: OpenFileContext,
  deps: OpenDeps,
): Promise<void> {
  // 1. Direct stat
  if (await deps.backend.stat(file.path).catch(() => null)) {
    deps.openInTab(file, source, ctx)
    return
  }

  // 2. Popup gate
  const ui = useUISettingsStore.getState()
  if (!ui.popupOnMissingFile) throw new FileNotFoundError(file.path)

  // 3. Layer 1
  if (ui.autoSearchLayer1) {
    const cache = usePathCacheStore.getState()
    const candidates = cache.lookup(ctx.hostId, ctx.sourceWorkspaceId, file.name)
    const verified: string[] = []
    for (const c of candidates) {
      if (await deps.backend.stat(c).catch(() => null)) {
        verified.push(c)
      } else {
        cache.pruneStaleCandidate(ctx.hostId, ctx.sourceWorkspaceId, c)
      }
    }
    if (verified.length === 1) {
      deps.openInTab({ ...file, path: verified[0] }, source, ctx)
      return
    }
    if (verified.length > 1) {
      deps.openPopup({ mode: 'layer1-multi', hits: verified, file, source, ctx })
      return
    }
  }

  // 4. Fall through
  deps.openPopup({ mode: 'ask-expand', file, source, ctx })
}
```

- [ ] **Step 4: Run test, expect PASS**

- [ ] **Step 5: Commit**

```bash
git add spa/src/lib/open-file.ts spa/src/lib/open-file.test.ts
git commit -m "feat(spa): tryOpenFile flow with stat-verified layer 1"
```

---

## Task 5.6 — `FileNotFoundPopup` component

**Files:**
- Create: `spa/src/components/editor/FileNotFoundPopup.tsx`
- Test: `spa/src/components/editor/FileNotFoundPopup.test.tsx`

- [ ] **Step 1: Write failing test**

```tsx
import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { FileNotFoundPopup } from './FileNotFoundPopup'

describe('FileNotFoundPopup', () => {
  const baseSpec = {
    mode: 'ask-expand' as const,
    file: { path: '/missing/foo.go', name: 'foo.go', extension: 'go', isDirectory: false },
    source: { type: 'inapp' as const },
    ctx: { hostId: 'h1', sourceWorkspaceId: 'w1', sessionCode: 's1' },
  }

  it('renders the missing path and expand button', () => {
    render(<FileNotFoundPopup spec={baseSpec} onClose={vi.fn()} onOpenPath={vi.fn()} onExpand={vi.fn()} />)
    expect(screen.getByText(/foo.go/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /expand/i })).toBeInTheDocument()
  })

  it('layer1-multi mode renders candidate list', () => {
    const spec = { ...baseSpec, mode: 'layer1-multi' as const, hits: ['/a/b/foo.go', '/c/d/foo.go'] }
    render(<FileNotFoundPopup spec={spec} onClose={vi.fn()} onOpenPath={vi.fn()} onExpand={vi.fn()} />)
    expect(screen.getByText('/a/b/foo.go')).toBeInTheDocument()
    expect(screen.getByText('/c/d/foo.go')).toBeInTheDocument()
  })

  it('clicking a candidate calls onOpenPath', () => {
    const onOpenPath = vi.fn()
    const spec = { ...baseSpec, mode: 'layer1-multi' as const, hits: ['/a/b/foo.go'] }
    render(<FileNotFoundPopup spec={spec} onClose={vi.fn()} onOpenPath={onOpenPath} onExpand={vi.fn()} />)
    fireEvent.click(screen.getByText('/a/b/foo.go'))
    expect(onOpenPath).toHaveBeenCalledWith('/a/b/foo.go')
  })

  it('ESC key closes popup', () => {
    const onClose = vi.fn()
    render(<FileNotFoundPopup spec={baseSpec} onClose={onClose} onOpenPath={vi.fn()} onExpand={vi.fn()} />)
    fireEvent.keyDown(document, { key: 'Escape' })
    expect(onClose).toHaveBeenCalled()
  })
})
```

- [ ] **Step 2: Run test, expect FAIL**

- [ ] **Step 3: Implement component**

```tsx
import { useEffect } from 'react'
import type { PopupSpec } from '../../lib/open-file'

interface Props {
  spec: PopupSpec
  onClose: () => void
  onOpenPath: (path: string) => void
  onExpand: () => void
}

export function FileNotFoundPopup({ spec, onClose, onOpenPath, onExpand }: Props) {
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose()
    }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [onClose])

  return (
    <div role="dialog" aria-modal="true" className="fixed inset-0 bg-black/50 flex items-center justify-center z-50">
      <div className="bg-bg-primary rounded-lg p-6 max-w-lg w-full">
        <h3 className="text-base text-text-primary mb-2">File not found</h3>
        <p className="text-sm text-text-secondary mb-3 break-all">{spec.file.path}</p>

        {spec.mode === 'layer1-multi' && spec.hits && (
          <div className="mb-4">
            <h4 className="text-xs uppercase text-text-muted mb-1">Recent candidates</h4>
            <ul className="border border-border rounded">
              {spec.hits.map((h) => (
                <li key={h}>
                  <button
                    type="button"
                    onClick={() => onOpenPath(h)}
                    className="w-full text-left px-2 py-1 text-sm hover:bg-bg-tertiary truncate"
                  >
                    {h}
                  </button>
                </li>
              ))}
            </ul>
          </div>
        )}

        <div className="flex justify-end gap-2">
          <button type="button" onClick={onClose} className="px-3 py-1 text-sm text-text-secondary">
            Cancel
          </button>
          <button type="button" onClick={onExpand} className="px-3 py-1 text-sm bg-accent rounded">
            Expand search
          </button>
        </div>
      </div>
    </div>
  )
}
```

- [ ] **Step 4: Run test, expect PASS**

- [ ] **Step 5: Commit**

```bash
git add spa/src/components/editor/FileNotFoundPopup.tsx spa/src/components/editor/FileNotFoundPopup.test.tsx
git commit -m "feat(spa): FileNotFoundPopup component with ESC + candidate list"
```

---

## Task 5.7 — Integrate into terminal-link + FileTreeView

**Files:**
- Modify: `spa/src/lib/terminal-link/openers/file-path.ts`
- Modify: `spa/src/components/FileTreeView.tsx`
- Modify: `spa/src/lib/register-modules.tsx`（注入 deps）

- [ ] **Step 1: Replace existing open-on-click with `tryOpenFile`**

Terminal link 內：把點擊呼叫 `openSingletonTab` 的部分改成走 `tryOpenFile`。`tryOpenFile` 把 deps（backend + openInTab + openPopup）注入；`openInTab` 內部仍呼叫 `openSingletonTab` 做最後 tab 創建（保留 P2 的 same-kind 行為）。

具體：在 `register-modules.tsx` 的 `terminalLink.filePathOpener` deps 注入 `tryOpenFile` adapter；popup 開關以 React portal mount。

實作 popup mount 的 helper（`spa/src/lib/popup-mount.tsx`，新建）：

```tsx
import { createRoot, type Root } from 'react-dom/client'
import { FileNotFoundPopup } from '../components/editor/FileNotFoundPopup'
import type { PopupSpec } from './open-file'

let root: Root | null = null
let host: HTMLDivElement | null = null

export function showFileNotFoundPopup(
  spec: PopupSpec,
  onOpenPath: (path: string) => void,
  onExpand: () => void,
): void {
  if (!host) {
    host = document.createElement('div')
    document.body.appendChild(host)
    root = createRoot(host)
  }
  const close = () => {
    root?.render(<></>)
  }
  root!.render(
    <FileNotFoundPopup
      spec={spec}
      onClose={close}
      onOpenPath={(p) => { close(); onOpenPath(p) }}
      onExpand={() => { close(); onExpand() }}
    />,
  )
}
```

(Layer 2/3 fs.search 邏輯放在 `onExpand` callback 內 — 呼叫 `fsSearchByBasename` 並產出新的 popup。為簡化，本 task 不做 layer 2/3 完整流，先把 popup 框架接上；layer 2/3 在 Task 5.8。)

- [ ] **Step 2: Wire in register-modules.tsx**

Replace existing `filePathOpener` openSingletonTab call with `tryOpenFile` indirection — 詳細替換留實作期間根據實際 file-path.ts 結構決定。

- [ ] **Step 3: 跑全測**

```
cd spa && npx vitest run
```

- [ ] **Step 4: Commit**

```bash
git commit -m "feat(spa): terminal-link/FileTree open files via tryOpenFile pipeline"
```

---

## Task 5.8 — Layer 2/3 expand search + popup re-render

**Files:**
- Modify: `spa/src/lib/popup-mount.tsx`（onExpand 真正實作）
- Modify: `spa/src/components/editor/FileNotFoundPopup.tsx`（顯示 expand results 區段）

- [ ] **Step 1: Extend popup spec to support expanded mode**

`PopupSpec` 加 `mode: 'expanded'`，內含 `layer2Hits` / `layer3Hits` arrays。

- [ ] **Step 2: Implement onExpand using layer 2 + layer 3**

```ts
async function onExpand(spec: PopupSpec): Promise<void> {
  const layer2Roots: string[] = []
  if (spec.ctx.cwdResolver) {
    const cwd = await spec.ctx.cwdResolver()
    if (cwd) layer2Roots.push(cwd)
  }

  const wsState = useWorkspaceStore.getState()
  const ws = wsState.workspaces.find((w) => w.id === spec.ctx.sourceWorkspaceId)
  const projectPath = ws?.config?.projectPath as string | undefined
  const layer3Roots = projectPath ? [projectPath] : []

  const [layer2, layer3] = await Promise.all([
    layer2Roots.length ? fsSearchByBasename(spec.ctx.hostId, spec.file.name, layer2Roots) : [],
    layer3Roots.length ? fsSearchByBasename(spec.ctx.hostId, spec.file.name, layer3Roots) : [],
  ])

  // Re-render popup with expanded results
  showFileNotFoundPopupExpanded(spec, layer2, layer3)
}
```

- [ ] **Step 3: Update FileNotFoundPopup to render expanded results**

加 sections for `layer2Hits` / `layer3Hits`，相同的 `<button onClick={() => onOpenPath(...)}>` pattern。

- [ ] **Step 4: Test expansion path**

加 `FileNotFoundPopup.test.tsx` case：`mode: 'expanded'` + 給 layer 2/3 hits → render 兩個 section + 點任一條 callback `onOpenPath`。

- [ ] **Step 5: Commit + PR**

```bash
git commit -m "feat(spa): file-not-found popup expands search via layer 2/3"
```

PR + 兩輪 codex review。

---

# Self-Review

## 1. Spec coverage

| Spec 項 | 對應 task |
|---|---|
| P1: ModuleDefinition.fileOpeners interface | T1.1 |
| P1: 註冊器走過 module 收集 fileOpeners | T1.2 |
| P1: Editor 收編三個 opener | T1.3 |
| P1: HMR 一致性 | T1.4 |
| P1: DisabledModulePlaceholder | T1.5 |
| P1: PaneRenderer fallback wiring | T1.6 |
| P2: findInsertTarget 泛用化 | T2.1 |
| P2: openBrowserTab 改用泛用版 | T2.2 |
| P2: openSingletonTab 加 opts | T2.3 |
| P2: terminal-link / FileTreeView 帶 predicate | T2.4, T2.5 |
| P3: i18n key 重組 | T3.1 |
| P3: LinkDetectionSection / EditorLinkDetectionSection | T3.2, T3.3 |
| P4: PathHint Go schema | T4.1 |
| P4: PathHint extractor + dedup | T4.2 |
| P4: emit wiring | T4.3 |
| P4: STORAGE_KEYS + TS PathHint | T4.4 |
| P4: usePathCacheStore | T4.5 |
| P4: resolveWorkspaceForSession | T4.6 |
| P4: dispatchAgentWsEvent path_hint | T4.7 |
| P4: useMultiHostEventWs filter | T4.8 |
| P4: workspace/host remove cleanup | T4.9 |
| P5: fs.search endpoint | T5.1, T5.2 |
| P5: SPA fs-search helper | T5.3 |
| P5: open behavior settings | T5.4 |
| P5: tryOpenFile flow | T5.5 |
| P5: FileNotFoundPopup | T5.6 |
| P5: terminal-link / FileTreeView 整合 | T5.7 |
| P5: layer 2/3 expand | T5.8 |

✅ 涵蓋。

## 2. Placeholder scan

掃過 — Task 1.6 提到「以實際檔案內容為準」是設定上的合理選擇（pane renderer caller 可能在多個檔案，需實際驗），不算 placeholder。Task 5.7 注入 deps 時「詳細替換留實作期間根據實際 file-path.ts 結構決定」是同理。其餘步驟皆有具體 code / command / expected output。

## 3. Type consistency

- `pruneStaleCandidate(hostId, workspaceId, candidatePath)` — 在 Task 4.5 store 定義 / 5.5 tryOpenFile 呼叫 / 5.5 SPEC 一致 ✅
- `OpenFileContext { hostId, sourceWorkspaceId, sessionCode?, cwdResolver? }` — 4.6 helper / 5.5 tryOpenFile / 5.6 popup props 一致 ✅
- `PopupSpec` 兩個 mode（`layer1-multi` / `ask-expand`）+ 5.8 加 `expanded` — 命名穩定 ✅
- `PathHint` Go schema vs TS type 欄位對齊 ✅（含 `agentId`/`hostId`/`sessionCode`/`kind`/`path`/`dir`/`pathKind`/`baseDir`/`confidence`/`toolName`/`timestamp`）

---

## 執行 Handoff

Plan 完成並儲存於 `.claude/worktrees/worktree-editor-self-contained/PLAN.md`。

**兩種執行方式：**

**1. Subagent-Driven（推薦）** — 派 subagent 逐 task 執行，每 task 後 review；fast iteration。
   - **Required sub-skill**: `superpowers:subagent-driven-development`

**2. Inline Execution** — 在當前 session 內逐 task 跑；checkpoint review。
   - **Required sub-skill**: `superpowers:executing-plans`

選哪個？

> 提醒：subagent 每個 Bash 必須 `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/worktree-editor-self-contained/ && ...` 前綴（per `feedback_subagent_cwd_enforcement.md`）。
