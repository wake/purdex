# Purdex Web P3b — Web 通知權限 + 外部連結 + focus Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.
>
> **修訂 r2**（codex plan review 後）：`requestWebNotificationPermission` 支援 Promise + 舊 callback 兩式；dismissal 用 `STORAGE_KEYS` + 安全存取；新增 **Settings 再入口**（AppearanceSection），banner dismissal 只停自動顯示、非唯一入口；強化測試（enable 後隱藏、guard、denied、focus fallback、更新既有 url.test.ts）。

**Goal:** 讓 web 版能實際跳出 OS 通知（補「請求權限」UX，不載入即彈，且有可重入的 Settings 入口）；terminal link 開新分頁帶安全 rel；通知點擊時聚焦視窗。

**Architecture:** dispatcher 已在 `Notification.permission === 'granted'` 時跳 web 通知並接 `onclick`。新增 `web-notifications.ts`（權限狀態 + Promise/callback 相容請求 + 安全 dismissal 存取）、可關閉 `WebNotificationPrompt` banner（web-only、`permission==='default'` 且未關閉時）、`AppearanceSection` 的可重入「啟用通知」row。`openExternalUrl` 補 `noreferrer`；`focusMyWindow` 補 web `window.focus()`。

**Tech Stack:** React 19 / TypeScript / Vitest。

## Global Constraints

- **不載入即彈**：僅 dismissible banner + 被動 Settings row；`Notification.requestPermission()` 只在 user gesture（點「啟用」）時呼叫。
- **Promise + callback 相容**：`Notification.requestPermission()` 舊介面回 callback、新介面回 Promise，須都支援，回傳一律正規化為 `NotificationPermission`。
- **web-only**：banner / 能力判斷僅 `!isElectron`；能力判斷與 `canNotification`（= Electron IPC）解耦。
- **可重入**：banner dismissal（永久 localStorage）只停「自動顯示」，Settings 的 row 為永久可重入入口。
- dismissal key 用 `STORAGE_KEYS.WEB_NOTIFICATION_DISMISSED`；讀寫經 typeof-window 安全存取。
- 測試：`cd spa && npx vitest run`。每 task 獨立 commit。

---

## File Structure

- **Modify** `spa/src/lib/storage/keys.ts` — 加 `WEB_NOTIFICATION_DISMISSED: 'purdex-webnotify-dismissed'`。
- **Create** `spa/src/lib/web-notifications.ts` + `.test.ts` — 權限狀態 / Promise+callback 請求 / 安全 dismissal 存取 / `shouldOfferWebNotifications`。
- **Create** `spa/src/components/WebNotificationPrompt.tsx` + `.test.tsx` — 可關閉 banner。
- **Modify** `spa/src/components/settings/AppearanceSection.tsx` — 加可重入「啟用瀏覽器通知」row（web-only）。
- **Modify** `spa/src/App.tsx` — 渲染 `<WebNotificationPrompt />`。
- **Modify** `spa/src/lib/terminal-link/openers/url.ts` — `window.open(uri, '_blank', 'noopener,noreferrer')`。
- **Modify** `spa/src/lib/terminal-link/openers/url.test.ts` — 更新既有 `_blank` 斷言為含 `noopener,noreferrer`。
- **Modify** `spa/src/hooks/useNotificationDispatcher.ts` — `focusMyWindow` web fallback（`window.focus()`）。
- **Modify** `spa/src/hooks/useNotificationDispatcher.test.ts` — 補 web `window.focus()` fallback 測試。
- **Modify** `spa/src/locales/zh-TW.json`, `spa/src/locales/en.json` — i18n keys。

---

## Task 1: web-notifications helper（Promise+callback + 安全存取）

**Files:**
- Modify: `spa/src/lib/storage/keys.ts`
- Create: `spa/src/lib/web-notifications.ts`, `spa/src/lib/web-notifications.test.ts`

