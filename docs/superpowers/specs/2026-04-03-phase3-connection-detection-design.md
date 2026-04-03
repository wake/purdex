# Phase 3: 連線偵測設計

> 2026-04-03 — 基於 Phase 1a+2 完成後的下一步，涵蓋 daemon 端基礎設施升級 + SPA 端連線管理

## 概述

Phase 3 建立完整的連線偵測機制，取代目前各 WS 各自獨立重連的零散做法。分兩個 PR：Daemon 端基礎設施 + SPA 端連線管理。

## 連線層級定義

```
SPA ←[HTTP/WS]→ Daemon ←[CLI]→ tmux server → sessions
     L1+L2              L3                L4
```

| Layer | 對象 | 偵測方式 | 重連策略 |
|-------|------|---------|---------|
| L1 | 主機可達性 | HTTP fetch timeout（3s） | 不間斷持續嘗試 |
| L2 | Daemon 可達性 | HTTP fetch 快速失敗 | 3 次後停止，等手動重連 |
| L3 | tmux server | Watcher broadcast + health check | Watcher 自動偵測 |
| L4 | Session 存在 | Tab 層級個別處理 | 不重連，顯示 session 已關閉 |

### L1/L2 HTTP 判定邏輯

一次 `fetch /api/health`（AbortController, 3s timeout）同時判定：

| 結果 | 判定 |
|------|------|
| HTTP 200 | connected |
| 快速失敗（TypeError/NetworkError） | refused（L2 — daemon 沒跑） |
| AbortError（3s timeout） | unreachable（L1 — 主機不可達） |

## PR 1: Daemon 端 — 連線偵測基礎設施

### 1a. Watcher 狀態機

現有 watcher 改為雙模式狀態機。

**啟動時**：立即執行一次 `TmuxAlive()` 設定初始 cached 狀態，確保 `/api/health` 在第一次 tick 前就有值。

**注意**：`ListSessions()` 在「tmux 沒跑」和「tmux 跑著但 0 個 session」兩種情況都回 `(nil, nil)`，無法區分。因此 `ListSessions()` 回 nil 時，需用 `TmuxAlive()` 做二次判定。

```
NORMAL（5s ticker）
  → ListSessions() 回傳 sessions → hash 比對 → broadcast sessions（如有變化）
  → ListSessions() 回傳 nil → TmuxAlive() 二次判定
     → alive → tmux 在跑但沒 session → 維持 NORMAL，broadcast 空 sessions
     → not alive → 切換 TMUX_DOWN

TMUX_DOWN（5s ticker）
  → broadcast { type: "tmux", value: "unavailable" }（狀態變化時發一次）
  → TmuxAlive() 失敗 → 維持 TMUX_DOWN，不重複 broadcast
  → TmuxAlive() 成功 → broadcast { type: "tmux", value: "ok" } → 切回 NORMAL
```

Watcher 內部維護 cached tmux 狀態（`bool`），供 `/api/health` 讀取。

**Goroutine A（tmux wait-for）配合**：現有 watcher 有兩個 goroutine — wait-for（即時推送）和 ticker（輪詢 fallback）。TMUX_DOWN 時 wait-for 會每秒失敗重試，產生無意義的 process spawn 和 log noise。修改：wait-for goroutine 在 TMUX_DOWN 時暫停，等 ticker 切回 NORMAL 後恢復。

### 1a-ext. Executor interface 新增 `TmuxAlive`

```go
// Executor interface 新增
TmuxAlive() bool  // 內部執行 tmux info，成功回 true
```

FakeExecutor 對應新增 `alive bool` 欄位 + `SetAlive(bool)` 方法。

### 1b. `/api/health` 擴充

回應格式：

```json
{"ok": true, "tmux": true}
```

- `tmux` 欄位讀 watcher cached 狀態，不自行執行 tmux 指令
- 維持 bypass auth（outer mux 註冊）

### 1c. WS ping/pong

只加在 host-events WS（原 session-events WS）：

| 參數 | 值 |
|------|---|
| ping 間隔 | 30s |
| pong timeout | 10s |

- Daemon 端 gorilla/websocket 設定 `SetWriteDeadline` + `WriteMessage(PingMessage)`
- Client 端瀏覽器自動回 pong（WebSocket 規範）
- pong 超時 → server 關閉連線 → client 觸發 onclose
- Terminal WS 和 stream WS 不加 ping/pong，依賴 host-events WS 偵測結果

### 1d. Rename: session-events → host-events

| 項目 | 舊 | 新 |
|------|---|---|
| WS endpoint | `/ws/session-events` | `/ws/host-events` |
| Go struct | `SessionEvent` | `HostEvent` |
| Go broadcaster | `EventsBroadcaster` | 不改（通用名稱） |
| 事件 type 新增 | — | `"tmux"` |

事件結構不變：

```go
type HostEvent struct {
    Type    string `json:"type"`    // sessions, relay, hook, handoff, tmux
    Session string `json:"session"` // 空字串表示 host 層級事件
    Value   string `json:"value"`
}
```

