# WK-5 App 端 plan：守門員喚醒設定（B 線）— 草稿 v4

> 2026-10-21，88 派的 Opus subagent 起草，交 88 審。只有 plan，沒有改 repo。
> **v2**：併入 codex 審查 `task-mvi6ky1o-jpn4y4`（11 條，逐條處理見 §12）與 88 的裁定；對齊 1f 的 spec v5.1 與 plan v4.1（皆已 merge 進 main）；加入使用者對主機分頁「模式」的決定（改成「閒置時休息」開關）。
> **v3**：寫入 88 對 v2 剩下三件的裁定（§11）：
> - 接受八張；
> - 409 以外的寫入失敗不能悄悄丟掉使用者的改動（4.4），為了放得下，5b-2～5b-4 的內容重新分配（§7）；
> - WK-5h 獨立先出、現在就派工。
>
> **v4**：照 88 的裁定修 codex 增量複審 `task-mvia45y9-0ockz6` 的兩條 critical（§12）：
> - 同一欄在途時再改，另起一筆排隊、base 用在途的 value；陣列整個比較（4.4、4.5）；
> - 從未取到時整塊唯讀；`""` → 真的 id 不算切換；真的 A → B 時，修改轉成「沒有送出」，不丟（4.3）。
>
> 5a-3 因此依預定切點拆成兩張，共九張（§7）。
> 依據（行號都是 main `693bc9b3d`）：
> - spec：`docs/specs/2026-10-21-watcher-wake-settings-spec.md`（下稱 spec）
> - 1f 的 plan：`docs/plans/2026-10-21-watcher-wake-settings-plan.md`（下稱 1f plan）
> - 盤點：`drafts/2026-10-21-watcher-wake-inventory.md`
> - 使用者定案：巡檢不可移除，只能放寬；設定全體一份；App 放守門員專區新的固定分頁「設定」；主機分頁守門員區塊只留啟動設定；「閒置時休息」是一個開關，跟巡檢不衝突。
>
> SPA 的行號自量測以來只多了 WK-1a 的 fixture 與守門測試，其他沒變。實作者開工時照樣重查。

## 0. 摘要

**新分頁**：守門員專區新增固定分頁「設定」（`watch-settings`）。第一個區塊「喚醒條件」包含：
- 總開關、自動叫起；
- 巡檢（沒有開關）；
- 三條規則；
- wwd 警報（開關、prompt、等級、種類）；
- 最近沒叫的紀錄與巡檢狀態。

**讀寫**：走 `GET/PUT /api/watch/wake-settings`（spec:111-148），整份取代並帶 `expect_etag`。
- 一次只有一個 PUT 在路上。
- 每筆待送修改記下「改之前的值」。拿到新的伺服器文件時逐欄三方比對：別人沒動的就續送，已經一樣的就丟掉，別人也改了的就轉成衝突，由使用者決定。
- 每一欄是一條依順序的鏈。同一欄在途時再改，另起一筆，以在途的值為 base。陣列整個當一個值比較。
- 從未取到正本時整塊唯讀。正本從 A 換到 B 時，修改轉成「沒有送出」，不丟。
- 409 不會清空佇列。prompt 草稿一律保留。
- 409 以外的失敗也不悄悄丟掉改動：
  - 503、沒有回應、主機錯誤：改動轉成看得到的「沒有送出」，附「重試」。
  - 格式不合：同樣轉成「沒有送出」，但不給重試。
  - 只有 403（這台裝置不能改）才丟掉改動，並整塊轉唯讀。

**閘門寫死**：
- 讀寫的候選看 `watch.wake_settings.v1`。
- 分頁建立與主機分頁瘦身只看 `watch.wake_rules.v1`（spec:107-109、1f plan:45）。
- 所以 WK-5 各張可以各自 merge，在 `wake_rules.v1` 出現前畫面不可見。

**主機分頁的兩件事**：
- **WK-5h**：「模式」的二選一換成「閒置時休息」開關，`patrol_idle_min` 改標「閒置多久後休息」。現有 daemon 就支援，不受閘門限制。**88 已裁定獨立先出、現在就派工**。
- **WK-5b-4**：拿掉 `enabled`／`auto_wake` 並加連結，受 `wake_rules.v1` 閘門。

**大小**：加了三方比對、同欄的鏈、「沒有送出」、更細的測試、wwd 整塊搬家之後，總量估約 4,930 行。切成九張（88 接受了八張；v4 依預定切點把 5a-3 拆成兩張，待 88 確認）：
- WK-5h；
- WK-5a-1、5a-2、5a-3a、5a-3b：資料層，沒有畫面；
- WK-5b-1～5b-4：畫面。

每張都控制在約 720 行以內，見 §7。

---

## 1. 現況量測（main）

### 1.1 新 pane kind 要跟著加的地方

照 `watch-jobs`（commit `6f6d1ffb2`＋`7f4734760`）的做法。spec:203 也已列出這些地方。

| 位置 | 現況 | 要加的 |
|---|---|---|
| `spa/src/lib/watch/ids.ts:11-14` | 三個固定 id；`isWatchTabId` 用 `watch-` 前綴（:20）；`hostIdOfTabId` 只認 `watch-host-`（:23-25） | `WATCH_SETTINGS_TAB_ID = 'watch-settings'`，更新 :11 的註解 |
| `spa/src/types/tab.ts:158-161` | 四個 watch kind | `\| { kind: 'watch-settings' }` |
| `spa/src/lib/pane-labels.ts:63-73`、`:110-117` | label／icon 的 switch | label `t('watch.pane.settings')`，icon `'GearSix'` |
| `spa/src/components/tab-icon-map.tsx:5`、`:24-27` | import 與 `ICON_MAP` 的 watch 區 | 加 `GearSix` |
| `spa/src/lib/route-utils.ts:148-152` | watch kind 一律回 `'/'` | 加 `case 'watch-settings'` |
| `spa/src/lib/register-modules/index.tsx:260-271` | `watch-area` 模組有四個 pane | 加 `{ kind: 'watch-settings', component: WatchSettingsPane }`，註解改成「五個」 |
| `spa/src/components/HistoryPage.tsx:34-38` | 專區 pane 的紀錄一律改開專區 | `if (c.kind === 'watch-settings') return openWatchArea({ tab: 'settings' })`。漏寫這個分支的話，`contentMatches` 對新 kind 會落到預設的 singleton（`spa/src/lib/pane-utils.ts:23-66`，末行 `return true`），結果被建成普通分頁 |
| `spa/src/components/HistoryPage.watch.test.tsx:34-40` | 只鎖 guardian／events → `{tab:'guardian'}`、不建立分頁 | 加 settings 的紀錄 → `{tab:'settings'}`、不建立分頁 |
| `spa/src/features/workspace/lib/open-watch-area.ts:11`、`:28-30` | `WatchTarget` 的 `tab` 只有 guardian／events／jobs | 加 `'settings'` |
| `spa/src/features/workspace/lib/watch-area.ts:173-189`、`:236-239`、`:364-368` | `reconcileJobs`：有已連線 daemon 宣告 `jobs.v1` 才建立，只建不移除；訂閱已含 `useNexHostStore`、`useHostStore` | 新增 `reconcileSettings`：條件是有已連線、`fetchedAt > 0`、宣告 **`watch.wake_rules.v1`** 的 daemon；位置排在 guardian／events／jobs（存在的那幾個）之後、主機分頁之前；只建不移除 |
| `spa/src/lib/profile/sections.test.ts:225-243` | 對 `PaneContent['kind']` 的完整 `Record`，少一個 tsc 就紅 | `'watch-settings': 'syncs'` |
| `spa/src/lib/watch/watch-panes.test.ts:36-95` | label、icon、route、contentMatches、非 light、四個 kind 已註冊 | 各加 settings；:92 改成五個 |

不用改：`pane-weight.ts`（新 kind 是重的）、`useMultiHostEventWs.ts`（見 1.3）、`main.tsx`。

### 1.2 主機分頁現有的守門員區塊

- `spa/src/components/watch/host/SettingsTab.tsx:28-32`：主機頁「設定」最下方，只有有 daemon 的主機才有，預設收合，內容是 `GuardianSection`。`daemon` 是 fronting 這台 pool host 的 App 主機（:17）。
- `spa/src/components/watch/WatchSettings.tsx`：
  - `FIELDS`（:15-21）有 `enabled`、`mode`、`patrol_idle_min`、`auto_wake`、`agent_command`、`workdir`、`start_prompt`、`wake_prompt`。
  - `mode` 用 `SegmentControl` 畫成「常駐／巡檢」（:167-169）。
  - 標籤與說明照 `watch.set.f.<key>`、`watch.set.d.<key>` 取（:157、:163）。
  - 整列寫回加 `baseRevision`（:108-134）。
  - `TextControl` 在送出前就清掉草稿，400 與 409 都會結束草稿（:52-58；鎖住這個行為的測試在 `WatchSettings.test.tsx:144-160`）。
- `spa/src/lib/watch/watch-row-write.ts:36-45` 的 `rowWith` 會整列帶回，看不懂的欄位也帶。所以畫面上拿掉的欄位，值不會被清掉。
- daemon 端：`mode` 只有 `patrol` 與 `resident`（`internal/watchsettings/watchsettings.go:10-14`），而且只拿來控制休息（`internal/module/watch/rest.go:150-155`：`s.Mode != ModePatrol` 時直接 reset）。巡檢不看它（spec:165）。
- 既有測試鎖住舊欄位：`WatchSettings.test.tsx:73-76` 斷言八列都在、`:101-106` 測 `auto_wake` 開關。依 capability 切換之後，這兩條在沒有 `wake_rules.v1` 的設定下照樣綠。

### 1.3 可沿用的寫法

- **寫入中、409、唯讀**：
  - `WorkerSettings.tsx:114-198`：一次只有一個 PUT 在路上，回應後送最新的。
  - 403 記在 `control-api.ts:28-32`，整區轉唯讀。
  - 草稿放元件外：`lib/pool/worker-draft-memory.ts`。
  - **不能沿用 `lib/pool/errors.ts` 的 `classify`**：`device_forbidden` 不在它的代碼表（:3-6），會被歸成 `unknown`→`log`（:31-35、:55）。
- **host-events**：
  - `HostEvent.type` 是封閉的 union（`lib/host-events.ts:4-34`）。
  - hook 在比對 connectionKey 之後才交給 `handleWatchEvent`（`hooks/useMultiHostEventWs.ts:371-377`）。
  - `isWatchEvent` 目前只認 `watch.changed`（`lib/watch/watch-ws.ts:9`）。擴充 watch-ws.ts 就夠，hook 不用改。
  - 事件 `watch.wake_settings.changed` 的 value 是 `{revision, etag, source}`，只拿來觸發重讀（spec:102）。
- **capability 與候選**：已連線、`fetchedAt > 0`、`daemonCapabilities.includes(cap)`，依 host order（`lib/pool/jobs-driver-live.ts:13-21`）。
- **讀哪一台**：
  - pool 用 `usePoolStore.sourceHostId`（`watch-area.ts:330-334`）。
  - GET 回 `source_id`，也就是正本 daemon 的 host_id（spec:119）。App 主機存著 daemon 的 host_id：`useHostStore.hosts[id].daemonId`（`stores/useHostStore.ts:60-65`），可以直接比對。
  - 「從未取到」時 `source_id` 是 `""`、`revision` 是 0（fixture `watch_wake_settings_get_stale.json`）。
- **世代保護**：`lib/watch/status-store.ts:129-149`（每次讀取記下 generation 與 connectionKey），`:173-186`（斷線、重連、re-point 都換世代）。
- **不要綁 `watch.v1`**：WWD-9 會改它（盤點 §5）。

### 1.4 tab-hosted state

- light 白名單只有 editor、image-preview、pdf-preview、dashboard、history（`lib/pane-weight.ts:18-24`）。`watch-settings` 是重的，切走就 unmount（`keepAliveCount` 預設 0，`stores/useUISettingsStore.ts:215`）。
- 草稿、捲動位置、展開狀態要放元件外，並用真 `TabContent` 寫回歸測試。範例：`WatchSettings.tab-switch.test.tsx`、`WatchEventsPane.tab-switch.test.tsx:54-66`、`jobs/JobsPane.tab-switch.test.tsx`。
- `components/hosts/form-fields.tsx:6-7` 的 `Section` 把展開狀態放在 `useState`。要加選填的受控 props `open`／`onOpenChange`，沒傳時行為不變。

### 1.5 現成元件

