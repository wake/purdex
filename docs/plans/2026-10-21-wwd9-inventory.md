# WWD-9（移除舊系統）盤點

日期：2026-10-21。基準：origin/main `90e4a44cf`（含 #3803）。作者：freya（task ed3920-80）。唯讀研究：沒有改程式、沒有跑測試或建置、沒有派 codex。

**怎麼讀這份文件**：本文是整合與結論（含建議的 PR 順序與要你決定的事）。逐檔的 file:line 表格在三份附錄，本文每個結論都標了附錄章節：

- 附錄 A：`wwd9-inventory-2026-10-21-appx-9ab.md`（9a gate 本體、9b assist／module/gate）
- 附錄 B：`wwd9-inventory-2026-10-21-appx-9cd.md`（9c watch 與「叫醒守門員路徑」、9d resources／fleet／host-config／capability）
- 附錄 C：`wwd9-inventory-2026-10-21-appx-9ef-cross.md`（9e 資料表、9f lease alias、Makefile／scripts／mod／CLI／docs／wwd 的 import 清單／守門測試）

三份附錄由三位唯讀研究員（Sonnet）各自完成，我抽查過下列主張並都成立：`cmd/pdx/main.go:142-144` 的未知子命令 exit 1；`cmd/pdx/test_cmd.go:54` 的 `fastlane.Direct{}`；`internal/wwd` 與 `cmd/wwd` 有 20 個檔案 import `internal/gate`（正式碼 8、測試 12）；`resource_leases`／`resource_positions` 只剩 `internal/module/resources/store.go` 的建表與遷移。附錄 A 的行數小計有一處重複計算，本文已更正（見第 2 節）。

---

## 1. 五個最重要的結論

1. **最大的風險是部署順序，不是程式碼。** 已安裝的 shim（`~/.zshenv` 區塊）把每個 `go`／`pnpm`／`npx`／`vitest`／`tsc`／`composer`／`xcodebuild`／`swift*` 轉成 `pdx gate --auto --tool X -- …`，只有 pdx 執行檔不存在時才退回真工具（`internal/gate/shim/shim.go:30-37`）。`pdx gate` 子命令一旦消失，`cmd/pdx/main.go:142-144` 印 `unknown command` 並 exit 1，**全機的這些指令都會失敗**。舊 mod 也會把 `node_modules/.bin/{vitest,tsc}`、`vendor/bin/{phpunit,pest}` 改寫成 `pdx gate --auto --`（`hooks/gate.js:461`），同樣沒有 fail-open。所以順序必須是：mod 先更新、每台主機撤 shim（`pdx setup --uninstall-gate`，`cmd/pdx/setup.go:49-60`）、確認之後，才能刪 `gate` 子命令。（附錄 A 第 0 節、附錄 C 3.2／3.3）
2. **wwd 本身還在用 `internal/gate`，有三條線，刪之前都要先搬或換掉**：(a) `internal/gate/assist_snapshot.go`（屬 9b 要刪的 `assist_*`）裡的 `WorkingTree`／`SnapshotTree`／`SnapshotInfo` 被 `internal/wwd/submit/submit.go` 與 `testcmd/lockwait.go` 當正式碼用；(b) `internal/wwd/fastlane` 借用 `gate.Options`、`AdmittedMark*`、`NotStartedMark`、`ExitRefused`、`ExitOrphaned`、`Class*`；(c) wwd worker 每個 job 還向 gate 取一個 heavy 槽（`internal/wwd/gateheld`、`cmd/wwd/worker.go:260-285`、`loop.Gate`）並讀 `gate_full`。（附錄 C 3.5；附錄 A 9a-2）
3. **9a 的核心不能先刪，因為 9c／9d 的程式 import 它。** `internal/watch/detector.go:419` 用 `gate.ClassifyReason`；`internal/module/resources/{watch,gate_input,fleet_local,module}.go`、`internal/fleet/{frame,load,local}.go`、`internal/module/fleet/aggregator.go:518` 用 `Config／Status／Pool／AdmissionConfig`。依賴方向和計畫書的 9a→9f 順序相反：**gate 核心要排在最後刪**。（附錄 A 第 0 節）
4. **repo 內沒有人再呼叫 `pdx lease run`**，但 repo 外是否還有（lead 的部署腳本 20 支、aigora-v2、aigora-ios，引自 `docs/specs/2026-10-18-wwd-spec.md:1015-1018`）**未確認**。而且 `lease_*.go` 三個檔案不能整檔刪：`gate_cmd.go`、`host_report.go`、`team_show_cmd.go` 與共用測試輔助還依賴裡面的符號，所以 **9f 要排在 9a 之前**。（附錄 C 第 2 節）
5. **9d 最大的阻擋項是手機通知的送達者選舉**：`internal/module/wwdalerts/module.go:136-148,234-250` 用 fleet 的 primary／standby 角色決定誰推手機通知；刪 fleet 之前要先在 wwdalerts 自己決定誰推。另外 App 的側欄主機列與 Hosts 頁資源池群組目前由 fleet 快照組出（`spa/src/lib/fleet/use-fleet-host-views.ts:12-35`），沒有 fleet 時會變空，必須先由 MD-1～MD-5 與監控分頁組換掉。（附錄 B 9d-2／9d-4）

