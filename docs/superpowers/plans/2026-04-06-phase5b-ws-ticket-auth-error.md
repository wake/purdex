# Phase 5b: WS Ticket 統一 + Auth Error UI — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 統一三條 WS 的 ticket auth、修正狀態機 auth 死循環、新增 auth-error UI、消費 health mode 欄位。

**Architecture:** Negotiation-First — 狀態機的 checkFn 升級為兩階段 negotiation（health + ws-ticket），同時驗證 reachability + auth。connectHostEvents 以 lazy mode 啟動，由狀態機觸發首次連線。Terminal/Stream WS 各自取 ticket。

**Tech Stack:** React 19 / Zustand 5 / Vitest / Go net/http middleware / Phosphor Icons

**Spec:** `docs/superpowers/specs/2026-04-06-phase5b-ws-ticket-auth-error-design.md`

---

### Task 1: Go — 移除 `?token=` legacy fallback

**Files:**
- Modify: `internal/middleware/middleware.go:79-83`
- Modify: `internal/middleware/middleware_test.go:80-88`

- [ ] **Step 1: 修改 Go test 為驗證 401**

```go
// internal/middleware/middleware_test.go — 把 TestTokenAuthQueryParam 改為：
func TestTokenAuthQueryParamRemoved(t *testing.T) {
	h := middleware.TokenAuth(func() string { return "secret" }, nil)(ok)
	req := httptest.NewRequest("GET", "/?token=secret", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 401 {
		t.Errorf("want 401 for removed query param token, got %d", rec.Code)
	}
}
```

- [ ] **Step 2: 執行 test 確認失敗**

Run: `cd /Users/wake/Workspace/wake/tmux-box && go test ./internal/middleware/ -run TestTokenAuthQueryParamRemoved -v`
Expected: FAIL（目前 ?token= 仍生效，回傳 200）

- [ ] **Step 3: 刪除 middleware.go 的 ?token= fallback**

刪除 `internal/middleware/middleware.go` 第 79-83 行：
```go
// 刪除以下段落：
// Fallback: ?token= query param (legacy, will be removed)
if subtle.ConstantTimeCompare([]byte(r.URL.Query().Get("token")), []byte(token)) == 1 {
    next.ServeHTTP(w, r)
    return
}
```

- [ ] **Step 4: 執行 test 確認通過**

Run: `go test ./internal/middleware/ -v`
Expected: 全部 PASS

- [ ] **Step 5: Commit**

```bash
git add internal/middleware/middleware.go internal/middleware/middleware_test.go
git commit -m "feat(middleware): remove legacy ?token= query param fallback"
```

---

### Task 2: checkHealth 兩階段 + HealthResult 擴充

**Files:**
- Modify: `spa/src/lib/host-connection.ts`
- Modify: `spa/src/lib/host-connection.test.ts`

- [ ] **Step 1: 寫 10 個 failing tests**

```typescript
// spa/src/lib/host-connection.test.ts — 替換整個檔案內容
import { describe, it, expect, vi, afterEach } from 'vitest'
import { checkHealth } from './host-connection'

function healthResponse(mode = 'normal') {
  return new Response(JSON.stringify({ ok: true, mode }), { status: 200 })
}
function ticketResponse(ticket = 'tk_abc') {
  return new Response(JSON.stringify({ ticket }), { status: 200 })
}

describe('checkHealth', () => {
  afterEach(() => { vi.restoreAllMocks() })

  it('Phase 1 only: no token, non-pairing → auth-error', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(healthResponse('normal'))
    const result = await checkHealth('http://localhost:7860')
    expect(result.daemon).toBe('auth-error')
    expect(result.mode).toBe('normal')
    expect(fetch).toHaveBeenCalledTimes(1) // Phase 2 skipped
  })

  it('Phase 1 only: no token, pairing mode → connected', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(healthResponse('pairing'))
    const result = await checkHealth('http://localhost:7860')
    expect(result.daemon).toBe('connected')
    expect(result.mode).toBe('pairing')
  })

  it('Phase 2 success: connected + ticket', async () => {
    vi.spyOn(globalThis, 'fetch')
      .mockResolvedValueOnce(healthResponse())
      .mockResolvedValueOnce(ticketResponse('tk_123'))
    const result = await checkHealth('http://localhost:7860', () => 'mytoken')
    expect(result.daemon).toBe('connected')
    expect(result.ticket).toBe('tk_123')
    expect(result.mode).toBe('normal')
  })

  it('Phase 2 returns 401 → auth-error', async () => {
    vi.spyOn(globalThis, 'fetch')
      .mockResolvedValueOnce(healthResponse())
      .mockResolvedValueOnce(new Response('unauthorized', { status: 401 }))
    const result = await checkHealth('http://localhost:7860', () => 'badtoken')
    expect(result.daemon).toBe('auth-error')
  })

  it('Phase 2 returns 503 (PairingGuard) → auth-error', async () => {
    vi.spyOn(globalThis, 'fetch')
      .mockResolvedValueOnce(healthResponse())
      .mockResolvedValueOnce(new Response('pairing_mode', { status: 503 }))
    const result = await checkHealth('http://localhost:7860', () => 'mytoken')
    expect(result.daemon).toBe('auth-error')
  })

  it('Phase 2 network error → fallback connected', async () => {
    vi.spyOn(globalThis, 'fetch')
      .mockResolvedValueOnce(healthResponse())
      .mockRejectedValueOnce(new TypeError('Failed to fetch'))
    const result = await checkHealth('http://localhost:7860', () => 'mytoken')
    expect(result.daemon).toBe('connected')
    expect(result.ticket).toBeUndefined()
  })

  it('Phase 1 timeout → unreachable', async () => {
    vi.spyOn(globalThis, 'fetch').mockRejectedValue(
      Object.assign(new Error('signal is aborted'), { name: 'AbortError' })
    )
    const result = await checkHealth('http://localhost:7860', () => 'mytoken')
    expect(result.daemon).toBe('unreachable')
    expect(result.latency).toBeNull()
  })

  it('Phase 1 network error → refused', async () => {
    vi.spyOn(globalThis, 'fetch').mockRejectedValue(new TypeError('Failed to fetch'))
    const result = await checkHealth('http://localhost:7860', () => 'mytoken')
    expect(result.daemon).toBe('refused')
  })

  it('mode field parsed from health response', async () => {
    vi.spyOn(globalThis, 'fetch')
      .mockResolvedValueOnce(healthResponse('pending'))
      .mockResolvedValueOnce(ticketResponse())
    const result = await checkHealth('http://localhost:7860', () => 'tok')
    expect(result.mode).toBe('pending')
  })

  it('latency measured from Phase 1', async () => {
    vi.spyOn(globalThis, 'fetch')
      .mockResolvedValueOnce(healthResponse())
      .mockResolvedValueOnce(ticketResponse())
    const result = await checkHealth('http://localhost:7860', () => 'tok')
    expect(result.latency).toBeGreaterThanOrEqual(0)
  })
})
```

