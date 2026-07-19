# Purdex Web P2b — 可安裝 PWA 殼 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 讓 web 版可安裝成 standalone app（桌面/手機加到主畫面），並保證重新部署後不會被 service worker 卡在舊版 SPA。

**Architecture:** manifest.json + icons + installability metadata **已存在**（`spa/public/manifest.json`、`spa/public/icons/*`、`index.html` head）。本 phase 只補**唯一缺口 service worker**：手刻極簡 **network-first、版本感知** SW（`spa/public/sw.js`），加註冊模組 `register-sw.ts`（web-only、更新時自動 reload 一次），wire 進 `main.tsx`。**不引入 vite-plugin-pwa/workbox**（契合「保守最小快取」、避免版本偏移陷阱與新依賴）。

**Tech Stack:** Service Worker API / Cache API / Vite（`public/` 靜態複製）/ TypeScript / Vitest。

## Global Constraints

- SW **network-first**：導覽（navigation）與靜態資產一律先打網路，僅離線時回退快取；**絕不快取 `/api/*` 或 `/ws/*`**（直接放行網路），確保 SPA 與 daemon 版本對齊。
- SW `skipWaiting()` + `clients.claim()`：新版即接管；activate 時清除舊 cache（版本感知）。
- client 端偵測 SW 更新接管（`controllerchange`）→ **reload 一次**，避免舊 bundle 與新 daemon 不對齊；但**首次取得控制權（無前一 controller）不 reload**。
- **僅 web 註冊**：Electron（`isElectron`）與非 http/https protocol 不註冊 SW（`app:`/`file:` 跳過）。
- SW 檔由 daemon 靜態託管（P1 handler 服務 `/sw.js`，GET、dist 根）；scope `/`。
- 測試：`cd spa && npx vitest run`；Lint：`cd spa && pnpm run lint`；Build：`cd spa && pnpm run build`。
- 每個 task 獨立 commit。

---

## File Structure

- **Create** `spa/src/lib/register-sw.ts` — 純 `shouldRegisterServiceWorker` / `shouldReloadOnControllerChange` + `registerServiceWorker()` glue。
- **Create** `spa/src/lib/register-sw.test.ts` — 純函式單元測試。
- **Create** `spa/public/sw.js` — 極簡 network-first、版本感知 service worker（純 JS，Vite 複製到 dist 根）。
- **Modify** `spa/src/main.tsx` — bootstrap 呼叫 `registerServiceWorker()`。

---

## Task 1: register-sw 純函式守衛 + 測試

**Files:**
- Create: `spa/src/lib/register-sw.ts`
- Test: `spa/src/lib/register-sw.test.ts`

**Interfaces:**
- Produces:
  - `shouldRegisterServiceWorker(opts: { isElectron: boolean; protocol: string; hasServiceWorker: boolean }): boolean`
  - `shouldReloadOnControllerChange(hadController: boolean): boolean`

- [ ] **Step 1: 寫失敗測試**

```ts
// spa/src/lib/register-sw.test.ts
import { describe, it, expect } from 'vitest'
import { shouldRegisterServiceWorker, shouldReloadOnControllerChange } from './register-sw'

describe('shouldRegisterServiceWorker', () => {
  const base = { isElectron: false, protocol: 'https:', hasServiceWorker: true }
  it('web + https + SW 可用 → 註冊', () => {
    expect(shouldRegisterServiceWorker(base)).toBe(true)
  })
  it('http（localhost dev）→ 註冊（瀏覽器只在 secure context 生效，失敗會被 catch）', () => {
    expect(shouldRegisterServiceWorker({ ...base, protocol: 'http:' })).toBe(true)
  })
  it('Electron → 不註冊', () => {
    expect(shouldRegisterServiceWorker({ ...base, isElectron: true })).toBe(false)
  })
  it('app: protocol（Electron bundled）→ 不註冊', () => {
    expect(shouldRegisterServiceWorker({ ...base, protocol: 'app:' })).toBe(false)
  })
  it('navigator 無 serviceWorker → 不註冊', () => {
    expect(shouldRegisterServiceWorker({ ...base, hasServiceWorker: false })).toBe(false)
  })
})

describe('shouldReloadOnControllerChange', () => {
  it('頁面原本已被控制（更新接管）→ reload', () => {
    expect(shouldReloadOnControllerChange(true)).toBe(true)
  })
  it('首次取得控制權（無前一 controller）→ 不 reload', () => {
    expect(shouldReloadOnControllerChange(false)).toBe(false)
  })
})
```

