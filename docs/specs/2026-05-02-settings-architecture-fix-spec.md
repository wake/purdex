# Settings 架構修正 Spec

- **Date**: 2026-05-02
- **Worktree**: `.claude/worktrees/settings-architecture-fix`（branch `worktree-settings-architecture-fix`）
- **Context**: alpha.284 後 Settings sidebar 累積成不一致狀態：（1）Modules switchboard（`module-config`）夾在 `link-detect (8)` 與 `editor (9)` 之間（同 order 8 衝突），語意上應是 modules 群組的標頭；（2）Editor module 把 Monaco 偏好 / Open Behavior / Link Detection 拆成三個獨立 sidebar entry，與「一個 module 對應一頁」的 mental model 不符；（3）Sync 功能自帶 engine + 7 個 contributors + store，自成一個邏輯單元，但仍以 built-in section 註冊，與其他 module（Quick Commands、Editor）的 sidebar 視覺 / 群組分類不一致；（4）puzzle icon 旋轉 30° + fill weight 視覺較雜，要轉正 + bold；（5）module-owned section 之間的 padding 寫法不一（`p-6 space-y-X` vs 無 padding）。

## 1. 背景

### 1.1 alpha.284 sidebar 現況（按實際 order 排序）

| order | id | label | source | 備註 |
|---|---|---|---|---|
| 0 | appearance | Appearance | built-in（`registerSettingsSection`） | |
| 1 | terminal | Terminal | built-in | |
| 2 | interface | Interface | built-in | 自帶 subsections |
| 5 | electron | Desktop App | built-in（條件） | `caps.canSystemTray` |
| 6 | performance-monitor | Performance Monitor | module-owned（`memory-monitor`）🧩 | |
| 7 | open-behavior | Open Behavior | module-owned（`editor`）🧩 | Editor 子設定 |
| 8 | link-detect | Link Detection | module-owned（`editor`）🧩 | Editor 子設定 ⚠️ 同 8 衝突 |
| 8 | module-config | Modules | built-in | switchboard ⚠️ 同 8 衝突 |
| 9 | editor | Editor | module-owned（`editor`）🧩 | Monaco 偏好 |
| 10 | quick-commands | Quick Commands | module-owned（`quick-commands`）🧩 | |
| 11 | sync | Sync | built-in | 功能上自成 module 但未升格 |
| 20 | dev-environment | Development | built-in（dev only） | |
| 21 | tmux-agent-monitor | Tmux Agent Monitor | built-in（dev/devUpdate） | |

四個結構性問題：

1. **群組無視覺分隔**：modules 群組（`memory-monitor` / `editor` 三頁 / `quick-commands`）跟非 module section 交錯排列；Modules switchboard 應落在 modules 群組標頭位置
2. **Editor 一拆三**：`open-behavior` (7) + `link-detect` (8) + `editor` (9) 是同一 module 的設定，但成三個 sidebar entry。使用者 mental model 是「找 Editor 設定 → 點 Editor」，不該分散
3. **Sync 功能 vs 結構不一致**：Sync 自帶 engine、7 contributors、settings UI、`use-sync-store`，與 Quick Commands / Editor 性質相同，但卻是 built-in section
4. **Padding/style drift**：`AppearanceSection` 用 `<div>` + `<h2>` + `<SettingItem>`；`QuickCommandsSettingsSection` 用 `<div className="p-6 space-y-4">`；`ModulesSwitchboardSection` 用 `<div className="p-6 space-y-6">`。`GlobalSettingsPage` 的 outer container 已經 `p-6`，這幾個會 double-pad

### 1.2 Puzzle icon 現況

`SettingsSidebar.tsx:88-93`:

```tsx
<PuzzlePiece
  size={12}
  weight="fill"
  className="flex-shrink-0 rotate-[30deg] text-text-muted"
  aria-hidden
/>
```

需要：`rotate-0`（轉正）+ `weight="bold"`（線框風）。Size 維持 12。

## 2. 範圍 + Non-goals

### 2.1 範圍（in scope）

**PR-1（視覺 / 順序）**

