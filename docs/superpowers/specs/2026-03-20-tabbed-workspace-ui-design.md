# tmux-box 分頁 + 工作區 UI 重構設計

**日期**: 2026-03-20
**狀態**: Draft
**範圍**: SPA 前端 UI 架構重構，後端 API 擴充

---

## 1. 設計目標

將 tmux-box 從「單 session 檢視」升級為「多分頁 + 工作區 + 多主機」架構，同時保持 terminal 內容最大化的核心體驗。

### 核心原則

- 分頁列主導佈局，側欄可折疊，內容區域最大化
- 工作區即分頁群組，不另設獨立群組概念
- 漸進式複雜度：單 Bar → 多 Bar，使用者自行選擇
- 圖示統一使用 Phosphor Icons

---

## 2. 整體佈局

**選定方案：頂部分頁列主導（方案 A）**

```
┌─────────────────────────────────────────────────────┐
│  [Bar▾] │ ⚑ WS1 [tab][tab][tab] │ ⚑ WS2 │ [tab] + │  ← 分頁列
├────────┬────────────────────────────────────────────┤
│        │                                            │
│ 側欄   │           內容區域                          │
│ 面板   │   Terminal / Stream / Editor               │
│        │                                            │
│        │                                            │
├────────┴────────────────────────────────────────────┤
│  host │ session │ status              │ mode │ size │  ← 狀態列
└─────────────────────────────────────────────────────┘
```

### 佈局元素

| 區域 | 說明 | 可見性 |
|------|------|--------|
| 分頁列 | Bar 切換 + 工作區群組 + 獨立分頁 + 操作按鈕 | 常駐 |
| 側欄面板 | 六種面板以分頁切換 | 可折疊，位置可自訂（左/右/分開） |
| 內容區域 | 當前分頁的內容渲染 | 常駐 |
| 狀態列 | 當前連線狀態摘要 | 常駐 |

---

## 3. 分頁系統

### 3.1 分頁類型

| 類型 | 圖示 | 說明 |
|------|------|------|
| Terminal | `Terminal` | tmux session 終端連線（xterm.js） |
| Stream | `ChatCircleDots` | Claude Code 串流對話 |
| Editor | `File` / `FileCode` | 遠端檔案檢視/編修 |
| （預留） | — | 未來擴充用 |

### 3.2 分頁狀態

每個分頁為一個獨立實體，包含：

```typescript
interface Tab {
  id: string              // 唯一識別
  type: 'terminal' | 'stream' | 'editor'
  label: string           // 顯示名稱
  icon: string            // Phosphor icon name
  hostId: string          // 所屬主機
  sessionName?: string    // terminal/stream: tmux session 名稱
  filePath?: string       // editor: 遠端檔案路徑
  isDirty?: boolean       // editor: 是否有未儲存變更
}
```

**所有權原則：** Tab 不持有 `workspaceId`。分頁與工作區的歸屬關係由 `Workspace.tabs` 單向管理（Workspace 為 source of truth）。判斷一個分頁是否為獨立分頁，透過「不被任何 Workspace.tabs 包含」來推導。這避免雙向引用造成的同步問題。

### 3.3 分頁操作

- **點擊**: 切換到該分頁
- **中鍵點擊 / 關閉按鈕**: 關閉分頁
- **拖曳**: 重新排序，或拖入/拖出工作區
- **右鍵選單**: 關閉、關閉其他、移到工作區、複製路徑等

---

## 4. 工作區（Workspace）

工作區本質上是**帶有上下文的分頁群組**。

### 4.1 工作區結構

```typescript
interface Workspace {
  id: string
  name: string
  color: string            // 群組 chip 的色標
  directories: PinnedItem[] // 釘選的目錄和檔案
  tabs: string[]           // tab IDs（有序）
  activeTabId: string      // 當前活躍分頁
  sidebarState: SidebarState // 側欄面板狀態（按工作區記憶）
}

interface PinnedItem {
  type: 'directory' | 'file'
  hostId: string
  path: string
}
```

### 4.2 工作區功能

- **跨主機**: 一個工作區可以混合不同 host 的 sessions、目錄
- **目錄監看**: 釘選的目錄/檔案即時顯示變動（新增、修改、刪除標記）
- **上下文切換**: 切換工作區時，側欄目錄/Git/資訊面板跟著切換
- **啟動 Session**: 從工作區直接建立新的 tmux session
- **側欄記憶**: 每個工作區記住自己的側欄面板選擇和寬度

### 4.3 獨立分頁

不屬於任何工作區的分頁：
- 在分頁列上與工作區群組並列（用分隔線區隔）
- 仍可在側欄顯示自己的相關資訊（如 terminal 的 session 狀態）
- 可拖入工作區加入群組

---

## 5. Bar 系統

### 5.1 三種模式

#### 單 Bar（預設）

