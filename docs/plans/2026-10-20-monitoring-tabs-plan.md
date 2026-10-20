# 監控分頁組＋側欄主機列 — plan

> spec：`docs/specs/2026-10-20-monitoring-tabs-spec.md`（下文的 M、A、W、P 編號都是 spec 的）。B 線 lead 88，2026-10-20。
> 一期一張 PR（≤ 800 行、≤ 20 檔，測試也算）；一個 task 一個 commit，先寫測試、看它紅，再實作。執行者：Sonnet member。
> 第一波（MON-1a～MON-4）細到 task；第二波（MON-5～MON-12）只寫到期，**開工前 plan 再細化一版**（含 file:line 與測試案例），細化版先給 88 看。
> spec 的 W1–W9 已由 88 定案（10-20，spec §7.3），本 plan 照定案寫。
> 行號以 `origin/main 86ced9555` 為準（從初稿的 `abaa46be5` 到這裡，main 沒有改到本 plan 引用的任何檔案，第一波的 file:line 已逐一核對），開工時以當下的 `origin/main` 再核對一次。
> 修訂 1（10-20）：照 codex plan 審查（task-mvgk4yis-tsgk70）的 8 條修正，88 全部採納；對照表在文末。

## 共通規則

- **流程與規矩**照 `~/.local/share/purdex-lead/briefs/b-common-rules.md`（2026-10-20 版）：worktree、TDD、每 task 一個 commit、i18n 兩邊、Phosphor、只用 theme token、不起 dev server（截圖由 lead 補）、只 kill 自己的 pid、不用 `git stash`、lead 放行才 merge、疊加 PR 不用 `--delete-branch`。
- **測試分層**：
  - 開發中：只跑改到的單一測試檔（`cd spa && npx vitest run <檔>`，經閘門）。
  - PR 層（併入最新 `origin/main` 之後的 head，送 air26 的 `assist-run.sh`）：`npx vitest related --run <改到的 spa/src 檔>`、守門測試（`git ls-files 'src/**/*guard*.test.ts*'`＋`src/main.test.tsx`＋`src/locales/locale-completeness.test.ts`）、`npx tsc --noEmit -p tsconfig.app.json`、`npx tsc --noEmit -p tsconfig.node.json`、`npx eslint <改到的檔>`。先在 mlab 跑單檔 vitest＋eslint、`tsc -p tsconfig.app.json` 單獨送 mserv，最終 head 才送 air26 一次。
  - **不跑完整 vitest**，不跑 `go test ./...`（本線只有 SPA）。
- **tab-hosted**：主機頁、任務佇列、事件等分頁裡要跨分頁保住的 state（子分頁、範圍、篩選、捲動、選取、草稿）放在元件外（`lib/watch/view-memory.ts`、`lib/pool/jobs-view-memory.ts` 那一類），每個新的分頁元件用真的 `TabContent` 寫「切走再切回」的測試（範例 `components/execution/WorkerDeckPane.tab-switch.test.tsx`、`components/watch/jobs/JobsPane.tab-switch.test.tsx`）。側欄不是 tab-hosted。
- **側欄不讀 fleet**（A1、A11）：MON-1c 起側欄的主機格只由 pool hosts（＋MON-2 的順序與外觀）產生，並加守門測試 `components/fleet/no-fleet-reads.guard.test.ts`。範圍是側欄（`components/fleet/**`）與本線在 `lib/pool/` 新增或改成只讀 pool 的檔（逐一列在測試裡）；**唯一豁免** `lib/pool/pool-look-migration.ts`（A3 的一次性搬遷，MON-12 刪）。不在範圍的是兩個過渡讀者：主機頁（`components/watch/WatchHostPane.tsx` 與它經 `lib/pool/fleet-bridge.ts` 的 fleet 接法，第一波照舊，MON-6 改寫時拿掉並把守門測試擴到主機頁）、Hosts 頁（`components/hosts/**`、`lib/pool/use-pool-host-rows.ts`，MON-9b 刪掉）。每期都不得在範圍內新增 fleet 讀者。
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
5. **jobs 的 `ownDaemon`**（spec §4 A1 的「現況缺陷」）
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

### MON-1c — 側欄只讀 pool

依賴 MON-1a、MON-1b、MON-2b（側欄的順序與外觀要用 MON-2b 的 `orderPoolHosts`、`usePoolHostLook`）。合計 21 個檔、約 650 行，所以**分兩張**：
- **MON-1c-0**（task 1，純搬移；只依賴 MON-1a，可以先做）：9 個檔——新檔 `lib/pool/fleet-bridge.ts`＋測試、`lib/pool/cell-model.ts`＋測試、`use-cell-models.ts`、`components/watch/WatchHostPane.tsx`、`components/watch/AvailRow.tsx`、`lib/pool/use-pool-host-rows.ts`、`components/fleet/FleetCells.tsx`（後五個只改 import；FleetCells 到 MON-1c 才離開這條接法）。純搬移不派 codex，PR 附逐宣告位元組比對。
- **MON-1c**（task 2–5）：16 個檔、約 560 行——`lib/pool/cell-model.ts`＋測試、`use-cell-models.ts`、新檔 `lib/pool/use-monitor-hosts.ts`＋測試、`components/fleet/HostCell.tsx`、`HostCell.test.tsx`、`HostCell.pool.test.tsx`、`HostCard.tsx`、`HostCard.test.tsx`、`FleetCells.tsx`、`FleetCells.test.tsx`、`FleetCells.pool.test.tsx`、`features/workspace/lib/open-watch-area.ts`、`open-watch-area.return.test.ts`、新檔 `components/fleet/no-fleet-reads.guard.test.ts`。

