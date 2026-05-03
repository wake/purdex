# Spec — Files module disableable + SR-2 修復

> Date: 2026-05-03  
> Status: Draft (round-2 codex review approved, ready for plan)  
> Repo: purdex / branch: `worktree-files-disableable-sr2`  
> Related: SR-2 from codex review of PR #617 (modules switchboard)

## 1. Context

`alpha.288` (PR #816 + #825) 落地 Modules Switchboard，把 disableable 模組的 settings 過濾收斂到單一 hook 點 `dispatchSettingsContributions`。當時 codex review 列了 SR-2：Files module 是「disableable lie」候選 — 它唯一的 settings surface 在 deprecated `ModuleDefinition.workspaceConfig`，渲染走 `WorkspaceSettingsPage` → `ModuleConfigSection` → `getModulesWithWorkspaceConfig()`，**完全繞過** `useModuleEnabledStore` 的 disable filter。所以即使把 Files 標 `disableable: true`，使用者 toggle off 後 Workspace settings 內仍可編輯 `projectPath`，是個謊話。SR-2 採取保守策略：暫時不標 disableable，並加一段 inline 註解警示。

`register-modules/index.tsx:243-247`

```ts
// SR-2 (codex review #617): intentionally NOT flagged disableable yet.
// The module's only settings surface lives in `workspaceConfig`, which
// `WorkspaceSettingsPage` renders through `ModuleConfigSection` — a path
// that does not consult `useModuleEnabledStore`. Toggling would be a lie
// until PR 3 wires workspace-scope legacy contributions into the filter.
```

`dispatch-settings-contributions.ts:32` 的 `DEPRECATED_LEGACY_CONFIG_EXEMPT` 也豁免了 Files 的 `workspaceConfig` deprecation warning。

本 spec 結束 SR-2 過渡：把 Files 從 deprecated `workspaceConfig` 遷到 `settings: [{ scope: 'workspace' }]`，讓 disable filter 真的接得到，順便拔 SR-2 註解 + deprecation exempt。

## 2. Goals / Non-Goals

### Goals
- G1: Files module 標 `disableable: true`，自動出現在 Modules Switchboard。
- G2: Files 的 workspace `projectPath` 設定走 `settings: [{ scope: 'workspace' }]` contribution path，受 `dispatchSettingsContributions` 的 disable filter 控制。
- G3: Disable Files **後 reload**（與其他 disableable module 同 UX：ReloadBanner 提示 → 使用者 reload）→ `WorkspaceSettingsPage` 內不再渲染 `projectPath` input。Live toggle **不**即時隱藏；保持與 alpha.288 既有架構（`useModuleEnabledStore.setEnabled` 只寫 store，不重新 dispatch contributions）一致。
- G4: Storage shape 不動 — `projectPath` 仍存在 `useWorkspaceStore.workspaces[wsId].moduleConfig['files'].projectPath`，所有現有 reader (`FileTreeView`, `file-open-bootstrap`) 不需改。
- G5: 移除 SR-2 inline 註解 + `DEPRECATED_LEGACY_CONFIG_EXEMPT` 的 `'files'` entry + 對應的 deprecation block 註解（`dispatch-settings-contributions.ts:29-32`）。

### Non-Goals
- N1: 不擴大 disable 範圍至 `views`（spec I4 不變量：disable 只影響 `dispatchSettingsContributions`，file-tree workspace/session view 仍可被加進 sidebar）。
- N2: 不刪 `ModuleConfigSection.tsx` / `ModuleDefinition.workspaceConfig` 欄位 / `getModulesWithWorkspaceConfig` helper。Files 遷出後 `getModulesWith*Config()` 自動回 `[]`，`ModuleConfigSection` 自動 `return null`。**Mount call 也保留**（codex PR adversarial round-1 high finding）— `WorkspaceSettingsPage.tsx` 仍 mount `<ModuleConfigSection scope={{ workspaceId }} />`，作為 in-flight migration / out-of-tree module 仍用 `workspaceConfig` 時的 escape hatch render path。完整拆（mount + component + helpers + 欄位）一次清在 follow-up F-1，避免本 PR 把 escape hatch 拆一半。
- N3: 不加 Files purdex-scope placeholder / settings page。對齊 Browser 的處理（disableable 但 purdex sidebar 無條目）；Files 設定本就是 workspace 範疇，purdex 沒東西可放，placeholder 是 dead UX。
- N4: 不調整 PR-2 落地的 purdex sidebar 順序（`MODULE_EDITOR` / `MODULE_QUICK_COMMANDS` / `MODULE_PERFORMANCE_MONITOR` / `MODULE_SYNC` 不動）。本 PR 只新增 workspace-scope 常數 `WORKSPACE_FILES`（§4.1）— Files contribution 是 workspace scope，跟 purdex sidebar 順序無關。

## 3. Invariants

- I1: `projectPath` 的 storage path 不變 — `useWorkspaceStore.workspaces[wsId].moduleConfig['files'].projectPath`。
- I2: Files disabled state（store 內 `enabled.files === false`）+ 重跑 `dispatchSettingsContributions` → `listContributions('workspace')` 結果不含 `files.workspace-files`（對齊 EC-1b — disable filter 在 `dispatchSettingsContributions` 內套）。實務上重跑只發生在 `registerBuiltinModules()` boot-time 執行；live toggle 後須 reload 才生效（對齊 ReloadBanner UX）。
- I3: Files enable + dispatch 後 `WorkspaceSettingsPage` 渲染 Files contribution 的 `<h3>` header + body（puzzle icon 因 `isModuleOwnedContribution(c) === true`）。
- I4: 本 PR 對 disable filter 副作用範圍的承諾（取代舊 spec 的廣義 I4，因 `module-registry.ts:170-188 resolvePaneRenderer` 與 `module-file-openers.ts:29-34 applyModuleFileOpeners` 已實際對 disable 反應）：
  - **Files 沒有 panes** → 不受 `resolvePaneRenderer` disable→placeholder 行為影響。
  - **Files 沒有 `fileOpeners`** → 不受 `applyModuleFileOpeners` 跳過行為影響。
  - **Files 有 views**（`file-tree-workspace` / `file-tree-session`）→ `getAllViews()` / `getViewDefinition()` 不套 disable filter，所以即使 disable Files 後再 reload，views 仍可被加進 sidebar。這是 spec `2026-04-23-modules-switchboard-spec.md` §3 I4 留下的 known limitation（v1 認可），本 PR 不擴大範圍。
  - **Files 有 settings**（本 PR 新增的 workspace contribution）→ 受 `dispatchSettingsContributions` disable filter 控制。
- I5: `DEPRECATED_LEGACY_CONFIG_EXEMPT` 移除 `'files'` 後，沒任何 module 用 `globalConfig` / `workspaceConfig`，dispatch 不會噴 deprecation warning（驗證手段：startup log 無 `[module] ...deprecated...` 訊息）。

## 4. Design

### 4.1 Module 定義改動

`register-modules/index.tsx` 內 Files registerModule：

```diff
 registerModule({
   id: 'files',
   name: 'Files',
-  // SR-2 (codex review #617): intentionally NOT flagged disableable yet.
-  // The module's only settings surface lives in `workspaceConfig`, which
-  // `WorkspaceSettingsPage` renders through `ModuleConfigSection` — a path
-  // that does not consult `useModuleEnabledStore`. Toggling would be a lie
-  // until PR 3 wires workspace-scope legacy contributions into the filter.
-  workspaceConfig: [
-    { key: 'projectPath', type: 'string', label: '專案路徑' },
-  ],
+  disableable: true,
+  descriptionKey: 'modules.files.description',
+  settings: [
+    {
+      localId: 'workspace-files',
+      scope: 'workspace',
+      order: SETTINGS_ORDER.WORKSPACE_FILES,
+      labelKey: 'settings.section.files_workspace',
+      component: FilesWorkspaceSettingsSection,
+    },
+  ],
   views: [
     { id: 'file-tree-workspace', ..., scope: 'workspace', ... },
     { id: 'file-tree-session',   ..., scope: 'tab', ... },
   ],
 })
```

順序考量：Workspace scope sidebar 既有唯一 entry 是 Editor 的 `workspace-home-path` (`order: 0`)。Files 用 `WORKSPACE_FILES = 10` 排在 Editor 之後（按字母 Editor → Files 順序，與目前 purdex sidebar 順序一致）。

`settings-order.ts` 的 lint 守則（spec PR-2 §4.1.2「禁止 hard-code 數字」）原本只 enforce 到 purdex scope（`editor-module.tsx` 的 workspace-home-path / host-home-path 是 inline 數字）。本 PR 把 Files workspace order 收編進 `SETTINGS_ORDER`，並更新該檔註解明確：所有 scope（purdex / workspace / host）新增 `settings: [...]` 都該從 `SETTINGS_ORDER` import；Editor 的 inline 數字屬 known legacy，未來可順手收編但不在本 PR scope。

新增常數：

```diff
 export const SETTINGS_ORDER = {
   ...
   MODULE_SYNC: 14,
   ...
+  // Workspace-scope module contributions.
+  WORKSPACE_FILES: 10,
 } as const
```

### 4.2 `FilesWorkspaceSettingsSection` 元件

新檔 `spa/src/components/settings/FilesWorkspaceSettingsSection.tsx`，介面參考 `EditorHomePathWorkspaceSection`：

```tsx
import { useId } from 'react'
import { useWorkspaceStore } from '../../features/workspace/store'
import { useI18nStore } from '../../stores/useI18nStore'
import type { SettingsContextFor } from '../../lib/settings-contribution-types'

interface Props { ctx: SettingsContextFor<'workspace'> }

export function FilesWorkspaceSettingsSection({ ctx }: Props) {
  if (ctx.scope !== 'workspace') return null
  return <Body workspaceId={ctx.workspaceId} />
}

function Body({ workspaceId }: { workspaceId: string }) {
  const t = useI18nStore((s) => s.t)
  const projectPath = useWorkspaceStore(
    (s) => s.workspaces.find((w) => w.id === workspaceId)?.moduleConfig?.['files']?.['projectPath'],
  )
  const value = typeof projectPath === 'string' ? projectPath : ''
  const inputId = useId()

  const handleChange = (next: string) => {
    useWorkspaceStore.getState().setModuleConfig(workspaceId, 'files', 'projectPath', next)
  }

  return (
    <div className="flex items-center justify-between py-1">
      <label htmlFor={inputId} className="text-xs text-text-secondary">
        {t('settings.files.project_path.label')}
      </label>
      <input
        id={inputId}
        className="w-48 px-2 py-0.5 rounded border border-border-default bg-surface-primary text-xs text-text-primary"
        type="text"
        value={value}
        onChange={(e) => handleChange(e.target.value)}
      />
    </div>
  )
}
```

設計重點：
- **Storage path 不動**：直接讀寫 `useWorkspaceStore` 的 `moduleConfig['files']['projectPath']`，跟 `FileTreeView.tsx:24,79` 用同一個 path（I1）。
- **無 trim / commit-on-blur**：對齊既有 `ConfigField` 的 onChange-immediate 寫法。`EditorHomePathWorkspaceSection` 的 trim/blur 流程是針對檔案路徑空白敏感性，本 PR 不引入新行為（最小 diff 原則）。如果未來要對齊 Editor 寫法，可開 follow-up。
- **i18n key**：`settings.files.project_path.label` 取代 hardcoded `'專案路徑'`。`labelKey: 'settings.section.files_workspace'` 是 sidebar header，內部 input label 另開 key。

### 4.3 `dispatch-settings-contributions.ts` 清理

```diff
-// PR-5 deprecation: module authors using `globalConfig` / `workspaceConfig`
-// should migrate to `settings: [{ scope, localId }]`. `files` is exempt until
-// the files owner completes its refactor.
-const DEPRECATED_LEGACY_CONFIG_EXEMPT: ReadonlySet<string> = new Set(['files'])
+// PR-5 deprecation: module authors using `globalConfig` / `workspaceConfig`
+// should migrate to `settings: [{ scope, localId }]`. Empty exempt set kept
+// as a future-friendly escape hatch — add a moduleId here to silence the
+// deprecation warning while a migration is in flight.
+const DEPRECATED_LEGACY_CONFIG_EXEMPT: ReadonlySet<string> = new Set()
```

保留 set 結構以便將來新增豁免（同 PR 內不刪變數，避免 churn）；註解同步更新去掉 Files 特例字眼。

### 4.4 i18n

新增 keys（en + zh-TW）：

| key | en | zh-TW |
|---|---|---|
| `modules.files.description` | "Workspace and session file tree, with click-to-open into the Editor" | "工作區與 Session 檔案樹，可點擊開啟至 Editor" |
| `settings.section.files_workspace` | "Files" | "檔案" |
| `settings.files.project_path.label` | "Project path" | "專案路徑" |

注意：用 `settings.section.files_workspace` 而非 `settings.section.files`，是為了預留將來 purdex-scope `settings.section.files` 的可能（不衝撞）。Sidebar header label 顯示就是 "Files" / "檔案"。

### 4.5 既有 reader 不動

`FileTreeView.tsx:24` `workspace?.moduleConfig?.['files']?.['projectPath']` 不變。  
`FileTreeView.tsx:79` `setModuleConfig(workspaceId, 'files', 'projectPath', trimmed)` 不變（empty-state inline 設定流程仍可用）。  
`file-open-bootstrap.ts:184` `cfg?.['files']?.['projectPath']` 不變。

→ G4 達成。

## 5. Edge Cases

| EC | 情境 | 預期行為 |
|---|---|---|
| EC-1 | 使用者 disable Files → 切到 Workspace settings（**不 reload**） | Live toggle 後 contribution registry 仍是舊的（`setEnabled` 只寫 store），所以 Files header + input **仍可見**。同時 `ModulesSwitchboardSection` 顯示 ReloadBanner（因 `hasPendingChanges() === true`），提示 reload 後生效。對齊 alpha.288 既有 UX。 |
| EC-1b | Disable Files → reload | `registerBuiltinModules()` 重跑 → `dispatchSettingsContributions()` 過濾掉 disabled module → Files header + input 不再出現；既存 `projectPath` 值仍在 store（disable 不清資料）。 |
| EC-2 | Disable Files 期間 file-tree views 是否能加進 sidebar？ | 是。`getAllViews()` / `getViewDefinition()` 不套 disable filter（I4 known limitation 對齊 `2026-04-23-modules-switchboard-spec.md` §3 I4）。`FileTreeWorkspaceView` 仍可渲染（只看 `projectPath` 是否設定，與 disable 無關）。 |
| EC-3 | 全新 workspace（無 `moduleConfig.files`）+ Files enabled | `FilesWorkspaceSettingsSection` 渲染空 input；輸入後 `setModuleConfig` 建立 `moduleConfig.files.projectPath`。 |
| EC-4 | Re-enable Files + reload → projectPath 已存在 | Header + input 恢復顯示，input value 自動帶回 store 既有值（zustand 訂閱重連）。 |
| EC-5 | 升級路徑（既有使用者已有 `moduleConfig.files.projectPath`） | Storage 不動 = 0 migration；新元件直接讀。 |
| EC-6 | Module enable state 預設 | `useModuleEnabledStore` 預設 enabled（與其他 disableable module 一致）；新使用者體感無變化。 |
| EC-7 | I1 dual-declaration guard (`assertNoLegacyScopeConflict`) | 本 PR 不會觸發 — Files 移除 `workspaceConfig` 後只剩 `settings: [{ scope: 'workspace' }]`，沒 dual。 |

## 6. Test Plan (TDD)

### 6.1 失敗測試先寫

新增 `spa/src/lib/register-modules.test.ts` 測試（或新檔）：

1. **「Files 在 contribution registry 以 workspace scope 出現」**
   ```ts
   // 紅 → 綠
   registerBuiltinModules()
   const list = listContributions('workspace')
   expect(list.find((c) => c.id === 'files.workspace-files')).toBeDefined()
   ```

2. **「Files disable → reload → Files contribution 從 workspace registry 消失」**
   ```ts
   // Simulate persisted disabled state BEFORE registerBuiltinModules() runs,
   // mirroring how the real reload flow works: persist write happens during
   // a previous session, then on next boot registerBuiltinModules() runs and
   // dispatchSettingsContributions() observes enabled.files === false.
   useModuleEnabledStore.setState({ enabled: { files: false }, baseline: null })
   registerBuiltinModules()  // calls dispatchSettingsContributions internally
   const list = listContributions('workspace')
   expect(list.find((c) => c.id === 'files.workspace-files')).toBeUndefined()
   ```
   說明：本測試對應 reload 後的觀察結果。Live toggle 不重 dispatch（與 alpha.288 既有架構一致）；對應的「toggle 後 ReloadBanner 出現」行為由分層測試涵蓋：
   - `useModuleEnabledStore.test.ts` 的 T1-9 系列覆蓋 `hasPendingChanges()` 純邏輯。
   - `ModulesSwitchboardSection.test.tsx` 的 T3-7 涵蓋 ReloadBanner UI 渲染條件。
   本 PR 不複製這兩層的測試。

3. **「Files 標 `disableable: true` + `descriptionKey`」**
   ```ts
   registerBuiltinModules()
   const filesMod = getModule('files')!
   expect(filesMod.disableable).toBe(true)
   expect(filesMod.descriptionKey).toBe('modules.files.description')
   ```

4. **「Files 不再使用 `workspaceConfig`」（拆 SR-2 確認）**
   ```ts
   registerBuiltinModules()
   const filesMod = getModule('files')!
   expect(filesMod.workspaceConfig).toBeUndefined()
   ```

5. **既有測試 `register-modules.test.ts:479` `does NOT warn for files module (exempted during transition)`** — **直接刪除**（採 codex round-1 Q 建議）。Files 不再用 `workspaceConfig`，這個豁免測試失去意義；同檔 fakemod / fakews 兩個 case 已完整涵蓋 deprecation 機制本身。改寫為「Files 特例」測試會殘留心智負擔（讀者看到「Files」會以為仍有特殊處理）。

   取而代之，新增兩個更具體的測試在 §6.1 #4（Files 不再聲明 workspaceConfig）+ 新增「Files bootstrap 不噴 deprecation warning」（與 §6.1 #4 互補）：

   ```ts
   it('does NOT emit any deprecation warning for the real Files bootstrap', () => {
     registerBuiltinModules()
     const msgs = (warnSpy.mock.calls as unknown[][]).map((c) => String(c[0]))
     expect(msgs.some((m) => m.includes('files') && m.includes('deprecated'))).toBe(false)
   })
   ```

### 6.2 元件測試

新增 `spa/src/components/settings/FilesWorkspaceSettingsSection.test.tsx`：

6. **「Read：渲染既有 `projectPath`」** — set store → render → expect input.value
7. **「Write：onChange 寫入 store」** — render → fire input.change → expect store updated
8. **「Empty state：未設定 projectPath → input value === ''」**

### 6.3 整合測試

新增 / 修改 `spa/src/features/workspace/components/WorkspaceSettingsPage.registry.test.tsx`：

9. **「Files enabled before bootstrap → WorkspaceSettingsPage 渲染 Files header + input」**
   ```ts
   // setup
   clearModuleRegistry()
   clearContributions()
   useModuleEnabledStore.setState({ enabled: {}, baseline: null })  // default enabled
   registerBuiltinModules()
   render(<WorkspaceSettingsPage workspaceId={wsId} />)
   expect(screen.getByText(t('settings.section.files_workspace'))).toBeInTheDocument()
   ```

10. **「Files disabled before bootstrap (i.e. simulating reload-after-disable) → WorkspaceSettingsPage 不渲染 Files section」**
    ```ts
    // setup mirrors a session that boots with persisted disabled state —
    // the only path Files becomes hidden in the registry under alpha.288's
    // reload-required architecture.
    clearModuleRegistry()
    clearContributions()
    useModuleEnabledStore.setState({ enabled: { files: false }, baseline: null })
    registerBuiltinModules()
    render(<WorkspaceSettingsPage workspaceId={wsId} />)
    expect(screen.queryByText(t('settings.section.files_workspace'))).toBeNull()
    ```
    說明：testname 含 "before bootstrap" 是為了避免被誤讀為「live toggle 即時隱藏」。Live toggle 不重 dispatch（與 alpha.288 既有架構一致）。

### 6.4 既有測試影響

- `ModuleConfigSection.test.tsx` — 不動（用 fixtures）。
- `dispatch-settings-contributions.test.ts` — 不動（dual-declaration guard 仍以 fakeWS / dualws fixtures 測）。
- `module-registry.test.ts:161-176` — 仍以 fixture 測 `getModulesWithWorkspaceConfig`，不動（helper 還在）。
- `WorkspaceSettingsPage.registry.test.tsx` 既有 case — 看實際是否依賴 Files；若依賴則調整。

### 6.5 驗證

- `pnpm --prefix spa exec vitest run` — 全綠（既有 ~3200 通過 + 新增 ≈ 8）
- `pnpm --prefix spa exec tsc -p tsconfig.app.json --noEmit` — 0 errors
- `pnpm --prefix spa run lint` — 0 errors
- `pnpm --prefix spa run build` — 通過

### 6.6 手動驗證 checklist

- [ ] `/settings/module-config` 出現 Files toggle row（描述用 `modules.files.description`）。
- [ ] **Disable Files（不 reload）→ `/settings` 切 workspace → Files header + input 仍可見** + 切回 `/settings/module-config` 看到 ReloadBanner（與其他 disableable module 同 UX）。
- [ ] **Disable Files → reload page → workspace settings → Files header + input 不出現**（reload-required 行為）。
- [ ] Enable Files + reload → workspace settings → Files header + input 回來，input 帶回原值。
- [ ] 在 input 改值 → `FileTreeWorkspaceView` 實時生效（同 store path）。
- [ ] Disable 期間 file-tree workspace/session views 仍可被加進 sidebar（I4 known limitation）。
- [ ] Browser console 不再噴 `[module] files uses deprecated workspaceConfig` warning。

## 7. Acceptance Criteria

- A1: Files 在 Modules Switchboard 出現，可被 toggle，state 持久（與其他 disableable module 一致）。
- A2: 預先 set `useModuleEnabledStore` `enabled.files = false` → `registerBuiltinModules()` → `listContributions('workspace')` 不含 `files.workspace-files`（reload-required 對齊）。
- A3: Enable Files + bootstrap dispatch 後 `WorkspaceSettingsPage` 渲染 Files contribution（puzzle icon + header + body）。
- A4: `register-modules/index.tsx` Files 區塊不再含 SR-2 inline 註解；不再含 `workspaceConfig` 欄位；改用 `disableable: true` + `descriptionKey` + `settings: [{ scope: 'workspace', order: SETTINGS_ORDER.WORKSPACE_FILES, ... }]`。
- A5: `DEPRECATED_LEGACY_CONFIG_EXEMPT` 不再含 `'files'`，註解去 Files 特例字眼；boot-time dispatch 對 Files 不噴 deprecation warning。
- A6: 所有既有 `projectPath` reader (`FileTreeView`, `file-open-bootstrap.ts`) 不需改動。
- A7: `SETTINGS_ORDER` 新增 `WORKSPACE_FILES = 10`；註解更新為「all scopes from this constant」。
- A8: 全測試通過（vitest / tsc / lint / build）。

## 8. Risks & Trade-offs

| Risk | Severity | Mitigation |
|---|---|---|
| `ModuleConfigSection` 留著但無人用會誤導讀者 | Low | Spec / PR 說明 + follow-up issue 追蹤拆除 |
| 沒對齊 `EditorHomePathWorkspaceSection` 的 trim/blur/race 處理 | Low | 對齊既有 `ConfigField` 的 onChange-immediate 寫法（最小 diff）；trim/blur 是 Editor home path 的特殊需求，Files projectPath 不必同 |
| Files purdex sidebar 無 entry 可能被未來「所有 disableable 都要 settings page」規則打破 | Low | 對齊 Browser 的處理；新規則沒落地，落地時再統一加 placeholder |
| Reload-required UX：使用者 toggle 後沒看 banner 直接切到 Workspace settings 會以為 toggle 沒生效 | Low | 對齊 alpha.288 既有 module（Editor / Quick Commands / Performance Monitor）；ReloadBanner 已是 product-wide UX |
| Disable Files 後 storage 既存 `projectPath` 是否該清？ | None | 不清。disable = 隱藏 UI，不破壞資料。對齊其他 disableable module 行為 |
| `WORKSPACE_FILES = 10` 與未來其他 workspace contribution 撞號 | Low | 目前 workspace scope 只有 Editor（inline 0）+ Files（10）；未來新增時按需收編進 SETTINGS_ORDER |

## 9. Out of Scope (follow-up issues)

- F-1: 拆 `ModuleConfigSection.tsx` + `getModulesWith*Config()` helpers + `ModuleDefinition.workspaceConfig` / `globalConfig` 欄位（無真實使用者後 dead code）。
- F-2: `ModulesSwitchboardSection` line 64-68 過時註解清理（提到 `editor/files/browser/memory-monitor` 都無 purdex contribution 已不正確）。
- F-3: Files purdex placeholder（若未來確立「所有 disableable module 都要 settings page」規則）。

## 10. Implementation Order (PR 不切 phase)

PR 規模 ~250 LOC change，單一 PR 不切 phase。Commit 順序建議：

1. **Commit 1 — i18n keys + `SETTINGS_ORDER.WORKSPACE_FILES`**：新增 3 個 i18n key（en + zh-TW）+ 常數 + 註解更新。無功能變更。
2. **Commit 2 — `FilesWorkspaceSettingsSection` 元件 + 單元測試**：新檔 + 紅綠測試。
3. **Commit 3 — Files module 遷移**：`register-modules/index.tsx` Files 改寫 + 拔 SR-2 註解 + `dispatch-settings-contributions.ts` 拔 exempt 與註解 + 對應 test 更新（紅 → 綠：reload-after-disable 過濾、deprecation warning 不噴）。
4. **Commit 4 — 整合測試 + PR description 手動 checklist**：`WorkspaceSettingsPage.registry.test.tsx` 增 enable / disabled-after-reload 雙測，更新 PR description。

每個 commit 獨立通過 vitest / lint / tsc。

注意：`VERSION` + `CHANGELOG.md` bump 是 PR merge **後**的獨立 bump PR，不在本 PR 內動（對齊 repo 規範 — 詳見 CLAUDE.md「完整開發流程」第 9 步）。
