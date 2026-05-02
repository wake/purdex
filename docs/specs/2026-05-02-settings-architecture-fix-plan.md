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
// 1.2.a — listContributions('purdex') after registerBuiltinModules 順序
//   expected (ASC): appearance(0), terminal(1), interface(2),
//                   [electron(5)?],  ← caps 條件可 mock 成 true
//                   performance-monitor(11), open-behavior(12),
//                   link-detect(13), editor(14), quick-commands(15),
//                   module-config(10) [插在 interface 與 perf-monitor 之間]
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

### Task 1.4 — 改 puzzle icon 三 caller（一次到位）

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

### Task 1.5 — 寫 puzzle icon 測試（守住三 caller）

新增 `spa/src/components/settings/SettingsSidebar.test.tsx`（如已存在則 append）：

```tsx
// 1.5.a — Sidebar puzzle row className 不含 'rotate-[30deg]'
// 1.5.b — Sidebar puzzle weight prop = 'bold'
//   用 rendered DOM SVG 看不到 weight prop，改測 className 是 fill / bold 對應的 svg path
//   Phosphor Icons 的 bold 與 fill 渲染 stroke-width 不同；最簡單測法是
//   render PuzzlePiece 直接，比對 outerHTML 含 'stroke-width' (bold) 而非 'fill="currentColor"'
//   （或更簡單：mock @phosphor-icons/react 接收 weight prop，比對 prop 值）
// 1.5.c — module-owned row 顯示 puzzle，built-in row 不顯示
```

**WorkspaceSettingsPage.test.tsx** / **HostSidebar.test.tsx**（如已存在）追加同樣斷言。如不存在不新建（避免 PR-1 範圍膨脹）。

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

### PR-1 Commits（建議切分）

1. `feat(settings): introduce SETTINGS_ORDER constants` (Task 1.1)
2. `test(settings): add PR-1 sidebar order assertions (red)` (Task 1.2)
3. `refactor(settings): reorder sidebar entries per PR-1 transitional table` (Task 1.3)
4. `style(settings): puzzle icon → bold weight, no rotation` (Task 1.4 + 1.5)
5. `refactor(settings): drop double p-6 from ModulesSwitchboardSection` (Task 1.6)

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
```

### Task 2.2 — 寫 URL alias 測試（先紅）

新增 `spa/src/components/SettingsPage.test.tsx`（如已存在則 append）。Setup：mock `wouter` `useLocation` 起點為 `/settings/link-detect`，再以 `useLocation` setter 觀察 replace 後路徑。

```tsx
// 2.2.a — start at /settings/link-detect → setLocation called with '/settings/editor', { replace: true }
// 2.2.b — start at /settings/open-behavior → 同上
// 2.2.c — start at /settings/editor-buffers → 維持既有 alias 行為（existing test 保留）
// 2.2.d — start at /settings/editor → 不 replace（identity，避免無限循環）
// 2.2.e — start at /settings/link-detect 時 Editor module 被 disable（mock useModuleEnabledStore）
//          → 不 mount Editor，self-heal 走 default 路徑（不要求 hard 404）
// 2.2.f — alias map identity case: rawUrlSection === canonical 不重複 setLocation
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

### Task 2.5 — Editor 三頁合一實作（讓 2.1 / 2.2 紅 → 綠）

#### 2.5.1 重組 `EditorPurdexSettingsSection`

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

#### 2.5.2 縮減 `editorModuleDefinition.settings`

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

#### 2.5.3 URL alias map

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

### Task 2.6 — Sync modularize 實作（讓 2.3 紅 → 綠）

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

### Task 2.7 — Quick Commands 頁首 + Performance Monitor / Editor 收尾 order

#### 2.7.1 Quick Commands

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

#### 2.7.2 Performance Monitor

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

#### 2.7.3 i18n 新增 key

`spa/src/locales/en-US.ts`（或對應 i18n source）：

```ts
'settings.quick_commands.desc': 'Manage commands and where they appear in the UI.',
```

`zh-TW.ts`：

```ts
'settings.quick_commands.desc': '管理快捷指令以及它們在介面中出現的位置。',
```

其他 locale 比照。

### Task 2.8 — 寫 PR-2 final order 測試

新增 `spa/src/lib/__tests__/settings-order-pr2.test.ts`：

```ts
// 2.8.a — listContributions('purdex') ASC 順序 = §4.1.3 final 表
//   appearance(0), terminal(1), interface(2), [electron(5)?],
//   module-config(10), editor(11), quick-commands(12),
//   performance-monitor(13), sync(14),
//   [dev-environment(20)?], [tmux-agent-monitor(21)?]
// 2.8.b — sidebar 不含 'open-behavior' / 'link-detect' rows
// 2.8.c — 每個 entry 用的 order 值都來自 SETTINGS_ORDER（grep editorModuleDefinition / register-modules
//          的 order 值，確認都是 SETTINGS_ORDER.X 而非 hard-coded number）
//          —— 用測試保證未來 reviewer 看 git diff 直接擋住
```

刪除 `settings-order-pr1.test.ts`（它的 transitional order 在 PR-2 已不適用）。

### Task 2.9 — 跑全部測試 + lint + 手動驗證

```bash
cd spa && npx vitest run && pnpm run lint && pnpm run build
```

手動（mlab dev server + Air `.app`）：
- `/settings` sidebar 順序與 §4.1.3 一致
- 直接訪問 `/settings/link-detect` + `/settings/open-behavior` 自動 replace 為 `/settings/editor`
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

### PR-2 Commits（建議切分）

1. `test(settings): add PR-2 red tests for editor consolidation / alias / sync` (Task 2.1-2.4)
2. `refactor(editor): collapse open-behavior + link-detect into Editor settings page` (Task 2.5)
3. `feat(settings): URL alias map for legacy editor sub-section paths` (Task 2.5.3)
4. `feat(sync): upgrade Sync to a structural module (non-disableable)` (Task 2.6)
5. `style(quick-commands): unify settings page header to Appearance pattern` (Task 2.7.1)
6. `refactor(settings): switch all module-owned orders to SETTINGS_ORDER constants` (Task 2.7.2 + 2.8)
7. `i18n(settings): add quick_commands.desc` (Task 2.7.3)

---

## 收斂與後續

- PR-1 + PR-2 各自跑兩輪 codex review（標準 + 三平行 adversarial）
- 預期 review finding：PR-1 收斂快（純視覺）；PR-2 finding 會集中在 alias edge case / Sync stale persisted state — 已在 plan §2.2.e + §2.3.e 預先測過
- Bump PR：alpha 號碼 +2（PR-1 一個、PR-2 一個），CHANGELOG 兩條
- 不在 PR scope 但需要開 issue 追蹤：
  - Performance Monitor 從 settings 拆出
  - Sync 加 `disableable: true` + engine/contributors disable wiring
