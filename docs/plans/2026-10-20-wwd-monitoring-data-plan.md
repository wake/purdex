# wwd 監控資料缺口 — plan（WWD-9 之前必要的部分；rev 2）

Rev 2 回應 codex plan review（task-mvg6pznt-ss38rg，8 條 important、2 條 minor，全部採納）：MD-2 定案「Purdex 輪詢＋本機事件提早重讀」；
MD-5 的部署前提加上兩台 Purdex daemon、補「回到 queued 清掉 started_at」與遷移測試；MD-4 改成 Purdex 自己的回應型別；MD-1 指定由
pool 模組持有身分、`/api/info` 不在請求中呼叫 wwd，並記下 iOS 的查證；MD-3 補 `until`／`next_until` 的測試；更正行號。

Spec：`docs/specs/2026-10-18-wwd-spec.md`（主要是 §4.1、§4.2、§8.4、§10、§12、§13.1、§13.3）。設計來源：B 線的監控分頁組原型第 10 節「資料缺口（給 1f）」
（https://pages.mlab.host/wake/purdex-b-line/monitoring-design.html，88 的 Opus 設計 subagent，2026-10-20；原始檔在 B 線 worktree 的 `docs/pages/b-line/`）。Lead 1f，2026-10-20。

## 背景與範圍

使用者 10-20 定案：wwd 不與舊系統雙軌，S1 → S2 → S3 → S4（WWD-9 移除 `fleet.v1`、`watch.v1`、`gate.decisions.v1`）。
S4 的前提是 App 只讀 pool 與 jobs 的 API。原型第 10 節列了 Purdex daemon 還缺的欄位與路由：G1–G17 是圖表指標（儀表板加分），
S1–S15 是骨架與頁面需要的。這份 plan 只處理 **WWD-9 之前一定要有的 S 項**；G 項等使用者看完原型、定好圖表優先序後另開 plan。

| 項目 | 原型的需求 | 這份 plan 的處理 |
|---|---|---|
| S1 主機順序、S4 主機外觀 | 側欄、分頁的順序與外觀 | **不在這裡**：存 App profile，以 wwd 的 host_id 為 key（1f 10-20 裁定、88 同意），B 線做 |
| S2 kind、S3 對到 HostConfig | 這台有沒有 Purdex daemon；從主機頁開 session、推播設定寫到哪 | **MD-1** |
| S5 告警給 App 讀 | 事件頁、盾牌數字 | **MD-2** |
| S15 兩種失聯的種類 | 事件頁、主機設定的告警 | **MD-2**（種類）；每台開關的寫入歸 S9，不在 WWD-9 前提內 |
| S6 觀察紀錄 | 事件頁的「觀察」、未宣告清單 | **MD-3** |
| S11 工作的重／輕 | 側欄佇列條兩種顏色、任務佇列 | **MD-4** |
| S13 claimed → running 的時間 | 任務佇列「準備中 → 執行中」的計時 | **MD-5**（唯一要動 wwd wire 與 schema 的一項） |
| S12 mserv／air19 的 jobs.changed | 任務佇列的即時性 | **延後**（見最後一節） |
| S7、S8 incident、S9、S10 設定 API | 守門員頁、主機設定 | **不在這裡**：S7／S8 隨 WWD-8b；S9／S10 另開 |
| S14 liveness | — | 已完成（#3683，763） |

## 查證的事實（origin/main 3a350e432）

- `/api/info`：`internal/core/info_handler.go:168 handleInfo`，以 `map[string]any` 組回應（`:214-226`），`host_id` 是 Purdex 自己的
  `Cfg.HostID`（`:215`）；**沒有**本機 wwd 的 host_id 或 alias。
- `/api/pool/hosts`：`wire.PoolHost`（`internal/wwd/wire/api.go:310-322`），`handleHosts` 在 `internal/module/pool/module.go:357`；
  **沒有** kind 或 daemon 相關欄位。App 的 `matchPoolHost`（`spa/src/lib/pool/cell-model.ts:37-39`）靠 `/api/fleet` 的 alias／kind，
  或「本次 session 驗證過」的 `daemonId`。
