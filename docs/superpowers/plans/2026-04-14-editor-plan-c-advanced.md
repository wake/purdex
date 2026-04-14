# Editor Module Plan C: 進階功能

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 補齊 editor module 的進階功能：Tiptap Markdown WYSIWYG、Monaco diff view、圖片/PDF 預覽 pane、外部變更偵測、Electron LocalBackend。

**Architecture:** 各功能相對獨立，可按任意順序實作。Tiptap 以 lazy load 載入；diff view 作為 editor pane 的內部模式；image-preview / pdf-preview 是獨立 pane kind（已在 Plan A 的 `panes` 陣列中預留）；LocalBackend 遵循現有 Electron IPC pattern。

**Tech Stack:** Tiptap v3 (`@tiptap/react`, `@tiptap/starter-kit`, `@tiptap/markdown`), Monaco Editor diff, Electron IPC, Vitest

**Spec:** `docs/superpowers/specs/2026-04-14-editor-module-design.md` Section 4.4-4.10

**前置條件:** Plan A + Plan B 已完成

---

## File Structure

### 新增檔案

| 路徑 | 職責 |
|------|------|
| `spa/src/components/editor/TiptapEditor.tsx` | Tiptap WYSIWYG 封裝（lazy load） |
| `spa/src/components/editor/DiffView.tsx` | Monaco diff editor 封裝 |
| `spa/src/components/editor/ImagePreviewPane.tsx` | 圖片預覽 pane |
| `spa/src/components/editor/PdfPreviewPane.tsx` | PDF 預覽 pane |
| `spa/src/lib/fs-backend-local.ts` | LocalBackend（Electron IPC） |
| `electron/fs-ipc.ts` | Electron main process FS IPC handler |

### 修改檔案

| 路徑 | 修改內容 |
|------|---------|
| `spa/src/components/editor/EditorPane.tsx` | 新增 diff mode、Markdown toggle、外部變更偵測 |
| `spa/src/components/editor/EditorToolbar.tsx` | 新增 diff 按鈕、raw/WYSIWYG toggle |
| `spa/src/lib/register-modules.tsx` | 註冊 image-preview / pdf-preview pane + LocalBackend + 對應 opener |
| `spa/src/types/tab.ts` | 新增 image-preview / pdf-preview PaneContent |
| `spa/src/lib/pane-labels.ts` | 新增 image-preview / pdf-preview label/icon |
| `spa/src/lib/pane-utils.ts` | 新增 image-preview / pdf-preview contentMatches |
| `spa/src/lib/route-utils.ts` | 新增 image-preview / pdf-preview tabToUrl |
| `electron/preload.ts` | contextBridge 新增 fs.* IPC 方法 |
| `electron/main.ts` | 註冊 fs IPC handler |
| `spa/src/types/electron.d.ts` | 新增 FS IPC 型別宣告 |
| `spa/src/lib/platform.ts` | 新增 `hasLocalFilesystem` 能力旗標 |
| `spa/package.json` | 新增 Tiptap 依賴 |

---

## Task 1: 安裝 Tiptap 依賴

**Files:**
- Modify: `spa/package.json`

- [ ] **Step 1: 安裝 Tiptap**

```bash
cd spa && pnpm add @tiptap/react @tiptap/starter-kit @tiptap/pm @tiptap/markdown
```

- [ ] **Step 2: 確認安裝成功**

Run: `cd spa && pnpm ls @tiptap/react @tiptap/markdown`
Expected: 列出版本

- [ ] **Step 3: Commit**

```bash
git add spa/package.json spa/pnpm-lock.yaml
git commit -m "chore: add tiptap dependencies for markdown WYSIWYG"
```

---

## Task 2: Tiptap WYSIWYG 元件

**Files:**
- Create: `spa/src/components/editor/TiptapEditor.tsx`

- [ ] **Step 1: 實作 TiptapEditor**

