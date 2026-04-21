# HSR PR-5：Editor `homePath` First Module User — Implementation Plan

> 日期：2026-04-22（v3 post-codex-review Round 2 task-mo8z6ppx-pj07yk）
> 狀態：Ready for Implementation（依賴 PR-2 + PR-3 + PR-4 全部 land）
> 主 spec：`2026-04-21-settings-contribution-registry-design.md`
> 決策對齊：3b（舊 API 在 PR-5 merge 時發 deprecation）+ 1c（走 adapter 的新 registry 路徑）
> PR 系列：PR-1 ✅ / PR-2 / PR-3 / PR-4 / **PR-5（本文件）**
> v3.1 收斂：Round 1 Finding 4 → v2 修 LinkContext API；Round 2 Finding 4 partial → v3 改以 `SessionPaneContent` 為 workspaceId 來源 + 獨立 plumbing commit；Round 3 HIGH（duplicate localId `homePath`）+ MED（`tabIds` 應為 `tabs`）→ v3.1 修 localId 拆為 `workspaceHomePath` / `hostHomePath` + plumbing 改用現成 `findWorkspaceByTab()`

---

## 1. 範圍

**HSR 第一個 module 用例**。Editor module 透過新 `ModuleDefinition.settings` 宣告兩個 contribution：
- `{ scope: 'workspace', localId: 'workspaceHomePath' }` — workspace 手動 home（Layer 1）
- `{ scope: 'host', localId: 'hostHomePath' }` — host 手動 home（Layer 2）

**注意（v3.1 修正，Round 3 HIGH）**：兩個 contribution **不得共用同一 `localId: 'homePath'`** — PR-1 registry 規定 `id = ${moduleId}.${localId}` 全域唯一，同 module 內兩個 localId 相同會在 `dispatchSettingsContributions()` collision check throw（`assertValidSettingsContribution` + seen-id set）。即使 scope 不同，id 仍會撞。因此 workspace / host 兩層各用獨立 localId。

Tilde path opener（`spa/src/lib/terminal-link/openers/file-path.ts`）的 `~/...` 分支改為**層疊 resolve**：workspace settings → host settings → `fetchPaneHome()`（Layer 3，PR #530 既有）→ 無則 fallthrough 原 rawPath 行為。

**關鍵前置（Round 1 Finding 4 + Round 2 partial 閉環）**：

- `LinkContext`（`spa/src/lib/terminal-link/types.ts:29`）目前只有 `hostId?: string` + `sessionCode?: string`，無 `workspaceId`。本 PR 擴 `LinkContext` 加 `workspaceId?: string`（link 來源 terminal 所屬的 workspace，**不是** `getActiveWorkspaceId()`）
- **Plumbing 來源（v3 確定）**：`workspaceId` 必須由 link 來源 pane 帶上來。可靠的入口是 `spa/src/components/SessionPaneContent.tsx:56-61`（現況已由 `useTabStore` 查到 `tabId`）；該層再查 `useWorkspaceStore` 找「包含此 tabId 的 workspace」得 `workspaceId`，以 prop 傳入 `TerminalView`。`TerminalView` 不自己找 workspace（避免在多層組件各自用 `activeWorkspaceId` 猜，造成 inactive pane / split / multi-workspace 不一致）
- `TerminalView` 接 `workspaceId?: string` prop，`linkContext` 構造改為 `useMemo(() => ({ hostId, sessionCode, workspaceId }), [hostId, sessionCode, workspaceId])`
- Resolver 用 `ctx.workspaceId` 查 `useWorkspaceSettingsStore`，確保 multi-workspace 情境下 override 來源正確；`ctx.workspaceId === undefined`（standalone pane）時跳過 workspace 層

PR-5 merge 時同步：
- **#540 解決**：三層 store `get()` 回傳 frozen shallow clone（防止 consumer 以 in-place mutation 繞過 persist / sync）
- **決策 3b**：對舊 `ModuleDefinition.globalConfig` / `workspaceConfig` 欄位加 JSDoc `@deprecated` + `register-modules.tsx` 在 register pass 偵測到 **非 `files` module** 使用舊欄位時 console.warn（files 保留無警告，由 files owner 後續 refactor 處理）