- Puzzle icon：`rotate-[30deg]` → 移除、`weight="fill"` → `weight="bold"`
- Sidebar 順序重排（見 §4.1）— Modules switchboard 移到所有 module-owned 上方
- Module-owned section 統一移除 `p-6 space-y-X` outer wrapper（依賴 `GlobalSettingsPage` 的 `p-6`）
- 不動內部 logic、不動 i18n key、不動 URL routing

**PR-2（Editor 收編 + Sync modularize + 頁首統一）**

- Editor 三 entry 收編成一個 `editor` page，內部以多段（Monaco 偏好 / Open Behavior / Link Detection）排列
- 移除 `link-detect` (order 8) + `open-behavior` (order 7) sidebar entry（從 `editorModuleDefinition.settings` 拿掉）
- URL alias：`/settings/link-detect` + `/settings/open-behavior` → `/settings/editor`（沿用 `editor-buffers` → `editor` 模式）
- Sync 升格為 module（`registerModule({ id: 'sync', name: 'Sync', settings: [{ scope: 'purdex', ... }] })`，**不**加 `disableable: true`），sidebar 自動取得 puzzle icon、自然落在 modules 群組
- 移除 `registerSettingsSection({ id: 'sync', ... })` 的呼叫（搬到 module def）
- Quick Commands 頁首樣式統一（`<h2>` + `<p>` + 拿掉 `p-6 space-y-4`），CRUD list 內容不動
- i18n：補 `settings.editor.{open_behavior,link_detect}.title` → 改成同頁內段落 heading 用，URL 路由 label 不再使用

### 2.2 Non-goals（本 PR 不做、可開 issue）

- Performance Monitor 從 settings 拆出（使用者明確指示「先放，後面要會再拆出來」）
- Sync 加 `disableable: true` 與對應的 engine / contributors 停用設計（structural module 升格後，未來題）
- Switchboard 加分隔線 / 視覺群組標題（純 sidebar 視覺，未要求）
- 其他非 module section（Appearance / Terminal / Interface / Sync 內部 / Electron / Dev / Tmux Agent Monitor）的樣式 refactor
- Subsection（Interface 的 new-tab / pane / sidebar）結構不動
- Editor 的 workspace-scope / host-scope contributions 不動
- 重命名 sidebar id（避免 URL 大規模 break）

## 3. 不變量

- **I1**：Sidebar 視覺順序：`built-in core (Appearance / Terminal / Interface / Electron) → Modules（switchboard）→ module-owned (Editor / Quick Commands / Performance Monitor / Sync) → built-in tail (Dev Environment / Tmux Agent Monitor)`
- **I2**：Module-owned 判定（`isModuleOwnedContribution`）不變；Sync 升格後自動歸類為 module-owned，自動取得 puzzle icon
- **I3**：URL stability — 任何在 alpha.284 之前可達的 `/settings/<id>` URL，PR-2 後須仍可達或被 alias 到語意對等的新 URL；不可 hard 404
  - `/settings/link-detect` → 302/replace 到 `/settings/editor`
  - `/settings/open-behavior` → 302/replace 到 `/settings/editor`
  - `/settings/editor-buffers` → `/settings/editor`（既有 alias，不動）
  - `/settings/sync` → 仍正常解析（id 不變，只是改由 module 註冊）