```typescript
// spa/src/components/editor/TiptapEditor.tsx
import { useEditor, EditorContent } from '@tiptap/react'
import StarterKit from '@tiptap/starter-kit'
import { Markdown } from '@tiptap/markdown'
import { useEffect, useRef } from 'react'

interface Props {
  content: string        // raw markdown
  onChange: (markdown: string) => void
  onSave: () => void
}

export function TiptapEditor({ content, onChange, onSave }: Props) {
  const onSaveRef = useRef(onSave)
  onSaveRef.current = onSave

  const editor = useEditor({
    extensions: [
      StarterKit,
      Markdown,
    ],
    content,
    onUpdate: ({ editor: ed }) => {
      // @tiptap/markdown 官方 API：editor.getMarkdown()
      const md = ed.getMarkdown()
      onChange(md)
    },
    editorProps: {
      handleKeyDown: (_view, event) => {
        if ((event.metaKey || event.ctrlKey) && event.key === 's') {
          event.preventDefault()
          onSaveRef.current()
          return true
        }
        return false
      },
    },
  })

  // Sync external content changes (e.g., reload from disk)
  useEffect(() => {
    if (!editor) return
    const currentMd = editor.getMarkdown()
    if (currentMd !== content) {
      // 必須指定 contentType: 'markdown'，否則會被當 HTML 解析
      editor.commands.setContent(content, false, { contentType: 'markdown' })
    }
  }, [content, editor])

  if (!editor) return null

  return (
    <div className="flex-1 overflow-auto p-4 prose prose-invert prose-sm max-w-none">
      <EditorContent editor={editor} />
    </div>
  )
}
```

> **注意：** `@tiptap/markdown` 的官方 API 是 `editor.getMarkdown()` 和 `editor.commands.setContent(md, false, { contentType: 'markdown' })`。社群套件 `tiptap-markdown` 使用 `editor.storage.markdown.getMarkdown()` 是不同的 API，不要混淆。

- [ ] **Step 2: 確認 TypeScript 編譯**

Run: `cd spa && npx tsc --noEmit --pretty`
Expected: 無錯誤（若 `getMarkdown()` 型別未自動擴充，可能需要 `(editor as any).getMarkdown()` 暫時斷言，後續透過 Tiptap 型別擴充解決）

- [ ] **Step 3: Commit**

```bash
git add spa/src/components/editor/TiptapEditor.tsx
git commit -m "feat(editor): add TiptapEditor WYSIWYG component"
```

---

## Task 3: EditorPane Markdown toggle 整合

**Files:**
- Modify: `spa/src/components/editor/EditorPane.tsx`
- Modify: `spa/src/components/editor/EditorToolbar.tsx`

- [ ] **Step 1: 在 EditorToolbar 新增 raw/WYSIWYG toggle**

```typescript
// EditorToolbar.tsx 新增 props
interface Props {
  filePath: string
  isDirty: boolean
  isMarkdown: boolean
  editorMode: 'raw' | 'wysiwyg'
  onSave: () => void
  onToggleMode?: () => void
}

// 在 toolbar 中新增 toggle 按鈕（僅 markdown 時顯示）
{isMarkdown && onToggleMode && (
  <button
    onClick={onToggleMode}
    className="px-2 py-0.5 rounded text-[10px] border border-border-subtle hover:bg-surface-hover text-text-secondary transition-colors"
  >
    {editorMode === 'raw' ? 'WYSIWYG' : 'Raw'}
  </button>
)}
```

- [ ] **Step 2: 在 EditorPane 新增 mode state 和 TiptapEditor lazy import**

```typescript
// EditorPane.tsx 新增
import { lazy, Suspense, useState } from 'react'

const TiptapEditor = lazy(() =>
  import('./TiptapEditor').then((m) => ({ default: m.TiptapEditor }))
)

// 在 component 內新增：
const isMarkdown = filePath.endsWith('.md') || filePath.endsWith('.mdx')
const [editorMode, setEditorMode] = useState<'raw' | 'wysiwyg'>('raw')

// 在 render 中，根據 mode 切換：
{editorMode === 'raw' ? (
  <MonacoWrapper ... />
) : (
  <Suspense fallback={<div className="flex-1 flex items-center justify-center text-text-muted text-xs">Loading editor...</div>}>
    <TiptapEditor
      content={buffer.content}
      onChange={(md) => updateContent(key, md)}
      onSave={handleSave}
    />
  </Suspense>
)}
```

