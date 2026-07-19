# Purdex Web P2a — Host scheme UI + 首連建議 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 讓使用者能透過產品 UI 新增/編輯 `https` host，並在純瀏覽器首次開啟（無可用 host）時一鍵「連到本 daemon」——解鎖在 `https://purdex.mlab.host/` 實際連線。

**Architecture:** 沿用 P0 的 `host-endpoint` 純函式。`AddHostDialog` 手動路徑加 `scheme` 選擇、以 `deriveDaemonBase` 取代 4 處寫死 `http://`、以 `hostEndpointKey` 去重、並接受 `initial` 預填 prop。`HostPage` 在 web + https + 當前 origin 未被表示為 host 時顯示「連到本 daemon」建議，開啟預填的 `AddHostDialog`。`OverviewSection` 可編輯既有 host 的 scheme。**pairing 流程維持 http 不變**。

**Tech Stack:** React 19 / Zustand 5 / TypeScript / Vitest + @testing-library/react。

## Global Constraints

- 連線一律走**顯式 host endpoint（含 scheme）+ ticket**，origin 無關；首連建議是「把當前 origin 預填進顯式 host entity」，**非隱式同源連線、不用相對 URL**（spec §6）。
- `scheme` 缺省 = `'http'`（向後相容）。
- endpoint 相關導出/去重**一律用 `spa/src/lib/host-endpoint.ts`**（`deriveDaemonBase` / `hostEndpointKey`），**禁止**再寫 `http://${ip}:${port}` 或 `ip+port` 比較。
- **pairing 路徑維持 http**：pairing code 解碼恆為 http（LAN/tailnet）；scheme 選擇只作用於手動（token / token-off）路徑。
- web 判斷用 `getPlatformCapabilities().isElectron === false`；https 判斷用 `window.location.protocol === 'https:'`。
- 測試：`cd spa && npx vitest run`；Lint：`cd spa && pnpm run lint`；Build：`cd spa && pnpm run build`。
- 每個 task 獨立 commit。

---

## File Structure

- **Modify** `spa/src/components/hosts/AddHostDialog.tsx` — 加 `scheme` state + `<select>`；4 處 base 改 `deriveDaemonBase`；dedupe 改 `hostEndpointKey`；`addHost`/`updateHost` 帶 `scheme`；接受 `initial` 預填 prop。
- **Create** `spa/src/components/hosts/AddHostDialog.scheme.test.tsx` — scheme 新增 host + 預填 + dedupe 測試。
- **Modify** `spa/src/components/hosts/OverviewSection.tsx` — 加 scheme 編輯欄位。
- **Create** `spa/src/lib/origin-host-suggestion.ts` — 純函式：依 hosts + 當前 location 判斷是否該建議、並算出建議的 host 草稿。
- **Create** `spa/src/lib/origin-host-suggestion.test.ts` — 純函式測試。
- **Modify** `spa/src/components/HostPage.tsx` — web+https+origin 未被表示時顯示「連到本 daemon」建議 → 開啟預填 `AddHostDialog`。

---

## Task 1: AddHostDialog scheme 支援（新增/驗證/去重）

**Files:**
- Modify: `spa/src/components/hosts/AddHostDialog.tsx`
- Test: `spa/src/components/hosts/AddHostDialog.scheme.test.tsx`

**Interfaces:**
- Consumes: `deriveDaemonBase`, `hostEndpointKey`（`../../lib/host-endpoint`）。
- Produces: 新增 host 時帶入 `scheme`；existing-host dedupe 用 `hostEndpointKey`。

- [ ] **Step 1: 寫失敗測試**

