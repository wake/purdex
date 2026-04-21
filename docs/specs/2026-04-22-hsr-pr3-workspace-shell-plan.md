# HSR PR-3：Workspace Settings Shell + Reserved Cleanup — Implementation Plan

> 日期：2026-04-22（v3 post-codex-review Round 2 task-mo8z6ppx-pj07yk）
> 狀態：Ready for Implementation（**依序 build on PR-2**，不可與 PR-2 並行）
> 主 spec：`2026-04-21-settings-contribution-registry-design.md`
> 決策對齊：5a（PR-3 清 reserved + 空頁）+ 2b（既有 `ModuleConfigSection` 不動）
> PR 系列：PR-1 ✅ / PR-2 / **PR-3（本文件）** / PR-4 / PR-5
> v3 收斂：Round 1 + Round 2 皆對 PR-3 無 finding；僅隨 PR-2 sidebar 敘事更新對齊（reserved buffer 在 PR-2 v3 改為 `Map` upsert，PR-3 清除時一併拔除 map 與 `listReservedItems` export）

---

## 1. 範圍

**Shell 改造 + 清理**。`WorkspaceSettingsPage` 在現有單頁佈局中插入一個「workspace-scoped contributions」區，疊渲染 `listContributions('workspace')`，每個 contribution 以 `ctx: { scope: 'workspace', workspaceId }` 傳入。同步清掉 `register-modules.tsx` 中兩個待清項目：reserved `workspace` section（`order: 10`，無 component）與 `module-config` section（global scope 下的空容器，production 無 `globalConfig` 使用者）。

**保留不動**（決策 2b + 3b）：
- `ModuleConfigSection.tsx`（`WorkspaceSettingsPage` 直接 render 以承接 `workspaceConfig` 舊軌，特別是 `files.projectPath`）
- 舊 `globalConfig` / `workspaceConfig` API 與 `useModuleConfigStore`
- 既有 settings section 內部

PR-3 結束時：
- `WorkspaceSettingsPage` 新增 registry-driven 區，可顯示任何宣告 `scope: 'workspace'` 的 contribution
- `register-modules.tsx` 不再註冊 `workspace` reserved 與 `module-config` section
- SettingsSidebar 在 PR-2 reserved 兼容路徑可拔除（或保留等更晚）
- Workspace 刪除時 `useWorkspaceSettingsStore` 已由 PR-1 cascade 清理（本 PR 不重做）
- URL 無變動（workspace settings 仍透過 `pane.content.scope = { workspaceId }` 進入）

---

## 2. 檔案清單

### 修改
- `spa/src/features/workspace/components/WorkspaceSettingsPage.tsx`
  - 在 `ModuleConfigSection` 下方插入 registry 區塊：遍歷 `listContributions('workspace')`，每個 contribution 以 `<section>` 包 header（`t(c.labelKey)`）+ body（`<c.component ctx={{ scope: 'workspace', workspaceId }} />`）；`disabled(ctx)` 為 true 時 section 隱藏（或顯示禁用樣式 — 任一策略，測試覆蓋）
  - 使用 `useMemo` 避免 list 每次 rerender 重算
  - 排序依 `order` 升冪（registry 已保證）
- `spa/src/lib/register-modules.tsx`
  - 移除 `registerSettingsSection({ id: 'workspace', ..., order: 10 })`（reserved 行）
  - 移除 `registerSettingsSection({ id: 'module-config', ..., order: 8, component: () => <ModuleConfigSection scope="global" /> })`（global scope 無 production 消費者）
  - 若 PR-2 有為 reserved 保留 local buffer 兼容路徑，同步拔除該 buffer
- `spa/src/components/settings/SettingsSidebar.tsx`（build on PR-2 v3 — PR-2 改為讀 `listContributions('purdex')` + `listReservedItems()` 兩路合併；本 PR 拔除 reserved 後 sidebar 簡化為只讀 `listContributions('purdex')`）
  - PR-3 rebase PR-2 後：清掉 `reservedStart` 分隔線邏輯（reserved 已不存在）
  - 移除 coming_soon disabled 灰字樣式（無 reserved item，分支恆未觸發）
  - 移除對 `listReservedItems()` 的呼叫；`settings-section-registry.ts` 的 `listReservedItems` export + `pendingReservedItems` Map 連帶拔除（dead code）
  - `getSettingsSections()` 過渡 API 保留給尚未遷的外部 callsite（若 grep 已無外部 callsite，可在本 PR 直接移除；實作時 audit 後決定）
