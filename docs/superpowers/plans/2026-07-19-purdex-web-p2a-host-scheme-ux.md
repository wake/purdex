# Purdex Web P2a — Host scheme UI + 首連建議 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.
>
> **修訂 r2**（codex plan review 後）：新增**第三條 token-off 直接新增 save path**（origin 首連需要，非 pairing/非 token API）；pairing confirm 固定 http、scheme selector 僅在手動 stage 顯示；health 空 port 依 scheme 取預設；**TokenField 納入**（改 deriveDaemonBase）；測試改用既有 placeholder 查詢；首連建議條件改「無任何 https host」；i18n 插值 `{{host}}`；補 HostPage integration test。

**Goal:** 讓使用者能透過產品 UI 新增/編輯 `https` host（含 token-off 直接新增），並在純瀏覽器首次開啟（無可用 https host）時一鍵「連到本 daemon」——解鎖在 `https://purdex.mlab.host/` 實際連線。

**Architecture:** 沿用 P0 的 `host-endpoint` 純函式。`AddHostDialog` 的 `handleConfirm` 依 `mode`（`token` / `pairing` / `direct`）分流：`direct`（token-off）不打任何 pair/token API，只建立顯式 host；`token` 走 `fetchTokenAuth`（base 依 scheme）；`pairing` 走 `fetchPairSetup`（base **固定 http**）。scheme selector 僅在手動 stage 顯示。`TokenField` 改用 `deriveDaemonBase`。`HostPage` 在 web + https + 無 https host 時顯示「連到本 daemon」建議，開啟預填的 `AddHostDialog`。`OverviewSection` 可編輯 scheme。

**Tech Stack:** React 19 / Zustand 5 / TypeScript / Vitest + @testing-library/react。

## Global Constraints

- 連線一律走**顯式 host endpoint（含 scheme）+ ticket**，origin 無關；首連建議是「把當前 origin 預填進顯式 host entity」，**非隱式同源連線、不用相對 URL**（spec §6）。
- `scheme` 缺省 = `'http'`（向後相容）。
- endpoint 導出/去重**一律用 `spa/src/lib/host-endpoint.ts`**（`deriveDaemonBase` / `hostEndpointKey`），**禁止**再寫 `http://${ip}:${port}` 或 `ip+port` 比較。
- **pairing 路徑固定 http**：pairing confirm 的 base 恆為 `deriveDaemonBase({scheme:'http',...})`；scheme selector 在 pairing stage（`paired`）**不顯示**。
- **token 維持關**（spec §2.2）：origin 首連走 `direct` mode，不打 pair/token API、不受 token 有效性 gate。
- web 判斷用 `getPlatformCapabilities().isElectron === false`；https 判斷用 `window.location.protocol === 'https:'`。
- i18n 插值語法為 **`{{host}}`**（雙大括號；`useI18nStore` 以 `/\{\{(\w+)\}\}/g` 替換）。新 key 同步 `zh-TW.json` 與 `en.json`。
- 測試查詢**沿用既有 pattern**（`getByPlaceholderText('100.64.0.1')` / `'7860'`、`getByRole('checkbox')`、`getByText('Pair'|'Confirm'|'Cancel')`）；新 `scheme` select 以 `aria-label` 供 `getByRole('combobox')` 或 `getByLabelText`。
- 測試：`cd spa && npx vitest run`；Lint：`cd spa && pnpm run lint`；Build：`cd spa && pnpm run build`。
- 每個 task 獨立 commit。

---

## File Structure

- **Modify** `spa/src/components/hosts/AddHostDialog.tsx` — scheme state + selector（manual only）；`handleConfirm` 依 `mode` 分流（含 token-off `direct`）；health effect scheme-aware port；3 處 base 改 `deriveDaemonBase`（pairing 固定 http）；dedupe `hostEndpointKey`；`initial` 預填 prop。
- **Modify** `spa/src/components/hosts/AddHostDialog.test.tsx` — 追加 scheme / initial / direct-add 測試（沿用既有 mock + placeholder 查詢）。
- **Modify** `spa/src/components/hosts/form-fields.tsx` — `TokenField` 加 `scheme`，驗證 base 改 `deriveDaemonBase`。
- **Modify** `spa/src/components/hosts/OverviewSection.tsx` — 傳 `host.scheme` 給 `TokenField`；用既有 `Field` 包 scheme `<select>` → `updateHost`。
- **Create** `spa/src/components/hosts/OverviewSection.scheme.test.tsx` — scheme 編輯測試（沿用既有 host-api mock pattern）。
- **Create** `spa/src/lib/origin-host-suggestion.ts` + `.test.ts` — 首連建議純函式。
- **Modify** `spa/src/components/HostPage.tsx` — 建議 CTA + 預填 `AddHostDialog`。
- **Modify** `spa/src/components/HostPage.test.tsx` — 首連 CTA + `initial` 傳遞 integration test。
- **Modify** `spa/src/locales/zh-TW.json`, `spa/src/locales/en.json` — 新 i18n keys。

