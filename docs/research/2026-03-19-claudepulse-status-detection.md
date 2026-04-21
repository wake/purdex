# ClaudePulse 狀態偵測機制研究

> 研究日期：2026-03-19
> 目的：分析 ClaudePulse 如何偵測 Claude Code 狀態，評估 hooks 機制對 tmux-box 的適用性

---

## 1. 專案概觀

**Repo**: [tzangms/claudepulse](https://github.com/tzangms/claudepulse)
**語言**: Swift 5.10，macOS native app（SwiftUI + AppKit），最低 macOS 14
**內部代號**: `ccani`
**功能**: 類似 iPhone Dynamic Island 的浮動面板，即時顯示所有 Claude Code session 的狀態

---

## 2. 核心架構

```
Claude Code hook 觸發
  → stdin JSON payload
  → curl POST 到 localhost:19280/hook
  → ccani HTTP server 收到事件
  → SessionManager 更新狀態
  → SwiftUI 面板即時反映
```

**關鍵：完全不使用 tmux capture-pane、process tree inspection、或任何終端畫面解析。全部靠 Claude Code 原生 Hooks 機制。**

### 關鍵檔案

| 檔案 | 職責 |
|------|------|
| `Sources/Server/HookServer.swift` | BSD socket HTTP 伺服器，接收 hook 事件 |
| `Sources/Setup/HooksConfigurator.swift` | 自動注入 hooks 到 `~/.claude/settings.json` |
| `Sources/Managers/SessionManager.swift` | Session 狀態管理、staleness timer |
| `Sources/Models/Session.swift` | 單一 session 的狀態機 |
| `Sources/Models/HookEvent.swift` | 事件資料結構、狀態定義 |

---

## 3. 偵測機制詳解

### 3.1 HTTP 伺服器

App 啟動時在 `127.0.0.1:19280-19289` 範圍找可用 port，建立 BSD socket HTTP server。Port 寫入 `~/.ccani/port`。

單一 instance 保護：讀取 port file + 嘗試 TCP connect，比 PID file 更可靠（PID file 在 crash 後會留下 stale file）。

### 3.2 Hook 注入

修改 `~/.claude/settings.json`，為 8 個事件各加一個 hook：

```json
{
  "hooks": {
    "UserPromptSubmit": [{
      "matcher": "",
      "hooks": [{
        "type": "command",
        "command": "curl -sf -m 2 -X POST -H 'Content-Type: application/json' -d \"$(cat)\" http://localhost:$(cat ~/.ccani/port 2>/dev/null || echo 19280)/hook || true",
        "async": true
      }]
    }]
  }
}
```

設計重點：
- `async: true` — 不阻塞 Claude Code
- `matcher: ""` — 空 matcher，匹配所有工具
- `$(cat)` — 從 stdin 讀取 CC 傳入的 JSON payload
- `curl -m 2 ... || true` — 2 秒 timeout，失敗靜默不影響 CC
- 動態讀取 port file，非硬編碼

### 3.3 事件資料結構

```swift
struct HookEvent: Decodable {
    let sessionId: String       // CC session UUID
    let hookEventName: String   // 事件名稱
    let cwd: String?            // 工作目錄
    let toolName: String?       // 工具名稱（PreToolUse/PostToolUse）
    let notificationType: String?
}
```

### 3.4 支援的 8 個 Hook 事件

| Hook 事件 | 觸發時機 |
|-----------|---------|
| `SessionStart` | CC session 開始 |
| `SessionEnd` | CC session 結束 |
| `UserPromptSubmit` | 使用者送出訊息 |
| `PreToolUse` | CC 準備執行工具（Bash、Read、Edit 等） |
| `PostToolUse` | 工具執行完成 |
| `PostToolUseFailure` | 工具執行失敗 |
| `PermissionRequest` | CC 等待使用者授權 |
| `Stop` | CC 停止（回到 idle） |

---

## 4. 狀態機

### 4.1 狀態定義

| 狀態 | 值 | 觸發條件 | UI 顏色 |
|------|-----|---------|---------|
| Idle | `.idle` | `SessionStart`、`Stop`、或 30 秒無事件 | 灰色 |
| Working | `.working` | `UserPromptSubmit`、`PreToolUse`、`PostToolUse`、`PostToolUseFailure` | 紫色 |
| Waiting for User | `.waitingForUser` | `PermissionRequest` | 橘色 |
| Stale | `.stale` | 10 分鐘無事件 | 半透明灰色 |

### 4.2 狀態轉換邏輯

```swift
// Session.swift:handleEvent
switch event.hookEventName {
case "SessionStart":       state = .idle
case "UserPromptSubmit":   state = .working
case "PreToolUse", "PostToolUse", "PostToolUseFailure":
                           state = .working
case "PermissionRequest":  state = .waitingForUser
case "Stop":               state = .idle
default:                   break
}
```

`SessionEnd` 事件直接從 `sessions` dict 中移除 session。

### 4.3 Staleness Timer

每 10 秒檢查一次（唯一的 timer，效能極低）：
- 30 秒無事件 → working 降級為 idle
- 10 分鐘無事件 → 標記為 stale
- 30 分鐘無事件 → 移除 session

---

## 5. 偵測能力與盲區

### 5.1 精確偵測

| 情境 | 偵測結果 | 說明 |
|------|---------|------|
| CC 等待工具權限 | waitingForUser | `PermissionRequest` 直接映射 |
| CC 執行工具中 | working | `PreToolUse`/`PostToolUse` |
| CC 收到使用者訊息 | working | `UserPromptSubmit` |
| CC 閒置在 `❯` prompt | idle | `Stop` 事件 |
| CC session 結束 | 移除 | `SessionEnd` |
| 多個 CC session | 完整支援 | 以 `session_id` 獨立追蹤 |

### 5.2 盲區

| 情境 | 問題 | 影響 |
|------|------|------|
| CC 思考中 vs 串流中 | 無法區分，都是 working | 無法提供更細的狀態 |
| CC 顯示 `/status` | 無法偵測 | `/status` 不觸發 hook |
| CC 被意外終止（kill -9） | 可能漏偵 | 沒有 `Stop`/`SessionEnd` event |
| 長時間 bash 命令（>30s） | **誤報 idle** | 30s 無事件就降級 |
| pane 跑的不是 CC | 完全不知道 | 只靠 CC 發事件 |
| CC 顯示 AskUserQuestion | 未處理 | 沒有對應 hook（只有 `PermissionRequest`） |

---

## 6. 與 tmux-box 偵測機制的比較

tmux-box 目前使用 `tmux capture-pane` + regex + `pane_current_command` 偵測狀態（`internal/detect/detector.go`）。

| 面向 | tmux-box（capture-pane） | ClaudePulse（hooks） |
|------|------------------------|---------------------|
| **精確度** | 依賴 UI 文字解析，容易被格式變化打破 | 事件精確，不受 UI 變化影響 |
| **延遲** | 輪詢間隔（預設 2s） | 即時（事件驅動） |
| **效能** | 每次輪詢執行 shell 命令（tmux capture-pane + ps） | 零輪詢，純 HTTP 接收 |
| **CC 版本耦合** | 高（依賴 `❯` prompt、`Allow/Deny` 文字、status bar 格式） | 低（hooks API 穩定） |
| **非 CC 偵測** | 能偵測（pane 跑的不是 claude） | 無法（只靠 CC 發事件） |
| **需額外設定** | 否 | 是（需注入 hooks 到 settings.json） |
| **Session ID 取得** | 需 `/status` 命令 + capture-pane 解析 | 每個事件都帶 `session_id` |
| **Cwd 取得** | 需 `/status` 命令解析 | 每個事件都帶 `cwd` |
| **工具名稱** | 無法取得 | `PreToolUse`/`PostToolUse` 帶 `tool_name` |

---

## 7. 對 tmux-box 的建議

### 7.1 最佳策略：Hooks 為主 + capture-pane 為輔

| 用途 | 方式 | 原因 |
|------|------|------|
| 即時狀態追蹤 | CC hooks → daemon HTTP endpoint | 精確、即時、低開銷 |
| Session ID / Cwd | hooks 事件中的欄位 | 不再需要 `/status` + capture-pane |
| CC 是否真的還在跑 | `pane_current_command` | hooks 無法偵測 CC 被 kill |
| pane 跑的不是 CC | `pane_current_command` | hooks 只有 CC 才會發事件 |
| Handoff 流程 | 仍需 capture-pane | `/status` 的 session ID 擷取（但可被 hooks 取代） |

### 7.2 如果採用 hooks，handoff 可大幅簡化

目前 handoff 流程：
```
1. detect CC running (capture-pane)
2. interrupt to idle (send-keys Ctrl+C)
3. send /status (send-keys)
4. capture-pane × 6 次解析 session ID
5. send Escape + /exit
6. launch relay
```

採用 hooks 後，daemon 已經知道 `session_id` 和 `cwd`（每次事件都帶），handoff 可簡化為：
```
1. 從 hooks 狀態確認 CC running + session_id 已知
2. interrupt to idle (send-keys Ctrl+C)
3. send /exit (不需 /status)
4. launch relay with --resume session_id
```

**省掉 Step 3-4（/status + 6 次 capture-pane 解析），這正是 Bug 1 的問題來源。**

### 7.3 Hook 注入方式

tmux-box daemon 可以：
1. 提供 `tbox hooks install` CLI 命令，修改 `~/.claude/settings.json`
2. 或在 SPA Settings 面板中提供一鍵安裝
3. Hook command 改為 POST 到 daemon 的 HTTP endpoint（而非另起 HTTP server）

```bash
curl -sf -m 2 -X POST -H 'Content-Type: application/json' \
  -d "$(cat)" http://127.0.0.1:7860/api/hooks/cc-event || true
```

### 7.4 注意事項

- hooks 需要 CC 在啟動時載入 `settings.json`，如果 CC 先於 daemon 啟動，需重啟 CC
- `async: true` 確保不影響 CC 效能
- 30 秒 idle 降級對長時間 bash 命令過於激進，建議搭配 `pane_current_command` 確認
- `SessionEnd` 可能在 CC 被 kill -9 時漏發，需要 staleness timer 作為 fallback

---

## 8. Hook 安裝/解除安裝機制深入分析

### 8.1 安裝流程（HooksConfigurator.swift）

#### 觸發時機

App 啟動 → HTTP server 綁定成功後 → `DispatchQueue.main.async` 中：
1. 呼叫 `needsSetup()` 檢查是否已安裝
2. 若未安裝 → 顯示 NSAlert 彈窗（「Configure Claude Code Hooks?」）
3. 使用者點 Configure → 執行 `install(port:)`
4. 使用者點 Skip → 不安裝（下次啟動仍會再問）

#### 偵測是否已安裝（needsSetup）

遍歷 `settings.json` 中所有 hook event 的所有 entry，檢查兩個條件（任一符合即判定已安裝）：
- `url` 欄位包含 `"1928"`（port 範圍 19280-19289）
- `command` 欄位包含 `"ccani"`（curl 命令中有 `~/.ccani/port` 路徑）

```swift
for hook in hookList {
    if let url = hook["url"] as? String, url.contains("1928") { return false }
    if let cmd = hook["command"] as? String, cmd.contains("ccani") { return false }
}
```

注意：實際寫入的是 `command` 型別，`url` 檢查是早期版本殘留（用 http 型別時的判斷），但不影響功能。

#### 安裝實作

```swift
private func install(port: UInt16) throws {
    // 1. 讀取現有 settings.json（不存在則用空字典）
    var json: [String: Any] = [:]
    if FileManager.default.fileExists(atPath: settingsPath.path) {
        let data = try Data(contentsOf: settingsPath)
        guard let existing = try JSONSerialization.jsonObject(with: data) as? [String: Any] else {
            throw ConfigError.malformedSettings
        }
        json = existing
    }

    // 2. 取出或建立 hooks 區塊
    var hooks = json["hooks"] as? [String: Any] ?? [:]

    // 3. 為 8 個事件各 append 一個 entry
    let curlCmd = "curl -sf -m 2 -X POST -H 'Content-Type: application/json' "
        + "-d \"$(cat)\" http://localhost:$(cat ~/.ccani/port 2>/dev/null "
        + "|| echo \(port))/hook || true"

    for event in allEvents {
        let entry: [String: Any] = [
            "matcher": "",
            "hooks": [["type": "command", "command": curlCmd, "async": true]]
        ]
        var existing = hooks[event] as? [[String: Any]] ?? []
        existing.append(entry)  // 只 append，不覆蓋
        hooks[event] = existing
    }

    // 4. 寫回
    json["hooks"] = hooks
    let data = try JSONSerialization.data(withJSONObject: json,
                                          options: [.prettyPrinted, .sortedKeys])
    try data.write(to: settingsPath, options: .atomic)
}
```

#### 合併策略

**只做 append，不做去重或覆蓋。** 其他工具或手動設定的 hooks 完全保留。

但這也意味著**每次安裝都會重複追加**。靠 `needsSetup()` 的字串匹配防止重複，但如果匹配失敗就會產生重複 entry。

#### 寫入的完整 JSON 結構

```json
{
  "hooks": {
    "SessionStart": [
      {
        "matcher": "",
        "hooks": [{
          "type": "command",
          "command": "curl -sf -m 2 -X POST -H 'Content-Type: application/json' -d \"$(cat)\" http://localhost:$(cat ~/.ccani/port 2>/dev/null || echo 19280)/hook || true",
          "async": true
        }]
      }
    ],
    "SessionEnd": [ /* 同上 */ ],
    "UserPromptSubmit": [ /* 同上 */ ],
    "PreToolUse": [ /* 同上 */ ],
    "PostToolUse": [ /* 同上 */ ],
    "PostToolUseFailure": [ /* 同上 */ ],
    "PermissionRequest": [ /* 同上 */ ],
    "Stop": [ /* 同上 */ ]
  }
}
```

### 8.2 解除安裝 — 完全不存在

**ccani 沒有實作任何 uninstall/removal 機制。**

- 沒有 `uninstall()` 或 `remove()` 方法
- App 退出時只刪除 `~/.ccani/port`，不清理 `settings.json`
- Menu 只有 Show/Hide、Position、Quit — 沒有「Remove Hooks」
- **Hooks 一旦寫入就永久留在 settings.json**，即使 ccani 被刪除

使用者必須手動編輯 `~/.claude/settings.json` 移除。

### 8.3 缺陷與風險

| 項目 | 問題 |
|------|------|
| **無 uninstall** | hooks 永久殘留，curl 命令持續嘗試連線（雖然 `\|\| true` 不影響 CC） |
| **無備份** | 直接覆寫 settings.json，`.atomic` 防止部分寫入但不防邏輯錯誤 |
| **無 file lock** | 如果 CC 或其他工具同時寫入 settings.json，可能互相覆蓋 |
| **重複追加** | `needsSetup()` 的字串匹配如果漏判，每次啟動都追加一組 hooks |
| **JSON 非型別化** | 用 `[String: Any]` 操作，容易在型別轉換時丟失資料 |
| **不支援 per-project settings** | 只改全域 `~/.claude/settings.json`，不處理 project-level `.claude/settings.json` |

### 8.4 tmux-box 如果實作 hooks，應改善的地方

1. **標記自己的 hooks** — 在 entry 中加入識別欄位（如 `"_source": "tbox"`），方便精確移除
2. **提供 uninstall** — `tbox hooks uninstall` 只移除帶 `_source: tbox` 標記的 entry
3. **備份 settings.json** — 修改前先備份到 `settings.json.bak`
4. **用 `http` 型別** — CC 原生支援 `"type": "http"`，不需要繞道 curl：
   ```json
   {
     "type": "http",
     "url": "http://127.0.0.1:7860/api/hooks/cc-event",
     "timeout": 2
   }
   ```
5. **去重邏輯** — 安裝前檢查是否已有相同 URL 的 entry，避免重複
6. **file lock** — 用 `flock` 或 advisory lock 防止並發寫入

### 8.5 Claude Code Hooks 完整事件列表

ccani 只註冊了 8 個事件，但 CC hooks 實際支援更多：

| 類別 | 事件 | ccani 有用？ |
|------|------|-------------|
| **Lifecycle** | `SessionStart`, `InstructionsLoaded`, `UserPromptSubmit`, `SessionEnd` | 3/4 有 |
| **Tool** | `PreToolUse`, `PermissionRequest`, `PostToolUse`, `PostToolUseFailure` | 4/4 有 |
| **Agent** | `SubagentStart`, `SubagentStop`, `Stop`, `StopFailure`, `TeammateIdle`, `TaskCompleted` | 1/6 有 |
| **Config** | `ConfigChange`, `PreCompact`, `PostCompact` | 0/3 |
| **Notification** | `Notification`, `Elicitation`, `ElicitationResult` | 0/3 |
| **Worktree** | `WorktreeCreate`, `WorktreeRemove` | 0/2 |

tmux-box 可額外利用的事件：
- `SubagentStart`/`SubagentStop` — 偵測子 agent 活動
- `Notification` — CC 完成通知轉發到 SPA
- `TaskCompleted` — 任務完成狀態
- `InstructionsLoaded` — 確認 CC 成功載入 CLAUDE.md

---

## 9. 參考資源

- [Claude Code Hooks 文件](https://docs.anthropic.com/en/docs/claude-code/hooks)
- [ClaudePulse README](https://github.com/tzangms/claudepulse)
- [ClaudePulse 原始碼 — HooksConfigurator.swift](https://github.com/tzangms/claudepulse/blob/main/Sources/Setup/HooksConfigurator.swift)
- [Claude Code settings.json 格式](https://docs.anthropic.com/en/docs/claude-code/settings)