- `spa/src/locales/en.json` / `zh-TW.json`
  - 移除 `settings.section.workspace` i18n key（若 reserved 特有，且無其他 callsite）
  - 保留 `settings.section.modules` key（`module-config` section 相關，但 `ModuleConfigSection` 本身仍在 workspace 頁用，key 可能仍有用；實作時 audit）

### 新增
- `spa/src/features/workspace/components/WorkspaceSettingsPage.test.tsx`
  - Render-level：註冊 fake contribution（`scope: 'workspace'`）→ `WorkspaceSettingsPage` render 後該 section 顯示 labelKey 與 body
  - ctx 注入：fake component 讀 `props.ctx.workspaceId === workspaceId`
  - `disabled(ctx)` 隱藏：註冊 disabled fake contribution → UI 不顯示（或 disabled 樣式）
  - order：多 contribution 順序正確
  - 無 contribution 時 registry 區塊不 render（或 render 空無影響 — 任一實作，測試明確）
  - reserved 清理回歸：hostsidebar / settingspage 不再顯示 `workspace` reserved item

### 不動
- `spa/src/components/settings/ModuleConfigSection.tsx`
- `spa/src/stores/useModuleConfigStore.ts`
- PR-1 新建的 `settings-contribution-registry.ts` / 三層 store
- `HostPage.tsx` / `SettingsPage.tsx`（其中 `SettingsPage` 的 Global 分支由 PR-2 改；本 PR 只動 `WorkspaceSettingsPage`）
- 既有 workspace-scoped 功能（icon picker / delete dialog / danger zone）

---

## 3. Test Case Matrix

### 3.1 `WorkspaceSettingsPage.test.tsx`（新增）

| 類別 | 測試項 | 預期 |
|---|---|---|
| Baseline | 無 contribution 時頁面 render（含 icon picker、name input、ModuleConfigSection、danger zone） | 通過，無 registry 區塊 |
| Render | 註冊 1 個 fake `scope: 'workspace'` contribution → 頁面 render 該 labelKey + body | 通過 |
| Ctx 注入 | fake component 讀 `props.ctx` → `scope === 'workspace'` && `workspaceId === 'wsA'` | 通過 |
| Multi + order | 註冊 3 個 contribution（order 10 / 20 / 5）→ 顯示順序 5 / 10 / 20 | 通過 |
| Disabled | contribution `disabled: (ctx) => true` → 該 section 不 render（或 UI 禁用） | 通過 |
| ctx.workspaceId 正確性 | 切換 workspace（重新 mount 帶不同 `workspaceId` prop）→ ctx 帶新 id | 通過 |
| Cascade（PR-1 已做） | 刪除該 workspace → `useWorkspaceSettingsStore.get(wsId, moduleId)` 回傳 undefined | 通過（驗 PR-1 cascade 仍生效） |

### 3.2 `register-modules.test.ts` 擴充

| 類別 | 測試項 | 預期 |
|---|---|---|
| Reserved 清理 | `getSettingsSections()` 回傳中**無** `id='workspace'` 項 | 通過 |
| Module-config 清理 | `getSettingsSections()` 回傳中**無** `id='module-config'` 項 | 通過 |
| 其餘 section 不變 | `appearance` / `terminal` / `interface` / `sync` / `editor-buffers` 仍存在，順序不變 | 通過 |

### 3.3 視覺回歸（手動）

- `cd spa && pnpm dev`
- 開一個 workspace → 打開 workspace settings（tab → 齒輪 / command）
- 驗：icon picker、name input、`ModuleConfigSection`（含 files.projectPath 輸入框）、danger zone 正常
- 驗：sidebar / settings 頁面**不再顯示** "Workspace" reserved 灰字項
- 驗：sidebar / settings 頁面**不再顯示** "Modules" / module-config 項
- 註冊一個臨時 workspace contribution（dev console 或臨時 module）→ 新區塊出現

### 3.4 i18n 驗證

- `settings.section.workspace` 若有其他 callsite 則保留（grep `settings.section.workspace`）
- `settings.section.modules` 同上 audit

---

## 4. 實作順序（TDD）

1. **紅**：寫 §3.1 `WorkspaceSettingsPage.test.tsx` 所有案例
2. **綠**：改 `WorkspaceSettingsPage.tsx` 插 registry 區塊
3. **紅**：寫 §3.2 register-modules 擴充測試
4. **綠**：拔 `register-modules.tsx` 兩個 `registerSettingsSection`
5. **紅**（optional）：SettingsSidebar reservedStart 分支測試（若 PR-2 已有此測試則順風消失）
6. **綠**：精簡 SettingsSidebar
7. **i18n audit**：grep 兩個 key 的 callsite，決定移或留
8. **驗證**：`cd spa && pnpm exec vitest run` / `pnpm run lint` / `pnpm run build` 全綠
9. **手動**：§3.3 視覺回歸

