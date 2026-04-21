# HSR PR-4：Host Settings Shell + Built-in Sub-page Adapter — Implementation Plan

> 日期：2026-04-22（v2 post-codex-review task-mo8xtpa8-bdxsh4）
> 狀態：Ready for Implementation（**依序 build on PR-2 + PR-3**）
> 主 spec：`2026-04-21-settings-contribution-registry-design.md`
> 決策對齊：4c（HostPage registry-driven + 六子頁走 built-in adapter registration + host route contract 鬆綁）
> PR 系列：PR-1 ✅ / PR-2 / PR-3 / **PR-4（本文件）** / PR-5

---

## 1. 範圍

**Shell 改造 + built-in adapter registration + host route contract 鬆綁**。

三層工作：
1. **Host route 型別鬆綁**：`HostSubPage` 從六字串 union 改為 `string`（runtime 由 registry 驗證）；`HOST_SUB_PAGES` 保留為 built-in 的預設值 const 但不再是型別來源；`parseRoute()` / `resolveSelection()` / `HostPage` / `HostSidebar` 所有 exhaustiveness 假設改為動態（codex review Finding 3 的硬約束）
2. **Built-in adapter 註冊**：六個既有 sub-page 透過 `registerSettingsSection` 風格的 host adapter 走 pending buffer，由 `dispatchSettingsContributions()` 統一 flush（沿用 PR-2 建立的 dispatch-flushed pattern，避免 clearContributions 清掉 built-in）；六子頁內部程式碼**零改動**
3. **Shell 吃 registry**：`HostPage.renderContent` + `HostSidebar.SUB_PAGES` 改讀 `listContributions('host')` + 以 `ctx: { scope: 'host', hostId }` 注入

本 PR 一併驗證 **#541**（cross-store rehydrate order），使用具體 harness（§3.4），不僅口頭描述。

PR-4 結束時：
- `HostPage` 的 sub-page 渲染透過 `listContributions('host')` + ctx 注入
- 六子頁以 `_builtin.host.<id>` 存在於 registry，與任何 future module-contributed host section 走同一條 render 路徑
- URL `/hosts/:hostId/:subPage` 的 `subPage` 從「六個 literal 之一」放寬為「registry 中 scope='host' 的任一 localId」
- `HostSubPage` 型別收斂為 `string` 或 branded string；exhaustive switch 被動態 dispatch 取代
- 新 module 可宣告 `settings: [{ scope: 'host', ... }]` 並出現在 `HostPage` 左側 sidebar
- `removeHost()` cascade 對 `useHostSettingsStore` 的清理 PR-1 已完成；本 PR 的 #541 harness 驗回歸 + rehydrate order invariant

---

## 2. 檔案清單

### 修改
- `spa/src/lib/host-routes.ts`（型別鬆綁 — Finding 3）
  - `HostSubPage` 改為 `type HostSubPage = string`（或 `type HostSubPage = string & { readonly __brand: 'HostSubPage' }` branded string，實作擇一）
  - `HOST_SUB_PAGES` 保留為 `const` 陣列但**僅作** built-in 預設值（供 `parseRoute()` fallback、測試 fixture、i18n key 檢索），**不再**作為 type literal 來源
  - `isHostSubPage(value): value is HostSubPage` 改為 `listContributions('host').some(c => c.localId === value)`
  - `parseRoute()` 回傳 shape 不變（`subPage: HostSubPage`），但型別 narrow 靠 `isHostSubPage` runtime 而非 literal union
- `spa/src/components/HostPage.tsx`
  - `renderContent()`：從 `switch (selection.subPage)` 改為 `const contribution = listContributions('host').find(c => c.localId === selection.subPage)`；找到則 `<contribution.component ctx={{ scope: 'host', hostId: selection.hostId }} />`；否則 redirect 到 default（沿用 `resolveSelection` 自癒邏輯）
  - 移除對六字串 literal 的 exhaustive switch 假設；以 registry lookup 取代
  - `getFallbackSubPage()` 現況為 const return；改為讀 `listContributions('host')[0]?.localId ?? 'overview'`（registry 空時 fallback 到 `'overview'` 字面值作 safety net）
- `spa/src/components/hosts/HostSidebar.tsx`
  - 移除硬編 `SUB_PAGES` const
  - `const subPages = listContributions('host')` — 排序已保證
  - 每項顯示 `t(c.labelKey)`（與舊 `SUB_PAGES[].labelKey` 一致）
  - `onSelect(hostId, subPage: string)` 參數型別鬆綁（subPage 是 registry localId，不再是六 literal 之一）
