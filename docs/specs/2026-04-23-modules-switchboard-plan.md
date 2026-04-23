# Modules Switchboard — Implementation Plan

> 日期：2026-04-23
> Worktree：`modules-tab-cleanup-puzzle-icon`（branch `worktree-modules-tab-cleanup-puzzle-icon`）
> Base commit：alpha.215（`29dd2e1a`）
> Spec ref：`docs/specs/2026-04-23-modules-switchboard-spec.md`
> 狀態：Ready for Implementation

---

## §1 Goal

本 PR 將 Settings sidebar 中長期空白的 Modules tab（舊 `globalConfig` 容器，production 無 caller）重新定位為 **Modules Switchboard**：列出所有宣告 `disableable: true` 的 module、提供 toggle、描述說明、跳轉 settings 連結，以及「Reload required」差異 banner。

具體產出：

1. `useModuleEnabledStore`：persisted `Record<moduleId, boolean>`，baseline snapshot（session 一次性，`baseline` 不進 localStorage）
2. `ModuleDefinition` 擴充 `disableable?: boolean` + `descriptionKey?: string`
3. `buildSettingsContributionBatch` 讀 store，disabled module 整批跳過 settings dispatch（含 workspace / host scope contributions）
4. `ModulesSwitchboardSection` UI 元件，接管 `module-config` registration；舊 `ModuleConfigSection.tsx` 保留原檔但取消 registration
5. `register-modules.tsx`：恢復 `module-config` section registration、標記 `editor` / `files` / `browser` / `memory-monitor` 四個 module 為 `disableable: true`、插入 `captureBaseline` 呼叫（在 `dispatchSettingsContributions` 正上方）
6. i18n：`settings.section.modules` 鍵回歸 + 4 個 `descriptionKey` + 2 個 banner key（en.json + zh-TW.json）
7. Puzzle-piece icon（已完成於本 worktree）：lint / regression 確認即可，不重新實作

PR 範圍為單一 commit，估計 ~400–600 LOC 含測試。

---

## §2 File Touch List

| 檔案路徑 | 操作 | 用途 | 預估 LOC |
|---|---|---|---|
| `spa/src/stores/useModuleEnabledStore.ts` | 新增 | persisted enabled map + in-memory baseline + actions | ~70 |
| `spa/src/stores/useModuleEnabledStore.test.ts` | 新增 | §3.1 全部 12 個測試 | ~100 |
| `spa/src/lib/storage/keys.ts` | 修改 | 加 `MODULE_ENABLED: 'purdex-module-enabled'` | +1 |
| `spa/src/lib/module-registry.ts` | 修改 | `ModuleDefinition` 加 `disableable?` + `descriptionKey?` | +3 |
| `spa/src/lib/dispatch-settings-contributions.ts` | 修改 | `buildSettingsContributionBatch` module loop 加 `isEnabled` guard | +5 |
| `spa/src/lib/dispatch-settings-contributions.test.ts` | 修改 | 追加 §3.2 的 6 個 disabled-module filter 測試 | +60 |
| `spa/src/components/settings/ModulesSwitchboardSection.tsx` | 新增 | Switchboard UI：list / toggle / banner / Open settings link | ~110 |
| `spa/src/components/settings/ModulesSwitchboardSection.test.tsx` | 新增 | §3.3 全部 9 個測試 | ~80 |
| `spa/src/lib/register-modules.tsx` | 修改 | 恢復 module-config registration + 4 flag + captureBaseline | +15 |
| `spa/src/locales/en.json` | 修改 | 新增 7 個 key | +9 |
| `spa/src/locales/zh-TW.json` | 修改 | 對應中文翻譯 7 個 key | +9 |

不動：`ModuleConfigSection.tsx`（保留但取消 registration）、`settings-contribution-types.ts`（puzzle helper 已完成）、`SettingsSidebar.tsx` / `HostSidebar.tsx` / `WorkspaceSettingsPage.tsx`（puzzle icon 已完成）。

---

## §3 TDD Test Matrix

### §3.1 `useModuleEnabledStore`（12 個案例）