---

## Task 1: AddHostDialog — scheme + token-off direct-add + initial 預填

**Files:**
- Modify: `spa/src/components/hosts/AddHostDialog.tsx`
- Test: `spa/src/components/hosts/AddHostDialog.test.tsx`（追加）
- Modify: `spa/src/locales/zh-TW.json`, `spa/src/locales/en.json`

**Interfaces:**
- Consumes: `deriveDaemonBase`, `hostEndpointKey`（`../../lib/host-endpoint`）。
- Produces: `AddHostDialog` 接受 `initial?: { scheme?: 'http'|'https'; ip?: string; port?: string; useToken?: boolean }`；`direct` mode 建立 token-off 顯式 host。

- [ ] **Step 1: 寫失敗測試（追加至既有 AddHostDialog.test.tsx）**

```tsx
  it('manual token 路徑選 https → 存入 scheme=https', async () => {
    vi.spyOn(hostApi, 'fetchTokenAuth').mockResolvedValue({ ok: true } as never)
    render(<AddHostDialog onClose={vi.fn()} />)
    fireEvent.click(screen.getByRole('checkbox')) // → manual token
    fireEvent.change(screen.getByRole('combobox', { name: /scheme/i }), { target: { value: 'https' } })
    fireEvent.change(screen.getByPlaceholderText('100.64.0.1'), { target: { value: 'purdex.mlab.host' } })
    fireEvent.change(screen.getByPlaceholderText('7860'), { target: { value: '443' } })
    fireEvent.change(screen.getByPlaceholderText('purdex_...'), { target: { value: 'x'.repeat(24) } })
    fireEvent.click(screen.getByText('Confirm'))
    await waitFor(() => {
      const s = useHostStore.getState()
      const id = s.hostOrder.find((i) => s.hosts[i].ip === 'purdex.mlab.host')!
      expect(s.hosts[id].scheme).toBe('https')
      expect(s.getDaemonBase(id)).toBe('https://purdex.mlab.host')
    })
    expect(hostApi.fetchTokenAuth).toHaveBeenCalledWith('https://purdex.mlab.host', 'x'.repeat(24))
  })

  it('initial 預填 https + token-off → direct-add 建立顯式 host，不打 pair/token API', async () => {
    const pairSetup = vi.spyOn(hostApi, 'fetchPairSetup')
    const tokenAuth = vi.spyOn(hostApi, 'fetchTokenAuth')
    const onClose = vi.fn()
    render(<AddHostDialog onClose={onClose} initial={{ scheme: 'https', ip: 'purdex.mlab.host', port: '443', useToken: false }} />)
    // 預填值就位
    expect((screen.getByRole('combobox', { name: /scheme/i }) as HTMLSelectElement).value).toBe('https')
    expect((screen.getByPlaceholderText('100.64.0.1') as HTMLInputElement).value).toBe('purdex.mlab.host')
    expect((screen.getByPlaceholderText('7860') as HTMLInputElement).value).toBe('443')
    // 直接 Confirm（無 token）
    fireEvent.click(screen.getByText('Confirm'))
    await waitFor(() => {
      const s = useHostStore.getState()
      const id = s.hostOrder.find((i) => s.hosts[i].ip === 'purdex.mlab.host')!
      expect(s.hosts[id].scheme).toBe('https')
      expect(s.hosts[id].token ?? null).toBeFalsy()
      expect(s.activeHostId).toBe(id)
    })
    expect(pairSetup).not.toHaveBeenCalled()
    expect(tokenAuth).not.toHaveBeenCalled()
    expect(onClose).toHaveBeenCalled()
  })
```

- [ ] **Step 2: 跑測試確認失敗**

