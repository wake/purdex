# KC3 沒掛時的穩定性：dev 重建換掉正在跑的執行檔、wwd 等工作目錄（spec）

日期：2026-10-22

## 背景

mlab 的 `~/Workspace` 搬到外接的 KC3（mlab repo `docs/superpowers/specs/2026-10-22-storage-reorg-design.md`），KC3 沒掛時掛載點是上鎖的空目錄。營運層已先止血（不在本 PR）：

- mlab 的 daemon 改從內建碟的 `~/.config/pdx/bin/pdx` 跑（比照 air26，App 安裝的 daemon 本來就在那裡），再跑 `pdx setup`。hook、statusline、mod 的 `pdx.json`、codex、opencode 的指向都取自 daemon 的 `os.Executable()`，所以跟著換到內建碟。daemon 沒起來時 `pdx hook` 與 `statusline-proxy` 都會靜默放行，只要執行檔在就不會報錯。
- wwd 的 `work_root`、`deps_root` 設到 `~/Workspace/.cache/wwd/{work,deps}`，把內建碟寫入量的大宗搬到 KC3。

剩下兩個程式缺口，就是本 spec。

## 1. dev 重建要換掉正在跑的那個執行檔

### 現況

`POST /api/dev/daemon/rebuild`（`internal/module/dev/daemon.go:84-128`）固定編到 `<repo_root>/bin/pdx.new`，rename 成 `<repo_root>/bin/pdx`，再請 core 重啟並 exec 那個檔（`core.RequestRestart` 的 `execPath`）。

daemon 從 `~/.config/pdx/bin/pdx` 跑時，重建之後 daemon 就跳回 repo（KC3）上的檔案：

- hook 仍指向內建碟上的舊版本，hook 與 daemon 的版本錯開；
- 下一次 `pdx setup` 讀到的 `os.Executable()` 是 repo 路徑，所有指向又被寫回 KC3，保護悄悄消失。

使用者會用這個入口自己部署 mlab（記憶 `reference_user_dev_mode_deploy`），所以這是常走的路徑。

### 要求

- **R1** 重建的目標是「正在跑的執行檔」：`os.Executable()` 經 `filepath.EvalSymlinks` 的實體路徑。解析成功時，這和 `pdx setup`、hook／statusline 安裝 API 寫進設定的是同一個值（`cmd/pdx/setup.go:129`、`internal/module/agent/handler.go:1050,1120`）。解析失敗時三者今天的處理並不一致：setup 退回未解析的路徑，agent API 會得到空字串。這個不一致另開 issue，不在本 spec；rebuild 在解析失敗時一律拒絕（R4）。
- **R2** 編到目標同目錄的 `<目標檔名>.new`，rename 成目標，重啟時 exec 目標。放在同目錄，才能保證同卷、rename 是原子的。
- **R3** daemon 本來就從 `<repo_root>/bin/pdx` 跑時（worktree、還沒切換的主機），行為與今天完全相同。
- **R4** 取不到執行檔路徑（`os.Executable` 或 `EvalSymlinks` 失敗，或得到空字串）時送 `error` 事件，不建置、不 rename、不重啟，也不在任何地方留下建置產物。**不得退回 repo 路徑**：退回就是本節要修的問題。
- **R5** 目標不是 `<repo_root>/bin/pdx` 時，不更新也不建立 repo 的 `bin/pdx`。它只是建置產物，runtime 已經沒有東西指向它。
- **不變**：建置指令、hash 與 version 注入、事件順序（`success` 在 rename 之後）、`daemonRebuildMu`、`/api/dev/daemon/check`。
- **非目標**：保留上一版執行檔（那是部署流程的事）；Electron App 的 local-daemon 更新流程（`download.go` 的 `bin/dist`）。

### 可測性

`DevModule` 加一個取執行檔路徑的函式欄位，比照 `restartFn`：

- nil 是測試安全的預設，等於 `<repo_root>/bin/pdx`（既有測試不必改，也絕不會 rename 到測試執行檔本身）；
- `Init` 把它設成 `os.Executable` 加 `EvalSymlinks`。

## 2. wwd 在工作目錄建不出來時原地等，不退出

### 現況

`cmd/wwd/main.go:131` 的 `openQuarantine` 先 `os.MkdirAll(work_root)`，失敗就 `return err`，wwd 結束（`cmd/wwd/worker.go:57`）。LaunchAgent 是 `KeepAlive` 加 `ThrottleInterval 5`，所以會每 5 秒重起一次。

mlab 的 `work_root` 在 KC3 上，因此：

- KC3 沒掛期間，wwd 每 5 秒重起一次，每次在 `wwd.log` 留一行錯誤；
- 每次開機 wwd 都比 booter 掛好 Vault 早 30–40 秒，所以都會先失敗幾輪。

這些失敗的重起不會寫進啟動紀錄（啟動紀錄在 Listen 之後才寫，`cmd/wwd/main.go:305,313`），所以不會觸發 `worker_crash_loop`；問題是重起本身與 log 噪音。

### 要求

- **W1** 在 `openQuarantine` 之前，`work_root` 建不出來（`MkdirAll` 失敗）就原地等：固定間隔重試（30 秒），建得出來才繼續原本的啟動流程。任何 `MkdirAll` 錯誤都等。設定寫錯、永遠建不出來的路徑也會一直等；這和現在一直重起是同一件事，只是安靜得多，而 log 會說明原因。
- **W2** log：第一次失敗印一行（含路徑與錯誤），之後最多每 10 分鐘一行；建出來時印一行，說明等了多久。
- **W3** 等待中收到 SIGTERM／SIGINT 時，`run` 回 `nil`，照 `run` 在 `ctx.Done()` 時的既有語意。等待中的這一輪還沒 Listen，不寫啟動紀錄；`watchStop` 留下的標記由下一次啟動的 `recordThisStart` 照現有規則處理（與今天「綁不到埠的啟動」相同）。
- **W4** 一次就建得出來時不等、不多印任何 log（現況不變）。
- **W5** 等待期間 HTTP 面不起，和現況「wwd 沒在跑」相同；其他主機看到這台連不上，就是預期的訊號。不新增「沒有 worker 的模式」。
- **非目標**：`deps_root` 不檢查（job 執行時才用；KC3 中途斷線時 job 失敗是現有行為）；launchd plist 範本不改。

## 驗收

- dev 重建：daemon 從 `~/.config/pdx/bin/pdx` 跑時重建，`/api/health` 的 hash 換新，`lsof -d txt` 仍是 `~/.config/pdx/bin/pdx`，`~/.claude/settings.json` 的指向沒變，repo 的 `bin/pdx` 沒被動到。
- wwd：把 `work_root` 指到一個建不出來的路徑啟動，wwd 行程不退出、log 只有一行；改成可以建的狀態後 30 秒內接著啟動。真機（KC3 卸載）在下一次維護時段驗。