- `SettingItem`：有 `stacked` 與 `disabled`（`components/settings/SettingItem.tsx:5-6`、:13、:20）。
- `ToggleSwitch`、`SegmentControl`（單選）。
- 勾選框樣式：`components/hosts/AddHostDialog.tsx:338-346`。
- 時間格式：`WatchEventsPane.tsx:53` 的 `hhmm`。
- 等級字樣沿用 `watch.events.level.push/digest/watch`（zh-TW.json:586-589）。

### 1.6 fixture（WK-1a 已 merge）

`spa/src/lib/watch/__fixtures__/` 有八個檔：
- `watch_wake_settings_get.json`
- `…_get_stale.json`（`source_id: ""`、`revision: 0`、`is_source: false`、`stale: true`、`fetched_at: null`）
- `…_put_200.json`（revision 2，含多行 prompt、非空的 times 與 kinds_off）
- `…_put_400.json`（三筆錯誤：`rules.patrol.enabled`/`unknown_field`、`rules.patrol.max_gap_hours`/`out_of_range`、`rules.patrol.times[0]`/`bad_time`）
- `…_put_400_missing_expect.json`
- `…_put_403.json`（`device_forbidden`＋`detail`）
- `…_put_409.json`（`current` 為 revision 1）
- `…_put_503.json`

守門測試是 `spa/src/lib/watch/fixtures-sync.guard.test.ts`，會核對位元組，也會核對每個 Go 檔都有副本（:28-31）。**沒有任何 fixture** 帶非空的 `suppressed`、失敗的 `patrol_status`、`version: 2` 或 `readonly: true`，見 §10。

### 1.7 出版現況

每一版都在 bump commit 上 `electron:build`（`~/.local/share/purdex-lead/deploy/release-774.sh:8`、`:267`）。mlab 沒開 SPA dev server（10-21 查 5174 沒有人監聽），所以 merge 的 SPA 改動會在下一次打包時送到使用者手上。因此 5b 各張靠 `wake_rules.v1` 閘門隱藏（spec:109、:226）。

---

## 2. 使用者看到什麼

### 2.1 版面

分頁名稱「設定」，圖示 `GearSix`。單欄，只用 `SettingItem`、`ToggleSwitch`、受控 `Section`、勾選框，不另做視覺設計。

```
喚醒條件
  這份設定所有主機共用，存在 mlab。（經 air26 讀取）       ← 第二句只在讀的不是正本時出現
  [狀態橫幅，見 2.2]
  自動喚醒                                        [開]
    關掉時，除了巡檢以外的條件都不會叫醒守門員。巡檢照常。
  守門員沒醒時叫起或啟動                            [開]
    關掉時，只有已經醒著的守門員會收到通知。巡檢不受影響，一律可以叫起。

  巡檢（沒有開關）
    巡檢一定會執行，沒有開關；這裡只能放寬間隔或加上固定時刻。
    最近一次巡檢：10:32（mlab）                    ← 5b-4；失敗時改成警告句
    守門員休息中／你在電腦前／你不在電腦前（沒開無人值守）／無人值守，主機吃緊／無人值守，主機平穩   [n] 分鐘
      每隔幾分鐘巡一次。0＝這個情境不另外巡，只靠最長不巡上限。
    最長不巡上限 [24] 小時
    每天固定時刻  09:00 ×  21:00 ×  [HH:MM] [加入]   時刻照 Asia/Taipei 計算
    附加 prompt  [多行文字框]

  ▸ 磁碟空間不足                [開]
  ▸ 無人值守開關切換            [開]
  ▸ 你回到電腦前  新            [開]
  ▸ wwd 警報                    [開]   ← 展開：prompt、要叫的等級、要叫的警報種類

▸ 最近沒叫的紀錄
```

- 規則卡預設收合。收合時標題列有開關；附了 prompt 的另標「已附 prompt」。展開狀態記在元件外。
- 巡檢區塊不收合。紀錄區塊預設收合。

### 2.2 狀態與對應畫面

| 狀態 | 怎麼判斷 | 畫面 |
|---|---|---|
| 沒有可讀的主機 | 沒有已連線、宣告 `wake_settings.v1` 的 App 主機 | 整頁一句 `watch.wake.none` |
| 讀取中 | 沒有記住的資料，且正在讀 | `watch.wake.loading`。有記住的資料就直接顯示，背景重讀 |
| 讀不到 | 每個候選都是 `no_answer` 或 `server_error` | `watch.wake.unreadable`＋「重新讀取」 |
| 讀不懂 | GET 回應形狀不對（`app_mismatch`） | `watch.wake.err.app_mismatch`＋「重新讀取」 |
| 正本連不上 | `stale: true` 且 `fetched_at` 有值 | 黃色橫幅 `watch.wake.stale`（含正本名與時間）。不鎖編輯：PUT 會轉給正本，有 etag 擋著，失敗走 503 的畫面 |
| 從未取到 | `fetched_at: null`、`source_id: ""`（spec:132；fixture `…_get_stale.json`） | **整塊唯讀**（v4）：顯示預設值，橫幅 `watch.wake.stale_never`（「還沒讀到正本（mlab），暫時不能改」）。不收任何修改，所以不會有待送的東西 |
| 唯讀（版本） | `readonly: true`，或 `settings.version !== 1`（spec:103） | 橫幅 `watch.wake.readonly`，整頁 `disabled`；看得懂的欄位照顯示 |
| 裝置 token | 這台某次 PUT 回 403 `device_forbidden`（本次執行期間記住） | 橫幅 `watch.wake.device`（「這台裝置不能改設定」）；整頁 `disabled`；待送的修改丟掉，prompt 草稿留著但不能編輯 |
| 有欄位衝突 | 4.4 的三方比對產生衝突 | 橫幅 `watch.wake.notice.conflicts`（帶件數）。每個衝突欄位旁有衝突條（2.3） |
| 503 | `source_unreachable` | 橫幅 `watch.wake.notice.unreachable`（帶 `source`）＋「重試」（重試全部）；被動到的欄位轉成「沒有送出」並各附「重試」（2.3）；立刻重讀一次 |
| 沒有回應 | 真正的 fetch 失敗 | 橫幅 `watch.wake.notice.no_answer`＋「重試」；欄位轉成「沒有送出」；重讀一次 |
| 主機錯誤 | 其他非 2xx 狀態碼（例如 500、502、404） | 橫幅 `watch.wake.notice.server_error`（帶狀態碼）＋「重試」；欄位轉成「沒有送出」；重讀一次 |
| 格式不合 | PUT 回應形狀不對，或 400 裡有 App 不該犯的代碼 | 橫幅 `watch.wake.err.app_mismatch`（App 與主機版本不合、要更新），**沒有**「重試」；欄位轉成「沒有送出」，只給「採用目前值」 |
| 正本換了 | 從一個真的 `source_id` 換到另一個真的（`""` → 真的 id 不算，4.3） | 橫幅 `watch.wake.notice.source_changed`；待送、在途、衝突的修改**全部轉成「沒有送出」**（可重試），prompt 草稿保留 |
| 寫入中 | 有 PUT 在路上 | 被動到的欄位旁顯示小字 `watch.wake.saving`；其他欄位照常可改（排隊） |

橫幅樣式照 `WatchSettings.tsx:154-155`（`role="status"`、`text-status-warning-text`）。

### 2.3 衝突條、「沒有送出」與 prompt 草稿

- **「沒有送出」**（4.4；畫面在 5b-2）：開關、數字框、時刻 chip、等級、種類都可能出現。
  - 欄位顯示使用者的值，旁邊標 `watch.wake.unsent`「沒有送出」。
  - 503、沒有回應、主機錯誤：附 `watch.wake.retry`「重試」與「採用目前值」。橫幅另有一個「重試」，一次重試全部。
  - 格式不合：只給「採用目前值」，沒有重試。
  - 按「重試」會走 4.4 的三方比對再送：別人沒改就送出，成功後標記消失；已經被別人改了就轉成衝突條。
  - 「採用目前值」就丟掉這筆修改。
- **衝突條**：會出現在數字框、prompt、時刻、等級、種類。開關不會衝突，原因見 4.4。衝突期間，欄位顯示使用者的值。
  - 數字框、時刻、等級：`watch.wake.field_conflict`，寫明「目前是 X、你的是 Y」。時刻用「、」串起來，等級用等級名稱。
  - prompt：`watch.wake.field_conflict_text`，內容太長不列出。
  - 種類：`watch.wake.field_conflict_list`，清單可能很長，不列出。
  - 兩個按鈕：`watch.wake.send_mine`「送出我的值」、`watch.wake.take_current`「採用目前值」。
  - 「送出我的值」會以目前的伺服器值為 base、我的值為 value，建一筆新的 patch。
  - 「採用目前值」就把衝突丟掉；prompt 的話連草稿一起清掉。
- **從未取到時**（`fetched_at: null`）整塊唯讀：不收修改，所以沒有衝突、也沒有「沒有送出」（v4）。
- **prompt 草稿一律保留**，直到伺服器上的值等於草稿（200，或採用到的文件就是這段字），或使用者按「採用目前值」。
  - 送出、400、409、403、503、沒有回應、主機錯誤、格式不合，都不會清掉 prompt 草稿（403 時留著但不能編輯）。
  - 草稿底下的伺服器值變了就標 stale（`watch.wake.field_conflict_text`），提供「採用目前值」。
- **跟現行 `WatchSettings.tsx` 不同的地方**：現行是送出前就清掉草稿，400／409 一律結束草稿（:52-58；`WatchSettings.test.tsx:144-160`）。新分頁的 prompt 不照這樣做，主機分頁的 `GuardianSection` 則維持原樣。

### 2.4 總開關與自動叫起（5b-3）

- `enabled`：「自動喚醒」，說明寫明巡檢照常。
- `auto_start`：「守門員沒醒時叫起或啟動」，說明寫明巡檢不受影響。
- 總開關關著時，四張規則卡各加一行 `watch.wake.master_off_hint`（wwd 卡在 5b-3，另外三張在 5b-4）。

### 2.5 巡檢（間隔、上限、prompt 在 5b-2；固定時刻與時區在 5b-3；巡檢狀態那行在 5b-4）

- **沒有開關**。App 在讀到的原文上改指定欄位，送出的文件永遠不會有 `rules.patrol.enabled`。
- 五個情境的數字框（分鐘）：前端只檢查整數，範圍交給主機，0 照送。
- 最長不巡上限（小時）：說明寫明不能關。
- 固定時刻：
  - 已排序的 chip，各帶 ×；加一個輸入框和「加入」。
  - 前端先擋格式（`^([01]\d|2[0-3]):[0-5]\d$`）與重複；超過 24 個交給主機回 `too_many`。
  - 加入、移除都立刻寫入。
- 時區只顯示 `tz`，App 不能改。
- 附加 prompt：多行（`rows=4`），失焦或 ⌘／Ctrl＋Enter 送出，草稿規則見 2.3。
- 巡檢狀態（5b-4）：
  - 成功：「最近一次巡檢：時間（主機）」。
  - 失敗：`last_fail_at` 比 `last_ok_at` 新時，顯示警告句 `watch.wake.status_fail`。原因用 `last_fail_code` 翻譯（spec:134 的四個代碼），後面以淡色附上 `last_fail_detail` 原文；代碼不認得就只顯示原文。
  - 兩者都沒有：`watch.wake.status_none`。

### 2.6 三條規則（5b-4）

| 規則 | 標題 | 參數 | 備註 |
|---|---|---|---|
| `disk_low` | 磁碟空間不足 | `below_gb`「低於（GB）」 | |
| `unattended_changed` | 無人值守開關切換 | 無 | |
| `user_back` | 你回到電腦前 | `away_min`「離開至少（分鐘）」 | 標題旁標「新」（accent 色小標籤） |

每張卡：標題列放開關（`rules.<r>.enabled`）；展開後是說明、參數、附加 prompt。

### 2.7 wwd 警報整塊（5b-3）

- 開關與附加 prompt，同 2.6。
- 等級：三個 `ToggleSwitch`（推播、摘要、守門員），對應 `levels` 的成員。
- 種類：依 `known_kinds` 的順序列出勾選框（不分組）。
  - 打勾＝要叫，取消＝加進 `kinds_off`。
  - 每行後面用小 chip 標出這個 kind 的 `levels`（spec:133，例如 `orphans` 是 `["digest","push"]`）。
  - 它的 `levels` 全部沒開時，那行加灰字 `watch.wake.kind_level_off`，但**不鎖勾選框**（spec:133）。
- 名稱用 `watch.wake.kind_name`：
  - zh-TW＝`{{label}}`；en＝`{{label}} ({{kind}})`，也就是照 daemon 的中文 label 並附原字（spec:133）。
  - label 是空的就只顯示 kind。
