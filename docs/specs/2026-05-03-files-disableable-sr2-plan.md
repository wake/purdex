# Plan — Files module disableable + SR-2 修復

> Date: 2026-05-03  
> Spec: [`2026-05-03-files-disableable-sr2-spec.md`](./2026-05-03-files-disableable-sr2-spec.md)  
> Status: Draft (round-2 codex plan review pending — round-1 finding 已修)  
> Worktree: `.claude/worktrees/files-disableable-sr2` / branch `worktree-files-disableable-sr2`

## 0. 開工前準備

**進 worktree** (已 done — session 已在 `/Users/wake/Workspace/wake/purdex/.claude/worktrees/files-disableable-sr2`)

**確認 staging**:
```bash
git status                                # 應該只有 spec 檔 staged
git diff --cached --stat                  # 確認無 spec 以外檔案 leak
```

**Subagent task brief**：每個 Bash 都要 `cd <worktree-path> && ...`（feedback_subagent_cwd_enforcement）。

**驗證指令**（每 commit 跑一次）：
```bash
pnpm --prefix spa exec vitest run
pnpm --prefix spa exec tsc -p tsconfig.app.json --noEmit
pnpm --prefix spa run lint
pnpm --prefix spa run build
```
建議 commit-1 跑完整套；commit 2-4 在開發中先跑 affected `vitest run <path>`，最後一個 commit 完整跑全套。

## 1. Commit 1 — i18n keys + `SETTINGS_ORDER.WORKSPACE_FILES`

### 1.1 觸碰檔案
- `spa/src/locales/en.json`
- `spa/src/locales/zh-TW.json`
- `spa/src/lib/settings-order.ts`

### 1.2 i18n keys 新增（en + zh-TW 對齊）

en.json 三個新 key（依字母 order 插在合適位置）：
```json
"modules.files.description": "Workspace and session file tree, with click-to-open into the Editor",
"settings.files.project_path.label": "Project path",
"settings.section.files_workspace": "Files",
```

zh-TW.json 對應（注意 `settings.section.files_workspace` 改用「檔案」對齊既有「終端機」/「外觀」風格）：
```json
"modules.files.description": "工作區與 Session 檔案樹，可點擊開啟至 Editor",
"settings.files.project_path.label": "專案路徑",
"settings.section.files_workspace": "檔案",
```

**插入位置（精確 line 對齊既有檔案，subagent 直接照插）**：

en.json：
- `modules.files.description` → 插在 line 24 後（`modules.quick_commands.description` 之後），形成 editor → browser → memory_monitor → quick_commands → files 群組（依既有非字母順序，append 風格）。
- `settings.section.files_workspace` → 插在 line 145 後（`settings.section.editor` 之後）。
- `settings.files.project_path.label` → 插在 line 165 後（`settings.editor` block 起始之前；獨立放在 `settings.editor.title` 之前更乾淨；如環境位置已偏，subagent 取「`settings.editor.*` block 之前」原則處理）。

zh-TW.json：對應 en.json 行號鏡像（zh-TW.json 結構同 en.json）；如 zh-TW.json line offset 與 en.json 不同，subagent 用「相同 key 鄰居」策略找位置。

### 1.3 `SETTINGS_ORDER.WORKSPACE_FILES` 新增

`spa/src/lib/settings-order.ts` diff（**整段 docblock 改寫，明確 order values are scoped per settings scope，避免讀者把 purdex MODULE_CONFIG=10 與 workspace WORKSPACE_FILES=10 誤讀為撞號**）：

