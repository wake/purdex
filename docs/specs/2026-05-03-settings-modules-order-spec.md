# Settings Modules Sidebar Alignment Spec

- **Date**: 2026-05-03
- **Worktree**: `.claude/worktrees/settings-modules-order` (branch `worktree-settings-modules-order`)
- **Base**: `origin/main` @ `05c4790d` (alpha.291)
- **Scope**: 對齊 Settings 左側 sidebar 與 Modules Switchboard 兩處 module 排列順序；補齊「所有 disableable module 都要有 purdex-scope settings page」規則漏網的 Browser / Files；微調兩個 sidebar label 短名。

## 1. 背景

PR #604 (HSR alpha.213) → #623 (Editor restructure alpha.219) → #707 (Editor self-contained alpha.247) → #816/#825 (Settings architecture fix alpha.288) → #833 (Files disableable / SR-2 close alpha.290) 之後，Settings 三層註冊機制逐步完成；但 sidebar 的 module-owned 區段仍有兩處不一致：

1. **排序不對齊**：Settings sidebar 與 Modules Switchboard 是兩個不同的視覺面板，但呈現「同一群 module」。目前 sidebar 由 `SETTINGS_ORDER`（11/12/13/14 寫死）排序，Switchboard 由 `getModules()` 註冊順序（registration order）排序 — 兩邊看起來不像同一份清單。
2. **「所有 disableable module 都有 settings page」規則未落地**：本 session 確立的新規則 — 每個 disableable module 在 Settings sidebar (purdex scope) 都要有 entry，即使是空 placeholder。目前 Browser / Files 都無 purdex-scope contribution，在 Switchboard 看得到（Browser）或在 sidebar 完全看不到（Files purdex 部分），跟 Editor / Performance Monitor / Quick Commands 不一致。
3. **Sidebar label 過長**：`Performance Monitor` / `Quick Commands` 在窄 sidebar 顯得冗長，user 要求短名 `Monitor` / `Commands`。

### 1.1 推翻 SR-2 spec N3

`docs/specs/2026-05-03-files-disableable-sr2-spec.md` §N3 明寫「不加 Files purdex-scope placeholder / settings page」（理由：Files 設定是 workspace 範疇，purdex 沒東西可放）。本 spec **顯式推翻 N3**，理由：
- 新規則「所有 disableable module 都有 purdex settings entry」優先於「沒東西可放」的 dead-UX 顧慮
- Switchboard / sidebar mental model 一致性是 user-facing 強需求；user 從 Switchboard 看到 Files，再到 sidebar 找不到，會誤以為 module 漏掉
- placeholder 成本低（單一共用 component + 一句 i18n 文案），UX 一致性回饋大
- SR-2 spec N3 的「沒東西可放」改為由 placeholder 顯式陳述，而非用「不放」隱式表達

## 2. 範圍 + Non-goals

### 範圍（in scope）

- 為 Browser 與 Files 補 purdex-scope placeholder settings page（component + i18n + SETTINGS_ORDER 加 entry）
- SETTINGS_ORDER 重排為 sidebar-label 字母序（B/C/E/F/M/Sync），常數命名仍綁 module identity
- ModulesSwitchboardSection 排序改為依 module 第一個 purdex-scope settings contribution 的 `order` 升冪排（= 對齊 sidebar 順序）
- Sidebar i18n labelKey 切換：
  - memory-monitor: `performance_monitor.title` → `settings.section.monitor`
  - quick-commands: `settings.section.quick_commands` → `settings.section.commands`
- 新增 i18n keys（en + zh-TW）：`settings.section.browser`, `settings.section.files`, `settings.section.monitor`, `settings.section.commands`, 共用 placeholder 字串

### Non-goals（後續 PR / issue 處理）

- Switchboard row 顯示名稱改短（仍用 `module.name` 全名 "Performance Monitor" / "Quick Commands"，跟 sidebar 短名是 trade-off，見 §5）
- ModuleConfigSection 拆解（PR #833 follow-up，issue #835/#836 等追蹤；非本 PR 範圍）
- views / panes 的 disable hook（spec I4 限定 disable 只影響 settings dispatch，本 PR 不擴大）
- pane label 文案變動（`performance_monitor.title` 仍由 `pane-labels.ts:45` 與 `MemoryMonitorPage.tsx:284` 沿用，本 PR 不動）
- 其他既有 sidebar entry（Appearance / Terminal / Interface / Electron / Dev Environment / Tmux Agent Monitor）的 label 或 order
- Sync settings 加 disableable（Sync 故意 NOT disableable，spec 已說明）