- `kinds_off` 裡不在 `known_kinds` 的值，另列一組「這版 App 不認得的種類（照原字顯示）」，每個 chip 可以按 × 拿掉。兩種語系都照原字。

### 2.8 最近沒叫的紀錄（5b-4）

- 每筆一行：
  - 時間：用 `hhmm`，不是今天的加上月日。
  - 主機。
  - 規則名：沿用 `watch.wake.patrol` 與 `watch.wake.rule.*`，不認得的照原字。
  - 原因：`watch.wake.why.<why>`，涵蓋 spec:131 的五個加上 :134 的 `patrol_failed`；不認得的照原字。
  - `detail`：單行，太長就截斷，全文放在 `title`。
- 讀的不是正本時，加一行 `watch.wake.records_local`。沒有資料時顯示 `watch.wake.records_none`。

### 2.9 主機分頁的守門員區塊

**WK-5h：閒置時休息（不受閘門，現有 daemon 就支援）**
- `FIELDS` 的 `{ key: 'mode', kind: 'mode' }` 改成 `{ key: 'mode', kind: 'rest' }`，畫成 `ToggleSwitch`「閒置時休息」。
  - 開＝寫 `mode: 'patrol'`，關＝寫 `mode: 'resident'`。存的值與 daemon 都不變，不用遷移。
  - 顯示：`valueOf(w, 'mode') !== 'resident'` 就是開。沒寫過的列預設是 `patrol`（`watch-row-write.ts:10`），所以顯示開。
  - 標籤與說明用新鍵 `watch.set.f.rest_idle`、`watch.set.d.rest_idle`，kind 為 `rest` 時改查這兩個鍵。testid 維持 `watch-set-row-mode`、`watch-set-mode`，既有的 :76 斷言不用改。
- `patrol_idle_min` 改標「閒置多久後休息（分鐘）」。開關關著時，這一列 `disabled`：值照顯示，但不能編輯，也不會送出。整列寫回時照 `rowWith` 原樣帶回。
- 移除不再使用的 `SegmentControl` import，以及四個語系鍵（§3.0）。

**WK-5b-4：受 `wake_rules.v1` 閘門**
- 看那台主機的 `daemon` 有沒有宣告 `watch.wake_rules.v1`。
- **有**：`FIELDS` 濾掉 `enabled`、`auto_wake`；區塊最上方加一行 `watch.set.moved` 和連結按鈕 `watch.set.open_settings`（`openWatchArea({ tab: 'settings' })`）。
- **沒有**：照舊（WK-5h 改過之後的樣子），沒有連結。

### 2.10 驗證錯誤碼 → 欄位與文字

App 只看 `code`（spec:135）。`path` 先去掉 `[i]` 再查欄位表，查不到的放頂部。

| code | path | 顯示在 | 語系鍵 |
|---|---|---|---|
| `missing_field`、`unknown_field`（含 `rules.patrol.enabled`）、`bad_type`、`bad_version`；`error: missing_expect` | 任何 | 頂部（整個 PUT 視為格式不合） | `watch.wake.err.app_mismatch` |
| `out_of_range` | `rules.patrol.interval_min.<情境>` | 該框下 | `watch.wake.err.interval` |
| `out_of_range` | `rules.patrol.max_gap_hours` | 該框下 | `watch.wake.err.max_gap` |
| `out_of_range` | `rules.disk_low.below_gb` | 該框下 | `watch.wake.err.below_gb` |
| `out_of_range` | `rules.user_back.away_min` | 該框下 | `watch.wake.err.away_min` |
| `out_of_range` | 其他 | 該欄位或頂部 | `watch.wake.err.range` |
| `bad_time` | `rules.patrol.times[i]` | 時刻列下 | `watch.wake.err.bad_time` |
| `duplicate_time` | `rules.patrol.times[i]` | 時刻列下 | `watch.wake.err.duplicate_time` |
| `too_many` | `rules.patrol.times` | 時刻列下 | `watch.wake.err.too_many_times` |
| `too_many` | `rules.wwd_alert.kinds_off` | 種類清單下 | `watch.wake.err.too_many_kinds` |
| `bad_level` | `rules.wwd_alert.levels[i]` | 等級列下 | `watch.wake.err.bad_level` |
| `bad_kind` | `rules.wwd_alert.kinds_off[i]` | 種類清單下 | `watch.wake.err.bad_kind` |
| `prompt_too_long` | `rules.<r>.prompt` | 該 prompt 下（草稿保留） | `watch.wake.err.prompt_too_long` |
| `prompt_bad_char` | `rules.<r>.prompt` | 該 prompt 下（草稿保留） | `watch.wake.err.prompt_bad_char` |
| `bad_tz` | `rules.patrol.tz` | 時區那行 | `watch.wake.err.bad_tz` |
| 其他代碼 | 任何 | 該欄位或頂部 | `watch.wake.err.other`（帶主機的 `message`） |

前端自己擋、不送出的：非整數（`watch.wake.err.not_int`）、時刻格式、時刻重複。

---

## 3. 語系鍵

兩份都要加，字面不出現內部代碼，placeholder 用 `{{x}}`。`locale-completeness.test.ts` 會檢查兩邊的 key 相同、沒有空值；它沒有「未使用的鍵」規則，所以不再用的鍵照慣例直接從兩份刪掉。

### 3.0 WK-5h（新增 2、改值 2、刪除 4）

| 動作 | key | zh-TW | en |
|---|---|---|---|
| 新增 | `watch.set.f.rest_idle` | 閒置時休息 | Rest when idle |
| 新增 | `watch.set.d.rest_idle` | 開著時，守門員閒置一段時間後會休息；定時巡檢不受影響，休息中也會被巡檢叫醒。 | On: the watcher rests after a while idle. The patrol is not affected and still wakes a resting watcher. |
| 改值 | `watch.set.f.patrol_idle_min` | 閒置多久後休息（分鐘） | Rest after idle (minutes) |
| 改值 | `watch.set.d.patrol_idle_min` | 守門員閒置這麼久就休息；「閒置時休息」關著時不使用這個值。 | How long the watcher stays idle before it rests; not used while "Rest when idle" is off. |
| 刪除 | `watch.set.f.mode`、`watch.set.d.mode`、`watch.set.mode.resident`、`watch.set.mode.patrol` | — | — |

### 3.1 WK-5a-1（錯誤，17 個；`errorKeyOf` 的測試會檢查這些鍵兩邊都在）

| key | zh-TW | en |
|---|---|---|
| `watch.wake.err.app_mismatch` | 這個 App 和主機的版本不合，修改沒有存到；請先更新 App（或主機）。 | This App and the host are on versions that do not match, so your change was not saved; update the App (or the host) first. |
| `watch.wake.err.interval` | 要是 0，或 1 到 1440 分鐘。 | Use 0, or 1 to 1440 minutes. |
| `watch.wake.err.max_gap` | 要在 1 到 72 小時之間，不能是 0。 | Use 1 to 72 hours; it cannot be 0. |
| `watch.wake.err.below_gb` | 要在 1 到 100000 GB 之間。 | Use 1 to 100000 GB. |
| `watch.wake.err.away_min` | 要在 5 到 1440 分鐘之間。 | Use 5 to 1440 minutes. |
| `watch.wake.err.range` | 超出允許的範圍。 | Out of the allowed range. |
| `watch.wake.err.bad_time` | 時刻要寫成 HH:MM（24 小時制），例如 09:00。 | Write a time as HH:MM (24-hour), e.g. 09:00. |
| `watch.wake.err.duplicate_time` | 這個時刻已經有了。 | That time is already on the list. |
| `watch.wake.err.too_many_times` | 最多 24 個時刻。 | At most 24 times. |
| `watch.wake.err.too_many_kinds` | 最多只能停用 32 種警報。 | At most 32 kinds can be turned off. |
| `watch.wake.err.bad_level` | 等級只能是推播、摘要或守門員。 | A level is push, digest or watcher. |
| `watch.wake.err.bad_kind` | 有一個警報種類的名字不合規則。 | An alert kind has a name that is not allowed. |
| `watch.wake.err.prompt_too_long` | 太長了：最多 2000 位元組（中文大約 660 字）。 | Too long: at most 2000 bytes. |
| `watch.wake.err.prompt_bad_char` | 含有不能用的控制字元；換行和 Tab 可以。 | It contains a control character that is not allowed; new lines and tabs are fine. |
| `watch.wake.err.bad_tz` | 主機不認得這個時區。 | The host does not know this time zone. |
| `watch.wake.err.other` | 主機不接受這個值：{{message}} | The host refused this value: {{message}} |
| `watch.wake.err.not_int` | 請輸入整數。 | Enter a whole number. |

### 3.2 WK-5b-1（外框與橫幅，18 個）

| key | zh-TW | en |
|---|---|---|
| `watch.pane.settings` | 設定 | Settings |
| `watch.wake.title` | 喚醒條件 | Wake conditions |
| `watch.wake.shared` | 這份設定所有主機共用，存在 {{source}}。 | One set for every host, kept on {{source}}. |
| `watch.wake.via` | （經 {{host}} 讀取） | (read through {{host}}) |
| `watch.wake.loading` | 正在讀取喚醒設定… | Reading the wake settings… |
| `watch.wake.none` | 沒有連線中、支援這項設定的主機。 | No connected host offers these settings. |
| `watch.wake.unreadable` | 目前讀不到喚醒設定。 | The wake settings cannot be read right now. |
| `watch.wake.reload` | 重新讀取 | Read again |
| `watch.wake.stale` | {{source}} 現在連不上，下面是 {{time}} 取得的副本。改動會轉給 {{source}}，連不上時會失敗。 | {{source}} cannot be reached; below is the copy fetched at {{time}}. A change is passed on to {{source}} and fails while it cannot be reached. |
| `watch.wake.stale_never` | 還沒讀到正本（{{source}}），暫時不能改；下面是預設值。 | The source ({{source}}) has not been read yet, so nothing can be changed for now; below are the defaults. |
| `watch.wake.readonly` | 這份設定是較新版的 Purdex 寫的，這個 App 只能看、不能改；請更新 App。 | These settings were written by a newer Purdex; this App can show them but not change them. Update the App. |
| `watch.wake.device` | 這台裝置不能改設定，只能看；還沒送出的修改已放棄。 | This device cannot change the settings, only look; changes not yet sent were dropped. |
| `watch.wake.saving` | 儲存中… | Saving… |
| `watch.wake.notice.conflicts` | 有 {{n}} 個欄位在別處被改過，請在欄位旁決定要送出你的值還是採用目前值。 | {{n}} fields were changed elsewhere; decide beside each one whether to send yours or use the current value. |
| `watch.wake.notice.unreachable` | {{source}} 現在連不上，修改還沒有存到，已標成「沒有送出」；已重新讀取。 | {{source}} cannot be reached, so your changes are not saved yet and are marked "Not sent"; the settings were read again. |
| `watch.wake.notice.no_answer` | 主機沒有回應，修改還沒有存到，已標成「沒有送出」。 | The host did not answer; your changes are not saved yet and are marked "Not sent". |
| `watch.wake.notice.server_error` | 主機回了錯誤（{{status}}），修改還沒有存到，已標成「沒有送出」。 | The host answered with an error ({{status}}); your changes are not saved yet and are marked "Not sent". |
| `watch.wake.notice.source_changed` | 設定的存放處換成 {{source}}；還沒存到的修改已標成「沒有送出」，確認後請按重試。 | The settings are now kept on {{source}}; changes not yet saved are marked "Not sent", so check them and press Retry. |

5b-1 只顯示橫幅的字。橫幅上的「重試」按鈕在 5b-2 才加，因為它要用到 5a-3 的寫入 store。

### 3.3 WK-5b-2（巡檢的間隔／上限／prompt、衝突條、「沒有送出」，20 個）