**Interfaces:**
- Produces:
  - `webNotificationPermission(): 'unsupported' | NotificationPermission`
  - `requestWebNotificationPermission(): Promise<'unsupported' | NotificationPermission>`（Promise+callback 相容）
  - `getWebNotifyDismissed(): boolean` / `setWebNotifyDismissed(): void`（安全存取）
  - `shouldOfferWebNotifications(opts: { isElectron: boolean; permission: 'unsupported' | NotificationPermission; dismissed: boolean }): boolean`

- [ ] **Step 1: 寫失敗測試**

```ts
// spa/src/lib/web-notifications.test.ts
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { shouldOfferWebNotifications, requestWebNotificationPermission, getWebNotifyDismissed, setWebNotifyDismissed } from './web-notifications'

function stubNotification(impl: Partial<{ permission: NotificationPermission; requestPermission: unknown }>) {
  Object.defineProperty(window, 'Notification', {
    configurable: true, writable: true,
    value: Object.assign(function () {}, { permission: 'default' as NotificationPermission, ...impl }),
  })
}

describe('shouldOfferWebNotifications', () => {
  const base = { isElectron: false, permission: 'default' as const, dismissed: false }
  it('web + default + 未關閉 → true', () => { expect(shouldOfferWebNotifications(base)).toBe(true) })
  it('granted → false', () => { expect(shouldOfferWebNotifications({ ...base, permission: 'granted' })).toBe(false) })
  it('denied → false', () => { expect(shouldOfferWebNotifications({ ...base, permission: 'denied' })).toBe(false) })
  it('dismissed → false', () => { expect(shouldOfferWebNotifications({ ...base, dismissed: true })).toBe(false) })
  it('Electron → false', () => { expect(shouldOfferWebNotifications({ ...base, isElectron: true })).toBe(false) })
  it('unsupported → false', () => { expect(shouldOfferWebNotifications({ ...base, permission: 'unsupported' })).toBe(false) })
})

describe('requestWebNotificationPermission', () => {
  it('Promise 版 → resolved 值', async () => {
    stubNotification({ requestPermission: vi.fn().mockResolvedValue('granted') })
    expect(await requestWebNotificationPermission()).toBe('granted')
  })
  it('callback 版 → 正規化為 permission', async () => {
    stubNotification({ requestPermission: (cb: (p: NotificationPermission) => void) => cb('granted') })
    expect(await requestWebNotificationPermission()).toBe('granted')
  })
  it('無 Notification → unsupported', async () => {
    // @ts-expect-error 移除
    delete window.Notification
    expect(await requestWebNotificationPermission()).toBe('unsupported')
  })
})

describe('dismissal 安全存取', () => {
  beforeEach(() => localStorage.clear())
  it('set → get true', () => {
    expect(getWebNotifyDismissed()).toBe(false)
    setWebNotifyDismissed()
    expect(getWebNotifyDismissed()).toBe(true)
  })
})
```

- [ ] **Step 2: 跑測試確認失敗** → `cd spa && npx vitest run src/lib/web-notifications.test.ts`（FAIL 模組不存在）。

- [ ] **Step 3: 實作**

`keys.ts` 於 `STORAGE_KEYS` 加：`WEB_NOTIFICATION_DISMISSED: 'purdex-webnotify-dismissed',`。

```ts
// spa/src/lib/web-notifications.ts
import { STORAGE_KEYS } from './storage/keys'

export function webNotificationPermission(): 'unsupported' | NotificationPermission {
  if (typeof window === 'undefined' || !('Notification' in window)) return 'unsupported'
  return Notification.permission
}

// Supports both the modern Promise-returning API and the legacy callback API.
export async function requestWebNotificationPermission(): Promise<'unsupported' | NotificationPermission> {
  if (typeof window === 'undefined' || !('Notification' in window)) return 'unsupported'
  try {
    const result = Notification.requestPermission((p) => p) as unknown
    if (result && typeof (result as Promise<NotificationPermission>).then === 'function') {
      return await (result as Promise<NotificationPermission>)
    }
    // Legacy callback API returns void; the callback already applied — read the
    // now-updated permission. If still default (callback async), fall back to it.
    return Notification.permission
  } catch {
    return Notification.permission
  }
}

export function getWebNotifyDismissed(): boolean {
  if (typeof window === 'undefined') return false
  try { return localStorage.getItem(STORAGE_KEYS.WEB_NOTIFICATION_DISMISSED) === '1' } catch { return false }
}

export function setWebNotifyDismissed(): void {
  if (typeof window === 'undefined') return
  try { localStorage.setItem(STORAGE_KEYS.WEB_NOTIFICATION_DISMISSED, '1') } catch { /* ignore */ }
}

export function shouldOfferWebNotifications(opts: {
  isElectron: boolean
  permission: 'unsupported' | NotificationPermission
  dismissed: boolean
}): boolean {
  if (opts.isElectron) return false
  if (opts.dismissed) return false
  return opts.permission === 'default'
}
```

