# Phase 12：Editor Module

**日期**: 2026-04-06
**狀態**: Draft
**前置**: Phase 11（Side Panel 系統）完成
**基於**: [tabbed-workspace-ui-design.md](2026-03-20-tabbed-workspace-ui-design.md) Section 9, 13
**補充**: [workspace-and-editor-module-design.md](2026-04-06-workspace-and-editor-module-design.md) Section 3-6

---

## 1. 目標

實作遠端檔案編輯功能：daemon 端 FS API、Monaco editor tab、file opener registry、file tree sidebar panel、跨檔搜尋、diff view。

---

## 2. 架構

```
┌─────────────┐     ┌──────────────────┐     ┌─────────────┐
│  File Tree   │────→│ File Opener      │────→│ Editor Tab  │
│  (sidebar)   │     │ Registry         │     │ (Monaco)    │
└─────────────┘     └──────────────────┘     └──────┬──────┘
                                                     │
                    ┌──────────────────┐              │
                    │ Daemon FS API    │←─────────────┘
                    │ (Go module)      │
                    └──────────────────┘
```

三元件解耦：
- **File tree**（側欄 panel）不知道怎麼開檔
- **File opener registry**（中間層）決定用哪個 opener
- **Editor pane**（tab 內容）專注編輯

---

## 3. 工作項目

### 12.1 Daemon FS Module

Go module `internal/module/fs/`，統一 POST 方法（路徑不走 URL 防洩漏）。

| 端點 | 用途 |
|------|------|
| `POST /api/fs/list` | 列出目錄（回傳 FileInfo[]） |
| `POST /api/fs/read` | 讀取檔案內容 |
| `POST /api/fs/write` | 寫入檔案 |
| `POST /api/fs/stat` | 檔案/目錄資訊 |
| `POST /api/fs/mkdir` | 建立目錄（支援 recursive） |
| `POST /api/fs/delete` | 刪除檔案/目錄 |
| `POST /api/fs/rename` | 重新命名/搬移 |
| `POST /api/fs/grep` | 跨檔搜尋 |
| `WS /ws/fs/watch` | 目錄變動監看 |

安全：
- 路徑驗證（禁 `..` traversal）
- 可選唯讀模式（daemon config）
- 敏感檔案警告（`.env`、私鑰等，不阻擋但提示）

### 12.2 SPA FS API 封裝

`spa/src/lib/fs-api.ts`：封裝 daemon FS API，使用 `hostFetch` 注入 auth。

```typescript
function listDir(hostId: string, path: string): Promise<FileInfo[]>
function readFile(hostId: string, path: string): Promise<FileContent>
function writeFile(hostId: string, path: string, content: string): Promise<void>
function statFile(hostId: string, path: string): Promise<FileStat>
function mkdir(hostId: string, path: string, recursive?: boolean): Promise<void>
function deleteFile(hostId: string, path: string, recursive?: boolean): Promise<void>
function renameFile(hostId: string, from: string, to: string): Promise<void>
function grepFiles(hostId: string, query: GrepQuery): Promise<GrepResult[]>
```

### 12.3 File Opener Registry

`spa/src/lib/file-opener-registry.ts`

```typescript
interface FileOpener {
  id: string
  label: string
  icon: string
  match: (file: FileInfo) => boolean
  priority: 'default' | 'option'
  open: (hostId: string, path: string, file: FileInfo) => void
}
```

- `registerFileOpener` / `unregisterFileOpener`
- `getDefaultOpener(file)` → 最高優先的 opener
- `getFileOpeners(file)` → 所有匹配的 opener（供 "Open With" 選單）

### 12.4 Monaco Editor Pane

新增 pane type：`{ kind: 'editor'; hostId: string; filePath: string }`

功能：
- 語法高亮（Monaco 內建，by file extension）
- 搜尋取代（⌘F / ⌘H）
- 多游標編輯
- Minimap
- ⌘S 明確存檔 → daemon `POST /api/fs/write`
- Tab 標題 unsaved 標記（●）
- 關閉未存檔 tab 時確認對話框

元件：
- `EditorPane.tsx`：Monaco editor 主體
- `EditorToolbar.tsx`：路徑、unsaved 標記、Diff / Save 按鈕
- `EditorStatusBar.tsx`：語言、編碼、換行、游標位置

### 12.5 內建 Openers 註冊

