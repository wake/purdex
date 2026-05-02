# Plan — Settings 架構修正

**Spec**: [2026-05-02-settings-architecture-fix-spec.md](2026-05-02-settings-architecture-fix-spec.md)
**Approach**: TDD, two-PR sequential. PR-1 純視覺/順序，PR-2 結構性收編 + Sync 升格。

## 共用前置

- 主環境跑 `pnpm install`（codex sandbox 無法跑）
- 每個 task 完成後 `cd spa && npx vitest run <pattern>` 跑相關 + `pnpm run lint`
- Test fixture 慣例：
  - `register-modules` 相關測試 import `clearAllForHmr` / `resetSettingsContributionsForHmr` 在 `beforeEach` 重置 registry
  - 既有 `register-modules.test.ts` / `register-modules.quick-commands.test.tsx` 是 task 1.x / 2.x 的主要 anchor
  - URL alias 測試在 `SettingsPage.test.tsx`（如不存在則新建）使用 wouter `Router` + `memoryLocation`

## PR-1：視覺 / 順序（純視覺，無結構變更）

### Task 1.1 — 新增 `spa/src/lib/settings-order.ts`

新檔案，集中常數 + JSDoc 說明 band 切分（§4.1.1 / §4.1.2）：

```ts
export const SETTINGS_ORDER = {
  APPEARANCE: 0,
  TERMINAL: 1,
  INTERFACE: 2,
  ELECTRON: 5,
  MODULE_CONFIG: 10,
  MODULE_EDITOR: 11,
  MODULE_QUICK_COMMANDS: 12,
  MODULE_PERFORMANCE_MONITOR: 13,
  MODULE_SYNC: 14,
  DEV_ENVIRONMENT: 20,
  TMUX_AGENT_MONITOR: 21,
} as const
```

**Tests**: 不需要單獨測試（純常數）。Linter / type check pass 即可。

### Task 1.2 — 寫 sidebar order 測試（先紅）

新增 `spa/src/lib/__tests__/settings-order-pr1.test.ts`：

```ts
// 1.2.a — listContributions('purdex') + listSettingsSections() 合併後 ASC：
//   appearance(0), terminal(1), interface(2), [electron(5)?],
//   module-config(10), performance-monitor(11), open-behavior(12),
//   link-detect(13), editor(14), quick-commands(15), sync(16),
//   [dev-environment(20)?], [tmux-agent-monitor(21)?]
//   ⚠️ 用 listContributions + registerSettingsSection 兩個 source 合併斷言
//
// 1.2.b — module-config 必須出現在所有 module-owned (puzzle) 上方:
//          rows.findIndex(id='module-config') < rows.findIndex(any module-owned)
//
// 1.2.c — 無重複 order：每個 entry 的 order 在 active 集合中唯一
```

**Expected**: 先紅（current code module-config 還是 8、perf-monitor 6 等）。

### Task 1.3 — 改 register-modules order（讓 1.2 紅 → 綠）

`spa/src/lib/register-modules/index.tsx`：

```diff
- order: 6,                              // memory-monitor settings
+ order: 11,
- registerSettingsSection({ id: 'module-config', ..., order: 8, ... })
+ registerSettingsSection({ id: 'module-config', ..., order: SETTINGS_ORDER.MODULE_CONFIG, ... })
- registerSettingsSection({ id: 'sync', ..., order: 11, ... })
+ registerSettingsSection({ id: 'sync', ..., order: 16, ... })  // PR-1 過渡
- registerSettingsSection({ id: 'appearance', ..., order: 0, ... })
+ registerSettingsSection({ id: 'appearance', ..., order: SETTINGS_ORDER.APPEARANCE, ... })
- registerSettingsSection({ id: 'terminal', ..., order: 1, ... })
+ registerSettingsSection({ id: 'terminal', ..., order: SETTINGS_ORDER.TERMINAL, ... })
- registerSettingsSection({ id: 'interface', ..., order: 2, ... })
+ registerSettingsSection({ id: 'interface', ..., order: SETTINGS_ORDER.INTERFACE, ... })
- registerSettingsSection({ id: 'electron', ..., order: 5, ... })
+ registerSettingsSection({ id: 'electron', ..., order: SETTINGS_ORDER.ELECTRON, ... })
- registerSettingsSection({ id: 'dev-environment', ..., order: 20, ... })
+ registerSettingsSection({ id: 'dev-environment', ..., order: SETTINGS_ORDER.DEV_ENVIRONMENT, ... })
- registerSettingsSection({ id: 'tmux-agent-monitor', ..., order: 21, ... })
+ registerSettingsSection({ id: 'tmux-agent-monitor', ..., order: SETTINGS_ORDER.TMUX_AGENT_MONITOR, ... })
```

