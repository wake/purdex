# JSONL 記錄格式 vs Stream-JSON 輸出格式研究

> 日期：2026-03-19
> 目的：比較 CC JSONL 檔案格式與 `-p stream-json` stdout 格式，找出 stream mode 實作缺口

---

## 1. JSONL 檔案格式

CC 自動寫入 `~/.claude/projects/{hash}/{session_id}.jsonl`，每行一個 JSON。

### 訊息類型分布（427c session，9.3MB）

| Type | 說明 | 數量 |
|------|------|------|
| `progress` | hook_progress / agent_progress | 2389 |
| `assistant` | LLM 完整回應 | 404 |
| `user` | 使用者訊息 + tool_result | 301 |
| `system` | api_error / stop_hook_summary / turn_duration | 63 |
| `file-history-snapshot` | 檔案狀態快照 | 53 |
| `queue-operation` | 背景任務佇列 | 2 |
| `pr-link` | PR 連結紀錄 | 2 |

### JSONL 獨有欄位（不在 stream-json 中）

```
parentUuid, isSidechain, parentToolUseID, toolUseID,
timestamp, uuid, userType, entrypoint, cwd, sessionId (camelCase),
version, gitBranch, isMeta, requestId
```

### JSONL 獨有 type（不在 stream-json 中）

- `progress` — CC 內部 hook/agent 進度
- `file-history-snapshot` — 檔案狀態快照
- `queue-operation` — 任務佇列操作（enqueue）
- `pr-link` — PR 連結紀錄

### JSONL 結構範例

```jsonc
// assistant
{
  "type": "assistant",
  "isSidechain": false,
  "parentToolUseID": null,        // camelCase
  "timestamp": "2026-03-17T09:39:43.967Z",
  "uuid": "a72cb606-...",
  "sessionId": "427c0115-...",    // camelCase
  "version": "2.1.77",
  "cwd": "/Users/wake/Workspace/wake/tmux-box",
  "message": {
    "id": "msg_01KLtWC8...",
    "type": "message",
    "role": "assistant",
    "model": "claude-opus-4-6",
    "stop_reason": null,
    "content": [{ "type": "thinking", "thinking": "..." }],
    "usage": { "input_tokens": 3, "output_tokens": 9, ... }
  }
}

// user (normal)
{
  "type": "user",
  "isMeta": null,
  "parentToolUseID": null,
  "message": { "role": "user", "content": "研究模式..." }
}

// user (isMeta — CC system bookkeeping)
{
  "type": "user",
  "isMeta": true,
  "message": { "role": "user", "content": [{ "type": "text", "text": "...skill content..." }] }
}

// user (tool_result)
{
  "type": "user",
  "isMeta": null,
  "parentToolUseID": null,
  "message": {
    "role": "user",
    "content": [{ "type": "tool_result", "tool_use_id": "toolu_01...", "content": "..." }]
  }
}

// progress
{
  "type": "progress",
  "data": {
    "type": "hook_progress",
    "hookEvent": "SessionStart",
    "hookName": "SessionStart:startup",
    "command": "/path/to/hook.sh"
  },
  "parentToolUseID": "ce542025-..."
}

// system (api_error)
{
  "type": "system",
  "subtype": "api_error",
  "error": { "status": 502, ... },
  "retryInMs": 540.08,
  "retryAttempt": 1,
  "maxRetries": 10
}

// system (turn_duration)
{
  "type": "system",
  "subtype": "turn_duration",
  "isMeta": false
}

// file-history-snapshot
{
  "type": "file-history-snapshot",
  "isSnapshotUpdate": ...,
  "messageId": "...",
  "snapshot": { ... }
}

// queue-operation
{
  "type": "queue-operation",
  "operation": "enqueue",
  "content": "<task-notification>...</task-notification>"
}

// pr-link
{
  "type": "pr-link",
  "prNumber": 12,
  "prUrl": "https://github.com/wake/tmux-box/pull/12",
  "prRepository": "wake/tmux-box"
}
```

---

## 2. Stream-JSON 格式（CC `-p --output-format stream-json` stdout）

### 訊息類型

| Type | 說明 | JSONL 有對應？ |
|------|------|---------------|
| `system` (init) | 初始化（model, tools, session_id） | 無 |
| `system` (status) | compacting 狀態 | 無 |
| `system` (compact_boundary) | compaction 邊界 | 無 |
| `system` (hook_*) | hook 進度 | JSONL 用 `progress` type |
| `system` (task_*) | 背景任務進度 | JSONL 用 `queue-operation` |
| `system` (files_persisted) | 檔案 checkpoint | JSONL 用 `file-history-snapshot` |
| `assistant` | LLM 回應 | 有（結構相同） |
| `user` | 使用者訊息回放 | 有（但 JSONL 含 isMeta） |
| `result` | 查詢完成 + 費用 | 無 |
| `stream_event` | Token 級串流 | 無 |
| `control_request` | 權限請求 | 無 |
| `tool_progress` | 工具執行心跳 | 無 |
| `tool_use_summary` | 工具完成摘要 | 無 |
| `rate_limit_event` | 速率限制 | 無 |
| `prompt_suggestion` | 後續提示建議 | 無 |
| `auth_status` | 認證狀態 | 無 |

