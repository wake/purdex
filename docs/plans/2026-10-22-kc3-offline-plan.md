# KC3 沒掛時的穩定性（plan）

spec：`docs/specs/2026-10-22-kc3-offline-spec.md`。一張 PR，分支 `worktree-kc3-offline`，每個 task 一個 commit。預估 diff 約 200–300 行（含測試）。

## Task 0：spec 與 plan 進 repo

把兩份文件放到 `docs/specs/`、`docs/plans/`，單獨 commit。

## Task 1：dev 重建換掉正在跑的執行檔（spec R1–R5）

**檔案**：`internal/module/dev/module.go`、`internal/module/dev/daemon.go`、`internal/module/dev/daemon_test.go`

1. 新增具名函式 `runningBinary() (string, error)`：`os.Executable()` 後 `filepath.EvalSymlinks`，任一步失敗就回錯，不退回未解析的路徑。
2. `DevModule` 加欄位 `selfPathFn func() (string, error)`，註解比照 `restartFn`：nil 等於 `<repoRoot>/bin/pdx`，是測試安全的預設（絕不會 rename 到測試執行檔本身）；`Init` 在 nil 時設成 `runningBinary`。
3. 加 `func (m *DevModule) rebuildTarget() (string, error)`：`selfPathFn` 為 nil 時回 `filepath.Join(m.repoRoot, "bin", "pdx")`；否則回 `selfPathFn()`，得到空字串也當錯誤。
4. `handleDaemonRebuild`：
   - 在建置之前（包括 `MkdirAll`、git hash 查詢之前）先取 `target`；失敗就送 `error` 事件（訊息固定以 `cannot find the running pdx binary` 開頭，說明原因），然後 return，不建置。
   - `binDir := filepath.Dir(target)`（保留 `MkdirAll`）；`newPath := target + ".new"`；`finalPath := target`。
   - 其餘（hash 與 version 注入、`buildBinary`、rename、`success`、`restartFn(reason, finalPath, nil)`）不變。
   - 更新相關註解：「Atomic swap: bin/pdx.new -> bin/pdx」改成說明換的是正在跑的執行檔，以及為什麼（spec §1）。
5. **測試（先寫、先紅）**：
   - `TestHandleDaemonRebuild_ReplacesTheRunningBinaryOutsideTheRepo`：`selfPathFn` 回 `<tmp>/installed/pdx`（先放一個內容為 `old` 的假檔）。重建後該檔不再是 `old`（是新建出的執行檔，大小大於 0）；`<tmp>/installed/pdx.new` 不存在；`<repo>/bin/pdx` 不存在；`restartFn` 收到的 `execPath` 等於 `<tmp>/installed/pdx`。
   - `TestHandleDaemonRebuild_NoRunningBinaryPathIsAnErrorAndNothingIsBuilt`：分兩個子測試，`selfPathFn` 回錯誤、回 `("", nil)`。事件恰好一個，是 `error`，訊息以 `cannot find the running pdx binary` 開頭；`restartFn` 沒被呼叫；**整個臨時 repo 目錄樹與目標目錄在呼叫前後完全相同**（走訪比對路徑清單，不只看 `bin/`）。這能抓到漏掉 `return` 時 `go build -o .new` 在 repo 根目錄留下的產物。
   - `TestRunningBinary_IsTheResolvedExecutable`：`runningBinary()` 的結果等於測試行程自己的 `filepath.EvalSymlinks(os.Executable())`，且是絕對路徑。
   - `TestInit_InstallsRunningBinary`：對 `selfPathFn` 為 nil 的模組呼叫 `Init`（用測試裡現有建 core 的方式；沒有就用最小的 core），之後 `selfPathFn` 不為 nil，呼叫結果等於 `runningBinary()`。
   - `TestHandleDaemonRebuild_RunningFromTheRepoBinaryIsUnchanged`：`selfPathFn` 回 `<repo>/bin/pdx`，結果與既有的 `RequestsCoreRestartWithRebuiltBinary` 相同。
   - 既有測試不改（nil 等於舊行為）。若需要 `go build` 的既有 helper（建臨時 repo），沿用它。
6. **mutation**（一次一條，前後 `git status` 都要乾淨，`go test` 帶 `-timeout 60s`）：
   - (a) `rebuildTarget` 一律回 repo 路徑 → 第一個測試要紅；
   - (b) 拿掉「取不到路徑就 return」→ 第二個測試要紅；
   - (c) `Init` 不設 `selfPathFn` → `TestInit_InstallsRunningBinary` 要紅；
   - (d) `runningBinary` 拿掉 `EvalSymlinks` → 視測試行程路徑是否含 symlink 而定；若不紅，在報告註明，不必硬造。
