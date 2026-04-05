# Phase 5b：WS Ticket 統一 + Auth Error UI

> 前置：Phase 5a（PR #164, alpha.46）已完成配對系統 + Token 認證。
> 本 Phase 解決 WS 連線缺乏 ticket auth、auth error 無 UI 回饋、以及 health mode 未消費三個問題。

---

## 一、問題陳述

### 1.1 狀態機 + Auth 死循環

`ConnectionStateMachine` 用 `GET /api/health`（無 auth）判斷 daemon 狀態。Health endpoint 在 outer mux、不經 TokenAuth middleware，因此 **token 失效時 health 仍回 200**。

完整流程：

```
WS close → 狀態機 trigger → checkHealth(/api/health) → "connected"
→ reconnect() → fetchWsTicket 401 → 靜默失敗（autoReconnect=false）
→ 無人再觸發狀態機 → UI 顯示綠色 "connected" 但 WS 完全斷線
```

兩個場景：

- **新 host token 錯**：WS 從未建立 → onClose 從未觸發 → 狀態機從未啟動 → HostRuntime 永遠 undefined → 灰色圓圈，無錯誤提示
- **既有 host token 失效**：WS 斷線 → 狀態機 trigger → health ok → 設 "connected" → reconnect 失敗（ticket fetch 401）→ UI 綠色但 WS 死

### 1.2 Terminal / Stream WS 無 auth

| WS 端點 | 現狀 |
|---------|------|
| `/ws/host-events` | ✅ 有 ticket auth |
| `/ws/terminal/{code}` | ❌ 無 auth（直接 URL 連線） |
| `/ws/cli-bridge-sub/{code}` | ❌ 無 auth（直接 URL 連線） |

三個端點都在 TokenAuth middleware 後面，daemon 會拒絕無 auth 的 WS upgrade。但 SPA 端 `connectTerminal` 和 `connectStream` 沒有取 ticket 的機制。

### 1.3 Health mode 未消費（#167）

`/api/health` 回傳 `mode: 'pairing' | 'pending' | 'normal'`，但 SPA 無消費者。AddHostDialog 靠 checkbox 手動切換配對碼/Token 路線。

### 1.4 Legacy `?token=` 安全風險

Middleware 仍有 `?token=` query param fallback（L79-83），token 出現在 URL/log/瀏覽器歷史。Phase 5b ticket 統一後無消費者。

---

## 二、設計方案：Negotiation-First

### 核心概念

狀態機的 `checkFn` 升級為兩階段 **connection negotiation**，同時驗證 reachability + auth：

```
Phase 1: GET /api/health (no auth) → daemon 是否活著 + mode
Phase 2: POST /api/ws-ticket (Bearer token) → token 是否有效 + pre-fetch ticket
```

靈感來自 Slack Socket Mode 的 `connections.open` 模式：HTTP 端點負責所有 auth，回傳可直接使用的 WS 憑證。Auth 失敗在 HTTP 層就被偵測，不會進入 WS 層。

### 為什麼用 ws-ticket 作為 auth probe

- `POST /api/ws-ticket` 需要 Bearer token → 401 = token 錯誤
- 成功時回傳 ticket → 直接給 host-events WS 使用，零浪費
- 不需要新端點、不需要「借用」`/api/info` 驗 auth

---

## 三、狀態模型

### 3.1 HealthResult 擴充

```typescript
// host-connection.ts
interface HealthResult {
  daemon: 'connected' | 'refused' | 'unreachable' | 'auth-error'
  tmux: 'ok' | 'unavailable'
  latency: number | null
  mode?: 'pairing' | 'pending' | 'normal'
  ticket?: string  // Phase 2 成功時附帶
}
```

### 3.2 checkHealth 兩階段

