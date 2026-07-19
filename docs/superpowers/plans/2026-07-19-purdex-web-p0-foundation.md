# Purdex Web P0 — 地基阻斷修正 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 修掉兩個讓純瀏覽器 web 版無法成立的地基阻斷——host endpoint model 無法表達 `https://hostname`，以及 token 關時連線卡在 auth-error。

**Architecture:** 新增 `spa/src/lib/host-endpoint.ts` 純函式集中「host → HTTP base / WS base / endpoint identity key」的導出，支援 `scheme`（http/https，缺省 http 向後相容）；`useHostStore` 與 4 處 configKey/endpoint-identity 委派此模組；`checkHealth()` 改為「無 client token 也嘗試 `/api/ws-ticket`」，用 200/401 區分 token-disabled 與 token-required。

**Tech Stack:** React 19 / Zustand 5 / TypeScript strict / Vitest。

## Global Constraints

- 連線一律走**顯式 host endpoint（含 scheme）+ ticket**，origin 無關；**不用相對 URL 硬連自身**、**不靠 same-origin cookie 免 auth**（spec §6）。
- `HostConfig.scheme` **缺省視為 `'http'`**（向後相容既有 persist 資料，Alpha 階段不需 persist migration 框架）。
- endpoint 預設 port：`http`/`ws` = 80，`https`/`wss` = 443；等於預設 port 時 URL 省略 `:port`。
- 測試：`cd spa && npx vitest run`；Lint：`cd spa && pnpm run lint`；Build：`cd spa && pnpm run build`。
- Codex sandbox 無網路：SPA 驗證由主 Claude 手動 `pnpm install` + vitest/lint/build（本 plan 由 SPA 環境直接跑）。
- 每個 task 獨立 commit。

---

## File Structure

- **Create** `spa/src/lib/host-endpoint.ts` — 純 endpoint 導出：`hostScheme` / `deriveDaemonBase` / `deriveWsBase` / `hostEndpointKey`。單一責任，無 React/store 依賴。
- **Create** `spa/src/lib/host-endpoint.test.ts` — 上述純函式單元測試。
- **Modify** `spa/src/stores/useHostStore.ts` — `HostConfig` 加 `scheme?`；`getDaemonBase`/`getWsBase` 委派 `host-endpoint`；`addHost`/`updateHost` 型別加 `scheme`。
- **Create** `spa/src/stores/useHostStore.endpoint.test.ts` — store 層 endpoint 導出測試。
- **Modify** `spa/src/hooks/useMultiHostEventWs.ts` — 2 處 configKey 改用 `hostEndpointKey`。
- **Modify** `spa/src/components/MemoryMonitorPage.tsx` — `snapshotHostTargetKey` 納入 scheme（改用 `hostEndpointKey`）。
- **Modify** `spa/src/lib/sync/contributors/hosts.ts` — `sameEndpoint` 改用 `hostEndpointKey`（scheme 變更即視為換 endpoint → token 重置）；export `mergeHostsPreservingTokens` 供測試。
- **Create** `spa/src/lib/sync/contributors/hosts.test.ts`（若不存在）— scheme 變更 → token 重置回歸測試。
- **Modify** `spa/src/lib/host-connection.ts` — `checkHealth` token-off negotiation。
- **Modify** `spa/src/lib/host-connection.test.ts` — 更新既有「no token → auth-error」測試為新語意 + 補 token-off 200 case。

---

## Task 1: host-endpoint 純函式

**Files:**
- Create: `spa/src/lib/host-endpoint.ts`
- Test: `spa/src/lib/host-endpoint.test.ts`

**Interfaces:**
- Consumes: `HostConfig`（type-only，來自 `../stores/useHostStore`）。
- Produces:
  - `hostScheme(host: Pick<HostConfig,'scheme'>): 'http' | 'https'`
  - `deriveDaemonBase(host: Pick<HostConfig,'scheme'|'ip'|'port'>): string`
  - `deriveWsBase(host: Pick<HostConfig,'scheme'|'ip'|'port'>): string`
  - `hostEndpointKey(host: Pick<HostConfig,'scheme'|'ip'|'port'>): string`

- [ ] **Step 1: 寫失敗測試**