- **I4**：Editor 收編後 spa/src/components/settings/editor/ 下的兩個現有 component（`EditorLinkDetectionSection`、`EditorOpenBehaviorSection`）以**段落（subsection block）**形式繼續存在，不重寫成 inline；只是 import 進 `EditorPurdexSettingsSection` 重組
- **I5**：Sync 升格為 module 不改變 `syncEngine` 的生命週期 — `registerSyncContributors()` 維持目前 boot-time 呼叫；module def 只是設定 entry 的 source 改變
- **I6**：Padding 一致性 — 所有 module-owned section 的 outer wrapper **不再**自帶 `p-6`，依賴 `GlobalSettingsPage` 的 `<div className="flex-1 overflow-y-auto p-6">`
- **I7**：i18n key 不刪 —`settings.editor.link_detect.title` / `settings.editor.open_behavior.title` 仍會被 EditorPurdexSettingsSection 用作段落 heading；`settings.section.editor`、`settings.section.modules`、`settings.section.sync`、`settings.section.quick_commands` 維持 sidebar label
- **I8**：Tests — 既有 settings section 測試（`AppearanceSection.test.tsx`、`TerminalSection.test.tsx`、`ModulesSwitchboardSection.test.tsx`、`InterfaceSection.test.tsx`、`SettingsSidebar.test.tsx` 等）只允許因 padding/icon 變動而調整 selector / class 斷言，不允許因順序 break 而被 skip 或刪除
- **I9**：Switchboard 的 `disableable: true` 過濾邏輯不動 — Sync 雖是 module，因 `disableable !== true`，**不**出現在 switchboard list；視覺上 module-owned 並非 disableable 的等價

## 4. 設計

### 4.1 Sidebar 順序

設定新的 order 區段（保留 0-29 的數字空間）：

| 區段 | 範圍 | 用途 |
|---|---|---|
| Top built-in | 0 – 4 | Appearance / Terminal / Interface |
| Top conditional built-in | 5 – 9 | Electron |
| Modules switchboard | 10 | `module-config` |
| Module-owned | 11 – 19 | Editor / Quick Commands / Performance Monitor / Sync |
| Tail built-in | 20 – 29 | Sync 升格後保留：Dev Environment / Tmux Agent Monitor |

新 order 表：

| id | order | source | label |
|---|---|---|---|
| appearance | 0 | built-in | settings.section.appearance |
| terminal | 1 | built-in | settings.section.terminal |
| interface | 2 | built-in | settings.section.interface |
| electron | 5 | built-in（條件） | settings.section.electron |
| module-config | 10 | built-in | settings.section.modules |
| editor | 11 | module-owned | settings.section.editor |
| quick-commands | 12 | module-owned | settings.section.quick_commands |
| performance-monitor | 13 | module-owned | performance_monitor.title |
| sync | 14 | module-owned（升格） | settings.section.sync |
| dev-environment | 20 | built-in（dev） | settings.section.dev_environment |
| tmux-agent-monitor | 21 | built-in（dev） | settings.section.tmux_agent_monitor |

說明：
- module-owned 區段內部排序按 alpha：Editor (11) → Quick Commands (12) → Performance Monitor (13) → Sync (14)；任何 listing 沿用 `order ASC`，sidebar 與 switchboard 一致
- `link-detect`、`open-behavior` 兩個 entry 不再出現在 sidebar（從 `editorModuleDefinition.settings` 移除）

### 4.2 Editor 三頁合一

#### 4.2.1 `EditorPurdexSettingsSection` 重組

新結構（單一 page，三段落）：

```tsx
<div>
  <h2>{t('settings.section.editor')}</h2>          // sidebar 同名
  <p>{t('settings.editor.desc')}</p>

  {/* Section 1：Monaco 偏好 — 既有 SettingItem 序列 */}
  <SettingItem ... />  // tab_size / insert_spaces / word_wrap / line_numbers / minimap / font_size

  {/* Section 2：Open Behavior */}
  <h3>{t('settings.editor.open_behavior.title')}</h3>
  <EditorOpenBehaviorSection />

  {/* Section 3：Link Detection */}
  <h3>{t('settings.editor.link_detect.title')}</h3>
  <EditorLinkDetectionSection />

  <p>{t('settings.editor.tiptap_note')}</p>
</div>
```

H3 標題樣式：`text-base text-text-primary mt-8 mb-2`（沿用 Appearance / Terminal 內部分隔的視覺節奏）。`<EditorOpenBehaviorSection />` 與 `<EditorLinkDetectionSection />` 內部 outer `<div>` / `<h2>` 由各自 component 拿掉（如有），確保段落 heading 由父頁負責。

#### 4.2.2 `editorModuleDefinition.settings` 縮減

