# Spec — Workspace Snapshot（工作區快照 / 一鍵重建）

**Date**: 2026-07-13
**Branch**: `worktree-workspace-snapshot`
**Author**: Claude (Opus 4.8) + Wake
**狀態**: Draft（待 codex 審）

---

## 1. 背景與現況

Purdex 的 tab / workspace / pane 結構**本來就會持久化到 localStorage**（`purdex-tabs` / `purdex-workspaces`），重開 app 時 tab 會自動還原。但有一個缺口：terminal（tmux-session）pane 在前端只存 `sessionCode`（daemon 對 tmux session 的編碼參照），**不存 tmux session name，也不存 cwd**。因此：

- 重開機 / tmux server 重啟 / 換機器後，舊 `sessionCode` 一律失效 → 自動還原的 terminal tab 會顯示為「已終止」，**無法自動把工作環境重建回來**。
- 使用者失去「我當時在哪些目錄開了哪些終端機」的資訊。

本功能新增「工作區快照」：在拍快照的當下，額外記錄每個 tmux session 的 **name + cwd（+ 當時在跑的 current_command，僅供顯示）**，讓還原時能對已死掉的 session **依 name + cwd 用 `tmux new-session` 重建**，達成「一鍵重建工作環境」。

### 引擎盤點（已存在，本次沿用，不改）

| 能力 | 位置 |
|------|------|
| Tab / Pane / PaneContent / SplitLayout / Workspace 型別 | `spa/src/types/tab.ts` |
| `listSessions(hostId)` → 每個 session 帶 `code / name / cwd / mode / current_command` | `spa/src/lib/host-api.ts:94` |
| `createSession(hostId, name, cwd, mode)` → **已支援指定 name + cwd** | `spa/src/lib/host-api.ts:100` |
| `fetchSessionCwd(hostId, code)`（單抓，備援） | `spa/src/lib/host-api.ts:157` |
| daemon cwd SOT = tmux `pane_current_path` | `internal/module/session/cwd_handler.go` / `service.go` |
| Tab store（persist `purdex-tabs`，partialize `tabs/tabOrder/activeTabId`） | `spa/src/stores/useTabStore.ts:454` |
| Workspace store（persist `purdex-workspaces`，partialize `workspaces/activeWorkspaceId`） | `spa/src/features/workspace/store.ts:292` |
| `scanPaneTree(layout, fn)`（唯讀走訪所有 pane）/ `collectLeaves` / `updatePaneInLayout` / `findTabBySessionCode` | `spa/src/lib/pane-tree.ts` |
| Terminated 標記機制（`TerminatedReason` / `markTerminated` / `markHostTerminated`） | `spa/src/types/tab.ts` / `useTabStore.ts` |
| 統一 storage backend `purdexStorage` + keys | `spa/src/lib/storage/index.ts` / `keys.ts` |
| Settings section 註冊 `registerSettingsSection({ id, label, order, component })` | `spa/src/lib/settings-section-registry.ts` |
| 既有 section 元件範例 | `spa/src/components/settings/*SettingsSection.tsx` |

### 關鍵事實

- **純前端 SPA 功能**：capture 靠 `listSessions`、restore 靠 `createSession(name, cwd, mode)`，daemon **大概率零改動**。唯一待探明：重建時 daemon 遇到「同 name 已存在的 session」的行為（§8 風險）。
- terminal pane 存的 `sessionCode` 在重建後**必然改變**，任何還原都必須改寫 layout 樹裡的 code（→ §3.3 重映射表）。

---

## 2. 目標與非目標

### 目標

1. 使用者可**拍下**當前整個工作區狀態的快照：所有 workspace / tab / pane 結構 + 每個 tmux session 的 `hostId / name / cwd / current_command`。
2. **單一份**快照，存 localStorage，再拍即覆蓋；還原前自動存一份 `-prev` 後悔藥。
3. 還原時對 tmux session：**活著直接接回、已死依 name+cwd 自動重建**、host 離線則標 terminated（隔離失敗，不中斷整體）。
4. Settings 新增「Snapshot」section 頁，分 **Tmux Sessions / Tabs** 兩區，含**即時健康度對帳**（🟢活著 / 🔴已死 / ⚪離線）。
5. 三個還原動作（可分別執行）：**重建所有 session**（Tmux 區）、**還原 tab 佈局**（Tab 區）、**全部還原**（頂部）；外加 **復原上次還原**（讀 `-prev`）。

### 非目標