```typescript
async function checkHealth(
  baseUrl: string,
  getToken?: () => string | undefined,
): Promise<HealthResult> {
  // Phase 1: health (no auth, 6s timeout)
  const controller = new AbortController()
  const timer = setTimeout(() => controller.abort(), 6000)
  try {
    const start = performance.now()
    const res = await fetch(`${baseUrl}/api/health`, { signal: controller.signal })
    const latency = Math.round(performance.now() - start)
    const body = await res.json()
    const mode = body.mode as 'pairing' | 'pending' | 'normal' | undefined

    // Phase 2: auth probe (only if token available)
    const token = getToken?.()
    if (!token) {
      return { daemon: 'connected', tmux: 'unavailable', latency, mode }
    }
    const ticketRes = await fetch(`${baseUrl}/api/ws-ticket`, {
      method: 'POST',
      headers: { Authorization: `Bearer ${token}` },
    })
    if (ticketRes.status === 401) {
      return { daemon: 'auth-error', tmux: 'unavailable', latency, mode }
    }
    if (!ticketRes.ok) {
      // ws-ticket endpoint 異常但非 auth → 視為 connected
      return { daemon: 'connected', tmux: 'unavailable', latency, mode }
    }
    const { ticket } = await ticketRes.json()
    return { daemon: 'connected', tmux: 'unavailable', latency, mode, ticket }
  } catch (err: unknown) {
    if (err instanceof Error && err.name === 'AbortError') {
      return { daemon: 'unreachable', tmux: 'unavailable', latency: null }
    }
    return { daemon: 'refused', tmux: 'unavailable', latency: null }
  } finally {
    clearTimeout(timer)
  }
}
```

### 3.3 狀態機新增 auth-error 分支

```typescript
// connection-state-machine.ts — trigger() 分類邏輯

if (lastResult.daemon === 'connected') return    // recovered
if (lastResult.daemon === 'auth-error') return   // 永久錯誤，不背景重試
if (lastResult.daemon === 'unreachable') {       // L1: 不間斷重連
  this.backgroundDeadline = null
  this.startBackground(L1_RETRY_DELAY_MS)
}
if (lastResult.daemon === 'refused') {           // L2: 3s 間隔，3 分鐘停止
  this.backgroundDeadline = Date.now() + L2_RETRY_TIMEOUT_MS
  this.startBackground(L2_RETRY_DELAY_MS)
}
```

### 3.4 HostRuntime 擴充

```typescript
interface HostRuntime {
  status: 'connected' | 'disconnected' | 'reconnecting' | 'auth-error'
  latency?: number
  info?: HostInfo
  daemonState?: 'connected' | 'refused' | 'unreachable' | 'auth-error'
  tmuxState?: 'ok' | 'unavailable'
  manualRetry?: () => void
}
```

### 3.5 狀態流轉

```
                    ┌──────────────┐
                    │   (initial)  │
                    └──────┬───────┘
                           │ effect 啟動
                           ▼
                    ┌──────────────┐
           ┌───────│  negotiating  │◄──── 手動重試 / WS close
           │       └──────┬───────┘
           │              │ Phase 1 + Phase 2
           │              ▼
     ┌─────┴─────┬────────┴────────┬──────────────┐
     ▼           ▼                 ▼              ▼
 connected   unreachable       refused       auth-error
 (綠色)      (L1 重試)        (L2 重試)      (鎖頭,不重試)
     │           │                │
     │      背景重連成功       背景重連成功
     │           │                │
     ▼           └───►connected◄──┘
  WS 連線
```

---

## 四、WS Ticket 統一

### 4.1 三條 WS 的 ticket 來源

| WS 端點 | 管理者 | Ticket 來源 |
|---------|--------|------------|
| `/ws/host-events` | `useMultiHostEventWs` | 狀態機 negotiation 的 pre-fetched ticket；後續重連由狀態機再次 negotiate |
| `/ws/terminal/{code}` | `useTerminalWs` | 連線前呼叫 `fetchWsTicket(hostId)` |
| `/ws/cli-bridge-sub/{code}` | `useRelayWsManager` | 連線前呼叫 `fetchWsTicket(hostId)` |

### 4.2 host-events：pre-fetched ticket

狀態機 Phase 2 成功時已拿到 ticket。`useMultiHostEventWs` 的 `onStateChange` callback 將 ticket 傳給 `connectHostEvents`：

```typescript
if (result.daemon === 'connected' && connRef.current) {
  connRef.current.reconnectWithTicket(result.ticket)
}
```

`connectHostEvents` 介面調整：接受 `ticket?: string` 直接使用（跳過 `getTicket` callback）。若 ticket 已過期（WS upgrade 失敗），觸發狀態機重新 negotiate。

### 4.3 connectTerminal 加 getTicket

```typescript
// ws.ts
export function connectTerminal(
  url: string,
  onData: (data: ArrayBuffer) => void,
  onClose: () => void,
  onOpen?: () => void,
  canReconnect?: () => boolean,
  getTicket?: () => Promise<string>,  // 新增
): TerminalConnection
```

連線流程：

```
connect() {
  if (getTicket) {
    try { ticket = await getTicket() → append ?ticket= to URL }
    catch { onClose() → 通知上層 → 觸發狀態機; return }
  }
  ws = new WebSocket(wsUrl)
  ...
}
```

