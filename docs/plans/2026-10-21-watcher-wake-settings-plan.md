# 守門員喚醒設定 — plan（v4.1，依 spec v5.1）

> 2026-10-21 lead 1f（Opus）。依 spec v4 `drafts/2026-10-21-watcher-wake-settings-spec.md`（併入 88 審閱、codex r1 `task-mvhvokwg-4frzp2`、r2 `task-mvhw7epy-iipqq4`）。
> 盤點：`drafts/2026-10-21-watcher-wake-inventory.md`（行號以 origin/main 為準，實作者開工先重查）。
> 前提：使用者確認 spec §0 的「巡檢不可移除」做法（巡檢無開關、最長不巡上限不能關）之後才派工。**2026-10-21 21:1x 使用者已確認**（巡檢無開關；最長不巡上限 1–72 小時、預設 24、不能關；固定時刻與沒有守門員時的上限由每台執行、收件端去重）。

## 共通規則（每張 brief 都附）

- TDD；每個 task 一個 commit。
- 測試分層：開發中只跑改到的套件；PR 前 `pdx test go-changed <套件…>`＋`pdx test lint`，job tree＝PR head；動到 goroutine／鎖的加 `pdx test go-race <套件>`；改 `cmd/pdx/plugin`（skill）的加 `./cmd/pdx/...`；新增 exec 的加 execstat-guard；SPA 只動 fixture 副本時跑 `pdx test spa-file spa/src/lib/watch/fixtures-sync.guard.test.ts`。tsc 不在 mlab 跑。
- 規模：每張 ≤ 800 行或 ≤ 20 檔，超過就拆，每張都能編譯。
- codex：R1 → R2（攻擊 → critic），`--model gpt-5.6-sol`；codex 不跑完整測試。
- 只能 kill 自己的 pid；禁止 pkill -f／killall。
- 新 capability 要有「真模組註冊進 core → `/api/info` 有宣告」的測試。

## WK-1a — 文件、正本、API（A 線）

1. `internal/watchwake/`（純邏輯）：`settings.go`（§2 型別、`Default()`、`Version`）、`normalize.go`（嚴格解碼、一次收集全部 `FieldError{Path, Code, Message}`、時刻排序、各錯誤碼）、`etag.go`（canonical JSON → sha256 前 16 hex）、`migrate.go`（`FromWatchRow`）。
2. hostconfig：`WakeRowStore` 服務（`Get(key)`、`Put(key, raw, baseRevision)` 用 `Store.PutWith` 做 CAS；key 只收 `watch_wake`、`watch_wake_cache`），registry key `hostconfig.watch-wake`；不開通用 PUT 路由；測試 CAS 相符／不符、列不存在時 `Put(key, raw, 0)`、其他 key 拒絕。
3. watch 模組 `wake_settings.go`、`wake_routes.go`：正本模式讀寫、第一次讀取遷移（CAS 競爭時重讀）、`readonly`（版本較新）、`GET`／`PUT`（§5）、`revision`、`source_id`（本機 host_id）、`known_kinds`（把 `internal/module/wwdalerts/wake.go` 的 kind 名稱表抽成可匯出的表，每個 kind 補 `levels` 陣列，spec §5）、`fetched_at`（從沒取到是 null）、`patrol_status` 的 `last_fail_code`／`last_fail_detail` 欄位（本張先回 null；值域見 spec §5）、`/ws/host-events` 事件 `watch.wake_settings.changed`（`value` 只帶 revision／etag／source）、capability `watch.wake_settings.v1`（`internal/core/info_handler.go`；**不宣告** `watch.wake_rules.v1`）。
4. `internal/config/config.go`：`[watch] wake_source`（預設 mlab）；本張只回 `source`、`is_source`。
5. `cmd/pdx/device_scope.go`：GET 加入允許清單；`cmd/pdx/device_scope_test.go` 的精確集合同步；PUT 不加（middleware 回 `device_forbidden`）。
6. fixture：十個 Go 檔（`watch_wake_settings_get.json`、`…_get_stale.json`、`…_get_full.json`、`…_get_readonly.json`、`…_put_200.json`、`…_put_400.json`、`…_put_400_missing_expect.json`、`…_put_403.json`、`…_put_409.json`、`…_put_503.json`）＋SPA 副本 `spa/src/lib/watch/__fixtures__/`，403 的 body 固定為 middleware 的 `{"error":"device_forbidden","detail":"this route is not available to a paired phone"}`；**新增** `spa/src/lib/watch/fixtures-sync.guard.test.ts`（照 `lib/pool` 那支寫，現行那支寫死目錄、沒有清單可加），PR 層跑 `pdx test spa-file spa/src/lib/watch/fixtures-sync.guard.test.ts`。
- **測試**：spec §10 的驗證、CAS／並行（兩個並行 PUT、遷移 vs 第一個 PUT）、遷移、版本、capability、裝置 token。mutation：etag 與欄位順序有關、遷移帶入 `watch.enabled`、`max_gap_hours` 接受 0。
- 約 750／18 檔；超過就把 1、2 拆成前置一張。

