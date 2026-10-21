# 守門員喚醒設定（watcher wake settings）— spec

> 2026-10-21 lead 1f（Opus）。**v5.1**（21:5x，併入 88 審 WK-5 時提的六點：capability 分兩個、`known_kinds.levels`、`fetched_at` null、`patrol_status` 代碼、事件內容、403 body、SPA fixture 守門測試、§7 檔案清單；codex 不再審）。**v5**：v2 併入 88 的審閱；v3 併入 codex r1 `task-mvhvokwg-4frzp2`；v4 併入 r2 `task-mvhw7epy-iipqq4`；v5 併入 r3 `task-mvhwklrv-1e34eo`（全文 `drafts/codex-wake-plan-review-r3.md`；對照表在文末）。
> 依據：
> - 使用者 10-21：「守門員身分不改，一樣是守門員」「喚醒的條件提供成可設定，包括定時巡檢，可改 prompt／設定時間間距／定時」「守門員的整套巡檢機制可以放寬執行週期與情境，但是禁止移除」。
> - 現況盤點 `drafts/2026-10-21-watcher-wake-inventory.md`（以下稱「盤點」）。
> - 同步收斂（使用者 10-19）：守門員設定屬「所有主機都該一樣」，App 沒開也要一致；收斂定案前用「單一正本＋讀取」，註明「暫定、待收斂」。

## 0. 目標、巡檢不可移除、不做的事

**目標**：每一種自動叫醒守門員的條件都能各自帶一段 prompt；巡檢以外的條件都能單獨開關；巡檢能依情境放寬間隔、加「每天固定時刻」；設定全體一份，App 與 CLI 都能改。

**巡檢不可移除**：巡檢沒有開關；總開關、`auto_start`、舊的 `watch.enabled`、每台的 `mode` 都不影響它。放寬的極限由**最長不巡上限**（`patrol.max_gap_hours`，1–72 小時，預設 24，不能關）保證：不論情境間隔設成多少（含 0）、守門員是否常駐、是否有守門員在跑，距上次巡檢超過這個時間就一定巡一次；沒有守門員時由**每一台 daemon** 各自檢查並把它啟動（§6.3），不依賴 fleet 角色、正本或任何其他設定。

**保證的範圍**：任何**設定**都無法讓巡檢不發生。主機或網路故障（primary 與 standby 都離線、`targets` 全部連不上）屬於運作故障，不在設定保證之內：由既有的 wwd `silence`／`partner_down` 警報與 §5 的 `patrol_status`（最近一次巡檢成功／失敗）讓使用者看得到。

**不做**：不改守門員的身分、名稱、啟動設定（`agent_command`、`workdir`、`targets`、`mode`、`patrol_idle_min`、`start_prompt`、`wake_prompt` 留在 `watch` 列）；不改「誰負責叫」（§6）；不改 wwd 端的警報規則與門檻（`[advisor] watch_rules`／`queue_rules` 決定警報**有沒有**，本 spec 決定**要不要叫守門員**）；v1 不做依星期幾的排程；手動叫醒（`pdx watch wake`、`POST /api/watch/wake`、`/start`）不受本設定限制。

## 1. 現況摘要（細節見盤點）

| reason | 觸發 | 誰叫 | 現在能設定的 |
|---|---|---|---|
| `patrol` | 距上次通知 ≥ 情境間隔 | 持有守門員的主機（沒有守門員時不叫） | `watch.enabled`（只擋巡檢）、`patrol_present/busy/calm/rest_min` |
| `disk_low` | 剩餘 < `disk_low_gb`（巡檢內） | 同上 | `disk_low_gb` |
| `wwd_alert` | 任何 wwd 警報 episode | fleet primary；standby 在 primary 確定不行時 | 無 |
| `unattended_changed` | 無人值守開關改變 | 切換的那台 | 無 |

`auto_wake`（找不到清醒的守門員時要不要叫起或啟動）對所有來源有效；它是傳給 `Locator.Current(targets, autoWake)` 的參數，已有清醒的守門員時照樣送達（`internal/watch/locate.go:311-327`）。

## 2. 設定文件

全體一份（§4）；寫入一律整份取代，所有欄位必填。

```json
{
  "version": 1,
  "enabled": true,
  "auto_start": true,
  "rules": {
    "patrol": {
      "prompt": "",
      "interval_min": { "resting": 10, "present": 30, "away": 30, "busy": 10, "calm": 15 },
      "max_gap_hours": 24,
      "times": [],
      "tz": "Asia/Taipei"
    },
    "disk_low":           { "enabled": true, "prompt": "", "below_gb": 14 },
    "wwd_alert":          { "enabled": true, "prompt": "", "levels": ["push", "digest", "watch"], "kinds_off": [] },
    "unattended_changed": { "enabled": true, "prompt": "" },
    "user_back":          { "enabled": true, "prompt": "", "away_min": 30 }
  }
}
```

