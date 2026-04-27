# SPEC — Editor 模組自有資產化 + 開檔體驗強化

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

- SPA `Module` interface 擴充 `fileOpeners`
- Editor module definition 收編三個 file opener
- `DisabledModulePlaceholder` 元件（停用時的 pane fallback）
- Tab 插入策略改 append-current（複用 browser pattern）
- Link detection 三個 file-path 偵測開關搬到 Editor settings
- daemon agent module 多一條 `PathHint` normalized event channel（沿用既有 `core.HostEvent` 廣播管道）
- CC HookInstaller 寫入 PathHint
- SPA 端 path cache（per-workspace LRU + localStorage）
- 檔案不存在時的 popup + 三層 fallback 搜尋
- daemon 出 `fs.search` API（layer 3 用）

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

---

## Phase 切分總覽

| Phase | 範圍 | review 大小 | 依賴 |
|---|---|---|---|
| **P1** | `Module.fileOpeners` interface + Editor 收編 + enable filter + `DisabledModulePlaceholder` | 小～中 | 無 |
| **P2** | Tab 插入改 append-current（泛用化 `findInsertTarget`） | 小 | 無 |
| **P3** | Link detection 三個檔案路徑開關搬 Editor settings | 小 | 無 |
| **P4** | daemon `PathHint` channel（沿用 `core.HostEvent`） + CC HookInstaller + SPA path cache store | 中 | 無 |
| **P5** | File-not-found popup + Layer 1/2/3 整合 + daemon `fs.search` | 中 | P1 + P4 |

P1-P4 互相獨立可平行 review；P5 用到 P1 的 opener pipeline、P4 的 path cache。

---

# P1 — Module.fileOpeners interface + Editor 收編 + 停用 placeholder

## 動機

