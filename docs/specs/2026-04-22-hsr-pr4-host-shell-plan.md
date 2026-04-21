# HSR PR-4：Host Settings Shell + Built-in Sub-page Adapter — Implementation Plan

> 日期：2026-04-22
> 狀態：Ready for Implementation
> 主 spec：`2026-04-21-settings-contribution-registry-design.md`
> 決策對齊：4c（HostPage registry-driven + 六子頁走 built-in adapter registration）
> PR 系列：PR-1 ✅ / PR-2 / PR-3 / **PR-4（本文件）** / PR-5

---

## 1. 範圍

**Shell 改造 + built-in adapter registration**。`HostPage` / `HostSidebar` / `host-routes` 改為 registry-driven；六個既有 sub-page（`overview` / `sessions` / `hooks` / `agents` / `uploads` / `logs`）**不改內部程式碼**，透過 built-in adapter 在 `registerBuiltinModules()` 尾段以 `moduleId: '_builtin.host'` 一次註冊進新 registry（走相同 `registerSettingsContribution` contract，但標記為 built-in 來源）。

`ctx.hostId` 由 shell 依 route resolution 結果注入（§5.3 rule 2）；`ctx.scope: 'host'`。

本 PR 順帶驗證 **#541**（cross-store rehydrate order）：`host-lifecycle.test.ts` 擴充測試，確保 `hostWasRecreated` 判定在 `HostStore` 與 `useHostSettingsStore` 兩種 rehydrate 順序下皆正確。

PR-4 結束時：
- `HostPage` 的 sub-page 渲染透過 `listContributions('host')` + ctx 注入
- 六子頁以 `_builtin.host.<id>` 存在於 registry，與任何 future module-contributed host section 走同一條 render 路徑
- URL `/hosts/:hostId/:subPage` 的 `subPage` 合法性判定改為對 registry 查詢
- 新 module 可宣告 `settings: [{ scope: 'host', ... }]` 並出現在 `HostPage` 左側 sidebar（module owner 自定 header / 分組策略由 sidebar 實作決定）
- `removeHost()` cascade 對 `useHostSettingsStore` 的清理 PR-1 已完成；本 PR 驗回歸

---

## 2. 檔案清單

### 修改
- `spa/src/components/HostPage.tsx`
  - `renderContent()`：從 switch 改為 `const contribution = getContribution(`_builtin.host.${selection.subPage}`)` 或更一般化 `listContributions('host').find(c => c.localId === selection.subPage)`
  - 找到 contribution 後以 `<contribution.component ctx={{ scope: 'host', hostId: selection.hostId }} />` render
  - fallback（contribution 不存在）：render `no_host_selected` 或等效空態
- `spa/src/components/hosts/HostSidebar.tsx`
  - 移除硬編 `SUB_PAGES` const
  - `const subPages = listContributions('host')` — 排序已保證
  - 每項顯示 `t(c.labelKey)`（現 `labelKey` 在 contribution 內；與舊 `SUB_PAGES[].labelKey` 一致）
  - `onSelect(hostId, subPage)` 參數仍用 `localId`（即現在的 `'overview'` / `'sessions'` 等值）
- `spa/src/lib/host-routes.ts`
  - `HOST_SUB_PAGES` 移除或保留為 fallback（implementation choice）
  - `isHostSubPage(value)` 改為 `listContributions('host').some(c => c.localId === value)`
  - 若保留 `HOST_SUB_PAGES`：只當 SSR / 極早期 bootstrap 時期 fallback（實務上 `registerBuiltinModules()` 在 React render 前就跑，通常不需要 fallback）
- `spa/src/lib/register-modules.tsx`
  - 結尾新增 `registerBuiltinHostSections()` pass（或直接 inline）：
    - 依序 `registerSettingsContribution({ id: '_builtin.host.overview', moduleId: '_builtin.host', localId: 'overview', scope: 'host', order: 0, labelKey: 'hosts.overview', component: wrap(OverviewSection) })`
    - 同樣六項：sessions (1) / hooks (2) / agents (3) / uploads (4) / logs (5)
    - `wrap(Section)` = `(props: { ctx: SettingsContext }) => props.ctx.scope === 'host' ? <Section hostId={props.ctx.hostId} /> : null`
  - 此 pass 為 internal — `registerSettingsContribution` `@internal` JSDoc 允許 `_builtin.*` 內部 callsite（#539 PR-2 已落地）
- `spa/src/lib/host-lifecycle.ts`（僅測試擴充，若現有邏輯未觸及 cross-store order 則不動 source）
- `spa/src/lib/host-lifecycle.test.ts`（擴充 #541 驗證）

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

### 3.4 `host-lifecycle.test.ts` 擴充（#541）

| 類別 | 測試項 | 預期 |
|---|---|---|
| Rehydrate order A | 模擬 HostStore 先 rehydrate 完成、`useHostSettingsStore` 後 → 刪除 + undo 情境下 `hostWasRecreated` 判定正確 | 通過 |
| Rehydrate order B | 模擬 `useHostSettingsStore` 先、HostStore 後 → 同上 | 通過 |
| Cascade 回歸 | 刪除 host → `useHostSettingsStore.get(hostId, moduleId)` 為 undefined（PR-1 功能）| 通過 |
| Undo 回歸 | 在 cascade clear 後的 undo window 內 undo → host 與其 hostSettings 均回復（PR-1 功能）| 通過 |

若 rehydrate order 測試發現 `hostWasRecreated` 需要改邏輯（e.g. 考慮 `useHostSettingsStore` 中該 host 的 key 是否存在），則加 source 改動（`host-lifecycle.ts`），並追加測試覆蓋新邏輯。

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

- **依賴**：PR-1（已 merged）
- **不依賴**：PR-2 / PR-3（三頁 shell 彼此獨立）
- **被依賴**：PR-5（Editor `hostConfig.homePath` 需要 PR-4 的 HostPage shell 能顯示 module-contributed host section）
- **順風解決**：#538（Host 層）/ #541（cross-store rehydrate order）
