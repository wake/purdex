# 監控分頁組＋側欄主機列 — plan

> spec：`docs/specs/2026-10-20-monitoring-tabs-spec.md`（下文的 M、A、W、P 編號都是 spec 的）。B 線 lead 88，2026-10-20。
> 一期一張 PR（≤ 800 行、≤ 20 檔，測試也算）；一個 task 一個 commit，先寫測試、看它紅，再實作。執行者：Sonnet member。
> 第一波（MON-1a～MON-4）細到 task；第二波（MON-5～MON-12）只寫到期，**開工前 plan 再細化一版**（含 file:line 與測試案例），細化版先給 88 看。
> spec 標「⚠ 待 88 確認」的 W1–W9，本 plan 照 spec 括號裡的建議寫；88 改了就照改，不另開輪。
> 行號以 `origin/main abaa46be5` 為準，開工時以當下的 `origin/main` 重新核對。

## 共通規則

- **流程與規矩**照 `~/.local/share/purdex-lead/briefs/b-common-rules.md`（2026-10-20 版）：worktree、TDD、每 task 一個 commit、i18n 兩邊、Phosphor、只用 theme token、不起 dev server（截圖由 lead 補）、只 kill 自己的 pid、不用 `git stash`、lead 放行才 merge、疊加 PR 不用 `--delete-branch`。
- **測試分層**：
  - 開發中：只跑改到的單一測試檔（`cd spa && npx vitest run <檔>`，經閘門）。
  - PR 層（併入最新 `origin/main` 之後的 head，送 air26 的 `assist-run.sh`）：`npx vitest related --run <改到的 spa/src 檔>`、守門測試（`git ls-files 'src/**/*guard*.test.ts*'`＋`src/main.test.tsx`＋`src/locales/locale-completeness.test.ts`）、`npx tsc --noEmit -p tsconfig.app.json`、`npx tsc --noEmit -p tsconfig.node.json`、`npx eslint <改到的檔>`。先在 mlab 跑單檔 vitest＋eslint、`tsc -p tsconfig.app.json` 單獨送 mserv，最終 head 才送 air26 一次。
  - **不跑完整 vitest**，不跑 `go test ./...`（本線只有 SPA）。
- **tab-hosted**：主機頁、任務佇列、事件等分頁裡要跨分頁保住的 state（子分頁、範圍、篩選、捲動、選取、草稿）放在元件外（`lib/watch/view-memory.ts`、`lib/pool/jobs-view-memory.ts` 那一類），每個新的分頁元件用真的 `TabContent` 寫「切走再切回」的測試（範例 `components/execution/WorkerDeckPane.tab-switch.test.tsx`、`components/watch/jobs/JobsPane.tab-switch.test.tsx`）。側欄不是 tab-hosted。
- **不新增 fleet 讀者**（A1、A11）：MON-3b 加守門測試，之後每期照著擴充允許清單（每一項寫理由與在哪一期拿掉）。
- **新欄位一律可選**：`PoolHost`、`PoolJob` 的新欄位用 `?:`（像 `PoolFacts.queue`），缺席＝不知道；不要改成必填（16 個測試檔用字面值建 `PoolJob`）。
- **mutation**：只針對新增的測試，一次一條，前後 `git status` 都乾淨。
- **codex**：R1 → R2（攻擊 → critic，critic 一定是 codex，附 R1＋攻擊方 findings 與 spec／plan 路徑）；`--model gpt-5.6-sol`、effort 維持 low；一次只跑一條（`b-codex-lock.sh`）；job id 一拿到就寫進 PR 說明；prompt 附「不要執行完整測試或建置（整套 vitest 等），測試結果以 Claude 側附上的為準」「不要對上層目錄 find／rg」。
- **拆 PR**：每期列了 **Split** 線；估計會超過就在那裡切，先回報 88 再拆。

## 查證過的 wire 欄位（`git grep` 於 `internal/`）

| 用途 | 欄位 | Go 來源 |
|---|---|---|
| 存活 | `liveness`（`ok`｜`wwd_down`｜`host_down`｜`unknown`）、`liveness_at`、`liveness_since`，三個都 omitempty；**本機那一列沒有**（`self` 不帶） | `internal/wwd/wire/api.go:66-80`、`:323-326`；`internal/module/pool/module.go:273-274`（`PoolWithLiveness`，`:425`）；`internal/wwd/daemon/pool.go:275-290` |
| 往返時間 | `rtt_ms`（omitempty；本機、沒回應、舊 wwd 為 null） | `internal/wwd/wire/api.go:320-321` |
| 重／輕 | job 的 `slot`（`"heavy"`｜`"light"`），Purdex 自己加在 `PoolJob` 上 | `internal/module/jobs/pooljob.go:8-11`；`internal/wwd/wire/usage.go:24-36` |
| 開始執行 | job 的 `started_at`（omitempty） | `internal/wwd/wire/jobs.go:128` |
| daemon 對應 | `/api/info` 的 `wwd {host_id, alias}`；能力 `pool.identity.v1`，只在知道時宣告，鍵缺席不是空字串 | `internal/core/info_handler.go:106-118`、`:238-257`；`internal/module/pool/module.go:216-223` |
| wwd 的 host_id | 16 位 hex（資料夾裡的 `host_id` 檔） | `cmd/wwd/main.go:341-357` |
| Purdex daemon 的 host_id | 帶 `:`（`PeerAlias` 取冒號前） | `internal/config/config.go:52`、`:263-272` |
| `liveness_at` 不觸發 `pool.changed` | 比對前清掉 `LivenessAt` | `internal/module/pool/events.go:73` |
| 對等體輪詢間隔 | 5 秒，讀不到時每輪看 22 埠 | `internal/wwd/daemon/pool.go:18`、`:103-121` |

## 第一波

### MON-1a — `slot`、`/api/info.wwd`、pool 主機 → daemon