| 編號 | 意圖 | Setup | Assertion |
|---|---|---|---|
| T1-1 | `disableable: true` module 無 persist → 預設 enabled | 乾淨 store；module 定義含 `disableable: true` | `isEnabled('editor') === true` |
| T1-2 | non-disableable module 無 persist → 永遠 true | 乾淨 store；module 定義 `disableable: false` | `isEnabled('sessions') === true` |
| T1-3 | `setEnabled` 寫 false → `isEnabled` 回 false | 呼叫 `setEnabled('editor', false)` | `isEnabled('editor') === false` |
| T1-4 | `setEnabled` 再設 true → 回復 | 先 false 再 true | `isEnabled('editor') === true` |
| T1-5 | `setEnabled` 後 localStorage 含 persist 資料 | `setEnabled('editor', false)` | `JSON.parse(localStorage.getItem('purdex-module-enabled')).state.enabled.editor === false` |
| T1-6 | hydrate：從 localStorage 恢復後 `isEnabled` 反映持久值 | 模擬 persist entry `{ state: { enabled: { editor: false } } }`，create new store | `isEnabled('editor') === false` |
| T1-7 | `captureBaseline` 首次呼叫 → `baseline` 非 null | `captureBaseline({ editor: true, files: true })` | `store.getState().baseline` 深等於傳入 snapshot |
| T1-8 | `captureBaseline` 重複呼叫 → no-op | 先傳 `{ editor: true }` 再傳 `{ editor: false }` | `baseline.editor === true`（首次值保留） |
| T1-9 | `hasPendingChanges` — enabled 與 baseline 一致 → false | `captureBaseline({ editor: true })`；`setEnabled('editor', true)` | `hasPendingChanges() === false` |
| T1-10 | `hasPendingChanges` — toggle 後 diff 存在 → true | `captureBaseline({ editor: true })`；`setEnabled('editor', false)` | `hasPendingChanges() === true` |
| T1-11 | `hasPendingChanges` — baseline 為 null → false | 乾淨 store，未呼叫 `captureBaseline` | `hasPendingChanges() === false` |
| T1-12 | `resetAll` — 清 `enabled`；`baseline` 不動 | `captureBaseline({ editor: false })`；`setEnabled('browser', false)`；`resetAll()` | `enabled === {}`；`baseline.editor === false` 不變 |

### §3.2 `buildSettingsContributionBatch` filter（6 個案例）

| 編號 | 意圖 | Setup | Assertion |
|---|---|---|---|
| T2-1 | disabled module settings 整批排除 | module `a` 有 purdex contribution；`setState({ enabled: { a: false } })` | dispatch 後 `listContributions('purdex')` 不含 `a.*` |
| T2-2 | enabled module settings 正常 dispatch | 同上但 `enabled: { a: true }` | `listContributions('purdex')` 含 `a.*` |
| T2-3 | disabled module 不參與 localId collision | `a` / `b` 皆宣告 `localId: 'x'`；`a` disabled | dispatch 不拋；`b.x` 出現在 registry |
| T2-4 | legacy adapter contributions 不受 `isEnabled` 影響 | `registerSettingsSection` 加 legacy section；`enabled` 全 false | legacy section 仍在 `listContributions('purdex')` |
| T2-5 | host-builtin contributions 不受 `isEnabled` 影響 | `setHostBuiltinSections([...])` 加 host section；`enabled` 全 false | host section 仍在 `listContributions('host')` |
| T2-6 | disabled module 的 workspace scope contribution 亦跳過 | module `a` 宣告 `scope: 'workspace'` contribution；`a` disabled | `listContributions('workspace')` 不含 `a.*` |

### §3.3 `ModulesSwitchboardSection` UI（9 個案例）

| 編號 | 意圖 | Setup | Assertion |
|---|---|---|---|
| T3-1 | 只列 `disableable: true` module | registry 含 `editor`（disableable）+ `sessions`（非 disableable） | 只見 editor row；sessions 不在 DOM |
| T3-2 | 全部 4 個 disableable module 皆 render | registry 含全部 4 個 module | 4 row 皆可見 |
| T3-3 | toggle enabled → disabled 觸發 `setEnabled(id, false)` | `isEnabled('editor') = true`；click toggle | `setEnabled` spy 收到 `('editor', false)` |
| T3-4 | toggle disabled → enabled 觸發 `setEnabled(id, true)` | `isEnabled('editor') = false`；click toggle | `setEnabled` spy 收到 `('editor', true)` |
| T3-5 | "Open settings →" 僅在有 purdex contribution 時出現 | `editor` 有 purdex contribution；`files` 無 | editor row 含連結；files row 無連結 |
| T3-6 | "Open settings →" 在 module disabled 時 aria-disabled + 不觸發 navigation | `isEnabled('editor') = false`；editor 有 purdex contribution | 連結有 `aria-disabled="true"`；click 後 `setLocation` spy 未被呼叫 |
| T3-7 | Banner 在 `hasPendingChanges() === true` 時顯示 | `captureBaseline({ editor: true })`；`setEnabled('editor', false)` | DOM 含 banner 元素含 `settings.modules.reload_required.title` 文字 |
| T3-8 | Banner 在 `hasPendingChanges() === false` 時不顯示 | 無 diff（enabled 與 baseline 一致） | DOM 無 banner 元素 |
| T3-9 | `descriptionKey` 翻譯文字呈現於 row | `editor` 帶 `descriptionKey: 'modules.editor.description'`；locale 含對應文字 | row 內含 description 文字 |

