# Modules Switchboard + Module-owned Contribution Marker Spec

- **Date**: 2026-04-23
- **Worktree**: `modules-tab-cleanup-puzzle-icon`（branch `worktree-modules-tab-cleanup-puzzle-icon`）
- **Context**: HSR 全系列完工 (#604 alpha.213) 後，Settings sidebar 的 `Modules` tab 是 dead UI（掛在 legacy `globalConfig` 容器，production 無任何 module 使用），由 #574 追蹤移除。本 PR 把它**重新定位為 module 開關總覽面板**（switchboard），並補上一個輔助使用者辨識「哪些 settings 項目是來自 module（而非 built-in / legacy adapter）」的 UI 標記。

## 1. 背景

HSR 完成後 settings 註冊機制有三個 source：

1. **Module-declared**（`ModuleDefinition.settings: [...]`）— `moduleId = module 自己的 id`，例如 `editor`
2. **Legacy adapter**（`registerSettingsSection()`）— `moduleId = '_builtin.legacy-section'`，蓋住 Appearance / Terminal / Interface / Sync / Editor Buffers / Desktop App / Development / Tmux Agent Monitor 八個 section
3. **Host built-in**（`host-builtin-sections`）— `moduleId = '_builtin.host'`，蓋住 HostPage 六個子頁

使用者視覺上無法分辨哪些項目來自**真正的 module**（理論上可獨立 enable/disable）vs **built-in / legacy**（core app 的一部分）。

此外，`Modules` tab 原本為了讓宣告 `globalConfig` 的 module 有個 UI 落點而存在；現在 HSR 路徑接手，`globalConfig` 已 deprecated 且 prod 無 caller，tab 內容永遠空白。

本 spec 一次處理兩件事：
- **UI 標記**：Settings sidebar / Workspace settings / Host sidebar 內的 module-owned row/section 在標題尾加 puzzle-piece icon（已於本 worktree 先行實作）
- **Switchboard 復活**：把 Modules tab 重新定位成「可獨立 enable/disable 的 module 總覽 + 開關」，類似 VSCode 的 Extensions tab

## 2. 範圍 + Non-goals

### 範圍（in scope）

- `isModuleOwnedContribution(c)` 共用 helper + puzzle-piece UI 渲染（3 shell）
- `ModuleDefinition.disableable?: boolean` + `descriptionKey?: string` 宣告欄位
- `useModuleEnabledStore`：persisted `Record<moduleId, boolean>`、default 以 `disableable` 為 true 時預設 enabled、boot baseline snapshot
- `ModulesSwitchboardSection` UI：列 disableable module、toggle、description、Open settings → 跳轉、Reload required banner
- `dispatchSettingsContributions` 讀 store，disabled module 的 `settings: [...]` 整批跳過
- Flag 4 個 module 為 `disableable: true`：`editor` / `files` / `browser` / `memory-monitor`
- i18n：恢復 `settings.section.modules` 鍵 + 4 module descriptionKey + banner 字

### Non-goals（PR 3 或之後）

- pane-level disable（`panes: [...]` 跳過；既存 tab redirect / unmount）
- `registerFsBackend` / `registerFileOpener` / `registerNewTabProvider` 的 disable 整合
- Runtime hot-switch：disable mid-session 即時生效（v1 需 reload）
- Workspace / host scope 的 module toggle（此 spec 只 purdex scope）
- Module 的安裝/移除（install/uninstall，非 enable/disable）
- Core module 的 disable（sessions / new-tab / settings / hosts / dashboard / history / editor-newtab-provider 類的系統級 module 不開放 disable）
- Modules tab 本身支援 disable 自己（Modules tab 永遠存在）

## 3. 不變量

- **I1**：核心 module 無法被 disable — `ModuleDefinition.disableable` default 為 `false`；只有顯式宣告 `true` 的 module 出現在 switchboard 且可被 toggle
- **I2**：Enabled 是 module-level 概念，不是 contribution-level — toggle 一個 module 會影響該 module 全部 `settings: [...]` contributions（v1 也只影響這一類）
- **I3**：`useModuleEnabledStore` 的 defaults 以 `disableable: true` 的 module 預設 `enabled: true` 為準；persist 過的 value 覆蓋 default；未出現在 store 的 module id = fall back 到 default
- **I4**：Disable 的 side-effect 僅作用於 `dispatchSettingsContributions`（v1 唯一 hook 點）— 其他 registry（panes / fs / file-opener / new-tab）**不受影響**。`descriptionKey` 或 README 類文案必須明確告知「目前 disable 只隱藏 settings sidebar 項目，不阻擋既存功能」，避免誤會
- **I5**：Baseline snapshot 在 app boot 的**第一次 dispatch 前**捕捉一次，後續不變 — banner 比較依據。Store 的 enabled state 由 toggle 改變，baseline 固定，兩者差異 = banner 顯示「Reload required」
- **I6**：Puzzle-piece icon 只在 `isModuleOwnedContribution(c) === true`（`moduleId` 不以 `_builtin.` 開頭）的 row/section 出現 — 跟本 PR 的 Modules switchboard 開關**正交**：puzzle 代表「此 contribution 來自 module」，並非「此 module 目前 enabled」
- **I7**：HMR dispose 會 clear 所有 contribution 與 legacy pending buffer，但**不清** `useModuleEnabledStore`（使用者 toggle 過的偏好要跨 HMR 保留）

## 4. 設計

### 4.1 `ModuleDefinition` 擴充

`spa/src/lib/module-registry.ts`：

```ts
export interface ModuleDefinition {
  id: string
  name: string
  panes?: PaneDefinition[]
  settings?: AnySettingsContributionDeclaration[]
  globalConfig?: ConfigDef[]          // @deprecated
  workspaceConfig?: ConfigDef[]       // @deprecated

  // NEW
  disableable?: boolean              // default false; only true modules appear in switchboard
  descriptionKey?: string            // i18n key for the switchboard row description
}
```

`disableable` default false 的選擇（而非 true）：
- **Safe default**：避免既有所有 module 一次暴露在 switchboard 裡、意外被 disable
- **顯式 opt-in**：每個 module owner 要刻意想過「我這個 module 獨立性夠不夠」才加 flag
- 未來可重新評估（例如大量 module 出現後改 default true + core 顯式 `disableable: false`）

### 4.2 `useModuleEnabledStore`

`spa/src/stores/useModuleEnabledStore.ts`（新檔）：

```ts
interface ModuleEnabledState {
  // Persisted: only entries for modules the user has explicitly toggled.
  // Defaults to `true` for any `disableable: true` module not in this map.
  enabled: Record<string, boolean>

  // In-memory only: captured at app boot (first dispatch).
  // `null` until `captureBaseline()` runs once.
  baseline: Record<string, boolean> | null

  setEnabled: (moduleId: string, enabled: boolean) => void
  resetAll: () => void
  captureBaseline: (snapshot: Record<string, boolean>) => void
  isEnabled: (moduleId: string) => boolean  // selector convenience
  hasPendingChanges: () => boolean          // compares enabled ↔ baseline
}
```

Persist key：`purdex-module-enabled`（依 `feedback_concurrent_session_safety` + `reference_localstorage_audit` 的 `purdex-*` 命名）。

`isEnabled(moduleId)` 邏輯：
1. 查 `enabled[moduleId]` — 有值就返回
2. 沒值時查 module definition 的 `disableable`：
   - `disableable: true` → `true`（預設開）
   - `disableable: false` 或未宣告 → `true`（永遠開，不可 disable）

`captureBaseline(snapshot)` 在 `register-modules.tsx::registerBuiltinModules()` 的**第一次 dispatch 之前**被呼叫一次，傳入「以當前 persist 過的 enabled + 各 module disableable 解析出的 effective snapshot」。第二次之後呼叫 no-op（baseline 一個 session 只設一次）。

**Baseline 生命週期**：
- App boot → `registerBuiltinModules()` → 計算 effective snapshot → `captureBaseline(snapshot)` → `dispatchSettingsContributions(modules)`（讀同一 snapshot filter）
- User toggle → `setEnabled()` 寫 persist — baseline 不動
- `hasPendingChanges()` 比對 `enabled` vs `baseline` → banner 顯示
- Reload → 重新 boot，baseline 被新 enabled snapshot 覆蓋

### 4.3 `dispatchSettingsContributions` 整合

`buildSettingsContributionBatch()` 在 module loop 前查 `useModuleEnabledStore.getState().isEnabled(module.id)`，disabled 的 module 整個 `settings: [...]` 跳過（含 validation collision check — disabled module 不參與 localId 衝突判斷）。

Legacy adapter 與 host built-in 的 contributions **不受影響**，因為它們的 moduleId 屬於 `_builtin.*` namespace 且 `disableable` 概念不適用於 legacy section。

```ts
for (const module of modules) {
  if (!useModuleEnabledStore.getState().isEnabled(module.id)) continue
  const settings = module.settings
  if (!settings || settings.length === 0) continue
  // ...
}
```

### 4.4 `ModulesSwitchboardSection` 元件

`spa/src/components/settings/ModulesSwitchboardSection.tsx`（新檔），由 `register-modules.tsx` 的 `registerSettingsSection({id: 'module-config', ...})` 登記（保留原 registration，只換 component）。

內容結構：

```
┌─────────────────────────────────────────────────────┐
│ [Banner: Reload required to apply changes]          │  ← hasPendingChanges() 時顯示
├─────────────────────────────────────────────────────┤
│ [PuzzlePiece] Editor         [●━━━] Toggle          │
│   Text / rich content editor                        │
│   Open settings →                                   │
│                                                     │
│ [PuzzlePiece] Files          [━━●] Toggle           │
│   File browser panes                                │
│   Open settings →  (disabled 時 grey 且無法跳)      │
│                                                     │
│ [PuzzlePiece] Browser        [●━━━]                 │
│   Web browser panes                                 │
│                                                     │
│ [PuzzlePiece] Memory Monitor [●━━━]                 │
│   System memory diagnostics                         │
└─────────────────────────────────────────────────────┘
```

資料流：
- 用 `getModules()` 取全部 module → filter `m.disableable === true`
- 每行狀態 `enabled = useModuleEnabledStore((s) => s.isEnabled(m.id))`
- Toggle handler 呼叫 `setEnabled(m.id, next)`
- "Open settings →" 條件：該 module 有 purdex scope contribution 才顯示（`listContributions('purdex').some((c) => c.moduleId === m.id)`）；點擊 → `setLocation('/settings/<first-purdex-localId>')`

Banner 文案 (i18n)：
- `settings.modules.reload_required.title`：`Changes require reload to take effect`
- `settings.modules.reload_required.hint`：`Disabled modules hide their settings entries; pane types and filesystem backends still register until a future release.`

### 4.5 `register-modules.tsx` 調整

- Revert `settings.section.modules` / `ModuleConfigSection` import 的刪除
- `registerSettingsSection({ id: 'module-config', label: 'settings.section.modules', order: 8, component: ModulesSwitchboardSection })` — id 保留 `module-config` 避免 URL breaking（`/settings/module-config`）
- 加 `disableable: true` + `descriptionKey` 到 4 個 module declaration
- `captureBaseline` 呼叫插在 `dispatchSettingsContributions(...)` 之前

未來可選：把 sidebar label 也從 "Modules" 微調成 "Modules & Extensions" 之類。本 PR 維持 "Modules" 即可。

### 4.6 Puzzle-piece icon（已實作）

- `spa/src/lib/settings-contribution-types.ts` export `isModuleOwnedContribution(c)` helper
- `SettingsSidebar.tsx`（purdex）/ `HostSidebar.tsx`（host）/ `WorkspaceSettingsPage.tsx`（workspace）三處在 row/section 標題尾（`gap-2` + `flex-shrink-0`）加 `<PuzzlePiece size={10-12} weight="fill" aria-hidden />`
- 狀態：已完成

## 5. 分相

**單一 commit / 單一 phase** — 總 diff 估計 ~400-600 行（含測試），所有 change 緊密耦合（store + dispatch + UI + baseline），硬拆反而增加不必要的 merge-state 複雜度。

實作順序（TDD）：
1. `useModuleEnabledStore` 寫測試 + 實作
2. `ModuleDefinition` 擴充欄位
3. `dispatchSettingsContributions` filter 測試 + 實作
4. `ModulesSwitchboardSection` 測試 + 實作
5. `register-modules.tsx` wire-up（revert removal + `captureBaseline` + 4 module flags）
6. i18n 恢復 + 新增
7. Puzzle-piece icon（已完成；lint 時確認即可）
8. 全套 `vitest run` + `pnpm run lint` + `pnpm run build` 綠

## 6. 測試計畫

### 6.1 `useModuleEnabledStore`

- Default `isEnabled('editor') === true` when `disableable: true` 且無 persist
- Default `isEnabled('sessions') === true` when `disableable: false`（永遠 true）
- `setEnabled('editor', false)` → `isEnabled('editor') === false`
- Persist：寫 localStorage、reload hydrate 後 `isEnabled` 回報持久值
- `captureBaseline` 只記錄一次、重複呼叫 no-op
- `hasPendingChanges` 在 enabled ≠ baseline 時為 true
- `resetAll` 清 enabled map（baseline 不動）

### 6.2 `dispatchSettingsContributions` filter

- Module `a` 宣告 `settings: [{localId: 'x', scope: 'purdex'}]`、`isEnabled('a') === false` → dispatch 後 `listContributions('purdex')` 不含 `a.x`
- Legacy adapter / host built-in contributions 不受 `useModuleEnabledStore` 影響（以 mock store 全 disable 驗證）
- Disabled module 的 `settings` 不參與 localId collision（避免 disabled module 的重複 localId 擋 enabled module）

### 6.3 `ModulesSwitchboardSection`

- 只列 `disableable: true` module（不列 core module）
- Toggle 呼叫 `setEnabled`
- "Open settings →" 僅在該 module 有 purdex scope contribution 時出現
- Banner 在 `hasPendingChanges() === true` 時 visible
- Banner 在無 diff 時 hidden

### 6.4 Integration

- Boot → captureBaseline → dispatch → switchboard 顯示全 enabled、no banner
- User toggle editor 到 disabled → banner 出現、sidebar 中 editor 的 purdex sections 仍在（要 reload 才消）
- Reload（simulate via `resetSettingsContributionsForHmr` + re-register + re-dispatch）→ banner 消、editor purdex sections 不見
- Puzzle-piece 在 module-owned row、不在 `_builtin.*` row

## 7. Open Questions

- **Q1**：Disabled module 的 switchboard row 本身要 disable 互動嗎？例如 description 是否仍可點？
  - 建議：row 內容全部仍可看，只有「Open settings →」在 disabled 時 grey 出且 unclickable（因為該 section 本身已經不在 sidebar，會 404）。
- **Q2**：`files` module 是 purdex 還是 workspace scope 為主？
  - 盤點：目前 `files` 只註冊 panes（FileTreeView / FileTreeSessionView），沒有 `settings: [...]`。Switchboard 仍列出（purely as on/off switch），但 "Open settings →" 不顯示。
- **Q3**：「Reload」動作是否需要內建一個按鈕？
  - 建議：不內建（Electron 有 Ctrl+R、web 用 F5）。Banner 只提示，避免 coupling。若使用體驗確有需要，後續加 `window.location.reload()` button。
- **Q4**：Disable 一個沒有任何 purdex contribution 的 module（例如 `files` / `browser`），banner 要不要顯示？
  - 建議：**顯示**。即便 v1 side-effect 只作用於 settings filter（對 `files` 視覺上無變化），使用者的意圖是 disable 整個 module，reload 是對未來 PR 3 pane-level 整合的預留 cue。語義一致勝過視覺精確。
- **Q5**：若 `disableable: true` module 改成 `disableable: false`（code 修改後），persist 過的 disabled entry 怎麼處理？
  - `isEnabled` 邏輯已經涵蓋：`disableable: false` → 永遠 true，忽略 persist value。Persist map 裡的 stale entry 不清理（無害）。
