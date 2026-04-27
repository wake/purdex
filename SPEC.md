# SPEC — Editor 模組自有資產化 + 開檔體驗強化

> Rev 4 — 吸收 PLAN 第二輪 4 份 codex review（通用 / 攻擊 / 防守 / 體質）共 33 findings，並落實 ABCD 決議：
>
> - **A. PLAN 拆檔**：`PLAN.md` 拆 index + `plans/P1..P5-*.md` × 5（per-phase）。SPEC 維持單檔。
> - **B. P1 加拆 god file**：P1 順手把 `register-modules.tsx`（467 行）拆成 `editor-module.tsx` + `fs-backends.ts` + 根 orchestrator。後續 P3/P5 的 editor 設定 / popup deps 直接落 editor 專檔，不再回灌 god file。
> - **C. PathHint schema v1 minimal**：只廣播 `{schemaVersion, agentId, sessionCode, dir, kind, timestamp}`。`path / pathKind / baseDir / confidence / toolName` 全部砍掉（privacy + YAGNI；未來 adapter 真需 relative path 再升 v2）。
> - **D. fs.search root hard-coded allowlist**：daemon 只允許 `cwd` / `workspace.projectPath` 兩種來源，client 不能任意 absolute path。mandatory excludes 與 client excludes 做 union 不可被覆蓋；`respectGitignore` 改 `*bool` default true；gitignore parse failure 回 4xx 不 fail-open。
>
> 額外吸收 critical：`module-registry.ts` 禁止 import UI component（`disabledComponent` opt-in 由 PaneDefinition 宣告 / renderer 層 wiring）；`tryOpenFile` 改 host-bound backend factory + 錯誤分類只把 `ENOENT` 當 missing；WS PathHint payload 不含完整 `path`（dir-level + basename 從 prune candidate 取）；`useMultiHostEventWs` 改 whitelist 三條 event type；`keepSettings: true` tear-off 不清 persisted cache；daemon dedup key 加 basename 避免 SPA prune 後 5 秒真空；`file-opener-registry` 改 owner-scoped registration。
>
> Rev 3 — 吸收第二輪 codex review `task-mogq6mtw-lz0fm6` 的 minor comments：補 `useMultiHostEventWs` 派發缺口、定義 `resolveWorkspaceForSession` helper 語意、`pruneStale` → `pruneStaleCandidate` 命名與語意對齊、enum 列舉值 const 化 + unknown drop、cwdResolver snapshot 行為、workspace remove `keepSettings` 場景處理。
>
> Rev 2 — 吸收第一輪 codex review `task-mogonr3k-ks81et` 回饋：P4/P5 順序對調（cache infra 先，popup 後）、移除假設不存在的 `pkg/eventbus`、補 Editor 停用後既有 tab 策略、補 race / symlink / 隱私邊界。

## Objective

把目前散落在 `register-modules.tsx` body 的「file-opener 註冊」收編進 Editor 模組定義，讓 Editor 在「panes / openers / settings」三件齊備且皆受 `useModuleEnabledStore` 切換影響；同時藉這次重構修補開檔流程的三個體質問題：

1. 從 terminal link / file tree 開檔開在錯位置（永遠 append 末尾）
2. 「點檔案開啟」相關設定散在 Terminal section
3. 點到不存在的檔案直接報錯，沒有 recovery 路徑

最終目標：**Editor 模組可乾淨啟用 / 停用，停用後所有開檔路徑（含 fallback popup）一致地不出現，已開的 panes 退化為 placeholder 不消失**；以及 **CC / 未來其他 agent 的工作軌跡可作為 path cache 素材，自動把搜尋層級壓在「最近實際工作的子目錄」**。

## Scope（涵蓋）

- SPA `Module` interface 擴充 `fileOpeners` + 可選 `disabledComponent`（pane fallback opt-in 點）
- Editor module definition 收編三個 file opener
- `register-modules.tsx`（god file 467 行）拆 `editor-module.tsx` + `fs-backends.ts` + 根 orchestrator
- `DisabledModulePlaceholder` 通用元件（放 `components/modules/`，預設 fallback；`disabledComponent` 未宣告時自動套用）
- Tab 插入策略改 append-current（複用 browser pattern）
- Link detection 三個 file-path 偵測開關搬到 Editor settings
- daemon agent module 多一條 `PathHint` normalized event channel（沿用既有 `core.HostEvent` 廣播管道）
- CC HookInstaller 寫入 PathHint
- SPA 端 path cache（per-workspace LRU + localStorage，key 版本化 `PATH_CACHE_V1`）
- 檔案不存在時的 popup + 三層 fallback 搜尋
- daemon 出 `fs.search` API（layer 3 用，server-side root allowlist）
- 新增子目錄：`spa/src/lib/file-open/` + `spa/src/lib/agent-ws/` + `spa/src/lib/tab-insert/` + `spa/src/stores/path-cache/` + `spa/src/components/modules/` + `spa/src/components/editor/popups/` + `spa/src/components/settings/editor/`
- 新增 cross-phase regression test：`spa/src/__tests__/editor-open-flow.integration.test.tsx`

## Non-Goals