| 欄位 | 意義 | 預設（＝現行行為） | 範圍 |
|---|---|---|---|
| `enabled` | 總開關：關掉時巡檢以外的自動叫醒都不發生 | true | bool |
| `auto_start` | 巡檢以外的叫醒，在沒有清醒的守門員時能不能叫起或啟動（現 `auto_wake`）；巡檢一律可以 | 現 `watch.auto_wake` | bool |
| `rules.<非巡檢>.enabled` | 這一種叫醒開不開（巡檢沒有這個欄位） | true | bool |
| `rules.*.prompt` | 這一種叫醒時附在通知後的文字（§3） | "" | ≤ 2000 bytes、UTF-8，可含換行與 tab，其他控制字元與 U+2028／2029 拒絕 |
| `patrol.interval_min.resting` | 守門員休息中 | 10 | 0 或 1–1440；0＝這個情境只剩最長不巡上限 |
| `patrol.interval_min.present` | 使用者在電腦前 | 30 | 同上 |
| `patrol.interval_min.away` | 不在電腦前、也非無人值守（現行併在 present） | 30 | 同上 |
| `patrol.interval_min.busy` | 無人值守且主機吃緊 | 10 | 同上 |
| `patrol.interval_min.calm` | 無人值守且平穩 | 15 | 同上 |
| `patrol.max_gap_hours` | 最長不巡上限 | 24 | 1–72，不能是 0 |
| `patrol.times` | 每天固定時刻，`"HH:MM"`，以 `patrol.tz` 解讀 | [] | 0–24 個、兩位數 24 小時制、不重複；存檔時排序 |
| `patrol.tz` | 固定時刻的時區（IANA），所有主機照它解讀 | 遷移時取正本本地時區 | `time.LoadLocation` 能載入 |
| `disk_low.below_gb` | 剩餘低於幾 GB 叫一次 | 現 `watch.disk_low_gb` | 1–100000 |
| `wwd_alert.levels` | 哪些等級要叫 | 三種都叫 | `push`／`digest`／`watch` 子集，可空 |
| `wwd_alert.kinds_off` | 哪些 kind 不叫（不認得的也照存） | [] | ≤ 32、`^[a-z][a-z0-9_:-]{0,63}$`、不重複 |
| `user_back.away_min` | 離開多久後回來才叫 | 30 | 5–1440 |

- 情境判斷順序不變：休息中 → 在電腦前 → 無人值守且吃緊 → 無人值守且平穩 → 其他（`away`）。實際間隔＝`min(情境間隔（0 視為無限）, max_gap_hours)`。
- `patrol_idle_min` 是休息倒數，不是巡檢間隔，留在 `watch` 列。

## 3. prompt 怎麼送到守門員

- 叫醒時若守門員在休息，先用 `wake_prompt`（單行、打進 shell）喚醒；醒來後送一則通知。**規則的 prompt 接在通知後面**，空一行：`[負載守門] 叫醒：<標籤>｜<detail>` ＋ `\n\n` ＋ prompt。
- **組字只在做決定的那台做一次**，與閘門判斷用同一份設定：`WakeWatcher` 讀設定 → 判斷 → 取 prompt → 組好全文。跨主機時，`POST /api/peers/watch/note` 的 body 多一個選填欄位 `prompt`，接收端有它就直接用、不再讀自己的設定（兩台的設定可能在快取窗口內不同）。本機與跨主機送出的文字因此相同。
- prompt 走通知（peer 訊息），可以多行；`detail` 仍是單行 ≤ 500 bytes。
- 合併：wwd 警報一次合併多則時只附一次 `wwd_alert.prompt`。
- `reasonLabel` 補 `user_back`（使用者回到電腦前）；固定時刻與最長不巡上限的 detail 分別寫「定時 09:00」「超過 24 小時未巡」。

## 4. 存放與一致性（暫定、待收斂）

