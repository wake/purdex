# HSR PR-2：Purdex Settings Shell + Legacy Adapter — Implementation Plan

> 日期：2026-04-22（v3.1 post-codex-review Round 3 task-mo90eijb-mk3gfg）
> 狀態：Ready for Implementation
> 主 spec：`2026-04-21-settings-contribution-registry-design.md`
> 決策對齊：1c（adapter-only, **dispatch-flushed**）+ 2b（既有 section 不搬家）+ 3b（舊 API deprecate 時點）
> PR 系列：PR-1 ✅ / **PR-2（本文件）** / PR-3 / PR-4 / PR-5
> v3 收斂：Round 1 Finding 1/6 → v2 修；Round 2 Finding 6 partial + N1 + N3 → v3 修
> v3.1 收斂：Round 3 MED（§2/§3.2 test 敘述仍把 adapter 寫成「直接可見」與 pending-buffer 衝突、active section 數硬編 6/7）→ 本版補上 `dispatchSettingsContributions()` 敘述、去掉硬編數字改為「至少 6 + 條件式」

---

## 1. 範圍

**Shell 改造 + legacy adapter（dispatch-flushed）**。`SettingsPage` 的 `GlobalSettingsPage` 改讀新 registry；舊 `settings-section-registry` 改為 pending-buffer adapter（**不直接呼叫** `registerSettingsContribution()`，否則 `dispatchSettingsContributions()` 的 `clearContributions()` 會把 legacy 項整批清掉 — codex review Finding 1 的硬約束）；改 push 到 `pendingLegacyContributions` module-scope buffer，由 `dispatchSettingsContributions()` 統一 drain + register；`getSettingsSections()` 改為對新 registry `moduleId === '_builtin.legacy-section'` 的 filtered view。既有 7 個 built-in section 內部程式碼**零改動**（決策 2b）。

同步完成 **#539**：把 `registerSettingsContribution` / `clearContributions` 降級為 `@internal`（僅 `dispatch-settings-contributions.ts` 與 test 呼叫；legacy adapter 也不直接呼叫，走 pending buffer）。

PR-2 結束時：
- `GlobalSettingsPage` 透過 `listContributions('purdex')` + `SettingsContext({ scope: 'purdex' })` 渲染
- 既有 7 個 section UI 外觀、order、URL 行為 100% 與 main 一致
- 舊 `registerSettingsSection` 所有 callsite 無需改碼即自動進新 registry
- `WorkspaceSettingsPage` / `HostPage` 不在本 PR scope（PR-3 / PR-4 接手）
- `workspace` reserved / `module-config` 空頁不在本 PR 清（PR-3 決策 5a）

---

## 2. 檔案清單

### 修改
- `spa/src/components/SettingsPage.tsx`
  - `GlobalSettingsPage` 內部 `getSettingsSections()` 改為 `listContributions('purdex')`
  - 新增 `const ctx: SettingsContext = { scope: 'purdex' }` 傳入 `<ActiveComponent ctx={ctx} />`
  - 排序依舊 by `order` 升冪（新 registry 已保證）
  - URL 分派 / `lastSection` / `SettingsRouteContext` 邏輯保留
- `spa/src/components/settings/SettingsSidebar.tsx`
  - 改吃 `listContributions('purdex')`（active sections，來源皆為新 registry — 含 module-declared 與 legacy adapter flush 後的 `_builtin.legacy-section.*`）
  - 另呼叫新 export `listReservedItems()`（來自 `settings-section-registry.ts`，回傳當下 `pendingReservedItems` 的 read-only snapshot）補顯示 reserved coming_soon 項
  - Sidebar 內部以統一資料流合併兩陣列，按 `order` 升冪一次排序（不用 `getSettingsSections()`）；`getSettingsSections()` 保留給尚未遷的外部 callsite（read-only 過渡，由 PR-3 清除 reserved 後再評估拔除）
  - `reservedStart` 分隔線邏輯保留（`component: undefined` 的項分到 reserved 區）；PR-3 清 reserved 後該分支成為 dead code，由 PR-3 移除
  - `label` → `labelKey`（其實只是欄位 rename，值原本就是 i18n key）
