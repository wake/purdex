# tmux 多 Client 視窗尺寸修正設計

> 日期：2026-03-19
> 狀態：設計確認

---

## 問題

### Root Cause

`tmux resize-window` 的所有形式（`-A`、`-x`/`-y`）都有副作用 — 自動將 `window-size` 設為 `manual`（見 man page："This command will automatically set window-size to manual in the window options"）。一旦變成 `manual`，tmux 不再理會任何 client 的尺寸變化。

影響發生在所有呼叫 `resize-window` 的地方：

| 位置 | 呼叫 | 副作用 |
|------|------|--------|
| `server.go` relay OnStart | `ResizeWindowAuto(name)` | 設為 manual |
| `handoff_handler.go` 前置 | `ResizeWindow(target, 80, 24)` | 設為 manual（刻意，由後置清理恢復） |
| `handoff_handler.go` 後置 | `ResizeWindowAuto(target)` | 設為 manual |
| `handoff_handler.go` error paths (×3) | `ResizeWindowAuto(target)` | 設為 manual |

### 症狀

1. 單一瀏覽器 refresh 後，iTerm 的尺寸被鎖死
2. 第二個瀏覽器（較大）連線後，第一個瀏覽器和 iTerm 的畫面溢出
3. 調整瀏覽器視窗大小無法恢復（因為 `window-size` 已被鎖為 `manual`）

### 多 Client 互搶

即使修復 `manual` 副作用，多個 client 在 `window-size latest` 策略下仍會互相影響 — 最近有活動的 client 決定所有 client 的 window 尺寸。

---

## 方案

採用 **方案 C（A + B 組合）**：

1. **Part 1**：修復 `resize-window -A` 副作用（必要 bug fix）
2. **Part 2**：Session Group 可選配置（進階功能）

---

## Part 1：修復 resize-window -A 副作用

### 變更

在 `tmux.Executor` 介面新增方法：

```go
SetWindowOption(target, option, value string) error
```

實作：

```go
func (r *RealExecutor) SetWindowOption(target, option, value string) error {
    return exec.Command("tmux", "set-window-option", "-t", target, option, value).Run()
}
```

### 修復點

引入 helper function 統一處理恢復：

```go
// restoreWindowSizing 在 resize-window 之後恢復 window-size 為 latest，
// 避免 resize-window 的副作用將 window-size 鎖為 manual。
func (s *Server) restoreWindowSizing(target string) {
    s.tmux.ResizeWindowAuto(target)
    s.tmux.SetWindowOption(target, "window-size", "latest")
}
```

所有呼叫 `ResizeWindowAuto` 的地方改用 `restoreWindowSizing`：

| 位置 | 修改 |
|------|------|
| `server.go` relay OnStart | `restoreWindowSizing(name)` |
| `handoff_handler.go` 後置清理（L278） | `restoreWindowSizing(target)` |
| `handoff_handler.go` error path（L243） | `restoreWindowSizing(target)` |
| `handoff_handler.go` error path（L251） | `restoreWindowSizing(target)` |
| `handoff_handler.go` error path（L259） | `restoreWindowSizing(target)` |

**handoff_handler.go 前置 `ResizeWindow(80,24)`（L227）**：不恢復。handoff 期間刻意固定尺寸，由後置清理或 error path 統一恢復。

---

## Part 2：Session Group 可選配置

### 配置

```toml
[terminal]
auto_resize = true        # 已有，預設 true
session_group = false      # 新增，預設 false
```

```go
type TerminalConfig struct {
    AutoResize   *bool `toml:"auto_resize"   json:"auto_resize"`
    SessionGroup *bool `toml:"session_group"  json:"session_group"`
}

func (tc TerminalConfig) IsSessionGroup() bool {
    return tc.SessionGroup != nil && *tc.SessionGroup
}
```

預設 `false`，因為 session group 改變了 tmux 行為（各 session 的 current window 指標獨立，切 window 不再連動），需要使用者主動選擇。

