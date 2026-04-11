# Daemon 背景模式 + Crash Log + Reconnect 錯誤清除

**Date:** 2026-04-12
**Status:** Draft
**Scope:** Daemon 可靠性與 UX 小改進（三個相關議題合併）

## 背景

目前 `tbox serve` 只能 foreground 啟動，使用者每次都要自己處理 detach（`nohup`、`&`、tmux session 等）。此外最近發生過一次 daemon crash 但沒有任何 log 可追查，前端在 daemon 復活重連後也留著舊的 "Failed to fetch" 錯誤訊息不清。

本 spec 合併處理三個小而相關的改動：

1. 內建 `start/stop/status` 子命令，daemon 可背景啟動
2. Crash log（兩層防護）+ per-host Logs 子頁查看
3. Reconnect 後清除 stale `testResult`

## 議題 1：`tbox start/stop/status` 子命令

### 設計

**新增檔案：** `cmd/tbox/daemon.go`（預估 ~150 行）

**`tbox start [serve flags...]`**

- 讀取 PID file（`<cfg.DataDir>/tbox.pid`）
  - 若檔案存在且 process 仍活著 → 報錯 `already running (pid N)`，exit 1
  - 若檔案存在但 process 已死 → 視為 stale，清掉繼續
- 用 `exec.Command(os.Args[0], append([]string{"serve"}, passthroughFlags...)...)` spawn 子行程
- `SysProcAttr{Setsid: true}` 讓子行程脫離 tty
- 子行程 stdin 導到 `/dev/null`，stdout/stderr 導到 `<cfg.DataDir>/logs/tbox.log`（append 模式，啟動時若 > 10MB 先 rotate，見議題 2）
- 父行程 spawn 後：
  - 寫 PID file
  - 等 500ms
  - 打 `GET /api/health` 確認啟動成功（最多 retry 5 次，每次間隔 200ms）
  - 失敗則印出 log 檔最後 20 行當 hint，exit 1
  - 成功則印 `tbox daemon started (pid N, bind X:Y, log <path>)`，exit 0

**`tbox stop`**

- 讀 PID file → 不存在則印 `not running`，exit 0
- 送 SIGTERM → 最多等 10 秒 polling process 狀態
- 若超時仍活著 → SIGKILL，印警告
- 成功後清掉 PID file

**`tbox status`**

- 讀 PID file + 檢查 process alive
- 若 alive → 打 `/api/health` 確認 HTTP 有回應
- 輸出：

  ```
  Status:  running
  PID:     12345
  Bind:    100.64.0.2:7860
  Uptime:  2h 15m
  Health:  ok
  Log:     ~/.config/tbox/logs/tbox.log
  ```

- 若未運行 → `Status: stopped`，exit code 1（方便 shell script 判斷）

### Foreground 保持不動

`tbox serve` 維持現狀（foreground、stdout 直接印到 terminal），方便開發與 debug。三個新命令只是在 serve 外包一層 process 管理。

### Flag 透傳

`tbox start --port 7861 --bind 0.0.0.0` 要能原樣傳給底層 `tbox serve`。實作上簡單把 `os.Args[2:]` 直接接到 spawn 的 argv 即可。

## 議題 2：Crash Log

### Layer 1：stdout/stderr → log 檔（僅 background 模式）

- `tbox start` spawn 子行程時已把 stdout/stderr 接到 `<cfg.DataDir>/logs/tbox.log`
- Go panic 的 stack trace 預設寫到 stderr，會自然進入此檔
- 簡易 rotation：`tbox start` 開啟 log file 前先檢查大小，> 10MB 就 rename 成 `tbox.log.1`（保留最多 `tbox.log.1` ~ `tbox.log.3`，更舊的刪掉）

### Layer 2：主動 panic recover（serve 全程都在）

在 `runServe` 開頭加 defer：

```go
defer func() {
    if r := recover(); r != nil {
        writeCrashLog(cfg.DataDir, r, debug.Stack())
        panic(r) // re-panic，讓 process 正常退出
    }
}()
```

`writeCrashLog` 寫到 `<cfg.DataDir>/logs/crash-YYYYMMDD-HHMMSS.log`，內容：

```
Time:        2026-04-12T04:11:36+08:00
Version:     1.0.0-alpha.88
Go Runtime:  go1.26.0
Goroutines:  42

Panic: <panic value>

Stack:
<debug.Stack() output>

All Goroutines:
<runtime.Stack(buf, true) output>
```

### Layer 3：HTTP handler recover middleware

新增 `internal/middleware/recover.go`，包在 handler chain 最外層。捕捉單一 request 的 panic，印 log 但不打掉整個 daemon。格式類似 crash log，但檔名帶 request 資訊。

這層同時算 bug prevention，避免一個壞 handler 讓整個 daemon 掛掉。

### API：`GET /api/crash-logs`

**新增 module：** `internal/module/logs/`（或掛在既有 `dev` module 下）

- `GET /api/crash-logs` → `[{ name, size, mtime }]`（list `<cfg.DataDir>/logs/crash-*.log`）
- `GET /api/crash-logs/:name` → 檔案內容（純文字）
- `GET /api/daemon-log?tail=200` → 回 `tbox.log` 最後 N 行（預設 200，上限 2000）
- `DELETE /api/crash-logs/:name` → 刪除指定 crash log（讓使用者清理）

