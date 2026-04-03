# Phase 4 — 錯誤 UI 設計規格

> 2026-04-04 — 基於 Phase 3 連線偵測基礎，實作完整的錯誤 UI、tab 終結狀態、host 刪除流程
>
> 注：原始架構規格中的 `SessionDestroyed` 元件在本規格中更名為 `TerminatedPane`，以更準確反映用途（涵蓋 session 關閉、tmux 重啟、host 刪除等多種終結情境）。

## 一、概述

Phase 4 為 Host 連線管理的主線最後一個 Phase，目標是讓使用者在各種錯誤情境下獲得清晰的資訊回饋和操作路徑。涵蓋：

- Tab 狀態模型（含 terminated 終結狀態）
- Terminated 錯誤頁元件
- Host 層級 L1-L3 錯誤 UI
- Host 刪除流程 + cascade cleanup + undo
- L2/L3 系統通知 + bug fixes

## 二、Tab 狀態模型

### PaneContent 類型更新

```typescript
export type PaneContent =
  | { kind: 'new-tab' }
  | { kind: 'tmux-session'; hostId: string; sessionCode: string; mode: 'terminal' | 'stream'; cachedName: string; tmuxInstance: string; terminated?: TerminatedReason }
  | { kind: 'dashboard' }
  | { kind: 'hosts' }
  | { kind: 'history' }
  | { kind: 'settings'; scope: 'global' | { workspaceId: string } }
  | { kind: 'browser'; url: string }
  | { kind: 'memory-monitor' }

type TerminatedReason = 'session-closed' | 'tmux-restarted' | 'host-removed'
```

**Rename**：`kind: 'session'` → `kind: 'tmux-session'`，涉及約 31 個檔案（原始碼 + 測試）的 kind 比對，機械性替換。具體檔案數以實作時 grep 結果為準。

**Zustand Persist Migration**：TabStore 的 persist version 從 1 升到 2，加入 `migrate` 函式，將舊資料的 `kind: 'session'` 轉換為 `kind: 'tmux-session'`。否則 localStorage 中已儲存的 tab 會變成 unknown kind，導致渲染失敗。

### Tab 顯示狀態推導

Tab 狀態不是獨立欄位，而是從 PaneContent + HostRuntime 推導：

```typescript
function deriveTabState(content: PaneContent, runtime?: HostRuntime) {
  if (content.kind !== 'tmux-session') return 'active'
  if (content.terminated) return 'terminated'
  if (runtime?.status === 'reconnecting') return 'reconnecting'
  return 'active'
}
```

Phase 4 的 tab 狀態處理只涉及 `tmux-session` kind，其他 kind 不受影響。

### Tab Icon / Name 對應

| 狀態 | Icon | Tab Name |
|------|------|----------|
| active | Terminal / Stream（依 mode） | `{sessionName}` |
| reconnecting | Spinner | `{cachedName}` |
| terminated | SmileySad（類 Chrome crashed） | `{cachedName}（Terminated）` |

> 注：terminated 使用 SmileySad 而非 Warning icon，是因為 Warning 保留給 reconnecting/disconnected 的 host 層級指示（HostSidebar L3），SmileySad 專門表達「此 tab 已終結、不可自動恢復」的語義。原始架構規格中的 `disconnected / error → Warning` 指的是 host 層級狀態。

### Terminated 生命週期

`terminated` 欄位記錄的是「發生過的事件」，不是可推導的狀態。觸發終結的上下文（session list 變化、tmux instance 變化、host 刪除）是瞬間的，之後會消失，因此需要在偵測到時主動寫入 PaneContent。

**寫入時機：**

| 事件 | 偵測位置 | Reason |
|------|---------|--------|
| Session 從 list 消失 | `useMultiHostEventWs` | `session-closed` |
| tmuxInstance 變化 | `useMultiHostEventWs` | `tmux-restarted` |
| 使用者刪除 host | `removeHost()` | `host-removed` |

**Session 消失偵測邏輯**：`useMultiHostEventWs` 收到 `sessions` 事件時，比對新 session list 與 TabStore 中所有 pane（含 split pane 的 secondary pane，需掃描完整 pane tree），找出 `content.kind === 'tmux-session' && content.hostId === hostId` 且 `sessionCode` 不在新 list 中的 pane，寫入 `terminated: 'session-closed'`。