```diff
 /**
- * Centralized order constants for the Settings sidebar.
- *
- * The sidebar is grouped into bands so visual ordering communicates intent:
- *
- *   | Band                        | Range  | Examples                                  |
- *   |-----------------------------|--------|-------------------------------------------|
- *   | Top built-in (core)         | 0 – 4  | Appearance / Terminal / Interface         |
- *   | Top conditional built-in    | 5 – 9  | Electron (gated by canSystemTray)         |
- *   | Modules switchboard         | 10     | `module-config` (single header row)       |
- *   | Module-owned                | 11–19  | Editor / Quick Commands / Perf / Sync     |
- *   | Tail built-in               | 20–29  | Dev Environment / Tmux Agent Monitor      |
+ * Centralized order constants for the Settings sidebar.
+ *
+ * Order values are **scoped per settings scope** (`purdex` / `workspace` /
+ * `host`); the sidebar sorts each scope's contributions independently. The
+ * tables below describe the visual bands within each scope. Numbers can
+ * legitimately repeat across scopes (e.g. purdex `MODULE_CONFIG = 10` and
+ * workspace `WORKSPACE_FILES = 10`) — they never compete because contribution
+ * lists are filtered by scope before sorting.
+ *
+ * **purdex scope** — sidebar at `/settings`:
+ *
+ *   | Band                        | Range  | Examples                                  |
+ *   |-----------------------------|--------|-------------------------------------------|
+ *   | Top built-in (core)         | 0 – 4  | Appearance / Terminal / Interface         |
+ *   | Top conditional built-in    | 5 – 9  | Electron (gated by canSystemTray)         |
+ *   | Modules switchboard         | 10     | `module-config` (single header row)       |
+ *   | Module-owned                | 11–19  | Editor / Quick Commands / Perf / Sync     |
+ *   | Tail built-in               | 20–29  | Dev Environment / Tmux Agent Monitor      |
+ *
+ * **workspace scope** — sidebar at `/settings/workspaces/<id>`:
+ *
+ *   | Band                        | Range  | Examples                                  |
+ *   |-----------------------------|--------|-------------------------------------------|
+ *   | Module-owned                | 0 – 19 | Editor home path (inline 0) / Files (10)  |
+ *
+ * **host scope** — sidebar at `/settings/hosts/<id>`:
+ *
+ *   | Band                        | Range   | Examples                                 |
+ *   |-----------------------------|---------|------------------------------------------|
+ *   | Module-owned                | 0 – 199 | Editor home path (inline 100)            |
  *
  * `register-modules/index.tsx`, `editor-module.tsx`, and any future
  * `registerSettingsSection` / `registerModule({ settings: [...] })` call
  * MUST import from this file instead of hard-coding numbers. Reviewers
  * watch for hard-coded `order:` literals during PR review.
  *
  * Spec §4.1.3 (PR-2 final values). PR-1's transitional `*_PR1` constants
  * were removed by PR-2 commit 5 once Editor was consolidated and Sync
  * was promoted to a structural module.
  */
 export const SETTINGS_ORDER = {
+  // ---- purdex scope ---------------------------------------------------
   // Top built-in (core) — always present.
   APPEARANCE: 0,
   TERMINAL: 1,
   INTERFACE: 2,
   // Top conditional built-in.
   ELECTRON: 5,
   // Modules switchboard — single row, header of the modules group.
   MODULE_CONFIG: 10,
   // Module-owned (PR-2 final order).
   MODULE_EDITOR: 11,
   MODULE_QUICK_COMMANDS: 12,
   MODULE_PERFORMANCE_MONITOR: 13,
   MODULE_SYNC: 14,
   // Tail built-in — dev / debug surfaces.
   DEV_ENVIRONMENT: 20,
   TMUX_AGENT_MONITOR: 21,
+  // ---- workspace scope ------------------------------------------------
+  WORKSPACE_FILES: 10,
 } as const
```

> 注意：（1）`WORKSPACE_FILES = 10` 放在 workspace section 註解後（檔尾）；（2）數字 `10` 與 purdex `MODULE_CONFIG = 10` 重複是 by-design，docblock 已說明 per-scope 獨立；（3）Editor inline 0 / 100 暫不收編（spec §4.1 註解備案）。

### 1.4 TDD（commit 1）
- 此 commit 純資料 / 常數，沒有新功能 → 無新測試。
- 既有測試應 0 影響。
- 驗證：跑全套 vitest / tsc / lint / build，預期全綠（`SETTINGS_ORDER.WORKSPACE_FILES` 有定義但無消費者）。

### 1.5 Commit message
```
chore(settings): seed i18n keys + SETTINGS_ORDER.WORKSPACE_FILES for Files module migration

Setup commit for SR-2 fix (Files module disableable). Adds three i18n keys
(modules.files.description, settings.section.files_workspace,
settings.files.project_path.label) and the WORKSPACE_FILES = 10 order
constant. No functional change; subsequent commits consume them.

Spec: docs/specs/2026-05-03-files-disableable-sr2-spec.md §1.x
```

## 2. Commit 2 — `FilesWorkspaceSettingsSection` 元件 + 單元測試

### 2.1 觸碰檔案
- 新檔 `spa/src/components/settings/FilesWorkspaceSettingsSection.tsx`
- 新檔 `spa/src/components/settings/FilesWorkspaceSettingsSection.test.tsx`

