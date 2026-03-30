# Agent Hook 狀態偵測增強

## 背景

現有 agent hook 狀態偵測（spec: `2026-03-29-agent-hook-status-design.md`）存在三個問題：

### 問題 1：SessionStart 錯誤推導為 running

`SessionStart` 的 `source` 有四種值，目前除 `compact` 外全部推導為 `running`，但 `startup`/`resume`/`clear` 實際上是 CC 等待使用者輸入（idle）。

| source | 含義 | 現行推導 | 正確推導 |
|--------|------|----------|----------|
| `startup` | 全新啟動 | ❌ `running` | `idle` |
| `resume` | 恢復舊 session | ❌ `running` | `idle` |
| `clear` | `/clear` 後重新開始 | ❌ `running` | `idle` |
| `compact` | 自動壓縮（工作中途） | ✅ `null` | `null` |

### 問題 2：StopFailure 缺少 error 狀態

`StopFailure` 目前推導為 `idle`，但實際上代表 API 錯誤（rate limit、認證失敗等），應顯示紅色錯誤燈號。

`StopFailure` 的 `error` 欄位可能值：
- `rate_limit` — 速率限制
- `authentication_failed` — 認證失敗
- `billing_error` — 帳單問題
- `server_error` — 伺服器錯誤
- `max_output_tokens` — 輸出上限
- `invalid_request` — 請求無效
- `unknown` — 未知錯誤

### 問題 3：Stale running 問題

