# OpenCode Hook Integration Design

## 目標

在既有 `cc` / `codex` agent hook 架構下，新增一套供 `opencode` 使用的等位 hooks 整合，讓 Purdex 可以：

1. 辨識 `opencode` process 並接受其 hook 事件。
2. 以 host-global plugin 方式安裝 / 移除 / 檢查 `opencode` hooks，對齊現有 `host > hooks` 控制語意。
3. 將 `opencode` 原生 event 映射為 Purdex 既有的 normalized status 流程，並對齊 Claude Code 的 subagent 處理語意。
4. 在設定頁面中把 `opencode` 當成第三種可管理的 agent hook module。

## 背景與限制

- `cc` 與 `codex` 走 CLI 原生 hook 設定檔：`~/.claude/settings.json`、`~/.codex/hooks.json`。
- `opencode` 沒有對等的 CLI hook 設定檔；官方擴充點是 plugin system。
- `opencode` plugin 可在 `.opencode/plugins/` 放 JS/TS 模組，OpenCode 啟動時自動載入。
- plugin 可透過 `event` 與 `chat.message` 等 hook 監聽 session / permission / message lifecycle。
- 現有 `pdx hook --agent <type> <event>` 已處理 tmux session、pane、sender PID/start time、daemon URL/token，應直接重用。

## 方案摘要

### 1. 新增 `internal/agent/opencode` provider

Provider 責任：

- `Type()` 回傳 `opencode`
- `DisplayName()` 回傳 `OpenCode`
- `Identify()` 支援：
  - executable basename = `opencode`
  - JS runtime argv 含 `opencode-ai` / `/opencode/`
- `Claim()`：hook event 時以 `agent_type == "opencode"` 為準
- `DeriveStatus()`：處理 plugin 送進來的語意事件
- `HookInstaller`：管理 host-global plugin 檔案

Provider 與 plugin 的責任邊界：

- **OpenCode plugin 層**：負責 OpenCode-specific event 正規化
  - 訂閱 `event` / `chat.message` / `tool.execute.before` / `tool.execute.after`
  - `callID -> agent_id`
  - `args.subagent_type` / `args.agent` -> `agent_type`
  - `session.error` 後的 `session.idle` suppress
  - 將正規化後的事件送進 `pdx hook --agent opencode <EventName>`
- **Go provider 層**：只處理已正規化的 Purdex event
  - `DeriveStatus()` 不解析 OpenCode 原生 schema
  - `HookInstaller` 不承擔 event mapping，只負責安裝/移除/檢查 managed plugin

對齊 Claude Code 基準的要求：

- 使用相同 event 名稱集合：`SessionStart`、`UserPromptSubmit`、`SubagentStart`、`SubagentStop`、`Stop`、`StopFailure`、`PermissionRequest`、`SessionEnd`
- `SubagentStart` / `SubagentStop` 與 `cc` 一樣只更新 subagent tracking，不改主 status
- subagent 事件 payload 也保留 `agent_id` / `agent_type`，讓現有 agent module 可以直接沿用

### 2. Hook 安裝方式改為 host-global plugin

安裝檔案位置：

`~/.config/opencode/plugins/pdx-agent-hooks.js`

原因：

- 這是 `opencode` 官方支援的擴充路徑
- 不需要修改使用者全域 `~/.config/opencode/opencode.json`
- `host > hooks` 現有 UI / API 是 host-scoped toggle，不是 project-scoped toggle
- 對使用者而言，按下 Install/Remove 應代表「這台 host 上的 OpenCode hooks 啟用/停用」，而不是只對某個 repo 生效

plugin 檔由 `pdx setup --agent opencode` 或 `/api/hooks/opencode/setup` 產生；內容帶入實際 `pdx` executable path。

這個設計刻意對齊現有 `cc` / `codex` hook card 的控制語意：

- `GET /api/hooks/opencode/status` 代表 host 上的全域安裝狀態
- `POST /api/hooks/opencode/setup` 的 install/remove 也是 host-global 作用域
- 不要求前端額外提供 repo path / cwd / git root

### 3. plugin 不直接 POST daemon，改呼叫既有 `pdx hook`

plugin 只做 event mapping，真正送事件一律走：

`"/path/to/pdx" hook --agent opencode <EventName>`

優點：

- 不重做 daemon URL / token 解析
- 不重做 tmux session / pane lookup
- 不重做 sender PID / start time provenance
- verify path 與現有 `cc` / `codex` 完全一致

### 3.1 OpenCode plugin source 的實作邊界

為了避免把核心 event mapping 藏在 `hooks.go` 的內嵌字串中，本次實作要求 plugin source 有獨立 owner：

- managed plugin 內容由獨立 template / source 檔產生
- `hooks.go` 只負責 render / write / check / remove
- OpenCode-specific lifecycle mapping 不應直接散落在 installer 邏輯內