**tmux-restarted 偵測邏輯**：Daemon 端在 `sessions` 事件 payload 中附帶 `tmuxInstance`。`useMultiHostEventWs` 收到 sessions 事件時，比對 payload 中的 tmuxInstance 與各 tab PaneContent 中存的值，若不一致則對該 host 所有 tmux-session tab 寫入 `terminated: 'tmux-restarted'`。需 daemon 端配合修改 sessions 廣播格式。

**清除時機：**

| 事件 | 結果 |
|------|------|
| 使用者在錯誤頁選新 session | 覆寫 PaneContent（新 hostId、sessionCode 等），`terminated` 消失 |
| 使用者點關閉按鈕 | 整個 PaneContent 從 TabStore 移除 |

## 三、Terminated 錯誤頁元件

### 元件結構

```
TerminatedPane
├── 錯誤訊息（依 reason 不同）
├── 關閉按鈕
└── SessionPickerList（只列已連線 host 的 session，按 host 分組）
```

### 錯誤訊息

| Reason | 主訊息 | 補充說明 |
|--------|------|---------|
| `session-closed` | Session 已關閉 | `{cachedName}` 已不存在 |
| `tmux-restarted` | tmux 已重啟 | 原有 session 已失效 |
| `host-removed` | Host 已移除 | 該主機已從列表中移除 |

### SessionPickerList 子元件

- 資料來源：遍歷 HostStore 所有 host，篩選 `runtime.status === 'connected'`，從 SessionStore 取各 host 的 session list
- 按 host 分組顯示，每組標題為 host 名稱
- 點選 session → 覆寫當前 tab 的 PaneContent（新 hostId、sessionCode、tmuxInstance、cachedName），`mode` 沿用原 tab 的值，`terminated` 清除，tab 回到 active
- 若無任何已連線 host → 顯示「目前沒有可用的連線」提示
- SessionStore 的 `sessions` 不做持久化，資料來自 WS `sessions` 事件的被動填充。正常情況下 terminated 頁面顯示時 WS 已建立、sessions 已有資料。若 sessions 為空（如 WS 尚未連線），顯示上述提示即可，不需額外觸發 fetch
- 此元件可被 TerminatedPane 和未來 new-tab 頁面共用

### 渲染進入點

在 `SessionPaneContent.tsx`（`tmux-session` kind 的渲染器）中加入判斷：

```typescript
if (content.terminated) return <TerminatedPane content={content} tabId={tabId} paneId={paneId} />
// 否則正常渲染 TerminalView / ConversationView
```

### 新增檔案

- `spa/src/components/TerminatedPane.tsx` — 錯誤頁主元件
- `spa/src/components/SessionPickerList.tsx` — 跨 host session list

## 四、Host 層級錯誤 UI（L1-L3）

### 各元件錯誤行為

| 元件 | L1（unreachable） | L2（refused） | L3（tmux down） |
|------|-------------------|---------------|-----------------|
| **HostSidebar StatusIcon** | 紅色 Circle | 紅色 Circle | 黃色 Warning |
| **StatusBar** | 紅色 `Disconnected` | 紅色 `Disconnected` | 黃色 `tmux unavailable` |
| **SessionPanel** | Session list disabled + 錯誤提示 | 同 L1 | Session list disabled +「tmux 未啟動」 |
| **OverviewSection** | 「主機無法連線」 | 「Daemon 未啟動」 | 「tmux 環境無法連線」 |

### L1 vs L2 區分

已在 Phase 3 實作，從 `HostRuntime.daemonState` 讀取：

- `unreachable`（timeout）→ L1：主機本身不可達
- `refused`（快速失敗）→ L2：主機可達但 daemon 沒跑

### Reconnecting Overlay

維持現有行為：terminal/stream 畫面上蓋半透明 overlay。新增手動重連按鈕：