```ts
// spa/src/lib/host-endpoint.test.ts
import { describe, it, expect } from 'vitest'
import { hostScheme, deriveDaemonBase, deriveWsBase, hostEndpointKey } from './host-endpoint'

describe('host-endpoint', () => {
  it('scheme 缺省視為 http（向後相容）', () => {
    expect(hostScheme({ scheme: undefined })).toBe('http')
    expect(hostScheme({ scheme: 'https' })).toBe('https')
  })

  it('deriveDaemonBase：缺 scheme → http://ip:port', () => {
    expect(deriveDaemonBase({ scheme: undefined, ip: '100.64.0.2', port: 7860 }))
      .toBe('http://100.64.0.2:7860')
  })

  it('deriveDaemonBase：https + 443 省略 port', () => {
    expect(deriveDaemonBase({ scheme: 'https', ip: 'purdex.mlab.host', port: 443 }))
      .toBe('https://purdex.mlab.host')
  })

  it('deriveDaemonBase：https + 非預設 port 保留', () => {
    expect(deriveDaemonBase({ scheme: 'https', ip: 'purdex.mlab.host', port: 8443 }))
      .toBe('https://purdex.mlab.host:8443')
  })

  it('deriveWsBase：http → ws、https → wss，並套用預設 port 省略', () => {
    expect(deriveWsBase({ scheme: undefined, ip: '100.64.0.2', port: 7860 }))
      .toBe('ws://100.64.0.2:7860')
    expect(deriveWsBase({ scheme: 'https', ip: 'purdex.mlab.host', port: 443 }))
      .toBe('wss://purdex.mlab.host')
    expect(deriveWsBase({ scheme: 'https', ip: 'purdex.mlab.host', port: 8443 }))
      .toBe('wss://purdex.mlab.host:8443')
  })

  it('hostEndpointKey：含 scheme，http 與 https 不同 key', () => {
    expect(hostEndpointKey({ scheme: undefined, ip: 'h', port: 7860 })).toBe('http:h:7860')
    expect(hostEndpointKey({ scheme: 'https', ip: 'h', port: 7860 })).toBe('https:h:7860')
  })
})
```

- [ ] **Step 2: 跑測試確認失敗**

Run: `cd spa && npx vitest run src/lib/host-endpoint.test.ts`
Expected: FAIL（`Cannot find module './host-endpoint'`）。

- [ ] **Step 3: 實作**

```ts
// spa/src/lib/host-endpoint.ts
// Pure endpoint derivation for host connections. A host is an explicit
// {scheme, ip(host), port} endpoint; HTTP/WS bases and the identity key are
// derived here so the SPA connects the same way whether served by the daemon
// (same-origin) or from a hosted client. No relative URLs, no origin assumptions.
import type { HostConfig } from '../stores/useHostStore'

const DEFAULT_PORTS = { http: 80, https: 443 } as const

export function hostScheme(host: Pick<HostConfig, 'scheme'>): 'http' | 'https' {
  return host.scheme ?? 'http'
}

function portSuffix(port: number, scheme: 'http' | 'https'): string {
  return port === DEFAULT_PORTS[scheme] ? '' : `:${port}`
}

export function deriveDaemonBase(host: Pick<HostConfig, 'scheme' | 'ip' | 'port'>): string {
  const scheme = hostScheme(host)
  return `${scheme}://${host.ip}${portSuffix(host.port, scheme)}`
}

export function deriveWsBase(host: Pick<HostConfig, 'scheme' | 'ip' | 'port'>): string {
  const scheme = hostScheme(host)
  const wsScheme = scheme === 'https' ? 'wss' : 'ws'
  return `${wsScheme}://${host.ip}${portSuffix(host.port, scheme)}`
}

// Endpoint identity — any change here (including scheme) means "different
// endpoint"; callers use it to trigger reconnect and token re-auth.
export function hostEndpointKey(host: Pick<HostConfig, 'scheme' | 'ip' | 'port'>): string {
  return `${hostScheme(host)}:${host.ip}:${host.port}`
}
```

- [ ] **Step 4: 跑測試確認通過**

Run: `cd spa && npx vitest run src/lib/host-endpoint.test.ts`
Expected: PASS（6 tests）。

- [ ] **Step 5: Commit**

```bash
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/web-version
git add spa/src/lib/host-endpoint.ts spa/src/lib/host-endpoint.test.ts
git commit -m "feat(host): pure endpoint derivation with scheme support (P0-a)"
```

---

## Task 2: HostConfig.scheme + store 委派

**Files:**
- Modify: `spa/src/stores/useHostStore.ts`
- Test: `spa/src/stores/useHostStore.endpoint.test.ts`

**Interfaces:**
- Consumes: `deriveDaemonBase`, `deriveWsBase`（Task 1）。
- Produces: `HostConfig.scheme?: 'http' | 'https'`；`addHost` opts 與 `updateHost` updates 皆接受 `scheme`。

- [ ] **Step 1: 寫失敗測試**

```ts
// spa/src/stores/useHostStore.endpoint.test.ts
import { describe, it, expect, beforeEach } from 'vitest'
import { useHostStore } from './useHostStore'