| key | zh-TW | en |
|---|---|---|
| `watch.wake.patrol` | 巡檢 | Patrol |
| `watch.wake.patrol_d` | 巡檢一定會執行，沒有開關；這裡只能放寬間隔或加上固定時刻。 | The patrol always runs and has no switch; here you can only space it out or add fixed times. |
| `watch.wake.ctx.resting` | 守門員休息中 | Watcher resting |
| `watch.wake.ctx.present` | 你在電腦前 | You are at the computer |
| `watch.wake.ctx.away` | 你不在電腦前（沒開無人值守） | You are away (unattended mode off) |
| `watch.wake.ctx.busy` | 無人值守，主機吃緊 | Unattended, host under load |
| `watch.wake.ctx.calm` | 無人值守，主機平穩 | Unattended, host calm |
| `watch.wake.interval_d` | 每隔幾分鐘巡一次。0＝這個情境不另外巡，只靠最長不巡上限。 | Minutes between patrols. 0: no patrol of its own here, only the longest gap applies. |
| `watch.wake.minutes` | 分鐘 | min |
| `watch.wake.max_gap` | 最長不巡上限 | Longest gap without a patrol |
| `watch.wake.max_gap_d` | 不論上面怎麼設，超過這麼久沒巡就一定巡一次（1–72 小時，不能關）。 | Whatever is set above, a patrol runs once this long has passed without one (1–72 hours; it cannot be turned off). |
| `watch.wake.hours` | 小時 | h |
| `watch.wake.prompt` | 附加 prompt | Extra prompt |
| `watch.wake.prompt_d` | 叫醒時接在通知後面一起送給守門員，可以多行。 | Sent to the watcher after the wake notice; it may span several lines. |
| `watch.wake.field_conflict` | 這欄在別處被改過：目前是 {{theirs}}，你的是 {{mine}}。 | This was changed elsewhere: it is now {{theirs}}, yours is {{mine}}. |
| `watch.wake.field_conflict_text` | 這欄在別處被改過；你輸入的內容還保留著。 | This was changed elsewhere; what you typed is kept. |
| `watch.wake.send_mine` | 送出我的值 | Send mine |
| `watch.wake.take_current` | 採用目前值 | Use the current value |
| `watch.wake.unsent` | 沒有送出 | Not sent |
| `watch.wake.retry` | 重試 | Retry |

（v2 留的 `watch.wake.on`／`off` 拿掉：開關不會衝突，「沒有送出」也只顯示使用者的值，不需要開／關的字。）

### 3.4 WK-5b-3（總開關、固定時刻、wwd 警報整塊、清單的衝突條，21 個）

| key | zh-TW | en |
|---|---|---|
| `watch.wake.master` | 自動喚醒 | Wake automatically |
| `watch.wake.master_d` | 關掉時，除了巡檢以外的條件都不會叫醒守門員。巡檢照常。 | Off: nothing but the patrol wakes the watcher. The patrol goes on. |
| `watch.wake.auto_start` | 守門員沒醒時叫起或啟動 | Wake or start a watcher that is not awake |
| `watch.wake.auto_start_d` | 關掉時，只有已經醒著的守門員會收到通知。巡檢不受影響，一律可以叫起。 | Off: only a watcher that is already awake is told. The patrol is not affected and may always wake one. |
| `watch.wake.times` | 每天固定時刻 | Fixed times each day |
| `watch.wake.times_d` | 到了這些時刻一定巡一次，時刻照 {{tz}} 計算。 | A patrol runs at each of these times, read in {{tz}}. |
| `watch.wake.times_none` | 沒有固定時刻 | No fixed times |
| `watch.wake.times_add` | 加入 | Add |
| `watch.wake.times_remove` | 移除 {{time}} | Remove {{time}} |
| `watch.wake.master_off_hint` | 自動喚醒關著，這條暫時不會叫。 | Waking automatically is off, so this does not wake for now. |
| `watch.wake.prompt_set` | 已附 prompt | Has a prompt |
| `watch.wake.rule.wwd_alert` | wwd 警報 | wwd alerts |
| `watch.wake.rule_d.wwd_alert` | 任何一台出現新的 wwd 警報時叫。 | When any host raises a new wwd alert. |
| `watch.wake.levels` | 要叫的等級 | Levels that wake |
| `watch.wake.levels_d` | 推播：會推到手機的；摘要：只進事件頁與摘要的；守門員：只給守門員看的。 | Push: sent to the phone. Digest: only in the events tab and the summary. Watcher: only for the watcher. |
| `watch.wake.kinds` | 要叫的警報種類 | Alert kinds that wake |
| `watch.wake.kind_level_off` | 這種警報的等級目前都不叫 | None of its levels wakes now |
| `watch.wake.kinds_unknown` | 這版 App 不認得的種類（照原字顯示） | Kinds this App does not know (shown as written) |
| `watch.wake.kind_remove` | 拿掉 {{kind}} | Remove {{kind}} |
| `watch.wake.kind_name` | {{label}} | {{label}} ({{kind}}) |
| `watch.wake.field_conflict_list` | 這份清單在別處被改過；畫面上是你的版本。 | This list was changed elsewhere; what you see is your version. |

### 3.5 WK-5b-4（三條規則、巡檢狀態、紀錄、主機分頁，28 個）

| key | zh-TW | en |
|---|---|---|
| `watch.wake.rule.disk_low` | 磁碟空間不足 | Disk space low |
| `watch.wake.rule_d.disk_low` | 這台剩餘空間低於門檻時叫一次。 | Once when this host's free space drops below the line. |
| `watch.wake.below_gb` | 低於（GB） | Below (GB) |
| `watch.wake.rule.unattended_changed` | 無人值守開關切換 | Unattended mode switched |
| `watch.wake.rule_d.unattended_changed` | 有人打開或關掉無人值守模式時叫。 | When someone turns unattended mode on or off. |
| `watch.wake.rule.user_back` | 你回到電腦前 | You are back at the computer |
| `watch.wake.rule_d.user_back` | 離開一段時間後回到電腦前時叫一次。 | Once when you come back after being away for a while. |
| `watch.wake.away_min` | 離開至少（分鐘） | Away at least (min) |
| `watch.wake.new` | 新 | New |
| `watch.wake.status_ok` | 最近一次巡檢：{{time}}（{{host}}） | Last patrol: {{time}} ({{host}}) |
| `watch.wake.status_none` | 還沒有巡檢紀錄 | No patrol yet |
| `watch.wake.status_fail` | 最近一次巡檢沒能叫起守門員（{{time}}）：{{why}} | The last patrol could not wake a watcher ({{time}}): {{why}} |
| `watch.wake.fail.targets_unreachable` | 可以啟動守門員的主機全部連不上 | None of the hosts the watcher may start on can be reached |
| `watch.wake.fail.targets_unknown` | 守門員的啟動主機裡有不存在的主機 | The watcher's start hosts name a host that does not exist |
| `watch.wake.fail.start_failed` | 叫起或啟動守門員失敗 | Waking or starting the watcher failed |
| `watch.wake.fail.deliver_failed` | 守門員在，但通知送不到 | The watcher is there, but the notice did not reach it |
| `watch.wake.records` | 最近沒叫的紀錄 | Recently not woken |
| `watch.wake.records_d` | 因為設定而沒有叫醒守門員的最近 50 筆。 | The last 50 times the settings kept the watcher from being woken. |
| `watch.wake.records_none` | 沒有紀錄 | Nothing yet |
| `watch.wake.records_local` | 這是 {{host}} 自己的紀錄（{{source}} 連不上）。 | This is {{host}}'s own list ({{source}} cannot be reached). |
| `watch.wake.why.master_off` | 自動喚醒關著 | Waking automatically was off |
| `watch.wake.why.rule_off` | 這條關著 | This rule was off |
| `watch.wake.why.level_off` | 這個等級不叫 | Its level does not wake |
| `watch.wake.why.kind_off` | 這種警報不叫 | Its kind does not wake |
| `watch.wake.why.no_auto_start` | 守門員沒醒，又不允許叫起 | No watcher was awake, and none may be woken |
| `watch.wake.why.patrol_failed` | 巡檢叫不起守門員 | The patrol could not wake a watcher |
| `watch.set.moved` | 喚醒條件、巡檢與 prompt 已移到守門員專區的「設定」分頁。 | Wake conditions, the patrol and their prompts are now on the watcher area's Settings tab. |
| `watch.set.open_settings` | 打開「設定」 | Open Settings |

---

## 4. 資料流

### 4.1 讀哪一台，候選有變就重選

- **候選**：已連線、`fetchedAt > 0`、宣告 `watch.wake_settings.v1` 的 App 主機，依 host order（`wakeSettingsDaemons()`，寫法同 `jobsDaemons()`）。
- **選擇順序**：
  1. 已採用文件的 `source_id`（不是 `""`）等於某候選的 `daemonId`（`selectDaemonIdMismatch` 為 undefined），選它；
  2. 否則用 `usePoolStore.sourceHostId`，條件是它在候選裡；
  3. 否則用第一個候選。
- **跳到正本**：讀到 `is_source: false`，而且 `source_id` 不是 `""`、又等於某候選的 `daemonId`，就改讀那台一次。每一輪最多跳一次。
- **失敗**：`no_answer` 或 `server_error` 換下一個候選；`app_mismatch` 不換，直接顯示「讀不懂」。
- **即時重選**：store 在第一次 `ensure` 時訂閱 `useHostStore` 與 `useNexHostStore`，算出「候選簽章」（依序列出候選 id、各自的 `daemonId` 與 capability）。簽章一變就立刻重選；如果讀的那台換了，就換世代並重讀，不等 60 秒的輪詢。涵蓋三種情況：候選從無到有、正本候選稍後才完成 `/api/info`、目前讀的那台失去 capability 或斷線。

### 4.2 什麼時候讀

- 分頁 mount 時一定重讀。有記住的資料先顯示，背景重讀。
- 目前讀的那台送來 `watch.wake_settings.changed`。value 不讀；別台送的忽略。
- 4.1 的重選。斷線、重連、re-point 也走重選。
- 503、沒有回應之後；採用到正本切換之後；按「重新讀取」。
- **分頁開著時每 60 秒重讀**，卸載就停（計時器放在 pane 的 effect 裡，不放 store）。原因是 `suppressed` 與 `patrol_status` 變動時沒有事件，事件只對設定變更發（spec:102）。

### 4.3 採用規則與正本身分（finding 3、4；v4 修 codex 複審第 2 條）

**文件身分是 `(source_id, revision)`。**
- 還沒採用過任何文件時，第一份一律採用。
- 同一個 `source_id`：只採用 revision **嚴格較大**的。相等的不採用，也不觸發比對。

**從未取到（`source_id: ""`，`fetched_at: null`）**
- 這時整塊**唯讀**：顯示預設值，橫幅 `watch.wake.stale_never`。`change` 直接拒絕，所以不會有待送、在途、衝突或「沒有送出」。prompt 框也不能打字，所以不會有草稿。
- `""` → 真的 id：**不算正本切換**，直接採用。為了讓還帶著 `""` 的晚到回應被丟掉，`sourceEpoch++`；除此之外什麼狀態都不動（本來也沒有待送的東西）。不顯示 `source_changed`，也不另外重讀。
- 已經採用過真的 id 之後，再收到 `source_id: ""` 的回應：不採用文件，也不採用它的中繼資料，避免出現「還沒讀到正本」橫幅。對這個讀取對象，當成一次讀取失敗，依 4.1 換下一個候選。

**真的 A → 真的 B（正本切換）**
- 採用 B，`sourceEpoch++`，revision 基準重設，所以 B 的 revision 比 A 小也照樣採用。
- 待送、在途、衝突的修改**不丟**，全部轉成「沒有送出」（可重試、不是 `uncertain`，見 4.4）。衝突的那筆轉成 `{ base: 衝突時的伺服器值, value: 使用者的值 }`。
- prompt 草稿保留。草稿的 key 不含 `source_id`（§5）。
- 顯示 `source_changed` 橫幅，然後立刻重讀。
- 使用者按重試時，對 **B 的文件**做三方比對（4.4）。

**每個 request 送出時記下** `{ gen, sourceEpoch, readerHostId, connectionKey, seq }`
- `gen` 在讀取對象改變時加一；`sourceEpoch` 在 `""` → 真的 id 或正本切換時加一；`seq` 是遞增序號。
- 回應回來時，`gen` 不同、`sourceEpoch` 不同、或 connectionKey 變了，一律丟掉（status-store.ts:129-149 的做法，再加上正本身分）。所以**正本切換之前送出的 request，回應一律丟掉**。
- **PUT 回應被丟掉時**：
  - `sourceEpoch` 沒變（只是換了讀取對象）：它的在途修改轉成「沒有送出」，並標 `uncertain`（不知道寫進去沒有），等下一份採用的文件來比對。
  - `sourceEpoch` 變了：已經依上面的正本切換轉成「沒有送出」。
  - 兩種情況都會重讀。

**中繼資料與文件分開**：`stale`、`fetched_at`、`is_source`、`readonly`、`source`、`known_kinds`、`suppressed`、`patrol_status`，取自已接受的回應中 `seq` 最大的那份（上面說的 `""` 回應除外）。它跟文件是否採用無關，所以 60 秒輪詢回來同一個 revision，紀錄與巡檢狀態一樣會更新。

### 4.4 寫入：逐欄三方比對（finding 1、2、3；v4 修 codex 複審第 1 條）