## WK-1b — 非正本（A 線）

1. 單一更新者 goroutine：輸入（輪詢結果、變更通知、轉寫成功）走同一個 channel；每個請求記下它問的正本（alias＋`source_id`），結果回來時正本已換就丟掉，同一正本只接受較新的 revision，換正本時重設基準；快取存 `watch_wake_cache`；落地失敗保留記憶體；stale 規則；前進到新 revision 時發 WS 事件；納入 WaitGroup。
2. peer 信封路由四條（spec §4 表）＋`internal/module/peers/policy.go` 白名單＋白名單測試；用 `HostCaller` 呼叫，內層 `status`／`body` 原樣回。
3. `PUT` 在非正本：包成 `/put` 轉寫，原樣回；正本連不上 503。
4. 正本寫入成功 → 對 fleet 其他 daemon 送 `/changed`。
5. `/wake-suppressed` 接收端：正本彙總最近 50 則。
- **測試**：spec §10 的正本／非正本、並行亂序（輪詢、通知、轉寫）、切換正本；httptest 假正本；go-race。mutation：接受較舊 revision、換正本後拒收較小 revision、信封丟掉內層 body、stale 不標。
- 約 600／12 檔。

## WK-2 — 非巡檢的叫醒（A 線）

1. `internal/module/watch/wake.go` 的 `WakeWatcher`：入口讀設定一次；`reason != "patrol"` 時檢查總開關與規則開關（記 `master_off`／`rule_off`，回 nil）；`autoWake` 改為 `reason == "patrol" || settings.AutoStart`；locator 因 `autoWake=false` 回 `ErrNoWatcher` → 記 `no_auto_start`。
2. 組字一次：`watch.WakeText` 加 prompt 參數（或新函式）；本機與跨主機送同一份全文；`noteBody` 加選填 `prompt`，`handlePeerNote` 有就用。
3. wwdalerts：取用兩個服務 `watch.WakeSettingsKey`、`watch.SuppressRecorderKey`（watch 模組提供）；自己判斷總開關、`rules.wwd_alert.enabled`、`levels`、`kinds_off`。在 `claimWake` 之前（`wake.go` 看得到 `Alert.Level`／`Kind` 處）過濾：被擋的不認領、不寫 ledger（schema 不變）；`wakeOnce` 取件時再判斷一次，已 pending 而被擋的從 ledger 刪除（同 `dropPending`），不 `markWoken`；只記 suppressed；混合批次只合併允許的。
4. suppressed 紀錄：環形緩衝 50；非正本送 `/wake-suppressed`（失敗就算了）。
5. `reasonLabel` 補 `user_back`；`pdx watch config` 對舊 key 印提示（`cmd/pdx/watch_config.go`）。
- **測試**：spec §10 閘門、prompt 一致；wwd 混合批次、關著期間重啟不叫、重新打開後未解除的警報叫一次、primary 擋 → 重新打開 → standby 接任只叫一次；go-race。mutation：kind 過濾拿掉、被擋的 episode 被認領、pending 被擋時 `markWoken`、`auto_start` 擋到已清醒的守門員。
6. capability `watch.wake_rules.v1`：WK-2 與 WK-3（含各自拆出的 a／b）之中**最後 merge 的那一張**宣告（spec §5）；兩張 PR 說明都寫明由誰宣告，lead 放行時核對。宣告那張要有「真模組註冊 → `/api/info` 有宣告」的測試。
- 可能超過 800：拆成 WK-2a（1、2、4、5）與 WK-2b（3）。

## WK-3 — 巡檢與 user_back（A 線）