describe('useHostStore endpoint derivation', () => {
  beforeEach(() => { useHostStore.getState().reset() })

  it('缺 scheme 的 host → http/ws（向後相容）', () => {
    const id = useHostStore.getState().addHost({ name: 'a', ip: '10.0.0.1', port: 7860 })
    expect(useHostStore.getState().getDaemonBase(id)).toBe('http://10.0.0.1:7860')
    expect(useHostStore.getState().getWsBase(id)).toBe('ws://10.0.0.1:7860')
  })

  it('scheme=https 的 host → https/wss，443 省略 port', () => {
    const id = useHostStore.getState().addHost({
      name: 'web', ip: 'purdex.mlab.host', port: 443, scheme: 'https',
    })
    expect(useHostStore.getState().getDaemonBase(id)).toBe('https://purdex.mlab.host')
    expect(useHostStore.getState().getWsBase(id)).toBe('wss://purdex.mlab.host')
  })

  it('updateHost 可改 scheme', () => {
    const id = useHostStore.getState().addHost({ name: 'a', ip: 'h', port: 443 })
    useHostStore.getState().updateHost(id, { scheme: 'https' })
    expect(useHostStore.getState().getDaemonBase(id)).toBe('https://h')
  })
})
```

- [ ] **Step 2: 跑測試確認失敗**

Run: `cd spa && npx vitest run src/stores/useHostStore.endpoint.test.ts`
Expected: FAIL（scheme=https 仍回 `http://...`；`updateHost` 不接受 `scheme` 造成 TS 型別錯誤或行為錯誤）。

- [ ] **Step 3: 實作**

在 `spa/src/stores/useHostStore.ts` 頂部 import 加入：

```ts
import { deriveDaemonBase, deriveWsBase } from '../lib/host-endpoint'
```

`HostConfig` 介面加入 `scheme` 欄位（放在 `port` 之後）：

```ts
export interface HostConfig {
  id: string
  name: string
  ip: string
  port: number
  // Connection scheme. Absent is treated as 'http' for backward compatibility
  // with persisted hosts. 'https' is required for browser clients over TLS
  // (e.g. purdex.mlab.host) so WS derives to wss:// and avoids mixed-content.
  scheme?: 'http' | 'https'
  // `null` is the explicit "token cleared, re-auth required" sentinel ...
  token?: string | null
  order: number
}
```

`HostState` 兩個簽章加入 `scheme`：

```ts
  addHost: (opts: { id?: string; name: string; ip: string; port: number; scheme?: 'http' | 'https'; token?: string | null }) => string
  updateHost: (hostId: string, updates: Partial<Pick<HostConfig, 'name' | 'ip' | 'port' | 'scheme' | 'token'>>) => void
```

`getDaemonBase` / `getWsBase` 改為委派（保留 fallback）：

```ts
      getDaemonBase: (hostId) => {
        const host = get().hosts[hostId]
        if (host) return deriveDaemonBase(host)
        const fallbackId = get().activeHostId ?? get().hostOrder[0]
        const fallback = fallbackId ? get().hosts[fallbackId] : undefined
        if (!fallback) return 'http://127.0.0.1:7860'
        return deriveDaemonBase(fallback)
      },

      getWsBase: (hostId) => {
        const host = get().hosts[hostId]
        if (host) return deriveWsBase(host)
        const fallbackId = get().activeHostId ?? get().hostOrder[0]
        const fallback = fallbackId ? get().hosts[fallbackId] : undefined
        if (!fallback) return 'ws://127.0.0.1:7860'
        return deriveWsBase(fallback)
      },
```

（`addHost` 內 `const { id: _discardId, ...restOpts } = opts` 已會把 `scheme` 帶進 `restOpts` → host，無需再改實作。）

- [ ] **Step 4: 跑測試確認通過**

Run: `cd spa && npx vitest run src/stores/useHostStore.endpoint.test.ts`
Expected: PASS（3 tests）。

- [ ] **Step 5: Commit**

```bash
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/web-version
git add spa/src/stores/useHostStore.ts spa/src/stores/useHostStore.endpoint.test.ts
git commit -m "feat(host): HostConfig.scheme + store endpoint delegation (P0-a)"
```

---

## Task 3: configKey / endpoint-identity 全面改用 hostEndpointKey

**Files:**
- Modify: `spa/src/hooks/useMultiHostEventWs.ts`
- Modify: `spa/src/components/MemoryMonitorPage.tsx`
- Modify: `spa/src/lib/sync/contributors/hosts.ts`
- Test: `spa/src/lib/sync/contributors/hosts.test.ts`