檔案（16 個、約 520 行）：`spa/src/lib/pool/jobs-types.ts`、`jobs-parse.ts`、`jobs-parse.test.ts`、`spa/src/stores/useHostStore.ts`、`spa/src/lib/nex/nex-host-reducer.ts`、`nex-host-effects.ts`、`spa/src/stores/useNexHostStore.test.ts`、新檔 `spa/src/lib/pool/daemons.ts`＋測試、`lib/pool/use-cell-models.ts`、`cell-model.ts`、`cell-model.test.ts`、`components/watch/WatchHostPane.tsx`、`lib/pool/use-pool-host-rows.ts`、`lib/pool/jobs-driver-live.ts`、`jobs-driver-live.test.ts`。

1. **`job.slot`**（MD-4）
   - 測試先行（`jobs-parse.test.ts`）：`slot: "light"` → `'light'`；`"heavy"` → `'heavy'`；缺席、`"medium"`、數字 → 欄位缺席；現有 fixture（`__fixtures__/job_done.json` 等，沒有 slot）照樣解得開、其他欄位不變。
   - 實作：`jobs-types.ts:30-55` `PoolJob` 加 `slot?: 'heavy' | 'light'`；`jobs-parse.ts:67-83` 只在合法時帶上。
   - mutation：不認得的值當 `'heavy'`；漏掉這欄。
2. **`/api/info.wwd` 進 Nex 快取**（MD-1）
   - 測試先行（`useNexHostStore.test.ts`，它驅動 effects）：`/api/info` 帶 `wwd: {host_id: "82b11c521df16c12", alias: "mlab"}` → entry 的 `wwd` 是 `{hostId, alias}`；缺鍵 → `null`；空字串或非字串 → `null`；generation／fingerprint 不符的過期答案不寫入（沿用現行機制，加一案）。
   - 實作：`useHostStore.ts:106-115` `HostInfo` 加 `wwd?: unknown`；`nex-host-reducer.ts:16-30` `NexHostEntry`、`Loaded` 加 `wwd?: { hostId: string; alias: string } | null`；`nex-host-effects.ts:47-59` `load()` 嚴格解析。
   - mutation：接受空的 `host_id`。
3. **pool 主機 → daemon 的對應**（A1）
   - 新檔 `lib/pool/daemons.ts`：純函式 `poolHostDaemons({ hostOrder, runtime, byHost }): Record<string, string>`（wwd `host_id` → HostConfig id）：依主機順序、連著、Nex entry `fetchedAt > 0`、`wwd.hostId` 有值；同一個 `host_id` 先到先得。hook `usePoolHostDaemons()`；非 React 用的 `readPoolHostDaemons()`。`refreshPoolIdentities()`：連著、宣告 `pool.v1`、entry 卻沒有 `wwd` 的 HostConfig 呼叫 `useNexHostStore.getState().ensure(id)`（受 60 秒 TTL 限制，所以 daemon 晚一點才知道自己的 wwd 時，最慢一分鐘內補上）。
   - `useRunPool`（`use-cell-models.ts:15-20`）掛著時訂閱 pool 的 `hosts`，變了就 `refreshPoolIdentities()`。
   - 測試：兩台 daemon；斷線的不算；`fetchedAt` 0 的不算；同一個 `host_id` 取主機順序在前的；refresh 只對缺 `wwd` 的呼叫 `ensure`。
   - mutation：拿掉「連著」的條件；重複時取後者。
4. **`matchPoolHost` 改用對應**
   - 測試先行（`cell-model.test.ts`）：alias 相同照樣對到；daemon 主機的 `hostConfigId` 是那台 pool 主機的 daemon 時，alias 不同也對到；舊的 `host_id === v.id`／`daemonId` 不再對到（Purdex 的 host id 不會等於 wwd 的）。
   - 實作：`cell-model.ts:37-39` 加參數 `daemons`；三個呼叫者 `use-cell-models.ts:44`、`WatchHostPane.tsx:114`、`use-pool-host-rows.ts:67` 傳 `usePoolHostDaemons()`。
   - mutation：拿掉 daemons 那一支。
5. **jobs 的 `ownDaemon`**（A1 的發現）
   - 測試先行（`jobs-driver-live.test.ts`）：pool 主機的 wwd `host_id` 等於某台連著、宣告 `jobs.v1` 的 daemon 的 `/api/info.wwd.host_id` → `ownDaemon[alias]` 是它；HostConfig 的 `daemonId` 剛好等於 wwd 的 `host_id` 但沒有 wwd 身分 → 不算（舊規則）；沒有 `jobs.v1` 的 daemon 不算。
   - 實作：`jobs-driver-live.ts:22-32` 改用 `readPoolHostDaemons()`。
   - 行為改變（寫進 PR 說明）：現在 `ownDaemon` 永遠是空的，每個 origin 都交給主機順序第一台 daemon 加 `?host=` 讀；那台自己的 origin 由本機回答（`internal/wwd/daemon/forward.go:41-45`），**另一台有 daemon 的主機（例如 air26）送出的工作卻是轉送到對等體拿到的視角，看不到它的快速通道與未宣告的工作**。改完後 mlab、air26 各向自己的 daemon 讀；換來源的 origin 由 store 從 0 重讀一次（cursor 是來源自己的，`jobs-store.ts:5-6`）。
   - mutation：改回 `daemonId` 比對。
- **Split**：task 5 獨立一張。

### MON-1b — `liveness` 解析、新的主機狀態模型

可與 MON-1a 並行（不碰同一檔）。檔案（11 個、470 行）：`lib/pool/types.ts`、`parse.ts`、`parse.test.ts`、新檔 `lib/pool/host-state.ts`＋測試、`lib/pool/store.ts`、`store.test.ts`、`lib/pool/words.ts`、`words.test.ts`、`locales/zh-TW.json`、`en.json`。