另一個小發現：計畫書的「Before 9a」兩件事（wwd 自己的磁碟下限、快速通道不再向 gate 取槽）程式上都已完成（`cmd/pdx/test_cmd.go:54`、`internal/wwd/fastlane/direct.go:23`），但 `docs/wwd-deploy.md` §13（約 573 行）與 `skills/pdx-test/SKILL.md:56-63` 還寫「快速通道向 gate 拿槽」，是現成的文件債，要在 9a 時一併改。

---

## 2. 各塊體量（`wc -l` 實測）

| 範圍 | 正式碼行 | 測試行 | 備註 |
|---|---|---|---|
| `internal/gate`（不含 shim、gatetest） | 9037（56 檔） | 10316 | 我重算的；附錄 A 的 9603／11129 已含 shim 與 gatetest，其「9603＋515＋51」重複計算了 |
| `internal/gate/shim` | 515 | 813 | 9a |
| `internal/gate/gatetest` | 51 | — | 測試輔助，`cmd/pdx/main_test.go:8,13`、`restart_integration_test.go:20,35` 用 |
| 　其中 assist 群（assist_*、out、history） | 1794（10 檔） | 2267 | 9b |
| `internal/module/assist` | 1555 | 1673 | 9b |
| `internal/module/gate`（decisions） | 222 | 183 | 9b／9d |
| `internal/module/watch` | 3988 | 4510 | 9c：確定可刪 2079（事件管線）＋單機版另可刪 452 |
| `internal/watch` | 3766 | 3813 | 9c：確定可刪 2586；單機版另可刪 562 |
| `internal/fleet` | 1778 | 1684 | 9d |
| `internal/module/fleet` | 2967 | 3472 | 9d |
| `internal/module/resources` | 2883 | 4427 | 9d：休眠磁碟守衛 387／334 可立刻刪 |
| `internal/resources` | 1516 | 1697 | 9d：不能整包刪（多個保留者在用） |
| `internal/wwd/gateheld` | 303 | 588 | 與 W3 一起刪 |
| `cmd/pdx`（gate／assist／watch／lease／setup_gate） | 3101 | 2992 | 其中 watch 的 wake／start／status 薄殼要留（見第 4 節） |
| `hooks/gate.js` ＋ `gate.test.ts` | 514 | 675 | **不能整檔刪**：同時含舊閘門與 wwd 入口（`entry`），要修剪 |
| SPA（fleet 25 檔、watch lib／components、`gate.decisions.v1` 歷史分頁） | lib/fleet 2796（含測試）、lib/watch 3886（含測試） | | 歸 B 線，不在 daemon PR |

---

## 3. 「誰還在用」的重點（逐檔表在附錄）

| 要刪的 | 還在用的地方（最關鍵的幾個） | 附錄 |
|---|---|---|
| gate 核心（9a） | wwd 8 個正式檔＋12 個測試檔；`internal/watch/detector.go:419`、`types.go:70,99`；`internal/module/resources/{watch.go:31-63,gate_input.go:21-34,fleet_local.go:173-188,module.go:90,125}`；`internal/fleet/{frame.go:306-319,load.go:196-268,local.go:56}`；`internal/module/fleet/aggregator.go:518`；`cmd/pdx` 22 檔；`cmd/pdx/plugin/embed_test.go:15,635,642,647`；`internal/execstat/guard_test.go:117-119`（白名單含 `internal/gate/run.go`、`assist_run.go`、`assist_snapshot.go`） | A 9a-2；C 3.5／3.6 |
| shim | `~/.zshenv` 區塊與 `~/.config/pdx/shims`（mlab、air26；air19、mserv 未確認）；`gate_cmd.go:147` 的 `shim.Resolve` | A 0、9a-2 |
| assist（9b） | `cmd/pdx/main.go:26,33,118-119,488,491`；gate 核心上反向掛的鉤子（`gate.go:81-86,343-344,369`、`run.go:41-50,83,206-208`、`decision.go:67-68`）；`module/assist` 取 `watch.WakerKey`（`module.go:110-121`）；SPA 唯一讀者是 `GET /api/gate/decisions`（`spa/src/lib/watch/api.ts:17`） | A 9b-2 |
| watch（9c） | `cmd/pdx/main.go:487`；`internal/module/resources/watch.go:211-219,267-277`（事件寫入）、`poolhost.go:174-179`（`watch.HostSignals`）；`module/team/watch_wake.go`、`unattended_handler.go:164`；`module/peers/policy.go:56`（8 條 `/api/peers/watch/*` 白名單）；SPA 讀者：`lib/watch/{api.ts,launch.ts,status-store.ts,events-store.ts,watch-ws.ts}`、`main.tsx:102-110`、`watch-row-write.ts:55` | B 9c-2 |
| resources／fleet（9d） | `module/team/spawn_resources.go:19-26`（`HostReaderKey`）、`module/monitor/module.go:483`（`LocalFactsKey`）、`module/pool/assess.go:4,14,32`（`fleet.LoadScore`）、`module/wwdalerts/module.go:30-31,104,137-143,234-250`、`cmd/pdx/team_show_cmd.go:36`、`host_report.go:67`、`module/peers/policy.go:59`；SPA：`lib/fleet/*`、`HostSidebar.tsx:57,188,196,204`、`OverviewSection.tsx:47,323-325`、`useTabQueued.ts:3,28`、`pool-look-migration.ts:7,91` | B 9d-2 |
| 三張表（9e） | `resource_leases`／`resource_positions`：無讀寫者，只有 `store.go:62-150,244-262` 建表與遷移＋測試；`host_minutes`：寫入 `minute.go:115`（只在沒走 pool 時，`sampler_loop.go:138-140`），讀取 `report_handler.go:19,85`（`pdx host report`） | C 1 |
| lease alias（9f） | repo 內無呼叫者；文字：`skills/pdx-gate/SKILL.md:40`；測試：`plugin/embed_test.go:635,642`、`internal/execstat/guard_test.go:114`（字串） | C 2 |