- [ ] **Step 2: 執行 test 確認失敗**

Run: `cd spa && npx vitest run src/lib/host-connection.test.ts`
Expected: FAIL（checkHealth 簽名不符 + 無 auth-error 等新值）

- [ ] **Step 3: 實作 checkHealth 兩階段**

將 `spa/src/lib/host-connection.ts` 替換為 spec 3.2 的完整實作：

```typescript
// spa/src/lib/host-connection.ts — Health check with two-phase negotiation

export interface HealthResult {
  daemon: 'connected' | 'refused' | 'unreachable' | 'auth-error'
  tmux: 'ok' | 'unavailable'
  latency: number | null
  mode: 'pairing' | 'pending' | 'normal'
  ticket?: string
}

const PHASE1_TIMEOUT_MS = 6000
const PHASE2_TIMEOUT_MS = 5000

export async function checkHealth(
  baseUrl: string,
  getToken?: () => string | undefined,
): Promise<HealthResult> {
  const ctrl1 = new AbortController()
  const timer1 = setTimeout(() => ctrl1.abort(), PHASE1_TIMEOUT_MS)
  try {
    const start = performance.now()
    const res = await fetch(`${baseUrl}/api/health`, { signal: ctrl1.signal })
    const latency = Math.round(performance.now() - start)
    const body = await res.json()
    const mode = (body.mode ?? 'normal') as 'pairing' | 'pending' | 'normal'

    const token = getToken?.()
    if (!token) {
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
        return { daemon: 'auth-error', tmux: 'unavailable', latency, mode }
      }
      if (!ticketRes.ok) {
        return { daemon: 'connected', tmux: 'unavailable', latency, mode }
      }
      const { ticket } = await ticketRes.json()
      return { daemon: 'connected', tmux: 'unavailable', latency, mode, ticket }
    } catch {
      return { daemon: 'connected', tmux: 'unavailable', latency, mode }
    } finally {
      clearTimeout(timer2)
    }
  } catch (err: unknown) {
    if (err instanceof Error && err.name === 'AbortError') {
      return { daemon: 'unreachable', tmux: 'unavailable', latency: null, mode: 'normal' }
    }
    return { daemon: 'refused', tmux: 'unavailable', latency: null, mode: 'normal' }
  } finally {
    clearTimeout(timer1)
  }
}
```

- [ ] **Step 4: 執行 test 確認通過**

Run: `cd spa && npx vitest run src/lib/host-connection.test.ts`
Expected: 10 tests PASS

- [ ] **Step 5: Commit**

```bash
git add spa/src/lib/host-connection.ts spa/src/lib/host-connection.test.ts
git commit -m "feat: checkHealth two-phase negotiation (health + ws-ticket)"
```

---

### Task 3: ConnectionStateMachine auth-error 分支

**Files:**
- Modify: `spa/src/lib/connection-state-machine.ts`
- Modify: `spa/src/lib/connection-state-machine.test.ts`

- [ ] **Step 1: 寫 3 個 failing tests**

在 `connection-state-machine.test.ts` 末尾 `})` 前新增：