- **不自動重跑** session 內原本在跑的程式（claude / vim…）。`current_command` 僅顯示，供使用者手動判斷。理由：任意命令自動執行有安全風險，且程式內部狀態（vim buffer、claude 對話）無法靠命令名還原。
- **不記錄** app-frame 側欄/面板佈局（`purdex-layout`）—— 使用者明確排除，只記 tab/workspace。
- **不做多份具名快照**（單一份覆蓋式）。
- **不新增跨裝置 sync**：快照存本機 localStorage，不進 `purdex-sync` snapshot-store。
- **不改** tab/workspace persist 格式與既有 store action 語意。

---

## 3. 設計

### 3.1 資料模型

快照存 localStorage，key = `purdex-workspace-snapshot`（正本）與 `purdex-workspace-snapshot-prev`（還原前自動備份）。

```ts
interface WorkspaceSnapshot {
  version: 1
  capturedAt: number
  // 沿用兩個 store 既有 partialize 的形狀，避免格式漂移
  tabs: Record<string, Tab>          // 完整 tab + layout 樹
  tabOrder: string[]
  activeTabId: string | null
  workspaces: Workspace[]
  activeWorkspaceId: string | null
  // sidecar：拍快照當下每個 tmux-session pane 的 tmux 側資訊
  sessionMeta: Record<string, {      // key = 拍快照當下的 sessionCode
    hostId: string
    name: string
    cwd: string
    mode: 'terminal' | 'stream'
    currentCommand?: string          // 顯示用；抓不到則省略
  }>
}
```

- `sessionMeta` 的 key 是**拍快照當下**的 `sessionCode`，作為與 layout 樹裡 pane 對接的橋樑。
- 讀 / 寫透過 `purdexStorage`（與其他 store 一致），非直接 `localStorage`。

### 3.2 拍快照（capture）

`captureSnapshot(): Promise<CaptureResult>`：

1. 從 `useTabStore.getState()` 取 `tabs / tabOrder / activeTabId`；從 `useWorkspaceStore.getState()` 取 `workspaces / activeWorkspaceId`。（用既有 partialize 形狀。）
2. `scanPaneTree` 走過每個 tab.layout，收集所有 `kind === 'tmux-session'` 的 pane，取得 `{ sessionCode, hostId, mode }`，依 `hostId` 分組。
3. 每個涉及的 host **呼叫一次** `listSessions(hostId)`（一次拿全部，優於逐一 `fetchSessionCwd`）。用回傳結果比對出每個 pane 的 `name / cwd / current_command`，填入 `sessionMeta`。
   - host `listSessions` 失敗 → 該 host 的 pane 記為 name 用 `cachedName`、cwd 空字串，並在 `CaptureResult` 累計 `unresolved` 計數（不中斷）。
   - session 已不在清單（拍快照當下就死了）→ 同上，用 `cachedName`、空 cwd。
4. 組出 `WorkspaceSnapshot`（`capturedAt` 由呼叫端注入，見下），整包覆蓋寫入 `purdex-workspace-snapshot`。
5. 回傳 `CaptureResult { total, resolved, unresolved }` 供 UI toast。

> **時間戳**：`capturedAt` 不在純函式內部產生（利於測試決定性）。由 UI 呼叫端以 `Date.now()` 帶入 capture 參數。

### 3.3 sessionCode 重映射（restore 的共同核心）

terminal pane 嵌的是 sessionCode，重建出的 code 必然不同，故任何 restore 都繞不開一張 `oldCode → newCode | null` 表。

`ensureSessions(sessionMeta): Promise<Remap>`：

- 蒐集 `sessionMeta` 涉及的所有 hostId，每個 host **呼叫一次** `listSessions(hostId)` 得「目前活著」的 code 清單（並偵測 host 是否可達）。
- 對 `sessionMeta` 每筆：
  - **活著**（oldCode ∈ 活著清單）→ `newCode = oldCode`（直接接回，內容不動）。
  - **已死** 且 host 可達 → `createSession(hostId, name, cwd, mode)` → `newCode = 新 code`。
  - **host 離線** 或 `createSession` 失敗 → `newCode = null`（該 pane 之後標 terminated）。
- 回傳 `Remap = Map<oldCode, string | null>` + `EnsureReport { reattached, rebuilt, failed }`。

### 3.4 套用重映射到 layout 樹

`remapLayoutSessions(layout, remap): PaneLayout`（純函式，基於 `updatePaneInLayout` / `scanPaneTree`）：