- [ ] **Step 3: 確認 TypeScript 編譯**

Run: `cd spa && npx tsc --noEmit --pretty`
Expected: 無錯誤

- [ ] **Step 4: Commit**

```bash
git add spa/src/components/editor/EditorPane.tsx spa/src/components/editor/EditorToolbar.tsx
git commit -m "feat(editor): integrate Tiptap WYSIWYG with raw/WYSIWYG toggle for markdown"
```

---

## Task 4: Monaco Diff View

**Files:**
- Create: `spa/src/components/editor/DiffView.tsx`
- Modify: `spa/src/components/editor/EditorPane.tsx`

- [ ] **Step 1: 實作 DiffView**

```typescript
// spa/src/components/editor/DiffView.tsx
import { DiffEditor } from '@monaco-editor/react'
import { useRef } from 'react'
import type { editor } from 'monaco-editor'

interface Props {
  original: string
  modified: string
  language: string
}

export function DiffView({ original, modified, language }: Props) {
  const editorRef = useRef<editor.IStandaloneDiffEditor | null>(null)

  return (
    <DiffEditor
      original={original}
      modified={modified}
      language={language}
      theme="vs-dark"
      onMount={(ed) => { editorRef.current = ed }}
      options={{
        readOnly: true,
        renderSideBySide: true,
        automaticLayout: true,
        fontSize: 13,
      }}
    />
  )
}
```

- [ ] **Step 2: 在 EditorPane 整合 diff mode**

在 `EditorPane.tsx` 中，當 `content.diff` 存在時：

```typescript
// 在 render 邏輯中新增：
if (content.diff) {
  const originalContent = content.diff.against === 'saved'
    ? buffer.savedContent
    : '' // 從另一個路徑讀取（需要額外 useEffect）

  return (
    <div className="flex-1 flex flex-col overflow-hidden">
      <EditorToolbar filePath={filePath} isDirty={false} isMarkdown={false} editorMode="raw" onSave={() => {}} />
      <div className="flex-1 overflow-hidden">
        <DiffView original={originalContent} modified={buffer.content} language={buffer.language} />
      </div>
    </div>
  )
}
```

- [ ] **Step 3: 在 EditorToolbar 新增 Diff 按鈕**

非 diff mode 時顯示 Diff 按鈕，開啟 unsaved changes diff：

```typescript
// EditorToolbar 新增 prop
onDiff?: () => void

// 在 toolbar 中：
{isDirty && onDiff && (
  <button onClick={onDiff} className="p-1 rounded hover:bg-surface-hover text-text-secondary transition-colors" title="Show diff">
    <GitDiff size={14} />
  </button>
)}
```

- [ ] **Step 4: 確認編譯**

Run: `cd spa && npx tsc --noEmit --pretty`
Expected: 無錯誤

- [ ] **Step 5: Commit**

```bash
git add spa/src/components/editor/DiffView.tsx spa/src/components/editor/EditorPane.tsx spa/src/components/editor/EditorToolbar.tsx
git commit -m "feat(editor): add Monaco diff view with unsaved changes comparison"
```

---

## Task 5: Image / PDF Preview Pane

**Files:**
- Create: `spa/src/components/editor/ImagePreviewPane.tsx`
- Create: `spa/src/components/editor/PdfPreviewPane.tsx`
- Modify: `spa/src/types/tab.ts`
- Modify: `spa/src/lib/pane-labels.ts`
- Modify: `spa/src/lib/pane-utils.ts`
- Modify: `spa/src/lib/route-utils.ts`
- Modify: `spa/src/lib/register-modules.tsx`

- [ ] **Step 1: 新增 PaneContent union 成員**

在 `spa/src/types/tab.ts` 新增：

```typescript
| { kind: 'image-preview'; source: FileSource; filePath: string }
| { kind: 'pdf-preview'; source: FileSource; filePath: string }
```

- [ ] **Step 2: 更新 pane-labels / pane-utils / route-utils**