- **正本**：預設 `mlab`；每台 `~/.config/pdx/config.toml` 可用 `[watch] wake_source = "<alias>"` 改。兩台不一致時各自照設定，GET 照實回 `source`。
- **正本存放**：hostconfig 新列 `watch_wake`。hostconfig 的 store 是私有的，新增一個窄介面服務 `hostconfig.WakeRowStore`（registry key `hostconfig.watch-wake`）：`Get(key) (raw []byte, revision int64, ok bool, err error)`、`Put(key, raw []byte, baseRevision int64) (revision int64, err error)`（CAS，用既有 `Store.PutWith`；不符回 `ErrConflict`）。`key` 只接受兩個常數：`watch_wake`（正本）與 `watch_wake_cache`（非正本的快取），其他一律拒絕。這兩列**不**開通用的 `PUT /api/hostconfig/<key>` 路由。
- **遷移**：正本第一次讀取而列不存在時，由本機 `watch` 列遷移（`auto_wake`→`auto_start`；`patrol_present/busy/calm/rest_min`→`present/busy/calm/resting`，`away`＝`present`；`disk_low_gb`→`below_gb`；`tz`＝本地時區；其餘取 §2 預設；`watch.enabled` 不遷移），以 `Put(raw, 0)`（列不存在才成功）寫入；與第一個 PUT 競爭時 CAS 決定勝負，輸的一方重讀。
- **revision**：每次寫入遞增；GET 回 `revision` 與 `source_id`（正本 daemon 的 host_id）。revision 只在同一個正本內有意義。
- **非正本**：一個**單一的更新者**（goroutine）負責快取的所有更新——60 秒輪詢、收到變更通知、轉寫成功——依序處理。每個請求在送出時記下它問的是哪個正本（`source` alias＋`source_id`）；結果回來時，正本若已不是設定中的那一個就丟掉；同一個正本只接受 revision 比快取新的結果（防止較早送出的輪詢回應把快取倒退）。`wake_source` 改變或正本的 `source_id` 改變時，快取的 revision 基準重設（下一個結果無條件接受）。快取存 hostconfig 列 `watch_wake_cache`（文件、etag、revision、`fetched_at`）；落地失敗時保留記憶體內的新值、記 log、下一次輪詢再寫。正本連不上用快取並 `stale: true`；從未取到用 §2 預設並 `stale: true`。快取被更新為新的 revision 時，在本機 `/ws/host-events` 發 `watch.wake_settings.changed`。
- **跨主機傳輸**：沿用現有 `HostCaller`（POST、`to_host_id`、回應須帶相符的 `host_id`），用**信封**包住內層的狀態碼與 body，讓 400 的 `fields`、409 的 `current` 原樣穿過：

  | 路由（peer token、加入 `internal/module/peers/policy.go` 白名單） | 請求 | 回應 |
  |---|---|---|
  | `POST /api/peers/watch/wake-settings/get` | `{to_host_id}` | `{host_id, status: 200, body: <GET 的 body>}` |
  | `POST /api/peers/watch/wake-settings/put` | `{to_host_id, settings, expect_etag}` | `{host_id, status, body}`（body＝正本 handler 的原樣輸出） |
  | `POST /api/peers/watch/wake-settings/changed` | `{to_host_id, revision}` | `{host_id}` |
  | `POST /api/peers/watch/wake-suppressed` | `{to_host_id, entries: [...]}` | `{host_id}` |

- **寫入**：App／CLI 可寫任何一台；非正本把 `PUT` 包成 `/put` 轉給正本，原樣回 `status`＋`body`，成功後交給單一更新者。正本連不上（含逾時）回 503 `source_unreachable`——正本可能已寫入，呼叫端重讀即知；下一次輪詢也會收斂。
- **變更通知**：正本寫入成功後在 `/ws/host-events` 發事件 `watch.wake_settings.changed`，`value` 只帶 `{"revision": 7, "etag": "9f3c…", "source": "mlab"}`（App 只拿它觸發重讀 GET，不從事件取文件）；非正本前進到新 revision 時發同一個事件、同樣形狀。並對 fleet 其他 daemon 送 `/changed`（失敗不重送，輪詢兜底）。
- **版本**：讀到存放的文件 `version` 大於自己認得的 → 叫醒用 §2 預設並記 log；GET 回原樣＋`readonly: true`；PUT 一律 `bad_version`。App 遇到 `version` 不是 1 或 `readonly` → 唯讀。

## 5. API

capability 有兩個：
- `watch.wake_settings.v1`：WK-1a 宣告（watch 模組 ready 時），意思是「這支 API 在」。
- `watch.wake_rules.v1`：意思是「叫醒已經改讀新文件」。由 WK-2、WK-3 裡**最後 merge 的那一張**宣告（兩張的 PR 說明都寫明由誰宣告；先 merge 的那張不宣告）。App 看到它才建立「設定」分頁、才拿掉主機分頁的 `enabled`／`auto_wake`；只看到 `wake_settings.v1` 時不露出新畫面。這樣 B 線的 PR 可以各自 merge，畫面在帶了 `wake_rules.v1` 的那一版才出現。

**`GET /api/watch/wake-settings`**（admin、App、裝置 token 都可讀；加入 `cmd/pdx/device_scope.go` 允許清單與 `device_scope_test.go`）

```json
{
  "settings": { … },
  "etag": "9f3c…",
  "revision": 7,
  "source": "mlab",
  "source_id": "82b11c521df16c12",
  "is_source": true,
  "stale": false,
  "readonly": false,
  "fetched_at": "2026-10-21T17:00:00+08:00",
  "known_kinds": [ { "kind": "orphans", "levels": ["digest", "push"], "label": "孤兒行程" } ],
  "suppressed": [ { "at": "…", "host": "air26", "reason": "wwd_alert", "why": "level_off", "detail": "air26：記憶體壓力…" } ],
  "patrol_status": { "last_ok_at": "…", "last_ok_host": "mlab", "last_fail_at": null, "last_fail_code": null, "last_fail_detail": null }
}
```

