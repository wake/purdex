# Settings Contribution Registry 設計 Spec（三層 Scope）

> 日期：2026-04-21（v3.2 對齊 2026-04-22，post-codex-review Round 2 task-mo8z6ppx-pj07yk）
> 狀態：Draft v3.2 — v3 對齊 5 個未決策點（§6.2 / §7.1 / §7.2 / §7.3 / §8 / §13）；v3.1 回應 Round 1 Finding 1+2（§7.2 dispatch 硬約束、§8 並行敘述修正）；v3.2 回應 Round 2 新 Finding N2（§13 PR-4 起點對齊 dispatch-flushed pattern）
> 關聯：kickoff `kickoff_host_module_settings.md`（擴大版，涵蓋三層 scope 而非僅 host）
> 參考：
> - Codex 探索結果：job `task-mo8crieu-phjsc8`（2026-04-21）
> - Codex spec 審查：job `task-mo8fef5j-v902sb`（2026-04-21）
> - 既有 registry pattern：`spa/src/lib/interface-subsection-registry.ts`、`spa/src/lib/file-opener-registry.ts`、`spa/src/lib/settings-section-registry.ts`
> - 既有 module 系統：`spa/src/lib/module-registry.ts`、`spa/src/lib/register-modules.tsx`

---

## 1. 概述

Purdex 已有 Module 系統（每個 module 是最小功能單位，可提供 Panes + Views），但 module **無法貢獻自己的 settings 項目**到設定頁面。目前所有 settings section 都在 `register-modules.tsx` 中央 side-effect 硬註冊，且只支援 Purdex（全域）一類，Host 頁完全硬編碼，Workspace 頁完全繞過 registry。

本 spec 建立三類 Settings 畫面的**統一註冊能力**：

1. **Purdex Settings**（全域 / 應用層）— 所有 host / workspace 共用
2. **Host Settings**（per-host scope）— 每個連線 daemon 獨立
3. **Workspace Settings**（per-workspace scope）— 每個 workspace 獨立

讓任一 builtin module 都能以宣告式方式把 settings section 插到三類畫面任一層，不需要再改中央 `register-modules.tsx` 的 side effect。

第一個功能層用例：Editor module 的「手動 home 路徑」host-scoped 設定（對應另一 session 進行中的 tilde path daemon 自動偵測，本 spec 不碰 daemon 端）。

**文件範圍**：本 spec 只定義**架構 invariants 與 API contract**。PR-1 的檔案清單、測試矩陣、驗收條件拆到 `2026-04-21-hsr-pr1-registry-core-plan.md`。

---

## 2. 問題定義

### 2.1 現況抽象分（Codex 探索結論，2026-04-21）

| 層 | 分數 | 問題 |
|---|---|---|
| Purdex Settings | 2/5 | `settings-section-registry` 太瘦（只有 id/label/order/component），無 moduleId/scope；sections 全在 `register-modules.tsx:249-349` 中央註冊；`SettingsPage` 自己 split URL、`parseRoute()` 又 parse 一次，兩套 parser 未來會漂移 |
| Host Settings | 1/5 | `HostPage` 硬 switch 六子頁（overview/sessions/hooks/agents/uploads/logs），無 registry |
| Workspace Settings | 3/5 | `Workspace.moduleConfig` 容器已泛化（files.projectPath 實用中），但 `WorkspaceSettingsPage` 是獨立頁不走 registry |
| ModuleDefinition | 2/5 | 只有 `panes/views/workspaceConfig/globalConfig/commands`，settings 不是一級欄位 |
| Extensions | 2/5 | 唯一案例是 CC statusline；`AgentExtensionRow.extensionId` 被寫死 `'statusline'` literal |

### 2.2 根本結構問題

1. **Settings 非 `ModuleDefinition` 一級契約** — 要幫 module 加 settings，必得改中央 `register-modules.tsx`，module 無法獨立
2. **三層 scope 各自硬編碼** — Purdex/Host/Workspace 三個頁面各自實作 shell，沒有共用 contract，後續想統一或加新 scope 成本倍增
3. **Core store 與 module settings 耦合** — `globalConfig` / `workspaceConfig` 原意是 module 自帶偏好，但 core store（Theme/UI/Layout/i18n）不屬此列，抽象不能把它們一起收編
4. **Host scope 的 render context 隱式** — Host-scoped component 若讀 `activeHostId` 會在 HostPage 切換路由時讀到錯 host（route-selected host ≠ activeHostId）

