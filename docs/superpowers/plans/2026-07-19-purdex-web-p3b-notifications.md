# Purdex Web P3b — Web 通知權限 + 外部連結 + focus Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 讓 web 版能實際跳出 OS 通知（補「請求權限」UX，不在載入即彈）、terminal link 開新分頁帶安全 rel、通知點擊時聚焦視窗——完成 Tier B 的通知/外部連結/focus fallback。

**Architecture:** 現況 dispatcher 已在 `Notification.permission === 'granted'` 時跳 web 通知並接 `onclick`（`useNotificationDispatcher.ts:269`），但全 app 無處請求權限。新增 `web-notifications.ts`（權限狀態 + 請求）與可關閉的 `WebNotificationPrompt` banner（web-only、`permission === 'default'` 且未關閉時顯示，使用者點擊＝gesture 請求）。`openExternalUrl` 補 `noreferrer`；`focusMyWindow` 補 web `window.focus()` fallback。

**Tech Stack:** React 19 / TypeScript / Vitest。

## Global Constraints

- **不在載入即彈**：僅顯示被動的 dismissible banner；`Notification.requestPermission()` 只在使用者點「啟用」（user gesture）時呼叫。
- **web-only**：banner 與權限請求僅在 `!isElectron`；Electron 續用 `electronAPI.showNotification`。
- **能力判斷勿用 `canNotification`**（該 flag = Electron 通知 IPC 可用），web 通知能力另判（`'Notification' in window` + permission）。
- dismissal 持久化用 localStorage（避免每次 reload 重彈）。
- 測試：`cd spa && npx vitest run`；Lint / Build 同 SPA 慣例。每 task 獨立 commit。

---

## File Structure

- **Create** `spa/src/lib/web-notifications.ts` — `webNotificationPermission()` / `requestWebNotificationPermission()` / `shouldOfferWebNotifications(...)` 純守衛。
- **Create** `spa/src/lib/web-notifications.test.ts`。
- **Create** `spa/src/components/WebNotificationPrompt.tsx` — 可關閉 banner（web-only）。
- **Create** `spa/src/components/WebNotificationPrompt.test.tsx`。
- **Modify** `spa/src/App.tsx` — 渲染 `<WebNotificationPrompt />`。
- **Modify** `spa/src/lib/terminal-link/openers/url.ts` — `window.open(uri, '_blank', 'noopener,noreferrer')`。
- **Modify** `spa/src/hooks/useNotificationDispatcher.ts` — `focusMyWindow` 的 web fallback（`window.focus()`）。
- **Modify** `spa/src/locales/zh-TW.json`, `spa/src/locales/en.json` — banner 文案 key。

---

## Task 1: web-notifications 純守衛 + 請求

**Files:**
- Create: `spa/src/lib/web-notifications.ts`, `spa/src/lib/web-notifications.test.ts`

**Interfaces:**
- Produces:
  - `webNotificationPermission(): 'unsupported' | NotificationPermission`
  - `requestWebNotificationPermission(): Promise<'unsupported' | NotificationPermission>`
  - `shouldOfferWebNotifications(opts: { isElectron: boolean; permission: 'unsupported' | NotificationPermission; dismissed: boolean }): boolean`

- [ ] **Step 1: 寫失敗測試**

```ts
// spa/src/lib/web-notifications.test.ts
import { describe, it, expect } from 'vitest'
import { shouldOfferWebNotifications } from './web-notifications'

describe('shouldOfferWebNotifications', () => {
  const base = { isElectron: false, permission: 'default' as const, dismissed: false }
  it('web + default + 未關閉 → offer', () => {
    expect(shouldOfferWebNotifications(base)).toBe(true)
  })
  it('已 granted → 不 offer', () => {
    expect(shouldOfferWebNotifications({ ...base, permission: 'granted' })).toBe(false)
  })
  it('已 denied → 不 offer', () => {
    expect(shouldOfferWebNotifications({ ...base, permission: 'denied' })).toBe(false)
  })
  it('已關閉 → 不 offer', () => {
    expect(shouldOfferWebNotifications({ ...base, dismissed: true })).toBe(false)
  })
  it('Electron → 不 offer', () => {
    expect(shouldOfferWebNotifications({ ...base, isElectron: true })).toBe(false)
  })
  it('unsupported → 不 offer', () => {
    expect(shouldOfferWebNotifications({ ...base, permission: 'unsupported' })).toBe(false)
  })
})
```

- [ ] **Step 2: 跑測試確認失敗**

Run: `cd spa && npx vitest run src/lib/web-notifications.test.ts`
Expected: FAIL（模組不存在）。

- [ ] **Step 3: 實作**