1. **純搬移**：`cell-model.ts` 的 `matchPoolHost`（`:37-39`）、`poolCellModel`（`:59-76`）、`factsToFast`（`:42-57`）、`PoolCellExtra`（`:11-29`）與 `use-cell-models.ts:31-49` 帶 fleet views 的 `usePoolCellModels`，搬到新檔 `lib/pool/fleet-bridge.ts`；`WatchHostPane.tsx:21-22`、`use-pool-host-rows.ts:4`、`:7`、`AvailRow.tsx`、`FleetCells.tsx:10` 的 import 改指它。既有測試搬去 `fleet-bridge.test.ts`。逐宣告位元組比對＋編譯，行為不變。這個檔是主機頁（到 MON-6）與 Hosts 頁（到 MON-9b）的過渡讀者，不在守門測試範圍，MON-9b 刪。
2. **側欄的模型**：新檔 `use-monitor-hosts.ts` `useMonitorHosts()`：pool store 的 `hosts` → `orderPoolHosts(hosts, order)`（M13）→ 每台 `{ hostId, alias, daemonHostId, state: poolHostState(pool, lastFactsAt[hostId], now), look: usePoolHostLook(hostId) }`；5 秒時鐘（照 `use-cell-models.ts:22-29`）；W4：pool store 在每個來源都失敗時會清空 `hosts`（`store.ts:141-148`），所以這裡記住這次啟動最後一份非空清單（模組層變數，不持久），讀不到時用它、每台 `unknown`；從沒讀到過就是空的。**不收 fleet views，也沒有「fleet 有、pool 沒列」的主機。** `cell-model.ts` 只留側欄用、只讀 pool 的型別與函式：`CellView`（HostCell／HostCard 讀的欄位與原 `PoolCellExtra` 的內容；讀數用自己宣告的 `CellFast`，欄位同 `FleetFast`）與 `cellOf(monitorHost)`，不 import `lib/fleet/`（連 `import type` 都不行）。`use-cell-models.ts` 只剩 `useRunPool`（與 MON-1a 的 `refreshPoolIdentities`）。測試：排序、狀態、外觀、W4 三種情況、pool 沒列的 fleet 主機不出現。
3. **兩個元件改吃 `CellView`**（暫時的樣子，狀態圖示與每台指標在 MON-3b）：`HostCell.tsx:49-62` 的 `view` 改成 `CellView`；拿掉 `waitingPrimary`、`primaryElsewhere`（fleet 主方的提示）與舊閘門的件數（`:103-104`、`:160` 讀 `view.slow.queue`；pool 模式下本來就是空的，wwd spec §10 的 Known gaps）；刪 `:155-158` 的 fallback 斷線角標；`:79` 的無障礙名稱改用 `livenessWord`；非 ok 照現在「離線」的畫法（徽章 40%、只有軌道）。`HostCard.tsx` 同樣改吃 `CellView`，`:168-170` 的 fallback 行換成 `livenessLines`，拿掉主方提示那行。測試 `HostCell.test.tsx`、`HostCell.pool.test.tsx`、`HostCard.test.tsx` 改寫。
4. **`FleetCells` 只讀 pool**：`FleetCells.tsx:17-39` 改用 `useMonitorHosts()`，不再 import `useFleetHostViews`、`usePoolCellModels`。按一下 `openWatchArea({ poolHost: { hostId, alias } })`：`open-watch-area.ts:9` 的 `WatchTarget` 加這一種；MON-6 換 tab id 之前，它用 alias 在 `readFleetView()` 的快照裡找 fleet id、開舊的主機分頁（橋接在 `open-watch-area.ts`，不在側欄；MON-6 刪）。測試 `FleetCells.test.tsx`、`FleetCells.pool.test.tsx` 改寫（順序、外觀、按下去開的分頁）；`open-watch-area.return.test.ts` 加一案。
5. **守門測試**：`no-fleet-reads.guard.test.ts`，範圍與唯一豁免照共通規則（側欄與本線的 `lib/pool` 新檔，逐一列名；`cell-model.ts`、`use-cell-models.ts` 在範圍內、不豁免）。範圍內的非測試檔不得 import `lib/fleet/`（含 `import type`），也不得出現 `useFleetStore`、`useFleetHostViews`、`readFleetView`。mutation：在 `FleetCells.tsx` 暫加一行 `useFleetStore`、在 `cell-model.ts` 暫加 `import type { FleetFast }`，都要轉紅。

### MON-1d — 主機分頁與 Hosts 頁換新狀態，刪 fallback

依賴 MON-1b、MON-1c。檔案（15 個、約 380 行，多數是修改與刪除）：`lib/pool/fleet-bridge.ts`＋測試、`components/watch/AvailRow.tsx`＋測試、`WatchHostPane.tsx`、`lib/pool/use-pool-host-rows.ts`、`components/hosts/pool-status-word.ts`、`PoolHosts.test.tsx`、`PoolHostPage.test.tsx`、刪 `lib/pool/host-view.ts`＋測試、`lib/pool/words.ts`＋測試、`lib/pool/store.ts`＋測試。