### 2.3 不納入本 spec 的既有 bug

- `FileTreeView` 讀 `workspace.moduleConfig.files.projectPath` 但 backend 取自 `activeHostId`，同一 workspace 切 host 會指向不同機器（`spa/src/components/FileTreeView.tsx:19-50`）— 既有 bug，另開 issue 追蹤，不在本 spec 修

---

## 3. 目標 / 非目標 / 適用邊界

### 3.1 目標

1. 讓任一 builtin module 以宣告式 `settings: SettingsContribution[]` 向三層 scope 貢獻 section
2. 三層 Settings 頁面未來可共用同一套 shell + renderer（PR-2/3/4 實作）
3. Host-scoped / Workspace-scoped component 取得 entity id 必須來自顯式 render context，不得讀 `activeHostId` / `activeWorkspaceId`
4. 遷移既有硬編碼 sections 時**不改其內部程式碼**（adapter 化）
5. Core 基礎設施 store（Theme/UI/Layout/i18n）**不收編**進新 registry（維持獨立）
6. 清理目前為空 / reserved 的僵屍 sections（`workspace` reserved、`module-config` 空頁）
7. **以型別層強制** scope context 的必填欄位（discriminated union），不依賴 runtime boundary

### 3.2 非目標

1. 不改 daemon 任何 API（純 SPA 層）
2. 不改 sync contributors 的 wire format（新 store 另立 contributor，未在 PR-1）
3. 不動 Theme/UI/Layout/i18n core store 的 persist schema
4. 不改既有 hardcoded section 內部程式碼（只換掛法）
5. Editor 的 tilde path daemon 自動偵測由另一 session 完成，本 spec 只負責手動路徑的 UI 掛點（PR-5）
6. 不在本 spec 收斂其他 registry（interface-subsection / new-tab / file-opener / hook-modules）成通用 contribution pattern — 見 §11 開放問題

### 3.3 適用邊界（Extension 語義）

- **適用**：同 repo 的 builtin module（`register-modules.tsx` 列入的 module 與未來同 repo 新增的 module）
- **不適用**：第三方 extension 系統。本 spec **不承諾** ABI 穩定性、沙箱、lifecycle、相容保證
- 第三方 extension 的設計另案；若未來導入，屆時以本 registry 為內部實作基礎，但對外介面可能另立

---

## 4. 名詞定義

| 名詞 | 定義 |
|---|---|
| **Contribution** | Module 向某個 scope 的 settings 畫面註冊一個 section 項目的單位 |
| **Scope** | `'purdex' | 'host' | 'workspace'` 三選一 |
| **Context** | Render 時注入給 contribution component 的執行期資訊（discriminated union，見 §6.1） |
| **Core store** | SPA 必備基礎設施的獨立 store（Theme/UI/Layout/i18n），**不走**本 registry |
| **Module-contributed settings** | 由 module 宣告，存在 `useGlobalSettingsStore` / `useHostSettingsStore` / `useWorkspaceSettingsStore` 三層 aggregate store |
| **Adapter section** | 既有硬編碼 section（AppearanceSection/TerminalSection 等）透過本 registry 掛載的 wrapper，不改 section 內部 |
| **localId** | Author 宣告的 section 短名，同 module 內唯一即可；registry 內部組成 `${moduleId}.${localId}` 作為全域 id |

---

## 5. 三層 Scope 模型

### 5.1 Scope 決策判準

**三條判準**（依序套用，找到第一條能判斷的即止）：

1. **資料擁有者**：這個設定值邏輯上屬於「應用程式」、「某個 host」、還是「某個 workspace」？
2. **持久化邊界**：刪除 host / workspace 時，此設定應該跟著清空嗎？
   - 刪 host 跟著清 → host scope
   - 刪 workspace 跟著清 → workspace scope
   - 不受刪除影響 → purdex
3. **執行者不等於擁有者的情況**：設定值雖然由 daemon 執行，但偏好本身屬於使用者（如 UI 呈現風格、快捷鍵）→ 看「值的變更是否應同步到所有 host」，是則 purdex，否則 host

### 5.2 範例（畫清常見混淆）