- `spa/src/lib/register-modules.tsx`（走 pending buffer，PR-2 dispatch-flushed pattern）
  - 新增 `registerBuiltinHostSection(def: HostSectionDef)` helper（可放 `settings-section-registry.ts` 或 `host-builtin-sections.ts`）：組 `SettingsContributionDeclaration`（`scope: 'host'`、`moduleId: '_builtin.host'`、wrap 過的 `component: (props) => props.ctx.scope === 'host' ? <Section hostId={props.ctx.hostId} /> : null`）push 到 pending buffer（與 legacy adapter 同一 queue，或獨立第二 queue — 任一實作，測試明確）
  - `registerBuiltinModules()` 結尾在 `dispatchSettingsContributions()` 之前（與 `registerSettingsSection` 同階段）呼叫六次 `registerBuiltinHostSection(...)` — sessions (1) / hooks (2) / agents (3) / uploads (4) / logs (5)（overview 為 0）
  - Dispatch pass 統一 flush 後六項進 `listContributions('host')`，與 PR-2 的 legacy adapter 機制一致
- `spa/src/lib/dispatch-settings-contributions.ts`（若採獨立第二 queue）
  - `drainHostBuiltinQueue()` 整合進 Phase 2，與 legacy queue、module-declared batch 三者合併
- `spa/src/lib/host-lifecycle.test.ts`（#541 具體 harness — Finding 5）
  - 不動 `host-lifecycle.ts` source（現況已 guard；本 PR 驗回歸 + 加 order 測試）

### 新增
- `spa/src/components/HostPage.test.tsx`（render-level）
  - 現有六 sub-page 能正常 render（透過 registry）
  - fake `scope: 'host'` contribution 註冊後出現在 sidebar + 可導航
  - `ctx.hostId` 正確注入
- `spa/src/lib/host-builtin-sections.test.ts`（optional — 可合併進 `register-modules.test.ts`）
  - Built-in adapter 註冊後 `listContributions('host').length >= 6`
  - 順序：overview → sessions → hooks → agents → uploads → logs
  - Wrap 的 component 正確 forward `hostId`

### 不動
- 六個既有 sub-page component 內部（`OverviewSection` / `SessionsSection` / `HooksSection` / `AgentsSection` / `UploadSection` / `LogsSection`）
- URL 結構 `/hosts/:hostId/:subPage`（decode / encode 不變）
- PR-1 三層 store 內部
- PR-2 / PR-3 shell

---

## 3. Test Case Matrix

### 3.1 `HostPage.test.tsx`（render-level）

| 類別 | 測試項 | 預期 |
|---|---|---|
| Built-in 渲染 | URL `/hosts/hA/overview` → render `OverviewSection` | 通過 |
| Built-in 渲染 | URL `/hosts/hA/sessions` → render `SessionsSection` | 通過 |
| Sidebar 順序 | 六 sub-page 顯示順序 = built-in 註冊 order | 通過 |
| Fake 擴充 | 註冊 fake `scope: 'host'` contribution（order=100）→ sidebar 多一項、URL `/hosts/hA/<fakeId>` 可 render fake body | 通過 |
| Ctx 注入 | fake component 讀 `props.ctx` → `scope === 'host'` && `hostId === 'hA'` | 通過 |
| Invalid subPage | URL `/hosts/hA/nonexistent` → redirect 到 `canonicalPath`（既有 resolveSelection 行為） | 通過 |
| Disabled | fake contribution `disabled: (ctx) => true` → sidebar 隱藏（或禁用樣式） | 通過 |

### 3.2 `host-routes.test.ts` 擴充或新增

| 類別 | 測試項 | 預期 |
|---|---|---|
| `isHostSubPage` | 現六 ID 回 true | 通過 |
| `isHostSubPage` | 任何 module-contributed host localId 回 true（註冊 fake 後） | 通過 |
| `isHostSubPage` | 未註冊的 ID 回 false | 通過 |

### 3.3 `register-modules.test.ts` 擴充

| 類別 | 測試項 | 預期 |
|---|---|---|
| Built-in 註冊 | 跑 `registerBuiltinModules()` 後 `listContributions('host')` 含六項，`moduleId='_builtin.host'` | 通過 |
| Order | 順序：overview / sessions / hooks / agents / uploads / logs | 通過 |
| Wrap forward | 取得第 0 項 `listContributions('host')[0]`，用 `ctx={{ scope:'host', hostId:'hA' }}` render → 等同 `<OverviewSection hostId='hA' />` 行為（以 render 內含可辨識文字斷言） | 通過 |
| Scope guard | `_builtin.host.overview` contribution 收到 `ctx.scope !== 'host'` 時 render null（wrap 的 guard） | 通過 |

