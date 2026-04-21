# HSR PR-1：Registry 核心 + Context + 三層 Stores — Implementation Plan

> 日期：2026-04-21
> 狀態：Ready for Implementation（v2，post plan-review）
> 主 spec：`2026-04-21-settings-contribution-registry-design.md`
> 關聯：kickoff `kickoff_host_module_settings.md`
> PR 系列：PR-1（本文件）/ PR-2 / PR-3 / PR-4 / PR-5（見主 spec §8）

---

## 1. 範圍

**純新增 + ModuleDefinition 擴充 + register pass**。不動任何既有頁面、不動任何 core store、不動任何 section 內部程式碼。

PR-1 結束時：
- 新 `settings-contribution-registry` 可用
- 三層 aggregate store 可用並接上 `syncManager`
- `ModuleDefinition.settings` 可宣告
- 註冊了 `settings` 的 module 會被 register pass 收進 registry
- **但任何 settings 頁面都還看不到新 contribution**（PR-2/3/4 才接 shell）— 由 smoke test 自動化證明

---

## 2. 檔案清單

### 新增

- `spa/src/lib/settings-contribution-types.ts` — `SettingsScope`、`SettingsContext`（discriminated union）、`SettingsContributionDeclaration`、`SettingsContribution`
- `spa/src/lib/settings-contribution-registry.ts` — `registerSettingsContribution` / `listContributions` / `getContribution` / `clearContributions`
- `spa/src/lib/settings-contribution-registry.test.ts` — registry 行為測試
- `spa/src/stores/useGlobalSettingsStore.ts` — global store（key `GLOBAL_SETTINGS`）+ `syncManager.register`
- `spa/src/stores/useGlobalSettingsStore.test.ts`
- `spa/src/stores/useHostSettingsStore.ts` — host store（key `HOST_SETTINGS`）+ `syncManager.register`
- `spa/src/stores/useHostSettingsStore.test.ts`
- `spa/src/stores/useWorkspaceSettingsStore.ts` — workspace store（key `WORKSPACE_SETTINGS`）+ `syncManager.register`
- `spa/src/stores/useWorkspaceSettingsStore.test.ts`
- `spa/src/lib/settings-contribution-smoke.test.tsx` — page-level smoke（註冊 fake contribution 後 `SettingsPage` / `HostPage` / `WorkspaceSettingsPage` 皆不顯示該 section，證明 PR-1 未誤接）

### 修改

- `spa/src/lib/module-registry.ts`
  - 新增 `settings?: SettingsContributionDeclaration[]` 欄位
  - re-export `SettingsContributionDeclaration` / `SettingsContext` / `SettingsScope`（方便 module 作者 import）
- `spa/src/lib/register-modules.tsx`
  - 於 `registerBuiltinModules()` 結尾新增 settings dispatch pass：
    1. 遍歷所有 registered module
    2. 檢查 Invariant I1（見主 spec §6.5）：同 module 同 scope 禁止新舊軌並存 → throw
    3. 為每個 declaration 產生完整 `SettingsContribution`（填 moduleId、組 id）並呼叫 `registerSettingsContribution(def)`
  - HMR：`import.meta.hot?.dispose(() => clearContributions())`（同時處理 module registry 的既有 dispose，若有）
- `spa/src/lib/register-modules.test.ts`
  - `clearAll()` helper 加入 `clearContributions()`
  - 新增測試（見 §3.5）
- `spa/src/lib/storage/keys.ts`
  - 新增 `GLOBAL_SETTINGS: 'purdex-global-settings'`
  - 新增 `HOST_SETTINGS: 'purdex-host-settings'`
  - 新增 `WORKSPACE_SETTINGS: 'purdex-workspace-settings'`

### 不動

- `spa/src/components/SettingsPage.tsx`、`spa/src/components/HostPage.tsx`、`spa/src/features/workspace/components/WorkspaceSettingsPage.tsx`（僅被 smoke test 以外部方式 render 驗證，不改內容）
- `spa/src/lib/settings-section-registry.ts` 及其所有消費者
- 所有 core store（`useThemeStore` / `useUISettingsStore` / `useLayoutStore` / `useI18nStore`）
- 所有既有 section 內部程式碼
- `useModuleConfigStore`（PR-2/3 遷移時再拔）
- `spa/src/lib/storage/sync.ts`（僅呼叫其公開 `register` API，不改實作）
- 任何 i18n key 檔案（PR-1 的 contribution 不渲染正式 UI，無需新 key；smoke test 用 inline fake label）