`pane-labels.ts` getPaneLabel：
```typescript
case 'image-preview':
case 'pdf-preview':
  return content.filePath.split('/').pop() ?? content.filePath
```

getPaneIcon：
```typescript
case 'image-preview': return 'Image'
case 'pdf-preview': return 'FilePdf'
```

`pane-utils.ts` contentMatches：
```typescript
if (a.kind === 'image-preview' && b.kind === 'image-preview') {
  return a.filePath === b.filePath && a.source.type === b.source.type
}
if (a.kind === 'pdf-preview' && b.kind === 'pdf-preview') {
  return a.filePath === b.filePath && a.source.type === b.source.type
}
```

`route-utils.ts` tabToUrl：
```typescript
case 'image-preview': return '/'
case 'pdf-preview': return '/'
```

- [ ] **Step 3: 實作 ImagePreviewPane**

```typescript
// spa/src/components/editor/ImagePreviewPane.tsx
import { useEffect, useState } from 'react'
import type { PaneRendererProps } from '../../lib/module-registry'
import { getFsBackend } from '../../lib/fs-backend'

export function ImagePreviewPane({ pane }: PaneRendererProps) {
  const content = pane.content
  if (content.kind !== 'image-preview') return null

  const [objectUrl, setObjectUrl] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    let url: string | null = null
    const backend = getFsBackend(content.source)
    if (!backend) { setError('No FS backend'); return }

    backend.read(content.filePath)
      .then((data) => {
        url = URL.createObjectURL(new Blob([data]))
        setObjectUrl(url)
      })
      .catch((err: Error) => setError(err.message))

    return () => { if (url) URL.revokeObjectURL(url) }
  }, [content.source, content.filePath])

  if (error) return <div className="flex-1 flex items-center justify-center text-red-400 text-xs">{error}</div>
  if (!objectUrl) return <div className="flex-1 flex items-center justify-center text-text-muted text-xs">Loading...</div>

  const fileName = content.filePath.split('/').pop() ?? ''

  return (
    <div className="flex-1 flex flex-col overflow-hidden">
      <div className="px-3 py-1 border-b border-border-subtle bg-surface-secondary text-xs text-text-secondary truncate">
        {fileName}
      </div>
      <div className="flex-1 flex items-center justify-center p-4 overflow-auto bg-surface-primary">
        <img src={objectUrl} alt={fileName} className="max-w-full max-h-full object-contain" />
      </div>
    </div>
  )
}
```

- [ ] **Step 4: 實作 PdfPreviewPane**

```typescript
// spa/src/components/editor/PdfPreviewPane.tsx
import { useEffect, useState } from 'react'
import type { PaneRendererProps } from '../../lib/module-registry'
import { getFsBackend } from '../../lib/fs-backend'

export function PdfPreviewPane({ pane }: PaneRendererProps) {
  const content = pane.content
  if (content.kind !== 'pdf-preview') return null

  const [objectUrl, setObjectUrl] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    let url: string | null = null
    const backend = getFsBackend(content.source)
    if (!backend) { setError('No FS backend'); return }

    backend.read(content.filePath)
      .then((data) => {
        url = URL.createObjectURL(new Blob([data], { type: 'application/pdf' }))
        setObjectUrl(url)
      })
      .catch((err: Error) => setError(err.message))

    return () => { if (url) URL.revokeObjectURL(url) }
  }, [content.source, content.filePath])

  if (error) return <div className="flex-1 flex items-center justify-center text-red-400 text-xs">{error}</div>
  if (!objectUrl) return <div className="flex-1 flex items-center justify-center text-text-muted text-xs">Loading...</div>

  const fileName = content.filePath.split('/').pop() ?? ''

  return (
    <div className="flex-1 flex flex-col overflow-hidden">
      <div className="px-3 py-1 border-b border-border-subtle bg-surface-secondary text-xs text-text-secondary truncate">
        {fileName}
      </div>
      <iframe
        src={objectUrl}
        title={fileName}
        className="flex-1 border-none bg-white"
      />
    </div>
  )
}
```