### 3.4 `host-lifecycle.test.ts` 擴充（#541 — 具體 harness）

**Harness 設計**：zustand persist 的 rehydrate 是自動的，要精準控制順序需用 test fixture：

```ts
// Setup pattern
import { createStore } from 'zustand/vanilla'
// 手動 create store instance（不經過 module singleton）
const makeHostStore = () => createStore(...)
const makeHostSettingsStore = () => createStore(...)

// 控制 rehydrate 順序
function harnessOrderA() {
  const hostStore = makeHostStore()
  hostStore.persist.rehydrate()  // 完成
  const settingsStore = makeHostSettingsStore()
  settingsStore.persist.rehydrate()  // 完成
  return { hostStore, settingsStore }
}

function harnessOrderB() {
  const settingsStore = makeHostSettingsStore()
  settingsStore.persist.rehydrate()
  const hostStore = makeHostStore()
  hostStore.persist.rehydrate()
  return { hostStore, settingsStore }
}
```

| 類別 | 測試項 | 預期 |
|---|---|---|
| Rehydrate order A | `harnessOrderA()` → 預設 seed data（hosts[hostA], hostSettings[hostA][editor]）→ 刪除 hostA → `hostSettingsStore.get(hostA, 'editor')` undefined；undo 在 window 內 → 兩 store 都恢復 | 通過 |
| Rehydrate order B | `harnessOrderB()` → 相同 seed + 行為 → 兩 store 恢復 | 通過 |
| Interleaved | 模擬「HostStore 半 rehydrate（hosts[hostA] 存在但 runtime 還沒）時觸發刪除」— 驗 `removeHost` 的 precheck 不會因部分 state 誤判 last-host | 通過 |
| hostWasRecreated | Rehydrate 完成後模擬 same-id 重建 → `hostWasRecreated(hostId)` 回 true（沿用 PR-1 實作） | 通過 |
| Cascade 回歸 | 刪除 hostA → `hostSettingsStore.get(hostA, *)` undefined（PR-1 cascade） | 通過 |
| Undo 回歸 | Cascade clear 後 undo window 內 undo → host + hostSettings 都恢復（PR-1） | 通過 |
| Workspace 交叉 | 刪 hostA + hostA 參與的 workspace tear-off / merge → workspaceSettings 不被連坐清（PR-1 的 D finding） | 通過（回歸） |

**若 harness 發現 bug**：改 `host-lifecycle.ts` source 修邏輯（不再 ship PR-4 直到 #541 解決）。若發現 bug 過大，選項 B：#541 拆獨立 PR，PR-4 scope 保留 built-in adapter + shell 部分（先 ship），#541 後補 follow-up。

**Harness 複雜度風險**：若 zustand v4+ 的 `persist.rehydrate()` API 無法精準控制，改用較直接的 state mutation：`store.setState(mockedRehydratedState)` 模擬「已 rehydrate」狀態；`store.setState(initialState)` 模擬「未 rehydrate」狀態。兩種實作擇一，測試先綠為準。

### 3.5 視覺回歸（手動）

- `cd spa && pnpm dev`
- 開 `/hosts`：左側 sidebar 顯示 host 列表 + 展開每個 host 顯示六 sub-page
- 點每個 sub-page：右側正常 render
- 點新增 host button、切換 host、切換 sub-page 均正常
- 無 console error

---

## 4. 實作順序（TDD）

1. **紅**：寫 §3.3 built-in 註冊測試（registry 有六項、順序正確）
2. **綠**：在 `register-modules.tsx` 加 `registerBuiltinHostSections()`
3. **紅**：寫 §3.1 `HostPage.test.tsx`
4. **綠**：改 `HostPage.tsx` `renderContent()` + `HostSidebar.tsx` 讀 registry
5. **紅**：寫 §3.2 `host-routes.test.ts` 擴充
6. **綠**：改 `host-routes.ts` `isHostSubPage` 為動態
7. **紅**：寫 §3.4 `host-lifecycle.test.ts` #541 案例
8. **綠**：若需要，修 `host-lifecycle.ts` 判定邏輯（否則只補 test）
9. **驗證**：`cd spa && pnpm exec vitest run` / `pnpm run lint` / `pnpm run build` 全綠
10. **手動**：§3.5 視覺回歸

---

## 5. 驗收條件