```tsx
// spa/src/components/hosts/AddHostDialog.scheme.test.tsx
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { AddHostDialog } from './AddHostDialog'
import { useHostStore } from '../../stores/useHostStore'

// fetchTokenAuth 等網路呼叫在 manual 儲存路徑會被觸發；mock 成功。
vi.mock('../../lib/host-api', async (orig) => {
  const actual = await orig<typeof import('../../lib/host-api')>()
  return { ...actual, fetchTokenAuth: vi.fn().mockResolvedValue(undefined) }
})

describe('AddHostDialog — scheme', () => {
  beforeEach(() => { useHostStore.getState().reset() })

  it('手動新增 https host → 存入 scheme=https，getDaemonBase 導出 https', async () => {
    render(<AddHostDialog onClose={() => {}} />)
    // 切到手動 token 模式（露出欄位）
    fireEvent.click(screen.getByLabelText(/token/i))
    // 選 https、填 host、port、token
    fireEvent.change(screen.getByLabelText(/scheme/i), { target: { value: 'https' } })
    fireEvent.change(screen.getByLabelText(/^ip|host/i), { target: { value: 'purdex.mlab.host' } })
    fireEvent.change(screen.getByLabelText(/port/i), { target: { value: '443' } })
    fireEvent.change(screen.getByLabelText(/token/i), { target: { value: 'x'.repeat(24) } })
    fireEvent.click(screen.getByRole('button', { name: /add|save|confirm|新增|儲存/i }))

    await waitFor(() => {
      const s = useHostStore.getState()
      const id = s.hostOrder.find((i) => s.hosts[i].ip === 'purdex.mlab.host')
      expect(id).toBeTruthy()
      expect(s.hosts[id!].scheme).toBe('https')
      expect(s.getDaemonBase(id!)).toBe('https://purdex.mlab.host')
    })
  })
})
```

> 註：實際 label/role 文案以元件現況為準（i18n key `hosts.ip`/`hosts.port`/`hosts.token`/`hosts.add_host`）。實作時對齊 `getByLabelText` 的 accessible name（必要時為新 scheme `<select>` 加 `aria-label` 或關聯 `<label htmlFor>`）。

- [ ] **Step 2: 跑測試確認失敗**

Run: `cd spa && npx vitest run src/components/hosts/AddHostDialog.scheme.test.tsx`
Expected: FAIL（無 scheme select；存入無 scheme）。

- [ ] **Step 3: 實作**

於 `AddHostDialog.tsx`：

1. import 加：`import { deriveDaemonBase, hostEndpointKey } from '../../lib/host-endpoint'`。
2. state 加：`const [scheme, setScheme] = useState<'http' | 'https'>('http')`（放在 `const [port, setPort] = ...` 之後）。
3. **健康檢查 useEffect**（原 `fetch(\`http://${ip}:${portNum}/api/health\`)`）改為：
   ```ts
   const res = await fetch(`${deriveDaemonBase({ scheme, ip, port: Number(portNum) })}/api/health`)
   ```
   並把 `scheme` 加入該 effect 的依賴陣列。
4. **handleConfirm** 內兩處 `const base = \`http://${trimmedIp}:${trimmedPort || '7860'}\`` 改為：
   ```ts
   const portNum = parseInt(trimmedPort, 10) || (scheme === 'https' ? 443 : 7860)
   const base = deriveDaemonBase({ scheme, ip: trimmedIp, port: portNum })
   ```
   （把 `portNum` 宣告上移到兩個 base 之前共用；移除下方原本重複的 `const portNum = parseInt(...)`。）
5. **dedupe** 改用 endpoint identity：
   ```ts
   const draftKey = hostEndpointKey({ scheme, ip: trimmedIp, port: portNum })
   const existingId = Object.keys(existingHosts).find(
     (id) => hostEndpointKey(existingHosts[id]) === draftKey,
   )
   ```
6. **addHost** 帶入 scheme：
   ```ts
   addHost({ name: trimmedIp, ip: trimmedIp, port: portNum, scheme, token: trimmedToken || undefined })
   ```