---

## 5. 驗收條件

- [ ] §3.1 / §3.2 測試全綠
- [ ] `cd spa && pnpm exec vitest run` 全綠
- [ ] `cd spa && pnpm run lint` 全綠
- [ ] `cd spa && pnpm run build` 全綠
- [ ] §3.3 手動視覺回歸：`ModuleConfigSection` 正常、reserved 項消失、workspace registry 區可擴充
- [ ] `gh issue close #538`（部分關閉 — 若 PR-2 已關則補充 workspace 層註記；若 PR-2 未關則本 PR 一併關）
- [ ] Codex 兩輪 review 無 critical / P1 未修項

---

## 6. 風險

| 風險 | 發生可能 | 緩解 |
|---|---|---|
| 刪除 `module-config` 後，未來若真有 module 要宣告 `globalConfig` 將無 UI 入口 | 中 | 決策 3b 已指示 `globalConfig` 在 PR-5 後 deprecate；新 `globalConfig` 用例應走新 `settings: [{ scope: 'purdex' }]`；若 PR-3 ship 後到 PR-5 前的空窗期出現新 `globalConfig` 需求，可臨時在 `SettingsPage` 以 `<ModuleConfigSection scope="global" />` render（僅 alpha 內部成本低） |
| `WorkspaceSettingsPage` 現有佈局（icon + name + ModuleConfig + danger zone）穿插 registry section 後視覺失衡 | 中 | registry 區塊用與 `ModuleConfigSection` 一致的 `<section>` 樣式；無 contribution 時不顯示任何空 header；§3.3 手動驗 |
| reserved item i18n key 被其他地方引用 | 低 | 移除前 grep `settings.section.workspace` + `settings.section.modules`；兩者若有外部 callsite 則保留 i18n，只拔 registry 註冊 |
| Disabled contribution 策略（隱藏 vs 禁用樣式）未對齊 | 低 | 本 plan §3.1 測試明確採「隱藏」；實作照此 |
| `WorkspaceSettingsPage` 使用者體驗：registry 區是否要 scroll 進 view、section anchor link、collapse | 低 | 本 PR 不做 UX 加分；只達基本渲染；進階 UX 進 backlog |

---

## 7. 超出 PR-3 範圍（明確不做）

- `SettingsPage` global shell 改造（PR-2）
- `HostPage` / `HostSidebar` / `host-routes` 改造（PR-4）
- `ModuleConfigSection.tsx` 本身遷到新 registry（等舊 `workspaceConfig` API 全移除之後，獨立 refactor PR）
- `files.projectPath` 搬家到新 `settings: [{ scope: 'workspace', localId: 'projectPath' }]`（決策 2b — 由 files module owner 後續 refactor）
- Editor `homePath` 用例（PR-5）
- #540 三層 store `get()` immutable snapshot（PR-5 前必解）
- URL subsection 支援於 workspace 層（目前 workspace 單頁無此需求）
- Sync contributor 於三層 store 的 wire shape（spec §10 sketch）

---

## 8. Commit 規劃

每 commit 綠。

1. **`feat(spa): WorkspaceSettingsPage renders workspace-scoped contributions`**
   - `WorkspaceSettingsPage.tsx` + test
2. **`refactor(spa): drop reserved workspace and module-config sections`**
   - `register-modules.tsx` 拔兩行 + test 擴充 + SettingsSidebar 精簡 + i18n audit 移除

共 2 commits。

---

## 9. 與其他 PR 的關聯

- **依賴**：PR-1（已 merged）+ **PR-2**（PR-3 build on PR-2 修好的 `SettingsSidebar.tsx` 與 dispatch-flushed adapter；reserved 清除會同步拔除 PR-2 留下的 `pendingReservedItems` buffer 與 `getSettingsSections()` 合併邏輯）
- **rebase 衝突點**（若與 PR-4 並行）：
  - `spa/src/lib/register-modules.tsx` — PR-3 拔 `workspace` / `module-config` 兩行；PR-4 加 `registerBuiltinHostSections()`。兩個位置不同，機械合併可行
- **被依賴**：PR-5（Editor `workspaceConfig.homePath` 首個用例 — 需要 PR-3 的 workspace shell 已能 render 新 contribution）
- **順風解決**：#538（workspace 層 render-level smoke）