```typescript
  it('auth-error exits FAST_RETRY on first attempt', async () => {
    checkFn.mockResolvedValue({ daemon: 'auth-error', tmux: 'unavailable', latency: 5, mode: 'normal' })
    sm = new ConnectionStateMachine(checkFn, onStateChange)
    await sm.trigger()
    expect(checkFn).toHaveBeenCalledTimes(1) // not 3
    expect(onStateChange).toHaveBeenCalledWith(
      expect.objectContaining({ daemon: 'auth-error' })
    )
  })

  it('auth-error does not start background retry', async () => {
    checkFn.mockResolvedValue({ daemon: 'auth-error', tmux: 'unavailable', latency: 5, mode: 'normal' })
    sm = new ConnectionStateMachine(checkFn, onStateChange)
    await sm.trigger()
    const countAfter = checkFn.mock.calls.length
    await vi.advanceTimersByTimeAsync(10_000)
    expect(checkFn.mock.calls.length).toBe(countAfter) // no background retries
  })

  it('manual trigger recovers from auth-error', async () => {
    checkFn.mockResolvedValue({ daemon: 'auth-error', tmux: 'unavailable', latency: 5, mode: 'normal' })
    sm = new ConnectionStateMachine(checkFn, onStateChange)
    await sm.trigger()
    checkFn.mockResolvedValue({ daemon: 'connected', tmux: 'ok', latency: 3, mode: 'normal' })
    await sm.trigger()
    const lastCall = onStateChange.mock.calls[onStateChange.mock.calls.length - 1][0]
    expect(lastCall.daemon).toBe('connected')
  })
```

- [ ] **Step 2: 執行 test 確認失敗**

Run: `cd spa && npx vitest run src/lib/connection-state-machine.test.ts`
Expected: 新增的 3 個 test FAIL（auth-error 未被 FAST_RETRY 特殊處理）

- [ ] **Step 3: 實作 auth-error 分支**

修改 `spa/src/lib/connection-state-machine.ts` 的 `trigger()` 方法，在 FAST_RETRY 迴圈內加入 auth-error 短路：

```typescript
  // 在 FAST_RETRY 迴圈裡，connected return 的後面加：
  if (lastResult.daemon === 'connected') {
    return // recovered
  }
  if (lastResult.daemon === 'auth-error') {
    return // permanent error, don't retry
  }
```

- [ ] **Step 4: 執行全部 test 確認通過**

Run: `cd spa && npx vitest run src/lib/connection-state-machine.test.ts`
Expected: 全部 PASS（含原有 9 + 新增 3 = 12 tests）

- [ ] **Step 5: Commit**

```bash
git add spa/src/lib/connection-state-machine.ts spa/src/lib/connection-state-machine.test.ts
git commit -m "feat: ConnectionStateMachine auth-error branch (no retry)"
```

---

### Task 4: HostRuntime 型別 + host-utils + i18n

**Files:**
- Modify: `spa/src/stores/useHostStore.ts:17-24`
- Modify: `spa/src/lib/host-utils.ts`
- Modify: `spa/src/lib/host-utils.test.ts`
- Modify: `spa/src/locales/zh-TW.json`
- Modify: `spa/src/locales/en.json`

- [ ] **Step 1: 寫 failing test**

在 `spa/src/lib/host-utils.test.ts` 末尾 `})` 前新增：

```typescript
  it('returns auth error message for auth-error status', () => {
    const runtime: HostRuntime = { status: 'auth-error', daemonState: 'auth-error' }
    expect(connectionErrorMessage(runtime, t)).toBe('hosts.error_auth')
  })
```

- [ ] **Step 2: 執行 test 確認失敗**

Run: `cd spa && npx vitest run src/lib/host-utils.test.ts`
Expected: FAIL（TypeScript: 'auth-error' 不在 status union type 中）

- [ ] **Step 3: 擴充 HostRuntime 型別**

修改 `spa/src/stores/useHostStore.ts`：

```typescript
export interface HostRuntime {
  status: 'connected' | 'disconnected' | 'reconnecting' | 'auth-error'
  latency?: number
  info?: HostInfo
  daemonState?: 'connected' | 'refused' | 'unreachable' | 'auth-error'
  tmuxState?: 'ok' | 'unavailable'
  manualRetry?: () => void
}
```

- [ ] **Step 4: 擴充 connectionErrorMessage**

修改 `spa/src/lib/host-utils.ts`，在函式開頭加入 auth-error 檢查：

```typescript
export function connectionErrorMessage(
  runtime: HostRuntime | undefined,
  t: (key: string) => string,
): string | null {
  if (runtime?.status === 'auth-error') return t('hosts.error_auth')
  if (!runtime || runtime.status !== 'connected') {
    if (runtime?.daemonState === 'unreachable') return t('hosts.error_unreachable')
    if (runtime?.daemonState === 'refused') return t('hosts.error_refused')
    return null
  }
  if (runtime.tmuxState === 'unavailable') return t('hosts.error_tmux_down')
  return null
}
```

- [ ] **Step 5: 新增 i18n keys**

在 `spa/src/locales/zh-TW.json` 的 `hosts.error_tmux_down` 後新增：

```json
  "hosts.error_auth": "Token 無效，請確認 Token 與 daemon 設定一致",
  "hosts.auth_error": "Token 無效",
  "hosts.auth_error_hint": "請確認 Token 與 daemon 設定一致，修改後自動重新連線",
  "status.auth_error": "Token 無效，點擊設定",
```

在 `spa/src/locales/en.json` 對應位置新增：

```json
  "hosts.error_auth": "Invalid token — check that the token matches the daemon config",
  "hosts.auth_error": "Invalid token",
  "hosts.auth_error_hint": "Verify the token matches the daemon config. Changes reconnect automatically.",
  "status.auth_error": "Invalid token — click to configure",
```

- [ ] **Step 6: 執行 test 確認通過**

Run: `cd spa && npx vitest run src/lib/host-utils.test.ts`
Expected: 全部 PASS（含新增 1 case）