- 走過樹，對每個 `tmux-session` pane：
  - `remap` 有 `newCode`（非 null）→ 設 `sessionCode = newCode`，清除 `terminated`。
  - `remap` 為 `null` → 保留原 code，設 `terminated`（沿用 `TerminatedReason`）。
  - `remap` 無此 code（理論上不會，防禦性）→ 原樣不動。

### 3.5 三個還原動作

全部建立在 §3.3 / §3.4 primitive 上，共用同一套邏輯：

| 動作 | 位置 | 步驟 |
|------|------|------|
| **重建所有 session** | Tmux 區 | `remap = ensureSessions(snapshot.sessionMeta)` → 對**當前已開**的 `useTabStore.tabs` 各 layout 套 `remapLayoutSessions` → `setState` 更新 tabs。**不動 tab 結構**（不新增/刪除 tab、不碰 workspace）。 |
| **還原 tab 佈局** | Tab 區 | 只針對「目前還活著」做輕量對帳（`listSessions` 比對；死掉者標 terminated，**不重建**——重建是 Tmux 區職責）→ 以 `snapshot.tabs/tabOrder/activeTabId` + `snapshot.workspaces/activeWorkspaceId` **原子取代** store。 |
| **全部還原** | 頂部 | `remap = ensureSessions(...)` → 對 `snapshot.tabs` 各 layout 套 `remapLayoutSessions` → 以改寫後結果 + workspaces **原子取代**兩 store。（= §3.2 完整流程） |

**原子取代**：以單次 `useTabStore.setState({ tabs, tabOrder, activeTabId })` + `useWorkspaceStore.setState({ workspaces, activeWorkspaceId })` 整包覆蓋；persist middleware 自動寫回 localStorage。中途失敗不留半套（先算完 remap + 改寫，最後才 setState）。

**還原前自動備份**：任一「取代式」動作（還原 tab 佈局 / 全部還原）執行前，先把**當前** store 狀態 capture 成一份 `WorkspaceSnapshot` 寫入 `purdex-workspace-snapshot-prev`。「復原上次還原」= 讀 `-prev` 走「全部還原」流程。

> 「重建所有 session」不取代 tab 結構、非破壞性，故不寫 `-prev`（僅改 sessionCode，可再拍/再還原）。

### 3.6 錯誤處理

- capture：host 失敗僅該 host 記空 cwd，其餘照拍；toast「已拍快照：N 個終端機、其中 M 個路徑未能記錄」。
- restore：逐 session 獨立失敗隔離（重建失敗只標該 pane terminated）；toast 彙總「X 直接接回 / Y 依路徑重建 / Z 無法重建」。
- restore 全程「先算後套」：`ensureSessions` + `remapLayoutSessions` 完成後才 `setState`，避免半套狀態。

### 3.7 UI — Settings「Snapshot」section

透過 `registerSettingsSection({ id: 'snapshot', label: 'Snapshot', order, component: SnapshotSettingsSection })` 註冊（order 置於既有 sections 之後，plan 階段定值）。

`SnapshotSettingsSection`：

- **頂部列**：
  - 「拍下快照」鈕（顯示 `capturedAt` 相對時間，例如「上次：3 小時前」）。
  - 「全部還原」鈕（快照不存在時 disabled）。
  - 「復原上次還原」鈕（`-prev` 不存在時 disabled）。
- **區塊 1 — Tmux Sessions**（對帳表）：
  - 每列：`host / name / cwd / current_command（拍當下）/ 健康度`。
  - 健康度：section 掛載時對各 host `listSessions` 即時對帳 → 🟢 活著（可接回）/ 🔴 已死（將依 cwd 重建）/ ⚪ host 離線（無法重建）。
  - 區塊動作鈕：「重建所有 session」。
- **區塊 2 — Tabs / Workspaces**：
  - 樹狀列出 workspace → tab → pane（kind + 識別資訊：terminal 顯示 name、editor/preview 顯示 filePath、browser 顯示 url）。
  - 區塊動作鈕：「還原 tab 佈局」。
- 快照不存在時，兩區顯示 empty state（只給「拍下快照」）。

---

## 4. 元件 / 資料流