- `spa/src/lib/settings-section-registry.ts`（adapter 化 + pending buffer）
  - 新增 module-scope `let pendingLegacyContributions: SettingsContributionDeclaration[] = []`（active，順序保留 push 序）
  - 新增 module-scope **`const pendingReservedItems = new Map<string, SettingsSectionDef>()`**（N1：keyed by `def.id`，upsert 語義，HMR / 重複 register 不累積）— reserved `component: undefined` 不進新 registry，僅保留在此 buffer 供 sidebar 顯示 coming_soon（Finding 6 統一解）
  - `registerSettingsSection(def)`：
    - 若 `def.component` 為 undefined → `pendingReservedItems.set(def.id, def)`（upsert；同 id 重 register 覆寫，不重複）
    - 否則 → 組 `SettingsContributionDeclaration`（`localId = def.id`、`scope: 'purdex'`、`order = def.order`、`labelKey = def.label`、`component = wrapLegacyComponent(def.component)`） push 到 `pendingLegacyContributions`（`moduleId = '_builtin.legacy-section'`；完整 `id` 由 dispatch 時組）；同 `def.id` 若已在 buffer 中，**覆寫**該項（upsert；避免 HMR 累積）
    - **不呼叫** `registerSettingsContribution`
  - 新增 export `drainLegacyContributionQueue(): SettingsContributionDeclaration[]`：回傳 pending buffer 的 deep copy 並清空 buffer（dispatch pass 呼叫）
  - 新增 export `listReservedItems(): readonly SettingsSectionDef[]`：回傳 `pendingReservedItems` 的 snapshot array（依 `order` 升冪），供 `SettingsSidebar` 顯示 coming_soon（讀取介面，不提供寫入）
  - 新增 HMR dispose hook（`register-modules.tsx` 既有 HMR 流程內呼叫）：`clearLegacyPending()` 清 `pendingLegacyContributions` **與** `pendingReservedItems`（N1：reserved 必須一併清，否則 HMR 重跑時 reserved 會被下輪 upsert 覆寫但孤兒 key 殘留；用 upsert + dispose 雙保險）
  - `getSettingsSections(): SettingsSectionDef[]`：將 `listContributions('purdex').filter(c => c.moduleId === '_builtin.legacy-section').map(toLegacyShape)` 與 `Array.from(pendingReservedItems.values())` 合併，依 `order` 升冪排序回傳（**保留給尚未遷的外部 callsite 過渡**；`SettingsSidebar` 改走 `listContributions('purdex') + listReservedItems()` 明確兩路，不經過本 API）
  - `clearSettingsSectionRegistry()`：清 `pendingLegacyContributions` + `pendingReservedItems`（僅 test 用）
  - `wrapLegacyComponent(Comp)`：`(props: { ctx: SettingsContext }) => <Comp />`（忽略 ctx；legacy component 不吃 ctx）
  - `toLegacyShape(c)`：將 contribution 還原回 `{ id, label, order, component }` shape（`component` 透過 `legacyComponentMap` WeakMap 取原 component reference，避免 wrap 破壞 React identity）
- `spa/src/lib/dispatch-settings-contributions.ts`（配合 adapter）
  - `dispatchSettingsContributions(modules)` 的 Phase 2，`clearContributions()` 後除了 register module-declared batch，還要：
    - `const legacyBatch = drainLegacyContributionQueue()`
    - 將 legacy declarations 補上 `moduleId + id`，透過相同 `assertValidSettingsContribution` + 重複 id 檢查，加入 `seenIds`
    - `registerSettingsContribution(...)` 兩批合計
  - 新增 invariant：module-declared 與 legacy 間不得出現同 `id`（理論上不會撞，因為 `moduleId` 不同；但防禦檢查）
- `spa/src/lib/settings-contribution-registry.ts`（#539 internal 收斂）
  - `registerSettingsContribution` / `clearContributions` / `assertValidSettingsContribution` 加 `/** @internal */` JSDoc tag
  - Re-export 從 `module-registry.ts` 拿掉（module 作者只透過 `ModuleDefinition.settings` 宣告；legacy adapter 透過 pending buffer；兩條路之外不得寫入）
  - `listContributions` / `getContribution` 保持 public（shell 消費）
  - ESLint `no-restricted-imports`（optional）：僅允許 `dispatch-settings-contributions.ts` / `settings-section-registry.ts` / 測試檔 import `registerSettingsContribution` / `clearContributions`