Run: `cd spa && npx vitest run src/components/hosts/AddHostDialog.test.tsx`
Expected: FAIL（無 scheme combobox；initial 不存在；direct-add 走到 pairing 分支）。

- [ ] **Step 3: 實作**

於 `AddHostDialog.tsx`：

1. import：`import { deriveDaemonBase, hostEndpointKey } from '../../lib/host-endpoint'`。
2. `Props`：
   ```ts
   interface Props {
     onClose: () => void
     initial?: { scheme?: 'http' | 'https'; ip?: string; port?: string; useToken?: boolean }
   }
   ```
   簽章 `export function AddHostDialog({ onClose, initial }: Props) {`。
3. state 初值（用 `initial`）：
   ```ts
   const [ip, setIp] = useState(initial?.ip ?? '')
   const [port, setPort] = useState(initial?.port ?? '7860')
   const [scheme, setScheme] = useState<'http' | 'https'>(initial?.scheme ?? 'http')
   const [token, setToken] = useState('')
   const [stage, setStage] = useState<Stage>(initial ? 'manual' : 'idle')
   const [useToken, setUseToken] = useState(initial?.useToken ?? false)
   ```
   （其餘 state 不變。）
4. `defaultPortFor`：新增小工具（檔案內）：
   ```ts
   const portOrDefault = (p: string, s: 'http' | 'https') =>
     parseInt(p, 10) || (s === 'https' ? 443 : 7860)
   ```
5. **health 檢查 useEffect**：`const portNum = port || '7860'` → 改用 scheme-aware：把 fetch 改成
   ```ts
   const res = await fetch(`${deriveDaemonBase({ scheme, ip, port: portOrDefault(port, scheme) })}/api/health`)
   ```
   並把 `scheme` 加入依賴陣列。
6. **handleConfirm** 全面替換為 `mode` 分流：
   ```ts
   const handleConfirm = async () => {
     const trimmedIp = ip.trim()
     const trimmedToken = token.trim()
     const portNum = portOrDefault(port.trim(), scheme)
     const mode: 'token' | 'pairing' | 'direct' =
       useToken ? 'token' : stage === 'paired' ? 'pairing' : 'direct'
     setStage('saving')
     setError('')

     const upsertHost = () => {
       const draftKey = hostEndpointKey({ scheme, ip: trimmedIp, port: portNum })
       const hosts = useHostStore.getState().hosts
       const existingId = Object.keys(hosts).find((id) => hostEndpointKey(hosts[id]) === draftKey)
       let hostId: string
       if (existingId) {
         useHostStore.getState().updateHost(existingId, { scheme, token: trimmedToken || undefined })
         hostId = existingId
       } else {
         hostId = addHost({ name: trimmedIp, ip: trimmedIp, port: portNum, scheme, token: trimmedToken || undefined })
       }
       useHostStore.getState().setActiveHost(hostId)
     }

     try {
       if (mode === 'direct') {
         upsertHost()
       } else if (mode === 'token') {
         await fetchTokenAuth(deriveDaemonBase({ scheme, ip: trimmedIp, port: portNum }), trimmedToken)
         upsertHost()
       } else {
         // pairing — always http (LAN/tailnet)
         await fetchPairSetup(deriveDaemonBase({ scheme: 'http', ip: trimmedIp, port: portNum }), setupSecret, trimmedToken)
         upsertHost()
       }
       setStage('done')
       onClose()
     } catch (err) {
       if (mode === 'token') {
         setStage('manual')
       } else {
         setStage('idle')
         setPairingCode('')
         setSetupSecret('')
       }
       if (err instanceof PairingError) {
         setError(`HTTP ${err.status}`)
       } else {
         setError(err instanceof Error ? err.message : t('hosts.connection_failed'))
       }
     }
   }
   ```
   （沿用既有 `addHost`、`fetchTokenAuth`、`fetchPairSetup`、`PairingError` import。移除舊 handleConfirm 內原本的 `existingId`/dedupe/`addHost` 區塊。）