iOS（purdex-ios）原始碼不在 mlab：是否讀 gate／assist／fleet／watch 的路由與 capability，一律**未查**。

---

## 4. 對外介面與舊 App／舊 daemon 的行為

| 介面 | 拿掉後，舊 App 或舊 daemon 的行為（依 SPA 實際程式碼判斷） | 風險 |
|---|---|---|
| capability `gate.decisions.v1`、`GET /api/gate/decisions` | `QueueSection.tsx:31-34,57`：沒宣告時「歷史」分頁停用並退回「當下」，不發請求（`QueueHistory.test.tsx:284-306` 有測） | 低 |
| capability `fleet.v1`、`GET /api/fleet`、`/api/fleet/ssh-aliases`、`POST /api/fleet/probe-check`、`/api/peers/fleet/*`、WS `fleet.changed`、`?fleet=v1` | `fleet-support.ts:26` 沒宣告就 `support='no'` 不呼叫；`OverviewSection.tsx:47` 不顯示 Fleet 編輯器與觀察主機清單；舊 daemon 忽略 `?fleet=v1` | 設定頁低；**側欄主機列與 Hosts 頁資源池群組會變空（`HostSidebar.tsx:188` 的 `pool.rows.length > 0` 變假）→ 要先由 MD／監控分頁組換掉** |
| 分頁沙漏（`useTabQueued.ts:30`） | 沒 fleet 回空字串，沙漏消失，不壞 | 低（UI 退化） |
| capability `watch.v1`、`/api/watch/{wake,status}`、WS `watch.changed` | **保留**（計畫書）。要拿掉的 `GET /api/watch/{holder,unhandled,events,log}`、`POST /api/watch/ack`、WS `watch.events`：SPA 收到 404 回 `unsupported` 並 forget（`api.ts:48,64`、`events-store.ts:108`），事件頁變空不報錯 | 低 |
| `POST /api/watch/start` | 計畫書沒列，但 App 啟動按鈕用它（`spa/src/lib/watch/launch.ts:97`）；拿掉後的 404 處理**未細讀** | **要你決定**（第 6 節 #2） |
| `GET /api/assist*` | SPA 無任何讀者；assist 沒有自己的 capability | 低 |
| `GET /api/resources`、`/api/resources/report`、WS `resources.changed` | SPA 無讀者；`pdx team show`（資源欄）與 `pdx host report` 讀它；iOS 未查 | 要決定兩個 CLI 去留 |
| host-config 列 `resources`／`probe_hosts`／`fleet` | `GET /api/hostconfig` 欄位消失，SPA 寬鬆解析成空（`fleet/config-api.ts:33-35`）；PUT 路由 404 只會在按存檔時發生，編輯器因無 `fleet.v1` 已不顯示 | 低 |
| host-config `watch` 列 | **不能在 9d 刪**：wake 路由要讀（`session.go:114-117,211-224`、`module.go:340-358`） | 順序 |
| 設定檔 `~/.config/pdx/config.toml` 的 `[gate]…` | daemon 用 `toml.Unmarshal`（`internal/config/config.go:408`），沒有 `Undecoded`／`DisallowUnknownFields`，多出的表不會讓 daemon 起不來（**未實測**）；`~/.config/pdx/gate/` 目錄留在磁碟無害，但 `pressure.json` 是 `hostmem` 共用檔（`hostmem/cache.go:41`），**不要連它一起刪** | 低 |
| `pdx.json` 鍵 | 只有 `pdx`、`data_dir`、`config`、`mod_socket`、`entry`，**沒有閘門專屬鍵**，不需遷移；`entry` 是 wwd 入口開關（`plugin.go:337-346`） | 無 |
| wire reason 字串 `gate_full` | SPA 只認這個字串（`spa/src/lib/pool/words.ts:10`），且容忍未知原因碼（`parse.test.ts:70-72`）；wwd 停發它不會壞 App | 低 |

---

## 5. 移除前必須先換掉的「正式路徑」