1. **解析**
   - 測試先行（`parse.test.ts`，用 765 快照的真資料列寫成字面值：mlab 沒有這三欄；air26 `liveness: "ok"`＋`liveness_at`）：`wwd_down`、`host_down`、`unknown` 原樣；不認得的字 → `'unknown'`；時間壞掉 → 那一欄缺席；`rtt_ms` 是非負數才帶。
   - 實作：`types.ts:53-66` `PoolHost` 加 `liveness?: PoolLiveness`、`liveness_at?: number`、`liveness_since?: number`、`rtt_ms?: number`；`parse.ts:138-157` `host()`，寫法照 `zoneOf`（`:21`）。
   - mutation：不認得的字當 `'ok'`。
2. **主機狀態模型**（M21，W3）
   - 新檔 `host-state.ts`：`poolHostState(pool: PoolHost, lastFactsAt: number | null, now: number): PoolHostState`，
     `{ state: 'ok'; hostId; alias; pool; facts } | { state: 'wwd_down' | 'host_down' | 'unknown'; hostId; alias; pool; lastFactsAt; checkedAt; since; stalled }`；
     另給 `hostIsOk(pool, now)`。不收 fleet 的輸入。`LIVE_MS` 30 秒從 `host-view.ts:8` 搬過來（`host-view.ts` 先留著，MON-1c 刪）。
   - 測試（表格）：對等體 `ok`＋facts 30 秒內 → ok；`ok`＋facts 超過 30 秒或缺 → `unknown`、`stalled: true`；`wwd_down`／`host_down`／`unknown` 原樣，帶 `checkedAt`（`liveness_at`）、`since`；沒有 `liveness` 欄、`reachable`＋新鮮 → ok；沒有欄＋不新鮮或 `reachable: false` → `unknown`；`lastFactsAt` 原樣傳出。
   - mutation：30 秒邊界（`<=` 改 `<`）；`stalled` 永遠 false。
3. **store**
   - 測試先行（`store.test.ts`）：每次讀到時，`reachable` 且有 facts 的主機記下 `lastFactsAt[host_id] = facts.at`（以 `host_id` 為 key，讀不到時保留）；事件模式下四台都 ok → 讀完不排計時器（現行）；有一台不是 ok → 5 秒後再讀；那台回到 ok → 下一次讀完計時器停。
   - 實作：`store.ts:23-31` 加 `lastFactsAt`（`lastLiveAt` 先留著，MON-1c 刪）；`:91-98` `applyHosts` 記錄；`:83-86` `eventDriven()` 加上「每台都 `hostIsOk`」（M20）。
   - mutation：拿掉「每台都 ok」的條件。
4. **狀態的字**（M17–M21）
   - `words.ts` 加 `livenessWord(t, state)` 與 `livenessLines(t, s, now)`（卡片的兩三行：說明、最後一筆讀數與多久前、最後檢查與「每 5 秒」；`stalled` 寫「wwd 有回應，但讀數停在 hh:mm:ss」；本機寫「本機的 wwd」）。i18n 鍵放 `pool.liveness.*`，兩個語系都加。
   - 測試：每種狀態的字與行；時間格式；`stalled`。

### MON-1c — 讀者全部換到新模型，拿掉 fallback

依賴 MON-1a、MON-1b。檔案（20 個、460 行，多數是修改與刪除）：`lib/pool/cell-model.ts`＋測試、`use-cell-models.ts`、`components/fleet/HostCell.tsx`、`HostCell.pool.test.tsx`、`HostCard.tsx`、`HostCard.test.tsx`、`FleetCells.pool.test.tsx`、`components/watch/AvailRow.tsx`＋測試、`WatchHostPane.tsx`、`lib/pool/use-pool-host-rows.ts`、`components/hosts/pool-status-word.ts`、`PoolHosts.test.tsx`、`PoolHostPage.test.tsx`、刪 `lib/pool/host-view.ts`＋測試、`lib/pool/words.ts`＋測試、`lib/pool/store.ts`。

1. **主機格的模型**：`cell-model.ts:11-29` `PoolCellExtra.state` 改成 `PoolLiveness`，`reason`／`since` 換成 `checkedAt`、`lastFactsAt`、`stalled`；`poolCellModel`（`:59-76`）吃 `PoolHostState`，非 ok 一律回「沒有讀數」。`use-cell-models.ts:31-49` 不再讀 `useFleetStore` 的 `stale`，用 `poolHostState(pool, lastFactsAt[pool.host_id], now)`；fleet 有、pool 沒列的主機直接給 `unknown`（不經 `poolHostState`）。測試改寫。
2. **側欄的兩個元件**（暫時的樣子，真正的圖示在 MON-3b）：`HostCell.tsx:155-158` 刪掉 fallback 的斷線角標，`:79` 的無障礙名稱改用 `livenessWord`；非 ok 的主機照現在「離線」的畫法（徽章 40%、只有軌道）。`HostCard.tsx:168-170` 的 fallback 行換成 `livenessLines`。`HostCell.pool.test.tsx`、`HostCard.test.tsx`、`FleetCells.pool.test.tsx` 裡 fallback 的案例改成三種狀態。
3. **主機分頁與 Hosts 頁**：`AvailRow.tsx:24-27`、`WatchHostPane.tsx:115`、`:142`（`'live'` → `'ok'`）、`use-pool-host-rows.ts:40-72`（燈：ok 綠、`wwd_down` 黃、`host_down` 與 `unknown` 灰；讀數只在 ok 時有，不再讀 fleet 的 `fast`）、`pool-status-word.ts:7-8`。測試 `AvailRow.test.tsx`、`PoolHosts.test.tsx`、`PoolHostPage.test.tsx`。
4. **刪除**：`host-view.ts` 與測試、`FallbackReason`、`fallbackWord`（`words.ts:59`）、store 的 `lastLiveAt`。fallback 的語系鍵先不刪（死鍵不影響 locale-completeness），MON-12 一起清，本期不動語系檔。
- **Split**：超過 20 檔就把 task 3 拆成 MON-1d。

### MON-2a — 外觀與順序 store（Profile Sync）

可與 MON-1a、MON-1b 並行。照 TG-1a-1 的清單（`git show 7fe823a27`、`5e74a7617`）。檔案（15 個、600 行）。