7. **confirm 按鈕 disabled**：改為 `disabled={confirmDisabled || (useToken && !tokenValid)}`——token 模式才需有效 token，`direct`/`pairing` 不受 tokenValid gate。（`confirmDisabled = stage !== 'paired' && stage !== 'manual'` 維持。）
8. **JSX scheme selector**（僅手動 stage 顯示）：於 ip/port grid **前**插入：
   ```tsx
   {stage === 'manual' && (
     <div>
       <label htmlFor="host-scheme" className="text-xs text-text-secondary block mb-1">{t('hosts.scheme')}</label>
       <select
         id="host-scheme"
         aria-label={t('hosts.scheme')}
         value={scheme}
         onChange={(e) => setScheme(e.target.value as 'http' | 'https')}
         className="w-full bg-surface-secondary border border-border-default rounded px-3 py-2 text-sm text-text-primary"
       >
         <option value="http">http</option>
         <option value="https">https</option>
       </select>
     </div>
   )}
   ```
9. i18n：`zh-TW.json` 加 `"hosts.scheme": "連線協定"`；`en.json` 加 `"hosts.scheme": "Scheme"`。

- [ ] **Step 4: 跑測試確認通過**

Run: `cd spa && npx vitest run src/components/hosts/AddHostDialog.test.tsx`
Expected: PASS（既有 + 2 新 case）。

- [ ] **Step 5: Commit**

```bash
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/web-version
git add spa/src/components/hosts/AddHostDialog.tsx spa/src/components/hosts/AddHostDialog.test.tsx spa/src/locales/zh-TW.json spa/src/locales/en.json
git commit -m "feat(hosts): AddHostDialog scheme + token-off direct-add + initial prefill (P2a)"
```

---

## Task 2: TokenField scheme 支援 + OverviewSection scheme 編輯

**Files:**
- Modify: `spa/src/components/hosts/form-fields.tsx`（`TokenField`）
- Modify: `spa/src/components/hosts/OverviewSection.tsx`
- Test: `spa/src/components/hosts/OverviewSection.scheme.test.tsx`（新建）

**Interfaces:**
- Consumes: `deriveDaemonBase`（`../../lib/host-endpoint`）。
- Produces: `TokenField` props 加 `scheme?: 'http'|'https'`；`OverviewSection` 傳 `host.scheme` 並提供 scheme `<select>`。

- [ ] **Step 1: 寫失敗測試**

```tsx
// spa/src/components/hosts/OverviewSection.scheme.test.tsx
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { OverviewSection } from './OverviewSection'
import { useHostStore } from '../../stores/useHostStore'

vi.mock('../../lib/host-api', () => ({
  hostFetch: vi.fn(),
  fetchInfo: vi.fn().mockResolvedValue(undefined),
  fetchHealth: vi.fn().mockResolvedValue(undefined),
}))

describe('OverviewSection — scheme edit', () => {
  beforeEach(() => { useHostStore.getState().reset() })

  it('切換 scheme → updateHost 生效、getDaemonBase 導出 https', () => {
    const id = useHostStore.getState().addHost({ name: 'h', ip: 'purdex.mlab.host', port: 443 })
    useHostStore.getState().setActiveHost(id)
    render(<OverviewSection hostId={id} />)
    fireEvent.change(screen.getByRole('combobox', { name: /scheme/i }), { target: { value: 'https' } })
    expect(useHostStore.getState().hosts[id].scheme).toBe('https')
    expect(useHostStore.getState().getDaemonBase(id)).toBe('https://purdex.mlab.host')
  })
})
```

> 註：`OverviewSection` props 以現況為準（若非 `hostId`，對齊實際簽章）。

- [ ] **Step 2: 跑測試確認失敗**

Run: `cd spa && npx vitest run src/components/hosts/OverviewSection.scheme.test.tsx`
Expected: FAIL（無 scheme combobox）。

- [ ] **Step 3: 實作**

`form-fields.tsx` — `TokenField`：
1. props 加 `scheme?: 'http' | 'https'`（型別區塊）。
2. import：`import { deriveDaemonBase } from '../../lib/host-endpoint'`。
3. 驗證 base：`const base = \`http://${ip}:${port}\`` → `const base = deriveDaemonBase({ scheme, ip, port })`。

`OverviewSection.tsx`：
1. import：`import { deriveDaemonBase } from '../../lib/host-endpoint'`（若後續需要；scheme select 用 store）。
2. Connection Section 內、IP 欄位**前**，用既有 `Field` 包 scheme select：
   ```tsx
   <Field label={t('hosts.scheme')}>
     <select
       aria-label={t('hosts.scheme')}
       value={host.scheme ?? 'http'}
       onChange={(e) => updateHost(hostId, { scheme: e.target.value as 'http' | 'https' })}
       className="bg-surface-secondary border border-border-default rounded px-2 py-1 text-sm text-text-primary"
     >
       <option value="http">http</option>
       <option value="https">https</option>
     </select>
   </Field>
   ```