1. **shim 與舊 mod**（結論 1）：先出新 mod、再逐機撤 shim。撤掉後，沒有 mod 的行程（Codex、一般終端機、launchd／cron、aigora-v2 在 air26 走 `assist-run.sh` 時的 `go`／`pnpm`／`swift`）就**不再有任何限流或排隊**，只剩 wwd 的觀察偵測（`internal/wwd/facts/observe_text.go:401-418`，只告警不攔）。這是行為變更，不是純刪除，**建議你向使用者確認**（第 6 節 #1）。
2. **wwd 三條依賴**（結論 2）：W1（搬 snapshot）、W2（fastlane 自有型別）、W3（拔 gateheld）。
3. **入口**：刪 `prefixGated` 與 would-wait 之後，沒宣告成 task 的直接呼叫只剩 `[entry].mode`（`rewrite` 轉成 `pdx test --adhoc`；未開 `entry` 的主機直接跑）。**確認所有開發主機（mlab、air26，air19／mserv 未確認）的 `pdx.json` 都有 `entry`**（`docs/wwd-deploy.md` §13）。
4. **非 Claude Code 的測試呼叫**：`assist-run.sh`、`assist-verify.sh`、`mserv-run.sh` 在 repo 外，S2 後已停用（lead 訊息），但 `module/assist` 的 `/api/assist` 通知與 air26 的 assist intake 是否已全部撤掉**未確認**（`docs/specs/2026-10-18-wwd-spec.md:1180` 說 hold 仍餵給 air26 的 assist intake 與 mserv 自檢）；刪 `module/assist` 前要有確認。
5. **叫醒守門員**（lucia 要求補的，詳見第 7 節）。
6. **wwdalerts 的送達者選舉**、**`module/pool` 借用的 `fleet.LoadScore`**、**spawn 資源卡與 monitor 讀 `module/resources`**、**gate 的 `pressure.json` 由 daemon 寫（`module/resources/gate_input.go:44-60`）**（附錄 B 9d-4）。
7. **`execstat` 守門測試**：`internal/execstat/guard_test.go:114,117-119` 以檔案路徑白名單列出 `internal/gate/run.go`、`assist_run.go`、`assist_snapshot.go` 與一條 `pdx lease run` 描述；搬或刪時要同步改，搬到 wwd 的 `WorkingTree`／`SnapshotTree` 要在新位置登記，PR 層加跑 `pdx test execstat-guard`。
8. **`.wwd/tasks.toml:161`** 與 `internal/wwd/manifest/purdex_gate_test.go:18` 的 `wwd-linux` 套件清單含 `./internal/wwd/gateheld/`（刪 gateheld 時同步改）。
9. **9e 的遷移**：`resources.db` 沒有 `user_version`；repo 沒有「退役後單純刪表」的先例（既有 `DROP TABLE` 都是重建型：`team/migrate.go:236`、`team/pause_store.go:102`、`store/meta.go:216`、`store/trace_migration.go:366,525`）。**回滾安全**：舊版 daemon 每次開 `resources.db` 都會 `CREATE TABLE IF NOT EXISTS` 把三張表建回來（`store.go:62-108,244-280`），所以新版 DROP 之後退回舊版不會壞，只是歷史資料沒了（`CHANGELOG.md:927` 已有同一承諾）。注意 `wwd.db` 也有一張同名 `host_minutes`（`internal/wwd/store/minutes.go:23-41`），9e 的 DROP 不可波及。

---

## 6. 需要你（或使用者）決定的事

| # | 問題 | 影響 | 我的建議 |
|---|---|---|---|
| 1 | 撤 shim 後，非 Claude Code 的 shell 不再限流，接受嗎？ | 9a 的前提 | 先問使用者；wwd 觀察仍會告警 |
| 2 | `POST /api/watch/start` 要不要留？（App 的啟動按鈕用它，`launch.ts:97`） | 9c 要不要留 `locate.go`／`asker`／`remote` | 留 wake 與 status 就夠的話，把 start 併進 wake；否則連 locator 一起留 |
| 3 | status 要不要顯示別台主機的守門員？ | 差 1014 行正式碼＋1376 行測試（`locate`／`asker`／`remote`／announce） | 若守門員固定在一台，只看本機 |
| 4 | `WakeWatcher`（與 `notifyWatcher`、`peerNotifier`）要不要留？ | lucia 說 wwd alert 之後要經它叫醒；目前 `internal/module/wwdalerts/module.go` **沒有**取 `watch.WakerKey`（全 repo 只有 assist、team 兩處取） | 要做就留，且 `internal/watch/wake.go`、`message.go:39` 的 `Plain` 從「可刪」清單移出 |
| 5 | fleet 消失後，誰推手機通知（wwdalerts 的送達者）？ | 9d 最大阻擋項 | 固定一台，或改依 wwd 的主機清單 |
| 6 | `pdx host report`、`pdx team show` 的資源欄，以及 `GET /api/resources[/report]` 去留？ | 9d-6 | 要留就把資料來源改成 wwd，再刪 module/resources |
| 7 | `team` 的無人值守開關通知（`team/watch_wake.go`，經 `WakeWatcher`）要不要保留？ | 9c-c | 若 #4 決定留 WakeWatcher，就一起留 |
| 8 | repo 外的 `pdx lease run --kind build` 呼叫者（20 支部署腳本、aigora-v2、aigora-ios）是否已搬完？ | 9f | 派工前由你核對；`--kind build` 在 alias 裡等於直接執行，換成直接呼叫 build 指令即可 |
| 9 | air26 的 assist intake、mserv-run.sh、air19／mserv 的 shim 是否已撤？ | 9b、S0 | 派工前由你或使用者確認 |
| 10 | 9e 要「真的 DROP」還是「只停止建立」？ | 零風險 vs 乾淨 | 第一步只停止建立（對已部署庫完全無動作），DROP 等切換穩定之後 |