- `etag`：正規化文件的雜湊（不落地）。`revision`：§4。
- `suppressed`：最近 50 則因設定而沒叫的紀錄，每筆帶做決定的 `host`；非正本以 `/wake-suppressed` 報給正本（失敗就算了），正本彙總；非正本的 GET 回自己的。記憶體內。`why`：`master_off`／`rule_off`／`level_off`／`kind_off`／`no_auto_start`。
- `fetched_at`：非正本最近一次成功取到正本的時間；正本回自己寫入／讀取的時間；**從來沒取到過時是 `null`**（此時 `stale: true`）。
- `known_kinds`：來自 Purdex wwdalerts 的 kind 名稱表（`internal/module/wwdalerts/wake.go` 抽成可匯出的表），每個 kind 帶 `levels`：這個 kind 可能出現的所有等級（例如 `orphans` 會從摘要升成推播，`internal/wwd/advisor/rules_orphans.go`，所以是 `["digest","push"]`），依 digest→push 排序。`label` 只有中文；App 英文介面照顯示並附 kind 原字。App 不依等級鎖住勾選。
- `patrol_status`：本機最近一次巡檢（任何一種：間隔、上限、固定時刻）成功與失敗的時間與原因。失敗原因是代碼 `last_fail_code` 加原文 `last_fail_detail`（App 依代碼翻譯、原文附在後面）；代碼值域固定為：`targets_unreachable`（`targets` 全部連不上）、`targets_unknown`（`targets` 指到不存在的主機）、`start_failed`（叫起或啟動守門員失敗）、`deliver_failed`（守門員在，但通知送不到）。沒有失敗過時兩個欄位都是 `null`。非正本的失敗也以 `/wake-suppressed` 一併上報給正本（`why: patrol_failed`），讓 App 讀正本就看得到「巡檢叫不起守門員」。
- `path` 寫法固定：`rules.<rule>.<field>`，陣列元素 `[i]`；App 只依 `code` 翻譯。

**`PUT /api/watch/wake-settings`**（admin、App token）body `{ "settings": {…整份…}, "expect_etag": "…" }`

| 狀態 | body | 時機 |
|---|---|---|
| 200 | 同 GET | 成功 |
| 400 | `{"error":"missing_expect"}` | 沒帶 `expect_etag` |
| 400 | `{"error":"invalid","fields":[{"path","code","message"}…]}` | 驗證失敗，一次列全部 |
| 409 | `{"error":"etag_mismatch","current":{…同 GET…}}` | 有人先改了 |
| 403 | `{"error":"device_forbidden","detail":"this route is not available to a paired phone"}` | 裝置 token（由現有 device scope middleware 回，`cmd/pdx/device_scope.go`；PUT 不在允許清單）；fixture 固定這個 body |
| 503 | `{"error":"source_unreachable","source":"mlab"}` | 非正本且正本連不上 |

錯誤碼：`missing_field`、`unknown_field`（含 `rules.patrol.enabled`）、`bad_type`、`out_of_range`（含 `max_gap_hours` 為 0）、`bad_time`、`duplicate_time`、`too_many`、`bad_level`、`bad_kind`、`prompt_too_long`、`prompt_bad_char`、`bad_tz`、`bad_version`。

**契約 fixture**：WK-1a 在 `internal/wwd/wire/testdata` 附 `watch_wake_settings_get.json`、`…_put_200.json`、`…_put_400.json`（至少三個錯誤）、`…_put_409.json`、`…_put_403.json`、`…_put_503.json`，**同張**把 SPA 副本放進 `spa/src/lib/watch/__fixtures__/`，並**新增** `spa/src/lib/watch/fixtures-sync.guard.test.ts`（照 `spa/src/lib/pool/fixtures-sync.guard.test.ts` 寫；現行那支寫死只比對 `lib/pool/__fixtures__`、沒有清單可加，放在 `lib/watch` 的副本不會被比對）。

**舊欄位**：`watch` 列的 `enabled`、`auto_wake`、`patrol_present/busy/calm/rest_min`、`disk_low_gb` 在 WK-2 起不再被叫醒或巡檢讀取；GET、PUT 照舊收送。`pdx watch config` 對這幾個 key 改印「已移到 `pdx watch wake-settings`」。

## 6. 行為

### 6.1 非巡檢的叫醒（disk_low、wwd_alert、unattended_changed、user_back）

