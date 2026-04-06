# Workspace 強化 + Editor Module 設計

**日期**: 2026-04-06
**狀態**: Draft
**基於**: [2026-03-20-tabbed-workspace-ui-design.md](2026-03-20-tabbed-workspace-ui-design.md)
**範圍**: 修訂 Workspace（Section 4）、修訂 Editor（Section 9）、新增 File Opener Registry

---

## 1. 概述

本文件是對既有 UI 設計 spec 的補充修訂，涵蓋三個面向：

1. **Workspace 強化** — 新增預設 host/path、快捷鍵切換、quick actions
2. **Editor Module 擴充** — 完整 Monaco editor 整合、明確存檔機制、二進制檔案預覽
3. **File Opener Registry** — 模組化的檔案開啟機制，解耦 file tree 與 editor

實作順序：Workspace 強化 → Side Panel 系統（Phase 3 照既有設計）→ Editor Module。

---

## 2. Workspace 強化（修訂 Section 4）

### 2.1 擴充 Workspace Interface

```typescript
interface Workspace {
  // 既有欄位
  id: string
  name: string
  color: string
  icon?: string
  tabs: string[]
  activeTabId: string | null
  sidebarState: WorkspaceSidebarState
  directories: PinnedItem[]

  // 新增欄位
  defaultHostId?: string   // 快速操作預設 host
  defaultPath?: string     // 快速操作預設工作目錄
  hotkey?: string          // 切換快捷鍵（如 ⌘1, ⌘2）
}
```

### 2.2 預設 Host / Path

- 工作區可設定一組預設的 `hostId` + `path`，作為快速操作的 context
- 不影響工作區內 tab 的歸屬——workspace 與 host/tab 之間沒有耦合關係
- 用途：一鍵在指定目錄啟動 agent session（Claude Code CLI、Codex CLI 等）

**設定入口：**
- 工作區右鍵選單 → 「設定」
- 工作區 dashboard（進階功能）

### 2.3 快捷鍵切換

- 每個工作區可綁定一個快捷鍵（如 `⌘1`、`⌘2`、自訂組合鍵）
- 按下快捷鍵 → 切換到該工作區（等同點擊 Activity Bar）
- 快捷鍵衝突偵測：設定時檢查是否與系統/其他工作區快捷鍵衝突
- 儲存在 workspace store，持久化

### 2.4 Quick Actions

基於 `defaultHostId` + `defaultPath` 的快速操作：

| Action | 說明 |
|--------|------|
| 新增 Terminal Session | 在預設 host/path 建立 tmux session，自動加入工作區 |
| 啟動 Claude Code | 在預設 path 啟動 `claude -p` stream session |
| 啟動 Codex | 在預設 path 啟動 codex CLI stream session |
| 開啟目錄 | 在 file tree panel 開啟預設 path |

Quick actions 的觸發方式：
- Activity Bar 工作區圖示右鍵選單
- 工作區 dashboard
- Command palette（未來）

### 2.5 Workspace Dashboard（進階）

每個工作區可選擇性地有自己的 dashboard tab，顯示：
- 工作區內 session 狀態總覽
- 預設 host 連線狀態
- Quick actions 按鈕
- 最近操作的檔案

Dashboard 為選用功能，不是必要的。

---

## 3. Editor Module（修訂 Section 9）

### 3.1 架構概覽

```
┌─────────────┐     ┌──────────────────┐     ┌─────────────┐
│  File Tree   │────→│ File Opener      │────→│ Editor Tab  │
│  (sidebar)   │     │ Registry         │     │ (Monaco)    │
└─────────────┘     └──────────────────┘     └──────┬──────┘
                                                     │
                    ┌──────────────────┐              │
                    │ Daemon FS API    │←─────────────┘
                    │ (read/write/list │
                    │  stat/grep)      │
                    └──────────────────┘
```

三個元件各自獨立：
- **File tree panel**：側欄面板，負責目錄瀏覽與檔案操作 UI
- **File opener registry**：中間層，決定如何開啟檔案
- **Editor pane**：tab 內容區，Monaco editor 或其他 viewer

共用 `fs` API layer（`lib/` 層封裝 daemon API 呼叫）。

### 3.2 v1 功能範圍

| 功能 | 複雜度 | 來源 |
|------|--------|------|
| 語法高亮 | 低 | Monaco 內建 |
| 搜尋取代（單檔 ⌘F / ⌘H） | 低 | Monaco 內建 |
| 多游標編輯 | 低 | Monaco 內建 |
| Minimap | 低 | Monaco 設定開關 |
| 檔案建立/刪除/重新命名 | 中 | Daemon API + file tree UI |
| 跨檔搜尋（grep） | 中高 | Daemon grep API + 結果 UI + 點擊跳轉 |
| Diff view（兩檔比較） | 中 | Monaco diff editor + UI 串接 |

**排除 v1：** git blame、inline diff（高複雜度，留待 Phase 5 git panel 整合時做）

### 3.3 存檔機制

採用**明確存檔**模式：

