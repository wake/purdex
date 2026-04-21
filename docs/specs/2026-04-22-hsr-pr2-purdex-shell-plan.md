# HSR PR-2：Purdex Settings Shell + Legacy Adapter — Implementation Plan

> 日期：2026-04-22
> 狀態：Ready for Implementation
> 主 spec：`2026-04-21-settings-contribution-registry-design.md`
> 決策對齊：1c（adapter-only）+ 2b（既有 section 不搬家）+ 3b（舊 API deprecate 時點）
> PR 系列：PR-1 ✅ / **PR-2（本文件）** / PR-3 / PR-4 / PR-5

---

## 1. 範圍

**Shell 改造 + legacy adapter**。`SettingsPage` 的 `GlobalSettingsPage` 改讀新 registry；舊 `settings-section-registry` 改為薄 adapter：`registerSettingsSection()` 內部轉呼 `registerSettingsContribution()`；`getSettingsSections()` 改為對新 registry 的 filtered view。既有 7 個 built-in section 內部程式碼**零改動**（決策 2b）。

同步完成 **#539**：把 `registerSettingsContribution` / `clearContributions` 收斂為 internal API（僅供 adapter + register pass + test 呼叫；外部 module 不得直接呼叫）。

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
  - 改吃 `listContributions('purdex')`
  - `reservedStart` 邏輯保留（因為 reserved section 會在 PR-3 才清，PR-2 期間仍可能出現無 `component` 的 contribution；但新 registry 的 `component` 是 required，所以實務上不會有 reserved 進 registry — adapter 在組 declaration 時若原 def `component` 為 undefined，**跳過註冊**，由 PR-3 shell 改造時一併拔除 reserved 顯示）
  - `label` → `labelKey`（其實只是欄位 rename，值原本就是 i18n key）
- `spa/src/lib/settings-section-registry.ts`（adapter 化）
  - `registerSettingsSection(def)`：組 `SettingsContributionDeclaration`（`localId = def.id`、`scope: 'purdex'`、`order = def.order`、`labelKey = def.label`、`component = wrapLegacyComponent(def.component)`）+ `moduleId = '_builtin.legacy-section'`，呼叫 `registerSettingsContribution({ ...decl, moduleId, id: `${moduleId}.${def.id}` })`
  - 若 `def.component` 為 `undefined` → **跳過註冊**（避免新 registry required field 衝突；reserved section 的 UI 由 PR-3 sidebar 改造時明確處理）
  - `getSettingsSections()`：`listContributions('purdex').filter(c => c.moduleId === '_builtin.legacy-section').map(c => ({ id: c.localId, label: c.labelKey, order: c.order, component: unwrapLegacyComponent(c.component) }))`
  - `clearSettingsSectionRegistry()`：呼叫 `clearContributions()`（registry test-only）或只清 legacy namespace（取決於是否只測 legacy adapter — 安全作法：只清 `_builtin.legacy-section.*`）
  - `wrapLegacyComponent(Comp)`：`(props: { ctx: SettingsContext }) => <Comp />`（忽略 ctx — legacy component 不吃 ctx）
  - `unwrapLegacyComponent(Wrapped)`：adapter 內部把 wrap 時原 Comp 掛在 `Wrapped.__legacyComponent` 取回；或直接存 map — 任一皆可，一致即可
- `spa/src/lib/settings-contribution-registry.ts`（#539 internal 收斂）
  - `registerSettingsContribution` / `clearContributions` 加 `/** @internal */` JSDoc tag
  - Re-export 從 `module-registry.ts` 拿掉（module 作者只透過 `ModuleDefinition.settings` 宣告，不得繞過 register pass 直接呼叫）
  - `listContributions` / `getContribution` 保持 public（shell 消費）
  - 若有外部 callsite（PR-1 後除 adapter 外理論應為 0 個），lint rule 或 ESLint `no-restricted-imports` 擋（optional，有則做）

### 新增
- `spa/src/components/SettingsPage.test.tsx`
  - Render-level smoke：註冊 fake contribution（scope: 'purdex'）→ `SettingsPage` render 後 sidebar 顯示該 label、active 時 content 區 render 該 component
  - URL routing：`/settings/<localId>` → 該 section active；URL subsection 可讀 `useSettingsRoute()`
  - `lastSection` 保留（remount 仍回到前次 section）
  - adapter 自動遷移：先呼 `registerSettingsSection({ id: 'test-legacy', label: '...', order: 99, component: Comp })` → `listContributions('purdex')` 回傳該項目
  - 關閉 #538 首個 render-level 目標
- `spa/src/lib/settings-section-registry.test.ts`（新增 adapter 行為測試）
  - adapter round-trip：`registerSettingsSection` → `getSettingsSections` 回傳原 shape
  - adapter 與 `listContributions('purdex')` 一致性（同一批資料）
  - `component` undefined 時跳過註冊（無 throw）
  - `clearSettingsSectionRegistry` 只清 legacy namespace，不清其他 module 的 contribution

### 不動
- 7 個既有 built-in section 檔（`AppearanceSection` / `TerminalSection` / `SyncSection` / `LinkDetectionSection` / `DevEnvironmentSection` / `TmuxAgentSection` 等 — 確切數量以當下 `register-modules.tsx` 註冊為準）
- `spa/src/lib/register-modules.tsx`（section 宣告照舊透過舊 `registerSettingsSection` 呼叫；adapter 會接住）
- `WorkspaceSettingsPage.tsx` / `HostPage.tsx` / `HostSidebar.tsx` / `host-routes.ts`
- 三層 settings store（PR-1 已建）
- PR-1 新 registry 的 `settings-contribution-types.ts`