---

## 3. Test Case Matrix

### 3.0 Invariant 覆蓋邊界

本 PR-1 只涵蓋 **I1（雙軌禁令）** 與 **I2（localId 格式）**。
**I3（Context 來源必須是 route resolution）** 屬 PR-2/3/4 shell 的測試責任，本 PR-1 matrix **不測 I3**（spec §5.3 rule 2 雖已定義，但 PR-1 不動頁面，無 shell 可驗）。

### 3.0.1 Test pollution 清理

Registry 是模組級 singleton。以下檔案的 `beforeEach` 必須呼叫 `clearContributions()`：

- `settings-contribution-registry.test.ts`
- `settings-contribution-smoke.test.tsx`
- `register-modules.test.ts`（併入既有 `clearAll()`）

### 3.1 `settings-contribution-registry.test.ts`

| 類別 | 測試項 | 預期 |
|---|---|---|
| 基本 | register + listContributions(scope) 回傳該 scope 項目 | 通過 |
| 基本 | getContribution(id) 查得到 | 通過 |
| 基本 | listContributions 依 order 升冪排序 | 通過 |
| 基本 | listContributions scope filter 不漏 / 不多 | 通過 |
| HMR/Re-entry | 同 id、**同 object reference** 重複 register → silent skip | 不 throw |
| HMR/Re-entry | 同 id、**不同 object reference** → throws（error 訊息含 id） | throw |
| HMR/Re-entry | `clearContributions()` 後以不同 object 重新 register 同 id → 通過 | 通過 |
| Collision | 兩個 module 撞 localId（組出相同 id）→ throws | throw |
| Validation | 空 `moduleId` / 空 `localId` / 空 `id` → throws | throw |
| Validation | `id` 不等於 `${moduleId}.${localId}` → throws | throw |
| Validation (I2) | `localId` 含空白 → throws | throw |
| Validation (I2) | `localId` 含 `.` → throws | throw |
| Validation (I2) | `localId` 含中文或其他非 ASCII → throws | throw |
| Validation (I2) | `localId` 以數字開頭（違反 `[a-zA-Z][a-zA-Z0-9_-]*`）→ throws | throw |
| Validation (I2) | `localId` 含大寫（`FooBar`）→ 通過（字元集允許） | 通過 |
| Validation (I2) | `localId` 含底線 / dash（`foo_bar` / `foo-bar`）→ 通過 | 通過 |
| 隔離 | `clearContributions()` 清空後 list 為空 | 通過 |

### 3.2 `useGlobalSettingsStore.test.ts`

| 類別 | 測試項 | 預期 |
|---|---|---|
| 基本 | `set(moduleId, patch)` 後 `get(moduleId)` 回傳合併後值 | 通過 |
| 基本 | `set` 為 **shallow merge**（module payload 頂層欄位合併；nested object 由 module 自行整包覆寫） | partial key 覆寫，未提及的頂層 key 保留 |
| 基本 | `get` 未設定的 moduleId 回 `undefined` | 通過 |
| 清除 | `clear(moduleId)` 清單一 module，其他不受影響 | 通過 |
| 清除 | `clear()` 清全部 | 通過 |
| 隔離 | 兩個 module 寫入互不干擾 | 通過 |
| Persist | 寫入後 `localStorage.getItem(STORAGE_KEYS.GLOBAL_SETTINGS)` 解析 JSON 含該資料 | 通過 |
| Sync | store 建立時 `syncManager.register(STORAGE_KEYS.GLOBAL_SETTINGS, ...)` 被呼叫（用 vi.spyOn / vi.mock 驗證） | 通過 |

### 3.3 `useHostSettingsStore.test.ts`

