# Spec — Workspace Snapshot（工作區快照 / 一鍵重建）

**Date**: 2026-07-13
**Branch**: `worktree-workspace-snapshot`
**Author**: Claude (Opus 4.8) + Wake
**狀態**: Draft v3。v2 納入 codex R1 六項修正（#1 複合鍵 Blocker、#2 visitHistory、#3 重建 remap 收窄、#4 best-effort 取代、#5 restorable、#6 session store 同步）。v3 納入 codex R2 修正（validateSnapshotConsistency 拆五條、restore 對 daemon 副作用非交易性揭露、sync 檔路徑）**與使用者定案的產品決策**：「重建所有 session」重建快照裡**所有可觀測 session（不收窄、orphan 為預期）**，明確 override codex R2 對 orphan 的收窄建議（§3.5）。**已通過 codex R3 複審（在接受 orphan 產品決策前提下 approve、可據以寫 plan）**，R3 兩小修已納入：§5 `createSession` 筆數斷言精確化（活著 session 走 reattached 不計入）、§3.5 validateSnapshotConsistency 範圍界定（僅驗導航參照）。

---

## 1. 背景與現況

Purdex 的 tab / workspace / pane 結構**本來就會持久化到 localStorage**（`purdex-tabs` / `purdex-workspaces`），重開 app 時 tab 會自動還原。缺口在於 terminal（tmux-session）pane 在前端只存 `sessionCode`（daemon 對 tmux session 的 host-local 編碼參照），**不存 tmux session name，也不存 cwd**。因此：

- **伺服器重開機 / tmux server 重啟 / 換機器**後，舊 `sessionCode` 一律失效 → 自動還原的 terminal tab 顯示為「已終止」，**無法自動把工作環境重建回來**。這正是本功能要解的**唯一核心痛點**（使用者親自定位）。
- 單純關掉 app 重開、或更新 Electron（不動 daemon）→ session 還活著、code 不變，**現在就會自動接回**，不是本功能目標。

本功能新增「工作區快照」：拍快照當下額外記錄每個 tmux session 的 **name + cwd（+ 當時 current_command，僅顯示）**，讓還原時對已死掉的 session **依 name + cwd 用 `tmux new-session` 重建**，達成「一鍵重建工作環境」。

### 引擎盤點（已存在，本次沿用，不改）

| 能力 | 位置 |
|------|------|
| Tab / Pane / PaneContent / SplitLayout / Workspace 型別 | `spa/src/types/tab.ts` |
| `listSessions(hostId)` → 每 session 帶 `code / name / cwd / mode / current_command` | `spa/src/lib/host-api.ts:94` |
| `createSession(hostId, name, cwd, mode)` → **已支援指定 name + cwd**，回傳完整 `Session` | `spa/src/lib/host-api.ts:100` |
| `fetchSessionCwd(hostId, code)`（單抓備援） | `spa/src/lib/host-api.ts:157` |
| daemon cwd SOT = tmux `pane_current_path`；建立走 `tmux new-session -c cwd` | `internal/module/session/cwd_handler.go` / `internal/tmux/executor.go:187` |
| **sessionCode 是 host-local**（`compositeKey(hostId, code)`；「sibling hosts can mint identical codes」；測試釘住跨 host 同 code 不互清） | `spa/src/lib/composite-key.ts` / `useMultiHostEventWs.ts:128` / `stores/path-cache/usePathCacheStore.test.ts:112` |
| Tab store（persist `purdex-tabs`，partialize `tabs/tabOrder/activeTabId`；另有**非 persist** `visitHistory`） | `spa/src/stores/useTabStore.ts:127/454` |
| Workspace store（persist `purdex-workspaces`，partialize `workspaces/activeWorkspaceId`；關 tab 讀 `visitHistory` 選下一個） | `spa/src/features/workspace/store.ts:195/292` |
| `scanPaneTree` / `collectLeaves` / `updatePaneInLayout` / `findTabBySessionCode` | `spa/src/lib/pane-tree.ts` |
| Terminated 標記（`TerminatedReason` / `markTerminated`） | `spa/src/types/tab.ts` / `useTabStore.ts` |
| storage backend `purdexStorage`（**每次 setItem 立即 localStorage + `syncManager.notify` 廣播**） | `spa/src/lib/storage/browser-backend.ts` / `spa/src/lib/storage/sync.ts:15` |
| pane 內容用 `useSessionStore.sessions[hostId]` 查 session；名稱快取只在 WS 事件更新 | `spa/src/components/SessionPaneContent.tsx:27` / `useMultiHostEventWs.ts:103` |
| Settings section 註冊 `registerSettingsSection({ id, label, order, component })` | `spa/src/lib/settings-section-registry.ts` |