### §3.4 Integration wire-up（4 個案例）

| 編號 | 意圖 | Setup | Assertion |
|---|---|---|---|
| T4-1 | `captureBaseline` 在 dispatch 前被呼叫 | spy `captureBaseline`；call `registerBuiltinModules()` | spy 在 `dispatchSettingsContributions` 前執行；`baseline` 非 null |
| T4-2 | `editor` disabled → editor purdex settings 不在 registry | `setEnabled('editor', false)`；re-dispatch | `listContributions('purdex')` 不含 `editor.*` |
| T4-3 | HMR simulate + re-dispatch → persist 保留 disabled 狀態 | `setEnabled('editor', false)`；`resetSettingsContributionsForHmr()`；re-dispatch | `listContributions('purdex')` 仍不含 `editor.*` |
| T4-4 | Puzzle-piece regression：module-owned contribution `isModuleOwnedContribution` 回 true | dispatch editor contributions | `editor.workspace-home-path` 的 `isModuleOwnedContribution(c) === true`；legacy section 回 false |

---

## §4 Implementation Steps

### Step 1：`STORAGE_KEYS` 加 `MODULE_ENABLED`

**前置測試**：無（常數變更）

**Touch**：`spa/src/lib/storage/keys.ts`

在 `STORAGE_KEYS` 物件末尾加入 `MODULE_ENABLED: 'purdex-module-enabled'`，遵循 `purdex-*` 命名慣例。

**Verification**：TypeScript compile 通過。

---

### Step 2：寫 T1-1 ~ T1-12 測試（紅）

**Touch**：`spa/src/stores/useModuleEnabledStore.test.ts`（新增）

`beforeEach` 用 `useModuleEnabledStore.setState({ enabled: {}, baseline: null })` 重置可變 fields（merge-mode，不 wipe actions，依 `feedback_zustand_harness_setstate`）。persist 相關測試直接操作 `localStorage`。

---

### Step 3：實作 `useModuleEnabledStore`（綠）

**前置測試**：T1-1 ~ T1-12

**Touch**：`spa/src/stores/useModuleEnabledStore.ts`（新增）

```ts
// 介面簽名（pseudocode）
interface ModuleEnabledState {
  enabled: Record<string, boolean>           // persisted
  baseline: Record<string, boolean> | null   // in-memory only
  setEnabled: (moduleId: string, value: boolean) => void
  resetAll: () => void
  captureBaseline: (snapshot: Record<string, boolean>) => void
  isEnabled: (moduleId: string) => boolean
  hasPendingChanges: () => boolean
}
```

- `persist` key：`STORAGE_KEYS.MODULE_ENABLED`；storage：`purdexStorage`
- `partialize`：只序列化 `enabled`（`baseline` 不進 localStorage）
- `isEnabled` 邏輯：先查 `enabled[moduleId]`（有則回）；無則查 `getModule(moduleId)?.disableable`，`true` 回 true，其餘一律 true（永不 disable non-disableable module）
- `captureBaseline` 邏輯：`if (get().baseline !== null) return`；`set({ baseline: snapshot })`
- `hasPendingChanges` 邏輯：`baseline === null` → false；遍歷 baseline keys 比對 `isEnabled(key)` vs `baseline[key]`，任一 diff 回 true
- `resetAll` 邏輯：`set({ enabled: {} })`（不動 baseline）
- 不掛 `syncManager`（本機偏好，不跨設備同步）

**Verification**：`vitest run spa/src/stores/useModuleEnabledStore.test.ts` 全綠。