已知 CC bug（[anthropics/claude-code#34713](https://github.com/anthropics/claude-code/issues/34713)）會導致 `UserPromptSubmit` hook 觸發後 CC 靜默丟棄 prompt，跳回輸入狀態。此時不會產生 `Stop`/`SessionEnd` 事件，DB 中 `UserPromptSubmit` 永遠不被覆蓋，snapshot 持續顯示 `running`。

事件流程斷裂：
```
UserPromptSubmit → (hook 觸發，POST 到 daemon) → CC 靜默丟棄 prompt → 跳回輸入
                                                    ↑
                                            沒有 Stop / SessionEnd
```

## CC Hooks 完整參考

以下是所有 CC hook 事件、tbox 目前使用狀態、及可用於狀態偵測的分析。

### 目前 tbox 註冊的 hooks（7 個）

定義在 `cmd/tbox/setup.go` 的 `hookEvents`：

| Hook | 推導狀態 | 用途 |
|------|----------|------|
| `SessionStart` | `running`（應改 `idle`） | CC 啟動/恢復 |
| `UserPromptSubmit` | `running` | 使用者送出 prompt |
| `Notification` | `waiting` 或 `idle` | 通知（權限/idle/auth） |
| `PermissionRequest` | `waiting` | 權限對話框 |
| `Stop` | `idle` | CC 正常結束回應 |
| `StopFailure` | `idle`（應改 `error`） | API 錯誤 |
| `SessionEnd` | `clear` | Session 結束 |

### 所有 CC hook 事件（完整清單）

| 事件 | 觸發時機 | Matcher 欄位 | 可 Block | stdin 關鍵欄位 |
|------|----------|-------------|----------|---------------|
| `SessionStart` | session 啟動/恢復 | `source`: startup, resume, clear, compact | 否 | `source`, `model`, `session_id` |
| `InstructionsLoaded` | CLAUDE.md 載入 | `load_reason` | 否 | `file_path`, `memory_type` |
| `UserPromptSubmit` | 使用者送出 prompt | 無 | 是（exit 2） | `prompt`, `permission_mode` |
| `PreToolUse` | 工具執行前 | `tool_name` (regex) | 是（exit 2） | `tool_name`, `tool_input`, `tool_use_id` |
| `PermissionRequest` | 權限對話框出現 | `tool_name` (regex) | 是 | `tool_name`, `tool_input` |
| `PostToolUse` | 工具成功執行後 | `tool_name` (regex) | 否 | `tool_name`, `tool_input`, `tool_response` |
| `PostToolUseFailure` | 工具失敗後 | `tool_name` (regex) | 否 | `tool_name`, `tool_input`, `error` |
| `Notification` | 通知發送 | `notification_type` | 否 | `message`, `notification_type` |
| `SubagentStart` | subagent 啟動 | `agent_type` | 否 | `agent_id`, `agent_type` |
| `SubagentStop` | subagent 結束 | `agent_type` | 是（exit 2） | `agent_id`, `agent_type`, `last_assistant_message`, `agent_transcript_path` |
| `TaskCreated` | Task 建立 | 無 | 是（exit 2） | `task_id`, `task_subject` |
| `TaskCompleted` | Task 完成 | 無 | 是（exit 2） | `task_id`, `task_subject` |
| `Stop` | CC 正常結束回應 | 無 | 是（exit 2） | `last_assistant_message`, `stop_hook_active` |
| `StopFailure` | API 錯誤 | `error` type | 否 | `error`, `error_details`, `last_assistant_message` |
| `TeammateIdle` | Teammate 閒置 | 無 | 是 | `teammate_name`, `team_name` |
| `ConfigChange` | 設定檔變更 | `source` | 是（exit 2） | `source`, `file_path` |
| `CwdChanged` | 工作目錄改變 | 無 | 否 | `cwd` |
| `FileChanged` | 監控檔案改變 | filename basename | 否 | `file_path` |
| `PreCompact` | context 壓縮前 | `compaction_type` | 否 | `compaction_type`: manual, auto |
| `PostCompact` | context 壓縮後 | `compaction_type` | 否 | `compaction_type`: manual, auto |
| `Elicitation` | MCP server 要求輸入 | MCP server name | 是 | `mcp_server`, `form_schema` |
| `ElicitationResult` | 使用者回應 MCP 輸入 | MCP server name | 是 | `mcp_server`, `user_response` |
| `WorktreeCreate` | Worktree 建立 | 無 | 是 | — |
| `WorktreeRemove` | Worktree 移除 | 無 | 否 | — |
| `SessionEnd` | session 結束 | `source`: clear, resume, logout, prompt_input_exit, bypass_permissions_disabled, other | 否 | `source` |

### SubagentStart / SubagentStop 配對

兩個事件都帶 `agent_id` 欄位，可做精確配對：

```json
// SubagentStart stdin
{
  "hook_event_name": "SubagentStart",
  "session_id": "abc123",
  "agent_id": "agent-abc123",        // ← 配對 key
  "agent_type": "Explore"            // Explore | Bash | Plan | 自定義
}

// SubagentStop stdin
{
  "hook_event_name": "SubagentStop",
  "session_id": "abc123",
  "agent_id": "agent-abc123",        // ← 同一個 ID
  "agent_type": "Explore",
  "agent_transcript_path": "...jsonl",
  "last_assistant_message": "...",
  "stop_hook_active": false
}
```

**已知限制**（[anthropics/claude-code#14859](https://github.com/anthropics/claude-code/issues/14859)）：
- 所有 hook 事件共用同一個 `session_id`，無法區分是主 agent 還是 subagent 產生的 PreToolUse
- 沒有 `parent_agent_id` 欄位，無法建立 agent 樹狀結構
- 只能做「這個 session 有 N 個 subagent 在跑」的粗略計數

## 修改方案

### 一、修改 AgentStatus 型別

```typescript
// spa/src/stores/useAgentStore.ts
export type AgentStatus = 'running' | 'waiting' | 'idle' | 'error'
```

### 二、修改 deriveStatus

`SubagentStart`/`SubagentStop` **不經過 deriveStatus**，在 `handleHookEvent` 中 early return，只更新 `activeSubagents` 追蹤，不影響主 agent 狀態。原因：subagent 是獨立的工作單元，主 agent 的 idle/running/error 應由自身事件決定。

```typescript
function deriveStatus(eventName: string, rawEvent?: Record<string, unknown>): AgentStatus | 'clear' | null {
  switch (eventName) {
    case 'SessionStart':
      if (rawEvent?.source === 'compact') return null
      return 'idle'                    // ← 修正：啟動 = 等待輸入
    case 'UserPromptSubmit':
      return 'running'
    case 'Notification': {
      const nt = rawEvent?.notification_type
      if (nt === 'permission_prompt' || nt === 'elicitation_dialog') return 'waiting'
      if (nt === 'idle_prompt' || nt === 'auth_success') return 'idle'
      if (nt !== undefined) console.warn('[deriveStatus] unknown notification_type:', nt)
      return null
    }
    case 'PermissionRequest':
      return 'waiting'
    case 'Stop':
      return 'idle'
    case 'StopFailure':
      return 'error'                   // ← 修正：錯誤狀態
    case 'SessionEnd':
      return 'clear'
    default:
      return null
  }
}
```

### 三、新增 hook 註冊

在 `cmd/tbox/setup.go` 的 `hookEvents` 新增：

> **注意**：`PreToolUse` 延後實作（目前無 stale running 問題），待需要時再加。

```go
var hookEvents = []string{
    "SessionStart",
    "UserPromptSubmit",
    "SubagentStart",        // ← 新增（ephemeral，不存 DB）
    "SubagentStop",         // ← 新增（ephemeral，不存 DB）
    "Stop",
    "StopFailure",
    "Notification",
    "PermissionRequest",
    "SessionEnd",
}
```

### 四、PreToolUse 效能考量

`PreToolUse` 觸發頻率極高（每個工具呼叫都觸發），需要在 daemon 端做優化：

**方案 A — 只更新 timestamp，不存完整事件**
- 收到 `PreToolUse` 時只更新 `agent_events` 表的 `updated_at` 欄位
- 不廣播完整 hook event 到 WS
- 前端用 `updated_at` 差異偵測 stale

**方案 B — 節流廣播**
- daemon 收到 `PreToolUse` 後標記 session 為 active
- 每 N 秒檢查一次，如果 session active 且當前狀態非 running，才廣播 status change
- 減少 WS 訊息量

### 五、error 狀態 UI

| 狀態 | 顏色 | 動畫 |
|------|------|------|
| running | `#4ade80` 綠 | 呼吸燈 |
| waiting | `#facc15` 黃 | 無 |
| idle | `#6b7280` 灰 | 無 |
| **error** | **`#ef4444` 紅** | **無** |

### 六、Stale running 偵測（前端輔助）

即使新增 `PreToolUse`，仍需前端 stale timeout 作為最後防線：

- `UserPromptSubmit` 後 N 秒內無後續事件 → 降級為 `idle`
- N 的建議值：60 秒（CC 長思考 + API 延遲的合理上限）
- 實作位置：`useAgentStore` 的 `handleHookEvent` 中啟動 timer

## 涉及檔案

| 檔案 | 修改內容 |
|------|----------|
| `cmd/tbox/setup.go` | `hookEvents` 新增 3 個事件 |
| `cmd/tbox/hook.go` | 無需修改（通用處理） |
| `internal/module/agent/handler.go` | PreToolUse 效能優化邏輯 |
| `internal/store/agent_event.go` | 可能新增 `updated_at` 欄位 |
| `spa/src/stores/useAgentStore.ts` | `AgentStatus` 加 `error`、`deriveStatus` 修正 |
| `spa/src/components/TabBar/TabButton.tsx` | error 燈號顏色 |
| `spa/src/components/SessionPanel/` | error 燈號顏色 |

## 參考資料

- [CC Hooks 官方文件](https://code.claude.com/docs/en/hooks)
- [#34713 — False "Hook Error" labels](https://github.com/anthropics/claude-code/issues/34713)
- [#13912 — UserPromptSubmit stdout error](https://github.com/anthropics/claude-code/issues/13912)
- [#14859 — Agent Hierarchy in Hook Events](https://github.com/anthropics/claude-code/issues/14859)