- 按鈕在 overlay 上，文字「重新連線」
- 點擊後顯示 spinner，成功或失敗後恢復按鈕狀態
- 按鈕 spinner 結束條件：訂閱 `HostRuntime.status`，當 status 變為 `connected` 或確認仍 `disconnected` 後恢復按鈕
- 觸發機制：透過 `useHostConnection(hostId)` hook 提供的 `manualRetry()` 函式呼叫 `ConnectionStateMachine.trigger()`

### 手動重連按鈕

OverviewSection 已有 Test Connection 按鈕，視為 host 層級的手動重連等效操作。Reconnecting Overlay 的重連按鈕為 tab 層級。其他頁面（SessionPanel、TerminatedPane）不額外提供重連按鈕。

### SessionPanel disable 行為

Host 斷線（L1/L2）或 tmux down（L3）時：

- Session list 項目變 muted，不可點擊開新 tab
- 列表上方顯示一行錯誤提示（與 OverviewSection 同訊息）

## 五、Host 刪除流程

### 刪除 UI

在 OverviewSection 現有 inline 確認區塊中增加 checkbox：

```
確定要刪除？
☑ 一併關閉所有此 Host 的分頁
[刪除]  [取消]
```

Checkbox 預設 checked。

### 刪除行為

確認後：

- **Checked**：關閉該 host 所有 `tmux-session` tab + cascade cleanup 所有 store
- **Unchecked**：tab 標記 `terminated: 'host-removed'`（保留） + cascade cleanup（不含 TabStore）

### Cascade Cleanup

不論 checkbox 狀態，刪除 host 時都執行：

| Store | 清理動作 |
|-------|---------|
| HostStore | 移除 `hosts[id]`、`hostOrder`、`runtime[id]` |
| SessionStore | `removeHost(id)` — 清除 `sessions[id]` |
| AgentStore | 清除該 host 的 composite key 資料：`events`、`statuses`、`unread`、`activeSubagents`（需新增 `removeHost(hostId)` action，掃描所有 `hostId:*` 前綴 key）。新 `removeHost` 涵蓋現有 `clearSubagentsForHost` 的功能，後者應被移除以避免重複 |
| StreamStore | 清除該 host 的 sessions、relayStatus、handoffProgress（需新增 `clearHost(hostId)` action，掃描所有 `hostId:*` 前綴 composite key） |
| WS 連線 | `useMultiHostEventWs` 透過 `hostOrderKey` 變化自動 cleanup |

### Undo Toast

刪除後底部出現 undo toast：

- 內容：「已刪除 {hostName}」+「復原」按鈕
- 5 秒倒數後自動消失
- 點擊復原 → 從 snapshot 還原所有受影響的 store 資料（含 tab 狀態）
- 實作方式：刪除前做 snapshot（受影響 store 的資料），存在記憶體中，復原 = 把 snapshot 寫回各 store
- Snapshot 只保存可序列化資料：StreamStore 的 `conn: StreamConnection` 等連線物件不納入 snapshot，undo 後 stream tab 回到「有歷史訊息但未建立連線」狀態，切換到該 tab 時自然重建 WS
- 衝突處理：undo 期間如果 snapshot 中的 tab 已被使用者覆寫（例如選了新 session），則略過不還原該 tab
- 最後一個 host 不可刪：維持現有 HostStore `removeHost` 的防呆（`Object.keys(hosts).length <= 1` 時不執行），刪除按鈕 disabled

### Terminated Tab 的 WS 隔離

`terminated` 狀態的 tab 不觸發任何 WS 連線。在 `useTerminalWs` 和 `SessionPaneContent` 中加 guard：

```typescript
if (content.terminated) return  // 不建立 WS
```

此修復同時解決 #159（ghost reconnect on deleted host）。

> 注：刪除 host 時，`hostOrderKey` 變化觸發 `useMultiHostEventWs` 的 cleanup effect re-run，SM 會在 cleanup 中被 `stop()`，不影響 terminated 寫入時序。即使 tab 正處於 reconnecting 狀態，`deriveTabState` 的優先順序為 `terminated` > `reconnecting`，因此 terminated 寫入後 tab 狀態立即正確。

## 六、通知

### L2/L3 Electron 系統通知

在 `useNotificationDispatcher` 擴充連線狀態通知：