- [ ] **Step 5: 在 register-modules.tsx 註冊 pane + opener**

```typescript
import { ImagePreviewPane } from '../components/editor/ImagePreviewPane'
import { PdfPreviewPane } from '../components/editor/PdfPreviewPane'

// 修改 editor module 的 panes 陣列：
registerModule({
  id: 'editor',
  name: 'Editor',
  panes: [
    { kind: 'editor', component: EditorPane },
    { kind: 'image-preview', component: ImagePreviewPane },
    { kind: 'pdf-preview', component: PdfPreviewPane },
  ],
})

// 註冊 image/pdf opener：
const IMAGE_EXTS = new Set(['png', 'jpg', 'jpeg', 'gif', 'webp', 'svg', 'ico'])
const PDF_EXTS = new Set(['pdf'])

registerFileOpener({
  id: 'image-preview',
  label: 'Image Preview',
  icon: 'Image',
  match: (file) => IMAGE_EXTS.has(file.extension.toLowerCase()),
  priority: 'default',
  createContent: (source, file) => ({ kind: 'image-preview', source, filePath: file.path } as PaneContent),
})

registerFileOpener({
  id: 'pdf-viewer',
  label: 'PDF Viewer',
  icon: 'FilePdf',
  match: (file) => PDF_EXTS.has(file.extension.toLowerCase()),
  priority: 'default',
  createContent: (source, file) => ({ kind: 'pdf-preview', source, filePath: file.path } as PaneContent),
})
```

- [ ] **Step 6: 確認編譯 + 測試**

Run: `cd spa && npx tsc --noEmit --pretty && npx vitest run`
Expected: 無錯誤，所有測試 PASS

- [ ] **Step 7: Commit**

```bash
git add spa/src/components/editor/ImagePreviewPane.tsx spa/src/components/editor/PdfPreviewPane.tsx \
  spa/src/types/tab.ts spa/src/lib/pane-labels.ts spa/src/lib/pane-utils.ts spa/src/lib/route-utils.ts \
  spa/src/lib/register-modules.tsx
git commit -m "feat(editor): add image-preview and pdf-preview pane kinds with openers"
```

---

## Task 6: 外部變更偵測

**Files:**
- Modify: `spa/src/components/editor/EditorPane.tsx`

- [ ] **Step 1: 新增 tab focus 偵測邏輯**

在 EditorPane 中新增 `useEffect`，當 `isActive` 變為 true 時檢查外部變更：

```typescript
// 在 EditorPaneInner 中新增（注意：isActive 和 key 都在 dependency array 中）：
useEffect(() => {
  if (!isActive) return

  const buf = useEditorStore.getState().buffers[key]
  if (!buf) return

  const backend = getFsBackend(source)
  if (!backend) return

  // Check for external changes on focus
  backend.stat(filePath)
    .then((stat) => {
      const currentBuf = useEditorStore.getState().buffers[key]
      if (!currentBuf?.lastStat) return
      if (stat.mtime === currentBuf.lastStat.mtime && stat.size === currentBuf.lastStat.size) return

      // mtime or size changed — read and compare
      return backend.read(filePath).then((data) => {
        const text = new TextDecoder().decode(data)
        const latestBuf = useEditorStore.getState().buffers[key]
        if (!latestBuf || text === latestBuf.savedContent) return

        if (!latestBuf.isDirty) {
          // Clean buffer — silent reload
          useEditorStore.getState().reloadBuffer(key, text, { mtime: stat.mtime, size: stat.size })
        } else {
          // Dirty buffer — show notification
          // TODO: implement conflict dialog (for now, just warn)
          console.warn(`[editor] External change detected for ${filePath}, buffer is dirty`)
        }
      })
    })
    .catch(() => {}) // File may have been deleted
  // eslint-disable-next-line react-hooks/exhaustive-deps — 只在 isActive 和 key 變化時觸發
}, [isActive, key])
```

- [ ] **Step 2: 確認編譯**

Run: `cd spa && npx tsc --noEmit --pretty`
Expected: 無錯誤

- [ ] **Step 3: Commit**