```ts
settings: [
  { localId: 'editor', scope: 'purdex', order: 11, ... },              // 唯一 purdex-scope entry
  { localId: 'workspace-home-path', scope: 'workspace', order: 0, ... }, // 不動
  { localId: 'host-home-path',      scope: 'host',      order: 100, ... },// 不動
]
```

移除：`{ localId: 'link-detect', ... }`、`{ localId: 'open-behavior', ... }`。

#### 4.2.3 URL alias

`SettingsPage.tsx` 既有 alias 邏輯（`editor-buffers` → `editor`）擴充為 map：

```ts
const URL_ALIASES: Record<string, string> = {
  'editor-buffers': 'editor',
  'link-detect': 'editor',
  'open-behavior': 'editor',
}
const urlSection = rawUrlSection ? URL_ALIASES[rawUrlSection] ?? rawUrlSection : null
```

Self-heal 既有的 `setLocation(..., { replace: true })` 路徑會自動把 URL 矯正成 `/settings/editor`。

### 4.3 Sync 升格為 module

#### 4.3.1 移除 built-in 註冊

`spa/src/lib/register-modules/index.tsx`：

```diff
-  registerSettingsSection({ id: 'sync', label: 'settings.section.sync', order: 11, component: SyncSection })
```

#### 4.3.2 新增 module def

```ts
registerModule({
  id: 'sync',
  name: 'Sync',
  // disableable: 不設定 — structural module，將來再加 disable 時另開 issue
  settings: [
    {
      localId: 'sync',
      scope: 'purdex',
      order: 14,
      labelKey: 'settings.section.sync',
      component: SyncSection,
    },
  ],
})
```

`registerSyncContributors()` 維持目前 boot-time 呼叫，不挪到 module def 內。

#### 4.3.3 SyncSection 內部不動

`SyncSection.tsx` 既有實作維持，只在 PR-1 時跟其他 section 一起拿掉 `p-6` outer padding（如有）。

### 4.4 Quick Commands 頁首統一

`QuickCommandsSettingsSection.tsx`：

```diff
- <div className="p-6 space-y-4">
-   <div className="flex items-center justify-between">
-     <h2 className="text-lg text-text-primary">{t('settings.quick_commands.title')}</h2>
+ <div>
+   <h2 className="text-lg text-text-primary">{t('settings.quick_commands.title')}</h2>
+   <p className="text-xs text-text-secondary mb-6">{t('settings.quick_commands.desc')}</p>
+   <div className="flex items-center justify-end mb-3">
      <button ...>{t('settings.quick_commands.new')}</button>
    </div>
    {/* commands list 維持 */}
```

新增 i18n key `settings.quick_commands.desc`（短說明，跟 Appearance/Terminal 一致）。

### 4.5 Padding 統一

`GlobalSettingsPage` 的 `<div className="flex-1 overflow-y-auto p-6">` 已負責整體 padding。對所有 settings section component 立規則：

- Outer wrapper 一律 `<div>`（無 `p-X`、無 `space-y-X`）
- 標題：`<h2 className="text-lg text-text-primary">...`
- 描述：`<p className="text-xs text-text-secondary mb-6">...`
- 內容：`<SettingItem>` rows 或自訂結構

需修正的檔案（已驗證實際 outer wrapper）：

| 檔案 | 現況 | 改動 |
|---|---|---|
| `AppearanceSection.tsx` | `<div>` | 不動 |
| `TerminalSection.tsx` | `<div>` | 不動 |
| `EditorPurdexSettingsSection.tsx` | `<div>` | PR-2 重組為三段 |
| `SyncSection.tsx` | `<div>` | 不動 |
| `InterfaceSection.tsx` | `<div className="flex h-full">`（自帶 sidebar 結構） | 不動 |
| `ModulesSwitchboardSection.tsx` | `<div className="p-6 space-y-6">` | PR-1 改為 `<div>`（拿掉 `p-6`，內部已用 `space-y-6` 元素，視需要保留） |
| `QuickCommandsSettingsSection.tsx` | `<div className="p-6 space-y-4">` | PR-2 改為 `<div>` + 統一頁首 |
| `ElectronSection.tsx` | `<div className="space-y-6">` | 不動（無 `p-X` 不雙 pad） |
| `DevEnvironmentSection.tsx` | `<div className="space-y-6">` | 不動 |
| `TmuxAgentMonitorSection.tsx` | `<div className="space-y-6">` | 不動 |