### 關鍵事實

- **純前端 SPA 功能**：capture 靠 `listSessions`、restore 靠 `createSession(name, cwd, mode)`，daemon **大概率零改動**（唯一待探明：撞同名 session，§8.1）。
- terminal pane 的 `sessionCode` **host-local**，重建後必變 → 任何還原都必須以 **(hostId, sessionCode) 複合鍵**改寫 layout 樹（§3.3）。

---

## 2. 目標與非目標

### 目標

1. **拍下**當前整個工作區狀態：所有 workspace / tab / pane 結構 + 每個 tmux session 的 `hostId / sessionCode / name / cwd / current_command`。
2. **單一份**快照存 localStorage，再拍即覆蓋；還原前自動存 `-prev` 後悔藥。
3. 還原 tmux session：**活著直接接回、已死且 cwd 可得則依 name+cwd 自動重建**、否則標 terminated（隔離失敗，不中斷整體）。
4. Settings 新增「Snapshot」section 頁，分 **Tmux Sessions / Tabs** 兩區 + **即時健康度對帳**。
5. 三個可分別執行的還原動作：**重建所有 session**、**還原 tab 佈局**、**全部還原**；外加 **復原上次還原**。

### 非目標

- **不自動重跑** session 內原本在跑的程式（claude / vim…）。`current_command` 僅顯示（安全＋內部狀態無法還原）。
- **不記錄** app-frame 側欄/面板佈局（`purdex-layout`，使用者明確排除）。
- **不做多份具名快照**（單一份覆蓋式）。
- **不進** `purdex-sync` snapshot-store、不做跨裝置 sync（本機 localStorage）。
- **不改** tab/workspace persist 格式與既有 store action 語意。

---

## 3. 設計

### 3.1 資料模型

快照存 localStorage，key = `purdex-workspace-snapshot`（正本）與 `purdex-workspace-snapshot-prev`（還原前自動備份）。

```ts
interface SessionMeta {
  hostId: string
  sessionCode: string              // 拍快照當下的 code（host-local）
  name: string
  mode: 'terminal' | 'stream'
  cwd?: string                     // 抓不到則 undefined（不是空字串）
  currentCommand?: string          // 顯示用；抓不到則省略
  restorable: boolean              // cwd 有值且 host 可達才 true
  captureError?: 'host-unreachable' | 'session-dead-at-capture'
}

interface WorkspaceSnapshot {
  version: 1
  capturedAt: number
  // 沿用兩 store 既有 partialize 形狀，避免格式漂移
  tabs: Record<string, Tab>
  tabOrder: string[]
  activeTabId: string | null
  workspaces: Workspace[]
  activeWorkspaceId: string | null
  // sidecar：以 (hostId, sessionCode) 複合鍵巢狀索引
  sessionMeta: Record<string, Record<string, SessionMeta>>  // [hostId][sessionCode]
}
```