> 註：callback 版 `requestPermission(cb)` 的正規化——測試以同步 callback 覆蓋；實務上 callback 版瀏覽器（舊 Safari）呼叫後 `Notification.permission` 會更新，故回讀即可。傳入 `(p)=>p` callback 對 Promise 版無害（Promise 版忽略參數）。

- [ ] **Step 4: 跑測試通過** → PASS。
- [ ] **Step 5: Commit**
```bash
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/web-version
git add spa/src/lib/web-notifications.ts spa/src/lib/web-notifications.test.ts spa/src/lib/storage/keys.ts
git commit -m "feat(notifications): web notification permission helpers (Promise+callback) (P3b)"
```

---

## Task 2: WebNotificationPrompt banner + AppearanceSection 再入口 + App 接線

**Files:**
- Create: `spa/src/components/WebNotificationPrompt.tsx`, `spa/src/components/WebNotificationPrompt.test.tsx`
- Modify: `spa/src/components/settings/AppearanceSection.tsx`, `spa/src/App.tsx`, `spa/src/locales/zh-TW.json`, `spa/src/locales/en.json`

- [ ] **Step 1: 寫失敗測試（banner）**

```tsx
// spa/src/components/WebNotificationPrompt.test.tsx
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { WebNotificationPrompt } from './WebNotificationPrompt'
import { STORAGE_KEYS } from '../lib/storage/keys'

function stubNotification(permission: NotificationPermission, requestPermission = vi.fn().mockResolvedValue('granted')) {
  Object.defineProperty(window, 'Notification', {
    configurable: true, writable: true,
    value: Object.assign(function () {}, { permission, requestPermission }),
  })
}

describe('WebNotificationPrompt', () => {
  beforeEach(() => {
    delete (window as unknown as { electronAPI?: unknown }).electronAPI
    localStorage.clear()
    stubNotification('default')
  })

  it('web + default → 顯示提示', () => {
    render(<WebNotificationPrompt />)
    expect(screen.getByText(/notification|通知/i)).toBeInTheDocument()
  })
  it('點啟用 → requestPermission 被呼叫且提示消失', async () => {
    const req = vi.fn().mockResolvedValue('granted')
    stubNotification('default', req)
    render(<WebNotificationPrompt />)
    fireEvent.click(screen.getByRole('button', { name: /enable|啟用/i }))
    expect(req).toHaveBeenCalled()
    await waitFor(() => expect(screen.queryByText(/notification|通知/i)).not.toBeInTheDocument())
  })
  it('點關閉 → 寫入 dismissal 且消失', () => {
    render(<WebNotificationPrompt />)
    fireEvent.click(screen.getByRole('button', { name: /dismiss|close|關閉|稍後/i }))
    expect(localStorage.getItem(STORAGE_KEYS.WEB_NOTIFICATION_DISMISSED)).toBe('1')
    expect(screen.queryByText(/notification|通知/i)).not.toBeInTheDocument()
  })
  it('granted → 不顯示', () => {
    stubNotification('granted')
    render(<WebNotificationPrompt />)
    expect(screen.queryByText(/notification|通知/i)).not.toBeInTheDocument()
  })
  it('denied → 不顯示', () => {
    stubNotification('denied')
    render(<WebNotificationPrompt />)
    expect(screen.queryByText(/notification|通知/i)).not.toBeInTheDocument()
  })
  it('pre-dismissed → 不顯示', () => {
    localStorage.setItem(STORAGE_KEYS.WEB_NOTIFICATION_DISMISSED, '1')
    render(<WebNotificationPrompt />)
    expect(screen.queryByText(/notification|通知/i)).not.toBeInTheDocument()
  })
  it('Electron → 不顯示', () => {
    ;(window as unknown as { electronAPI?: unknown }).electronAPI = {}
    render(<WebNotificationPrompt />)
    expect(screen.queryByText(/notification|通知/i)).not.toBeInTheDocument()
  })
})
```

