# wwd 監控資料缺口 — plan（WWD-9 之前必要的部分）

Spec：`docs/specs/2026-10-18-wwd-spec.md`（主要是 §4.1、§10、§13.3）。設計來源：B 線的監控分頁組原型第 10 節「資料缺口（給 1f）」
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
  （`wire/alert.go:176-180`）；事件串流有 `alert`（`wire/enums.go:142`），payload 只有 `{host}`，讀者要重讀清單。Purdex 的
  `internal/module/wwdalerts` 每 30 秒輪詢（`module.go:40`），`RegisterRoutes` 是空的（`:159`）。
- 失聯：只有一個 kind `silence`（`wire/alert.go:71`），兩種失聯靠 detail 文字分（`alert.go:77-91`，`SilenceHostDown(detail)`），
  因為 `/v1/alerts` 是 strict 解碼，當時不加欄位。`alert_unreachable` 是每個 peer 一個的 `*bool`（`daemon/config.go:165`）。
- 觀察：wwd 有 `GET /v1/observations` 與 `/v1/observations/undeclared`（`daemon/observe_routes.go:39-40`，經 `viaHost`）；
  Purdex 沒有路由，client 套件也沒有方法。
- 重／輕：`wire.Job`（`wire/jobs.go:91-123`）沒有 slot；slot 只在寫 usage 時由 `slotOf`（`daemon/usagerec.go:180-185`：
  `Local && !AdHoc` → light，其餘 heavy）推出來。快速通道的本機執行登記後是一筆 `Local: true` 的 job，會出現在 `GET /v1/jobs`。
- running 的時間：`queue/claim.go:69-92 Start` 只寫 `state='running'`、`lease_deadline`、`updated_at`（`:80`），**沒有存開始時間**；
  `Claim.Since` 是領取時間，`Result.StartedAt` 只在結束後才有。
- 現成的轉送寫法：`internal/module/pool/module.go:369-382 handleExceptions`（`validAlias` → `m.serve(w, r, "key|"+alias, fn)` →
  client；錯誤經 `historyError`，wwd 的 4xx 原樣帶 `wwd_rejected`，舊 wwd 的 404 回 `known: false`）。
- strict 解碼：`wire.Decode`（`wire/decode.go:15-18`）是 Purdex 讀 jobs、alerts、pool 與 wwd 對等體讀取的共同路徑；SPA 用手寫
  parser，不拒多餘欄位。#3710（mei，進行中）把回應、推送、對等體與落盤的讀者改成 `DecodeLenient`。

## 設計決定

1. **能在 Purdex 端衍生的，就不動 wwd 的 wire**：MD-1 到 MD-4 只改 Purdex daemon 的回應（App 用的 JSON 由 Purdex 自己組，SPA 不
   strict），與 wwd 的版本無關，可以隨時出。只有 MD-5 一定要在 wwd 存新的東西。
2. **MD-5 等 #3710 的轉換版**：job 上的新欄位對 764／765 的 strict 讀者是破壞性變更。MD-5 只能在「四台（含 air19）都已升到 #3710
   所在的轉換版」之後的版本出；放行前查 `/v1/pool` 四台的 version。
3. **對應主機用 wwd 的 host_id**：App profile 的主機外觀與順序已決定以 host_id 為 key（S1／S4），對應 daemon 也用同一把 key，不再
   靠 `/api/fleet` 的 alias 與 kind（WWD-9 移除）。

## 任務

### MD-1 `/api/info` 帶本機 wwd 的身分（S2、S3）
- `handleInfo` 多一個鍵 `wwd: {host_id, alias}`：讀本機 wwd 的 `/v1/info`（已有快取就用快取；讀不到就**省略這個鍵**，不要回空字串）。
  能力 `pool.identity.v1`（與 `pool.v1` 同條件）。
- App（B 線）的對法：一台 pool 主機「有 daemon」＝某個 HostConfig 的 `/api/info.wwd.host_id` 等於它的 `info.host_id`；這個 HostConfig 就是它
  對到的 daemon（S3）。`matchPoolHost` 改用這個，不再用 fleet 的 kind。
- 測試：wwd 可讀 → 帶 `wwd`；wwd 不可讀 → 沒有這個鍵；能力在與不在；host_id 以本機 wwd 的為準（不是 Purdex 的 `Cfg.HostID`）。
- spec §10「For the App」補一條。規模約 80 行。