1. **過渡接法換新狀態**：`fleet-bridge.ts` 改用 `poolHostState`（不再收 fleet 的 `stale`、不再有 fallback）；`PoolCellExtra.state` 改成 `PoolLiveness`，`reason`／`since` 換成 `checkedAt`、`lastFactsAt`、`stalled`；fleet 有、pool 沒列的主機（只剩主機頁與 Hosts 頁會遇到）給 `unknown`。測試。
2. **主機分頁與 Hosts 頁**：`AvailRow.tsx:24-27`、`WatchHostPane.tsx:115`、`:142`（`'live'` → `'ok'`）、`use-pool-host-rows.ts:40-72`（燈：ok 綠、`wwd_down` 黃、`host_down` 與 `unknown` 灰；讀數只在 ok 時有，不再讀 fleet 的 `fast`）、`pool-status-word.ts:7-8`。測試 `AvailRow.test.tsx`、`PoolHosts.test.tsx`、`PoolHostPage.test.tsx`。主機頁其他的 fleet 讀取（`WatchHostPane.tsx:94`、`:104-105` 的快照與主方）是 A1 的過渡例外，留到 MON-6。
3. **刪除**：`host-view.ts` 與測試、`FallbackReason`、`fallbackWord`（`words.ts:59`）、store 的 `lastLiveAt`。fallback 的語系鍵先不刪（死鍵不影響 locale-completeness），MON-12 一起清，本期不動語系檔。

### MON-2a — 外觀與順序 store（Profile Sync）

可與 MON-1a、MON-1b 並行。照 TG-1a-1 的清單（`git show 7fe823a27`、`5e74a7617`）。檔案（15 個、約 650 行）。

1. **store**（A3、W1）
   - 新檔 `spa/src/stores/usePoolHostsStore.ts`，`lib/storage/keys.ts:52` 之後加 `POOL_HOSTS: 'purdex-pool-hosts'`。狀態 `{ order: string[]; looks: Record<string, PoolHostLook> }`（spec §4 A3；外觀欄位依 88 定案 W1，與 `HostLookEntry` 相同）。
   - `merge` 一律清洗：key 必須是 wwd 產生的 `host_id` 格式 `^[0-9a-f]{16}$`（`cmd/wwd/main.go:351-355`：8 個亂數位元組的小寫 hex），`order` 與 `looks` 都一樣，其他一律丟掉（含 `__proto__`）；最多 32 台；`order` 去重、只留合法 id；外觀欄位用 `sanitizeHostLookEntry`（`stores/useHostLookStore.ts:59-68`），丟掉 `name`；`indicator.shape` 在三種之內，`data` 1～2 項且都認得（環形、單柱只留第一項；雙柱缺第二項補 `'pressure'`）。
   - 動作：`setOrder`、`patchLook(hostId, fn)`（回 `undefined` 刪掉）、`putLooksIfAbsent`、`setIndicatorForAll(hostIds, indicator)`。persist version 1，`partialize` 只存 `order`、`looks`。`syncManager.register`。
   - 測試：清洗的表格——收 `82b11c521df16c12`；**拒** alias（`mlab`、`mserv`）、Purdex daemon 的 host id（帶 `:`）、大寫 hex、15／17 位、`__proto__`；各動作；另一個視窗寫入後 rehydrate 也經過清洗。已知限制：手動改過 `host_id` 檔、不是這個格式的 wwd，外觀與順序存不進來（四台都是 wwd 自己產生的）。
   - mutation：拿掉 32 台的上限；接受不認得的資料項；key 的正規式放寬成任意字串（拒 alias 的案例要轉紅）。
2. **settings 的接線**
   - `lib/profile/types.ts:35` 之後加 `'purdex-pool-hosts'`；`projections.ts:98` 之後加 `...settingsPaths('purdex-pool-hosts', ['order', 'looks'])` 與註解；`SECTION_SCHEMA_ORDINAL.settings`（`:149`）12 → 13，註解加「13: purdex-pool-hosts.{order,looks} (monitoring tabs spec A3, MON-2a) — a projection change …；an ordinal-12 payload has no such store: applying it CLEARS the store」；`collector.ts:121`、`apply-to-stores.ts:132` 的 store 對照加一筆；`applier.ts:376` `CLEARED_WHEN_ABSENT` 加入。若 merge 時 `origin/main` 已到 13，順延並同步改註解與測試。
   - **不做** host id 換算：不碰 `sections.ts:390-396`、`applier.ts:109-129`（`settingsFromWire`）、`host-identity.ts`、`host-reresolve.ts`、`host-lifecycle.ts`，也不加進 `host-lifecycle.test.ts:632` 的 `PERSISTED`——key 是 wwd 的 `host_id`，每台裝置都一樣。
   - 既有測試跟著改：`applier.test.ts:1129`（13 → 14）、`apply-to-stores.test.ts:527`（13 → 14）、`collector.test.ts:60-70`（store 與 key 清單）、`projections.test.ts:281`（13 → 14）與 `:331-344` 的 store 對照、**`projections.test.ts:813-835` 的 fingerprint／ordinal 守門 snapshot**（settings 換成新的 fingerprint 與 13，跟 ordinal 同一個 commit 改，TG-1a-1 的同一步）、`executor.direction.integration.test.ts:739`（12 → 13 與註解）。
   - 新測試 `lib/profile/apply-to-stores.pool-hosts.test.ts`：`hash(build(apply(p))) === hash(p)`；ordinal 12 的 payload（沒有這個 store）套用後本機清空；壞的條目套用時被清洗；key 原樣經過（長得像本機 host id 的也不換）。
   - mutation：從 `CLEARED_WHEN_ABSENT` 拿掉 → 清空的測試轉紅。
3. **收斂與衝突測試**（新檔 `lib/profile/pool-hosts.integration.test.ts`，寫法照 `lib/profile/task-groups.integration.test.ts`）：
   - 裝置 A 改 mserv 的外觀與指標 → 經真的 collector 與 applier 到裝置 B；B 改順序 → 回到 A；兩台內容相同時 build 出同一個 hash、不會互推。
   - **ordinal 12 的 SOT**（照 `task-groups.integration.test.ts:239-273` 的兩案）：本機 settings 區乾淨 → 套用、store 清空、SOT 升到 13 一次、之後安靜；**本機 store dirty（剛改了外觀與順序）、SOT 是 ordinal 12 → settings 進 `locked:conflict`，本機的外觀與順序保留，SOT 仍是 ordinal 12、寫者不變**。