- **複合鍵（codex Blocker #1）**：`sessionCode` **host-local**（`composite-key.ts`、`useMultiHostEventWs.ts:128` 註解、`usePathCacheStore.test.ts:112` 測試皆釘住）。`sessionMeta` 以 `[hostId][sessionCode]` 巢狀索引；`Remap`（§3.3）與 `remapLayoutSessions`（§3.4）一律以 **(hostId, sessionCode)** 對帳，禁用裸 code。
- **`restorable`（codex #5）**：僅當 `cwd` 有值（tmux `pane_current_path` 成功）且 host 可達才 `true`。cwd 不明者**不進 `createSession`**（避免 `tmux new-session -c ''` 開在錯目錄），restore 時直接標 terminated。
- 讀 / 寫透過 `purdexStorage`，非直接 `localStorage`。

### 3.2 拍快照（capture）

`captureSnapshot(now: number): Promise<CaptureResult>`：

1. 從 `useTabStore.getState()` 取 `tabs / tabOrder / activeTabId`；`useWorkspaceStore.getState()` 取 `workspaces / activeWorkspaceId`。
2. `scanPaneTree` 走每個 tab.layout，收集所有 `kind === 'tmux-session'` 的 pane（`{ sessionCode, hostId, mode }`），依 `hostId` 分組。
3. 每個涉及 host **呼叫一次** `listSessions(hostId)`，依 `(hostId, sessionCode)` 比對出 `name / cwd / current_command`，填 `sessionMeta[hostId][sessionCode]`，`restorable = true`。
   - host `listSessions` 失敗 → `name = cachedName`、`cwd = undefined`、`restorable = false`、`captureError = 'host-unreachable'`，累計 `unresolved`（不中斷）。
   - session 已不在清單（拍當下就死）→ `name = cachedName`、`cwd = undefined`、`restorable = false`、`captureError = 'session-dead-at-capture'`。
4. 組出 `WorkspaceSnapshot`（`capturedAt = now`），整包覆蓋寫 `purdex-workspace-snapshot`。
5. 回傳 `CaptureResult { total, resolved, unresolved }` 供 toast。

> **決定性**：`capturedAt` 不在函式內呼叫 `Date.now()`，由 UI 帶入（`now`），利於單元測試決定性斷言。

### 3.3 sessionCode 重映射（restore 共同核心）

terminal pane 嵌 host-local code，重建後必變，故任何 restore 都繞不開以 **(hostId, oldCode)** 為鍵的重映射表。

```ts
type RemapEntry =
  | { status: 'reattached'; newCode: string; session: Session }  // 活著，code 不變
  | { status: 'rebuilt';    newCode: string; session: Session }  // 依 name+cwd 重建
  | { status: 'failed' }                                         // 無法重建 → 標 terminated
type Remap = Record<string /* hostId */, Record<string /* oldCode */, RemapEntry>>
```

`ensureSessions(sessionMeta): Promise<{ remap: Remap; report: EnsureReport }>`：

- 蒐集涉及的 hostId，每個 host **呼叫一次** `listSessions(hostId)`（成功回應 = host 可達訊號）。
- 對 `sessionMeta[hostId][oldCode]` 每筆：
  - **活著**（oldCode ∈ 該 host 活著清單）→ `reattached`，`newCode = oldCode`，`session` = 該筆。
  - **已死** 且 `restorable`（host 可達 + cwd 有值）→ `createSession(hostId, name, cwd, mode)` → `rebuilt`，`newCode` = 回傳 session code，`session` = 回傳物件（**含 daemon 實際採用的 name**，§8.1）。
  - **host 離線** / **`restorable === false`（cwd 不明）** / `createSession` 失敗 → `failed`（後續標 terminated；**不呼叫 `createSession('', …)`**）。
- **回傳完整 `Session` 物件**（codex #6），供 restore 後同步 `useSessionStore` + `cachedName`。
- `EnsureReport { reattached, rebuilt, failed }` 供 toast。

### 3.4 套用重映射到 layout 樹

`remapLayoutSessions(layout, remap, opts?): PaneLayout`（純函式，基於 `updatePaneInLayout` / `scanPaneTree`），對每個 `tmux-session` pane 以 **(pane.hostId, pane.sessionCode)** 查 `remap`：