### 2.2 元件實作

```tsx
// spa/src/components/settings/FilesWorkspaceSettingsSection.tsx
import { useId } from 'react'
import { useWorkspaceStore } from '../../features/workspace/store'
import { useI18nStore } from '../../stores/useI18nStore'
import type { SettingsContextFor } from '../../lib/settings-contribution-types'

interface Props {
  ctx: SettingsContextFor<'workspace'>
}

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

設計筆記：
- `useId()` for stable input id（與 `ConfigField` 一致）。
- `useWorkspaceStore` selector 走「找 workspace → 讀 moduleConfig」鏈，nullish chain 保 storage shape 缺失安全（與 `FileTreeView.tsx:24` 完全等價）。
- onChange-immediate（不對齊 EditorHomePathWorkspaceSection 的 trim/blur）— spec §4.2 已說明。
- `if (ctx.scope !== 'workspace') return null` 是型別 narrowing 兼運行守則。

### 2.3 TDD — 兩階段紅綠

**階段 A — Stub commit-step**：先建立最小 stub `FilesWorkspaceSettingsSection.tsx` 只 export `() => null`，讓測試 import 不爆但行為紅：

```tsx
// stub
export function FilesWorkspaceSettingsSection(_props: { ctx: any }) { return null }
```

**階段 B — Test-first 行為紅**：寫下方測試，預期：
- "renders empty value when no projectPath is set" → 紅（找不到 `textbox`）
- "renders existing projectPath" → 紅（找不到 `textbox`）
- "writes back to ..." → 紅
- "label uses i18n key ..." → 紅
- "returns null when ctx scope is not workspace" → 綠（stub 永遠 null）

**階段 C — 實作補綠**：把元件改寫成 §2.2 完整實作；五個 test 全綠。

> 此三段必須在同一 commit 內完成（commit 2 是「component + tests」單一語意），不分多 commit。階段命名只是 subagent 內部執行步驟說明。

`FilesWorkspaceSettingsSection.test.tsx`：

```tsx
import { vi } from 'vitest'
vi.mock('../../stores/useI18nStore', () => ({
  useI18nStore: Object.assign(vi.fn((sel: any) => sel({ t: (k: string) => k })), {
    getState: () => ({ t: (k: string) => k }),
  }),
}))

import { describe, it, expect, beforeEach } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import { FilesWorkspaceSettingsSection } from './FilesWorkspaceSettingsSection'
import { useWorkspaceStore } from '../../features/workspace/store'

describe('FilesWorkspaceSettingsSection', () => {
  let wsId: string

  beforeEach(() => {
    cleanup()
    useWorkspaceStore.getState().reset()
    const ws = useWorkspaceStore.getState().addWorkspace('Test')
    wsId = ws.id
  })

  it('returns null when ctx scope is not workspace', () => {
    // narrow guard branch
    // @ts-expect-error — feeding wrong-scope ctx to verify runtime guard
    const { container } = render(<FilesWorkspaceSettingsSection ctx={{ scope: 'purdex' }} />)
    expect(container.innerHTML).toBe('')
  })

  it('renders empty value when no projectPath is set', () => {
    render(<FilesWorkspaceSettingsSection ctx={{ scope: 'workspace', workspaceId: wsId }} />)
    expect((screen.getByRole('textbox') as HTMLInputElement).value).toBe('')
  })

  it('renders existing projectPath', () => {
    useWorkspaceStore.getState().setModuleConfig(wsId, 'files', 'projectPath', '/home/user/proj')
    render(<FilesWorkspaceSettingsSection ctx={{ scope: 'workspace', workspaceId: wsId }} />)
    expect((screen.getByRole('textbox') as HTMLInputElement).value).toBe('/home/user/proj')
  })

  it('writes back to useWorkspaceStore.moduleConfig.files.projectPath on change', () => {
    render(<FilesWorkspaceSettingsSection ctx={{ scope: 'workspace', workspaceId: wsId }} />)
    fireEvent.change(screen.getByRole('textbox'), { target: { value: '/new/path' } })
    const stored = useWorkspaceStore
      .getState()
      .workspaces.find((w) => w.id === wsId)?.moduleConfig?.['files']?.['projectPath']
    expect(stored).toBe('/new/path')
  })

  it('label uses i18n key settings.files.project_path.label', () => {
    render(<FilesWorkspaceSettingsSection ctx={{ scope: 'workspace', workspaceId: wsId }} />)
    expect(screen.getByLabelText('settings.files.project_path.label')).toBeInTheDocument()
  })
})
```

> Pattern 對齊 `ModuleConfigSection.test.tsx`（也用 i18n mock + render + fireEvent）。`useWorkspaceStore.reset()` 清狀態。

### 2.4 驗證
```bash
pnpm --prefix spa exec vitest run spa/src/components/settings/FilesWorkspaceSettingsSection.test.tsx
```
預期：5 tests pass。

### 2.5 Commit message
```
feat(settings): add FilesWorkspaceSettingsSection workspace-scope component