- Codex / OpenCode 的 HookInstaller 實作（schema 預留 `AgentID`，留 issue）
- 全域檔案搜尋（不在三層內）
- `files` 模組標 `disableable: true`（既有 SR-2 阻擋，留現有 issue）
- Editor / Tab UI 視覺重構
- File-not-found 之外的錯誤類型 popup（permission denied、binary 誤判）
- Symlink canonical 化（follow-up issue）
- Codex apply_patch hook payload 解析（待 [openai/codex#16732](https://github.com/openai/codex/issues/16732) 修復後評估）

## Constraints

- 不破壞 `disableable` 既有語意（Editor 停用 → 重載生效）
- 不增加 daemon 對非 CC agent 的硬依賴
- Path cache 寫入路徑需 idempotent dir-level（避免 hot path 灌爆）
- 全程符合 `feedback_core_vs_module_settings.md`：core store 不收編進 module abstraction
- 不新增 `pkg/eventbus` 抽象 — 沿用既有 `core.HostEvent { Type, Session, Value }` + `agent-ws-dispatch.ts` 派發路徑
- **架構鐵則：`spa/src/lib/` 不准 import `spa/src/components/`**（`module-registry.ts` 不能直接 import `DisabledModulePlaceholder`；fallback 由 renderer 層注入）
- **WS event payload privacy**：PathHint 廣播 payload 不含完整 `path`、不含 `basename`，只 dir 級。完整路徑只在 SPA 端用 `lookup(dir, basename)` 動態組
- **schema versioning**：所有跨進程 schema（WS event JSON、persisted localStorage）必須帶 `schemaVersion`；rehydrate 時 unknown version / unknown enum 值一律 defensive drop
- **localStorage key**：`STORAGE_KEYS.PATH_CACHE_V1`（含版本後綴，未來 v2 不撞 namespace）
- **file-opener-registry owner-scoped**：`registerFileOpener({...spec, ownerModuleId})`；`unregisterByOwner(moduleId)` 只清屬於該 module 的 opener，不可 `clearAll`
- **fs.search root allowlist**：daemon 不接受 client-supplied absolute path 作 root；只允許 `{kind: "session-cwd", sessionCode}` / `{kind: "workspace-projectPath", workspaceId}` 兩種 capability，daemon 端解析成實際 absolute path
- **fs.search mandatory excludes**：`["node_modules", ".git", ".cache", "dist", ".pnpm-store", ".next", ".turbo"]` daemon-side hard-coded，與 client `excludeDirs` 做 union（client 傳 `[]` 不會關掉）
- **fs.search respectGitignore default**：欄位用 `*bool` 或 decode 後判斷 nil → true；gitignore parse 失敗回 4xx + warning，不 fail-open
- **跨 host 操作邊界**：所有 fs / stat 呼叫必須 host-bound（`stat(hostId, path)` 或注入 host-bound backend factory）；`tryOpenFile` 內全程使用 captured `ctx.hostId`，不讀 active host
- **錯誤分類嚴格**：fs `stat` `catch` 只把 `ENOENT` 當 not-found；`auth / network / host-removed` bubble 為原始錯誤，不偽裝成 missing

---

## Phase 切分總覽

| Phase | 範圍 | review 大小 | 依賴 |
|---|---|---|---|
| **P1** | `register-modules.tsx` 拆檔 + `Module.fileOpeners` interface + owner-scoped registry + Editor 收編 + 通用 `DisabledModulePlaceholder` + PaneLayoutRenderer fallback wiring | 中 | 無 |
| **P2** | Tab 插入改 append-current（泛用化 `findInsertTarget`） | 小 | 無 |
| **P3** | Link detection 三個檔案路徑開關搬 Editor settings | 小 | 無 |
| **P4** | daemon `PathHint` v1 minimal channel + dedup-by-(session,dir,basename) + CC HookInstaller + SPA path cache store + auto-cleanup（dispose-aware） | 中 | 無 |
| **P5** | File-not-found popup service（HMR/cancellation safe）+ Layer 1/2/3 整合 + daemon `fs.search` server-side allowlist | 中 | P1 + P4 |

P1-P4 互相獨立可平行 review；P5 用到 P1 的 opener pipeline、P4 的 path cache。

每個 phase 必跑 verification gate（`cd spa && pnpm install && npx vitest run && pnpm run lint && pnpm run build && go test ./...`）才能 PR；每個 PR 走兩輪 codex review（標準 + 三向平行）。

---

# P1 — register-modules god file 拆 + Module.fileOpeners interface + 停用 placeholder

## 動機

兩個結構問題同步處理：

1. **`registerFileOpener()` 三個呼叫目前 inline 在 `registerBuiltinModules()` body**，不在 `editor` 模組定義裡。Editor 標 `disableable: true` 但停用後 opener 仍掛 registry，terminal link 點檔案會產出 `editor` kind 的 PaneContent 而沒有 renderer 對應 — 死路。
2. **`register-modules.tsx` 467 行已是 god file**，且 P3（editor settings）/ P5（popup deps）若不先拆，會持續灌大。P1 本來就動 editor 註冊，是最低成本切點。

同時 `useTabStore` 持久化 tabs，已開的 `editor / image-preview / pdf-preview` 在 reload 後仍存在，renderer 消失會白屏 — 必須有通用 placeholder fallback。

## 變更

### 1. 擴充 `Module` interface（`spa/src/lib/module-registry.ts`）

```ts
interface Module {
  id: string
  name: string
  disableable?: boolean
  panes?: PaneRenderer[]
  views?: View[]
  settings?: SettingsContribution[]
  workspaceConfig?: WorkspaceConfigField[]
  descriptionKey?: string
  fileOpeners?: FileOpenerSpec[]            // ← 新增
  disabledComponent?: React.ComponentType<{ moduleId: string; paneKind: string }>  // ← opt-in；未宣告時 renderer 用通用 placeholder
}
```

`FileOpenerSpec` 採用既有 `FileOpener` 的 shape；命名差異化避免跟 registry 內部 entry 混淆。

**架構鐵則**：`module-registry.ts` 不 import 任何 `spa/src/components/` 內容；`disabledComponent` 由 module owner 在 `register-modules/<module>.tsx` import，registry 只持有 type 引用。

### 2. file-opener-registry 改 owner-scoped

`spa/src/lib/file-opener-registry.ts`：

```ts
interface RegisteredOpener extends FileOpener {
  ownerModuleId: string  // ← 必填
}

function registerFileOpener(spec: FileOpener & { ownerModuleId: string }): void
function unregisterByOwner(ownerModuleId: string): void
function clearAllForHmr(): void  // 只給 HMR / 測試用
```

`register-modules` 重 apply 時呼叫 `unregisterByOwner('editor')` 而非 `clearAll`，避免殺掉外部（譬如未來 plugin host）opener。

### 3. 拆 `register-modules.tsx`（B 決議 (ii)）

新目錄結構：

```
spa/src/lib/register-modules/
├── index.tsx                 # registerBuiltinModules() orchestrator（< 80 行）
├── editor-module.tsx         # editor module definition + fileOpeners + settings sections（含 P3/P5 add-on 點）
├── fs-backends.tsx           # 既有 fs backend 註冊（檔案開檔需要）
└── module-file-openers.ts    # apply 流程：iterate modules、disable filter、unregisterByOwner + register
```

`spa/src/lib/register-modules.tsx`（舊位置）保留為一行 `export * from './register-modules'` 過渡，待 P5 完成可刪除（alpha 階段不留 backward-compat shim）。

### 4. 註冊 apply 流程（`module-file-openers.ts`）

```ts
export function applyModuleFileOpeners(): void {
  for (const m of getModules()) {
    unregisterByOwner(m.id)  // idempotent；HMR re-apply 不殘留
    if (!m.fileOpeners) continue
    if (m.disableable && !useModuleEnabledStore.getState().isEnabled(m.id)) continue
    for (const spec of m.fileOpeners) registerFileOpener({ ...spec, ownerModuleId: m.id })
  }
}
```

### 5. Editor module 收編三個 opener（`editor-module.tsx`）

`registerFileOpener(...)` 三段 inline 呼叫從 `registerBuiltinModules()` body 刪除，搬到 Editor module 定義的 `fileOpeners` 欄位。

### 6. HMR 一致性

`file-opener-registry.ts` 加 `clearAllForHmr()` + module orchestrator 在 `import.meta.hot.dispose` 時呼叫 `clearAllForHmr() + applyModuleFileOpeners()`，沿用 `resetSettingsContributionsForHmr` pattern。

### 7. `DisabledModulePlaceholder` 通用元件（`spa/src/components/modules/DisabledModulePlaceholder.tsx`）

```tsx
export function DisabledModulePlaceholder({ moduleId, paneKind }: { moduleId: string; paneKind: string }) {
  const enable = useModuleEnabledStore((s) => s.setEnabled)
  return (
    <div className="...">
      <h3>{moduleId} 模組目前已停用</h3>
      <p>啟用後重載即可恢復這個 {paneKind} 分頁。</p>
      <button onClick={() => { enable(moduleId, true); /* prompt reload */ }}>
        啟用 {moduleId}
      </button>
    </div>
  )
}
```

放 `components/modules/` 子目錄（防止未來其他 disabled fallback 散在 components/ root）。

### 8. `resolvePaneRenderer` API（`module-registry.ts`）

API 只回 metadata，**不 import component**：

```ts
type RendererResolution =
  | { kind: 'render'; component: React.FC<PaneRendererProps> }
  | { kind: 'disabled'; moduleId: string; paneKind: string; customComponent?: React.ComponentType<{ moduleId: string; paneKind: string }> }
  | { kind: 'unknown'; paneKind: string }

function resolvePaneRenderer(content: PaneContent): RendererResolution
```

### 9. PaneLayoutRenderer 消費（`spa/src/components/PaneLayoutRenderer.tsx:28`）

由 renderer 層做 fallback 選擇（lib → UI 反向依賴杜絕）：

```tsx
const r = resolvePaneRenderer(content)
if (r.kind === 'render') return <r.component {...props} />
if (r.kind === 'disabled') {
  const Cmp = r.customComponent ?? DisabledModulePlaceholder
  return <Cmp moduleId={r.moduleId} paneKind={r.paneKind} />
}
return <UnknownPaneFallback paneKind={r.paneKind} />
```

實際 caller 是 `PaneLayoutRenderer.tsx`（不是 `Pane.tsx`），由通用 review 檢出。

## 不做

- `registerBuiltinTerminalLinks()` 整段 — 維持外部基礎設施（跨多 module 邊界）
- 動態 enable/disable hot toggle — Editor disable 本來就需重載
- 自動關閉已開 panes — 改用 placeholder 保留使用者狀態
- `disabledComponent` 暫時不給除 Editor 外的 module 使用（YAGNI；介面預留）

## 檔案影響

- `spa/src/lib/module-registry.ts` — interface 擴充 + `resolvePaneRenderer` 回 metadata
- `spa/src/lib/file-opener-registry.ts` — `ownerModuleId` 欄位 + `unregisterByOwner` + `clearAllForHmr`
- `spa/src/lib/register-modules/index.tsx` — 新建（orchestrator，< 80 行）
- `spa/src/lib/register-modules/editor-module.tsx` — 新建（Editor module definition + fileOpeners）
- `spa/src/lib/register-modules/fs-backends.tsx` — 新建（fs backend 註冊）
- `spa/src/lib/register-modules/module-file-openers.ts` — 新建（apply 流程）
- `spa/src/lib/register-modules.tsx` — 縮成 `export * from './register-modules'` 過渡 shim
- `spa/src/components/modules/DisabledModulePlaceholder.tsx` — 新建
- `spa/src/components/modules/DisabledModulePlaceholder.test.tsx` — 新建
- `spa/src/components/PaneLayoutRenderer.tsx` — 改用 `resolvePaneRenderer` + 在 disabled case render fallback
- `spa/src/lib/module-registry.test.ts` — `resolvePaneRenderer` 三 case 測試
- `spa/src/lib/file-opener-registry.test.ts` — owner-scoped register/unregister 測試
- `spa/src/lib/register-modules/module-file-openers.test.ts` — apply 流程 + disable filter + HMR 重 apply 測試

## Acceptance Criteria

- [ ] `register-modules.tsx` 從 467 行縮到 < 30 行（過渡 shim），核心邏輯散在 `register-modules/` 子目錄各 < 200 行
- [ ] `module-registry.ts` import graph 不含任何 `spa/src/components/` 路徑（lint rule 或 deps test enforce）
- [ ] Module 定義裡宣告的 `fileOpeners` 在 module enable 時自動註冊、disable 時不註冊
- [ ] `unregisterByOwner('editor')` 不影響其他 owner 的 opener
- [ ] `resolvePaneRenderer` 回 `'render' | 'disabled' | 'unknown'` 三 case，PaneLayoutRenderer 對應 render 正確
- [ ] Editor 停用後 reload，**未持有既有分頁**情境：terminal link 點 `.txt` → `getDefaultOpener` 回 null，不新增分頁
- [ ] Editor 停用後 reload，**持有既有 editor / image-preview / pdf-preview 分頁**情境：分頁仍在 tab bar，pane 區顯示 `DisabledModulePlaceholder`（DOM 實際 render，不只 truthy）
- [ ] FileTreeView 點檔案在 Editor 停用時：`getDefaultOpener` 回 null，silent fail（不 popup，不錯誤）
- [ ] new-tab page 上 Editor 相關 entry（EditorNewTabSection / ManageBuffersNewTabCard）在停用時不顯示
- [ ] Placeholder「啟用模組」按鈕 → 觸發 `useModuleEnabledStore.setEnabled(moduleId, true)` → 顯示重載提示
- [ ] Editor 重新啟用（重載）→ opener 回來、placeholder 替換為實際 renderer
- [ ] HMR re-apply（dispose → import）不殘留舊 opener、不重複 mount placeholder
- [ ] `editor-open-flow.integration.test.tsx` 啟動，最少 1 case：Editor 停用 → terminal link → silent fail
- [ ] 既有所有測試通過、新增 ≥ 6 個 case（owner-scoped register / disable filter / placeholder render / `resolvePaneRenderer` × 3 case / HMR 重 apply）

---

# P2 — Tab 插入改 append-current

## 動機

`useTabStore.openSingletonTab` 找不到既有 tab 時呼叫 `addTab(tab)` 沒帶 `afterTabId`，掉到 `[...state.tabOrder, tab.id]` append 末尾。Browser tab 開啟（`openBrowserTab`）已經做對了：用 `findBrowserInsertTarget` 找同類聚集。

## 變更

### 1. 泛用化 `findBrowserInsertTarget` → `findInsertTarget`

`spa/src/lib/find-browser-insert-target.ts` 改名 `find-insert-target.ts`：

```ts
export function findInsertTarget(
  visibleOrder: string[],
  activeTabId: string,
  tabs: Record<string, Tab>,
  isSameKind: (content: PaneContent) => boolean,
): string | undefined {
  // 既有「找右邊最近同類 tab」邏輯，predicate 化
}
```

### 2. `openBrowserTab` 改用泛用版

傳入 `(c) => c.kind === 'browser'`。

### 3. `useTabStore.openSingletonTab` 簽名擴充

```ts
openSingletonTab: (
  content: PaneContent,
  opts?: { isSameKind?: (content: PaneContent) => boolean },
) => string
```

實作：找不到既有 tab 時，用 active tab id + `isSameKind` predicate 算 `afterTabId`，傳給 `addTab(tab, afterTabId)`。

> **`openSingletonTab` 內不呼叫 `useWorkspaceStore.insertTab(...)`**（通用 review A3 修正）：caller（terminal-link / register-modules / FileTreeView）已自行 insert workspace；store 層只處理 `tabOrder` / active tab；workspace insertion 仍由 caller 負責，避免雙重操作。

### 4. Terminal-link file-path opener 帶 file-kind predicate

```ts
const FILE_KINDS: PaneContent['kind'][] = ['editor', 'image-preview', 'pdf-preview']
openSingletonTab(content, {
  isSameKind: (c) => FILE_KINDS.includes(c.kind),
})
```

### 5. FileTreeView 點擊走相同路徑

## 邊界

- `opts` 不傳 → 行為等同舊版 append 末尾（向後相容）
- `activeTabId === null` → fallback 末尾
- 找不到同類 → active tab 後面（既有 fallback）
- `activeTabId` 不在 `visibleOrder`（如目前在隱藏 workspace）→ fallback 末尾

## 檔案影響

- `spa/src/lib/find-browser-insert-target.ts` → `find-insert-target.ts`（rename + predicate 化）
- `spa/src/lib/open-browser-tab.ts` — 改用泛用版
- `spa/src/stores/useTabStore.ts` — `openSingletonTab` 加 opts
- `spa/src/lib/terminal-link/openers/file-path.ts` — 帶 file-kind predicate
- `spa/src/components/FileTreeView.tsx` — 帶 predicate 開檔
- 對應測試檔同步更新

## Acceptance Criteria（具體 case）

- [ ] **case 1**：active tab 為 file 類，右邊有更多 file 類 → 新分頁插在最右側 file 類**右邊**
- [ ] **case 2**：active tab 為 non-file 類，右邊有 file 類 → 新分頁插在最近 file 類**右邊**
- [ ] **case 3**：workspace 無任何 file 類分頁 → 新分頁插在 active tab **後面**
- [ ] **case 4**：`activeTabId` 不在 `visibleOrder` → 新分頁 append **末尾**
- [ ] FileTreeView 開檔同上 4 case
- [ ] `openSingletonTab` 不帶 `opts` → 行為與重構前一致（既有 ≥ 5 個呼叫者不受影響）
- [ ] Browser tab 行為完全不變（4 case 同樣 verify）

---

# P3 — Link detection 設定遷移

## 動機

`LinkDetectionSection` 目前掛在 Terminal 設定下，4 個開關語意混雜：
- `linkDetectAbsolute` / `linkDetectTilde` / `linkDetectRelativeSlash`：**file path 識別**
- `linkDetectBareFilename`：**bare filename 識別**（一半 file 一半 URL 模糊地帶）

User 決議：file path 三個搬 Editor，bare 留 Terminal。

## 變更

### 1. 拆 `LinkDetectionSection` 成兩個元件

- `LinkDetectionSection`（留 Terminal section 內）：只剩 `linkDetectBareFilename`
- `EditorLinkDetectionSection`（新建，掛 Editor purdex scope，放 `spa/src/components/settings/editor/EditorLinkDetectionSection.tsx`）：三個 file path 開關

### 2. 註冊位置

- `EditorLinkDetectionSection` 透過 Editor module definition 的 `settings` 陣列宣告（`scope: 'purdex'`），由 P1 拆出的 `register-modules/editor-module.tsx` 內掛上
- 自動受 module enable filter 影響；Editor 停用後 → 三個 file path 開關自動消失

### 3. Store 不動

`useUISettingsStore` 仍是這四個 flag 的 source of truth；只有 UI 拆分，無 schema migration、無 default 值變動。

### 4. i18n key 重組

- `settings.terminal.link_detect.absolute.*` → `settings.editor.link_detect.absolute.*`
- `tilde` / `relative_slash` 同上
- `bare` 留原 key

旧 key 不保留 backward compat（alpha 階段不需 migration，per `feedback_no_alpha_migration.md`）。

## 檔案影響

- `spa/src/components/settings/LinkDetectionSection.tsx` — 縮減為 bare 一個
- `spa/src/components/settings/editor/EditorLinkDetectionSection.tsx` — 新建（子目錄）
- `spa/src/lib/register-modules/editor-module.tsx` — settings 陣列加新 section
- `spa/src/locales/zh-TW.json` / `spa/src/locales/en.json` — key 重整（**注意路徑是 `locales/` 不是 `i18n/`**）
- `spa/src/locales/locale-completeness.test.ts` — 一定要跑過
- 對應測試

## Acceptance Criteria

- [ ] Terminal settings 只剩 bare filename 開關
- [ ] Editor purdex settings 出現三個 file path 識別開關
- [ ] Editor 停用 + 重載 → file path 三個開關不在 Settings UI 出現
- [ ] 切換開關 → terminal link 偵測即時生效（既有行為不變）

---

# P4 — PathHint channel + CC HookInstaller + SPA path cache

## 動機

P5 layer 1 需要 path cache 資料來源。CC hook 是初期最直接素材：每次 Read / Write / Edit / NotebookEdit 工具呼叫時把 file_path 的 dirname 推進 cache。設計成 agent-agnostic schema，未來 Codex / OpenCode adapter 可以接同一管道。

依 codex review，**不新增 `pkg/eventbus` 抽象**：沿用既有 `core.HostEvent { Type, Session, Value }` 廣播 + SPA `agent-ws-dispatch.ts` 派發路徑。

## 變更

### 1. daemon `PathHint` schema v1 minimal（`internal/module/agent/path_hint.go` 新建）

依 C 決議與防守 review：只廣播 SPA 真消費的欄位。

```go
type PathHint struct {
    SchemaVersion int       `json:"schemaVersion"` // 固定 1
    AgentID       string    `json:"agentId"`       // "claude-code" | (未來 "codex" | "opencode")
    SessionCode   string    `json:"sessionCode"`   // 6-碼 base36
    Dir           string    `json:"dir"`           // dirname（absolute）
    Kind          string    `json:"kind"`          // "read" | "write" | "edit"
    Timestamp     time.Time `json:"timestamp"`
}
```

**移除**（v1 不放）：`path / pathKind / baseDir / confidence / toolName / hostId`。HostId 由 WS broadcast 路徑帶（既有 `core.HostEvent` 機制），不入 schema。完整 path 不廣播（privacy）。

**Dedup key**：`(SessionCode, Dir, Basename)` — 不放進 payload，僅 daemon 端 ring buffer 用。Basename 進 dedup key 避免「SPA prune 後 5 秒同 dir 再有不同 file 不會 reseed」的真空期。

未來 v2 升級點（不在這次）：relative path adapter（codex apply_patch）需 `pathKind` / `baseDir` 時，整批升 `schemaVersion: 2`，SPA 維持向下相容（unknown drop）。

### 2. CC HookInstaller 寫入 PathHint（拆 task 4.3a / 4.3b）

既有 `pdx hook <event>` CLI 流（per `project_architecture_decisions.md`）已能收 PreToolUse / PostToolUse payload。但**現行 `agentpkg.NormalizedEvent` 不含 `ToolName / ToolInput / HookEventName`** — 只有 `AgentType / Status / RawEventName / Detail`。修法：從 `req.RawEvent` decode + `req.EventName == "PreToolUse" || "PostToolUse"` 判斷，**不依賴 normalized 結構**。

`internal/module/agent/path_hint_extractor.go`（4.3a）：

- 純函式 `ExtractPathHint(rawEvent json.RawMessage, eventName string, agentType string) (PathHint, bool)`
- `Read` / `Write` / `Edit` / `NotebookEdit` 工具 → 從 `tool_input.file_path` 抽 absolute path 的 dirname + basename
- **Symlink**：不 canonicalize，原始路徑直存（follow-up issue 處理）
- **非 absolute**：drop，回 `(_, false)`
- 純函式可獨立 unit test，不需 daemon mock

`handler.go` 的 hook handler 接點呼叫 `ExtractPathHint` → `m.emitPathHint(hint, basename)`（4.3b 整合測試）。

### 3. 廣播：沿用 `core.HostEvent`

```go
// internal/module/agent/handler.go (existing handler 內)
func (m *Module) emitPathHint(hint PathHint, basename string) {
    if m.dedup.Seen(hint.SessionCode, hint.Dir, basename) { return }
    m.dedup.Mark(hint.SessionCode, hint.Dir, basename)
    payload, err := json.Marshal(hint)
    if err != nil { log.Warn(...); return }
    m.core.Events.Broadcast(hint.SessionCode, "agent.path_hint", string(payload))
}
```

`HostEvent.Value` 是 string；JSON 字串塞進去（既有 agent event 慣例）。Payload **不含** basename。

### 4. SPA 端訂閱（擴 `agent-ws-dispatch.ts` + `useMultiHostEventWs` 派發）

**4a. `useMultiHostEventWs` 改 whitelist 三條 event type**（防守 review #4）：

```ts
const AGENT_DISPATCH_TYPES = new Set([
  'agent.status',
  'agent.status.cleared',
  'agent.path_hint',
])

if (AGENT_DISPATCH_TYPES.has(event.type)) {
  dispatchAgentWsEvent(hostId, event)
}
```

**禁用** `event.type.startsWith('agent.')` broad filter — 未來新 `agent.*` event 應顯式加進 whitelist，避免被錯誤歸類為 agent store event。

**4b. 拆 `agent-ws-dispatch.ts` 為子目錄**（體質 review #4）：

```
spa/src/lib/agent-ws/
├── index.ts                    # dispatchAgentWsEvent router
├── status-dispatch.ts          # agent.status / agent.status.cleared 既有邏輯搬入
├── path-hint-dispatch.ts       # agent.path_hint 新邏輯
└── resolve-workspace-id-for-agent-session.ts  # 重命名版 helper（更具體）
```

舊 `spa/src/lib/agent-ws-dispatch.ts` 留 `export * from './agent-ws'` 過渡。

**4c. `path-hint-dispatch.ts`**：

```ts
export function handlePathHintEvent(hostId: string, event: HostEvent): void {
  let hint: PathHint
  try { hint = JSON.parse(event.value) } catch { return }  // malformed drop
  if (hint.schemaVersion !== 1) return                      // unknown version drop
  if (typeof hint.dir !== 'string' || !hint.dir.startsWith('/')) return  // defensive
  const wsId = resolveWorkspaceIdForAgentSession(hostId, hint.sessionCode)
  if (!wsId) return  // standalone session 或無對應 tab → 不寫 cache
  usePathCacheStore.getState().add(hostId, wsId, hint.dir)
}
```

`try/catch` 也包進 `resolveWorkspaceIdForAgentSession` 萬一拋 — dispatch 永不炸 WS pipeline。

**4d. `resolveWorkspaceIdForAgentSession(hostId, sessionCode): string | null`** — 命名比 `resolveWorkspaceForSession` 更具體，避免被誤用為泛用 workspace resolver。

語意（**不取 active 捷徑**，攻擊 review #6 提示）：
1. 掃 `useTabStore.tabs` 找 primary pane 是 `tmux-session` 且 `(hostId, sessionCode)` 匹配的 tab
2. 用 `features/workspace/store.findWorkspaceByTab(tabId)` 取所有命中 workspace（snapshot）
3. **若多個 workspace 同時命中**（罕見：tear-off 中過渡狀態）→ 回 `null`，drop 該 hint，避免寫到「使用者剛切過去的 workspace」
4. 唯一命中 → 回該 workspace id；無命中 → `null`
5. **standalone session（無 tab 對應）或 cross-host 來源不明 → `null`**

### 5. SPA path cache store（`spa/src/stores/path-cache/usePathCacheStore.ts` 新建）

```ts
interface PathCacheState {
  // key = `${hostId}:${workspaceId}`
  dirsByScope: Record<string, string[]>  // LRU; head = most recent
  add: (hostId: string, workspaceId: string, dir: string) => void
  lookup: (hostId: string, workspaceId: string, basename: string) => string[]
  pruneStaleCandidate: (hostId: string, workspaceId: string, candidatePath: string) => void
  clearScope: (hostId: string, workspaceId: string) => void
  clearHost: (hostId: string) => void
}
```

- LRU 容量 50 條 per scope，dedup 同 dir 移到 head
- `add()` 內建 normalization（防守 review #7）：
  - 非 absolute path（不以 `/` 開頭）→ silently reject（log dev warn）
  - trim trailing slash（`/foo/bar/` → `/foo/bar`）
  - canonical `.` / `..`（用 `path-browserify.normalize`）
- `lookup(basename)` 純字串組合：對每個 cached `dir` 回傳 `path.join(dir, basename)`，**不做 stat**（stat 留 P5 caller）
- `pruneStaleCandidate(candidatePath)`：取 `dirname(candidatePath)`，從該 scope LRU 移除這個 dir entry（P5 stat 失敗時呼叫）
- 持久化：`storage: purdexStorage`（與其他 store 一致）+ localStorage key `STORAGE_KEYS.PATH_CACHE_V1`
- `partialize` 只存 `{ dirsByScope }`，不持久化 actions
- `onRehydrateStorage` 防禦：`dirsByScope` 不是 object 或內含非 string array → reset 為 `{}`

**Workspace lifecycle（防守 review #11 修正）**：

- `keepSettings: true` 的 tear-off / merge → **不清 persisted localStorage cache**，只清本 window in-memory state（避免影響其他同 origin window 的同 workspace cache）
- `keepSettings: undefined` 或 `false`（真正 delete workspace）→ `clearScope`，清 in-memory + persisted
- `useHostStore` host remove → `clearHost`，清該 host 所有 scope（in-memory + persisted）

**不進 sync schema**（per `feedback_skeleton_convergence.md` + B3 codex review）。

### 6. Auto-cleanup subscriber 拆檔（`spa/src/stores/path-cache/auto-cleanup.ts`）

獨立檔案，不讓 store 本體 import `useWorkspaceStore` / `useHostStore`（避免循環依賴 + 測試污染）。

```ts
export function attachPathCacheAutoCleanup(): () => void {
  // 等 workspace store hydrate 完才 attach（防 hydration race）
  let unsub1: (() => void) | undefined
  let unsub2: (() => void) | undefined
  const start = () => {
    unsub1 = useWorkspaceStore.subscribe((state, prevState) => {
      const removed = computeRemovedWorkspaceIds(prevState, state)
      for (const { hostId, wsId, keepSettings } of removed) {
        usePathCacheStore.getState().clearScope(hostId, wsId, { keepPersisted: keepSettings === true })
      }
    })
    unsub2 = useHostStore.subscribe((state, prevState) => {
      const removed = computeRemovedHostIds(prevState, state)
      for (const hostId of removed) usePathCacheStore.getState().clearHost(hostId)
    })
  }
  if (useWorkspaceStore.persist.hasHydrated()) start()
  else useWorkspaceStore.persist.onFinishHydration(start)
  return () => { unsub1?.(); unsub2?.() }
}
```

回傳 dispose function（攻擊 review #7、體質 review #3）：測試 `afterEach` 必須呼叫；HMR `import.meta.hot.dispose` 也呼叫。

### 7. STORAGE_KEYS 加新 key

`spa/src/lib/storage/keys.ts` 加 `PATH_CACHE_V1: 'purdex-path-cache-v1'`（含版本後綴，未來 v2 不撞 namespace）。

## 邊界

- 只 CC adapter 上線；Codex / OpenCode 的 HookInstaller 留 issue 追蹤
- daemon ring buffer 不持久化（重啟丟失 OK，hooks 會 reseed）
- SPA localStorage 重啟保留 — 但 P5 caller 會 stat 驗證後才用（C2）
- 廣播 payload 不含使用者輸入內容（純路徑 metadata）
- 不寫硬碟 daemon log（per 隱私 requirement）

## 檔案影響

- `internal/module/agent/path_hint.go` — v1 minimal schema + ring buffer (in-memory, max 200 per host) + Go const for `Kind` 列舉值
- `internal/module/agent/path_hint_extractor.go` — 純函式 CC RawEvent → PathHint（不依賴 normalized）
- `internal/module/agent/path_hint_extractor_test.go` — 純函式 unit test（4.3a；不需 daemon mock）
- `internal/module/agent/path_hint_test.go` — emitPathHint integration（4.3b；dedup-by-(session,dir,basename) / non-absolute drop / broadcast payload 不含 path/basename）
- `internal/module/agent/handler.go` — 在既有 hook handler 接點呼叫 `ExtractPathHint` + `emitPathHint`，從 `req.RawEvent` decode（非 normalized）
- `spa/src/stores/path-cache/usePathCacheStore.ts` — LRU store + add normalization
- `spa/src/stores/path-cache/usePathCacheStore.test.ts` — LRU + normalization + duplicate move-to-head + overflow eviction + onRehydrateStorage defensive
- `spa/src/stores/path-cache/auto-cleanup.ts` — workspace/host subscribe + dispose function
- `spa/src/stores/path-cache/auto-cleanup.test.ts` — repeat attach 不重複清理、dispose 後不清理、hydration race
- `spa/src/lib/storage/keys.ts` — 加 `PATH_CACHE_V1`
- `spa/src/lib/agent-ws/index.ts` — router 入口
- `spa/src/lib/agent-ws/status-dispatch.ts` — 既有 status 邏輯搬入
- `spa/src/lib/agent-ws/path-hint-dispatch.ts` — 新邏輯 + try/catch + schemaVersion check
- `spa/src/lib/agent-ws/path-hint-dispatch.test.ts` — schemaVersion mismatch drop / malformed JSON drop / resolver throw 不炸 dispatcher
- `spa/src/lib/agent-ws/resolve-workspace-id-for-agent-session.ts` — helper（重命名版）
- `spa/src/lib/agent-ws/resolve-workspace-id-for-agent-session.test.ts` — 唯一命中 / 多重命中 drop / 無命中 null
- `spa/src/lib/agent-ws-dispatch.ts` — 過渡 shim `export * from './agent-ws'`
- `spa/src/hooks/useMultiHostEventWs.ts` — whitelist 三條 event type
- `spa/src/types/agent-events.ts` — `PathHint` v1 type 定義（與 daemon 對齊；TS const 化 `Kind` 列舉值，unknown drop）

## Acceptance Criteria

- [ ] CC `Read` tool 觸發 → daemon 從 `req.RawEvent` decode tool_input.file_path → 廣播 `core.HostEvent { Type: "agent.path_hint", Session: <code>, Value: <json> }`
- [ ] payload 為 v1 minimal：`{schemaVersion: 1, agentId: "claude-code", sessionCode, dir, kind, timestamp}`，**不含 `path / basename / pathKind / baseDir / confidence / toolName`**
- [ ] daemon dedup-by-`(SessionCode, Dir, Basename)`：同 (session,dir,basename) 5 秒內重複 → dedup 為 1 筆；同 dir 不同 basename → 都廣播（避免 SPA prune 真空期）
- [ ] 非 absolute path → drop，不廣播，metric 記錄
- [ ] `path_hint_extractor_test.go` 純函式測試 ≥ 4 case：Read / Write / Edit / NotebookEdit + 1 case non-absolute drop
- [ ] **`useMultiHostEventWs` whitelist 三條**：`agent.status` / `agent.status.cleared` / `agent.path_hint` 才 dispatch；`agent.foo` 等其他 type 不 dispatch（regression test）
- [ ] `dispatchAgentWsEvent` 收到 `agent.path_hint` → `resolveWorkspaceIdForAgentSession(hostId, sessionCode)` 回 wsId → `usePathCacheStore.add` 對應 scope
- [ ] **`schemaVersion !== 1` → defensive drop**，不寫 cache、不報錯
- [ ] **malformed JSON → defensive drop**（try/catch in dispatch）
- [ ] **resolver 拋例外 → dispatch 不炸**（regression test：mock resolver throw）
- [ ] **`resolveWorkspaceIdForAgentSession` 唯一命中規則**：唯一 → 回；多重命中 → null（不取 active 捷徑）；無命中 → null
- [ ] **`usePathCacheStore.add` normalization**：非 absolute reject、trim trailing slash、`./..` canonical
- [ ] LRU 容量 50；溢出最舊淘汰；同 dir 重複移到 head（含 add 0..49 → touch d0 → add d50 → expect d1 evicted, d0 still in head 區的 regression）
- [ ] 切換 workspace → cache 隔離不互相污染（不同 scope key）
- [ ] localStorage `purdex-path-cache-v1` 重啟保留前次 session 的 cache
- [ ] **persist hydration race**：path cache 先 hydrate、workspace 後 hydrate → `attachPathCacheAutoCleanup` 不會以空 workspace set 作 baseline 誤刪 cache（`onFinishHydration` gate）
- [ ] `onRehydrateStorage` defensive：localStorage 內容 malformed → reset `dirsByScope = {}` 不炸
- [ ] **workspace remove `keepSettings: false`**（真 delete）→ 對應 scope 清 in-memory + persisted
- [ ] **workspace remove `keepSettings: true`**（tear-off）→ 對應 scope 只清 in-memory，**保留 persisted**（避免影響其他 window）
- [ ] host remove → 整 host 所有 scope 清空（in-memory + persisted）
- [ ] `attachPathCacheAutoCleanup` 回 dispose function；重複呼叫 attach 不重複 subscribe；dispose 後不再 cleanup
- [ ] CC 以外 agent 沒 HookInstaller → daemon 不報錯、無 PathHint 推送（架構支援未來擴展）

---

# P5 — File-not-found popup + Layer 1/2/3 fallback

## 動機

點 terminal link 或 FileTreeView 內檔案時，若路徑不存在，目前 silent fail 或丟原始錯誤。我們要：
- **Layer 1**（path cache，由 P4 提供）：自動跑、命中前 stat 驗證
- **Layer 2**（current agent cwd）+ **Layer 3**（workspace path）：popup 確認後才搜尋
- **使用者開關**：A = popup 機制總開關（預設 on）；B = 自動跑 layer 1（預設 on）

## 流程

```
點檔案路徑（已 normalized 為 absolute path，含 tilde expansion）
  └→ stat 檢查存在性
      ├→ 存在 → 開啟（走 P1 + P2 流程）
      └→ 不存在
          ├→ 開關 A off → 拋既有錯誤、不彈 popup
          └→ 開關 A on
              ├→ 開關 B on → 跑 layer 1（path cache lookup + 對每個 candidate stat）
              │   ├→ stat 失敗 → 從 cache 移除該 entry，繼續看其他 candidate
              │   ├→ stat 成功 命中 1 個 → 直接開
              │   ├→ stat 成功 命中多個 → popup 列選擇（mode = layer1-multi）
              │   └→ 0 命中 → popup 詢問展開搜尋（mode = ask-expand）
              └→ 開關 B off → 直接 popup（mode = ask-expand）
```

## 變更

### 1. File-not-found popup service（拆 5.7a / 5.7b / 5.8）

**拆分原則**（攻擊 review #5、體質 review #11、通用 review C3）：

- **5.7a**：popup mount service（singleton lifecycle，獨立可測）
- **5.7b**：terminal-link / FileTreeView 接 `tryOpenFile`，但 expand button 暫 disabled
- **5.8**：Layer 2/3 expand UI + `fs.search` 呼叫 + cancellation

**5.7a 服務 `spa/src/lib/file-open/file-not-found-popup-service.tsx`**（命名比 `popup-mount.tsx` 更精確；體質 review #11）：

```ts
let root: ReactDOMRoot | undefined
let host: HTMLElement | undefined
let currentToken: AbortController | undefined

export function showFileNotFoundPopup(spec: PopupSpec): AbortController {
  hideFileNotFoundPopup()  // singleton
  host = document.createElement('div')
  document.body.appendChild(host)
  root = createRoot(host)
  currentToken = new AbortController()
  root.render(<FileNotFoundPopup spec={spec} signal={currentToken.signal} onClose={hideFileNotFoundPopup} />)
  return currentToken
}

export function hideFileNotFoundPopup(): void {
  currentToken?.abort()
  root?.unmount(); host?.remove()
  root = undefined; host = undefined; currentToken = undefined
}

if (import.meta.hot) import.meta.hot.dispose(hideFileNotFoundPopup)
```

**Cancellation token**：`onExpand` `await` 完成時必須先檢查 `signal.aborted` — close 後 resolve 不可重新 mount popup（攻擊 review #5）。

**HMR dispose**：避免 hot reload 後 module-level `root/host` 殘留 zombie root。

**FileNotFoundPopup 元件 `spa/src/components/editor/popups/FileNotFoundPopup.tsx`**：

- 顯示原始路徑、basename、為什麼找不到
- 區段 1：layer 1 命中清單（若有，可點選開啟）
- 區段 2：「展開搜尋」主 CTA（防守 review #4）— 文案明確顯示「搜尋目前 session（cwd: …）」+「搜尋 workspace（projectPath: …）」，不像錯誤訊息次要按鈕
- **Snapshot 行為**：popup 開啟時把當下 cache candidates 凍結；後續 path cache 變動不影響當前 popup（除非按 refresh）
- ESC / 取消按鈕關閉、focus trap

### 2. 新流程 `tryOpenFile()`（`spa/src/lib/file-open/open-file.ts`）

**Service factory 模式**（防守 review #6）：caller 不需要知道 backend / popup / openInTab 細節。

```ts
interface OpenFileService {
  tryOpenFile(file: FileInfo, source: FileSource, ctx: OpenFileContext): Promise<void>
}

interface OpenFileDeps {
  fsBackendFactory: (hostId: string) => FsBackend  // host-bound（攻擊 critical C5）
  popupController: { show: typeof showFileNotFoundPopup; hide: typeof hideFileNotFoundPopup }
  tabOpener: (file: FileInfo, source: FileSource, ctx: OpenFileContext) => void
}

interface OpenFileContext {
  hostId: string
  sourceWorkspaceId: string  // captured at click time, not read on each await
  sessionCode?: string       // 觸發來源 session（layer 2 用）
  cwdResolver?: () => Promise<string | null>  // layer 2 動態解析
}

export function createOpenFileService(deps: OpenFileDeps): OpenFileService { /* ... */ }
```

**`tryOpenFile` 實作**（攻擊 critical C5）：

```ts
async function tryOpenFile(file, source, ctx) {
  const fs = deps.fsBackendFactory(ctx.hostId)  // host-bound，後續 await 全用同一 backend

  // 錯誤分類嚴格：只 ENOENT / 404 當 missing；auth/network bubble
  const stat = await fs.stat(file.path).catch((err) => {
    if (isNotFoundError(err)) return null
    throw err  // bubble auth/network/host-removed
  })
  if (stat) return deps.tabOpener(file, source, ctx)

  if (!useUISettingsStore.getState().popupOnMissingFile) throw new FileNotFoundError(file.path)

  if (useUISettingsStore.getState().autoSearchLayer1) {
    const cached = usePathCacheStore.getState().lookup(ctx.hostId, ctx.sourceWorkspaceId, file.basename)
    const verified: string[] = []
    for (const candidate of cached) {
      const ok = await fs.stat(candidate).catch((err) => {
        if (isNotFoundError(err)) return null
        throw err
      })
      if (ok) verified.push(candidate)
      else usePathCacheStore.getState().pruneStaleCandidate(ctx.hostId, ctx.sourceWorkspaceId, candidate)
    }
    if (verified.length === 1) return deps.tabOpener({ ...file, path: verified[0] }, source, ctx)
    if (verified.length > 1) return deps.popupController.show({ mode: 'layer1-multi', hits: verified, file, ctx })
  }
  return deps.popupController.show({ mode: 'ask-expand', file, ctx })
}
```

`isNotFoundError`：daemon HTTP 404 / Go `os.ErrNotExist` 對應的 SPA error code。

### 3. Daemon `fs.search` API（拆 5.1a engine + 5.1b handler）

**5.1a `internal/module/fs/search_engine.go`**：純函式 `Search(ctx, request) (response, error)`，可獨立 unit test。

**5.1b `internal/module/fs/search_handler.go`**：HTTP decode/validate/route，配合 `fs/module.go` 既有 handler pattern：

```go
func (m *FsModule) handleSearch(w http.ResponseWriter, r *http.Request) { /* ... */ }
// route: mux.HandleFunc("POST /api/fs/search", m.handleSearch)
```

**API spec**（D 決議 + 攻擊 critical C4）：

```
POST /api/fs/search
Body: {
  mode: "basename",                    // ← envelope（防守 review #8 留擴展性）
  query: { basename: string },         // ← 對應 mode
  roots: [
    // ✦ 不接受 client-supplied absolute path ✦
    { kind: "session-cwd", sessionCode: string }
    | { kind: "workspace-projectPath", workspaceId: string }
  ],
  limits?: {
    maxResults?: int,    // default 50, hard cap 200
    maxDepth?:   int,    // default 8
    timeoutMs?:  int,    // default 5000
  },
  filters?: {
    excludeDirs?:           []string,  // ← union with mandatory
    excludeBasenameGlobs?:  []string,  // ← union with mandatory
    respectGitignore?:      *bool,     // ← nil → true（默認）
  }
}
Resp: { matches: []{ path, modTime, sizeBytes, root }, partial: bool, warnings: []string }
```

**設計要點**：

- **Root resolution**（D 決議；capability-only，不接受 absolute path）：
  - `{ kind: "session-cwd", sessionCode: string }` → daemon 透過 `core.Sessions.Get(sessionCode).Cwd` 解析
  - `{ kind: "workspace-projectPath", workspaceId: string }` → daemon 透過內部 workspace registry / 由 SPA 在 WS handshake 時告知的 mapping 解析（無此 mapping 時拒絕）
  - 兩種解析結果都再經 system-path validate，拒絕 `/`、`/etc`、`/sys`、`$HOME` 直系、`/Users` 直系
  - **不接受** `{ kind: "absolute", path }` 或任何 SPA-supplied absolute path（攻擊 critical C4 + D 決議）；schema reject
- **Mandatory excludes union**（攻擊 review #10）：daemon-side hard-coded `["node_modules", ".git", ".cache", "dist", ".pnpm-store", ".next", ".turbo"]` ∪ client `excludeDirs`。空 array 不能關掉。
- **Mandatory basename excludes**：daemon-side `["*.lock", "*.log"]` ∪ client。
- **`respectGitignore` 默認 true**（攻擊 review #10）：欄位 `*bool`，nil → true。
- **gitignore parse failure 不 fail-open**（攻擊 review #11）：回 4xx + warning，不靜默忽略。
- exclude 規則 directory-aware：
  - `excludeDirs`：directory basename 比對（prune 整個子樹）
  - `excludeBasenameGlobs`：檔案 basename glob 比對
- **Symlink**：預設不 follow（避免迴圈）；root 內 symlink 到 root 外的目錄會被忽略
- **Hidden dirs**（`.` 開頭）：預設搜尋（與既有 `fs.list` 行為一致）

### 4. SPA 端 `fsSearchByBasename` helper（`spa/src/lib/file-open/fs-search.ts`）

封裝對應 host 的 `POST {hostBaseUrl}/api/fs/search` 呼叫；caller 提供 `roots` capability（不是 absolute path）；回傳結果以 modTime desc 排序。

### 5. 整合進 Terminal link / FileTreeView 開檔路徑（5.7b）

- `terminal-link/openers/file-path.ts` 既有路徑做 path normalization（tilde expansion / cwd resolve），結果丟給 `tryOpenFile()`
- `FileTreeView.tsx` 點擊改用 `tryOpenFile()`
- 兩處都不再自己 `getDefaultOpener + openSingletonTab`（職責線：file-opener-registry 只負責 file type → pane content factory；open-file 負責 stat/cache/popup decision）

### 6. Workspace projectPath 取得修正

`features/workspace/store.ts` 的 `Workspace` schema **沒有 `config` 欄位，只有 `moduleConfig`**（通用 review D1）。SPA 端取 projectPath：

```ts
const projectPath = ws?.moduleConfig?.files?.projectPath  // ← 不是 ws.config?.projectPath
```

### 7. 兩個新 settings（`useUISettingsStore`）

```ts
popupOnMissingFile: boolean     // default true
autoSearchLayer1:    boolean    // default true
```

UI 放 Editor purdex scope 新區塊 `EditorOpenBehaviorSection`（與 P3 並列），檔案落 `spa/src/components/settings/editor/EditorOpenBehaviorSection.tsx`。

## 邊界

- **`tryOpenFile` 接收的 `file.path` 必須是 absolute** — 上游（terminal link / FileTreeView）負責 normalization
- **Symlink 不 canonicalize**（per C3 決策 (a)）— 原始路徑直存 cache 與 popup 顯示
- Layer 2 `sessionCode` 缺失（FileTreeView 等非 session 來源）→ popup 內 layer 2 按鈕 disabled
- Layer 3 `projectPath` 未設定 → popup 內 layer 3 按鈕 disabled + tooltip 提示
- Search 結果視覺：相對 home 路徑簡寫（`~/...`）
- Popup 不阻塞其他互動（modal 但 ESC 可關）
- Editor 模組停用（P1）→ 開檔路徑根本走不到 popup（opener 已不存在）
- **fs.search 不接受 client absolute path roots** — 只接 `session-cwd` / `workspace-projectPath` capability
- **fs.search system path validate**：`/`、`/etc`、`/sys`、`/Users` 直系、`$HOME` 直系拒絕（即使透過 capability 解析後也擋）
- **tryOpenFile 全程 host-bound**：`fsBackendFactory(ctx.hostId)` 取一次，後續所有 stat 用同一 backend；workspace/host 切換不影響進行中的 open flow
- **tryOpenFile 錯誤分類**：只 ENOENT/404 視作 not-found；auth/network/host-removed 必須 bubble 為原始錯誤，不偽裝成 missing file

## 檔案影響

- `spa/src/components/editor/popups/FileNotFoundPopup.tsx` — 新建（子目錄）
- `spa/src/components/editor/popups/FileNotFoundPopup.test.tsx` — 新建
- `spa/src/components/settings/editor/EditorOpenBehaviorSection.tsx` — 新建（子目錄；兩個開關）
- `spa/src/lib/file-open/open-file.ts` — service factory + tryOpenFile（子目錄）
- `spa/src/lib/file-open/open-file.test.ts` — 5 種 mode case + host switch race + auth error 不誤判 missing
- `spa/src/lib/file-open/fs-search.ts` — search helper
- `spa/src/lib/file-open/file-not-found-popup-service.tsx` — singleton mount service + HMR dispose + AbortController
- `spa/src/lib/file-open/file-not-found-popup-service.test.tsx` — close 後 promise resolve 不 re-mount / HMR dispose 不殘留 root
- `spa/src/lib/terminal-link/openers/file-path.ts` — 改走 `tryOpenFile`（保留既有 normalization）
- `spa/src/components/FileTreeView.tsx` — 改走 `tryOpenFile`，移除既有 `getDefaultOpener + openSingletonTab` 直接呼叫
- `spa/src/stores/useUISettingsStore.ts` — 加兩個 flag
- `spa/src/stores/path-cache/usePathCacheStore.ts` — 加 `pruneStaleCandidate(hostId, wsId, candidatePath)`（P4 已定義 API，P5 呼叫處）
- `internal/module/fs/search_engine.go` — 新建純函式 engine（5.1a）
- `internal/module/fs/search_handler.go` — 新建 HTTP handler（5.1b）
- `internal/module/fs/search_engine_test.go` — depth/exclude/glob/timeout/symlink loop/gitignore 測試
- `internal/module/fs/search_handler_test.go` — root capability resolve / system path 拒絕 / mandatory excludes union / respectGitignore default true / parse failure 4xx
- `internal/module/fs/module.go` — route 註冊 `m.handleSearch`
- `spa/src/lib/register-modules/editor-module.tsx` — Editor settings 加 `EditorOpenBehaviorSection`
- `spa/src/__tests__/editor-open-flow.integration.test.tsx` — 跨 phase 整合（補滿）

## Acceptance Criteria

### Open flow
- [ ] 點存在的檔案 → 直接開（既有行為不變）
- [ ] 點不存在的檔案 + 開關 A off → 拋 `FileNotFoundError`，不彈 popup
- [ ] 開關 A on + B on + cache 命中 1 個（stat 通過） → 直接開
- [ ] 開關 A on + B on + cache 命中多個 → popup `layer1-multi` 模式，列出全部已驗證 candidates
- [ ] 開關 A on + B on + cache 0 命中 → popup `ask-expand` 模式
- [ ] 開關 A on + B off → popup `ask-expand` 模式
- [ ] cache candidate stat 失敗 → 呼叫 `pruneStaleCandidate(candidate)` 從 cache 移除，不顯示在 popup 中
- [ ] popup 開啟中，path cache 收到新 hint → 當前 popup candidates 不變（snapshot）

### Workspace context
- [ ] 點檔案 → workspace 切換 → popup 仍顯示原 workspace 的 layer 2/3 結果（captured context）
- [ ] `tryOpenFile` 內所有 await 點之後讀 cache → 仍用 `ctx.sourceWorkspaceId` 而非當前 active

### Layer 2/3
- [ ] popup 內按「展開搜尋」 → 顯示 layer 2 + layer 3 結果
- [ ] `sessionCode` 缺失 → layer 2 按鈕 disabled
- [ ] workspace `projectPath` 未設定 → layer 3 按鈕 disabled

### Host switching safety（攻擊 critical C5）
- [ ] tryOpenFile 內 stat 後 active host 切換 → 後續 await stat 仍打 ctx.hostId（不會打到新 active）
- [ ] tryOpenFile 收到 401/403 auth error → throw（不偽裝 missing），popup 不開
- [ ] tryOpenFile 收到 network error / host removed → throw，popup 不開
- [ ] tryOpenFile 只在 ENOENT / 404 才視作 missing 走 popup 流程

### Popup lifecycle（攻擊 important）
- [ ] popup 開啟中按 ESC → currentToken.abort()
- [ ] onExpand 內 await fs.search 回來時 signal.aborted → 不 re-mount popup
- [ ] HMR re-import file-not-found-popup-service.tsx → 舊 root unmount + host removed，document 內只有一個 popup host
- [ ] hideFileNotFoundPopup 重複呼叫不報錯（idempotent）

### daemon fs.search
- [ ] `maxResults` 截斷在指定上限（測 1 / 50 / 200）
- [ ] `timeoutMs` 觸發 → 回傳 `partial: true` + 已找到的
- [ ] `excludeDirs` 命中 → 整個子樹 prune（不下鑽）
- [ ] `excludeBasenameGlobs` 命中 → 個別檔案 skip
- [ ] **client 傳 `excludeDirs: []` → mandatory excludes 仍生效**（不被覆蓋）
- [ ] **respectGitignore omitted → 默認 true**（Go *bool nil → true）
- [ ] **gitignore parse 失敗 → 4xx + warning**，不 fail-open
- [ ] root 內 `.gitignore` 存在 + `respectGitignore: true` → ignored 的不在結果裡
- [ ] symlink loop（A → B → A）→ 不無限遞迴
- [ ] `maxDepth: 8` → 第 9 層不掃
- [ ] **body 接受 `roots: [{kind:"session-cwd", sessionCode}]`** → daemon resolve 成 absolute path
- [ ] **body 接受 `roots: [{kind:"workspace-projectPath", workspaceId}]`** → daemon 透過內部 mapping resolve 成 absolute path（無 mapping 拒絕）
- [ ] **system path 被拒絕**：`/`、`/etc`、`/sys`、`/Users` 直系、`$HOME` 直系（return 4xx）
- [ ] **client 傳 `roots: [{kind:"absolute", path:"..."}]` → schema reject**（無此 kind）

### Module disable interaction
- [ ] Editor 停用 → terminal link 點檔案 → `getDefaultOpener` 回 null，根本不會走進 `tryOpenFile`
- [ ] Editor 停用 → FileTreeView 點檔案 → 同上

### Cross-phase integration（`editor-open-flow.integration.test.tsx`）
- [ ] Editor 啟用 + cache 命中 → 直接開
- [ ] Editor 停用 → silent fail
- [ ] missing + popup → expand → fs.search → 命中 → 開
- [ ] tear-off keepSettings:true 不清 persisted cache

---

## 全 Phase 共通

### Code style

- TypeScript：既有專案規範（`spa/eslint.config.js`），無破例
- Go：`gofmt` + `go vet`，error path explicit
- 命名：opener / hint / cache 等概念跟既有 lib 一致
- 測試命名：`<file>.test.ts(x)` / `<file>_test.go`，不另立 `__tests__` 子目錄（除非檔案多到該分）
- localStorage key 一律走 `STORAGE_KEYS` SSoT（`spa/src/lib/storage/keys.ts`），key 名含版本後綴（如 `PATH_CACHE_V1`）
- **commit message lowercase**（通用 review C2）：`feat(spa): file tree opens files clustered with file-kind tabs`，不寫 `feat(spa): FileTreeView clusters file-kind tabs`
- **檔案組織**：`lib/` 與 `components/` 根目錄已過寬，本次新檔一律走子目錄：
  - `spa/src/lib/file-open/` — open-file / fs-search / file-not-found-popup-service
  - `spa/src/lib/agent-ws/` — index / status-dispatch / path-hint-dispatch / resolve-workspace-id-for-agent-session
  - `spa/src/lib/tab-insert/` — find-insert-target
  - `spa/src/lib/register-modules/` — index / editor-module / fs-backends / module-file-openers
  - `spa/src/stores/path-cache/` — usePathCacheStore / auto-cleanup
  - `spa/src/components/modules/` — DisabledModulePlaceholder
  - `spa/src/components/editor/popups/` — FileNotFoundPopup
  - `spa/src/components/settings/editor/` — EditorLinkDetectionSection / EditorOpenBehaviorSection

### Testing strategy

- **P1**：module-driven opener 註冊單元測（含 disable filter）+ HMR dispose 行為測 + `DisabledModulePlaceholder` render 測 + pane renderer fallback 測
- **P2**：`findInsertTarget` 純函式測（4 case predicate）+ `openSingletonTab` integration 測 + `openBrowserTab` regression 4 case
- **P3**：i18n key smoke test + section 渲染測（Editor enable/disable 切換）
- **P4**：
  - daemon path_hint extractor 各 tool name case + dedup window + non-absolute drop + broadcast format 測
  - SPA usePathCacheStore LRU + 跨 workspace 隔離 + localStorage 持久化 + `clearScope` / `clearHost` 觸發 測
  - `dispatchAgentWsEvent` `agent.path_hint` case 測
  - 端到端：mock CC hook payload → daemon → broadcast → SPA store assert
- **P5**：
  - daemon `fs.search` 單元測（gitignore / depth / timeout / excludeDirs / excludeBasenameGlobs / symlink loop）
  - SPA `tryOpenFile` 各分支測（5 種 mode + workspace switch race + stat prune）
  - popup 元件 a11y 基本測（ESC 關閉、focus trap）+ snapshot 行為測

### Boundaries — 永遠

- `feedback_core_vs_module_settings.md`：core store（theme/UI/layout/i18n）不收編進 module abstraction
- `feedback_no_alpha_migration.md`：alpha 階段不寫 persist migration
- `feedback_subagent_cwd_enforcement.md`：subagent 每個 Bash 強制 `cd <worktree-path> &&` 前綴
- `feedback_skill_review_vs_codex.md`：每個 PR 進 review 走 codex 跨模型獨立第二意見（不只 self-review）
- `feedback_codex_sandbox_no_install.md`：Codex sandbox 無網路；SPA 任務主 Claude 必須手動 pnpm install + vitest/lint/build 驗證
- 所有 commit / PR：「TDD：先寫測試再實作」+「每個 task 獨立 commit」
- localStorage 一律走 `STORAGE_KEYS` SSoT
- **lib → UI 反向依賴永禁**：`spa/src/lib/` 不准 import `spa/src/components/`（類型引用例外）
- **WS payload privacy**：路徑相關 payload 永遠 dir-level，不含完整 path、不含 basename
- **fs API 永遠 host-bound**：跨 host 操作必須走 host-id-aware backend factory，不取 active host 捷徑

### Boundaries — 先問

- 任何修改要動 `module-registry.ts` 既有 module shape 的 breaking change
- daemon `fs.search` 想加 `include_hidden=false` flag（與既有 `fs.list` 不一致）
- 任何「同步 / Sync 模組」的接點

### Boundaries — 永遠不

- 不為 P4 提前實作 Codex / OpenCode HookInstaller（schema 預留即可）
- 不在 SPA 直接讀 daemon log file（保持 daemon API 邊界）
- 不把 Editor 重造為 plugin host（`feedback_skeleton_convergence.md`：避免「把 working code 變 data」）
- 不把 path cache 寫進 sync schema（per-host 本地資產，不跨機同步）
- 不對 symlink 做 canonical 化（per C3 決策 (a)；follow-up issue 處理）
- 不新增 `pkg/eventbus` 抽象（沿用 `core.HostEvent`）
- 不接受 client-supplied absolute path 作 fs.search root（攻擊 critical C4）
- `module-registry.ts` 不准 import 任何 component（攻擊 / 體質 critical）
- `useMultiHostEventWs` 不准用 `event.type.startsWith('agent.')` broad filter（防守 important）
- PathHint v1 schema 不再加 `path / pathKind / baseDir / confidence / toolName`（C 決議；要加 → 升 v2）

---

## 已知 Follow-up（不在這次 PR）

- `files` 模組標 `disableable: true` 待 PR 3 補 workspace-scope filter（既有 SR-2）
- Codex HookInstaller adapter（待 [openai/codex#16732](https://github.com/openai/codex/issues/16732) 修復後評估）
- OpenCode HookInstaller adapter（架構就緒後新議題）
- `fs.search` 加 fuzzy / content search（mode envelope 已留位置：`mode: "fuzzy" | "content"`）
- Symlink canonical 化策略 — `EvalSymlinks` vs hybrid dual-key
- `fs.search` 是否提供 `include_hidden=false` 開關
- daemon ring buffer 持久化（重啟後 PathHint 暖開機）
- PathHint v2 schema：relative path / baseDir / confidence / toolName 等欄位（待 Codex apply_patch adapter 真正需要時批次升）
- `disabledComponent` opt-in 給其他 disableable module 使用（YAGNI；Editor 之外都用通用 placeholder）

---

## 驗證

每個 phase 進入 review 前必須跑：

```bash
cd spa && pnpm install
cd spa && npx vitest run
cd spa && pnpm run lint
cd spa && pnpm run build
go test ./...
```

通過才能 PR。Codex sandbox 無法 install，主 Claude 必須手動跑驗證（per `feedback_codex_sandbox_no_install.md`）。
