# Editor Module 設計

**日期**: 2026-04-14
**狀態**: Draft
**取代**: [2026-04-06-workspace-and-editor-module-design.md](2026-04-06-workspace-and-editor-module-design.md) 中的 Section 3-6
**範圍**: Editor Module、FS 抽象層、File Opener Registry、module-registry 擴充

---

## 1. 概述

Editor Module 是 Purdex 的檔案閱讀與編輯模組。它是一個**消費者**——不主動管理檔案來源，而是透過 registry 接收其他模組（Files、Review 等）提供的開啟請求。

三個獨立但相關的部分：

1. **Editor Module** — 提供三種 pane kind（editor、image-preview、pdf-preview）+ New Tab 入口
2. **FS 抽象層** — 統一的檔案系統介面，三個 backend（InApp、Local、Daemon）
3. **File Opener Registry** — 解耦檔案來源與開啟方式的中間層

### 1.1 設計原則

- **Editor 是消費者**：透過 registry 接收開啟請求，不直接依賴任何檔案來源模組
- **Markdown 是 SOT**：`.md` 檔案以 Markdown 純文字為唯一真實來源，WYSIWYG 只是渲染方式
- **FS backend 透明**：editor 不需要知道檔案在哪一層，FS 抽象層處理差異
- **漸進式能力**：最低條件（SPA 無 daemon）也能運作（InApp 儲存），Electron 環境解鎖 local file 能力

---

## 2. 引擎選擇

### 2.1 程式碼 / 文字編輯：Monaco Editor

用於：程式碼檔案、raw Markdown 編輯、diff view。

選擇理由：
- Diff view 內建支援（Monaco diff editor）
- 語法高亮、搜尋取代、多游標、minimap 開箱即用
- Electron app 不受 bundle size 影響（~2.5MB gzip ~800KB）

### 2.2 Markdown WYSIWYG：Tiptap v3

用於：`.md` 檔案的 WYSIWYG 編輯模式（pane 內 toggle 切換）。

選擇理由：
- 最大社群（36K stars、6.7M 週下載），長期維護風險低（YC S23、VC 資金）
- MIT 授權
- v3 官方 `@tiptap/markdown` extension 支援 Markdown round-trip
- React 19 明確支援，效能調優工具成熟（`useEditorState`、`shouldRerenderOnTransaction`）

已知限制與對策：
- Markdown round-trip 非原生架構（v3.7.0 後加）→ 需為 table、footnotes 等自訂 serializer
- 官方標示為 early release → pin 版本，關鍵場景寫整合測試
- Frontmatter 不支援 → 自訂 extension 處理

### 2.3 Tiptap 載入策略

Tiptap lazy load——只在使用者開啟 `.md` 檔案並切換到 WYSIWYG 模式時才載入。一般程式碼編輯不會載入 Tiptap。

---

## 3. FS 抽象層

### 3.1 三層 Backend

```
Editor Module ──┐
                ├──→ FS 抽象層 ──┬── InAppBackend（IndexedDB）  → Electron + SPA
Files Module  ──┘                ├── LocalBackend（Node.js fs） → Electron only
                                 └── DaemonBackend（HTTP API）  → Electron + SPA
```

| Backend | 可用環境 | 儲存位置 | 用途 |
|---------|---------|---------|------|
| **InAppBackend** | Electron + SPA | IndexedDB | 無 daemon 無 local 時的 fallback，scratch pad |
| **LocalBackend** | Electron only | 本機 filesystem（Node.js fs via IPC） | 直接讀寫本機檔案，不需 daemon |
| **DaemonBackend** | Electron + SPA | 遠端主機 filesystem（HTTP API） | 讀寫 daemon 所在主機的檔案 |

### 3.2 統一介面

```typescript
interface FsBackend {
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

interface FileStat {
  size: number
  mtime: number       // Unix timestamp ms
  isDirectory: boolean
  isFile: boolean
}

interface FileEntry {
  name: string
  isDir: boolean
  size: number
}
```

### 3.3 v1 不實作的操作

- **grep（跨檔搜尋）**：延後，未來作為獨立功能加入
- **watch（檔案監聽）**：延後，用 tab focus 時 stat 比對替代

### 3.4 Daemon FS API

既有的 `GET /api/files?path=` 只支援 list。v1 需要擴充為完整的 FS module：

