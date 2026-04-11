# Daemon 背景模式 + Crash Log + Reconnect 錯誤清除

**Date:** 2026-04-12
**Status:** Draft
**Scope:** Daemon 可靠性與 UX 小改進（四個相關議題合併）

## 背景

目前 `tbox serve` 只能 foreground 啟動，使用者每次都要自己處理 detach（`nohup`、`&`、tmux session 等）。此外最近發生過一次 daemon crash 但沒有任何 log 可追查，前端在 daemon 復活重連後也留著舊的 "Failed to fetch" 錯誤訊息不清。

本 spec 合併處理四個小而相關的改動：

1. 內建 `start/stop/status` 子命令，daemon 可背景啟動
2. Crash log（兩層防護）+ per-host Logs 子頁查看
3. Reconnect 後清除 stale `testResult`
4. Agent state transition trace（daemon + frontend 雙側，解決 SubagentDots 類型的 silent miss 難以追查）

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

## 議題 4：Agent State Transition Trace

### 問題背景

PR #283（`fix/subagent-dots-stuck`）修了 5 個 root cause，散落在 daemon 與 frontend 兩側。這類「燈號卡住」問題的共同特徵是 **silent miss**：某個 event 該觸發更新但沒觸發（omitempty 省略、guard 誤擋、session rename 查錯 key、late event 在 clear 後重新污染）。只靠「最終狀態」debug 非常困難，需要完整的狀態轉換流水帳才能定位。

### 雙側 trace

由於狀態轉換橫跨 daemon 與 frontend，純 daemon log 無法覆蓋 frontend-only 的 race。兩側各有一條 trace，都收進 per-host Logs 子頁。

### Daemon 側：`<cfg.DataDir>/logs/agent-state.log`

NDJSON 格式，每個 mutation 一行。欄位：

| 欄位 | 說明 |
|------|------|
| `ts` | RFC3339Nano 時間戳 |
| `seq` | 從 `agent_events` DB 取的遞增序號（跨源對齊用） |
| `session_code` | session code；若 `nameToCode` 對應失敗則為 `null` 並在 `tmux_session` 記 raw name |
| `tmux_session` | tmux session 名稱 |
| `source` | `hook` / `api` / `replay` / `check_alive` / `rename` |
| `event` | 原始 hook event type（`SessionStart` / `SubagentStart` / `SubagentStop` / `StatusClear` / …） |
| `trigger` | 人類可讀描述，例如 `"hook SubagentStop name=researcher"` |
| `pre` | `{ status, subagents: [...], valid }` — mutation 前的快照 |
| `post` | `{ status, subagents: [...], valid }` — mutation 後的快照 |
| `diff` | 計算過的 delta（`added` / `removed` / `status_changed`），方便視覺 scan |
| `broadcast` | `{ sent: bool, clients: N, payload: <normalized event> }` |
| `guard` | 若 mutation 被 guard 擋下（如 Bug 3 的 StatusClear guard），記擋住的原因；否則 `null` |
| `goroutine` | goroutine id（並發定位） |

**關鍵：被 guard 跳過的 case 也必須記錄**。Silent miss 最容易漏的就是這個點 —「沒做任何事」本身也是一個 trace event。

**寫入點收斂**：`internal/module/agent/handler.go` 每個 mutation path 統一呼叫 `m.logStateTransition(before, after, source, event, broadcast, guard)` helper，在 `m.mu` 內執行，確保 pre/post 一致性。

**Rotation**：單檔 5MB，保留 5 份（比 `tbox.log` 嚴，避免 log spam）。

### Frontend 側：in-memory ring buffer

`spa/src/stores/useAgentStore.ts` 每個 mutation 寫入一個固定大小 ring buffer（500 筆），欄位對應 daemon 側但 source 分：

- `ws-event`（收到 daemon 廣播的 normalized event）
- `session-closed`（前端偵測 session 關閉觸發 clearSession）
- `clear`（主動清除）
- `replay-on-reconnect`（WS reconnect 後收到 replay event）

Ring buffer 不持久化（daemon 重啟會 reset，但它只是 debug 工具不需要）。

透過 per-host Logs 子頁提供 **"Export Frontend Trace"** 按鈕，dump 成 JSON 檔下載，方便貼回 GitHub issue。

### API

- `GET /api/agent-state-log?tail=N&session=CODE&event=TYPE&guard_only=1` — 讀 daemon 側 trace，支援 filter：
  - `tail`：最後 N 筆（預設 200，上限 2000）
  - `session`：filter session code
  - `event`：filter event type
  - `guard_only`：只看被 guard 擋下的

Path traversal 防護同議題 2。

### Log Sub 頁呈現

議題 2 的 Logs 子頁擴充成四個 block（見下節更新）。

## 議題 2 + 4 UI：Per-Host Logs 子頁

### 位置

在 `Hosts → <host> → <sub page>` 側欄新增 **Logs** 項目，與 Overview / Sessions / Hooks / Agents / Uploads 同層。程式位置：`spa/src/components/hosts/LogsSection.tsx`。

### 內容

四個區塊：

**1. Daemon Log**

- 顯示 `/api/daemon-log?tail=200` 的內容（monospace、pre-wrap、深色背景）
- 右上角 refresh 按鈕 + 自動每 5 秒 refresh（可關）
- "Load more" 按鈕（tail 參數加倍，上限 2000）