1. **store**（A3、W1）
   - 新檔 `spa/src/stores/usePoolHostsStore.ts`，`lib/storage/keys.ts:52` 之後加 `POOL_HOSTS: 'purdex-pool-hosts'`。狀態 `{ order: string[]; looks: Record<string, PoolHostLook> }`（spec §4 A3；外觀欄位照 W1 的建議與 `HostLookEntry` 相同）。
   - `merge` 一律清洗：`host_id` 合 `^[A-Za-z0-9_-]{1,64}$`、`__proto__` 丟掉；最多 32 台；`order` 去重、只留合法 id；外觀欄位用 `sanitizeHostLookEntry`（`stores/useHostLookStore.ts:59-68`），丟掉 `name`；`indicator.shape` 在三種之內，`data` 1～2 項且都認得（環形、單柱只留第一項；雙柱缺第二項補 `'pressure'`）。
   - 動作：`setOrder`、`patchLook(hostId, fn)`（回 `undefined` 刪掉）、`putLooksIfAbsent`、`setIndicatorForAll(hostIds, indicator)`。persist version 1，`partialize` 只存 `order`、`looks`。`syncManager.register`。
   - 測試：清洗的表格；各動作；另一個視窗寫入後 rehydrate 也經過清洗。
   - mutation：拿掉 32 台的上限；接受不認得的資料項。
2. **settings 的接線**
   - `lib/profile/types.ts:35` 之後加 `'purdex-pool-hosts'`；`projections.ts:98` 之後加 `...settingsPaths('purdex-pool-hosts', ['order', 'looks'])` 與註解；`SECTION_SCHEMA_ORDINAL.settings`（`:149`）12 → 13，註解加「13: purdex-pool-hosts.{order,looks} (monitoring tabs spec A3, MON-2a) — a projection change …；an ordinal-12 payload has no such store: applying it CLEARS the store」；`collector.ts:121`、`apply-to-stores.ts:132` 的 store 對照加一筆；`applier.ts:376` `CLEARED_WHEN_ABSENT` 加入。若 merge 時 `origin/main` 已到 13，順延並同步改註解與測試。
   - **不做** host id 換算：不碰 `sections.ts:390-396`、`applier.ts:109-129`（`settingsFromWire`）、`host-identity.ts`、`host-reresolve.ts`、`host-lifecycle.ts`，也不加進 `host-lifecycle.test.ts:632` 的 `PERSISTED`——key 是 wwd 的 `host_id`，每台裝置都一樣。
   - 既有測試跟著改：`applier.test.ts:1129`（13 → 14）、`apply-to-stores.test.ts:527`（13 → 14）、`collector.test.ts:60-70`（store 與 key 清單）、`projections.test.ts:281`（13 → 14）與 `:331-344` 的 store 對照、`executor.direction.integration.test.ts:739`（12 → 13 與註解）。
   - 新測試 `lib/profile/apply-to-stores.pool-hosts.test.ts`：`hash(build(apply(p))) === hash(p)`；ordinal 12 的 payload（沒有這個 store）套用後本機清空；壞的條目套用時被清洗；key 原樣經過（長得像本機 host id 的也不換）。
   - mutation：從 `CLEARED_WHEN_ABSENT` 拿掉 → 清空的測試轉紅。
3. **收斂測試**（`lib/profile/pool-hosts.integration.test.ts`，寫法照 `tabs-local-only.integration.test.ts`）：裝置 A 改 mserv 的外觀與指標 → 經真的 collector 與 applier 到裝置 B；B 改順序 → 回到 A；兩台內容相同時 build 出同一個 hash、不會互推；較舊 ordinal 的 payload 只落在乾淨的本機 settings 區（executor 現行規則，一案）。
4. **iOS 查證**（不寫程式，寫進 PR 說明）：purdex-ios 只單向讀 settings（qr-pairing spec R4）並套它自己列的外觀鍵。到 GitHub 上 purdex-ios 的 `main` 確認它遇到不認得的 settings ordinal 不會拒絕、會略過不認得的 store。**待查**；若它卡 ordinal，停下回報 88。
- 發版注意（寫進 PR 說明）：settings 的 schema 鎖是整個 profile 的，還在舊版的裝置會鎖 settings 直到升級（每次升 ordinal 都一樣）。
- **Split**：task 3 獨立一張。

### MON-2b — 外觀解析、排序、一次性搬遷

依賴 MON-1a（daemon 對應）、MON-2a。可與 MON-1c、MON-3a 並行。檔案（約 7 個、450 行）：新檔 `lib/pool/host-looks.ts`＋測試、新檔 `lib/pool/pool-look-migration.ts`＋測試、`lib/storage/keys.ts`、`spa/src/main.tsx`（`main.test.tsx` 若有對 boot 的斷言一起改）。

1. **解析與寫入**（A3、M9、M13、W7）
   - `usePoolHostLook(hostId)`：有 daemon（`usePoolHostDaemons`）→ `useHostLookResolver()(hostConfigId)` 的 `colors`／`color`／`icon`／`iconWeight`（`lib/host-look.ts`；不直接讀 HostConfig 的外觀欄位，`host-look.guard.test.ts` 管著）；沒有 → 新 store。
   - `indicatorOf(look)`：store 的 `indicator`，沒有時是預設：由 `useUISettingsStore.fleetCellIndicator` 換算（`ring` → 環形＋`load`；`bars-pressure` → 雙柱 `cpu`＋`pressure`；`bars-memory` → 雙柱 `cpu`＋`mem`）。
   - `orderPoolHosts(hosts, order)`：純函式，M13。
   - `writePoolHostLook(hostId, patch)`：顏色、圖示 → 有 daemon 的寫 `useHostLookStore.patchLook(lookKeyOf(host, looks), …)`（Hosts 頁現行的寫法），沒有的寫新 store；指標一律寫新 store。
   - 測試：daemon 主機讀與寫都走 `useHostLookStore`、分頁徽章同一份；沒有 daemon 的走新 store；預設指標的三種換算；排序的 M13 例子；`order` 有、pool 沒列的不出現。
   - mutation：daemon 判斷反過來；排序把沒排到的放前面。