4. **iOS 查證**（不寫程式，寫進 PR 說明）：purdex-ios 只單向讀 settings（qr-pairing spec R4）並套它自己列的外觀鍵。到 GitHub 上 purdex-ios 的 `main` 確認它遇到不認得的 settings ordinal 不會拒絕、會略過不認得的 store。**待查**；若它卡 ordinal，停下回報 88。
- 發版注意（寫進 PR 說明）：settings 的 schema 鎖是整個 profile 的，還在舊版的裝置會鎖 settings 直到升級（每次升 ordinal 都一樣）。
- **Split**：task 3 獨立一張。

### MON-2b — 外觀解析、排序、一次性搬遷

依賴 MON-1a（daemon 對應）、MON-2a。可與 MON-1b 並行（MON-1c 依賴它）。檔案（8 個、約 520 行）：新檔 `lib/pool/host-looks.ts`＋測試、新檔 `lib/pool/pool-look-migration.ts`＋測試、`lib/pool/use-cell-models.ts`（抽出 `retainPool()`）、`lib/storage/keys.ts`、`spa/src/main.tsx`、`main.test.tsx`。

1. **解析與寫入**（A3、M9、M13、W7）
   - `usePoolHostLook(hostId)`：有 daemon（`usePoolHostDaemons`）→ `useHostLookResolver()(hostConfigId)` 的 `colors`／`color`／`icon`／`iconWeight`（`lib/host-look.ts`；不直接讀 HostConfig 的外觀欄位，`host-look.guard.test.ts` 管著）；沒有 → 新 store。
   - `indicatorOf(look)`：store 的 `indicator`，沒有時是預設：由 `useUISettingsStore.fleetCellIndicator` 換算（`ring` → 環形＋`load`；`bars-pressure` → 雙柱 `cpu`＋`pressure`；`bars-memory` → 雙柱 `cpu`＋`mem`）。
   - `orderPoolHosts(hosts, order)`：純函式，M13。
   - `writePoolHostLook(hostId, patch)`：顏色、圖示 → 有 daemon 的寫 `useHostLookStore.patchLook(lookKeyOf(host, looks), …)`（Hosts 頁現行的寫法），沒有的寫新 store；指標一律寫新 store。
   - 測試：daemon 主機讀與寫都走 `useHostLookStore`、分頁徽章同一份；沒有 daemon 的走新 store；預設指標的三種換算；排序的 M13 例子；`order` 有、pool 沒列的不出現。
   - mutation：daemon 判斷反過來；排序把沒排到的放前面。
2. **一次性搬遷，排在 Profile Sync 之前**（A3、W2）
   - `bootPoolLooks(): Promise<void>`（永不 reject），**併進 `main.tsx:95` 的開機鏈**：`void bootHostLooks().then(() => bootPoolLooks()).then(() => startProfileSync())`，所以 collector 建立之前搬遷已經做完或已經放行，第一次 settings build 不會早於它。
   - 這台裝置的 marker `purdex-pool-host-looks-migrated`（JSON `{ done, tries }`；不投影、不註冊 `syncManager`；讀寫包 try/catch，照 `lib/host-look-migration.ts:38-52`）。`done` 為真 → 立刻 resolve。
   - 否則等兩樣東西：pool 的第一次回答（`usePoolStore` 的 `sourceHostId` 不是 null；等待期間用 `retainPool()` 打開 pool 的讀取——它是 `useRunPool`（`use-cell-models.ts:13-20`）同一個掛載計數抽出來的，結束時放掉）與 fleet 第一份不 stale 的快照（`useFleetStore` 的 `snapshot` 有值且 `stale` 為 false）。**兩樣都到 → 搬一次**：每台 fleet `kind: 'probe'` 的主機（沒有 daemon 的）、帶外觀、依 alias 對到一台 pool 主機、新 store 沒有那個 `host_id` 的外觀 → `putLooksIfAbsent`；store 的 `order` 是空的 → 把 `snapshot.order`（fleet id → alias → pool 的 `host_id`，pool 沒列的略過）寫進 `order`。一次 `setState`，寫 marker `done`，resolve。
   - **逾時**：從開機起 10 秒還沒等到兩樣 → 不搬、resolve 放行 Profile Sync；**放行之後這次執行不再搬**（晚到的 fleet 快照不觸發寫入），marker 的 `tries` 加一，下次開機（同樣在 Profile Sync 之前）再試；連續 3 次逾時就寫 `done` 放棄（多半是這台已經沒有 fleet 可讀）。
   - 不靠 MD-1 判斷有沒有 daemon（開機時 `/api/info` 可能還沒讀到），改看 fleet 的 `kind`；有 daemon 的主機外觀本來就在 `useHostLookStore`，不搬。
   - 各裝置搬進來的內容相同（probe 主機的外觀與順序是兩台 daemon 上同一份），settings 的 hash 相同，不會互推；第一波沒有編輯這些外觀的畫面（MON-9a 才有），所以搬遷期間不會跟別台的編輯衝突。MON-9a 之後才第一次開機、而 SOT 已有不同內容的裝置，會進 settings 衝突，由使用者選「留本機／用同步版本」（Profile Sync 既有行為）。
   - 檔頭註明「WWD-9 拿掉 fleet 時刪（MON-12）」；這是側欄守門測試範圍內唯一的豁免。
   - 測試（`pool-look-migration.test.ts`）：兩樣都到才搬；只搬 probe 主機、不覆蓋已有的外觀；`order` 只在空的時候寫；stale 快照不算；`done` 之後立刻 resolve、使用者「回到預設」後不會被搬回來；逾時放行且不寫入、放行後晚到的快照也不寫入、`tries` 加一、第 3 次逾時寫 `done`；`retainPool` 有放掉。開機順序（`main.test.tsx`，照 `:86-143` 的寫法）：`bootPoolLooks` 還沒 resolve 時 `startProfileSync` 沒被呼叫；順序是 host 外觀搬遷 → pool 外觀搬遷 → `startProfileSync`，而且 `startProfileSync` 被呼叫時新 store 已經有搬進來的外觀（第一次 collector build 拿得到）。
   - mutation：拿掉 `done` 檢查；拿掉 stale 檢查；逾時後仍允許寫入；把 `bootPoolLooks` 移出開機鏈（順序測試要轉紅）。