| 端點 | 方法 | 用途 |
|------|------|------|
| `/api/fs/list` | POST | 列出目錄內容 |
| `/api/fs/read` | POST | 讀取檔案內容 |
| `/api/fs/write` | POST | 寫入檔案內容 |
| `/api/fs/stat` | POST | 檔案/目錄資訊（size、mtime） |
| `/api/fs/mkdir` | POST | 建立目錄 |
| `/api/fs/delete` | POST | 刪除檔案/目錄 |
| `/api/fs/rename` | POST | 重新命名/搬移 |

全部使用 POST，避免路徑洩漏到 access log。

實作為單一 Go module `internal/module/fs/`，取代現有的 `internal/module/files/`。

### 3.5 LocalBackend Electron IPC

LocalBackend 透過 Electron IPC 呼叫 main process 的 Node.js `fs/promises`。遵循現有的 `browser-view-ipc.ts` + `preload.ts` pattern：

- `electron/preload.ts`：contextBridge 暴露 `window.electron.fs.*` 方法
- `electron/main.ts`（或新建 `electron/fs-ipc.ts`）：`ipcMain.handle('fs:*', ...)` handler
- `spa/src/types/electron.d.ts`：型別宣告

IPC channel 命名與具體實作留給 implementation plan，遵循既有 pattern 即可。

### 3.6 安全考量

- 路徑驗證：daemon 端和 Electron main process 端都必須做。禁止 `..` traversal，拒絕非絕對路徑
- 寫入保護：可選的唯讀模式（daemon config）
- 敏感檔案過濾：`.env`、私鑰等檔案開啟時顯示警告（不阻擋）

---

## 4. Editor Module

### 4.1 Pane Kinds

Editor Module 透過 `module-registry` 提供三個 pane kind：

| Pane Kind | Renderer | 用途 |
|-----------|----------|------|
| `editor` | Monaco / Tiptap + diff mode | 程式碼、文字、Markdown 編輯 |
| `image-preview` | 圖片預覽元件 | png、jpg、gif、webp、svg、ico |
| `pdf-preview` | PDF 預覽元件 | pdf |

需要將 `ModuleDefinition.pane`（單數）改為 `panes`（複數）以支援一個 module 提供多個 pane kind。

### 4.2 PaneContent 型別

```typescript
// 新增到 PaneContent union
| { kind: 'editor'; source: FileSource; filePath: string; diff?: { against: 'saved' | string } }
| { kind: 'image-preview'; source: FileSource; filePath: string }
| { kind: 'pdf-preview'; source: FileSource; filePath: string }

// FileSource 標示檔案來自哪個 FS backend
type FileSource =
  | { type: 'daemon'; hostId: string }
  | { type: 'local' }
  | { type: 'inapp' }
```

`source`：標示檔案來自哪個 FS backend，FS 抽象層根據此欄位路由到對應的 backend。

`diff.against`：
- `'saved'`：比較目前編輯內容 vs 磁碟版本（unsaved changes diff）
- 字串：另一個檔案的路徑（兩檔比較）

### 4.3 Editor Pane 內部架構

```
EditorPane
  ├── 判斷檔案類型
  │     └── isMarkdown(filePath)?
  │           ├── YES → 顯示 raw/WYSIWYG toggle
  │           └── NO  → 純 Monaco
  ├── 判斷 diff 模式
  │     └── diff prop 存在?
  │           ├── YES → MonacoDiffEditor
  │           └── NO  → MonacoEditor (or Tiptap)
  ├── EditorToolbar
  │     └── 檔案路徑、unsaved 標記、Diff 按鈕、Save 按鈕、(MD: raw/WYSIWYG toggle)
  └── EditorStatusBar
        └── 語言模式、編碼、換行符號、游標位置
```

### 4.4 Markdown 雙模式

`.md` 檔案的 editor pane 內建 toggle：

- **Raw 模式**（預設）：Monaco editor，語法高亮 + 標準編輯
- **WYSIWYG 模式**：Tiptap v3，所見即所得
- 切換時透過 Tiptap 的 markdown extension 做雙向轉換
- **Markdown 是 SOT**：切換到 WYSIWYG 時從 raw 內容 parse，切回 raw 時 serialize 回 Markdown

Markdown preview 並排（raw 左 + preview 右）為未來擴充，v1 先做 toggle 切換。

### 4.5 Editor Buffer Store

採用 VS Code TextModel/ModelService 模式——獨立的 runtime buffer store，與 PaneContent 和 FS backend 解耦。