### Stream-JSON 結構範例

```jsonc
// assistant（注意 snake_case）
{
  "type": "assistant",
  "parent_tool_use_id": null,     // snake_case
  "uuid": "msg-uuid-123",
  "session_id": "abc123",         // snake_case
  "message": {
    "id": "msg_01XYZ",
    "type": "message",
    "role": "assistant",
    "model": "claude-sonnet-4-6",
    "content": [{ "type": "text", "text": "回覆內容" }],
    "stop_reason": "end_turn",
    "usage": { "input_tokens": 100, "output_tokens": 50 }
  }
}

// system/init（Stream-JSON 獨有）
{
  "type": "system",
  "subtype": "init",
  "session_id": "abc123",
  "model": "claude-sonnet-4-6",
  "tools": ["Bash", "Read", "Write", ...],
  "permissionMode": "default"
}

// result（Stream-JSON 獨有）
{
  "type": "result",
  "subtype": "success",
  "total_cost_usd": 0.0234,
  "duration_ms": 5432,
  "num_turns": 3
}

// control_request（Stream-JSON 獨有）
{
  "type": "control_request",
  "request_id": "uuid-123",
  "request": {
    "subtype": "can_use_tool",
    "tool_name": "Bash",
    "input": { "command": "ls -la" },
    "tool_use_id": "tool-uuid"
  }
}

// stream_event（Stream-JSON 獨有，需 --verbose）
{
  "type": "stream_event",
  "event": {
    "type": "content_block_delta",
    "index": 0,
    "delta": { "type": "text_delta", "text": "Hello" }
  }
}
```

---

## 3. 關鍵差異

### 命名風格

| 欄位 | JSONL | Stream-JSON |
|------|-------|-------------|
| session ID | `sessionId` | `session_id` |
| parent tool use | `parentToolUseID` | `parent_tool_use_id` |

### user message content

| 方面 | JSONL | Stream-JSON |
|------|-------|-------------|
| content 型別 | `string` 或 `ContentBlock[]` | `string` 或 `ContentBlock[]` |
| 系統訊息 | `isMeta: true` 需過濾 | 不會出現 |
| CC 內部標記 | `<local-command-stdout>`, `<command-name>` 需過濾 | 不會出現 |
| tool_result | 出現在 user content 中 | 不直接輸出 |

### 互動功能（僅 Stream-JSON）

- `control_request` / `control_response` — 權限核准
- `stream_event` — 逐字串流
- `result` — 回合結束 + 費用
- `tool_progress` — 工具執行心跳

---

## 4. 現有 ParseJSONL 轉換邏輯（history.go）

```
JSONL → ParseJSONL → [{type, message}] → SPA ConversationView
```

過濾規則：
1. 只保留 `type == "user"` 或 `type == "assistant"`
2. 過濾 `isMeta == true`
3. 過濾 `model == "<synthetic>"`
4. 過濾 `<local-command-stdout>` / `<local-command-caveat>` 開頭的 user content
5. 提取 `<command-name>X</command-name>` → `X`
6. 正規化 string content → `[{type: "text", text: "..."}]`
7. 輸出只有 `{type, message}`

---

## 5. SPA 端目前處理的 Stream-JSON 類型

```typescript
// useRelayWsManager.ts
if (msg.type === 'assistant' || msg.type === 'user')  → addMessage
if (msg.type === 'result')                             → addCost + setStreaming(false)
if (msg.type === 'control_request')                    → addControlRequest
if (msg.type === 'system' && subtype === 'init')       → setSessionInfo
// 其餘全部忽略
```

---

## 6. Stream Mode 實作缺口

| 待處理 | 目前狀態 | 影響 | 優先級 |
|--------|---------|------|--------|
| `stream_event` | 忽略 | 無逐字串流（整段出現） | 高 |
| `tool_progress` | 忽略 | 長工具無進度顯示 | 中 |
| `tool_use_summary` | 忽略 | 工具結果摘要不顯示 | 中 |
| `system/status` (compacting) | 忽略 | 使用者不知道正在壓縮 | 中 |
| `system/task_*` | 忽略 | 背景任務進度不顯示 | 低 |
| `system/hook_*` | 忽略 | hook 進度不顯示 | 低 |
| `rate_limit_event` | 忽略 | 速率限制無提示 | 低 |
| `prompt_suggestion` | 忽略 | 後續建議不顯示 | 低 |
| 子代理訊息（`parent_tool_use_id` 非 null） | 混在一起 | 無法區分巢狀層級 | 中 |