| 類別 | 測試項 | 預期 |
|---|---|---|
| 基本 | `set(hostA, mod1, patch)` + `get(hostA, mod1)` | 回傳值 |
| 基本 | **shallow merge** semantic（如 §3.2） | 通過 |
| 隔離 | `hostA` 寫入不影響 `hostB` 的 `get` | 通過 |
| 隔離 | 同一 host 的 mod1 寫入不影響 mod2 | 通過 |
| 清除 | `clearHost(hostA)` 清該 host 所有 module | 通過 |
| 清除 | `clearModule(hostA, mod1)` 只清該 module | 通過 |
| Persist | 寫入後 `localStorage.getItem(STORAGE_KEYS.HOST_SETTINGS)` 解析 JSON 含該資料 | 通過 |
| Sync | store 建立時 `syncManager.register(STORAGE_KEYS.HOST_SETTINGS, ...)` 被呼叫 | 通過 |

### 3.4 `useWorkspaceSettingsStore.test.ts`

同 host store，將 `hostA`/`hostB` 替換為 `wsA`/`wsB`，`clearHost` → `clearWorkspace`，persist key 為 `STORAGE_KEYS.WORKSPACE_SETTINGS`。

### 3.5 `register-modules.test.ts`（擴充既有）

| 類別 | 測試項 | 預期 |
|---|---|---|
| Dispatch | module 宣告 `settings: [{localId:'general', scope:'purdex', ...}]` 後，`listContributions('purdex')` 含 id=`${moduleId}.general` | 通過 |
| Dispatch | 宣告多個 contribution（不同 scope）都被分派 | 通過 |
| Dispatch | `moduleId` 由系統填入、`id` 組出正確 | 通過 |
| Invariant I1 | 同一 module 同時宣告**非空** `globalConfig` + `settings scope='purdex'` → register pass throws | 通過 |
| Invariant I1 | 同一 module 同時宣告**非空** `workspaceConfig` + `settings scope='workspace'` → register pass throws | 通過 |
| Invariant I1 | 只宣告 `globalConfig` 無 `settings scope='purdex'` → 不 throw（既有 module 型態，不得誤擋） | 通過 |
| Invariant I1 | 空 `globalConfig: []` + `settings scope='purdex'` → 不 throw（I1 文字「非空」明示） | 通過 |
| Re-entry | 連呼 `registerBuiltinModules()` 兩次（未 clear）→ 第二次 throw（spec §6.4 定案：不同 object reference → throws） | throw |
| Re-entry | `clearAll() + clearContributions()` 後再呼叫 → 通過 | 通過 |

**註（spec §6.5）**：Host scope 無舊軌（`ModuleDefinition` 沒有 `hostConfig` 欄位），因此 `globalConfig + settings scope='host'` 組合不觸發 I1，屬**不可能的組合**，不列為測試案例。

### 3.6 `settings-contribution-smoke.test.tsx`（新增）

page-level smoke：證明「PR-1 不動頁面」斷言。

| 類別 | 測試項 | 預期 |
|---|---|---|
| Smoke | 註冊 fake contribution（每 scope 一個）後 render `SettingsPage` → 不出現 fake contribution 的 labelKey / localId 字樣 | 通過 |
| Smoke | 同上，render `HostPage`（帶 fake hostId ctx） | 不出現 |
| Smoke | 同上，render `WorkspaceSettingsPage`（帶 fake workspaceId ctx） | 不出現 |

測試實作備註：`render` 後用 `queryByText` 斷言不存在；不需 `act` 等待非同步（頁面 shell PR-1 不會消費 registry）。

---

## 4. 實作順序（TDD）

1. **紅**：寫 §3.1 registry 測試（registry 類），同批次寫 §3.6 smoke（先宣告測試，確認失敗狀態）
2. **綠**：建 `settings-contribution-types.ts` + `settings-contribution-registry.ts`，讓 §3.1 綠
3. **紅**：寫 §3.2 / §3.3 / §3.4 三層 store 測試
4. **綠**：實作三層 store（zustand + persist + `syncManager.register`）+ 擴 `storage/keys.ts`
5. **紅**：擴 `register-modules.test.ts`（§3.5）
6. **綠**：擴 `module-registry.ts` + `register-modules.tsx`（加 `settings` 欄位 + dispatch pass + I1 檢查 + HMR dispose hook）
7. **驗證**：§3.6 smoke 在 §6 完成後自動變綠（此時頁面仍不消費 registry，smoke 應通過）；跑完整 suite `cd spa && pnpm exec vitest run`、`cd spa && pnpm run lint`、`cd spa && pnpm run build`