`registerModule` 內 quick-commands `order: 10` → `15`。

`spa/src/lib/register-modules/editor-module.tsx`：

```diff
  settings: [
-   { localId: 'editor', scope: 'purdex', order: 9, ... },
+   { localId: 'editor', scope: 'purdex', order: 14, ... },        // PR-1 過渡
-   { localId: 'link-detect', scope: 'purdex', order: 8, ... },
+   { localId: 'link-detect', scope: 'purdex', order: 13, ... },
-   { localId: 'open-behavior', scope: 'purdex', order: 7, ... },
+   { localId: 'open-behavior', scope: 'purdex', order: 12, ... },
    { localId: 'workspace-home-path', scope: 'workspace', order: 0, ... },
    { localId: 'host-home-path', scope: 'host', order: 100, ... },
  ]
```

⚠️ PR-1 的 SETTINGS_ORDER 常數對 module-owned 對應 PR-2 final（11/12/13/14），PR-1 程式碼用 hard-coded 11-16；只有 module-config / 非 module-owned 用常數。Plan acceptance 守住「PR-1 結束時 ASC 排序與 §4.1.4 表完全一致」。

**Run**: `cd spa && npx vitest run lib/__tests__/settings-order-pr1` → 綠。

### Task 1.4 — 寫 puzzle icon 測試（先紅）

新增 `spa/src/components/settings/SettingsSidebar.test.tsx`（如已存在則 append）。最穩的測法是 mock `@phosphor-icons/react` 攔截 props：

```tsx
// vi.mock('@phosphor-icons/react', () => ({
//   PuzzlePiece: ({ weight, className, ...rest }) => (
//     <i data-testid="puzzle" data-weight={weight} className={className} {...rest} />
//   ),
//   /* ...其他在這個檔案會渲染的 icon 也要 mock 成 minimal stub... */
// }))
//
// 1.4.a — render SettingsSidebar 並確認 module-owned row 的 puzzle:
//          dataset.weight === 'bold'
//          className 不含 'rotate-[30deg]'
// 1.4.b — render SettingsSidebar 並確認 built-in row 沒有 [data-testid="puzzle"]
```

WorkspaceSettingsPage / HostSidebar 採同樣 mock 法在它們既有的 test 檔（如有）追加同樣斷言；如測試檔不存在不新建（避免 PR-1 範圍膨脹）。

### Task 1.5 — 改 puzzle icon 三 caller（讓 1.4 紅 → 綠）

三個檔案，改法相同：

```diff
  <PuzzlePiece
-   size={12}                                         // hosts 是 size={10}
-   weight="fill"
-   className="flex-shrink-0 rotate-[30deg] text-text-muted"
+   size={12}
+   weight="bold"
+   className="flex-shrink-0 text-text-muted"
    aria-hidden
  />
```

檔案：
1. `spa/src/components/settings/SettingsSidebar.tsx:88-93`
2. `spa/src/features/workspace/components/WorkspaceSettingsPage.tsx:162-167`
3. `spa/src/components/hosts/HostSidebar.tsx:124-129`（size={10} 不動）

