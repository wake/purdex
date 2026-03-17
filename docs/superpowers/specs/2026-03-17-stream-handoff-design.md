# Stream Handoff 雙向切換設計

## 概述

在 term（互動式 CC）與 stream（`-p` 串流模式）之間實現雙向 handoff，使用 `--resume <session_id>` 精確續接同一 CC 對話。使用者可在兩種模式間自由切換，不遺失對話脈絡。

## 前提條件

Handoff **僅在 CC 正在執行時可用**（`cc-idle` / `cc-running` / `cc-waiting`）。若 tmux session 處於 shell（`normal`）或執行非 CC 程式（`not-in-cc`），handoff 按鈕 disabled，不可觸發。

## Handoff to Stream（term → stream）

### 觸發方式

使用者在 stream 頁面點擊 "Handoff" 按鈕（原 "Start {preset}" 按鈕更名）。

### 後端流程（`handoff_handler.go` — `runHandoff`）

```
前提檢查: detect(session) ∈ {cc-idle, cc-running, cc-waiting}
         否則 → broadcast("failed:no CC running") 並返回

Step 1 — 斷開現有 relay（若有）
  與現有邏輯相同：送 shutdown → 等 5s relay 斷開

Step 2 — 中斷進行中的工作（若非 idle）
  若 status = cc-running 或 cc-waiting:
    tmux send-keys C-u          // 清空輸入區
    tmux send-keys C-c          // 中斷當前任務
    輪詢等待 idle (❯)，最多 10s
    若超時 → broadcast("failed:could not reach CC idle")

Step 3 — 擷取 session ID
  tmux send-keys "/status"      // 注入 /status 指令
  等待 1-2s（讓 CC 輸出 status 資訊）
  tmux capture-pane 取最近 30 行
  解析 session_id（正規表達式匹配）
  若解析失敗 → broadcast("failed:could not extract session ID")

Step 4 — 退出 CC
  tmux send-keys Escape         // 跳離 /status 顯示畫面
  等待 0.5s
  tmux send-keys "/exit"        // 優雅退出 CC
  輪詢等待 StatusNormal (shell)，最多 10s
  若超時 → broadcast("failed:CC did not exit")

Step 5 — 啟動 relay
  寫入 token 臨時檔案（C3 安全，與現有邏輯相同）
  組裝指令:
    tbox relay --session {name} --daemon ws://127.0.0.1:{port} \
      --token-file {path} -- \
      claude -p --input-format stream-json --output-format stream-json \
      --resume {session_id}
  tmux send-keys 注入指令

Step 6 — 等待 relay 回連
  輪詢 bridge.HasRelay(session)，最多 15s
  relay init 訊息中的 session_id 由前端 store 接收並保存

Step 7 — 更新狀態
  DB: session.mode = "stream"
  DB: session.cc_session_id = {session_id}  // 新欄位
  broadcast("connected")
```

### 事件廣播順序

```
detecting → stopping-cc (若需要) → extracting-id → exiting-cc → launching → connected
```

新增兩個進度狀態：`extracting-id`、`exiting-cc`。

## Handoff to Term（stream → term）

### 觸發方式

使用者在 stream 模式下方工具欄最右側點擊 "Handoff to Term" 按鈕。

### 後端流程（新增 `runHandoffToTerm` 或擴充現有 handler）

```
Step 1 — 取得 session_id
  從 DB (session.cc_session_id) 或前端請求 body 取得

Step 2 — 關閉 relay
  bridge.SubscriberToRelay(session, {"type":"shutdown"})
  等 relay 斷開（最多 5s）
  若超時 → broadcast("failed:relay did not disconnect")

Step 3 — 等待 shell
  輪詢 detect(session) = StatusNormal，最多 10s
  （relay 結束後其子程序 claude -p 也會退出，shell 應很快恢復）

Step 4 — 注入互動式 CC
  tmux send-keys "claude --resume {session_id}"

Step 5 — 更新狀態
  DB: session.mode = "term"
  broadcast("handoff-to-term:connected")
```

### API 端點

擴充現有 `POST /api/sessions/{id}/handoff`：

```json
// Handoff to stream（現有）
{ "mode": "stream", "preset": "cc" }

// Handoff to term（新增）
{ "mode": "term" }
```

