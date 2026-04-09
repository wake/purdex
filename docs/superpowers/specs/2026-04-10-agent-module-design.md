# Agent Module 設計規格

> 日期：2026-04-10
> 狀態：Draft

## 概述

將 agent 概念從目前散落在 `cc`、`agent`、`detect` 等模組的狀態，重構為獨立的 **AgentProvider 體系**。Mode（terminal/stream）和 Agent（cc/codex/none）是兩個正交軸——Terminal 是 dumb renderer 不知道裡面跑什麼，Agent 是行為提供者獨立於渲染模式。

### 目標

1. 後端 AgentProvider interface + Registry，CC 和 Codex 各自實作
2. 後端負責 deriveStatus，廣播正規化狀態，前端 zero per-agent code
3. Hook 安裝自動化支援 CC 和 Codex（`tbox setup --agent <type>`）

### 非目標

- Stream mode 的 agent 抽象化（StreamCapable interface 預留但不實作）
- Codex HistoryProvider（格式待研究）
- 向下相容舊版 hook command 格式

## 架構模型

### 正交軸

```
Mode (怎麼渲染)     Agent (誰在跑)
─────────────       ──────────────
terminal            none (純 shell)
stream              cc
                    codex
```

Session = `{ mode, activeAgent? }`

- mode 決定渲染：`terminal` → TerminalView，`stream` → ConversationView
- agent 決定：tab icon + 燈號、status 推導、可用操作、stream handoff 行為

### 目錄結構

```
internal/
├── agent/                    # agent 定義層（interface + registry）
│   ├── provider.go           # AgentProvider + capability interfaces
│   ├── registry.go           # AgentRegistry
│   └── status.go             # AgentStatus enum + NormalizedEvent
├── agent/cc/                 # CC provider 實作
│   ├── provider.go           # 組裝，實作 AgentProvider
│   ├── detector.go           # 現有 detect 邏輯搬入
│   ├── operator.go           # 現有 operator 搬入
│   ├── history.go            # 現有 history 搬入，實作 HistoryProvider
│   ├── hooks.go              # CC hook 安裝，實作 HookInstaller
│   └── status.go             # CC-specific deriveStatus
├── agent/codex/              # Codex provider 實作
│   ├── provider.go           # 組裝，實作 AgentProvider
│   ├── detector.go           # process detection
│   ├── hooks.go              # Codex hook 安裝，實作 HookInstaller
│   └── status.go             # Codex-specific deriveStatus
└── module/
    └── agent/                # agent module（hook routing + registry 管理）
        ├── module.go         # 擴充 registry 初始化
        ├── handler.go        # 改為經 registry 分派 + 廣播 NormalizedEvent
        ├── upload.go         # 不動
        └── upload_mgmt.go    # 不動
```

### 舊模組處置

| 現有模組 | 處置 |
|---------|------|
| `internal/detect/` | 搬入 `internal/agent/cc/detector.go` |
| `internal/module/cc/` | 整併進 `internal/agent/cc/`，模組刪除 |
| `internal/module/agent/cc_hooks.go` | 搬入 `internal/agent/cc/hooks.go` |
| `internal/module/agent/` 其餘 | 保留，改造 |
| `cmd/tbox/main.go` 的 `cc.New()` | 刪除，CC 改由 agent module 初始化 provider |

### Stream Module 整合（重要）

Stream module（`internal/module/stream/`）的 handoff orchestrator 直接使用 `CCDetector` 和 `CCOperator`。本次不抽象化 stream handoff（StreamCapable 預留），但必須處理 import path 變更：

1. CC provider 初始化時，將 detector 和 operator 註冊到 `core.Registry`（key 不變，只是由 agent module 代替舊 cc module 註冊）
2. Stream module 的 `Init()` 從 registry 取得時，型別改為從 `internal/agent/cc` 引入
3. `detect.Status*` 常數跟著搬到 `internal/agent/cc`，stream module 的 import path 更新

效果：stream module 的邏輯完全不動，只改 import path。未來做 StreamCapable 抽象時再改 stream module 的內部實作。

## 後端設計

### AgentProvider Interface（`internal/agent/provider.go`）

Capability-based composition——必備 interface 小而精，選配 capability 各自獨立。