```typescript
// useEditorStore（Zustand，不 persist）
interface EditorState {
  // key = compositeKey(source, filePath)
  buffers: Record<string, {
    content: string          // 目前編輯內容
    savedContent: string     // 上次存檔/讀取的內容
    isDirty: boolean         // content !== savedContent
    language: string         // Monaco language ID
    cursorPosition: { line: number; column: number }
    lastStat: { mtime: number; size: number } | null
  }>
}
```

三層分工：
- **PaneContent**（persist to localStorage）：只存 reference（source + filePath），不存內容
- **useEditorStore**（runtime，不 persist）：存 buffer 狀態，tab bar 從此讀取 `isDirty` 顯示 `●`
- **FS Backend**：實際 IO

生命週期：
- 開檔 → FS `read()` → 寫入 buffer store
- 編輯 → 更新 buffer store 的 `content`，自動計算 `isDirty`
- 存檔 → buffer `content` → FS `write()` → 更新 `savedContent`
- Tab 關閉 → 清除對應 buffer（dirty 時先確認）
- App 重開 → PaneContent 還原 → 重新 FS `read()` → 重建 buffer

### 4.6 BufferProvider（Editor 獨立運作）

當沒有 Files module 或其他檔案來源模組時，Editor module 透過內建的 BufferProvider 提供最小功能：

- BufferProvider 使用 InAppBackend（IndexedDB），為 editor 建立空文件
- PaneContent 與一般檔案相同：`{ kind: 'editor', source: { type: 'inapp' }, filePath: '/buffer/<id>.md' }`
- Buffer 有自己的 id（不綁 tab id），tab 關了 buffer 還在 IndexedDB
- **Settings 整合**：Editor module 在 Settings 註冊一個管理頁面，列出所有 in-app buffer files，使用者可查看、刪除
- New Tab 入口：透過 `new-tab-registry` 提供「建立新文件」選項

### 4.7 存檔機制

- **明確存檔**：`⌘S` 觸發寫回（呼叫 FS 抽象層的 `write()`）
- **Unsaved 標記**：tab 標題顯示 `●`（content !== savedContent）
- **關閉確認**：關閉含未存檔變更的 tab 時顯示確認對話框
- **存檔失敗**：顯示錯誤通知（網路問題、權限不足等）

### 4.6 外部變更偵測

採用 VS Code 策略（不實作 watch，用 stat 比對替代）：

1. Tab 取得 focus 時 → `stat()` 取得 mtime + size
2. 與上次讀取時的 mtime / size 比較
3. 不一致 → 讀取檔案內容，與 buffer 的 `savedContent` 比對
4. 確實不同時：
   - **Buffer clean**（無未存檔修改）→ 靜默 reload
   - **Buffer dirty**（有未存檔修改）→ 提示使用者選擇（accept external / keep mine / show diff）

### 4.7 大檔案處理

- 不設硬性上限
- 開啟大檔案（暫定 5MB+）時顯示警告：「此檔案較大，開啟可能影響效能。是否繼續？」
- 使用者確認後正常開啟

### 4.8 二進制檔案處理

| 檔案類型 | Pane Kind | 處理方式 |
|----------|-----------|---------|
| 文字檔（程式碼、config、markdown） | `editor` | Monaco / Tiptap，可編輯 |
| 圖片（png、jpg、gif、webp、svg、ico） | `image-preview` | 唯讀圖片預覽 |
| PDF | `pdf-preview` | 唯讀 PDF 預覽 |
| 其他二進制 | `editor` | 顯示檔案資訊（大小、類型），不開啟內容 |

---

## 5. File Opener Registry

### 5.1 設計動機

將檔案來源（Files module、Review module 等）與開啟行為（editor、preview 等）解耦。遵循既有的 registry pattern（與 `new-tab-registry`、`settings-section-registry` 一致）。

### 5.2 介面定義

```typescript
interface FileInfo {
  name: string
  path: string
  extension: string
  size: number
  isDirectory: boolean
}

interface FileOpener {
  id: string
  label: string
  icon: string                                        // Phosphor icon name（string）
  match: (file: FileInfo) => boolean
  priority: 'default' | 'option'
  createContent: (source: FileSource, file: FileInfo) => PaneContent  // 回傳 PaneContent，不碰 store
}

// Registry API
function registerFileOpener(opener: FileOpener): void
function getFileOpeners(file: FileInfo): FileOpener[]
function getDefaultOpener(file: FileInfo): FileOpener | null
```

Opener 只負責回傳 `PaneContent`，**不負責建立 tab**。Tab 建立由消費端（Files module 等）統一處理，確保 workspace 插入位置、singleton 判斷等邏輯集中管理。