---

## 7. 叫醒守門員路徑（lucia 要求補的）

守門員的「叫醒」有兩層：**A. 把 session 叫起來（`session.Wake`，打進 tmux pane）**；**B. 對已醒著的守門員送一則文字通知（note）**。`WakeWatcher` 同時做兩者。逐項 file:line 見附錄 B「叫醒守門員路徑」。

| 路徑 | 結論 |
|---|---|
| App「叫醒」按鈕：`launch.ts:97` → POST `/api/watch/wake`（`module/watch/module.go:295`）→ `routes_session.go:138 handleWake` → `wakeReply` → `session.go:114 sessionCtl.Wake` → `work`（`EnsureSession`、`PaneAtShell`、`TypeLine`、等 `Reconcile`） | **必須留**。依賴 `session.go`、`reconcile.go`、`changed.go`、`internal/watch/{holder,session,pane,prompts}.go`、host-config `watch` 列（`watchsettings`，裁欄位） |
| App「啟動」按鈕：POST `/api/watch/start` → `routes_session.go:101 handleStart` → `locate.go:314` | 看第 6 節 #2 |
| 別台主機：`asker.go:57-62` → `/api/peers/watch/wake` → `handlePeerWake` | 跨主機版才留 |
| `watch.waker` 服務：註冊 `module/watch/module.go:174`，實作 `module/watch/wake.go:28 WakeWatcher` | 使用者：assist、team、patrol |
| patrol 定時叫醒：`patrol.go:144-146,158,188,194` | **可刪**（S3 退場巡邏）。但 `recoverSession`（`session.go:347`）只由 patrol 驅動，patrol 刪後要換驅動 |
| team 無人值守開關：`unattended_handler.go:164` → `team/watch_wake.go:25` → `WakeWatcher` | 看第 6 節 #7 |
| assist 開關／允許名單：`assist/switch.go:59`、`allow.go:217` → `assist/notify.go:295` → `WakeWatcher` | 隨 9b 刪 |
| **事件送達時叫醒（`unmanaged_heavy` 等）**：`module/resources/watch.go:267-277 sink.Enqueue` → `module/watch/delivery.go:96` → `deliverPass`（`:156`）→ `loc.Current(..., s.AutoWake)`（`:169`）→ `sendMessage`（`:231`） | **可刪**（偵測器與事件管線整條退場；pool 模式本來就不推 E1） |
| 休息流程：`rest.go:183,284` | 可刪，但 **`notifyWatcher`（`rest.go:284`）與 `peerNotifier`（`twoawake.go:279-285`）住在要刪的檔裡，WakeWatcher 的單機通知與 `handlePeerNote` 都靠它，要先搬出** |
| 收到別台的 note：`/api/peers/watch/note` → `wake.go:93-143 handlePeerNote` | WakeWatcher 留才留 |
| 之後的 wwd alert | 目前沒有（見第 6 節 #4）；要做就不能刪 `WakeWatcher` |

**拆 watch 時必須留下**：`handleWake`／`wakeReply`／`requireAdmin`、`sessionCtl`、`Reconcile`、`handleStatus`／`Status`／`view`／`watcherNow`／`notifyChanged`、`internal/watch` 的 holder／session／pane／prompts、host-config `watch` 列與 `watchsettings`（裁欄位）、WS `watch.changed`、capability `watch.v1`（宣告文字改成只含 status／wake／start）；若第 6 節 #4 決定留 WakeWatcher：`notifyWatcher`＋`peerNotifier`、`internal/watch/wake.go`、`message.go:39 Plain`。**留下的部分依賴會被刪的東西（要先搬出）**：`notifyWatcher`、`peerNotifier`、`recoverSession` 的驅動、`Plain`（assist 與 team 在用）、`onRegistered`（`twoawake.go:56`，`Init` 掛到 reconcile 的 registered hook，刪 twoawake 要改這裡）。

---

## 8. 子期相依與建議順序

依賴方向（箭頭＝「必須先於」）：