**patch 的形狀**：每一筆都是 `{ path, base, value, phase }`。
- `path` 是整個欄位，**陣列也一樣**：`patrol.times`、`wwd_alert.levels`、`wwd_alert.kinds_off` 整個陣列當成一個值，不做逐成員合併（v4）。比較時陣列要深度相等。勾或取消一個種類時，是從畫面上的陣列加減那個成員，再整個寫成新的 `value`，所以 App 不認得的值會原樣留著。
- `phase` 有三種：`queued`（排隊）、`sent`（在途）、`unsent`（沒有送出，另帶 `retryable` 與 `uncertain`）。衝突是另外的欄位層級狀態。

**每一欄是一條依順序的鏈**
- 一欄同一時間最多有一筆 `sent`，後面最多跟一筆 `queued`。
- **使用者改了某一欄**：
  - 這一欄最後一筆是 `queued`：更新它的 `value`。`value === 它自己的 base` 就刪掉這筆，只跟自己的 base 比。
  - 最後一筆是 `sent`：**不折進在途那筆**，另起一筆 `queued`，`base` 是**在途那筆的 `value`**，也就是在途成功後伺服器預期的值。
  - 這一欄沒有鏈：另起一筆 `queued`，`base` 是已採用文件裡這一欄的值。
  - 這一欄處於「沒有送出」：等於對這一欄按了重試（見下面「沒有送出」）。新的修改接在鏈尾。
  - 這一欄處於衝突：衝突裡「你的值」換成新值。
- 文字欄在失焦或 ⌘Enter 時才算「改了」。

**送出**
- 沒有在途 PUT、有 `queued`、而且可寫時，所有 `queued` 都變成 `sent`，送 `{ settings, expect_etag }`：
  - `settings` 是已採用原文的深拷貝，把每一欄設成它鏈尾的 `value`。絕不從型別重建，所以看不懂的欄位會原樣保留，也不會出現 `rules.patrol.enabled`。
  - `expect_etag` 是已採用文件的 etag。
- 一次只有一個 PUT。

**比對：沿著鏈依序走**（`walkChain(serverValue, links)`）
- 從 `expected = 伺服器目前這一欄的值` 開始，依序看每一筆：
  - `link.base` 等於 `expected` → **保留**，然後 `expected = link.value`（假設它會生效）；
  - 否則 `link.value` 等於 `expected` → **完成**，移除；`expected` 不變；
  - 否則 → **衝突**。這一筆和這一欄後面所有的筆，合成一個欄位衝突 `{ theirs: 伺服器值, mine: 鏈尾的 value }`。
- 開關只有兩種值，伺服器值不是 base 就是 value，所以**開關永遠不會衝突**。數字、prompt、陣列都可能衝突。
- 走完之後，如果整條鏈都不在途，而且第一筆不是 `uncertain`，就把鏈收成一筆 `{ base: 第一筆的 base, value: 最後一筆的 value }`；`value === base` 就整條移除。`uncertain` 的鏈不收，理由見下。

**什麼時候走**
- **採用到新文件（背景 GET）時**：只走「這一欄沒有在途那筆」的鏈，包括 `queued` 與「沒有送出」。有在途那筆的欄位先不動，等它的 PUT 結果。
- **PUT 結果回來時**：
  - 200：在途那幾筆完成。能採用就採用回應。接著用「目前已採用的文件」走每一條有過在途的鏈；後面那筆的 base 就是在途的 value，會等於伺服器值而保留。然後照常送下一個 PUT。
  - 409：能採用就採用 `current`。**先**處理在途那筆的三方比對，**再**依序處理後面的。每一筆都拿 `expected`（伺服器目前值，或前一筆保留後的預期值）跟自己的 base 比。佇列不清空。只有產生衝突時才出現橫幅。

**各種結果**

| 結果 | 處理 |
|---|---|
| 200 | 見上 |
| 409 `etag_mismatch` | 見上 |
| 400 `invalid` | `fields` 裡出現 app_mismatch 那一組代碼，就整個當成格式不合（下一列）。否則 path（去掉 `[i]`）對得到的那幾欄，在途那筆丟掉：數字與陣列回到主機值，prompt 保留草稿並顯示錯誤；這幾欄後面排隊的那筆，`base` 改回伺服器值。其餘在途的回到 `queued`，在原文上重送 |
| 400 `missing_expect`、形狀不對（malformed 200、409 缺 `current`、`fields` 缺或畸形、403／503 的 body 不符） | `app_mismatch`：橫幅說明 App 與主機版本不合、要更新。在途與排隊的全部轉成**「沒有送出」、不可重試**，在途那幾筆另標 `uncertain`。不重讀 |
| 403 `device_forbidden` | 記住這台唯讀，整塊 `disabled`；所有鏈與衝突**全部丟掉**；橫幅說明「這台裝置不能改設定」 |
| 503 `source_unreachable` | 在途與排隊的全部轉成**「沒有送出」、可重試**，在途那幾筆另標 `uncertain`；橫幅帶 `source`；立刻重讀一次；**不自動重送** |
| fetch 失敗 | `no_answer`：處理同 503 |
| 其他狀態碼 | `server_error`：處理同 503，橫幅帶狀態碼 |

**「沒有送出」**（88 裁定：使用者的改動不能被悄悄丟掉）
- 鏈上的筆變成 `unsent` 之後，不會被下一個 PUT 帶出去，除非使用者按「重試」，所以不會自動重送。理由：結果不確定，正本也可能一直連不上，自動重送會變成迴圈。
- 畫面顯示「已採用文件＋每一欄鏈尾的 value」，被動到的欄位標「沒有送出」。
- **`uncertain`**：在途時失敗的那筆，不知道到底寫進去沒有。
  - 它的鏈不收成一筆。例：`0→1`（uncertain）、`1→0`；如果收成 `0→0` 就會被刪掉，但伺服器其實可能已經是 1，使用者最後要的 0 就不見了。
  - 送出時照樣把鏈尾的值送出去，由 etag 擋住：真的寫進去了就會拿到 409，再依 409 的流程走鏈。
  - 等 `seq` 晚於這次失敗的文件被採用、走鏈時看過它，`uncertain` 才解除。
- 每採用一份新文件，「沒有送出」的鏈也照上面的規則走。
  - 第一筆的 value 等於伺服器值就完成並消失，例如 503 其實已經寫進去，重讀後看到了；
  - base 等於伺服器值就保持「沒有送出」；
  - 兩者都不是就轉成衝突。
- **重試**（單欄或全部）：只對 `retryable` 的。把那幾條鏈改回 `queued`，先以已採用文件走一次鏈，已被別人改了就轉成衝突，然後照一般流程送出。成功後標記消失。
- **採用目前值**：丟掉那一欄整條鏈。
- prompt 的修改也走同一條鏈，草稿本來就一律保留（2.3）。
- 唯讀（版本、`readonly`、403、從未取到）時，`change` 與 `retry` 直接拒絕，不發請求。

### 4.5 三個例子

**不同欄位（A 在途、B 後改、A 收到 409）**
1. 「你在電腦前」從 30 改成 45：`sent{30→45}`，送出。
2. 在途時把「最長不巡上限」從 24 改成 48：這一欄沒有在途那筆，所以是 `queued{24→48}`。
3. A 收到 409，`current` 是別人把 `disk_low.below_gb` 改成 20。走鏈：
   - 情境：30＝base，保留；
   - 上限：24＝base，保留。
   - 下一個 PUT 帶著 45 與 48，疊在 `current` 上送出。
4. 如果 `current` 裡別人也把上限改成 36：上限轉成衝突（「目前是 36、你的是 48」），情境照常重送。

**同一欄在途時改回原值（codex 複審第 1 條）**
1. 某欄從 0 改成 1：`sent{0→1}`。
2. 在途時改回 0：另起 `queued{1→0}`，base 是在途的 value 1；`0 ≠ 1`，所以保留。
3. 200：在途完成，伺服器是 1。走鏈：`queued` 的 base 1＝伺服器值，保留。送出 0，最後是 0。
4. 換成 409、伺服器那欄仍是 0：
   - 在途那筆的 base 0＝伺服器值，保留，`expected` 變成 1；
   - 後面那筆的 base 1＝`expected`，保留；
   - 不在途了，鏈收成 `{0→0}` 後整條移除，不用送，最後是 0。
5. 換成 409、伺服器那欄被別人改成 5：在途那筆 base 0 ≠ 5、value 1 ≠ 5，轉衝突；後面那筆跟著併進同一個衝突，`mine = 0`。

**陣列在途時新增又刪除同一成員**
1. 時刻從 `[]` 加入 09:00：`sent{[]→[09:00]}`。
2. 在途時又移除 09:00：`queued{[09:00]→[]}`。
3. 200 之後送出 `[]`，最後不含 09:00。

---

## 5. tab-hosted state

新模組 `spa/src/lib/watch/wake-settings-memory.ts`，只放記憶體：
- `drafts: Map<string, string>`，key 是 `fieldId`。v4 起不含 `source_id`，因為正本切換時草稿要保留。用在數字框、時刻輸入框、每個 prompt。
  - 數字框與時刻輸入框：送出時清掉。
  - prompt：依 2.3，伺服器值等於草稿或使用者採用目前值時才清掉。
- `expanded: Set<string>`：規則卡 id 與 `'records'`。
- `scrollTop`。
- `clearWakeMemory()`：給測試用。v3 的 `forgetWakeDraftsExcept` 拿掉。

資料、佇列、衝突放在 store（模組層級），不放元件。

**回歸測試** `spa/src/components/watch/WatchSettingsPane.tab-switch.test.tsx`：真 `TabContent`、`keepAliveCount: 0`，每案都斷言切到 dashboard 時元件真的卸載了。

- 5b-1：
  - 切回來不閃讀取中，捲動位置還在。
  - **切走期間 store 因事件或 PUT 回應更新了，切回來顯示新版**，不是舊的快照。
  - **卸載期間沒有 60 秒計時器**（假時鐘推 120 秒，GET 次數不變），切回來後才重新啟動。
- 5b-2：
  - 巡檢 prompt 與某個情境數字框的草稿還在。
  - 「沒有送出」的標記與重試按鈕還在，因為存在 store。
  - **真的 A 換成真的 B**：切走期間採用到 B 的文件。切回來時 prompt 草稿還在；本來待送或在途的數字標「沒有送出」，可以重試；有 `source_changed` 橫幅。（v4 取代 v3 的「舊來源草稿丟掉」）
- 5b-3：wwd 卡的展開狀態、wwd prompt 草稿、時刻輸入框的草稿還在。
- 5b-4：三張規則卡的展開狀態、規則 prompt 草稿、紀錄的展開狀態還在。

WK-5h 擴充現有的 `WatchSettings.tab-switch.test.tsx`：切換之後，開關仍反映主機值，分鐘數草稿仍在。

---

## 6. fixture 與 parse 測試（5a-1 的 `wake-settings.test.ts`）

八個副本全部照抄引用（`import x from './__fixtures__/<name>.json'`）。B 線不新增、不修改它們，守門測試會核對位元組。

| fixture | 驗什麼 |
|---|---|
| `watch_wake_settings_get.json` | 每個欄位的型別都對：`settings` 五條規則、`etag`、`revision`、`source`、`source_id`、`is_source`、`stale`、`readonly`、`fetched_at`、`known_kinds`（kind／`levels` 陣列／label，`orphans` 是 `["digest","push"]`）、`suppressed`、`patrol_status`（五個欄位都是 null）。`withPath` 之後，除了那個 path 都和原文深度相等；在副本上多加一個不認得的欄位，`withPath` 也會保留 |
| `watch_wake_settings_get_stale.json` | `fetched_at: null` 且 `stale: true` → 狀態是「從未取到」，`writable` 為 false（v4：整塊唯讀）；`source_id: ""` 解析成「未知身分」，不會被拿去比對 `daemonId` |
| `watch_wake_settings_put_200.json` | 用 GET 的 parser 解析成功；多行 prompt、非空的 times 與 kinds_off 都讀得到 |
| `watch_wake_settings_put_400.json` | 得到 `invalid`，三筆 path／code／message；因為含 `unknown_field`，整體要分類為 `app_mismatch`（4.4）；另外三個 path 經 `fieldIdOfPath` 都對得到欄位 |
| `watch_wake_settings_put_400_missing_expect.json` | 得到 `app_mismatch` |
| `watch_wake_settings_put_403.json` | 得到 `forbidden`（看狀態碼加 `error === 'device_forbidden'`） |
| `watch_wake_settings_put_409.json` | 得到 `conflict`，`current` 用 GET 的 parser 解析成功 |
| `watch_wake_settings_put_503.json` | 得到 `unreachable`，`source` 是 `mlab` |