1. **規則**：`WakeWatcher` 入口讀設定（同一份用到底），總開關或該規則關 → 不叫，記 `master_off`／`rule_off`，回傳 nil（呼叫端不重試）。
2. **自動叫起**：把 `auto_start` 當 `autoWake` 傳給 `Locator.Current`。已有清醒的守門員時照常送達；找不到而因 `auto_start=false` 回 `ErrNoWatcher` 時，記 `no_auto_start`。
3. **wwd 警報的 level／kind**：在 **wwdalerts 進佇列之前**（看得到 `Alert.Level`／`Kind` 的地方）過濾。wwdalerts 經 registry 取兩個服務：設定讀取（`watch.WakeSettingsKey`）與被擋紀錄（`watch.SuppressRecorderKey`）。wwdalerts 自己判斷 `enabled`（總開關）、`rules.wwd_alert.enabled`、`levels`、`kinds_off`，不靠 `WakeWatcher` 的入口判斷。被擋的 episode **不認領、不寫 ledger**（`claimWake` 之前就跳過），所以 ledger 不需要新狀態。**已認領、排在 pending 的 episode 在送出前（`wakeOnce` 取件時）再判斷一次**：若此時被擋，就把它從 ledger 刪除（與 `dropPending` 處理已消失警報相同，不 `markWoken`），之後重新打開規則時它才會被當成新的。只記一則 suppressed（UI 用）。效果：設定關著期間不叫、重啟也不叫；**把規則重新打開後，仍未解除的警報會被當成新的叫一次**（與「第一次看到」相同，受既有的 `wake_hosts` 首見規則與 10 分鐘節流約束）。之後若主備切換，新 primary 的 ledger 沒有這個 episode，可能再叫一次——這與現行主備切換時「寧可重複不漏」的行為一致，接受並寫進測試的預期。混合批次只合併允許的那些，叫一次。
4. **prompt**：§3，由這台組好全文。

### 6.2 巡檢（間隔、固定時刻、最長不巡上限）

- **不經**總開關、規則開關、`auto_start`；`patrolStep` 拿掉現行讀 `watch.enabled` 的早退（`internal/module/watch/patrol.go:130-132`）；`mode=resident` 不影響。叫醒時 `autoWake` 一律 true。
- **執行者**：情境間隔與（有守門員時的）最長不巡上限 → 持有守門員的主機（照現行；休息中的持有者若記得的守門員在別台就不跑）。固定時刻、沒有守門員時的上限 → **每一台 daemon 都檢查並送出**，由守門員所在那台的收件端去重（§6.3、§6.4）。巡檢的執行不看 fleet 角色、`wake_source`、`targets` 以外的任何設定，所以沒有設定能讓所有主機都不執行。
- 間隔＝`min(情境間隔, max_gap)`；任何成功的通知重置計時；失敗等下一個間隔。
- **基準持久化**：每台把「上次巡檢成功」的時間寫進 `DataDir/watch-patrol-last.json`（暫存檔＋rename），重啟後從它接續，不再以 daemon 啟動時間為基準（避免一直在到期前重啟而永遠不巡）。
- 磁碟（`disk_low`）屬非巡檢規則：門檻讀 `rules.disk_low.below_gb`，經 6.1 的規則判斷。

### 6.3 沒有守門員時

- 任何主機都沒有清醒或休息中的守門員時（locator 查無），**每一台 daemon** 在自己的巡檢 tick 評估最長不巡上限（基準為本機 `watch-patrol-last.json` 的時間；沒有檔案時為現在，並立刻寫檔），到了就 `WakeWatcher(ctx, "patrol", …)`，由 locator 依本機 `targets` 啟動一個。
- 兩台同時要求啟動時，目標主機的 `sessionCtl` 一次只跑一個啟動（單一 job＋`StartCooldown`），第二個請求會在 locator 重查時找到剛啟動的守門員並改為送通知，由收件端去重。
- 每台的上限計時各自獨立，所以放寬到極限時，守門員一個上限內最多會收到每台各一次的上限巡檢（2 台＝最多 2 次／上限），接受。
- `targets` 全部連不上或指到不存在的主機時，巡檢照常觸發但叫不起守門員：記進 `patrol_status`（`patrol_failed`），App 以警告顯示。這屬設定錯誤的**呈現**，不是巡檢被移除。

### 6.4 固定時刻

- **每一台 daemon 都評估固定時刻**並送出；通知帶 `dedupe_key`（下述邏輯鍵），**守門員所在那台的收件端**（本機 `notifyWatcher` 與 `handlePeerNote` 共用）記住最近 24 小時收過的 key，重複的直接丟掉（回成功，不轉給守門員）。守門員剛好在 10 分鐘窗口內換主機時可能重複一次，接受。
- 每個 tick 以 `patrol.tz` 算出**昨天與今天**的時刻（處理跨午夜的 10 分鐘窗口）。條件：`slot ≤ now < slot + 10 分鐘`，而且這個時刻的**邏輯鍵**不在已叫紀錄裡。邏輯鍵＝`<該 tz 的當地日期> <HH:MM>`（例 `2026-10-21 09:00`），所以改 tz 不會讓同一天同一個時刻再叫一次。
- **每台至多送一次**：先把鍵寫進本機 `DataDir/watch-wake-times.json`（暫存檔＋fsync＋rename），再送出。寫入後當機、或送出失敗 → 這台的這一次就算用掉，不重試，失敗記進 `patrol_status`；另一台的同一個時刻仍會送，所以單台失敗不會讓那個時刻整個漏掉。紀錄保留最近 8 個當地日期；時鐘倒退超過 7 天時不保證不重送。
- `noteBody` 新增選填 `dedupe_key`；收件端的 key 紀錄存在記憶體並落地 `DataDir/watch-note-dedupe.json`（24 小時），重啟不失去。
- 同一 tick 到了多個時刻 → 叫一次，detail 列出。
- DST：當地時間不存在的時刻那天跳過；重複的時刻只取第一次（Go `time.Date` 的規則，測試釘住）。執行中改 `tz` → 下一個 tick 用新 tz 重算。
- 守門員休息中也叫醒；成功後重置間隔計時。