僅 `ModulesSwitchboardSection` 與 `QuickCommandsSettingsSection` 因含 `p-6` 與 `GlobalSettingsPage` 的 `p-6` 雙 pad，需修。其餘只用 `space-y-6` 不雙 pad，視覺差異是「元素間距不一致」而非「邊距重複」，超出本 PR 範圍。

### 4.6 Puzzle icon

`SettingsSidebar.tsx`：

```diff
   <PuzzlePiece
     size={12}
-    weight="fill"
-    className="flex-shrink-0 rotate-[30deg] text-text-muted"
+    weight="bold"
+    className="flex-shrink-0 text-text-muted"
     aria-hidden
   />
```

範圍（已 grep 確認三個 caller，全部一致改）：

- `spa/src/components/settings/SettingsSidebar.tsx:88-93`（size 12）
- `spa/src/features/workspace/components/WorkspaceSettingsPage.tsx:162-167`（size 12）
- `spa/src/components/hosts/HostSidebar.tsx:124-129`（size 10）

三處都有 `weight="fill"` + `rotate-[30deg]`，PR-1 一次改完。Size 維持各自原值（10 / 12）不統一改。

## 5. PR 切分

### 5.1 PR-1：視覺 / 順序

範圍（純視覺、無行為改變）：

1. Puzzle icon：rotate-0 + weight=bold（含 spa 內所有 `<PuzzlePiece>` 用法）
2. Sidebar order 重排：electron 5（不動）、module-config 10、editor 11、quick-commands 12、performance-monitor 13、dev-environment 20（不動）、tmux-agent-monitor 21（不動）。Sync 暫留 11（PR-2 才升格 + 改 14）
3. Padding 一律拿掉 `p-X` outer wrapper：`ModulesSwitchboardSection` / 其他帶 padding 的 module section
4. **不**碰 Editor 三頁、不碰 Sync 註冊方式、不碰 Quick Commands 頁首

Acceptance：
- A1：Sidebar visual order = §4.1 表（Sync 仍在 11）
- A2：SettingsSidebar / WorkspaceSettingsPage / HostSidebar 三處 puzzle icon 皆為直立（無 `rotate`）+ bold weight
- A3：所有 sidebar URL（既有 + new）正常解析
- A4：Settings 內容不雙 pad（screenshot 比較或 test 斷言 outer 無 `p-6`）
- A5：既有 vitest 全綠（含 SettingsSidebar / ModulesSwitchboardSection / 各 section 測試）

### 5.2 PR-2：Editor 收編 + Sync modularize + Quick Commands 頁首

範圍：

1. `EditorPurdexSettingsSection` 重組為單頁三段
2. `editorModuleDefinition.settings` 移除 `link-detect` / `open-behavior` entry
3. `SettingsPage` URL alias map 擴充
4. Sync `registerSettingsSection` → `registerModule`
5. Quick Commands 頁首改 `<h2>` + `<p>`
6. 新增 i18n：`settings.quick_commands.desc`

Acceptance：
- A6：`/settings` sidebar 只剩一個 Editor entry（`Open Behavior`、`Link Detection` 不在 sidebar）
- A7：Editor 頁內可看到三段：Monaco 偏好 / Open Behavior / Link Detection；功能行為與舊頁等價（既有測試 pass）
- A8：`/settings/link-detect` + `/settings/open-behavior` 開啟後 URL 自動 replace 為 `/settings/editor` 並 mount Editor 頁
- A9：`/settings/sync` 開啟後仍 mount SyncSection；sidebar 上 Sync row 顯示 puzzle icon
- A10：Sync 在 ModulesSwitchboard 中**不**出現（disableable !== true）
- A11：Quick Commands 頁首結構與 Appearance / Terminal 視覺一致（h2 + p + 主要內容）
- A12：所有 vitest 全綠（含新增 alias / module-owned 判定測試）