### 4. `opencode` event → Purdex event 映射

plugin 監聽以下 OpenCode hooks：

| OpenCode hook | 條件 | 送給 `pdx hook` 的事件 | 說明 |
|---------------|------|-------------------------|------|
| `event` | `session.created` | `SessionStart` | 新 session 建立 |
| `chat.message` | 每次 user turn | `UserPromptSubmit` | 開始新一輪工作 |
| `tool.execute.before` | `tool == "task"` | `SubagentStart` | 啟動 subagent/task |
| `tool.execute.after` | `tool == "task"` | `SubagentStop` | subagent/task 結束 |
| `event` | `permission.asked` | `PermissionRequest` | 工具權限等待 |
| `event` | `question.asked` | `PermissionRequest` | user question / elicitation 也視為 waiting |
| `event` | `session.idle` | `Stop` | 正常回到 idle |
| `event` | `session.error` | `StopFailure` | 回報錯誤狀態 |
| `event` | `session.deleted` | `SessionEnd` | session 結束 |

補充：

- `session.error` 後若緊接 `session.idle`，plugin 需 suppress 該次 `Stop`，避免錯誤狀態被立即清掉。
- `tool.execute.before/after(task)` 使用 `callID` 當 `agent_id`；`args.subagent_type` 或 `args.agent` 映射為 `agent_type`，若缺少則 fallback `task`。
- 這裡刻意不用 `message.part.updated` 推 subagent lifecycle，因為 `task` tool hook 可直接拿到穩定 `callID`，較接近 Claude Code 的 `agent_id` 配對模型。

### 4.2 Error guard 對齊要求

僅靠 plugin suppress `session.error -> session.idle` 不足以保證狀態正確，backend 也要提供第二層保護。

本次規格要求：

- **`SessionEnd` 必須能在 error 狀態下通過**，不能被現有 error guard 擋掉
- **對 `opencode` 而言，`Stop` 不是 error 清除白名單事件**
- `opencode` error 狀態只能由以下事件清除：
  - `UserPromptSubmit`
  - `SessionStart`
  - `SessionEnd`

理由：

- OpenCode 的 `session.idle` 是執行結束訊號，但在 `session.error` 後可能只是 error 後續收尾，不能視為成功回到 idle
- 若 backend 仍把 `Stop` 當成 error 清除事件，任何 plugin suppress 漏網都會把錯誤狀態洗掉
- `SessionEnd` 若被 error guard 擋掉，session/subagent cleanup 會失效，與 Claude Code 既有 lifecycle 模型衝突

### 4.1 觀察項：`session.status=busy/retry`

目前先不把 `session.status=busy/retry` 映射成 `UserPromptSubmit` 或其他 running 類事件。

原因：

- 這不是 Claude Code hook 的直接等位事件，比較偏 OpenCode 特有的執行態訊號。
- 若直接映射為 `UserPromptSubmit`，會把「使用者送出 prompt 邊界」和「執行中狀態變化」混在一起。
- 先以 `chat.message` 作為唯一 running 起點，比較容易對齊既有 Claude Code 語意。

但這個訊號仍記錄為後續觀察項：

- 若實測發現 `opencode` 在 permission/question/error 後恢復執行時，Purdex 長時間停在舊狀態，才再考慮把 `busy/retry` 納入第二階段設計。

### 5. `DeriveStatus()` 規則

`opencode` provider 使用和 `cc` 相同的 normalized status 集合：

| Event | Status |
|-------|--------|
| `SessionStart` | `idle` |
| `UserPromptSubmit` | `running` |
| `SubagentStart` | 無狀態變更 |
| `SubagentStop` | 無狀態變更 |
| `PermissionRequest` | `waiting` |
| `Stop` | `idle` |
| `StopFailure` | `error` |
| `SessionEnd` | `clear` |

raw detail 保留：

- `UserPromptSubmit`: `session_id`, `message_id`, `agent`, `modelName`, `source`
- `SubagentStart`: `agent_id`, `agent_type`, `description`, `prompt`
- `SubagentStop`: `agent_id`, `agent_type`, `title`, `output`
- `PermissionRequest`: `request_type`, `permission`, `patterns`, `questions`
- `StopFailure`: `error`, `error_details`

### 5.1 Subagent 處理基準

這一段明確對齊目前 Claude Code provider / agent module 的行為：

- `SubagentStart` / `SubagentStop` 是 transient event，只進 broadcast path，不改主 status
- event 需讓 backend 能用現有 `applyFrameEvent` / `syncProjectionState` 流程追蹤 active subagents
- `SessionStart` / `SessionEnd` 清理 subagent 狀態的語意不變
- 若 `task` tool 異常中斷而沒收到對應 stop，殘留 subagent 與目前 `cc` 一樣屬 best-effort；會由後續 session lifecycle 事件或新一輪 start/clear 清理

進一步要求：