**形狀不符一律是 `app_mismatch`**（finding 9），用 `it.each` 列出：
- 200 但 body 不是物件，或缺 `settings`／`etag`／`revision`；
- 409 缺 `current`，或 `current` 形狀不對；
- 400 `invalid` 缺 `fields`、`fields` 不是陣列，或某筆缺 path／code；
- 403 的 `error` 不是 `device_forbidden`；
- 503 缺 `source`；
- 回應 body 不是 JSON。

**只有 `fetch` 丟例外才是 `no_answer`**。其他狀態碼（500、502、404）是 `server_error`。

fixture 沒有的變體在測試裡從 GET 副本 spread 出來，並在註解寫明來源：非空的 `suppressed`、失敗的 `patrol_status`、`version: 2`、`readonly: true`。

---

## 7. PR 切分（88 接受八張；v4 依預定切點把 5a-3 拆成兩張，共九張，待 88 確認）

估算含測試與兩份語系，以這個 repo 的程式密度為準。每張都能單獨編譯、單獨通過測試。

```
main ──→ WK-5h（獨立，不受閘門，現在就派工）
WK-1a（已 merge）→ 5a-1 → 5a-2 ─┬→ 5a-3a → 5a-3b ─┐
                                └→ 5b-1 ──────────┴→ 5b-2 → 5b-3 → 5b-4
```

- 5b-1 只讀不寫，所以和 5a-3a、5a-3b 可以並行。5b-2 要等 5a-3b 與 5b-1 都進來。
- 5b 各張在 `wake_rules.v1` 出現前不可見，準備好、88 放行就能 merge（spec:226、1f plan:72）。
- 5b-4 和 WK-5h 都改 `WatchSettings.tsx`；WK-5h 先出，5b-4 rebase 上去。
- **v3 的重新分配**：「沒有送出」讓 5a-3 與 5b-2 各多出約 140 行，所以 5b-2～5b-4 的內容重排過。各張都在 b 系列後段，畫面在 `wake_rules.v1` 前看不到，中途不完整沒有關係。
  - 5b-2：數字與 prompt 欄位、衝突條、「沒有送出」條、橫幅的重試、巡檢的間隔／上限／prompt；
  - 5b-3：開關欄位、總開關、固定時刻與時區、wwd 警報整塊；
  - 5b-4：三條規則、巡檢狀態、紀錄、主機分頁瘦身。
- **v4 的大小變化**：
  - 5a-3 加上「同欄的鏈」、`uncertain`、正本切換轉成「沒有送出」與 88 指定的八條測試之後，估到約 780 行，沒有餘量。所以照 v3 預定的切點拆成 5a-3a（佇列、鏈、200／409／400）與 5a-3b（「沒有送出」、重試、`uncertain`、正本切換）。
  - 5a-2 加上「從未取到唯讀」與 `""` 的規則，約 +60 行。
  - 5b-3 加上陣列的衝突條，約 +70 行。
  - 其他張不變。

| PR | 範圍 | 檔案 | 估計 |
|---|---|---|---|
| WK-5h | 主機分頁：閒置時休息開關、`patrol_idle_min` 改標與停用 | 5 | 約 130 行 |
| WK-5a-1 | 契約與純邏輯：API、形狀分類、patch 與走鏈、錯誤對應、時刻／種類運算 | 6 | 約 620 行 |
| WK-5a-2 | 讀取 store：候選即時重選、世代與正本身分、`""` 的規則與從未取到唯讀、文件採用、中繼資料、事件 | 5 | 約 540 行 |
| WK-5a-3a | 寫入 store（一）：每欄的鏈、送出、200／409／400、同欄在途再改 | 2 | 約 430 行 |
| WK-5a-3b | 寫入 store（二）：「沒有送出」、重試、`uncertain`、403、正本切換轉成「沒有送出」 | 2 | 約 400 行 |
| WK-5b-1 | 新分頁註冊、外框、橫幅、讀取生命週期（含 60 秒）、記憶模組 | 19 | 約 700 行 |
| WK-5b-2 | 數字與 prompt 欄位、衝突條、「沒有送出」條與重試、巡檢的間隔／上限／prompt | 9 | 約 700 行 |
| WK-5b-3 | 受控 `Section`、開關欄位、總開關、固定時刻、wwd 警報整塊、陣列的衝突條 | 10 | 約 720 行 |
| WK-5b-4 | 三條規則、巡檢狀態、最近沒叫的紀錄、主機分頁瘦身與連結 | 12 | 約 690 行 |

### WK-5h 主機分頁：閒置時休息（88 已裁定：獨立先出，現在派工）

- **檔案**：`components/watch/WatchSettings.tsx`、`WatchSettings.test.tsx`、`WatchSettings.tab-switch.test.tsx`、`locales/en.json`、`locales/zh-TW.json`（§3.0）。
- **依賴**：無，直接從 main 開；不受任何 capability 閘門。
- **驗收測試**：
  - 打開會寫 `mode: 'patrol'`，關掉會寫 `mode: 'resident'`（整列，其他欄位原樣）。
  - 主機回 `patrol` 時顯示開，回 `resident` 時顯示關，沒寫過的列顯示開。
  - 關掉時分鐘數那一列 `disabled`（不能編輯），值照顯示；寫入時這個值原樣帶回、沒被改掉。
  - 標籤與說明是新字，畫面上沒有「常駐／巡檢」二選一。
  - tab-switch：開關與分鐘數草稿都還在。
- **mutation**：
  1. 開關方向反了；
  2. 關掉時仍可編輯分鐘數；
  3. 關掉時把分鐘數清掉或改掉；
  4. 未寫過的列顯示成關。

### WK-5a-1 契約與純邏輯（沒有 store、沒有畫面）

- **檔案**：
  - 新 `lib/watch/wake-settings.ts`：
    - `WAKE_SETTINGS_CAPABILITY`、`WAKE_RULES_CAPABILITY`、`wakeSettingsDaemons()`、`wakeRulesDaemons()`；
    - 型別；`parseWakeSettingsResponse`（含 `writable`：版本、`readonly`、從未取到三者都會讓它為 false）；`readPath`／`withPath`；
    - `fetchWakeSettings`、`putWakeSettings`：回傳 union `ok | conflict | invalid | forbidden | unreachable | app_mismatch | server_error | no_answer`，不丟例外。
  - 新 `wake-settings.test.ts`。
  - 新 `lib/watch/wake-settings-model.ts`：
    - patch 與鏈的型別；
    - `walkChain(serverValue, links)`（4.4 的走鏈，陣列深度比較）；
    - `collapseChain`（不在途、第一筆不是 `uncertain` 才收）；
    - `fieldIdOfPath`、`errorKeyOf`、`isAppMismatchCode`；
    - 時刻的格式檢查、重複檢查、排序；
    - `kindRows`（已知種類的勾選狀態與 `levelOff`，加上不認得的值）；`toggleKind`（回傳整個新陣列）。
  - 新 `wake-settings-model.test.ts`。
  - `en.json`、`zh-TW.json`（§3.1）。
- **依賴**：WK-1a（已 merge）。
- **驗收測試**：
  - §6 全部；形狀不符的 `it.each`。
  - `walkChain`：
    - 單筆的保留、完成、衝突三種結果；
    - 兩筆鏈依序走（`0→1`、`1→0` 在伺服器為 0、1、5 時各自的結果，見 4.5）；
    - 一筆轉成衝突後，後面的筆併進同一個衝突；
    - 陣列整個比較；
    - 開關不會衝突。
  - `collapseChain`：`{0→1, 1→0}` 收成空；有 `uncertain` 時不收。
  - `fieldIdOfPath` 涵蓋頂層、巢狀情境、陣列索引（`times[0]` 對到整個時刻欄位）。
  - `errorKeyOf` 涵蓋 spec:148 每個代碼，回傳的鍵兩份語系都存在。
  - 時刻加入後已排序，重複與格式錯誤在本地擋下。
  - `kindRows`、`toggleKind` 保留不認得的值；`levels` 全關時 `levelOff` 為真，但不鎖。
- **mutation**：
  1. 形狀不符歸成 `no_answer`；
  2. `walkChain` 拿掉 base 比較（一律保留）；
  3. `walkChain` 每一筆都拿伺服器值比、不拿 `expected` 比；
  4. `collapseChain` 忽略 `uncertain`；
  5. `withPath` 丟掉不認得的欄位；
  6. `max_gap_hours` 的 `out_of_range` 對到情境間隔的字。

### WK-5a-2 讀取 store

- **檔案**：新 `lib/watch/wake-settings-store.ts`（讀取那一半）、新 `wake-settings-store.read.test.ts`、`lib/watch/watch-ws.ts`、`watch-ws.test.ts`、`lib/host-events.ts`。
- **依賴**：5a-1。
- **驗收測試**：
  - 依 `daemonId` 選到正本；`is_source: false` 時跳一次，而且只跳一次（兩台互指不會來回）；`source_id: ""` 不跳；`no_answer` 時換下一個候選；沒有候選時是 `none`。
  - **候選即時重選**：
    - 候選從無到有，立刻讀；
    - 正本候選稍後才出現（`fetchedAt` 從 0 變正），立刻改讀正本；
    - 目前候選失去 capability 或斷線，立刻換到下一個或變成 `none`。
    - 以上都不靠計時器。
  - **從未取到**（`get_stale` fixture）：store 的 `writable` 為 false。
  - **`""` → 真的 id 不算切換**：採用、`sourceEpoch` 加一；沒有 `source_changed`；沒有額外的重讀；帶 `""` 的晚到回應被丟掉。
  - **採用過真的 id 之後，`""` 的回應不採用**（文件與中繼資料都不變），並換下一個候選。
  - **正本切換**：A 的 request 送出 → 另一個回應帶來 B → 採用 B → A 的回應晚到，被丟掉；B 的 revision 比 A 小，照樣採用。
  - 同一來源較小的 revision 不採用；**相等的 revision 不採用**（文件物件不變），但中繼資料照 `seq` 更新。
  - 斷線或 re-point 發生在送出與回應之間時，回應被丟掉。
  - 目前來源送來的 `watch.wake_settings.changed` 會重讀；別台送的不會；`isWatchEvent('watch.events')` 仍為 false。
- **mutation**：
  1. 相等的 revision 也採用；
  2. 正本切換後接受切換前送出的回應；
  3. 拿掉候選訂閱（只靠輪詢）；
  4. 拿掉跳轉；
  5. `isWatchEvent` 改成前綴比對；
  6. 任何一台的事件都重讀；
  7. **`""` → 真的 id 也當成正本切換**；
  8. 從未取到時 `writable` 為 true。

### WK-5a-3a 寫入 store（一）：每欄的鏈、200／409／400

- **檔案**：`wake-settings-store.ts`（加入 `change`、送出、走鏈時機、`conflicts`、`sendMine`、`takeCurrent`、`fieldErrors`）、新 `wake-settings-store.write.test.ts`。
- **依賴**：5a-2。
- **驗收測試**：
  - **A 在途、B 後改、A 收到 409**：
    - B 那欄沒被別人改 → B 續送；被改了 → B 變成衝突，`conflicts` 有一筆，下一個 PUT 的那一欄是伺服器值。
    - A 自己那欄沒被改 → A 自動重疊後續送。
    - 佇列沒有被清空。
  - **同一欄在途時再改**（codex 複審第 1 條）：
    - `0→1` 在途時改回 `0`：200 之後再送 `0`，最後是 `0`；
    - 在途時同一欄改兩次：只送最後一次；
    - 在途收到 409，而伺服器那欄被別人改了：在途那筆與後面那筆都轉成同一個衝突，`mine` 是最後一次的值；
    - 陣列在途時新增又刪除同一個成員：最後送出的陣列不含那個成員。
  - **prompt 在排隊或已輸入，另一欄 409**：prompt 依走鏈處理，草稿不被清掉；伺服器值變了就標成衝突。
  - **GET(新) 先到、PUT(舊) 回應後到**：PUT 200 的 revision 等於已採用的，不重新採用；在途那筆完成；下一個 PUT 疊在 GET 的文件與 etag 上。
  - **反過來的順序**：PUT 200 先採用，晚到的相等 GET 被忽略。
  - **背景 GET 時有在途的欄位不動**：只有沒有在途那筆的欄位被比對。
  - **GET 帶來的是別人的寫入**：之後的 409 依走鏈處理。
  - 任何時刻只有一個 PUT；`expect_etag` 一定是已採用文件的 etag；送出的值是每一欄鏈尾的 value。
  - 400：app_mismatch 那組代碼整個當格式不合，交給 5a-3b 的「沒有送出」，本張先斷言分類正確；其他代碼只丟對到的那幾欄（`times[0]` 對到整個時刻欄位），其餘重送。
  - 看不懂的欄位原樣送回，送出的文件不含 `rules.patrol.enabled`。