- [ ] **Step 7: Lint**

Run: `cd spa && pnpm run lint`
Expected: 無新增 error

- [ ] **Step 8: Commit**

```bash
git add spa/src/stores/useHostStore.ts spa/src/lib/host-utils.ts spa/src/lib/host-utils.test.ts spa/src/locales/zh-TW.json spa/src/locales/en.json
git commit -m "feat: HostRuntime auth-error type + connectionErrorMessage + i18n keys"
```

---

### Task 5: connectHostEvents — lazy mode + reconnectWithTicket

**Files:**
- Modify: `spa/src/lib/host-events.ts`
- Create: `spa/src/lib/host-events.test.ts`

- [ ] **Step 1: 寫 failing tests**

```typescript
// spa/src/lib/host-events.test.ts
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { connectHostEvents } from './host-events'

class MockWebSocket {
  static CONNECTING = 0
  static OPEN = 1
  static CLOSED = 3
  readyState = MockWebSocket.CONNECTING
  binaryType = ''
  url: string
  onopen: (() => void) | null = null
  onclose: (() => void) | null = null
  onmessage: ((e: { data: unknown }) => void) | null = null
  onerror: (() => void) | null = null
  send = vi.fn()
  close = vi.fn(() => { this.readyState = MockWebSocket.CLOSED; this.onclose?.() })
  simulateOpen() { this.readyState = MockWebSocket.OPEN; this.onopen?.() }
  constructor(url: string) { this.url = url; wsInstances.push(this) }
}

let wsInstances: MockWebSocket[] = []

beforeEach(() => {
  wsInstances = []
  vi.stubGlobal('WebSocket', MockWebSocket)
})
afterEach(() => { vi.unstubAllGlobals() })

describe('connectHostEvents', () => {
  it('lazy mode does not connect immediately', () => {
    connectHostEvents('ws://test/ws/host-events', vi.fn(), undefined, undefined, undefined, false, true)
    expect(wsInstances).toHaveLength(0)
  })

  it('reconnectWithTicket creates WS with ticket in URL', async () => {
    const conn = connectHostEvents('ws://test/ws/host-events', vi.fn(), undefined, undefined, undefined, false, true)
    conn.reconnectWithTicket('tk_pre')
    await vi.dynamicImportSettled?.() // flush microtasks
    await new Promise((r) => setTimeout(r, 0))
    expect(wsInstances).toHaveLength(1)
    expect(wsInstances[0].url).toContain('ticket=tk_pre')
  })

  it('pendingTicket is consumed once, second connect falls back to getTicket', async () => {
    const getTicket = vi.fn().mockResolvedValue('tk_callback')
    const conn = connectHostEvents('ws://test/ws/host-events', vi.fn(), undefined, undefined, getTicket, false, true)
    conn.reconnectWithTicket('tk_once')
    await new Promise((r) => setTimeout(r, 0))
    expect(wsInstances[0].url).toContain('ticket=tk_once')
    // Simulate WS close → reconnect without pendingTicket
    wsInstances[0].close()
    conn.reconnect()
    await new Promise((r) => setTimeout(r, 0))
    expect(getTicket).toHaveBeenCalled()
    expect(wsInstances[1].url).toContain('ticket=tk_callback')
  })

  it('getTicket failure with no pendingTicket calls onClose', async () => {
    const onClose = vi.fn()
    const getTicket = vi.fn().mockRejectedValue(new Error('401'))
    const conn = connectHostEvents('ws://test/ws/host-events', vi.fn(), onClose, undefined, getTicket, false, true)
    conn.reconnect()
    await new Promise((r) => setTimeout(r, 0))
    expect(onClose).toHaveBeenCalled()
    expect(wsInstances).toHaveLength(0) // no WS created
  })

  it('connecting flag prevents concurrent connect calls', async () => {
    const getTicket = vi.fn().mockImplementation(() => new Promise((r) => setTimeout(() => r('tk'), 100)))
    const conn = connectHostEvents('ws://test/ws/host-events', vi.fn(), undefined, undefined, getTicket, false, true)
    conn.reconnect()
    conn.reconnect() // second call while first is awaiting getTicket
    await new Promise((r) => setTimeout(r, 150))
    expect(getTicket).toHaveBeenCalledTimes(1) // only one connect ran
  })
})
```

- [ ] **Step 2: 執行 test 確認失敗**

Run: `cd spa && npx vitest run src/lib/host-events.test.ts`
Expected: FAIL（connectHostEvents 無 lazy 參數、無 reconnectWithTicket）

- [ ] **Step 3: 實作 connectHostEvents 改動**

修改 `spa/src/lib/host-events.ts`，按 spec 4.2 實作 lazy mode、connecting flag、pendingTicket、reconnectWithTicket。完整替換：