1. `patrol.go`：拿掉 `!s.Enabled` 早退；情境間隔改讀新文件（含 `away`）；實際間隔＝`min(情境間隔, max_gap)`；`disk_low` 門檻改讀新文件、經 6.1 判斷。
2. 基準持久化：`DataDir/watch-patrol-last.json`（上次巡檢成功的時間，暫存檔＋rename），持有者與 primary 都從它接續。
2b. 沒有守門員時（spec 6.3）：**每一台 daemon** 的巡檢 tick 用 locator 確認查無 → 評估上限 → `WakeWatcher("patrol", …)`；不看 fleet 角色與 `wake_source`。
2c. `patrol_status`：記本機最近一次巡檢成功／失敗；失敗也經 `/wake-suppressed`（`why: patrol_failed`）報給正本。
3. 固定時刻（spec 6.4）：`watch_times.go`，**每一台 daemon 都跑**：算昨天與今天的時刻、窗口判斷、邏輯鍵 `<當地日期> <HH:MM>`、本機 `watch-wake-times.json`（先寫後送、保留 8 個當地日期、送出失敗也算用掉並記 `patrol_status`）、DST、改 tz。
3b. 收件端去重：`noteBody` 加選填 `dedupe_key`；`notifyWatcher` 與 `handlePeerNote` 共用一個去重器（24 小時、落地 `watch-note-dedupe.json`），重複的回成功不轉達。
4. presence 三態：`internal/push/presence_key.go` 新增 `PresenceStateReader`，push 模組實作（`internal/module/push/presence.go`）；`atComputer()` 舊介面不變。
5. `user_back.go`：只在 fleet primary 跑；三態處理；角色切換重設；goroutine 納入 WaitGroup。
- **測試**：spec §10 的巡檢不可移除、固定時刻、user_back；時鐘注入；DST 用 `America/New_York`；go-race。mutation 見 spec §10。
- 可能超過 800：拆成 WK-3a（1、2、2b、2c、3、3b）與 WK-3b（4、5）。3b 動到收件端，WK-2 的 `noteBody` 變更要先 merge。

## WK-4 — CLI 與 skill（A 線）

- `cmd/pdx/watch_wake_settings.go`＋`cmd/pdx/watch_cmd.go` 分派 `wake-settings`（`wake` 仍是手動叫醒）＋usage；path 解析與 spec §5 同一套；`@file`；409→3、400→4。
- pdx-watch skill 更新；`embed_test.go` 若有斷言同步。
- **測試**：httptest GET／PUT；path 解析；結束碼；`pdx watch wake` 仍走手動叫醒（防撞名迴歸）；`./cmd/pdx/...`。
- 約 400／7 檔。

## WK-5a／5b — App（B 線，88）

- 細部 plan 由 88 寫。容器已定（spec §7）：守門員專區固定分頁 `watch-settings`（`lib/watch/ids.ts` 的 `WATCH_SETTINGS_TAB_ID`、pane kind、`features/workspace/lib/watch-area.ts` 的排序、`components/watch/WatchSettingsPane.tsx`）。另須處理：`WatchSettings.tsx`／`host/SettingsTab.tsx` 瘦身、`en.json`／`zh-TW.json` 與 locale completeness、有無守門員 session 兩態、fixture 副本的讀寫測試、409／readonly／stale／裝置唯讀。
- 88 的拆法（10-21）：5a-1 讀取、5a-2 寫入、5b-1 分頁與狀態、5b-2 巡檢與規則卡、5b-3 警報種類、紀錄與主機分頁瘦身，疊加 PR，第一張等 WK-1a merge。
- 新畫面只在 `watch.wake_rules.v1` 出現時建立（spec §5），所以 WK-5 的 PR 可以各自放行、隨任何一版出；宣告 `wake_rules.v1` 的那一版打包 Mac App。

## 順序與出版

```
WK-1a → WK-1b → WK-2(a,b) → WK-3(a,b) ┐
WK-1a → WK-4                           ├→ 宣告 watch.wake_rules.v1 的那一版出現新畫面（打包 Mac App）
WK-1a → WK-5a、WK-5b ──────────────────┘
WK-3 → 9d-3b → 9c-a → 9c-b → 9c-c（WWD-9）
```

- 人力：raj（_i3s2he）7b2-0a（0a-1、0a-2）；freya N-2。WK 系列由先空出來的 member 接，WK-1a 優先；A 線上限 2。
- 部署檢查（加進那一版的 release 腳本）：mlab `GET /api/watch/wake-settings` 的 `is_source: true`、遷移值正確、`revision ≥ 1`；air26 `is_source: false`、`stale: false`、revision 與 mlab 相同；`/api/info` 有 capability。
- 回滾：新列留著無害；退到 WK-2 前，叫醒恢復讀 `watch` 列（值是遷移時的舊值，使用者在新版的改動不會帶回去，PR 說明寫明）。

## 對 WWD-9 plan 的修訂（已同步寫進 `2026-10-21-wwd9-removal-plan.md`）

見 spec §8：9c-a 搬 `deliveryState.notifier`、9c-b-ii 不刪 `auto_wake` 且先搬 `ValidateNote`、9c-c 先搬 `peerNotifier`／`WatcherNotifier`、9d-3b 同張改巡檢資料源、S3-3c 移除、順序 WK-3 → 9d-3b → 9c。
