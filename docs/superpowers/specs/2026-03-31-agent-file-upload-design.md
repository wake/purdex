# Agent File Upload — Phase 1.6c-pre3

SPA/Electron 拖曳檔案到 terminal view，上傳至 daemon，daemon 將路徑注入 tmux pane 讓 CC TUI 接收處理。

## 範圍

- Daemon upload + inject endpoint（agent module）
- SPA TerminalView 拖曳處理
- StatusBar 上傳進度顯示
- StatusBar agent label badge 視覺改版

### 不做（延後）

- 暫存管理 UI / 自動清理 → Host 管理頁面（Phase 1.6c）
- User input buffer（上傳期間暫停使用者輸入）→ 非必要
- Stream 模式上傳 → 遠期

## 啟用條件

- 僅 CC agent 模式下啟用（`useAgentStore.statuses[sessionCode]` 存在且非 `undefined`）
- 不區分 agent 子狀態（running / idle / waiting / error 都可上傳）
- Codex agent 模式暫時忽略

## Daemon API

### `POST /api/agent/upload`

掛在 agent module（`RegisterRoutes` 內）。agent module 需新增對 session module 的依賴（`Dependencies` 加 `"session"`），透過 `Registry` 取得 `SessionProvider` 以查詢 session code → tmux session name 的映射。

**Request**：`multipart/form-data`

| 欄位 | 類型 | 必填 | 說明 |
|------|------|------|------|
| `file` | file | 是 | 上傳的檔案，不限大小與類型 |
| `session` | string | 是 | session code |

**處理流程**：

1. 驗證 session code 有效（tmux session 存在）
2. 建立目錄 `~/tmp/tbox-upload/{session_code}/`（如不存在）
3. 存檔，同名衝突加後綴
4. `tmux send-keys -t {tmux_session} " {path}" -l`（一律空格前綴，`-l` literal mode）
5. 回傳結果

**Response**：`200 OK`

```json
{
  "filename": "screenshot.png",
  "injected": true
}
```

**錯誤**：

| 狀態碼 | 說明 |
|--------|------|
| 400 | 缺少 file 或 session |
| 404 | session 不存在 |
| 500 | 存檔失敗或 send-keys 失敗 |

### 暫存目錄

- 預設路徑：`~/tmp/tbox-upload/`
- 結構：`{upload_dir}/{session_code}/{filename}`
- 未來可在 Host 管理頁面設定路徑（per-host daemon config）

### 同名檔案後綴

```
file.png 已存在 → file-1.png → file-2.png → ...
file（無副檔名）→ file-1 → file-2 → ...
```

迴圈遞增，不設上限。

## SPA — TerminalView 拖曳處理

### 拖曳偵測

TerminalView 元件內部自行處理 drag-drop（不抽共用元件，各 module 內部自行處理）。

- `onDragEnter`：counter++，顯示 drop overlay
- `onDragOver`：`preventDefault()`
- `onDragLeave`：counter--，counter 歸零時隱藏 overlay
- `onDrop`：收集 `FileList`，開始上傳流程

Counter 模式參考現有 ConversationView 的巢狀事件處理。

### 啟用 / 不啟用

- Agent 活躍（statuses 存在）→ 攔截拖曳事件，顯示 overlay
- Agent 不活躍 → 不攔截，穿透到 terminal 原生行為

### Drop Overlay

- 半透明深色遮罩覆蓋整個 terminal 區域
- 中央顯示 Phosphor Icons `UploadSimple` 圖示 + 文字「拖放檔案上傳」

### 上傳流程

```
drop → files[0..N]
  → for each file (sequential):
      update store: currentFile = filename, completed count
      POST /api/agent/upload (multipart: file + session_code)
      → 成功：completed++
      → 失敗：failed++, 記錄 error filename
  → 全部完成 → update store status (done / error)
```

逐一上傳逐一注入（方案 A），每個檔案上傳完 daemon 立即 send-keys，terminal 上路徑逐個出現作為天然進度回饋。

## SPA — API Function

`spa/src/lib/api.ts` 新增：

```typescript
export async function agentUpload(
  base: string,
  file: File,
  session: string,
): Promise<{ filename: string; injected: boolean }> {
  const form = new FormData()
  form.append('file', file)
  form.append('session', session)
  const res = await fetch(`${base}/api/agent/upload`, {
    method: 'POST',
    body: form,
  })
  if (!res.ok) throw new Error(`${res.status} ${res.statusText}`)
  return res.json()
}
```

## SPA — Upload Store

`useUploadStore`（Zustand，不 persist）管理 per-session 上傳狀態。

```typescript
interface SessionUploadState {
  total: number
  completed: number
  failed: number
  currentFile: string    // 當前正在上傳的檔名
  error?: string         // 最近一次失敗的檔名
  status: 'uploading' | 'done' | 'error' | null
}

interface UploadState {
  sessions: Record<string, SessionUploadState>
  startUpload: (session: string, total: number, firstFile: string) => void
  fileCompleted: (session: string) => void
  fileFailed: (session: string, filename: string) => void
  nextFile: (session: string, filename: string) => void
  dismiss: (session: string) => void
}
```

## SPA — StatusBar 上傳進度

從 `useUploadStore` 讀取當前 active session 的上傳狀態，顯示在 agent label 右側、view mode toggle 左側。

| 狀態 | 顯示 | 行為 |
|------|------|------|
| `uploading` | 黃色 spinner + `Uploading file.png (2/5)...` | 持續顯示直到完成 |
| `done` | 綠色勾 + `5 files uploaded` | 3 秒後自動 dismiss |
| `error` | 紅色叉 + `Upload failed: file.png` 或 `3 uploaded, 2 failed` | 點擊 / 重新拖曳 / 30 秒後 dismiss |
| `null` | 不顯示 | — |

### 錯誤消除條件

以下任一條件觸發 dismiss：
- 使用者點擊錯誤訊息
- 重新拖曳檔案（新一輪上傳覆蓋舊狀態）
- 30 秒超時

## SPA — StatusBar Agent Label Badge

現有 agent label 從純文字改為帶框 badge：

| 情境 | 樣式 |
|------|------|
| 有 model name（#127 修復後） | 橘棕色 badge：`bg-[rgba(154,96,56,0.15)]` / `text-[#e8956a]` / `border-[rgba(180,110,65,0.3)]` |
| Fallback `Agent` | 白色 badge：`bg-white/8` / `text-white/70` / `border-white/15` |

Badge 高度比 view mode toggle 略矮（`py-0 px-[7px] leading-4 rounded-[3px]`）。