```typescript
// spa/src/lib/host-events.ts

export interface HostEvent {
  type: 'handoff' | 'relay' | 'hook' | 'sessions' | 'tmux'
  session: string
  value: string
}

export interface EventConnection {
  close: () => void
  reconnect: () => void
  reconnectWithTicket: (ticket?: string) => void
}

export function connectHostEvents(
  url: string,
  onEvent: (event: HostEvent) => void,
  onClose?: () => void,
  onOpen?: () => void,
  getTicket?: () => Promise<string>,
  autoReconnect = true,
  lazy = false,
): EventConnection {
  let ws: WebSocket
  let retryMs = 1000
  let closed = false
  let connecting = false
  let pendingTicket: string | undefined

  async function connect() {
    if (connecting) return
    connecting = true
    try {
      let wsUrl = url
      const ticket = pendingTicket ?? (getTicket ? await getTicket().catch(() => null) : null)
      pendingTicket = undefined

      if (ticket) {
        const u = new URL(wsUrl)
        u.searchParams.set('ticket', ticket)
        wsUrl = u.toString()
      } else if (getTicket) {
        if (!closed) onClose?.()
        return
      }

      ws = new WebSocket(wsUrl)
      ws.onopen = () => { retryMs = 1000; onOpen?.() }
      ws.onmessage = (e) => {
        try {
          const event = JSON.parse(e.data) as HostEvent
          onEvent(event)
        } catch { /* ignore parse errors */ }
      }
      ws.onerror = () => {}
      ws.onclose = () => {
        if (closed) return
        onClose?.()
        if (autoReconnect) {
          setTimeout(() => { if (!closed) connect() }, retryMs)
          retryMs = Math.min(retryMs * 2, 30000)
        }
      }
    } finally {
      connecting = false
    }
  }

  if (!lazy) connect()
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

- [ ] **Step 4: 執行 test 確認通過**

Run: `cd spa && npx vitest run src/lib/host-events.test.ts`
Expected: 全部 PASS

- [ ] **Step 5: Commit**

```bash
git add spa/src/lib/host-events.ts spa/src/lib/host-events.test.ts
git commit -m "feat: connectHostEvents lazy mode + reconnectWithTicket"
```

---

### Task 6: connectTerminal — getTicket 參數

**Files:**
- Modify: `spa/src/lib/ws.ts`
- Modify: `spa/src/lib/ws.test.ts`

- [ ] **Step 1: 修改 MockWebSocket 記錄 URL + 寫 3 個 failing tests**

在 `ws.test.ts` 的 MockWebSocket constructor 加入 URL：
```typescript
// 改為：
vi.stubGlobal('WebSocket', class extends MockWebSocket {
  constructor(url: string) {
    super()
    this.url = url
    wsInstances.push(this)
  }
})
```

MockWebSocket class 加入 `url = ''`。

在檔案末尾新增測試：

```typescript
describe('connectTerminal with getTicket', () => {
  it('appends ticket to WS URL on success', async () => {
    const getTicket = vi.fn().mockResolvedValue('tk_term')
    connectTerminal('ws://test/ws/terminal/abc', vi.fn(), vi.fn(), undefined, undefined, getTicket)
    await vi.advanceTimersByTimeAsync(0)
    expect(wsInstances[0].url).toContain('ticket=tk_term')
  })

  it('retries with backoff on getTicket failure', async () => {
    const getTicket = vi.fn().mockRejectedValue(new Error('401'))
    const onClose = vi.fn()
    connectTerminal('ws://test/ws/terminal/abc', vi.fn(), onClose, undefined, undefined, getTicket)
    await vi.advanceTimersByTimeAsync(0)
    expect(wsInstances).toHaveLength(0) // no WS created
    expect(onClose).not.toHaveBeenCalled() // no onClose — self backoff
    // After 1s backoff, retry
    getTicket.mockResolvedValueOnce('tk_retry')
    await vi.advanceTimersByTimeAsync(1100)
    expect(wsInstances).toHaveLength(1)
    expect(wsInstances[0].url).toContain('ticket=tk_retry')
  })

  it('stops retrying when canReconnect returns false', async () => {
    const getTicket = vi.fn().mockRejectedValue(new Error('401'))
    connectTerminal('ws://test/ws/terminal/abc', vi.fn(), vi.fn(), undefined, () => false, getTicket)
    await vi.advanceTimersByTimeAsync(0)
    await vi.advanceTimersByTimeAsync(1100)
    expect(getTicket).toHaveBeenCalledTimes(1) // no retry
  })
})
```

- [ ] **Step 2: 執行 test 確認失敗**

Run: `cd spa && npx vitest run src/lib/ws.test.ts`
Expected: 新增的 3 個 test FAIL

- [ ] **Step 3: 實作 connectTerminal getTicket**

修改 `spa/src/lib/ws.ts`，`connectTerminal` 加入 `getTicket` 參數，`connect()` 改為 async，按 spec 4.3 實作。

函式簽名改為：
```typescript
export function connectTerminal(
  url: string,
  onData: (data: ArrayBuffer) => void,
  onClose: () => void,
  onOpen?: () => void,
  canReconnect?: () => boolean,
  getTicket?: () => Promise<string>,
): TerminalConnection {
```

`connect()` 改為 `async function connect()`，開頭加入：
```typescript
    let wsUrl = url
    if (getTicket) {
      try {
        const ticket = await getTicket()
        const u = new URL(wsUrl)
        u.searchParams.set('ticket', ticket)
        wsUrl = u.toString()
      } catch {
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
```

- [ ] **Step 4: 執行 test 確認通過**

Run: `cd spa && npx vitest run src/lib/ws.test.ts`
Expected: 全部 PASS

- [ ] **Step 5: Commit**

```bash
git add spa/src/lib/ws.ts spa/src/lib/ws.test.ts
git commit -m "feat: connectTerminal getTicket param for WS ticket auth"
```

---

### Task 7: useMultiHostEventWs — 整合 negotiation + lazy connect

**Files:**
- Modify: `spa/src/hooks/useMultiHostEventWs.ts`

- [ ] **Step 1: 修改 useMultiHostEventWs**

核心改動：
1. 狀態機 `checkFn` 傳入 `getToken` 動態 closure
2. `connectHostEvents` 設 `lazy: true`
3. `onStateChange` 用 statusMap 映射 + reconnectWithTicket/reconnect 分流
4. 移除 `getTicket` 參數改由狀態機管理初始連線
5. Effect 尾部加 `sm.trigger()`

修改 `useMultiHostEventWs.ts` 中 per-host 迴圈內：

```typescript
      // --- Connection state machine (per host) ---
      const connRef: { current: EventConnection | undefined } = { current: undefined }

      const statusMap: Record<string, string> = {
        connected: 'connected',
        unreachable: 'disconnected',
        refused: 'disconnected',
        'auth-error': 'auth-error',
      }

      const sm = new ConnectionStateMachine(
        () => checkHealth(baseUrl, () => useHostStore.getState().hosts[hostId]?.token),
        (result) => {
          useHostStore.getState().setRuntime(hostId, {
            status: (statusMap[result.daemon] ?? 'disconnected') as HostRuntime['status'],
            latency: result.latency ?? undefined,
            daemonState: result.daemon,
          })
          // On recovery → reconnect WS with pre-fetched ticket
          if (result.daemon === 'connected' && connRef.current) {
            if (result.ticket) {
              connRef.current.reconnectWithTicket(result.ticket)
            } else {
              connRef.current.reconnect()
            }
          }
        },
      )
      stateMachines.set(hostId, sm)
      useHostStore.getState().setRuntime(hostId, { manualRetry: () => sm.trigger() })

      // --- WS connection (per host, lazy — waits for SM) ---
      const conn = connectHostEvents(
        wsUrl,
        (event) => { /* ... 既有 event handler 不變 ... */ },
        () => {
          useHostStore.getState().setRuntime(hostId, { status: 'reconnecting' })
          sm.trigger()
        },
        () => {
          useHostStore.getState().setRuntime(hostId, {
            status: 'connected',
            daemonState: 'connected',
          })
          useAgentStore.getState().clearSubagentsForHost(hostId)
          const daemonBase = useHostStore.getState().getDaemonBase(hostId)
          useSessionStore.getState().fetchHost(hostId, daemonBase).catch(() => {})
        },
        () => fetchWsTicket(hostId),
        false,  // autoReconnect = false
        true,   // lazy = true
      )
      connRef.current = conn
      connections.set(hostId, conn)

      // Start negotiation — SM will trigger reconnectWithTicket on success
      sm.trigger()
```

- [ ] **Step 2: 加入 HostRuntime type import**

在 imports 區加入：`import type { HostRuntime } from '../stores/useHostStore'`

- [ ] **Step 3: 執行 lint + test**

Run: `cd spa && pnpm run lint && npx vitest run`
Expected: lint 無 error，既有 test 全部 PASS

- [ ] **Step 4: Commit**

```bash
git add spa/src/hooks/useMultiHostEventWs.ts
git commit -m "feat: useMultiHostEventWs negotiation-first + lazy connect"
```

---

### Task 8: useRelayWsManager — 外層 ticket fetch

**Files:**
- Modify: `spa/src/hooks/useRelayWsManager.ts`

- [ ] **Step 1: 修改 relay connected 分支**

在 `useRelayWsManager.ts` 的 `if (connected && !wasConnected)` 分支內，把同步的 `connectStream` 改為先 await ticket。加入 `fetchWsTicket` import：

```typescript
import { fetchWsTicket } from '../lib/host-api'
```

改寫建立邏輯：

```typescript
          if (connected && !wasConnected) {
            const wsBase = useHostStore.getState().getWsBase(hostId)

            fetchWsTicket(hostId).then((ticket) => {
              if (!useStreamStore.getState().relayStatus[ck]) return

              const url = new URL(`${wsBase}/ws/cli-bridge-sub/${encodeURIComponent(sessionCode)}`)
              url.searchParams.set('ticket', ticket)

              const conn = connectStream(
                url.toString(),
                (msg: StreamMessage) => { /* ... 既有 onMessage 不變 ... */ },
                () => {
                  useStreamStore.getState().setConn(hostId, sessionCode, null)
                  activeConns.delete(ck)
                },
              )
              useStreamStore.getState().setConn(hostId, sessionCode, conn)
              activeConns.set(ck, conn)
            }).catch((err) => {
              if (!(err instanceof Error && err.message.includes('ws-ticket'))) {
                console.error('[useRelayWsManager] stream WS setup error', err)
              }
            })
          }
```

- [ ] **Step 2: 執行 lint + test**

Run: `cd spa && pnpm run lint && npx vitest run`
Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add spa/src/hooks/useRelayWsManager.ts
git commit -m "feat: useRelayWsManager ticket fetch before stream WS"
```

---

### Task 9: SessionPaneContent — 傳入 getTicket

**Files:**
- Modify: `spa/src/components/SessionPaneContent.tsx`
- Modify: `spa/src/hooks/useTerminalWs.ts`

- [ ] **Step 1: useTerminalWs 加 getTicket prop**

修改 `spa/src/hooks/useTerminalWs.ts`：

介面加入 `getTicket?: () => Promise<string>`：
```typescript
interface UseTerminalWsOpts {
  wsUrl: string
  // ... 既有 props ...
  hostId?: string
  getTicket?: () => Promise<string>  // 新增
  // ...
}
```

解構時加入 `getTicket`，傳入 `connectTerminal`：
```typescript
    const conn = connectTerminal(
      wsUrl,
      (data) => { /* ... */ },
      () => onDisconnectRef.current(),
      () => { /* ... */ },
      canReconnect,
      getTicket,  // 新增
    )
```

- [ ] **Step 2: SessionPaneContent 傳入 getTicket**

修改 `spa/src/components/SessionPaneContent.tsx`，在 `<TerminalView>` 或直接傳給 `useTerminalWs`：

加入 import：
```typescript
import { fetchWsTicket } from '../lib/host-api'
```

在呼叫 `useTerminalWs` 或傳給 `TerminalView` 的 props 中加入：
```typescript
getTicket={() => fetchWsTicket(hostId)}
```

- [ ] **Step 3: 執行 lint + test**

Run: `cd spa && pnpm run lint && npx vitest run`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add spa/src/hooks/useTerminalWs.ts spa/src/components/SessionPaneContent.tsx
git commit -m "feat: terminal WS ticket auth via getTicket callback"
```

---

### Task 10: HostSidebar — auth-error 圖示

**Files:**
- Modify: `spa/src/components/hosts/HostSidebar.tsx`

- [ ] **Step 1: 加入 LockSimple import + auth-error 分支**

修改 `HostSidebar.tsx`，import 加入 `LockSimple`：
```typescript
import { Plus, CaretDown, CaretRight, Circle, Spinner, Warning, LockSimple } from '@phosphor-icons/react'
```

`StatusIcon` 函式在 `reconnecting` 和 default 之間插入：
```typescript
  if (runtime.status === 'auth-error')
    return <LockSimple size={12} weight="fill" className="text-red-400" />
```

Host 名稱在 auth-error 時改為紅色。在 button 的 className 邏輯中，加入：
```typescript
// 在 truncate flex-1 的 span 上加條件 class
className={`truncate flex-1 ${runtime[hostId]?.status === 'auth-error' ? 'text-red-400' : ''}`}
```

- [ ] **Step 2: 執行 lint**

Run: `cd spa && pnpm run lint`
Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add spa/src/components/hosts/HostSidebar.tsx
git commit -m "feat: HostSidebar auth-error lock icon"
```

---

### Task 11: StatusBar — auth-error 顯示 + 導航

**Files:**
- Modify: `spa/src/components/StatusBar.tsx`
- Modify: `spa/src/App.tsx`（提供 onNavigateToHost callback）

- [ ] **Step 1: StatusBar 加入 auth-error 狀態 + onNavigateToHost prop**

修改 `StatusBar.tsx` Props：
```typescript
interface Props {
  activeTab: Tab | null
  onViewModeChange?: (tabId: string, paneId: string, mode: 'terminal' | 'stream') => void
  onNavigateToHost?: (hostId: string) => void  // 新增
}
```

import 加入 `LockSimple`：
```typescript
import { CaretUp, CircleNotch, CheckCircle, XCircle, LockSimple } from '@phosphor-icons/react'
```

狀態顯示邏輯（L139-148 區域）加入 auth-error 分支：
```typescript
      <span
        className={
          status === 'auth-error' ? 'text-red-400 cursor-pointer flex items-center gap-1'
            : status === 'connected' && hostRuntime?.tmuxState === 'unavailable' ? 'text-yellow-400'
            : status === 'connected' ? 'text-green-500'
            : status === 'reconnecting' ? 'text-yellow-400'
            : 'text-red-400'
        }
        onClick={status === 'auth-error' && agentHostId ? () => onNavigateToHost?.(agentHostId) : undefined}
      >
        {status === 'auth-error' && <LockSimple size={10} weight="fill" />}
        {status === 'auth-error' ? t('hosts.auth_error')
          : status === 'connected' && hostRuntime?.tmuxState === 'unavailable'
            ? t('hosts.error_tmux_down')
            : status}
      </span>
```

- [ ] **Step 2: App.tsx 傳入 onNavigateToHost**

在 `App.tsx` 中，`StatusBar` 的 props 加入 `onNavigateToHost` callback，觸發切換到 HostPage 的 OverviewSection。具體實作取決於 App 的路由/頁面切換機制（查看 App.tsx 既有的頁面切換 state）。

- [ ] **Step 3: 執行 lint**

Run: `cd spa && pnpm run lint`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add spa/src/components/StatusBar.tsx spa/src/App.tsx
git commit -m "feat: StatusBar auth-error display + navigation"
```

---

### Task 12: OverviewSection — auth-error banner + 自動重試

**Files:**
- Modify: `spa/src/components/hosts/OverviewSection.tsx`

- [ ] **Step 1: 加入 auth-error banner**

import 加入 `LockSimple`：
```typescript
import { CaretDown, CaretRight, ArrowsClockwise, Trash, Plugs, Eye, EyeSlash, Check, X, LockSimple } from '@phosphor-icons/react'
```

在 `<h2>` 標題下方、Connection Section 上方插入 auth-error banner：
```tsx
      {runtime?.status === 'auth-error' && (
        <div className="flex items-start gap-3 px-3 py-2.5 rounded-md mb-4 bg-red-500/10 border border-red-500/20">
          <LockSimple size={16} weight="fill" className="text-red-400 flex-shrink-0 mt-0.5" />
          <div>
            <p className="text-sm text-red-400 font-medium">{t('hosts.auth_error')}</p>
            <p className="text-xs text-text-muted mt-0.5">{t('hosts.auth_error_hint')}</p>
          </div>
        </div>
      )}
```

- [ ] **Step 2: Status 欄位加入 auth-error 顯示**

修改 status className（L502-506 區域）加入 auth-error：
```typescript
            runtime?.status === 'auth-error' ? 'text-red-400'
              : runtime?.status === 'connected' && runtime?.tmuxState === 'unavailable' ? 'text-yellow-400'
              : ...
```

statusLabel 加入 auth-error 圖示：
```typescript
  const statusLabel = (r?: HostRuntime) => {
    if (!r) return 'unknown'
    if (r.status === 'auth-error') return 'auth-error'
    return r.status
  }
```

- [ ] **Step 3: TokenField onSave 觸發自動重試**

修改 TokenField 的 `onSave` callback：
```typescript
        <TokenField
          token={host.token}
          ip={host.ip}
          port={host.port}
          onSave={(token) => {
            updateHost(hostId, { token: token || undefined })
            // Auto-retry: set reconnecting then trigger SM
            useHostStore.getState().setRuntime(hostId, { status: 'reconnecting' })
            const rt = useHostStore.getState().runtime[hostId]
            if (rt?.manualRetry) rt.manualRetry()
          }}
          t={t}
        />
```

- [ ] **Step 4: 執行 lint**

Run: `cd spa && pnpm run lint`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add spa/src/components/hosts/OverviewSection.tsx
git commit -m "feat: OverviewSection auth-error banner + token auto-retry"
```

---

### Task 13: AddHostDialog — health mode 自動導流（#167）

**Files:**
- Modify: `spa/src/components/hosts/AddHostDialog.tsx`

- [ ] **Step 1: 加入 debounced health check**

在 `AddHostDialog` 元件內新增 state 和 effect：

```typescript
  const [healthMode, setHealthMode] = useState<'pairing' | 'pending' | 'normal' | null>(null)

  // Debounced health check when in manual mode with IP+port filled
  useEffect(() => {
    if (!useToken || !ip || stage === 'saving' || stage === 'done') {
      setHealthMode(null)
      return
    }
    const portNum = port || '7860'
    const timer = setTimeout(async () => {
      try {
        const res = await fetch(`http://${ip}:${portNum}/api/health`)
        const body = await res.json()
        setHealthMode(body.mode ?? 'normal')
      } catch {
        setHealthMode(null)
      }
    }, 300)
    return () => clearTimeout(timer)
  }, [useToken, ip, port, stage])