### 5.3 Editor Module 註冊的 Openers

| ID | Match | Priority | 回傳 PaneContent |
|----|-------|----------|-----------------|
| `monaco-editor` | 文字檔（by extension） | default | `{ kind: 'editor', source, filePath }` |
| `image-preview` | png/jpg/gif/webp/svg/ico | default | `{ kind: 'image-preview', source, filePath }` |
| `pdf-viewer` | pdf | default | `{ kind: 'pdf-preview', source, filePath }` |

### 5.4 消費流程

```
使用者在 file tree 點擊檔案
  → Files Module 呼叫 getDefaultOpener(fileInfo)
  → 拿到 opener（例如 monaco-editor）
  → 呼叫 opener.createContent(source, fileInfo)
  → 拿到 PaneContent
  → Files Module 負責 createTab(content) + addTab()

Review Module 開啟 diff
  → 直接建立 PaneContent：{ kind: 'editor', source, filePath, diff: { against: 'saved' } }
  → 自行 createTab(content) + addTab()
  → 不經過 file-opener-registry（review module 知道自己要什麼）
```

---

## 6. module-registry 擴充

### 6.1 pane 改為 panes

```typescript
// Before
interface ModuleDefinition {
  pane?: PaneDefinition
  // ...
}

// After
interface ModuleDefinition {
  panes?: PaneDefinition[]
  // ...
}
```

`getPaneRenderer(kind)` 調整為：
```typescript
for (const m of modules.values()) {
  for (const p of m.panes ?? []) {
    if (p.kind === kind) return p
  }
}
```

### 6.2 受影響的檔案

`pane → panes` 是破壞性改動，以下檔案全部需要同步修改：

| 檔案 | 影響 |
|------|------|
| `spa/src/lib/module-registry.ts` | `ModuleDefinition` 型別 + `getPaneRenderer()` 遍歷邏輯 |
| `spa/src/lib/register-modules.tsx` | 8 個既有 module 的 `pane:` → `panes: [...]` |
| `spa/src/components/NewPanePage.tsx` | `m.pane` → flatMap `m.panes`，且新 kind 必須排除在 `SIMPLE_KINDS` 外 |
| `spa/src/lib/pane-labels.ts` | `getPaneLabel` / `getPaneIcon` 新增三個 kind 的 case |
| `spa/src/lib/pane-utils.ts` | `contentMatches` 新增 editor/image-preview/pdf-preview 的比對邏輯（同 kind + 同 filePath + 同 source） |
| `spa/src/lib/route-utils.ts` | `tabToUrl` 新增三個 kind 的 URL mapping |
| `spa/src/lib/module-registry.test.ts` | 測試 fixture 欄位名稱 |
| `spa/src/lib/register-modules.test.ts` | 同上 |

### 6.3 向後相容

既有的 module 只有一個 pane，遷移為 `panes: [{ kind, component }]` 即可。一次性修改，不需要 migration 機制（alpha 階段）。

### 6.4 NewPanePage 注意事項

`NewPanePage` 的 `SIMPLE_KINDS`（只需 kind 即可建立的 pane）必須排除 `editor`、`image-preview`、`pdf-preview`，因為這三個 kind 需要 `source` + `filePath` 才能建立合法的 PaneContent。

---

## 7. Registry 整體架構

Editor Module 啟動時的註冊全景：

| Registry | 註冊內容 | 消費者 |
|----------|---------|--------|
| `module-registry` | 三個 pane kind（editor、image-preview、pdf-preview） | PaneLayoutRenderer |
| `file-opener-registry` | 三個 opener（monaco-editor、image-preview、pdf-viewer） | Files module、任何未來模組 |
| `new-tab-registry` | editor 的 New Tab section | NewTabPage |

各 registry 獨立，型別安全，遵循既有的 Service Locator pattern。Editor 對消費端零耦合。

---

## 8. 元件組織

### 8.1 新增檔案