2. **一次性搬遷**（A3、W2）
   - `startPoolLookMigration()`：在 `main.tsx:107` `startWatchArea()` 之後啟動，訂閱 pool store 與 fleet store。這台裝置的 marker `purdex-pool-host-looks-migrated`（JSON：搬過的 `host_id` 清單與 `order` 是否寫過；不投影、不註冊 `syncManager`；讀寫包 try/catch，照 `lib/host-look-migration.ts:38-52`）。
   - 每台：沒有 daemon、新 store 沒有它的外觀、不在 marker 裡、fleet 快照不 stale 且有同 alias 的主機帶外觀 → `putLooksIfAbsent`，記進 marker。順序：store 的 `order` 是空的、快照不 stale → 把 `snapshot.order`（fleet id → alias → pool 的 `host_id`）寫進 `order` 一次。一輪最多一次 `setState`。
   - 檔頭註明「WWD-9 拿掉 fleet 時刪（MON-12）」；這是本線唯一新增的 fleet 讀者，MON-3b 的守門測試列為例外。
   - 測試：只搬一次；不覆蓋已有的；使用者「回到預設」之後不會被搬回來（marker）；daemon 主機略過；stale 快照不搬；`order` 只在空的時候寫；不同 alias 不搬。
   - mutation：拿掉 marker 檢查；拿掉 stale 檢查。

### MON-3a — 寬側欄的佇列條與件數

依賴 MON-1a（`slot`）。可與 MON-1c、MON-2b 並行。檔案（19 個、640 行）。

1. **色 token**：`spa/src/styles/themes.css` 的 `@theme`（`:2-41`）加 `--color-queue-heavy: var(--queue-heavy, #a18ee6)`、`--color-queue-light: var(--queue-light, #5fb3b3)`；四個主題區塊（dark `:43`、light `:85`、nord `:125`、dracula `:164`）各給值（light 用深一點的，對比要夠）。**不加進** `THEME_TOKEN_KEYS`（`lib/theme-tokens.ts:1-15`），自訂主題用 fallback，同 `journal-kind-*` 的做法。測試：新檔 `src/styles/themes.queue.test.ts`（讀 CSS 的寫法照 `src/index.css.test.ts`），斷言兩個 token 在 `@theme` 裡且有 fallback、四個主題區塊都有值。
2. **抽出 pool 的工作清單**：`components/watch/jobs/JobsPane.tsx:46-55`（`shown` 的篩選與攤平）抽成 `lib/pool/pool-jobs.ts` 的 `poolJobsOf(byOrigin, hosts, answered, oldDaemon)` 與 `usePoolJobs()`；JobsPane 改用它，行為不變（既有測試照過）。新測試涵蓋三種情況（有清單、答了空清單、還沒答）。
3. **佇列條的資料**（M1–M5、M15 共用）：新檔 `lib/pool/queue-strip.ts` `queueStripOf(entries, nseg)` → `{ segments: ('heavy' | 'light')[]; overflow; counts: { heavy; light }; top: QueueEntry[] }`；`entries` 是 `queueEntries`（`jobs-lanes.ts:57-63`）去掉 `held` 的；`slot` 缺席當重。測試：M1–M4 的例子（3 件 → 3 亮 7 灰；14 件 → 10 亮＋4；接走的不在；保留的不算；空的全灰）；節數 6、8、12；`top` 最多 10 件。mutation：`held` 不去掉；overflow 少一。
4. **件數與顏色**（M6、M7）：新檔 `lib/pool/host-counts.ts` `hostCountsOf(entries, jobs)` → `Record<alias, { n; tone: 'neutral' | 'warn' | 'error' }>`：這台送出、在排（非保留）的件數＋`claim.worker` 是這台的 `claimed`／`running` 件數；顏色看這台送出的在排工作：任一 `noHost` → error，否則任一 `long` → warn。測試：M6、M7 的例子；保留不算；紅優先；沒有 claim 的不算任何一台。mutation：紅黃優先序對調；`long` 的邊界。
5. **節數設定**（M2、W6）：`stores/useLayoutStore.ts:33-52` 加 `queueStripSegments: 6 | 8 | 10 | 12`（預設 10，rehydrate 讀到不合法的值改回 10）與 setter；它不在投影裡（`projections.ts` 的 layout 只投 `tabPosition`，加一案斷言它沒被投影）。`components/settings/TerminalSection.tsx:177-181`「主機格指示」之後加一列 `SegmentControl`「佇列條節數」。測試：store 預設與不合法值；設定頁顯示與寫入。
6. **`QueueStrip` 元件**：新檔 `components/fleet/QueueStrip.tsx`：寬側欄整行，`nseg` 節，`bg-queue-heavy`／`bg-queue-light`／`bg-border-default`；`overflow > 0` 時最後一節右上角 `CountBadge size="sm"`「+N」；滑過浮出清單（M5，定位照 `HostCard.tsx` 的 portal 做法，提成共用的 `QueueTooltip`，MON-4 再用）；按一下 `openWatchArea({ tab: 'jobs' })`；`aria-label`「資源池排隊 N 件」。測試：節數與顏色、+N、空的全灰且元素還在、清單的各行與「… 另有 n 件」、按一下開任務佇列。
7. **掛上去**：`components/fleet/FleetCells.tsx:17-39` 寬側欄在主機格上方放 `<QueueStrip />`；掛著時 `useEffect(() => watchJobs(), [])`（A4；寬窄都掛時 driver 會計數，沒問題）。測試：掛上就開始 watch、卸下就放掉；窄側欄還沒有條（MON-4）。
- 語系：佇列條與設定的字串兩邊都加。
- **Split**：task 5（節數設定）移到 MON-3b。

### MON-3b — 寬側欄主機格與卡片

依賴 MON-1c、MON-2b、MON-3a。檔案（約 20 個、780 行）。