- [ ] **Step 2: 跑測試確認失敗**

Run: `cd spa && npx vitest run src/lib/register-sw.test.ts`
Expected: FAIL（模組不存在）。

- [ ] **Step 3: 實作純函式（含 glue，glue 不在本 task 測）**

```ts
// spa/src/lib/register-sw.ts
// Registers the web PWA service worker. Web-only: skipped in Electron and on
// non-http(s) protocols. On an updated SW taking control, reloads once so the
// running SPA can't drift from the newly-served bundle. Pure decision helpers
// are unit-tested; the navigator glue is thin and reviewed.
import { getPlatformCapabilities } from './platform'

export function shouldRegisterServiceWorker(opts: {
  isElectron: boolean
  protocol: string
  hasServiceWorker: boolean
}): boolean {
  if (opts.isElectron) return false
  if (!opts.hasServiceWorker) return false
  return opts.protocol === 'https:' || opts.protocol === 'http:'
}

// Only reload when an updated SW takes over a page that already had a
// controller. The first-ever control acquisition (no prior controller) must
// NOT reload, or every first load would refresh once.
export function shouldReloadOnControllerChange(hadController: boolean): boolean {
  return hadController
}

export function registerServiceWorker(): void {
  if (typeof window === 'undefined' || typeof navigator === 'undefined') return
  const ok = shouldRegisterServiceWorker({
    isElectron: getPlatformCapabilities().isElectron,
    protocol: window.location.protocol,
    hasServiceWorker: 'serviceWorker' in navigator,
  })
  if (!ok) return

  const hadController = !!navigator.serviceWorker.controller
  let reloading = false
  navigator.serviceWorker.addEventListener('controllerchange', () => {
    if (reloading) return
    if (!shouldReloadOnControllerChange(hadController)) return
    reloading = true
    window.location.reload()
  })
  navigator.serviceWorker.register('/sw.js').catch(() => {
    /* registration best-effort; non-secure contexts (e.g. http dev) will reject */
  })
}
```

- [ ] **Step 4: 跑測試確認通過**

Run: `cd spa && npx vitest run src/lib/register-sw.test.ts`
Expected: PASS（7 tests）。

- [ ] **Step 5: Commit**

```bash
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/web-version
git add spa/src/lib/register-sw.ts spa/src/lib/register-sw.test.ts
git commit -m "feat(pwa): service-worker registration guards + web-only reload-on-update (P2b)"
```

---

## Task 2: service worker + bootstrap 接線

**Files:**
- Create: `spa/public/sw.js`
- Modify: `spa/src/main.tsx`

**Interfaces:**
- Consumes: `registerServiceWorker`（Task 1）。

- [ ] **Step 1: 建立 service worker**