PR-5 結束時：
- Editor 在 Workspace settings 頁與 Host 設定頁各顯示一個 "Home path" 區塊（文字輸入 + clear 按鈕）
- `~/foo.ts` 點擊先用 workspace settings 解析，否則 host，否則 pane shell fallback
- HSR 架構透過「真實 module + 真實 UI + 真實行為」完整閉環驗證
- 舊 `globalConfig` / `workspaceConfig` 發 deprecation 警告（保留可運作）

---

## 2. 檔案清單

### 新增
- `spa/src/modules/editor/EditorHomePathWorkspaceSection.tsx`（或 `spa/src/features/editor/settings/` — 路徑以實作時確認）
  - Props: `{ ctx: SettingsContext }`（scope === 'workspace' 守護）
  - 讀 `useWorkspaceSettingsStore.get(ctx.workspaceId, 'editor')?.homePath`
  - 一個輸入框（placeholder「Auto-detect from pane shell」）+ Clear button
  - `onChange` 呼叫 `useWorkspaceSettingsStore.set(ctx.workspaceId, 'editor', { homePath: value })` 或 `delete` patch
- `spa/src/modules/editor/EditorHomePathHostSection.tsx`
  - 類似但 scope === 'host'；store 用 `useHostSettingsStore`
- `spa/src/modules/editor/EditorHomePathWorkspaceSection.test.tsx`
- `spa/src/modules/editor/EditorHomePathHostSection.test.tsx`
- `spa/src/lib/editor-home-resolver.ts`（抽出 tilde 層疊 resolve 邏輯）
  - `resolveEditorHomePath(ctx: { hostId, workspaceId?, sessionCode, signal }, deps): Promise<string | null>`
  - 依序嘗試：workspace → host → `fetchPaneHome` → null
- `spa/src/lib/editor-home-resolver.test.ts`

### 修改
- `spa/src/lib/terminal-link/types.ts`（Finding 4 — LinkContext 擴充）
  - `interface LinkContext` 加 `workspaceId?: string`（optional — legacy callsite 不給時 resolver 直接 fallthrough 到 host 層）
  - JSDoc 標示：「workspaceId 應為 link 來源 terminal 所屬的 workspace id，**不是** active workspace id；由 `SessionPaneContent` 用 pane-to-tab-to-workspace 查詢得出並透過 `TerminalView.workspaceId` prop 注入」
- `spa/src/components/TerminalView.tsx`
  - Props 加 `workspaceId?: string`
  - `linkContext` 構造改為 `useMemo(() => ({ hostId, sessionCode, workspaceId }), [hostId, sessionCode, workspaceId])`
  - **不自行查 workspace**（依 v3 plumbing 約定由父層注入；若 prop undefined 表示 standalone pane，linkContext.workspaceId 為 undefined）
- `spa/src/components/SessionPaneContent.tsx`（v3 新增 — Finding 4 plumbing 真來源）
  - 現況已用 `useTabStore` 查出 `tabId`（line 56-61）
  - 新增：用 `useWorkspaceStore` 的現成 helper `findWorkspaceByTab(tabId)` 查 workspace（見 `spa/src/features/workspace/store.ts:136-138`；實際 workspace.tabs 欄位為 `tabs: string[]`，非 `tabIds`）：`const workspaceId = useWorkspaceStore((s) => tabId ? s.findWorkspaceByTab(tabId)?.id : undefined) ?? undefined`
  - 把 `workspaceId` 以 prop 傳入 `<TerminalView workspaceId={workspaceId} ... />`
  - `<ConversationView>` 分支（stream mode）若未來也吃 linkContext（現況不吃）則同樣由此注入；PR-5 scope 內不動 ConversationView