## 3. 不變量

- **I1**：每個 `disableable: true` 的 module（檢查 `getModules()` 回傳的 definition，非 `listContributions('purdex')`，因為 disabled module 會在 dispatch 時被跳過）必須在 `module.settings` 宣告**至少一個** purdex-scope contribution（即使是空 placeholder）。違反者 test 應抓出（見 §6 T3）
- **I2**：Settings sidebar (purdex scope) 與 Modules Switchboard 對「`disableable === true` 的 module-owned contributions」的**相對順序**必須一致。Sync 只參與 sidebar order，不參與 Switchboard 比對（Sync 非 disableable）。Switchboard 排序 key = module 第一個 purdex-scope contribution 的 `order`；fallback 至 `Number.POSITIVE_INFINITY` + `module.name` localeCompare（v1 不該觸發 fallback，因 I1 保證有 purdex entry）
- **I3**：SETTINGS_ORDER 常數名稱綁 module identity（如 `MODULE_QUICK_COMMANDS`），常數值反映 sidebar 顯示位置。未來 sidebar label 變更不應牽動常數命名
- **I4**：sidebar label 短名 (`Monitor` / `Commands`) 不取代 `module.name` 全名 — Switchboard / pane label / inner page heading 仍用全名 (`Performance Monitor` / `Quick Commands`)。語意分工：sidebar = navigation 短名；其他 = 識別全名
- **I5**：placeholder settings page 是 view-only，不寫資料、不訂 store。內容固定為 i18n 文案 `settings.module.no_purdex_settings`（內容統一；view 視覺一致，跟 Browser / Files 一個 component 共用）。Component test 守「只渲染 i18n 文案、無 store subscription」靠 mock store assertion 達成（見 §6 T8）

### 3.1 排序基準說明

**「字母序」**指 **English / default sidebar short label** 的排序（穩定基準），不隨 runtime locale 動態排序：

| Module | English short label | Sort position |
|---|---|---|
| browser | Browser | 1 |
| quick-commands | Commands | 2 |
| editor | Editor | 3 |
| files | Files | 4 |
| memory-monitor | Monitor | 5 |
| sync | Sync | 6 |

→ SETTINGS_ORDER 數值（11–16）一次寫死於 source code，不會因為使用者切到 zh-TW（`瀏覽器/指令/編輯器/檔案/監控/同步`）而改變。zh-TW 字面字母序與 English 不同 — 這是預期行為，不是 bug。

## 4. 設計

### 4.1 SETTINGS_ORDER 重排

`spa/src/lib/settings-order.ts`：

```ts
// Module-owned (alphabetical by sidebar label):
//   Browser / Commands / Editor / Files / Monitor / Sync
// Constant name reflects module identity; value reflects display order.
MODULE_BROWSER: 11,
MODULE_QUICK_COMMANDS: 12,  // sidebar label: "Commands"
MODULE_EDITOR: 13,
MODULE_FILES: 14,
MODULE_PERFORMANCE_MONITOR: 15,  // sidebar label: "Monitor"
MODULE_SYNC: 16,
```

Tail built-in 不動：`DEV_ENVIRONMENT: 20`, `TMUX_AGENT_MONITOR: 21`。

### 4.2 Browser purdex placeholder

新建 `spa/src/components/settings/PlaceholderSettingsSection.tsx`（共用組件）：

```tsx
export function PlaceholderSettingsSection() {
  const t = useI18nStore((s) => s.t)
  return (
    <div className="space-y-4">
      <p className="text-sm text-text-secondary">
        {t('settings.module.no_purdex_settings')}
      </p>
    </div>
  )
}
```

`register-modules/index.tsx` Browser registerModule 加 `settings: [...]`：

```ts
registerModule({
  id: 'browser',
  name: 'Browser',
  disableable: true,
  descriptionKey: 'modules.browser.description',
  settings: [{
    localId: 'browser',
    scope: 'purdex',
    order: SETTINGS_ORDER.MODULE_BROWSER,
    labelKey: 'settings.section.browser',
    component: PlaceholderSettingsSection,
  }],
  panes: [{ kind: 'browser', component: BrowserPaneWrapper }],
})
```