**2. Crash Logs**

- 列出 `/api/crash-logs` 的結果（filename / size / mtime）
- 點擊展開 inline 顯示檔案內容（或 modal）
- 每筆有刪除按鈕
- 空狀態顯示 "No crashes recorded"

**3. Agent State Trace（daemon 側）**

- 讀 `/api/agent-state-log?tail=200`
- 表格顯示：`ts` / `seq` / `session_code` / `source` / `event` / `trigger` / `diff`（人類可讀的 summary）
- 點一列展開顯示完整 `pre` / `post` / `broadcast` / `guard`
- Filter 控制：session code 下拉、event type 下拉、`guard_only` toggle
- 被 guard 擋下的行用特別顏色標記（例如黃色 outline），一眼看得出 silent miss

**4. Frontend Trace**

- "Export Frontend Trace" 按鈕 → dump ring buffer 為 JSON 檔下載
- 下方顯示最近 50 筆（精簡欄位：`ts` / `source` / `event` / `session_code`），快速預覽
- 不用打 daemon，純 frontend debug 工具

### 路由

`HostSidebar.tsx` 新增 Logs 項目，路由 key 沿用既有 sub page 機制（`overview` / `sessions` / `hooks` / `agents` / `uploads` / `logs`）。

四個區塊用 collapsible section，預設展開 Daemon Log、其餘收合。

## 檔案變動彙整

### 新增

- `cmd/tbox/daemon.go` — start/stop/status 子命令
- `internal/middleware/recover.go` — HTTP panic recover
- `internal/module/logs/` — crash log / daemon log / agent state log API module（或併入 `dev` module）
- `internal/module/agent/state_trace.go` — `logStateTransition` helper + rotation
- `spa/src/components/hosts/LogsSection.tsx` — per-host Logs 子頁（四個 block）
- `spa/src/components/hosts/LogsSection.test.tsx`
- `spa/src/lib/agent-state-trace.ts` — frontend ring buffer + export helper

### 修改

- `cmd/tbox/main.go` — 註冊 `start` / `stop` / `status` command，`runServe` 加 panic recover defer
- `internal/module/agent/handler.go` — 每個 mutation path 呼叫 `logStateTransition`（含 guard skip 的 case）
- `internal/module/agent/module.go` — `checkAliveAll` 的 orphan 清理也要記 trace
- `spa/src/stores/useAgentStore.ts` — 每個 state mutation 寫入 ring buffer
- `spa/src/components/hosts/OverviewSection.tsx` — 加 reconnect 清 `testResult` 的 effect
- `spa/src/components/hosts/OverviewSection.test.tsx` — 新增測試
- `spa/src/components/hosts/HostSidebar.tsx` — 加 Logs 選項
- 路由層（看實際 host sub page routing 機制）— 註冊 `logs` route
- `spa/src/locales/en.json` / `zh-TW.json` — 新增 `hosts.logs*` key

## 資料路徑

- PID file: `<cfg.DataDir>/tbox.pid`
- Daemon log: `<cfg.DataDir>/logs/tbox.log`（rotated: `.1` / `.2` / `.3`，單檔上限 10MB）
- Crash logs: `<cfg.DataDir>/logs/crash-YYYYMMDD-HHMMSS.log`
- Agent state trace: `<cfg.DataDir>/logs/agent-state.log`（rotated: `.1` ~ `.5`，單檔上限 5MB）

`cfg.DataDir` 預設為 `~/.config/tbox/`，第一次 `start` 時若 `logs/` 目錄不存在則建立。

## Testing Strategy

### Go

- `cmd/tbox/daemon_test.go`：start/stop/status 的 happy path + stale PID file + already running + stop 無 PID file
- `internal/middleware/recover_test.go`：handler panic 時 response 是 500 且 daemon 不死
- `internal/module/logs` unit test：path traversal 防禦、檔案列表正確
- `internal/module/agent/state_trace_test.go`：每個 mutation path 都有 trace、guard skip 也有 trace、pre/post 正確快照、rotation

### React

- `OverviewSection.test.tsx`：reconnect 清 testResult
- `LogsSection.test.tsx`：list + expand + delete + empty state + filter
- `useAgentStore.test.ts`：ring buffer 寫入、overflow 後 evict 最舊、export 格式

### 手動驗證

- `tbox start` → `tbox status` → `curl /api/health` → `tbox stop`
- 在 serve 執行中用 SIGSEGV 或故意 panic 一個 handler 驗證 crash log 產生
- Electron 端開 Logs 子頁能看到 daemon log 與 crash log

## Out of Scope

- **launchd / systemd 整合**：可在之後獨立 phase，這次先做 manual start/stop
- **Log rotation 精細化**：目前只做 size-based，不做 time-based 或 gzip
- **Crash log 自動上傳**：不做
- **完整 structured logging**：daemon 現有 `log.Printf` 不動，只確保 panic / stderr 能被捕捉
- **Agent state trace 持久化到 DB**：只寫 NDJSON 檔案 + frontend ring buffer，不進 SQLite（避免寫入壓力）
- **改名 Purdex 相關工作**：獨立 spec 處理