```go
package agent

import "encoding/json"

// --- 必備：所有 provider 都要實作 ---

type AgentProvider interface {
    Type() string                  // "cc", "codex"
    DisplayName() string           // "Claude Code", "Codex"
    IconHint() string              // 前端 icon map 的 key
    Claim(ctx ClaimContext) bool    // 這個 session 是我的嗎？
    DeriveStatus(eventName string, rawEvent json.RawMessage) (Status, bool)
    IsAlive(tmuxTarget string) bool
}

type ClaimContext struct {
    HookEvent   *HookEvent
    ProcessName string
}

type HookEvent struct {
    TmuxSession string
    EventName   string
    RawEvent    json.RawMessage
    AgentType   string
}

// --- 選配 capability ---

type HookInstaller interface {
    InstallHooks(tboxPath string) error
    RemoveHooks(tboxPath string) error
    CheckHooks() (HookStatus, error)
}

type HookStatus struct {
    Installed bool                      `json:"installed"`
    Events    map[string]HookEventInfo  `json:"events"`
    Issues    []string                  `json:"issues"`
}

type HookEventInfo struct {
    Installed bool   `json:"installed"`
    Command   string `json:"command"`
}

type HistoryProvider interface {
    GetHistory(cwd, sessionID string) ([]map[string]any, error)
}

// 預留，本次不實作
type StreamCapable interface {
    ExtractState(tmuxTarget string) (SessionState, error)
    ExitInteractive(tmuxTarget string) error
    RelayArgs(state SessionState) []string
    ResumeCommand(state SessionState) string
}

type SessionState struct {
    SessionID string
    Cwd       string
}
```

### Status 定義（`internal/agent/status.go`）

```go
package agent

type Status string

const (
    StatusRunning Status = "running"
    StatusWaiting Status = "waiting"
    StatusIdle    Status = "idle"
    StatusError   Status = "error"
    StatusClear   Status = "clear"   // session 結束，清除所有狀態
)

type NormalizedEvent struct {
    AgentType        string   `json:"agent_type"`
    Status           string   `json:"status"`
    Model            string   `json:"model,omitempty"`
    Subagents        []string `json:"subagents,omitempty"`
    RawEventName     string   `json:"raw_event_name"`
    NotificationType string   `json:"notification_type,omitempty"` // 給前端通知系統用
}
```

### Registry（`internal/agent/registry.go`）

```go
type Registry struct {
    providers []AgentProvider  // 註冊順序 = 優先序
}

func NewRegistry() *Registry
func (r *Registry) Register(p AgentProvider)
func (r *Registry) Get(agentType string) (AgentProvider, bool)   // 依 type 找
func (r *Registry) Claim(ctx ClaimContext) (AgentProvider, bool)  // 遍歷，第一個 claim 的贏
func (r *Registry) All() []AgentProvider
```

**路由語意：**
- `Get(agentType)`：hook event 進來時使用（agent_type 已知）
- `Claim(ctx)`：只用在 process detection path（無 hook、需辨識 session 裡跑什麼）

### CC Provider

**DeriveStatus：**

| Event | Status |
|-------|--------|
| SessionStart（非 compact） | idle |
| UserPromptSubmit | running |
| Notification（permission_prompt / elicitation_dialog） | waiting |
| Notification（idle_prompt / auth_success） | idle |
| PermissionRequest | waiting |
| Stop | idle |
| StopFailure | error |
| SessionEnd | clear |
| SubagentStart / SubagentStop | 不影響主 status，更新 subagent 追蹤 |

**Claim：** hook event 的 `agent_type == "cc"` 或 process detection（現有 detect 邏輯）。

**IsAlive：** 使用現有 detector 檢查 CC 狀態（StatusCCIdle / StatusCCRunning / StatusCCWaiting 視為 alive）。

**HookInstaller：** 改寫 `~/.claude/settings.json`，現有 `tbox setup` 邏輯。

**HistoryProvider：** 讀 `~/.claude/projects/{hash}/{sessionId}.jsonl`，現有邏輯。

### Codex Provider

**DeriveStatus：**

| Event | Status |
|-------|--------|
| SessionStart | idle |
| UserPromptSubmit | running |
| Stop | idle |

狀態粒度比 CC 粗——沒有 waiting / error / clear。

**Claim：** hook event 的 `agent_type == "codex"` 或 `pane_current_command == "codex"`。

**IsAlive：** 檢查 `pane_current_command` 是否為 `codex`。準確度有限，為最佳可用方案。

**HookInstaller：** 改寫 `~/.codex/hooks.json`。格式範例：

```json
{
  "hooks": {
    "SessionStart": [
      {
        "type": "command",
        "command": "/path/to/tbox hook --agent codex SessionStart",
        "timeout": 5
      }
    ],
    "UserPromptSubmit": [
      {
        "type": "command",
        "command": "/path/to/tbox hook --agent codex UserPromptSubmit",
        "timeout": 5
      }
    ],
    "Stop": [
      {
        "type": "command",
        "command": "/path/to/tbox hook --agent codex Stop",
        "timeout": 5
      }
    ]
  }
}
```

**HistoryProvider：** 本次不實作。

### Agent Module 改造

**handler.go** 事件分派流程：

```
POST /api/agent/event { agent_type, event_name, raw_event, tmux_session }
  → 存 raw event 到 DB（不變，保留原始資料）
  → registry.Get(agent_type) 找 provider
  → provider.DeriveStatus(event_name, raw_event) 推導狀態
  → 組裝 NormalizedEvent → 廣播 WS
```

**Subagent 追蹤** 從前端搬到後端 module 層：