| 設定 | Scope | 理由 |
|---|---|---|
| App 主題（dark / light） | purdex（core） | 使用者層偏好，跨 host/workspace 一致；屬 core store，不走 registry |
| Terminal renderer | purdex（core） | 同上 |
| CC Hook install state（per host） | host | 以 daemon 為安裝目標；刪 host 時 hook 不再有意義 |
| Daemon ANSI palette | host | daemon 自持 config；切 host 要看到各自值 |
| Editor 手動 home 路徑 | host | per-host 檔案路徑，切 host 指向不同機器 |
| Files projectPath | workspace | 一個 workspace 對應一個專案目錄 |
| Editor 字型偏好 | purdex | 使用者偏好，跨 host/workspace 一致 |
| Sync 開關 / conflict handling | purdex | 應用層選項 |

### 5.3 Render Context（型別層強制）

Context 以 **discriminated union** 定義，scope 欄位是判別器：

```ts
export type SettingsContext =
  | { scope: 'purdex' }
  | { scope: 'host'; hostId: string }
  | { scope: 'workspace'; workspaceId: string }
```

**使用規則（hard rules）**：

1. Component prop 簽名 `{ ctx: SettingsContext }`；透過 `ctx.scope` narrow 後 TypeScript 會保證 `hostId` / `workspaceId` 非 undefined
2. **Host scope 的 `ctx.hostId` 必須來自 route resolution 結果**（`parseRoute()` 回傳的 hostId），不得來自 `useHostStore().activeHostId`
3. **Workspace scope 的 `ctx.workspaceId` 必須來自 route resolution 結果**（`useRouteSync()` 決定的 workspaceId），不得來自 `useWorkspaceStore().activeWorkspaceId`
4. 頁面 shell（PR-2/3/4 實作）負責建構 ctx；shell 外任何 component 不得自行構造 ctx

### 5.4 三層 Store 設計

**固定 aggregate key**（不用動態 key，避免破壞 `syncManager.register(key, store)` 假設）：

| Store | Key | Payload shape | API |
|---|---|---|---|
| `useGlobalSettingsStore` | `purdex-global-settings` | `{[moduleId]: Record<string, unknown>}` | `get(moduleId)` / `set(moduleId, patch)` / `clear(moduleId?)` |
| `useHostSettingsStore` | `purdex-host-settings` | `{[hostId]: {[moduleId]: Record<string, unknown>}}` | `get(hostId, moduleId)` / `set(hostId, moduleId, patch)` / `clearHost(hostId)` / `clearModule(hostId, moduleId)` |
| `useWorkspaceSettingsStore` | `purdex-workspace-settings` | `{[workspaceId]: {[moduleId]: Record<string, unknown>}}` | `get(workspaceId, moduleId)` / `set(workspaceId, moduleId, patch)` / `clearWorkspace(workspaceId)` / `clearModule(workspaceId, moduleId)` |

**關鍵**：三個 store API 各自 fit 該 scope 的語意，**不共用 `scopeId: string` 的裸簽名**。避免 hostId 被誤傳入 workspace store 等跨層錯配。

---

## 6. API 設計

### 6.1 `SettingsContribution` 型別

```ts
// spa/src/lib/settings-contribution-types.ts

export type SettingsScope = 'purdex' | 'host' | 'workspace'

export type SettingsContext =
  | { scope: 'purdex' }
  | { scope: 'host'; hostId: string }
  | { scope: 'workspace'; workspaceId: string }

// Module 作者宣告時使用（systems fields moduleId / id 由 register pass 填入）
export interface SettingsContributionDeclaration {
  localId: string                                          // 同 module 內唯一
  scope: SettingsScope
  order: number                                            // 同 scope 內升冪排序
  labelKey: string                                         // i18n key
  descriptionKey?: string                                  // i18n key（可選）
  component: React.ComponentType<{ ctx: SettingsContext }> // 自訂 section
  disabled?: (ctx: SettingsContext) => boolean             // 動態 gating
  disabledReasonKey?: string                               // disabled 時 i18n key
}

// Registry 持有、shell 消費的完整形態
export interface SettingsContribution extends SettingsContributionDeclaration {
  id: string       // 系統組 `${moduleId}.${localId}`（registry 全域唯一鍵）
  moduleId: string // 系統填
}
```

**規則**：
- `localId`：作者欄位，同 module 內唯一；character set 限 `[a-zA-Z][a-zA-Z0-9_-]*`
- `id`：系統欄位，`${moduleId}.${localId}` 組成；register pass 自動產生
- 作者**不得**手填 `id` 或 `moduleId`（型別上透過 `Omit<SettingsContribution, 'id' | 'moduleId'>` 的 declaration 型別阻擋）