| 觸發 | 條件 | 通知內容 |
|------|------|---------|
| L2（daemon refused） | `daemonState` 從 `connected` 變為 `refused` | 「{hostName} — Daemon 未啟動」 |
| L3（tmux down） | `tmuxState` 從 `ok` 變為 `unavailable` | 「{hostName} — tmux 環境無法連線」 |
| L1（unreachable） | 不發通知 | — |

規則：

- 每 host 每次斷線只發一次（恢復後再斷才會再發）
- 使用 `Notification` API（瀏覽器）或 Electron `notification` module
- 點擊通知 → 切換到該 host 的 overview 頁面

## 七、Bug Fixes

### #161 — checkHealth JSON parse error

`useMultiHostEventWs` 中 `JSON.parse(event.value)` 無 try-catch。修復：wrap try-catch，parse 失敗 log warning，不影響連線狀態。

### #156 — AddHostDialog /api/info 失敗無提示

目前 fetch 失敗時無回饋。修復：加入錯誤訊息顯示，區分 L1（主機無法連線）/ L2（Daemon 未啟動）/ 401（Token 無效）。

### #140 — TokenField 清空時誤顯 Invalid token

使用者清空 token 欄位時觸發驗證 → 空值送 API → 401 → 顯示錯誤。修復：空值不觸發驗證，清除錯誤訊息。

### #137 — Electron 離線 renderer crash

Electron 啟動時 daemon 不可達 → renderer 未處理錯誤 → 白屏。修復：Phase 3 ConnectionStateMachine 已處理底層，Phase 4 補 UI 層 — 確保 L1 狀態有正確的錯誤頁面而非白屏。

## 八、i18n Key 清單

Phase 4 新增的所有 UI 文字需同步加入 `en.json` 和 `zh-TW.json`：

| Key | en | zh-TW |
|-----|----|-------|
| `terminated.session_closed` | Session closed | Session 已關閉 |
| `terminated.session_closed_desc` | {name} no longer exists | {name} 已不存在 |
| `terminated.tmux_restarted` | tmux restarted | tmux 已重啟 |
| `terminated.tmux_restarted_desc` | Previous sessions are no longer valid | 原有 session 已失效 |
| `terminated.host_removed` | Host removed | Host 已移除 |
| `terminated.host_removed_desc` | This host has been removed | 該主機已從列表中移除 |
| `terminated.no_sessions` | No available connections | 目前沒有可用的連線 |
| `terminated.close_tab` | Close tab | 關閉分頁 |
| `terminated.select_session` | Select a session to reconnect | 選擇 session 以重新連線 |
| `hosts.confirm_delete_tabs` | Also close all tabs for this host | 一併關閉所有此 Host 的分頁 |
| `hosts.deleted_toast` | Deleted {name} | 已刪除 {name} |
| `hosts.undo` | Undo | 復原 |
| `hosts.error_unreachable` | Host unreachable | 主機無法連線 |
| `hosts.error_refused` | Daemon not running | Daemon 未啟動 |
| `hosts.error_tmux_down` | tmux unavailable | tmux 環境無法連線 |
| `connection.reconnect` | Reconnect | 重新連線 |
| `notification.daemon_refused` | {name} — Daemon not running | {name} — Daemon 未啟動 |
| `notification.tmux_down` | {name} — tmux unavailable | {name} — tmux 環境無法連線 |

## 九、Sub-Phase 拆分

### Phase 4a — Tab 狀態基礎建設

- `kind: 'session'` → `kind: 'tmux-session'` rename（約 31 個檔案，含 `useNotificationDispatcher.ts`）
- Zustand persist migration（version 1 → 2）
- PaneContent 加 `terminated?: TerminatedReason`
- Tab icon 對應（SmileySad for terminated）
- Tab name 對應（`{cachedName}（Terminated）`）
- `deriveTabState()` 推導函式（放在 `spa/src/lib/tab-state.ts`）
- i18n key 新增（第八節清單）

### Phase 4b — Terminated 錯誤頁元件