如 WorkspaceSettingsPage / HostSidebar 既有 test 在 1.4 已加斷言，三個檔案要在同一 commit 改完才會全綠。

### Task 1.6 — `ModulesSwitchboardSection` 拿掉 `p-6`

`spa/src/components/settings/ModulesSwitchboardSection.tsx:36`：

```diff
- <div className="p-6 space-y-6">
+ <div className="space-y-6">
```

`ModulesSwitchboardSection.test.tsx` 既有測試應通過（如果有 className 斷言調整 selector）。

### Task 1.7 — 跑全部 vitest + lint

```bash
cd spa && npx vitest run && pnpm run lint
```

預期：所有測試綠 + lint 0。

### PR-1 Acceptance（spec §5.1）

- [ ] A1：vitest `settings-order-pr1` 全綠 → sidebar order = §4.1.4 表
- [ ] A2：三個 PuzzlePiece caller 都改完（grep `rotate-\[30deg\]` 全 spa = 0 hit）
- [ ] A3：URL routing 不變（既有 settings paths 全部仍解析）
- [ ] A4：ModulesSwitchboard outer wrapper 無 `p-6`
- [ ] A5：vitest 全綠

### PR-1 Commits（每個 commit 都可獨立通過 CI）

每個 commit 內先寫紅測試（local 確認失敗），再加實作讓綠 — 同 commit 提交以維持 CI 綠。

1. `feat(settings): introduce SETTINGS_ORDER constants` (Task 1.1，純常數新增，無測試)
2. `refactor(settings): reorder sidebar to PR-1 transitional table + add order assertions` (Task 1.2 + 1.3 同 commit：紅測試 + register-modules order 改動同時提交)
3. `style(settings): puzzle icon bold + no rotation, with three-caller assertions` (Task 1.4 + 1.5 同 commit：mock-prop 紅測試 + 三 caller icon 改動同時提交)
4. `refactor(settings): drop double p-6 from ModulesSwitchboardSection` (Task 1.6)

---

## PR-2：Editor 收編 + Sync modularize + Quick Commands 頁首

⚠️ PR-2 必須建立在 PR-1 已 merged main 的 base 上。

### Task 2.1 — 寫 Editor 收編測試（先紅）

新增 `spa/src/components/settings/__tests__/editor-section-consolidation.test.tsx`：

```tsx
// 2.1.a — listContributions('purdex') 不含 'open-behavior' / 'link-detect' localId
// 2.1.b — editor section render 後同時包含三段 heading：
//   - <h2> 文本含 t('settings.section.editor')
//   - 第一段 SettingItem rows（Monaco prefs, tab_size 等）
//   - <h3> 含 t('settings.editor.open_behavior.title')
//   - <h3> 含 t('settings.editor.link_detect.title')
// 2.1.c — Open Behavior toggle (popupOnMissingFile) 改變 useEditorSettingsStore state
// 2.1.d — Link Detection toggle (linkDetectAbsolute) 改變 useUISettingsStore state
//   （用 within(<screen text=link_detect.title 的 section>) 鎖定範圍避免誤點 sidebar）
// 2.1.e — 父頁無重複 h3（h3 數 = 2）
// 2.1.f — Editor 段落內無多餘 outer p-6（render 後 outer div 的 className 不含 'p-6'）
// 2.1.g — workspace / host scope 不回歸：
//   editorModuleDefinition.settings 仍含 { localId: 'workspace-home-path', scope: 'workspace' }
//                                      + { localId: 'host-home-path', scope: 'host' }
//   purdex scope 只剩 { localId: 'editor' }
```

### Task 2.2 — 寫 URL alias 測試（先紅）