```
W1 搬 snapshot ─┐
W2 fastlane 自有型別 ─┼─→ 9b-2（刪 assist 群）
W3 拔 gateheld ─┘
S0 撤 shim 的準備 ─→ 9a-1（刪 shim）、9a-2（修剪 mod）─→ 9a-3（刪 pdx gate 子命令）
9f（刪 lease alias） ─→ 9a-3
9b-1（拔 gate 核心上的 assist 鉤子）─→ 9b-2 ─→ 9b-3（module/assist）─→ 9c（assist 與 team 還在叫 WakeWatcher、用 Plain）
MD-1…MD-5＋監控分頁組（SPA 不再讀 /api/fleet）─→ 9d 刪 fleet
9d 的搬遷（LoadScore、wwdalerts、spawn 資源卡、monitor）─→ 9d 刪 fleet／resources
9c ＋ 9d（偵測器與 resources／fleet 不再 import gate）─→ 9a-4（刪 gate 核心）
9d-6（module/resources、host report 的 host_minutes 讀者）─→ 9e-2
```

所以**計畫書的 9a→9f 順序要改**，建議實際執行順序：**W1／W2／W3 → 9f → S0（含逐機撤 shim）→ 9a-1／9a-2 → 9b → 9c → 9d → 9a-3／9a-4 → 9e**。

---

## 9. 建議的 PR 切法（每張 ≤ 800 行或 ≤ 20 檔；純刪除放寬，PR 說明寫「純刪除、無新增邏輯」並附 `rg` 證明沒有呼叫端）

