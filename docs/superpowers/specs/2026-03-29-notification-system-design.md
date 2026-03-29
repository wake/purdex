# 1.6c-pre2: CC 通知系統

## 概述

Agent hook 事件觸發 Electron 系統通知，使用者點擊通知跳轉到對應 tab。架構：SPA 判斷 + Electron 執行（方案 C），符合現有「Electron 是殼、邏輯在 SPA」原則。

## 依賴

- 1.6c-pre1 Agent Hook 狀態偵測（✅ alpha.24）

## 架構

```
Agent hook event (WS)
  │
  ▼
useAgentStore.handleHookEvent()
  │
  ▼
useNotificationDispatcher() [判斷是否通知]
  ├─ derived === 'waiting' || 'idle'?
  ├─ session 有對應 tab?（或 notifyWithoutTab 開啟?）
  ├─ focusedSession !== session?
  ├─ event_name 在通知設定中啟用?
  └─ 全部通過 ↓
      │
      ▼
  ┌─────────────────────────────────────┐
  │ Electron:                           │
  │ electronAPI.showNotification(opts)  │
  │ → IPC → main: new Notification()   │
  │                                     │
  │ PWA (預留):                         │
  │ new Notification(title, { body })   │
  └─────────────────────────────────────┘
      │
      ▼ 點擊
  notification:clicked { sessionCode }
  → findTabBySessionCode → setActiveTab + focus window
```

## §1: Daemon 改動（agent_type 欄位）

### tbox hook CLI

- 新增 `--agent` flag：`tbox hook --agent cc Notification`
- `hookPayload` 新增 `AgentType string` 欄位（`json:"agent_type"`）
- `tbox setup` 產生的 hook command 自動帶 `--agent cc`

### Daemon storage

- `agent_events` 表新增 `agent_type TEXT NOT NULL DEFAULT ''`
- `AgentEvent` struct 新增 `AgentType string`
- API 回傳 / WS 廣播的 JSON 帶 `agent_type`

### WS 廣播 broadcast_ts

- daemon 廣播時附加 `broadcast_ts`（`time.Now().UnixNano()`）
- 用於 Electron main process 多視窗去重（同一次廣播的所有 subscriber 收到同一個 timestamp）
- 不需存 SQLite

### Migration

- SQLite `ALTER TABLE agent_events ADD COLUMN agent_type TEXT NOT NULL DEFAULT ''`

## §2: Electron IPC 層

### 新增 IPC channels

**`notification:show`**（renderer → main）：

```ts
interface NotificationShowPayload {
  title: string          // session 名稱
  body: string           // 事件內容
  sessionCode: string    // 用於點擊跳轉
  eventName: string      // hook event name
  broadcastTs: number    // daemon 廣播 timestamp，用於去重
}
```

**`notification:clicked`**（main → renderer）：

```ts
interface NotificationClickedPayload {
  sessionCode: string
}
```

### main.ts

- `ipcMain.handle('notification:show', ...)` → `new Notification({ title, body })`
- `.on('click')` → 發送 `notification:clicked` 回所有 renderer + `win.show()` + `win.focus()`
- 多視窗去重：維護 `Set<number>`（近 5 秒的 broadcastTs），見過即丟棄，setTimeout 自動清除

### preload.ts

- `showNotification(opts: NotificationShowPayload): Promise<void>`
- `onNotificationClicked(callback: (payload: NotificationClickedPayload) => void): () => void`

### platform.ts

- `PlatformCapabilities` 新增 `canNotification: boolean`（`= isElectron`）

## §3: SPA 通知判斷邏輯

### useNotificationDispatcher hook

在 `App.tsx` 掛載一次，subscribe `useAgentStore` 事件。獨立 hook，不污染 agent store。

**判斷流程**（依序）：

1. `derived` 是 `waiting` 或 `idle`？
2. 通知總開關 `enabled`？
3. 該 `event_name` 在 per-agent 設定中啟用？
4. 該 session 有對應的已開 tab？（沒有 → 檢查 `notifyWithoutTab`）
5. `focusedSession !== session`？（正在看的不通知）
6. 全部通過 → 送出通知

### 通知內容組裝

從 `raw_event` 提取，不截斷（OS 自行處理）：

| event_name | title | body |
|------------|-------|------|
| `Notification` | `{session.name}` | `raw_event.message` |
| `PermissionRequest` | `{session.name}` | `需要授權：${raw_event.tool_name}` |
| `Stop` | `{session.name}` | `raw_event.last_assistant_message` |

### 執行層抽象

```ts
if (capabilities.canNotification) {
  window.electronAPI.showNotification(opts)
} else if ('Notification' in window && Notification.permission === 'granted') {
  new Notification(opts.title, { body: opts.body })
}
```

PWA 共用判斷邏輯，只換執行層。PWA 不需多視窗去重。

## §4: 通知設定 Store

### useNotificationSettingsStore（Zustand + persist）

```ts
interface NotificationSettings {
  enabled: boolean                    // 總開關，預設 true
  events: Record<string, boolean>     // per event_name，預設全 true
  notifyWithoutTab: boolean           // tab 關閉後仍通知，預設 false
  reopenTabOnClick: boolean           // 點擊通知重開 tab，預設 false
}

interface NotificationSettingsState {
  agents: Record<string, NotificationSettings>  // key = agent_type
  getSettingsForAgent(agentType: string): NotificationSettings
}
```