```ts
// spa/src/lib/web-notifications.ts
// Web notification capability + permission request. Kept separate from the
// Electron `canNotification` capability (which means "Electron notification IPC
// is available") — web notification availability is its own thing.
export function webNotificationPermission(): 'unsupported' | NotificationPermission {
  if (typeof window === 'undefined' || !('Notification' in window)) return 'unsupported'
  return Notification.permission
}

export async function requestWebNotificationPermission(): Promise<'unsupported' | NotificationPermission> {
  if (typeof window === 'undefined' || !('Notification' in window)) return 'unsupported'
  try {
    return await Notification.requestPermission()
  } catch {
    return Notification.permission
  }
}

// Offer the enable-notifications prompt only on web, when permission is still
// undecided ('default') and the user hasn't dismissed it.
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

- [ ] **Step 4: 跑測試確認通過**

Run: `cd spa && npx vitest run src/lib/web-notifications.test.ts`
Expected: PASS（6 tests）。

- [ ] **Step 5: Commit**

```bash
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/web-version
git add spa/src/lib/web-notifications.ts spa/src/lib/web-notifications.test.ts
git commit -m "feat(notifications): web notification permission helpers (P3b)"
```

---

## Task 2: WebNotificationPrompt banner + App 接線

**Files:**
- Create: `spa/src/components/WebNotificationPrompt.tsx`, `spa/src/components/WebNotificationPrompt.test.tsx`
- Modify: `spa/src/App.tsx`, `spa/src/locales/zh-TW.json`, `spa/src/locales/en.json`

**Interfaces:**
- Consumes: `webNotificationPermission` / `requestWebNotificationPermission` / `shouldOfferWebNotifications`、`getPlatformCapabilities`。

- [ ] **Step 1: 寫失敗測試**

```tsx
// spa/src/components/WebNotificationPrompt.test.tsx
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { WebNotificationPrompt } from './WebNotificationPrompt'

const DISMISS_KEY = 'purdex-webnotify-dismissed'

describe('WebNotificationPrompt', () => {
  beforeEach(() => {
    delete (window as unknown as { electronAPI?: unknown }).electronAPI // web
    localStorage.removeItem(DISMISS_KEY)
    Object.defineProperty(window, 'Notification', {
      configurable: true, writable: true,
      value: Object.assign(function () {}, { permission: 'default' as NotificationPermission, requestPermission: vi.fn().mockResolvedValue('granted') }),
    })
  })

  it('web + default 權限 → 顯示啟用提示', () => {
    render(<WebNotificationPrompt />)
    expect(screen.getByText(/notification|通知/i)).toBeInTheDocument()
  })

  it('點「啟用」→ 呼叫 requestPermission 並隱藏', async () => {
    render(<WebNotificationPrompt />)
    fireEvent.click(screen.getByRole('button', { name: /enable|啟用/i }))
    expect((window.Notification as unknown as { requestPermission: () => void }).requestPermission).toHaveBeenCalled()
  })

  it('點關閉 → 記住 dismissal（localStorage）且隱藏', () => {
    render(<WebNotificationPrompt />)
    fireEvent.click(screen.getByRole('button', { name: /dismiss|close|關閉|稍後/i }))
    expect(localStorage.getItem(DISMISS_KEY)).toBeTruthy()
    expect(screen.queryByText(/notification|通知/i)).not.toBeInTheDocument()
  })

  it('已 granted → 不顯示', () => {
    ;(window.Notification as unknown as { permission: string }).permission = 'granted'
    render(<WebNotificationPrompt />)
    expect(screen.queryByText(/notification|通知/i)).not.toBeInTheDocument()
  })
})
```

- [ ] **Step 2: 跑測試確認失敗**

Run: `cd spa && npx vitest run src/components/WebNotificationPrompt.test.tsx`
Expected: FAIL（模組不存在）。

- [ ] **Step 3: 實作**

```tsx
// spa/src/components/WebNotificationPrompt.tsx
import { useState } from 'react'
import { Bell, X } from '@phosphor-icons/react'
import { getPlatformCapabilities } from '../lib/platform'
import {
  webNotificationPermission,
  requestWebNotificationPermission,
  shouldOfferWebNotifications,
} from '../lib/web-notifications'
import { useI18nStore } from '../stores/useI18nStore'

const DISMISS_KEY = 'purdex-webnotify-dismissed'

