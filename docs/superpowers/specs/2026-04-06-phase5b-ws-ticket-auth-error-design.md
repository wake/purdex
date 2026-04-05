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
  mode: 'pairing' | 'pending' | 'normal'  // daemon 永遠回傳，非 optional
  ticket?: string  // Phase 2 成功時附帶
}
```

`mode` 為非 optional。`checkHealth` 解析時使用 `body.mode ?? 'normal'` fallback 保護。

### 3.2 checkHealth 兩階段

**重要**：`getToken` 必須是**動態 closure**，每次呼叫時從 store 讀取最新值。
若寫成靜態快照，使用者修改 token 後狀態機仍用舊 token，Phase 2 永遠 401。

呼叫端範例：
```typescript
() => checkHealth(baseUrl, () => useHostStore.getState().hosts[hostId]?.token)
```

完整實作：

```typescript
const PHASE1_TIMEOUT_MS = 6000
const PHASE2_TIMEOUT_MS = 5000

async function checkHealth(
  baseUrl: string,
  getToken?: () => string | undefined,
): Promise<HealthResult> {
  // Phase 1: health (no auth, 6s timeout)
  const ctrl1 = new AbortController()
  const timer1 = setTimeout(() => ctrl1.abort(), PHASE1_TIMEOUT_MS)
  try {
    const start = performance.now()
    const res = await fetch(`${baseUrl}/api/health`, { signal: ctrl1.signal })
    const latency = Math.round(performance.now() - start)
    const body = await res.json()
    const mode = (body.mode ?? 'normal') as 'pairing' | 'pending' | 'normal'

    // Phase 2: auth probe (獨立 timeout)
    const token = getToken?.()
    if (!token) {
      // SPA 無 token → 視為 auth-error（daemon 可能有 token，WS 會被拒）
      // 除非 mode=pairing（daemon 尚未設定 token，不需要 auth）
      if (mode === 'pairing') {
        return { daemon: 'connected', tmux: 'unavailable', latency, mode }
      }
      return { daemon: 'auth-error', tmux: 'unavailable', latency, mode }
    }

    const ctrl2 = new AbortController()
    const timer2 = setTimeout(() => ctrl2.abort(), PHASE2_TIMEOUT_MS)
    try {
      const ticketRes = await fetch(`${baseUrl}/api/ws-ticket`, {
        method: 'POST',
        headers: { Authorization: `Bearer ${token}` },
        signal: ctrl2.signal,
      })
      if (ticketRes.status === 401) {
        return { daemon: 'auth-error', tmux: 'unavailable', latency, mode }
      }
      if (ticketRes.status === 503) {
        // PairingGuard 攔截 — daemon 處於 pairing mode，不重試
        return { daemon: 'auth-error', tmux: 'unavailable', latency, mode }
      }
      if (!ticketRes.ok) {
        // ws-ticket endpoint 異常但非 auth/pairing → 視為 connected
        return { daemon: 'connected', tmux: 'unavailable', latency, mode }
      }
      const { ticket } = await ticketRes.json()
      return { daemon: 'connected', tmux: 'unavailable', latency, mode, ticket }
    } catch {
      // Phase 2 timeout 或 network error → 回退用 Phase 1 結果
      return { daemon: 'connected', tmux: 'unavailable', latency, mode }
    } finally {
      clearTimeout(timer2)
    }
  } catch (err: unknown) {
    if (err instanceof Error && err.name === 'AbortError') {
      return { daemon: 'unreachable', tmux: 'unavailable', latency: null }
    }
    return { daemon: 'refused', tmux: 'unavailable', latency: null }
  } finally {
    clearTimeout(timer1)
  }
}
```

#### 設計決策說明

- **無 token 時回傳 `auth-error`**（非 `connected`）：SPA 無 token 但 daemon 有 token 時，WS 會被 daemon 401 拒絕。只有 `mode=pairing` 例外（daemon 尚未設定 token）。
- **503 → `auth-error`**：PairingGuard 回 503 代表 daemon 在 pairing mode，此時 token 認證不可用。UI 顯示與 401 相同（引導使用者設定）。
- **Phase 2 獨立 timeout（5 秒）**：Phase 1 成功後 daemon 可能在 Phase 2 前掉線。獨立 AbortController 防止掛起。
- **Phase 2 的非 401/503 錯誤回退**：ws-ticket endpoint 本身故障（500 等）不是 auth 問題，回退用 Phase 1 結果。

### 3.3 狀態機新增 auth-error 分支

FAST_RETRY 迴圈中，`auth-error` 在**第一次 checkFn 回傳時就跳出**（不跑滿 3 次），因為重試也不會改變結果（token 不會自己變對），且每次 Phase 2 成功會消耗一個 ticket。

```typescript
// connection-state-machine.ts — trigger() 的 FAST_RETRY 迴圈