```js
// spa/public/sw.js
// Purdex web PWA service worker — minimal, network-first, version-aware.
// Purpose: installability + courteous offline shell load. It NEVER caches API
// or WS traffic and always prefers the network, so the SPA stays version-
// matched to the daemon (no stale bundle). Bump CACHE to invalidate old shells.
const CACHE = 'purdex-shell-v1'
const SHELL = '/'

self.addEventListener('install', (event) => {
  self.skipWaiting()
  event.waitUntil(
    caches.open(CACHE).then((c) => c.add(SHELL)).catch(() => {}),
  )
})

self.addEventListener('activate', (event) => {
  event.waitUntil(
    caches
      .keys()
      .then((keys) => Promise.all(keys.filter((k) => k !== CACHE).map((k) => caches.delete(k))))
      .then(() => self.clients.claim()),
  )
})

self.addEventListener('fetch', (event) => {
  const req = event.request
  const url = new URL(req.url)

  // Only same-origin GET. Never touch API/WS — let them hit the network directly.
  if (req.method !== 'GET' || url.origin !== self.location.origin) return
  if (url.pathname.startsWith('/api/') || url.pathname.startsWith('/ws/')) return

  // Navigations: network-first; fall back to the cached shell only when offline.
  if (req.mode === 'navigate') {
    event.respondWith(
      fetch(req)
        .then((res) => {
          const clone = res.clone()
          caches.open(CACHE).then((c) => c.put(SHELL, clone)).catch(() => {})
          return res
        })
        .catch(() => caches.match(SHELL).then((r) => r || Response.error())),
    )
    return
  }

  // Static assets: network-first with opportunistic cache for offline shell.
  event.respondWith(
    fetch(req)
      .then((res) => {
        if (res.ok) {
          const clone = res.clone()
          caches.open(CACHE).then((c) => c.put(req, clone)).catch(() => {})
        }
        return res
      })
      .catch(() => caches.match(req).then((r) => r || Response.error())),
  )
})
```

- [ ] **Step 2: 接線 bootstrap**

於 `spa/src/main.tsx`，import 加：

```ts
import { registerServiceWorker } from './lib/register-sw'
```

在 bootstrap 區（`registerBuiltinModules()` 之後、`createRoot(...)` 之前）加：

```ts
// Web PWA: register the service worker (no-op in Electron / non-http(s)).
registerServiceWorker()
```

- [ ] **Step 3: 驗證測試 + build**

Run: `cd spa && npx vitest run`
Expected: 全綠（`registerServiceWorker()` 在 jsdom 測試環境會因 `getPlatformCapabilities().isElectron` 與 `'serviceWorker' in navigator` 判斷而 no-op；不影響既有測試）。
Run: `cd spa && pnpm run build`
Expected: 成功；`sw.js` 出現在 `spa/dist/sw.js`（Vite 複製 `public/`）。

- [ ] **Step 4: Commit**

```bash
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/web-version
git add spa/public/sw.js spa/src/main.tsx
git commit -m "feat(pwa): network-first version-aware service worker + bootstrap wiring (P2b)"
```

---

## 收尾驗證（全 task 完成後，主 Claude 執行）

- [ ] `cd spa && npx vitest run` — 全綠。
- [ ] `cd spa && pnpm run lint` — 無新增錯誤。
- [ ] `cd spa && pnpm run build` — 成功且 `dist/sw.js`、`dist/manifest.json`、`dist/icons/*` 皆在。
- [ ] **交付使用者手動驗證**（部署後）：桌面 Chrome 於 `https://purdex.mlab.host/` 應出現安裝提示；安裝後為 standalone 視窗；重新部署 SPA → reload 後為新版（SW 不卡舊）；DevTools → Application → Service Workers 應見 `sw.js` active、Network 顯示 `/api`、`/ws` 未被 SW 攔截。

---

## Self-Review 對照 spec

- **spec §5.2 可安裝 PWA 殼（manifest + 圖示 + display standalone）** → 已存在（`public/manifest.json` + icons + head），本 phase 補 SW 完成可安裝性。✅
- **spec §5.2 SW network-first / 版本對齊（skipWaiting+clientsClaim + 新版 reload + 不快取 API/WS）** → Task 2 sw.js + Task 1 reload-on-update。✅
- **spec §2.2 非目標：不做離線資料、不做 Push** → SW 僅殼 network-first、無 API/WS 快取、無 push handler。✅
- **spec §6 防繞路 / web-only** → Task 1 Electron/protocol guard；SW 不影響連線模型。✅
- 無 placeholder；`shouldRegisterServiceWorker`/`shouldReloadOnControllerChange`/`registerServiceWorker` 命名一致。✅