## 6. 風險 + 對策

| Risk | Severity | 對策 |
|---|---|---|
| Editor 收編破壞既有 OpenBehavior / LinkDetection 內部行為 | Medium | TDD：先抽 OpenBehavior / LinkDetection 段落 component（不重寫），父頁 import；既有測試 pass = 行為不變 |
| URL alias map 邏輯擴充誤判 — 例如 `link-detect` 內含 `editor-buffers` 結構衝突 | Low | alias map 是 plain object lookup，無 prefix match；新增 unit test 覆蓋三個 alias |
| Sync 升格後 `dispatchSettingsContributions` 過濾路徑改變（從 built-in → module-owned） | Medium | Sync `disableable !== true`，過濾邏輯（`disabled module 跳過`）對它無作用；新增 test 確認 Sync contribution 在 dispatch 後仍存在 |
| Sidebar order 衝撞 — 既有 order 8/8 collision 同時也是 alpha.284 在跑的狀態，重新編號可能讓使用者感覺位置跳動 | Low | order 改變屬於本次重排目的，CHANGELOG 記錄 |
| Puzzle icon 改動同時影響 HostPage / Workspace settings | Low | grep `PuzzlePiece` 全 spa 確認所有 caller 一致 |
| Quick Commands 頁首改動破壞既有 dialog 測試 | Low | 改動限於 outer wrapper + h2/p 新增；list 與 dialog 內部不動；既有 vitest 應 pass |

## 7. 測試策略

### 7.1 既有需要 pass

- `SettingsSidebar.test.tsx` — order 改變後 mock contribution 順序需更新
- `ModulesSwitchboardSection.test.tsx` — Sync 升格後**不**進 switchboard 列表（新增斷言 + 既有不破）
- `AppearanceSection.test.tsx` / `TerminalSection.test.tsx` — 不動
- `InterfaceSection.test.tsx` — 不動
- `EditorPurdexSettingsSection`（如有測試）— 重組為三段後測試需要更新

### 7.2 新增

- **PR-1**
  - SettingsSidebar：puzzle icon className 不含 `rotate-[30deg]`；weight prop = `bold`
  - SettingsSidebar：order 排序在 mock fixture 下符合 §4.1
- **PR-2**
  - `SettingsPage` URL alias：`/settings/link-detect` 跟 `/settings/open-behavior` 都 mount Editor section + URL 被 replace 為 `/settings/editor`
  - Editor 頁：三段 heading 都 render（Monaco 偏好 / Open Behavior / Link Detection）
  - Sync：在 contribution registry 中 `moduleId === 'sync'` 而非 `_builtin.legacy-section`（`isModuleOwnedContribution` 為 true）
  - Sync：不在 ModulesSwitchboard 列表（`getModules().filter(disableable === true)` 不含 `sync`）

### 7.3 手動驗證（mlab + Air）

- `/settings` 進去 sidebar 視覺順序與 §4.1 一致
- 所有 module-owned row 顯示直立 puzzle icon
- 點 Editor → 三段都展示，Monaco 偏好可改、Open Behavior toggle 可動、Link Detection toggle 可動
- 直接訪問 `/settings/link-detect` 跟 `/settings/open-behavior` URL，自動跳到 `/settings/editor`
- Sync 在 sidebar 顯示 puzzle icon、點進去 SyncSection 正常運作、Modules switchboard 不列 Sync
- Quick Commands 頁首是 h2 + 描述 p（跟 Appearance 視覺一致），下方仍是 commands list
- Performance Monitor 不動（暫不重組）

## 8. Future work（開 issue 追蹤，本 PR 不做）

- Performance Monitor 從 settings 拆出（使用者明確指示，未來題）
- Sync 加 `disableable: true` + engine / contributors disable wiring
- Switchboard 加分隔線 / group title 視覺
- Performance Monitor / Sync / 其他 module-owned section 內部樣式統一