### MON-3a — 寬側欄的佇列條與件數

依賴 MON-1a（`slot`）、MON-1c（兩期都改 `FleetCells` 的接線，1c 先 merge 再開）。不與 MON-1c 並行。檔案（19 個、約 680 行）。

1. **色 token**：`spa/src/styles/themes.css` 的 `@theme`（`:2-41`）加 `--color-queue-heavy: var(--queue-heavy, #a18ee6)`、`--color-queue-light: var(--queue-light, #5fb3b3)`；四個主題區塊（dark `:43`、light `:85`、nord `:125`、dracula `:164`）各給值（light 用深一點的，對比要夠）。**不加進** `THEME_TOKEN_KEYS`（`lib/theme-tokens.ts:1-15`），自訂主題用 fallback，同 `journal-kind-*` 的做法。測試：新檔 `src/styles/themes.queue.test.ts`（讀 CSS 的寫法照 `src/index.css.test.ts`），斷言兩個 token 在 `@theme` 裡且有 fallback、四個主題區塊都有值。
2. **抽出 pool 的工作清單與「pool 可讀」閘門**：`components/watch/jobs/JobsPane.tsx:46-56`（`shown` 的篩選與攤平）抽成 `lib/pool/pool-jobs.ts` 的 `poolJobsOf(byOrigin, hosts, answered, oldDaemon)` 與 `usePoolJobs()`；JobsPane 改用它，行為不變（既有測試照過）。注意 JobsPane 在 pool 還沒回答時會**保留已讀的工作**（`shown` 回 `() => !oldDaemon`），那是任務佇列頁要的，**側欄不能直接沿用**：同檔另給 `poolReadable(state)` 與 `usePoolReadable()`＝`usePoolStore` 的 `sourceHostId !== null`（讀到時由 `applyHosts` 設，`store.ts:97`；每個來源都失敗時清成 null，`:141-148`；沒有任何來源時也是 null，`:118-120`）。新測試：三種清單情況（有清單、答了空清單、還沒答）；閘門在三種 store 狀態下的值。
3. **佇列條的資料**（M1–M5、M15、M25 共用）：新檔 `lib/pool/queue-strip.ts` `queueStripOf(entries, nseg, readable)` → `{ readable; segments: ('heavy' | 'light')[]; overflow; counts: { heavy; light }; top: QueueEntry[] }`；`entries` 是 `queueEntries`（`jobs-lanes.ts:57-63`）去掉 `held` 的；`slot` 缺席當重。**`readable` 為 false 時一律回空：沒有彩色節、`overflow` 0、`counts` 0／0、`top` 空**（M25），不管 jobs store 裡還留著什麼。測試：M1–M4 的例子（3 件 → 3 亮 7 灰；14 件 → 10 亮＋4；接走的不在；保留的不算；空的全灰）；節數 6、8、12；`top` 最多 10 件；**所有來源都失敗、jobs store 仍有 14 件舊的在排工作 → 全灰、沒有 +N、counts 0／0**。mutation：`held` 不去掉；overflow 少一；忽略 `readable`（上一案要轉紅）。
4. **件數與顏色**（M6、M7）：新檔 `lib/pool/host-counts.ts` `hostCountsOf(entries, jobs)` → `Record<alias, { n; tone: 'neutral' | 'warn' | 'error' }>`：這台送出、在排（非保留）的件數＋`claim.worker` 是這台的 `claimed`／`running` 件數；顏色看這台送出的在排工作：任一 `noHost` → error，否則任一 `long` → warn。測試：M6、M7 的例子；保留不算；紅優先；沒有 claim 的不算任何一台。mutation：紅黃優先序對調；`long` 的邊界。
5. **節數設定**（M2、W6）：`stores/useLayoutStore.ts:33-52` 加 `queueStripSegments: 6 | 8 | 10 | 12`（預設 10，rehydrate 讀到不合法的值改回 10）與 setter；它不在投影裡（`projections.ts` 的 layout 只投 `tabPosition`，加一案斷言它沒被投影）。`components/settings/TerminalSection.tsx:177-181`「主機格指示」之後加一列 `SegmentControl`「佇列條節數」。測試：store 預設與不合法值；設定頁顯示與寫入。
6. **`QueueStrip` 元件**：新檔 `components/fleet/QueueStrip.tsx`：吃 `usePoolReadable()` 與 `usePoolJobs()`，交給 `queueStripOf`；寬側欄整行，`nseg` 節，`bg-queue-heavy`／`bg-queue-light`／`bg-border-default`；`overflow > 0` 時最後一節右上角 `CountBadge size="sm"`「+N」；滑過浮出清單（M5，定位照 `HostCard.tsx` 的 portal 做法，提成共用的 `QueueTooltip`，MON-4 再用）；按一下 `openWatchArea({ tab: 'jobs' })`；`aria-label`「資源池排隊 N 件」。讀不到資源池時整行灰、滑過寫「讀不到資源池」。測試：節數與顏色、+N、空的全灰且元素還在、清單的各行與「… 另有 n 件」、按一下開任務佇列；**用真的 `usePoolStore` 讓每個來源都失敗（`sourceHostId` null）、`useJobsStore` 留著舊的在排工作 → 畫面全灰、沒有 +N**。
7. **掛上去**：`components/fleet/FleetCells.tsx:17-39` 寬側欄在主機格上方放 `<QueueStrip />`；掛著時 `useEffect(() => watchJobs(), [])`（A4；寬窄都掛時 driver 會計數，沒問題）。測試：掛上就開始 watch、卸下就放掉；窄側欄還沒有條（MON-4）。
- 語系：佇列條與設定的字串兩邊都加。
- **Split**：task 5（節數設定）移到 MON-3b。