檔名要驗證避免 path traversal（只允許 `crash-\d{8}-\d{6}\.log` 與 `tbox\.log(\.\d)?`）。

## 議題 3：Reconnect 後錯誤訊息沒清

### 根因

`spa/src/components/hosts/OverviewSection.tsx:27, 183-189`：`testResult` 是 local state，只有 `handleTestConnection` 會寫。使用者點 Test Connection 失敗後留下 `{ ok: false, error: 'Failed to fetch' }`，之後 runtime 自動 reconnect 成功也不會清。

### 修法

`OverviewSection.tsx` 加一個 `useEffect`：

```tsx
useEffect(() => {
  if (runtime?.status === 'connected') {
    setTestResult(null)
  }
}, [runtime?.status])
```

成功的 `testResult` 也會一併清掉（它是「此刻測試回饋」的快照，runtime 恢復後就沒意義了）。

### 測試

`spa/src/components/hosts/OverviewSection.test.tsx` 新增 case：

- Mount 時 `runtime.status = 'reconnecting'`，手動設 `testResult` 為失敗態
- 改 `runtime.status = 'connected'`
- 斷言 `testResult` 相關 UI 消失

## 議題 2 UI：Per-Host Logs 子頁

### 位置

在 `Hosts → <host> → <sub page>` 側欄新增 **Logs** 項目，與 Overview / Sessions / Hooks / Agents / Uploads 同層。程式位置：`spa/src/components/hosts/LogsSection.tsx`。

### 內容

兩個區塊：

**Daemon Log**

- 顯示 `/api/daemon-log?tail=200` 的內容（monospace、pre-wrap、深色背景）
- 右上角 refresh 按鈕 + 自動每 5 秒 refresh（可關）
- "Load more" 按鈕（tail 參數加倍，上限 2000）

**Crash Logs**

- 列出 `/api/crash-logs` 的結果（filename / size / mtime）
- 點擊展開 inline 顯示檔案內容（或 modal）
- 每筆有刪除按鈕

空狀態（沒有 crash log）顯示友善訊息 "No crashes recorded"。

### 路由

`HostSidebar.tsx` 新增 Logs 項目，路由 key 沿用既有 sub page 機制（`overview` / `sessions` / `hooks` / `agents` / `uploads` / `logs`）。

## 檔案變動彙整

### 新增

- `cmd/tbox/daemon.go` — start/stop/status 子命令
- `internal/middleware/recover.go` — HTTP panic recover
- `internal/module/logs/` — crash log / daemon log API module（或併入 `dev` module）
- `spa/src/components/hosts/LogsSection.tsx` — per-host Logs 子頁
- `spa/src/components/hosts/LogsSection.test.tsx`

### 修改

- `cmd/tbox/main.go` — 註冊 `start` / `stop` / `status` command，`runServe` 加 panic recover defer
- `spa/src/components/hosts/OverviewSection.tsx` — 加 reconnect 清 `testResult` 的 effect
- `spa/src/components/hosts/OverviewSection.test.tsx` — 新增測試
- `spa/src/components/hosts/HostSidebar.tsx` — 加 Logs 選項
- 路由層（看實際 host sub page routing 機制）— 註冊 `logs` route
- `spa/src/locales/en.json` / `zh-TW.json` — 新增 `hosts.logs*` key

## 資料路徑

- PID file: `<cfg.DataDir>/tbox.pid`
- Daemon log: `<cfg.DataDir>/logs/tbox.log`（rotated: `.1` / `.2` / `.3`）
- Crash logs: `<cfg.DataDir>/logs/crash-YYYYMMDD-HHMMSS.log`

`cfg.DataDir` 預設為 `~/.config/tbox/`，第一次 `start` 時若 `logs/` 目錄不存在則建立。

## Testing Strategy

### Go

- `cmd/tbox/daemon_test.go`：start/stop/status 的 happy path + stale PID file + already running + stop 無 PID file
- `internal/middleware/recover_test.go`：handler panic 時 response 是 500 且 daemon 不死
- `internal/module/logs` unit test：path traversal 防禦、檔案列表正確

### React

- `OverviewSection.test.tsx`：reconnect 清 testResult
- `LogsSection.test.tsx`：list + expand + delete + empty state

### 手動驗證

- `tbox start` → `tbox status` → `curl /api/health` → `tbox stop`
- 在 serve 執行中用 SIGSEGV 或故意 panic 一個 handler 驗證 crash log 產生
- Electron 端開 Logs 子頁能看到 daemon log 與 crash log

## Out of Scope

- **launchd / systemd 整合**：可在之後獨立 phase，這次先做 manual start/stop
- **Log rotation 精細化**：目前只做 size-based（10MB / 保留 3 份），不做 time-based 或 gzip
- **Crash log 自動上傳**：不做
- **完整 structured logging**：daemon 現有 `log.Printf` 不動，只確保 panic / stderr 能被捕捉
- **改名 Purdex 相關工作**：獨立 spec 處理