7. **不另寫「替換正在執行的檔案」的測試**：rename 蓋過正在跑的執行檔、再 exec 該路徑，正是今天 dev 重建對 repo `bin/pdx` 的做法（daemon 就從那個檔跑），已在正式環境用過多次。真機驗收（spec「驗收」第一點）由 lead 在部署後做。

## Task 2：wwd 等工作目錄（spec W1–W5）

**檔案**：`cmd/wwd/worker.go`（或同套件新檔 `cmd/wwd/workroot.go`）、`cmd/wwd/main.go`、對應的 `_test.go`

1. 新增：
   ```go
   // waitWorkRoot makes the work root, waiting while it cannot be made (the volume it is on is not mounted yet) rather than
   // returning: launchd would start wwd again every few seconds. ... returns false when ctx ends first.
   func waitWorkRoot(ctx context.Context, dir string, retry, logEvery time.Duration, now func() time.Time, logf func(string, ...any)) bool
   ```
   - 第一次 `os.MkdirAll(dir, 0o700)` 成功就直接回 `true`，不印 log（W4）。
   - 失敗：印一行，內含路徑、錯誤字串、重試間隔；之後每 `retry` 重試；距離上一行 log 滿 `logEvery` 才再印（W2）。
   - 成功：印一行，內含路徑與等待時間（用注入的 `now` 計算），回 `true`。
   - `ctx.Done()` 時回 `false`（W3）。
2. `main.go` 的 `run`：在 `openQuarantine` 之前呼叫 `waitWorkRoot(ctx, cfg.WorkRoot, 30*time.Second, 10*time.Minute, time.Now, log.Printf)`；回 `false` 就 `return nil`。`openQuarantine` 自己的 `MkdirAll` 保留（無害，也讓它單獨被呼叫時依然正確）。
3. **測試（先寫、先紅）**：
   - 目錄建得出來：回 `true`、log 零行。
   - 讓 `MkdirAll` 失敗的方式：在 `<tmp>/vol` 放一個**一般檔案**，工作目錄設成 `<tmp>/vol/work`（得到 ENOTDIR，與執行身分無關，不必 skip）。恢復時刪掉該檔、建成目錄。
   - 失敗後恢復：另一個 goroutine 約 50 ms 後把 `<tmp>/vol` 換成目錄。`retry` 5 ms，應回 `true`；log 恰好兩行：第一行含 `<tmp>/vol/work`、錯誤字串（`not a directory`）與重試間隔；第二行含 `<tmp>/vol/work` 與等待時間（注入的假 `now`，斷言印出的時間是假時鐘算出的值）。
   - log 節流：`<tmp>/vol` 維持是檔案，`retry` 1 ms、`logEvery` 1 小時，跑約 30 ms 後 cancel ctx。應回 `false`，失敗 log 恰好一行。
   - ctx 在等待中被 cancel：回 `false`，且在一個 `retry` 內返回。
   - `run` 不寫單元測試（它會綁 tailnet 位址，現有測試也沒有直接跑它）。接線位置用 code review 把關：`waitWorkRoot` 必須在 `openQuarantine` 之前、在 `d.Listen` 之前。
4. **mutation**（同上規則）：
   - (a) `waitWorkRoot` 失敗時直接回 `false` → 第二個測試要紅；
   - (b) 拿掉 `logEvery` 節流 → 節流測試要紅。

## PR 層驗證（照 CLAUDE.md「測試分層」）

- 一律走 `pdx test`（沒有對應 task 時用 `pdx test --adhoc -- …`）：`go test ./internal/module/dev/ ./cmd/wwd/`，再加 `make lint`。
- 不跑 `-race`：沒有改到鎖或 goroutine 的共享狀態，測試裡的 goroutine 只做 chmod。
- 沒有新增 `exec.Command`，不必加跑 `./internal/execstat/`。
- 不改 SPA，不跑 vitest 或 tsc。

## 不做

- `internal/module/agent/handler.go:1050,1120` 在 `EvalSymlinks` 失敗時得到空字串：另開 issue（spec R1）。

- 專案 `CLAUDE.md` 不改：主 checkout 有使用者未 commit 的 `CLAUDE.md` 修改，避免衝突；文件由 lead 另外處理。
- 部署腳本、plist 範本、保留上一版執行檔。