3. `TokenField` 的呼叫加 `scheme={host.scheme}`：
   ```tsx
   <TokenField token={host.token ?? undefined} ip={host.ip} port={host.port} scheme={host.scheme} onSave={...} t={t} />
   ```

- [ ] **Step 4: 跑測試確認通過**

Run: `cd spa && npx vitest run src/components/hosts/OverviewSection.scheme.test.tsx`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/web-version
git add spa/src/components/hosts/form-fields.tsx spa/src/components/hosts/OverviewSection.tsx spa/src/components/hosts/OverviewSection.scheme.test.tsx spa/src/locales/zh-TW.json spa/src/locales/en.json
git commit -m "feat(hosts): TokenField + OverviewSection scheme support (P2a)"
```

---

## Task 3: 首連建議純函式 + HostPage 接線

**Files:**
- Create: `spa/src/lib/origin-host-suggestion.ts` + `.test.ts`
- Modify: `spa/src/components/HostPage.tsx`
- Test: `spa/src/components/HostPage.test.tsx`（追加）
- Modify: `spa/src/locales/zh-TW.json`, `spa/src/locales/en.json`

**Interfaces:**
- Consumes: `hostScheme`（`../lib/host-endpoint`）、`HostConfig`。
- Produces:
  - `shouldSuggestOriginHost(opts: { isElectron: boolean; protocol: string; hosts: HostConfig[] }): boolean`
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

describe('shouldSuggestOriginHost', () => {
  it('web + https + 無任何 https host → 建議', () => {
    expect(shouldSuggestOriginHost({ isElectron: false, protocol: 'https:', hosts: [host({ scheme: 'http' })] })).toBe(true)
  })
  it('已有任一 https host → 不建議', () => {
    expect(shouldSuggestOriginHost({ isElectron: false, protocol: 'https:', hosts: [host({ scheme: 'https' })] })).toBe(false)
  })
  it('Electron → 不建議', () => {
    expect(shouldSuggestOriginHost({ isElectron: true, protocol: 'https:', hosts: [] })).toBe(false)
  })
  it('非 https 頁面 → 不建議', () => {
    expect(shouldSuggestOriginHost({ isElectron: false, protocol: 'http:', hosts: [] })).toBe(false)
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
// suggest adding the current origin as a host. On an https page, only https
// hosts can connect (http hosts are mixed-content-blocked), so we suggest
// when there is no usable https host at all. The suggestion is an explicit
// host draft, never an implicit same-origin connection (spec §6).
import type { HostConfig } from '../stores/useHostStore'
import { hostScheme } from './host-endpoint'

export function shouldSuggestOriginHost(opts: {
  isElectron: boolean
  protocol: string
  hosts: HostConfig[]
}): boolean {
  if (opts.isElectron) return false
  if (opts.protocol !== 'https:') return false
  return !opts.hosts.some((h) => hostScheme(h) === 'https')
}

export function originHostDraft(opts: { hostname: string; port: string }): {
  scheme: 'https'
  ip: string
  port: string
  useToken: boolean
} {
  return { scheme: 'https', ip: opts.hostname, port: opts.port || '443', useToken: false }
}
```

- [ ] **Step 4: 跑測試確認通過**

Run: `cd spa && npx vitest run src/lib/origin-host-suggestion.test.ts`
Expected: PASS（6 tests）。

- [ ] **Step 5: HostPage 接線 + integration test**

於 `HostPage.tsx`：
1. import：
   ```ts
   import { getPlatformCapabilities } from '../lib/platform'
   import { shouldSuggestOriginHost, originHostDraft } from '../lib/origin-host-suggestion'
   ```
   （`useHostStore` 若未 import 於此檔則補上。）
2. 元件內：
   ```ts
   const hosts = useHostStore((s) => s.hostOrder.map((id) => s.hosts[id]))
   const [prefillOrigin, setPrefillOrigin] = useState(false)
   const suggestOrigin =
     typeof window !== 'undefined' &&
     shouldSuggestOriginHost({
       isElectron: getPlatformCapabilities().isElectron,
       protocol: window.location.protocol,
       hosts,
     })
   ```
