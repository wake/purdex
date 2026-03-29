# CC Hook Event Reference

> Claude Code hooks 事件與 tbox 狀態映射的完整參考。
> 來源：[CC Hooks 文件](https://code.claude.com/docs/en/hooks)、[anthropics/claude-code#32634](https://github.com/anthropics/claude-code/issues/32634)

---

## tbox 註冊的 Hook 事件

tbox 透過 `tbox setup` 在 `~/.claude/settings.json` 註冊以下 hook 事件：

| 事件 | 觸發時機 |
|------|---------|
| `SessionStart` | CC session 開始（startup / resume / clear / compact） |
| `UserPromptSubmit` | 使用者送出 prompt |
| `Stop` | CC 完成回應，回到 idle prompt |
| `StopFailure` | API 錯誤導致中斷（rate_limit / authentication_failed / billing_error 等） |
| `Notification` | CC 發出通知（idle_prompt / permission_prompt / auth_success / elicitation_dialog） |
| `PermissionRequest` | CC 等待工具權限核准 |
| `SessionEnd` | CC session 結束 |

---

## 狀態映射（deriveStatus）

`spa/src/stores/useAgentStore.ts` 的 `deriveStatus(eventName, rawEvent)` 將 hook 事件映射為 tab 狀態：

### 基本映射

| 事件 | 狀態 | Tab 顏色 |
|------|------|---------|
| `SessionStart` | running | 綠色（呼吸動畫） |
| `UserPromptSubmit` | running | 綠色（呼吸動畫） |
| `PermissionRequest` | waiting | 黃色 |
| `Stop` | idle | 灰色 |
| `StopFailure` | idle | 灰色 |
| `SessionEnd` | clear（移除狀態） | 無 |

### 需要子類別判斷的事件

#### Notification（依 `notification_type`）

| `notification_type` | 狀態 | 說明 |
|---------------------|------|------|
| `permission_prompt` | waiting | CC 需要權限核准 |
| `elicitation_dialog` | waiting | MCP server 請求使用者輸入 |
| `idle_prompt` | idle | CC 閒置提醒（見下方說明） |
| `auth_success` | idle | 認證成功 |
| 未知 / 缺少 | null（不變更） | console.warn 記錄 |

#### SessionStart（依 `source`）

| `source` | 狀態 | 說明 |
|----------|------|------|
| `startup` | running | 全新 session |
| `resume` | running | 恢復 session |
| `clear` | running | 使用者執行 /clear |
| `compact` | null（不變更） | 背景 context 壓縮，CC 繼續執行中 |

---

## idle_prompt 設計決策

### 機制

`idle_prompt` 是 CC 內建的閒置通知，使用 **60 秒 timer heuristic**（非狀態機驅動）。

- CC 停止輸出後等待 ~60 秒才觸發
- 無法區分「CC 真的 idle」和「CC 長時間思考/工具執行中」
- 參考：[anthropics/claude-code#32634](https://github.com/anthropics/claude-code/issues/32634)

### 與 Stop 的關係

典型事件順序：
```
UserPromptSubmit → running
  ↓
Stop → idle（立即）→ 觸發通知 + unread dot
  ↓ (~60 秒後)
Notification(idle_prompt) → idle（延遲）→ 不通知、不掛 unread
```

### tbox 的處理策略

| 項目 | 處理 | 原因 |
|------|------|------|
| 狀態 | 設為 `idle` | 語意正確（CC 確實 idle） |
| Desktop 通知 | 不發送 | `Stop` 已發過通知，60 秒後重複通知無意義 |
| Unread dot | 不標記 | 同上，`Stop` 已標記過 |

### auth_success

認證完成後 CC 回到 prompt，會有 `SessionStart` 或 `Stop` 處理狀態轉換。`auth_success` 本身是資訊性通知，處理策略同 `idle_prompt`。

---

## Unread 標記邏輯

`handleHookEvent` 中的 unread 標記規則：

```
unread = (derived === 'waiting') ||
         (derived === 'idle' && event_name !== 'Notification')
```

| 條件 | unread | 說明 |
|------|--------|------|
| waiting（任何來源） | ✓ | 需要使用者注意 |
| idle + Stop/StopFailure | ✓ | 任務完成或出錯 |
| idle + Notification(idle_prompt) | ✗ | Stop 已處理，延遲提醒不重複 |
| idle + Notification(auth_success) | ✗ | 資訊性通知 |

---

## StopFailure 錯誤類型

| `error` 值 | 說明 |
|------------|------|
| `rate_limit` | API 速率限制 |
| `authentication_failed` | 認證失敗 |
| `billing_error` | 帳單問題 |
| `invalid_request` | 無效請求 |
| `server_error` | Anthropic 伺服器錯誤 |
| `max_output_tokens` | 輸出 token 超限 |
| `unknown` | 未知錯誤 |

通知 body 優先序：`error_details` → `error` → fallback "Task stopped unexpectedly"

---

## hooksInstalled Fallback

當 hooks 已安裝但 session 尚無任何事件時，tab 預設顯示 idle 狀態點（灰色）。

- `hooksInstalled` 在 App mount 時從 `/api/agent/hook-status` 取得
- `useHookStatus.runAction`（install/remove）後同步更新
- 不持久化（每次啟動重新查詢）
- SessionEnd 清除 statuses 後的短暫 fallback 是過渡態（session tab 會隨 tmux session 銷毀而消失）