- `spa/src/lib/register-modules.tsx`
  - Editor `registerModule({ id: 'editor', ..., settings: [{ localId: 'workspaceHomePath', scope: 'workspace', order: 0, labelKey: 'editor.settings.home_path.workspace', component: EditorHomePathWorkspaceSection }, { localId: 'hostHomePath', scope: 'host', order: 0, labelKey: 'editor.settings.home_path.host', component: EditorHomePathHostSection }] })` — 兩個 contribution 的完整 id 為 `editor.workspaceHomePath` 與 `editor.hostHomePath`，無 collision
  - 加舊 API deprecation warning（僅對非 `files` 使用者）—— 在 register pass 中偵測 `globalConfig.length > 0` 或 `workspaceConfig.length > 0` 且 moduleId !== 'files' → `console.warn('[module] ${id} uses deprecated globalConfig/workspaceConfig; migrate to settings: [{ scope, localId }]')`
  - Warn de-dupe：module-scope `Set<string>` 記 `${moduleId}:${scope}` 已 warn 過的 key，避免 HMR 重跑反覆 warn
- `spa/src/lib/terminal-link/openers/file-path.ts`
  - 將 `~/...` 分支的 `fetchPaneHome` 直呼改為 `resolveEditorHomePath(ctx, deps)` 呼叫（內部依序嘗試 workspace → host → fetchPaneHome）
  - `ctx` 傳入 resolver 的完整 LinkContext（含 `workspaceId`）；resolver 內部直接 `import` 三層 store，不走 deps injection（SPA 內部不跨 boundary）
  - Inactive workspace / undefined workspaceId 時，resolver 自動 skip workspace 層
- `spa/src/stores/useGlobalSettingsStore.ts`（#540）
  - `get(moduleId)` 回 `Object.freeze({ ...internal })`（shallow clone + freeze）
- `spa/src/stores/useHostSettingsStore.ts`（#540）
  - `get(hostId, moduleId)` 同上
- `spa/src/stores/useWorkspaceSettingsStore.ts`（#540）
  - `get(wsId, moduleId)` 同上
- `spa/src/stores/useGlobalSettingsStore.test.ts` / `useHostSettingsStore.test.ts` / `useWorkspaceSettingsStore.test.ts`
  - 新增測試：`get` 回傳物件 frozen（`Object.isFrozen()` 為 true）
  - 新增測試：mutate returned object throws / 不影響 store 內部
- `spa/src/lib/module-registry.ts`
  - `globalConfig?` / `workspaceConfig?` 加 `/** @deprecated Use settings: [{ scope: 'purdex' | 'workspace', localId }] instead. */`
- `spa/src/locales/en.json` / `zh-TW.json`
  - 新增 `editor.settings.home_path.workspace` / `.host` / `.description` / `.clear` 等 key

### 不動
- `fetchPaneHome` 介面（opener 內部改實作即可）
- daemon 端 `/api/sessions/{code}/home`（PR #530 已完成）
- 其他 terminal-link opener / matcher
- PR-1 / PR-2 / PR-3 / PR-4 shell
- 其他 module 的 `globalConfig` / `workspaceConfig`（`files.projectPath` 照常運作，但不觸發 deprecation warning）

---

## 3. Test Case Matrix

### 3.1 `EditorHomePathWorkspaceSection.test.tsx`

| 類別 | 測試項 | 預期 |
|---|---|---|
| Render | 無 value → input placeholder 顯示 | 通過 |
| Render | 已存 value → input 顯示該值 | 通過 |
| Write | 輸入 `/Users/foo` 後 blur / enter → `useWorkspaceSettingsStore.get(wsId, 'editor').homePath === '/Users/foo'` | 通過 |
| Clear | 按 clear → store `homePath` 變 undefined | 通過 |
| Ctx guard | 傳 `ctx.scope !== 'workspace'` → render null 或 throw（實作擇一） | 通過 |

### 3.2 `EditorHomePathHostSection.test.tsx`

對 `useHostSettingsStore` 做相同矩陣。

### 3.3 `editor-home-resolver.test.ts`