---

### Step 4：`ModuleDefinition` 擴充欄位

**前置測試**：無（pure type change）

**Touch**：`spa/src/lib/module-registry.ts`

```ts
// 在 ModuleDefinition interface 新增（pseudocode）
disableable?: boolean    // default false; only true modules appear in switchboard
descriptionKey?: string  // i18n key for the switchboard row description
```

加 JSDoc 說明 `disableable` 預設 false 的設計意圖（safe default，顯式 opt-in）。

**Verification**：`pnpm run lint` 無新錯誤。

---

### Step 5：寫 T2-1 ~ T2-6 測試（紅）

**Touch**：`spa/src/lib/dispatch-settings-contributions.test.ts`（修改）

在現有 `describe` 之後追加 `describe('disabled module filter')` 區塊。直接用 `useModuleEnabledStore.setState({ enabled: { a: false } })` 設定 test enabled state（不需額外 mock，因為 `buildSettingsContributionBatch` 在執行時同步 `getState()`）。

---

### Step 6：`buildSettingsContributionBatch` 加 `isEnabled` filter（綠）

**前置測試**：T2-1 ~ T2-6

**Touch**：`spa/src/lib/dispatch-settings-contributions.ts`

在 `for (const module of modules)` 迴圈最頂端加一行 guard：

```ts
// pseudocode
for (const module of modules) {
  if (!useModuleEnabledStore.getState().isEnabled(module.id)) continue
  // ...現有邏輯不動...
}
```

加對應 import。Legacy / host-builtin 迴圈不加 guard（它們不在 `modules` 陣列）。

**Verification**：`vitest run spa/src/lib/dispatch-settings-contributions.test.ts` 全綠。

---

### Step 7：寫 T3-1 ~ T3-9 測試（紅）

**Touch**：`spa/src/components/settings/ModulesSwitchboardSection.test.tsx`（新增）

`beforeEach`：`clearModuleRegistry()` + `clearContributions()` 清空；手動 `registerModule(...)` 塞測試 module。Mock `wouter` 的 `useLocation` 回 `['/settings/module-config', mockSetLocation]`。`hasPendingChanges` 透過直接 setState 控制（無需額外 spy）。

---

### Step 8：實作 `ModulesSwitchboardSection`（綠）

**前置測試**：T3-1 ~ T3-9

**Touch**：`spa/src/components/settings/ModulesSwitchboardSection.tsx`（新增）

```tsx
// pseudocode 骨架
function ModulesSwitchboardSection({ ctx: _ctx }: { ctx: SettingsContextFor<'purdex'> }) {
  const hasPending = useModuleEnabledStore(s => s.hasPendingChanges())
  const t = useI18nStore(s => s.t)
  const [, setLocation] = useLocation()
  const modules = getModules().filter(m => m.disableable === true)

  return (
    <div>
      {hasPending && <ReloadBanner t={t} />}
      {modules.map(m => (
        <ModuleRow key={m.id} module={m} setLocation={setLocation} t={t} />
      ))}
    </div>
  )
}
```

- `ModuleRow` 子元件：reactive 訂閱 `useModuleEnabledStore(s => s.isEnabled(m.id))`；使用現有 `ToggleSwitch` + `PuzzlePiece`（Phosphor）
- "Open settings →" 條件：`listContributions('purdex').some(c => c.moduleId === m.id)`；取 first match 的 `localId` 做 navigate
- disabled 時連結加 `aria-disabled="true"` + 點擊 no-op（不呼叫 setLocation）

**Verification**：`vitest run spa/src/components/settings/ModulesSwitchboardSection.test.tsx` 全綠。

---

### Step 9：`register-modules.tsx` wire-up

**前置測試**：T4-1 ~ T4-4（Step 1–8 完成後執行）

**Touch**：`spa/src/lib/register-modules.tsx`

**9a. 恢復 module-config section**：
- 加 import `ModulesSwitchboardSection`
- 加 `registerSettingsSection({ id: 'module-config', label: 'settings.section.modules', order: 8, component: ModulesSwitchboardSection })`
- `id: 'module-config'` 維持不變（URL stability）

**9b. 4 個 module 加 `disableable: true` + `descriptionKey`**：

```ts
// pseudocode — 4 個 registerModule 呼叫各加這兩行
disableable: true,
descriptionKey: 'modules.<id>.description',
```