所有工作區群組 + 獨立分頁在同一列。不顯示 Bar 切換下拉。

```
│ ⚑ WS1 [tab][tab] │ ⚑ WS2 [tab] │ [standalone] │ + │
```

群組可收合為子母層，降低橫向壓力：

```
母層: │ ⚑ WS1(3) │ ⚑ WS2(2) │ [standalone] │ + │
子層: │ [tab1]  [tab2]  [tab3]                     │
```

#### 多 Bar（手動配置）

使用者建立多個 bar，每個 bar 有自己的分頁集合。分頁列最前方增加下拉選單切換 bar。

```
│ [開發環境 ▾] │ ⚑ WS1 [tab][tab] │ [standalone] │ + │
```

下拉選單列出所有 bar，附帶內容摘要。

#### 自動多 Bar

每個工作區自動成為獨立 bar，獨立分頁歸入「一般」bar。等同 Vivaldi 工作區切換。

```
│ [My Project ▾] │ [tab1] [tab2] [tab3] │ + │
```

### 5.2 Bar 結構

```typescript
interface Bar {
  id: string
  name: string
  icon?: string
  workspaceIds: string[]  // 包含的工作區（有序）
  standaloneTabs: string[] // 包含的獨立分頁（有序）
}

type BarMode = 'single' | 'multi' | 'auto'
```

**所有權規則：**
- 每個 Workspace 只能歸屬一個 Bar（一對多）
- 獨立分頁（不屬於任何 Workspace 的 Tab）只能歸屬一個 Bar
- `single` 模式下只有一個 Bar，包含所有 Workspace 和獨立分頁
- `auto` 模式下每個 Workspace 自動映射為一個 Bar，所有獨立分頁歸入名為「一般」的預設 Bar
- `multi` 模式下使用者自由分配 Workspace 和獨立分頁到不同 Bar

### 5.3 模式切換

- 設定中可切換模式
- 從單 bar 手動新增 bar 會自動進入多 bar 模式
- 自動模式下，新建工作區自動建立對應 bar
- 模式切換時的遷移邏輯：
  - `single` → `multi`：現有的單 bar 成為第一個 bar，新 bar 初始為空
  - `single` → `auto`：每個 workspace 拆出為獨立 bar，獨立分頁歸入「一般」
  - `multi` → `single`：所有 bar 合併回一個 bar
  - `auto` → `multi`：自動產生的 bar 保留，使用者可自由調整

---

## 6. 橫列式群組行為

### 6.1 展開/收合的控制方式

每個工作區群組**各自獨立**控制展開/收合狀態（不是全域開關）。

- **預設狀態**：所有群組展開（平攤子分頁）
- **切換方式**：點擊群組 chip 左側的展收箭頭（或雙擊群組 chip）
- **記憶**：每個群組的展收狀態持久化

### 6.2 展開狀態

工作區的子分頁平攤在分頁列中，操作方式與一般分頁相同。群組 chip 在最前方，帶有工作區色標。分頁列維持**單行**。

```
│ ⚑[色標] WS名稱 │ [tab1] [tab2] [tab3] │ ... │
```

### 6.3 收合狀態（子母層）

當**任何一個以上的群組收合**時，分頁列轉為**雙行結構**：

- **母層（上）**：收合的群組 chip（顯示分頁數量）+ 展開的群組 chip（不含子分頁）+ 獨立分頁
- **子層（下）**：目前被選中的收合群組的子分頁列表

點擊不同的收合群組 chip → 切換子層顯示該群組的分頁。

```
母層: │ ⚑ WS1(3) │ ⚑ WS2(2) │ [standalone] │ + │
子層: │ [tab1]  [tab2]  [tab3]                     │  ← WS1 的分頁
```

**當所有群組都展開時**，子層消失，回到單行結構。

### 6.4 混合狀態

部分群組展開、部分收合時：
- 展開的群組子分頁在母層平攤（和一般分頁混在一起）
- 收合的群組只在母層顯示 chip
- 子層顯示當前被選中的收合群組的分頁

### 6.5 群組操作

- **點擊群組 chip**: 展開模式下選中工作區；收合模式下切換子層內容
- **展收箭頭 / 雙擊**: toggle 群組展開/收合
- **右鍵群組 chip**: 重新命名、變更顏色、展開/收合、刪除工作區等
- **拖曳分頁進群組**: 加入該工作區
- **拖曳分頁出群組**: 變為獨立分頁

---

## 7. 側欄面板

### 7.1 六種面板