3. host 內容區上方條件顯示 CTA：
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
4. 既有 `{showAddHost && <AddHostDialog onClose={() => setShowAddHost(false)} />}` 改：
   ```tsx
   {showAddHost && (
     <AddHostDialog
       onClose={() => { setShowAddHost(false); setPrefillOrigin(false) }}
       initial={prefillOrigin ? originHostDraft({ hostname: window.location.hostname, port: window.location.port }) : undefined}
     />
   )}
   ```
5. i18n（`{{host}}` 語法）：
   - `zh-TW.json`：`"hosts.suggest_origin": "此頁由 {{host}} 的 daemon 提供，要連到它嗎？"`、`"hosts.connect_this_daemon": "連到本 daemon"`。
   - `en.json`：`"hosts.suggest_origin": "This page is served by the daemon at {{host}}. Connect to it?"`、`"hosts.connect_this_daemon": "Connect to this daemon"`。

6. **HostPage integration test**（追加至 `HostPage.test.tsx`，沿用其既有 mock；若該檔已 mock `AddHostDialog`，改為可捕捉 props 的 mock）：
   ```tsx
   it('web + https + 無 https host → 顯示連線建議，點擊後 AddHostDialog 收到 origin initial', () => {
     // 模擬 https origin
     const orig = window.location
     Object.defineProperty(window, 'location', {
       value: { ...orig, protocol: 'https:', hostname: 'purdex.mlab.host', port: '' },
       writable: true,
     })
     // 只有預設 http host（reset 後預設 mlab http）
     render(<HostPage /* 依既有 test 的 props */ />)
     const btn = screen.getByText(/connect to this daemon|連到本 daemon/i)
     fireEvent.click(btn)
     // 依 HostPage.test.tsx 對 AddHostDialog 的 mock 捕捉 props，斷言 initial.scheme==='https' && initial.ip==='purdex.mlab.host'
     // ...（對齊該檔既有 mock 擷取方式）
     Object.defineProperty(window, 'location', { value: orig, writable: true })
   })
   ```
   > 實作時對齊 `HostPage.test.tsx` 既有 `AddHostDialog` mock 的擷取方式（例如 `vi.mock('./hosts/AddHostDialog', ...)` 記錄 last props）。若既有測試未 mock 該元件，改用 `vi.mock` 記錄 `initial` prop。

- [ ] **Step 6: 跑測試 + Commit**

Run: `cd spa && npx vitest run src/lib/origin-host-suggestion.test.ts src/components/HostPage.test.tsx`
Expected: PASS。

```bash
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/web-version
git add spa/src/lib/origin-host-suggestion.ts spa/src/lib/origin-host-suggestion.test.ts spa/src/components/HostPage.tsx spa/src/components/HostPage.test.tsx spa/src/locales/zh-TW.json spa/src/locales/en.json
git commit -m "feat(hosts): suggest current origin as https host on web first-connect (P2a)"
```

---

## 收尾驗證（全 task 完成後，主 Claude 執行）

- [ ] `cd spa && npx vitest run` — 全綠。
- [ ] `cd spa && pnpm run lint` — 無新增錯誤。
- [ ] `cd spa && pnpm run build` — 成功。

---

## Self-Review 對照 spec + codex review

- **spec §5.2 host scheme UI** → Task 1（AddHostDialog scheme + deriveDaemonBase + dedupe）+ Task 2（TokenField + OverviewSection scheme）。✅
- **spec §5.2 首連 UX（預填 origin、顯式 host、非隱式同源）** → Task 1（initial + direct-add）+ Task 3（suggestion + HostPage）。✅
- **spec §5.2 / §2.2 pairing 維持 http、token 維持關** → Task 1 pairing 分支固定 http、scheme selector 不顯示於 paired、direct mode token-off 不打 API。✅（codex C1/C2）
- **codex I1** health scheme-aware port → Task 1 step 5。**codex I2** TokenField → Task 2。**codex I3** 測試用 placeholder/aria-label → 全 task。**codex I4** `{{host}}` → Task 3。**codex I5** HostPage integration test → Task 3 step 5。**codex I6** predicate 改「無 https host」→ Task 3。**codex m1/m2** Field 包裝 + host-api mock → Task 2。✅
- 無 placeholder；`scheme`/`deriveDaemonBase`/`hostEndpointKey`/`originHostDraft`/`shouldSuggestOriginHost` 命名跨 task 一致。✅