| 檔案 | 類型 | 職責 |
|------|------|------|
| `spa/src/lib/snapshot/types.ts` | 新增 | `WorkspaceSnapshot` / `SessionMeta` / `Remap` / `CaptureResult` / `EnsureReport` 型別 |
| `spa/src/lib/snapshot/storage.ts` | 新增 | 讀/寫 `purdex-workspace-snapshot(-prev)`（走 `purdexStorage`） |
| `spa/src/lib/snapshot/capture.ts` | 新增 | `captureSnapshot`（讀兩 store + `listSessions` 補 meta） |
| `spa/src/lib/snapshot/restore.ts` | 新增 | `ensureSessions` / `remapLayoutSessions` / 三動作 orchestration + `-prev` 寫入 |
| `spa/src/components/settings/SnapshotSettingsSection.tsx` | 新增 | Settings section UI（兩區 + 健康度對帳 + 動作鈕 + toast） |
| section 註冊呼叫點（對齊既有 `*SettingsSection` 註冊處） | 修改 | `registerSettingsSection(...)` |

- **依賴方向**：UI → restore/capture → host-api + pane-tree + stores。純邏輯層（capture/restore/storage）不依賴 React，利於 Vitest 單元測試。

---

## 5. Phase 切分（依 review 大小）

### Phase 1 — 資料模型 + capture + 持久化
- `types.ts` / `storage.ts` / `capture.ts`。
- `captureSnapshot` 產出 `WorkspaceSnapshot`（含 sessionMeta 正確對應 code→name/cwd/command）、寫入 localStorage。
- **驗收**：mock 兩 store + mock `listSessions`，斷言快照內容正確；host 失敗時 `unresolved` 計數正確、不中斷。純邏輯，TDD。

### Phase 2 — restore 引擎 + 三動作
- `restore.ts`：`ensureSessions`（重映射表）、`remapLayoutSessions`（純函式）、三個 orchestration 動作、`-prev` 寫入。
- **驗收**：
  - 「部分活著、部分已死、host 離線」情境 → `oldCode→newCode` 表正確、layout 樹 code 正確改寫、失敗 pane 標 terminated。
  - 原子取代後兩 store 狀態 = 快照內容。
  - 三動作語意各自正確（重建 session 不動 tab 結構；還原 tab 佈局不重建 session；全部還原兩者兼具）。
  - `-prev` 於取代式動作前被寫入；「復原上次還原」能還回去。
  - **撞名邊界**：先寫一個測試釘住 daemon `createSession` 對同名 session 的行為（§8 探明後補斷言）。

### Phase 3 — Settings Snapshot section UI
- `SnapshotSettingsSection.tsx` + 註冊。
- 兩區呈現、掛載時即時健康度對帳、三顆動作鈕、toast 彙總、empty state。
- **驗收**：React Testing Library — 健康度三態渲染、動作鈕觸發對應 orchestration（mock restore 層）、快照不存在時 empty state。

---

## 6. 測試策略

- 純邏輯層（Phase 1/2）以 Vitest 單元測試為主，mock `host-api` 與兩 store（依 `feedback_zustand_harness_setstate` 的 merge-mode setState 慣例）。
- UI 層（Phase 3）用 RTL，mock restore/capture 層，聚焦互動與狀態渲染。
- 每個 Phase 獨立 commit；`cd spa && npx vitest run` + `pnpm run lint` + `pnpm run build` 全綠才進下一 Phase。

---

## 7. 限制（寫進 UI 說明文案）

- 重建的 session 只回到 **cwd**；原本在跑的程式不會自動重跑（僅顯示拍當下的 `current_command` 供參考）。
- `stream` mode session 重建後為空（Claude `-p` 串流不重播），同樣只還原 cwd。
- 快照為**單一份**，再拍即覆蓋。跨裝置不同步（本機 localStorage）。

---

## 8. 待探明 / 風險

1. **撞同名 session**（唯一可能碰 daemon 的點）：`createSession(hostId, name, cwd, mode)` 在 daemon 上已存在同 name 的 session 時的行為為何？（拒絕 / 自動改名 / 復用？）Phase 2 先寫一個測試/手動打 API 探明；若行為不利（例如靜默復用到錯的 session），再評估 daemon 端補「name 唯一性 / 回傳實際 name」。**這是 spec 唯一的開放技術問題。**
2. **host 可達性判定**：`ensureSessions` 需區分「session 死了但 host 活著（可重建）」vs「host 離線（不可重建）」。以 `listSessions` 是否成功回應作為 host 可達訊號。
3. **大量 session**：`ensureSessions` 對已死 session 逐一 `createSession`；session 很多時為序列/並行？plan 階段定（傾向有上限的並行 + 逐一失敗隔離）。
4. **capturedAt 決定性**：純函式不呼叫 `Date.now()`，由 UI 帶入，確保單元測試可決定性斷言。
