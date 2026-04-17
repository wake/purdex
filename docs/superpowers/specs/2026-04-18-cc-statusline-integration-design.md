# CC statusLine 結構化狀態整合設計（Backlog）

> 日期：2026-04-18
> 狀態：**設計中 / 暫不實作**
> 關聯文件：[2026-03-29-agent-hook-status-design.md](./2026-03-29-agent-hook-status-design.md) — 已實作的 hook 整合
> 起源：研究「xterm.js 能不能拿到 CC 結構化狀態」時發現 statusLine 是唯一完整資料源

---

## 目標

讓 Purdex 能顯示 CC 的**即時結構化狀態**（context %、cost、rate limit、model display name 等），補足現有 hook 整合（只能拿離散事件）的缺口。

---

## CC 兩條 extension point 對照

Purdex 已整合 hooks（[已有 design](./2026-03-29-agent-hook-status-design.md)）。現在評估的是第二條 extension point — `statusLine`。

| 面向 | `hooks.*` | `statusLine` |
|---|---|---|
| 用途定位 | 生命週期事件攔截 | UI 底部狀態列內容 |
| 觸發時機 | 事件（SessionStart / UserPromptSubmit / PreToolUse / PostToolUse / Stop / Notification / SubagentStart / SubagentStop / ...） | 新 assistant message、permission mode 變更、vim mode 切換；300ms debounce；可選 `refreshInterval` |
| 觸發頻率 | 事件驅動（離散） | 狀態變化（近連續） |
| stdin 格式 | JSON（`hook_event_name` + event-specific） | JSON（session / model / cost / context_window / rate_limits …） |
| stdout 語意 | 看事件：context 注入 / exit 2 blocking / 忽略 | **原樣顯示為 status bar 內容**（含 ANSI / OSC 8） |
| 會不會取代 CC UI | ❌ 不會 | ✅ 會定義底部 status bar（沒設就沒有，設了就是 script 輸出） |

### hooks 能拿到的欄位（貧乏）

所有事件共用：`session_id` / `transcript_path` / `cwd` / `permission_mode` / `hook_event_name`，subagent 情境才有 `agent_id` / `agent_type`。

事件各自 payload：
- `SessionStart`：`source`（startup / resume / clear / compact）、`model`（**只有 id string**，如 `claude-sonnet-4-6`）
- `UserPromptSubmit`：`prompt`
- `PreToolUse` / `PostToolUse`：`tool_name` / `tool_input`（+ `tool_response`） / `tool_use_id`
- `Notification`：`message` / `notification_type`（`permission_prompt` / `idle_prompt` / `auth_success` / `elicitation_dialog`）
- `SubagentStart` / `SubagentStop`：`agent_id` / `agent_type`（+ `last_assistant_message`）
- `Stop`：`stop_reason`
- `PreCompact` / `PostCompact` / `SessionEnd`：只共用欄位

**hooks 拿不到：** cost、context_window、rate_limits、model.display_name、session_name、output_style、vim mode、worktree 資訊。

### statusLine 能拿到的欄位（完整）

```json
{
  "cwd": "/current/working/directory",
  "session_id": "abc123...",
  "session_name": "my-session",
  "transcript_path": "/path/to/transcript.jsonl",
  "model": {
    "id": "claude-opus-4-7",
    "display_name": "Opus"
  },
  "workspace": {
    "current_dir": "...",
    "project_dir": "...",
    "added_dirs": [],
    "git_worktree": "feature-xyz"
  },
  "version": "2.1.90",
  "output_style": { "name": "default" },
  "cost": {
    "total_cost_usd": 0.01234,
    "total_duration_ms": 45000,
    "total_api_duration_ms": 2300,
    "total_lines_added": 156,
    "total_lines_removed": 23
  },
  "context_window": {
    "total_input_tokens": 15234,
    "total_output_tokens": 4521,
    "context_window_size": 200000,
    "used_percentage": 8,
    "remaining_percentage": 92,
    "current_usage": {
      "input_tokens": 8500,
      "output_tokens": 1200,
      "cache_creation_input_tokens": 5000,
      "cache_read_input_tokens": 2000
    }
  },
  "exceeds_200k_tokens": false,
  "rate_limits": {
    "five_hour": { "used_percentage": 23.5, "resets_at": 1738425600 },
    "seven_day": { "used_percentage": 41.2, "resets_at": 1738857600 }
  },
  "vim": { "mode": "NORMAL" },
  "agent": { "name": "security-reviewer" },
  "worktree": {
    "name": "my-feature",
    "path": "/path/to/.claude/worktrees/my-feature",
    "branch": "worktree-my-feature",
    "original_cwd": "/path/to/project",
    "original_branch": "main"
  }
}
```