- 告警：wwd 的 `GET /v1/alerts`（`internal/wwd/daemon/alerts_routes.go:32`，經 `viaHost`，可帶 `?host=`），回 `wire.AlertList`
  （`wire/alert.go:176-180`）；事件串流有 `alert`（`wire/enums.go:142`），payload 只有 `{host}`，讀者要重讀清單。**`alert` 事件只走本機
  socket 的串流，對等體的 TCP 串流不帶**（`PublishAlert` 以 `adHoc=true` 排除 TCP 訂閱者，`internal/wwd/daemon/events.go:142-148`；
  spec §13.1），所以別台的告警只能輪詢。Purdex 的 `internal/module/wwdalerts` 每 30 秒輪詢（`module.go:40`），`RegisterRoutes` 是空的（`:159`）。
- 失聯：只有一個 kind `silence`（`wire/alert.go:71`），兩種失聯靠 detail 文字分（`alert.go:77-91`，`SilenceHostDown(detail)`），
  因為 `/v1/alerts` 是 strict 解碼，當時不加欄位。`alert_unreachable` 是每個 peer 一個的 `*bool`（`daemon/config.go:165`）。
- 觀察：wwd 有 `GET /v1/observations` 與 `/v1/observations/undeclared`（`daemon/observe_routes.go:39-40`，經 `viaHost`）；
  Purdex 沒有路由，client 套件也沒有方法。
- 重／輕：`wire.Job`（`wire/jobs.go:90-123`）沒有 slot；slot 只在寫 usage 時由 `slotOf`（`daemon/usagerec.go:175-185`：
  `Local && !AdHoc` → light，其餘 heavy；spec §8.5）推出來。快速通道的本機執行登記後是一筆 `Local: true` 的 job，會出現在 `GET /v1/jobs`。
  Purdex 的 `/api/pool/jobs` 直接把 client 解出的 `wire.JobsListResponse`（`Jobs []wire.Job`，`wire/api.go:353-364`）寫成 JSON
  （`internal/module/jobs/module.go:209-251`）；單筆把 `wire.Job` 放進 `wire.JobDetail`（`module.go:254-293`、`wire/api.go:430-436`）。
- running 的時間：`queue/claim.go:67-92 Start` 只寫 `state='running'`、`lease_deadline`、`updated_at`，**沒有存開始時間**；
  `Claim.Since` 是領取時間，`Result.StartedAt` 只在結束後才有。running 會因 worker release 或 lease 過期回到 queued（spec §4.2）。
  queue schema 現在是 8（`internal/wwd/queue/schema.go:10-16`，遷移的 case 在 `:38-77`）。
- `/api/info` 的讀者：SPA 不 strict；iOS 的 `WireInfo` 是標準 `Decodable`、預設 `JSONDecoder`，會忽略未知的鍵（purdex-ios 049191b，
  `App/Sources/Net/Wire.swift:82`、`App/Sources/Net/DaemonClient.swift:190`；現在的 `/api/info` 已經有很多它沒宣告的鍵）。
- 現成的轉送寫法：`internal/module/pool/module.go:367-381 handleExceptions`（`validAlias` → `m.serve(w, r, "key|"+alias, fn)` →
  client；錯誤經 `historyError`，wwd 的 4xx 原樣帶 `wwd_rejected`，舊 wwd 的 404 回 `known: false`）。
- strict 解碼：`wire.Decode`（`wire/decode.go:15-18`）是 Purdex 讀 jobs、alerts、pool 與 wwd 對等體讀取的共同路徑；SPA 用手寫
  parser，不拒多餘欄位。#3710（mei，進行中）把回應、推送、對等體與落盤的讀者改成 `DecodeLenient`。

## 設計決定

1. **能在 Purdex 端衍生的，就不動 wwd 的 wire**：MD-1 到 MD-4 只改 Purdex daemon 的回應（App 用的 JSON 由 Purdex 自己組，SPA 不
   strict），與 wwd 的版本無關，可以隨時出。只有 MD-5 一定要在 wwd 存新的東西。
