# Changelog

## [1.0.0-alpha.768] - 2026-10-20

> daemon、`pdx` CLI、`wwd` 與 SPA 有改；**mod 沒有改，不用重跑 `pdx setup`**。依賴沒變，不用 `pnpm install`。Electron 外殼的程式與 767 相同，但 App 仍要用 768 重新打包（見下一段）。資料庫結構沒變（host schema 9、queue schema 9），wire 的 `proto` 仍是 3。
>
> **要一起更新 Mac App**：Profile Sync 的 settings 區版本從 12 升到 13（#3773）。還沒更新的 App 在 settings 區會被擋住，不能同步設定，所以 air26 和 air19 的 Mac App 要跟著更新。iOS App 不受影響。
>
> **能力退役**：`task_groups.v1` 從 `/api/info` 拿掉了，`/api/hostconfig/task-groups` 也不再提供（#3757）。App 從 TG-6 起就不讀它。
>
> **worker**：部署時各台的 worker 先一律關閉。檢查通過後只打開 air26；mlab 先維持關閉，由守門員決定何時打開（這次用它取代 exception，避免擋到 mlab 閘門的重名額）。mserv、air19 仍關著。

### Added：wwd 主機指標補齊（G 項）

- **series 帶出 Facts 已有的欄位（#3749，G-1）**：`/v1/facts/series` 每分鐘的點多了 load1／load5、swap 用量、壓縮器、swap 換出速率、解壓速率、worker 執行數、佇列中的工作數與各磁碟的用量（後面 G-2～G-4 的新欄位也會出現在 series）。另加一條守門測試：取樣程式（`facts`、`hostmem`）不准另外 fork 行程，只有兩個已知的例外。
- **CPU 細項（#3755，G-2）**：Facts 的 `cpu` 多了 `load15`、`user_pct`、`system_pct`，以及每個核心的使用率與種類（效能核／節能核）。
- **磁碟 IO（#3759，G-3）**：Facts 多了 `disk_io`，列出每顆磁碟的讀寫速率與 IOPS；macOS 與 Linux 都有。
- **其他指標（#3756，G-4）**：行程總數 `procs_total`、開機時間 `boot_at`、wwd 自己的 CPU 與記憶體 `self`、swap 總量與記憶體分項（wired、active、inactive、檔案快取）。`/v1/pool` 與 Purdex 的 `/api/pool/hosts` 多了 `rtt_ms`（輪詢那一台花了多久），Purdex daemon 也要是 768 才看得到。

### Changed：wwd 的工作目錄與快取

- **Go 建置快取依「多久沒用」修剪（#3760）**：以前快取超過上限、而且沒有 job 在用時才整份清空；現在每小時刪掉超過 `[worker] gocache_prune_hours`（預設 24 小時）沒用過的項目，超過上限時從最舊的刪到上限的 80%，不必等快取沒人用。只有修剪後仍超過上限的快取才會整份清空。mlab 的 `gocache_max_mb = 6144` 照舊。
- **工作目錄的 tree 算得更快（#3751，#3745）**：本機快速通道登記工作目錄 tree 的額外時間變短。

### Fixed

- **關閉分頁與瀏覽歷史跟著主機改寫（#3753，#3685）**：主機被辨認或移除後，從「重開最近關閉的分頁」或 History 頁重開的分頁，主機與 team 編組都會指向正確的主機，不再指向已經不存在的舊主機。
- **主機記憶體測試不再偶發失敗（#3761，#3758）**：解壓計數的檢查會等過 macOS 一秒內回傳快取值的窗口再重試，忙碌的主機不再誤判。

### Added：B 線個人面板（TG-5）

- **不在 team 裡的 agent 分頁也有個人面板（#3763、#3764、#3767、#3769）**：標題列的「個人」按鈕取代原本的「自己的工作簿」。單行顯示頭像、名稱與標題；展開後是完整卡片（agent 燈號、主機、context 用量、最後一行狀態、工作簿按鈕），以及設定：把這個 session 放進或移出任務編組、新增任務編組、改名稱（也可以雙擊編組膠囊直接改）、改顏色。

### Changed：B 線監控（資料層，畫面還沒換）

- **監控分頁組的資料準備（#3768、#3771、#3772、#3773、#3775、#3776、#3777）**：job 帶出用的是重名額還是輕名額；`/api/info` 的 `wwd` 讓 App 對得上主機與它的 wwd；資源池主機的順序與外觀改存在 Profile Sync 的 settings 區（所以 settings 版本升到 13）；App 啟動時把舊的主機外觀一次搬過去（排在 Profile Sync 之前，最多等 10 秒，連續 3 次逾時就放棄）；主機狀態改成「正常／wwd 沒回應／主機沒回應／不明」四種。側欄與監控分頁的畫面要等之後的版本才換。

### Docs

- **監控分頁組與側欄主機列的 spec 與 plan（#3765）**。

## [1.0.0-alpha.767] - 2026-10-20

> daemon、`pdx` CLI、`wwd` 與 SPA 有改；**mod 沒有改，不用重跑 `pdx setup`**。依賴沒變，不用 `pnpm install`。Electron 與 766 相同。資料庫結構沒變（host schema 9、queue schema 9），wire 的 `proto` 仍是 3。
>
> **主機設定**：mlab 的 wwd 設定 `[worker] gocache_max_mb` 改為 6144（原本用預設 10240）。wwd 的 go 任務有自己一份 Go 建置快取，超過上限且沒有 job 在用時會整份清空；mlab 磁碟偏緊，先把上限降低。改成依「多久沒用」修剪另由 #3748 處理。
>
> **worker**：部署時各台的 worker 先一律關閉，檢查通過後由 lead 打開 mlab 與 air26；mserv、air19 仍關著。

### Fixed：wwd 的工作目錄與紀錄

- **clone 缺 base 時先 fetch 一次（#3738，#3730）**：job 的 base 是 clone 上次 fetch 之後才推上來的 commit 時（例如發版閘門在推送後幾秒就開跑），worker 先對 clone 的 origin fetch 一次（最多 30 秒），以 base 是否已在 clone 裡為準；不再直接退回 tar，所以同一個任務的工作目錄不會因為開跑的時間點不同，時而有 `.git`、時而沒有。
- **本機快速通道記實際的工作目錄（#3742，#3735）**：`pdx test` 在本機快速通道跑的 job，紀錄的 `snapshot.tree` 改成登記當下工作目錄的 tree（乾淨時等於 HEAD 的 tree），以前一律記 HEAD 的；等過鎖或名額的 run 在開始時重算。算不出來時 tree 留空，原因寫在報告的 notes。submodule 內未提交的改動目前不在 tree 裡（#3743）；每次約多 0.5 秒（#3745 會降低）。

### Fixed：未宣告指令清單

- **看穿閘門 shim 的 `pdx gate`（#3746）**：被閘門接手的重指令，以前在清單上一律顯示成 `pdx gate`；現在 signature 取 `pdx gate [旗標] -- <指令>` 裡的指令（例如 `go test`），查詢用的子命令（`would-wait` 等）不解包。另修正 `eval` 裡 `bash -c "…"` 的腳本被當成引號片語的問題。

### Changed：B 線任務編組檢視

- **上方分頁列只在目前群組內（#3741，TG-4a）**：分頁列、⌘1–9、上一個／下一個分頁都只在目前選的群組裡動；關掉一個分頁後顯示同群組的下一個。
- **分頁列照群組排列（#3744，TG-4b）**：lead 在最前，接著是各成員和它的分頁，最後是一般分頁；不再畫 team 框，收合的 team 也列出全部分頁，空群組會顯示提示。
- **分頁列可以拖曳（#3750，TG-4c）**：任務編組檢視的上方分頁列恢復拖曳。

### Docs

- **wwd 主機指標補齊的 spec 與 plan（#3747）**：G 項（series 帶出現有欄位、CPU 細項、磁碟 IO、行程總數等），實作進行中。

## [1.0.0-alpha.766] - 2026-10-20

> daemon、`wwd` 與 SPA 有改；**mod 沒有改，不用重跑 `pdx setup`**。依賴沒變，不用 `pnpm install`。Electron 與 765 相同。
>
> ⚠️ **wwd 的資料庫升級：queue schema 8 → 9**（#3733，`jobs` 多 `started_at`）；host schema 仍是 9，wire 的 `proto` 仍是 3。部署前備份 `wwd.db.pre-766`。要退回 765：停 wwd → 把 `wwd.db.pre-766` 放回 `wwd.db`（刪掉旁邊的 `-wal`、`-shm`）→ 給新 epoch → 啟動 765；備份之後的工作與 `PUT /v1/worker` 的設定會遺失（765 的 wwd 會拒絕開 queue schema 9 的資料庫）。
>
> 這是 765 轉換版之後第一個在 job 上加欄位的版本：四台 wwd 與兩台 Purdex daemon 都已是 765，所以舊讀者會忽略新的 `started_at`。
>
> **入口已打開（S2）**：765 部署後，mlab 與 air26 的 mod 設定 `pdx.json` 已設 `entry: "on"`，Bash 裡的 `go test …`、`pnpm vitest run …` 會依 `.wwd/tasks.toml` 改寫成 `pdx test <task>`。這是主機設定，不在這一版的程式裡。
>
> **worker**：部署時各台的 worker 先一律關閉，檢查通過後由 lead 打開 mlab 與 air26；mserv、air19 仍關著。

### Changed：wwd 監控資料

- **job 記下開始執行的時間（#3733，MD-5）**：`jobs.started_at`（origin 的時鐘，毫秒）在 job 由 claimed 變成 running 時寫入；退回 queued 時清掉；origin 的時鐘回撥時不會早於 claim 的時間。`/api/pool/jobs` 與單筆 job 帶出這個欄位。

### Fixed：未宣告指令清單看得出真正跑的是什麼

- **看穿 Claude Code 的 zsh 包裝（#3734）**：Claude Code 每條 Bash 指令都包成 `source 快照 && export … && setopt … && eval '<指令>'`，以前清單把它們全歸成 `setopt NO_EXTENDED_GLOB NO_BARE_GLOB_QUAL`；現在 signature 是 `eval` 裡第一個真正的指令（例如 `go test`）。引號內的 `&&`、`;` 不再被當成分隔；去掉引號後重新遮罩一次，避免分段的秘密被拼回來。
- **被遮罩的 shell 改用最重的子行程命名（#3739）**：含 `$(`、`` ` ``、`${`、heredoc 的腳本為了不外洩秘密會整段遮罩，以前 signature 只剩 `zsh`；現在改用同一棵行程樹裡、路徑上全是 shell 或包裝指令（`sudo`、`time`、`nice`、`nohup`、`env`）的第一個非 shell 行程中，子樹累計 CPU 最多的那一個。遮罩本身沒有放寬。每次取樣在 50 棵樹、半數根是 shell 時約多 7.7 ms。

### Fixed：測試

- **dev 模組的 daemon 檢查測試自建 git repo（#3731，#3729）**：在 wwd job 的工作目錄沒有 `.git`（clone 還沒有 base、退回 tar）時測試不再失敗；測試隔離行程帶的 `GIT_*` 變數。`git log -1` 失敗或沒有輸出時，daemon 檢查會在日誌寫下原因。

### Changed：B 線任務編組

- **群組重新排序（#3728，TG-3e-1）**：排序決定寫回各群組的 rank，必要時整批重排。
- **群組列的拖曳改用 dnd-kit（#3732，TG-3e-2/3）**：與工作區共用側欄的 DndContext，刪掉原生拖放；工作區分頁的關閉鈕按住拖曳不會再啟動整列拖曳。

## [1.0.0-alpha.765] - 2026-10-20

> daemon、`pdx` CLI、`wwd` 與 SPA 有改；**mod 沒有改，不用重跑 `pdx setup`**。依賴沒變，不用 `pnpm install`。Electron 與 764 相同。
>
> ⚠️ **wwd 的資料庫升級：host schema 8 → 9**（#3706，`worker_settings` 多 `disk_min_gb`）；queue schema 仍是 8，wire 的 `proto` 仍是 3。部署前備份 `wwd.db.pre-765`。要退回 764：停 wwd → 把 `wwd.db.pre-765` 放回 `wwd.db`（刪掉旁邊的 `-wal`、`-shm`）→ 給新 epoch → 啟動 764；備份之後的工作與 `PUT /v1/worker` 的設定會遺失。
>
> ⚠️ **這一版是轉換版（#3710，#3713）**：wwd 之間、Purdex 讀 wwd 的回應、推送與落盤紀錄，改成容忍不認得的欄位（`wire.DecodeLenient`，必填與值域照驗）；本機的 PUT／POST 本文維持 strict。轉換版本身沒有加任何需要容忍才安全的新欄位。等四台 wwd 與兩台 Purdex daemon 都升到 765 以上，之後的版本才可以在 job、facts、pool 上加欄位；之後就不能退回 765 以前（`docs/wwd-deploy.md` 有這條規則）。
>
> **部署順序**：每一台先升 Purdex daemon，再升 wwd；四台 wwd 一起升。原因是 #3706 的新欄位在磁碟偏低時才出現，而 765 以前的讀者是 strict。
>
> **wwd 自己檢查磁碟下限（#3702，#3706）**：work root 所在的卷可用空間低於 `[worker] disk_min_gb`（預設 10 GB，0＝關；可用 `PUT /v1/worker` 即時改）時，heavy 的 headroom 變成 0，理由是 `disk_low`，light 不受影響；預設值也套用到原本沒有 gate 的 mserv、air19。這是移除舊 gate（WWD-9）的前提。App 的理由文字是「磁碟剩餘空間低於下限」（#3704）。
>
> **入口切換（S2；入口本身仍是關的）**：
> - `go test ./...` 這類遞迴樣式不再被改寫成本機快速通道的任務：`./...` 對到發版任務 `go-all`，其他遞迴樣式對到 `go-changed`，都排佇列；`-count`、`-v`、`-p`、`-parallel`、`-timeout`、`-json`、`-failfast` 這些只講「怎麼跑」的旗標不再擋比對（#3711）。
> - 發版閘門改跑 `pdx test`：`scripts/release-gate.sh <commit>` 在專用的乾淨 clone 裡把所有發版任務送進佇列，印出一張表；新增 `wwd-linux`、`wwd-linux-vet`（mserv 上線前先 `--skip`）與五個腳本測試任務（#3708，`docs/wwd-deploy.md` §14）。765 的閘門就是用它跑的。
>
> **監控資料（WWD-9 之前的缺口，plan #3712）**：`/api/info` 多帶本機 wwd 的 `{host_id, alias}`，能力 `pool.identity.v1`（#3717，MD-1）；告警的讀取路由 `GET /api/pool/hosts/{alias}/alerts` 與 host event `alerts.changed`，失聯告警分成「wwd 沒有回應」與「主機離線」，能力 `pool.alerts.v1`（#3719，MD-2）；觀察紀錄的讀取路由 `…/observations`、`…/undeclared`，能力 `pool.observations.v1`，裝置 token 不能讀（#3724，MD-3）；`/api/pool/jobs` 與單筆 job 多一個 `slot`（heavy／light）（#3715，MD-4）。
>
> **B 線：任務編組**：側欄改成每個分頁都經由所屬群組列出，群組有標題列並可從側欄選取，新開的分頁會加入目前選取的群組，視圖也會記住各裝置的選擇並跟著使用中的分頁（#3709，#3716，#3718，#3722）。
>
> **worker**：部署時各台的 worker 先一律關閉，檢查通過後由 lead 打開 mlab 與 air26；mserv、air19 仍關著。

### Changed：wwd 磁碟下限與入口切換

- **wwd 自己檢查磁碟下限（#3706）**：host schema 9，`[worker] disk_min_gb`，低於下限時 heavy 的 headroom 為 0、理由 `disk_low`。
- **App 顯示 `disk_low` 的理由文字（#3704）**：「磁碟剩餘空間低於下限」。
- **遞迴樣式不交給本機快速通道（#3711）**：`./...` 對到 `go-all`，其他遞迴樣式對到 `go-changed`；只講怎麼跑的旗標不再擋比對。
- **發版閘門改跑 `pdx test`（#3708）**：`scripts/release-gate.sh`、`wwd-linux`、`wwd-linux-vet` 與五個腳本測試任務。

### Changed：wwd 監控資料

- **`/api/info` 帶本機 wwd 的身分（#3717）**：`{host_id, alias}`，能力 `pool.identity.v1`。
- **job 帶 `slot`（#3715）**：`wire.JobSlot` 與 Purdex 的 PoolJob 型別。
- **告警給 App 讀（#3719，MD-2）**：`GET /api/pool/hosts/{alias}/alerts` 回 `{host, known, alerts}`，失聯告警多一個 `silence: wwd_down | host_down | unknown`；Purdex 每 30 秒輪詢各台、本機的 `alert` 事件提早重讀，內容變了才發 `alerts.changed {host}`；能力 `pool.alerts.v1`。
- **觀察紀錄給 App 讀（#3724，MD-3）**：`GET /api/pool/hosts/{alias}/observations?since=&until=` 與 `/undeclared?since=`，回 wwd 的原樣；`next_until` 原字串帶回；指令列可能帶秘密，裝置 token 回 403；能力 `pool.observations.v1`。

### Changed：wwd wire（轉換版）

- **讀取端容忍不認得的欄位（#3713，#3710）**：wwd 之間、Purdex 讀 wwd 的回應、推送與落盤紀錄改用 `wire.DecodeLenient`，必填與值域照驗；本機 PUT／POST 本文維持 strict。AST 守門測試限定 strict 只能用在白名單裡的請求本文。四台 wwd 與兩台 daemon 都升到 765 以後，新欄位才可以加。

### Internal

- **docs（#3712，#3714）**：wwd 監控資料缺口的 plan；WWD-9 保留 watch 的 wake／status 到 8b。
- **測試在 wwd job 裡也正確（#3725）**：unix socket 測試改用 `/tmp` 下的短目錄（job 的 TMPDIR 太長）；檢查自身標記的測試依自己的環境判斷；起 helper 時拿掉外層的 `WWD_JOB`、`WWD_ORIGIN`。765 閘門第一次經 wwd job 跑 go-all 時這三個是紅的。

### Changed：介面（B 線）

- **側欄依群組列出每個分頁（#3709）**：「未分組」也列出不是 session 的分頁；team 底下多出成員的第二個分頁與釘選分頁的列。
- **任務編組檢視記住選取與展開（#3716）**：存在本機、不同步；某台主機不再列出某個 team 時，對它的記憶一併清掉。
- **新分頁落在選取的群組最後（#3718）**：在任務編組檢視選了某一組之後才有；工作區檢視、窄側欄、分頁放左邊與選「未分組」時不變。
- **群組標題列（#3722）**：數字換成展開箭頭；點標題選取那一組並切到它最後看的分頁；空的新群組顯示「拖曳 session 到這裡」；側欄的視圖切換回到上次的位置。
- **群組標題的操作（#3726）**：點已選取的群組標題會切換展開；手動群組的標題右鍵可以重新命名、改顏色、刪除；任務編組檢視底部的 + 改成「新增任務編組」。

## [1.0.0-alpha.764] - 2026-10-20

> daemon、`pdx` CLI、`wwd` 與 SPA 有改；**mod 沒有改，不用重跑 `pdx setup`**。依賴沒變，不用 `pnpm install`。Electron 與 763 相同。
>
> **資料庫沒有變**：host schema 與 queue schema 都還是 8，wire 的 `proto` 仍是 3。部署前照常備份 `wwd.db.pre-764`；要退回 763 只要換回舊的執行檔。
>
> **快速通道改拿 wwd 自己的名額（#3698，#3700）**：本機的開發測試（單一套件的 `go test`、`vitest run <檔案>`）以前跟舊的 pdx gate 拿 light 名額；現在改在 wwd 登記時一起檢查名額，沒有名額回 429，呼叫端每 2 秒重問，跟鎖的等待共用同一個期限。宣告的任務用 light、臨時指令（`--adhoc`）用 heavy 並跟佇列工作共用；記憶體壓力到危險才擋 light。`facts.worker.running` 現在會算進這些執行。這是移除舊 gate（WWD-9）的前提。
>
> **worker 的 Go 編譯快取共用（#3669，#3695）**：每個 Go 工作以前都從零編譯；現在同一個 Go 版本共用一份快取（每台主機一份、只留最新兩個版本、上限預設 10 GB，`[worker] gocache_max_mb`）。測試結果不跨工作重用：拿到共用快取的工作會把 `-count=1` 併進 `GOFLAGS`。
>
> **入口切換的準備（S2；入口本身仍是關的）**：
> - purdex 的 manifest 把 `[entry] mode` 改成 `rewrite`，並新增發版任務 `e2e`（#3688）；`docs/wwd-deploy.md` §13 寫明怎麼打開一台主機的入口。
> - 入口只在「現在有連得上、worker 開著、標籤齊全的主機」時，才把指令改寫成排佇列的任務；沒有就照原指令執行（#3696）。避免其他 repo（例如 aigora-v2）的測試被改寫成沒有主機能接的工作。
>
> **其他 wwd 修正**：每台 Mac 都讀主人是否在用機器，沒有主人規則時只回報、不計入（#3682，#3692）；App 的主人規則編輯器因此也會出現在 mlab、air19 等 Mac 上，預設不啟用。舊版 wwd 對 `GET /v1/exceptions` 回 405 時，Purdex 視為「不知道」而不是錯誤（#3681，#3690）。git 2.53（Linux）的 ref lock 訊息也會重試（#3694）。e2e 測試裡的 wwd 不再讀到主機真的 pdx gate（#3693，只改測試）。
>
> **B 線：任務編組改成隨 profile 同步、可跨主機的手動群組**（各張見下方「介面（B 線）」）。⚠️ 資料形狀升級：settings ordinal 11 → 12、tabs ordinal 4 → 5。**舊版 App 視窗在第一次寫入新形狀之後，整份 profile 會停止同步，重新載入就恢復**；iOS 不受影響。另新增一個只存在本機的 localStorage key `purdex-task-group-roots`，不進同步。
>
> **worker**：部署時各台的 worker 先一律關閉，檢查通過後由 lead 打開 mlab 與 air26；air26 的主人規則照使用者 10-19 的指示維持關閉；mserv、air19 仍關著。

### Changed：wwd 切換上線（S2 準備、S4 前置）

- **快速通道改拿 wwd 的名額（#3698，#3700）**：登記本機執行時一起檢查名額，沒有名額回 429 `SlotBusy`，呼叫端每 2 秒重問、與鎖等待共用期限；不再占舊 gate 的 light。`facts.worker.running` 算進佇列工作與本機執行；worker 認領前先預留 heavy。
- **purdex 的入口改 `rewrite`，新增發版任務 `e2e`（#3688）**：manifest 單獨不打開任何東西，主機的入口要在 mod 的 `pdx.json` 設 `entry`（`docs/wwd-deploy.md` §13）。
- **入口只在有主機接得了時才改寫成排佇列的任務（#3696）**：讀本機的資源池，需要連得上、worker 開著、標籤齊全的主機；沒有就照原指令執行，讀不到資源池也一樣。

### Changed：wwd worker

- **Go 工作共用編譯快取（#3695，#3669）**：每個 Go 版本一份、每台主機一份、只留最新兩份，上限 `[worker] gocache_max_mb`（預設 10240）；共用快取的工作把 `-count=1` 併進 `GOFLAGS`，測試結果不跨工作重用。

### Fixed：wwd

- **每台 Mac 都讀主人是否在用機器（#3692，#3682）**：沒有主人規則時只回報、不計入；有讀數的主機接受 `owner_rule`，`[owner] enabled` 只給起始值；工作的用量紀錄只在規則生效時記主人。
- **舊版 wwd 的 `GET /v1/exceptions` 回 405 視為「不知道」（#3690，#3681）**。
- **git 2.53 的 ref lock 訊息也會重試（#3694）**：`fetching ref … failed: reference already exists` 在強制 refspec 下只會是 lock 被佔。

### Internal

- **e2e 的 wwd 不讀主機真的 pdx gate（#3693）**：harness 帶 `PDX_GATE_DIR` 指向不存在的目錄。

### Changed：介面（B 線）

- **手動群組的儲存與同步（#3670，#3674）**：含主機改指向、刪除時的成員改寫與兩台裝置收斂。
- **分頁記錄所屬群組與群內排序（#3680，#3684）**。
- **側欄改讀新的群組資料（#3686，#3689，#3697）**：成員以主機與對話根判定，不再按主機分區，可跨主機拖曳，列選單可加入任何群組；root 快取搬出 React（本機 localStorage `purdex-task-group-roots`）。
- **刪除舊的各主機任務編組程式碼（#3699）**。
- **釘選的團隊成員分頁也顯示團隊面板（#3701）**。
- **有效視圖、分頁歸屬與群內排序三個純模組（#3703）**：本版尚不改畫面。

## [1.0.0-alpha.763] - 2026-10-20

> daemon、`pdx` CLI（含 `pdx gate`）、`wwd` 與 SPA 有改；**mod 沒有改，不用重跑 `pdx setup`**。依賴沒變，不用 `pnpm install`。Electron 與 762 相同。
>
> **資料庫沒有變**：host schema 與 queue schema 都還是 8，wire 的 `proto` 仍是 3。部署前照常備份 `wwd.db.pre-763`；要退回 762 只要換回舊的執行檔。
>
> **修好 gate 的 shim 無限遞迴（#3671，#3677）**：10-20 早上 mlab 出現 `pdx gate --auto --tool go` 一層套一層呼叫自己，兩分鐘長到上千個行程、吃掉約 13 GB 記憶體。原因是 gate 用 `$HOME` 找 shim 目錄與狀態目錄，HOME 被換掉時排除不到真的 shim。現在 shim 以檔頭辨識、目錄改用帳號的 home，而且同一個工具疊到第 8 層就以 127 停下，不再往下開。設定檔的位置仍跟 `$HOME`（#3679）。
>
> **wwd 切換上線的障礙清掉一批（使用者 10-20 定案：不與舊系統雙軌）**：
> - worker 沙盒裡的 Go 工作讀得到私有模組（#3663，#3672）：worker 在沙盒外用自己的身分把模組先下載到一份共用、唯讀的快取，工作只讀它，帳密不進沙盒；固定 `GOTOOLCHAIN=local`、`GOWORK=off`。
> - 「所有主機都夠新才寫新欄位」的判斷，不再因為一台離線的主機而永遠不成立（#3678）：離線的主機看它最後一次回報的版本，並存在 `peers-last.json`，重啟後也讀得回來。通過測試摘要因此會照常寫出，資格比對才起得來。
> - 分得出主機停止還是 wwd 停止（#3676，#3683）：讀不到某台的 wwd 時，對它的 22 埠做 TCP 存活探測（不登入、不讀資料）；失聯告警分成「wwd 沒有回應（主機還在）」與「主機離線」，狀態一變就再推一次。新欄位只給主動要的讀者（`GET /v1/pool?liveness=1`）。
> - 對照主機的標籤改讀 clone 裡 `origin/main` 的 manifest（#3658，#3673），每 10 分鐘 fetch 一次；比對的快照也用同一個 commit。air26 的 wwd 設定加上 `[manifest_refs] purdex = "origin/main"`。
> - `spa-related` 跟 `tsc` 一樣，改成只給持有 `task:purdex/spa-related` 的主機接（#3668）；`docs/wwd-deploy.md` §12 寫好 mserv 上線的步驟。
>
> **其他**：同時有兩台讀不到資源池時，各自退回推自己那份告警（#3610，#3661）；`cannot lock ref` 有限次重試（#3546，#3660）；C2 矩陣的測試修字（#3664）。B 線：任務編組的側欄列（#3662）與團隊面板一行模式的寬度（#3659）。
>
> **worker**：部署時三台的 worker 先一律關閉，檢查通過後由 lead 打開 mlab 與 air26；air26 的 `[owner]` 照使用者 10-19 的指示維持關閉；mserv 仍關著（等 S2 停用人工送法後開）；air19 已在 762 升級，這次若在線一併升。

### Fixed：gate

- **gate 的 shim 不再無限遞迴（#3677，#3671）**：shim 以檔頭辨識，shim 目錄與狀態目錄改用帳號的 home；同一個工具疊到第 8 層以 127 停下，不再 spawn。

### Changed：wwd 切換上線（S1）

- **worker 沙盒的 Go 工作讀得到私有模組（#3672，#3663）**：worker 在沙盒外先把模組下載到共用唯讀快取，工作只讀它，帳密不進沙盒；固定 `GOTOOLCHAIN=local`、`GOWORK=off`。
- **離線主機看最後一次回報的版本（#3678）**：存在 `peers-last.json`，重啟後讀得回來，「所有主機都夠新」的判斷不再被一台離線主機卡死。
- **分得出主機停止還是 wwd 停止（#3683，#3676）**：讀不到 wwd 時對 22 埠做 TCP 存活探測，失聯告警分成兩種並在狀態改變時再推一次；新欄位只給 `GET /v1/pool?liveness=1`。
- **對照主機的標籤改讀 `origin/main` 的 manifest（#3673，#3658）**：每 10 分鐘 fetch 一次，比對的快照也用同一個 commit；remote 取最長名稱前綴。
- **`spa-related` 只給持有 `task:purdex/spa-related` 的主機接（#3668）**：與 `tsc` 一致；`docs/wwd-deploy.md` §12 寫好 mserv 上線步驟。

### Fixed：wwd

- **ref 寫入等過 `cannot lock ref`（#3660，#3546）**：共用 clone 上的 ref 寫入有限次重試。
- **兩台同時讀不到資源池時各自推自己的告警（#3661，#3610）**。
- **C2 matrix 的 dry-run 說明不含 FAIL 字樣（#3664）**：`wwd-c2-matrix_test.sh` 恢復綠燈。

### Changed：介面（B 線）

- **一行模式的團隊面板寬度跟著內容（#3659）**：`min(區域寬, max(所有格, 只有 lead + 100px))`；full 模式不變，journal 卡片貼合實際寬度。
- **任務編組的側欄列（#3662，TG-0b）**：沒有分頁顯示的團隊座位也穿上 persona 列（暗色，點擊仍可開啟）；名稱後接分頁標題；群組標題加 focus ring。

### Docs

- **wwd 計畫：切換上線、不與舊系統雙軌（#3667）**：WWD-9 不再等 C3，S1→S4 順序寫入 `docs/plans/2026-10-18-wwd-plan.md`。
- **任務編組升為 workspace 層的 spec 與 plan（#3666）**：手動編組改隨 profile 同步（r5.1）。

## [1.0.0-alpha.762] - 2026-10-20

> daemon、`pdx` CLI、`wwd` 與 SPA 有改；**mod 沒有改，不用重跑 `pdx setup`**。依賴沒變，不用 `pnpm install`。Electron 與 761 相同。
>
> **資料庫：`wwd.db` 的 host schema 從 7 升到 8**（新增 `qualify_attempts` 表，記每個任務最近一次沒成功的資格比對，讓「失敗後 24 小時才重試」撐過重啟）；queue schema 仍是 8，Purdex 這邊沒有新表。部署前每台都會備份成 `wwd.db.pre-762`。**761 的 wwd 會拒絕 host schema 8 的資料庫，所以退回 761 時要照 `docs/wwd-deploy.md` §5：停掉 wwd 後還原 `wwd.db.pre-762`、換新的 epoch 再啟動，而且退版前隔離區必須是空的。** wire 的 `proto` 仍是 3。
>
> **資格比對（WWD-5b-2c）**：`pdx wwd qualify <repo>/<task>` 可以手動對一台主機發起資格比對，`--status` 看進行中與最近的紀錄（#3651）；facts 每次更新時也會自動檢查要不要發起（#3655）。只排給 worker 開著、有該 repo 與工具標籤、不是對照主機的主機。**purdex 的 `tsc` 改成只給持有 `task:purdex/tsc` 的主機接**（拿掉 `os:darwin`）；air26 是對照主機，不必比對就持有這個標籤。這一版部署後不會馬上起任何比對：mlab 的 wwd 沒有設定 `[repos]`，mserv 的 worker 關著。
>
> **任務宣告帶工具標籤（#3657）**：跑 Go 的任務要求 `tool:go1.26`，跑 pnpm 的任務要求 `tool:node24` 與 `tool:pnpm10`；PATH 上沒有這些工具的主機不會再領走任務然後失敗。版本寫死在 manifest，有測試比對 `go.mod`；升 Go 的順序寫在 `docs/wwd-deploy.md` §11。
>
> **air26 的 wwd 改用專屬的 clone**：`~/Assist/repos.noindex/purdex-wwd`，`[repos] purdex` 改指它，不再和輔測腳本共用同一份 clone，避免搶 ref 鎖（#3546）。
>
> **重啟前先問接力（#3590，#3653）**：開發頁的「Rebuild & 重啟」現在也會先讀接力數，有接力在進行就問「重啟後多半能接續，但正在交接的那一個可能被打斷。仍要重啟？」；設定頁的「重啟 daemon」改成同一句。daemon 本身不擋任何重啟。
>
> **其他**：主機別名不接受「.」與「..」（它們到不了資源池 API，#3645）；gate 關閉或巢狀呼叫時不再建 gate 目錄（#3650）。B 線：團隊面板的設定區標籤、資料夾列、別台的授權資料夾與成員燈號位置（#3652，761 驗收）；任務編組檢視不再把同一個團隊 session 在「未分組」列兩次（#3654）。
>
> **worker**：部署時三台的 worker 先一律關閉，檢查通過後由 lead 打開 mlab 與 air26；air26 的 `[owner]` 照使用者 10-19 的指示維持關閉；mserv 仍關著（#3549）；air19 若仍連不上就沿用舊版。

### Added：資格比對（WWD-5b-2c）

- **手動發起資格比對（WWD-5b-2c-1，#3651）**：新增 `pdx wwd qualify <repo>/<task>` 與 `--status [--json]`；`/v1/qualify` 只在本機 socket 提供，手動配對由 daemon 每 10 秒結算。
- **自動觸發資格比對，purdex 的 `tsc` 走 qualify（WWD-5b-2c-2，#3655）**：facts 更新時只通知專用 goroutine 去檢查要不要發起；`tsc` 拿掉 `os:darwin`，改要求 `task:purdex/tsc`，以 air26 為對照主機。

### Changed：任務宣告與部署

- **任務宣告帶工具標籤（#3657，#3546）**：跑 Go 的任務要求 `tool:go1.26`，跑 pnpm 的任務要求 `tool:node24` 與 `tool:pnpm10`，沒有這些工具的主機不再領走任務後失敗；版本寫死在 manifest，並有測試比對 `go.mod`。

### Changed：重啟前先問接力

- **開發頁與設定頁重啟前先問接力（#3653，#3590）**：「Rebuild & 重啟」先讀接力數，有接力進行就跳確認框；設定頁「重啟 daemon」文案改成同一句；daemon 本身不擋任何重啟，讀不到接力數也照常執行。

### Fixed

- **主機別名不接受「.」與「..」（#3645，#3615）**：`wire.ValidAlias` 排除這兩個值，因為它們在路徑中會被當成 dot segment 正規化，worker 與例外操作到不了 handler。
- **gate 關閉或巢狀呼叫時不再建 gate 目錄（#3650，#3649）**：這兩種直接放行改用不建目錄的鎖檔；全新目錄上的那一筆 decision 不再記錄。

### Fixed：團隊面板與任務編組（B 線）

- **團隊面板 761 驗收四項（#3652）**：設定區標籤顏色、資料夾列改為唯讀加編輯鈕、別台已套用的授權資料夾也列出、成員燈號移到第一行 agent 圖示右上角。
- **任務編組不再把同一個團隊 session 列兩次（#3654）**：`buildGroupView` 會認領團隊席位 session 的所有分頁，不再讓多出的分頁掉到「未分組」。
- **docs：記錄團隊面板驗收的規格變更（#3656）**：把 #3652 的改動補進姓名池 v2 規格 §9.5，純文件。

## [1.0.0-alpha.761] - 2026-10-20

> daemon、`pdx` CLI（含 `pdx gate`）、`wwd` 與 SPA 有改；**mod 沒有改，不用重跑 `pdx setup`**。依賴沒變，不用 `pnpm install`。Electron 與 760 相同。
>
> **資料庫：`wwd.db` 的 host schema 從 6 升到 7**（`exceptions` 表多了 `allow_json` 與 `flags` 兩個可為空的欄位，存 hold 的設定）；queue schema 仍是 8，Purdex 這邊沒有新表。部署前每台都會備份成 `wwd.db.pre-761`。**760 的 wwd 會拒絕 host schema 7 的資料庫，所以退回 760 時要照 `docs/wwd-deploy.md` §5 做三步：停掉 wwd 後還原 `wwd.db.pre-761`；替還原的資料庫換一個新的 epoch 再啟動；而且退版前隔離區必須是空的。** 備份之後才建的 hold 會跟著消失。同一個 `wwd.db` 不能同時跑兩個 wwd（#3630）。wire 的 `proto` 仍是 3。
>
> **hold：要一台主機安靜下來，改問 wwd（WWD-8 8b-3）**：新增 `pdx wwd hold <host> --for <時間> --reason <原因>`，可加 `--allow-class`／`--allow-repo`（只放行列出的工作類別或 repo）、`--release`（發版用：輕工作名額降到 1）、`--expect pressure`（壓力測試時暫停 mserv 的 PSI 推播）。指令會等到 facts 顯示這筆 hold 才算生效。worker 只接 hold 放行的工作（#3633）。**`pdx gate` 現在也會讀本機 wwd 的例外（#3641）**：只要本機有生效中的例外，heavy 指令就排隊等它結束；light 指令只等「也擋 light」的例外。讀 wwd 最多等 300 毫秒，讀不到就照常放行，結果快取 5 秒。已知限制：在同一台主機開了 hold 的人，自己經 gate 跑的 heavy 也會等到 hold 結束（#3642）。
>
> **依賴快取（#3620，#3634）**：宣告 `deps` 的任務（例如 `spa-related`）開跑前會拿到裝好的 `node_modules`。快取以 lockfile 內容、安裝指令、工具版本、平台與一份列舉的設定檔清單為 key；某台主機第一次遇到一個 key 時要花幾分鐘安裝，之後同一個 key 只要幾秒就複製好。新增設定 `[worker] deps_root`／`deps_keep`（預設 2）／`deps_store_max_mb`（預設 4096）；air26 磁碟較小，設成 `deps_keep = 1`。key 只看列舉的檔案，不追蹤 `yarnPath`、外掛或 composer scripts 引用的檔案，這是已知限制（#3639）。
>
> **標籤隔離（WWD-5c-2b，#3617）**：同一份工作在兩台主機上一成一敗、origin 記下不一致之後，標籤的擁有者會把那個標籤隔離起來。
>
> **mserv 的 wwd.slice 有上限了（#3623）**：安裝時寫入 `/etc/systemd/system/wwd.slice.d/10-wwd-limits.conf`（CPUWeight=50、CPUQuota=300%、MemoryMax=6G、MemorySwapMax=0、TasksMax=4096），再 `systemctl daemon-reload`，不重啟 wwd。每個值都可以在 wwd 的 `config.toml` 的 `[slice]` 段覆寫。mserv 的 worker 仍然關著（#3549）。
>
> **App（B 線）**：工作分頁補上歷史表格與篩選（WWD-7b1-2b）、工作詳情與 log（7b1-3a）、「⋯」選單（調整順序與取消，7b1-3b）；主機分頁新增「工作」區（7b1-4）；分頁的沙漏也會被自己 session 的資源池工作點亮（7b1-6）；監看的 1 小時／6 小時／24 小時改讀資源池的 1 分鐘序列（7a-5）；新增唯讀的 `GET /api/pool/hosts/{alias}/exceptions`（G4，#3614）。側欄只在任務群組檢視顯示人物頭像、預設頭像外觀與 CapStepper 的按鈕名稱也有修正（759 驗收）。
>
> **worker**：部署時三台的 worker 先一律關閉，檢查通過後由 lead 打開 mlab 與 air26；air26 的 `[owner]` 照使用者 10-19 的指示維持關閉；mserv 仍關著（#3549）；air19 若仍連不上，就沿用舊版。

### Changed：告警投遞

- **讀不到自己的 wwd 就不算發送者（#3643，#3631）**：`/api/info` 的 `wwdalerts.deliverer` 必須讀得到本機 wwd 才為 true，讀不到時回 false。

### Added：wwd hold（WWD-8 8b-3）

- **例外的 allow／flags 存進 wwd.db（8b-3 PR-A，#3629）**：`exceptions` 表多了可為空的 `allow_json` 與 `flags`，host schema 由 6 升到 7；POST 也開放帶這兩個欄位。舊版 wwd 會拒絕 v7 的檔案，退版要還原備份。
- **worker 依 hold 的 allow 篩工作（8b-3 PR-B，#3633）**：對外的 headroom 語意不變（有 hold 就是 0），worker 自己多算，只接 hold 放行的工作類別與 repo。
- **`pdx wwd hold`（8b-3 PR-D，#3638）**：`pdx wwd hold <host> --for <時間> --reason <原因>`，可加 `--allow-class`、`--allow-repo`、`--release`、`--expect pressure`；等到 facts 顯示這筆 hold 才算生效。
- **release hold 降輕名額、`pdx gate` 讀 wwd 的例外（8b-3 PR-C，#3641）**：有 `release` 旗標的 hold 讓輕名額降為 1；`pdx gate` 的 would-wait 與准入去問本機 wwd，讀不到就照常放行，結果快取 5 秒。

### Added：wwd 依賴快取與標籤隔離

- **宣告 `deps` 的任務拿到裝好的依賴（#3634，#3620）**：把依賴快取接進 `jobexec.Prepare`，命中就複製、沒命中才安裝一次再存；新增 `[worker] deps_root`／`deps_keep`／`deps_store_max_mb`。
- **spec §5 補上依賴快取的限制（#3640）**：寫明 key 只看列舉的檔案，不追蹤 `yarnPath`、外掛或 composer scripts 引用的檔案（純文件）。
- **標籤的主人在 origin 提供不一致時隔離標籤（WWD-5c-2b，#3617）**：每 60 秒讀自己與各 peer 的不一致清單，同一份工作一成一敗時隔離該標籤；沒有 schema 變更。

### Added：wwd 例外路由與 mserv slice

- **例外的唯讀路由（G4，#3614）**：wwd 新增 `GET /v1/exceptions`，Purdex 新增 `GET /api/pool/hosts/{alias}/exceptions`，舊版主機回 `known:false` 與空清單。
- **exceptions 讀取的 golden JSON（#3618）**：把上述答案存成 golden fixtures，供 App 的 fixtures-sync 守門比對（只動測試資料）。
- **mserv 的 `wwd.slice` 有資源上限（#3623，#3549 PR1）**：安裝時寫入 drop-in（CPUWeight=50、CPUQuota=300%、MemoryMax=6G、MemorySwapMax=0、TasksMax=4096），每項可在 `config.toml` 的 `[slice]` 覆寫。

### Added：WWD-7 App 的資源池與工作介面（B 線）

- **工作分頁的入口（WWD-7b1-2a 2/2，#3599）**：daemon 宣告 `jobs.v1` 且已連線時建立「工作」分頁，只建不刪。
- **分頁的排隊沙漏（WWD-7b1-6，#3612）**：自己 session 的資源池工作處於排隊、認領或執行中時，沙漏也會亮，與閘門的 waiters 合併成同一個沙漏。
- **1 小時／6 小時／24 小時改讀 1 分鐘序列（WWD-7a-5，#3616）**：主機分頁在這三個範圍改讀 `GET /api/pool/facts/series`，載入完成後圖表立刻重算。
- **工作分頁的歷史（WWD-7b1-2b，#3622）**：歷史改為表格，加上狀態、主機、repo 三個篩選，並補上跨分頁切換的 view memory。
- **App 讀取例外路由的新格式（#3628）**：`parseExceptionList` 改讀 `{host, known, exceptions}`，`known` 不是 `true` 或答案壞掉時視為不知道。
- **工作詳情與 log（WWD-7b1-3a，#3632）**：選一件工作後右側顯示摘要、失敗項目、log 與各項欄位。
- **主機分頁的「工作」區（WWD-7b1-4，#3635）**：在「資源」與「行程」之間顯示這台正在跑的、從這台送出排隊中的、以及最近在這台結束的工作。
- **工作詳情的「⋯」選單（WWD-7b1-3b，#3636）**：排隊中的工作可移到最前／最後、暫緩、恢復，未結束的工作可取消；位置變更帶 `host=<origin>`，避免落到別台的佇列。

### Fixed：介面修正（759 驗收）

- **CapStepper 的按鈕名稱（#3619，#3611）**：「調低／調高成員上限」的 accessible name 帶團隊名，多主機時再帶主機名。
- **沒有頭像時的預設樣子與點距（#3621）**：有角色沒圖用角色底色與白字首字，沒角色用中性圓；移除 `fallbackColor`；並調整團隊面板 subagent 點與 agent 圖示的間距。
- **側欄只在任務群組檢視用頭像（#3626）**：一般模式回到 758 之前的樣子，任務編組檢視才顯示頭像與角色名。
- **姓名池 v2 的 spec 補上 759 驗收的調整（#3627）**：側欄、頭像預設、團隊圖示與單行格子的規則寫回 spec（純文件）。

### Fixed：761 發版閘門抓到的問題

- **依賴快取查工具版本改走 execstat（#3646）**：`deps.go` 查工具版本的 `exec.Command` 改經 execstat 的 helper，讓 execstat 守門測試恢復綠燈。
- **不需要名額的指令不讀 hold、不撥 wwd（#3648，#3641）**：這類指令不建 gate 目錄、不讀 hold、不撥 wwd；`pdx test` 的 fast lane 也經同一個建構點讀 hold。
- **snapshot 截斷測試改用小的 socket 緩衝（#3647）**：不再依賴主機的緩衝大小（只動測試）。

## [1.0.0-alpha.760] - 2026-10-20

> daemon、`pdx` CLI、`wwd` 與 SPA 有改；**mod 沒有改，不用重跑 `pdx setup`**。依賴沒變，不用 `pnpm install`。Electron 與 759 相同。
>
> **資料庫：`wwd.db` 裡 queue 的 schema 從 7 升到 8**（新增 `task_outcomes` 與 `outcome_disagreements` 兩個表，遷移可以重跑）；host schema 仍是 6，Purdex 這邊沒有新表。部署前每台都會備份成 `wwd.db.pre-760`。**退回 759 時要照 `docs/wwd-deploy.md` §5 做三步：停掉 wwd 後還原 `wwd.db.pre-760`；替還原的資料庫換一個新的 epoch 再啟動；而且退版前隔離區必須是空的。** wire 的 `proto` 仍是 3。
>
> **測試結果重用這一版才完整**：759 修好了認領資料，但結果比 log 早到時，建檔那一刻還沒有 log，結果就被擋下來；這一版改成 log 收下之後才補建（#3600）。部署後會在 air26 送兩次相同的宣告任務驗收：第二次要直接重用。
>
> **結果不一致開始記錄（WWD-5c-2a）**：每件可比較的工作結束時，origin 會在同一個交易裡記下結果；同一份工作在兩台主機上一成一敗時，記一筆不一致。這一版只記錄、只提供唯讀的 `GET /v1/outcomes/disagreements`，**還不會隔離任何主機的標籤**（那是 5c-2b）。
>
> **告警投遞（WWD-8a-3c）**：同一台主機不通，多台 wwd 會各自升起 `silence:<主機>`，現在算成同一個故障、只推一次（#3596）。primary 讀不到自己那台的 wwd 時，`push_ready` 會變成 false，由 standby 接手推播，primary 自己也不再推，避免雙送（#3605）。已知缺口：primary 和 standby 同時讀不到時，兩邊都不推（#3610）。推播仍預設關閉。
>
> **daemon 少 fork `ps`**：建 peers 清單時，只有 `ps` 讀得出啟動參數的行程（例如啟動 prompt 含中文的 session）改用 60 秒快取，不再每則訊息都 fork 兩次（#3602）。
>
> **其他**：已轉送、但還沒註冊的 spawn 被撤回時，成員主機會把它停掉，不會留下孤兒成員（#3537）；App 會顯示 wwd 離線（WWD-8a-3b）；WWD-7a／7b1 的資源池與工作畫面持續補齊（B 線）。
>
> **worker**：部署時三台的 worker 先一律關閉，檢查通過後由 lead 打開 mlab 與 air26；air26 的 `[owner]` 照使用者 10-19 的指示維持關閉；mserv 仍關著（#3549）；air19 連不上，沿用 757。

### Added：wwd 結果記錄與告警

- **結果不一致開始記錄（WWD-5c-2a，#3589）**：origin 在每件可比較的工作結束時，於同一個交易裡記下結果；同一份工作在兩台主機上一成一敗就記一筆不一致，並提供唯讀的 `GET /v1/outcomes/disagreements`。queue schema 由 7 升到 8，這一版還不會隔離任何主機的標籤。
- **進程內故障矩陣與 silence 告警去重（WWD-8a-3c-1，#3596）**：同一台主機不通時，多台 wwd 各自升起的 `silence:<主機>` 現在算成同一個故障、只推一次，並新增涵蓋各元件輪流停掉的矩陣測試。
- **本機 wwd 讀不到時由 standby 接手推播（WWD-8a-3c-2，#3605）**：primary 讀不到自己那台的 wwd 時 `push_ready` 變成 false，由 standby 推播，primary 不再推，避免雙送。

### Added：WWD-7 App 的資源池與工作介面

- **工作分頁的面板（WWD-7b1-2a，#3598）**：新增 `watch-jobs` 面板，每台主機一條泳道，排隊的工作依順序顯示等待時間與紅線；入口分頁在後續 PR，這張單獨看不到變化。
- **`jobs.changed` 接線與 jobs store driver（WWD-7b1-1b，#3571）**：收到 `jobs.changed` 只重讀那一件，並依錯誤種類換來源、重試或退避，重連後從 cursor 之後補讀。
- **主機分頁的「可接工作」列（WWD-7a-4a，#3583）**：顯示 worker 狀態、各類工作的接收情形與原因、owner 在不在與生效中的例外。
- **主機分頁的 5 分鐘歷史與區段色帶（WWD-7a-4b，#3595）**：打開主機分頁時一次讀入 wwd 的 5 分鐘歷史，圖表一開始就是滿的，並新增依區段上色的色帶與 `host_not_local` 處理。
- **「暫停」（WWD-7b1-5b，#3593）**：主機設定頁新增暫停：1、3、8 小時或自訂結束時間，可結束既有暫停，也可選擇輕型工作是否一併不接。
- **pool 停止輪詢（WWD-7a-1c，#3609）**：來源 daemon 宣告 `pool.events.v1` 時，App 改在事件、重連、視窗轉回可見時才讀，不再每 5 秒輪詢；讀取失敗仍自行重試。
- **App 的 wwd 離線標記（WWD-8a-3b，#3592）**：SSH 備援讀得到時，區分「連不上」與「數字停了」，格子左下角顯示斷線標記，文案與 aria 一併更新。

### Added：團隊介面

- **團隊圖示的樣式與顏色（#3597）**：團隊圖示可以單獨設定 weight 與顏色（沒設時跟著 profile 顏色），並拿掉側欄切換鈕的粉紅外框。
- **頭像圓形底色與縮小團隊面板的頭像（#3604）**：頭像墊上圓形底色，透明背景的角色頭像在深色主題也看得清；縮小的團隊面板格子改畫角色頭像。

### Fixed

- **已 apply 的 forwarded spawn 被撤回時，成員主機會結束並停掉它（#3594）**：以前會留下孤兒成員；現在 op 還在跑就終止並釋放名字，已註冊完成的回 `too_late` 且不動成員。
- **測試結果重用補齊（#3600）**：結果比 log 早到時，改成 log 收下之後才建檔，避免結果被擋下來。
- **Worker 設定與「暫停」字串的參數括號（#3603）**：9 個字串用了單括號，畫面會原樣顯示花括號；改成雙括號，並新增掃描整份語系檔的守門測試。

### Changed

- **inventory pass 少 fork `ps`（#3602）**：只有 `ps` 讀得出啟動參數的行程改用 60 秒快取，每則訊息不再重複 fork。
- **QuotaStepper 的加減按鈕帶欄位名與行名（#3607）**：按鈕名稱變成「動作＋欄位＋是誰的」，並移除無人值守面板裡重複的一份實作。

## [1.0.0-alpha.759] - 2026-10-19

> daemon、`pdx` CLI 與 `wwd` 有改；**mod 只改了一份 skill 說明（`pdx-test` 的結束碼 5），hooks 沒變，但要跑 `pdx setup` 才會把新的說明裝到各 session**。SPA 有改，依賴沒變，不用 `pnpm install`。Electron 與 758 相同。
>
> **測試結果重用這一版才真的生效**：758 的寫入端一筆都沒寫進去——worker 在啟動時（第一筆 facts 之前）就把自己的標籤與指紋算好存起來，之後每次認領都帶那份凍結的快照，存結果時和主機現在的指紋永遠比不上（fail-closed，沒有給出錯的結果）。這一版改成每次認領當下現讀（#3582）。部署後會在 air26 送兩次相同的宣告任務驗收：第一次寫入一筆，第二次直接重用。
>
> **`pdx test` 送件時就判斷有沒有主機能接（#3586）**：pool 裡沒有任何主機持有任務的全部 labels 時，當場回結束碼 5 並列出缺的 labels，不再靜靜排到逾時；有主機持有、只是現在都接不了（worker 關、連不上、主人在用）時照常排隊，並印一行說明在等誰。
>
> **新的 capability**：`jobs.control.v1`（管理端取消 job、調整排隊順序）、`pool.events.v1`（daemon 每 5 秒讀本機 wwd，主機的 pool 資料變了就發 `pool.changed` 事件；App 看到這個 capability 就停止輪詢 `/api/pool/hosts`）。
>
> **wwd 告警新增「不通」兩條（WWD-8a-3a）**：`silence:<peer>`（某台 peer 連續 3 分鐘不回應）與 `partner_down`（本機 Purdex daemon 3 分鐘沒來讀告警）。部署時會在 mlab、air26 的 wwd 設定加 `[advisor] partner_check = true`，並在三台的 `[[peers]]` 裡把 air19 設成 `alert_unreachable = false`（air19 目前連不上，也不會常開）。推播仍預設關閉。
>
> **「主人在不在」只看生效中的 owner 規則（#3457）**：規則是空字串的主機，owner 讀數照樣顯示，但不再讓 headroom 歸零，worker 也不再讓位。測試用的 `owner_active_file` 只負責「取代讀數」（#3561：設了這個檔案時，真實的鍵盤活動完全不讀），C2 矩陣的案例 7 因此要求 air26 的 `[owner]` 開著、規則非空，precheck 會擋。
>
> **dev 模式的「重建 daemon」改走正常重啟（#3572）**：以前編完新的執行檔就直接原地 exec，不關模組、不寫 log；現在走和 `/api/daemon/restart` 同一條關機流程，log 會有 `dev rebuild requested by …`、`shutdown: … done, restarting` 與 `restart: exec path overridden to …`，重啟後跑的是剛編好的那個執行檔。重啟中仍不檢查是否有對話正在接力（#3590 追蹤）。
>
> **姓名池 v2 與團隊介面 v2 這一批全部到齊**（角色匯入與頭像授權、頭像加名字、配對紀錄、第一次存角色的改名數字），88 會另外給一份驗收清單。**第一次在姓名池存角色時，所有存活的對話會一次改名**（舊名字仍可送達，lead 的地址也會換）。
>
> **資料庫與 wire**：沒有 schema 變動（wwd queue 仍是 7、host 仍是 6，Purdex 沒有新表），wire 的 `proto` 仍是 3。`passed_digest`（5b-2w）只在 pool 裡每台主機都連得上、且 proto ≥ 3 時才寫；air19 連不上的期間不會寫，facts 也維持 v1 形狀。
>
> **worker**：部署時四台的 worker 先一律關閉，檢查通過後由 lead 打開 mlab 與 air26。air26 依使用者 10-19 的指示，wwd 的 `[owner]` 已關閉（使用中也照樣接一重兩輕），這是部署設定、不在程式碼裡；mserv 仍關著（#3549），air19 連不上，沿用 757。

### Added：姓名池 v2 與團隊介面 v2

- **角色匯入對話框與頭像授權（#3550）**：SPA 新增角色匯入對話框，並在介面中標示頭像的授權來源。
- **畫面上改稱「人」的名字（#3555、#3563、#3569）**：deck 的 peer 說明文字、狀態列的 peer 識別與改名彈窗，核准卡標籤、額度表與通知標題，以及團隊相關畫面（提示、空缺的 lead、已結束清單、工作簿標題與根目錄），都改為顯示角色名字。
- **記錄 lead／member 配對（#3556）**：spawn 註冊或核准 adopt 時，daemon 會記下這一組 lead 與 member 的關係。
- **第一次存角色的改名數字由 daemon 提供（#3568、#3580）**：新增 `POST /api/peers/rename-preview`，第一次在姓名池存角色時，提示裡的「會改名的對話數」改用 daemon 算出的數字。
- **姓名池 v2 規格（#3566）**：補上 lineage root 來源、角色的信任邊界，以及排隊那一行維持純文字的規則。

### Added：WWD-7 App 的資源池與工作介面

- **主機格與卡片讀取資源池（#3531）、Hosts 頁列出池中主機（#3543）、資源池主機頁（#3570）**：fleet 的主機格與卡片、Hosts 頁與每台主機的頁面，現在都讀資源池的資料。
- **工作（jobs）的讀取與衍生視圖（#3551、#3552、#3567）**：新增 jobs 的資料型別與寬鬆解析、`/api/pool/jobs*` 的讀取與每個來源各一份的 jobs store，以及 jobs 分頁的衍生視圖。
- **管理端取消工作與調整順位（#3553）**：admin token 可取消 job、查詢排隊位置，並公告 capability `jobs.control.v1`。
- **主機資料變動即時推送（#3576）**：新增 `pool.changed` 主機事件與 capability `pool.events.v1`，App 看到後不再輪詢 `/api/pool/hosts`。
- **池中主機的 worker 設定頁（#3574）**：在池中主機的「設定」頁可調整 worker 設定。
- **headroom 回傳值的黃金測試（#3575）**：固定非空的 `headroom_reasons` 與 load 的輸出格式。

### Added：wwd 結果重用與告警

- **`passed_digest` 寫入（WWD-5b-2w，#3554）**：摘要解析會填入 `passed_digest`，且只在池中每台主機都讀得到時才寫。
- **「不通」告警（WWD-8a-3a，#3562）**：新增 `silence:<peer>`（peer 連續 3 分鐘不回應）與 `partner_down`（本機 Purdex daemon 3 分鐘沒來讀告警）兩條規則。
- **`pdx test` 送件時判斷有沒有主機能接（#3586）**：沒有任何主機持有任務的全部 labels 時當場回結束碼 5 並列出缺的 labels；有主機持有但暫時接不了則照常排隊並說明在等誰。

### Fixed

- **測試結果快取的寫入端終於生效（#3582）**：每次認領時現讀 worker 的標籤與指紋，不再使用啟動時凍結的快照，修正 758 一筆都寫不進去的問題。
- **告警投遞狀態更準確（#3581）**：`wwdalerts` 只在自己的推播模組就緒時才回報有投遞者。
- **「主人在不在」只看生效中的 owner 規則（#3578）**：規則為空字串的主機不再讓 headroom 歸零。
- **`owner_active_file` 完整取代真實讀數（#3577）**：設了這個檔案時，不再讀真實的鍵盤活動。
- **快照取得方式依認領的來源決定（#3579）**：不再依 `Job.Origin` 選擇。
- **dev 模式的「重建 daemon」改走正常重啟流程（#3587）**：會寫 log、依序關閉模組後再重啟，重啟後跑的是剛編好的執行檔。

### Internal

- **run 的 alive 檢查把 `/proc` 讀取的 ESRCH 視為已結束（#3584）**，避免誤判。

## [1.0.0-alpha.758] - 2026-10-19

> daemon、`pdx` CLI 與 `wwd` 有改；**mod 沒有改，不用重跑 `pdx setup`**。SPA 有改，依賴沒變，不用 `pnpm install`。Electron 與 757 相同。
>
> **測試結果開始重用（WWD-5d）**：`pdx test` 送出宣告的任務時，會先查同一份程式碼、同一個任務定義、在有資格的主機上 3 天內跑過的結果；查到就直接回傳，不再重跑。要強制重跑時加 `--no-cache`。結果只在「做出結果當下主機沒有漂移」時才寫入，主機讀不到 facts 時一律不寫。
>
> **部署順序：先升兩台 Purdex daemon，再緊接著升四台 wwd。** 758 的 `pdx` 送出宣告任務時會帶新欄位 `def_hash`，757 的 wwd 會以 400 拒絕；這只發生在「同一台的 Purdex 已升、wwd 還沒升」的那幾分鐘。wire 的 `proto` 仍是 3，主機之間沒有格式變動。
>
> **資料庫**：`wwd.db` 裡 queue 的 schema 從 6 升到 7（jobs 加 `def_hash`、`claim_labels`、`claim_fingerprint`），host schema 仍是 6；Purdex 這邊沒有新表或索引。757 的 wwd 會拒絕開 queue schema 7，所以**退回 757 時要照 `docs/wwd-deploy.md` §5 還原 `wwd.db.pre-758`、換一個新的 epoch，而且退版前隔離區必須是空的。**
>
> **告警規則補齊大部分**：新增 pressure held、thrash、resident growth（記憶體）、mserv service（只在 Linux）與 orphans（開發用的孤兒行程與 fork 迴圈，只列出、絕不 kill）。告警照樣只寫入、手機推播仍預設關閉；`/api/info` 現在看得到投遞狀態（`wwdalerts`）。
>
> **`/team <名字>` 開始生效**：daemon 公告 `team.lead_name.v1`，在一般 session 輸入 `/team <名字>` 就會建立並綁定該名字的 lead（對不上或對上多個角色時走核准卡）。**第一次在姓名池存角色時，會一次替所有存活的對話改名**（舊名字仍可送達），88 會在使用者送測前另外說明。
>
> **worker**：部署時四台的 worker 先一律關閉；mlab 與 air26 由 lead 在部署檢查通過後打開。mserv 的設定這一版加上 `explicit_labels_only`（只接明確標給它的工作），但 worker 仍然關著，要等 #3549（`wwd.slice` 上限與人工送法停用）完成、守門員同意才開；air19 照舊不開。
>
> 部署後另外補了一個部署設定的缺口（不在程式碼裡）：air26 的 wwd 設定加了 `[tools]`（go）與 `[repos]`（purdex），要求 `repo:purdex` 的 `pdx test` 任務現在都會送到 air26 跑（#3546）。

### Added：wwd 結果重用（WWD-5d）

- **送件時先查已存的結果（WWD-5d-2a，讀取端）**：`pdx test` 送出宣告的任務時，daemon 會對本機與每台可達的 peer 查「同一份程式碼、同一個任務定義」是否已有結果；命中就讓 job 直接成為成功並標記 `cache_hit`，log 指向原本那個 job，不再重跑。條目必須來自被問的那台主機，讀不到或 peer 不可達一律當作沒命中。新增 `pdx test --no-cache` 強制重跑（不能與 `--list`、`--adhoc` 併用）；`def_hash`、`no_cache` 只走本機 socket，不進 job、不傳給 peer。已知限制：peer 的隔離狀態在 5c-2 之前尚未驗證（#3522）。
- **成功結果存進快取（WWD-5d-2b，寫入端）**：宣告任務第一次跑成功，就以「做出結果那次 claim 當下」worker 自述的標籤與指紋存一筆條目，之後同一個 snapshot 的送件就會命中。只有可快取、非本機、非 adhoc、非快取命中、沒被取代且 exit 0 的結果才會寫，且主機讀不到 facts 時一律不寫。**queue 的 schema 從 6 升到 7**（jobs 加 `def_hash`、`claim_labels`、`claim_fingerprint`），舊的工作這三欄是空的，其結果不會被存；回退舊版 wwd 要還原升級前的 `wwd.db` 備份，部署前先備份（#3548）。

### Added：wwd 告警規則（WWD-8a-2）

- **記憶體壓力與常駐成長三條規則（8a-2b）**：新增 pressure held（push 級，連續 5 個完整分鐘都在 warning 以上且 swap 增加至少 1 GB）、thrash（digest 級，compressor 超過 4096 MB 且解壓速率過高、持續 60 秒以上）與 resident growth，並含已知常駐行程的抑制。取樣不足、缺欄位或重播時一律「未判斷」，不誤報也不清除既有告警；Linux 上不適用的規則視為不成立（#3516）。
- **mserv service 規則（8a-2c-1）**：只在 Linux 上判斷（push 級）。PSI `some avg60` 連續兩個完整分鐘達 10 以上且最新讀數仍高，或最新一筆可用記憶體低於 1.5 GiB 就告警；`expect_pressure` 暫停只影響 PSI 這一半，低記憶體照報。同一張也更新了 spec 的 mserv service 與 orphans 兩列（#3538）。
- **orphans 規則（8a-2c-2）**：列出使用者自己的、ppid 為 1、啟動超過 2 小時且屬於開發路徑（worktree、scratch、pdx 暫存、測試用 wwd、playwright、codex-broker）的孤兒行程，以及 ppid 為 1 的 shell fork 迴圈。**只列出，絕不 kill**。為此 facts 的行程紀錄多了啟動時間，並新增只讀行程表的 `Fresh()`，一秒內多次呼叫也很便宜（#3544）。

### Added：wwd worker 與隔離

- **worker 的 `explicit_labels_only`（#3441）**：設定 `[worker] explicit_labels_only = true`（預設關閉）後，worker 只接標籤含 `os:` 或 `task:<repo>/<task>` 的工作，沒有標籤的舊 commit 工作一律不接。mserv 的設定這一版會開啟（#3533）。
- **沙箱 unit 帶來源標籤，隔離區處理同名 id（#3373 PR3b）**：job id 只在做出它的來源內唯一，所以沙箱 unit 改名為 `wwd-<來源標籤>-<job id>`（標籤是連線名稱 SHA-256 的前 16 個十六進位字元；沒有標籤時維持舊名）。隔離區現在同時認兩種 unit 名，`Clear` 遇到有歧義的 id 會拒絕，不會清錯。沒有 wire 或 schema 變動（#3528）。
- **來源標籤沙箱的 mserv 真機測試（#3529）**：新增兩個只在 mserv 上跑的情境（build tag `mserv && linux`，平常不跑）：unit 名稱與環境變數正確，以及兩個來源用同一個 job id 同時跑時各在自己的 unit。不驗 workspace 隔離，因為測試的路徑是 helper 依標籤命名的，證明不了正式碼；結果記在 `docs/wwd-4d3-mserv.md`（#3547）。

### Added：App 的 pool 資料層與任務編組（WWD-7a／NA-6c）

- **pool store 與主機視圖（WWD-7a-1b）**：新增 `usePoolStore`，每 5 秒向第一台已連線且宣告 `pool.v1` 的 daemon 讀 `/api/pool/hosts`；失敗時依類型換下一台、短暫重試或退避（2、4、8…最長 30 秒）並保留最後的資料，全部失敗則每台主機退回 fleet 顯示。畫面隱藏時不讀取、不計時。這一張沒有 UI，也沒有任何地方掛載，沒有 `pool.v1` 時畫面不變（#3512）。
- **右鍵把 session 加入、新建、移出任務編組（NA-6c-1）**：側欄右鍵選單多出對應的列，並有上限（最多 32 組、64 個 root、一個 root 只在一組、名稱 1–64 bytes）。寫入必須取得真正的 lineage root，拿不到就不寫、不拿 session 自己的 id 冒充；主機有讀不了的列或資料還在讀取時，列照樣出現但停用並說明原因（#3523）。
- **組名就地改名、清除空組、team 標題開編輯表單（NA-6c-2）**：手動組名稱雙擊（或 Enter／Space／F2）就地改名；沒有在線 session 的組可「清除」，這是破壞性操作，畫面確認、寫入前重讀在線 session、寫入時再驗，三道防線任一不成立都拒絕（#3527）。
- **拖曳 solo session 到任務組或「未分組」（NA-6c-3）**：只能拖到同一台主機的手動組；主機的編組不能編輯或 root 還在讀取時，放下仍被接住但不寫入，並以通知說明原因。team 的席位不可拖（#3534）。

### Added：team 與姓名池

- **lead 在核准時選名字（NP-4c C1）**：核准 lead 申請時可帶 `lead_name`（3–12 個小寫英文字母）。流程是先改名、關閉申請，失敗或輸掉競爭就還原改名；名字被占用回 409 `name_taken` 並附目前清單，申請維持開啟。核准流程以 per-approval 鎖串行化（#3521）。
- **`/team <名字>` 開始生效（NP-4c C2）**：daemon 公告 `team.lead_name.v1`；人員的 token 可帶 `lead_name` 或核准卡 `card`（兩者互斥），綁定以 token 為準，請求自己改不了。名字對上就直接建立並綁定該名字的 lead，對不上或對上多個角色時走核准卡。第一次在姓名池存角色時，會一次替所有存活的對話改名（舊名字仍可送達）（#3536）。
- **本機 spawn 受理時預訂成員名字（NP-4 A2a）**：受理時先預訂名字，`pdx spawn` 在 stderr 印一次「→ 名字（預訂）」。結束點（註冊完成、失敗、中止、被別人先結束、team 結束、資源卡被拒、啟動逾時）都會釋放預訂，每個結束點都有測試（#3524）。
- **遠端 spawn 帶成員名字（NP-4 A2b）**：lead 主機挑名字後立即釋放，只有成員主機持有預訂；名字隨 `spawn` 指令送出，舊版成員主機會忽略這個欄位。指令被拒、過期、出錯或重放時 op 已結束，都會釋放（#3532）。

### Added：App 的角色與指揮台

- **角色頁的正本主機與第一次存檔確認（NA-1d PR-B）**：新增正本主機下拉（只列已宣告 `personas.v1` 的主機，選擇只存在這台裝置、不同步），正本連不上時頁面以一行說明原因。第一次存角色一律先白話告知「至少 N 個現有的對話會改用角色名字，舊地址照樣送得到」，取消則不送出。第一次存的目的地改為正本主機（若宣告 `personas.v1`）（#3517）。
- **角色頭像選擇器（NA-1b-2c-1）**：來源可選首字母、六種 DiceBear 樣式、照片網址或上傳檔案；DiceBear 停手 400 ms 後才畫，照片網址要按「取得照片」才連線。開啟既有角色不會重畫，過時或離開頁面的繪製結果不會套到別的角色上。匯入對話框與頁尾授權在下一張（#3541）。
- **指揮台的說話開頭顯示角色頭像與名字（NA-3）**：對話的 agent 是角色時，回合的第一個說話開頭顯示頭像、名字、模型與時間，之後工具或思考之後再說話的開頭在文字上方多一行淡色的頭像與名字。不是角色的對話完全維持原樣（#3539）。

### Fixed：告警與 info

- **`/api/info` 補上 `wwdalerts`（#3526）**：推播開關是開是關、這台是不是投遞者、被壓下幾則，之前在 `/api/info` 完全看不到（只有模組內有 `Status()`，core 沒有去問）。現在回應一律有 `wwdalerts` 欄位，模組沒掛載或沒有 `Status()` 時為 `null`；補了「真模組註冊進 core 後 `/api/info` 看得到」的測試（#3535）。

### Changed：文件

- **WWD-7 設計的使用者答案**：Q1–Q6 寫進 WWD plan：worker 開關只在主機分頁設定；主機格右上角的數字暫時不動（7a-2 不加數字、7b1-4 不做數字與顏色，之後整體重新設計）；工作清單在守門員專區「工作」分頁；關 worker 時正在跑的跑完；只有取消正在跑的才需確認；spec 的 exception 在 App 叫「暫停」，時長 1／3／8 小時或自訂結束時間，App 字串不出現「例外」。純文件（#3557）。

## [1.0.0-alpha.757] - 2026-10-19

> daemon、`pdx` CLI 與 `wwd` 有改；**mod 有改（hooks 的 `member.js`／`register.js`，skills 的 `pdx-team`／`pdx-test`），要重跑 `pdx setup`**。SPA 有改，依賴沒變，不用 `pnpm install`。Electron 與 756 相同。
>
> **wire 的 `proto` 升到 3（WWD-5b-2r，讀取端）**：每台 wwd 與兩台 Purdex daemon 都認得 job 結果裡的新欄位 `summary.passed_digest`，`/v1/info` 回報 `"proto":3`；這一版沒有任何地方寫入這個欄位，寫入端在下一版。facts 的版本從這一版起不再等於 protocol 版本，仍然是 `v: 2`。**部署順序：先升兩台 Purdex daemon，再連續升四台 wwd。** 反過來的話，舊版 Purdex 會拒絕新版 wwd 的 pool 回覆，`/api/pool/hosts`、facts 歷史與 fleet 畫面會回「bad response」，直到 Purdex 也升級為止（見 `docs/wwd-deploy.md`）。
>
> **告警開始寫入，手機推播預設關閉**：8a-2a 的三條規則（磁碟空間不足、磁碟空間持續下降、wwd 反覆崩潰重啟）上線後，告警會寫進每台主機的 `wwd.db`，用 `GET /v1/alerts` 看得到。手機推播由 host 設定 `wwd_alerts.push` 控制，**預設關閉**，等使用者決定後才開；關著時不送任何推播，開關方式見 `docs/wwd-deploy.md` §7。
>
> **資料庫**：這一版沒有 schema 變動，`wwd.db` 仍是 host schema 6，Purdex 這邊也沒有新表或索引。部署前照樣備份四台的 `wwd.db`；**退回 756 時照 `docs/wwd-deploy.md` §5：還原 `wwd.db.pre-757`、換一個新的 epoch，而且退版前隔離區必須是空的。**
>
> **閘門**：wwd worker 在有閘門的主機上執行佇列工作時，會占用一個閘門的重名額（#3513），避免佇列工作與各線的互動測試同時把主機吃滿。
>
> **worker**：部署時四台的 worker 先一律關閉；mlab 與 air26 由 lead 在部署檢查通過後打開；mserv 要等 #3441 完成才開；air19 照舊不開。
>
> 下一版（758）預計帶 digest 的寫入端（5b-2w）；它出貨前，資格配對不會啟動。

### Added：wwd 資格與結果重用（WWD-5）

- **通過測試清單的 digest，只有讀取端（5b-2r）**：job 結果的 `summary` 多一個可省略的欄位 `passed_digest`（格式 `sha256:<64 位十六進位>`），`wire.Proto` 由 2 升到 3；facts 的版本另外拆成 `wire.FactsProto = 2`，所以 proto 3 的建置仍寫 `v: 2` 的 facts，不會被 proto 2 的主機拒收。資格比對時，兩邊都有摘要而任一邊缺 digest 一律判不一致，不退回只比數量。這一版沒有任何地方寫入 digest，寫入端（5b-2w）隔一版出貨。部署要讓四台 wwd 與兩台 Purdex daemon 快速依序升級，升版空檔裡 proto 2 的主機會把 proto 3 的 `/v1/info` 當成連不到的 peer，閘門暫時關閉（#3491）。
- **資格認證的協調器 `qualify.Coordinator`（5b-2b）**：負責替候選主機與 reference 主機各啟動一半同一個 snapshot 的測試，兩半都跑完才比對；一致就記下 earned 標籤並重新廣告，不一致或有一半沒跑完就作廢並取消另一半，自動重試要等工具鏈指紋改變或滿 24 小時，人工要求不受此限。已知限制：這一版是函式庫，不接任何線（有測試釘住沒有人 import 它），進行中的一對與重試等待只在記憶體，重啟會忘記；自動觸發的接線在 5b-2c，不得早於 5b-2w（#3494）。
- **漂移會撤銷 earned 標籤（5c-1）**：一個標籤只看 `os_version` 與它任務需要的工具；工具消失、當初沒記到或版本變了都算漂移，漂移時刪掉該標籤，並只刪這台自己做出的結果快取（不動 reference 主機做出、仍有效的條目），再重算一次廣告。不認識的任務不算漂移，已隔離的不再檢查；表只有個別壞列時照常處理，其他讀取錯誤則什麼都不撤銷。已知限制：5b-2c 之前沒有任何路徑授予 earned 標籤，所以這條撤銷路徑在正式環境暫時沒有東西可撤（#3500）。

### Added：wwd 告警（WWD-8a-2）

- **告警推播到手機的開關（8a-2a-0）**：hostconfig 新增 `wwd_alerts` 一列，內容恰好是 `{"push": bool}`，預設 `false`，以 `PUT /api/hostconfig/wwd_alerts` 設定，`GET /api/hostconfig` 多一個 `wwdAlerts` 欄位。投遞模組每次推播前都會讀它，關著、讀不到或解析失敗時一律不推播，也不寫嘗試時間與 ledger，所以打開的那一刻仍在作用中的 push 級告警會依原規則推出去；digest 級照收。`/api/info` 裡可看到目前是關是開與被壓下的告警數。每台 daemon 各存各的，投遞者是 primary 或接手的 standby，止血時兩台都要關（#3474）。
- **告警規則框架與三條規則（8a-2a）**：新增 `advisor` 的規則評估器，每 30 秒判一次：工作磁碟剩餘低於 12 GB（push 級，回到 12.5 GB 才清除）、磁碟空間持續下降（digest 級，剩餘低於 14 GB，或一小時內每小時少 2 GB 以上且照此速度六小時後會低於 10 GB）、wwd 一小時內啟動三次以上（push 級）。判斷不了時既不發告警也不清除，只記一次日誌。告警不放在 proto 2 閘門後，`GET /v1/alerts` 看得到；`alert` 事件只送本機 socket，不再送到 peer 的 TCP 串流。新增 `wwd alerts-replay --db <副本> --work-root <dir>`，只讀資料庫副本，用歷史資料重播規則，方便調門檻（#3504）。

### Added：wwd 與 pool

- **pool 的主機格資料（#3478 三張中的第 2 張）**：`GET /api/pool/hosts` 對有 facts 的主機多回 `headroom_reasons`（heavy 與 light 各自沒有餘裕的原因，依序為 `worker_off`、`zone_*`、`exception:<name>`、`owner_active`、`slots_full`、`gate_full`；空清單代表有餘裕）與 `load`（0 到 100 的綜合分數與最大的來源）；沒有 facts 的主機兩者為 null，能力為 `pool.assess.v1`。facts 的 wire 沒有改（#3496）。
- **圖表資料改由 wwd 就地裁切（#3478 三張中的第 3 張）**：wwd 新增唯讀路由 `GET /v1/facts/series`，擁有資料的那台主機直接回圖表點（最多 21,600 點，超過保留最新並標示截斷），一天的 1 分鐘資料約 300 KB，不再把 10 MB 的紀錄傳過 tailnet；Purdex 的 `GET /api/pool/facts/series` 先問這條路由，只有 wwd 回標準的 404 才退回舊的抓全部歷史再裁切，其他錯誤不會被當成退路重試。已知限制：舊版 wwd 沒有這條路由時仍走較貴的舊路徑（#3505）。
- **wwd worker 的佇列工作占用閘門名額（#3513）**：有 pdx 閘門的主機上，worker 每執行一件佇列工作就占用一個 heavy 名額，工作結束才釋放，讓閘門與 worker 只有一本帳；先前閘門 0/2 時 worker 領一件、閘門還會再放兩件進來，變成三件。閘門自己的放行條件（壓力、換頁速率與磁碟剩餘）優先，在擋新 heavy 指令時不領件。無法確認已停止而被隔離的工作會一直占著名額直到隔離解除，重啟後會先補回再領件。沒有閘門的主機不受影響（#3509 一併關閉）。
- **App 端 pool 資料層（WWD-7a-1a）**：新增 `lib/pool/`，包含 wire 型別、寬鬆解析（壞列整列丟棄、不認識的 zone 讀成 `unknown`）、`/api/pool/*` 的讀取、錯誤分類與代碼對應文字表，兩種語系都補了文字。目前沒有任何畫面使用，所以沒有 `pool.v1` 時畫面不變。已知限制：`headroom_reasons` 與 `load` 還沒有對照 #3496 的實際 fixture 驗證，計畫中提到的 10-19 真實快照不在 repo，沒有涵蓋（#3497）。

### Added：姓名池與 peers（NP-3／NP-4）

- **舊對話依角色改名並通知（NP-3c-3）**：角色池建立後，名字不是以角色代號結尾的存活本機對話（包含 `kenji` 這類內建名與 `purdex-88-b8` 這類帶 ref 的名字）會被改名：存活的 lead 取候選順序第一個，本機 lead 的成員挑與該 lead 配對過的名字，其他人挑新面孔。改名採比較後設定，舊名成為別名仍可送達；接力中或讀不到座位狀態的對話跳過，下一輪再處理。被改名的對話會收到一則給自己的通知，說明新地址與舊地址仍有效，通知失敗只記日誌。團隊模組為此多了接力守門讀取介面（`RelayGuard`），本機成員的座位也會帶出 lead 的 session（#3493）。
- **成員名字在派工時預訂（NP-4 A1）**：名字服務新增預訂與釋放，預訂算進已占用的名字、10 分鐘自然失效、開機時從團隊模組重建，且第一次命名時會依 tmux session 名取回預訂，避免新成員被別人搶走已指定的名字。團隊模組端的呼叫另開一張（#3507）。
- **`/team <name>` 與 `pdx names`（NP-4d，mod 端）**：新增 `pdx names [--json]` 列出啟用中的角色；`pdx relay person … --command lead` 多了互斥的 `--lead-name <code>`（2 到 16 個小寫字母）與 `--card`。在一般 session 輸入 `/team <name>`，名字剛好對上一個角色就直接建立並綁定該名字，沒有角色或對上多個角色（daemon 不替人挑）就走核准卡等人決定；單打 `/team` 在非 lead 的 session 走核准卡，在 lead 內仍是看名冊。代理收到的提示會說明名字已綁定或要等卡片、不得自行核准。已知限制：daemon 端（儲存 `lead_name`／`card` 與核准時改名）在下一張，需要重跑 `pdx setup`（#3508）。
- **lead 候選帶名稱、類別與最後時間（NP-4c-0）**：`LeadCandidate` 多了 `name`（顯示名，查不到時退回代號）、`kind`（keep、former、lead、other，也是排序依據）與 `last_at`（只有 former 有值），給核准卡使用，六個方法的簽名不變。顯示名在 pool generation 鎖內查，避免候選與名字跨世代不一致（#3514）。

### Added：App

- **手動任務編組的資料層（NA-6a）**：App 端新增手動任務編組的讀、寫與每主機快取，只在 daemon 宣告 `task_groups.v1` 時才讀。寫入以 revision 把關，每主機一次一筆，衝突時在 daemon 的副本上重做一次；若 daemon 丟掉了某些列或存的值不是清單，可見清單不完整，為避免整份寫回抹掉其餘資料，一律拒絕寫入。主機被移除或換位址後，排隊中與重試中的寫入不再送出。成員以 lineage root session id 存，App 從 `GET /api/team/unattended` 的 quotas 取得。這一版不接任何 UI，合併後沒有可見變化（#3495）。
- **姓名池正本主機與角色複製（NA-1d PR-A）**：新增每台裝置自己的正本主機設定（不同步、不進 Profile），角色以正本為準；正本離線時退回 stamp 最高的那台並標示唯讀。存檔先寫正本，再複製到其他已連線且宣告 `personas.v1` 的主機，被拒時把多出的角色補進正本再複製，最多兩輪；每次讀完一台也會比內容，不同就把正本補過去，存檔提示會列出複製失敗的主機。已知限制：選擇正本主機的下拉、「正本離線」說明與第一次存角色的確認在下一張，這一版單獨上線時一律用固定的預設值（主機順序第一台）（#3498）。
- **側欄改為唯讀編組檢視（NA-6b）**：側欄依序顯示團隊組、各主機的手動組與未分組，團隊優先；一個 tab 代表它第一個存活的 tmux 窗格所屬的 session，不是 session 的 tab 不列出，無法確認安全的主機顯示說明行。原本的閘門刪除，所以主頁切換鈕與外觀編輯器的團隊圖示列永遠顯示。團隊座位列開啟座位自己的 tab，不顯示關閉鈕、中鍵也不會關掉整個分割 tab。已知限制：本版純唯讀，右鍵、拖曳與改名在 NA-6c（#3511）。

### Changed：文件與 skill

- **新增 `pdx-test` skill 草稿（WWD-6b）**：寫給 agent 看，說明何時用 `pdx test`、單檔走本機快車道、沒有 task 時用 `--adhoc`、等待與取消的行為、結束碼表（與程式逐一對照），以及與 `pdx-gate` 並存到 WWD-9 為止的分工；不要把 `pdx test` 包在 `pdx gate` 裡。CLAUDE.md 與 brief 範本暫不引用，等跨主機 `pdx test` 在 C2 驗收後才引用（#3296）。

## [1.0.0-alpha.756] - 2026-10-19

> daemon、`pdx` CLI 與 `wwd` 有改；**mod 有改（`cmd/pdx/plugin/purdex/skills/pdx-watch/SKILL.md`），要重跑 `pdx setup`**，hook 程式沒有改。SPA 有改，依賴沒變，不用 `pnpm install`。Electron 與 755 相同。
>
> **WWD-8a 的寫入端**：facts 開始填 proto 2 的欄位（`mem.decompress_rate`、`queue.queued`），只在所有主機都回報 proto ≥ 2 時才寫（755 已讓四台都是 proto 2，所以這一版一上線就生效）。告警的儲存、手機推播與投遞模組（含 primary 沒有推播時由 standby 接手）都已就位，但這一版**沒有任何告警規則**（規則在 8a-2），所以不會寫入任何告警，也不會推播。
>
> **資料庫**：`wwd.db` 的 host schema 由 5 升到 6（`earned_labels`、`result_cache`）。舊版會拒絕開 schema 6，所以**退回 755 時要還原 `wwd.db.pre-756` 並換一個新的 epoch，而且退版前隔離區必須是空的**：per-job 資料庫改用新的名稱，舊版的清掃會把它們刪掉（步驟見 `docs/wwd-deploy.md` §5）。Purdex 這邊：`team.db` 加了三個索引，`meta.db` 的 `peer_name_aliases` 加了 `last_live_at` 欄位（有預設值）；舊版都不受影響，退回不必還原。
>
> **pdx test 準備開放**：守門員現在認得 wwd 的佇列工作（只記錄、不叫醒），`pdx test`／`pdx jobs` 送出的工作也會帶上送件人的 session，完成通知找得到對話。部署並在真機上確認後，lead 會廣播 pdx test 可以使用。
>
> **worker**：部署時四台的 worker 先一律關閉；mlab 與 air26 由 lead 在部署檢查通過後打開；mserv 要等 #3441 完成才開；air19 照舊不開。
>
> 下一版（757）會帶 WWD-5 的 digest 讀取端，wire 的 `proto` 升到 3；部署時每台主機都要先升 Purdex、再升 wwd。

### Added：wwd 告警與訊號（WWD-8a 寫入端）

- **三個新訊號與啟動紀錄（8a-0）**：wwd 開始寫出 `v: 2` 的 facts，新增 `mem.decompress_rate`（macOS 讀 `host_statistics64` 的 decompressions，以和 page-out 相同的視窗算每秒差值，一分鐘取最大值；Linux 沒有這個欄位）與 `queue.queued`（state=queued 的工作數，一分鐘取尖峰；讀不到時省略，不寫 0），以及每次啟動寫一列的 `wwd_starts`（版本與前一次結束方式，保留 7 天，供之後的 crash-loop 規則使用）。寫入有閘門：只有 `wire.PoolAtLeast(peers, 2)` 成立，也就是所有 peer 都回報 proto ≥ 2 時才寫，閘門關著時照舊寫 v1 形狀。這一張必須晚於讀取端（755）一版出貨，755 已完成（#3419）。
- **wwd 端的告警寫入與 alert 事件（8a-1a）**：store 新增 `RaiseAlert`、`ClearAlert`，告警保留 7 天，`cleared_at` 不得早於 `last`，過長的 detail 截斷而不拒絕；daemon 的 `AlertBook` 寫表後，只有內容有變更且訊號閘門開著，才發出 `alert` 事件（也送給 peer 訂閱者）。這一版沒有任何規則會呼叫它，所以不會產生告警（#3426）。
- **告警的手機推播（8a-1b）**：push 模組實作 `AlertNotifier`，替 wwd 告警送手機推播；人在電腦前時略過，沒有裝置時視為完成，push 尚未啟動或佇列滿時回報未送出。這一版還沒有呼叫端，既有推播路徑不變（#3428）。
- **告警投遞模組 `wwdalerts`（8a-1c-i）**：每 30 秒經本機 wwd 讀每台主機的 `GET /v1/alerts`；只有持有 fleet `primary` 角色的 daemon 會推播，standby 只讀並收 digest。「已投遞」只在 push 模組確認 APNs 收下（`Sent`）時才寫進 ledger；沒有確認時，同一個告警最多每 5 分鐘交給 push 一次、一小時內最多 12 次，確認送出後一小時內不再推，一小時後仍 active 才再推，消失後再次出現視為新實例並立刻推。讀不到的主機略過，不當成已解除。`digest` 級只寫進 `digest` 表（保留 14 天）。使用自己的資料庫 `wwdalerts.db`。**必須與 8a-1c-ii 同版出貨**（#3434）。
- **primary 沒有推播時由 standby 接手（8a-1c-ii）**：fleet 的 role 回覆多一個可省略欄位 `push_ready`，舊版會忽略它、新版讀到舊版的回覆視為沒回報；沒有新增路由。值取自 push 模組是否仍就緒（模組停掉後視為未就緒）。引擎記下對方的回覆，綁角色世代，角色改變後舊紀錄視為未知，未知時 standby 接手。fleet 的 `order` 是 `[air26, mlab]`，primary 不固定，所以 air26 當 primary 又沒有 `[push]` 時需要這一條（#3445）。

### Added：wwd 資格與結果重用（WWD-5）

- **`wwd.db` host schema 6（5-0）**：新增 `earned_labels` 與 `result_cache`（含兩個索引），新庫直接是 v6，v5 檔開啟時補表並保留舊列。這一張只有 DDL 與遷移。部署前四台要先備份 `wwd.db`（#3448）。
- **earned 標籤進廣告，`host:<alias>`（5b-1）**：每台 wwd 廣告 `host:<alias>`（只縮小誰能接，不給權限）；earned 的 task 標籤進廣告，quarantined 的不進。reference 主機不用配對就持有 `task:<repo>/<task>`（有該 repo 與任務要求的全部標籤才給）。earned 集合在兩筆 facts 之間改變時重算，只在變動時送 labels 事件。新增只走本機 socket 的 `GET /v1/earned-labels`（#3468）。
- **reference 主機的標籤取自 manifest，`pdx wwd labels`（5b-1b）**：wwd 從本機各 clone 的 `.wwd/tasks.toml` 讀出 `qualify.reference` 的任務，reference 主機才會真的廣告 `task:<repo>/<task>`（依檔案 mtime／size 快取，壞掉的 manifest 視同沒有）。新增 `pdx wwd labels [--json]` 列出 earned／quarantined 與各自的 fingerprint（#3472）。
- **結果快取的讀寫（5d-1b）**：`result_cache` 上限 10,000 筆（最舊先出），超過 3 天視為沒有（剛好 3 天仍命中）；新增依標籤刪除（漂移撤銷標籤時用）與清理，並有測試釘住「cache ≤ log ≤ job 的保留期」。已知限制：quarantined 的標籤不會被廣告，所以之後隔離標籤的一方必須同時刪除該標籤的快取。這一版還沒有呼叫端，行為不變（#3476）。
- **資格觸發規則與配對比較（5b-2a）**：新套件 `internal/wwd/qualify`，純函式：`Check`／`Decide`／`Manual` 判斷何時該為某任務啟動一對（自動重試要等 fingerprint 改變或滿 24 小時），`Compare` 判斷一對是否一致（exit 狀態、通過與失敗數量、失敗集合相同；任一半缺結果、主機不符或摘要被截斷則作廢）。這一版還沒有呼叫端，行為不變。已知限制：`wire.Summary` 只有通過數量、沒有通過清單，跑了不同測試卻通過同樣數量的兩台分不出來；補救是通過清單的 digest，分兩版出（757 讀取端、之後寫入端），**5b-2c 不得早於寫入端上線**（#3487）。

### Changed：wwd

- **per-job 資料庫改用 key `source/id` 命名（#3373 PR2）**：job id 只在發出它的 origin 內唯一，所以 per-job MySQL 資料庫與使用者改由 key 的摘要命名；名稱形狀（`^wwd_[0-9a-f]{32}$`、`^wwdu_[0-9a-f]{26}$`）、SQL 與設定都不變。清掃會同時保留一個仍存活 key 的新舊兩種名稱，避免升級後第一次清掃把仍被持有的舊資料庫（例如隔離中的工作）當成殘留刪掉；舊名稱的相容在下一版移除。這也是退版前隔離區必須為空的原因（#3462）。
- **工作的行程標記加上 `WWD_ORIGIN`（#3373 PR3a）**：除了 `WWD_JOB=<id>`，行程環境多一個 `WWD_ORIGIN=<16 位十六進位>`，讓同一個 id 來自兩個 origin 的工作可以區分；任務自己的環境不能設定它。清掃與用量取樣都改用 `tag:id`。沒有 wire 與 schema 變動，不用升 `proto`。舊版 wwd 啟動、升級後仍在跑的指令沒有 `WWD_ORIGIN`，過渡期內仍會被認得，這條過渡規則下一版移除（#3465 追蹤）（#3481）。

### Fixed：wwd

- **reference 任務的 repo 與自己的 task 標籤不再被當成環境需求（5b-1c）**：5b-1b 把任務標籤裡的 `repo:<repo>` 與 `task:<repo>/<task>` 也當成非環境需求，使真實的 purdex manifest（例如 `spa-related`）在 reference 主機上永遠不會被持有。現在知道 repo 名時會把這兩種標籤從需求拿掉，外來的 `task:`、`host:`、別的 `repo:` 仍讓任務失敗（#3485）。

### Added：pdx、watch 與 pool API

- **守門員認得 wwd 的佇列工作（#3447 2/2）**：wwd worker 的佇列工作不在閘門名額內，原本每件都會被 `unmanaged_heavy` 抓到而叫醒守門員。現在以行程環境的 `WWD_JOB` 判斷，標籤為新的 `wwd_job`，指令與簽章照樣記錄，但立即標成已處理（備註「wwd 佇列工作（名額外），只記錄」），不進送出佇列、不喚醒守門員、不計入未處理；用 `pdx watch log` 查看。標記判斷失敗時退回一般路徑。`pdx-watch` 的 SKILL.md 標籤表補了一行，**所以要重跑 `pdx setup`**（#3461）。
- **另一台主機的歷史經本機 wwd 轉送，新增 `GET /api/pool/facts/series`（WWD-7a 第 1 張）**：`GET /api/pool/facts/history?host=<alias>` 現在會經本機 wwd 轉送，不再回 501 `host_not_local`；wwd 的 4xx 以原狀態與 `wwd_rejected` 回傳，其他仍是 502 `wwd_error`。新增 `GET /api/pool/facts/series`（能力 `pool.series.v1`）把歷史精簡成圖表需要的點，`since` 可為 RFC 3339 時間或相對長度（最遠 14 天），`step` 預設視窗 ≤ 2 小時為 5 秒、否則 1 分鐘，最多 2000 點（保留最新的，`truncated` 標示）；已加入配對裝置的允許清單。已知限制：wwd 轉送上限 8 MiB，另一台主機的 series 約只能到 1 小時，本機約 1.5 天，不是路由接受的 14 天（#3489 追蹤）。wwd 的 wire 沒有改動（#3490）。

### Fixed：pdx

- **`pdx test`／`pdx jobs` 的 submitter session 讀得到了（#3477）**：原本只讀 `PDX_SESSION_ID`，而沒有任何地方設定它，所以 `submitter.session` 一律是空字串，完成通知對不到對話、App 分頁的排隊標記對不到分頁，`pdx jobs` 預設「只列這個 session 的工作」的過濾也形同虛設。現在依序讀 `CLAUDE_CODE_SESSION_ID`、`CLAUDE_SESSION_ID`（舊名），三處（閘門、`pdx test`、`pdx jobs`）共用同一份讀取；`PDX_SESSION_ID` 仍可在 `pdx` 端明確覆寫，閘門不讀它。環境值需 8～64 字元的 `[0-9A-Za-z_-]`，否則視為沒設。尚未驗證子行程／subagent 是否沿用同一個 session id，需真機確認（#3484）。

### Added：姓名池 v2

- **挑名規則（NP-3b）**：以純函式實作三條挑名規則（lead 候選順序、member 優先挑與 lead 配對過的名字、其他人先挑新面孔再挑與 lead 配對最少的名字；配對次數相同時依 pool 順序，不隨機）。這一版它還沒有被正式程式碼呼叫（#3464）。
- **命名流程改用規則 3 與名字歷史（NP-3c-1）**：新進對話優先從沒用過的名字隨機挑，其次挑與任一 lead 配對最少的，lead:true 的角色排最後；舊的「在所有空名字中隨機」取消。名字歷史讀不到時照舊挑並記一行 log。與上一版的差別：沒有歷史也沒有 lead:true 角色時，行為與先前相同（對所有空名字隨機挑）。其他主機的熟悉名字集合留待 NP-3d 接上（#3471）。
- **名字服務（NP-3c-2）**：peers 模組實作並登記 `NameService`：lead 候選名單、為 lead 改名（檢查代號是否為啟用中的角色、是否已被本機或其他主機占用，比對與別名在同一個 meta 交易中完成）、可還原的撤銷、記錄 lead 與配對。成員名字的預留在 NP-4。目前由團隊模組呼叫的部分還沒接上（#3483）。
- **別名保留與 envelope 的熟悉名字（NP-3d）**：`peer_name_aliases` 加上 `last_live_at`；每次啟動一分鐘後與其後每小時做一次清理，別名本身或其繼任者仍活著就更新時間，30 天沒有活動的刪除，總數上限 512。只有清單完整可信時才會刪。envelope 的 `names.familiar` 帶本機最近 64 個代號，讀不到時回報 `names.unavailable`（#3488）。
- **成員上限提高到 9，新增 `team.max_members.v2`（NP-4a）**：團隊建立、`PUT /api/team/max-members` 與 CLI `--max-members` 的範圍由 1～8 改為 1～9；既有團隊已存的 8 以下的值不會被改寫。無人值守 lead 的成員上限（3）不變。舊 App 只看得到 v1 能力，仍維持 8（#3469）。
- **無人值守視圖的決策日誌（NP-6b）**：`GET`／`PUT /api/team/unattended` 多一個 `log`（能力 `team.decision_log.v1`），最新 30 筆、新到舊，合併團隊申請的決策（不論誰決定）與開關的開／關紀錄；舊的 `approved` 分頁不變，舊 App 照常運作。無人值守模式拒絕的 lead／adopt 申請在日誌中列為 `denied`；申請人撤回、放棄、尚未決定與聊天提示不列入（#3460）。

### Added：團隊介面 v2 與 App

- **lead 核准視窗的名字卡（NA-7）**：payload 帶 `lead_names` 時，核准視窗多一列「名字」：22px 頭像加名字，「保留目前的名字」第一個且預設選取，接著「當過 lead」「預設 lead」，其餘收在「更多…」。只有選了保留以外的名字才送 `grant.lead_name`。遇到 409 `name_taken` 會用回應刷新候選並提示，不跳失敗 toast；離線選名、重連重送被拒時也會通知開著的卡片刷新（#3467）。
- **無人值守面板拿掉上限欄與資料夾（NA-8a）**：兩者已在團隊面板底部的設定區塊，面板的額度列固定三欄；接力額度兩邊都保留（#3470）。
- **無人值守面板改顯示決策日誌（NA-8b）**：有 `log` 的主機顯示日誌（跨主機合併、最新 30 條、可捲動），每列有 18px 頭像、名字、類型、狀態標籤與時間，開關事件以粉紅底粗體整列顯示；顯示不只一台主機時每列標主機。沒有 `log` 的舊 daemon 維持原本的「自動通過的申請」清單與「顯示更多」，讀不到的主機照舊點名（#3475）。
- **側欄檢視切換鈕與 `teamIcon`（NA-5a）**：側欄檢視切換鈕從 Home 按鈕拆出，外觀設定新增 `teamIcon`（預設 `UsersThree`）與 `sidebarView`。**整個可見部分在程式常數閘門後面，預設關**（NA-6 拿掉閘門），所以這一版使用者看不到任何變化（#3486）。
- **側欄 session 列戴 persona 頭像（NA-5b）**：session 列的 agent 圖示換成 persona 頭像，燈號改成頭像右上角的 overlay（未讀色、等待核准的手、subagent 點、queue 符號都保留）；認不出 persona 的維持 agent 圖示。判斷只讀 App 已有的資料、不發請求：先看團隊 roster（比對 tmux session 名稱與世代），沒有再用該主機已快取的 `/api/peers` 列（世代相符才採用）。已知限制：單獨 session、不是 team 成員、且該主機的 peers 還沒被抓過時仍是 agent 圖示（#3492）。

### 文件

- **WWD-7（App 端的 wwd）設計**：在 wwd plan 補上 WWD-7 的設計章節（單一主機狀態、輪詢與歷史範圍、PR 切分）。僅文件（#3482）。

## [1.0.0-alpha.755] - 2026-10-19

> daemon、`pdx` CLI 與 `wwd` 有改；**mod（`cmd/pdx/plugin/`）沒改，不用重跑 `pdx setup`**。SPA 有改，依賴沒變，不用 `pnpm install`。Electron 有改（#3443 頭像抓圖 IPC），但頁面上的照片選擇介面要等 NA-1b-2c，這一版不跑 `electron:build` 打包；dev update 偵測到原始碼變了會自己建置。
>
> **wire 契約 `proto` 2 的讀取端（WWD-8a-c）**：四台 wwd 與兩個 Purdex daemon 要用同一份建置部署。這一版只教會所有讀取端認得新欄位，寫入端仍送 `v: 1`；寫入新欄位的部分（告警、訊號、推播）在 756，不能跟這一版一起出。部署後每台 wwd 的 `GET /v1/info` 應為 `"proto": 2`。
>
> **資料庫**：`wwd.db` 的 host schema 由 4 升到 5（新表 `wwd_starts`、`alerts`）。舊版會拒絕開 schema 5，所以**退回 754 時要還原部署前的 `wwd.db.pre-755`，並換一個新的 epoch**（步驟見 `docs/wwd-deploy.md` §5）。Purdex 這邊只新增表：`host_config.db` 的 `unattended_events`，`meta.db` 的 `peer_name_aliases`、`name_leads`、`name_pairs`；舊版會忽略它們，退回不必還原。
>
> **worker**：wwd 判斷重名額空位時，會把同一台 `pdx gate` 已在跑的重指令算進去：閘門的重名額滿了就不領新工作，讀不到閘門時保守視為滿；沒有閘門的主機（mserv、air19）不受影響。部署時四台的 worker 先一律關閉；mlab 與 air26 由 lead 在部署檢查通過後打開；mserv 要等 #3441（worker 端只領明確標記的工作）完成才開；air19 照舊不開。
>
> 下一版（756）會帶 WWD-8a 的寫入端，以及 `wwd.db` 的 host schema 6（WWD-5 的資格標籤與結果快取）。

### Added：wwd

- **wire 契約 `proto` 2（WWD-8a-c 1/2）**：`Proto` 升為 2（最低仍是 1），`Info` 與 `Facts` 接受 1～2，其他版本拒絕。新增的欄位全部 `omitempty`：facts 的 `mem.decompress_rate`、`queue`，例外（與 facts 內的 `FactExc`）的 `allow`／`flags`，以及 `Alert`、`AlertList`、`AlertEvent` 與 SSE 事件 `alert`。寫入端一律不填（`WriteProto = 1`，sampler 照舊寫 `v: 1`），所以 proto 1 的嚴格 decoder 看不到新東西；pool 裡從沒連上過的 peer，占位 info 報最低版本，不會宣稱自己是 proto 2。因為舊讀取端遇到新欄位會拒收，四台 wwd 與兩個 Purdex daemon 必須同版部署（#3412）。
- **`wwd.db` 遷移 v5 與告警讀取（WWD-8a-c 2/2）**：host schema 由 4 升到 5，新增 `wwd_starts`（啟動紀錄）與 `alerts`，全部 `IF NOT EXISTS`、可重跑；比 5 新的 schema 會被拒。新增唯讀的 `GET /v1/alerts`（socket 可用 `?host=<alias>` 轉發；TCP 帶 pool token 且帶 host 回 400；沒有 store 時回 404），`client.Alerts` 把 404 當作「還沒有告警」。`wire.PoolAtLeast(peers, min)` 供之後寫入端判斷「每個 peer 都回報 `proto >= min`」，沒連上過的 peer 算最舊。`docs/wwd-deploy.md` 補上 proto 2 的兩階段部署、`wwd.db.pre-<version>` 備份與還原步驟，以及升級後的檢查指令。這一版沒有任何東西會寫入告警表（#3413）。
- **worker 領工作的識別（#3373 PR1）**：job id 只在發出它的 origin 內唯一，而 worker 會從自己的佇列與每個 peer 領工作。新增 `internal/wwd/worker/ident`，以 `(Source, ID)` 命名一件工作，`Source` 取自 worker 自己設定檔裡該連線的名字，不信 job JSON 裡 peer 自己填的 `Origin`。runner 的三個步驟都改收這個識別。`wire.Job.Validate` 會拒絕含斜線的 id，避免 `(a, b/c)` 與 `(a/b, c)` 撞成同一個 key；id 本來就是 origin 產生的 ULID，正常情況沒有差別。這張只建型別與管線，行為不變（#3451）。
- **結果重用的判斷（WWD-5d-1a）**：新增 `reuse.Key` 與四條查詢條件（同一件工作、主機涵蓋所需標籤、主機環境仍有效、只限同一台），合成 `Answers`。全是純函式，不碰 store 與時鐘，目前沒有任何呼叫端，所以不改變行為（#3454）。

### Changed：wwd

- **閘門重名額算進 headroom（#3447 1/2）**：worker 領到的工作不經過 `pdx gate`，等於在閘門名額外多開一個池。現在 wwd 每次取樣會從同一台直接讀閘門（鎖檔與持有者紀錄，不經網路）：已占的重名額數 ≥ 總數就把 `headroom.heavy` 設為 0，不再領新工作，正在跑的工作不受影響，`light` 不動。沒有閘門（沒有閘門目錄或 `[gate] enabled = false`，如 mserv、air19）時不受影響；有閘門卻讀不到（設定檔壞掉、目錄或鎖檔打不開）時保守視為滿，並記一行 log，同一個問題不重複記。wire 沒變，`pdx wwd pool` 的 FREE heavy 會直接反映（#3450）。

### Fixed：wwd

- **H2 沒有 owner 規則時，檔案不存在代表主人離開**：`[owner] enabled = false` 的主機，facts 的 owner 是 null，而 yield 只在讀到「主人離開」時才結束，所以 H2 的檔案造成一次 yield 後，檔案移除了 worker 仍卡在 yield 直到重啟。現在這種主機上，H2 指定的檔案確認不存在（ENOENT；其他 stat 錯誤不算）時，owner 區塊回報 `active: false`，yield 得以結束；有 owner 規則的主機，null 仍是讀取失敗，yield 不會被解除（#3437）。
- **H2 的判斷改看生效中的 owner 規則**：上一條原本依啟動時的設定判斷「有沒有規則」，現在改讀 `PUT /v1/worker` 之後生效的規則，與 worker 迴圈用的是同一個值。已知限制：`[owner]` 在設定檔中是啟用的主機，用 PUT 把規則清空後 owner 來源仍在跑，不屬於這一條的範圍（#3456）。
- **Purdex 的 `.wwd/tasks.toml` 每個能離開本機的任務都標 `os:darwin`**：原本 `tsc`、`lint`、`execstat-guard`、`go-all`、`spa-full`、`tsc-force`、`mod-all` 完全沒有標籤，其他任務也只有 `repo:purdex`，開了 worker 的 Linux 主機（mserv）會領走然後因缺少環境而失敗。同時新增測試檢查真的 manifest：任何不是「永遠本機」的任務都必須有 `os:darwin`（例外需 A 類名單、`qualify` 與 `task:purdex/<name>` 標籤三者齊備，目前名單是空的）（#3439）。

### Added：姓名池 v2

- **名字別名與名字歷史（NP-3a）**：meta DB 新增 `peer_name_aliases`、`name_leads`、`name_pairs`。改名時舊的完整名字在同一個交易裡留成別名（CAS 輸了不會留）；新名字不再是任何人的別名；每個 session 最多 512 個別名。送訊時，本機目標若按名字找不到，會再用別名經 ref 解析，所以改名後舊名字仍送得到，接力後的繼任者也到得了；別名永遠排在現行名字之後，括號形式 `<name> [<ref>]` 接受「該 ref 的別名」，別名配到別人的 ref 則是 `name_mismatch`。已知的別名找不到 ref 時回 `not found`，不會往 tmux 名字那一層落空。別名表每次送訊讀取，讀失敗時回 `not_ready`；遠端目標不帶別名。這張沒有任何動作會改名，所以現在線上唯一看得到的變化是多了這幾張表（#3435）。
- **sidebar 手動任務群組的儲存（NP-5）**：hostconfig 新增 `task_groups` 這個 key 與 `PUT /api/hostconfig/task-groups`（`{items, baseRevision}`，基底過期回 409）、能力 `task_groups.v1`。一個群組是 `{id, name, color?, roots, created_at, updated_at}`；規則：最多 32 個群組、每組最多 64 個 root、**一個 root 只能在一個群組**、名字比照團隊名字規則（1～64 bytes、可列印）、顏色 `#rrggbb`、不收未知欄位。沒給 id 時由 daemon 產生 8 碼十六進位。root 是 lineage root，接力後 session 留在原群組；這由 App 負責，daemon 只存收到的內容。各主機各存各的，不同步；空群組允許存在（#3433）。
- **無人值守開關的變更紀錄（NP-6a）**：`host_config.db` 新增 `unattended_events` 表。每次切換無人值守，紀錄與開關在同一個交易裡提交，要嘛都成功要嘛都不寫；CAS 輸了重讀不留紀錄，值沒變不寫，只存 client 的種類與標籤（不存位址），表只留最新 100 筆。新增 `Module.UnattendedEvents(limit)` 由新到舊列出紀錄，單列損壞時略過並記 log，不讓整個讀取失敗。把紀錄合進 `GET /api/team/unattended` 的日誌在 NP-6b，這一版還看不到。遷移只新增表，部署前由 lead 照例備份 `host_config.db`（#3442）。
- **姓名池 v2 的 spec 修訂**：使用者 10-19 的暫定決定：角色（persona）只有一台來源主機（預設 mlab），由 App 寫入並在儲存與重連時複製到其他 daemon，daemon 之間不互相同步；因此取消 NP-2、新增 NA-1d。僅文件（#3438）。

### Added：團隊介面 v2

- **成員上限可到 9（NA-4／NA-7 基礎）**：團隊上限範圍是 1～8；對列出 `team.max_members.v2` 能力的 daemon 則是 1～9。SPA 在送出前就拒絕超過該主機範圍的值，v1 daemon 自己會夾到 8，所以它回報的內容不變；無人值守面板的上限步進器同樣跟著範圍走（#3436）。
- **團隊面板的列改版（NA-4a）**：完整面板的每一列左邊是 44 px 頭像（沒有角色時用主機色底的縮寫，版面不位移），燈號變成頭像右下角的徽章；第一行是不帶燈的 agent 圖示、主機徽章與色塊、名字（13 px 粗體）、title、lead／未開標籤、context 環與工作簿按鈕；第二、三行放在內文欄。lead 的第二行改為它自己最後一行日誌（日誌有讀到它那個 unit 時）。與舊版的差別：完整列不再畫 subagent 的小點（#3430）。
- **團隊面板底部的設定區塊（NA-4b）**：取代 #3187／#3188 的齒輪與浮動面板。區塊永遠顯示在**自己是 lead 的團隊**面板底部（只在 lead 的分頁、完整面板；單行面板、標題列、成員與單人分頁都沒有），每一行只在該主機撐得起時出現：「成員上限」步進器與「用了 N」、「自動接力」與「成員接力池」額度步進器。額度與無人值守面板共用同一份 store 與寫入端，兩邊同時顯示同一個值；讀取中顯示「讀取中…」，讀取失敗顯示錯誤行。無人值守面板仍保留自己的步進器與資料夾，到 NA-8 才移除（#3444）。
- **設定區塊的「資料夾」行（NA-4b-2）**：roots 以 chips 顯示（中間省略，tooltip 是完整路徑）加一個虛線 +，按 + 展開既有的 `TeamRootsEditor`。只在 roster 帶 `roots` 與 `roots_rev` 時出現；只有 roots 時區塊也會顯示。切換團隊時重建，展開狀態與本地錯誤不會帶到別的團隊（#3453）。

### Added：角色頁與 Electron

- **設定的「角色」頁（NA-1b-2b）**：設定新增「角色」頁，排在工作台之後、App 之前，開啟時才載入。左邊清單顯示 32 px 頭像、顯示名、代號、lead 皇冠與頭像來源，已退休的排最後、變淡並標「已退休」；右邊表單有 96 px 預覽、代號（建立後鎖住）／顯示名、性別／國籍、個性／做事方法／興趣（中英各一欄）與「預設擔任 lead」，文字欄位有字數計數。**沒有刪除，只有「退休／恢復」**。存檔寫回目前顯示的那份集合所在的主機；回傳沒有 base 或遇到 stale／409 時，表單從目前集合重建並提示，不帶舊內容往前存；打到一半的草稿放在元件外，切走設定分頁再回來還在。`validatePersona` 補上國籍與 template 每段 ≤ 200 字（以字元計，對齊 daemon）。頭像選擇器、匯入與頁尾授權在下一張 NA-1b-2c（#3431）。
- **Electron 的 `avatar:fetch`（NA-1c）**：PlaceCats 與 PlaceDog 沒送 CORS 標頭，頁面讀不到，改由主程序去抓（Picsum 有 CORS，頁面自己抓）；抓不到時頁面請使用者改用上傳。因為這是「使用者輸入的網址變成從使用者電腦發出的請求」，收得很窄：只收 `https`，主機限 `picsum.photos`、`placecats.com`、`placedog.net` 完整比對，拒絕帳密、port、反斜線、尾點與 IDN 等怪字元；不自動跟隨轉址，每一跳都用同一個白名單檢查，最多 3 跳（`fastly.picsum.photos` 只能當轉址目標，不能當起點），轉到別的主機、http、內網位址、`localhost`、`file:` 一律拒絕且不發請求；回應最多 2 MB，只收 png／jpeg／webp／gif／avif（SVG 拒絕）。回傳 data URL，失敗以 `avatar-fetch:<code>` 開頭的錯誤丟出。preload 新增 `electronAPI.fetchAvatar(url)`。需要重新打包 Electron 才有作用，但頁面入口要到 NA-1b-2c 才有（#3443）。

### Fixed：訊息階段

- **被吸收的排隊訊息不再永遠停在「排隊中」**：Claude Code 忙的時候排進去的訊息，若在回合中途被吸收（「立刻送出」也是這個結果），階段會一直停在 `queued`，Mac 與 iOS 的氣泡永遠不離開「排隊中」。原因是比對時要求項目的時間戳不早於排隊那一列，但 `queued_command` 的時間是使用者送出的當下，早於 `enqueue` 紀錄（實測相差約 230 ms）。現在容許項目比排隊列早最多 2 秒；比對仍按佇列順序，每個項目只給一個 id，所以相鄰的兩則同文字訊息不會互換。已知限制：排隊列沒有時間戳時的處理另開 #3459 追蹤（#3458）。

## [1.0.0-alpha.754] - 2026-10-19

> daemon、`pdx` CLI 與 `wwd` 有改；**mod（`cmd/pdx/plugin/`）沒改，不用重跑 `pdx setup`**。Electron 與 753 相同。SPA 有改，並新增兩個依賴（`@dicebear/core`、`@dicebear/styles`），主 checkout 要先 `pnpm install`（dev server 與各 worktree 的 `node_modules` symlink 都讀它）。資料庫沒有 schema 變更：Purdex 的 hostconfig 多一個 `personas` key（舊版會忽略它），`wwd.db` 與 753 相同；退回 753 不需要還原。
>
> **C2 真機矩陣已在 753 上通過（14 格）**，這一版帶的是 C2 期間找到並修好的問題，以及各線已 merge 的功能。部署時四台的 worker 先一律關閉；mlab 與 air26 由 lead 在部署檢查通過後打開；mserv 要等三件事完成才開：purdex 的 `.wwd/tasks.toml` 每個非本機任務都標上 `os:darwin`、`wwd.slice` 設好 CPU 權重與上限、mserv 的人工送法停用；air19 照舊不開。mserv 的 `config.toml` 加上 `[tools] extra_path`（node toolchain），工作才找得到 node。
>
> 下一版（755）會帶 wire 契約 `proto` 2 的讀取端與 `wwd.db` 遷移（WWD-8a-c），那一版四台 wwd 與兩個 Purdex daemon 要同版部署；寫入新欄位的部分再晚一版（756）。

### Fixed：wwd

- **關機順序**：原本 worker 停止後就排空用量記錄器，但 HTTP servers、queue 的 sweeper、快車道還可能在那之後送出工作的終態事件，那一列用量就丟了。現在順序是 worker 停、這些生產者安靜（HTTP servers 關閉並答完在途請求，sweeper 與快車道返回）、記錄器排空、關 store。已知缺口：servers 的 3 秒關閉上限內沒答完的在途請求，其終態事件仍會丟；若還有 SSE 長連線，關機最多多等 3 秒（#3385）。
- **job 的 origin 由連線決定**：worker claim 成功後，job 的 origin 一律取自設定檔裡該連線的名字，不再信 claim 回應裡對方自己填的字串，避免惡意 peer 填另一個 peer 的 alias 讓 worker 去那邊要快照。`JobDir` 的雜湊含 origin，所以目錄名會和升級前不同，舊目錄由隔離區 marker 記的路徑管理（#3386）。
- **origin 自己的快照錯誤不再停權整個 repo**：origin 的 snapshot 路由因 commit 沒被 pin 等原因回 5xx 時，worker 改以 `reclaim` 歸還（不計 attempt），只對「這一個 job」退避（15 秒起、連續失敗加倍、上限 5 分鐘、只放記憶體），不再停權 (origin, repo) 5 分鐘；連不上、逾時、解壓、磁碟、git 錯誤仍算 infra。限制：origin 端的 `queue.Release` 目前不保存 detail，原因只寫在 worker 的 log；origin 一直拒絕的 job 會一直 queued，到 `queue_timeout`（預設 2 小時）變 `expired`（#3387）。
- **測試**：`run` 測試的 `alive()` 不再把殭屍行程當成存活（Linux 讀 `/proc/<pid>/stat`，KILL 測試也最多等 3 秒），mserv 上連跑 40 次無失敗；僅測試改動（#3388）。

### Added：wwd 的 `[tools] extra_path`

- `config.toml` 新增 `[tools] extra_path`（絕對路徑或 `~/`；不存在的目錄啟動時跳過並記一行 log），放在預設工具目錄之前。facts 的工具探測與工作的 PATH 由同一份清單產生，改設定後重啟 wwd 即生效，不必重跑 install。起因是 mserv 上工作的 PATH 寫死在 systemd 單元，找不到 `~/Assist/toolchains.noindex/node/bin` 的 node。mserv 的設定與 wwd 重啟不在這張 PR，等 C2 結束後由部署時做（#3398）。

### Changed：效能觀測

- **ps 退路快取**：argv 含非可印 ASCII 而只有 ps 讀得出來的行程（例如啟動指令列帶長中文 prompt 的守門員），註冊表 liveness 這條路改用 `ProcessSnapshot.ReadCached`，最多沿用 60 秒內的 ps 答案，不再每次讀註冊表就 fork 兩次 ps（753 部署後約每分鐘 43～55 次）。快取鍵含 pid、啟動時間與核心指令名，上限 64 筆；身分每次仍核對，pid 已不在或被別的行程取得時在 fork 前就回 `ErrProcessChanged`。`ReadFresh` 不碰快取，範圍只有 peers liveness（#3390）。
- **`tmux.read` 明細事件**：`tmux.read` 這個 tag 原樣保留，另外每次讀取記一個 execstat 事件 `tmux.read.<方法名>`（例如 `tmux.read.ListAllPanes`，共 16 個呼叫點），可從 `GET /api/debug/execstat` 的 `events` 看各路徑每分鐘幾次。只加觀測，不改行為（#3391）。

### Fixed：C2 矩陣腳本

- 修正第一次矩陣執行發現的問題：case 1 每個 claim 的輸出行補換行；case 8 找得到 air26 上的 pdx；等待 `running`／`claimed` 的前置條件改為硬性，沒達成就 `FAIL precondition not reached` 並結束該 case；跑在 air26 worker 的 case 先最多等 10 分鐘讓 owner 不再 active，否則 SKIP；`C2_RAN_LOG` 可移動探測 log 並檢查其值；air26 的 `owner_rule` 為空時 precheck 警告。僅腳本、測試與文件，沒有 Go 改動（#3401）。
- 修正第二次執行發現的問題：case 8 在 air26 的暫存目錄改為 `/tmp/wwd-c2.r.$$`（原本落在 mlab 的 `TMPDIR` 而 Permission denied）；`fault.log_upload_fail` 啟用時，除非只跑 `--case 6`，precheck 一律拒絕執行；ssh 加 `ConnectTimeout` 20 秒，沒連上的連線重試一次，中途斷線的不重放，單一 case 因連線中斷只算該 case 失敗。僅腳本、測試與文件（#3425）。

### Added：日誌 journal v5

- **session 頁的 wire 與 store（JV-4a）**：新增 `getJournalSessions`、`getJournalPage` 與 `journal.sessions.v1` 能力探測；目前那頁仍是 `unit.entries`，其他 session 的頁放 `unit.pages`，每個 unit 最多保留 10 頁，被擠掉的下次顯示時重讀。換了 leader session（relay、`/clear`）時整頁換掉不併入。這張沒有畫面改動，沒有 capability 時行為完全不變（#3184）。
- **日誌卡片的分頁（JV-4b）**：有 `journal.sessions.v1` 時，卡片出現頁首與底部上一頁／下一頁（邊界 disabled）、可重試的錯誤區塊；篩選只作用在該頁，待確認 band 與計數換頁不變。捲動位置以頁為單位記憶（每個 unit 最多 50 頁），目前在第幾頁切 tab 回來還在。沒有該 capability 時維持舊的「載入更早」。第一次讀就 503 時，卡片改為顯示錯誤而不是空白（#3185）。
- **移除過渡用的 `pinned[]` 與 pin 路由**：daemon 不再送 `pinned[]`，entry 不再帶 `pinned`，`PUT /api/journal/pin` 不再註冊（由 410 `pin_removed` 改為 404）；舊檔 meta 裡的 `"pinned"` 仍可解析，欄位被忽略（#3394）。SPA 同步不再讀 `pinned[]`，待確認 band 只看 `confirms[]`，沒有該欄位的回應就沒有待確認項目；`confirmSupport` 只管核取方塊（#3397）。

### Added：無人值守

- **共用讀取器（JV-5b 1/3）**：新增 `lib/team/unattended-reader.ts`，作為讀取主機無人值守第一頁的唯一入口，無人值守面板與團隊設定共用。同一台主機同時只發一個請求（single-flight，後來者加入）、10 秒逾時；單一呼叫者中止只放掉自己，全部等待者都放掉才中止請求，且不算主機讀取失敗。額度的讀取結果存進 `relay-quota.ts` 的 `reads`，面板的額度列改從 store 畫；數字仍只來自 `confirmed`／`writes`（#3186）。
- **讀取失敗的主機在額度區保留位置**：主機的無人值守頁讀取逾時時，額度區不再靜默漏掉該主機，改顯示它自己最後一次的讀取，沒有的話顯示「讀不到額度」（#3395）。

### Added：訊息階段 MS2（daemon 與 Mac Deck）

- **daemon 的階段帳本**：新增記憶體內的 `client_msg_id` 階段帳本（`received` < `filled` < `entered` < `queued` < `read`；終態 `read`／`failed`／`recalled`；`unknown` 仍可被比對推進），含比對規則與記憶體上限（每則文字 16 KiB、全部 2 MiB）。這張只有帳本與單元測試，不改行為（#3409）。
- **typed 路由寫入階段**：`POST /api/sessions/{code}/submit` 依序寫入 `received`、`filled`，並依回應寫 `entered`／`failed`／`unknown`（keys 模式的 `text_left_in_terminal` 記為 `unknown`）；成功的 `recall` 把該 session 所有 `queued` 的 id 記為 `recalled`。重送的 id 不寫階段。這張仍沒有任何東西送到 client（#3420）。
- **`conversation.stages` 與能力宣告**：WS 新增 `conversation.stages` frame，階段變動即使 transcript 沒變也會推送；`conversation.snapshot`（WS 與 HTTP）多一個 `stages`，重連後看得到訊息停在哪；宣告 `conversations.stages.v1`。不認得該 frame 的 client 會忽略，舊 daemon 不送（#3421）。
- **send-now 路由**：新增 `POST /api/sessions/{code}/send-now`（手機可用），送出 Claude Code 自己的 Ctrl+X Ctrl+S；輸入框有草稿、有對話框等待、佇列為空、沒有 mod owner 等情況各有拒絕碼。宣告 conversation 的 `send_now: "keys"` 與主機的 `sessions.send_now.v1`。send-now 不新增階段，已排隊的訊息仍靠一般比對到 `read`。已知限制：mod 讀輸入框與送出按鍵之間的毫秒內，若有人在終端機打字，仍可能被一併送出（#3427）。
- **Mac Deck 待送訊息的階段標記**：在宣告 `conversations.stages.v1` 的主機上，待送訊息以單一氣泡顯示階段（在本機、已到主機、在佇列、送不出：原因、不確定有沒有送到），確認只靠自己的 id 或 `read` 階段、不再靠文字比對；daemon 重啟後帳本遺失的訊息顯示「不確定有沒有送到」。沒有該 capability 時行為不變（#3410）。
- **Mac Deck 的立刻送出按鍵**：Ctrl+Enter 或 1.5 秒內 Ctrl+X 接 Ctrl+S 呼叫 send-now，並顯示提示「已立刻送出佇列裡的訊息；如果 Claude 正在寫回應，這一輪會被中斷。」；各拒絕原因有各自文字，桌機輸入框有未送出的文字時不送出。Up 鍵在第一行（不只是空輸入框）時把佇列取回。需要 `send_now: "keys"` 才啟用，否則 Ctrl+Enter 仍是 Enter（#3415）。
- **關閉階段模式時放掉階段**：主機重連到較舊或降版的 daemon 而關閉階段模式時，送出佇列的 entry 會清掉階段、已認領的列與讀取計時器，不再殘留階段標記（#3422）。

### Added：角色 personas（姓名池 v2）

- **hostconfig 的 personas（NP-1a）**：新增 `personas` 設定鍵與 `GET／PUT /api/hostconfig/personas`（以 revision 做 CAS，過期回 409 附現值，欄位不合回 400 並指出 index 與 field）；驗證代號、顯示名、文字長度、最多 64 個、recipe 2 KB、頭像 32 KB 且 128x128 以內等限制。`personas.v1` 能力在 NP-1b 才宣告（#3417）。
- **姓名池與日誌跟隨 personas（NP-1b）**：名字池改為可替換的登錄表，「可挑選」只含啟用中的角色（沒有任何角色時退回內建 30 個名字），「可辨識」則包含已退休的角色，使用中的對話保留原名；日誌的 `DisplayName` 改顯示角色的顯示名。宣告 `personas.v1`（#3423）。
- **App 端資料層（NA-1a）**：新增角色 store、`seatOf`／`useSeat`／`addressName` 與 `<Avatar>`（16／18／24／44，無圖時畫名字首字母加主機色）；只在 daemon 宣告 `personas.v1` 時才讀取，舊 daemon 不變。這張沒有新畫面（#3407）。
- **角色限制、匯入與存檔邏輯（NA-1b-1）**：新增限制檢查、`personas.json` 匯入（乾跑摘要為新增／變更／未變／略過並附原因）、頭像 128 px 點陣化（WebP，不支援時 PNG，超過 32 KB 降畫質）與存回目前集合所在主機（409 時重讀後再丟出）。僅邏輯，頁面是下一張（#3411）。
- **頭像繪製管線與 DiceBear 依賴（NA-1b-2a）**：新增 `@dicebear/core`、`@dicebear/styles`（精確版本）與六套款式，各自動態載入、不進主 chunk；照片網址只收 https 的 `picsum.photos`、`placecats.com`、`placedog.net`（後兩者需 NA-1c 的 `electronAPI.fetchAvatar`，沒有時請改用上傳）；頁尾授權資料標示 Micah 為 CC BY 4.0。頁面 UI 尚未接上（#3424）。

### Fixed：介面

- **拖放／貼上檔案的佔位符落在游標處**：拖入、貼上或用附件按鈕選取檔案後，`[Image #N]`（非圖片則為路徑行）會放在當下游標位置，並隨上傳期間的編輯移動，不再接在草稿結尾。同一次的多個檔案依檔案順序、以空白隔開。限制：拖放無法取得指標下的字元位置，改用輸入框的游標；位置靠比對編輯前後草稿推得，在邊緣遇到與鄰字相同的字元時可能偏一個字（#3400）。

### Docs

- wwd：WWD-5b～5d 的拆張與 10-19 裁定寫入 plan 與 spec，只有文件（#3389）。
- wwd：WWD-8 的 spec §13（Advisor）與 plan 一節換成 lead 的設計原文，並保留三處待使用者確認（#3405）。
- 訊息階段：step 2 的 plan（階段契約、send-now、Mac 按鍵、iOS 手勢），經 codex 審查後為 rev 2（#3396）。
- 訊息階段：plan 的 L7 補上「有 stages 時只以 id 或主機的讀取確認」，避免沒有 id 的 transcript 項目以文字誤確認舊的未送出訊息（#3408）。
- 姓名池 v2：團隊介面 v2 的 spec 與 plan，使用者決定為 U1～U19，codex 的 11 項發現已併入 rev 2（#3402）。
- 姓名池 v2：plan 記錄 NP-0 的量測結果，保留以 op 的 tmux session 名找到預約的做法（#3403）。
- 姓名池 v2：plan 加入 NA-0 的 App 端設計規則（#3406）。
- 姓名池 v2：spec 改為匯入 personas 時逐欄位合併，不刪除、不退休，recipe 變更的頭像重畫（#3414）。
- 姓名池 v2：spec 補上同步時保留僅存在於本機的代號，並以新的本機 stamp 重新發布合併結果（#3429）。

## [1.0.0-alpha.753] - 2026-10-19

> daemon、`pdx` CLI、`wwd` 與 **mod**（`cmd/pdx/plugin/` 的 `gate.js`）有改，**兩台都要重跑 `pdx setup`**（`pdx.json` 的 `entry` 開關保留，預設關）。SPA 與 Electron 與 752 相同。Purdex 的 `jobnotify.db` 多一張表（`cursor_epoch`，`CREATE IF NOT EXISTS`，舊版可以忽略它），退回 752 不需要還原。**`wwd.db` 再往前遷移**（佇列 schema 4 → 6：變更游標的 epoch、手動插隊欄位；主機 store schema 3 → 4：工作用量表），752 的 wwd 會拒絕開啟；部署腳本在升級 wwd 前會在每台留一份 `~/.local/share/wwd/wwd.db.pre-753`。wwd 與 Purdex 的 client 必須同版（回應多了必有的欄位，嚴格解碼的舊 client 會失敗）。
>
> **這一版 `wwd` 第一次真的跑 worker 主迴圈**（認領、執行、回報、讓位、隔離區都接上了）。部署時四台的 worker 一律先關，由 C2 矩陣逐台打開驗證；`[fault]` 區塊與 `[jobdb]` 區塊都不要預先加（`[jobdb]` 只在 mserv、要在新 binary 就位之後才加，舊 binary 認不得它）。沙箱主機沒有 guard 時 worker 不啟動；隔離區無法還原 hold 時 wwd 起不來（log 會寫原因）。

### Added：wwd 的 worker（零件、主迴圈與 `cmd/wwd` 接線）

- **認領與評分**：`worker/pick` 依 spec §4.6 計分（`1000·class + 等待分鐘 + origin 加分 + 暖機加分 − 大小罰分 − 同 session 罰分`），篩 labels、locks、停權、hold；手動位置先於評分；peer 走 TCP＋Bearer 讀 queued 工作（嚴格解碼、10 秒逾時、32 MiB 上限）（#3319）。
- **認領與續租控制器**（`worker/runner`）：`Controller.Work` 一個 job 從被挑中到命令結束，期限從請求送出前起算、回應不亂序、heartbeat 回 cancel 時只停命令（#3326）；跑完卻無法證明行程樹已停時，保留 workspace 與 lock keys（`Ended.Quarantined`，fail closed）（#3329）。
- **回報**（`worker/report`）：result、log、release；`Result.Err` 在沙箱下一律當 infra 上報；OOM 只信單元的 `Result`；infra 讓該 (origin, repo) 停權 5 分鐘；`Reclaim` 接 `yield.ReclaimAll`（#3325、#3328）。
- **兩種 origin**：本機 queue 與 peer（TCP＋Bearer，不跟隨轉址，409／404 嚴格判定）；claim 回應的 locks 與候選不同就放棄該 claim（#3333）。
- **遠端工作驗證**（`worker/remotejob`）：只信快照內的 manifest；`not_remote`、`local_only`、`class_above_task`、`args_invalid`、`cwd_outside` 等固定的拒絕碼（#3320）。
- **真的 `Exec`**（`worker/jobexec`）：每個 job 一個以 sha256 命名的目錄，工作目錄、HOME、TMPDIR 都在裡面；先做 workspace、用快照自己的 manifest 算出要跑什麼、再執行；已存在的目錄不被接管（#3340）。
- **隔離區**（`worker/quarantine`）：沙箱沒證明單元已停的工作寫 marker（原子寫入、0600），重啟後 `Restore` 補回 lock hold（marker 解除前拒絕同 key），`Sweep` 只在單元被證明已停時才刪目錄、marker 與 hold；目錄必須是 WorkRoot 的直接子目錄且非符號連結，沒設 WorkRoot 就一律不刪；`Restore` 遇到 key 衝突回 error，接線端要把它當成不能開始接 job 的條件（#3342）。
- **`Start`／`Running` 與 owner 讓位**：claim 之後把 job 放到背景跑；`Running.Stop` 取消命令但續租、等 Finish 結束，在 Finish 內以 `reclaim` 歸還（不計 attempts、不加 lapses），單元沒證明已停時改以 `infra` 歸還（#3348）。
- **worker 主迴圈**（`worker/loop`）：每 2 秒一輪，owner 回來時全部停手歸還，再依名額、headroom、facts 新鮮度（30 秒）與 settle（上次 claim 後 10 秒）決定要不要 claim；每輪最多一個；peer 的 zone 取自 pool 的 facts；關機時優雅收尾（#3360）。
- **隔離區接進迴圈**（#3367）：`Run` 先 `Restore`，失敗就一輪都不跑；marker 寫成功前不歸還 job。**marker 改在命令啟動前寫**（`Begin`／`Discard`／`Settle`、`runner.Guard`），wwd 在沙箱單元執行中被殺，重啟後也能補回 hold；entry 的狀態機與每次轉移的 generation 寫進 spec §5（#3371）。
- **只記錄 origin 收下結果的 run**（`Reporter.Delivery`）：result 已被收下但回覆隨租約遺失時，在獨立 ctx 內確認一次（#3369）。
- **jobexec 的 per-job 資料庫 hook**：manifest 檢查通過後才 `Create`，只取 `DB_*` 或 `WP_TESTS_DB_*` 其中一組並排在 task 自己的 env 之後；沒有 DB 的主機收到 `db = "per-job"` 回 `db_unavailable`；`Cleanup` 最多等 `DropTimeout`（#3375）；jobdb 的鎖在呼叫端 ctx 結束時放棄等待（#3372，#3376）。
- **`cmd/wwd` 接上 worker**：一張 `locks.Table` 由快車道、隔離區與 worker 共用；啟動順序是隔離區 `Restore` → queue／快車道／sampler → worker；本機 queue 在前、各 peer 在後；`[worker]` 多了 `background_dev`、`speed_factor`、`sandbox_user`（只在 Linux）；沙箱主機沒有 guard 時 worker 不啟動；`localruns.Service.Ready()` 之後 worker 才建 loop；關機先停 worker 再停用量記錄器再關 store（#3378）。
- **接線補漏**（e2e 在 main 上找到的兩處）：queue 沒接 `Pinner`，peer 的 worker 領不到快照；`LogDir` 沒人建，log 上傳 500，重試迴圈靜默。現在 `queue.New` 建 `LogDir`（0700，拒絕符號連結），`report` 的重試每次失敗都記 log（#3380，補 #3378）。

### Added：Linux 沙箱（mserv）

- **systemd 沙箱 runner**：工作以暫存 service `wwd-<job id>` 在 `wwd.slice` 內執行（`CPUQuota`、`MemoryMax`、`MemorySwapMax=0`、`RuntimeMaxSec`、`PrivateTmp`、`ProtectHome=read-only`、`NoNewPrivileges`、封鎖 docker.sock），結果取自 `systemctl show` 而不是 `systemd-run` 的結束碼；逾時與取消用 `systemctl stop`；`reset-failed` 在讀完結果後（#3321）。
- **在 mserv 實測**：情境 A–I 全過；實測抓到兩個假 `sudo`／`systemctl` 看不出的錯（沒載入過的單元 `reset-failed` 會失敗；成功結束的單元立刻被卸載、讀不到 `Result`），並新增 `Sandbox.ExtraProperties`（只收「只會收回權限」的屬性白名單）（#3332）。
- **`ProtectSystem=strict` 成為沙箱的基本屬性**：整個檔案系統唯讀，只有工作目錄與私有的 `/tmp`、`/var/tmp` 可寫（先前 `wake` 在 mserv 寫得進 `/srv/backups`、`/srv/stacks`、`/var/crash`）（#3334）。
- **每個 job 一個 MySQL 資料庫**（`task.db = "per-job"`）：`wwd_<hash>` 資料庫與只握有該庫權限的 `wwdu_<hash>` 使用者，隨機密碼，結束後清掉，啟動時掃除沒有主人的殘留（#3324）；manifest 新增 `db_env`（`db` → `DB_*`；`wptests` → `WP_TESTS_DB_*`＋table prefix），`db` 與 `db_env` 納入任務定義雜湊（#3341）。
- **MySQL 管理帳號走預存程序**（`wwd_admin`@`localhost`，不握任何全域權限，只有 `wwdctl` 資料庫的 `EXECUTE`）：七個預存程序以 `root` 身分執行，先對每個參數做錨定、區分大小寫的形狀檢查；`scripts/wwd-mysql-setup.sh install|remove|status` 與 35 項檢查的 `scripts/wwd-mysql-probe.sh`；已在 mserv 的開發實例安裝並檢查通過（#3343）；`jobdb` 改成只呼叫這七個預存程序，不送原始 SQL，所以受限帳號 `wwd_admin` 就夠用（4d-5b，#3346）。
- **`jobdb.PerJob` 與 `[jobdb]` 設定**（`enabled`、`host`、`port`、`user_hosts`、`defaults_file`；預設關，開了才檢查 defaults 檔存在、是一般檔、權限 600）；關閉時完全不建 Manager、不碰 MySQL（#3352）。
- `Sandbox.Gone`：判斷單元是否已停（不存在、`inactive`、`failed` 才算；讀不到是錯誤，不是「已停」），給隔離區使用（#3342）。

### Added：Purdex 讀 wwd 的工作

- **`jobs` 模組與 `jobs.v1`**：`GET /api/pool/jobs`（`state`、`session`、`host`、`after`、`limit`）、`/{id}`、`/{id}/log`；別台主機用 `?host=<alias>`；`?after=` 超過 wwd 的 head 回 409 `CursorAhead`；wwd 沒開回 503（#3315）。手機的 device token 也能讀這三條，唯讀（#3315 的 attack 修正）。
- **`jobs.changed` 主機事件**：每個工作事件轉成 `{host, job_id, state}`，App 收到只是重讀資源；斷線每 5 秒重連（#3318）。
- **完成通知改用 wwd 的游標**：`jobnotify` 依儲存的游標分頁 catch-up（1000 筆一頁），worker 時鐘落後不再漏通知（#3302，關 #3251）；wwd.db 被重建（epoch 變了或收到 `cursor_ahead`）時從頭來過，新資料庫的新工作會通知、舊歷史不會（#3311，關 #3303）。

### Added：wwd 的佇列與資料庫

- **資料庫 epoch 與超前游標**（佇列 schema 5）：`GET /v1/jobs` 回 `{jobs, cursor, epoch}`；`?after=` 大於 head 回 409 `{reason:"cursor_ahead", head, epoch}`。**還原備份的 `wwd.db` 時，要在 wwd 停止時輪替 epoch**（`UPDATE db_epoch SET id = lower(hex(randomblob(16)));`），否則 client 會把舊游標當成還有效（#3309，關 #3303）。
- **手動插隊**（佇列 schema 6）：`jobs.manual`（`front`／`back`／`hold`）與 `manual_seq`，`POST /v1/jobs/{id}/position`（只在本機 socket；TCP 沒有這條路由，`?host=` 回 400）與 `pdx jobs move <id> front|back|hold|normal`；hold 的工作不能被 claim，手動位置優先於評分（#3317）。
- **工作用量**（主機 store schema 4）：`wire.JobUsage`、`job_usage` 表（保留 30 天）、`GET /v1/usage`（只在本機 socket）與 `pdx wwd usage`（#3336、#3337）。只記錄，不影響任何放行判斷。
  - **取樣**：facts 的每工作累加器沿用 sampler 已有的 5 秒取樣，不增加頻率也不 fork（#3344）；快車道的工作結束時由 `UsageRecorder` 寫列（#3350）。
  - **精確值**：`run.Result.Usage` 回報 CPU 秒數，Linux 沙箱單元另回報記憶體峰值（取自單元自己的 cgroup，由 wrapper 寫進狀態檔）（#3358）；worker 跑完的工作由 `RecordRun` 寫列，只寫「origin 收下結果」的那一次（#3365）。
  - **`lower_bound`**：命令返回後仍有行程留著（tmux、vite、playwright 之類）時，數字只是下限，列上標記，`pdx wwd usage` 的 NOTE 欄顯示（#3363，關 #3359）。
  - **`slot_charged`**：記的是主機放行時實際被扣的名額，不是工作的屬性（#3356）。schema 4 還沒部署到任何一台，以上欄位都是原地加，版本維持 4。

### Added：mod 與觀察紀錄

- **`gate.js`：mode rewrite 沒比對到 task 時改寫成 `pdx test --adhoc -- <argv>`**（#3276 的 ad-hoc 入口）；指令含 shell 展開字元（`$`、反引號、`* ? [ ] { } ~ !`、`\`）或權限判定會變時照舊 advise；開關關閉時行為與 752 相同（#3310）。
- **觀察紀錄的讀取與接線**：`GET /v1/observations`、`GET /v1/observations/undeclared`（socket 與 TCP 都有；socket 上 `?host=` 轉送）、`[observe] min_cpu_seconds／min_mem_mb`（預設 60 秒、1024 MB）、sampler 把行程表交給 Observer（#3304、#3308）。
- **指令簽章**：已知工具（docker、kubectl、helm、git、pnpm、npm、yarn）的全域旗標連值一起跳過；暫存目錄底下腳本名稱的尾端計數去掉（#3314，關 #3297）。

### Added：C2 矩陣的故障掛鉤（預設關）

- `[fault] enabled = true` 加上各掛鉤自己的鍵才生效，沒有路由、旗標或環境變數可以打開；有鍵沒 `enabled` 讓 wwd 起不來；`/v1/info` 的 `fault` 宣告開了哪些（沒開時 JSON 不變）。H1 `log_upload_fail`、H2 `owner_active_file`、H3 `zone`（只設 `zone_override`，真實 `zone` 不變）、H4 `snapshot_delay_s`（H3、H4 的消費端在 4a-4b2／4a-4c 才接）（#3338）。

### Changed：daemon 固定耗用（#3316、#3322、#3323）

- **session 清單一次 `list-sessions`**：一次讀出 session、active pane 的標題與 window 名、pane id 與 tmux instance，建一次清單從 `1 + N + 1` 個 fork 降成 1 個（私有 daemon 量到 17 → 1）；沒有 UTF-8 locale 時 tmux 會把 TAB 與非 ASCII 字元都換成 `_`，所以改用可印的分隔符 `|~|` 加 `tmux -u`，解析結果不可信時整份退回舊讀法。hook 的 PathHint 只在事件沒帶 cwd 時才讀 tmux（#3327）。
- **`projectPane` 用共享 pane 快取；peers 盤點的「前」instance 取自同一次 list**：預期每個 Pre／PostToolUse 再少 1～3 個 `tmux.read`；`verifyEvent` 保持即時讀（#3331）。
- **`agent.ps` 的 fork 改記在 `agent.ps.<做什麼>@<誰呼叫>`**：只加觀測，不改行為，`GET /api/debug/execstat` 看得出 76 次／分是誰呼叫的（#3339）。
- **輕量狀態不再每次查表**（#3322 第 1 項）：`LightStatus` 與 `BackgroundWork` 讀 session 的 roots 改用 1 秒內的複本，寫入 `agent_frames` 的 12 個方法都會使複本作廢；其他呼叫端仍即時讀（#3357）。60 秒 profile 裡 `ListRootsBySessionID` 原本占 0.23 s／分。
- **團隊的 commands／facts 泵保險輪詢 1 秒 → 5 秒**（#3322 第 2 項）：每分鐘查詢 120 → 24 次。輪詢拉長後會浮現的三個缺口與一個競態一起補了（泵啟動時先跑一次 pass；`spawn_resume` 把損壞的 op 標成失敗後補 kick；`afterMemberRelayApproved` 讀不到 op 時仍 kick；已在跑的 drain 不會吞掉 drain 結束邊緣的 kick），各有測試；最壞的可見延遲是 5 秒（原本 1 秒），退避到期的重試最多晚一個 tick；remote notice 泵不動（#3362）。
- **`pdx hook` 每個事件的 fork 3 → 2**（darwin，#3323）：找 sender 的 ppid 與 args 改用 sysctl（`kern.proc.pid`、`kern.procargs2`），只回答本來就能不 fork 答對的情形，讀完再核對一次 ppid 與啟動時間，其餘一律退回 `ps`；與 `ps` 逐位元相同有測試（#3351）。
- 測試：`TestRealExecutorActivePaneMetadata_RealTmuxForgedTitle` 改用私有 tmux server，並用 `new-session -n` 關掉 window 自動改名造成的間歇失敗（#3335，關 #3330）；`ask.test.ts` 改斷言 mod 的等待上限而不量牆鐘，另留一條寬鬆的牆鐘後備（#3347，關 #3307）；`relay.test.ts` 的 51 筆報告佇列測試給 15 秒（#3355，關 #3353）。

### 文件

- **C2 矩陣對照 main 核對過**：案例 2 改用凍結讓租約過期（bootout／stop 是優雅結束，以 `reclaim` 歸還，不計 attempts）、佇列檢查改讀 `.jobs`、案例 9 的狀態是 `cancelled`、案例 5a 的 attempts 是 0、案例 12 與 13 的等待與判斷方式（#3382）；案例 13 的沙箱探測加了對照組（寫工作目錄）、以可連線與否判斷 docker.sock、十個工作目錄外的位置、主機 `/tmp` 的哨兵檔，在 mserv 實測且沒有沙箱時會紅（#3364）。
- **e2e**（`cmd/wwd/e2e_*_test.go`，build tag `e2e`，一般 `go test` 不編進去）：起真的 wwd 行程，驗證兩個 daemon 互見、peer 的 worker 領到 job 並跑完、取消、worker 被殺後租約過期由另一個接手、owner 返回後的回收；harness 在下次啟動時收掉被殺的 run 留下的行程與目錄，只殺命令列能證明是自己的 pid。跑法 `go test -tags e2e -timeout 25m -run TestE2E ./cmd/wwd/`，約 2 分鐘（#3381）。
- C2 真機矩陣的跑法與每個案例的預期（#3299）；spec 補 epoch／`cursor_ahead`、工作路由的介面細節、還原 `wwd.db` 要輪替 epoch（#3313）；mserv 沙箱實測紀錄 `docs/wwd-4d3-mserv.md`（#3332）；`docs/wwd-deploy.md` 新增 §6（mserv 的 MySQL 與預存程序）與故障掛鉤的開關步驟。

### 發版測試

- 切點 `6f4e150da`，air26：`go test -p 3 ./...` 全過、`make lint` 通過；e2e（`go test -tags e2e -run TestE2E ./cmd/wwd/`，7 個 TestE2E）全過。
- mlab，mod 的 hook 測試：全部檔案分段跑，0 失敗；`scripts/` 的三支 shell 測試（`wwd-mysql-setup_test.sh`、`wwd-c2-matrix_test.sh`、`wwd-c2-matrix_stub_test.sh`）全過。
- mserv（Linux x86_64）：13 個 wwd 套件交叉編譯後帶 `testdata` 執行，10 個過、3 個紅，原因都已知：`jobdb` 與 `quarantine` 各有一條守門測試要讀 repo 原始碼，遠端只有測試執行檔，這兩條在 air26 的全套已跑過；`run` 有一條 Linux zombie 的偶發失敗，追蹤在 #3383。
- SPA 與 Electron 自 752 起沒有變，沿用 752 的結果。

## [1.0.0-alpha.752] - 2026-10-18

> daemon、`pdx` CLI、`wwd` 與 **mod**（`cmd/pdx/plugin/` 的 `gate.js`）有改，**兩台都要重跑 `pdx setup`**（寫入新的 mod；`pdx.json` 的 `entry` 開關會保留，預設關）。SPA 與 Electron 與 750 相同。Purdex 的資料庫沒有變，退回 751 不需要還原。**`wwd.db` 再往前遷移**（佇列 schema v4 加變更游標、觀察紀錄表），750／751 的 wwd 會拒絕開啟；部署腳本在升級 wwd 前已在每台留一份 `~/.local/share/wwd/wwd.db.pre-752`。

### Changed：pool 模式下 daemon 不再自己量主機（C1 收尾）

- **resources 的取樣迴圈只讀行程表**：`fleet.source = pool` 而且 pool 讀得到本機時，不再自己讀 sysctl、`vm_stat`、加總 `ps` 的 CPU；壓力區、換頁率、記憶體、load 改用 wwd 的數字；不再寫 `host_minutes`。行程表保留給守門員的偵測與 session 歸屬。閘門的 `pressure.json` 改由 wwd 的壓力等級與換頁率餵，閘門不必自己多跑 `vm_stat`；退回自家取樣時數字不會跳（#3294）。
- **fleet 的本機讀取只在需要時才啟動**：本機由 pool 服務時停掉，pool 讀不到時才啟動；巡檢的磁碟可用空間改用 wwd 的數字（資料目錄與 home 在同一個裝置時才用，否則照舊自己讀）（#3300）。
- 允許保留的例外寫在 plan 的 C1 驗收：守門員偵測用的行程樹、閘門持有者、閘門指令自己的壓力判斷（WWD-9a 才拆）、輔測送出前的遠端探測（WWD-9b）、「檢查連線」、monitor 的每個 session 的 `ps`，以及 pool 讀不到時的退路（#3289）。

### Added：wwd 的佇列

- **origin 變更游標**：`GET /v1/jobs?after=<cursor>` 回傳 `{jobs, cursor}`，每個 job 只出現一次、給最新狀態、依變更順序；只延長期限的 heartbeat 不推進游標（#3293，關 #3251）。
- **工作的讀取、取消與提交路由**（`internal/wwd/jobsapi`）：本機 socket 可以提交，TCP 只能讀與取消、不能提交；log 轉發改成串流、有 2 分鐘總時限（#3301）。
- **本機快車道登記、log 上傳與保留**：`RecordLocal`、`Ended`、`LiveClaim`、log 存 3 天（#3288）；`/v1/local-runs` 接進 wwd，wwd 只建一個佇列、每 5 秒清掃一次；這條路由要等 worker 協定的路由也掛上才會開放，目前不存在（#3292、#3281）。

### Added：觀察紀錄（WWD-6c 的前兩段）

- 被系統接手的 codex 常駐程序，用它環境變數裡的 Claude Code session ID 接回原本的 session（只在找不到 session 祖先時才讀，只留那一個值）（#3287）。
- 觀察紀錄的格式、指令去敏（token、Bearer、`key=value` 等，含引號、跳脫、巢狀替換時整段遮蔽）、指令簽章、`wwd.db` 的紀錄表（保留 14 天）（#3290）；追蹤不屬於任何工作、CPU 超過 60 秒或記憶體峰值超過 1 GB 的指令樹（#3291）。還沒有 API，接上取樣與查詢在下一段。

### Added：接替 `gate.js` 的 hook（預設關）

- `gate.js` 多了 entry 路徑：開關打開時，測試／建置指令比對到 `.wwd/tasks.toml` 的 task 就改寫成 `pdx test`，比對不到依 `[entry].mode` 給建議或拒絕；`pdx entry match` 出錯一律退回舊行為。**開關預設關，關閉時行為與 751 逐位元組相同**（#3279）。

### 文件

- spec：變更游標與 `?after=` 的語意（#3295）；C1 的例外清單與 2d-2（#3289）。
- C2 真機矩陣腳本（14 個案例，只寫還沒跑；#3246）。

### 發版測試

- 切點 `21dae1971`，air26：`go test -p 3 -timeout 30m ./...` 全過、`make lint` 通過（一格名額依序跑）。
- mlab，mod 的 hook 測試（6a-2 改了 `gate.js`，照 748 的跑法）：relay 分四段、events 分三段全過；其餘檔案 269 條過、2 條紅——兩條都是 `ask.test.ts` 的「5 秒內回來」計時斷言（實測 5.04 秒、5.27 秒），這個檔這版沒有改，負載下偶發（748 也出現過）；單獨重跑 `ask.test.ts` 78 條全過。
- mserv（Linux x86_64）：`internal/wwd/{facts,derived,labels,clones}` 交叉編譯後帶 `testdata` 執行，全過。
- SPA 與 Electron 與 750 相同，沿用 750 的結果。

## [1.0.0-alpha.751] - 2026-10-18

> daemon、`pdx` CLI 與 `wwd` 有改；SPA、mod（`hooks/`、`cmd/pdx/plugin/`）與 Electron 與 750 相同，**不需要重跑 `pdx setup`**。Purdex 的資料庫沒有變，退回 750 不需要還原。**`wwd.db` 的佇列 schema 升到 v3**（新增租約失效次數），只能往前遷移，750 的 wwd 會拒絕開啟；部署腳本在升級 wwd 前已在每台留一份 `~/.local/share/wwd/wwd.db.pre-751`，要退回 750 的 wwd 時用它還原。mserv 的 `wwd.service` 改進 `wwdd.slice`（重跑安裝會更新 unit）。

### Fixed：pool 模式

- **pool 讀不到的主機留在 SSH**：切到 `fleet.source = pool` 時，某台主機只要在 pool 裡沒有可讀的新鮮資料（沒列出、列著但連不上、還沒有資料、停止更新），那一台就繼續用原本的 SSH 探測（daemon 自己那台用本機取樣），pool 恢復後再自動切回，切換期間不會兩邊同時探。750 在 C1 切換時，還沒裝 wwd 的 air19 因此被顯示成離線，這版修好；「新鮮」看的是對方時間戳最後一次變動的時刻，不拿對方的時鐘跟本機比（#3283）。
- **pool 模式的 `pdx host report` 不再是近似值**：wwd 的每分鐘彙整帶有逐樣本的統計時，報表直接用它（full 的次數與最長持續、各壓力區的秒數、執行中工作的峰值）；只有更早、沒有統計的分鐘才用 750 的近似算法，報表與 CLI 的備註分成兩種（#3272）。

### Changed：wwd

- **「主人在用」改讀 `IOHIDSystem` 的 `HIDIdleTime`**（air26 的 owner 規則 1）：原本讀的 CoreGraphics 數值在 air26 上每次都比 `ioreg` 小很多（1575 秒對 24871 秒），會讓 worker 以為主人一直在用；改讀之後與 `ioreg` 一致，跟 spec 的寫法相同（#3280）。
- **mserv 的 `wwd.service` 進 `wwdd.slice`**：與 mysql、livekit 等正式服務所在的 `system.slice`，以及 wwd 工作的 `wwd.slice` 分開；加上 `CPUQuota=100%`，保留 `MemoryMax=512M`、`TasksMax=256`；不再設 `NoNewPrivileges`，之後的工作要經 `sudo systemd-run` 啟動（#3275）。

### Added：wwd 的佇列與本機執行

- **跨主機工作的租約**（WWD-3b-3a）：`claim`、`start`、`heartbeat`、`release`、`result`，租約失效由佇列的清掃處理，來源重啟有寬限；`attempts` 計每一次退回與失效，「2 次就判遺失」只算租約失效；已要求取消或已被取代的工作在退回或失效時直接結束、不重排（#3278）。這些在 C2 驗收前還沒有主機會去接工作。
- **`/v1/local-runs`（快車道工作登記到 wwd）的伺服端**：先查本機 lock 表，被占時回 `409` 並列出佔著的 job（`holders`），工作結束的事件釋放 lock，定期對帳；尚未接進 daemon（#3270、#3274）。
- **`pdx test --adhoc -- <指令…>`**：未宣告的指令當作 ad-hoc 工作，只在本機、走閘門的重名額、不經 shell、不送佇列，輸出即時印出；`--detach` 會被拒絕（exit 4）（#3276）。
- **`pdx entry match`**（接替 `hooks/gate.js` 的第一半，WWD-6a-1）：依 repo 的 `.wwd/tasks.toml` 判斷一個測試／建置指令該改寫成哪個 `pdx test` 任務；hook 還沒接上，行為不變（#3273）。

### 文件

- spec：租約到清掃時才結束、失效次數與嘗試次數分開計、保留手動插隊、`wwdd.slice`、`--adhoc`、pool 單一主機退回 SSH（#3282）；owner 規則只寫量到的事實（#3285）。

### 發版測試

- 切點 `4377b97c7`，air26：`go test -p 3 -timeout 30m ./...` 109 個套件全部重新跑過、全過；`make lint`（含 `GOOS=linux go vet` 的 wwd）通過。go test 與 lint 依序只用一格重名額，另一格留給同時在跑的 iOS UI 測試。
- mserv（Linux x86_64）：在 mlab 交叉編譯 `internal/wwd/{facts,derived,labels,clones}` 的測試，帶 `testdata` 在 mserv 執行，全過。
- SPA、mod 與 Electron 與 750 相同，沿用 750 的結果（vitest 1074 個檔、19743 條，`tsc -b --force`）。

## [1.0.0-alpha.750] - 2026-10-18

> daemon、`pdx` CLI、SPA 有改，另外多了一支新的執行檔 `wwd`（`make build-wwd` 產出 `bin/wwd-<os>-<arch>`）；mod（`hooks/`、`skills/`）與 Electron 沒有改（`git diff 3e5023921..origin/main` 在這兩處為空）。mod 沒變，**不需要為 mod 重跑 `pdx setup`**；但 wwd 不會自己裝上：一般的 `pdx setup` 不碰 wwd，要用 `pdx setup --wwd --wwd-alias <主機名> [--wwd-binary <路徑>]`（mlab、air26）或 `scripts/wwd-deploy.sh [--token-file <檔>] <主機>`（含 mserv、air19）安裝，細節見 `docs/wwd-deploy.md`。不裝 wwd 時，這一版的行為與 749 相同，只是 daemon 少了租約。
> **Daemon 讀 wwd 預設關閉**：`fleet.source` 預設仍是 `ssh`，要在 fleet 的主機列把來源改成 `pool` 才會改讀 wwd。
> **租約已移除**：`pdx lease` 只剩 `pdx lease run`（閘門的別名）；daemon 的租約路由、引擎與型別都刪了，閘門早已取代它。
> 資料庫：`resources.db` **沒有新的遷移**，租約相關的表（`resource_leases`、`resource_positions`、`notices`）照舊建立與遷移，只是 750 不再讀寫，會在 WWD-9 才刪；退回 749 不需要還原資料庫。新資料庫 `wwd.db` 由 wwd 自己建立，只有安裝了 wwd 才會有，749 的 daemon 不讀它；它的表用 `host_schema`、`queue_schema` 兩張版本表只向前遷移，**舊版 wwd 會拒絕開啟新版 wwd 寫過的 `wwd.db`**。其餘資料庫沒有新增表或欄位。
> 新檔案與新目錄（都只在安裝 wwd 之後才出現）：`~/.config/wwd/config.toml`、`~/.config/wwd/pool.token`（0600）、`~/.local/share/wwd/bin/wwd`、`~/.local/share/wwd/wwd.sock`、`~/.local/share/wwd/wwd.db`、`~/.local/share/wwd/wwd.log`、`~/.local/share/wwd/local-runs/`（快車道的 log 與報告）；macOS 是 `~/Library/LaunchAgents/gs.wake.wwd.plist`（LaunchAgent `gs.wake.wwd`），mserv 是 `/etc/systemd/system/wwd.service`；wwd 監聽 tailnet 位址與 127.0.0.1 的 7871。gate 目錄另有 `wwd-notified.json`（工作結束通知的去重紀錄）。repo 根多了 `.wwd/tasks.toml`、`scripts/wwd-deploy.sh`、`docs/wwd-deploy.md`。

### Added：wwd（每台主機一個，量測主機狀態並排隊執行工作的新服務）

- **wwd 本體與安裝**：新的獨立執行檔 `wwd`，有設定檔、以 token 把關的 TCP 通道、本機 unix socket 與 `/v1/info`（WWD-1a，PR #3198）；跨主機共用同一把 pool token；`wwd install|uninstall`、`pdx setup --wwd`、`scripts/wwd-deploy.sh` 可重複執行，冪等安裝 binary、設定檔、token 與 LaunchAgent／systemd unit，每個指令都有 `--dry-run`（WWD-1e，PR #3232）。線路格式（wire）的契約套件另成一個 package（WWD-0c，PR #3195）。
- **主機事實**：macOS 的 CPU、記憶體、磁碟讀數與環形暫存、分鐘彙整（PR #3206）；行程、常駐程式與「這個行程屬於誰」的歸屬（PR #3209）；工具版本與 air26 的 owner 規則（PR #3211）；Linux 的事實與行程快照（mserv，PR #3240、#3256）；衍生判斷（壓力區、可開的 session 空間等）、取樣迴圈與 `/v1/facts`（PR #3215）；`/v1/events`、`/v1/pool` 與 `?host=` 轉送（PR #3218）。
- **每分鐘彙整與統計**：wwd 把每分鐘的 rollup 存進 `wwd.db`，並帶磁碟的 statfs 大小（`total_bytes`、`avail_bytes`，PR #3252）；rollup 另帶逐樣本的 `stats`（取樣秒數、full 的次數與最長持續、各壓力區秒數、執行中 heavy／light 的峰值），`worker.running` 取該分鐘的峰值；`gate_waiting_max` 目前輸出 `null`（wwd 還沒有閘門可以數等待，PR #3269）。
- **worker 與例外**：每台主機的 worker 開關、heavy／light 名額、owner 規則存在 `wwd.db`；例外（`exceptions`，用 `until` 或 `pid` 二選一）可以新增與移除，變更會發事件，也能轉送給別台主機（WWD-1f，PR #3222）。
- **自動標籤與 repo 標籤**：從事實推出 `os:`、`arch:`、`tool:…` 標籤與工具指紋，標籤變動才發 `labels` 事件；設定檔的 `[repos]` 表可以宣告備好的 clone，通過檢查（自己擁有的、有 `.git` 的目錄）才給 `repo:<名稱>` 標籤（WWD-5a，PR #3258）。
- **工作清單（manifest）**：`.wwd/tasks.toml` 的解析與驗證（含 placeholder 規則、定義雜湊、`local` 政策），repo 自己的任務清單把測試三層寫成 task（WWD-3a，PR #3204）；merge 層加上 `go-race`、`mod-changed` 兩個任務（PR #3253）。
- **工作佇列**：工作提交的資料結構與 `interruptible`、`queue_timeout_s`、`adhoc` 等屬性（PR #3203、#3224）；佇列的儲存、提交與取代、查詢（PR #3220）；取消、過期清掃、七天保留與 pin 管理，佇列只解除自己記錄過的 pin，不會動到同一個 repo 裡別人的 `refs/wwd/*`（PR #3243）；主機的 lock 表（PR #3219）。
- **快車道（單檔測試留在本機）**：task 的 `local` 允許時，在呼叫端的工作樹直接跑，不取快照、走閘門的 light 名額，輸出與報告留在 `~/.local/share/wwd/local-runs/`（WWD-3e，PR #3221）；快車道的執行會登記到 wwd，`wwd` 新增對應入口（PR #3242）；lock 被占時改成每 2 秒重試並印出等待訊息，超過 `queue_timeout` 以 exit 6 結束，**不會送進佇列**，`--detach` 則被拒絕（exit 4）（使用者定案 D12，PR #3255）。
- **工作執行與快照**：工作執行器、摘要解析（PR #3214、#3213）；工作結束後清掃帶有工作標記的行程（PR #3216）；origin 端的快照路由與 `refs/wwd/<job id>` 的 Pin／Unpin（PR #3207）、列出已 pin 的 job（PR #3226）；worker 端取回快照（Client、Materialize、Untar，PR #3229），解 tar 改走 `os.Root`（PR #3233）；相依套件快取與就地 checkout（PR #3236）；`dev` 工作在 macOS 以 `taskpolicy -b` 降到背景優先權，owner 使用主機時的讓出狀態機（WWD-4c，PR #3239）。
- **工作結束通知**：新的 `jobnotify` module 聽本機 wwd 的事件，工作結束時通知提交它的 session（成功、失敗、取消、被取代、逾時、快取命中都涵蓋；斷線重連會補抓；快車道的工作不通知，因為呼叫端行程自己會印結果；WWD-3f，PR #3247）。
- wwd 的 client 套件（jobs、worker、exceptions、`WaitJob`，PR #3227）。

### Added：`pdx test`／`pdx jobs`／`pdx wwd`

- **`pdx test <task> [args…] [--class] [--detach] [--json]`、`pdx test --list`**：照 `.wwd/tasks.toml` 執行任務；允許 `local` 的任務走快車道，否則提交到佇列並等待結果，Ctrl-C 只中斷等待、不取消工作（印出 `pdx jobs show/cancel` 的提示）。退出碼：工作自己的 exit code、124 逾時、130 取消或被中斷、3 連不到 wwd、4 manifest／任務／參數錯誤、5 沒有主機能接、6 排隊過期（WWD-3c，PR #3231，提交端組裝在 PR #3210）。
- **`pdx jobs [ls|show|log|cancel]`**：預設列本 session 的工作，`--all`、`--state`、`--limit`、`--json`；**`pdx wwd [status|pool|worker on|off|exception start|stop]`**：看本機 wwd 的狀態與整個 pool、切換 worker、設定或移除例外（PR #3237）。

### Added：Purdex daemon 改讀 wwd（pool 模式，預設關）

- **讀取 wwd 的 API**：daemon 只經本機 wwd 的 socket 取資料，新增 `GET /api/pool/hosts` 與 `GET /api/pool/facts/history`，並宣告 capability `pool.v1`；wwd 沒在跑時回 `503 wwd_unavailable`；手機（device token）可唯讀這兩條（WWD-2a，PR #3202）。
- **`fleet.source` 設定**（`ssh` 或 `pool`，**預設 `ssh`**，與 749 完全相同）：改成 `pool` 之後，fleet 的各主機數字改從 wwd 讀，不必重啟；pool 沒列出的主機仍走 SSH，本機不在 pool 時退回本機取樣，wwd 連不上超過 20 秒就整批退回 SSH（WWD-2b，PR #3223、#3230）。
- **pool 模式下其他功能改讀 wwd**：`spawn_resources` 與巡檢讀到的主機讀數（zone 由 wwd 判斷，趨勢未知、15 分鐘平均不填；PR #3248）；monitor 的 CPU、記憶體、磁碟（磁碟用 statfs 的 `total_bytes`／`avail_bytes`，與沒開 pool 時的算法一致；PR #3249、#3259）；守門員偵測 E2 讀 wwd 的記憶體壓力與換頁速率，E1、E3、E4 仍在本機（PR #3254）。
- **`pdx host report` 在 pool 模式**：舊的 `host_minutes`（`resources.db`）接上 wwd 的每分鐘 rollup，在讀取時合併，不複製進 `wwd.db`；預設（`ssh`）來源的輸出與 749 逐位元組相同。**沒有分鐘統計的分鐘是近似值**：full 以該分鐘的平均負載、最低可用記憶體、最高壓力判定、不經 5 秒的 FullLatch，一個 full 分鐘算 12 個 tick；zone 秒數把整分鐘算在該分鐘最差的 zone；樣本不足的完整分鐘算 60 秒；報告與 CLI 會標明近似（WWD-2d，PR #3264）。wwd 在 #3269 之後送出的 rollup 帶 `stats`，但 daemon 端把 `stats` 直接對映進報告的分支**還沒做**，所以 750 的 pool 模式報告仍是近似值（PR #3269）。
- **從 App 改主機的 worker 與例外**：`PUT /api/pool/hosts/{alias}/worker`、`POST /api/pool/hosts/{alias}/exceptions`、`DELETE /api/pool/hosts/{alias}/exceptions/{name}`，只收 admin token（device、一次性 ticket、host principal 一律 403），經本機 wwd 轉送，body 先以 wire 型別嚴格驗證（逾時 12 秒）；宣告 capability `pool.control.v1`。alias 的規則統一為 `wire.ValidAlias`（含 wwd 設定檔的檢查，不接受 Unicode 控制字元；WWD-2f，PR #3265）。

### Changed：閘門與輔測派工

- **租約移除（WWD-0a）**：`pdx lease` 的 ls／acquire／release／confirm／cancel／done 刪除，現在回 exit 2 並說「已移除」；**保留 `pdx lease run`**（閘門別名，仍有外部腳本在用，預計在 WWD-6 才刪；PR #3193）。daemon 側刪掉 `/api/resources/leases*`、`/api/resources/positions*`、admission pass、sweeper、notices 等，`Snapshot` 不再帶 leases／waiters／recent／positions，`resources.changed` 只在新訂閱者連上時送一次；`spawn_resources` 卡的 heavy／queue 清單目前恆為空陣列；磁碟保護（`diskguard.go`）成了沒有呼叫者的休眠程式，保留（PR #3196，−17k 行）。
- **輔測派工自動挑主機**：等待中的閘門 ticket 依「air26 再 mserv」的順序、能不能接該指令、該 repo 是否被占用，挑出可試的輔測主機，以一次 ssh 問對方的 `pdx gate would-wait --auto --json` 決定要不要送（X2b-1，PR #3173）；接進等待迴圈：ticket 產生後送出一次，之後每 30 秒再問；任何失敗都讓 ticket 留在原位繼續等；只有 `assist.json` 開啟輔測的主機（mlab）才會這樣找，`--assist-host` 與 `--wait-max` 會跳過（X2b-2，PR #3180）。
- **閘門等待逾時的判斷**：`gate.Options.WaitedOut` 由 `Gate.Run` 依決定結果寫入，快車道直接採用，不再用耗時推論；exit 75 現在能分清「等待逾時」與「閘門目錄壞、磁碟下限、holder 紀錄寫不進去」等其他拒絕（#3260，PR #3262）。
- **快照**：閘門的快照以呼叫端索引的複本為起點，被 ignore 規則比對到、但已追蹤的檔案不再遺漏（原本會讓輔測主機少一個檔），快照時間 purdex 從 1.5 秒降到 0.25 秒；複本沿用原索引的 mtime，避免 git 的 racily-clean 規則讓同一秒內的編輯看起來沒變（PR #3199、#3235）。
- 閘門快照與 wwd 快照共用一個乾淨的 git 環境，`gitenv.Clean` 丟掉所有 `GIT_*` 變數（含可注入指令的 `GIT_CONFIG_*`），並設 `GIT_TERMINAL_PROMPT=0`（PR #3212）。

### Added：日誌與團隊介面

- **團隊面板在每台主機都停靠**：拿掉「放大（max）」狀態與放大鈕，面板只有標題列、單行、完整三種（舊設定的 max 讀成完整）；日誌卡堆疊不再含團隊卡並浮在面板左邊；「團隊」按鈕在完整／單行與標題列之間切換；成員列顯示目前的任務與最後一次回報（讀 `GET /api/journal/members`，需 `journal.members.v1`）（JV-2a…2d，PR #3151、#3152、#3153、#3154）。
- **日誌分頁依 session 瀏覽**：`GET /api/journal/sessions` 列出 leader 的 session 鏈（接力與 `/clear` 都算），`GET /api/journal?session_id=&page=` 回該 session 的條目（上限 1000、附 `truncated`），宣告 `journal.sessions.v1`；新條目帶 `sid`（JV-1b，PR #3169）。
- **日誌卡預設浮動、可固定**：日誌／路線圖／任務三張卡點外面就關，header 有「固定」切換；待確認 band 與 detail 的每一項有勾選框（宣告 `journal.confirm.v1` 時可勾，否則唯讀），樂觀更新、寫入失敗還原；**拿掉釘選**（所有「釘選」字樣、釘選鈕與 `setPinned` API）（JV-3，PR #3171）。
- **守門員的啟動鈕**依 `starting` 與 `cooldown_until` 結束忙碌狀態：冷卻中立即釋放並鎖住按鈕到冷卻結束（W5e-3，PR #3167）。
- **重指令佇列的執行中卡片**：佔著名額的卡片用淡綠底、綠外框與呼吸點標出（WA-3，PR #3179）。
- **主機格**的排隊徽章縮小（9 px）並再往外 5 px，指示燈右移 2 px，格與格之間不留空隙（WA-1，PR #3182）。
- **新 spawn 的資源檢查（RT-5a）**：主機在 warning／danger 區時，本機新 spawn 不直接開跑，先進 `awaiting_resources` 並出一張 `spawn_resources` 卡，放行才執行；不放行、逾時、CLI 不再續租都會讓 op 失敗並釋放座位；無人值守時 agent 的 spawn 回 409 `unattended_refused`，人打的 `/team spawn` 仍出卡（PR #3181）。

### Fixed

- **`TestMach_VMAgreesWithVMStat` 偶發失敗**：兩次讀值不是同一瞬間，判定改成「vm_stat 落在 Mach 前後兩次讀值的區間內」，最多重試 5 次，`Free < Speculative` 直接判不一致（#3168，PR #3174；只動測試）。
- **`TestProcReaderCPUShare` 不再依賴主機負載**，改與同一行程的 `getrusage` 在同一視窗比較（PR #3228；只動測試）。
- **fleet 測試的資料競爭**：`fakeTimer.Stop` 與時鐘共用同一把鎖（#3234，PR #3263；只動測試）。
- **閘門佇列測試不再受主機記憶體壓力影響**：測的是佇列順序與 rank，不是 admission，所以測試設定把 admission 關掉（#3241，PR #3266；只動測試）。

### 文件

- WWD 的 spec 與 plan（PR #3194），之後的修訂：量測結果與鎖、ad-hoc 的裁定（PR #3200）、Purdex daemon 只經本機 wwd 連到其他主機（PR #3205）、`git_dir` 與快照 pin、manifest 不經 shell 執行（PR #3208）、`sessions_room` 公式與 macOS 標記清掃（PR #3217）、快車道執行的記錄方式與 lock 表（PR #3225）、威脅模型（PR #3238）、清掃只釋放佇列記錄過的 pin（PR #3244）、D12 執行細節與 `[repos]` 來源（PR #3261）、`host_minutes` 讀取時接起而非複製（PR #3268）。
- 輔測派工的決定 12：先 air26，再 mserv，最後 mlab（mserv 取得 A 級，不只跑 PHP；PR #3177、#3178）。
- 新增 `docs/wwd-deploy.md`（四台主機的安裝、檢查、升級、退回與移除步驟，含在 PR #3232，補 `[repos]` 範例在 PR #3258）。

### 發版測試

- 切點 `c9286a83f`，air26：vitest 全套 1074 個檔、19743 條全過（`--maxWorkers=3`），`tsc -b --force` 通過；`go test -p 3 -timeout 30m ./...` 全過，`make lint`（含 `GOOS=linux go vet` 的 wwd）通過。
- mserv（Linux x86_64，wwd 第一次安裝的主機）：在 mlab 交叉編譯 `internal/wwd/{facts,derived,labels,clones}` 的測試，在 mserv 執行全過（facts 59 條，含讀這台真實 `/proc` 的 `TestSystemSourcesReadThisLinuxHost`）。第一次跑時 facts 有一條紅，是發版腳本沒把 `testdata` 一起帶過去，不是程式的問題；帶上後重跑全過，腳本已修。
- mod（`hooks/`、`skills/`）與 Electron 與 749 相同，沿用 749 的結果。

## [1.0.0-alpha.749] - 2026-10-17

> daemon、`pdx` CLI、SPA 有改；mod 只改了守門員的 skill（`skills/pdx-watch/SKILL.md`），閘門多了 `xcodebuild`／`swift` 的 shim，**兩台都要重跑 `pdx setup`**（寫入新 skill 與新 shim）。Electron 沒有改。資料庫沒有變，退回 748 不需要還原資料庫；新檔案只有閘門目錄的 `assist*.json` 與守門員的 `session.json`。

### Added：守門員

- **啟動與叫醒的按鈕（W5e-2，PR #3128）**、**事件分頁與推送**（W5e A／B，PR #3101、#3103）、**主機分頁的排隊「歷史」**（W5d，PR #3132；需要 daemon 宣告 `gate.decisions.v1`）：歷史列出每條指令的類別、等候與執行時間、平均核心數、記憶體峰值、換頁拖累。
- **守門員的 skill**（W3b-3，PR #3110）、**daemon 主導的巡檢節奏**（W3b-5b，PR #3130：在場 30 分、無人值守吃緊 10／平穩 15、休息中每 10 分叫醒、磁碟 < 14 GB 立即）、**`WakeWatcher`**：無人值守開關切換時叫醒守門員（W3b-5a，PR #3126）。
- 守門員的 status 與 `watch.changed` 帶 `starting` 與 `cooldown_until`（#3131，PR #3137）；`pdx watch ack` 的剩餘數涵蓋每台主機（#3143，PR #3159）。
- 守門員專區：分頁標籤固定「守門員」、返回鈕、⌘W 回到進入前的工作區（WA-2，PR #3147）。

### Added：閘門

- **佇列順序**：`pdx gate queue front|back|hold|normal`（GQ-b，PR #3118）；`PDX_GATE_RANK=back` 讓一條指令一進佇列就排在後面（PR #3158，air26 的輔測用它讓位本機開發）。
- **互斥規則**：`pdx gate conflict add|remove|list`，兩種指令不同時跑（GC-a／b／c，PR #3124、#3129、#3135，#3058）。
- `pdx gate set --expect`（W3b-4，PR #3114）、`pdx gate free <class>-<k>`（W4，PR #3148）。
- **指令歷史 API** `GET /api/gate/decisions`，紀錄多了 `cpu_s`、`max_rss_mb`、`major_faults`（A1，PR #3123）。
- **`xcodebuild`、`swift build`／`swift test` 進閘門算重指令**（#3111，PR #3149；絕對路徑呼叫仍會繞過）。

### Added：輔測主機派工（X1、X2a）

- `pdx assist`：主機、開關、允許名單、mlab 保留（發版期間），`pdx gate hosts` 看這個 session 能用哪些輔測主機；開關與允許時通知 session、叫醒守門員（X1a-1…4、X1b，PR #3115、#3119、#3122、#3125、#3133）。
- 送到輔測主機執行的機制：送出中的 ticket、派送名額、`--wait-max`、遠端執行與串流、失敗與收回退回原位（X2a-1…3，PR #3141、#3146、#3162；自動選機在 X2b，還沒做）。

### Added：日誌與團隊

- 日誌：手動勾選待確認、拿掉釘選（JV-1a，PR #3144）；成員狀態 API（JV-1c，PR #3145）；無人值守面板用團隊名稱稱呼 lead（JV-5a，PR #3140）。
- 「已寫好接力檔」的清單、小標與 half 結束提示（RA-3，PR #3090、#3091）。

### Fixed／效能

- **daemon CPU 突增（#3092）**：驗證 hook 時不再 fork `ps`（每次 48.5 ms → 0.18 ms，PR #3136）；team sweeper 每輪只讀一次 peers registry（PR #3150）；hook 只讀該 session 的 frames 並共用 pane 列表（每次 1.8 ms → 0.2 ms，`tmux list-panes` 從每次一次降到約每 2 秒一次，PR #3161）。
- **模擬器的記憶體被重複加總**：樹超過門檻時改用 footprint，`launchd_sim` 樹只看 CPU（#3072，PR #3134）。
- 測試：`main.test.tsx` 的啟動測試不再在負載下逾時（#3027，PR #3165）。
- 輔測派工的子行程改經 `execstat` 計數（PR #3170）。

### 文件

- 輔測派工器 spec 補「mlab 發版中」保留；A1 plan（PR #3121）；journal v5 spec＋plan（PR #3138）；fleet spec（PR #3139）。

### 發版測試

- 切點 eba48d594（之後 merge 的歸 750）；部署的執行檔建在這個切點的 bump commit 上。全部在 air26 輔測跑。
- vitest 全套 1066 檔／19631 項全過（`--maxWorkers=3`）、`tsc -b --force` 綠、`make lint` 綠；`go test -p 3 ./...` 在 39248b1d9 上只有兩紅：
  - `internal/execstat` 的 fork 守門測試：輔測派工的 4 個檔直接用 `exec.Command`（各張 PR 都沒跑這個掃描整個 repo 的守門測試）→ #3170 修正（daemon 側改走 `execstat`，CLI 側登記 `cliOnly`），切點改到它之後，重跑 `execstat`、`gate`、`module/assist`、`make lint` 全綠。
  - `internal/hostmem` 的 `TestMach_VMAgreesWithVMStat`：兩次讀取之間空閒頁數差 27887（容許 20000），主機滿載所致；單獨跑 3 次全綠、749 沒改 hostmem → 開 #3168。
- mod 的 hook 測試沿用 748（hooks 未改）。Electron 未改。

## [1.0.0-alpha.748] - 2026-10-17

> daemon、`pdx` CLI、SPA 與 **mod** 有改（RT-4a，mod 協定 9），**兩台都要重跑 `pdx setup`**；Electron 沒有改。mlab 與 air26 要同版（member 安靜規則與 roster 欄位兩邊一起看）。
> 資料庫：team.db 的 `mod_hello` 多一欄 `idle_intercept`（預設 0）。退回 747 不需要還原資料庫：747 寫這張表時列出欄位名，不碰新欄位。

### Added：守門員

- **啟動與叫醒守門員（W3b-1，PR #3100）**：`POST /api/watch/start`、`/api/watch/wake`（只收 admin token）與 `pdx watch start|wake`；沒有醒著的守門員時，叫醒上一位或在第一台可用的主機上開新的，失敗後冷卻 30 分鐘；同一台一次只開一個，重啟中途不會開兩次。
- **巡檢模式的守門員閒置後會休息（W3b-2，PR #3107）**：閒置到設定時間就先通知「該休息了」，等它回答（或 5 分鐘）再送 `/exit`，必要時才 SIGTERM、SIGKILL；通知之後有新事件就取消休息。常駐模式不休息。
- **事件可以標成已處理（W2b-2a／2b，PR #3077、#3085）**：`pdx watch ack <id>… | --all --note "<一行>"`、`pdx watch log [--host|--all]`；守門員在一台、事件記在別台時，ack 會轉給記錄的那台處理（不再往下轉）。
- **主機分頁的重指令排隊區（W5b1，PR #3061）**：排隊的車道、閘門狀態列、等候中的卡片與表格；agent 圖示在指令排隊時顯示沙漏，滑過可看位置、等了幾分鐘與指令（W5b2，PR #3097）。
- **守門員專區的「狀態｜設定」與觀察主機編輯器（W5c1，PR #3096）**；zh-TW 的「只探查」改名為「觀察主機」。
- 主機的高耗用行程改用實體記憶體（footprint，含壓縮頁）排序與顯示，讀不到時標 RSS（#3057，PR #3081、#3082）。

### Added：閘門

- **名額設成 0 就暫停這一類（W3c，PR #3106）**：新的同類指令排隊、原因顯示 paused，已在跑的不受影響；停用中的閘門不顯示暫停。
- 佇列順序（front／back／hold）的資料層與排序（GQ-a，PR #3112）：這一版還沒有可以設定順序的入口，行為不變。

### Added：團隊與接力

- **member 閒置時的自動壓縮改由 lead 決定（RT-4a，PR #3067）**：mod 擋下 member 閒置期間的 auto compaction，daemon 依權威角色每個閒置期通知 lead 一次。
- **member 安靜規則（MQ-1／MQ-2／MQ-2b，PR #3088、#3086、#3104）**：team member 的等待與閒置不推播、未讀不顯示也不計數；判定 member 時核對 tmux 世代，tmux 重開後同名的新 session 不會被當成 member。
- roster 帶 `tmux_instance`、`relay/fresh` 帶 session code、半成品接力卡以 `half` 收卡（PR #3095）。

### Added：日誌

- **標題列的「團隊／日誌」按鈕與卡片疊（J-2b，PR #2980）**；日誌卡的篩選、人名標示、ref 連結、釘選（J-2c，PR #3054、#3059、#3064）；任務與路線圖分頁（J-2d，PR #3066）。

### Changed

- 主機格：主機圖示一律亮態，滑過顯示該主機讀數的資訊卡（PR #3065，使用者 10-17）；點擊不再畫外框（PR #3093）。
- 閘門：`make lint` 算 heavy，外層不再佔輕名額等內層的 `go vet`（#3089，PR #3105）；go 旗標值補齊、最多 2 個套件的 `go test`／`go vet` 算 light、`tsc -p` 與裸 `tsc` 算 heavy（#3073，PR #3083）。
- 重構：tmux 的檢查並輸入 helper 搬到 `internal/tmuxpane`（RT-7a，PR #3094，純搬移）。

### Fixed

- 可用記憶體重複加了 speculative 頁，air26 約高估 530 MB（#3074，PR #3084）。
- `/Applications` 底下的常駐 app（如 Herd）只有記憶體超標時不再報 unmanaged_heavy，CPU 超標照報（#2965，PR #3098）。

### 文件

- 閘門互斥規則（#3058，spec §5.3、plan GC，PR #3060）；輔測主機派工器 spec 與 plan（PR #3109）。

### 發版測試

- 切點 6820e2543（之後 merge 的歸 749）；部署的執行檔就建在這個切點的 bump commit 上。
- **air26 輔測**跑主測試（使用者 10-17：輔測開著時相容測試一律先送 air26）：vitest 全套 1032 檔／19248 項全過（`--maxWorkers=3`）、`tsc -b --force` 綠、`go test -p 3 ./...` 綠、`make lint` 綠。
- **mlab** 跑 mod 全部（照 741 分段）：relay 四段、events 三段全過；其他檔 213 過 2 紅，都是 `ask.test.ts`「5 秒內回來」的時間斷言（5.1／5.5 s）。對照：747、RT-4a 前、RT-4a 後同檔的四條耗時隨當下負載在 3.2–4.9 s 浮動（RT-4a 前的版本反而最慢），ask 的程式自 747 前未改、748 的 mod 與 RT-4a 後相同；748 重跑 78/78 全過。判定為負載型假紅，開 #3127 讓斷言不受負載影響。
- 腳本缺口：mod「其他檔」那段原本直接呼叫 `claude plugin test`，沒有經過閘門（守門員發現），已改包 `pdx gate --class heavy`。Electron 自 747 未改。

## [1.0.0-alpha.747] - 2026-10-17

> daemon、`pdx` CLI 與 SPA 有改，mod、Electron 沒有改，**不需要重跑 `pdx setup`**。資料庫沒有變，退回 746 不需要還原資料庫。
> ⚠️ **Profile Sync 的 settings 序號 10 → 11**（#3047）：升級後確認沒有裝置停在舊版。還停在 10 的裝置整份 profile 會鎖成 `locked:schema`（不寫出也不拉取，不會遺失或覆蓋資料），升級後自動恢復；會踩到的只有連不到 mlab dev server、改用內建 renderer 的 Mac App。

### Added：守門員

- **事件投遞給守門員（W2b-1，PR #3042）**：監看偵測到的事件排進 outbox、同類摺疊、失敗重試，新守門員接手時補送積壓；跨主機走 daemon 對 daemon 的 `POST /api/peers/watch/event`，收件端依事件 id 去重並持久化，由守門員自己的收件匣送進對話。一則訊息帶的是事件而不是文字、有大小上限，收件端永久拒收的事件會被丟棄而不卡住佇列。
- **守門員專區（W5a2、W5a3，PR #3044、#3056）**：主機分頁的「資源」有儀表、圖表與時間範圍；側欄的盾牌入口顯示守門員狀態，守門員分頁顯示守門員所在主機的 `pdx-watch` 終端機。App 重開後第一輪還沒讀到狀態時不會改寫分頁（也不會因此同步蓋掉另一台）。

### Added：主機狀態

- **主機格改用 App 的主機徽章、成員環與珠子排法（PR #3033）**，並可在設定切換指示：環（預設）／CPU＋記憶體壓力雙柱／CPU＋記憶體用量雙柱（主機格指示，PR #3047；使用者 10-16 定案兩種都要）。
- **觀察主機有自己的入口、樣式設定與連線檢查（F-2d，PR #3039）**。

### Fixed

- **主機外觀改了，主機格沒跟著變（PR #3063，使用者 10-17 回報）**：主機外觀改變或新加入時，接下來的 fleet 推送帶整列資料；觀察主機存檔後 App 立刻重讀。
- **閘門：`--help`、`-h`、`--version` 與 help 子指令不佔名額（#3043，PR #3051）**：適用所有被分類的工具；help 字只有在不可能是某個選項的值、也不是 script 參數的位置才算說明請求（`pnpm run build --help` 的 `--help` 是給 script 的，照樣佔名額）。
- **守門員誤報 slot_long 292 年（#3049，PR #3055）**：閘門拿到名額的鎖就先寫一筆帶 pid 與開始時間的暫時紀錄（暫存檔＋改名），監看遇到沒有紀錄或零值開始時間的名額不發事件，只記診斷。
- 守門員專區的主機名稱改走主機外觀（PR #3069）：W5a3 的守門員分頁與啟動面板原本直接讀 `HostConfig.name`，改了外觀名稱不會跟著變；發版時 `host-look.guard` 抓到。
- 測試：MemoryMonitorPage 三個不穩定的測試改用假時間（#3026，PR #3052）。

### 文件

- lead 改 member 的 model（spec §4.9、plan RT-7，PR #3045）；訊息階段 spec、守門員儀表、probe-check 答覆、SPA 測試步驟（PR #3048）。

### 發版測試

- 切點 91070fb0b：go 全套綠（重名額兩格時發版 go 段放行 go vet／go test 並行，未出現時序假紅）；`make lint` 綠；tsc -b --force 綠。vitest 18800／18801：唯一失敗是 `host-look.guard`（W5a3 兩處直接讀主機名稱，`vitest related` 挑不到掃整個 repo 的守門測試），由 PR #3069 修正後切點改為 95aaa705d；修正的 PR 層：5 支 `*guard*` 測試＋元件測試 108/108、`tsc --noEmit -p` app／node 皆 0、mutation 讓守門測試與新測試皆紅。#3026 的 2 個 MemoryMonitorPage 既有失敗已消失。mod、Electron 自 746 未改，沿用。

## [1.0.0-alpha.746] - 2026-10-16

> daemon、`pdx` CLI 與 SPA 有改，mod、Electron 沒有改，**不需要重跑 `pdx setup`**。資料庫沒有變，退回 745 不需要還原資料庫。

### Changed：閘門

- **`vitest related` 改算重指令（#3024，PR #3040）**：`related` 會沿 import 帶進所有受影響的測試，改到共用元件時等於跑大半套件；改排 heavy 名額、一次一條近滿寬度跑完，不再和其他 vitest 疊在 light 名額裡讓記憶體翻攪。`vitest run <測試檔>`、`-t`、`--changed`、`--project` 仍是 light。
- **一棵打包樹只晚占一條名額（#3031，PR #3036）**：最外層的打包 gate 開跑時就在子行程環境放標記，內層打包 gate（例如 `pnpm run electron:build` 裡的 `npx electron-builder`）不再各自晚占。

### Added：守門員

- **狀態與推送跟著遠端的守門員（W3a-2d，PR #3022）**：`/api/watch/status` 帶守門員所在主機、session、醒著／休息，守門員在另一台時每 30 秒向那台確認一次；有變動時在事件串流送 `watch.changed`（一次變動一次）。W3a 至此完成。

### Added：主機狀態與守門員專區

- **觀察主機的連線檢查（F-2c，PR #3032）**：`POST /api/fleet/probe-check`（管理者限定、裝置 403、不開給其他主機）用一次性 SSH 檢查「SSH 金鑰可用」「讀得到量測」，失敗分類為金鑰被拒／主機金鑰不符／連不到／讀不到量測；每次 15 秒逾時，同一台同時只跑一次，SSH 的原始錯誤只進 log。
- **佇列資料（W5b，PR #3025）**：fleet 的 `slow.queue` 帶名額持有者（不含 token）、排隊者的 session、記憶體壓力擋下的原因與各類是否暫停。
- **守門員專區第一段（W5a1，PR #3029）**與**主機設定側欄的箭頭可以收合（PR #3030）**：App 端，已在 SPA dev server 上生效。

### Fixed

- 測試：`TestGate_WouldWaitBatchAnswersAsOneByOne` 不再在 holder 紀錄寫好前就比對（#3037，PR #3041）。

### 文件

- 閘門佇列順位（提前／延後／暫緩，#3035）、閒置壓縮一律攔截（U-4.3，#3038）的 spec 與 plan。

### 發版測試

- go 全套綠；tsc -b --force 綠；`make lint` 綠（這一版起補回 PR 層的 make lint，涵蓋 744–746 間沒跑的 12 張）。vitest 18526／18528：2 個失敗都是 `MemoryMonitorPage` 的計時測試（#3026），在 745 與 746 單獨重跑同樣失敗，元件自 745 未改，不是這版的回歸。mod 自 745 未改，沿用。

## [1.0.0-alpha.745] - 2026-10-16

> daemon、`pdx` CLI、mod 與 SPA 有改，Electron 沒有改。**兩台都要重跑 `pdx setup`** 才會換上新的 mod（只改 `gate.js`，協定不變）。資料庫沒有變。守門員設定的 `model` 欄位改名為 `agent_command`：745 讀到舊列時在記憶體裡轉換，不會自己改寫；在 745 下用 `PUT /api/hostconfig/watch` 寫過的列只有 `agent_command`，退回 744 前要把它改回 `model` 再寫一次，否則 744 讀不進那一列（退回 744 之後也要再跑一次 `pdx setup` 換回 744 的 mod）。

### Added：守門員（W3a，spec §12.3–§12.4）

- **認出守門員（W3a-2b，PR #3015）**：在 tmux session `pdx-watch` 第一個 pane 裡跑的 Claude Code（cc 的 root agent，session id 與 hook 請求相符）就是這台的守門員；子代理、其他 agent、別的 pane、舊一輪遲到的 SessionStart 都不算。daemon 啟動時、收到 `pdx-watch` 的 SessionStart、有人問狀態（超過 30 秒）與每 5 分鐘都會重新確認，所以**部署後現在手動開的守門員會被自動登記，不用重開**；agent 結束後轉成「休息中」並保留 session id。
- **兩台同時有守門員醒著（W3a-2c，PR #3016）**：每台 daemon 用同一條規則選，醒得最久的留下（同一秒則 host id 小的），與 `targets` 順序、訊息先後無關；另一台的 daemon 會請自己的守門員停下（只通知，不結束行程）。
- **pane 輔助函式（W3a-2a，PR #3014）**：建 `pdx-watch`、判斷 pane 是否停在 shell、組指令列（prompt 以單引號包成一個字、拒絕控制字元）、檢查與打字在同一個 tmux 指令裡完成。這一版還沒有呼叫者（開啟／喚醒／休息在 W3b）。
- **守門員設定與查詢（W3a-1，PR #3012）**：`watch` 設定的 `model` 改為 `agent_command`（啟動守門員的整行指令，預設 `claude --dangerously-skip-permissions --model opus`）；舊的 `model` 寫法讀與寫都會轉換，兩個都帶回 400；一行、512 bytes 內、不得有控制字元；`start_prompt`／`wake_prompt` 4 KB 內、不得有控制字元。agent 模組回報 pane 擁有者時一併帶 pid 與行程啟動時間。

### Added：主機頁的觀察主機（F-2a2，PR #3000–#3002）

- 主方的主機總覽裡可以新增、編輯、刪除只觀察的主機（air19、mserv），挑圖示與顏色，並設定兩台 daemon 各自用的 SSH 別名，存到兩台；任一台寫入失敗時保留對話框與草稿。圖示／顏色挑選器抽成共用元件。

### Changed：閘門

- **打包指令不再被擋，60 秒後補占名額（#2996，PR #3011、#3018）**：`build`／`package`／`pack`／`dist`／`bundle`（含 `:*`）與 `electron:build` 由閘門當父行程立刻開跑，跑超過 60 秒才補占一個 light 名額（名額滿就超額占，不擋、不殺）；子孫已持有名額時不補占；不認得的 `run` 腳本只記錄耗時（10 秒以上）。設定在 `[gate.packaging]`。是否轉送 Ctrl-C 只看是不是在終端機，不再跟有沒有名額綁在一起（終端機下子行程不會收到兩次）。
- **`pdx lease run` 先用閘門的分類（PR #2970）**：`--kind` 只在閘門認不得時才用；`claude plugin test` 改經閘門。`claude` 加進預設的 `intercept.tools`；自己寫過 `tools` 清單的設定要自己加上 `claude`。
- **`tsc --watch`／`-w` 不佔名額（#2989，PR #3021）**：常駐監看不是一次性的編譯；`--watch false` 照舊算一次性。

### Changed：fork 計數（D0c）

- **剩下 17 個檔案的 fork 改走 `execstat`（PR #2992）**：待辦清單清空，pty 改用 `ptyCounted` 清單＋行為測試守門。
- **hook CLI 自己的 fork 也算（PR #3008）**：`pdx hook` 呼叫 tmux／ps 的次數隨事件送到 daemon，記在 tag `hook-cli`（單次上限 32），只做觀測。

### Fixed

- **`make lint` 在 `internal/hostmem` 失敗（PR #3013）**：`go vet` 的 unsafeptr 檢查（743 的 purego 程式碼；`//nolint:govet` 是 golangci 的寫法，`go vet` 不認）。改用 purego 直接傳指標，行為不變。
- 測試：fleet 推送測試不再與量測迴圈競爭（#2990，PR #3006）；team 的無人值守競爭測試改用 self_relay 列（#2997，PR #3003）。

### 文件

- 守門員正式版 spec／plan（#3007、#3010、#3017、#3019、#3020）：守門員在固定 tmux session `pdx-watch`、驗證式登記、`watch.events` 推送、沒有「全部略過」、名額 0＝暫停這一類（W3c）、守門員與使用者調名額不設上下限與頻率限制。守門員專區 spec（B 線，#3009）。

### 發版測試

- go 全套綠；tsc -b --force 綠；mod `gate.test.ts` 78/78（其他 mod 檔自 743 未改，沿用）。vitest 18441／18445：4 個失敗都是高負載下的時序問題，單獨重跑皆通過——`host-look.guard`（掃整個 repo 超過 60 秒）重跑綠；`MemoryMonitorPage` 三個計時重試在 745 與 744 單獨重跑都 40/40，744 也曾失敗，不是這版的回歸（#3026 追蹤）。

## [1.0.0-alpha.744] - 2026-10-16

> 743 的修正版。daemon、`pdx` CLI 與 mod 有改，SPA、Electron 沒有改。**兩台都要重跑 `pdx setup`** 才會換上新的 mod（協定不變）。資料庫沒有變，退回 743 不需要還原資料庫，但要再跑一次 `pdx setup` 換回 743 的 mod。

### Fixed

- **App 看不到主機格（PR #3004）**：743 的 fleet 模組沒有回報自己是否就緒，`/api/info` 因此一直沒有宣告 `fleet.v1`，App 就不顯示側欄的主機格（daemon 端的量測其實正常）。現在模組在啟動成功後回報就緒與目前的角色（主方／副方／關閉），停止後不再宣告；新增用真模組透過 `/api/info` 驗證的測試。

### Changed：閘門

- **每次 Bash 呼叫只問閘門一次（G-D1c，PR #2994）**：一次呼叫裡有好幾個會經過閘門的指令時，mod 改用 `pdx gate would-wait --auto-batch` 一次問完（原本每個指令各開一個 pdx 行程，最多 6 個），答案和逐一詢問相同；pdx 太舊、不認得批次時自動退回逐一詢問。

### 部署備註

- 743 部署時 mlab 先重啟、當時 air26 還是舊版，mlab 當上了 fleet 主方。744 一樣先重啟 mlab，它會發現 air26 排在前面而退成副方，主方回到 air26（使用者 10-16 定的順序 `[air26, mlab]`）。

## [1.0.0-alpha.743] - 2026-10-16

> daemon、`pdx` CLI 與 SPA 有改，mod、Electron 沒有改，不需要重跑 `pdx setup`。資料庫只有新增：`session_predecessors` 表、`agent_frames.prev_session_id`、`last_agent_run.pane_id`（都有預設值），退回 742 不需要還原資料庫。日誌改為預設開啟（兩台在 `config.toml` 加 `[journal] person_name = "Wake"`，不設 `push_projects`）；關閉方式是 `[journal] enabled = false` 再重啟。退回 742 後，各專案的日誌 worktree（`<DataDir>/journal/<project>/wt`）會留在磁碟上，無害，要清再手動刪。新增依賴 `github.com/ebitengine/purego`（不需要 cgo，`CGO_ENABLED=0` 照樣建置兩種架構）。

### Added：主機狀態（fleet，spec rev 2）

- **側欄最下面的主機格（F-2b，PR #2995；資料層 F-2a1，PR #2981）**：每台主機一格，主機色的方塊加上負載環（顏色看區域：正常／偏高／過載／離線），有人排隊時方塊右上角顯示排隊數，被記憶體壓力擋住時轉紅。滑過看五項讀數與佇列；窄的活動列改成直排。App 只向主方拿資料（`fleet=v1` 的推送，每秒一次），讀數歷史只留在 App 記憶體。
- **主方與副方（F-1b2，PR #2978）**：順序 `[air26, mlab]`，主方量，副方每 30 秒確認主方還活著；主方沒回應、或有回應但最新讀數超過 10 秒，副方就接手，不切回。主方還沒有任何讀數時視為活著（不換手）。只有通過驗證、且在順序內的主機能呼叫 `/api/peers/fleet/role`、`/announce`。
- **量測（F-1c1 PR #2975、F-1c2 PR #2982、#2986）**：主方用自己 daemon 的讀數量本機（不 fork），其他主機（另一台 daemon 主機、只探查的 air19 與 mserv）各開一條長駐 SSH，遠端迴圈每秒回報 CPU、記憶體、壓力等級、換頁率、磁碟，每 5 秒回報最吃 CPU／記憶體的行程與閘門佇列。實測遠端迴圈在 mlab 約佔一顆核心的 3.5%。`GET /api/fleet`（裝置權杖可讀）；daemon 停止時一定收掉 SSH 與它的子行程（忽略 TERM 的也會在寬限後 KILL）。
- **本機讀數來源（LocalSource，PR #2984）**：resources 模組每秒讀一次本機（`host_processor_info`／`host_statistics64` 經 purego，其餘 sysctl），只在這台是主方時跑；讀不到的項目標在 `missing`，不填 0。
- **主機設定（F-1b1，PR #2972）**：hostconfig 新增 `fleet` 列（順序、這台用來連另一台 daemon 主機的 SSH 別名、開關）與 `probe_hosts` 列（只探查的主機）。部署時寫入：air26 用 `mlab`、mlab 用 `air26`；只探查的主機 air19、mserv（兩台同一份）。**air19 與 mserv 的圖示和顏色要等下一版的主機頁才能挑**，這一版顯示為灰底的預設圖示。

### Added：日誌（journal）接上 daemon（J-1e，PR #2900、#2979）

- daemon 接上日誌：每個 lead／solo 回合多一次 Haiku 呼叫（走 mod，不在主機上開行程），drainer、任務鏡像（每 5 秒）、git 迴圈（每 10 分鐘，只在本機 commit）開始跑；`journal.v1` 能力亮起。**畫面這一版不變**，App 仍顯示工作簿；日誌的卡片介面在之後的版本。

### Added：往回翻到接力前與 `/clear` 前（S-1，PR #2878）

- 對話的 snapshot 多一個 `window.predecessor`（上一段的 session、原因是 relay 或 clear、時間），App 可以用同一支 API 以舊 id 往回讀。

### Changed：閘門（負載控制）

- **閘門回報是否被壓力擋住（PR #2971）**：`pdx gate status` 與 `--json` 多 `Admission {held, reason}`，和放行用的是同一條規則。
- **單一專案的 tsc 是輕指令（PR #2988）**：`tsc -p <project>`（不帶 `-b`）歸輕指令，`pnpm exec`／`pnpm -C <dir> exec` 的寫法裡外一致；`tsc -b` 與不帶參數的 tsc 仍是重指令。
- **呼叫者不在的排隊自動退出（PR #2991）**：排隊中的 `pdx gate` 發現父行程變了就放棄排隊、不執行（結束碼 129，紀錄為 `orphaned`）；刻意脫離終端機的背景工作可設 `PDX_GATE_KEEP_ORPHAN=1`。
- **決策紀錄帶指令、cwd、session（PR #2959）**：`decisions.jsonl` 每筆多 `argv`、`cwd`、`session`，巢狀的標 `nested`。
- **記憶體讀數統一（G-D1a／b，PR #2961、#2962）**：閘門、resources、monitor 共用同一個 `vm_stat` 解析；daemon 跑著時，每次取樣順便寫閘門的 `pressure.json`，閘門 CLI 不必自己 fork。

### Added：開子行程計數（去重計畫 D0，PR #2963、#2987）

- daemon 開子行程一律經 `execstat`（帶呼叫點標籤），`GET /api/debug/execstat` 看各呼叫點的次數與耗時；AST 守門測試擋新的直接 `exec.Command`。第一批 18 個檔已改走計數（含 fleet 的 SSH）。

## [1.0.0-alpha.742] - 2026-10-16

> 只有 daemon 與 `pdx` CLI 改，mod、SPA、Electron 沒有改，不需要重跑 `pdx setup`。`host_minutes` 多三個欄位（新增欄位、預設 0），741 的 daemon 照樣讀寫，退回 741 不需要還原資料庫。部署後主機的區域（normal／warning／danger）改由記憶體壓力與換頁率決定。

### Changed：資源租約的保留部分改看新訊號（負載控制 L2，spec §6）

- **主機區域改看記憶體壓力與換頁率（L2b，PR #2953）**：與閘門同一組訊號——壓力等級 ≥ 2 或每秒換頁超過閘門的上限（50）是 warning，等級 ≥ 4 是 danger；升級立刻、降級要降一級並持續（warning 要換頁率低於上限的 0.9 倍）；兩個訊號都讀不到時是 unknown，不再當成 normal。CPU 負載只作參考。換頁率用取樣本來就讀的 `vm_stat` 計數，沒有多 fork。`mem_warn_pct`／`mem_danger_pct` 照收但不再影響 daemon 的區域（fleet 仍用，L3 之後才刪）。
- **每分鐘紀錄改記閘門（L2b）**：`host_minutes` 新增 `heavy_running`、`light_running`、`gate_waiting`，從閘門的名額紀錄讀（只看、不清不鎖）；租約欄位寫 0。
- **報表只讀每分鐘紀錄（L2c，PR #2956）**：`GET /api/resources/report` 不再讀租約與排隊位置表；新指令 `pdx host report [--since 24h] [--json]`（重指令分鐘與最大並行、輕指令最大並行、閘門排隊分鐘、各區域秒數；L2 之前的租約口徑另列 `legacy_minutes`）。`pdx lease report` 只印指路訊息。新 CLI 連到舊 daemon 會以 `incompatible_daemon` 結束，不把缺的欄位當 0。
- **hostconfig 讀取放寬（L2a，PR #2951）**：讀已存的設定時，忽略之後要刪的租約鍵；打錯字或新版才有的鍵仍判為無效，避免讀成預設值或在讀回再寫入時刪掉新設定。用 PUT 寫入照樣嚴格。

### Added：負載守門員（還不會通知任何 session）

- **找守門員（W2a-3，PR #2949）**：daemon 依 `targets` 找目前的守門員並快取，比對各台的 `targets` 是否一致；`pdx watch status`、`GET /api/watch/status` 顯示這台看到的守門員、狀態與清單。叫醒與啟動在之後的 W3。

## [1.0.0-alpha.741] - 2026-10-16

> mod 與 daemon 有改，SPA、Electron 沒有改。兩台都要重跑 `pdx setup` 才會換上新的 mod（mod ↔ daemon 協定仍是 8）。hostconfig 多了 `watch` 一列（缺少時用預設值），資料庫 schema 沒有變；退回 740 不需要還原資料庫，但要再跑一次 `pdx setup` 換回舊 mod。daemon 開始在資料目錄的 `watch/` 底下寫守門員的事件紀錄與 `state.json`。

### Removed：mod 端的資源租約（L1b，PR #2947）

- mod 不再有租約 hook：刪掉 `hooks/lease.js` 與 `pdx-lease` skill，不再宣告 `lease.notice.v1`，也不再接收 daemon 的租約通知。重指令一律由閘門處理（L1a 的 `gate.js` 與 shim），新增 `pdx-gate` skill 說明排隊、結束碼與 `pdx lease run` 別名的語意。
- mod 收到不認得的 job（例如退役的租約通知，或之後新增的種類）會回報 dropped／`unsupported_kind`，不再默默吞掉，daemon 的佇列不會因此卡到交付逾時。
- daemon 端的租約（`pdx lease` 其餘指令、資料表、設定）還在，下一階段 L2 才拆。

### Added：負載守門員（spec §12，還不會通知任何 session）

- **偵測接進 daemon（W1b-3，PR #2940）**：daemon 每次主機取樣（5 秒）順便判斷 E1 閘門外的重指令樹、E2 記憶體壓力或換頁率持續過高、E3 gate 已不在但名額仍被佔住、E4 名額佔用過久，事件寫進資料目錄 `watch/` 的每日紀錄（保留 30 天）。不依賴租約模式；取樣失敗的那一拍跳過，寫檔錯誤只記 log。查持鎖行程（lsof）只在 E3 成立時做一次。`pdx watch log` 讀這些紀錄。
- **守門員設定（W2a-1，PR #2943）**：hostconfig 的 `watch` 列除了偵測門檻，還有 `targets`（預設 `[air26, mlab]`）、`mode`（`patrol`／`resident`）、`patrol_idle_min`（120）、`auto_wake`、`model`（opus）、`workdir`（`~/Workspace/wake/mlab-purdex-watch`）、`start_prompt`、`wake_prompt`，寫入時逐欄驗證；`pdx watch config [get [key] | set key=value…]` 讀寫。
- **守門員在哪（W2a-2，PR #2948）**：daemon 記住目前守門員所在的主機與 session（`holder.json`），其他主機的 daemon 可用 `POST /api/peers/watch/holder` 詢問（只開放這個路徑，有每台主機的頻率上限）。找人、叫醒、送達在之後的 W2b／W3。

### Changed

- **`pdx gate status` 列出排隊者（#2941，PR #2944）**：文字版多一段 `排隊 #n  pid  等多久  指令  cwd`；`--json` 新增 `QueueHeavy`／`QueueLight`（先到的在前，每筆 class、place、pid、since、argv、cwd、session）。`pdx gate set`／`profile` 的旗標放在名稱前後都可以。`release` 是內建 profile（等同 `--fg`、保留 `-count=1`），`pdx gate profile list` 會列出它；要加別的設定用 `pdx gate set --profile release …`。

### Fixed

- **閘門記錄的 session 一直是空的（#2945，PR #2946）**：閘門讀的是 `CLAUDE_SESSION_ID`，Claude Code 實際提供的是 `CLAUDE_CODE_SESSION_ID`；現在先讀後者、再退回前者。佔名額、排隊與決策紀錄都會帶上所屬 session。

## [1.0.0-alpha.740] - 2026-10-16

> mod 與 daemon（`pdx` CLI）有改，SPA、Electron 沒有改，資料庫 schema 沒有變。兩台都要重跑 `pdx setup` 才會換上新的 mod（mod ↔ daemon 協定仍是 8）；退回 739 不需要還原資料庫，但要再跑一次 `pdx setup` 換回舊 mod。

### Added：主機負載控制 L1a（mod 端的閘門，spec docs/specs/2026-10-15-host-load-control-spec.md §6、D12）

- **agent 直接呼叫的工具也經過閘門（PR #2929）**：mod 新增 `hooks/gate.js`。agent 的 Bash 直接呼叫 `node_modules/.bin/vitest`、`node_modules/.bin/tsc`、`vendor/bin/phpunit`、`vendor/bin/pest`（含 `./` 開頭）時，自動在前面加上 `pdx gate --auto --`，shim 管不到的這些呼叫也會排隊。權限判定會因此改變的呼叫不改寫。
- **會排隊的前景呼叫自動轉到背景（D12）**：前景的 Bash 呼叫若 `pdx gate would-wait --auto` 回答「會等」，mod 把整段呼叫改成 `run_in_background` 並附一段說明，session 不會卡在前景的時間上限裡等名額（10-15 實測：背景任務跨接力仍會收到完成通知）。全程 fail open，規劃總期限 4 秒。順序固定為閘門先、舊租約後。
- **`pdx gate would-wait`（PR #2923）**：不啟動任何行程、不拿名額，只回答「這一類（或用 `--auto` 依指令文字判斷的這個指令）現在會不會排隊」與原因；不需要 daemon，`--json` 給程式讀。

### Added：負載守門員的核心（還沒接進 daemon，spec §12）

- **偵測核心（W1b-1，PR #2922）**：`internal/watch` 的偵測器，判斷 E1（閘門外的重指令樹）、E2（記憶體壓力或換頁率持續過高）、E3（gate 已不在、名額仍被佔住 15 分鐘以上）、E4（名額佔用超過上限），每個事件一次、持續超標不重響。這一版只有程式庫，daemon 還沒呼叫它。
- **事件紀錄與狀態（W1b-2，PR #2939）**：事件寫進每天一個 JSON lines 檔（保留 30 天，指令列先遮蔽敏感值），偵測器的跨重啟記憶存成原子寫入的 `state.json`；`pdx watch log [--since 24h|7d|日期] [--kind …] [--json]` 讀紀錄（daemon 接線前沒有任何紀錄）。

### Changed

- **`pdx gate status --json` 變輕（#2933，PR #2938）**：機器讀的 JSON 預設只對「gate 已不在」的名額查是誰拿著鎖（每查一次要掃全機行程，約 0.3 秒 CPU），每次從約 0.4 秒降到 0.06 秒 CPU；要完整資料加 `--holders`。人看的 `pdx gate status` 照舊全查。

### Fixed（測試）

- **測試不再碰真實的 gate 目錄（#2932，PR #2935）**：`cmd/pdx`、`internal/gate`、`internal/gate/shim` 的測試一律把 HOME 與 `PDX_GATE_DIR` 指到暫時目錄，並各有一個守門測試。起因是一個舊測試漏下的行程拿著 mlab 真實的重指令名額 3.5 小時。
- **佔名額的測試不再靠牆鐘（#2936，PR #2937）**：改成等檔案才放行，主機忙的時候不會因為佔位的 `sleep` 先結束而誤判。

## [1.0.0-alpha.739] - 2026-10-15

> 緊急修正（738 的重指令閘門大部分沒攔到）。只改 `pdx setup` 寫進 `~/.zshenv` 的區塊，daemon、SPA、mod、Electron 行為都沒有改，資料庫 schema 沒有變。兩台都要重跑 `pdx setup`，舊區塊會被換成新的；退回 738 不需要還原資料庫。

### Fixed

- **閘門的 shim 一定排在 PATH 最前面（#2930，PR #2931）**：738 的區塊只在 PATH 裡「沒有」shim 目錄時才把它加到前面。登入 shell 讀完 `.zprofile`、`.zshrc` 之後，homebrew、nvm 又排到 shim 前面，所以部署後才開的終端機、tmux 與 Claude Code session，shim 雖然在 PATH 裡，`go`、`pnpm`、`npx` 卻都直接執行、沒經過閘門。現在區塊不管 shim 目錄原本在哪都把它移到最前面（不會讓 PATH 變長）；互動 shell 另外在每次出現提示字元前再移一次，蓋過 `.zshrc` 與之後的 `nvm use`。已知限制：舊版 Claude Code 的 shell 快照最後會整個重設 PATH，用那種快照的 session 要重開（或另行處理快照）才會經過閘門；`zsh -ic '<指令>'` 這種不出提示字元的互動呼叫，`pnpm`／`npx` 仍可能是 nvm 的。

## [1.0.0-alpha.738] - 2026-10-15

> daemon 與 SPA 有改，mod 與 Electron 沒有改。這一版新增重指令閘門 `pdx gate`，兩台都要重跑 `pdx setup` 才會裝上 shim（`~/.config/pdx/shims/`）與 `~/.zshenv` 區塊；裝上之後，所有 zsh（含正在跑的 session 的下一個指令）呼叫 `go`、`pnpm`、`npx`、`vitest`、`tsc`、`composer` 都會先經過閘門。資料庫 schema 沒有變，退回 737 不需要還原資料庫；要撤掉閘門可跑 `pdx setup --uninstall-gate`（只移除那個區塊與 shim）。舊的資源租約維持「只記錄」模式（advise），在之後的 L1–L3 才拆掉。

### Added：主機負載控制第一階段（spec docs/specs/2026-10-15-host-load-control-spec.md）

- **`pdx gate`（G1a，PR #2909／#2910／#2911／#2912）**：重指令先拿名額才執行。名額是 `~/.config/pdx/gate/` 底下的檔案鎖，不依賴 daemon；重指令一次一個（`slots.heavy` 1）、輕指令三個（`slots.light` 3），照到達順序排隊，等待時每分鐘印出誰佔著、佔多久、前面幾個。記憶體壓力到第 2 級或換頁率超過每秒 50 頁時不放行新的工作，正在跑的永遠不會被中斷。執行中把並行數設為「核心數 − 2」（mlab 為 8，依剩餘記憶體下修；vitest 上限 3），優先序用 `nice 10`。指令結束時收掉它的行程群組與行程樹，並把名額釋放給所有人；逃過清理、仍拿著鎖的行程（例如測試留下的 tmux server）會被點名，不再佔住名額。腳本裡的巢狀呼叫只佔一個名額；在 codex 的唯讀 sandbox 裡會直接執行並印一行說明。
- **調整與檢視（G1b，PR #2919）**：`pdx gate set <模組>.<參數>=<值>` 改設定（保留註解、只改那一個值，下一個指令就生效）；`[gate.profiles.<名稱>]` 與 `pdx gate profile use|list`；每個工作一行決策紀錄（`decisions.jsonl`：等了多久、為什麼、套用的參數與來源、結束碼、耗時）；`pdx gate status` 列出每個設定的生效值與來源，以及每個名額目前拿著鎖的行程。
- **攔截（G2，PR #2907／#2917）**：`pdx setup` 在 `~/.config/pdx/shims/` 寫入 `go`、`pnpm`、`npx`、`vitest`、`tsc`、`composer` 的 shim，並在 `~/.zshenv` 加一個區塊把它放在 PATH 最前面（已經在就不重複加、不輸出任何文字）；`pdx setup --uninstall-gate` 精確移除。shim 每次執行時才去找真正的執行檔，`nvm` 切版本也跟得上；pdx 不在時 shim 會直接執行真正的工具。分類：`go test` 多個套件或 `./...`、`go build ./...`、vitest 完整執行、`tsc -b`、`pnpm check`、`composer test`、不帶篩選的 `phpunit`／`pest` 是重指令；單一套件、指定檔案或篩選是輕指令；**打包一律不經過閘門**（`pnpm build`、`electron:build`、`vite build`、`electron-builder`、`go build -o`、單一目標的 `go build`、`composer build`、`pdx lease run --kind build`）。沒有子指令的 `vitest` 在 agent、腳本與 CI 裡是一次完整執行，只有人在互動終端機時才算 watch。`pdx lease run` 改成 `pdx gate` 的別名（`--kind`／`--weight` 對應到重或輕，`--ask`／`--queue`／`--wait` 不再有作用）。
- **閘門的預設值依 G0 實測（PR #2908）**：`taskpolicy -b` 在每組乾淨對照都比較慢（10～46%），所以用 `nice`；`go test ./...` 用 8 個並行加 nice 是 382 秒、3 個並行加 taskpolicy 是 448 秒，記憶體壓力都正常；vitest 用 8 個 worker 只快 19%，卻讓記憶體壓力升到第 2 級並多出 101 個逾時失敗，所以上限維持 3。

### Changed

- **daemon 閒置耗用（G3，PR #2901／#2904／#2906）**：peer 清單的讀取共用一份每 2 秒更新的行程表，不再每筆各 fork 一次 `ps`；team 的檢查程序只在資料庫有提交、有申請到期、存活檢查那一拍或滿 5 秒時才讀開著的申請；對話畫面的即時連線在記錄檔沒變大、agent 沒收到新事件時，每拍只做一次大小比對，最多每 2 秒完整處理一次（agent 行程無事件結束時，狀態燈最慢 2 秒更新，原本約 0.5 秒）。
- **peer 名稱與標題一次解析（#2892，PR #2895）**：一份來源清單只查一次名稱與標題。
- **資源租約的負載上限（#2759，PR #2898）**：租約執行中、負載超過 `load_cap_pct` 時先不放行下一個請求。租約仍是只記錄模式，這項會在拆租約時一起移除。

### Added（其他線）

- **審核推播的同意／拒絕按鈕（P-1，PR #2883）**：審核推播直接帶「同意」「拒絕」兩個按鈕；人在電腦前時，推播延後 60 秒才送。
- **工作簿日誌載入（J-2a，PR #2864／#2865／#2866）**：日誌的型別、解析、REST、置頂、檢視記憶，SPA 的日誌 store 與 `journal.changed` 事件分支，以及使用中 tab 的日誌單元常駐載入。
- **fleet 葉套件（F-1a，PR #2885）**：`internal/fleet` 純函式套件，這一版沒有接線。
- **負載守門員的底層管線（W1a，PR #2921）**：取樣器的 `ps` 多一欄指令名稱、`vm_stat` 保留換頁計數、`gate.ClassifyReason`、指令列遮蔽搬到 `internal/redact`。行為不變。

## [1.0.0-alpha.737] - 2026-10-15

> 只有 daemon 改，mod、SPA、Electron 都沒有改，不需要重跑 `pdx setup`。資料庫 schema 沒有變（工作簿仍是 schema 4），退回 736 不需要還原資料庫。兩台主機的資源租約已在 736 部署後正式切到「租約」模式（lease）。

### Fixed

- **daemon 長期吃掉約三分之一 CPU 的元兇（#2887，PR #2889）**：每個 hook 事件存 trace 時，都會把 `agent_trace_chains` 整張表（mlab 剛好滿 10000 列）讀一遍，才決定要不要刪舊的，而且是在持有寫鎖的交易裡讀。現在表裡有多少筆改在記憶體裡維護，每 512 次存檔才重新數一次；超過上限時才從最舊的那端讀，一次刪到上限的 90%，不再每次都剛好修到上限。736 部署後錄的 CPU profile 顯示這一項佔 daemon CPU 約 35%。
- **subagent 的工具事件不再移動主 session 的燈號（#2873，PR #2877，介面線）**：subagent 呼叫工具時帶的是主 session 的 id，以前會把主對話的燈推成「執行中」，造成燈號與未讀亂跳。現在只有造成「等待」的那個 subagent，能在它的權限獲准後把燈清回原本的狀態，其他 subagent 的工具事件都不動主燈。

### Added

- **資源租約：找不到 mod 時記下原因（PR #2894）**：「輪到你了」的通知找不到能接收的 mod、只能改發 App 告警時，log 會記下這個 session 每條 stream 的 mod 版本、最後一次送資料與宣告能力距今多久。用來查 10-15 驗收時一次 61 秒就讓出的 offer。行為不變。
- **工作簿日誌：讀取端（J-1e a，PR #2884，介面線）**：日誌月檔的排序、游標分頁、置頂與待確認計數等讀取函式。這一版沒有接線，不會實際產生任何變化。

## [1.0.0-alpha.736] - 2026-10-15

> daemon 與 mod 有改，SPA 只改版本號，Electron 沒有改。mod 協定仍是第 8 版，但 mod 檔案有改，兩台都要重跑 `pdx setup`。工作簿資料庫升到 schema 4：部署後如果要把 daemon 退回 735，必須連同部署前備份的 `workbook.db` 一起還原（735 會拒絕開比它新的 schema）。兩台主機的資源租約仍是「只記錄」模式（advise），這一版部署後在 mlab 暫時切到「租約」模式重跑驗收，結果由使用者決定是否正式切換。

### Fixed

- **舊版 Claude Code 載入不了新 mod（#2871，PR #2882）**：735 的 mod 在 Claude Code 2.1.296 上載入失敗，部署前就在跑的 session 因此一直停在 734 的 mod。原因是 `events.js` 把 `$` 傳給從 `lease.js` 匯入的函式，而 2.1.296 只允許把 `$` 傳給同一個檔案裡宣告的函式。現在改由 `lease.js` 自己監聽 Stop 事件來讀背景任務清單，2.1.294 到 2.1.300 都能載入。部署時跑 `pdx setup` 後，這些 session 不必重開就會換上新 mod。新增兩個守門測試：一個擋 `events.js` 再把 `$` 傳給 `lease.js`，一個用本機裝的最舊版 Claude Code 對整棵 mod 跑 `plugin validate`。
- **資源租約：巢狀的 `pdx lease run` 不再被排隊（#2759 P2f，PR #2874）**：外層已經用 `pdx lease run` 拿到租約的指令，裡面再跑 `pdx lease run`（例如 Aigora 的 `pnpm check`）時，daemon 從程序的父鏈認出它是巢狀的，直接放行、不另外占一份租約，報表多一欄「nested」。另外，收不到 mod 通知、只能改發 App 告警的「輪到你了」，在告警送出 60 秒後就讓給下一位，不會一直卡到切回只記錄模式。這兩個問題都是 735 部署後第一輪 lease 驗收找到的；這一版仍是只記錄模式，行為不變。

### Added

- **daemon 加上 pprof（PR #2875）**：`/debug/pprof/` 掛在 7860 既有的認證後面，只接受 admin token、只開 GET，已配對的手機一律拒絕。用來找出 daemon 長期約 30% CPU 的熱點，部署後會抓一次 30 秒的 CPU profile。
- **工作簿日誌：team 任務鏡像（J-1c，PR #2868，介面線）**：team 的任務新增與完成會寫成日誌條目，並為每個 team 產生一份 `tasks.md`。這一版仍沒有接線，日誌不會實際產生。工作簿資料庫升到 schema 4（outbox 的唯一索引改為只管屬於某筆紀錄的列）。

## [1.0.0-alpha.735] - 2026-10-15

> daemon、mod、SPA 都有改，Electron 沒有改。mod 協定升到第 8 版，兩台都要重跑 `pdx setup`；姓名池要兩台一起部署才保證跨主機不撞名。工作簿資料庫升到 schema 3：部署後如果要把 daemon 退回 734，必須連同部署前備份的 `workbook.db` 一起還原（734 會拒絕開比它新的 schema）。兩台主機的資源租約仍是「只記錄」模式（advise），切到「租約」模式（lease）另外驗收後才做。

### Added：資源租約修正版 A 完成（#2759，PR #2837／#2838／#2840／#2842／#2844／#2847／#2852／#2859／#2863）

- **CLI**：`pdx lease run`／`acquire` 新增 `--queue`、`--ask`、`--wait`。沒指定時由 daemon 判斷：呼叫者在互動中的 session 底下就排隊（立刻回報第幾位、以結束碼 15 結束，不執行指令），其他情況（launchd、cron、`claude -p`）照舊等待。新指令 `pdx lease confirm <id>`、`cancel <id>`、`done`；`pdx lease ls` 多了「排隊位置」一區；報表多了位置統計。
- **mod**：前景重指令遇到排隊時，這一次不執行，並告訴 session 排第幾位、輪到時會通知、不要改成背景或加 & 繞過。背景重指令（只在互動 session，而且包裝不會改變權限判定時）會包成 `pdx lease run --ask`，在背景等，輪到時 session 會被問要不要執行。被 Claude Code 移到背景繼續跑的前景指令，會等它真的結束才釋放租約（#2841）。指令結束時會帶回 daemon 的最後通知。
- **通知**：「輪到你了」的通知經 mod 送進 session，只在 session 閒置時送，3 分鐘的接續窗口從送到時才開始算。背景指令的確認通知會帶出指令原文、目錄和啟動時間，讓模型認得是自己的指令。session 沒有 mod 時，member 的通知改送給它的 lead，非 member 則發 App 告警。
- **門檻（G1，使用者 10-14 決定）**：主機在「警告」區、負載持平時放行，只有負載上升或進入危險區時才擋；兩次放行間隔 30 秒、先來後到照舊保留。
- 以上只在「租約」模式下生效。這一版部署後兩台仍是「只記錄」模式，行為不變。

### Added：接力 v2 的 RT-3（PR #2839／#2843／#2858）

- **閒置時先寫好交接檔**（mod 第 8 版）：session 閒置的接力申請卡 5 分鐘沒人回應時，mod 先讓 session 寫好交接檔，但不清空對話。狀態列顯示「已寫好接力檔」，並跳一則提示：要接力請打 `/relay` 或 `/relay now`，會直接接上後半（清空並由新對話接手，不用重寫接力檔）；不需要就關掉 session 或照常繼續。之後 session 只要開始做事、手動 `/clear` 或被壓縮，這份交接檔就作廢。
- **`pdx kill <ref> --handoff`**：先讓 member 寫好交接檔，再關掉它。寫完後、關掉前若 daemon 當機，重開後不會自動補關（避免關錯程序），會通知 lead 再下一次。
- App 與推播可以看到哪個 session 有現成的交接檔；清理舊檔時不會刪掉還能沿用的交接檔。

### Added：姓名池（NP-1，PR #2851／#2854，介面線）

- 新開的對話，地址後綴從 ref 前兩碼改成 30 個名字之一，例如 `mlab/purdex-3f-adam`；已經在跑的對話維持原名，接力後沿用名字。兩個活著的對話撞到同一個名字時，比較晚開始的那個會換名字。所以要記下地址留到以後用時，一律用 `<host>/_<ref>`。專案 CLAUDE.md 的地址說明已同步。

### Added：Mac App 跟上接力 v2（RA-2，PR #2829，介面線）

- 拿掉「這個 session 不再詢問」勾選框；核准者顯示下指令的人；無人值守的說明文字改版。

### Added：工作簿日誌的基礎（J-0a、J-1a、J-1b1、J-1b2、J-1d、J-1n，PR #2835／#2845／#2846／#2848／#2849／#2850／#2855／#2856／#2857／#2860／#2862，介面線）

- 日誌的資料層、日誌工作、寫入與版本控制提交的機制都已就位，但這一版沒有接線，日誌不會實際產生。
- 工作簿資料庫升到 schema 3（新增日誌用的表，替既有待辦補編號）。
- daemon 設定新增 `[journal]` 一段（`person_name`、`push_projects`）。使用者 10-14 決定日誌先只存本機，兩台都不設 `push_projects`（wake/purdex 是公開 repo）。

### Fixed

- **開新 member 的啟動期限從 20 秒放寬到 60 秒（#2853，PR #2867）**：mlab 上 Claude Code 冷啟動平常就要十幾秒，負載一高就超過 20 秒，member 還在正常啟動就被判失敗。逾時的訊息現在會寫出等了幾秒、最後卡在哪一步。
- **通知裡遠端 member 的地址（#2833，PR #2836）**：member 在別台主機時，通知裡的地址改寫成 member 那台的主機名。
- push 模組 openState 純搬移到 `open_state.go`（#2777，PR #2834）。

## [1.0.0-alpha.734] - 2026-10-14

> daemon、mod 和 SPA 都有改，Electron 沒有改。mod 協定升到第 7 版，所以兩台都要重跑 `pdx setup`。SPA 的改動按 dev update，或靠 dev server 的 HMR 就會生效。兩台主機的資源租約仍是「只記錄」模式（advise）。

### Fixed：跨主機 member 的通知送不到 lead（PR #2827）

- member 和 lead 在不同主機時（例如 lead 在 mlab、member 在 air26），member 發給 lead 的通知一律送不出去。受影響的通知包括接力申請、用量、閒置、壓縮、接力後換地址等。結果是接力申請 5 分鐘後過期，member 一路用到 Claude Code 自己壓縮。
- 原因：lead 那台找不到 member 的信箱，程式就改用「lead 自己的信箱」當寄件人，被 pdx 當成「自己寄給自己」擋下來。
- 現在這類通知改由 daemon 直接送給 lead，不附回覆地址，訊息開頭會寫明是哪個 member。member 和 lead 在同一台主機時，行為不變。

### Added：接力／team 規則 v2 第二批（RT-2a1、RT-2a2、RT-2b、RT-2c，PR #2803／#2808／#2815／#2819／#2822／#2826）

- context 用到 90% 以上時發出的接力申請是「最後一張」：到期沒人回應時，卡不會關掉，而是讓 session 停止作業（暫停原因 relay_unanswered），等你在 App 核准。
- 解除這種暫停時，那張沒人回應的卡會被當成拒絕，而且在同一個交易裡完成。
- lead 可以用 `pdx relay <ref> --deny` 拒絕 member 的接力申請。無人值守開著時，member 的申請會自動接受。member 最後一次申請沒人回應時，member 也會暫停。
- mod（協定第 7 版）：只有 daemon 確實回覆時，才記下「上次申請時的用量」，daemon 沒開不算申請過。用到 90% 之後不再申請。最後一張卡沒人回應時，原本卡住的訊息會照原樣送出，狀態列會寫「已暫停作業；在 App 核准後接力」。mod 重新載入後，會把那張卡接回來。

### Added：Mac App 跟上接力 v2（RA-1，PR #2823，介面線）

- 因為接力沒人回應而暫停的 session 會亮燈，解除暫停前會先跟你確認。
- 最後一張接力卡會寫「已暫停作業，等你核准」。
- 只有一個 session 自己申請（沒有 lead）的接力卡，到期後 App 會自己重新讀取最新狀態。
- iOS 的對應改動（RI-1、RI-3）已經在 0.6.78、0.6.79 出版。

### Added：Mac 的已讀同步（BG-5，#2769，PR #2805，介面線）

- Mac App 會記錄每個分頁讀到哪裡，你看過的訊息會回報給 daemon，其他裝置的未讀數會跟著更新。

### Added：閒置時先寫交接檔（RT-3a1，PR #2828，daemon 端）

- daemon 支援「閒置半程接力」：session 閒置時先寫好交接檔、不換 session，之後真的接力時可以直接用這份檔。這要等 mod 升到第 8 版（RT-3b）才會用到。
- 這一版實際會變的只有：閒置接力的申請卡，5 分鐘沒人回應就過期，原本是 10 分鐘。另外 team 資料庫多了一張表和一個欄位，舊資料不受影響。

### Changed：資源租約修正版 A 的 daemon 端（#2759，PR #2806／#2807／#2809／#2810／#2814／#2818／#2820／#2824／#2825）

- daemon 已經具備新的排隊方式：重指令不會讓 session 停下來等。輪到時，前景指令會收到「可以發起了」的通知；背景指令會先跟 session 確認，再代為啟動。每個 session 只占一個位置，前一個指令跑完後有 3 分鐘可以接著跑下一個。
- 新增 session 操作自己位置的 API（確認、取消請求、放棄位置、提早釋出），資源快照和報表也會顯示排隊位置。
- 這些只在「租約」模式（lease）下生效。兩台主機仍是「只記錄」模式，所以這一版的行為不變；切到 lease 模式前，還要等 CLI、mod 和通知的部分完成。

### Fixed：dev 模式找不到 pnpm（#2812，PR #2813）

- dev update 打包 SPA／Electron 時，會自己去找 pnpm、node 和 go，不再依賴 daemon 啟動時的 PATH。找不到時，錯誤訊息會列出搜尋過的目錄。

## [1.0.0-alpha.733] - 2026-10-14

> daemon 與 mod 都有改：兩台都要重跑 `pdx setup`（mod 協定升到 6）。SPA 與 Electron 沒有改。兩台主機的資源租約仍是「只記錄」模式（advise）。

### Added：session 裡的 /team 指令，以及不需核准的 /relay（RT-1c，PR #2798）

- 新的 `/team` 指令：在 session 裡打 `/team` 會讓這個 session 成為 lead（不用再等核准卡）；已經是 lead 時，`/team` 會列出 team 和成員。lead 可以用 `/team spawn <任務>` 開 member，用 `/team adopt|release|kill <ref>` 收編、釋出或結束 member。
- 這些指令都會帶一個只用一次的 person token（代表「是使用者本人打的」的一次性憑證），daemon 認得就直接生效。這也解除了 732 的已知限制：無人值守開著時，使用者打的指令不會再被拒絕。
- 舊的 `/lead` 還能用，會被當成 `/team`，但會提示「/lead 將移除，請改用 /team」。
- `/relay` 現在是使用者親手下的指令：直接接力，不需要核准、不扣接力額度。如果剛好有一張接力申請卡開著，打 `/relay` 就等於核准那張卡。
- 接力進行中打的 `/team`（當 lead 或開 member）會排隊，接力結束後立刻執行；如果中間 /clear 換了對話，就不會帶到新對話去。
- pdx-team skill 的說明文字也同步更新。

### Changed：資源租約修正版 A 的資料表（#2759，PR #2802）

- daemon 的 resources 資料庫多了一張「位置」表和幾個欄位（呼叫模式、指令內容、工作目錄等），是為了接下來「session 不必停下來等租約」的新排隊方式做準備。這一版只建表、不改變任何行為；兩台仍是「只記錄」模式。資料表是新增的，舊資料不受影響。

### Fixed

- SPA 的型別檢查（tsc -b）恢復通過：補上 BG-2 測試檔裡缺的欄位（PR #2804，只改測試檔）。

## [1.0.0-alpha.732] - 2026-10-14

> daemon、SPA、Electron 都有改，mod 沒有改，不用跑 pdx setup。Mac 要按 dev update 才會拿到 Electron 的改動（Dock 圖示的數字）；SPA 的改動按 dev update 或靠 dev server 的 HMR 就會生效。兩台主機的資源租約仍是「只記錄」模式（advise）。

### Added：team 分頁群組可以整組拖曳（TG-1，PR #2766，介面線）

- 拖曳 team 的 lead 分頁時，整個群組（lead 和它的成員分頁）會一起移動。
- 一般分頁拖到群組上時，會落在群組的前面或後面，不會插進群組中間。
- 成員分頁只能在自己的群組內換位置。
- team 介面規格的 §4.2 拖曳一節也同步更新。這是純 SPA 改動。

### Added：Mac 的 Dock 圖示顯示待處理的 session 數（BG-2，#2769，PR #2796／#2797，介面線）

- Mac App 的 Dock 圖示上會出現一個數字，代表「有未讀分頁或有待處理的卡（例如 lead 申請卡、hook 卡）的 session 有幾個」。
- 同一個 session 不論有幾個未讀或幾張卡，都只算一次。
- 卡關掉之後，數字會跟著降。重新整理視窗後，數字會重新計算並出現。
- 這需要 Electron 的新版本：要按 dev update 更新 App 才看得到。只有 SPA 那半更新時，什麼都不會送出，也沒有副作用。

### Changed：無人值守時不再自動核准 lead／收編申請；使用者自己打的 /team 指令直接生效（RT-1b，PR #2794）

- 無人值守模式打開時，agent 自己送出的「申請當 lead」和「收編 session（adopt）」不會再被自動核准，而是直接拒絕，CLI 會印出「無人值守模式不開放申請，請等待模式解除後，依情況重新申請」。打開無人值守的那一刻，已經開著的這兩種申請卡會被關掉（取消）。
- 使用者在 session 裡親手打的 /team 指令（lead、adopt、spawn）會帶一個只用一次的 person token（代表「是使用者本人打的」的一次性憑證），daemon 認得就直接生效，不必再按核准。
- 已知限制：要讓 daemon 認得「是使用者本人打的」，mod 要到下一段（RT-1c）才會帶 person token；在那之前，無人值守開著時使用者打的 /lead 也會被拒絕（關著時照舊開核准卡）。目前兩台的無人值守都是關的。
- App 端：開關無人值守的回應多了一個 refused 欄位，舊版 App 會忽略它。

### Changed：資源租約 v2 第三段：保留排隊位置與接回原位（#2759，PR #2791／#2792）

- daemon 現在能替排隊太久的請求「保留位置」：這次呼叫先結束，位置留著，之後同一個 session 再下同一種指令，就接回原位。也能從 `pdx lease run` 的行程往上找到是哪個 session 發起的。
- 輪詢時會帶上 client id，被新呼叫接手的舊呼叫會收到「已被取代」。
- 一次呼叫最多等待時間的預設從 300 秒改為 540 秒（同樣只在排隊模式下有作用）。
- 目前兩台都是「只記錄」模式，這些功能只在排隊模式下才作用，所以這一版不會改變任何行為。排隊的等待方式正在重新設計（session 不必停下來等租約），新設計會沿用這一段。

## [1.0.0-alpha.731] - 2026-10-13

> 只改了 daemon，不用跑 pdx setup。兩台主機的資源租約已經從「排隊」模式切回「只記錄」模式（advise），因為排隊的等待方式要重新設計：新設計會讓 session 不必停下來等租約。這次切換只是設定改變，不在這個版本的程式碼裡。

### Added：已讀同步的推播端（BG-4b，#2790，介面線）

- daemon 現在宣告 capability `session.read_sync.v1`（固定宣告、沒有條件），所以部署後 App（Mac 的 BG-5、iOS 的 BG-3e）才會開始使用已讀同步。這補上了 730 的讀取端（BG-4a，PR #2785）。
- 新增 `[push] read_flush_minutes` 設定（1–120 分鐘，預設 15；只在 daemon 啟動時讀取，沒設就是 15）。
- 裝置可以開啟 `prefs.read_sync`。開啟的裝置會收到已讀資訊：每則推播最多帶 16 筆已讀紀錄（`purdex.reads`）；另外每隔設定的分鐘數送一則背景推播（kind `reads`，不會響也不會跳通知）把累積的已讀送出去；每則 agent 推播也會帶 `purdex.event_cursor`。
- 新增 API：`POST /api/sessions/<code>/read`，session 存在回 204，不存在回 404 `{"error":"no_such_session"}`。
- 已讀的位置與待送的已讀只存在記憶體，daemon 重啟就清空，這是設計如此。

## [1.0.0-alpha.730] - 2026-10-13

> 只改了 daemon，不用跑 pdx setup。介面線的 Mac 和 iOS 部分之後才做。資源租約仍是「只記錄」模式（只記下每個指令會等多久，不會擋任何指令）；新准入規則的一天觀察期從這個版本開始。

### Added：App 角標第二段：核准卡關掉時，用背景推播讓手機上的數字跟著降（#2769，PR #2781，介面線）

- 給用手機看待處理數量的人：以前卡片（例如核准請求）在別處處理掉之後，手機 App 圖示上的紅色數字不會馬上變小。現在 daemon 會在卡片關掉後，悄悄送一則背景推播（不會響、不會跳通知），讓手機自己把數字更新。
- 同一時間關掉好幾張卡時，會等 2 秒合併成一則推播，不會一張送一則。推播裡帶著目前還沒處理的卡片清單；如果讀不到這份清單，就乾脆不送，避免把數字弄錯。
- 只有在裝置設定開啟 `badge_push` 時才會送。新增 capability `push.badge_bg.v1`，`/api/info` 會宣告；推播規格文件也同步更新。
- 原有會響、會跳通知的推播完全沒變。

### Added：已讀同步的讀取端（PR #2785，介面線）

- 給同時用多台裝置的人：這是「在一台裝置看過，其他裝置也跟著算已讀」功能的第一半。App 現在可以告訴 daemon「這個 session 我讀過了」，daemon 會記住每個 session 讀到哪裡（只存在記憶體，重啟就清空），並且只往前記、不會退回去。
- daemon 也會把已讀的消息透過連線轉給有開啟這項功能的 App；沒開的連線收不到。
- 目前 daemon 還沒有宣告這項能力，所以 App 暫時不會用它。要等下一段（BG-4b，推播端）完成後才會對使用者生效。

### Changed：資源租約 v2 第二段：模擬佇列、報表，以及調整後的門檻（#2759，PR #2782／#2783／#2786）

- 「模擬佇列」：租約目前只記錄、不真的擋。現在 daemon 會在記錄的同時，假裝新的准入規則已經上線，替每個指令算出「如果照新規則，它會等多久、為什麼要等」並寫下來。這是為了讓我們用真實資料判斷新規則合不合理，才開始正式啟用。這個等待時間是偏高的估計，因為指令在只記錄模式下其實已經在跑。
- 主機每分鐘的紀錄多記了「這一分鐘有幾秒處在一般、警戒、危險三種狀態」，以及該分鐘記憶體壓力與狀態的最差值。
- 新增 `pdx lease report`（和 `GET /api/resources/report`）：顯示各狀態的時間佔比、指令等待時間依原因分組的統計、最長的幾筆等待，以及模擬佇列的結果。`pdx lease ls` 第一行會顯示主機目前的狀態、用量方向和下次放行的剩餘秒數，排隊清單多一欄 REASON，說明每個指令為什麼在等。
- 第一次實測後調整了「用量方向」的判斷：現在只看記憶體和記憶體壓力，不再看 CPU（CPU 仍照常記錄，但不參與判斷）；觀察視窗從 60 秒拉長到 120 秒，記憶體門檻從每分鐘 1 點改為 2 點。用實測的 545 筆樣本重算，「持平」的比例從 4.3% 升到約 81%，不會再動不動被判成上升。
- CPU 滿載（負載達到核心數）仍單獨算警戒，維持重指令一次一個。
- 注意：之前已經手動儲存過資源設定的主機，舊的數值會繼續生效；目前兩台都沒有這種情況。

### Added：接力 v2 第一段：你親手打的 /relay 直接生效（daemon 端，PR #2780）

- 給自己在對話裡輸入 `/relay` 的人：規劃中的做法是，你親手打的 `/relay` 會直接執行，不再另外開一張要你核准的卡、不扣接力額度，也不受開關或暫停影響。
- 這次只有 daemon 端進來了（以一次性的短效憑證辨識「這是人親手打的」，另有 `pdx relay person` 等指令）；mod 端下一段才會接上，所以使用者看到的行為還沒有改變。
- 規格與計畫文件一併進 repo：`docs/specs/2026-10-13-relay-team-v2-spec.md`、`docs/plans/2026-10-13-relay-team-v2-plan.md`。

### 其他

- 角標規格新增第 7 節「跨裝置半被動已讀同步」（PR #2784，只有文件）。

## [1.0.0-alpha.729] - 2026-10-13

> 只改了 daemon，不用跑 pdx setup。Mac 和 iOS 的部分之後才做（BG-2、BG-3）。

### Added：App 角標第一段：daemon 端的計數規則（#2769，PR #2775，介面線）

- 角標計數認得卡片的種類了：lead、自我接力、member 接力、收回、終端機問答、權限卡都算進待處理數。有些卡片（例如只在終端機裡的問答、收回、權限卡）算進數字，但不會因此發推播。
- 屬於某個 session 的核准卡，數在那個 session 底下（`c:` key）；對不上 tmux session 時，改用它來源 session 的 id。
- team member 只有在這台裝置開著它的分頁時才會推播，不受 `notify_without_tab` 影響。
- 靜音的 session 不再收到 agent 推播（`prefs.muted`）；核准類推播不受靜音影響。
- agent 推播帶一個 member 旗標（`purdex.member`）。新增 capability `push.badge.v2`，`/api/info` 會宣告。
- 角標規格與計畫在 `docs/specs/2026-10-13-app-badge-spec-plan.md`（PR #2770）。

## [1.0.0-alpha.728] - 2026-10-13

> daemon 和 mod 都有改（pdx-team skill 的文字變了），要跑 pdx setup。資源租約兩台仍是「只記錄」模式，新的准入規則還不會擋任何指令。

### Changed：開 member 逾時的訊息分成兩種，並附上主機負載（#2761，PR #2765）

- 啟動上限維持 20 秒（使用者決定）。
- 主機上看得到 Purdex mod 在運作時，逾時訊息改成「主機忙：member 沒在 20 秒內完成啟動（load 1／5／15 分鐘、核心數；記憶體已用 %、壓力），名額已釋出，可以稍後重試」。只有這台主機從沒看過 Purdex hooks 時，才提示 `pdx setup --agent cc`。
- 資源數值讀資源模組最新一次取樣，不另外 fork。跨主機 `spawn --host` 轉發的失敗暫時不帶數值。

### Added：資源租約 v2 第一段：三區、用量方向、新准入規則（#2759，PR #2767／#2768／#2771／#2772）

- 主機狀態分成一般／警戒／危險三區（記憶體 80％／90％、記憶體壓力、負載），加上最近 60 秒的用量方向（上升／持平／下降）。`GET /api/resources` 多了 `zone`、`trend`、`zone_since`；`?samples=1` 可以看最近 10 分鐘的取樣。
- lease 模式的准入改成「三區 × 有沒有租約在跑 × 用量方向」，每次放行後隔 30 秒，拿掉「等滿就超量放行」。取樣器剛開始失敗的前兩次沿用上一次的數值，連續失敗才算未知。兩台目前仍是 advise（只記錄），所以實際上不影響任何指令。
- 每筆申請記下在等什麼（間隔、危險區、警戒且持平、用量上升、排在後面…），並新增門檻、間隔、用量方向、告警等設定欄位，預設值見 spec。
- spec 與 plan 進 repo：`docs/specs/2026-10-13-host-resource-lease-v2-spec.md`、`docs/plans/2026-10-13-host-resource-lease-v2-plan.md`。

### Fixed：測試留下永不結束的 tmux server（#2760，PR #2776）

- `internal/tmux` 的測試開私有 tmux server 時，會讀到使用者 `~/.tmux.conf` 裡的 `exit-empty off`，清理後 server 不會結束，也連不到。2026-10-13 當機時累積了 261 個（766 MiB）。現在測試不讀使用者設定，結束時用 label 關掉自己的 server。

## [1.0.0-alpha.727] - 2026-10-13

> 只改了 daemon，mod 沒動。

### Fixed：team 因 lead 消失而結束後，它的遠端 member 收不回來（#2757，PR #2758）

- 2026-10-13 主機當機重開後，三個 team 以「lead 不在了」結束。依設計，team 結束時不改 member 列，所以 member 列還是 active。結果同一台 lead 主機重建 team 後，要再收回另一台主機上的同一個 session 時，被當成「已經是 member」擋下（409 `adopt_already_member`）。
- 判斷遠端 member 還在不在 team 時，現在會排除已結束的 team。新的收回核准時，會把那一列舊紀錄改成「已釋出」，新的 member 列才能生效。本機 member 原本就是這樣做。
- 已結束 team 的「已收為 member」通知不再補送。
- team 結束時仍然不改 member 列（設計 D4 不變）。

## [1.0.0-alpha.726] - 2026-10-13

> daemon 只是搬了檔案；這版主要是 SPA。Mac 按 dev update（或靠 dev server 的 HMR）就生效，mod 沒有改。

### Added：在 team 面板直接授權另一台主機的資料夾（跨主機授權 XR-3，#2755，介面線）

- team 的資料夾欄位寫 `air26:/路徑`，就是授權 air26 上的資料夾；只寫路徑的，仍然是 lead 主機自己的 team 資料夾。
- 每台已配對主機有一個小節，註明「授權整台共用（所有 team）」。每一行會顯示目前狀態：
  - 已生效
  - 等 <alias> 上線
  - <alias> 需要更新
  - 配對失效
  - 套用中
  - 失敗（旁邊有「清除」）
  - 已取消
- 對方沒有允許這台 lead 主機開 member 時，那一節會顯示一行說明。
- 對方離線時不會失敗：變更先記在 lead 主機，對方一上線就自動送過去。
- 要移除的資料夾裡還有 member 在用時，會被擋下，並在那一行列出是哪些 member，要先結束它們才能移除。
- lead 主機的 daemon 太舊時，整段不顯示。編輯器開著、而且還有排隊中的變更時，每 10 秒重新讀一次。

### 其他

- `internal/module/team/relay_handler.go` 依職責拆成 handler／begin／close 三個檔（#2754，關閉 #2741）。純搬移：用 go/ast 逐宣告比對，21 個宣告前後一致。

## [1.0.0-alpha.725] - 2026-10-13

> 只改了 daemon，mod 沒動。跨主機授權的 daemon 端到這版完成；App 的 team 面板（XR-3）接著由介面線做。

### Added：lead 主機可以管理自己在別台主機的授權資料夾，對方離線也能先記下（跨主機授權 XR-2b，#2750）

- `POST /api/team/host-roots` `{host, op: add|remove, path, client}`：新增或移除 lead 主機在另一台主機（alias 或 host id）的授權資料夾。
  - 對方離線也不會失敗：變更先存在 lead 主機，對方一上線就照順序送過去，由對方檢查路徑。
  - 要移除的資料夾裡如果還有這台 lead 的 member（或正在啟動的 member），會直接拒絕：409 `roots_in_use`，並列出是哪些 member。要先把那些 session 結束才能移除。
  - 只能移除對方目前有的資料夾，或還在排隊的新增項目；其他路徑回 400 `not_a_root`。
- 移除排隊期間，lead 不能在那個資料夾開新 member，也不能收編那裡的 session：409 `root_being_removed`。判斷和寫入在同一筆交易裡完成，兩件事不會同時成立。
- `GET /api/team/host-roots`：每台配對主機回傳狀態（`ok`／`offline`／`consent_off`／`unsupported`／`unauthorized`）、目前的資料夾清單，以及排隊中、被拒、被丟棄的變更。回應前會並行問各台一次，最多等 2 秒；對方重建配對、版本號倒退時，會自動更新快取。
- `POST /api/team/host-roots/dismiss` `{id}`：清除被拒或被丟棄的那一列。
- 已知限制：lead 端判斷「是否還有 member 在用」只做字面比對，大小寫要一致，也看不出捷徑（symlink）。大小寫或寫法不同的情況，由對方主機用實際路徑再檢查一次，擋下後結果會顯示在那一列。

## [1.0.0-alpha.724] - 2026-10-13

> 這版改了 daemon 與 SPA，mod 沒有改。Mac 按 dev update（或靠 dev server 的 HMR）就會生效；iOS 隨後出版。

### Added：App 看得到暫停狀態、可以解除；接力卡片顯示名稱和工作報告（無人值守額度用完暫停 UP-5，#2747、#2748、#2749，介面線）

- **被擋下的燈號＋解除按鈕**（Mac，#2749）：被暫停的 session 會亮「被擋下」燈。燈號出現在無人值守面板、team 名冊和標題列的提示點，文字依原因不同，例如「接力額度用完」或「lead 已暫停」，有訊息被退回時會附上則數。按「解除」只放行這一個 session，無人值守仍保持開著；按的是 lead，它的 member 會一起解除。主機要先宣告 `team.pause.v1` 才會顯示。
- **接力卡片**（Mac，#2748）：
  - tmux id／peer id 旁邊多了 session 名稱。
  - 卡片附上工作報告：「申請前做了什麼」是這個 session 最新的 3 筆工作簿紀錄，「接下來打算做什麼」是還沒完成的待辦。報告直接讀工作簿，工作簿每輪結束後已經由 Haiku 寫好，所以不會另外呼叫模型。沒有工作簿或讀取失敗時不顯示，也不會拖慢卡片。
  - 回位或解除暫停後重新送出的申請，卡片上會標「重新申請」。
- **手機**（#2747）：手機的裝置權杖可以呼叫 `GET /api/team/paused` 和 `POST /api/team/paused/{session_id}/release`，iOS 的燈號和解除按鈕會用到。同一路徑的其他方法仍然擋住。

### 其他

- 跨主機授權 XR-2a（#2746，A 線）：lead 主機新增要送到別台的授權變更表（`host_root_changes`）和對方資料夾的快取（`host_roots_seen`），每台主機由一個背景工作依序送出。對方連續回 401 超過 10 分鐘，就視為已解除配對。目前還沒有路由會寫入變更（要等 XR-2b），所以沒有任何行為改變。

## [1.0.0-alpha.723] - 2026-10-13

> daemon 與 mod 都有改（mod 協定 4 → 5）：兩台都跑了 pdx setup。App 的「被擋下」燈號、解除按鈕，以及接力卡片上的工作報告是下一階段（介面線 UP-5）；這一版先在 daemon 端運作。

### Added：無人值守時接力額度用完 → 暫停，等使用者回來（A 線，#2735–#2743）

- **什麼時候會暫停**：無人值守開著、要接力（70%、閒置或 member 接力），但額度已經用完。以前會開一張等人核准的卡片，最多擋 10 分鐘訊息，之後就繼續跑到被自動壓縮；現在改成不開申請、讓 session 暫停。
- **暫停時**：
  - 手上這一輪會做完，不會被打斷。
  - session 會收到「接力額度用完（無人值守），先暫停：手邊這一步收尾後不要開始新的工作，等使用者回來。」，手機收到一則推播（新 kind `pause`）。
  - 別人傳給它的訊息一律退回（409 `session_paused`），發送者當下就知道「對方暫停中」。退回的訊息記成清單：同一來源、同樣內容只記一筆，每個 session 最多 100 筆。
  - 你自己打的字照常送進去。
- **lead 停＝整隊停**：lead 暫停時，它在本機的 member 一起暫停（「lead 已暫停…等 lead 指示」）；其他主機上的 member 只會收到通知，不會被暫停。member 自己的額度池用完時，只暫停那個 member，lead 的 `pdx relay` 會收到 409 `member_paused_no_quota`，由 lead 決定怎麼處理。跨主機 member 自己的額度用完時，照舊出卡片。
- **回來時**：
  - 關掉無人值守，所有暫停都會解除。lead 和一般 session 會收到「管理者已回位」，並重新送出一張接力申請給你核准；它們的 member 隨後解除，收到「lead 已恢復，等 lead 指示」。
  - 每個恢復的 session 都會收到暫停期間被退回的訊息清單。
  - 也可以用 `POST /api/team/paused/{session_id}/release` 只解除一個 session。App 的按鈕在下一階段。
- **mod**：被暫停後不會再自己申請接力，直到收到「重新申請」、開新 session，或你打 `/clear`。收到「重新申請」時，會帶一次性的放行券重新送出，這張卡片一定等你核准，不會自動放行。舊版 mod 收到的是「請在這個 session 打 /relay 重新申請接力」。
- **可靠性**：daemon 開機時和之後每 30 秒對帳一次。關無人值守的過程中如果當機，或之後又被打開，留下的舊暫停都會被解除。通知走持久化的 outbox，照順序送、至少送到一次；連續失敗 30 次的會移到 dead-letter，不擋後面的通知。
- 新 capability `team.pause.v1`；查詢路由 `GET /api/team/paused`；roster 每列多一個 `paused` 欄位。
- 規格：`docs/specs/2026-10-12-unattended-quota-pause-spec-plan.md`。

## [1.0.0-alpha.722] - 2026-10-12

> 只有 daemon 改動，mod 沒有改。這一版是跨主機授權的第一段，只做「被授權的那台主機」那一端；lead 主機和 App 那兩端還沒上，所以目前看不出差別。

### Added：別台 lead 主機可以增減自己在這台的授權資料夾（跨主機授權 XR-1，#2732）

- 新的 peer 路由 `POST /api/peers/team/roots-change`：已配對、而且這台已經對它開了「允許開 member」的 lead 主機，可以新增、移除或讀取自己在這台的授權資料夾。「允許開 member」這個開關本身仍然只能在這台切換。
- 移除時，如果還有那台 lead 的 member 在這個資料夾裡跑，就拒絕（`roots_in_use`，附上 member）。重複送同一個請求，結果都一樣。
- 新增的路徑在這台檢查：路徑要存在、要是資料夾，捷徑會解析成實際路徑，最多 16 個。只有真的有變動才寫入，版號才加一，並記一行 log。
- `GET /api/peers/team/roots` 多回 `team_roots_rev`；這台主機回報的 capability 多一項 `roots_change`。
- 跨主機 team 規格同步修訂：「同意只能收回」改成「`allow_team` 只能收回，在它之下 lead 主機可以增減自己的資料夾」（使用者 2026-10-12），並加註收斂式請求的例外。
- 規格：`docs/specs/2026-10-12-host-roots-from-lead-spec-plan.md`。

## [1.0.0-alpha.721] - 2026-10-12

> 只有 SPA 改動：daemon 只是為了版號重啟，mod 沒有改。Mac 按 dev update（或 dev server 的 HMR）就生效。

### Fixed：Mac 從終端機也能切到啟動面板（NA-1 後續，#2730，介面線）

- 在沒有 agent 在跑的 session 上，狀態列的指揮台／聊天按鈕不再鎖住，點了就會看到啟動面板。720 的已知限制就此解除。
- 狀態列和面板共用同一份啟動計畫：同一個 session、同一個 tmux 世代只讀一次。停在終端機畫面時就先讀好。計畫讀好、確認沒有 agent 在跑，按鈕才放行；讀取中按鈕提示「正在確認」。codex 在跑時，按鈕維持原本的灰色。

## [1.0.0-alpha.720] - 2026-10-12

> daemon 與 mod 都有改：兩台都跑了 pdx setup。Mac 的啟動面板是 SPA，按 dev update（或 dev server 的 HMR）就生效；iOS 是 0.6.71。

### Added：沒有 agent 的 session 可在指揮台／聊天直接接續上次對話或開新對話（Mac NA-1 #2725、#2729；iOS 0.6.71／0.6.72 NA-2，介面線）

- 在指揮台或聊天畫面打開一個沒有 agent 在跑的 session，不再只顯示「這個 session 沒有 Claude Code 對話」，而是顯示啟動面板：
  - 狀態列：在 tmux 裡、要不要先切到專案目錄（附路徑與分支）、pane 正在跑什麼。
  - 兩個按鈕：「接續上次對話」「開新對話」。
  - 切到終端機的連結與重新整理按鈕。
- 面板只顯示 daemon 算好的計畫、送出你的選擇，不會自己切到終端機；送出成功時不另外提示。
- 送出時連線失敗：顯示「不確定有沒有送出」，並重新讀一次計畫。
- 主機還沒有 `session.launch.v1`（舊版 daemon）時照舊顯示原本的畫面。
- 版面：整塊置中，區塊內文字靠左（#2729；iOS 0.6.72 同步）。
- 已知限制：Mac 狀態列的指揮台／聊天按鈕，在沒有 agent 的 session 上還是鎖著的，所以要先停在指揮台或聊天畫面，才看得到面板。從終端機切過去的入口在下一版開放（#2730 修正中）。
- iOS 同樣的面板在 purdex-ios fc1dcad（OTA 0.6.71 (93)），版面調整在 c2f51e8（OTA 0.6.72 (94)）；iOS 原本在這個狀態下會藏起模式切換，現在一律可以切到終端機。

### Changed：接力 prompt 說明為什麼接力，等待中的新對話會停下來等（A 線，#2727）

- 寫接力檔與新對話的 prompt 不再一律說「context 已滿」，改用新變數 `{{why}}` 說明實際原因：
  - 閒置接力：「已閒置 N 分鐘（context P%），趁快取還沒過期先接力」
  - 達到門檻：「context 已達接力門檻」
  - 手動 `/relay`：「使用者手動要求接力」
  - lead 安排：「lead 安排接力」
  - mod 重新載入後恢復：「mod 重新載入過，原因沒有保留」
- 寫接力檔時多一條要求：如果現在是在等使用者、lead 或其他 session，「下一步」要寫清楚在等什麼、等誰、等到之後的第一個動作。
- 新對話的第 3 步改成：「下一步」如果是在等人，就停在這裡等，不要自己開始新的工作；否則照原本的「下一步」繼續。
- 設定頁的接力 prompt 編輯器多列出 `{{why}}` 這個變數。自訂過的 prompt 不會被改動；沒用 `{{why}}` 的自訂 prompt 組出來跟以前一樣。

## [1.0.0-alpha.719] - 2026-10-12

> 只有 daemon 改動，mod 沒有改。Mac／iOS 的啟動面板還沒上（要等這一版在兩台主機上線後才 merge），所以 App 暫時看不出差別。

### Added：啟動面板的 daemon 路由（NA-0b，#2723、#2726，介面線）

- `GET /api/sessions/{code}/launch-plan`：回報這個 session 現在能怎麼啟動 agent——有沒有 agent 在跑、上次跑的對話能不能接續、要不要先切到專案目錄、開新對話用哪個 agent、pane 是不是正在忙。上次的紀錄只在 tmux 世代沒變時才給。
- `POST /api/sessions/{code}/launch`：由 daemon 自己重新算出要打的指令（不收 client 傳來的指令），只在 pane 停在 shell 提示字元、且 tmux 世代沒變時才送出；判斷和送出在同一次 tmux 呼叫裡完成。世代變了回 409 `generation_conflict`，pane 在忙回 `busy`，同時按兩次只有一次會送出（`already_launching`），agent 已經在跑回 `agent_running`。要切目錄時指令前面固定加 `cd '<路徑>' && `，路徑一律單引號。
- 新 capability `session.launch.v1`；這兩條路由也開放給手機等 device token 使用（同路徑的其他方法仍然擋）。

## [1.0.0-alpha.718] - 2026-10-12

> 只有 daemon 改動，mod 沒有改。App 看不出差別；這一版先上線，是為了讓主機開始記錄每個 session 最後跑過的 agent，之後的啟動面板一上線就有紀錄可以接續。

### Added：記錄每個 session 最後一次跑的 agent（啟動面板 NA-0a，#2721，介面線）

- 主 agent 結束時，daemon 記下這個 tmux session 最後一次跑的是哪個 agent、哪段對話、在哪個目錄（每個 session 只留最新一筆）。這筆紀錄和結束時的認領在同一個交易裡寫入：兩個結束事件同時到時，只有搶到認領的那一邊會寫；子 agent 結束、主機開機清理殘留時都不寫。tmux 重啟過的話，舊世代的紀錄不會蓋掉新世代的。
- 資料庫：`agent_frames` 加兩欄（tmux 世代、是否為主 agent），舊資料在升級當下回填；`agent_events.db` 新增 `last_agent_run` 表。
- 還沒有任何路由或畫面會用到這筆紀錄（NA-0b 起才會）。

### 其他

- 沒有 agent 在跑的 session 改顯示啟動面板（接續上次對話／開新對話）的 spec 與 plan（#2720，介面線，文件）。

## [1.0.0-alpha.717] - 2026-10-12

> 只有 SPA 改動：daemon 只為版號重啟，mod 沒有改。App 要按 dev update 才看得到。

### Added：Mac 指揮台狀態列也有用量環（#2712）

- 一般 session 的指揮台狀態列右端（花費之後）加上 context、5 小時、7 天三個用量環，外觀與提示跟底部狀態列相同、不調暗；這個 session 沒有自己的快照時仍顯示主機的 5 小時／7 天。worker 指揮台的狀態列加上 5 小時／7 天。
- 窄寬度時用量環與數字不隱藏、不換行，左邊的文字先截短讓位。
- 主機額度（`/v1/host`）改成每台主機只有一個共用的輪詢，worker 列與底部列不再重複查。

### Added：閒置接力申請的卡片多一行說明（閒置接力 IR-3，#2718）

- Mac App 的接力申請卡片：閒置觸發的申請在用量那行上方多一行「閒置 N 分鐘，快取快過期；趁現在接力最省。」（en: "Idle N min; the cache expires soon — relaying now is cheapest."）。資料格式不對時不顯示，卡片照舊；只有接力申請的卡片會出現。

## [1.0.0-alpha.716] - 2026-10-12

> mod 有改（#2715）：**要部署 daemon，並在兩台重跑 `pdx setup`**（各 session 會跳一次重新載入 mod 的提示）。

### Added：閒置 40 分鐘先申請接力，不再被 Claude Code 直接壓縮（閒置接力 IR-2，#2715）

- Claude Code 會在 session 閒置約 54 分鐘時自行壓縮（趁 1 小時的 prompt 快取過期前），常常還沒到 70% 的接力門檻。現在一般 session 在主對話一輪結束後閒置滿 40 分鐘、context 達 40% 時，mod 會先送出接力申請：Mac App 出現卡片，不在電腦前就推到手機（iOS 可核准）。核准就趁快取還熱的時候接力；沒核准的話，申請約 10 分鐘後逾時，Claude Code 照常在約 54 分鐘時壓縮。
- **閒置申請不擋訊息**（使用者定案）：申請開著時你打字或有 peer 訊息進來，申請會自動撤回（`cancelled{resumed}`），訊息照原樣送出。70% 的申請照舊會擋。
- 同一段閒置只問一次；被拒絕或逾時後，要 context 再多 10% 才會再問（與 70% 申請共用）。剛接力完的新對話、有背景工作或排程在跑時、member（由 daemon 通知 lead）都不會申請。daemon 沒宣告支援時整個關閉。
- mod 會分辨閒置壓縮（自動壓縮發生時沒有一輪在跑）並回報 `idle`：member 被閒置壓縮時，lead 收到的通知改成「閒置太久，被 Claude Code 自動壓縮了」，不再誤說「lead 未在 70% 時接力」。
- 背景工作的追蹤不再依賴事件回報器是否開啟。
- 驗收用環境變數（在 session 的環境裡）：`PDX_RELAY_IDLE_MINUTES`（1–50）、`PDX_RELAY_IDLE_THRESHOLD`（1–100）。

### Internal：mod 進入點檢查不再綁死 import 寫法（#2716）

- `cmd/pdx/plugin` 檢查 register.js 有沒有載入 ask／events／lease 模組時，改用 regexp 認「import 清單裡有這個名字」加「有呼叫它」，清單多一個 export 或用 `as` 別名都不會誤判。

## [1.0.0-alpha.715] - 2026-10-12

> daemon 有改（#2707）：**要部署 daemon**；mod 沒有改，不用重跑 `pdx setup`。

### Added：一般 session 也顯示 git branch（#2707）

- daemon 的 session 清單多帶 `branch`：session 模組在背景讀 cwd 的 `.git/HEAD`（不啟動 git 子程序、只讀一般檔案、HEAD 與 `.git` 檔上限 1 KiB、驗證 ref 名、每個 cwd 同時最多一個讀取、1 秒逾時），清單只讀快取；換 branch 後約 5～6 秒內廣播。實測暖快取的清單呼叫多約 5 µs，背景一輪 13 個 cwd 約 150 µs。
- App 的狀態列：有 worktree 時顯示 worktree 的 branch，否則顯示 session 的 branch。iOS 0.6.66 起也會讀這個欄位。

### Fixed：用量環顯示（#2710）

- 用量環不再被調暗；context 用量取這個 session 自己的；5 小時與 7 天額度取同一台主機最新的一筆（只在帶可信時間的快照之間比較）。過了重置時間（`resets_at`）就當作已用 0%，計時跨過邊界時會重畫。

## [1.0.0-alpha.714] - 2026-10-12

> daemon 有改（#2708）：**要部署 daemon**；mod 沒有改，不用重跑 `pdx setup`。

### Internal：「拉回訊息」的狀態機與副作用分開（#2705，#2682）

- Mac 指揮台拉回訊息（K-2b）的判斷改成純狀態機，送出請求、改畫面等副作用另外處理，方便測試；行為不變。

### Added：daemon 支援「閒置接力申請」（閒置接力 IR-1a，#2708）

- 為之後的 mod（IR-2）鋪路，這一版只改 daemon 與 CLI，行為還不會變：`pdx relay hello` 回 `idle_minutes`（40）與 `idle_threshold`（40），mod 據此判斷這台 daemon 支援閒置申請；`pdx relay begin --self --reason idle --idle-minutes N` 開的申請會在卡片資料記下原因與閒置分鐘數。
- 推播：閒置申請的通知改成「某 session 閒置 40 分鐘，申請接力（已用 N%）」，副標「趁快取還在時接力最省；不核准的話，Claude Code 稍後會自行壓縮」。
- 新的取消原因 `resumed`：閒置申請開著時有新訊息進來，mod 會用它撤回申請（使用者定案：閒置申請不擋訊息；70% 的申請照舊擋）。只能用在閒置申請的「取消」。
- spec：`docs/specs/2026-10-11-idle-relay-spec-plan.md`。

## [1.0.0-alpha.713] - 2026-10-12

> daemon 有改（#2700、#2701）：**要部署 daemon**；mod 沒有改，不用重跑 `pdx setup`。⚠️ **Profile Sync 的設定版本 9 → 10**（#2698）：還沒更新的 App 會看到比自己新的設定而鎖住同步，**兩台 Mac 要一起按 dev update**。

### Added：⌘F 搜尋得到摺疊的輸出與子 agent 的步驟（第 2 段，#2690，#2665）

- 摺起來的指令／工具輸出、bash 指令的輸出、已載入的子 agent 步驟都搜得到；命中時自動展開那一段再標記（子 agent 裡的輸出會兩層一起展開）。還沒展開載入過的子 agent 沒有內容可搜，diff 內文仍只搜路徑。
- 畫面內容變動（串流增長、遠處的回合換成完整排版、縮圖載入）時，搜尋標記會跟著重畫，不會跳動捲軸。

### Added：設定頁可以選「離開電腦幾分鐘後推到手機」（PA-2b，#2680）

- 設定 → 桌面 App 新增選單：1／2／3／5／10／15／30／60 分鐘（預設 5）。只有換上 PA-2a 新殼的 Mac App 才會出現。
- App 把主機清單交給 Electron 主行程的回報器；主行程接手某台主機後，舊的頁面回報（v1）就對那台送最後一次「不在」然後停止，主機離開清單時立刻恢復。沒有新殼的 App 行為不變。

### Changed：核准燈箱的「核准」不再切換分頁，新增「核准並檢視」（#2695）

- lead、接力、收編、member 接力的核准燈箱：「核准」只核准、留在原本的分頁；新增「核准並檢視」，核准後切到申請者的分頁（申請者不在 tmux 裡時不顯示）。「拒絕」與斷線時記下、重連後才送出的決定都不切換。
- 燈箱出現時焦點直接在「核准」上（表單還不合法時在燈箱本身）。燈箱出現後 0.5 秒內用鍵盤按的 Enter／空白鍵不算數，避免在終端機打字時誤按核准；滑鼠點擊不受影響。

### Changed：快速回覆分成「標題」與「內容」（QR-2a，#2696）

- 每一則快速回覆有標題與內容：輸入框上方那排按鈕顯示標題，滑過去看得到內容，點一下送出的是內容。設定頁的編輯器改成標題、內容兩欄（標題 ≤ 100 bytes、內容 ≤ 1000 bytes，不能空白）。
- 預設三則：繼續、跑測試、解釋。daemon 端格式在 alpha.712 已上線（QR-1）；接到舊版 daemon 時，標題與內容都取舊的那段文字。

### Changed：指揮台與聊天裡的 peer 訊息超過 3 行會收合（#2694）

- 其他 session 傳來的訊息只顯示前 3 行；滑過去會變亮，點整塊（或用 Enter／空白鍵）展開、再點收回，選取文字時不會誤觸。展開狀態切 tab 回來還在；⌘F 命中被收起的行時會自動展開。3 行以內的訊息不受影響。

### Fixed：聊天模式裡，還在緩衝或排隊的訊息也靠右（#2697）

- 你送出的訊息在 1.5 秒緩衝中、或已排進 Claude Code 佇列時，跟已送達的泡泡一樣靠右顯示，不再暫時出現在左邊。

### Added：快速回覆的開關（QR-2b，#2698）

- 設定頁的快速回覆區多兩種開關：「顯示快速回覆列」整排開關，以及每一則「在列上顯示」的個別開關（按主機記）。關掉的只是不顯示在列上，清單本身仍存在主機上。
- 開關存在 App 這邊，透過 Profile Sync 同步到你的其他 Mac（設定版本因此從 9 升到 10）。主機清單裡已刪掉的項目，開關紀錄會一併清掉。

### Changed：聊天模式的 bash 指令改成步驟列，並畫出輸出（#2699）

- 聊天模式裡用 `!` 執行的 bash 指令，改成靠左的步驟列（不再當成你的泡泡），下面接可摺疊的輸出；指令文字會去掉 ANSI 色碼與雙向文字控制字元。
- ⌘F 也搜得到聊天與指揮台裡這兩種指令輸出。

### Added：member 閒置太久時先通知 lead（閒置接力 IR-1b，#2700）

- Claude Code 會在 session 閒置約 54 分鐘時自行壓縮（趁 1 小時的 prompt 快取過期前），常常還沒到 70% 的接力門檻。現在本機 member 閒置滿 40 分鐘、context 達 40% 時，daemon 會通知它的 lead 一次：「閒置 40 分鐘、已用 N%，快取快過期，要趁現在接力請執行 `pdx relay _<ref>`」，由 lead 決定。同一段閒置只通知一次；被壓縮後、或 member 又動起來前不再通知。daemon 重啟後重新計時。
- `pdx relay compacted --trigger idle`：mod 回報的是閒置壓縮時，lead 收到的通知改成「閒置太久，被 Claude Code 自動壓縮了（不是 context 滿）」，不再誤說「lead 未在 70% 時接力」。（mod 端的判斷在之後的 IR-2。）
- 驗收用環境變數：daemon 的 `PDX_RELAY_IDLE_MINUTES`（1–50）、`PDX_RELAY_IDLE_THRESHOLD`（1–100）。

### Internal：輸入框 ↑↓ 的判斷抽成純模組（#2702，#2687）

- SessionInput 裡「↑↓ 要拉回訊息、走訪歷史還是移動游標」的判斷搬到獨立的純函式模組，方便測試；行為不變。

### Fixed：daemon 重啟後，iOS 的狀態列不再空白（#2701）

- Claude Code 狀態列送來的快照（usage、branch、worktree 等）原本只存在記憶體，每次部署重啟就清空，閒置的 session 要等下次有動靜才補上，所以 iOS 狀態列常常空白。現在快照存進 `agent_events.db` 的 `status_snapshots` 表（與 context 用量共用每 10 秒一次的寫入、只在有變動時寫；實測 512 筆 4.6 ms），開機時還原最多 512 筆（只還原 tmux 世代對得上的）；表不存在或 DB 是空的也照常啟動。
- `agent.status` 多帶 `at`（毫秒），App 依原始時間判斷快照是否過期。

## [1.0.0-alpha.712] - 2026-10-11

> daemon、Electron、SPA 都有改：**要部署 daemon**（#2691 快速回覆 v2）；mod 沒有改，不用重跑 `pdx setup`。Electron 的改動（PA-2a）要在 App 按 dev update 才會生效。

### Changed：Mac 指揮台送出改成「打進 Claude Code」（K-2a-2、K-2a-3，#2663、#2667）

- 指揮台送出訊息改走 `POST /api/sessions/{code}/submit`：由 session 的 mod 把文字填進 Claude Code 的輸入框再送出，交給 Claude Code 自己的佇列處理（可以在回合中途插話，終端機與指揮台同步）；沒裝 mod 的 session 改走直接打字。
- 送出前的緩衝改成 1.5 秒，不再等 agent 閒置；遇到忙碌就換新的 id 重試。
- 已排進 Claude Code 佇列的訊息改由對話資料（header）顯示，App 不再重複畫一份；10 秒沒動靜就標「可能已送出」。
- daemon 太舊、沒有這條路由時，顯示看得懂的錯誤。

### Removed：舊的 worker 房間檢視（U4-5b，#2674）

- worker 分頁已全面改用指揮台／聊天，舊的 ExecutionView、room、prelude、stints 一併移除（111 個檔案，純刪除，行為不變）。

### Removed：房間檢視的殘留（U4-5c，#2683）

- 移除舊房間檢視留下的 prelude 捲動錨點與欄位、原生圖片附件、room 搜尋索引（24 個檔案，大多是刪除，行為不變）。

### Added：Mac 指揮台可以把送出的訊息拉回（K-2b，#2681）

- 點還在緩衝中的訊息，只拉回那一則；點已排進 Claude Code 佇列的訊息，或在空的輸入框按 ↑，走 `/recall` 全部拉回，接在草稿前面。拉回時比對當下對話資料裡的排隊列，回應完全吻合才把那幾列藏起來。

### Added：指揮台與聊天的快速回覆（#2685，#2664）

- 輸入框上方多一排快速回覆（內容是主機的快速回覆清單，在設定頁編輯），點一下就照「送出」鈕的方式送出；指揮台與聊天、一般 session 與 worker 分頁共用同一套。這排可以收合，收合狀態記在這台裝置。

### Added：指揮台與聊天的 ⌘F 對話內搜尋（第 1 段，#2688，#2665）

- 焦點在指揮台或聊天裡時按 ⌘F（其他平台 Ctrl+F）開搜尋列，不會搶走終端機與編輯器的 ⌘F。Enter 跳下一個、Shift+Enter 跳上一個，到頭尾會繞回；Esc 關閉，焦點回到輸入框。
- 搜得到：你說的話、agent 的回覆（比對畫面上顯示的字，不是 markdown 原文）、每個步驟的那一行（指令、diff 路徑、子 agent 描述）。這一段還搜不到指令輸出、提問卡、摺起來的內容。
- 搜尋列的狀態（開著與否、搜尋字、目前第幾個）切 tab 回來會保留。搜尋列開著時串流不會自動捲到底。

### Changed：快速回覆改成「標題＋內容」（QR-1，#2691）

- daemon 的快速回覆清單每一項改成 `{id, title, content}`，標題 ≤ 100 bytes、內容 ≤ 1000 bytes、最多 20 項。讀取與寫入的回應都另外附上 `text`（等於 content），舊版 App 照樣讀得懂。
- 舊格式 `{id, text}` 仍可寫入：內容取 text，標題沿用同一個 id 原本的標題，沒有就取 text 的前 100 bytes。主機上已存的舊資料讀出時自動換成新格式，不改寫原檔。
- App 的編輯畫面（QR-2a）與 iOS（QR-3）在這版上線後才會跟進。

### Added：Mac App 回報「人在不在電腦前」（PA-2a，#2672、#2675、#2676、#2677）

- Electron 主行程定期取樣系統閒置時間與螢幕鎖定狀態，回報給每一台支援 `push.presence.v2` 的主機，作為推播判斷「要不要推到手機」的依據（daemon 端規則在 alpha.711 已上線）。「離開電腦幾分鐘後推到手機」預設 5 分鐘，存在 App 的偏好設定裡；設定頁的選項在 PA-2b。
- 要在 App 按 dev update 換上新的殼才會開始回報；換上之前推播照舊規則。

### Added：指揮台輸入框用 ↑↓ 叫回已送出的訊息（#2686，#2666）

- 輸入框是空的、又沒有可以拉回的訊息時，按 ↑ 依序叫出這個對話自己送出過的訊息（由新到舊，連續重複的只算一次；peer 訊息、系統通知不算），↓ 往回，↓ 超過最新一則就清空。多行訊息要游標在第一行（↑）或最後一行（↓）才會切換；一編輯就停止走訪。送出過的圖片會變回附件標籤。切 tab 回來，走訪狀態還在。

## [1.0.0-alpha.711] - 2026-10-11

> daemon、mod、SPA 都有改：**要部署 daemon，並重跑 `pdx setup`**（#2662 改了 mod 的 pdx-team skill）。

### Internal：沒裝 mod 的終端機對話宣告可以直接打字（K-2a-1，#2654）

- 對 live、沒有 mod owner 的終端機對話，daemon 的 capabilities 宣告 `send: keys`（之後 App 送出時改用直接打字的路徑）；WS 推送的 capabilities 改用刷新後的 header 計算，並排在 header 訊框之前。App 要等 K-2a-2 才會用到，這一版行為不變。

### Changed：worker 指揮台的即時列與結束狀態改用 Nexen 事件（U4-4a，#2650）

- worker 分頁的指揮台：即時列與背景工作提示改由 Nexen 事件驅動，生命週期變化顯示成系統列；worker 結束後保留指揮台內容並在底部顯示已結束。

### Fixed：同一個 member 不會被連續接力兩次（#2653）

- lead 對 member 的舊 ref 下 `pdx relay` 時，daemon 會經接力紀錄找到新 session；若前一次接力還在收尾（新 session 的第一輪還沒結束），現在回 409 `relay_open` 並附上前一個 op，不再對新 session 再接力一次。跨主機的 member 由成員主機在同一個交易裡擋下（lead 端會看到這次接力失敗）。

### Changed：部署前檢查不被「正在收尾」的接力擋住（#2657）

- `GET /api/team/inflight` 的 `relays_active` 不再計入「已清掉舊 session、新 session 正在跑」的接力（只差新 session 第一輪結束的回報，或 15 分鐘後由 daemon 自行結算）；它仍列在 `relays` 裡並標 `settling: true`。新 session 不在線或狀態不明的照計。`pdx setup` 遇到只剩收尾中的接力會放行並提示。

### Changed：`pdx relay --wait` 等待時印進度（#2662）

- 等待期間每次狀態變化在 stderr 印一行（例如「接力 claimed → writing（member 正在寫交接檔，12s）」），同一狀態超過一分鐘再印「接力仍在進行」，讓前景跑的終端機不會看起來像卡死；exit code 與 stdout 不變。pdx-team skill 補充：lead 也可以用背景執行，但要讀背景任務的結果，「接力完成」通知只是輔助訊號。


### Fixed：直接打字送出的長文字不再被吞掉 Enter（K-1c，#2660）

- 沒裝 mod 的 session 走直接打字時，改成先打字、等 150 ms、重新確認燈號，再單獨送 Enter。真機實測文字和 Enter 一起送時，90 字以上 Enter 會被吞掉，daemon 卻回報已送出。
- 結尾是反斜線的文字一律補一個空格（避免被當成換行接續），填字與打字兩條路一致；補完超過 4000 bytes 回 `too_long`。進行中的打字認領不會被淘汰或過期。

### Added：worker 指揮台補上狀態 chips 與交接控制（U4-4b，#2658）

- 狀態列右側顯示事件串流狀態、觀察者數與控制權持有者（例如「● live · 2 observers · lease: you」）。
- 指揮台也能「帶回終端機」與「退出」：狀態列的終端機鈕、檢視選單的「終端機」項、狀態列的「退出」鈕，流程與確認對話框跟原本的房間檢視相同。

### Changed：worker 分頁預設打開指揮台，聊天改用共用的聊天元件（U4-5a，#2668）

- worker（Nexen 執行體）分頁預設顯示指揮台；聊天檢視改用跟一般 session 同一套聊天元件，舊的 room 檢視退場。

### Changed：在電腦前時不推到手機（daemon 端，PA-1，#2670）

- Mac 回報新增「整台電腦閒置多久、有沒有鎖螢幕、幾分鐘算離開」（`PUT /api/push/presence` 的 `v:2` 欄位），新增 capability `push.presence.v2`。只要有一台 Mac 判定你在電腦前（閒置未達你設的分鐘數，或鎖螢幕未滿 1 分鐘），**手機一律不推**——agent 事件與核准請求都一樣、不論 session 或分頁；推播延遲送出前會再確認一次。舊版 App 照舊用原本的規則（正在看的 session 不推）。
- AskUserQuestion 的去重只算「真的推出去」的那一則；daemon 重啟時不再把當時開著的提問當成已推過（寧可重複一次，也不漏掉唯一的提醒）。
- App 端（Mac 回報新欄位、設定「離開電腦幾分鐘後推到手機」）是下一階段；這一版部署後，新版 App 上線前行為照舊。規格：`docs/specs/2026-10-11-push-away-presence-spec.md`。

## [1.0.0-alpha.710] - 2026-10-11

> daemon、mod、SPA 都有改：**要部署 daemon，並重跑 `pdx setup`**（#2633 改了 mod）。

### Added：指揮台的即時列與完成列顯示這一輪產出多少 token（#2624）

- 每一輪帶 `output_tokens`：主執行緒各則回覆的輸出 token 加總（同一則訊息的多列只算一次，subagent 不算進來）。即時列顯示「✻ 執行中 · Bash · ↓ 9.2k · 3 分 03 秒」，完成列顯示「✻ 完成 · 20 秒 · ↓ 3.1k · 02:58」，歷史回合也有。
- 跟終端機的數字一致；執行中會略落後（轉錄檔每寫完一段才更新），結束後相同。舊 daemon 不帶這個欄位，App 就不顯示 ↓。

### Added：完成列提醒「背景還有工作在跑」（#2627）

- 對話資料多了 `background`：Claude Code 結束一輪時還在背景執行的指令、subagent、其他工作各有幾個（取自 Stop hook 的 `background_tasks`）。只在 session 在線、未結束時帶。
- 指揮台與聊天：即時列附「· 1 個背景指令」，最後一回合的完成列結尾加「· 1 個背景指令仍在執行」，對應終端機的「1 shell still running」。舊 daemon 不帶，App 就不顯示。

### Added：daemon 認得 worker 擁有的對話（U4-1a，#2628）

- worker（Nexen 執行體）跑的 Claude Code 對話，對話資料現在標 `backend: execution`，帶 worker 的即時燈號（每次更新都重讀），worker 結束就轉成已結束；能做的操作（送訊息、中斷、回覆權限、背景工作）也照 worker 的能力回報。
- 這是 worker 分頁改用指揮台／聊天（U4）的 daemon 半邊；App 端是下一張 PR，舊 App 不受影響。

### Changed：還沒被 agent 接受的訊息用醒目框，檔案可以拖到整個 pane（#2629）

- 還在 App 手上或在 Claude Code 佇列裡排隊、agent 還沒讀進去的你的訊息，改回醒目的紫框區塊；已經被讀進去的照現行樣式、再調暗一小步，一眼分得出哪些還沒送達。其他 session 的排隊訊息不變。
- 指揮台與聊天：檔案拖到 pane 任何地方都能放下（整個 pane 顯示虛線框），走原本的上傳流程進輸入框附件；終端機不變。
- 表格格線調亮一階（深色更亮、淺色更深）。

### Fixed：點 team 成員時主機被隱藏不再毫無反應（#2631）

- 點 team 面板或側欄的成員，如果它所在的主機在這個工作台被隱藏，原本什麼都不發生；現在會提示「{主機} 在這個工作台是隱藏的」，並提供「顯示並開啟」。

### Added：worker 分頁可以切到「指揮台」檢視（U4-1b，#2632）

- worker（Nexen 執行體）分頁的檢視選單／狀態列多了「指揮台」，用跟一般 session 一樣的指揮台看 worker 的對話、燈號與可用操作（daemon 半邊是 #2628）。daemon 太舊時狀態顯示為已結束。

### Internal：「打字送出」的地基（K-1a-1，#2633）

- daemon 與 mod 之間新增三種工作（把文字填進 Claude Code 輸入框、只清掉自己填的內容、讀輸入框），以及送出流程的帳本狀態；mod 端照著執行，任何不確定的情況都不送出。之後 Mac 與 iOS 送訊息會改成「打進 Claude Code」，讓 Claude Code 自己的佇列處理、可中途插話、終端機與指揮台同步。這一版還沒有任何地方使用，行為不變；但 mod 有改，部署後要重跑 `pdx setup`。


### Added：新的送出路由「打進 Claude Code」（K-1a-2，#2635、#2636）

- 新路由 `POST /api/sessions/{code}/submit`（裝置也能用）：daemon 請 session 的 mod 把文字填進 Claude Code 的輸入框，確認內容相符、agent 不在等待回覆時，才對那個 pane 送 Enter；Enter 前任何不對（填錯、對話框開著、pane 在捲動模式、燈號轉成等待）都會清掉自己填的字、不送出。結果不確定時回 unknown，重送也絕不重打。
- 沒裝 mod 的 session：確定沒有 owner 超過 10 秒、燈號是閒置或執行中時，才直接打字＋Enter（同樣的守衛）；Enter 被拒會回報字留在終端機。
- 舊的 `/api/conversations/claude/{sid}/submit` 不變，兩者共用同一把每 session 的鎖。App 還沒改用新路由（K-2）。

### Fixed：跨主機開出來的 member 在 App 點不開（#2630，#2634）

- lead 從 mlab 遠端開到另一台主機的 member，lead 端名冊漏記它的 tmux session 名稱，App 點它只會跳「session 尚未出現在清單」。現在登記時就記下名稱；已經登記但空著的，名冊回傳時自動補上（不改資料庫）。從對方主機收編進來的 member 本來就正常，不受影響。

### Changed：worker 分頁的指揮台可以直接送訊息（U4-2，#2637）

- 指揮台的輸入列改成跟一般 session 同一個輸入元件：worker 分頁裡送出走 Nexen、可以中斷、附檔、草稿會保留，還沒被接受的訊息一樣用醒目框標示。一般 session 的終端機行為不變。

### Fixed：「打進 Claude Code」送出路徑的四個邊界（#2639）

- codex 補審 K-1a 找到並修正：mod 回報結果逾時（daemon 其實收到）時不再自己清掉輸入框，避免 daemon 接著按 Enter 送出空白；沒裝 mod 的直接打字模式，同一則訊息改成原子地認領，不會被兩個 session 各打一次；Enter 前讀燈號失敗一律不送（不再退回填字前的狀態），避免權限對話框跳出時仍按下 Enter；Enter 結果不明後清理輸入框會重試，仍失敗就放棄並回報，不再永久卡住。

### Internal：叫回排隊訊息的路由（K-1b，#2644）

- 新路由 `POST /api/sessions/{code}/recall`（裝置可用）：把剛「打進 Claude Code」、還在輸入框或佇列裡的訊息叫回來（保守版）。App 與 iOS 還沒接上（K-2／K-3），使用上沒有差別。

### Changed：worker 指揮台的權限請求改成輸入框上方的作答卡（U4-3，#2641）

- worker 分頁的指揮台裡，Nexen 送來的權限請求改成輸入框上方的作答卡（同意／拒絕走 Nexen 的權限通道）；其他裝置先答了，卡片會自動收起。

### Fixed：側欄燈號延遲、接力時機失準的根因——hook 處理不再每件 fork 數百次 ps（#2647）

- daemon 每處理一個 hook 事件，都會在序列化的區段裡把所有 live frame 的身分重新檢查一遍，每個 frame 要 fork `ps` 5～13 次（實測約每秒 280 次 fork，每件 hook 0.4～1 秒，最慢排隊 3 分鐘），主機一忙側欄燈號就跑掉、接力時機也跟著失準。現在追父行程只讀 PPID，PPID 與啟動時間都用一次 sysctl 讀出、不 fork：每個 frame 的檢查從 15～69 ms 降到 0.3 ms 以下。
- 啟動時間的文字必須與 `ps -o lstart=` 逐字相同（frame 存的是 ps 的文字）：daemon 會先用自己這個行程比對一次，時區或語系一變就重新比對，對不上就照舊問 ps——只會變慢，不會把還活著的 frame 誤判成 pid 被重用。

### Changed：狀態列的 peer 只顯示 name [ref]（#2648）

- 狀態列的 peer 區段只留 `name [ref]`，title 移到滑鼠提示；斷線時提示裡也保留位址、ref 與 title。

## [1.0.0-alpha.709] - 2026-10-11

> daemon 與 SPA 都有改：**要部署 daemon**（#2619）；mod 沒有改，不必重跑 `pdx setup`。

### Added：指揮台與聊天顯示 Claude Code 佇列裡排隊中的訊息（#2619、#2621）

- daemon 讀懂轉錄檔裡的排隊紀錄（`queue-operation`），對話的 header 多了 `queued`：還在 Claude Code 佇列裡、agent 還沒讀進去的訊息（內文、時間、來源是你或其他 session 的 peer 訊息）。REST 快照與即時串流都有；session 不在線或已結束時不帶。
- 指揮台與聊天把這些訊息畫在串流尾端（#2621）：你在終端機排隊的顯示「你 · 排隊中（在終端機排隊）」，其他 session 送來的顯示「來自 … · 排隊中」；只在 agent 執行中或等待時顯示，讀進去就消失。這兩種都不能撤銷或叫回。

### Added：Nex 設定表單可以勾選 extra_profiles（#2613）

- Host 設定的 Nex 區塊新增 `[nex.sandbox] extra_profiles` 勾選欄，不用再手寫 config 或打 API 就能開關 `confined` 這類需要明確啟用的 profile。
- 讀取時會去重、非陣列值視為空；勾了主機不認得的名稱會提示「主機可能拒絕」。

### Added：設定可以調指揮台與聊天的字級、行高（#2618、#2622）

- 設定 → 外觀新增「指揮台與聊天文字」：字級 11–18 px、行高 1.3–2.0，各有「恢復預設」，改了立即生效，閱讀位置不會跳掉。
- 步驟列、指令輸出、diff、思考列、作答卡（dock）也一起縮放，原本比內文小一級的調大後仍小一級（#2622）。
- 只存在這台裝置、不同步；沒動過就跟原本一模一樣。

### Changed：指揮台／聊天的即時列分出「思考中」與「執行中」（#2614）

- 沒有工具或 subagent 在跑時（模型在想或在寫）顯示「✻ 思考中… · 3 分 03 秒」；有工具在跑顯示「✻ 執行中 · Bash · …」，刪節號拿掉。
- 只有 subagent 在跑時改顯示「執行中 · 2 個 subagent」，不再把 Agent 當成工具名。指揮台與聊天共用同一元件，一起生效。

### Changed：指揮台「你」的區塊與 agent 訊息頭更好辨識（#2615）

- 指揮台裡你自己發言的區塊，框與底色改成和側欄「選中的工作區」同一組紫色，說話者標題改用主要文字色（深色主題對比從 3.2 提到 8 以上）。聊天泡泡、peer、通知不變。
- agent 訊息頭（`◆ 模型名 · 時間`）的名稱、時間、圖示各調亮一階，深淺主題都適用。

### Changed：指揮台步驟一眼看得出成敗，檔案異動預設收起（#2616）

- 每個工具列、卡片、subagent 列最前面多一顆狀態點：執行中會脈動，成功綠、失敗紅，拒絕／中斷／未知灰（未知狀態不會顯示綠）。
- 失敗的輸出（含指令結束但 exit code 非 0）標頭與內文都改紅字。
- 檔案異動卡預設只顯示 3 行、diff 調暗，按標頭右側的展開鈕才看原本的 16 行與「顯示全部」；工具列的白字也淡一階。

### Changed：App 扣著的訊息改成灰色、畫在串流尾端，空輸入框按 Up 可整份叫回（#2617、#2621）

- 還在 App 手上的訊息（撤銷窗口內、或等 agent 空閒）改成灰色、無外框，像終端機的排隊訊息；已交出去的改寫「傳送中…」／「已送出」，「排隊中」只用在 App 扣著的那種。
- 這些訊息改畫在串流尾端、即時列之後，跟著內容往下走，不再釘在輸入框正上方（#2621）。
- 輸入框空著時按 Up，所有還扣著的訊息依順序接回輸入框，附件與圖片一起回來；「撤銷」也改成帶附件整份退回。已交給 agent 的叫不回。

### Fixed：回合中途送達的訊息被誤標「排隊中」（#2617）

- agent 在回合中途讀進去的訊息，現在標「回合中送達」；回合的第一則照一般訊息顯示。

### Fixed：指揮台模式開右側面板會出錯（#2616）

- 指揮台模式下，右側面板裡的輸出折疊、diff、subagent 步驟會因為少了折疊狀態的 provider 而丟錯；現在面板自帶 provider。

### Tests：#2590 events_optedin 卡住的 flake 根治（#2612）

- 測試改用 gate 住的 write pump，讓「塞不下就關連線」和「讓出一格就送達」兩條路徑各自決定性；production 沒有改動。

## [1.0.0-alpha.708] - 2026-10-11

> 只升依賴（nexen v0.22.1）：**要部署 daemon**；mod 沒有改，不必重跑 `pdx setup`。

### Fixed：confined worker 的 context 不再帶 cwd 外的 git 資訊 — nexen#189（#2610）

- nexen 升到 v0.22.1。之前 confined 的工作目錄若在某個 git repo 的子目錄裡，Claude Code 會把整個 repo 的 branch、git user、`git status`（含工作目錄外的檔名）與最近的 commit 訊息放進模型 context，跟「只讀得到工作目錄」的承諾不符；現在 confined 啟動時關掉這個區塊（設定與環境變數兩道）。
- confined 啟動時也不再列出內建 skills／slash commands（confined 本來就沒有 Skill 工具，只是清掉清單）。

## [1.0.0-alpha.707] - 2026-10-11

> daemon 與 SPA 都有改：**要部署 daemon**；mod 沒有改，不必重跑 `pdx setup`；SPA 已隨主機上的 dev server 生效。nexen 升到 v0.22.0（資料庫格式不變）。

### Added：nexen v0.22.0 與 confined profile — c933b0-122（#2608）

- nexen 升到 v0.22.0，帶進 `confined` sandbox profile：worker 只讀得到自己的工作目錄、只能改明確列出的檔案，persona 取自工作目錄的 `CLAUDE.md`，不載入全域與上層設定、hooks 與 auto-memory。
- 主機要在 `[nex.sandbox]` 加 `extra_profiles = ["confined"]` 才能請求 confined；沒列時請求會被拒絕（`profile_not_allowed`）。從 App 或 API 存其他 nex 設定時，手寫的 `extra_profiles` 會保留，明確給 `[]` 才清掉；改了要重啟 daemon 才生效。
- `pdx nex delegate` 可帶 `--model`、`--effort`、`--writable <檔案>`（可重複）；daemon 沒宣告支援時會直接報錯、不送出。

### Added：切換分頁自動 focus 輸入框，Esc 清草稿可復原 — #2604（介面線）

- 切到指揮台或聊天分頁、切換 view 時，輸入框自動取得焦點。
- 按 Esc 清掉草稿後 5 秒內可以復原。

## [1.0.0-alpha.706] - 2026-10-11

> daemon 與 SPA 都有改：**要部署 daemon**；mod 沒有改，不必重跑 `pdx setup`；SPA 已隨主機上的 dev server 生效。

### Fixed：`/api/fs/read` 只讀一般檔案 — #2601（介面線）

- 讀檔 API 遇到 FIFO 不再卡住整個請求；`/dev/null` 這類字元裝置與其他非一般檔案改成回 400（之前是 200）。

### Changed：無人值守面板的數字設定更好用 — #2599（介面線）

- member 上限等 stepper 停用時會說明原因，也可以直接輸入數字；儲存被拒（409）時顯示 daemon 給的原因。

### Added：指揮台附件縮圖 — #2584（介面線）

- 指揮台 v3 的附件顯示縮圖，輸入框裡以 `[Image #N]` 佔位；已送出的訊息也帶縮圖標籤；附件區拿掉上方分隔線。含一項安全修正。

### Fixed：輸入框逐行加高時，指揮台仍貼在底部 — #2603（介面線）

- 修正 #2586 之後出現的回歸：輸入框隨內容加高時，訊息流不再脫離底部。

## [1.0.0-alpha.705] - 2026-10-11

> daemon、CLI、SPA 與 mod 都有改：**要部署 daemon，並重跑 `pdx setup`**（mod 內的 pdx-team skill 說明更新）；SPA 已隨主機上的 dev server 生效。

### Fixed：`pdx msg send` 不再對其實已送達的訊息回報逾時 — #1009（#2593）

- 跨主機送訊整條路的逾時改成由下往上推導、每層都比下游最壞情況多 2 秒（收件端 10 秒 → daemon 之間 12 秒 → 寄件端最壞 17 秒 → `pdx msg send` 19 秒）。之前上下游的逾時剛好相等，最慢的那次送達會被誤報成 `context deadline exceeded`，agent 看到失敗就重送，對方收到重複訊息。
- 寄件端已把請求整個送出、但等不到對方回應時，改回 `delivery_uncertain`（可能已送達，不要重送），不再當成一般失敗；請求根本沒送出去才是失敗。
- 舊版 `pdx msg send`（等 15 秒）對新版 daemon 仍可能在最壞情況下誤報，升級 CLI 即可。

### Fixed：spawn 的 brief、report、task 訊息「可能沒送達」時會說出來 — #2595（#2598）

- `pdx spawn` 的 brief、`pdx report`、`pdx task` 送出的訊息，以及 `pdx msg send`，遇到 `delivery_uncertain`（可能已送達、也可能沒有）或 daemon 的回應讀不懂時，會在 stderr 提示「不要重送，用 `pdx msg log --tail 10` 確認」。之前這三個指令丟掉回應，可能沒送達的訊息也被當成送達。exit code 不變（不重送，避免重複）。
- pdx-team skill 補上這條規則：exit 1 或 uncertain 時先查審計紀錄，確認沒送出才手動補送。

### Fixed：指揮台與聊天可以一路往上捲到最早的回合 — #2586（介面線）

- 修正回歸：往上捲時不再停在中途載不到更早的回合；聊天分頁同樣修好。

### Changed：字距 1px 只套用在中文 — #2596（介面線）

- 指揮台內文的 1px 字距改成只對中文字元生效，英數與程式碼維持原本字距。

## [1.0.0-alpha.704] - 2026-10-11

> daemon 與 SPA 都有改：**要部署 daemon**；mod 沒有改，不必重跑 `pdx setup`；SPA 已隨主機上的 dev server 生效。

### Fixed：設定檔經過懸空 symlink 時，所有寫入者仍認得是同一個檔 — #2576（#2589）

- 設定檔路徑改成一段一段照實際檔案系統解析（`..` 在前一個 symlink 展開後才套用、懸空的目錄 symlink 也會跟過去，最多 40 次跳轉）。之前在這兩種手動做出來的特殊配置下，不同寫入者可能算出不同的鎖檔名；預設路徑與一般 symlink 不受影響。

### Added：指揮台即時狀態列、回合收尾列、Agent 步驟耗時 — #2587（介面線）

- 指揮台與聊天訊息流最底下加一行即時狀態（例：`✻ 執行中… · Bash · 2 個 subagent · 3m 03s`），每秒更新；等你回覆時改顯示「等你回覆」，閒置不顯示。
- 每個完成的回合最後加一行收尾（例：`✻ 完成 · 20 秒 · 02:58`）。
- Agent（subagent）步驟顯示耗時，可以就地展開看內容。

## [1.0.0-alpha.703] - 2026-10-11

> daemon 與 SPA 都有改：**要部署 daemon**；mod 沒有改，不必重跑 `pdx setup`；SPA 已隨主機上的 dev server 生效。

### Fixed：`pdx token generate` 不會再在 daemon 執行中悄悄換掉 token — #2562（#2582）

- daemon 執行期間會持有設定檔旁的 `<config>.run` 鎖。`pdx token generate` 改看這把鎖判斷「有沒有 daemon 正在用這個設定檔」，不再只看 data_dir 裡的 pid 檔：data_dir 改過、路徑寫法不同、經過 symlink，都不會漏掉執行中的 daemon；沒帶 `--force` 就拒絕，檔案不動。
- daemon 啟動時要等正在寫設定檔的程式寫完才讀，不會出現「檢查完沒有 daemon、daemon 隨即啟動讀到舊 token」的空檔。設定檔被佔住超過 5 秒時，冷啟動會失敗並回報；熱重啟則沿用舊 image 交來的鎖，最多再等約 2 分鐘。

### Fixed：nex 設定存檔不再把一堆空值寫進 config.toml — #1057（#2585）

- 從 App 存 nex 設定時，預設本來就是空的欄位（`claude_bin`、`[nex.timeouts]` 三項、`[nex.peer]` 的 `wake_template`／`reply_line`／`max_pending`）沒設定就不再寫進 config.toml；讀回來的值不變。

### Added：在 App 直接編輯執行中 team 的工作目錄 — #2561（#2575，介面線）

- 無人值守面板的 lead 列（member 上限旁）會顯示執行中 team 的工作目錄清單，可以直接新增或移除；daemon 回報的是實際路徑。同時有人改過時會提示並刷新成最新清單；要移除的目錄還有 member 或待處理的 spawn／收編在用時會列出是誰，清單不變。

## [1.0.0-alpha.702] - 2026-10-11

> daemon 與 SPA 都有改：**要部署 daemon**；mod 沒有改，不必重跑 `pdx setup`；SPA 已隨主機上的 dev server 生效。

### Fixed：`pdx start` 不再把別人的 daemon 當成自己起的，失敗時也不留下孤兒行程 — #958、#959（#2580）

- `/api/health` 多回 `pid`。`pdx start` 等健康檢查時要求回應的 pid 就是它剛起的那顆：同一個位址上若有另一顆 daemon（不同 data dir 或舊版）在回應，不再誤判為啟動成功，而是等到新那顆因位址被佔而退出、印出真正的原因與 log；逾時的錯誤訊息會點名「另一個 pid 在回應」。健康檢查不走環境變數的 proxy、不跟隨轉址。
- 啟動失敗時改成終止整個 process group，daemon 啟動到一半起的子行程不會留成孤兒；daemon 起的 tmux server 本來就不在這個群組裡，不受影響。

### Changed：指揮台文字排版 — #2579（介面線）

- 指揮台內文行距加大到 1.65、字距加 1px；輸入框字級與內文一致、拿掉輸入框下方的分隔線；狀態列往下移 4px。

## [1.0.0-alpha.701] - 2026-10-11

> daemon 與 SPA 都有改：**要部署 daemon**；mod 沒有改，不必重跑 `pdx setup`；SPA 已隨主機上的 dev server 生效。

### Fixed：寫 config 不再蓋掉別人改過的值 — #2556（#2570）

- daemon 改設定時，改成在 `<config>.lock` 檔案鎖裡、以磁碟上的檔案為底，只寫這次改動的欄位：別的寫入者（例如 `pdx token generate --force`）改過的 token 或 hosts 不會被 daemon 的下一次設定變更蓋回舊值。磁碟上的 token 與執行中的不同時，daemon 記一行（只有 fingerprint）提醒重啟才會採用；不會自動熱換 token。
- `--bind`／`--port` 這類啟動參數不再被寫進 config；`--quick` 自己挑的 bind 照舊會寫。
- 第二個 daemon 被 pid lock 擋下時不再先動 config（寫 host_id 移到取得 pid lock 之後）。monitor 的設定也改走同一條路。

### Fixed：daemon 關閉或熱重啟時，進行中的請求不再打到已關的資料庫 — #1033（#2577）

- backup、hostconfig、profiles、push、devices、workbook 六個模組原本在「停止」那一步就關掉 SQLite，但 daemon 要等進行中的請求跑完才真正收工，中間這段時間的請求會打到已關的資料庫而失敗。改成請求都排空之後才關資料庫；熱重啟在重新執行前一樣會正常關檔。

### Fixed：介面 — #2569（介面線）

- 切回指揮台／聊天分頁時不再從舊位置滑到底；內容分批到達時保持閱讀位置。

## [1.0.0-alpha.700] - 2026-10-11

> daemon 與 SPA 都有改：**要部署 daemon**；mod 沒有改，不必重跑 `pdx setup`；SPA 已隨主機上的 dev server 生效。

### Fixed：mod 被停用或 `--safe-mode` 重開的 session，遠端又看得到它的提問 — #1832（#2555）

- daemon 判斷「這個 session 有沒有 mod」改看 30 秒內有沒有收到 mod 的事件（與介面的燈號同一個定義），不再看「曾經打過招呼」。之前只要打過一次招呼就永遠算有 mod，之後以 `--safe-mode` 重開、或 mod 被停用的 session，它的 AskUserQuestion／權限提示不會開唯讀卡片，遠端完全看不到。
- daemon 剛重啟的那幾秒會把有 mod 的 session 看成沒有，多開的唯讀卡片會立刻被 mod 自己的那張接管，畫面上只有一張。

### Fixed：第二個 daemon 關閉時不再拔掉第一個的 tmux hook — #1109（#2571）

- tmux 的 session hook 是整個 tmux server 共用的，而且每個 daemon 裝的內容都一樣；之前任何一個 daemon 關閉都會把它移除，同一個 tmux server 上還在跑的 daemon 因此悄悄退回 5 秒輪詢（新開的 session 要等一下才出現）。現在 daemon 關閉時不再移除，只在啟動時重新設定；熱重啟中間也不再有沒有 hook 的空檔。要清掉可用 Settings 裡的移除（`POST /api/hooks/tmux/setup`）。

### Changed：指揮台 — #2567（介面線）

- 指揮台的三種聲音（agent、系統、使用者）各有自己的排版；session 狀態列也帶主機色底；調整行距與字級。

## [1.0.0-alpha.699] - 2026-10-11

> daemon 與 SPA 都有改：**要部署 daemon**；mod 沒有改，不必重跑 `pdx setup`；SPA 已隨主機上的 dev server 生效。

### Added：App 可以改正在運作的 team 的授權目錄 — #2561（#2566）

- 新增 `PUT /api/team/roots`（只給 App 用，同調整上限）：送整份新清單與看到的 `roots_rev`；路徑存成真實路徑，重複或被包住的合併。兩個視窗同時改時，晚到的那個回 409 `rev_mismatch`（附目前的清單）；要移除的目錄底下還有 member、啟動中的 spawn 或進行中的收編時回 409 `roots_in_use` 並列出是誰。已在跑的 member 不受影響，只管之後的 spawn。
- roster 多帶 `roots` 與 `roots_rev`。App 端的編輯畫面由介面線接著做。
- spawn 寫入前會在同一個交易裡再確認一次工作目錄還在授權範圍內，避免剛好在目錄被移除時 spawn 進去。

### Fixed：`pdx token generate` 不再悄悄讓整台主機 401 — #2557（#2560）

- daemon 正在跑時，`pdx token generate` 預設拒絕、config 檔不動：之前它會直接改寫 config 的 token，而 daemon 記憶體裡仍是舊的，於是這台上所有 `pdx` 指令（mod、hook、接力）都回 401（2026-10-11 02:02 在 mlab 發生過）。真的要換就加 `--force`，它會說明 daemon 要重啟、App／手機／其他主機都要重新配對。
- token 明文只在終端機顯示；導向檔案或管線時只印 fingerprint 與路徑。

### Changed：狀態列底色帶主機色 — #2564（介面線）

- 終端機與 worker 的狀態列底色改成「主機色 16% 混進主題底色」，一眼看出是哪台主機；沒設主機色的主機維持原樣。文字在四個主題、八個預設主機色下對比都 ≥ 4.5:1。
- 指揮台 v3：內容貼齊底部；附件區加邊線與淡底（#2565）。

## [1.0.0-alpha.698] - 2026-10-11

> daemon 與 SPA 都有改：**要部署 daemon**；mod 沒有改，不必重跑 `pdx setup`；SPA 已隨主機上的 dev server 生效。

### Fixed：daemon 自己產生 token 時不再寫進 log、不再洗掉 config — #1950（#2554）

- daemon 第一次啟動、config 沒有 token 而自己產生一個時，明文只顯示在終端機（前景啟動、要拿去配對 App 的那一次）；導向 log 或管線時只印 fingerprint 與 config 的路徑。
- 寫回 config 時只改 token 那一行：其他設定、註解、未知的表格原樣保留，`--bind`／`--port` 這類啟動參數也不會被寫進去。寫不進去就拒絕啟動並說明怎麼修，不再帶著一個重啟就會換掉的 token 繼續跑。
- `pdx token generate` 改用同一套只改一行的寫法；所有寫 config 的地方改用各自的暫存檔並先 fsync 再換上。

### Changed：指揮台視覺 v2 — #2558（介面線）

- agent 文字排版一致；系統訊息改成灰色靠左；使用者訊息框改成淡紫細框、極淡底色，聊天的使用者泡泡改白字；輸入框改成終端機式；狀態列改成「repo [worktree] [模型 · effort]」，右側顯示花費，StatusBar 右側同色。

### Fixed：沒有 token 的 daemon 不再放行所有請求 — #1950（#2559）

- daemon 的 token 是空的時候，`/api` 一律回 401（之前是全部放行）。正常啟動的 daemon 一定有 token（見上一條），這是多一層保險；只有快速配對進行中的 `POST /api/pair/verify` 與 `/api/pair/setup` 照舊不需要 token（它們各自驗配對密碼，配對完成後就跟其他路由一樣要 token）。

## [1.0.0-alpha.697] - 2026-10-11

> 只動到 daemon 與 SPA：**要部署 daemon**；mod 沒有改，不必重跑 `pdx setup`；SPA 已隨主機上的 dev server 生效。

### Fixed：判斷不出 session 在不在時，不再當成「不在」 — #1857（#2552）

- 之前 registry 裡有一個活著的行程、但它的檔案驗證不了時，所有「session 在不在」的判斷都會把找不到的 session 當成已經不在：開著的申請被放棄、member 接力被判失敗、kill 把 member 標成永久消失、hook 的旗標檔被刪。現在這些動作只在確定「不在」時才做；判斷不出來就留到下一輪，並記一筆 log（同一件事每 10 分鐘最多一筆）。
- 換對話後一直等不到結果的接力，判斷不出來時最多再等 30 分鐘，之後照原規則處理，不會卡住這個 session 的下一次接力。
- kill 一個判斷不出在不在的 member 時回 503，請稍後重試，不再直接標成已消失。

### Fixed：介面 — #2550（介面線）

- Mac 的問題卡與 daemon 檢查答案的規則一致（#2548）：題目文字讀不出來的卡片只能在終端回答；送出被 daemon 拒絕時顯示 daemon 說的是哪一題，重試時清掉上次的失敗訊息。

## [1.0.0-alpha.696] - 2026-10-11

> 只動到 daemon：**要部署 daemon**；mod 的執行碼沒有改（只多一份測試用的共用案例檔），不必重跑 `pdx setup`；SPA 沒有改動。

### Fixed：daemon 停機時漏掉的 statusline 讀數，開機時補上 — #2545（#2549）

- daemon 停機期間 Claude Code 的 statusline 送不出去時，`pdx statusline-proxy` 會把每個 session 最新的一筆留在 `<data_dir>/statusline-pending/`；daemon 開機時把比手上新的讀數補進去（只補 model／effort／context 讀數，不重播畫面狀態），再刪掉檔案。所以停機時用 `/model` 換了 model，回來後交給 worker 也會帶新的 model（694／695 的 U18 已知限制）。
- 檔案只留最新一筆、目錄 0700／檔案 0600、檔名只接受安全的 session id、最多 512 個、超過 7 天開機時直接刪；正常 render 只多一次檢查目錄（約 1 µs）。

### Fixed：遠端回答 hook_ask 時檢查答案是否對得上題目 — #1845（#2548）

- `POST /api/team/approvals/{id}/decide` 回答 AskUserQuestion 時，答案要每題恰一個非空字串、不能有多餘的題目；對不上就回 400 並指出哪一題，不再先標成已回答、卡片顯示終端其實不會採用的答案。規則與 mod 的 `answersFit` 相同，兩邊共用同一份測試案例。

## [1.0.0-alpha.695] - 2026-10-11

> daemon、mod、SPA 都有改：**要部署 daemon，並重跑 `pdx setup`（mod 有改）**；SPA 已隨主機上的 dev server 生效。內嵌的 nexen 升到 v0.21.0（不升 schema，不必刪 DB）。

### Changed：交給 worker 時也沿用 model 與 effort — #1647 PR-2（#2543）

- 終端交給 worker（handoff），以及重建 worker 時，worker 每一個 turn 都會用這個 session 原本的 model 與 effort 啟動；例如原本是 Opus 5.5 xhigh，worker 不再用主機預設。搭配 694 的反方向，來回切換都會保留。
- 重建 worker 時以被取代那一段記下的值為準，statusline 只補缺的，daemon 重啟後也一樣。
- Bedrock、Vertex 的 model id 也會帶過去（之前只認 Anthropic 格式）。
- 讀不到數值時照舊用主機預設。已知限制：daemon 停機期間用 `/model` 換了 model、又在下一次 statusline 更新前就交給 worker，worker 會用換之前的 model（#2545 追蹤）。

### Fixed：終端先回答時，遠端的答案不再蓋掉它 — #1848（#2542）

- AskUserQuestion 在終端先回答、而 daemon 那時忙碌或正在重啟（`pdx ask begin` 3 秒內沒回）時，之前這次回答不會被記下：之後 begin 開出的卡片若被遠端（例如手機）回答，就停在遠端的答案；沒人回答也會在已結束的對話上開著，直到 30 秒後才被放棄。現在 mod 會改用「這個 session 的這一題」回報，等 begin 的卡片出現（最多約 45 秒）就把它標成終端已回答，遠端已先答的則記為終端覆蓋。
- 新增 `POST /api/ask/report`（不帶 id，依 session 與 tool use）與 `pdx ask report --session --tool-use --since`；只看這次 begin 之後建立的卡片，不會套到同一題更早留下的紀錄。

### Fixed：介面 — #2540（介面線）

- 新分頁畫布、模組面板、儲存空間列不再對螢幕閱讀器宣稱可用鍵盤拖曳（實際只能用滑鼠拖）；新分頁畫布只能用滑鼠拖的把手不再是 Tab 停靠點；tab 列的分頁可用 Enter／空白鍵選取（#2538）。

### Internal：介面 — #2546（介面線）

- team 席位的讀數帶上讀取時間與「是否過期」判斷（門檻暫定 30 分鐘），元件尚未使用，畫面不變（#2410）。

## [1.0.0-alpha.694] - 2026-10-11

> daemon 與 SPA 都有改：**要部署 daemon**；mod 沒有改，不必重跑 `pdx setup`；SPA 已隨主機上的 dev server 生效。

### Changed：交給 worker 再拿回來時保留 model 與 effort — #1647 PR-1（#2532）

- 從 worker 拿回終端（take-back、take-to-terminal），或交接被拒而退回終端時，續接指令會帶上這個 session 原本的 `--model` 與 `--effort`；例如原本是 Opus 5.5 xhigh，回來不再變成 high。
- 數值取自交接當下 statusline 回報的 model 與 effort，並記在 worker 上，所以 daemon 重啟後拿回來也一樣。
- 自訂的續接範本只要含 `--resume {id}` 也會補上；範本自己已寫 `--model` 或 `--effort` 的，那一項以範本為準。範本是 codex、opencode、包裝腳本，或含管線、引號等 shell 結構時，一律不改動。
- 讀不到數值（例如 session 還沒有 statusline）時，指令維持原樣。
- 終端交給 worker 時帶上 model 與 effort 是下一步（要等 nexen v0.21.0）。

### Changed：team 開機收尾集中一處 — #1968（#2535）

- daemon 啟動時修正上次留下的 team 資料，這些步驟集中到同一處、順序與失敗處理寫明並以測試釘住；行為不變。

### Fixed：介面 — #2537、#2539（介面線）

- Mac 桌面通知的內文改用跟手機推播相同的整理規則：連結與圖片只留文字，去掉程式碼區塊標記、反引號、粗體、標題井號、引用符號與清單符號，連續空白併成一格，太長就截斷加刪節號；兩邊共用同一份測試資料（#2539、#2144）。
- 窄模式的工作區按鈕、tab 列的分頁不再對螢幕閱讀器宣稱「按空白鍵可拾起拖曳」（實際只能用滑鼠拖）；窄模式工作區按鈕也不再多一個 Tab 停靠點（#2531）。

## [1.0.0-alpha.693] - 2026-10-11

> 只動到 daemon：**要部署 daemon（lead 與 member 兩台都要）**；mod 沒有改，不必重跑 `pdx setup`；SPA 已隨主機上的 dev server 生效。

### Added：lead 接力遠端 member 的收尾 — MR-3a-2（#2523）

- lead 的接力額度用完時，幫遠端 member 接力改成開核准卡片（之前直接回「不支援」）；按下核准才把接力指令送出，指令的建立時間取核准那一刻，所以等卡片等很久也不會被 member 那台當成過期指令拒絕。
- 接力指令 10 分鐘內送不到就作廢：member 那台還沒開始就取消並回報，已經開始的照常做完、結果照常回來，lead 這台不會出現「回報失敗但其實接力成功」。
- 兩台主機解除配對時，還在等結果的接力一併結束（原因 unpaired），不會卡住那個 member 之後的接力。
- 至此 member 接力規格（docs/specs/2026-10-10-member-relay-manual-and-cross-host-spec-plan.md）的六張全部完成。

### Fixed：介面 — #2529、#2530（介面線）

- 快速回覆編輯器裡的項目在別處被刪掉（409）時保留草稿（#2529）；工作區列的標題不再是看不見的 Tab 停靠點（#2530）。

## [1.0.0-alpha.692] - 2026-10-11

> 只動到 daemon：**要部署 daemon（lead 與 member 兩台都要）**；mod 沒有改，不必重跑 `pdx setup`；SPA 已隨主機上的 dev server 生效。

### Added：別台主機上的 member 用量到 70% 時，申請會傳到 lead — MR-4（#2522）

- 跨主機的 member（例如 air26 上的 iOS member）用量到 70% 時，它的 mod 照常申請接力；member 那台先確認 lead 那台支援這個通知才記下申請，再把申請送到 lead 那台，lead 收到「member air26/_<ref> 已用 N%，申請接力。M 分鐘內同意請執行：pdx relay air26/_<ref>」。lead 在時限內執行就接上；不理會就過期作罷，member 照舊會在 Claude Code 自動壓縮。
- lead 那台是舊版、或連不上時，member 那台不記申請、mod 保持安靜；申請在送出前已被接受或過期，就不再送。

### Changed（內部，對外無變化）：nex worker 狀態轉換的 hub 與分類 — PW-1（#2521，介面線）

- 為之後的 worker 推播鋪路；目前沒有任何訂閱者，行為不變。

### Fixed：介面 — #2524（介面線）

- 側欄工作區列與首頁列的按鈕，用鍵盤移動焦點時看得到外框。

## [1.0.0-alpha.691] - 2026-10-10

> 只動到 daemon：**要部署 daemon（lead 與 member 兩台都要）**；mod 沒有改，不必重跑 `pdx setup`；SPA 已隨主機上的 dev server 生效。

### Added：lead 可以幫別台主機上的 member 接力 — MR-3b（#2516）、MR-3a-1（#2515）

- lead 對另一台主機上的 member 執行 `pdx relay <主機>/_<ref>`，現在會真的接力：lead 這台先記下並送出接力指令，member 那台開始接力（寫交接檔、清空、帶新 session 開始），完成後通知回 lead 這台，名單換成新 ref、`pdx relay --wait` 拿到結果，lead 收到「member 接力完成」通知；失敗或取消也會回報原因。
- member 那台要允許這台主機管理 member（`allow-team`），而且 member 的 mod 要夠新；超過 10 分鐘才送到的接力指令會被拒絕，不會在很久之後突然開始。
- 還沒做（下一版）：lead 的接力額度用完時要開核准卡片（現在先回「不支援」）、指令送不到時的作廢、解除配對時收尾。

### Changed：介面 — #2510、#2513、#2517、#2519（介面線）

- deck 畫面外的回合延後渲染 markdown 並快取，300 回合的掛載從 525 ms 降到 116 ms（#2510）；刪除工作區時，分割分頁次要窗格裡它的設定頁只關窗格、保留分頁（#2513），套用分頁設定時也會丟掉指向不存在工作區的設定窗格（#2519）；worker 標題預抓在沒有窗格引用時清掉標記與摘要（#2517）。

## [1.0.0-alpha.690] - 2026-10-10

> 動到 daemon、mod 與 Electron：**要部署 daemon，並重跑 `pdx setup --agent cc`**（mod protocol 升到 4）；Electron 已重新打包，Mac App 用 dev update 更新；SPA 已隨主機上的 dev server 生效。

### Added：member 的 session 裡親手打 /relay 一律放行 — MR-1（#2491）、MR-2（#2489）

- 使用者在 member 的 session 打 `/relay`（或 `/relay now`）不再被「member 的接力由 lead 安排」擋住，照一般 session 的流程開核准卡片；接力完它仍是同一個 lead 的 member，lead 收到「member 由使用者手動接力：舊 ref → 新 ref」。開著無人值守也一樣要人按卡片，不會自動核准、不扣任何額度。mod 自己在 70% 發起的接力仍由 lead 決定。
- 跨主機的 member（例如 air26 上的 iOS member）也適用：接力在 member 那台進行，完成後 lead 那台的名單換成新 ref，舊 ref 照樣找得到它。lead 那台若是舊版、不認得這個通知，member 那台會丟掉它而不卡住其他通知。
- 正在被 kill 的 member 不能手動接力。

### Fixed：核准通知點擊只送到發出它的視窗 — #1919（#2508，介面線）

- Mac App 有多個視窗時，點核准通知只會在發出它的那個視窗開卡片；「開啟 session」照舊會到有那個分頁的視窗。

### Fixed：介面 — #2511（介面線）

- Worker 已消失／測試用分頁按「重試」後，鍵盤焦點留在原地。

### Refactor

- pairing.ts 純搬移拆成 types／transport／mint／session（#2509）。

## [1.0.0-alpha.689] - 2026-10-10

> 動到 daemon 與 SPA：**要部署 daemon**；mod 沒有改，不必重跑 `pdx setup`；SPA 已隨主機上的 dev server 生效。

### Added：輸入框可以附檔案 — #2483、#2490、#2500、#2502（介面線）

- 指揮台與聊天的輸入框可以貼上、拖放或按附件鈕附加檔案（#2483）；輸入框有焦點時拖放也看得到高亮（#2490）；超過 256 MiB 的檔案送出前就擋下（#2500）；終端機拖放上傳失敗時，狀態列說明原因（#2502）。

### Added：問題卡與終端機薄條 — U3-4 dock（#2488、#2494，介面線）

- 輸入框上方顯示待回答的問題卡，綁定它的核准；「已在終端機回答」提示從出現起算停留 3 秒（#2494）。

### Changed：手機上傳的限制 — #2466（#2492）、#2493（#2498）（介面線）

- 每支裝置的 token 同時最多 2 筆上傳，多的回 429（#2492）。撤銷裝置會中止它正在進行的上傳；裝置上傳有整體期限（#2498）。

### Fixed：tmux 卡住時不再拖住事件處理 — #2039（#2486）

- 查 pane 屬於哪個 session 的後援查詢原本沒有期限，tmux 不回應時會卡住事件處理與 mod。現在整次讀取共用一個期限，逾時就當這次讀取失敗、什麼都不廣播，下一輪重來。

### Fixed：介面 — #2496、#2497、#2499、#2503、#2504（介面線）

- lead 分頁拆出或合併視窗後不留殘影列（#2496）；對話內文的清單縮排與程式碼字型（#2497）、行內程式碼改成 chip（#2499）；主機的各項能力旗標斷線時不留舊值（#2503）；淺色主題的警告文字看得清楚（#2504）。

### Refactor

- TeamPanel.tsx 純搬移拆檔（#2495）、ApprovalDialogHost 的 helper 搬到 approval-dialog-helpers.ts（#2506），行為不變。

### Docs

- member 接力 spec＋plan：member 親手 /relay 一律放行、跨主機 member 接力（#2487）。
- 專案 CLAUDE.md：codex 仲裁改用 gpt-5.6-sol effort high，永遠不用 astra（#2505）。

## [1.0.0-alpha.688] - 2026-10-10

> 只動到 daemon：**要部署 daemon**；mod 沒有改，不必重跑 `pdx setup`；SPA 沒有變更。

### Fixed：按 Esc 或 App 中斷後，對話標題列的燈不再卡在「工作中」 — #2484（介面線）

- 中斷時 Claude Code 不會跑 Stop hook，對話標題列的燈原本只看 hook 的狀態，所以一直顯示工作中。現在標題列和燈號也套用 mod 回報的即時狀態（分頁本身的燈之前已經是這樣），Esc 和 App 的中斷鈕兩條路都會回到閒置。

### Fixed：效能頁的主機 CPU 在 macOS 26 恢復顯示 — #2013（#2482）

- macOS 26 拿掉了原本讀 CPU 的系統資訊（`kern.cp_time`），所以 CPU 一直顯示「無法使用」。現在改成在背景每隔一個更新週期跑一次 `iostat` 取真正的使用率：有人在看效能頁時才開始取樣，一分鐘沒人看就停；剛開始的第一兩秒顯示「等待中」。Linux 不受影響。

## [1.0.0-alpha.687] - 2026-10-10

> 動到 daemon 與 mod：**要部署 daemon，並重跑 `pdx setup --agent cc`**（mod 有改）；SPA 沒有變更。

### Fixed：在 App 按「中斷」後，燈號與這一輪不再卡在「工作中」 — #2479（介面線）

- 實測 Claude Code 在 App 的中斷之後不會觸發 Stop hook，也不寫中斷標記，所以這一輪一直被當成還在跑。現在 mod 中斷成功後自己把這一輪結束（`turn.complete`，原因 aborted）；daemon 收到後燈號回到閒置，對話裡這一輪記成「已中斷」，它底下還開著的步驟一起結束。
- 只處理 App 的中斷鈕；在終端機按 Esc 的情況是下一個 PR。

## [1.0.0-alpha.686] - 2026-10-10

> 動到 daemon 與 SPA：**要部署 daemon**；mod 沒有改，不必重跑 `pdx setup`；SPA 已隨主機上的 dev server 生效。

### Fixed：本機 lead 授權的資料夾記成真實路徑 — #2450（#2477）

- 申請 lead 時，資料夾範圍會先把捷徑（symlink）解析成真正的位置再存，核准卡片與 `pdx team` 顯示的就是那個位置；App 在卡片上新加的資料夾在按下核准時解析。
- 開 member 時，授權的資料夾如果已經不是當初那個（被換成捷徑），就不再算數——跟跨主機那條路同一套規則。這版之前核准的授權照舊方式判斷，升級不會讓現有的 team 失去權限。

### Added：分頁的聊天畫面與右側面板 — U3 掛載（#2473，介面線）

- 分頁的「聊天」顯示真正的對話內容，輸入框沿用指揮台同一個；指揮台與聊天共用右側面板（寬時並排、窄 pane 時疊在上面）；輸入框下方加上狀態列。

### Internal

- 修好兩個機器忙時才會偶發失敗的測試：profiles 的「刪除與寫入搶先」不再斷言誰贏過幾次（#2226）；team 套件裡故意塞滿訂閱者的測試不再把預期的 log 混進別的失敗輸出，失敗時仍會印出（#2210）（#2476）。

## [1.0.0-alpha.685] - 2026-10-10

> 只動到 daemon：**要部署 daemon**；mod 沒有改，不必重跑 `pdx setup`；SPA 沒有變更。

### Added：跑重活前先看磁碟，空間不夠就清 Go 編譯快取 — #2470（#2471）

- 完整測試、單套件測試、build、全專案 lint 這四種重活拿到資源名額前，daemon 先看 Go 編譯快取所在磁碟的剩餘空間：低於 **15 GiB** 先清掉快取裡 **2 小時以上沒動過**的項目再放行。
- 清完仍低於 **3 GiB** 照樣放行，但 log 與那張名額的回應會帶警告；`pdx lease acquire` 印在 JSON 的 `warning` 欄，`pdx lease run` 印在 stderr。
- 只清「確定是 Go 編譯快取」的目錄（根目錄不能是 symlink、要有 Go 自己寫的 README），且只刪兩位十六進位子目錄裡的一般檔案；每 10 分鐘最多清一次，清理不佔資源模組的鎖。

## [1.0.0-alpha.684] - 2026-10-10

> 動到 daemon 與 SPA：**要部署 daemon**；mod 沒有改，不必重跑 `pdx setup`；SPA 已隨主機上的 dev server 生效。

### Added：iPhone 可以上傳附件 — #2465（介面線）

- `POST /api/agent/upload` 開放給裝置 token（iOS 指揮台與聊天的附件），回應帶存檔路徑；`inject=0` 只存檔、不貼進終端機；大小上限裝置 64 MiB、管理端 256 MiB（超過回 413）。

### Added：指揮台上線 — U3（#2467、#2455、#2458、#2459、#2464，介面線）

- 分頁的「指揮台」顯示真正的對話內容與輸入框，送出走 682 起的 mod 通道（#2467）；輸入框本體與送出佇列（#2455）；每一輪的工作列與 iOS 一致（#2458）；右側面板與聊天畫面本體（#2459）；狀態列元件（#2464）。

### Fixed：介面 — #2472、#2468、#2462（介面線）

- 分頁關閉時釋放它的送出佇列、草稿、面板、摺疊與捲動記憶，修好長時間使用的記憶體洩漏（#2472）；重新連線後工作簿畫面會重新載入，名單座位的初次載入失敗會有限次重試（#2468）；聊天泡泡的清單縮排與行內程式碼樣式（#2462）。

### Fixed：過時的「接力會在回合結束時進行」通知不再送出 — #2440 後續（#2452）

- 第 15 分鐘那則通知送出前，會重讀接力狀態與 member 狀態；接力已經開始或 member 已閒下來就不送。

### Internal

- team roots 在 spawn 當下重新檢查的回歸測試，並如實註明殘餘的檢查到使用之間的空窗（#2254、#2451）。

### Docs

- 對話 golden 的 README 更正（#2460）。

## [1.0.0-alpha.683] - 2026-10-10

> 動到 daemon、mod 與 SPA：**要部署 daemon，並重跑 `pdx setup --agent cc`**（mod 有改）；開著的 session 會自動重新載入 mod（會印一次 reloaded 提示）。跨主機 team 的修正要兩端都升級才完整。SPA 已隨主機上的 dev server 生效。

### Fixed：接力撐得過 mod 重新載入；`pdx setup` 不再打斷接力 — #2441（#2442、#2448）

- mod 重新載入（例如部署時的 `pdx setup`）會丟掉記憶體裡的接力進度：接力檔已寫好卻沒有 `/clear`，卡到 15 分鐘停滯規則才失敗。現在 mod 載入時會從 daemon 的 hello 讀回自己進行中的接力：已寫好就接著 `/clear` 與 seed（使用者的回合還在跑就等它結束）；寫到一半就解鎖並以有名字的原因失敗，讓 lead 重送。
- `pdx setup --agent cc` 在有接力進行中時拒絕執行（exit 13、`relay_active`，列出 op），加 `--force` 才繼續；daemon 有回應但出錯也拒絕，完全連不上 daemon 才放行。

### Fixed：member 接力等得過長回合，沒回應時說出原因 — #2439（#2440）

- lead 同意接力後，member 若還在跑一輪（例如 subagent 在做事），接力會一直等到這一輪結束，最多 60 分鐘；第 15 分鐘通知 lead 一次「接力會在它的回合結束時進行」，到上限才以 `member_busy_timeout` 失敗。原本 15 分鐘就判 `member_unresponsive`。
- member 的 mod 60 秒內都沒回應時改成 `member_unseen`，提示「終端機可能停在對話框（例如 Mods: Enable hot reloading?）或 Claude Code 沒在跑」；member 停在權限或提問的提示時是 `member_blocked`。這些都維持 exit 14。

### Changed：member 主機自己判斷 adopt／spawn 命令是否過期 — #2398（#2436）

- lead 建立的 adopt／spawn 命令帶上建立時間（`created_at`）；member 主機收到超過 10 分鐘（加 2 分鐘時鐘誤差）的就以 `command_expired` 拒絕，lead 收到不重送。舊版 lead 不帶這個欄位就照舊處理；舊版 member 會忽略它。

### Changed：team roots 的編輯不再互相覆蓋 — #2340（#2447）

- 每台 host 的 team roots 帶版本號 `team_roots_rev`；`PUT /api/peers/hosts/{alias}` 可帶版本號當前置條件（過期就 409、不寫入），或用原子的 `add_team_roots`／`remove_team_roots`。不帶版本號的整份寫入照舊。
- `pdx peers host allow-team --root` 維持「設定成這些」（先讀版本號再寫，遇到別人剛改過就提示重試、不覆蓋）；新增 `--add-root`、`--remove-root` 做原子增刪。

### Changed：對話畫面與工作簿 — U3-1b-ii、U3-1c a、WA-2b-1b／2、標題列、team roots（#2433、#2446、#2432、#2444、#2438、#2454，介面線）

- 對話的串流、store 與分頁對應（#2433）；指揮台的項目元件（#2446）；工作簿畫面第二批（#2432）與完整的工作簿畫面：目前狀況、待辦、紀錄、重整（#2444）；標題列左右圖示垂直置中（#2438）；App 編輯 team roots 改用原子的 add／remove（#2454）。

### Refactor：拆開 team 的 handler.go 與 team_cmd.go — #2018（#2443、#2445）

- 純搬移：逐宣告位元組比對零差異，行為不變。

## [1.0.0-alpha.682] - 2026-10-10

> 動到 daemon、mod 與 SPA：**要部署 daemon，並重跑 `pdx setup --agent cc`**（mod 有改，協定升到 3）；開著的 session 會自動重新載入 mod（會印一次 reloaded 提示）。team.db 新增 `relay_asks` 表（新表，不需遷移）。SPA 已隨主機上的 dev server 生效。

### Added：member 自動接力改成「問 lead」 — member relay ask（#2428、#2429、#2431）

- member 的用量到 70% 時，它的 mod 會在回合邊界自動向 lead 送一則接力申請：`[pdx team] member … 已用 N%，申請接力。M 分鐘內同意請執行：pdx relay _<ref>（不同意不用回覆，過期即作罷）`。送出後 member 照常工作、不暫停。
- lead 在 5 分鐘內執行 `pdx relay _<ref>` 就算同意，接力在 member 的下一個回合邊界進行；額度規則照舊（無人值守時從 member 池扣）。5 分鐘內沒同意就作罷，member 照舊一路走到自動壓縮；之後要再多用 10 個百分點才會再申請（最多 70／80／90% 各一次）。
- 通知送失敗會由 daemon 每輪重送，直到送達或過期；申請與 lead 的 `pdx relay` 同時發生時嚴格排序；自動壓縮或 member 離開 team 會撤回申請（只發原本那則壓縮通知）。
- `pdx team` 的 TASK 欄顯示「接力申請（剩 N 分）」，`GET /api/team` 帶 `relay_ask_until`；新的 mod 內部指令 `pdx relay ask`。
- 會自己申請的 mod（協定 3）不再收到舊的「70% 且閒置」通知；舊版 mod 照舊。跨主機的 member 不申請（跨主機接力目前不支援）。

### Added：對話的型別、REST client 與文件模型 — U3-1b-i（#2427，介面線）

- App 端對話功能的資料層（還沒有畫面）。

## [1.0.0-alpha.681] - 2026-10-10

> 動到 daemon 與 SPA：**要部署 daemon**；mod 沒有改，不必重跑 `pdx setup`；SPA 已隨主機上的 dev server 生效。

### Fixed：代跑 subagent 的結束不再跳 Mac「任務完成」通知 — #2424（介面線）

- Claude Code 的背景 subagent 去跑 codex 時，每個 codex 結束都會在 Mac 上跳一則「任務完成」桌面通知並標成未讀（678 只擋了 iPhone 推播；Mac 的通知是 App 自己依廣播事件跳的）。daemon 廣播的 agent 事件現在帶 `from_proxy`，App 對這類結束不跳通知、不標未讀；session 自己的結束照舊通知。

### Fixed：daemon 重啟不再多等約 10 秒 — #2420（#2426，介面線）

- 680 起每次重啟，關機時都要等各 session 的 mod 掛著的長輪詢（App 送訊息用的 prompt/next、工作簿的 next）逾時，多花約 10 秒。現在關機時立刻回應這些長輪詢，mod 會自己重試。

### Changed：App 送出的訊息能精確對上對話裡的那一則 — U3（#2411，介面線）

- 對話的使用者訊息帶上 `client_msg_id`（依文字與時間一對一配對），App 先顯示的那則可以精確對到；送出／中斷／回答提問的能力變動時即時推給 App；有核准通道時 `answer_question` 標成 approval。

### Fixed：`pdx msg selftest` 在新版 Claude Code 上通過 — #2387（#2419）

- 臨時 session 改用 Haiku、先用 ToolSearch 載入 SendMessage，並註明全域 CLAUDE.md 對原生 SendMessage 的禁令不適用於這次檢查；只綁這次的探針、關掉 MCP 與 skills。2.1.294／2.1.296 不再出現「版本比驗過的新」的提醒（提醒門檻改用 selftest 通過的版本；peer 協定量測的版本不變）。

### Changed：分頁可以切換 終端機／指揮台／聊天 — U3-1a（#2421，介面線）

- 每個分頁有自己的 view 按鈕（終端機／指揮台／聊天），交接與拿回分成兩個按鈕；切換 view 時終端機不會被卸載。view 只記在這台裝置上。

### Changed：team 面板第 6 輪 — #2422（介面線）

- 標題列的 team 條移到右邊、視窗標題保持置中並先被截短；面板最小寬度隨燈號樣式與圓環大小調整（徽章 356／圖示點 412），拖曳過的寬度會被記住；每種模式只留一個「移到標題列」按鈕。

### Added：工作簿畫面第一批 — WA-2b-1a（#2412，介面線）

- team 面板接上工作簿：每席的目前事項一行、Notebook 按鈕、點進去的詳情（先放佔位）、已結束的清單（接上 674／679 的工作簿資料層）。

### Refactor：拆開過大的 team store 檔案 — #2381（#2415、#2416）

- 純搬移：命令 store 依領域拆成六個檔，spawn 收尾的交易搬到專責檔案；逐宣告位元組比對零差異，行為不變。

### Internal

- 修好高負載下會偶發失敗的 `TestRoster_SpawnAnnouncesTheClaimedTitle`（#2210、#2425）。

### Docs

- member 自動接力改成問 lead 的 spec＋plan（#2423）。

## [1.0.0-alpha.680] - 2026-10-10

> 只動到 daemon：**要部署 daemon**；mod 與 SPA 沒有改，不必重跑 `pdx setup`（mod 沒變，`pdx setup` 也不會再重寫 mod 資料夾）。跨主機 team 的修正要 member 主機也升級才生效。

### Fixed：member 主機重啟後，lead 那邊的跨主機 member 不再空白 — #2406（#2408）

- 每個 session 最後一次的 statusline 讀數（用量、model、effort 與時間）會存進 daemon 的資料庫，開機時只為仍然開著的 session 讀回來。原本只存在記憶體裡，member 主機一重啟就清空，要等那個 session 下一輪對話才補回來，lead 的 team 面板那段時間只能顯示空白。數值有變才寫，每 10 秒合併寫一次。

## [1.0.0-alpha.679] - 2026-10-10

> 動到 daemon、mod 與 SPA：**要部署 daemon，並重跑 `pdx setup --agent cc`**（mod 有改）；開著的 session 會自動重新載入 mod（會印一次 reloaded 提示）。team.db 會換一個唯一索引（自動遷移，開機不會因此失敗）。SPA 已隨主機上的 dev server 生效。

### Added：App 可以經由 session 的 mod 送出訊息與中斷 — U3-0b mod（#2402，介面線）

- mod 會宣告 `prompt.v1`，並對目前的 session 掛著長輪詢等 App 的指令：送出時以使用者本人的身分送進 session（這一輪還在進行就立刻回 busy）；中斷時停下正在跑的那一輪（沒有在跑就回 not_running）。`/clear` 之後原本那個 session 的指令會被丟掉。mod 重新載入後，`POST /api/conversations/claude/{sid}/submit`／`interrupt` 才會從 409 `no_mod` 變成可用。

### Changed：對話裡標出訊息從哪裡來 — #2396（#2405，介面線）

- plugin 自己發的提示（例如接力）與以 peer 包裝開頭的訊息，現在標成 peer 來源並帶上寄件者；只靠文字包裝、無法驗證的標成未驗證；寄件者名稱會先清理過。

### Changed：mod 沒改就不重寫 mod 資料夾 — #2403（#2404）

- `pdx setup` 改成比對 mod 的內容：一次發版如果沒動到 mod，資料夾裡一個檔都不寫，開著的 session 也就不會再印「purdex: … changed — reloaded」；mod 真的改了才整包換一次（舊版留下、新版已刪的檔案也會清掉）。hooks 狀態的「mod 過期」也改成看內容，不再因為版本號不同就誤報。

### Changed：member 主機的命令紀錄只留 30 天 — #2265（#2399）

- 已決定的跨主機命令紀錄超過 30 天就分批刪除；仍有效的 adopt 紀錄會保留，讓晚到的撤銷照樣有效。lead 端也加了防線：開機先讓逾時的 adopt／spawn 作廢，送出時絕不送超過 10 分鐘的。

### Changed：資料庫保證一個 session 最多只當一個 member — #2152 後續（#2400）

- team.db 的唯一索引改成同時涵蓋 active 與 killing（kill 進行中）：同一個 session 不可能同時有兩筆有效的成員資格。遷移在同一個交易裡處理既有的重複資料（保留 active，否則保留最新的，其餘標成 gone），任何一步失敗都整筆退回、改建保底索引，開機不會失敗。

### Changed：team 面板 — WA-2a′、單行格子（#2355、#2407，介面線）

- team 面板四種狀態、標題列的 team 條與 Notebook 按鈕；單行格子：側欄列配置、用量圓環取代主機方塊、符號用主機主色並做視覺置中、格子間距 4px、面板預設寬 412 讓 lead 加 3 位 member 排得下一列；context 數字改成顯示剩下多少（圓環仍畫已用）。

### Added：工作簿第 2 版資料層 — WA-1b（#2388，介面線）

- App 端讀取待辦、重整與往前載入更多紀錄的資料層（還沒有畫面）。

## [1.0.0-alpha.678] - 2026-10-10

> 只動到 daemon：**要部署 daemon**；mod 與 SPA 沒有改，不必重跑 `pdx setup`。

### Fixed：Claude Code 底下代跑的 codex 不再狂發「任務完成」推播 — #2397（介面線）

- Claude Code 的背景 subagent 去跑 `codex exec` 時，每個 codex 結束都被當成那個 session 結束，一分鐘可以收到好幾則「任務完成」。現在認得出這是代跑的 subagent，它們的結束（Stop／StopFailure）不再推播；session 自己的結束照舊推播。

### Changed：App 經由 mod 送出的訊息會出現在對話裡 — U3-0b 第一部分（#2394，介面線）

- App 透過 session 的 mod 以使用者身分送出的訊息（`asUser`），轉錄檔裡標的是 plugin 來源，原本整筆被略過，對話裡看不到、App 也對不上自己先顯示的那一則。現在當成使用者訊息；plugin 自己發的提示（例如接力）照舊略過。

### Added：App 送出／中斷訊息的 daemon 端 — U3-0b 第二部分（#2395，介面線；補記：已隨本版出去，當時漏記）

- 新路由 `POST /api/conversations/claude/{sid}/submit`、`interrupt` 與 mod 用的 `/mod/v1/prompt/next`、`result`；snapshot 帶 capabilities，`/api/info` 宣告 `conversations.submit.v1`。mod 還沒宣告 `prompt.v1` 前一律回 409 `no_mod`。

### Fixed：kill 本機 member 時先佔住、再送訊號 — #2152（#2386）

- kill adopted 或 spawned 的本機 member，改成先把它標成「kill 進行中」（killing）才送訊號或關 tmux；這段期間同一個 lead 的 release、接力與第二次 kill 都會被擋，不會出現「行程已經被殺、卻回報 release 成功」。送不出去就退回 active；daemon 在中間當機的話，開機時只把行程還在的退回 active，不會在開機時替它送訊號；卡住的由 sweeper 收尾。killing 期間這個 session 不能被加成另一個 member。

### Fixed：本機 spawn 收尾的三個舊問題 — #2384（#2392）

- team 結束時，還在進行的本機 spawn 一起標成失敗，等待中的請求會立刻拿到結果，tmux 由 runner 收掉；原本可能在已結束的 team 裡長出一個 active member。
- 收尾時遇到舊版留下的同一筆 member，要 team、session、主機、pane、tmux 身分都相符而且還是 active 才沿用，否則整筆退回。
- 中止 spawn 時改成先把 op 標成失敗、搶到的人才關 tmux，不會誤關另一個已經成功的 runner 的 session。

## [1.0.0-alpha.677] - 2026-10-10

> 只動到 daemon：**要部署 daemon**；mod 與 SPA 沒有改，不必重跑 `pdx setup`。

### Fixed：斜線命令之後的第一輪沒有記到工作簿 — #2390（介面線）

- 下過斜線命令（`/workbook refresh`、`/model`、`/clear` 等）之後，下一輪結束時沒有自己的紀錄；前一輪反而在再下一輪結束時才補記，還被標成那一輪的時間與內容。原因是斜線命令在轉錄檔裡自成一輪，daemon 以為「新的不只一輪」就不認領；現在只要最新那一輪還在進行、還沒有任何文字，就認定是這次結束的那一輪。676 的 #2385 修了游標，這次補上另一半。

## [1.0.0-alpha.676] - 2026-10-10

> 只動到 daemon：**要部署 daemon**；mod 與 SPA 沒有改，不必重跑 `pdx setup`。

### Fixed：重整之後的下一輪沒有記到工作簿 — #2385（介面線）

- 按了 `/workbook refresh` 或 Mac 的重整之後，下一輪結束時常常沒有紀錄（也就不會推播），要等再下一輪才補記。原因是 daemon 把「重整」那筆紀錄當成一般的一輪來接續；現在找上一輪、配對推播、整理時參考的前幾輪，都只看一般的一輪。#2370 引入。

### Fixed：spawn 最後一步失敗時不再留下 member 和 task — #2105（#2382）

- spawn 收尾時，「標記完成」跟「寫入 member 與第一張 task」改在同一個交易裡做：其中任何一步失敗，或被結束 team／中止搶先，三者都不寫。原本 client 看到 spawn 失敗，team 卻還佔一個名額、`pdx task ls` 也留著一張沒人做的 task。

## [1.0.0-alpha.675] - 2026-10-10

> 只動到 daemon：**要部署 daemon**；mod 與 SPA 沒有改，不必重跑 `pdx setup`。跨主機 team 的修正要兩端都升級才完整（舊版收件端會忽略新欄位，不會出錯）。

### Added：對話內容多了提問、讀取範圍、搜尋範圍、新建檔案與壓縮摘要 — U3-0（#2379，介面線）

- `/api/conversations` 的 step 新增：`question`（AskUserQuestion 這類提問與使用者的回答；拒答記成被使用者拒絕）、`read` 的 offset／limit、`search` 的範圍（路徑、glob 或 `web`）、`diff.created`（Write 新建的檔案）、`system compacted` 帶壓縮摘要。
- 工具摘要的寫法改成跟 Collie 一致：檔案類工具顯示**完整路徑**（原本只有檔名）、換行與連續空白收成一格、超過 200 字截斷加 `…`。**App 端顯示的摘要文字會變**，iOS 要重新釘 fixtures。

### Fixed：team 改名後才加入的 member 立刻顯示正確的名稱與顏色 — #2346（#2378）

- adopt／spawn 的跨主機命令會帶上 team 目前的短名與顏色，member 主機一收到就存下；原本要等到下一次改名才更新。外觀在寫入命令的同一個交易裡讀取，改名跟加入同時發生也不會讓舊外觀蓋掉新外觀。

### Fixed：開機清理孤兒 tmux 時，只在標記沒變的當下才關 — #2350（#2380）

- 開機清理 spawn 留下的孤兒 tmux session，改成在同一次 tmux 呼叫裡由 tmux 自己比對「還是同一個 tmux server、標記還是那個 spawn」才關；使用者剛好在那一刻清掉標記、把 session 留給自己用時，不會被誤關。

## [1.0.0-alpha.674] - 2026-10-10

> 只動到 SPA：**不必部署 daemon、不必重跑 `pdx setup`**；SPA 已隨主機上的 dev server 生效。

### Added：工作簿資料層（第 1 版）— WA-1a（#2372，介面線）

- App 端開始接收工作簿：讀取對話與紀錄的型別與嚴格解析（格式壞掉的單筆整筆丟掉、只警告一次；紀錄的對話跟外層對不上也丟掉）、工作簿 store（每台主機、每段對話各自一份，最新在前、依 id 去重，最多留 500 筆，最近用的對話優先保留）。
- 收到 `workbook.entry`／`workbook.status` 事件直接更新，事件不會觸發重新抓取；只有 team 席位或打開的畫面才抓，每席每次連線抓一次。偵測主機是否支援 `workbook.v1`／`v2`，主機移除或改指向時清掉它的資料。
- 這一版還沒有畫面；待辦、重整等第 2 版功能另見 WA-1b。

## [1.0.0-alpha.673] - 2026-10-10

> 動到 daemon 與 mod：**要部署 daemon，並重跑 `pdx setup --agent cc`**（mod 有改）；已開著的 session 要重新載入 mod 才會有 `/workbook refresh`。

### Added：工作簿第 2 版——待辦清單與完成紀錄 — WB-2b-i（#2369、#2370，介面線）

- 每筆紀錄多了種類（一般／重整）、用量與「這一筆對待辦做了什麼」；新路由 `GET /api/workbook/conversations/{provider}/{session_id}/todos`（未完成／已完成，可分頁）；待辦變動時發 `workbook.todos` 事件；能力 `workbook.v2`。待辦只能看，由每一輪的整理與重整更新。

### Added：分岔重整（手動）— WB-2b-i、WB-2b-ii（#2370、#2373）

- `POST /api/workbook/conversations/{provider}/{session_id}/refresh` 與 session 內的 `/workbook refresh`：用主模型讀整段對話，重寫目前狀況並整理待辦，結果記成一筆「重整」紀錄。只有手動觸發；session 還在、mod 宣告支援時才可按（`refresh_available` 與 `workbook.refresh_available` 事件）。**注意耗用**：重整用的是主模型、帶整段對話，長對話在快取冷掉時一次可達數十萬 token。

### Changed：team 珠子與「代理 lead 列」也顯示 subagent 點點 — #2374（補記：已隨本版出去，當時漏記）

- 側邊欄 team 的珠子跟分頁列一樣，會畫出那一席的 subagent 點點（浮在機器人圖示左邊、不佔寬度，沒有 subagent 就不畫）；lead 分頁關掉時代替它的那一列也照樣畫。收合時的細條不變。

### Docs

- team 單行格子設計稿（#2371）。

## [1.0.0-alpha.672] - 2026-10-10

> 只動到 daemon：**要部署 daemon**；mod 與 SPA 沒有改，不必重跑 `pdx setup`。

### Changed：「做完／出錯」的推播改用工作簿的短句 — WB-3（#2365，介面線）

- 一輪結束（Stop）或出錯（StopFailure）要推播時，daemon 最多等 8 秒（主機設定 `workbook.push_wait_s`，0＝不等）拿這一輪工作簿的推播短句：標題改成「<主機>：<session>・<事名>」（沒有主機標籤時「<session>・<事名>」），內文是那一句短句，payload 帶 `purdex.workbook = {conv_key, entry_id}`，iOS 點推播可以開到那一筆。沒有紀錄、整理失敗或逾時就照舊用原本的標題與內文。要求權限與其他通知不變。Mac 在場時照舊不推。

### Internal

- 對話轉譯 README 改用短 sha，修好 main 上的守門測試（#2367）。

## [1.0.0-alpha.671] - 2026-10-10

> 只動到 daemon 與 SPA：**要部署 daemon**；mod 沒有改，不必重跑 `pdx setup`；SPA 已隨主機上的 dev server 生效。

### Fixed：工作簿每一輪晚一輪才記到 — #2360（介面線）

- 一輪結束（Stop）的當下，轉錄檔常常還沒寫入那一輪最後的回覆文字（差約 50 毫秒），於是那一輪被跳過、到下一輪才補記，每一輪都慢一輪。現在 daemon 每 100 毫秒重讀一次、最多等 1.5 秒，逾時才改用 hook 帶來的文字；每一輪結束後約 2 秒內就有自己的紀錄。

### Fixed：介面 — #2358、#2359（介面線）

- 用量環的底色調亮一點。
- team 面板單行模式的容量改成逐格加總實際寬度，不再把放得下的 member 擠到「更多」。

### Internal

- 對話轉譯的 turn rows 測試資料（#2361）。

## [1.0.0-alpha.670] - 2026-10-10

> 動到 daemon 與 mod：**要部署 daemon，並重跑 `pdx setup --agent cc`**（mod 有改）；已開著的 session 要重新載入 mod（`/reload-plugins` 或開新 session）才會開始寫工作簿。

### Added：工作簿開始寫紀錄 — WB-1c（#2348）、#2351（介面線）

- 每個帶 Purdex mod 的 Claude Code session，每結束一輪（有回覆或出錯的那種，不含 subagent），mod 會向 daemon 領一份整理工作，用這個 session 自己的帳號跑一次 Haiku（effort low），把結果交回 daemon；daemon 檢查、去除秘密後寫進工作簿（事名、推播短句、紀錄、目前狀況、待辦）。整理在背景進行，session 從不等它。
- 修正：Stop 當下剛結束的那一輪在轉錄檔裡還沒標成結束，原本會被跳過、讓紀錄慢一輪；現在那一輪會被正確納入。
- 推播還沒改用工作簿的短句（之後的 WB-3）；App 的工作簿面板也還在做。

### Docs

- 介面 U3 plan（#2352）。

## [1.0.0-alpha.669] - 2026-10-10

> 動到 daemon 與 SPA：**要部署 daemon**；mod 沒有改，不必重跑 `pdx setup`；SPA 已隨主機上的 dev server 生效。部署時 team.db 的 remote_members 自動加 `team_label`、`team_color` 欄。跨主機的修正要**兩台都部署**才會生效。

### Added：team 改名同步到被收進的那台 — #2288（#2347，A 線）

- 在 lead 那台改 team 的名稱、短標籤或顏色時，同一筆交易裡對有遠端 member（或正在開 member）的主機排出 `team.appearance` 指令；那台更新自己的紀錄，通知與畫面改用新名稱。沒有遠端 member 的 team 改名完全不連網路；那台沒宣告支援或連不上時略過並記一行 log（那台停在舊名，直到下次改名）。

### Fixed：team 結束時收掉還沒開好的遠端 member — #2327（#2337，A 線）

- lead 那台結束 team 時，「結束」也送到只有開人中的主機，並釋放座位；被收進的那台收到後把還沒開好的那筆標成放棄、關掉它的 tmux session，不會留下沒有 lead 的 member。

### Fixed：開機清掉沒有紀錄的 spawn tmux session — #2341（#2344，A 線）

- daemon 啟動時，帶開人標記、而本機紀錄已失敗的 tmux session 會被關掉（動手前再確認一次是同一個 session）；找不到紀錄的只記 log、不動。

### Changed：介面 — X5-App-b（#2338）、#2339、#2349（介面線）

- 主機頁可設定每台配對主機的 team 同意（允許開 member）與可用資料夾。
- 用量環：已用的環改成逆時針長，team 的 context 環用「已用」的顏色。
- 從 team 面板點 member，分頁開在 lead 的工作區、群組旁邊。

### Docs

- 跨主機 team spec 補 §14：實作期間定下的 11 條修正（#2343）；介面 U3 spec（#2345）。

## [1.0.0-alpha.668] - 2026-10-10

> 動到 daemon 與 SPA：**要部署 daemon**；mod 沒有改，不必重跑 `pdx setup`；SPA 已隨主機上的 dev server 生效。跨主機的新功能要**兩台都部署**才會生效。部署時 team.db 自動加 `remote_spawns` 表與欄位。

### Fixed：被收進 team 的遠端 member 點了對不到分頁、TITLE 是「-」 — X5-fix（#2335，A 線）

- 遠端 member 的 tmux 名稱寫入時拆成 session 名與 pane；名單與 `GET /api/team` 讀取時一律只取 session 名，已部署的那一列不用遷移就恢復正常。App 依 (主機, tmux 名) 能對到那台的 session 與分頁。
- 遠端 member 的 TITLE 取自那台主機回報的資料（讀不到時沿用上一次的）。

### Added：從 lead 那台在別台開 member — X4b（#2323、#2328，A 線）

- `pdx spawn --host <別名> --cwd <那台的絕對路徑>`：先確認那台接受開 member 且允許我們，再在同一筆交易裡建立遠端開人紀錄、佔座位、排出指令；CLI 等到結果（開好回 member 位址與第一個任務；失敗依原因 exit 13／14／1）。
- lead 那台套用「已開好」「開失敗」回報：開好時建立 member、第一個任務建在 lead 這台；失敗時釋放座位。這版開始宣告接受這兩種回報。

### Added：遠端 member 的回報與任務 — X6（#2329、#2332，A 線）

- 被收進別台 team 的 member 照常用 `pdx report`／`pdx task`：它那台把呼叫轉給 lead 那台（只帶 member 編號，不帶呼叫者資訊），lead 那台只放行三件事——發回報、列出自己的任務、改自己任務的狀態，身分只由「哪台主機＋member 編號」決定。lead 那台連不上時回 503 `lead_unreachable`。

### Changed：回報先確認對方接受才送 — X4a-3（#2318，A 線）

- 被收進的那台送回報前，先看 lead 那台宣告接受哪些種類；還不接受的先扣著（不丟、不算重試次數），對方升級後下一輪就送。

### Changed：工作簿 daemon 第二、三段 — WB-1b′-b／c（#2320、#2321、#2325、#2331，介面線）

- 回合結束的訂閱、補抓、每個對話的佇列與結果處理；mod socket 的工作派發與回報路由（`/mod/v1/workbook/next`、`/result`），事件批次可宣告能力。mod 端執行器還沒上，所以每一輪目前都記成「沒有 mod（no_mod）」，不影響現有行為。

### Changed：team 面板顯示遠端 member — X3c-App（#2315）、X5-App-a（#2334）、#2330（介面線）

- 收進別台 session 的流程與等待畫面；遠端 member 顯示主機標籤與狀態文字，依主機對到那台的分頁；面板標題列整列可點、不會選到文字。

## [1.0.0-alpha.667] - 2026-10-10

> 動到 daemon 與 SPA：**要部署 daemon**；mod 沒有改，不必重跑 `pdx setup`；SPA 已隨主機上的 dev server 生效。部署時 team.db（spawn_ops 新欄、team_fact_log 新表）與 workbook.db（結構第 2 版、待辦表）自動遷移。跨主機的新指令與回報要**兩台都部署**才會生效。

### Added：把別台主機的 session 收進 team — X3c（#2305，A 線）

- `pdx adopt <別台別名>/_<ref>`：先向那台確認它接受收進、允許我們、找得到那個 session，才建立核准；核准＝使用者同意，在同一筆交易裡建立「加入中」的 member 並把指令排進送出佇列。新路由 `GET /api/team/adoptions/{id}` 可等到結果（加入、失敗、作廢）；CLI 依結果結束（0 加入、13 失敗、14 作廢、11 等到上限仍在加入中）。

### Added：lead 那台接收對方的回報 — X3b-2（#2306，A 線）

- 新路由 `POST /api/peers/team/facts`：被收進的那台回報「已結束」時，lead 那台把對應 member 標成結束（team 還在進行時才改）；同一回報重送回同一結果、內容不同回 409，拒絕也記錄。這版開始宣告接受的回報種類（`fact_kinds`）。

### Added：被收進的那台可以 kill 與開 member — X4a（#2308、#2314，A 線）

- 收到 lead 的 kill：先在交易裡決定並記錄，再確認仍允許 team，才對記錄中的那個行程送 SIGTERM（行程號碼與啟動時間都要對得上）。
- 收到 lead 的開 member 指令：工作目錄必須在這台授權給那台 lead 的資料夾內（每次都重新解析、被換成捷徑的資料夾不算），每台 lead 最多 16 個；用這台自己的啟動指令開 session，成功或失敗都在同一筆交易裡回報給 lead。這版開始宣告接受 kill 與 spawn。lead 那台的 `pdx spawn --host` 在下一步（X4b）。

### Changed：工作簿 daemon 第一段 — WB-1b′-a（#2310、#2311、#2312，介面線）

- workbook.db 升到第 2 版：紀錄多了種類與用量欄、新增待辦表；內建 prompt 第 2 版（含待辦，effort low）與七個欄位的回覆檢查、整理用的輸入組裝。還沒有開始整理紀錄（要等 mod 端執行器）。

### Changed：team 面板 — TI-7（#2300）、#2313（介面線）

- 面板標題點一下切換模式，雙擊可改 team 名稱、短標籤與顏色；分頁群組陰影固定為 V0，試用設定移除。

### Fixed：燈號與用量環 — #2317、#2307（介面線）

- session 在問你問題時，燈號維持等待中的黃色，不再被「未讀」蓋成紅色。
- context 的數字改回顯示「剩餘」，環仍顯示「已用」。

### Docs

- 工作簿 plan 第 4 版（#2301）、spec 修正（#2316）。

## [1.0.0-alpha.666] - 2026-10-10

> 動到 daemon、mod、`pdx` 指令與 SPA：**要部署 daemon，並重跑 `pdx setup --agent cc`**（mod 有改）；SPA 已隨主機上的 dev server 生效。

### Added：lead 的終端機底部顯示 team 人數 — TI-5b（#2302，介面線）

- 帶 team 的 lead session，終端機底部模式列多一個「lead mode · N members」（N 含別台主機的 member）。mod 每 15 秒向本機 daemon 查一次，畫面只讀查到的值；查不到就維持上一次的值，不是 lead 就不顯示。

### Added：team 名單顯示 member 在哪台主機 — X5（#2299，A 線）

- team 名單（App 的事件與 `GET /api/team/roster`）與 `GET /api/team` 的 member 多帶 `host_id`、`host_alias`、`context_unavailable`；別台主機的 member 位址是 `<那台別名>/<ref>`，加入中、放出中、結束中的也會列出。
- `pdx team` 多一欄 HOST；別台 member 的 context 與模型讀自那台的 `GET /api/peers`（快取 10 秒；名單從不等遠端，`pdx team` 最多等 3 秒），那台沒回應就顯示「(主機無回應)」。`/api/peers` 的 context 資訊多帶 `model_id`、`effort`。

### Changed：只看本機 member 的地方不再碰到別台的 — X3b-1c（#2293，A 線）

- 存活檢查、最後一輪、member 接力、放出／kill／消失標記、呼叫者身分判斷等只看本機的 member；座位、任務指派、角色判斷照舊兩台都算。別台 member 不能做 member 接力（回 `relay_unsupported`）。

### Fixed：用量環的意思 — #2303（介面線）

- context 環與數字改成顯示「已用」；5 小時與每週額度顯示「剩餘」。

## [1.0.0-alpha.665] - 2026-10-10

> 動到 daemon 與 SPA：**要部署 daemon**；mod 沒有改，不必重跑 `pdx setup`；SPA 已隨主機上的 dev server 生效。跨主機的「宣告指令種類」要**兩台都部署**才會生效（被收進 team 的那台也要升級）。

### Added：被收進 team 的那台會通知自己的 session — X3d-2（#2291，A 線）

- 被別台 lead 收進、放出、換 lead、team 結束或在本機結束時，這台會以 lead 的身分把固定文字的通知送進那個 session；文字只填 lead 位址與 team 名稱（投遞當下讀，改名後也是新名字），不用對方傳來的文字。送不到會退避重試、不放棄；session 已不在或狀態已變就作廢並記 log。

### Added：開始接受跨主機 team 指令 — X3d-3（#2294，A 線）

- 這台對配對主機宣告可接受「收進、放出、結束、換 lead、撤回」五種 team 指令；lead 那台要先讀到宣告才會送。開 member（spawn）與 kill 仍不接受（之後的 X4a）。

### Added：mod 可以查自己的 team 角色 — TI-5a（#2295，介面線）

- mod socket 新增 `GET /mod/v1/team?session_id=…`（只在本機 socket，不在網路埠）：回這個 session 是 lead／member／都不是，lead 另回 member 數（含別台的）與 team 短標籤。之後 mod 底部的「lead 模式 · N 位 member」會用它。

### Changed：team 介面第二輪 — TI-6（#2284，介面線）

- 上方列與左側清單拿掉 team 膠囊、上方列不再有收合按鈕（跟側欄共用狀態）；分頁群組陰影提供四種試用樣式（設定 → 介面 → 分頁，預設第三種）。

### Docs

- 工作簿 spec v2（#2296）：整理改由 session 自己的 mod 呼叫 Haiku、待辦清單與完成紀錄（只讀）、手動「分岔重整」、iOS 詳情頁；新 prompt 文件（prompt_ver 2）。

## [1.0.0-alpha.664] - 2026-10-10

> 只動到 daemon：**要部署 daemon**；mod 與 SPA 沒有改，不必重跑 `pdx setup`。部署後 team.db 的 teams 表多一欄 `team_color`（自動加，可重入）。

### Added：team 改名、短標籤、顏色 — TR-1（#2290，介面線）

- 新路由 `PUT /api/team/appearance`：App 一次送出 team 的名稱、短標籤、顏色（0–7，或 null＝照舊自動配色）。短標籤留空時照建立時的規則從名稱推；只能改還在進行的 team（已結束回 409 `not_live`）。改完 team 名單即時更新，名單多帶 `team_color`（自動時不帶）；能力 `team.edit.v1`。每次修改記一行 log（舊值→新值、來源）。
- 跨主機：被收進 team 的那台還會看到舊名稱與自動顏色，之後由 #2288 同步。

### Added：工作簿讀取 API 與即時事件 — WB-2（#2286，介面線）

- `GET /api/workbook/conversations/{provider}/{session_id}`（同一條接力鏈的任一個 session 都查得到同一本）與 `GET /api/workbook/entries`（依時間查跨對話的紀錄）；新增或狀態改變時發 `workbook.entry`／`workbook.status` 事件；能力 `workbook.v1`。手機（已配對的裝置）可讀。目前還不會產生任何紀錄，整理流程改走 mod 後才接上。

### Added：跨主機 team 的放出、結束、換 lead 指令 — X3b-1b（#2282，A 線）

- lead 那台放出或結束遠端 member、team 結束、lead 接力換人時，會在同一筆交易裡把對應指令排進送往對方主機的佇列；`pdx team kill`／`release` 可以用 `<別台別名>/<ref>` 指定遠端 member。目前還沒有遠端 member，所以不會真的送出。

### Changed：peers 投遞的目標端共用一份 — X3d-1（#2287，A 線）

- 收訊投遞的「重新確認目標、限流、建回覆通道、寫入」抽成共用函式，並新增給 team 用的窄接縫（遠端 member 的通知由它那台以 lead 身分投遞）。現有收訊行為不變；接縫目前沒有人呼叫。

### Docs

- team 面板：標題列點一下切換模式、雙擊改名／標籤／顏色（#2289）。

## [1.0.0-alpha.663] - 2026-10-10

> 動到 daemon、`pdx` 指令與 SPA：**要部署 daemon**；mod 沒有改，不必重跑 `pdx setup`；SPA 已隨主機上的 dev server 生效。

### Added：列出並結束被別台收走的 session — X2c-1b（#2277，A 線）

- 新路由 `GET /api/team/remote-members`（依建立時間排序）與 `POST /api/team/remote-members/end`：看這台有哪些 session 被別台主機收進 team，並可在本機結束。主機設定頁之後會用它們。
- `pdx peers host allow-team <別名> off --end-members`：關掉同意的同時，結束那台主機收走的所有 session；`pdx peers host list` 多一欄 MEMBERS。

### Added：跨主機 team 的回報開始送出 — X2c-2（#2283，A 線）

- 被收進 team 的 session 結束時，這台會把「已結束」送回 lead 那台（共用 lead 那邊的送出佇列）。lead 那台要等下一步（X3b-2）才收得到；在那之前回報會排隊、退避重試，屬預期行為。lead 主機被解除配對或長期不認我方 token 時，相關 member 在本機結束。

### Added：lead 那台的遠端 member 狀態 — X3b-1a（#2276，A 線）

- lead 那台記錄遠端 member「加入中／在 team 裡／放出中／結束」等狀態與座位計算；team 結束時遠端 member 的紀錄維持原狀（與本機 member 一致）。目前還不會產生遠端 member。

### Changed：team 面板可浮動、可縮放 — WA-2a（#2275，介面線）

- team 面板區可以浮動、拖曳調整大小、放大；為之後與工作簿共用面板做準備。

### Added：工作簿設定與整理規則 — WB-1b.1／1b.2（#2279、#2280，介面線）

- 主機設定多了工作簿的「推播最多等幾秒」（預設 8 秒，0–30）；daemon 內建量測過的整理提示詞與回覆檢查。還沒有開始整理任何紀錄。

### Docs

- team 介面第二輪調整說明（#2281）。

## [1.0.0-alpha.662] - 2026-10-10

> 只動 daemon：**要部署 daemon**；mod 沒有改，不必重跑 `pdx setup`；SPA、Electron 不必更新。會新建 `workbook.db`，team.db 多一張表；使用上沒有可見變化。

### Added：跨主機 team：lead 那台開始會送指令 — X3a-2（#2270，A 線）

- lead 那台的指令佇列有了送出的程式：每台目標主機一條先進先出的佇列，失敗會從 30 秒起倍增重試（最長 10 分鐘），對方 10 分鐘都不認得我方 token 就視為對方已解除配對。目前佇列裡不會有指令（對方主機還沒宣告支援），所以不會真的送出任何東西。

### Added：跨主機 team：被收進 team 的那台會回報「session 不在了」 — X2c-1a（#2272，A 線）

- 被別台收進 team 的 session 結束時，這台會記下並排一則「已結束」的回報給 lead 那台（實際送出在下一步）；lead 那台被解除配對時，這台的相關 member 會在本機結束。

### Added：工作簿模組與資料庫 — WB-1a-ii（#2273、#2274，介面線）

- daemon 多了 workbook 模組與 `workbook.db`（權限 0600），以及去除 token、金鑰等秘密的共用過濾、讀取對話最近幾輪的介面。還沒有開始整理任何紀錄（下一步才接上）。

## [1.0.0-alpha.661] - 2026-10-09

> 動到 daemon 與 SPA：**要部署 daemon**；mod 沒有改，不必重跑 `pdx setup`；SPA 已隨主機上的 dev server 生效。team.db 會自動加幾張跨主機用的表與 `team_members` 的兩個欄位，既有資料不變。

### Added：跨主機 team：被收進 team 的那台能收指令 — X2b（#2263、#2267、#2268，A 線）

- 新路由 `POST /api/peers/team/commands`：只接受已配對、已驗證的主機。能處理「收進 team／放出／結束／換 lead／取消」；同一則指令重送不會做兩次，被拒絕的也會留紀錄；取消一則已執行的「收進 team」會把 session 放回來。
- 目前還沒有主機會送這些指令（這台尚未宣告支援任何指令），使用上沒有變化。
- 主機間的身分驗證抽成一份共用程式，訊息投遞與這條路由用同一套規則。

### Added：跨主機 team：lead 那台的指令佇列 — X3a-1（#2266，A 線）

- lead 這邊要送給別台的指令，會跟起因在同一筆資料庫交易裡排進佇列；「收進 team」10 分鐘沒完成就作廢並釋放座位，解除配對時一併收尾。實際送出（佇列的 pump）在下一版。

### Added：工作簿的 daemon 地基 — WB-1a-i（#2269，介面線）

- team 模組提供「接力鏈最早那個 session」與「這個 session 現在是哪個 team 的什麼角色」兩個查詢；一輪出錯結束時也會發出事件，給之後的工作簿使用。現有行為不變。

### Changed：左側列的 team 外觀 — TI-3（#2259，介面線）

- 左側 session 列表裡同一個 team 的項目用珠串與樹狀線連起來，可以收合；lead 不在列表時會顯示淡色的 lead 列；可以拖曳成員珠。

### Docs

- 工作簿 plan（#2260）、介面用語頁：chat 是比較精簡的對話模式（#2264）。

## [1.0.0-alpha.660] - 2026-10-09

> 只動 daemon：**要部署 daemon**；mod 沒有改，不必重跑 `pdx setup`；SPA、Electron 不必更新。

### Changed：核准推播可以直接打開對應的 session — #2256（介面線）

- 核准類推播現在帶上該 session 的代碼與「目前還在等哪幾張核准」的清單，讓 iOS 點推播時能直接打開那個 session，並在核准被別處處理掉時更新通知（iOS 端隨之後的版本）。
- daemon 讀 tmux session 清單時多讀一個「建立時間」欄位；沒有 UTF-8 語系時的防護與以前相同。

### Added：跨主機 team：被收進 team 的那台記得身分 — X2a（#2257，A 線）

- 被別台主機收進 team 的 session 會被記下來，所有「一個 session 只能有一種身分」的檢查都會算進去（例如它不能當 lead、不能被本機再收一次、自己接力會關閉）。目前還沒有路由能建立這種紀錄，使用上沒有變化。

## [1.0.0-alpha.659] - 2026-10-09

> 改了 mod 與 pdx-team skill：**要部署 daemon，並重跑 `pdx setup`**；SPA、Electron 不必更新。

### Added：member 被自動壓縮時通知 lead — P7-2（#2258，A 線）

- 同一台主機上的 member 被 Claude Code 自動壓縮對話時，lead 會收到 `[pdx team] <ref> 已自動壓縮（lead 未在 70% 時接力）`，表示錯過了在 70% 幫它接力的時機。手動 `/compact` 不通知。
- 收到這則之後，70% 的提醒會先停用，等接力完成或用量降下來才恢復，避免拿壓縮前的舊讀數再提醒一次。
- pdx-team skill 裡兩則通知的文字改成與實際送出的一字不差。

## [1.0.0-alpha.658] - 2026-10-09

> 動到 daemon、`pdx` 指令與 SPA：**要部署 daemon**；mod 沒有改，不必重跑 `pdx setup`；SPA 已隨主機上的 dev server 生效。

### Added：member 用到 70% 且閒置時通知 lead — P7-1（#2253，A 線）

- 同一台主機上的 member，context 用到 70% 而且停下來時，lead 會收到一則通知：`[pdx team] member <地址> [<ref>]「<名稱>」已用 N%，目前閒置。要接力請執行：pdx relay _<ref>`。要不要接力由 lead 決定，daemon 不會自己動手。
- 每個 member 只通知一次；接力完成或用量降回門檻以下才會再提醒。正在接力的 member 不會被提醒。

### Added：允許別台主機把這台的 session 收進 team — X1b（#2252，A 線）

- 新指令 `pdx peers host allow-team <別名> on|off [--root <目錄>]…`：決定某台主機能不能把這台的 session 收進它的 team，以及它能在哪些目錄開 member（沒設目錄就不能開）。主機設定頁之後也會有這個開關。
- 只有驗證過身分的主機才能打開；目前跨主機 team 還沒有任何動作能用，這只是先把同意的設定存起來。

### Changed：上方分頁的 team 群組外觀 — TI-2b（#2250，介面線）

- 上方分頁列裡，同一個 team 的分頁會用標籤膠囊、陰影與底色框成一組，可以收合，也能在組內拖曳排序。

## [1.0.0-alpha.657] - 2026-10-09

> 動到 daemon 與 SPA：**要部署 daemon**；mod 沒有改，不必重跑 `pdx setup`；SPA 已隨主機上的 dev server 生效。

### Fixed：配對後手機卡在「連線中」 — #2249（介面線）

- 第一次真機 QR 配對後，iPhone 一直停在「連線中」：App 連上時要讀設定檔清單，但配對手機沒有這個權限。現在配對手機可以列出設定檔，但只看得到它綁定的那一份。

### Added：重啟 daemon 前看得到進行中的接力 — P6-8（#2248，A 線）

- 重啟 daemon 的確認框，除了「N 個申請等待核准」，現在也會顯示「N 個接力進行中（重啟後會接續）」；兩者都有時合成一行。

### Changed：team 面板認得每個位置屬於哪台主機 — TI-2a（#2240，介面線）

- team 面板的每個 member 位置記得自己在哪台主機上，為之後收進其他主機的 session 做準備；目前看起來沒有變化。

### Added：跨主機 team 的連線基礎 — X1a（#2247，A 線）

- daemon 之間呼叫時一律依主機身分定址（主機別名被刪掉重建也不會送錯台）、不跟轉址、有時限與大小上限，並把對方的回應分成「完成／暫時失敗／對方拒絕／對方版本太舊」等類別。這是跨主機 team 的地基，目前沒有新功能。

### Docs：member 接力驗收結果 — #2246（A 線）

- 記錄 P6-6 真機驗收：閒置與忙碌中的接力都成功；實測發現 Claude Code 不會等 turn.start 的 hook 完成，因此鎖改在送出指示前上（656）。

## [1.0.0-alpha.656] - 2026-10-09

> 改了 mod：**要部署 daemon，並重跑 `pdx setup`**；SPA、Electron 不必更新。

### Fixed：寫交接檔時的鎖真的會先鎖上 — #2241（A 線）

- 實測發現：寫交接檔那一輪原本要先鎖住其他工具，但 Claude Code 不會等上鎖完成才開始動作，所以鎖常常晚到、等於沒鎖。現在改成「先上鎖，鎖好了才送出寫交接檔的指示」。
- 如果那時你剛好送出一則訊息、正在跑，接力會等那一輪做完才上鎖，不會擋到你的工作。

## [1.0.0-alpha.655] - 2026-10-09

> 動到 daemon 與 SPA：**要部署 daemon**；mod 沒有改，不必重跑 `pdx setup`；SPA 已隨主機上的 dev server 生效。

### Added：Mac App 的「配對手機」— QP-3（#2237，介面線）

- 主機頁新增「配對手機」：產生配對碼讓手機掃描配對，另有「已配對的手機」清單可以撤銷。

### Changed：推播帶上待核准數 — #2239（介面線）

- 每則推播都帶上「目前有幾張核准卡在等你」，並讓 iOS 的通知擴充程式能在 App 沒開時更新 App 圖示上的數字（iOS 端隨之後的版本）；讀不到就不帶，舊版 App 會忽略。

### Docs：session 工作簿 spec（#2242、#2243）

- 工作簿的規格與 Haiku 提示詞定稿：每輪結束後在背景整理一句推播與一筆工作簿紀錄；Mac App 的 team 面板與工作簿共用一塊可縮放的面板，點 member 可看它的工作簿；iOS 可從推播直接打開那一筆。實作由介面線進行。

## [1.0.0-alpha.654] - 2026-10-09

> 只動 daemon：**要部署 daemon**；mod 沒有改，不必重跑 `pdx setup`；SPA、Electron 不必更新。

### Changed：配對手機可以調整接力額度與 member 上限 — QP-1b-iii（#2230，介面線）

- 依你 20:2x 的決定：配對手機（裝置 token）可以修改 team 的接力額度與 member 上限，跟在 Mac App 上一樣。手機仍看不到 team 名單。目前還沒有手機使用裝置 token（要等之後的 QP-2／QP-3）。

### Added：手機配對碼 — QP-2（#2234，介面線）

- daemon 可以產生一次性的配對碼（有效 1～10 分鐘），手機用它向 daemon 領取配對；領取這一步不需要 token，但只接受 tailnet 或本機來的連線，且每個來源每分鐘最多試 10 次。同時最多 16 組有效配對碼。
- 這是 QR 配對流程的 daemon 端；手機端（QP-3）上線前，使用上沒有可見變化。

## [1.0.0-alpha.653] - 2026-10-09

> 只動 daemon：**要部署 daemon**；mod 沒有改，不必重跑 `pdx setup`；SPA、Electron 不必更新。對現在的使用沒有可見變化（配對手機的流程還沒上線）。

### Added：配對手機只能「新增」分頁 — QP-1c（#2229，介面線）

- 配對手機（裝置 token）可以在自己那份設定檔的工作區分頁裡追加新分頁；既有分頁不能刪改，格式與雜湊驗證不過就拒絕。
- daemon 有了跟 App 一致的設定檔雜湊計算，之後配對流程用來確認兩邊內容相同。目前還沒有手機使用裝置 token（要等之後的 QP-2／QP-3）。

## [1.0.0-alpha.652] - 2026-10-09

> 改了 mod：**要部署 daemon，並重跑 `pdx setup`**；SPA、Electron 不必更新。部署後，lead 可以幫 member 接力了。

### Added：member 端的接力 — P6-6（#2224，A 線）

- lead 用 `pdx relay <member>` 幫 member 接力時，member 的 mod 會自己收下 lead 的通知（member 的對話看不到這則訊息）。member 正在做事的話，等這一輪做完才開始接力，不會打斷。
- 接力的步驟跟自己接力一樣：寫交接檔 → 檢查 → 清空對話 → 新對話讀交接檔接著做；lead 會收到完成或失敗的通知。member 的舊地址照樣送得到。
- 交接檔的「協作關係」一節會自動帶入：member 寫上自己的 lead 與 team；lead 寫上自己管理的 members。
- 其他 peer 冒用接力通知沒有作用：只有 daemon 確認屬於這個 member 的接力才會執行。

## [1.0.0-alpha.651] - 2026-10-09

> 只動 daemon：**要部署 daemon**；mod 沒有改，不必重跑 `pdx setup`；SPA、Electron 不必更新。推播資料庫會自動加一個欄位，既有的手機推播登記不受影響。

### Changed：配對手機的權限與推播歸屬 — QP-1b-ii（#2225，介面線）

- 配對手機（裝置 token）改成「只能用明列允許的功能」，取代先前的暫時限制。
- 手機的推播登記會記住是哪支配對手機登記的；撤銷那支手機時，它的推播登記一起刪掉，之後不會再推到它。用管理者 token 登記的（現在你的手機就是）不受影響。
- 設定檔（profile）只能讀寫該手機自己的那一份。目前還沒有手機使用裝置 token（要等之後的配對流程 QP-2／QP-3）。

## [1.0.0-alpha.650] - 2026-10-09

> 動到 daemon、`pdx` 指令與內建的 pdx-team skill：**要部署 daemon，並重跑 `pdx setup`**（skill 更新）；mod 沒有改；SPA、Electron 不必更新。

### Added：lead 幫 member 接力的指令 — P6-5（#2222，A 線）

- 新指令 `pdx relay <member> [--wait 9m]`：lead 決定要幫哪個 member 接力時用它。member 可以用 ref、地址或名字指定；同一個要求重送不會開出第二次接力。
- 加 `--wait` 會等到接力有結果。結束碼分別代表：完成、被拒絕、逾時（或還在等你核准）、取消（或只是等待被中斷，接力其實還在進行）、被拒收（不是 lead、不是自己的 member、對方沒裝 Purdex mod）、member 沒回應或已不在。
- 無人值守且 member 接力額度用完時，指令會顯示「等待核准：member 額度用完（無人值守）」；核准一律由你決定，lead 不能自己核准。
- pdx-team skill 改回「member context 高時由 lead 用 `pdx relay` 決定接力」。member 端的 mod 還沒支援（下一版 P6-6），在那之前這個指令會回「對方沒有 Purdex mod」。

### Changed：裝置連線可以即時撤銷 — QP-1b-i（#2221，介面線）

- 裝置換取的一次性連線票，現在記得是誰申請的；兌換時會重新確認裝置還有效，已撤銷的裝置直接拒絕。
- 裝置開著的所有即時連線（終端機、事件、對話等）統一登記；撤銷裝置時，它的連線會立刻被關掉。管理者自己的連線不受影響。目前還沒有 App 使用裝置 token（之後的 QP-3 才會）。

## [1.0.0-alpha.649] - 2026-10-09

> 只動 daemon：**要部署 daemon**；mod 沒有改，不必重跑 `pdx setup`；SPA、Electron 不必更新。接力卡住太久時 daemon 會自己收尾；daemon 多了裝置 token 的地基。

### Added：接力逾時 — P6-4b（#2218，A 線）

- lead 幫 member 接力時，member 60 秒內沒回應就算失敗（「member 沒有回應」）；member 回應了但 15 分鐘內沒開始，也算失敗。計時從接力真正開始的那一刻算起，等你核准的時間不算。
- 接力開始後 15 分鐘都沒有進展，daemon 先看終端機面板確認，還是沒動靜才記成失敗。
- 接力其實已完成（新 session 已在跑），只是新 session 的第一輪做很久時，記成「完成」，不會誤判成失敗。
- daemon 剛重啟的頭一段時間不判逾時。

### Added：裝置 token 模組 — QP-1a（#2216，介面線）

- daemon 新增裝置管理（`devices.db`）：管理者可以替裝置建立專用 token。目前裝置 token 只能讀主機資訊、更新自己的裝置資料；還沒有任何 App 會使用它（之後的 QP-3 才會）。

## [1.0.0-alpha.648] - 2026-10-09

> 只動 daemon：**要部署 daemon**；mod 沒有改，不必重跑 `pdx setup`；SPA、Electron 不必更新。接力做到一半時 daemon 重啟或漏了回報，現在 daemon 會自己從終端機狀態補判。

### Fixed：接力的回報漏掉時，daemon 自己對帳 — P6-4a（#2215，A 線，#1735）

- 接力已經開始（已認領、寫交接檔中或已寫好）時，如果 session 其實已經清空對話換成新的、卻沒回報給 daemon，daemon 會從那個終端機面板的狀態看出來，並代為記成「已接力」，後續（標題、lead 角色移轉等）照常完成。
- 那個 session 已經不在、面板也沒有任何 agent 時，記成「member 已消失」並結束這次接力。
- daemon 剛重啟的頭一段時間（session 可能還沒重新登記）不會下「已消失」的判斷，等寬限期過了再對帳一次，避免誤判。
- lead 幫 member 接力時，接力完成、失敗或取消都會通知 lead（member 接力要等 P6-6 才會實際發生）。

## [1.0.0-alpha.647] - 2026-10-09

> 動 mod：**要部署 daemon，並重跑 `pdx setup --agent cc`**（mod 改了）；SPA、Electron 不必更新。**接力寫交接檔的那一輪，Claude 只能做「寫交接檔」這一件事。**

### Changed：寫交接檔那一輪鎖住其他工具 — P6-3c（#2213，A 線）

- 你核准接力後，mod 在寫交接檔那一輪開始時上鎖：這一輪 Claude 只能用一次 Write 寫出交接檔，其他工具（讀檔、跑指令、Edit 等）都會被擋下，避免它在 context 快滿時還去做別的事。
- 交接檔寫完、清空對話之前就解鎖；寫入失敗、這一輪被中斷、session 重新開始時也會解鎖。萬一 mod 自己來不及解鎖，daemon 在接力完成或失敗時也會把鎖拿掉。
- 上鎖失敗時只記一行 log，接力照常進行（不會因為鎖而卡住）。

## [1.0.0-alpha.646] - 2026-10-09

> 只動 daemon（與 SPA）：**要部署 daemon**；mod 沒有改，不必重跑 `pdx setup`；SPA 已隨主 checkout 快轉生效（Mac App 按 Cmd+R）；Electron 不必更新。「lead 幫 member 接力時扣 member 額度」的規則上線，但要等 member 端的 mod 更新（P6-6）才會實際用到。

### Added：lead 幫 member 接力時扣 member 額度 — #2062 RQ-2b（#2202，A 線）

- 無人值守開著、而且額度規則開著時，lead 每幫一個 member 接力，就從 lead 的「member 額度」扣 1；額度用完時，這次接力會掛著等你在 App 核准。
- 你在場（無人值守關著）或額度規則關著時，lead 幫 member 接力不需要核准、也不扣額度，和原本的設計一樣。
- 等你核准的這種接力會列在無人值守面板「額度用完，等你核准」的清單裡，手機也會收到推播。
- 目前 member 端的 mod 還不支援被接力（P6-6），所以 lead 發起的接力一律回「member 的 mod 版本不支援」——**現在沒有任何行為改變**。

### Added：App 的 member 接力核准卡片與推播 — #2207，介面線

- App 認得新的「member 額度用完」核准卡片：寫明哪個 lead 要幫哪個 member 接力、member 的 context 用了多少，可以核准或拒絕；手機推播也會送這種核准。

### Added：接力寫交接檔時的工具鎖（判斷部分）— P6-3b-2（#2208，A 線）

- daemon 與 `pdx hook` 學會「接力鎖」：鎖住時只放行寫交接檔的那一次 Write，其他工具一律擋下。新增 `pdx relay lock|unlock` 指令。
- 鎖由 mod 在寫交接檔那一輪舉起（下一版 P6-3c），**這一版還不會有 session 被鎖**，行為不變。

## [1.0.0-alpha.645] - 2026-10-09

> 動 daemon 與 mod：**要部署 daemon，並重跑 `pdx setup --agent cc`**（mod 改了）；SPA、Electron 不必更新。在手機上遇到 Claude 的選擇題時，可以不選選項、直接回一段話給它。

### Added：在手機上用一段話回覆 AskUserQuestion（ask chat）— #2205，介面線

- Claude 用 AskUserQuestion 問你時，手機的 ask 小卡多了「改成跟 agent 聊聊」：你打的話會直接送回給 Claude，當作你的回覆（等同原生的 Chat about this），電腦上那個原生對話框也會自動關掉。
- 原本的選選項回覆、在電腦上直接回答、推播都不變。
- 舊版 mod 遇到新版 daemon 時會退回原本的對話框，不會出錯。

### Changed：接力用的鎖檔工具搬到共用位置 — P6-3b-1（#2206，A 線）

- 純搬移，行為不變；為之後「接力寫交接檔時鎖住其他工具」做準備。

## [1.0.0-alpha.644] - 2026-10-09

> 動 mod、daemon 與 pdx-team skill：**要部署 daemon，並重跑 `pdx setup --agent cc`**（mod 與 skill 改了）；SPA、Electron 不必更新。接力時，交接檔裡的 git 狀態改由 mod 自動抓好給 Claude 照抄。

### Changed：接力交接檔自動帶入 git 狀態 — P6-3a（#2203，A 線）

- 寫交接檔時，mod 會先在那個 session 的資料夾跑 `git status --short`、`git diff --stat`、`git log --oneline -10`，把結果放進寫作指示裡，Claude 照抄進交接檔的「檔案異動」一節，不必自己再跑一次。
- 每個指令最多等 10 秒，輸出最多 200 行；不是 git 資料夾或指令失敗時只寫一行原因，不會讓接力失敗。
- 檔名或 commit 訊息裡的控制字元、看不見的方向字元會先清掉，不會混進指示裡。
- 交接檔要求一次寫完整份（不要分段修改），修正時也是整份重寫。
- 你在 App 自訂過寫作指示的主機，保留你的版本（設定頁會標「已自訂」）。

### Note：alpha.643 已一併上線 RQ-2a（#2199，A 線）

- 「lead 幫 member 接力、member 額度用完時等你核准」的核准狀態機已在 daemon 裡，但目前沒有任何路徑會開出這種核准，行為沒有變化；等介面線的核准卡片上線後才會啟用。

## [1.0.0-alpha.643] - 2026-10-09

> 動 daemon、CLI 與 pdx-team skill：**要部署 daemon，並重跑 `pdx setup --agent cc`**（skill 改了）；SPA、Electron 不必更新。alpha.642 沒有單獨部署，它的內容（手機推播等）隨這一版一起上線。team 核准後也能調整人數上限了（App 的調整按鈕由介面線接著做）。

### Added：核准後調整 team 人數上限（#2195，A 線）

- daemon 新增一條只給 App 用的路由，可以把一個 team 的人數上限調成 1–8 人，但不能低於目前已經在用的人數（在職的 member 加上還在啟動中的）。無人值守核准時的上限 3 只管核准那一刻，之後你在 App 可以調到 8。
- team 清單多了「上限」與「使用中」兩個數字；`pdx team` 的標頭會顯示 `members 使用中/上限`。
- agent 沒有任何指令能改上限，skill 也寫明上限是你的。
- 修正一個舊問題：新 member 剛完成註冊的那一瞬間，同一席會被算成兩席，可能讓新增 member 被誤擋。

## [1.0.0-alpha.642] - 2026-10-09

> 動 daemon 與 SPA：**要部署 daemon**；mod 沒有改，不必重跑 `pdx setup`；SPA 已隨主 checkout 快轉生效（Mac App 按 Cmd+R）；Electron 不必更新。**手機開始會收到 agent 的推播**：你不在 Mac 前時，Claude 停下來等你、做完或出錯，會推到已登記的 iPhone。

### Added：agent 事件推播到手機 — 推播 PU-3b2（#2187）＋PU-4（#2193），介面線

- daemon 收到 agent 的 hook 事件（等你回覆、做完、出錯）時，依和 Mac 相同的規則決定要不要推到手機：同一個事件只推一次、靜音的通知不推、出錯在 60 秒內只推一次、只推手機上設定過的分頁。
- **Mac 有視窗正顯示那個 session、而且你在用 Mac 時，就不推到手機**：Mac App 會每 20 秒回報一次你正在看哪些 session。
- 已經用 AskUserQuestion 推過的問題，同一段等待不會再推一次。

### Added：推播在場狀態讀數（#2196，介面線）

- `/api/info` 的 push 區段多兩個數字：目前回報中的 Mac 視窗數、其中你正在使用的視窗數。只有數字，不含任何名稱。

### Added：對話 API 補欄位 — U1-6g（#2192，介面線）

- 對話 API 的每一輪多了耗時（毫秒），系統訊息多了「mod 通知」這一類。只加不改，舊的 App 不受影響。

### Added：lead 幫 member 接力的路由與認領 — P6-2b-1（#2191）＋P6-2b-2（#2194），A 線

- daemon 新增「lead 要求 member 接力」的路由、寄給 member 的控制訊息、member 認領接力的路由與等待結果的長輪詢。
- 這些路徑要等 member 端的 mod 更新（P6-6）才會真的被用到；在那之前，要求一律回「member 的 mod 版本不支援」，**現在沒有任何行為改變**。
- 開機時會替接力紀錄表再加兩個欄位（行程啟動時間、已讀時間），舊資料不受影響。

## [1.0.0-alpha.641] - 2026-10-09

> 只動 daemon：**要部署 daemon**；mod 沒有改，不必重跑 `pdx setup`；SPA、Electron 不必更新。這一版沒有你看得到的變化，是「lead 幫 member 接力」的地基。

### Added：member 接力的資料準備 — P6-2a（#2188，A 線）

- daemon 會把每個 session 載入的 Purdex mod 版本記進資料庫，重啟後仍然記得（之前只放在記憶體裡）。之後 lead 幫 member 接力前，會用它確認 member 的 mod 夠新。
- 接力紀錄多記 member 的行程編號與 tmux pane，之後用來確認接力完成的是同一個 member。
- 開機時會替既有的接力紀錄表加兩個欄位、建一張新表，舊資料不受影響。

## [1.0.0-alpha.640] - 2026-10-09

> 動 daemon 與 pdx-team skill：**要部署 daemon，並重跑 `pdx setup --agent cc`**（skill 改了）；SPA、Electron 不必更新。lead 接力後，它的 member 會收到新 lead 的地址。

### Added：lead 接力後通知 member — P6-1′（#2185，A 線）

- lead 接力完成時，daemon 會從**新 lead** 寄一則訊息給每個還在職的 member：`[pdx team] 你的 lead 已換手：<新地址> [<ref>]（舊 ref 仍可用）`。member 回覆會直接送到新的 lead。
- 只寄一次；通知寄送失敗只記 log，不影響接力本身。原本的舊 ref 照樣送得到，所以漏收通知也不會斷線。

### Changed：pdx-team skill 不再叫 lead 跑還不存在的指令

- skill 原本叫 lead 在 member context 偏高時跑 `pdx relay <ref>`，但那個指令還沒做（P6-5 才有）。現在改成：在 `pdx team` 看到 member 的 CTX 偏高時，先告訴使用者。

### Added：推播的 Mac 在場回報與事件閘門 — 推播 PU-3b1（#2184，介面線）

- daemon 新增一條路由，讓 Mac App 回報「使用者正在 Mac 前」；之後的推播會依這個判斷要不要送到手機，同一個 session 短時間內的重複事件也只送一次。
- 這一版還沒接上 agent 的事件，所以不會送出任何新推播。

## [1.0.0-alpha.639] - 2026-10-09

> 只動 daemon：**要部署 daemon**；mod 沒有改，不必重跑 `pdx setup`；SPA、Electron 不必更新。這一版沒有你看得到的變化，是之後「推播通知」的地基。

### Added：daemon 內部的 hook 事件分發點 — 推播 PU-3a（#2180，介面線）

- daemon 收到 tmux 裡 agent 的每一個 hook 事件時，會多送一份給 daemon 內部的訂閱者，之後的推播功能會從這裡接收「哪個 session 發生了什麼」。
- 這一版還沒有任何訂閱者，所以燈號、狀態與 log 的行為都和上一版一樣。
- 訂閱者處理太慢時只會丟掉它自己的事件並記一筆 log，不會拖慢燈號更新。

## [1.0.0-alpha.638] - 2026-10-09

> 動 daemon 與 SPA：**要部署 daemon**（log 輪替重啟後生效）；mod 沒有改，不必重跑 `pdx setup`；SPA 已隨主 checkout 快轉生效（Mac App 按 Cmd+R）；Electron 不必更新。這一版可以在 App 設定每個 session 的自動接力額度，daemon 的 log 也會自動輪替。

### Added：在 App 設定自動接力額度 — #2062 RQ-A（#2176，介面線）

- 標題列無人值守開關旁的面板列出這台主機執行中的 session，每個 session 都能調整「無人值守時還能自動接力幾次」；lead 另有一個 member 額度池可以調整。
- 因為額度用完而掛著、等你核准的接力申請，會列在同一個面板裡。
- 多個視窗同時調整時以最新的一次為準。

### Changed：額度規則已啟用（2026-10-09 16:56）

- 依使用者決定，主機的額度規則開關已打開：**無人值守時，只有給了額度的 session 會自動接力**，每次扣 1；額度 0 的接力申請會掛著等你核准。你自己按核准不扣額度；無人值守關著時行為不變。要退回原本的全自動，把開關關掉即可。

### Changed：daemon 的 log 自動輪替（#2179，#2163）

- `~/.config/pdx/logs/pdx.log` 超過 50 MiB 時自動改名並壓縮成 `pdx.log.1.gz`，保留最近 5 份（先前曾長到 621 MB）。
- 輪替時不會漏寫或寫到已改名的檔；壓縮失敗不會刪掉既有的舊 log。

## [1.0.0-alpha.637] - 2026-10-09

> 動 daemon、CLI 與 pdx-team skill：**要部署 daemon，並重跑 `pdx setup --agent cc`**；SPA、Electron 不必更新。每個 session 的自動接力額度（#2062）的 daemon 端全部完成，但**規則的開關預設是關的**：在 App 能設定額度之前，無人值守的行為和以前完全一樣。

### Added：無人值守時依額度自動接力（開關預設關）— #2062 RQ-1b（#2171）

- 新的主機設定開關 `relay_quota`（預設關）。開著時：無人值守自動核准接力會扣這個 session 所在接力鏈的額度 1 次；額度是 0 的申請就掛著等你核准，你在 App 補額度後一秒內自動核准。你自己按核准不扣額度。
- 開關關著時：照舊全部自動核准、不扣額度（額度只記錄、只顯示）。
- 無人值守的資料多一份「因額度用完而掛著」的清單，給 App 顯示。
- 開關是獨立的設定項，舊版 App 存接力設定時不會把它改掉。

### Added：`pdx team` 顯示 lead 的額度 — #2062 RQ-1c（#2177）

- `pdx team` 的名稱下方顯示「relay quota: 自己 N 次 · member 池 M 次」，只能看不能改；讀不到時不顯示，不會顯示錯的 0。
- pdx-team skill 加一條：額度與開關都是使用者在 App 設定的，agent 沒有指令可改、不得去改、也不得要求加額度；額度用完的接力就等使用者。

## [1.0.0-alpha.636] - 2026-10-09

> 只動 daemon：**要部署 daemon**；mod 沒有改，不必重跑 `pdx setup`；SPA、Electron 不必更新。接力額度（#2062）的資料讓 App 能可靠地顯示；額度仍然**不會扣**，無人值守的行為不變。

### Changed：接力額度的資料可以分辨新舊與讀取失敗 — #2062 RQ-1a2（#2174）

- 每份額度多一個版本號，每次修改加 1；App 收到多筆更新時只採用較新的那份，兩個視窗同時修改也不會顯示錯。
- 無人值守資料裡的額度清單：讀取失敗時明確回報「讀不到」，成功但沒有 session 時回報「空清單」，App 不會把失敗當成沒有額度。
- team.db 會自動補上版本欄位，不必手動遷移。

## [1.0.0-alpha.635] - 2026-10-09

> 只動 daemon：**要部署 daemon**；mod 沒有改，不必重跑 `pdx setup`；SPA、Electron 不必更新。這一版讓等人核准的申請可以推播到手機；iOS App 登記裝置後才會真的收到。

### Added：等人核准時推播到手機 — 推播 PU-2（#2166，介面線）

- 有申請在等人核准時（lead 申請、接力、收編等），daemon 會透過 Apple 推播服務（APNs）通知已登記的 iPhone。
- 推播失敗依原因處理：裝置已失效就移除登記；憑證過期會重新簽發；暫時性錯誤稍後重試。
- 目前還沒有裝置登記（iOS 端的登記之後才開放），所以這一版不會實際送出任何推播。agent 事件與在線狀態的推播之後才會加上（PU-3／PU-4）。

## [1.0.0-alpha.634] - 2026-10-09

> 只動 daemon：**要部署 daemon**；mod 沒有改，不必重跑 `pdx setup`；SPA、Electron 不必更新。這一版為「每個 session 的自動接力額度」（#2062）打底：額度可以存、可以設定、會顯示，但**還不會扣**，無人值守的行為完全不變。

### Fixed：「無人值守自動核准」不能再被冒充 — #2062 RQ-0（#2165）

- 核准 API 原本接受任何呼叫者自稱是「無人值守」，紀錄上就分不出是 daemon 自動核准還是有人冒用。現在只有 daemon 內部的自動核准才會記成無人值守，外部請求自稱無人值守會被拒絕。
- 「無人值守期間自動通過」清單不再列出早先被冒用的紀錄。

### Added：每個 session 的自動接力額度（尚未生效）— #2062 RQ-1a（#2169）

- 新的額度紀錄跟著整條接力鏈走：同一條鏈上的每個對話讀到的是同一份額度。
- App 可以透過新的設定路由給額度（0–99），變更會即時推送；成員清單、`pdx team` 與無人值守的資料都帶上額度欄位，App 的顯示之後由介面線補上。
- 扣額度的規則還沒有加上，所以這一版不會改變任何接力行為。

## [1.0.0-alpha.633] - 2026-10-09

> 動 daemon、CLI（hook）與 SPA：**要部署**（`bin/pdx` 換新，hook 呼叫的就是它）；mod 沒有改，不必重跑 `pdx setup`；SPA 已隨主 checkout 快轉生效；Electron 不必更新。

### Fixed：PATH 裡沒有 tmux 的 pane 也能註冊 session（#2158，#2123，介面線）

- 從 iOS 以非互動 ssh 開的 session，pane 的 PATH 只有 `/usr/bin:/bin`，找不到 Homebrew 的 tmux，`pdx hook` 因此無法辨識自己在哪個 tmux session，這個 session 就不會出現在 Purdex。現在 hook 會改用已知的 tmux 路徑。

### Fixed：浮動面板不再整圈畫出焦點框（#2162，#2063）

- 無人值守面板等浮動面板裡沒有可聚焦的按鈕時，整個面板外圍會出現一圈橘色框；現在不會了。面板內的按鈕照常有自己的焦點樣式，高對比模式下仍看得到焦點。

### 其他

- 每個 session 的自動接力額度（#2062）的 spec 與 plan（#2164，文件）。

## [1.0.0-alpha.632] - 2026-10-09

> 只動 daemon：**要部署 daemon**（與 alpha.631 一起部署時照 631 的說明重跑 `pdx setup`）；SPA、Electron 不必更新。這一版修正 daemon 每次重啟就多留一個殭屍子行程的問題。

### Fixed：daemon 重啟不再留下殭屍子行程（#2159，#2137）

- daemon 每次原地重啟都會留下一個已結束卻沒被回收的子行程（mlab 上累積到 68 個，正好對上 70 次重啟），長期下去會用光使用者的行程名額。
- 原因是 daemon 常駐的 tmux 事件監聽子行程在關機時被結束，但 daemon 沒等它收尾就換成新程式。現在關機時會先等它結束（最多 2 秒）。
- 新程式開機時，會先收掉舊程式留下的已結束子行程；還在執行的則持續監看，結束時再收掉。log 會記錄收了幾個。

## [1.0.0-alpha.631] - 2026-10-09

> 動 mod：**要部署 daemon，並重跑 `pdx setup --agent cc`**；SPA、Electron 不必更新。這一版完成「把執行中的 session 收進 team」（U24）的最後一塊：被放出 team 的 session 會重新開始自動接力。U24 第一批（收編、釋放、結束 member）已在 alpha.630 驗收通過，並於當時重跑 `pdx setup` 讓 skill 生效。

### Changed：被放出 team 的 session 會恢復門檻接力 — U24 PL-1g（#2157）

- mod 先前記得「這個 session 是 member」就不再自己申請接力；member 被釋放或 team 結束後，這個記憶不會更新，session 就再也不會在門檻時接力。
- 現在 context 超過門檻時，mod 每分鐘最多向 daemon 重新確認一次角色；確認已不是 member，就照常申請接力。
- 門檻以下不做任何確認；`/relay now` 與 `/lead` 本來就以 daemon 當下的回答為準，不受影響。

## [1.0.0-alpha.630] - 2026-10-09

> 只動 daemon 與 CLI：**要部署 daemon**；這一版**刻意不跑 `pdx setup`**（skill 的收編說明等 U24 第一批全部完成、驗收時再一起裝），SPA、Electron 不必更新。這一版大幅降低 daemon 的 CPU 用量，並加上釋放 member、收編與釋放的 CLI。

### Changed：daemon 的掃描不再一直呼叫 `ps`（#2153，#2138，介面線）

- daemon 每 2 秒掃一次 session，原本對每個 session、以及往上追的每一層父行程都各呼叫一次 `ps`，session 越多越吃 CPU（mlab 上 daemon 長時間約佔 30–45% 的 CPU）。現在每輪只讀一次行程表，所有查詢都從那一份回答。

### Added：釋放 member、結束被收編的 member — U24 PL-1d2（#2150）

- lead 可以把 member 釋放出 team：它繼續以一般 session 執行，並收到通知。
- 結束一個被收編的 member 時，只停止它的 Claude Code 行程（送出前會再確認行程身分），它的 tmux session 與 shell 都留著；派出的 member 照舊結束它自己的 tmux session。
- 已被釋放的 member 不能再被 `pdx kill`。

### Added：`pdx adopt`、`pdx release` 指令 — U24 PL-1e（#2154）

- `pdx adopt <ref>`：把這台主機上執行中的 session 收進自己的 team，等待 App 核准（無人值守時立即核准）。兩個 session 剛好同一個 ref 時會請你改用 session id。
- `pdx release <ref>`：釋放 member。
- pdx-team skill 的對應說明（包含 lead 自己判斷要結束 member 時，必須先問使用者「釋出／關閉／保留」）會在下一次 `pdx setup` 生效。

## [1.0.0-alpha.629] - 2026-10-09

> 只動 daemon：**要部署 daemon**；mod 沒有改，不必重跑 `pdx setup`；SPA、Electron 不必更新。這一版讓手機推播在 mlab 上就緒，並加上收編與釋放 member 時發給對方的通知（目前還沒有入口會觸發）。

### Fixed：推播金鑰路徑可以寫成 `$HOME/…`、`~/…` 或絕對路徑（#2149，介面線）

- 金鑰設定裡的 `APNS_KEY_FILE` 原本只接受單純檔名，mlab 現有的設定用的是 `$HOME/…` 寫法，因此推播一直顯示未就緒。現在接受 `$HOME/`、`~/` 開頭或絕對路徑，但解析後必須正好是金鑰目錄裡的檔案；`..` 與指向目錄外的連結照樣拒絕。

### Added：收編與釋放的通知 — U24 PL-1d1（#2147）

- 被收編或被釋放的 session 會收到一則來自 lead 的通知；送不到時會重試，10 分鐘後放棄。
- 通知已寫出、只是等不到對方確認時不會重送，避免收到兩次。
- 收編與釋放的 CLI 尚未開放，這一版不會有通知實際送出。

## [1.0.0-alpha.628] - 2026-10-09

> 動 daemon 與 SPA：**要部署 daemon**，並在 mlab 的 `~/.config/pdx/config.toml` 加上 `[push]` 一段（`apns_dir = "~/.config/apns"`）；mod 沒有改，不必重跑 `pdx setup`；SPA 已隨主 checkout 快轉生效；Electron 不必更新。這一版準備好手機推播的裝置登記（還不會送出推播），把「收編執行中的 session 進 team」的 API 接上（CLI 之後才開放），並調整 team 分頁與桌面通知。

### Added：手機推播的裝置登記 — 推播 PU-1（#2141，介面線）

- 新的 `[push]` 設定段（只在 daemon 啟動時讀取，不經 `PUT /api/config`），指向 APNs 金鑰所在目錄。
- 手機 App 可以登記與移除自己的裝置；`/api/info` 會回報推播是否已設定、是否就緒，就緒時才宣告 `push.v1`。
- 金鑰缺漏或損壞時 daemon 照常啟動，只是推播顯示未就緒並說明原因。
- 這一版還不會送出任何推播（PU-2 才會）。

### Added：收編執行中的 session 進 team 的 API — U24 PL-1c0、PL-1c、PL-1c2（#2136、#2139、#2145）

- lead 可以申請把一個執行中的 session 收進自己的 team：申請、核准、無人值守時自動核准都已接上，App 會顯示收編核准卡片（PL-2a，#2132）。
- 用 6 碼 ref 指定對象時，若剛好有兩個對話同一個 ref，會明確回報「有歧義」，請改用 session id。
- 不在 tmux 裡執行的 session 也會記錄每一輪的最後一句，被收編後 lead 一樣看得到（#2115）。
- CLI（`pdx adopt`、`pdx release`）與通知尚未開放，會在之後的 PR 一起上線並驗收。

### Changed：team 分頁的排列與關閉 — TI-1c（#2135，介面線）

- 同一個 team 的分頁（lead 在前、member 依序在後）會排在一起；離開 team 的 member 分頁會排到群組後面。
- 關掉 lead 的分頁時，同一個工作區裡這個 team 的其他分頁會一起關閉。

### Changed：桌面通知只發給有開分頁的 session（#2134，介面線）

- 所有 session 一律「有開分頁才通知」，不再對不在 tmux 裡的 session 例外。

### 其他

- 手機推播的 spec 與 plan（#2133，文件）。

## [1.0.0-alpha.627] - 2026-10-09

> 只動 daemon：**要部署 daemon**；mod 沒有改，不必重跑 `pdx setup`；SPA、Electron 不必更新。這一版讓對話的即時推送帶上燈號變化，並為之後「把執行中的 session 收進 team」打好資料層基礎（目前沒有可見的行為變化）。

### Changed：對話推送帶上即時燈號，任務摘要正確解碼 — 介面語言 U1-6f（#2129，介面線）

- 對話的 WS 連線每次輪詢時會重新讀取這個 session 的燈號；燈號一變，就推送一則對話標頭更新，不用重連。
- 任務通知的摘要裡的 `&amp;`、`&gt;` 等字元改為正確顯示。

### Added：收編與釋放 member 的資料層 — U24 PL-1b（#2128、#2130）

- team.db 自動補上新欄位：member 怎麼加入（派出或收編）、何時離開、欠它什麼通知，以及申請被取消的原因。既有的 member 一律視為派出，行為不變。
- 核准收編時，會在寫鎖內重新檢查所有條件，並以資料庫裡存的申請內容為準。
- `pdx kill` 不會再對已被釋放的 member 動手：那個 session 已經不屬於任何 team，會被拒絕，tmux 完全不會被碰到。
- 收編與釋放的 API、CLI 尚未開放（之後的 PR）。

## [1.0.0-alpha.626] - 2026-10-09

> 動 daemon 與 SPA：**要部署 daemon**；mod 沒有改，不必重跑 `pdx setup`；SPA 已隨主 checkout 快轉生效（Mac App 按 Cmd+R）；Electron 不必更新。這一版修正 daemon 與新 tmux session 帶著別的 Claude Code session 身分的問題，並換上新的狀態列用量顯示。

### Fixed：daemon 與新 tmux session 不再帶著別的 Claude Code session 的身分（#2126，#2122）

- daemon 若是從某個 Claude Code session 裡啟動的，會繼承那個 session 的身分變數（session id、訊息 socket 與 token、pid 等 11 個），之後它開的 tmux session、Nexen worker 也跟著繼承：新 shell 裡的 `pdx msg send` 會被算到別人的信箱，新開的 claude 會以為自己是子 session。
- daemon 啟動時先把這些變數清掉；部署重啟後就是乾淨的。
- daemon 每次開新的 tmux session 時，順帶把 tmux 全域環境裡的這些變數清掉。
- 只清 Claude Code 標示「自己是哪個 session」的變數；`CLAUDE_CONFIG_DIR`、`CLAUDE_CODE_PLUGIN_DIRS` 等設定照常保留。

### Changed：狀態列的用量改成圖示＋圓環＋剩餘百分比（#2117，介面線）

- 拿掉狀態列裡重複的位址格。
- context／5 小時／7 天用量移到右側控制區，各自以圖示加圓環顯示，數字是剩餘百分比；用到 70% 以上轉黃、90% 以上轉紅。worker 的用量列同樣改版。

### Added：team 介面的操作 — TI-1b（#2125，介面線）

- 點 team 成員會打開它的分頁，或切到已開的分頁並把鍵盤焦點交給它所在的窗格；收合中的群組會先展開。
- 群組收合、只在看得到的分頁之間切換、固定分頁的保護。這些操作由之後的 team 介面畫面（TI-2／3／4）呼叫。

### 其他

- U24 plan 與現況對齊（#2124，文件）。

## [1.0.0-alpha.625] - 2026-10-09

> 只動 daemon：**要部署 daemon**；mod 沒有改，不必重跑 `pdx setup`；SPA、Electron 不必更新。這一版讓 team 的成員清單帶上每個 member 手上的任務，App 之後可以直接顯示；派工／回報（T 線）到此全部完成。

### Added：成員清單帶上目前的任務 — 派工／回報 T-3b（#2120）

- `GET /api/team/roster` 與 `team.roster` 事件裡，每個 member 多一個 `task` 欄位：`{id, subject, status}`。優先取進行中的任務，沒有就取最新一個待辦的；都沒有就不帶這個欄位。
- 只放任務主旨，不帶最後一輪或回報的內容。
- 任務建立、開始、完成、刪除、改派，以及 member 回報開始或完成時，成員清單會立刻更新。
- 所有 team 的未完成任務用一次查詢讀出，不會隨 team 數量變慢。

## [1.0.0-alpha.624] - 2026-10-09

> 只動 daemon：**要部署 daemon**；mod 沒有改，不必重跑 `pdx setup`；SPA、Electron 不必更新。這一版讓 lead 在 `pdx team` 的 LAST 欄看到每個 member 最近一輪說了什麼。

### Added：member 的最後一輪 — 派工／回報 T-3a（#2113、#2116）

- daemon 在每輪對話結束時記下 member 說的第一句話（最多 200 字，遇句號就切），寫到它正在做的任務上；沒有進行中的任務就記在 member 本身。
- `pdx team` 的 LAST 欄會顯示最新的一筆：任務上的最後一輪、最後一次回報，或 member 本身的最後一輪，取最新的。
- 接力後的新對話沿用同一個 member 的紀錄，下一輪就會更新。
- 只記 team 裡活躍 member 的回合；lead、獨立 session、已離開的 member 都不記。順序錯亂或重複送達的通知不會蓋掉較新的紀錄。
- 目前只記在 tmux 裡執行的 session；不在 tmux 裡的 session 留待之後補（#2115）。
- team.db 會自動補欄位，不必手動遷移。

## [1.0.0-alpha.623] - 2026-10-09

> 只動 mod 與 pdx-team skill（daemon 行為不變，但 mod 隨執行檔打包）：**要部署並重跑 `pdx setup --agent cc`**；SPA、Electron 不必更新。這一版新增兩個 Claude Code 指令：`/relay now` 立刻接力、`/lead` 立刻申請成為 lead。

### Added：`/relay now` 立刻接力（#2108）

- 只打 `/relay`（不帶參數）等於 `/relay now`：不管 context 用了多少，立刻申請接力，之後的流程與到達門檻時相同（核准 → 寫接力檔 → 換新對話）。原本不帶參數是查狀態，現在要打 `/relay status`。
- 這個 session 已經用 `/relay off` 暫停時不會接力，會提示先 `/relay on`。
- member 的接力由 lead 安排；主機的自我接力開關關著、或接力已在進行中時，也不會接力，各自回一句原因。
- 回覆只說「已送出接力申請，等待核准」，不預設由誰核准（無人值守模式下會自動核准）。

### Added：`/lead` 立刻申請成為 lead（#2111）

- `/lead [補充]`：請這個 session 立刻申請成為 lead，不再先判斷工作夠不夠大。補充可以用自然語言寫名稱、短名或人數上限，例如「/lead 叫 B 線、上限 2」；超過 200 bytes 會截斷並告知。
- 已經是 lead 時直接回「已經是 lead：<名稱> ［<短名>］（上限 N）」，不會再申請。
- member、已有申請在等待等情況，由 daemon 的回應決定，agent 會轉告原因。
- 接力進行中時不受理 `/lead`。
- pdx-team skill 補上兩行：使用者打 `/lead` 時立刻申請；`/relay` 是使用者的指令，agent 不會自己執行。

### 其他

- 對話 API 的真機驗收腳本 `scripts/acceptance/u1-6.sh`（#2110，介面語言 U1-6）；只是測試腳本，不影響執行。

## [1.0.0-alpha.622] - 2026-10-09

> 動 daemon、CLI 與 mod：**要部署 daemon，並重跑 `pdx setup --agent cc`**（mod 與 pdx-team skill 都有改）；SPA、Electron 不必更新。這一版讓 lead 派 member 時可以同時交辦第一個任務，member 接力後的新對話也會看到自己手上還沒做完的任務。

### Added：開 member 時一併交辦第一個任務 — 派工／回報 T-2a（#2104）

- `pdx spawn --task-subject <主旨> [--done-when <完成條件>]…`：開 member 的同時建立它的第一個任務（brief 就是任務說明）。member 與任務在同一筆交易裡建立，重送同一個 spawn 不會重複建任務；不帶 `--task-subject` 時行為完全不變。
- `pdx task mine [--all] [--json | --seed]`：member 查自己手上的任務；lead 會被拒絕（exit 13）。`--seed` 印出接力通知用的幾行文字，沒有未完成的任務時什麼都不印。
- pdx-team skill 補上新旗標的用法。
- 舊的 team.db 會自動補欄位，不必手動遷移。

### Added：接力後的新對話看得到手上的任務 — 派工／回報 T-2b（#2106）

- member 接力時，交給新對話的第一則訊息最後多一段「你手上的任務：」，列出還沒完成的任務（最多 10 項，其餘顯示「另有 N 項」）。
- 這段是固定附加的，使用者自訂過接力訊息內容也一樣會列；沒有任務、lead、舊版 pdx、查詢失敗或逾時時這段整個省略，訊息與之前逐字相同。

## [1.0.0-alpha.621] - 2026-10-09

> 只動 daemon：**要部署 daemon**；mod 沒有改，不必重跑 `pdx setup`；SPA、Electron 不必更新。這一版加上對話的即時推送（給之後 Mac App 指揮台與 iOS 用），目前還沒有畫面使用它。

### Added：對話的即時推送 — 介面語言 U1-6（#2099–#2102）

- `WS /ws/conversations/claude/{session_id}`：連上時先給一份對話快照，之後推送增量變化；也會推送這個 session 待處理的核准（先給一份清單，之後逐筆推送開啟與關閉）。
- 對話的 WS 開著時，就算是一個能遠端回應核准的端點：終端機裡的權限詢問會建立核准列，App 可以回應。這跟 App 開著主畫面時的行為一樣，終端機那邊照常可以直接回答。
- 對話 API 回應的每個項目多帶 `index`（#2102）。
- team 模組提供每個 session 的核准訂閱（#2100）：只推該 session 自己的核准；接力後的新對話有新的訂閱，不沿用舊的；連線不論怎麼關閉都會退訂並釋放計數。

## [1.0.0-alpha.620] - 2026-10-09

> 只動 daemon：**要部署 daemon**；mod 沒有改，不必重跑 `pdx setup`；SPA、Electron 不必更新。這一版加上對話模型的 HTTP 介面（給之後 Mac App 指揮台與 iOS 用），目前還沒有畫面使用它。

### Added：對話的 HTTP 介面 — 介面語言 U1-6（#2089–#2097）

- `GET /api/conversations/claude/{session_id}`：把一個 Claude Code 對話整理成回合、步驟、輸出的結構化內容；支援 `turns`、`before`、`after`、`around` 取一段，以及增量更新。`GET …/subagents/{agent_id}` 取子 agent 的對話。主機能力清單多 `conversations.v1`。
- 只讀、需要 token；只回答確實正在某個 pane 執行的 session。對話紀錄檔若是 FIFO 之類的特殊檔，直接拒絕，不會卡住。
- 底層：對話紀錄的追蹤、快取與解析器（#2089、#2090、#2093），以及「哪些 pane 確實在跑這個 session」的確認（#2094）；`internal/transcriptpath` 純搬移（#2091）。

## [1.0.0-alpha.619] - 2026-10-09

> 動 mod：**要重跑 `pdx setup --agent cc`**（daemon 一起換新，程式沒有改）；SPA、Electron 不必更新。

### Fixed：Monitor 工具的背景符號（介面語言 U1-3，#2087）

- 用 Monitor 工具開的背景任務，外掛原本沒有回報成「監控」，所以分頁角落的眼睛符號不會出現。現在會正確回報並顯示。

## [1.0.0-alpha.618] - 2026-10-09

> 動 mod：**要重跑 `pdx setup --agent cc`**（daemon 一起換新，內容沒有改）；SPA、Electron 不必更新。資源租約仍是「只記錄、不攔」（advise），「只記錄一天」從這一版部署時重新起算。

### Fixed：重指令攔截的誤判（資源租約 P2，#2085）

- 用 heredoc 寫檔或跑腳本時（例如 `python3 - <<'EOF' … EOF`），內文裡如果提到重指令（像用反引號包著 `go test ./...` 的說明文字），原本會被當成要執行的重指令而攔下。實際的 Bash 紀錄裡這種情況很常見。現在引號 heredoc 的內文與註解都不再被當成指令。

## [1.0.0-alpha.617] - 2026-10-09

> 動 daemon、CLI、mod、skill 和 SPA：**要部署 daemon，也要重跑 `pdx setup --agent cc`**（mod 新增重指令攔截、skill 更新）；SPA 在主 checkout 快轉後就生效；Electron 不必更新。**部署前主機的資源租約先切到「只記錄、不攔」（advise）**：外掛會攔下前景的重指令並記錄「照規則要不要等」，但一律立刻放行。跑一天、看過報告之後，才會切成真的排隊。

### Added：外掛自動攔截重指令 — 資源租約 P2（#2079、#2081、#2083）

- 每個 Claude Code session 在前景跑重指令時（完整測試、build、`go test ./...`、`go vet ./...` 等），外掛會先替它申請租約，跑完自動釋放；一般指令、只跑單一檔案或單一套件的測試、背景執行的指令都不攔，也不會多花任何時間。
- 完整 vitest 沒帶 `--maxWorkers` 時，外掛會自動補上 `--maxWorkers=3`，並告訴 agent 改了什麼。
- 真的排隊時（之後切成 lease 模式才會發生），外掛會告訴 agent「先等了多久，主機忙，不是卡住」，避免它重試或以為當掉。實測：重指令在別人佔位時排隊 34 秒後執行，agent 回覆「不是卡住，所以沒有重試」。
- 判斷「是不是重指令」是啟發式的：寧可多攔，也不漏攔常見寫法；advise 那一天的報告會列出誤攔與漏攔的抽樣。
- skill `pdx-lease` 改寫：前景重指令由外掛處理，只有寫進腳本的重指令才需要自己用 `pdx lease run`。**「完整 vitest 先向統籌拿名額」的規定照舊**，等切成真的排隊之後才取消。

### Changed：燈號 v2 的 App 端 — 介面語言 U1-3（#2070、#2076、#2080、#2082）

- 只有狀態真的改變時才標未讀（同一個狀態重複送來不再標）。
- App 改用新版協定接收燈號：每筆帶序號，有缺漏時自動重新同步；舊版主機照舊運作。
- 分頁的燈號取所有窗格裡最要緊的那個；工作區的未讀按分頁計；「等你處理」的手掌圖示跨窗格顯示。
- 分頁角落顯示背景符號（排程、監控、背景工作）。

### Added：核准框的 team 短名欄位 — team label TL-2（#2077）

- lead 申請的核准框在「Team 名稱」下多一個「標籤（短名）」欄位：預填申請時給的短名；清空時即時顯示會從名稱推導出什麼（推不出來顯示「（無）」）；旁邊有寬度計數，超過 10 格不能核准。分頁群組標籤與右邊 panel 的顯示由介面線接著做。

### Docs

- 介面語言設計文件 §5 與 plan 修訂（#2075）。

## [1.0.0-alpha.616] - 2026-10-09

> 動 daemon、CLI 和 skill：**要部署 daemon，也要重跑 `pdx setup --agent cc`**（skill 更新；mod 沒有改）；SPA、Electron 不必更新。使用者看得到的有兩項：**team 可以有短名標籤**（`pdx lead request --label`），以及**資源租約的監控報告** `pdx lease report`。App 的核准框欄位與分頁群組標籤的顯示，是之後的版本。

### Added：team 短名標籤 — 主機端（#2066、#2067、#2069、#2072）

- team 名稱分成兩種：**短名標籤**（分頁群組最前面的標籤）與**正常名稱**（右邊 team 面板）。lead 申請時用 `--label "<短名>"` 給短名，例如「A 線」「資源線」「資源派工」；skill 要求 lead 一律自己取一個有意義的短名，而不是把長名截短。
- 短名最多 10 格寬：中文、全形字與 emoji 算 2 格，其他算 1 格，大約是中文 5 個字，英文可以長一點。太寬時指令直接拒絕，不會送出申請。
- 沒給短名時，主機只取正常名稱裡第一個分隔符號（`：` `|` `／` `－` `—`；英文的 `-` `/` 要兩側有空白才算）前面那段，放得進 10 格才用，例如「A 線：資源租約＋派工回報」取「A 線」；取不到就留空，**不會從中間截斷**。
- 核准時可以改短名；舊版 App 核准（不帶短名欄位）時保留申請時給的短名；審核者清空則改從正常名稱推導。無人值守自動核准用申請時給的或推導出來的。
- `pdx team` 第一行變成 `team: <名稱> ［<短名>］`（兩者相同時只印名稱）；`GET /api/team`、team 名冊帶 `team_label`；主機能力清單多 `team.label.v1`。

### Added：資源租約的監控報告（R10，#2065）

- `pdx lease report [--since 24h] [--json]` 與 `GET /api/resources/report`：各類重指令的申請次數，直接放行、等過才放行、超量放行各幾次，等待時間的中位數、p90、最長；**原本的規則（B）會擋下幾次**；主機「滿」的時間比例、次數與最長一次；兩個以上大租約同時進行時的最高負載與記憶體；各類指令實際用量對照估值。報告只讀已存的紀錄，最多看 14 天。

### Docs

- team 介面 plan（#2068）；介面語言 U1-6 plan（#2073）。

## [1.0.0-alpha.615] - 2026-10-09

> 動 daemon 和 CLI：**要部署 daemon**（`bin/pdx` 一起換新）；mod 與 skill 都沒有改，不必重跑 `pdx setup`；SPA、Electron 不必更新。資源租約第一版的收尾：主機設定的等待期限現在真的會生效，App 也可以收到租約變動的即時推送。

### Fixed：主機設定的等待期限被指令蓋掉（#2061）

- `pdx lease run／acquire` 沒有指定 `--wait` 時，原本一律送出 5 分鐘，蓋掉主機設定的等待期限。現在沒指定時由主機的設定決定，有指定才覆蓋。

### Added：租約變動的即時推送與「工作已結束」的收回（資源租約 P1-2d，#2059）

- 新的 WS 事件 `resources.changed`：有租約持有中或排隊時，最多每 5 秒推一次；放行、結束、有新的排隊時推一次；一訂閱就先給一份快照；完全閒置時不推。App 的顯示由介面線負責，顯示資源指標期間也會每 15 秒自己查一次 `GET /api/resources` 作為兜底。
- 以 session 計量的租約，持有者底下連續兩次取樣都沒有任何行程時，視為工作已結束，收回租約。這是給之後外掛自動攔截的保險：外掛萬一漏了釋放，租約不會一直佔著。主機重啟後要先重新量過一次，才會套用這條規則。

## [1.0.0-alpha.614] - 2026-10-09

> 動 daemon 和 CLI：**要部署 daemon**（`bin/pdx` 一起換新）；mod 與 skill 都沒有改，不必重跑 `pdx setup`；SPA、Electron 不必更新。這一版修好 613 的資源租約：613 的租約一建立就被收回，排隊從沒真正生效。

### Fixed：資源租約在非 UTC 時區失效（#2057）

- 613 在台灣時區（UTC+8）下，每個 `pdx lease run`／`acquire` 的租約一建立就被判「持有者已不在」而收回，所以不會排隊，也不會佔額度。方向是全部放行，不會擋到任何人，但租約等於沒有生效。
- 原因：持有者的啟動時間有兩個來源。`ps` 印的是本地時間；Claude Code 的 session 紀錄是 UTC。主機一律當 UTC 解讀，所以 `ps` 來的差了 8 小時。
- 現在 `pdx` 指令會先用自己的時區把 `ps` 的時間轉成 UTC 再送出，主機存的一律是 UTC。測試也改成在 UTC+8 下執行。**主機和指令要同一版**：新主機搭配舊的 `pdx` 指令，問題會再出現。

### Docs

- 燈號 v2 的 App 端（U1-3）plan（#2058）。

## [1.0.0-alpha.613] - 2026-10-09

> 動 daemon、CLI 和 skill：**要部署 daemon，也要重跑 `pdx setup --agent cc`**（skill 新增 pdx-lease；mod 沒有改）；SPA、Electron 不必更新。使用者看得到的是**資源租約第一版**：`pdx lease run` 把重指令包起來，主機忙時先排隊。這一版只有主動用 `pdx lease run` 的指令會排隊，外掛還不會自動攔截。

### Added：資源租約 P1 — `pdx lease`（#2050、#2052、#2053、#2054、#2055）

- `pdx lease run [--kind K | --weight N] [--wait 5m] -- <指令>`：先申請租約，拿到才執行，結束後自動釋放。排過隊會在 stderr 印一行說明等了多久；結束碼原樣傳回；Ctrl-C 等訊號會轉給指令。主機的 daemon 沒回應時，等 5 秒就照常執行（不會因為租約卡住工作）。
- 准入照使用者定案的規則（R9）：容量 100 只在重指令之間分配（完整測試、build 各估 35，同時最多兩個）；主機實際用量只用來判斷「滿了沒」——負載到核心數或記憶體 90% 就排隊，降到 9 成以下才放；最多等 5 分鐘，時間到就放行並記一筆「超量」。
- `pdx lease ls`：主機的負載與記憶體、持有中的租約（誰、什麼指令、估值、實際用量）、排隊中的租約、最近結束的租約（超量會標出）。`pdx lease acquire／release` 是給外掛與腳本用的底層指令。
- 每一次准入都記下當時的主機狀態，以及「原本的規則會不會擋」（R10），之後的報告會用到。持有者不見了或超過 60 分鐘，租約會被收回。
- 新的 skill `pdx-lease`：agent 跑重指令時用 `pdx lease run` 包起來；等待就是主機忙，不要重試、不要殺掉、不要改成背景執行。**完整 vitest 先向統籌拿名額的規定照舊**，等外掛自動攔截上線後才退役。
- 主機資源 API：`GET /api/resources` 的租約清單改為顯示真實的估值與用量；`POST／GET／DELETE /api/resources/leases`。

## [1.0.0-alpha.612] - 2026-10-09

> 只動 daemon 和 CLI：**要部署 daemon**；mod 沒有改，不必重跑 `pdx setup`；SPA、Electron 不必更新。使用者看得到的有：member 可以用 `pdx report` 回報，派工／回報的第一批（T-1）到這一版全部可以用了；燈號 v2 的主機端全部完成。資源租約的監控紀錄開始記錄每分鐘的主機狀態；租約還不會攔任何指令。

### Added：`pdx report` — 派工／回報規格 T-1 完成（#2040）

- member 用 `pdx report <kind>` 回報里程碑：開工、進度、提問、待放行、已合併、卡住、完成。不同種類要帶的欄位不同，指令會檢查；回報同時會通知 lead。送出失敗可以用 `--id` 重送，不會重複記錄。
- `pdx report ls／show` 查看回報；member 只看得到自己任務的回報，查詢時要帶 `--task`。
- 搭配 611 的 `pdx task` 與 `pdx team` 的 TASK／LAST 欄：lead 派工、member 回報、`pdx team` 看每個人在做什麼，整套可以用了。

### Changed：燈號 v2 的主機端完成（介面語言 U1-2，#2045、#2048）

- 不在 tmux 裡的 session，也會保留最後一筆燈號，重新連線時補送（#2045）。
- 用新版協定連線的 App 會在連上時拿到完整的燈號快照，每一筆都帶序號；快照讀取失敗時不送半套，直接斷線讓 App 重連（#2048）。

### Internal

- 資源租約：使用者定案的准入規則 A（R9）與監控紀錄（R10）寫進 spec 與 plan（#2042）；准入公式本身（#2043，純函式，還沒有呼叫端）。
- 資源租約的監控紀錄開始寫入（#2046）：主機每分鐘一筆（負載、記憶體、是否滿載與滿了幾秒、持有與排隊的租約數），保留 14 天；租約表加上「准入時的主機狀態」欄位，開機時自動遷移，舊資料標成「未記錄」。
- 對話模型：轉換器的黃金測試資料與檢查（#2033、#2047），目前不在任何執行路徑上。

## [1.0.0-alpha.611] - 2026-10-09

> 只動 daemon 和 CLI：**要部署 daemon**；mod 沒有改，不必重跑 `pdx setup`；SPA、Electron 不必更新。**alpha.610 部署 3 分鐘後就回退到 609**：燈號的一個改動讓每次 hook 都要等約 1 秒。這一版修好了那個問題，610 的內容也在這一版第一次真正上線。使用者看得到的有：燈號不再拖慢 hook、`pdx task` 派工指令、`pdx team` 多了任務欄與最後一句欄。

### Fixed：610 的 hook 變慢（燈號 v2，#2037）

- 610 讀燈號狀態時，每個 pane 要問 tmux 兩次；23 個 pane 約 1.25 秒，而且所有 hook 都排隊等這一步，所以每次工具呼叫大約慢 1 秒。
- 現在一次讀取只問 tmux 一次，取回所有 pane（實測 3.5–3.9 ms）。tmux 卡住超過 2 秒時，那次讀取直接放棄、不送出燈號，也不會把燈號清掉。

### Added：派工指令 — 派工／回報規格 T（#2032、#2036）

- `pdx task add／ls／show／start／done／delete／reassign`：lead 派任務給 member，每筆任務有名稱、目標、完成定義與狀態，派出時會通知 member。member 只看得到、也只能改自己的任務。
- `pdx team` 多 TASK、LAST 兩欄：每個 member 目前在做哪件任務，以及它最後一次回報。中文也會正確對齊（先前含中文的 title 會讓欄位錯位）。
- member 回報用的 `pdx report` 指令是下一版；回報的 API 這一版已經在了（#2027，610 的 changelog 漏寫）。

### 610 的內容（這一版第一次真正上線）

- **燈號 v2**：短回合結束時不再閃一下綠燈（#2026）；一個 tmux session 有多個 pane 時，燈號依優先序取最要緊的（#2014）；hook 的燈號一律經同一個出口送出，帶上序號（#2029）。
- **任務 API**：`/api/team/tasks`，capability `team.tasks.v1`（#2020）；回報 API 加上回報表的主鍵遷移，開機時自動完成（#2027）。
- **資源租約 P1 的底層**：租約資料庫 `resources.db`（只有自己能讀）、資源設定（完整測試的預設估值 35）、判斷「滿」時加緩衝、`GET /api/resources` 的租約清單（#2021、#2024、#2028）；持有者不見或超時就收回租約（#2031）；量測每個租約自己的用量（#2035）。**這一版還沒有申請租約的指令，不會攔任何指令，也不會排隊。**

## [1.0.0-alpha.610] - 2026-10-09

> 動 daemon 和 SPA：**要部署 daemon**；mod 沒有改，不必重跑 `pdx setup`；SPA 在主 checkout 快轉後就生效；Electron 不必更新。使用者看得到的有三項：**lead 申請的核准框可以改 team 名稱**、短回合結束時不再閃一下綠燈、一個 tmux session 有多個 pane 時，燈號依優先序顯示最要緊的那個。其餘是底層。

### Added：核准框的 team 名稱欄位 — team name TN-2（#2023）

- lead 申請的核准框多一個「Team 名稱」欄位，預填 lead 申請時給的名字，可以改或清空；不合規則（超過 64 bytes、有不能顯示的字元）時無法核准，並顯示原因。
- 對方主機的 daemon 太舊、不認得 team 名稱時，不顯示這個欄位。
- 名字要顯示在分頁群組、側欄、team 面板，是之後的版本。

### Fixed／Changed：燈號 v2（介面語言 U1-2，#2026、#2014、#2029）

- 短回合結束時不再閃一下綠燈：hook 與外掛事件改用事件發生的時間比較先後，容許的時間差放寬到 5 秒（#2026）。
- 一個 tmux session 有多個 pane 時，燈號依優先序取最要緊的 pane：錯誤 > 等你處理 > 工作中 > 閒置；同級時取比較新的。背景符號取所有 pane 裡最高的那個（#2014）。
- hook 送出的燈號一律經同一個出口，帶上序號，並記錄佔用時間，為之後的完整快照做準備（#2029）。

### Internal

- 派工／回報規格 T：任務 API（`/api/team/tasks`，capability `team.tasks.v1`）已在主機上，lead 與 member 各自只碰得到自己能碰的任務；`pdx task` 指令與回報 API 是之後的版本，目前沒有任何指令會用到（#2020）。
- 資源租約 P1：租約資料庫（`resources.db`，只有自己能讀）、資源設定（完整測試的預設估值 35）、判斷「滿」時加上緩衝、`GET /api/resources` 多出租約清單（#2021、#2024、#2028）。這一版還沒有申請租約的指令，不會攔任何指令，也不會排隊。

## [1.0.0-alpha.609] - 2026-10-09

> 只動 daemon：**要部署 daemon**；mod 沒有改，不必重跑 `pdx setup`；SPA、Electron 不必更新。這是燈號 v2 的熱修：背景分頁不再在每個回合開頭被誤標未讀。

### Fixed：回合開頭的假未讀（燈號 v2 熱修，#2015）

- 607 起，每個回合開頭會先送出一筆「閒置」，約 1 秒後才變成「工作中」。App 會把每筆閒置都標成未讀，所以背景分頁一收到 prompt（包括 peer 訊息叫醒的回合）就亮未讀。
- 現在只有兩種 hook 可以在回合交界短暫蓋過外掛的事件：送出 prompt 變成「工作中」，回合結束變成「閒置」。其他情況一律以外掛的即時事件為準。

### Internal

- 對話模型：Claude Code 對話紀錄轉換器補上 diff、指令、子 agent 連結與子 agent 對話的轉換；目前不在任何執行路徑上（#2011）。

## [1.0.0-alpha.608] - 2026-10-09

> 動 daemon、CLI 和 skill：**要部署 daemon，也要重跑 `pdx setup --agent cc`**（skill 換版；mod 沒有改）；SPA、Electron 不必更新。使用者看得到的有兩項：**team 可以取名字**（lead 申請時交出、`pdx team` 第一行顯示；App 的核准框欄位是下一版），以及沒有外掛即時事件的 session 燈號更準了。其餘是底層。

### Added：team 名稱 — 主機端（#2016）

- `pdx lead request --name "<名字>"`：lead 申請時一起交出 team 名稱。名字可以不填，但 skill 會要求 lead 一律填。規則跟 session title 一樣：頭尾空白去掉，最多 64 bytes，只能是可顯示的字元。
- 核准時可以改名；不改就用申請時給的名字。無人值守自動核准也用申請時的名字。
- `pdx team` 在表格上方多一行 `team: <名字>`；`GET /api/team` 與 `GET /api/team/roster` 都帶 `team_name`。主機能力清單多 `team.name.v1`。
- 核准後還不能改名（待確認）。App 的核准框欄位，以及分頁群組、側欄、team 面板上的名字顯示，會在之後的版本加上。

### Fixed：沒有外掛即時事件時的燈號（介面語言 U1-2a-4，#2005）

- 按 Esc 中斷一個回合時，燈號會回到閒置；Claude Code 不會為中斷送出「回合結束」，以前燈號會卡著。
- 子 agent 失敗時，主 session 的燈號不再跟著變成錯誤。
- 回合結束時，如果還有背景工作或排程，燈號會加上背景符號。
- 一個 pane 出錯，不再擋住同一個 session 其他 pane 的燈號更新。

### Internal

- 派工／回報規格 T：任務表與回報表的資料層（#2007、#2012）。還沒有 API 或指令可以用，這一版只會在開機時建表。
- 對話模型：Claude Code 對話紀錄轉換器加上工具步驟（種類、結果配對、狀態、輸出截斷）；目前不在任何執行路徑上（#2010）。

## [1.0.0-alpha.607] - 2026-10-09

> 動 daemon、CLI 和 SPA：**要部署 daemon**（`bin/pdx` 一起換新）；這版 mod 沒有改，不必重跑 `pdx setup`；主 checkout 快轉後 SPA 就生效；Electron 不必更新。使用者看得到的有三項：**燈號改由 Claude Code 外掛的即時事件驅動**（燈號 v2 上線）、`pdx team` 多了 CPU／記憶體欄、移除主機時會一併清掉它的核准框。其餘是底層，還沒有新畫面。

### Changed：燈號 v2 上線 — 介面語言 U1-2（#1990）

- 燈號改以 Claude Code 外掛（mod）回報的即時事件為準，hook 推算的燈號退居後備。例如在終端機核准權限時，黃燈會立刻撤掉，不必等下一個事件。
- daemon 重啟、App 重新連線後，同樣的狀態不會再送一次，所以不會憑空冒出未讀。
- 外掛接手某個 session 之後，舊的探測不會再把燈號蓋回去。

### Added：主機資源量測 — 資源租約 P0（#1999、#2003）

- daemon 每 5 秒量一次主機負載（1 分鐘平均負載、記憶體）和每個 session 的行程樹用量，提供 `GET /api/resources`。
- `pdx team` 多 CPU、MEM 兩欄，顯示每個 member 目前的用量；`pdx lease ls` 列出主機數字與各 session 的用量。這一版**只量測**，不攔任何指令、也不排隊。
- 平台不支援或讀取失敗時，主機數字標成不可用；`pdx team` 照常顯示，不會印錯誤。

### Fixed

- 移除主機，或主機的 IP／port 改變時，會清掉它留下的核准框；送到一半的決定不會落到新主機上（#1998，#1978）。

### Internal

- App 端的 team 名冊資料層（每台主機的 roster store、`teamOfTab` 等查詢），給之後的 team 介面用；目前沒有畫面使用（#1996、#1997）。
- 對話模型：Claude Code 對話紀錄轉換器的核心（之後 iOS 與 Mac App 指揮台共用），目前不在任何執行路徑上（#2000）。

### Docs

- 派工／回報規格 T 的 spec 與 plan（#2004）。

## [1.0.0-alpha.606] - 2026-10-09

> 動 daemon、mod 和 SPA：**要部署 daemon，也要重跑 `pdx setup --agent cc`**（mod 換版）；主 checkout 快轉後 SPA 即生效；Electron 不必更新。使用者看得到的是**標題列的「無人值守」開關**與它的期間清單、daemon 重啟後燈號不再全數變未讀；其餘是底層（team 名冊、燈號 v2 的 daemon 前半、對話模型型別），還沒有新畫面。

### Added：標題列的無人值守開關 — U23 SPA（#1982、#1986、#1991、#1987）

- 標題列（版型按鈕左邊）多一顆「無人值守」：一次套用到工作台上顯示的所有主機，狀態有「關／開／部分」（部分＝有主機沒開、連不上或 daemon 太舊，tooltip 逐台列出）。
- 開著時「成為 lead」與「自我接力」的申請由 daemon 自動通過，不跳核准框、不發通知；工具權限與 agent 的提問照常等人。
- 旁邊的 ▾ 列出「無人值守期間自動通過」的申請（主機、session、種類、時間；非今天顯示日期；「顯示更多」）；某台讀取失敗或連不上會寫出名字（連不上的標「可能仍在自動通過」）。
- 你自己按的核准／拒絕剛好輸給自動核准時，仍會提示是誰決定的。
- 核准框的 member 上限一律預填 3，lead 申請的數字不同時在旁顯示「lead 申請 N 個」（U25）。

### Fixed

- daemon 重啟後，燈號不再全部變成未讀：重播只有在狀態真的改變時才可能標未讀（例如原本工作中、重播已閒置＝離線期間做完了）（#1975）。介面線 U1-3 之後會全面取代這個做法。
- 核准框的開啟／關閉通知改走嚴格推送，接收端塞滿時改為斷線重連拿完整狀態，不再靜默遺失（#1994，#1970）。

### Added：team 名冊（daemon）— U24 PL-1a′／PL-1f′（#1980、#1984、#1985、#1992）

- `GET /api/team/roster` 與 `team.roster` 事件：每台主機的 live team、lead 與 member（地址、title、tmux session、狀態、模型、effort、context 用量），名冊變動時以嚴格推送通知 App。`GET /api/team` 的 member 多 `origin` 欄位。
- 修正：接力（relay cleared）後，新 session 不再顯示上一個 session 的 context 讀數（`pdx team` 同修）。
- 給之後的 team 介面（左側 member 列、上方 lead 群組、浮蓋面板）用；畫面還沒做。

### Added：燈號 v2 的 daemon 前半 — 介面語言 U1-2（#1974、#1976、#1983）

- mod 每批事件帶 cwd 與是否互動（daemon 重啟後也對得回 pane）；心跳帶錯誤與背景狀態；在終端機核准權限的瞬間送出 `tool.approved`（燈號 v2 會據此立刻離開黃燈）；`$.agent.list()` 失敗時不再送空清單。
- daemon 的燈號狀態機與 mod 訂閱已就緒，但**開關預設關**，現行燈號行為不變（事件多了 `source`、`background` 兩個欄位）。

### Added：對話模型型別 — 介面語言 U1-4a（#1989）

- daemon 端對話模型的型別、JSON 形狀、驗證與能力宣告（之後 iOS 與 Mac App 指揮台共用）；尚無 API。

### Docs

- 主機層資源租約 spec 與 plan（#1993）；U1-4 plan（#1988）；team 顯示的確認包與設計稿（#1981，已由使用者 19 條定案取代）、U24 顯示先行的重排（#1977）。

## [1.0.0-alpha.605] - 2026-10-08

> 動 daemon、skill 和 SPA：**要部署 daemon，也要重跑 `pdx setup --agent cc`**（skill 文字改了）；主 checkout 快轉後 SPA 即生效；Electron 不必更新。使用者看得到的是 worker 狀態改由 daemon 推送（燈號更即時）；無人值守模式的 daemon 端已就緒，標題列按鈕在之後的版本。

### Added：worker 狀態改由 daemon 推送 — #1866 PR2（#1960、#1961、#1964、#1965）

- App 連到主機時改帶 `?nex=v1`，worker 的狀態變化由 daemon 主動推過來，不再整份清單重抓；斷線、漏推、daemon 重啟都會自動對帳補齊。
- 每 120 秒做一次安全對帳（只在畫面看得到、有訂閱時），發現不一致會修正並計數。
- 已封存的 worker 清單以封存版本號重新整理。
- App 要重新整理（或重連）一次才會開始接收推送。

### Added：無人值守模式的 daemon 端 — U23（#1957、#1962、#1967、#1969、#1971）

- 開關存在各主機的 daemon（重啟後保留）。開著時，「成為 lead」與「自我接力」的申請由 daemon 立即自動核准，記錄的決定者是 `unattended`；開啟當下已在等待的這兩種申請也一併核准。worker 的工具權限與 agent 的提問照常等人。
- 自動核准的 lead 申請，member 上限最多 3（U25）。
- 新 API：`GET`／`PUT /api/team/unattended`（只能由 App 呼叫、需管理權限）；能力宣告 `relay.unattended.v1`；開關變更以嚴格推送通知所有視窗（塞不下就斷線重連拿最新狀態）。
- skill 明定 agent 絕不開啟、也不要求開啟無人值守。
- 標題列的開關按鈕與「離開期間自動核准了哪些」清單在之後的版本。

### Added：peer 清單的執行體列 — peer mailbox P4a（#1956）

- daemon 的 peer 清單多了執行體列（依 session 去重：tmux 列 ＞ 執行體列 ＞ 其他），目前**還不能送訊**；送訊（P4b）留到通訊線恢復。

### Docs

- Claude Code 官方 Agent Teams 與 workflow 的設計研究（#1966）；介面語言 U1-2 plan（#1972，含 spec §7 的序號修正）。

### 追蹤

- #1968（team module Start 職責膨脹）、#1970（核准框的開啟／關閉通知可能遺失）、#1959（peers/module.go 拆分）。

## [1.0.0-alpha.604] - 2026-10-08

> 動 daemon、mod 和 SPA：**要部署 daemon，也要重跑 `pdx setup --agent cc`**（mod 換版）；主 checkout 快轉後 SPA 即生效；Electron 不必更新。使用者看得到的是清單與設定的小修；其餘是底層（mod 事件上報、執行體狀態推送的 daemon 端、無人值守開關的儲存），還沒有畫面。

### Added：mod 事件上報 — 介面語言 U1-1b（#1941、#1951）

- Purdex mod 把互動式 Claude Code session 的事件送到 daemon 的 mod socket：session 開始／切換／結束、回合開始與結束、工具起訖、核准判斷、subagent、壓縮、用量、背景工作，以及每 10 秒一次心跳。非互動（`-p`）的 session 不回報。
- daemon 新增讀取 API：`GET /api/mod/streams`、`GET /api/mod/streams/{stream}/events?after=<seq>`。之後燈號 v2 與對話模型都從這裡取資料。
- 上報出錯絕不影響 Claude Code：hook 只觀察、照原樣放行；佇列有上限、會重試，丟掉的數量會記下來。
- mod 多一個檢查：任何事件在兩個檔案都不帶 matcher 註冊就擋下（那會讓整個 mod 載入失敗，連接力與提問分流一起失效）。

### Added：執行體狀態推送的 daemon 端 — #1866 PR1c（#1943–#1946）

- daemon 逐頁讀 Nexen 清單並輪替 epoch／bseq；訂閱中斷時以退避重新訂閱並重新播種；每 120 秒做一次安全對帳，補上漏推的狀態並計數。
- 只對連線時帶 `?nex=v1` 的訂閱者生效；App 還沒帶（SPA 端是 PR2），所以目前沒有可見變化。

### Added：無人值守模式的開關儲存 — U23 PU-1a（#1957）

- 主機設定多一個 `unattended` 值，讀取時嚴格檢查（缺欄位、null、開著卻沒有時間，一律當無效）。還沒有程式讀它，按鈕在之後的版本。

### Fixed：清單與交接的小修 — #1627（#1947）

- 清單的退出確認框：那一列消失時自動關閉，列回來也不會重新打開。
- 退出鈕永遠預留位置；滑鼠移到整列才變底色，鍵盤聚焦時也看得到。
- worker 還沒退出時，「交給終端機」只顯示持續的提示。
- 重建時遇到別處先改（CAS 失敗）的提示多一顆「開啟」。
- 清單載入失敗按重試，鍵盤焦點留在原地；重試中保留錯誤列，Workers 清單重試不再閃骨架。
- 切換重建模式會清掉舊的重建錯誤。

### Fixed：設定／工作區小 bug 批次（#1954）

- Profile Sync 移除某個工作區時，其他工作區裡屬於它的設定分頁一起關掉。
- 之後才出現的模組會補進基準，不再覆寫已知的模組。
- 其餘是內部整理（#285、#822、#839、#1042、#1485）。

### 追蹤

- #1950（daemon token 為空時 API 不驗證）、#1953（執行體重訂閱的 TOCTOU）、#625（需設計決定）、#1952、#1955。


## [1.0.0-alpha.603] - 2026-10-08

> 動 daemon：**要部署 daemon**；不需 `pdx setup`、Electron 不必更新。使用者可見：peer 地址改成虛擬名。⚠️ **地址格式改變**：Claude Code 自己的 session 名（CLI 名）不再能當收件人。

### Changed：peer 地址改成虛擬名 — peer mailbox P3a／P3b（#1917、#1918、#1915、#1937）

- peer 地址改成虛擬名 `<基底>-<ref 前兩碼>`（例 `mlab/purdex-b0-q3`），`pdx peers`／`pdx msg whoami`／狀態列都顯示這個名字，也可以直接複製。
- Claude Code 自己的 session 名（CLI 名）不再路由，用它送會回 `peer_not_found`。
- `peer_not_found` 會附「did you mean `<host>/<虛擬名>`?」提示虛擬地址。
- 送訊支援括號形式 `"<host>/<name> [ref]"` 並核對 name（不符回 `name_mismatch`）；同名回 `ambiguous` 並附候選。`pdx kill` 與 `pdx team` 也依虛擬名比對。
- 送訊 envelope 帶 `address_version=5`，新舊版本互通以此判斷。

### Added：mod 事件通道的 daemon 端 — 介面語言 U1-1a（#1925–#1928，無使用者可見變化）

- daemon 在 `<data_dir>/mod.sock` 開 Unix socket，接收 Purdex mod 上報的事件；mod 端的上報（events.js）在 U1-1b，下一版才上。
- stream registry 有上限，滿了回 503 `registry_full`；socket 無法 listen 時只記錄，不影響 daemon 啟動。
- `pdx setup --agent cc` 寫出的 `pdx.json` 多一個 `mod_socket` 欄位（下次跑 `pdx setup` 才會寫入）。


## [1.0.0-alpha.602] - 2026-10-08

> 只動 SPA：**fast-forward 主 checkout 即生效**，不需部署 daemon、不需 `pdx setup`。使用者可見：核准對話框可以縮小成右下角膠囊；Hosts › 接力 可以編輯三段接力 prompt。lead/team 接力線的 P9（spec U21／U22）至此全部完成。

### Added：核准對話框縮小成膠囊 — P9b-2（#1920、#1922，spec U22 (b)）

- 對話框標題列多一顆「縮小」：縮成右下角膠囊「● 待核准 N · m:ss」（N＝所有主機待核准的筆數，倒數是最近到期那一筆），點一下還原。
- 縮小期間鍵盤與滑鼠都回到 tab（沒有焦點鎖定，Esc 也不再被攔），焦點交還原本的位置；還原時對話框重新取得焦點；已填的上限、根目錄與「不再詢問」都保留。
- 縮小期間有新申請：膠囊的數字更新並閃一下，**不會**自動展開；申請照常倒數、照常發系統通知。點系統通知時，只有那一筆還在等才會還原對話框。
- 最後一筆申請結束後膠囊消失，下一筆申請會直接顯示對話框。縮小只屬於這個視窗，重新整理後顯示對話框。
- 螢幕閱讀器會念出筆數與倒數，筆數增加時公告一次。

### Added：Hosts › 接力 編輯接力 prompt — P9a-3（#1938、#1939，spec U21）

- 三個編輯框：撰寫接力檔、補齊接力檔、喚醒新對話。固定的頭尾（機器標記、回覆規則、8 個段落標題、機器事實）以唯讀顯示在編輯框上下，並列出可用變數。
- 「儲存」與「還原預設」；內容等於預設時存成預設。超過 16 KiB、含控制字元、含 `[pdx-relay` 或無效字元時不能儲存並顯示原因。
- 未儲存的草稿在切換分頁或子頁後仍在；主機被移除或位址改變時清掉。換主機或換位址時會重新讀取，不會沿用上一台的內容。
- daemon 太舊（沒有這個功能）時顯示一行說明，自我接力開關照常可用。

### 追蹤
- #1919（通知點擊會作用在每個視窗）、#1902（register.js 拆分）。

## [1.0.0-alpha.601] - 2026-10-08

> 只動 SPA，透過 HMR 生效，daemon 和 Electron 都不必更新。

### Added：指揮台認得 peer 訊息喚醒的那一輪（peer mailbox P2，#1923）

- 執行體被 peer 訊息喚醒時，指揮台會開一個新回合，並把那則訊息畫成「peer 訊息」區塊：左邊一條資訊色線，標「來自 mlab/purdex-xx · 時間」，內文照 agent 文字排版。room 和 chat 兩種畫面都一樣，不會畫成你自己打字的泡泡。
- 你自己正在送出的訊息不受影響：peer 那一輪開始或結束，都不會解除你的送出鎖、也不會清掉你的待送泡泡。
- 方向鍵叫出的送出紀錄不會出現 peer 的文字；搜尋找得到 peer 訊息內文。
- 「由 peer 訊息喚醒」的收合列之後再做。

## [1.0.0-alpha.600] - 2026-10-08

> 只動 SPA，透過 HMR 生效，daemon 和 Electron 都不必更新。

### Fixed：peer 建立的 turn 也會刷新 worker 清單（#1866，#1932）

- Nexen v0.20.0 由 peer 建立 turn 時發的是 `peer_message`，不是 `execution.message_accepted`。alpha.587 的站台事件流白名單是照 v0.19.0 寫的，漏了它，清單因此不會因 peer 的 turn 刷新。現在補上了。

## [1.0.0-alpha.599] - 2026-10-08

> 只動 SPA，而且是純內部整理：使用者看不到任何變化，不用部署 daemon。

### Changed：桌面通知的程式拆成幾個獨立模組（#1930）

- 原本一個檔案同時負責「要不要發通知」、「去重」、「點通知後跳到哪」、「連線狀態通知」，現在各自一個模組；程式碼逐字搬移、行為不變，另補了點擊通知三種動作的契約測試。

## [1.0.0-alpha.598] - 2026-10-08

> 動 daemon，**需要部署新 binary 並重啟**（由統籌安排）。現有使用者沒有可見變化：目前的 SPA 連 host-events 時不帶 `nex=v1`，daemon 不會對它送出任何新事件，既有事件的行為也完全不變。SPA、`pdx` 指令、Electron 都沒有改動。

### Added：worker 狀態改由主機推送的第二步 — daemon 端推送有變動的那一列（#1866 PR1b：#1905、#1906、#1907、#1908、#1911、#1916）

- **推送**：daemon 監聽 Nexen 內部事件，某個 worker 的狀態、權限請求、工具進度、費用、封存等有變動時，合併 75–250 ms 後重讀那一列，經 `/ws/host-events` 推出 `nex.execution` 事件。每筆都帶連續序號，client 能察覺有沒有漏收。
- **只送給要的人**：只有連線時帶 `?nex=v1` 的 client 才收得到；舊版 SPA 與其他 client 完全看不到這些事件，也不會因為它們被斷線。
- **不會默默漏收**：帶 `nex=v1` 的連線，任何一則事件塞不進送出佇列就直接斷線，讓 client 重連後重新拿到完整狀態；它的佇列也加大到 1024 格，能吸收一次多個 worker 同時變動的突發。
- **連線時的基準點**：新連線一連上就收到 `nex.executions.hello`，告訴 client 從哪個序號開始接。
- **補抓靜默的狀態轉換**：Nexen 在 worker 跑完後轉成 idle 時不發事件（nexen#162），daemon 會在 +150 ms、+600 ms、+2 s 補讀，直到狀態不再是 running。
- **涵蓋 Nexen v0.20.0 的 `peer_message`**：由 peer 建立的 turn 也會觸發推送；另加測試，Nexen 日後新增的事件種類都必須先分類才能通過。

## [1.0.0-alpha.597] - 2026-10-08

> 動 daemon 與 SPA：**需要部署新 binary 並重啟 daemon**（SPA 部分走 HMR）。config.toml 的 `[nex]` 多一段 `[nex.peer]`，舊設定檔沒有這段時自動視為開啟；不改資料庫。

### Added：peer 信箱設定（peer mailbox P1，#1912）

- 內嵌 Nexen 的 peer 信箱改由 `[nex.peer]` 控制，**預設開啟**：`enabled`、`max_pending`（排隊上限，0＝Nexen 預設 32）、`wake_template`／`reply_line`（喚醒 prompt 樣板，空白＝Nexen 預設）。
- 樣板寫壞時，載入設定與從設定頁儲存時就會被擋下，不會等到下次重啟 daemon 才起不來。
- 主機 → Nex 設定頁新增「peer 信箱」開關與「排隊上限」；排隊上限只接受 0 以上的整數，不會再把 `1.5`、`1e3` 截成 1。兩個樣板不在畫面上編輯，但儲存時原樣保留。
- 從其他 client 或手寫 API 儲存時，沒帶到的 peer 欄位沿用目前的值；`"peer": null` 會被拒絕。
- 改了 `[nex.peer]` 會顯示「需要重啟」。
- 目前還沒有任何東西會送訊息進信箱；`pdx msg send` 送給執行體要等後續的 P4。

## [1.0.0-alpha.596] - 2026-10-08

> 動 daemon：**需要部署新 binary 並重啟 daemon** 才會生效（SPA 部分走 HMR）。不改資料庫、不改既有寫入規則；舊版 SPA 讀新 daemon 也沒問題（新增的欄位會被忽略）。

### Fixed：主機設定裡存壞的資料，不會再讓整台主機的設定載不出來，也不會擋住儲存（#1914）

- **之前**：主機設定裡只要有一筆資料被手動改成不合法的 JSON，整台主機的設定（專案、指令、快速回覆、Resume 範本、接力）都載不出來；格式看似正確、但違反規則的列（例如 slug 不合格、id 重複）會出現在清單裡，之後那個區塊每次儲存都失敗，直到找出那一列改掉。
- **現在**：daemon 讀設定時逐列檢查，跟儲存時用的是同一套規則；不合格的列不會送到畫面上，並附上數量與原因，畫面上顯示「此主機儲存的X有 N 筆格式錯誤，已略過不顯示；在這裡儲存變更時會一併移除」，而且儲存一定會成功、順便把那幾列清掉。其他區塊照常載入。
- 接力設定讀不了時，兩個開關一律顯示關閉（跟 daemon 實際行為一致）；只有某一段接力 prompt 壞掉時，只略過那一段，開關照常。

## [1.0.0-alpha.595] - 2026-10-08

> 只動 SPA：Mac App 從 dev server 載入的話 HMR 會直接生效，不用部署 daemon。

### Fixed：主機設定的資料格式壞掉時，主機頁不會再整頁當掉（#1901）

- **之前**：主機上存的專案、指令、快速回覆、Resume 範本或接力設定，只要有一筆被手動改壞（例如少了欄位、型別不對），打開主機頁就整頁當掉。
- **現在**：壞掉的那一筆會略過不顯示，整個集合格式錯誤就當作空白；該區塊會寫出「此主機儲存的X有 N 筆格式錯誤，已略過不顯示；在這裡儲存變更時會一併移除」。其他區塊與其他主機不受影響，也照樣可以編輯。
- **接力設定**壞掉時，兩個開關都顯示關閉，跟 daemon 實際的行為一致（daemon 讀不到就不允許自我接力），切換任一開關就會重新寫入。接力列上存的三段 prompt 內容，切換開關時會原樣保留。
- Resume 範本裡畫面上看不到、但 daemon 會拒收的項目（例如不合格的 agent 名稱）會被略過，不再讓每次儲存都失敗。
- 剛存好的設定不會再被稍早送出、較晚回來的重新整理蓋回舊版。

### 已知限制

- 型別正確、但不符合 daemon 規則的列（例如 slug 格式不對、id 重複）仍會顯示，儲存那個區塊時會被拒絕並指出是哪一列，要先改掉或刪除那一列。這部分由 daemon 端處理（#1889）。

## [1.0.0-alpha.594] - 2026-10-08

> 動接力 mod（embed 在 `pdx` 裡）：**需要部署新 binary 並重跑 `pdx setup --agent cc`**；之後新開的 session 才會用新的 mod。daemon 程式碼與 alpha.593 相同。使用者目前看不到變化：三段 prompt 都還是預設，設定頁的編輯框在 P9a-3。

### Changed：接力 prompt 在用到時才向 daemon 讀取 — P9a-2（#1903，spec U21）

- 撰寫接力檔、補齊接力檔、喚醒新對話三段 prompt 送出前，mod 各向 daemon 讀一次目前的內文（`pdx relay prompts`，最多等 8 秒），所以在主機上改了內文，下一次接力就生效，不必重跑 `pdx setup`。
- 機器標記、`HANDOFF-WRITTEN` 回覆規則、接力檔的 8 個段落標題與機器提供的事實由 mod 固定加上，內文怎麼改都不會少。
- 讀不到、逾時、daemon 太舊，或內文含 daemon 本來就會拒絕的字元時，改用內建預設，接力照常完成。
- 預設內文組出來的 prompt 與之前完全相同；只有「接力檔不完整」那段，把缺少的段落改成另起一行列出。

### 追蹤
- #1902（register.js 拆分）。

## [1.0.0-alpha.593] - 2026-10-08

> 動 daemon、`pdx` 指令與 SPA：**需要部署新 binary、重啟 daemon，並 fast-forward 主 checkout**（不需重跑 `pdx setup`：新的 `prompts.js` 在 P9a-2 之前沒有人使用）。使用者可見：在本視窗核准／拒絕之後會切到發起者的 tab。host config 的 `relay` 那一列可多存三段 prompt（`prompt_*`，未寫過就不出現）。⚠️ **回滾限制**：存過任何一段自訂 prompt 之後，若要把 daemon 換回這版以前的 binary，必須先在新版把 write／fix／seed 三段都按「還原預設」，否則舊版讀到不認得的欄位，self relay 會回 503。

### Added：接力 prompt 放進 daemon — P9a-1（#1890、#1891，spec U21）

- **host config `relay.prompt_write`／`prompt_fix`／`prompt_seed`**：撰寫接力檔、補齊接力檔、喚醒新對話三段 prompt 的可編輯內文。空白或未設定＝內建預設；超過 16 KiB、非 UTF-8、含控制字元（換行與 tab 除外）或含 `[pdx-relay` 會被拒（400）。只有 admin token 能寫。
- **`GET /api/relay/prompts`** 與 **`pdx relay prompts`**：回傳三段目前生效的內文、內建預設、固定的頭尾與可用變數。
- 內建預設與 mod 內的備用副本由同一份 Go 原始碼產生，測試釘住兩邊一致。mod 還不會讀取（P9a-2）。

### Added：核准後切到發起者的 tab — P9b-1（#1897、#1898，spec U22 (a)）

- 在這個視窗按下**核准**或**拒絕**（含斷線時排隊、重連後送出的決定）成功後，切到顯示發起者 tmux session 的 tab；沒有這樣的 tab 就開一個新 tab 接上，和從主機的 session 清單開啟一樣。lead 與 self relay 兩種申請都適用。
- 別的視窗或裝置做的決定、逾時、取消、發起者沒有 tmux，或主機在這個工作台被隱藏，都不會切換。
- 還有其他申請開著時，下一個對話框會留住鍵盤焦點：被切過去的終端機不會把焦點搶走（這也修好了對話框開著時點通知、按鍵卻打進終端機的情況）。

## [1.0.0-alpha.592] - 2026-10-08

> 動 daemon，**需要部署新 binary 並重啟**（由統籌安排）。現有使用者沒有可見變化：目前的 SPA 會忽略新加的欄位，也不會收到新的 503。SPA、`pdx` 指令、Electron 都沒有改動。

### Added：worker 狀態改由主機推送的第一步 — 讀取槽與版本化清單（#1866 PR1a：#1884、#1885、#1886、#1892）

為之後「daemon 直接推送有變動的那一列，不必整份重抓清單」打底。

- **讀取槽**：daemon 對 worker 清單的每一次讀取，都排進同一個槽依序進行；讀取成功才發一個遞增的版本號。之後 SPA 才能判斷 daemon 推來的列和自己抓的清單哪一個比較新。
- **清單帶版本**：`GET /api/nex/v1/executions` 的成功回應多一個 `pdx` 欄位（`epoch`、`ver`、`bseq`）。其他欄位原封不動。
- **槽太忙時**：等超過 2 秒，只有明確要求的 client（`?pdx=retry`，之後的 SPA 才會帶）會收到 503 `nex_busy`；其他 client 照樣拿到清單，只是不帶版本，並記一行 log。
- **出錯不蓋版本**：引擎處理清單時若 panic，一律回 500 `nex_list_panicked`，不會把這頁當成成功的版本化讀取。
- **單列讀取器**：daemon 可以在行程內讀單筆 execution，形狀和清單列一致；下一步（PR1b）的推送會用到。

## [1.0.0-alpha.591] - 2026-10-08

> 只動 SPA：Mac App 從 dev server 載入的話 HMR 會直接生效，不用部署 daemon。

### Fixed：分割分頁裡看著的 agent 不會再被標成未讀（#1887）

- **之前**：分割分頁的第二個（或更深的）窗格裡的 agent／worker，就算你正看著它，有事件時還是會被標上未讀紅點；切到這個分頁也不會自動清掉。只有主窗格會被當成「正在看」。
- **現在**：分頁裡任何一個窗格顯示的 agent 都算「正在看」，跟通知的判斷一致——看著時不會標未讀，切到分頁時分頁裡每個 agent 的未讀都會清掉；在已經開著的分頁裡新開一個 agent 窗格也會清掉它的未讀。
- 已結束的 tmux 窗格不算「正在看」（tmux 重啟後代號可能被新的 session 重用），跟通知一樣。
- 分頁標籤上的徽章仍然只反映主窗格。

## [1.0.0-alpha.590] - 2026-10-08

> 只動 SPA：Mac App 從 dev server 載入的話 HMR 會直接生效，不用部署 daemon。

### Fixed：背景 worker 的燈號與通知會更新了（#1873）

- **之前**：切到別的分頁後，worker 分頁的燈號會停在「執行中」，要切回去才更新；所以背景 worker 等待核准、完成或出錯時都不會跳通知。
- **現在**：worker 的狀態（燈號、等待核准的手掌、所有通知）一律只從主機的 worker 清單取，跟終端機的 agent 一樣；分頁開著或切走都一樣會更新。分頁內的即時串流只負責畫對話與核准請求卡。
- 正在看著該分頁時，燈號約慢 0.5 秒才變（清單更新的去抖時間加一次往返）。
- 同一個 worker 開在兩個窗格時，其中一個關掉或連線失敗，不會再讓另一個窗格斷線或被標成已關閉。
- 清單只拿到一部分（例如超過上限被截斷、有壞掉的列）時，不會把沒列到的 worker 當成已結束而清掉燈號。
- 一輪還在執行中、只有輪數往前時，不會再把你沒看過的未讀標記清掉。

### 已知限制（之後由 #1866 縮小）

- 在同一個去抖時間（約 0.5 秒）內先終止又封存的 worker，只會清掉燈號，不會跳「已終止」通知。
- 兩輪排隊之間 Nexen 短暫回到閒置時，若剛好在那一刻更新，可能多跳一次「完成」通知（Nexen #162）。
- 一整輪在同一個去抖時間內跑完，不會跳「完成」通知。
- 在同一個去抖時間內被別處回答的核准請求，不會顯示也不會通知。

## [1.0.0-alpha.589] - 2026-10-08

> 動 daemon、`pdx` 指令與接力 mod 的 skill：**需要部署新 binary、重啟 daemon，並重跑 `pdx setup --agent cc`**。team.db 新增 `spawn_ops` 表與 team 用量欄位（`CREATE TABLE IF NOT EXISTS`／`ALTER TABLE ADD COLUMN`，不改既有資料；舊版 binary 仍可讀）。使用者可見：lead 現在可以開、列、關 member。

### Added：lead / member / team 與 context 接力 — P4-4～P4-7（#1862、#1864、#1865、#1867、#1868、#1869、#1872、#1876、#1878、#1879）

- **`pdx spawn`**：lead 在 grant 允許的目錄開一個 member（新的 tmux session 跑 Claude Code，一律載入 Purdex mod）。
  - 可用 `--model`／`--effort` 指定模型（U20）；沒指定時會提醒「member 會用這台主機當下的預設模型」。
  - 成為 lead 時也會提醒依任務指定 member 的模型。
  - 開好後把 brief 從 lead 送給 member。
  - 超過 member 上限、目錄不在 grant 內、不是 lead 都會被拒。
  - member 20 秒內沒啟動就收掉並釋放名額。
  - daemon 重啟後會從記錄的那一步接續，同一個 member 不會開兩次。
- **`pdx team`**：列出自己 team 的 member，含 address、狀態、context %、實際的 model 與 effort。
- **`pdx kill`**：關掉自己 team 的 member；只動 Purdex 開的那個 tmux session（比對世代與所有權記號），接力中會拒絕。
- **member 離開後釋放名額**：daemon 確定 member 的程序已結束才標記為離開（無法確認時保留）。
- **安全**：member 啟動指令只接受單純的指令（`team.member_command`），每個參數重新加引號，model 一律單引號；目錄檢查在建立 session 前後各做一次。

### 追蹤
- #1856、#1857（存活判定的保守邊角）、#1845、#1848（分流的 daemon 端驗證與 begin 逾時邊角）。

## [1.0.0-alpha.588] - 2026-10-08

> 只動 daemon 的依賴（內嵌 Nexen），**需要部署新 binary 並重啟**才會生效；但這版本身沒有任何使用者可見的變化，可以跟下一個 daemon 版本一起部署。

### Changed：內嵌 Nexen 升到 v0.20.0（#1874）

- Nexen v0.20.0 新增「peer 信箱」：其他 session 可以把訊息送進 worker，閒置的 worker 會被喚醒處理。這個功能在 Nexen 預設關閉，Purdex 這版也沒有打開，行為與之前相同。
- v0.19.0 → v0.20.0 之間只有 peer 信箱這一項（nexen #158、#159）；資料庫 schema 沒有升版，不需要遷移。
- 接下來會加上 `[nex.peer]` 設定、`pdx msg send` 送給 worker 的路徑，以及 worker 畫面上 peer 訊息的顯示。

## [1.0.0-alpha.587] - 2026-10-08

> 只動 SPA，透過 HMR 生效，daemon 和 Electron 都不必更新。

### Fixed：有 worker 在輸出時，其他 worker 的狀態不再一直延後更新（#1866，#1870）

- 每台主機的 worker 清單靠一條站台事件流觸發重抓。這條流以前連 token 串流、原始對話內容都會送，每一筆都把 0.5 秒的重抓計時重新歸零；只要有一個 worker 在輸出，同一台主機上其他 worker 的狀態（等待核准、跑完、出錯）就一直等不到更新。
- 現在只訂閱會改到清單內容的事件（狀態、權限請求、工具與子任務進度、費用、封存、標題等），token 和原始內容不再觸發重抓。第一次連線也不再把所有歷史對話內容整段重播。
- 這是 #1866 的第一步；之後會改由 daemon 直接推送有變動的那一列，不必整份重抓。

## [1.0.0-alpha.586] - 2026-10-08

> 動 daemon 與 `pdx` 指令，**需要部署新 binary 並重啟**（不需要重跑 `pdx setup`，mod 沒有改動）。daemon 的 team.db 新增 `teams`、`team_members` 兩張表（`CREATE TABLE IF NOT EXISTS`，不改既有表；舊版 binary 會忽略它們）。使用者可見的變化：核准 lead 申請後真的建立 team；同一個 lead 不能再開第二個 team；team 的 member 不能自我接力。還不能 spawn member（P4-5 起）。

### Added：lead / member / team 與 context 接力 — P4-1～P4-3（#1849、#1854、#1855、#1859、#1860）

- **team 的建立與結束**：核准 lead 申請時，在同一筆交易裡建立 team（使用者在對話框改過的 member 上限與根目錄為準）。lead 的對話結束時 team 跟著結束；lead 自我接力時 team 跟著新的對話走，不會結束。daemon 重啟後 30 秒內不判斷，判斷 lead 是否還在一律以 lead 自己的程序為準，無法確認時保留 team。
- **角色規則**：已是 lead 的對話再申請 → `already_lead`；team 的 member 申請 lead → `member_cannot_lead`（申請時與核准時都檢查）。**member 不能自我接力**：member 的 relay hello 一律回 `self_relay=off`，申請一律回 `member_relay_is_leads`；已開著的自我接力申請，若對話在核准前成為 member，核准時會在同一筆交易裡取消。
- **`pdx lead request`**：核准後輸出包含 `team_id`。
- wire 契約（team、member、spawn、kill）與 U20 的 `--model`／`--effort` 驗證規則先行定義，供 P4-4 起的 spawn 使用。

### 追蹤
- #1856（殘留的壞 registry 檔可能讓 team 不結束）、#1857（其他路徑仍把無法確認當成不在）。

## [1.0.0-alpha.585] - 2026-10-08

> 只動 SPA，透過 HMR 生效，daemon 和 Electron 都不必更新。

### Fixed：分割分頁裡的 agent／worker 通知判斷正確（#1840，#1852）

- 以前通知只看分頁的主窗格：agent 或 worker 若放在分割分頁的**次要窗格**，App 在背景時不會通知（沒開「沒有分頁也通知」時）、你正看著那個分頁時反而會跳通知、點通知也找不到分頁。現在會看分頁裡的每個窗格。
- 點通知會切到那個分頁，並把鍵盤焦點直接放進被通知的窗格（終端機或 worker 輸入框），就算那個分頁本來就開著也一樣，不會把你接下來打的字送進別的窗格。
- 已經結束的終端機窗格不會再攔下新 session 的通知（tmux 重啟後 session 編號會重複使用）。
- 另開 #1853：未讀標記與切到分頁時的自動已讀，仍只看主窗格。

## [1.0.0-alpha.584] - 2026-10-08

> 動 `pdx` 指令與接力 mod。**部署要換 binary 並重跑 `pdx setup --agent cc`**（mlab 已全域安裝 mod）。daemon 沒有改動，但 binary 換了，照常重啟。Mac App 使用者沒有可見變化：Mac App 不畫選擇題卡片（spec U19(b)），終端機的原生對話框照常；能回答的遠端客戶端（iOS）上線後才看得到效果。

### Added：lead / member / team 與 context 接力 — P8a-2（#1843）

AskUserQuestion「分流」的 mod 端（spec §6.6；plan v2 Task 8a.9）。

- **終端機與遠端同時能答**：Claude Code 跳出選擇題時，終端機照常畫原生對話框；同時 mod 開一筆申請給連線中的客戶端。誰先答就用誰的；遠端先答時終端機的對話框立刻關閉並帶入答案，終端機先答時以終端機為準並通知 daemon。
- **任何狀況都不擋終端機**：daemon 不通、沒有客戶端、回應看不懂、mod 自己出錯，一律退回原生對話框；終端機答完最多再等 3 秒就繼續。
- **遠端答案要完整才收**：遠端答案必須剛好回答每一題，否則忽略、對話框照常。
- **`pdx ask report --detach`**：終端機先答而 daemon 一時沒回應時，回報交給獨立的背景行程送出，hook 結束後仍會送達。
- 已知限制：終端機先答且 daemon 超過 3 秒沒回應開題時，卡片可能與終端機答案不一致（#1848，iOS 開放回答前修）；daemon 端尚未驗證遠端答案與題目一致（#1845）。

### Changed：P4-0（#1846）

- `internal/module/team/relay_store.go` 依職責拆成五個檔（純搬移，逐宣告位元組比對證明，#1703）。

## [1.0.0-alpha.583] - 2026-10-08

> 只動 SPA，透過 HMR 生效，daemon 和 Electron 都不必更新。

### Fixed：窗格右鍵選單、路徑導覽彈窗、工作區右鍵選單不再被推進標題列（#1825，#1844）

- 接續 #1801：這三個浮層在視窗很矮或內容很高時，以前也可能被推到標題列那 36px 的拖曳區，按鈕點下去變成拖視窗。現在頂端最少停在標題列下緣，內容太高時改成可捲動；空間夠的一般情況位置不變。
- **工作區右鍵選單**以前完全不會避開畫面邊緣（開在右下角會超出畫面），現在會自動拉回畫面內。它的背景遮罩也設成不可拖曳：選單開著時點標題列會關掉選單（和點其他地方一樣），而不是拖動視窗。

## [1.0.0-alpha.582] - 2026-10-08

> 只動 SPA，透過 HMR 生效，daemon 和 Electron 都不必更新。

### Changed：worker 等待核准會跳桌面通知，規則和 agent 詢問一樣（#1839）

- 依你的新決定（原本「等待核准不推播」是設計錯誤）：worker 停下來等你核准時，和終端機裡的 Claude Code 問你權限一樣，**會跳桌面通知**。例外也完全一樣：App 在前景而且你正在看那個分頁時不跳；你在通知設定裡關掉「權限請求」時不跳（終端機的詢問和 worker 共用同一個開關）。
- 通知標題是 worker 的名稱，內文是「需要授權：<工具>」，點下去會開到那個 worker。
- **每個請求只通知一次**：同一個請求在重新連線、重新整理清單、切換分頁、重新整理頁面之後都不會再跳；同一段等待裡出現第二個新請求會再跳一次（就算兩個請求在同一毫秒建立）。App 關著時才來的請求，重開後通知一次；這台電腦從沒見過的 worker，第一次只記下現況、不跳（和 agent 詢問一樣）。
- 另開 #1840：分頁被分割時，次要窗格裡的 agent／worker 的通知判斷只看主窗格（所有 agent 類型本來就這樣，不是這次引入）。

## [1.0.0-alpha.581] - 2026-10-08

> 只動 SPA，透過 HMR 生效，daemon 和 Electron 都不必更新。

### Fixed：已封存的 worker 分頁重新整理後顯示正確標題（#1557，#1828）

- 以前一個 worker 被「接到終端機」封存後重新整理頁面，還沒點過的那個分頁標題會顯示「執行」，要點進去才變回正確名字。原因是封存的 worker 不在預設的 worker 清單裡，交接時又沒有任務說明，沒點開的分頁找不到任何標題來源。
- 現在畫面會替這種分頁各自補抓一次 worker 資訊（每台主機一次一個請求、每個 worker 只抓一次，失敗就維持原樣不重試），分頁不必點開就有正確標題，通知標題也一致。主機移除再加回、或改指向別台時，舊的回應會被丟掉，不會寫錯主機。
- 已知：補抓到的標題之後不會自動更新；長時間開關大量封存 worker 時，這些暫存不會回收（另開 #1831）。

## [1.0.0-alpha.580] - 2026-10-07

> 只動 daemon 原始碼的排版（gofmt）與 `make lint`，編出來的程式行為完全相同，**不需要另外部署或重啟**（579 本身要部署，照 579 的說明即可）。SPA、`pdx` 指令、Electron 都沒有改動。

### Changed：Go 原始碼排版整理，第二批，main 已全數 gofmt-clean（#1651，#1833）

- 剩下的 12 個 Go 檔（agent／nex／peers 模組）照 `gofmt` 重新排版；逐檔比對程式碼 token 與註解，與原本完全一致，沒有行為變化。
- `make lint` 會先檢查 `cmd/`、`internal/` 有沒有沒排版的 Go 檔，有就列出檔名並失敗，之後才跑 `go vet`。repo 沒有 CI 也沒有 pre-commit，這個檢查只在有人跑 `make lint` 時生效。

## [1.0.0-alpha.579] - 2026-10-07

> 動 daemon，**需要部署新 binary 並重啟**。沒有裝接力 mod 的 session 從這版起，跳出選擇題或權限詢問時，連線中的客戶端會看到一張「只能在終端機回答」的唯讀卡片；目前 Mac App 還不畫這種卡片（spec U19(b)），要等 iOS App 才看得到，所以 Mac App 使用者無可見變化。`pdx` 指令、SPA、Electron 都沒有改動。

### Added：lead / member / team 與 context 接力 — P8a-1d（#1827）

AskUserQuestion「分流」的終端機降級（spec §6.6；plan v2 Task 8a.8）。

- **只能在終端機回答的卡片**：沒有 mod 的 session 跳出選擇題或權限詢問時，daemon 開一筆唯讀申請，讓其他裝置知道終端機在等人；同一題、同一個權限詢問重複觸發不會多開。使用者在終端機回答後卡片顯示「已在終端機回答」並帶上答案；取消、工具失敗、送出新訊息、對話結束都會把卡片收掉。
- **不會影響原本的決策**：這條路徑永遠不產生決策、也不會讓 hook 失敗；被 lead 硬鎖的 session 不開卡片。
- **旗標清理**：卡片開著時 daemon 會留一個旗標，讓 `pdx hook` 只在需要時才多轉送事件；daemon 定期清掉沒有對應卡片的旗標（例如重啟後殘留的）。
- 已知限制：session 曾經載入過 mod、之後在同一 session 停用 mod 時不會開卡片（#1832）。

## [1.0.0-alpha.578] - 2026-10-07

> 只動 daemon 原始碼的排版（gofmt），編出來的程式行為完全相同，**不需要部署或重啟**。SPA、`pdx` 指令、Electron 都沒有改動。

### Changed：Go 原始碼排版整理，第一批（#1651，#1829）

- 15 個 Go 檔照 `gofmt` 重新排版；逐檔比對程式碼 token，與原本完全一致，沒有行為變化。
- `shellSingleQuote` 的註解改寫：gofmt 會把註解裡的 shell 跳脫寫法 `'\''` 改成彎引號，把範例改錯；改成程式碼區塊後就不受影響。
- 剩下的 12 個檔（agent／nex／peers 模組）與 `make lint` 的 gofmt 檢查在第二批。

## [1.0.0-alpha.577] - 2026-10-08

> 只動 SPA，透過 HMR 生效，daemon 和 Electron 都不必更新。

### Fixed：改名彈窗與分頁右鍵選單不再被推進標題列（#1801，#1824）

- 視窗很矮、或內容很高（例如有很多 pane 的改名彈窗、可合併很多分頁的右鍵選單）時，這兩個浮層以前會被推到離頂端只剩 4px，掉進標題列那 36px 的拖曳區，上面的按鈕點下去會變成拖視窗。現在它們的頂端最少停在標題列下緣；內容太高時改成可捲動，不會溢出畫面。空間夠的一般情況位置完全不變。
- 同類的另外三個浮層（窗格右鍵選單、路徑導覽彈窗、工作區右鍵選單）另開 #1825，之後處理。

## [1.0.0-alpha.576] - 2026-10-08

> 只動 SPA，透過 HMR 生效，daemon 和 Electron 都不必更新。

### Fixed：Electron preload 比畫面舊時不再丟分頁、不再讓瀏覽器窗格當掉（#1816，#1822）

Mac App 是從開發伺服器載入畫面，Electron 的 preload 可能比畫面舊（少了某些函式）：

- **移到新視窗**：preload 沒有這個功能時，**原分頁不會被關掉**，改為跳出提示「這個版本的 Purdex App 還不支援移到新視窗，請更新 App」（以前會先關掉分頁再出錯，分頁就沒了）。有這個功能時行為和以前完全一樣。
- **瀏覽器窗格**：preload 缺少對應功能時不再當掉；沒有 `openBrowserView` 時窗格會顯示說明與「回到新分頁」按鈕，而不是一片空白。
- 已知殘留（沒有變，不是這次引入）：移到新視窗的 IPC 本身在分頁關掉之後才丟錯時，分頁仍會遺失；`browser-cleanup`、工作區移到新視窗有同類的缺口，另行處理。

## [1.0.0-alpha.575] - 2026-10-08

> 只動測試，沒有任何使用者可見的變化，daemon、SPA 行為、Electron 都不必更新。

### Changed：純瀏覽器收斂批 4b — 清掉多餘的測試設定（#1818）

- 浮動面板（FloatingPanel）與選單（Menu）的測試，不再模擬平台模組，也不再重複跑「有／沒有 Electron」兩種情境（它們早就不讀平台旗標，兩種情境測的是同一件事）。每個測試要驗證的位置性質都保留，預期值沒有改。至此純瀏覽器收斂批 1–4 全部完成。

## [1.0.0-alpha.574] - 2026-10-08

> 只動 SPA，透過 HMR 生效，daemon 和 Electron 都不必更新。Mac App 上沒有可見的變化（順序與位置逐項不變）。

### Changed：純瀏覽器收斂批 4a — 拿掉「瀏覽器還是 Electron」的平台旗標（#1815）

- `lib/platform.ts` 原本有 8 個旗標，其中 6 個其實就是「有沒有 Electron」：`isElectron`、`canTearOffTab`、`canMergeWindow`（沒有人用）、`canBrowserPane`、`canSystemTray`、`canNotification`。現在都拿掉，使用點一律當作有 Electron。
- **保留**兩個真的能力偵測：`devUpdateEnabled`（preload 有 `getAppInfo`）和 `hasLocalFilesystem`（preload 有 `fs`）。原因：Mac App 是從開發伺服器載入畫面，Electron 的 preload 可能比畫面舊。`window.electronAPI?.` 的防呆也都沒動。
- Mac App 上：分頁右鍵選單的「移到新視窗」、設定裡 Electron（系統匣）區塊與它的順序、瀏覽器新分頁入口、桌面通知，都和以前一樣。
- 預期的副作用：在一般瀏覽器開這個畫面（例如做驗收）時，現在也會看到這些入口（移到新視窗、瀏覽器窗格、系統匣設定區塊）；它們的呼叫點都有 `window.electronAPI` 防呆，不會當掉。
- 另開 #1816：Electron preload 比畫面舊（缺 `tearOffTab`／`openBrowserView`）時，「移到新視窗」會先關掉原分頁再失敗、瀏覽器窗格會拋錯；這在本批之前就存在。

## [1.0.0-alpha.573] - 2026-10-08

> 只動 SPA，透過 HMR 生效，daemon 和 Electron 都不必更新。

### Fixed：「worker 已退出」提示的名稱與清單一致（#1788，#1813）

- 在終端機手動 resume 一個 worker 的對話，worker 被自動退出時跳出的提示，以前用另一套規則取名（對話標題優先、不看主機是否支援），同一個 worker 在提示和清單裡可能叫不同的名字。現在提示用和清單列相同的規則：任務說明第一行 → 對話標題（主機支援時）→ 工作目錄的最後一段；都沒有才退回 tmux session 名或執行 id。

## [1.0.0-alpha.572] - 2026-10-08

> 動 daemon，**需要部署新 binary 並重啟**（由統籌安排）。SPA 沒有改動。對正常使用沒有可見變化。

### Added：HTTP 伺服器的 header／閒置逾時，與上傳 body 的停滯偵測（#1523，#1808）

`http.Server` 原本完全沒有讀取逾時，任何通過驗證的連線都能用慢速 header 或慢速 body 長期佔住連線（slowloris）。實測後的做法：`ReadHeaderTimeout` 10 秒與 `IdleTimeout` 120 秒；**不**設全域 `ReadTimeout`／`WriteTimeout`（實測會誤砍慢網路上的大檔上傳，而 WebSocket 與 SSE 本來就不受 `ReadTimeout` 影響）。兩個上傳路由（`/api/agent/upload`、`/api/nex/executions/{id}/uploads`）改用「停滯偵測」：每次讀取前把讀取期限延長 30 秒，所以只要持續有資料就不會被砍（總時間不限），停滯超過 30 秒才中斷。上傳的累計配額與封存時的清理另開 #1806（design）。

## [1.0.0-alpha.571] - 2026-10-08

> 只動 SPA，透過 HMR 生效，daemon 和 Electron 都不必更新。

### Fixed：「只有圖示」樣式下，等待核准仍會顯示在分頁上（#1809）

- 依你的決定：分頁指示樣式設成「只有圖示」（關掉狀態燈）時，**正在等待核准的 worker 分頁仍然會顯示黃點加手掌圖示與「等待核准」提示**；它是必須顯示的例外，其他狀態（執行中、閒置、錯誤、一般等待）在這個樣式下照舊不顯示。

## [1.0.0-alpha.570] - 2026-10-07

> 只動 `pdx` 指令（CLI），daemon、SPA、Electron 都沒有改動，**不需要重啟 daemon**。新指令與轉送在 daemon 端接上（P8a-1d）、mod 接上（P8a-2）之前不會有可見變化。

### Added：lead / member / team 與 context 接力 — P8a-1c（#1807）

AskUserQuestion「分流」的指令端（spec §6.6；plan v2 Tasks 8a.6–8a.7）。

- **`pdx ask begin|wait|report`**：mod 用來開申請、等遠端答案、回報「終端機已回答」。`begin` 掉了回應重送時會拿回原本那一筆；daemon 回的 id 為空或 `wait` 回的狀態不認得時，一律當錯誤（exit 1），不會留下沒人能關的申請。
- **hook 有界轉送**：`pdx hook` 在同一個 5 秒預算內，把 AskUserQuestion／PermissionRequest 轉給 daemon（有旗標時也轉關閉事件）。任何出錯、旗標未開、daemon 不通都照常放行，不印任何東西、exit 永遠 0。旗標路徑只接受單一路徑元素，擋掉 `../` 別名。

## [1.0.0-alpha.569] - 2026-10-07

> 動 daemon，**需要部署新 binary 並重啟**（由統籌安排）。新路由還沒有 client 會呼叫（`pdx ask` 指令在 P8a-1c、mod 在 P8a-2），現有使用者無可見變化。`pdx` 指令、SPA、Electron 都沒有改動。

### Added：lead / member / team 與 context 接力 — P8a-1b（#1804）

AskUserQuestion「分流」的 daemon 路由（spec §6.6；plan v2 Task 8a.5）。

- **`/api/ask/begin`**：終端機跳出選擇題時，mod 開一筆申請讓其他裝置也能回答；沒有任何遠端可回答時什麼都不開；同一題重複開會拿回原本那一筆；若 daemon 先為它開了「只能在終端機回答」的那一筆，會在同一個交易裡換成可回答的一筆（任何一步失敗都完整復原）。
- **`/api/ask/wait`**：mod 等遠端答案（核准、或權限的拒絕都會帶回內容）；**`/api/ask/report`**：mod 回報「終端機已回答」或「已取消」。終端機的答案一律優先：遠端先答、終端機後答時，以終端機為準。回報必須帶上合法答案，避免一筆壞回報把遠端答案清掉。
- **遠端回答（decide）**：選擇題必須帶答案、權限可核准或拒絕；只能在終端機回答的那一種會被拒絕（「這題只能在終端機回答」）。
- 重啟確認的「進行中」不再把這類申請算進去。原本的 `/api/team/approvals/{id}` 與 mod 用的 `/api/relay/wait` 長輪詢行為不變。

## [1.0.0-alpha.568] - 2026-10-07

> 動 daemon 與 SPA，**需要部署新 binary 並重啟，並快轉主 checkout**（由統籌安排）。這版還不會開出任何新的申請，對現有使用者沒有可見變化；SPA 的變更最好與 daemon 同時或更早生效。`pdx` 指令、Electron 沒有改動。

### Added：lead / member / team 與 context 接力 — P8a-1a（#1799）

AskUserQuestion／權限詢問「分流」（U19，spec §6.6）的底層：之後 Claude Code 跳出選擇題或權限詢問時，終端機照常顯示原生對話框，其他裝置（之後的 iOS App）會收到一張事件卡，誰先回答就算誰的。這個 PR 只放契約，路由、CLI、mod 在後續 PR。

- **兩種新的申請類別**（AskUserQuestion、權限詢問）與三種結束狀態（已在終端機回答、終端機覆蓋了遠端答案、被取消），daemon 能儲存遠端給的答案。
- daemon 能判斷「現在有沒有遠端可以回答」（有連線中的 App／裝置），以及某個 session 有沒有載入 Purdex mod。
- **Mac App 不顯示這兩種卡片**（U19 (b)：終端機本身就是它的回答介面），但也不會因為清單裡出現它們就把整份申請清單（含 lead 申請）丟掉；遇到未來版本 daemon 的新類別，同樣只略過那一筆。

## [1.0.0-alpha.567] - 2026-10-08

> 只動 SPA，透過 HMR 生效，daemon 和 Electron 都不必更新。Mac App 上沒有可見的變化。

### Changed：純瀏覽器收斂批 3 — 標題列一律渲染（#1800）

- Purdex 之後只有 Mac App 與 iOS App，不再維護獨立的 Web 版。這一批把「沒有 Electron 時不畫標題列」的分支拿掉：`TitleBar` 一律渲染，浮動面板（FloatingPanel）與選單（Menu）一律避開標題列的高度。
- **預期的副作用**：在一般瀏覽器裡開這個 SPA（例如用 Playwright 做驗收），畫面上方現在也會有標題列；以前沒有。這是預期，不是壞掉。repo 裡沒有需要同步更新的 Playwright 腳本（驗收腳本都在各 session 的暫存目錄）。
- 沒動的：`lib/platform.ts` 的各項 can 旗標（批 4 另行決定）、`window.electronAPI?.` 的防呆。
- 另開 #1801：改名彈窗與分頁右鍵選單在視窗很矮時可能被推進標題列的拖曳區（Electron 下本來就有，不是這次引入）。

## [1.0.0-alpha.566] - 2026-10-08

> 只動 SPA，透過 HMR 生效，daemon 和 Electron 都不必更新。

### Changed：分頁上的「等待核准」改用手掌圖示（#1792）

- 依你的決定，worker 分頁的**標題不再帶「（等待核准）」文字**。分頁上改成：黃色狀態點照舊（位置、大小、外環都不變，其他分頁的指示不會跳動），旁邊多一個小手掌圖示，滑過去顯示「等待核准」提示。窗格標頭、側欄與新分頁的 Workers 清單的完整文字不變。
- 載入時就已經有待決請求的 worker 分頁，手掌一開始就會出現，不必等狀態同步。
- 等待核准時，分頁**仍會**有紅色未讀標記（它和黃點是各自獨立的狀態）。
- 已知：狀態指示樣式設成「只有圖示」時，分頁上不會顯示等待核准（窗格標頭與清單仍會）；這是否要當成例外，等你決定。

## [1.0.0-alpha.565] - 2026-10-07

> 只動 Purdex 的 Claude Code mod（隨 `pdx` 內嵌）與文件。**不需要部署動作**：mod 只有在主機上跑過 `pdx setup --agent cc` 才會被 Claude Code 載入；照使用者決定，mlab 何時安裝另行決定。至此自我接力（P5b）全部完成，已在拋棄式 session 端到端真機驗收三次。daemon、SPA、Electron 都沒有改動。

### Added：lead / member / team 與 context 接力 — P5b-3（#1780）

自我接力的最後一塊（spec §8.7、§10；plan v2 Task 5b.5）。

- **等待核准時暫停輸入**：接力申請送出中或等待核准時，你打的訊息（以及 peer、bridge、其他 plugin 送進來的）都會先等著，不會開始新的一輪。核准後放行，並附一段說明請模型「這一輪只做簡短回應，新工作寫進接力檔的下一步」；拒絕、逾時、取消或 daemon 不在時原樣放行。等待期間 mod 只在本機計時，不會對 daemon 多發請求；任何異常都會放行訊息，絕不丟掉。
- **壓縮**：已核准但還沒寫接力檔時，自動壓縮會被略過、改寫接力檔；你自己下的 `/compact` 照常執行。申請還開著時遇到壓縮，申請會被取消，所有裝置上的核准對話框一起關閉，等著的訊息放行。
- **`/relay off|on|status`**：暫停或恢復這個 session 的接力詢問、看目前狀態（主機開關、門檻）；member 會看到「member 的接力由 lead 安排」。
- **再問的規則**：拒絕後要再多用 10 個百分點才會再問；接手完成、`/relay on`、壓縮之後重新計算。
- **`pdx-team` skill**：教模型何時申請 lead、怎麼等核准、lead／member 怎麼做、自我接力是怎麼回事。
- **驗收食譜** `docs/testing/self-relay-acceptance.md`：在拋棄式 session 用 `--plugin-dir` 載入 mod 的 10 步檢查，不碰主機設定。
- 內嵌 mod 時會濾掉 Claude Code 在 plugin 目錄裡產生的型別檔，避免被打包進 `pdx`。

Review 後補強（R1 一條、攻擊方五條含一條 critical、critic 兩輪）全部收進上面的行為。

## [1.0.0-alpha.564] - 2026-10-08

> 動 daemon，**需要部署新 binary 並重啟**（由統籌安排）。只多日誌，行為不變。SPA 沒有改動。

### Added：關機耗時日誌與 tmux／ps fork 計數器（#1767，#1791）

重啟「請求到回來」比新 image 的 `startup: ready` 長很多（舊 image 關機約 8 秒），但日誌看不出時間花在哪。現在關機會多這幾行：`shutdown: stop N modules in …`／`shutdown: close N modules in …`（各模組耗時）、`shutdown: in-flight requests: N […]` 與 HTTP `Shutdown` 之後剩幾條（只含方法與路徑前兩段，不含 query 或 token）、`shutdown: stop-modules=… http-shutdown=… serve-return=… close-modules=… total=…`（`Shutdown` 超時強關時有 `http-forced-close` 標記）、restart 路徑 exec 之前的 `shutdown: done, restarting after …`。另外 `internal/execstat` 以原子計數記 tmux 與 ps 的 fork 次數與總耗時（不記指令參數），顯示在 `[agent] start exec:` 與 `startup: ready` 行尾，用來判斷 `sweepOnce`／`replayStatus` 剩下的時間是不是 fork。沒有改任何 timeout、順序或行為。

## [1.0.0-alpha.563] - 2026-10-08

> 只動 SPA，透過 HMR 生效，daemon 和 Electron 都不必更新。

### Fixed：交接來的 worker 在清單裡有名字了（#1771，#1786）

- 活動列、新分頁的 Workers 清單、設定 → Worker 的列：以前交接來的 worker 沒有任務說明（brief），整列只有圓點和圖示，同時有好幾個在等待核准時分不出誰是誰。現在沒有 brief 時，改顯示這個對話的標題（主機支援時），再沒有就顯示工作目錄的最後一段；**已經有 brief 的列完全不變**。
- 搜尋也找得到列上顯示的名稱（例如用對話標題搜尋）；舊版 daemon 殘留的標題不會被搜到。
- 另開 #1788：手動 resume 造成的「worker 已退出」提示用的名稱規則和列不同，之後再統一。

## [1.0.0-alpha.562] - 2026-10-08

> 動 daemon，**需要部署新 binary 並重啟**（由統籌安排）。沒有使用者看得到的變化；重啟的瞬間不再有 hook 請求 panic。SPA 沒有改動。

### Fixed：關機時 hook 事件撞到已關閉的 trace sink 而 panic（#1189，#1785）

agent 模組在 `Stop` 就關掉 trace sink，但 HTTP server 還在處理請求，這段窗口內結束的 hook 請求會對已關閉的 channel 送值而 panic（日誌出現 `send on closed channel` 堆疊，該請求失敗）。現在 agent 實作 `core.Closer`，在 HTTP 請求都處理完之後（`CloseModules`）才關 sink，一般關機不再丟 trace；sink 本身也防護成「已關閉就丟棄並計數」（`Shutdown` 超時強關連線的極端情況），日誌只有一行 `sink closed: dropping trace records` 與累計到 10、100… 時的 `dropped N trace record(s) since close`，hook 事件本身的處理不受影響。另外修了 `FlushForTest` 與併發入列的 WaitGroup 誤用，並移除一個測試裡迴避同一個 panic 的 sleep。

## [1.0.0-alpha.561] - 2026-10-08

> 只動 SPA，透過 HMR 生效，daemon 和 Electron 都不必更新。這一版是權限通道在 mlab 真機驗收後的修正。

### Fixed：權限通道真機驗收找到的問題（#1779）

- **等待核准時可以從窗格退出**：以前只要是從這個窗格送出訊息、那一輪還沒結束，窗格標頭的「退出」（和選單裡的）都是灰的，等待核准的期間也包含在內，只能去 Workers 清單那一列退出。現在只有訊息還在送出途中才會暫時不能按。
- **交接確認框的說明跟著所選的模式走**：以前選了「需要核准」，內文還是寫「不會再有權限詢問」；現在會說明 worker 會停下來等你同意或拒絕。
- **新分頁不再提供「需要核准」的 Sandbox profile**：這個模式只在交接時使用。若主機只提供這類 profile，新分頁會顯示「這台主機沒有可用的 Sandbox profile」並停用送出。
- **請求卡的拒絕說明在切換分頁後保留**：打到一半的說明、展開的狀態、「已在這個窗格回答」都會跨分頁切換保留；回答之後切走再切回，已處理的卡片不會再冒出來。
- 文件：驗收範例改用非唯讀指令（唯讀指令如 `echo`、`date` 在「需要核准」模式下 Claude Code 會自己放行，不會詢問）。
- 另開 #1771：交接來的 worker 在清單裡沒有名字。

## [1.0.0-alpha.560] - 2026-10-08

> 動 daemon，**需要部署新 binary 並重啟**（由統籌安排）。這次重啟起，啟動預期再快約 5 秒（`[agent] start:` 的 `replayStatus=` 由約 5600 ms 降到 500 ms 以內）。SPA 沒有改動。

### Changed：daemon 啟動時 `replayStatus` 不再為每個 session 重建全部投影（#1767，#1781）

alpha.559 的耗時日誌指出剩下的啟動時間幾乎都在 agent 模組的 `replayStatus`（5618 ms／6727 ms）：它對每個 session 都重新讀全部 frame 並為每個 pane 各 fork 一次 `tmux display-message`（還順便付了一個被丟棄的 session code 查詢）。現在這一輪只載入投影一次、每個 pane 最多解析一次（快取只活在這一輪，失敗不快取）；武裝 probe intent 之前會重讀目標 frame 的 DB row，已被 sweep 刪除的 frame 不會被武裝。另外 `selectSessionProjection` 不再為被丟棄的 session code 付 `resolveSessionCode`。熱路徑（每個 hook 事件約 P 次 tmux fork）另開 #1777；一個既有的「frame 在讀取與武裝之間被刪」窗口另開 #1782。

## [1.0.0-alpha.559] - 2026-10-08

> 動 daemon，**需要部署新 binary 並重啟**（由統籌安排）。只多了日誌，行為不變。SPA 沒有改動。

### Added：daemon 啟動耗時日誌（#1767，#1774）

重啟變慢時日誌看不出時間花在哪。現在每次啟動會多這幾行：`startup: init N modules in Xms: …` 與 `startup: start N modules in Xms: …`（各模組耗時由大到小，小於 5 ms 併成 `others`；單一模組 ≥ 1 秒另有 `slow module` 警示；啟動失敗時也會先記出已完成模組的耗時）、`[agent] start: sweepOnce=… replayFromDB=… startSweep=… replayStatus=…`、`[peers] start: sweep=…`、`session: start: …`，以及 `pdx daemon listening` 之前的 `startup: ready in Xms`。用來定位 #1767 剩下的 5–13 秒（agent 模組啟動）。日誌只含模組名稱與毫秒數。

## [1.0.0-alpha.558] - 2026-10-08

> 動 daemon，**需要部署新 binary 並重啟**（由統籌安排）。這次重啟起，啟動會比近期快 8 秒左右。SPA 沒有改動。

### Fixed：daemon 啟動被兩個殭屍 helper 拖慢 8 秒（#1767，#1769）

peers 模組啟動時會清理上一個 image 留下的 helper 行程。若 helper 是在 exec 重啟邊界之後才結束，它會成為 daemon 的殭屍子行程：`kill(pid, 0)` 仍成功、SIGTERM／SIGKILL 無效，於是每次啟動都空等約 4 秒（每筆），紀錄還永遠清不掉，重啟時間因此從約 5 秒一路拖到 16 秒以上。現在在確認它確實是本 daemon 的殭屍（狀態 Z、父行程是本 daemon、200 ms 後仍是 Z、身分相符）之後直接回收，不再送訊號與等待；任何一項不成立都退回原本的 SIGTERM 流程。同時 `Sweep` 加上單次保護：並行的第二次呼叫會等待並共用第一次的結果，不會重跑，也不會把失敗讀成成功。回收時日誌會有 `peers: sweep: reaped zombie pid …`。

## [1.0.0-alpha.557] - 2026-10-07

> 只動 Purdex 的 Claude Code mod（隨 `pdx` 內嵌）。**不需要任何部署動作**：mod 只有在主機上跑過 `pdx setup --agent cc` 才會被 Claude Code 載入，而這要等 P5b-3 完成後整個接力功能一起驗收再裝。daemon、SPA、Electron 都沒有改動。

### Added：lead / member / team 與 context 接力 — P5b-2（#1763）

mod 的自我接力核心（spec §8.1–§8.3、§8.7、§15「Mod」；plan v2 Task 5b.4）。

- **何時問**：每個回合結束時，若 context 已用到門檻（daemon 設定，預設 70%）、自上次接手後又長了一定量、且比上次詢問多 10 個百分點，就向 daemon 發起接力申請；要等 mod 向 daemon 報到成功、拿到門檻後才會問。member、或 host／session 關掉接力時不問；`claude -p` 永遠不問。
- **核准後**：送出一段接力 prompt，請模型照 8 個段落寫接力檔；寫完由 mod 檢查，缺段落或太短最多再請它補兩輪；通過後 mod 自動 `/clear`，把接力檔帶進新 session（「↪ 接手自 <舊 ref>」），完成後回報 daemon。
- **只放行那一個檔案**：接力期間，mod 只允許 Write／Edit 這次接力的那個接力檔路徑。
- **不會卡住 Claude**：所有要等 daemon 的動作都從背景計時器送出；任何一步失敗（送不出 prompt、`/clear` 失敗、帶入失敗）都會回報並回到平常狀態，帶入失敗時會提示接力檔在哪裡。使用者中途自己 `/clear` 時，mod 會回報取消或失敗。
- **認得自己的回合**：每次送出接力 prompt 都帶一個隨機記號（來自系統的密碼學亂數），只在對應階段接受一次，使用者或其他 plugin 打出相同文字也不會被誤認。
- **回報可靠送達**：回報依接力順序排隊；daemon 暫時不在或暫時錯誤時下一個回合重送（每筆最多 20 次、最多排 50 筆），不會讓「完成」比「已清空」先到而被拒絕。

**還沒有的**：等待核准時暫停使用者輸入、核准後的說明、壓縮時的處理、`/relay` 指令——P5b-3。

Review 後補強（R1 兩條、攻擊方五條、critic 三輪）全部收進上面的行為。

## [1.0.0-alpha.556] - 2026-10-08

> 只動 SPA，透過 HMR 生效，daemon 和 Electron 都不必更新。

### Fixed／Added：Worker 回覆框（#1765）

- 輸入法組字中按 Enter 不再誤送出；中斷快捷鍵修正。
- 一輪結束、出錯、被孤立或撤回時，回覆框會正確解鎖。
- 回覆框支援輸入歷史（方向鍵叫回先前送出的內容）。
- 切換分頁後，尚未送出的草稿會保留。
- 不合適的「思考中」跳動點已隱藏。
- 尚未完成：對等端 ID 片段只在 daemon 提供 `peer_address` 時顯示；拖放問題根因尚未重現（僅做防護）。

## [1.0.0-alpha.555] - 2026-10-08

> 只動 SPA，透過 HMR 生效，daemon 和 Electron 都不必更新。**需要 daemon ≥ alpha.552**（已部署在 mlab）；更舊的 daemon 上一切照舊，只是「測試用」分頁會說明此主機尚未支援。

### Added：Worker「測試用」分頁（#1762）

- **設定 → Worker 新增「測試用」分頁**：cwd 在 `/private/tmp`（含 `/tmp`）底下的對話放這裡，分「執行中」「已退出」「已不見」三段，一個搜尋框；動作與一般分頁相同。這些對話不再出現在 Workers、已退出、Gone 三個一般分頁；New Tab 的 Workers 與活動列不受影響。
- 已退出／Gone 改以 `scope=normal` 讀清單，測試用分頁以 `scope=test`。主機的 daemon 沒有 `conversations.scope.v1` 時，三個一般分頁行為完全不變。
- 標題來源多了 `registry` 的型別（daemon 端的 registry 名稱退路，alpha.552 起；歷史對話無法回填）。

## [1.0.0-alpha.554] - 2026-10-08

> 只動 SPA，透過 HMR 生效，daemon 和 Electron 都不必更新。**需要 daemon ≥ alpha.541**（已部署在 mlab）。權限通道（「需要核准」）的畫面到這版齊全。

### Added：「等待核准」狀態（權限通道 P-2b-2，#1756）

- worker 正在等你核准時，這個狀態會顯示在：**活動列與新分頁的 Workers 清單、設定 → Worker 的列**（警告色圓點加一個小手掌圖示，提示文字「等待核准」，和「排隊中」的純黃點區分）、**窗格標頭**（狀態文字改成「等待核准」）、**分頁標籤**（標題後面加「（等待核准）」）。
- 不需要開著那個 worker 的分頁：清單會隨主機的事件自動更新。
- 已經結束（終止、封存、被拒、失敗）的 worker 不會顯示「等待核准」；每一輪結束時，還沒回答的請求也會一併結清，請求卡與控制權保持都會放掉，不會卡住。
- **不會推播通知**（PC2）：等待核准只呈現在標籤、圓點與未讀標記上，不會跳出桌面通知。
- 沒有這個欄位的舊 daemon 不受影響。

## [1.0.0-alpha.553] - 2026-10-07

> 動 `pdx` 指令與 daemon，**需要部署新 binary 並重啟**（由統籌安排）。部署本身**不會**改 `~/.claude/settings.json`；要讓 mod 生效，需在該主機跑一次 `pdx setup --agent cc`（或 Hosts 頁 CC hooks 卡片的 Install）。之後每次部署若 mod 有改動也要再跑一次。SPA、Electron 都沒有改動。

### Added：lead / member / team 與 context 接力 — P5b-1（#1752）

Purdex 的 Claude Code mod 隨 `pdx` 出貨（spec §5「Shipping」、§8.3；plan v2 Tasks 5b.1–5b.3）。這一版的 mod 只做一件事：向 daemon 報到。

- **mod 內嵌在 `pdx` 裡**，`pdx setup --agent cc` 會把它解壓到 `<data_dir>/cc-plugin/purdex/`，並加進 `~/.claude/settings.json` 的 `env.CLAUDE_CODE_PLUGIN_DIRS`（附加在你原有的清單後面、重複安裝不會重複、移除時只拿掉 Purdex 的那一項）；`pdx setup --agent cc --remove` 全部拿掉。
- **mod 的行為**：互動式 `claude` 開啟時與每次 `/clear` 之後，呼叫 `pdx relay hello` 告訴 daemon「這個 session 有 mod」；`claude -p`（Nexen worker）什麼都不做。報到從背景計時器送出，daemon 不在或重啟中時不會讓 session 啟動卡住。mod 會帶上安裝它的那個 daemon 的 config，不會打錯 daemon。
- **安裝狀態**：Hosts 頁的 CC hooks 卡片現在會檢查 mod 是否已安裝且是目前版本；從舊版升級後會顯示「未安裝」，按 Install 即可。
- **安裝的安全性**：解壓先寫到暫存目錄、舊版本先備份，就位失敗會還原；跨程序加鎖，daemon 與 `pdx setup` 同時跑不會互相刪檔；同版本時若檔案缺失或被改動會自動修復；`settings.json` 寫入失敗時還原成安裝前的樣子。

Review 後補強（R1 兩條、攻擊方六條、critic 兩輪）全部收進上面的行為。

## [1.0.0-alpha.552] - 2026-10-07

> 動 daemon，**需要部署新 binary 並重啟**（由統籌安排）。SPA 沒有改動；使用者看得到的變化要等 PR-2（SPA）：本版只是 daemon 端先備好。

### Added：Worker「測試用」分頁與 registry 名稱標題退路 — PR-1 daemon（#1757）

- **`?scope=test|normal|all`**：`GET /api/nex/conversations` 多一個過濾參數（預設 `all`＝舊行為）。測試用＝cwd 在 `/private/tmp`（含 `/tmp`）底下。過濾只作用在回應的副本，快取的 snapshot 不變；`total`、`truncated`、`unknown_owner` 以過濾後為準。非法值回 400 `bad_scope`。`/api/info` 的 capabilities 多了 `conversations.scope.v1`。
- **registry 名稱當標題退路**：活著的對話，peers 每個 inventory 週期把 registry 的 `name` 記到新表 `conversation_names`（名字改變或超過 1 小時才寫；寫入失敗不影響清單、下輪重試；proxy／helper entry 不記）。對話結束後，標題順序變成 custom → ai → nexen → **registry（新，`title_source: "registry"`）** → prompt 首行 → session id 前 8 碼。名稱表不清除（索引本來就沒有保留期）。
- 已知限制：沒被 daemon 看過活著的歷史對話無法回填名字；daemon 重啟後若同一 session 同時有多個不同名字的 live entry，依規則而非新舊選一個（僅顯示用）。

## [1.0.0-alpha.551] - 2026-10-08

> 只動 SPA，透過 HMR 生效，daemon 和 Electron 都不必更新。**需要 daemon ≥ alpha.541**（已部署在 mlab）。

### Added：在 worker 分頁回答「需要核准」的請求（權限通道 P-2b-1，#1741）

- 用「需要核准」模式交接的 worker，做有風險的事之前會停下來；worker 分頁在輸入框上方出現**請求卡**：工具名稱與說明、指令或輸入的預覽（可展開）、為什麼要問、等了多久；若是背景子代理在問，會標出是哪個。
- **同意** 直接放行請求本身的內容；**拒絕** 可附一行說明（最多 2048 位元組），worker 看得到這句話並自己決定怎麼改。
- 回答走這個分頁的控制權；等待核准期間控制權會一直保持，不會因為閒置而掉；被別的分頁搶走時，下次回答會自動重新取得。有人按到別人已經處理過的請求（已被回答、取消、逾時、worker 已退出），卡片會安靜消失，不顯示錯誤。
- 正在退出、接到終端機或接回時，同意與拒絕會暫時停用。
- 逾時自動拒絕的請求，會留下一行淡色「已逾時自動拒絕（N 分鐘）」；之後另一筆請求被處理或有新請求時才會消失。
- 內部：權限事件以 request id 對應，不依序號推論先後；不會混進對話紀錄，並會讓分頁摘要重新讀取。

## [1.0.0-alpha.550] - 2026-10-07

> 只動 SPA（快轉主 checkout 即可），daemon 不用重啟。Electron 行為不變。

### Changed：SPA 純瀏覽器路徑收斂 — 第 2 批（#1746）

移除通知派送在沒有 Electron 時退回 Web Notification 的兩處 fallback（`useNotificationDispatcher`），以及開發環境頁「重新載入 SPA」按鈕的 `location.reload()` fallback（該區段只在 Electron 才註冊）。

## [1.0.0-alpha.549] - 2026-10-07

> 純 SPA：主 checkout 快轉後 dev server 立即生效（Mac App 走 dev server 時自動帶到）；daemon、`pdx` 指令、Electron 都沒有改動，不需要重啟。

### Added：lead / member / team 與 context 接力 — P5a-3b（#1742）

接力的介面（spec §8.7、§6.7；plan v2 Tasks 5a.12–5a.14）。至此 P5a（daemon 接力核心）全部完成。

- **Hosts › 接力**：每台主機兩個開關——「solo 的 session 自己接力」與「lead 自己接力」，預設都開；下方註明「member 的接力一律由 lead 安排」。daemon 太舊時顯示不支援、離線時鎖住；連點兩下會正確地切回原值。
- **接力申請對話框**：session 用量到門檻時跳出，顯示主機、session、位址、ref、工作目錄、「已用 72%」與說明「核准後這個 session 會寫接力檔、清空並在原處接手（約 1 分鐘）」，一鍵核准／拒絕（不像 lead 申請要選人數與目錄）。
- **「這個 session 不再詢問」**：勾選後在送出決定前先暫停這個 session 的接力詢問；暫停失敗會跳提示、決定照送。daemon 重啟中（斷線）時，決定連同暫停一起排隊，重連後先暫停再送決定。
- **通知標題**：「mlab：purdex-tester 申請接力（已用 72%）」。

Review 後補強：斷線時暫停隨決定排隊、開關連點（R1）；重連時暫停失敗不再靜默、離線連點不覆寫先按的決定（攻擊方）。

## [1.0.0-alpha.548] - 2026-10-07

> 只動 SPA（快轉主 checkout 即可），daemon 不用重啟。Electron 行為不變。

### Changed：SPA 純瀏覽器路徑收斂 — 第 1 批（#1744）

產品只有 Mac App 與 iOS App，一般瀏覽器不是目標（見 project_product_platforms）。移除兩處純瀏覽器 fallback：終端機連結開啟器非 Electron 時的 `window.open`（連同 `isElectron` 參數），以及 BrowserPane 沒有 Electron 時顯示的「需要桌面版本」placeholder。其餘 `window.electronAPI` 守衛是 jsdom／Playwright 驗收的安全網，刻意不動。

## [1.0.0-alpha.547] - 2026-10-07

> 動 daemon，**需要部署新 binary 並重啟**（由統籌安排）。沒有使用者看得到的變化。

### Fixed：daemon 重啟 Phase A 的幾個小後續（#1569，#1740）

- boot id 的時間回退值補零到 16 碼（`crypto/rand` 失敗才會走到，實務上不會）。
- 關機流程若 panic，不再留下偵測第二個訊號的 goroutine；它在 panic 路徑最多等 1 秒收尾，且 `done` 關閉後絕不會再呼叫 exit。
- 重啟整合測試改為剔除所有繼承來的 `PDX_*` 環境變數。
- #1569 其餘項目（free-port TOCTOU、boot-plan 捕捉位置的測試、`signal.Stop` 順序）仍開著。

## [1.0.0-alpha.546] - 2026-10-07

> 只動 SPA（快轉主 checkout 即可），daemon 不用重啟。

### Fixed：Nex 管理表的未歸檔模式一次渲染整個清單（#1593，#1737）

共享清單最多 10,000 列，表格原本全部渲染。現在先顯示 100 列，按「顯示更多」每次再加 100；切換主機或「顯示已歸檔」會回到 100 列，並一併清掉打開中的「確認終止」，避免它在列重新出現時復活。

## [1.0.0-alpha.545] - 2026-10-07

> 動 daemon 與 `pdx` 指令，**需要部署新 binary 並重啟**（由統籌安排）。SPA、Electron 都沒有改動。

### Added：lead / member / team 與 context 接力 — P5a-3a（#1733）

接力檔的清理與 `pdx peers` 的舊 ref 顯示（spec §8.3「Retention」、§8.4；plan v2 Tasks 5a.10–5a.11）。

- **接力檔自動清理**：daemon 啟動時與每小時掃一次 `<data_dir>/relay/`。每條接力鏈只留最新 3 份、完成超過 14 天的刪、失敗或取消的留 3 天方便除錯；刪掉的 op 在資料庫標成 pruned（路徑保留）。
- **只清已結束的 op**：還在進行中的接力（等核准到 `cleared`）不論多久都保留檔案——那是接力內容的唯一副本，`/clear` 之後還要拿來帶入新 session。行程中途死掉而卡在 `claimed`／`writing`／`written` 的 op，要等 P6 的重啟收斂把它結束後才會清（#1735）。
- **只刪 `<data_dir>/relay/<op id>.md`**：op id 必須是單一路徑元素、relay 目錄必須是真目錄（不是 symlink）、目標必須是一般檔或 symlink（只解除連結、不跟隨）；其餘一律不碰，只標 pruned 或留待下次。
- **`pdx peers`**：接力過的對話在位址後面顯示 `(was _xxxxxx)`（最新的那個舊 ref）。

Review 後補強：路徑穿越與 symlink 目錄、進行中 op 的檔案保護（攻擊方）；spec §8.3 措辭不再宣稱卡住的 op 已都會被結束（critic）。

## [1.0.0-alpha.544] - 2026-10-08

> 只動 SPA，透過 HMR 生效，daemon 和 Electron 都不必更新。**需要 daemon ≥ alpha.537**（已部署在 mlab）。

### Added：交接時可選「需要核准」（權限通道 P-2a，#1732）

- **交接確認框**多一個選擇：**完全放行（預設）** 或 **需要核准**。選「需要核准」時，worker 做有風險的事之前會停下來等你回答（回答的介面在下一版）。只有主機支援時才會出現這個選項；完全放行送出的請求和以前一樣。
- 主機拒絕「需要核准」的交接時，會顯示它給的原因，**不會**自動改成完全放行再試一次。
- **設定 → Worker** 新增「等待核准逾時」（不逾時、5、15、30、60 分鐘）：多久沒回答就自動拒絕。只影響之後的交接，只存在這台裝置、不會同步到別台；只有主機支援時才顯示。若所選時間超過主機允許的上限，交接或重建會在送出前直接拒絕並告訴你上限。
- 「重建 worker」會沿用原本的模式：原本是「需要核准」的，重建後仍是。
- 內部：新增讀取主機權限能力與送出核准回答的程式（回答要從能力裡宣告的路徑送）。

## [1.0.0-alpha.543] - 2026-10-07

> 動 `pdx` 指令與 daemon（begin 的 `request_id` 重送），**需要部署新 binary 並重啟**（由統籌安排）。mod 在 P5b 才會呼叫這些指令，現有使用者流程不受影響。SPA、Electron 都沒有改動。

### Added：lead / member / team 與 context 接力 — P5a-2c（#1726）

mod 要用的 `pdx relay` 指令（spec §8.3、§14；plan v2 Task 5a.9）。

- **`pdx relay hello | begin | wait | self | report | op`**：跑在會等 daemon 重啟的 client 上，exit code 照 spec §14（核准 0、拒絕 10、逾時 11、取消／中斷 12、被規則拒絕 13、daemon 不在 20／送出沒回應 21、用法錯 2）。
- **`wait`** 每輪長輪詢續租；`--wait` 用完申請還開著 → exit 0 並印出 `state:"open"` 的核准單（mod 看到就再呼叫一次）；到期那一刻剛好核准下來的話，會多讀一次拿到真實結果，不會因為時間差印成「還開著」；daemon 三次沒回應 → 20。
- **被規則拒絕時** 把 409 的代碼印在 stderr 最後一個字（mod 直接讀）；`relay_open`／`bad_transition` 把 daemon 回的 op 印在 stdout。
- **`begin` 可以安全重送**：CLI 先產生一個 request id 帶上去，daemon 看到同一個 id（同一 session、同樣的用量與 window）就回原來那個 op，不會因為回應在半路掉了而開第二筆申請；不同 payload 拿舊 id 來用是 409。
- **`report claimed`** 在本地就擋（用法錯）；`cleared` 時 daemon 若還沒看到新 session 回 503，CLI 回 20 讓 mod 下個回合重送。
- spec §14 的 exit 13 清單補上 `relay_open`、`bad_transition`。

Review 後補強：`--window` 必填、`--used` 拒 NaN／Inf（R1）；request id 重送與到期短讀（攻擊方）；重送要同 payload（critic）。`cmd/pdx/relay.go` 的職責拆分列 #1730。

## [1.0.0-alpha.542] - 2026-10-08

> 只動 daemon，**需要部署新 binary 並重啟 daemon**，由統籌安排。SPA、資料庫、Electron 都沒有改動，桌機上沒有使用者可見的變化。修正 alpha.540 的終端機鏡像連線（purdex-ios 實測回報）。

### Fixed：鏡像連線是唯一 client 時仍會縮小桌機的 window（#1725）

- alpha.540 的 `?mirror=1` 以 `-f ignore-size` attach，但那只在**還有其他 client** 時有效；手機是唯一 client 時（桌機沒開著這個 session），tmux 仍以手機的大小決定 window，window 被縮成手機的大小（實測 150x44 的 window 變成 83x55），手機斷線後也不會復原。我先前只在有桌機 client 的情況下驗證，漏了這種情況。
- 現在鏡像連線的 PTY 與 window 綁定：連線前先查 window 的大小與 status bar 的行數，PTY 以「window 寬 × (window 高 ＋ status 行數)」啟動——光是同高還不夠，tmux 會把 client 的 status 那一行從 window 扣掉（150x44 的 client 會讓 window 變 150x43）。之後每秒同步，window 或 status 變了就跟著調整。
- 鏡像連線送來的 `resize` 一律忽略，不再改任何大小。查不到 window 大小時拒絕連線（WebSocket close 1011，client 當暫時失敗重試），不以猜的大小啟動。
- window text frame 多了 `pty_cols`／`pty_rows`（PTY 實際大小）；client 的終端格線以它們為準，`cols`／`rows` 只是 window 本身。status bar 開關使 PTY 變動時也會重送。
- 已知限制（#1727）：同步靠每秒輪詢，桌機在輪詢之間調整大小並立刻離開 session，或在查詢與 attach 之間切換 current window，仍可能讓 window 被舊尺寸重算；根治需要事件驅動，另案。

## [1.0.0-alpha.541] - 2026-10-08

> 只動 daemon，**需要部署新 binary 並重啟 daemon**，由統籌安排。SPA、資料庫、Electron 都沒有改動。桌機上沒有使用者可見的變化：這是權限通道（讓 worker 做有風險的事之前先問你）的 daemon 半段，前端要等 SPA 那一半。

### Changed：「退出」「從終端機接手」「重建」都先把原分頁的控制權搶下來（權限通道 P-1b，#1724）

- 以前退出 worker、手動在終端機 resume（Q1）、以及重建並取代舊 worker，都是**借用**原分頁的 lease 去結束它，借用期間原分頁仍然可以寫入。現在改成和「接到終端機／接回」一樣的 D22 搶佔：先釋放持有者的 lease，再由 daemon 排他取得，所以原分頁之後的送訊與（之後會有的）核准回答都會得到 `lease_mismatch`。
- **退出仍然不會因為別的分頁持有控制權而失敗（D4）**：搶佔最多試三輪、共用一個 20 秒的預算，用完或釋放失敗就退回原本的借用；非 pdx 的持有者照舊回 `held_by`，什麼都不動。
- 已知殘餘（寫進 spec §5.4）：Nexen 在寫入前才重查 lease、寫入本身沒有圍欄；Q1 在使用者於終端機 resume 之後、Purdex 偵測到並搶佔之前按下的「同意」，仍會送到 worker。
- 新增在真實 Nexen 引擎上的測試，釘住三條保證：搶佔後舊 lease 的回答得到 `lease_mismatch`；中斷或終止生效後任何回答得到 `permission_not_pending`；結束路徑不會把拒絕、取消、逾時變成允許。

## [1.0.0-alpha.540] - 2026-10-08

> 只動 daemon，**需要部署新 binary 並重啟 daemon**，由統籌安排。SPA、資料庫、Electron 都沒有改動，桌機上沒有使用者可見的變化。這是 purdex-ios 需要的三項 daemon 小改動的第 2、3 項（第 1 項在 alpha.538）。

### Added：終端機鏡像連線 `/ws/terminal/{code}?mirror=1`（#1721）

- 手機以前一連上終端機，tmux window 就被縮成手機的大小，桌機的畫面跟著變小。鏡像連線以 `-f ignore-size` attach，不參與 window 大小計算，**也不論 `terminal.sizing_mode` 為何都不會去改 window 大小**；只有這條連線自己的 PTY 大小會變。實測同樣是 40x20 的 client，一般 attach 把 120x40 的 window 縮成 40x19，鏡像連線維持 120x40。
- 鏡像連線會收到 text frame `{"type":"window","cols":N,"rows":N}`（binary frame 仍是終端機輸出）：連線建立後、第一筆終端機輸出**之前**先送一次，之後每秒檢查，大小變了才再送，手機據此用桌機的欄數渲染。tmux 查詢卡住時，單次查詢 2 秒就放棄、下一輪重試，不會拖住輸出。
- 沒帶 `mirror=1`（或值不是字面的 `1`）的連線行為完全不變。

### Added：`/api/info` 的 `capabilities`（#1722）

- 回傳這台 daemon 提供哪些選用功能，目前是 `["transcript.v1","terminal.mirror.v1"]`。client 依此開關功能，不再比對 `purdex_version`（使用者有多台主機，版本可能不同）。

## [1.0.0-alpha.539] - 2026-10-07

> 動 daemon，**需要部署新 binary 並重啟**（由統籌安排）。接力的回報路由上線，但還沒有 mod 或 `pdx relay` 指令去呼叫（P5a-2c／P5b），現有使用者流程不受影響。`pdx` 指令、SPA、Electron 都沒有改動。

### Added：lead / member / team 與 context 接力 — P5a-2b（#1716）

接力 op 的回報與重啟收斂（spec §8.1、§8.4、§8.7、§9.3；plan v2 Task 5a.8）。

- **`POST /api/relay/ops/{id}/report`**：mod 回報 op 走到哪（writing／written／cleared／done／failed／cancelled）；同一狀態重送是冪等的，不合法的轉換回 409 並帶出 op。`claimed` 不能用這條路回報——self op 只由核准單的核准來 claim，擋掉「mod 自己宣稱已核准」。
- **`cleared`**（舊 session 換成新 session）：新 session **必須**是這台主機上、與原來同一個行程的活 session（`/clear` 不換行程）——指向別人的活 session 回 400；registry 還沒出現新 session 時最多等 3 秒、等不到回 503 讓 mod 下個回合重送。寫下血統後把 title 搬到新 session。
- **終態回報會把還開著的核准單一起關掉**（例如等核准時被 /compact 掉 → `cancelled`）：先走核准單的 CAS 再動 op，與使用者的核准／拒絕是同一個競爭點——誰先贏誰算數，mod 的回報永遠不會蓋掉使用者已做的決定。
- **回報後續動作可重試**：title 搬家或關單失敗時，同一狀態重送就會再做一次；`done` 時也會再確認 title 已搬；daemon 重啟時把「op 已終態但核准單還開著」的單關掉、把 `cleared` 的 op 再搬一次 title。
- **`GET /api/relay/ops/{id}`**；**`GET /api/team/inflight`** 多了 `relays_active`。

Review 後補強（R1 一條、攻擊方四條、critic 三輪）全部收進上面的行為：`claimed` 不可回報、新 session 的行程綁定（fail-closed）、先關單再動 op、CAS 輸了先把 op 推到單的裁決、後續動作重試、讀不到 op 不盲寫。

## [1.0.0-alpha.538] - 2026-10-08

> 只動 daemon，**需要部署新 binary 並重啟 daemon**，由統籌安排。SPA、資料庫、Electron 都沒有改動，桌機上沒有使用者可見的變化。這是 purdex-ios 需要的三項 daemon 小改動的第一項。

### Added：`GET /api/sessions/{code}/transcript`（#1717）

- 手機端以前得經 `/api/fs/read` 整份讀 Claude 的對話紀錄，但那支有 10 MB 上限，長時間的 session 常常超過（實測 37 MB），手機就完全看不到。新端點由 daemon 自己決定檔案，只回尾段或增量。
- `tail=N` 取最後 N 個完整行（預設 800、上限 5000）；`after=OFFSET` 取該位置之後的完整行；兩者不可同時帶。永遠不會回半行。單次回應上限 2 MB，超過會帶 `more:true`，用 `end_offset` 接著讀。
- 對話檔換了（`/clear`、`--resume`、重新啟動 claude）時回 `reset:true`，client 應重新取尾段。
- 只讀得到 `~/.claude/projects/` 底下的 `.jsonl`；路徑由 daemon 決定，client 不送路徑，因此不會變成任意讀檔的管道。codex／opencode 目前回 404 `unsupported`。
- 已知不處理：同檔名被外部替換不會觸發 reset（#1718）；Claude Code 自己的輪替一律換新檔名，不受影響。

## [1.0.0-alpha.537] - 2026-10-07

> 只動 daemon，**需要部署新 binary 並重啟 daemon**，由統籌安排。SPA、資料庫、Electron 都沒有改動。這一版沒有使用者可見的變化：權限通道（讓 worker 做有風險的事之前先問你）的前端還沒上，要等 SPA 那一半。

### Changed：Nexen 升到 v0.19.0（權限通道 P-1a）

- Nexen 內嵌引擎從 v0.18.1 升到 v0.19.0（中間含 v0.18.2 關閉時等 context 結束、v0.18.4 watchdog、v0.19.0 權限通道）。資料庫 schema 不變，新增的兩張表在啟動時自動補建，不需要手動處理。
- 「交接」與「worker 重建」改為檢查**實際要用的 profile** 是否可用：`max_profile` 設成 `handoff_ask` 的主機現在能用「需要核准」模式（以前連交接都會被拒）；不認得或不可用的 profile 在任何動作之前就回 409。
- 「交接」與「worker 重建」可帶 `permission_timeout_s`（0–86400 秒，等待核准多久後自動拒絕）。型別或範圍不對、或搭配沒有通道的 profile，一律回 400 `invalid_permission_timeout`，且不會有任何副作用（不 delegate、不動 tmux、不退出被取代的 worker）。
- 順序變動：profile 的檢查移到解析請求之後，所以不能交接的主機收到格式錯誤的請求時，現在回 400 `malformed_body`（原本是 409 `handoff_unsupported`）。

## [1.0.0-alpha.536] - 2026-10-07

> 動 daemon，**需要部署新 binary 並重啟**（由統籌安排；與 alpha.532／533 一次重啟）。這是接力第一個有行為的版本：四條 `/api/relay/*` 路由上線，但還沒有 mod 或 `pdx relay` 指令去呼叫（P5a-2c／P5b），所以現有使用者的流程不受影響。`pdx` 指令、SPA、Electron 都沒有改動。

### Added：lead / member / team 與 context 接力 — P5a-2a（#1708）

接力（spec §8.1、§8.7）的 daemon 端起點，plan v2 Task 5a.7。

- **`POST /api/relay/begin`**：session 的 mod 在 context 用量到門檻時來開一個接力 op，daemon 同時開一張 `self_relay` 的核准單（10 分鐘期限、30 秒租約），`model_id`／`effort` 由 daemon 從該 session 最近的 statusline 讀數填。拒絕的情況：member（U9，等 P4 才會出現）、host 的接力開關關著、該 session 自己暫停、已有一個進行中的 op（回 409 並帶出那個 op）。
- **核准單一關，op 跟著動**：核准 → `claimed`；拒絕／逾時／取消／租約到期 → `cancelled` 並記原因；不論是從 App 決定、`DELETE`、sweeper 的逾時或租約、還是來源 session 消失，都走同一條路。
- **卡住的 op 自癒**：op 與核准單是兩次寫入，中間 daemon crash 或第二次寫入失敗時，下一次 begin 會從核准單重新推導那個 op（單已關 → 照上面規則移動；單不存在 → 當作放棄），session 不會被 409 卡到重啟。
- **`POST /api/relay/hello`**：mod 啟動時報到（daemon 記住最近 512 個 session 有 mod，給之後的降級判斷用）。
- **`POST /api/relay/self`**：這個 session 的「暫停接力／恢復／查狀態」；host 開關關著時，session 自己開也還是關。
- **`GET /api/relay/wait/{id}`**：核准單的長輪詢（續租）。
- team module 開始對外提供接力血統（`previous_refs` 從這版起會出現在 `pdx peers`／App 的 peer 清單），讀 host 設定的接力開關，啟動時建 `<data_dir>/relay/`。

Review 後補強：一個 session 最多一個 op 由資料表索引接住時同樣回 409 帶 op（P5a-1a fix note）；卡住 op 的就地自癒（攻擊方）；409 帶的是重導後的 op（critic）。

## [1.0.0-alpha.535] - 2026-10-07

> 動 daemon，**需要部署新 binary 並重啟**（由統籌安排）。沒有使用者看得到的變化。

### Fixed：對話索引冷掃描逾時就永遠完成不了（#1655，#1711）

`conversations.Scan` 原本掃完才一次寫入索引，所以超過 60 秒的冷掃描什麼都不會留下、下一次又從零開始。現在每 200 筆分塊寫入，逾時前寫好的進度會保留，下一次掃描從那裡接著做。

## [1.0.0-alpha.534] - 2026-10-07

> 動 daemon，**需要部署新 binary 並重啟**（由統籌安排）。沒有使用者看得到的變化。

### Fixed：nex 停止時的假 log（#1656，#1709）

`stopManualResume` 在 ctx 已逾時、但沒有任何 manual-resume 工作在跑時，仍可能記出「still running」。現在只在確實有工作在途時才記（以原子計數判斷）。

## [1.0.0-alpha.533] - 2026-10-07

> 動 daemon，**需要部署新 binary 並重啟**（由統籌安排；可與 alpha.532 合併一次重啟）。本版沒有使用者看得到的變化：還沒有任何東西註冊接力血統（P5a-2a 才會），`pdx peers` 輸出不變。`pdx` 指令、SPA、Electron 都沒有改動。

### Added：lead / member / team 與 context 接力 — P5a-1b（#1705）

「接力後舊 ref 繼續有效」（U3，spec §8.4）的 peers 端，與 host 的接力開關（spec §8.7 (a)）。plan v2 Tasks 5a.4–5a.6。

- **peer 列多了 `previous_refs`**：這個對話接力過的所有舊 ref，最新在前、不設上限。
- **地址解析多一層**：`_ref` 沒有活的對話持有、但恰好是一個活的對話的舊 ref ⇒ 送到那個對話；活的 ref 永遠優先（含 `<name> [ref]` 寫法）；兩個對話都說那是自己的舊 ref ⇒ 不猜。「找不到」的提示改說：改名與接力都會保留 ref。
- **血統讀不到時**（`lineage_unavailable`）：peer 清單照常、不標 partial，但 ref 形式的位址查不到時回「尚未就緒」而不是「不存在」，也絕不會落到 tmux 名稱那一層誤投。
- **host 設定 `relay`**：`self_solo`／`self_lead` 兩個開關（預設都開）；PUT 只接受這兩個布林欄位，`null`、未知欄位、拼錯都回 400（避免「拼錯開關＝靜默開啟」）。
- daemon 內部：以 session id 解析來源（給之後的 self relay／AskUserQuestion 分流用）。

Review 後補強：combined form 的活 ref 優先（R1）、`null` 與未知欄位（R1／攻擊方）、`lineage_unavailable`（攻擊方 A-1）、血統鏈 memo 化。既有 `internal/module/agent` 測試在全套件負載下偶發逾時列 #1706。

## [1.0.0-alpha.532] - 2026-10-07

> 動 daemon，**需要部署新 binary 並重啟**（由統籌安排）：啟動時 team.db 多建三張空表與索引，沒有其他行為變更，可以跟下一個 PR 合併一次重啟。`pdx` 指令、SPA、Electron 都沒有改動。

### Added：lead / member / team 與 context 接力 — P5a-1a（#1702）

接力的儲存層（spec §8.1、§8.4、§8.7；plan v2 Tasks 5a.2–5a.3）。還沒有任何路由或指令用到它；P5a-1b 起開始接。

- **team.db 三張表**：`relay_ops`（接力 op 與九態狀態機；一個 session 同時只能有一個未結束的 op，由 partial unique index 保證）、`session_lineage`（session 換手的血統：誰接了誰、舊 ref 是什麼）、`session_prefs`（每 session 的「這個 session 不再詢問」）。
- **狀態回報是 CAS 交易**：報同一狀態是冪等 no-op，不合法的轉換回 `bad_transition`；`cleared` 在同一交易寫下 lineage 並存新 session id／ref，會弄壞血統的回報（空 id、新舊相同、新 session 已被別的 op 接走、形成環）一律拒絕、op 不變。
- **`PreviousRefs`**：整條鏈、最新在前、不設上限（U3），給 P5a-1b 的 peers 解析用。
- **title 搬家**（`PeerLabelStore.Move`）：接力後 title 跟著新 session id 走，一次、冪等（舊 row 已釋放就不再搬），給 P5a-2b 在 `cleared` 後與 boot reconciliation 呼叫。
- spec §8.4 措辭對齊 plan 裁決：title 搬家是 meta.db 另一個冪等交易，不是跟 lineage「同一交易」（`database/sql` 無跨庫交易）。

Review 後補強：partial unique index（R1）、`cleared` 的血統守衛（攻擊方 A-1／A-3）；`relay_store.go` 的職責拆分列 #1703。

## [1.0.0-alpha.531] - 2026-10-07

> 純型別與常數，**不需部署**（沒有任何行為變更；跑中的 daemon 完全不受影響）。資料庫、SPA、Electron 都沒有改動。

### Added：lead / member / team 與 context 接力 — P5a-0（#1700）

接力（relay，spec §8）的 wire 契約，plan v2 `docs/specs/2026-10-06-lead-team-relay-plan-v2.md` P5a 的第一個 PR；之後 P5a-1a 起的 store、路由、CLI、SPA、mod 都以這一份字面值為準。

- **`internal/team/wire_relay.go`**：接力 op 的 kind（self／member）、九個狀態與終態判定、八個 failed／cancelled 原因、六個錯誤碼、門檻常數（70%、20000 tokens、10 分鐘、`<data_dir>/relay/`）、`RelayOp` 與 hello／begin／self／report 的 request／response 型別、`LineageReader` 介面。
- **`APIError` 多了 `op`**：409 `relay_open`／`bad_transition` 會帶出現行的 op。
- 契約測試逐一釘住每個字面值與每個 DTO 的 JSON 形狀（含 optional 欄位省略、`used_percentage` 的「沒量到」與「量到 0」之分）。

## [1.0.0-alpha.530] - 2026-10-07

> 只動 `pdx` 指令，**需要部署新的 `pdx` 執行檔**（與 alpha.529 的 daemon 變更一起重啟即可），由統籌安排。資料庫、SPA、Electron 都沒有改動。

### Added：lead / member / team 與 context 接力 — P2c-2（#1697）

硬鎖（U17）完成：lead 申請等待核准期間，該 session 的每一次工具呼叫都會被 Claude Code 的 hook 擋下（spec §6.4、§6.6），補上軟鎖「模型把指令丟背景執行」的漏洞。

- **`pdx lead request`** 建立申請成功後，在 `<data_dir>/hooklocks/cc/<session_id>` 寫下旗標；不論核准、拒絕、逾時、Ctrl-C 或 kill，結束時都會移除。寫不了旗標時印一行提醒並退回軟鎖。
- **`pdx hook`**：只有該 session 有旗標時才去問 daemon（沒有旗標的 session 跟以前一樣零成本）。`PreToolUse` 收到「拒絕」就印出 Claude Code 讀得懂的 deny 與原因（「lead 申請等待核准中（<id>），核准或拒絕前這個 session 不能執行工具；請在 Purdex 介面處理」）；`PermissionRequest` 與其他情況不印。**整條路徑最多 5 秒**（事件回報與詢問並行、共用一個預算），daemon 連不上就當沒事、照常放行；永遠 exit 0，不會卡住任何 session。
- Review 後的補強：旗標內容是申請 id，結束時只在仍屬於自己時才移除（flock 保證跨程序原子），同 session 緊接著的下一個申請不會被前一個的收尾誤刪；hook 的 stdin 讀取也納入同一個 5 秒預算；事件回報與詢問並行、不互相等待（spec §6.6 措辭同步修正）。

## [1.0.0-alpha.529] - 2026-10-07

> 動 daemon 與 `pdx` 指令，**需要部署新 binary 並重啟 daemon**，由統籌安排。Codex 使用者要重跑一次 `pdx setup --agent codex` 才會拿到新的 hook timeout。資料庫結構不變（只多 `<data_dir>/hooklocks/` 目錄）。SPA、Electron 都沒有改動。

### Added：lead / member / team 與 context 接力 — P2c-1（#1695）

硬鎖的 daemon 端（spec §6.6；plan v2 `docs/specs/2026-10-06-lead-team-relay-plan-v2.md`，本版也併入該 plan 的文件 PR #1694）：

- **`POST /api/hooks/decide`**：hook 問「這個 session 現在能不能執行工具」。該 session 有尚未核准的 lead 申請時回「拒絕」與原因（「lead 申請等待核准中（<id>），核准或拒絕前這個 session 不能執行工具；請在 Purdex 介面處理」）；其他情況回空（照常執行）。
- **旗標檔目錄 `<data_dir>/hooklocks/<agent>/<session_id>`**：只有存在旗標的 session，hook 才會來問；旗標的 session 已不存在時由 sweeper 清掉。
- `pdx` 的 daemon client 多了可調的重啟寬限（hook 路徑用 5 秒，不是 30 秒）。
- Codex 的 `PreToolUse` hook timeout 從 5 秒調到 10 秒（給硬鎖路徑用）；Claude Code 的 hook 設定不變。

`pdx hook` 真正去問 daemon、`pdx lead request` 寫旗標，在下一個 PR（P2c-2）。

## [1.0.0-alpha.528] - 2026-10-07

> 只動 daemon，**需要部署新 binary 並重啟 daemon**，由統籌安排。SPA、資料庫、Electron 都沒有改動。目前部署環境裡所有 lease 持有者都是 pdx 自己，所以這版沒有可見的行為變化，是補一個之後才會碰到的缺口。

### Fixed：「退出」的歸檔一律先取得控制權再做（對話主體，#1665／#1692）

- 退出時，不需要終止的列（已終止但還沒歸檔、failed、rejected）以前會**不帶 lease 直接歸檔**。若終止之後有別的非 pdx 主體拿到 lease，下一次退出會蓋過它。現在這些列歸檔前一律先取得控制權、帶 lease 歸檔；有非 pdx 持有者就回 `held_by`，什麼都不改（D4）。
- 借用 pdx 持有者的 lease 時，歸檔前會先續租，避免 lease 快到期時歸檔失敗。
- 歸檔時 lease 易手：非 pdx 持有者回 409 `held_by`，其他回新的 409 `lease_contended`（以前是 500 `archive_failed`）。已終止的列照舊「不報錯、下次退出再歸檔」。
- 交接回滾（rejected 列）遇到這種情況，回應會帶 `exit_error` 與 `exited: false`，不再吞掉。
- 歸檔事件的 `principal_id` 在這些列上是 lease 持有者，不是 HTTP 呼叫者。

## [1.0.0-alpha.527] - 2026-10-07

> 只動 SPA，透過 HMR 生效、不需部署 daemon。**從這版起 Purdex.app 會收到 lead 申請並跳出核准對話框**（daemon 需在 alpha.518 以上，mlab 已是 alpha.523）。資料庫、Electron 都沒有改動。

### Added：lead / member / team 與 context 接力 — P3b（#1689）

lead 申請的核准流程在 App 端接通（spec §6.3、§9.4、§9.5）：

- **事件接線**：daemon 的 `approval.request` 事件（新連線時整組 snapshot、申請開啟、申請關閉）進入 store；別的 client 先回應時，這邊關掉對話框並提示「<主機>：<session> 的 lead 申請 已由 <client> 核准／拒絕」。
- **重連**：daemon 重啟期間按下的決定，重連拿到 snapshot 後只送出一次；若申請已在斷線期間結束，提示「等待期間已結束」。
- **系統通知**：申請開啟時跳「<主機>：<session> 申請成為 lead」，點了只把 App 視窗帶到前面（對話框已經在那裡）；snapshot 不會重複通知。
- **重啟確認對話框**多一行「N 個申請等待核准」（向 daemon 查 inflight，3 秒內查不到就用本地數字；為 0 時不顯示）。
- `approval.*` 文案 en／zh-TW 完整對齊，由測試鎖定。

至此 **lead 申請（P2＋P3）端到端可用**：agent 跑 `pdx lead request` → 任一台 Purdex.app 按一下核准 → CLI 拿到 grant。team 與 spawn 在 P4。

## [1.0.0-alpha.526] - 2026-10-07

> 只動 SPA，透過 HMR 生效、不需部署 daemon。**對話框已掛上畫面，但要等 P3b 接上事件之後才會收到申請**（這版開著 App 不會看到任何變化）。資料庫、Electron 都沒有改動。

### Added：lead / member / team 與 context 接力 — P3a-2（#1686）

- **lead 申請的核准對話框**（spec §6.3）：顯示主機、申請的 session（標題或名稱、位址與 ref、目錄、tmux session）、申請理由與倒數；可以改 grant（member 上限、允許的目錄）；**核准／拒絕各一下**（U5b）。多筆申請排隊、最舊的先顯示，並標示還有幾筆。
- 別的 Purdex.app 先回應時，對話框跟著關閉並提示「<主機>：<session> 的 lead 申請 已由 <client> 核准／拒絕」；自己按下後慢一步被告知已處理（409）也同樣提示。
- daemon 重啟中：對話框留著、按鈕暫停並顯示「daemon 重啟中…」；這時按下的決定會先記住、重連後送出一次。Esc 不會關掉它。

## [1.0.0-alpha.525] - 2026-10-07

> 只新增 SPA 內部程式碼，**尚未掛上任何畫面**，透過 HMR 生效、不需部署。daemon、資料庫、Electron 都沒有改動。

### Added：lead / member / team 與 context 接力 — P3a-1（#1684）

Purdex.app 核准對話框的資料層（spec §6.3、§9.4）：

- **wire 型別與 API client**：對應 daemon `/api/team/approvals` 的型別（申請、來源 session、grant、決定、事件三種 op、inflight），以及 `decide`／列出 open 申請／查 inflight 的呼叫；錯誤都轉成帶 code 的型別化錯誤，409「已被處理」會帶整筆勝出的決定。
- **`useApprovalStore`**：每台主機各自保存 open 的申請；daemon 的 snapshot 以整組取代（斷線期間關掉的申請會消失）；收到 closed 後同一筆再出現的 opened 會被忽略（tombstone，每台主機最多記 256 筆）；斷線時按下的決定先排隊、重連後只送一次；跨主機以最舊的申請優先。

## [1.0.0-alpha.524] - 2026-10-07

> 只動 SPA，透過 HMR 生效，daemon 和 Electron 都不必更新。**需要 daemon ≥ alpha.515**（已部署在 mlab）。

### Added：設定 → Worker 的「已退出」「已消失」分頁（對話主體 P4-2b，#1681）

- **已退出**：列出這台主機上所有已結束、還能再啟動的對話，不分上次是在終端機還是在 Worker。每列有標題、工作目錄（家目錄縮成 `~`）、「3 小時前」這樣的最後活動時間，以及「上次在終端機／上次在 Worker」。
  - 搜尋涵蓋標題、工作目錄、第一則 prompt、session id。
  - 按「重建…」：上次在 Worker 且有紀錄的，開回原本的 Worker 分頁；其餘開「對話已結束」的重建分頁（預選上次所在的模式）。
  - 沒有工作目錄的對話不給重建；工作目錄已不存在的標示「工作目錄已不存在」。
  - 讀不到對話檔目錄時顯示提示、停用所有重建；有狀態無法確認的對話時，清單下方用灰字計數；超過 2,000 筆時提示較舊的沒有列出。
- **已消失**：列出對話檔已被清掉、再也無法啟動的對話；整列灰掉、沒有按鈕，結尾寫「對話檔已清除，無法再啟動」。
- 兩個分頁載入中顯示「載入中…」；只在打開分頁與按「重試」時抓取；切換主機會一併清掉搜尋條件。
- 拿掉舊的「已退出」資料來源（依執行紀錄與終端機 session 推算的那一套）。

## [1.0.0-alpha.523] - 2026-10-07

> 只動 `pdx` 指令（新增 `pdx lead`），**需要部署新的 `pdx` 執行檔**，由統籌安排；daemon 程式碼、資料庫、SPA、Electron 都沒有改動（daemon 端的路由在 alpha.518 已上）。

### Added：lead / member / team 與 context 接力 — P2b-3（#1680）

- **`pdx lead request` 正式接進 `pdx` 入口**（spec §6.1）。這是 lead 申請的指令端：agent 在前景執行它、在 Purdex 介面核准後拿到 grant。核准對話框在 P3（SPA）接續；在那之前可以用 API 直接 decide 驗證。
- **跨 daemon 重啟的端到端測試**：同一個 port 關掉再開、boot id 換新，指令印一次「daemon 重啟中，繼續等待…」與一次「daemon 已重新啟動（boot …）」，接著用**同一個申請 id** 繼續輪詢直到核准（spec §9.1、Review Focus 3）。

## [1.0.0-alpha.522] - 2026-10-07

> 只新增 `pdx` 指令端的程式碼，**指令還沒接進 `pdx` 的入口**（下一個 PR 才會），不需要部署。daemon、資料庫、SPA、Electron 都沒有改動。

### Added：lead / member / team 與 context 接力 — P2b-2（#1677）

- **exit code**（spec §14）：0 核准／完成、1 錯誤、2 用法、10 拒絕、11 逾時（視同拒絕）、12 取消或放棄、13 被 team 規則拒絕、14 member 未啟動或未回應、20 daemon 連不上、21 daemon 不支援這個指令。
- **`pdx lead request --reason <text> [--max-members N] [--root <dir>]... [--wait 9m]`**（實作完成，待接入口）：向 daemon 提出 lead 申請並在前景等待核准。
  - 先印一行「申請 lead 中（<id>），請在 Purdex 介面核准；這個呼叫必須在前景等待（Bash timeout 600000）」，然後每 25 秒輪詢一次、每次都續約；核准後 stdout 印出申請 id 與 grant（team id 要等 P4）。
  - 等待期間 daemon 重啟會自動撐過（沿用上一版的 client）；daemon 接了連線卻連續三次不回應才放棄（exit 20「daemon 沒有回應」）。
  - Ctrl-C 或被 kill 會先取消申請再結束（exit 12）。
  - 用法錯誤立即 exit 2，不讀設定也不連 daemon。

## [1.0.0-alpha.521] - 2026-10-07

> 只動 SPA，透過 HMR 生效，daemon 和 Electron 都不必更新。這一版畫面上還看不到變化（要等已退出分頁接上才會出現）。

### Added：已結束的對話可以重建成「對話已結束」分頁（對話主體 P4-2a-2，#1672）

- 新增「對話已結束」的終端機重建分頁：以對話標題當分頁名稱、後綴「（已結束）」，預設勾選 resume，重建後變成一般終端機分頁，也含在「全部重建」裡。
- 同一個對話再按一次重建，會切到已開的分頁並更新標題與最後寫入時間，不改你已選的模式。
- 對話最後寫入不到 120 秒時，重建畫面直接顯示警告（秒數即時、超過就消失、不跳確認框）。
- 這種分頁不顯示「上次看到時 Claude Code 還在執行…」那一行。

## [1.0.0-alpha.520] - 2026-10-07

> 只新增 `pdx` 指令端的內部套件，**還沒有任何指令使用它**，不需要部署；下一個有行為的版本一起上。daemon、資料庫、SPA、Electron 都沒有改動。

### Added：lead / member / team 與 context 接力 — P2b-1（#1669）

`cmd/pdx/daemonclient`：之後 `pdx lead request`、接力與分流用的 `pdx relay/ask …` 共用的 HTTP client，照 spec §9.1 撐過 daemon 重啟：

- daemon 連線被拒、連線中斷、回 503「正在關閉／尚未就緒」時視為重啟中：印一次「daemon 重啟中，繼續等待…」，以 0.25 → 0.5 → 1 秒的間隔重試，**從第一次失敗起最多 30 秒**（硬上限，期限絕對），超過就回 `daemon_unavailable`（指令端 exit 20）。恢復後若 daemon 的 boot id 變了，印一次「daemon 已重新啟動（boot …）」。
- 只有上述訊號會重試：DNS 失敗、主機或網路不可達、TLS 錯誤、pairing 模式的 503 都立即回報，不會白等 30 秒。
- 沒有帶期限的呼叫每次嘗試最多等 60 秒；daemon 接了連線卻不回應回 `no_answer`，由指令端決定是否再問。
- 寫入請求（POST／DELETE）送出後才斷線時，只有呼叫端標記為冪等（帶 client 產生的 id）才會重送，避免重複建立。
- 舊 daemon 沒有該路由（非 JSON 的 404，含代理回的 HTML）→ `unsupported`（exit 21）；daemon 自己的 JSON 404 原樣回給呼叫端。

review 期間修正：30 秒改為硬上限並加每次嘗試的逾時、重試訊號收窄為 allowlist、404 改以 body 判定、寫入重送需冪等標記。

## [1.0.0-alpha.519] - 2026-10-07

> 只動 SPA，透過 HMR 生效，daemon 和 Electron 都不必更新。這一版畫面上還看不到變化。

### Added：讀取已結束／已消失對話清單的前端程式（對話主體 P4-2a-1，#1668）

- 新增呼叫 daemon `GET /api/nex/conversations` 的程式與對應的 hook，供之後的「設定 → Worker → 已退出／已消失」分頁使用。
- 只在打開分頁與按「重試」時抓取；同一台主機、同一種清單同時只會有一個請求。這台主機沒有啟用 Nexen 時會標示為「不可用」。

## [1.0.0-alpha.518] - 2026-10-07

> 只動 daemon，**需要部署新 binary 並重啟 daemon**，由統籌安排。首次啟動會在 data dir 建立 `team.db`（新檔，不動既有資料庫）。SPA 與 Electron 都沒有改動。

### Added：lead / member / team 與 context 接力 — P2a-3（#1662）

lead 申請的 daemon 端自此完整並掛上路由（spec §6.2、§9.2、§9.5）；指令 `pdx lead request` 與 Purdex.app 的核准對話框在 P2b、P3 接續。

- **`GET /api/team/approvals/{id}?wait=N`**：長輪詢一筆申請（最多 25 秒），每次輪詢都續約 30 秒的 lease；daemon 停止時立即回目前狀態，讓指令端等重啟後接著問同一筆。
- **`DELETE /api/team/approvals/{id}`**：申請方放棄。**`POST /api/team/approvals/{id}/decide`**：任一 Purdex.app 按一下核准或拒絕（帶 client 標籤與來源位址，供廣播與稽核）；慢一步的那個 client 收到 409 與勝出的決定。
- **`GET /api/team/inflight`**：等待核准的申請數（接力數先固定 0），給重啟確認對話框用。
- **sweeper**：逾時視同拒絕（U7）、lease 過期或申請的 session 已不在 → 放棄；每一筆關閉恰好廣播一次 `approval.request {op: closed}`。
- **啟動**：open 的申請在 daemon 啟動時 lease 一律延到啟動後 30 秒（U12）；新連上的 client 先收到一份 open 申請的 snapshot。

review 期間修正：snapshot 與即時事件改為同一順序（新連線不會被過期 snapshot 蓋掉或復活已關閉的申請）；sweeper 只在 lease／deadline 仍然過期時才關閉（續約成功的申請不會被誤關）；續約寫入失敗回 503 讓指令端重試。

### Docs

- spec 新增 **U19（分流）**：終端機照常出現原生 AskUserQuestion／權限對話框，其他 client 顯示事件卡，任一邊回答後全部關閉；由 Purdex mod 讓原生框與 daemon 的答案賽跑（M24 實測）；沒有 mod 的 session 退化成唯讀卡；P8a／P8b。

## [1.0.0-alpha.517] - 2026-10-07

> **需要部署 daemon**（內嵌的 Nexen 升級，加上退出 worker 的行為變更）。資料庫結構不變（仍是 schema v6），不用清資料。SPA 與 Electron 都不必更新。

### Changed：內嵌的 Nexen 升到 v0.18.1，退出 worker 時封存改帶同一把 lease（#1664，D23）

- **退出 worker 不再有空檔**：退出分兩步，先停止、再封存。現在封存會帶上停止時用的那把控制權 lease，Nexen 會在同一句寫入裡確認 lease 還是自己的。如果兩步之間有別人拿走了控制權，封存會被擋下，不會替一個已經不歸自己管的 worker 做決定（Nexen #113）。被擋下時 worker 仍算已退出（已停止、未封存），下次退出時會補封存。
- **其他 Nexen 改進**（v0.17.2～v0.18.1）：
  - 站台級事件流改成白名單（#90），wire 不變；
  - 開資料庫時會檢查所有資料表（#69）；
  - 輸出過大時，bash 輸出的頭尾少一行空白（#112）；
  - 使用者已離開的對話紀錄請求，不再被記成伺服器錯誤。
- spec：對話主體 §5 新增 D23；worker prelude spec §4.3 記下 #112。
- 後續追蹤：#1665（封存的補做重試還沒有 lease 圍欄；目前所有 lease 持有者都是 Purdex，碰不到）。

## [1.0.0-alpha.516] - 2026-10-07

> 只新增 daemon 內部程式碼，**還沒掛上任何路由**（P2a-3 才會），不需要部署；下一個有行為的版本一起上。資料庫、SPA、Electron 都沒有改動。

### Added：lead / member / team 與 context 接力 — P2a-2（#1660）

- **peers 匯出 origin resolver**：由 Claude Code 的 inbox 找到提出申請的 session（ref、name、pid、cwd、tmux、title、address），給 team module 用來認定「誰在申請」。registry 讀不到時明確回報為暫時性錯誤，不會被當成「找不到這個 session」。
- **team module 骨架與前兩條路由**：`POST /api/team/approvals`（建立 lead 申請）與 `GET /api/team/approvals?state=open`。
  - 建立是冪等的：同一個 id 同樣內容重送回同一筆（即使同一個 session 後來又開了另一筆）；同 id 不同內容拒絕。
  - 同一個 session 同時只能有一筆 open 的申請；並發送出時恰好一筆成立。
  - id 必須是 UUID v4；daemon 正在停止時回 503 讓指令端等重啟。
  - 成功建立會廣播 `approval.request {op: opened}` 事件（之後給 Purdex.app 的核准對話框用）。

review 期間修正：冪等重試優先於「已有 open 申請」的檢查、UUID v4 驗證、停止與建立的競態、registry 讀取錯誤改回 503。

## [1.0.0-alpha.515] - 2026-10-07

> 只動 daemon，**需要部署**，由統籌安排重啟。資料庫結構不變（`conversation_index` 表在 alpha.510 就已建立），不用清資料。SPA 與 Electron 都不必更新；畫面上的「已退出／已消失」分頁要等之後的 SPA 版本。

### Added：列出已結束與已消失的對話（對話主體 P4-1c，#1657）

- **新的 daemon 端點** `GET /api/nex/conversations?state=ended|gone`：
  - `ended`：沒人在用、對話檔還在的對話，不論上次是在終端機還是 Worker；
  - `gone`：對話檔已被清掉的對話；
  - 正在終端機或 Worker 裡的不會列出；無法確認是否還在用的也不列出，只回報數量；
  - 每種最多 2,000 筆，從最近的開始，超過時會註明。
- **什麼時候掃描**：daemon 啟動時掃一次、之後每 6 小時一次；有人查詢時，若上次結果超過 5 秒就重新掃。同時多個查詢只會跑一次掃描。第一次掃描要把所有對話檔讀過一遍，之後只讀有變動的部分。
- **保守處理**：對話檔目錄讀不到（例如磁碟沒掛上）時照樣回傳清單並附上錯誤，不會把任何對話判成已消失；Nexen 清單讀不完整或讀取失敗時直接回錯誤，不會回傳不完整的清單。
- **daemon 關閉時**：會等進行中的掃描結束（最多 3 秒），關閉後不再回傳任何掃描結果。
- 每次掃描在 daemon log 記一行：檔案數、重讀數、讀了多少位元組、花多久。

## [1.0.0-alpha.514] - 2026-10-07

> 只新增 daemon 內部套件，**還沒有任何東西呼叫它**，不需要部署；下一個有行為的版本一起上。資料庫、SPA、Electron 都沒有改動。

### Added：lead / member / team 與 context 接力 — P2a-1（#1654）

lead 申請（之後也包括自我接力的核准）的資料層，照 spec §6.2 的 state machine：

- **`internal/team`**：wire 契約（leaf package）——申請的 kind、state、error code、上限（member 預設 3、最多 8；等待預設 9 分鐘、最長 10；lease 30 秒；輪詢 25 秒；重啟寬限 30 秒）、`Approval`／`Origin`／`Grant`／`Client` 等 JSON 形狀，以及 `approval.request` 事件的 opened／closed／snapshot 三種 value。
- **`internal/module/team/store.go`**：`team.db` 的 `approval_requests` 表。建立是冪等的（同 id 同內容回同一筆；同 id 不同內容拒絕）；**關閉用 compare-and-set**，decide、逾時、取消三方同時關閉時恰好一個成功，落敗的一方拿回成功那方的完整結果（誰決定的、何時、grant）；lease 可續、重啟時整批延長。
- 測試含 `-race` 的 16 路競爭與 mutation 驗證（拿掉 CAS 守衛測試即紅；落敗方缺欄位測試即紅）。

路由、module 與 `pdx lead request` 指令在 P2a-2、P2a-3、P2b 接續。

## [1.0.0-alpha.513] - 2026-10-07

> 只動 daemon 與 `pdx` 指令，**需要部署新的 `pdx` 執行檔並重啟 daemon**，由統籌安排。資料庫、SPA、Electron 都沒有改動。

### Added：lead / member / team 與 context 接力 — P1（#1650）

這是「lead / member / team 與 context 接力」規格的第一個程式 PR。規格與計畫在前一個純文件 PR（#1648）：`docs/specs/2026-10-06-lead-team-relay-spec.md`（使用者決策 U1–U18、量測事實 M1–M22、phase P0–P8）與 `docs/specs/2026-10-06-lead-team-relay-plan.md`（plan v1，P0–P3）。`PRODUCT.md` 的詞彙也在那個 PR 改為 lead / member / team（P0）。

- **`pdx peers` 多一欄 `CTX`**：每個 Claude Code session 的 context 已用百分比（例如 `72%`），不知道時顯示 `-`。`--all` 的表格也有。`--json` 的每一列多 `agent.context {used_percentage, window, at}`。
  - 數值來自 statusline 回報，**按 CC session id 保存**：同一個 tmux session 裡開兩個 Claude Code pane，各自顯示各自的用量，不再互相覆蓋。
  - 一個 session 剛啟動、statusline 還沒回報百分比時顯示 `-`；移除 statusline 後也會清掉。
  - 同一份資料也記下 session 目前的 model 與 effort 等級（給之後的接力與切換用，這版還沒顯示）。
- **`pdx peers` 的 CWD 修正**：session 列的目錄現在跟著 Claude Code 自己回報的工作目錄走（進了 worktree 就顯示 worktree 路徑），其次才是 tmux 的 session 路徑。以前一律顯示 tmux 的路徑，進 worktree 的 session 會顯示錯。
- **`peer_not_found` 的提示**不再說 ref「永遠不變」：ref 會在手動 `/clear` 後改變；它不變的是改名。

### Fixed：U16 用詞（#1649）

- SPA 的三條切換錯誤文案「交接」改為「切換」（`handoff.error.*`）。英文與程式識別字不變。

### 待追蹤

- #1652：statusline remove 與在途 status POST 交錯時可能重新留下過期 CTX（視窗極窄，與既有快取行為對稱）。
- #1647／nexen #131：切換（handoff）沿用 session 的 model 與 effort（U18）。

## [1.0.0-alpha.512] - 2026-10-07

> 只動 daemon 的內部程式碼，**目前沒有任何地方會呼叫到它，這一版本身不需要部署**（alpha.511 的 Nexen 升級若已排部署，照原計畫即可）。資料庫結構不變。SPA 與 Electron 都不必更新。

### Added：對話索引的掃描與狀態判斷（對話主體 P4-1b，#1645）

為之後「設定 → Worker → 已退出／已消失」做準備；這一版還看不到畫面上的變化。

- **掃描對話檔**：只重讀有變動的檔案。檔頭的資訊（工作目錄、誰開的、第一則 prompt）找到後就保留；還沒找到的從上次停下的地方接著讀；只有檔案被改寫（變短、或換成另一個檔案）才從頭重讀。檔尾每次只讀最後 256 KB。
- **列出所有在終端機裡的 Claude Code session**：一次取得，用來排除正在使用的對話。
- **判斷每個對話的狀態**：正在終端機或 worker 裡 → 不列出；沒人在用而對話檔還在 → 已退出；對話檔已不在 → 已消失；無法確認是否還在用的 → 不列出、另外計數。同時決定標題、「上次在」終端機或 Worker、工作目錄是否還在、最後活動時間。

## [1.0.0-alpha.511] - 2026-10-07

> **需要部署 daemon**（內嵌的 Nexen 升級）。資料庫結構不變（仍是 schema v6），不用清資料。SPA 與 Electron 都不必更新。

### Changed：內嵌的 Nexen 升到 v0.17.1（#1643）

- **worker 畫面不再漏事件**：live 事件現在保證依序、每筆恰好一次。以前斷線重連時，可能永遠漏掉「subagent 已結束」這類事件（Nexen #83）。
- **被權限擋下的工具改顯示「被拒絕」**：worker 在唯讀模式、或被 hook 擋下的工具呼叫，以前顯示成「執行了但失敗」，現在正確顯示成「沒有執行」（Nexen #122）。升級前已經記錄的舊事件不會回頭改。
- Purdex 本身沒有改程式；只升了 `go.mod` 的版本。

## [1.0.0-alpha.510] - 2026-10-07

> 只動 daemon 的內部程式碼，**目前沒有任何地方會呼叫到它，不需要部署**。資料庫會在下次啟動時多一張空表（`conversation_index`），不用清資料。SPA 與 Electron 都不必更新。

### Added：對話索引的底層（對話主體 P4-1a，#1641）

為之後「設定 → Worker → 已退出／已消失」列出所有結束或已清除的對話做準備；這一版還看不到畫面上的變化。

- **對話索引表**：在 `meta.db` 新增一張表，每個看過的 Claude Code 對話檔記一筆（工作目錄、誰開的、標題、第一則 prompt、檔案大小與時間、讀到哪裡）。只增不刪。
- **讀對話檔的工具**：
  - 列出 `~/.claude/projects` 底下所有對話檔；資料夾是捷徑（symlink）會跟過去，對話檔本身是捷徑則略過。
  - 某個專案資料夾讀不到時（權限、磁碟沒掛上），只把那個資料夾記成「讀不到」，其他照常；之後不會因此把裡面的對話誤判成已消失。
  - 從檔頭一路往後讀到第一則人類 prompt 為止（最多 16 MB），可以從上次停下的地方接著讀；檔尾讀最後 256 KB 找標題。
- 文件：P4 的實作計畫，以及 spec 兩處修訂（互動式開啟的對話都算在範圍內，例如 IDE 外掛；「上次在」的退路規則）。

## [1.0.0-alpha.509] - 2026-10-07

> 只動 SPA，透過 HMR 生效，daemon 和 Electron 都不必更新。

### Fixed：工具結果裡的圖片不再顯示成原始 JSON（#1629、#1638）

- **以前**：worker 用 Read 讀圖片時，那個工具呼叫的結果會印出一串 `[{"type":"image",…}]`，後面再接一行「含非文字內容」。交接前的對話、指揮室、聊天模式都一樣。
- **現在**：每張圖片或每份文件各顯示成一個佔位，例如 `[image · png · 80 B]`，跟對話裡其他附件的樣子相同；結果同時有文字時，文字照常顯示在佔位前面。
  - 正在執行中收到的圖片，大小由圖片資料推算，和交接前對話裡顯示的大小一致；不知道大小時只顯示 `[image · png]`。
  - 只有圖片、沒有文字的結果不再畫一個空白輸出框；若輸出被 daemon 截斷，截斷提示照樣顯示。
- 其他形狀的結果（例如 subagent 交回來的內容）維持原本的顯示方式，不會少資訊。
- 搜尋不再把圖片的 JSON 當成可搜尋的文字。

## [1.0.0-alpha.508] - 2026-10-07

> 只動 `pdx` 指令（`pdx msg selftest`），**需要部署新的 `pdx` 執行檔**，由統籌安排。daemon 程式碼、資料庫、SPA、Electron 都沒有改動。

### Fixed：`pdx msg selftest` 誤判失敗（#1631、#1635）

- **不再誤報「沒有回覆」**：自我測試會開一個拋棄式的 Claude Code，請它用內建的 SendMessage 回覆。
  - 以前全域 CLAUDE.md 要求 agent 一律用 `pdx msg send` 回覆，拋棄式 session 就改走 Bash；可是這種模式下 Bash 不能核准，回覆根本送不出去，結果判成失敗。
  - 現在只對這個拋棄式 session 關掉 Bash，並在訊息裡講明要用 SendMessage 回覆。你的 CLAUDE.md、平常的 session、`pdx msg send` 都不受影響。
- **不再留下對話檔**：拋棄式 session 改成不寫對話檔，跑完 `~/.claude/projects` 底下不會多出檔案。
- **啟動失敗講清楚**：若 Claude Code 不認得啟動參數、一啟動就結束，會顯示 `FAIL: claude failed to start: <它印出的最後一行>`，不再顯示成「沒有註冊」。
- 已在 mlab 用 Claude Code 2.1.291 實測通過。

## [1.0.0-alpha.507] - 2026-10-06

> 只動 daemon，**需要部署**，由統籌安排重啟。資料庫不變，不用清資料。SPA 與 Electron 都不必更新。

### Fixed：對話主體的 daemon 延後項目（#1633）

- **判斷對話在誰手上，不再受順序影響**：交接、接回終端機、重建這些動作，開始前都會先檢查對話目前在誰手上。
  - 以前若有一筆「無法確認還在跑」的終端機紀錄排在前面，即使後面有一筆確定還在跑的，也會回「暫時無法確認，請重試」。
  - 現在只要有任何一個確定還在跑的終端機，就直接回「這段對話正在終端機中」。
  - 只有全部都無法確認時，才回「請重試」。
- **其他**：
  - 補上幾處說明：多個訂閱者收到通知的先後順序不保證；取消訂閱的當下可能還有一次通知送達；查詢對話擁有者時，最多掃描 10,000 筆。
  - 補齊審查時延後的測試。

## [1.0.0-alpha.506] - 2026-10-06

> 只動 SPA，透過 HMR 生效，daemon 和 Electron 都不必更新。

### Fixed：對話主體的延後項目（#1630）

- **搜尋跟畫面一致（#1617）**：「交接前對話」裡之前那段 worker 的工具呼叫，畫面上用的是那段 worker 自己記錄的狀態（例如失敗）。以前 chat 模式的搜尋還照對話檔推斷的狀態走，跳到符合的結果時，可能打開錯的收合，也看不到標記。現在兩邊用同一份狀態。
- **只送圖片的那一行自成一輪（#1539）**：這是 alpha.505 已修好的行為，這版補上測試並關閉 issue。
- **重建畫面**：
  - 在「終端機」與「Worker」之間切換時，上一次重建失敗的錯誤訊息會清掉。
  - 讀螢幕軟體讀選項時，改用畫面上那段標題，不再重複念一次。
- **設定 → Worker →「已退出」**：載入失敗後按「重試」時，即使清單上還留著先前的資料，也會在上方顯示載入中，並告知讀螢幕軟體清單正在更新。
- **其他**：
  - 重新啟用時，各段 worker 的清單會先顯示「載入中」，不會短暫沿用舊結果。
  - 幾處內部整理，以及補上保護既有行為的測試。

## [1.0.0-alpha.505] - 2026-10-06

> 只動 SPA，透過 HMR 生效，daemon 和 Electron 都不必更新。到這版為止，對話主體 P3（完整對話渲染）全部完成。

### Added：對話主體 P3b-2c——早段 worker 的附件縮圖；只送附件的 prompt 也算新的一輪（#1622）

- **附件縮圖**：在 worker 畫面上方的「交接前對話」裡，之前某段 worker 收到的圖片，原本只顯示「圖片已省略」的佔位。現在只要能確定是哪一張，就會改成真的縮圖，從那段 worker 存下的附件抓。
  - **什麼叫「能確定」**：這段裡帶圖片的 prompt 則數，要等於那段 worker 收到附件的次數，而且每一則的圖片張數也要對得上。只要有一處對不上，整段都保留佔位，不冒險配錯。
  - **PDF 等文件附件**：維持原本的佔位，不會被當成圖片。
- **只送附件的 prompt 也算新的一輪**：以前一則只有圖片、沒有文字的訊息，在「交接前對話」的 chat 模式裡會被併進上一輪。現在它會自成一輪，搜尋也跟著分開。worker 重建後第一則只送附件的情況，也會正確歸到新的那段 worker（修正 #1614）。
  - 正在跑的 worker 畫面不受影響：只送附件的訊息本來就自成一輪。
- **安全**：對話檔裡如果夾帶了 Purdex 自己用的附件欄位，讀入時會一律濾掉，不會被拿去抓附件。
- **「事件太多」那行**：筆數加上千分位（例如「只補充了前 5,000 筆」），依介面語系的格式顯示。

## [1.0.0-alpha.504] - 2026-10-06

> 只動 SPA，透過 HMR 生效，daemon 和 Electron 都不必更新。對話主體 P3b-2b 到這版全部完成。

### Added：對話主體 P3b-2b-2——早段 worker 的每一輪顯示費用（#1620）

- **看得到什麼**：在 worker 畫面上方的「交接前對話」裡，之前某段 worker 跑過的每一輪底下，會多一行小字，顯示這一輪花了多少錢、產出多少 token、花了多久。樣式跟正在跑的 worker 每輪結尾那行一樣。
- **怎麼對上**：用那段 worker 自己的紀錄來對應：每一輪的結果配上它那幾則回覆的 id，不靠「第幾輪」的順序硬配。對不上的就不顯示。
- **含交接前的費用**：某段 worker 是接續之前的對話時，它第一輪的費用可能把交接前的花費也算進去。這時那行小字會帶一個提示「含交接前歷史」，跟右上角費用面板的說法一致，數字本身不改。
- **同一段裡有兩輪**：如果兩輪落在同一個段落裡（例如中間那則只送了附件），兩輪的費用會依序都列出來，不會只剩最後一輪。
- **沒補到的段落**：抓不到紀錄或還在抓時，不顯示費用行，畫面跟以前一樣。

## [1.0.0-alpha.503] - 2026-10-06

> 只動 SPA，透過 HMR 生效，daemon 和 Electron 都不必更新。需要主機的 Nexen 是 v0.17（mlab 已是）。

### Added：對話主體 P3b-2b-1——早段 worker 的段落顯示當時真正的工具狀態（#1618）

- **看得到什麼**：worker 畫面上方的「交接前對話」裡，凡是之前某段 worker 寫的段落，現在會用那段 worker 自己的紀錄來畫：
  - **工具結果**：成功、失敗、被拒或中斷，照當時記下的狀態顯示。
  - **耗時**：每個工具呼叫實際花的時間。
  - **subagent**：子代理的進度，像正在跑的 worker 一樣，掛在呼叫它的 Task 底下。
  - 以前這些段落只能從對話檔推斷，工具一律顯示「完成」，也看不到耗時和 subagent 的內容。
- **chat 模式**：失敗的工具呼叫會從「用了 N 個工具」的收合裡移出來，單獨顯示，跟正在跑的 worker 一致。
- **什麼時候去抓**：段落第一次畫出來時才抓那段 worker 的紀錄。同一個分頁每段只抓一次。關掉或切走分頁，抓取會立刻停下。
- **紀錄太多**：一段 worker 超過 5000 筆紀錄時，只用前 5000 筆，段落最後會多一行灰字說明。
- **抓不到**：畫面照舊，不跳錯誤訊息。下次打開這個分頁時會再試一次。
- **已知限制**：
  - chat 模式搜尋時，若某個工具呼叫在補上狀態後從收合裡移出來，跳到它時可能展開錯的收合（#1617）。
  - worker 重建後第一則只送附件的情況見上一版說明（#1614）。

## [1.0.0-alpha.502] - 2026-10-06

> 只動 SPA，透過 HMR 生效，daemon 和 Electron 都不必更新。這版畫面上同樣看不出變化，下一版才會用它來補上早段 worker 的費用和工具狀態。

### Added：對話主體 P3b-2a-2——判斷「交接前對話」裡每一行出自哪一段 worker（#1613）

- **怎麼判斷**：worker 畫面上方的「交接前對話」裡，凡是 worker 寫的行，會依它在對話檔裡的位置，歸給「開始位置不超過它、而且最接近它」的那一段 worker。
  - 剛好落在某段的開始位置上，就歸給那一段。
  - 終端機寫的行不歸給任何一段。
  - 目前正在跑的這一段也不參與，它的內容本來就從自己的事件紀錄畫。
- **什麼時候才去查**：只有在主機的 Nexen 夠新，而且已載入的部分裡確實有 worker 寫的段落時，才會去列這段對話的各段 worker。純粹從終端機交接過來的對話不會多發請求。
- **畫面完全不變**：判斷結果目前只用來把畫面分成一段一段，HTML 一個字都沒變。往上載入較舊的內容時，既有的段落也不會被重畫。
- **搜尋**：搜尋列開著時，如果剛好查完各段 worker、畫面被重新分段，高亮會重新標上，不會消失。
- **已知限制**：chat 模式下，worker 重建後如果第一則只送附件、沒有文字，那一則會被併進前一個 turn，接下來的內容也會被算給前一段 worker。之後補充費用和工具狀態時，這部分只會沒補到，不會補錯。這個問題排在 P3b-2c 修正（#1614）。

## [1.0.0-alpha.501] - 2026-10-06

> 只動 SPA，透過 HMR 生效，daemon 和 Electron 都不必更新。這版畫面上看不出變化，是下一版「早段 worker 對話補上費用與工具狀態」的前置工作。

### Added：對話主體 P3b-2a——列出同一段對話的各段 worker（#1611）

- **補上對話檔位置（offset）**：worker 畫面上方的「交接前對話」每一行，Nexen v0.17 都附有它在對話檔裡的位置。SPA 先前讀進來時會把這個欄位丟掉，現在保留下來，之後用來判斷每一行是哪一段 worker 寫的。
- **列出同一段對話的每一段 worker**：
  - 新版 Nexen 可以直接依 session 查詢；舊版改用 Purdex 交接時貼上的 `purdex.session_id` 標籤。
  - 已歸檔的也一併列出，而且會把分頁走完。
  - 每一段再查出它在對話檔裡從哪裡開始，最多看最近的 50 段。
- **寧可不標，也不標錯**：清單只要不完整，這段對話就不做任何歸屬，照原樣顯示。不完整包括：分頁被截斷、分頁卡在同一頁，或有資料格式不對而被略過。
- **一開始就是 worker 的對話**：沒有從終端機接過來、一開始就是 worker 建立的對話，視為從對話檔開頭寫起，不必另外查詢。

## [1.0.0-alpha.500] - 2026-10-06

> daemon 和 SPA 都有改動。**daemon 需要部署**，由統籌安排一次重啟：Nexen 升級到 v0.17.0，資料庫 schema 不變，不用清資料。SPA 透過 HMR 生效，Electron 不必更新。
>
> 部署前若 SPA 先拿到新版，連的還是舊 daemon，handoff 確認框會照舊顯示原本的說法，行為也跟以前一樣。

### Changed：對話主體 P3b-1——交接到 worker 不再寫一段假對話進去（#1609）

- **交接與重建變成「先建好、等你開口」**
  - 「交接到 worker」或「重建為 worker」時，worker 會先建好、停在閒置狀態，不會自己先跑一輪。
  - 第一輪要等你送出下一則訊息才開始。
  - 以前會塞一句「(handed off from …)」或「(rebuilt as worker)」進對話紀錄，模型還會回一句，也就多花一次呼叫。現在這些都沒有了。
- **確認框文案**
  - 新版 daemon 顯示「…這段對話改由 worker 接手，並等待你的下一則訊息」。
  - 還沒升級的主機照舊顯示原本的說法。
- **交接時選了不保留 tmux session**：交接成功、worker 停在閒置時，同樣會關掉那個已經空了的 tmux session。
- **判斷對話在誰手上**
  - 查某段對話目前在哪個 worker，改用 Nexen 新的「依 session 查詢」，不再逐頁掃整張表。
  - session id 的大小寫一律視為相同，終端機與 worker 兩邊都是，不會因為大小寫不同而漏判。
- **競態**：「最後一次確認」和「建 worker」之間剛好有人在終端機手動 resume 時，因為新 worker 不會自己跑，不會出現兩邊同時寫入。daemon 也會隨即讓那個閒置的 worker 退出。

## [1.0.0-alpha.499] - 2026-10-06

> 只動 SPA，透過 HMR 生效，daemon 和 Electron 都不必更新。對話主體 P2 到這版全部完成。

### Added：對話主體 P2-2——設定 → Worker →「已退出」（#1607）

- **列出什麼**：「設定 → Worker」新增「已退出」分頁。選一台主機，就會列出它所有已經不活著的對話，一段對話一列，用最近的那一段代表，最近結束的排在最前面。只要這段對話還有任何一段活著，它就算 worker，只會出現在 Workers，不會出現在這裡。
- **搜尋**：可以用標題、brief 第一行、工作目錄或 session id 搜尋。
- **重建**：每列有「重建…」，會在分頁打開這個 worker 的「已退出」畫面，再選要接回終端機還是 Worker。
- **在終端機中**：如果這台裝置已經在某個終端機分頁開著這段對話，該列會標「在終端機中」，不提供重建。
- **清單更新**：分頁開著時，清單會跟著更新。更新時一次只跑一趟，兩趟之間至少隔 5 秒，避免反覆抓整份歷史。
- **筆數上限**：已退出的紀錄只會越積越多。超過 10,000 筆時，畫面會顯示「已退出的對話紀錄超過 10,000 筆，最新的可能沒有列出」，因為 Nexen 是由舊到新列出。之後若 Nexen 支援由新到舊分頁（wake/nexen#119），就不會再有這個限制。

## [1.0.0-alpha.498] - 2026-10-06

> 只動 SPA，透過 HMR 生效，daemon 和 Electron 都不必更新。需要 daemon 至少是 alpha.492（mlab 已部署）。

### Changed：對話主體 P2-1——新分頁可切換 Sessions／Workers，設定 → Worker 改為分頁（#1605）

- **新分頁**
  - 每台主機的區塊在 Nexen 就緒時，標題列多一個「Sessions／Workers」切換。
  - Workers 列出這台主機活著的 worker，一段對話一列，點一下就開啟。
  - 每列也有「退出」，和側欄一樣：執行中的會先問一次。
  - Nexen 沒就緒時看不到切換，畫面固定在 Sessions。
- **設定 → Worker 改成分頁**
  - 「外觀」是原本的設定內容。
  - 「Workers」可以選主機，列出它活著的 worker，可以開啟或退出。
  - 選到 Nexen 未啟用或無法使用的主機時，會直接說明原因，不會一直顯示載入中。
  - 之後的「已退出」分頁會加在同一排（P2-2）。
- **共同的行為**
  - 清單超過上限時，一樣顯示「未歸檔的執行紀錄超過 10,000 筆，最新的可能沒有列出」。
  - 兩處的分頁都可以用方向鍵與 Home／End 切換。

## [1.0.0-alpha.497] - 2026-10-06

> 只動 SPA，走 HMR 套用，daemon 和 Electron 都不必更新。需要 daemon 至少是 alpha.495，mlab 已部署。對話主體 P1 到這版全部完成。

### Changed：對話主體 P1c-3——關閉的終端機分頁也能重建為 worker（#1603）

- 終端機分頁的 tmux session 不在了，就會出現原本的「已結束」畫面。以下三項都成立時，畫面上多一個「重建為」選擇：
  - 這台主機的 Nexen 已就緒；
  - 分頁記得這段對話的 Claude session id；
  - 分頁記得工作目錄。
- 預設是「終端機」，行為和以前完全一樣。改選「Worker」後，畫面會顯示工作目錄和「重建」按鈕，按下去就在同一個分頁開一段新的 worker 接續這段對話。
- 對話已經在別的終端機或 worker 裡時，會直接在按鈕下方說明，不會重建。
- 重建途中關掉了分頁，新的 worker 照樣建起來，畫面會提示你到 Worker 清單打開它。
- 終端機重建和 worker 重建共用同一把鎖。其中一個進行中時，另一個和「重建為」的選擇都會停用，不會在同一個分頁同時建出兩個東西。

## [1.0.0-alpha.496] - 2026-10-06

> 只動 SPA，走 HMR；daemon 和 Electron 都不必更新。需要 daemon 至少 alpha.495（mlab 已部署）才有重建端點。

### Changed：對話主體 P1c-2——worker 結束後的畫面可以重建（#1601）

- **已退出的 worker**：分頁顯示「這個 worker 已退出」。可以選擇「重建為」終端機或 Worker，預設是 Worker，按「重建」就在同一個分頁接續這段對話。
  - 選終端機：開一個新的 tmux session，再 `claude --resume`。
  - 選 Worker：開一段新的 worker，沿用原本的 sandbox profile。
- **啟動失敗的 worker**：分頁顯示「啟動失敗」與原因，一樣可以重建；重建時會先把失敗的那段退出。旁邊多一個「退出」，只想收掉時可以直接退出，退出後畫面變成「已退出」。
- **交接到 worker 被拒絕**：被拒且終端機沒有還原時，分頁直接變成那個 worker 的「啟動失敗」畫面，錯誤訊息照常顯示。
- **選項不可用時**：
  - 這台主機的 Nexen 還沒就緒，或不知道這段對話的 session id／工作目錄時，Worker 選項不能選，下方會直接寫出原因。
  - 「重建為」可以用方向鍵切換。
- **擋下與部分成功**：
  - 對話已在終端機或另一個 worker 裡時，會在按鈕下方說明，不會重建。
  - 重建成終端機成功、但舊 worker 沒能退出時，會出現常駐提示，請你到清單手動退出。
  - 重建途中關掉了分頁時，新的 worker 照樣會建起來，畫面會提示到 Worker 清單打開它。
- 已結束的 worker 分頁不再顯示對話紀錄（退出後只剩重建）。

## [1.0.0-alpha.495] - 2026-10-06

> 只動 daemon。**daemon 尚未部署**，由統籌安排一次重啟，用重啟鈕路徑。SPA 要到 P1c-2 才會用到這個端點，部署前 SPA 照舊運作，Electron 也不必更新。

### Added：對話主體 P1c-1——把一段對話重建成 worker（#1599）

- 新增 `POST /api/nex/worker-rebuild`：替一段 Claude 對話開一段新的 worker，接續原本的對話。之後的「重建」畫面（P1c-2／P1c-3）和設定頁的「已退出」清單（P2-2）都會用它。
- **可以指定要取代哪一段**：例如啟動失敗的那段，它會先被退出，再建新的。舊的那段退不掉時，不會建新的。
- **重建前會先確認對話沒被別人用著**：已經在終端機裡，或已有另一個 worker 時，會擋下並說明在哪裡；正在轉移中也會擋下。
- **session id 會先檢查格式**，大小寫不同視為同一段對話，避免繞過上述檢查。
- **已知限制**：在終端機手動 resume 的那一瞬間剛好重建，可能會短暫同時存在。daemon 會在幾秒內自動讓 worker 退出。之後改用 Nexen v0.17 的「先建成閒置」（P3b）後就不會再發生。

## [1.0.0-alpha.494] - 2026-10-06

> 只動 SPA，走 HMR；daemon 和 Electron 都不必更新。需要 daemon 至少是 alpha.492（mlab 已部署）才有退出端點與新錯誤碼。

### Changed：對話主體 P1b-2——worker 只有一個「退出」，手動 resume 會跳通知（#1597）

- **「退出」取代「終止」**：
  - worker 分頁標題列與側欄 Worker 清單的每一列都有「退出」。
  - 執行中的 worker 會先問一次（這一輪會被中斷），其他狀態直接退出。
  - 啟動失敗（failed／rejected）的 worker 也能退出。
  - 退出後分頁立刻停用輸入，該列從清單消失。
- **被別人控制中**：worker 的控制權在非 Purdex 的程式手上（例如 Ploom agent）時，退出會被拒絕，並寫出是誰在控制。
- **接到終端機後退不掉**：「接到終端機」或「接回原終端機」成功了、但 worker 沒能退出時，會出現常駐提示「已在終端機接續，但 worker 未能退出，請到清單手動退出」。
- **在終端機手動 `claude --resume`**：若這段對話正被 worker 使用，daemon 會讓 worker 退出，畫面跳出「<名稱> 已在終端機接續，worker 已退出」。名稱優先用清單上的標題。
- **交接的錯誤訊息講清楚原因**：
  - 對話已在終端機或另一個 worker 裡；
  - 正在轉移中；
  - 暫時無法確認對話是否被接走；
  - worker 控制權一直在變動。
  - 「已封存」的提示改成「這個 worker 已退出，可改用『接到終端機』」。
- **防重複**：
  - 同一個 worker 從清單和分頁同時按退出，只會送出一次。
  - 退出進行中不能送訊；送訊進行中也不能退出。

## [1.0.0-alpha.493] - 2026-10-06

> 只動 SPA，走 HMR 即時生效；daemon 和 Electron 都不必更新。

### Changed：對話主體 P1b-1——Worker 清單只列活著的對話、一段對話一列（#1592）

- **清單抓得完整**：側欄 Worker 清單改成跟著 Nexen 的分頁一路抓完，每頁 500 筆、最多 20 頁。以前只抓一頁 100 筆，而 Nexen 是由舊到新排序，所以超過 100 筆的主機只看得到最舊的那些。
  - 未歸檔的紀錄超過 10,000 筆時，清單照樣顯示已抓到的部分，並在上方常駐一行「未歸檔的執行紀錄超過 10,000 筆，最新的可能沒有列出」。實務上幾乎碰不到，因為 worker 退出就會歸檔。之後若 Nexen 支援由新到舊分頁（wake/nexen#119），就不再有這個限制。
  - 伺服器回了格式不對的頁面時，這次更新算失敗，畫面保留上一次的清單。分頁的 cursor 重複時，就停在那裡，保留已抓到的部分並去掉重複。
- **只列活著的對話，一段對話一列**：
  - 已退出（terminated 或已歸檔）的不再出現。
  - 啟動失敗（failed、rejected）的仍會列出，要等退出才消失。
  - 同一段對話接手過好幾次時，只顯示最新的那一段。
- **狀態點顏色統一**：Worker 清單、Nex 管理表和 worker 分頁標題用同一套顏色，跟 terminal 的 agent 標示一致：執行中綠、排隊黃、閒置與已結束灰、失敗紅。清單裡的「閒置」從琥珀色改成灰色，分頁標題的「已結束」從紅色改成灰色。

## [1.0.0-alpha.492] - 2026-10-06

> 只動 daemon。**daemon 尚未部署**：跟 alpha.490／491 一起，由統籌安排一次重啟。對話主體 P1a 的 daemon 部分到這版全部完成。SPA 要到 P1b-2 才會對應新的錯誤碼和「worker 已退出」通知；在那之前 SPA 照舊運作，Electron 也不必更新。

### Changed：對話主體 P1a-4——交接先確認擁有者，在終端機手動 resume 會讓 worker 自動退出（#1590）

- **交接到 worker（handoff）**：
  - 先確認這段對話沒有在別的終端機或另一個 worker 裡。否則回 409 `session_owned`；同一段對話正在轉移時回 409 `transfer_in_progress`；無法確認時回可重試的 503 `owner_check_failed`。這些檢查都在停掉 CC、送出任何按鍵之前。
  - Nexen 拒絕時，回應多了 `execution_id` 與 `exited`。如果拒絕後已經回復原狀，那筆 worker 就直接退出；沒有回復原狀的就保留下來，之後讓 pane 顯示「啟動失敗」（P1c）。
  - 新建的 worker 會帶 `purdex.session_id` 標籤，標明它屬於哪一段對話。
- **在終端機手動 `claude --resume` 一段正被 worker 用的對話 → worker 自動退出（Q1）**：
  - 只有在 daemon 再確認一次「這段對話現在確實在這個終端機裡」之後才動作。所以一次失敗的轉移剛關掉的終端機，就算之後才送來啟動訊號，也不會誤退 worker。
  - Purdex 自己的轉移進行中時會先跳過，轉移結束後再重查。遇到鎖被占用或暫時性錯誤時，每 3 秒重查一次，最多 20 次。
  - 每退出一個 worker，就廣播一次主機事件 `nex-worker-exited`（給 P1b-2 的 toast 用）。
  - 啟動訊號積壓太多時，會改成整張表掃一次、逐段對話對帳，不會漏掉。
- **其他**：
  - 「接到終端機」與「接回原終端機」若在送鍵前發現有人接走，也會在放開鎖之後重查一次。
  - daemon 關閉時會先停下這些背景工作，最多等 3 秒，不會拖住重啟。

## [1.0.0-alpha.491] - 2026-10-06

> Daemon only; no SPA or Electron change. **The daemon is not deployed yet.** The coordinator batches the mlab restart with the other lines. Until then the status bar can still show `tmux:<name>` with agent「—」for sessions late in the alphabet.

### Fix: the peer inventory resolves every session within its budget (#1573 #1574 #1582 #1584)

- **Symptom.** The status bar sometimes showed `tmux:<name>` and agent「—」for a session that runs Claude Code. `pdx peers` listed it twice: once as a `tmux:` row with no agent, and once as a `?` registry row. It also printed `partial: N sessions not resolved within budget`.
- **Cause.** The local inventory (`GET /api/peers`, also used by send, deliver and reply) ran out of its 2 s budget. On mlab (21 sessions) every run hit the budget at about 2020 ms and left 4–12 sessions unresolved, always the ones late in the alphabet. The time went to:
  - asking tmux, once per session, for the session of every pane that has a frame: ≈ 1.6 s;
  - four `ps` forks per process read: owner walks plus every live registry entry.
- **Now, one pass per inventory:**
  - one process-table snapshot (`sysctl kern.proc.all` on darwin, no fork; one `ps -A` on Linux);
  - two `tmux list-panes -a` (enumerate, then re-confirm membership);
  - owner walks read only the snapshot's PPIDs;
  - the registry read uses the same snapshot.
- **Every answer stays the same as before.** The guards are kept:
  - PID plus start time identity;
  - the join-pane re-check;
  - the proxy depth;
  - "lookup failed" ≠ "no owner" (#988);
  - an expired deadline is no answer.

  In detail:
  - A registry verdict uses the snapshot only when it can vouch for the PID; otherwise it uses the old per-PID reader.
  - A malformed or failed pane listing is a failed lookup, never an empty one.
  - The 2 s budget is unchanged, and now also bounds the snapshot.
- `GET /api/sessions/{code}/provenance` answers through the same machinery with its own one-session pass.
- New primitives:
  - `agent.ProcessSnapshot` / `ProcessView`, field-for-field equal to the old reader, including argv escaping (non-ASCII argv falls back to `ps`), zombies, other users' processes, and DST;
  - `tmux.Executor.ListAllPanes`.

## [1.0.0-alpha.490] - 2026-10-06

> 只動 daemon。**daemon 尚未部署**：跟 alpha.487／488／489 累積在一起，由統籌安排一次重啟。SPA 的錯誤訊息要到 P1b-2 才會對應新錯誤碼，部署前 SPA 照舊運作。Electron 不必更新。

### Changed：對話主體 P1a-3——「接到終端機」與「接回原終端機」改成先 resume、成功後才退出 worker（#1586）

- **Take to terminal（take-to-terminal）**：
  - 先確認這段對話沒有在別的終端機或另一個 worker 裡。否則回 409 `session_owned`，並寫明在哪裡（終端機 pane 或 worker）。
  - 轉移期間持有 worker 的控制權，所以中途沒有人能送訊息進去。
  - 終端機 resume 成功之後，worker 才退出（terminate＋archive）。resume 失敗就什麼都不退出，原本「先封存、失敗再取消封存」的作法拿掉了。
  - 已退出或啟動失敗（rejected）的 worker 也接受，給下一段的「重建為終端機」用。
- **Take back（接回原終端機）**：流程同上，一樣在 resume 成功後才退出 worker。
- **控制權（D22）**：
  - lease 被另一個 Purdex 分頁握著時，daemon 會先替它釋放、再取一把只屬於自己的 lease。原分頁之後送訊會失敗，轉移結束（失敗時一定歸還）後重新取得控制就好。
  - 「退出」仍然只借用。
  - 非 Purdex 的持有者照舊回 `held_by`，什麼都不動。
- **防止同一段對話被 resume 兩次**：
  - 送出 resume 指令前會再確認一次擁有者。
  - 成功後最多等 3 秒，直到這段對話的終端機 frame 出現。
  - 之後 10 秒內，同一段對話的轉移一律當作「已在終端機」擋下。
- **回應**：多了 `exited`（呼叫結束時 worker 是否已退出），以及在 resume 成功但 worker 退不掉時才出現的 `exit_error`。`archived` 欄位保留。
- 讀不到啟動時間的終端機程序不算擁有者，但也不能確定沒人在用，所以回可重試的 503 `owner_check_failed`。

## [1.0.0-alpha.489] - 2026-10-06

> Both daemon and SPA change; Electron needs no update. **The daemon is not deployed yet.** The new binary has to go in with one restart, together with alpha.487/488 and scheduled by the coordinator. Only after that does the new "重新啟動 daemon" button work. On an older daemon the button reports「這台 daemon 版本不支援遠端重啟」. The SPA goes out through HMR.

### Feature: restart the daemon from the SPA (#1568 #1570 #1575 #1579)

- **Three entry points:**
  - On the host page: Overview → "Daemon 設定" →「重新啟動 daemon」(shown for a connected host).
  - On the Nex config page: a「立即重啟」button next to the hint that says a restart is needed after saving.
  - Settings → Development → local daemon. While the App-managed daemon is running, restart is always available and can sit next to "更新". A daemon not started by the App gets the button once it is in the host list and responding.
- **Confirm first.** The dialog says terminal connections drop for a few seconds and reconnect by themselves, and tmux sessions are not affected. If workers are running it adds「N 個 worker 正在執行，重啟會中斷它們這一輪（之後可以繼續對話）」. If the count cannot be checked it shows「無法確認…」instead of hiding the warning.
- **While restarting.** Every entry point for that host spins and is disabled.
- **Done.** Done means the daemon answers with a new `boot_id`. The toast is「<主機>：daemon 已重新啟動」. If the shutdown hit cleanup errors, it adds「但關閉時有 N 個警告」.
- **Failure.**
  - If the daemon is not back within 60 seconds, an error points to `~/.config/pdx/logs/pdx.log` on that host.
  - An old daemon gets "pdx stop && pdx start".
- **Stale confirm dialogs are invalidated.** A pending count or an open dialog is cancelled when the host changes, when another entry point already restarted that host, or when the page locks it. It never restarts the wrong host or restarts twice.

### Daemon: `POST /api/daemon/restart`, boot_id, re-exec in place (#1568 #1570)

- **New endpoint** (host token). It replies `202 {boot_id}`, runs the normal shutdown, then re-execs in place with the executable path, argv and environment captured at startup.
  - The pid stays the same, so the App ownership record and the pid file stay valid.
  - `PDX_DEV_MODE` and similar settings are kept.
  - A binary swapped on disk is picked up.
  - Other responses: during a restart, `409 restart_in_progress`. Once shutdown has started, `503 shutting_down`. Without a hook, `503 restart_unavailable`.
- **`/api/health`** gains `boot_id`, which is new on every process start.
- **pid lock handed across the exec.** The fd stays open, and close-on-exec is cleared only right before the exec under ForkLock, so a concurrent `pdx start` cannot grab the lock. If `pdx stop` arrives during the restart's shutdown, it becomes a plain stop with no re-exec.
- **Cleanup errors during shutdown.** The daemon still restarts, which is equivalent to `pdx stop` followed by `pdx start`.
  - The errors are written to `<data_dir>/last-shutdown.json` (0600, exclusive tmp file, then rename).
  - The new process consumes the file once by rename, then reports it on `/api/info` `last_shutdown`, tied to the new `boot_id`.
- **Known limitation.** If the binary on disk is rolled back to a version before this one, a remote restart leaves the daemon down. Run `pdx start` on that host.

### Internal

- spec/plan: `docs/specs/2026-10-06-daemon-restart-spec.md`, `docs/plans/2026-10-06-daemon-restart-plan.md`.
- Integration test `go test -tags integration ./cmd/pdx/ -run TestRestart_ReexecKeepsPidNewBootID` runs a real serve → restart. It checks:
  - the pid is unchanged;
  - `boot_id` is new;
  - the env is kept;
  - the lock never goes free.
- Follow-ups (test hardening, edge cases): #1569.

## [1.0.0-alpha.488] - 2026-10-06

> 只動 daemon，目前沒有 UI 會呼叫它（SPA 的「退出」在 P1b-2）。**daemon 尚未部署**：跟 alpha.487 一起累積，由統籌安排重啟。SPA 與 Electron 都不必更新。

### Internal：對話主體 P1a-2——退出 worker（#1578）

- **新端點 `POST /api/nex/executions/{id}/exit`**，body 可選帶 `{lease_id}`。回 `{exited, terminated, archived, state}`。做法依 spec §5：
  - running、idle、queued 先 terminate 再 archive；failed、rejected 只 archive；terminated 但沒 archive 的補 archive。
  - 已經退出的再按一次，不會碰 engine。
  - terminate 失敗也照樣嘗試 archive（D4），所以 worker 一樣會算成已退出。
- **控制權（lease）**：
  - 先用 caller 帶來的 lease，但會先驗證；過期就改走重新取得。
  - 再來是自己取一把，用完就還。
  - 被另一個 Purdex 分頁握著時，借用它當下的 lease（不歸還）。
  - 被非 Purdex 的身分握著時，回 409 `held_by`，什麼都不動。
  - 在讀取與使用之間換手時會重新判斷，並在 archive 前再確認一次持有者。
- **找出某個對話所有活著的 worker**：分頁掃描 embedded Nexen（每頁 500、最多 20 頁）。同一個 session id 符合 `session_id` 或 `resume_session_id` 的、沒 archive 也沒 terminated 的，就算這個對話的 worker。
- 已知限制：「確認持有者」和「archive」不是同一個原子操作。目前的部署裡所有 lease 持有者都是 Purdex 自己簽的身分，所以不會發生；真要完全封死，需要 Nexen 提供附 lease 圍欄的 archive。

## [1.0.0-alpha.487] - 2026-10-06

> 只動 daemon，使用者看不到變化。這是「一個對話＝一個主體」的第一段，後續的 worker 退出與擁有者檢查會用到它。**daemon 尚未部署**：要等後面幾段累積完，由統籌跟使用者約時間一起重啟。SPA 與 Electron 都不必更新。

### Internal：對話主體 P1a-1——終端機查詢、SessionStart 訂閱、transcript_path（#1572）

- **transcript_path**：Claude Code 每個 hook 都帶 `transcript_path`，現在會存進 `agent_frames.transcript_path`（新欄位，啟動時自動補）。寫入順序跟 session identity 相同（`identity_seq`），也放進 provenance 信封與 `GET /api/sessions/{code}/provenance` 的回應。
- **依 session id 查終端機**：新增 `agent.terminal-sessions` 服務，`LiveBySessionID(ctx, agentType, sid)` 回傳記錄著這個 session id 的 root frame。只回 pid 還活著、啟動時間對得上的；讀不到啟動時間的會標成 `Verified=false`，不算 owner（spec D1）。
- **SessionStart 訂閱**：`SubscribeSessionStart(fn)`。只有真正的 SessionStart、拿到 provenance 信封、而且 session identity 確實寫進 frame，才會發出事件。每個訂閱者有自己的佇列和消費者：同一個 session 只留最新一筆，最多 1024 個 session，超過時送一筆 `Overflow` 事件代表「全部重查」。發布不會擋住 hook，事件也不會遺失。
- spec／plan：`docs/specs/2026-10-06-conversation-entity-spec.md`、`docs/plans/2026-10-06-conversation-entity-plan.md`（#1571）。

## [1.0.0-alpha.486] - 2026-10-06

> 只有 SPA，走 HMR；daemon 與 Electron 不必更新。標題列與狀態列 mode 鈕開出的確認框，請在 Electron 上點一次確認（見 #1566 的 review 紀錄）。

### Feature：側欄 Worker 清單加上標題列與關閉鈕（#1566）

- 寬版側欄的 Worker 區塊頂端多一行「Workers」，右邊是 ×，按下就收起清單，跟按底部的閃電鈕一樣；捲動清單時標題列不動。窄版的浮動面板本來就有，兩邊現在一致。
- 標題列算在原本可拖曳的高度內，記住的高度不變；最小高度 96 時清單區仍有 66 px。

### Fix：點殼層按鈕不再搶走 pane 的 focus（#1566）

- 以前用滑鼠點底部按鈕（新增工作區、Workers、主機、設定、收合切換）、標題列（版型、側欄切換）或狀態列（terminal／worker／chat、可複製的欄位、重新整理）之後，focus 會停在按鈕上：下一個按鍵就冒出外框，按空白鍵還會再觸發一次（例如 Worker 清單又被切換）。現在點完 focus 留在原本的 pane，繼續打字就會進到 pane。鍵盤操作（Tab、Enter、空白）不變。
- 確認框（套用版型、交給 nex 等）打開時會拿走 focus，Tab 只在框內循環，Enter 不會誤送出 pane 裡的草稿；關掉後 focus 回到原本的 pane。
- 切到別的分頁時，舊分頁裡的 focus 與選取範圍會放掉。Safari／WebKit 以前會把之後的按鍵打進已經看不見的輸入框，現在不會（Electron 本來就不會）。

### Internal

- 新增 `lib/keep-focus.ts`（mousedown 時 `preventDefault`），套在殼層按鈕上。
- `ConfirmDialog`：開啟時 focus 面板本身（不預選按鈕）、Tab 圈在框內、關閉時 focus 仍在框內才還原。
- `TabContent`：分頁從 active 變 inactive 時，若 focus 或選取範圍在它裡面，就 blur 並清掉選取範圍（在 layout effect 執行，早於新分頁的 activation focus）。
- spec／plan：`docs/specs/2026-10-06-shell-polish-spec.md`、`docs/plans/2026-10-06-shell-polish-plan.md`。

## [1.0.0-alpha.485] - 2026-10-06

> 要重新部署 daemon（內嵌 Nexen 升到 v0.16.1，不必刪 nex.db）；mlab 已在 2026-10-06 部署（`6e6ed9c7`）。SPA 走 HMR。air26 的 Nexen 是關的，不受影響。

### Feature：交接前內容裡，在終端機貼上的文字收成可展開的區塊（#1564）

- 在終端機貼上的大段文字，不再連同 `<pasted_content …>` 標籤整段攤開，改成一個標題「貼上的文字 · N 行」的區塊。短的直接全部顯示，長的只露出開頭幾行，點一下看全文；同一則訊息裡自己打的字照常顯示在前後。
- 被 daemon 截斷（超過 64 KB）的貼上，標題寫「N+ 行」，後面一條「只顯示前 64 KB（共 75 KB）」提示，數字是整則訊息實際顯示的大小。
- 搜尋（Cmd+F）找得到貼上內容裡的字，命中時區塊自動展開。
- room 和 chat 都一樣；chat 裡貼上的區塊在你這一側（右邊）。只影響交接前的內容，worker 自己的對話不變。

### Fix：交接前內容補上 `/model` 這類指令、`!` 大輸出不再露出標籤（#1563）

- 在終端機打的 `/model` 這類指令（Claude Code 2.1.289 改用另一種紀錄格式）現在會顯示在交接前內容裡，下面接它的輸出（例如「Kept model as Opus 5.5」）。
- `!seq 1 20000` 這種超大輸出不再露出 `<persisted-output>` 標籤，直接顯示「Output too large (106.3KB)…」與預覽。

### Internal

- Nexen pin v0.16.0 → v0.16.1（`system`/`local_command` 行改寫成 slash command＋`command_output` note；bash 串流裡的 `<persisted-output>` 包裝剝一層；schema 仍是 v6）。
- `lib/nex/pasted-text.ts`：照 Claude Code 2.1.289 自己的解析規則拆貼上內容。結尾標籤帶同一個 id（`</pasted_content id="hhhh">`），所以內容裡字面上的結尾標籤不會提早結束；第一版誤以為結尾不帶 id，review 後讀 CLI 原始碼才更正。截斷提示用 SPA 內部欄位 `shown_bytes` 報整則訊息的大小。
- spec §2 新增使用者決定 U3（貼上文字的呈現）、§5.3 對應規則；plan Task 11。
- follow-up：nexen #112（剝掉 `<persisted-output>` 後內文開頭留一個換行）。

## [1.0.0-alpha.484] - 2026-10-06

> SPA 走 HMR；daemon 要是內嵌 Nexen v0.16.0 的版本（#1554，482 起）。mlab 已在 2026-10-05 部署（`3d7390af`）。air26 的 Nexen 是關的，不受影響。

### Feature：交接來的 worker 正式顯示交接前的對話（#1548 #1554 #1558 #1561）

- 476／477 先上的「交接前對話」，在 daemon 換成內嵌 Nexen v0.16.0 後正式啟用：從終端機「Hand to nex」交出去的 worker，第 1 個 turn 上方會畫出這段對話在 transcript 裡更早的全部內容，往上捲到頂會自動載入更早的部分，room 和 chat 都有，搜尋（Cmd+F）也找得到。
- 交接前內容的最下方、worker 第 1 個 turn 正上方，多一條「Headless（worker）」分界線，標出從終端機交接進這個 worker 的位置；中間來回交接過幾次，每個切換點都有一條線。
- 在交接前內容中間切換 room ⇄ chat，會停在同一段內容，不再跳到最底（#1534）；pane 被收起時才載入的較舊內容，也不會讓回來時的位置跑掉。
- 搜尋列的「更早的內容尚未全部載入 · 全部載入」補上中間的點。
- 已知缺口：在終端機打的 `/model` 這類指令還不會出現在交接前內容裡，`!` 指令的大輸出會露出 `<persisted-output>` 標籤，這兩項等 Nexen v0.16.1；在終端機貼上的大段文字會連同 `<pasted_content>` 標籤整段畫出來，下一版改成可展開的「貼上的文字 · N 行」區塊。

### Internal

- Nexen pin v0.15.0 → v0.16.0（`GET /v1/executions/{id}/prelude`、`transcript_prelude` capability；schema 仍是 v6，不必刪 nex.db）。
- 契約測試：Nexen 早期 golden page（`__fixtures__/prelude-golden-nexen.json`）與 mlab 真實 capture（`prelude-06GGS8J1….json`，兩段 assistant 文字去識別化）各一份 replay。
- 捲動記憶加錨點（`ScrollMemo.anchor`）：prelude 元素帶 `data-prelude-pos`／`data-prelude-poses`，錨點元素共用 `scroll-anchor` class，從即時清單二分搜尋，不在每個 scroll 事件掃 DOM。
- spec §7 真機驗收紀錄在 #1558；spec §4.3 補上 v0.16.1 的契約（`system`/`local_command`、`<persisted-output>`），§4.2／§8 的存取措辭改成與 `/events` 相同的信任模型。
- follow-up #1543（ExecutionView 職責拆分）、#1557（封存的 worker 分頁標題）。

## [1.0.0-alpha.483] - 2026-10-05

> 純 SPA，不必重新部署 daemon。殼層整理最後一支（P7／7）。這個版本同時包含 worker prelude 線的 #1558，內容見該 PR。

### Feature：標題列版型鈕會亮出目前版型，收掉 pane 前先問（#1556）

- 標題列的單一／左右／上下三顆鈕，目前的版型那顆會亮；按亮著的那顆不會有動作。（瀏覽器版沒有標題列，分割請在 pane 上按右鍵。）
- 換成 pane 數較少的版型時，決定留下哪些 pane：
  - 有內容的 pane 本來就放得下 → 直接套用，空格補空白 pane。
  - 分頁裡剛好有那麼多個 agent pane（跑 Claude Code／Codex 的終端機、worker）→ 留下它們，先列出會關掉的 pane 請你確認。
  - 其他情況 → 跳出選擇視窗讓你勾要留下哪些（預設勾最近點過的），勾滿才能確認。
- 確認框會說明：session 與 worker 會在背景繼續、可以再打開；未儲存的編輯器內容會遺失。
- 對話框開著時切到別的分頁會自動取消；按確認時若 agent 狀態或 pane 已經變了，會改問新的問題，不會照舊的計畫套用；那個新問題的確認鈕會先鎖半秒，雙擊不會誤按。

### Fix

- 確認對話框蓋到 Electron 標題列時，點一下不會再變成拖動視窗。

## [1.0.0-alpha.482] - 2026-10-05

> 殼層整理第五支（P6／7）是純 SPA。這個版本同時包含 #1554（daemon 內嵌 Nexen v0.16.0、`transcript_prelude`），它的部署與驗收由 worker prelude 那條線處理，見該 PR。

### Feature：狀態列跟著 agent pane，並可切換 terminal／worker／chat（#1553）

- 狀態列拿掉兩顆分割鈕（分割改用標題列或在 pane 上按右鍵），換成 **terminal／worker／chat** 三顆，目前是哪種就亮哪顆。
  - 終端機 → worker／chat：開「交給 Nexen」對話框（chat 會在交接後直接用聊天模式）。只有跑著 Claude Code、而且這台主機的 Nexen 已就緒時才能按，否則灰掉並說明原因。
  - worker → 終端機：跑這個 worker 自己的「接回終端機」（執行中會先確認）。
  - 指揮室 ↔ 聊天：直接切換。
- 分頁有分割時，狀態列**整條**跟著 agent pane（跑 Claude Code／Codex 的終端機、worker）：點純終端機或編輯器不會讓狀態列離開 agent pane；兩個 agent pane 時跟著你點的那個；分頁裡沒有 agent pane 時跟著最後點過的 pane。
- worker 有自己的狀態列：主機、名稱、工作目錄。

### Fix

- 「交給 Nexen」確認框開著時，如果 Claude Code 退出、session 終止、或 Nexen 不再就緒，確認框會自動關閉；按下確認前也會再檢查一次，不會送出不該送的交接。右鍵選單在 Claude Code 退出後不再提供「交給 Nexen」。
- 切換指揮室／聊天時同時核對主機，pane 剛好被換成另一台主機上同 ID 的 worker 時不會改到它。

### Internal

- `useStatusTargetPane`／`isAgentPane`、`components/status/*`（從 `StatusBar` 抽出的片段與 `WorkerStatusBar`、`PaneModeButtons`）、`useHandoffDialogStore`＋`HandoffDialogHost`（對話框拉到 App 層）、`useHandoffGate`（附原因）、`handoffBlockReasonNow`、`take-to-terminal-registry`、`setExecutionPaneMode`。

## [1.0.0-alpha.481] - 2026-10-05

> 純 SPA，不必重新部署 daemon。殼層整理第四支（P5／7）。

### Fix：切換分頁時 focus 回到你最後點的 pane（#1551）

- 以前分頁切回來時，每個 pane 各自搶 focus，最後一個（通常是右下）贏。現在只有「這個分頁最後點過的 pane」會拿到 focus；沒點過（新分頁、或那個 pane 已經關掉）就是主 pane。重新整理後一律從主 pane 開始。
- 在同一個分頁裡點另一個 pane，不會再有任何 pane 主動把 focus 搶回去；切回分頁後在下一幀之前就點了別的 pane，也不會被搶回。
- worker 的回覆框：只有**自己這次送出完成**時才回 focus；連線恢復、歷史載入完成、接回終端機結束等都不會搶 focus。打開一個還在載入的 worker 時，回覆框會在可以輸入的那一刻拿到 focus（若你沒在別處打字、也沒切走）。
- 終端機、Monaco、Tiptap 都套用同樣的規則。

### Internal

- `usePaneFocusStore`（只在記憶體；分頁從 tab store 消失就清掉紀錄）、`lib/pane-focus.ts`（`focusTargetOf`、`statusTargetOf`，後者給下一支狀態列用）、`useActivationFocus` hook、`PaneRendererProps.isFocusTarget`。

## [1.0.0-alpha.480] - 2026-10-05

> 純 SPA，不必重新部署 daemon。殼層整理第三支（P3＋P4／7）。

### Removed：4 區側欄（#1549）

- 左一左二、右一右二四個可收合的側欄區塊整套拿掉，標題列左邊對應的 4 顆開關也一併拿掉；標題列右側只剩單一／左右／上下三顆版型鈕。
- Worker 清單已在 478 搬到左側欄底部的 Workers 鈕，功能不受影響。
- 設定 › 介面拿掉「Pane」「Sidebar」兩個「即將推出」的空分頁，只剩「新分頁」。

### Internal

- 刪除 `SidebarRegion`／`RegionManager`／`RegionContextMenu`、`types/layout.ts`、`useLayoutStore` 的 `regions` 與所有 region action、`module-registry` 的 views API（`ViewProps`／`ViewDefinition`／`getAllViews` 等）與相關 locale key。舊資料裡殘留的 `regions` 會在下一次寫入時自然消失，不需 migration。

## [1.0.0-alpha.479] - 2026-10-05

> 純 SPA，不必重新部署 daemon。殼層整理第二支（P2／7）。

### Removed：工作區檔案樹與「專案路徑」設定（#1546）

- 拿掉側欄的「檔案（工作區）」與「檔案（Session）」兩個 view，以及工作區設定裡的「專案路徑」。專案改由主機管理 › 專案維護（兩者資料不共用，主機的專案清單不受影響）。
- 模組開關裡不再有「Files」。
- 找不到檔案的彈窗拿掉「搜尋工作區」：它依賴上面那個專案路徑，而且 daemon 對這種搜尋本來就回 501。在 session 目錄裡搜尋照舊。

### Internal

- 整個 `files` module、`FileTreeView`／`FileTreeSessionView`／`FilesWorkspaceSettingsSection`、file-tree 專用的 open service、`PopupSpec.layer3Hits`、`fs-search` 的 `workspace-projectPath` root kind 與只為合併兩種搜尋存在的緩衝都已刪除；SR-2 測試改掛在 editor 的 `workspace-home-path`。

## [1.0.0-alpha.478] - 2026-10-05

> 純 SPA，不必重新部署 daemon。殼層整理第一支（P1／7）。

### Feature：側欄的 Workers 清單、底部按鈕可收成一排（#1540）

- 左側欄底部新增 **Workers** 鈕。寬版時 worker 清單出現在工作區清單**下方**，兩者同時看得到；中間的分隔線可以拖曳調整高度，會記住。
- 清單列出這個工作台顯示中的**每一台主機**，一台一段（段頭是主機名和 Nexen 狀態點）；沒開 Nexen 的主機不顯示。
- 點 worker：已經開著就切過去（連同它所在的工作區，不會被搬走）；沒開過就在**目前的工作區**開新分頁。修正了以前會被搬進「未分類」的問題。
- 窄版側欄按 Workers 會在側欄右邊開浮動面板，底部對齊按鈕，不會改變寬窄設定。
- 寬版底部的按鈕可以切成「一橫排只剩圖示」，用區塊上的小箭頭切換，會記住；側欄太窄時會自動換行，按鈕不會被壓扁。
- 視窗太矮時清單只是暫時被壓縮，存的高度不變；拖曳時存的是放開那一刻畫面上的高度。

### Fix

- 分隔線拖曳中切到別的視窗、或在視窗外放開，不再殘留拖曳狀態（游標、禁止選字、之後滑鼠移動還在改大小）；pane 分割的分隔線也一併受惠。

### Internal

- `openWorkerTab`（`features/workspace/lib/open-worker-tab.ts`）、`WorkerList`、`BottomNav`（寬／窄共用）、`useWorkerListResize`。
- `FloatingPanel` 新增 `placement='right'`：render 後量實際高度再定位，內容長高時重新對齊。
- `useLayoutStore` 新增 `workerListOpen`／`workerListHeight`／`bottomNavCompact`，只在同一裝置跨視窗同步，不進 Profile Sync（有守門測試）。
- spec／plan：`docs/specs/2026-10-05-shell-cleanup-spec.md`、`docs/plans/2026-10-05-shell-cleanup-plan.md`。

## [1.0.0-alpha.477] - 2026-10-05

> 純 SPA，不必重新部署 daemon。和 476 一樣，要等 daemon 內嵌 Nexen v0.16.0（`transcript_prelude`）才會出現；在那之前畫面與 475 完全相同。

### Feature（尚未啟用）：交接前的對話也支援 chat 模式和搜尋（#1541 #1542）

- 切到 chat 模式時，交接前的內容也照 chat 的規則顯示：同一段的工具操作收成一行「使用了 N 個工具」，展開後逐條列出；thinking 不顯示，和 worker 自己的對話一致。
- 被 daemon 截斷的工具呼叫與結果，在 room 和 chat 都會在該工具區塊裡註明只顯示了多少，包括 chat 的「編輯」行。
- 搜尋（Cmd+F）會從已載入的交接前內容開始找，順序和畫面一致，跳到收合區塊裡的結果會自動展開。
- 交接前的內容還沒全部載入時，搜尋列下方會顯示「更早的內容尚未全部載入 · 全部載入」。按「全部載入」會一路往前讀到最早，期間顯示「載入更早的內容…」；先前載入失敗的話會先重試。

### Internal

- chat：每一段的畫法抽到 `components/chat/ChatTurnBody.tsx`（純搬移，helper 逐位元組相同），由 `ChatTranscript` 和 `PreludeSection` 共用；prelude 段落的 key 用該段**最後一則**訊息的 id（頁面只會從前面長，段尾不動）。截斷提示移進 `OperationAt`，room 與 chat 共用。
- 搜尋：`buildSearchUnits({ prelude })` 先走 prelude，id 用穩定的 `p<pos>`，畫法與 `PreludeSection` 對齊；room × chat 錨點一致性測試涵蓋 prelude。
- spec §5.3 修正：截斷提示只出現在畫面有畫出的區塊（chat 不畫 thinking，就不提示）。
- follow-up #1539（只有圖片的使用者訊息不會在 chat 開新段）、#1543（ExecutionView 職責拆分）。

## [1.0.0-alpha.476] - 2026-10-05

> 純 SPA，不必重新部署 daemon。功能要等 daemon 內嵌 Nexen v0.16.0（`transcript_prelude`）才會出現；在那之前畫面與 475 完全相同。

### Feature（尚未啟用）：交接來的 worker 會顯示交接前的完整對話（#1533 #1536 #1537 #1535）

- 從終端機「Hand to nex」交出去的 worker，第 1 個 turn 上方會直接畫出這段對話在 transcript 裡更早的全部內容，不管中間 terminal ↔ worker 來回過幾次。
- 開啟時仍停在最底部；往上捲到頂會自動載入更早的部分，載入時畫面不會跳動。
- 在終端機跑的段落和 headless（worker）跑的段落之間、對話被壓縮的位置，各畫一條細線標示。
- 指令輸出、`!` 指令、背景工作通知、peer 訊息都會顯示；圖片和文件顯示成 `[圖片 · png · 120 KB]` 這樣的佔位；被 daemon 截斷的內容會註明只顯示了多少。
- transcript 已被 Claude Code 清除或讀不到時，頂端會說明一行，worker 自己的對話不受影響。
- 不是交接來的 worker（從 NewTab 開的）完全不變。
- chat 模式的呈現與搜尋（Cmd+F）涵蓋交接前內容，下一版補上。

### Internal

- 資料層：`lib/nex/prelude-wire.ts`（API 邊界逐型別封閉清洗、client 資源預算）、`lib/nex/prelude.ts`（獨立的 `prelude` state slice，不碰 `messages`／`lastSeq`／`turnStarts`／成本；request id 守門、cursor 循環偵測）、`useExecutionPrelude`。
- 畫面：transcript 的 key 改走 `idOf`（`lib/nex/message-keys.ts`，缺省時與原本逐位元組相同）；`PreludeSection`／`PreludeAnchor`（以 `getSnapshotBeforeUpdate` 量 prelude 區塊高度修正捲動）。
- spec `docs/specs/2026-10-05-worker-prelude-spec.md`、plan `docs/plans/2026-10-05-worker-prelude-plan.md`；follow-up #1534。

## [1.0.0-alpha.475] - 2026-09-30

> 純 SPA，不必重新部署 daemon。

### Fix：同步狀態不再卡在「負責同步的視窗已經不在了」（#1530）

- 視窗被縮小或遮住一陣子再回來時，設定 › 工作台 › 同步會一直顯示「同步狀態不明」和「原本負責同步的視窗已經不在了」，即使只開了一個視窗。現在視窗一回到前景就恢復正常顯示。
- 開多個視窗時，其他視窗也會在負責同步的視窗恢復時立刻更新，不再停在過期的提示。

### Internal

- 根因：背景視窗的計時器被節流，同步 lease 在持有者活著時過期，持有者把自己的舊狀態判成 stale，之後沒有任何事件把它改回來。
- lease 續約取回自己已過期的 lease 時發出 `onRecovered`，由同步層重新判斷；stale 的 follower 會在 lease 變動時重新讀取，另有 2 秒起、最長 60 秒退避的保險檢查。
- follow-up #1531（`start.ts`／`sync-status.ts` 拆檔）。

## [1.0.0-alpha.474] - 2026-09-29

> 純 SPA，不必重新部署 daemon（需要 473 起的 daemon，也就是內嵌 Nexen v0.15.0）。

### Feature：圖片直接傳給 worker（#1527 #1528）

- 附加的圖片（png／jpeg／gif／webp）現在直接傳給 agent，它在第一輪就看得到圖，不必再自己去讀檔。其他檔案照舊用路徑引用。
- 可以只送圖片不打字。
- 送出後你的訊息會帶縮圖；重新載入、切到別的分頁再回來也看得到，點縮圖可以開大圖。
- 超過張數或大小上限的圖片會自動改走路徑上傳，附件上會註明；如果文字加圖片太大，會擋下送出並說明原因，不會送出去才失敗。
- 舊 daemon 或 codex worker 不支援直接傳圖時，行為和 472 一樣（全部走路徑）。
- 圖片還在讀取時移除它，就不會被送出；送出期間輸入欄與快速回覆會暫停。

### Internal

- 縮圖以 Nexen 附件端點取回，最多同時 4 張、每張 30 秒逾時，離開畫面會中止下載；只接受圖片格式。
- 預覽縮圖的暫存網址由 execution store 統一回收。

## [1.0.0-alpha.473] - 2026-09-29

> **需要重新部署 daemon**（內嵌 Nexen 升到 v0.15.0；schema 仍是 v6，**不必**刪 `nex.db`）。

### Feature：worker 分頁標題優先用 Claude 的 session 標題（#1525）

- 從 terminal 交接過來的 worker，分頁標題改用 Claude 自己產生的 session 標題（例如「Nexen worker pane 顯示修正」）；你在 terminal 用 `/rename` 取的名字優先。沒有標題的 worker 照舊用交接前的終端機標題或任務第一行。
- 通知的標題和分頁標題一致。
- 舊 daemon 沒有這個功能時，標題維持原本的規則。

### Internal

- 內嵌 Nexen v0.13.2 → v0.15.0：帶來圖片附件（v0.14.0）與 session 標題（v0.15.0）。圖片直接傳給 agent 在下一版接上。
- SPA 能讀新的能力欄位；圖片能力在缺少請求大小上限時一律視為不支援。

## [1.0.0-alpha.472] - 2026-09-29

> **需要重新部署 daemon**（新增上傳端點 `POST /api/nex/executions/{id}/uploads`）。舊 daemon 下 worker 的上傳會失敗並在附件上顯示錯誤，其他功能不受影響。

### Feature：worker 可以附加檔案（#1520 #1521 #1522）

- 在 worker 輸入欄可用三種方式附加檔案：拖到 pane 任何位置、直接貼上、按 `+` 選檔。
- 每個檔案顯示成可移除的附件（上傳中／完成／失敗，圖片有縮圖）；上傳中或失敗時不能送出，並顯示原因。送出後附件會以 `[file: <路徑>]` 附在訊息下方，agent 會自己讀取。
- 檔案存在 worker 專案目錄的 `.purdex-uploads/` 底下（這個資料夾自帶 `.gitignore`，版本控制看不到）。這是因為 worker 預設的權限讀不到專案以外的檔案。
- 快速回覆也會帶上已完成的附件；上傳中時快速回覆一樣會停用。
- 從 Office 等富文字 app 貼上時只貼文字，不會多上傳一張圖片。

### Internal

- 上傳限制：單檔 50 MiB；專案目錄不存在、worker 已結束、路徑含換行等特殊字元時會拒絕；檔名中的控制字元與方括號會換成 `_`。
- follow-up：#1523（daemon 缺讀取逾時、上傳無總量限制；皆為既有問題）。圖片改為直接傳給 agent 在 phase E 接上（Nexen v0.14.0）。

## [1.0.0-alpha.471] - 2026-09-29

> 純 SPA，不必重新部署 daemon。

### Feature：側欄的 worker 分頁（#1517 #1518 #1516）

- worker 分頁現在有和 terminal agent 一樣的燈號：執行中（綠色呼吸燈）、完成（灰）、失敗（紅），執行中的 subagent 以小點顯示。規則和 terminal 完全相同，失敗後會維持紅燈，直到你送出下一則訊息。
- 未讀與桌面通知也和 terminal 一樣：不在畫面上的 worker 完成或失敗時會標未讀、跳通知，點通知會切到那個 worker 分頁。重新載入 app 不會重播舊通知。
- 分頁 icon 可在 設定 → Worker 選擇：provider logo（單色，沿用 Claude／Codex 圖示設定）、provider logo（原色）、自選 icon。先前 worker 分頁的 icon 是空白的，已修正。
- 分頁標題改成「交接前的終端機標題（沒有就用任務第一行） - 專案資料夾」，不再一律顯示「執行」。

### Internal

- worker 的 agent key 是 `<host>:exec-<executionId>`；只看 split 的主 pane 找分頁，和 terminal 一致。
- follow-up：Nexen `session_title`（v0.15.0）與圖片附件（v0.14.0）在 phase E 接上。

## [1.0.0-alpha.470] - 2026-09-29

> 純 SPA，不必重新部署 daemon。

### Fix：worker 的捲動位置會保留（#1514）

- 往上捲著看時，新的輸出不再把畫面拉回最底，搜尋列關著也一樣。只有原本就停在最底時才會跟著捲。
- 切到別的分頁再切回來、分頁被重新載入、room／chat 互切時，會回到原本的位置。room／chat 互切時會停在同一輪。
- 自動捲到底的途中滑動滾輪、觸控或拖捲軸，會馬上停止跟隨。

### Feature：每一輪結束時顯示耗時與完成時間（#1514）

- 每輪結尾多一行 `✻ Worked for 38m 2s · done 5:48 PM`，時間跟著系統的 12／24 小時制，hover 看完整日期。
- 失敗的輪次顯示紅色的 `✻ Failed after 12s · 5:48 PM`。逾時、daemon 關閉、額度用完造成的中斷也算失敗；你自己中斷或終止的輪次不顯示。
- 排隊送出的訊息也會對到正確的那一輪。

## [1.0.0-alpha.469] - 2026-09-28

> ⚠️ **Profile Sync 的 settings schema 從 8 升到 9**：新版裝置寫入設定後，還停在舊版的裝置會把 profile 鎖住（`locked:schema`），**所有裝置都要更新到這一版**。純 SPA，不必重新部署 daemon。

### Feature：worker pane 外觀（Purdex theme）（#1512）

- worker pane 新增外觀 theme 架構，目前只有一套「Purdex」；在 設定 → Worker 選擇，跟著 Profile Sync 同步。
- 字級改成和內文相同：h1–h6 不再放大、只加粗；行距改成接近終端機的密度，段落之間空一行。
- 有序／無序列表的編號與符號改成淡色，換行時對齊項目文字。
- markdown 表格現在會正確畫出（先前整張表變成一段文字）；太寬時在表格內橫向捲動。
- 你的輸入改成整列全寬的底色條、前面加 `›`，和終端機一樣。
- 工具與 subagent 前面的直線調亮。

## [1.0.0-alpha.468] - 2026-09-28

> **需要重新部署 daemon**（provenance 回應多了 `started_at`；舊 daemon 仍相容，只是補抓時間戳退回本機時鐘）。

### Fix：Profile Sync 無故衝突與「保留這台裝置的」撤銷操作（#1508）

- 兩台裝置同時開著時，agent 啟動／結束、自動補抓等由主機事件觸發的寫入不再互相衝突：時間戳改用主機（daemon）提供的時間，兩台寫出相同內容，同步主機會直接收斂。
- 衝突時按「保留這台裝置的」，保留的是**現在畫面上的內容**（包含鎖住後做的操作，例如重建 session），不再退回衝突當下的舊快照。原本這會把鎖定期間的重建撤銷，讓 pane 顯示「已關閉」。
- 衝突確認框移除「會復原你之後的變更」提示，「這台」的數量改算現在的內容。
- 較舊的 SessionStart 晚到時不再蓋掉較新的 agent 記錄；異常的 daemon 時間不會被採用。

### Internal

- daemon 的 session provenance 回應新增 `started_at`（frame 開始時間）。
- follow-up：#1507（移除已無路徑的 restore-local 機制）、#1509（選舉混用兩種時鐘）、#1510（debounce 期間確認保留會先推舊內容）。

## [1.0.0-alpha.467] - 2026-09-28

> **需要重新部署 daemon**（內嵌 Nexen 升到 v0.13.2；schema 仍是 v6，**不必**刪 `nex.db`）。worker pane R4 的最後一支，R4 全部完成。

### Feature：worker 列表的總覽（#1505）

- 側欄每個 worker 多顯示執行中背景工作的數量與成本（`$0.11`）；窄的時候只有描述會被截斷，不會換行。
- 狀態點 hover 顯示 worker 正在做什麼：「執行 Bash」／「思考中」／「啟動中」／「閒置」／「排隊中」。
- 接續既有對話的 worker，成本 hover 註明「含接續前的費用」。
- Hosts › Nex 的 worker 表多了成本、turn 數、最後工具、執行中四欄。
- 成本只在 daemon 採用新的成本規則（Nexen v0.13.2 起）時顯示，與 pane 裡的數字一致；較舊的 daemon 不顯示成本，其他欄位照常。

### Internal

- 內嵌 Nexen v0.13.1 → v0.13.2（列表成本改用與 Purdex 相同的規則）。
- 單筆 worker 查詢的總覽欄位也做格式檢查，與列表一致。

## [1.0.0-alpha.466] - 2026-09-28

> 純 SPA，不需要重新部署 daemon（新內容需要 alpha.464 以上的 daemon；舊 daemon 的 dock 與 subagent 照舊顯示）。worker pane R4 的第三支。

### Feature：dock 顯示執行中的背景工作（#1503）

- 指揮室的 dock 會列出 worker 正在跑的背景 shell 與 subagent，例如「▸ 2 執行中 ● pnpm dev (4m) ● tail -f log (2m)」，經過時間每 30 秒更新。
- 展開後每一列有「檢視」按鈕，會捲到啟動它的那個 Bash／Task 呼叫，必要時自動展開折疊。
- 斷線重連後會重新讀一次執行中的工作，補回斷線時錯過的結束。
- 目前沒有從 dock 停止背景工作的功能（Nexen 還沒有這個操作）。

### Feature：subagent 結束後顯示用量

- subagent 折疊列在結束後多顯示 tokens · 工具次數 · 耗時；執行中顯示經過時間。**不顯示成本**（provider 不回報 subagent 的花費）。
- 失敗是紅色「失敗」；被停止是「已停止」；行程結束時還沒收尾的是中性的「中斷」（不是失敗）。

### Fix

- 用「檢視」或搜尋跳到某處後，新進來的訊息不會再把畫面拉回底部；自己捲回底部，或關掉搜尋列，就恢復自動捲到底。
- 成本的多模型加總改為固定順序（與 Nexen 一致，兩邊算出同一個數）。

## [1.0.0-alpha.465] - 2026-09-28

> 純 SPA，不需要重新部署 daemon。worker pane R4 的第二支。

### Fix：worker 的成本不再重複計算（#1500）

- 每個 turn 回報的成本有時是「到目前為止的累計」（同一個行程裡的多筆結果；新版 CLI 跨 resume 也會累計），以前一律相加，多 turn 的 worker 會多算。
  現在一筆結果要有 token 數字證明它是前一筆的延續，才只算差額；否則照舊整筆計入。實錄的一個 worker 從 $0.26 修正為 $0.20。
- 同一條規則也用在 Nexen v0.13.2 的列表成本，之後側欄與 pane 會顯示同一個數字。
- 已知限制：有 subagent 的獨立 turn 接在很短的 turn 後面時，仍可能被當成延續而少算（#1501，等實錄資料再定）。

### Feature：接續既有對話的 worker 標出「含接續前的費用」

- 從 tmux session 交給 worker（hand-over）且第一個 turn 確實接續成功時，成本的 hover 與成本面板會註明「含接續前的費用」。
- 接續失敗、還沒有任何有費用的結果、或不是接續來的 worker，不顯示。

## [1.0.0-alpha.464] - 2026-09-28

> **需要重新部署 daemon，而且升級時要先刪掉 `~/.config/pdx/nex/nex.db*`**（內嵌 Nexen 升到 v0.13.1，資料庫 schema v6，alpha 期不做 migration；
> 不刪的話 worker 功能整個回 503，錯誤訊息會附上檔案路徑）。舊的 worker 紀錄會一起消失。
> worker pane R4（背景工作、subagent 收尾、列表總覽）的第一支，畫面還沒有變化。

### Internal：接上 Nexen v0.13.1 的背景工作資料（#1498）

- 內嵌 Nexen 從 v0.12.0 升到 v0.13.1：多了背景 shell／subagent 的開始與結束事件、`/tasks` 查詢、列表的成本／turn 數／最後工具／執行中數量／目前狀態。
- 背景工作事件記在各 worker 自己的工作表，**不會混進對話紀錄**（否則 daemon 一升級，turn 分組與搜尋就會多出看不見的項目）。
- 晚到的結束事件照樣會關掉那一列；背景工作事件不影響一般訊息的去重與重連接續點。
- 舊 daemon（沒有 `worker_rollup`）行為完全不變。

## [1.0.0-alpha.463] - 2026-09-27

> 純 SPA，不需要重新部署 daemon。worker pane R3（快速回覆與搜尋）的最後一支，R3 全部完成。

### Feature：在 worker 的對話裡搜尋（#1495）

- 在 worker pane 按 **Cmd+F**（Windows／Linux 是 Ctrl+F）打開搜尋列：顯示「3 / 12」，Enter 下一筆、Shift+Enter 上一筆、Esc 關閉。
  指揮室與聊天都能用；切換顯示方式時搜尋列與目前那一筆都會保留。
- **被折起來的內容也搜得到**：跳到折疊中的輸出、diff、思考或 subagent 時，會自動展開並標出來。
- 從**目前畫面往下的第一筆**開始，跟瀏覽器的尋找一樣；結果超過 10000 筆時保留畫面附近的，最新的內容也找得到。
- 搜尋列開著時，新訊息不會把你拉離正在看的那一筆；關掉搜尋列就恢復自動捲到底。
- 一個中文字（或注音、假名）就能搜，英文至少兩個字母；前後空白不算。markdown 以畫面上看到的文字為準（連結網址、程式碼語言標籤不算）。
- 同一個分頁分割成兩個 worker 時，Cmd+F 只開在你最後點過的那一邊；確認對話框開著時不會在它後面打開。
- 送出訊息後正在搜尋框打字，送出完成時不會把游標搶回回覆框。

## [1.0.0-alpha.462] - 2026-09-27

> 純 SPA，不需要重新部署 daemon（快速回覆的清單需要 alpha.461 的 daemon；舊 daemon 會顯示預設三則）。
> worker pane R3 四支 PR 的第二、三支。

### Feature：輸入欄上方的快速回覆（#1493）

- 指揮室與聊天的輸入欄上方多一排快速回覆按鈕（來自 Hosts › 指令 › 快速回覆，每台主機各自一份），**點一下就直接送出**。
- 輸入欄裡打到一半的字不受影響，送出失敗時也不會被蓋掉；失敗只顯示錯誤。
- 清單刪光就不顯示這一排；清單還在載入或讀不到時也不顯示，不會冒出你刪掉的預設回覆。
- 送出中、worker 已結束或連線中斷時，按鈕跟輸入欄一起停用。

### Internal：對話搜尋的基礎（#1492）

- 搜尋用的索引、展開與標示已就位（含折疊內容、markdown 以畫面實際顯示的文字計算、多個 pane 各自標示）；
  搜尋列與 Cmd+F 在下一版（R3-C2）才會出現，這版畫面上沒有變化。

## [1.0.0-alpha.461] - 2026-09-27

> ⚠️ **需要重新部署 daemon**（新增 host config 的 `quick_replies`）。舊版 daemon 仍可搭配新版 SPA：
> 設定頁會顯示「daemon 太舊」，不能編輯。worker pane R3（快速回覆與搜尋）四支 PR 的第一支。

### Feature：每台主機的快速回覆清單（#1483）

- Hosts › 指令 多一個「快速回覆」分頁：新增、就地編輯、刪除、上下排序；每台主機各自一份，存在該主機的 daemon。
- 還沒存過時先列出預設三個：`continue`、`run the tests`、`explain that`；第一次修改才會真的寫入。刪光就是不要快速回覆。
- 每則最多 1000 bytes、每台最多 20 則；前後空白會去掉。
- 輸入欄上方的快速回覆按鈕列在下一版（R3-B）才會出現。

## [1.0.0-alpha.460] - 2026-09-27

> 純 SPA，不需要重新部署 daemon。

### Feature：側欄直接看見、直接處理 Profile 同步衝突（#1482）

- 側欄頂部的 profile 區塊（Home 列）在 master 有任何被鎖住的同步區段時，會出現琥珀色警告圖示；點開列出每個衝突與原因：
  設定／工作區清單的衝突附「到設定處理」，某個工作區的分頁衝突附「前往工作區」，目前在本機 profile（slave）時則提示切回 master。
- 每個工作區列在它的分頁衝突時出現同樣的圖示（只在 master 在畫面上時），點開就能直接選「保留本機／採用主機」，
  沿用設定頁同一套確認與防護；解完圖示立即消失。Home 切換選單的 master 項目也會標示。
- 修正：浮動面板裡的確認框按 Escape 只關確認框，不再連同面板一起關掉。
- 僅寬版側欄；窄版不顯示。follow-up #1484／#1485／#1486。

## [1.0.0-alpha.459] - 2026-09-27

> **daemon 需要重新部署**（兩台）；SPA 也有變更（dev server 即時生效）。線上協定不變，新舊 SPA／daemon／Electron 內建前端任意搭配都正確。

### Fix：主機沒有 tmux server 時視為正常的空狀態（#1480，修 #1474，處理 #1108）

- 重開機後（或從沒開過 tmux）主機不再顯示黃色「tmux 環境無法連線」、不再跳通知，「＋」可以直接建立 session，建立時會自動把
  tmux 帶起來。只有 tmux 真的不能用（沒安裝、無法執行、卡住）才顯示警告並擋住建立。
- 前端一連上就會收到主機目前的 tmux 狀態；以前 daemon 啟動時 tmux 就不在的話，前端永遠不知道。
- 建立 session 時順便啟動了 tmux，daemon 會馬上完成後續準備（hooks、session 列表推送），不用等下一次 5 秒輪詢。

### Feature：分頁讀不到 session 清單時會說明原因（#1480）

- 主機連得上、但 10 秒內都拿不到 tmux session 清單時，還沒連上的分頁不再只顯示 `connecting...`，而是顯示「已連線到主機，
  但讀不到 tmux session 清單，會自動重試」，讀到後自動恢復。分頁換到另一台主機時重新計時。

## [1.0.0-alpha.458] - 2026-09-27

> **daemon 需要重新部署**（兩台）；SPA 無變更。

### Fix：主機重開機後，tmux 分頁不再永遠停在 connecting（#1477，修 #1473）

- 重開機後（或那台機器從沒開過 tmux）tmux 回報「沒有 server」的方式跟平常不同，daemon 以前認不出來，把它當成讀取失敗：
  session 清單一份都送不出去，這台主機的分頁永遠停在 `connecting...`、不會變成「已結束」，只能先到那台機器手動開一個 tmux session。
  現在 daemon 認得這種情況，會回報「目前沒有 session」，分頁會正確變成已結束、可以直接重建。
- 同一個判斷也套用到其他把「沒有 server」當正常狀況的地方（pane 偵測、tmux 設定讀取、hook 狀態讀取、`pdx msg selftest` 的清理）。
- tmux hooks（session 變動即時推送）現在會自動補裝：daemon 啟動時沒有 tmux server 的話，以前 hooks 永遠裝不上，session 列表只能靠
  5 秒輪詢更新；現在 server 一出現就會裝上，裝失敗會每 5 秒重試，tmux server 換成新的一代時也會重裝。在主機頁手動移除 hooks 後，
  這次 daemon 執行期間不會自動裝回。

## [1.0.0-alpha.457] - 2026-09-27

> 純 SPA，不需要重新部署 daemon。

### Fix：已結束的分頁內容過長時捲不到頂端（#1475）

- 重建卡片加上 session 清單超過視窗高度時，上半部（標題、重建卡片開頭）會被切掉、捲不上去；現在內容放得下照樣置中，放不下就改成靠上，整頁都捲得到。

## [1.0.0-alpha.456] - 2026-09-27

> 純 SPA，不需要重新部署 daemon。worker pane R2（chat 視圖）的第二支，R2 全部完成。

### Feature：聊天模式顯示工具、改檔與失敗（#1471）

- 每個 turn 的工具收成一行「使用了 N 個工具」（執行中是「正在使用 N 個工具…」），點開就地展開成跟指揮室一樣的區塊；
  Task 展開後可以看到它的子行程。
- 改檔另外一行「已編輯 notes.md（+3 −0）」，點開是 diff。
- **失敗或被拒絕的工具一律顯示**成一行紅字，點開看錯誤內容——alpha.455 的聊天模式會把它們藏起來，這版補上。
- 工具在跑時，「思考中」的點點改由「正在使用…」那一行表示；新出現的工具行會捲到底。
- 指揮室與聊天用同一套規則判斷工具是成功、改檔還是失敗，兩邊不會不一致。

## [1.0.0-alpha.455] - 2026-09-27

> 純 SPA，不需要重新部署 daemon。worker pane R2（chat 視圖）兩支 PR 的第一支。
>
> ⚠️ **chat 目前只有文字**：工具呼叫、改檔與失敗的工具要等下一版（R2-B）才會出現在 chat 裡；需要看工具細節請切回指揮室。

### Feature：worker 可以切換「指揮室／聊天」兩種顯示方式（#1469）

- header 原本的「Take to terminal」按鈕換成「顯示方式」選單：**指揮室**、**聊天**，以及「終端機」（動作，會把 session 接回終端機）。
  pane 窄的時候這三項收在「⋯」選單裡。
- 預設永遠是指揮室。切換是即時的，不會重新連線或重抓歷史；展開過的區塊在來回切換後仍保持展開。
- 切換結果會**跟著分頁同步到你的其他裝置**：在手機把某個分頁切成聊天，桌機上同一個分頁也會變成聊天。
- **聊天模式**：agent 在左、你在右的泡泡；思考內容不顯示（思考中與工具執行中只顯示「思考中」的點點）；
  沒有 dock，header 只留狀態、成本與「⋯」選單（中斷、終止、顯示方式都在裡面）。以手機的窄寬為優先。

## [1.0.0-alpha.454] - 2026-09-26

> 純 SPA，不需要重新部署 daemon。worker pane 重新設計（R1）七支 PR 的最後一支，R1 全部完成。

### Feature：worker 的輸入欄改成滿寬、有高度上限（#1467）

- 輸入欄拿掉圓角框，改成滿寬、上方一條細線；游標在輸入欄時那條線會變色。
- 高度最多 200px，再長就在欄內捲動，不會把對話擠掉。
- 拿掉沒有在用的附件／交給終端機按鈕列。
- 搜尋與快速回覆（spec §4.8）屬於 R3，不在這一版。

## [1.0.0-alpha.453] - 2026-09-26

> 純 SPA，不需要重新部署 daemon。worker pane 重新設計（R1）七支 PR 的第六支。

### Feature：worker pane 的 header 收成一列，資訊移到 popover 與 dock（#1464）

- header 只剩一列：狀態、worker 名稱（工作目錄的最後一段）、成本、操作按鈕；turn 數不再顯示。
- **Terminate 一開始就是紅色**（仍要按兩次確認）；**Take to terminal** 前面加一條分隔線，因為它是換綁定、不是中斷。
- pane 窄的時候，成本、Interrupt／Terminate 與 Take to terminal 收進「⋯」選單，只留狀態與名稱在列上。
- 點 worker 名稱會打開資訊面板：provider、profile（要求與實際不同時顯示 `要求 → 實際`）、完整工作目錄（可選取）、
  session id、是否已封存。
- pane 內、對話與輸入欄之間新增一條 dock：`連線狀態 · N 位觀看 · lease: 你`，點開是表格。這些原本擠在 header 裡。

## [1.0.0-alpha.452] - 2026-09-26

> 純 SPA，不需要重新部署 daemon。worker pane 重新設計（R1）七支 PR 的第五支。

### Feature：subagent 的內容收進它的 Task 裡（#1460，closes #1263、#1228）

- subagent 的訊息不再散在頂層：它們收在呼叫它的 Task 區塊裡，預設收成一行「名稱 · N 個工具」，展開後往內縮一層、
  有自己的一條 rail；subagent 再叫 subagent 會再縮一層。
- subagent 的提問不再畫成你說的話；交回的結果以文字顯示（不是原始 JSON），排在 subagent 自己的輸出之後。
- subagent 自己的工具也有計時與狀態了，但不會讓主流程的 turn 提早結束；主 turn 結束時，還沒回報的 subagent 工具會標成中止。
- `Task` 工具的標題顯示它的 description（跟 `Agent` 一致），不再印 prompt 的開頭。
- 「全部展開」第一次只打開 subagent 本身，再按一次才展開它裡面的工具。

## [1.0.0-alpha.451] - 2026-09-26

> 純 SPA，不需要重新部署 daemon。

### Fix：側欄分頁的鎖頭與關閉按鈕對齊（#1461）

- 鎖定分頁的鎖頭以前比關閉按鈕偏右 3px；現在兩者中心對齊在同一條線上，鎖頭大小不變。

## [1.0.0-alpha.450] - 2026-09-25

> 純 SPA，不需要重新部署 daemon。worker pane 重新設計（R1）七支 PR 的第四支。

### Feature：worker pane 所有內容對齊同一條左邊界，turn 成為容器（#1458）

- 不再有左右泡泡：你的話、agent 的話、工具、輸出、diff 都從同一條左邊界開始。你的話改成左側一條標記線；
  說明文字限制閱讀寬度，程式碼、輸出與 diff 用滿整個寬度。
- 每個 turn 是一個容器，**沒有分隔線、不顯示每個 turn 的耗時或成本**；滑鼠移到 turn 上時右上角會出現
  「全部展開／全部收合」，只影響那一個 turn 裡的工具、diff 與思考。
- 送出後還沒被接受的訊息（半透明）也放在一個 turn 裡，被接受時直接換成正式的那一個，不會多出一個 turn。
- 思考只有真的有文字才顯示，並且跟其他輸出用同一套折疊規則；展開狀態重新渲染後仍保留。
- 中斷提示與斜線指令拿掉泡泡形狀與寫死的顏色，改用主題色和圖示。
- 串流中的區塊，展開狀態只屬於那一則訊息，不會帶到下一則。

## [1.0.0-alpha.449] - 2026-09-25

> 純 SPA，不需要重新部署 daemon；工作台設定的格式不變。

### Fix：以 push 設定工作台主檔時，同步主機上的舊分頁不再流回來（#1454，修 #1450）

- 用精靈把某個本機工作台設為主檔並選「推送」時，如果這台的某個工作區在同步後是空的（例如固定存在的「未分類」
  工作區裡只有 Hosts 這類不同步的分頁），同步主機上同一個工作區的舊分頁以前會被拉回這台畫面上；現在會照精靈
  說的，以這台的內容（空的）取代同步主機上的那一份。
- 推送進行期間，如果其他裝置剛好新增了工作區，這台會先收到工作區、分頁稍後才到；這種工作區仍會等它的分頁送到，
  不會被當成空的推回去蓋掉。
- 精靈說明：主檔沒有命名時，不再寫「Home」，而是說明原本的主檔會以這台裝置的名稱留下來（實際名稱可能加上編號）。

## [1.0.0-alpha.448] - 2026-09-25

> 純 SPA，不需要重新部署 daemon。worker pane 重新設計（R1）七支 PR 的前三支。

### Feature：worker pane 的工具呼叫改成一個操作一個區塊（#1451，closes #1227、#1265）

- 呼叫與結果合成一個區塊，拿掉卡片外框；只有 error／denied 有底色（原本相反：成功是綠底、失敗只有紅字）。
- 輸出用同一套折疊規則：≤6 行整段顯示、≤40 行先給前 6 行、更長給前 3 行，error／denied 少折一級；預覽保留換行，不再壓成一行（#1265）。
- diff 走同一套折疊預算（#1227），保留外框，`+N −M` 跟著 diff 走、折起來也看得到；標題沒顯示路徑時才另外印出路徑。
  折起來時單一超長的 diff 行會被截短，展開才顯示完整內容。
- 沒有內建對應的工具，標題的參數預覽有長度上限並以 `…` 結尾；沒有參數時不再印出 `{}`。
- subagent 的結果不會再配到主流程同 id 的呼叫。
- 舊結果卡上的「N lines」刻意拿掉，改由折疊按鈕的 `+N lines` 表示。

### Feature：折疊規則與呼叫／結果配對層（#1447）

- 新增共用的折疊規則、呼叫與結果的配對、turn 起點記錄，以及 pane 層的折疊狀態記憶（重新渲染後展開狀態不會遺失）。

### Fix：成本面板不再印出完整的帳號 email（#1264，#1445）

- 帳號名稱只留前兩個字元（例如 `wa…@protype.tw`），截圖不會外流完整帳號；成本的 hover 摘要在面板打開時會自動關閉。

## [1.0.0-alpha.447] - 2026-09-25

> 純 SPA，不需要重新部署 daemon；工作台設定的格式不變（settings ordinal 仍是 8）。
>
> ⚠️ **升級後每個本機工作台的主機都是隱藏的。** 本機工作台從此有自己的「顯示的主機」清單，升級時一律從空的開始；
> 到 Hosts 頁打開「在此工作台顯示」即可。已經開著的分頁不會被關掉。工作台主檔的清單不受影響。

### Feature：每個工作台各自記錄顯示哪些主機（#1442，#1443，#1444，#1446）

- 工作台主檔的清單照舊跟著同步；本機工作台的清單只存在這台裝置上。切換工作台時，顯示的主機跟著換，互不影響。
- 設為主檔時清單跟著工作台走：新主檔同步的是它原本的清單，被換下來的舊主檔保留原本主檔那份。
- 切換或設為主檔進行到一半、還無法確定畫面上是哪個工作台時，一律視為「全部隱藏」（不連線、不開分頁），完成後即恢復。
- 主機換 id（例如取得 daemonId）時，每個本機工作台的清單都會一起換；切到另一個工作台讓某台主機由隱藏變顯示時，
  它的 session 會自動接回。
- 主機外觀（名稱、顏色、圖示）維持整台裝置共用一份。

### Feature：Settings › 工作台 的三顆建立按鈕（#1448）

- **全部複製**：複製畫面上的工作台，含工作區、分頁與顯示清單；分頁的 tmux 綁定照抄，兩邊共用同一個 session。
- **只複製設定**：只複製顯示清單，得到一個空的工作區、沒有分頁。
- **新增空白工作台**：顯示清單是空的（所有主機隱藏），自己到設定畫面打開。
- 副本不帶名稱、圖示與配色；建立後畫面不會切走。

### Refactor：拆掉主機清單的套用路徑（#1438，#1439）

主機歸屬 H3 的收尾：收到的 `hosts` 區段一律視為不認得的區段、不再套用；相關的套用程式碼、錯誤代碼與文案一併移除，
工作台文件也不再包含 `hosts`。舊版 apply 留下的主機別名仍可用來對應既有分頁。

## [1.0.0-alpha.446] - 2026-09-24

> ⚠️ **兩台 client 要一起升級。** 工作台設定的格式往前推一版（settings ordinal 8、`@wire:hosts-retired=1`）；還沒升級的
> client 碰到新版的工作台設定會停在「需要升級」並暫停同步那一區，不會寫壞資料。純 SPA，不需要重新部署 daemon。

### Feature：主機清單屬於每台裝置，不再跟著工作台同步（#1422，#1425，#1431）

主機歸屬 H3。主機的位址、token、daemonId 與主機清單本身從此只存在各自的裝置上：

- 在一台新增或刪除主機，其他裝置的主機清單完全不受影響；工作台同步（推或拉）永遠不會新增、取代或移除這台的主機。
- 主機的名稱、顏色、圖示仍跟著工作台同步（H2c）；分頁指向這台沒有的主機時顯示「這台裝置沒有這台主機」（H1）。
- 要把主機搬到另一台裝置，用主機頁的「分享主機／接收主機」傳遞碼（alpha.441）。
- 同步主機上舊版留下的主機清單（含 token）新版不讀、不寫、不刪，也不會被新版鎖住；刪除該工作台時才一起刪除，刪除確認框
  會說明這一點。本機殘留的舊主機清單暫存會在開機時清除。
- 設定工作台同步的精靈：拉取時不再列出「會移除這些主機」；只有舊主機清單的工作台視為空的（只能推）。

**已知限制**：同步主機上只有舊主機清單、還沒被新版寫過其他內容的工作台，無法把仍在跑的舊版 client 鎖住，它們會繼續
同步那份舊主機清單。兩台一起升級即可避免。

## [1.0.0-alpha.445] - 2026-09-24

> ⚠️ **兩台 client 要一起升級。** 工作台設定的格式往前推一版（settings ordinal 7、`@wire:shown-hosts=1`）；還沒升級的
> client 會把工作台設定鎖成「版本不符」，直到升級為止。純 SPA，不需要重新部署 daemon。
>
> ⚠️ **升級後所有主機在每個工作台都是隱藏的。** 到 Hosts 頁點進主機，打開總覽最上方的「在此工作台顯示」，
> 才能從它開分頁。已經開著的分頁不會被關掉。

### Feature：每個工作台自己決定顯示哪些主機（#1421、#1424、#1426、#1429、#1430、#1433、#1434）

主機歸屬 H2d。每個工作台有一份「顯示的主機」清單，跟著工作台同步；只影響這個工作台的「分頁 ↔ tmux session」。

- **Hosts 頁**：照舊列出所有主機；在目前工作台隱藏的主機變淡並標「已隱藏」，一樣可以點進去管理。主機總覽最上方的
  「在此工作台顯示」開關是唯一的切換入口；在一台切換，同一個工作台的其他裝置不必重新整理就跟上。
- **新加入的主機預設隱藏**，要手動打開。
- **隱藏主機不會關任何分頁**：已開的分頁留著，屬於這台主機的 pane 顯示「主機已於此工作台關閉」且不連線；分割分頁
  不拆，只有那一格變，要不要關那一格由你決定。重新顯示後，同一個 pane 直接重新連上，已結束又重建的 session
  也會自動接回，不必等 daemon 推送。
- **隱藏的主機不能開新分頁**：New Tab 不顯示它的區塊、session 挑選器不列出它、Hosts 頁不提供「開啟」
  （「新增 Session」保留）、History 與「重開已關閉分頁」不會重建它的分頁（紀錄保留，重新顯示後仍可重開）、
  「Bring in Tab」不列出它；它的通知照常發出，點擊改開 Hosts 頁；深連結與 execution 網址改落在 Hosts 頁；
  Hand to nex／Take back／Take to terminal 在主機中途被隱藏時不改寫分頁。
- 主機層級的連線（事件、健康檢查、session 清單）照常，隱藏期間不會對該主機的 pane 做探測。

## [1.0.0-alpha.444] - 2026-09-24

> 純 SPA，不需要重新部署 daemon。同步格式沒有變（settings ordinal 仍是 6），但 443 的 client 不會讀寫工作台的主機
> 外觀，兩台請一起升級。

### Fix：在這台刪除主機只影響這台裝置（#1413，修 #1396）

主機歸屬 H1c。刪除主機時，工作台裡指向它的分頁、主機設定、新分頁欄位都保留，只是在這台顯示「這台裝置沒有這台
主機」；不再標成「主機已移除」、不關分頁、不刪設定，也不會推送任何分頁或設定的變動（主機清單在主機歸屬 H3 之前
仍會同步）。

- 刪除確認框拿掉「關閉分頁」選項，文案改成「其他裝置上的分頁不受影響」。
- 復原（Undo）把主機原樣加回，包括 daemonId、順序、目前使用中的主機（#1396），分頁不必重新整理就重新連線。
- 刪除時若有其他操作進行中，會等最多約 4 秒；仍無法進行就提示稍後再試。等待期間主機被改動或移除則不刪，並提示。
- 刪除失敗會提示；連還原都失敗時顯示不會自動消失的警告，請重新載入並檢查主機設定。

### Feature：主機的名稱、顏色、圖示改由工作台保存與同步（#1415，#1417）

主機歸屬 H2c。主機外觀從主機設定搬到工作台設定，以 daemon 身分對應：

- 第一次開啟時自動把現有的主機外觀搬過去，畫面上看起來完全一樣。
- 在一台改主機名稱、顏色或圖示，同一個工作台的其他裝置不必重新整理就會跟上；清除顏色也會同步。
- 用傳遞碼收到的主機：名稱寫進主機本身，外觀寫進工作台，而且只寫工作台上還沒有的（工作台原有的外觀優先）。外觀
  寫入失敗時可以重試。

## [1.0.0-alpha.443] - 2026-09-24

> ⚠️ **兩台 client 要一起升級。** 這版把工作台設定的格式往前推一版（settings ordinal 6、`@wire:host-look=1`）；還沒升級的
> client 碰到新版的工作台設定會停在「需要升級」並暫停同步那一區，不會寫壞資料。純 SPA，不需要重新部署 daemon。

### Fix：這台加入某台主機後，指向它的分頁與欄位自動接上（#1406）

主機歸屬 H1b。同步進來、指向這台原本沒有的主機的參照，在這台之後加入（或認出）那台主機時，自動改回本機的主機：

- 分頁（含停放中的工作台主檔與本機工作台）、主機設定、新分頁欄位都會改寫，不必重新整理；改寫前後送到同步主機的
  內容完全相同，不會多推一次。
- 同一台主機的欄位不會重複出現：新增主機的當下若本機欄與同步欄暫時並存，送出去的設定以同步欄為準，另一台看不到
  重複。
- 寫入失敗時會還原並自動重試；連還原都失敗時以重跑收斂（含重新開啟時）。
- 終端機第一次連線前也會確認主機還在，主機被刪掉時連線立刻關閉。

### Refactor：主機的名稱、顏色、圖示改由同一個入口讀取（#1407，#1408，#1409，#1410）

主機歸屬 H2 的前置：畫面上所有顯示主機名稱、顏色、圖示的地方改走同一個 selector，並有測試擋下新的直接讀取；新增
「工作台的主機外觀」資料（目前還是空的，下一步才會開始使用）。畫面上看到的內容與之前逐位元組相同。
順帶移除已無人使用的 `SessionPanel`。

## [1.0.0-alpha.442] - 2026-09-24

### Fix：同步進來的分頁指向這台沒有的主機時，照原樣保留（#1400）

主機清單即將改成每台裝置各自一份（主機歸屬 H1a）。在那之前先讓這台裝置能容忍「別台有、這台沒有」的主機：

- 分頁裡的 tmux pane 如果指向這台沒有的主機，直接顯示「這台裝置沒有這台主機」，不會發出任何連線，也不會退而連到
  其他主機。
- 同步套用分頁時，不再把這類 pane 標成「主機已移除」並推回同步主機；存下來的內容與收到的一模一樣，不會多推一次。
- 同步套用設定時，新分頁欄位與主機設定裡這類主機的項目原樣保留，不會被刪掉。

## [1.0.0-alpha.441] - 2026-09-24

### Feature：用傳遞碼把主機搬到另一台裝置（#1394，#1397）

主機頁側欄多了「分享主機」與「接收主機」。分享端勾選主機、選一台兩邊都連得到的中轉主機，產生一組 8 碼的傳遞碼
（例如 `GNHX-VRH1`，10 分鐘有效、只能用一次）；接收端選同一台中轉主機、輸入傳遞碼，每台主機都會先用收到的 token
連線確認身分，再列出預覽（新主機、已存在、身分不符、連不上、重複、跟本機衝突），勾選後一次寫入。

- **daemon**：新增 `/api/host-transfer` 與 `/api/host-transfer/redeem`。傳遞碼只存在記憶體（10 分鐘或兌換後即刪，
  daemon 重啟就消失），內容不寫 log。同時最多 16 組、每組最多 32 台／64 KiB；60 秒內猜錯 10 次後一律回 429，直到
  該 60 秒結束。未設定 daemon token 時兩個端點直接拒絕。**需要重新部署 daemon**（當中轉的那台）。
- **信任說明**：中轉主機在傳遞碼有效期間讀得到你分享的主機 token，分享對話框在產生傳遞碼前會明白寫出來。
- **模式**：「只新增」（預設）與「同時更新相同的主機（位址、token、名稱與外觀）」。「全部取代」目前顯示為停用，等
  本機刪除主機的新行為上線後再開放（#1395）。
- **外觀**：收到的名稱、顏色、圖示暫時寫進主機本身；之後外觀改由工作台保存時會一併搬過去。
- IPv6 請用中括號寫法（例如 `[fe80::1]`）。

追蹤：#1395（全部取代）、#1396（刪除主機的還原會遺失 daemonId）、#1399、#1401。

### Docs

- Rebuild 最後狀態的說明與介面用詞一致（正常結束／行程已消失）（#1393）。
- 主機歸屬的 spec 與 H1 plan（#1398）。

## [1.0.0-alpha.440] - 2026-09-24

### i18n：中文介面的 profile 改稱「工作台」（#1391）

zh-TW 裡 profile 一律改成「工作台」：master 叫「工作台主檔」、本機 profile 叫「本機工作台」、同步主機上的叫
「同步主機上的工作台」、Profile Sync 叫「工作台同步」，設定側欄的「Profile」改成「工作台」。PRODUCT.md §3.9
詞彙表的中文欄同步改，並補「工作台設定檔」（一份工作台自己的名稱、圖示、配色）。英文介面不變；Nexen 的
sandbox profile 另案處理（#1290）。

### i18n：主機設定頁補齊中文（#1389）

中文介面下主機頁還有英文：24 個只寫了英文的字串補上中文，連線狀態、agent 狀態、視窗大小模式的選項改走翻譯
（例如「已連線」「閒置」「自動」），「host／mode／views」這類中英混用統一成「主機／模式／檢視」，側欄的檢視名稱
（例如「檔案（工作區）」）也能翻譯了。Session、Hook、Agent、Token 等技術名詞保留英文、一律單數。

追蹤：#1388（daemon 回傳的說明文字未翻譯）、#1390（其餘零星的英文）。

## [1.0.0-alpha.439] - 2026-09-23

### Fix：語系與主題重開 App 後會跳回預設（#1385，#1386）

選了中文（或淺色主題）後重開，選單仍顯示你的選擇，但畫面是英文（或深色）。原因：這幾個 store 在建立的同時就從
storage 讀回資料，讀回後的處理函式又引用了還沒建立完成的 store 本身，錯誤被靜默吞掉；加上內建語系包在那一刻還沒
註冊。改成處理函式用建立時拿到的 `set`、store 模組自己先註冊內建語系與主題。語系、主題、介面設定三個 store 一起修。

## [1.0.0-alpha.438] - 2026-09-23

### Feature：純介面分頁不再同步（#1380）

新分頁、設定、dashboard、hosts、history、效能監看、編輯緩衝清單這些「只是頁面」的分頁不進 Profile Sync：送出時
濾掉、收到時保留這台自己的（位置不變，目前停在它上面也不會被跳走）。新分頁一旦啟動成終端機等內容就開始同步；分割
畫面只要有一個 pane 已啟動就整個同步。設定頁改成每台裝置各自一個。tabs 格式加標記，舊版 client 會 `locked:schema`
到升級為止（不會刪掉自己的這類分頁）。

### Feature：Rebuild 顯示 pane 最後的 agent 狀態，resume 預設跟著走（#1381，#1382）

- **daemon**：最上層 agent 結束時（正常 `SessionEnd`，或程序被掃描到消失）廣播 `pdx_exit`（frame id、session id、
  原因、時間）；刪除 frame 改成原子 claim，只有真正刪到的一方送出，事件晚到或清理失敗都不會重送或漏送；晚到的舊
  `SessionEnd` 不再刪掉同一程序中新一次執行的 frame（既有 bug）。**需要重新部署 daemon。**
- **App**：分頁的 rebuild 紀錄記下 agent 的 frame id；收到結束通知且 frame id 與 session id 都相符才記「已結束」
  （時間、正常結束／行程已消失），同 session 其他 pane 的 agent 不會混進來。Rebuild 面板顯示「‹Agent› 最後所見仍在執行」
  或「‹Agent› 已於 ‹時間› 結束（正常結束／行程已消失）」，agent 已結束時 resume 預設不勾（仍可手動勾）；Rebuild all 同規則。
  App 斷線期間的結束不會補記（畫面會說明）。tabs 格式再加一個標記（ordinal 4）。

追蹤：#1379（Rebuild all 預覽表，擱置）、#1383（proxy 關聯只用 pid 辨識的既有競態）。

## [1.0.0-alpha.437] - 2026-09-23

### Fix：第一次拉取後，Settings › Profile 不再出現嚇人的 `pull-hash-mismatch`（#1369，#1376）

兩台裝置對同一 daemon 用不同 host id 時，拉取後這台會把自己的 id 補進主機的舊 id 紀錄（`aliases`）再推一次——這是
設計好的一次回寫，但以前會被記成 problem、一直掛到 Stop sync。現在只有「多出的只是這台自己的 id、少掉的只是
16 個上限擠掉的」這種差異時照推、不記；其他差異照舊記。順帶修掉一個同類問題：沒有 guard 時，主機清單可能在
collector 備好資料前就被推出去而多一條 `push-payload-missing`。

## [1.0.0-alpha.436] - 2026-09-23

### Fix：wizard「拉取」只套用你確認過的主機清單版本（#1366，#1370）

wizard 的確認到第一次拉取之間，另一台裝置若改了主機清單（例如又拿掉一台主機），以前會直接套用、你不會被告知。
現在連上同步時帶著你在 wizard 看過的主機清單版本；在它以相符的版本真正套用完成之前，其他區段一律不動作
（拉、推、還原本機、刪除、自動解鎖都等）。版本不符 → 什麼都不套用、停止同步（等同 Stop sync），並在
Settings › Profile 留一則通知請你重跑 wizard。本機主機清單早已等於確認版本時直接放行。

### Chore：清除已移除功能留下的資料（#1303，#1373）

- **App 開機時**（一次性、可重跑、不阻塞開機）：刪 IndexedDB `purdex-sync`、localStorage
  `purdex-workspace-snapshot`／`-prev`；`purdex-sync-state` 只在新的 client 身分已存下時才刪（避免裝置換身分，
  舊 id 的收養流程保留）。
- **daemon 啟動時**：刪 data dir 裡的 `sync.db`、`device_state.db`（含 `-wal`／`-shm`）——只刪檔名逐位元組相符的
  一般檔案（大小寫不同的 `SYNC.DB` 等使用者檔案保留），找不到不報錯，每刪一個記一行 log。**需要重新部署 daemon。**

## [1.0.0-alpha.435] - 2026-09-23

### Fix：Profile Sync 終於能跨兩台真實裝置同步——主機以 daemon 身分對應，不再靠各裝置亂數的 host id（#1359，#1365，#1362）

以前兩台裝置各自加入同一台 daemon（連內建的 mlab 都是），host id 一定不同；同步資料又以 host id 為 key，
所以第二台裝置 wizard「拉取」必然 `locked:invalid`（「會移除同步用的那台主機」），其他共用的主機則被當成移除、
pane 標成「主機已移除」再推回同步主機。先前的真機驗收因為兩個瀏覽器載入同一份主機設定而沒抓到。

- **邊界翻譯**（#1359 codec、#1365）—— 每台裝置的 host id 永遠不變；只有同步資料用「同步 id」：由 daemon 身分
  （`/api/info` 的 `host_id`，alpha.434 起記錄）算出的 `d1_…`，每台裝置算出同一個。送出時本機 id → 同步 id、收到時
  同步 id → 本機 id，涵蓋主機清單、pane 的主機欄位、主機設定、新分頁版面的 `sessions:`／`headless:` 欄位。
  收到的主機一對一配對（同一 daemon 兩列、兩列共用舊 id 都會整份拒絕），配到的就地更新、配不到的新建。
  舊格式資料靠每列記下的舊 id（`aliases`，所有裝置算出同一份：排序後前 16 個）在轉換中斷時也翻得回來。
  settings 要等 hosts 與 workspaces 都穩定才處理；套用途中主機身分若改變就回滾重試。
- **舊版 client 真的會被擋住** —— 原本只升 ordinal 擋不住（格式 fingerprint 相同就放行），這次 hosts／tabs／settings
  的 fingerprint 都帶上 wire 標記；未更新的 client 同步這份 profile 會 `locked:schema`，更新後恢復。
- **wizard 與暫停**（#1362）—— 拉取前要求用來同步的主機已驗證身分且一致，同步主機上沒有它就先擋下並說明；
  拉取會移除哪些本機主機先列出來；任何主機實際連到的 daemon 與記錄不同（或本機兩台記同一 daemon）時，整份 profile
  暫停同步並說明原因。沒選主機時第一台連上的自動選上。讀清單／hosts 都核對位址。

已知限制：改主機位址會清掉它的 daemon 身分，其他裝置會重建該主機（#1363）；#1366、#1367 追蹤剩餘的時序邊界。

### Fix：App 內補上主機 token 後會直接重新連線（#1360，#1361）

連線以 ip:port 辨識，換 token 不會重連，而且沒有 token 時第一次驗證失敗後就不再重試。現在以 ip、port、token 一起
辨識，換 token 走和改位址相同的重連流程（副作用相同：清掉該主機記憶體中的 session 狀態）。

## [1.0.0-alpha.434] - 2026-09-23

### Fix（daemon）：session 清單的 tmux 讀取有期限（#1293，#1344／#1345／#1350／#1354／#1346）

組 session 清單時會跑 `tmux list-sessions`，再對每個 session 讀 pane metadata，以前全都沒有期限。帶版本清單的四條路徑
（`?fresh=1`、WS 訂閱快照、wait-for 推送、ticker 推送）都在同一把鎖裡讀，一次卡住的讀取就會讓它們一起停擺——
包括 SPA 開 attach gate 所依賴的新連線第一個 frame。

- **整條鏈一個期限** —— tmux 清單、每個 session 的 pane metadata、tmux instance 探測、meta DB 讀取共用 5 秒；
  逾時的 tmux client 會被砍掉，清單回錯誤（不會交出不完整的清單、不消耗 seq、不旋轉 epoch）。`?fresh=1` 逾時照契約回 500。
- **每個 pane 一次 `display-message`** —— 原本 7 次（mlab 實測 15 個 session 約 44 ms／session，100 個 session 會逼近 5 秒），
  合併成一次約 10 ms。自由文字欄位由 tmux 先把 TAB 換成空白，解析端要求剛好 7 欄＋固定 id 驗證，不符就退回逐欄查詢。
- **排隊可以放棄** —— 版本清單與一般清單的快取重新填充都改成可放棄等待的 slot，請求的 context 結束就離開，
  不必卡在壞掉的讀取後面；invalidation 不會等讀取。
- **HTTP 跟著 request 走** —— session module 的 handler、fs search（含 `timeoutMs`）、nex handoff／take-back、agent upload
  都把 request context 傳進讀取；客戶端一斷線，卡住的讀取就結束。agent hook 的讀取刻意用獨立的限時 context：
  hook 事件要完整處理，送出端斷線不能讓狀態更新只做一半。
- **建立 session 全程有上限** —— 建立前取消什麼都不建；`new-session` 用獨立的限時 context（不綁 request，避免留下
  「建好了卻以為沒建」的 session），成功後一定跑完、不會留下沒有 meta 的 session；結果無法確認時 `session_alive` 回 false。
- **訂閱快照失敗會恢復** —— 背景重試 1／2／4 秒（每次用新的完整期限），全部失敗或 frame 排不進佇列就關閉該 WS 讓 SPA 重連；
  tmux 長時間卡住時約 27 秒重連一輪。其他推送維持記錄後略過。
- **peers inventory** —— 兩次 tmux instance 探測與清單共用同一個 2 秒 budget；預算用完回 partial。
- **驗證** —— 五支 PR 各自 R1＋攻擊方＋critic；合併後 main 上 `go vet`／`go build`／`go test ./...` 全過。
  追蹤：#1348、#1352、#1353、#1356、#1357（大檔拆分）、#1355（既有的 flaky 測試）。

### Feature（SPA）：Profile Sync 跨裝置 host 身分・第一塊（#1349、#1351）

- **記下 daemon 身分** —— 每個 host 記住 daemon 的身分（`/api/info` 的 `host_id`，存成 `daemonId`）；連線、換位址或換 token 時
  重新驗證。本機驗到的身分與記錄不同時只標記（不寫入、不同步），例如 daemon 重裝或同一位址在不同裝置連到不同 daemon。
- **新增主機依身分去重** —— 同一個 daemon 換個位址也會被擋下；配對流程若 token 已輪替：既有主機在這台已驗證為同一個 daemon 時
  自動更新它的 token，否則讓使用者選「把這個位址用在 X」／「另外新增」，新 token 絕不遺失。
- **daemonId 隨 hosts section 同步** —— `SECTION_SCHEMA_ORDINAL.hosts` 1 → 2：還沒升級的舊版 client 同步這份 profile 會進
  `locked:schema`，直到升級為止；新版 client 拉到舊形狀的 hosts 不會鎖。
- **尚未完成的第二塊** —— 跨裝置 pull 的修正還沒做：兩台裝置各自加入同一個 daemon 時，第二台用 wizard 拉資料仍會
  `locked:invalid`（「payload removes the master host」）。

## [1.0.0-alpha.433] - 2026-09-23

### Fix：英文介面其餘寫死的中文（#1337，#1342）

檔案樹的「請先選擇 Workspace」「設定專案路徑以顯示檔案樹」「確認」、分頁右鍵的「加入 … tab 成為 pane」、Session 檔案樹的未實作提示改走 i18n；
zh-TW 字串不變。全 repo 掃描後已沒有使用者看得到的寫死中文。

### Refactor：`/w/<ws>/t/<tab>` 定為舊別名（#1336，#1343）

`/t/<tab>/<mode>` 是分頁唯一的正式網址；`/w/<ws>/t/<tab>/<mode>` 仍可開，只啟用該分頁（ws 段忽略，所屬 workspace 以 store 為準）並正規化成 `/t/…`——
與先前行為相同。移除 `tabToUrl` 從未被使用的 `workspaceId` 參數。

## [1.0.0-alpha.432] - 2026-09-23

### Feature：所有裝置都停止同步後，可以從 wizard 刪除同步主機上的 profile（#1325，#1340）

Settings › Profile 的「同步主機上的 profiles」區塊需要 attach 著 master 才出現，停止同步後就沒有任何 UI 能刪主機上的 profile
（P3 驗收第 6 步只能打 daemon 的 REST DELETE）。

- wizard 選主機／profile 那一步：fetched index 顯示**沒有裝置 attach** 的列多一個「刪除」，先跳確認框；有裝置的列說明為什麼不能刪；
  主機回 409 時在該列列出仍 attach 的裝置；清單多一個「重新整理」。
- 刪除規則從 `SotProfilesBlock` 抽成共用的 `useSotDelete`，Settings 與 wizard 一套。
- **安全修正（review 抓到，Settings 原本就有）**：主機端的刪除／改名與清單請求原本只綁 hostId——同一個 hostId 的位址被改到另一台時，
  可能刪到那台同 id 的 profile。現在全部帶 `expectEndpoint`：位址一變確認框就關、清單重抓；主機不在設定裡就不發任何請求。
- wizard 的失敗訊息只用依失敗類型挑的句子，不顯示主機回傳的原文。

## [1.0.0-alpha.431] - 2026-09-23

### Fix：operation lock 一釋放，就用有版本的 session 清單重新對帳（#1309、#1310，#1330）

切換 profile、profile apply、rebuild 這些改寫分頁的動作都在 operation lock 裡執行、寫完才釋放。以前釋放時只拿手上最後一份清單做 revive，
那份清單可能已經過時；而 profile apply 換上畫面的 pane，要等該 host 下一次 `sessions` 推送才會對帳——host 上沒有其他變化時就永遠不會。

- **單一觸發點** —— lock 釋放時，對「連線已對帳過、且是新版 daemon」的 host `GET /api/sessions?fresh=1` 重新對帳，revive 也用這份清單；
  其他 host 維持以前的行為。#1309：lock 釋放後的 revive 改用釋放之後才讀的清單（`runRevivePassAll` 移除）。
  #1310：profile apply（含 wizard 的「拉」）寫完就釋放 lock，剛換上畫面的 pane 立刻用 apply 之後讀的清單對帳，判定再推回 SOT。
  `switchActiveProfile` 不再自己呼叫 refresh，一次切換每個 host 只抓一次。
- **lock 世代** —— 某次釋放發出的 fetch，若回來時下一個動作已拿到 lock，就丟掉，交給下一次釋放。WS 對帳失敗後的 recovery 也套用；
  被擋下的 host 在釋放時重發，gate 關著也不會永遠等不到對帳。
- **暫存 WS 清單** —— 從拿到 lock 到釋放後那次 refresh 完成之間，新版 daemon 送來的 `sessions` frame 先暫存最新一份，
  refresh 完成後再依版本判定。避免 apply 之前讀、之後才送達的清單把剛到的 pane 誤判成已結束（不可逆）；#1255 的切換也一併補上。
- **驗證** —— 真機雙 client（:5176、自建測試 profile）：B 在線上時拉到一個綁已死 session 的 pane → 剛好一次 `fresh=1` → `session-closed` → 推回 SOT；
  對照組維持 live。純 SPA，不用 deploy。
- **追蹤** —— #1331（`refresh-sessions.ts` 拆分）。

## [1.0.0-alpha.430] - 2026-09-23

### Fix：冷啟動深連結與 persisted active tab 無限乒乓，renderer 當掉（#1326 後半，#1335）

alpha.428（#1327）修好了 App 內導頁的迴圈，但**整頁載入**深連結時仍卡死（只剩 `useRouteSync.ts:41`）。

- **根因**：`useRouteSync` 的兩個 effect 都等 `hydrated`；冷啟動 hydration 完成的那個 commit 裡兩個同時跑，各拿對方剛改掉的舊值——
  Tab→URL 用 persisted 的 active tab 覆寫網址，URL→Tab 依（已被覆寫的）深連結切換分頁；下一輪兩邊互換，永不停止。
  範圍比回報大：**所有**與 persisted active tab 不同的可解析深連結都會中（`/w/<ws>/settings`、`/t/<別的分頁>`、`/history`、`/hosts`、`/execution/…`）。
- **修法**：URL→Tab 先宣告、先把深連結套進 store；Tab→URL 在 effect 當下讀 store，只校正 URL→Tab 無法處理的網址。沒有迴圈計數器。
  另修冷啟動時多記一筆「沒造訪過的舊分頁」瀏覽紀錄。
- 真機（整頁載入）：`/w/<ws>/settings`、`/history` 落點正確、零錯誤；換回舊版則 12 秒內 19 次 max depth。
- 已知（既有、非本次引入）：`/w/<ws>/t/<tab>/…` 會被正規化成 `/t/<tab>/…`（#1336）。

### Fix：英文介面出現寫死的中文（#1324，#1334）

Sidebar 的 add-view／管理按鈕、空狀態、區塊標題、右鍵選單空狀態，以及「找不到檔案」彈窗的兩個按鈕（含可見文字與 aria-label）改走 i18n，
zh-TW 字串不變。其餘元件的寫死中文追蹤於 #1337。

## [1.0.0-alpha.429] - 2026-09-23

### Feature：Profile Sync P3d-4 — 在 Settings › Profile 解鎖定的區段、看得到同步細節（#1295，#1311，#1328）

P3 的最後一塊：衝突不必再靠 dev hook，從 UI 就能解；整個 P3 通過兩台 client 的真機驗收（全程只用 UI）。

- **Resolve 區塊**（#1311）—— 每個鎖定的區段一列：衝突與「同步主機那份被重建過」可選「保留這台裝置的」或「採用同步主機的」；
  「無法套用」只能保留本機的，並說明原因（`invalidReason` 七種封閉代碼）。每個選擇都先經確認框，框裡是**選了之後會保留下來的**
  那一份的數量（衝突時是當初送出的那份，不是現在的畫面），主機那側在開框時讀一次。確認框凍結開啟當下的 lock、master（host、
  profile、第幾次連上）與位址；任一變了就自動關閉並說明，送出時再核對一次，讀主機的請求在組請求的同一步核對位址。
  follower 視窗送出後顯示「已送出」，到 lock 變了或指令 30 秒有效期為止。
- **同步細節**（#1295）—— 每個區段顯示協議過的版次；「截至 <時間> 已同步」（最後一次收到主機回應且整份已同步的時間）；
  一直失敗的區段顯示下次重試時間；「settings 在等 workspaces」在 workspaces 鎖定**或持續失敗**時都會說明。profile 從主機消失的
  兩個來源都會顯示。跨視窗的狀態紀錄有嚴格的形狀驗證與 512 KiB 上限，超過時由負責同步的視窗依序精簡（problem 留 10 筆 → 拿掉
  → 拿掉區段細節，lock 永不丟）；problem 的說明截到 1000 字元，不再存整份錯誤內容。
- **真機驗收修正**（#1328）—— 精靈拉取完成後立刻顯示「截至…已同步」；Auto-sync 關閉時不再誤稱「同步中…」，改說「等待中——
  自動同步已關閉」，Resolve 確認框也說明要按「立即同步」才生效；時間依介面語言格式化；文案對齊。
- **PRODUCT.md §3.9 Profile** —— profile／主要／本機 profile／畫面上的 profile／同步主機／區段／鎖定的定義與「不是什麼」。

追蹤：#1296（executor.ts 拆檔）、#1304（全套負載下的 flaky 測試）、#1324、#1325（UI 無法刪除同步主機上的 profile）、#1332。

## [1.0.0-alpha.428] - 2026-09-23

### Fix：全域 Settings 在前景時進入 `/w/<wsId>/settings`，renderer 無限迴圈當掉（#1326，#1327）

Profile Sync P3 真機驗收時兩台 client 都重現：`Maximum update depth exceeded`，CPU 衝破 100%、記憶體數 GB 後 renderer 死掉。

- **根因**：`GlobalSettingsPage` 的自我修正 effect 把「網址沒有 section」當成「網址是 `/settings`」，對任何不在 `/settings` 底下的網址也
  `replace` 成 `/settings/<section>`。網址變更後，舊的全域 Settings 頁還會掛一個 commit（子元件的 effect 先於 `useRouteSync`），
  於是和 `useRouteSync` 的 Tab→URL 互相 `replace`，永不停止。同一個原因也會讓保持存活（keepAliveCount > 0）的全域 Settings
  把切走的分頁拉回來。
- **修法**：這個 effect 只在網址是 `/settings` 或其子路徑時動作；`useRouteSync` 不動、沒有加迴圈計數器。
- **測試**：走真正的 `TabContent`＋keepAliveCount 的四個回歸測試；拿掉修正四個全紅。真機：修正版落在 workspace 設定分頁、零錯誤；
  換回舊版則頁面卡死、1,273 次 max depth。

## [1.0.0-alpha.427] - 2026-09-23

### Fix：切換 profile 後，剛換上畫面的世界立刻用有版本的 session 清單對帳（#1255 SPA 那一半，#1312）

以前一個世界停放期間若有 session 關掉，切回來時它的 pane 仍綁著那個 session，要等該 host 下一次 `sessions` 推送才會更正——
host 上沒有其他變化時就永遠不會。原因是手上的清單都沒有版本、不能當「session 已關」的證據（`session-closed` 不可逆、
依名稱 revive 會綁錯 instance，master 還會推上 SOT）。daemon 在 alpha.423（#1292）提供了不經快取、帶版本的清單。

- **切換後重新對帳** —— `switchActiveProfile` 成功（兩把鎖都放開）後，對每個連線已對帳過的 host `GET /api/sessions?fresh=1`；
  世界又換了、連線斷了、host 被移除或換位址、或清單不比已知的新，就丟掉。舊 daemon 回陣列 ＝ 沒有版本 → 維持以前的行為。
  失敗有限重試（1、2、4 秒），每次重試前重新確認上面的條件。只有發起切換的視窗抓（tab store 共用，結果經 rehydrate 傳到其他視窗）。
- **WS `sessions` frame 依版本排序** —— 每個 host 記住已對帳清單的 `{epoch, seq}` 與連線世代；比它舊的 frame 一律不對帳。
  版本在套用**之前**就記下（對帳不是交易式的，寫到一半丟例外時也不能讓較舊的清單進來），失敗改走重試。
- **revive 快照綁定世界** —— 修掉一個既有 bug：切換本身釋放 operation lock 時，會拿切換前的清單去 revive 新世界的 pane。
  現在畫面換過世界就不 revive，等拿到新世界的清單再說（promote 也會觸發，屬保守方向）。
- **已關閉的 host-events socket** —— `close()` 讓連線世代失效，佇列裡還沒處理的 frame 與排入的 open 事件都不再送進 handler
  （host 被移除或換位址時，舊 daemon 的清單可能被拿去對帳）。
- **驗證** —— 真機 :5176：slave 停放期間砍掉它綁的 session，切回 slave 後恰好一次 `fresh=1`、pane 變成 `session-closed`；
  來回切換四次 binding 完全不變。純 SPA，不用 deploy。
- **追蹤** —— #1309（lock 釋放時的 revive 改抓新清單）、#1310（profile apply 換上畫面的 pane 同樣沒有對帳證據）、#1313（WS hook 拆分）。

## [1.0.0-alpha.426] - 2026-09-23

### Refactor：拆掉 device-state 與 workspace snapshot（Profile Sync P4b，#1314–#1320 SPA、#1321 daemon）

device-state 備份（每台 client 上傳結構快照、可取代或合併）與 workspace snapshot（手動擷取／還原）都已被 Profile Sync 取代。
依定案 14，Host › Snapshots 只留 rebuild 紀錄。

- **SPA**（約 9,100 行）：`lib/device-state/`、`components/settings/device-state/`、`useDeviceStateStore`、`lib/snapshot/`，
  Host › Snapshots 的 tmux／tabs／client snapshot 區塊，以及開機時的 device-state uploader；兩個語系檔各刪約 60 個 key。
- **使用者看得到的變動**：裝置名稱欄位（Profile Sync 顯示的那個名稱唯一的編輯處）搬到 **Settings › Profile 最上方**，
  原本在 dev host 的 Host › Snapshots 裡。
- **保留**：開機時仍解析預設裝置名稱（原本是 uploader 的副作用；拿掉的話名稱欄位會一直顯示「Browser」、wizard 也可能把它寫進 profile——codex R1 抓到）；
  `purdex-device-state` 這個 key 仍是 `useDeviceNameStore` 的存放處。
- **修正**：Host › Snapshots 的 live-session 查詢改以 host 與每次查詢的身分比對——同一台 host 的分頁清空再出現時，不再沿用上一次的連線狀態。
- **daemon**：`internal/module/devicestate/` 與四條 `/api/device-state` 路由（`TestRemovedRoutesAre404` 證明經過真正的 mux 都是 404）。
- **刻意留下的**：localStorage `purdex-workspace-snapshot(-prev)`、daemon 的 `device_state.db`——都已無人讀寫，清理追蹤 #1303。
- **部署**：daemon 與 alpha.424（P4a，#1305）的變更一起 deploy 到 mlab 與 air26。

## [1.0.0-alpha.425] - 2026-09-23

### Refactor：新分頁版面的「profile」改名為「preset」（Profile Sync P3e，#1291、#1297）

新分頁版面（三欄／兩欄／單欄）原本也叫 profile，跟 Profile Sync 的 profile 撞名。使用者 2026-09-23 定案現在就改，
避免後續接手的 session 遇到語意混淆。Profile Sync 的 profile 與 Nexen 的 Sandbox profile 都沒動；中文介面文案不變。

- **#1291（純改名，零 byte 變更）** —— `lib/resolve-preset.ts`（`PresetKey`／`LayoutPreset`／`resolvePreset`）、
  `NewTabPresetSwitcher`、`presetKey` prop、`preset-*` testid、`settings.interface.preset_*` 六個 i18n key、
  英文 "Fallback preset (cannot be disabled)"。以固定狀態釘住 localStorage 與同步 payload 的字串，證明一個 byte 都沒變。
- **#1297（持久化欄位＋同步形狀，原子 PR）** —— store 欄位 `profiles` → `presets`、`activeEditingProfile` → `activeEditingPreset`，
  persist `version: 2` 加 migrate（對 20 種壞資料與 v1 修復結果等價、跨視窗 rehydrate 不打迴圈）；
  settings projection 改同步 `presets`，`SECTION_SCHEMA_ORDINAL.settings` 3 → 4（settings fingerprint 變動是預期）。
- **新舊 client 共存** —— ordinal-3 client 寫的 settings（欄位仍叫 `profiles`）在 `applySettingsSection` 入口先 upcast，
  clean pull、首次 attach pull、keep-sot、重啟後 keep-local 重放四條路徑都不會 `invalid` 也不會把版面清成預設；
  新 client 推一次 ordinal-4 的 settings 後就安靜，舊 client 遇到 ordinal 4 進 `locked:schema`、零寫入。
  review 抓到 keep-local 會先推一筆「舊形狀＋ordinal 4」的 row，已改成先 upcast、只推一次（`local-restored` 多帶 `localHash`）。
- **使用者注意** —— 升級後第一次同步會把 SOT 的 settings 升到 ordinal 4，還沒升級的其他裝置會停在 `locked:schema` 直到升級；
  自訂語系若翻譯過舊的六個 `settings.interface.profile_*` key，這六個會回到內建文字。
- **驗證** —— 真機 :5176：main → branch 同 origin，v1 → v2 資料逐欄相等、設定頁與寬螢幕新分頁畫面一致。純 SPA，不用 deploy。
- **追蹤** —— #1306（`executor.ts` 過大，拆分）。

## [1.0.0-alpha.424] - 2026-09-23

### Refactor：拆掉舊的 Sync 模組（Profile Sync P4a，#1298–#1302 SPA、#1305 daemon）

舊的 Sync（手動／檔案／daemon 三種 provider、三方合併、快照歷史）已被 Profile Sync 取代；P3d-3 起 `Settings › Sync`
只在還有 pending conflicts 或 provider 開著時才出現。使用者 2026-09-23 確認 alpha.422 的側欄已看不到它，才開始拆。

- **SPA**（純刪除，約 9,900 行、64 檔）：`lib/sync/`、`features/settings/`（同步歷史頁）、`SyncSection`、`SyncConflictBanner`、
  TitleBar 的衝突圖示、`register-modules` 的 `sync` 註冊、App 開機時的 `ensureSessionPristine`、`SETTINGS_ORDER.MODULE_SYNC`，
  以及兩個語系檔各 97 個 key（`settings.sync.time.*` 仍有人用，保留）。
- **daemon**：`internal/module/sync/` 與九條 `/api/sync/*` 路由；`TestRemovedRoutesAre404` 證明它們經過真正的 mux 都是 404。
- **驗證**：以反向驗證取代 mutation——每個被刪的 import、匯出符號與 i18n key 都零引用；編輯過的檔案只刪不加。
- **刻意留下的**：localStorage `purdex-sync-state`（`client-identity` 仍靠它收養舊的 client id，刪了裝置會換身分）、
  IndexedDB `purdex-sync`、daemon 的 `sync.db`——都已無人讀寫，清理追蹤 #1303。
- **部署**：daemon 這次先不重啟，和 P4b（拆 device-state）的 daemon 變更一起 deploy。

## [1.0.0-alpha.423] - 2026-09-23

### Feature（daemon）：不經快取、帶版本的 session 清單（#1255 daemon 那一半，#1292）

Profile Sync 切換 profile 之後要重新對帳 session，但原本拿到的清單都不能當「session 已經關掉」的證據：
`GET /api/sessions` 走 1 秒快取、create／rename／kill 都不會讓它失效；WS 的 `sessions` 推送經過 500 ms debounce，
也沒辦法跟 fetch 比先後。

- **`GET /api/sessions?fresh=1`** —— 回 `{epoch, seq, sessions}`，永遠是新的 tmux 讀取。只有 `fresh=1` 才是新格式，
  其他照舊回陣列；舊 daemon 會忽略 query 回陣列，SPA 以此判定「沒有版本」。
- **WS `sessions` frame** —— 訂閱快照、wait-for 推送、ticker 推送都多帶頂層 `epoch`／`seq`；其他 frame 位元組不變。
- **語意** —— `epoch` 是 daemon 行程身分（64-bit 亂數，只比相等）；`seq` 所有管道共用一個計數器，
  取號與 tmux 讀取在同一把鎖內，所以同一個 epoch 裡 seq 大的一定是後讀的，每個 seq 也只屬於產生那份清單的那次讀取。
  收到 create／rename／delete 的回應後才發的 `?fresh=1` 一定反映該變更。
- 舊的 1 秒快取現在會在 create／rename／delete／wait-for／ticker 偵測到變更時失效（舊 client 也受惠）。
- SPA 那一半（切換後用這份清單重新對帳）另案處理；#1255 保持 open。已知殘留：tmux 讀取沒有 deadline（#1293，既有問題）。

## [1.0.0-alpha.422] - 2026-09-22

### Fix: x64 的 .app 在 Intel Mac 上啟動即 crash——ad-hoc 簽章少了 entitlements（#1288）

air-2019（Intel、macOS 14.8.9）啟動 Purdex 時 dyld 直接 abort，錯誤訊息是
`Library not loaded: @rpath/Electron Framework.framework/Electron Framework ... different Team IDs`。
Team ID 是**假線索**——兩邊都是 ad-hoc、`TeamIdentifier` 都是空的，`codesign --verify` 也回 `valid on disk`。
真正的原因是 **Library Validation**：`scripts/build-electron.mjs` 對 x64 用 `--options runtime`
（Hardened Runtime）簽章卻**沒帶 `--entitlements`**，Hardened Runtime 會打開 Library Validation，
而空的 entitlement set 沒有東西能把它關掉，於是自家的 `Electron Framework` 被擋。

arm64 之所以沒事，是因為 electron-builder 只對 arm64／universal 做 ad-hoc fallback
（`macPackager.js:215`）並帶上它的預設三鍵模板；x64 被 electron-builder 跳過不簽，落到我們的腳本手上——
而腳本沒帶 entitlements。從 `7cad0823` 引入 `--options runtime` 起就壞著，因為平常只有 arm64 的機器在跑新建置。

壞掉的不只主程式：x64 那一份**六個 bundle 全中**（app、framework、四個 helper），
而且 `--deep` 與 `--identifier` 併用還把每個 nested bundle 的 identifier 都壓平成 `dev.wake.purdex`
（arm64 的 framework 正常是 `com.github.Electron.framework`）。

- **`electron/entitlements.mac.plist`（新）** ——三個 V8 鍵，成為兩個架構共用的單一來源；
  key set 以 `plutil -convert json` diff 確認與 electron-builder 模板相同。
- **`package.json`** —— `build.mac.entitlements` ＋ `entitlementsInherit` 指向它。
  `hardenedRuntime` 刻意不設（electron-builder 對非 MAS 本來就預設 `true`）。
- **`scripts/mac-sign.mjs`（新）** —— 無 top-level 副作用的簽章模組。`buildSignArgs()` **補上** `--entitlements`、
  **拿掉** `--identifier`（實測：不帶它時頂層仍從 `Info.plist` 取得 `dev.wake.purdex`，nested 各自保留正確 identifier）。
- **build 期守門（G3）** —— 對 app **與其四個 helper** 逐一檢查「Hardened Runtime ⇒ `disable-library-validation` 為 `true`」，
  而且是**讀值**不是比對 key 字串（把 plist 的值改成 `<false/>` 也會被擋）。兩條路徑都吃到：x64 的新簽路徑，
  以及 arm64「沿用 electron-builder 簽章」的早退路徑——後者正是偵測 electron-builder 回歸的唯一防線。
- **`electron/signing.test.ts`** —— 原本斷言 `build-electron.mjs` 含 `'codesign'`／`'--verify'` 字串的案例
  分不出能動的 build 與壞掉的 build，換成對 `buildSignArgs()` 的行為斷言。
- **dev-update 的 full-rebuild 偵測** —— 兩份 tracked paths 補上 `scripts/mac-sign.mjs` 與
  `electron/entitlements.mac.plist`；搬移程式碼後簽章改動原本會讓 rebuild 提示完全失聲。

驗證：`scripts/mac-sign_test.mjs` 9 個案例全程驅動**真的 `codesign`** 簽臨時 bundle（不是原始碼字串比對）；
兩輪 review 共 11 個 mutation，最後全數被殺。真機：air-2019 上新建的 x64 bundle 存活 45 秒以上、
renderer helper 正常、無新的 `.ips`，行程組成與 air-2026 上正常運作的那份相同。
產出對照：x64 十個 bundle 全帶 `disable-library-validation`、framework identifier 回到
`com.github.Electron.framework`；arm64 與修改前基準線 `diff` 乾淨、entitlements XML 位元組相同（G4）。

`test:mac-sign` 9/9、`electron` vitest 199/199、`go test ./internal/module/dev/` ok、
`rm -rf dist && pnpm run electron:build` exit 0。這是 `docs/release/macos-signing.md` Stage 1a 的
**app 半部**，daemon 簽章管線仍 pending。

follow-up：兩份 rebuild 清單靠註解維持同步、無自動化防線；`nestedHelperBundles()` 目錄不存在的分支無覆蓋（fail-closed）；
守門未涵蓋 `chrome_crashpad_handler`；`electron:build` 每個架構的呼叫都會把兩個架構都建一次（多建的直接刪掉）。

## [1.0.0-alpha.421] - 2026-09-22

### Feature: 設定同步的 wizard——第一次不靠 dev hook 設定 Profile Sync——Profile Sync P3d-3（#1283、#1284、#1285、#1286）

Settings › Profile 的「開始設定」（已同步時是「換一份 profile／換一台主機…」）就地展開一個 wizard，走使用者定案第 10 條的五步、**一次一步、每步確認、順序不可調換**：(1) 已 attach 就先停止同步（主機沒被通知的結果不會被吞掉）→ (2) 選主機與 SOT profile（既有的或新建）→ (3) 選本機哪一份當 master（選本機 profile 是**搬移**不是複製）→ (4) 方向：新建的與空的 profile 只能「推」；「拉」先顯示會被取代的 workspace／tab 數量，並**預設勾選**「先存一份成本機 profile」→ (5) 顯示摘要、按「開始」才執行；任何子步驟失敗就停在那裡，說清楚已做、未做、現況與能怎麼辦，不自動往下做。

- **「拉」之前要保住的是 attach 當下標成 master 的那個世界**，不是畫面上的那個：一律在 promote 之後、attach 之前用 `copyMasterAsSlave`（五種情況的表在 plan「P3d-3 — As built」，逐格有測試；原本預設的 `saveScreenAsSlave` 在「選停放的本機 profile 當 master」時會存錯世界）。
- **按「開始」的那一刻重新讀 SOT**（codex 的 critical）：比對選擇當時每個 section 的 rev／hash，任何變動就不執行、回到方向步驟重選——否則 wizard 開著的期間別台往那份「空的」profile 推了內容，這台仍會被強制 push 而靜默覆蓋它。已查證 attach 的 push 在第一次對帳期間等同無條件覆蓋、沒有地方接 rev，所以只能靠這次重讀；殘留窗口寫在檔頭。
- 建立 profile 的結果不明（timeout 等）時先重新列出，**不自動沿用**同名的空 profile（可能是另一台同時建的）：標示「可能是你剛才建立的」由使用者自己選；不會盲目再建一份。
- 「主機沒被通知」的幽靈登記改成**集合**（每筆以 endpoint＋host＋profile 為 key、逐筆呈現與重試；合併在 Web Lock 內做；alpha.420 的單一格式會轉入）；`attachMaster` 內部兩處盡力而為的刪除也寫進它。
- 「拉」完成後設定頁連同 wizard 會被新世界換掉（它屬於被取代的世界）→ 結果一律用 toast 說：現在與誰同步、原本的內容存成哪一份本機 profile。
- **舊的 Settings › Sync 離開側欄**（spec：Profile 取代 Sync；模組本體 P4a 才刪）——但使用者還在用舊 Sync（provider 沒關）或還有待處理的衝突時，入口與 TitleBar 圖示保留，且會隨 store 更新即時出現／消失；順手修掉舊頁面「provider 關著就看不到衝突 banner」的條件。

真機驗收（兩個 client、全程經 UI、不用 dev hook attach）：A 新建→推；B 既有→拉並保留副本（B 變成 A 的世界、原本的世界完整存成本機 profile、SOT 上找不到 B 舊世界的任何內容）；critical 的情境重現並確認被擋下（B 沒有執行、回到方向步驟、A 的內容原封不動）；Stop sync 後 daemon 登記歸零；console 零錯誤。codex：R1＋攻擊方（1 critical、2 high、1 medium、1 P2）＋critic（F1／F5／F6 已修好，追加三條 high，已修）。`ProfileWizard.tsx` 的職責 → #1240。

使用者 2026-09-22 確認的兩點已從「待決定」改為定案：先選圖示才能配色；沒有本機 profile 的人點 Home 維持今天的行為。

vitest 9932、lint、tsc、build 綠。純 SPA，daemon 仍是 411、免 deploy。下一支 P3d-4：解決衝突的面板、每個 section 的 rev／失敗狀態／上次同步時間、PRODUCT.md、整個 P3 的真機驗收。

## [1.0.0-alpha.420] - 2026-09-22

### Feature: Settings › Profile——這台裝置上的 profiles（名稱／圖示／配色）、主機上的 profiles、同步狀態——Profile Sync P3d-2（#1279、#1280、#1281）

Settings 側欄多了「Profile」。沒有 master、沒有本機 profile 的人打開它：看到靜態內容、**不寫 storage、不向任何 host 發請求**；沒打開這一頁的人與 alpha.419 相同。

- **這台裝置上的 profiles**：每個 profile 一列、master 在最上面。**每個 profile（含 master）的名稱／圖示／配色就地編輯**（使用者 2026-09-21 的決定；重用既有的 workspace icon picker 與 host 色票）——改完 Home 按鈕立刻跟著變。列內可以切換到該 profile（走切換器同一條路徑，不另寫重試）；本機 profile 可以排序、刪除（在畫面上的那個不能刪；要確認）；「把 master 複製成本機 profile」「把目前畫面存成本機 profile」，預設名稱用這台裝置的名字且不與既有的重名。
- **主機上的 profiles**（attach 了 master 才顯示）：改名、刪除——只有**抓回來的**清單顯示沒有任何裝置 attach 時才能刪，409 `attached` 會列出還 attach 著的裝置，目前 attach 的那一份不能刪。
- **同步狀態**（資料只來自 `useProfileSync()`）：同步的 profile 與 host、整體狀態（與切換器的狀態點同一個函式）、每個 section 一列（`Tabs · alpha`，不是 `tabs.4ecsi1`；名稱查 **master 的世界**）、Auto-sync、Sync now（follower 視窗也能按）、Stop sync（要確認）。follower 視窗標明數字來自哪個視窗、何時可能過期；`blocked` 與各種 unsettled 用白話說、當成**狀態不是錯誤**；「設定正在等 workspaces」會明講。
- 切換器裡多一個通往這一頁的項目。還沒有 wizard（P3d-3）與解決衝突的按鈕（P3d-4）——也沒有任何點了沒反應的控制項；attach master 這一版仍只能用 dev hook。

Review（codex R1＋攻擊方＋critic，全 `gpt-5.6-sol`）：刪除確認框開著時別的視窗把 master 換到另一台 host，確認後會**刪到新 host 上同 id 的 profile** → 每個主機端操作綁定發起時的 host 與 profile，一變就關閉、送出前再核對；**Stop sync 通知主機失敗完全看不到**、留下讓別台永遠刪不掉 profile 的幽靈登記 → `detachMaster()` 回報結果，提示存進 device-local 的 `pendingDetach`（reload 後還在）並提供重試；critic 追加：重試固定在 attach 當時的 **endpoint**，host 位址被改過就不送、顯示兩個位址；只持久化簡短的原因，不存 response body 或錯誤訊息；未存的名稱不再靜默覆寫別的視窗的改名。**改 plan 而非改程式**（附理由）：沒有本機 profile 的人點 Home 維持今天的行為、master 那一列不藏（鐵則＋使用者的決定）。移到 P3d-4：`workspaces` 持續**失敗**時的「設定在等」、每個 section 的 rev、上次同步時間——executor 得先公開這些。`CurrentBlock`／`SotProfilesBlock` 的職責 → #1240。

真機驗收兩輪、十一步、全程經頁面操作：全過、console 零錯誤——含「讓主機通知失敗的 Stop sync」：提示 → reload 還在 → 改位址（不提供重試）→ 改回 → 重試 → 消失；塞進假錯誤裡的 token 字串在頁面與 storage 都找不到。

待使用者決定（集中在 `profile-rules.ts` 的 `COLOR_NEEDS_ICON`，翻案改一處）：圖示是點陣的 logo 時配色沒有東西可以著色，目前的做法是先選圖示才能配色。

vitest 9719、lint、tsc、build 綠。純 SPA，daemon 仍是 411、免 deploy。下一支 P3d-3：wizard——第一次不靠 dev hook 設定同步；舊的 Settings › Sync 屆時離開側欄。

## [1.0.0-alpha.419] - 2026-09-21

### Feature: Home 按鈕就是 profile——名稱、圖示、配色；有本機 profile 時變成 profile 切換器——Profile Sync P3d-1（#1276、#1277；#1275）

Profile Sync **第一次有看得見的東西**。使用者 2026-09-21 的兩個決定：「Home 統一改為 Profile 名稱，沒有命名時預設 Home」「Profile 可以選 logo / phosphor icon + 配色」。

- **每個 profile（含 master）都有名稱、圖示、配色**：master 的名稱可以是空的（→ 顯示 `Home`），沒選圖示 → 現在的 Purdex logo，配色著色在 Phosphor 圖示上。存在 `useLocalProfilesStore`——**每台裝置各自的，不進 SOT**；型別重用 workspace 的圖示與 host 的配色，下一支 P3d-2 的編輯介面可以直接重用既有的 icon picker 與色盤。**編輯介面在 P3d-2**，這一版只能用 dev hook 設。
- **沒命名、沒選圖示、沒選配色、沒有本機 profile 的使用者：兩種側欄的 DOM 與今天逐項相同**（測試釘住；也不會寫出任何新的 storage key）。
- **有本機 profile（slave）時，Home 按鈕打開 profile 切換器**：master（帶 `Master` 標示；attach 了 master 才有同步狀態點，follower 視窗也看得到）、接著是各個 slave，畫面上那個打勾；快捷鍵 `switch-workspace-home` 開同一個選單。切到哪個 profile，Home 顯示的就是那個 profile 的名稱／圖示／配色——slave 是「永不同步」的世界，這是最需要看得見的地方。
- **`components/Menu.tsx`——repo 第一個真正的 menu primitive**：portal 到 `document.body`（44 px 的窄版側欄是 `overflow-hidden`）、`role="menu"`、方向鍵循環並跳過不可用的項目、Home／End、Enter／Space、Escape、Tab；焦點進打勾的項目、關閉時回到 trigger（但不從使用者已移去的元素搶回來）；翻面並夾在 viewport 內、避開 Electron 標題列。
- 外觀在 promote 時**跟著世界走**（回滾時也還原）；複製 slave 不複製外觀。`busy` 每 250 ms 靜默重試、距第一次點擊 4 秒才 toast；`unsettled`／`superseded` 用中性訊息；`write-failed` 帶細節。

Review（codex R1＋攻擊方＋critic，全 `gpt-5.6-sol`）：五條修四條——切換進行中切側欄寬窄可以再起第二次切換（→ in-flight 的切換改由 store 擁有、帶 generation；關選單＝停止嘗試，不假裝能撤銷已送出的呼叫）、焦點所在的項目被別的視窗刪除後鍵盤失效、Escape 同時關掉底下的面板（→ capture 階段處理並消費，IME composition 不攔）、busy 項目缺 `aria-disabled`；`Menu.tsx` 的職責併入 #1240。critic 判定四條已修好，追加一條：profile 名稱濾掉控制字元、bidi 控制碼與零寬字元（不做 Unicode normalization；CJK、emoji 修飾符、NFD 不受影響）。真機驗收兩輪、兩種側欄、真的點擊與鍵盤：全過、console 零錯誤。

**#1275（Refs #1255，未關閉）**：WS `sessions` handler 的對帳抽成可呼叫的 `reconcileHostSessions`（純搬移，逐宣告比對零差異）。「切換 profile 後立刻重跑 session 對帳」**做了又拿掉**：codex 兩輪依序證明，拿一份沒有版本的 payload 去判定（`session-closed`、`tmux-restarted`、依名稱 revive）都會改錯一個活著的 pane 的 binding 並推上 SOT；向 daemon 重新要清單也不行——`GET /api/sessions` 是 1 秒 TTL 的快取、建立 session 不會讓它失效。切換後的世界仍由各 host 的下一次 payload 對帳（晚，但不會錯）；根治需要先改 daemon，量測與做法記在 #1255。

待 P3d-2 決定的一個設計點：圖示是（點陣的）logo 時，配色沒有東西可以著色。

vitest 9509、lint、tsc、build 綠。純 SPA，daemon 仍是 411、免 deploy。下一支 P3d-2：Settings › Profile 的現況區塊與 profile 管理（含名稱／圖示／配色的編輯）。

## [1.0.0-alpha.418] - 2026-09-21

### Refactor: standalone tab 的死碼移除——Profile Sync P3c-2（#1271、#1272、#1273）

alpha.417 讓「每個 tab 恰好屬於一個 workspace」成為政策＋持續性不變式之後，standalone tab 的 UI 與 helper 已經到不了；這一版把它們刪掉（52 檔分三個 PR、淨 −500 行）。刪除型的改動沒有 mutation 可做，改成**反向驗證**：每一類刪除都附上證明「零引用／到不了」的 `rg` 指令與結果。

- **刪掉的**：`move-tab-to-standalone`／`reorder-standalone-tabs` 兩個 drag action、`home-header` 作為 drop target、`HomeRow` 的 tab 清單與展開 chevron、Home 的未讀 badge 與狀態點（那些未讀現在顯示在擁有該 tab 的 workspace 上，原本就有測試）、`isStandaloneTab`、`reorderStandaloneTabOrder`、`MigrateTabsDialog`、`getVisibleTabIds` 的 Home 分支、`closeTabInWorkspace` 的 standalone 分支、Profile Sync collector 的 standalone census。**Home 列在窄版與寬版都是單一按鈕**（點了聚焦第一個 workspace；P3d 會把它變成 profile 切換器）。
- **過渡狀態落在安全的預設上**（codex R1＋攻擊方四條，同一個根因——standalone 分支刪掉之後，過渡狀態落到了不安全的預設）：`activeWorkspaceId` 暫為 null 而有 workspace 時，可見範圍不再是整個 `tabOrder`（「關閉其他分頁」會關到別的 workspace 的 tab），而是 active tab 所屬的 workspace、否則第一個；active 的孤兒 tab 在被收養前可以用快捷鍵關掉；收養到 active tab 時 workspace 指標跟著走（只在那時）；在收養空窗內切 profile 不再把孤兒連同 master 一起靜默停放——要停放的世界由**同一個**純函式 `repairTabOwnership` 修好，且只在 membership 確知靜止時才修，否則回可重試的 `busy`。
- **有界的出口**（critic）：靜止閘會被持續的 membership 變動餓死（忙碌的 agent 連續開關 tab 做得到）→ 改以「**同一個待修狀態持續存在多久**」為準：同一個 tab 連續待修滿 3 秒就修它、而且只修年齡已滿的（年齡未滿的可能正是別的視窗剛新增、歸屬還在路上的那一個）；unsettled 期間不累計。`sections-unrendered` 保留——它與 standalone 無關（SOT 上有這台從沒見過的 workspace 的 `tabs.<wsId>`）。

Review：codex 三次（R1、攻擊方、critic），全 `gpt-5.6-sol`；critic 對 F1–F4 判定已修好，唯一追加的一條已修並以測試釘住上界。真機回歸兩輪（修正前後）十二步：每一步孤兒 0、重複歸屬 0、雙視窗一致、兩種側欄的 Home 都是單一按鈕、空窗內關孤兒乾淨、console 零錯誤。spec §9.14 與 plan 的「P3c › As built」記下給 P3d 的契約（`getVisibleTabIds` 在指標為 null 時的語意、`repairTabOwnership`、切換多了一種最壞 3 秒的可重試 `busy`）。

vitest 9321、lint、tsc、build 綠。純 SPA，daemon 仍是 411、免 deploy。下一支：#1255（切換後重跑 session 對帳）→ P3d（Home 變成 profile 切換器、Settings › Profile）。

## [1.0.0-alpha.417] - 2026-09-21

### Change: 每個 tab 恰好屬於一個 workspace——Profile Sync P3c-1（#1269）

Profile Sync 裡**唯一會改變一般使用者體驗**的一支（spec §4.3；使用者兩台機器實測 standalone tab 都是 0）：不屬於任何 workspace 的「standalone tab」這個概念取消。standalone 的 UI 與 helper 這一版仍然編譯得過、只是到不了，下一支 P3c-2 才刪。

- **`insertTab` 永不靜默 no-op**：沒指定目標 → active workspace → 第一個 workspace → 新建 `Unsorted`（`未分類`）。它的 id 是固定的 `unsorted`（其他 workspace id 一律來自 `generateId()` 的 6 碼 base36，不可能相撞），所以兩個視窗、或兩台機器各自收養時會收斂成同一個 workspace；已存在就沿用、不改名。
- **持續性的不變式，不是開機步驟**（`features/workspace/lib/adopt-standalone.ts`）：零個 owner → 收養；多個 owner → 依 workspace 順序保留第一個。只在 master 世界 settled、且 membership **靜止 500 ms** 之後才動——以 membership 簽章做 debounce：別的視窗的 `addTab`＋`insertTab` 是分兩次 rehydrate 抵達的（同步收養會把每個視窗新開的 tab 都拉進 `Unsorted`），而忙碌的 agent 每秒改寫 tab store 好幾次（單純的 debounce 會被餓死）。開機也走同一個延遲。
- 刪 workspace 而保留 tabs → 移到相鄰的 workspace（一個都不剩就移到新的 `Unsorted`）；scope 指向被刪 workspace 的 settings tab 一律關掉，不論鎖定與否、不論被拖到哪裡（以前會留下一個「Workspace not found」的孤兒）。
- Home 按鈕與 `switch-workspace-home` 暫時改為聚焦第一個 workspace（P3d 會把 Home 變成 profile 切換器）；零 workspace 的空狀態改用 i18n 字串。

Review：codex R1＋攻擊方七條，修四條（throttle 不是 debounce、開機同步收養繞過延遲、不變式不修多 owner、被鎖定的 settings tab 被搬走）；critic 對增量判定 **approve、無 material findings**。不修並附理由：`unsorted` id「劫持」既有 workspace（沒有任何路徑能產生這個 id）；`store.ts` 職責 → #1240；sync apply／tear-off 路徑的孤兒 settings tab 與懸空 id → #1268。真機驗收兩輪（修正前後各一；真的頁面、reload、雙視窗、實際點擊 Home）：每一步孤兒 0、重複歸屬 0，別的視窗新開的 5 個 tab 留在自己的 workspace，磁碟上的雙 owner 在 reload 後收斂，沒設 master 的人零 `purdex-profile*` key。

vitest 9312、lint、tsc、build 綠。純 SPA，daemon 仍是 411、免 deploy。下一支 P3c-2：刪除 standalone 的死碼。

## [1.0.0-alpha.416] - 2026-09-21

### Fix: 預設 `upload_dir` 移出家目錄（#1266）

上傳暫存區的預設值本來是 `~/tmp/purdex-upload`——一個純暫存目錄卻佔在家目錄第一層。改放到 daemon 自己的狀態目錄底下：`<data_dir>/uploads`（預設 `~/.config/pdx/uploads`），與既有的 `<data_dir>/logs`、`<data_dir>/nex` 一致；新位置的父目錄實測是 0700，比原本 755 的 `~/tmp` 更私有。

**既有安裝不受影響**：`config.toml` 都已寫死 `upload_dir`，所以只有全新安裝吃得到新預設；各 host 的實體檔案搬移另外執行。順帶修掉一個測試隔離問題——`TestDefaultsHaveUploadDir` 原本呼叫 `config.Load("")`，會讀到真實的 `~/.config/pdx/config.toml`，使用者自訂的 `upload_dir` 會滲進「預設值」斷言（改成指向不存在的檔案，與同檔 `TestLoadDefaultsWhenFileNotExist` 同作法）。

**不在本次範圍**：TTL 自動清理（目前只有 Host › 上傳暫存頁手動刪除，沒有任何保留期限）、上傳目錄與檔案的權限位元（維持 0755／0644）。

## [1.0.0-alpha.415] - 2026-09-20

### Feature: Profile Sync P3a＋P3b——可訂閱的跨視窗同步狀態、slave profile 與 active 指標（無 UI）（#1253、#1254、#1257–#1261）

Profile Sync 第五、六支，**仍然沒有 UI**：唯一的入口是 dev-only 的 `window.__purdexProfileSync`（production build 裡 grep 不到）。沒設 master、沒用過 slave 的使用者與 alpha.414 相同——零新 key、零 listener、零 timer，由不 mock 的鐵則測試守著。P3 的 plan（`docs/specs/2026-09-20-profile-sync-p3-plan.md`）先量再寫、經 codex 審過（10 條、3 critical 全採納）後拆成六個 PR 階段；這一版是前兩個。

**P3a（地基）**：device name 搬到 P4b 刪不到的地方（`useDeviceNameStore`／`lib/device-name.ts`，同 key 同 bytes、免 migration；預設名稱的解析改由 store 擁有——原本只有 device-state 的 uploader 會解析，P4b 之後 driver 會永遠回報 `Browser`）。`start.ts` 不再吞掉 executor 的狀態：`subscribeProfileSync`＋有快取的 snapshot＋`useProfileSync` hook；**leader 發布、follower 讀**；指令一個 key 一個（陣列是 read-modify-write，必然掉指令）。review 的五條 findings 同一個根因——跨視窗資料沒綁 master 身分——→ status 與指令一律綁 `hostId|profileId|attachGeneration`，`resolve` 綁使用者當時看到的整個 lock（status＋currentHash＋sot＋conflict pair）。

**P3b（slave 與 active 指標）**：`useLocalProfilesStore`（slaves、active 指標、停放的 master；永不進 SOT）。兩個 tab store 帶**世界標記＋epoch**，`master-world.ts` 是讀寫 master 世界的唯一入口——三個 store 的 epoch 相等且標記等於指標才算 *settled*，否則 collector 什麼都不回報、apply 回 `busy`、executor 不 sweep 不 push（**寧可沉默，不可錯報**）。slave 在畫面上時 applier 寫進**停放的** master；刪 host 會處理每個停放的世界，undo 依世界歸屬還原。`switch-active.ts`：三個 store 的單一同步區塊＋rollback、唯一的複製（所有 id 重映射、tmux 綁定照抄）、唯一的搬移（`promoteToMaster`，attach 中拒絕）。`settings` 裡唯一以 workspace id 為 key 的欄位只投影 master 的 workspace，並重現、關掉一個資料遺失：`settings` 比 `workspaces` 先到時新 workspace 的設定被丟掉、再把「沒有那筆」推回 SOT → `settings` 對 `workspaces` 加 pull **與** push 閘。

**真機驗收三輪**（worktree `:5175`、活的 mlab daemon、兩個 client＋同 client 雙視窗；以 sentinel 字串掃 daemon 上每個 section 的 payload）：**slave 的內容在 SOT 上的出現次數三輪都是 0**；複製、在 slave 裡大量編輯、切回 master 全程零寫入；別台改 master 時停放的 master 跟著更新、畫面不動。抓到兩個 9000 個單元測試與 codex 都沒抓到的問題：(1) 拿著舊世界的視窗一寫入就把舊 epoch 蓋回磁碟 → 儲存層 **epoch fencing**（`lib/storage/world-fence.ts`）；(2) 量測到 BroadcastChannel 觸發的 rehydrate 在另一個 renderer **落後恰好一筆寫入**（磁碟 7 個 workspace、記憶體 6 個）——整個 app 既有的潛在問題，平常下一次寫入就補上，三個 store 必須精確一致時才變成永久卡住 → `syncManager` 同時聽原生 `storage` 事件。第三輪全過（四輪跨視窗來回，兩個視窗每一輪都 settled 在同一個世界）。

Codex：P3a 三次、P3b 五次（R1、攻擊方、critic 三輪），全 `gpt-5.6-sol`。P3b 的 critic 三輪找到的是同一類問題的逐步收窄（同 epoch 並行 → read-check-write 不原子 → 取得鎖後 storage 視圖過期），都源自 localStorage 沒有跨 renderer 的一致性；前兩條已修（每次操作唯一的 epoch、有 Web Locks 就用、沒有就把殘留用測試釘成已知行為），第三條**刻意不修**、記為殘留 #1256（人手到不了的毫秒級窗口、後果是「後切換的人贏」、屏障仍成立）。follow-up：#1255（切換後重跑 session 對帳，P3d 的前置）、#1256、#1240（拆大檔，`sync-status.ts` 併入）。

vitest 9256、lint、tsc、build 綠。純 SPA，daemon 仍是 411、免 deploy。下一支 P3c-1：每個 tab 都有 workspace（standalone tab 移除）。

## [1.0.0-alpha.414] - 2026-09-20

### Feature: Profile Sync P2b-2——driver 接線：collector、executor、租約 leader、`startProfileSync()`；真機驗收（#1247、#1249、#1250、#1251）

Profile Sync 第四支，**第一次真的接線**：`main.tsx` 呼叫 `startProfileSync()`。鐵則：沒有設定 master 的使用者，app 與今天相同——只訂閱 `useProfileStore`，不訂閱其他 store、不算 hash、不讀寫租約、不發請求、不排 timer；由一個**完全不 mock** 的測試守著（真的 collector／executor／lease／api 在後面：一次訂閱、零 `fetch`、零 `crypto.subtle.digest`、零 `purdex-profile*` key、60 秒後 timer 數為 0）。**目前設定 master 的唯一途徑是 dev-only 的 `window.__purdexProfileSync`**（production build 裡 grep 不到），P3 的 wizard 之後會呼叫同一條 `attachMaster(host, profile, direction)`／`detachMaster()`。38 檔，依規則在 commit 邊界拆成四個 PR、連續 merge。

零件：reducer 加 `locked:invalid`（SOT 上有這台 client 拒絕套用的 payload——不會每 30 秒重抓，觀察到**任何**與被拒那份不同的 SOT 就解鎖：absent 沒有 rev 可比、profile 重建時 rev 會倒退）並能還原持久化的 conflict；`apply-to-stores`（把拉下來的 section 寫進 store）；`collector`（per-section 500 ms debounce）；`leader`（localStorage 租約，檔頭明寫「IT IS NOT A LOCK」）；`executor`（dispatch → reduce → persist → decide → act：profile 層級一次只送一個寫入，所以 schema 409 擋得住「之後」的寫入；reindex single-flight、失敗的清單不告訴 section 任何事；profile 從清單消失走 executor 層級的 `profileGone`——對每個 section 說 absent 會讓狀態機去 pull 刪除、清光本地；`hosts`／`workspaces` **settled**（synced＋已 index＋SOT 沒動＋閒置）之前不 pull `tabs.*`）。另有兩個 store 依各自原始碼的宣告改為 device-local、**待使用者確認**：`useEditorSettingsStore`（「the small-screen laptop may want fontSize 11 while the big monitor uses 14」）；settings ordinal → 3。

**整合時，被要求「契約自相矛盾就如實回報、不要默默選一種解讀」的 subagent 報上來的**：`setState`＋`persist.rehydrate()` 的前提逐 store 家族驗證成立，但挖出兩個沒人料到的事——rehydrate 的 heal 是原地改 state 物件、不通知 subscriber 也不寫回（→ 之後補一個空 `setState`）；tab／workspace store **不能** rehydrate（沒有 hook 可跑，卻會從 JSON 重建每個 tab 物件、讓所有 pane re-render）。「`hosts` 是 `synced`」不等於 settled（乾淨但落後的也讀作 `synced`，此時套 `tabs.*` 會把別台新增的活 pane 標成 `host-removed` 再推回去）。`pull-applied` 要帶兩個 hash（SOT 的進 base、套用後從 store 重算的進 `currentHash`；只有一個時 sanitiser 的修正永遠推不回去）。別台新增的 workspace 到貨時，本地那個空的 `tabs.<id>` 是 placeholder 不是編輯。移除 host **重用 app 既有的 `deleteHostCascade`、一行都沒改**（分段寫入繞過「只剩一台不准刪」，以「最終狀態 deep-equal 手動刪除」的測試守著）。

**真機驗收**（spec §9.10；worktree `:5175`、活的 mlab daemon、兩個 Playwright context ＝兩台 client；token 經 0600 的 storage-state 檔載入，從未出現在指令列或輸出）三輪全過：鐵則、第一次 push、第二台**無人介入**地 pull 且零寫入、**傳播 519 ms**、converged（rev 只前進一次）、衝突只鎖一個 section 且其他照常同步、只是落後不算衝突、dirty 拒絕 inbound、section 生滅無孤兒、schema lock 後零寫入、焦點與 split 比例不鏡射、attach 中不可刪 profile、同一 context 雙視窗（單一 leader、follower 的編輯經 leader 恰好寫一次、關閉後接手）。**抓到三個 8500 個單元測試抓不到的真問題**：`crypto.subtle` 在純 HTTP 的 tailnet IP 上不存在——而那正是兩台工作站 dev 模式載入 app 的方式——一個 hash 都算不出來（P2a 的量測其實寫了這點，我歸為「既有風險」放過；→ 純 JS SHA-256 fallback，與 WebCrypto 逐位元組相同）；attach 沒有方向，第二台 client 一開場就衝突（→ 定案第 10 條的 push／pull）；schema lock 只在 reindex 時評估，但 client 是經事件得知新 rev 再 pull 的，舊 client 照寫不誤。流程教訓：第一輪我讓 subagent 在同一個 worktree 做 mutation test 時頁面還開著，**Vite HMR 把故意改壞的 SHA-256 送進活頁面、推上了真 daemon**（→ 真機頁面開著時不做 mutation）。未跑：air-2026 的跨機驗收（它的 App 載入主 checkout 的 `:5174`，隔離的 worktree session 無法 `git pull`）。

Codex 五輪（R1、兩份攻擊方、critic、兩次窄範圍 re-review），每一輪都找到真東西、一輪比一輪窄，**沒有任何一輪出現 critical**：detach 先 await 伺服器端清理而 driver 還在跑；同 master 再 attach 時 `pull` 沒生效、`pendingDirection` 永久殘留（日後的衝突會被過期的方向靜默覆寫）→ 每次 attach 都是全新對帳；master host 的 ip／port 被原地改掉 → attach 當時的 endpoint 持久化、不同就擋住；attachment 確認前就開始同步 → reachable ＝ attached＋connected，且在**每個請求送出的地方**再問一次；非法 workspace id 被推進 `workspaces`、讓別台的 guard 拒絕而鎖死（會傳播的毒資料）→ 視為 device-local；rollback 的範圍比 apply 小。我有一條以證據反駁、critic 判定反駁成立（`BroadcastChannel` 是單例，main 上早有 18 個 store 註冊）。critic 對四個**修法**提出反對，全部成立並重做——最細的一條：attach 等 PUT 的 15 秒內舊 writer 還活著 → suspension（帶 token、會到期、同步設下、由 `localStorage` 直接讀而不等 BroadcastChannel）；再下一輪又找到「suspend 晚了一個 microtask」與「別的視窗的 detach 被較早開始的 attach 反轉」（後者的修正需要一件沒人指定的事：被超車的視窗必須先從 storage rehydrate 才能寫任何東西，否則記憶體裡的舊 master 會被 persist 回去、蓋掉對方的 detach）。三次要 subagent「查證這個前提、不成立就停下來說」，三次前提都不如我所述。剩下的以殘留的名義寫在註解裡：`localStorage` 不是交易式儲存、讀後動作不是原子、已送出的位元組收不回來。follow-up：#1240（拆大檔）、#1244（conflict stash → IndexedDB）、#1248（刪 host 後 Undo 的既有缺口）。

vitest 8695、lint、tsc、build 綠。純 SPA，daemon 仍是 411、免 deploy。下一支 P3：profile switcher、Settings › Profile（wizard／Resolve 面板）。

## [1.0.0-alpha.413] - 2026-09-20

### Feature: Profile Sync P2b-1——傳輸層：CAS client、`'profile'` 事件、control／section 兩個儲存、client identity（#1242、#1245）

Profile Sync 第三支，**仍未接線**（沒有任何 service 啟動；除 `keys.ts` 外沒有非測試檔引用新模組），driver 是 P2b-2。因為整包 31 檔，依規則在 commit 邊界拆成兩個 PR。唯一可觀察的變化：首次使用時多寫一個 `localStorage` key `purdex-client-identity`。

`lib/client-identity.ts`：client id 原本住在 P4a 要刪的 `useSyncStore`，但它有兩個 caller 屬於不會被刪的 Storage-backup。新家用 `browserStorage` 直接讀寫、**沒有永久的 in-memory cache**（以 storage 為準、寫入後讀回，兩個視窗同時首次呼叫會收斂到最後寫入者，而不是各自永久 cache 自己產生的那個）；**沿用既有 id**（daemon 上的 device-state 與 backup 紀錄都掛在它上面）；`useSyncStore.getClientId()` delegate 過去並仍填 state 欄位；`isClientIdPersisted()` 讓 P2b-2 的 driver 在 identity 撐不過 reload 時拒絕 attach。`lib/profile/api.ts`：P1 十條路由的 client，**任何函式都不 throw、不 reject**——每個結果都是 discriminated union，因為 executor 要保證一個 flight 恰好一個 terminal event，而且「清單請求失敗」必須無法被表示成「清單是空的」；status→outcome 對照表直接從 Go 原始碼整理、一列一個測試；409 先分支再讀 body；每個欄位驗證而不是 cast；request 的建構（路徑編碼、`JSON.stringify`）也在受保護入口之內。`profile-ws-dispatch.ts`：只接受 daemon 實際送出的兩種 wire shape（刪除＝`deleted:true` 且 `hash:""`；live＝64 hex），`subscribeProfileEvents(fn)` 回傳只會移除自己的 unsubscribe。`useProfileStore`：control plane（master、autoSync），註冊 `syncManager`——每個視窗必須對它有一致認知。`section-store.ts`：leader 的工作狀態，**一個東西一個 key**（每個 section 一個 key、每個 conflict payload 以 hash 定址）。另外一項 P2a 的選擇被翻回：`useModuleEnabledStore` 的原始碼自己寫明模組開關是 device-local 偏好（「資源有限的 host 可以關掉不想跑的模組」），從 settings projection 移除、`settings` ordinal → 2——**待使用者確認**，翻回來是一行 projection ＋再 bump 一次。

**兩個讀程式碼（而不是讀 plan）才發現的事實**：`getDaemonBase` 對未知的 hostId **不會 throw，而是默默退回 active host、再不然就是 `127.0.0.1:7860`**——master host 若從 store 消失，CAS 會被送到**另一台 daemon**、還帶著那台的 token；`api.ts` 先確認 `hosts[hostId]` 存在，否則回 `failed/unknown-host`、一個 byte 都不送。`AbortSignal.timeout` **不受 fake timers 驅動**，用它寫的 timeout 測試等於沒驗；改成手寫 `AbortController`＋`setTimeout`，每條路徑都清、並與請求賽跑。

Codex：**plan review 十七條（五條 critical）全收**，我自己點名懷疑的兩個設計都在寫任何一行 driver 之前被打穿——Web Locks 只存在於 secure context（Electron dev 視窗載的是純 HTTP 的 `100.64.0.2:5174`），而且我寫的 `acquireLeadership()` 簽名本身就會卡死（`locks.request()` 的 promise 要等鎖釋放才 resolve）→ P2b-2 改用 localStorage 租約＋心跳；「follower 的編輯經 `syncManager` 進 leader」不成立，因為 `useEditorSettingsStore` 根本沒註冊 → 每個視窗在原生 `storage` 事件上 rehydrate 未註冊的投影 store。其餘：attachment 路由沒有任何 task 呼叫（daemon 的「attach 中不准刪」形同虛設）、profile 層級一次只送一個寫入（schema 409 才擋得住「之後」的寫入）、`locked:invalid`、hosts payload 不得移除或改指 master 自己的 host、conflict 與 payload 跨重啟持久化、reindex single-flight 且失敗的清單絕不是空清單。PR review：R1 無 finding；兩份攻擊方九條，七條修、兩條延後（#1243 首次啟動的 id race——既有行為，本 PR 反而縮小它；#1240 拆檔）。其中 WS parser 把矛盾事件當刪除是**我在 prompt 裡指定的寬鬆規則、指定錯了**。critic 確認六條關閉，對 section-store 提出兩條有證據的反對：我第一次修 lost update 用的 generation fencing，本身就是 localStorage 上非原子的讀→比→寫（A 讀 g1 → B claim g2 → A 把整份 g1 文件寫回，蓋掉 B 的一切）——在沒有交易的儲存上做 fence 只是把窗口縮小，卻讓 API 看起來像有保證 → **移除 fencing、改成一個東西一個 key**，stale write 再也沒有不相干的資料可以毀掉；剩下的（同一個 section key、兩個 leader、last writer wins → 最壞是一次假衝突，SOT 由 daemon 的 CAS 保護）寫成殘留、不宣稱防住了。第二條：1 MiB 的人為上限讓較大的 conflict 靜默地無法跨重啟保存，而「留在記憶體、重啟後重新得出」**不得被描述成符合契約** → 上限回到 daemon 的 5 MiB、由瀏覽器配額決定；存不下時，spec §7 如實記為「違反 §4.6.2 的已知缺口」（#1244，遷 IndexedDB）。增量 re-review approve。

每個 task 與每個修正都回報 mutation 結果；`section-store` 的邊界測試真的把 payload 寫進 jsdom 的 storage（實測配額：每個 origin 5,000,000 個 UTF-16 code unit）而不是 mock `setItem`。vitest 8148、lint、tsc、build 綠。下一支 P2b-2：collector、executor、apply 接線、租約 leader、`startProfileSync()` ＋真機驗收。

## [1.0.0-alpha.412] - 2026-09-20

### Feature: Profile Sync P2a——`spa/src/lib/profile/`，同步的純核心（#1239）

Profile Sync 第二支，**什麼都沒接線**：沒有 store 訂閱、沒有請求、沒有 UI，app 行為與 alpha.411 完全相同；傳輸與接線是 P2b。runtime import 只有 `sha256Hex` 與 lib 自己的模組，其餘全是 `import type`——沒有 store／`fetch`／timer／`Date.now`／`Math.random`／React，範本是 `lib/nex/nex-host-reducer.ts` ＋它的 effects 兄弟檔（那個兄弟檔就是 P2b）。六個模組：`hash.ts`（canonical form ＋ SHA-256）、`projections.ts`（**唯一的 allowlist**：path grammar 含 `*` 與「任意深度排除」用來剝掉 split 的 `sizes`；fingerprint ＋ ordinal；spec §4.5 的 guard test 是 `lib/` 第一個 inline snapshot）、`sections.ts`（store state → section payload；`adoptStandaloneTabs`）、`sync-state.ts`（每個 section 的狀態機：`reduceSection`／`decideSection` ＝ spec §4.6.1）、`profile-state.ts`（schema lock、profile 狀態、`tabs.*` 生滅）、`applier.ts`（per-section 的 `(local, incoming) → next`、`restoreSizes`、`isWellFormedSection`）。

**先量再寫，spec 有幾處是錯的**：`HostConfig` 有五個持久化欄位沒被 `hosts` projection 列入（`order` 還是必填；照原 spec 實作，pull 之後所有 host 的顏色與圖示都會消失）；`workspaces` 是陣列不是 Record；三個 settings store 沒有 `partialize`，且有三個持久化欄位根本不是偏好（`terminalSettingsVersion` 是強制 terminal 重連的計數器、`activeEditingProfile` 是編輯器開在哪個分頁、`knownIds` 是衍生的）→ settings 改成逐 store 明列欄位；`replaceTabSnapshot` 是整個世界替換、吃不下單一 `tabs.<ws>`，applier 重寫成 per-section 純函式。沿用的 `sortKeysDeep` 慣用法也錯了兩次：JS 引擎會重排整數樣的 key（`"9"` 永遠在 `"10"` 前面，輸出其實沒排序），對複本寫 `out['__proto__']` 是設 prototype 不是設 key——`hash.ts` 改成直接組字串。

Codex：**plan review 十七條（六條 critical）全收，全部落在還沒寫出來的狀態機上**——`sot: null` 同時代表「從未存在」與 tombstone，成功刪除後下一次 index 會被讀成「別處刪了、去 pull」然後永遠 404（→ 兩側都是 `{rev, hash|null}`，absent ＝ `hash === null`，對 absent 建立一律 wire `baseRev 0`）；遲到的 index 回應讓已知 rev 倒退 → 發出明知 stale 的 push（→ epoch）；decide 與 push-started 之間的競態（→ flight token）；conflict 只存 hash、拿不回送出去的那份（→ `retainedHashes`）；`Take SOT` 無法表達、`Keep local` 用鎖定當下的 rev 會再 409（→ `forcePull`；鎖住的 section 繼續「知道」較新的 rev，只是拒絕套用）。實作中 subagent 被要求「plan 自相矛盾就如實回報、不要默默選一種解讀」，於是又收斂了一輪：restore-local 排到 reindex 之前（還原是本地動作，不該等網路）、先看過 index 才准動作（`reconnected` 就是 reconcile-on-connect）、409 到達時已 clean 就不上鎖、送出的 delete 撞 409 要能還原成「不存在」、§4.6.3 兩句互相矛盾的話以「client 先前知不知道這個 workspace」調和。PR review：攻擊方三條＋ R1 兩條全修——settings guard 放行了 builder 永遠不會產生的 payload，搭配「缺席欄位＝對方清除」，一個 `{"purdex-ui-settings":{"terminalSettingsVersion":1}}` 就清空所有 UI 設定且永不收斂（→ 只准已知 store、只准列名欄位、`applySettings` 比對值形狀，任何 rejected 就整份不套用）；選了 Keep local 之後、還原之前的新編輯被舊 snapshot 蓋掉；持續打字讓每個 index 回應過期、section 完全停止同步（我在 focus 裡點名請它評估的活鎖，確認成立 → 獨立的 index epoch，本地編輯不動它）。critic 確認前三者關閉，對 R1-2 的修法提出有證據的反對：「飛行中學到 live SOT」不能證明它比 409 新——事件與 HTTP 回應走不同通道、沒有因果順序，R1 與 critic 各自說中歧義的一個方向 → **不猜**：不上鎖、標 index stale，由權威 index 決定；連帶「stale 期間不做 convergence fold」與「absent 的一側沒有 rev」。增量 re-review 無 finding。體質債 #1240（`applier.ts` 的 apply 與 guard 拆開）。

`lib/profile` 396 測試（`sync-state` 有 seeded 30,000 步 property test、每步約 20 條不變式，plan review 的每個事件序列都是逐字的回歸測試），每個 task 與每個修正都回報 mutation 結果。15 檔。vitest 7708、lint、tsc、build 綠。下一支 P2b：CAS client、`'profile'` 事件、collector 訂閱、reconcile-on-connect、`useProfileStore`。

## [1.0.0-alpha.411] - 2026-09-20

### Feature: Profile Sync P1——daemon `profiles` 模組，per-section compare-and-set 的 SOT（#1237）

Profile Sync（spec `docs/specs/2026-09-20-profile-sync-spec.md`：一個 profile ＝整份 Purdex 狀態，經 dev host 同步；取代 device-state／workspace snapshot／Sync 模組）的第一支，**daemon-only**：沒有任何 SPA 變更、UI 碰不到，十條路由目前只能用 `curl` 打。`internal/module/profiles`（`profiles.db`，0600——裡面有 host token，`Init` 先以 0600 預建檔再讓 sqlite 開，連 `-wal`／`-shm` 一起 chmod）：profile 是一組各自編 revision 的 section（`hosts`／`settings`／`workspaces`／`tabs.<wsId>`），唯一的寫入動詞是 `PUT …/sections/{section}` 的 compare-and-set——`rev == baseRev` 套用並廣播；rev 不同但 hash 相同回 `applied:false`、**完全不寫也不廣播**（「驗算，沒改變就不寫回」）；rev 不同 hash 也不同回 409 `conflict` 帶 SOT 那一側；shape 不合回 409 `schema`。daemon 刻意是笨的：比對、儲存、廣播，永不合併。

每個 CAS 都是單一條件式 SQL 讀 `RowsAffected()`、不開 transaction（profile 存在性也併進語句：`INSERT … SELECT … WHERE EXISTS`）；分類用的 re-read 可能晚一個 revision，無害，client 下次事件會重跑 §4.6.1。schema 檢查先於 revision 且 fail closed，stored ordinal 永不下降（`MAX(ordinal, ?)`）。`PutSection` 是有上限（4 次）的重試迴圈，耗盡回 503 `Retry-After: 1`。broadcast 是注入的（`"profile"` 事件，key `profileId／section／rev／hash／writerClientId／deleted` 是與 P2b 的 wire contract，測試斷言精確 key 集合）。

Codex：**plan review 八條全套**，其中一條 critical——section DELETE 若真的刪列，rev 會從 1 重來：A 刪 rev 1 → B 重建又拿到 rev 1 → A 因回應遺失重試 `DELETE baseRev=1` 就刪掉 B 的新內容（ABA）。改成 **tombstone**（`deleted=1, rev+1`），一個 `(profile, section)` 的 rev 在 profile 存活期間嚴格遞增，這也是 §4.6.1「`rev < baseRev` ⇒ profile 被重建過」成立的前提。其餘：同 fingerprint 時舊 client 能把 ordinal 降級、`DeleteProfile ∥ PutAttachment` 非原子、attachment DELETE 的 `clientId` 沒有來源（改 query 且須與路徑的 profile 相符）、`:memory:` 單連線測不到競態（加檔案型 WAL 並行測試）、5 MiB 是 payload 上限不是 body 上限、路由是十條不是九條。PR R1 ＋攻擊方各一條、同一根因：`DeleteProfile` 刪 profile 列與掃 sections 是兩句——中間 crash 會讓含 token 的孤兒 sections 永久殘留（重試只得 404），空窗內並行的 section UPDATE 還會被確認並廣播後才被掃掉；改成單一 transaction（第一句就是條件式 DELETE，直接取寫鎖，WAL 下沒有 snapshot 升級），以故障注入測試把關。攻擊方**找不到**讓兩個 writer 同時 applied、rev 倒退或繞過 schema gate 的時序。critic 同意兩條並確認已關閉，另抓一條有證據的 spec drift：tombstone 之上重建會覆寫 ordinal（7 → 1）——我修 plan 時寫的「tombstone 沒有 shape 可保護」是錯的；tombstone 保留 fingerprint／ordinal 並過同一道 schema gate（先於 `baseRev`）。每個修正都有 mutation 佐證（拿掉 `AND rev = ?`、`MAX` 改回賦值、tombstone 改回真 DELETE、`DO NOTHING` 改 `DO UPDATE`、tx 拆回兩句……各自讓指名的測試轉紅）。15 檔（≤ 20 檔合格；5,057 行裡 docs 989、測試約 2.5K）。`go build／vet／test ./...` 綠、`-race` 綠。下一支 P2a：`lib/profile/` 純核心。

## [1.0.0-alpha.410] - 2026-09-19

### Feature: exec pane 成本 panel——每 turn 表、token 拆解、模型、host 額度（#1233，P-B4.2）＋ P-B4 真機驗收

alpha.409 的 `$0.26` 現在點得開：`FloatingPanel`（拖得動、Escape／點外面關）由上而下是總計一行（`$0.2577 · 12 turns · 22 API rounds · 1m 05s API / 1m 33s wall`）、四格 token（output／input／cache read／cache write，來源是 `modelUsage` 加總）、分模型拆帳（沒有 `modelUsage` 的舊幀有成本時加一行「n 輪沒有分模型拆帳」，Σ models ≠ total 才有解釋）、每 turn 一列的表（`#`、成本 4 位小數、輸出、快取讀、API／牆鐘、回合、`error` 格帶原始 `subtype` 當 title；開啟時自動捲到最新一輪；panel 開著時新 turn 落地就多一列）、最後一行 host 額度——`fetchNexHost` 每次掛載只抓一次、daemon 回 `quota` 才顯示，標題寫「**主機額度 — <帳號>**」因為那是 host 登入帳號的視窗，未必是這顆 execution 計費的帳號（契約 §1.7）。寬度 `min(440, viewport − 16)` 且跟著 resize 走。

Codex：R1 抓固定 440px 在窄視窗裁切（修：viewport 上限＋resize 監聽，re-review 再抓我加的 240 下限與 render-time 讀值，一起拿掉）；攻擊方兩條——host 切換殘留舊帳號／額度（critic 有證據反對：exec pane 以 `hostId:executionId` 為 key 會整個重掛，產品路徑不可達；仍照修，effect 開頭 `setHost(null)`）、額度百分比被四捨五入（`99.6` → `100%` 會誤判額度耗盡；改顯示原值）。critic 另核對 subagent 自行決定的三件事（models 空但有 unsplit 仍顯示 note、表頭 sticky、「API / wall」「$」「out」不進 i18n）皆與 spec 相容。

真機驗收（spec §6.1，worktree :5175 ＋ playwright）：12-turn 的 execution 全部段落數字與 fixture 一致、2 個 error 列、自動捲到 `#12`、額度列 `wake@protype.tw 5h 20% · 7d 64%`；subagent 那顆一列 `$0.0882` 不重複；panel 開著從輸入框送一輪 → `#2` 追加、header `$0.09` → `$0.22`、console 0 錯。P-B4 至此完成——使用者定案第 5 條（tool 摘要／行號 diff／成本 hover／打字機）全部落地。follow-up：#1234（HoverTooltip 共用 a11y）、#1235（每幀重算 O(N)，先量 5000 則再決定）。vitest 7312、lint、tsc、build 綠。

## [1.0.0-alpha.409] - 2026-09-19

### Feature: exec pane 成本摘要——rollup ＋ header hover（#1231，P-B4.1）

使用者定案第 5 條的最後一項（tool 摘要／行號 diff／成本 hover／打字機必做）開工，**走 client 端**：資料全在 `result` 幀裡，SPA 自己算自己畫，不做 Nexen rollup。先量再寫，三件事實推翻了「照記憶寫」會犯的錯：① `total_cost_usd` 恆等於 `Σ modelUsage[*].costUSD`（13 幀全對）；② **多模型 turn 的 `usage` 只涵蓋一個模型**——seq 8 haiku＋sonnet 那輪 `usage.output_tokens` 364 但 `modelUsage` 加起來 376、input 2 vs 911；seq 974 cache read 46 401 vs 80 127——所以 token 拆解要加總 `modelUsage`，`usage` 只當沒有 `modelUsage` 時的 fallback；③ **subagent 沒有自己的 `result` 幀**（用 `trusted` profile 真的派了一顆用 Agent 工具的 execution 才量到；`standard` profile 根本沒有 Agent 工具），花費已含在父 result（0.088 vs 同 brief 無 subagent 0.027），所以只加 `parent_tool_use_id == null` 的 result、絕不再加 subagent 幀的 `message.usage`。`num_turns` 是一個 Nexen turn 內的 API 回合數，不是 Nexen 的 turn。

`lib/nex/cost-summary.ts` 的 `costSummary(messages)` 把頂層 result 摺成每 turn 一列（成本、四類 token、API vs 牆鐘、回合、`isError`、模型）＋總計＋跨 turn 的 per-model 拆帳；`modelUsage` 逐 entry 驗有效性、`usage` fallback 對「存在但畸形」的欄位整個作廢而非補 0、所有累加走 `addFinite`（溢位 clamp 在 `MAX_VALUE`，與訊息順序無關）。header 的 `$0.26` 變成 button：history 全載完前顯示 `$…` 且 disabled（history 本來就分頁載到底才開 SSE，所以永遠不會顯示部分加總）；hover 800 ms 出一行 `12 turns · $0.2577 · 4.5k out · 1m 05s API / 1m 33s wall`，button 以 `aria-describedby` 指向它（`HoverTooltip` 為此多一個 optional `id` prop，不傳時 DOM 一字不變）。點開 panel 是 P-B4.2。

Codex：plan review 九條全套（`isError` 欄位、砍 `ttft`、F6 論述收斂、quota 要標 host 帳號、`modelUsage` 逐 entry 有效性、ExecutionView 整合測試、outside-click 測試、PR 行數 gate）；PR R1 無 finding；攻擊方四條——溢位（修）、usage 混合畸形（修）、每幀重算 O(N)（critic 有證據反對：與既有訊息 map 同量級，follow-up）、tooltip a11y（critic：共用元件既有缺口，本 PR 只補 described-by）；re-review 再抓「丟棄溢位 addend 會依順序不同」改 clamp。critic 另指 831 行超過 plan 寫的 800——plan 把 CLAUDE.md「≤ 800 行**或** ≤ 20 檔」寫嚴了，12 檔合格，措辭已對齊。vitest 7283、lint、tsc、build 綠。

## [1.0.0-alpha.408] - 2026-09-19

### Feature: Edit／Write 結果的行號 diff view（#1225，P-B3.3）＋ P-B3 真機驗收

P-B3 最後一支。`lib/nex/diff-lines.ts` 的 `diffRows(hunk)` 從 `old_start`／`new_start` 起算走 unified hunk：` ` 兩邊推進、`-` 只推舊、`+` 只推新、`\ No newline…` 是 meta 列不推進，未知首字元當 context（fail-safe）。`ToolDiffView` 照 console R11：每個 hunk 一行 `@@ -a,b +c,d @@`、兩欄右對齊 `tabular-nums` 行號、符號欄、文字欄；增刪整列鋪底色、meta 列淡化斜體；`diff.truncated` 尾端加一列提示——**即使 daemon 把 hunk 丟到 0 個也要顯示**（攻擊方抓到 `hunks: []` ＋ `truncated` 時提示被吞；daemon 以整個 hunk 為單位從尾端丟，第一個 hunk 就超過 2000 行時就會這樣）。掛在 result block 展開區、raw content 之前；沒有 diff 時 DOM 與 alpha.407 逐字相同（Task 5a 的 baseline snapshot 一路不帶 `-u`）。hunk 的四個數字與所有計數改要求 `Number.isSafeInteger`（超過 2^53 的整數 `++` 後會重複行號）。critic 判「一次同步渲染 2000 行 ≈ 10k DOM」為 follow-up 而非阻斷（預設收合、點了才渲染、尚未量測）→ #1227。

真機驗收（spec §6.1，worktree :5175 ＋ playwright，daemon alpha.405）五步全過：N2 execution 的 history 顯示 `primary_arg` 摘要、`4 lines`／`+1 −1`、行號 diff；pre-N2 的七個 Bash 呼叫與 alpha.405 一模一樣（client 摘要、`created_at` 差的徽章、沒有 facts）；對它送新 turn 變成 mixed execution，舊的不動、新的吃 N2、總數無重複；live turn 的 spinner → `1.0s` → `4.0s` → `6.9s`（＝ daemon `duration_ms` 6862）；還意外拿到一個真實 denied——sandbox 拒絕讀 cwd 外的檔案，新呼叫劃刪除線＋ `denied`、result 中性色，而兩格上方舊的 raw-only 拒絕仍是紅的，R3 兩條路並排。26／24 ms 顯示成 `0.0s` 不夠有用 → #1229。

Follow-up：#1226（seq guard 對 nexen #83 亂序永久丟事件，既有缺口）、#1227（diff view 顯示預算）、#1228（subagent 工具追蹤）、#1229（毫秒級時長）。成本 hover（console R13）是 raw `result` 幀的 turn 級事實，不屬 N2，另開 phase。vitest 7211、lint、tsc、build 綠。

## [1.0.0-alpha.407] - 2026-09-19

### Feature: exec pane 顯示 N2 事實——摘要、狀態、時長、result facts（#1223，P-B3.2）

alpha.406 把 daemon 的工具事實放進 `tools` map，這支讓它們上畫面。工具呼叫標頭的摘要改成三段決策（`lib/nex/tool-summary.ts`）：daemon 給的 `primary_arg.value` 優先；`known: false`（MCP／自訂工具）走 console R10——列 input 的前三個 key 寫成 `key: value`；其餘才退回 P-B2 的 client 端 `getSummary` 表（原封搬過去，逐位元組比過）。截 80 字留在 renderer，是顯示裁量不是事實。完成徽章有 `duration_ms` 就用它（daemon 觀測值，含 N2 unseen 路徑 `startedAt` 為 0 的情況），沒有才回 `endedAt − startedAt`。`denied` 工具名劃刪除線、扳手圖示、警示色徽章——而且**時長照顯示**（攻擊方抓到我給 subagent 的指示與 spec R2 矛盾，critic 判這是唯一的 spec drift）。result block 的外觀改由 N2 `status` 主導：`denied` 一律中性色＋ `Prohibit` 圖示（claude 對權限拒絕也標 `is_error: true`，照 raw 畫會每次拒絕都紅一片）、`error` 才紅、沒有 N2 才退回 raw `is_error`；標頭多一段 facts（`lib/nex/tool-result-facts.ts`）：Read `4 lines`、Edit／Write `+1 −1`（U+2212，`+0 −0` 也出——Write 建新檔的 `hunks: []` 是正常狀態）、多行 output 的行數、`truncated`／`non-text` 標記。

守門：先對**未動**的 `ToolResultBlock` 建四段 baseline snapshot 再改 component（codex plan review 抓到原本「render 兩次比 innerHTML」是同版本自比，證明不了東西），之後每個 commit 都不帶 `-u` 跑——沒有 entry／沒有 facts 的 DOM 一字未變。`ConversationMessages`／`ToolUseBlock` 查 `tools` 一律 own-key（`Object.hasOwn`），跟 #1221 同一個原型鏈修法，測試用 `Object.create({tu1: entry})` 證明原型鏈上的 entry 被忽略（單用 `constructor` 當 id 驗不到守衛，mutation 實測過）。

Codex：R1 無 finding；攻擊方三條——result tone 沒吃 N2 `error`（critic 引 nexen `toolevents.go:252-261` 有證據反對：`error` 就是從 `is_error` 導出的，wire 上不可能不一致；但改成有 `facts.status` 就以它為準比推論不變量便宜，照修）、denied 丟時長（同意、修）、R10 fallback 對超大 input 先全量 `JSON.stringify` 再截（疑慮；改成有界 `previewValue`：手走值、額度用完立即停、深度 4、`[Circular]`、getter 炸回 `[unserializable]`；re-review 再抓 `Object.keys` 對寬物件仍 O(n)，改 `for…in` 逐鍵停）。vitest 7185、lint、tsc、build 綠。行號 diff view 是 P-B3.3。

## [1.0.0-alpha.406] - 2026-09-19

### Feature: exec pane 開始消費 Nexen N2 `tool_use`／`tool_result`（#1221，P-B3.1）

alpha.405 只是把這兩種 kind 跳過不當訊息；這支 PR 讓 reducer 真的吃它們。做法不是 brief 裡提的「看 `capabilities.tool_events` 決定整顆 execution 走 N2 還是 P-B2」——那個切換沒有正確的粒度：契約 §2 #53 不回填，pre-N2 的 execution 在新 daemon 上收到新 turn 後，同一份 log 裡就同時有 raw-only 的舊 turn 與 raw＋N2 的新 turn，per-execution 選哪邊都有一半錯。改成 **per-tool-call overlay**：P-B2 的 raw 規則 A1–A4 照跑建 entry，N2 事件把 server 事實（`primary_arg`／`known`／`duration_ms`／`output` 摘要事實／`file`／`diff` hunks／`status` 含 `denied`）疊到**同一個 `tool_use_id`** 的 entry 上，N2 有的欄位以 N2 為準、沒有的留 P-B2 值。單一 map、單一 key，不可能算兩次；pure reducer 也不必碰 capabilities（型別照樣加了 `tool_events`，只是 pane 不靠它分支）。N2 `output.text` 不存——raw `user` 幀本來就在 `messages` 裡，存第二份是浪費。

真機量到的事實進了 spec §3：N2 事件的 `created_at` 與 raw 幀完全相等，`duration_ms` 與 raw 幀 `created_at` 差在 1 ms 內（26／24／752 vs 753）。那顆 execution（Read → Edit → Bash）的 history 頁直接當 golden fixture：全量 replay 得到三個 entry 的完整事實；把 N2 事件濾掉再 replay 得到的 status／時間與全量一致但沒有任何 overlay 欄位——這條等價測試就是 mutation guard，把 N2 規則 stub 掉會炸 4 條斷言。

Codex：plan review 六條（#83 亂序的論述錯了——那是 API goroutine 與泵之間、不會拆開 raw＋derived 同一批，原本的 swap 測試會被 seq guard 吃掉；denied 的 result block 要覆寫 `is_error` 外觀；重複 `tool_use_id` 是 P-B2 既有的共用 entry 行為，寫成 N7；snapshot 守門不能同版本自比；`TimingBadge` 的 `default` 讓 union 擴充悄悄漏掉，改 exhaustive；`Pick` 的 `status` 必填要 `Partial`）全套進 spec v1.1。PR R1 無 finding；攻擊方兩條——`tool_use_id` 為 `constructor`／`__proto__` 會命中原型鏈被當成既有 entry（raw 路徑也有同病，四個函式全改 `Object.hasOwn`）、數值不檢 finite／非負且 hunks 無上限（加 `DIFF_LINES_SANITY_CAP` 10 000，超限在 map 前短路）；critic 同意 A1、A2 列疑慮、無 spec drift。vitest 7109、lint、tsc、build 綠。摘要／狀態／facts 渲染是 P-B3.2，行號 diff 是 P-B3.3；seq guard 對真亂序會永久丟掉低 seq 事件的既有缺口開 follow-up。

## [1.0.0-alpha.405] - 2026-09-19

### Chore: nexen 升 v0.12.0 — N2 `tool_use`／`tool_result` 正規化事件（#1219）

Nexen 側（nexen-0d，PR #82／#87）把工具呼叫正規化成獨立的 durable kind：每個 `assistant` 幀的 `tool_use` block、`user` 幀的 `tool_result` block 各推導一筆，跟 raw 幀同一筆 transaction 落庫、seq 緊跟其後，payload 帶事實（配對、`status ok|error|denied`、`duration_ms`、工具名＋主參數完整值、行數、hunks、子代關係）不帶裁好的顯示字串——跟 Q3 定案唯一的差別是「截字／預覽行數留給 client」。feature-detect 看 `capabilities.tool_events` 存不存在，不比版本號。

purdex 這邊升 pin 的三個面：**wire** 只加不改，exec pane 的 reducer 對這兩種 kind 只推進 seq、不當訊息塞進去（否則會留一堆 `type` 為空的隱形項目；打字機與工具計時暫時仍從 raw 幀算）；**schema** `user_version` 仍是 5，**不用刪 `nex.db`**，升級前的 execution 不回填；**CLI printer** `pdx nex events`／`watch` 多印兩種 row，`host`／`ls` 對線上 daemon 驗過輸出不變。

下一步（要先問使用者）：SPA 改吃正規化事件取代 P-B2 的 client 端推導（三個 client 共用一份），並用 `capabilities.tool_events` 對舊 execution 退回 raw 幀。

## [1.0.0-alpha.404] - 2026-09-19

### Feature: 任何 execution 都能接進終端機 — SPA 端（#1216，#1210 2／2）

exec pane header 那顆按鈕改叫「Take to terminal」，凡是 claude execution、有 session id、狀態在 running／idle／failed／terminated 且未 archive 就出現——不再限定「從 session 交過來的」。有來源 session 的走原本的 `nex-takeback` 回到同一個 session；沒有的走 alpha.403 的 `take-to-terminal`，pane 就地變成新建的終端機、sidebar 多一個 session。session 名照 launcher 規則 `<slug>-N`：slug 取 cwd 命中的 host project（存成 `~/…` 的專案用 `checkHostPath(host, '~')` 跟 daemon 要 home 展開後比對——攻擊方抓到我原本的 suffix 猜法會把 `~/w` 配到 `/x/other/w`；拿不到 home 就不猜），沒命中就用目錄名清成合法字元。撞名 `session_exists` 重試一次。

Hand to nex 的確認框多一個「保留 tmux session」勾選——**每次開啟都預設勾選、不記憶**（使用者定案）。取消就在 execution 起來後把 tmux session 砍掉，pane 不記來源，之後 Take to terminal 走新建那條路；session 還有別的 pane 在用時多一行「另有 N 個窗格使用這個 session」提醒。確認框標題與成功 toast 改成不再假設「回到」終端機；`execution_archived` 的錯誤文案直接寫恢復方法（Host › Nex 取消封存，或手動 `claude --resume <id>`），這是 critic 對「archive 之後 crash」視窗要求的恢復契約。

真機兩輪（見 #1212）：v2 headless／CLI 起的 execution 接回、running 中確認框、`~/` 專案命中 `e2t-1`、`cwd_missing` toast、兩個 pane 同 session 不保留的提醒與 terminated 顯示。第二輪在那個瀏覽器 session 裡右鍵「Hand to nex」沒出現（daemon／provenance／rebuild record 都說 cc，第一輪同一份 gate 是過的）→ #1214 追蹤 gate 的可觀測性。vitest 7039、lint、build 綠。

## [1.0.0-alpha.403] - 2026-09-19

### Feature: 任何 execution 都能接進終端機 — daemon 端（#1215，#1210 1／2）

P-C.3 的「Take back to terminal」只對從 tmux session 用 Hand to nex 交出去的 execution 有效；從 NewTab Headless、`pdx nex delegate` 起的那些沒地方可回。使用者定案「應該要可以」，所以 daemon 多一條 `POST /api/nex/executions/{id}/take-to-terminal`：settle（跑到一半就 lease→interrupt）→ 在 execution 的 cwd **新建一個 tmux session**（名字由 SPA 依 launcher 規則算好送來）→ **先 archive** → 送 `claude --resume <sid>` → 等 CC 起來。可行性量過：Nexen 起 `claude -p` 時 HOME 原樣繼承、只注入 OAuth token 與 keychain scope，transcript 跟互動式落在同一個 `~/.claude/projects/<slug(cwd)>/<sid>.jsonl`。

四輪 codex（plan 10 條、R1 2 條 P1、攻擊方 5 條、critic）幾乎全在同一件事上打轉：**同一份 transcript 絕不能有兩個 writer**。因此：兩條 take-back 路徑共用 execution 級鎖（`exec:<id>`，session-bound 在 session 鎖之後再拿）；archive 在 resume **之前**（archive 失敗就砍剛建的 session 回 `archive_failed`；resume 失敗砍 session＋unarchive）；archived 的 execution 一律 409 `execution_archived`；所有 kill 改走新的 `tmux.KillSessionIfInstance`（跟 send-keys 同一招 `if-shell -F` 條件式，按 `$N` 在建立當下的 generation 下砍，tmux 重啟後絕不按名字砍到別人的）；`CreateSession` 建立前後各取一次 generation，變了就拒絕。archive 之後、resume 之前 daemon crash 會留下「已 archived＋空 tmux session」——這是刻意選的失敗方向（可見、可用 Host › Nex 的 Unarchive 或手動 `claude --resume` 恢復），反過來的順序 crash 會靜默毀 transcript，spec §4.1.1 正式接受。

同一支順便做了使用者要的第二件事：**每次 Hand to nex 都問要不要保留 tmux session**（daemon 端 `nex-handoff` 收 `keep_session`、回 `session_kept`；不保留就在 execution 確認 running 後按 id 砍掉，pane 之後沒有來源 session、Take to terminal 會走新建那條路）。`takeback.go` 抽成 `settleForResume`／`resumeInWindow` 兩個 helper，原本 34 個 take-back 測試逐位元不變。

真機（mlab，兩輪）：headless → Take to terminal、running 中 interrupt＋resume、cwd 消失回 `cwd_missing` 且不 interrupt、名字碰撞 `-N+1`、兩個 client 並發打同一顆 → 一個 200 一個 409 且只 resume 一次、`keep_session:false` session 消失。SPA 端在 alpha.404（#1216）。順帶抓到的既有問題：#1214（Hand to nex 右鍵項目偶爾不出現，gate 缺可觀測性）、#1213（拆檔）。

## [1.0.0-alpha.402] - 2026-09-18

### Refactor: P-D.3b `Session` 拿掉 legacy relay 欄位、fixture 清掃（#1207）— P-D 完結

P-D 最後一支。daemon 從 alpha.396 起就不送 `cc_session_id`／`cc_model`／`has_relay`，SPA 這邊把 `Session` 型別的三個欄位刪掉，31 個測試檔的 fixture 用一條三規則的 `sed -E` 掃完（單行中段／單行開頭／三行式），**零檔手修**，`tsc -p tsconfig.app.json` 一次過。`handoff-gate.ts` 第三順位的 `session.cc_session_id` fallback 是死碼（沒有 daemon 會設）一併拆掉，`PaneLayoutRenderer` 不再為了它訂閱 session row；測試裡最後幾個無理由餵 `'stream'` 的 fixture 改成 `terminal`，刻意餵舊 daemon 值的留著並加「Legacy input」註解。`src/locales` 的 `stream.*` i18n key 是 exec pane 的 `StreamInput` 在用，只是名字 legacy，不動。純機械 PR，不派 codex；vitest 6970、lint、build 綠，`grep cc_session_id|cc_model|has_relay` 在 `spa/src` 歸零（含註解）。

P-D 全程（alpha.395／396／400／402）：Go −13.6K 行（execution、dispatch、stream、relay、bridge、history 六個套件）、SPA Stream 家族 13 檔；互動 CC ↔ headless 只剩 `nex-handoff`／`nex-takeback`。

## [1.0.0-alpha.401] - 2026-09-18

### Feature: 主機的 self alias 可以從 API／CLI／Peers 頁設定（#1196；#1200 #1201 #1202 #1205）

daemon 對外自稱的名字（對方 Peers 頁的 `calls itself "…"`、對方 POST 不給 alias 時採用的 entry 名、`<alias>/<name>` 位址前綴）＝ `PeerAlias()`：`config.toml` 的 `[peers] alias`，沒設就用 `host_id` 冒號前那段。以前只能手改檔案再重啟；現在三個入口都能改，而且因為所有讀取者都在 `CfgMu` 下即時讀，**不用重啟**。spec `docs/specs/2026-09-18-peer-self-alias-spec.md`、plan `docs/plans/2026-09-18-peer-self-alias-plan.md`。

#### daemon（#1200）

- `config.ValidateSelfAlias(alias, hosts)`：與 entry alias 同一套 pattern／保留字規則（抽成共用 `validateAliasShape`），再加「不得（不分大小寫）等於任何已設定 peer host 的 alias」——本機 alias 與 peer alias 共用 `<alias>/<name>` 命名空間（S-1）。
- `PUT /api/peers/settings` 收 `alias`：缺或 `null` ＝不動；`""` ＝清回衍生值，**但衍生值也要過同一關**（plan review F1：某個 peer entry 可能剛好叫 host_id 那段）→ 409；其餘驗證後原樣存（S-2）。pattern 400、碰撞 409（S-6），跟 `deliver` 在同一個 `UpdateConfig` closure，alias 被拒時 deliver 也不寫。body 必須恰好一個 JSON 值（尾隨垃圾 400，codex A-1）。
- 回應多 `alias_source: "config" | "host_id"`（S-3）——這就是舊 daemon 探測：舊版忽略未知 key 回 200 但沒有這欄（S-5）。
- **`handleAddHost` 的 commit closure 補對即時 `cfg.PeerAlias()` 重驗**（plan review F2）：原本 snapshot 在鎖外、中間還有一次 verify 撥號，self alias 的 PUT 插進來就能建出與新 self alias 同名的 entry；用假 peer 在 verify handler 中途改 alias 重現（修前 201 落盤），修後 409。

#### CLI（#1201）

`pdx peers alias`／`pdx peers alias <name>`／`pdx peers alias --clear`。PUT body 用 CLI 自己的 `{alias}` struct（共用型別的 nil `Deliver` 會序列化成 `"deliver":null`）。回應驗收（S-5，F5 收緊）：`alias_source ∈ {config,host_id}` 且 alias 非空；set 要 alias 精確等於送出值且 source 為 `config`；clear 要 `host_id`；不符 → `daemon did not apply the alias (…; daemon too old?)` exit 1。

#### Peers 頁（#1202）

`peers-self` 那行加 inline 編輯器：`(from host_id)` 標記、Edit → 輸入／Save／Cancel、Clear（只在 `config` 時出現，旁邊寫「(default would be “mini-lab”)」），還有一段說明改名會怎樣（對方保留 entry 名、顯示 drift 直到 Rename；位址變 `<alias>/<name>`；ref 不變——S-4）。全部走 D4 的單一寫入 runner（`selfKey`，頁面鎖＋refresh）。GET 已經沒有 `alias_source` 就不給 Edit、直接說 daemon 太舊（F7）；PUT 回應照 CLI 同一套規則核對（codex A-2）。存衍生值本身是有意義的寫入（把它釘進 config），只有 `config` 且值相同才算 no-op。

#### 驗證

- 測試：Go config 49→58、module/peers 526→542、cmd/pdx 461→501；SPA 6980→7032。mutation 三條（碰撞條款、CLI 接受無 `alias_source` 的 200、Save 繞過 runner）全紅。
- codex：plan review 7 條全採納；R1 無 finding；攻擊方 A-1／A-2 修完 critic 同意，critic 另抓 §4.3 default hint drift 已補。
- 真機（記在 #1202）：mlab CLI 負面 409／400；**air26 真改名 `air26x`**：自己的位址立刻變 `air26x/…`，mlab verify 顯示 drift、`pdx peers --all` 仍綠、`pdx msg send air26/_64wca8` 仍送達（entry 名沒變），改回後 drift 消失；頁面把 mlab 從衍生 `mini-lab` 存成 config `mini-lab` → `(config)`、對方無 drift → 頁面負面 `air26`／`..` inline 錯誤 → Clear → 回 `(host_id)`、config.toml `alias = ""`。兩台 daemon 都已在這個 build 上。
- 部署地雷：把新 binary `cp` 到執行中的 Mach-O 同一 inode 上，之後每次 exec 都被 SIGKILL（137）——要先 `rm` 再 `cp`。

## [1.0.0-alpha.400] - 2026-09-18

### Refactor: P-D.3a 拆除 SPA Stream 家族，pane mode 收成 terminal（#1198）— P-D 第 3／3 段主體

daemon 兩段（alpha.395／396）拆完後，SPA 這邊把 Stream 那一代也下線：`lib/stream-ws.ts`（WS client）、`useStreamStore`、`useRelayWsManager`、`ConversationView`（＋snapshot）、`HandoffButton` 13 個檔案刪掉；StatusBar 的 terminal／stream 下拉、tab 右鍵的切換項、`useTabStore.setViewMode`、event WS 的 `handoff`／`relay` 分支、`host-api` 的 `switchMode`／`fetchHistory`／`handoff`／`ConfigData.stream`、Host › Overview 的 stream presets 欄、Sessions 表的 Mode 欄、通知點擊硬寫的 `mode:'stream'` 全部走。exec pane 還在用的 `stream-json` 訊息型別（`ContentBlock`／`AssistantMessage`／`StreamMessage` 等 8 個宣告）先原文搬到 `lib/nex/message-types.ts`——commit message 內附 8/8 `cmp` identical——再動其他東西，每個 commit 都過 `tsc -p tsconfig.app.json`。

`PaneContent.mode` 從 `'terminal' | 'stream'` 收成 `'terminal'`，tab store persist **v2→v3**：`migrateTabStore` 新的 `<3` 步驟走遍每個 split layout 的 leaf，把 tmux-session pane 的非 terminal mode 改掉——codex plan review 抓的：只加 merge hook 對既有 v2 blob 根本不會跑。舊資料一律當 terminal：v1 workspace snapshot 的 `mode:'stream'` restore 現在送 `mode:"terminal"`（真機看到 request body）、device-state 的 stream meta 可以 reattach、`/t/<id>/stream` 舊 deep link 照開並正規化成 `/terminal`。攻擊方抓到一條真的不一致：`reattach`／`revive` 還在拒絕**尚未升級到 P-D.2 的 peer daemon** 回的 live `mode:'stream'`，而 snapshot restore 與直接開啟都接受——同一個遠端 session 依入口不同被判存活或遺失；改成四條路徑同一政策（mode 不參與 binding，code＋tmux_instance 才是）。16 處 always-true 的 `mode === 'terminal'` guard 拿掉、邏輯保留。

一個坑寫進 spec：`spa/tsconfig.json` 是 solution-style（`files: []`），裸的 `npx tsc --noEmit` 是 no-op、永遠 exit 0，要用 `-p tsconfig.app.json` 或 `tsc -b`。真機（worktree 自己起 :5175 對 mlab daemon）：v2 blob reload 成兩個 terminal pane 且 blob 變 v3、右鍵只剩 Split ＋ Hand to nex、舊 deep link、舊 snapshot rebuild、Hand to nex → MANGO → Take back 全過；順帶抓到既有的 `useRouteSync` 整頁載入無限迴圈（main 同樣重現）→ #1203。vitest 6907、lint、build 綠。P-D.3b（`Session` 的 `cc_session_id`／`cc_model`／`has_relay` 與 ~60 個 fixture 檔）另開 PR。

## [1.0.0-alpha.399] - 2026-09-18

### Fix: badge 預覽三態＋跟側欄同底；浮動面板一律開在欄位下方（#1197）

三個使用者回報：

- **預覽顏色跟側欄看起來不一樣**——badge 底色是 22% 透明的 `light`，側欄的列坐在活動列 `surface-tertiary` 面板上，預覽卻坐在設定頁底色上，同一個半透明綠疊在不同底就不同色。預覽列現在包在一塊 `surface-tertiary` 面板裡。
- **預覽少了三態**——原本只有「一般」與「Hover · 作用中」兩列，但真實側欄 hover 的列底（`surface-hover`）跟作用中（`surface-active`）不同。改成一般／Hover／作用中三列，class 取自 `INLINE_TAB_ROW_CLASSES`（新增 `hoverPreview`＝inactive 去掉 `hover:` 前綴，靜態呈現 hover 面）。色塊本身仍照 spec：圖示一般用 middle、hover 與作用中用 main，底色三態一律 light。
- **高的 Main 色盤跑到視窗頂端**——欄位下方放不下就退到「放上方」再被夾到頂。`FloatingPanel` 改成一律開在 anchor 下方，`maxHeight`＝從那裡到視窗底的剩餘空間、內容可捲，最少保留 160px（貼底時只往上滑到剛好夠）。R1 與 re-review 補了拖曳時 maxHeight 要跟著 top 重算、極小視窗不套 160 下限。

6988 tests 全綠，純 SPA。

## [1.0.0-alpha.398] - 2026-09-18

### Feature: Peers 頁面可以 Pair／Unpair／Rotate（Peer Pairing D4，#1184 #1185 #1191）— Phase D 完結

D2（alpha.384）的 Peers 頁只會看、驗證與採用 alias；現在配對的三個寫入動作都能從頁面做，daemon 端是 D1／D3 已在 alpha.391 上的 route，daemon 不動。spec `docs/specs/2026-09-18-peer-pairing-ui-spec.md` §7、§8.4、§9 D4；plan `docs/plans/2026-09-18-peer-pairing-d4-spa-plan.md`。

#### lib 層（#1184 D4a-1、#1185 D4a-2）

- `host-api.ts`：`PeerHostRow` 加 `rotation_pending`／`last_inbound_auth`（<391 的 daemon 沒這兩個 key，wrapper 正規化成 `false`／`''`）；五個 wrapper `addPeerHost`／`deletePeerHost`／`rotatePeerHost`／`commitRotation`／`cancelRotation`。**commit／cancel 不帶 body**（daemon 把空 body 讀成 `force:false`），所以頁面永遠送不出 `force`（D-7）。
- `peer-pairing.ts`：`rotationOffer(row)` — §7.3 規則的純函數：`pending+current → commit`、`pending+prev → cancel`、`pending+'' → none`、不 pending → `null`。
- `peer-pairing-load.ts`：`snapshot.candidates`（可配對的 App host，含對方是否已持有我方 entry 的 repair 判定與 `listError`）；**step 5 post-dial re-read**——原本 list 在 dial 之前，所有 `last_inbound_auth` 都是 dial 前的讀數；現在每個有 pending 的 host 在 `Promise.all(dials)` 之後再 list 一次，`gateStale: {entry, returnEntry}` 分方向記錄讀數是否 post-dial。
- `peer-pairing-actions.ts`（新）：`pairHosts`（§7.1 三步＋repair path）、`rotateDirection`（mint → push，**不 commit**）、`unpairHosts`（兩側同時刪）。Repair path 依 §7.1「then commit on Y」收尾，但走 §7.3 協定：X 再撥一次、重讀 Y 的 row、只在 `current` 才 commit；commit 回傳的 row 若不是 `current`（並發 cancel 讓 commit 變 no-op）不報 `paired`。Step 2 失敗的 undo 先 re-list Y 比對 url／host_id 才刪。token 值只活在流程的區域變數。

#### 頁面（#1191 D4b）

- **Pair with…**：列候選 host；409 變 inline alias 輸入；step 2 失敗會清掉 Y 上半成品；step 3 失敗留 `one-way`＋「Retry return path」。Y 的 entry 已有 pending rotation 時 Pair 停用、候選列直接給 Commit／Cancel（as of the peer's last dial）；Y 讀不到時 Pair 停用。
- **Unpair**：`ConfirmDialog` 點名兩側；404 當已完成；非 App 對端不刪並在對話框說明。
- **Rotate**（每個方向線）：mint → push → 頁面 refresh → 由 post-dial 的 fresh row 決定**只給一顆** Commit／Cancel／都不給，永遠不從「push 成功」的記憶決定；`gateStale` 時不給按鈕。Inbound 線在回程驗證失敗時變「Retry return path」、對方沒 entry 時變「Create return entry」。
- 頁面級單一寫入流程：Rotate／Pair／Unpair／Commit／Cancel／Rename 全走同一個 runner，一個在跑時其他寫入控制項與 Refresh 全部停用；流程由世代擁有，切 host 後舊流程遲到的 report 不會鎖住或畫到新頁（codex 抓的）。
- i18n `peers.*` +44 key（en／zh-TW）。

#### 驗證

- 測試 6870 → 6980；mutation record `docs/plans/2026-09-18-peer-pairing-d4-mutations.md`（15 條，含 §8.4 指名的「dial 前就讀 row」，fixture 在 dial **settle** 才翻旗以釘住並行讀）。
- 真機 §9 D4（記在 #1191）：從頁面 unpair mlab↔air26 → 兩台 `host list` 空 → Pair → 兩邊 entry 與兩把 token 都在、`pdx peers --all` 雙向綠 → 兩個方向各 rotate 一次、Commit → 雙向仍綠、`pdx msg send air26/…` 送達。
- codex：plan review 7 條全採納；D4a R1／攻擊／critic（repair commit、undo TOCTOU、unpair 並行）＋增量 P1（commit no-op）；D4b R1／攻擊（切 host 鎖頁、gate 未進鎖）修完 critic approve。
- follow-up：#1188（daemon 條件式 DELETE、peer-host 請求 timeout）；`allow_bypass`／`deliver` 切換仍是 spec §10。

## [1.0.0-alpha.397] - 2026-09-18

### Fix: 浮動面板壓到 Electron 標題列時拖不動、關不掉（#1193）

alpha.394 的浮動面板在 .app 裡開色盤，面板比視窗高，定位退到最上面、壓在 `TitleBar`（36px、`-webkit-app-region: drag`）上——那個區域的 pointer 事件被 OS 視窗拖曳吃掉，標題列拖不動、× 也點不到（使用者截圖）。

修三件：面板根元素標 `no-drag`；高度上限＝視窗高扣掉頂部保留與 padding、內容可捲（resize 時重算）；定位與拖曳的 clamp 讓 `top` 永遠在標題列下方。codex R1 抓到兩條對的：maxHeight 原本沒扣頂部 36px 會超出底邊 28px；36px 保留只該在 Electron（Web 版沒 TitleBar）——現在依 `isElectron` 決定 inset。6980 tests 全綠，純 SPA。

## [1.0.0-alpha.396] - 2026-09-18

### Refactor: P-D.2 拆除 Stream 模組、relay 與 bridge（#1190）— P-D 第 2／3 段

daemon 端的 Stream 那一代全部下線：`internal/module/stream`、`internal/relay`、`internal/bridge` 三個套件（含 `pdx relay` 子指令與 usage 字串）、legacy `POST /api/sessions/{code}/handoff`、`/ws/cli-bridge/{code}`、`/ws/cli-bridge-sub/{code}`、`POST /api/sessions/{code}/mode`、`GET /api/sessions/{code}/history`（只有 stream 的 `handoff` WS 分支在打，連同 `agent.HistoryProvider`／`cc/history.go`／孤兒套件 `internal/history` 一起走）、config `[stream]` 與它的 PATCH 欄位。互動 CC ↔ headless 的往返從此只有 `nex-handoff`／`nex-takeback`。

`Session` JSON 少了 `cc_session_id`、`cc_model`、`has_relay`（後者從來沒人設成 true）；`mode` 留著但只會是 `terminal`。**建 session 時送 legacy `"stream"` 不會 400，會被正規化成 `terminal`** —— 這是 codex plan review 抓的：舊 workspace snapshot／device-state 的 `SessionMeta.mode` 可能還是 `stream`，restore 會原樣送上來；拒絕就等於把舊快照弄壞。`session_meta` 表的 `cc_session_id`／`cc_model` 欄位不做 migration，舊 DB 留著（有 `DEFAULT ''`），`TestMetaStore_OldSchemaWithExtraColumns` 用舊 schema 建表證明 UPSERT／SELECT 照常。`cmd/pdx` 多了 `TestRemovedRoutesAre404`：七條拆掉的路由在真正的 composed mux 上都是 404，附一條 control request 證明 mux 是活的。

P-D.2 → P-D.3 之間 SPA 還帶著 Stream 家族：真機確認 persisted `mode:'stream'` 的 pane 會渲染成空的 ConversationView、按 Handoff 拿 404 只在 console 報錯、不炸也不擋其他 tab；`config.stream` 用 optional chaining、`PUT /api/config` 帶 `stream` 鍵回 200 被忽略。

三份 codex review 零 material finding（critic 只點名兩處過時註解，已清）。真機（mlab，hash `9f474c0e`）：scratch daemon 開 live `meta.db` 副本＋帶 `[stream]`/`[dispatch]` 的 config 全過；nex 往返前後 session 都是 `terminal` 且無三舊欄位。重啟時 log 裡既有的 `hookTraceSink` shutdown panic → #1189。

## [1.0.0-alpha.395] - 2026-09-18

### Refactor: P-D.1 拆除 M0 execution／dispatch 模組（#1182）— P-D 第 1／3 段

P-C 全 ship、真機過關之後，開始拆 Nexen 之前那一代的「Purdex 自己跑 headless」機制。spec `docs/specs/2026-09-18-pd-teardown-spec.md` v1.1 切三段：P-D.1 daemon M0 → P-D.2 daemon Stream／relay／bridge → P-D.3 SPA Stream 家族；純刪除 PR 不套 800 行規則，改以「被編輯的存活檔案數」與爆炸半徑切。

這一段刪 `internal/module/execution/`（6059 行）與 `internal/module/dispatch/`（4133 行）—— alpha.326 的 Ploom 派工 M0，三台機器 0 筆執行、自 Ploom 整合改走 Nexen 起就是死碼。`main.go` 拔兩行接線，`config.DispatchConfig`／`[dispatch]` 表一起走，新測試 `TestLoadIgnoresUnknownDispatchTable` 守住「既有 config.toml 還帶 `[dispatch]` 表照樣載入」。`GET /api/execution/{id}`、`POST /api/dispatch/reclaim` 從此 404；`~/.config/pdx/execution.db` daemon 不再開，可手動刪。`m0-ploom-handoff.md` 標 deprecated 留作歷史，feature-inventory 頁同步改成已移除。

Commit 順序是「先刪 package、再刪它讀的 config」，每個 commit 都用 git archive 匯出實測 `go build ./...` 綠 —— 這是 codex plan review（11 條全採納）抓到的：原 plan 先動 config 會讓中間 commit 紅。攻擊方提的「升級遺棄進行中 M0 dispatch 要加 preflight gate」不採納：mlab `execution.db` 0 筆、無 `pdx-exec-*` session，critic 判有證據反對。

真機（mlab，hash `d892947f`）：`/api/config` 無 `dispatch`、sessions 照列、`nex-handoff` → follow-up 有輸出 → `nex-takeback` 回互動 CC 一趟全過，證明 nex 路徑不依賴 M0。`go test -race ./...` 除既有 `TestConsumeSignals_*_RearmsAfterTeardown` timing flake（origin/main 乾淨樹同樣會紅）全綠。

## [1.0.0-alpha.394] - 2026-09-18

### Feature: 色盤與圖示選擇器改成可拖曳的浮動視窗（#1179）

Host 設定頁的色盤（alpha.389）與圖示選擇器原本是原地展開，使用者要的是「可拖曳的浮動視窗，點其他地方就關」。

新的共用元件 `FloatingPanel`：portal 到 `document.body`、`position: fixed`，開在觸發按鈕下方並夾在視窗內；標題列是拖曳把手（pointer capture、位移、永遠留一角在畫面上、記住拖過的位置）；Esc、× 或在外面 mousedown 都關。關鍵細節：**觸發按鈕那排不算「外面」**——outside-click 走 mousedown、按鈕切換走 click，不排除的話會先關再開、永遠關不掉。色盤面板標題是層名（Main／Middle／Light），圖示面板標題是「Change host icon」；編輯器本身不再畫邊框底色，chrome 由面板供應。

四輪 review 補了一串邊角：鍵盤開啟後焦點要移進面板、關閉時還回去（portal 會打斷 Tab 順序）；捲動設定頁或縮小視窗時面板要跟著 anchor（沒拖過）或至少夾回可操作範圍（拖過）；IME 組字中的 Esc 不關；兩個面板同時開時只有最上層吃 Esc、底層卸載時不搶焦點、原焦點元素已消失時退回 anchor；圖示選擇器在 inline 模式不再自己聽 Esc（交給容器）。`FloatingPanel` 一個元件同時管定位／拖曳／dismiss／焦點，攻擊方點名 SRP，先記著。

6893 tests 全綠，純 SPA。

## [1.0.0-alpha.393] - 2026-09-18

### Test: 凍結 codex-cli 0.153.4 hook fixtures 與版本測試（#1159 PR 2/2，#1180）— #1159 完成

純測試 PR，daemon 行為不變。`internal/agent/codex/testdata/codex-0.153.4-*` 照 opencode 1.14.23 的版型：`events.json`（14 條，含 2 條 retired）、`manifest.json`、`version.txt`、`source.md`、`payloads/` 10 份真實 payload —— 7 份取自 daemon 自己的 `agent_trace_chains.root_payload_json.raw_event`（spec 原本指的 `agent_trace_steps.payload_json` 對 codex 是空的），3 份（`PermissionRequest`／`PostToolUse`／`Interrupt`）用 scratch 專案層 `.codex/hooks.json` 現場擷取（`PermissionRequest` 在 DB 裡 0 筆，因為近期全跑 bypass；`~/.codex` 完全沒動）。所有 session/turn id、路徑、prompt、tool input 都洗成範例值，測試拒收含真實家目錄的 fixture。

三個測試：`TestCodexEvents_ClassifyAgainstFrozenManifest`（catalog ⇔ events.json 雙向、manifest 計數含 `upstreamEvents`——這條是 R1 抓的 P2）、`TestCodexPayloadFixtures_DeriveStatusContract`（10 份 fixture 全過 `DeriveStatus`，狀態與 events.json 釘的一致）、`TestCodexCheckHooks_ExceedsSupportAgainstPin`（PATH 上放假 `codex --version`：0.153.4 不超、0.160.0 超、0.124.0 不超）。

#### #1159 收尾（alpha.392 + 393）真機驗收（mlab，2026-09-18）

spec §3 五項全過：Host › Hooks 顯示 0.153.4 無警告；Install 後 hooks.json 恰好 10 個 key、SessionEnd/Interrupt timeout 3、`[features] hooks = true` 無 `codex_hooks`、`[hooks.state]` 11 筆 trusted_hash 保留；codex 啟動無 `deprecated`／`clamping`，`/hooks` 只要求核准 PostToolUse 與 Interrupt；`ls` 後 `PdxPostToolUse status=running`，Ctrl-C 後 `PdxInterrupt status=idle`、projection 1 秒內 idle 不靠 probe；`pdx setup --agent codex --remove` 後 hooks 清空、features 不動。

## [1.0.0-alpha.392] - 2026-09-18

### Fix: codex hooks 事件表對齊 codex-cli 0.153.4（#1159 PR 1/2，#1172）

daemon 的 codex hook 整合一直釘在 0.124.0；裝的是 0.153.4 之後每次啟動印兩行警告（`[features].codex_hooks` deprecated、`clamping SessionEnd hook timeout to 3s`），而且 installer 寫的 `Notification`／`StopFailure` 兩個 key **codex 從來沒發過**（三天 mlab log：codex 0 筆 vs cc 512 筆），真正會發的 `PostToolUse` 反而被標成 unsupported 不裝。結果：permission 放行後燈只能靠 screen-change probe 才從 waiting 回 running；Ctrl-C 之後燈要等 probe 才會從 running 回 idle。

#### 事件表（`internal/agent/codex/events.go`）

12 個 upstream 事件全部宣告，10 個可安裝：`SessionStart, UserPromptSubmit, SubagentStart, SubagentStop, Stop, PermissionRequest, SessionEnd, PreToolUse, PostToolUse, Interrupt`。`PostToolUse` → running（permission 放行後的第一個 hook）；`Interrupt`（使用者 Ctrl-C）走 Stop lifecycle → idle，broker proxy ref 照 `turn_id` detach，跟 `PdxStop` 一樣。`PreCompact`／`PostCompact` 宣告為 ignored。`Notification`／`StopFailure` 退役成明確 ignored，但**保留 EmitsStatus 與 DeriveStatus case**（spec §2.1／§2.5：in-flight payload 仍解析、`SupportedStatuses` 不變）。upstream 名單釘死雙向比對（catalog − retired ＝ 文件 12 個）。

#### installer（`hooks.go`）

- install／remove 都會把 retired key 下 pdx 自己的 entry 拔掉、空了就刪 key；第三方 entry 與非陣列值原樣保留。
- per-event timeout 表：`SessionEnd` 與 `Interrupt` 都是 3 秒（**Interrupt 是實測啟動訊息才發現的**，spec 原本只寫 SessionEnd），其餘 5。
- 寫 `[features] hooks = true`、刪掉 deprecated `codex_hooks`。`CheckHooks` 語意（review 時定案並回寫 spec）：canonical `hooks` 存在就它說了算，非 bool（`"true"`、`1`）視為 disabled 不 fallback；canonical 缺席才看 legacy；兩者皆無 ＝ enabled（upstream 預設）。
- 寫入順序改成 config.toml 先、hooks.json 後：config 寫失敗時 hooks.json 一個 byte 都不動（fault-injection 測試）。
- `[hooks.state."<path>:<event>:n:m"] trusted_hash` round-trip 測試補上（原本零覆蓋），重裝不會讓 codex 重問一次 `/hooks` 核准。
- `codexHooksSupportedVersion` → `0.153.4`。

#### module 側（`frame_ops.go`）— review 抓到的

攻擊方抓到一條 high：codex broker（cc 內用 codex-companion 派出去的）沒有自己的 frame，只是 cc parent 上的 proxy ref；`PostToolUse` 是 broker 每次工具呼叫都會發的第一個 LifecycleNone 事件，原本會走通用路徑替 broker **建一個獨立的 running frame**（SessionEnd／sweep 後遲到的 PostToolUse 也會把死 session 復活）。修法：sender 沒有 frame 的 codex PostToolUse 改走 PreToolUse 已在用的 turn-aware proxy 路徑 —— 有 parent 就照 `turn_id` upsert ref、沒 parent 就 skip（`post_tool_without_proxy_parent`）；增量 re-review 再補一條：parent 在 OCC 寫入前消失也 skip（`post_tool_parent_vanished`），不再沿用 UserPromptSubmit 的 recovery fallthrough。catalog lifecycle 維持 None，handler 的 error guard 不受影響。

#### 未做／後續

- PR 2（#1159 收尾）：凍結 0.153.4 fixtures（7 份取自 `agent_trace_chains.root_payload_json`、3 份現場擷取——`PermissionRequest` 在 DB 裡 0 筆，因為近期全跑 `bypassPermissions`）＋ `CheckHooks` 版本測試。
- `ProbeIntentKindScreenChange` 的退役、SPA 不動。
- 已知既有 flake：`TestConsumeSignals_GraceWindowDrop_RearmsAfterTeardown` 在整包高負載下偶爾 2 秒超時（#799 起就有），單獨跑 5/5 過。

## [1.0.0-alpha.391] - 2026-09-18

### Feature: inbound-token rotation，commit／cancel 以「對端最近一次撥入用哪把 token」為閘門（Peer Pairing D3，#1175）

Phase D 的第三段，純 daemon + CLI。配對 entry 多一個 `inbound_token_prev`：`rotate` 鑄一把新的 inbound token、舊的退到 prev，**兩把同時有效**，對端換到新 token 之前不會有任何一秒鎖在外面（D-7）。`MatchInboundToken` 對每個 entry 的 current 與 prev 都比（無 early exit、last match wins），`PeerAuth` 把「用的是哪把」帶上 principal。

#### 閘門靠證據，不靠 App 記憶

daemon 在記憶體裡記每個 alias「對端最近一次認證用的 token」——存的是 token 的 fingerprint（sha256 前 16 hex，不可逆），`last_inbound_auth` 在讀取時對 entry 當下的 current／prev **推導**出 `current` / `prev` / `""`。`rotate/commit`（丟舊的）只在對端最近被看到用**新** token 時放行，`rotate/cancel`（丟新的）只在最近被看到用**舊** token 時放行，`""`（本 epoch 沒撥入）兩者都 409 `rotation unconfirmed`，除非 `force`。刻意不持久化：daemon 重啟後兩把都仍有效、兩個閘門都拒到對端再撥一次為止——fail-closed。record 跟著 entry 走（rename 搬、delete 清）。

#### review 抓到什麼（這段是重點）

plan 過 codex 就抓到一條 critical：spec §6.3 原本說「check 與 write 之間落地的撥入，對端仍握有兩把 token」——錯，對端只存一把。whole-branch review 進一步用臨時測試證明：撥入在 `PeerAuth` 比對**之後**、handler 記錄**之前**，若此時 admin `rotate` 重置了 epoch，那筆記錄會以 `current` 寫進新 epoch → commit 放行 → 鎖死。修法是 fingerprint＋推導（記錄綁 token 身分而不是時刻），原本 cancel 事後把 `prev` 改寫成 `current` 的程式碼整個刪掉。codex 攻擊方再往前推一步：middleware 已比對、handler 尚未記錄的請求對閘門仍不可見——critic 同意，且指出 §6.3 把它歸為 future evidence 是 spec 自打嘴巴。最終做法：`HostMatcher` 在 `CfgMu.RLock` 下完成比對**並同步記錄**（`Core.HostAuthObserver`），閘門跑在 `CfgMu.Lock` 下，所以每一筆先完成的認證閘門都看得到。三個真 middleware 交錯測試釘住。另外：cancel 的記錄改寫曾一度移到落盤後（避免寫檔失敗留下假 `current`）、閘門 body 要求恰好一個 JSON 值、rename／delete 寫檔失敗只會 fail-closed（補測試，不改行為）。

#### #1152 一併關掉

惡意 peer 收到我們的 outbound token（Bearer）後可以原樣回顯。現在 `fetchHostResult`、`send.go`（deliver 回應、resolve candidates、name-mismatch detail、audit、log）、`reply.go`、`verifyHost` 的每一段 peer 文字都先 `redactSecret` 再 `boundRemoteText`，包括 `Peers` 列的每個字串欄位與學到的 host_id；add／put 學 host_id 時若含 token 直接拒絕，不會落進 config。

#### CLI 與驗收

`pdx peers host rotate <alias> [--commit|--cancel] [--force]`，409 時 stderr 直接說「去 peer 上跑 `pdx peers host verify`」；`host list` 多一欄 `ROTATION`（`-` / `pending` / `pending, confirmed`）。真機（mlab ↔ air26，兩台 390+D3）：rotate → 立刻 commit 被拒 → air `set-token` → 兩邊綠 → `pending, confirmed` → commit 200 → `-`；負面：rotate 不推、air 用舊 token 撥入仍綠、`pending`、commit 拒、cancel 200；重啟後 cancel 拒、再撥一次才 200。**mlab→air26 這條現在跑在剛輪替過的 token 上。** mutation record M1–M32（含「`""` 不得當 prev/current」M9/M10）。Go 五個套件 `-race` 全綠。兩台 daemon 已部署。下一步 D4：頁面上的 pair／unpair／rotate。

## [1.0.0-alpha.390] - 2026-09-18

### Feature: 「交給 nex」／「接回 terminal」進 SPA（P-C.3b，#1170）— P-C 完成

exec 模式的最後一塊：互動 CC 與 headless execution 之間可以從畫面上來回。至此 P-C 三段（Headless 啟動、Executions view、handoff）全部落地，Stream／relay 可以進 P-D 拆除。

#### 交給 nex

在跑 Claude Code 的 terminal pane 上**右鍵 → Hand to nex**（pane-local，split 後每個 leaf 都有；不放 StatusBar 因為它只認 primary pane）。顯示條件：host 的 nex 支援 handoff，且這個 session 是 CC——判定順序是即時的 `agentTypes`（若說是別的 agent，舊的 rebuild 記錄也擋不回來）→ `rebuild.agent.type` → legacy `cc_session_id`；daemon 反正會再驗身分。確認框說明「不會有權限提示（handoff profile）」，送出前先把該 host 的 CC resume 範本載入好當回滾指令。成功後那個 pane 直接變成 execution pane，`from` 記住原本的 tmux session。

#### 接回 terminal

從 session 交過來的 execution pane 標題列多一個 **Take back**：running 先確認；send／interrupt／terminate 任一在飛就不給按；送出時把這個分頁的 lease 交給 daemon，並先叫 lease hook `forget()`——否則 pane 換掉時 hook 會去 release 一個 daemon 已經用掉的 lease。成功後 pane 變回 terminal，execution 由 daemon 封存。

#### 三個「pane 不一定還在」的防線

- `trySetPaneContent` 改成 compare-and-swap：daemon 做完事回來時 pane 若已不存在、或內容已被換成別的 session／execution，不覆蓋，改出 toast 帶「開啟 execution」動作。
- execution 的 singleton 查找掃全部 leaf、優先帶 `from` 的那個，route 打開同一個 execution 不會另開一個沒有 Take back 的分頁。
- host 已被刪除時直接拒絕（`hostFetch` 對未知 host 會 fallback 到別台）。

#### 真機驗收抓到的

`↩`「來自 session」marker 從來沒亮過：daemon 在 origin 裡寫的是它自己的 `host_id`（`mini-lab:278cbm`），SPA 拿 client 端的 host entry id 去比。單元測試把錯的命名空間也寫進去了，所以全綠。改用 capabilities 的 `host_id`。這已是 P-C 第三次「測試全綠、真機才抓到」（前兩次：live SSE 無 `created_at`、CC 提示符帶 ANSI）。

codex：R1 兩 P1（take-back 期間要凍結 execution 寫入；resume 範本要等 host config 載入）、攻擊方 host 移除 fallback 與非原子 pane swap，critic 全同意；「handoff.ts 200 行」有證據反對不改。daemon 端「送鍵前再確認 settled」→ #1171。

#### 測試

6455 → 6853；spec §6.4 五項在 mlab 全過（含雙擊只建一個、殺掉 tmux 後 Take back 回 404 且 pane 不動）。純 SPA，daemon 免動。

## [1.0.0-alpha.389] - 2026-09-18

### Feature: 標準款 2D 色盤 + Host 設定頁「外觀」區塊（#1169）

alpha.385 的三條 H／S／L 滑軌使用者用了一下就說不對——「我是要一般的一個大方塊有全域色的那個色盤」。

#### 色盤

編輯面板改成大家熟悉的樣子：一個大方塊（左白→右純色相、往下漸黑）拖標記選飽和度與明度，下面一條色相軸、一條透明度軸、hex 輸入，preset 保留。內部模型從 HSL 換成 HSV（`hexToHsv`／`hsvToHex`，8 個 preset 精確 round-trip），一樣把 HSV 存在本地狀態、只在 prop 的 hex 跟本地模型對不上時重新推導，所以灰／黑／白拖來拖去不會丟色相。方塊支援鍵盤：方向鍵 ±1、Shift ±10。

pointer 處理走了三輪 review：先是多指觸控要記 `pointerId`（任一手指放開不能中斷另一指）；再來是 capture 遺失（`setPointerCapture` 不存在、在視窗外放開）會讓方塊永遠卡在拖曳狀態——加 `lostpointercapture` 處理與 window 層級的 fallback 監聽；最後 fallback 模式下元素與 window 各處理一次移動，改成只走一邊。

無障礙語意在 `role="slider"`（只能報一個值）與一般可聚焦 div（沒有 widget 角色）之間 reviewer 各執一詞，先採可聚焦 div＋`aria-label` 帶目前飽和度／明度出貨，正解（兩個協同的隱藏 slider）→ #1173。

#### 外觀區塊

Color、Icon 與 badge 預覽從 Connection 拆出來，獨立成「Appearance／外觀」小節，放在 Daemon Config 上方；Color 列的 mode 狀態仍由 OverviewSection 持有、兩列共用。

6716 tests 全綠，純 SPA。

## [1.0.0-alpha.388] - 2026-09-18

### Feature: Host 設定頁的 badge 即時預覽（#1165）

alpha.385 的色盤上線後使用者第一個要求：Icon 列右邊給一塊預覽，調色時不用切去看分頁列。

Icon 列右側現在有兩個仿側欄分頁的假列——**一般**與 **Hover · 作用中**——裡面放的是**真的 `HostBadge`**（吃側欄的大小／內縮／圓角／host 色或中性灰設定）、TerminalWindow 圖示與 host 名稱。假列帶跟 `InlineTab` 一樣的 `group` + `data-active`，所以 `index.css` 那條 hover/active 規則在這裡也生效：作用中那列直接顯示 main，一般那列滑鼠移上去也會切成 main。Color 列選的 mode（Console／Terminal／Execution）抬升到 `OverviewSection` 跟 Icon 列共用，切 mode 預覽跟著換；拖任一條滑軌預覽同步動。沒設顏色也沒設圖示時只畫假列不畫色塊，並註明「尚未設定顏色或圖示」。

codex 攻擊方抓到假列的 class 是自己寫的（一般列固定 secondary 底色，沒有 hover 樣式）跟真實 InlineTab 對不上，會讓人依錯誤對比調色——把 `InlineTab` 的 active／inactive class 抽成 `inline-tab-row-classes.ts` 兩邊共用，不會再漂。另把 icon 測試改成 spy `getIconPath` 的實際參數（`Laptop`/`duotone`；非法 icon 回退 `Desktop`）。6707 tests 全綠，純 SPA。

## [1.0.0-alpha.387] - 2026-09-18

### Fix: CC readiness 比對提示符前先去掉 ANSI（#1166）

**daemon 變更，要重啟。** P-C.3a 的 CLI 驗收第一步就撞到：pane 明明停在 `❯`，`nex-handoff` 卻回 504 `cc_exit_timeout {step: interrupt}`。根因是既有 bug：`readiness.go` 用 `capture-pane -e` 抓（活動偵測需要保留顏色），再用 `HasPrefix("❯")` 比對；CC 2.1.276 把提示符渲染成 `\x1b[39m❯ `，前面多了一個顏色重設，所以永遠判 running，`Interrupt` 一路等到逾時。legacy relay handoff 走同一個 checker，早就一起壞了，只是沒人再用它。修法一行：比對前 `probe.StripANSI`。加了三個 readiness case（ANSI 提示符、ANSI 提示符 + 狀態列、ANSI spinner 仍算 running）。

## [1.0.0-alpha.386] - 2026-09-18

### Feature: daemon 端「交給 nex」／「接回 terminal」端點（P-C.3a，#1161）

**daemon 變更，mlab 要 `make build` + 重啟。** 這是 relay 之外第一條「互動 CC ↔ headless execution」的正式往返路徑，SPA 按鈕在下一個 PR（P-C.3b）。

#### `POST /api/sessions/{code}/nex-handoff`

一把 per-session 鎖內把整串做完：generation → 從 agent module 的 `OwnerResolver` 讀 CC 的 session id + cwd（**必須在 `/exit` 之前**，因為 `PdxSessionEnd` 會把 frame 刪掉）→ CC 存活 → 再取一次 generation → 忙就 interrupt、再 `/exit`（沿用既有 CC operator）→ 第三次 generation → 直接呼叫內嵌 Nexen `Service.Delegate`（`resume_session_id`、`handoff` profile、`labels {source: purdex, handoff_session}`、`origin purdex://host/<id>/session/<code>`）。rejected／infra error／逾時 → 用 SPA 傳進來的 resume 範本（`{id}` 佔位）回滾到 window 0。`/exit` 之後 generation 變了**不回滾**——那是另一個 tmux server，往它送鍵不是我們退出的那個 pane——回 `after_exit` 帶 session id 讓人手動 resume。

#### `POST /api/sessions/{code}/nex-takeback`

preflight（session 存在、generation、pane 沒有 CC）**先於任何 execution 操作** → `Store.Get` → **execution 必須綁在這個 session 上**（`handoff_session` label + origin 都對）→ running 就拿 lease（caller 給的或自取，自取的用 `defer` 在每條路徑釋放）→ `Interrupt` → 確認 settled → **送鍵前再查一次 pane 沒有 CC** → 條件送鍵到 window 0 → 等 CC 起來 → archive。成功才 archive；每條失敗路徑都帶 `session_id`。

#### codex 抓到的

R1 兩條 P1：stream 與 nex 各自 `NewHandoffLocks()`——同型別不等於同一把鎖，legacy `/handoff` 與 `/nex-handoff` 對同一 code 可同時進場（改由 session module 在 registry 註冊唯一實例）；take-back 在 lease/interrupt 期間使用者可能已手動起 CC，仍會把 shell 指令灌進 Claude 的輸入框（送鍵前重查）。攻擊方另抓到：`SendKeysIfInstance` 用 `$N:` 是送到 session 的 **active pane**，而 liveness 看的是 `name:0`——active window 不是 0 就注入錯 pane（executor 加 `SendKeysIfInstanceTarget` 帶 window，三處統一）；`context.Background()` 無 deadline，engine 卡住時鎖與 lease 永久佔用（全改 detached + bounded，Delegate 逾時走 rollback）；take-back 沒核對 execution 屬於這個 session——拿 A 的 code 配 B 的 execution 會在 A 的 pane 跑 `claude --resume <B>` 再 archive B（spec 缺口，v1.4 補綁定）。critic 對「provenance 的 cwd 可能過期」有證據反對：spec 明寫 cwd 來自 provenance，不算本 PR 偏離。體質題 → #1163。

#### 其他

nex module 現在依賴 `session` + `agent`（缺 provider 是硬錯誤，engine soft-fail 不變）、engine seam 帶 `Service`/`Store`、import boundary 放行 `nexen/execution` + `nexen/store`；一個 capabilities pin test 用真 handler 釘住 `delegate.resume_session_id` 與 `handoff` profile——之後升 pin 若拿掉，測試會先紅。`go test ./...` 39 packages 綠，nex 套件 +72 測試。

## [1.0.0-alpha.385] - 2026-09-18

### Feature: Host Color Modes P3 — Host 設定頁的三 mode × 三色層色盤（#1160）

Host Color Modes 的最後一段：資料層（alpha.381）與三態渲染（alpha.383）之後，這版把調色的介面放到 Host 設定頁的 Color 列上。

#### 長什麼樣

第一行 `Console / Terminal / Execution` 切 mode；第二行三個色塊 **Main / Middle / Light**，各自顯示 hex＋透明度（繼承時寫 `inherit 60%`，整組繼承 Console 時三塊都標 `Inherits Console` 並變淡，沒顏色時寫 `No color`）；點任一塊就在下面展開該層的編輯面板——跟 Host 圖示選擇器同一種「原地展開」的做法，不是浮動 popover（spec D8 改字：repo 裡根本沒人用 `@floating-ui/react`，使用者要的是滑軌和即時預覽，不是容器）。

面板裡：Main 有 8 個 preset；**色相／飽和度／明度／透明度四條 range 滑軌**，軌道底色是依目前顏色算出來的漸層，拖哪條就預覽哪條的結果；hex 輸入框；Middle／Light 多一個「繼承主色」開關，開著只剩透明度軸。**每次拖動都直接寫 store**，所以側欄與上方分頁的色塊會跟著手指動。

#### 兩個邊界

- 灰／黑／白的 hex 存不住色相（與飽和度），編輯器如果每次都從 prop 重算 HSL，拖 hue 會被下一次 render 拉回去卡死。所以 HSL 存在本地狀態，只在 prop 顏色跟本地 HSL 算出來的 hex 對不上（別處改了顏色）時才重新推導。codex plan review 抓到的。
- 沒 set 的 mode 不能寫 middle／light（P1 的 store 規則）。所以在繼承 Console 的 mode 點任一色塊，會先把 Console 的 main 複製過來再開；完全沒顏色的 host 點任一色塊，先寫 main＝第一個 preset 再開——點下去就是「選了顏色」這個動作。

#### review 抓到什麼

plan review 五條（HSL 卡死、無色 host 開 Middle 全是 no-op、繼承 mode 的 caption 不合 spec、缺 picker→store→badge 的跨元件測試、OverviewSection 既有測試會掛）全進 plan。PR R1 與攻擊方同指一條：編輯器沒 key，兩層解析成同一個 hex 時 React 沿用同一個 instance，上一層的隱藏色相／未送出的 hex 草稿會跨層殘留——改成 `key=mode:layer`。攻擊方另抓「materialise 被 store 拒絕或遠端清掉後，色塊還亮著卻沒面板」；第一版用 derived mask 遮，增量 re-review 再抓到「顏色回來時面板會自己重開」→ 改成 render-phase 直接重設 open 狀態。

新增 `HostColorField.integration.test.tsx`：拖 Middle 透明度 → 側欄分頁色塊的 `--hb-middle` 立刻變；Terminal 設不同 main → agentType 出現時 badge 變紅、清掉時回藍。6699 tests 全綠，純 SPA。

**下一步是真機**：`:5174` 把主 checkout 拉到 main 之後，依 spec §8 看兩台 host 的實際效果，再決定預設 alpha（100／60／22）要不要調。

## [1.0.0-alpha.384] - 2026-09-18

### Feature: Hosts › Peers 頁面 — 兩個方向即時驗證、alias drift 一鍵採用（Peer Pairing D2，#1146 + #1147）

Phase D 的第二段。`/hosts/<id>/peers` 列出這台 daemon 的每個 `[[peers.hosts]]` entry，把它接到「就是那台 peer」的 App host（host_id 優先、URL 只在一側缺 host_id 時當備援），**兩個方向各跑一次 D1 的 verify**（App 是唯一同時握兩把 admin token 的協調者，所以「對方連不連得到我」＝從對方那端跑既有的 outbound verify），狀態五個字擇一：`bidirectional` / `one-way` / `outbound-only`（對端不在 App，永久事實）/ `return-unknown`（對端在 App 但此刻問不到，暫時事實）/ `unpaired`；後兩者畫中性色、永不綠。peer 自報的 alias 跟 entry 名不同就標 drift，一顆 **Rename to `<self_alias>`** → `PUT {alias}` → 重驗，daemon 不知道自己在「採用」。400/409 原句 inline，不重試不加後綴。什麼都不快取：每次 mount／Refresh 都是真的撥號，一顆過期的綠燈正是這頁要拿掉的東西。沒有配對／解除／輪替／toggle（那是 D3/D4）。

#### 三層，各自可測

`host-api.ts` 只多五個 typed wrapper（`HostApiError` 加 `detail`：daemon 的 `{error}` 句子，沒有就 statusText，再沒有就 `HTTP <status>`——HTTP/2 的 statusText 是空字串，舊 daemon 的純文字 404 也是）。`peer-pairing.ts` 是純規則：`matchCounterpart`／`matchReturnEntry`（同一條規則角色對調）、`pairStatus`（spec §5.1 表格逐列）、`aliasDrift`（跟 `pdx peers --all` 同一條）。`peer-pairing-load.ts` 是 §5.2 的編排：X 三個前置呼叫 → 其他 host 的 metadata（或問不到的原因）→ join → 每個 counterpart 只 `list` 一次 → 所有 verify 平行，**join 後 emit 一次、每個 verify 落地再 emit 一次**，每個 snapshot 都是獨立物件，頁面先畫 `checking` 再逐條填。`PeersSection` 只畫 snapshot，generation counter 丟掉過期的 run（換 host、Refresh、StrictMode 雙 mount、換 host 後才回來的 Rename）。

#### review 抓到什麼

plan review 兩條 blocker：一條 mutation 寫法根本不會紅、漏了 HMR-safe count 測試。實作期間 subagent review 抓到 loader 第一次 emit 把之後會被改 rows 的同一個物件交出去（測試只因 `structuredClone` 才過）、loader 讀 `Error.message` 丟掉 daemon 的 detail、元件在 module load 就碰 host-api export 害一個整包 mock 的舊測試整檔不能跑。codex 拆成兩個 PR 各跑 R1 → 攻擊 → critic：D2a 兩條成立——兩台 App host 同 URL 時配對結果跟著 hostOrder 走（改成先取 host_id 已知者、已知互異視為矛盾）、`list(Y)` 失敗一樣繞過 detail；critic 對「已知 host_id 的 entry 不得以 URL 配 unavailable host」有證據反對（spec §5.2 step 2 明定、且那條路徑不對 Y 發任何請求），D-3 補了一句。D2b 一條 medium（row key 只有 alias）修成 `host:alias` 但實測連 prop-change 路徑都到不了；攻擊方一條 high「惡意 peer 把 outbound token 回顯進 error 文字」——鏈成立但那是 peer 自己的 token、CLI 早已印同一字串、SPA 拿不到 token 無從遮罩，裁定遮罩在 daemon `fetchHostResult`，開 #1152 併進 D3。§5.3 wireframe 的 `[Verify]` 按鈕跟 §5.2 自動驗證矛盾，刪掉。

#### 驗收

真機（mlab ↔ air，兩台 alpha.378）：一列 `air`、接到 App 的 Air 2026、兩線綠、`bidirectional`、drift `air26`；按 Rename → 標題 `air26`、`pdx peers host list` 是 `air26`、`pdx peers --all` 零 drift 行、`pdx msg send air26/_64wca8` 送達——**spec §2.1 那個活案例關掉了**。第一次 paint 時 Air 的 runtime 還沒連上，回程先顯示 `could not be asked: unknown`，Refresh 後才綠；「not connected yet」會比 `unknown` 清楚，記進 #1151。mutation record M1–M24（22 全紅、2 條的第二個佐證測試因 fixture 巧合沒紅，主測試都紅）。測試 6648 全綠。純 SPA，daemon 免動。follow-up：#1149（peer-host API 拆出 host-api.ts）、#1150（pre-378 peer 的回程文案）、#1151（rows 等 `list(Y)` 才畫、fetch 無 timeout）、#1152（daemon 遮罩回顯 token）。

## [1.0.0-alpha.383] - 2026-09-18

### Feature: Host Color Modes P2 — badge 三態渲染 + 拿掉每 surface 的濃度設定（#1153）

alpha.381 換完資料層，這版把它畫出來：**作用中／hover 的分頁用 `main`、非作用中用 `middle`、底色一律 `light`**，而且純 shell 分頁（console）與 agent 分頁（terminal）各吃各的一組。

#### 怎麼畫

`HostBadge` 現在收的是解析好的 `{ main, middle, light }` rgba；底色直接畫 `light`，`main` / `middle` 以 CSS custom property 掛在色塊上，圖示顏色讀 `var(--hb-icon, var(--hb-middle))`。`index.css` 一條規則 `.group:hover [data-host-badge], .group[data-active="true"] [data-host-badge] { --hb-icon: var(--hb-main) }` 負責切換——hover 不追 JS 狀態，兩條分頁列（含釘選分支）都帶 `data-active`。`lineColor = neutral` 照舊畫灰圖示但保留底色；沒顏色的 host 照舊只畫圖示不畫底。

`useTabHostBadge` 依主 pane 有沒有**活著的** agentType 決定 mode（terminated 的 agent pane 算 console，跟分頁圖示同一條規則，spec D1 補寫），`useMemo` 包住 resolver 所以無關的 store 寫入不會讓每列重畫。

#### 拿掉的設定

側欄／上方分頁各自的「圖示濃度」「底色濃度」四個欄位整個刪掉——濃度已經跟著每台 host 的顏色層走，兩處都留會打架。store 欄位、clamp、setter、sync 欄位表、設定 UI、locale key 全清；persist `version` 3→4 的 migrate 把舊 key 從 persisted state 裡丟掉，`sanitizeHostBadgePrefs` 在 sync 路徑也會剝掉（`HOST_BADGE_REMOVED_FIELDS`）。剩下的大小／內縮／圓角三個數字框終於有標籤了（之前那排「100 % 22 % 16 px 2 px 4 px」使用者看不懂是什麼）。

#### review 抓到什麼

codex plan review 六條：stale persisted key 靠 shallow merge「忽略」是錯的（要 migrate 才刪得掉）、terminated pane 的 mode 要寫進 spec、三個渲染 task 中間會有編不過的 commit（合成一個）、釘選分支與 CSS 規則沒測試、既有 opacity 測試沒交代處置——全進 plan。PR 攻擊方抓到一條真 bug：hook 的 host 取「前序第一個 tmux pane」、mode 卻取 `getPrimaryPane`，split tab 第一格不是 tmux 時會拿 A pane 的 host 配 B pane 的 mode——改成同一個 pane（`getTabBadgePane`）同時給 host、sessionCode、terminated。結構問題（`useUISettingsStore` 一次要改六條路徑）→ #1154。

6487 tests 全綠。純 SPA。下一步 P3：Host 設定頁的 mode 切換＋Main／Middle／Light 三色塊＋色盤 popover。

## [1.0.0-alpha.382] - 2026-09-18

### Feature: sidebar「Executions」view + 每 host 一條共用的 site-wide SSE（P-C.2，#1148）

exec 模式的「看」入口。sidebar 多一個 view（RegionManager 自己加，不會動到既有配置）：每台 host 的未封存 execution 依來源分組（Local／Purdex／其他原樣）、狀態點、單行 brief、相對時間、從 tmux session 交過來的帶 `↩`；點一列開 execution tab（singleton）。

#### 一條 SSE，兩個消費者

Host → Nex 的表格原本自己開 site-wide SSE、自己管清單；sidebar 再開一條就是每 host 兩條長連線。這版把清單搬進 `useExecutionListStore`：**runtime 所有權**（subscriber token、generation、SSE handle、保留車道、debounce）跟**渲染用 cache** 分開放，refcount 到零關流但留資料（重開即顯示）。失效路徑四條：readiness 翻 false（in-flight fetch 與 debounce 一起作廢）、host 身分變更（`ip:port:token` 變 → 清 cache、重新 `ensure`、ready 後重開一次）、host 移除 cascade、SSE terminal close（立刻放掉車道）。表格改讀這個 store，DOM snapshot 位元組相同；封存切換走表格自己的查詢，用 store 的 `refreshRevision`（每次嘗試都 bump，失敗也算）帶動。

#### 連線預算

瀏覽器每個 origin 6 條連線，P-B 把 execution pane 的 live SSE 上限訂 4、留兩條給 REST。site-wide 這條也吃同一個預算，所以 `subscription-slots` 多了 `reserve(host, 'site-wide')`：保留期間 pane 上限降到 3，LRU 立刻 evict。真機驗收用網路紀錄證明：4 個 pane + sidebar 時每次切 tab 都重開串流（cap 4 不可能發生），site-wide 那條恰好一個 200、Hosts → Nex 頁打開也不增。

#### codex 抓到的

攻擊方三條 high：(1) `openNexSse` 在 host 已移除時會在回傳前**同步**發 `closed`，`open()` 在 callback 跑完後才把已關閉的 handle 寫回、還發 fetch——runtime 永遠以為串流活著、車道也沒保留；修法是先記 generation、回傳後比對，變了就丟 handle。回歸測試用真的 `openNexSse` 走那條路。(2) wire payload 沒驗證，`{items:{}}` 或一列 `labels: null` 就讓整個 view 卸載——加了 API 邊界的 `sanitizeExecutionsPage`（壞列丟掉、渲染欄位強制型別、`archived` 只認真布林）。(3) `refreshRevision` 只在成功時 bump，shared fetch 失敗時封存視圖靜默停在舊資料且錯誤不顯示。critic 對「六種職責要改狀態機」有證據反對（沒有可重現缺陷），沒做；另抓到一條 spec drift（gate 用 info-ready 而 spec 寫 `selectReady`）——plan 當時刻意選的，理由是表格不能比以前晚出現，spec 改成 v1.2 對齊。這是第一個照新流程跑的 PR：R1 → 攻擊 → critic 串行，修完只 re-review 增量。

#### 測試與驗收

6357 → 6455；spec §6.2 七項在 mlab 全過。純 SPA，daemon 免動。

## [1.0.0-alpha.381] - 2026-09-18

### Feature: Host Color Modes P1 — 每台 host 依 mode 的三色資料層 + 移除 ✳ 標題符號（#1144）

alpha.362 的 host badge 用了一陣子之後的結論：一台 host 一個顏色、每列畫法一樣，作用中／非作用中／純 shell 分頁全長一個樣。這是三個 PR 的第一個，**只換資料層，badge 外觀這版不變**。

#### 資料模型

`HostConfig.colors` 依 mode（`console` = 主 pane 沒 agentType、`terminal` = 有 agentType、`execution` = 預留給 P-C 的 execution tab）各一組 `main` / `middle` / `light`，每層帶 alpha。繼承鏈是 `colors[mode] → colors.console → 舊的 color → 沒顏色`；`middle` / `light` 沒設顏色就沿用 `main` 的色相、只帶透明度（預設 100 / 60 / 22，22 就是現在的底色濃度）。舊的 `color` 欄位**只讀不寫**：兩台已設的顏色照舊顯示，任何新的寫入都會把它換成 `colors` 並刪掉舊欄位——alpha 階段照慣例不做 migration。

`resolveHostColorSet` / `resolveHostColors` 是唯一的讀取入口（純函數，輸出 `rgba()`），`useTabHostBadge` 與 Host 設定頁的 Color 列都改走它；`setHostColor` 保留簽名、內部改寫 `colors.console.main`，所以現在的色塊 UI 不用動。sync 的 `sanitizeHostConfig` 同步學會清 `colors`：非物件整個丟、未知 mode 丟、`main` 壞掉整組丟、`middle`/`light` 壞掉只丟那層，全有效時保持引用不重建。

#### ✳ 不見了

Claude Code 把 `✳ <摘要>`（U+2733＋空白）寫進 tmux `pane_title`，Purdex 一直原樣拼進分頁標題。新增 `Strip agent title marker` 設定（預設開、有同步），pattern 表 `AGENT_TITLE_MARKERS` 按 agentType 查——codex 那條等使用者給樣本再補（實測 codex 0.153.4 的 pane_title 只有 cwd basename，沒有動畫字元；他看到的動畫多半是 Purdex 自己的圖示動畫，待確認）。

#### review 抓到什麼

codex plan review 七條全進 plan：store 驗證改嚴格（alpha 必須是有限數字、color 必須已是 `#rrggbb`、任何壞輸入 no-op 不 throw）、legacy 只在 **applied** write 才刪、resolver 對旁路進來的髒 `colors` 補了不 throw 的測試、sync contributor 補了 hostile `colors` payload 測試。PR R1 與攻擊方同指一條：移除本來就不存在的 `middle`/`light` 層仍算 applied write、順手把 legacy 刪了——已修成 no-op。critic 對「有 legacy＋terminal set、沒 console set 時按 No color」判定 spec 要先改字：現在明定那種 host 清 console 就是清 legacy，因為 resolver 讓使用者看到的 console 顏色就是 legacy。既有問題另開 #1143（sync payload 裡 host entry 是 null 會 throw）。

6295 tests 全綠。純 SPA，daemon 免動。下一步 P2：badge 三態渲染（active/hover 用 main、inactive 用 middle）＋設定頁拿掉兩個濃度欄位並補標籤；P3：色盤 popover。

## [1.0.0-alpha.380] - 2026-09-18

### Feature: New Tab「Headless」區塊 + 共用的 nex host store（P-C.1，#1141）

exec 模式的第一個「啟動」入口。New Tab 現在每台 host 多一個「Headless — <host>」區塊：brief（UTF-8 位元組計數，上限來自 capabilities）、目錄（root 下拉＋相對子路徑）、sandbox profile（選項與預設都來自 capabilities，不寫死），送出後那個 pane 直接變成 execution pane。上次用的 root／profile 每台 host 各自記住。

#### 一個 nex truth

之前 SPA 有三處各自問「這台 host 的 nex 能不能用」：`useNexHostData` 打 `/api/info`、`NexEngineStatus` 自抓 capabilities、`lease-ttl.ts` 又抓一次 capabilities 自己 cache 一輩子。New Tab 與之後的 sidebar／session pane 再各加一份就是六份。這版收成 `useNexHostStore`（`nex-host-reducer.ts` 純函數 + `nex-host-effects.ts` 抓取編排 + zustand 殼）：每 host 一份 info + capabilities，TTL 60 秒，in-flight 去重，reconnect 自動 invalidate，host 移除時跟著清。Host → Nex 頁與 lease TTL 全部改讀它，頁面 phase 語意不變（有量：舊 hook 在 refresh 失敗時本來就保留 info，這次用測試釘住而不是改掉）。

#### codex 三份同時抓到同一個信任邊界洞

攻擊方、防守方、體質方各自獨立指出：host 改 ip／port／token 之後，60 秒 TTL 內 cache 照用——UI 會拿舊 daemon 的 roots／profiles 去打新 daemon。原設計 fingerprint 只在 in-flight commit 時比對、沒存在 entry 上、也不含 token。修法：entry 帶 `ip:port:token` fingerprint，`isFresh` 要 TTL **且** fingerprint 相符，watcher 看到身分變更直接清 entry；三個 named test（TTL 內改址要重抓、in-flight 中換 token 丟棄結果、換 token 後 TTL 不重用）。另外 R1 抓到 headless 啟動被 tmux 狀態擋住（headless 只需要 daemon）→ 新 `isHostDaemonLive`。

#### 表單的邊界

子路徑只收相對路徑：拒 `/`、`~`、`..`、`.`、`\`、空段、前後空白；`$HOME` 不展開；不設長度上限。client 只做提示，server 的 `rejected` 才是權威——真機驗收看到的措辭是 `mount "/Users/wake/Workspace/does-not-exist-…" is not under any allowlisted root`（Nexen 用 EvalSymlinks 正規化，不存在的目錄配不到 root）。400 才丟錯誤，`rejected` 是資料，Executions 表會多一列 rejected。

New Tab registry 順帶補了 source 級 teardown（`moduleId` on `NewTabProviderSource`）——sessions source 從來沒被 unregister 過。

#### 測試與驗收

6249 → 6357。spec §6.1 六項在 mlab 全過（worktree :5175 + playwright）。純 SPA，daemon 免動。

## [1.0.0-alpha.379] - 2026-09-18

### Feature: exec pane 打字機 + 工具執行中狀態（P-B2.2，#1132）

alpha.377 把 transient 幀接進 store，這版把它畫出來。exec pane 現在：assistant 文字逐字出現、尾端一個閃爍游標，durable 幀落地時整段取代、不重複；`tool_use` 送出後 wrench 變 spinner、旁邊一個秒數在跳，`tool_result` 回來變成 `8.5s` 這種 duration badge；turn 被 interrupt／daemon 重啟時工具標 `aborted`；觀察者分頁（沒有 lease 的那個）也看得到這一切。

#### 真機驗收抓到一個測試抓不到的 bug

spec §6 在 mlab 用 worktree dev server + playwright 跑：步驟 1／3／4／5 一次過，**步驟 2 的計時 badge 在 live turn 上永遠不出現**，但同一個 execution 從第二個分頁用 REST history 載入就有 `8.8s`。根因：Nexen 的 live SSE `data:` 是**裸 provider payload**，沒有 `{seq, kind, payload, created_at}` wrapper（`api/sse.go:303-315`），`frameToEvent` 拿到 `created_at: 0`，reducer 照 A4 規則不給計時。spec F6 是從 REST 事件形狀推到 SSE 的——「先量再寫」的教訓又一次。修法跟 Nexen console 一樣：hook 對缺 wrapper 的 live durable 幀蓋 client 到達時間，history 保留 server 毫秒，reducer 維持 pure。復驗 elapsed `0.0s → 3.0s → 5.0s`、duration `8.5s`、console 零錯誤。

順帶：工具跑的時候「思考中」點點不該跟 spinner 並存，`showThinking` 加 `!anyRunning`。

#### 四份 codex + fix wave

R1 與攻擊方同時抓到「`tool_use` 剛 start、還沒 `input_json_delta` 時有畫面但不算可見」——點點跟 spinner 並存、而且新出現的那列不觸發捲動；攻擊方另抓到純空白 delta 會渲染一個空泡泡把點點壓掉。修法是**一個** predicate `isPartialBlockVisible`（started tool 算可見、text/thinking 要 `trim()` 非空），渲染與 R3 判斷都用它；`partialVersionOf` 改成結構字串（只嵌長度不嵌內容）。體質方要求 `ToolCallBlock` 的五個鬆散 prop 收成 discriminated `activity` union（不可能狀態不可表示）、游標抽 `StreamCursor`、活動查表抽 `ToolUseBlock`。`ConversationMessages` 的大拆與測試檔拆分開 #1133／#1134，游標掉到段落下一行 #1139。R3 scoped：無 P1/P2。

#### Stream 模式零變動

三個共用 renderer 的 default-prop snapshot 在改動前先鎖（`26d16cc9`），整個 PR 逐 commit 位元組相同；`ConversationView` snapshot 未動。

#### 測試

6096 → 6188；spec §6.1／§6.2 真機紀錄在 spec 檔內。純 SPA，daemon 免動。

## [1.0.0-alpha.378] - 2026-09-18

### Feature: peer pairing D1 — 驗證單一 peer entry、改名、CLI `host verify` / `host rename`（#1131）

Phase D 的 daemon 半邊；D2 的 Peers 頁面（狀態／雙向驗證／drift 一鍵採用）接在它上面。Spec `docs/specs/2026-09-18-peer-pairing-ui-spec.md` §4（D2–D4 未進這版）。

#### 回程驗證＝在對方那端跑既有的 outbound verify

`POST /api/peers/hosts/{alias}/verify`（admin-only、唯讀）直接重用 `fetchHostResult` —— 就是一列 `scope=all` 扣掉 peer rows，所以頁面與 `pdx peers --all` 對同一台的判定不可能不一致。每種探測結果都是 200 帶 `ok`（`no outbound token` 不撥號、transport error、`peer: <bounded>`、`host_id mismatch`…），404 只給 unknown alias，永不寫入（D-6），永不帶 token 值。App 同時握兩把 admin token，所以「B 連不連得到 A」＝從 B 對 A 跑這條——不需要新的 inbound route、不需要新的 token 流。**為什麼不寫更輕的 probe**：更輕的 probe 是對方要新增的 inbound route，混版本時舊 daemon 會被誤報 unreachable；`GET /api/peers` 是 P2 起每台都對 host principal 開的唯一路由，才是對混版本誠實的探針。

`fetchHostResult` 順手補上它缺的一個翻譯：peer 回 `ok:false` 但沒有錯誤文字 → `peer reported ok=false`（`verifyHost` 早就用這個字串），從 verify route 和 `scope=all` 兩邊釘住。

#### 改名是 PUT 的第三個獨立欄位

`PUT /api/peers/hosts/{alias}` 加 `alias`：`ValidateAlias`＋不分大小寫唯一性（排除自己，所以 `air`→`Air` 合法）在撥號前先擋、進 `UpdateConfig` closure 再擋一次，**拒絕一定在任何欄位寫入之前**，alias 是 closure 的最後一個寫入。identity re-check（URL＋InboundToken 對 pre-lock snapshot）現在 rename-only 也走——review 抓到 rename 撞上同名 DELETE+POST 會把新 entry 改名；用 `putHostAfterSnapshot` 測試 seam 逼出這個交錯（rename-only 沒有 fetch 可以阻塞）。體質 review 再抓到快速路徑拿**過期的 `idx`** 判「是不是自己」——中間有人刪了前面的 entry，case-only rename 會誤 409；改成同一把 RLock 內重找。D2 的「採用對方自報 alias」就是這個 PUT，daemon 不知道自己在「採用」——自報值過的是 operator 的門檻（v4 §7.2）。

#### CLI

`pdx peers host verify <alias> [--json]`（ok 出 0、否則 1；drift 那行跟 `--all` 一字不差）、`pdx peers host rename <alias> <new-alias>`。**對舊 daemon**：verify 拿到 404 出 1；rename 會被 376 忽略而回 200 帶舊名——CLI 比對回傳的 alias 與要求的**完全相等**才報成功（codex R1 抓到 `EqualFold` 會放過 case-only rename），否則 `daemon did not apply the rename` 出 1。`--json` 在 host mode 只對 verify 開放。

#### 證據

mutation record `docs/plans/2026-09-18-peer-pairing-d1-mutations.md`：M1–M16 全紅，含誠實的 M9（快速路徑的 `ValidateAlias` 只有 no-dial 測試抓得到）；在 review fix wave 後於 HEAD 全數重跑。codex 兩輪（R1 標準；R2 攻擊零發現／防守／體質）全部處置。

#### 尚待真機驗收（deploy 後）

`pdx peers host verify air` on mlab → `air  ok …`、`self alias: air26`、`alias drift: peer calls itself air26`；停掉 air daemon → `ok:false` transport error、exit 1。

## [1.0.0-alpha.377] - 2026-09-18

### Feature: exec pane 的串流基礎 — partial assembly、工具活動、transient 佇列（P-B2.1，#1129）

**這版看不到任何畫面變化。** 它把 Nexen 送來、P-B 一直丟掉的 transient 幀（`stream_event` 逐字增量、`stream_snapshot`）接進 store，並從 durable 事件推導「哪個工具正在跑、跑了多久」。畫面（打字機游標、工具 spinner + 計時）是下一個 PR（P-B2.2）。

#### 先量再寫

- Claude Code 2.1.275 `-p` 跑一個 6 秒的 Bash，**完全不送 `tool_progress`**（29 幀：19 `stream_event`、5 `system`、2 `assistant`、1 `user`、1 `result`、1 `rate_limit_event`）；Nexen 也沒這個 kind。所以「工具進行中」只能靠 `tool_use` 已到、`tool_result` 未到來推導 —— 跟 Nexen console 一樣。那份 wire 樣本進了 repo（`spa/src/lib/nex/__fixtures__/cc-2.1.275-sleep6.jsonl`，路徑已清），當 golden replay 測試。
- SSE 重連在 P-B 就做完了（backoff、jitter、`Last-Event-ID`、idle timeout）。158 天前記憶裡「WS 永不重連」講的是 relay 時代。

#### Reducer：一個 `finalized` 計數器，不是「刪最低 index」

partial 以 (message.id, block index) 為鍵；durable `assistant` 幀一次一個 block，第 N 幀 finalize 第 N 個 block。spec v1 原本寫「刪掉 partial 裡最低的 index」——codex spec review 抓到反例：帶著舊 `Last-Event-ID` 重連時 snapshot 只有 block 1、replay 補送 assistant block 0，「刪最低」會把 block 1 刪掉、之後它的 delta 全丟。改成 `finalized` 計數器，**種子來自這個 client 已渲染的同 message.id assistant 幀數**，snapshot 的 index 不參與。兩個 mutation check 各自只讓對應的 reconnect case 變紅。

subagent 幀（`parent_tool_use_id` 非 null）在 partial、tool activity、turn 結束、`pendingSend` 四條路上都跳過 —— R1 抓到 subagent 的 `result` 會把主 turn 收掉，R3 抓到它還會把輸入框解鎖。

#### Hook：rAF 合批 + connection generation

claude 一秒送幾十個 delta；每個都寫 store 會讓 `ReactMarkdown` 每個字重跑一次。transient 幀先進佇列、一個 animation frame 沖一次；durable 幀來時先沖佇列再套用，delta 與它的 finalizing `assistant` 順序不會反。codex 抓到第二個 P1：舊連線排定的 flush 會在重連後落在新 snapshot 之後，把文字重複或把已清掉的 partial 復活。佇列綁 connection generation，`connecting`／`reconnecting`／`closed`／unmount 全部清掉，過期的 flush 不寫。

#### 檔案體質

reducer 一度長到 415 行、三個職責混一檔。拆成 `partial.ts`（161）、`tool-activity.ts`（42）、`content-blocks.ts`（11），`event-reducer.ts` 剩 227 行只做 compose；hook 的佇列狀態機抽成 `transient-frame-queue.ts`（89，10 個直接單測）。純搬移用逐宣告位元組比對證明：29/29 宣告、68/68 測試本體相同。

#### 測試

reducer 22 → 72、store 11 → 15、hook 22 → 29、佇列 +10、golden replay 1；整套 6096 綠。四份 codex（R1、攻擊、防守、體質）+ R3 scoped 全部處置完。

## [1.0.0-alpha.376] - 2026-09-18

### Fix: 狀態列的 peer id 在 pane 換了 agent 之後會跟上（#1127）

狀態列顯示 `purdex-4a`，但那個 tmux 座位上坐的已經是 `purdex-bb` —— 舊 agent 退出、新 agent 在**同一個 tmux session** 裡啟動，狀態列停在前一個人，變暗，直到有人按 ↻。

#### 為什麼一直在那裡、為什麼現在才被看到

`usePeerInfo` 的快取鍵是 (host, session code, tmux instance)，守衛比對的是 tmux 世代。這擋得住 tmux **重啟**，擋不住 **CC session 在活著的 tmux session 裡被換掉** —— tmux 層什麼都沒變。60 秒後它把 row 標成 `stale`，但刻意不重抓：註解寫著「the refresh control is the way back」。

v3 時狀態列顯示 `_q34psn4f` 這種雜湊，舊的和新的長得一樣，沒人看得出它 stale。v4 把名字變可讀，staleness 就變得肉眼可見。**一個既有的快取缺陷，被可讀性照出來。**

#### 修法：用一個早就存在的訊號

`writeProvenanceRecord` 在 `SessionStart` 時已經把新 agent 的 `sessionId` 寫進 pane 的 rebuild 記錄。`usePeerInfo` 現在訂閱那個字串，它變成**不同且非空**的值就 refresh 該 host。同一個 agent 的狀態變化不觸發。

selector **刻意不用** hook 的 `tmuxInstance` 參數篩 pane：狀態列傳的是 `session?.tmux_instance ?? ''`，session 還沒 reconcile 時是空字串，靠它篩會讓值從 `''` 跳到實值 → 每次開 tab 一次假 refresh。`setPaneRebuild` 寫入時已經對過 pane binding，所以 host + code + 排除 terminated 就夠。

#### 兩層保險

stale 邊界那個 timer 本來就精準地在 `fetchedAt + 60s` 觸發，只是只 `setNow`。現在對連線中、有 active pane 的 host 順便 refresh —— 每個邊界一次，不是 polling。失敗的 host 保留 `fetchedAt`，effect 的依賴不變就不會反覆打。

Codex review 抓到一個縫：掛載或重連時答案**已經**過界，`remaining <= 0` 直接 return，什麼都不會發生。補上：那個分支對連線中的 host 立刻 refresh。「切 tab 不發 fetch」這條規則因此精確化為「切 tab 不發 fetch **fresh** 資料」，並用一個新 case 釘住。

#### 測試

+13 case，四個 mutation 各自變紅（拿掉 sessionId 訂閱 3 紅／每次寫入都觸發 1 紅／邊界只 `setNow` 2 紅／掛載過界仍 return 2 紅）。既有 case 零調整。

順手修一個測試衛生問題：兩個檔的 `beforeEach` 用 `mockClear()`，它不會清 `mockImplementationOnce`，紅燈階段一個沒被消耗的 once-impl 洩漏到下一個舊 case。改成 `mockReset()`。

只動 `spa/`。
## [1.0.0-alpha.375] - 2026-09-18

### Fix: `pdx msg send` 不再誤標 bypass 為 prompting（#1124）

修正 #1124：當 `pdx msg send` 沒帶 `--mode` 時，daemon 無法得知呼叫者的權限模式（不在環境變數、registry 或可觀測的狀態），所以不再猜測為 "prompting"，改為送 "unknown"。

接收端規則：不同模式才觸發 hold。所以 bypass session 寄訊而被標成 prompting（相同）就直接投遞，應該觸發的 hold 卻被拿掉了。

現在送 "unknown"，接收端會判定為 mismatch → hold for approval ✅

- 新增 `ModeUnknown` 常數到 wire protocol
- `ValidateMode("")` 改為返回 `ModeUnknown`（而非 `ModePrompting`）
- `clampMode` 保留 `ModeUnknown` 不做 clamp
- 補 mutation test 確保改動不被誤改回去

## [1.0.0-alpha.374] - 2026-09-17

### Feat: 本機投遞 —— 一個入口，一組 ref（#1119）

`pdx msg send` 現在送得到本機 peer。`local_target` 刪除，所以 `pdx peers` 印出來的每一個地址都是 `pdx msg send` 收得下的。

之前必須先知道對方在哪台，才能決定用哪個工具，而兩個工具對同一個 session 的稱呼還不一樣：

```
pdx peers --all   →  mini-lab/purdex-dd [h0h3ln]
ListAgents        →  purdex-dd [ba68ab]
```

兩個都對，它們是不同的命名空間。**選入口就等於選命名空間**，而且兩者不可能合併 —— native 的 ref 是**每個觀察者本機**的編號（同一個對話在 mlab 是 `d8dc4a`、在 air 是 `ad1737`），也無法在行程外導出（11 種輸入 × 9 種雜湊 × 20 個位移全不中）。

這也是 **#1118 被取代**的原因：它提的是「把兩個命名空間對齊」，但只要入口收斂成一個，就沒有第二個命名空間需要對齊。

#### 本機路徑比遠端**短**，不是長

```
remote:  resolve → HTTP → peer daemon → acquire helper → write
local:   resolve → write
```

helper 存在的理由，是讓收件端（它只會回覆 `uds:` 地址）在寄件人不同機時有個本機的東西可以回。本機寄件人的 socket 就在同一個檔案系統裡，所以 frame 直接帶它自己的 inbox，收件端原路回去。

#### 兩個做這件事時才浮出來的東西

**送給自己是可達的，而且真的會投遞。** 實作前的測試回的是 `200 delivered`，收件人的 session id 等於寄件人。它一直碰不到只是因為 `local_target` 擋在前面。現在拒絕 `self_target`（400）—— 那個 frame 的回覆地址會是收件端自己的 socket，而 native 回覆不經過 pdx，`HopChain` 與 pair limit 都不在那條路上，沒有任何既有機制會擋住自我迴圈。

**ambiguity 的那個缺陷，有一個看不見它的測試。** 唯一蓋到撞名的 e2e 用的是 `twin-1`/`twin-2` —— 同一個對話的**兩個行程**，名字不同、地址就不同，v4 之下本來就分得開。它蓋到的正是**不需要 ref** 的案例。本次補上需要的那個，並且讓論證變成可執行的證據：在 mutation M5 之下新測試變紅，**twin 測試維持綠**。

v4 §9.7 就是會抓到這件事的驗收項，它被跳過了。§6.2 是那條驗收改寫成的自動化測試。

#### 決定

| # | 決定 |
|---|---|
| L1 | 一個入口。`pdx msg send` 接受本機目標，`local_target` 刪除 |
| L2 | pdx 自己解析本機目標，所以 ref 到處都是 pdx 的 |
| L3 | 不加 local/remote 欄位 —— 目標在哪台是 daemon 的內部判斷 |
| L4 | 本機投遞不用 helper，frame 的 `from` 是寄件人自己的 socket |
| L5 | ambiguity 拒絕帶上每個候選的 ref |
| L6 | 原生 `SendMessage` 照常運作，只是不再是到達本機 peer 的必要工具 |
| L7 | 送給自己會被拒絕（`self_target`） |
| L8 | 本機路徑**不跑 dedup** —— 它的 id 由同一個 handler 每次現鑄，不可能重複 |

**policy 逐項決定，而不是「照常適用」。** audit-in、dedup、host limit、pair limit、mode clamp 全部住在 `handleDeliver`，本機送訊不經過它，所以每一項都得決定：pair limit **留**（它保護的是收件端 session 不被灌爆，跟訊息從哪來無關）、host limit **不留**（那是管外部主機的配額）、audit **一筆** `DirOut`（一個 daemon 只做了一次觀察，兩筆不是佐證是重複記帳）、mode 照 declared。

本機的 bypass 由 admin 路由加上 live origin 歸屬授權，**不是**由 `AllowBypass` —— 那是不同的授權基礎，明寫出來而不是讓它讀起來像疏漏。

### Fix: ambiguity 拒絕現在指得出是哪一個（#1119）

`AmbiguousCandidate` 沒有 `Ref`。v4 之下兩個撞名的對話產生**完全相同**的 `Address` 和 `AgentName`，所以那個拒絕列出兩列、而操作者一列都定址不到 —— `pid` 和 `cwd` 都不是地址形式。

v4 spec §3.1 白紙黑字寫了「會連同兩個候選**和它們的 ref** 一起告知」。實作從來沒帶，七輪 review 全漏。

`displayAddress` 的括號規則有**兩個**條件而非一個（ref 為空，或地址本身已經以 ref 結尾），兩個渲染點現在共用同一個 `addressWithRef`，而不是各自複述規則。

### 已知且已接受

**`origin_inbox` 是呼叫端送進來的參數，daemon 無從驗證它屬於呼叫端。** 在 L4 之下它成為 frame 的回覆地址，所以持有本機 admin token 的人可以讓一個本機 session 看起來是另一個，而受害者的回覆會流向被冒充者、pdx 看不到。

這不只是「同 user 行程本來就無所不能」的重述：那個論據對**本機行程**成立（`/tmp/cc-socks` 是 `0700`、socket `0600`），但 daemon 綁的是 tailnet 位址而非 loopback，**admin token 的呼叫端不必是同 user 行程** —— 對它來說 `0600` 是真正的屏障，而這條路徑繞過了它。

明知而接受，spec §4.6 記錄，#1120 追蹤選項。

### 其他

- 本機寫入失敗現在把 `socket_write_failed` 記進 audit 的 result 欄，跟 `/deliver` 一致（先前留空，`pdx msg log` 上分不出來）
- `self_target` 的比對加上 `isLocal` 把關 —— 先前對遠端目標也生效，而那一行上方的註解正**斷言著它不會發生**
- `handleSend` 452 → 373 行，本機投遞移到 `send_local.go`；完整拆分見 #1121
- follow-up：#1120（冒充/回覆地址）、#1121（`handleSend` 拆分）、#1122（測試 fixture 組織）


## [1.0.0-alpha.373] - 2026-09-17

### Feat: Peer Address v4 —— 可讀的地址，背後仍是精確的那個（#1107）

```
before   mini-lab/_q34psn4f:aigora2-purdex-b0
after    mlab/purdex-b0 [q34psn]
```

形狀直接比照 Claude Code 自己的 `ListAgents` / `SendMessage`：**名字就是地址**，方括號裡的 ref 只在名字不夠用時才需要打。前面多一段主機，因為 pdx 跨機器而 Claude Code 不跨。

#### v3 為什麼變得不可讀，以及那個理由站不站得住

v3 拿掉可讀段的技術理由是「Claude Code 的 registry `name` 是 `derived`，會變」。把 mini-lab 上每一個 live registry 檔全量量過：

| 觀察 | 結果 |
|---|---|
| 名字從啟動到現在沒變過 | **14 / 14** |
| `nameSince` ≠ `startedAt` | 3 筆，差距都是 **1 毫秒**（寫入時序，不是改名） |
| 14 個名字互不相同 | **14** |

這個取樣能支撐的是「**實務上穩定**」，不是「不可能變」，spec §2 就是照這個範圍寫的 —— resume、compact、使用者改名，以及**這個 repo 自己的 `RewriteRegistryName`**，四條路徑都沒被涵蓋。所以名字被定義成**方便用的別名**，任何必須撐過改名的東西都走 ref。

#### 三層

**A · 地址**　名字當地址頭，由新的 `RoutableName` 文法把關；ref 從 8 碼縮到 6 碼 base36；`Suffix` 刪除、`TmuxName` 補上；`Resolve` 多一層名字 tier、一個共用的 `resolveRefHead`，以及一個拒絕。

**B · `label` → `title`**　自由文字、上限 64 bytes、沒有保留字、永不參與路由；撞名正規化後警告並成功。

**C · 主機自報 alias**　地址在兩台機器上指的是同一個字串。配對時採用對方自報的名字，drift 只在 `pdx peers --all` 顯示，**絕不跟隨** —— 悄悄採用對方的改名，會把每一個寫下來的地址從人腳下抽走。

#### 這輪最值得記的：三個「綠燈但什麼都沒驗證」的測試

每一個都是綠的，而且都會**繼續綠著**穿過這次改動：

- **一個控制字元消毒測試**用 `Agent.PeerName` 當 payload —— 那是 `NAME` 欄，v4 已經不渲染它了。payload 移到 `TmuxName` 和 `Title`，也就是 v4 真正會被別人的資料餵進來的那兩格。
- **狀態列的 `PEER_ROW` fixture** 是 v3 形狀的地址，按 v4 規則「名字段」會變成整團 `ai-chat4:ai-chat4-…` 後綴 —— 顯示是壞的，斷言也斷言那個壞結果。
- **一段宣稱「title 讀不到會讓地址變成雜湊預設值」的文案**被一個**以那句假話命名**的測試釘住。v4 下它根本不成立：地址是 registry 名字或 sessionId 導出的 ref，兩者都不經過 title store。一個釘住假陳述的測試，會保護那個 bug 不被任何一次 sweep 修掉。（關閉 #1094）

測試會用兩種方式失效：**停止驗證任何東西**，或**開始守護錯誤的東西**。這三個各佔一種。

#### Mutation testing 從加分項變成交付項

Task A4 照著計畫把測試全寫完、全綠，然後自己去戳那條它存在的理由 —— **把 stale-version 閘門移到 tier 1 底下，整包還是綠的**。原因很具體：那些 fixture 都沒帶 registry 名字，所以本來就會 miss 掉 tier 1，閘門在上在下都一樣。

從那之後每個 task 都必須弄壞自己的規則、證明有東西變紅。好幾次證明了**該斷言的不是狀態碼**：

- 刪掉 alias 撞名的 409，狀態碼仍是 409（`UpdateConfig` 的二次檢查也回 409），只有回應內容會變
- 跳過 `sanitizeLearnedAlias`，狀態碼仍是 400；真正的損失是對方（攻擊者可控）的位元組被引號原樣回吐進本機終端機

#### Review 抓到的七件事，其中兩件會複合

`RoutableName` 存在的原因本身就是一次 review：spec 初稿宣稱 registry name 不可能以 `_` 開頭，所以名字和 ref 的命名空間天生互斥。**沒有任何東西在保證那件事** —— `registry.go` 原樣賦值，而這個 repo 自己的 `ccuds/registry_write.go` 可以寫進任意字串。互斥現在是**造出來的**，不是假設的。

PR 階段的七個發現裡，F1（combined form 繞過 `RoutableName`）與 F2（`normalizeRemoteRows` 原樣信任遠端給的地址主體）**會複合**：F1 是路由的洞，F2 是把誘餌放到操作者眼前。已配對的主機是攻擊者可控的，它可以自報 `address: "air/trusted:ops"` 配上對應的 `peer_name`，本機就會印出 `air/trusted:ops [abc123]` —— 一列看起來和其他列毫無差別的地址。單獨修任一個都不夠。

現在遠端列的地址是**用本機自己驗證過的欄位重算出來的**，而且只讀 `Agent` / `Ref` / `SessionName`，**完全不讀 `rec.Address`** —— 「地址是重算的」因此是一個 grep 就能驗證的宣稱，而不是一段需要推理的論證。

#### 兩個我開的處方是錯的

把 name tier gate 在 `rest == ""` 沒有用：`SplitSession("purdex-b0:")` 回傳的 rest 就是空字串，尾端冒號會溜過去 —— 而且 tiers 2/3 有一模一樣的洞，`_q34psn:` 一直解得開，沒有任何測試蓋到。規則是「**不得含冒號**」，這才是「v4 沒有 suffix 形式」的實際意思。發現的方式是：先寫的測試包含了尾端冒號那個案例，套上處方之後**還是紅的**。

用「檢查遠端的 session 部分是否恰好是 `tmux:<name>`」來辨認 tmux 形式 —— 那仍然是在讀我們正要拒絕信任的那個欄位。

#### 行為變更

- 遠端的 **proxy 列現在顯示空地址**，原本是 `<alias>/cc:<對方自取的名字>`。那個形式本來就設計成不可解析，所以它唯一的作用是把攻擊者可選的文字放進 `ADDRESS` 欄。本機的 proxy 列不變。
- 一個 registry 名字剛好叫 `cc` 或 `tmux` 的對話，現在拿得到也解得開 `<host>/cc`。先前 daemon 會鑄出並印出這個地址，然後用「cc: addresses were removed」拒絕它 —— **自己印出來、自己不認**。

#### 未做

**spec §9 的九條真機驗收**需要兩台部署好的 daemon。§9.9（把一台上產生的地址貼到另一台）是卡 release 的那一條。

Follow-up：#1113 #1114 #1115 #1116，另在 #1092 #1093 補了發現。
## [1.0.0-alpha.372] - 2026-09-17

### Feat: daemon 的 tmux 呼叫不再依賴「它是怎麼被啟動的」（#1110）

daemon 用**裸名稱** exec `tmux`，共約 35 處，沒有一處呼叫 `LookPath`；同時它會繼承啟動它那個 pane 的 `$TMUX`，而 `tmux.c` 的 `main` 在沒帶 `-L`/`-S` 時**拿 `$TMUX` 當 socket 路徑**。

兩個雷今天都不會發作 —— 兩台機器的 PATH 都完整，mlab 繼承的 `$TMUX` 又剛好等於預設 socket。**那是運氣，不是設計**，而 LaunchAgent 化的 daemon 會把第一份運氣花掉：它拿到的 PATH 是 `/usr/bin:/bin:/usr/sbin:/sbin`，而 macOS 沒有 `/usr/bin/tmux`。

新增 `internal/tmuxenv`，在 daemon 啟動時修正**行程環境**一次。因為 `exec.Command` 繼承環境，那 35 個呼叫點**一個都不用改** —— 而 `cmd/pdx/hook.go`（在 pane 裡跑，`:127` 真的讀 `TMUX_PANE`）因為是另一個行程而天然不受影響。這正是把修正放在環境層而非 `RealExecutor` 的理由。完全比照既有的 `internal/locale`：同一類問題、同一個掛載點。

#### 附加，不是前置 —— 這個方向是 review 換來的

原本的實作是把探測到的目錄**前置**到 PATH。對抗性 review 指出：修好的 PATH 會被 tmux server 繼承、再被 pane 繼承，而 pane 收到的是**裸 `pdx relay ...`**（`module/execution/launcher.go:166`、`module/stream/orchestrator.go:120`）。贏得優先順序就可能讓探測目錄裡某顆無關的 `pdx` 蓋掉 daemon 自己正在跑的那顆 —— 在 LaunchAgent 情境下這不是假設：daemon 可能是 `~/.config/pdx/bin/pdx`，而 `/opt/homebrew/bin` 放著別的東西。**那種失敗會表現成 agent 逾時，離 tmux 十萬八千里**，正好是這個套件存在的目的所要避免的診斷。

改成附加什麼都沒失去：這段程式只有在 `LookPath("tmux")` **已經失敗**時才會走到，所以今天解析得到的指令一個都不會變順位，而 tmux —— 哪裡都解析不到 —— 從 PATH 的哪一端找都找得到。測試現在斷言的是**位置**而不只是成員，所以哪天有人好意改回前置，紅的會是測試而不是幾週後的某個 agent。

#### 掛載點是雙重承重的

1. 必須早於第一次 tmux exec —— 那是經由 module init 與 `/api/info` 的 `GetTmuxInstance` 抵達的（`core/info_handler.go:69`、`session/module.go:62`），**不是**經由 `config.Load`
2. 必須早於 nex 的 PATH policy —— nex 在 Init 快照 PATH、soft-fail 時還原該快照（`module/nex/module.go:131/171`）。先跑代表快照裡已含本次修正，nex 失敗不會把它洗掉

#### 行為變更（明講）

從 `tmux -L other` 的 pane 啟動的 daemon，以前管理的是**那顆** server，之後管理預設那顆。attach relay 也一樣。這是把一個沒寫下來的意外變成寫下來的規則。

找不到 tmux **不擋啟動**，但會印一行明確的錯誤 —— 比照 local daemon install spec 既有的「warning 但不阻擋」先例。alpha.364 的 PATH gate 守的是 `pdx` 自己，缺了會毀掉整個 agent/hook/CLI 生態，性質不同。

#### 驗證方式

不只單元測試。帶著假的 `$TMUX=/private/tmp/tmux-501/pretend,999,1` 起一顆臨時 daemon，log 印出丟棄那一行之後，**tmux hook 安裝成功** —— 若繼承的 `$TMUX` 還活著，tmux 會去找 `pretend` 那顆 socket，安裝必然失敗。

三輪 codex review 收斂（spec 6 個必須 → 標準 1 個 → 對抗 1 個 → 再驗無 finding）。其中兩個是**我 spec 與程式碼註解都寫錯的事實**：`config.Load` 並不觸發 `GetTmuxInstance`；「purdex 不讀 TMUX/TMUX_PANE」是錯的。

#### 留下的三個追蹤

- **#1108** — tmux 一顆都沒跑時 UI 擋住建立 session。那個 gate 是**刻意**加的（當初真的踩過建立失敗且錯誤無法分辨），根因未查明，刻意延後
- **#1111** — pane 裡的 relay 指令用裸 `pdx`，可能跑到與 daemon 不同的 binary。既有問題，本次只是降低風險
- **#1109** — 第二顆 daemon 關閉時會移除第一顆的 tmux global hook（驗證過程中意外踩到）


## [1.0.0-alpha.371] - 2026-09-17

### Chore: `pdx nex host` 終於說得出憑證是哪來的（#1105，nexen v0.11.2）

`cmd/pdx/nex.go` 直接 import nexen 自己的 CLI printer（`lab.protype.tw/wake/nexen/cmd/nex/client`），所以 `pdx nex host` 印什麼是由 nexen 的 pin 決定的。v0.11.0 在 `GET /v1/host` 加的三個欄位一直在 wire 上，但從來沒進到終端機。

最要緊的是 **`credential_warning`** —— 它非空的時機正是「兩個憑證 backend 拿著**不同帳號**、nexen 的挑選是任意的」。Engine 卡片從 alpha.368 起會顯示它（#1098），但看 CLI 的人完全沒有管道知道。R9-5 的整句主張是「CLI 靜默選一個，nexen 要說出來」—— 而 nexen 的第一層皮自己也沒說出來。

對**沒有重啟也沒有改動**的現有 daemon 跑新 CLI：

```
active_account: wake.gs@gmail.com
account_id: HOST            ← 新
credential_source: file     ← 新
quota_five_hour_pct: 18.0
...
```

三個欄位在 wire 上都是 `omitempty`，舊 daemon 一個都不送，CLI 不會替它生空行 —— 替一個從沒被告知的事實生出空行，等於 CLI 宣稱知道它不知道的事。

nexen 同版另外拿掉了 CLI 的 `(none — single-account mode)`：那個模式在 v0.11.0（R1-1）之後就不存在，指著一個 daemon 已經沒有的模式，只會叫讀的人去找不在那裡的東西。

**purdex 側零程式碼改動**，只有 `go.mod` / `go.sum` 兩行。v0.11.1 → v0.11.2 在 nexen 側只動 `cmd/nex/client` 與其測試，wire 與 schema 都沒碰（仍是 v5），**部署不用做任何事**。


## [1.0.0-alpha.370] - 2026-09-17

### Test: 測試不再碰得到機器的 keychain（#1102，nexen v0.11.1）

alpha.368 為了讓 turn 通過 admission，在測試自己的 `$HOME` 底下寫了一份假的 Claude Code 登入。當時就標了一個 ⚠️ 並開了 #1099：那份假 token 只有在「檔案是唯一可用 backend」時才留在本機。

**這個洞比當時寫的大。** `hostcred.Reader` 的**檔案**路徑跟著 home 走，但 **keychain 用的是固定的 service 名字**：

```go
Path:        filepath.Join(home, ".claude", ".credentials.json")  // 跟著 home
Service:     hostcred.DefaultKeychainService                       // 固定字串
KeychainRun: hostcred.RunSecurity                                  // 真的
KeychainDel: hostcred.DeleteKeychainItem                           // 真的
```

轉向 `$HOME` 只隔離了**一個** backend。另一個仍然對著操作者真實的登入 —— 而那正是這個 tailnet 三台裡有兩台（a19／a26）實際存放憑證的地方。

於是「在 temp `$HOME` 寫一份假登入」拿到的不是一個可用憑證，是**兩個**，兩個後果同時成立：兩把 token 都會被送去 `GET /api/oauth/profile` 以便分辨彼此（#1099 只寫到這裡），而且**憑證看門狗握著對真實 keychain 項目的 DELETE 能力**。

mlab 一路沒踩到，只是因為這台的 keychain 是空的 —— 憑證住在檔案裡。那是環境巧合，不是設計保證。

#### #1099 原本提的修法不夠

原議是把 `credential_source` 接進 `[nex]`、fixture 釘死 `file`。nexen 那邊驗過**不足以代替**：`credential_source` 只跳過身分解析的慢路，`Reader.Read` 仍然會起 `security` 子行程，看門狗也仍然帶著 deleter。

nexen v0.11.1 新增的 `Options.DisableHostKeychain` 才是正解，而它存在的理由正是這個形狀：**行程本身的 `$HOME` 已經被轉向**的 embedder，自動的 `HomeDir` 規則看不到 —— 因為 `os.UserHomeDir()` 讀的就是 `$HOME`，所以被轉向的 `$HOME` 在那個行程眼裡就是「真的」home。

#### 改了什麼

`go.mod` → `v0.11.1`。`newMountFixture` 在 **assemble seam** 上設旗標，不是在 `buildOptions` —— production **必須**讀 keychain，a19／a26 的 host 登入就住在那裡，誤關會讓那兩台解析不到帳號。

守衛測試讀的是**真 `Assemble` 實際收到的 `Options`**，不是 fixture 自己設的旗標。所以只刪掉設值那一行、或整個 wrapper 刪掉，測試都會紅；要讓它綠就只能真的把旗標送到邊界另一邊。

**本版不用動 DB**：v0.11.0 → v0.11.1 純新增，只改 `assemble.go` 與其測試，schema 仍是 v5。

#### 一個可以帶走的通則

nexen 那邊修這個洞時，一度 commit 了「宣稱修好但其實沒修到」的版本，發現方式是 mutation 測試沒有變紅。根因值得記：**任何要拿來做安全決策的路徑比對，不能先 `Abs` 或 `Clean`** —— 兩者都用文字消解 `..`，會把你正要檢查的東西先消掉。


## [1.0.0-alpha.369] - 2026-09-17

### Fix: Host Launcher 回報的五個缺陷（#1101）

Host Launcher（alpha.357–360）上線後回報的五個問題，一併修掉。

#### 🔴 啟動的 session 根本沒進專案目錄（daemon）

從路徑是 `~/Workspace/wake/malb` 的專案卡片啟動 `cld-yolo`，pane 的提示字元是 `wake [/Users/wake] ❯` —— 指令跑在家目錄，不是專案裡。

根因在 tmux：`tmux new-session -c '~/foo'` **不展開波浪號，也不報錯**，直接把 session 開在 `$HOME`；`-c` 指向不存在的目錄同樣如此（兩者實測都回 exit 0）。而 host 專案路徑是明確允許 `~/…` 的（`hostconfig/validate.go`），`hostconfig/checkpath.go` 也早就正確展開了 —— 只有建 session 這條路徑沒有。

新的 `internal/module/session/cwd.go`：trim → 空字串維持 `/` → 拒絕 NUL → 以 `os.UserHomeDir` 展開開頭的 `~` / `~/` → 拒絕仍是相對路徑的輸入 → `filepath.Clean` → `os.Stat`。目錄不存在或不是目錄一律回 **400**，不再讓 tmux 默默吞掉。

**這是行為變更**：tab rebuild 與 snapshot restore 碰到已刪除的專案目錄，現在會明確失敗，而不是看似成功卻開在 `$HOME`。codex 逐一檢查過所有 `createSession` 呼叫端，沒有任何一個合理需要不存在的 cwd。

另外 `handleCreate` 改為記錄 **tmux 實際使用的目錄**（`#{session_path}`）到 `SessionMeta.Cwd` 與 create 回應，而非「要求的」路徑，讓三條路徑（create / list / get）的 `Cwd` 全都來自 tmux。兩者真正分歧時打 warning；**不殺 session** —— `session_path` 來自 `getcwd()`，會正規化 symlink 與檔名大小寫（macOS `/tmp` → `/private/tmp`），字串比對必然誤判，為誤判殺掉剛建好的 session 比它要防的罕見 race 嚴重得多。判斷改用 `os.SameFile`，純正規化不出聲。

#### New Tab 一開啟就捲到 session 清單最底

`BrowserNewTabSection` 在 mount 時 focus 網址列。`focus()` 會把所有可捲動祖先捲到目標可見為止，而 New Tab 的欄位是 `overflow-y-auto`，於是每次開分頁都被拖過 session 清單。只在 Electron 下會發生（`disabled: !caps.canBrowserPane`）。自動聚焦整個拿掉。

#### Host 頁開在上次看的那台，不是第一台

`pickHostIdFallback` 的優先序是 `lastSelection.hostId` → `activeHostId` → `hostOrder[0]`。未指名 host 的路由現在一律選 `hostOrder[0]`，完全不記憶。子頁記憶保留，且仍會經 `pickSelectableSubPage` 夾取；明確的 `/hosts/<id>/<sub>` 依然優先。

#### New Tab 的「+」靠右並加上底色

每台 host 的 `+` 從 `ml-1` 移到列尾（`ml-auto`），15% 透明度的 accent tint 換成實心 accent 底色；reconnecting 標籤上移到 host 名稱旁。

#### Launcher 卡片壓成一列

`[{專案} {指令}{指令}{指令}]`，最窄一行兩張。永遠可見的路徑列拿掉，路徑、slug 與指令名稱改由 `HoverTooltip` 在 hover 時顯示 —— tooltip 掛在不會 disabled 的 wrapper span 上，所以 `locked` 狀態的按鈕一樣有提示。鍵盤模型、`data-launch-item` 順序與啟動語意皆未變。

#### 部署

daemon 有變更：mlab 需重新 `make build` + 重啟，air-2026 需更新 App 內建的 daemon，第一項修正才會生效。

## [1.0.0-alpha.368] - 2026-09-17

### Refactor: 拔掉 cswap 殘留，nexen 升到 v0.11.0（#1098）

nexen v0.11.0 把外部切帳工具 **cswap 整個移除**。它本來就壞了——兩個帳號的 usage 都是 null、refresh token 皆 `invalid_grant`、keychain 讀取累積失敗 1279 次——所以沒有損失任何還能用的功能。purdex 這邊的 `cswap_bin` 從此是死碼，Go config、`module/nex` 的映射與 `effective` 輸出、SPA 的表單欄位／型別／en+zh-TW i18n 一併拔除。

`go.mod` 的 `lab.protype.tw/wake/nexen` 從 pseudo-version 升到 `v0.11.0`。兩件事合成一個 commit：只刪欄位不升 pin 的話，舊 pin 的 nexen `Validate` 會去 PATH 找 `cswap`，測試會紅。

#### 🔴 部署必須刪 nex.db

nexen 的 schema version 從 3 跳到 5（`turns` 新增 `account_uuid` / `credential_source`），alpha 期沒有 migration，`store.Open` 直接拒絕舊 DB：

```
pdx stop
rm ~/.config/pdx/nex/nex.db*
pdx start
```

不刪不會讓 daemon 掛掉——`Init` 對 assemble 失敗是 soft-fail，nex 模組改吐 503 並在 Status 顯示 `store: … has schema version 3, this build expects 5`，terminal daemon 照常運作。

#### 順帶補上：憑證來源與歧義警告（adversarial review 抓到）

v0.11.0 讓 host 自己的 Claude Code 登入**就是** turn 所跑的帳號，而那個登入是從**兩個** backend 解析出來的（`~/.claude/.credentials.json` 與 macOS keychain）——CLI 會寫其中一個、刪掉另一個，從不說用了哪個。nexen 相對 CLI 的價值就是它**會說**：`GET /v1/host` 新增 `account_id` / `credential_source` / `credential_warning`，最後那個在兩個 backend 拿著**不同帳號**、挑選是任意的時候才非空。

升 pin 卻不讀這三個欄位，等於使用者可能對著擲硬幣選出來的帳號跑 turn，畫面上一個字都沒有。Engine 卡片現在把 warning 當橫幅顯示在帳號列上方，來源（含 account id）另開一列。三個欄位都是 optional：沒有 host 登入的 daemon、以及所有比 v0.11.0 舊的 daemon 都不會回報，那些情況整組列不渲染而不是渲染成空的。

#### 一個留著沒補的洞（#1099）

`mount_test` 的新 fixture 在測試自己的 `$HOME` 底下寫假憑證讓 turn 通過 admission。但 hostcred 的 resolver 是兩條路：**一個** backend 可用時讀 `~/.claude.json`（~1ms，純本地），**兩個**都可用時把**每一把 token** 都送去 `GET /api/oauth/profile`。所以在 keychain 裡確實有 `Claude Code-credentials` 的機器上，這些測試會帶著假 token 和使用者的真 token 去打 Anthropic。

mlab 現在踩不到（這台的憑證在檔案不在 keychain），但那是環境巧合不是設計保證。真正的修法是把 nexen 的 `credential_source` 旋鈕接進 `[nex]` 讓 fixture 釘死 `file`——那是新的設定介面，不適合長在一個以「拔設定欄位」為題的 PR 裡，開成 #1099。

#### 相容性

既有 `config.toml` 殘留的 `cswap_bin` 不會出事：`toml.Unmarshal` 忽略 struct 沒宣告的 key，下次寫入時自然消失。舊版 SPA 對新 daemon PUT 帶 `cswap_bin` 同理。


## [1.0.0-alpha.367] - 2026-09-17

### Feat: Peer Address v3 —— 一個地址，加上一個只是標籤的標籤（#1091）

一個對話從此只有**一個地址**，由 `sessionId` 導出、終生不變：`<host>/_3k9f2mq4:<suffix>`。`label` 留下來當欄位，但不再是第二種定址方式——它是你**挑**人的依據，不是你**找到**人的方式。

這個切分就是整件事的全部：**唯一必須唯一的東西，正是唯一沒有人能選的東西。**

#### 為什麼

alpha.363（#1079）讓沒命名的 session 用它的 tmux session 名當地址。三個後果，全部是實測出來的：

- **改一次 tmux 名，地址就永久 stale。** `Entry.Tmux` 來自 Claude Code 自己的 registry 檔，啟動時抓一次就固定。在 mini-lab 實測：`tmux rename-session aigora2 aigora2zz` 之後，等到 registry 檔**確實被改寫過**（mtime 前進、`status` 從 `busy` 變 `shell`），那個欄位仍是 `aigora2:@5.%5`。會一路掛到 agent 結束。
- **第三方一開同名 session，兩邊一起失去地址。** 舊的幽靈佔著那個名字，新的來搶，occupancy 規則把**兩個**都跳過。在位者什麼都沒做，地址靜默改變，沒有人被告知。
- **保底雜湊繞過了它自己的撞名規則。** 安全（歧義拒送，不會誤送）但兩敗俱傷。

#### 改了什麼

label 撞名從此**警告但成立**——它不參與路由，所以不需要唯一。序號慣例（`purdex-tester-2`）留著，但改由 CLAUDE.md 要求、不由 daemon 強制：衝突爆在「宣告的當下」讓 agent 自己加序號，而不是事後靜默改掉別人的地址。

`pdx peers` 改成 **LABEL 在最前、ADDRESS 在後**——閱讀順序符合使用方式：掃 LABEL 找到人，複製 ADDRESS 去找他。`pdx msg name` 成功時同時印出 label 與**未改變的**地址，因為剛替自己命名的 agent 最可能誤以為那個名字可以拿來送訊。

#### 四輪 review 沒看到的那一層

`peer_labels.label` 是 `TEXT UNIQUE`，而 `Claim` 的第一件事是 `DELETE FROM peer_labels WHERE label = ? AND session_id <> ?`——**把在位者靜默改成無名**，正是這次要消滅的那個行為。一輪跨模型加三個平行視角（攻擊／spec drift／體質）全部停在應用層。是實作者在「D7 的回歸測試怎麼寫都不會過」時撞出來的。

`CREATE TABLE IF NOT EXISTS` 不會改既有的表，所以 mini-lab 與 air 上那個 `UNIQUE` 還在——沒有針對性遷移的話，**這個版本最主要的行為在真實機器上不會生效**。遷移已包含，並對 `~/.config/pdx/meta.db` 的複本實際驗證過。

#### 混版

升級不是原子的。v2 daemon 不送 `canonical` 欄位，所以：

- 對 v2 遠端解析會回 `remote_too_old` 而**不是**落到 tmux 名 fallback。攻擊方 review 證實了那條 fallback 會把訊息送到同名的 tmux session 去。偵測徵候是「有 live cc 列卻沒有 canonical」——把不變式反過來讀，不依賴任何版本字串。
- CLI 對 v2 daemon 的成功回應會明確報版本不符，而不是靜默解成空身份。舊 daemon 回的裸 `PeerRecord` 會**成功**解成零值，`pdx msg whoami` 會印出空地址並 exit 0。

#### 已知且刻意保留

tmux 改名後 `pdx msg whoami` 與 `pdx peers` 會顯示不同的 suffix。self 路由不建盤點，而昂貴的那條路連保證都給不出（逾時就 fallback 回凍結名，等於把「一定不一致」換成「通常一致」，那是更難寫進文件的契約）。分歧僅止於 suffix：兩個 head 都是 canonical，`Resolve` 在 tier 1 之前就把 suffix 丟掉了。已用測試釘住。

- 17 個實作 commit，Go 全套與 SPA 453 檔 / 5983 測全綠。
- Follow-up：#1092 #1093 #1094 #1095 #1096

## [1.0.0-alpha.366] - 2026-09-17

### Fix: 狀態列的 peer 段落對齊與間距（#1088、#1089）

alpha.365 上線後由真機截圖回報：cwd / agent / peer id 三段比旁邊的 host 與 session name 暗，間距也太寬。**這正是 #1085 自己標記為「跑不到、不粉飾」的那項驗證**——jsdom 沒有 layout 引擎，也不可能抓到顏色繼承。

**#1088 — 顏色繼承**：容器是 `text-text-muted`，host 與 session name 各自指定了 `text-text-secondary`，三個新段落沒有。class 移到 `CopySegment` 的 base，新段落無法再忘記。

**#1089 — 不再用亮度表達「可能過期」**：#1088 把 stale 的 `opacity-50` 放寬到 70%，方向仍是錯的。第二張截圖指出同一件事——**在一行裡，比鄰居暗的段落讀起來是「壞掉了」而不是「暫定」，沒有哪個 opacity 能說出「這可能過期」而不同時說成「這被停用」**。文字因此完全不再變暗（peer id 與 agent 與 host 同色），狀態移到旁邊的 refresh 控制項：轉琥珀色、tooltip 帶原因。它就在它所修飾的值旁邊，而且本身就是修好那個狀態的東西。

分隔間距 `mx-2` → `mx-1`：八個段落各隔 16px 讀起來像一串不相干的項目，不像一行狀態。

兩條新測試釘住新行為：stale 的段落必須與 host 同色且**不含任何 opacity class**、refresh 必須帶 `data-stale`。下次有人想「讓它暗一點表示過期」會直接撞到紅燈。

- 測試 453 檔 / 5983，lint 與 build 全綠。

## [1.0.0-alpha.365] - 2026-09-17

### Feat: peer 資訊進 tab 資訊面板與狀態列（#1085）

狀態列與 tab 雙擊面板顯示 `{host} {cwd} {agent} {peer id} {connected status}`，每段有視覺分隔、各自可點擊複製。**peer id 顯示 label、複製完整地址**——label 本身不可定址，而複製「peer id」的人要的是能貼進 `pdx msg send` 的東西。

**決定設計的那個發現**：`GET /api/peers` 實測 ~2.1 秒（CLI 啟動基準 10ms）。不是第一次慢——`module.go` 設了 `budget: 2 * time.Second`，會在該預算內逐一解析**每個** tmux session 的 owner。那是艦隊盤點端點，不是單一 session 查詢。狀態列若在切分頁時呼叫它，等於每次點擊都在 daemon 上排一個兩秒的 tmux 重活。

**兩個「以為免費」的欄位其實不是**（同一種錯誤：把元件手上已有的值當成功能需要的值）：
- 既有的 agent badge 是 **model 名稱**（`Claude Opus 4`），而 peers API 的 `agent.peer_name` 是 `ai-chat-story-3a`。model badge 不動，peer name 是新的。
- `Session.cwd` 是 `#{session_path}`（session **建立時**的目錄），不跟著 `cd`。真正的 cwd 有自己的便宜端點，snapshot capture 在 alpha.321 就因為同一個原因換過去了。這一段**可複製**，錯的值會被貼進指令。

最終：host 與 status 免費即時、cwd 一次便宜的 tmux 呼叫、agent 名與 peer id 來自貴的那支並快取。

**取用策略集中在 `usePeerInfo` 一個 hook**——刻意如此，那是「切分頁不得觸發 fetch」這條測試寫得出來的唯一原因。不輪詢、切分頁不抓；面板開啟時抓、refresh 控制點擊時抓。`connected` + `tmuxState: undefined` **要**抓（那是 WS 連上後的正常過渡態，當成「不 ok」會導致連上後第一次 render 永遠不抓）。

**過期處理**：default label 是位置地址，tmux 改名會讓地址變而 SPA 收不到通知。超過 60 秒變暗並在 tooltip 帶年齡。**點擊語意不隨過期改變**——永遠是複製，refresh 是獨立控制項；讓過期的段落改成 refresh 是在等使用者誤點。

**Review 三輪**（spec 1 + plan 1 + PR R1 + R2 三視角）。R2 六項，其中兩條互相矛盾（防守方說狀態列失敗時仍顯示舊地址、體質方說面板失敗時藏掉已知資料）——那是同一個不一致的兩面，裁定照 spec §6：**兩邊都不顯示過期地址**，並**否決**體質方那條，因為它會把「複製到過期地址→送錯 agent」放回來。

最嚴重的一條（攻擊方）：peer 與 cwd 只用 `sessionCode` 對應，**tmux 重啟後 code 會被重用**，可能把新 session 的地址顯示並複製到舊 pane 上。已改為納入 tmux generation 比對，且**世代未知不得當成匹配**。

**未驗證、未粉飾的兩項**：400px 不溢出的實測沒跑（jsdom 沒有 layout 引擎，那條測試的名字本身就聲明了它只證明「決策」；真實量測需要已認證的 SPA，而注入 token 會讓它出現在指令裡）；也沒有對 daemon 做 live check。前兩個 PR 各有一個 bug 只有真機跑才抓得到，所以這個缺口值得指名。

延後：#1086（`/api/peers/session/{code}` 單一 session 端點，毫秒級、可讓地址永不過期）。

- 測試：453 檔 / 5981，lint 與 build 全綠。

## [1.0.0-alpha.364] - 2026-09-16

### Feat: `pdx path` —— 讓 CLI 真的可達，不可達就不讓 daemon 啟動（#1081）

App 把 daemon 裝到 `~/.config/pdx/bin/pdx`、用**絕對路徑**啟動它，但安裝流程完全不碰 PATH、也不建 symlink。daemon 跑得起來，**指令不存在**。air-2026 實測就是這個狀態：binary 在、`pdx` 是 command not found、PATH 裡連 `~/.local/bin` 都沒有——於是 `CLAUDE.md` 裡整套要求 agent 執行 `pdx msg name` / `pdx peers --all` 的流程，在每一台由 App 裝 daemon 的機器上都跑不動。agent 照文件做，得到的是 command not found，而且無從得知那份文件是照一台手工設定過的機器寫的。

**新指令（離線修復：不讀 config、不開 store、不發 HTTP）**

| | |
|---|---|
| `pdx path` | 報告：`pdx` 解析到哪、**是不是**這顆 binary、`~/.local/bin` 在不在 PATH 上、該用哪個修法；`--json` 給程式讀 |
| `pdx path link [--force]` | 建 `~/.local/bin/pdx` → **執行這個指令的那顆 binary** |
| `pdx path add-to-shell [--dry-run]` | 偵測 shell、備份、append 有標記的區塊到 rc 檔 |

兩個指令在 Settings → Development 都有按鈕，也附可複製的指令原文——使用者自己決定用哪個。

**Gate**：`start` / `restart` / `ensureRunning` / `install` 最後的 start 在 `pdx` 不可達時拒絕，並同時列出兩個修法。理由是「一台 daemon 在跑、CLI 卻不可達的機器，看起來健康而實際不是」——那個失敗會在幾小時後、在某個 agent 裡、以一句 command not found 的形式浮現。

**幾個刻意的設計決定**

- **完全不做任何 sanitize**：`path link` 連到你執行的那顆 binary（`os.Executable` + `EvalSymlinks`），且**沒有 `--force` 就絕不重指既有的 symlink**——mlab 的那條指向 repo build，是使用者正在用的開發設定。
- **`add-to-shell` 真的寫 rc 檔**，推翻交接時「絕不改 rc 檔」的規則。那條規則保護的是「**安裝流程**背著使用者改設定」，而一個以自己行為命名、不執行就什麼都不做的指令不是那回事。仍然絕對禁止：安裝／啟動／更新／輪詢過程中的任何 rc 寫入（有測試擋著）。
- **rc 檔本身是 symlink 時**（任何 dotfiles 管理的常態）解析後編輯 **target**、保留連結；解析到 `$HOME` 之外就拒絕。早期草案的 temp+rename 會把使用者的 symlink 默默換成普通檔。
- **gate 的判準是 `resolved === null`**，不是 JSON 的 `ok`、也不是 exit code。`pdx path` 在「PATH 上是別顆 pdx」時 exit 1——那對 CLI 自己的問題是失敗，對 gate 不是。用 `ok` 當判準會把所有手工 symlink 指向 repo build 的機器整台擋死。
- **保證只宣稱實作扛得住的**：`--force`「絕不覆蓋實體檔案」只對 pdx-vs-pdx 成立（一把 flock、rename 前重新 `Lstat`），對外部行程明講不防，因為 `rename` 無法條件式執行。
- **`launchEnv()` 會快取 PATH**：不處理的話，使用者照拒絕訊息跑完 `add-to-shell`、開新終端機、回來按 Start，會被一條修好**之前**抓到的 PATH 再拒絕一次。拆成 `cachedLaunchEnv` / `refreshLaunchEnv`，gate 重探、status 用快取（除非上次答案是壞的，那就最多每 5 秒重探一次）。
- **「pdx 可不可達」只有一份實作**，在 Go；gate 用 launch env 去跑 `pdx path --json`，TypeScript 端不自己走 PATH（`deps.fs` 沒有 `lstat`/`readlink`，會分不出 dangling symlink 和不存在）。

**Review**：spec 兩輪、plan 一輪、PR 三輪（R1 一項；R2 攻擊／防守／體質三份平行，兩個 high；R3 收斂 approve）。R2 的兩個 high：gate 把「binary 答不出來」當成 PATH 沒問題（攻擊與防守**獨立指向同一點**），以及 lockfile 用 mtime 猜 staleness（慢的持有者會被偷鎖、舊持有者 release 會刪掉新持有者的鎖）——後者改用 `flock(2)` 把整類問題消掉。

**一個真機跑出來、單元測試抓不到的 bug**：`pdx path` 原本對「PATH 上是別顆 binary」也印「pdx is NOT reachable… command not found」，那句在 mlab 上是**假的**。所有結構化欄位都對，只有散文錯，而沒有任何測試斷言散文。現在有了。

**順帶修正**：`CLAUDE.md` 把 tmux 導出的預設 label 描述成「正規化後」——那是 alpha.363 出貨時就帶著的錯誤（A 的 R2 修正已移除 sanitize）。同時補上 `pdx: command not found` 時該怎麼辦的入口。

**體質**：`cmd/pdx/path.go` 拆成六檔、`electron/local-daemon/cli.ts` 抽出，兩者都以逐宣告位元組比對證明是純搬移。延後項目 #1082 / #1083。

- 測試：Go 2957、Electron 198、SPA 5826，全綠。

## [1.0.0-alpha.363] - 2026-09-16

### Feat: 未命名 session 的預設 peer label 改用 tmux session 名（#1079）

全機 14 個 agent 命名率 0%，所以大家實際打的地址就是 `mini-lab/_d4t5cs`。完整形式 `mini-lab/_d4t5cs:ai-chat4-ai-chat-story-3a` 裡兩個看得懂的識別（tmux 名 `ai-chat4`、CC 名 `ai-chat-story-3a`）全在 **suffix**——那是 resolver 會丟棄、文件還明說「打不打都一樣」的部分。Peer Address v2 的好處要等使用者命名才出現，噪音卻無條件先付。

現在未命名 session 的預設 label ＝ 它所在的 **tmux session 名**：`mini-lab/purdex1:purdex1-purdex-69`。

- **語意**：default label ＝ **位置**（那個 tmux session 裡的那個 live agent），user label ＝ **對話**。這正是 `tmux:<name>` fallback 一直以來的語意，只是改成講明白，而不是再鑄一個不可讀的識別碼。
- **不 sanitize**：tmux 名必須**本身就是合法 label** 才算數。轉換過的名字是**另一個字串**，而那個字串可能正是同主機上另一個真實 session 的名字（`foo.bar` → `foo-bar`），tier 1 會搶在 tier 2 之前回答一個呼叫者從沒指的 session。實測本機 14 個 agent 的 tmux 名全部本來就合格。
- **歧義一律退回 hash**：`_xxxxxx` 從常態變成 fallback。競爭者共五種——同名的另一個候選值、只是「人在那個 session 裡」但自己沒有候選值的對話、把該名字當 user label 持有的 live 對話、**讀不到的** label store、以及會被轉換名影射到的另一個真實 session。任何一種出現，相關各方全部退回 hash。
- **`Resolve` 未動**（含 tier 2），只有 tier 1 會命中的字串集合變了。
- **列表與 whoami 必須逐字元相同**：兩邊都只從「live、非 proxy 的 registry entries」這**一個** population 導出預設值，因為 whoami 讀不到 tmux inventory。`TestSelf_AddressMatchesListing` 是那條絆線，實作途中真的絆到過一次。
- `release` 在寫入前多讀一次 label store（讀失敗 → `store_unavailable` 且不寫）。
- `peer_not_found` 的訊息會教新形式；`pdx peers` 仍以 `*` 標記 default。
- **不相容**：升級後所有未命名 agent 的舊 `_hash` 地址立即失效（不是改名後才失效）。失敗是 loud 的 `peer_not_found` 並指向 `pdx peers --all`，**不會誤投遞**。已否決「保留 legacy hash tier」的緩解方案——那會讓每列同時有兩個地址，並保住正要淘汰的那個識別碼。
- **Review**：spec 一輪、plan 一輪、PR 四輪（R1 標準；R2 攻擊／防守／體質三份平行；R3 收斂）。R2 三份各回報一個 high，**全部是同一個 bug 的三種化身**：§2.2 那條不變量的「不指向其他任何東西」被少數了三種東西。R3 無 finding。
- **延後**：拆 `internal/peers/label.go`、`applyLabel`／`EntryRecord` 改參數物件、精簡新測試裡複述 spec 的註解。
- 測試 Go 全綠；SPA 未動（`rg "api/peers" spa/src` 零命中）。

## [1.0.0-alpha.362] - 2026-09-16

### Feat: 多主機分頁改用 host badge，取代漸層色標（#1077）

alpha.352 的 host color mark（24px 左緣漸層／左線／底線）在深色主題上糊成一塊、還蓋住分頁圖示，使用者實測「和想像落差大」。改為 **host badge**：主機顏色淡底的小方塊，裡面放該台 host 自己的圖示。設計以旋鈕頁 `docs/pages/host-color-lab.html` 逐項調到定案。

- **位置**：分頁圖示插槽之後、標題之前（綠色 `✳` 是標題文字的一部分，不被拆開）；釘選分頁（36px 只放圖示）不畫。
- **外觀**：底色＝host 色低透明度、線條＝host 色實色；色塊貼齊文字（預設 16px）、圖示內縮 2px、圓角 4px。
- **每台 host 自選圖示**：`HostConfig.icon` / `iconWeight`，複用 workspace 的 Phosphor 選擇器（搜尋、分類、六種字重含 duotone），隨 hosts sync 同步。
- **沒設定就沒有**：host 既無顏色也無圖示 → 不畫、不佔空間。
- **設定 > Terminal**：側欄與上方分頁各一組，可調啟用、線條顏色（host／中性灰）、線條濃度、底色濃度、色塊大小、圖示內縮、圓角。
- **安全**：host 圖示名稱在寫入、同步、還原、渲染四處都必須是真實存在的 Phosphor 名稱（bounded shape guard＋catalog 比對），避免外來字串被當文字畫進色塊。
- **移除**：`HostColorMark`、`useTabHostColor`、`HostColorMarkSetting`、4 個 `hostColor*` 設定欄位與其 locale key。
- 順手修：`WorkspaceIcon` 在沒有名稱時會先閃一個 `?` 再顯示圖示。
- **Review**：spec 與 plan 各一輪 codex 審（plan 兩個 blocker：刪除順序會讓樹編不過、badge 位置疑慮）；PR R1 無問題，R2 攻擊＋防守同指圖示名稱未驗證（已修）、檔案體質 approve。
- 測試 vitest 449 files / 5818 tests；Go 未動。

## [1.0.0-alpha.361] - 2026-09-16

### Style: 全 app 細版中性 scrollbar；並移除 Tmux Agent Monitor 設定頁（#1072）

兩件不相干的變更，各自一個 commit：scrollbar 樣式從「只有 activity bar 有自訂」推到全域，以及把已無人使用的 Tmux Agent Monitor 設定頁與其唯讀 API 整組移除。

- **全域 scrollbar**：原本只有 activity bar 套了自訂樣式，其餘 40 多個 scroll 容器（New Tab、Settings、Host 各頁、檔案樹、editor）都吃系統預設，在深色主題下是一條淺灰色粗條。現在 `scrollbar-color` 放 `:root`（inherited，還能穿 shadow boundary）、`scrollbar-width: thin` 用 `*`（**這個屬性不是 inherited**，只放 `:root` 會只改到 viewport 而完全改不到內層容器）。
- **thumb 顏色改用 `--text-secondary` 55%**，不是 `--text-muted`：實測四個內建主題 × 四種 surface，`text-muted` 45% 最差只有 1.35:1，而且即使 100% 不透明在 Dracula 的 elevated surface 上也只有 1.94:1，救不回來；`text-secondary` 55% 最差 2.46:1，與 VS Code 預設 slider（2.42:1）同一級。
- **終端 viewport（`.xterm-viewport`）刻意排除**，渲染與 alpha.360 完全相同：`@xterm/addon-fit` 為 scrollbar 預留的是寫死的 14px 而非量測值，把實際 scrollbar 改細會讓它多留寬度、最多浪費一欄（方向安全，不會遮住內容）。要正式處理終端 scrollbar 得連 `overviewRuler.width` 一起改 → #1075。
- 既有兩個例外照舊：`.scrollbar-hide`（分頁溢出條，完全隱藏）與 `.activity-bar-workspace-scroll`（accent 色）。五條規則全部收進新檔 `spa/src/styles/scrollbars.css`。
- **移除 Tmux Agent Monitor**：設定頁與 4 個元件、`GET /api/agent/monitor/{chains,chains/{id},projection}` 三個 endpoint、`host-api.ts` 的三個 fetch 函式與四個 interface、60 個 i18n key、`monitor.go` 與其測試 —— 共約 1830 行。
- **trace 寫入管線與整個 `TraceStore`（含 `trace_read.go`）刻意保留**：寫入成本低且有 `traceLimits()` 上限，trace 表是唯一能事後重建 hook chain 的資料，且 store 讀取層並非死碼（十幾個保留的 Go 測試透過 `ListChains` / `GetChainRecord` 斷言）。狀態燈不讀 trace（走 hook WS 廣播與 statusline），不受影響。
- Lights rebuild 的 Phase 5 Dev Inspector 原本規劃沿用這三個 endpoint（從未實作），該 spec 已就地加註須自行重建讀取層；另兩份仍把已刪 API 寫成有效的 spec 也一併加註。
- Follow-up：#1073（trace 讀取層歸屬）、#1074（daemon/renderer 版本落差政策）、#1075（終端 scrollbar）。
- ⚠️ 純 SPA + daemon 變更。SPA 走 HMR 即可生效；daemon 需重新編譯才會移除那三個路由。

## [1.0.0-alpha.360] - 2026-09-16

### Feat: Session Launcher — 專案／指令格狀啟動器取代舊的 cwd 輸入（B3，#1070）

New Tab 每台 host 的 `+` 與 Host › Sessions 的「New Session」改為開啟同一個啟動器：上方是 session 名稱欄位，下方是該 host 所有專案的格狀清單，每個專案後面接著所有 normal 指令的圖示。

- **兩種啟動方式**：點擊指令圖示會以該專案路徑為 cwd 建立 tmux session，接著送出該指令；點擊專案名稱則只啟動該 cwd。在名稱欄位打字後按 Enter 維持舊有預設（`~`、terminal 模式）。
- **名稱產生**：專案啟動時名稱留白會使用 `{slug}-{N}`；遇到 409 會自動換名重試（1 次嘗試 + 最多 5 次重試，只對產生的名稱重試，被拒絕的名稱不會再次提出）。
- **建立與送出走同一條 pinned host transport**，並帶 tmux 世代守衛；舊 daemon 若沒有回傳世代就跳過送出並明確說明；送出失敗時保留已建立的 session，並以 toast 回報。
- **格狀版面**：以 container query 確保永遠不會少於兩欄，完整鍵盤操作，Escape 取消；沒有專案與舊 daemon 的狀態都在畫面上就地說明。
- **Host 離線守衛保留在啟動器內**，包含 session 已建立之後的情況（啟動器會留著並顯示錯誤，不會靜默關閉）。
- **移除舊的 cwd 輸入與 terminal／stream 下拉選單**：cwd 現在一律來自專案，stream 模式之後會以 Nexen 模式回歸。
- ⚠️ 純 SPA 變更，HMR 即可生效；需搭配 alpha.358 的 daemon。

## [1.0.0-alpha.359] - 2026-09-16

### Feat: Host 設定頁 — 專案／指令／快照三頁、每台 host 的 resume 範本，並移除 Quick Command（B2，#1068）

Host 設定新增 Projects、Commands、Snapshots 三個頁面，全部以 alpha.358 的 daemon `hostconfig` 為後端；resume 範本改為每台 host 各自一份，整套 Quick Command 系統同時移除。

- **Host › Projects**：名稱／slug／路徑編輯、即時路徑檢查（`dir`／`not_dir`／`missing`／`error`）、拖曳排序、上限 200 筆；daemon 錯誤訊息就地顯示，409 衝突自動重新載入最新內容。
- **Host › Commands — Normal 分頁**：名稱／指令／圖示；圖示選擇器提供 5 個 agent 圖示，並可搜尋完整 Phosphor 圖示庫。
- **Host › Commands — Resume 分頁**：每台 host 自己的 resume 範本，指令字（command word）對照該 host 檢查。
- **Host › Snapshots**：host 範圍的重建紀錄／tmux session 一律作用在已依 host 過濾的快照上；device 範圍的區塊（分頁、還原版面、全部還原、各電腦狀態）只在 dev host 顯示；擷取與重建共用同一把 busy lock。
- **每台 host 的 resume 範本**：全域 resume 範本 store 移除；批次重建改為先分組，再逐一載入各 host 的範本。
- **移除 Quick Command**：store、slots、slot executor、`CommandSlot`、`QuickCommandMenu`、模組 command API、全域 Commands 設定頁、sync contributor、i18n keys 全數刪除，約 5900 行。
- **Review 修正**：host config 請求綁定該 host 當下的位址與世代（過期回應丟棄、host 變更或移除時中止在途請求）；同一 host＋集合的儲存改為排隊並以 id 定位項目；resume 草稿在儲存失敗後保留，只有在仍持有已儲存的值時才釋放；單列快照重建改為傳入當初規劃的 binding。
- ⚠️ 純 SPA 變更，HMR 即可生效；但需搭配 alpha.358 的 daemon（舊 daemon 會顯示「不支援」提示）。
- ⚠️ 不做 persist migration：舊的 quick-command／resume-template localStorage 直接捨棄。

## [1.0.0-alpha.358] - 2026-09-16

### Feat: Daemon hostconfig 模組 — 每台 host 的專案／指令／resume 範本（B1，#1065）

Daemon 新增 `hostconfig` 模組（`DataDir/host_config.db`），存放每台 host 的 projects、commands 與 resume templates，作為 host launcher 的後端基礎。

- **讀取**：`GET /api/hostconfig` 回傳三個集合與目前 revision。
- **整批取代＋CAS**：`PUT /api/hostconfig/{projects,commands,resume-templates}` 以整個集合取代，帶 `baseRevision` 做 compare-and-swap（`BEGIN IMMEDIATE`）；revision 過期一律回 409 並附目前狀態，即使送來的項目不合法也以 409 優先。
- **路徑檢查**：`POST /api/hostconfig/check-path` 支援 `~` 展開，回傳 `dir`／`not_dir`／`missing`／`error`。
- **輸入防護**：欄位驗證上限、request body 1 MB 上限、拒絕重複 JSON key。
- 尚無 SPA 使用端（B2／B3 接續）。
- ⚠️ 需重新建置 daemon 並重啟才生效。

## [1.0.0-alpha.357] - 2026-09-16

### Feat: New Tab 每台 host 一個 Sessions 區塊＋host 選單樣式調整（#1064）

New Tab 的 Sessions 區塊改為每台 host 各一個（`sessions:<hostId>`，版面編輯器顯示 `Sessions · <host 名稱>`），可在 Settings › Interface › New Tab 分別擺放。

- **每 host 一個區塊**：`new-tab-registry` 新增 registry 層級的 provider source（`registerNewTabProviderSource`／`subscribeNewTabProviders`／`getStaleNewTabProviderIds`）與 `labelParams`；host source 由 host store 推導 provider，每台 host 保持穩定 component（區塊不重新掛載）。
- **舊版面遷移**：既有 `sessions` 就地換成各 host 區塊；使用者先前移除的區塊維持移除。
- **Prune 安全**：host store hydration 完成前不 prune；只清掉 source 所擁有的過期 id（已移除 host、舊 `sessions`），無 source 認領的 id 仍然不動。
- **模組可見性**：模組啟用與否以 mount 當下的快照判定（仍需 reload 生效）。
- **Host 標頭**：收合箭頭更亮、host 名稱粗體、「+」移到 host 名稱旁並加強顯示；j/k 與方向鍵導覽限定於各區塊內。
- **Host 設定子選單**：未選取項目與圖示改為較亮的色調。
- **命名**：「Nex」標籤更名為「Nexen」（en／zh-TW，程式識別字不變）。
- 測試 vitest 447 files、5677 tests；Go 未動。

## [1.0.0-alpha.356] - 2026-09-16

### Feat: 各電腦狀態備份 P3 — 合併還原（#1062）

「各電腦狀態備份」每一列新增「合併」：把某台電腦狀態中本機沒有的 workspace 與分頁加進來，既有的 workspace、分頁與目前選取都不變動；同樣只還原結構、不建立 tmux session。至此各電腦狀態備份三個 phase 全部完成。

- **分頁身分判定（`identity.ts`）**：tmux 分頁＝host＋session 名；editor／圖片／PDF＝來源＋路徑（未命名 editor 永不相同）；browser＝URL；execution＝host＋id；settings 依 workspace 名稱（去前後空白，指向不存在的 workspace 視為 global）；key 以 JSON 編碼，網址或路徑含 `:`、`|` 不會誤撞。
- **合併（`mergeDeviceState`）**：同名 workspace 保留原設定、補上缺的分頁；本機沒有的 workspace 整個新增（新 id）；本機任何地方已存在的分頁跳過；只有「新分頁」的分頁不帶入；複製分頁一律產生不與本機衝突的新 tab／pane／split id，tmux pane 保留 rebuild 紀錄；settings 分頁的 workspace 參照改指到對應的本機 workspace。
- **合併還原（`restoreDeviceStateMerge`）**：與完整取代共用同一條流程（操作鎖 → 形狀驗證 → 標記缺少的 host → 依名稱接回 → 只含結構的 `-prev` → 原子寫入＋rollback → 同步 session store）；合併以接回完成當下的本機狀態為基底，等待網路期間新開的分頁不會遺失。
- **Undo 備份一致性**：`-prev` 先在記憶體組好、確認 tab／workspace store 在擷取期間沒有變動才寫入（最多重拍 3 次，仍變動則放棄這次還原並保留原本的備份），Undo 不會丟掉還原前已存在的分頁；「復原」同樣不會重建 session。
- **UI**：每列「合併」按鈕（確認後一律抓最新紀錄）、與「完整取代」「刪除」共用同一個 action hook、單一執行與全域鎖，結果顯示新增 workspace／分頁與已存在數量。
- **Review**：P3 plan 經 codex 審並修訂（rebuild 紀錄保留、fresh id 避撞、dangling settings→global）；T1 自查出 key 分隔字元誤撞並改 JSON 編碼；PR R1 1×P2（Undo 備份與實際套用的世界不同步）；R2 攻擊＋防守同指 1 項（重拍失敗時已覆寫原本的 Undo 備份）已修；「分割方向不同但 pane 相同被視為同一分頁」依 D5 定案不改；檔案體質 approve。
- 測試 vitest 443→445 files、5586→5655 tests；Go 未動。

## [1.0.0-alpha.355] - 2026-09-16

### Feat: 各電腦狀態備份 P2 — 電腦清單＋完整取代（#1060）

Settings > Snapshot 的「各電腦狀態備份」區塊現在列出存放 host 上每台電腦的最後狀態，可展開看 workspace／分頁，並以任一台的狀態完整取代本機目前的 workspace 與分頁；只還原結構，絕不建立 tmux session。

- **依名稱接回（`reattachByName`）**：每台 host 查一次 session 列表（逾時 10 秒視同該 host 失敗，不再卡住全域操作鎖）；同 host 同名、code 與 `tmux_instance` 皆有效、模式相容才接回（code 不同也接）；同名且相容的 live session 不只一個時判定模稜兩可而不接；stream pane 不接回。
- **本機沒有的 host**：該 pane 標 `terminated: 'host-removed'`，不猜其他 host。
- **完整取代（`restoreDeviceStateReplace`）**：在快照操作鎖下執行形狀驗證 → 標記缺少的 host → 依名稱接回 → 寫只含結構的 `-prev` → 原子取代分頁與 workspace store（失敗 rollback）→ 同步 session store；錯誤回報帶 `hostRemoved`。
- **復原維持只還原結構**：device-state 還原寫入的 `-prev` 全部 `restorable:false`，同頁既有的「復原」不會重建 session（有整合測試）。
- **UI**：每台電腦一列（本機徽章、相對時間、版本、數量）、展開 workspace → 分頁樹、完整取代／刪除皆需確認、單一執行＋全域鎖、結果顯示接回／未連線／host 不存在數量；取代一律重新抓最新紀錄（清單刷新後不會套用舊 payload）；本機列在 clientId 尚未產生時也能正確辨識、不能刪除。
- **Review**：P2/P3 plan 經 codex 審並修訂；PR R1 無問題；R2 三視角 4 項（取代套用快取舊 payload、host 列表無 timeout 卡鎖、同名 live session 模稜兩可、clientId 未產生時本機列可刪除）全數修入；「連按兩次復原仍可能重建」屬既有 Undo 語意，防守方判定不擋。
- 測試 vitest 439→443 files、5520→5586 tests；Go 未動。

## [1.0.0-alpha.354] - 2026-09-16

### Feat: 各電腦狀態備份 P1 — daemon 模組＋自動上傳（#1055）

每台 Purdex client 會把自己最新的 workspace / 分頁結構自動存到 Development 選定的 host（`devHostId`），並標明來源電腦；P2（列表＋完整取代）、P3（合併）的 plan 隨 PR 進 `docs/specs/`。

- **Daemon `devicestate` 模組**：`device_state.db` 每個 sync `clientId` 一列、只留最新；`PUT / GET / GET 單筆 / DELETE /api/device-state` 走預設 token 驗證；`capturedAt` 較舊的寫入忽略（多視窗）；payload 頂層形狀與 SPA 讀取守衛 `isWellFormedSnapshotV1` 完全一致才存、5 MB 上限→413。
- **SPA 上傳器**：不連網的 payload builder（`WorkspaceSnapshot`＋由 pane 推出的 sessionMeta，restorable:false）；結構變動後 5 秒 debounce；內容 hash＋電腦名稱都相同則不送；單一 in-flight＋至多一次重跑；未設定 / 離線狀態顯示；上傳前重新確認目標 host 與其 ip/port/token，途中切換或斷線不會送到舊 host，端點或 token 變更會重傳。
- **電腦名稱**：可改名，預設 Electron `os.hostname()`／Web「瀏覽器 · OS」，一律 trim＋截到 64 字（過長 hostname 不再被 daemon 拒收）。
- **Settings > Snapshot** 新增「各電腦狀態備份」區塊：本機名稱（編輯中的草稿不會被背景預設名稱覆蓋）、存放 host、上傳狀態。
- **Review**：spec 與 P1/P2/P3 plan 各經 codex 審並修訂；PR R1 標準 1×P2（長 hostname）、R2 三視角 4 項（daemon 驗證與讀取守衛不一致、同 host 端點/token 變更不重傳、上傳途中切換 host 送到舊 host、預設名稱覆蓋編輯中草稿），全數修入、無延後 issue。
- 測試 vitest 431→439 files、5430→5520 tests（含合併 main）；Go 新增 devicestate 套件測試。

## [1.0.0-alpha.353] - 2026-09-16

### Feat: Nexen 整併 P-B.3 — Host「Nex」子頁 + daemon 對壞掉的 `[nex]` 軟失敗（#1054）

P-B.2（alpha.351）讓 execution pane 吃 Nexen；這輪補上每台 host 的 Nex 管理頁，並讓 daemon 在 `[nex]` 環境性錯誤時不再整個起不來。

- **Daemon soft-fail（I8）**：nex `Init` 對 buildOptions / data_dir / assemble 失敗改記錄 `initErr` 並回 nil，daemon 照常起；所有 `/api/nex/…` 回 `503 {"code":"nex_unavailable","error":…}`，`Start/Stop/Close` 為 no-op；失敗時把 `path_prepend` 對行程 PATH 的修改還原。`NexConfig.Validate` 的靜態格式錯誤仍在 config load 時致命。`pdx nex` 對 503 印出原始錯誤而非「not enabled」。
- **`/api/info.nex`**：`{configured, mounted, ready, init_error, effective, restart_required}`；`configured` 是開機時的值；`effective` 是實際傳給 Nexen 的展開後設定；`restart_required` 由 daemon 比對開機時的 `[nex]` 與目前設定（nil 與空 list 視為相同），UI 不必自己重做 `~` 展開與 PATH 存在性過濾。`core.StatusReporter` 介面讓 core 不 import nex module。
- **`PUT /api/config` 接受 `nex`（I9）**：整段物件、`Validate` 靜態驗證（400 帶欄位名）、寫入 `config.toml`，**不熱套用**；`{"nex": null}` → 400。root / PATH list 正規化成 `[]`，沒有 `[nex]` 的 host 不會送出 `null`。
- **SPA Host → Nex 子頁**（order 6）三張卡：
  - 引擎狀態：Disabled / 未執行 / Unavailable（附 init error）/ Ready；phase · host_id、帳號、5h/7d 額度（`null` 顯示「未知」不顯示 0）、roots、profiles、lease TTL、目前生效設定；host 重連後重抓。
  - 設定表單：完整 `[nex]` 欄位（list 編輯、profile 下拉、duration）；400 錯誤對應到欄位；存檔後依 `restart_required` 顯示「需重啟 daemon」；儲存途中繼續編輯不會被覆蓋。
  - 執行體表格：列表 + 全站 SSE 當刷新訊號（500 ms debounce、斷線以 `Last-Event-ID` 續傳不重播全站歷史）、Open（host-scoped，I10）、Terminate（確認 + lease best-effort release）、Archive/Unarchive、顯示已歸檔。
- **Review**：subagent TDD 逐 task review（Task 4–7 各有修正輪，含 StrictMode 下表格永遠不顯示列、切 host 後舊列可被 Open、表單在 config 未載入時可把真實 `[nex]` 蓋成空值、Refresh 失敗無法恢復）；final whole-branch review（opus）2 Critical（`~` 路徑讓「需重啟」永遠亮、`[nex]` 缺 list → JSON null → 頁面崩）＋一波修（`restart_required` 改由 daemon 算）；codex R1 1×P2、R2 三視角（soft-fail 後 PATH 未還原、Refresh／存檔回應跨 host、狀態卡殘留前一台資料、`configured` 應讀開機值、抽 `useNexHostData`）全修；R3 1×P2（存檔途中再編輯卻顯示「已儲存」）已修。
- 測試：Go 全綠（nex module soft-fail／Status／PATH 還原、core info/config handler、boot snapshot、pdx nex 503 probe）；vitest 422→427 files、5214→5276 tests；延後 #1051（gofmt 既有 28 檔）。

## [1.0.0-alpha.352] - 2026-09-15

### Feat: 多主機 Host 顏色標示（#1052）

每台 host 可設定自己的顏色，屬於該 host 的分頁在側欄與上方 tab 列顯示色標，多主機一眼可辨；未設定＝完全不畫。

- **Host 顏色**：`HostConfig.color`（`#rrggbb`，隨 hosts sync 同步）；Host > Overview 新增選色器（8 預設色＋hex 輸入＋清除，IME 安全），寫入一律走 `setHostColor`。
- **判定**：分頁的 host＝第一個 tmux-session pane 的 hostId；editor / browser 等非 tmux 分頁不上色。
- **顯示方式分開設定**（Settings > Terminal）：側欄與上方 tab 各自選「漸層 / 左邊線 / 底線 / 關閉」，線條樣式可調 1–6px；預設側欄漸層、上方底線、2px。色標為絕對定位，不影響 tab 寬度、拖曳與截斷。
- **信任邊界**：host color 在 sync 反序列化與本機 rehydrate 時驗證，非法值直接移除；顯示設定在 sync 與 rehydrate 共用同一 sanitizer（非法 style 丟棄、寬度夾在 1–6）；渲染前再驗一次，杜絕 inline CSS 注入。
- **Review**：spec 與 plan 各一輪 codex 審（NEEDS-REVISION → 全修）；PR R1 標準 1×P2（清除未重設草稿）、R2 三視角 4 項（非字串 color 使選色器崩潰、hosts sync 未擋非法 color、UI 設定 rehydrate 未驗證、`HostColorMarkSetting` 抽出），全數修入、無延後 issue。
- 測試 vitest 424→425 files、5214→5334 tests；Go 未動。

## [1.0.0-alpha.351] - 2026-09-15

### Feat: Nexen 整併 P-B.2 — execution pane 改吃 Nexen（#1047）

P-B.1（alpha.350）的 Nexen client + execution store 接上 UI：`{kind:'execution'}` pane 現在是即時的 Nexen execution 對話窗，取代 M0 的 execution detail 頁。

- **Renderer 共用**：從 Stream `ConversationView` 抽出 `ConversationMessages`（I7 snapshot 保證 DOM 完全一致）；`StreamInput` 加 `showAttach` / `initialValue`。
- **Pane identity＝`(hostId, executionId)`**（I10）：route / deeplink 先解析 host 再開；`resolveExecutionHostId` 對**有給的 host hint 原樣回傳**（即使該 host 已不存在，pane 顯示「Host removed」），只有完全沒 hint 才 fallback 第一台。
- **`useExecutionLease`**：ensure / renew / release / forget、idle 策略、`beforeunload` keepalive release、stale continuation 防護、host 移除與 undo 復原。
- **`useExecutionSubscription`**：summary → history → SSE 依契約順序、`Last-Event-ID` 續傳、每 host 4 條 live SSE（LRU 暫停/恢復）、初始鏈路失敗 backoff 重試、`not_found` / `nex_unavailable` / `nex_disabled` / `host_removed` 問題狀態。
- **`ExecutionView` + `useExecutionActions`**：send / interrupt / terminate 一律走 lease；樂觀 bubble 與 `message_accepted` 對帳（I12）、送出前即上鎖、過期的 send 回應不覆蓋新 send、`lease_*` 錯誤丟本地 lease、SSE 終局關閉後鎖輸入；i18n en / zh-TW。M0 `ExecutionDetailPage` / `execution-api` 移除。
- **安全**：`nexFetch` 與 SSE 對未知 host 一律拒絕（`getDaemonBase` 對未知 id 會 fallback 到 active host，會把 Bearer 送到別台）；刪 host 時無 host 的舊 pane 以刪除前第一台當解析 host（closeTabs 關、keep-tabs 釘住）。
- **Review**：subagent TDD 逐 task review、最終 whole-branch review（2 Critical + 3 Important + 1）一波修 + scoped re-review；PR R1（2×P1 route/deeplink host fallback、1×P2 送出鎖）、R2 三視角（3 條真 bug + 體質 3 項）、R3（1×P1 lease 取得途中 host 被刪）、R4（1×P2），全數修入。延後 #1048（subscription hook 拆分）、#1049（deferred minors）。
- 測試 vitest 422→420 files（M0 detail 頁測試移除）、5114→5214 tests；Go 未動。

## [1.0.0-alpha.350] - 2026-09-15

### Feat: Nexen 整併 P-B.1 — SPA 端 Nexen client + execution store（#1045）

P-A（alpha.346）把 Nexen 引擎掛進 pdx daemon 的 `/api/nex`，這輪補上 SPA 消費它的底層（無 UI）；P-B.2 execution pane 與 P-B.3 Host「Nex」設定/監控頁的 plan 隨 PR 進 `docs/specs/`。

- **`spa/src/lib/nex/`**：`client-id.ts`（每個分頁一個 `X-Pdx-Client`，principal 變 `pdx:<host>/<id>` 才能仲裁 lease）、`types.ts`（wire 型別 + `NexApiError {status, code}`，網路失敗也映成 `code:'network'`）、`nex-api.ts`（Bearer + `X-Pdx-Client`，永不帶 ticket）、`sse-parser.ts` + `nex-sse.ts`（fetch/ReadableStream SSE，`Last-Event-ID` **header** resume、backoff 1s→30s 含 jitter clamp、45s idle/stall timeout 對應 server 15s keepalive、401/403 與結構化終局碼停止、只對 `draining`/裸 5xx/網路錯誤重連、`stream_url` 一律鎖回 daemon origin）、`event-reducer.ts`（seq 冪等純 reducer；`id:` 行為 seq 權威；`lease.*` 事件只碰 `summary.lease`，本機 lease 只來自 attach/renew 回應）。
- **`useExecutionStore`**：key＝`(hostId, executionId)`、只放資料不放連線；`setSummary(…, asOfSeq)` 讓 refetch 期間到達的 lifecycle 事件不被舊 summary 蓋掉；host 移除 cascade 清 store 並關該 host 的 execution tab。
- **Spec v2.1**：每 host 最多 4 條 live SSE（HTTP/1.1 每 host 6 連線上限，LRU 暫停/恢復）；execution pane 的 host 缺失一律顯示「Host removed」，不 fallback 到別台。
- **Review**：codex spec 一輪（12 條）、plan 一輪（8 條）、subagent TDD 逐 task review、最終 whole-branch review（3 Important + 1 minor 一波修）、PR R1（1 條）+ R2 三視角（7 條，含 P1：絕對 `stream_url` 會把 Bearer 送到別的 origin、`503 nex_unavailable` 無限重連）+ R3 乾淨，全數修入。
- 測試 +12 檔（vitest 411→422 files，5034→5114 tests）；Go 未動。

## [1.0.0-alpha.349] - 2026-09-15

### Fix: 點系統通知跳錯 tab——session code 跨主機碰撞，tab 查找改帶 hostId（#1041）

**起因**：Mac Electron 點系統通知會跳到另一台主機的 tab（常是某 workspace 的第一個 tab）。Session code 是 tmux `$N` 的**確定性**編碼（`codec.go`），兩台主機 `$N` 相同就得到相同 code——實測 mlab `purdex` 與 air `bb2` 都是 `zk16vd`（另 `qorh3k`、`8fawn6` 各撞一組）。而 `findTabBySessionCode(tabs, sessionCode)` 只比 code 不比 host，`handleNotificationClick` 拿到 payload 的 `hostId` 也沒用上，於是命中 `tabs` 插入順序最早的同 code tab。

- **`findTabBySessionCode(tabs, hostId, sessionCode)`**：primary pane 需 host 與 code 皆相符；dispatcher `hasTab`、click routing、deeplink `focusExistingSessionTab`、ExecutionDetailPage 五處全帶 hostId。
- **Codex R1 無 finding；R2 三視角六條全修**（gpt-5.5）：
  - 攻擊方：compositeKey 用第一個 `:` 拆會把含 `:` 的 hostId 切壞（daemon `host_id` 即 `hostname:code` 形式）→ 抽 `splitCompositeKey` 用**最後一個** `:`（sessionCode 固定 6 字 base36 不含 `:`），統一 dispatcher（含 removed-host debounce 清理）與 `useRelayWsManager` 共 5 處；無 `action` 的 legacy click 分支自 #162 起是死碼且碰撞時會猜錯 host → 刪除；ExecutionDetailPage render 重算 hostId 可能與 fetch 用的分裂 → 存進 `ready` state。
  - 防守方獨立抓到 relay stream 路徑同型拆解，與攻擊方收斂。
  - 體質：延後 #1042（notification / deeplink / shortcut 三處 tab 啟用邏輯抽共用 `activateTab`）、#1043（`useNotificationDispatcher.test.ts` >700 行拆分）。
- 測試 +11：pane-tree 同 code 不同 host（兩種插入順序）、dispatcher 複現 bug + 含 `:` hostId 走 production subscription、relay 含 `:` hostId、composite-key 互逆、ExecutionDetailPage hostId 釘住。

## [1.0.0-alpha.348] - 2026-09-15

### Fix: 打包版 menu bar tray 圖示消失＋新增「顯示於選單列」開關（#1037）

**起因**：使用者問「purdex 現在的 electron 會產生系統列？」——會，而且從第一版就無條件建 `Tray`，但打包後的 `.app` 其實只在 menu bar 佔了一個**看不見的空格**：`tray.ts` 寫死 `../../spa/public/icons/icon-192.png`，只有從源碼跑才存在（electron-builder 只打 `out/**`），`nativeImage.createFromPath` 拿到不存在路徑回空圖。就算路徑對，`icon-192.png` 是不透明圓角方塊，套 `setTemplateImage(true)` 後也只會是一顆實心色塊。

- **專用 template 圖示**：從 app icon 萃取紫色玻璃形，產生 16px + 32px `@2x` 的黑色＋alpha `trayTemplate.png`，放 `spa/public/icons/` 讓 Vite 帶進 `out/renderer/icons/`——打包與 dev-update（兩者都只搬 `out/`）都能拿到。`resolveTrayIconPath` 純函式先找 `out/renderer/icons/`、再退回源碼樹；找不到就 warn 並**不建 tray**，不再生空圖。
- **「顯示於選單列」開關**（Settings → 桌面應用程式）：偏好 SOT 放 main process 的 `userData/app-prefs.json`（`showTray` 預設 true），因為 `whenReady` 時就得決定建不建，不能等 renderer；SPA 只走 IPC `tray:get-visible` / `tray:set-visible` 讀寫，不另存 zustand。macOS 關掉 tray 後關視窗仍由 Dock + `activate` 叫回；非 macOS 若 tray 關閉則最後一個視窗關閉即退出（否則沒有路回到視窗）。
- **Codex 三輪 review 收斂**（gpt-5.5；R1 一條 P3、R2 三視角四條 medium/high、R3 一條 P2，全修）：
  - `set-visible` 改為 `applyTrayVisibility` 交易語意（`electron/tray-visibility.ts`，deps 注入、純函式可測）：**先寫 prefs 再動 tray**，寫檔 throw 時 runtime 不變、IPC reject 讓 UI 回滾；`createTray` 因圖示缺失回 null 時回寫 `showTray:false` 並回傳實際狀態，不再出現 prefs／UI／runtime 三方分裂。IPC 抽成 `electron/tray-ipc.ts`（`fs-ipc.ts` 的 register 模式），`main.ts` 不再長胖。
  - 成功套用後對所有 `BrowserWindow` 廣播 `tray:visibility-changed`，多視窗的 Settings 頁同步；ElectronSection 用遞增 request id 丟棄連點時過期的 resolve／reject，初始 `getVisible()` 若在使用者已切換後才回來也不覆寫。
  - dev server 把新 SPA 熱載進舊 shell（preload 尚無 `electronAPI.tray`）時，該列 disabled 而非一顆按了會靜默回滾的開關。
- 測試：electron +25（app-prefs 14、tray-icon 3、tray-visibility 8），SPA ElectronSection 12。

⚠️ 需在 Mini 重跑 `pnpm run electron:build` 讓 `out/` 帶上新 main/preload，Air 再 dev update；bump 後已重打。

## [1.0.0-alpha.347] - 2026-09-15

### Fix: nex module codex R2 follow-up——WS ticket 只認真握手、`--addr` 正規化、關機 signal 語意、HOME-less Init、`X-Pdx-Client` CORS（#1038）

alpha.346 merge 後 ChatGPT 配額回來，對 main 補跑 codex R2 三視角（gpt-5.5；從這版起 codex 一律 `--model gpt-5.5`，gpt-6-astra 太燒）。六條全修，外加 codex R1 抓到第一版修法的殘餘。

- **WS ticket 用途 scope**（攻擊方 + R1）：`TokenAuth` 原本對任何 method/path 都收 `?ticket=`，P-A 把 `/api/nex/` 掛在同一條 chain 後，一張 30 秒一次性的 WS 票券就能 `delegate`/`send`/`interrupt`。第一版只加 `IsWebSocketUpgrade` 判斷，R1 指出 curl 帶兩個 upgrade header 送 POST 仍會過；定案 `isWebSocketHandshake`：**GET + `Connection: Upgrade` + `Upgrade: websocket` + `Sec-WebSocket-Version`**（RFC 6455 握手形狀），其他一律 401 且不消耗票券。daemon 四個 WS 端點都由 gorilla `Upgrader` 服務、SPA 只在 `new WebSocket()` 用 ticket，零影響。auth 矩陣改：plain ticket → 401、POST+握手 header → 401、GET 握手 → 到達。
- **`pdx nex --addr` 正規化**（攻擊方）：`url.Parse`，要求 http/https + host，拒 query/fragment/userinfo（exit 2），`path.Clean` 去尾斜線——之前 `/api/nex/` 會拼成 `//v1/…`、`?x=y` 會把路徑吞進 query。
- **Serve-first 關機路徑的 drain 拿掉**（體質）：alpha.346 那個「先 drain 殘留 signal」會把 Serve 返回到 select 之間到達的**真正第一個** Ctrl-C 吃掉，要按第三次才退出。現在 Serve-first 路徑任何第一個 signal 都提示、第二個才 exit 130；signal 觸發的路徑不變。
- **`Init` 不再無條件要 `HOME`**（體質）：config 層已容許「全絕對路徑 + `path_prepend = []`」在 HOME-less 下通過，但 `Init` 一開始就 `UserHomeDir()` 失敗回錯，opt-in 的 host 起不來。改成與 `Load` 同規則：只有真的含 `~` 才報 HOME。
- **CORS 加 `X-Pdx-Client`**（防守方）：P-A 定案 SPA 每個 client 送這個 header 拿 per-client principal，但 allow-headers 漏了，瀏覽器 preflight 會擋。
- I11 測試改名，明寫手補 transcript 只證明 principal，續談契約由 acceptance 3a 覆蓋。

Review：Claude（opus）六項全過；codex R1 gpt-5.5 一條 P2（上述殘餘）已修。

## [1.0.0-alpha.346] - 2026-09-15

### Feat: Nexen 執行引擎以 opt-in 的 `nex` module 嵌入 pdx daemon（P-A，#1035）

**起因**：指揮官模式（一個互動式 CC 控多個 `claude -p` worker）、手機接管桌機 session、Aigora 事件升級，三個場景都需要每台 host 上有一個 headless agent 執行引擎。Nexen（`lab.protype.tw/wake/nexen` v0.9.0：`claude -p` stream-json runner，自帶 SQLite event store、SSE、lease）就是那個引擎，但使用者不想再維護第二套跨機部署——所以把它當 Go library 編進 `pdx`，讓既有的 local daemon install／dev update 一併帶上，後續 P-B（SPA execution pane）、P-C（啟動 UI + handoff）、Aigora、Swift 手機端全部吃 `/api/nex/v1/*` 這一份契約。同時定案 Purdex 自己的 Stream 模式／`pdx relay`／bridge／M0 execution+dispatch 之後在 P-D 全拆。

**`[nex]` config**（預設 `enabled = false`，未 opt-in 的 host 零影響）：`repo_roots`／`service_roots`（admission allowlist，至少一個）、`claude_bin`、`cswap_bin`、`path_prepend`（launchd/Finder 起的 daemon 沒有 shell PATH，Init 時把 `~/.local/bin`、`/opt/homebrew/bin`、`/usr/local/bin` 依序前置到 process PATH，冪等、`[]` 停用——全域影響已在 spec §4.6 承認）、`[nex.sandbox] max_profile/default_profile`、`[nex.timeouts]`。pdx 側驗證指名 key，Nexen 自己的 `Validate()` 在 `Assemble` 前跑；`HOME` 缺席時已停用的 section 不會擋住 daemon 啟動（R2 抓到的回歸）。`PUT /api/config` 拒絕 `nex` key（Settings UI 是 P-C）；`/api/info` 多 `nex: {configured, mounted}`。

**掛載與 auth**：`internal/module/nex` 以 `StripPrefix(/api/nex)` + `PublicPrefix` 掛 Nexen handler（回應裡的 `lease.*.path`／`stream_url` 帶前綴），外層仍是 daemon 的 `CORS → IPWhitelist → PairingGuard → TokenAuth` chain；Nexen 的 Authenticator 回 principal `pdx:<host_id>`，可選 header `X-Pdx-Client` 加尾段 `pdx:<host_id>/<client>`（同 token 的兩個 client 否則會靜默互相 re-mint control lease——P-B 的 SPA 每個分頁送自己的 id，CLI 不送）。11 列 auth 矩陣釘住「未授權永遠到不了 handler」。Go 側只走 HTTP，`go/parser` 測試守住 import 邊界（只准 `nexen`、`api`、`config`、`sandbox`）。request-scoped recoverer 不包 writer（`Flusher`／`Hijacker` 原樣、`ErrAbortHandler` 透傳、logf 自己 panic 也保證 500 + stdlib log）。

**關機**：新 `core.Closer` + `CloseModules`、`core.ShutdownBudget`，`cmd/pdx/shutdown.go` 的 `serveAndWait` 把序列收成一條：`cancel → StopModules(ctx) → srv.Shutdown(ctx)[逾時 → Close] → CloseModules`，同一個 ctx、恰好一次、main 等到最後一步；第二次 Ctrl-C 立即 exit 130（Serve 先返回的路徑：第一個 signal 只提示、第二個才退出；預先殘留的 signal 先 drain 掉不誤判）。**pdx 重啟會中斷執行中的 turn**——Nexen reconcile 收斂成 `idle`，`send` 續談（實測 `--resume` 保留中斷前脈絡）。CORS 加 `Last-Event-ID`：SSE 一律 fetch + Bearer + `Last-Event-ID` header，不走 ticket。

**`pdx nex …`**：包 Nexen client（`delegate / ls / show / watch / events / attach / send / interrupt / archive / terminate / host`），addr/token 優先序 flag > `PDX_NEX_ADDR`/`PDX_NEX_TOKEN` > config；`--addr` 指外部時不回退本機 token；mount 404 才報「not enabled」（含 probe URL）。

**建置**：`go 1.26`、`lab.protype.tw/wake/nexen v0.0.0-20260914203205-1e5d09014b65`（Gitea private：機器層級 `go env -w GOPRIVATE=lab.protype.tw` + git `insteadOf` 轉 ssh:9079，`make check-goenv` 預檢、README 有寫；`.DEFAULT_GOAL := build`）、`modernc.org/sqlite` 1.46 → 1.54（MVS）。

**驗收**（spec §6，在隔離 daemon :7899 上，production :7860 未動）：冷快取裸 env 建置 ✅、start log／info／capabilities／401 ✅、真 claude delegate → result ✅、SIGTERM 中斷 → `idle` → `send` 續談 ✅、`kill -9` → `turn_orphaned` `process_killed:true` ✅、本 session 平行派兩 worker + interrupt ✅、execution → tmux `claude --resume` ✅、preflight + `Last-Event-ID` 續傳 ✅。**未閉合**：tmux → execution 需要 Nexen delegate 加 `resume_session_id`（v0.9.0 沒有，之前「delegate 已有 session_id 欄位」是誤讀；#1032 / nexen #65，P-C 前置）；3b 超時路徑未強迫；a26 真機（#1034）；`backup`/`sync`/`execution` 在 `Stop()` 關 store 的既有問題由新 seam 修（#1033）。**merge ≠ deploy**：mlab daemon 尚未換這版 binary。

**流程**：Nexen 側 N1 先 library 化（nexen v0.9.0，PR #59）→ P-A spec 一輪 codex（13 條全處置）→ plan 一輪 codex（12 條全處置）→ 12 task subagent TDD（每 task 獨立 review，5 個 fix round）→ final whole-branch review（opus）→ fix wave + 補丁 → PR codex R1（1 P2：Makefile 預設目標）→ **codex R2 三視角因 ChatGPT 配額用盡（至 9/19）改由 Claude 三視角代跑**（攻 3 Major／防 0／體質 0，全修）→ scoped re-review 全過。spec 定稿 v2.1。

## [1.0.0-alpha.345] - 2026-09-15

### Feat: Peer Address v2（P4b）——label 地址上線到 wire，遠端 helper 名稱原地跟著改（#1028）

alpha.344 讓 session 能用 `pdx msg name` 取名，但跨主機的 `from-name` 與對方 `ListAgents` 看到的 helper 名稱還是 v1 形式（tmux 名或 `cc:<name>`）。這版把地址送上線：`/api/peers/deliver` 的 `from` 多 `address`（`<label>:<suffix>`）與 `address_rev`（sender label store 的 host-wide 版本號），接收端只做語法驗證（head 是 user label 或 `_` 短碼、suffix 符合 `^[A-Za-z0-9_.-]{1,65}$`，否則 `400 bad_address` 不入 audit），當作已驗證主機的顯示資料；wrapper 的 `from-name` 變成 `<alias>/<label>:<suffix>`。

**helper 原地改名**：代表遠端 session 的 helper 由 daemon 直接改寫它自己的 `~/.claude/sessions/<pid>.json`（`map[string]json.RawMessage` 逐欄位 byte-identical 只換 `name`/`nameSince`，`.<pid>.json.tmp` + fsync + rename，殘留 temp 預清、`null` 檔拒絕），不 respawn、不加 stdin 命令（P3 D8 不破）；socket/pid/proxies.json 全不動，飛行中的 frame 不會丟。改名以 `address_rev` 門控：spawn 時就把 rev 記在 instance 上（waiter 取消也留得住）、只有嚴格更新的 rev 才改、名字沒變也推進 rev（擋 A→B→A 亂序重放）、改寫失敗 rollback 並照常投遞；v1 請求 spawn 的 helper 沒有 applied rev，升級後第一個 v2 位址不論 rev 都套用。freshness 是單向的：只有該 origin 的下一次 `/deliver` 會更新對方主機上代表它的 helper；suffix-only 變化（tmux 或 CC 改名但沒 claim/release）不推進 rev 所以遠端不更新，label store 重建導致 rev 倒退時要等超過遠端 applied rev 或 helper 重建——這兩個限制寫進 spec §3.5。

**鎖**：codex 攻擊方指出改寫的 fsync 若在 manager 全域鎖內，一個 peer 交替送合法位址就能讓所有 origin 與 `Stop` 一起卡在慢磁碟上；改成每-instance `renameMu` 兩段式（鎖序 renameMu → m.mu，phase 1 在 m.mu 下驗證 instance/state/rev、phase 2 在 m.mu 下提交，I/O 在鎖外；`Release` 清檔前先等飛行中的改寫完成），re-review 逐鎖列表證明無反向取鎖、無死鎖。

**流程**：4 task subagent TDD（全部一次過 review）→ final whole-branch review（Ready for PR，3 個便宜 minor 順手修）→ codex R1 無發現、R2 三視角 1 high（全域鎖 fsync）+ 2 medium（`null` registry panic、spec 未寫 freshness 限制）→ 全修，spec 定稿 v3.3（§11 記處置）。e2e `TestE2E_HelperRename` 五步：v2 from-name、native reply 讓 A 的 helper 命名為 `b/purdex-tester:…`、re-claim 後**同一 instance**改名、fresh `msg_id` 的舊 rev 重放不會退回、反向 freshness。`internal/agent/probe` 兩個時間敏感測試在 `-race -count>1` 下會 flake（本 PR 未動，另開 issue）。兩台一起升；混版無害（alpha.344 receiver 忽略 `address`，alpha.344 sender 拿到 legacy 名、升級後第一個 v2 請求改名）。

## [1.0.0-alpha.344] - 2026-09-15

### Feat: Peer Address v2（P4a）——session 用 `pdx msg name` 給自己取名，label 成為跨主機地址的主段（#1026）

**起因**：Peer Bridge 的人類地址 `<host>/<session>` 只有 tmux 名／tmux code／`cc:<registry name>` 三種來源，沒有一種是使用者當下能掌控的——mlab 的 tmux 都叫 `mt0`…`mt7`，code 是 tmux `$N` 的可逆編碼（server 重啟後 `$0` 會鑄出同一個 code），而要跑 computer use 的 Claude Desktop session 根本不在 tmux 裡，只剩 `air/cc:purdex-3f` 這種 `<cwd>-<2 碼>` 的名字，三個同 cwd 的 Desktop session 完全分不出誰是「測試員」。

**地址 v2**：`<host>/<label>[:<suffix>]`。**只有 label 是地址**；`:<suffix>` 由 daemon 從 entry 自己的 registry `tmux` 欄位與 CC 名算出（`mt0-purdex-49`、Desktop 則 `purdex-3f`），純供辨識，輸入時剝掉忽略。label 規則 `^[a-z0-9][a-z0-9-]{1,31}$`，`cc`、`tmux` 保留；未命名時是 `_` + sessionId 的 FNV-1a 取 base36 六碼（`_k3x9qz`，`_` 在使用者字集之外所以永遠撞不到、一眼看得出沒取名，resume 前後一致）。`<host>/tmux:<name>` 是不經 label 的明確 fallback；裸 tmux 名只在目標 inventory 完整時當第二層；`cc:<name>` 退役（`404 peer_not_found` 附 `pdx peers --all` 提示）。

**占用規則**：label 綁 CC `sessionId` 存在 host-local 的 `peer_labels`（`CREATE TABLE IF NOT EXISTS`，無 migration），配 host-wide 嚴格遞增的 `rev`（claim 與 release 都在同一交易內 bump，跨 daemon 重啟）。**持有＝持有者在 registry 裡有 live entry**：活的不被頂替（`409 label_taken` 回 `holder` 與該主機所有活的 `live_labels`，agent 一次就能挑個沒撞的）、死的自動空出、crash 後重開的 session 說一句 `pdx msg name purdex-tester` 就接回；舊持有者 resume 回來退回預設短碼。registry 檔讀不到／解不開、而檔名 pid 還活著的，歸類 **unknown**（`kill(pid,0)` 回 `EPERM` 也算活）：這種檔存在時該主機 inventory 標 `partial` 並列在 `unknown_registry_files`，claim 一律 `503 not_ready`（附 `skipped`），裸名解析也拒絕——寧可拒絕也不猜。

**Inventory**：每個活的非 proxy registry entry 恰好一列（新增 `row_kind: entry`，取代原本「tmux 內非 owner 的 entry 被吞掉」），列上多 `label` / `label_source` / `label_rev` / `suffix`；envelope 多 `daemon_version`、`unknown_registry_files`、`labels_unavailable`（三個 partial 來源各自獨立可見）。同一 conversation 有兩個 live process 時兩列同 label → 解析 `ambiguous`，誠實暴露而不是暗中挑一個；`tmux:` 仍走 pane tiebreak。

**API 與 CLI**：`POST /api/peers/self`、`PUT|DELETE /api/peers/self/label`（admin-only；origin 用 live entry 的 inbox 歸屬，helper socket 進不來；回應由已驗證的 entry + label row 直接組出，不再讀第二次 inventory；`labelMu` 只包 registry 讀＋DB 寫，encode 在鎖外）。`pdx msg name <label>` / `--release` / `pdx msg whoami`；`pdx peers` 多 `LABEL` 欄（`*`＝預設）、entry 列縮排、每主機 `daemon <version>` trailer 與三種 partial 原因行。CLAUDE.md 新增「Peer addresses」段給 agent 照做。`from-name` 與 helper 名稱維持 v1 形式，等 P4b。

**流程**：spec 兩輪 codex（R1 2 Blocker/12 Major、R2 1 Blocker/8 Major，全採納，v1→v3）→ plan 一輪 codex（1 Blocker/9 Major，全採納）→ 10 task subagent TDD（3 個 task 各修一輪：reply 守衛沒被測試 pin 住、proxy-origin 測試空轉、CLI 請求流程重複）→ final whole-branch review（3 Important：deliver/reply 的 partial 規則沒跟 send 對齊、`pdx peers` 看不到 unknown-file partial、遠端 `daemon_version` 未截斷）→ PR codex R1 無發現、R2 三視角抓到 1 high（registry 不完整時 label 單一命中繞過同 conversation 的 ambiguous 保護——現在有 alive-unknown 檔時單一命中也 `not_ready`）、3 medium（死亡 conversation 的 label 擋住同名 tmux fallback、`--all` 只有 owner-unresolved 時完全不顯示 partial、label-store 故障無法獨立傳達）→ 全修，spec 定稿 v3.2（§10 記處置）。兩台（mlab + air-2026）須一起升，不是混版協定；spec §5 的 P4a 驗收子集部署後跑。

## [1.0.0-alpha.343] - 2026-09-15

### Fix: 純檔名連結不再從連字號後面起頭；相對路徑偵測改為預設開啟（#1024）

alpha.342 上線後真機驗證抓到的兩件事。第一，`docs/…/115潛優修正計畫-核定.pdf` 在只開「純檔名」偵測時，可點的是 `核定.pdf`——`BARE_RE` 的主幹字元類含 `-` 但 lookbehind 沒擋 `-`，候選可以從連字號後面起頭；ASCII 同樣中招（`docs/foo-bar.md` → `bar.md`），點下去只會是 not-found。這是 #1022 spec 裡註記為既有、範圍外的 quirk，現在補上：lookbehind 改 `(?<![\p{L}\p{M}\p{N}_/:.-])`，ABS / TILDE / REL 不動；`foo-bar.md` 本身仍可連（連字號在主幹內），空白後的 `-foo.md` 這種像 flag 的 token 仍會連——那要動主幹規則，另案。第二，使用者實際踩到的根因其實是**「相對路徑（含 `/`）」偵測預設是關的**（四個開關只有絕對路徑與 `~/` 預設開），而 agent 輸出最常見的就是相對路徑；改為預設開。alpha 不做 migration：已持久化的設定保留使用者原值，只有新安裝拿到新預設。預設值測試改對 `getInitialState()` 斷言，先前的寫法在 `beforeEach` 種值後改期望也不會紅。codex 標準 review 無發現、adversarial approve（720 組裸檔名對 main 比對、persist merge 實測 false 保留／缺鍵取 true）。測試 5022。

## [1.0.0-alpha.342] - 2026-09-14

### Fix: 終端機連結——CJK 檔案路徑能點了；URL 不再被全形標點、黏著的中文與成對括號帶歪（#1022）

**檔案路徑**：`docs/data/申請流程/潛優/115潛優修正計畫-核定.pdf` 之前完全不會變成連結——四支 regex（`ABS`/`TILDE`/`REL`/`BARE`）的目錄段與檔名主幹都用 `\w`，沒有 `u` flag 時就是 `[A-Za-z0-9_]`，任何 CJK 段都讓比對失敗；管線其他部分（`col-map` 的寬字元欄位換算、opener）本來就吃得下 Unicode，只卡在這一關。現在四支 regex 從共用片段組出、加 `u` flag，段名與主幹接受 `\p{L}\p{M}\p{N}_`（`\p{M}` 是因為 macOS `ls` 常吐 NFD 分解形，`が` 是 `か`+U+3099；比對到的字串原樣交給 opener，不做正規化）。副檔名鏈拆成「中間段 Unicode、**最後一段維持 ASCII**」：黏在檔名後的中文敘述（`已更新 docs/a.pdf並重新`）會自然停在 `pdf`，而 `docs/報告.最終版.pdf` 仍整段連結。這是偏向不是保證——`docs/a.pdf然後/x.txt` 這種後面又接路徑的會整段吃掉，已用測試釘住當作接受的限制。

**URL**：`URL_RE` 原本只認空白和幾個 ASCII 引號當邊界，所以 `https://e.com/a，然後回報`、`…/a。`、`（見 …/a）` 全都把標點與後面的字吃進去；`Foo_(bar)` 結尾的成對括號則被誤剝（程式碼註解早就承認）。改成三步：(1) 掃描到全形標點集就停——`・` 刻意不在集合裡，日文標題常見；(2) **非 ASCII 截斷**：非 ASCII 字元只有在「前一字是 ASCII 字母／數字、且它自己不是拉丁字母或組合記號」時才切——`參考https://e.com/x這頁` 切在 `x`，`/臺灣`、`=測試`、`ISO_標準`、`www.例子.com`、`bücher.example`、`café/menu` 都完整保留；接受的犧牲是 `?q=abc中文` 這種字母直接接中文的真 URL，與 `/?然後回報` 這種分隔符後直接接敘述的誤保留；截斷後從截斷點重掃，同一行後面的 URL 不會跟著消失；(3) 尾端剝除改成先數括號再線性回走，只剝不成對的 `)`/`]`，加 scheme-only 守衛。`range` 一律是 UTF-16 offset，非 BMP 前綴（`😀`）算兩格，交給 `col-map` 換成 cell。

**流程**：spec + plan 一輪 codex（rework：9 項全採納——IDN 混合 label、`・`、`\p{M}`、線性 strip、scheme 守衛等）→ 兩 task subagent TDD → PR codex R1 無發現、R2 三視角（攻擊 approve：12 萬組 ASCII 對 main 全同、300 字元壓力無災難回溯；體質 ship；防守抓到 2 個 P2：重音拉丁字母被切、截斷後後續 URL 消失）→ 修正後收斂 review 無發現。terminal-link 測試 158 → 228，全套 5017 綠。純 SPA 改動，dev server HMR 即生效。

## [1.0.0-alpha.341] - 2026-09-14

### Feat: Development 頁面改為明確指定「開發主機」；Local daemon 區塊直接秀 URL / token，可一鍵加入 host（#1018；原 bump #1020 與並發的 #1017 撞號，改為 alpha.341）

**起因**是 air-2026 的情境：App 先裝、daemon 後裝，Development 頁面卻一律拿 `hostOrder[0]` 當更新來源——第二台機器上那台可能就是自己那顆沒 repo 的本機 daemon，永遠查不到更新；而本機 daemon 的 token 只存在 `~/.config/pdx/config.toml`，要加 host 得自己去翻。

**開發主機**：頁面頂端多一個 host 下拉，選擇持久化為 `devHostId`（裝置本地，不進 hosts 同步 payload）。**沒有 fallback**：沒選就整頁不發任何 dev 請求，Check / Update / Rebuild / Install 全部停用並提示。來源一變（換 host 或 token 改了）先關掉舊 stream、清掉舊 host 的更新狀態與 daemon check/rebuild log，再對新來源查；每個在途回應（fetch、SSE reader、rebuild 後 3 秒的重查 timer、`streamCheck` callback）都綁一個世代號，世代不符就丟，舊 host 的晚到結果不會畫到新 host 的畫面上。App 更新或 daemon rebuild 進行中鎖住下拉。

**Local daemon**：Electron `localDaemonStatus` 現在帶 config 的 token 與 hostname。區塊多三列：URL、token（固定遮罩＋顯示／複製；`http://` dev-server origin 下 `navigator.clipboard` 是 undefined——final review 實測到的——所以複製有 `execCommand` fallback，失敗顯示獨立的行內錯誤不污染區塊錯誤）、host 清單狀態——這個 `bind:port` 不在清單就給「加入 Host」，走既有 `registerLocalHost`。比對用新的共用 `findHostByEndpoint`（嚴格 ip+port，loopback 與 Tailscale IP 是兩個 endpoint，不做 hostname/token 猜測合併），UI 與註冊不可能不一致。

**流程**：spec 一輪 codex（6 項全採納）→ plan 一輪 codex（5 項全採納）→ 5 task subagent TDD 各一輪 review（Task 5 修一輪 clipboard 錯誤處理）→ final whole-branch review（1 Important：insecure origin 剪貼簿）→ codex R1 無發現、R2 三視角。R2 抓到 spec §2.2 的真缺口：來源若由**非 picker 路徑**（Hosts 頁改 token、跨視窗 rehydrate、sync full-replace）在 rebuild/update 進行中改掉，操作狀態會被 reset 抹掉——半套修法會鎖死或串狀態，正解是 operation-scoped state，併同 `DevEnvironmentSection.tsx`（502 行）抽 hook 的完整方案一起記在 #1019，spec §7 記錄已知缺口。測試 4913 全綠；Development 測試檔改為全域 stub fetch，不再真的打 100.64.0.2。

## [1.0.0-alpha.340] - 2026-09-14

### Fix: App 啟動的 daemon 看不到任何 tmux session——沒有 UTF-8 locale 時 tmux 會把 TAB 分隔符換成 `_`（#1014）

air-2026 上由 Finder 啟動的 Purdex.app 帶起 daemon 後，`GET /api/sessions` 永遠回 `[]`、`POST` 回 500 `session created but not found`、`session_meta` 一直 0 rows、log 一行錯誤都沒有——但 tmux 每一步都成功，手動跑 `list-sessions` 也正常。根因不是 tmux 版本（3.6a / 3.7c 都一樣）也不是 PATH：**App 的 launchd 環境沒有 `LANG`/`LC_ALL`/`LC_CTYPE`**，tmux 依 *client* 的 locale 清洗 `-F` 輸出，沒有 UTF-8 codeset 時每個不可列印字元（含 daemon 拿來切欄位的 TAB）都換成 `_`，整行變成一個欄位 `$0_probe1_/Users/wake` → `EncodeSessionID` 失敗 → 靜默 `continue`。更糟的是這串壞 ID 被丟給 `CleanOrphans`，把既有 meta 全清掉。mlab 沒事純粹因為 daemon 是從有 `LANG` 的 login shell 起的；daemon 起的 tmux server 連帶每個 pane 的 shell、agent、`capture-pane`、terminal relay 也都沒 locale。

修法放在 daemon 自己身上，不靠啟動器：新 `internal/locale.EnsureUTF8()` 在 `pdx serve` 解析完 flag、任何 tmux exec 之前跑——`LC_ALL` > `LC_CTYPE` > `LANG` 取第一個非空值（與 tmux.c 判 `CLIENT_UTF8` 的字串比對一致），什麼都沒設就 `LANG=en_US.UTF-8`（只設 `LANG`，pane 的 rc 檔仍可覆蓋），已是 UTF-8 就不動，使用者明確設了非 UTF-8 只警告不覆蓋。tmux server 從 daemon 繼承，pane / agent 一併修好。解析端也補防禦：`list-sessions` 少於 3 欄的行直接跳過不再填空字串（空 list 讓 `CleanOrphans` no-op），每次呼叫記一則含 locale 提示的 log；session 的 invalid-id skip 與 monitor 的 pane 解析錯誤也都會說話。

隔離 smoke（自己的 `--config` data dir + 自己的 tmux socket）：舊 binary 500/`[]`，新 binary 201/正常列出、log `exported LANG=en_US.UTF-8`、`tmux show-environment -g LANG` 正確。⚠️ air-2026 App 真機驗收待做。follow-up：#1015（Warned 情境的 log throttle）、#1016（`launch-env.ts` 轉發使用者自己的 LANG）。

## [1.0.0-alpha.339] - 2026-09-14

### Fix: Hosts → Sessions 頁面在未連線主機上整頁崩潰（#1012）

`SessionsSection` 的 selector 寫 `s.sessions[hostId] ?? []`：主機從沒送過 sessions payload（沒 token、連不上）時 `?? []` 每次都回新陣列，Zustand 5 / React 19 的 `useSyncExternalStore` 判定 snapshot 不穩定 → 無限 re-render → `Maximum update depth exceeded` → 整頁被 ErrorBoundary 接住。air-2026 加了 mlab 但還沒填 token，一開 Sessions 就炸；Mini 上主機都在線所以從沒踩到。改成模組層級共用一個空陣列。全 repo 掃過只有這一處這種 hook selector 寫法。

## [1.0.0-alpha.338] - 2026-09-14

### Feat: Peer Bridge 第三階段——跨主機把訊息投進 Claude Code session，原生回覆回得來（#1006）

前兩段讓 daemon 看得到自己與別台的 agent peer；這一段讓它送得到。在 host A 的 Claude Code session 裡跑 `pdx msg send air/<session> "…"`，訊息會以**原生 peer 訊息**出現在 host B 那個 session 裡（`<cross-session-message from="uds:…" from-name="mini-lab/<session>">`），對方 Claude 用自己的 `SendMessage` 回，回覆再回到 A 的 session——雙方都不知道中間有 Purdex。只送 Claude Code（Codex/OpenCode 列 `deliverable:false`），不做 typed-input。

**每個遠端 origin 一個 `pdx peer-proxy` 小程序當虛擬 peer。** 接收端 daemon 為每個 `(host_id, agent_session_id, pid, proc_start)` 起一個 helper：綁 `/tmp/cc-socks/<pid>.sock`、以 `O_EXCL` 寫兩個 registry 檔冒充一個 Claude Code session，訊息以 helper 的 `uds:` 位址寫進目標 inbox，對方的原生回覆就打回 helper，helper 用 stdout 原樣交給 daemon 轉回去。helper 只有 stdin/stdout、拿不到任何 token、env 只剩 `PATH`/`HOME`；ownership 在 ready 之前先原子寫進 `proxies.json`，daemon 啟動先 sweep：活著且 procStart 相符才送 SIGTERM→SIGKILL，每次送信號前重查身分，檔案只在 procStart 相符時才刪、socket 只在 connect 被拒時才刪，證明不了的留著並保留記錄；cap 32（starting/stopping/未解決記錄都算）、idle 30 分鐘回收（回收前在鎖內重查）、對端回 `target_gone` 才回收。

**投遞每一步都綁在已驗證身分上。** `/deliver` 只收 host 憑證（admin 403）；比對到的 config entry 要同時 alias 與 host_id 都對、`from.host_id` 等於它；`allow_bypass` 與回程 token 都只從那條 entry 讀；`from-mode` 跨主機一律夾成 prompting，接收端 per-host `allow_bypass=true` 才放行 bypass；`msg_id` 10 分鐘去重、每對 30/分鐘、每 host 120/分鐘（在 dedup/audit/inventory 之前）；audit 列先寫再投遞、寫不進就拒收；目標 tuple 在投遞當下重驗，inventory 只有部分結果時回 `not_ready` 而不是 `target_gone`（否則對端會誤回收 helper）。任何遠端自報字串進本機 log/audit/回應前都截斷或用固定文字；wire 上的 label、session id、hop_chain 都有長度與可列印限制；`/deliver` 的 200 回應也要驗過 result/mode 才吃。另一台 daemon 的 helper 在共用 registry 上會被辨識成 `proxy` 列（看程序 argv，看不到就跳過），永遠不可投遞、不可當 origin、不可當 replier。

**`pdx msg selftest` 是升版守門。** 開一個 throwaway `claude -p` session（stdin 掛 pipe——tty stdin 會直接退出，pane_pid 因此是 wrapper，claude pid 從 registry 取）、真的起一個 `pdx peer-proxy`、送 nonce、等原生回覆，最後一定拆：先 helper、再 tmux session、再對 claude pid 與 pane pid 各自身分重查後升級信號、registry 檔只在 procStart 相符時刪；任何一步不確定就 `cleanup incomplete` 非零退出。Claude Code 升版後跑一次；inventory 也會對比 2.1.270 新的版本各警告一次。

**真機驗收（mlab 兩個隔離 daemon + 真 Claude Code session）**：本 session → `b/mt-target` delivered；對方 Claude 原生回覆回到本 session 的 inbox；兩端 audit 各兩列（out/in、in/reply）；`--mode bypass` 在 `allow_bypass` 開前後分別記成 `b→p`、`b→b`；helper 隨 daemon 一起死並自清、重啟 sweep 把 `proxies.json` 清成 `[]`；env 未設/本機目標/admin 打 `/deliver`/deliver off/取消配對全部按預期拒絕；`pdx msg selftest` PASS 11 秒、cleanup ok、無殘留。

**流程**：plan 三輪 codex（1B/13M/3m → 1B/9M/2m → 4M）；12 個 task subagent TDD、各一輪 review 修正；final review 11 項修正；PR codex 標準 3 項 + 三視角 adversarial 7 項全修；收斂 review 無新發現。延後項開 issue 追蹤。

## [1.0.0-alpha.337] - 2026-09-14

### Feat: Peer Bridge 第二階段——host registry、每主機獨立憑證、跨主機 `pdx peers --all`（#998）

alpha.334 讓一台主機能盤點自己的 agent peer；這一段讓 daemon 知道其他主機、能以自己的憑證去問對方，並把結果併成一張表。air-2026 進 tailnet 後的用法就是：`pdx peers host add air https://…`、把印出來的 token 拿到 air 那邊 `host add mini-lab … --token …`、回來 `set-token`，之後 `pdx peers --all`。

**憑證是每個 inbound host 一把，沒有共用的。** spec 第一版寫的是接收端一把 `accept_token` 給所有遠端；codex 第二輪 review 指出持有那把 token 的任何人都能自稱另一台 `allow_bypass=true` 的主機。改成每個 `[[peers.hosts]]` 各有 `inbound_token`（本機鑄、只在 `host add` 的回應出現一次）與 `token`（對方鑄、我們出示），比對到哪一把就是哪一台，payload 裡的 `from.host_id` 只是查表 key。配對因此是兩步：兩邊各 `host add`，帶 token 的那一步會去打對方的 `/api/peers` 學回 `host_id`；兩邊都 verified 才算通。

**`/api/peers` 前綴走自己的一條 middleware 鏈。** 既有的 `TokenAuth` 在 admin token 為空時放行所有路由、也吃一次性 ticket，這兩件事對遠端 daemon 打進來的路由都不能成立；而在它外層再包一層判斷也沒用，因為內層照樣會拒掉 inbound token（防守方 review 抓到的）。所以 `cmd/pdx/http_chain.go` 把 `/api/peers` 與 `/api/peers/` 掛成獨立的 chain，結尾是 `PeerAuth`：admin bearer → admin principal，某台 host 的 inbound_token → host principal（帶 alias 與已驗證的 host_id 進 context），其他 401；host principal 只能打本機範圍的 `GET /api/peers`，其餘 403。一般 chain 對其他路由一個位元都沒動，包含 WebSocket 的 ticket。

**設定寫入收斂成一個入口。** `Core.UpdateConfig`：鎖內深拷貝 → mutate → 寫檔 → 提交（保留指標身分）→ 解鎖後才通知。原本 token 與 pairing 兩個 handler 是解鎖後才寫檔、又共用同一個 `.tmp`，兩個請求交錯會把舊 registry 寫回磁碟——現在全部走同一條序列化邊界。順手修掉 `Clone` 把明確清空的陣列變成 nil、重啟後偷偷回預設值的問題。配對的網路驗證在鎖外跑（3 秒），提交時在鎖內重查 alias 唯一、entry 還在、URL 與 inbound_token 沒被刪除重建換掉、host_id 為空或相同、不是自己——任何一項不對就 409，什麼都不落盤。

**遠端回來的東西一律當不可信。** 不跟 redirect（bearer 只會到你填的那台）、3 秒、16 MiB；錯誤文字截 200 bytes 加 `peer:` 前綴；學到的 `host_id` 要 ≤128 個可列印字元才存；每台的列重編碼後超過 1 MiB 就整台換成失敗列，不讓一台把總表撐爆到 CLI 讀不下；遠端列的 `Host`/`HostID`/`Address` 一律覆寫成本機 config 的 alias 與已驗證的 host_id，重建後不合法的地址設空；CLI 印表前跳脫所有控制字元（ANSI/OSC 注入），`--json` 維持原樣。

**真機驗收**：mlab 上兩個隔離 daemon 雙向配對成功，`--all` 兩邊各 26 列；九格授權矩陣（inbound token 只能 `GET /api/peers`，`scope=all`、hosts 路由 403，`/api/config` 401；無 bearer / ticket 401；admin 200）；`/api/config` 回應兩個 token 都是空字串而磁碟上兩把都在；對方停掉後顯示 `b  (unreachable: …)` 且 exit 0。

**流程**：plan 一輪 codex（1 Blocker 7 Major 3 Minor）、7 個 task 各自 review（兩個安全 task 用 opus；Task 5 一輪修正、Task 7 一條文法裁定）、final review 9 項修正、PR 標準 + 三視角 adversarial 5 項修正、收斂兩輪 2 項修正後無發現。延後 #996。P3（虛擬 peer 投遞）已由另一個 session 接手。

### Feat: tmux 重啟後的 pane 依 session 名稱自動重新掛回（#1002）

同一個 daemon 開兩個 Purdex client：tmux server 重啟後在 A 按 Rebuild 重建了 session，B 上同一個分頁卻永遠停在「tmux restarted」面板——它的 pane 還綁著死掉的 `(code, tmux_instance)`，而已終止的 pane 不會再被對帳。現在 `tmux-restarted` 的 pane 只要同一 host 上有一個活的 session 名字跟它最後看到的一樣，就自己重新 attach：`decideRevive`（純函式：名字精確相符、live `tmux_instance` 非空、`mode` 存在但非 terminal 就拒）、`reviveAllowed`（對 rebuild operation store 的逐 pane 閘門）、`runRevivePass`（rebuild 鎖持有中整批跳過；host 的 attach gate 要開）。兩個觸發點：`sessions` 事件對帳＋開閘之後、probe 之前跑一次（同一個 payload 標死的 pane 在同一事件內就復活，離線重連的情境）；rebuild 鎖釋放時對每個 host 再跑一次。只處理 `tmux-restarted`，不碰 `session-closed`。純 SPA，daemon 無變更。

## [1.0.0-alpha.336] - 2026-09-14

### Feat: App 在自己所在的機器上安裝／啟動／更新 pdx daemon——Electron + SPA 側（#997）

alpha.335 給了 daemon 交叉編譯下載端點與建置身分，這是第二段：Electron 主程序拿到那個 binary、放到 `~/.config/pdx/bin/pdx`、寫初始設定、啟動、驗證，然後 SPA 把新 daemon 自動登記成一台 host。目標機器是沒有 repo、沒有 Go toolchain 的 air-2026；Mini 上的 App 看到的是 repo 那顆 daemon，會被判成 `external`，不提供任何破壞性操作（spec D8）。spec 在 `docs/specs/2026-09-14-local-daemon-install-spec.md` §3–§4，plan 在 `…-plan-b.md`。

**所有 I/O 都經 `deps` 注入，邏輯是純的。** `electron/local-daemon/index.ts` 的 `createLocalDaemon(deps)` 只認識 `deps.exec / fs / fetch / probePort / kill0 / networkInterfaces / now / sleep`，`node-deps.ts` 才是真的 Node 實作。這讓 132 個 vitest 能用一個 in-memory fs 加一個會寫 pid、開 listener、翻 health 的假 `pdx start/stop` 把整條安裝序列跑完——step 順序、config 位元組與 0600、launch env、exec 順序、安裝到一半 ownership 被外人搶走、settle 預算、lsof 逾時與根本沒跑、與 App 更新在同一把鎖上交錯——而不是斷言 mock 有沒有被呼叫。

**ownership 是 lsof 的事，pid 檔只是候選。** pid 檔現在是永久的（alpha.335），重開機後那個數字可以落到任何程序上，所以 `status()` 從不相信它：`process.kill(pid, 0)` 活著之後還要 `lsof -p <pid> -d txt -F0` 讓 `txt` 的 realpath 等於 `realpath(binPath)` 才算「我方 alive」；再用 `lsof -iTCP:<port> -sTCP:LISTEN` 找出誰在我們的 endpoint 上聽，任何不是 alive.pid 的 listener 都是 `external`。每次 lsof 5 秒、逾時／spawn 失敗／exit code 不是 0 或 1 一律 `external` 而不是「沒有 process」——codex 三路 review 有兩路獨立抓到後兩種原本會被當成空結果放行。lsof 路徑依平台選（macOS `/usr/sbin`、Linux `/usr/bin`）。停機前**再查一次** ownership，幾分鐘前的 status 從不拿來做破壞性決定。

**安裝序列把「還沒停舊 daemon」的失敗和「已經停了」分開。** prepare → download（bearer、6 分鐘、串流到 `pdx.new`、等 stream `close` 後比 `Content-Length` 和 `X-Pdx-Sha256`、只收 200）→ verify（`pdx.new version --json`，goos/goarch/hash 要對得上 header，JSON 不是物件也算不能跑）→ configure（只在沒有 config 時寫，先刪殘留的 `.tmp` 再以 0600 寫、rename；bind 取唯一的 Tailscale `100.64/10` + `utun` 介面，不唯一就 `127.0.0.1` 並帶 `bindNote`）→ stop（只在 alive 時；`pdx stop` 35 秒，然後等到 `kill(pid,0)` 失敗**且** port 明確 `ECONNREFUSED`，最多再 5 秒）→ swap → start（`pdx start` 70 秒，之後 `/api/health` 的 hash 必須等於磁碟 binary 的 hash，不然是「port 被別的東西佔了」）→ register。verify 之前的任何失敗都不動舊 daemon、舊 binary；`pdx.new` 在每條失敗路徑都會被清掉。防守方 review 抓到 `probePort` 原本把 timeout 和任何 socket error 都當成「已拒絕」，Tailscale 介面暫時不通就能提早 swap——現在是 `open | refused | unknown` 三態，`unknown` 繼續等到預算用完然後拒絕。

**`pdx` 從 Finder 啟動時要有 PATH。** Finder 給的 PATH 沒有 Homebrew 的 `tmux`，daemon 會健康但每個 tmux 呼叫都失敗。`launch-env.ts` 用 `$SHELL -ilc` 印一段夾在隨機 sentinel 之間的 `$PATH`（`-ilc` 才拿得到 `.zshrc` 裡的 nvm 與 `~/.local/bin`），失敗退到 `-lc`，再退到 `process.env.PATH` 前綴 Homebrew 路徑；argv 裡沒有 NUL。`status().tools.tmux` 為 null 時 UI 只警告不阻擋。

**一把鎖管所有事。** `status / install / start / restart / ensureRunning` 全走同一條 promise queue，`main.ts` 也把既有的 `applyUpdate`（App 自我更新）整段包進 `localDaemon.withLock`，所以 App 更新和 daemon 操作互斥到結束而不只是開頭。兩個因此浮出的死鎖都在 review 中修掉：`openWrite().close()` 在寫入已經失敗後永遠 pending（stream 的 `error/close` 早就發過、listener 掛太晚，用真實 fs 重現後改成建立時就追蹤狀態），以及 `applyUpdate` 的下載沒有逾時、來源停在 headers 之後就能永久持鎖（加 10 分鐘 `AbortSignal.timeout` 給 fetch 與 pipeline）。`ensureRunning` 在 App ready 時跑一次：managed 且沒 alive 就 `start()`，3 次、間隔 5 秒，永不 reject。

**Electron dev mode 預設開啟（D6）。** 與 Go 側同步，`PDX_DEV_MODE` 只有等於 `'0'` 才關；`main.ts` 啟動時設預設值，所有閘門讀 `!== '0'`。`smol-toml` 放 root `devDependencies` 讓 electron-vite 把它內聯進 `out/main`（dev update 只送 `out/`），驗證方式是 bundle 裡沒有對它的 `import/require`。

**SPA。** `useHostStore.registerLocalHost` 是建在 `addHost/updateHost` 上的冪等 helper：同 ip:port 已存在就回它的 id、只在 token 為空時補；`LocalDaemonSection` 掛在 Settings → Development 的 Daemon 區塊下，Install / Start / Update（磁碟 hash ≠ 最新 hash）**否則** Restart（磁碟 hash ≠ 執行中 hash），external 顯示原因、無破壞性按鈕；web 版 render null。en / zh-TW 各 27 個 `settings.dev.local.*` key。

**流程**：subagent TDD 九個 task 各自 review（一個 Important 是 controller 給 reviewer 的 constraint 寫過頭，裁定 plan 為準）、final whole-branch review 兩個 Important 一波修正、codex R1 一個 P2 加三視角 adversarial（攻擊 P1+2 P2、防守 P1+P2、體質 P2）七項修正加 scoped re-review 乾淨。延後追蹤：#999（`pdx stop` 靠 pid 檔 flock 決定對象、不綁定我們驗證過的 pid——視窗極窄且修法偏離 spec，先記錄）、#1000（spec-drift 清單：`ensureRunning` 重試不重驗 ownership、unhealthy 時 Update vs Restart、reason 字串未 i18n、UI 狀態）、#1001（體質：切 `download.ts`、測試分檔、adapter 測試、命名）。**尚未在 air-2026 實機驗收**（spec §4 七條列在 PR 描述），Electron 需重新打包後由 Air 走 dev update 拉取。

## [1.0.0-alpha.335] - 2026-09-14

### Feat: daemon 端跨機安裝基礎——交叉編譯下載端點、建置身分、dev mode 預設開啟（#993）

新機 air-2026 要跑 Purdex，但不打算放 repo、也不裝 Go toolchain，所以「daemon 怎麼更新」這個問題沒有答案：既有的 `/api/dev/daemon/rebuild` 是同機 `git log` + `go build` + exec self，離開有 checkout 的 Mini 就不成立。決定是**讓 App 在自己所在的機器上安裝／更新 daemon**——App 本來就持有每台 host 的 token，不需要新增 daemon 之間的信任關係。這是兩段的第一段（Go 側），Electron/SPA 側在下一個 PR；spec 在 `docs/specs/2026-09-14-local-daemon-install-spec.md`，§1 的九個決定是固定的。

**`GET /api/dev/daemon/download?goos=&goarch=`。** Mini daemon 依請求交叉編譯（`CGO_ENABLED=0`，modernc sqlite 是純 Go 所以不依賴 host 的 C toolchain），快取在 `bin/dist/pdx-<goos>-<goarch>-<hash>`，同 target 只留一份。整個請求——包含 cache hit 的傳輸——都持有 `daemonRebuildMu`，這樣 `/rebuild` 的 `syscall.Exec` 不會在傳輸中途把 server 換掉；忙碌回 409。身分只取一次：同一個 `hash` 同時是快取鍵、ldflags 值和 `X-Pdx-Hash` header，handler 從不重讀 HEAD。回應帶 `X-Pdx-Sha256`（整個 artifact 的雜湊，即使是 206 也一樣）與精確的 `Content-Length`，客戶端只驗完整的 200 回應。時限從進入 handler 起算 6 分鐘（context 加 `SetWriteDeadline`，因為 server 沒有全域 `WriteTimeout`，慢讀取者否則能無限占鎖），建置本身另有 5 分鐘子 context。

**取消建置要殺整個 process group。** 攻擊方 review 抓到 `CommandContext` 只殺 `go`，`compile`/`link` 子行程會繼續吃 CPU，而且它們繼承的 pipe 會讓 handler（和 mutex）卡到它們結束。修法是 `Setpgid` + `cmd.Cancel` 對 `-pgid` 送 SIGKILL，保留 `WaitDelay` 當 pipe 保險——順帶把 `buildBinary` 從 `StdoutPipe` 改成 `io.Writer`，因為 `WaitDelay` 只會強制關閉 Go 自己建立的 pipe（對照 go1.26 `os/exec` 原始碼確認）。新的 `lineWriter` 有 1 MiB 的單行上限，不會被無換行輸出撐爆。

**建置身分搬到 `internal/buildinfo`。** `dev.BakedInHash` 和 `core.Version`（一直是 `"dev"`）合併成一個 leaf package 的 `Hash`/`Version`，Makefile 兩個都注入；`/api/health` 現在帶 `version` 與 `hash`（執行中 daemon 的身分），`/api/info.purdex_version` 不再是 `"dev"`。新增 `pdx version [--json]` 讓 App 不啟動 daemon 也能讀磁碟上 binary 的身分。

**dev mode 預設開啟。** Purdex 只有一個使用者，每次帶 `PDX_DEV_MODE=1` 啟動只是摩擦。`internal/devmode.Enabled()` 取代兩處 `== "1"` 的讀取：unset 或任何非 `"0"` 的值都是開；config 的 `dev.update` 仍是第一層閘。⚠️ 部署注意：這會讓 `internal/module/agent` 裡 23 個原本只在 dev 模式輸出的 log 點（`[hook]`、`[derive]`、`[probe]`…）在每台既有部署上打開，`~/.config/pdx/logs/pdx.log` 沒有 rotation；要回到原本音量就在 launcher 設 `PDX_DEV_MODE=0`。

**pid file 改為永久。** `releasePidLock` 原本 unlock 後 unlink，任何順序都留一個窗口讓正在啟動的 `serve` 鎖到一個路徑上已經不存在的 inode，之後 `pdx status`/`stop` 會對活著的 daemon 說「沒在跑」。現在只 unlock + close（`stop` 的 SIGKILL 分支也不再刪檔），`isDaemonRunning` 本來就靠 flock 狀態判斷所以行為不變。同時 `serve` 遇到鎖被持有時**直接退出**而不是印警告繼續——兩個 daemon 共用一個 `data_dir` 等於共用 SQLite 檔案，從來就不安全。防守方 review 再抓到探測本身用 `LOCK_EX` 會撞到啟動中的 `serve`，改成 `LOCK_SH` 探測加 5×50 ms 的有界重試。

**流程**：spec 三輪 codex（1+1+0 Blocker、10+6+5 Important，處置表在 spec §6–§8）、plan 兩輪（1 Blocker、7+1 Important）、subagent TDD 七個 task 各自 review、final whole-branch review 一波七項修正、PR 標準 review 乾淨加三視角 adversarial 五項修正。延後追蹤：#994（三份 `git log -1` 集中、`lineWriter` 尾行、註解理由）。既有的 `TestConsumeSignals_GraceWindowDrop_RearmsAfterTeardown` 只在 `-race` 下失敗，與本 PR 無關。

## [1.0.0-alpha.334] - 2026-09-14

### Feat: 跨主機 agent peer 橋接第一階段——本機 peer 盤點 `pdx peers`（#991）

Claude Code 2.1.224 起 session 之間可以互傳訊息（`ListAgents` / `SendMessage`），但它的發現邏輯編在 binary 裡、沒有擴充點：同機靠 `~/.claude/sessions/<pid>.json` 加 `/tmp/cc-socks/<pid>.sock`，跨機只有 Remote Control 一條路（經 Anthropic、只列有進 RC 的 session、機器取的名字被隱藏、Codex 完全不在裡面）。新機 air-2026 進了 tailnet 之後，「我的 Purdex 主機上現在有哪些 peer」這個問題沒有答案。這是三階段（盤點 → 跨主機 fan-out → 投遞）的第一階段，spec 在 `docs/specs/2026-09-13-peer-bridge-spec.md`。

**先實測再設計。** 動手前用一支 proxy 腳本對 Claude Code 的 inbox socket 做了六組實驗（spec §3），確認了幾件文件沒寫的事：frame 必須是 SDK 的 `type:"user"` NDJSON、`from` 一定要 `uds:` 前綴才會掛回信地址、wrapper 裡的 `from-mode` 是寄件方**自報**的、任何程序只要綁 socket 加寫兩個登記檔就會出現在 `ListAgents` 而且對方能原生回信（「虛擬 peer」）。這些是第三階段的基礎，也是這次 spec 被兩輪 codex review 砍掉 tmux `send-keys` 投遞通道的原因——typed input 帶使用者權限，沒有 harness 對 peer 訊息的那層限制，一條會「安靜降級」的橋等於把 peer 憑證換成使用者憑證。

**join 的規則寫死在 sessionId 上，沒有 pane fallback。** daemon 本來就知道哪個 agent 擁有哪個 tmux session（Tab Rebuild 的 owner resolver），Claude Code 的登記檔也有 `sessionId`，所以一個 session 列的 agent 就是「owner 的 sessionId 對得上的那個 live 登記檔」。同一個 sessionId 有多個 live 程序（`--resume` 後舊程序還沒被清）時，只有 pane id 精確相符且唯一那筆才採用，否則整列標 `ambiguous` 而不是任選；而一個登記檔最多只能被一個 session 列採用——review 抓到 pane 在單次請求中途搬家可以讓兩列同時宣告同一個 inbox，之後 `cc:<name>` 位址就會誤判成 ambiguous。liveness 比對的是 instant 不是 wall clock：登記檔的 `procStart` 是 UTC ctime、`ps lstart` 是本地時間，`Truncate(Second).Equal` 才不會被時區騙。

**owner 查不到和查失敗是兩回事。** 原本的 resolver 介面只有 `(PaneOwner, bool)`，tmux 讀取錯誤、5 秒逾時、context 取消全部塌成「沒有 owner」，一個正在跑 Claude Code 的 session 會在那一瞬間被列成純 shell、而且 `partial` 還是 false。三份 review 各自獨立點名這件事。現在介面帶 `error`，查失敗的列是 `agent:null, reason:""` 加 `partial:true`，只有「查成功但真的沒 agent」才是 `no_agent`。收斂那輪又抓到最後一個 pane 逾時會被 `panesOfSession` 吞掉的路徑，一併補上。

**`/api/peers` 從第一天就不能是開的。** 既有的 `TokenAuth` 在 admin token 為空時放行所有路由、也吃一次性 ticket；peer 路由是給遠端 daemon 打的，這兩件事都不能繼承。`PeerRouteAuth` 掛在 `TokenAuth` 外層只管 `/api/peers` 前綴：非空 admin bearer、不看 ticket、空 token 一律 401；其他路由一個位元都沒動。第二階段的 per-host `inbound_token` 會加在同一個 wrapper 上，review 提醒屆時要讓通過 scoped auth 的請求繞過內層 `TokenAuth` 而不是只加分支。

**攻擊方 review 修了兩個資源邊界。** 登記檔目錄是同 uid 可寫的，一個 `1.json -> /dev/zero` 的 symlink 或一支 FIFO 就能讓 daemon 無限讀或永久卡住——現在用 `O_NOFOLLOW|O_NONBLOCK` 開、fstat 確認是普通檔、64 KiB 上限。CLI 也改成先看狀態碼再讀 body，非 200 只取 4 KiB 摘要，200 的 body 超過 16 MiB 直接拒絕。

**tmux 世代跨越請求就整包作廢。** 盤點分兩步（列 session、逐一問 owner），tmux 在中間重啟並重用 session id 會把新 session 的 agent 併到舊 session 的名字底下；跟既有 provenance handler 一樣前後各取一次 `TmuxInstance`，不同就回 `ok:false`。

**真機驗收**：mlab 上 12 個 tmux session、9 個 Claude Code 全部 `deliverable yes`、peer name / pid / inbox / status / version 正確，1.0 秒內完成（2 秒 soft budget，`partial:false`）；`curl /api/peers` 無 bearer 401、帶 `?ticket=` 401、帶 bearer 200。

**流程**：spec 兩輪 codex（2+1 Blocker、10+6 Major，全部採納，處置表在 spec §8/§9）、plan 一輪（1 Blocker、6 Major、5 Minor）、subagent TDD 八個 task 各自 review、final whole-branch review 四項修正、PR 標準加三視角 adversarial review 六項修正、收斂一項。延後追蹤：#988（已由本 PR 關閉）、#989（liveness 每個 entry 四次 `ps` fork 吃預算）、#990（雜項）。

### Fix: 深色主題的 success 顏色改成看得見的文字色（#985）

`--status-success` 原本是 `#2a4a3a`——一個填色、不是前景色，而所有用到它的地方都是文字。在 elevated 底色上對比 1.63，連大字的 3:1 門檻都不到，所以 #977 加的「Resolves to cld-yolo」判定字在深色主題下是字面意義的看不見。改成 `#7ec699`，三種底色上 7.95 / 9.73 / 9.10，落在 warning 旁邊。

### Fix: 探測範本時跳過開頭的變數指派（#987）

`OPENCODE_YOLO=true opencode -s {id}` 被回報成「login shell 找不到」。範本沒錯，是 probe 拿第一個空白分隔 token 去解析 `OPENCODE_YOLO=true`——那不是指令，永遠解析不到。POSIX simple command 前面可以接變數指派，*command word* 是指派之後的那個；#977 的 spec 就是這樣寫的，這是實作漏掉自己的契約。`NAME=` 是全部的判斷條件（值裡有 `=` 也無妨；flag 與絕對路徑不可能被誤判），全是指派的範本沒有 command word，Test 保持關閉。

## [1.0.0-alpha.333] - 2026-09-08

### Feat: 分頁自己去問「這個 pane 是誰的」，resume 指令變成可設定的範本（#977）

alpha.332 之後留下兩個洞。一是**部署前就開著的 session 重建只會得到純 shell**——紀錄的 agent 只由合格的 `SessionStart` 寫入，而那些 session 的 `SessionStart` 早在新程式上線前就發生過了，使用者實際看到那個空欄位。二是組出來的 `claude --resume <id>` **對他是叫錯執行檔**——他用的是一個 zsh function（會補上 `--dangerously-skip-permissions` 與 iTerm2 狀態指示），所以重建會 resume 到對的對話、卻用錯的程式啟動。

**第一版設計整個被推翻，而且推翻得對。** 原本是 daemon 在任何 owner 事件上主動掛第二個 envelope，並節流成「每個 frame 一次」。review 指出兩個缺陷，兩個都是「推」這個形式本身的問題，不是節流參數調不好：廣播出去時使用者往往還沒開那個分頁（他會先開 Purdex 再開 tab），那一次資格就這樣消耗掉了；而「只填不覆蓋」的寫入無法被後續同樣「只填不覆蓋」的寫入更正，所以一旦寫錯就定型。改成**由 SPA 主動查詢**之後，兩者同時消失——答案是給發問者的 response，不會沒人接；而且隨時可以再問。節流機制、arming map、混版風險也一併不需要了。

**ownership 判定沒有動。** 仍然是 frame layer 的 ancestry walk，從 `classifyAncestor` 抽成共用函式而非另寫一套，外加每個被接受的事件本來就會通過的 pane process tree 檢查。`agent_frames` 補上它從來沒存過的 session id，由**任何** own-frame hook 事件透過專屬的窄 UPDATE 寫入——那個 UPDATE 不參與任何 read-modify-write，所以 proxy attach 的 retry loop 不會把它洗掉。

**`resolvePanePID` 修的是一個既有的 bug，而且專案自己記錄過。** `PanePID` 走 `tmux list-panes -t <target>`，tmux 會把 pane id 解析成它所屬的 window，於是回傳的是**第一個** pane 的 PID。`liveness.go` 的註解在 PR #638 就寫清楚過並改用 `ActivePanePID`，但 `verify.go` 沒跟上——所以分割視窗裡非第一個 pane 的 hook 一直被判成 `pid_not_in_pane_tree` 而拒絕，那些 pane 根本不會有 frame。這是本功能的必要前提（查詢要求走訪途中看到 panePID），也意味著那些 pane 從此會開始產生先前不存在的 frame。

**shell probe 只驗指令的第一個 token，而且必須在互動 login shell 裡驗。** 一個 shell function 對任何非互動 shell 都是不存在的——那正是這個功能要解決的情境。`type` 的輸出在 zsh 與 bash 不同（bash 還會印出整個函式本體），`command -v` 的輸出也不是「路徑或單字」（alias 會印出自己的定義、PATH 上的相對路徑會印相對路徑），所以 API **不宣稱找到的是哪一類東西**，只回報解析成功與 shell 實際印出什麼。

**五輪 review、24 項發現、23 修 1 延後。** 其中四項是 plan 本身寫錯——`op.created` 這個符號不存在、空指令是關掉 resume 步驟而不是排除整個 pane、`field` 的同值提早返回會讓「確認同一個目錄」標不上使用者來源、`Executor` 根本沒有列舉 session panes 的能力。這些都在進到程式碼之前就被攔下來。另外四項是**測試綠了卻沒驗到東西**，每一項都靠變異測試才確認：deadline 只在一開始就過期時測、partial slice 恰好是空的所以分辨不出有沒有丟棄；disowned 的測試從未建立 deferred timer；單一 pane 的取消測試分辨不出「回傳部分結果」與「整個丟棄」。

**兩個實測推翻了 review 的診斷。** bash job control 那項，第二輪說「`-i` 下沒有 `m`、要靠 rc 裡的 `set -m` 才會逃出 group」，第三輪量到相反結果。最後用一支複製 probe 實際 fd 配置的 Go harness 量了七種組合才釐清：決定性變因是 **bash exec 時是否已是 process-group leader**（`Setsid` 與 `Setpgid` 都會讓它是），而且**在 bash 還在 source 啟動檔期間啟動的背景 job，不論 `$-` 有沒有 `m` 都會留在 shell 自己的 group**。兩邊的結論都對、理由都錯，註解現在三個事實都寫進去。

清理仍有做不到的邊界，並且寫在 spec 裡而不是宣稱解決：自行呼叫 `setsid()` 的 descendant、`ps` 快照之後才 fork 的程序、`Getsid` 到 `Kill` 之間被縮小但未關閉的 PID reuse 窗口。

**已知限制**：部署後那個 session 必須**至少送出一次 hook**，daemon 才知道它的 session id——還在用的 session 下次互動就會補齊，之後完全沒再碰過的則維持純 shell（但有 cwd）。範本是所有 host 共用的，只有測試是選 host 的。probe 近似 pane 的 shell，不是重現它（沒有 tty、不跑 tmux 的 `default-command`）。

延後追蹤：#978（walker 把讀取失敗與「不是 root」混為同一個 verdict）、#979（`PanePID` 其餘呼叫者稽核）、#980（清空範本會安靜退化成純 shell）、#981（SPA 既有 flake）、#983（rebuild 規則堆積在 `useTabStore`）。

## [1.0.0-alpha.332] - 2026-09-07

### Feat: 每個分頁記住自己怎麼被重建（#970）

使用者的需求：主機重開之後 tmux session 全滅，希望每個崩掉的分頁能「一鍵還原」——記住自己的 pwd、tmux id、跑的是 claude/codex/opencode，以及對應的 resume 指令。

**先量再寫。** 三家 agent 的 hook payload 全部實地觀測，沒有一項靠推論：cc 與 codex 的 `SessionStart` 從 `agent_trace_steps` 逐字撈出，兩者都帶 `session_id` + `cwd`；opencode 的則是實跑一次才拿到——它**只**帶 `session_id`，所以 `cwd` 只能從 plugin 的 `PluginInput` 取，而且 `session.created` 只在第一次送出 prompt 時才發，TUI 啟動不發。六個 resume flag 全部用 `--help` 驗過。

**判定「這個 SessionStart 屬不屬於這個 pane」花掉最多來回。** spec 三輪 review：第一輪要求把判定從 frame layer 移到 process ancestry（因為同型巢狀 `cc` 裡開 `cc` 不走 proxy 收摺），第二輪指出那會踩到 launcher 反例——`codex.js` 用 `spawn` 不是 exec-replace，node launcher 全程是 native codex 的父程序，而 `codex.Identify` 對 argv 含 `/codex/` 的 JS runtime 回 true，所以**每一個 npm 裝的 codex 都會被判成巢狀在自己裡面**，永遠不記錄。實測確認後 v3 退回 frame layer：frame 只由 hook sender 建立，launcher 從不送 hook 所以永遠沒有 frame——「要求祖先有 frame」正是讓判定安全的那個條件。最終只把既有走訪那個被過載的 `(nil, nil)` 拆成四態，並把 envelope 掛在**變更結果**而非預先判定上（`applyFrameEvent` 在 Upsert 之後還有第二次 proxy 收摺會撤銷 root 判定）。

實測數據也顯示原本擔心的 subagent 情形在事件層根本不會發生：cc `PdxSubagentStart` 21 次 vs `PdxSessionStart` 16 次、codex 9 vs 270，subagent 走獨立事件；跨型巢狀已被既有 proxy 收摺擋掉 45 次。真正的洞只有同型巢狀一種。

**世代（tmux server identity）貫穿整條路徑。** session code 是 tmux `$N` 的可逆編碼，重開機後 `$0` 會鑄出同一個 code，所以 pane 可能靜默接到陌生 session。`tmux_instance` 現在蓋在每個 `Session`、每個 provenance envelope 上，並**併入 watcher 的 hash**——否則「重啟後重建出完全相同的清單」不會觸發廣播，SPA 會永遠停在舊世代。`tmuxInstance` 這個宣告已久卻從無人寫入的欄位，同時被補實。

**PR 四輪 review、20 項發現、19 修 1 延後。** 反覆出現的其實是同一條規則的兩個方向：**沒有證據就不動作**——世代不明時不宣告 pane 已死（避免誤殺），世代不明時也不授權寫入或送指令（避免誤傷）。前三輪的每個洞都是把前者的「不判死」誤讀成後者的「可執行」：cwd probe 用**問的時候**的世代寫入回應值、retry 拿 SPA 快取當授權、`created.tmux_instance` 為空時直接省略前置條件。修法是給 daemon 兩個前置條件（`/cwd` 回傳與 cwd 同時採樣的世代、`send-keys` 接受 `expected_tmux_instance` 不符回 409），並讓 `/cwd` 在 pane 讀取的**前後兩側**各採樣一次、不一致就回空——`GetSession` 原本是在 pane 讀取之前蓋章，中間重啟會把新伺服器的 cwd 配上舊戳記交出去。

**送鍵的檢查與送出必須是同一次 tmux 呼叫。** 分成兩次呼叫的任何做法都留有窗口，再採樣一次也沒用，因為 `SendKeysRaw` 會重新連線並按名稱解析目標。改用 `if-shell -F` 讓**收到按鍵的那台 server 自己評估條件**：server 若重啟，指令連到的是新 server，`#{pid}:#{start_time}` 不符即走 else 分支。按鍵以 hex 送出（巢狀指令字串會被 tmux 二次解析）、目標用 session id 而非名稱、sentinel 是唯一判決通道（`if-shell` 兩分支都 exit 0）。對真 server 實測過相符與不符兩路。

**入口三處**：崩掉的 pane 上三列可勾可改的動作集 + 一顆 Rebuild；double-click 分頁名稱展開同樣三項（活著的 session 也能看能改，split tab 每個 pane 一塊）；Settings → Snapshot 的批次表，依 `(hostId, tmuxInstance, sessionCode)` 分組，兩個 pane 指向同一個死 session 只會建一個。舊的 snapshot capture／layout restore／undo **保留**——它們是另一件事，能力邊界寫在 spec §4.11：legacy 動作只開 shell，永遠不執行 agent 指令。

**行為變更**（非功能本身、但你會看到）：原本靜默接錯 session 的 pane 現在會正確顯示成 terminated；opencode plugin template 改了而 `CheckHooks` 是 byte-compare，已安裝的 plugin 會判定 drift 需重裝；terminal attach 現在要等該連線的第一份 session payload 才建立。另外修掉一個既有 transport bug：`connect()` 在 ticket 飛行中提前返回，導致 `reconnectWithTicket()` 存進的新 ticket 從未被使用、舊 ticket 反而開了 socket。

延後：`SnapshotSettingsSection` 拆檔（#975）。既有缺陷另開 #971 / #972 / #973 / #974。

**尚未做**：重開機路徑無法單元測試，plan 末尾有 6 步手動驗證清單，需要在 Air 上真的 `tmux kill-server` 跑一次。


## [1.0.0-alpha.331] - 2026-09-07

### Refactor(store): trace store 依職責拆分（#968）

純重構、無行為變更。清掉 #962 的 file-health review 留下的三項體質債（#963 / #964 / #965）。

**原始碼**：`internal/store/trace.go` 1,195 行同時承載型別、schema DDL、migration、legacy rebuild、CRUD、保留期清理、去重規劃、cursor 編解碼與 JSON helper，拆成四檔——`trace.go`（89，型別與介面）、`trace_migration.go`（550）、`trace_write.go`（358）、`trace_read.go`（247）。pruning 放 write 而非 migration，因為它在 `SaveChain` 的同一交易內；`rawJSONText` 放 write，因為只有寫入路徑用它。同 package，不新增 service 層或 interface。順帶刪除 `firstNonEmpty`——整個 repo 沒有任何呼叫者。

**測試**：兩檔重整成五檔，**fixture 依實際呼叫者分配而非名稱前綴**。單一主題專用的跟著主題走（`seedLegacy*` / `seedIntermediate*` 全部只服務 migration），只有真正跨檔共用的才進 helpers，因此 helpers 收斂到 144 行。restart 測試歸 migration，理由是它釘的正是「重跑 migration 不得破壞已去重資料」。

**`planTraceRootDedup`**：回傳型別從 `(string, []bool)` 改為 `tracePayloadPlan{RootPayload, Steps []storedTracePayload}`。這消除重複的 `rawJSONText` 轉換並把 payload 與旗標綁在一起，但**沒有消除位置耦合**（codex 指正我原本的宣稱）——`SaveChain` 仍以 `steps[i]` 配 `plan.Steps[i]`，該契約改為明訂：等長同順序、不得單獨重排、`RootPayload` 不可留 struct 零值、candidate 需在清空任何項目前捕獲。

**怎麼證明是純搬移**：行數持平證明不了什麼——SQL、斷言或條件都可能在總行數不變下被改掉。改用逐宣告內容比對：從 `origin/main` 與工作樹各自抽出所有頂層宣告依名稱配對，**101 個宣告位元組完全相同**，差異只有五項預期變更。測試名稱清單前後一致，75 個無遺失。

Codex 兩輪 review 皆無阻擋性發現。對抗性額外抓到一個歸屬瑕疵並已修：`assertRawShape` 在共用 helpers 卻依賴被我放進 dedup 檔的 `readRawTraceRoot`（成因是我 grep 時排除了 helpers 檔本身，漏看這個呼叫者）。

## [1.0.0-alpha.330] - 2026-09-07

### Perf(store): agent trace 的 root payload 每條 chain 只存一次（#962）

使用者問「這些 event 資料有長期／短期保留的必要嗎」，查下去發現問錯的不是保留期，是每筆存了什麼。

`agent_trace_steps` 每列存三個 JSON blob。live 資料庫實測（10,000 chains / 39,554 steps / 396 MB）：`payload_json` 總計 **324.9 MB**（平均 8.2 KB、單筆最大 **682 KB**），而 `before_json` + `after_json` 加起來才 7.2 MB。也就是 `payload_json` 幾乎就是整個資料庫。

而其中 **73% 是重複**：去重後（`DISTINCT chain_id + payload_json`）只有 86.3 MB。根因是 `trigger` / `verify` / `frame` / `projection` 四個 step 都把整個 `req`（`EventRequest`）當自己的 payload，四份位元組完全相同；`PdxPostToolUse` 特別肥是因為 payload 帶著 tool result，可能是整個檔案的內容。

**保留機制本來就有**（`pruneTraceChains` 上限 10,000 chains / 100,000 steps，表卡在 10,000 就是它在運作，涵蓋約 32 小時），所以這輪不動保留期。

- `agent_trace_chains` 加 `root_payload_json`、`agent_trace_steps` 加 `payload_is_root` 旗標，共用的 root payload 每條 chain 只存一次。
- 以 `rawJSONText` 的輸出（stored form）比對，**只有 bytes 相等才合併**；語意相同但 bytes 不同的保留各自副本，只降低去重率、不影響正確性。
- 讀取用 **JOIN + `CASE WHEN`** 在 SQL 內解析，而非分兩次查詢在 Go 裡拼——後者會讓交錯的 `SaveChain` 把 root B 的 chain row 配上 root A 的 step rows，靜默回傳錯的 payload。**API 輸出位元組完全不變**，SPA 不需改動。
- 新欄位**刻意不列入** `needsChainRebuild` / `needsStepRebuild` 的 required 清單：列進去會讓現行 schema 全部走 legacy rebuild，而該路徑的 copier 讀現行表沒有的 `created_at` / `agent_type`，migration 會直接失敗。兩個 regression 測試釘住這件事。
- 既有資料不 backfill，靠 pruning 汰換。

24 個新測試全程 TDD。涵蓋部分去重 `[A,B,B]` / `[A,A,B,B]`、位元組保真、亂序輸入與 `Seq` tie-break、四種空值慣例、re-save 轉換、step INSERT 失敗的 rollback、migration 矩陣、重啟後已去重資料仍可讀、混合新舊 chain 的 pruning。交錯讀寫用 `sqlQuerier` 接縫做成決定性測試，並**實作過錯誤版本確認它真的會紅**。

Codex 四輪 review（標準 + 攻擊／防守／體質三平行）全部 approve 無實質發現。體質提出的註解措辭問題已修；拆檔、測試重整、`planTraceRootDedup` 回傳型別三項延後至 #963 / #964 / #965。

**⚠️ 部署限制**：這是磁碟格式變更，**舊版 daemon 不能讀已被此版本寫過的資料庫**——舊 reader 只讀 `payload_json`，會把去重過的 row 讀成空 payload，靜默不報錯。回滾只能用仍懂新格式的版本，或還原升級前備份。

延後項目見 #957（payload 截斷：768 列 >64 KB 佔 241 MB，但那是有損的）。

## [1.0.0-alpha.329] - 2026-09-07

### Fix(daemon): `pdx start` 健康檢查窗口修復（#960）

開機後 purdex 沒起來，表面像是重開機造成的，實際上跟重開機無關 —— 是既有問題被資料庫大小推過了臨界點。

`pdx start` spawn 出 `pdx serve` 之後，只用固定的 `500ms + 5×200ms ≈ 1.5s` 探測 `/api/health`，探不到就 SIGKILL 子行程並 exit 1。而 `agent_events.db` 已經長到 1.1 GB，冷開機時檔案不在 page cache，光是 SQLite 開檔就超過這個窗口 —— **daemon 被自己的啟動指令殺掉**，只留下一個指向死 pid 的 `pdx.pid`。因為是 SIGKILL，沒有 panic 也沒有 crash report，看起來像是無聲不啟動。

這個窗口是個跟「啟動實際需要多久」無關的固定預算，資料庫越大越容易踩到。

- 抽出可測試的 `waitForHealthy()`：窗口 1.5s → **60s**，200ms 輪詢。
- 用一條跑 `cmd.Wait()` 的 goroutine 偵測子行程提早退出 —— 真的啟動失敗時立刻回報，不會白等滿 60 秒（沒有這層，延長窗口只會讓失敗變慢）。
- 整個等待綁在一個 `context.WithDeadline` 上，probe 改用 `http.NewRequestWithContext`；每次 probe 前先檢查 deadline，單次上限取 `min(5*interval, 剩餘時間)`，沒有任何一次 probe 能活過窗口。
- 接受 200 之前再複查 `childExited` 與 deadline —— 子行程送出 200 後隨即崩潰、或 200 落在窗口外，都不會被誤判成啟動成功。
- 失敗訊息區分「子行程退出」與「窗口耗盡」，取代原本含糊的 `health check failed, killing child`；等超過 3 秒印進度行。
- 探測改用帶 timeout 的 request（原本 `http.Get` 無 timeout，daemon 接了連線但 hang 住會讓整個窗口失效），並移除無條件的 500ms sleep。

端對端實測：子行程 bind 失敗即死，舊版 1.535s + 含糊訊息 → 新版 0.47s + 指出根因；正常啟動 0.21s（比舊版快）。

Codex 三輪 review（標準 → 對抗性 → 複查），對抗性攔下兩條：接受 200 前未複查子行程退出、deadline 到了仍發動完整長度 probe（最壞總耗時可逼近 `2×timeout`，且窗口外的 200 會被判成功）。

同時手動 `VACUUM` 把 `agent_events.db` 從 1.1 GB 縮到 389 MB（其中 730 MB 是舊 FK leak 刪除後未回收的 free pages）。成長來源未修，追蹤於 #957。

延後項目：#957（`agent_trace_steps` 無保留期限）、#958（失敗時只 Kill 子行程未殺 process group）、#959（健康檢查無法分辨 200 是否來自自己 spawn 的 daemon）。

## [1.0.0-alpha.328] - 2026-08-19

### Fix(editor/storage): in-Purdex 檔案編輯四大問題修復（#940 / #947 / #952）

使用者回報 in-Purdex 檔案編輯的四個問題，調查後拆成三個 PR 依序處理。全程 spec → codex 審 → plan → codex 審 → subagent TDD → PR 兩輪 codex review 的完整流程，三個 PR 共經 **10 次 codex review**（含 3 組 3-parallel 對抗性），攔下 **13 條會靜默毀損或誤刪使用者檔案的路徑**。

**PR-A #940 — 遠端檔案資料安全 + 近期檔案重映射 + 儲存提示**

- 「遠端檔案開起來少內容」與「所有遠端檔案都顯示有變動」是**同一個 bug 的兩面**：daemon backend 忽略 `source.hostId`（打到 active host）＋ 讀取失敗靜默開空 buffer（`lastStat` 為 null，而 dirty 燈號綁的正是含 `!lastStat` 的 `canSave`）。
- `getFsBackend` 加 resolver 層依 `source.hostId` 綁定；載入失敗改為錯誤態 + Retry，**不建立 buffer**，因此不可能存檔覆寫真實檔案；dirty 燈號改綁 `isDirty`。
- 近期檔案補 `renamePath` / `removePath`，接上 rename / move / delete / 編輯器內改名（含遠端）。
- 儲存結果 toast：已儲存 / 沒有變動需要儲存 / 儲存失敗（含原因）。
- review 攔下：host 被移除時 `getDaemonBase` 仍 fallback 到 active host（wrong-host 防線在邊界失效）、**untitled 首次儲存輸入既有檔名會直接覆寫該檔**（既有 bug）、write 成功但 stat 失敗誤報儲存失敗。

**PR-B #947 — Markdown Live Mode 無損化**

Live Mode（Tiptap）的文件模型是 markdown 的有損子集，**損壞發生在 parse 當下**，編輯一個字就會把有損結果寫回。實測修復前：表格整段變空字串、front matter 變 heading、raw HTML 被剝除、task list checkbox 消失、CRLF 轉 LF、檔尾換行被吃。

- 加入 TableKit / TaskList / TaskItem / Image 擴充，表格與 task list 現在能安全編輯（附表格浮動選單）。
- 新增 round-trip 安全閘門（marked token 白名單 default-deny + front matter / footnote / HTML entity / 混合行尾的專項偵測），無法無損表達的文件改以 raw 開啟並在工具列說明原因。
- 保留原始行尾、檔尾換行與前導空行（新增載入後不可變的 `sourceEol` / `sourceTrailingNewline` / `sourceLeadingBlankLines`）。
- 閘門依 `savedContent` 而非即時 `content` 評估，打字不會在編輯途中抽換編輯器。
- 對抗性 review 用真實 Tiptap 實測攔下 4 條靜默毀損：**圖片被吃成純文字**（白名單宣稱支援 image 但 schema 根本沒有 image node）、HTML entity 雙重 escape、混合行尾被洗成單一、純空行檔案行數折疊。另補 `image-in-mark`（`[![badge](x)](url)` 失去連結）與 `bracketed-url`（含空白的 destination）兩個 blocker。

**PR-C #952 — Storage 操作 + placeholder 自動清理**

- 每列 hover 操作（Open / Rename / Delete，作用於該列而非目前選取）、可見的批次選取（checkbox + 全選三態 + 動作列）、清除 0 B 空檔。
- 大量 0 B `Untitled-N.txt` 的成因是 New File 的 eager reservation（一按就落地真實空檔）。新增 placeholder registry 記錄「這個路徑是我們建的、使用者還沒碰過」，在最後一個引用消失時自動清除。
- 明確否決「靠 buffer 形狀推論」的原方案（會誤刪「本來有內容、被清空後存檔」的檔案）；刪除判斷基於 **post-`closePane`** 的剩餘引用，而非元件卸載（否則 pane 移動會誤刪）。
- review 攔下：registry 缺跨分頁同步（A 分頁存檔後 B 分頁會刪真實檔案）、外部變更 reload 未除籍、Clean Empty 用舊快照刪除、批次刪除只顯示數量不顯示路徑。最後加上「刪除前 re-stat 確認仍是 0 B」的結構性防線。

**順帶修掉的死 class**：`text-accent-base` 與 `bg-surface-selected` 在 Tailwind 4 沒有對應 token（build 產出 CSS 命中 0 次）—— 後者導致 **Storage 選取中的列一直以來沒有任何高亮**。

**體質**：`EditorPane.tsx` 732 → 389 行（抽 4 個 hook）、`EditorPane.test.tsx` 2025 行與 `StoragePane.test.tsx` 1981 行拆分、`remapPanesUnder` 職責分離、抽 `lib/path-remap` 共用規則。三次純重構均驗證測試總數不變、零斷言修改。

測試 3983 → **4365**（+382）。lint / build / `go test ./...` 全綠。

**Follow-up**：#941 restore 後近期檔案失連 · #942 `text-accent-base` repo-wide · #943 `getWsBase` 同類 host fallback（WebSocket 可連錯機器）· #944 untitled 改名後 ⌘S 撞名靜默中止 · #945 flaky test · #946 `openRecentEntry` 收斂 · #948 footnote 偵測誤判 code fence · #949 save/rename hook 耦合 · #950 `round-trip-safety.ts` 拆分 · #951 Live Mode 支援連結包圖片（README badge）· #953 placeholder 除籍收斂 · #954 Storage 檔案過大 · #955 路徑快照 ABA

## [1.0.0-alpha.327] - 2026-08-18

### Fix(spa): New Tab「Bring in an open tab」限高半窗並內部捲動

分割視窗時右側 new-tab pane 的 Bring-in 區塊無高度上限（`flex-shrink-0`），開啟的 tab 一多就整段撐長，把下方 provider grid（Sessions / Files…）擠出可視範圍。

改為 `max-h-[50%]` + `min-h-0` 的 flex column：標題固定、候選清單 `overflow-y-auto` 內部捲動；保留 `flex-shrink-0` 讓候選少時仍維持自然高度。附 regression test（20 個候選 tab）。

## [1.0.0-alpha.326] - 2026-07-19

### Feat(m0): Ploom↔Purdex 派工整合 — 設計基礎 + Purdex 執行消費端 (#933, #937)

M0 walking skeleton 的 **Purdex 側**：Ploom issue 派工 → daemon 輪詢領工 → 在指定 repo 起 `claude -p` → 狀態/diff 回報 → deeplink 觀看。Ploom 側為獨立 PR（另一 repo）。

**設計基礎 (#933，docs-only)**：M0 spec（3 輪 codex 深審，findings 8→4→1→0）+ implementation plan + **共享 wire contract SOT**（`docs/specs/m0-contract.md`）+ 16 個 golden fixtures（`docs/fixtures/m0/`），作為兩 repo 各自 mock 的唯一真相。

**執行消費端 (#937，12 個 TDD task)**
- **傳輸**：pull 模型 —— `GET /daemon/dispatches?status=pending` → claim → 兩段式 fetch → report；Ploom 純 server 無 callback。
- **execution runtime SOT**：`execution_id` 為唯一對外 handle；狀態機 + `dispatch_id` 冪等；`GET /api/execution/{id}` 唯讀投影。
- **Crash-consistency**：狀態轉換與 report enqueue 在**同一 SQLite transaction**（outbox 併入 execution DB）；launch fence（`launch_state`）防重複 launch；`session_name` 為 crash-recovery handle（`HasSession` 探活、by-name 收孤兒）；startup reconcile sweep + manual reclaim（`POST /api/dispatch/reclaim`）；ack cursor + accepted-before-lifecycle ordering + 重啟 replay。
- **Terminal 兩來源**：process-exit 決定**時點**（權威），`result.is_error` 決定**成敗**；exit 0 但無 result → completed(`exit_only`，degraded 並記錄來源）。
- **Admission**：canonical repo key（EvalSymlinks 防別名繞過）+ per-repo lock 跨 accept→launch（防 TOCTOU）+ status-based 單一 live execution + `head_at_start`/`dirty_at_start` 快照。
- **安全**：`dispatch.allowed_repo_roots` **fail closed**（未設則一律拒，建立 repo 信任邊界）；sandbox profile 全序 clamp（只降不升）映射 `claude --permission-mode`；派工 prompt 走 **relay stdin stream-json**（不進 tmux 指令列，零 injection）；缺依賴時停用 consumer 而非 claim-and-drop。
- **Deeplink**：`purdex://execution/<id>` OS protocol handler（single-instance / open-url / 冷啟動 buffer / 單一落點視窗）+ SPA execution route 與 **observe-only** 詳情頁（不掛 stdin 寫入）。
- **Artifact**：pointer-first（diff `{files,add,del}` 摘要 + transcript pointer，不 inline blob）。

**Review**：codex 標準 review（3 項全修）+ 3-parallel adversarial（攻擊/防守/檔案體質，三份皆 needs-attention）收斂出 6 項，5 項修復、1 項（DB 層單-live guard，M0 單 daemon 前提外）→ issue #938。

`go test ./...` 全綠 / vitest 3982 / lint / build 綠。

## [1.0.0-alpha.325] - 2026-07-19

### Fix(opencode): child (subagent) session 事件不再劫持父 session 燈號 (#934)

opencode 每次 subagent（Task tool）完成，父 session 的燈號會錯誤塌成 idle（甚至變紅／frame 被刪），即使父 session 還在跑。根因：opencode 把每個 subagent 開成獨立 **child session**，plugin 之前把 child 的每個生命週期事件（created/idle/error/deleted）都當父 session 的 `Pdx*` 事件 emit；daemon 用 `(pane, senderPID, senderStartTime)` 比對 frame（**非** opencode session_id），而單一 opencode process 的父子共用同一 pane／sender identity，所以 child 事件全落在父 frame 上。

- **修法（純 plugin，daemon/SPA 不動）**：plugin 維護 `subagentSessions = Map(childSessionID → parentSessionID)`，從 `session.created` 的 `info.parentID` 學習，gate 掉 known child 的全部 parent-level emit。subagent 的真實表徵仍由 `PdxSubagentStart/Stop`（detail-only，不搶 frame）提供。
- **reload-proof delete**：`session.deleted` 也 publish 完整 info（`session.ts:624`），故 child delete 直接用事件自帶 `info.parentID` 判定，即使 plugin reload 期間漏收該 child 的 created，也不會誤刪**父** frame。
- **防禦**：`sid = sessionID || info.id` fallback；空 sid 不入 map；parent delete 只清 value 相符的 children（單 process 可有多 root session）。
- **已知殘留（#935 追蹤）**：`session.status` idle 與 `session.error` 上游確實不帶 parentID（opencode #30043），故 plugin reload 中途的罕見窗口仍可能漏一次假 idle（`notification_silent`、可自我修正）／假 error；非回歸，且遠窄於修前的「每個 subagent 都漏」。
- 上游 schema 對現行版本查證（`session.status` 無 parentID、`session.created`/`session.deleted` 帶完整 info）；codex spec+plan+PR 標準+對抗性 review 共 4 輪，抓修 reframe（僅擋 idle→擋全生命週期）、`Set`→`Map`、reload-window delete、空 sid 污染等；Go template + `pluginSimState` mirror 雙軌 + Bun 真 JS 序列測試；全套 go test（含 `-race`）綠。

## [1.0.0-alpha.324] - 2026-07-14

### Feat(snapshot): Sessions 對帳表手動編輯 cwd（rebuild 落點）(#931)

在 Snapshot Sessions 對帳表 double-click **Directory** 欄，手動修正/補上該 session 重建時的目標 cwd。純 client-side 改快照，不碰 daemon、不動 live session——只改「Rebuild all / Restore everything 對該 session 傳給 createSession 的 cwd」。承接 alpha.321（cwd 改用 pane_current_path），補上使用者直接控制落點的能力。

- **所有列可編**：⚠️ 無 cwd 者補上路徑即變可重建（restorable:true）；🟢 live 設將來死掉的落點；空值清成 structure-only。
- **`setSessionMetaCwd` 純函式**（storage.ts）：trim；非空 → cwd+restorable:true+清 captureError（手動值權威，蓋過 cwd-probe-failed/dead）；空 → cwd:undefined+restorable:false；複合鍵 `[host][code]` 定點更新、不 mutate；維持 `restorable ⟺ 有 cwd` 不變式（與 capture / computeHealth 🔴 predicate 一致）。
- **`EditableCwdCell`** inline edit：double-click 進編輯（預填現值）、Enter/blur 存、Esc 取消；committedRef latch 防 Enter-then-blur 雙提交。存檔走 readSnapshot→setSessionMetaCwd→writeSnapshot→既有 refresh() 重讀+重跑健康度 → 該列 badge 即時更新（⚠️→🔴）。
- **重拍整包覆蓋**手動編輯（不 sticky，定案）；無路徑驗證/無 `~` 展開。
- codex 兩輪（標準+對抗性三視角）抓修 2：H1 cwd 編輯在 busyRef 單飛守衛外 → 與 in-flight capture/restore race + lost update（改 `disabled={busy}` 不進編輯 + commit busy 時 reject）、H2 Enter 在 IME composition 中誤 commit（compositionRef + isComposing 雙偵測，composing 時 Enter/Esc 不觸發——CJK 選字安全）。subagent-driven TDD；settings+snapshot 314 測試綠 / 全套 3963 / lint / build。純 SPA→HMR。

## [1.0.0-alpha.323] - 2026-07-14

### Style(new-tab): Sessions 列表標題亮度對齊 id (#929)

New-Tab Sessions 列表的 session 動態標題（`pane_title`）由 `text-text-muted`（較暗）改為 `text-text-secondary`，與 session code（id）同亮度。純 SPA→HMR。

## [1.0.0-alpha.322] - 2026-07-14

### Feat(new-tab): Sessions 列表在 session id 後顯示動態標題 (#925)

New-Tab 的 Sessions 列表，每列在 **session code 之後**顯示該 session 的動態標題（`pane_title`，即 tab 上顯示的那種活動標題），填入原本空白區域。

- `SessionRow` 由 `[icon] name  code(ml-auto)` 改為 `[icon] name  code  {pane_title}`；code 去掉 `ml-auto`、`pane_title` 接於其後、`text-text-muted` `truncate` 填滿剩餘空間；僅 `session.pane_title` 存在時渲染，不 gate on `dynamicTabName`。
- Flex 佈局經 codex 4 輪 review + headless-chromium 實測收斂：name `truncate` 可截斷（防長名溢出）；**僅在有 pane_title 時**加 `max-w-[50%]` cap（保 title 可見、避免兩者皆長時 title 塌成 0px）；無 title 時 name 用全寬。code 恆 `flex-shrink-0`。
- vitest 3930 綠 / lint / build。純 SPA→HMR。

## [1.0.0-alpha.321] - 2026-07-14

### Fix(snapshot): 擷取 cwd 改用 pane_current_path，非 session 起始目錄（`~`）(#926)

Workspace Snapshot 實測發現 Sessions 對帳表的 Directory 欄大多顯示 `~`。根因：`capture.ts` 取 `listSessions().cwd`（= tmux `#{session_path}` = session 起始目錄，且不展開 `~`），非目前 pane 的實際 cwd。影響 UI 可讀性，且「Rebuild all」會把 session 重建到起始目錄（常是 home）而非實際工作目錄。

- **修法**：live session 改用 `fetchSessionCwd`（daemon `/api/sessions/{code}/cwd` → tmux `#{pane_current_path}`）解析目前 pane 的絕對 cwd。每 host 仍只 `listSessions` 一次（liveness + name/current_command/mode）；per-session `fetchSessionCwd` **先按 `(host,code)` 去重**再 `Promise.all` 並發、各自 try/catch 隔離。
- **fallback 可觀測**：`fetchSessionCwd` throw/空 → 退回 `listSessions().cwd` 並標 `captureError:'cwd-probe-failed'`（不再靜默偽裝成精準捕獲；non-empty 仍 restorable 保留 best-effort 重建）；兩者皆空 → restorable:false。dead / host-offline 分支不變。
- codex 兩輪（標準 + 對抗性三視角）抓修 2：G1 同 session 多 pane 未去重的並發 `/cwd` race（last-writer 覆蓋真實 cwd）、G2 靜默 fallback 偽裝退化快照。subagent-driven TDD；`src/lib/snapshot/` 72 測試綠 / 全套 3931 / lint / build。純 SPA→HMR。

## [1.0.0-alpha.320] - 2026-07-14

### Feat(new-tab): Sessions 列表 tab 同款 agent 指示器 + 依 host 建立 session 並 attach (#923)

New-Tab 起始畫面的 **Sessions 列表**兩項增強：

- **① tab 同款 icon/status prefix**：每列改用 `<TabIcon>` 顯示與分頁一致的 agent 圖示（Claude Code / Codex / OpenCode）、執行狀態、subagent dots、unread，並跟隨使用者的 `tabIndicatorStyle` 設定。抽出共用 hook `useSessionAgentIndicator`（`compositeKey`→`useAgentStore` + `getAgentIcon` + `tabIndicatorStyle`），`useTabDisplay` 重構為消費此 hook（單一真相來源、行為等價），`SessionSection` 每列抽 `SessionRow`；無 agent 時 fallback 回終端機圖示。
- **② 依 host 建立 session + attach**：每個 host 標題列（含單 host、含零 session 的 host）加獨立 `+` 建立鈕（header 改 `<div>`、collapse 與 `+` 為分離按鈕、`+` 不觸發收合、收合態點 `+` 會展開）。`NewTabSessionForm`（name/cwd/mode，沿用 `createSession()`）建立成功後直接 `onSelect` attach 進**當前 pane**（全頁 new-tab 或分割格皆適用）。`+`/submit 採 host 頁 offline 語意 disable（`!runtime || status!=='connected' || tmuxState==='unavailable'`）。

多重資料安全守衛：blank code / host-live 送出前後雙重再查、`activeRef` mounted guard（cancel/collapse/host-removed/tab-switch/StrictMode double-invoke 皆不誤 attach 或 unmounted setState）、`creatingRef` 雙擊防護。

spec + plan 各經 codex 跨模型審一輪並強化；subagent-driven TDD（2 phase 5 task）；PR 經 codex **4 輪** review（標準 + 對抗性三視角 + 2 確認輪）抓修 4 個問題（收合表單失效 / stale async attach / offline pre-POST / StrictMode activeRef）全數修復並附回歸測試。vitest 3927 綠 / lint / build。純 SPA→HMR。

## [1.0.0-alpha.319] - 2026-07-14

### Feat(snapshot): Settings「Snapshot」section UI（Phase 3）(#920)

工作區快照 / 一鍵重建的**使用者入口**：Settings 新增「Snapshot」section，接上 alpha.318 的 headless capture/storage/restore 引擎。至此整個 Workspace Snapshot 功能（Phase 1+2+3）完整可用——拍下工作區、伺服器重開機 / tmux 重啟後依 name+cwd 一鍵重建。

- **健康度四態對帳表**（掛載時各 host `listSessions` 即時對帳）：🟢 活（live 含 code+name，與引擎 reattach 規則一致）/ 🔴 已死可重建（restorable 且有 cwd 且 host 可達）/ ⚠️ 只保結構（不可重建）/ ⚪ host 離線。Tmux 區塊對帳表（host/name/cwd/current_command/健康度）+ Tabs 區塊樹（workspace→tab→pane）。
- **三動作 + 復原**：拍快照、重建所有 session、還原 tab 佈局、全部還原、復原上次還原（無 `-prev` 時 disabled）。`useRef` 單飛守衛；toast 依 `RestoreReport` 彙總；restore 失敗以 error tone + `rebuiltButUnattached` 揭露。`SETTINGS_ORDER.SNAPSHOT=22` + 38 個 i18n key（en/zh-TW 對等）。
- **codex 兩輪跨模型審**（標準 + 對抗性 3-parallel 攻擊/防守/體質）抓修 3 項：capture 後 `snap` mount 凍結不刷新致同頁 capture-then-restore 失效（改可變 state + mutation 後 refresh 重跑健康度對帳）、健康度 🔴 predicate 未含 `cwd` 與引擎 rebuild 規則不一致、`RestoreError` 失敗被降級成 warn（改 error tone + 保留揭露）。元件/測試拆 hook 重構延後 #921。
- subagent-driven TDD；`SnapshotSettingsSection` 19 測試綠 / 全套 3910 / lint / build。純 SPA→HMR。

## [1.0.0-alpha.318] - 2026-07-14

### Feat(snapshot): 工作區快照 capture／storage／restore 引擎（Phase 1+2，headless）(#914)

工作區快照 / 一鍵重建的**純前端邏輯層**：拍下整個工作區（workspace/tab/pane 結構 + 每個 tmux session 的 name/cwd），讓伺服器重開機 / tmux 重啟後依 name+cwd 一鍵把工作環境重建回來。本版僅引擎，Settings UI 為後續 Phase 3。Spec/Plan 經 codex R1–R3 全過。

- **Phase 1 資料模型 + capture + 持久化**：`types.ts`（`SessionMeta`/`WorkspaceSnapshot`/複合鍵 `Remap`/`RestoreReport`/`RestoreError`）、`storage.ts`（正本 + `-prev` 後悔藥 key，走 `browserStorage`）、`capture.ts`（`buildSnapshot` 純建物件不寫 storage、`captureSnapshot` 寫正本 + 統計；每 host 一次 `listSessions`，live 無 cwd / 已死 / host 不可達 → `restorable=false`）。
- **Phase 2 restore 引擎 + 三動作**（`restore.ts`）：`ensureSessions`（對帳 + 重建，逐筆失敗隔離、以 `createSession` 回傳物件為準）、`remapLayoutSessions`（純函式改寫 layout 樹）、`validateSnapshotConsistency`（5 條導航守衛）、`replaceTabSnapshot`（整包取代兩 store，任一步 throw 全回滾）、三動作 orchestration（`rebuildAllSessions` 不收窄含 orphan、`restoreTabLayout`、`restoreAll`、`undoLastRestore`）、`syncSessionStore` per-host 聚合。
- **實作中修正 plan 矛盾**：`-prev` 誤用 `captureSnapshot`（會覆寫正本毀還原來源）改用 `buildSnapshot`（B1）。
- **codex 兩輪跨模型審**（標準 + 對抗性 3-parallel 攻擊/防守/體質）抓修 5 項：syncSessionStore 整包覆蓋抹掉非快照 live session（+ 修法引入的幽靈 session）、`writePrevSnapshot` 失敗未揭露已建 session、reattach 只憑 code → 改 **code+name 都吻合**（防 daemon 重啟 code 重用錯接）、storage 只驗 version → 加輕量 shape 守衛。駁回「pane mode 不同步」（誤報，違 app 慣例）。restore.ts 拆檔延後 #918。
- subagent-driven TDD；`src/lib/snapshot/` 68 測試綠 / 全套 3889 / lint / build。純 SPA→HMR。

## [1.0.0-alpha.317] - 2026-07-14

### Fix(panes): 分割後 pane 內容無法捲動 — split 容器改用 h-full 取得有界高 (#916)

分割（pane split）後 pane 內容無法捲動（如 new-tab 起始畫面在半高分割格搆不到底部）；全頁單葉分頁不受影響。

- **根因**：頂層 split 直接掛在 `TabContent` 的 `<div class="absolute" style="inset:0">` **block** 容器下，而 split renderer 的 root 用 `flex-1`——flex-item 屬性在 block 父層失效 → split 容器塌陷成 content height → 往下每層 pane 失去有界高 → pane 內 `overflow-y-auto` 撐到全內容高、永不可捲，尾端被上層 `overflow-hidden` 裁掉。單葉全頁分頁走 `h-full w-full`（有界）故正常，形成「全頁可捲、分割不可捲」的差異。
- **修法**：split 容器 root 由 `flex-1 flex …` 改為 `h-full w-full flex …`，對 absolute wrapper 的有界高解析成功並級聯穿透巢狀 split（其父為 flex wrapper，100% 同樣可解析），`w-full` 維持橫向分割滿寬。
- 新增結構守衛測試（split root 必含 `h-full w-full`、不含 `flex-1`）。jsdom 不量佈局，另以 headless-chromium 佈局實測確認頂層與巢狀 split 皆從 `canScroll:false` → `canScroll:true`、last item 可達。全套 vitest 3821 綠 / lint / build。codex 兩輪（標準+對抗性三視角）皆 approve、無實質發現。純 SPA→HMR。

## [1.0.0-alpha.316] - 2026-07-14

### Fix(new-tab): 「Bring in an open tab」僅在分割格顯示，不再外洩到全頁新分頁 (#913)

「Bring in an open tab」區塊（alpha.315 #910 引入）原本會出現在**每個獨立的全頁新分頁**。根因：全頁新分頁本身就是「1 tab + 1 pane」，一律帶著 `currentTabId + currentPaneId`，而這是唯一的顯示 gate。此區塊只有在 new-tab pane 是**分割（split）的其中一格**時才有意義（把另一個 tab 的內容拉進這格對照檢視）。

- `NewTabPage` 的 `bringInCandidates` 新增 gate：僅當擁有此 pane 的 tab 為分割狀態（`countLeaves(tab.layout) > 1`）時才列出候選。全頁新分頁回到原本裸啟動畫面（`h-full` 捲動契約不變），區塊只在 split 一格時重新出現。
- 測試：新增「全頁新分頁即使有多個 movable tab 也不渲染區塊」；既有 bring-in 測試改以 `splitPaneBlank` 建構真實分割反映新語意；保留 current tab 不列入自身候選的驗證。全套 vitest 3820 綠 / lint / build。codex 兩輪跨模型審（標準 + 對抗性三視角）皆 approve、無實質發現。

## [1.0.0-alpha.315] - 2026-07-11

### Feat(panes): 跨 workspace 把已開分頁拉進分割格 (#910)

pane split UI 的 Phase 3（承接 alpha.313 PR-A 的右鍵分割 + StatusBar 分割鈕）：在空白 New Tab 分割格可**把已開的分頁（跨 workspace）拉進來**做雙邊檢視。

- **`moveTabContentIntoPane` 搬移原語**（`lib/pane-move.ts`）：guard（來源存在 / `!locked` / `countLeaves===1` / kind ∈ `MOVABLE_KINDS` / 非自身 / **target tab+pane 存在**）後 `setPaneContent` 注入目標格，再經 `closeTabInWorkspace({skipHistory:true})` 退場。刻意走搬移語意（非 `tab-lifecycle::closeTab` 銷毀語意）→ 跳過誤報 dirty-confirm、內建 active fallback。allowlist = `editor / tmux-session / image-preview / pdf-preview`（排除 browser 的 BrowserView 生命週期）。
- **`NewTabPage`「Bring in an open tab」區塊**：跨所有 workspace 列「單-pane 且 primary kind ∈ allowlist 且 `!locked` 且非本 tab」候選，標所屬 workspace 名；點擊即搬入。`NewTabPaneWrapper` render 時反查 `currentTabId` 傳入；無候選時回裸 grid 保護既有 h-full 捲動契約。
- codex 三輪跨模型審抓出並修復三個資料安全問題：editor 未存 buffer 於 pull-in 時因 unmount `closePane` 早於 mount `attachPane` 遺失（pre-bind 目標 pane 引用）、跨 host 相同 sessionCode 誤標名稱（改用 `hostId` scope lookup）、stale target 時內容遺失（加 target 存在性 guard）。subagent-driven TDD；全套 vitest 3808 綠 / lint / build。

### Fix(terminal-link): 檔名點分段允許 `+` build metadata (#911)

終端機檔案連結偵測在語義版本 build metadata 的 `+` 處截斷（`com.wake.custom-css-0.0.0+075a408.tar.gz` 只抓到 `...0.0.0`）。四條路徑 regex（ABS/TILDE/REL/BARE）的副檔名鏈由 `(?:-[A-Za-z0-9]+)*` 擴為 `(?:[-+][A-Za-z0-9]+)*`，讓 `+` 比照連字號納入合法字元（承接 alpha.314 #904 連字號修正的同家族延伸）。

版本字串排除採**明確 bias — 不 linkify 版本雜訊**：從第一個 `+` 切開，前段為純版本號者不當連結（`1.0.0+exp.sha`、`1.0.0+build-123` 等終端常見版本輸出）。真實 build 產物帶套件名 stem（`com.wake.custom-css-...`）故前段非純版本、仍可點。取捨：stem 本身為裸版本號的檔名（`v1.0.0+build123.txt`）不連結——罕見，優於把大量版本雜訊變成死連結。此邊界（`.sha` build 識別碼 vs `.log` 真副檔名語法無法區分）經 codex 三輪確認為物理互斥，由使用者定案 bias。TDD；全套 3795 綠。

## [1.0.0-alpha.314] - 2026-07-08

### Feat(editor): 新分頁「最近開啟」檔案清單 (#905)

在 Editor 新分頁區塊、New File 按鈕下方加入**最近開啟檔案清單**，涵蓋本機 / 遠端(daemon) / in-app 檔案。

- **資料層** `useRecentFilesStore`（persist localStorage，仿 browser history）：entry = `{source, path, name, kind, openedAt}`，以 `source(+host)+path` 去重、most-recent-first、cap 50。
- **記錄規則**：現有檔**開啟即記**（`defaultTabOpener`、missing-file popup `onOpenPath`、`openInAppFile`）；新建檔**存檔才記**（`EditorPane.handleSave` 一般存檔分支 + `saveUntitledBuffer`）；從清單重開亦刷新 recency。不攔 tab restore 避免污染。
- **UI**：固定 chips（全部 / 文字 / 圖片 / PDF）；每列 kind 圖示 + 檔名（截斷 + tooltip）+ 淡路徑 + daemon host badge。
- **點擊**：best-effort 就地開（沿用 `onSelect`，與 New File 一致）。daemon 先驗 host 存在（擋 `getDaemonBase` fallback 開錯主機）+ stat（含 isFile 守衛）；成功開、找不到 / 非檔案 / 錯誤跳 toast。

設計經 brainstorming 定案、spec+plan 各過一輪 codex 跨模型審並整合缺口；實作以 subagent-driven TDD 分 5 task（store→recorder→wiring→openRecentEntry→UI）；PR 經 codex 兩輪（R1 修 reopen 未刷新 recency；R2 三視角指出 daemon pane host-blind / read 失敗開空 buffer / persist 無 guard 皆為既有全 app 行為，追蹤 #906/#907/#908）。全套 vitest 3772 綠、lint / build 通過。

### Fix(terminal-link): 檔名點分段允許連字號 (#904)

終端機 pane 的檔案連結自動偵測在檔名**點分段含連字號**時會截斷（如 `docs/souls/morphy.pre-edit.SOUL.md` 只抓到 `docs/souls/morphy.pre`）。四條路徑 regex（ABS/TILDE/REL/BARE）的副檔名鏈由 `(?:\.[A-Za-z0-9]+)+` 改為 `(?:\.[A-Za-z0-9]+(?:-[A-Za-z0-9]+)*)+`——允許段內以單一連字號連接的英數群組（`pre-edit`、`min-2`、`2024-01`），且不吃前導 / 結尾 `-`。

同步強化版本字串排除（`allExtensionsVersionLike`）：`v1.2.3-beta` 等 semver prerelease 不再被誤判成假連結，`bar.min-2.js` / `v1.2.3.tar.gz` 等真檔名仍連結。純數字-prerelease 單一副檔名（`report.2024-01`）維持既有 IP/版本取捨不連結。TDD；全套 3756 綠。

## [1.0.0-alpha.313] - 2026-07-08

### Feat(panes): pane split UI — 右鍵選單 + StatusBar 分割鈕 + 移除 grid-4 (#901)

為既有 tab 分割引擎補**互動式 UI 入口**（引擎原已支援任意巢狀，僅缺入口）：

- **Pane 右鍵選單**（非 editor pane）：`Split Horizontal` / `Split Vertical`（恆有）＋ `Close pane` / `Detach to tab`（僅 `countLeaves>1` 時）。editor(Monaco) pane **不攔截右鍵**（保留原生選單）；攔截型 pane 的 **`Shift`+右鍵** 為 escape hatch 放行原生選單（xterm/browser）。
- **StatusBar 分割鈕**：終端機 pane 在 `terminal/stream` 膠囊旁加 split-H/V 鈕，作用於 primary pane。
- **`splitPaneBlank`**：新 store action，分割切出空白 New Tab 格（不改既有 `splitPane`）。
- **移除 grid-4 硬編碼特例**：田字版型的連動分隔線特例移除，grid-4 形狀改由通用遞迴 renderer 渲染、各 split 獨立 resize，與任意巢狀相容。TitleBar 版型鈕保留 single / split-h / split-v。

設計經 brainstorming 定案、spec+plan 各過 codex 審；PR 經 codex 兩輪 + 最終確認 review（R1 修單-pane wrapper flex→block 縮寬回歸；R2 修選單 stale 誤關整個 tab + grid-4 內 split 連動 resize 衝突；final 抓出 browser pane 右鍵入口受 BrowserView overlay 阻擋 → 追蹤 #902）。

**純 SPA → HMR 即時生效。** 已知限制：browser pane 右鍵分割需 Electron IPC（追蹤 #902）。剩 PR-B（跨 workspace 拉 tab）待做。

## [1.0.0-alpha.312] - 2026-07-03

### Feat(editor): Outline-style markdown Live Mode + content-width toggle (#899)

markdown「Live Mode」（Tiptap WYSIWYG）套用 **Outline（getoutline.com，同屬 ProseMirror 家族）** 的閱讀排版，解決先前 `max-w-none` 貼齊面板、寬螢幕單行字元數暴增（100+）閱讀不適的問題。樣式數值直接取自 Outline 原始碼：內容欄 `52em`、字級 h1 28 / h2 22 / h3 18 / h4 16 / h5-6 15、行高 `1.7`（沿用 Outline 對複雜文字的放鬆，含 CJK）、標題無底線 600 粗、code `6px` 圓角。

新增**內容寬度切換**（全域偏好、持久化）：`narrow`（限寬置中 52em，預設）⇄ `full`（滿寬）；toggle 位於狀態列 Live Mode 鈕旁，**僅 Live Mode 顯示**。

- 樣式 scope 於 `.tiptap-editor`，**顏色全用既有 theme CSS 變數**（跨主題自適應，不改配色）；不影響 `MessageBubble` 等泛用 prose 使用處。
- 系統性中和 Tailwind Typography 外洩的 prose 預設：blockquote 斜體 + open/close-quote 彎引號、inline code 注入的 backtick + 600 粗細（掃過 built CSS 全部 4 條 pseudo-content 規則確認無殘留）。
- 寬度偏好放 `useEditorSettingsStore`（persist + sanitize + rehydrate 信任邊界）；寬度改由 `<EditorContent>` 外層置中欄 wrapper（`max-w-[52em] mx-auto box-border`）控制，`useEditor` 初始化不變，避免 remount / viewState 風險。
- Spec + Plan 各經 codex 跨模型審閱定稿；PR 經 codex 兩輪深度 review（R1 blockquote pseudo 外洩 / 排除 h2 padding 誤報；R2 Diff view toggle 洩漏 AC4 + inline code backtick）全數修正。

**純 SPA → HMR 即時生效。** 取捨：`52em` 先忠實沿用 Outline 值，CJK 長文觀感可後續微調。

## [1.0.0-alpha.311] - 2026-07-01

### Fix(spa): keep light (non-terminal) tabs alive across switches (#897)

修正 **Live Mode（及任何 editor tab）切走再切回會「重新載入」回到頂端**。根因：`keepAliveCount` 預設 0，alive pool 只保留 active tab → editor 每次切 tab 都 unmount+remount → viewState 還原不穩 → 回頂 + reload flash。而 `keepAliveCount` 本質是 terminal renderer（xterm/WebGL）記憶體設定，editor 被連坐 evict。

修法：把 tab 分 **light vs heavy**。heavy = 含 `tmux-session` / `browser` pane（GPU/WebContents 記憶體）；light = 自足、一次性載入、不做背景工作的渲染器（allowlist：`editor` / `image-preview` / `pdf-preview` / `dashboard` / `history`）。light tab 不受 keepAliveCount 連坐，以獨立 LRU（`LIGHT_KEEP_ALIVE_MAX = 8`，most-recent-first）保活 → 工作集切 tab 不 remount、scroll/mode 原生保留、記憶體有界。heavy tab 維持原 keepAliveCount LRU（無 light tab 時 alive set byte-identical）。

- 新 `isLightTab(layout)`（`lib/pane-weight.ts`）用 light allowlist 掃 leaves（未知/未來 kind 預設 heavy，保守）。
- `useTabAlivePool` 加 optional `light`：bounded light LRU + budget-aware history trim（不因大量 heavy 訪問而遺忘仍在預算內的 light tab）+ pinned light/heavy 皆豁免預算 + active 恆存活。
- PR 經 codex 6 輪 adversarial review 逐一關閉：memory-monitor/settings/new-tab 誤分類（背景輪詢/可擴充 host）、light 無上限記憶體、history trim invariant、pinned light 契約——最終 approve。

**純 SPA → HMR 即時生效。** 取捨：最多 8 個近期 light tab 常駐（editor/Monaco/Tiptap，遠小於 terminal GPU）；terminal 記憶體行為完全不變。

## [1.0.0-alpha.310] - 2026-07-01

Batch A — editor markdown + terminal-link 使用者需求。

### Feat(spa): markdown opens in Live Mode + fix Live Mode scroll jump on tab switch (#894)

- **markdown 預設 Live Mode**：`.md` 從 raw（Monaco）改為預設進 **Live Mode（Tiptap wysiwyg）**；非 markdown 維持 raw。`EditorPaneState.editorMode` 改為 `EditorMode | null`（null = 未明確選擇 → render 時解析語言預設），使用者明確切換的具體值優先且跨 remount 保留。語言預設只在 paneState 對齊 buffer 後套用，保住 #863 不變式（Tiptap lazy 不對 stale 掛載、buffer 切換不閃 Loading）。
- **修 Live Mode 切 tab 跳頂**：`isActive` false→true 的重新聚焦改用 `.focus({ preventScroll: true })`，避免把游標（頂端）捲進視野造成整頁跳頂。
- （探索過的「明確 mode 選擇跨 pane 換檔持久化」因 stale-path meta-drift 撤回；切 tab 的 mode 保持在 keep-alive 下本就 work。）

### Feat: shift+click a URL opens the OS default browser, not a mini window (#895)

終端機（及 browser pane 內）URL 的 shift+click 從「Electron 迷你視窗」改為「**OS 預設瀏覽器**」（一般點擊仍開 app 內 tab）。SPA url opener 的 `openMiniWindow` dep 換成 `openExternal`，接到新的 `openExternalUrl` bridge → main 端 `shell:open-external` handler，經 `isAllowedUrl`（`new URL().protocol` 對 http/https 白名單）scheme 防護後 `shell.openExternal`。統一 `openAllowedExternalUrl` helper try/catch 避免 unhandled rejection。迷你視窗保留給 browser pane pop-out。**⚠️ 含 Electron main/preload 改動，Air 端需重打包 + dev update 才生效。**

## [1.0.0-alpha.309] - 2026-06-30

### Fix(spa): new-tab / history panes fill height via h-full, not flex-1 (#891)

修正「起始畫面（new-tab）」與「History pane」**無法捲動**的 layout bug——延續 #889 的同一 bug class。

根因：`NewTabPage` / `HistoryPage` root 用 `flex-1` 撐高，但 `TabContent` 的每分頁掛載層是
`position:absolute inset:0` 純 block（非 flex container），`flex-1` 在其下失效 → root 塌成內容高 →
內層 `overflow-y-auto`（new-tab 欄 / history 列表）拿不到有界高度就不能捲。`EditorPane` / `TerminalView`
本來就用 `h-full w-full` 故正常。

修法：兩 pane root（NewTabPage 含 grid + empty-state + placeholder 四個 return）`flex-1` → `h-full w-full`。
`HostPage` / `SettingsPage` root 是 `flex h-full`、scroll 容器為正確 flex child，**不受影響、未動**。回歸測試
斷言兩 root 用 `h-full` 且非 `flex-1`；vitest 3696 綠、lint、build 通過，codex review 0 findings。

## [1.0.0-alpha.308] - 2026-06-30

### Fix(spa): image/pdf preview fill height via h-full, not flex-1 (#889)

修正在 tab 內瀏覽圖片時「寬度等比縮、高度卻不縮、且無法捲動」的 layout bug（PDF 預覽同症狀）。

根因：`ImagePreviewPane` / `PdfPreviewPane` root 用 `flex-1` 撐高，但 `TabContent` 的每分頁掛載
層是 `position:absolute inset:0` 的純 block（非 flex container），`flex-1` 在其下失效 → root 塌成
內容高 → 內層容器無明確高度 → `<img>` 的 `max-height:100%`（PDF 為 iframe 的 `h-full`）解析成
`none`，只剩寬度受限 → 直幅圖/PDF 高度溢出被 `overflow-hidden` 裁掉、不可捲。split / 有 header 的
情境父層是 flex 容器故不受影響，僅「單一整頁」會中。

修法：兩個 pane root `flex-1` → `h-full w-full`，對齊既有的 `EditorPane`（`h-full` 能解析到 block
父層的 inset:0 明確高度）。恢復 `object-contain` 長邊優先等比縮，並讓 actual 模式的捲動路徑可達。
回歸測試斷言兩 pane root 用 `h-full` 且非 `flex-1`；vitest 3694 綠、lint、build 通過，codex review 0 findings。

## [1.0.0-alpha.307] - 2026-06-29

### Feat(storage): restore UI — history/viewer/restore + pane reconciliation (Phase 2c-2) (#887)

子系統 2 Phase 2c-2：前端還原 UI，把已 ship 的 2c-1 還原引擎接到 Storage pane 右側欄 + modal。
**Storage 子系統 2（daemon 備份/還原版本庫）全系列（2a/2b/2c）完成。** spec §4.4/§4.7 + AC-2c。
4 TDD task；PR 經 codex 標準 + 三視角 + **5 輪深度 review**，逐層關閉「跨 host UI 狀態」整類問題
（2 P2 + 3 high + 2 medium 全修），full suite 3692 綠、lint、build 通過。

- **history list**（`BackupHistoryList`，右側欄）：`getHistory` newest-first + device/相對時間/trigger/
  **fork badge**；loading/error/empty；fetch deps `[hostId, lastBackupAt]`；rows 以 host tag 標記，
  切 host 同步隱藏舊主機的列（不可誤點）。
- **manifest viewer modal**（`BackupSnapshotModal`）：點 row 開 modal，`getSnapshot` 列檔案清單
  （path/kind/size/words，**不下載 blob**）+ header（device/time/trigger/fork）+ Close/Restore。
- **restore wiring**（`restore-wiring.runRestore`）：綁 live deps（dirty/locked guard、pre-restore
  forcePost、read API、backend）；done 後跑 pane reconciliation；backing-up 禁用；blocked 只列衝突
  中止（不隱式 save/discard）；pre-commit 失敗才報 restore error（樹未動），post-commit reconcile
  best-effort 不 throw。
- **pane reconciliation**（`reconcile-panes` + `pane-tree.remountLeaf` + `useTabStore.remountPane`）：
  還原後對齊開啟的 In-App pane —— close removed editor / reload changed clean editor
  （savedContent+isDirty+lastStat）/ preview **force-remount**（換 leaf pane.id 強制 re-read）；
  file→dir 轉換視為 close；failure-isolated。
- **跨 host 安全**：selection 綁 host、restore outcome 以 request token request-scoped、success
  banner host-scoped —— 任何 host 切換都不會用錯 host 還原或把 A 的狀態顯示在 B。

## [1.0.0-alpha.306] - 2026-06-29

### Feat(storage): restore engine — Phase 2c-1 (subsystem 2) (#885)

子系統 2 Phase 2c-1：前端還原引擎（headless，無 UI；由後續 2c-2 接上 history/viewer/restore UI +
pane reconciliation）。spec §4.4 + AC-2c。plan 經 codex 2 輪 review（R1 1C+2P1+2P2+1P3 → 全修 →
R2 READY-TO-IMPLEMENT）；5 個 TDD task 派 subagent 實作；PR 經 codex 標準 + 三視角 + 2 確認輪
（共修 1 P1 + 1 Critical + 1 medium，最終 R4 approve）。full suite 3651 綠、lint、build 通過。

- **讀取 client**（`backup-api`）：`getHistory`/`getSnapshot`/`getBlob` + `SnapshotSummary`/
  `SnapshotDetail` 型別；404 用 typed `BackupNotFoundError`。
- **`SupportsReplaceTree` capability + guard**（鏡像 `SupportsUniqueCreate`）；`InAppBackend.replaceTree`：
  單一 IDB txn 清空 root 子樹 → dirs-first → files，前置 relPath 全驗證，失敗 abort 回滾，不 emit
  `onMutation`（restore 不自觸發 auto-backup）。
- **原子還原守衛**：`InAppBackend` 維護 monotonic tree revision（每個 mutation txn 內 bump）；
  `replaceTree(…, expectedRevision)` 在自己 txn 內、刪除前重驗 revision，不符即 `TreeRevisionMismatchError`
  不 mutate —— 消除「pre-restore 後到 apply 前的本機寫入被靜默覆寫」的資料遺失窗口。
- **`backupNow(hostId, {trigger?,forcePost?})`** 回傳 `snapshotId|null`；`forcePost` 繞過 client no-op
  （pre-restore 一定 POST）；per-host single-flight（restore 與 auto-backup 不重疊）。
- **`restoreSnapshot(deps)`** orchestrator：guard → capture revision → pre-restore（forcePost）→
  getSnapshot + 逐 blob 驗 sha256/size（per-entry）→ apply-time guard re-check → atomic
  `replaceTree(expectedRevision)`。任一失敗在任何 IDB 寫入前 abort（R2-Pa）。
- **`findRestoreConflicts`** pure guard：dirty inapp buffer / locked tab 含 inapp editor pane（R2-Pg）。

### 不在本階段
2c-2：history sidebar list + manifest modal + Restore 按鈕 wiring + pane reconciliation（含
`remountPane` primitive）+ fork 指示。

## [1.0.0-alpha.305] - 2026-06-29

### Feat(storage+browser): post-2b UI polish (#883)

使用者實測 Phase 2b 後的 4 項 UI/UX polish（輕量 self-review；vitest 3616 / lint / build 全綠）。

- **調亮文字**：Storage 檔名 `text-text-secondary`→`primary`、size/words metric `muted`→`secondary`；
  備份側欄面板 `muted`→`secondary`、標題 →`primary`。
- **「New Buffer」→「New File」**：`editor.buffers.new`（en `New File` / zh-TW `新增檔案`），對齊
  Editor 區措辭。
- **隱藏 URL 歷史 dropdown（邏輯保留）**：`BrowserNewTabSection` 加 `SHOW_URL_HISTORY_DROPDOWN`
  flag gate render，history store / 鍵盤導航 / outside-click / scroll-into-view 全留，翻 flag 即還原。
- **啟動時檢查一次 backup**：backup auto-trigger 加 `runInitialCheck`，app boot 時 host 就緒即跑一次
  `backupNow`（未就緒則等首個 host 出現時觸發一次，idempotent，dispose 清乾淨）；靠 no-op 抑制 +
  daemon content-keyed no-op，內容沒變只是輕量 round-trip。

## [1.0.0-alpha.304] - 2026-06-29

### Feat(storage): front-end backup engine — Phase 2b (subsystem 2) (#881)

子系統 2 第二階段：前端 `spa/` 備份引擎，驅動已 ship 的 2a daemon backup API。把 Storage pane
右側欄的「備份（即將推出）」placeholder 填成實際備份引擎：監聽 In-App（IndexedDB `/buffer`）變更
→ debounce ~2s → walk 整棵樹 → 瀏覽器 sha256 → per-blob negotiation 上傳 → POST snapshot →
右側欄狀態。Plan 經 codex 3 輪 review（R3 READY-TO-IMPLEMENT，R1 1C+4P1+4P2+2P3 / R2 4 findings
全收斂），7 個 vertical-slice TDD task 派 subagent；full suite **322 files / 3612 tests** 全綠、
lint clean、build OK。

- **新 util**：`lib/crypto-hash.ts`（WebCrypto sha256，對齊 daemon lowercase hex）、`lib/text-metrics.ts`
  （從 `StorageRow` 抽出 word-count SOT，row 與 manifest builder 算出同一指標，無回歸）。
- **manifest builder**（`lib/storage-backup/manifest.ts`）：walk tree、**UTF-8 byte comparator**
  canonical sort（配 daemon Go byte order，否則 `400 unsorted`）、空 dir 進 manifest、blob dedup。
- **API client**（`backup-api.ts`）：`postMissing`/`putBlob`/`postSnapshot` over `hostFetch`，
  錯誤冒泡 HTTP status 供 engine 區分 409/400/413。
- **engine + `useBackupStore`**（per-host keyed）：client no-op suppression、任何成功 post（含
  server no-op 回 head）皆收斂 lineage、`parentId`=自身 prior（await 前同步捕捉，Design 5）、
  `applyRemoteBackupDone` status-only 不污染 lineage、same-bytes rename（0 PUT/1 POST）。
- **`backup:done`** 加進 `HostEvent` union + `useMultiHostEventWs` dispatch（只跨裝置 refresh，
  own-device 忽略）；`backup-ws-dispatch.ts` 可單測 seam。
- **右側欄狀態面板**（`BackupStatusSidebar.tsx`）：上次備份/備份中/inline error/尚未備份；i18n en+zh-TW。
- **In-App mutation emitter + 常駐 auto-trigger**：`InAppBackend.onMutation`（write/delete/mkdir/
  rename + unique-create，**非** replaceTree）；掛 app bootstrap（`main.tsx`，**非** StoragePane，
  Storage pane 未開也備份）；debounce capture mutation-time host、host 變更 cancel-only never retarget。

### 不在本階段
2c（前端還原 UI：history/viewer、dirty-block guard、atomic `replaceTree`、pane reconciliation、
fork 顯示）後續 PR。

## [1.0.0-alpha.303] - 2026-06-29

### Feat(storage): daemon backup/restore snapshot store — Phase 2a (subsystem 2) (#879)

子系統 2 第一階段：daemon 端 content-addressed append-only snapshot 版本庫，為前端 In-App
（IndexedDB `/buffer`）檔案樹提供跨裝置備份/還原的後端。純 Go daemon，無前端改動。Spec 經
4 輪 codex review（13→7→4→0 findings）收斂 SHIP-READY，plan 經 3 輪 review READY-TO-IMPLEMENT；
7 個 vertical-slice TDD task 派 subagent；PR 兩輪深度 review（標準 + 攻擊/防守/體質）+ 兩輪確認
共 1 P1 + 5 P2 + 2 P3 findings 全修（1 path-encoding 誤報排除），full suite 全綠。

- **新 module** `internal/module/backup/`（比照 sync 四層）+ 獨立 `backup.db`（無 FK，references
  在 handler 驗證，避開 #850 DSN-pragma 坑）：`backup_blobs`（sha256 去重、immutable）+
  `backup_snapshots`（append-only，parent_id DAG）。
- **API** `/api/backup/`：`POST /missing`（per-blob negotiation）、`PUT/GET /blob/{hash}`（raw
  blob，cap+1→413、sha256 驗證、冪等、404）、`POST /snapshot`（單一 `BEGIN IMMEDIATE` txn：
  read head→validate→content-keyed no-op→fork→insert→GC）、`GET /history`、`GET /snapshot/{id}`。
- **正確性保證**：content-keyed no-op（manifest==head 即不寫列，與 parentId 無關，跨裝置不產生
  重複 fork）；`is_fork` 僅 content differs 時評估；GC union keep-set（latest 100 ∪ 90 天 ∪
  ancestor closure，非 hard cap）+ blob refcount/grace；manifest canonical order + well-formed
  tree 驗證；`backup:done` 廣播僅 committed write（no-op 不廣播）。
- **DoS 防線**：snapshot/missing body streaming decode（item cap+1 即 413，不先 materialize）+
  `MaxBytesReader` 硬上限 + 外層 object 收尾/EOF 驗證（拒 truncated/trailing）。
- **體質**：store 拆 `blob_store`/`snapshot_store`/`query_store`/`gc` 分檔；serialisation 以
  deterministic `afterReadHead` barrier 測（broken 非交易實作穩定失敗）。

### 不在本階段
2b（前端備份引擎）、2c（前端還原 UI + fork 顯示）各自後續 PR。

## [1.0.0-alpha.302] - 2026-06-28

### Feat(storage): In-App upload / download / binary-disposition / quota (subsystem 1, Phase 1c) (#877)

子系統 1 第三階段：在 1a 巢狀樹 + 1b 巢狀 CRUD 之上補齊檔案進出。純前端（IndexedDB
`InAppBackend`），無 daemon/Electron-IPC 改動。Detailed plan 經 3 輪 codex review 收斂
SHIP-READY；4 個 TDD task 派 subagent；PR 兩輪深度 review（R1 標準 + R2 攻擊/防守/體質）
共 7 findings + 確認 review 補 1 項 defense-in-depth，全修後 full suite 3555 綠。

- **上傳**：toolbar file picker + OS 檔案拖入（native HTML5 drop，用 `dataTransfer.files`
  與 1b dnd-kit node-move 區分共存）；泛化 `createUnique(dir, base, ext: string, content?)`
  以 IDB `store.add()` 原子保留+寫入（不覆蓋；suffix `-N`）；**file.name sanitise**（展平
  basename、去控制字元、拒 `.`/`..`/路徑分隔符、`isUnderRoot` + traversal 段防線）防 path
  traversal/隱藏資料；`uploadFiles → UploadSummary{uploaded[], failed[]}` typed kind 回報。
- **下載/匯出**：單檔 `backend.read`→Blob→anchor download（byte-identical）；抽共用
  `lib/download-file.ts`。
- **非可預覽 binary 開檔→下載 disposition**：`openInAppFile` 對 `DOWNLOAD_EXTS`
  （docx/xlsx/zip…）read+download 不掛 editor；抽 leaf lib `lib/file-extension-roles.ts`
  （`roleForExtension`，registry 與 openInAppFile 單一 SOT）；修 .docx 掛 monaco 變亂碼。
- **軟上限 ~25MB**（write 前擋）+ **quota 錯誤**（`isQuotaError` lift 到 `lib/quota.ts`，
  write catch）→ inline banner warning/error 分流。
- 體質債延後 #872/#873（storage-actions/StoragePane 拆分，1c 已註記加劇）。

## [1.0.0-alpha.301] - 2026-06-28

### Feat(storage): In-App nested CRUD + recursive folder move (subsystem 1, Phase 1b) (#871)

在 1a 巢狀樹之上補齊資料夾級操作。純前端（IndexedDB `InAppBackend`），無 daemon 改動。
Detailed plan 經 3 輪 codex review 收斂 SHIP-READY；依 plan 切 8 個 TDD task 派 subagent 實作；
PR 兩輪深度 review（R1 標準 + R2 攻擊/防守/體質）共 9 findings，fix-wave 全修後確認 review
APPROVE-TO-MERGE。

- **統一 eager 新檔 namer（修 #854 dup-bufferKey race）**：`InAppBackend.createUnique` 用
  IDB `store.add()` 作單一序列化點原子保留檔名；`createUniqueInAppFile(dir, ext)` 收斂三個
  new-file 入口（StoragePane / EditorPane 新 buffer / EditorNewTabSection）。後者由 lazy
  in-memory `untitled:` 改為 eager 立即建檔（`.txt`/`.md` 以 `ext: 'md'|'txt'` 保留），既有
  `untitled:` runtime contract 不動。
- **資料夾可選 + 新增資料夾**：tree selection 從「只選檔案」擴成可選資料夾（單擊 name 選取 /
  caret 獨立展開 / modifier 多選）+ `targetDir` 衍生；toolbar New Folder（`mkdirUnique`
  add-reserve）+ New File 落在選中目錄。
- **改名（檔案 + 資料夾）**：`renameStorageEntry` 統一路徑、**恰好一次** `backend.rename`，
  把舊 `performBufferRename` 重構為 pure `remapPanesUnder`（不碰後端，re-point 含 editor /
  image / pdf 的後代開啟 pane）；碰撞 pre-check + 同名 no-op。
- **遞迴刪除**：locked/dirty 守衛擴成涵蓋資料夾後代（`filePath` 前綴比對）；重疊 target 正規化
  避免半刪；資料夾走遞迴刪除。
- **拖曳搬移**：dnd-kit `DndContext`（每列 droppable 攜帶權威 `targetDir`：資料夾→自身、
  檔案→父目錄、root→`/buffer`）+ pure `moveStorageEntry`（self/descendant/same-parent no-op
  + 碰撞）；保留 click-select / double-click-open / 鍵盤 a11y 共存。
- **體質**：`createUnique`/`mkdirUnique` 改 `SupportsUniqueCreate` capability interface（不再
  強制所有 backend stub）；DnD resolver 抽到獨立 `storage-dnd.ts`。延後體質債 #872-#875。

## [1.0.0-alpha.300] - 2026-06-27

### Feat(storage): In-App nested file manager — "Storage" (subsystem 1, Phase 1a) (#869)

把「Manage buffers」升級為完整的巢狀檔案管理 **Storage**（純前端，後端沿用
`InAppBackend` / IndexedDB，已支援巢狀）。spec + plan 各 3 輪 codex review 收斂後，
依 plan 切 10 個 TDD task 派 subagent 實作；PR 3 輪 review（R1 2 P1 / R2 1 high+2 medium /
R3 2 P2+1 P3）全收斂。

- **修兩個實測症狀**：① 清單裸文字、無法一眼識別 → 巢狀樹 + Phosphor 檔案/資料夾 icon +
  row metadata（size + 文字檔字數，二進位/超 256KB/未知型別只顯 size）；② smart-open 跨 tab
  劫持「開錯/開不了」→ 改走 `openInAppFile`（open-or-focus + registry dispatch：png→圖片
  檢視器 / pdf→PDF 檢視器 / md→編輯器；先 stat 確認存在才開，避免把 stale/已刪檔開成空白
  editor）。
- **巢狀樹**：`listTreeUnder` full-recursive + `useStorageTree`（full-path node identity）；
  展開收合；`STORAGE_ROOT` 集中原本寫死 7+ 處 `/buffer`。
- **元件拆分**：462 行 `EditorBuffersPane` → `storage/` 四模組（StoragePane 兩區框架含右側
  daemon 備份 placeholder / StorageTree / StorageRow / storage-actions，leaf CRUD 改
  full-path、保留 dirty/locked 守衛、rename/delete 同步 image/pdf preview pane）。
- **tab icon** 與 tree row 共用 `fileIconForPath`；breadcrumb popover 巢狀化並走
  `openInAppFile`；改名 Buffers→「Storage」（en + zh-TW「儲存空間」）。

範圍外（後續）：Phase 1b（mkdir / 資料夾搬移 + 後端遞迴 / 拖曳 / 統一 new-file namer #854）、
Phase 1c（上傳/下載 / quota）、子系統 2（daemon 備份還原 snapshot 版本庫，右側欄）。

## [1.0.0-alpha.299] - 2026-06-26

### Fix(sync+test): clear host token as null sentinel + fix stale TabBar tooltip test (#674)

修兩個 origin/main 既有、互相獨立的 baseline 測試失敗（共 4 個 test）。

- **Sync host token 清除契約**：`hostsContributor.deserialize` 在 host 為新增、或 id 撞名但
  endpoint（ip/port）不同時須清除 token（安全：避免 bearer token 重送到不同/惡意 daemon）。
  原寫 `undefined`，改採 **`null`** 明確 sentinel（「已清除，需 re-auth」），`null` 比
  `undefined` 多了跨 JSON persist 邊界存活的語意。`HostConfig.token` 放寬為 `string | null`，
  下游 `string | undefined` sink 於邊界 coalesce `?? undefined` 收斂。抽
  `mergeHostsPreservingTokens` helper 讓 **`full-replace` 與 `field-merge` 兩個 merge mode
  共用同一契約**（原 field-merge 在 conflict 選 remote hosts 時會靜默丟失所有本地 token）。
- **TabBar pinned tooltip stale test**：`HoverTooltip` 已 `createPortal` `role="tooltip"`
  到 `document.body`，test 卻在 tab 子樹內查 → 改用 `screen.getByRole('tooltip', { name })`
  按可及性名稱定位（純 test-only，無 impl 變更）。

## [1.0.0-alpha.298] - 2026-06-26

### Fix(editor): prevent Loading editor flicker on markdown buffer switch (#863)

同一 pane 從一個 markdown buffer（`wysiwyg` 模式）切到另一個 markdown buffer 時，UI
先 paint 一幀 `Loading editor…` fallback（閃爍 + 一瞬焦點中斷）才落到目標 editor。根因是
`attachPane` 為 post-commit `useEffect`：切 buffer 的第一個 render 仍看到舊 buffer 的
stale `paneState`（`editorMode=wysiwyg`、`bufferKey=舊`），stale `editorMode` 驅動
wysiwyg 分支，被 PR #862 的 gating render 擋成 `Loading editor…`。

- **修法**：render 階段同步派生 `alignedPaneState`（僅當 `paneState.bufferKey === key`
  才信任，否則退回 fresh-pane 預設 raw + null viewState）。`attachPane` 對齊後本就會把
  `paneState` 重建為這些預設值，故 stale 視窗直接 render 最終態的 raw Monaco —— 不閃爍，
  也不外洩舊 buffer 的 mode / viewState / cursor / diff 狀態到新 buffer。
- **連帶**：移除 PR #862 的 R3 post-commit gating fallback。wysiwyg 分支前提變為
  `bufferKey === key`，stale 視窗不可能 mount `TiptapEditor`，`didRestoreRef` 不會被
  stale `paneState` 污染（比 R3 更強的保護）。

## [1.0.0-alpha.297] - 2026-06-26

### Feat(editor): focus on switch + markdown wysiwyg scroll/cursor restore (#857)

切換到 editor 分頁時兩個 UX 缺陷：(1) markdown editor 不自動 focus —— focus 只依賴
`[isActive]`，但 editor 的 ready 時機（Monaco async `handleMount` / Tiptap lazy+Suspense）
可能晚於 `isActive` 變 true，`focus()` no-op 且不重試；(2) markdown wysiwyg(Tiptap) 切回
跳到最後 —— Tiptap 完全沒 viewState 持久化，切回 `setContent`+`focusEditable` 把游標拉到
末尾。

- **focus**：Monaco `handleMount` + Tiptap one-shot ready effect 在 editor ready 時若
  `isActiveRef.current` 補 focus（避 stale closure），保留既有 `[isActive]` effect。
- **viewState**：新增 `tiptapViewState` paneState 欄位 + `saveTiptapViewState`；Tiptap
  unmount（`useLayoutEffect` + `didRestoreRef` guard，避免 ref detach 後讀 scrollTop=0 /
  editor-ready commit 後立刻 unmount 漏存）存 scroll + selection（`type: 'text' | 'node'`，
  保留 `NodeSelection` 如 horizontal rule）；one-shot restore（selection→scroll→focus，
  paint 前 layout effect）。`resolveRestoreSelection` 用 **inlineContent 前置檢查**（本地
  實證 `TextSelection.create` 對非法位置不 throw、只 `console.warn`）。
- **EditorPane gating render**：僅在 `paneState.bufferKey === key`（paneState 已對齊 buffer）
  才 render `TiptapEditor` —— stale paneState 根本不 mount，避免 `React.lazy` cache 後同步
  mount 用 stale viewState 鎖 `didRestoreRef`。

spec 2 輪 + plan 2 輪 codex review；PR R1 標準 + R2 三平行（攻擊/防守/體質）+ R3 + R4 收斂
（D1 unmount race→`useLayoutEffect` / D2 NodeSelection / R3 props-gating→gating render /
R4 cosmetic 閃爍）。161 tests 綠、lint 0、build ok。延後追蹤：#863（markdown↔markdown buffer
切換 transient `Loading editor…` paint，根因既有 `attachPane` post-commit 時序）。

## [1.0.0-alpha.296] - 2026-06-26

### Feat(editor): persist In-App Storage to IndexedDB (fixes #856) (#858)

新建文件預設存到 In-App Storage（`inapp` source），但 `InAppBackend` 底層是純記憶體
`Map` —— 存檔對記憶體成功、UI 顯示已存，但 app upgrade/重啟即遺失，重開 tab（已
persist）變空白 + dirty。改用 **IndexedDB**（復用既有 `openIDB`）持久化，重啟後讀得回。

七個 `FsBackend` method 行為契約完全不變（只換儲存層）：`read`/`stat`/`rename` 對缺失
路徑仍 throw（`EditorPane` mount catch 依賴）；`write` 為單一 readwrite transaction，
auto-create parent dirs 且不驗證 parent 是否為 directory；`rename`/`mkdir` 保留
blind-overwrite；`rename` 只搬單一 entry（non-recursive）。

20 tests（AC1-14，含 persist-across-`closeAllIDB`、空 `Uint8Array` 與非文字 bytes 的
byte 級 round-trip、registry 薄整合驗證）。spec/plan 各一輪 codex review（各 4 finding
全修——點破 AC2 假驗 persist 須先 `closeAllIDB`、`indexedDB.deleteDatabase` 非 Promise
須 wrap）；PR R1 標準 0 finding + R2 三平行（攻擊確認 transaction auto-commit 安全、防守
0 blocker、體質 H1 拆測試 + H3 對齊 IDB upgrade style）收斂。

Scope = 止血（本地持久），不做跨機同步 / 衝突 diff。Known limitation：IndexedDB
per-origin，dev server 與 bundled `.app` 各自獨立 store。延後追蹤：#859（write quota
UI recovery）、#860（`list(missing)` cross-backend contract）。

## [1.0.0-alpha.295] - 2026-06-19

### Feat(editor): close dirty-guard, image viewer zoom, monaco scroll buffer (#852)

Three editor open/close/preview UX improvements addressing user requests:

- **Tab close dirty-guard** — `closeTab` scans the tab's pane tree and prompts
  `window.confirm` when any editor pane's buffer is dirty (including untitled
  drafts with unsaved content); cancelling aborts the close. A shared
  `bufferKey` helper was extracted so the guard, `EditorPane`, and
  `EditorBuffersPane` key buffers identically.
- **Image preview fit/actual zoom** — oversized images (natural > container)
  become click-to-toggle between fit (`object-contain`) and actual size with
  zoom-in/zoom-out cursors and a scrollable container; cached/HMR-complete
  images measure synchronously. Preview state (zoom/natural/objectUrl/error)
  resets on the composite `(identity, backend)` session.
- **Monaco scroll buffer** — `scrollBeyondLastLine: true` keeps a scroll buffer
  past the last line.

Two rounds of codex review plus three incremental rounds and a two-perspective
consulting pass converged the image-preview state machine (R5 0 findings).
86 tests / lint / build green. Follow-ups: #853 (broader dirty protection
across pane-level close / workspace move / window close), #854 (untitled name
race).

## [1.0.0-alpha.294] - 2026-05-05

### Fix(store): SQLite foreign_keys via DSN _pragma + pin trace migration to single conn (#849)

Investigating an 18.3 GB `agent_events.db` revealed that `agent_trace_steps`
had grown to 137,550 rows while `agent_trace_chains` was correctly capped at
exactly 10,000 — a 13.75 : 1 ratio that contradicts the schema-defined
`ON DELETE CASCADE`. Root cause: SQLite's `PRAGMA foreign_keys` is
per-connection, but the daemon enabled it via a single post-`Open`
`db.Exec("PRAGMA foreign_keys = ON")`. With `db.SetMaxOpenConns(4)`, only
~25 % of `BeginTx` calls landed on the FK-enabled connection. Cascade
silently no-op'd on the other 75 %, leaving orphan trace_steps every time
`pruneTraceChains` evicted a chain.

Fix moves FK enablement into the DSN `_pragma=foreign_keys(1)` parameter
(applied by `modernc.org/sqlite` at every new-connection dial) and
removes the post-`Open` `db.Exec`. The `:memory:` test path keeps the
explicit Exec since it always runs at `MaxOpenConns(1)` (no pool-miss
risk).

Round-2 codex adversarial review (3-way fan-out) caught a related
secondary bug: `migrateTraceDB` itself toggled `PRAGMA foreign_keys`
through the pool, conflicting with the new always-on DSN behaviour and
risking failed schema rebuilds against legacy DBs. Migration now acquires
a single `*sql.Conn` at entry, runs all DDL through that pinned
connection, and ends with a one-time orphan cleanup
(`DELETE FROM agent_trace_steps WHERE NOT EXISTS chain`) so existing
deployments are repaired idempotently.

Three rounds of codex review (R1 standard 0 / R2 三平行 4 dedup → 3 fixed
in PR + 1 follow-up issue / R3 standard 0). Follow-up #850 tracks
extracting the magic-`4` pool-size constant for test parameterization.

Live impact verified on mlab: DB shrank from 17 GB to 52 MB after a
manual SQL trim + VACUUM (this PR ensures alpha.294+ daemons no longer
leak orphans in the first place).

## [1.0.0-alpha.293] - 2026-05-04

### Fix(settings): Align modules switchboard with sidebar order (#843)

The Modules Switchboard now mirrors the Settings sidebar's relative
order for the disableable subset. Previously the Switchboard rendered
modules in registration order while the sidebar sorted by
`SETTINGS_ORDER` — same conceptual list, two different visible
sequences. The Switchboard sort key is now the minimum `order` across
a module's purdex contributions; tie-break by `module.id` agrees with
the sidebar's `(order, moduleId, localId)` triple comparator. `Sync`
remains sidebar-only because it is not `disableable`.

Browser and Files now declare a purdex-scope placeholder settings page
via the shared `PlaceholderSettingsSection` component. Every
`disableable` module now has a Settings sidebar entry — invariant **I1**
in the new spec — so the Modules Switchboard ↔ sidebar mental model
stays consistent. The Files purdex placeholder explicitly supersedes
§N3 of `2026-05-03-files-disableable-sr2-spec.md` (PR #833): the prior
"no global settings" UX argument is replaced with an explicit
placeholder so users following the Switchboard never end up with no
sidebar landing.

`SETTINGS_ORDER` is reordered alphabetically by **English short**
sidebar label (B / C / E / F / M / Sync). Constant names stay bound to
module identity (`MODULE_QUICK_COMMANDS` even though the sidebar shows
"Commands") so future label changes do not ripple into the constants
table. Two sidebar labels are shortened: `Performance Monitor` →
`Monitor` and `Quick Commands` → `Commands`. The Switchboard rows,
pane labels and inner page headings continue to use the full
`module.name` ("Performance Monitor" / "Quick Commands") — sidebar =
navigation short, switchboard / pane = identity full. Five new i18n
keys land in both English and 繁體中文.

`listContributions(scope)` is now the single source of truth for
sidebar order with a deterministic `(order, moduleId, localId)` triple
comparator. `SettingsSidebar` and `GlobalSettingsPage`'s default-mount
logic in `SettingsPage.tsx` both consume it without local re-sorts —
for equal-order pairs the auto-mounted default page can no longer
diverge from the first visible sidebar row. `dispatchSettingsContributions`
now throws at registration time when a `disableable` module declares
no purdex contribution, so authoring errors fail loudly instead of
silently leaving the module visible only in the Switchboard. The
Switchboard sort uses `reduce` (not `Math.min(...orders)`) to remove
the spread-arg upper-bound risk if a module ever declares many purdex
contributions.

PR review history: 1 spec-review round (3 medium → all addressed) +
1 plan-review round (4 medium + 2 low → all addressed) + R1 standard
PR review (0 finding) + R2 three-perspective adversarial review
(2 medium + 1 low → all fixed in-PR) + R3 standard verify (1 P2 → fixed
via centralized comparator) + R4 standard verify (0 finding).

Files: `register-modules/index.tsx`, `settings-order.ts`,
`dispatch-settings-contributions.ts`, `settings-contribution-registry.ts`,
`SettingsSidebar.tsx`, `ModulesSwitchboardSection.tsx`,
`PlaceholderSettingsSection.tsx` (new), `register-modules.test.ts`,
`register-modules.quick-commands.test.tsx`,
`settings-order-pr2.test.ts`, `dispatch-settings-contributions.test.ts`,
`PlaceholderSettingsSection.test.tsx` (new),
`ModulesSwitchboardSection.test.tsx`, `en.json`, `zh-TW.json`.

## [1.0.0-alpha.292] - 2026-05-04

### Feat(spa): Trailing-edge sliding debounce for error notifications (#842)

PR-B of the rate-limit-cleanup work (PR-A `#832` shipped at alpha.290).
Adds a 60s trailing-edge sliding debounce in
`useNotificationDispatcher.shouldNotify` for `derived === 'error'`,
keyed by `JSON.stringify([compositeKey, eventName, errorString])`.
First error in a bucket fires the OS notification; subsequent same-key
arrivals within the window extend `silentUntil` and return `false`. A
storm of 100 events ≈ 1 notification (≥99% suppression — spec AC5),
matching the dthn-class scenario where cc was firing ~1.5 Hz of
`PdxStopFailure` during a rate-limit window.

Cleanup runs on `clearSession` / `removeHost` via a single module-level
store subscription (no reverse import) plus a throttled TTL self-cleanup
(at most once per `ERROR_NOTIFY_WINDOW_MS`). The Map is hard-capped at
1000 entries with FIFO eviction so a high-cardinality `errorString`
storm cannot blow up the renderer. The `unread` badge and
`derived === 'waiting'` / `'idle'` paths are deliberately untouched —
debounce only gates desktop notifications, not the in-app sticky state.

Three rounds of codex review (R1 standard 0 / R2 三平行 6 dedup → 4
fixed in PR + 1 spec amend + 3 follow-up issues / R3 standard 0). R2
landed: colon-safe `removeHost` cleanup (\`startsWith\` instead of
\`split(':')[0]\`), throttled sweep + hard-cap, subscribe early-exit on
\`lastEvents === prevState.lastEvents\`, and a rewritten unread-badge
test that actually drives the suppression path. Spec AC9 was amended
retrospectively to mark the LOC cap as informational rather than a
hard ceiling.

Follow-up tracking: #844 (errorString normalization), #845 (extract
`errorNotificationDebounce.ts` module), #846 (test seam naming
review).

## [1.0.0-alpha.291] - 2026-05-03

### Fix(files): Files module disableable + close SR-2 (#833)

Closes SR-2 from codex review of PR #617. The Files module previously
declared its `projectPath` setting via the deprecated
`ModuleDefinition.workspaceConfig` API, whose render path
(`WorkspaceSettingsPage` → `ModuleConfigSection` →
`getModulesWithWorkspaceConfig()`) bypassed the Modules Switchboard's
`useModuleEnabledStore` filter. As a result, marking Files as
`disableable: true` would have been a lie — toggling it off would still
leave the workspace `projectPath` editor visible. SR-2's protective
workaround was to leave Files un-toggleable until the migration landed.

This release migrates Files to the contribution-registry path
(`settings: [{ scope: 'workspace' }]`), wires it through
`dispatchSettingsContributions`'s disable filter, and marks Files
`disableable: true`. The behavior matches Editor / Quick Commands /
Performance Monitor: toggling Files off in `/settings/module-config`
shows the ReloadBanner; after reload, the workspace settings page no
longer renders the Files header / `projectPath` input. Storage shape
is unchanged — `useWorkspaceStore.workspaces[wsId].moduleConfig.files.projectPath`
still backs the value, so all existing readers (`FileTreeView`,
`file-open-bootstrap.ts`) keep working without modification.

The PR also hardens `FileTreeView`'s `projectPath` selector (replacing
an unsafe `as string | undefined` cast with a `typeof === 'string'`
guard, defending against a poisoned-persist payload that could have
caused a render crash) and removes the now-empty
`DEPRECATED_LEGACY_CONFIG_EXEMPT` set in
`dispatch-settings-contributions.ts`. The
`SETTINGS_ORDER.WORKSPACE_FILES = 10` constant is added (first
workspace-scope entry), and the `settings-order.ts` docblock is
expanded to clarify per-scope ordering. Three new i18n keys land in
both English and 繁體中文.

PR review history: 2 spec-review rounds + 2 plan-review rounds +
1 standard PR review (0 finding, approved) + 1 three-parallel
adversarial PR review (0 P0/P1; 4 P2/P3 fixed in-PR; 5 P3 deferred to
follow-up issues #834-#839). The adversarial review caught one critical
escape-hatch issue early — removing the `<ModuleConfigSection>` mount
in `WorkspaceSettingsPage` would have detached the only render path
for any module still using the deprecated `workspaceConfig` API while
the API itself was kept alive — and the mount was restored in commit
`09bcc61e`. Full removal of the legacy renderer + `workspaceConfig`
field + helper functions is tracked as follow-up F-1.

Files: `register-modules/index.tsx`, `dispatch-settings-contributions.ts`,
`FileTreeView.tsx`, `FilesWorkspaceSettingsSection.tsx` (new),
`settings-order.ts`, `register-modules.test.ts`,
`WorkspaceSettingsPage.registry.test.tsx`,
`FilesWorkspaceSettingsSection.test.tsx` (new), `en.json`, `zh-TW.json`,
`docs/specs/2026-05-03-files-disableable-sr2-{spec,plan}.md` (new).

## [1.0.0-alpha.290] - 2026-05-03

### Fix(daemon): PdxStopFailure native subagent detach (rate-limit cleanup) (#832)

Closes the dthn-class accumulation bug where cc subagent failures via
the rate-limit error path leak native `SubagentRef` entries into
`agent_frames.subagents_json`. Empirical observation against dthn (pane
`%50`) showed **3944** native refs accumulated, with **97.9%** of
`SubagentStart` events terminating via `PdxStopFailure` (carrying the
failing subagent's `agent_id`) instead of `PdxSubagentStop`. The
existing `LifecycleStopFailure` handler dropped the payload's `agent_id`
on the floor when `frame != nil`, so native refs had no GC path —
projection broadcasts grew without bound and SPA showed permanent
"subagent in flight" lights.

This release teaches the daemon to detach matching native refs when
`PdxStopFailure` carries an `agent_id`. Three providers (cc/codex/
opencode) now surface `agent_id` in `DeriveResult.Detail`. A new pure
helper `findNativeRefByID` gates the detach so a non-matching payload
never triggers a phantom mutation. The `LifecycleStopFailure` case is
split from `LifecycleStop`; the new branch invokes the new
`mutateSubagentsAndStatusWithRetry` helper which atomically removes the
ref **and** writes `Status=error` in the same `UpsertIfUnchanged`
transaction. Four disjoint outcomes (`Detached` / `RefAlreadyAbsent` /
`FrameMissing` / retry-exhaustion-error) each map to a distinct trace
reason — retry races no longer emit phantom detach traces, and retry
exhaustion no longer collapses into `frame_missing`. New trace reason:
`native_subagent_detached_on_stop_failure`. Existing `frame_missing`
reason reused for concurrent SessionEnd race.

Three rounds of codex review converged across spec (P1×3 → P1×2 → fact
+ P2), plan (P1×3 → P1 + P2×3 → fact), and code (R1 P1 atomic write →
R2 three parallel adversarial 2 high + 2 medium → R3 0 finding). PR
size: ~225 production + ~720 test LOC across 7 commits, exceeding spec
AC9 cap of 500 in service of race-safety guarantees forced by R2 review.

Operational follow-up: dthn pane `%50` retains 3944 historical refs
whose terminal `StopFailure` events predate trace retention; these
won't self-heal via the new code path (cc emits each `agent_id`'s
terminal event exactly once). One-shot SQL cleanup per spec §5
(backup → `BEGIN; UPDATE agent_frames SET subagents_json='[]' WHERE
json_array_length(subagents_json) > 100; COMMIT;` → daemon restart) is
the expected primary recovery path.

PR-B (SPA notification debounce for derived=error during rate-limit
storms) is a separate forthcoming PR per spec §11 sequencing.

## [1.0.0-alpha.289] - 2026-05-03

### Feat(lights): cc Bash sniff delegating flag for codex visibility (#829)

Closes issue #821. When cc invokes `/codex:rescue`, `/codex:review`, or
`/codex:adversarial-review`, the cc tab now shows the **orange dot**
(delegating) instead of the misleading **blue native dot**. Pure cc-side
detection via `PreToolUse(Bash)` command sniff with token-boundary check
on `codex-companion.mjs` — no codex plugin cooperation, no `IsProxy`
invariant pollution, zero dependence on governance phases.

Adds two omitempty fields to `SubagentRef` (`Delegating bool` +
`DelegatingToolUseIDs []string`, invariant
`Delegating == len(DelegatingToolUseIDs) > 0`), a pure-function
`ExtractDelegationHint` extractor with no-regex token detection, two
`mark` / `unmark` helpers mirroring the `upsertProxyRefForBroker`
optimistic-concurrency retry loop, and a handler wiring block that
mark/unmark by `tool_use_id` on cc PreToolUse / PostToolUse /
PostToolUseFailure. SPA `SubagentDots` composes
`is_proxy || delegating` to render orange and exposes a new
`data-delegating` attribute alongside the unchanged `data-is-proxy`.

To reach the new wiring on cc's side, this release upgrades
`PdxPreToolUse` and `PdxPostToolUseFailure` from
`HookHandlingUnsupported` / `HookHandlingIgnored` to detail-only
installable: `cc/status.go` returns `Valid=true` with `Status=""` and a
`tool_use_id` / `agent_id` detail, and `cc/events.go` drops the handling
annotation so `mergeClaudeHooks` writes both keys into
`~/.claude/settings.json` on hook merge.

A handler short-circuit guards detail-only PreToolUse / PostToolUseFailure
from `applyFrameEvent`'s `LifecycleNone+Status=""` fallback so the new
events cannot resurrect torn-down frames as `StatusIdle`. Mark/unmark
lookup excludes `IsProxy=true` refs to keep the visual signal native-only
even if a proxy ref happens to share an ID with a native subagent.
`DelegatingToolUseIDs` is capped at 32 entries with FIFO evict-oldest so
a degenerate hook stream cannot inflate `subagents_json` unboundedly;
the Delegating flag stays true throughout cap enforcement so the
visual signal does not flap.

User action required for existing managed installs: cc's
`~/.claude/settings.json` must be re-merged so `PreToolUse` and
`PostToolUseFailure` hook entries land — run `pdx install hooks`,
relaunch the daemon with the managed-install path, or use Settings →
Hooks → Reinstall. Fresh installs pick up the new keys automatically on
first hook merge.

## [1.0.0-alpha.288] - 2026-05-03

### Refactor(settings): PR-2 — Editor consolidation, Sync modularize, Quick Commands header (#825)

Completes the settings architecture cleanup started in alpha.287 PR-1. The
Editor module's three sidebar entries (`editor` / `link-detect` /
`open-behavior`) collapse into a single Editor settings page with the two
removed sections rendered as embedded subsections; `/settings/link-detect`
and `/settings/open-behavior` URLs alias to `/settings/editor` via the
extended `URL_ALIASES` map. Sync upgrades from a built-in legacy section
to a structural module (non-`disableable`), so it picks up the puzzle icon
and the modules-group ordering naturally without changing engine
lifecycle. Quick Commands settings adopts the Appearance/Terminal header
pattern (bare `<div>` outer, `<h2>` + `<p>` description). All module-owned
order values now flow through `SETTINGS_ORDER` constants — the PR-1
transitional `*_PR1` keys are removed in favor of the final
`MODULE_EDITOR=11`, `MODULE_QUICK_COMMANDS=12`,
`MODULE_PERFORMANCE_MONITOR=13`, `MODULE_SYNC=14` values.

URL alias resolution uses `Object.hasOwn` to guard against
prototype-property collisions (e.g. a hypothetical section with localId
`constructor`) and self-heals to `firstSelectable` instead of a stale
`lastSection` whenever the canonical alias target is unselectable —
covering both fresh-mount and mounted-then-navigate paths.

Follow-up issue #822 tracks extracting `<ModuleOwnedPuzzleIcon>` as a
shared component across the three sidebar callers.

### Fix(agent): ignore detached frames in pane projection (#826)

Pane/session projections now ignore alive-but-detached agent frame rows whose
process no longer belongs to the current tmux pane tree. This prevents stale
standalone Codex broker frames from becoming the tab's top agent after the real
foreground agent exits; when no pane-owned frame remains, the projection emits
`status=clear` so the SPA returns to the terminal icon.

The filter is projection-only and fail-open on transient tmux/process lookup
errors. Detached broker rows remain in the database for telemetry/history; they
are just ineligible to represent the current pane unless their process ancestry
includes the pane PID and their PID start time still matches.

## [1.0.0-alpha.287] - 2026-05-03

### Refactor(settings): PR-1 sidebar order and spacing cleanup (#816)

Adds the first settings architecture cleanup slice: centralises sidebar order in
`SETTINGS_ORDER`, applies the PR-1 transitional settings order, switches puzzle
icons to upright bold styling, and removes duplicate Modules Switchboard padding.
This keeps the visual/sidebar cleanup separate from later Editor consolidation
and Sync modularisation work.

### Fix(spa): silence empty opencode stop notifications (#823)

OpenCode `PdxStop` events derived from `session.status.idle` now carry
`notification_silent=true` metadata. The SPA still applies the idle status for
lights, but skips unread markers and desktop notifications for these empty Stop
events so users no longer see generic `Task completed` notifications with no
actionable content.

## [1.0.0-alpha.286] - 2026-05-03

### Feat(codexbroker): P2 — decision predicates + kill sequence + manual sweep API (#813)

Phase P2 of the codex broker governance series. Builds on alpha.280 P1
inventory; adds decision evaluation (predicates A/B/C + stale-running
detection + emergency overrides E1/E2 + foreign-broker quarantine) and the
operator-driven kill sequence (Step 0 identity verify → audit preimage →
graceful RPC → SIGTERM → SIGKILL → verify-gone → cleanup with socket-inode
verification + audit postscript). New endpoint `POST /api/codex/brokers/sweep`
with `mode=dry-run|apply` and optional `brokerKey` filter.

Mass-kill safety guarantees on mlab steady state (50+ pre-existing brokers
with empty launch registry):

- Unfiltered `mode=apply` issues zero kills against any foreign broker —
  every record's `Reason="foreign_quarantine"` blocks the unfiltered path.
- Operator override `mode=apply&brokerKey=<X>` (the spec §5.1 line 371
  manual override semantic — no separate `force` flag) is the only path
  that can kill a foreign broker, and only when baseline `Kill=true`
  (¬A∧¬B∧¬C ∧ idle expired). Alive predicates ALWAYS protect.
- Identity-mismatch on Step 0 re-verify (and re-verify before every
  signal) auto-quarantines the broker rather than retrying.
- Quarantine load corruption fails closed: apply returns 503 until the
  operator clears the renamed `.bak` file and restarts the daemon.

Concurrency model: two-layer `sync.RWMutex` (`globalApplyMu` + per-broker
`sync.Map[brokerKey]*sync.RWMutex`) with fixed lock order — deadlock
impossible by construction. Unfiltered dry-run takes the global write
lock to avoid lock-storm on 50+ broker scans. `quarantineMu` serialises
shared quarantine state across concurrent filtered applies. Identity
re-verification happens immediately before EACH `syscall.Kill` to defend
against PID-reuse during the audit/graceful window.

Plan + PR went through 5 rounds of plan review (converged at 0 finding)
and 4 rounds of PR review (R1 standard + R2 three-parallel adversarial
+ R3 standard + R4 standard final 0 finding) before merge. 27 commits
on top of plan v5; full audit trail in commit messages.

Known follow-up gaps tracked as separate issues:

- Darwin sockverify: CGo-free libproc binding (currently uses `lsof`
  fallback; spec §5.4 line 472 explicitly accepts `lsof` as bounded
  fallback).
- Linux sockverify: inode-based verification via parsing `socket:[inode]`
  fd targets (currently forces `lsof` fallback after R1 finding caught
  path-comparison bug).
- Integration test: full sweep contract assertion gated on real broker
  discovery; sandbox uses `PDX_INTEGRATION_SKIP_ON_DISCOVERY=1` env var
  opt-out.

P3 (automatic triggers, daemon tick, ExitWorktree hook, SPA dashboard)
deferred to later PRs per spec §9 build sequence.

## [1.0.0-alpha.285] - 2026-05-03

### Fix(spa): clear stale subagent dots (#814)

Fixes stale subagent dots when the backend explicitly broadcasts
`subagents:null`. The SPA now treats explicit `null` the same as an empty
subagent list while preserving the existing no-op behavior when the field is
absent. The agent projection path also serializes empty subagent lists as `[]`
instead of `null` so reconnect and hook broadcasts stay consistent.

## [1.0.0-alpha.284] - 2026-05-02

### Feat(codex): W6-6 ScreenChange ProbeIntent for permission-reply detection (#799)

Last piece of the Lights rebuild W6 series. Codex emits no PdxStop hook when
the user replies to a `PdxPermissionRequest` waiting state by pressing enter
to approve — the lights would stay stuck in `waiting`. W6-6 introduces a
ScreenChange ProbeIntent that uses `Prober.Watch` to observe the 10 lines of
pane content above the input row; once ScreenStable lands, the first
ScreenChanged event emits a Signal that flips status to `running`.

PR-level highlights:

- 2-phase + 2-case truth table contract (v5 user reframe).
- Reuses W6-3 ProbeIntentProvider interface + dispatcher 5-case lifecycle.
- Reject path race covered by J3 dispatcher generic pre-grace (alpha.281).
- v6.1 detector close-race fix: mutex-protected `closed bool` flag.
- v6.2 watchLoop baseline retry: capture failure no longer cleanup-returns;
  500ms loop waits for the next successful capture.
- Ownership-aware teardown: Prober.WatchHandle + StopWatchOwned avoid
  same-paneID re-arm cancelling new watcher (W3 legacy / sweep keep
  StopWatch since they own target end-to-end).
- Post-grace by-Kind: ScreenChange (hook-armed observer) skips the 2s
  suppression that ProcessDead (race competitor) needs.
- Spec §0.4 / §4.3 / §8 reframe (F7-3): baseline failure is a probe-layer
  transient, not a detector lifecycle signal; 3 drift anchors guard against
  future regression.
- 6 rounds of PR codex review converged: R1 P2 (StopWatch race), R2 three-way
  adversarial (3 findings), R3 P2 (post-grace by-Kind), R4 P2 (Watch baseline
  fail), R5 P2 (tight rearm loop), R6 standard 0 finding.
- Known limitation: case 2 (quick-approval / fast-with-output) — armed=false
  drops ScreenChanged → no emit → idle via PdxStop, lights waiting → idle
  skips running phase (by-contract, spec §0.5).
- F4 (50ms cleanup sleep brittle) → issue #800.

Closes #762 (Lights rebuild umbrella).

## [1.0.0-alpha.283] - 2026-05-02

### [L2] Codex broker turn-aware proxy detach on Stop (#801)

Codex broker (long-lived `codex resume <thread>`) under cc/opencode parent
now uses turn-aware proxy ref identity for Stop detach. The pre-L2
SourceTurnID-blind wildcard detach sometimes dropped the wrong turn's ref
on legitimate intra-broker turn-change races; L2 stamps each
UserPromptSubmit/PreToolUse upsert with a `turn_id` (parsed from codex
raw_event) and routes Stop through three sub-cases (spec §3.3.D):
targeted detach when Stop's turn matches; skip when broker's ref carries
a non-empty turn but Stop's turn is missing/malformed (preserves the
dot — Stop is unreliable signal there); empty-turn fallback with
turn-aware filter (`turnID==""`) re-verifying the empty state inside the
optimistic-concurrency loop, so a concurrent upgrade is no longer
wildcard-dropped.

PR-level highlights:

- Spec v5 final after 5 rounds of codex spec review.
- 11 TDD commits across 3 phases (P1 catalog/parse, P2 helpers, P3
  dispatch + 22 spec §5 rows + concurrency rows 14/15/15b/16/19).
- 4 rounds of PR codex review:
  R1 standard → 1 blocker (PreToolUse no-parent short-circuit).
  R2 three-way adversarial → 10 findings adopted (A1 TOCTOU / A2
  turn_id preserve / A3 sender_start_time canonicalization / A4
  turn_id length cap / D1-D4 defensive rows + trace audit / F3 file
  split / F6 line-number cleanup).
  R3 standard → 1 P2 (round-2 A2 vs round-3 P2 trade-off boundary).
  R3 consulting (independent codex session) → confirmed physical
  trade-off, recommended option-1 revert.
  R4 standard → 0 finding.
- A2 production reverted to unconditional overwrite (round-3 P2
  resolution); spec §3.5 known-limitation #5 documents the trade-off.
- 5 follow-up issues opened: #803 (race-mode controlled interleaving),
  #804 (`applyFrameEvent` dispatcher split), #805
  (`frame_ops_proxy.go` extraction), #806 (`TurnIdentityProvider` for
  cc/opencode L3 forward-compat), #808 (explicit unknown/new-generation
  turn state to break the SourceTurnID single-field trade-off).
- A6 (PID-reuse triple collision) and F5 (SenderTurn naming) accepted
  without change.

## [1.0.0-alpha.282] - 2026-05-02

### Fix(spa): preserve dot-slash terminal file links (#807)

Terminal file-link detection no longer slices `./CLAUDE.md` into `/CLAUDE.md`.
The absolute path matcher now treats `.` as a path-prefix boundary, while the
relative-slash matcher continues to recognize the full `./...` path.

## [1.0.0-alpha.281] - 2026-05-02

### Feat(agent): J3 ProbeIntent dispatcher bidirectional graceWindow (#797)

W6-6 ScreenChange ProbeIntent 前置 PR — 將既有 `probeGraceWindow`
（post-direction only：hook 後 2s drop probe）擴成雙向。dispatcher
`consumeSignals` 在進入 `applyProbeGuards` 之前先做 signalAt-based
post-grace pre-check（preserve 2s threshold），再 hold 300ms
`probeIntentPreGraceWindow`，期間 hook 到 → drop pre-grace；ctx cancel
→ classify 為 hook race vs lifecycle cancel。

Generic 對所有 ProbeIntent Kind 適用（W6-3 ProcessDead / W6-6
ScreenChange / 未來 Kind），per fix-spec §3 不為單一 Kind 特化約束。

mlab live verify：
- A11 SIGKILL ✅ PASS — codex pid SIGKILL → status=error end-to-end ≤1.5s
- A12 daemon replay ✅ PASS — restart → replayStatus → re-arm ~2s
- A9/A10 deferred to W6-6 PR（W6-6 ScreenChange 未 merge → 無可量對象）

5 輪 codex review 收斂（R1 standard 0 / R2 三平行 adversarial 3
finding 全採納 / R3 P1 + R4 P2 + R5 0 finding），spec v7.5 + plan v3。
W6-3 spec §9.14 加 generic-Kind drift anchor。

R13 boundary race 為 known limitation — fail-fast handling：mlab
A9 PASS = R13 acceptable；A9 fail = followup issue 加 per-event trace。

## [1.0.0-alpha.280] - 2026-05-01

### Feat(codexbroker): P1 inventory endpoint (read-only, governance phase 1) (#792)

First step of the codex broker / app-server governance feature (kickoff
`kickoff_codex_broker_and_lights_governance.md` → governance P1). Adds a
new daemon-side `internal/codexbroker` package and `GET /api/codex/brokers`
endpoint that enumerates every codex broker process, state directory, and
socket directory visible on the host with full attribution and a closed-
list anomaly schema. **Read-only** — zero side effects on broker
processes or filesystem state.

Live mlab verification at ship time:

```
summary.total=66 withProcess=42 withStateDir=55 withSocket=42
scanSourceTimeouts=[] partial=false p95=69ms
ps count 42→42 unchanged across two scans
```

P2 (decision + kill), P3 (trigger), P4 (SPA dashboard) and the four lights
phases (L1–L4) ship in subsequent PRs per the same kickoff. Issue #668
(codex broker orphan tracking) stays open until P3 lands.

Two rounds of codex review (R1 standard, R2 three-parallel adversarial)
caught nine blockers total — all fixed in-PR. Three deferred follow-ups
tracked as #793 (hung-syscall goroutine leak under truly-stuck FS),
#794 (AnomalyCode runtime emission enforcement), #795 (file-quality
cleanup: split `reconcile.go` / unify naming / unexport / move test
seams).

Spec drift correction along the way: original §3.4 specified NFC + APFS-
aware case-fold inside `BrokerKey`, but live integration revealed this
desyncs from codex CLI's own state-dir naming (raw `realpathSync.native`
bytes). The hash is now byte-faithful to codex; collision detection
moves to a P2 anomaly check rather than touching the primary key.

## [1.0.0-alpha.279] - 2026-05-01

### Feat(agent): cc PdxPostToolUse → running status (W6-1a) (#790)

Promotes cc `PdxPostToolUse` from `Handling=Ignored` (non-installable) to a status-emitting hook with `EmitsStatus=[Running]`, closing the W5-1 gap where lights stayed yellow after a permission grant until `PdxStop` flipped to idle — skipping the running phase entirely. Now `PostToolUse` gives `waiting → running` a real hook signal.

mlab live verify (cc 2.1.123): `Write(/tmp/...)` permission flow shows `PdxPermissionRequest` → waiting, `PdxNotification(permission_prompt)` → waiting (dual fire confirmed, both map to waiting), user approves, **`PdxPostToolUse` → running** (the W6-1a fix), then `PdxStop` → idle. cc installable hook count goes from 9 to 10; existing installs auto-pick-up the new hook on next `pdx setup --agent cc`.

Also includes a documentation-only outcome for W6-2 (cc compact): `SessionStart(source=compact)` does fire on `/compact` end but the daemon's `compact_ignored` branch is intentional and trace-only. Pre-compact `PdxStop` already settles status to idle in all observed scenarios, so no code change is needed for W6-2 (verified-only).

## [1.0.0-alpha.278] - 2026-05-01

### Fix(daemon): drop sweep idle_timeout — alive idle session no longer cleared (#788)

Removes `sweep.go` path 3 (`idle_timeout`) which marked frames as cleared when their PID was still alive but no hook had bumped `LastSeenAt` for ≥ 1 hour. The invariant was wrong: cc / codex / opencode are long-running processes and an idle session for an hour is normal usage — not a "logically dead" agent. True process death is already covered by `pid_dead` (PID gone) and `pid_reused` (`process_start_time` mismatch) without time bound. The 1h destructive sweep led to all SPA tab lights being silently cleared after a quiet hour even though every agent was still running; `clearSession` then dropped the keyed status map entry and indicator dots stopped rendering until the next hook landed.

`frameIdleThreshold` const is removed. `nowFn` is preserved (still consumed by `canonicalizePane` / `pruneDeadProxyRefs` / `afterFrameCleared` for deterministic broadcast timestamps in tests). Future SPA-side idle-duration display goes through `frame.LastSeenAt → broadcast → SPA` directly (issue #787) and does not require sweep-side detection.

Tests: IS1 rewritten as inverted invariant (`alive + identity-verified frame with stale LastSeenAt is preserved + emits zero broadcast`); IS2 / IS3 / IS5 / IS6 deleted (all defensive against the removed path); pid_dead orphan watcher cleanup still covered by IS4; `setProjectionTopStatus` narrow-update contract still covered by IS7.

## [1.0.0-alpha.277] - 2026-04-30

### Fix(spa): W6-LightsUI 4 lights polish (#783)

Closes #762. Four self-contained SPA lights fixes after W1 audit + W6-3+W6-4 ship: `useAgentStore` clears leftover unread when status flips to running so a background tab marked unread by a prior waiting/idle no longer keeps the red dot indefinitely; `TabStatusIndicator` overlay dot drops `animate-breathe` when `isUnread` so the unread tint stays static; `TabStatusIndicator` switches `WarningDiamond` weight from `duotone` to `fill` for crisper error rendering; `SubagentDots` now colors by `is_proxy` (proxy = orange, native = blue) instead of agent type family and drops the hollow-ring proxy variant since the parent agent icon already conveys type.

## [1.0.0-alpha.276] - 2026-04-30

### Perf(daemon): hook pipeline fast-path + sqlite tail tuning (#776)

Hook hot path no longer fans out `1 + 7×S` tmux subprocesses per event. The agent module accepts a new `tmux_session_id` field in hook payloads and resolves the session code via `EncodeSessionID` — a pure O(1) function that bypasses the name cache and `ListSessions` entirely when the ID is present. Legacy hook clients (older `pdx hook` binaries that send only `tmux_session`) still work via a `LookupCodeByName` fast-path with 250 ms TTL plus three-layer invalidation (HTTP handlers, watcher hash change, watcher wait-for) and a name-path fallback safety net.

The session module gains a `nameCache` keyed by tmux name and invalidated on create/rename/delete, on watcher-detected hash changes, and at the start of every `broadcastSessions` call so external `tmux kill-session` / `new-session` / `rename-session` operations are reflected within ~50 ms via the daemon's tmux hooks. The agent module logs a startup line indicating whether the fast path is active so decorator-wrapped providers that drop the `LookupCodeByName` interface are visible at boot.

SQLite stores add `_pragma=busy_timeout(500)` and pool caps (4/4 for the agent DB shared by frames and trace, 2/2 for meta) — transient contention now waits up to 500 ms instead of aborting, without changing durability (`synchronous=FULL` retained).

Hook session resolution is labelled with `id_path` / `id_empty` / `malformed_id` in broadcast log reasons so operators can grep migration status. The `TestResolveSessionCodeFromHook_TrustsIDOverMismatchedName` test locks the trust contract: a valid `tmux_session_id` is authoritative; the daemon does not cross-validate against `tmux_session`/`tmux_pane_id`.

Live verify on mlab measured 5/6 hook chains completing `[hook] trigger → [broadcast]` within the same daemon-log second (vs ~5 s baseline). Follow-ups: #781 (system-wide session_id-keyed identity) and #782 (`log.Lmicroseconds` for sub-second timing measurement).

Closes the round-2 stale-name-reuse race finding via the immutable `tmux_session_id` path; legacy fallback retains the bounded race window covered by `TestLookupCodeByName_NameReuseAfterInvalidate`.

## [1.0.0-alpha.275] - 2026-04-30

### Feat(spa): add performance monitor settings page (#779)

Moves Performance Monitor discovery into Settings as a module-owned page while keeping the existing `memory-monitor` module id and pane renderer for restore compatibility. The settings page is hidden when the monitor module is disabled.

Performance Monitor is no longer offered as pane replacement content from `NewPanePage`, matching the settings-first entry point.

## [1.0.0-alpha.274] - 2026-04-30

### Feat(agent): W6-3+W6-4 codex error/clear ad-hoc ProbeIntent — first per-agent probe (#765)

Lights system gains its first ad-hoc per-agent ProbeIntent — codex `ProcessDead` detector polls senderPID + paneID and emits a single `Signal` once either invariant fails, recovering the missing `StopFailure` (W6-3 error light) and `SessionEnd` (W6-4 clear light) transitions that codex CLI 0.124.0 does not fire.

The dispatcher introduces a 5-case lifecycle (case 1 noop / 2 arm / 3 stop / 4 already-armed noop / 5 target-mismatch rearm), a generation-scoped teardown helper, fail-closed handling for unsupported kinds, and replay recovery so daemon restart re-evaluates active intents.

Five rounds of codex review surfaced 11 findings, all addressed: F1 graceWindow strand + rearm, F2 generation-scoped teardown race, F3 PID reuse → issue #777, F4 hint validation revert, F5+F7 reconcile/stopAll observability + helper extraction, F6 unsupported-kind fail-closed, F8 `HasPane (bool, error)` + tmux transient-vs-confirmed-absent semantics, F9 nil-traceSink panic, F10 "no server running" → confirmed absence.

Closes #698 (daemon restart activeWatchers recovery). Status flips: W5-4 (codex error) ✅ / W5-5 (codex clear) ✅.

## [1.0.0-alpha.273] - 2026-04-30

### Chore(spa): update performance monitor labels (#774)

Updates the monitor module display name, pane label, provider title, and module description to Performance Monitor in English and Traditional Chinese while retaining the existing `memory-monitor` pane kind and module id for restore compatibility.

## [1.0.0-alpha.272] - 2026-04-30

### Test(spa): cover disabled monitor polling (#772)

Adds a regression test for the disabled `memory-monitor` module path. The test verifies that the disabled module placeholder renders instead of mounting `MemoryMonitorPage`, and that daemon monitor fetches plus Electron client metric pulls remain inactive across fake timer advancement.

## [1.0.0-alpha.271] - 2026-04-30

### Feat(spa): poll monitor client metrics (#770)

Polls Electron client metrics through `getProcessMetrics()` at the effective Performance Monitor refresh interval instead of relying on fixed push updates. Matching rows now render client CPU, memory, and browser-view state.

Client metrics are scoped by both pane id and pane kind so stale browser metrics are not shown after a pane id is reused for non-browser content.

## [1.0.0-alpha.270] - 2026-04-30

### Feat(spa): show selected monitor top processes (#768)

Adds selectable Performance Monitor pane rows with an accessible control linked to the top-process detail panel. Selecting a daemon-backed session row now shows daemon-reported top processes with PID, CPU, and memory details.

Selected rows without daemon metrics or reported top processes show explicit empty states, and the new labels are localized in English and Traditional Chinese.

## [1.0.0-alpha.269] - 2026-04-30

### Feat(spa): add monitor config controls (#766)

Adds Performance Monitor controls for refresh interval and top-process limit using daemon-provided bounds. Updates persist through the monitor config API and trigger an immediate monitor reload so new refresh cadence takes effect without waiting for the old timer.

The controls preserve dirty drafts across polling refreshes, avoid submitting blank drafts, and guard pending update responses across host endpoint changes and active-host switches.

## [1.0.0-alpha.268] - 2026-04-30

### Feat(spa): fetch monitor rows per host (#763)

Fetches monitor snapshots for every distinct host-bound `tmux-session` pane, so Performance Monitor rows can show daemon metrics for non-active hosts without colliding on equal session codes.

Active-host summary rendering now remains independent from slow or failing secondary host snapshots. Secondary row snapshots settle per host, use bounded timeouts, preserve last matching metrics across active refreshes, and are invalidated when the host record or endpoint identity changes.

## [1.0.0-alpha.267] - 2026-04-30

### Feat(spa): show monitor session metrics (#760)

Wires daemon session metrics into Performance Monitor rows for active-host `tmux-session` panes. Matching rows now display daemon CPU, memory, and process count using the current monitor snapshot's `sessions[].session_code` data.

Rows from other hosts and active-host session panes missing from the snapshot stay explicitly marked as not wired, avoiding accidental cross-host metric attribution. Daemon unavailable reasons now use localized stable reason text.

## [1.0.0-alpha.266] - 2026-04-29

### Feat(spa): show monitor pane rows (#758)

Adds the first Performance Monitor tab-row slice. The page now renders an Open Panes table with one row for every open leaf pane, including split-pane leaves, and shows stable `{tabId, paneId}` identity plus pane kind.

Rows follow the active workspace tab order when present, falling back to global tab order otherwise, and skip stale tab ids safely. Client and daemon metric cells are explicitly marked as not wired yet so later slices can attach Electron client metrics and daemon session matching without changing row identity.

Adds localized Open Panes labels and empty-state text in English and Traditional Chinese.

## [1.0.0-alpha.265] - 2026-04-29

### Feat(spa): show monitor host summary (#756)

Rewrites `MemoryMonitorPage` into the first daemon-backed Performance Monitor UI slice. The page now fetches monitor config and snapshots for the active host, no longer requires Electron metrics to render, and displays host CPU, memory, disk, sample time, and effective refresh cadence.

Snapshot polling follows the daemon config interval, avoids fallback daemon requests when the active host record is missing, preserves the last host summary through transient refresh failures, and keeps retrying after errors. Loading/error states now expose live-region roles.

Adds localized Performance Monitor host-summary labels and unavailable reason text in English and Traditional Chinese. Full tab table, client metrics, and monitor settings controls remain deferred.

## [1.0.0-alpha.264] - 2026-04-29

### Feat(spa): add monitor host API helpers (#754)

Adds SPA TypeScript types for the daemon monitor snapshot and config contracts, including nullable host/session metrics, top processes, and config bounds.

Adds `fetchMonitorSnapshot`, `fetchMonitorConfig`, and `updateMonitorConfig` helpers through `hostFetch`, with tests covering auth headers, non-OK errors, JSON PUT bodies, and nullable response typing.

This is the first SPA integration slice for the rewritten Performance Monitor; UI wiring remains deferred.

## [1.0.0-alpha.263] - 2026-04-29

### Feat(monitor): stabilize API contract fields (#752)

Adds handler-level monitor API contract coverage for snapshot JSON shape, stable units, sample timestamps, config bounds, and partial config updates.

Monitor host metrics and session daemon metrics now serialize `unavailable_reason` as a stable nullable field. Available metrics return `null`, while pending or unavailable metrics return the corresponding reason string, giving future SPA consumers a consistent response shape.

This remains a daemon-only API contract slice; SPA host API types and UI integration remain deferred.

## [1.0.0-alpha.262] - 2026-04-29

### Feat(monitor): single-flight snapshot sampling (#750)

Adds single-flight coordination for stale daemon monitor snapshot sampling. Concurrent stale snapshot requests now share one in-flight collection instead of multiplying host, tmux, or process-table scans.

Expensive snapshot collection now runs outside the monitor snapshot state lock, so monitor config updates can invalidate cache without waiting for collectors. Cache writes are guarded by a snapshot generation so in-flight results from an older config cannot repopulate stale cache after invalidation.

This remains a daemon-only, non-breaking slice; SPA UI and daemon CPU-delta refinements remain deferred.

## [1.0.0-alpha.261] - 2026-04-29

### Feat(monitor): add bounded top processes (#748)

Adds bounded daemon `top_processes` for monitor session metrics. Top processes are sorted deterministically by CPU descending, memory descending, then PID ascending, and are truncated by the effective daemon `top_process_limit` config.

Session totals remain inclusive of all pane root processes and descendants even when the top-process list hides lower-ranked processes. Unavailable session metrics now serialize `top_processes` as an empty array while keeping numeric daemon fields as explicit `null` values.

This remains a daemon-only, non-breaking slice; SPA UI, single-flight sampling, and daemon CPU-delta refinements remain deferred.

## [1.0.0-alpha.260] - 2026-04-29

### lights rebuild W3+W4: revert ProbeProfile framework + observability dev log (#744)

Reverts the always-on probe framework introduced by Phase 4a-1 (issue #719) and adds cross-layer dev-log observability so future W5/W6 status-bug work can see exactly which step the chain stopped at.

**W3 — Framework revert** drops `ProbeProfileProvider` interface, `ProbeProfile` struct, the cc adapter, and the always-on `manageActivityWatch` policy. `startWatch` now takes plain `WatchOptions`; default behavior is no-op (the orchestrator only manages a watch when an explicit ad-hoc `ProbeIntent` arrives, which W6 will introduce per agent).

**W4 — Observability** adds dev-log lines `[hook] trigger / [derive] verify_passed|skipped / [handler] frame_apply|projection_built|invalid_skip / [broadcast]` covering every hook chain branch, plus a TraceStore step coverage audit recorded inline in the `internal/module/agent/trace.go` package comment (8 hook paths × 5 trace steps with reasonable-absence rationale). All `log.Printf` (incl. argument formatting) is gated by `if isDevMode() { ... }` so production performs no I/O. `chain_id` is propagated end-to-end so a single hook chain greps as a connected 5-step sequence.

Until W6 lands the per-agent ad-hoc `ProbeIntent` for cc spinner / codex error and clear / opencode running mid-state, those status branches will not auto-promote — accepted regression observation period per spec §0.2 (the framework was the wrong abstraction and is now removed cleanly). Issue #719 always-on `[probe]` chatter no longer appears in daemon log.

Closes #719.

## [1.0.0-alpha.259] - 2026-04-29

### Feat(monitor): preserve unavailable session metrics (#745)

Keeps daemon monitor session rows visible when session daemon metrics cannot be mapped or collected. tmux pane listing failures, process table failures, missing pane mappings, and missing process data now return stable `unavailable_reason` values instead of failing the snapshot request.

Unavailable daemon metric fields are serialized as explicit `null` values for `cpu_percent`, `memory_bytes`, and `process_count`, while available session metrics continue returning numeric values. Reasons are scoped per session so one session's failure state does not overwrite another session's known mapping state.

This remains a daemon-only, non-breaking slice; SPA UI, top-process selection, and single-flight sampling remain deferred.

## [1.0.0-alpha.258] - 2026-04-29

### Feat(monitor): aggregate session process totals (#742)

Adds the next daemon monitor slice: live Purdex sessions are now represented in monitor snapshots by `session_code` and tmux session identity. The monitor reads the session provider from the core registry and aggregates process totals for each Purdex tmux session.

Session totals include every pane in the tmux session, including inactive panes, and include each pane root process plus descendants. Processes are de-duplicated by PID across panes so split/session layouts do not double-count shared descendants.

This remains a daemon-only, non-breaking slice. SPA UI, unavailable-state rendering, top-process selection, and single-flight sampling are still deferred to later PRs.

## [1.0.0-alpha.257] - 2026-04-29

### Feat(monitor): add daemon performance monitor foundation (#740)

Adds the first non-breaking daemon foundation slice for the performance monitor. The daemon now registers an independent `monitor` module with `/api/monitor/config` and a cached `/api/monitor/snapshot` skeleton, separate from agent trace monitor routes.

The config API persists monitor refresh interval and top-process limit settings with safe bounds and clamping. Snapshot caching now reuses fresh samples, invalidates on config updates, and avoids tmux/process sampling until later session-metrics slices.

This release also adds backend primitives for subsequent tab/session metrics: host CPU/memory/disk summaries, all-pane tmux listing, process table parsing, and pane process-tree aggregation. No SPA monitor UI or user-visible session metrics are enabled yet.

## [1.0.0-alpha.256] - 2026-04-29

### Refactor(agent): W2 cleanup-followup — simplify cc/codex cleanup sets to two-set union (#738)

Plan §5.3 CLEANUP-T1 follow-up to W2 alpha.255 ship. Drops the `ccLegacyEventNames` / `codexLegacyEventNames` static fixtures introduced in P3-T4a as a transition safety net while `HookEventSpec.Name` was being removed.

cc and codex catalogs use a 1:1 upstream/Pdx mapping where each installable spec's `UpstreamKeys[0]` is identical to the pre-W2 command-tail token (e.g. `Stop`). The three-set union (`UpstreamKeys ∪ PurdexName ∪ legacy Name`) collapses to a two-set union once legacy Name is dropped — pre-W2 tokens are still recognised via the UpstreamKey leg.

**Behavioural delta: zero.** Verified by codex round-1 review (0 finding) plus the existing `TestC{c,odex}OwnedCleanupEventNames_CleansLegacyAndNew` round-trip tests, which mix legacy `Stop` with new `PdxStop` entries and assert only the canonical Pdx entry remains after reinstall.

**Spec deviation** — plan §5.3 anticipated a `TestCcOwnedCleanupEventNames_OldFixtureNotCleaned` test asserting that "舊命令會殘留（已不在 cleanup set）" after CLEANUP-T1. That assertion isn't implementable for cc/codex's actual catalog because legacy Name strings == UpstreamKeys[0]; the cleanup set still contains them via the UpstreamKey leg. PR body documents the deviation.

Net diff: −145 / +27 lines across 4 files.

## [1.0.0-alpha.255] - 2026-04-29

### Feat(agent): W2 Phase 3 — opencode catalog naming separation + transition cleanup (#736)

Final phase of the W2 catalog naming separation. opencode joins cc (alpha.251) and codex (alpha.254) on the unified `PurdexName` / `UpstreamKey` / `Lifecycle` schema, and the legacy fallback path is fully removed. All three agents are now metadata-driven end-to-end.

**opencode catalog** — 65 entries gain `PurdexName` (Pdx-prefixed) / `UpstreamKeys[]` / `Lifecycle`. The cross-agent invariant runner flips opencode from reverse `LegacyShape` → forward `AllForwardInvariants`. `DeriveStatus` switch cases route on `PdxXxx`; legacy literals miss the catalog. `PdxPermissionRequest` keeps its multi-source mapping (`UpstreamKeys = ["permission.asked", "question.asked"]`) per spec §2.3.

**Plugin emit via `PURDEX_EVENT` const** — installer injects an 8-key Go-sourced JS const into `~/.config/opencode/plugins/pdx-agent-hooks.js`; all `emit(...)` callsites now reference `PURDEX_EVENT.PdxXxx` instead of legacy string literals. Magic marker `pdx-managed:opencode-hooks:v1` unchanged. Reinstall is idempotent (sha256 stable on second run).

**Transition cleanup** — `legacy_hook.go` deleted in full; `isLegacyHookForUnmigrated` predicate removed; `classifyLifecycle` now dispatches purely on catalog `LifecycleEventKind` metadata. Hot path (`handler.go`) and cold path (`sendSnapshot` / `replayFromDB`) both skip+clean events whose stored `event_name` no longer maps to a catalog `PurdexName` (R2-Attack follow-up `P3-T6.2` covers SPA cold reconnect).

**Schema cleanup** — deprecated `Name` field on `HookEventSpec` removed (`P3-T4`); `event_name` JSON unmarshal alias on `EventRequest` removed (`P3-T5`). The cleanup helper's legacy event-name set is now derived from a fixture constant (P3-T4a), unblocking the field removal.

**Round-2 follow-ups**

- **P3-T6.1** (R1 P2 → alpha-acceptable): `replayFromDB` rejects stored opencode legacy `event_name` rows on daemon restart. Spec §0 + plan G1 + memory feedback `no_alpha_migration` accept the cross-version transition gap; the negative test pins the alpha boundary.
- **P3-T6.2** (R2-Attack medium): SPA cold reconnect path (`sendSnapshot` / `replayFromDB`) was rebroadcasting `Valid=false` rows keyed by `raw_event_name`, causing stale legacy events to resurrect on a freshly-upgraded daemon. Mirrors `handler.go:230` hot-path cleanup in both replay paths.
- **P3-T7.1** (R2-Defense medium): `pluginSimState` contract simulator was a second SOT emitting legacy literals; renamed all 9 callsites to `PdxXxx` to match the post-P3-T3 plugin emit contract.

**File-Health (R2)** — approved.

**Upgrade note** — after upgrading to alpha.255, run:

```
pdx install --reinstall --agent opencode
# (cc/codex already migrated at alpha.251/.254)
```

Pre-existing opencode plugins (loaded into already-running opencode processes) continue emitting legacy literals until opencode is restarted; the daemon classifies these as `event_not_in_catalog` (no broadcast). User-facing impact: opencode lights stop updating until user reinstalls plugin and restarts opencode. This is the alpha breaking change deliberately accepted in spec §0.

**Live verify (mlab)** — fresh `tmux new-session 'opencode'` produced 3 chains (`PdxSessionStart` / `PdxUserPromptSubmit` / `PdxStop`), all 5-step `trigger → verify_passed → frame → projection → broadcasted`. Pre-existing opencode session emitting legacy literals classified `event_not_in_catalog → skipped` — confirms fallback predicate is gone.

## [1.0.0-alpha.254] - 2026-04-29

### Feat(agent): W2 Phase 2 — codex catalog naming separation + lifecycle metadata (#730)

Codex agent catalog migration mirroring cc's W2 Phase 1 (alpha.251). Daemon-internal canonical event id (`PurdexName`, `Pdx`-prefixed) is now decoupled from the upstream Codex hook event name (`UpstreamKey`). Lifecycle dispatch goes through catalog metadata; the legacy fallback predicate retains only the opencode case until Phase 3 ship.

**Catalog** — codex's 11 entries gain `PurdexName` / `UpstreamKeys` / `Lifecycle`. Cross-agent invariant runner flips codex from reverse `LegacyShape` → forward `AllForwardInvariants`. DeriveStatus switch cases rename to `PdxXxx`; legacy literals now miss the catalog (regression-pinned by `TestCodexDeriveStatus_LegacyNames_Invalid` on the 9 negatives).

**Installer boundary** — `~/.codex/hooks.json` keyed by `UpstreamKey` (e.g. `Stop`, `SessionStart`); command tail is `PurdexName` (e.g. `pdx hook --agent codex PdxStop`). `CheckHooks` mirrors the boundary on the read path. `codexKnownEventNames` / `codexOwnedCleanupEventNames` derive from the catalog as a three-way union (`UpstreamKeys ∪ PurdexName ∪ legacy Name`).

**Predicate** — codex case removed from `isLegacyHookForUnmigrated`. Only opencode remains until Phase 3.

**SPA** — codex event-name fixtures updated to `Pdx` prefix.

**Round-2 follow-up (P2-T7)** — `TestHandleEvent_CodexLegacyEventName_IsCatalogMiss` pins the post-P2 boundary: a user upgrading the daemon without running `pdx install --reinstall --agent codex` ends up with hooks emitting legacy literals that now hit the catalog-miss path. Spec §0 + plan G1 deliberately accept this for alpha — reinstall is the user-facing transition trigger. The negative test makes future drift re-introducing a fallback path observable.

**Upgrade note** — after upgrading to alpha.254, run `pdx install --reinstall --agent codex` (and `--agent cc` if not already done at alpha.251). Legacy hooks will silently miss the catalog instead of broadcasting status updates until reinstalled.

## [1.0.0-alpha.253] - 2026-04-29

### Fix(spa): wide-mode workspace drag — reorderable, smooth, axis-locked (#732)

Restore workspace reordering by drag in the wide ActivityBar. The fix bundles four related issues uncovered iteratively while debugging:

**Root cause** — `WorkspaceRow`'s name button (`flex-1`, the dominant click target) and Plus button called `onPointerDown stopPropagation()`, blocking dnd-kit `PointerSensor` from receiving pointerdown so workspace drag never activated. The "drag hijack" guard added in 259c2a4f / Phase 3a was redundant: `activationConstraint: { distance: 5 }` already separates click-without-movement from drag-with-movement.

**Three follow-up fixes** surfaced once drag was reachable again:

- *Sibling rows bouncing*: each row registers two overlapping droppables (sortable wrapper + header `useDroppable`). `pointerWithin` returned both, `over` flickered between ids, `verticalListSortingStrategy` displaced siblings only when over.id was in items → bounce. `customCollisionDetection` now filters `droppableContainers` by active drag type.
- *Drag escapes the list / horizontal drift*: add `restrictWorkspaceDrag` modifier mirroring `ActivityBarNarrow` — Y-axis lock + scroll-zone bbox clamp; short-circuits for tab drag to preserve cross-workspace movement.
- *Text blurs / appears compressed*: switched from `CSS.Transform.toString(transform)` (3-axis translate + scaleX/Y, non-integer y → sub-pixel blur) to `translate3d(0, Math.round(y), 0)`, matching the narrow-mode pattern. Removed the `title` attr on the workspace name span (was firing a native browser tooltip during drag).

`WorkspaceRow.test.tsx`'s `drag-steals-click guard` describe (its premise was the bug) replaced with `header pointer-down propagation (drag reachability)` covering name / chevron / Plus.

## [1.0.0-alpha.252] - 2026-04-29

### Fix(daemon): sweep:pid_dead broadcasts status=clear (#727 / closes #717)

Fix opencode (and any agent) leaving its SPA indicator stuck after Ctrl+C exit. Sweep was correctly detecting process death and clearing `agent_frames`, but the WebSocket broadcast carried `status=""` instead of `"clear"`, so SPA's `handleNormalizedEvent` (which keys the clear path on `status === 'clear'`) couldn't recognize the session was empty.

**Root cause** — `internal/module/agent/frame_ops.go` `buildProjectionNormalized` had asymmetric handling of two semantically equivalent "no top frame to display" states. The `projection == nil` branch passed through `result.Status` verbatim, while `projection != nil && TopFrame == nil` forced `StatusClear`. Sweep's `afterFrameCleared` (`sweep.go:551`) called the helper with `agentpkg.DeriveResult{}`, so the empty status leaked through to broadcast whenever a sweep cleared the last frame in a session.

**Round-1 fix** — pass `agentpkg.DeriveResult{Status: agentpkg.StatusClear}` explicitly at the sweep callsite. Surgical 1-line change with explicit caller intent.

**Round-2 hardening** (codex 3-parallel adversarial review):
- *Defense in depth*: `buildProjectionNormalized`'s `projection == nil` branch now defaults empty `Status` to `StatusClear` (fail-safe for future callers).
- *Race fix*: `afterFrameCleared` re-resolves `projectionForSession` right before broadcasting. If a hook handler raced in to create a new frame for the same session (e.g. user kills opencode and immediately runs `opencode` again, SessionStart hook lands mid-sweep), the broadcast now carries the live state instead of overwriting the just-installed running status with a stale clear. Best-effort — full serialization requires per-session locking, tracked separately.
- *Test hygiene*: split broadcast contract tests into a focused `internal/module/agent/sweep_broadcast_test.go` with a shared `readSweepNormalizedEvent` helper that decodes the `HostEvent` envelope and inner `NormalizedEvent` once.

#### Followups tracked

- #728 — Sweep broadcast may be silently dropped (lossy WS, no replay) — pre-existing architectural concern; #717's fix makes the consequence more visible but doesn't worsen it.
- #729 — Refactor: extract sweep broadcast emission into shared helper across `sweep.go:327` / `:499` / `:551`.

## [1.0.0-alpha.251] - 2026-04-29

### Feat(agent): W2 Phase 1 — schema + cc catalog naming separation + lifecycle metadata (#710)

Phase 1 of W2 (catalog naming separation) in the Lights system rebuild — see `docs/specs/2026-04-28-catalog-naming-separation-spec.md` for the full design and `docs/specs/2026-04-28-lights-rebuild-fix-spec.md` §1 for how W2 fits the larger rebuild.

**Goal**: separate three previously-conflated dimensions of hook events — Purdex's daemon-internal canonical name, the upstream agent's hook map key, and the lifecycle/frame-mutation effect — so daemon code uses Pdx-prefixed canonical names everywhere internally and only translates at the input/output boundaries (cc settings.json, codex hooks, opencode plugin).

**Phase scope**: cc end-to-end migration + shared schema + handler/frame_ops lifecycle metadata dispatch. codex / opencode catalogs remain on the legacy literal path via `isLegacyHookForUnmigrated` (Phase 2 / 3 migrate them); single user-facing alpha bump after all three phases land per spec §「W2 設計關鍵決議」#4.

#### Schema additions

- `HookEventSpec` gains `PurdexName` (Pdx-prefixed daemon-internal canonical), `UpstreamKeys []string` (upstream hook map keys), `Lifecycle LifecycleEventKind` (frame-mutation effect classification, 8 kinds). `Name` retained per plan G1 until PR-W2-cleanup-followup so legacy `~/.claude/settings.json` cleanup keeps working through the transition.
- `LookupByPurdexName` / `LookupByUpstreamKey` free functions provide spec §2.5's calling convention.
- `LifecycleEventKind` enum (`SessionStart` / `SessionEnd` / `UserPromptSubmit` / `Stop` / `StopFailure` / `SubagentStart` / `SubagentStop` / `None`) classifies frame-mutation effect independently of upstream naming.

#### CC end-to-end migration

- `EventRequest` / `hookPayload` JSON migrated to `purdex_name` (with `event_name` unmarshal alias for transition; Phase 3 removes the alias).
- cc 28 catalog entries populated with the three new fields (including unsupported / ignored entries — `Pdx`+original name with `LifecycleNone`).
- cc `DeriveStatus` + installer + cleanup/known sets all keyed by `PurdexName` / `UpstreamKey` via catalog-derived helpers; settings.json `command` trailing token now writes `pdx hook --agent cc PdxXxx` (matching the daemon-internal canonical), upstream hooks key remains `SessionStart` / `Notification` / etc.
- SPA cc fixtures keyed under `agent_type='cc'` migrated from upstream-key strings to PdxXxx; `useAgentStore` Notification-idle no-unread branch accepts both literals during the transition.
- SPA notification dispatch (`shouldNotify` informational suppression + `NotificationSettings.events` lookup + `buildNotificationContent` switch) normalizes the 4 user-facing PdxXxx names back to legacy at entry via `normalizeEventName`, so cc desktop notifications survive PdxXxx broadcasts (codex round-2 attack-side finding A-F01 — without normalization every cc permission prompt / Stop / StopFailure notification would silently drop).

#### Lifecycle metadata-driven dispatch

- `handler.handleEvent` error-guard, subagent transient-emit, and SessionStart subagent-reset paths key off `agentpkg.LifecycleEventKind` via `classifyLifecycle`'s spec §3.4.2 three-branch decision tree (catalog hit > legacy fallback > LifecycleNone). Catalog-hit `LifecycleNone` is a legitimate no-op classification; `LookupByPurdexName` + `isLegacyHookForUnmigrated` per-agent literal sets keep codex/opencode pre-migration traffic routing correctly.
- `frame_ops.applyFrameEvent` (detail-only / SessionEnd / SubagentStart-Stop / four SessionStart hot paths), `updateSubagents` / `mutateSubagents`, and `path_hint_extractor` migrated to lifecycle metadata. Transitional `matchesLifecycleName` / `normalizeLifecycleName` helpers added in P1-T8 and removed in P1-T11 / P1-T12 alongside their final callers.
- `isLegacyHookForUnmigrated` per-agent literal sets: codex 9 entries (includes Notification); opencode 8 entries (includes PermissionRequest, deliberately omits Notification — opencode's catalog has no Notification entry, accepting it would route an unknown event into the legacy fallback).

#### Cross-agent catalog-miss invariant tests (D-F01 / Q-F01 drift guard)

`TestHandleEvent_CodexPrematurePdxName_IsCatalogMiss` / `TestHandleEvent_OpencodeNotification_IsCatalogMiss` / `TestHandleEvent_CCPdxNotification_IsCatalogHitNotMiss` pin spec §3.4.2's three-branch boundary at the handler level so Phase 2/3 catalog migrations can't regress silently.

#### Codex review history

- **Spec / plan**: 5 codex review rounds on the spec, 1 on the plan; 5 plan-level findings (G1–G5) + 2 spec findings (S1–S2) all applied before implementation.
- **PR review round 1 (standard, job `bvl94kd52`)**: 1 P1 finding (R1-F1, cc legacy hook upgrade compatibility) **dismissed** per spec §「W2 設計關鍵決議」#4 + alpha-stage breaking-change posture from `kickoff_lights_rebuild.md` §「對齊已決議的設計關鍵點」#8.
- **PR review round 2 (3 parallel adversarial: attack `b4opavt0q` / defense `b0f8s7ahj` / file-quality `bhuqqc9i6`)**: A-F01 (SPA notification regression), Q-F03 (legacy_hook_test comment) fixed in this PR. D-F01 / Q-F01 (lifecycle classification overload) drift-guarded with new handler-level invariant tests; structural `EventResolution` refactor tracked as #722. Q-F02 (handleEvent SRP) tracked as #723. No critical / P1 findings outstanding.

### Closes

- Closes via review summary references throughout — no individual issue closes in this PR.
- Follow-up issues opened: #722 (EventResolution refactor for lifecycle classification overload), #723 (handleEvent SRP split, depends on #722).

## [1.0.0-alpha.250] - 2026-04-29

### Refactor(electron): retire runtime codesign — Stage 1b (#709) (#720)

Stage 1b of the macOS code-signing roadmap. Retires the runtime codesign concept (`detectSignedState` preflight + `resignAppBundle` helper) introduced in PR #672 / Stage 0. The runtime path was vestigial: macOS does not re-verify `CodeResources` post-launch on same-machine same-path relaunches, dev update is `PDX_DEV_MODE`-gated at three boundaries, and the OS never reads the signature record after first launch on a quarantine-cleared bundle.

Stage 0 protected against a SIGKILL caused by an unnecessary action; Stage 1b removes the action entirely (Option β).

- **`electron/updater.ts`** — delete `detectSignedState`, `resignAppBundle`, `getAppBundlePath`, `APP_ID`, `SignedState`, `PREFLIGHT_TIMEOUT_MS`, `NOT_SIGNED_PATTERN`, `stripAnsi`, the `__testing` namespace export, the `progress('signing')` call site, the now-stale `// no progress('restarting')` comment, the `node:child_process` import, and trim `path` import to `{ join }` (drop `dirname`). About 80 lines of code removed.
- **`electron/signing.test.ts`** — replace 3rd presence-checking static test with absence smoke; add 4 new static guards (progress-sequence with literal-array equality + total-call count, preload strict gate, daemon strict gate, SPA-absence). Delete entire `describe('updater signing preflight (runtime)', ...)` block (17 mock-driven tests covering deleted code) plus the helpers / `vi.mock` setup that supported it. File: 281 → 65 lines.
- **`spa/src/components/settings/DevEnvironmentSection.tsx`** — drop dead `signing` and `restarting` entries from `stepLabels`. `signing` was emitted by the deleted call site; `restarting` was always dead because `app.exit(0)` killed the process before any IPC could deliver. Daemon-rebuild flow's separate `daemonPhase === 'restarting'` render path is unaffected.
- **`electron/preload.ts`** — Round-2 attacker fix: tighten dev-update bridge gate from truthy ternary (`process.env.PDX_DEV_MODE ? ... : {}`) to strict `=== '1'`, matching the daemon's `os.Getenv("PDX_DEV_MODE") == "1"`. The truthy form would have exposed `applyUpdate`, `checkUpdate`, `streamCheck`, `onUpdateProgress` for `PDX_DEV_MODE=0`/`false`/`no` — values commonly used to mean "off". Pre-existing bug from the original dev-update PR; codified contract via the new strict-equality preload-gate guard.
- **`electron/main.ts`** — Round-2 defender fix: wrap `dev:*` `ipcMain.handle(...)` registrations in `if (process.env.PDX_DEV_MODE === '1')` block. Closes the spec §7 R7 "main-process boundary half-open" residual surface — now all three layers (preload + main + daemon) refuse dev update with the same strict gate.

### Test gate

39 → 27 tests, all green: `signing.test.ts` 20 → 8 (2 existing static + 1 absence smoke + 1 progress-sequence + 1 preload-gate + 1 daemon-gate + 1 SPA-absence + 1 main-gate); `keybindings.test.ts` 19 unchanged.

### Live verification

Manual Air verification (spec §8.2 unsigned bundle / §8.3 ad-hoc signed bundle) is the canonical Stage 1b safety gate. The `--verify --deep --strict` post-update failure on signed bundles is the **expected outcome** documented in §3.4 — the Stage 1b safety claim is "relaunch succeeds on same machine same path", not "signature stays cryptographically intact for redistribution".

### Closes

Closes #712 (darwin integration test for Stage 0 preflight — preflight retired, nothing to integration-test) and #713 (`APP_ID` constant drift across `updater.ts`/`package.json`/`build-electron.mjs` — `APP_ID` deleted from `updater.ts`).

### Review history

| Round | Findings | Outcome |
|-------|----------|---------|
| Spec review (`task-moivfw7n-a8x1ph`) | 8 (2 P1 + 4 P2 + 2 P3) — narrow §3.4 claim scope, ad-hoc signed bundle Stage-1b gate, PDX_DEV_MODE precision, progress guard, R2 split, bump separation, SPA dangling | All addressed in spec v1.1 before plan |
| Plan review (`task-moiw8665-6ak7i6`) | 11 (3 P1 + 5 P2 + 3 P3) — preload/daemon gate static asserts, ordered array equality, T2 tsc gate placement, dirname trim, vitest imports, Run A/B independent installs, SPA grep gate, Closes syntax, T2 naming | All addressed in spec v1.2 + plan v1.1 before implementation |
| R1 standard | 0 actionable defects | Approve |
| R2 attacker (`review-moiyv5n5-eqxwcr`) | 1 P1 — preload truthy gate vs daemon strict gate | Fixed `fcb78091` |
| R2 defender (`review-moiyz7eb-l7hofd`) | 3 (D1 manual gate / D2 main-process boundary half-closed / D3 progress guard imprecise) | D2/D3 fixed `9f20bcb1`; D1 manual verification deferred to Air post-merge |
| R2 file-health (`review-moiyzrod-r7x9j2`) | 3 (F1 test SRP drift / F2 doc internal staleness / F3 dead `restarting` label) | F2/F3 fixed `9f20bcb1`; F1 deferred (rename ripple too wide) |

### Known follow-up

- **F1 (medium)** — `signing.test.ts` now owns gate contracts that exceed the "signing configuration" naming. Defer rename/split until file-health concern accumulates.
- **Air manual verification** — Run A (unsigned) + Run B (ad-hoc signed) per spec §8.2/§8.3 / plan §5. Post-merge to be executed; relaunch failure on either run is a rollback trigger.

## [1.0.0-alpha.249] - 2026-04-29

### Fix(opencode): plugin emit() stdin TypeError hotfix (#715) (#716)

Pre-existing bug since commit `ffdd4e14` (2026-04-21, initial opencode integration). The rendered opencode plugin's `emit()` helper passed a raw JSON string as `Bun.spawn`'s `stdin` field, which Bun rejects with `TypeError: ERR_INVALID_ARG_TYPE`. Every opencode hook event has thrown for 7 days; `agent_events.db` had zero `agent_type='opencode'` rows in production. Existing template tests only diffed rendered text and never exercised a real Bun runtime, so CI silently passed.

- **Daemon `internal/agent/opencode/plugin_template.go`** — pull JSON encoding before `Bun.spawn` (`const encoded = JSON.stringify(payload)`), switch the `stdin` field to `'pipe'`, and write the payload through the FileSink lifecycle (`proc.stdin.write(encoded); proc.stdin.end(); await proc.exited`). Pre-encoding restores the pre-fix semantic where a serialization failure prevents the spawn entirely (rather than leaving the spawned `pdx hook` blocked on stdin EOF). The rendered body is byte-different from the broken era, so `CheckHooks` reports drift on pre-fix managed plugins and the next `pdx setup --agent opencode` writes the fixed body.
- **Test `internal/agent/opencode/plugin_template_bun_integration_test.go`** (new) — real-Bun integration test that renders the plugin against a stub `pdx` shell binary, runs the result with `bun <plugin.mjs>`, and asserts the stub captured the JSON payload on stdin. Failure-mode classification distinguishes the pre-fix `ERR_INVALID_ARG_TYPE` red signal from harness failures (envelope mismatch, deadlock, syntax error). Four-layer skip gates: Windows / missing `/bin/sh` / no `bun` on `PATH` / `bun --version` failure — each is a `t.Skip` not a fail, so a single `go test ./...` run works across CI environments.
- **Test `internal/agent/opencode/hooks_test.go`** — append `TestCheckHooks_PreFixManagedBodyReportsDrift` that synthesizes the actual pre-fix body (`stdin: JSON.stringify(payload)` with no `encoded` const, no `write`/`end` follow-up lines), writes it under HOME, and asserts `CheckHooks` reports drift on at least one event before `InstallHooks` returns the directory to the canonical state. Lifts spec AC3 from manual mlab observation into a CI-enforced unit assertion. `RenderManagedPluginForTesting` exposed via `hooks_export_test.go` (mirroring the existing `SetResolveCanonicalPdxPathForTesting` export pattern).

### Live verification

mlab on alpha.248 + spawn-fix branch captured 20 opencode events end-to-end: 4 `SessionStart`, 6 `UserPromptSubmit`, 5 `Stop`, 2 `SubagentStart`, 2 `SubagentStop`. All `latest_decision=broadcasted`, all `terminal_status=completed`. Pre-fix DB had **zero** opencode rows for the entire 7-day broken window.

### Review history

| Round | Findings | Outcome |
|-------|----------|---------|
| Spec review (`task-moiu936r-x7a8nh`) | 7 (5 P2 + 2 P3) — stdin types precision, await semantics note, `bun <script>` vs `bun run`, `bun --version` probe, Windows skip-gate, CheckHooks drift acceptance, acceptance-criteria scope | All addressed in spec v1.1 before plan |
| Plan review (`task-moiufay4-0c5pdm`) | 6 (3 P2 + 3 P3) — TDD red precision, single-execution `CombinedOutput`, `.mjs` vs `.js` ESM, `/bin/sh` gate, AC3 in-repo unit test, DOD program/process split | All addressed in plan v1.1 before implementation |
| R1 standard (`review-moiuppm6-63g2f2`) | 0 blockers | Approve |
| R2 3-parallel (attack + defense + file-quality) | 2 medium (F1 0.78 partial-failure orphan subprocess; F2 0.87 drift fixture impossible hybrid body) + 1 approve | Both addressed in commit `147bd46c` |

### Known follow-up (not blocking #715)

- **#717** — opencode session indicator stays active after Ctrl+C exit because upstream opencode (anomalyco/opencode#10524) intercepts SIGINT/SIGTERM before plugin handlers run. `Stop` events fire correctly per prompt, but no `SessionEnd` ever fires — daemon-side liveness probe / heartbeat needed. Tracked separately.

## [1.0.0-alpha.248] - 2026-04-28

### Fix(electron): unsigned-aware preflight in dev update resign (#709 Stage 0) (#711)

Stage 0 of the macOS code signing roadmap (#709). Fixes the dev update regression introduced by PR #672 (alpha.234) where `applyUpdate` unconditionally re-signed the running bundle and triggered macOS AMFI to SIGKILL the process before `app.relaunch()` could run — symptom: SPA progress reaches `applying` then disappears, app does not relaunch, manual restart shows old version. Reproduces on **unsigned** `Purdex.app` (the actual user deployment — `codesign -dv` reports `code object is not signed at all`).

- **Electron `electron/updater.ts`** — `resignAppBundle()` gains a three-state preflight via new `detectSignedState()` helper. `codesign -dv` exit 0 → `signed` (re-sign + verify, unchanged); non-zero with normalised stdout/stderr matching `/code object\s+is\s+not\s+signed\s+at\s+all/i` → `unsigned` (skip codesign, the actual fix); any other outcome (status null / spawnSync error / unrelated stderr) → `unknown` (throw with actionable error including bundle path + `PDX_SKIP_MAC_SIGN=1` remediation hint, routed through existing `applyUpdate` rollback). `PREFLIGHT_TIMEOUT_MS = 10_000` prevents codesign hang from blocking the main process. Detection normalises ANSI escapes + merges stdout/stderr so case differences, ANSI wrapping, stdout-borne phrasing, and irregular whitespace don't cause false-negatives. Imports unified to `node:child_process` so tests can `vi.mock('node:child_process')`. `__testing` namespace export gates `detectSignedState` + `resignAppBundle` for unit tests (Stage 1 will retire this when bundle-swap refactor lands).
- **Test `electron/signing.test.ts`** — restructured into static block (3 grep tests for package.json + build-electron + updater contract markers) and runtime block (17 tests under `vi.mock('node:child_process')` + `vi.mock('electron')`): 5 detection core cases, 5 resilience cases (case difference / ANSI wrap / stdout-borne / irregular whitespace / timeout), 1 timeout-option assertion, 6 `resignAppBundle` dispatch cases (PDX_SKIP_MAC_SIGN / non-darwin / unsigned-skip / unknown-throw + actionable / ad-hoc identity / forced identity). `mockCodesign()` + `loadTesting()` helpers eliminate boilerplate. `process.platform` mock preserves descriptor flags via `getOwnPropertyDescriptor` + `afterEach` restore (vs. `vi.stubGlobal` which would replace the entire process object).

### Review history

| Round | Findings | Outcome |
|-------|----------|---------|
| Spec review | 9 (4 P1 + 4 P2 + 1 P3) — three-state detection contract gap, mock strategy, identity precedence, etc. | All addressed in spec v1.1 before plan |
| Plan review | 8 (4 P1 + 4 P2) — verification toolchain availability, T2 RED contract, `process.platform` descriptor restore, bump base protocol | All addressed in plan v1.1 before implementation |
| R1 standard | 0 blockers (sandbox couldn't run vitest due to read-only filesystem; static analysis clean) | — |
| R2 4-parallel (adversarial + attacker + defender + file-health) | 12 (2 P1 + 4 P2 + 6 P3) — stderr brittleness, no spawnSync timeout, runtime test boilerplate, plan docs RED count inconsistency, error message actionability, `__testing` namespace style, `applyUpdate` SRP, APP_ID drift | All P1 + must-fix P2 fixed in commit `17b81143`; F6/F9 → issues #712/#713; F7/F8 deferred to Stage 1 (#709) |

### Plan deviations

1. T2 RED count: plan v1.0 predicted 11 RED runtime tests; actual was 4 because the stub's hardcoded `'unknown'` coincidentally satisfied 3 detection assertions and the unmodified `resignAppBundle` satisfied 4 dispatch assertions. The 4 actual REDs were exactly the new contract paths (`signed`/`unsigned` classification + unsigned-skip + unknown-throw); plan v1.1 documented this.
2. R2 stderr matching expanded beyond plan's exact-substring shape — added ANSI strip + normalised regex per adversarial F1.

### Out-of-scope (tracked under #709)

- Stage 1: dev update bundle-swap refactor + entitlements + Hardened Runtime config
- Stage 2: self-signed cert flow + cross-machine trust docs
- Stage 3: Apple Developer ID + notarization in CI release pipeline

### Followup

- `#712` — Stage 1+: darwin integration test for real codesign -dv (close stderr-format-drift gap)
- `#713` — refactor(electron): consolidate APP_ID across updater.ts / package.json / build-electron.mjs

## [1.0.0-alpha.247] - 2026-04-28

### Feat(spa+daemon): file-not-found popup with three-layer fallback (P5) (#707)

Editor P5 — final phase of the **Editor self-contained** series. Clicking a non-existent file path (terminal link or FileTreeView) no longer silent-fails: the new pipeline runs `stat → Layer-1 path-cache lookup → popup` and the popup's expand action triggers Layer-2 (session cwd) + Layer-3 (workspace projectPath) `fs.search` against a server-side capability allowlist.

- **Daemon `internal/module/fs/search_engine.go`** — pure `Search(ctx, req)` with mandatory dir excludes (`node_modules`, `.git`, `.cache`, `dist`, `.pnpm-store`, `.next`, `.turbo`) and basename excludes (`*.lock`, `*.log`) UNIONed with client filters; `respectGitignore *bool` nil → true; gitignore parse failure returns 4xx (no fail-open); symlink loop avoidance; depth via `filepath.Rel`. In-house `preflightGitignore` + `bracketsBalanced` because `go-gitignore` silently drops bad lines.
- **Daemon `internal/module/fs/search_handler.go`** — `(m *FsModule) handleSearch` capability-only roots: `{kind:"session-cwd", sessionCode}` resolves via `SessionProvider.GetSession(code).Cwd` and is **EvalSymlinks'd** so symlink hops can't bypass the system-path allowlist (R2 high — a `/tmp/ws/home-link/proj` symlink to `$HOME` would otherwise let WalkDir scan the user's home tree). Both lexical and resolved forms gate; `kind:"absolute"` → 4xx; `workspace-projectPath` → 501 (defer); system paths `/`, `/etc`, `/sys`, `$HOME` direct, `/Users` direct, `/Volumes` rejected.
- **SPA `spa/src/lib/file-open/`** — host-bound `createOpenFileService({fsBackendFactory, popupController, tabOpener})` with strict ENOENT/404 error classification; `fsBackendFactory(hostId)` resolves once per call so workspace/host switch mid-flight can't corrupt the open. `fsSearchByCapability(hostId, basename, roots, limits?, signal?)` threads an AbortSignal into fetch so popup dismiss tears down the daemon-side WalkDir (R2 medium). `FileNotFoundError` re-thrown by tryOpenFile when `popupOnMissingFile` is off.
- **SPA `file-not-found-popup-service.tsx`** — singleton mount with `import.meta.hot.dispose(hideFileNotFoundPopup)` and `AbortController` cancellation; controlled re-render reuses the live root + token instead of aborting (R2 medium — previously layer-2 results arriving first would abort the layer-3 fetch via the popup token).
- **SPA `file-open-bootstrap.ts`** — module-level `mergedHits` accumulator so layer-2 + layer-3 results merge into the same expanded popup; reset on fresh ask-expand / layer1-multi mounts. Exports `openFileAsBufferDirect` for the tilde fallback when home resolve fails (R3 P2 — daemon rejects `stat('~/foo')` with 400, classified as auth/network not ENOENT, so the missing-file pipeline can't open a blank buffer).
- **SPA `EditorOpenBehaviorSection`** — two new toggles in Editor settings (per `feedback_core_vs_module_settings`, `useEditorSettingsStore` not `useUISettingsStore`): `popupOnMissingFile` (default `true`) master gate; `autoSearchLayer1` (default `true`) cache auto-lookup.
- **Terminal link / FileTreeView integration** — both consumers route through `tryOpenFile`; the previous `getDefaultOpener + openSingletonTab` direct calls move into the bootstrap-time `tabOpener`. Click-handler context: `FileNotFoundError` → `console.warn` (expected when popup off); auth/network/host removed → `console.error` so console-watching surfaces broken transport instead of silently dropping clicks (R1 P2 + R4 P2 symmetric fix in FileTreeView).
- **Privacy** — fs.search payload sends only basename + capability roots; daemon resolves the capability and never echoes the resolved path beyond what the user already sees in the popup CTAs.

### Review history

| Round | Findings | Outcome |
|-------|----------|---------|
| R1 standard | 1 P1 (daemon stat 404 lost status — popup pipeline never triggered for daemon backend) + 1 P2 (terminal-link catch swallowed every error including auth) | Both fixed |
| R2 adversarial | 1 high (symlink session cwd bypassed allowlist) + 1 medium (popup dismiss didn't cancel fetch; show() replacement aborted peer searches) | Both fixed; mergedHits accumulator added |
| R3 standard | 1 P2 (unresolved tilde regression — `~/foo` → daemon 400 instead of new buffer) | Fixed; `openAsBuffer` direct-open dep added |
| R4 standard | 1 P2 (FileTreeView voided promise — unhandled rejections from auth/popup-off) | Fixed; FileTreeView mirrors terminal-link error split |

### Plan deviations

1. Path cache keying — actual `usePathCacheStore` is keyed by `(hostId, cwd)` per the P4 redesign, not workspaceId. `OpenFileContext` carries `cwd` (captured at click time) for the cache and `sourceWorkspaceId` only for Layer-3.
2. Settings store moved from `useUISettingsStore` to `useEditorSettingsStore` per `feedback_core_vs_module_settings`.
3. Layer-3 501 silently treated as not-implemented (no surface error).
4. `go-gitignore` library silently drops bad lines; in-house preflight catches the unbalanced `[...]` case the plan tests against.

### Followup

- closes #703 — `usePathCacheStore.lookup` / `pruneStaleCandidate` now have a production consumer.
- daemon `workspace-projectPath` capability — needs workspace registry; deferred.
- bare-filename Editor-disabled UX gap from P3 still open.

## [1.0.0-alpha.246] - 2026-04-28

### Feat(spa): Quick Commands v2 Phase 1c — HOST_ACTIONS entry with host liveness probe (#705)

Phase 1c of the Quick Commands v2 capability/binding/slot system — adds a chip slot beside the new-session button on the host detail page (`SessionsSection`). Users who bind a command to mount = HOST in Settings now see chips next to the `+ New Session` button; clicking creates a tmux session on that host (cwd = host default `~`), sends the bound command, and switches to the new session.

- **`runHostSlot` / `HostSlotContext`** — new sibling executor to `runWorkspaceSlot`. `HostSlotContext.hostId` is required non-null string (host detail page owns it via prop). `HostDeps` carries `switchToSession` + `assertHostLive`; deliberately omits `assertContextLive` and `resolveHostId` (workspace probes / picker callback) so cross-shape pollution fails `tsc -b`. Failure UX matches `runWorkspaceSlot`'s precedence rules per spec §3.3.
- **Host liveness probe — three call sites for defense in depth** (R2 of codex PR review):
  1. **Pre-create** — at `runHostSlot` entry, before `createSession`. Catches stale chip clicks (host already deleted at click time, before chip re-rendered disabled). Without this gate, `useHostStore.getDaemonBase` falls back to `activeHostId ?? hostOrder[0]` and creates an orphan session on the wrong host.
  2. **Post-create** — after `createSession` resolves, before `executeCommand`. Catches the during-await race (host deleted while createSession Promise was in flight). Stops destructive commands from shipping.
  3. **Retry** — inside the toast retry action. Catches the between-toast-and-retry window.
- **Workspace-aware switch** (R1 of codex PR review): `switchToSession` snapshots the host page's owning workspace at click time via `findWorkspaceByTab(activeTabId)` BEFORE any await, then closes over the captured `owningWsId`. Standalone host pages get explicit `null` (bypasses `insertTab`'s `activeWorkspaceId` fallback so a stale or post-await `activeWorkspaceId` can't redirect the new session).
- **Executor-level busy guard** — `executingRef + executing` state mirrors workspace popover pattern; suppresses fast double-click between React event ticks.
- **Disconnect handling** — chip is `busy={isOffline || executing}`, matching new-session / row actions disable semantics.
- **`<QuickCommandMenu>` row removal** — removed from `SessionsSection` rows (consolidated to new-session entry); v1 component itself stays for `PaneLayoutRenderer`.
- **Empty `session.code` handling** — server returning blank/whitespace `code` is treated as create failure (avoids confusing 404 from downstream `executeCommand`).
- **Spec / plan / JSDoc** — three-site `assertHostLive` contract documented in spec §3.3.1, plan §1c.0 design block, and `HostDeps.assertHostLive` JSDoc. Tests use `mockReturnValueOnce` sequences to model phase-specific probe states; assertions favor `>= 1` over exact counts to avoid false-fails when probe timing changes.
- **Review history** — codex plan-review (`task-moijtc8w-w8rf8w`, 6 findings) → R1 standard PR review (1 P2: click-time snapshot race, fixed `4a54af18`) → R2 adversarial (1 medium: pre-create gate missing, fixed `0103c942`) → R3 verification (1 low: docs not aligned with three-site contract, fixed `976c83c2`) → R4 final approve.

### Followup issues

- #689 — server-side orphan session cleanup (Phase 2)
- #695 — ESLint custom rule restricting `runWorkspaceSlot` / `runHostSlot` parameter shape escape hatches (`as any`, spread, `Object.assign` cast bypass)

## [1.0.0-alpha.245] - 2026-04-28

### Feat(daemon+spa): pathhint v1 channel + spa path cache (P4) (#696)

Editor P4 — daemon emits `agent.path_hint` events when CC PreToolUse / PostToolUse touches a file, and the SPA caches recent dirs per `(hostId, cwd)` so future P5 fuzzy lookups can resolve relative paths without round-tripping through the user. Cache scope is keyed by the agent's working directory (the real boundary), with `sessionCode` carried as a per-entry tag so lookups can sort same-session entries first. This survives CC restarts in the same repo (a common workflow when the context window fills up) and lets a Codex session in the same cwd benefit from CC's earlier hints, without crossing into unrelated repos in the same workspace.

- **Daemon** — `agent.path_hint` v1 schema (`{ schemaVersion, agentId, sessionCode, cwd, dir, kind, timestamp }`) with 200-entry ring buffer; pure `ExtractPathHint` with cwd sourced from CC raw_event and `SessionInfo.Cwd` fallback; payload defenses (≤64 KiB raw event, ≤4 KiB file_path / cwd, no NUL/control chars); `normalizeCwd` preserves `/` so agents at root don't lose their cwd; `PathHintDedupCache` keyed by `(session, dir, basename)` to prevent 5s blackout after SPA prune; `EmitPathHint` wired into CC PreToolUse / PostToolUse right after verify accepts.
- **SPA** — `usePathCacheStore` keyed by `(hostId, cwd)` with NUL-separator scope keys (so colon-bearing hostIds don't collide), 50-entry LRU per scope, and `lookup(currentSessionCode?)` priority of same-session-first then recency. `clearBySession(hostId, code)` is host-scoped per R3 P2 so cross-host sessionCode collisions don't wipe live entries. Persisted via `purdexStorage` with rehydrate sanitizer that replays the same invariants `add()` enforces (R2 file-quality F1). Intentionally NOT registered with `syncManager` — cache is per-window-owner; per-origin localStorage still survives tear-off.
- **Dispatch** — `lib/agent-ws/` split into router + per-event-type handlers; `path-hint-dispatch.ts` validates `event.session === payload.sessionCode` (R2-D3), rejects payloads >64 KiB (R2-A2), drops on schema/cwd/dir/kind invariants. `AGENT_WS_EVENT_TYPES` SSoT (R2-F2) consumed by both router and `useMultiHostEventWs`.
- **Privacy** — dir-level only; no full file path or basename in broadcast payload. HostId travels in the broadcast envelope.
- **Review history** — R1 standard (2 P2 fixed) → R2 attacker / defender / file-quality (5 high/medium across the three lanes) → scope redesign `(host, workspace) → (host, cwd)` per user feedback (workspaces are free-form groupings; cwd is the real boundary; sessionCode demoted to per-entry priority tag) → R3 standard (1 P2 cross-host sessionCode collision + 1 P3 root cwd `/` collapsed by TrimRight, both fixed) → R4 standard (1 P2 NotebookEdit `notebook_path` field fix; remaining "no consumer" finding tracked as #703).

### Followup issues

- #703 — `usePathCacheStore.lookup` / `pruneStaleCandidate` need a production consumer; P5 (NewTabPage popup-mount + `fs.search` backend allowlist) is the planned wirepoint, plus the bare-filename Editor-disabled UX gap from P3.

## [1.0.0-alpha.244] - 2026-04-28

### Feat(spa): Quick Commands v2 Phase 1b' — Plus hover popover (WORKSPACE_ACTIONS) (#701)

Quick Commands v2 Phase 1b' — adds the Plus-button hover popover entry-point for `WORKSPACE_ACTIONS` chips on each `WorkspaceRow` in the sidebar, with mobile/touch long-press fallback. Marked transitional and packaged as a stand-alone PR so it can be reverted/replaced later without touching the stable Phase 1b foundation (data layer / Settings UI / context menu).

- Desktop: hover (or keyboard focus) on Plus → chip popover unfolds left of the button. Mouseleave / blur the wrapper → popover collapses.
- Mobile/touch: long-press (≥500ms) on Plus → popover latches open; release-then-tap a chip executes. Short tap (<500ms) → original add-tab behavior unchanged. Tapping outside the hub closes the popover.
- `HostPickerPopover` portal'd to `document.body` so the picker escapes the transformed wrapper subtree and isn't torn down by hub blur events. Sync `onPickerOpenChange` notification from `resolveHostId` / `settlePicker` (not `useEffect`) so the hub's `pickerOpenRef` updates BEFORE child auto-focus fires blur on the chip.
- Wires `runWorkspaceSlot` with the #690 `assertContextLive` enforcement (alpha.242); passes a workspace-liveness probe for fail-closed during async `createSession`.
- `switchToSession` carries the canonical pre-check / read-back / rollback transaction so workspace-deletion mid-`executeCommand` leaves no orphan tab + no active-tab/workspace mutation.
- Long-press click suppressor is now one-shot — cleared inline in `onClick` and via `popoverOpen=false` effect, so post-touch mouse/keyboard activation isn't silently dropped on hybrid devices.
- Four rounds of codex review collapsed to zero findings: R1 standard (3 P2), R2 3-parallel attacker/defender/file-quality (5 medium-high), R3 verification (1 medium portal+focus race), R4 final approve. Twelve regression tests added across `WorkspaceQuickActionsPopover.test.tsx` + `WorkspaceRow.test.tsx`.

### Followup issues

- #689 — server-side orphan session cleanup (Phase 2)
- #695 — ESLint custom rule limiting `runWorkspaceSlot` / `runHostSlot` second/third arg cast/spread escape hatches

## [1.0.0-alpha.243] - 2026-04-28

### Docs(specs): hook→status→燈號 audit (W1, fix-spec PR-2) (#692)

Lights rebuild W1 — the canonical 577-line audit (`docs/specs/2026-04-28-hook-status-audit-spec.md`) for cc / codex / opencode that becomes the SOT for W3-W7. Drives §6 W5 燈號 bug 修復清單 (8 items) and §7 W6 ad-hoc ProbeIntent gap 清單 (6 items), and disciplines spec drift via §7.1 design constraints.

- §6 / §7 are the canonical work pools — §4 三家 agent 矩陣 only short-references them.
- §7.1 prevents the framework drift that triggered the fix-spec rewrite: probes go through `ProbeIntentProvider`, not synthetic hook events; no always-on probe; no generic `ProbeProfileProvider`; detectors live in `internal/agent/{cc,codex,opencode}/probe_intent_*.go`.
- §7.2 picks W6-3 (codex error) as the first ProbeIntent so the interface gets finalised against a real, lazy-shaped need.
- Two-round codex review converged with all findings addressed (R1 standard + R2 defender / spec-alignment per `feedback_codex_pr_review_spec_alignment`).
- Follow-up issues filed: #698 (daemon restart 後 activeWatchers 不恢復, W4/W6 platform prerequisite) + #699 (audit doc 拆檔評估 after W3-W6 ship).
- Pure docs change. Unblocks PR-3 W2 catalog naming separation (W1-independent) and PR-4 W3+W4 framework 撤回 + observability.

## [1.0.0-alpha.242] - 2026-04-28

### Feat(spa): enforce assertContextLive on runWorkspaceSlot Deps (#690) (#694)

Quick Commands v2 — type-level + runtime fail-closed defense in depth for the round-4 destructive-command guard introduced in PR #686. Future workspace-context Slot callers (Phase 1b' Plus hover popover is the first) can no longer silently regress the guard by omitting `assertContextLive`, and Phase 1c HOST_ACTIONS is now type-locked into a sibling `runHostSlot` rather than reusing the workspace executor.

- `runWorkspaceSlot.Deps.assertContextLive` is type-level required (was optional). Single production caller already wires it; no caller change needed for the 1b path.
- New `WorkspaceSlotContext = SlotContext & { workspaceId: string }`; `runWorkspaceSlot(ctx)` requires it, so `{ hostId }` + dummy probe can no longer slip through (round-2 D1).
- Runtime guard hardened to fail-closed: `typeof === 'function'` + try/catch + strict bool check. Cast-bypassed or throwing probes now route to the `switch_failed` toast path instead of unhandled-rejecting after `createSession` (round-2 A2).
- Type-level invariants verified by `tsc -b` (run via `pnpm run build`), not vitest. Conditional-type assertions (`IsAny` + `Pick`-required + null-rejection) replace the round-1 `@ts-expect-error` directive that could be silently consumed by unrelated `Deps` growth.
- Spec §3.3.1 + master plan superseded notes added so Phase 1b' / 1c implementers see the enforcement contract before copying historical examples.
- Three rounds of codex review converged: spec+plan pre-impl review (5 findings) → R1 standard (0) → R2 attacker / defender / file-quality (5 medium — D1/A1/A2/F1 fixed; A3 → issue #695 for ESLint custom rule) → R3 verification (1 high — branch base lagged main, rebased). Followup #695 (ESLint enforcement against type cast escape hatches) deferred as scope > #690.

## [1.0.0-alpha.241] - 2026-04-28

### Refactor(spa): migrate file path link detection settings to editor (P3) (#688)

Editor P3 — file-path link detection (the terminal feature that turns `path/to/file.ts` and `path/to/file.ts:42:7` into clickable links that open in the editor) is now scoped to the Editor module instead of bleeding into Terminal Settings. When Editor is disabled, the matchers also stop detecting, eliminating the prior gap where settings were hidden but matchers still ran.

- New `EditorLinkDetectionSection` component owns the file-path link detection toggles; Terminal Settings keeps only terminal-native link types.
- i18n keys for file-path link detection moved from `terminal.*` to `editor.*` scope.
- New `fileMatchersEnabled` flag gates all four file-path matchers (basename + path with/without line/column) on Editor module enablement; renamed from the round-1 `editorFilePathMatchersEnabled` after the defender / file-quality reviewers independently flagged the bare-filename matcher having the same boundary.
- HMR dispose path now calls `__resetBuiltinTerminalLinks` so a hot reload doesn't leak stale matcher registrations.
- Three rounds of codex review converged: R1 standard (1 P2 — settings hide but matchers still detect) → R2 attacker / defender / file-quality (2 medium — bare matcher boundary + HMR reset) → R3 approve (no actionable correctness issues). Known minor UX gap: bare-filename toggle remains visible in Terminal Settings when Editor is disabled, but runtime no longer detects (setting-without-effect; deferred polish).

## [1.0.0-alpha.240] - 2026-04-28

### Feat(spa): quick-commands v2 — Phase 1b (Settings UI + executor + workspace context menu) (#686)

Phase 1b of Quick Commands v2 — the first user-visible mount point for the v2 capability/binding/slot model. Settings UI lets users create / edit / delete commands and bind them to slots; right-clicking a workspace in the sidebar shows the bound `WORKSPACE_ACTIONS` chips and runs them through `slot-executor` (create session → optional host picker → send keys → switch focus). Five rounds of codex review converged.

- `inferWorkspaceHostId` — majority-vote host resolution from a workspace's tmux-session tabs (spec §3.2.1) with deterministic tie-break; returns null when the workspace has no tmux-session tabs so callers can open `HostPickerPopover`.
- `<HostPickerPopover>` — reusable host picker honouring spec §3.2.2 lifecycle contract (resolves to null on unmount / outside-click / duplicate-resolve).
- `<CommandSlot>` — shared component bridging bindings → bound capabilities → executor; `mountTo` / `hostId` / `busy` / optional custom `render` props.
- `slot-executor` — three-stage failure UX (create-session / send-keys / switch) per spec §3.3 with toast-precedence decision matrix; `assertContextLive` callback blocks `executeCommand` if the workspace was deleted while `createSession` was in flight (codex round-4 — destructive commands must not ship to a session whose context is gone).
- `useUndoToast` schema — optional `action` / `actionLabel` so create-session and switch failures can omit the action button while send-keys failure carries Retry (spec §3.3 / codex round-1 B4).
- `QuickCommandsSettingsSection` — list + edit dialog + multi-select mount chips with arrow-key roving focus (codex round-1 C15) and a11y (focus trap / Esc / aria).
- `WorkspaceQuickCommandsContextMenu` — mounts `<CommandSlot mountTo=WORKSPACE_ACTIONS>` on `WorkspaceRow` right-click; transactional `switchToSession` with pre-check, read-back, and rollback (closeTab + restore prevActiveTabId) so a deleted-workspace race leaves no orphan tab and no dangling active-tab mutation. Inherits `cwd` from `workspace.moduleConfig.files.projectPath` (spec §3.2).
- `quick-command-bindings.ts` consumers (CommandSlot / WorkspaceContextMenu / WorkspaceQuickCommandsContextMenu / QuickCommandsSettingsSection) all route binding lookups through `getBindingTargets` so capability ids colliding with inherited Object.prototype methods (`toString` / `valueOf` / etc.) can't crash the slot host.
- Five rounds of codex review converged: R1 standard (2 P2) → R2 attacker / defender / file-quality (4 findings) → R3 transaction layer B (orphan tab) → R4 transaction layer C (destructive command) → R5 final approve. 13 findings closed; 2 follow-ups tracked: #689 (server-side orphan session cleanup) and #690 (assertContextLive wiring enforcement).

## [1.0.0-alpha.239] - 2026-04-28

### Feat(spa): tabs cluster by kind on insert (P2) (#684)

Editor P2 — tab insertion no longer appends every new file to the very end of the workspace tab strip. When a file kind already exists to the right of the active tab, the new tab clusters next to it instead.

- New `findInsertTarget` predicate-based helper (generalized from the previous browser-only `findBrowserInsertTarget`).
- New `computeClusterInsertTarget` helper anchors on `ws.activeTabId` (workspace-scoped) so async terminal-link opens that race a workspace switch can't return an `afterTabId` belonging to another workspace.
- New `openClusteredTab` helper guarantees the same `afterTabId` is forwarded to BOTH `useTabStore.openSingletonTab` and `useWorkspaceStore.insertTab` — the TabBar renders from `workspace.tabs`, so the two stores must agree on placement.
- Three call sites adopt the predicate: `open-browser-tab` (browser-only, refactored), `terminal-link` file-path opener, `FileTreeWorkspaceView`. Editor / image-preview / pdf-preview tabs cluster together; sessions and browser tabs are unaffected.
- Three rounds of codex review: round-1 caught a tabOrder vs workspace.tabs divergence; round-2 (3-parallel attacker / defender / file-quality) independently flagged a cross-workspace `activeTabId` race; round-3 standard review saw no introduced bugs. Follow-up #685 tracks an intentional gap (existing singleton file tabs not auto-repositioning on re-open).

## [1.0.0-alpha.238] - 2026-04-28

### Refactor(spa): extract quick-command-bindings module + split store tests (#682)

Closes #679 (Q2 + Q3) — codex round-2 followups deferred from PR #677. Pure-data primitives (`QuickCommand` / `Bindings` / `QuickCommandData` / `UNSAFE_KEYS` / `sanitizeBindings` / `getBindingTargets` / `mergePersistedQuickCommandState`) move out of the store into a standalone module.

- New `spa/src/lib/quick-command-bindings.ts` — schema, sanitizer, own-property guard, persist merge function. `useQuickCommandStore` reduces to state + actions + persist + sync glue and imports from the pure module. Sync contributor + its test import `sanitizeBindings` and `QuickCommand` directly from the pure module instead of pulling them through the store.
- `mergePersistedQuickCommandState` is now generic over `T extends QuickCommandData` so the store can pass its full state (data + actions) and recover the same shape — action methods survive the merge. New test asserts this contract.
- 366-line `useQuickCommandStore.test.ts` split into three focused files: `lib/quick-command-bindings.test.ts` (sanitizer + own-property guard + hydrate boundary), `stores/useQuickCommandStore.crud.test.ts` (capability CRUD), `stores/useQuickCommandStore.bindings.test.ts` (store-level binding integration + null-hostId + prototype-key safety).
- Two rounds of codex review approved with no findings.

## [1.0.0-alpha.237] - 2026-04-28

### Feat(spa): editor module owns file openers + disabled placeholder (P1) (#675)

Editor module is now self-contained: it declares its three file openers (Image / PDF / Text) instead of having `register-modules.tsx` register them globally. When Editor is disabled, panes show a `DisabledModulePlaceholder` with reload-required hint instead of silently rendering nothing.

- New `ModuleDefinition.fileOpeners` field; `applyModuleFileOpeners()` helper applies them owner-scoped.
- `file-opener-registry` switched to nested `Map<owner, Map<id, opener>>` to avoid cross-module key collisions, with `clearFileOpenersForOwner()` HMR-safe disposal.
- New `DisabledModulePlaceholder` component + i18n keys (`module.disabled.*`) used by `PaneLayoutRenderer` and `NewTabPage` when the owning module is disabled.
- `module-registry.ts` now exposes `RendererResolution` metadata so renderers can stay free of UI imports (no lib→UI reverse dep).
- 478-line `register-modules.tsx` split into `register-modules/{index,editor-module,fs-backends,module-file-openers}.tsx` (transitional shim re-exports from the subdir).
- Reload-required contract locked across bootstrap: `applyModuleFileOpeners` / `registerEditorNewTabProviders` / `dispatchSettingsContributions` run at bootstrap; `PaneLayoutRenderer` snapshots `enabled` map at mount; `NewTabPage` uses `useMemo([])` to capture providers + isEnabled snapshot.
- Five rounds of codex review converged (5 → 3 → 1 → 1 → 0 findings). Two follow-ups tracked: #678 (immediate enable/disable UX redesign) and #674 (baseline test failures: TabBar pinned tooltip + sync hosts token preservation).

## [1.0.0-alpha.236] - 2026-04-28

### Feat: quick-commands v2 — Phase 1a (data layer) (#677)

Pure data layer for the v2 capability/binding/slot architecture — no UI, no executor. Phase 1b will add the Settings dialog, CommandSlot rendering, and workspace context-menu entry.

- New `QUICK_COMMAND_SLOTS` typed constants (`workspace.actions`, `host.actions`) — slot id namespace and mountTarget invariants.
- `useQuickCommandStore` adds `bindings: Record<commandId, slotId[]>` + `sanitizeBindings` (accepts any non-empty string as a forward-compat slot id per spec §2.3) + `__proto__`/`constructor` unsafe-key guard.
- Sync contributor exposes a `bindings` field; hydrate merges via `mergePersistedQuickCommandState`.
- `quick-commands` is promoted to a disableable module (mirroring editor / browser) and can be toggled in Settings → Modules.
- Two rounds of codex review: round-1 closed 2 P2 findings; round-2 ran three parallel reviewers (attacker / defender / file-quality) and closed 5 findings (prototype safety, sanitizer hardening, spec §2.3 forward-compat alignment, null-hostId ordering lock, sync normalize).

## [1.0.0-alpha.235] - 2026-04-28

### Fix(electron): stabilize mac signing for updates (#672)

Stabilize macOS app identity for packaged builds and dev updates.

- Stop explicitly disabling Electron macOS signing and verify final moved app bundles.
- Re-sign the `.app` after dev update swaps bundled `out/` resources, preserving `dev.wake.purdex` identity.
- Track `scripts/build-electron.mjs` in full-rebuild detection and add signing guard tests.

## [1.0.0-alpha.234] - 2026-04-27

### Feat(probe): probe primitive + cc + helper + dev log (#670)

Phase 4a PR-4a-1. Architectural pivot: probe layer is now **dumb** — it
observes raw screen events; the orchestrator (new
`internal/module/agent/probe_orchestrator.go`) owns status-transition
dedup via `currentStatus` comparison. No emit-once flags, no Rearm API.

- New `tmux.CapturePaneRange` + `CapturePaneTopLines` (with `-e` to
  preserve ANSI so spinner color changes still produce diff hashes).
- Probe primitive: `Watch(target, opts, cb)` long-lived watcher emits
  `ScreenChanged` on every diff tick and `ScreenStable` every
  `IdleStableTicks` consecutive identical hashes. Replaces the legacy
  `ActivityCallback` fire-once-then-exit watcher.
- New `agentpkg.ProbeProfileProvider` optional interface lets agents
  tune capture region + idle threshold. cc adopts it (TopLines: 12,
  IdleStableTicks: 3); codex/opencode fall through to default profile
  (BottomLines: 10, IdleStableTicks: 3) — same capture region as
  legacy `activityLoop`.
- Orchestrator `interpretScreenEvent` applies: stale-callback guard,
  graceWindow suppress (2s post-hook hook authority), Error Guard,
  v2.0 transition gate, atomic stale-callback revalidation, and a
  ScreenStable cheap pre-gate (skip bottom capture when already Idle
  with alive top-frame PID).
- 4 new expvar counters under `purdex_probe_*` (kept separate from
  `purdex_phase35_*`). `PDX_DEV_MODE=1` enables `[probe]` log channel.
- `manageActivityWatch` + `renameSessionLocked` migrated to orchestrator;
  `lastHookAt` migrates across rename so graceWindow stays in effect.
- Plan went through 15 rounds of codex review + a v2.0 architectural
  pivot (consulting B vetoed dedup-in-probe premise) + 3 rounds on the
  PR (1 standard + 3 adversarial + 1 convergence check). 39 new tests,
  race-clean.

## [1.0.0-alpha.233] - 2026-04-27

### Feat(agent/opencode): OpenCode 1.14.23 hooks completion (#664)

Phase 4a PR-4a-0. Audit and freeze the OpenCode 1.14.23 hook surface, switch the plugin from the deprecated `session.idle` Bus event to the canonical `session.status` filtered to `{type:"idle"}`, and ship a boundary-enforcement script for the PR-4a-0 file scope.

- Audit all 65 upstream OpenCode hooks (19 strong hooks + 48 Bus events) and freeze the catalog with a full-stage manifest + 9-event payload fixtures + provenance source map.
- Decision 3 (switch): plugin template moves from `session.idle` to `session.status` filter; single `case` swap, no Map/dedup/dual-subscribe.
- Decision 4 (defer): `session.status` busy/retry variants received-but-no-op; trigger conditions tracked in #661.
- Add HC5 / HC5b / HC5c / HC5d catalog ↔ manifest contract tests; HC5e enforces H6.4 partial-stale policy invariants.
- OC1 / OC1a contract tests (18 sub-tests) verify post-Decision-3 mapping against rendered-template payload fixtures.
- Fix wave: clear stale idle suppression on new `chat.message`; correct SupportedVersion safety-net framing in audit doc; remove legacy pluginState dead code; add rendered-template parity guard.
- Boundary script (`scripts/check-pr-4a0-boundary.sh`) uses three-dot diff to scope the file-scope assertion to HEAD changes only.
- Five rounds of codex review (R1 standard + R2a/b/c adversarial + final re-check); five fix wave commits address Round 2 findings.

## [1.0.0-alpha.232] - 2026-04-27

### Test(spa): guard OpenCode agent icon (#662)

Add focused coverage for the already-shipped OpenCode agent icon lookup.

- Verify `getAgentIcon('opencode')` renders the OpenCode SVG marker rather than a cc/Codex variant.
- Verify the OpenCode icon remains stable across the full cc/Codex icon variant matrix.
- Document PR6 as a coverage-only guard with no production icon or tab rendering changes.

## [1.0.0-alpha.231] - 2026-04-27

### Fix(agent/opencode): report hook support version (#659)

Report OpenCode hook support metadata without changing the managed plugin mapping.

- Add OpenCode `SupportedVersion=1.14.23` and `ExceedsSupport` reporting across `CheckHooks` result paths.
- Cover missing, unmanaged, drifted, installed, and version comparison paths.
- Defer deep OpenCode runtime/source contract validation to #658 to match the cc/Codex install-check verification depth.

## [1.0.0-alpha.230] - 2026-04-26

### Feat(agent): classify cc and Codex hook catalogs (#655)

Classify Claude Code and Codex hook catalogs without changing runtime install behavior.

- Add version-pinned Claude Code upstream hook catalog assertions, including non-installable `ignored` / `unsupported` events.
- Add Codex current-docs upstream subset assertions while retaining existing Purdex compatibility installable entries.
- Keep installable hook sets stable: Claude Code = 9, Codex = 9.
- Keep OpenCode event contract and mapping refresh deferred to follow-up PRs.

## [1.0.0-alpha.229] - 2026-04-26

### Chore(dev): rename `PDX_DEV_UPDATE` env var to `PDX_DEV_MODE` (#653)

Generalize the dev mode env flag to host future dev observation features (e.g., Phase 4a graceWindow logging). Pure search & replace across live code + docs; historical CHANGELOG entries and archived `docs/superpowers/{plans,specs}/2026-04-18-*` are intentionally preserved as immutable historical record.

- `internal/module/dev/module.go` — env check + log message + comment
- `internal/module/dev/{module,daemon}_test.go` — `t.Setenv` calls
- `electron/{preload,updater}.ts` — `process.env` access + comment
- `CLAUDE.md` + `AGENTS.md` — docs

`config.Dev.Update` config field is unchanged — it specifically gates the update feature; only the env var was generalized.

## [1.0.0-alpha.228] - 2026-04-26

### Fix(agent): hook install and cleanup correctness (#645)

Agent hook installer/checker hotfix. Hook handling now distinguishes classified catalog entries from installable events, and cc/Codex cleanup ownership is explicit and provider-local.

- Add `HookHandling` helpers and scope installer/checker/template completeness to installable `status/detail` events.
- Codex hook install now enables and checks `features.codex_hooks`, preserves config file permissions, and treats `PermissionRequest` as a current required event.
- Codex/Claude cleanup now removes only Purdex-owned hook commands, preserves unknown same-provider tokens and third-party shapes, and rejects unsupported hook roots/install shapes before writing.
- Codex remove no longer leaves empty event keys that Purdex later reports as broken.
- Regression tests cover installable filtering, ownership cleanup edges, unsupported shape preservation, and strict `pdx hook` command grammar.

## [1.0.0-alpha.227] - 2026-04-26

### Feat(agent): Phase 3.5b — sweep canonicalize defense-in-depth (#650)

Sweep 加第三 pass `canonicalizePane`，把 PR-3.5a hot-path 漏網的 standalone descendant frame 在 2s 內 fold 進 cross-type ancestor proxy ref，補完 DB-level eventual consistency。Defense-in-depth — user-visible correctness 已由 PR-3.5a projection dedup 保證；3.5b 把 DB 層 partial state 的存活時間從「等 SessionEnd / 1h idle / pid_dead」縮到「下次 sweep tick ≤ 2s」。13 commits（4 plan v1→v4 + 5 impl + 4 review fix）；4 plan rounds + 5 PR rounds 共 9 輪 codex review 收斂。

**Hybrid B+ 四層架構完成**

| 層 | 職責 | 落地 |
|---|---|---|
| Hot path | best-effort canonicalization；失敗不 retry/rollback | 3.5a |
| Projection | 隱藏 partial（**唯一 strongly consistent 邊界**） | 3.5a |
| **Sweep** | **bounded-time recovery（2s）** | 3.5a prune + **3.5b canonicalize** |
| Observability | partial 發生率 expvar | 3.5a + 3.5b 接 `MetricSweepCanonicalized` |

**新增 / 改動**

- `internal/module/agent/frame_ops.go`：抽出 `candidateHasOwnedState(candidate)` 共享 helper（mirror hot-path inline block 1098-1137；pure refactor 0 行為變更）
- `internal/module/agent/sweep.go`：
  - `canonicalizePane(paneID, broadcastTs)` — 四 case branching：silent-skip（parent 已有 ref + owned state）/ delete-only（parent 已有 ref + 無 owned state）/ attach-only F2 recovery（parent 缺 ref + owned state）/ attach+delete 主路徑
  - `findCanonicalAncestor(candidate, framesByPID)` — PPID 鏈走 `proxyMaxDepth=5` 找 cross-type live identity-verified ancestor frame
  - `broadcastProxyCanonicalized` — per-pane broadcast pattern
  - `sweepOnce` 第三 pass：每 pane `canonicalizePane` 跑在 `pruneDeadProxyRefs` 之前
  - **F1+F4 hoisted ancestor revalidation** — `isPidAliveFn` + `processStartTimeFn` 在 DeleteIfUnchanged 前共同前置點重驗，覆蓋 attach-then-delete 與 delete-only 兩條路徑
  - **F2 owned-state attach-only recovery** — 在 parent 缺 proxy ref 且 child 有 owned state 時 attach（projection_dedup 可隱藏 + merge），不刪 row 保留 native state
- `internal/agent/metrics.go`：`MetricSweepCanonicalized` 接上 caller（PR-3.5a 已宣告但無 caller）

**測試**

- 19 個 integration tests（IT10 + IT10b-IT10s）涵蓋 race interleavings、identity gates、partial recovery 路徑、F1/F2/F4 closure regression guards
- 11 個 unit tests（RC6-RC11 `findCanonicalAncestor` + RC12-RC16 `candidateHasOwnedState`）
- 23 packages 全綠；無 skip

**Codex review trail**

- Plan：R1 high (hasOwnedState 缺) → v2 / R2 high (IT10n 不精準) → v3 / R3 approve
- PR：R1 standard clean / R2 三角度 3 findings (F1 attacker high + F2 defender high + F3 file-health medium) / R3 closure F4 high (delete-only bypass) / R4 closure 1 medium (plan drift) / R5 approve
- Fix commits：F1 → `3c4ebd07` / F2 → `38eef1a6` / F3 → `a2aafdf2` / F4 → `f46f616c` / Plan v4 → `d8c454cf`
- 多輪 high 嚴重性遞減符合 `feedback_codex_review_termination.md` 終止條件

**Plan**

`docs/specs/2026-04-26-lights-rebuild-phase-3-5b-plan.md` v4（含完整 review trail）

## [1.0.0-alpha.226] - 2026-04-26

### Feat(agent): Phase 3 — daemon restart frame recovery + no_parent_fallback (#638)

Lights rebuild Phase 3。daemon 重啟後若 hook 事件抵達時 frame 不在記憶體，改走 PPID descendant tree 重建擁有者；沒有 parent 時走 explicit `no_parent_fallback` reason 而非舊的 binary `parent_frame_found / missing`。2 輪 plan review + 2 輪 code review 收斂。

- `Prober.FirstAliveAgentInTree` + `identifierOrder` slice 提供 PPID descendant tree 上對 cc/codex/opencode 的 deterministic first-match（避開 Go map iteration 不穩定）。
- `tryRebuildFromProcessTree` helper 在 `applyFrameEvent` fallback 鏈中先試重建 frame ownership，再退到 `no_parent_fallback`。
- 三態 reason（`parent_frame_found` / `daemon_restart_recovery` / `no_parent_fallback`）取代舊的 binary，配合 grep guard 確認 production code 無遺留舊字串。
- `FrameTraceMeta.MatchedAgentType` 把 type 信號保留到 trace step `after` payload（給 Inspector / 未來 reparent 使用）。
- Code R1（standard）：`PanePID` 在多 pane window 會選到錯 pane → 改用 `ActivePanePID`，補測試與註解。
- Code R2（adversarial）：fail-soft missed identifier 在 `FirstAliveAgentInTree` 會 panic → double-deferred recover 包 `safeIdentifyPID` + outer top-level，新增 P7/P8 + R9/R10 regression。
- Tests：23 個 probe / frame_ops / handler 測試（P1-P8 / R1-R3+R5-R8+R9-R10 / N1-N3 / I1-I3）；既有 PR-2b 0 regression。Pre-existing same-type bugs（`verify.go:82` / `IsAliveFor`）out of PR scope，由 issue #639 追蹤。

### Feat(agent): Phase 3.5a — cold-start proxy canonicalization (#644)

修正 PR-2b（alpha.221）`findProxyParent` 留下的 cold-start race：兩個跨 type 的 SessionStart 同時抵達同一 pane（典型情境：daemon 重啟同時 cc + codex proxy spawn）時，pre-Upsert PPID walk 兩邊都 miss，造成兩個 standalone frame，少了 canonical parent + proxy ref。Hybrid B+ 設計（consulting 驅動）— 接受 partial state 為 ephemeral telemetry table 的合法狀態，user-visible correctness 由 daemon-side projection dedup 保證；DB-level eventual consistency 留給 sweep（PR-3.5b 後續）。5 輪 codex review，14 個 finding fix + 2 個 deferred。

**後端 race fix（`internal/module/agent/frame_ops.go`）**

- `pidIsAncestorOfWithCap`（深度 5）走 descendant 的 PPID 鏈找 ancestor PID。
- `canonicalizeDescendantsAfterUpsert`（self-as-ancestor）：ancestor SessionStart 後掃 pane 上跨 type、live、identity-verified 的 standalone descendant，PPID 鏈穿過 self 就 fold 成 proxy ref + best-effort 刪除原 standalone 列。
- `reconcileCreatedFrameAsProxy`（self-as-descendant）：descendant 在 post-Upsert 走 ancestor walk + attach proxy ref + best-effort delete 自己 standalone frame；**沒有 rollback** — partial state 合法、由 sweep 修。
- `applyFrameEvent` 三段 wiring：§2.2.1 new-frame post-Upsert（reconcile → descendant-scan）、§2.2.2 existing-frame **filter-merge-retry**（取代 v5 三步 snapshot+reset+re-attach 的 concurrent-attach race）、§2.3 SessionEnd hot-path proxy cleanup（best-effort `removeProxyRefForSender`，先 detach 後 delete）。
- Cross-frame native ID collision（S1）：proxy dedup baseline 改 owner-aware，`(IsProxy, ID, SourcePID, SourceStartTime)` 同時比對。
- Filter-merge baseline（T1）：`(Type, ID, StartedAt)` 三元組做 identity gate，避免 ID-only 漏 reset 同 ID 的舊 subagent。

**Projection dedup（`internal/module/agent/projection.go`）— 唯一 strongly-consistent 邊界**

- `buildPaneProjection` 在 parent.Subagents 含 `IsProxy + (SourcePID, SourceStartTime)` 時，把對應的 standalone frame 從 `TopFrame` 候選中排除 — SPA 永遠看不到 partial。
- 全部被 claim 時 fallback 回 unfiltered（極端 edge case）。

**Observability（`internal/agent/metrics.go`，新檔）**

- 4 個 expvar counter（in-process）：`partial_state_observed` / `projection_dedup_applied` / `partial_canonicalization_created` / `sessionend_cleanup_applied`。Endpoint exposure 之後 follow-up。

**Tests — 全部 23 packages 綠**

- Unit：RC1-RC8 race interleavings、PD1-PD3 projection dedup edge cases。
- Integration（real sqlite）：IT1-IT9 + IT11 + IT12 + IT15-IT20，含 IT4 ancestor-late race（核心 J1/F1 regression guard）、IT12 SessionEnd ghost dot（Side B 發現的 bug）、IT20 concurrent attach during retry（J1 regression guard）。

**Plan + review trail**

- Plan v1→v12，2 輪平行 architectural consulting（Side A SQL transaction vs Side B Hybrid B+）。v3→v4 為 paradigm shift（接受 partial state 取代 patch-over-patch retry/rollback）。
- 5 輪 codex review 後 finding 收斂在 1 high/輪（diminishing returns）；所有 high/medium 修完，2 個 deferred 開 follow-up issue（N1 projection dedup hot-path liveness syscall perf；IT12b detach error injection 需要 `m.frames` interface 抽象 refactor）。
- 完整 plan：`docs/specs/2026-04-25-lights-rebuild-phase-3-5-plan.md`。

## [1.0.0-alpha.225] - 2026-04-26

### Feat(spa): add tab name tooltip controls (#642)

Tab name tooltip 新增可設定顯示範圍，並延後到滑鼠懸停 800ms 後才顯示，降低短暫滑過 tab 時的視覺干擾。

- Terminal 設定在 Dynamic tab name 下方新增 `Tab name tooltip`，可選不顯示、上方、左邊或都顯示。
- 上方 tab 與左側 activity bar inline tab 依設定分別控制完整 tab name tooltip 是否渲染。
- `HoverTooltip` 新增 800ms 延遲與離開前取消邏輯，並補對應 regression tests。

## [1.0.0-alpha.224] - 2026-04-25

### Fix(spa): keep activity bar home fixed (#640)

Activity bar 的 Home、workspace 分隔線與底部 actions 固定在原位，只有 workspace 清單區域捲動；同時修正 tab hover tooltip 被 overflow 容器裁切的問題。

- 窄版與寬版 activity bar 都改為 Home / divider / workspace scroll / footer actions 的固定結構，避免 Home 被納入捲動範圍。
- workspace 分隔線固定在 scroll 區外並加上 `shrink-0`，防止 flex 壓縮導致分隔線忽隱忽現。
- workspace scroll bar 使用 Purdex theme token 與透明 track，符合現有視覺風格。
- `HoverTooltip` 改用 body portal + fixed positioning，讓上方 TabBar 與左側 inline tabs 的完整 tab name tooltip 不再被 overflow 裁切。
- 補 activity bar scroll boundary、divider placement 與 tooltip portal regression tests。

## [1.0.0-alpha.223] - 2026-04-25

### Fix(spa): restore tab bar shrink behavior (#636)

Tab bar layout 修正，讓 tabs 在空間不足時先縮到最小寬度，再進入水平捲動，並清掉 overflow/active 視覺瑕疵。

- normal tab strip 改回可吃剩餘寬度並加上 `min-w-0`，移除 `min-content` minimum 導致 tab 無法 shrink 的約束；保留 `max-content` 讓 tab 少時新增按鈕仍貼在最後一個 tab 後方。
- 左右 scroll controls 拆成「漸層 fade 區 + 不透明按鈕區」，避免箭頭底下透出 tab label。
- active tab 改用透明 border，移除上緣不等長亮色邊框，同時保留 border 佔位避免尺寸跳動。
- 補 `TabBar` / `SortableTab` regression tests，鎖住 shrink layout、右側 scroll fade 結構與 active tab border 視覺。

## [1.0.0-alpha.222] - 2026-04-25

### Feat(agent): add tmux metadata dynamic titles (#634)

Agent dynamic title 改以 tmux active pane metadata 作為 source of truth，並在 Host > Agents 補上 `allow-set-title` integration 狀態與安裝/移除控制。

- 後端新增 tmux active pane metadata 查詢與 sanitize，讓 session/tab display 可從 pane title、window name 與 pane command 推導 agent title。
- Host > Agents 新增 `Agent title` 區塊，可檢查/安裝/移除 Purdex managed `allow-set-title` marker block；remove 不強制關閉 tmux runtime option。
- `allow-set-title` runtime check 改用 `tmux show-options -w -g -q -v`，避免新版 tmux 的 `show-window-option -q` 不支援錯誤。
- SPA title preference、agent store 與 sync contributor 同步支援新的 dynamic title source。

## [1.0.0-alpha.221] - 2026-04-25

### Feat(agent): Phase 2 PR-2b — proxy detection + idle sweep + SubagentDots visual (#631)

Lights rebuild Phase 2 收尾棒（PR-2a schema + wire 已於 alpha.218 落地）。把 PPID 祖先鏈 proxy 偵測、frame idle sweep、SubagentDots 視覺升級、以及 `subagents_json` 的多路 atomic RMW 全在一個 PR 內收齊。13 commits / 5 feat + 7 review-fix + 1 R4 test stub / 9 輪 codex review 全收斂 / R9 RMW inventory ship-ready approve。

**後端（internal/agent + internal/store + internal/module/agent）**

- **PPID 祖先鏈 proxy 偵測**（`findProxyParent`，depth=5）：cc pane 跑 `/codex:*` 不再建獨立 codex frame，而是掛進 cc parent 的 `Subagents` 標 `IsProxy=true`；live-only same-type hard-stop（dead/PID 重用的 stale 同 type ancestor 走 skip+walk-up，不阻 walk）+ `(SourcePID, SourceStartTime)` start_time identity 三分支
- **SessionEnd proxy cleanup**（`removeProxyRefForSender`）：proxy 來源 agent 結束時從 parent 移除 ref
- **Idle sweep 第三條規則**（1h + conditional DELETE）：無 hook 活動 1h 的 frame 被 sweep 清；用新 `DeleteIfUnchanged` store method 防 concurrent refresh 被 clobber；`setProjectionTopStatus` 在 probe 活動轉 Status 同時 bump `LastSeenAt` 避免誤清 live frame
- **`clearFrame` 拆 `afterFrameCleared` 共用 helper**：pid_dead / pid_reused / idle_timeout 三路徑統一 side-effects pipeline；同步修正 orphan StopWatch bug（清 frame 時若 session 有 active activity watcher 補 `StopWatch`）
- **Kind-aware `subagentRefMatches`**：`updateSubagents` 比對時 proxy 用 `(SourcePID, SourceStartTime)`、native 用 `ID`，`IsProxy` 不同永不 match — 防 proxy 合成 ID 字串與 native agent_id 撞
- **Atomic RMW on `subagents_json`**：新 `UpsertIfUnchanged` + `mutateSubagentsWithRetry` helper（proxy attach/detach + native SubagentStart/Stop 共用，bounded retry `proxyUpsertMaxAttempts = 3`）；probe path / general hook path / SessionStart reset 改 narrow column updates（`UpdateStatusAndLastSeen` / `UpdateHookPath` / `UpdateHookPathAndResetSubagents`），不碰 `subagents_json` — race 消失於 SQL 層

**SPA（spa/src/components + hooks）**

- `SubagentDots` API 從 `{count}` 升 `{refs: SubagentRef[]}`：每 dot 依 agent type 上色（`TYPE_COLOR` cc 藍 / codex 黃 / opencode 橘），proxy ref 渲染 hollow ring outline，native ref 保 solid fill
- 5 consumer props 同步：`TabIcon` / `SortableTab` / `InlineTab` / `renderInlineTabIcon` / `useTabDisplay`

**Tests**

- 29 新 Go tests：PR1-PR17（proxy walk + same-type hard-stop + identity 三分支 + R3/R6 race regression）/ F4-F9（store DeleteIfUnchanged / UpsertIfUnchanged / narrow updates）/ SE1-SE3（SessionEnd proxy cleanup）/ IS1-IS7（idle sweep + R1/R7 LastSeenAt bumps + RMW probe path）/ HB2（broadcast shape）/ U6-U7（kind-aware match + retry helper）/ HookRace1（R8 general hook RMW guard）
- 12 新 SPA tests + 1 regression（SubagentDots TYPE_COLOR + outline + 5 consumer props 整合）
- Go build/vet/test clean / SPA lint+build+vitest clean（baseline 3 hosts.test.ts pre-existing 無關）

**Review 收斂（9 輪）**

R1 standard（1 P2 idle sweep live-frame regression → `f26888a9`）/ R2 3-parallel adversarial（1 P1 proxy/native ID collision → `9f29e123`）/ R3 sanity（1 P1 stale same-type 阻 walk → `c46de72b`）/ R4 sanity（test coverage stub → `16485c44`）/ R5 final-gate（1 P2 proxy attach race，使用者判斷 in-PR 修 → `9ca25a3f`）/ R6 sanity（1 P2 native SubagentStart/Stop race → `269022e4`）/ R7 sanity（1 P2 probe status RMW race → `bd729cb7`）/ R8 sanity（1 P2 general hook RMW race → `bc39cbb3`）/ R9 RMW inventory（0 findings — approve ship-ready）。所有 9 路 `subagents_json` 寫路徑 R9 入清單分類驗證：proxy attach/detach + native SubagentStart/Stop 走 atomic-retry / 一般 hook 走 narrow / SessionStart reset 走 narrow-reset / new frame 走 insert-only / SessionEnd / probe / replay / migrate 各自封口。

關閉 #632（Proxy attach/detach on Subagents is not atomic — R5 follow-up，已在 R5/R6 兩路 atomic helper 一併修完）。

**關鍵技術決策**

- Proxy ID 格式：`fmt.Sprintf("proxy:%s:%d:%s", req.AgentType, req.SenderPID, req.SenderStartTime)` — kind-aware identity key 防 native agent_id 撞
- Proxy walk depth = 5（cover codex→codex-companion→cc layout + 3 hops buffer）
- Same-type hard-stop 前置於 alive + identity gate
- Store API invariant：`Upsert` 只給 new-frame insert；`UpsertIfUnchanged` 服務所有 RMW；narrow updates 不碰 `subagents_json` 除 explicit reset

## [1.0.0-alpha.220] - 2026-04-25

### Fix(spa): Editor UI polish — rotation, alignment, line highlight, settings layout (#629)

Editor Restructure 落地後的四個小 polish，純視覺對齊、無邏輯變動。

- 拼圖片（module-owned contribution marker）改為往右 30° 旋轉（原本 -12°），在 sidebar 10-12px 下輪廓更清晰；SettingsSidebar / HostSidebar / WorkspaceSettingsPage 三處同步
- EditorPurdexSettingsSection 重寫對齊 canonical pattern（Appearance / Terminal / Sync / Interface 共用的 `<h2>` + `<SettingItem>` 佈局），並把所有字串抽到 `settings.editor.*` i18n keys（EN + zh-TW 共 16 條）
- EditorToolbar 麵包屑路徑原本 `items-start` + `pt-0.5` nudge 導致文字偏上；改為 `items-center` 讓 path 與 action 按鈕同軸
- Monaco 當前行高亮從預設邊框改為 VSCode 風格的淡背景亮度：新定義 `purdex-dark` theme 繼承 `vs-dark`，將 `editor.lineHighlightBorder` 設為透明、`editor.lineHighlightBackground` 設為 `#FFFFFF0D`（約 5% 白）

## [1.0.0-alpha.219] - 2026-04-24

### Feat(spa): Editor restructure — Buffers pane + HSR migration + breadcrumb popover (#623)

把 Editor 模組收攏到 PR #617 的 module-owned contribution framework 下，同時把 `/buffer/*` 從 legacy settings tab 升級成正式 pane + breadcrumb popover 快切，並透過 `EditorPurdexSettingsSection` 讓 Monaco 六個 preference 成為使用者可控。六個 commit / 53 tests / 六輪 codex review（3 spec + 3 PR-diff）。

- 新 `useEditorSettingsStore`（Zustand + persist，key `purdex-editor-settings`）：`tabSize` / `insertSpaces` / `wordWrap` / `lineNumbers` / `minimap` / `fontSize` 六欄位 + 合理 defaults + clamp / merge-sanitize rehydrate；S1-9 regression guard 鎖住 happy-path rehydrate
- `MonacoWrapper.tsx` 所有 option 從 store 讀，無 hardcoded 殘留
- Editor 模組取得 `localId:'editor'` HSR entry（scope:`purdex`, order:9）；legacy `registerSettingsSection({id:'editor-buffers',...})` 移除；sidebar "Editor" 行為**首個**帶 puzzle-piece marker（`isModuleOwnedContribution` 首次 true）
- `SettingsPage` 對舊書籤 `/settings/editor-buffers` URL 做 alias redirect 自動導到新 section；i18n `settings.section.editor_buffers` → `settings.section.editor`（EN "Editor" / zh-TW "編輯器"）
- 新 pane kind `editor-buffers`：list（`/buffer/*` 直子，name-asc 排序）+ toolbar（New / Rename / Delete / Open）+ multi-select + smart-open + empty-state；`ManageBuffersNewTabCard`（order 6, `moduleId:'editor'`）以標準 `onSelect` 取代當前 NewTab pane（**非** singleton）
- Smart-open（spec §4.6）：active tab → tabOrder scan → fallback 新 tab；eligibility filter（`source==='inapp'` AND buffer 非 dirty）— 來源不同或 dirty 的 pane 跳過，永不靜默吞資料
- Delete flow（spec §4.9.5）：locked-tab 硬拒 + dirty-specific confirm + single-confirm；close loop 同時呼叫 `useTabStore.closePane` **與** `useEditorStore.closePane(paneId, bufferKey)` 避免背景 tab 的 stale buffer 復活已刪檔（G2）
- Rename：pre-check `backend.stat` 防止覆寫同名（F4）；`performBufferRename` helper 三步 sync（`backend.rename` → `useTabStore.renameEditorPanes` → `useEditorStore.renameBuffer`），rename 後 pane `filePath` + buffer key + paneState.bufferKey + buffer metadata（language / languageSource）全部跟進（G1 + R6 MED）
- Breadcrumb popover：EditorToolbar 的 Purdex chip 在 `source.type==='inapp'` 時成為 `<button>`；React portal + z-100（高於 Monaco popup）；列出所有 `/buffer/*`（aria-current 標當前）+ "Manage buffers..." 連結；`onSwitch` / `onNewBuffer` 帶 dirty guard（`window.confirm`）
- `openBufferByName(name)` helper — toolbar Open 與 row double-click 走同一入口，用顯式 name argument 取代 stale `singleSelected` closure（G3）
- Ancillary switch sites：`tabToUrl` / `getPaneLabel` / `getPaneIcon` 全加 `editor-buffers` branch；`NewTabProvider.moduleId?:string` 新欄位 + `NewTabPage` module-aware filter；NewTabPage 全 column filter 為 null 時 fallback 到 empty state（F8）
- Refactor：`createMetadata` / `detectLanguage` / `detectLanguageSource` / `untitledStoragePath` / `untitledSuggestedName` 抽到新的 `spa/src/lib/editor-language.ts` util，兩個 editor 元件共用，防止下一次 rename 流程再度漂移

53 個 Vitest cases：S1-1..9 / R1-1..5 / L1-1 / M1-1 / P2-1 / B2-1..18 / B2-16b / B2-16c / N2-1..3 / A2-1..7 / C3-1..7 / T3-1..2。

Review 歷程：Spec 三輪（R1 15 findings → R2 3 HIGH → R3 4 HIGH，全吸收 v1.1→v1.3）→ PR-diff 三輪（R4 1 CRIT 撤回 + 7 HIGH 作 Commit 4；R5 0 CRIT + 2 HIGH + 4 MED + 1 LOW，must-fix G1/G2/G3/G7 作 Commit 5；R6 focused adversarial 9/10 probe clean + 1 MED 作 Commit 6）。R5 的 G4/G5/G6 開 follow-up issue #625 / #626 / #627。



## [1.0.0-alpha.218] - 2026-04-24

### Feat(agent): Phase 2 PR-2a — SubagentRef schema + wire breaking upgrade (#622)

Lights rebuild 系列 Phase 2 第一棒，把 subagent 從 ID-only 字串升級為結構化 `SubagentRef`（agent ID + canonical Type + StartedAt + SourcePID + SourceStartTime + IsProxy），為下一棒 PR-2b 的 proxy 偵測 / frame idle sweep / SPA 視覺區分鋪路。此棒只升 schema + wire + migration，視覺仍為 count-based。

- 新型別 `internal/agent/subagent.go` — `SubagentRef{ID, Type, StartedAt, SourcePID, SourceStartTime, IsProxy}`；`IsProxy,omitempty`；`source_pid` / `source_start_time` 非 omitempty（native refs 顯式序列化 0）；`Type` 固定用 `frame.AgentType`（canonical agent family，不吃 detail.agent_type）
- `store.Frame.Subagents` / `SessionProjection.Subagents` / `Module.subagents` map / `NormalizedEvent.Subagents` / SPA `useAgentStore` — 全部從 `[]string` → `[]SubagentRef`
- `updateSubagents(current, eventName, ref) → refs` 取代 string 版
- **Schema upgrade safety**：`migrateFramesDB` 掃全表 `subagents_json` 三態處理 — new schema ✅ pass / legacy `[]string` → truncate / malformed → `fmt.Errorf` block daemon 啟動（operator-visible，不靜默吞）
- `Module.New` frames init 失敗 fatal（不吞）；traces init 失敗 log warning + continue nil（best-effort，不阻啟動）
- SPA TabIcon / SortableTab / InlineTab / renderInlineTabIcon 仍 count-based — 視覺升級留給 PR-2b
- 新 test helper `store.AgentEventStore.ExecRawForTest` + `framesInitFn` / `tracesInitFn` test seams（顯式 ForTest 命名 + cross-package test seed）
- 14 個新 Go tests：SubagentRef JSON R1-R3 / store F1-F3 / updateSubagents U1-U5 / HB1 broadcast shape / Migration legacy-mixed-malformed-new（4）/ New fail-on-malformed + traces-best-effort（2）；配 ~20 SPA fixture upgrade
- **破壞式升級通知**：daemon 首次啟動會自動把舊 `[]string` 形式的 `subagents_json` truncate 為空陣列；如遇 malformed JSON 會 refuse 啟動並 log row id，需手動修（不會靜默丟資料）

11 commits：5 subagent TDD + 6 codex fix + 6 plan docs。Plan review 5 輪（v1→v6，findings 4→2→2→2→0）；Code review 4 輪全採納：R1 標準（P1 legacy DB 500 + P2 canonical Type）、R2 3 parallel 攻防體質（P1 LIMIT 1 probe miss mixed + P2 malformed wipe）、R3 sanity（P1 Module.New 吞 err）、R4 sanity（P1 R3 修過頭 traces 誤 fatal）。



### Feat(agent): Lights rebuild — Hook events declaration (#613) + §2.4 guardrails (#616)

把 Hook events 的 SSoT 從分散在三家的 emitter 集中到 `HookInstaller.Events()`，讓 installer / CheckHooks / SupportedStatuses derivation / 未來 Inspector UI 讀同一份 `HookEventSpec` 清單；順帶補齊 codex 9-event installer、drift 測試、plan spec §2.4 架構護欄，以及上一輪 code review 後三輪 codex review 的 11 個 findings 修復（含 FutureOnly bit、byte-exact template 比對、quote-aware command tokenizer 等）。

- `HookEventSpec{Name, EmitsStatus, Description, FutureOnly}` 取代分散在三家的 string slice；`DeriveSupportedStatuses(specs)` 以 union 推導 `SupportedStatuses()`
- `HookStatus{Managed, UpgradesAvailable}` + `HookEventInfo{FutureOnly}` 新欄位讓 UI 區分三面向：是否受管（Remove 按鈕條件）、可否升級（Install 按鈕條件）、per-event 是否為 FutureOnly tolerated-absent
- codex installer 擴張到 9 event（3 主線 + 6 FutureOnly: SubagentStart/Stop、StopFailure、Notification、PermissionRequest、SessionEnd）；legacy 3-event 安裝保持健康且可自助升級
- codex `CheckHooks` 嚴格 per-event 驗證 — `isPdxCommandCodexForEvent(cmd, eventName)` + quote-aware `tokenizeCodexCommand` 支援 `/Applications/Purdex Beta/pdx` 含空白路徑
- opencode `CheckHooks` 改 byte-exact template 比對；`resolveCanonicalPdxPath()` 用 `os.Executable() + EvalSymlinks` 取 trusted path，檔內 `pdxPath` 不再自我圓滿
- drift test per-event 強相等斷言；template/specs parity 改 `TestTemplateSpecsParity` test-only guard（`renderManagedPlugin` 不再 runtime panic）
- `HookModuleCard`：Remove 按鈕 `managed ?? installed`（tmux 等無 managed 欄位 API 相容）；Install 對 `upgradesAvailable` 非空仍 enable + FutureOnly event 灰色標籤
- codex review 軌跡：4 輪 plan review（findings 4→3→2；v4 user-cancel）+ 2 輪 4 路 code review（第 2 輪 4 findings + 第 3 輪 5 findings）+ 1 輪限定 standard review（2 findings）— 全數採納
- deferred finding #5 追蹤為 follow-up（結構化 parser / codegen 取代 regex；install/check 路徑也跑 validator）

## [1.0.0-alpha.216] - 2026-04-24

### Feat(spa): Modules Switchboard + module-owned contribution marker (#617)

把閒置已久的 Settings → Modules tab 重新實作為 **Module 開關面板**（switchboard），並加上一個 puzzle-piece UI marker，讓使用者可以辨識哪些設定入口來自真正的 module（非 built-in / legacy adapter）。

- `ModuleDefinition.disableable?: boolean` + `descriptionKey?: string` — 預設 `false`，opt-in 形式，core module 不進 switchboard
- `useModuleEnabledStore` — persist 的 `Record<moduleId, boolean>` + session 內 in-memory baseline；`hasPendingChanges()` 驅動「Reload required」banner
- `buildSettingsContributionBatch` 對 disabled module 整批跳過 `settings: [...]` dispatch（purdex / workspace / host 三 scope 皆影響）；legacy adapter 與 host-builtin contributions 不受影響
- 初期 3 個 module flag `disableable: true`：`editor` / `browser` / `memory-monitor`
- `ModulesSwitchboardSection` 註冊在舊 `module-config` 位置（URL 保留），每行：名稱 + 描述 + Toggle + 上方 Reload banner
- `isModuleOwnedContribution(c)` helper（`moduleId` 不以 `_builtin.` 開頭）→ `SettingsSidebar` / `HostSidebar` / `WorkspaceSettingsPage` 在 row/section title 尾端渲染 `PuzzlePiece`（rotated -12°）
- 關閉 #574；兩輪 codex review 4/4 採納（SR-2 + AR-1/2/3），1 個 deferred finding 建 issue #618（legacy `globalConfig` / `workspaceConfig` + `ModuleConfigSection` 全面退場 cleanup）

不在本 PR（追蹤中）：
- PR 2：Editor Buffers → Editor 重命名 + HSR migration + Buffers 管理 pane + breadcrumb quick-switch popover
- PR 3：pane-level / fs-backend / file-opener disable 整合 + runtime hot-switch + workspace-scope legacy filter（之後 `files` 可重新 flag `disableable`）

## [1.0.0-alpha.215] - 2026-04-23

### Feat(agent): Lights rebuild — Phase 1 L2 status alignment (#612)

Lights 子系統 rebuild 系列第二棒，把 Phase 0 留白的宣告矩陣填滿、補齊 codex 事件覆蓋、處理未知/已知不可導出事件的觀測性。

- 三家 provider（`cc` / `codex` / `opencode`）實作 `SupportedStatuses()`，宣告 `[Running, Waiting, Idle, Error, Clear]`
- `codex/status.go` `DeriveStatus` 補齊 5 個事件：`Notification`（permission/elicitation→Waiting，idle/auth_success→Idle）、`PermissionRequest`、`SubagentStart`/`SubagentStop`（detail-only）、`SessionEnd`（Clear）、`StopFailure`（Error）
- `spa/src/lib/agent-icons.tsx` 補上 OpenCode brand icon（`@lobehub/icons-static-svg/icons/opencode.svg`）
- `handler.go` 新加 invalid-result 早退分支：`DeriveResult.Reason` 區分 `event_not_in_catalog` / `compact_ignored` / `notification_unknown_type`，trace 寫一筆 verify-kind step + 清除 legacy `agent_events` row（避免 replay/snapshot 回灌）
- Error guard 對 `SessionEnd` 統一放行（codex StopFailure→Error 後 SessionEnd 終於可清）
- 新檔 `internal/agent/drift_test.go`：宣告 ↔ 實作的 per-fixture + set-equality 雙重斷言，確保刪掉任一 Notification 子分支會被抓到

13 commits（8 implementation + 5 review-fix）。兩輪 codex review（標準 + 3 parallel 攻防體質），6 findings 採納修復（5 必修 + 1 doc + issue），2 follow-up issues：#613（codex hook installer alignment）+ #614（file-quality cleanup）。

## [1.0.0-alpha.214] - 2026-04-23

### Feat(agent): Lights rebuild — Phase 0 Status alignment skeleton (#610)

Lights 子系統 rebuild 系列第一棒，導入最小骨架讓後續 Phase 可對齊 agent Status。

- 新 optional interface `StatusSupporter`（`SupportedStatuses() []Status`）— 與既有 `HookInstaller` / `StatuslineInstaller` 同型
- 新 `Coverage(Registry)` helper 產出 agent 宣告矩陣（`[]CoverageRow`）— 保留 registry 註冊順序（Claim priority 語意）、`Declared` defensive copy、nil → `[]Status{}` normalize
- 零改動既有 provider / handler / module / probe — Phase 0 僅骨架，Phase 1 才各 agent 填入實作
- 附帶完整 tightened 版 Lights rebuild spec（6 Phase roadmap）+ Phase 0 TDD plan + 討論文件

6 commits + 2 codex review（R1 標準 approve / R2 三視角 3 findings 採納 + 2 findings 維持判斷 + 1 findings 事實承認修 docs）。8 個 Coverage 測試涵蓋空 registry / 已宣告 / 未宣告 / defensive copy / nil normalize / 註冊順序 / duplicate AgentType / 混合情境。

## [1.0.0-alpha.213] - 2026-04-23

### Feat(spa): HSR PR-5 — Editor homePath first module user + #540 immutable snapshot + deprecation warn (#604)

HSR 系列最後一棒。Editor module 透過新 `ModuleDefinition.settings` 宣告 `workspace-home-path` + `host-home-path` 兩個 contribution，作為整套 registry + 三層 store 架構的 real module validation proof。

- Editor tilde path 層疊 resolve：workspace settings → host settings → pane shell fallback
- `LinkContext` 擴 `workspaceId?`，由 `SessionPaneContent` 用 `findWorkspaceByTab()` 查後 plumbing 到 `TerminalView`（非 active — multi-workspace inactive pane 正確性）
- 三層 store `get()` 改回 frozen deep-clone，WeakMap 處理 alias / cycle（closes #540）
- 新增 `removeKey(scope, moduleId, key)` store API — section 清 homePath 不會誤刪 sibling 鍵
- file-path opener 在 await 前 snapshot link-source workspaceId；standalone pane 延後讀 active
- Editor home path input：focus ref + dirty ref + live store re-read，防 BroadcastChannel 同步覆寫 / stale snapshot
- `ModuleDefinition.globalConfig` / `workspaceConfig` 加 `@deprecated` + register-time warn（豁免 `files`；de-dupe；HMR reset 會清）

17 commits：plan §8 6 + R1 codex 3 + R2 codex (3-way adversarial) 5 + R3 codex 3。4 輪 review 收斂 HIGH/P1 → P2/P3 → known issues 追蹤（#607 workspace layer 在 mixed-host workspace 的 host scope 缺口 / #608 workspace 刪除中 opener orphan tab race）。

測試新增 ~75 個 PR-5 cases 全綠，完整 suite 2644 passed（3 pre-existing hosts.test.ts fail 無關）。

## [1.0.0-alpha.212] - 2026-04-23

### Fix(dev): exec rebuilt daemon binary after rebuild (#605)

- `POST /api/dev/daemon/rebuild` 在成功 rename `bin/pdx.new -> bin/pdx` 後，原本仍用目前行程的 `os.Executable()` 做 self-exec。當 daemon 不是從 repo 內的 `bin/pdx` 啟動時，重啟會回到舊 binary，UI 會看到 build 完成與 restarting，但實際 daemon 版本不會更新。
- 現在改為直接 exec 剛完成原子替換的 `bin/pdx`，確保 daemon self-rebuild 真的切到新 binary。
- 新增回歸測試，驗證 rebuild SSE 跑完後，exec target 指向 repo 內重建出的 `bin/pdx`。

## [1.0.0-alpha.211] - 2026-04-23

### Fix(agent): preserve symlink invocation path for versioned CLI binaries (#602)

- Claude Code 2.1.113+ 以 symlink (`~/.local/bin/claude`) 指向版本化 Mach-O binary (`~/.local/share/claude/versions/<X.Y.Z>`)。原先 `normalizeExecutablePath` 的 `EvalSymlinks` 會把 `ExePath` 解析成版本化 target，`cc.Provider.Identify` 看到 basename `"2.1.117"` 而非 `"claude"`，導致每個 cc hook 都以 `identify_mismatch` 在 verify 階段被拒絕。
- Spoofing 防線仍由 `internal/module/agent/verify.go:63` 的 `pidAncestorIncludesFn` 提供（要求 sender PID 在目標 pane 的 process tree 內），比 binary path 匹配更強。
- 測試：新增 unit test `TestNormalizeExecutablePath_PreservesSymlinkInvocationPath`；既有整合測試 `TestProcessInfo_ResolvesSymlinks` 改名為 `TestProcessInfo_PreservesSymlinkInvocationPath` 並翻轉 assertion。

## [1.0.0-alpha.210] - 2026-04-23

### Fix(spa): focus WYSIWYG editor from empty clicks (#600)

- `TiptapEditor` 把 Live Mode 空白區點擊視為 editor focus 請求，現在點擊整個 WYSIWYG 畫布都會把焦點導回實際的 `contenteditable` 輸入區。
- 直接點在現有 editable 內容上的行為維持不變，只補上原本落在空白區時不會 focus 的缺口。
- 新增回歸測試，覆蓋點擊 editor 空白區會觸發 focus 的行為。

## [1.0.0-alpha.209] - 2026-04-23

### Feat(spa): HSR PR-4 follow-up — #586 batch-replace + #588 reactive host runtime (#593)

- **#586 host built-in batch-replace + transactional commit**：`registerBuiltinHostSection()` + pending-buffer/drain 移除，改為 `setHostBuiltinSections(defs[])`（一次性 stage）+ `commitHostBuiltinSources()`（dispatch Phase 2 內 staged → live 原子 swap）。Wrapper identity 按 localId stable cache（HMR / drop+re-add 都不換 React component reference）；wrapper render 時讀 `liveSources` — dispatch 失敗時 `liveSources` 不變，不會出現 sidebar/route 與 body 內容 split-brain。`dispatchSettingsContributions()` 對 host built-ins 完全 idempotent（第二次 standalone call 不再清空，閉環 #586 原 bug）。Legacy adapter 仍走原 destructive drain（無 production trigger，#597 追蹤後續對稱化）。
- **#588 reactive host runtime via `SettingsContextFor<'host'>.runtime`**：discriminated union 加 `runtime: HostRuntime | undefined` 必填欄位；HostPage 用兩段式 selective subscription（`preResolveHostId` 算 tentative hostId → `useHostStore((s) => s.runtime[tentativeHostId])` 只訂閱該 host，背景 host tick 不觸發重 render）；ctx 在 `resolvedHostId !== tentativeHostId` 時保守傳 `runtime: undefined`，避免錯誤把 host A runtime 套進 host B 的 disabled(ctx)。HostSidebar 跨 host 切換時優先保留 user 當前 selectedSubPage（若 target host 仍 selectable），否則 fallback 至 target host 第一個 selectable subPage — 消除 runtime-gated 模組情境的 visible blank-and-redirect 閃爍。
- **HostPage isActive side-effect gate**：TabContent 用 `visibility:hidden` 保留非作用中 pane mounted；`lastSelection` 寫回 effect + canonical-path navigate effect 加 `isActive` gate，避免 hidden 實例污染 active 實例的 module-scoped lastSelection、避免 hidden 實例競爭 setLocation。Runtime selector 仍用真實值（不 fabricate undefined）以維持 hidden body 的 disabled(ctx) 評估正確（PR-4 「disabled body 不 mount」契約）。
- **shared `pickHostIdFallback` helper**：把 `preResolveHostId` 與 `resolveSelection.getFallbackSelection` 共用的 fallback 順序（lastSel → activeHostId → hostOrder[0]）抽成單一 source of truth + 等價性測試 14。
- **render-local lastSelection snapshot**：HostPage render 開頭 snapshot 一次傳到所有 helper，避免單 render cycle 內讀到不同 module-scoped 值（多 HostPage 實例 race 的 architectural mitigation；ownership refactor 由 #598 追蹤）。
- 4 輪 codex review 收斂（spec R1 + plan R1 + R1+R2+R3 共 11 findings 修完，R4 standard clean）；3 個延後 follow-up：#596 (HostRuntime DTO 收窄)、#597 (legacy adapter idempotent)、#598 (host-builtin reset 邊界 + lastSelection ownership)。

## [1.0.0-alpha.208] - 2026-04-22

### Revert(lights): rip arbitrator / observation / arbmode stack + trace schema extensions (#594)

- 撤回 Lights 子系統：PR-1a #559（trace schema +10 欄 + `frame_divergences` 表）、PR-1b-0 #564（trace envelope 再 +11 欄 + row class discriminator）、PR-1b-1a #575（observation types + AGENT_ARB_MODE + trace_id strategy）、PR-1b-1b #583（arbitrator goroutine + admission + 9 步 apply pipeline）共四個 merge 全部 revert，-14,577 行 / 56 files。
- 撤回原因：盤點發現 SPA 現行 status 顯示全部走 legacy `internal/agent/{cc,codex,opencode}/status.go` → `AgentEventStore` → WS → `useAgentStore` 已 production 路徑；Lights 是平行建的第二套，main 上 `SubmitObservation` 零呼叫點，對使用者完全透明。繼續推進前發現 per-agent-type status enum / source authority mapping 未定義，架構根基需要重新收斂，決定全面 revert。
- 保留項目：PR-0 #553 的 codex `HasReadiness` capability placeholder（無害、未來可能用到）；legacy hook / probe / sweep 路徑及 `internal/agent/*/status.go` 的 per-agent mapping（production 跑中）。
- 還原 anchor：safety tag `pre-lights-revert-alpha206` → `4348e7bc`（revert 前 origin/main，可一鍵 `git reset --hard` 還原）；archive tag `pr-590-lights-1b-1c-archive` → `96458913`（原 PR #590 的 13 commit 全保存在 origin）。
- PR #590 關閉並刪除遠端分支。相關 worktree / local branch 清理完成。
- 驗證：`go build ./...` 通過、23 個 Go package test 全綠、SPA lint + build 通過、`npx vitest run` 2527/2530 pass（3 fail 在 `src/lib/sync/contributors/hosts.test.ts` 為 pre-existing，與本撤回無關）。

## [1.0.0-alpha.207] - 2026-04-22

### Feat(spa): add untitled editor document flow (#591)

- 新的 in-app 編輯器文件改為先建立在獨立的 `untitled:` URI 上，名稱採 `Untitled`、`Untitled-1` 遞增，並把語言、EOL、encoding 與 untitled 狀態一起留在 editor store。
- untitled rename 改成純前端遷移 pane 與 buffer key，不再誤走後端檔案 rename；第一次存檔則共用 `RenamePopover`，未 rename 的文件先提示 `.txt` / `.md` 建議檔名，已 rename 的文件可直接寫入 `/buffer/<name>`。
- save 後會把編輯中的 untitled 文件遷移到實際的 in-app buffer 路徑，清除 untitled metadata，並同步更新 toolbar / status bar 顯示與相關測試覆蓋。

## [1.0.0-alpha.206] - 2026-04-22

### Feat(spa): HSR PR-4 — Host settings shell + built-in adapter + #541 harness (#582)

- HostPage shell 改 registry-driven：`renderContent` 改用 `listContributions('host').find(c => c.localId === selection.subPage)`，六子頁（overview / sessions / hooks / agents / uploads / logs）透過 pending buffer + dispatch-flushed adapter 註冊為 `_builtin.host.<localId>`，與 PR-2 legacy adapter 同 contract（spec §7.2 dispatch hard constraint）。
- `HostSubPage` 型別從六字面 union 鬆綁為 `string`；`isHostSubPage()` 改為 `listContributions('host').some(c => c.localId === value)` 動態驗證；`HOST_SUB_PAGES` const 保留作 i18n key 參考 + 註冊順序註解（JSDoc 標注非型別來源）。
- Section subtree key：`HostPage.renderContent` 使用 `key={\`${hostId}:${c.id}\`}` 強制跨 host 切換 remount（對稱 PR-3 `${workspaceId}:${c.id}` pattern），防止跨 host 狀態汙染。
- Disabled contribution UX：HostSidebar 不 filter disabled contributions，渲染 disabled row + `disabledReasonKey` title + `data-disabled-ctx="true"` + click no-op；`renderContent` 對 disabled ctx return null（對齊 PR-2 F7 / PR-3 F2）。
- `isSelectable(c, ctx)` + `pickSelectableSubPage(hostId, requested)` helper：三處 fallback（getFallbackSelection / sidebarSubPage / resolveSelection）統一走 selectable 檢查；`lastSelection` read-time clamp 到 live registry；跨 host 切換由 HostPage 透過 helper 重算目標 subPage；disabled/missing URL 自癒 emit `canonicalPath` redirect（R1+R2 六方 codex review 收斂後的統一修法）。
- #541 cross-store rehydrate harness：9 invariant tests 覆蓋 `host-lifecycle.ts` 全部 6 個 `!hostWasRecreated` gate（含 tab restore）；merge-mode `setState` 顯式 reset 所有 mutable store fields（useTabStore.visitHistory / useAgentStore.oscTitles / ccStatus），避免 cross-test leak；PR-1 cascade + undo guards 正確，`host-lifecycle.ts` source 0 改動。
- Commit 1/2/3/4 per-commit spec + quality review 各修 2–4 nits 後 approved；整體 R1 標準 + R2 三視角 parallel（攻擊 / 防守 / 體質）+ R3 verify 共三輪 codex review 收斂，no critical / no P1。
- Follow-ups：#586（dispatch-flushed 第二次 standalone call 會清空 host built-ins，theoretical-risk）/ #587（parseRoute / isHostSubPage 依賴 mutable registry state — architecture discussion）/ #588（disabled(ctx) 動態變化時 body 不 reactive 卸載）均 tracked for PR-5 前處理。#581（disabled built-in self-heal）已由本 PR commit `ca000a81` 修復關閉。

## [1.0.0-alpha.205] - 2026-04-22

### Feat(lights): PR-1b-1b — Arbitrator goroutine + admission + apply pipeline (#583)

- 新 `internal/module/agent/arbitrator/` 套件：單一 goroutine `Run(ctx)` 消費 `Arbitrator.in` (cap 1024)；9 步 apply pipeline（generation gate / watcher / idempotency / pending routing / source priority / monotone lifecycle / single-primary invariant / mode branch / trace emit），含 hook-storm 10ms/50 obs pre-gate（§3.4.2）與 authoritative fail-closed pre-gate（reject 前不修改任何 frameState）。
- `SessionStart` helper：一次完成 force-end old-gen actors（reason=session_restart）+ clear pending + per-obs `SessionRestartCleared` trace + `TraceIDMinter.Mint(sid, newGen)` + `PruneSessionBefore` + `arbmode.Manager.ApplyAtSessionStart()` + synthetic boundary trace（使用**新 mint** 的 trace_id）。
- `TraceWriter` 採 bounded **priority ring buffer** cap 4096（非 FIFO channel）：滿載時 O(n) 淘汰既有最低 DropPriority 項；每 100ms flush 一次走 `TraceStore.AppendSteps` batch。flush 成功才 pop 已送批次，失敗保留 buf 等下輪 retry，不再靜默遺失。
- 新 `TraceStore.AppendSteps([]TraceStep)` API：單 tx 多 chain；chain INSERT OR IGNORE（不存在則建最小 placeholder）+ step INSERT OR IGNORE（idempotent on retry）+ 每個變動 chain 回填 step_count / latest_step_kind / latest_decision / latest_step_reason（COALESCE 保留既有非空值）。
- `frameState` 單 owner in-memory reducer（無 mutex）：per-session generation + actor lifecycle + watcher tokens + IsPrimary；passthrough 與 authoritative mode 都更新，只有 FramesStore / frame_divergences / WS broadcast 按 mode gate（1b-1b 全不寫外部；1b-1c 寫 divergence；Phase 2 寫 frame）。
- `Module.SubmitObservation` admission helper：committed 100ms 有界阻塞 + timeout drop；proposed 非阻塞 drop；滿載 metric `lights_arb_in_dropped{priority}`。
- Shutdown drain：`Run(ctx).Done()` 後繼續 consume inCh 內已排隊 obs 到空才退出，避免關機邊界遺失 committed 事件。
- reconcile tick 5s：flushPendingDue + stale actor detect（30s LastActivity，僅發 trace 不改 status）+ idempotency cache 5 分鐘 TTL prune。stale trace 走 `TraceIDLookup.Get` 取 session/gen trace_id，miss 則不 emit 避免 poison batch。
- #578 修復：`TraceIDRegistry` 拆 `TraceIDMinter` / `TraceIDLookup` 兩介面；具體 struct unexported；constructor 回兩個 interface 值。Arbitrator 收 Minter，hook path（1b-1c）收 Lookup。
- #579 修復：`arbmode.Manager` 改用 `atomic.Pointer[modeTarget]` published snapshot 線性化 `OnConfigChange` ↔ `ApplyAtSessionStart`；最後完成的 publish 保證在下一 SessionStart 被 apply 讀到。
- Plan review 處理：3 P0 / 3 P1 / 2 P2 / 1 P3 全修（atomic published snapshot / priority ring buffer + AppendSteps / frameState both-modes update / SessionStart helper / 1b-1b scope 內移除 retryCh / hook-storm 納入 / idempotency canonical bytes + order-invariant / authoritative fail-closed / interface compile assertion 替代）。
- R1 + R2 三視角 review 處理：C1-C6, C8-C10 全修（boundary trace_id 新 mint / reconcile trace_id lookup / shutdown drain / flush retain-on-error / authoritative pre-gate / committed phase 保留 / stable SpanID UUID / AppendSteps chain summary 回填 / idem prune 接 reconcile tick）；C7 production pending entry trigger 延後追蹤於 #584。

## [1.0.0-alpha.204] - 2026-04-22

### Feat(lights): PR-1b-1a — Observation types + AGENT_ARB_MODE + trace_id strategy (#575)

- 新 `internal/module/agent/observation/` 套件：`Observation` 值型別（11 欄：ActorKey / DecisionPorts / Evidence / TraceID / ObservedAt / SessionID / Generation / ActorID / Source / Kind / ArbMode），`Builder` 採 single-use + deep-copy（DecisionPorts / Evidence / nested slices），第二次 `Build()` panic；`EvidenceRef.Value: any` 為 shallow copy（caller 不得在 Build 後改 Value 內容）。
- 新 `internal/module/agent/arbmode/` 套件：`Manager.Snapshot()` 一次性回傳避免 torn read，不暴露分欄 getter；`env AGENT_ARB_MODE > config.AgentConfig.ArbMode > default("passthrough")`，env 鎖 hot reload；`PUT /api/config` 的空字串 `arb_mode=""` canonicalize 成 `"passthrough"` 再存（R2 共識修正）。
- `TraceIDRegistry` + per-session watermark：`PruneSessionBefore` 推進 watermark；低於 watermark 的 `Mint` 回 `""` + log error，caller 必須 check empty（R2 P2 修正）。
- `ActorKey` 非空要求完整 triple（SessionID + Generation + ActorID 全非空）；zero ActorKey 允許（reconcile 無 proposal 情境，R2 P2 修正）。
- `internal/module/agent/module.go` Init：arbmode build 移到 session check 前，session provider 缺席時 `/api/agent/arbitrator/mode` + `OnConfigChange` 仍可運作（R1 P3 修正）。
- Test：47 observation + 13 arbmode + 7 config/handler；全 repo `-race` clean。
- Review：plan review（3 P0 / 4 P1 / 3 P2 / 1 P3 全修）+ 實作 9 輪 task-level review + R1 標準 + R2 三視角 parallel（4 攻擊 / 3 防守 / 2 體質），共識項全收斂；剩餘延後為 #578（Minter/Lookup interface split）+ #579（arbmode Apply epoch race），於 PR-1b-1b 處理。

## [1.0.0-alpha.203] - 2026-04-22

### Feat(spa): HSR PR-3 — Workspace settings shell + reserved cleanup (#573)

- `WorkspaceSettingsPage` 在 `ModuleConfigSection` 下方新增 registry-driven 區塊：讀 `listContributions('workspace')` 依 `order` 升冪渲染，每個 contribution 收到 `ctx: { scope: 'workspace', workspaceId }`（spec §5.3 rule 4 — ctx 僅由 shell 產生）。
- `register-modules.tsx` 拔掉 reserved `workspace` section（`order: 10`，無 component 的 coming_soon 項）；`SettingsSidebar` 同步精簡：移除 `listReservedItems()` 呼叫、`reservedStart` 分隔線、coming_soon 灰字樣式（reserved cleanup 後皆 dead code），PR-2 F7 的 `disabled(ctx)` 處理保留。
- `settings-section-registry.ts` 拔 `pendingReservedItems` Map、`listReservedItems` export、與 `registerSettingsSection` 的 reserved 分支：舊 API 呼叫方若仍傳 `component: undefined`，改為 `console.warn + return`（非 throw），避免 stale caller / HMR 舊 session 炸 bootstrap（R2 attack F5 修正）。
- **新 finding 收斂**（R1+R2 四路 review）：F1 移除 `WorkspaceSettingsPage` 的 `useMemo([ctx])` 過度快取，`disabled(ctx)` 改為每次 render 重算，store/flag 變化即時反映；F2 workspace shell 對 disabled contribution 改為「顯示 disabled row + `disabledReasonKey`」，與 PR-2 purdex shell 契約一致；F4 section subtree 加 `key={${workspaceId}:${c.id}}` 強制 workspace 切換時 remount，防止 contribution 內 `useState(ctx.workspaceId)` / `useEffect(..., [])` 跨 workspace 狀態汙染。R3 confirmation review clean。
- F3：原規劃拔掉的 `module-config` 全域節 restored — `globalConfig` API 仍 live 但 UI 入口被移除形成 silent dead-end；改為保留 UI，等 PR-5 才真正 deprecate（tracked in #574）。
- 新 `WorkspaceSettingsPage.registry.test.tsx` 7 cases：baseline / render / ctx / order / disabled / workspaceId freshness / cross-scope isolation；`register-modules.test.ts` / `settings-section-registry.test.ts` / `SettingsSidebar.test.tsx` / `SettingsPage.test.tsx` / `settings-contribution-smoke.test.tsx` 同步更新。i18n 移除 `settings.section.workspace` key（`settings.section.modules` 隨 F3 還原保留）。
- Follow-up issues：#574（globalConfig deprecation after PR-5，Backlog）；#538 workspace 層 render-level smoke milestone 可關閉。
- 本 PR 僅含 workspace 層 shell 遷移；`HostPage` 改 registry-driven 與 Editor module 首個用例由 PR-4/5 接手。

### Fix(spa): restore build baseline — MonacoWrapper test missing isActive prop (#576)

- `MonacoWrapper.test.tsx:133` 的 `rerender` 呼叫缺 `isActive` prop（第一次 render 有傳，rerender 漏補），導致 `tsc -b` 自 #570 後在 main red。補上 `isActive={true}` 恢復 `pnpm run build` 全綠。
- Vitest 對型別較寬鬆沒抓到；問題由 HSR PR-3 review 階段發現並獨立以 baseline PR 修復（不夾帶 feature 變更）。

## [1.0.0-alpha.202] - 2026-04-22

### Feat(spa): HSR PR-2 — Purdex settings shell + legacy adapter (dispatch-flushed) (#558)

- `SettingsPage` 的 `GlobalSettingsPage` 與 `SettingsSidebar` 改為 registry-driven，讀 `listContributions('purdex')` + `listReservedItems()`，並向 active component 注入 `ctx: { scope: 'purdex' }`（spec §5.3 rule 4 — ctx 由 shell 產生）。
- `settings-section-registry.ts` 由直接寫 registry 改為 **pending-buffer adapter**：active 項 push 到 `pendingLegacyContributions`（upsert by id），reserved 項寫入 `pendingReservedItems: Map<string, SettingsSectionDef>`（upsert）；`dispatchSettingsContributions()` Phase 2 在 `clearContributions()` 後以 `drainLegacyContributionQueue()` 統一 drain + register，滿足 spec §7.2 dispatch-flushed 硬約束（避免 legacy 被 clearContributions 整批清掉的 regression）。
- 同 commit 完成 #539 收斂：`registerSettingsContribution` / `clearContributions` / `assertValidSettingsContribution` 標為 `@internal`，並以 ESLint `no-restricted-imports` 限定僅 `dispatch-settings-contributions.ts` / `settings-section-registry.ts` / test 可 import 寫入 API；HMR dispose 走單一 `resetSettingsContributionsForHmr()` helper 同清 registry + pending buffer。
- React component identity 透過 `wrapLegacyComponent` + `WeakMap<WrappedComp, OrigComp>` 逆查保留（`toLegacyShape` unwrap 後 `component === 原 passed-in Comp`）。
- 3 輪 codex review（R1 標準 + R2 三路對抗 + R3 確認）全部收斂：R1/R2 HIGH findings（shell 用 localId 當全域 route key / reserved↔active 雙 Map 無互斥清理 / dispatch destructive drain / @internal 無 runtime 護欄）於 F1-F4 commit 修復；R3 P2 findings（localId regex 對不齊 parseRoute / sidebar 未評估 `disabled(ctx)`）於 F6/F7 commit 修復。
- F1：dispatch-time 新增 per-scope localId 唯一性 assertion（同 scope 跨 module 同 localId → throw）；保留 URL `/settings/<localId>` 向後相容。
- F2：reserved ↔ active 互斥清理（每次 `registerSettingsSection` 寫入先刪對方 buffer 同 id 項）；workspace reserved → active 升級（PR-3 動作）不再留殭屍列。
- F3：`peekLegacyContributionQueue()` snapshot + validate-before-commit；dispatch 驗證失敗時 queue 保留供 retry。
- F6：`SETTINGS_LOCAL_ID_RE` 共用 const（`/^[a-z0-9-]{1,32}$/`），registry validator 與 `route-utils.ts` 同源；既有 built-in 10 個 localId 全部 conform。
- F7：單一 `isSelectable()` predicate 貫穿 sidebar / self-heal / initial state picker / handleSelectSection guard / ActiveComponent gate；`disabled(ctx)` 評估 + `disabledReasonKey` i18n tooltip；deep-link 到 disabled section → URL replace 到第一個 selectable。
- 延後 follow-up issue：#563（registry.ts SRP refactor，Backlog；等 PR-5 後 adapter scope 穩定再處理）；#538/#540/#541 持續由後續 PR-3/4/5 解。
- 本 PR 僅含 Purdex 層 shell 遷移；`WorkspaceSettingsPage` / `HostPage` 改 registry-driven 與 Editor module 首個用例由 PR-3/4/5 接手。

## [1.0.0-alpha.201] - 2026-04-22

### Fix(spa): restore lint and build baseline (#556)

- 修正 `main` 上殘留的 SPA lint 與 typecheck 基線失敗，讓 `pnpm --prefix spa run lint` 與 `pnpm --prefix spa run build` 再次可通過。
- 將退化集中收斂到現行型別與 runtime 契約，包含 preview pane、fs backend、sync provider、drag/statusline helper 與多批測試 fixture 對齊。

## [1.0.0-alpha.200] - 2026-04-22

### Test: realign stale fs and editor suites with current runtime (#544)

- 將已失效的 `internal/module/files` list handler 測試遷回現行 `internal/module/fs`，保留目錄排序、hidden file 過濾、empty dir、broken symlink 等仍有意義的 coverage，讓 `make test` 重新可完整跑完。
- `EditorPane` 測試改寫為對齊現行 `FsBackend` editor runtime，覆蓋初始載入、儲存成功、儲存失敗、unmount cleanup 與 active reload，移除對舊 `docId` / `bindingStatus` 模型的錯誤依賴。

## [1.0.0-alpha.199] - 2026-04-22

### Feat(spa): HSR PR-1 — scope-parameterized settings contribution registry + 3-layer stores (#542)

- 建立 Host-scoped Module Settings Registry（HSR）核心：`ModuleDefinition.settings` 以 scope-parameterized discriminated union 宣告，module 可在 `purdex` / `host` / `workspace` 三種 scope 各自掛自己的設定；`AnySettingsContributionDeclaration` distributive union 在 registry / dispatcher / ModuleDefinition 三處 boundary 保留 scope↔ctx 關聯。
- 新增三層 settings store（`useGlobalSettingsStore` / `useHostSettingsStore` / `useWorkspaceSettingsStore`）：persist + BroadcastChannel sync + rehydrate heal 皆就位；heal 拒絕 `__proto__` / `constructor` / `prototype` keys 防 prototype pollution。
- `dispatch-settings-contributions.ts` 以 atomic two-phase（validate → commit）註冊，Invariant I1 保證同 module 同 scope 不與舊 `globalConfig` / `workspaceConfig` 雙軌並存。
- Cascade integration：host 刪除 / workspace 移除會同步清掉對應 scope 的 settings；host 刪除 undo 對 last-host veto、same-id 重建 race、tab restore 綁到新 host 三類邊界 case 都有保護。
- Tear-off / merge 搬移 window 時透過 `removeWorkspace(wsId, { keepSettings: true })` 保留 workspace 設定不被連坐清除。
- 3 輪 codex 4-way review 全部收斂（R3 標準 no findings）；遺留 #538–#541 追蹤 shell migration 後才處理的 MED 級項目（smoke render / internal API 收斂 / store ref 外洩 / cross-store rehydrate order）。
- 本 PR 僅含 registry + 型別 + 三層 store + cascade 整合；SettingsPage / HostPage / WorkspaceSettingsPage 遷 registry-driven 與 Editor module 首個用例（tilde Layer 1/2）由後續 PR-2/3/4 接手。

## [1.0.0-alpha.198] - 2026-04-21

### Feat(agent): add opencode hook integration (#534)

- daemon 新增 `opencode` provider、`pdx setup --agent opencode` 與 `/api/hooks/opencode/*` host-global installer，讓 OpenCode hooks 可比照 Claude Code / Codex 由 Host Hooks 介面統一管理。
- OpenCode plugin 會把 `session`、`permission`、`task` lifecycle 映射成現有 agent hook 事件，並將 `task` tool 對齊為 Claude-compatible `SubagentStart` / `SubagentStop`，補上跨 session pairing、duplicate start 忽略、malformed event 不廣播等保護。
- agent module 補強 OpenCode error guard 與 subagent cleanup：`SessionStart` 會清殘留 subagents、`Stop` 不會誤清 error、`SessionEnd` 在 error 狀態下仍可正常 cleanup。
- Settings 的 Host Hooks 現在會顯示 `OpenCode Hooks` 卡片，並補上 focused Go / SPA tests 覆蓋 provider、installer、subagent contract、detect API 與 page-level hooks UI 接線。

## [1.0.0-alpha.197] - 2026-04-21

### Feat(spa): support ~/... tilde paths in terminal file links (#530)

- Terminal 點 `~/foo.ts` 會以 pane shell `$HOME` 展開（daemon `$HOME` 為 fallback），修正 `~` 被絕對路徑正則 lookbehind 吃掉、點擊後開到 `/foo.ts` 錯檔的 regression。
- Daemon 新增 `GET /api/sessions/{code}/home` 端點：Linux 讀 `/proc/<pid>/environ`、Darwin 走 `ps -E`；pane shell 讀不到時 fallback daemon `HOME`，兩者皆空才回 500。
- Settings「Link 偵測」新增第四個 toggle（預設開）供停用 tilde 偵測；pane home 取得失敗改 fallthrough 用原 raw path 讓 Editor 開空白 buffer，而非 warn+return。
- ESLint config 統一加 `argsIgnorePattern: '^_'`，順手清掉 18 個既有檔散落的 `// eslint-disable-next-line @typescript-eslint/no-unused-vars` 註解。
- 4 輪 Codex review（標準 + 攻擊 / 防守 / 體質）完成；workspace / host 手動 home 不在本 PR scope，遺留由後續追蹤：#531（`~/../foo` 逃出 `$HOME`，刻意保留）、#532（Darwin `ps -E` 遇 env 含空白切壞）、#533（lint config 不該混 feature PR 的 review policy 討論）。

## [1.0.0-alpha.196] - 2026-04-21

### Fix(spa): align sync history settings UI (#528)

- 將 Sync History 頁面從早期 wireframe 風格的硬切 split-pane 收斂回現行 Settings 設計語言，補上正常的 section header、surface card 和一致的狀態樣式。
- History tabs、snapshot rows、detail panel 與 restore dialog 全面改用既有 border / surface / hover token，避免 Sync 子頁在視覺上脫離整體設定頁。
- loading、empty、error 與 warning 狀態一併改成與現行 Settings 相同的面板式呈現，不變動既有 restore 行為。

## [1.0.0-alpha.195] - 2026-04-20

### Feat(agent): add tmux agent hook trace monitor (#526)

- daemon 新增 hook-only trace rail，將 trigger / verify / frame / projection / emit 傳遞鏈持久化到 SQLite，並補上 migration hardening、retention 與 typed monitor APIs。
- Settings 新增 dev-only `Tmux Agent Monitor` 區塊，可依 host / session / pane 檢視 chain list、階層 step tree、selected-step JSON inspector 與 projection summary。
- 前端 monitor 針對 active host、pane-scoped projection、stale request 覆寫與失敗後殘留舊資料等除錯誤導情境補上防護與測試。
- 後續結構性收斂另開 #523、#524、#525 追蹤，不阻擋本次功能出貨。

## [1.0.0-alpha.194] - 2026-04-20

### Fix(agent): harden statusline self-test handshake across version skew (#520)

- Statusline self-test 新增 capability-based `init` / `ready-v1` handshake：新前後端組合會先完成 bus subscriber 就緒，再進入真正的 `proxy -> daemon -> WS -> SPA` 驗證路徑。
- `/api/agent/status` 的 test nonce 仍保留真實 broadcast path，避免 self-test 因為走旁路而失去對 production WS shape / routing 的覆蓋。
- 舊版前端或舊版 daemon 仍可走 legacy fallback，不會因版本錯位把 self-test 永久卡住；SPA 端也把 test bus 命中收斂到當前 `hostId`，避免跨 host 同 nonce 誤判成功。
- 另開 #521 追蹤 `ready-v1` timeout policy 的後續設計，不阻擋本次修正出貨。

## [1.0.0-alpha.193] - 2026-04-20

### Fix(spa): preserve host deep links across tab switches (#518)

- Host 管理頁現在支援 `/hosts/:hostId/:subPage` deep link，切換 tab 後回到 hosts 頁時會保留上一個 canonical host section，而不是跳回第一個 section。
- `HostPage` 改成 URL-driven selection；bare `/hosts`、invalid deep link、host deletion fallback 都會收斂到可預期的 canonical path，且在沒有 host 可選時會回到 bare `/hosts`。
- `useRouteSync()` 對 valid / invalid host deep links 都只開 singleton hosts tab，不會把 host selection 滲進 tab model。
- Host route 會對 `hostId` 做 URL encode/decode，避免含保留字元的 host id 破壞 deep link。
- 補齊 parser、`HostPage`、mounted route-sync 測試，涵蓋 remount、keep-alive、invalid route、no-host fallback 與 canonicalization。

## [1.0.0-alpha.192] - 2026-04-20

### Feat(sync): P1 Sync History + Restore (#488)

- **SnapshotStore on IndexedDB**：`lib/storage/idb.ts` 版本感知 cache（rejected / terminated / blocking 時重連），`features/sync/snapshot-store.ts` 提供 `init / create / get / list / delete / compact / demoteSessionPristine / rotateSessionPristine / clear`，`list` 僅回 metadata，tiered compaction 每層保留最新（monthly bucket cap 12）。
- **Restore contract**：`useSyncStore.restoreFromSnapshot(snapshot, source, options?)` 引入 `PreOpFailedError / SnapshotCoverageError / RestoreFailedError` 與 `RestoreOptions (skipPreOp, allowMissingContributors)`；snapshot 需涵蓋完整 contributor registry，進入 deserialize 前清 `pendingConflicts` 避免 partial-restore 後 stale conflict 覆寫 restored 資料；`restoreTokenRef` 隔離 in-flight restore 不跨 row 污染。
- **Safety net**：`createPreOperationSnapshot` 與 `ensureSessionPristine` 序列化完整 contributor registry（不是 `enabledModules`）；pristine rotation 走單一 IDB readwrite transaction，跨 tab 也能收斂到恰好一份 pristine；quota retry：`createSnapshot` put 失敗 → compact → retry 一次。
- **Auth boundary**：`contributors/hosts.ts` full-replace 時僅在 endpoint `(ip, port)` 完全一致才保留 token，否則強制 re-auth，避免惡意 bundle 將 bearer token 重指向攻擊者 daemon。
- **UI**：雙層路由 `/settings/<section>/<subsection>` + `SettingsRouteContext`；`SnapshotHistoryPage` + `HistoryTabs` + `HistoryList` + `SnapshotDetail` + `SnapshotRestoreDialog`（confirm / preOpFailed / coverageWarning 三模態，override 跨 retry 累積）；dialog Cancel 在 restore in-flight 時 disabled，阻斷並行 restore。
- **Integration**：`manual-provider.ts` 匯入前建立 pre-import snapshot（失敗不阻斷匯入）；`App.tsx` bootstrap 呼叫 `ensureSessionPristine`（rejection 已 catch）；i18n 新增 45+2 key（en + zh-TW）。
- **Scope**：daemon `/api/sync/snapshots` + Remote tab 接線留給 PR B；Remote tab 目前對 daemon user 顯示占位。
- **Known issues（後續 PR 處理）**：[#492](https://github.com/wake/purdex/issues/492) · [#493](https://github.com/wake/purdex/issues/493) · [#494](https://github.com/wake/purdex/issues/494) · [#495](https://github.com/wake/purdex/issues/495) · [#503](https://github.com/wake/purdex/issues/503) · [#510](https://github.com/wake/purdex/issues/510) · [#511](https://github.com/wake/purdex/issues/511) · [#512](https://github.com/wake/purdex/issues/512) · [#514](https://github.com/wake/purdex/issues/514)。

## [1.0.0-alpha.191] - 2026-04-20

### Feat(agent): identity and liveness convergence phases 1-7 (#489)

- **Hook provenance v2**: `pdx hook` payload 現在攜帶 `tmux_pane_id`、`sender_pid`、`sender_start_time`、`sender_uncertain`，daemon 端只接受新 schema，並驗證 hook 送進來的 pane / PID / start time provenance。
- **Frame-authoritative state**: 新增 `agent_frames` store，session 狀態改由 frame projection / replay / sweep 驅動；accepted v2 hooks 不再 dual-write `agent_events`，startup 先 sweep，再 replay，避免 ghost session resurrection。
- **Process identification**: provider 改吃 `ProcessInfo`，`probe.IsAliveFor()` 走 PID tree + `Identify(ProcessInfo)`，不再依賴 command-name / `pane_current_command` 舊路徑；legacy liveness handlers 已移除。
- **Activity convergence**: activity watcher 收斂為 `waiting` / `running` / `idle` 三規則，加入 shell prompt 檢測與 `capture-pane -e` 路徑；`shell_prompt + dead PID` 現在會正確觸發 sweep / clear。
- **Docs and follow-up**: convergence spec / plan 已同步到最終實作；剩餘 replay / snapshot legacy fallback duplication 另開 #505 追蹤。

### Chore(spa): flag-gated debug logs for statusline self-test chain (#506)

- `spa/src/lib/statusline-test-debug.ts` — 以 `localStorage.getItem('pdx:debug:statusline-test') === '1'` gate 的 `debugStatuslineTest` helper，no-op when flag absent。
- Instrumented points（只對 `__pdx_test_*` sessions fire）：`ws.entry`（`useMultiHostEventWs`）、`dispatch.entry` / `.parsed` / `.emit-bus` / `.reject.*`（`agent-ws-dispatch`）、`bus.subscribe` / `.emit` / `.unsubscribe`（`statusline-test-bus`）、`hook.stage1-passed` / `.bus-callback` / `.stage5-check` / `.early-hit-check` / `.sse-outcome` / `.grace-start` / `.grace-result`（`useStatuslineTest`）。
- 用途：本地 triage stage 4 `"WS event not received"` 真因時，整條 daemon → WS → dispatcher → bus → hook 的 timeline 可視化，pinpoint 哪一層斷掉。

## [1.0.0-alpha.190] - 2026-04-20

### Fix(spa): statusline test — stage 4 grace window for late WS events (#490)

- **Root cause**：SSE `done` 比 WS `agent.status` 先到 SPA，舊版 `unsubBus` 立即執行 → 後到的 WS 沒人接，stages 4/5 卡 `running` 轉圈到天荒地老。舊 8s overall timeout 救不到（`work` 已 resolve 走 `done` 路徑）。
- **Fix**：新增 `STAGE4_GRACE_MS = 2000` 的 post-SSE grace window，grace 超時 → `unsubBus` → `markFailThenSkipRest(4, "WS event not received within 2000ms after stream completed")`，stage 5 連帶 skipped。既有 `StatuslineTestPanel` log 按鈕自動顯示 error。
- **State machine 清理**：`stage4Awaiting` 與 `stage4Resolver` 合併為 `Stage4State` union（`pending` / `armed` / `fired` / `cancelled`），消除雙 flag 隱性依賴；`markFailThenSkipRest` 不再能蓋掉已 fired 狀態。
- **Early-hit 修正**：命中後立即 `unsubBus`，避免後到 WS 觸發冗餘 subscriber。
- **Doc**：`OVERALL_TIMEOUT_MS` comment 註明 grace 不在 overall 內，整體最壞 8+2=10s。
- **Tests**：新增 `SSE done without WS event → stage 4 fails after grace period`（fake timers）；既有 happy-path / stage-1 failure / overall-timeout 全綠（5 passes）。2158 個 SPA 測試全綠。
- **Follow-ups**：開 #496（StrictMode ref reset）、#497（error 字串 i18n）、#498（dispatcher↔bus contract test）、#499（SSE 亂序防禦）、#500（state machine 抽離）追蹤。

### Feat(spa): Codex icon variant setting (#491)

- **Settings → Terminal → Codex icon**: OpenAI / Codex monochrome button pair with live preview, mirroring the existing Claude Code bot/star row. Active button uses `aria-pressed`; when tab indicator is dot-only, a hidden-hint paragraph matches the CC behaviour.
- **Icon options**: `openai` (Phosphor `OpenAiLogo`, default — unchanged behaviour) and `codex` (monochrome `@lobehub/icons-static-svg/codex.svg`, inherits tab theme via `currentColor`).
- **A11y**: `aria-hidden="true"` on the agent-icons wrappers so lobehub SVG `<title>` elements no longer pollute button accessible names.
- **Tests**: +3 in `agent-icons.test.tsx` (codex variant coverage), +1 in `TerminalSection.test.tsx` (codex button click), +locale-completeness keeps en/zh-TW in sync. Full suite 2161/2161.

### Refactor(spa): move tab/icon preferences to useUISettingsStore

Driven by codex review findings on PR #491. Previously these preferences
lived in `useAgentStore` alongside runtime state, so any schema bump would
silently reset user settings — and the new `codexIconVariant` would not
have roamed through the preferences sync contributor.

- **Moved**: `tabIndicatorStyle`, `ccIconVariant`, `codexIconVariant`, `showOscTitle` (types, state, setters) from `useAgentStore` → `useUISettingsStore`.
- **Migration**: `useUISettingsStore` v1 → v2 with a `migrate` that imports the old `purdex-agent` payload's UI pref keys when present, so upgraders keep their settings.
- **Sync**: `preferences` sync contributor's `DATA_FIELDS` now includes the four moved keys — they roam across devices and survive import/export.
- **Store**: `useAgentStore` no longer persists anything and drops the `persist` middleware + `syncManager.register` entirely. It's now pure runtime state.

## [1.0.0-alpha.189] - 2026-04-19

### Fix(agent): probe wrapped descendants with bounded cache (#484)

- **Wrapped agent liveness**: `probe.IsAliveFor()` 保留前景 command 與 direct-child 快路徑，之後補上 recursive descendant 偵測，修正 `shell -> node -> codex` 這類包裝程序被誤判成 dead 的問題。
- **Bounded cache**: descendant 結果快取現在綁定 tmux target、pane PID、direct-child snapshot，並加上短 TTL；同 target 換 pane、grandchild 變動或 cache 過期都會重查，避免把 stale alive/dead 判斷釘住。
- **Probe-local capability**: recursive descendant 查詢改為 probe 內部的 optional capability，不再把 `PaneDescendantCommands` 掛進全域 `tmux.Executor` 介面。
- **Tests**: 新增 wrapped descendant、TTL 內命中、TTL 後 grandchild 變動、pane PID 變更等 probe regression tests；`go test ./internal/agent/probe ./internal/tmux ./internal/module/agent` 通過。

## [1.0.0-alpha.188] - 2026-04-19

### Feat(spa): Extensions sub-row status icon (#480, PR #482)

- 13px Phosphor status icon left of the Status integration label in `AgentExtensionRow`:
  - `pdx` / `wrapped` → `CheckCircle` (green-400)
  - `unmanaged` → `WarningCircle` (yellow-400)
  - `none` → omitted
- Icons marked `aria-hidden="true"`; the existing text badge already conveys state to screen readers.
- 4 new TDD tests covering all 4 modes + icon type + a11y attribute.

### Feat: Statusline pipeline self-test panel (#481, PR #483)

- **One-click loopback self-test** under Status integration validating the full `proxy → daemon → WS → SPA store → UI` chain without spawning `claude -p`.
- **Daemon** (Go)
  - `POST /api/agent/cc/statusline/test` SSE endpoint streams per-stage pass/fail for stages 1-3 (proxy spawned / daemon POST received / daemon WS broadcast).
  - Test nonce `__pdx_test_<8-hex>` rides through the real `pdx statusline-proxy` subprocess via new `PDX_STATUSLINE_TEST_SESSION` env override.
  - `handleAgentStatus` detects nonce prefix → signals per-nonce observer channel → broadcasts WS keyed by nonce → never persists to `statusSnapshots` or hits `resolveSessionCode`. Phantom-tmux-session-free.
  - Targeted `agent.status.cleared` (scoped by session) for cleanup — distinct from the existing unscoped (empty session) wipe-all semantics used for statusline uninstall.
  - `testSpawnProxy` seam for unit tests; `defaultSpawnTestProxy` uses `os.Executable()` + `filepath.EvalSymlinks` + 2s context timeout.
- **SPA** (React / TypeScript)
  - `useStatuslineTest(hostId)` hook: POSTs, parses SSE for stages 1-3, subscribes to `statuslineTestBus` for stage 4 (WS event received), introspects `useAgentStore.ccStatus` for stage 5 (store populated). 8s overall client timeout with `reader.cancel()` on timeout and `runningRef` re-entrancy guard.
  - `StatuslineTestPanel` renders 5-node status with Phosphor icons (`CheckCircle` / `XCircle` / `Minus` / `CircleNotch`), Run-again button, expandable failure log.
  - `AgentExtensionRow` embeds the panel when `state.mode` is `pdx` / `wrapped`; panel's internal `autoRanRef` + mount/unmount cycle auto-runs exactly once per install.
  - `agent-ws-dispatch`: nonce-aware routing — `agent.status` with `__pdx_test_` session also emits to the test bus; `agent.status.cleared` with non-empty session routes to scoped `clearSession(hostId, nonce)`.
  - 14 new `hosts.extensions.test.*` i18n keys (en + zh-TW).
- **Review fixes**: timeout 5s → 8s (server worst case is 6s), dangling SSE reader `reader.cancel()`, re-entrancy guard on `run()`, `EvalSymlinks` for symlinked binary installs.

### Follow-up issues

- #480 ✅ addressed by PR #482
- #481 ✅ addressed by PR #483
- Deferred observations (low priority): auto-run on panel remount (by design — fresh install remounts), discarded `cmd.Stderr` in test spawn (stage-1 failure shows only exit code), synthetic `ccStatus[host:nonce]` lingers if SSE connection drops before cleanup (harmless, bounded by page lifetime)

## [1.0.0-alpha.187] - 2026-04-19

### Fix(agent): Codex hook schema compatibility + hook support version warning (#479)

- **Codex hooks schema**：`~/.codex/hooks.json` 改寫為 Codex 0.121+ 需要的 matcher-group schema（`{"hooks": [...]}`），避免 legacy direct-entry 看似已安裝但實際完全不觸發。
- **Legacy detection scoped to pdx**：`CheckHooks` 偵測 legacy direct-entry 時，只對 pdx 自己的 command 觸發「reinstall required」；第三方 legacy hook 與 pdx matcher-group 並存時不再誤報。
- **Agent version cache**：`DetectHookAgentVersion` 加 60s per-binary TTL cache。原本每次 `CheckHooks` 都同步 spawn `claude --version` / `codex --version`（含 5s timeout），現在最差情況降為每分鐘一次。
- **Hook version support fields**：hook status API 新增 `agentVersion` / `supportedVersion` / `exceedsSupport`，目前記錄已驗證的 Claude Code（2.1.114）/ Codex（0.121.0）hook 支援上限。
- **Hosts UI**：Hook 卡片顯示 Agent 版本與 Hook 支援上限，agent 版本高於支援版本時顯示黃色警示（`<WarningCircle>` + `hosts.hook_version_warning` key）。
- **Tests**：新增 version parser/compare 測試、version cache TTL 測試、Codex legacy 格式 regression test（含 pdx 與第三方 legacy 並存情境）、HookModuleCard 版本資訊顯示測試。

## [1.0.0-alpha.186] - 2026-04-19

### Feat: Statusline installer UI (PR-1c, #473)

- SPA installer UI for CC statusline extension under Host → Agents. CC card gains Extensions region with Install / Remove affordance; Codex card has no Extensions section.
- `useStatuslineInstall` hook wraps `GET /api/agent/cc/statusline/status` + `POST /api/agent/cc/statusline/setup`; exposes `{ state, phase, error, install, remove, refresh }` with per-effect cancelled closure for hostId-change safety.
- `StatuslineConflictDialog` prompts user when CC has an unmanaged statusLine — Wrap route POSTs `{ action: install, mode: wrap, inner: rawCommand }`. Full a11y: `role=dialog` + `aria-modal` + `aria-labelledby`; Escape / backdrop / stop-propagation dismiss.
- `AgentExtensionRow` gates button choice by `state.mode` (not `installed`): `none` / `unmanaged` → Install, `pdx` / `wrapped` → Remove. Remove confirmation via `window.confirm`. Buttons disabled when host offline (matches `HookModuleCard` pattern).
- 14 new `hosts.extensions.*` i18n keys (en + zh-TW).
- Pre-merge round-2 review fixes: `setError(null)` before mutation fetches, stale hostId fetch cancellation, offline button guard, test comment clarification.

### Follow-up issues

- #474 add fetch timeout to `useStatuslineInstall`
- #475 `ConflictDialog` should dismiss on `hostId` change
- #476 extract `ConfirmDialog` to replace `window.confirm`
- #477 `AgentExtensionRow.extensionId` prop is false extensibility
- #478 `AGENT_EXTENSIONS` config table instead of `agentType === 'cc'` branching

## [1.0.0-alpha.185] - 2026-04-19

### Feat: terminal link detection — 3 modes (absolute / relative with `/` / bare filename), gated by settings（#470）

- 將內建 file-path matcher 拆為 3 個獨立 matcher（絕對路徑 / 含 `/` 的相對路徑 / 無 `/` 的裸檔名），各自對應 `linkDetectAbsolute` / `linkDetectRelativeSlash` / `linkDetectBareFilename` 設定開關（預設僅 absolute 開啟，其餘因高誤判率預設關閉）。
- 同時修正所有 matcher 的 multi-extension 錯配 bug：`foo.d.ts` / `file.min.js` / `v1.2.3.tar.gz` 原本只匹配第一段副檔名，現以 `(?:\.[A-Za-z0-9]+)+` 全數納入。
- `TerminalSection` 新增 `<LinkDetectionSection>` 子元件（3 toggle + i18n，en + zh-TW），與 appearance 設定解耦。
- Matcher 採 `createFilePathMatcher({ id, regex, isEnabled })` factory 樣式，gate 由 `register.ts` 在註冊時注入，解除對 `useUISettingsStore` 的直接耦合，unit test 不再需 mutate store。

### Fix: terminal link underline no longer drifts on lines with CJK / wide chars

- 新增 `buildJsOffsetToCol`（`col-map.ts`）：走訪 xterm `IBufferLine.getCell` 建立 JS UTF-16 offset → terminal cell column 的單調遞增表，binary search 查詢；正確處理 CJK 寬字元、emoji surrogate pair、past-end clamp。
- `createXtermLinkProvider` 每次 `provideLinks` 建表一次供所有 matcher 共用，matcher 回傳的 JS offset 統一在 provider 層轉為 terminal cell column 再建構 `ILink.range`，修正連結底線偏移。
- `LinkRange` 型別註解更新，明確雙層語意：matcher emit JS offset / dispatch 到 opener 時 `token.range` 已為 terminal cell column。

### Feat: daemon `GET /api/sessions/{code}/cwd` — returns tmux `pane_current_path` for link resolution

- `tmux.Executor` 新增 `PaneCurrentPath(target)`，呼叫 `tmux display-message -p -t <target> '#{pane_current_path}'`；`FakeExecutor` 對應 `SetPaneCwd` setter。
- 新增 `GET /api/sessions/{code}/cwd` 路由，handler 先 `GetSession(code) → info.Name` 解碼再查 tmux（對齊其他 handler 模式）。
- SPA 新增 `fetchSessionCwd(hostId, sessionCode, signal?)` host-api client。`BuiltinTerminalLinksDeps` 由 8 欄位扁平結構重組為 `{ urlOpener, filePathOpener }`，sub-object 與各自 `createOpener` 參數 1:1 對應。

### Fix: file-path opener security — path traversal guard + IP/version false-positive filter + 5s timeout

- `resolveCwdPath(cwd, rel)` 正規化 `.` / `..` / 重複斜線後驗證結果仍在 `cwd` 的父目錄範圍內；`../../../etc/passwd` 類攻擊被拒絕，並以 `console.warn` 記錄。允許一層 `../App.tsx` 的常見相對路徑。
- Matcher 新增「全副檔名皆為數字」的排除（如 `192.168.1.1` / `1.2.3` / `1.5`），避免 IP 位址與版本號誤判為檔名。
- Opener cwd fetch 加 5 秒 timeout（AbortController），慢/斷線 host 不再 ghost click 無回饋；所有失敗路徑改為 `console.warn` 帶 host/session/path 上下文，非靜默。

### Follow-up issues

- Daemon `fs/handler.go` 僅做 `filepath.IsAbs(filepath.Clean(path))` 驗證，無 cwd boundary 檢查；SPA 為單一防線。此為既有架構，非本 PR 引入，另開 issue 追蹤。

## [1.0.0-alpha.184] - 2026-04-19

### Feat: CC statusline installer — SPA ccStatus store + tab rendering（PR-1b，#471）

- 新增 `<HoverTooltip>` 元件（`spa/src/components/HoverTooltip.tsx`）：從 `ActivityBarNarrow` 的 `ws-tooltip` pattern 抽出，純 CSS `group-hover:opacity-100` fade-in，`placement: 'top' | 'right'`，`role="tooltip"`，parent 需 `.relative.group`。`ActivityBarNarrow` + `InlineTab` + `SortableTab`（pinned + regular）皆改用之。
- `useAgentStore`：新增 `ccStatus: Record<compositeKey, CcStatusEntry>` 非 persist slot，加 `setCcStatus` / `clearHostAgentStatus` actions。`setCcStatus` 將 `session_name` 鏡射到現有 `oscTitles` channel（經 `sanitizeOscTitle` 清理 ANSI/C0）。`clearHostAgentStatus` 按 provenance 只清除 ccStatus 來源的 oscTitles，保留終端 OSC 0/2 來源的 titles。抽出 module-level `omitKeys` helper，`removeHost` 共用。
- `dispatchAgentWsEvent`（`spa/src/lib/agent-ws-dispatch.ts`）：新 pure helper，由 `useMultiHostEventWs` 呼叫。解開 daemon wire shape `{agent_type, status}`、驗證 `agent_type === 'cc'`、把 inner `status` 傳給 `setCcStatus`。`agent.status.cleared` 呼叫 `clearHostAgentStatus`。非物件 JSON / 錯 agent_type / 缺 status 欄位皆靜默忽略。
- `useTabDisplay.displayTitle` 改為 `${oscTitle} - ${baseLabel}` 組合形式（原本 oscTitle only）；`tooltip` deprecated 欄位整個移除，InlineTab + SortableTab 統一從 `displayTitle` 取值。
- `InlineTab` 改用 `<HoverTooltip placement="right">`（避開 `ActivityBarWide.overflow-y-auto` 截斷）；`SortableTab` 兩個 call sites（pinned button + regular span）也遷移到 HoverTooltip（placement=top）。
- 兩輪 review + 大量 pre-merge fix：round-1 clean；round-2（attacker / defender / file-size）找到 3 個 user-facing bug（wire shape mismatch 導致 feature 整個不運作、tooltip placement 被 sidebar 截斷、a11y regression）全部 pre-merge 修；defender 提出的 oscTitles provenance concern 也順手修；filterKeys 重複 + deprecated tooltip 欄位一併清掉。
- 測試：2065/2065 pass（+10 vs PR-1a baseline），204 files，`tsc --noEmit` clean。

### Follow-up issues

- #472 HoverTooltip 擴充 `placement='bottom'`（SortableTab 在 top TabBar 用 `placement='top'` 是 compromise，實測若 clipping 明顯再處理）

## [1.0.0-alpha.183] - 2026-04-19

### Feat: CC statusline installer — wrapper + daemon endpoints（PR-1a，#464）

- 新增 `pdx statusline-proxy` subcommand：讀 stdin JSON、render 預設 `[pdx]` 或透過 `--inner` exec 原 CC statusLine 指令、同步 POST 狀態到 daemon（2s timeout，靜默失敗）。`resolveDaemonHost` 把 `0.0.0.0`/`::`/空字串重寫為 `127.0.0.1`，避免 daemon bind 萬用位址時 proxy 無法連線。
- `internal/agent/cc`：`StatuslineInstaller` interface 實作，install / remove 對 `~/.claude/settings.json` 做 atomic write（保留 file mode、tmp-leak-safe），shell-quote round-trip（pdxPath、inner 皆單引號包覆），mode 偵測（none / pdx / wrapped / unmanaged）用 `go-shellwords` 解析；remove 拒絕 unmanaged 以 409 回報。
- `internal/module/agent`：`GET/POST /api/agent/{agent}/statusline/setup` + `GET /api/agent/{agent}/statusline/status` + `POST /api/agent/status`（收 proxy POST）+ WS `agent.status` snapshot replay on subscribe + `agent.status.cleared` broadcast on remove。`statuslineMutex` 序列化 RMW 並於 HTTP 回應前釋放；`statusSnapshots` 狀態掛在 `Module` struct（非 package global）。
- 兩輪 review + 5 個 pre-merge fix：round-1（5-angle）修 `pdxPath` shell-quote；round-2（attacker / defender / file-size）修 `bind=0.0.0.0` 靜默失敗、`os.Exit(0)` 一致性、`statuslineMutex` 包住 HTTP response、snapshot 狀態搬進 `Module`、`snapshotMu` 在 WS `sub.Send` 前釋放。
- 測試：Go ~50 tests（proxy render / shellwords / install / remove / WS snapshot / handler）全綠。

### Follow-up issues

- #465 Statusline handler 硬編碼 `agentType != "cc"` guard（Codex 擴展時需統一為 registry + interface assertion）
- #466 `removeStatusline` 雙重讀取 `settings.json` 存在 TOCTOU（可靜默刪除使用者 unmanaged 設定）
- #468 `hooks.go` 與 `statusline.go` 的 `settings.json` 讀/寫流程重複（hooks 那份缺 file mode 保留）
- #469 `handler.go` 641 行 / 5 職責，PR-1c 前建議拆分 `handler_statusline.go` / `handler_hooks.go`

## [1.0.0-alpha.182] - 2026-04-19

### Feat: terminal link registry — matcher/opener plugin architecture（#458）

- 新增 `spa/src/lib/terminal-link/` 模組：`LinkMatcher`（偵測 token）+ `LinkOpener`（處理點擊）可獨立註冊的 registry 架構；dispatch 以 priority DESC 路由。
- 內建 URL matcher（`http(s)://…`，含結尾標點剝除、URL query path 誤匹配排除）+ URL opener（Electron：browser tab / shift+click mini window；web：`window.open`；加 http(s) scheme 白名單兜底）。
- 內建 file-path matcher（絕對 Unix 路徑 + 末段需副檔名 + 可選 `:line[:col]`；regex 改寫避免 ReDoS）+ file-path opener（橋接既有 `FileOpener` registry，Editor / Image / PDF 既有 opener 零修改自動套用）。
- xterm 整合：新增 `createXtermLinkProvider` 將 registry 包成 `ILinkProvider`，於 `useTerminal` 以 `registerLinkProvider` 掛載；`linkContext` 透過 ref 傳入讓 mount-only effect 不需重綁。
- 移除 `WebLinksAddon` + 舊 `spa/src/lib/link-handler.ts`；boot 在 `registerBuiltinModules()` 透過 DI 接上 Tab/Workspace store + Electron API。
- 測試：181 tests（registry / matcher / opener / xterm-provider / register / TerminalView 整合）全綠，含 ReDoS 防護 + scheme 白名單 + dispose lifecycle 驗證。

### Follow-up issues

- #462 Electron `openMiniWindow` API 版本不匹配缺 fallback
- #463 host switch 時 activate token 可能 stale

## [1.0.0-alpha.181] - 2026-04-19

### Feat: daemon self-rebuild + server-side `PDX_DEV_UPDATE` gate（#456）

- 新增 `POST /api/dev/daemon/rebuild`（SSE）：TryLock → `go build` 帶 `-ldflags -X BakedInHash=<hash>` → atomic rename `bin/pdx.new → bin/pdx` → `syscall.Exec` self。build context 繼承 `m.stopCtx`，SIGTERM 會取消進行中 build。
- 新增 `GET /api/dev/daemon/check`：回 `{current_hash, latest_hash, available}`，`current_hash` 由 `-ldflags` 在 build 時注入（Makefile 已更新）。
- 新增 listener `SO_REUSEADDR` + 指數退避 retry bind（最後一圈不 sleep），survive exec-self restart。
- `/api/dev/update/*` + `/api/dev/daemon/*` 補上 server-side `PDX_DEV_UPDATE=1` gate（原本只在 Electron preload 端 gate，是裸露洞）；註冊雙層 gate 註解 + `Start` disabled log 升級說明 `config.Dev.Update` + env var 兩條件。
- SPA `Settings → Development` 新增 Daemon 區塊：check / rebuild 按鈕、即時 SSE log 面板、rebuild 成功 3s 後自動重 check；i18n keys `settings.dev.daemon.*`（en + zh-TW）。
- 測試：38 Go tests（含 race）覆蓋 gate、listener rebind、check、rebuild success / error / 409 / rename-failure；SPA 1906 tests 全綠。

### Follow-up issues

- #459 SPA `rebuildDaemon` 缺 AbortController + setTimeout cleanup
- #460 `go run` 啟動時 rebuild 靜默失敗偵測
- #461 `DevEnvironmentSection` 拆出 `DaemonRebuildSection`

## [1.0.0-alpha.180] - 2026-04-19

### Feat: active workspace/home 標題點擊改 toggle 展開（#457）

- `WorkspaceRow` / `HomeRow`：當該列已 active 且 `tabPosition !== 'top'` 時，點擊標題切換 inline tabs 展開/收合（不再 re-select）。
- 非 active 時維持原本 select 行為，chevron 按鈕一律 toggle。

### Fix: 從 Home 切到空 workspace 卡住 standalone tab（#457）

- `handleSelectWorkspace` 在目標 ws 沒 tabs 時補 `setActiveTab(null)`；先前 standalone tab 留著讓 `activeStandaloneTabId` 繼續遮蔽 `ActivityBar` 的 `isActive`，visual 看起來沒切過去。
- Pre-existing bug，被 active-click toggle 誘發後才注意到。

## [1.0.0-alpha.179] - 2026-04-19

### Fix: Activity bar resize handle 無法拖曳（#455）

- `ActivityBarWide` 包 `RegionResize` 的 wrapper 從 `hidden lg:block` 改為 `hidden lg:flex`。
- 原本 `.relative.w-px` 是 block-in-block，高度塌成 0，`inset-y-0` 熱區跟著變 0 高，看似 handle 存在實則點不到。
- 改成 flex 容器後內層以 flex item stretch 填滿，熱區恢復正常可拖。

## [1.0.0-alpha.178] - 2026-04-18

### Tweak: TitleBar 按鈕下移幅度 2px → 2.5px

- `sidebar-toggle` / `layout-buttons` 兩個 slot 的 `translate-y` 從 2px 微調到 2.5px。
- 直接 main commit（微調，無 PR）。

## [1.0.0-alpha.177] - 2026-04-18

### Tweak: TitleBar 按鈕下移幅度 3px → 2px

- `sidebar-toggle` / `layout-buttons` 兩個 slot 的 `translate-y` 從 3px 再降到 2px。
- 直接 main commit（微調，無 PR）。

## [1.0.0-alpha.176] - 2026-04-18

### Tweak: TitleBar 按鈕下移幅度 5px → 3px

- `sidebar-toggle` / `layout-buttons` 兩個 slot 的 `translate-y` 從 5px 降到 3px，視覺上稍微往上靠一點。
- 直接 main commit（微調，無 PR）。

## [1.0.0-alpha.175] - 2026-04-18

### Polish: TitleBar 按鈕視覺收斂 (#454)

- `CollapseButton` topbar variant 拿掉多餘的 `flex items-center justify-center`，class string 與 TitleBar 右側 region-toggle 完全一致（svg `display: block` 本來就能自然置中）；header-right / divider 變體保留 flex 置中（它們有固定 w/h）。
- `TitleBar`：`sidebar-toggle` 與 `layout-buttons` 兩個 slot 加上 `translate-y-[5px]`，整體下移 5px，不再與 traffic-light row 視覺互衝；title 與 sync 警示（absolute overlay）不動。
- 測試：CollapseButton 補 3 組 class-shape assert（topbar 無 flex / header-right + divider 保留 flex）；TitleBar 補 2 組 slot 有 `translate-y-[5px]` 的 regression。

## [1.0.0-alpha.174] - 2026-04-18

### Fix: `tabPosition='both'` 允許折疊 activity bar (#453)

- #452 改了 icon + 樣式但沒解掉核心 bug：`both` 模式下 `CollapseButton.locked` 永遠為 true，按下沒反應、tooltip 還誤寫「tabs are on the left」。
- 根因：`locked` / store guards / heal invariant 把 `left` 和 `both` 綁在一起要求 wide。實際上 `both` = 上方 tab bar 已經可以觸及所有 tab，activity bar 縮窄完全合理。
- `CollapseButton.locked` 收窄到 `tabPosition === 'left'`；`setActivityBarWidth` / `toggleActivityBarWidth` / `setTabPosition` / `healLayoutInvariant` 都拿掉 `both` 的強制 wide。
- 現有 both+wide 的 persisted layout 不動，只是從現在起可以自由 toggle。
- 測試：`useLayoutStore.test.ts` + `CollapseButton.test.tsx` 改寫成新規格並加入 "toggle wide→narrow in both" 與 `setTabPosition` 保留寬度兩組新案例。

## [1.0.0-alpha.173] - 2026-04-18

### Tweak: Activity-bar collapse button 換成 SidebarSimple (#452)

- `CollapseButton` 改用單一 `SidebarSimple` icon，取代動態 `CaretDoubleLeft` / `CaretDoubleRight` 兩張圖互換。
- `topbar` variant 加上 region-toggle 慣用的 active 配色：wide 時 accent 底色（讀作「activity bar is on」），narrow 時 secondary 常態，和 TitleBar 右側 region toggles 同一家族。
- 單一 icon 消除每次點擊的 swap flicker，連帶掃除 #446 commit message 裡提到的「感覺壞掉」觀感。
- 測試面：`CollapseButton.test.tsx` 補上 SidebarSimple path 簽章、topbar active/inactive 配色、與 `p-1 rounded` 共用 pattern 的三組 regression。

## [1.0.0-alpha.172] - 2026-04-18

### Cross-platform: sub-pixel tab icon margin 在 @1x 退回整數 (#451)

- 新增 Tailwind 4 custom variant `lowdpi (@media (max-resolution: 1.5dppx))` 在 `spa/src/index.css`，covering @1x 與 Windows fractional scaling < 1.5x DPR。
- `TabIcon.tsx` / `renderInlineTabIcon.tsx` 兩處 sub-pixel margin 補上 fallback：
  - 非 badge wrapper：`ml-[1.5px] lowdpi:ml-px`
  - badge wrapper：`mr-[0.5px] lowdpi:mr-0`
- 編出的 CSS 為 `@media (resolution<=1.5x){.lowdpi\:mr-0{...}}`，避免 @1x 螢幕上 0.5px 被瀏覽器不一致 round 造成 tab icon 抖動或對不齊。

## [1.0.0-alpha.171] - 2026-04-18

### Tweak: Tab icon margin 依 indicator style 微調 (#450)

- **icon / dot / iconDot wrapper**：`ml-px` → `ml-[2px]`，向右多 1px 與 badge icon 視覺右緣對齊。
- **badge wrapper**：保持 `ml-px`，新增 `mr-px`，讓 icon+overlay 群與 trailing label 之間多 1px 呼吸感，平衡 overlay dot 在 `right:-2` 突出的不對稱。
- 影響 `TabIcon.tsx` 與 `renderInlineTabIcon.tsx` 兩個 surface。

## [1.0.0-alpha.170] - 2026-04-18

### Hotfix: Tab icon 對齊統一 ml-px (#449)

- **alpha.169 對齊翻車**：badge mode 改 `ml-[3px]` 想補償 Phosphor robot glyph 的視覺偏移，方向與幅度都猜錯，結果 robot 變成比 terminal 偏右 2–3px。
- **修正**：badge mode 改回 `ml-px`（alpha.168 值），同時 icon / dot / iconDot 三個 wrapper 也補上 `ml-px`。`TabIcon.tsx` 與 `renderInlineTabIcon.tsx` 兩處同步。
- **效果**：所有 indicator style 的 icon 欄位橫向位置完全脫鉤、落在同一 x。

## [1.0.0-alpha.169] - 2026-04-18

### Fixes / UI: tab/workspace 對齊與對比微調 (#448)

- **Tab icon 對齊**：badge mode wrapper `ml-px` → `ml-[3px]`，補償 Phosphor robot glyph 在 viewbox 中略偏左的視覺偏移，與 terminal icon 對齊；同時為 subagent dots 預留更多左側空間（`TabIcon` / `renderInlineTabIcon`）。
- **Workspace header 按鈕對齊 InlineTab close**：row 加 `pr-1.5`；`+` 與 chevron 都改 12px icon + `p-0.5`，移除 `+` 的 `weight="bold"` 與 chevron 的 `mr-0.5`，並補上 `hover:text-text-primary`，三者視覺與位置一致。
- **Unread 燈號亮度**：`#b91c1c` (red-700) → `#ef4444` (red-500)，比照 running 與 error 級別亮度；影響 `TabIcon` UnreadPip / `TabStatusIndicator` UNREAD_COLOR / `SortableTab` 與 `InlineTab` 角落 pip / `renderInlineTabIcon` UNREAD_PIP。
- **StatusBar host 名稱**：`onClick` → `onDoubleClick`，避免單擊誤開主機設定頁；`status.open_host_hint` zh-TW/en 文案改為「雙擊」。

## [1.0.0-alpha.168] - 2026-04-18

### Refactor: `useTabDisplay` hook 收斂 tab 顯示邏輯 (#447)

- **新 hook `useTabDisplay`**：把原本在 `SortableTab`（top TabBar）與 `InlineTab`（activity bar）各寫一次的 OSC title 解析、agent icon fallback、host offline 判定、agent store 訂閱集中到同一處，兩個 surface 從此共用一份顯示邏輯，未來加 badge / indicator 只需改 hook 一處。
- **InlineTabList 不再計算 title**：原本用跨 host flat session lookup（同 code 不同 host 會標到錯的 session 名），改由 hook 用每個 tab 自己的 `hostId` 做 per-host 查詢 — 順手修掉多 host 命名碰撞的 pre-existing bug。
- **InlineTab `aria-label` 改用 `displayTitle`**：OSC 啟動時螢幕閱讀器讀到的是目前顯示的標題，與視覺一致。
- **砍 `iconMap` prop**：hook 內部從 `ICON_MAP` 解析 pane icon，`TabBar` / `SortableTab` 不再互傳。
- 淨效果：相較 alpha.167，`SortableTab` / `InlineTab` / `InlineTabList` 合計減 130+ 行重複邏輯；新增 155 行 hook 測試涵蓋 label / OSC / per-host 隔離 / host offline / agent 欄位。

## [1.0.0-alpha.167] - 2026-04-18

### UI: TitleBar 搬家 + 視窗按鈕置中 (#446)

- **CollapseButton 移至 TitleBar 左側**：從 ActivityBarWide 的 header-right 與 ActivityBarNarrow 的 divider 下架，改到 TitleBar 左邊、macOS 72px traffic-light 保留區之後的 `sidebar-toggle` 槽（Claude.ai 風格）。
- **Topbar variant 樣式統一**：`p-1 rounded` + 14px icon + `hover:bg-surface-hover`，和 region-toggle / layout-pattern 按鈕同一個視覺 family。
- **TitleBar 高度 30 → 36**：讓 macOS traffic-light 上下置中（y=12 + 6 = 18 ↔ 36/2 = 18）。
- **副作用修正**：按鈕從 ActivityBar 內部（被 DndContext 包的 scrollable 區域）搬出後，先前「展開狀態下按鈕失效」的 bug 一併解決。

## [1.0.0-alpha.166] - 2026-04-18

### Fixes / UI (#445)

- **InlineTab icon 同寬佔位**：`DOT_SLOT` 從 `w-3.5` (14px) 改為 `w-4` (16px)，跟 `TabIcon` 一致。14px icon 放進 16px slot 後四周多 1px 內距，不同 agent / pane icon 寬度統一，tab name 起始 x 對齊。
- **Badge overlay 位置對齊 top bar**：slot 擴大後 `TabStatusIndicator` overlay（`top:-1 right:-2`）相對 icon 的位置跟上方 TabBar 一致。
- **iconDot 結構對齊**：拿掉兩個 slot 之間的 `gap-1`，跟 `TabIcon` 相同。

## [1.0.0-alpha.165] - 2026-04-18

### Fixes / UI (#444)

- **InlineTab 顯示規則對齊 SortableTab**：dot / iconDot 模式內嵌 unread pip，badge 模式改以紅色 overlay；外部 corner pip 加上 `shouldShowGlobalUnreadPip` 判定並對齊上方 TabBar 的位置/大小（`w-2 h-2`、`-top-[4px] -right-[4px]`）。
- **Active tab 移除 accent border**：`border border-accent-muted` → `border border-transparent`，兩態都保留透明 1px 邊框，尺寸不跳。
- **Host tab icon**：`ICON_MAP` 補 `HardDrives`，上方 TabBar 的 host tab 現在能正確渲染硬碟 icon。
- **Settings icon**：`GearSix` → `Sliders`（ActivityBar 寬/窄、WorkspaceContextMenu、SessionPanel、tab-icon-map、pane-labels）。
- **Editor tab icon**（非 diff）：`File` → `TextAlignLeft`，對齊 VSCode 習慣。

## [1.0.0-alpha.164] - 2026-04-18

### Sync P0 — 體質清理 (#432)

- **ConflictBanner**: new UI for resolving per-field sync conflicts. Appears at top of Settings → Sync when pending conflicts exist. Supports expand/collapse, per-row local/remote choice, bulk "keep all local / use all remote", all-or-nothing apply.
- **TitleBar warning icon**: global Phosphor `Warning` icon next to workspace title when there are pending sync conflicts. Tooltip shows count, click deep-links to `/settings/sync`.
- **`/settings/<section>` deep-link**: URL now reflects the active settings section; back/forward navigation and external links (e.g. TitleBar icon) open the correct section.
- **i18n**: full migration of SyncSection — 47 new `settings.sync.*` keys in `en.json` and `zh-TW.json`. Closes #397.
- **Fix #394**: DaemonProvider URL-encodes `clientId` query params; `listHistory` rejects non-positive-integer limits.
- **Fix #395**: `handleExportAll` now honours the `busy` flag to prevent mid-operation export.
- **Fix #396**: `importFromText` enforces 5 MB size + 32 depth limits via typed `ImportError`, surfaced as friendly i18n messages.
- **State**: `useSyncStore` persists `pendingConflicts` / `pendingRemoteBundle` / `pendingConflictsAt` across sessions.

### Round-2 review fixes

- **resolveConflicts push** (R2-A4): `handleResolveConflicts` now pushes the merged bundle back to the daemon provider after a successful resolve, preventing other devices from seeing ghost conflicts on their next pull.
- **pendingRemoteBundle trim** (R1-#2): `setPendingConflicts` keeps only the collections for contributors that actually have conflicts, capping localStorage footprint (`resolveConflicts` never reads the rest).
- **Provider-change race guard** (R2-A6): `handleSyncNow` and `handleFileChange` snapshot `activeProviderId` before their awaits and drop stale results if the user swapped provider mid-flight.
- **TitleBar predicate alignment** (R1-#3): warning icon now matches `SyncSection` banner's full render guard (`provider !== null && remoteBundle && pendingConflictsAt`) so clicking the icon never leads to a banner-less settings page.
- **Settings URL self-heal** (R2-A7): `/settings/<bad>` or `/settings/foo/extra` are replaced with the canonical `/settings/<active>` so back/forward history never preserves garbage URLs.
- **Plural-safe i18n** (R2-A5): 4 count-sensitive keys (`conflict.banner`, `conflict.tooltip`, `conflict.resolved`, `status.conflictsPending`) split into `_one` / `_other` variants via new `pluralKey` helper; English singular stops rendering "1 conflicts".

### Tracked separately (review follow-ups)

- Engine `ResolvedFields` compound-key refactor → #440
- `formatRelativeTime` live tick → #441
- `SyncSection.tsx` split (useSyncActions hook + StatusLine) → #442

### Deferred / tracked separately

- Daemon Pairing UI → gh #421 (Phase P2)
- Cloud Provider → gh #422 (Phase P4)
- Onboarding flow → gh #423 (Phase P6)
- Sync History Dialog → Phase P1
- File Provider → Phase P3
- Content-addressed (Editor) → Phase P5

## [1.0.0-alpha.163] - 2026-04-18

### 介面調整

- **spa**：Activity bar 的 `InlineTab` 視覺對齊上方 `TabBar` —— 移除未定義 token `border-accent-base` 造成的白色左 border（原本 fallback 到 `currentColor`），改用 `border border-accent-muted` / `border border-transparent`；active 文字 `text-white`、inactive 文字 `text-text-muted`，和 `SortableTab` 一致（#443）
- **spa**：`WorkspaceRow` / `HomeRow` 的 `+` 與 chevron 按鈕拿掉硬寫的 `text-text-primary` / `text-text-muted`，改繼承父層顏色，和列內 close X 表現一致（#443）
- **spa**：`InlineTab` 內容左移 6px（`pl-6` → `pl-[18px]`）（#443）
- **spa**：`renderInlineTabIcon` icon 從 12px 改為 14px，對齊上方 `SortableTab` 的 icon 尺寸（`DOT_SLOT` 同步 `w-3 h-3` → `w-3.5 h-3.5`）（#443）

## [1.0.0-alpha.162] - 2026-04-18

### 介面調整

- **spa**：Activity bar collapse 按鈕從 HomeRow 抽出到專屬的上方 header 區，仿 Claude Code desktop 的留白佈局，不再貼著 Home 列（#439）
- **spa**：workspace hover 顯示的 `+` new tab 按鈕改亮改粗（`text-text-primary`、`size=14`、`weight="bold"`），提高可見度（#439）
- **spa**：workspace / home 列的展開收合 chevron 改放到最右側，與 `+` 按鈕同側（#439）
- **spa**：清掉點擊 workspace 列後殘留的瀏覽器預設橘色 focus 外框（header 容器與內部 buttons 加 `focus:outline-none focus-visible:outline-none`）（#439）
- **spa**：Settings → Appearance 的 Tab Position 從 radio list 改為共用的 `SegmentControl`，與 Tab indicator style 欄位風格一致（#439）

## [1.0.0-alpha.161] - 2026-04-18

### 修復

- **spa**：Tab double-click 現在會打開 rename popover（TabBar 與 Activity Bar InlineTab 都生效），複用既有 `openRenameForTab`（#438）
- **spa**：StatusBar `{host}` 由雙擊改名改為單擊開啟 Host 設定頁並選中該 Host（#438）

### 介面調整

- **spa**：StatusBar `{host}` / `{session}` span 移除 `cursor-pointer` —— 仍可互動但不再顯示手型，減輕介面認知負擔（#438）

## [1.0.0-alpha.160] - 2026-04-18

### 功能

- **daemon / spa**：Dev Update 加 SSE build log streaming（#428）—— 新增 `GET /api/dev/update/check/stream`，Settings → Development 按下 Check 後即時顯示 build 的 stdout / stderr / phase 事件，取代原本 3 秒 JSON polling；遲到的 subscriber 拿到完整 replay；並發多個 client 共享同一次 build
- **daemon**：新增 `requiresFullRebuild` 旗標 —— 偵測 `package.json` / `pnpm-lock.yaml` / `electron-builder.yml` / `build/` 變動時，Settings 跳警告 banner 提示需要手動跑 `pnpm run electron:build`（dev update 只抽換 JS bundle）；hash 由 `electron.vite.config.ts` 寫入 `out/.build-info.json` 的 `rebuildHash` 欄位

### 介面調整

- **spa**：新元件 `DevBuildLogPanel` —— 可捲動 `<pre>` 顯示 build log、sticky auto-scroll（使用者本在底部時才跟著捲）、複製完整 log 按鈕；`full_rebuild_hint` 文案指明「執行 daemon 的主機」避免 Air / Mini 情境混淆

### 重構

- **daemon**：`runBuild` 改接受 `*BuildSession` 參數，`CombinedOutput` 換成 pipe-based `streamCmd` 逐行廣播；`snapshotCheck` 拆出唯讀版 `observeCheck` 供終局 SSE 事件使用，避免 build-info 寫失敗時誤觸第二次 build
- **electron**：`streamCheck` 加在 `updater.ts`，SSE 用 fetch + ReadableStream 自解；IPC 每次呼叫配獨立 reply channel，舊 stream 事件不會灌進新 listener
- **spa**：`DevEnvironmentSection` 改用 `streamCheck` IPC；`daemonBase` / `token` 改變時自動重啟 stream 關閉舊 host 殘流

### 測試

- Go 新增 17 個 daemon 測試（BuildSession fanout / 遲到 subscribe / 併發 unsubscribe / handleCheckStream 各狀態 / 終局不觸發第二次 build / client disconnect 釋放 subscription / rebuild-hash 5 case）
- SPA 新增 1 個 `DevEnvironmentSection` test（host 切換重啟 stream）+ 5 個 `DevBuildLogPanel` tests（總計 1876 通過）

### 後續

- Issues #433–#437 追蹤延後項目：build error 脈絡、首次連線 401 文案、`electron/updater.ts` / `internal/module/dev/handler.go` / `DevEnvironmentSection.tsx` 拆分

## [1.0.0-alpha.159] - 2026-04-18

### 介面調整

- **spa**：badge 模式 tab icon + badge 整組右移 1px（`ml-px`），subagent dots 改用 literal `left: -4px` 貼在 box 外緣避開 icon 欄位（#431）；`SubagentDots` 加可選 `left?: number` prop，dot / iconDot 模式沿用原 `calc(50% + offset)` 行為
- **spa**：順手移除 `TabIcon` 在 dot / iconDot branches 傳給 `SubagentDots` 的無效 `isActive` prop

## [1.0.0-alpha.158] - 2026-04-18

### 功能

- **spa**：三選 `tabPosition`（`top` / `left` / `both`）（#427）—— `left` 模式自動展開 active workspace 內嵌 tabs，`top` 隱藏 Activity Bar caret 與 inline `+`，`both` 同時顯示兩個 tab 層（Settings → Appearance 新增三選 radio）
- **spa**：`InlineTab`（left mode）支援 `tabIndicatorStyle`（`icon` / `dot` / `iconDot` / `badge`）—— agent icon、hover close、host-offline `WifiSlash`、locked `Lock` 圖示，與 top-bar tab 達到功能對等；active tab 改用強調左邊框 + 高亮背景，視覺上與 active-workspace 紫色 ring 明確區分
- **spa**：Cross-workspace tab drag 顯示 make-way 動畫 —— 透過 optimistic move 在 target workspace 即時預覽 gap，遵循 dnd-kit multi-list 模式；tab 拖動限制為垂直軸（`x = 0`）
- **spa**：Activity Bar collapse toggle 分拆為三個位置 —— title bar 常駐按鈕、寬欄時 workspace home-row 右側按鈕、窄欄時 divider 右邊緣 hover-reveal overlay

### 介面調整

- **spa**：`+ New tab` 按鈕從各 workspace 底部移至 workspace header 右側（hover-reveal），減少左側欄縱向雜訊
- **spa**：`RegionResize` 可拖拽區域從原本窄縫擴大到 11px、hover 顏色加深，side-panel 與 activity-bar 的拖拽邊界更容易捕捉；視覺縫線維持 1px

### 重構

- **spa**：`SortableTab` 抽離 `ICON_MAP` 為獨立模組 `tab-icon-map.tsx`，left/top tab 共用同一份 icon 註冊
- **spa**：cross-workspace drag orchestration 抽離為 `useCrossWorkspaceDragOver` hook；`useWorkspaceStore.getState()` 在 `removeTabFromWorkspace` 後重新讀取，遵循 PR #392/#419 stale-closure 慣例
- **spa**：`renderInlineTabIcon` 抽離為 `features/workspace/lib/renderInlineTabIcon.tsx`，專門處理 left-mode 12px dot/badge/iconDot 呈現

### 測試

- SPA 合計 1895 tests 通過（+28）：`InlineTab` 行為（indicator styles、host offline、lock、pointerdown dnd-kit 順序 active/inactive 三 case）、`renderInlineTabIcon` 四模式、`useCrossWorkspaceDragOver` optimistic move + pinned/standalone short-circuit、`ActivityBarWide` auto-expand、`WorkspaceRow`/`HomeRow` chevron 條件顯示、`CollapseButton` 三 variant、`RegionResize` 11px hit zone、`useLayoutStore` `both` mode invariant

## [1.0.0-alpha.157] - 2026-04-18

### 功能

- **spa**：Agent 動態標題 + 雙擊 session rename（#424）—— Settings → Terminal 新增「顯示 agent 動態標題」toggle，啟用後 tab label 改顯示 agent 送的 OSC 0/2 title（僅 agent-identified session 生效，shell-only session 保留 session name），hover tooltip 顯示「title - sessionName」；右下 status bar 多一列 OSC title（同樣受 toggle 控制）；左下 status bar 的 host / session name 雙擊進入 session rename popover，anchor 用 `e.currentTarget` 貼合被點元素；`useAgentStore` 新增持久化 `showOscTitle` + ephemeral `oscTitles` per-session map；`useTerminal` 暴露 `onTitle` callback 並綁定 xterm `onTitleChange`

### 介面調整

- **spa**：icon+badge 模式 overlay dot 位置微調 `top:-1px / right:-2px`，與 icon 角落更貼合
- **spa**：未讀視覺改 per-mode 呈現 —— `badge` 模式把 overlay dot 染 unread 紅 (#b91c1c) 取代原本獨立角落 pip；`dot` / `iconDot` 模式在 dot wrapper 右上角疊 5px 小紅 pip；`icon` 模式與尚未收到 agent 事件時保留原本 corner pip 做 fallback
- **spa**：Agent error 狀態改用 `WarningDiamond` duotone 圖示取代燈號 —— `badge` 模式在 overlay 位置繪製；`dot` / `iconDot` 模式整顆 dot 換成 warning diamond
- **spa**：status bar host / session name 加上 `cursor:pointer` + `select-none` + `status.rename_hint` tooltip，視覺上明確可雙擊

### 重構

- **spa**：`TabStatusDot` 改名為 `TabStatusIndicator`（元件現在會渲染 dot 或 WarningDiamond，舊名不再貼合行為）；prop `style` → `mode`；`data-testid` 同步更名 `tab-status-dot` → `tab-status-indicator`
- **spa**：抽 `renderTabIcon` / `UnreadPip` / `shouldShowGlobalUnreadPip` 出 `SortableTab.tsx`，分拆到新檔 `TabIcon.tsx` 與 `tab-icon-helpers.ts`（函式 export 與元件 export 拆開以符合 react-refresh 規則）
- **spa**：`useAgentStore` 新增 `sanitizeOscTitle(raw)` helper，strip ANSI CSI (`\x1b[...m`) 與 C0 控制字元後再存 `oscTitles`，避免第三方 shell 或 agent 嵌 escape 序列時畫面亂碼
- **spa**：`features/workspace/hooks.ts` 提出 `openRenameForTab(tab, anchor?)` helper，context menu 與 StatusBar 雙擊共用同一開啟邏輯，不再複製 rect 計算

### 測試

- SPA 新增 14 個測試：`TabStatusIndicator` unread 紅 tint / error warning-diamond（overlay + replace）3 項；`SortableTab` badge unread 染紅、dot mode pip、error warning-diamond（badge + dot）共 5 項；`useAgentStore` `setShowOscTitle` toggle、`setOscTitle` 的 trim / ANSI strip / C0 strip / equality guard / clearSession / removeHost 共 6 項；`sanitizeOscTitle` 4 分支；`TerminalSection` OSC toggle 1 項；`TerminalView` mock 補 `onTitleChange`；合計 +14，全套 1867 tests 通過

### 文件

- 新增 `docs/superpowers/specs/2026-04-18-cc-statusline-integration-design.md` backlog spec，記錄 CC `statusLine` / hooks 結構化狀態整合的後續設計方向（HTTP POST 路徑、env session 注入、wrapper pattern、跨機 URL 策略），並彙整 hooks 貧乏欄位 vs statusLine 完整 JSON 對照；同步建立 issue #425（InlineTab 對齊 common tab）與 #426（stream mode OSC 擷取）追蹤本版未處理的一致性缺口

## [1.0.0-alpha.156] - 2026-04-17

### 功能

- **spa**：Agent 官方 logo + Tab indicator 樣式 + Claude Code icon 變體（#414）—— `AGENT_ICONS` 改用 `@lobehub/icons-static-svg`（MIT）的官方 Claude Code + Codex logos，透過 `vite-plugin-svgr` 以 React 元件載入並包一層 `{ size, className }` wrapper；Settings → Terminal 新增 Tab indicator style（`icon` / `dot` / `iconDot` / `badge`，預設 `badge`）與 Claude Code icon variant（`bot` / `star`，預設 `bot`，附 live preview button）；`electron.vite.config.ts` 補 `svgr()` plugin，讓 bundled fallback 在 dev server 不可達時也能渲染 SVG

### 介面調整

- **spa**：`@keyframes breathe` 改以 opacity 1 → 0.25 過渡（原本到 0 會短暫消失），避免 subagent dot 在深色主題下看起來像閃爍 bug；overlay dot `right: 0` 貼齊避免 ring 越界到 app 背景
- **spa**：`dot` 模式下在 Claude Code icon 設定加入淡色提示，說明該模式不顯示圖示，此設定需切換到其他模式才會生效

### 重構

- **spa**：`getAgentIcon(agentType, { ccVariant })` 改採 options 物件，`ccVariant` 只對 `cc` 分支有意義，避免 codex 分支忽略參數的 API 不對稱；Codex icon 改用專用 wrapper 元件取代 `as unknown as AgentIconComponent` cast
- **spa**：`DotStyle` 刪除不再使用的 `'inline'` 成員與 `TabStatusDot` fallback 分支；移除 dead CSS vars `--breathe-color` / `--breathe-bg` 與 `SubagentDots` 用不到的 `isActive` prop
- **spa**：清除 `settings.appearance.tab_indicator.*` 六個廢棄 i18n key

### 測試

- SPA 新增 13 個測試：`getAgentIcon` 5 項、`renderTabIcon` 4 分支 + terminated session 邊界、`TerminalSection` SegmentControl 與 CC icon row 互動 3 項；刪除 `TabStatusDot` inline 測試 1 項，合計 +12，全套 1714 tests 通過

## [1.0.0-alpha.155] - 2026-04-17

### 功能

- **spa**：Layout Phase 3 PR D — 跨 workspace tab DnD（#419）—— tab 可跨 workspace 拖放、拖到 Home header 轉 standalone、拖到他 workspace 的 tab-slot 插入指定位置；`WorkspaceRow` / `HomeRow` header 成為 drop target 附 `isOver` ring；custom collision detection（`pointerWithin → rectIntersection → closestCenter`）解決 header 與 tab-slot 重疊；active tab 跟隨搬動、原 ws 變空時 active 自動切目的 ws（關閉 #402）
- **spa**：Spring-load 500ms 自動展開 collapsed header —— 懸停 500ms 於 collapsed workspace / home header 自動展開讓使用者繼續拖入；fire-time 重查 expanded 狀態避免與手動展開競速；`useSpringLoad(delayMs)` 單槽 timer hook（關閉 #403）
- **spa**：Pinned tab 跨 ws drop 禁止 —— pinned tab 只能在所屬 ws 內重排，其他 drop target（他 ws tab-slot / workspace-header / home-header）一律 noop；dragOver 同步短路 spring-load，不對禁止 target 自動展開（關閉 #404）
- **spa**：Cross-ws handler 可從 `ActivityBarProps` 注入 —— 新增 `onMoveTabToWorkspace` / `onMoveTabToStandalone` optional props；預設走 store 直接變更，parent 可 override 做 intercept 或 veto

### 修復

- **spa**：`insertTab` 對不存在 target workspace 的呼叫直接 abort —— 避免並發刪除情境下把 tab 從 source 移除但未插入任何 ws（孤兒 tab）
- **spa**：同 workspace header drop 改為 noop —— 原本經 `insertTab` dedup 會默默改 `activeTabId`；同步消除誤導的 drop ring highlight
- **spa**：Cross-ws handler 改以 `useTabStore.getState()` 讀 activeTabId —— 對齊 PR #392 resize handler 的 stale-closure 修正慣例

### 測試

- SPA 新增 11 個測試（cross-ws 4、pinned guard 3、insertTab orphan/null 3、spring-load 7、droppable header 3），全套 1834 tests 通過

## [1.0.0-alpha.154] - 2026-04-17

### 功能

- **spa**：Sidebar inline tab 視覺對齊 `SortableTab`（#418）—— agent status dot + subagent dots、unread 紅點、離線 host 的 `WifiSlash` icon、locked tab 的 `Lock` icon 並隱藏 Close button；status slot 只在實際有 status/subagent 時渲染，非 tmux tab 不額外縮進（關閉 #401）

## [1.0.0-alpha.153] - 2026-04-17

### 重構

- **spa**：`ActivityBarWide.handleDragEnd` 抽離為 `computeDragEndAction` 純函式 + `dispatchDragEndAction` 分派（#417）—— discriminated action union（noop / reorder-workspaces / reorder-standalone-tabs / reorder-workspace-tabs）讓 Phase 3 cross-workspace drop、spring-load、pinned guard 有清楚擴充點；dispatch switch 加 exhaustiveness never 斷言（關閉 #407）

## [1.0.0-alpha.152] - 2026-04-17

### 修復

- **spa**：Layout Phase 3 hardening（#415）—— `reorderWorkspaceTabs` / `reorderStandaloneTabOrder` 加 stale-subset guard、phantom id 過濾與重複 id dedup；`WorkspaceRow` name button / `InlineTab` 加 pointerdown 守則，避免手震 ≥5px 偷走 click；移除 chevron 無效的 `onMouseDown stopPropagation`（關閉 #405, #406）

## [1.0.0-alpha.151] - 2026-04-17

### 功能

- **spa**：介面設定 + 多欄 New Tab 佈局（#398）—— Settings 新增 Interface section，New Tab 支援 3col/2col/1col 三個 profile，依視窗寬度 ≥1024/≥640/<640 自動切換，可於設定頁拖曳排序欄位順序；首次啟動僅啟用 1col，2col/3col 預填但 disabled，使用者依需求開啟

## [1.0.0-alpha.150] - 2026-04-17

### 功能

- **spa**：Terminal tab icon 依 agent 類型顯示 —— Claude Code 顯示 Lightning、Codex 顯示 Code；未收到 hook event 的 session 維持 `TerminalWindow`/`ChatCircleDots` pane icon，terminated session 保留 `SmileySad` tombstone（#413）

## [1.0.0-alpha.149] - 2026-04-17

### 介面調整

- **spa**：Status bar 主機名/session 名、Title bar 標題與右側按鈕統一採用 `text-text-secondary`，與 Activity bar icon 顏色一致（#400）
- **spa**：Settings `SettingItem` 外層改為 `items-start`，Sync 頁 Modules 區塊 label 不再被右側多行 checkbox 撐開垂直置中（#411）

## [1.0.0-alpha.148] - 2026-04-17

### 功能

- **spa**：Sync Now 接線 — `sync-actions.ts` 編排 `SyncEngine.pull → push` 並處理 ok / conflicts / error 三種結果
- **spa**：Import apply 接線 — 匯入檔案透過 one-shot provider 走 `engine.pull` 三方合併，不再停留於 stub
- **spa**：`useSyncStore.syncHostId` — Daemon provider 的目標 host（持久化）
- **spa**：Settings → Sync 新增 Sync Host dropdown（Daemon only），Sync Now / Import 顯示 busy / success / warn / error 狀態行

### 修復

- **spa**：衝突分支正確推進 `lastSyncedBundle` — 對 engine 已套用的非衝突 contributor 前進 baseline，避免下次 sync 以過時 ancestor 重比（issue #388）

### 測試

- SPA 新增 9 個 sync wiring 測試（syncNow / applyImport / partialBaseline 各 variant），全套 1638 tests 通過

## [1.0.0-alpha.147] - 2026-04-17

### 功能

- **spa**：Activity bar 寬窄雙模式 — 窄版維持現況（44px icon），寬版顯示 icon + workspace 名稱（預設 240px），可拖右邊界 resize（120-600 clamp）
- **spa**：Activity bar 底部 `CollapseButton` 切換寬窄；`tabPosition='left'`（Phase 2 上線後）時鎖定為寬版
- **spa**：`useLayoutStore` 新增 `activityBarWidth` / `tabPosition` / `activityBarWideSize` / `workspaceExpanded` state，持久化到 `purdex-layout` 並透過既有 `syncManager` 跨視窗同步
- **spa**：`reconcileWorkspaceExpanded` GC — workspace 刪除時清除 stale expanded 狀態，保留 `home` 保留鍵
- **spa**：`healLayoutInvariant` rehydrate self-heal — 自動修復 `{narrow, left}` 無效組合
- **spa**：Resize handle 新增 `onResizeEnd` 支援，拖曳期間使用 ephemeral state，釋放時才 commit 避免 localStorage 風暴
- **spa**：i18n — `nav.collapse_activity_bar` / `nav.expand_activity_bar` / `nav.collapse_locked_tooltip`

### 重構

- **spa**：`ActivityBar` 拆為協調者 + `ActivityBarNarrow`（既有行為）+ `ActivityBarWide`（新）
- **spa**：共用 `ActivityBarProps` type 抽到 `activity-bar-props.ts`

### 測試

- SPA 新增 54 個 layout 測試（useLayoutStore、CollapseButton、ActivityBar coordinator、Narrow/Wide 變體）

## [1.0.0-alpha.146] - 2026-04-17

### 功能

- **spa**：SyncEngine — pluggable module contributor registry + SyncBundle serialize/deserialize + 三方比對衝突偵測
- **spa**：7 個 SyncContributor — workspaces、hosts（排除 auth token）、preferences、layout、quick-commands、i18n、notification-settings
- **spa**：3 個 SyncProvider — Manual（匯出/匯入）、Daemon（REST push/pull）、File（iCloud/Syncthing 同步資料夾）
- **daemon**：Sync module — SQLite 儲存（groups、canonical bundle、history、pairing codes）+ 9 個 HTTP endpoints
- **spa**：Sync Settings UI — provider 選擇器、module checkboxes、匯出/匯入、sync status

### 測試

- SPA 新增 182 個 sync 測試（15 test files），全套 1626 tests 無回歸
- Go daemon 新增 5 個 sync store 測試

## [1.0.0-alpha.145] - 2026-04-16

### 修復

- **spa**：補上 `settings.section.editor_buffers` i18n 翻譯（zh-TW「編輯器暫存」、en "Editor Buffers"）

## [1.0.0-alpha.144] - 2026-04-16

### 功能

- **spa**：Tiptap v3 WYSIWYG — Markdown 檔案支援 raw/WYSIWYG 雙模式切換（lazy load）
- **spa**：Monaco Diff View — 未存檔變更的 side-by-side diff 比較
- **spa**：Image/PDF Preview Pane — 新增 `image-preview` / `pdf-preview` pane kind
- **spa**：外部變更偵測 — tab focus 時比對 stat，clean buffer 靜默 reload
- **electron**：LocalBackend — IPC fs handler（home dir sandbox + realpath）+ LocalBackend class
- **spa**：BufferListSection — Settings 頁面 in-app buffer 管理

### 修復

- **spa**：修正 Tiptap v3 `setContent` API（v2 三參數 → v3 options object）
- **spa**：PdfPreviewPane iframe 加 `sandbox=""` 防止 XSS
- **spa**：`contentMatches` 補上 image/pdf preview daemon hostId 檢查
- **spa**：`markSaved` 新增 stat 參數，save 後更新 `lastStat` 避免無謂 re-read
- **electron**：`validatePath` 改用 `realpath()` 防止 symlink bypass
- **spa**：TiptapEditor 加 `internalUpdateRef` 防止 content sync thrashing
- **spa**：BufferListSection `handleDelete` 加 error handling

### 測試

- 新增 image/pdf preview `contentMatches` 測試（8 cases）
- 新增 `LocalBackend` 測試（11 cases）
- 新增 `markSaved` stat 更新測試（2 cases）

（#380）

## [1.0.0-alpha.143] - 2026-04-16

### 功能

- **spa**：`WorkspaceIconPicker` 和 `WorkspaceDeleteDialog` 加入 Escape 鍵關閉支援，與其他 overlay 元件行為一致（#195, #378）

## [1.0.0-alpha.142] - 2026-04-16

### 修復

- **spa**：`useUISettingsStore` 新增 `onRehydrateStorage`，啟動時自動將 WebGL renderer 下的 `keepAliveCount` clamp 至上限 6，避免既有高值設定超出 GPU context 限制（#188, #377）

## [1.0.0-alpha.141] - 2026-04-16

### 修復

- **spa**：`useRelayWsManager` useEffect cleanup 後，in-flight `fetchWsTicket` promise 不再建立遊離 WS 連線；加入 `cancelled` flag 阻止 cleanup 後的 `.then()` 執行（#173, #376）

## [1.0.0-alpha.140] - 2026-04-16

### 雜務

- **daemon**：`cmd/pdx/quick.go` 更名為 `pairing_init.go`，名稱與實際職責一致
- **spa**：刪除 dead i18n keys（`hosts.pairing_connecting`、`hosts.saving`）
- **spa**：AddHostDialog IP/port/token 輸入加 `.trim()` 防止空白造成 URL 錯誤
- **spa**：AddHostDialog 加 `aria-labelledby` 改善無障礙
- **docs**：修正 plan doc stale ref（`internal/core/base58` → `internal/pairing/base58`）及 spec L75 文字（#166, #375）

## [1.0.0-alpha.139] - 2026-04-16

### 修復

- **spa**：Workspace 自動命名改用 `nextWorkspaceName()` 尋找最小未使用的 `Workspace N`，避免刪除 workspace 後新建時產生重複名稱（#199, #374）

## [1.0.0-alpha.138] - 2026-04-16

### 修復

- **spa**：PaneHeader swap menu 加入 `useClickOutside` 和 Escape 鍵關閉機制，行為與 TabContextMenu 一致（#249, #373）

## [1.0.0-alpha.137] - 2026-04-16

### 修復

- **spa**：`checkHealth` 在 HTTP 200 但 `res.json()` 拋出 SyntaxError 時，不再錯誤歸類為 `refused`（L2），改為正確回傳 `connected`；新增 inner try-catch 包住 JSON parse，失敗時 early return 不進入 Phase 2（#372）

## [1.0.0-alpha.136] - 2026-04-16

### 重構

- **spa**：`useLayoutStore` 新增 `partialize` 明確持久化欄位 + `reconcileViews()` 啟動時驗證 views 有效性、清除 stale ID、首次安裝自動填入 defaults；移除 `main.tsx` 的 ad-hoc `hasAnyView` 邏輯（#370）

## [1.0.0-alpha.135] - 2026-04-16

### 修復

- **dev**：設定頁檢查更新觸發的自動編譯，改為先執行 `pnpm install --frozen-lockfile` 與 icon 生成，再跑 `electron-vite build`；同時改善步驟化錯誤訊息，避免缺依賴時反覆編譯失敗（#369）

## [1.0.0-alpha.134] - 2026-04-15

### 測試

- **daemon**：為 files module 新增 Go 測試（9 個案例），覆蓋 handleList 所有程式路徑（#367）

## [1.0.0-alpha.133] - 2026-04-15

### 重構

- **spa**：將 `SidebarRegion` 型別從 `types/tab.ts` 搬移至 `types/layout.ts`，釐清 layout 系統與 tab 系統的型別邊界（#366）

## [1.0.0-alpha.132] - 2026-04-15

### 修復

- **electron/spa**：Home 模式下封鎖 Cmd+Alt+Up/Down 工作區切換；新增 Cmd+Alt+0 跳至 Home（#364）

## [1.0.0-alpha.131] - 2026-04-15

### 新增

- **spa**：Editor Module Plan A — Monaco 基礎編輯器 + FS 抽象層（#359）
  - `FsBackend` 介面 + `InAppBackend`（in-memory）+ backend registry
  - `file-opener-registry` 解耦檔案來源與開啟行為
  - `module-registry` `pane` → `panes` 遷移，支援多 pane kind
  - `editor` PaneContent kind + pane utilities 更新
  - `useEditorStore` buffer store（runtime，不 persist）
  - `MonacoWrapper`（⌘S + cursor tracking）+ `EditorToolbar` + `EditorStatusBar`
  - `EditorPane` 主元件：載入 → buffer → 編輯 → 存檔全流程
  - New Tab 入口：「新增檔案」+「新增 Markdown」

## [1.0.0-alpha.130] - 2026-04-15

### 修復

- **spa**：Icon picker grid 從固定 8 欄改為 ResizeObserver 動態計算欄數，填滿容器寬度（#358）

## [1.0.0-alpha.129] - 2026-04-15

### 修復

- **electron**：`electron:build` 前置執行 `generate-icon-data.mjs`，確保 bundled SPA 包含 icon weight JSON（#357）
- **spa**：移除 Workspace Settings 頁面的重複 Style weight switcher，改由 IconPicker 統一管理（#357）
- **agent**：上傳檔案注入從 `send-keys` 改為 `paste-buffer -p`（bracketed paste），讓 CC 自動偵測圖片路徑並 chip 化為 `[Image #N]`；使用具名 buffer 避免並發競態（#357）

## [1.0.0-alpha.128] - 2026-04-14

### 修復

- **electron**：啟動預設載入 bundled SPA，避免 dev server 模組解析問題導致 crash 白屏；mini-browser 同步修正（#356）
- **chore**：移除殘留的 `spa/pnpm-lock.yaml`（pnpm workspace 只需 root lockfile）

## [1.0.0-alpha.127] - 2026-04-14

### 修復

- **spa**：standalone tab 聚焦時 Home badge 正確顯示其他 standalone tabs 的未讀數，排除聚焦 tab 本身的計數（#253, #355）

## [1.0.0-alpha.126] - 2026-04-14

### 修復

- **spa**：browser tab 插入位置修正 — workspace 排序現在正確使用 `insertTab`（PR #233 regression），並改為插入在右側最近 browser tab 之後（#353）

## [1.0.0-alpha.125] - 2026-04-14

### 重構

- **spa**：拆分 `store.test.ts`（611 行）為 `store-workspace.test.ts`（194 行）+ `store-tabs.test.ts`（431 行），51 tests 完整保留（#254, #354）

## [1.0.0-alpha.124] - 2026-04-14

### 修復

- **daemon**：`pdx setup` 在 daemon 不可達時加入 local fallback，直接呼叫 provider hook 安裝邏輯，不再強制要求 daemon 運行（#255, #352）

## [1.0.0-alpha.123] - 2026-04-14

### 修復

- **spa**：bump `useAgentStore` persist version 2→3，確保舊用戶的 `tabIndicatorStyle` 重設為新預設 `replace`（#268, #351）

## [1.0.0-alpha.122] - 2026-04-14

### 修復

- **spa**：TitleBar 標題改用 `max-w-[calc(100%-26rem)]` 取代固定 `px-20` padding，防止標題在極端情況與右側 layout 按鈕重疊（#267, #350）

## [1.0.0-alpha.121] - 2026-04-14

### 修復

- **spa**：`ModuleConfigSection` boolean toggle 改用 `ToggleSwitch` 元件，補齊 `role="switch"`、`aria-checked`、`type="button"` 等 a11y 屬性；text/number 欄位加上 `htmlFor`/`id` label 關聯（#258, #348）

## [1.0.0-alpha.120] - 2026-04-14

### 修復

- **spa**：`FileTreeWorkspaceView` 在 `workspaceId` 為 undefined（standalone tab 模式）時，不再渲染無法使用的 projectPath 表單，改為顯示「請先選擇 Workspace」提示（#257, #346）

## [1.0.0-alpha.119] - 2026-04-14

### 功能

- **daemon**：Terminal relay WS 加入 ping/pong keep-alive（30s interval, 10s pong timeout），防止 proxy/firewall 靜默斷開閒置連線（#160, #345）

## [1.0.0-alpha.118] - 2026-04-14

### 修復

- **spa**：Terminal WS `canReconnect` gate 加入 host 存在性檢查，防止 host 刪除後 ghost reconnect（#159, #344）

## [1.0.0-alpha.117] - 2026-04-14

### 效能

- **daemon**：`GetTmuxInstance()` 和 `getTmuxVersion()` 加入 3s exec timeout，防止 tmux server 異常時 `/api/info` handler 阻塞（#154, #343）

## [1.0.0-alpha.116] - 2026-04-14

### 功能

- **spa**：SessionPanel / SessionSection 的 host header 支援點擊收合/展開，多 host 時以 CaretDown/CaretRight 指示狀態，active host 不可收合（#144, #342）

## [1.0.0-alpha.115] - 2026-04-14

### 功能

- **daemon+spa**：Upload 暫存目錄路徑可在 UploadSection 中編輯，透過 daemon config 持久化（#340）

### 修復

- **daemon**：upload handler 的 `uploadDir` 讀寫加 mutex 保護，防止 config 變更時的 data race（#340）

## [1.0.0-alpha.114] - 2026-04-14

### 重構

- **spa**：提取 register-modules 匿名 pane wrapper 為命名元件（#338）

## [1.0.0-alpha.113] - 2026-04-14

### 修復

- **spa**：Workspace name input 加入 `maxLength=64` 防止超長名稱（#337）

## [1.0.0-alpha.112] - 2026-04-14

### 修復

- **spa**：ActivityBar badge 數字超過 99 時截斷為 `99+`（#336）

## [1.0.0-alpha.111] - 2026-04-14

### 功能

- **spa**：RenamePopover 新增 client-side session name 驗證（#335）
  - 即時格式檢查，鏡像 daemon 的 `^[a-zA-Z0-9_-]+$` 正則
  - 抽取 `isValidSessionName()` utility 至 `lib/session-name.ts`
  - Legacy session name 不觸發 popover 打開即顯示錯誤
  - 新增 i18n key（en + zh-TW）

## [1.0.0-alpha.110] - 2026-04-13

### 重構

- **daemon**：Probe Chain 三層探測架構取代舊 CC Detector（#334）
  - Liveness 層：process name + child process + content fallback，統一 CC/Codex 偵測
  - Readiness 層：ReadinessChecker interface，CC/Codex 各自實作狀態辨識
  - Activity 層：CapturePaneContent hash diff 偵測畫面變化，解決黃燈卡住問題
  - 刪除 `cc.Detector`、`cc.CCDetector` interface、`codex.detector`
  - Stream orchestrator 改用 `IsAliveFor` + `CheckReadiness` 組合

## [1.0.0-alpha.109] - 2026-04-13

### 功能

- **spa**：Icon 系統重構 — React.lazy + 1,445 chunks 改為 SVG path data 架構（#333）
  - Build-time 腳本從 `@phosphor-icons/core` 提取 SVG path，產生 6 個 per-weight 靜態 JSON
  - `icon-path-cache` 按需 fetch + 記憶體快取 + concurrent dedup + 失敗重試
  - `WorkspaceIcon` 同步 SVG 渲染，消除 Suspense 閃爍
  - `WorkspaceIconPicker` 改用 Fuse.js 模糊搜尋（支援 tags/categories）+ TanStack Virtual 虛擬捲動
  - Picker 新增 weight toggle UI，支援 6 種 Phosphor weight 即時預覽
  - `IconWeight` 從 3 種擴充為全部 6 種（bold/regular/thin/light/fill/duotone）
  - Build 產出：JS 檔案 1,446 → 1，dist 9.0MB → 6.1MB，main bundle 453KB → 448KB gz

## [1.0.0-alpha.108] - 2026-04-13

### 測試

- **spa**：新增 SortableTab 測試 — onPointerDown focus prevention、data-tab-id attribute、onSelect/onClose 互動（#211）

## [1.0.0-alpha.107] - 2026-04-13

### 功能

- **spa**：Workspace tooltip 顯示 unread 數量與 agent 狀態，與 aria-label 一致（#228）

### 修正

- **spa**：修正 active workspace 的 aria-label 錯誤包含 agent status（#228 review 發現）

## [1.0.0-alpha.106] - 2026-04-13

### 功能

- **spa**：RenamePopover 新增垂直 viewport clamping — 下方空間不足時翻轉到 anchor 上方，上下皆不足時 clamp 到頂部（#212）

## [1.0.0-alpha.105] - 2026-04-13

### 功能

- **daemon + spa**：Session list 即時同步 — SPA 新增 `useSessionWatch()` ref-counted polling hook，daemon `handleList` 新增 1s TTL debounce cache（#128）

## [1.0.0-alpha.104] - 2026-04-13

### 測試

- **spa**：補充 HandoffButton `agentStatus='error'` 和 `'waiting'` 測試案例（#125）

## [1.0.0-alpha.103] - 2026-04-13

### 修正

- **daemon**：`Stop()` 取消進行中的 build goroutine，防止 daemon restart 時產生並行 build（#99）

## [1.0.0-alpha.102] - 2026-04-13

### 功能

- **daemon**：dev auto-build 新增 5 分鐘逾時，防止 electron-vite 卡住導致 building 狀態永久鎖定（#97）

## [1.0.0-alpha.101] - 2026-04-13

### 重構

- **spa**：拆分 `useTabStore.test.ts` (517→343 行)，獨立 `terminated` 和 `migration` 測試檔案（#213）

## [1.0.0-alpha.100] - 2026-04-13

### 修正

- **electron**：merge window list 在新視窗 SPA 未載入時顯示 'Purdex' fallback 而非空白按鈕（#220）

## [1.0.0-alpha.99] - 2026-04-13

### 重構

- **spa**：建立 `closeTab()` helper 統一 locked guard 和 `destroyBrowserViewIfNeeded` 呼叫，修��� WorkspaceSettingsPage BrowserView 洩漏（#217）

## [1.0.0-alpha.98] - 2026-04-13

### 測試

- **electron**：新增 `keybindings.ts` 19 個單元測試 + vitest 測試基礎設施（#83）

## [1.0.0-alpha.97] - 2026-04-13

### 修正

- **daemon**：`handleDownload` 改為 buffer tar.gz 後才送出，Walk 錯誤時回傳 HTTP 500 而非產出損壞的 tar（#82）

## [1.0.0-alpha.96] - 2026-04-13

### 修正

- **daemon**：dev update 改用 config `repo_root` 欄位取代 `os.Getwd()`，daemon 從非 repo 目錄啟動時不再失效（#79）

## [1.0.0-alpha.95] - 2026-04-13

### 修正

- **LocaleEditor / ThemeEditor**：編輯 custom entry 時就地更新，不再每次 Save 建立重複項目（#74）

## [1.0.0-alpha.94] - 2026-04-13

### 變更

- **App icon**：符合 Apple HIG 標準（留白 100px、圓角 185.4px、陰影 Y12 blur28 @1024）
- **Per-arch build**：`scripts/build-electron.mjs` 按 arch 切換 icon 打包
- **i18n**：補齊 `settings.section.modules` 翻譯
- **ActivityBar**：Home 按鈕使用 Purdex 透明 logo

## [1.0.0-alpha.93] - 2026-04-12

### 修正

- **App icon**：改用白色圓角背景版，ActivityBar 保留透明版（`logo-transparent.png`）
- **i18n**：補齊 `settings.section.modules` 翻譯（Modules / 模組）
- **Legacy hooks**：清除舊 tbox hook 殘留，不做向下相容

## [1.0.0-alpha.92] - 2026-04-12

- chore: rename cleanup + Purdex logo (#315)

### 變更

- **Go 內部符號**：`tboxPath`/`makeTboxEntry`/`isTboxCommand` 等全部更名為 `pdxPath`/`makePdxEntry`/`isPdxCommand`
- **App 圖示**：PWA icons、macOS .icns、favicon 全部替換為 Purdex logo
- **ActivityBar**：Home 按鈕使用 Purdex logo 取代 SquaresFour icon
- **Maskable icon**：加入紫色背景 + 70% safe zone padding
- **型別安全**：新增 `SplitLayout` 型別 + `isSplit` type guard，移除 `PaneLayoutRenderer` 的 unsafe `as` cast
- **測試**：新增 `isGrid4` 6 個單元測試

## [1.0.0-alpha.91] - 2026-04-12

- refactor: brand rename from tmux-box to Purdex (#308, #309, #310, #311, #312)

### 變更

- **專案更名**：tmux-box → Purdex，CLI binary tbox → pdx
- **Go module**：`github.com/wake/tmux-box` → `github.com/wake/purdex`
- **Config 路徑**：`~/.config/tbox/` → `~/.config/pdx/`
- **環境變數**：`TBOX_TOKEN` → `PDX_TOKEN`、`TBOX_DEV_UPDATE` → `PDX_DEV_UPDATE`
- **tmux session channel**：`tbox_sess_evt` → `purdex_sess_evt`
- **Electron**：appId `dev.wake.purdex`、productName `Purdex`

## [1.0.0-alpha.90] - 2026-04-12

- feat: daemon background mode + crash log + reconnect error clear (#307)

### 新增

- **`tbox start/stop/status` 子命令**：daemon 可背景啟動，使用 `flock` PID file 管理生命週期，`stop` 以 SIGTERM 優雅關閉（30 秒 timeout + SIGKILL fallback）
- **Crash log**：`runServe` 加 panic recover defer，寫入 `~/.config/tbox/logs/crash-YYYYMMDD-HHMMSS.log`，含 secret redaction（Authorization header、purdex\_/tbox\_ token、cfg.Token）
- **Logs 子頁**：per-host Logs sub-page 顯示 Daemon Log（`/api/logs/daemon`）+ Crash Logs（`/api/logs/crash`），含手動 refresh + offline 狀態

### 修正

- **Reconnect 後 testResult 殘留**：`OverviewSection` 加 transition-aware `useEffect`，只在 status 從非 connected 轉為 connected 時清除 stale 錯誤訊息，避免 `manualRetry()` 循環清掉成功 pill

### 追蹤

- #305 — safeGo helper for cross-goroutine panic recovery
- #306 — HTTP recover middleware

## [1.0.0-alpha.89] - 2026-04-12

- fix(electron): restore renderer focus when backgrounding BrowserView (#301)

### 修正

- **切回 Terminal tab 後 terminal 無法 auto-focus**：使用者點過 Browser tab 的 `WebContentsView` 內容後，OS 鍵盤 focus 會留在那個 webContents；切回 Terminal tab 時 `BrowserPane` unmount → `closeBrowserView` → `BrowserViewManager.deactivate()` 只把 view 移到 off-screen，沒把 focus 交回主視窗 renderer，導致 `TerminalView` 在 visible-effect 中呼叫的 `termRef.current.focus()` 形同無效（DOM element focus 撈不到鍵盤輸入），使用者必須手動點一下 terminal 才能恢復輸入。修法在 `deactivate()` 移 off-screen 後呼叫 `entry.window.webContents.focus()` 把鍵盤 focus 交回 host renderer；以 `entry.window.isFocused()` 守衛避免 multi-window 場景搶奪其他 window 的 focus。後續在 #302/#303/#304 追蹤 destroy/discard 路徑、pop-out 流程、反向 activate focus 等延伸問題

## [1.0.0-alpha.88] - 2026-04-12

- revert: undo ineffective TitleBar cursor-pointer attempts (#296, #297) (#299)

### 回退

- **TitleBar 右側按鈕 pointer cursor 問題無法用 CSS 解決**：`#296`（把 `-webkit-app-region: drag` 移到 flex spacer）與 `#297`（加 `relative z-10` + `cursor-pointer!` important）都已用 Electron DevTools 驗證對 macOS OS 游標毫無影響 — computed cursor 已經是 `pointer !important`，問題出在 Electron 上游（[electron/electron#5723](https://github.com/electron/electron/issues/5723)、[#21632](https://github.com/electron/electron/issues/21632)）：`titleBarStyle: 'hiddenInset'` 下 NSWindow 仍保留頂部 ~38px 的 title bar 區域並攔截 Chromium 的 cursor update，但 click event 正常。將 `spa/src/components/TitleBar.tsx` 回退到 `#296` 之前的狀態，避免保留無效的 dead code；真正的修復（結構性繞開，仿 VSCode 把按鈕放到 tracking zone 以外）改由 #300 追蹤，標 `pending` 暫擱

## [1.0.0-alpha.87] - 2026-04-12

- fix(electron): restore Toggle Developer Tools in View menu (#298)

### 修正

- **DevTools 快捷鍵失效**：`electron/keybindings.ts:buildMenuTemplate` 的 View menu 只收錄 `byCategory.get('View')` 的自訂項目，完全沒有 `role: 'toggleDevTools'`，自訂 menu 一旦 `Menu.setApplicationMenu` 取代預設 menu，Electron 內建的 `Cmd+Option+I` 就一併遺失。在 View submenu 尾端補上 separator + `{ role: 'toggleDevTools' }`，Electron 自動綁回預設 accelerator，`Cmd+Option+I` 與 View → Toggle Developer Tools 都可打開 DevTools

## [1.0.0-alpha.86] - 2026-04-12

- fix(spa): force cursor pointer on TitleBar buttons (#297)

### 修正

- **TitleBar 按鈕 cursor pointer 強制生效**：#296 只把 `-webkit-app-region: drag` 從容器移到 spacer，但 absolute `inset-0 pointer-events-none` 的 title 層在 paint order 上仍高於未定位的 buttons（CSS positioned siblings 不論 DOM 順序皆覆於 non-positioned siblings 上），Electron/Chromium 的 cursor fall-through 在此情境下不穩。將 buttons 容器升入 positioned stacking layer（`relative z-10`）讓它確實畫在 title 之上、重新加回 `WebkitAppRegion: 'no-drag'` 雙保險、每顆 button 改用 Tailwind v4 的 `cursor-pointer!`（產生 `!important`）硬覆寫任何競爭的 cursor 規則，disabled 態對應 `disabled:cursor-default!`

## [1.0.0-alpha.85] - 2026-04-12

- fix(spa): restore cursor pointer on TitleBar buttons (#296)

### 修正

- **TitleBar 右側按鈕無 pointer cursor**：原本 `spa/src/components/TitleBar.tsx` 最外層容器帶 `-webkit-app-region: drag`，Chromium 對 drag region 的所有子孫強制使用預設游標，即便按鈕 container 已設 `no-drag` 也無法覆寫 `cursor-pointer`。改為最外層不帶 drag、另外插入一個 `flex-1 self-stretch` spacer 承擔 `WebkitAppRegion: 'drag'`，讓右側按鈕脫離 drag region 祖先鏈；視窗拖曳行為由 spacer 提供，置中標題仍以 `absolute inset-0 pointer-events-none` 跨滿整條 bar

## [1.0.0-alpha.84] - 2026-04-12

- fix(session): guard handleCreate with mutex against same-name race (#61) (#294)

### 修正

- **#61 handleCreate TOCTOU race**：`internal/module/session/handler.go:handleCreate` 原本從 `HasSession` 到 `SetMeta` 完全無鎖，兩個同名 POST 可以同時通過 duplicate check 然後各自呼叫 `NewSession`，在 FakeExecutor 上重現為 `sessionOrder` 重複 entry（確定性），在真實 tmux 上會讓第二次呼叫失敗並回傳 500 而非 409。新增 `SessionModule.createMu sync.Mutex`，`handleCreate` 於輸入驗證後 `Lock + defer Unlock`，涵蓋整段 `HasSession → NewSession → ListSessions → SetMeta` critical section。Watcher goroutines 不取此鎖，可能看到中間狀態但下一輪會自動修正（已追蹤為 #295）
- **test infra — `:memory:` connection pool pinning**：`internal/store/meta.go:OpenMeta(":memory:")` 新增 `db.SetMaxOpenConns(1)`。Go `database/sql` 的 pool 可能對同一 DSN 開多條連線，而每條 `:memory:` 連線各自是一個獨立的空 DB，先前讓並發測試隨機打到 empty DB 出現 `no such table: session_meta`。只對 `:memory:` 路徑生效，production 使用 `cfg.DataDir/meta.db` 不受影響
- **新增並發回歸測試**：`TestHandlerCreateSessionConcurrentSameName` 以 close-on-start barrier 釋放 N=100 個 goroutine 同時 POST 相同 session name，斷言恰好 1 個 201、99 個 409、`ListSessions()` 長度 == 1；於 `-race -count=20` 下穩定通過

## [1.0.0-alpha.83] - 2026-04-12

- fix: rollback in-memory config on writeConfig failure (#28) (#293)

### 修正

- **#28 atomic config update**：`internal/core/config_handler.go` 的 `handlePutConfig` 原本先更新記憶體 `c.Cfg`、再寫檔；若 `config.WriteFile` 失敗則回 500 但記憶體已變，造成執行中的 daemon 使用新設定、重啟後讀回舊設定的不一致。改在 mutation 前 `snapshot := *c.Cfg`，寫檔失敗時 `*c.Cfg = snapshot`（透過指標寫回，保留其他 goroutine 持有的 `c.Cfg` 指標身分），整段都在 `CfgMu.Lock` 範圍內；`NotifyConfigChange` 仍只在成功分支觸發
- **新增 rollback 回歸測試**：`TestPutConfigRollsBackOnWriteFailure` 將 `CfgPath` 指向「父路徑是一般檔案」的位置，觸發 `MkdirAll` ENOTDIR（跨平台可靠、無需 chmod cleanup），驗證 500 + 錯誤訊息含 "not a directory"、Stream/Detect/Terminal 全數 rollback、callback 未觸發、`c.Cfg` 指標身分未變、rollback 後再發一次正常 PUT 仍可成功
- **新增 invariant 註解**：snapshot 與 mutation block 各有一段註解明確要求所有 mutation 必須整個欄位指派，禁止 `append` 或 map in-place 寫入（shallow snapshot rollback 正確性的前提）

## [1.0.0-alpha.82] - 2026-04-12

- fix: lock CfgMu when reading sizing mode in HandleTerminalWS (#26) (#292)

### 修正

- **#26 race fix**：`internal/module/session/service.go` 的 `HandleTerminalWS` 讀取 `m.core.Cfg.Terminal.GetSizingMode()` 時未持 `CfgMu.RLock`，與 `handlePutConfig` 在 `CfgMu.Lock` 下寫入相同欄位產生 data race。改採 snapshot pattern：read lock 內取值後立即解鎖，仿 `internal/module/stream/handler.go:177-182` 既有 pattern
- **新增 race 回歸測試**：`TestHandleTerminalWS_NoConfigRace`（50 reader × 20 iterations + 1 writer），於 `go test -race` 下驗證；修復前命中 DATA RACE，修復後通過。Test cleanup 用 `sync.Once + t.Cleanup` 確保 panic 時 writer goroutine 不 leak

## [1.0.0-alpha.81] - 2026-04-11

- refactor: App.tsx 拆分 — 提取 hooks + 具名 callback (#282)
- fix: SubagentDots 燈號殘留 (5 root cause + 3 輪 review follow-up) (#283)

### 重構

- **App.tsx 409 → 286 行**：提取 `GlobalUndoToast` 為獨立元件、`useElectronIpc` hook（收納 4 個 IPC effect）、`useWorkspaceWindowActions` hook（workspace tear-off/merge）
- **`openSingletonAndSelect`**：在 `useTabWorkspaceActions` 新增 helper 統一 4 處 singleton tab 開啟模式，支援可選 `wsId` 參數
- **Inline lambda 全面具名化**：JSX 不再包含業務邏輯，所有 handler 提為 `useCallback`
- Closes #202, #219, #225, #237, #243, #261, #281

### 修正

- **#231**：`onWorkspaceReceived` catch 範圍縮窄，僅捕 `JSON.parse` 錯誤避免靜默吞掉 store mutation 錯誤
- **`openWsSettings` cross-workspace**：修正右鍵非 active workspace 的 settings 時 tab 被插入錯誤 workspace
- **Bug 0 (主因)**：移除 `Subagents` 的 `omitempty` tag，最後一筆 `SubagentStop` 永遠送 `subagents:[]`，前端不再卡住
- **Bug 0b**：新增 `RenameSessionAtomic(old, new, doRename)` API，把 tmux + DB + in-memory rename 包進單一 lock，修復 rename 後 hook 用新名查空 map 的問題
- **Bug 1**：新增 `useAgentStore.clearSession(hostId, code)` action，session-closed 時集中清理
- **Bug 2**：`checkAliveAll` orphan 分支改用 `tmux.HasSession()` 二次確認，防止 `ListSessions` 暫時性失敗時誤刪
- **Bug 3**：`SubagentStart` guard 改用 `events.Get + DeriveStatus(StatusClear)` 持久化 DB state，修復 daemon restart + compact `SessionStart` 邊界
- **rename rollback**：`renameSessionAtomic` 改為 DB-first，tmux 失敗時 best-effort 回滾 DB，確保三方一致

## [1.0.0-alpha.80] - 2026-04-10

fix: SubagentDots 相位同步 + terminal reconnect 自動恢復 (#279)

### 修正

- **SubagentDots 動畫相位同步**：`useMemo` dependency 改為 `[clamped]` + key 強制 remount，修正新 subagent dot 與既有 dot 相位不同步的雜亂閃爍
- **Terminal reconnect gate 持續重試**：`canReconnect()` gate 回 false 時改為固定間隔輪詢（不累積 backoff），host 恢復後自動重連
- **connectWithTicket 殭屍 WS 防護**：await getTicket 後加 `closed` guard，防止 unmount 後建立多餘 WebSocket

## [1.0.0-alpha.79] - 2026-04-10

fix: topbar cursor pointer + host 子分頁切換保留 (#276)

### 修正

- **TitleBar 按鈕 cursor pointer**：明確加 `cursor-pointer` class 修正 Electron drag region 覆蓋 cursor 樣式的問題
- **Host 子分頁切換保留**：展開不同 host 時保留當前選中的 subPage，不再 hardcode reset 到 overview

## [1.0.0-alpha.78] - 2026-04-10

Quick Commands + Host Agents + tmux 精確匹配修正 (#269)

### 新增

- **Quick Commands Module**：可插拔指令快捷系統 — module registry `commands` extension point、QuickCommandStore（global + per-host persist）、`useCommands()` hook、QuickCommandMenu 下拉選單
- **POST /api/sessions/{code}/send-keys**：透過 `SendKeysRaw` 送指令到 tmux session
- **GET /api/agents/detect**：偵測 host 上 claude/codex CLI 安裝狀態（path + version）
- **Host > Agents 子頁面**：顯示各 agent CLI 的安裝狀態
- **Codex Hooks**：HOOK_MODULES 加入 CODEX_HOOKS，Hooks 頁面顯示 tmux/CC/Codex 三區

### 修正

- **tmux 精確匹配**：RenameSession / KillSession / send-keys target 加 `=` 前綴避免 prefix matching 誤判
- **handleRename 重名 409**：rename 前檢查目標名稱是否已存在
- **CC getLastTrigger**：加 `agent_type` 過濾，避免 codex event 被算進 CC

### 移除

- **Settings > Agent 區塊**：hooks 統一走 Host > Hooks 管理

## [1.0.0-alpha.77] - 2026-04-10

Sidebar / Panel View Management (#266)

### 新增

- **Region 管理 UI**：每個 sidebar/panel 可自行管理啟用哪些 view、拖曳排序
- **三個管理入口**：⚙ 按鈕（pinned header）、+ 按鈕（collapsed bar）、右鍵 context menu
- **RegionManager 元件**：替換 region 內容的管理畫面，@dnd-kit drag-to-reorder
- **RegionContextMenu 元件**：右鍵 checkbox 清單快速開關 view
- **View scope 三層模型**：ViewDefinition.scope 支援 `'system' | 'workspace' | 'tab'`
- **Layout Store 新 actions**：`addView`、`removeView`、`reorderViews`

### 改善

- **toggleVisibility 記憶狀態**：TitleBar toggle 隱藏後恢復時記住之前是 pinned 或 collapsed
- **Region 空狀態**：空 region 仍渲染管理入口（不再完全消失）
- **TitleBar**：始終顯示全部 4 個 region toggle 按鈕

### 移除

- `ViewDefinition.defaultRegion`（view 不再綁定特定 region）
- `getViewsByRegion()` 查詢函式（改為 `getAllViews()`）
- `WorkspaceSidebarState` 型別（未被使用）

## [1.0.0-alpha.76] - 2026-04-10

### 修正

- **Topbar 標題置中**：改用 absolute 定位，以完整視窗寬度置中，不受右側按鈕影響 (#262)
- **全域 cursor: pointer**：全域 CSS 規則讓所有 button、[role="tab"]、[role="button"] 預設顯示 pointer cursor (#263)
- **Workspace icon 拖曳範圍限制**：restrictToVertical modifier 增加 Y 軸邊界計算，限制拖曳在列表範圍內 (#264)

### 變更

- **Tab Indicator Style 設定隱藏**：從 Settings UI 移除選項，預設改為 replace，保留 store 架構

## [1.0.0-alpha.75] - 2026-04-10

### 修正

- **Session 建立重名 409**：`handleCreate` 建 session 前先 `HasSession` 檢查，回 409 Conflict + 明確訊息；`HasSession` 改用 `=` 前綴精確匹配避免 tmux prefix matching 誤判 (#260)
- **createRequest 加 Mode 欄位**：前端傳的 mode 不再被 JSON decoder 靜默忽略，新建 session 正確套用 terminal/stream (#260)
- **Workspace icon 拖曳排序恢復**：恢復在 status pill refactor 中遺失的 `@dnd-kit` DnD（`SortableWorkspaceButton`、`reorderWorkspaces` store action）；加防禦性 guard 防止 stale orderedIds 丟失 workspace (#260)
- **Tab bar + 按鈕位置**：移除 `normalTabsRef` 的 `flex-1`，+ 按鈕緊鄰最後一個 tab (#260)
- **前端錯誤訊息改善**：NewSessionDialog 顯示 response body 而非僅 HTTP status code (#260)

## [1.0.0-alpha.74] - 2026-04-10

### 修正

- **Topbar region toggle**：從展開/收合改為完全顯示/隱藏，新增 `toggleVisibility` action (#259)
- **Workspace module settings**：WorkspaceSettingsPage 加入 ModuleConfigSection，workspace 範圍模組設定現在可見 (#259)

## [1.0.0-alpha.73] - 2026-04-10

Agent Module Provider Pattern 重構 (#247)

### 新增

- **AgentProvider 介面**：capability-based composition（HookInstaller、HistoryProvider、StreamCapable），支援多 agent 擴充
- **Agent Registry**：thread-safe provider 註冊/查詢，hook event 路由依 agent_type 分派
- **CC Provider**：從散落的 cc/detect 模組整合為 `internal/agent/cc/`（detector、operator、history、hooks、status derivation）
- **Codex Provider**：新增 `internal/agent/codex/`（status derivation、process detection、hook installer）
- **NormalizedEvent**：後端推導 status/model/subagents，前端零 per-agent 邏輯
- **Per-agent hook 管理**：`GET/POST /api/hooks/{agent}/status|setup` 參數化 API
- **Agent icons**：`spa/src/lib/agent-icons.ts` icon + name mapping
- **AgentSection UI**：Settings 頁面顯示每個 agent 的 hook 安裝狀態與操作按鈕

### 改善

- **CLI `--agent` flag**：`tbox hook` 和 `tbox setup` 必須指定 agent type
- **useAgentStore 簡化**：移除前端 `deriveStatus`，store 只接收後端 pre-derived 狀態
- **Frontend agent-agnostic**：新增 agent 只需後端加 provider + 前端加一行 icon mapping

### 移除

- `internal/module/cc/`（整個目錄）— 合併至 `internal/agent/cc/`
- `internal/detect/`（整個目錄）— 合併至 `internal/agent/cc/`
- `internal/module/agent/cc_hooks.go` — 移至 `internal/agent/cc/hooks.go`
- 前端 `deriveStatus` 函式、`AgentHookEvent` 型別、`clearSubagentsForHost` action

## [1.0.0-alpha.72] - 2026-04-10

Sidebar/Panel/Pane 修正 + Module Config 系統 (#245)

### 新增

- **Module Config 系統**（#244）：Module 透過 registry 宣告 workspace/global 層級設定，Workspace 提供泛用 `moduleConfig` 儲存，Settings 頁面自動產生表單
- **Files Module 拆分**：workspace view（以 projectPath 為根）+ session view（placeholder，待 daemon cwd API）
- **Pane swap 功能**：PaneHeader 新增交換按鈕，可在同 tab 的不同 pane 間交換內容
- **Tab mergeToTab**：Tab 右鍵選單新增「加入 Tab 成為 pane」功能
- **TitleBar Region Toggle**：4 個 sidebar/panel 切換按鈕，僅在 region 有 view 時顯示
- **Global Module Config Store**：`useModuleConfigStore` 支援全域 module 設定持久化

### 改善

- **PaneSplitter 視覺**：hover 加寬、顏色加深、hit area 擴大
- **PaneHeader 視覺**：按鈕加大、邊框加強
- **Grid-4 水平聯動**：四宮格水平 splitter 同步 resize
- **Pane detach 位置**：彈出的 tab 插入到來源 tab 的下一位（而非尾端）
- **SidebarRegion props**：正確傳遞 region/workspaceId/hostId 給 view component
- **insertTab afterTabId**：workspace store 支援指定位置插入 tab

## [1.0.0-alpha.71] - 2026-04-10

Home 按鈕未讀指示修正 + standalone tab 關閉 scope 修正

### 修正

- **Home badge 未讀數**：Home 按鈕 badge 改用 `useWorkspaceIndicators` 計算未讀數，而非顯示 standalone tab 總數
- **Home status dot**：Home 按鈕新增 status dot indicator（running/waiting/error），與 workspace 按鈕一致
- **standalone tab 關閉 scope**：關閉 standalone tab 時，nextTab 候選範圍改為只包含其他 standalone tabs，避免跳到 workspace tab
- **standaloneTabIds memoize**：`useMemo` 穩定 `standaloneTabIds` 陣列引用，避免每次 render 重算 indicator

## [1.0.0-alpha.70] - 2026-04-09

Workspace 狀態指示器迭代

### 變更

- **Status dot**：workspace icon 狀態指示器從 3px 左側 pill 改為 5px 圓點，與 TabStatusDot 風格一致
- **Active 隱藏**：active workspace 不顯示狀態圓點（狀態已可在 tab bar 看到）
- **Aria-label 強化**：workspace 按鈕的 aria-label 現在包含 agent 狀態（running/waiting/error）
- **測試補強**：新增 4 個 status dot 測試（顯示/隱藏/waiting 靜態/aria-label）

## [1.0.0-alpha.69] - 2026-04-09

Module Layout Foundation (Plan 1+2) + Review 修正

### 新功能

- **Module Registry**：統一 pane + view 註冊系統（`module-registry.ts`），取代舊 `pane-registry.ts`
- **Layout Store**：4-region sidebar/panel 狀態管理（`useLayoutStore`），持久化到 `purdex-layout`
- **TitleBar**：Electron 標題列元件，含 traffic light safe zone + layout pattern 按鈕 placeholder
- **SidebarRegion**：可折疊/展開的 sidebar 容器，支援 view 切換 + 拖曳調整寬度
- **RegionResize**：拖曳調整 region 寬度的把手元件
- **App 佈局重構**：統一 TabBar 位置 + 4 SidebarRegion 整合

### 修正（Review）

- **RegionResize stale closure**：drag 時 `onResize` callback 使用 `useRef` 保持最新引用
- **syncManager 註冊遺漏**：`useLayoutStore` 現在正確註冊 syncManager，跨 tab/視窗同步
- **ViewDefinition icon 型別**：`icon` 從 `string` 改為 React component type，collapsed bar 渲染 Phosphor Icon
- **activeViewId fallback**：展開 region 時若 activeViewId 未設定，自動 fallback 到第一個 view
- **移除 `mode: 'default'`**：移除從未使用的 mode 值，RegionState 只保留 `'pinned' | 'collapsed'`
- **`side` → `resizeEdge`**：prop 更名為更清晰的語義
- **TitleBar layout 按鈕加 disabled**：Plan 3 前 placeholder 按鈕標記為 disabled
- **移除 `--app-region` 死碼**：TitleBar 移除無效的 CSS custom property
- **PaneLayoutRenderer 空 children 防護**：split layout children 為空時顯示 fallback
- **SidebarRegion 收合按鈕**：展開狀態新增 collapse button

## [1.0.0-alpha.68] - 2026-04-09

Workspace icon indicators — unread badge + status pill (PR #226)

## [1.0.0-alpha.67] - 2026-04-09

Tab 拖曳 / Rename CORS / 通知 GC / Tab 溢出 / 上傳階段修復 (PR #224)

### 修正

- **Active tab 無法拖曳**：dnd-kit 檢查 `nativeEvent.defaultPrevented` 會靜默中止拖曳；調整 `handlePointerDown` 順序，先呼叫 dnd-kit handler 再 `preventDefault()`
- **Rename session "Failed to fetch"**：CORS middleware 缺少 `PATCH` method，瀏覽器 preflight 被拒
- **通知點擊無反應**：Electron `Notification` JS wrapper 在 `show()` 後被 GC 回收（C++ 層不使用 `SelfKeepAlive`），用 `Set<Notification>` 保持強引用
- **Tab 溢出整個 app**：Electron title bar tab 容器缺少寬度約束；加入 `flex-1 min-w-0` 並讓 `normalTabsRef` 使用 `flex-1` + `min-content` 讓 tab 先縮減再捲動

### 新功能

- **上傳「輸入中…」階段**：圖片上傳流程新增 `typing` 狀態，完整流程為 `uploading → typing(1.5s) → done(3s) → dismiss`

## [1.0.0-alpha.66] - 2026-04-09

Browser UX 修復 — mini window theme + tab shortcuts (PR #223)

### 修正

- **Mini window toolbar 不可見**：獨立瀏覽器視窗現在正確初始化 theme（ThemeInjector + useThemeStore hook），toolbar 可見
- **Mini window Cmd+W**：獨立視窗現在可用 Cmd+W 關閉

### 新功能

- **Tab shortcut handler registry**：不同 tab type 可註冊各自的快捷鍵 handler，useShortcuts 作為 dispatcher
- **Browser 導航快捷鍵**：Cmd+[/] (back/forward)、Cmd+←/→ (macOS)、Cmd+R (reload)、Cmd+L (focus URL)、Cmd+P (print)
- **Print IPC**：新增 browser-view:print channel，支援 Cmd+P 列印

## [1.0.0-alpha.65] - 2026-04-09

Home tab 殘留修復 + workspace icon tooltip (PR #221)

### 修正

- **Home tab 殘留**：切回 Home 時若無 standalone tabs，`activeTabId` 現在正確清除為 null，不再殘留前一個 workspace 的 tab
- **Workspace icon tooltip**：ActivityBar workspace icon hover 即時顯示名稱（CSS tooltip），取代原生 title 延遲；改用 `aria-label` 避免雙重 tooltip

## [1.0.0-alpha.64] - 2026-04-09

新增 ⌘⇧H 快捷鍵開啟 Host 管理面板 (#178)

### 新功能

- **⌘⇧H 開啟 Hosts 面板** — 以 singleton tab 方式開啟 Host 管理頁面，與 ⌘, (Settings)、⌘Y (History) 操作模式一致

## [1.0.0-alpha.63] - 2026-04-09

Workspace 增強 — Ctrl+Tab、通知導航、workspace tear-off/merge (PR #218)

### 新功能

- **Ctrl+Tab / Ctrl+Shift+Tab** — 切換 tab 的替代快捷鍵（macOS 部分鍵盤設定可能衝突）
- **通知點擊切 workspace** — 點擊通知自動切換到含有該 tab 的 workspace（或回到 Home）
- **Workspace tear-off** — 右鍵 workspace context menu「獨立到新視窗」，整個 workspace 搬到新 Electron 視窗
- **Workspace merge** — 右鍵「合併到視窗」，將 workspace 合併到另一個已開啟的視窗
- **importWorkspace store action** — 跨視窗 workspace 傳輸，含 ID 去重

### 修正

- merge 視窗清單過濾當前視窗（避免 self-merge）
- IPC 呼叫成功後才清理 store（防止失敗時資料遺失）
- merge 目標消失時正確 throw（不再靜默失敗）
- workspace 接收端驗證 activeTabId 存在性
- tear-off/merge 後全域 activeTabId 與 workspace activeTabId 同步
- spa:ready listener 在視窗關閉時清除（防止洩漏）
- 禁止 tear-off 空 workspace（0 tabs guard）
- getWindows() IPC 加 .catch() 防止永久 Loading

## [1.0.0-alpha.62] - 2026-04-08

Workspace activeTabId 同步修正

### 修正

- **快捷鍵 tab 切換同步 workspace** — switch-tab-*、prev/next-tab、switch-workspace 快捷鍵現在正確同步 `ws.activeTabId`，切回 workspace 時恢復最後瀏覽的 tab
- **冗餘寫入優化** — `activateTab` helper 在值未改變時跳過 `setWorkspaceActiveTab`

## [1.0.0-alpha.61] - 2026-04-08

Close-tab workspace scoping 重構 + ActivityBar 間距 (PR #216)

### 重構

- **`closeTabInWorkspace` composite action** — 取代 hook 層 post-close 補丁，在 workspace store 一次完成 recordClose → removeFromWorkspace → closeTab → workspace-scoped active tab 選取（visitHistory 優先 → adjacent fallback）
- **`closeTab` 簡化** — 移除全域 tabOrder auto-select，只負責刪除 tab + 清理 visitHistory
- **5 個 caller 統一遷移** — useShortcuts、hooks.ts、TerminatedPane、WorkspaceSettingsPage、host-lifecycle
- **`destroyBrowserViewIfNeeded` 共用 helper** — 修正 useShortcuts close-tab 缺少 browser view cleanup

### 修正

- **`ws.activeTabId` 同步** — close-tab 後正確同步 workspace 的 activeTabId（修 PR #208 review issue）
- **`ws.activeTabId` 覆寫** — 關閉非 active tab 不再錯誤覆寫 workspace activeTabId
- **host-lifecycle undo 還原 workspace** — cascade delete undo 現在同時還原 tab 的 workspace 歸屬
- **host-lifecycle skipHistory** — cascade delete 不再污染「最近關閉」記錄

### 改善

- **ActivityBar 按鈕尺寸** — 32px → 30px + 容器 px-px，減少側邊欄擠壓

### 測試

- 1045 tests pass / lint clean / build OK

## [1.0.0-alpha.60] - 2026-04-08

URL history dropdown 對齊修正

### 修正

- **dropdown 位置** — URL 歷史下拉選單左邊界對齊 input 左側，不再包含 Globe icon 的空間

## [1.0.0-alpha.59] - 2026-04-08

Tab UX 改善 (PR #209)

### 新功能

- **Rename Session** — tab 右鍵選單新增 Rename Session，popover 出現在 tab 正下方 inline 編輯，API 失敗時顯示錯誤訊息
- **URL 歷史下拉** — new tab 頁面 URL 欄位帶出歷史紀錄，輸入時 auto-filter，鍵盤 ↑↓ 選擇，最多 100 筆持久化
- **Session 鍵盤導航** — Tab 鍵進入 session list，↑↓/jk 移動，Enter 選擇
- **瀏覽紀錄回退** — 關閉 tab 時回到上一個瀏覽的 tab（visitHistory stack），而非相鄰 tab

### 改善

- **New Tab 頁面** — browser URL 欄位移到最上方並自動 focus，移除 Memory Monitor 區段
- **Focus 保持** — 點擊已 active 的 tab 不搶走 content 區域的 focus

### 測試

- 1047 tests pass / lint clean / build OK

## [1.0.0-alpha.58] - 2026-04-08

Tab 操作 workspace 隔離 (PR #208)

### 修正

- **close-tab 跨 workspace 防護** — cmd+w 只能關閉當前 workspace 可見的 tab，空 workspace 時不會誤刪其他 workspace 的 tab
- **post-close scoping** — 關閉後若 closeTab 自動選了其他 workspace 的 tab，重設為當前 workspace 內的 tab 或 null

### 測試

- 1012 tests pass / lint clean
- 新增跨 workspace close-tab 和 reopen-closed-tab 測試

## [1.0.0-alpha.57] - 2026-04-08

Workspace UI 微調 (PR #207)

### 改善

- **Context menu 精簡** — 只保留 Settings，移除 rename/color/icon/delete（已在設定頁可用）
- **Settings sidebar 左邊距** — active 指示線不再緊貼 ActivityBar
- **Icon picker ring 修正** — 選中 icon 的紫色邊框不再被裁切

### 清理

- **移除 workspace 顏色系統** — 刪除 WorkspaceColorPicker、WorkspaceRenameDialog、WorkspaceChip、workspaceColorStyle、WORKSPACE_COLORS 等死碼（7 檔 302 行）
- Workspace interface 移除 `color` 欄位
- 清理 App.tsx ~80 行 dialog state

### 測試

- 1009 tests pass / lint clean

## [1.0.0-alpha.56] - 2026-04-08

Phase 11 — Workspace UI 改善 (PR #201)

### 新功能

- **Workspace 設定頁** — 前台式單頁設定（名稱編輯、Phosphor Icon picker、icon weight toggle、刪除）
- **Phosphor Icons Picker** — 8 分類精選 + 搜尋完整 1512 icon 庫，lazy loading per-icon chunk
- **Icon Weight** — bold / duotone / fill 三種風格切換
- **Empty workspace state** — 切換到無 tab workspace 時顯示空白引導頁
- **WorkspaceContextMenu** — 新增 Settings 項目
- **Vibrant color palette** — S=55-80% L=55-65% 取代舊 S=36% 色板（12 色）

### 改善

- **ActivityBar** — 系統配色（白前景/深背景）、active 紫色背景 + ring-purple-400
- **WorkspaceChip 移除** — tab bar 不再顯示 workspace 標題，上下文完全由 ActivityBar 提供
- **Tab recall fix** — 切換 workspace 時正確回到上次瀏覽的 tab（getState 取代 stale closure）
- **useRouteSync** — workspace-settings 路由補 setActiveWorkspace + insertTab

### Review 修正

- WorkspaceIcon rules-of-hooks — useMemo 移到 early return 前避免條件式 hook 呼叫
- WorkspaceIcon ErrorBoundary — icon import 失敗時 fallback 至文字而非 crash 整個 app
- WorkspaceSettingsPage delete — 濾除 settings tab、記錄 history、sync activeTab
- Context menu 點 Settings 後正確關閉
- 搜尋去重：curated icons 跨 category 重複時不再產生 React key 警告
- 修正無效 Phosphor icon 名稱（Tv → Television, Brackets → BracketsCurly）
- workspaceColorStyle JSDoc 修正

### 測試

- 997 tests pass / lint clean

## [1.0.0-alpha.55] - 2026-04-07

Browser Tab 強化 (PR #200)

### 新功能

- **Browser tab toolbar** — 導航按鈕（← → ↻/✕）、可編輯 URL 欄位、⋯ 更多選單
- **Mini browser 獨立視窗** — Shift+click 連結彈出獨立視窗，共用同一套 BrowserToolbar
- **Terminal 連結統一處理** — click 開新 browser tab、shift+click 開 mini browser（SPA fallback 為 window.open）
- **WebContentsView preload 注入** — 攔截頁面連結點擊，回報 shiftKey 給 main process
- **MiniWindowManager** — 管理 mini browser BrowserWindow 生命週期
- **browser-view-ipc.ts** — 集中管理所有 browser-view IPC handler
- **useBrowserViewState hook** — 訂閱 Electron state-update（URL、title、canGoBack、isLoading）
- **useBrowserViewResize hook** — 共用 ResizeObserver 邏輯
- **link-handler factory** — createLinkHandler 根據 platform + shiftKey 分派
- **URL 正規化** — normalizeUrl utility（自動補 https://、scheme 白名單）
- **Browser tab close → destroy** — 主動關閉走 destroy 路徑，tab 切換走 background

### 測試

- 1011 tests pass / lint clean

## [1.0.0-alpha.54] - 2026-04-07

Phase 10 — Workspace 強化 (PR #189, #190, #191)

### 新功能

- **Workspace 全自由制** — 移除預設 workspace，支援 0 workspace 模式，`activeWorkspaceId` 可為 null
- **Feature module 架構** — workspace 相關程式碼搬遷至 `features/workspace/`（store、hooks、components、lib）
- **insertTab store action** — 原子化操作 + singleton dedup（跨 workspace 移除重複 tab）
- **getVisibleTabIds 共用函式** — 純函式，含 Home mode 支援
- **insertTab 收斂** — 所有 `addTabToWorkspace + setWorkspaceActiveTab` 模式統一為 `insertTab`
- **WorkspaceDeleteDialog** — 刪除確認 UI + tab 勾選清單
- **右鍵選單 + Chip** — WorkspaceContextMenu + Titlebar WorkspaceChip
- **重新命名/顏色/圖示設定** — RenameDialog、ColorPicker、IconPicker
- **Electron 快捷鍵** — ⌘⌥1-9 位置切換 + ⌘⌥↑/↓ 循環切換
- **MigrateTabsDialog** — 首個 workspace 建立時詢問遷移既有 tab
- **Standalone Tabs Home 入口** — ActivityBar 頂部 Home 按鈕

### 修正

- deleteWs 後同步 activeTabId 到新 workspace
- Home 按鈕高亮條件修正
- MigrateTabsDialog Skip 後自動切回 Home
- prev-workspace 從 Home 出發跳到最後一個 workspace

### 測試

- 981 tests pass / lint clean

## [1.0.0-alpha.53] - 2026-04-07

Phase 7.4 — Daemon 品質改善 (PR #196, #133, #130, #131, #121, #134)

### 修正

- **upload delete TOCTOU (#133)** — 移除 `os.Stat` check-then-act，直接 `os.Remove` + `IsNotExist` 判斷
- **send-keys 失敗清理 (#130)** — `SendKeysRaw` 失敗時 `os.Remove(destPath)` 清理孤立檔案
- **dedup filename TOCTOU (#131)** — `deduplicateFilename` 改為 `createDedupFile` 使用 `O_CREATE|O_EXCL` 原子佔檔名
- **settings.json atomic write (#121)** — `mergeHooks` 改用 tmp + `os.Rename` 原子寫入

### 效能

- **session watcher debounce (#134)** — `broadcastSessions()` 加 500ms debounce，防止 wait-for + ticker 重複廣播

### 測試

- 新增 6 個 Go 測試（upload delete 404/success、send-keys fail cleanup、atomic write、debounce、debounce expiry）

## [1.0.0-alpha.52] - 2026-04-07

Phase 7.3 — Refactor 拆檔 (PR #192, #163, #138, #185, #182)

### 重構

- **deleteHostCascade 提取 (#163)** — 從 OverviewSection.tsx 提取 cascade delete + undo 邏輯至 `lib/host-lifecycle.ts`，同時修正原本遺漏的 `models` snapshot/restore
- **form-fields 提取 (#138, #185)** — Section / Field / EditableField / TokenField 提取至 `hosts/form-fields.tsx`，OverviewSection 655→280 行
- **cc_hooks 拆分 (#182)** — handler.go CC hook 邏輯移至 `cc_hooks.go`，與 session/hooks.go 結構對稱，handler.go 227→87 行

### 關閉（無需修復）

- **#184** — deriveStatus 已是獨立 exported pure function，提取無實質改善

### 測試

- Tests: 913 → **914**（新增 agentModels undo 測試）

## [1.0.0-alpha.51] - 2026-04-07

Phase 7.2 — UI 元件修正 (PR #187, #139, #179)

### 修正

- **HostSidebar auto-expand (#139)** — selectedHostId 變更時（如 host 刪除 fallback）自動展開新 host，用 derived state（`||`）取代 useEffect
- **WebGL context loss handler (#179 L1)** — `onContextLoss` → dispose addon → fallback DomRenderer → re-fit，防止 terminal 切離再切回時縮小
- **TabContent visibility:hidden (#179 L2)** — 取代 `left:-9999em` off-screen hack，語意更正確
- **keepAliveCount WebGL cap (#179 L3)** — WebGL 上限 6、DOM 上限 10，DOM→WebGL 切換時 auto-clamp，settings 顯示 hint

### 關閉（無需修復）

- **#140** — 已在 commit `e8cfe7ff` 修復（empty draft 跳過驗證）
- **#157** — storage key 切換早於欄位新增，舊資料已遺棄
- **#176** — 架構上場景不成立（inactive tab off-screen）

### 測試

- Tests: 906 → **912**（新增 HostSidebar auto-expand、TabContent visibility、TerminalSection WebGL cap、WebGL context loss 行為測試）

## [1.0.0-alpha.50] - 2026-04-06

Phase 7.1 — Lint + Agent Store 修正 (PR #183, #175, #105, #92, #93, #94, #126, #124, #110, #169)

### 修正

- **SubagentStop events 保護 (#126)** — SubagentStart/Stop 不再覆寫 events map，只更新 activeSubagents
- **Error 狀態白名單 (#124)** — 只有 UserPromptSubmit/SessionStart/Stop 可清除 error，非白名單事件完全跳過（events + status 都不更新）
- **PermissionRequest i18n (#105)** — 通知 body 改用 `{{tool}}` 參數
- **Notification body (#110)** — permission_prompt/elicitation_dialog 顯示特定 body
- **SubagentDots 同步 (#169)** — negative CSS animation-delay 同步呼吸動畫相位
- **Rename tbox_ → purdex_ (#175)** — user-facing 字串 + `tbox_version` → `purdex_version` API 欄位
- **setState-in-useEffect (#92)** — useReducer、render-time state、direct DOM、ref deps
- **useCallback deps (#93)** — 修復遺漏 deps + justified eslint-disable
- **Lint cleanup (#94)** — fast-refresh、explicit-any、globals-reassign
- **TerminalView retrying** — SM cycle 完成後 `Promise.resolve().finally()` 清除 spinner
- **manualRetry 型別** — `() => void` → `() => Promise<void> | void`

### 改善

- Lint: 31 errors/warnings → **0**
- Tests: 904 → **906**（新增 error guard + events 一致性測試）

## [1.0.0-alpha.49] - 2026-04-06

Phase 6 — Hooks 統一架構 (PR #181, #150, #109, #108, #103, #142, #127)

### 新功能

- **Daemon API 統一** — tmux 和 CC hooks 統一為 `/api/hooks/{module}/status` + `/setup` 路由模式
- **HookModule 介面** — 新增模組化架構，新 hook module 只需加一個 config 物件
- **HookModuleCard 元件** — 通用 card 元件，含安裝/移除按鈕、事件狀態、loading spinner
- **Agent Hooks UI 完成** — CC hooks 從 stub 升級為完整的安裝/移除/狀態/錯誤 UI
- **Model Name 持久化 (#127)** — `models` map 防止 model name 被後續事件覆寫
- **觸發時間顯示 (#142)** — `getLastTrigger` API + 相對時間顯示（剛剛 / Nm ago / Nh ago）

### 修正

- **StatusBar reactivity** — model badge 改用 reactive store selector，確保 hook event 到達後即時更新
- **getLastTrigger 解耦** — 從 lib 層移除 store 依賴，改為純函式 + `useMemo` 穩定引用
- **setup() unmount guard** — `mountedRef` 防止 unmount 後 setState
- **exec.Command timeout** — CC hook setup 加入 30s context timeout
- **Response agent_type 移除** — hook status/setup API 回應不再包含多餘的 `agent_type` 欄位
- **i18n placeholder** — 修正 `{n}` → `{{n}}` 格式

### 改善

- **useModuleHook** — 共用 data-fetching hook，管理 loading/error/status 生命週期
- **死路徑清除** — 移除 `useHookStatus`、`App.tsx` init fetch、`hooksInstalled` 欄位、舊 API 函式
- **測試覆蓋** — 新增 `HookModuleCard.test.tsx`（13 tests）、`useModuleHook.test.ts`（9 tests）、`hooks_test.go`（5 tests）、setup handler tests（4 tests）、reactive tests

### 關閉 Issues

- #150, #109, #108, #103, #142, #127, #114, #64

## [1.0.0-alpha.48] - 2026-04-06

api.ts 舊 API 層缺 auth — 遷移至 hostFetch 統一認證 (PR #180, #177)

### 修正

- **API 層統一認證** — 將 `api.ts` 全部 9 個函式遷移至 `host-api.ts`，改用 `hostFetch` 自動帶 Bearer token，解決 daemon 設 token 後所有 API 呼叫 401 的問題
- **Raw fetch 修正** — `App.tsx`、`useHookStatus.ts` 的 3 處 raw fetch 改用 `fetchAgentHookStatus` / `setupAgentHook`
- **Electron updater auth** — `checkUpdate` / `applyUpdate` 新增 `token?` 參數，透過 IPC 傳遞

### 改善

- **Store 簽名簡化** — `useSessionStore.fetchHost(hostId, base)` → `fetchHost(hostId)`、`useConfigStore.fetch/update(base)` → `fetch/update(hostId)`，消除 caller 傳錯 base URL 的可能
- **Dead code 清理** — 移除 `electron/main.ts` 啟動時的 background update check（`dev:update-available` 無 listener、main process 無法取得 auth token）
- **測試補充** — 新增 `host-api.test.ts`（21 個測試），覆蓋所有遷移函式含 auth header 驗證

### 刪除

- `spa/src/lib/api.ts` — 舊 API 層，已完全由 `host-api.ts` 取代

## [1.0.0-alpha.47] - 2026-04-06

Phase 5b — WS Ticket 統一 + Auth Error UI (PR #168)

### 新功能

- **Negotiation-First 狀態機** — `checkHealth` 升級為兩階段（GET /api/health + POST /api/ws-ticket），同時驗證 daemon reachability 與 token auth
- **WS Ticket 統一** — terminal、stream、host-events 三條 WS 全面使用 ticket auth
- **Auth Error 狀態** — `HostRuntime.status` 新增 `'auth-error'`，狀態機偵測 401/503 後不重試
- **Auth Error UI** — HostSidebar 鎖頭圖示（animate-pulse）、StatusBar 可點擊導航至設定頁、OverviewSection 紅色 banner + Token 自動重試
- **Health Mode 消費（#167）** — AddHostDialog 根據 daemon mode 自動導流（pairing → 配對碼、pending/normal → Token）
- **Per-host diff 更新** — 修改單一 host 的 IP/Port 只重建該 host 連線，不影響其他 host
- **connectHostEvents lazy mode** — WS 不立即連線，等待狀態機 negotiation 完成後由 `reconnectWithTicket` 啟動
- **connectTerminal 雙函式設計** — sync `connect()` + async `connectWithTicket()`，既有同步呼叫端不受影響

### 安全性

- **移除 `?token=` query param fallback** — 消除 token 出現在 URL/log/瀏覽器歷史的風險
- **Token-less host 偵測** — SPA 無 token 時回傳 `auth-error`（daemon pairing mode 除外），避免 WS 被 daemon 401 拒絕後的死循環
- **PairingGuard 503 偵測** — daemon 在 pairing mode 時 ws-ticket 回 503，狀態機正確判定為 auth-error

### 修正

- **狀態機 + Auth 死循環** — health endpoint 不驗 auth → 誤判 connected → WS 靜默失敗。兩階段 negotiation 解決
- **新 host token 錯時靜默失敗** — 狀態機不自動啟動 → 灰色圓圈無回饋。lazy mode + 立即 `sm.trigger()` 解決
- **`startBackground` auth-error guard** — L1 背景重試遇 auth-error 正確停止，不再無限循環
- **`ws.close()`/`send()`/`resize()` 安全性** — async getTicket 期間 ws 未初始化時加 optional chaining
- **Relay 雙重 stream WS** — `pendingFetches` Set 防止快速 reconnect 建立重複 WS
- **`reconnect()`/`reconnectWithTicket()` double-trigger** — `ws.onclose = null` 後再 close，防止 onclose handler 重複觸發

### 關聯 Issues

- #148 pt.2 — Terminal/Stream WS auth（統一用 ticket）
- #148 pt.3 — WS 401 auth error 提示
- #167 — health mode SPA 消費

## [1.0.0-alpha.46] - 2026-04-05

Phase 5a — 配對系統 + Token 認證 (PR #164)

### 新功能

- **Quick 模式** — `tbox serve --quick`：13 碼 Base58 配對碼（IP+Port+Secret 編碼），PairingGuard 攔截非配對 API，verify → setupSecret → setup 三步完成
- **一般模式** — `tbox serve`（無 token）：daemon 產生 `purdex_` runtime token 印到 terminal，client 用 `POST /api/token/auth` 確認後持久化
- **PairingState 狀態機** — pairing/pending/normal 三態，thread-safe（atomic.Int32）
- **SetupSecretStore** — 128-bit one-time secret，5 分鐘 TTL，constant-time 比較
- **TokenAuth getter** — 每請求動態讀取 token，支援 runtime token 變更
- **PairingGuard middleware** — Quick 模式下只放行 `/api/pair/*`，其餘回 503
- **Base58 codec** — `internal/pairing/` 獨立 package，encode/decode 配對碼（9 bytes → 13 chars, 4-4-5 格式）
- **Pairing handlers** — `/api/pair/verify` + `/api/pair/setup`，brute-force 保護（10 次失敗 regenerate）
- **Token auth handler** — `/api/token/auth`，confirm 後持久化到 config.toml
- **`/api/health` mode 欄位** — 回傳 `{"ok":true,"mode":"pairing"|"pending"|"normal"}`
- **SPA AddHostDialog 重寫** — 配對碼 + Token 雙路線，stage state machine，IP:port 去重
- **SPA pairing-codec** — TypeScript Base58 解碼 + `purdex_` token 產生
- **i18n** — 新增 12 個配對相關翻譯 key（en + zh-TW）

### 安全性

- **persist-first** — `handlePairSetup` 先 WriteFile 成功才設 runtime token，失敗不污染 state
- **config.toml 權限** — `WriteFile` 改用 `0600`（原為 `0666`），保護明文 token
- **concurrent verify 序列化** — CfgMu.Lock + TOCTOU guard 防止並發 verify 覆蓋 setupSecret
- **PairingSecret 鎖保護** — CfgMu RLock/Lock 保護讀寫，消除 data race

### 重構

- `internal/core/base58.go` → `internal/pairing/base58.go`（獨立 package）
- `cmd/tbox/main.go` 配對初始化抽取至 `cmd/tbox/quick.go:initPairing()`
- PairingGuard 移除 OPTIONS dead code（CORS 已處理）

## [1.0.0-alpha.45] - 2026-04-04

Phase 4 Hotfix — SM tmuxState 覆寫 + L2 背景重連 + Test Connection 重連

### 修復

- **SM onStateChange 不覆寫 tmuxState** — `checkHealth` 的 tmux 是硬編碼 `unavailable`，不應覆蓋 WS event 推送的正確狀態
- **Test Connection 觸發 SM reconnect** — 成功時呼叫 `manualRetry()` 讓 WS 恢復連線
- **i18n** — 「測試連線」→「嘗試連線」

### 新功能

- **L2 refused 背景重連** — FAST_RETRY 後每 3 秒嘗試連線，3 分鐘後停止。Daemon 重啟後不再需要手動操作

## [1.0.0-alpha.44] - 2026-04-04

Phase 4 錯誤 UI — Terminated Tab + Host Error Display + Cascade Delete (PR #162)

### 新功能

- **Tab 狀態模型** — PaneContent `kind: 'session'` 改名為 `kind: 'tmux-session'`，新增 `terminated?: TerminatedReason` 欄位（event-sourced）
- **TerminatedPane 錯誤頁** — session 關閉 / tmux 重啟 / host 刪除三種情境，顯示對應訊息 + 關閉按鈕 + 跨 host SessionPickerList
- **SessionPickerList** — 列出所有已連線 host 的 session，按 host 分組，可用於 tab 重新綁定
- **deriveTabState** — 從 PaneContent + HostRuntime 推導 tab 顯示狀態（active / reconnecting / terminated）
- **Host 層級 L1-L3 錯誤 UI** — StatusBar / HostSidebar / OverviewSection / SessionsSection 各自顯示連線錯誤狀態
- **useHostConnection hook** — 封裝 ConnectionStateMachine 存取，提供 `manualRetry()` 手動重連
- **Reconnecting overlay 手動重連按鈕** — TerminalView 斷線覆蓋層新增重連按鈕 + spinner
- **Host 刪除 cascade cleanup** — checkbox 選擇是否關閉分頁，多 store cascade（AgentStore.removeHost + StreamStore.clearHost）+ 全域 undo toast（5s snapshot 復原）
- **NotificationAction 模組化** — 通知 click handler 改為 action payload dispatch 模式，支援 `open-session` / `open-host`
- **L2/L3 系統通知** — daemon refused / tmux unavailable 狀態轉換時發送桌面通知

### 重構

- **PaneContent kind rename** — `'session'` → `'tmux-session'`，含 Zustand persist migration v1→v2
- **connectionErrorMessage 共用** — 抽取到 `lib/host-utils.ts`，OverviewSection 和 SessionsSection 共用
- **Undo toast 全域化** — `useUndoToast` store + `GlobalUndoToast` 元件，跨頁面導航持續顯示

### 修復

- **#156** — AddHostDialog 顯示具體 L1/L2/401 錯誤訊息
- **#140** — TokenField 清空時不觸發驗證
- **health timeout** — checkHealth timeout 從 3s 調整為 6s

## [1.0.0-alpha.43] - 2026-04-03

Phase 3 連線偵測 — Watcher 狀態機 + WS ping/pong + useHostConnection 閘控 (PR #158)

### 新功能

- **Watcher 狀態機** — NORMAL/TMUX_DOWN 雙模式，tmux 斷線自動偵測 + broadcast `tmux` event，wait-for goroutine 在 TMUX_DOWN 時暫停
- **WS ping/pong** — host-events WS 加入 30s ping / 10s pong timeout，整合 write pump（one-writer rule）
- **API 三層分離** — `/api/health`（無 auth, liveness）、`/api/ready`（有 auth, readiness + tmux 狀態）、`/api/info`（有 auth, identity）
- **ConnectionStateMachine** — 純 class 重連狀態機，L1 不間斷背景重試 + L2 停止 + epoch counter 防止 stale callback
- **checkHealth** — AbortController 3s timeout，L1（unreachable）/ L2（refused）分類
- **WS 閘控** — host-events WS 停止自身 reconnect 由 SM 管理，terminal WS 受 `canReconnect` gate 閘控
- **HostRuntime 擴充** — 新增 `daemonState`（connected/refused/unreachable）+ `tmuxState`（ok/unavailable）
- **TmuxAlive** — Executor interface 新增 `TmuxAlive() bool`，RealExecutor 用 `tmux info`（5s timeout）

### 重構

- **Rename** — `SessionEvent` → `HostEvent`、`/ws/session-events` → `/ws/host-events`（Go + SPA 全面更新）

### 修復

- **notifyWaitFor drain** — 先清空 channel 再寫入，防止 stale signal 阻塞 resume
- **wstate 封裝** — `updateHash` accessor 取代 tickNormal 直接操作 mutex
- **SM stopped guard** — await 後檢查 stopped + epoch，防止 unmount 後 state mutation
- **reconnect ws close** — 重連前關閉既有 WS，防止 duplicate connection

## [1.0.0-alpha.42] - 2026-04-03

SPA 識別系統整合 — Phase 2b (PR #155)

### 新功能

- **PaneContent 擴充** — session kind 新增 `cachedName`（斷線後仍顯示名稱）+ `tmuxInstance`（偵測 tmux 重啟）
- **Daemon Host ID 整合** — AddHostDialog 連線成功後 fetch `/api/info` 取得 daemon 的 `host_id` 作為 store key
- **cachedName 即時同步** — WS session 更新時自動同步 tab 的 cachedName（rename 即時反映）
- **Tab label fallback 鏈** — live name → cachedName → sessionCode

### 修復

- **addHost dedup** — 重複 daemon `host_id` 不再造成 `hostOrder` 重複
- **updateSessionCache layout 安全** — 使用 `updatePaneInLayout` 取代 hardcoded leaf，保護未來 split pane 結構
- **notification cachedName** — 從 notification 重開 tab 時從 sessionStore 查 name，避免空窗期顯示 sessionCode

### Breaking Changes

- PaneContent session 新增 required 欄位，既有 persisted tabs 資料重置

## [1.0.0-alpha.41] - 2026-04-03

Daemon Host ID 產生 + /api/info 擴充 — Phase 2a (PR #153)

### 新功能

- **Host ID** — Daemon 啟動時產生穩定的 `hostname:6-char-code` 識別碼，持久化到 `config.toml`
- **/api/info 擴充** — 回傳 `host_id`（daemon 自報 ID）+ `tmux_instance`（`pid:startTime`，偵測 tmux server 重啟）
- **config.WriteFile** — 統一的 config 原子寫入函式（取代重複的 `persistConfig` / `writeConfig`）

### 修復

- **HostID rollback** — `EnsureHostID` 持久化失敗時回滾 `cfg.HostID`，避免使用不穩定的 ID
- **PUT /api/config redact** — 回應與 GET 一致，隱藏 `host_id` + `token`

## [1.0.0-alpha.40] - 2026-04-03

Storage 抽象層 + Key 遷移 — Phase 1a (PR #152)

### 新功能

- **Storage 抽象層** — 新增 `spa/src/lib/storage/` 模組，以 Zustand `StateStorage` 介面為基礎建立可替換的 storage backend
- **BrowserBackend** — localStorage 包裝 + BroadcastChannel 跨 tab 狀態同步
- **STORAGE_KEYS 常數** — 11 個 localStorage key 的 single source of truth

### 重構

- **Key 遷移** — 所有 10 個 persist store 的 key 從 `tbox-*` → `purdex-*`，統一使用 `STORAGE_KEYS` 常數
- **移除死碼 migrate** — useHostStore（v0→v1）、useSessionStore（v1→v2）、useTabStore（v1→v2）的 migrate 函式及 `addHostIdToLayout` helper（-92 行）
- **Version 統一** — 所有 store 重設為 `version: 1`

### 修復

- **Rehydration 迴圈** — `browserStorage.setItem` 加入 equality check，防止 `onRehydrateStorage` callback 觸發無限 BroadcastChannel ping-pong
- **BC null guard** — `sync.ts` onmessage 加入型別檢查，防止非預期訊息格式導致 TypeError

### Breaking Changes

- 所有 localStorage key 更名（alpha 階段不向下相容），升級後本地 persist 資料重置

## [1.0.0-alpha.39] - 2026-04-02

Unify session mode naming + remove JSONL mode (PR #151)

### 重構

- **Mode 命名統一** — Daemon 和 SPA 統一使用 `"terminal"` / `"stream"`（原 daemon 回傳 `"term"`，導致 StatusBar 顯示不一致）
- **移除 JSONL session mode** — 移除未使用的 JSONL mode（`JSONLConfig`、jsonl preset、jsonl icon），保留 CC 歷史記錄 `.jsonl` 檔案格式解析
- **DDL schema 同步** — SQLite `DEFAULT 'term'` → `'terminal'`
- **升級遷移** — `ResetStaleModes()` 啟動時自動將舊的 `"term"` / `"jsonl"` 記錄遷移為 `"terminal"`

### 清理

- **刪除 TopBar** — Phase 1 遺留的 deprecated 元件（已被 TabBar + StatusBar 取代，無任何引用）

## [1.0.0-alpha.38] - 2026-03-31

Host Page UI + Multi-Host Integration — Phase 1.6c C+D (PR #136)

### 新功能

- **Host Page** — 完整主機管理頁面（ActivityBar HardDrives 按鈕），含 sidebar accordion 導覽 + 4 個子頁面
- **Overview** — 連線設定（editable name/ip/port/token with validation）、Daemon Config（sizing mode）、System Info
- **Sessions** — Session CRUD table（New/Open/Rename/Delete）+ agent status badge
- **Hooks** — tmux hooks + agent hooks 安裝狀態、install/remove 操作、per-event indicator
- **Uploads** — 暫存檔案按 session 分組、stats 顯示、個別/批次刪除
- **Add Host dialog** — health check → 401 偵測 → token 輸入 onboarding 流程
- **Token editing** — show/hide toggle、儲存前 /api/sessions 驗證、401 error handling
- **Multi-host grouping** — SessionPanel + SessionSection 按 host 分組、single host 時隱藏 header
- **Offline handling** — SortableTab WifiSlash icon、StatusBar 三色狀態、離線 disable
- **ErrorBoundary** — React error boundary 防止全畫面崩潰
- **Electron crash recovery** — render-process-gone 自動重載 SPA

### 修正

- sizing_mode 選項值對齊 daemon（auto/terminal-first/minimal-first）
- isOffline 邏輯統一（runtime undefined = not offline）
- isActiveSession 檢查 activeHostId（multi-host 防衝突）
- getAgentStatus 改用 reactive hook（非 getState）
- StatusIcon grey for undefined runtime
- AddHostDialog stage reset 涵蓋 needs-token/error
- HooksSection schema 對齊 daemon（tmux_hooks map）
- EditableField double-save 防護（savedRef）
- ws?.close() null safety + onerror handler (#137)
- fetchHost unhandled promise .catch() (#137)
- i18n 全面覆蓋（9 hardcoded strings 修正）
- a11y: AddHostDialog aria-modal + Escape、Section aria-expanded、TokenField aria-label
- InlineRename onBlur 防止編輯卡住

### 測試

- 63 個新測試（7 個 test files），734/735 pass

### 追蹤 Issues

- #138 OverviewSection 拆檔
- #139 HostSidebar expanded 同步
- #140 TokenField 清除 UX
- #141 Terminal palette 顯示
- #142 Hook 最後觸發時間
- #143 Upload 暫存目錄編輯
- #144 SessionPanel host header 可收合

## [1.0.0-alpha.37] - 2026-03-31

Agent File Upload — 拖曳檔案上傳到 CC agent（PR #129）

### 新功能

- **Daemon upload endpoint** — `POST /api/agent/upload`，multipart 上傳 → 存到 `~/tmp/tbox-upload/{session}/` → `tmux send-keys` 注入路徑
- **TerminalView drag-drop** — CC agent 活躍時攔截拖曳，逐一上傳逐一注入，drop overlay 提示
- **StatusBar 上傳進度** — uploading（黃色 spinner + 檔名）/ done（綠色勾）/ error（紅色叉，可點擊消除）
- **Agent label badge** — 有 model name 時橘棕色 badge，fallback 白色帶框
- **useUploadStore** — per-session 上傳狀態管理（Zustand，不 persist）
- **i18n** — 上傳相關文字支援 en/zh-TW，含單複數處理

### 安全修復

- **Path traversal 防護** — `filepath.Base()` sanitize 上傳檔名
- **路徑引號包裹** — send-keys 注入路徑以雙引號包裹，處理含空格檔名

### 修正

- **並發 Drop 保護** — `dismiss()` 不清除 uploading 狀態，防止重複拖曳污染 store

### 追蹤 Issues

- #127 StatusBar agent label modelName 被 latest event 覆蓋
- #130 upload send-keys 失敗時清理孤立暫存檔案
- #131 deduplicateFilename TOCTOU race condition

## [1.0.0-alpha.36] - 2026-03-31

Agent Hook 增強 — error 狀態、subagent 追蹤、unread 修正（PR #123）

### 新功能

- **error 狀態** — `StopFailure` 推導為新的 `error` 狀態，紅色燈號（`#ef4444`），觸發 unread 紅點和桌面通知
- **Subagent 追蹤** — 註冊 `SubagentStart`/`SubagentStop` hooks，以 `agent_id` 追蹤 active subagents（ephemeral，不存 DB）
- **SubagentDots 元件** — tab icon 左側顯示 1-3 顆藍色呼吸燈（`#60a5fa`），依 subagent 數量遞減尺寸
- **通知 newline 壓縮** — 彈窗 body 連續換行壓成單個

### 修正

- **Unread 紅點不可見** — 移除 tab `overflow-hidden`，重新定位到右上角框線上（`-top-[4px] -right-[4px]`、`z-20`），不再被 close 按鈕 gradient 遮蔽
- **SessionStart 推導修正** — `startup`/`resume`/`clear` → `idle`（等待輸入），非 `running`
- **shouldNotify 遺漏 error** — `StopFailure` 現在正確觸發桌面通知
- **error 不被 idle Notification 降級** — `idle_prompt`/`auth_success` 不會覆蓋 error 燈號
- **HandoffButton error 支援** — `isAgentActive` 加入 `error`，StopFailure 後 Handoff 按鈕不會 disabled
- **SessionStart(compact) 不清空 subagents** — compact 是工作中途壓縮，subagent 可能還在跑
- **WS 重連清空 subagents** — `onOpen` callback 清除 ephemeral 追蹤，避免斷線後殘留
- **SessionStatusBadge 加 error 顏色** — `bg-red-500`

### 追蹤 Issues

- #124 PermissionRequest 可覆寫 error 狀態
- #125 HandoffButton isAgentActive error 測試
- #126 SubagentStop orphan event 覆寫 events map

## [1.0.0-alpha.35] - 2026-03-30

衍生 focusedSession + tab 切換 auto-focus（PR #122）

### 修正

- **通知在 active tab 仍彈出** — `focusedSession` 從手動同步改為從 `activeTabId` 即時衍生（`getActiveSessionCode()`），鍵盤快捷鍵切 tab 時通知正確抑制
- **隱藏 tab 攔截鍵盤輸入** — 非 active 的 keep-alive tab 加上 `inert` attribute，阻止 offscreen terminal 捕獲 focus
- **Stream tab 切換後自動 focus** — `StreamInput` 加 `focused` prop，切到 stream tab 時 textarea 自動 focus
- **通知點擊 active tab 未清 unread** — `handleNotificationClick` 補 `markRead` 保底

### 重構

- 移除 `focusedSession` / `setFocusedSession` 狀態，改用 cross-store subscription 自動 markRead
- 移除 `useTabWorkspaceActions` 中的手動 focus 同步邏輯

## [1.0.0-alpha.34] - 2026-03-30

防止 tbox setup 重複 hook entries（PR #120）

### 修正

- **setup 防重複 hook** — 從不同路徑執行 `tbox setup`（如 `./tbox` vs `./bin/tbox`）不再累積重複 entries，每次 setup 先清除所有既有 tbox entries 再加入當前路徑
- **`entryIsTbox` 精確比對** — 用 binary basename 邊界檢查（`/tbox"` / `/tbox `）取代寬鬆的 `Contains`，避免誤刪 `tbox-extra` 等第三方工具的 hooks
- **移除死碼** — 清除不再使用的 `hasTboxEntry`、`filterOutTbox`、`entryMatchesTbox`
- **測試 `expectedEvents` 改用 `hookEvents`** — 補齊遺漏的 `StopFailure` 事件覆蓋

## [1.0.0-alpha.33] - 2026-03-30

移除 tab 燈號 fallback（PR #119）

### 修正

- **移除無 snapshot 時的 idle fallback** — 原本 hooksInstalled 為 true 時預設顯示灰色燈號，無法區分「agent 在跑但尚未送出 event」與「沒有 agent」，現在只在收到實際 hook event 後才顯示燈號

## [1.0.0-alpha.32] - 2026-03-30

通知去重 + 視窗焦點感知（PR #118）

### 修正

- **通知持久化去重** — 用 localStorage `lastSeenTs`（Infinity sentinel）取代 in-memory `notifiedRef`，跨重啟不重複通知
- **視窗焦點感知** — 只在 App 視窗有焦點且正看該 tab 時抑制通知，App 在背景時仍發通知
- **SessionEnd 清理** — 清除 `lastSeenTs` 防止 session code 重用時舊 ts 阻擋通知
- 三層 dedup 架構文件化（localStorage → shouldNotify focus → Electron main recentBroadcasts）

## [1.0.0-alpha.31] - 2026-03-30

Hook 引號路徑匹配修復（PR #117）

### 修正

- **`findTboxCommand` 支援引號路徑** — strip `"` 後匹配 `tbox hook`，修復 `hooksInstalled` 在引號路徑下回傳 false 的問題

## [1.0.0-alpha.30] - 2026-03-30

SPA 來源切換 Preflight（PR #116）

### 修正

- **forceLoadSPA preflight** — 切換至 Dev Server 前先驗證可達性（2s timeout + `response.ok`），避免用戶困在錯誤頁
- IPC error 序列化為 plain string（contextBridge 相容）
- 錯誤訊息顯示具體原因 + i18n 化

## [1.0.0-alpha.29] - 2026-03-30

Dev Update 包含 Renderer（PR #115）

### 修正

- **Dev update 包含 renderer** — download tar 現在打包 `out/renderer/`，updater 也替換它，SPA 改動不再需要重裝 `.app`
- Rollback 各目標獨立 try-catch，防止連鎖失敗
- 測試 tar reader 區分 `io.EOF` 和真實錯誤

## [1.0.0-alpha.28] - 2026-03-30

Electron CORS 修復 + SPA 來源切換（PR #113）

### 新增

- **`app://` custom protocol** — bundled SPA 改用 `app://` 取代 `file://`，啟用標準 CORS 行為
- **SPA Source 顯示** — Development Settings 顯示當前 SPA 來源（Dev Server / Bundled）
- **SPA 來源切換** — 一鍵切換 Dev Server ↔ Bundled，即時 reload
- **`forceLoadSPA` IPC** — Electron preload 暴露（`TBOX_DEV_UPDATE` gate 內）

### 修正

- Protocol handler 路徑遍歷防護（`startsWith` 驗證 + 403）
- `forceLoadSPA` 改 async、await `loadURL`、IPC handler return promise
- `spaSource` 偵測改正向匹配 `app:` protocol

## [1.0.0-alpha.27] - 2026-03-30

Agent Hook 子類別狀態判斷（PR #107）

### 新增

- **`deriveStatus` 子類別判斷** — Notification 依 `notification_type`、SessionStart 依 `source` 精確判斷狀態
- **`StopFailure` 事件處理** — daemon 註冊 hook + SPA 狀態映射（idle）+ 桌面通知（error_details）
- **`hooksInstalled` fallback** — hooks 已安裝但尚無事件的 session tab 預設顯示 idle 狀態點
- **CC Hook Event Reference 文件** — `docs/cc-hook-event-reference.md`，完整事件映射與設計決策

### 修正

- `Notification(idle_prompt)` 不再覆蓋 `Stop` 設定的 idle 狀態為 waiting
- `SessionStart(compact)` 背景壓縮不再錯誤觸發 running 狀態
- `shouldNotify` 排除 idle Notification（避免 idle_prompt 重複通知）
- `useNotificationDispatcher` 傳入 `rawEvent` 給 `deriveStatus`（修正通知靜默 regression）
- `useHookStatus` install/remove 後同步 `hooksInstalled` 到全域 store
- `App.tsx` hook-status fetch 加入 `res.ok` 檢查
- 未知 `notification_type` 記錄 console.warn
- Unread dot 排除資訊性 Notification（idle_prompt / auth_success）
- 新增 `notification.fallback.stopFailure` i18n key（en + zh-TW）

## [1.0.0-alpha.26] - 2026-03-29

1.6c-pre2: CC 通知系統（PR #102）

### 新增

- **Electron 系統通知** — Agent hook 事件（waiting + idle）觸發 macOS 系統通知，點擊跳轉對應 tab
- **`agent_type` 欄位** — `tbox hook --agent cc` 識別 agent 類型，daemon 儲存並廣播
- **`broadcast_ts` 持久化** — 多視窗去重 + 防止 WS 重連通知爆發
- **Agent Settings section** — Settings 內新增 Agent 區塊，per-agent 通知開關 + per-event toggle
- **Hook 狀態檢視** — Agent Settings 顯示 hook 安裝狀態 + 一鍵安裝/移除按鈕
- **`GET /api/agent/hook-status`** — 讀取 CC settings.json 回報 hook 安裝狀態
- **`POST /api/agent/hook-setup`** — 執行 `tbox setup` 安裝或移除 hook
- **`useNotificationDispatcher`** — SPA 通知判斷 + Electron/PWA 雙路徑分發
- **`useNotificationSettingsStore`** — Per-agent 通知設定（Zustand + persist）
- **`useHookStatus`** — Hook 狀態查詢 custom hook
- **i18n** — 新增 19 個 Agent 相關 locale key（en + zh-TW）
- **三層級設定預留** — system → host → workspace 覆寫架構（先做 system 層）

### 修正

- `focusedSession` 切到非 session tab 時正確清除
- 通知點擊重開 tab 時正確加入 workspace
- 多視窗通知點擊只聚焦有 tab 的視窗（不閃現所有視窗）

## [1.0.0-alpha.25] - 2026-03-29

Dev Update Auto-Build（PR #96）

### 新增

- **`out/.build-info.json`** — `electron-vite build` 完成後寫入 build metadata（version + hash + timestamp）
- **check/download 一致性** — `check` 端點改讀 `.build-info.json` 作為 build hash，頂層回傳 build hash + `source` 回傳 git hash
- **Auto-build** — source ≠ build 時 daemon 背景自動觸發 `electron-vite build`，回傳 `building: true`
- **Build 失敗退避** — 同一 source hash 失敗後不重複觸發，source 改變才重試
- **Download 建置鎖** — build 進行中 download 回傳 409 Conflict
- **SPA Building 狀態** — 顯示「建置中…」+ 每 3 秒 poll，完成後自動比對

### 修正

- Partial build 不再污染 `.build-info.json`（build 前先刪除）
- `pnpm exec` 取代 `npx`，符合 pnpm-only 規範
- `RemoteInfo` 型別從 `electron.d.ts` derive，消除三處重複定義

### 關閉 Issue

- #78（feat: dev update — auto-build before download）
- #98（refactor: unify RemoteVersionInfo type）

## [1.0.0-alpha.24] - 2026-03-29

Agent Hook 狀態偵測（PR #91）

### 新增

- **`tbox hook` 子命令** — CC hook 觸發時讀取 stdin + tmux session name，POST 到 daemon `/api/agent/event`
- **`tbox setup` 子命令** — 自動配置 `~/.claude/settings.json` hook entries（冪等、支援 `--remove`）
- **Agent module（daemon）** — 純 relay：存 raw event + WS broadcast，不解析 payload
- **AgentEventStore** — 獨立 SQLite 儲存每 session 最近一筆 hook event，新 WS subscriber 自動 snapshot
- **useAgentStore（SPA）** — hook event → running/waiting/idle 狀態機 + unread 管理
- **TabStatusDot** — 三種 tab 指示器樣式（overlay / replace / inline），Settings 可切換，預設 overlay
- **呼吸燈動畫** — running 狀態 `background-color` fade 到 tab 底色
- **未讀紅點** — 5px 暗紅圓點在 inactive tab 右上角
- **Session Panel 燈號** — 狀態 dot 在 code 前方（running/waiting/idle）
- **StatusBar agent 資訊** — 有 agent 時顯示名稱（`getAgentLabel` 集中化）

### 變更

- **移除 CC status poller** — 完全移除 `poller.go`，CC 狀態改為 hook 驅動
- **WS "hook" 事件取代 "status"** — `SessionEvent.type` 不再包含 `'status'`
- **SessionStatusBadge** — 改用 `AgentStatus`（running/waiting/idle），非 agent session 不顯示 badge
- **HandoffButton** — `sessionStatus` prop 改為 `agentStatus`，語意等價
- **useStreamStore** — 移除 `sessionStatus` 欄位，agent 狀態獨立管理

### 修正

- Hook POST 加入 `Authorization: Bearer` header（token 環境下不再 401）
- TabStatusDot running 加 fallback `backgroundColor`（CSS animation 不生效時仍可見）
- `tbox setup` 路徑含空格時加引號、`entryMatchesTbox` 改用 `HasPrefix` 避免誤刪
- 空 `tmux_session` 不存入 DB（避免 garbage row）
- SortableTab 抽出 `renderTabIcon` 消除 pinned/normal 重複邏輯

## [1.0.0-alpha.23] - 2026-03-28

Dev update 進度回饋（PR #85）

### 新增

- **Update 進度顯示** — 點 Update App 後即時顯示 Downloading → Extracting → Applying 各階段
- **錯誤訊息顯示** — 更新失敗時顯示具體錯誤（之前完全無回饋）
- **`dev:update-progress` IPC** — main process 透過 push 事件回報步驟

### 修正

- Error 跨 contextBridge 序列化失敗 — 改在 main process catch 後轉為 string re-throw
- 加入 `updateInProgress` lock 防止重複呼叫 `applyUpdate` 導致檔案競態
- 移除不可達的 `progress('restarting')` 呼叫（app.exit 前 IPC 來不及送達）

## [1.0.0-alpha.22] - 2026-03-28

Electron 快捷鍵系統 + tear-off 修正（PR #84）

### 新增

- **Keybinding registry** — `electron/keybindings.ts` 集中定義快捷鍵，`menuGroup` 分組，為未來自定義擴充預留
- **Electron Menu** — App / File / Edit / Tab / View 五層選單，含快捷鍵提示
- **快捷鍵** — `Cmd+T` 新增分頁、`Cmd+N` 新增視窗、`Cmd+1~9` 切換 tab、`Cmd+Option+←/→` 前後切換、`Cmd+,` Settings、`Cmd+Y` History、`Cmd+Shift+T` 重開 tab
- **useShortcuts hook** — 統一 `shortcut:execute` IPC listener，workspace-aware tab 切換
- **17 個單元測試** — 含 workspace 整合、邊界情況

### 修正

- **Tear-off 帶走所有 tab** — 新視窗從 localStorage persist 恢復出全部 tab。改用 `replace` 旗標，tear-off 時清空再加入
- **reopen-closed-tab 不加入 workspace** — 重開的 tab 現在加入 active workspace
- **prev-tab/next-tab 用全域 tabOrder** — 改用 workspace visible tabs，與 TabBar 顯示一致
- 移除 `App.tsx` 硬編碼 `Cmd+Shift+T`，統一由 Menu accelerator 驅動

## [1.0.0-alpha.21] - 2026-03-27

Dev auto-update system（PR #77）

### 新增

- **Daemon dev module** — `/api/dev/update/check` + `/api/dev/update/download` 端點，`[dev] update = true` config 控制
- **Build hash 注入** — `__APP_VERSION__`、`__ELECTRON_HASH__`、`__SPA_HASH__` 透過 Vite define 編譯時注入
- **Electron updater** — 下載 tar.gz、解壓、備份 + rollback、替換 out/、重啟
- **Settings「Development」section** — 版本資訊 + 檢查更新 / 更新 App / 重新載入 SPA
- **啟動時背景檢查** — main process 啟動後靜默查詢 daemon 有無新版

### 修正

- `devUpdateEnabled` 改由 `TBOX_DEV_UPDATE` 環境變數控制（preload 條件性暴露 IPC）
- 更新流程加入 backup + rollback 防止 partial update 損壞

## [1.0.0-alpha.20] - 2026-03-26

Electron shell — 桌面應用完整實作（PR #76）

### 新增

- **Electron desktop shell** — electron-vite + pnpm workspace monorepo 架構
- **多視窗管理** — tear-off / merge via context menu
- **WebContentsView browser pane** — 生命週期管理（ACTIVE → BACKGROUND → DISCARDED）
- **System tray** — 最小化到系統匣
- **Memory monitor** — process metrics 監控頁面

## [1.0.0-alpha.19] - 2026-03-26

PWA + Platform capabilities + Browser pane（PR #75）

### 新增

- **PWA installability** — manifest.json + icons（192/512/maskable）+ Apple meta tags
- **Platform capabilities** — `getPlatformCapabilities()` + ambient `electron.d.ts` 型別宣告
- **PaneContent `browser` kind** — labels + route mapping + i18n keys
- **NewTabProvider disabled 支援** — disabled provider 顯示說明文字

## [1.0.0-alpha.18] - 2026-03-25

i18n 系統 — 自建多語系 + 自訂語系 + 編輯器/匯入（PR #72）

### 新增

- **Locale Registry** — Map-based，與 Theme Registry 同架構
- **useI18nStore** — `t(key, params?)` 翻譯函式，persist `tbox-i18n`
- **Fallback chain** — active locale → en → key itself
- **LocaleEditor / LocaleImportModal** — fork builtin → 編輯 → 另存
- **locale-completeness.test.ts** — en/zh-TW key 完全對稱守門測試

## [1.0.0-alpha.17] - 2026-03-25

Theme 系統 — 多主題 + 自訂主題 + 匯入匯出（PR #71）

### 新增

- **23 語義 CSS token** — Tailwind v4 `@theme` + CSS Variables，分 6 組
- **4 預設主題** — Dark / Light / Nord / Dracula
- **Theme Registry** — Map-based + Zustand Theme Store（localStorage persist）
- **ThemeEditor** — 即時預覽 + fork / export / import
- **ThemeInjector** — runtime 注入自訂主題 `<style>`

## [1.0.0-alpha.16] - 2026-03-24

Settings UI — VSCode 風格 sidebar + content pane（PR #70）

### 新增

- **Settings pane** — 取代 overlay，以 singleton tab 呈現
- **Settings Section Registry** — 動態註冊，新增 section 只需 2 檔
- **通用元件** — SegmentControl / ToggleSwitch / SettingItem
- **Appearance section** — Theme / Language（disabled, 待 Phase 2/3）
- **Terminal section** — Renderer / Keep-alive / Reveal Delay

## [1.0.0-alpha.15] - 2026-03-24

Tab/Session 解耦 + Pane 模型 + wouter 路由（PR #69）

### 新增

- **Tab/Session 解耦** — Tab 從 Session 1:1 容器改為通用容器
- **Pane 模型** — PaneLayout tree + PaneContent discriminated union（new-tab / session / dashboard / history / settings）
- **Pane Registry** — `registerPaneRenderer(kind, { component })`
- **NewTab Provider Registry** — 可擴充的 content picker
- **wouter 路由** — hash → path-based（`/t/:tabId/:mode`、`/w/:workspaceId`）
- **useRouteSync** — 雙向路由同步（Tab ↔ URL）

## [1.0.0-alpha.14] - 2026-03-24

整合 CC + Stream modules，刪除 legacy server（PR #68）

### 變更

- **Module 整合** — `cc.New()` + `stream.New()` 接入 main.go
- **Legacy 清除** — 刪除 `internal/server/`（18 檔、~4600 LOC）+ legacy store + migration
- **Session code 統一** — SPA 全面從 session name 改用 session code

## [1.0.0-alpha.13] - 2026-03-23

Phase 1.6b Tasks 9-10 — Stream module（PR #66）

### 新增

- **Stream module** — relay WS 管理、SPA subscriber fan-out、handoff 編排
- **Bridge 改用 session code** 作為 key
- **Handoff 改用 CCOperator methods** — 取代重複的 raw tmux send-keys

## [1.0.0-alpha.12] - 2026-03-23

Phase 1.6b Tasks 1-8 — Core 擴充 + CC module（PR #65）

### 新增

- **Core 擴充** — Module `Dependencies()` + `Stop(ctx)`、Kahn's algorithm 拓撲排序
- **EventsBroadcaster** — fire-and-forget + OnSubscribe snapshot
- **Config handler** — OnConfigChange callback
- **CC module** — CCDetector + CCOperator + CCHistoryProvider + Status Poller
- **Middleware 搬遷** — `internal/middleware/` 獨立 package

## [1.0.0-alpha.11] - 2026-03-22

Phase 1.6b Part 1 — Core 擴充 + CC module 基礎（PR #63）

### 新增

- Core layer 擴充基礎建設
- CC module 初始結構（後續 PR #65 完成）

## [1.0.0-alpha.10] - 2026-03-22

修復 MetaStore 冗餘寫入（PR #60）

### 修正

- Handoff Step 8 移除 `SetMeta` 後重複的 `UpdateMeta`
- `MigrateFromLegacy` 錯誤改為 log 輸出

## [1.0.0-alpha.9] - 2026-03-22

Phase 1.6a — Daemon Module 架構 + Session 重設計（PR #59）

### 新增

- **Module 架構** — Core + ServiceRegistry + Module interface，支援可插拔模組
- **Session 重設計** — tmux 為 SOT，DB 降級為 meta cache
- **Session ID 編碼** — 6 碼 base36 code（multiplicative cipher）

## [1.0.0-alpha.8] - 2026-03-22

修復 Tab 拖曳右邊界（PR #58）

### 修正

- Tab 拖曳右邊界限制在最後一個 tab，不再進入 + 按鈕區域

## [1.0.0-alpha.7] - 2026-03-22

Pin/Lock 獨立化（PR #57）

### 變更

- Pin 和 Lock 解耦為獨立旗標：pin 只負責定位，lock 只負責擋關閉
- Pinned tab 可被關閉（除非同時 locked）
- Reopen 恢復 pinned 狀態

## [1.0.0-alpha.6] - 2026-03-21

Tab 互動強化 — 拖曳排序 + 溢出箭頭 + 右鍵選單（PR #56）

### 新增

- **拖曳排序** — @dnd-kit 雙區（pinned / normal）+ restrictToTabZone modifier
- **溢出箭頭** — tab 超出可視範圍時顯示左右捲動按鈕
- **右鍵選單** — Pin / Lock / Close / Close Others
- **中鍵關閉** — 中鍵點擊 tab 關閉

## [1.0.0-alpha.5] - 2026-03-20

Phase 1.5 Task 2 — TerminalView 拆分 + Keep-Alive（PR #55）

### 新增

- **TerminalView 拆分** — 222 → 79 行，抽出 `useTerminal` + `useTerminalWs` hooks
- **useTabAlivePool** — LRU keep-alive pool 管理
- **TabContent pool 渲染** — `display: none` 隱藏非活躍 tab

## [1.0.0-alpha.4] - 2026-03-20

Phase 1.5 Task 1 — Tab 模型擴充（PR #54）

### 新增

- Tab interface 加入 `pinned` / `locked` 欄位
- useTabStore 新增 pin / unpin / lock / unlock 方法
- `removeTab` / `dismissTab` 加入 locked guard

## [1.0.0-alpha.3] - 2026-03-20

Phase 1.1 — Tab 模型修正 + view toggle（PR #48）

### 變更

- Tab 模型從封閉 union 改為開放式 `type: string` + `viewMode` + `data` bag
- 新增 Tab Renderer Registry
- 還原 v0 的檢視/handoff 分離設計

## [1.0.0-alpha.2] - 2026-03-20

xterm addons + terminal renderer toggle（PR #47）

### 新增

- **@xterm/addon-unicode11** — CJK 字元寬度支援
- **@xterm/addon-web-links** — 可點擊 URL
- **Terminal 渲染器切換** — WebGL / DOM 下拉選單

## [1.0.0-alpha.1] - 2026-03-20

Phase 1: 分頁系統 + Activity Bar — SPA 架構從「單 session 檢視」升級為「多分頁 + 工作區」

### 新增

- **Tab 系統** — 每個 tmux session 自動對應一個 tab，支援 terminal / stream / editor 三種類型
- **ActivityBar** — 左側垂直工作區切換列（Workspace icons + standalone tabs + 設定入口）
- **TabBar** — 水平分頁列（切換 / 關閉 / 新增 / dirty indicator）
- **TabContent** — 只掛載 activeTab，切換即銷毀重建（keep-alive 因 tmux resize corruption + WebGL 耗盡移除）
- **StatusBar** — 底部狀態列（host / session / mode）
- **SessionPicker** — Session 選擇 popover（搜尋 + 已開啟標記）
- **useTabStore** — Tab CRUD + `dismissTab` 防止關閉的 tab 被 auto-sync 復活 + localStorage 持久化
- **useWorkspaceStore** — Workspace 管理 + tab 歸屬 + per-workspace activeTab
- **useHostStore** — 取代 hardcoded daemonBase（最小版，Phase 6 擴充為多主機）
- **useUISettingsStore** — 前端 UI 設定（terminalRevealDelay 300ms + terminalRenderer webgl/dom）
- **useIsMobile** — 響應式 breakpoint hook（768px）
- **Hash routing** — `#/tab/{tabId}` 格式，支援 back/forward + 重整後保留
- **App.tsx 重構** — 提取 `useSessionEventWs`、`useSessionTabSync`、`useHashRouting` 三個 custom hooks（345→247 行）
- **xterm.js addons** — `@xterm/addon-unicode11`（CJK 字元寬度）+ `@xterm/addon-web-links`（可點擊 URL）
- **Terminal 渲染器切換** — Settings 新增 WebGL / DOM 下拉選單，變更後自動重連

### 修正

- **crypto.randomUUID fallback** — 非 localhost HTTP context 無法使用，加了 Date.now + Math.random fallback
- **Terminal reveal delay 設定化** — 從 hardcoded 300ms 改為 `useUISettingsStore` 可調整，用 ref + subscribe 避免設定變更觸發 terminal 重建
- **Reconnect overlay 回歸修復** — 恢復 `if (revealed) setReady(true)` 讓 WS 重連後立即顯示 terminal
- **Stale tab 清理** — sessions 消失時自動移除對應 tab（guard `sessions.length > 0` 防止初始渲染清空）
- **Subscribe 洩漏修復** — TerminalView 的 Zustand subscribe 移入 useEffect + cleanup
- **Lint + type errors 全面修正** — 移除 `as any`、修正 SessionStatus type、補 missing fields

### 已知限制

- keep-alive 已移除，每次切 tab 都重新建立 terminal WS 連線（TerminalView 的 `visible` 路徑保留供未來 LRU 快取）
- StatusBar 狀態固定顯示 'connected'（未接 relayStatus/sessionStatus）
- TopBar 標記 @deprecated 但未刪除
- useIsMobile hook 已建立但未在任何元件中使用（Phase 7b）

## [0.5.4] - 2026-03-19

修復 handoff 相關的 terminal resize 與 copy-mode 問題

### 修復

- **Handoff 後 tmux 自動 resize 恢復** — `tmux resize-window -x 80 -y 24`（handoff step 3.5）會讓 tmux 進入手動尺寸模式，導致回到 term 後 window 卡在 80x24 不隨瀏覽器 viewport 調整。handoff 完成 `/status` 擷取後立即呼叫 `resize-window -A` 清除手動旗標
- **Handoff 前退出 tmux copy-mode** — terminal 處於 copy-mode（捲動瀏覽歷史）時 handoff 會失敗。改用 `tmux send-keys -X cancel` 取代依賴 `Escape`，不受 vi/emacs mode 影響且不送按鍵到底層應用

### 新增

- **`[terminal] auto_resize` 設定** — 預設啟用，每次 terminal WS 連線時自動清除手動尺寸旗標。使用者可設 `auto_resize = false` 停用
- **`Executor.ResizeWindowAuto`** — 封裝 `tmux resize-window -A`
- **`Relay.OnStart` callback** — PTY 啟動後的 hook，用於 terminal 連線時重設視窗尺寸

## [0.5.2] - 2026-03-19

架構重構：Stream UI 狀態改由 server-derived relayStatus 驅動

### 重構

- **ConversationView 改用 relayStatus** — 不再依賴 ephemeral `handoffState`，改為 `relayStatus[session]` 作為 single source of truth。Page refresh / WS 重連後自動恢復 stream UI 狀態
- **移除 handoffState** — store 中的 `HandoffState` type、`handoffState` map、`setHandoffState` action 全部移除
- **HandoffButton 簡化** — props 從 `state: HandoffState` 改為 `inProgress: boolean`

### 新增

- **TerminalView `visible` prop** — 切回 term tab 時自動 refit + resize，用遮罩擋住 500ms 等待 tmux 調整完畢再 fadeout

### 修復

- **Handoff 前退出 copy-mode** — 發送 Escape + C-u 退出 tmux 捲動模式並清空輸入，避免 send-keys 注入失敗
- **Handoff Escape error check** — SendKeysRaw(Escape) 失敗時提早返回

## [0.5.1] - 2026-03-18

Bugfix: Handoff tmux target、pane resize、xterm.js 選取

### 修復

- **Handoff tmux target 解析** — 所有 tmux 操作改用 `sess.TmuxTarget`（session:window 格式），避免 bare session name 被 tmux 模糊解析到錯誤的 pane
- **Handoff pane resize** — xterm.js 在 `display:none` 時 PTY 尺寸過小（10x5），tmux smallest-client 規則縮小 pane，`/status` TUI 渲染錯亂。relay PTY 預設 80x24，handoff 前檢查 pane 尺寸並 resize window
- **xterm.js 文字選取** — 啟用 `macOptionClickForcesSelection` 和 `rightClickSelectsWord`，抑制 terminal container 的右鍵選單

## [0.5.0] - 2026-03-18

Stream Message UI — 完整渲染所有 CC 訊息類型

### 新增

- **ThinkingBlock** — 可摺疊的 thinking 區塊（Brain icon，collapsed by default）
- **ToolResultBlock** — 可摺疊的 tool result 顯示（CheckCircle/XCircle 區分成功/錯誤）
- **Slash command 氣泡** — `/exit`、`/status` 等指令以黃棕色氣泡顯示（TerminalWindow bold icon）
- **Interrupted 提示** — 中斷訊息靠左紅棕色顯示（Prohibit icon）
- **@tailwindcss/typography** — 啟用 prose markdown 樣式

### 修改

- **MessageBubble** — User: 藍色氣泡靠右；Assistant: 移除氣泡，直接 prose markdown 輸出（Cowork 風格）
- **ToolCallBlock** — 統一 Wrench icon（移除 per-tool icons），新增 Agent/Grep/Glob summary
- **ConversationView** — 接線所有 content block 類型（thinking、tool_use、tool_result、text、command、interrupted）

### 修復

- **ParseJSONL 過濾 CC 內部標記** — 跳過 `isMeta`、`<local-command-caveat>`、`<local-command-stdout>`、`<synthetic>` assistant；解析 `<command-name>` 為乾淨文字
- **aria-expanded** — 所有可摺疊元件加入無障礙屬性

## [0.4.2] - 2026-03-18

Bugfix: 從 CC `/status` 取得 cwd，修復空 cwd session 的歷史載入

### 新增

- **`detect.ExtractStatusInfo`** — 從 CC `/status` 同時解析 Session ID 和 cwd
- **`store.SessionUpdate.Cwd`** — 支援更新 session 的 cwd 欄位

### 修復

- **空 cwd 導致歷史載入失敗** — auto-scan 使用 `#{session_path}` 取得 cwd，但部分 tmux session 該值為空，導致 history handler 無法定位 JSONL 檔案。改為在 handoff 時從 CC `/status` 輸出取得 cwd 並寫入 DB
- **cwdRegex 空白行誤匹配** — `cwd:` 行僅含空白時不再匹配為有效路徑

## [0.4.1] - 2026-03-18

Bugfix: Handoff 狀態管理修正

### 修復

- **Stream→Term handoff 後 stream 頁面狀態錯誤** — handoff 完成後 `handoffState` 錯留在 `'connected'`，切回 stream tab 時顯示無法互動的對話 UI 而非 HandoffButton。現在根據 session mode 判斷，term handoff 後正確重置為 `'idle'`
- **Term→Stream handoff 載入對話歷史** — `fetchSessions` 改為 await，確保用 fresh session data（含 `cc_session_id`）取得歷史。同時移除 `msgs.length > 0` 條件，空歷史也正確覆蓋避免舊 messages 殘留
- **Relay 關閉時的誤觸事件** — `runHandoffToTerm` 在關閉 relay 前先更新 DB mode 為 `"term"`，防止 `revertModeOnRelayDisconnect` 發送假的 `"failed:relay disconnected"` 事件
- **Handoff 失敗後的 mode rollback** — `runHandoffToTerm` 的 pre-update 在後續步驟失敗時會 rollback mode 到原始值，避免留下不一致的 DB 狀態
- **Term handoff 後清理 per-session state** — 切回 term 時呼叫 `clearSession` 清除上一輪 stream 的 messages、cost、sessionInfo
- **fetchSessions 失敗時的 fallback** — 從 `'connected'`（可能導致無法互動的 UI）改為 `'idle'`（安全預設，顯示 HandoffButton 讓使用者重試）

## [0.4.0] - 2026-03-18

Phase 2.5b: Stream WS Lifecycle Redesign — 修復 stream 訊息不通的根因

### 新增

- **Per-session store** — `useStreamStore` 從全域單例改為 `Record<string, PerSessionState>`，切換 session 不再丟失對話
- **useRelayWsManager hook** — relay 事件驅動 WS 生命週期（relay:connected → 建立 WS，relay:disconnected → 關閉 WS）
- **Relay 事件廣播** — session-events WS 新增 `relay` 事件類型 + snapshot，冷啟動單一資料源
- **Init metadata 攔截** — bridge handler 捕獲 CC init message 的 model 資訊存 DB
- **JSONL history API** — `GET /api/sessions/{id}/history` 讀取 CC 的 JSONL 檔案，resume 時顯示之前的對話
- **SessionResponse DTO** — session list API 回傳 `has_relay` + `cc_model`
- **`cc_model` DB 欄位** — sessions 表新增 cc_model 欄位 + migration
- **`GetSessionByName`** — store 新增 O(1) name 查詢方法
- **`RelaySessionNames`** — bridge 新增列舉所有有 relay 的 session 方法
- **`fetchHistory`** — SPA API client 新增歷史訊息查詢函式

### 修復

- **幽靈連線根因修復** — ConversationView 不再管理 WS 連線，改為純 UI 元件從 per-session store 讀取狀態
- **WS 生命週期脫鉤** — WS 建立/銷毀完全由 relay 事件驅動，不再依賴 component mount 時機
- **set() 內 side effect** — clearSession 的 conn.close() 移到 set() 外避免 re-entrant mutation
- **selector 穩定性** — 使用 stable 空陣列常數避免 Zustand `?? []` 造成的無限 render loop
- **subscribeWithSelector** — store 加入 Zustand middleware 支援 relay status 訂閱

### 改善

- ConversationView props 簡化為 `sessionName`（移除 `wsUrl`、`sessionStatus`）
- session-events type 擴充為 `'status' | 'handoff' | 'relay'`
- bridge 測試恢復 4 個被刪除的單元測試

## [0.3.0] - 2026-03-18

Phase 2.5a: Stream Handoff — 雙向切換

### 新增

- **Stream Handoff** — term（互動式 CC）與 stream（`-p` 串流模式）之間的雙向 handoff
- **SendKeysRaw** — tmux 控制鍵注入（C-u, C-c, Escape 不帶 Enter）
- **ExtractSessionID** — 解析 CC `/status` 輸出的 Session ID（UUID regex）
- **cc_session_id** — sessions 表新欄位 + migration + CRUD
- **Handoff 8 步流程** — CC 偵測 → 中斷 → `/status` 取 ID → `/exit` 退出 → relay `--resume`
- **Handoff to Term** — 6 步反向 handoff（shutdown relay → shell → `claude --resume`）
- **HandoffButton** — CC 狀態感知、進度標籤、disabled 狀態
- **StreamInput "Handoff to Term"** — 底部操作按鈕
- **E2E pipeline 測試** — SPA→bridge→relay→subprocess→bridge→SPA 完整往返驗證
- **Relay 斷線自動 revert** — session mode 自動回 term
- **session-events snapshot** — 新 subscriber 收到初始狀態快照

### 修復

- 混合式 CC 偵測（子程序樹 + pane content fallback）
- relay command 使用 config bind address
- `--verbose` 加入 stream-json preset（CC 2.1.77+ 要求）

## [0.2.0] - 2026-03-17

Phase 2: Stream 模式 — Claude Code 結構化互動

### 新增

- **StreamManager** — `claude -p` 子程序生命週期管理（spawn / stop / pub-sub stdout）
- **WebSocket `/ws/stream/{session}`** — 雙向 NDJSON 中繼（write mutex 保護）
- **Mode Switch API** — `POST /api/sessions/{id}/mode`（term ↔ stream 切換）
- **Store.GetSession** — 單一 session 查詢
- **ConversationView** — 結構化對話渲染（markdown / 程式碼高亮 / 自動捲動）
- **MessageBubble** — user / assistant 訊息氣泡（react-markdown + rehype-highlight）
- **ToolCallBlock** — 可摺疊工具呼叫區塊（工具圖示 + 摘要）
- **PermissionPrompt** — Allow / Deny 按鈕（`can_use_tool` control_request）
- **AskUserQuestion** — radio / checkbox 選項（支援完整 protocol 格式）
- **StreamInput** — 底部訊息輸入框
- **TopBar** — session 名稱 + 三模式按鈕（term / jsonl / stream）+ Stop 按鈕
- **SessionPanel 更新** — Phosphor Icons 狀態燈號 + 底部 Settings 入口
- **useStreamStore** — stream 模式狀態管理（messages / control requests / cost）
- **stream-ws** — 訊息型別定義 + WebSocket 連線管理（含 interrupt / sendControlResponse）

### 修復

- StreamSession：readLoop 中呼叫 cmd.Wait()（防止 zombie process）
- StreamSession：Unsubscribe 關閉 channel（防止 goroutine 洩漏）
- StreamSession：Send 使用 Lock（防止 stdin write race）
- Delete handler：同時停止 stream session（防止子程序洩漏）
- SwitchMode：UpdateSession 錯誤處理 + 回滾
- main.go：st.Close() 改用 defer 保護
- switchMode API：POST 方法（修正 PUT → POST）
- isStreaming 語意：僅在使用者送訊息時啟用（非 WebSocket open）
- AskUserQuestion：回應格式符合 STREAM_JSON_PROTOCOL（含 questions + answers）
- window.__streamConn hack 改為 Zustand store 管理
- clear() 同時重置 sessionId / model
- ConversationView handlers 用 useCallback memoize

### 改善

- TopBar 三模式按鈕（term / jsonl / stream）各自 active 樣式
- TopBar 底色提亮
- Settings 文字亮度對齊 SESSIONS 標題

## [0.1.1] - 2026-03-17

### 修復

- Terminal relay 生命週期：goroutine 互相取消，防止無限 block
- WebSocket write race condition：加入 mutex 保護
- Token 認證改用 constant-time 比較，防止 timing attack
- Token 認證支援 `?token=` query param（WebSocket 無法送 header）
- Session 建立失敗時 rollback tmux session（防止孤立 session）
- Delete handler 正確處理 ListSessions 錯誤
- Batcher 釋放 mutex 後再呼叫 onFlush（防止 deadlock）
- UpdateSession / UpdateGroup 正確回傳錯誤和 ErrNotFound
- Session name 驗證（`^[a-zA-Z0-9_-]+$`）

### 改善

- 自動掃描主機上既有的 tmux sessions（不需手動透過 API 建立）
- Terminal 全寬高顯示（修正 flex layout + 初始 resize 時序）
- WebSocket 連線後送出初始 resize（防止 tmux 按 80x24 渲染）
- URL encode session 名稱（支援空格、中文等特殊字元）
- Loading overlay 帶呼吸動畫，收到資料後 300ms fade out
- Session 按鈕加 cursor pointer + 切換後自動 focus terminal
- Sidebar 文字亮度提升
- Zustand persist 只存 activeId（避免快取過期 session 資料）
- WebSocket onmessage 加 ArrayBuffer type guard
- ResizeObserver 用 requestAnimationFrame debounce
- ws.ts 修正 onerror + onclose 重複觸發 onClose

## [0.1.0] - 2026-03-17

Phase 1: Daemon 基礎 + Terminal 模式

### 新增

- **tbox daemon** — Go HTTP + WebSocket API server
  - Config 載入（TOML，自動讀取 `~/.config/tbox/config.toml`）
  - SQLite 持久化（sessions / groups CRUD）
  - tmux session 管理（建立 / 刪除 / 列出）
  - Terminal relay（WebSocket ↔ PTY 雙向中繼，含 resize）
  - DataBatcher（16ms / 64KB 輸出批次化）
  - 安全：IP 白名單（CIDR）、token 認證（constant-time 比較）、CORS
  - Session name 驗證（`^[a-zA-Z0-9_-]+$`）
  - Graceful shutdown（SIGTERM / SIGINT）

- **tbox spa** — React SPA（獨立部署）
  - Session 面板（左側選單，模式圖示，active 高亮）
  - Terminal 畫面（xterm.js + WebGL + FitAddon + resize）
  - API client（可設定 daemon base URL）
  - Session store（Zustand + localStorage 持久化）

### 架構

- Daemon 和 SPA 完全分離部署
- Daemon 是純 API server，不含前端檔案
- SPA 可封裝為 Electron 或放在獨立主機上 serve

### 技術棧

- Daemon: Go / net/http / gorilla/websocket / creack/pty / modernc.org/sqlite / BurntSushi/toml
- SPA: React 19 / Vite / xterm.js / Zustand / Tailwind CSS / Vitest