新增 `spa/src/components/SettingsPage.test.tsx`（如已存在則 append）。每個 case `beforeEach` 必須：
- `resetLastSection()`（avoid SettingsPage module-level `lastSection` 跨測試污染）
- 以 `memoryLocation`（wouter `wouter/memory-location`）注入 wouter Router，初始 path 設為該 case 的起點
- 用 isolated registry：beforeEach 跑 `resetSettingsContributionsForHmr()` + 重新 register 一份穩定 fixture：
  - `appearance` 用 legacy `registerSettingsSection({ id: 'appearance', order: 0, ... })` — 永遠 selectable，當 firstSelectable fallback
  - `editor` **必須**用 `registerModule({ id: 'editor', disableable: true, settings: [{ localId: 'editor', scope: 'purdex', order: 11, ... }] })` 註冊；否則 2.2.e 的 `useModuleEnabledStore` mock 對 legacy 註冊路徑無效，editor 不會從 listContributions 移除

```tsx
// 2.2.a — start at /settings/link-detect → location 在 mount 後 effects 跑完變成 '/settings/editor'
// 2.2.b — start at /settings/open-behavior → 同上
// 2.2.c — start at /settings/editor-buffers → 維持既有 alias 行為（既有測試保留）
// 2.2.d — start at /settings/editor → location 維持不變（identity，避免無限循環 / 多餘 history entry）
// 2.2.e — start at /settings/link-detect 時 alias canonical target ('editor') 不 selectable（mock
//          useModuleEnabledStore 讓 editor module disabled，dispatch 後 listContributions 不含 'editor'）
//          → location 自我修復到 firstSelectable（在 fixture 下 = 'appearance' = order 0）
//            而非停在 '/settings/editor' 或 '/settings/link-detect'。
// 2.2.f — alias map identity case：rawUrlSection === canonical（editor）不重複觸發 setLocation
//          （spy useLocation setter 確認 mount + 後續 effect 不對同 path 多次 replace）
```

### Task 2.3 — 寫 Sync modularize 測試（先紅）

新增 `spa/src/lib/__tests__/sync-as-module.test.ts`：

```ts
// 2.3.a — registerBuiltinModules 後 getModules() 含 { id: 'sync', ... }
// 2.3.b — getModules().filter(m => m.disableable === true) 不含 sync
//          → ModulesSwitchboard 不列 Sync
// 2.3.c — listContributions('purdex') 中 sync 的 moduleId === 'sync'
//          → isModuleOwnedContribution 為 true
// 2.3.d — sync 的 order === SETTINGS_ORDER.MODULE_SYNC (=14)
// 2.3.e — stale persisted state useModuleEnabledStore { sync: false } 仍保留 sync contribution
//          （非 disableable 的 module 不被 enabled-store 過濾）
// 2.3.f — 重複呼叫 dispatchSettingsContributions（HMR 模擬）後 sync 仍存在且 moduleId 不變
// 2.3.g — registerSyncContributors 仍在 boot 被呼叫（檢查 syncEngine.getContributors().length === 7）
```

### Task 2.4 — 寫 Quick Commands 頁首測試（先紅）

`spa/src/components/settings/QuickCommandsSettingsSection.test.tsx` append：

```tsx
// 2.4.a — outer wrapper className 不含 'p-6'
// 2.4.b — render 後 DOM 含 <h2> 文字 = t('settings.quick_commands.title')
// 2.4.c — 緊隨 <h2> 之後是 <p> 文字 = t('settings.quick_commands.desc')
// 2.4.d — "新建" 按鈕仍存在 + click 後仍開 dialog（既有行為不破）
// 2.4.e — commands list 既有功能（edit / delete）仍可用（既有測試保留）
```

### Task 2.5 — 寫 PR-2 final order assertion（先紅）

新增 `spa/src/lib/__tests__/settings-order-pr2.test.ts`（取代 PR-1 的 transitional test）：