1. **指標的讀數**（M8）：新檔 `lib/pool/indicator.ts` `indicatorReading(item, facts, load, zone)` → `{ pct; tone }`（七個項目，門檻照 M8；`load` 依區域上色，`unknown` 灰；壓力等級 0 改看記憶體使用率；`sysload` 是 load1 ÷ 核心數，70 黃、100 紅；`slots` worker 關著灰、滿了黃）。門檻與 `lib/fleet/bar-readings.ts` 相同，但不 import 它（那個檔 MON-12 刪）。測試：每個項目的表格，含邊界。mutation：各門檻的比較符號。
2. **側欄的模型**：新檔 `lib/pool/use-monitor-hosts.ts` `useMonitorHosts()` → 依 M13 排好的 `{ hostId; alias; state: PoolHostState; look; indicator; count }[]`（pool store、`poolHostState`、`orderPoolHosts`、`usePoolHostLook`、`indicatorOf`、`hostCountsOf(usePoolJobs…)`、5 秒的時鐘，照 `use-cell-models.ts:22-29`）。W4：pool store 在每個來源都失敗時會清空 `hosts`（`store.ts:141-148`），所以 `useMonitorHosts` 自己記住這次啟動最後一份非空清單（模組層變數，不持久），整個讀不到時用它、每台 `unknown`；從沒讀到過就是空的。測試：排序、件數、W4 的三種情況。
3. **`HostCell` 改寫**（M10、M17–M19、U3）：`components/fleet/HostCell.tsx:49-174` 改吃上面的模型。徽章 `resolveHostBadge(look, 'terminal')` → `HostBadge`，`host_down` 40%、`unknown` 60%；件數（ok 且 n > 0）用 `CountBadge size="sm"`，灰／`status-warning`／`status-error`；指標：環形 → `ContextRing`（`:162-165`），單柱、雙柱 → `HostBars`（`components/fleet/HostBars.tsx:33-41` 改成一根或兩根）；非 ok → 那一格換成 `LinkBreak`（`status-warning`）／`Power`（`status-error`）／`Question`（`text-muted`），粗細 bold；**刪掉** `:159` 的呼吸綠點。測試（`HostCell.test.tsx`、`HostCell.pool.test.tsx` 合併改寫）：三種形狀 × 代表性資料項；三種非 ok 狀態；件數三種顏色與 99+；沒有 `worker-running-*`。mutation：非 ok 時仍顯示件數；淡化比例對調。
4. **`HostCard` 改寫**（M11）：`components/fleet/HostCard.tsx` 改吃模型：讀數列（含「環」「柱」標記，沿用 `fleet.card.ring_tag` 等鍵或改名到 `pool.card.*`）、下半部各列、非 ok 的卡片（`livenessLines`）。定位與 portal 程式不動。測試改寫 `HostCard.test.tsx`。
5. **`FleetCells` 換資料**：`FleetCells.tsx` 改用 `useMonitorHosts()`，不再 import `useFleetHostViews`、`usePoolCellModels`。按一下 `openWatchArea({ poolHost: { hostId, alias } })`：`features/workspace/lib/open-watch-area.ts:9` 的 `WatchTarget` 加這一種；MON-6 換 tab id 之前，它用 alias 在 `readFleetView()` 的快照裡找 fleet id、開舊的主機分頁（橋接，註明 MON-6 刪）。測試：`FleetCells.test.tsx`、`FleetCells.pool.test.tsx` 改寫（順序、件數、按下去開的分頁）；`open-watch-area.return.test.ts` 加一案。
6. **守門測試**：新檔 `components/fleet/no-fleet-reads.guard.test.ts`：`components/fleet/**` 與 `lib/pool/**` 的非測試檔不 import `lib/fleet/`、不出現 `useFleetStore`；允許清單逐項寫理由與拿掉的期：`lib/pool/pool-look-migration.ts`（A3 搬遷，MON-12）、`lib/pool/cell-model.ts`、`lib/pool/use-cell-models.ts`、`lib/pool/use-pool-host-rows.ts`（Hosts 頁還在用，MON-9b）。mutation：在 `FleetCells.tsx` 暫加一行 `useFleetStore` 要轉紅。
- `usePoolCellModels`、`poolCellModel` 還有 Hosts 頁在用，本期不刪。
- **Split**：task 4（卡片）拆成 MON-3c。

### MON-4 — 窄側欄（U1、U2）

依賴 MON-3b。檔案（約 10 個、430 行）。

1. **`HostBars` 支援 N 根**（若 MON-3b 還沒做到）：`bars: (BarReading | null)[]`，寬度與間距可調。測試。
2. **四項狀態小圖**（M14、M16、W5）：新檔 `components/fleet/NarrowHostBlock.tsx`：底色是 `resolveHostBadge(look, 'terminal')` 的 light 層（沒設顏色用中性的 `surface-hover`）；四根直條依序 `cpu`、`mem`、`pressure`、`disk`（`indicatorReading`）；`CountBadge size="sm"` 在右上角；不畫主機圖示；非 ok → 置中的狀態圖示、整塊淡化比照徽章、不顯示件數；滑過浮出 `HostCard`（placement `right`），按一下同寬側欄。尺寸要放得進 44 px 的窄側欄（現在窄格 `HostCell.tsx:149` 是 `gap-0.5 px-0.5`、約 40 px），先做 32 × 24 px，最後的 px 由 lead 看截圖定。測試：四根的讀數與顏色、底色來自外觀與中性底、件數的位置與 test id、三種非 ok、滑過與按一下。
3. **兩個色塊＋數字**（M15）：新檔 `components/fleet/QueueCounts.tsx`：重（`bg-queue-heavy`）、輕（`bg-queue-light`）各一塊，數字是 `queueStripOf(...).counts`；0 件那塊 `bg-surface-hover`、照寫 0；永遠畫出來；滑過用 MON-3a 的 `QueueTooltip`，按一下開任務佇列。測試：數字（含 0／0）、顏色、不收起、按一下。
4. **掛上去**：`FleetCells.tsx` 窄側欄：上方 `<QueueCounts />`，每台一塊 `NarrowHostBlock`（直排、間距 0）。測試。