2. **MD-5 等 #3710 的轉換版（765）**：job 上的新欄位對轉換版以前的 strict 讀者是破壞性變更。讀者不只四台 wwd，還有兩台 Purdex daemon：
   `/api/pool/jobs` 的 client（`internal/wwd/client/jobs.go:26-38`、`:211-234`）、`jobs.changed` 與 jobnotify 的事件
   （`internal/module/jobs/events.go:131-141`、`internal/module/jobnotify/module.go:179-188`），以及對等體 worker 讀別台的 job 清單
   （`internal/wwd/worker/pick/source.go:62-89`）。MD-5 只能在「四台 wwd **與** mlab、air26 兩台 Purdex daemon 都已升到 765 以上」之後的
   版本出；放行前查 `/v1/pool` 四台的 version 與兩台 `/api/health` 的 version。
3. **對應主機用 wwd 的 host_id**：App profile 的主機外觀與順序已決定以 host_id 為 key（S1／S4），對應 daemon 也用同一把 key，不再
   靠 `/api/fleet` 的 alias 與 kind（WWD-9 移除）。

## 任務

### MD-1 `/api/info` 帶本機 wwd 的身分（S2、S3）
- **誰持有**：pool 模組。它的 watch 每 5 秒讀本機 wwd 的 `GET /v1/pool`，回應的第一筆就是本機（`self`），從那裡取 `{host_id, alias}` 存在
  模組內（換值時更新，wwd 不見時清空）。pool 模組的 `Status`（`internal/module/pool/module.go:186-192`）多回這兩個值；core 的 `handleInfo`
  （`internal/core/info_handler.go:167-235`）照讀 wwdalerts Status 的方式讀它，**`/api/info` 的請求路徑上不呼叫 wwd**，所以 wwd 慢或不在
  都不會拖住 `/api/info`。
- `/api/info` 多一個鍵 `wwd: {host_id, alias}`；pool 模組還沒讀到（剛啟動、wwd 不在）就**省略這個鍵**，不回空字串。能力 `pool.identity.v1`
  加在 `info_handler.go:142-149` 那一組，條件是「這個鍵現在有值」。
- App（B 線）的對法：一台 pool 主機「有 daemon」＝某個 HostConfig 的 `/api/info.wwd.host_id` 等於它的 `info.host_id`；這個 HostConfig 就是它
  對到的 daemon（S3）。`matchPoolHost` 改用這個，不再用 fleet 的 kind。
- iOS 不受影響（`WireInfo` 忽略未知的鍵，見「查證的事實」）。
- 測試：watch 讀到之後帶 `wwd`；還沒讀到、wwd 不在 → 沒有這個鍵、沒有能力；host_id 以本機 wwd 的為準（不是 Purdex 的 `Cfg.HostID`）；
  wwd 卡住時 `/api/info` 照常立即回（不等 wwd）。
- spec §10「For the App」補一條。規模約 120 行。

### MD-2 告警的讀取路由與事件（S5，加上 S15 的種類）
- `GET /api/pool/hosts/{alias}/alerts`：照抄 `handleExceptions`，問本機 wwd `GET /v1/alerts?host=<alias>`；回
  `{host, known, alerts}`，`alerts` 每一筆是 wwd 的 `Alert` 加上 Purdex 衍生的 `silence: "wwd_down" | "host_down" | "unknown"`
  （只有 kind 是 `silence` 時才有；用 `SilenceHostDown(detail)` 與 `SilenceDetailWwdDown` 判斷，寫成 wire 套件的一個純函式，讓 wwd
  與 Purdex 共用）。這是 Purdex 自己的回應型別，不改 `wire.Alert`。舊 wwd 沒有路由（404）→ `known: false`。裝置 token 可讀（加進
  allow-list）。快取 1 秒，與其他讀取一致。