---

## 3. Test Case Matrix

### 3.1 `settings-section-registry.test.ts`（adapter）

| 類別 | 測試項 | 預期 |
|---|---|---|
| Adapter round-trip | `registerSettingsSection({ id, label, order, component })` → `getSettingsSections()` 回傳原 shape | 通過 |
| Adapter 轉遷 | 註冊後 `listContributions('purdex')` 含該項（`moduleId='_builtin.legacy-section'`、`localId=id`、`labelKey=label`） | 通過 |
| Order 保留 | 多項註冊後 `getSettingsSections` / `listContributions` 均依 order 升冪 | 通過 |
| Reserved skip | `registerSettingsSection({ ..., component: undefined })` → 新 registry 內**無該項**，不 throw | 通過 |
| Namespace 隔離 | 另一 module（透過 `ModuleDefinition.settings`）註冊 `scope: 'purdex'` 的 contribution 不出現在 `getSettingsSections()` | 通過 |
| Clear | `clearSettingsSectionRegistry()` 只清 legacy namespace | 通過 |
| Identity | 同 object 重複呼叫 `registerSettingsSection` → 不 throw（對應 PR-1 §6.2 identity check） | 通過 |

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
| Adapter 整合 | 只透過舊 `registerSettingsSection` 註冊 → `<SettingsPage>` 仍 render 該 section | 通過 |
| Workspace 分派 | `props.pane.content.scope = { workspaceId: 'wsA' }` → render `WorkspaceSettingsPage`，不走 `GlobalSettingsPage` | 通過（保留既有） |

### 3.3 `register-modules.test.ts` 擴充

| 類別 | 測試項 | 預期 |
|---|---|---|
| 端對端 | 跑 `registerBuiltinModules()` 後，現有 7 個 built-in section 皆在 `listContributions('purdex')` 中，`moduleId='_builtin.legacy-section'` | 通過 |
| 無 regression | I1 guard 對既有 module fixture 仍不 throw | 通過 |

### 3.4 視覺回歸（肉眼 / 手動）

- `cd spa && pnpm dev` 起站
- 開 `/settings`：sidebar 上 7 個 section 顯示順序、label、active highlight 與 `main` 分支一致
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
| Adapter 的 wrap / unwrap 破壞 React component identity（影響 memo / render optimization） | 中 | wrap 時把原 component 存在 `WeakMap<WrappedComp, OrigComp>` 或 static field，unwrap 時穩定取回；測試覆蓋 `getSettingsSections().component === 原 component` 不強求，只求 render 正確 |
| `_builtin.legacy-section.<id>` 撞到未來真 module 的 id | 低 | `_builtin.*` 前綴保留為 built-in namespace；I1 / I2 對 moduleId 本身不做格式限制，但文件註明 `_builtin.*` 保留 |
| `SettingsRouteContext` 行為被無意破壞 | 中 | §3.2 subsection 測試覆蓋 |
| Reserved section（`component` undefined）原本 sidebar 顯示「coming_soon」灰字 — adapter skip 後 UI 少一格 | 中 | 驗 `main` 分支 reserved section 目前確實顯示於 sidebar（`workspace` / `module-config`）；若是 → PR-2 **暫時保留**現有行為（`SettingsSidebar` 仍吃舊 `getSettingsSections()` 或特殊 branch），待 PR-3 決策 5a 一併清；不提前在 PR-2 清 |
| `@internal` JSDoc 沒實際效力（TypeScript 不強制） | 低 | 加註 + optional lint rule（`no-restricted-imports` pattern），不阻擋 PR |

**風險備註**：reserved section 處理有兩條路。若 PR-2 要保持 UI 100% 一致（包含 coming_soon 灰字），`SettingsSidebar` 內部仍需透過舊 API 取得 reserved 項目；adapter 保留 `component: undefined` 項目於 legacy namespace 但不轉進新 registry — 這條路讓 PR-2 完全無 UX 變動。**本 plan 採此路**：adapter `registerSettingsSection` 時，若 `component` 為 undefined，項目仍存在 legacy local buffer（不進新 registry），`getSettingsSections()` 同時回傳 new registry + local reserved，`SettingsSidebar` 原 reservedStart 邏輯照舊。PR-3 清完 reserved 後 local buffer 也可拔。

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

1. **`docs: HSR spec v3 + PR-2/3/4/5 plans`**（若尚未於前置 docs PR merge 則此 commit 納入；若已 merge 可省略）
2. **`feat(spa): adapt legacy settings-section-registry to new contribution registry`**
   - `settings-section-registry.ts` adapter 化 + `settings-section-registry.test.ts` 新增
3. **`feat(spa): SettingsPage reads new contribution registry`**
   - `SettingsPage.tsx` + `SettingsSidebar.tsx` 改吃新 registry + `SettingsPage.test.tsx` 新增
4. **`refactor(spa): mark registerSettingsContribution as internal (#539)`**
   - JSDoc `@internal` + re-export 調整
5. **（若 adapter PR scope 大）** 拆分 reserved section 兼容處理為獨立 commit

共 3–5 commits。

---

## 9. 與其他 PR 的關聯

- **依賴**：PR-1（已 merged）
- **不依賴**：PR-3 / PR-4（三頁 shell 改造彼此獨立）
- **被依賴**：PR-5（Editor 首個用例需要 PR-2 的 shell 已完成，才能在 `/settings/editor` 看到新 section — 但 PR-5 的 Editor `homePath` 是 workspace/host scope，不是 purdex scope，因此嚴格來說也不依賴 PR-2）
- **順風解決**：#538（Purdex 層）/ #539（internal 收斂）