**Interfaces:**
- Consumes: `hostEndpointKey`（Task 1）。
- Produces: `mergeHostsPreservingTokens`（改為 `export`）。

- [ ] **Step 1: 寫失敗測試（scheme 變更 → token 重置）**

先在 `spa/src/lib/sync/contributors/hosts.ts` 把 `mergeHostsPreservingTokens` 前面加 `export`（否則測試無法 import）。然後：

```ts
// spa/src/lib/sync/contributors/hosts.test.ts
import { describe, it, expect } from 'vitest'
import { mergeHostsPreservingTokens } from './hosts'
import type { HostConfig } from '../../../stores/useHostStore'

function host(over: Partial<HostConfig>): HostConfig {
  return { id: 'h1', name: 'h', ip: '10.0.0.1', port: 7860, order: 0, ...over }
}

describe('mergeHostsPreservingTokens', () => {
  it('相同 endpoint → 保留現有 token', () => {
    const current = { h1: host({ token: 'keep' }) }
    const incoming = { h1: host({ token: 'incoming' }) }
    expect(mergeHostsPreservingTokens(current, incoming).h1.token).toBe('keep')
  })

  it('scheme 變更（http→https，ip/port 相同）視為換 endpoint → token 重置為 null', () => {
    const current = { h1: host({ token: 'keep' }) } // scheme 缺省 = http
    const incoming = { h1: host({ token: 'incoming', scheme: 'https' }) }
    expect(mergeHostsPreservingTokens(current, incoming).h1.token).toBeNull()
  })
})
```

- [ ] **Step 2: 跑測試確認失敗**

Run: `cd spa && npx vitest run src/lib/sync/contributors/hosts.test.ts`
Expected: FAIL（第二個 case：現況只比 ip/port，scheme 變更仍判 sameEndpoint → token 未重置）。

- [ ] **Step 3: 實作三處替換**

`spa/src/lib/sync/contributors/hosts.ts` — import 加 `import { hostEndpointKey } from '../../host-endpoint'`；`sameEndpoint` 改為：

```ts
    const sameEndpoint =
      currentHost !== undefined &&
      hostEndpointKey(currentHost) === hostEndpointKey(host)
```

`spa/src/hooks/useMultiHostEventWs.ts` — import 加 `hostEndpointKey`（與既有 `hostWsUrl` 同 import 行或新增一行 `import { hostEndpointKey } from '../lib/host-endpoint'`）。兩處：

第 27-28 行（hosts 依賴 memo key）：
```ts
      return h ? `${id}:${hostEndpointKey(h)}` : id
```

第 53 行（per-host configKey）：
```ts
      const configKey = hostEndpointKey(host)
```

並把第 21 行註解 `configKey: string // "ip:port"` 更新為 `// hostEndpointKey`。

`spa/src/components/MemoryMonitorPage.tsx` — import 加 `import { hostEndpointKey } from '../lib/host-endpoint'`；`snapshotHostTargetKey` 改為：

```ts
function snapshotHostTargetKey(host: HostConfig) {
  return JSON.stringify([host.id, hostEndpointKey(host), host.token ?? ''])
}
```

- [ ] **Step 4: 跑測試確認通過 + 全套回歸**

Run: `cd spa && npx vitest run src/lib/sync/contributors/hosts.test.ts`
Expected: PASS（2 tests）。
Run: `cd spa && npx vitest run`
Expected: 全綠（既有 useMultiHostEventWs / MemoryMonitor 測試不受影響）。

- [ ] **Step 5: Commit**

```bash
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/web-version
git add spa/src/lib/sync/contributors/hosts.ts spa/src/lib/sync/contributors/hosts.test.ts spa/src/hooks/useMultiHostEventWs.ts spa/src/components/MemoryMonitorPage.tsx
git commit -m "refactor(host): endpoint identity keys include scheme (P0-a)"
```

---

## Task 4: checkHealth token-off negotiation

**Files:**
- Modify: `spa/src/lib/host-connection.ts`
- Test: `spa/src/lib/host-connection.test.ts`

**Interfaces:**
- Consumes: 無新依賴。
- Produces: `checkHealth` 行為變更——無 client token 時仍嘗試 `POST /api/ws-ticket`，用 200/401 區分 token-disabled / token-required。

- [ ] **Step 1: 更新既有測試 + 補新 case**

`spa/src/lib/host-connection.test.ts`：**移除**既有 `it('Phase 1 only: no token, non-pairing → auth-error', ...)`（其斷言的舊語意已作廢），替換為以下兩個；`no token, pairing mode → connected` 保留不動：