```ts
// 2.5.a — listContributions('purdex') ASC 順序 = §4.1.3 final 表
//   appearance(0), terminal(1), interface(2), [electron(5)?],
//   module-config(10), editor(11), quick-commands(12),
//   performance-monitor(13), sync(14),
//   [dev-environment(20)?], [tmux-agent-monitor(21)?]
// 2.5.b — sidebar 不含 'open-behavior' / 'link-detect' rows
// 2.5.c — listContributions('purdex') 每筆 entry 的 order 值都落在
//          `Object.values(SETTINGS_ORDER)` 集合內（防止非法 / 未經規劃的 order）。
//          ⚠️ 限制：runtime 拿不到「來源是常數還是 hard-code 數字」的資訊，所以這條
//          只能擋「order 值非法」，擋不到「值碰巧等於常數但來自 hard-code」。後者由
//          code review + commit 訊息守住，不在 test 範圍。
```

PR-2 commit 流程明確一條路徑：
- commit 1（Editor 收編）的開頭就 `git rm` 掉 `spa/src/lib/__tests__/settings-order-pr1.test.ts`，避免 commit 1-4 過程中 transitional test 因為某些 entry 已改、某些尚未改而紅
- `settings-order-pr2.test.ts`（Task 2.5）在 commit 5（最後一個 commit）才**新增**並讓綠 — 此時 perf-monitor / quick-commands / editor / sync 都已改完
- commit 1-4 期間 sidebar order 已經沒有任何專屬 test 守住（因 PR-1 transitional 已刪、PR-2 final 還沒加），這是預期窗口
- 其他 PR-2 紅測試（Task 2.1 / 2.2 / 2.3 / 2.4）對應的實作落在同一個 commit 內，commit 1-4 個別都綠

### Task 2.6 — Editor 三頁合一實作（讓 2.1 / 2.2 / 2.5 紅 → 部分綠）

#### 2.6.1 重組 `EditorPurdexSettingsSection`

`spa/src/components/settings/EditorPurdexSettingsSection.tsx`：

```tsx
import { EditorOpenBehaviorSection } from './editor/EditorOpenBehaviorSection'
import { EditorLinkDetectionSection } from './editor/EditorLinkDetectionSection'

export function EditorPurdexSettingsSection(_props: Props) {
  // ... 既有 hooks ...

  return (
    <div>
      <h2 className="text-lg text-text-primary">{t('settings.editor.title')}</h2>
      <p className="text-xs text-text-secondary mb-6">{t('settings.editor.desc')}</p>

      {/* 既有 6 個 Monaco SettingItem rows，原本就在 component 內，不動 */}

      <EditorOpenBehaviorSection />
      <EditorLinkDetectionSection />

      <p className="text-xs text-text-muted mt-4">
        {t('settings.editor.tiptap_note')}
      </p>
    </div>
  )
}
```

#### 2.6.2 縮減 `editorModuleDefinition.settings`

`spa/src/lib/register-modules/editor-module.tsx`：

```diff
  settings: [
    {
      localId: 'editor',
      scope: 'purdex',
-     order: 14,                                         // PR-1 過渡值
+     order: SETTINGS_ORDER.MODULE_EDITOR,               // = 11
      labelKey: 'settings.section.editor',
      component: EditorPurdexSettingsSection,
    },
-   {
-     localId: 'link-detect',
-     scope: 'purdex',
-     order: 13,
-     labelKey: 'settings.editor.link_detect.title',
-     component: EditorLinkDetectionSection,
-   },
-   {
-     localId: 'open-behavior',
-     scope: 'purdex',
-     order: 12,
-     labelKey: 'settings.editor.open_behavior.title',
-     component: EditorOpenBehaviorSection,
-   },
    { localId: 'workspace-home-path', ... },
    { localId: 'host-home-path', ... },
  ]
```

`EditorOpenBehaviorSection` / `EditorLinkDetectionSection` 兩個 component 不刪、不重寫；只是 register-modules 不再單獨註冊，由 `EditorPurdexSettingsSection` import。

#### 2.6.3 URL alias map

`spa/src/components/SettingsPage.tsx`：