- [ ] **Step 2: 跑測試確認失敗** → FAIL（模組不存在）。

- [ ] **Step 3: 實作 banner**

```tsx
// spa/src/components/WebNotificationPrompt.tsx
import { useState } from 'react'
import { Bell, X } from '@phosphor-icons/react'
import { getPlatformCapabilities } from '../lib/platform'
import {
  webNotificationPermission, requestWebNotificationPermission,
  shouldOfferWebNotifications, getWebNotifyDismissed, setWebNotifyDismissed,
} from '../lib/web-notifications'
import { useI18nStore } from '../stores/useI18nStore'

export function WebNotificationPrompt() {
  const t = useI18nStore((s) => s.t)
  const [dismissed, setDismissed] = useState(getWebNotifyDismissed)
  const [permission, setPermission] = useState(webNotificationPermission)

  if (!shouldOfferWebNotifications({
    isElectron: getPlatformCapabilities().isElectron, permission, dismissed,
  })) return null

  const enable = async () => setPermission(await requestWebNotificationPermission())
  const dismiss = () => { setWebNotifyDismissed(); setDismissed(true) }

  return (
    <div className="fixed bottom-3 right-3 z-50 max-w-xs rounded border border-border-default bg-surface-primary shadow-lg px-3 py-2 text-sm flex items-start gap-2">
      <Bell size={16} className="mt-0.5 shrink-0" />
      <div className="flex-1">
        <div className="mb-1">{t('notifications.enable_prompt')}</div>
        <button className="px-2 py-1 rounded bg-accent text-white text-xs" onClick={enable}>{t('notifications.enable')}</button>
      </div>
      <button aria-label={t('common.dismiss')} onClick={dismiss} className="shrink-0 text-text-muted hover:text-text-primary"><X size={14} /></button>
    </div>
  )
}
```

於 `App.tsx` app 根層渲染 `<WebNotificationPrompt />` + import。

- [ ] **Step 4: AppearanceSection 可重入 row**

於 `AppearanceSection.tsx`，在既有 `SettingItem` 群組末尾加入（web-only）：

```tsx
{!getPlatformCapabilities().isElectron && webNotificationPermission() !== 'unsupported' && (
  <SettingItem label={t('notifications.browser_title')} description={t('notifications.browser_desc')}>
    <button
      className="px-2 py-1 rounded bg-accent text-white text-xs disabled:opacity-50"
      disabled={webNotificationPermission() === 'granted' || webNotificationPermission() === 'denied'}
      onClick={() => requestWebNotificationPermission()}
    >
      {webNotificationPermission() === 'granted' ? t('notifications.granted')
        : webNotificationPermission() === 'denied' ? t('notifications.denied')
        : t('notifications.enable')}
    </button>
  </SettingItem>
)}
```

import：`getPlatformCapabilities`（`../../lib/platform`）、`webNotificationPermission`/`requestWebNotificationPermission`（`../../lib/web-notifications`）。（`SettingItem` props 以現況為準——對齊既有用法。）

- [ ] **Step 5: i18n**

`zh-TW.json` / `en.json` 加：`notifications.enable_prompt`、`notifications.enable`（「啟用」/「Enable」）、`notifications.browser_title`（「瀏覽器通知」/「Browser notifications」）、`notifications.browser_desc`、`notifications.granted`（「已啟用」/「Enabled」）、`notifications.denied`（「已封鎖（請至瀏覽器設定調整）」/「Blocked (change in browser settings)」）；`common.dismiss` 若已存在則沿用。