### 新增
- `spa/src/components/SettingsPage.test.tsx`
  - Render-level smoke：**先** register fake contribution + **呼叫 `dispatchSettingsContributions([...modules])`**（或透過 `registerBuiltinModules()` 完整 bootstrap fixture，其內部已含 dispatch）→ `SettingsPage` render 後 sidebar 顯示該 label、active 時 content 區 render 該 component
  - URL routing：`/settings/<localId>` → 該 section active；URL subsection 可讀 `useSettingsRoute()`
  - `lastSection` 保留（remount 仍回到前次 section）
  - adapter 自動遷移：`registerSettingsSection({ id: 'test-legacy', label: '...', order: 99, component: Comp })` → `dispatchSettingsContributions([])` → `listContributions('purdex')` 回傳該項目（dispatch 是唯一 flush 入口）
  - 關閉 #538 首個 render-level 目標
- `spa/src/lib/settings-section-registry.test.ts`（新增 adapter 行為測試）
  - adapter round-trip：`registerSettingsSection(x)` → `dispatchSettingsContributions([])` → `getSettingsSections()` 回傳原 shape（**dispatch 後** active 項來自新 registry filter + reserved 來自 `pendingReservedItems`）
  - adapter 與 `listContributions('purdex')` 一致性：dispatch 後同一批資料（active 項目）；dispatch 前兩者都空（pending buffer 未 flush）
  - `component` undefined 時改進 reserved map（無 throw）；`listReservedItems()` 含該項
  - `clearSettingsSectionRegistry` 只清 pending buffer（legacy namespace），不清其他 module 的 contribution（走新 registry 的 `clearContributions`）

### 不動
- 既有 built-in section 檔（`AppearanceSection` / `TerminalSection` / `InterfaceSectionHost` / `SyncSection` / `ModuleConfigSection` / `BufferListSection` 等；conditional: `ElectronSection` / `DevEnvironmentSection` / `TmuxAgentMonitorSection` — 確切清單以當下 `register-modules.tsx` 註冊 + caps flag 為準）
- `spa/src/lib/register-modules.tsx`（section 宣告照舊透過舊 `registerSettingsSection` 呼叫；adapter 會接住）
- `WorkspaceSettingsPage.tsx` / `HostPage.tsx` / `HostSidebar.tsx` / `host-routes.ts`
- 三層 settings store（PR-1 已建）
- PR-1 新 registry 的 `settings-contribution-types.ts`

---

## 3. Test Case Matrix

### 3.1 `settings-section-registry.test.ts`（adapter + pending buffer）

| 類別 | 測試項 | 預期 |
|---|---|---|
| Pending buffer | `registerSettingsSection({ id, label, order, component })` 後 `listContributions('purdex')` **空**（因 dispatch 尚未跑） | 通過 |
| Pending buffer | 同上後呼叫 `drainLegacyContributionQueue()` 回傳含該項的 declaration；再呼叫一次回傳空陣列 | 通過 |
| Dispatch flush | 完整流程：`registerSettingsSection(x)` → `dispatchSettingsContributions([module])` → `listContributions('purdex')` 含 `_builtin.legacy-section.x` | 通過 |
| Dispatch survive | `registerSettingsSection(x)` → `dispatchSettingsContributions([])` → `listContributions('purdex')` 仍含該項（legacy drain 後 clear 重 register，**不會被清**） | **關鍵 — Finding 1 回歸** |
| Adapter round-trip | dispatch 後 `getSettingsSections()` 回傳 active 項目原 shape（id / label / order / component reference 等於原 def.component） | 通過 |
| React identity | dispatch 後 `getSettingsSections()[i].component === 原 passed-in Comp`（透過 `legacyComponentMap` unwrap） | 通過 |
| Order 保留 | 多項 active + reserved 混合註冊後 `getSettingsSections()` 依 order 升冪交錯回傳 | 通過 |
| Reserved buffer | `registerSettingsSection({ ..., component: undefined })` → dispatch 後新 registry **無** 該項；`listReservedItems()` **有** 該項，`component` 為 undefined；`getSettingsSections()` 合併 active + reserved 也回該項（過渡 API） | **Finding 6 — 統一策略驗證** |
| Reserved upsert | 連續 `registerSettingsSection({ id: 'r1', component: undefined, order: 10, label: 'A' })` × N 次（N≥3）→ `listReservedItems()` 長度 = 1；若最後一次改 `order: 5` → 該項 `order === 5`（upsert 覆寫，N1 驗證） | **N1 — reserved 不累積** |
| Active upsert | 連續 `registerSettingsSection({ id: 'a1', component: Comp1 })` → `registerSettingsSection({ id: 'a1', component: Comp2 })` → dispatch → `listContributions('purdex')` 中 id `_builtin.legacy-section.a1` 只 1 項，component unwrap 後 `=== Comp2`（N1 驗證 active 也不累積） | **N1 — active 不累積** |
| HMR dispose | `registerSettingsSection({ component: undefined })` → `clearLegacyPending()` → `listReservedItems().length === 0`；同樣對 active buffer → `drainLegacyContributionQueue()` 已回空 | **N1 — HMR dispose 雙 buffer 清** |
| HMR re-run | `registerSettingsSection(x)` → dispatch → `clearLegacyPending()` → 再次 `registerSettingsSection(x)` → dispatch → 不 throw，最終一份（upsert + dispose 雙保險） | 通過 |
| Namespace 隔離 | 另一 module 透過 `ModuleDefinition.settings` 宣告 `scope: 'purdex'` → dispatch 後 `getSettingsSections()` **不**回傳該項（filter `moduleId === '_builtin.legacy-section'`） | 通過 |
| Clear | `clearSettingsSectionRegistry()` 清 pending buffer（不碰新 registry；dispatch 後的結果由 `clearContributions()` 負責） | 通過 |
| Cross-id guard | module 宣告 `moduleId='foo', localId='appearance'` + legacy adapter 收 `id='appearance'` → dispatch 後兩者 id 不同（`foo.appearance` vs `_builtin.legacy-section.appearance`），無衝突 | 通過 |