可能缺的欄位：`session_name`（未 `/rename` 時缺）、`workspace.git_worktree`、`vim`、`agent`、`worktree`、`rate_limits`（非 Pro/Max 或首次 API 呼叫前缺）。

部分欄位可能為 `null`：`context_window.current_usage`（首次 API 呼叫前）、`used_percentage` / `remaining_percentage`（session 早期）。

---

## 策略互補，不是二選一

| 要拿的資料 | 用哪個 | 理由 |
|---|---|---|
| Agent idle / busy / permission prompt / stop 事件 | **hooks** | 已實作，事件驅動最準 |
| 每個 prompt 送出 / tool call | **hooks** | 同上 |
| Subagent 進出 | **hooks** | 同上 |
| Context % / Cost / Rate limit / Model display name / Session name | **statusLine** | hooks 根本沒這些欄位 |

兩條管道聚合到 daemon，daemon 合併後 push 前端。

---

## 傳輸方案比較

目標：把 statusLine script 拿到的 JSON 送到 Purdex daemon，daemon 廣播到前端。

| 方案 | script 做的事 | 優點 | 缺點 |
|---|---|---|---|
| **OSC 包 JSON** | `printf '\x1b]1337;purdex-status=%s\x07' "$json"` → 走 PTY 流 | 零外部連線；session 對應天然（PTY 哪條就是 tab 哪個） | 污染 PTY 流；xterm.js 需解析 escape；跨 tmux server/client 時 OSC 能否透傳？ |
| **HTTP POST daemon** ⭐ | `curl -s -X POST daemon/api/agent/status -d @-` | 結構化、乾淨、daemon 可聚合多路；與既有 hook 整合路徑一致 | 需 env 注入 session key + auth token |
| **Unix socket / file watch** | 寫 `/tmp/pdx-<session>.sock` 或 file | 無網路層 | 只能 local；跨機（Air → Mini daemon）不通 |

**選擇：HTTP POST**，與既有 `tbox hook` 路徑完全一致。

### 現有 hook 整合已鋪的基礎

從 [2026-03-29-agent-hook-status-design.md](./2026-03-29-agent-hook-status-design.md) 的架構：

```
CC → hook → tbox hook <event_name> → POST /api/agent/event → Daemon relay → WS → SPA
```

`tbox`（= `pdx`）binary 已經有 `hook` 子命令、已經會 POST 到 `/api/agent/event`、daemon 已經是純 relay、SPA agent store 已經有處理邏輯。

**新增 statusLine 支援只需要：**

1. 新增 `pdx statusline` 子命令（或 `pdx hook statusline`）— 讀 stdin JSON，POST 到 `/api/agent/status`（或沿用 `/api/agent/event` 用 `event_name: "statusLine"` 區分）
2. Daemon 新增對應 endpoint（或擴展現有的）
3. SPA agent store 接收 statusLine payload，狀態合併進既有 `agent` 資料結構
4. UI 新增顯示（context bar、cost、rate limit 等）

---

## 主要摩擦點

### 1. 取代既有 statusLine 配置衝突