- module 內部維護 `map[string][]string`（tmuxSession → activeSubagentIDs）
- SubagentStart / SubagentStop 不存 DB（維持現有行為），更新 in-memory map
- 廣播 NormalizedEvent 時帶上 `subagents` 欄位（subagent ID 為 CC 的 `agent_id` UUID）
- StatusClear 時同步清除該 session 的 subagent map entry

**Error guard** 從前端搬到後端：

- module 維護 per-session 的 current status（in-memory）
- 在 error 狀態下，只有 UserPromptSubmit / SessionStart / Stop 能清除
- **Daemon restart 恢復：** `Start()` 時從 DB replay 所有 session 的最後一筆 raw event，經 provider.DeriveStatus 重建 in-memory status map（包含 error guard 狀態）

**Route 變化：**

```
# Hook 管理（per-agent）
GET  /api/hooks/{agent}/status
POST /api/hooks/{agent}/setup

# History（per-agent，經 session 的 agent_type 查 HistoryProvider）
GET  /api/sessions/{code}/history
```

**isAlive 觸發時機（不做 polling）：**

1. SPA reconnect（sendSnapshot 時）：遍歷所有 session，呼叫 provider.IsAlive()，已死的廣播 StatusClear。**非同步執行**，不阻塞 WS handshake，設 5 秒 timeout budget。
2. 前端主動請求：`POST /api/agent/check-alive/{session}`

## 前端設計

### useAgentStore 簡化

**刪除：**
- `deriveStatus()` 函數
- error guard 邏輯
- subagent tracking 邏輯
- model extraction
- `events` map（不再存 raw event）

**保留（純 UI 邏輯）：**
- unread tracking
- tabIndicatorStyle

```typescript
interface AgentState {
  // 後端推導好的狀態
  statuses: Record<string, AgentStatus>
  agentTypes: Record<string, string>
  models: Record<string, string>
  subagents: Record<string, string[]>

  // 純 UI 狀態
  unread: Record<string, boolean>
  tabIndicatorStyle: TabIndicatorStyle

  // Actions
  handleNormalizedEvent: (hostId: string, sessionCode: string, event: NormalizedEvent) => void
  markRead: (hostId: string, sessionCode: string) => void
  removeHost: (hostId: string) => void
  setTabIndicatorStyle: (style: TabIndicatorStyle) => void
}
```

**handleNormalizedEvent** 只做：存狀態 + unread 判斷。

Unread 判斷邏輯（agent-agnostic）：
- `waiting` / `error` → 一律 actionable
- `idle` 且 `raw_event_name !== 'Notification'` → actionable
- 非當前 active session → 標 unread

### Notification Dispatcher 調整

`useNotificationDispatcher.ts` 和 `notification-content.ts` 現在直接讀 `raw_event.notification_type`。改為從 NormalizedEvent 的 `notification_type` 欄位讀取，不再依賴 raw event。

### Agent Icon Map

```typescript
// spa/src/lib/agent-icons.ts
export const AGENT_ICONS: Record<string, IconComponent> = {
  cc: Lightning,
  codex: Code,
}

export const AGENT_NAMES: Record<string, string> = {
  cc: 'Claude Code',
  codex: 'Codex',
}
```

新增 agent 只需加一行。

### Tab 燈號

```
無 agent  → Terminal icon（灰）
有 agent  → AGENT_ICONS[agentType] + STATUS_COLORS[status]
```

Status 顏色：running=blue, waiting=yellow, idle=gray, error=red。

### Settings Hook 管理 UI

Per-agent 獨立開關，各自呼叫 `/api/hooks/{agent}/status` 和 `/api/hooks/{agent}/setup`。

## CLI 改造

### `tbox hook`

```
現在：tbox hook <event_name>
改後：tbox hook --agent <type> <event_name>
```

`--agent` 必填，不提供時報錯。

### `tbox setup`

```
現在：tbox setup [--remove]
改後：tbox setup --agent <type> [--remove]
```

- `--agent cc`：改寫 `~/.claude/settings.json`
- `--agent codex`：改寫 `~/.codex/hooks.json`

`--agent` 必填，不提供時報錯。

### 相容性

不做向下相容。Alpha 階段，`tbox setup --agent cc` 重新安裝即可。Settings UI 顯示 hooks 狀態異常時引導使用者重裝。

## Codex Hook Event 對照

| Event | CC | Codex | 備註 |
|-------|:--:|:-----:|------|
| SessionStart | ✓ | ✓ | |
| UserPromptSubmit | ✓ | ✓ | |
| Stop | ✓ | ✓ | |
| SessionEnd | ✓ | ✗ | Codex 靠 isAlive 補償 |
| Notification | ✓ | ✗ | Codex 無法偵測 waiting |
| PermissionRequest | ✓ | ✗ | Codex 無法偵測 waiting |
| StopFailure | ✓ | ✗ | Codex 無法偵測 error |
| SubagentStart/Stop | ✓ | ✗ | |
| PreToolUse/PostToolUse | ✗ | ✓ | 本次不處理 |