New self-contained component for the Files module's projectPath setting.
Reads/writes useWorkspaceStore.workspaces[wsId].moduleConfig.files.projectPath
(same path FileTreeView already uses), so the storage shape is unchanged.

Not yet wired into the registry — commit 3 migrates the Files module to
settings: [{ scope: 'workspace' }] and references this component.

Spec: docs/specs/2026-05-03-files-disableable-sr2-spec.md §4.2
```

## 3. Commit 3 — Files module 遷移 + SR-2 拔註解

### 3.1 觸碰檔案
- `spa/src/lib/register-modules/index.tsx`
- `spa/src/lib/dispatch-settings-contributions.ts`
- `spa/src/lib/register-modules.test.ts`（修既有測試）
- `spa/src/features/workspace/components/WorkspaceSettingsPage.tsx`（移除 ModuleConfigSection 渲染掛載點）
- 不動：`spa/src/components/settings/ModuleConfigSection.tsx`（自然 dead；follow-up issue 拆）

### 3.2 `register-modules/index.tsx` Files 區塊改寫

完整 diff：
```diff
   // FS backends
   registerBuiltinFsBackends(caps)

   registerModule({
     id: 'files',
     name: 'Files',
-    // SR-2 (codex review #617): intentionally NOT flagged disableable yet.
-    // The module's only settings surface lives in `workspaceConfig`, which
-    // `WorkspaceSettingsPage` renders through `ModuleConfigSection` — a path
-    // that does not consult `useModuleEnabledStore`. Toggling would be a lie
-    // until PR 3 wires workspace-scope legacy contributions into the filter.
-    workspaceConfig: [
-      { key: 'projectPath', type: 'string', label: '專案路徑' },
-    ],
+    disableable: true,
+    descriptionKey: 'modules.files.description',
+    settings: [
+      {
+        localId: 'workspace-files',
+        scope: 'workspace',
+        order: SETTINGS_ORDER.WORKSPACE_FILES,
+        labelKey: 'settings.section.files_workspace',
+        component: FilesWorkspaceSettingsSection,
+      },
+    ],
     views: [
       ...
     ],
   })
```

新增 import（檔案頂部）：
```ts
import { FilesWorkspaceSettingsSection } from '../../components/settings/FilesWorkspaceSettingsSection'
```

### 3.3 `dispatch-settings-contributions.ts` 清理

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

### 3.4 `WorkspaceSettingsPage.tsx` 移除 ModuleConfigSection 掛載點

```diff
 import { WorkspaceIconPicker } from './WorkspaceIconPicker'
 import { WorkspaceDeleteDialog } from './WorkspaceDeleteDialog'
-import { ModuleConfigSection } from '../../../components/settings/ModuleConfigSection'

 ...

         {/* Icon */}
         <section className="mb-8">
           ...
         </section>