for (let i = 0; i < FAST_RETRY_COUNT; i++) {
  if (this.stopped || this.epoch !== myEpoch) return
  lastResult = await this.checkFn()
  if (this.stopped || this.epoch !== myEpoch) return
  this.onStateChange(lastResult)

  if (lastResult.daemon === 'connected') return    // recovered
  if (lastResult.daemon === 'auth-error') return   // 永久錯誤，立即跳出
}

// FAST_RETRY 結束後的分類（只有 unreachable / refused 會走到這）
if (lastResult.daemon === 'unreachable') {       // L1: 不間斷重連
  this.backgroundDeadline = null
  this.startBackground(L1_RETRY_DELAY_MS)
} else if (lastResult.daemon === 'refused') {    // L2: 3s 間隔，3 分鐘停止
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
                           │ effect 啟動 → 立即 sm.trigger()
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

**關鍵設計**：

1. `connectHostEvents` 以 `lazy: true` 建立 — **不立即呼叫 `connect()`**，等待 SM 指令
2. Effect 建立後立即 `sm.trigger()` — SM 先跑完 negotiation
3. SM 完成後透過 `onStateChange` → `reconnectWithTicket(ticket)` 啟動第一次 WS 連線

這避免了 SM 與 `connectHostEvents` 初始 `connect()` 並行的 race condition（兩個 `connect()` 同時跑會建立兩個 WebSocket）。`connectHostEvents` 只有在 SM 明確說 connected 後才建立連線。

### 3.6 HostRuntime 狀態映射規則

狀態機 `onStateChange(result)` 的映射：

```typescript
const statusMap: Record<HealthResult['daemon'], HostRuntime['status']> = {
  'connected': 'connected',
  'unreachable': 'disconnected',
  'refused': 'disconnected',
  'auth-error': 'auth-error',
}

setRuntime(hostId, {
  status: statusMap[result.daemon],
  daemonState: result.daemon,
  latency: result.latency ?? undefined,
})
```

`status` 和 `daemonState` 都會反映 `auth-error`。

### 3.7 所有 HostRuntime.status 消費者

以下元件消費 `runtime.status`，必須處理新的 `'auth-error'` 值：

| 元件 | 檔案 | 現有邏輯 | Phase 5b 調整 |
|------|------|---------|--------------|
| StatusIcon | `HostSidebar.tsx` L21-28 | switch on status | 加 `auth-error` → Lock 圖示 |
| StatusBar | `StatusBar.tsx` L139-148 | status className | 加 `auth-error` → 紅色 + 鎖頭文字 |
| OverviewSection | `OverviewSection.tsx` L502-506 | status className | 加 `auth-error` → 紅色 + banner |
| SessionPickerList | `SessionPickerList.tsx` L23 | `=== 'connected'` | auth-error ≠ connected → 不顯示 session，行為正確 |
| SessionPanel | `SessionPanel.tsx` L54 | `!== 'connected'` | auth-error 視為 offline → 行為正確，但可加提示 |
| SortableTab | `SortableTab.tsx` L106 | `!== 'connected'` | 同上 |
| HooksSection | `HooksSection.tsx` L31 | `!== 'connected'` | 同上 |
| UploadSection | `UploadSection.tsx` L35 | `!== 'connected'` | 同上 |
| useTerminalWs | `useTerminalWs.ts` L50-54 | canReconnect gate | auth-error 時 `status !== 'connected'` → 不重連，正確 |

| SessionSection | `SessionSection.tsx` L27,34,36 | `status === 'reconnecting'` / else | auth-error 落入 else → 紅色圓圈，行為同 disconnected，可接受 |
| SessionPanel | `SessionPanel.tsx` L54,61-68 | `!== 'connected'` / `=== 'reconnecting'` | 同上 |

前三個（HostSidebar、StatusBar、OverviewSection）需主動加 `auth-error` 分支。其餘八個的 `!== 'connected'` 邏輯自然排除 auth-error，行為正確無需改動。

### 3.8 Terminal WS 在 auth-error 時的行為

auth-error 只在 WS upgrade 時偵測。**已建立的 WS 連線不受影響**（WebSocket 協議在 upgrade 後不再驗 auth）。

場景：使用者正在使用 terminal，daemon 端 token 變更：
- Terminal WS **仍然連通**，使用者可繼續操作（預期行為）
- Host-events WS 若因其他原因斷線 → SM 偵測 auth-error → status = 'auth-error'
- Terminal 下次斷線時，`canReconnect()` 回傳 false → 暫停重連
- 使用者修改 token → status 回到 connected → terminal 下次可重連

此為 WebSocket 協議的本質限制，不強制中斷已建立的連線。

---

## 四、WS Ticket 統一

### 4.1 三條 WS 的 ticket 來源

| WS 端點 | 管理者 | Ticket 來源 |
|---------|--------|------------|
| `/ws/host-events` | `useMultiHostEventWs` | 狀態機 negotiation 的 pre-fetched ticket；後續重連由狀態機再次 negotiate |
| `/ws/terminal/{code}` | `useTerminalWs` | 連線前呼叫 `fetchWsTicket(hostId)` |
| `/ws/cli-bridge-sub/{code}` | `useRelayWsManager` | 連線前呼叫 `fetchWsTicket(hostId)` |

### 4.2 host-events：pre-fetched ticket

狀態機 Phase 2 成功時已拿到 ticket。`useMultiHostEventWs` 的 `onStateChange` 透過 `pendingTicket` 機制傳給 `connectHostEvents`。

#### EventConnection 介面擴充

```typescript
export interface EventConnection {
  close: () => void
  reconnect: () => void
  reconnectWithTicket: (ticket?: string) => void  // 新增
}
```

#### connectHostEvents 內部實作

```typescript
export function connectHostEvents(
  url: string,
  onEvent: ...,
  onClose?: ...,
  onOpen?: ...,
  getTicket?: () => Promise<string>,
  autoReconnect = true,
  lazy = false,            // 新增：true 時不立即連線
): EventConnection {
  let ws: WebSocket
  let retryMs = 1000
  let closed = false
  let connecting = false   // 防止並發 connect()
  let pendingTicket: string | undefined

  async function connect() {
    if (connecting) return  // 防止雙重 connect
    connecting = true
    try {
      let wsUrl = url
      // 優先消費 pendingTicket（由 reconnectWithTicket 注入）
      // 若無，回退到 getTicket callback
      const ticket = pendingTicket ?? (getTicket ? await getTicket().catch(() => null) : null)
      pendingTicket = undefined

      if (ticket) {
        const u = new URL(wsUrl)
        u.searchParams.set('ticket', ticket)
        wsUrl = u.toString()
      } else if (getTicket) {
        // getTicket 失敗且無 pendingTicket → 通知上層
        if (!closed) onClose?.()
        return
      }
      ws = new WebSocket(wsUrl)
      // ... 其餘不變
    } finally {
      connecting = false
    }
  }

  if (!lazy) connect()     // lazy=true 時等 reconnectWithTicket 觸發
  return {
    close: () => { closed = true; ws?.close() },
    reconnect: () => { if (!closed) { retryMs = 1000; ws?.close(); connect() } },
    reconnectWithTicket: (ticket) => {
      if (!closed) {
        pendingTicket = ticket
        retryMs = 1000
        ws?.close()
        connect()
      }
    },
  }
}
```

**`connecting` flag**：防止 `reconnectWithTicket` 觸發的 `connect()` 與前一個仍在飛行的 `connect()` 並行。若前者仍在 `await getTicket()` 階段，後者直接 return。前者完成後，透過 ws.onclose → onClose → SM trigger → 下一輪 reconnectWithTicket 再啟動。

#### useMultiHostEventWs 呼叫方式

```typescript
// SM onStateChange callback:
if (result.daemon === 'connected' && connRef.current) {
  if (result.ticket) {
    connRef.current.reconnectWithTicket(result.ticket)
  } else {
    // Phase 2 timeout 等情況 — ticket 不存在
    // 用普通 reconnect，讓 getTicket callback 自行取 ticket
    connRef.current.reconnect()
  }
}
```

**重要**：`reconnectWithTicket(undefined)` 會清空 `pendingTicket`，導致 `connect()` 呼叫 `getTicket()`，若 getTicket 也 timeout → `onClose` → SM trigger → Phase 2 再次 timeout → 無限循環。必須只在 `result.ticket` 有值時才用 `reconnectWithTicket`。

#### useMultiHostEventWs 初始化流程

```typescript
// 1. 建立 connectHostEvents（lazy mode，不立即連線）
const conn = connectHostEvents(
  wsUrl, onEvent, onClose, onOpen,
  () => fetchWsTicket(hostId),
  false,  // autoReconnect = false（SM 管重連）
  true,   // lazy = true（等 SM 指令）
)

// 2. 立即觸發 SM negotiation
sm.trigger()

// SM 完成後 onStateChange 自動呼叫 reconnectWithTicket 或 reconnect
```

**一次性消費語意**：`pendingTicket` 在 `connect()` 內消費後清空。若 WS 在使用 pre-fetched ticket 後斷線再重連，`pendingTicket` 已空，回退到 `getTicket()` callback（觸發新的 `fetchWsTicket`）。若 `getTicket` 也失敗，通知 `onClose` → 狀態機重新 negotiate。

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

```typescript
async function connect() {
  let wsUrl = url
  if (getTicket) {
    try {
      const ticket = await getTicket()
      const u = new URL(wsUrl)
      u.searchParams.set('ticket', ticket)
      wsUrl = u.toString()
    } catch {
      // getTicket 失敗 → 沿用現有 backoff 自行重試，不呼叫 onClose
      // （避免與 ws.onclose 的 retry timer 雙重觸發）
      setTimeout(() => {
        if (closed) return
        if (canReconnect && !canReconnect()) return
        connect()
      }, retryMs)
      retryMs = Math.min(retryMs * 2, 30000)
      return
    }
  }
  ws = new WebSocket(wsUrl)
  // ... 其餘不變
}
```

**設計決策**：`getTicket` 失敗時不呼叫 `onClose()`，而是沿用 `connectTerminal` 自身的 backoff 重試。原因：
- Terminal 的 `onClose` → `TerminalView.setDisconnected(true)`，只更新 React 元件 state，不觸發狀態機
- 如果同時呼叫 `onClose` + 排程 retry timer，會產生雙重觸發
- Terminal WS 的 backoff 最終會因 `canReconnect()` gate 停止（當狀態機偵測到 auth-error，host status ≠ connected → canReconnect 回傳 false）

**狀態機觸發路徑**（間接）：
1. Terminal getTicket 401 → 自行 backoff 重試
2. 同時，host-events WS 也在嘗試連線 → getTicket 也失敗 → 觸發 `onClose` → 狀態機 trigger
3. 狀態機 negotiate → Phase 2 401 → `auth-error` → `canReconnect()` 回傳 false → terminal 停止重試

`useTerminalWs` 和 `SessionPaneContent` 傳入 `() => fetchWsTicket(hostId)`。

### 4.4 connectStream：外層 await ticket

`connectStream` 保持同步介面不變（內部直接 `new WebSocket(url)`）。Ticket 取得在 `useRelayWsManager` 外層完成：

```typescript
// useRelayWsManager.ts — relay connected 時
if (connected && !wasConnected) {
  const wsBase = useHostStore.getState().getWsBase(hostId)

  // 外層 await ticket，再帶入 URL
  fetchWsTicket(hostId).then((ticket) => {
    // Guard: relay 可能在 ticket fetch 期間已斷線
    if (!useStreamStore.getState().relayStatus[ck]) return

    const url = new URL(`${wsBase}/ws/cli-bridge-sub/${encodeURIComponent(sessionCode)}`)
    url.searchParams.set('ticket', ticket)

    const conn = connectStream(
      url.toString(),
      onMessage, onClose, onOpen,
    )
    useStreamStore.getState().setConn(hostId, sessionCode, conn)
    activeConns.set(ck, conn)
  }).catch((err) => {
    // 區分 ticket fetch 失敗（預期）與程式錯誤（非預期）
    if (err instanceof Error && err.message.includes('ws-ticket')) {
      // ticket fetch 失敗 — 不建立 WS
      // 狀態機會從 host-events 路徑偵測 auth-error
    } else {
      console.error('[useRelayWsManager] stream WS setup error', err)
    }
  })
}
```

**設計決策**：不改 `connectStream` 簽名。原因：
- `connectStream` 是同步函式，內部直接 `new WebSocket(url)`，塞 async getTicket 需要重構整個函式
- Stream WS 的生命週期由 relay event 驅動（relay connected → 建立 WS），與 terminal 的長連線模式不同
- 在外層 await 更清晰：ticket 取得是 `useRelayWsManager` 的職責，不應耦合到通用的 `connectStream`

**Race condition 防護**：如果 ticket fetch 還在進行中，relay 斷開了（`!connected && wasConnected`），cleanup 邏輯會執行 `existing?.close()`。但此時 conn 尚未建立（`then` 未執行），`activeConns.get(ck)` 為 undefined，安全。Ticket fetch 完成後的 `then` callback 會建立一個新的 conn，但此時 `relayStatus[ck]` 已為 false，下一輪 subscribe 會清理它。

### 4.5 Ticket fetch 401 處理

**狀態機是唯一的 auth 狀態擁有者**。Terminal / Stream 不自行判斷 auth error。

觸發路徑統一為：

```
任何 WS 的 ticket fetch 401
  → host-events WS 也在 401（同一 host 的所有 WS 共用 token）
  → host-events onClose 觸發狀態機（或 effect 初始 trigger）
  → 狀態機 negotiate Phase 2 → 偵測 401 → 設 auth-error
  → HostRuntime.status = 'auth-error'
  → Terminal canReconnect() 回傳 false → 停止重試
  → Stream 不建立新連線（ticket fetch 失敗直接 catch）
```

Terminal 和 Stream 不需要自己觸發狀態機，因為同一 host 的 host-events WS **一定也會遇到相同的 auth 問題**，而 host-events 的失敗路徑已與狀態機連結。

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
auth-error → 紅色鎖頭 + "Token 無效" + "— 點擊設定 Token"（可點擊）
```

不顯示「重新連線中」等暫時性語言。

**導航機制**：StatusBar 新增 `onNavigateToHost?: (hostId: string) => void` prop，由外層 App/Layout 注入。點擊 auth-error 文字觸發 `onNavigateToHost(hostId)` → 切換到 HostPage → 選中該 host 的 OverviewSection。此 prop 模式與現有 `onViewModeChange` 一致。

### 5.3 OverviewSection

auth-error 時顯示：

1. **Banner**（紅色背景）：鎖頭圖示 + "Token 無效" + "請確認 Token 與 daemon 設定一致，修改後自動重新連線"
2. **TokenField**：label 標紅 + 外框紅色 border + 錯誤提示文字
3. **Status 欄位**：鎖頭 + "auth-error"

### 5.4 Token 修改 → 自動重試

```
使���者修改 token → updateHost(hostId, { token: newToken })
  → 先 setRuntime(hostId, { status: 'reconnecting' })  // 避免 auth-error 閃爍
  → 再呼叫 manualRetry()
  → 狀態機��新 negotiate（Phase 1 + Phase 2）
  → 成功 → 'connected'
  → 仍失敗 → 'auth-error'
```

OverviewSection 的 TokenField `onSave` 先設 `reconnecting`（讓 UI 立即從紅色鎖頭切換為黃色旋轉），再觸發 `manualRetry()`。避免舊的 auth-error 在新的 negotiate 完成前短暫閃現。

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

**僅在 manual route（勾選 Token）時觸發**。Pairing route 不需要 health check — IP 從配對碼解碼而來，daemon 一定在 pairing mode。

**觸發時機**：使用者在 IP + port 欄位都有值後，debounced（300ms）自動打 `GET http://{ip}:{port}/api/health`。欄位變更時重新觸發。

| health mode | 行為 |
|-------------|------|
| `pairing` | 自動切換到配對碼路線（取消 Token 勾選）+ badge "daemon 等待配對中" |
| `pending` | 維持 Token 路線 + 提示「請輸入 daemon 終端機顯示的 Token」 |
| `normal` | 維持 Token 路線 + 提示「Daemon 運作中，請輸入已設定的 Token」 |
| health 失敗 | 無提示，兩條路線都開放（使用者手動選） |

**Auto-switch 保護**：只在 `stage === 'manual'` 且 token 欄位尚未填入時才自動切換。若使用者已填入 token（`token.length > 0`），不觸發自動切換，避免打斷已完成的配置。使用者仍可手動覆蓋。

此為 UX 優化，不影響核心認證流程。

---

## 七、Legacy 清理

### 7.1 移除 `?token=` query param

`internal/middleware/middleware.go` L79-83 的 `?token=` fallback 移除。Phase 5b 後所有 WS 連線統一用 `?ticket=`，HTTP API 用 Bearer header。

**Go 測試同步修改**：`internal/middleware/middleware_test.go` 的 `TestTokenAuthQueryParam` 目前驗證 `?token=secret` 回傳 200。移除 `?token=` 後此 test 必紅。改為驗證 `?token=` 回傳 **401**（確認 fallback 已移除）。

---

## 八、影響範圍

### 8.1 Daemon（Go��— 極小

| 項目 | 改動 |
|------|------|
| middleware.go | 刪除 `?token=` fallback（~5 行） |
| middleware_test.go | `TestTokenAuthQueryParam` 改為驗證 401 |
| 其他 | 無 — 不需���端點、不改 WS handler |

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
| `StatusBar.tsx` | auth-error 顯示 + `onNavigateToHost` prop |
| `OverviewSection.tsx` | auth-error banner + TokenField 標紅 + 儲存後 setRuntime reconnecting + manualRetry |
| `host-utils.ts` | `connectionErrorMessage` 加 auth-error |
| `AddHostDialog.tsx` | health mode debounced 自動導流 |
| i18n JSON | 新增 keys |
| App/Layout | 提供 `onNavigateToHost` callback 給 StatusBar |

### 8.3 不受影響

- Daemon 所有 API 端點（行為不變）
- Daemon WS handler（已支援 ticket）
- Host 資料持久化（HostConfig 型別不變）
- Tab / Session / Stream / Agent store
- 已建立的 WS 連線（auth 只在 upgrade 時驗）

### 8.4 風險

| 風險 | 緩解 |
|------|------|
| Phase 2 非 401/503 失敗 | 回退用 Phase 1 結果，不誤判為 auth-error |
| 重連延遲（多一次 HTTP） | 僅在重連路徑，本地網路 < 10ms |
| Pre-fetched ticket 過期 | TTL 30 秒，negotiate → WS 連線通常 < 1 秒 |
| `stop()` 無法中止 mid-Phase-2 fetch | fetch 完成後 epoch 檢查 → 結果被丟棄，不影響正確性（殭屍 fetch 可接受） |
| Stream ticket fetch 與 relay disconnect race | `then` callback 建立的 conn 會被下一輪 subscribe 清理（見 4.4 說明） |

---

## 九、��試範圍

### 9.1 SPA 測試

**`host-connection.test.ts`**（現有，需擴充）：

用 `vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce()` 鏈式 mock 序列呼叫。

| 測試 case | Phase 1 | Phase 2 | 預期 daemon |
|-----------|---------|---------|------------|
| Phase 1 only（無 token） | 200 | 不呼叫 | `auth-error`（非 pairing 時無 token 視為 auth 問題） |
| Phase 1 only（pairing mode 無 token） | 200 `mode:pairing` | 不呼叫 | `connected` |
| Phase 2 成功 | 200 | 200 + ticket | `connected` + ticket |
| Phase 2 返回 401 | 200 | 401 | `auth-error` |
| Phase 2 返回 503（PairingGuard）| 200 | 503 | `auth-error` |
| Phase 2 network error | 200 | throw | `connected`（回退 Phase 1） |
| Phase 2 timeout | 200 | 掛起 >5s | `connected`（Phase 2 abort） |
| Phase 1 timeout | 掛起 >6s | — | `unreachable` |
| Phase 1 network error | throw TypeError | — | `refused` |
| Mode 欄位解析 | 200 `mode:pending` | 200 | mode = `'pending'` |

**`connection-state-machine.test.ts`**（現有，需擴充）：

用 `vi.useFakeTimers()` + `vi.advanceTimersByTimeAsync()`。

| 測試 case | 說明 |
|-----------|------|
| auth-error 第一次就跳出 FAST_RETRY | checkFn 只被呼叫 1 次（非 3 次） |
| auth-error 不啟動背景重試 | advanceTimers 後 checkFn 不再被呼叫 |
| 手動 trigger 從 auth-error 恢復 | checkFn 改回 connected → onStateChange 被呼叫 |

**`ws.test.ts`**（現有，需擴充）：

MockWebSocket 需補 `this.url = url` 以驗證 ticket 附加。

| 測試 case | 說明 |
|-----------|------|
| getTicket 成功 → URL 含 `?ticket=` | 檢查 `wsInstances[0].url` |
| getTicket 失敗 → backoff 重試 | 用 fake timer 確認 setTimeout 排程 |
| getTicket 失敗 + canReconnect false → 停止 | 確認不再排程 |

**`stream-ws.test.ts`**（現有只測 parseStreamMessage，需從零建立 WS 連線測試）：

從 `ws.test.ts` 移植 MockWebSocket 基礎架構。但 `connectStream` 簽名不變（不加 getTicket），只需確認既有行為不被 Phase 5b 改動影響。

**`host-events.test.ts`**（新建）：

| 測試 case | 說明 |
|-----------|------|
| reconnectWithTicket(ticket) → URL 含 ticket | pendingTicket 一次性消費 |
| reconnectWithTicket 後 WS 斷線 → 回退 getTicket | pendingTicket 已清空 |
| getTicket 失敗 + 無 pendingTicket → 呼叫 onClose | 觸發狀態機路徑 |

**`host-utils.test.ts`**（現有，需加 1 case）：

| 測試 case | 說明 |
|-----------|------|
| `status: 'auth-error'` → `t('hosts.error_auth')` | 新狀態 |

### 9.2 Go 測試

| 檔案 | 測試項目 |
|------|---------|
| `middleware_test.go` | `TestTokenAuthQueryParam` 改為：`?token=secret` → 401（確認 fallback 已移除） |
| `middleware_test.go` | 確認 Bearer header + `?ticket=` 仍正常通過 |

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