- `reattached` / `rebuilt` → 設 `sessionCode = newCode`、`cachedName = session.name`，清除 `terminated`。
- `failed` → 保留原 code，設 `terminated`（`TerminatedReason`）。
- remap 無此 (hostId, code)（防禦性）→ 原樣不動。
- `opts.onlyTerminated`（供「重建所有 session」，§3.5）為 true 時**只處理原本 `terminated` 的 pane**，活著的 pane 完全不碰。

### 3.5 三個還原動作

全部建立在 §3.3 / §3.4 primitive 上：

| 動作 | 位置 | 步驟 |
|------|------|------|
| **重建所有 session** | Tmux 區 | ①`ensureSessions(snapshot.sessionMeta)` —— **重建快照裡所有 `restorable` 且 host 可達的 session**，不限當前 tab 是否引用（見「重建範圍＝產品決策」）；②對**當前已開** tabs 套 `remapLayoutSessions(…, { onlyTerminated: true })`（只把 terminated pane 接到重建結果，活著的 pane 不碰）→ `replaceTabState`（僅 tabs）+ 同步 session store。**不動 tab 結構**、不碰 workspace、不寫 `-prev`。 |
| **還原 tab 佈局** | Tab 區 | 對「目前還活著」依 (hostId,code) 輕量對帳（死掉標 terminated，**不重建**）→ `replaceTabSnapshot(snapshot)` 取代 tab/workspace。 |
| **全部還原** | 頂部 | `ensureSessions(...)` → 對 `snapshot.tabs` 套 `remapLayoutSessions` → `replaceTabSnapshot(改寫後)` + 同步 session store。（= §3.2 完整流程） |

**「重建所有 session」重建範圍＝產品決策（使用者定案，勿翻案）**：此動作對**整份** `snapshot.sessionMeta` 做 `ensureSessions`，重建快照裡**所有** `restorable` 且 host 可達的 session —— **刻意不限縮**在「當前已開 tab 仍引用的 session」。核心情境是伺服器重開機後 tmux 全滅，使用者要一次把**整個工作環境的 tmux session** 都拉回來；因此重建出當前畫面尚未接上的 session（orphan）是**預期行為、非缺陷** —— 隨後配合「還原 tab 佈局」/「全部還原」即接上，即使單獨執行，那些 session 也會出現在 daemon `listSessions` / Sessions 清單供手動接回。**codex R2 對 orphan 的收窄建議在此明確不採納。**

**remap 套用範圍仍收窄（避免誤改活 pane，codex R1 #3）**：重建產生的 remap 僅以 `onlyTerminated: true` 套回當前 tabs 的 terminated pane —— 活著的 pane 完全不碰，杜絕「某活 pane 的 (hostId, code) 恰撞 snapshot 舊 code 而被誤改」。此為正確性防線，與上述重建範圍**獨立、互不影響**（重建範圍決定 daemon 上建哪些 session；remap 範圍決定改當前哪些 pane）。**副作用揭露**：本動作在 daemon 上實際建立 session（含 orphan），非唯讀；因僅新增 tmux session、不動 tab/workspace 結構，仍不寫 `-prev`。

**取代語意（best-effort，非真原子）（codex #4）**：兩 store 各自 `setState` **並非真原子** —— `browserStorage.setItem` 每次立即 `localStorage.setItem` + `syncManager.notify`，其他視窗會立刻 rehydrate 單一 key，故中間有可觀察空窗。改用 coordinator：