| # | 面板 | Phosphor Icon | 說明 |
|---|------|---------------|------|
| 1 | Sessions | `List` | 所有 host 的 tmux session 清單，按主機分組 |
| 2 | 目錄 | `FolderOpen` | 當前工作區的釘選目錄/檔案，即時變動標記 |
| 3 | Git | `GitBranch` | 當前工作區目錄的 git log / changes / branches |
| 4 | 資訊 | `Info` | 工作區摘要（host、branch、分頁數、sessions） |
| 5 | 提示詞 | `Lightning` | 可編輯的提示詞清單，點擊注入當前分頁輸入 |
| 6 | AI 歷史 | `ClockCounterClockwise` | 工作區的 stream 對話歷史紀錄 |

### 7.2 面板位置

- 使用者可將面板放在左側或右側
- 支援左右各開一個面板（分開模式）
- 位置設定持久化

### 7.3 面板操作

- 頂部分頁列（圖示）切換面板內容
- 拖曳邊緣調整寬度
- 拖到最小自動折疊
- 快捷鍵 toggle 顯示/隱藏

### 7.4 上下文感知

- 切換工作區 → 目錄/Git/資訊/AI 歷史面板跟著切換
- 選中獨立分頁 → 顯示該分頁的相關資訊
- 每個工作區記憶自己的側欄面板選擇

### 7.5 Sessions 面板細節

- 按主機分組顯示所有 session
- 顯示 session 名稱、模式圖示、狀態（running/streaming/idle）
- 點擊 session → 開啟為新分頁（或切換到已開啟的分頁）
- 支援搜尋過濾
- 底部「新增 Session」按鈕

### 7.6 目錄面板細節

- 顯示當前工作區的釘選目錄和檔案
- 樹狀展開目錄結構
- 即時顯示檔案變動標記（M: modified, +: added, D: deleted, 數字: 變動數量）
- 點擊檔案 → 開啟為 Editor 分頁
- 底部「釘選」按鈕新增目錄或檔案
- 核心用途之一：監看 AI 工作過程中的檔案變動

### 7.7 提示詞注入細節

- 可編輯維護的提示詞清單
- 點擊提示詞 → 依當前分頁類型注入：
  - Terminal 分頁 → 送入 tmux session
  - Stream 分頁 → 貼入 StreamInput 輸入框
  - Editor 分頁 → 不適用（或插入游標位置）
- 支援變數模板（如 `{dir}` 替換為當前目錄）

---

## 8. Quick Switcher

### 8.1 觸發方式

- 快捷鍵 ⌘K（可自訂）
- 側欄 Sessions 面板中的搜尋也可觸發

### 8.2 介面

- 置中覆蓋面板（modal overlay）
- 頂部搜尋框，即時過濾
- 結果按主機分組顯示
- 每個結果顯示：名稱、模式、狀態

### 8.3 操作

- 鍵盤上下鍵選取
- Enter: 開啟為新分頁（或切換到已開啟的分頁）
- Shift+Enter: 開啟在新工作區
- ESC: 關閉
- 目前定位為純 tmux session 選單，架構預留日後擴展為通用切換器

---

## 9. 檔案編輯器

### 9.1 功能

- 透過 daemon 讀寫遠端主機的檔案系統
- 程式碼檔案：語法高亮顯示
- Markdown 檔案：支援 Raw / Preview 模式切換
- 顯示檔案修改狀態

### 9.2 介面

- 頂部工具列：檔案路徑、修改狀態、模式切換按鈕、儲存按鈕
- 主體：程式碼/Markdown 內容
- 底部狀態：檔案類型、編碼、行列位置

### 9.3 開啟方式

- 側欄目錄面板點擊檔案
- Quick Switcher（未來擴展）
- 工作區內其他操作（如 git diff 點擊檔案）

---

## 10. 多主機管理

### 10.1 Host 結構

```typescript
interface Host {
  id: string
  name: string           // 顯示名稱（如 mlab, air-2019）
  address: string        // daemon 連線地址
  port: number           // daemon 端口
  status: 'connected' | 'disconnected' | 'connecting'
}
```

### 10.2 Host 管理

- 設定中新增/編輯/刪除 host
- 每個 host 獨立的 daemon 連線
- Session 清單按 host 分組
- 工作區可跨 host（混合不同主機的 sessions 和目錄）

### 10.3 連線管理

- 每個 host 獨立的 WebSocket 連線集合（terminal WS、session-events WS、stream WS）
- 自動重連機制（沿用現有的指數退避策略）
- 狀態列顯示當前分頁的 host 連線狀態

---

## 11. 狀態管理

### 11.1 新增 Store

現有三個 Zustand store 需要擴充，並新增：

| Store | 職責 |
|-------|------|
| `useTabStore` | 分頁狀態、排序、活躍分頁 |
| `useWorkspaceStore` | 工作區定義、目錄、分頁群組 |
| `useBarStore` | Bar 配置、模式、切換 |
| `useHostStore` | 多主機連線狀態 |
| `useSidebarStore` | 側欄面板狀態、位置、寬度 |

### 11.2 持久化

以下狀態需持久化（localStorage 或 daemon 端）：