- **mutation**：
  1. 拿掉 base 比較（一律續送）；
  2. 409 清空佇列；
  3. 相等的 revision 也採用（寫入這一側）；
  4. **把後續修改折進在途那筆**；
  5. **後續 patch 的 base 用在途那筆的 base**；
  6. 400 時重送失敗的那筆。

### WK-5a-3b 寫入 store（二）：「沒有送出」、重試、正本切換

- **檔案**：`wake-settings-store.ts`（加入 `unsent` 的轉換、`retry(key?)`、`uncertain`、唯讀記憶、正本切換的轉換）、新 `wake-settings-store.unsent.test.ts`。
- **依賴**：5a-3a。
- **驗收測試**：
  - **409 以外的失敗，每種一條**：
    - 503：在途與排隊的轉成「沒有送出」、可重試，在途那幾筆標 `uncertain`；發出一次重讀；沒有第二個 PUT（不自動重送）；
    - `no_answer`：同上；
    - `server_error`：同上，`notice` 帶狀態碼；
    - `app_mismatch`：轉成「沒有送出」但不可重試，`retry` 對它無效；不重讀；
    - 403：所有鏈與衝突全部清空；之後 `change` 與 `retry` 都不發請求。
  - **重試成功後「沒有送出」標記消失**：`retry` → PUT 200 → 沒有 `unsent`。
  - **重試時那一欄已被別人改了 → 轉衝突**：503 之後重讀到別人改的值，按 `retry` 時那筆不送出，進 `conflicts`。
  - 重讀後發現 503 其實已經寫進去（伺服器值等於 value）→ 那筆自動消失。
  - **`uncertain` 的鏈不收**：`0→1`（503、uncertain）、`1→0` → 重試時照樣送出 0；伺服器其實已是 1 時拿到 409，走鏈後再送 0，最後是 0。
  - 「沒有送出」的那一欄又被使用者改 → 接到鏈尾，等於重試那一欄。
  - 上述每一種失敗，prompt 草稿都還在。
  - **從未取到時不能編輯**：`change` 不發請求、不產生任何鏈。
  - **`""` → 真的 id 不清任何狀態**：剛好有讀取中的 GET 也一樣，沒有東西被轉換或丟掉。
  - **A → B 切換時**：待送、在途、衝突的修改全部轉成「沒有送出」，**沒有被丟掉**；A 那邊在途 PUT 的晚到回應被丟掉。
  - **A → B 之後重試**：對 B 的文件走鏈——B 的值等於 base 就送出；不等於就轉衝突。
  - readonly 時 `change` 與 `retry` 都不發請求。
- **mutation**：
  1. **503 改成清掉修改**；
  2. `retry` 不經走鏈直接送；
  3. `uncertain` 的鏈照樣收成一筆；
  4. **A → B 時清掉待送的修改**；
  5. 從未取到時仍接受 `change`。

### WK-5b-1 新分頁、外框、讀取生命週期

- **檔案**（19）：
  - §1.1 的九個程式檔；
  - 測試：`HistoryPage.watch.test.tsx`、`lib/profile/sections.test.ts`、`lib/watch/watch-panes.test.ts`、新 `features/workspace/lib/watch-area.settings.test.ts`；
  - 新 `components/watch/WatchSettingsPane.tsx`（外框、2.2 所有橫幅的文字、mount 時讀取、60 秒輪詢）；
  - 新 `lib/watch/wake-settings-memory.ts`；
  - 新 `WatchSettingsPane.test.tsx`、新 `WatchSettingsPane.tab-switch.test.tsx`；
  - `en.json`、`zh-TW.json`（§3.2）。
- **依賴**：5a-2（只讀）。橫幅上的「重試」按鈕在 5b-2 才加。
- **驗收測試**：
  - reconcile（照 `watch-area.jobs.test.ts`）：
    - **只看 `wake_rules.v1`**：只有 `wake_settings.v1` 時不建立；`fetchedAt` 為 0 也算沒有；
    - 建立位置：jobs 之後；沒有 jobs 就排在 events 之後；再沒有就排在 guardian 之後；一律在主機分頁之前；
    - capability 消失不移除；同步進來的分頁保留。
  - `openWatchArea({ tab: 'settings' })` 會選到它；分頁不存在時退回第一個分頁。
  - **HistoryPage**：settings 紀錄呼叫 `{tab:'settings'}`，不建立分頁。
  - label、icon（在 `ICON_MAP` 裡）、route、`contentMatches`、不是 light、五個 kind 已註冊、`KIND_DECISION` 是 syncs。
  - Pane 的每個狀態都有畫面：none、loading、unreadable、讀不懂、stale（含時間）、stale_never、readonly、裝置唯讀（文字是「這台裝置不能改設定」）、衝突件數、503、沒有回應、主機錯誤、正本換了；`shared` 與 `via` 兩句。
  - **從未取到**（`get_stale` fixture）：橫幅寫「還沒讀到正本（mlab），暫時不能改」，整塊 `disabled`。
  - mount 時讀取；60 秒輪詢；卸載後停止。
  - tab-switch：§5 列給 5b-1 的三案。
- **mutation**：
  1. reconcile 改看 `wake_settings.v1`；
  2. 排在 jobs 之前；
  3. HistoryPage 漏掉 settings 分支；
  4. 卸載不清計時器；
  5. 讀到的資料改放 `useState`；
  6. 從未取到時沒有 `disabled`。

### WK-5b-2 數字與 prompt 欄位、衝突條、「沒有送出」、巡檢的間隔／上限／prompt

- **檔案**（9）：
  - 新 `components/watch/wake/WakeRows.tsx`：
    - `IntRow`（衝突條、「沒有送出」條）；
    - `PromptRow`（草稿規則、衝突條、「沒有送出」條、⌘Enter）；
    - 共用的 `FieldStrip`（衝突與「沒有送出」兩種樣式；後面幾張的欄位都用它）。
  - 新 `components/watch/wake/PatrolBlock.tsx`（五個情境、最長不巡上限、附加 prompt）。
  - `WatchSettingsPane.tsx`（掛上巡檢；橫幅加「重試」全部）。
  - 新 `WakeRows.test.tsx`、新 `PatrolBlock.test.tsx`、`WatchSettingsPane.test.tsx`、`WatchSettingsPane.tab-switch.test.tsx`。
  - `en.json`、`zh-TW.json`（§3.3）。
- **依賴**：5a-3b、5b-1。
- **大小**：約 700 行。
- **驗收測試**：
  - 巡檢區塊：沒有開關；五個情境的標籤正確；0 會送出；非整數在本地擋下；上限框。
  - prompt：多行；失焦與 ⌘Enter 都會送出；400 時保留草稿並顯示錯誤；409 時保留草稿；伺服器值變了就出現衝突條；「採用目前值」會清掉草稿，「送出我的值」會再送一次。
  - 數字框的衝突條：「目前是／你的是」兩個值都對；兩個按鈕各自生效。
  - 同一欄在途時再改：框裡顯示最後一次的值，200 之後送出，最後顯示的是使用者最後打的值（例如 30→45 在途時改回 30，最後是 30）。
  - **「沒有送出」，每種各一條**：
    - 503、沒有回應、主機錯誤：數字框顯示使用者的值、標「沒有送出」、有「重試」與「採用目前值」；橫幅有「重試」；
    - 格式不合：標「沒有送出」，只有「採用目前值」，沒有重試；橫幅說明要更新 App；
    - 403：沒有「沒有送出」標記，整塊唯讀，橫幅寫「這台裝置不能改設定」。
  - **重試成功後標記消失**：按欄位旁的「重試」→ PUT 200 → 標記與按鈕都不見了。橫幅的「重試」全部也一樣。
  - **重試時那一欄已被別人改了 → 轉衝突**：畫面從「沒有送出」條變成衝突條。
  - 每種失敗之後，prompt 的草稿文字都還在框裡。
  - 錯誤落點：用 400 fixture 的 `max_gap_hours` 那一筆（拿掉 `unknown_field` 後 spread 出的變體）驅動，錯誤落到上限框。
  - tab-switch：§5 列給 5b-2 的案子。
- **mutation**：
  1. `PromptRow` 在 400 或 409 時清掉草稿；
  2. 巡檢多畫一個開關；
  3. 0 在本地被擋；
  4. 衝突條的「送出我的值」用錯 base；
  5. 格式不合時仍顯示「重試」；
  6. 重試成功後沒有清掉標記。

### WK-5b-3 開關欄位、總開關、固定時刻、wwd 警報整塊

- **檔案**（10）：
  - `components/hosts/form-fields.tsx`（`Section` 受控 props）；
  - `WakeRows.tsx`：加 `BoolRow`、`TimesEditor`，都接上 `FieldStrip` 的「沒有送出」；
  - `PatrolBlock.tsx`：掛上固定時刻與時區；
  - 新 `components/watch/wake/AlertBlock.tsx`：wwd 開關、prompt、等級、種類。等級與種類都是整個陣列的 patch，接上衝突條與「沒有送出」；
  - `WatchSettingsPane.tsx`：掛上兩個總開關與 wwd 卡；
  - 新 `AlertBlock.test.tsx`、`WakeRows.test.tsx`、`WatchSettingsPane.tab-switch.test.tsx`；
  - `en.json`、`zh-TW.json`（§3.4）。
- **依賴**：5b-2。
- **大小**：約 720 行（v4 加了陣列的衝突條，約 +70 行）。
- **驗收測試**：
  - 兩個總開關：送出整份文件，只差那一個 path；說明裡都提到巡檢。
  - 固定時刻：加入與移除送出時已排序；重複與格式錯誤在本地擋下；`tz` 有顯示。
  - 開關與時刻 chip 的「沒有送出」：503 之後標記出現、可重試；重試成功後消失。
  - **陣列的衝突條**（v4：陣列整個比較）：
    - 時刻被別人改過時出現衝突條，「目前是 09:00、你的是 09:00、21:00」；
    - 等級同理，用等級名稱；
    - 種類用 `field_conflict_list`，不列出值。
    - 「送出我的值」與「採用目前值」各自生效。
  - wwd 卡：
    - 開關、prompt；總開關關著時有提示；預設收合；等級開關把整個 `levels` 寫成新陣列。
    - 種類：取消勾選會加進 `kinds_off`，勾回會拿掉；某種類的 `levels` 全關時有提示、但不鎖；不認得的值照原字列出、可以拿掉，勾或取消別的種類時它們保留。
    - **en 介面顯示 `磁碟空間不足 (disk_low)`，zh-TW 只顯示 `磁碟空間不足`**；不認得的種類兩種語系都照原字。
  - tab-switch：§5 列給 5b-3 的案子。
- **mutation**：
  1. 勾別的種類時丟掉不認得的值；
  2. 依等級鎖住勾選框；
  3. en 不附 kind；
  4. 展開狀態改放 `useState`；
  5. 加入時刻後沒有排序；
  6. 陣列衝突時沒有衝突條。

### WK-5b-4 三條規則、巡檢狀態、紀錄、主機分頁瘦身

- **檔案**（12）：
  - 新 `components/watch/wake/RuleBlocks.tsx`（disk_low、unattended_changed、user_back）；
  - 新 `components/watch/wake/WakeRecords.tsx`；
  - `PatrolBlock.tsx`（巡檢狀態那行）、`WatchSettingsPane.tsx`；
  - `components/watch/WatchSettings.tsx`（`wake_rules.v1` 閘門與連結）；
  - 新 `RuleBlocks.test.tsx`、新 `WakeRecords.test.tsx`、`PatrolBlock.test.tsx`、`WatchSettings.test.tsx`、`WatchSettingsPane.tab-switch.test.tsx`；
  - `en.json`、`zh-TW.json`（§3.5）。
- **依賴**：5b-3；WK-5h（已先出，rebase 上去）。
- **大小**：約 690 行。
- **驗收測試**：
  - 三張規則卡：都有開關、參數、prompt；`user_back` 有「新」；總開關關著時有提示；預設收合；開關與數字的「沒有送出」照 5b-2／5b-3 的規則。
  - 紀錄：每筆有時間、主機、規則名、原因、detail；不認得的 why 與 reason 照原字；空狀態；非正本時有 `records_local`。
  - 巡檢狀態：
    - 成功、從未、失敗三種句子；
    - 四個 `last_fail_code` 都翻譯得到，並附上 `last_fail_detail`；
    - 不認得的代碼只顯示原文。
  - 主機分頁：
    - **daemon 有 `wake_rules.v1`**：沒有 `enabled`／`auto_wake` 兩列；連結會開到設定分頁；寫入時那兩個值原樣帶回。
    - **只有 `wake_settings.v1` 或都沒有**：照 WK-5h 之後的樣子，既有測試不改也照樣綠。
  - tab-switch：§5 列給 5b-4 的案子。