- 新 agent type 首次出現 → 自動用預設值
- `reopenTabOnClick` 只在 `notifyWithoutTab` 開啟時可操作
- host / workspace 層：型別預留 `scope?: 'system' | 'host' | 'workspace'`，邏輯先只做 system

## §5: Agent Settings UI

### Settings Section

在 Settings Section Registry 註冊 `agent` section。

**結構**：

- Section 標題：「Agent」
- 按 `agent_type` 動態產生子區塊（從 `useAgentStore.events` 收集已出現過的 agent_type）
- 沒收到過事件的 agent type 不顯示
- 每個子區塊：
  - 總開關 toggle
  - Event 清單：每個 event_name 一個 toggle
  - 進階：`notifyWithoutTab` toggle + `reopenTabOnClick` toggle（後者依賴前者）

### i18n

- `agent.cc.name`: "Claude Code"
- `agent.cc.event.Notification`: "通知"
- `agent.cc.event.PermissionRequest`: "權限請求"
- `agent.cc.event.Stop`: "任務完成"

## §6: 點擊通知 → 跳轉 tab

### 流程

```
Electron main: Notification.on('click')
  → IPC 'notification:clicked' { sessionCode }
  → 聚焦視窗 (win.show() + win.focus())

SPA: onNotificationClicked callback
  → findTabBySessionCode(sessionCode)
  → 有 tab → setActiveTab(tabId) + setFocusedSession(sessionCode)
  → 沒 tab + reopenTabOnClick → createTab({ kind: 'session', sessionCode, mode: 'stream' })
  → 沒 tab + !reopenTabOnClick → 只聚焦視窗
```

### findTabBySessionCode

遍歷所有 tab，`getPrimaryPane(tab.layout)` 找 `content.kind === 'session' && content.sessionCode === target`。放在 `pane-tree.ts` 作為 utility。

### 多視窗聚焦

Main process 不知道各視窗的 tab 狀態，因此：

1. Main 廣播 `notification:clicked` 到**所有** renderer
2. 每個 SPA 檢查自己是否有該 sessionCode 的 tab
3. 有 tab 的 SPA 回覆 IPC `notification:focus-window` 請求聚焦自己的視窗
4. Main 收到後 `win.show()` + `win.focus()`
5. 沒有任何 SPA 回覆（所有視窗都沒有該 tab）→ 由 reopenTabOnClick 設定決定，主視窗處理

## §7: PWA 路徑（預留）

- 判斷邏輯完全共用 `useNotificationDispatcher`
- 執行層：`new Notification(title, { body })`
- 點擊回調：`notification.onclick` → `findTabBySessionCode` 跳轉
- 不需多視窗去重

## §9: Hook 設定狀態檢視

### Daemon 端點

`GET /api/agent/hook-status` — 讀取 `~/.claude/settings.json`（或對應 agent 的設定檔），回傳：

```json
{
  "agent_type": "cc",
  "installed": true,
  "events": {
    "SessionStart": { "installed": true, "command": "/usr/local/bin/tbox hook --agent cc SessionStart" },
    "Notification": { "installed": true, "command": "/usr/local/bin/tbox hook --agent cc Notification" },
    "Stop": { "installed": false, "command": null }
  },
  "issues": ["Stop hook 未安裝"]
}
```

### Agent Settings UI 整合

在 §5 的 agent type 子區塊頂部顯示 hook 狀態：

- **已安裝**：綠燈 + 各 event hook 狀態清單
- **未安裝 / 部分安裝**：警告提示 + 「安裝 Hook」按鈕
- 「安裝 Hook」按鈕觸發 daemon 端點執行 `tbox setup --agent cc`
- 「移除 Hook」按鈕觸發 `tbox setup --agent cc --remove`

### Daemon 端點：安裝 / 移除

- `POST /api/agent/hook-setup` body: `{ "agent_type": "cc", "action": "install" | "remove" }`
- 內部呼叫 `tbox setup --agent cc` 或 `tbox setup --agent cc --remove`
- 回傳操作結果 + 更新後的 hook 狀態

## §8: 測試策略

### SPA 單元測試（Vitest）

- `useNotificationSettingsStore` — 預設值、per-agent 讀寫、toggle 連動
- `useNotificationDispatcher` 判斷邏輯 — 各條件組合（focused 不通知、無 tab 不通知、設定關閉）
- 通知內容組裝 — 各 event_name 的 title/body 提取
- `findTabBySessionCode` — 有 tab / 無 tab / 多 tab

### Daemon 測試（Go test）

- `tbox hook --agent cc` flag parsing
- `agent_events` 表 `agent_type` 欄位存取
- WS 廣播帶 `broadcast_ts` + `agent_type`
- `GET /api/agent/hook-status` 回傳格式
- `POST /api/agent/hook-setup` install / remove

### 不測

- Electron `new Notification()`（系統 API）
- IPC 傳輸（Electron 框架保證）

## 設定三層級（需求記錄）

未來實作 host / workspace 層時：
- system settings 為基礎預設
- host 層可覆寫（per-daemon 連線）
- workspace 層可覆寫（per-workspace）
- 合併策略：workspace > host > system，缺值向上 fallback