- 分頁列表和排序
- 工作區定義和釘選目錄
- Bar 配置和模式
- Host 清單
- 側欄位置和寬度
- Quick Switcher 快捷鍵

### 11.3 現有 Store 影響

- `useSessionStore`: 保留，但 `activeId` 概念被 `useTabStore.activeTabId` 取代
- `useStreamStore`: 保留，每個 stream 分頁對應一個 session 的 stream 狀態
- `useConfigStore`: 擴充，增加 bar mode、sidebar position 等設定

---

## 12. 路由

### 12.1 從 Hash 路由升級

現有 `#/{uid}/{mode}` 需升級以支援分頁系統：

**方案：保留 hash 指向當前活躍分頁，完整狀態由 store 管理**

```
#/tab/{tabId}
```

分頁的完整資訊（類型、session、host 等）存在 store 中，hash 只負責指向活躍分頁。這樣可以支援直接 URL 分享和重新整理後恢復。

---

## 13. 後端 API 擴充

### 13.1 檔案系統 API（新增）

> **設計決策**：FS 和 Git API 統一使用 POST 方法（包含讀取操作），避免檔案路徑透過 URL query string 洩漏到 access log。

| 端點 | 方法 | 用途 |
|------|------|------|
| `/api/fs/read` | POST | 讀取檔案內容 |
| `/api/fs/write` | POST | 寫入檔案內容 |
| `/api/fs/list` | POST | 列出目錄內容 |
| `/api/fs/watch` | WS | 監看目錄/檔案變動 |
| `/api/fs/stat` | POST | 取得檔案/目錄資訊 |

### 13.2 Git API（新增）

| 端點 | 方法 | 用途 |
|------|------|------|
| `/api/git/log` | POST | 取得 git log |
| `/api/git/status` | POST | 取得 git status |
| `/api/git/branches` | POST | 列出 branches |

### 13.3 設定 API（擴充）

現有 `/api/config` 擴充支援：

- Bar 模式設定
- Host 清單
- 側欄偏好
- 提示詞清單

---

## 14. 手機版（響應式設計）— 待獨立設計

### 14.1 基本原則

- 分頁列簡化：只顯示當前分頁名稱 + 左右滑動切換
- 側欄改為全螢幕抽屜（從左/右滑出）
- Quick Switcher 全螢幕
- 工作區群組改為純下拉切換（不支援橫列展開）
- 檔案編輯器：只讀預覽為主，編輯功能視螢幕寬度

### 14.2 待確認項目

手機版的具體互動細節需要另外用 mockup 核對：

- 分頁切換的手勢操作
- 側欄面板的觸控操作
- Terminal 在小螢幕的操作體驗
- Bar 切換在手機上的呈現方式

---

## 15. 實作分期建議

### Phase 1：分頁系統基礎
- TabStore + Tab 元件
- 分頁列渲染（無群組）
- 多分頁切換 + 內容區域動態渲染
- 重構現有 App.tsx 佈局

### Phase 2：工作區
- WorkspaceStore + 工作區群組 UI
- 橫列式展開/收合（子母層）
- 工作區色標 + 右鍵選單
- 分頁拖曳（群組內外）

### Phase 3：側欄面板
- SidebarStore + 面板框架
- Sessions 面板（重構現有 SessionPanel）
- 目錄面板 + 檔案變動監看
- 其餘面板逐項實作

### Phase 4：Bar 系統
- BarStore + Bar 切換 UI
- 多 Bar 手動配置
- 自動多 Bar 模式

### Phase 5：檔案編輯器
- 後端 FS API
- Editor 分頁元件
- Markdown 預覽

### Phase 6：多主機
- HostStore + Host 管理 UI
- 多 daemon 連線架構
- Session 清單按 host 分組

### Phase 7：進階功能
- Quick Switcher
- 提示詞注入
- AI 對話歷史面板
- Git 面板
- 手機版響應式

---

## 16. 技術考量

### 16.1 分頁效能

- Terminal 分頁：xterm.js 實例在非活躍時應保留（避免重連開銷），但可卸載 WebGL renderer
- Stream 分頁：訊息列表在非活躍時保留在 store，元件可卸載
- Editor 分頁：檔案內容快取在 store

### 16.2 拖曳實作

- 使用原生 HTML5 Drag and Drop API 或輕量拖曳庫
- 分頁拖曳目標：重新排序、移入工作區、移出工作區
- 視覺回饋：拖曳時顯示 drop indicator

### 16.3 檔案監看

- 後端使用 `fsnotify` 監看釘選目錄
- 透過 WebSocket 推送變動事件到前端
- 前端增量更新目錄樹狀態

### 16.4 多主機連線

- 每個 host 獨立的連線管理實例
- 共用現有的自動重連邏輯
- Store 中以 hostId 為 key 區分不同主機的資料