| 類別 | 測試項 | 預期 |
|---|---|---|
| Priority | `ctx.workspaceId` 有值 + workspace store 有值 + host 有值 + fetchPaneHome 有值 → 回 workspace 值 | 通過 |
| Priority | workspace 空 + host 有值 → 回 host 值 | 通過 |
| Priority | workspace / host 皆空 + fetchPaneHome OK → 回 fetchPaneHome 值 | 通過 |
| Priority | 三層皆空（fetchPaneHome reject / 回空字串）→ 回 null | 通過 |
| Priority | workspace 值為空字串（使用者 clear）→ 視為無值，fallthrough 到 host | 通過 |
| **Workspace skip — undefined workspaceId** | `ctx.workspaceId === undefined`（link 來源 pane 非屬任一 workspace）→ resolver 跳過 workspace 層，直接進 host → fetchPaneHome | 通過 |
| **Workspace skip — wrong workspaceId** | workspace store 有 `wsA.homePath`，但 `ctx.workspaceId === 'wsB'` → resolver 讀 `wsB.homePath`（undefined）→ fallthrough 到 host，**不**讀 `wsA` 的值（即 multi-workspace inactive pane 正確性） | **關鍵 — Finding 4 回歸** |
| Abort | `signal.aborted` → fetchPaneHome 不呼叫、回 null | 通過 |
| Absolute only | workspace / host 值非以 `/` 開頭 → 視為無效，fallthrough | 通過 |

### 3.4 `openers/file-path.test.ts` 擴充

| 類別 | 測試項 | 預期 |
|---|---|---|
| Tilde + workspace override | 點 `~/foo.ts`；`ctx.workspaceId='wsA'`、workspace store `wsA.editor.homePath='/Users/x'` → 開啟 `/Users/x/foo.ts` | 通過 |
| Tilde + host override | 點 `~/foo.ts`；workspace 空、host 有 `homePath='/home/y'` → 開啟 `/home/y/foo.ts` | 通過 |
| Tilde fallback | 三層皆空、`fetchPaneHome` 回 `/Users/z` → 開啟 `/Users/z/foo.ts` | 通過（PR #530 回歸） |
| Tilde all fail | 三層皆空、`fetchPaneHome` reject → 開啟 raw `~/foo.ts`（blank buffer；既有行為） | 通過 |
| **Multi-workspace** | 點 `~/foo.ts`；active workspace = `wsA`（有 `/Users/x`），但 link 來源 pane 屬 `wsB`（無 value）；`ctx.workspaceId='wsB'` → resolver 不讀 `wsA`，進 host 層或 fetchPaneHome | **關鍵 — Finding 4 回歸** |
| **Standalone pane** | 點 `~/foo.ts`；pane 非屬任一 workspace；`ctx.workspaceId === undefined` → skip workspace 層，走 host / fetchPaneHome | 通過 |

### 3.5 Store immutability (#540)

| 類別 | 測試項 | 預期 |
|---|---|---|
| Frozen | `useWorkspaceSettingsStore.get(ws, mod)` 回傳 `Object.isFrozen()` 為 true | 通過 |
| Mutation guard | `store.get(ws, mod).foo = 'x'` 在 strict mode 拋（或在 loose mode 靜默忽略）；**store 內部狀態不變** | 通過 |
| Patch 仍可 | `store.set(ws, mod, { foo: 'x' })` 正常運作（set 不受 frozen 影響） | 通過 |

### 3.6a LinkContext plumbing（commit 2 — v3 新增）

測試分兩處：`TerminalView.test.tsx` 與 `SessionPaneContent.test.tsx`（或合併為 `SessionPaneContent.test.tsx` 一處）。

| 類別 | 測試項 | 預期 |
|---|---|---|
| TerminalView prop | 傳 `workspaceId='wsA'` → `linkContext.workspaceId === 'wsA'`（可透過 mock `useTerminal` 觀察 `linkContext` 參數） | 通過 |
| TerminalView prop | 不傳 workspaceId → `linkContext.workspaceId === undefined` | 通過 |
| SessionPaneContent 查詢 | pane 在 workspace `wsA` 的 tab `tX` 下 → `<TerminalView>` 收到 `workspaceId='wsA'` | 通過 |
| SessionPaneContent standalone | pane 在 tab `tY`，`tY` 不屬任何 workspace → `<TerminalView>` 收到 `workspaceId={undefined}` | 通過 |
| Multi-workspace isolation | `workspaces: [{ id: 'wsA', tabs: ['tX'], ... }, { id: 'wsB', tabs: ['tY'], ... }]`；pane 在 `tY` → `findWorkspaceByTab('tY')?.id === 'wsB'`，TerminalView 收到 `workspaceId='wsB'`，非 active 的 `wsA` | **關鍵 — Finding 4 plumbing 閉環** |