```bash
git add spa/src/components/editor/EditorPane.tsx
git commit -m "feat(editor): add external change detection on tab focus"
```

---

## Task 7: Electron LocalBackend IPC

**Files:**
- Create: `electron/fs-ipc.ts`
- Modify: `electron/preload.ts`
- Modify: `electron/main.ts`
- Modify: `spa/src/types/electron.d.ts`
- Modify: `spa/src/lib/platform.ts`
- Create: `spa/src/lib/fs-backend-local.ts`
- Modify: `spa/src/lib/register-modules.tsx`

- [ ] **Step 1: 建立 Electron FS IPC handler**

```typescript
// electron/fs-ipc.ts
import { ipcMain } from 'electron'
import { readFile, writeFile, stat, readdir, mkdir, rm, rename } from 'fs/promises'
import { resolve, isAbsolute } from 'path'
import { homedir } from 'os'

// 安全：限制只能存取 home 目錄及其子目錄
function validatePath(path: string): string {
  if (!isAbsolute(path)) throw new Error('Path must be absolute')
  const resolved = resolve(path)
  const home = homedir()
  if (!resolved.startsWith(home + '/') && resolved !== home) {
    throw new Error('Access denied: path outside home directory')
  }
  return resolved
}

export function registerFsIpc(): void {
  ipcMain.handle('fs:read', async (_event, path: string) => {
    const resolved = validatePath(path)
    const data = await readFile(resolved)
    return data // Uint8Array (serialized as Buffer over IPC)
  })

  ipcMain.handle('fs:write', async (_event, path: string, content: Uint8Array) => {
    const resolved = validatePath(path)
    await writeFile(resolved, content)
  })

  ipcMain.handle('fs:stat', async (_event, path: string) => {
    const resolved = validatePath(path)
    const s = await stat(resolved)
    return {
      size: s.size,
      mtime: s.mtimeMs,
      isDirectory: s.isDirectory(),
      isFile: s.isFile(),
    }
  })

  ipcMain.handle('fs:list', async (_event, path: string) => {
    const resolved = validatePath(path)
    const entries = await readdir(resolved, { withFileTypes: true })
    return entries
      .filter((e) => !e.name.startsWith('.'))
      .map((e) => ({
        name: e.name,
        isDir: e.isDirectory(),
        size: 0, // readdir doesn't provide size; caller can stat if needed
      }))
      .sort((a, b) => {
        if (a.isDir !== b.isDir) return a.isDir ? -1 : 1
        return a.name.localeCompare(b.name)
      })
  })

  ipcMain.handle('fs:mkdir', async (_event, path: string, recursive: boolean) => {
    const resolved = validatePath(path)
    await mkdir(resolved, { recursive })
  })

  ipcMain.handle('fs:delete', async (_event, path: string, recursive: boolean) => {
    const resolved = validatePath(path)
    await rm(resolved, { recursive, force: recursive })
  })

  ipcMain.handle('fs:rename', async (_event, from: string, to: string) => {
    const resolvedFrom = validatePath(from)
    const resolvedTo = validatePath(to)
    await rename(resolvedFrom, resolvedTo)
  })
}
```

- [ ] **Step 2: 在 main.ts 註冊**

```typescript
// electron/main.ts — 在現有的 registerIpcHandlers() 函式內呼叫（遵循 registerBrowserViewIpc 的 pattern）：
import { registerFsIpc } from './fs-ipc'

function registerIpcHandlers() {
  // ... existing handlers ...
  registerFsIpc()
}
```

- [ ] **Step 3: 在 preload.ts 暴露**

```typescript
// electron/preload.ts — 在 contextBridge.exposeInMainWorld 中新增：
fs: {
  read: (path: string) => ipcRenderer.invoke('fs:read', path),
  write: (path: string, content: Uint8Array) => ipcRenderer.invoke('fs:write', path, content),
  stat: (path: string) => ipcRenderer.invoke('fs:stat', path),
  list: (path: string) => ipcRenderer.invoke('fs:list', path),
  mkdir: (path: string, recursive: boolean) => ipcRenderer.invoke('fs:mkdir', path, recursive),
  delete: (path: string, recursive: boolean) => ipcRenderer.invoke('fs:delete', path, recursive),
  rename: (from: string, to: string) => ipcRenderer.invoke('fs:rename', from, to),
},
```

