# Codex CLI vs Claude Code CLI 深度比較

> 日期：2026-03-19
> 目的：研究 Codex CLI 與 Claude Code CLI 在串流模式、JSONL、協定等方面的差異，評估 tmux-box 多引擎支援可行性

---

## 1. 基本資訊

| | Codex CLI | Claude Code |
|--|-----------|-------------|
| **Repo** | [github.com/openai/codex](https://github.com/openai/codex) (66.3k stars) | 閉源 (`@anthropic-ai/claude-code` npm) |
| **授權** | Apache-2.0 | 商業 |
| **語言** | Rust 95.6%（核心）+ thin npm wrapper | TypeScript (Node.js) |
| **npm** | `@openai/codex` + `@openai/codex-sdk` | `@anthropic-ai/claude-code` |
| **預設模型** | GPT-5.4 (1M context) | Claude Opus 4.6 (200K context) |
| **專案指引** | `AGENTS.md` | `CLAUDE.md` |
| **Config 格式** | TOML (`~/.codex/config.toml`) | JSON (`~/.claude/settings.json`) |

---

## 2. 架構差異

### Claude Code
- **單體 TypeScript** — 直接以 Node.js 執行
- **TUI**: Ink (React for CLI)
- **通訊**: stdin/stdout pipe（stream-json NDJSON）
- **沙箱**: 應用層 permission list + hooks

### Codex CLI
- **Rust monorepo** — 約 70 個 crate，npm 包是 thin wrapper 呼叫 Rust binary
- **TUI**: Ratatui (Rust native)
- **通訊**: App Server protocol（JSON-RPC 2.0，支援 **WebSocket** 和 **stdio** 兩種傳輸）
- **沙箱**: OS 核心層（macOS Seatbelt / Linux Landlock+seccomp）

```
Codex 架構：
┌─────────────┐  ┌─────────────┐  ┌─────────────┐
│  TUI (CLI)  │  │  VS Code    │  │  Desktop    │
└──────┬──────┘  └──────┬──────┘  └──────┬──────┘
       │                │                │
       └────────────────┼────────────────┘
                        │
              ┌─────────▼─────────┐
              │   App Server      │  ← JSON-RPC (WS / stdio)
              │   (codex-rs)      │
              ├───────────────────┤
              │   codex-core      │  ← Op/Event 佇列驅動
              │   sandbox         │  ← OS 原生沙箱
              │   MCP client      │
              │   rollout (JSONL) │
              └───────────────────┘
```

---

## 3. 互動模式對照

| 模式 | Codex CLI | Claude Code |
|------|-----------|-------------|
| **互動式 TUI** | `codex` | `claude` |
| **非互動** | `codex exec "prompt"` | `claude -p "prompt"` |
| **JSON 串流** | `codex exec --json "prompt"` | `claude -p --output-format stream-json "prompt"` |
| **純文字輸出** | `codex exec "prompt"`（預設） | `claude -p --output-format text "prompt"` |
| **結構化輸出** | `codex exec --output-schema schema.json` | 無 |
| **寫檔輸出** | `codex exec -o output.txt` | stdout redirect |
| **不保存 session** | `codex exec --ephemeral` | 無對應 flag |
| **恢復對話** | `codex resume` / `codex resume --last` | `claude --resume <id>` / `claude --continue` |
| **分支對話** | `codex fork` | `claude --resume <id> --fork-session` |
| **SDK** | `@openai/codex-sdk` (TS + Python) | 無官方 SDK（用 stream-json pipe） |

---

## 4. 串流協定比較（核心差異）

### 4.1 事件類型對照

**Claude Code stream-json (~15 種頂層 type):**

```
system (init/status/compact_boundary/hook_*/task_*/files_persisted)
assistant
user
result
stream_event
control_request
tool_progress
tool_use_summary
rate_limit_event
prompt_suggestion
auth_status
keep_alive
```

**Codex CLI exec --json (thread/turn/item 三層, 70+ 種事件):**

```
thread.started
turn.started / turn.completed / turn.failed
item.started / item.updated / item.completed
error
```

其中 `item` 有以下子類型：
- `agent_message` — agent 回覆文字
- `reasoning` — 推理摘要
- `command_execution` — 指令執行（含 stdout, stderr, exit_code）
- `file_change` — 檔案變更（path, kind: add/delete/update）
- `mcp_tool_call` — MCP 工具呼叫
- `collab_tool_call` — 子 agent 協作
- `web_search` — 網路搜尋
- `todo_list` — 待辦計畫清單
- `error` — 非致命錯誤

### 4.2 結構範例對照

**Claude Code — assistant 訊息：**
```json
{
  "type": "assistant",
  "uuid": "msg-uuid",
  "session_id": "abc123",
  "parent_tool_use_id": null,
  "message": {
    "id": "msg_01XYZ",
    "role": "assistant",
    "model": "claude-opus-4-6",
    "content": [
      { "type": "text", "text": "讓我讀取檔案..." },
      { "type": "tool_use", "id": "toolu_01", "name": "Read", "input": {"file_path": "/path"} }
    ],
    "stop_reason": "tool_use",
    "usage": { "input_tokens": 100, "output_tokens": 50 }
  }
}
```

**Codex CLI — 對等的事件序列：**
```json
{"type":"turn.started"}
{"type":"item.started","item":{"id":"item_1","type":"agent_message","text":"讓我讀取檔案..."}}
{"type":"item.completed","item":{"id":"item_1","type":"agent_message","text":"讓我讀取檔案..."}}
{"type":"item.started","item":{"id":"item_2","type":"command_execution","command":"cat /path","status":"in_progress"}}
{"type":"item.completed","item":{"id":"item_2","type":"command_execution","command":"cat /path","exit_code":0,"aggregated_output":"file content..."}}
{"type":"turn.completed","usage":{"input_tokens":100,"output_tokens":50,"cached_input_tokens":0}}
```

**關鍵差異：**
- Claude Code 把文字和工具呼叫放在同一個 `assistant` 訊息的 `content[]` 陣列中
- Codex 把每個動作拆成獨立的 `item`，有明確的 started/completed 生命週期
- Claude Code 的工具是抽象的（`tool_use` + `tool_result`），Codex 的工具是具體型別（`command_execution`, `file_change` 等）

### 4.3 Token 串流（逐字顯示）

**Claude Code — stream_event:**
```json
{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}}
```

**Codex CLI — AgentMessageDelta（僅在 app-server protocol 中，exec --json 不含）:**
```json
// app-server notification (非 exec --json)
{"method":"AgentMessageDelta","params":{"text":"Hello"}}
```

**注意：** `codex exec --json` 目前**不輸出增量 delta**，只輸出完成的 item。
逐字串流僅在 app-server protocol（WebSocket/stdio JSON-RPC）中提供。

### 4.4 權限請求

**Claude Code — control_request:**
```json
{
  "type": "control_request",
  "request_id": "uuid-123",
  "request": {
    "subtype": "can_use_tool",
    "tool_name": "Bash",
    "input": { "command": "rm -rf /tmp/test" },
    "tool_use_id": "tool-uuid"
  }
}
```
回應: `control_response` with `behavior: "allow"` 或 `"deny"`

**Codex CLI — exec --json 不支援互動式審批。** 審批僅在以下場景：
- TUI 互動模式
- App Server protocol（`ExecCommandApproval`, `ApplyPatchApproval` 等 JSON-RPC 請求）

審批選項比 Claude Code 更豐富：
- `Approved` — 單次通過
- `ApprovedForSession` — 整個 session 通過
- `ApprovedExecpolicyAmendment` — 通過並加入規則（prefix 匹配）
- `Abort` — 拒絕

---

## 5. JSONL Session 記錄比較

### Claude Code

```
~/.claude/projects/{project-hash}/{session_id}.jsonl
```

- 純文字 JSONL，無壓縮
- 每行一個 JSON，type 有：`progress`, `assistant`, `user`, `system`, `file-history-snapshot`, `queue-operation`, `pr-link`
- 欄位使用 **camelCase**（`sessionId`, `parentToolUseID`）
- 內容包含完整的 message（含 tool_use、tool_result、thinking）
- 無獨立索引檔（靠檔案系統掃描）

### Codex CLI

```
~/.codex/sessions/YYYY/MM/DD/rollout-YYYY-MM-DDThh-mm-ss-{thread_id}.jsonl.zst
```

- JSONL + **Zstandard 壓縮**（`.jsonl.zst`）
- 按日期分目錄
- 額外有全域索引：`~/.codex/session_index.jsonl`（append-only）
- 額外有 SQLite state DB（metadata、logs）
- 封存機制：`~/.codex/sessions/archived/`

**Rollout 檔案結構：**

每行 JSON 有兩種頂層類型：
1. `RolloutLine::Meta` — session 元資料（檔案開頭）：thread_id, source, model, sandbox/approval policies
2. `RolloutLine::Item` — 事件紀錄：user messages, assistant responses, reasoning, tool calls, token usage

**持久化模式 (EventPersistenceMode)：**
- `Full` — 完整紀錄
- `Minimal` — 僅必要元資料
- `None` — 不持久化（`--ephemeral`）

---

## 6. 任務追蹤比較

### Claude Code — TaskCreate / TaskUpdate

- 以 `tool_use` content block 形式嵌在 `assistant` 訊息中
- 工具名：`TaskCreate`（建立）、`TaskUpdate`（更新狀態）、`TaskGet`、`TaskList`
- 狀態：`open` / `in_progress` / `completed`
- TUI 渲染：✔/■/□ 圖示 + 刪除線
- **不是獨立訊息類型**，需從 tool_use 中解析

### Codex CLI — Plan / TodoList

- 在 `exec --json` 中以 `item` 類型 `todo_list` 出現
- 在 app-server protocol 中有 `PlanUpdate` 和 `PlanDelta` 專用事件
- 結構：`Vec<PlanItemArg>` with `step` (描述) + `status` (Pending/InProgress/Completed)
- **是獨立的 item 類型**，不需從其他訊息中解析

---

## 7. 子 Agent / 多代理

### Claude Code
- `Agent` tool（在 `tool_use` content block 中）
- `parent_tool_use_id` 標記巢狀層級
- 無明確的子 agent 生命週期事件

### Codex CLI
- 原生多 agent 系統（最多 6 threads, depth 1）
- 內建角色：`default`, `worker`, `explorer`
- 自定 agent：放在 `~/.codex/agents/` 或 `.codex/agents/`（TOML 格式）
- 協作工具：`spawnAgent`, `sendInput`, `resumeAgent`, `wait`, `closeAgent`
- 在 `exec --json` 中以 `collab_tool_call` item 類型出現
- 在 app-server protocol 中有完整的 `Collab*` 事件系列

---

## 8. App Server Protocol（Codex 獨有，tmux-box 可參考）

Codex 的 App Server 是統一的後端，CLI、VS Code、Desktop 共用：

```bash
codex app-server --listen ws://0.0.0.0:8080   # WebSocket
codex app-server --listen stdio://              # stdio
```

使用 **JSON-RPC 2.0**，分三類訊息：

**Client → Server 請求：**
- `thread/start` — 建立新 thread
- `turn/start` — 發送使用者輸入
- `turn/interrupt` — 中斷
- `thread/resume` / `thread/fork` / `thread/archive`
- `config/read` / `config/write`
- `review/start` — 程式碼審查

**Server → Client 通知（40+ 種）：**
- `AgentMessageDelta` — 逐字串流
- `ReasoningTextDelta` — 推理串流
- `ItemStarted` / `ItemCompleted`
- `TurnStarted` / `TurnCompleted`
- `CommandExecOutputDelta` — 指令輸出增量
- `FileChangeOutputDelta`
- `PlanDelta` — 計畫更新
- `ContextCompacted`
- `HookStarted` / `HookCompleted`
- `ErrorNotification`

**Server → Client 請求（需回應）：**
- `ExecCommandApproval` — 指令審批
- `ApplyPatchApproval` — 檔案變更審批
- `PermissionsRequestApproval`
- `ToolRequestUserInput`

**這與 Claude Code 的 stream-json pipe 最大差異**：
- Claude Code 是單向 stdout 輸出 + 少量 stdin 控制
- Codex App Server 是完整的雙向 JSON-RPC，支援請求/回應配對

---

## 9. 其他差異

| 功能 | Codex CLI | Claude Code |
|------|-----------|-------------|
| **Memory** | 兩階段自動提取（Phase 1 提取 + Phase 2 合併），存為 `raw_memories.md` | `MEMORY.md` + memory files，手動/自動寫入 |
| **Skills** | 內建 skills 系統（loader, manager, remote） | 透過 Skill tool 觸發 plugin |
| **Hooks** | `SessionStart`, `UserPromptSubmit`, `Stop` 三種 | `PreToolUse`, `PostToolUse`, `UserPromptSubmit`, `Stop` 等 20+ 種 |
| **Code Review** | `/review` 指令，有 `ReviewFinding` + confidence score | 無內建（靠 skill plugin） |
| **Web Search** | 內建一等公民 | 需 MCP 工具或 WebSearch tool |
| **即時語音** | Realtime Conversation API 整合 | 無 |
| **MCP** | 雙向（client + 可作為 MCP server） | 僅 client |
| **沙箱逃逸偵測** | 自動偵測 exit code + stderr pattern，可暫時升級權限 | 無 |
| **Guardian Agent** | 特殊子 agent 自動審批（基於風險評估） | 無 |
| **對話回滾** | `ThreadRollback { num_turns }` / `Undo` | 無 |
| **Context 壓縮** | `Compact` op，有 `ContextCompacted` 事件 | `/compact` 指令，有 `compact_boundary` 訊息 |

---

## 10. tmux-box 整合評估

### 如果要支援 Codex CLI 作為 Stream Mode 的引擎

**可行路徑 A：`codex exec --json`（最接近現有架構）**
- relay 改為執行 `codex exec --json "prompt"` 而非 `claude -p ...`
- 解析 `thread.started`, `item.*`, `turn.*` 事件
- 缺點：**不支援互動式審批**、**無逐字串流**

**可行路徑 B：`codex app-server --listen ws://`（最完整）**
- relay 直接連 Codex App Server 的 WebSocket
- 完整雙向通訊：送使用者輸入、接收串流、回應審批
- 缺點：架構差異大，需要全新的 protocol adapter

**可行路徑 C：抽象化 adapter 層**
- 定義統一的 `StreamEvent` 介面
- Claude Code adapter：解析 stream-json
- Codex adapter：解析 exec --json 或 app-server protocol
- SPA 只處理統一格式

### 主要映射關係

| tmux-box 統一概念 | Claude Code | Codex CLI |
|-------------------|-------------|-----------|
| Session 初始化 | `system/init` | `thread.started` |
| 回合開始 | 收到 `user` 後 | `turn.started` |
| 回合結束 | `result` | `turn.completed` |
| 文字回覆 | `assistant` (text block) | `item` (agent_message) |
| 工具呼叫 | `assistant` (tool_use block) | `item` (command_execution / file_change) |
| 逐字串流 | `stream_event` (delta) | `AgentMessageDelta` (app-server only) |
| 權限請求 | `control_request` | `ExecCommandApproval` (app-server only) |
| 任務清單 | `TaskCreate/TaskUpdate` tool_use | `item` (todo_list) / `PlanUpdate` |
| 費用 | `result.total_cost_usd` | `turn.completed.usage` (tokens only, no USD) |
| 歷史載入 | ParseJSONL from `.jsonl` | 讀取 `.jsonl.zst` (需解壓) |

---

## Sources

- [OpenAI Codex GitHub](https://github.com/openai/codex)
- [Codex CLI Reference](https://developers.openai.com/codex/cli/reference)
- [Codex Non-interactive Mode](https://developers.openai.com/codex/noninteractive)
- [Codex SDK Documentation](https://developers.openai.com/codex/sdk)
- [Codex Agent Approvals & Security](https://developers.openai.com/codex/agent-approvals-security)
- [Codex Sandboxing](https://developers.openai.com/codex/concepts/sandboxing)
- [Codex Multi-agents / Subagents](https://developers.openai.com/codex/subagents)
- [Codex App Server Architecture (DeepWiki)](https://deepwiki.com/openai/codex)
- [How Codex is Built (Pragmatic Engineer)](https://newsletter.pragmaticengineer.com/p/how-codex-is-built)