### MON-3b — 寬側欄主機格與卡片

依賴 MON-1c、MON-3a。檔案（約 14 個、650 行）：新檔 `lib/pool/indicator.ts`＋測試、`lib/pool/use-monitor-hosts.ts`＋測試、`lib/pool/cell-model.ts`、`components/fleet/HostCell.tsx`、`HostCell.test.tsx`、`HostCell.pool.test.tsx`、`HostBars.tsx`＋測試、`HostCard.tsx`、`HostCard.test.tsx`、`locales/zh-TW.json`、`en.json`。

1. **指標的讀數**（M8）：新檔 `lib/pool/indicator.ts` `indicatorReading(item, facts, load, zone)` → `{ pct; tone }`（七個項目，門檻照 M8；`load` 依區域上色，`unknown` 灰；壓力等級 0 改看記憶體使用率；`sysload` 是 load1 ÷ 核心數，70 黃、100 紅；`slots` worker 關著灰、滿了黃）。門檻與 `lib/fleet/bar-readings.ts` 相同，但不 import 它（守門測試；那個檔 MON-12 刪）。測試：每個項目的表格，含邊界。mutation：各門檻的比較符號。
2. **側欄模型加件數與指標**：MON-1c 的 `useMonitorHosts()` 每台多帶 `indicator`（`indicatorOf(look)`，MON-2b）與 `count`（`hostCountsOf(queueEntries(usePoolJobs()…), …)`，MON-3a）；`usePoolReadable()` 為 false 時（M25）不算件數。`CellView` 跟著加這兩欄。測試：件數與顏色帶到每台；讀不到資源池時沒有件數。
3. **`HostCell` 改寫**（M10、M17–M19、U3）：`components/fleet/HostCell.tsx` 改成畫每台的指標與狀態。徽章 `resolveHostBadge(look, 'terminal')` → `HostBadge`，`host_down` 40%、`unknown` 60%；件數（ok 且 n > 0）用 `CountBadge size="sm"`，灰／`status-warning`／`status-error`；指標：環形 → `ContextRing`（現行 `:162-165`），單柱、雙柱 → `HostBars`（`components/fleet/HostBars.tsx:33-41` 改成一根或兩根）；非 ok → 那一格換成 `LinkBreak`（`status-warning`）／`Power`（`status-error`）／`Question`（`text-muted`），粗細 bold；**刪掉** `:159` 的呼吸綠點。測試（`HostCell.test.tsx`、`HostCell.pool.test.tsx` 合併改寫）：三種形狀 × 代表性資料項；三種非 ok 狀態；件數三種顏色與 99+；沒有 `worker-running-*`。mutation：非 ok 時仍顯示件數；淡化比例對調。
4. **`HostCard` 改寫**（M11）：`components/fleet/HostCard.tsx` 改吃模型：讀數列（含「環」「柱」標記，沿用 `fleet.card.ring_tag` 等鍵或改名到 `pool.card.*`）、下半部各列、非 ok 的卡片（`livenessLines`）。定位與 portal 程式不動。測試改寫 `HostCard.test.tsx`。
- 守門測試（MON-1c 建的）照舊、不加豁免：本期新增的 `indicator.ts` 列進它的範圍。
- **Split**：task 4（卡片）拆成 MON-3c。

### MON-4 — 窄側欄（U1、U2）

依賴 MON-3b。檔案（約 10 個、430 行）。

1. **`HostBars` 支援 N 根**（若 MON-3b 還沒做到）：`bars: (BarReading | null)[]`，寬度與間距可調。測試。
2. **四項狀態小圖**（M14、M16、W5）：新檔 `components/fleet/NarrowHostBlock.tsx`：底色是 `resolveHostBadge(look, 'terminal')` 的 light 層（沒設顏色用中性的 `surface-hover`）；四根直條依序 `cpu`、`mem`、`pressure`、`disk`（`indicatorReading`）；`CountBadge size="sm"` 在右上角；不畫主機圖示；非 ok → 置中的狀態圖示、整塊淡化比照徽章、不顯示件數；滑過浮出 `HostCard`（placement `right`），按一下同寬側欄。尺寸要放得進 44 px 的窄側欄（現在窄格 `HostCell.tsx:149` 是 `gap-0.5 px-0.5`、約 40 px），先做 32 × 24 px，最後的 px 由 lead 看截圖定。測試：四根的讀數與顏色、底色來自外觀與中性底、件數的位置與 test id、三種非 ok、滑過與按一下。
3. **兩個色塊＋數字**（M15）：新檔 `components/fleet/QueueCounts.tsx`：重（`bg-queue-heavy`）、輕（`bg-queue-light`）各一塊，數字是 `queueStripOf(entries, nseg, usePoolReadable())` 的 `counts`；0 件那塊 `bg-surface-hover`、照寫 0；**讀不到資源池時兩塊都是灰底 0**（M25）；永遠畫出來；滑過用 MON-3a 的 `QueueTooltip`，按一下開任務佇列。測試：數字（含 0／0）、顏色、不收起、按一下；每個來源都失敗而 jobs store 仍有舊工作 → 兩塊灰底 0。
4. **掛上去**：`FleetCells.tsx` 窄側欄：上方 `<QueueCounts />`，每台一塊 `NarrowHostBlock`（直排、間距 0）。測試。