```

- [ ] **Step 2: auto-switch on pairing detection**

在 `useEffect` 的 health mode 成功回呼後加入 auto-switch 邏輯（只在 token 欄位空白時）：

```typescript
      // After setHealthMode, check for auto-switch
      if (body.mode === 'pairing' && !token) {
        handleToggleToken(false) // switch to pairing route
      }
```

- [ ] **Step 3: 顯示 mode badge**

在 Host/Port/Token 欄位上方加入 mode 提示：

```tsx
          {healthMode && useToken && (
            <div className={`flex items-start gap-2 px-2 py-2 rounded text-xs ${
              healthMode === 'pairing' ? 'bg-yellow-500/10 border border-yellow-500/20 text-yellow-400'
                : healthMode === 'pending' ? 'bg-blue-500/10 border border-blue-500/20 text-blue-400'
                : 'bg-green-500/10 border border-green-500/20 text-green-400'
            }`}>
              {healthMode === 'pairing' && t('hosts.mode_pairing_hint')}
              {healthMode === 'pending' && t('hosts.mode_pending_hint')}
              {healthMode === 'normal' && t('hosts.mode_normal_hint')}
            </div>
          )}
```

- [ ] **Step 4: 加入 i18n keys**

在 `zh-TW.json` 和 `en.json` 新增：
```json
  "hosts.mode_pairing_hint": "Daemon 等待配對中",
  "hosts.mode_pending_hint": "請輸入 daemon 終端機顯示的 Token",
  "hosts.mode_normal_hint": "Daemon 運作中，請輸入已設定的 Token"
```

- [ ] **Step 5: 執行 lint + test**

Run: `cd spa && pnpm run lint && npx vitest run`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add spa/src/components/hosts/AddHostDialog.tsx spa/src/locales/zh-TW.json spa/src/locales/en.json
git commit -m "feat: AddHostDialog health mode auto-detect (#167)"
```

---

### Task 14: 全面整合測試 + build 驗證

**Files:** None (verification only)

- [ ] **Step 1: 執行全部 SPA 測試**

Run: `cd spa && npx vitest run`
Expected: 全部 PASS

- [ ] **Step 2: 執行 SPA lint**

Run: `cd spa && pnpm run lint`
Expected: 無 error

- [ ] **Step 3: 執行 SPA build**

Run: `cd spa && pnpm run build`
Expected: 建置成功

- [ ] **Step 4: 執行全部 Go 測試**

Run: `go test ./...`
Expected: 全部 PASS

- [ ] **Step 5: Commit 任何 fix**

若前面步驟發現問題，修復後 commit。