`useTerminalWs` 和 `SessionPaneContent` 傳入 `() => fetchWsTicket(hostId)`。

### 4.4 connectStream 加 getTicket

```typescript
// stream-ws.ts
export function connectStream(
  url: string,
  onMessage: (msg: StreamMessage) => void,
  onClose: () => void,
  onOpen?: () => void,
  getTicket?: () => Promise<string>,  // 新增
): StreamConnection
```

`useRelayWsManager` 在建立 stream WS 前取 ticket：

```typescript
const conn = connectStream(
  `${wsBase}/ws/cli-bridge-sub/${encodeURIComponent(sessionCode)}`,
  onMessage, onClose, onOpen,
  () => fetchWsTicket(hostId),
)
```

### 4.5 Ticket fetch 401 處理

Terminal / Stream 的 `getTicket` 失敗時呼叫 `onClose()`，通知上層。`useMultiHostEventWs` 的 WS close handler 觸發狀態機 → 狀態機重跑 negotiation → Phase 2 偵測 401 → 設 `auth-error`。

**狀態機是唯一的 auth 狀態擁有者**。Terminal / Stream 只回報失敗，不自行判斷 auth error。

---

## 五、Auth Error UI

### 5.1 HostSidebar 狀態圖示

| 狀態 | 圖示 | 顏色 |
|------|------|------|
| connected | 實心圓 | green-400 |
| connected + tmux ✗ | Warning | yellow-400 |
| reconnecting | Spinner | yellow-400 animate-spin |
| auth-error | Lock（鎖頭） | red-400 animate-pulse |
| disconnected | 實心圓 | red-400 |
| (no runtime) | 實心圓 | text-muted |

auth-error 用鎖頭圖示（Phosphor `LockSimple`）而非紅色圓圈，視覺上與 disconnected 區分。Host 名稱文字也改為 red-400。

### 5.2 StatusBar

```
auth-error → 紅色鎖頭 + "Token 無效" + "— 點擊設定 Token"
```

不顯示「重新連線中」等暫時性語言。可點擊引導到 OverviewSection。

### 5.3 OverviewSection

auth-error 時顯示：

1. **Banner**（紅色背景）：鎖頭圖示 + "Token 無效" + "請確認 Token 與 daemon 設定一致，修改後自動重新連線"
2. **TokenField**：label 標紅 + 外框紅色 border + 錯誤提示文字
3. **Status 欄位**：鎖頭 + "auth-error"

### 5.4 Token 修改 → 自動重試

```
使用者修改 token → updateHost(hostId, { token: newToken })
  → onSave callback 呼叫 manualRetry()
  → 狀態機重新 negotiate（Phase 1 + Phase 2）
  → 成功 → 'connected'
  → 仍失敗 → 'auth-error'
```

OverviewSection 的 TokenField `onSave` 後自動觸發 `manualRetry()`，使用者不需額外操作。

### 5.5 connectionErrorMessage 擴充

```typescript
// host-utils.ts
export function connectionErrorMessage(runtime, t): string | null {
  if (runtime?.status === 'auth-error') return t('hosts.error_auth')
  if (runtime?.daemonState === 'unreachable') return t('hosts.error_unreachable')
  if (runtime?.daemonState === 'refused') return t('hosts.error_refused')
  if (runtime?.status === 'connected' && runtime?.tmuxState === 'unavailable')
    return t('hosts.error_tmux_down')
  return null
}
```

### 5.6 i18n keys 新增

```
hosts.error_auth        → "Token 無效，請確認 Token 與 daemon 設定一致"
hosts.auth_error        → "Token 無效"
hosts.auth_error_hint   → "請確認 Token 與 daemon 設定一致，修改後自動重新連線"
status.auth_error       → "Token 無效，點擊設定"
```

---

## 六、Health Mode 消費（#167）

### 6.1 checkHealth 回傳 mode

Phase 1 health check 解析 response body 的 `mode` 欄位，存入 `HealthResult.mode`。

### 6.2 AddHostDialog 自動導流

AddHostDialog 開啟時，若使用者已輸入 IP:port，可選擇先打 `GET /api/health`：

| health mode | 行為 |
|-------------|------|
| `pairing` | 自動進入配對碼路線 + badge "daemon 等待配對中" |
| `pending` | 自動進入 Token 路線 + 提示「請輸入 daemon 終端機顯示的 Token」 |
| `normal` | Token 路線 + 提示「Daemon 運作中，請輸入已設定的 Token」 |
| health 失敗 | 兩條路線都開放 |