### 6.2 Registry API

```ts
// spa/src/lib/settings-contribution-registry.ts

registerSettingsContribution(def: SettingsContribution): void
listContributions(scope: SettingsScope): SettingsContribution[]
getContribution(id: string): SettingsContribution | undefined
clearContributions(): void   // 測試用
```

**行為規則**：
- `registerSettingsContribution`：
  - **若 id 已存在且 def 是同 object reference（identity check）** → silent skip（idempotent，HMR / 重複 import safe）
  - **若 id 已存在但 def 是不同 object** → throw（防止兩個 module 撞 `localId` 或實作衝突）
  - 空 `moduleId`、空 `localId`、`id` 與 `${moduleId}.${localId}` 不符 → throw
- `listContributions(scope)`：依 `order` 升冪排序；只回傳指定 scope；不做 `disabled(ctx)` filter（由 shell 處理）
- `clearContributions()`：測試隔離用；production flow 不呼叫

**與既有 `settings-section-registry` 的關係**：PR-1 保留舊 registry 原樣，新 registry 並存。**PR-2 合流策略鎖定 adapter-only**（決策 1c）：舊 `registerSettingsSection()` 改為對新 registry 的 adapter，所有既有 callsite 不用動；`SettingsPage` / `WorkspaceSettingsPage` / `HostPage` 只信新 registry。

### 6.3 `ModuleDefinition` 擴充

```ts
// spa/src/lib/module-registry.ts （擴充既有型別）

interface ModuleDefinition {
  id: string
  name: string
  panes?: PaneDefinition[]
  views?: ViewDefinition[]
  globalConfig?: ConfigDef[]         // 既有（保留；見 §7 invariant）
  workspaceConfig?: ConfigDef[]      // 既有（保留；見 §7 invariant）
  commands?: CommandDefinition[]     // 既有

  // 新增
  settings?: SettingsContributionDeclaration[]
}
```

### 6.4 Register pass 與 HMR 語義

**Production 啟動流程**：
1. `registerBuiltinModules()` 呼叫 — 已有流程
2. 額外一 pass：掃描所有已註冊 module 的 `settings: []`，為每個 declaration 產生 `{ id: '${moduleId}.${localId}', moduleId, ...decl }` 並 `registerSettingsContribution(def)`
3. `registerSettingsContribution` 的 identity-check：同一 pass 重跑、同一 def object reference 會 silent skip；HMR 若持有新 object（例如 module 檔重新求值）則是**新定義 → throws**

**HMR 策略**（PR-1 實作時採）：
- `register-modules.tsx` 在 `import.meta.hot?.dispose()` 或等效點呼叫 `clearContributions()` 再重跑 pass
- 若 HMR infra 無法 hook dispose，**接受 dev 環境需重載頁面**（生產不受影響；HMR 本就是 dev-only 便利性）

### 6.5 Invariants（register pass enforcement）

**I1. 單 module + scope 不得雙軌暴露同功能**

同一 `moduleId` 在同一 scope 內，不得同時透過「舊 `globalConfig` / `workspaceConfig`」與「新 `settings`」暴露同一設定 key。

- **強制方式**：register pass 偵測到同一 moduleId 同時存在 `globalConfig`（非空）與任一 `settings` 宣告 `scope: 'purdex'` → throw
- 同理 `workspaceConfig` 非空 + `scope: 'workspace'` → throw
- Host scope 無舊軌，不適用
- 例外：`settings` 為空陣列時不觸發（純宣告未用）

**I2. localId 格式約束**：見 §6.1

**I3. Context 來源**：見 §5.3 rules 1-4

---

## 7. 既有抽象的處理

### 7.1 Core store 不收編（依 feedback）

**不動**：`useThemeStore` / `useUISettingsStore` / `useLayoutStore` / `useI18nStore`。

理由：這些是 SPA 必備 core（bootstrap 必讀、不可延遲載入、不可由 module 卸載），不符合「可選式 module-contributed settings」語意。詳見 `feedback_core_vs_module_settings.md`。