## PR 2: SPA 端 — useHostConnection + 閘控

### 2a. Rename 對應

| 項目 | 舊 | 新 |
|------|---|---|
| `connectSessionEvents()` | `session-events.ts` | `connectHostEvents()` in `host-events.ts` |
| `SessionEvent` interface | | `HostEvent` |
| `useMultiHostEventWs` | 內部引用更新 | 加入 `tmux` 事件處理 |

### 2b. useHostConnection

**觸發時機**：
1. host-events WS `onclose` 事件
2. host-events WS **首次連線失敗**（host 加入 hostOrder 後）

兩種情況都進入同一套 health check 流程，確保初始連線失敗也有完整的偵測與重連。

```typescript
useHostConnection(hostId)
  → fetch /api/health (3s timeout)
  → 判定 daemonState: 'connected' | 'refused' | 'unreachable'
  → 寫入 useHostStore.runtime[hostId]
```

#### 重連狀態機

```
觸發（WS onclose 或首次連線失敗）
  → runtime.status = 'reconnecting'
  → FAST_RETRY（立即連續 3 次 health check）
     → 任一成功 → CONNECTED（通知 WS 可重建）
     → 3 次都失敗 → 以最後一次結果判定 L1 or L2（最新網路狀態）

L1 unreachable:
  → runtime.status = 'disconnected'
  → UI 顯示斷線資訊 + 重連按鈕（純提示）
  → 背景不間斷持續嘗試（每次 attempt 3s timeout = 自然節拍）
  → 成功 → CONNECTED

L2 refused:
  → runtime.status = 'disconnected'
  → UI 顯示斷線資訊 + 重連按鈕
  → 停止自動重連
  → 手動重連按鈕觸發新一輪 FAST_RETRY
```

手動重連按鈕：立即觸發一次 health check attempt，不用等當前 timeout。

#### CONNECTED 恢復流程

```
health check 成功
  → runtime.status = 'connected'
  → 主動重建 host-events WS（不依賴 WS 自身 backoff）
  → host-events WS 重連後收到 initial snapshot
  → terminal WS 閘門放行，恢復重連
```

### 2c. WS 閘控

| WS | disconnected 時 | connected 恢復時 |
|----|-----------------|-----------------|
| host-events WS | **完全停止自身 reconnect**，交由 useHostConnection 管理 | useHostConnection 主動重建 |
| terminal WS | 暫停重連 | 閘門放行，恢復 backoff reconnect |
| stream WS | 不變（本來不重連） | 不變 |

**關鍵設計**：host-events WS `onclose` 後不再自行 backoff 重連。原因：它是觸發 useHostConnection 的來源，若自身 backoff 在 useHostConnection 判定前先連上，狀態機會混亂。重建由 useHostConnection 在確認 connected 後主動發起。

Terminal WS 閘控：reconnect 前檢查 `runtime.status`，若為 `disconnected` 或 `reconnecting` 則不發起連線，等待 useHostConnection 恢復後放行。

### 2d. HostRuntime 擴充

```typescript
interface HostRuntime {
  status: 'connected' | 'disconnected' | 'reconnecting'
  latency: number | null
  daemonState: 'connected' | 'refused' | 'unreachable'
  tmuxState: 'ok' | 'unavailable'
}
```

- `status`：供 UI 元件快速判斷（向下相容現有消費者）
- `daemonState`：精細分類，Phase 4 錯誤 UI 用
- `tmuxState`：平時由 WS broadcast 即時更新，斷線期間由 health check 補充
- `latency`：health check 成功時記錄 RTT

### 2e. tmux 事件處理

host-events WS 收到 `{ type: "tmux", value: "unavailable" }` 時：

```typescript
if (event.type === 'tmux') {
  useHostStore.getState().setRuntime(hostId, {
    tmuxState: event.value === 'ok' ? 'ok' : 'unavailable'
  })
}
```

## 關聯 Issues

| Issue | 解決程度 |
|-------|---------|
| #146 Host status 健康檢查 | 完整解決 |
| #147 Session 消失後 tab 無限重連 | 閘控機制解決 |
| #154 GetTmuxInstance timeout | watcher 狀態機涵蓋 |

#137（Electron 離線 crash）和其餘 Phase 4 相關 issue 留待 Phase 4 錯誤 UI 處理。

## 測試策略

### PR 1（Daemon）
- Watcher 狀態機：mock tmux executor，測試 NORMAL ↔ TMUX_DOWN 切換 + broadcast
- `/api/health`：測試 tmux true/false 回應
- WS ping/pong：測試 pong timeout 後連線關閉
- Rename：確認 endpoint path 變更

### PR 2（SPA）
- useHostConnection：mock fetch，測試 L1/L2 分類 + 重連狀態機
- 閘控：測試 disconnected 時 terminal WS 不重連
- tmux 事件：測試 HostRuntime.tmuxState 更新