| Opener | Match | Priority | Pane Type |
|--------|-------|----------|-----------|
| `monaco-editor` | 文字檔 | default | `editor` |
| `image-preview` | png/jpg/gif/webp/svg/ico | default | `image-preview` |
| `pdf-viewer` | pdf | default | `pdf-preview` |
| `markdown-preview` | md | option | `markdown-preview` |

額外 pane types：
- `{ kind: 'image-preview'; hostId: string; filePath: string }`
- `{ kind: 'pdf-preview'; hostId: string; filePath: string }`

`ImagePreviewPane.tsx`：讀取圖片 → 顯示（fit/actual size 切換）
`PdfPreviewPane.tsx`：讀取 PDF → 嵌入式預覽

### 12.6 File Tree Panel

註冊為 Side Panel（Phase 8 的 panel registry）：
- `id: 'file-tree'`
- `scope: 'workspace'`
- `defaultZone: 'left-inner'`

功能：
- 樹狀目錄瀏覽（lazy load 子目錄）
- 檔案/目錄建立、刪除、重新命名（右鍵選單）
- 點擊檔案 → 呼叫 file opener registry → 開對應 tab
- 右鍵檔案 → "Open With..." 列出所有匹配 openers
- 工作區 `defaultPath` 作為初始根目錄
- 目錄變動即時更新（daemon `WS /ws/fs/watch`）

### 12.7 大檔案與二進制偵測

- 文字檔大小 > 5MB（暫定）→ 開啟前顯示警告對話框
- 二進制檔案偵測：檢查 file extension + daemon stat 回傳的 mime type
- 無匹配 opener 的二進制檔 → 顯示檔案資訊頁（大小、修改時間、mime type）

### 12.8 Grep — 跨檔搜尋

Daemon 端：
- `POST /api/fs/grep`：pattern + path + options（regex、case、glob filter）
- 回傳：`{ file, line, column, content, contextBefore[], contextAfter[] }`

SPA 端：
- `GrepPanel.tsx`：註冊為 side panel（`scope: 'workspace'`、`defaultZone: 'left-inner'`）
- 搜尋輸入框 + 選項（regex toggle、case toggle、file glob）
- 結果按檔案分組、顯示匹配行與前後各 2 行 context
- 點擊結果 → 開啟 editor tab 並跳轉到該行列

### 12.9 Diff View

新增 pane type：`{ kind: 'diff-view'; hostId: string; leftPath: string; rightPath: string }`

功能：
- Monaco diff editor 元件
- 預設：目前編輯 vs 磁碟版本（unsaved changes diff）
- 可選擇兩個已開啟的 editor tab 做比較
- Side-by-side / inline 模式切換

觸發方式：
- Editor toolbar「Diff」按鈕
- 右鍵選單「Compare with...」

---

## 4. 依賴關係

```
12.1 Daemon FS ──→ 12.2 SPA API ──→ 12.4 Monaco Editor
                                  ──→ 12.6 File Tree
                                  ──→ 12.8 Grep

12.3 Opener Registry ──→ 12.5 Openers 註冊
                      ──→ 12.6 File Tree（開檔行為）

12.4 Monaco Editor ──→ 12.7 大檔案處理
                   ──→ 12.9 Diff View

12.5 Openers ──→ 12.7 二進制偵測
```

可並行的組合：
- 12.1（daemon）與 12.3（registry）無依賴，可並行
- 12.8（grep）與 12.9（diff）無互依賴，可並行

---

## 5. Editor Store

```typescript
// 非持久化，tab 關閉時清除
interface EditorState {
  buffers: Record<string, {     // key = compositeKey(hostId, filePath)
    content: string             // 目前編輯內容
    savedContent: string        // 磁碟版本
    language: string            // Monaco language ID
    isDirty: boolean
    cursorPosition: { line: number; column: number }
  }>
}
```

---

## 6. 測試策略

- 12.1：Go test（API handler + 路徑驗證 + 權限控制）
- 12.2：API 封裝 unit test（request 格式、error handling）
- 12.3：registry CRUD + match + priority 排序
- 12.4：editor pane 渲染 + save flow + dirty state
- 12.5：各 opener match 規則 + open 行為
- 12.6：file tree 展開/收合 + CRUD 操作 + opener 整合
- 12.7：大檔案警告觸發 + 二進制偵測準確性
- 12.8：grep 結果渲染 + 點擊跳轉
- 12.9：diff view 渲染 + 模式切換