### MD-2 告警的讀取路由與事件（S5，加上 S15 的種類）
- `GET /api/pool/hosts/{alias}/alerts`：照抄 `handleExceptions`，問本機 wwd `GET /v1/alerts?host=<alias>`；回
  `{host, known, alerts}`，`alerts` 每一筆是 wwd 的 `Alert` 加上 Purdex 衍生的 `silence: "wwd_down" | "host_down" | "unknown"`
  （只有 kind 是 `silence` 時才有；用 `SilenceHostDown(detail)` 與 `SilenceDetailWwdDown` 判斷，寫成 wire 套件的一個純函式，讓 wwd
  與 Purdex 共用）。舊 wwd 沒有路由（404）→ `known: false`。裝置 token 可讀（加進 allow-list）。快取 1 秒，與其他讀取一致。
- host event `alerts.changed {host}`：pool 模組的 watch 已經讀本機 wwd 的事件串流與 `/v1/pool`；收到本機 wwd 的 `alert` 事件就發
  `alerts.changed`，每台每 2 秒最多一次（沿用 `pool.changed` 的合併規則）。別台的告警：先查清楚本機 wwd 的事件串流會不會轉來 peer 的
  `alert`（`daemon/events.go:154` 一帶）；不會的話，App 對別台每 30 秒重讀一次（與 wwdalerts 的輪詢同節奏），並在 spec 寫明。
  能力 `pool.alerts.v1`。
- 測試：轉送與錯誤對照（404 → known:false、502、503、4xx → wwd_rejected）；`silence` 三種衍生值各一；`alerts.changed` 的合併與首讀
  不發；裝置 token 可讀、不能寫。
- spec §10、§13.3 補。規模約 300 行。

### MD-3 觀察紀錄的讀取路由（S6）
- `internal/wwd/client` 加 `Observations(ctx, host, since, until)` 與 `Undeclared(ctx, host, since)`（照 `alerts.go` 的寫法）。
- `GET /api/pool/hosts/{alias}/observations?since=&until=` 與 `/undeclared?since=`：轉送，回 wwd 的回應原樣（`ObservationsResponse`、
  `UndeclaredResponse`，`wire/undeclared.go`），錯誤同 MD-2。`since` 沿用 series 的寫法（RFC 3339 或往回的時長，最多 14 天）。
  能力 `pool.observations.v1`。
- 測試：轉送、參數驗證（壞的 since 回 400 且不問 wwd）、截斷旗標原樣、錯誤對照。
- spec §10、§8.4 補。規模約 200 行。

### MD-4 工作的重／輕（S11）
- 把 `slotOf` 搬到 wire 套件成為 `wire.JobSlot(Job) Slot`（純函式；`daemon/usagerec.go` 改呼叫它，行為不變）。
- Purdex 的 `GET /api/pool/jobs` 與 `/api/pool/jobs/{id}` 回應的每一筆 job 多一個衍生欄位 `slot`（"heavy" | "light"），由 Purdex 端算，
  不加在 `wire.Job` 上。
- 已知限制（寫進 spec）：快速通道在等名額（收到 429）的期間還沒有登記，所以看不到；要看得到需要 wwd 記錄等待者，等有需要再開。
- 測試：`JobSlot` 的表格（local、local+adhoc、佇列工作）；兩條路由都帶 `slot`；usage 的 `slot_charged` 不變（回歸）。
- 規模約 100 行。

### MD-5 claimed → running 的時間點（S13）
- queue schema +1：`jobs` 加 `started_at`（可為 null）；`Start`（`queue/claim.go:69-92`）在同一個 UPDATE 寫入；遷移照既有的寫法，舊列為
  null。`wire.Job` 加可選的 `started_at`（`omitempty`），只在 running 與之後的狀態出現；`Result.StartedAt` 不變。
- **前提**：#3710 的轉換版已經在四台都部署（決定 2）。PR 說明附放行時 `/v1/pool` 四台的 version。
- 退版：比照 #3706 的寫法（還原 `wwd.db.pre-<版本>`、給新 epoch）；寫進 PR 說明，lead 放進部署腳本。
- 測試：Start 之後 `started_at` 有值且不晚於現在；claim 之後、start 之前為 null；遷移（舊庫升上來為 null）；JSON 往返；舊結構體
  （凍結副本）用 lenient 讀新記錄成功。
- spec §4.1、§4.2 補。規模約 250 行。

## 順序與分工

- MD-1、MD-3、MD-4 彼此獨立，可並行；MD-2 跟 MD-3 都改 pool 模組的路由註冊（`module.go:195-198`），排在同一個 member 身上依序做，避免衝突。
- MD-5 等 #3710 的轉換版部署到四台之後才開工。
- 建議：hana 做完 S2-b0b、S2-b 之後接 MD-2 → MD-3；mei 做完 #3710 之後接 MD-4 → MD-5；MD-1 給先空出來的人。每張 PR ≤ 800 行／20 檔。
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