7. **handlePair 維持 http**：pairing 路徑不動（scheme 不影響 pairing）。pairing 成功後若要一致，可 `setScheme('http')`（pairing 恆 http）——加在 `setStage('paired')` 之前。
8. **JSX**：在 ip/port 那組 `grid grid-cols-3` 前或內，加入 scheme 選擇。將該區塊改為 4 欄或在其上方加一列：
   ```tsx
   <div>
     <label htmlFor="host-scheme" className="text-xs text-text-secondary block mb-1">{t('hosts.scheme')}</label>
     <select
       id="host-scheme"
       aria-label={t('hosts.scheme')}
       value={scheme}
       onChange={(e) => setScheme(e.target.value as 'http' | 'https')}
       disabled={!fieldsEnabled}
       className="w-full bg-surface-secondary border border-border-default rounded px-3 py-2 text-sm text-text-primary disabled:opacity-50"
     >
       <option value="http">http</option>
       <option value="https">https</option>
     </select>
   </div>
   ```
9. i18n：於 `spa/src/locales/zh-TW.json` 與 `en.json` 加 `"hosts.scheme"`（分別 `"連線協定"` / `"Scheme"`）。

- [ ] **Step 4: 跑測試確認通過**

Run: `cd spa && npx vitest run src/components/hosts/AddHostDialog.scheme.test.tsx`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/web-version
git add spa/src/components/hosts/AddHostDialog.tsx spa/src/components/hosts/AddHostDialog.scheme.test.tsx spa/src/locales/zh-TW.json spa/src/locales/en.json
git commit -m "feat(hosts): scheme selector in AddHostDialog (deriveDaemonBase + hostEndpointKey dedupe) (P2a)"
```

---

## Task 2: AddHostDialog 預填 prop（initial）

**Files:**
- Modify: `spa/src/components/hosts/AddHostDialog.tsx`
- Test: `spa/src/components/hosts/AddHostDialog.scheme.test.tsx`（同檔追加）

**Interfaces:**
- Produces: `AddHostDialog` 接受選填 `initial?: { scheme?: 'http'|'https'; ip?: string; port?: string; useToken?: boolean }`，掛載時預填對應 state 並在有 `initial` 時直接進入可編輯 stage。

- [ ] **Step 1: 寫失敗測試（同檔追加）**

```tsx
  it('initial 預填 https + host → 欄位帶入且可直接編輯', () => {
    render(
      <AddHostDialog
        onClose={() => {}}
        initial={{ scheme: 'https', ip: 'purdex.mlab.host', port: '443', useToken: true }}
      />,
    )
    expect((screen.getByLabelText(/scheme/i) as HTMLSelectElement).value).toBe('https')
    expect((screen.getByLabelText(/^ip|host/i) as HTMLInputElement).value).toBe('purdex.mlab.host')
    expect((screen.getByLabelText(/port/i) as HTMLInputElement).value).toBe('443')
  })