- `TerminatedPane` 元件（訊息 + 關閉按鈕 + session list）
- `SessionPickerList` 元件（跨 host，只列 connected）
- Tab 重新綁定：選 session 後覆寫 PaneContent
- `SessionPaneContent` 加入 terminated 判斷分支
- Terminated 偵測寫入（useMultiHostEventWs 中 session 消失 / tmux 重啟）

### Phase 4c — Host 層級錯誤 UI

- StatusBar / SessionPanel / HostSidebar / OverviewSection 的 L1-L3 錯誤顯示
- Reconnecting overlay 手動重連按鈕
- SessionPanel disable 行為

### Phase 4d — Host 刪除流程 + Cascade Cleanup

- 刪除確認 UI 加 checkbox（一併關閉分頁）
- Cascade cleanup（SessionStore、AgentStore、StreamStore）
- Undo toast + snapshot 復原
- Terminated tab WS 隔離（同時修復 #159）

### Phase 4e — 通知 + Bug fixes

- L2/L3 Electron 系統通知
- Bug fix: #137、#140、#156、#161
- `HEALTH_TIMEOUT_MS` 從 3000 改為 6000（對應 F-2 決策）

### Sub-Phase 依賴關係

```
4a (rename + terminated field + i18n + migration)
 ├──→ 4b (TerminatedPane 元件 + 偵測寫入)
 ├──→ 4c (Host 層級錯誤 UI)        ← 4b 與 4c 可並行
 └──→ 4d (刪除流程 + cascade)      ← 僅依賴 4a，不需等 4b/4c
          └──→ 4e (通知 + bug fixes)
```

## 十、設計決策記錄

### C-2 — SM 存取方式（已決定：useHostConnection hook）

新增 `useHostConnection(hostId)` hook，封裝 `ConnectionStateMachine` 的存取，對外提供 `manualRetry()` 函式。Reconnecting Overlay 透過此 hook 觸發手動重連。按鈕 spinner 結束條件為訂閱 `HostRuntime.status` 變化。

### C-3 — tmuxInstance 廣播來源（已決定：sessions 事件附帶）

Daemon 端在 `sessions` 事件 payload 中附帶 `tmuxInstance`（`pid:startTime`）。SPA 在 `useMultiHostEventWs` 收到 sessions 事件時，比對 payload 中的 tmuxInstance 與各 tab PaneContent 中存的值，若不一致則寫入 `terminated: 'tmux-restarted'`。需要 daemon 端配合修改 sessions 廣播格式。

### F-2 — Background retry timeout

`checkHealth` 的 timeout 從 3s 調整為 6s，讓不穩定網路有更多緩衝。背景重連的實際節奏變為 `6s timeout + 100ms delay ≈ 每 6 秒一次`。

### F-3 — 通知 click handler 模組化

現有 `handleNotificationClick` 只支援跳到 session tab。改為 action payload 模式，通知攜帶導航意圖（純資料），中央 dispatcher 解讀：

```typescript
type NotificationAction =
  | { kind: 'open-session'; hostId: string; sessionCode: string }
  | { kind: 'open-host'; hostId: string }
  // 未來擴充：| { kind: 'open-file'; hostId: string; path: string }

function handleNotificationClick(action: NotificationAction) {
  switch (action.kind) {
    case 'open-session': // 找到或開啟 session tab
    case 'open-host':    // 切換到 hosts 頁面 + 選中 hostId
  }
}
```

新增通知類型只需加一個 `NotificationAction` variant + 一個 switch case。不依賴 tab 是否已存在。Phase 4e 將現有 agent 通知遷移到此模式，並加入 L2/L3 host 通知。

`open-host` 的導航實作：呼叫 `openSingletonTab({ kind: 'hosts' })` 切到 hosts 頁面，再呼叫 `setActiveHost(hostId)` 選中目標 host。

## 十一、關聯 Issues

| Issue | 對應 Sub-Phase | 處理方式 |
|-------|---------------|---------|
| #137 | 4e | 補 L1 錯誤 UI，避免白屏 |
| #140 | 4e | 空值不觸發驗證 |
| #156 | 4e | 加入 L1/L2/401 錯誤訊息 |
| #159 | 4d | terminated tab 不觸發 WS |
| #161 | 4e | JSON.parse try-catch |