- `validateSnapshotConsistency(snapshot)`（codex R2 拆明五條，修正「全部驗在 `tabs`」的矛盾）：①`workspace.tabs` 每個 id ∈ `tabs`；②`activeTabId` ∈ `tabs`（或 `null`）；③各 `workspace.activeTabId` ∈ 該 `workspace.tabs`；④`activeWorkspaceId` ∈ `workspaces`（**非** `tabs`）或 `null`；⑤`tabOrder` 每個 id ∈ `tabs`。任一不通過則中止、不動任何 store。**範圍界定（codex R3）**：本檢查僅涵蓋 tab/workspace **導航參照**；**不驗** pane content 內部語意參照 —— 例如 `settings` pane 的 `scope.workspaceId` 指向已刪 workspace，該情況既有 UI 已顯示 `Workspace not found`、非致命，plan 可評估是否補驗（本次不擴張範圍）。
- `replaceTabSnapshot(snapshot)`：先驗證 → 保存兩 store 舊值 → 依序 setState（tab 先、workspace 後）→ 任一步丟錯則**用舊值 rollback** 兩 store。
- **visitHistory（codex #2）**：`useTabStore.visitHistory` 非 persist、workspace 關 tab 讀它選下一個（`workspace/store.ts:195`）；取代時把 `visitHistory` filter 成新 `tabOrder` 子集（移除指向已消失 tab 的引用），連同 `tabs/tabOrder/activeTabId` 一起 set。
- spec 明訂此為 **best-effort 一致，非跨視窗真原子**。

**同步 session store（codex #6）**：取代/重建後，用 `ensureSessions` 回傳的 `Session` 物件 upsert `useSessionStore`（`replaceHost` / 逐筆），並確保各 pane `cachedName` = daemon 回傳實際 name，避免 restore 後 UI 短暫顯示錯名 / 查不到新 session。

**還原前自動備份**：任一「取代式」動作（還原 tab 佈局 / 全部還原）執行前，先把**當前** store capture 成 `WorkspaceSnapshot` 寫 `purdex-workspace-snapshot-prev`。「復原上次還原」= 讀 `-prev` 走「全部還原」。

> 「重建所有 session」非破壞性（僅改 terminated pane 的 code），故不寫 `-prev`。

### 3.6 錯誤處理

- capture：host 失敗僅該 host 記 `restorable=false`（cwd undefined），其餘照拍；toast「已拍快照：N 個終端機、其中 M 個無法記錄路徑」。
- restore：逐 session 獨立失敗隔離（重建失敗或 `restorable=false` 只標該 pane terminated，不 `createSession('')`）；toast 彙總「X 直接接回 / Y 依路徑重建 / Z 無法重建」。
- restore「先算後套」：`ensureSessions` + `remapLayoutSessions` 完成後才進 `replaceTabSnapshot`（含一致性驗證 + rollback）。**「不留半套」僅指前端 store**（codex R2）：`replaceTabSnapshot` 失敗會用舊值 rollback 兩 store，但 `ensureSessions` 已在 daemon 建立的 session 是**真副作用、不會自動撤銷**；此時彙整「已重建但未接上」session 清單，toast + 日誌揭露（plan 評估是否補償刪除，見 §8.5）。

### 3.7 UI — Settings「Snapshot」section

`registerSettingsSection({ id: 'snapshot', label: 'Snapshot', order, component: SnapshotSettingsSection })`（order 置既有 sections 之後，plan 定值）。

`SnapshotSettingsSection`：

- **頂部列**：「拍下快照」（顯示 `capturedAt` 相對時間）、「全部還原」（無快照 disabled）、「復原上次還原」（無 `-prev` disabled）。
- **區塊 1 — Tmux Sessions**（對帳表）：
  - 每列：`host / name / cwd（無值顯示「未記錄」）/ current_command（拍當下）/ 健康度`。
  - 健康度：section 掛載時對各 host（依 hostId）`listSessions` 即時對帳 → 🟢 活著（可接回）/ 🔴 已死可重建（`restorable` 且 host 可達）/ ⚠️ 只能保留結構（`restorable=false`，無 cwd）/ ⚪ host 離線（無法重建）。
  - 區塊鈕：「重建所有 session」（重建**快照裡所有** 🔴，不限當前 tab 是否引用；⚠️/⚪ 維持 terminated）。
- **區塊 2 — Tabs / Workspaces**：樹狀列 workspace → tab → pane（terminal 顯示 name、editor/preview 顯示 filePath、browser 顯示 url）。區塊鈕：「還原 tab 佈局」。
- 無快照時兩區顯示 empty state（只給「拍下快照」）。