### 4.3 Files purdex placeholder

`register-modules/index.tsx` Files registerModule 在現有 workspace settings 之外加一個 purdex entry：

```ts
settings: [
  {
    localId: 'workspace-files',
    scope: 'workspace',
    order: SETTINGS_ORDER.WORKSPACE_FILES,
    labelKey: 'settings.section.files_workspace',
    component: FilesWorkspaceSettingsSection,
  },
  {
    localId: 'files',
    scope: 'purdex',
    order: SETTINGS_ORDER.MODULE_FILES,
    labelKey: 'settings.section.files',
    component: PlaceholderSettingsSection,
  },
]
```

### 4.4 Switchboard 排序

`spa/src/components/settings/ModulesSwitchboardSection.tsx`：

```ts
const modules = getModules()
  .filter((m) => m.disableable === true)
  .map((m) => ({
    module: m,
    order: m.settings?.find((s) => s.scope === 'purdex')?.order
        ?? Number.POSITIVE_INFINITY,
  }))
  .sort((a, b) => {
    if (a.order !== b.order) return a.order - b.order
    return a.module.name.localeCompare(b.module.name)
  })
  .map(({ module }) => module)
```

註：fallback `localeCompare` 不期望被觸發（I1 保證），但留作 tie-break 防呆。

### 4.5 i18n labelKey 切換

`register-modules/index.tsx`：

```ts
// memory-monitor settings contribution
labelKey: 'settings.section.monitor',  // was 'performance_monitor.title'
order: SETTINGS_ORDER.MODULE_PERFORMANCE_MONITOR,  // value 由 13 → 15

// quick-commands settings contribution
labelKey: 'settings.section.commands',  // was 'settings.section.quick_commands'
order: SETTINGS_ORDER.MODULE_QUICK_COMMANDS,  // value 由 12 → 12（不變但 label 改）
```

`performance_monitor.title` 與 `settings.section.quick_commands` 兩個舊 key **保留不刪**：
- `performance_monitor.title`：`pane-labels.ts:45` + `MemoryMonitorPage.tsx:284` 仍用
- `settings.section.quick_commands`：`register-modules.quick-commands.test.tsx:26` 測試文字會更新；其他無 prod 引用 → 可選保留（最小變更原則：保留）

### 4.6 i18n 字串新增

| Key | en | zh-TW |
|---|---|---|
| `settings.section.browser` | Browser | 瀏覽器 |
| `settings.section.files` | Files | 檔案 |
| `settings.section.monitor` | Monitor | 監控 |
| `settings.section.commands` | Commands | 指令 |
| `settings.module.no_purdex_settings` | This module has no global settings. | 此模組沒有全域設定。 |

## 5. Trade-offs / 決定點

### TO-1：Switchboard 顯示名 vs sidebar 短名

| 選項 | 說明 | Pros | Cons |
|---|---|---|---|
| A（採用） | Switchboard 用 `module.name` 全名 | Switchboard 是「module 識別」context，全名更清楚；最小變更 | Switchboard 順序 (Browser → Quick Commands → Editor → Files → Performance Monitor) 不是字母排 by 顯示名 |
| B | Switchboard 也用短名（i18n） | 視覺完全字母排 | `module.name` 與 sidebar 同名，Switchboard "識別"功能弱化；要再加一層 i18n |

→ **採 A**。理由：sidebar 是 navigation 快速定位（短名為主），Switchboard 是設定全貌（識別為主）；兩個 context 不同，可以容忍視覺微差。User 已 ack。

### TO-2：order 衝突偵測

新加 Browser / Files 都用 `SETTINGS_ORDER.MODULE_*` 常數，不會有 hard-code 衝突。`dispatch-settings-contributions.ts` 既有 dedup logic 可抓 localId 重複，order 重複僅靠 PR review 規律抓。本 PR 不引入 runtime order-uniqueness assertion（過度工程，常數表本身就是 source of truth）。

### TO-3：placeholder 是否禁止 disable

placeholder 本身就是「無設定」的視覺確認；不需特殊禁止 — 即使 user disable Browser 後，sidebar 該 entry 會消失（disable filter 接管），placeholder 自然不再出現。一致行為，無需額外處理。

## 6. 驗證

### 自動測試