### relay 建立方式

`handleTerminal` 根據配置選擇 attach 方式：

```go
// session_group = false（現行）
tmux attach-session -t {name}

// session_group = true
relaySession := fmt.Sprintf("%s-tbox-%x", name, randomBytes(4))  // 8 hex chars
tmux new-session -d -t {name} -s {relaySession}
tmux attach-session -t {relaySession}
```

- `randomBytes(4)` 產生 4 bytes 隨機數，hex 編碼為 8 字元（4,294,967,296 種組合）
- 用 `-d`（detached）+ `attach` 兩步，因為 `new-session -t` 直接執行會嘗試接管當前終端，在 PTY 環境下需要分開處理
- 如果 `new-session` 因名稱碰撞失敗，重新產生 ID 重試（最多 3 次）

### 清理

**正常斷開**：relay 的 PTY close / WS disconnect 時，kill grouped session：

```go
defer exec.Command("tmux", "kill-session", "-t", relaySession).Run()
```

**異常殘留清理**：daemon 啟動時清理殘留的 grouped sessions。獨立函數 `cleanupStaleRelays()`，不混入 `resetStaleModes()`：

```go
func (s *Server) cleanupStaleRelays() {
    // tmux list-sessions -F '#{session_name}'
    // 匹配 pattern: {name}-tbox-{8 hex chars}（正規表達式驗證 hex 部分）
    // 對每個匹配的 session 執行 kill-session
}
```

使用正規表達式 `^.+-tbox-[0-9a-f]{8}$` 驗證，避免誤殺使用者的 session。

### 與 auto_resize 的交互

Session group 啟用時，每個 grouped session 只有一個 tmux client（relay 自己）。`window-size latest` 會自動將 window 調整為該 client 的尺寸，因此 **`auto_resize`（`resize-window -A`）在 session_group 模式下不需要執行** — 省去 `-A` 的副作用問題。

```go
// session_group = true 時，OnStart 不設定 auto_resize
// session_group = false 時，OnStart 設定 restoreWindowSizing（現行 + Part 1 修復）
```

### 與 handoff 的交互

Handoff 操作的是原始 session（`name`），不是 grouped session。流程：

1. Handoff 開始 → relay 被 shutdown → relay 的 defer 自動 `kill-session` 清理 grouped session
2. Handoff 期間的 `ResizeWindow` / `restoreWindowSizing` 作用在原始 session，不受 session group 影響
3. Handoff 完成 → 新 relay 啟動 → 如果 session_group=true，建立新的 grouped session

---

## 測試計畫

### Part 1 測試

- `restoreWindowSizing` 呼叫 `ResizeWindowAuto` + `SetWindowOption("window-size", "latest")`
- relay OnStart 呼叫 `restoreWindowSizing`
- handoff 後置清理呼叫 `restoreWindowSizing`
- handoff error paths（send-keys 失敗）呼叫 `restoreWindowSizing`
- handoff 前置 `ResizeWindow(80,24)` 不呼叫 `restoreWindowSizing`
- `FakeExecutor` 新增 `SetWindowOption` 記錄

### Part 2 測試

- `IsSessionGroup()` 預設 false、設為 true 時返回 true
- session_group=true 時 relay 建立 grouped session（`new-session -d -t` + `attach`）
- relay 斷開時 grouped session 被 kill
- `cleanupStaleRelays` 清理匹配 `{name}-tbox-{hex}` 的 sessions
- `cleanupStaleRelays` 不誤殺不匹配 pattern 的 sessions
- session_group=true 時 `auto_resize` 不執行 `resize-window -A`
- session_group=false 時行為與現行一致
- `new-session` 名稱碰撞時重試

### 發佈策略

Part 1（bug fix）和 Part 2（新功能）在同一個 PR 中交付，因為兩者共享 `SetWindowOption` 基礎設施且改動範圍緊密相關。