---

## 4. 元件 / 資料流

| 檔案 | 類型 | 職責 |
|------|------|------|
| `spa/src/lib/snapshot/types.ts` | 新增 | `WorkspaceSnapshot` / `SessionMeta`（含 `restorable`）/ `Remap`（複合鍵）/ `CaptureResult` / `EnsureReport` |
| `spa/src/lib/snapshot/storage.ts` | 新增 | 讀/寫 `purdex-workspace-snapshot(-prev)`（走 `purdexStorage`） |
| `spa/src/lib/snapshot/capture.ts` | 新增 | `captureSnapshot(now)`（讀兩 store + `listSessions` 依 (hostId,code) 補 meta） |
| `spa/src/lib/snapshot/restore.ts` | 新增 | `ensureSessions`（回傳完整 Session）/ `remapLayoutSessions`（複合鍵、`onlyTerminated`）/ `validateSnapshotConsistency` / `replaceTabState` / `replaceTabSnapshot`（rollback + visitHistory filter + session store 同步）/ 三動作 + `-prev` |
| `spa/src/components/settings/SnapshotSettingsSection.tsx` | 新增 | Settings section UI（兩區 + 健康度對帳 + 動作鈕 + toast） |
| section 註冊呼叫點（對齊既有 `*SettingsSection`） | 修改 | `registerSettingsSection(...)` |

- **依賴方向**：UI → restore/capture → host-api + pane-tree + stores。純邏輯層（capture/restore/storage）不依賴 React，利於 Vitest。

---

## 5. Phase 切分（依 review 大小）

### Phase 1 — 資料模型 + capture + 持久化
- `types.ts` / `storage.ts` / `capture.ts`。
- **驗收**：mock 兩 store + mock `listSessions`，斷言 `sessionMeta` 依 (hostId,code) 巢狀正確、`restorable`/`captureError` 正確；host 失敗 `unresolved` 計數正確、不中斷；`capturedAt` 由入參決定。純邏輯 TDD。

### Phase 2 — restore 引擎 + 三動作
- `restore.ts`：`ensureSessions`（複合鍵、回傳 Session）、`remapLayoutSessions`（複合鍵、`onlyTerminated`）、`validateSnapshotConsistency`、`replaceTabState`/`replaceTabSnapshot`（rollback + visitHistory filter + session store 同步）、三個 orchestration、`-prev`。
- **驗收**：
  - **跨 host 同 code 不撞（#1）**：兩 host 各有相同 `sessionCode` 值，remap 依 (hostId,code) 各自對帳、不互污。
  - 「部分活著 / 已死 / host 離線 / cwd 不明」→ 各 entry status 正確、layout 依複合鍵改寫、`failed` 與 `restorable=false` 標 terminated 且**未呼叫 `createSession('')`（#5）**。
  - **取代一致性（#4 + R2 C）**：`validateSnapshotConsistency` 五條參照檢查各自擋掉對應壞快照（含 `activeWorkspaceId` 不在 `workspaces`、`tabOrder` 含幽靈 id、`workspace.activeTabId` 不在該 workspace）；第二個 store setState 丟錯時兩 store 皆 rollback。
  - **visitHistory（#2）**：取代後不含已消失 tab 引用；「restore 後立刻關 active tab」選到的下一個 tab 正確。
  - **session store 同步（#6）**：`rebuilt` 後 `useSessionStore` 有新 session、pane `cachedName` = daemon 回傳實際 name。
  - **重建範圍 + remap 收窄（#3 + a 點產品決策）**：「重建所有 session」對整份 `sessionMeta` 重建 —— 斷言 `createSession` 呼叫筆數 = 快照中 `restorable` **且** restore 當下對帳為「已死」的 session 數（即 🔴，含當前 tab 未引用的 orphan）；活著的 session 走 `reattached`、**不計入** create 筆數（codex R3）；且不因當前 tab 未引用而略過任何已死 orphan。但 remap 只改 `terminated` pane —— 當前有一活著且 code 恰等於某 snapshot 舊 code 的 pane 不被動到。
  - **daemon 副作用揭露（R2 B）**：`replaceTabSnapshot` 驗證失敗 / rollback 時，回傳含「已重建但未接上」session 清單供 UI 揭露。
  - 三動作語意各自正確；`-prev` 於取代式動作前寫入、「復原上次還原」能還回去。
  - **撞名邊界（§8.1）**：先寫測試釘住 daemon `createSession` 對同名 session 的行為（探明後補斷言）。