### 6.5 user_back

- **只有 fleet primary 跑**（直接看角色）；每 60 秒讀 presence。
- presence 要分三態：在、不在、**不知道**（沒有任何有效的 v2 回報）。`internal/push/presence_key.go` 新增選填介面 `PresenceStateReader { PresenceState() (atComputer, known bool) }`，push 模組的同一個值實作它。`known=false` → 清掉 `awaySince`，不叫。
- 由不在變在、且離開 ≥ `away_min` → 經 6.1 叫一次。第一次讀值只當基準；角色切換（primary → standby → primary）時重設基準；失敗不重送。

### 6.6 unattended_changed

- 照現行（切換的那台叫），經 6.1。

## 7. CLI 與 App

- **CLI**：`pdx watch wake-settings [show] [--json]`、`pdx watch wake-settings set <path>=<value>…`（陣列逗號分隔；`@file` 讀多行；讀 → 改 → 帶 expect 寫回；409 結束碼 3、400 逐條印、結束碼 4）。`pdx watch wake`（手動叫醒）不變。
- **App**（B 線，88 定細節）：守門員專區新增一個**固定分頁「設定」**（不放進守門員分頁本身——那裡的內容會在 `WatchGuardianPane` 與 session 的 terminal／deck／chat 之間切換）。掛法照事件、任務佇列分頁：`spa/src/lib/watch/ids.ts` 加 `WATCH_SETTINGS_TAB_ID`（`'watch-settings'`）與 pane kind `{ kind: 'watch-settings' }`；`spa/src/features/workspace/lib/watch-area.ts` 的 reconcile 把它排在任務佇列之後、主機分頁之前；register-modules 註冊 `spa/src/components/watch/WatchSettingsPane.tsx`，「喚醒條件」是它的第一個區塊。有沒有守門員 session 都不影響這個分頁。巡檢一列沒有開關：五個情境間隔（0＝只剩上限）、最長不巡上限、固定時刻（顯示 `tz`）、prompt。其他規則：開關、多行 prompt、參數；`wwd_alert` 的 kind 用 `known_kinds` 勾選；總開關與自動叫起註明不影響巡檢；`user_back` 標「新」；`stale`、`readonly`、`suppressed`（含 host）、409、裝置 token 唯讀都要有畫面。主機分頁的守門員區塊只留啟動設定（`patrol_idle_min` 改標「閒置多久後休息」），拿掉 `enabled`、`auto_wake`，加連結。中英兩份 locale。
  - 出現條件：App 看到 `watch.wake_rules.v1` 才建立「設定」分頁並瘦身主機分頁（§5）。
  - 新 pane kind 除了上面三處，還要改：`types/tab.ts`、`pane-labels.ts`、`route-utils.ts`、`tab-icon-map.tsx`、`HistoryPage.tsx`、`open-watch-area.ts`，與測試 `lib/watch/watch-panes.test.ts`、`lib/profile/sections.test.ts`（88 盤點，行號以實作時為準），否則 tsc 會紅。
- **pdx-watch skill**：通知後可能附有使用者的 prompt，照做；設定由使用者改；巡檢不可關。

## 8. 對 WWD-9 的影響（要同步改 WWD-9 plan）

- 9c-a：除了已列的，還要把 `delivery.go` 的 `deliveryState.notifier` 欄位與初始化搬出（`rest.go:283-289` 的 `notifyWatcher` 用它）；`recoverSession` 保留現狀。
- 9c-b-ii：不刪 `auto_wake` 欄位；刪 ack 前先搬出 `ValidateNote`。
- 9c-c：刪單機 twoawake 前先搬出 `peerNotifier`／`WatcherNotifier`。
- 9d-3b：巡檢的 `hostBusy` 與磁碟讀取改讀 pool 提供的資料（同一張 PR）。
- S3-3c：由 WK-3 的 `user_back` 取代，從 WWD-9 移除。
- **順序**：WK-3 → 9d-3b → 9c-a → 9c-b → 9c-c（都動 `internal/module/watch`／巡檢資料源，避免衝突與資料源短暫消失）。

## 9. 分段（每張 ≤ 800 行或 ≤ 20 檔）