- host event `alerts.changed {host}`（**定案：Purdex 輪詢，本機事件只用來提早重讀**）：pool 模組新增一個 alerts watch，每 30 秒對每台主機讀
  一次告警清單（本機直接讀、別台經 `?host=`），跟上一次比對內容，變了才發 `alerts.changed {host}`；本機 wwd 的串流來了 `alert` 事件，就
  立刻重讀本機那一份，不等 30 秒。別台的 `alert` 事件不會進來（`events.go:142-148`、spec §13.1），所以別台最慢 30 秒。規則沿用
  `pool.changed`：首讀只當基準、不發；每台每 2 秒最多一次、窗內的變化合併到結尾；wwd 不見時對已知的每台各發一次，wwd 回來後的第一次
  讀取對曾經有、現在有的每台各發一次。能力 `pool.alerts.v1`，只在這個 watch 跑著時宣告。App 看到這個能力就不再自己輪詢告警。
- 測試：轉送與錯誤對照（404 → known:false、502、503、4xx → wwd_rejected）；`silence` 三種衍生值各一；alerts watch：首讀不發、別台清單
  變了下一輪才發、本機 `alert` 事件立即重讀並發、內容沒變不發、2 秒合併、wwd 斷線與恢復各發一次；能力只在 watch 跑著時出現；裝置
  token 可讀、不能寫。
- spec §10、§13.3 補。規模約 350 行。

### MD-3 觀察紀錄的讀取路由（S6）
- `internal/wwd/client` 加 `Observations(ctx, host, since, until)` 與 `Undeclared(ctx, host, since)`（照 `alerts.go` 的寫法）。
- `GET /api/pool/hosts/{alias}/observations?since=&until=` 與 `/undeclared?since=`：轉送，回 wwd 的回應原樣（`ObservationsResponse`、
  `UndeclaredResponse`，`wire/undeclared.go`），錯誤同 MD-2。`since`、`until` 照 spec §8.4 的格式，Purdex 先驗證，壞的回 400 且不問 wwd；
  `until` 原字串轉給 wwd，回應的 `next_until` 原字串回給 App，不重新格式化（分頁靠它）。能力 `pool.observations.v1`。
- 測試：轉送；壞的 `since`、壞的 `until` 各回 400 且不問 wwd；`truncated` 與 `next_until` 原樣（含時區與小數秒的字串不被改寫）；
  以 `next_until` 翻下一頁時，邊界是 inclusive，同一筆可能在兩頁各出現一次（spec §8.4），測試把這個寫成預期；錯誤對照。
- spec §10、§8.4 補。規模約 220 行。

### MD-4 工作的重／輕（S11）
- 把 `slotOf` 搬到 wire 套件成為 `wire.JobSlot(Job) Slot`（純函式；`daemon/usagerec.go` 改呼叫它，行為不變）。
- Purdex 自己的回應型別（`internal/module/jobs`）：`PoolJob struct { wire.Job; Slot string \`json:"slot"\` }`（內嵌，JSON 欄位攤平），
  清單 `PoolJobsList`（`Jobs []PoolJob` 加上原本的 `cursor`、`epoch` 等所有欄位）、單筆 `PoolJobDetail`（`Job PoolJob` 加上原本的 `log`
  等所有欄位）。`module.go:209-251` 與 `:254-293` 兩個 handler 改成逐筆轉換後再寫出；不改 `wire.Job`，也不改 wwd 的回應。
- 已知限制（寫進 spec）：快速通道在等名額（收到 429）的期間還沒有登記，所以看不到；要看得到需要 wwd 記錄等待者，等有需要再開。
- 測試：`JobSlot` 的表格（local、local+adhoc、佇列工作）；兩條路由都帶 `slot`；**golden 對照**：轉換後除了多一個 `slot`，其餘欄位
  （job 的每個欄位、`cursor`、`epoch`、`log`）與轉換前的 JSON 逐鍵相同；usage 的 `slot_charged` 不變（回歸）。
- 規模約 150 行。