| 路徑 | 職責 |
|------|------|
| `spa/src/lib/fs-backend.ts` | FS 抽象層介面定義 + FileSource type |
| `spa/src/lib/fs-backend-inapp.ts` | InApp backend（IndexedDB） |
| `spa/src/lib/fs-backend-local.ts` | Local backend（Electron IPC） |
| `spa/src/lib/fs-backend-daemon.ts` | Daemon backend（HTTP API） |
| `spa/src/lib/file-opener-registry.ts` | File opener 註冊與查詢 |
| `spa/src/stores/useEditorStore.ts` | Editor buffer store（runtime，不 persist） |
| `spa/src/components/editor/EditorPane.tsx` | Editor pane 主元件（路由 + 狀態管理） |
| `spa/src/components/editor/MonacoEditor.tsx` | Monaco editor 封裝 |
| `spa/src/components/editor/TiptapEditor.tsx` | Tiptap WYSIWYG 封裝（lazy load） |
| `spa/src/components/editor/DiffView.tsx` | Monaco diff editor 封裝 |
| `spa/src/components/editor/EditorToolbar.tsx` | 工具列 |
| `spa/src/components/editor/EditorStatusBar.tsx` | 底部狀態列 |
| `spa/src/components/editor/ImagePreviewPane.tsx` | 圖片預覽 |
| `spa/src/components/editor/PdfPreviewPane.tsx` | PDF 預覽 |
| `spa/src/components/editor/BufferListSection.tsx` | Settings 內的 in-app buffer 管理列表 |
| `electron/fs-ipc.ts` | Electron main process FS IPC handler |
| `internal/module/fs/module.go` | Go daemon FS module |
| `internal/module/fs/handler.go` | FS API handler |

### 8.2 修改檔案

| 路徑 | 修改內容 |
|------|---------|
| `spa/src/lib/module-registry.ts` | `pane` → `panes`（複數）+ `getPaneRenderer` 遍歷邏輯 |
| `spa/src/lib/register-modules.tsx` | 新增 editor module 註冊，既有 module 遷移為 `panes` |
| `spa/src/types/tab.ts` | 新增 editor / image-preview / pdf-preview PaneContent + FileSource type |
| `spa/src/components/PaneLayoutRenderer.tsx` | 配合 `panes` 調整 |
| `spa/src/components/NewPanePage.tsx` | `m.pane` → flatMap `m.panes`，排除非 SIMPLE_KINDS |
| `spa/src/lib/pane-labels.ts` | 新增三個 kind 的 label / icon |
| `spa/src/lib/pane-utils.ts` | `contentMatches` 新增 filePath + source 比對 |
| `spa/src/lib/route-utils.ts` | `tabToUrl` 新增三個 kind |
| `spa/src/components/FileTreeView.tsx` | `GET /api/files` → FS 抽象層 `DaemonBackend.list()` |
| `electron/preload.ts` | contextBridge 新增 `fs.*` IPC 方法 |
| `spa/src/types/electron.d.ts` | 新增 FS IPC 型別宣告 |
| `cmd/pdx/main.go` | 註冊 fs module，取代 files module |

---

## 9. 與其他模組的關係

```
                    ┌─────────────────────┐
                    │  file-opener-registry │
                    └──────┬──────────────┘
                           │ register openers (createContent)
                    ┌──────┴──────────────┐
                    │    Editor Module     │
                    │  ├─ editor pane      │
                    │  ├─ image-preview    │
                    │  ├─ pdf-preview      │
                    │  ├─ useEditorStore   │  ← runtime buffer（不 persist）
                    │  └─ BufferProvider   │  ← InApp fallback
                    └──────┬──────────────┘
                           │ consumes FS
                    ┌──────┴──────────────┐
                    │   FS 抽象層          │
                    ├─ InAppBackend       │
                    ├─ LocalBackend       │
                    └─ DaemonBackend      │
                    └─────────────────────┘

Files Module ──→ file-opener-registry.getDefaultOpener()
             ──→ opener.createContent() → PaneContent
             ──→ Files Module 自行 createTab() + addTab()

Review Module ──→ 直接建立 PaneContent { kind: 'editor', diff }
              ──→ 自行 createTab() + addTab()

Editor standalone ──→ BufferProvider → InAppBackend
                  ──→ New Tab 入口建立空文件
```

Editor Module 不知道 Files / Review module 的存在。Files / Review module 不知道 Editor 的內部實作。Tab 建立由消費端負責，opener 只回傳 PaneContent。

---

## 10. 未來擴充（不在 v1 範圍）

- **grep（跨檔搜尋）**：daemon `POST /api/fs/grep` + 搜尋 UI panel + 結果跳轉
- **watch（檔案監聽）**：daemon WS endpoint + 即時偵測外部變更
- **更多 file opener**：hex viewer、CSV viewer、SQLite browser
- **Markdown preview 並排**：pane 內部 split，raw 左 + preview 右
- **Agent Review Module**：獨立模組，即時/事後 diff review，借用 editor 的 diff view