## 第二波（只寫到期；開工前 plan 再細化一版）

### MON-5 — 任務佇列分頁（M26–M34、M40 的沙漏）
- **目標**：`watch-jobs` 分頁改成「佇列池／工作池＋路線」，靜態；原因與為什麼在等；摘要列與最近結束；`started_at`；沙漏入口。
- **主要檔案**：`components/watch/jobs/JobsPane.tsx`、`JobLanes.tsx`、`JobQueue.tsx`（換成新的 `QueueFlow`）、`lib/pool/jobs-lanes.ts`（佇列池格、工作池格、M31／M32 的原因：`headroom_reasons`＋exceptions 的 holds＋名額）、`jobs-types.ts`／`jobs-parse.ts`（`started_at?`）、新的 `lib/pool/queue-routes.ts`（純幾何：同一行一條曲線；換行時沿行間空隙與兩側走，不穿過格子，照設計稿 `routeD`）、`QueueRoutes.tsx`（SVG 疊層，`ResizeObserver`＋`document.fonts.ready` 重算，hover 只高亮、不變暗，排隊中的畫虛線到能接的主機）；沙漏：`components/BackgroundSymbol.tsx` 一帶加按一下 → `openWatchArea({ tab: 'jobs' })`＋`writeSelectedJob`。`JobHistory`、`JobDetail`、`JobMenu` 沿用。
- **依賴**：MON-1a、MON-1b（M24 的「讀不到」）、MON-3a（`pool-jobs`、`queue-strip`）；MD-5（766）。
- **大小**：兩期——5a 版面與資料、5b 路線與 hover，各約 700 行。可與 MON-6～MON-10 並行（不碰同一批檔）。

### MON-6 — 主機頁骨架與「監控」（M41–M43、A7、W2）
- **目標**：6a 換 tab id 與分頁跟著 pool；6b 主機頁的殼（標頭、四個子分頁、範圍、橫幅 M22）與「監控」三段。**拿掉 `WatchHostPane` 的 fleet 讀取**（A1 的過渡例外到此結束：快照、主方、`fleet-bridge.ts` 的接法都不再用），並把側欄的守門測試範圍**擴到主機頁**（`components/watch/**` 與主機頁的新檔）；`fleet-bridge.ts` 之後只剩 Hosts 頁在用。
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
- **主要檔案**：9a：新的 `components/watch/host/SettingsTab.tsx`、`WorkerSettings.tsx`、`WorkerExceptions.tsx`、`WatchSettings.tsx`（watch 列整段拿掉）、新的 `lib/pool/push-switch.ts`（`GET /api/hostconfig`、`PUT /api/hostconfig/wwd_alerts`）、`HostColorField.tsx`／`HostIconField.tsx`（沿用）、`TerminalSection.tsx`。9b：刪 `components/hosts/` 的 `PoolHostsSection`、`PoolHostGroup`、`PoolHostPage`、`PoolHostRoute`、`PoolStatusDot`、`ProbeHostsSection`、`ProbeHostPage`、`ProbeHostEditor`、`ProbeHostForm`、`ProbeCheck`、`probe-rows.tsx`、`use-probe-rows.ts`、`useProbeEditor.ts`、`FleetRowEditor.tsx`、`pool-status-word.ts`，`lib/pool/use-pool-host-rows.ts`、`lib/pool/fleet-bridge.ts`（最後一個讀者走了）；改 `HostSidebar.tsx`、`OverviewSection.tsx`、`components/HostPage.tsx`。
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
- **目標**：SPA 不再讀 `fleet.v1`、`gate.decisions.v1`；`watch.v1` 只留守門員的 status 與 wake（88 定案 W9：保留 `/api/watch/status`、`/api/watch/wake` 的讀者——守門員頁與盾牌——到 8b 的 incident 路由上線才拿掉）。
- **主要檔案**：刪 `lib/fleet/*`（store、ws、api、history、config-*、probe-*、parse、types、`use-fleet-*`、`bar-readings.ts`、`ring-driver.ts`）；`hooks/useMultiHostEventWs.ts:148-150`、`:354-357`（fleet 的訂閱與處理）；`hooks/useTabQueued.ts` 的 fleet 那半；`components/watch/QueueSection.tsx`、`QueueNow.tsx`、`QueueLane.tsx`、`QueueHistory.tsx`；`lib/watch/api.ts` 的 decisions、`decisions-types.ts`；`lib/pool/pool-look-migration.ts`；`cell-model.ts`、`use-cell-models.ts` 剩下的部分；`useUISettingsStore.fleetCellIndicator` 與它的投影（settings ordinal 再升一次）；fallback 與 fleet 的語系鍵。守門測試改成全 repo 不 import `lib/fleet/`。
- **依賴**：MON-5～MON-10；時程跟 1f 的 WWD-9（daemon 端拿掉 `fleet.v1`、`gate.decisions.v1`）對齊。
- **大小**：一～兩期；純刪除只算新增行。