### MD-5 claimed → running 的時間點（S13）
- queue schema 8 → 9：`jobs` 加 `started_at`（可為 null）。遷移照 `queue/schema.go:38-77` 的 case 寫法；可重跑；`queue_schema` 寫成 9；
  舊庫的列為 null。
- 寫入與清除：`Start`（`queue/claim.go:67-92`）在同一個 UPDATE 寫入 `started_at`；**回到 queued 的每一條路徑都清成 null**（worker release、
  lease 過期，spec §4.2）；再次 Start 寫入新值。終態保留最後一次的值。
- wire：`wire.Job` 加可選的 `started_at`（`omitempty`）；`Result.StartedAt` 不變。
- **前提**：決定 2 的條件成立（四台 wwd 與兩台 Purdex daemon 都 ≥ 765）。PR 說明附放行時的 version 證據；lead 在部署腳本裡再查一次，
  不成立就停。
- 退版：763／764 的 wwd 會拒絕 queue schema 9。步驟比照 #3706：停 wwd → 還原 `wwd.db.pre-<版本>`（刪 `-wal`、`-shm`）→ 給新 epoch
  （spec §12、`docs/wwd-deploy.md` 的 Roll back）→ 啟動舊版。PR 說明寫明，lead 放進部署腳本。
- 測試：claim 之後、start 之前為 null；Start 之後有值且不晚於現在；release 回 queued 清成 null、lease 過期回 queued 清成 null、再次 Start
  換成新值；遷移 8 → 9、重跑遷移不出錯、`queue_schema` 為 9；舊 binary 開 schema 9 的庫被拒絕（錯誤訊息）；JSON 往返。相容路徑四條
  （#3710 之後的 lenient 讀者讀帶 `started_at` 的記錄都成功）：Purdex 的 jobs 清單、單筆 job、對等體 worker 讀別台的佇列、`jobs.changed`
  與 jobnotify 的事件；另用凍結的舊結構體（strict）讀新記錄失敗，把「不能退回轉換版以前」寫成可執行的文件。
- spec §4.1、§4.2、§12 補。規模約 300 行。

## 順序與分工

- MD-1、MD-3、MD-4 彼此獨立，可並行；MD-2 與 MD-3 都改 pool 模組的路由註冊（`module.go:195-198`），排在同一個 member 身上依序做，避免衝突；
  MD-1 與 MD-2 都改 pool 模組的 watch，也排在一起。
- MD-5 等決定 2 的條件成立（765 在四台 wwd 與兩台 daemon 部署完）才開工。
- 建議：mei 先做 MD-4，然後 MD-5（等 765）；hana 做完 S2-b0b、S2-b 之後接 MD-1 → MD-2 → MD-3。每張 PR ≤ 800 行／20 檔，預估超過先報。
- App 那一側（matchPoolHost 改用 host_id、事件頁讀告警與觀察、佇列條兩種顏色、準備中 → 執行中的計時）由 B 線接，資料到了就能做。

## 延後的項目與理由

- **S12（mserv、air19 當 origin 的 jobs.changed）**：job 存在 origin 那台的佇列；mlab、air26 的 job 不論在哪台跑，都會出現在它們自己的
  事件裡（`module/jobs/events.go:31-59`、`jobsapi/worker.go:27-32`）。現在沒有人從 mserv、air19 送工作，App 對這兩台每 10 秒輪詢已足夠
  （spec §10 的 Known gaps 已寫）。等有人從那兩台送工作再處理。
- **S15 的每台開關可寫**：讀取在 MD-2 已能分辨兩種失聯；把 `alert_unreachable` 分成兩種、並開放 App 寫入，屬 S9（告警門檻的讀寫 API），
  不是 WWD-9 的前提。

## 驗收

- MD-1 到 MD-4 部署後：App（B 線）不讀 `/api/fleet` 也能對到 daemon、顯示告警與觀察、分重輕。
- MD-5 部署後：任務佇列能顯示「準備中 → 執行中」的時間。
- WWD-9 的 C4 驗收（App 與入口都還能用、沒有人讀退場的路徑）以這份 plan 的路由為準。