### 3.6 Deprecation warning (#3b)

| 類別 | 測試項 | 預期 |
|---|---|---|
| Warn | 註冊一個 fake module `{ id: 'fake', globalConfig: [{...}], ... }` → `console.warn` 被呼叫，訊息含 `fake` 與 `deprecated` | 通過 |
| 不 warn | 註冊 `files` module（已使用 `workspaceConfig`）→ 無 warn | 通過 |
| 不 warn | 註冊使用新 `settings` 的 module → 無 warn | 通過 |

### 3.7 視覺回歸（手動）

- `cd spa && pnpm dev`
- 打開 workspace settings → 看到 Editor Home Path (Workspace) 區塊；輸入 `/tmp/foo` → 開 terminal 點 `~/x` → Editor 開 `/tmp/foo/x`
- 打開 host 設定 → 看到 Editor Home Path (Host) 區塊；清空 workspace 的值，輸入 host 值 `/home/bar` → 點 `~/y` → 開 `/home/bar/y`
- 三層全空 → 點 `~/z` → 依 pane shell fallback 行為

---

## 4. 實作順序（TDD）

1. **紅**：寫 §3.5 store immutability 測試
2. **綠**：三層 store `get()` 加 freeze + shallow clone
3. **紅**：寫 §3.3 resolver 測試
4. **綠**：建 `editor-home-resolver.ts`
5. **紅**：寫 §3.4 opener 擴充測試
6. **綠**：改 `file-path.ts` `~/` 分支用 resolver
7. **紅**：寫 §3.1 / §3.2 section 測試
8. **綠**：建兩個 section component
9. **紅**：寫 §3.6 deprecation warning 測試
10. **綠**：`register-modules.tsx` 加 warning + Editor `settings` 宣告 + i18n keys
11. **驗證**：`cd spa && pnpm exec vitest run` / `pnpm run lint` / `pnpm run build`
12. **手動**：§3.7 視覺回歸

---

## 5. 驗收條件

- [ ] §3.1–§3.6 測試全綠
- [ ] `cd spa && pnpm exec vitest run` 全綠
- [ ] `cd spa && pnpm run lint` 全綠
- [ ] `cd spa && pnpm run build` 全綠
- [ ] §3.7 手動驗：workspace / host / fallback 三路徑皆可用
- [ ] `gh issue close #540`
- [ ] Codex 兩輪 review 無 critical / P1 未修項
- [ ] Kickoff 記憶更新：HSR PR-5 完成，全部 PR 落地

---

## 6. 風險

| 風險 | 發生可能 | 緩解 |
|---|---|---|
| freeze 後既有 consumer mutate frozen object 造成既有 bug 顯性化 | 中 | PR-1 後 store 無 production consumer；§3.5 測試保證 immutability；若發現 consumer 依賴 mutation，改為 `set()` callsite |
| resolver 的 abort signal 傳遞遺漏 | 中 | §3.3 abort 測試覆蓋；resolver 需支援 `AbortSignal` 貫穿 |
| workspace / host 的 `homePath` 值為相對路徑或含 `..` 的 traversal | 中 | §3.3 「absolute only」測試；值非 `/` 開頭 → 視為無效；相對值不納入（由 PR #531 tracking，不在 PR-5 scope 擴展） |
| Editor section UI 與 WorkspaceSettingsPage 既有佈局（ModuleConfigSection 的 files.projectPath）並排視覺不一致 | 低 | 採同樣 `<section>` + `<h3>` 樣式；§3.7 手動驗 |
| Deprecation warning 吵到 dev 輸出（每次 HMR 重跑都 warn 一次）| 中 | warn 加 de-dupe（per (moduleId, scope) 只 warn 一次）—— 用 `Set` in module scope 記已 warn 過的 id |
| `files` module 被豁免 warn 的判定太寬鬆（未來若 `files` 也該遷但仍保留就永遠沒 warn）| 中 | 豁免清單明確為 `['files']`；未來若決定搬，就從豁免清單移除並處理；清單在 `register-modules.tsx` 內顯式定義 |