-        {/* Module Settings */}
-        <ModuleConfigSection scope={{ workspaceId }} />
-
         {/* Registry-driven workspace-scoped contributions.
             ... */}
         {workspaceContributions.map((c) => {
```

**理由更正（codex round-1 P1）**：Files 拔 `workspaceConfig` 後 `getModulesWithWorkspaceConfig()` 自動回 `[]`，`ModuleConfigSection.tsx:16` 的 `if (modules.length === 0) return null` 會讓元件不渲染任何 DOM。所以「移除 mount」**不是功能上必要**（disable filter 已透過 `settings: [...]` 路徑生效），而是 housekeeping：

- 移除後讀者看 `WorkspaceSettingsPage.tsx` 不會再對「為什麼有個 `<ModuleConfigSection>` 但好像沒輸出」困惑。
- 與 spec N2「不刪 `ModuleConfigSection.tsx` 檔案 / 欄位 / helper」對齊：本 PR 只拔 mount call，`ModuleConfigSection.tsx` 元件本體仍在。

> 風險評估：若 `ModuleConfigSection.tsx` 被任何**外部**（test / dev tool / story）引用，移除 import 不會影響它們。`rg "ModuleConfigSection" spa/src` 確認過：唯二引用是 `WorkspaceSettingsPage.tsx`（mount 點 — 本 PR 移除）和 `ModuleConfigSection.test.tsx`（自身測試 — 不動）。

### 3.5 修既有測試 `register-modules.test.ts`

刪掉 `does NOT warn for files module (exempted during transition)`（現有 line 479-488）。新增一個更具體的：

```ts
it('does NOT emit any deprecation warning for the real Files bootstrap', () => {
  registerBuiltinModules()
  const msgs = (warnSpy.mock.calls as unknown[][]).map((c) => String(c[0]))
  expect(msgs.some((m) => m.includes('files') && m.includes('deprecated'))).toBe(false)
})
```

> 放在同個 `describe('ModuleDefinition.globalConfig / workspaceConfig deprecation (PR-5)', ...)` 裡。`registerBuiltinModules()` 在既有 `register-modules.test.ts:58-145` 已被大量直接呼叫且不需特別 mock — `getPlatformCapabilities()` 對 vitest 環境下缺 `window.electronAPI` 是 safe fallback，FS backend / sync registry 寫入都是同步 in-memory；不再保留窄 fixture fallback（codex round-1 P1：fallback 會弱化 SR-2 真實驗證，而 real bootstrap 已可用）。

### 3.6 TDD — 紅先（disable filter 接得到的測試）

對應 spec §6.1 #1 / #2 / #3 / #4。**重點：codex round-1 P0 — 「Files disabled → 無 contribution」單獨測試會 false-green（current Files 沒有任何 workspace contribution，所以 disabled 時 list 本來就空），無法證明 SR-2 修好。修法：在同一 test 內先驗 enabled 出現 → reset → disabled 不出現。**（legacy hardcoded `'專案路徑'` label absence 在 §4.2 UI test 另驗，registry test 不覆蓋 DOM path — codex round-2 P2 釐清。）

```ts
// 在 register-modules.test.ts 新 describe block
// 補上既有 ./module-registry import 中尚未列入的 getModule（既有檔已 import clearModuleRegistry / getModules / getPaneRenderer / registerModule — codex round-2 P1）
import { getModule } from './module-registry'
import { useModuleEnabledStore } from '../stores/useModuleEnabledStore'
import { SETTINGS_ORDER } from './settings-order'

describe('Files module — SR-2 fix (disable filter via settings contribution)', () => {
  beforeEach(() => {
    clearAll()  // 沿用既有 helper（已含 clearModuleRegistry / clearContributions）
    useModuleEnabledStore.setState({ enabled: {}, baseline: null })
  })

  afterEach(() => {
    clearAll()
    useModuleEnabledStore.setState({ enabled: {}, baseline: null })
  })

  it('Files registers with disableable: true + descriptionKey + no workspaceConfig', () => {
    registerBuiltinModules()
    const filesMod = getModule('files')!
    expect(filesMod.disableable).toBe(true)
    expect(filesMod.descriptionKey).toBe('modules.files.description')
    expect(filesMod.workspaceConfig).toBeUndefined()
  })

  it('Files contributes a workspace-scope settings entry with correct localId/order', () => {
    registerBuiltinModules()
    const list = listContributions('workspace')
    const filesEntry = list.find((c) => c.id === 'files.workspace-files')
    expect(filesEntry).toBeDefined()
    expect(filesEntry?.scope).toBe('workspace')
    expect(filesEntry?.order).toBe(SETTINGS_ORDER.WORKSPACE_FILES)
    expect(filesEntry?.labelKey).toBe('settings.section.files_workspace')
    expect(filesEntry?.moduleId).toBe('files')
  })

  // CRITICAL: same-test before/after compare to avoid false-green from
  // "Files never had a workspace contribution to begin with" (codex R1 P0).
  it('reload-after-disable: Files contribution present when enabled, absent when disabled before bootstrap', () => {
    // Step 1 — Files enabled (default) → Files contribution present
    registerBuiltinModules()
    const enabledList = listContributions('workspace')
    expect(enabledList.find((c) => c.id === 'files.workspace-files')).toBeDefined()

    // Step 2 — reset state and bootstrap with Files persisted-disabled
    clearAll()
    useModuleEnabledStore.setState({ enabled: { files: false }, baseline: null })
    registerBuiltinModules()
    const disabledList = listContributions('workspace')
    expect(disabledList.find((c) => c.id === 'files.workspace-files')).toBeUndefined()
  })
})
```

> 注意：`registerBuiltinModules()` 在既有 vitest 環境已大量被呼叫（line 58-145），不需額外 mock（codex R1 P1 確認）。新 describe 的 beforeEach + afterEach 都顯式 reset `useModuleEnabledStore` 是必要的 — `enabled` 由 persist middleware 寫到 storage（`useModuleEnabledStore.ts:79, 112-119`），不 reset 會讓 case 順序影響後續 test。

### 3.7 整合測試 `WorkspaceSettingsPage.registry.test.tsx` — 留到 commit 4

### 3.8 驗證
```bash
pnpm --prefix spa exec vitest run spa/src/lib/register-modules.test.ts
pnpm --prefix spa exec vitest run spa/src/lib/dispatch-settings-contributions.test.ts
pnpm --prefix spa exec tsc -p tsconfig.app.json --noEmit
pnpm --prefix spa run lint
```

預期：
- `register-modules.test.ts` 既有 ~30+ tests + 新增 3-4 tests 全綠（其中刪了 1 個 `does NOT warn for files module (exempted during transition)`）。
- `dispatch-settings-contributions.test.ts` 全綠（dual-declaration / F1 / etc 不受影響）。
- tsc / lint clean。

### 3.9 Commit message
```
refactor(files): migrate Files module from workspaceConfig to settings contribution (close SR-2)

Files now declares `disableable: true` + `settings: [{ scope: 'workspace' }]`
instead of the deprecated `workspaceConfig`. This wires the module's
projectPath setting into `dispatchSettingsContributions`'s disable filter,
so toggling Files off in the Modules Switchboard actually hides the
setting (after a reload, matching alpha.288's reload-required UX).

Changes:
- register-modules/index.tsx: Files block rewritten; SR-2 inline comment removed
- dispatch-settings-contributions.ts: drop 'files' from DEPRECATED_LEGACY_CONFIG_EXEMPT
  and update the surrounding comment
- WorkspaceSettingsPage.tsx: remove now-dead <ModuleConfigSection> mount
- register-modules.test.ts: drop the "files exempted during transition" test
  and add coverage for the new disable-filter path

Storage shape unchanged — moduleConfig.files.projectPath still backs the value;
existing readers (FileTreeView, file-open-bootstrap) unaffected.

Spec: docs/specs/2026-05-03-files-disableable-sr2-spec.md §4.1, §4.3, §6.1
Closes SR-2 from PR #617.
```

## 4. Commit 4 — 整合測試（補 UI coverage，非 TDD 紅綠 commit）

**定位調整（codex round-1 P0）**：commit 3 已落地實作 + registry 層測試，commit 4 是「補 UI 渲染層 coverage」而非 TDD 紅綠 commit。Commit message 與 spec §6.3 強調 coverage 性質。下方 test 若在 commit 3 之後寫，本就會直接綠 — 這是預期行為，不違反 TDD 紀律（registry 層的紅綠在 commit 3 §3.6 已完成）。

### 4.1 觸碰檔案
- `spa/src/features/workspace/components/WorkspaceSettingsPage.registry.test.tsx`

### 4.2 新增測試（spec §6.3 #9 #10）

在既有 `describe('WorkspaceSettingsPage — workspace-scoped registry rendering', ...)` 後新增：

```tsx
import { registerBuiltinModules } from '../../../lib/register-modules'
import { clearModuleRegistry } from '../../../lib/module-registry'
import { useModuleEnabledStore } from '../../../stores/useModuleEnabledStore'

describe('WorkspaceSettingsPage — Files module integration (SR-2)', () => {
  let wsId: string

  beforeEach(() => {
    cleanup()
    clearModuleRegistry()
    clearContributions()
    useWorkspaceStore.getState().reset()
    useModuleEnabledStore.setState({ enabled: {}, baseline: null })
    const ws = useWorkspaceStore.getState().addWorkspace('Test WS')
    wsId = ws.id
  })

  afterEach(() => {
    cleanup()
    clearContributions()
    clearModuleRegistry()
    useModuleEnabledStore.setState({ enabled: {}, baseline: null })
  })

  // CRITICAL: same-test before/after compare to prove SR-2 fix actually wires
  // the disable filter (codex R1 P0). Reuse render() output to verify the
  // legacy hardcoded `'專案路徑'` label is also gone — that string used to
  // come from ConfigField + ModuleConfigSection's deprecated path; if it
  // still shows up, the SR-2 mount removal is incomplete.
  it('reload-after-disable: Files header/input render when enabled, disappear when disabled before bootstrap', () => {
    // Step 1 — Files enabled (default) → header + input rendered
    registerBuiltinModules()
    const { unmount } = render(<WorkspaceSettingsPage workspaceId={wsId} />)
    expect(screen.getByText('settings.section.files_workspace')).toBeInTheDocument()
    expect(screen.getByLabelText('settings.files.project_path.label')).toBeInTheDocument()
    // Legacy hardcoded label from old ConfigField path must be gone
    expect(screen.queryByText('專案路徑')).toBeNull()
    unmount()

    // Step 2 — reset registries + state, bootstrap with Files persisted-disabled
    clearModuleRegistry()
    clearContributions()
    useModuleEnabledStore.setState({ enabled: { files: false }, baseline: null })
    registerBuiltinModules()
    render(<WorkspaceSettingsPage workspaceId={wsId} />)
    expect(screen.queryByText('settings.section.files_workspace')).toBeNull()
    expect(screen.queryByLabelText('settings.files.project_path.label')).toBeNull()
  })
})
```

> 注意 imports: `registerBuiltinModules`、`clearModuleRegistry`、`useModuleEnabledStore` 都要加；既有檔頂部已有 `clearContributions` / `useWorkspaceStore`。

### 4.3 驗證
```bash
pnpm --prefix spa exec vitest run spa/src/features/workspace/components/WorkspaceSettingsPage.registry.test.tsx
pnpm --prefix spa exec vitest run                # 完整跑一次確認沒打到別人
pnpm --prefix spa exec tsc -p tsconfig.app.json --noEmit
pnpm --prefix spa run lint
pnpm --prefix spa run build
```

預期：~3200 tests + 新增 ≈ 9（commit 2 五個 + commit 3 四個 + commit 4 兩個 - commit 3 刪一個 = 淨增約 8），全綠。

### 4.4 Commit message
```
test(settings): add WorkspaceSettingsPage UI coverage for Files module enable/disable (SR-2)

Coverage commit (not TDD red→green — registry layer red→green is in commit 3).
Adds end-to-end UI verification of the reload-after-disable contract:
registerBuiltinModules() + render(<WorkspaceSettingsPage>) with Files
enabled vs disabled-before-bootstrap. Same-test before/after compare proves
the SR-2 fix actually wires the disable filter; explicit `'專案路徑'`
absence assertion guards against the legacy ConfigField path leaking back.

Spec: docs/specs/2026-05-03-files-disableable-sr2-spec.md §6.3
```

## 5. 風險點 / Rollback

| 風險 | 影響 | 偵測 | Rollback |
|---|---|---|---|
| `registerBuiltinModules()` 在測試環境噴 capability 缺漏 | commit 3 / 4 紅 | vitest run output | 改用窄 fixture（直接 registerModule + dispatch）替代 `registerBuiltinModules()` |
| `WorkspaceSettingsPage.tsx` 拿掉 ModuleConfigSection 後，現存 `WorkspaceSettingsPage.registry.test.tsx` 既有 case 撞到（如 baseline 測試 querySelector 路徑） | commit 3 紅 | vitest run | 修既有測試的 selector，不還原刪掉的掛載點 |
| `useWorkspaceStore.reset()` 清掉測試前 setup 的 workspace | commit 2 / 4 紅 | vitest output `wsId is undefined` | beforeEach 內先 reset 後 addWorkspace（已照此順序） |
| zustand persist 副作用洩漏（test 跑完 localStorage 殘留 enabled.files=false） | follow-on test 紅（看狀態） | vitest output | beforeEach 強制 `useModuleEnabledStore.setState({ enabled: {}, baseline: null })` |
| Bundle size 影響（新增 component） | 微（< 1KB gzipped） | `pnpm --prefix spa run build` 看 size 報告 | 不需 rollback |

**Rollback 路徑**：每個 commit 獨立可 revert。若 commit 4 出包，留 commit 1-3（disable filter 已生效）；若 commit 3 出包，留 commit 1-2（純準備 + 元件，無功能變更）。

## 6. PR description 草稿

```markdown
## Summary

Close SR-2 from codex review of PR #617: migrate Files module from
deprecated `workspaceConfig` to `settings: [{ scope: 'workspace' }]` so
the Modules Switchboard's disable filter actually hides the projectPath
setting. Files is now `disableable: true` and behaves identically to
Editor / Quick Commands / Performance Monitor (reload-required UX).

Spec: [`2026-05-03-files-disableable-sr2-spec.md`](docs/specs/2026-05-03-files-disableable-sr2-spec.md)
Plan: [`2026-05-03-files-disableable-sr2-plan.md`](docs/specs/2026-05-03-files-disableable-sr2-plan.md)

## Changes

- `register-modules/index.tsx` — Files: `disableable: true` + `settings: [...]` + drop SR-2 inline comment
- `dispatch-settings-contributions.ts` — drop `'files'` from `DEPRECATED_LEGACY_CONFIG_EXEMPT` + update surrounding comment
- `WorkspaceSettingsPage.tsx` — drop now-dead `<ModuleConfigSection>` mount
- New: `FilesWorkspaceSettingsSection.tsx` — workspace-scope settings UI for projectPath (storage path unchanged)
- New: `SETTINGS_ORDER.WORKSPACE_FILES = 10` — first workspace-scope constant
- 3 new i18n keys (en + zh-TW)
- Tests: ~8 net additions, 1 deletion

## Storage

Storage shape **unchanged**: `useWorkspaceStore.workspaces[wsId].moduleConfig.files.projectPath`.
All existing readers (`FileTreeView`, `file-open-bootstrap.ts`) untouched.

## Test plan

> Subagent 跑完 PR 開立前驗證後，再把下方 `[ ]` 換成 `[x]`。

- [ ] `pnpm --prefix spa exec vitest run` — full suite green
- [ ] `pnpm --prefix spa exec tsc -p tsconfig.app.json --noEmit` — 0 errors
- [ ] `pnpm --prefix spa run lint` — 0 errors
- [ ] `pnpm --prefix spa run build` — passes
- [ ] **mlab live verify**:
  - [ ] `/settings/module-config` lists Files toggle row with description text
  - [ ] Disable Files (without reload) → workspace settings still shows Files (live toggle does not re-dispatch); ReloadBanner appears
  - [ ] Reload → workspace settings no longer shows Files header / input
  - [ ] Re-enable + reload → Files header / input return with prior value
  - [ ] file-tree workspace/session views still mountable into sidebar even when Files disabled (I4 known limitation)
  - [ ] Browser console no longer warns `[module] files uses deprecated workspaceConfig`

## Follow-up issues

- F-1 (planned): drop `ModuleConfigSection.tsx` + `getModulesWith*Config()` helpers + `ModuleDefinition.workspaceConfig` / `globalConfig` fields once no module uses them
- F-2: clean stale comment in `ModulesSwitchboardSection.tsx` lines 64-68 (mentions `editor/files/browser/memory-monitor` having no purdex contribution; no longer accurate)
- F-3: if "all disableable modules need a settings page" rule is ever adopted, add Files purdex placeholder for symmetry with Browser
```

## 7. Resolved decisions（codex round-1 review 後收斂）

- Q1（resolved）：commit 3 移除 `<ModuleConfigSection>` mount 不拆獨立 commit。理由更正（codex R1 P1.5）— 移除**不是**功能需要，是 housekeeping；但歸到 commit 3 仍合理，因 commit 3 是「Files 遷移與 SR-2 收尾」單一語意。spec N2 講「不刪 component file」與本 PR 拔 mount 不矛盾（檔案保留、call site 拔）。
- Q2（resolved）：不收編 Editor 的 inline `workspace-home-path` / `host-home-path` 進 SETTINGS_ORDER。理由：（1）超出本 PR scope；（2）Editor 既有風格穩定；（3）`settings-order.ts` 註解 workspace band 已標明 Editor inline 0 是 known legacy；（4）codex R1 Q2 確認此判斷正確。改用 follow-up issue（已收進 spec §9 F-X 之外的 backlog）。
- Q3（resolved）：`registerBuiltinModules()` 在測試環境可直接呼叫，不需先補 stub。codex R1 Q3 已確認：`getPlatformCapabilities()` 對缺 `window.electronAPI` 是 safe fallback、FS / sync registry 寫入都是同步 in-memory。窄 fixture fallback 已從 plan 移除（codex R1 P1.4）。