對應 id：`editor` / `files` / `browser` / `memory-monitor`（`memory-monitor` 的 descriptionKey 用 `modules.memory_monitor.description`，底線不連字號）

**9c. `captureBaseline` 呼叫（在 `dispatchSettingsContributions()` 正上方）**：

```ts
// pseudocode
const disableableIds = getModules()
  .filter(m => m.disableable === true)
  .map(m => m.id)
const snapshot: Record<string, boolean> = {}
for (const id of disableableIds) {
  snapshot[id] = useModuleEnabledStore.getState().isEnabled(id)
}
useModuleEnabledStore.getState().captureBaseline(snapshot)
dispatchSettingsContributions()
```

加 import `useModuleEnabledStore`。

**Verification**：`vitest run spa/src/lib/dispatch-settings-contributions.test.ts` + T4-* 全綠。

---

### Step 10：i18n 新增 key

**Touch**：`spa/src/locales/en.json`、`spa/src/locales/zh-TW.json`

**en.json 新增 7 個 key**（建議位置：緊接 `settings.section.editor_buffers` 之後）：

```
"settings.section.modules": "Modules",
"settings.modules.reload_required.title": "Changes require reload to take effect",
"settings.modules.reload_required.hint": "Disabled modules hide their settings entries; pane types and filesystem backends still register until a future release.",
"modules.editor.description": "Text and rich content editor with syntax highlighting",
"modules.files.description": "File browser panes for workspace and session directories",
"modules.browser.description": "Embedded web browser pane (requires desktop app)",
"modules.memory_monitor.description": "System memory and renderer diagnostics"
```

**zh-TW.json 對應翻譯**（7 個 key）。

**Verification**：`pnpm run lint` 通過（i18n key 完整）；T3-7 / T3-9 因 locale 補齊而全綠。

---

### Step 11：全套驗證

```
cd spa && npx vitest run
cd spa && pnpm run lint
cd spa && pnpm run build
```

**Regression 重點**：
- `/settings/module-config` URL 可用（`module-config` 出現在 `listContributions('purdex')`，`isSelectable` 通過）
- Puzzle-piece icon 出現在 `editor.workspace-home-path` sidebar row；不出現在 `_builtin.legacy-section.*` row（SettingsSidebar 既有實作，T4-4 regression）
- Toggle editor disabled → banner 出現；reload 模擬（`resetSettingsContributionsForHmr` + re-dispatch）→ editor purdex sections 消失、banner 消失

---

## §5 Edge Cases / Failure Modes

**EC-1：Baseline 重複呼叫（HMR 重載）**

`captureBaseline` 的 `if (baseline !== null) return` guard 防止第二次呼叫覆蓋。HMR dispose 不 clear `useModuleEnabledStore`（I7），`baseline` 跨 HMR 保留。

**EC-2：Store hydrate race**

`purdexStorage` 用 `createJSONStorage(() => browserStorage)`（localStorage，sync read）。`create()` 時 hydration 同步完成，`captureBaseline` 呼叫時 `enabled` 已是持久化值，無 async race。

**EC-3：Disabled module 的 localId 與 enabled module 衝突（T2-3）**

Filter guard 在 `for` 迴圈最頂端，disabled module 整個跳過，**不** 進 collision check。disabled module 的 localId 不妨礙 enabled module 的同名 localId。

**EC-4：`resetAll` 後 banner 消失（false positive 防止）**

`resetAll` 清空 `enabled`，`isEnabled` 回 default（全 true）。若 `baseline` 中所有 module 也是 `true`，`hasPendingChanges()` 回 false → banner 消失，符合語義。

**EC-5：`files` / `browser` 無 purdex contribution，banner 仍顯示（spec §7 Q4）**

toggle 記錄在 `enabled` map，`hasPendingChanges()` 可回 true → banner 顯示。語義一致性優先於視覺精確性（即便 v1 side-effect 對 `files` 視覺無變化）。

**EC-6：HMR dispose — `baseline` 不清（I7）**

`resetSettingsContributionsForHmr()` 不 touch `useModuleEnabledStore`。下次 HMR 重跑 `registerBuiltinModules` 時 `captureBaseline` no-op，`baseline` 保留 boot 時的值。

**EC-7：`disableable` 改 false 後 persist 中有 stale disabled entry**