---

## 7. 超出 PR-5 範圍（明確不做）

- 舊 `globalConfig` / `workspaceConfig` 欄位**完全移除**（還要等所有 consumer 遷完）
- `files.projectPath` 搬到新 `settings`（決策 2b — files owner 後續 refactor）
- `ModuleConfigSection.tsx` 移除（同上）
- `useModuleConfigStore` 移除
- `~/../foo` traversal 防護（遺留 issue #531，不在 PR-5 scope）
- Darwin `ps -E` 空白切割（#532，不在 PR-5 scope）
- Editor 其他偏好（字型 / theme）走新 registry — 保留舊實作
- PR-5 後續 clean-up PR（全面移除舊 API）

---

## 8. Commit 規劃

每 commit 綠。

1. **`refactor(spa): settings stores return frozen immutable snapshot (#540)`**
   - 三層 store `get` + test
2. **`feat(spa): LinkContext carries link-source workspaceId (plumbing)`**
   - `terminal-link/types.ts` 加 `workspaceId?`
   - `TerminalView.tsx` 加 prop + linkContext 注入
   - `SessionPaneContent.tsx` 查 workspaceId 傳 prop
   - 單測：`TerminalView.test.tsx` / `SessionPaneContent.test.tsx` 驗 workspaceId 正確傳遞（active ws / standalone pane / multi-workspace inactive pane 三路徑）
   - 此 commit 後 `linkContext.workspaceId` 在所有開啟 terminal 的情境都填對；因 opener 還沒消費該欄位，行為與 main 一致
3. **`feat(spa): editor-home-resolver with layered workspace → host → pane-shell resolve`**
   - `editor-home-resolver.ts` + test（含 wrong-workspaceId 回歸）
4. **`feat(spa): terminal-link opener uses editor-home-resolver for tilde paths`**
   - `file-path.ts` 改 resolver 呼叫 + test 擴充（含 Multi-workspace / Standalone pane 測試）
   - 此 commit 消費 commit 2 的 `workspaceId` plumbing + commit 3 的 resolver
5. **`feat(spa): Editor module declares homePath contributions (workspace + host)`**
   - 兩個 section component + test + `register-modules.tsx` Editor `settings` 宣告 + i18n
6. **`refactor(spa): deprecate ModuleDefinition.globalConfig / workspaceConfig`**
   - `module-registry.ts` JSDoc + `register-modules.tsx` warn + test

共 6 commits。每 commit 獨立可綠：
- commit 1：只動 store.get 實作與測試
- commit 2：plumbing 純傳值，無邏輯消費者（`workspaceId` 進 linkContext 但 opener 還沒讀）— 行為與 main 一致
- commit 3：resolver 純邏輯 + 測試，無 callsite 接進
- commit 4：opener 切到 resolver，同時消費 commit 2 plumbing + commit 3 resolver
- commit 5：加 section + module 宣告，UI 出現但不影響其他流程
- commit 6：deprecation warning，純 runtime 觀察，不改行為

---

## 9. 與其他 PR 的關聯

- **依賴**：
  - PR-1（已 merged）— 三層 store + registry 基礎
  - **PR-2** — dispatch-flushed adapter pattern；`@internal` boundary（本 PR 的新 registry 消費遵守同樣 boundary）
  - **PR-3** — WorkspaceSettingsPage shell 能渲染 workspace contribution（Editor `workspace.homePath` section 的 render 環境）
  - **PR-4** — HostPage shell + host route contract 鬆綁（Editor `host.homePath` section 走動態 subPage localId）
- **rebase 衝突點**：無（PR-5 動 `terminal-link/types.ts` + `TerminalView.tsx` + `register-modules.tsx` Editor module 段，與前面 PR 不重疊）
- **被依賴**：後續「全面移除舊 `globalConfig` / `workspaceConfig`」的獨立 refactor PR（要等至少 `files` 也遷完）
- **順風解決**：#540（三層 store immutable snapshot）/ 決策 3b（deprecation warning 落地 + de-dupe 機制）