**既有 section 搬家節奏（決策 2b）**：AppearanceSection / TerminalSection / SyncSection / LinkDetectionSection 等既有 built-in section，在 HSR PR-2/3/4 階段**只換掛法**（透過 §7.2 adapter 進新 registry），**不改內部程式碼、也不強制轉為 module-owned declaration**。真正把 section 搬進各自 module 的 `settings: []` 宣告，由各 owner（Sync / Editor / Appearance 等）後續獨立 refactor PR 接手，不是 HSR 系列的 scope。

### 7.2 既有 `settings-section-registry`（決策 1c — adapter-only）

**鎖定策略**：adapter-only，且 **legacy adapter 的寫入必須經由 `dispatchSettingsContributions()` 統一 flush**（見下方硬約束）。

- 舊 `registerSettingsSection(section)` 實作改成：組出 `SettingsContributionDeclaration`（`localId` = 原 `id`、`scope: 'purdex'`、`labelKey` = 原 `label`、`component` = 原 component 包一層吃 `{ ctx }` 的 wrapper），**push 到 module-scope pending buffer**（不立刻呼叫 `registerSettingsContribution`）
- `dispatchSettingsContributions(modules)` 於 Phase 2 `clearContributions()` 之後，同時 register：
  1. module-declared contributions（來自 `ModuleDefinition.settings`）
  2. legacy adapter pending buffer（由 `drainLegacyContributionQueue()` 取出並清空 buffer）
- 舊 `getSettingsSections()` 與舊 registry 的其他 read API 保留，實作改成對新 registry 的 `moduleId === '_builtin.legacy-section'` filtered view（供尚未遷的 callsite 過渡）
- `SettingsPage` / `WorkspaceSettingsPage` / `HostPage` 自 PR-2/3/4 起**只讀新 registry**，不再經過舊 API
- 舊 registry 的完全移除延到 HSR 系列全部 land 後，當舊 callsite 清空再獨立 refactor PR 拔除

**硬約束（dispatch 時序，防 Finding 1 的 regression）**：
- `registerSettingsSection()` **不得**直接呼叫 `registerSettingsContribution()`，否則 `registerBuiltinModules()` 結尾的 `dispatchSettingsContributions()` → `clearContributions()` 會把 legacy 項整批清掉
- 新 registry 的**唯一**寫入入口是 `dispatchSettingsContributions()`；#539 將 `registerSettingsContribution` 降級 `@internal` 後，adapter 也需改走 pending buffer
- HMR 重跑 `registerBuiltinModules()` → `registerSettingsSection()` 再次 push 到 pending buffer → dispatch 再次 drain；buffer 為本輪用完即清

**理由**：alpha 階段雖無 backwards-compat 包袱，但一次全遷會迫使 PR-2 同時處理 shell 切換 + section 搬家，scope 難收斂；adapter 讓既有 7 個 builtin section 零改動進新 registry，PR-2 scope 聚焦於 shell 與 adapter 正確性。

### 7.3 待清理項

- `workspace` reserved section（`register-modules.tsx:258-259`）—— **PR-3 清掉**（決策 5a）
- `module-config` 空頁 section（`register-modules.tsx:260-265`）—— **PR-3 清掉**（決策 5a；PR-3 shell 本就動 WorkspaceSettingsPage，順手最省事）
- 舊 `globalConfig` / `workspaceConfig` 欄位（決策 3b）：invariant I1 守住雙軌禁令；**PR-5 merge 後**發 deprecation warning（console.warn + JSDoc `@deprecated`），指向新 `settings: [{ scope: 'workspace', ... }]`；**全面移除**延到至少 1 個 module 將舊軌遷移完畢之後，由獨立 cleanup PR 處理

**理由（決策 3b）**：deprecate 要有替代範例可指，PR-5（Editor `homePath`）才是第一個真正用新 `hostConfig` / `workspaceConfig` 替代路徑的用例；PR-5 merge 後再發 deprecation 警告，避免 API 消費者沒有可行遷移路徑就被警告轟炸。

---

## 8. 實作 Phase（Roadmap）