- `⌘S` 觸發寫回遠端（呼叫 daemon `POST /api/fs/write`）
- Tab 標題顯示 unsaved 標記（圓點 `●`）
- 關閉含未存檔變更的 tab 時顯示確認對話框
- 存檔失敗（網路問題、權限不足等）顯示錯誤通知

### 3.4 二進制檔案處理

| 檔案類型 | 處理方式 |
|----------|----------|
| 文字檔（程式碼、config、markdown） | Monaco editor，可編輯 |
| 圖片（png、jpg、gif、webp、svg） | 唯讀圖片預覽 |
| PDF | 唯讀 PDF 預覽 |
| 其他二進制 | 顯示檔案資訊，不開啟內容 |

**大檔案處理：**
- 不設硬性上限
- 開啟大檔案（閾值待定，暫定 5MB+）時顯示警告對話框：「此檔案較大，開啟可能影響效能。是否繼續？」
- 使用者確認後正常開啟

### 3.5 Editor Pane 介面

```
┌─────────────────────────────────────────────────┐
│ 📄 src/App.tsx ● (unsaved)       [Diff] [Save]  │  ← 工具列
├─────────────────────────────────────────────────┤
│                                          ▐████  │
│  import React from 'react'               ▐█  █  │
│  import { App } from './App'             ▐████  │  ← minimap
│  ...                                     ▐    █  │
│                                          ▐██  █  │
│                                                  │
├─────────────────────────────────────────────────┤
│ TypeScript │ UTF-8 │ LF │ Ln 42, Col 17         │  ← 狀態列
└─────────────────────────────────────────────────┘
```

**工具列：** 檔案路徑（可點擊複製）、unsaved 標記、Diff 按鈕、Save 按鈕
**狀態列：** 語言模式、編碼、換行符號、游標位置

### 3.6 Diff View

- 點擊工具列 Diff 按鈕 → 進入 diff 模式
- 預設比較：目前編輯內容 vs 磁碟上的版本（unsaved changes diff）
- 可選擇兩個已開啟的 editor tab 做比較
- 使用 Monaco 內建 diff editor 元件
- Side-by-side 或 inline diff 模式切換

### 3.7 跨檔搜尋（Grep）

**Daemon 端：**
- `POST /api/fs/grep`：接受 pattern、path、options（regex、case-sensitive、file glob）
- 回傳 match 列表：`{ file, line, column, content, contextBefore, contextAfter }`

**SPA 端：**
- 搜尋 UI 面板（側欄或浮動）
- 輸入框 + 選項（regex、大小寫、file glob filter）
- 結果按檔案分組，顯示匹配行與上下文
- 點擊結果 → 開啟 editor tab 並跳轉到該行

---

## 4. File Opener Registry

### 4.1 設計動機

將 file tree 與具體的檔案開啟行為解耦。File tree 只負責瀏覽和操作目錄結構，不知道也不關心檔案要怎麼開——由 registry 中註冊的 opener 決定。

這與既有的 `pane-registry`（pane renderer 註冊）模式一致。

### 4.2 介面定義

```typescript
interface FileInfo {
  name: string
  path: string
  extension: string
  size: number
  isDirectory: boolean
  mimeType?: string
}

interface FileOpener {
  id: string
  label: string                          // 顯示名稱（如 "Text Editor", "Image Preview"）
  icon: string                           // Phosphor icon name
  match: (file: FileInfo) => boolean     // 是否能處理此檔案
  priority: 'default' | 'option'         // default: 優先使用; option: 僅在 "Open With" 出現
  open: (hostId: string, path: string, file: FileInfo) => void  // 執行開啟
}

// Registry API
function registerFileOpener(opener: FileOpener): void
function unregisterFileOpener(id: string): void
function getFileOpeners(file: FileInfo): FileOpener[]           // 所有匹配的 opener
function getDefaultOpener(file: FileInfo): FileOpener | null    // 最高優先的 opener
```

### 4.3 內建 Openers

| ID | Match | Priority | 行為 |
|----|-------|----------|------|
| `monaco-editor` | 文字檔（by extension + mime） | default | 開啟 Monaco editor tab |
| `image-preview` | `png/jpg/gif/webp/svg/ico` | default | 開啟圖片預覽 tab |
| `pdf-viewer` | `pdf` | default | 開啟 PDF 預覽 tab |
| `markdown-preview` | `md` | option | 開啟 Markdown 渲染預覽 tab |

`markdown-preview` 為 option 優先級——`.md` 檔案預設用 Monaco editor 開啟，右鍵 "Open With" 可選 Markdown Preview。

### 4.4 File Tree 互動流程

```
使用者點擊檔案
  → file tree 呼叫 getDefaultOpener(fileInfo)
  → 找到 opener → 呼叫 opener.open(hostId, path, fileInfo)
  → opener 內部呼叫 tab store 建立對應 pane type 的 tab

使用者右鍵檔案 → "Open With..."
  → file tree 呼叫 getFileOpeners(fileInfo)
  → 列出所有匹配的 opener
  → 使用者選擇 → 呼叫 selected opener.open()
```

### 4.5 擴充性