- **mutation**：
  1. 只看到 `wake_settings.v1` 也瘦身；
  2. 有 `wake_rules.v1` 仍顯示舊欄位；
  3. 紀錄不顯示主機；
  4. 失敗代碼不翻譯；
  5. 規則卡的展開狀態改放 `useState`。

---

## 8. 給 member 的共通規則（每張 brief 都附）

- 先讀 `~/.local/share/purdex-lead/briefs/b-common-rules.md`。TDD，每個 task 一個 commit。commit 訊息用英文，結尾 `Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>`。
- **測試**：
  - 開發中：mlab 本機只跑改到的單一測試檔（`cd spa && npx vitest run <檔>` 或 `pdx test spa-file <檔>`），以及 `npx eslint <檔>`。
  - PR 層：併入最新 `origin/main` 並 commit 之後，在 spa 下執行：
    - `pdx test spa-related <改到的 spa/src 檔…> <git ls-files 'src/**/*guard*.test.ts*' 的全部> src/main.test.tsx src/locales/locale-completeness.test.ts`
    - `pdx test tsc`
    - 本機 `npx eslint <改到的檔>`
  - 回報 job id，88 會核對 `.snapshot.tree` 等於 PR head 的 tree、`.lane` 不是 true、rc 為 0。
  - 不准在 mlab 跑 tsc、多檔 vitest、`vitest related`、`pdx test --adhoc`。
- mutation 只對新增的測試，一次一條，前後 `git status` 都要乾淨。
- codex：R1，再加 R2 的攻擊方與 critic（`--model gpt-5.6-sol`，effort 不拉高）。送之前拿 B 線 codex 鎖，job id 一拿到就寫進 PR 說明。prompt 附「不跑完整測試」「不要對上層目錄 find／rg」兩句。攻擊方的 focus：
  - 5a-2：世代與正本身分、候選訂閱；
  - 5a-3a：同一欄的鏈（在途再改、改回原值）、走鏈順序、回應先後順序；
  - 5a-3b：「沒有送出」會不會被悄悄丟掉或自動重送、`uncertain`、正本切換；
  - 5b-1：reconcile 的閘門與排序、計時器；
  - 5b-2：草稿、衝突條、重試；
  - 5b-3：不認得的種類能不能保住；
  - 5b-4、WK-5h：主機分頁的閘門與整列帶回。
- 不起 dev server，截圖寫「由 lead 補」。只能 kill 自己的 pid，不用 `git stash`。疊加 PR 不用 `--delete-branch`。88 放行之前不 merge。

---

## 9. 出版

- 閘門已寫死（spec:107-109、:226；1f plan:45、:72）：
  - 資料讀寫的候選看 `watch.wake_settings.v1`；
  - 分頁建立與主機分頁瘦身**只看** `watch.wake_rules.v1`。
- WK-5a、WK-5b 各張可以各自 merge，在 `wake_rules.v1` 出現前畫面不可見。宣告它的那張（WK-2、WK-3 裡最後 merge 的）所在那一版，新畫面才出現，那一版打包 Mac App。
- **WK-5h 不受閘門**，任何一版都可以出。它只是同一個欄位換一種呈現，現有 daemon 就支援。
- 帶 `wake_rules.v1` 的那一版發布時：照慣例打包，並請使用者更新 Mac App。在他更新之前，舊 App 主機分頁的 `enabled`／`auto_wake` 開關已經不起作用（spec:152）。release 說明要寫這一句。部署檢查加一條：mlab 與 air26 的 `/api/info` 都有 `watch.wake_rules.v1`。

---

## 10. 跟 spec v5.1／1f plan 還對不上的地方

v5.1 已解決上一版列的這些：守門測試、capability 分兩個、檔案清單、`known_kinds.levels`、`fetched_at: null`、`last_fail_code`、事件 value、403 body。剩下的：

1. **fixture 的數目**：spec:150 列六個，main 上實際有八個（多了 `get_stale` 和 `put_400_missing_expect`）。spec 的文字落後，不影響實作。
2. **`source_id: ""` 沒寫進 spec**：spec:90、:119 說 `source_id` 是正本的 host_id，但從未取到時 fixture 給的是 `""`。App 把 `""` 當成「未知身分」，不拿去比對 `daemonId`，之後採用到真正的 id 時視為正本切換（4.3）。建議 spec 補一句。
3. **why 值域散在兩處**：spec:131 列五個，`patrol_failed` 在 :134。App 六個都翻譯。另外 :176 寫「記進 `patrol_status`（`patrol_failed`）」，但 `patrol_status` 用的是 `last_fail_code` 的四個代碼，用詞不一致。
4. **1f plan 自相矛盾**：:10 的共通規則仍寫「SPA 只動 fixture 副本時跑 `…lib/pool/fixtures-sync.guard.test.ts`」，:23 已改成 `lib/watch` 那支。
5. **有幾種形狀沒有 fixture**：非空的 `suppressed`、失敗的 `patrol_status`、`version: 2`、`readonly: true`（§1.6）。App 測試只能自己 spread，這些形狀沒有跟 Go 端對過。建議 WK-1b 或 WK-3 補一個 `…_get_records.json`（有紀錄、巡檢失敗）。
6. **主機分頁的「閒置時休息」**：spec:17 說不改 `mode`，App 也沒改值，只是換了呈現方式，所以不衝突；但 spec:201 的 App 描述沒提到。建議 1f 在 §7 補一句「主機分頁的 `mode` 以『閒置時休息』開關呈現（開＝patrol）」，避免之後有人以為 `mode` 跟巡檢有關。
7. **實作提醒**（不是 spec 的錯）：
   - pool 的 `classify` 會把 `device_forbidden` 歸成 `unknown`（`lib/pool/errors.ts:31-35`）；
   - `Section` 不能記住展開狀態（`components/hosts/form-fields.tsx:6-7`）；
   - `suppressed` 與 `patrol_status` 沒有更新事件，App 只能輪詢（spec:102）。

---

## 11. 88 的裁定

**唯一待確認**：v4 依預定切點把 5a-3 拆成 5a-3a、5a-3b，總數從八張變九張。原因是同欄的鏈、`uncertain`、正本切換轉「沒有送出」，加上指定的八條測試，讓 5a-3 估到約 780 行。如果要維持八張，5a-3 就會貼著 800 行的上限。

### v3 寫入的裁定

1. **接受切成八張**（§7；v4 變九張，見上）。
2. **409 以外的寫入失敗，原則是「使用者的改動不能被悄悄丟掉」**（4.4、2.2、2.3）：
   - 503、`no_answer`、`server_error`：待送的數字、開關、清單類修改**不清掉**，轉成看得到的「沒有送出」，附「重試」；重試走同一條三方比對路徑；同時重讀一次；不自動重送。
   - 403（`device_forbidden`）：整塊轉唯讀，待送的修改丟掉，橫幅說明「這台裝置不能改設定」。
   - `app_mismatch`：修改轉成「沒有送出」，不給重試；橫幅說明 App 與 daemon 版本不合、要更新。
   - prompt 草稿一律保留。
   - 測試每種各一條，加上「重試成功後標記消失」「重試時那一欄已被別人改 → 轉衝突」。mutation 有「503 改成清掉修改」。放在 5a-3（v4 起是 5a-3b，store）與 5b-2（畫面）。
3. **WK-5h 獨立先出，現在就派工，不受閘門限制**。

### v4 寫入的裁定（codex 增量複審 `task-mvia45y9-0ockz6`）

4. **同一欄在途時再改**（4.4、4.5）：
   - 不折進在途那筆，另起一筆排隊，`base` 是在途那筆的 `value`；`value === base` 才刪，只跟自己的 base 比。
   - 依序處理：200 → 下一筆照常送；409 → 先比在途那筆，再依序比後面的。
   - 陣列整個當一個值比較，不做逐成員合併。
5. **正本身分**（4.3）：
   - 從未取到時整塊唯讀；
   - `""` → 真的 id 不算切換；
   - 真的 A → B 時，待送、在途、衝突的修改轉成「沒有送出」，不丟；重試時對 B 的文件比對；prompt 草稿保留。

### 照裁定寫死、不再另列的推定

- 閘門；en 的種類名稱。
- `patrol_idle_min` 在開關關著時用 `disabled`。
- `source_id: ""` 視為未知身分；已經採用過真的 id 之後，再收到 `""` 的回應就不採用，並換下一個候選。
- 60 秒輪詢放在 pane。
- `app_mismatch` 不重讀。
- 開關、時刻 chip、等級、種類的「沒有送出」與衝突畫面放在 5b-3／5b-4，共用 5b-2 的 `FieldStrip`。
- 在途時失敗的那一筆標 `uncertain`：它的鏈不收成一筆，直到看過晚於這次失敗的文件。這條是我推出來的，用來避免「503 其實寫進去了」時把使用者最後一次修改收掉。codex 複查時請一併看。

---

## 12. codex 審查對照（`task-mvi6ky1o-jpn4y4`）

| # | 摘要 | 處理 |
|---|---|---|
| 1 critical | 409 誤刪在途期間的後續修改 | 改成逐欄三方比對、409 不清空（4.4、4.5）；5a-3 測 A 在途／B 後改的兩種情況與 A 自動重疊；mutation：拿掉 base 比較、409 清空佇列 |
| 2 | prompt 草稿的 409 規則矛盾 | prompt 草稿一律保留、伺服器值變了就標記（2.3）；寫明與 `WatchSettings.tsx:52-58`、`WatchSettings.test.tsx:144-160` 的差異；5a-3、5b-2 都有測試 |
| 3 | 背景 GET、PUT、新修改三方交錯 | `(source_id, revision)` 嚴格較大、`sent` 在結果回來時以最新文件比對、中繼資料依 `seq`（4.3、4.4）；5a-3 測兩種先後順序與相等 revision；mutation：相等 revision 也採用 |
| 4 | 正本切換的晚到回應 | request 記下 `sourceEpoch`，切換前送出的回應一律丟掉（4.3）；5a-2 測 A → 切 B → A 晚到，以及 B 的 revision 較小仍採用 |
| 5 | en 的警報種類 | `watch.wake.kind_name`：en＝label (kind)、zh＝label（2.7）；5b-3 測已知與未知種類；原 §11 那一項刪除 |
| 6 | 漏了 `HistoryPage.watch.test.tsx` | 列進 5b-1，測 `{tab:'settings'}` 且不建立分頁 |
| 7 | tab-state 少三案 | §5：切走期間 store 更新（5b-1）、正本換了（5b-2；v4 改成草稿保留、修改轉成「沒有送出」）、卸載期間沒有計時器（5b-1） |
| 8 | 候選沒有即時重選 | 4.1 訂閱兩個 store、比對候選簽章；5a-2 測三種情況；mutation：拿掉訂閱 |
| 9 | 形狀錯誤被報成無回應 | 形狀不符一律 `app_mismatch`，只有 fetch 丟例外才是 `no_answer`，其他狀態碼是 `server_error`（§6）；mutation：形狀不符歸成 `no_answer` |
| 10 | 5b-2 沒有餘量 | wwd 整塊移到 5b-3；v3 為了放「沒有送出」再重排，5b-2 只放數字與 prompt 欄位、衝突與「沒有送出」條、巡檢的間隔／上限／prompt，重估後切成八張（§7） |
| 11 | 閘門文字未定 | 寫死兩個 capability 的分工（§0、4.1、§9、5b-1、5b-4）；原 §9、§11 的三個方案刪除 |

### codex 增量複審對照（`task-mvia45y9-0ockz6`，v3 → v4）

| # | 摘要 | 處理 |
|---|---|---|
| 1 critical | 同一欄在途時改回原值，最後一次修改會遺失（v3 的 4.4 會把它跟原始 base 比、刪掉） | 每欄一條依序的鏈，在途時再改另起一筆，base＝在途的 value；陣列整個比較；409 先比在途再依序比（4.4、4.5）。5a-3a 測四個序列；mutation：折進在途那筆、後續 base 用在途的 base |
| 2 critical | `""` 第一次變成真的 id 被當成正本切換，修改全丟 | 從未取到時整塊唯讀，不會有待送的東西；`""` → 真的 id 不算切換；真的 A → B 時修改轉成「沒有送出」，不丟（4.3）。5a-2、5a-3b 測試；mutation：`""` → 真的 id 也當切換、A → B 時清掉修改 |