### Phase 3 — Settings Snapshot section UI
- `SnapshotSettingsSection.tsx` + 註冊。
- 兩區呈現、掛載時即時健康度四態對帳、三顆動作鈕、toast 彙總、empty state。
- **驗收**：RTL — 健康度四態渲染、動作鈕觸發對應 orchestration（mock restore 層）、無快照時 empty state。

---

## 6. 測試策略

- 純邏輯層（Phase 1/2）以 Vitest 為主，mock `host-api` 與兩 store（依 `feedback_zustand_harness_setstate` 的 merge-mode setState 慣例）。
- UI 層（Phase 3）用 RTL，mock restore/capture 層。
- 每 Phase 獨立 commit；`cd spa && npx vitest run` + `pnpm run lint` + `pnpm run build` 全綠才進下一 Phase。

---

## 7. 限制（寫進 UI 說明文案）

- 重建的 session 只回到 **cwd**；原本在跑的程式不會自動重跑（僅顯示拍當下 `current_command`）。
- `stream` mode session 重建後為空（Claude `-p` 串流不重播），同樣只還原 cwd。
- **cwd 記不到的 session（`restorable=false`）只保留 tab 結構、無法一鍵重建**，UI 明示、restore 標 terminated。
- 「取代式」還原為 **best-effort 一致，非跨視窗真原子**（§3.5）。
- 快照為**單一份**，再拍即覆蓋；跨裝置不同步（本機 localStorage）。

---

## 8. 待探明 / 風險

1. **撞同名 session**（唯一可能碰 daemon 的點）：`createSession(hostId, name, cwd, mode)` 遇 daemon 上已存在同 name session 的行為？（拒絕 / 自動改名 / 復用？）Phase 2 先寫測試/手動打 API 探明。緩解已內建：`ensureSessions` 一律以 `createSession` **回傳物件的實際 code + name** 為準（§3.3），故即使 daemon 自動改名，前端仍對接正確 session；只有「靜默復用到錯 session」才需 daemon 端補 name 唯一性。**這是 spec 唯一開放技術問題。**
2. **host 可達性判定**：`ensureSessions` 以 `listSessions` 是否成功回應區分「session 死但 host 活（可重建）」vs「host 離線（`failed`）」。
3. **大量 session**：`ensureSessions` 對已死 session 逐一 `createSession`；量大時序列/並行？plan 定（傾向有上限並行 + 逐一失敗隔離）。
4. **`replaceTabSnapshot` rollback 邊界**：第一個 `setState` 已 `syncManager.notify` 廣播後才 rollback，其他視窗仍可能短暫見中間態 —— §3.5 已明訂 best-effort、非承諾消除。plan 評估是否引入單一 batched key（超出本次範圍，先記錄）。
5. **restore 的 daemon 副作用非交易性（codex R2 B）**：`ensureSessions` 先在 daemon 建立 session，之後 `replaceTabSnapshot` 若一致性驗證失敗 / rollback，已建 session **不會自動刪除**（前端 store 乾淨，daemon 端多出 session）。緩解：restore 失敗時彙整「已重建但未接上」清單 toast + 日誌；plan 評估補償刪除或引導用「重建所有 session」重試接上。此與「重建所有 session」刻意產生的 orphan（§3.5）性質一致 —— daemon 端多出的 session 可被後續動作接回或手動清理，**不造成資料遺失**，故本次採「揭露而不自動刪除」。