- `SessionStart` 的 cleanup 不能只清 in-memory `m.subagents`；也必須同步清除 projection / frame 上殘留的 `Subagents`
- 否則 `handleEvent()` 在 `SessionStart` 後重新 `syncProjectionState()` 時，舊的 frame subagents 會被寫回記憶體，造成跨輪殘留
- 因此，`SessionStart` 在 `opencode` 路徑下必須被視為 subagent cleanup 事件，和 `SessionEnd` 一樣需要有 module-level 驗證

### 5.1.1 Subagent pairing 合約

`task` tool 的 `callID -> agent_id` 是本次 OpenCode subagent lifecycle 的唯一配對鍵，不能只測 happy path。

本次規格要求至少覆蓋以下負向案例：

- `SubagentStop` 的 `agent_id` 不存在於當前 active set
- `SubagentStop` 先於 `SubagentStart` 到達
- 重複 `SubagentStart` 使用相同 `agent_id`
- 缺少 `agent_id` 的 `SubagentStart` / `SubagentStop`

對應語意：

- malformed 或缺配事件不得讓 UI 看起來像成功更新了 subagent 狀態
- malformed event 可以被忽略，但必須有測試保證不會污染 projection / in-memory state

### 5.2 Process detection 對齊要求

spec 第 1 點的「辨識 `opencode` process」不是只有 provider 存在即可，還必須接進現有 `prober`。

本次規格要求：

- 在 agent module 初始化時註冊 `m.prober.RegisterIdentifier(opencodeProvider.Type(), opencodeProvider.Identify)`
- `RegisterReadiness` 本階段不是必要條件；若沒有可靠的 OpenCode readiness 訊號，可以先不做

也就是說，本階段要完成的是 **liveness/process identification**，不是完整 readiness integration。

### 6. Hook 狀態檢查與 install/remove 語義

`CheckHooks()`：

- 檢查 plugin 檔是否存在
- 檢查檔案是否帶有 `pdx-managed:opencode-hooks:v1` marker
- 若是 managed file，回報所有 `opencodeHookEvents` 為 installed=true
- 若檔案不存在，回報未安裝
- 若同名檔存在但不是 managed file，回報 issue `plugin file exists but is unmanaged`

`InstallHooks()`：

- 解析使用者 home directory
- 確保 `~/.config/opencode/plugins/` 存在
- 若同名 unmanaged file 已存在，拒絕覆寫
- 以 atomic write 產生 managed plugin

`RemoveHooks()`：

- 若 managed file 存在則刪除
- 若不存在則視為成功
- 若是 unmanaged file，拒絕移除

### 6.1 `host > hooks` 對齊要求

`opencode` hook installer 的控制語意需與既有 UI 完全對齊：

| 面向 | 對齊要求 |
|------|----------|
| 作用域 | host-global |
| 前端輸入 | 只需要 `hostId`，不需要 repo path |
| 狀態檢查 | 單一全域狀態，不因不同 repo 改變 |
| Install | 對 host 上所有 OpenCode session 生效 |
| Remove | 對 host 上所有 OpenCode session 失效 |

因此本次不做 project-local plugin 安裝。若未來需要 repo-scoped hooks，應另外設計新的 UI / API 與使用者心智模型。

### 7. UI 與 CLI 整合

需要同步擴充：

- `internal/module/agent/module.go`：註冊 `opencode` provider
- `internal/module/agent/handler.go`：`/api/agents/detect` 增加 `opencode --version`
- `cmd/pdx/setup.go`：支援 `--agent opencode`
- `spa/src/lib/hook-modules.ts`：新增 `opencode` hook card
- `spa/src/lib/agent-metadata.ts`：新增 `opencode: 'OpenCode'`
- `spa/src/locales/en.json` / `zh-TW.json`：新增 hooks 文案

## 驗證策略

### Go

- `internal/agent/opencode/status_test.go`
- `internal/agent/opencode/hooks_test.go`
- `internal/agent/opencode/plugin_template_test.go` 或等位測試：驗證 plugin event mapping、subagent pairing 與 error-after-idle suppress
- `cmd/pdx/setup_test.go` 補 `opencode` case
- `internal/module/agent` 補 module-level 測試：驗證 `SubagentStart` / `SubagentStop` / `SessionEnd` 在 `opencode` 路徑下的 projection 與 error guard 行為

### 前端

- 以型別 / build 驗證 `hook-modules`、locales、agent metadata 變更可編譯

## 風險

1. `opencode` event 順序可能與文件或本機觀察不同，plugin 需要做最小量 state 去重與 suppress。
2. host-global plugin 代表使用者若在同一 host 上跑多個 repo，都會共用同一份 hook plugin；這和 `host > hooks` 的現有心智模型一致，但不是 repo-isolated 行為。
3. `opencode` plugin API 是設定驅動而非固定 hook schema，未來版本若改 event 名稱，需要更新 plugin mapping。