---

## 5. 驗收條件（全部須達）

- [ ] 所有 §3 測試案例綠
- [ ] `cd spa && pnpm exec vitest run` 全綠
- [ ] `cd spa && pnpm run lint` 全綠
- [ ] `cd spa && pnpm run build` 全綠（`build` 包 `tsc -b`，即含 typecheck）
- [ ] 跑起 dev server（`cd spa && pnpm dev`）：Settings / Host / Workspace 三頁外觀與行為與 main 分支一致（肉眼驗）
- [ ] §3.6 smoke 綠（自動化證明「PR-1 未誤接」）
- [ ] Codex 兩輪 review：標準 `/codex:review --base main` + 三路對抗 `/codex:adversarial-review` 無 critical / P1 未修項
- [ ] 問題彙整表依信心 / 關聯 / 複雜度優先處理；未修項開 `gh issue` 追蹤

---

## 6. 風險（PR-1 專屬）

| 風險 | 發生可能 | 緩解 |
|---|---|---|
| zustand persist middleware 對 partial patch merge 行為不如預期 | 低 | §3.2-3.4 明定 shallow merge；明文測試不同 depth 的 patch 情境 |
| Invariant I1 guard 誤判（false positive）— 將任何既有 `globalConfig` 都當成衝突 | 中 | I1 guard 檢查必須**同時成立兩個條件**：(a) `globalConfig.length > 0` 且 (b) `settings` 內有 `scope==='purdex'` 之 declaration。§3.5 兩條測試（「只宣告 globalConfig 不 throw」與「空 globalConfig 不 throw」）涵蓋；pass 執行時會掃所有 builtin module 的 fixture 確認全部通過 |
| 三個新 persisted store 漏接 `syncManager.register` | 低 | §3.2-3.4 的 Sync 測試條明文驗證 `syncManager.register` 被呼叫 |
| HMR 環境下 `clearContributions` 沒正確介接 | 中 | Dev only；以 `if (import.meta.hot)` 守；若失效接受 dev 需重載頁面（不影響 production） |

---

## 7. 超出 PR-1 範圍（明確不做）

- Sync contributor（主 spec §10 已 sketch 原則，實作延後）
- Schema-based primitive renderer（未來 PR 疊上 `schema?: ConfigDef[]`）
- SettingsPage / HostPage / WorkspaceSettingsPage shell 改動（PR-2/3/4）
- 既有 `settings-section-registry` 合流（PR-2 決定）
- 四個既有 registry 收斂（主 spec §11 開放問題）
- 清理 reserved `workspace` section / 空 `module-config` section（PR-3/5）
- Editor `homePath` 實作（PR-5）
- I3（Context 來源必須 route resolution）的測試 — 歸 PR-2/3/4

---

## 8. Commit 規劃

每個 commit 必須可獨立過 `vitest run` / `lint` / `build`（即每個 commit「完全綠」，不留紅測試給下一 commit 修）。

1. **`docs: HSR spec v2 + PR-1 implementation plan`**（必須最先入，後續實作引用）
   - 新增 `docs/specs/2026-04-21-settings-contribution-registry-design.md`
   - 新增 `docs/specs/2026-04-21-hsr-pr1-registry-core-plan.md`
2. **`feat(spa): add settings contribution registry + types`**
   - `settings-contribution-types.ts` + `settings-contribution-registry.ts` + `settings-contribution-registry.test.ts`
3. **`feat(spa): add global/host/workspace settings stores`**
   - 三個 store 檔 + 三份 test 檔 + `storage/keys.ts` 補三個 key
4. **`feat(spa): ModuleDefinition.settings + register pass + I1 + HMR`**
   - `module-registry.ts` 欄位擴充 + `register-modules.tsx` dispatch pass + HMR dispose + `register-modules.test.ts` 擴充 + `settings-contribution-smoke.test.tsx`

共 4 個 commit。實作過程若發現需要調整 commit 切分（如 smoke test 搬到 commit 2），可在 PR 內 rebase 整理，保持最終每 commit 綠。
