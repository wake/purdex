# tmux 多 Client 視窗尺寸問題研究

> 研究日期：2026-03-19
> 目的：解決 tmux-box relay attach 造成其他終端畫面被壓縮的問題

---

## 1. 問題描述

tmux 的預設行為：所有 attach 到同一 session 的 client 中，取最小尺寸（tmux 2.9 前）或最近操作者的尺寸（2.9+ 預設 `latest`）。

在 tmux-box 架構中，relay.go 用 `tmux attach-session -t name` 建立連線，這讓 relay 的 PTY 成為一個額外的 client。如果 PTY 尺寸與使用者的真實終端不同，會互相干擾。

### 影響場景

| 場景 | Client 來源 | 尺寸 | 影響 |
|------|------------|------|------|
| 使用者 SSH 直連 | 真實 terminal | 例如 198x42 | 被小 client 拖小 |
| SPA TerminalView（可見） | relay PTY | 跟隨 xterm.js FitAddon | 可能與真實終端不同 |
| SPA TerminalView（`display:none`） | relay PTY | 80x24（預設值） | 壓縮其他 client |
| 手機瀏覽器 | relay PTY | 可能極小（40x20） | 嚴重壓縮 |

---

## 2. tmux 提供的解法

### 2.1 `window-size` 選項（tmux 2.9+）

```tmux
set-option -g window-size latest    # 預設值：最近操作的 client 決定尺寸
set-option -g window-size largest   # 用最大 client，小 client 看到截斷
set-option -g window-size smallest  # 用最小 client（2.9 前的舊預設）
set-option -g window-size manual    # 手動控制，不自動 resize
```

- `latest` 是 tmux 2.9+ 的**預設值**，大多數使用者已在使用
- GitHub `.tmux.conf` 出現次數：`largest` 203、`latest` 123、`smallest` 253

### 2.2 `aggressive-resize on`（最廣泛採用）

```tmux
set-window-option -g aggressive-resize on
```

- GitHub 出現次數：**1,368**（遠超所有 `window-size` 變體加總）
- tmux-sensible（2,135 stars）列為必備設定
- 效果：視窗尺寸只受「正在看這個 window 的 client」約束，不在當前 window 的 client 不影響尺寸
- tmux-sensible 註解：*"super useful when using grouped sessions and multi-monitor setup"*

### 2.3 Session Groups（`new-session -t`）

```bash
# 不是 attach 到同一個 session，而是建一個 linked session
tmux new-session -t "original-session" -s "relay-view-abc123"
```

- GitHub 出現次數：215
- 多個 session 共享同一組 windows，但各 session 有**獨立的 current window**
- 搭配 `aggressive-resize on`：各 client 看不同 window 時完全互不干擾
- 需要清理：斷開時 `tmux kill-session -t relay-view-abc123`

### 2.4 `attach-session -f ignore-size`（tmux 3.1+）

```bash
tmux attach-session -t mysession -f ignore-size
# 或唯讀模式（-r 是 -f read-only,ignore-size 的別名）
tmux attach-session -t mysession -r
```

- GitHub 出現次數：149
- 將特定 client 標記為「不影響視窗尺寸」
- 適合唯讀觀察者

---

## 3. 同類專案的做法

| 專案 | 架構 | 多 Client 處理 |
|------|------|---------------|
| **tmate** | tmux fork | smallest-client 策略，`recalculate_sizes()` 取所有 viewer 的 min |
| **gotty** | 獨立 PTY per 連線 | 不用 tmux attach，每個連線獨立 process |
| **ttyd** | 獨立 PTY per 連線 | `ioctl(TIOCSWINSZ)` 到自己的 PTY，無跨 client 衝突 |
| **wetty** | 獨立 PTY per 連線 | xterm.js WebSocket，每連線獨立 |

gotty/ttyd/wetty 都不用 `tmux attach`，因此沒有跨 client 尺寸問題。tmate 因為是 tmux fork，使用 smallest-client 強制策略。

---

## 4. 採用率排名

| 排名 | 方案 | GitHub 出現次數 | 適用場景 |
|------|------|----------------|---------|
| 1 | `aggressive-resize on` | 1,368 | 多 window、多螢幕 |
| 2 | `window-size latest`（預設） | 不需設定 | 通用 |
| 3 | Session Groups (`new-session -t`) | 215 | 獨立 viewer |
| 4 | `attach -f ignore-size` | 149 | 唯讀觀察者 |

最被推薦的組合：**Session Groups + `aggressive-resize on`**

---

## 5. tmux-box 建議方案

### 方案 A：relay 改用 Session Group（推薦）

relay.go 改用 `new-session -t` 取代 `attach-session`：

```go
// 現在（會影響其他 client 的尺寸）
exec.Command("tmux", "attach-session", "-t", name)

// 改成（獨立 session，共享 window，互不干擾）
relaySession := name + "-relay-" + uniqueID
exec.Command("tmux", "new-session", "-t", name, "-s", relaySession)

// 斷開時清理
defer exec.Command("tmux", "kill-session", "-t", relaySession).Run()
```

- 每個 SPA 連線有自己的 grouped session，可獨立 resize
- 不壓縮使用者真實終端的畫面
- 需要清理機制（disconnect 時 kill-session）

### 方案 B：relay 用 `ignore-size` flag

```go
exec.Command("tmux", "attach-session", "-t", name, "-f", "ignore-size")
```

- 最簡單，一個 flag 搞定
- relay 的 PTY 尺寸完全不影響其他 client
- 限制：relay 看到的可能是被裁切的畫面（因為尺寸不計入）
- 需 tmux 3.1+

### 方案 C：daemon 啟動時設定 `aggressive-resize`

```go
exec.Command("tmux", "set-option", "-g", "aggressive-resize", "on").Run()
```

- 最小改動
- 缺點：改了使用者的 tmux 全域設定
- 只解決不同 window 的情況，同 window 仍會互相影響

### 建議

**優先考慮方案 A（Session Group）**，原因：
1. 完全不影響使用者的 tmux 環境設定
2. 每個 viewer 獨立尺寸，最乾淨
3. 是 tmux 官方設計的正解
4. tmux-sensible 推薦的就是這個模式

如果要快速修正且不介意依賴 tmux 3.1+，**方案 B（`ignore-size`）** 是最少改動的選擇。

---

## 6. 參考資源

- [tmux man page — window-size option](https://man.openbsd.org/tmux.1)
- [tmux-sensible — aggressive-resize](https://github.com/tmux-plugins/tmux-sensible)
- [tmux wiki — Getting Started](https://github.com/tmux/tmux/wiki/Getting-Started)
- [tmate source — resize.c](https://github.com/tmate-io/tmate/blob/master/resize.c)