## 順序與分工

- 依賴：MON-1a、MON-1b、MON-2a 互不依賴；MON-1c-0（1a 之後）；MON-2b（1a、2a 之後）→ MON-1c（1a、1b、2b、1c-0 之後）→ MON-1d（1b、1c 之後）與 MON-3a（1a、1c 之後）可並行 → MON-3b（1c、3a 之後）→ MON-4（3b 之後）。MON-3a 不與 MON-1c 並行（兩期都改 `FleetCells` 的接線）。
- 建議兩位 Sonnet member：
  - 甲：MON-1a → MON-1c-0 → MON-2b → MON-1c → MON-3a → MON-3b → MON-4。
  - 乙：MON-2a → MON-1b → MON-1d（等 1c merge），之後接第二波（MON-10 或 MON-5 的細化）。
  - 只有一位時照 1a → 1c-0 → 1b → 2a → 2b → 1c → 1d → 3a → 3b → 4 疊分支做。
- 第二波：MON-5、MON-6、MON-10 可同時開；MON-7、MON-8、MON-9 在 MON-6 之後並行；MON-11 等 A 線；MON-12 最後。
- 改到 team 模組的不在本線範圍；本線全是純 SPA，88 自己放行（B 線 10-19 起的規則），bump 與部署歸 1f。MON-2a 升 settings ordinal，88 放行前先告訴 1f（同 TG-1a-1）。

## 待查與已定案

- **iOS（待查）**：purdex-ios 對 settings ordinal 13 與新 store 的反應（MON-2a task 4）。
- **88 定案（10-20）** W1–W9（spec §7.3），落在這些 task：W1 → MON-2a task 1、MON-2b task 1；W2 → MON-2b task 2、MON-6a；W3 → MON-1b task 2；W4 → MON-1c task 2、MON-3a task 3；W5 → MON-4 task 2；W6 → MON-3a task 5；W7 → MON-2b task 1、MON-9a；W8 → MON-9a；W9 → MON-11、MON-12。
- **A 線（不擋第一波）**：兩種失聯推播開關的寫入 API 由 88 請 1f 排（A 線小項，W8），上線前 MON-9a 唯讀顯示現值；series 補 `slots`、PSI、`sessions_room`、常駐程式（spec §3 最後一段）由 1f 排。

## codex plan 審查（task-mvgk4yis-tsgk70）對照表

88 全部採納（10-20）。

| # | 嚴重度 | 內容 | 處理 |
|---|---|---|---|
| 1 | critical | 整個資源池讀不到時佇列條要整行灰；jobs store 留著的舊工作會讓條還亮著 | MON-3a task 2 加「pool 可讀」閘門 `usePoolReadable()`（`sourceHostId !== null`），task 3 `queueStripOf` 吃它、讀不到時回空，task 6 `QueueStrip` 吃它；MON-4 窄側欄兩色塊灰底 0；測試加「所有來源失敗、jobs store 仍有舊工作」。明寫 JobsPane 保留已讀工作的行為不沿用。spec M25、M15 同步 |
| 2 | important | 第一波的側欄沒有真的不讀 fleet | MON-1c 改成「側欄只讀 pool」：主機格模型只由 pool hosts＋MON-2b 的順序與外觀產生，不收 fleet views、不留 pool 沒列的主機；fleet 接法純搬移到 `fleet-bridge.ts` 給主機頁與 Hosts 頁；守門測試移到 MON-1c、`cell-model.ts`／`use-cell-models.ts` 不豁免，唯一豁免是搬遷檔。fleet 接法的搬移獨立成 MON-1c-0（純搬移），原 MON-1c 的主機頁與 Hosts 頁部分改成 MON-1d。主機頁的 fleet 讀取明定為過渡例外（spec A1），MON-6 拿掉並把守門測試擴到主機頁 |
| 3 | important | 一次性搬遷可能晚於 Profile Sync 的第一次 build | MON-2b task 2 改成 `bootPoolLooks()` 併進 `main.tsx:95` 的開機鏈、排在 `startProfileSync` 之前；等 pool 第一次回答與 fleet 第一份不 stale 的快照，10 秒逾時就放行、放行後這次不再搬、下次開機再試、3 次逾時放棄；`main.test.tsx` 證明第一次 collector 不早於搬遷 |
| 4 | important | 缺「本機 dirty、SOT 是 ordinal 12」的整合測試 | MON-2a task 3 照 `task-groups.integration.test.ts:239-273` 加兩案：乾淨 → 清空並升級一次；dirty → `locked:conflict`、本機外觀與順序保留 |
| 5 | important | MON-3a 與 MON-1c 同改 `FleetCells` 接線，不能並行 | MON-3a 依賴改成 1a、1c；分期總表（spec §8）與依賴圖（本 plan「順序與分工」）一起改 |
| 6 | important | store 的 key 清洗太鬆 | 改成 wwd 產生的格式 `^[0-9a-f]{16}$`（`cmd/wwd/main.go:351-355`）；測試拒 alias 與 Purdex daemon 的 host id；寫明已知限制 |
| 7 | minor | MON-2a 的既有測試清單漏了守門 snapshot | 加 `projections.test.ts:813-835` 的 fingerprint／ordinal inline snapshot，跟 ordinal 同一個 commit 改 |
| 8 | minor | 基準 SHA 過時 | spec 與 plan 改成 `origin/main 86ced9555`；`abaa46be5..86ced9555` 沒有改到引用的檔案，第一波的 file:line 已重新核對 |