| PR | 範圍 | Plan 文件 | 狀態 |
|---|---|---|---|
| **PR-1** | Registry 核心 + types + 三層 stores + `ModuleDefinition.settings` + register pass（**不動任何頁面**） | `2026-04-21-hsr-pr1-registry-core-plan.md` | ✅ merged (#542, alpha.199) |
| PR-2 | Purdex Settings 頁 shell → registry-driven + 舊 `settings-section-registry` adapter（決策 1c） | `2026-04-22-hsr-pr2-purdex-shell-plan.md` | 📝 plan |
| PR-3 | Workspace Settings 頁 shell → registry-driven + 清理 reserved `workspace` section + 清理 `module-config` 空頁（決策 5a） | `2026-04-22-hsr-pr3-workspace-shell-plan.md` | 📝 plan |
| PR-4 | Host Settings 頁 shell → registry-driven + 六子頁走 built-in adapter registration（決策 4c，非 module 宣告，但走同 contract） | `2026-04-22-hsr-pr4-host-shell-plan.md` | 📝 plan |
| PR-5 | Editor `settings: [{ scope: 'host', localId: 'homePath' }, { scope: 'workspace', localId: 'homePath' }]` 首個 module 用例 + PR-5 merge 後對舊 `globalConfig` / `workspaceConfig` 發 deprecation warning（決策 3b） | `2026-04-22-hsr-pr5-editor-homepath-plan.md` | 📝 plan |

**相依與順序（修正：PR-2/3/4 並非無衝突並行）**：

PR-2/3/4 共享兩個檔案，無法零衝突並行：
- `spa/src/components/settings/SettingsSidebar.tsx`：PR-2 改 source（讀新 registry）+ PR-3 清 `reservedStart` 分隔線分支
- `spa/src/lib/register-modules.tsx`：PR-3 拔 reserved 項 + PR-4 加 `registerBuiltinHostSections()`

**建議合併順序**：**PR-2 → PR-3 → PR-4 → PR-5**（序列化）。若真要並行，PR-3 / PR-4 必須 rebase PR-2，PR-4 必須 rebase PR-3，並在 rebase 時手動 resolve：
- `SettingsSidebar.tsx`：PR-3 的 reservedStart 清除 build on PR-2 的 registry-read 版本
- `register-modules.tsx`：PR-3 的 reserved 拔除與 PR-4 的 built-in host adapter 註冊位於不同函式區塊，衝突可機械合併

PR-5 依賴：
- **PR-3**（`ctx.workspaceId` 由 PR-3 的 WorkspaceSettingsPage shell 注入）
- **PR-4**（`ctx.hostId` 由 PR-4 的 HostPage shell 注入；PR-4 的 host route contract 重構讓 Editor host contribution 能走動態 subPage）

既有 section（Appearance / Terminal / Sync / LinkDetection 等）的真正搬家到 module-owned declaration **不在** HSR 系列 scope，由各 owner 後續獨立 refactor PR 接手（決策 2b）。

**延後 issue 對應**：
- PR-2/3/4 任一 land 後 → 補 **#538**（render-level smoke test）
- 第一個 consumer PR 一併處理 → **#539**（`registerSettingsContribution` 收斂為 internal API；PR-2 切斷外部 callsite 最合適）
- PR-5 前必解 → **#540**（三層 store `get()` 回傳 internal ref / 改回 immutable snapshot）
- PR-4 sync subsystem 驗證 → **#541**（cross-store rehydrate order）

---

## 9. 風險與 Gotchas

| 風險 | 處理 |
|---|---|
| `syncManager.register(key, store)` 假設 key 固定 | 三層 store 走固定 aggregate key（§5.4），不走 `purdex-host-<id>` 動態 key |
| Host/Workspace scope component 讀錯 entity id | §5.3 rules 2-3 + discriminated union + PR-4 shell 灌入規則；PR-1 在型別上就擋下 |
| 新 aggregate store 需要 sync contributor | 本 PR **不加**；未來加 contributor 時依 §10 sketch 處理 conflict |
| Workspace / Host 刪除時殘留 payload | PR-3 / PR-4 shell 遷移時接 `removeWorkspace()` / `removeHost()` lifecycle；PR-1 先留著（alpha 階段 negligible） |
| 既有 `globalConfig` / `workspaceConfig` 與新 `settings` 語意重疊 | Invariant I1（§6.5）register pass throws 擋雙軌；PR-5 或後續全面拔舊軌 |
| Adapter section 與原 section 的 i18n key 衝突 | PR-2 adapter 沿用原 labelKey；新 contribution 用新命名空間；PR-1 無此風險（不渲染） |
| dup-id throws 在 HMR 反覆爆 | §6.2 identity-check（same-object idempotent）+ §6.4 HMR dispose reset |

---

## 10. Sync Contributor Sketch（未實作，預留設計空間）

PR-1 不加 sync contributor；但為避免 store shape 被後續 sync 設計反咬，此處 sketch 原則：

1. **粒度**：per-(scope, module)。最小同步單位是「某個 moduleId 在某個 scopeId 下的整包 `Record<string, unknown>`」
2. **Conflict 策略**：last-write-wins（LWW），依 SPA 端 device clock timestamp；未來若發現 LWW 不夠可升為 field-level（另案）
3. **刪除表達**：explicit tombstone。`clearModule(x, y)` 觸發時寫入 `{ [moduleId]: null }` 而非 delete key，同步時 null = tombstone；compaction 時清除
4. **Host / Workspace 刪除連動**：host/workspace 的刪除是獨立 sync event（沿用現有 `hosts` / `workspaces` contributor），本 registry store 的 contributor 看到 parent entity 消失時被動 cleanup

此設計不在 PR-1 實作；PR-1 確保 store shape 能 forward-compatible 支援以上原則。

---

## 11. 開放問題（未在本 spec 解決）

### OP-1：四個既有 registry 是否收斂成通用 contribution pattern

現況 registry：`settings-section-registry` / `interface-subsection-registry` / `new-tab-registry` / `file-opener-registry` + 半 registry 常數 `HOOK_MODULES`。

本 spec 只建 settings 專用 registry。長期是否要收斂成單一 `contribution-registry` 並讓以上全走同一 API，**本 spec 不決定**。

**處理方式**：開 GitHub issue 追蹤（待 PR-1 merge 後建立），等至少 2 個 scope 的 registry 落地後再評估價值。

---

## 12. 驗收原則（PR 層級）

PR-1 具體驗收條件見 `2026-04-21-hsr-pr1-registry-core-plan.md`。

通用原則（適用每個 HSR 系列 PR）：
- `pnpm test` / `pnpm run lint` / `pnpm run build` 全綠
- Codex 兩輪 review（標準 + 三路對抗）無 critical / P1 未修項
- 既有功能外觀與行為 100% 無變化（除非該 PR 明確包含 UI 改動）
- 新增 invariant 必須有至少一個測試案例覆蓋

---

## 13. 後續 PR 銜接備忘（決策對齊版）

- **PR-2 起點（決策 1c）**：`SettingsPage` 改**只讀新 registry**（無 feature flag）；`settings-section-registry` 的 `registerSettingsSection` 改為 push 到 pending buffer + export `drainLegacyContributionQueue()`；`dispatchSettingsContributions()` 修改為同時 flush module-declared + legacy pending（見 §7.2 硬約束）；既有 7 個 built-in section 無需改碼即自動進新 registry；#539 在此 PR 把 `registerSettingsContribution` 降級為 `@internal`（adapter 改走 pending buffer，dispatch 成為新 registry 唯一寫入入口）
- **PR-3 起點（決策 5a）**：`WorkspaceSettingsPage` 拆 shell + reserved `workspace` section 清除 + `module-config` 空頁清除 + `removeWorkspace()` cleanup hook（與 PR-1 的 `useWorkspaceSettingsStore.clearWorkspace` 對接）
- **PR-4 起點（決策 4c）**：`HostPage` switch → shell；六子頁（overview/sessions/hooks/agents/uploads/logs）**不轉為 module 宣告**，改為 **`registerBuiltinModules()` 階段 push 到 pending queue** 的「built-in adapter registration」— 由 `dispatchSettingsContributions()` 統一 flush（沿用 §7.2 的 dispatch-flushed pattern，**不得**在 `HostPage` 載入時直接呼叫 `registerSettingsContribution()`，否則 dispatch 的 `clearContributions()` 會把 built-in 項整批清掉；與 PR-2 legacy adapter 同一機制），走同一條 contract 但來源標記為 built-in；`ctx.hostId` 來源由 route resolution 提供（§5.3 rule 2）；`removeHost()` cleanup hook 對接 `useHostSettingsStore.clearHost`；#541 在此 PR 驗證 cross-store rehydrate order
- **PR-5 起點（決策 3b）**：Editor module 宣告 `settings: [{ localId: 'homePath', scope: 'host', ... }, { localId: 'homePath', scope: 'workspace', ... }]`；opener 層做層疊 resolve（workspace → host → `fetchPaneHome` fallback）；PR-5 merge 時對舊 `globalConfig` / `workspaceConfig` 加 console.warn + JSDoc `@deprecated`，指向新 `settings` 路徑；全面移除延後獨立 PR
