# 1.6c-pre1: Agent Hook 狀態偵測

## 概述

用 CC hooks 取代 poller 的 CC status detection，實現即時 agent 狀態推送。Daemon 作為純 relay，不解析 agent payload，所有狀態判斷由 SPA 負責。

## 架構

```
CC (tmux pane 內)
  │ hook 觸發
  ▼
tbox hook <event_name>（子命令，同一個 binary）
  ├─ 讀 stdin（raw JSON blob）
  ├─ tmux display-message -p '#{session_name}' → tmux session
  └─ POST /api/agent/event
      │
      ▼
Daemon（agent module，純 relay）
  ├─ 存 tmux_session + event_name + raw_event（MetaStore）
  └─ 廣播到 session-events WS（type: "hook"）
      │
      ▼
SPA
  ├─ agent store 接收 hook 事件
  ├─ cc agent module 狀態機解釋 → running / waiting / idle+unread
  ├─ Tab 燈號 + Session Panel 燈號 + StatusBar 更新
  └─ unread 管理（tab 聚焦時清除）
```

## 一、tbox hook 子命令

### 用法

```bash
tbox hook <event_name>
# stdin: CC hook 的原始 JSON
```

### 職責

1. 讀 stdin → raw payload（不解析內容）
2. `tmux display-message -p '#{session_name}'` → tmux session name
3. POST `/api/agent/event` → 送出

### POST body

```json
{
  "tmux_session": "my-project",
  "event_name": "Stop",
  "raw_event": { "...stdin 原始內容..." }
}
```

### 設計要點

- **快速失敗**：任何錯誤直接 exit 0，不阻塞 CC
- **Daemon 位址**：讀 `~/.config/tbox/config.toml` 的 `bind` + `port`，fallback `127.0.0.1:7860`
- **不在 tmux 內**：`tmux display-message` 失敗時 `tmux_session` 送空字串
- **raw_event 型別**：巢狀 JSON object，不做 string escape
- **程式碼位置**：`cmd/tbox/` 下新增 hook 子命令

## 二、Daemon 端

### 新建 agent module

獨立 module，作為 agent 的通用處理層。

```go
type Module struct { /* ... */ }
func (m *Module) Name() string        { return "agent" }
func (m *Module) Dependencies() []string { return []string{"session"} }
func (m *Module) RegisterRoutes(mux *http.ServeMux) {
    mux.HandleFunc("POST /api/agent/event", m.handleEvent)
}
```

### POST /api/agent/event

收到後做三件事：

1. 以 `tmux_session` 為 key，存入 MetaStore（覆寫前一筆）
2. 查 `tmux_session` → session code（透過 session module 映射）
3. 廣播到 session-events WS

找不到 session code 時：仍存入 MetaStore，不廣播。下次 session list 查詢時自然匹配。

### MetaStore 儲存

| 欄位 | 來源 | 說明 |
|------|------|------|
| `tmux_session` | tbox hook 查 tmux | 外部資訊 |
| `event_name` | tbox hook CLI 參數 | 外部資訊 |
| `raw_event` | stdin blob | 完全不動 |

三個欄位都不需要打開 payload。Daemon 不解析、不判斷 agent 狀態。

### WS 事件格式

```json
{
  "type": "hook",
  "session": "a1b2c3",
  "value": "{\"tmux_session\":\"my-project\",\"event_name\":\"Stop\",\"raw_event\":{...}}"
}

```

沿用現有 `SessionEvent{Type, Session, Value string}` 結構。

### 新 subscriber snapshot

新 WS 連線建立時，送出每個有紀錄的 session 的最近一筆 hook event。

## 三、Poller 移除

- **完全移除** poller（`cc/poller.go` 的 CC status detection + polling loop）
- **Orphan cleanup** 移入 `GET /api/sessions` handler，查詢時順帶清理
- `sendStatusSnapshot` 由 agent module 的 hook snapshot 取代
- `"relay"` / `"handoff"` WS 事件不變

## 四、SPA 事件處理

### 新建 agent store

獨立於 stream store，管理 hook 驅動的 agent 狀態。

`useStreamStore` 的 `sessionStatus` 欄位移除，由 agent store 取代。`relayStatus` 和 `handoffProgress` 留在 stream store。

### useSessionEventWs 新增 hook 處理

```typescript
case "hook":
  // parse value JSON → 交給 agent module
  agentModule.handleHookEvent(session, parsed)
```

### CC Agent Module 狀態機