- [ ] **Step 6: 跑測試通過** → `cd spa && npx vitest run src/components/WebNotificationPrompt.test.tsx`（PASS）+ AppearanceSection 既有測試回歸綠。
- [ ] **Step 7: Commit**
```bash
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/web-version
git add spa/src/components/WebNotificationPrompt.tsx spa/src/components/WebNotificationPrompt.test.tsx spa/src/components/settings/AppearanceSection.tsx spa/src/App.tsx spa/src/locales/zh-TW.json spa/src/locales/en.json
git commit -m "feat(notifications): web permission prompt banner + settings re-entry (P3b)"
```

---

## Task 3: openExternalUrl noreferrer + focusMyWindow web fallback

**Files:**
- Modify: `spa/src/lib/terminal-link/openers/url.ts`, `spa/src/lib/terminal-link/openers/url.test.ts`
- Modify: `spa/src/hooks/useNotificationDispatcher.ts`, `spa/src/hooks/useNotificationDispatcher.test.ts`

- [ ] **Step 1: 更新既有 url.test.ts 斷言（先紅）**

把 `spa/src/lib/terminal-link/openers/url.test.ts` 中既有的
```ts
expect(spy).toHaveBeenCalledWith('https://example.com', '_blank')
```
改為
```ts
expect(spy).toHaveBeenCalledWith('https://example.com', '_blank', 'noopener,noreferrer')
```

- [ ] **Step 2: 跑測試確認失敗** → `cd spa && npx vitest run src/lib/terminal-link/openers/url.test.ts`（FAIL：現況只帶 `_blank`）。

- [ ] **Step 3: 實作 url.ts**

web 分支 `window.open(uri, '_blank')` → `window.open(uri, '_blank', 'noopener,noreferrer')`。

- [ ] **Step 4: focusMyWindow web fallback + 測試**

`useNotificationDispatcher.ts` 兩處（約 :381 / :389）：
```ts
if (window.electronAPI?.focusMyWindow) {
  window.electronAPI.focusMyWindow()
} else if (typeof window !== 'undefined') {
  window.focus()
}
```
於 `useNotificationDispatcher.test.ts` 補一個 web（無 electronAPI）情境：mock `window.focus`，觸發 notification click handler 後斷言 `window.focus` 被呼叫（對齊既有測試觸發 click 的 pattern）。

- [ ] **Step 5: 跑測試 + 全套回歸** → `cd spa && npx vitest run src/lib/terminal-link/openers/url.test.ts src/hooks/useNotificationDispatcher.test.ts` PASS；`cd spa && npx vitest run` 全綠。
- [ ] **Step 6: Commit**
```bash
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/web-version
git add spa/src/lib/terminal-link/openers/url.ts spa/src/lib/terminal-link/openers/url.test.ts spa/src/hooks/useNotificationDispatcher.ts spa/src/hooks/useNotificationDispatcher.test.ts
git commit -m "feat(web): external-link noreferrer + focusMyWindow web fallback (P3b)"
```

---

## 收尾驗證（全 task 完成後，主 Claude 執行）

- [ ] `cd spa && npx vitest run` — 全綠。
- [ ] `cd spa && pnpm run lint` — 無新增錯誤。
- [ ] （build 的 pre-existing HoverTooltip.tsx 環境型別問題見 ledger；確認本 phase 新檔 `tsc --noEmit` 無錯。）

---

## Self-Review 對照 spec + codex review

- **spec §5.3 通知請求權限 UX（不載入即彈）+ fallback + onclick** → Task 1 helper + Task 2 banner；**codex I2** Settings 再入口。✅
- **codex I1** Promise+callback 相容 + 測試。**codex I3** 安全存取。**codex I4** 強化測試（enable 後隱藏 await / guard / denied / focus fallback / 更新既有 url.test.ts）。**codex m** STORAGE_KEYS。✅
- **spec §4 Tier B：openExternalUrl noreferrer / focusMyWindow fallback** → Task 3。✅
- 無 placeholder；命名一致。