- [ ] **Step 4: 更新型別宣告**

```typescript
// spa/src/types/electron.d.ts — 新增：
fs: {
  read: (path: string) => Promise<Uint8Array>
  write: (path: string, content: Uint8Array) => Promise<void>
  stat: (path: string) => Promise<{ size: number; mtime: number; isDirectory: boolean; isFile: boolean }>
  list: (path: string) => Promise<Array<{ name: string; isDir: boolean; size: number }>>
  mkdir: (path: string, recursive: boolean) => Promise<void>
  delete: (path: string, recursive: boolean) => Promise<void>
  rename: (from: string, to: string) => Promise<void>
}
```

- [ ] **Step 5: 更新 platform.ts**

```typescript
// spa/src/lib/platform.ts — 在 PlatformCapabilities interface 新增欄位：
hasLocalFilesystem: boolean

// 在 getPlatformCapabilities() 的回傳物件新增：
hasLocalFilesystem: isElectron && !!window.electronAPI?.fs,
```

- [ ] **Step 6: 實作 LocalBackend**

```typescript
// spa/src/lib/fs-backend-local.ts
import type { FsBackend } from './fs-backend'
import type { FileStat, FileEntry } from '../types/fs'

export class LocalBackend implements FsBackend {
  readonly id = 'local'
  readonly label = 'Local Files'

  available(): boolean {
    return !!window.electronAPI?.fs
  }

  private get api() {
    const api = window.electronAPI?.fs
    if (!api) throw new Error('Local filesystem not available (requires Electron)')
    return api
  }

  async read(path: string): Promise<Uint8Array> {
    return this.api.read(path)
  }

  async write(path: string, content: Uint8Array): Promise<void> {
    return this.api.write(path, content)
  }

  async stat(path: string): Promise<FileStat> {
    return this.api.stat(path)
  }

  async list(path: string): Promise<FileEntry[]> {
    return this.api.list(path)
  }

  async mkdir(path: string, recursive?: boolean): Promise<void> {
    return this.api.mkdir(path, recursive ?? false)
  }

  async delete(path: string, recursive?: boolean): Promise<void> {
    return this.api.delete(path, recursive ?? false)
  }

  async rename(from: string, to: string): Promise<void> {
    return this.api.rename(from, to)
  }
}
```

- [ ] **Step 7: 在 register-modules.tsx 註冊 LocalBackend**

```typescript
import { LocalBackend } from '../lib/fs-backend-local'
import { getPlatformCapabilities } from '../lib/platform'

// 在 registerBuiltinModules() 中：
const caps = getPlatformCapabilities()
if (caps.hasLocalFilesystem) {
  registerFsBackend('local', new LocalBackend())
}
```

- [ ] **Step 8: 確認編譯**

Run: `cd spa && npx tsc --noEmit --pretty`
Expected: 無錯誤

- [ ] **Step 9: Commit**

```bash
git add electron/fs-ipc.ts electron/preload.ts electron/main.ts \
  spa/src/types/electron.d.ts spa/src/lib/platform.ts \
  spa/src/lib/fs-backend-local.ts spa/src/lib/register-modules.tsx
git commit -m "feat(editor): add Electron LocalBackend with IPC filesystem access"
```

---

## Task 8: BufferListSection（Settings 內 in-app buffer 管理）

> **Spec 4.6 要求**：Editor module 在 Settings 註冊一個管理頁面，列出所有 in-app buffer files，使用者可查看、刪除。

**Files:**
- Create: `spa/src/components/editor/BufferListSection.tsx`
- Modify: `spa/src/lib/register-modules.tsx`

- [ ] **Step 1: 實作 BufferListSection**