| 測試 | 檔案 | 描述 |
|---|---|---|
| T1 | `ModulesSwitchboardSection.test.tsx` | 三個 disableable module（不同 order）→ 渲染順序按 order 升冪 |
| T2 | `ModulesSwitchboardSection.test.tsx` | disableable module 無 purdex contribution → 排在最後（fallback 行為） |
| T2b | `ModulesSwitchboardSection.test.tsx`（整合） | Switchboard DOM row 順序 vs purdex contribution 中 module-owned + disableable 子集的 order 升冪一致 |
| T3 | `register-modules.test.ts` | I1 invariant：呼叫 `registerBuiltinModules()` 後，`getModules()` 中所有 `disableable === true` 的 module 都至少有一個 purdex-scope settings contribution（含 Browser / Files / Editor / memory-monitor / quick-commands） |
| T4 | `register-modules.test.ts` | Browser 有 purdex settings entry，labelKey = `settings.section.browser`，component 為 placeholder |
| T5 | `register-modules.test.ts` | Files 有 purdex settings entry，labelKey = `settings.section.files`，component 為 placeholder |
| T6 | `register-modules.test.ts` | memory-monitor labelKey = `settings.section.monitor`（取代既有 `performance_monitor.title` assertion at register-modules.test.ts:83） |
| T7 | `register-modules.quick-commands.test.tsx` | quick-commands labelKey = `settings.section.commands`（取代既有 `settings.section.quick_commands`） |
| T8 | `PlaceholderSettingsSection.test.tsx` | 渲染含 `settings.module.no_purdex_settings` 文字；mock store 後驗證無 store subscription（I5） |

### 手動驗證

- 進 `/settings`：sidebar 從上到下順序 = Appearance / Terminal / Interface / Electron / Modules / Browser / Commands / Editor / Files / Monitor / Sync / Dev Environment / Tmux Agent Monitor
- 點 Browser / Files：右側顯示 placeholder「This module has no global settings.」
- 進 Modules Switchboard：清單從上到下順序 = Browser / Quick Commands / Editor / Files / Performance Monitor（顯示全名，順序對齊 sidebar 相對位置）
- 切 zh-TW：sidebar 短名顯示 瀏覽器 / 指令 / 編輯器 / 檔案 / 監控 / 同步
- pane label / MemoryMonitorPage 標題仍顯示「Performance Monitor / 效能監控」（i18n 未動）

### 跑完整套

```
cd spa && npx vitest run && pnpm run lint && pnpm run build
```

## 7. 相關檔案

| 檔案 | 行數 | 用途 |
|---|---|---|
| `spa/src/lib/settings-order.ts` | 42–62 | SETTINGS_ORDER 表 |
| `spa/src/lib/register-modules/index.tsx` | 167–215 / 240–272 | Browser / memory-monitor / quick-commands / Files registerModule |
| `spa/src/components/settings/ModulesSwitchboardSection.tsx` | 33 | disableable filter / 排序點 |
| `spa/src/components/settings/SettingsSidebar.tsx` | 43–55 | sidebar 排序（已用 contribution.order，不需動） |
| `spa/src/lib/dispatch-settings-contributions.ts` | 155 | disable filter（不動） |
| `spa/src/locales/en.json` | 多處 | en i18n |
| `spa/src/locales/zh-TW.json` | 多處 | zh-TW i18n |
| `spa/src/lib/register-modules.test.ts` | 83 | memory-monitor labelKey assertion（要更新） |
| `spa/src/lib/register-modules.quick-commands.test.tsx` | 26 | quick-commands labelKey assertion（要更新） |

## 8. 開發流程

依 CLAUDE.md：
1. ✅ 進 worktree（已完成）
2. 寫 spec → 委派 codex review
3. 寫 plan → 委派 codex review
4. TDD 開發（subagent 跑）
5. PR + 兩輪 codex review（標準 + 三平行 adversarial）
6. 修 finding → ship → 獨立 bump PR

## 9. 預估工作量

- 純 UI / config / i18n 改動，無 runtime / store / async logic
- 新增 1 個 component (PlaceholderSettingsSection)
- 改 4 個 i18n key + 5 個新字串（10 處 — en/zh 各 5）
- 改 1 個排序 logic
- 改 4 個現有 test
- 新增 ~3 個 test
- 預估 1 個 PR、2-3 commits、~150 行 net diff