`registerFileOpener()` 三個呼叫目前在 `registerBuiltinModules()` body 直接 inline，**不在 `editor` 模組定義裡**。Editor 標 `disableable: true` 但停用後 opener 仍掛 registry，terminal link 點檔案會產出 `editor` kind 的 PaneContent 而沒有 renderer 對應 — 死路。同時 `useTabStore` 持久化 tabs，已開的 `editor / image-preview / pdf-preview` 在 reload 後仍存在，renderer 消失會白屏 — 必須有 placeholder。

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
  fileOpeners?: FileOpenerSpec[]   // ← 新增
}
```

`FileOpenerSpec` 採用既有 `FileOpener` 的 shape；命名差異化避免跟 registry 內部 entry 混淆。

### 2. 註冊器走過所有 module 收集 fileOpeners（`registerBuiltinModules()` 尾段）

```ts
for (const m of getModules()) {
  if (!m.fileOpeners) continue
  if (m.disableable && !useModuleEnabledStore.getState().isEnabled(m.id)) continue
  for (const opener of m.fileOpeners) registerFileOpener(opener)
}
```

### 3. Editor module 收編三個 opener

`registerFileOpener(...)` 三段 inline 呼叫從 `registerBuiltinModules()` body 刪除，搬到 Editor module 定義的 `fileOpeners` 欄位。

### 4. HMR 一致性

`file-opener-registry.ts` 加 `resetFileOpenerRegistryForHmr()`，沿用 `resetSettingsContributionsForHmr` pattern，`import.meta.hot.dispose` 時呼叫。

### 5. `DisabledModulePlaceholder` 元件

新檔案 `spa/src/components/DisabledModulePlaceholder.tsx`：

```tsx
export function DisabledModulePlaceholder({ moduleId, paneKind }: { ... }) {
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

### 6. PaneRenderer fallback wiring

`module-registry.ts` 的 pane renderer 解析改成：

```ts
function resolvePaneRenderer(content: PaneContent): React.FC<PaneRendererProps> {
  const [moduleId, paneEntry] = findPaneOwner(content.kind)
  if (!moduleId) return UnknownPaneFallback  // 既有
  const owner = getModules().find((m) => m.id === moduleId)
  if (owner?.disableable && !useModuleEnabledStore.getState().isEnabled(moduleId)) {
    return () => <DisabledModulePlaceholder moduleId={moduleId} paneKind={content.kind} />
  }
  return paneEntry.component
}
```

## 不做

- `registerBuiltinTerminalLinks()` 整段 — 維持外部基礎設施（跨多 module 邊界）
- 動態 enable/disable hot toggle — Editor disable 本來就需重載
- 自動關閉已開 panes — 改用 placeholder 保留使用者狀態

## 檔案影響

- `spa/src/lib/module-registry.ts` — interface 擴充 + renderer fallback
- `spa/src/lib/file-opener-registry.ts` — 加 HMR reset helper
- `spa/src/lib/register-modules.tsx` — Editor module 收編、移除 inline `registerFileOpener` × 3
- `spa/src/components/DisabledModulePlaceholder.tsx` — 新建
- `spa/src/components/__tests__/DisabledModulePlaceholder.test.tsx` — 新建
- `spa/src/lib/module-registry.test.ts` — module-driven 註冊 + disable filter 測試

## Acceptance Criteria

- [ ] Module 定義裡宣告的 `fileOpeners` 在 module enable 時自動註冊、disable 時不註冊
- [ ] Editor 停用後 reload，**未持有既有分頁**情境：terminal link 點 `.txt` → `getDefaultOpener` 回 null，不新增分頁
- [ ] Editor 停用後 reload，**持有既有 editor / image-preview / pdf-preview 分頁**情境：分頁仍在 tab bar，pane 區顯示 `DisabledModulePlaceholder`
- [ ] FileTreeView 點檔案在 Editor 停用時：`getDefaultOpener` 回 null，silent fail（不 popup，不錯誤）
- [ ] new-tab page 上 Editor 相關 entry（EditorNewTabSection / ManageBuffersNewTabCard）在停用時不顯示
- [ ] Placeholder「啟用模組」按鈕 → 觸發 `useModuleEnabledStore.setEnabled(moduleId, true)` → 顯示重載提示
- [ ] Editor 重新啟用（重載）→ opener 回來、placeholder 替換為實際 renderer
- [ ] HMR 切換 enable 不殘留舊 opener
- [ ] 既有所有測試通過、新增 ≥ 4 個 case（fileOpener 註冊 / disable filter / placeholder render / pane renderer fallback）

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

實作：找不到既有 tab 時，用 active tab id + `isSameKind` predicate 算 `afterTabId`，傳給 `addTab(tab, afterTabId)`；同步呼叫 `useWorkspaceStore.insertTab(tab.id, wsId, afterTabId)`。

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
- `EditorLinkDetectionSection`（新建，掛 Editor purdex scope）：三個 file path 開關

### 2. 註冊位置

- `EditorLinkDetectionSection` 透過 Editor module definition 的 `settings` 陣列宣告（`scope: 'purdex'`），自動受 module enable filter 影響
- Editor 停用後 → 三個 file path 開關自動消失

### 3. Store 不動

`useUISettingsStore` 仍是這四個 flag 的 source of truth；只有 UI 拆分，無 schema migration、無 default 值變動。

### 4. i18n key 重組

- `settings.terminal.link_detect.absolute.*` → `settings.editor.link_detect.absolute.*`
- `tilde` / `relative_slash` 同上
- `bare` 留原 key

旧 key 不保留 backward compat（alpha 階段不需 migration，per `feedback_no_alpha_migration.md`）。

## 檔案影響

- `spa/src/components/settings/LinkDetectionSection.tsx` — 縮減為 bare 一個
- `spa/src/components/settings/EditorLinkDetectionSection.tsx` — 新建
- `spa/src/lib/register-modules.tsx` — Editor settings 陣列加新 section
- `spa/src/i18n/zh-TW.json` / `en.json` — key 重整
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

### 1. daemon `PathHint` schema（`internal/module/agent/path_hint.go` 新建）

```go
type PathHint struct {
    AgentID     string    `json:"agentId"`     // "claude-code" | "codex" | "opencode"
    HostID      string    `json:"hostId"`      // 由 broadcast 端填，schema 不依賴
    SessionCode string    `json:"sessionCode"` // 6-碼 base36
    Kind        string    `json:"kind"`        // "read" | "write" | "edit" | "unknown"
    Path        string    `json:"path"`        // file path（若可用）
    Dir         string    `json:"dir"`         // dirname（若 Path 是 absolute；relative 時為空）
    PathKind    string    `json:"pathKind"`    // "absolute" | "relative" | "unknown"
    BaseDir     string    `json:"baseDir,omitempty"`    // relative path 的 resolve base（CWD / workspace root）
    Confidence  string    `json:"confidence"`  // "high" | "medium" | "low"
    ToolName    string    `json:"toolName"`    // 來源工具名（Read / Write / apply_patch / ...）
    Timestamp   time.Time `json:"timestamp"`
}
```

設計要點：
- **CC adapter** 寫 `PathKind: "absolute"` + `Confidence: "high"`（CC tool args 一定 absolute file_path）
- **未來 Codex apply_patch adapter** 可寫 `PathKind: "relative"` + `BaseDir: <session cwd>` + `Confidence: "medium"`（patch header 含相對路徑）
- **未來 OpenCode adapter** `PathKind: "absolute"` + `Confidence: "high"`（output.args.filePath 直接給）
- 消費端（SPA path cache）只取 `PathKind === "absolute"` 的 `Dir`；其他先收進 daemon ring buffer 待未來規範化

### 2. CC HookInstaller 寫入 PathHint

既有 `pdx hook <event>` CLI 流（per `project_architecture_decisions.md`）已能收 PreToolUse / PostToolUse payload。新增 `path_hint_extractor.go`：

- `Read` / `Write` / `Edit` / `NotebookEdit` 工具 → 從 `tool_input.file_path` 抽 dirname，dedup window 5 秒（同 SessionCode + Dir）
- **Symlink**：不 canonicalize，原始路徑直存（C3 決策：(a) 起步，follow-up issue 處理）
- **Tilde / relative**：CC 一律 absolute，但 defensive 檢查：非 absolute → drop，記 metric `path_hint_drop_reason="not_absolute"`
- **獨立路徑**：不通過 `AgentProvider.DeriveStatus`（per codex D2），直接走 `m.core.Events.Broadcast(sessionCode, "agent.path_hint", payload)`
- 失敗（broadcast 錯誤、JSON encode 錯誤）fire-and-forget log warn，不擋 hook

### 3. 廣播：沿用 `core.HostEvent`

```go
// internal/module/agent/handler.go (existing handler 內)
func (m *Module) emitPathHint(hint PathHint) {
    payload, err := json.Marshal(hint)
    if err != nil { log.Warn(...); return }
    m.core.Events.Broadcast(hint.SessionCode, "agent.path_hint", string(payload))
}
```

`HostEvent.Value` 是 string；JSON 字串塞進去（既有 agent event 慣例）。

### 4. SPA 端訂閱（擴 `agent-ws-dispatch.ts` + `useMultiHostEventWs` 派發）

**4a. 擴 `useMultiHostEventWs` 把 `agent.*` 全交給 dispatcher**

既有 `useMultiHostEventWs` 目前只在 `event.type === 'agent.status' || 'agent.status.cleared'` 時呼叫 `dispatchAgentWsEvent`，新 type 收不到。改為：

```ts
if (event.type.startsWith('agent.')) dispatchAgentWsEvent(hostId, event)
```

**4b. `dispatchAgentWsEvent` 加 `agent.path_hint` case**

```ts
case 'agent.path_hint': {
  const hint = JSON.parse(event.value) as PathHint
  if (hint.pathKind !== 'absolute' || !hint.dir) return
  const wsId = resolveWorkspaceForSession(hostId, hint.sessionCode)
  if (!wsId) return  // standalone session 或無對應 tab → 不寫 cache
  usePathCacheStore.getState().add(hostId, wsId, hint.dir)
  break
}
```

**4c. 新 helper `resolveWorkspaceForSession(hostId, sessionCode): string | null`**（`spa/src/lib/resolve-workspace-for-session.ts` 新建）

語意：
1. 掃 `useTabStore.tabs` 找 primary pane 是 `tmux-session` 且 `(hostId, sessionCode)` 匹配的 tab
2. 用 `useWorkspaceStore.findWorkspaceByTab(tabId)` 找對應 workspace
3. 優先順序：active workspace 內命中 → 任一 workspace 命中 → `null`
4. **standalone session（無 tab 對應）或 cross-host 來源不明 → `null`，不寫 cache**

### 5. SPA path cache store（`spa/src/stores/usePathCacheStore.ts` 新建）

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
- `lookup(basename)` 純字串組合：對每個 cached `dir` 回傳 `path.join(dir, basename)`，**不做 stat**（stat 留 P5 caller）
- `pruneStaleCandidate(candidatePath)`：取 `dirname(candidatePath)`，從該 scope LRU 移除這個 dir entry（P5 stat 失敗時呼叫）
- 持久化：localStorage `STORAGE_KEYS.PATH_CACHE`（per codex B3 — 用 SSoT keys.ts）
- `partialize` 只存 `{ dirsByScope }`，不持久化 actions
- **Workspace remove 訂閱**：透過 `useWorkspaceStore.subscribe` 偵測 workspace 從 state 中消失 → 呼叫 `clearScope`。**Tear-off / merge（`removeWorkspace(wsId, { keepSettings: true })`）場景需區分**：本 window 視角 workspace 不見就清，避免 cache 殘留 — `keepSettings` 是設定保留語意，path cache 屬 runtime 資產，照清
- 訂閱 `useHostStore` 的 host remove → 自動 `clearHost`
- **不進 sync schema**（per `feedback_skeleton_convergence.md` + B3 codex review）

### 6. STORAGE_KEYS 加新 key

`spa/src/lib/storage/keys.ts` 加 `PATH_CACHE: 'purdex-path-cache'`。

## 邊界

- 只 CC adapter 上線；Codex / OpenCode 的 HookInstaller 留 issue 追蹤
- daemon ring buffer 不持久化（重啟丟失 OK，hooks 會 reseed）
- SPA localStorage 重啟保留 — 但 P5 caller 會 stat 驗證後才用（C2）
- 廣播 payload 不含使用者輸入內容（純路徑 metadata）
- 不寫硬碟 daemon log（per 隱私 requirement）

## 檔案影響

- `internal/module/agent/path_hint.go` — schema + ring buffer (in-memory, max 200 per host) + Go const for `PathKind` / `Confidence` / `Kind` 列舉值
- `internal/module/agent/path_hint_extractor.go` — CC payload → PathHint
- `internal/module/agent/path_hint_test.go` — daemon 測試（含 dedup window / non-absolute drop / broadcast format）
- `internal/module/agent/handler.go` — 在既有 hook handler 接點加 emit 呼叫
- `spa/src/stores/usePathCacheStore.ts` — LRU store
- `spa/src/stores/usePathCacheStore.test.ts`
- `spa/src/lib/storage/keys.ts` — 加 `PATH_CACHE`
- `spa/src/lib/resolve-workspace-for-session.ts` — 新 helper
- `spa/src/lib/resolve-workspace-for-session.test.ts`
- `spa/src/lib/agent-ws-dispatch.ts` — 加 `agent.path_hint` case
- `spa/src/lib/agent-ws-dispatch.test.ts` — case 測試
- `spa/src/hooks/useMultiHostEventWs.ts` — 擴展 `agent.*` 全部 dispatch
- `spa/src/types/agent-events.ts` — `PathHint` type 定義（與 daemon 對齊；TS const 化列舉值，unknown value 保守 drop）

## Acceptance Criteria

- [ ] CC `Read` tool 觸發 → daemon 廣播 `core.HostEvent { Type: "agent.path_hint", Session: <code>, Value: <json> }`
- [ ] payload 內 `pathKind === "absolute"`、`dir` 等於 file_path 的 dirname、`agentId === "claude-code"`、`confidence === "high"`
- [ ] 5 秒內同 SessionCode + Dir 重複 → dedup 為 1 筆
- [ ] 非 absolute path → drop，不廣播，metric 記錄
- [ ] **`useMultiHostEventWs` 收到 `agent.path_hint` event 會 dispatch 給 `dispatchAgentWsEvent`**（不被既有 type filter 擋掉）
- [ ] `dispatchAgentWsEvent` 收到 `agent.path_hint` → `resolveWorkspaceForSession(hostId, sessionCode)` 回 wsId → `usePathCacheStore` 對應 scope add 1 條
- [ ] **`resolveWorkspaceForSession` 命中順序測試**：active workspace 優先、跨 workspace 命中次之、無對應 → null（不寫 cache）
- [ ] LRU 容量 50；溢出最舊淘汰；同 dir 重複移到 head
- [ ] 切換 workspace → cache 隔離不互相污染（不同 scope key）
- [ ] localStorage 重啟保留前次 session 的 cache
- [ ] workspace remove → 對應 scope 清空（含 `keepSettings: true` 的 tear-off 場景）
- [ ] host remove → 整 host 所有 scope 清空
- [ ] **未知 `pathKind` / `confidence` 值** → defensive drop，不寫 cache、不報錯
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

### 1. 新元件 `FileNotFoundPopup`（`spa/src/components/editor/FileNotFoundPopup.tsx`）

- 顯示原始路徑、basename、為什麼找不到
- 區段 1：layer 1 命中清單（若有，可點選開啟）
- 區段 2：「展開搜尋 layer 2/3」按鈕
- **Snapshot 行為**（per codex C5）：popup 開啟時把當下 cache candidates 凍結；後續 path cache 變動不影響當前 popup（除非按 refresh）
- ESC / 取消按鈕關閉、focus trap

### 2. 新流程函式 `tryOpenFile()`（`spa/src/lib/open-file.ts`）

**簽名擴充**（per codex C4 — 帶 captured context 避免 race）：

```ts
interface OpenFileContext {
  hostId: string
  sourceWorkspaceId: string  // captured at click time, not read on each await
  sessionCode?: string       // 觸發來源 session（layer 2 用）
  cwdResolver?: () => Promise<string | null>  // layer 2 動態解析；popup 開啟後仍用 captured ctx 不重讀 active session
}

async function tryOpenFile(
  file: FileInfo,
  source: FileSource,
  ctx: OpenFileContext,
): Promise<void> {
  const fs = getFsBackend(source)
  if (await fs.stat(file.path).catch(() => null)) {
    return openInTab(file, source, ctx)  // 走 P1 + P2
  }
  if (!useUISettingsStore.getState().popupOnMissingFile) throw new FileNotFoundError(file.path)

  if (useUISettingsStore.getState().autoSearchLayer1) {
    const cached = usePathCacheStore.getState().lookup(ctx.hostId, ctx.sourceWorkspaceId, file.basename)
    const verified: string[] = []
    for (const candidate of cached) {
      const ok = await fs.stat(candidate).catch(() => null)
      if (ok) verified.push(candidate)
      else usePathCacheStore.getState().pruneStaleCandidate(ctx.hostId, ctx.sourceWorkspaceId, candidate)
    }
    if (verified.length === 1) return openInTab({ ...file, path: verified[0] }, source, ctx)
    if (verified.length > 1) return openPopup({ mode: 'layer1-multi', hits: verified, file, ctx })
  }
  return openPopup({ mode: 'ask-expand', file, ctx })
}
```

### 3. Daemon `fs.search` API（`internal/module/fs/search.go` 新建）

```
POST /api/fs/search
Body: {
  basename:     string,
  roots:        []string,
  maxResults?:  int,    // default 50, hard cap 200
  maxDepth?:    int,    // default 8
  timeoutMs?:   int,    // default 5000
  excludeDirs?: []string,  // default ["node_modules", ".git", ".cache", "dist"]
  excludeBasenameGlobs?: []string,  // default ["*.lock", "*.log"]
  respectGitignore?: bool,  // default true
}
Resp: { matches: []{ path, modTime, sizeBytes, root } }
```

**設計要點**（per codex B4）：
- **不**接受 `hostId` — daemon 操作本機 fs，跨 host routing 由 SPA 用對應 daemon base URL 完成
- exclude 規則 directory-aware（per B5）：
  - `excludeDirs`：directory basename 比對（prune 整個子樹）
  - `excludeBasenameGlobs`：檔案 basename glob 比對
- **Symlink**：預設不 follow（避免迴圈）；root 內 symlink 到 root 外的目錄會被忽略
- **Hidden dirs**（`.` 開頭）：預設搜尋（與既有 `fs.list` 行為一致）
- **gitignore**：root 內若有 `.gitignore`，用 `go-gitignore` lib 解析，pattern 套用整子樹

### 4. SPA 端 `fsSearchByBasename(hostId, basename, roots)` helper（`spa/src/lib/fs-search.ts`）

封裝對應 host 的 `POST {hostBaseUrl}/api/fs/search` 呼叫，回傳結果以 modTime desc 排序。

### 5. 整合進 Terminal link / FileTreeView 開檔路徑

- `terminal-link/openers/file-path.ts` 既有路徑做 path normalization（tilde expansion / cwd resolve），結果丟給 `tryOpenFile()`
- `FileTreeView.tsx` 點擊改用 `tryOpenFile()`

### 6. 兩個新 settings（`useUISettingsStore`）

```ts
popupOnMissingFile: boolean     // default true
autoSearchLayer1:    boolean    // default true
```

UI 放 Editor purdex scope 新區塊 `EditorOpenBehaviorSection`（與 P3 並列）。

## 邊界

- **`tryOpenFile` 接收的 `file.path` 必須是 absolute** — 上游（terminal link / FileTreeView）負責 normalization
- **Symlink 不 canonicalize**（per C3 決策 (a)）— 原始路徑直存 cache 與 popup 顯示
- Layer 2 `sessionCode` 缺失（FileTreeView 等非 session 來源）→ popup 內 layer 2 按鈕 disabled
- Layer 3 `projectPath` 未設定 → popup 內 layer 3 按鈕 disabled + tooltip 提示
- Search 結果視覺：相對 home 路徑簡寫（`~/...`）
- Popup 不阻塞其他互動（modal 但 ESC 可關）
- Editor 模組停用（P1）→ 開檔路徑根本走不到 popup（opener 已不存在）

## 檔案影響

- `spa/src/components/editor/FileNotFoundPopup.tsx` — 新建
- `spa/src/components/editor/FileNotFoundPopup.test.tsx` — 新建
- `spa/src/components/settings/EditorOpenBehaviorSection.tsx` — 新建（兩個開關）
- `spa/src/lib/open-file.ts` — 新建統一開檔流程
- `spa/src/lib/open-file.test.ts` — 新建（5 種 mode case）
- `spa/src/lib/fs-search.ts` — 新建 search helper
- `spa/src/lib/terminal-link/openers/file-path.ts` — 改走 `tryOpenFile`（保留既有 normalization）
- `spa/src/components/FileTreeView.tsx` — 改走 `tryOpenFile`
- `spa/src/stores/useUISettingsStore.ts` — 加兩個 flag
- `spa/src/stores/usePathCacheStore.ts` — 加 `pruneStaleCandidate(hostId, wsId, candidatePath)`（P4 已定義 API，P5 呼叫處）
- `internal/module/fs/search.go` — 新建 daemon endpoint
- `internal/module/fs/search_test.go` — daemon 測試
- `spa/src/lib/register-modules.tsx` — Editor settings 加 `EditorOpenBehaviorSection`

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

### daemon fs.search
- [ ] `maxResults` 截斷在指定上限（測 1 / 50 / 200）
- [ ] `timeoutMs` 觸發 → 回傳 partial results（已找到的）+ status flag
- [ ] `excludeDirs` 命中 → 整個子樹 prune（不下鑽）
- [ ] `excludeBasenameGlobs` 命中 → 個別檔案 skip
- [ ] root 內 `.gitignore` 存在 + `respectGitignore: true` → ignored 的不在結果裡
- [ ] symlink loop（A → B → A）→ 不無限遞迴
- [ ] `maxDepth: 8` → 第 9 層不掃
- [ ] body 不含 `hostId`（schema 驗證）

### Module disable interaction
- [ ] Editor 停用 → terminal link 點檔案 → `getDefaultOpener` 回 null，根本不會走進 `tryOpenFile`
- [ ] Editor 停用 → FileTreeView 點檔案 → 同上

---

## 全 Phase 共通

### Code style

- TypeScript：既有專案規範（`spa/eslint.config.js`），無破例
- Go：`gofmt` + `go vet`，error path explicit
- 命名：opener / hint / cache 等概念跟既有 lib 一致
- 測試命名：`<file>.test.ts(x)` / `<file>_test.go`，不另立 `__tests__` 子目錄（除非檔案多到該分）
- localStorage key 一律走 `STORAGE_KEYS` SSoT（`spa/src/lib/storage/keys.ts`）

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
- 所有 commit / PR：「TDD：先寫測試再實作」+「每個 task 獨立 commit」
- localStorage 一律走 `STORAGE_KEYS` SSoT

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

---

## 已知 Follow-up（不在這次 PR）

- `files` 模組標 `disableable: true` 待 PR 3 補 workspace-scope filter（既有 SR-2）
- Codex HookInstaller adapter（待 [openai/codex#16732](https://github.com/openai/codex/issues/16732) 修復後評估）
- OpenCode HookInstaller adapter（架構就緒後新議題）
- `fs.search` 加 fuzzy / content search（目前只 basename match）
- Symlink canonical 化策略 — `EvalSymlinks` vs hybrid dual-key
- `fs.search` 是否提供 `include_hidden=false` 開關
- daemon ring buffer 持久化（重啟後 PathHint 暖開機）

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