```typescript
// spa/src/components/editor/BufferListSection.tsx
import { useEffect, useState } from 'react'
import { Trash } from '@phosphor-icons/react'
import { getFsBackend } from '../../lib/fs-backend'
import type { FileEntry } from '../../types/fs'

export function BufferListSection() {
  const [files, setFiles] = useState<FileEntry[]>([])

  const refresh = async () => {
    const backend = getFsBackend({ type: 'inapp' })
    if (!backend) return
    try {
      const entries = await backend.list('/buffer')
      setFiles(entries.filter((e) => !e.isDir))
    } catch {
      setFiles([])
    }
  }

  useEffect(() => { refresh() }, [])

  const handleDelete = async (name: string) => {
    const backend = getFsBackend({ type: 'inapp' })
    if (!backend) return
    await backend.delete(`/buffer/${name}`)
    refresh()
  }

  if (files.length === 0) {
    return <p className="text-xs text-text-muted">No in-app files</p>
  }

  return (
    <div className="space-y-1">
      {files.map((f) => (
        <div key={f.name} className="flex items-center justify-between py-1 px-2 rounded hover:bg-surface-hover">
          <span className="text-xs text-text-primary truncate">{f.name}</span>
          <button
            onClick={() => handleDelete(f.name)}
            className="p-1 rounded hover:bg-surface-hover text-text-muted hover:text-red-400 transition-colors"
            title="Delete"
          >
            <Trash size={12} />
          </button>
        </div>
      ))}
    </div>
  )
}
```

- [ ] **Step 2: 在 register-modules.tsx 註冊 settings section**

```typescript
import { BufferListSection } from '../components/editor/BufferListSection'

registerSettingsSection({
  id: 'editor-buffers',
  label: 'settings.section.editor_buffers',
  order: 9,
  component: BufferListSection,
})
```

- [ ] **Step 3: 確認編譯**

Run: `cd spa && npx tsc --noEmit --pretty`
Expected: 無錯誤

- [ ] **Step 4: Commit**

```bash
git add spa/src/components/editor/BufferListSection.tsx spa/src/lib/register-modules.tsx
git commit -m "feat(editor): add BufferListSection for in-app file management in Settings"
```

---

## Task 9: 端到端驗證

**Files:** 無新增

- [ ] **Step 1: 驗證 Markdown WYSIWYG**

1. 開啟或建立 `.md` 檔案
2. Toolbar 應顯示 WYSIWYG toggle 按鈕
3. 點擊 toggle → Tiptap WYSIWYG 載入
4. 編輯內容 → 切回 Raw → Markdown 內容應反映 WYSIWYG 的修改
5. ⌘S 存檔 → 重新開啟確認 round-trip

- [ ] **Step 2: 驗證 Diff View**

1. 開啟檔案 → 修改內容（不存檔）
2. Toolbar 應顯示 Diff 按鈕
3. 點擊 Diff → 進入 side-by-side diff mode
4. 左側（original/saved）vs 右側（modified/current）

- [ ] **Step 3: 驗證 Image / PDF Preview**

1. 在 file tree 中點擊 `.png` → 應開啟 image-preview pane
2. 在 file tree 中點擊 `.pdf` → 應開啟 pdf-preview pane
3. Tab 標題顯示檔案名稱，icon 正確

- [ ] **Step 4: 驗證外部變更偵測**

1. 開啟一個檔案到 editor
2. 在外部修改該檔案（terminal 或其他 editor）
3. 切回 Purdex 的 editor tab
4. Clean buffer → 應自動 reload
5. Dirty buffer → 應出現 console 警告（v1 暫時 console.warn）

- [ ] **Step 5: 驗證 Electron LocalBackend（需要在 Electron app 中測試）**

1. 打包 Electron app
2. 開啟 → platform capabilities 應含 `hasLocalFilesystem: true`
3. 嘗試從 New Tab 建立檔案（應使用 InApp）
4. 如果有 Local 入口（Plan C 後續），嘗試開啟本機檔案

- [ ] **Step 6: Lint + 全部測試**

Run: `cd spa && pnpm run lint && npx vitest run`
Expected: 無錯誤

- [ ] **Step 7: Commit（如有修正）**

```bash
git add -A
git commit -m "fix(editor): address issues from Plan C integration testing"
```