### 3.2 `SettingsPage.test.tsx`（render-level）

| 類別 | 測試項 | 預期 |
|---|---|---|
| Render | 註冊 fake contribution → `<SettingsPage>` render 後 sidebar 含 label、content 區 render fake component | 通過 |
| Ctx 注入 | fake component 讀 `props.ctx.scope` === `'purdex'` | 通過 |
| Order | 多項註冊 → sidebar 順序依 order | 通過 |
| URL 同步 | 初始 URL `/settings/<fakeId>` → 該 section active | 通過 |
| URL 自癒 | URL 指 invalid section → redirect 到 default | 通過（保留既有行為） |
| URL 自癒 | URL `>2` 段 → trim 到 `/settings/<section>` | 通過 |
| Subsection | fake component 透過 `useSettingsRoute()` 讀 subsection | 通過 |
| `lastSection` 持久 | remount `<SettingsPage>` 仍回到前次 active | 通過 |
| Adapter 整合 | 透過舊 `registerSettingsSection` 註冊 + 呼叫 `dispatchSettingsContributions([])` → `<SettingsPage>` 仍 render 該 section（bootstrap fixture 走完整 `registerBuiltinModules()` 或測試顯式 dispatch） | 通過（保留既有） |
| Workspace 分派 | `props.pane.content.scope = { workspaceId: 'wsA' }` → render `WorkspaceSettingsPage`，不走 `GlobalSettingsPage` | 通過（保留既有） |

### 3.3 `register-modules.test.ts` 擴充

| 類別 | 測試項 | 預期 |
|---|---|---|
| 端對端 | 跑 `registerBuiltinModules()` 後，現有 **active** built-in section 皆在 `listContributions('purdex')`，`moduleId='_builtin.legacy-section'`；至少 6 項（appearance / terminal / interface / sync / module-config / editor-buffers；workspace 為 reserved 不進 registry；conditional: electron / dev-environment / tmux-agent-monitor 視 caps / `import.meta.env.DEV` 而定）| 通過 |
| Reserved 顯示 | `listReservedItems()` 含 workspace reserved（`component: undefined`，可被 sidebar render 為 coming_soon）；數量為 1（目前僅 workspace reserved） | 通過 |
| 無 regression | I1 guard 對既有 module fixture 仍不 throw | 通過 |
| Dispatch 時序 | 對 `registerBuiltinModules()` 流程做 integration test：`registerModule` + `registerSettingsSection` 都跑完後 `dispatchSettingsContributions()` 最後呼叫，確認 legacy 項不被清 | 通過 |

### 3.4 視覺回歸（肉眼 / 手動）

- `cd spa && pnpm dev` 起站
- 開 `/settings`：sidebar 的 active sections + reserved 分隔線排序、label、active highlight 與 `main` 分支一致（實際項數視 dev/caps 而異；以 `main` 同環境為基準 diff）
- 點每個 section：右側 content 正常渲染（無 console error / 白屏）
- `/settings/<section>/<subsection>`：subsection context 仍可用（挑一個目前有用 subsection 的 section 驗，如 `/settings/sync/history`）
- Workspace 打開 settings tab（`pane.content.scope` object）：仍走 `WorkspaceSettingsPage`（本 PR 不動）