---

## 7. CC TUI 任務清單（TaskCreate / TaskUpdate）

CC TUI 會顯示如下的任務追蹤 UI：

```
9 tasks (8 done, 1 open)
✔ Task 1: Executor 介面新增 SetWindowOption
✔ Task 2: restoreWindowSizing helper + server.go 修復
...
□ Task 9: 建立 PR
```

### 資料來源：散佈在 JSONL 的 assistant 訊息中

任務資料以 `tool_use` content block 形式存在於 `assistant` 訊息裡，**不是獨立的訊息類型**。

#### TaskCreate（建立任務）

出現在 `assistant` 訊息的 `message.content[]` 中：

```jsonc
{
  "type": "tool_use",
  "name": "TaskCreate",
  "id": "toolu_01USEzTU3NCY8mp3eZSmpsq1",
  "input": {
    "subject": "探索專案現況",
    "description": "檢查 relay.go 的 attach 機制、現有 resize 邏輯、相關測試"
  }
}
```

對應的 `tool_result`（在後續的 `user` 訊息中）：
```jsonc
{
  "type": "tool_result",
  "tool_use_id": "toolu_01USEzTU3NCY8mp3eZSmpsq1",
  "content": "Task #1 created successfully: 探索專案現況"
}
```

**注意：** TaskCreate 沒有回傳 ID，ID 是隱含遞增的（第 N 個 TaskCreate = task #N）。
但可以從 `tool_result` 的回應字串中解析出 `#1`。

#### TaskUpdate（更新狀態）

```jsonc
{
  "type": "tool_use",
  "name": "TaskUpdate",
  "id": "toolu_01UDCGB9pkUFMvxxHta5t4h8",
  "input": {
    "taskId": "1",
    "status": "in_progress"    // "in_progress" | "completed" | "open"
  }
}
```

對應的 `tool_result`：
```jsonc
{
  "type": "tool_result",
  "tool_use_id": "toolu_01UDCGB9pkUFMvxxHta5t4h8",
  "content": "Updated task #1 status"
}
```

### 實測資料（1a58cc3e session，完整任務生命週期）

```
TaskCreate #1:  "探索專案現況"
TaskCreate #2:  "釐清需求與限制"
TaskCreate #3:  "提出 2-3 個方案與取捨"
TaskCreate #4:  "分段呈現設計"
TaskCreate #5:  "撰寫設計文件並 commit"
TaskCreate #6:  "Spec review loop"
TaskCreate #7:  "使用者審閱 spec"
TaskCreate #8:  "轉入 writing-plans skill"
TaskUpdate #1:  in_progress → completed
TaskUpdate #2:  in_progress → completed
TaskUpdate #3:  in_progress → completed
TaskUpdate #4:  in_progress → completed  ← 此時 TUI 顯示 "8 tasks (4 done, 4 open)"
...
TaskCreate #9:  "Task 1: Executor 介面新增 SetWindowOption"  ← 第二批任務
TaskCreate #10: "Task 2: restoreWindowSizing helper..."
...
TaskCreate #17: "Task 9: 建立 PR"
TaskUpdate #9~#17: 依序 in_progress → completed
```

### 不在 JSONL / Stream-JSON 中的部分

以下是 **CC TUI 獨有的渲染**，不會出現在任何訊息格式中：

- 「9 tasks (8 done, 1 open)」摘要列
- ✔（completed）/ ■（in_progress）/ □（open）圖示
- 已完成任務的刪除線樣式
- 任務清單的即時彙總視圖

### SPA Stream Mode 實作方案

要在 SPA 顯示類似的任務追蹤 UI，需要：

1. **攔截 `assistant` 訊息中 `name == "TaskCreate"` 的 `tool_use` block**
   - 提取 `input.subject` 和 `input.description`
   - 從 `tool_result` 回應解析 task ID（`Task #N created...`）
   - 或直接用遞增計數器

2. **攔截 `name == "TaskUpdate"` 的 `tool_use` block**
   - 提取 `input.taskId` 和 `input.status`

3. **在 useStreamStore 維護 per-session task list 狀態**
   ```typescript
   interface Task {
     id: number
     subject: string
     description: string
     status: 'open' | 'in_progress' | 'completed'
   }
   ```

4. **渲染任務清單 UI**（可放在 ConversationView 側邊或內嵌）

5. **JSONL 歷史載入時**：ParseJSONL 目前會保留含 TaskCreate/TaskUpdate 的 assistant 訊息，SPA 可在 `loadHistory` 時重播這些 tool_use 來重建任務狀態