```ts
  it('no token + token-off daemon（ws-ticket 200）→ connected + ticket', async () => {
    vi.spyOn(globalThis, 'fetch')
      .mockResolvedValueOnce(healthResponse('normal'))
      .mockResolvedValueOnce(ticketResponse('tk_off'))
    const result = await checkHealth('http://localhost:7860')
    expect(result.daemon).toBe('connected')
    expect(result.ticket).toBe('tk_off')
    expect(fetch).toHaveBeenCalledTimes(2) // 無 token 也嘗試 ws-ticket
  })

  it('no token + token-required daemon（ws-ticket 401）→ auth-error', async () => {
    vi.spyOn(globalThis, 'fetch')
      .mockResolvedValueOnce(healthResponse('normal'))
      .mockResolvedValueOnce(new Response('unauthorized', { status: 401 }))
    const result = await checkHealth('http://localhost:7860')
    expect(result.daemon).toBe('auth-error')
  })

  it('no token 時 ws-ticket 請求不帶 Authorization header', async () => {
    const spy = vi.spyOn(globalThis, 'fetch')
      .mockResolvedValueOnce(healthResponse('normal'))
      .mockResolvedValueOnce(ticketResponse('tk_off'))
    await checkHealth('http://localhost:7860')
    const secondCallInit = spy.mock.calls[1][1] as RequestInit
    expect((secondCallInit.headers ?? {})).not.toHaveProperty('Authorization')
  })
```

- [ ] **Step 2: 跑測試確認失敗**

Run: `cd spa && npx vitest run src/lib/host-connection.test.ts`
Expected: FAIL（現況無 token 時第一段就回 auth-error，只呼叫 fetch 一次，不會有 ticket）。

- [ ] **Step 3: 實作 negotiation**

`spa/src/lib/host-connection.ts` 中，將現有的：

```ts
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
```

替換為：

```ts
    // Pairing mode short-circuits: no ticket needed regardless of token.
    if (mode === 'pairing') {
      return { daemon: 'connected', tmux: 'unavailable', latency, mode }
    }

    // Attempt ws-ticket for BOTH tokened and token-off daemons. When the daemon
    // has no token configured, TokenAuth passes the unauthenticated request
    // through and issues a ticket (200). 401/503 means auth is actually
    // required/unavailable. This lets a token-off daemon (tailnet-protected)
    // connect a browser client that holds no token.
    const token = getToken?.()
    const ctrl2 = new AbortController()
    const timer2 = setTimeout(() => ctrl2.abort(), PHASE2_TIMEOUT_MS)
    try {
      const ticketRes = await fetch(`${baseUrl}/api/ws-ticket`, {
        method: 'POST',
        headers: token ? { Authorization: `Bearer ${token}` } : {},
        signal: ctrl2.signal,
      })
```

（其後 `if (ticketRes.status === 401)` / `=== 503` / `!ticketRes.ok` / 成功取 ticket 的分支維持不變。）

- [ ] **Step 4: 跑測試確認通過**

Run: `cd spa && npx vitest run src/lib/host-connection.test.ts`
Expected: PASS（含新 3 case 與既有 token-based case）。

- [ ] **Step 5: Commit**

```bash
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/web-version
git add spa/src/lib/host-connection.ts spa/src/lib/host-connection.test.ts
git commit -m "feat(host): token-off ws-ticket negotiation (P0-b)"
```

---

## 收尾驗證（全 task 完成後，主 Claude 執行）

- [ ] `cd spa && npx vitest run` — 全綠。
- [ ] `cd spa && pnpm run lint` — 無新增錯誤。
- [ ] `cd spa && pnpm run build` — 成功。

---

## Self-Review 對照 spec

- **spec §5.0 P0-a（endpoint scheme model）** → Task 1（純函式）+ Task 2（HostConfig.scheme + store）+ Task 3（endpoint identity keys）。✅
- **spec §5.0 P0-b（token-off negotiation）** → Task 4。✅
- **spec §6 防繞路**：endpoint 顯式含 scheme、無相對 URL、無 same-origin cookie → `host-endpoint.ts` 為純顯式導出，無 origin 假設。✅
- **向後相容**（scheme 缺省 http）→ Task 1/2 測試涵蓋。✅
- **P0 驗收**（可新增 `https://<hostname>` host、token 關 daemon 取得 ticket 連 WS）→ Task 2（https host 導出）+ Task 4（token-off ticket）。UI 端「新增 https host 的表單」屬 P2 首連 UX，此處以 store API 驗證。✅
- 無 placeholder；型別一致（`hostEndpointKey`/`deriveDaemonBase`/`deriveWsBase` 跨 task 命名一致）。✅