```

- [ ] **Step 2: 跑測試確認失敗**

Run: `cd spa && npx vitest run src/components/hosts/AddHostDialog.scheme.test.tsx`
Expected: FAIL（`initial` prop 不存在，欄位為預設空/http）。

- [ ] **Step 3: 實作**

於 `AddHostDialog`：

1. `Props` 介面加：
   ```ts
   interface Props {
     onClose: () => void
     initial?: { scheme?: 'http' | 'https'; ip?: string; port?: string; useToken?: boolean }
   }
   ```
2. 函式簽章：`export function AddHostDialog({ onClose, initial }: Props) {`。
3. state 初值改用 `initial`：
   ```ts
   const [ip, setIp] = useState(initial?.ip ?? '')
   const [port, setPort] = useState(initial?.port ?? '7860')
   const [scheme, setScheme] = useState<'http' | 'https'>(initial?.scheme ?? 'http')
   const [useToken, setUseToken] = useState(initial?.useToken ?? false)
   const [stage, setStage] = useState<Stage>(initial ? 'manual' : 'idle')
   ```
   （其餘 state 不變。`initial` 存在時直接進 `manual` stage，讓欄位可編輯——`fieldsEnabled = stage === 'paired' || stage === 'manual'` 已涵蓋。）

- [ ] **Step 4: 跑測試確認通過**

Run: `cd spa && npx vitest run src/components/hosts/AddHostDialog.scheme.test.tsx`
Expected: PASS（3 tests）。

- [ ] **Step 5: Commit**

```bash
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/web-version
git add spa/src/components/hosts/AddHostDialog.tsx spa/src/components/hosts/AddHostDialog.scheme.test.tsx
git commit -m "feat(hosts): AddHostDialog initial-prefill prop (P2a)"
```

---

## Task 3: 首連建議純函式 + HostPage 接線

**Files:**
- Create: `spa/src/lib/origin-host-suggestion.ts`
- Test: `spa/src/lib/origin-host-suggestion.test.ts`
- Modify: `spa/src/components/HostPage.tsx`

**Interfaces:**
- Consumes: `hostEndpointKey`、`HostConfig`。
- Produces:
  - `shouldSuggestOriginHost(opts: { isElectron: boolean; protocol: string; hostname: string; port: string; hosts: HostConfig[] }): boolean`
  - `originHostDraft(opts: { hostname: string; port: string }): { scheme: 'https'; ip: string; port: string; useToken: boolean }`

- [ ] **Step 1: 寫失敗測試**

```ts
// spa/src/lib/origin-host-suggestion.test.ts
import { describe, it, expect } from 'vitest'
import { shouldSuggestOriginHost, originHostDraft } from './origin-host-suggestion'
import type { HostConfig } from '../stores/useHostStore'

function host(over: Partial<HostConfig>): HostConfig {
  return { id: 'h', name: 'h', ip: '10.0.0.1', port: 7860, order: 0, ...over }
}
const base = { isElectron: false, protocol: 'https:', hostname: 'purdex.mlab.host', port: '' }

describe('shouldSuggestOriginHost', () => {
  it('web + https + origin 未被任何 host 表示 → 建議', () => {
    expect(shouldSuggestOriginHost({ ...base, hosts: [host({ ip: '100.64.0.2', port: 7860 })] })).toBe(true)
  })
  it('origin 已被 https host 表示 → 不建議', () => {
    expect(shouldSuggestOriginHost({
      ...base,
      hosts: [host({ ip: 'purdex.mlab.host', port: 443, scheme: 'https' })],
    })).toBe(false)
  })
  it('Electron → 不建議', () => {
    expect(shouldSuggestOriginHost({ ...base, isElectron: true, hosts: [] })).toBe(false)
  })
  it('非 https 頁面 → 不建議', () => {
    expect(shouldSuggestOriginHost({ ...base, protocol: 'http:', hosts: [] })).toBe(false)
  })
})

describe('originHostDraft', () => {
  it('無 port → 443、scheme https、token off', () => {
    expect(originHostDraft({ hostname: 'purdex.mlab.host', port: '' }))
      .toEqual({ scheme: 'https', ip: 'purdex.mlab.host', port: '443', useToken: false })
  })
  it('顯式 port 保留', () => {
    expect(originHostDraft({ hostname: 'h', port: '8443' }).port).toBe('8443')
  })
})
```

- [ ] **Step 2: 跑測試確認失敗**

Run: `cd spa && npx vitest run src/lib/origin-host-suggestion.test.ts`
Expected: FAIL（模組不存在）。

- [ ] **Step 3: 實作純函式**

```ts
// spa/src/lib/origin-host-suggestion.ts
// Decides whether a plain-browser (non-Electron), https-served SPA should
// suggest adding the current origin as a host — because an http default host
// would be mixed-content-blocked. The suggestion is an explicit host draft,
// never an implicit same-origin connection (spec §6).
import type { HostConfig } from '../stores/useHostStore'
import { hostEndpointKey } from './host-endpoint'

export function shouldSuggestOriginHost(opts: {
  isElectron: boolean
  protocol: string
  hostname: string
  port: string
  hosts: HostConfig[]
}): boolean {
  if (opts.isElectron) return false
  if (opts.protocol !== 'https:') return false
  const draft = originHostDraft({ hostname: opts.hostname, port: opts.port })
  const draftKey = hostEndpointKey({ scheme: draft.scheme, ip: draft.ip, port: Number(draft.port) })
  return !opts.hosts.some((h) => hostEndpointKey(h) === draftKey)
}

export function originHostDraft(opts: { hostname: string; port: string }): {
  scheme: 'https'
  ip: string
  port: string
  useToken: boolean
} {
  return {
    scheme: 'https',
    ip: opts.hostname,
    port: opts.port || '443',
    useToken: false,
  }
}
```

- [ ] **Step 4: 跑測試確認通過**

Run: `cd spa && npx vitest run src/lib/origin-host-suggestion.test.ts`
Expected: PASS（6 tests）。

- [ ] **Step 5: HostPage 接線**

於 `spa/src/components/HostPage.tsx`：

1. import：
   ```ts
   import { getPlatformCapabilities } from '../lib/platform'
   import { shouldSuggestOriginHost, originHostDraft } from '../lib/origin-host-suggestion'
   ```
2. 於元件內（已有 `const [showAddHost, setShowAddHost] = useState(false)`）加：
   ```ts
   const hosts = useHostStore((s) => s.hostOrder.map((id) => s.hosts[id]))
   const suggestOrigin =
     typeof window !== 'undefined' &&
     shouldSuggestOriginHost({
       isElectron: getPlatformCapabilities().isElectron,
       protocol: window.location.protocol,
       hostname: window.location.hostname,
       port: window.location.port,
       hosts,
     })
   const [prefillOrigin, setPrefillOrigin] = useState(false)
   ```
   （`useHostStore` 若尚未 import 於此檔，補 `import { useHostStore } from '../stores/useHostStore'`。）
3. 在 host 清單/內容區上方，條件顯示建議 CTA（樣式對齊既有 banner；文案用新 i18n key）：
   ```tsx
   {suggestOrigin && (
     <div className="mx-3 mt-3 rounded border border-border-default bg-surface-secondary px-3 py-2 text-sm flex items-center justify-between gap-2">
       <span>{t('hosts.suggest_origin', { host: window.location.hostname })}</span>
       <button
         className="px-2 py-1 rounded bg-accent text-white text-xs whitespace-nowrap"
         onClick={() => { setPrefillOrigin(true); setShowAddHost(true) }}
       >
         {t('hosts.connect_this_daemon')}
       </button>
     </div>
   )}
   ```
4. 既有 `{showAddHost && <AddHostDialog onClose={() => setShowAddHost(false)} />}` 改為帶入預填：
   ```tsx
   {showAddHost && (
     <AddHostDialog
       onClose={() => { setShowAddHost(false); setPrefillOrigin(false) }}
       initial={prefillOrigin
         ? originHostDraft({ hostname: window.location.hostname, port: window.location.port })
         : undefined}
     />
   )}
   ```
5. i18n：`zh-TW.json` / `en.json` 加 `hosts.suggest_origin`（`"偵測到此頁由 {host} 的 daemon 提供，要連到它嗎？"` / `"This page is served by the daemon at {host}. Connect to it?"`）與 `hosts.connect_this_daemon`（`"連到本 daemon"` / `"Connect to this daemon"`）。

- [ ] **Step 6: Commit**

```bash
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/web-version
git add spa/src/lib/origin-host-suggestion.ts spa/src/lib/origin-host-suggestion.test.ts spa/src/components/HostPage.tsx spa/src/locales/zh-TW.json spa/src/locales/en.json
git commit -m "feat(hosts): suggest current origin as https host on web first-connect (P2a)"
```

---

## Task 4: OverviewSection scheme 編輯

**Files:**
- Modify: `spa/src/components/hosts/OverviewSection.tsx`
- Test: `spa/src/components/hosts/OverviewSection.scheme.test.tsx`（新建）

**Interfaces:**
- Consumes: `updateHost`（既有）。
- Produces: 既有 host 可於 Overview 編輯 scheme。

- [ ] **Step 1: 寫失敗測試**

```tsx
// spa/src/components/hosts/OverviewSection.scheme.test.tsx
import { describe, it, expect, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { OverviewSection } from './OverviewSection'
import { useHostStore } from '../../stores/useHostStore'

describe('OverviewSection — scheme edit', () => {
  beforeEach(() => { useHostStore.getState().reset() })

  it('切換 scheme → updateHost 生效', () => {
    const id = useHostStore.getState().addHost({ name: 'h', ip: 'purdex.mlab.host', port: 443 })
    useHostStore.getState().setActiveHost(id)
    render(<OverviewSection hostId={id} />)
    fireEvent.change(screen.getByLabelText(/scheme/i), { target: { value: 'https' } })
    expect(useHostStore.getState().hosts[id].scheme).toBe('https')
    expect(useHostStore.getState().getDaemonBase(id)).toBe('https://purdex.mlab.host')
  })
})
```

> 註：`OverviewSection` 的 props 以現況為準（若非 `hostId`，對齊實際簽章）。

- [ ] **Step 2: 跑測試確認失敗**

Run: `cd spa && npx vitest run src/components/hosts/OverviewSection.scheme.test.tsx`
Expected: FAIL（無 scheme 控制項）。

- [ ] **Step 3: 實作**

於 `OverviewSection.tsx` 的 Connection Section 內，於 IP 欄位前加入 scheme 選擇（用原生 `<select>`，因 `EditableField` 為文字型）：

```tsx
<div className="flex items-center justify-between py-1.5">
  <label htmlFor="overview-scheme" className="text-xs text-text-secondary">{t('hosts.scheme')}</label>
  <select
    id="overview-scheme"
    aria-label={t('hosts.scheme')}
    value={host.scheme ?? 'http'}
    onChange={(e) => updateHost(hostId, { scheme: e.target.value as 'http' | 'https' })}
    className="bg-surface-secondary border border-border-default rounded px-2 py-1 text-sm text-text-primary"
  >
    <option value="http">http</option>
    <option value="https">https</option>
  </select>
</div>
```

（`host`、`updateHost`、`t` 於該元件已在作用域；若 `host` 來自 store selector，確認 `host.scheme` 可讀。樣式對齊該區其他列。）

- [ ] **Step 4: 跑測試確認通過**

Run: `cd spa && npx vitest run src/components/hosts/OverviewSection.scheme.test.tsx`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/web-version
git add spa/src/components/hosts/OverviewSection.tsx spa/src/components/hosts/OverviewSection.scheme.test.tsx
git commit -m "feat(hosts): edit host scheme in OverviewSection (P2a)"
```

---

## 收尾驗證（全 task 完成後，主 Claude 執行）

- [ ] `cd spa && npx vitest run` — 全綠。
- [ ] `cd spa && pnpm run lint` — 無新增錯誤。
- [ ] `cd spa && pnpm run build` — 成功。

---

## Self-Review 對照 spec

- **spec §5.2 host 端點 scheme UI（承接 P0）** → Task 1（AddHostDialog scheme + deriveDaemonBase + hostEndpointKey dedupe）+ Task 4（OverviewSection scheme edit）。✅
- **spec §5.2 首連 UX（預填當前 origin、顯式 host entity、非隱式同源）** → Task 2（initial 預填）+ Task 3（`shouldSuggestOriginHost`/`originHostDraft` + HostPage CTA）。✅
- **spec §5.2 pairing 維持 http** → Task 1 handlePair 不動、scheme 只作用手動路徑。✅
- **spec §6 防繞路** → 首連建議為顯式 host draft（scheme+hostname+port），非相對 URL、非同源 cookie。✅
- 無 placeholder；`scheme`/`deriveDaemonBase`/`hostEndpointKey`/`originHostDraft` 命名跨 task 一致。✅
- **UI 文案/label** 依 i18n key，實作時對齊 accessible name 供測試 `getByLabelText`。
