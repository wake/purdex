# Claude Code `stream-json` 協定完整規格

> 來源：官方 Agent SDK TypeScript 文件、Claude Code CLI 原始碼、The Vibe Company 逆向工程文件
> 最後更新：2026-03-17

---

## 概述

Claude Code CLI 支援以 NDJSON（每行一個 JSON 物件）格式進行雙向通訊。

**啟動方式：**

```bash
claude \
  --input-format stream-json \
  --output-format stream-json \
  --print \
  -p "placeholder"
```

也可搭配 WebSocket SDK 模式：

```bash
claude --sdk-url ws://localhost:8765 \
       --input-format stream-json \
       --output-format stream-json \
       --print --verbose \
       -p "placeholder"
```

**傳輸格式：** 每則訊息為一個 JSON 物件 + 換行符 (`\n`)。多條訊息可串接發送。

---

## 目錄

1. [OUTPUT — CLI 發出的訊息類型](#1-output--cli-發出的所有訊息類型)
2. [INPUT — CLI 接受的訊息類型](#2-input--cli-接受的訊息類型)
3. [CONTROL 協定 — 權限與工具核准](#3-control-協定--權限與工具核准)
4. [Content Block 類型](#4-content-block-類型)
5. [Stream Events（--include-partial-messages）](#5-stream-events)
6. [所有內建工具的 Input/Output Schema](#6-工具-inputoutput-schema)
7. [Session 管理](#7-session-管理)
8. [傳輸層細節](#8-傳輸層細節)

---

## 1. OUTPUT — CLI 發出的所有訊息類型

### 完整型別聯集（TypeScript `SDKMessage`）

```typescript
type SDKMessage =
  | SDKAssistantMessage
  | SDKUserMessage
  | SDKUserMessageReplay
  | SDKResultMessage
  | SDKSystemMessage            // subtype: "init"
  | SDKPartialAssistantMessage  // type: "stream_event"
  | SDKCompactBoundaryMessage   // subtype: "compact_boundary"
  | SDKStatusMessage            // subtype: "status"
  | SDKHookStartedMessage       // subtype: "hook_started"
  | SDKHookProgressMessage      // subtype: "hook_progress"
  | SDKHookResponseMessage      // subtype: "hook_response"
  | SDKToolProgressMessage      // type: "tool_progress"
  | SDKAuthStatusMessage        // type: "auth_status"
  | SDKTaskNotificationMessage  // subtype: "task_notification"
  | SDKTaskStartedMessage       // subtype: "task_started"
  | SDKTaskProgressMessage      // subtype: "task_progress"
  | SDKFilesPersistedEvent      // subtype: "files_persisted"
  | SDKToolUseSummaryMessage    // type: "tool_use_summary"
  | SDKRateLimitEvent           // type: "rate_limit_event"
  | SDKPromptSuggestionMessage; // type: "prompt_suggestion"
```

---

### 1.1 `system` — 系統訊息（6 個 subtype）

#### 1.1.1 `system/init` — 初始化

**何時發出：** CLI 啟動後、WebSocket 連線建立後的第一則訊息。

```typescript
type SDKSystemMessage = {
  type: "system";
  subtype: "init";
  uuid: string;          // UUID
  session_id: string;
  agents?: string[];
  apiKeySource: "user" | "project" | "org" | "temporary" | "oauth";
  betas?: string[];
  claude_code_version: string;
  cwd: string;
  tools: string[];       // ["Bash", "Read", "Write", "Edit", "Glob", "Grep", ...]
  mcp_servers: { name: string; status: string }[];
  model: string;         // e.g. "claude-sonnet-4-6"
  permissionMode: "default" | "acceptEdits" | "bypassPermissions" | "plan" | "dontAsk";
  slash_commands: string[];
  output_style: string;
  skills: string[];
  plugins: { name: string; path: string }[];
};
```

**範例：**
```json
{
  "type": "system",
  "subtype": "init",
  "uuid": "550e8400-e29b-41d4-a716-446655440000",
  "session_id": "abc123",
  "claude_code_version": "1.0.38",
  "cwd": "/Users/user/project",
  "tools": ["Bash", "Read", "Write", "Edit", "Glob", "Grep", "WebFetch", "WebSearch", "Agent", "AskUserQuestion"],
  "mcp_servers": [{"name": "playwright", "status": "connected"}],
  "model": "claude-sonnet-4-6",
  "permissionMode": "default",
  "apiKeySource": "user",
  "slash_commands": ["/help", "/clear", "/compact"],
  "output_style": "normal",
  "skills": [],
  "plugins": []
}
```

#### 1.1.2 `system/status` — 狀態變更

**何時發出：** 開始/結束 context compaction 時。

```typescript
type SDKStatusMessage = {
  type: "system";
  subtype: "status";
  status: "compacting" | null;  // null = compacting 結束
  permissionMode?: PermissionMode;
  uuid: string;
  session_id: string;
};
```

#### 1.1.3 `system/compact_boundary` — Compaction 邊界

**何時發出：** Context compaction 完成後。

```typescript
type SDKCompactBoundaryMessage = {
  type: "system";
  subtype: "compact_boundary";
  uuid: string;
  session_id: string;
  compact_metadata: {
    trigger: "manual" | "auto";
    pre_tokens: number;
  };
};
```

#### 1.1.4 `system/hook_started` — Hook 開始執行

```typescript
type SDKHookStartedMessage = {
  type: "system";
  subtype: "hook_started";
  hook_id: string;
  hook_name: string;
  hook_event: string;   // "PreToolUse", "PostToolUse", etc.
  uuid: string;
  session_id: string;
};
```

#### 1.1.5 `system/hook_progress` — Hook 中間輸出

```typescript
type SDKHookProgressMessage = {
  type: "system";
  subtype: "hook_progress";
  hook_id: string;
  hook_name: string;
  hook_event: string;
  stdout: string;
  stderr: string;
  output: string;
  uuid: string;
  session_id: string;
};
```

#### 1.1.6 `system/hook_response` — Hook 執行完畢

```typescript
type SDKHookResponseMessage = {
  type: "system";
  subtype: "hook_response";
  hook_id: string;
  hook_name: string;
  hook_event: string;
  output: string;
  stdout: string;
  stderr: string;
  exit_code?: number;
  outcome: "success" | "error" | "cancelled";
  uuid: string;
  session_id: string;
};
```

#### 1.1.7 `system/task_notification` — 背景工作完成

```typescript
type SDKTaskNotificationMessage = {
  type: "system";
  subtype: "task_notification";
  task_id: string;
  tool_use_id?: string;
  status: "completed" | "failed" | "stopped";
  output_file: string;
  summary: string;
  usage?: {
    total_tokens: number;
    tool_uses: number;
    duration_ms: number;
  };
  uuid: string;
  session_id: string;
};
```

#### 1.1.8 `system/task_started` — 背景工作開始

```typescript
type SDKTaskStartedMessage = {
  type: "system";
  subtype: "task_started";
  task_id: string;
  tool_use_id?: string;
  description: string;
  task_type?: string;
  uuid: string;
  session_id: string;
};
```

#### 1.1.9 `system/task_progress` — 背景工作進度

```typescript
type SDKTaskProgressMessage = {
  type: "system";
  subtype: "task_progress";
  task_id: string;
  tool_use_id?: string;
  description: string;
  usage: {
    total_tokens: number;
    tool_uses: number;
    duration_ms: number;
  };
  last_tool_name?: string;
  uuid: string;
  session_id: string;
};
```

#### 1.1.10 `system/files_persisted` — 檔案 Checkpoint 寫入

```typescript
type SDKFilesPersistedEvent = {
  type: "system";
  subtype: "files_persisted";
  files: { filename: string; file_id: string }[];
  failed: { filename: string; error: string }[];
  processed_at: string;
  uuid: string;
  session_id: string;
};
```

---

### 1.2 `assistant` — 助理回應

**何時發出：** LLM 完成一次完整回應後。

```typescript
type SDKAssistantMessage = {
  type: "assistant";
  uuid: string;
  session_id: string;
  message: {
    id: string;           // "msg_01..."
    type: "message";
    role: "assistant";
    model: string;
    content: ContentBlock[];  // 見 §4 Content Block 類型
    stop_reason: "end_turn" | "tool_use" | "max_tokens" | null;
    usage: {
      input_tokens: number;
      output_tokens: number;
      cache_creation_input_tokens?: number | null;
      cache_read_input_tokens?: number | null;
      server_tool_use?: { web_search_requests: number; web_fetch_requests: number } | null;
      service_tier?: "standard" | "priority" | "batch" | null;
    };
  };
  parent_tool_use_id: string | null;  // 非 null 表示在子代理中
  error?: "authentication_failed" | "billing_error" | "rate_limit"
        | "invalid_request" | "server_error" | "unknown";
};
```

**範例：**
```json
{
  "type": "assistant",
  "uuid": "msg-uuid-123",
  "session_id": "abc123",
  "message": {
    "id": "msg_01XYZ",
    "type": "message",
    "role": "assistant",
    "model": "claude-sonnet-4-6",
    "content": [
      { "type": "text", "text": "這是我的回覆。" }
    ],
    "stop_reason": "end_turn",
    "usage": { "input_tokens": 100, "output_tokens": 50 }
  },
  "parent_tool_use_id": null
}
```

---

### 1.3 `user` — 使用者訊息回放

**何時發出：** 使用 `--replay-user-messages` 時回放歷史使用者訊息。

```typescript
type SDKUserMessageReplay = {
  type: "user";
  uuid: string;          // 必填
  session_id: string;
  message: {
    role: "user";
    content: string | ContentBlock[];
  };
  parent_tool_use_id: string | null;
  isSynthetic?: boolean;
  tool_use_result?: unknown;
  isReplay: true;        // 標記為回放
};
```

---

### 1.4 `result` — 查詢結果

**何時發出：** 一次 query 完成時（成功或失敗）。

```typescript
// 成功
type SDKResultSuccess = {
  type: "result";
  subtype: "success";
  uuid: string;
  session_id: string;
  duration_ms: number;
  duration_api_ms: number;
  is_error: boolean;
  num_turns: number;
  result: string;              // 最終文字輸出
  stop_reason: string | null;
  total_cost_usd: number;
  usage: NonNullableUsage;
  modelUsage: { [modelName: string]: ModelUsage };
  permission_denials: SDKPermissionDenial[];
  structured_output?: unknown; // 有定義 outputFormat 時才有
};

// 錯誤
type SDKResultError = {
  type: "result";
  subtype: "error_max_turns"
         | "error_during_execution"
         | "error_max_budget_usd"
         | "error_max_structured_output_retries";
  uuid: string;
  session_id: string;
  duration_ms: number;
  duration_api_ms: number;
  is_error: boolean;
  num_turns: number;
  stop_reason: string | null;
  total_cost_usd: number;
  usage: NonNullableUsage;
  modelUsage: { [modelName: string]: ModelUsage };
  permission_denials: SDKPermissionDenial[];
  errors: string[];
};
```

**`SDKPermissionDenial`：**
```typescript
type SDKPermissionDenial = {
  tool_name: string;
  tool_use_id: string;
  tool_input: Record<string, unknown>;
};
```

**`ModelUsage`：**
```typescript
type ModelUsage = {
  inputTokens: number;
  outputTokens: number;
  cacheReadInputTokens: number;
  cacheCreationInputTokens: number;
  webSearchRequests: number;
  costUSD: number;
  contextWindow: number;
  maxOutputTokens: number;
};
```

**範例（成功）：**
```json
{
  "type": "result",
  "subtype": "success",
  "uuid": "result-uuid",
  "session_id": "abc123",
  "duration_ms": 5432,
  "duration_api_ms": 4200,
  "is_error": false,
  "num_turns": 3,
  "result": "已完成所有修改。",
  "stop_reason": "end_turn",
  "total_cost_usd": 0.0234,
  "usage": { "input_tokens": 5000, "output_tokens": 800, "cache_creation_input_tokens": 0, "cache_read_input_tokens": 3000 },
  "modelUsage": {},
  "permission_denials": []
}
```

---

### 1.5 `stream_event` — Token 串流事件

**何時發出：** 啟用 `includePartialMessages: true`（SDK）或 `--verbose`（CLI）時。

```typescript
type SDKPartialAssistantMessage = {
  type: "stream_event";
  event: BetaRawMessageStreamEvent;  // Anthropic SDK 串流事件
  parent_tool_use_id: string | null;
  uuid: string;
  session_id: string;
};
```

`BetaRawMessageStreamEvent` 為 Anthropic Messages API 的原生串流事件，包含以下事件類型：

| 事件類型 | 說明 |
|---------|------|
| `message_start` | 訊息開始，包含 message 物件 |
| `content_block_start` | 新 content block 開始 |
| `content_block_delta` | 增量文字或工具輸入 delta |
| `content_block_stop` | content block 結束 |
| `message_delta` | 訊息層級更新（stop_reason, usage） |
| `message_stop` | 訊息結束 |

**`content_block_delta` 內的 delta 類型：**

```json
// 文字 delta
{ "type": "text_delta", "text": "部分文字" }

// 工具輸入 delta（JSON 增量字串）
{ "type": "input_json_delta", "partial_json": "{\"comma" }

// Thinking delta
{ "type": "thinking_delta", "thinking": "讓我想想..." }
```

**範例：**
```json
{
  "type": "stream_event",
  "event": {
    "type": "content_block_delta",
    "index": 0,
    "delta": { "type": "text_delta", "text": "Hello" }
  },
  "parent_tool_use_id": null,
  "uuid": "stream-uuid",
  "session_id": "abc123"
}
```

---

### 1.6 `tool_progress` — 工具執行心跳

**何時發出：** 長時間工具執行期間週期性發出。

```typescript
type SDKToolProgressMessage = {
  type: "tool_progress";
  tool_use_id: string;
  tool_name: string;
  parent_tool_use_id: string | null;
  elapsed_time_seconds: number;
  task_id?: string;
  uuid: string;
  session_id: string;
};
```

---

### 1.7 `tool_use_summary` — 工具使用摘要

**何時發出：** 工具執行完成後。

```typescript
type SDKToolUseSummaryMessage = {
  type: "tool_use_summary";
  summary: string;
  preceding_tool_use_ids: string[];
  uuid: string;
  session_id: string;
};
```

---

### 1.8 `rate_limit_event` — 速率限制事件

**何時發出：** Session 遭遇 API 速率限制時。

```typescript
type SDKRateLimitEvent = {
  type: "rate_limit_event";
  rate_limit_info: {
    status: "allowed" | "allowed_warning" | "rejected";
    resetsAt?: number;       // Unix timestamp (ms)
    utilization?: number;    // 0.0 ~ 1.0
  };
  uuid: string;
  session_id: string;
};
```

---

### 1.9 `prompt_suggestion` — 後續提示建議

**何時發出：** 啟用 `promptSuggestions: true` 後，每回合結束時。

```typescript
type SDKPromptSuggestionMessage = {
  type: "prompt_suggestion";
  suggestion: string;
  uuid: string;
  session_id: string;
};
```

---

### 1.10 `auth_status` — 認證狀態

**何時發出：** 認證流程進行中。

```typescript
type SDKAuthStatusMessage = {
  type: "auth_status";
  isAuthenticating: boolean;
  output: string[];
  error?: string;
  uuid: string;
  session_id: string;
};
```

---

### 1.11 `keep_alive` — 心跳（WebSocket 模式）

```json
{ "type": "keep_alive" }
```

---

## 2. INPUT — CLI 接受的訊息類型

### 2.1 `user` — 使用者訊息

```typescript
type SDKUserMessage = {
  type: "user";
  uuid?: string;
  session_id: string;
  message: {
    role: "user";
    content: string | ContentBlock[];
  };
  parent_tool_use_id: string | null;
  isSynthetic?: boolean;
  tool_use_result?: unknown;
};
```

**純文字範例：**
```json
{
  "type": "user",
  "message": { "role": "user", "content": "列出此專案的檔案" },
  "parent_tool_use_id": null,
  "session_id": ""
}
```

**含圖片範例：**
```json
{
  "type": "user",
  "message": {
    "role": "user",
    "content": [
      { "type": "text", "text": "分析這張架構圖" },
      {
        "type": "image",
        "source": {
          "type": "base64",
          "media_type": "image/png",
          "data": "<base64-encoded>"
        }
      }
    ]
  },
  "parent_tool_use_id": null,
  "session_id": ""
}
```

### 2.2 `control_response` — 回應控制請求

見下方 §3 完整說明。

### 2.3 `keep_alive` — 心跳

```json
{ "type": "keep_alive" }
```

### 2.4 `update_environment_variables` — 更新環境變數（WebSocket 模式）

```json
{
  "type": "update_environment_variables",
  "variables": { "PATH": "/custom/path", "DEBUG": "1" }
}
```

---

## 3. CONTROL 協定 — 權限與工具核准

### 3.1 概述

Control 協定使用 `request_id` 進行請求/回應配對。CLI 發出 `control_request`，外部回應 `control_response`。

### 3.2 `control_request`（CLI → 外部）

```json
{
  "type": "control_request",
  "request_id": "<uuid>",
  "request": {
    "subtype": "<control_subtype>",
    // ... subtype 特定欄位
  }
}
```

### 3.3 `control_response`（外部 → CLI）

```json
{
  "type": "control_response",
  "response": {
    "subtype": "success",
    "request_id": "<uuid>",
    "response": { /* subtype 特定回應 */ }
  }
}
```

---

### 3.4 所有 Control Subtype

#### 3.4.1 `can_use_tool`（CLI → 外部）★ 最重要

**何時觸發：** Claude 想使用一個尚未被 allow 規則或 permissionMode 核准的工具。

**請求：**
```json
{
  "type": "control_request",
  "request_id": "uuid-123",
  "request": {
    "subtype": "can_use_tool",
    "tool_name": "Bash",
    "input": { "command": "rm -rf /tmp/test" },
    "tool_use_id": "tool-uuid",
    "decision_reason": "hook"
  }
}
```

**`tool_name` 可能值：** `Bash`, `Read`, `Write`, `Edit`, `Glob`, `Grep`, `WebFetch`, `WebSearch`, `Agent`, `AskUserQuestion`, `NotebookEdit`, `TodoWrite`, `Config`, `EnterWorktree`, `ExitPlanMode`, `TaskOutput`, `TaskStop`, `ListMcpResources`, `ReadMcpResource`, `mcp__<server>__<tool>`

**回應 — 允許：**
```json
{
  "type": "control_response",
  "response": {
    "subtype": "success",
    "request_id": "uuid-123",
    "response": {
      "behavior": "allow",
      "updatedInput": { "command": "rm -rf /tmp/test" }
    }
  }
}
```

`updatedInput` 為**必填**（可直接傳回原始 input 或修改後的版本）。

**回應 — 拒絕：**
```json
{
  "type": "control_response",
  "response": {
    "subtype": "success",
    "request_id": "uuid-123",
    "response": {
      "behavior": "deny",
      "message": "危險操作已被阻擋",
      "interrupt": true
    }
  }
}
```

`interrupt: true` 會中斷整個代理回合。

**TypeScript `PermissionResult` 型別：**
```typescript
type PermissionResult =
  | {
      behavior: "allow";
      updatedInput?: Record<string, unknown>;
      updatedPermissions?: PermissionUpdate[];
      toolUseID?: string;
    }
  | {
      behavior: "deny";
      message: string;
      interrupt?: boolean;
      toolUseID?: string;
    };
```

**`CanUseTool` callback 接收的 options：**
```typescript
type CanUseToolOptions = {
  signal: AbortSignal;
  suggestions?: PermissionUpdate[];  // 建議的權限更新
  blockedPath?: string;              // 觸發權限請求的檔案路徑
  decisionReason?: string;           // 觸發原因
  toolUseID: string;                 // 此工具呼叫的唯一 ID
  agentID?: string;                  // 子代理 ID（如適用）
};
```

#### 3.4.2 `AskUserQuestion` via `can_use_tool`

當 `tool_name === "AskUserQuestion"` 時，這是 Claude 的「澄清問題」機制：

**請求中的 input 結構：**
```json
{
  "questions": [
    {
      "question": "你想要哪種輸出格式？",
      "header": "格式",
      "options": [
        { "label": "摘要", "description": "簡短概覽" },
        { "label": "詳細", "description": "完整說明" }
      ],
      "multiSelect": false
    }
  ]
}
```

**回應 — 提供答案：**
```json
{
  "behavior": "allow",
  "updatedInput": {
    "questions": [ /* 原始 questions 陣列 */ ],
    "answers": {
      "你想要哪種輸出格式？": "摘要"
    }
  }
}
```

多選題用 `", "` 連接多個 label。

#### 3.4.3 `initialize`（外部 → CLI）

在第一則 `user` 訊息前註冊 hooks、MCP servers、agents。

```json
{
  "subtype": "initialize",
  "hooks": { "PreToolUse": [...] },
  "sdkMcpServers": ["server1"],
  "systemPrompt": "You are...",
  "agents": { "name": {...} }
}
```

#### 3.4.4 `interrupt`（外部 → CLI）

中斷當前代理回合。

```json
{ "subtype": "interrupt" }
```

#### 3.4.5 `set_permission_mode`（外部 → CLI）

```json
{
  "subtype": "set_permission_mode",
  "mode": "bypassPermissions"
}
```

**可用模式：** `default`, `acceptEdits`, `bypassPermissions`, `plan`, `delegate`, `dontAsk`

#### 3.4.6 `set_model`（外部 → CLI）

```json
{ "subtype": "set_model", "model": "claude-opus-4-6" }
```

#### 3.4.7 `set_max_thinking_tokens`（外部 → CLI）

```json
{ "subtype": "set_max_thinking_tokens", "max_thinking_tokens": 10000 }
```

#### 3.4.8 `mcp_status`（外部 → CLI）

查詢 MCP 伺服器狀態。回應包含 `McpServerStatus[]`。

```json
{ "subtype": "mcp_status" }
```

#### 3.4.9 `mcp_message`（雙向）

路由 JSON-RPC 至/從 MCP 伺服器。

```json
{
  "subtype": "mcp_message",
  "server_name": "filesystem",
  "message": { /* JSON-RPC */ }
}
```

#### 3.4.10 `mcp_reconnect`（外部 → CLI）

```json
{ "subtype": "mcp_reconnect", "serverName": "postgresql" }
```

#### 3.4.11 `mcp_toggle`（外部 → CLI）

```json
{ "subtype": "mcp_toggle", "serverName": "slack", "enabled": false }
```

#### 3.4.12 `mcp_set_servers`（外部 → CLI）

```json
{
  "subtype": "mcp_set_servers",
  "servers": {
    "postgres": {
      "type": "stdio",
      "command": "node",
      "args": ["server.js"],
      "env": { "DB_HOST": "localhost" }
    }
  }
}
```

#### 3.4.13 `rewind_files`（外部 → CLI）

```json
{
  "subtype": "rewind_files",
  "user_message_id": "msg_uuid",
  "dry_run": false
}
```

回應：`{ "canRewind": true, "filesChanged": ["a.ts", "b.ts"], "insertions": 10, "deletions": 5 }`

#### 3.4.14 `hook_callback`（CLI → 外部）

CLI 觸發已註冊的 hook callback。

```json
{
  "subtype": "hook_callback",
  "callback_id": "hook_1",
  "input": { /* hook 特定資料 */ }
}
```

---

### 3.5 權限評估流程

```
工具請求到達
    ↓
[1] Hooks（PreToolUse）
    → allow / deny / continue
    ↓
[2] Deny Rules（disallowed_tools + settings.json deny）
    → 若命中 → 拒絕（即使 bypassPermissions 也生效）
    ↓
[3] Permission Mode
    → bypassPermissions → 核准
    → acceptEdits → 檔案操作核准
    → 其他 → 繼續
    ↓
[4] Allow Rules（allowed_tools + settings.json allow）
    → 若命中 → 核准
    ↓
[5] canUseTool callback / control_request
    → dontAsk 模式下跳過此步驟直接拒絕
    → 否則發出 can_use_tool 請求等待回應
```

**`--dangerously-skip-permissions` / `allowDangerouslySkipPermissions`：** 等同於設定 `permissionMode: "bypassPermissions"`，所有未被 deny 規則阻擋的工具自動核准。

---

## 4. Content Block 類型

assistant 訊息的 `message.content` 陣列中的區塊：

### 4.1 `text` — 文字

```json
{ "type": "text", "text": "回應文字內容" }
```

### 4.2 `tool_use` — 工具呼叫

```json
{
  "type": "tool_use",
  "id": "toolu_01ABC123",
  "name": "Bash",
  "input": { "command": "ls -la", "description": "列出檔案" }
}
```

### 4.3 `tool_result` — 工具結果

出現在 user 訊息中（自動由 CLI 生成）：

```json
{
  "type": "tool_result",
  "tool_use_id": "toolu_01ABC123",
  "content": "file1.txt\nfile2.txt",
  "is_error": false
}
```

### 4.4 `thinking` — 延伸思考

```json
{
  "type": "thinking",
  "thinking": "讓我分析這個問題...",
  "budget_tokens": 5000
}
```

### 4.5 `image` — 圖片（user 訊息中）

```json
{
  "type": "image",
  "source": {
    "type": "base64",
    "media_type": "image/png",
    "data": "<base64>"
  }
}
```

---

## 5. Stream Events

啟用方式：SDK 設定 `includePartialMessages: true`，CLI 加上 `--verbose`。

所有 stream event 包裝在 `type: "stream_event"` 中：

```json
{
  "type": "stream_event",
  "event": { /* Anthropic Messages API 串流事件 */ },
  "parent_tool_use_id": null,
  "uuid": "...",
  "session_id": "..."
}
```

### 串流事件序列

```
message_start          → { "type": "message_start", "message": { ... } }
content_block_start    → { "type": "content_block_start", "index": 0, "content_block": { "type": "text", "text": "" } }
content_block_delta    → { "type": "content_block_delta", "index": 0, "delta": { "type": "text_delta", "text": "Hello" } }
content_block_delta    → { "type": "content_block_delta", "index": 0, "delta": { "type": "text_delta", "text": " world" } }
content_block_stop     → { "type": "content_block_stop", "index": 0 }
content_block_start    → { "type": "content_block_start", "index": 1, "content_block": { "type": "tool_use", "id": "toolu_01...", "name": "Bash", "input": {} } }
content_block_delta    → { "type": "content_block_delta", "index": 1, "delta": { "type": "input_json_delta", "partial_json": "{\"com" } }
content_block_delta    → { "type": "content_block_delta", "index": 1, "delta": { "type": "input_json_delta", "partial_json": "mand\":" } }
content_block_stop     → { "type": "content_block_stop", "index": 1 }
message_delta          → { "type": "message_delta", "delta": { "stop_reason": "tool_use" }, "usage": { "output_tokens": 42 } }
message_stop           → { "type": "message_stop" }
```

### Delta 類型

| Delta Type | 說明 |
|-----------|------|
| `text_delta` | `{ "type": "text_delta", "text": "..." }` |
| `input_json_delta` | `{ "type": "input_json_delta", "partial_json": "..." }` |
| `thinking_delta` | `{ "type": "thinking_delta", "thinking": "..." }` |

---

## 6. 工具 Input/Output Schema

### 所有內建工具名稱

| 工具名稱 | Input 類型 | 說明 |
|---------|-----------|------|
| `Agent` | `AgentInput` | 啟動子代理（別名 `Task`） |
| `AskUserQuestion` | `AskUserQuestionInput` | 向使用者詢問澄清問題 |
| `Bash` | `BashInput` | 執行 shell 命令 |
| `Read` | `FileReadInput` | 讀取檔案 |
| `Write` | `FileWriteInput` | 寫入檔案 |
| `Edit` | `FileEditInput` | 精確字串替換 |
| `Glob` | `GlobInput` | 模式匹配搜尋檔案 |
| `Grep` | `GrepInput` | ripgrep 正則搜尋 |
| `WebFetch` | `WebFetchInput` | 抓取網頁內容 |
| `WebSearch` | `WebSearchInput` | 網路搜尋 |
| `NotebookEdit` | `NotebookEditInput` | 編輯 Jupyter notebook |
| `TodoWrite` | `TodoWriteInput` | 管理工作清單 |
| `Config` | `ConfigInput` | 取得/設定配置 |
| `EnterWorktree` | `EnterWorktreeInput` | 進入 git worktree |
| `ExitPlanMode` | `ExitPlanModeInput` | 離開計畫模式 |
| `TaskOutput` | `TaskOutputInput` | 取得背景工作輸出 |
| `TaskStop` | `TaskStopInput` | 停止背景工作 |
| `ListMcpResources` | `ListMcpResourcesInput` | 列出 MCP 資源 |
| `ReadMcpResource` | `ReadMcpResourceInput` | 讀取 MCP 資源 |
| `mcp__<server>__<tool>` | 動態 | MCP 伺服器工具 |

### 關鍵工具 Input Schema

```typescript
// Bash
type BashInput = {
  command: string;
  timeout?: number;
  description?: string;
  run_in_background?: boolean;
  dangerouslyDisableSandbox?: boolean;
};

// Read
type FileReadInput = {
  file_path: string;
  offset?: number;
  limit?: number;
  pages?: string;   // PDF 頁面範圍 "1-5"
};

// Write
type FileWriteInput = {
  file_path: string;
  content: string;
};

// Edit
type FileEditInput = {
  file_path: string;
  old_string: string;
  new_string: string;
  replace_all?: boolean;
};

// Glob
type GlobInput = {
  pattern: string;
  path?: string;
};

// Grep
type GrepInput = {
  pattern: string;
  path?: string;
  glob?: string;
  type?: string;
  output_mode?: "content" | "files_with_matches" | "count";
  "-i"?: boolean;
  "-n"?: boolean;
  "-B"?: number;
  "-A"?: number;
  "-C"?: number;
  context?: number;
  head_limit?: number;
  offset?: number;
  multiline?: boolean;
};

// Agent
type AgentInput = {
  description: string;
  prompt: string;
  subagent_type: string;
  model?: "sonnet" | "opus" | "haiku";
  resume?: string;
  run_in_background?: boolean;
  max_turns?: number;
  name?: string;
  team_name?: string;
  mode?: "acceptEdits" | "bypassPermissions" | "default" | "dontAsk" | "plan";
  isolation?: "worktree";
};

// AskUserQuestion
type AskUserQuestionInput = {
  questions: Array<{
    question: string;
    header: string;    // 最多 12 字元
    options: Array<{ label: string; description: string; preview?: string }>;
    multiSelect: boolean;
  }>;
};

// WebFetch
type WebFetchInput = { url: string; prompt: string };

// WebSearch
type WebSearchInput = {
  query: string;
  allowed_domains?: string[];
  blocked_domains?: string[];
};
```

### 關鍵工具 Output Schema

```typescript
// Bash
type BashOutput = {
  stdout: string;
  stderr: string;
  rawOutputPath?: string;
  interrupted: boolean;
  isImage?: boolean;
  backgroundTaskId?: string;
  backgroundedByUser?: boolean;
  dangerouslyDisableSandbox?: boolean;
  returnCodeInterpretation?: string;
  structuredContent?: unknown[];
  persistedOutputPath?: string;
  persistedOutputSize?: number;
};

// Read
type FileReadOutput =
  | { type: "text"; file: { filePath: string; content: string; numLines: number; startLine: number; totalLines: number } }
  | { type: "image"; file: { base64: string; type: string; originalSize: number; dimensions?: {...} } }
  | { type: "notebook"; file: { filePath: string; cells: unknown[] } }
  | { type: "pdf"; file: { filePath: string; base64: string; originalSize: number } }
  | { type: "parts"; file: { filePath: string; originalSize: number; count: number; outputDir: string } };

// Edit
type FileEditOutput = {
  filePath: string;
  oldString: string;
  newString: string;
  originalFile: string;
  structuredPatch: Array<{ oldStart: number; oldLines: number; newStart: number; newLines: number; lines: string[] }>;
  userModified: boolean;
  replaceAll: boolean;
  gitDiff?: { filename: string; status: "modified" | "added"; additions: number; deletions: number; changes: number; patch: string };
};

// Write
type FileWriteOutput = {
  type: "create" | "update";
  filePath: string;
  content: string;
  structuredPatch: Array<{...}>;
  originalFile: string | null;
  gitDiff?: {...};
};
```

---

## 7. Session 管理

### Session ID

- 由 CLI 透過 `crypto.randomUUID()` 產生
- 包含在每條發出的訊息中
- 使用 `--resume <session-id>` 恢復之前的 session
- 使用 `--resume <session-id> --fork-session` 分叉

### 多回合對話

收到 `result` 後，發送另一則 `user` 訊息即可繼續對話。WebSocket 保持連線期間 CLI 持續運作。

### Context Compaction 流程

```
1. status: "compacting" 訊息發出
2. compaction 完成後: compact_boundary 訊息
3. status: null 訊息（compaction 結束）
```

---

## 8. 傳輸層細節

### WebSocket 重連參數

| 參數 | 值 |
|------|-----|
| 最大重試次數 | 3 |
| 基礎延遲 | 1000ms |
| 最大延遲 | 30000ms |
| 計算公式 | `min(1000 * 2^(attempt-1), 30000)` |
| Ping 間隔 | 10 秒 |

### 混合傳輸（HTTP POST 發送）

設定 `CLAUDE_CODE_POST_FOR_SESSION_INGRESS_V2` 環境變數後：
- WebSocket 接收訊息
- HTTP POST 發送訊息至 `https://host/session/path/events`
- 最大 POST 重試：10 次
- POST 退避：`min(500 * 2^(attempt-1), 8000)`

### 認證 Header

```
Authorization: Bearer <session_access_token>
X-Environment-Runner-Version: <version>
X-Last-Request-Id: <uuid>  （重連時用於訊息重播）
```

Token 優先順序：
1. `CLAUDE_CODE_SESSION_ACCESS_TOKEN` 環境變數
2. 內部 session ingress token
3. `CLAUDE_CODE_WEBSOCKET_AUTH_FILE_DESCRIPTOR` 的 file descriptor

---

## 附錄 A：Hook Events 完整列表

| Hook Event | 說明 | Python | TypeScript |
|-----------|------|:------:|:----------:|
| `PreToolUse` | 工具呼叫前（可阻擋/修改） | Yes | Yes |
| `PostToolUse` | 工具執行後 | Yes | Yes |
| `PostToolUseFailure` | 工具執行失敗後 | Yes | Yes |
| `UserPromptSubmit` | 使用者提交 prompt | Yes | Yes |
| `Stop` | 代理執行停止 | Yes | Yes |
| `SubagentStart` | 子代理初始化 | Yes | Yes |
| `SubagentStop` | 子代理完成 | Yes | Yes |
| `PreCompact` | Compaction 請求 | Yes | Yes |
| `PermissionRequest` | 權限對話框觸發 | Yes | Yes |
| `Notification` | 代理狀態訊息 | Yes | Yes |
| `SessionStart` | Session 初始化 | No | Yes |
| `SessionEnd` | Session 終止 | No | Yes |
| `Setup` | Session 設定/維護 | No | Yes |
| `TeammateIdle` | 隊友閒置 | No | Yes |
| `TaskCompleted` | 背景工作完成 | No | Yes |
| `ConfigChange` | 設定檔變更 | No | Yes |
| `WorktreeCreate` | Git worktree 建立 | No | Yes |
| `WorktreeRemove` | Git worktree 移除 | No | Yes |

---

## 附錄 B：Permission Modes 對照表

| 模式 | 說明 | 自動核准 |
|------|------|---------|
| `default` | 預設行為 | 無，未匹配的工具觸發 canUseTool |
| `dontAsk` | 不詢問直接拒絕（僅 TS） | 僅 allowedTools 中的工具 |
| `acceptEdits` | 自動接受檔案操作 | Edit, Write, mkdir, touch, rm, mv, cp |
| `bypassPermissions` | 繞過所有權限檢查 | 所有工具（deny 規則除外） |
| `plan` | 計畫模式，不執行 | 無（可用 AskUserQuestion） |

---

## 附錄 C：完整訊息流程範例

```
── 連線建立 ──
← CLI:  { "type": "system", "subtype": "init", ... }

── 發送使用者訊息 ──
→ 外部: { "type": "user", "message": { "role": "user", "content": "修復 auth.py 的 bug" }, ... }

── 串流回應（若啟用） ──
← CLI:  { "type": "stream_event", "event": { "type": "message_start", ... } }
← CLI:  { "type": "stream_event", "event": { "type": "content_block_start", ... } }
← CLI:  { "type": "stream_event", "event": { "type": "content_block_delta", ... } }
...

── 完整 assistant 回應 ──
← CLI:  { "type": "assistant", "message": { "content": [
           { "type": "text", "text": "讓我先讀取檔案..." },
           { "type": "tool_use", "id": "toolu_01", "name": "Read", "input": { "file_path": "/path/auth.py" } }
         ] } }

── 權限請求 ──
← CLI:  { "type": "control_request", "request_id": "uuid-1", "request": {
           "subtype": "can_use_tool", "tool_name": "Read",
           "input": { "file_path": "/path/auth.py" }, "tool_use_id": "toolu_01" } }

── 核准工具使用 ──
→ 外部: { "type": "control_response", "response": {
           "subtype": "success", "request_id": "uuid-1",
           "response": { "behavior": "allow", "updatedInput": { "file_path": "/path/auth.py" } } } }

── 工具執行心跳 ──
← CLI:  { "type": "tool_progress", "tool_use_id": "toolu_01", "tool_name": "Read", "elapsed_time_seconds": 2 }

── 工具摘要 ──
← CLI:  { "type": "tool_use_summary", "summary": "讀取了 auth.py", "preceding_tool_use_ids": ["toolu_01"] }

── 下一輪 assistant 回應（含編輯） ──
← CLI:  { "type": "assistant", "message": { "content": [
           { "type": "text", "text": "找到 bug，正在修復..." },
           { "type": "tool_use", "id": "toolu_02", "name": "Edit", "input": { ... } }
         ] } }

── 再次權限請求 ──
← CLI:  { "type": "control_request", ... }
→ 外部: { "type": "control_response", ... }

── 最終結果 ──
← CLI:  { "type": "result", "subtype": "success", "result": "已成功修復 auth.py 中的認證 bug。",
           "duration_ms": 12345, "total_cost_usd": 0.045, "num_turns": 3 }
```

---

## 附錄 D：SDK 選項到 CLI 旗標對應

| SDK Option | CLI Flag | 說明 |
|-----------|----------|------|
| `includePartialMessages: true` | `--verbose` | 啟用 stream_event |
| `permissionMode` | `--permission-mode` | 權限模式 |
| `allowDangerouslySkipPermissions` | `--dangerously-skip-permissions` | 繞過所有權限 |
| `model` | `--model` | 指定模型 |
| `maxTurns` | `--max-turns` | 最大回合數 |
| `maxBudgetUsd` | `--max-budget-usd` | 預算上限 |
| `resume` | `--resume` | 恢復 session |
| `continue` | `--continue` | 繼續最近對話 |
| `systemPrompt` | `--system-prompt` | 系統提示 |
| `promptSuggestions: true` | `--prompt-suggestions` | 啟用後續提示建議 |
| `cwd` | `--cwd` | 工作目錄 |