```diff
+ const URL_ALIASES: Record<string, string> = {
+   'editor-buffers': 'editor',
+   'link-detect': 'editor',
+   'open-behavior': 'editor',
+ }

  const rawUrlSection = parts[0] || null
- // Legacy URL alias: pre-HSR the Editor Buffers tab lived at
- // `/settings/editor-buffers`; the new Editor section id is `editor`.
- // Bookmarks / browser history entries must keep resolving — map the old
- // id to the new one before the normal selection logic runs.
- const urlSection = rawUrlSection === 'editor-buffers' ? 'editor' : rawUrlSection
+ // Legacy URL aliases: keep prior `/settings/<id>` URLs resolving after
+ // sidebar restructure (editor-buffers → editor: HSR; link-detect /
+ // open-behavior → editor: PR-2 collapse).
+ const urlSection = rawUrlSection ? URL_ALIASES[rawUrlSection] ?? rawUrlSection : null
```

Self-heal 既有的 `setLocation(`/settings/${urlSection}`, { replace: true })` 路徑會自動把 URL replace 為 canonical `/settings/editor`，行為不變。

### Task 2.7 — Sync modularize 實作（讓 2.3 紅 → 綠）

`spa/src/lib/register-modules/index.tsx`：

```diff
+ import { SyncSection } from '../../components/settings/SyncSection'  // 既有 import 不動

  // 移除：
- registerSettingsSection({ id: 'sync', label: 'settings.section.sync', order: 16, component: SyncSection })

  // 新增（registerModule 區段內，最好放 quick-commands 之後）：
+ registerModule({
+   id: 'sync',
+   name: 'Sync',
+   // disableable 留空 — structural module，未來再加 disable 行為時另開 issue
+   settings: [
+     {
+       localId: 'sync',
+       scope: 'purdex',
+       order: SETTINGS_ORDER.MODULE_SYNC,
+       labelKey: 'settings.section.sync',
+       component: SyncSection,
+     },
+   ],
+ })
```

`registerSyncContributors()` call 維持目前位置不挪。

### Task 2.8 — Quick Commands 頁首 + Performance Monitor / Editor 收尾 order

#### 2.8.1 Quick Commands

`spa/src/components/settings/QuickCommandsSettingsSection.tsx`：

```diff
  return (
-   <div className="p-6 space-y-4">
-     <div className="flex items-center justify-between">
-       <h2 className="text-lg text-text-primary">{t('settings.quick_commands.title')}</h2>
+   <div>
+     <h2 className="text-lg text-text-primary">{t('settings.quick_commands.title')}</h2>
+     <p className="text-xs text-text-secondary mb-6">{t('settings.quick_commands.desc')}</p>
+     <div className="flex items-center justify-end mb-3">
        <button ref={triggerRef} ... >
          <Plus size={12} /> {t('settings.quick_commands.new')}
        </button>
      </div>
      {/* commands list 維持不動 */}
```

`registerModule({ id: 'quick-commands', ..., order: 15 })` → `SETTINGS_ORDER.MODULE_QUICK_COMMANDS` (=12)。

#### 2.8.2 Performance Monitor

`spa/src/lib/register-modules/index.tsx`：

```diff
  registerModule({
    id: 'memory-monitor',
    ...
    settings: [{
      localId: 'performance-monitor',
      scope: 'purdex',
-     order: 11,                                          // PR-1 過渡值
+     order: SETTINGS_ORDER.MODULE_PERFORMANCE_MONITOR,   // = 13
      labelKey: 'performance_monitor.title',
      component: PerformanceMonitorSettingsSection,
    }],
  })
```

#### 2.8.3 i18n 新增 key

`spa/src/locales/en.json`：

```json
"settings.quick_commands.desc": "Manage commands and where they appear in the UI."
```

`spa/src/locales/zh-TW.json`：

```json
"settings.quick_commands.desc": "管理快捷指令以及它們在介面中出現的位置。"
```

其他 locale（如有）比照。

### Task 2.9 — Sanity check（沒有專用實作步驟）