| event_name | → 狀態 | 備註 |
|------------|--------|------|
| `SessionStart` | running | |
| `UserPromptSubmit` | running | |
| `Stop` | idle + unread（如果 tab 非前景） | |
| `Notification` | waiting | 從 raw_event 判斷子類型 |
| `PermissionRequest` | waiting | |
| `SessionEnd` | clear（移除狀態） | |

### unread 管理

- **標記 unread**：收到 Stop / Notification(idle) 時，該 session 的 tab 不在前景
- **清除 unread**：使用者切到該 tab

### Handoff 參數傳遞

SPA 從 hook 事件解出 `agent_session_id`，呼叫 handoff / history API 時主動帶參數。Daemon 不需自己查。

## 五、UI 設計

### Tab 燈號

三種樣式，Settings 提供選項，**預設 A**：

**A（預設）— Icon + overlay 燈號**
- TerminalWindow fill icon（14px）保留
- 6px 狀態燈在 icon 右上角，偏移 3px（`top:0; right:-1`）
- 黑邊 1.5px（`box-shadow`），顏色跟隨 tab 背景

**B — 燈號取代 Icon**
- Agent 模式下 icon 移除，改為 8px 狀態 dot
- 非 agent 保留原本 icon

**C — Icon + inline 燈號**
- Icon 保留，6px dot 排在 icon 後方、label 前方
- Gap 縮為 4px

### 狀態燈顏色

| 狀態 | 顏色 | 動畫 |
|------|------|------|
| running | `#4ade80` 綠 | 呼吸燈（`background-color` fade 到 tab 底色） |
| waiting | `#facc15` 黃 | 無 |
| idle | `#6b7280` 灰 | 無 |
| 非 agent | 不顯示 | — |

呼吸燈 fade 使用 `background-color` 動畫到 tab 底色（active: `var(--surface-active)`，inactive: `var(--surface-secondary)`），不使用 opacity。

### 未讀指示

- 5px 暗紅 `#b91c1c` 圓點
- Tab 右上角內縮 2px（`top:2; right:4`）
- 只在 inactive tab + unread 時顯示

### Session Panel 燈號

- 位置：name 和 code 之間（code 前方）
- 8px dot，顏色同 tab 燈號定義
- 不處理 unread
- 非 agent 不顯示

### StatusBar

- 有 agent 時：顯示 agent 名稱 + 版本（例如 `Claude Code 1.0.32`）
- 無 agent 時：不顯示 agent 資訊
- 其餘（host / session / connection / view mode toggle）不變

## 六、Setup 機制

### tbox setup 子命令

```bash
tbox setup
```

自動將 hook 配置寫入 `~/.claude/settings.json`：

```json
{
  "hooks": {
    "SessionStart":      [{"hooks": [{"type": "command", "command": "/full/path/to/tbox hook SessionStart"}]}],
    "UserPromptSubmit":  [{"hooks": [{"type": "command", "command": "/full/path/to/tbox hook UserPromptSubmit"}]}],
    "Stop":              [{"hooks": [{"type": "command", "command": "/full/path/to/tbox hook Stop"}]}],
    "Notification":      [{"hooks": [{"type": "command", "command": "/full/path/to/tbox hook Notification"}]}],
    "PermissionRequest": [{"hooks": [{"type": "command", "command": "/full/path/to/tbox hook PermissionRequest"}]}],
    "SessionEnd":        [{"hooks": [{"type": "command", "command": "/full/path/to/tbox hook SessionEnd"}]}]
  }
}
```

### 設計要點

- **完整路徑**：`tbox setup` 自動偵測自身位置，寫入絕對路徑
- **不覆蓋既有 hook**：追加而非取代
- **冪等**：比對 command 欄位，不產生重複
- **`tbox setup --remove`**：移除 tbox 的 hook 條目
- **提示重啟**：修改後提示使用者重啟 CC 生效

## 七、已在跑的 CC

不處理。Hook 配好後，CC 還沒發生互動前 SPA 連上 → daemon 無該 session 事件 → 顯示 normal。使用者跟 CC 互動後 hook 觸發，狀態自動歸位。

## 八、與現有功能的關係

- **Poller**：完全移除 CC status detection，orphan cleanup 移入 sessions handler
- **WS "status" 事件**：由 "hook" 取代
- **WS "relay" / "handoff"**：不變
- **Handoff**：SPA 主動帶 agent_session_id 參數，daemon 不查 MetaStore
- **History API**：同上，SPA 帶 cc_session_id + cwd

## 九、不在此 phase 範圍

- Electron 系統通知（1.6c-pre2）
- SPA 圖片上傳到 CC（1.6c-pre3）
- 多 Host 管理（1.6c）
- Stream 逐字渲染（1.7）