statusLine 不是疊加，是定義。使用者如果已經有自己的 statusLine（或用 ccstatusline / cc-usage-bar 等第三方），我們不能粗暴取代。

**解法選項：**
- (a) 安裝前偵測，已存在則跳過 + 提示使用者手動整合
- (b) 提供 wrapper pattern：Purdex script 先 call 使用者原本的 script 取得原輸出，再把 tee 出 stdin JSON 到 daemon，回印原輸出
- (c) 提供 `pdx install-cc-integration` CLI，互動式選：取代 / wrapper / 跳過

**推薦 (c)**，因為使用者知情且可選。

### 2. Session 對應

statusLine script 是 CC fork 的 subprocess，繼承 CC 的 env。CC 繼承 tmux pane env。

需要在 **Purdex daemon 建 tmux session 時注入 env**：
- `PDX_SESSION_ID`：tmux session name / uuid
- `PDX_DAEMON_URL`：daemon base URL（Mini: `127.0.0.1:7860`；跨機: `100.64.0.2:7860`）
- `PDX_TICKET`：與 WS ticket 相同的 auth scheme

script 啟動時讀這些 env，POST 時帶上。

**已有類似機制：** hook 整合靠 `tmux display-message -p '#{session_name}'` 取得 session name。statusLine 走 env 注入，或也用 tmux CLI（如果 tmux 可用）。

### 3. 跨機情境

- CC 跑在 Mini → daemon 在 Mini → `PDX_DAEMON_URL=127.0.0.1:7860`
- CC 跑在 Air 的 purdex → 但 tmux server 在 Mini，CC 實際跑在 Mini → 同上
- 使用者 ssh 進 Air，在 Air 的 shell 裡跑 CC：statusLine script 也跑在 Air → 要 `PDX_DAEMON_URL=100.64.0.2:7860`（Tailscale IP）
- daemon 建 session 時注入的 URL 必須是「script 那台能連到 daemon 的位址」

### 4. statusLine script stdout 會被顯示

script 的 stdout 被 CC 當 status bar 顯示。所以：
- `curl -s -X POST ... > /dev/null &`（背景、丟棄輸出、fire-and-forget）
- 然後印使用者看的內容到 stdout（或空字串）
- 延遲不可過高：debounce 300ms，若 `curl` 阻塞 status bar 更新

### 5. OSC fallback 的意義

如果使用者**不想**安裝 statusLine hook：
- **OSC 0/2 tab title** 仍是 minimum viable signal（Claude Code / Codex CLI 原生會送）
- 這部分不依賴任何 Purdex 額外配置，現有 PTY 流裡就有

所以 OSC 0/2 （這輪 PR 範圍）不會被 statusLine 整合取代，兩者並存。

---

## 後續步驟（暫不實作）

1. [ ] daemon 新增 `/api/agent/status` endpoint（或擴充 `/api/agent/event`）
2. [ ] `pdx` binary 新增 `statusline` 子命令
3. [ ] daemon 建 tmux session 時注入 `PDX_SESSION_ID` / `PDX_DAEMON_URL` / `PDX_TICKET` 到 pane env
4. [ ] 寫 `pdx install-cc-integration` CLI（互動式安裝 hook + statusLine）
5. [ ] SPA agent store 擴充：接收 statusLine payload 並合併
6. [ ] UI 新增 context bar / cost / rate limit 顯示（位置待定）
7. [ ] 處理既有配置衝突（wrapper pattern）
8. [ ] 跨機 URL 注入策略驗證

---

## 參考資料

- [Customize your status line - Claude Code Docs](https://code.claude.com/docs/en/statusline)
- [Hooks reference - Claude Code Docs](https://code.claude.com/docs/en/hooks)
- [2026-03-29-agent-hook-status-design.md](./2026-03-29-agent-hook-status-design.md) — 已實作的 hook 整合
- [2026-04-14-xtermjs-osc-agent-statusline.md](../../research/2026-04-14-xtermjs-osc-agent-statusline.md) — OSC / xterm.js 研究