| 段 | 內容 | 線 |
|---|---|---|
| WK-1a | 文件型別、正規化與驗證、etag、遷移；hostconfig `WakeRowStore` 服務（兩個固定 key）；正本 `GET`／`PUT`、revision、`source_id`、capability、WS 事件、device scope；六個 fixture＋SPA 副本 | A |
| WK-1b | 非正本：單一更新者、輪詢、快取、stale、依正本身分＋revision 單調；peer 信封路由與白名單；轉寫；變更通知；suppressed 與 patrol_status 接收 | A |
| WK-2 | 6.1 的規則判斷與 `auto_start`→locator；wwdalerts 認領前過濾（不寫 ledger）、兩個 registry 服務；§3 組字一次、note 帶 prompt；suppressed 紀錄與上報；`reasonLabel`；`pdx watch config` 提示 | A |
| WK-3 | 6.2–6.5：拿掉 `watch.enabled` 早退、情境間隔與上限、`watch-patrol-last.json`、無守門員時 primary 啟動、固定時刻（primary、邏輯鍵、至多一次）、`patrol_status`、presence 三態、user_back | A |
| WK-4 | CLI `pdx watch wake-settings`、pdx-watch skill | A |
| WK-5a／5b | App（88 的拆法） | B |

**出版**：WK-1a、WK-1b、WK-4 可先出（只多 API，行為不變）。WK-5 的 PR 可以各自 merge、隨任何一版出（沒有 `watch.wake_rules.v1` 時不露出新畫面）；**宣告 `watch.wake_rules.v1` 的那張（WK-2、WK-3 裡最後 merge 的）所在那一版，新畫面才出現，那一版打包 Mac App**。

## 10. 測試

- **驗證**：每個錯誤碼至少一條；一次多個錯誤；整份取代；`rules.patrol.enabled` 是 `unknown_field`；`max_gap_hours` 0 與 73 拒絕；prompt 多行可、控制字元拒絕；時刻排序與去重；`bad_tz`。
- **CAS／並行**：expect 相符寫入、不符 409 且不變、沒帶 400；etag 與欄位順序無關；兩個並行 PUT 只有一個成功；遷移與第一個 PUT 競爭只有一個寫入；非正本的輪詢、變更通知、轉寫三者亂序抵達時快取只前進不倒退；快取落地失敗時記憶體值仍新、下次輪詢補寫。
- **遷移**：只做一次；對應正確；之後改 `watch` 列不影響。
- **正本／非正本**：讀到正本的值；斷線用快取 `stale`；從未取到用預設；轉寫成功更新快取；400／409 的 body 經信封原樣回到呼叫端；變更通知觸發重取；非正本前進到新 revision 時發事件；**切換 `wake_source` 後新正本的較小 revision 仍被接受、舊正本延遲的回應被丟掉**；`WakeRowStore` 拒絕兩個常數以外的 key。
- **wwd 排隊後才被擋**：pending 的 episode 在送出前被擋 → 從 ledger 刪除、不 `markWoken`；重新打開後會叫。
- **閘門（6.1）**：總開關、規則開關、level、kind 各自擋得住並記 suppressed（帶 host，非正本上報）；`auto_start=false` 時：有清醒守門員照常送達、休息中或沒有時記 `no_auto_start`；wwd 混合批次只叫允許的、只附一次 prompt；被擋的 episode 不寫 ledger、關著期間重啟也不叫；重新打開後仍未解除的警報叫一次；**primary 擋 → 重新打開 → primary 叫一次 → standby 接任**時可能再叫一次（與現行主備切換一致，測試釘住這個預期）。
- **巡檢不可移除**：`watch.enabled=false`、總開關關、`auto_start=false`、`mode=resident`、五個情境全 0、`times` 空，每一種（與組合）下，最長不巡上限到時都會叫；沒有守門員時**任一台**都會發動啟動（fleet 關閉、`wake_source` 指到不存在的主機時也一樣），兩台同時發動只啟動一個；daemon 在到期前反覆重啟仍會到期（基準來自檔案）；`targets` 全部無效時記 `patrol_failed`。mutation：恢復 `!s.Enabled` 早退、讓總開關擋到巡檢、把上限當成可關、基準改回啟動時間、只讓 primary 執行，五條要紅。
- **固定時刻**：每台都送、收件端依 `dedupe_key` 只轉達一次（含收件端重啟後）；一台送出失敗時另一台的仍會到；窗口內叫一次；重啟不重叫；超過 10 分鐘不補；跨午夜（23:58 的時刻在 00:03 仍會叫）；時鐘倒退不重叫；同一天改 tz 不重叫；DST 不存在與重複的時刻（用有 DST 的 tz 測）；寫入失敗時不叫醒、叫醒失敗不重試（至多一次）且記進 `patrol_status`；守門員換主機不影響。
- **prompt 一致**：本機與跨主機送出的全文逐字相同；接收端的設定與送出端不同時仍用送出端帶來的 prompt。
- **user_back**：離開 ≥ away_min 回來叫一次；未滿不叫；第一次讀值不叫；presence 不知道時不叫且清掉基準；standby 不叫；primary → standby → primary 重設基準。
- **capability**：真模組註冊 → `/api/info` 有 `watch.wake_settings.v1`。
- **真機驗收**：App 設一個 2 分鐘後的固定時刻與多行 prompt，守門員收到的通知含該 prompt；關掉 `wwd_alert` 後製造一則 watch 級警報，守門員沒被叫、`suppressed` 有一筆、之後打開也不補叫。