未來可註冊的 opener 範例：
- `hex-viewer`：二進制檔案的 hex dump 檢視
- `csv-viewer`：CSV/TSV 表格檢視
- `log-viewer`：大型 log 檔案的串流檢視（不全部載入記憶體）
- `sqlite-browser`：SQLite 資料庫瀏覽

---

## 5. Daemon FS API（修訂 Section 13.1）

既有 spec 已定義基本端點，補充 grep 與細節：

| 端點 | 方法 | 用途 | 補充 |
|------|------|------|------|
| `/api/fs/list` | POST | 列出目錄內容 | 回傳 `FileInfo[]`，含 name/size/mtime/isDir |
| `/api/fs/read` | POST | 讀取檔案內容 | 回傳 raw content，大檔案 streaming |
| `/api/fs/write` | POST | 寫入檔案內容 | 接受 path + content，回傳成功/失敗 |
| `/api/fs/stat` | POST | 檔案/目錄資訊 | 回傳 size/mtime/permissions |
| `/api/fs/mkdir` | POST | 建立目錄 | 支援 recursive |
| `/api/fs/delete` | POST | 刪除檔案/目錄 | 目錄需明確 recursive flag |
| `/api/fs/rename` | POST | 重新命名/搬移 | 接受 from + to |
| `/api/fs/grep` | POST | 跨檔搜尋 | pattern/path/options → match 列表 |
| `/api/fs/watch` | WS | 監看目錄變動 | 推送 create/modify/delete 事件 |

所有端點統一 POST（含讀取操作），避免路徑洩漏到 access log。

FS API 實作為單一 Go module `internal/module/fs/`，不拆分。

### 5.1 安全考量

- 路徑驗證：禁止 `..` traversal，限制在 daemon 可存取的範圍
- 寫入保護：可選的唯讀模式（daemon config）
- 敏感檔案過濾：`.env`、私鑰等檔案預設警告（不阻擋，但顯示提示）

---

## 6. SPA 元件組織

### 6.1 新增 Pane Types

在 `PaneContent` discriminated union 中新增：

```typescript
// 新增到 PaneContent union
| { kind: 'editor'; hostId: string; filePath: string }
| { kind: 'image-preview'; hostId: string; filePath: string }
| { kind: 'pdf-preview'; hostId: string; filePath: string }
| { kind: 'diff-view'; hostId: string; leftPath: string; rightPath: string }
| { kind: 'grep-results'; hostId: string; query: GrepQuery }
```

### 6.2 新增檔案

| 路徑 | 職責 |
|------|------|
| `spa/src/lib/file-opener-registry.ts` | File opener 註冊與查詢 |
| `spa/src/lib/fs-api.ts` | Daemon FS API 封裝 |
| `spa/src/components/editor/EditorPane.tsx` | Monaco editor pane |
| `spa/src/components/editor/EditorToolbar.tsx` | 工具列（路徑、存檔、diff） |
| `spa/src/components/editor/EditorStatusBar.tsx` | 底部狀態列 |
| `spa/src/components/editor/DiffViewPane.tsx` | Diff view pane |
| `spa/src/components/editor/ImagePreviewPane.tsx` | 圖片預覽 |
| `spa/src/components/editor/PdfPreviewPane.tsx` | PDF 預覽 |
| `spa/src/components/sidebar/FileTreePanel.tsx` | 側欄 file tree 面板 |
| `spa/src/components/sidebar/GrepPanel.tsx` | 跨檔搜尋面板 |

### 6.3 新增 Store

```typescript
// Editor 狀態（非持久化）
interface EditorState {
  // key = compositeKey(hostId, filePath)
  buffers: Record<string, {
    content: string          // 目前編輯內容
    savedContent: string     // 上次存檔的內容
    language: string         // Monaco language ID
    isDirty: boolean         // content !== savedContent
    cursorPosition: { line: number; column: number }
  }>
}
```

Buffer 在 tab 關閉時清除。未存檔的 tab 關閉前需確認。

---

## 7. 實作順序

### Phase A：Workspace 強化

對應既有 spec Phase 2 的延伸。

1. 擴充 `Workspace` type（`defaultHostId`、`defaultPath`、`hotkey`）
2. Workspace 設定 UI（右鍵選單 → 設定面板）
3. 快捷鍵註冊與切換邏輯
4. Quick actions（基於 defaultHost/Path 建立 session）
5. Workspace dashboard tab（選用）

### Phase B：Side Panel 系統

照既有 spec Phase 3，無修訂。

### Phase C：Editor Module

對應既有 spec Phase 4 的擴充。

1. Daemon `fs` module — CRUD API（list/read/write/stat/mkdir/delete/rename）
2. SPA `fs-api.ts` 封裝層
3. File opener registry
4. Monaco editor pane（語法高亮、搜尋取代、多游標、minimap、⌘S 存檔）
5. 內建 openers 註冊（editor、image preview、PDF preview）
6. File tree panel（目錄瀏覽、檔案 CRUD 操作、file opener 整合）
7. 大檔案警告 + 二進制檔案偵測
8. Daemon grep API + 跨檔搜尋 UI
9. Diff view（Monaco diff editor 整合）