此為 UX 優化，不影響核心流程。使用者仍可手動切換。

---

## 七、Legacy 清理

### 7.1 移除 `?token=` query param

`internal/middleware/middleware.go` L79-83 的 `?token=` fallback 移除。Phase 5b 後所有 WS 連線統一用 `?ticket=`，HTTP API 用 Bearer header。

---

## 八、影響範圍

### 8.1 Daemon（Go）— 極小

| 項目 | 改動 |
|------|------|
| middleware.go | 刪除 `?token=` fallback（~5 行） |
| 其他 | 無 — 不需新端點、不改 WS handler |

### 8.2 SPA — 中等

**核心架構**：

| 檔案 | 改動 |
|------|------|
| `host-connection.ts` | `checkHealth` 兩階段 + `HealthResult` 擴充 |
| `connection-state-machine.ts` | auth-error 分支（不進入 L1/L2） |
| `useHostStore.ts` | `HostRuntime.status` 加 `'auth-error'` |

**WS Ticket 統一**：

| 檔案 | 改動 |
|------|------|
| `ws.ts` | `connectTerminal` 加 `getTicket` 參數 |
| `stream-ws.ts` | `connectStream` 加 `getTicket` 參數 |
| `host-events.ts` | `connectHostEvents` 支援 `reconnectWithTicket` |
| `useTerminalWs.ts` | 傳入 `getTicket` |
| `SessionPaneContent.tsx` | 提供 `getTicket` callback |
| `useRelayWsManager.ts` | stream WS 前 fetch ticket |
| `useMultiHostEventWs.ts` | 傳遞 pre-fetched ticket + auth-error 處理 |

**UI**：

| 檔案 | 改動 |
|------|------|
| `HostSidebar.tsx` | auth-error 圖示（LockSimple） |
| `StatusBar.tsx` | auth-error 顯示 |
| `OverviewSection.tsx` | auth-error banner + TokenField 標紅 + 自動 manualRetry |
| `host-utils.ts` | `connectionErrorMessage` 加 auth-error |
| `AddHostDialog.tsx` | health mode 自動導流 |
| i18n JSON | 新增 keys |

### 8.3 不受影響

- Daemon 所有 API 端點（行為不變）
- Daemon WS handler（已支援 ticket）
- Host 資料持久化（HostConfig 型別不變）
- Tab / Session / Stream / Agent store
- 已建立的 WS 連線（auth 只在 upgrade 時驗）

### 8.4 風險

| 風險 | 緩解 |
|------|------|
| Phase 2 非 401 失敗（network error） | 只有 HTTP 401 判定 auth-error，其他回退用 Phase 1 結果 |
| 重連延遲（多一次 HTTP） | 僅在重連路徑，本地網路 < 10ms |
| Pre-fetched ticket 過期 | TTL 30 秒，negotiate → WS 連線通常 < 1 秒 |

---

## 九、測試範圍

| 層面 | 測試項目 |
|------|---------|
| `checkHealth` | Phase 1 only（無 token）/ Phase 2 成功 / Phase 2 返回 401 / Phase 2 network error 回退 |
| `ConnectionStateMachine` | auth-error 不進入 L1/L2 / 手動重試可從 auth-error 恢復到 connected |
| `connectTerminal` | getTicket 成功帶 ticket / getTicket 失敗觸發 onClose |
| `connectStream` | getTicket 成功帶 ticket / getTicket 失敗觸發 onClose |
| `connectionErrorMessage` | auth-error 回傳正確 i18n key |
| `host-utils` | 新狀態的 error message |
| Daemon middleware | 移除 `?token=` 後既有 test 通過 |

---

## 十、關聯 Issues

| Issue | 解決方式 |
|-------|---------|
| #148 pt.2 — Terminal/Stream WS 無 Bearer | 統一用 ticket |
| #148 pt.3 — WS 401 無 auth error 提示 | 狀態機偵測 + UI 顯示 |
| #167 — health mode SPA 未消費 | 狀態機 Phase 1 + AddHostDialog 導流 |
| 死循環 — 狀態機誤判 connected | 兩階段 negotiation |
| 靜默失敗 — 新 host token 錯無回饋 | 狀態機首次 negotiation 偵測 |
| `?token=` 安全風險 | 移除 legacy fallback |

不涵蓋（獨立處理）：#165 測試覆蓋率、#166 程式碼清理、#159 ghost reconnect、#160 WriteControl。