| 序 | PR | 內容 | 規模 | 前置／備註 |
|---|---|---|---|---|
| **1（建議第一張）** | **W1** | 把 `WorkingTree`／`SnapshotTree`／`SnapshotInfo`（連同 `buildTree`、`tempIndexConfig`、`readIndex`／`writeTo`、`flaggedEntries`／`clearFlags`，約 265 行）從 `assist_snapshot.go` 搬到 `internal/wwd/snapshot`（已有 `git.go` 用同類符號；若造成 import 循環則另開 `internal/wwd/worktree`，**循環未確認**）；測試 `assist_snapshot_index_test.go` 233 行＋`assist_worktree_test.go` 166 行跟著搬；換 import（`submit.go`、`lockwait.go`、3 個 `snapshot` 測試）；`execstat` 白名單同步 | 約 650 行搬移，約 10 檔 | 純搬移：用逐宣告位元組比對證明，免派 codex。**無前置，可立刻做**，風險最低，且解開 9b |
| 2 | W2 | `fastlane` 自己定義 `Options`（只留 Argv／Real／Env／Dir／Stdio）、標記字串（值不變）、結束碼、`Class`；5 個 fastlane 測試改成不用 `gate.New` | 約 400 行改，約 12 檔 | 無前置 |
| 3 | W3 | 刪 `internal/wwd/gateheld/`（303＋588）、`loop.Gate`／`GateSlot`、`cmd/wwd` 的 `gateTaker`、`sampler.GateHeavyFull`、`derived.GateHeavyFull`、`.wwd/tasks.toml:161`、`purdex_gate_test.go:18`、`worker_gate_test.go`；`wire` 的 `gate_full` 字串保留但不再產生 | 約 −1250 行，約 20 檔 | 行為變更，需 wwd 負責人（_doxhaf）審；與 W1／W2 無相依 |
| 4 | 9d-1 | 刪休眠的磁碟守衛（`diskguard.go`、`diskfree_*.go`，387／334，`diskguard.go:64-66` 註解「Nothing calls it」） | 5 檔 | 與其他無關，可隨時做 |
| 5 | 9e-1 | `DROP TABLE IF EXISTS resource_leases／resource_positions`（單一連線、`CREATE` 之前、含索引隨表消失）；刪 `store.go` 對應建表與 `migrateDecisionColumns` 段落、`minute_test.go:230-280`、`testdata/resources-alpha610.schema.sql`；測試：舊庫有表→開啟後沒了、新庫無表不報錯、開兩次冪等 | 約 −250／＋60，約 4 檔 | 無讀寫者，可隨時做；見第 6 節 #10 |
| 6 | 9f | 刪 `lease_cmd.go`／`lease_run_alias.go`／`lease_mode.go` 與兩個測試；把 `splitRunArgs`／`leaseStdinIsTTY`／`classByClassifier`／`classByKind` 併入 `gate_cmd.go`，`leaseClient*` 併入 `host_report.go`，`getResources` 併入 `team_show_cmd.go`，測試輔助 `fakeResourcesDaemon`／`fakeSnapshot` 搬到共用測試檔；刪 `main.go:102-103`、`host_report_test.go:152-160`、`embed_test.go:635`、SKILL.md:40 一行、`exitcodes.go` 的 `ExitQueued`；改 `execstat/guard_test.go:114` 字串（`reap_children.go` 不是 lease 專用，**保留**） | 淨刪約 −600 行，約 12 檔 | **repo 外呼叫者先確認**（#8） |
| 7 | S0 | `pdx setup` 自動撤 shim 與 `~/.zshenv` 區塊，並讓 `pdx gate --auto --tool X -- cmd` 退成直接 exec 真工具（過渡版）；部署步驟寫進 `docs/wwd-deploy.md`：新 mod 先上 → 確認 session 重載 → 每台確認 shim 已撤 | 約 100 行 | 必須先於 9a 其他刪除 |
| 8 | 9a-1 | 刪 shim 套件、`setup_gate.go`、`--uninstall-gate`；`gate_cmd.go:147` 的 `shim.Resolve` 依賴先搬進 `cmd/pdx` | 約 −515／−813／−112，約 12 檔 | S0，且每台確認 shim 已撤 |
| 9 | 9a-2 | mod：修剪 `hooks/gate.js`（刪 `prefixGated`／`askWouldWait*`／`planGate` 後半，留分詞與 `entry`）、`gate.test.ts` 對應測試、`pdx-gate` skill、`pdx-test` skill 與 `embed_test.go`／`skill_pdxtest_test.go` 的斷言、`CLAUDE.md:122` 與測試分層說明、`docs/wwd-deploy.md` §13 過時句 | 約 600 行；**要跑 `./cmd/pdx/...`（含 plugin 子套件）與 mod 測試** | S0 |
| 10 | 9b-1 | 拔 gate 核心上的 assist 鉤子（`Options.Assist／Walk`、`run.go` 的 `tryAssist／walkAssist`、`decision.AssistWhy`、`out.go` 的 out ticket、`rank.go`／`slots.go` 的處理） | 修改多於刪除，約 400 行，約 8 檔 | W1 |
| 11 | 9b-2 | 刪 assist 群（`internal/gate/assist*`、`out.go`、`history.go`）＋`cmd/pdx/assist_cmd.go`、`gate_walk.go`、`gate_history_cmd.go` 與 `gate_cmd.go` 內的 `hosts／history` 分支＋`main.go:118` | 純刪除，正式約 2600／測試約 2600，約 25 檔（放寬） | 9b-1、W1、assist 外部腳本確認退役（#9） |
| 12 | 9b-3 | 刪 `internal/module/assist/`、`internal/team/assist.go` 與 `module/team` 的 `RefSuccessors`／`LocalLiveSessionsOfTeam`（`relay_store_lineage.go:105,127`、`module.go:575`）、`main.go:26,491`、`wake.go` 的 assist 理由、pdx-watch skill 的 assist 一節 | 純刪除，正式約 1600／測試約 1700，約 25 檔（放寬） | 9b-2 |
| 13 | 9c-a | 把留下的路徑搬乾淨：`notifyWatcher`＋`peerNotifier` 搬進 session／module、`recoverSession` 換驅動、`Plain` 搬出 | ≤ 800 行，純搬移可用位元組比對 | 9b-3；#2／#4 先決定 |
| 14 | 9c-b-i／ii | 刪事件管線：(i) `internal/watch` 的 12 檔＋13 測試檔；(ii) `module/watch` 的 delivery／events／eventslog／ackpeer／outbox 等 7 檔＋7 測試檔＋`module/resources/watch.go` 與 `watch_test.go`＋`internal/gate/watch_api.go`＋4 條 peers 路由＋WS `watch.events` | 純刪除，兩張各約 2000／2300 | 9c-a；SPA 事件頁讀者（B 線 8b）要先確定不再讀，或接受 404（目前安全） |
| 15 | 9c-c | 刪 patrol、rest、單機版 twoawake、`team/watch_wake.go`、CLI 的 `ack`／`log`／`config`、`pdx-watch` skill 的對應段落（`watch wake`／`start`／`status` 薄殼依 #2 保留） | 純刪除 | 9c-b；#4／#7 |
| 16 | 9d-2 | 搬 `LoadScore`／`LocalFast`／`levelScore` 到 `module/pool`；wwdalerts 改成不靠 fleet 角色（#5） | 約 300 行 | 必須在刪 fleet 之前 |
| 17 | 9d-3 | spawn 資源卡與 monitor 的來源改接 wwd（pool 模組）；取消 `resources.host-reader`／`local-facts` 註冊 | 約 400 行 | 9a 的 `pressure.json` 決定之後 |
| 18 | 9d-4 | 刪 `module/fleet`＋`internal/fleet`＋`?fleet=v1`＋peers 白名單 `policy.go:59`＋capability `fleet.v1`＋三條 `/api/fleet` 路由 | 純刪除，正式 4745／測試 5156，約 44 檔，**拆成兩張**：(i) `internal/fleet`＋`module/fleet`；(ii) `core`／`peers`／`info_handler` 的小改 | **MD-1…MD-5 與監控分頁組先出**（SPA 不再讀 `/api/fleet`） |
| 19 | 9b-4／9d-7 | 刪 `module/gate`＋capability `gate.decisions.v1`＋`main.go:33,488`＋`info_handler.go:180-182`；SPA 另開一張（歷史分頁 `QueueHistory.tsx` 等，`vitest related`＋`tsc -b`） | 後端 ≈400、SPA ≈360＋測試 | 與 9d 同批；等監控頁已改接 pool jobs |
| 20 | 9d-5 | 刪 hostconfig 的 `resources`／`probe_hosts`／`fleet` 列（含 PUT 路由與 GET 欄位） | 正式 607／測試 880，約 9 檔 | SPA 不再讀之後 |
| 21 | 9d-6 | 刪 `module/resources` 與 `GET /api/resources[/report]`、`resources.changed`、host-config `resources` 設定；`pdx host report`／`pdx team show` 改法（#6） | 正式約 2210／測試約 3628 | 9d-3；`getResources`／`leaseClient` 已在 9f 搬走 |
| 22 | 9a-3 | 刪 `pdx gate` 子命令與其餘 CLI（`gate_cmd／queue／conflict／free／fuse`） | 純刪除，約 1250／1600 | 9b-2、9a-1、9a-2、9f |
| 23 | 9a-4 | 刪 `internal/gate/` 剩下的核心（gate、run、slots、rank、conflict、config*、decision*、caps、mod_*、classify*、pressure、gatetest…），更新 `cmd/pdx/main_test.go`／`restart_integration_test.go` 的 `gatetest.Main`（TestMain 要換掉） | 純刪除，約 8000／9000；**拆成兩張**：classify＋pressure＋mod；核心 | **9c 與 9d 必須先完成或改接** |
| 24 | 9e-2 | 刪 `host_minutes`：`InsertMinute`、`PruneMinutes`、`minute.go`、`boot.go`、`report_handler.go` 的舊段與 `report_pool.go` 的 carry 邏輯、`minute_*_test.go`、`boot_test.go` | 約 −700 行，約 8 檔 | 9d-6；兩台主機 `fleet.source` 已是 pool；舊列過了 14 天保留期或接受失去舊段 |