## 第二波（只寫到期；開工前 plan 再細化一版）

### MON-5 — 任務佇列分頁（M26–M34、M40 的沙漏）
- **目標**：`watch-jobs` 分頁改成「佇列池／工作池＋路線」，靜態；原因與為什麼在等；摘要列與最近結束；`started_at`；沙漏入口。
- **主要檔案**：`components/watch/jobs/JobsPane.tsx`、`JobLanes.tsx`、`JobQueue.tsx`（換成新的 `QueueFlow`）、`lib/pool/jobs-lanes.ts`（佇列池格、工作池格、M31／M32 的原因：`headroom_reasons`＋exceptions 的 holds＋名額）、`jobs-types.ts`／`jobs-parse.ts`（`started_at?`）、新的 `lib/pool/queue-routes.ts`（純幾何：同一行一條曲線；換行時沿行間空隙與兩側走，不穿過格子，照設計稿 `routeD`）、`QueueRoutes.tsx`（SVG 疊層，`ResizeObserver`＋`document.fonts.ready` 重算，hover 只高亮、不變暗，排隊中的畫虛線到能接的主機）；沙漏：`components/BackgroundSymbol.tsx` 一帶加按一下 → `openWatchArea({ tab: 'jobs' })`＋`writeSelectedJob`。`JobHistory`、`JobDetail`、`JobMenu` 沿用。
- **依賴**：MON-1a、MON-1b（M24 的「讀不到」）、MON-3a（`pool-jobs`、`queue-strip`）；MD-5（766）。
- **大小**：兩期——5a 版面與資料、5b 路線與 hover，各約 700 行。可與 MON-6～MON-10 並行（不碰同一批檔）。

### MON-6 — 主機頁骨架與「監控」（M41–M43、A7、W2）
- **目標**：6a 換 tab id 與分頁跟著 pool；6b 主機頁的殼（標頭、四個子分頁、範圍、橫幅 M22）與「監控」三段。
- **主要檔案**：6a：`lib/watch/ids.ts`（`watch-host-<host_id>`）、`features/workspace/lib/watch-area.ts:175-240`（主機分頁改依 pool 清單與 `orderPoolHosts`；60 秒規則照舊，舊的 fleet id 分頁由它收掉；拖曳主機分頁寫回 `order`）、`types/tab.ts:160`（`watch-host` 加 `hostId`）、`lib/pane-labels.ts:72`、`hooks/useTabDisplay.ts:136`（標籤取 pool 的 alias）、`components/HistoryPage.tsx:36`、刪 `open-watch-area.ts` 的橋接。6b：`components/watch/WatchHostPane.tsx` 重寫成主機頁、`Gauge.tsx`、`MetricChart.tsx`（加矮的卡片樣式）、`ProcTable.tsx`、`jobs/HostJobs.tsx`（壓縮版）、`lib/watch/view-memory.ts`（改以 `host_id` 為 key）、`lib/pool/history.ts`、`use-series-history.ts`（G-1 的欄位有就用）。告警標記讀 MON-10 的告警 store；MON-10 還沒出時先自己讀一次 `/alerts`。
- **依賴**：MON-1c、MON-2b、MON-3b；G-1（768）——之前 1 小時以上的負載、Swap、名額寫「等 series API 補欄位」。
- **大小**：兩期，各約 700 行。6a 要兩台裝置的 reconciler 測試；6b 要 TabContent 切走再切回的測試。

### MON-7 — 主機頁「狀態」（M44、M43）
- **目標**：六張卡、facts 全部欄位表、完整圖表分組（監控那六張不重複）、區域色帶、同步 hover。
- **主要檔案**：`lib/pool/types.ts`、`parse.ts`（G-2：`cpu.load15`、`user_pct`、`system_pct`、`cores[]`；G-3：`disk_io[]`；G-4：`procs_total`、`boot_at`、`self`、`swap_total_mb`；series 的新欄位；全部可選、null 安全）、新的 `components/watch/host/StatusTab.tsx` 與圖表定義、`ZoneBand.tsx`、`AvailRow.tsx`。每核 CPU 讀 5 秒 history（series 不帶 cores）；主機之間的延遲只畫當下值。
- **依賴**：MON-6；G-1～G-4（768）；G-3（768，沒趕上就 769，之前磁碟 IO 那張畫「A 線補」佔位）。
- **大小**：兩期（卡片與欄位表、完整圖表），各約 700 行。可與 MON-8、MON-9 並行。

### MON-8 — 主機頁「行程」（M45）
- **目標**：CPU 前 12／記憶體前 12（可拉時間軸）、常駐程式、未宣告的重指令、觀察紀錄（`next_until` 翻頁、依 id 去重）。
- **主要檔案**：`ProcTable.tsx`、`ResidentsTable.tsx`、新的 `lib/pool/observations-api.ts`（`/observations`、`/undeclared`；device token 403 時說明只有 admin token 能讀）、新的 `components/watch/host/ProcTab.tsx`。
- **依賴**：MON-6；MD-3（765）。
- **大小**：一期，約 600 行。