當 `mode = "term"` 時，不需要 preset，改走 `runHandoffToTerm` 邏輯。

## 前端變更

### 1. 狀態保留 — 雙 View 同時掛載

**現況**：`App.tsx` 用條件渲染，切頁時卸載非當前 View。

**變更**：TerminalView 與 ConversationView 同時 mount，用 CSS `display: none` 控制可見性。

```tsx
// App.tsx — 主內容區
<div className="flex-1 overflow-hidden">
  {active && (
    <>
      <div style={{ display: currentMode === 'term' ? 'block' : 'none', height: '100%' }}>
        <TerminalView wsUrl={...} />
      </div>
      <div style={{ display: currentMode === 'stream' ? 'flex' : 'none', height: '100%' }}>
        <ConversationView wsUrl={...} ... />
      </div>
    </>
  )}
</div>
```

**效果**：
- 切到 term 時保留 stream WebSocket 連線與訊息歷史
- 切到 stream 時保留 terminal PTY 連線
- TerminalView 的 xterm.js 需在重新顯示時觸發 `fit()`

**注意**：未來分頁模式（下一期）會進一步擴展此架構，為每個開啟的 session 保留獨立的 View 實例。

### 2. HandoffButton 更名

```tsx
// 原本
{state === 'handoff-in-progress' ? progressLabel(progress) : `Start ${presetName}`}

// 改為
{state === 'handoff-in-progress' ? progressLabel(progress) : 'Handoff'}
```

**啟用條件**：按鈕僅在 session status 為 `cc-idle` / `cc-running` / `cc-waiting` 時可點擊。其他狀態 disabled + 顯示提示 "No CC running"。

新增進度標籤：
- `extracting-id` → "Extracting session..."
- `exiting-cc` → "Exiting CC..."

### 3. StreamInput — 新增 "Handoff to Term" 按鈕

在 `StreamInput` 底部工具欄最右側新增按鈕：

```tsx
<div className="flex items-center px-2 pb-1.5">
  <button onClick={onAttach} ...>
    <Plus size={16} />
  </button>
  <div className="flex-1" />
  <button onClick={onHandoffToTerm} ...>
    <Terminal size={14} />
    <span>Handoff to Term</span>
  </button>
</div>
```

Props 新增：`onHandoffToTerm: () => void`

### 4. TopBar

stream 按鈕行為不變。仍透過 preset 選擇觸發 handoff to stream。

## 資料模型變更

### sessions 表新增欄位

```sql
ALTER TABLE sessions ADD COLUMN cc_session_id TEXT NOT NULL DEFAULT '';
```

用途：儲存最近一次從 CC 擷取的 session ID，供 handoff to term 使用。

### Store 更新

```go
type SessionUpdate struct {
    Mode        *string
    Name        *string
    GroupID     *int64
    CCSessionID *string  // 新增
}
```

## /status 輸出解析

CC 的 `/status` 指令會輸出包含 session ID 的資訊。需要：

1. 確認 `/status` 的輸出格式（可能包含 `Session: <uuid>` 或類似欄位）
2. 編寫正規表達式解析 session ID
3. 在 `internal/detect/` 或新建 `internal/extract/` 中實作解析邏輯

**實作前需確認**：在實際環境中執行 `/status` 並記錄輸出格式，以此為基礎編寫解析器。

## 錯誤處理

| 情境 | 處理 |
|------|------|
| CC 未執行 | 前端 disable 按鈕；後端 reject handoff |
| 中斷 CC 超時 (10s) | broadcast `failed:could not reach CC idle` |
| 解析 session ID 失敗 | broadcast `failed:could not extract session ID` |
| CC 退出超時 (10s) | broadcast `failed:CC did not exit` |
| Relay 未回連 (15s) | broadcast `failed:relay did not connect within 15s` |
| Relay 關閉超時 (5s) | broadcast `failed:relay did not disconnect` |
| Shell 未恢復 (10s) | broadcast `failed:shell did not recover` |

所有失敗狀態都透過 `/ws/session-events` 的 `handoff` 事件推送，前端顯示錯誤並重置為可重試狀態。

## 不在此次範圍

- 分頁模式（下一期）
- JSONL 模式 handoff
- 多 preset 的 handoff to term（term 模式無 preset 概念）
- WebSocket 自動重連