**建議的第一張 PR：W1**（純搬移、無前置、免派 codex、解開 9b）。第二、三張是 W2、W3；9d-1、9e-1 兩張獨立小刪除可穿插。

---

## 10. 「未確認」清單（repo 內查不到，派工前要問）

1. `assist-run.sh`、`assist-verify.sh`、`mserv-run.sh` 與 air26 的 assist intake 是否已全部退役（repo 外）。
2. air19、mserv 是否裝有 shim；mlab、air26 的 `~/.zshenv` 目前狀態。
3. `pdx lease run --kind build` 的外部呼叫者（lead 部署腳本 20 支、aigora-v2 `scripts/lib/check.ts:500`、`desktop/scripts/walkthrough.sh`、aigora-ios `scripts/build.zsh:103`）是否已搬完。
4. iOS App 是否讀 gate／assist／fleet／watch 的路由與 capability。
5. mlab、air26 目前的 `fleet.source` 設定（影響 9e-2）與 `resources.db` 實際大小。
6. W1 搬到 `internal/wwd/snapshot` 是否造成 import 循環。
7. `[gate]` 表留在 config.toml 時 daemon 是否真的照常啟動（只有程式碼推論，沒有實測）。
8. `POST /api/watch/start` 被拿掉時，`launch.ts` 的 404 處理（未細讀）。
9. `execstat` 守門測試在白名單條目對應的檔案不存在時的行為（PR 實作時跑該單一套件證實）。

---

## 11. lead 裁定（1f，2026-10-21）

| # | 裁定 | 理由 |
|---|---|---|
| 1 | 接受。撤 shim 是使用者 10-20 定的 S4（移除 gate shim／名額）的一部分，不另問。前提：S0 之前 aigora-v2 的測試要先接上 wwd（它在 air26 經 assist-run 跑 go／pnpm／swift，撤 shim 後就完全不排隊），由 lead 跟 Aigora 那邊協調；Codex 與一般終端機靠 wwd 的觀察與 S3 的 alert 盯。 | 不雙軌；風險集中在 aigora 的重建置 |
| 2 | 留 `POST /api/watch/start`（App 的啟動按鈕在用，不能讓 App 壞）。 | App 不能壞 |
| 3 | 留跨主機（locate／asker／remote）。S3-1 由 fleet primary（現在是 air26）叫醒住在 mlab 的守門員，必須跨主機。 | S3-1 需要 |
| 4、7 | 留 `WakeWatcher`、`notifyWatcher`、`peerNotifier`、`internal/watch/wake.go`、`message.go` 的 `Plain`；team 的無人值守通知也留。 | S3-1、team 都靠它 |
| 5 | 9d 設計時定（fleet 拿掉後的送達者／叫醒者選舉）；S3-1 先沿用 fleet 角色。 | 等 9d |
| 6 | 9d 設計時定；傾向改讀 wwd，沒有讀者的就刪。 | 等 9d |
| 8 | lead 在 9f 派工前核對 repo 外的 `pdx lease run --kind build` 呼叫端。 | |
| 9 | mserv-run.sh、assist-run（purdex）、assist-verify 已停用（10-21）；air26 assist intake 與 air19／mserv 的 shim 由 lead 在 S0 前實查。 | |
| 10 | 9e 先只停止建立，DROP 等切換穩定後另一張。 | 零風險先行 |

順序採用第 8 節：W1/W2/W3 → 9f → S0 → 9a-1/9a-2 → 9b → 9c → 9d → 9a-3/9a-4 → 9e；S3-1、S3-2 與 W 系列並行。