export function WebNotificationPrompt() {
  const t = useI18nStore((s) => s.t)
  const [dismissed, setDismissed] = useState(() => localStorage.getItem(DISMISS_KEY) === '1')
  const [permission, setPermission] = useState(() => webNotificationPermission())

  const offer = shouldOfferWebNotifications({
    isElectron: getPlatformCapabilities().isElectron,
    permission,
    dismissed,
  })
  if (!offer) return null

  const enable = async () => {
    const result = await requestWebNotificationPermission()
    setPermission(result)
  }
  const dismiss = () => {
    localStorage.setItem(DISMISS_KEY, '1')
    setDismissed(true)
  }

  return (
    <div className="fixed bottom-3 right-3 z-50 max-w-xs rounded border border-border-default bg-surface-primary shadow-lg px-3 py-2 text-sm flex items-start gap-2">
      <Bell size={16} className="mt-0.5 shrink-0" />
      <div className="flex-1">
        <div className="mb-1">{t('notifications.enable_prompt')}</div>
        <button className="px-2 py-1 rounded bg-accent text-white text-xs" onClick={enable}>
          {t('notifications.enable')}
        </button>
      </div>
      <button aria-label={t('common.dismiss')} onClick={dismiss} className="shrink-0 text-text-muted hover:text-text-primary">
        <X size={14} />
      </button>
    </div>
  )
}
```

於 `spa/src/App.tsx` 適當處（app 根層，與其他 overlay 同級）渲染 `<WebNotificationPrompt />`，並 import。

i18n（`{{}}` 無插值）：
- `zh-TW.json`：`"notifications.enable_prompt": "啟用瀏覽器通知，在 agent 完成或需要你時提醒你。"`、`"notifications.enable": "啟用"`、`"common.dismiss": "關閉"`（若 `common.dismiss` 已存在則沿用）。
- `en.json`：`"notifications.enable_prompt": "Enable browser notifications to get alerted when an agent finishes or needs you."`、`"notifications.enable": "Enable"`、`"common.dismiss": "Dismiss"`。

- [ ] **Step 4: 跑測試通過**

Run: `cd spa && npx vitest run src/components/WebNotificationPrompt.test.tsx`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/web-version
git add spa/src/components/WebNotificationPrompt.tsx spa/src/components/WebNotificationPrompt.test.tsx spa/src/App.tsx spa/src/locales/zh-TW.json spa/src/locales/en.json
git commit -m "feat(notifications): dismissible web notification permission prompt (P3b)"
```

---

## Task 3: openExternalUrl noreferrer + focusMyWindow web fallback

**Files:**
- Modify: `spa/src/lib/terminal-link/openers/url.ts`
- Modify: `spa/src/hooks/useNotificationDispatcher.ts`
- Test: `spa/src/lib/terminal-link/openers/url.test.ts`（若存在則追加；否則沿用既有測試 pattern）

**Interfaces:** 無新介面。

- [ ] **Step 1: 寫失敗測試（url opener noreferrer）**

於既有 `url.test.ts`（若存在）追加，否則於新建測試檔驗證 web 分支帶 `noopener,noreferrer`：

```ts
it('web：window.open 帶 noopener,noreferrer', () => {
  const spy = vi.spyOn(window, 'open').mockReturnValue(null)
  const opener = createUrlOpener({ isElectron: false, openBrowserTab: () => {}, openExternal: () => {} })
  opener.open(
    { type: 'url', text: 'https://example.com' } as never,
    {} as never,
    { shiftKey: false } as never,
  )
  expect(spy).toHaveBeenCalledWith('https://example.com', '_blank', 'noopener,noreferrer')
})
```

- [ ] **Step 2: 跑測試確認失敗**

Run: `cd spa && npx vitest run src/lib/terminal-link/openers/url.test.ts`
Expected: FAIL（現況只帶 `'_blank'`）。

- [ ] **Step 3: 實作**

`url.ts`：web 分支
```ts
window.open(uri, '_blank', 'noopener,noreferrer')
```

`useNotificationDispatcher.ts`：兩處 `if (window.electronAPI?.focusMyWindow) { window.electronAPI.focusMyWindow() }`（約 381、389 行）改為：
```ts
if (window.electronAPI?.focusMyWindow) {
  window.electronAPI.focusMyWindow()
} else if (typeof window !== 'undefined') {
  window.focus()
}
```

- [ ] **Step 4: 跑測試通過 + 全套回歸**

Run: `cd spa && npx vitest run src/lib/terminal-link/openers/url.test.ts`
Expected: PASS。
Run: `cd spa && npx vitest run`
Expected: 全綠。

- [ ] **Step 5: Commit**

```bash
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/web-version
git add spa/src/lib/terminal-link/openers/url.ts spa/src/lib/terminal-link/openers/url.test.ts spa/src/hooks/useNotificationDispatcher.ts
git commit -m "feat(web): external-link noreferrer + focusMyWindow web fallback (P3b)"
```

---

## 收尾驗證（全 task 完成後，主 Claude 執行）

- [ ] `cd spa && npx vitest run` — 全綠。
- [ ] `cd spa && pnpm run lint` — 無新增錯誤。
- [ ] （build 的 tsc 有一個 pre-existing 非本 feature 的 HoverTooltip.tsx 環境型別問題，見 ledger；確認本 phase 新檔 `tsc --noEmit` 無錯即可。）

---

## Self-Review 對照 spec

- **spec §5.3 通知：請求權限 UX（不載入即彈）+ 沿用 new Notification fallback + onclick 導向** → Task 1 helper + Task 2 dismissible banner（gesture 請求）；dispatcher 既有 fallback/onclick 不動。✅
- **spec §5.3 通知能力勿用 canNotification** → `web-notifications.ts` 獨立判斷。✅
- **spec §4 Tier B：openExternalUrl → window.open(noopener,noreferrer)；focusMyWindow web fallback** → Task 3。✅
- 無 placeholder；命名一致。