刪除動作已在 commit 1 做，final order test 在 commit 5 引入。本 task 只是 commit 流程上的 checkpoint：在開 PR 前確認 `settings-order-pr1.test.ts` 不存在、`settings-order-pr2.test.ts` 綠。

### Task 2.10 — 跑全部測試 + lint + 手動驗證

```bash
cd spa && npx vitest run && pnpm run lint && pnpm run build
```

手動（mlab dev server + Air `.app`）：
- `/settings` sidebar 順序與 §4.1.3 一致
- 直接訪問 `/settings/link-detect` + `/settings/open-behavior` 自動 replace 為 `/settings/editor`
- 直接訪問 `/settings/sync`（升格 module 後 deep link 仍可達 SyncSection）
- Editor module 在 Modules switchboard 改成 disabled → 訪問 `/settings/link-detect` 不 mount Editor、URL self-heal 到第一個 selectable section
- Editor 頁三段都顯示且互動正常
- Sync row 顯示 puzzle icon
- ModulesSwitchboard 不列 Sync
- Quick Commands 頁首視覺與 Appearance 一致

### PR-2 Acceptance（spec §5.2）

- [ ] A6：sidebar 不含 open-behavior / link-detect entry
- [ ] A7：Editor 頁三段 render + 互動 store 改變（test 2.1.c, 2.1.d 綠）
- [ ] A8：URL alias replace（test 2.2.a-f 綠）
- [ ] A9：Sync moduleId === 'sync' + puzzle icon（test 2.3.c, 2.3.d 綠）
- [ ] A10：Sync 不在 switchboard（test 2.3.b 綠）
- [ ] A11：Quick Commands h2 + p + outer 無 p-6（test 2.4.a-c 綠）
- [ ] A12：vitest + lint + build 全綠

### PR-2 Commits（每個 commit 都可獨立通過 CI — slice 內 red+green 同 commit）

1. `feat(editor): consolidate open-behavior + link-detect into Editor settings page` (Task 2.1 紅 + Task 2.6.1/2.6.2 實作 + Task 2.9 刪 PR-1 test 同 commit；測試 + 實作不分開)
2. `feat(settings): URL alias map for legacy editor sub-section paths` (Task 2.2 紅 + Task 2.6.3 實作同 commit)
3. `feat(sync): upgrade Sync to a structural module (non-disableable)` (Task 2.3 紅 + Task 2.7 實作同 commit)
4. `style(quick-commands): unify settings page header to Appearance pattern + add desc i18n` (Task 2.4 紅 + Task 2.8.1/2.8.3 實作同 commit)
5. `refactor(settings): switch all module-owned orders to SETTINGS_ORDER constants + add PR-2 final order test` (Task 2.8.2 實作 + Task 2.5 final order test 在這個 commit 內**新增**並讓綠)

關鍵點：
- commit 1 開頭就 `git rm spa/src/lib/__tests__/settings-order-pr1.test.ts` — transitional test 在 PR-2 一啟動就刪
- final order test 直到 commit 5 才**新增**（不是在 commit 1-4 中先以紅狀態存在）
- commit 1-4 期間沒有 sidebar order 專屬 test 守護是預期窗口；其他 PR-2 紅測試（Task 2.1/2.2/2.3/2.4）對應實作落在同一 commit 內，個別 commit 都綠

---

## 收斂與後續

- PR-1 + PR-2 各自跑兩輪 codex review（標準 + 三平行 adversarial）
- 預期 review finding：PR-1 收斂快（純視覺）；PR-2 finding 會集中在 alias edge case / Sync stale persisted state — 已在 plan §2.2.e + §2.3.e 預先測過
- Bump PR：alpha 號碼 +2（PR-1 一個、PR-2 一個），CHANGELOG 兩條
- 不在 PR scope 但需要開 issue 追蹤：
  - Performance Monitor 從 settings 拆出
  - Sync 加 `disableable: true` + engine/contributors disable wiring