`isEnabled` 對 `disableable: false` module 永遠回 true，忽略 `enabled[moduleId]`。stale entry 留在 localStorage 無害。

**EC-8：Workspace / host scope contributions 亦被 filter（T2-6）**

`buildSettingsContributionBatch` 的 filter 作用於 `for (const module of modules)` 整個迴圈，所有 scope 的 declarations 均被跳過，符合 I2（toggle 影響 module 全部 `settings: [...]`）。

**EC-9：Contribution collision 與 disabled module**

EC-3 已覆蓋：disabled module 不進 collision check，不會衝突。

---

## §6 Rollback / Revert Notes

**最小回退範圍**（若 merge 後出問題）：

1. `register-modules.tsx`：移除 `module-config` registration、4 個 `disableable` flag、`captureBaseline` 呼叫與 import
2. `dispatch-settings-contributions.ts`：移除 `isEnabled` guard 與 `useModuleEnabledStore` import
3. 刪除 `spa/src/stores/useModuleEnabledStore.ts` + `.test.ts`
4. 刪除 `spa/src/components/settings/ModulesSwitchboardSection.tsx` + `.test.tsx`
5. `module-registry.ts`：移除 `disableable` + `descriptionKey` 欄位
6. `storage/keys.ts`：移除 `MODULE_ENABLED`
7. `en.json` + `zh-TW.json`：移除新增 7 個 key

**Migration**：Alpha 階段無 persist migration（`feedback_no_alpha_migration`）。`purdex-module-enabled` localStorage key 若存在，revert 後為孤立 key，無害不影響其他 store。

---

## §7 Open Items for Implementation Q&A

**Q-impl-1**：`dispatch-settings-contributions.ts` 直接 import `useModuleEnabledStore` — 是否觸發 ESLint `no-restricted-imports` 規則？

確認 `.eslintrc` / `eslint.config` 中 dispatch 模組有無 store import 限制。若有，備選方案：在 `dispatchSettingsContributions()` 的呼叫端（`register-modules.tsx`）預先 filter modules，以 `modules.filter(m => useModuleEnabledStore.getState().isEnabled(m.id))` 傳入 `dispatchSettingsContributions(filteredModules)`，避免 lib 層直接依賴 store。

**Q-impl-2**：`ModulesSwitchboardSection` 的 "Open settings →" 在 module disabled 時行為（spec §7 Q1 分歧）

Spec 建議 grey + unclickable；但 v1 disabled 只影響下次 dispatch，當前 session 的 contribution 仍在 registry、section 仍可存取。實作前確認：若採 spec 建議（unclickable），T3-6 測試需驗 `aria-disabled`；若採 "仍可點"，T3-6 需調整 assertion。建議依 spec 建議（unclickable）實作，語義清晰。

**Q-impl-3**：T4-1（captureBaseline 在 dispatch 前呼叫）的整合測試策略

`registerBuiltinModules` 副作用多（fs backend / sync contributors 等），建議 T4-1 改為 unit test：擷取 `captureBaseline` + `dispatchSettingsContributions` 呼叫的小段邏輯（抽 helper 或直接 spy 兩個函式），驗呼叫順序，而非測完整 `registerBuiltinModules`。若測完整版本，需大量 mock。

**Q-impl-4**：Test harness reset 方式

`useModuleEnabledStore.setState({ enabled: {}, baseline: null })` 用 merge-mode（不 wipe actions），符合 `feedback_zustand_harness_setstate`。若發現 `hasPendingChanges` 在 merge reset 後仍殘留舊 baseline，改用 `setState({ enabled: {}, baseline: null })` 確保 baseline 被 null 化。

**Q-impl-5**：`captureBaseline` snapshot 計算時機 — `disableable` flag 已全部 `registerModule` 完畢？

是。Step 9c 的 `captureBaseline` 呼叫在所有 `registerModule(...)` 之後、`dispatchSettingsContributions()` 之前，`getModules()` 可返回含 flag 的完整 module list。

**Q-impl-6**：`memory-monitor` 的 `descriptionKey` 命名

`modules.memory_monitor.description`（底線，非 `memory-monitor`），因為 i18n key 通常避免 hyphen 在 key segment 中造成 parser 困惑（參考 `monitor.provider_label` 等現有命名）。

---

*共 §7 節、31 個測試案例（T1-1~12 / T2-1~6 / T3-1~9 / T4-1~4）、11 個 Implementation Steps。*