### MON-9 — 主機頁「設定」與 Hosts 頁（M46–M52、W7、W8）
- **目標**：9a 設定子分頁：接工作、名額、主人（沿用 `WorkerSettings`）、暫停（沿用 `WorkerExceptions`）、告警（唯讀門檻；兩個失聯開關等 W8 的 API，之前唯讀顯示現值）、推播總開關（M50）、側欄顯示（M51：顏色、圖示、形狀、資料、套用到四台、回到預設，寫 MON-2b 的 `writePoolHostLook`）；拿掉「設定 → 終端機」的「主機格指示」控制項（欄位與投影留到 MON-12 一起清，免得多升一次 ordinal）。9b Hosts 頁：拿掉資源池主機與觀察主機（M52），留一行連結。
- **主要檔案**：9a：新的 `components/watch/host/SettingsTab.tsx`、`WorkerSettings.tsx`、`WorkerExceptions.tsx`、`WatchSettings.tsx`（watch 列整段拿掉）、新的 `lib/pool/push-switch.ts`（`GET /api/hostconfig`、`PUT /api/hostconfig/wwd_alerts`）、`HostColorField.tsx`／`HostIconField.tsx`（沿用）、`TerminalSection.tsx`。9b：刪 `components/hosts/` 的 `PoolHostsSection`、`PoolHostGroup`、`PoolHostPage`、`PoolHostRoute`、`PoolStatusDot`、`ProbeHostsSection`、`ProbeHostPage`、`ProbeHostEditor`、`ProbeHostForm`、`ProbeCheck`、`probe-rows.tsx`、`use-probe-rows.ts`、`useProbeEditor.ts`、`FleetRowEditor.tsx`、`pool-status-word.ts`，`lib/pool/use-pool-host-rows.ts`；改 `HostSidebar.tsx`、`OverviewSection.tsx`、`components/HostPage.tsx`。
- **依賴**：MON-6、MON-2b；W8 的 API（只擋那兩個開關）。
- **大小**：9a 約 750 行；9b 以刪除為主（只算新增行）。可與 MON-7、MON-8 並行。

### MON-10 — 事件分頁、盾牌與分頁上的數字（M35–M37、M23）
- **目標**：事件改讀 wwd 告警與觀察；篩選（主機、來源、狀態）、依日分段、兩種失聯的列；盾牌與「事件」分頁的數字（M36，值班者那一半在 S7 前為 0）。
- **主要檔案**：新的 `lib/pool/alerts-store.ts`（每台 `GET …/alerts`；`alerts.changed` 經 `hooks/useMultiHostEventWs.ts`；沒有 `pool.alerts.v1` 時 30 秒輪詢）、`components/watch/WatchEventsPane.tsx`（重寫）、`features/workspace/components/WatchShieldButton.tsx`、`features/workspace/lib/watch-area.ts`（事件分頁的建立條件改看 `pool.alerts.v1`）；`lib/watch/events-store.ts` 的讀者移走。
- **依賴**：MON-1a、MON-1b；MD-2、MD-3（765）。
- **大小**：一～兩期。可與 MON-5、MON-6 並行。

### MON-11 — 守門員分頁（M38、M39）
- **目標**：值班卡、打開對話／叫醒／問問題、進行中的 incident、處理紀錄、提議的同意與拒絕。
- **主要檔案**：`components/watch/WatchGuardianPane.tsx`、`WatchLaunchPanel.tsx`、`lib/watch/status-store.ts`（lucia 期間仍讀 `/api/watch/status`、`/wake`，W9）、新的 incident client（S7 的路由）。
- **依賴**：A 線的 S7、S8（8b；P7 (A) 由 1f 排）。等 A 線的 plan 出來再細化。
- **大小**：一～兩期。

### MON-12 — fleet 退場（A11、W9）
- **目標**：SPA 不再讀 `fleet.v1`、`gate.decisions.v1`；`watch.v1` 只留守門員的 status 與 wake。
- **主要檔案**：刪 `lib/fleet/*`（store、ws、api、history、config-*、probe-*、parse、types、`use-fleet-*`、`bar-readings.ts`、`ring-driver.ts`）；`hooks/useMultiHostEventWs.ts:148-150`、`:354-357`（fleet 的訂閱與處理）；`hooks/useTabQueued.ts` 的 fleet 那半；`components/watch/QueueSection.tsx`、`QueueNow.tsx`、`QueueLane.tsx`、`QueueHistory.tsx`；`lib/watch/api.ts` 的 decisions、`decisions-types.ts`；`lib/pool/pool-look-migration.ts`；`cell-model.ts`、`use-cell-models.ts` 剩下的部分；`useUISettingsStore.fleetCellIndicator` 與它的投影（settings ordinal 再升一次）；fallback 與 fleet 的語系鍵。守門測試改成全 repo 不 import `lib/fleet/`。
- **依賴**：MON-5～MON-10；時程跟 1f 的 WWD-9（daemon 端拿掉 `fleet.v1`、`gate.decisions.v1`）對齊。
- **大小**：一～兩期；純刪除只算新增行。

## 順序與分工

- 依賴：MON-1a ∥ MON-1b ∥ MON-2a → MON-1c（1a、1b 之後）、MON-2b（1a、2a 之後）、MON-3a（1a 之後）→ MON-3b（1c、2b、3a 之後）→ MON-4。
- 建議兩位 Sonnet member：
  - 甲：MON-1a → MON-1c → MON-3b → MON-4。
  - 乙：MON-1b → MON-2a → MON-2b → MON-3a。
  - 只有一位時照 1a → 1b → 1c → 2a → 2b → 3a → 3b → 4 疊分支做。
- 第二波：MON-5、MON-6、MON-10 可同時開；MON-7、MON-8、MON-9 在 MON-6 之後並行；MON-11 等 A 線；MON-12 最後。
- 改到 team 模組的不在本線範圍；本線全是純 SPA，88 自己放行（B 線 10-19 起的規則），bump 與部署歸 1f。MON-2a 升 settings ordinal，88 放行前先告訴 1f（同 TG-1a-1）。

## 待查與待確認

- **iOS**：purdex-ios 對 settings ordinal 13 與新 store 的反應（MON-2a task 4）。
- **⚠ 待 88 確認** W1–W9（spec §7.3）：本 plan 已照建議寫；88 改了，受影響的是 W1 → MON-2a task 1、MON-2b task 1；W2 → MON-2b task 2、MON-6a；W3、W4 → MON-1b task 2、MON-3b task 2；W5 → MON-4 task 2；W6 → MON-3a task 5；W7 → MON-2b task 1、MON-9a；W8 → MON-9a；W9 → MON-11、MON-12。
- **請 1f 排（不擋第一波）**：兩種失聯推播開關的寫入 API（W8）；series 補 `slots`、PSI、`sessions_room`、常駐程式（spec §3 最後一段）。