---

## 4. 實作順序（TDD）

1. **紅**：寫 §3.1 adapter 測試（僅對 adapter 層）
2. **綠**：改 `settings-section-registry.ts` 為 adapter
3. **紅**：寫 §3.2 `SettingsPage.test.tsx`
4. **綠**：改 `SettingsPage.tsx` `GlobalSettingsPage` + `SettingsSidebar.tsx` 吃新 registry；構造 `ctx` 注入
5. **紅**：寫 §3.3 register-modules 端對端擴充測試
6. **綠**：驗證 `registerBuiltinModules` 走 adapter 後仍 pass；若 I1 在 adapter scope 有誤擋，調整 I1 判斷（理論上不需，因為 built-in section 走 legacy adapter 的 moduleId 是 `_builtin.legacy-section`，與 module 宣告的 moduleId 不同，I1 不會觸發）
7. **#539 收斂**：加 `@internal` + 調整 re-export
8. **驗證**：`cd spa && pnpm exec vitest run` / `pnpm run lint` / `pnpm run build` 三綠
9. **手動**：§3.4 視覺回歸

---

## 5. 驗收條件

- [ ] §3.1–3.3 測試全綠
- [ ] `cd spa && pnpm exec vitest run` 全綠
- [ ] `cd spa && pnpm run lint` 全綠
- [ ] `cd spa && pnpm run build` 全綠
- [ ] §3.4 手動視覺回歸無差異
- [ ] 現有 7 個 built-in section 在 `listContributions('purdex')` 出現，`moduleId='_builtin.legacy-section'`
- [ ] `registerSettingsContribution` 標為 `@internal`；非 adapter / register-pass / test 的 callsite 為 0（`rg "registerSettingsContribution"` 人工 audit）
- [ ] Codex 兩輪 review（標準 + 三路對抗）無 critical / P1 未修項
- [ ] #538 關閉或標記為「部分關閉 — Purdex 層已補 render smoke，待 PR-3/4 補 Workspace / Host 層」

---

## 6. 風險

| 風險 | 發生可能 | 緩解 |
|---|---|---|
| Adapter wrap / unwrap 破壞 React component identity（影響 memo） | 中 | wrap 時 `legacyComponentMap: WeakMap<WrappedComp, OrigComp>`；`getSettingsSections()` 的 `toLegacyShape` 透過 map 取原 component；§3.1 「React identity」測試明確 assert `component === 原 Comp` |
| `_builtin.legacy-section.<id>` / `_builtin.host.<id>` 撞到未來真 module 的 id | 低 | `_builtin.*` 前綴保留為 built-in namespace；本 PR 在 `settings-section-registry.ts` 硬編 `'_builtin.legacy-section'` 常數，集中管理；未來引入 namespace guard（禁止 module 作者用 `_builtin.` 開頭的 moduleId）由 #539 延伸追蹤 issue |
| `SettingsRouteContext` 行為被無意破壞 | 中 | §3.2 subsection 測試覆蓋 |
| Reserved section 與 active section 的 order 混排視覺順序改變 | 低 | §3.1「Order 保留 — 多項 active + reserved 混合註冊後依 order 升冪交錯」測試；現況 active/reserved 已依 order 分佈（active order=0/1/2/8/9/11, reserved order=10），交錯順序與 `main` 一致 |
| `@internal` JSDoc 沒實際效力（TypeScript 不強制） | 低 | 加註 + optional `no-restricted-imports`；不阻擋 PR |
| Dispatch pass 被多次呼叫 → legacy buffer drain 後 buffer 空 → 第 2 次 dispatch 後新 registry 只剩 module-declared（legacy 消失） | **高（若不小心實作）** | §3.1「HMR re-run」測試要求：每次 `registerBuiltinModules()` 前端流程 `registerSettingsSection` 會重新 push，所以每次 dispatch 都能 drain 到完整 legacy 批次；HMR 中須保證 `registerSettingsSection` 先於 `dispatchSettingsContributions` 重新跑（這是現況流程 — dispatch 在 `registerBuiltinModules()` 結尾） |

---

## 7. 超出 PR-2 範圍（明確不做）