## 附：codex plan review r1 對照（`task-mvhvokwg-4frzp2`）

| # | 摘要 | 處理 |
|---|---|---|
| 1 | `patrolStep` 的 `!s.Enabled` 早退仍能關巡檢 | 6.2 拿掉；測試＋mutation |
| 2 | `patrol_required` 不足（resident、無守門員） | 改為最長不巡上限＋6.3 正本啟動；刪 `patrol_required` |
| 3 | `auto_start` 要看有沒有清醒守門員 | 6.1-2：傳給 locator，查無才記 `no_auto_start` |
| 4 | wwd level／kind 需結構化資訊 | 6.1-3：進佇列前過濾、ledger `suppressed`、兩個 registry 服務 |
| 5 | `pdx watch wake` 撞名 | CLI 改 `pdx watch wake-settings` |
| 6 | `HostCaller` 只 POST、錯誤 body 不保留 | §4 信封路由 |
| 7 | hostconfig store 私有 | §4 `WakeRowStore` 服務 |
| 8 | 快取並行與部分失敗 | §4 單一更新者、revision 單調、落地失敗語意 |
| 9 | 裝置 token 403 body | PUT 由 middleware 回 `device_forbidden`；`device_scope_test.go` |
| 10 | 固定時刻持久語意 | 6.4 至多一次、以 instant 為鍵、跨午夜、DST、改 tz |
| 11 | presence 分不出不知道；角色切換 | 6.5 三態介面、重設基準 |
| 12 | stale 期間 prompt 不一致 | §3 決定端組字、note 帶 prompt |
| 13 | fixtures-sync 只看 SPA 已有副本 | §5 WK-1a 同張放 SPA 副本 |
| 14 | 9c-a 的 `deliveryState.notifier`、依賴順序 | §8 補上並排定順序 |
| 15 | App 容器會消失、檔案未列 | §7 交 88 的 WK-5 plan，註明不能放 `WatchGuardianPane` |

## 附：codex plan review r2 對照（`task-mvhw7epy-iipqq4`）

| # | 摘要 | 處理 |
|---|---|---|
| r1-2／N1 | 無守門員時只由正本做＝單點；重啟可讓上限永不到期；targets 不可達 | 改由 fleet primary（有選舉接任）；基準寫檔；§0 寫明保證範圍＝設定，運作故障由警報與 `patrol_status` 呈現 |
| r1-4／N4／N5 | ledger `suppressed` 與每台 ledger、角色切換、狀態轉移不完整 | 不寫 ledger、認領前跳過；重新打開後未解除的警報叫一次（行為明定並測主備切換） |
| r1-8／N2 | `watch_wake_cache` 沒有寫入介面 | `WakeRowStore` 支援兩個固定 key |
| r1-8／N3 | revision 跨正本不可比 | 結果帶正本身分；切換正本重設基準；`source_id` |
| r1-10／N6 | 固定時刻本機紀錄、改 tz、保留期、叫醒失敗語意 | 只 primary 評估；邏輯鍵（當地日期＋時刻）；保留 8 天；叫醒失敗也算用掉並記錄；主備切換窗口可能重複一次（接受） |
| r1-14 | WWD-9 plan 細節段落未同步 | 已直接改 9c-a、9c-c 前提、9d-3b 段落與順序關卡 |
| r1-15 | App 穩定容器未指定 | 88 指定：守門員專區固定分頁 `watch-settings`（`WatchSettingsPane.tsx`），寫進 §7 |

## 附：codex plan review r3 對照（`task-mvhwklrv-1e34eo`）

| # | 摘要 | 處理 |
|---|---|---|
| C1／N1 | fleet 關閉或 alias 不在 order → 全部 RoleOff，primary 執行者不存在；`targets` 只驗語法 | 固定時刻與無守門員上限改為**每台都執行**、收件端以 `dedupe_key` 去重；啟動由目標主機的單一 job 串行化；`targets` 無效時記 `patrol_failed` 並在 App 警告 |
| N4 | 重新打開後主備切換可能再叫一次 | 接受，與現行主備切換「寧可重複不漏」一致，測試釘住 |
| N5 | 已排隊的 episode 被擋時 `markWoken` | 送出前再判斷，被擋就從 ledger 刪除（同 `dropPending`），不 `markWoken` |
| r1-14 | WWD-9 §2 仍列 S3-3c | 已改 |