- [ ] §3.1–§3.4 測試全綠
- [ ] `cd spa && pnpm exec vitest run` 全綠
- [ ] `cd spa && pnpm run lint` 全綠
- [ ] `cd spa && pnpm run build` 全綠
- [ ] §3.5 手動視覺回歸：六 sub-page 行為與 `main` 一致、無 console error、registry 擴充可見
- [ ] `gh issue close #541`（或補充「PR-4 已驗證 cross-store rehydrate order」）
- [ ] `gh issue close #538`（若 PR-2/3 尚未關閉 Host 層，則本 PR 關；否則補充）
- [ ] Codex 兩輪 review 無 critical / P1 未修項

---

## 6. 風險

| 風險 | 發生可能 | 緩解 |
|---|---|---|
| Built-in 註冊時機錯過第一次 render（race）| 中 | `registerBuiltinModules()` 在 app bootstrap 早期同步跑；`HostPage` render 前已完成；測試覆蓋「initial URL `/hosts/hA/overview` 即 render」 |
| `HOST_SUB_PAGES` 移除後其他 callsite 爆 | 中 | grep `HOST_SUB_PAGES` 找所有 import → 改為動態查詢 或 保留為 optional fallback |
| Wrap 的 scope guard 漏寫，contribution 收到錯誤 ctx | 低 | §3.3 scope guard 測試強制覆蓋 |
| #541 測試發現 rehydrate order 確實有 bug，需要動 `host-lifecycle.ts` | 中 | Plan §3.4 已預留修 source 的餘地；若 bug 過大影響 PR-4 scope，可切成獨立 fix PR，PR-4 blocker |
| labelKey 顯示（sidebar）與 URL localId 可能不一致（例：label 翻譯後中文，但 URL 還是英文）| 低 | 現行 `SUB_PAGES` 已如此；保留 localId 為 URL token，labelKey 為顯示名稱 |
| 新 host contribution 被註冊時 URL `/hosts/hA/<new>` 可直接進入 — 但沒有 invariant 保證新 localId 不與既有撞（overview/sessions/…）| 低 | PR-1 registry `id` collision throw 已擋（兩個 `_builtin.host.overview` 會拋錯）；module 作者撞 built-in localId 也會拋 |

---

## 7. 超出 PR-4 範圍（明確不做）

- `SettingsPage` global shell 改造（PR-2）
- `WorkspaceSettingsPage` shell 改造（PR-3）
- 六 sub-page 轉為 module-owned declaration（決策 4c 明確採 built-in adapter，不搬家）
- Editor `hostConfig.homePath` 用例（PR-5）
- 舊 `globalConfig` / `workspaceConfig` deprecate（PR-5 後）
- Host tab 拖放 / 排序 等 UX 改動
- `HostStore` persist schema 遷移（alpha 階段不處理，決策依記憶 `feedback_no_alpha_migration`）

---

## 8. Commit 規劃

每 commit 綠。

1. **`feat(spa): register six built-in host sub-pages as host contributions`**
   - `register-modules.tsx` 加 `registerBuiltinHostSections()` + test（§3.3）
2. **`feat(spa): HostPage + HostSidebar read new contribution registry`**
   - `HostPage.tsx` / `HostSidebar.tsx` + `HostPage.test.tsx`
3. **`refactor(spa): isHostSubPage dynamic via contribution registry`**
   - `host-routes.ts` + test
4. **`test(spa): cross-store rehydrate order invariants (#541)`**
   - `host-lifecycle.test.ts` 擴充（若需要連帶改 source 則拆 refactor + test 兩 commit）

共 4 commits。

---

## 9. 與其他 PR 的關聯

- **依賴**：PR-1（已 merged）+ **PR-2**（本 PR 的 built-in host adapter 沿用 PR-2 的 dispatch-flushed pattern + pending buffer 機制；若 PR-2 尚未 merge，PR-4 的 built-in adapter 會被 `dispatchSettingsContributions()` 的 `clearContributions()` 清掉）
- **不依賴**：PR-3（shell 檔案不重疊，但 `register-modules.tsx` 有 rebase 衝突 — 見 §9 rebase 備註）
- **rebase 衝突點**（若與 PR-3 並行）：
  - `spa/src/lib/register-modules.tsx` — PR-3 拔 reserved 兩行；PR-4 加 built-in host sections 六行。位置不同，機械合併可行
- **被依賴**：PR-5（Editor `hostConfig.homePath` 需要 PR-4 的動態 subPage 機制與 host shell render 新 contribution）
- **順風解決**：#538（Host 層 render-level smoke）/ #541（cross-store rehydrate order，具體 harness）