- `WorkspaceSettingsPage` 改 registry-driven（PR-3）
- `HostPage` / `HostSidebar` / `host-routes` 改 registry-driven（PR-4）
- 清理 reserved `workspace` section 與 `module-config` 空頁（PR-3，決策 5a）
- 既有 7 個 section 搬家為 module-owned declaration（決策 2b — 不在 HSR 系列 scope）
- 舊 `globalConfig` / `workspaceConfig` deprecate（PR-5 後，決策 3b）
- 三層 store `get()` 回傳 immutable snapshot（#540；等 PR-5 前必解，本 PR 可先開 issue 交付）
- `registerSettingsContribution` 完全移除 public export（`@internal` JSDoc 即可；強制移除留到至少確認 0 外部 callsite 之後）

---

## 8. Commit 規劃

每個 commit 必須獨立過 vitest / lint / build（完全綠）。

**N3 修正**：commit 1 要呼叫 `drainLegacyContributionQueue()`，該 export 必須同 commit 提供（否則 commit 1 無法獨立編譯）。策略：commit 1 在 `settings-section-registry.ts` 先 export **no-op stub**（`export function drainLegacyContributionQueue(): SettingsContributionDeclaration[] { return [] }` + `export function listReservedItems(): readonly SettingsSectionDef[] { return [] }` + `export function clearLegacyPending(): void {}`），commit 2 才把這三個 API 接上真實 buffer。commit 1 的 stub 行為等同「legacy queue 一直是空」，既有 `registerSettingsSection` 仍走舊 registry 寫入 — 所以 commit 1 land 時 end-to-end 行為與 `main` 一致，獨立可綠。

1. **`feat(spa): dispatch pass flushes legacy contribution queue (stub)`**
   - `settings-section-registry.ts` 先 export `drainLegacyContributionQueue` / `listReservedItems` / `clearLegacyPending` no-op stub（真實 buffer 在 commit 2）
   - `dispatch-settings-contributions.ts` 加 drain 呼叫 + stub 情境測試（`dispatch-settings-contributions.test.ts` 擴充）
   - 此 commit 前後 end-to-end 行為不變（stub 回空 → dispatch drain 無項目 → 同 `main`）；型別 / lint / vitest 三綠
2. **`feat(spa): settings-section-registry uses pending buffer + reserved upsert map`**
   - `settings-section-registry.ts` 把 commit 1 的 stub 換成真實實作（pending buffer + `Map<string, SettingsSectionDef>` reserved upsert + `clearLegacyPending` 雙清 + `listReservedItems` snapshot + `getSettingsSections` 合併）
   - `settings-section-registry.test.ts` 新增 §3.1 全部案例（含 upsert / HMR dispose / reserved buffer）
   - 此 commit 後 `registerBuiltinModules()` 跑完的 end-to-end 行為與 `main` 一致（existing 6 active + 1 reserved）
3. **`feat(spa): SettingsPage + SettingsSidebar read new contribution registry with ctx`**
   - `SettingsPage.tsx` 改 `listContributions('purdex')` + `ctx: { scope: 'purdex' }` 注入
   - `SettingsSidebar.tsx` 改 `listContributions('purdex') + listReservedItems()` 合併渲染（不走 `getSettingsSections()`）
   - `SettingsPage.test.tsx`（§3.2 全部）
4. **`refactor(spa): mark registerSettingsContribution as internal (#539)`**
   - JSDoc `@internal` + re-export 調整 + optional ESLint rule

共 4 commits。每個皆獨立可綠：
- commit 1：純新增 API stub + dispatch drain 呼叫；stub 回空所以既有流程行為不變
- commit 2：把 stub 替換為真實實作；此 commit 的測試依賴 commit 1 的 drain 整合
- commit 3：只讀 registry + reserved snapshot，不動 registry 寫入
- commit 4：純文檔/型別

---

## 9. 與其他 PR 的關聯

- **依賴**：PR-1（已 merged）
- **必須先於**：
  - PR-3（共改 `SettingsSidebar.tsx` — PR-3 清 `reservedStart` 分支 build on PR-2 新版；共改 `dispatch-settings-contributions.ts` 時序假設）
  - PR-4（共改 `register-modules.tsx` — PR-4 加 `registerBuiltinHostSections()` 假設 dispatch pass 能正確吸收多種 contribution 來源）
- **不依賴**：純 purdex scope 的 `SettingsPage` 改動不影響 host / workspace shell
- **被依賴**：PR-3 / PR-4 / PR-5（都依 PR-2 建立的 dispatch-flushed adapter pattern 與 `@internal` boundary）
- **順風解決**：#538（Purdex 層 render-level smoke）/ #539（internal 收斂 + adapter 為唯一合法 side-channel）
