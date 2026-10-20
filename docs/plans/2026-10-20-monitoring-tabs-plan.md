# 監控分頁組＋側欄主機列 — plan

> spec：`docs/specs/2026-10-20-monitoring-tabs-spec.md`（下文的 M、A、W、P 編號都是 spec 的）。B 線 lead 88，2026-10-20。
> 一期一張 PR（≤ 800 行、≤ 20 檔，測試也算）；一個 task 一個 commit，先寫測試、看它紅，再實作。執行者：Sonnet member。
> 第一波（MON-1a～MON-4）細到 task；第二波的 MON-5、MON-6、MON-10 在修訂 2 細到 task；MON-7～9、11、12 仍只寫到期，**開工前 plan 再細化一版**（含 file:line 與測試案例），細化版先給 88 看。
> spec 的 W1–W9 已由 88 定案（10-20，spec §7.3），本 plan 照定案寫。
> 行號以 `origin/main 93034abc5` 為準（修訂 2、3 核對；第一波的 file:line 是在 `86ced9555` 核對的，`86ced9555..93034abc5` 之間 merge 了 MON-1a～1d、2a、2b、3a、3b 與 768 的 bump，還沒 merge 的 MON-4 開工時照舊再對一次），開工時以當下的 `origin/main` 再核對一次。
> 修訂 1（10-20）：照 codex plan 審查（task-mvgk4yis-tsgk70）的 8 條修正，88 全部採納；對照表在文末。
> 修訂 2（10-20）：第二波 MON-5、6、10 細化（MON-5 拆 5a～5c、MON-6 拆 6a～6f（修訂 3 再拆成 9 張）、MON-10 拆 10a～10c，理由寫在各期）；MON-7、8、9、11、12 的依賴與範圍跟著調整；細化時查到的 8 個待確認點，88 定案（10-20）全部照 plan 的寫法，記在「待查與已定案」；spec 同步改了 §8 的分期表、M31 的例字與 A7 的欄位名。
> 修訂 3（10-20）：照 codex 第二波 plan 審查（task-mvgu8cfz-t8p13g）的 8 條修正，88 全部採納；MON-6 改成 6a-1、6a-2、6b、6c、6d-1、6d-2、6e、6f-1、6f-2 九張，MON-10c 改依賴 6f-2；對照表在文末。

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
   - `usePoolHostLook(hostId)`：有 daemon（用 `usePoolHostOwners`：Nex 快取曾指名這個 `host_id` 的 App 主機，不管現在連不連著、快取新不新，重複時主機順序在前的贏；不用要求連線的 `usePoolHostDaemons`，否則外觀歸屬會跟著重連跳動。owner 還不知道時寫進新 store 的顏色圖示，在 owner 的外觀有顏色或圖示之前照樣顯示；owner 一旦有，下一次改外觀就把新 store 裡那份清掉，指標留著）→ `useHostLookResolver()(hostConfigId)` 的 `colors`／`color`／`icon`／`iconWeight`（`lib/host-look.ts`；不直接讀 HostConfig 的外觀欄位，`host-look.guard.test.ts` 管著）；沒有 → 新 store。
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

1. **指標的讀數**（M8）：新檔 `lib/pool/indicator.ts` `indicatorReading(item, facts, load, zone)` → `{ pct; tone }`（七個項目，門檻照 M8；`load` 依區域上色，`unknown` 灰；壓力等級 0 改看記憶體使用率；`sysload` 是 load1 ÷ 核心數，70 黃、100 紅；`slots` worker 關著灰、滿了黃）。門檻與原 `lib/fleet/bar-readings.ts` 相同；該檔已在 MON-1c-0b 搬到 `lib/pool/bar-readings.ts`（守門範圍內、MON-12 不刪），`indicator.ts` 直接 import 它的 CPU、記憶體、壓力讀數。測試：每個項目的表格，含邊界。mutation：各門檻的比較符號。
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

## 第二波

> 修訂 2：MON-5、MON-6、MON-10 細到 task（照第一波的寫法）；MON-7～9、11、12 仍只寫到期，開工前再細化一版。三條線（任務佇列、主機頁、事件）可同時開，各自的期依序疊；跨線的依賴寫在各期。
> 第二波的共通事項：
> - **守門測試**：每期新增的 `lib/pool/*.ts` 加進 `no-fleet-reads.guard.test.ts` 的 `POOL_FILES`（同 MON-3a #3783 的做法）；元件的範圍在 MON-5b（`components/watch/jobs/**`）、MON-6d-2（`components/watch/host/**`）、MON-6f-2（主機頁其餘的檔、`WatchSettings.tsx` 與 `watch-area.ts`、`open-watch-area.ts`）、MON-10a／10c（盾牌、事件分頁）擴進去。偵測式只看直接的 import：`lib/watch/queue-time.ts` 這種自己 import fleet 型別（`:3`）、被本線元件拿來用 `mss` 的檔不在範圍，MON-12 拆。
> - **舊畫面因此沒人用的檔**不在本線各期刪（每期 ≤ 20 檔），列在 MON-12。
> - 原因、狀態的字沿用既有的 `pool.reason.*`、`pool.liveness.*`（88 定案（10-20），「待查與已定案」第二波第 8 條）；新字兩個語系都加。
> - 每一期的「檔案」含測試檔與語系檔；行數含測試、不含純刪除。

### MON-5 — 任務佇列分頁（M24、M26–M34、M40 的沙漏）

pane kind 與 tab id 沿用 `watch-jobs`，分頁名改「任務佇列」。歷史（`JobHistory`）、詳情（`JobDetail`）、「⋯」選單（`JobMenu`）沿用 7b1、不改；M33 的取消規則 `JobMenu` 已經是（`JobMenu.tsx:1-2`；`JobMenu.test.tsx:106` 排隊中直接取消、`:123` 跑著的先問），本線只在 5b 的整合測試再斷言一次。
**為什麼拆三期**：原估 5a／5b 各 700 行；細化後資料（純函式＋表格測試）約 560、靜態版面約 720、路線與 hover 約 720，兩期裝不下。
依賴（整條）：MON-1b（`poolHostState`）、MON-1c（`useMonitorHosts`：M13 順序、外觀、W4 留著的清單）、MON-3a（#3783，已 merge：`lib/pool/pool-jobs.ts` 的 `usePoolJobs`、`lib/pool/queue-strip.ts` 的 `queueStripOf`、`components/fleet/QueueStrip.tsx`）；MD-5 在 766。與 MON-6、MON-10 並行（共用的只有語系檔與守門測試的清單）。

#### MON-5a — 佇列流的資料（純函式）

依賴 MON-1b、MON-1c、MON-3a（皆已 merge），可以馬上開。檔案（8 個、約 590 行）：`lib/pool/jobs-types.ts`、`jobs-parse.ts`、`jobs-parse.test.ts`、`lib/pool/jobs-view.ts`、`jobs-view.test.ts`、新檔 `lib/pool/queue-flow.ts`＋`queue-flow.test.ts`、`components/fleet/no-fleet-reads.guard.test.ts`（`POOL_FILES` 加 `queue-flow.ts`）。

1. **`job.started_at`**（MD-5，M34）
   - 測試先行（`jobs-parse.test.ts`）：RFC 3339 → ms；缺席、`null`、壞掉的時間 → 欄位缺席；`result.started_at` 不受影響；現有 fixture 照樣解得開。
   - 實作：`jobs-types.ts:30-57` `PoolJob` 加 `started_at?: number`（共通規則：新欄位可選）；`jobs-parse.ts:67-84` 用 `time(v.started_at)`，非 null 才帶（寫法同 `:72` 的 `slot`）。MON-6e 的「在這台執行」也讀它。
   - mutation：拿 `result.started_at` 當它；壞時間當 0。
2. **工作池格與名額格的「不接」**（M27、M31）
   - 型別：`Busy = { kind: 'worker_off' } | { kind: 'paused'; name: string; until: number | null } | { kind: 'code'; code: string } | { kind: 'full' } | { kind: 'unreadable'; state: PoolLiveness }`；`SlotRow = { slot: 'heavy' | 'light'; total: number | null; jobs: PoolJob[]; busy: Busy | null }`；`WorkerCell = { hostId; alias; state: PoolHostState; heavy: SlotRow; light: SlotRow }`。
   - `workerCellsOf(hosts, active)` → `{ cells: WorkerCell[]; off: string[] }`：worker 開著（ok 讀 `facts.worker.enabled`，其他讀 pool 列的 `worker.enabled`）**或手上有工作**的主機一格（`laneModels` 的「lane 不藏工作」，`jobs-lanes.ts:24-41`；剛關掉 worker 時正在跑的會跑完，M47）；`off` 是其餘主機的 alias（M27 下面那一行）。工作依 `job.slot ?? 'heavy'` 進重列或輕列，各列執行中在前、準備中在後（`lanes()`，`jobs-view.ts:57-69`）；`total` 在 ok 時是 `facts.worker.slots[slot]`，不 ok 時 `null`（名額不明，M17）。
   - `busyOf(host, slot, active)`，依序：狀態不是 ok → `unreadable`；worker 關 → `worker_off`；`headroom_reasons[slot]` 的第一個 `exception:<name>` → `paused`（`until` 取 `facts.exceptions` 同名那筆，`types.ts:15`）；其他代碼（去掉 `worker_off`、`exception:`）的第一個 → `code`；占用 ≥ `total` → `full`；否則 null。
   - 測試：M31 的例子（mlab 暫停到 16:21（watcher-766）→ 重列 `paused`、`until` 對）；名額滿 → `full`；worker 關但手上有一件 → 有格、`worker_off`；worker 關、沒工作 → 在 `off`；`slot: 'light'` 進輕列、缺席進重列；`wwd_down` 的主機手上有工作 → 有格、`total` null、`unreadable`（M24 的那一分鐘）；沒有 `headroom_reasons`（舊 daemon）→ 只看名額。
   - mutation：`until` 取錯筆；`>=` 改 `>`；worker 關的主機手上有工作時被藏起來。
3. **佇列池格**（M24、M26、M32）
   - 「這台能不能接這件」只有一個寫法：`jobs-view.ts:79` 的條件（worker 開著**而且**標籤都有）抽成同檔匯出的 `hostCanTake(job, host)`，`noHostToGoTo`（`:77-81`）改用它，行為不變（`jobs-view.test.ts` 照過，加 `hostCanTake` 的四格表：開／關 × 標籤符合／不符）；這裡的候選與 5c 的虛線都用它。
   - 型別（`queue-flow.ts`）：`FlowHost = { hostId; alias; state: PoolHostState }`（`useMonitorHosts()` 的一台，已照 M13 排好）；`PoolCell = { hostId; alias; readable: boolean; rows: QueueEntry[]; longestS: number | null; away: number; whys: WhyLine[] }`；`WhyLine = { why: Why; n: number; bad: boolean }`；`Why = { kind: 'held' } | { kind: 'no_host'; labels: string[] } | { kind: 'busy'; hosts: { alias: string; busy: Busy }[] }`。
   - `poolCellsOf(hosts, entries, active)`：每台一格、照 `hosts` 的順序；`rows` 是這台送出的在排工作，順序就是 `queueEntries`（`jobs-lanes.ts:57-63`）的顯示順序（保留的本來就在最後、`held` 為真）；`longestS` 只看非保留的；`away` 是這台送出、正在任一台準備或執行的件數（設計稿的「・k 在跑 ↓」）；**狀態不是 ok 的主機 `readable: false`、`rows` 與 `whys` 都空**（M24：工作在那台的 wwd 裡，jobs store 留著的舊工作不畫）。
   - 每件的 why：保留 → `held`；`entry.noHost`（`jobs-view.ts:77-81` 的紅線）→ `no_host`（`bad`）；否則候選＝`hostCanTake` 為真的主機，**每一台**的 `busyOf(host, slot, active)`（task 2）都不是 null → `busy`（依候選的順序）；有一台能接 → 沒有這行（只是在等順序）。相同的 why 合併、`n` 計件（畫面在格子裡不只一件時寫「N 件等：…」，照設計稿 `poolCell`）。
   - 測試（表格）：M32 的例子（mlab 8 件，mlab 暫停到 16:21、air26 閘門的名額被占住（`gate_full`）→ 一行、`n` 8、兩台的 busy）；M24（mserv `wwd_down`：`readable` false、jobs store 裡 mserv 送出的舊在排工作不出現）；`no_host` 是 bad；保留的在 rows 最後、不算 longest；`away` 跨主機；rows 與 `queueEntries` 同序。
   - mutation：非 ok 仍列 rows；合併時不計件；候選只要一台 busy 就寫 busy（應該是全部）。
4. **摘要、計時與最近結束**（M34）
   - `flowSummaryOf(entries, jobs)` → `{ queued; heavy; light; running; preparing; held; longestS: number | null; long: boolean }`：`queued`／`heavy`／`light` 直接取 `queueStripOf(entries, 10, true).counts`（同一套算法；任務佇列頁照它自己「保留已讀工作」的規則，不套側欄的 `poolReadable` 閘門）；`long` 用 `WAIT_LONG_S`（`lib/watch/queue-time.ts:11`）。
   - `elapsedSince(job)`：`running` → `started_at ?? claim.since`；`claimed` → null（畫「準備中」）。
   - `recentEndedOf(jobs)` ＝ `historyOf(jobs, {}).slice(0, 5)`（`jobs-view.ts:95-101`）。
   - 測試：M34 的計數（排隊 N、重 a・輕 b、執行中、準備中、保留）；最久等的 2 分鐘邊界；`started_at` 有與沒有；最近結束取 5 件、新的在上。
   - mutation：永遠用 `claim.since`；保留算進 `queued`。
- **Split**：task 4 移到 5b。

#### MON-5b — 佇列流的版面（靜態）與分頁名

依賴 MON-5a、MON-3a。檔案（12 個、約 720 行）：`components/fleet/QueueStrip.tsx`、新檔 `components/watch/jobs/QueueFlow.tsx`＋`QueueFlow.test.tsx`、新檔 `components/watch/jobs/RecentEnded.tsx`、`JobsPane.tsx`、`JobsPane.test.tsx`、`JobsPane.tab-switch.test.tsx`、`lib/pool/jobs-view-memory.ts`＋`jobs-view-memory.test.ts`、`locales/zh-TW.json`、`en.json`、`no-fleet-reads.guard.test.ts`（glob 加 `components/watch/jobs/**`）。

1. **節條抽出**：`QueueStrip.tsx` 畫節的那段（`:32` 的 `kinds` 與 `:48-54` 的 `kinds.map`）抽成同檔匯出的 `QueueSegments({ strip, nseg })`，`QueueStrip` 改用它、照舊傳自己的設定（`:19` 的 `useLayoutStore.queueStripSegments`，MON-3b），行為不變（`QueueStrip.test.tsx` 照過）；任務佇列的摘要列固定傳 10 節（M34）。
2. **`QueueFlow`**（M27、M28、M31、M32、M34 的摘要列）
   - 摘要列：排隊 N（重 a・輕 b）｜執行中｜準備中｜最久等（`long` 時 `text-status-warning-text`；沒有在排寫「—」）｜保留 N，右端 `QueueSegments`（10 節）。
   - 佇列池（`Stack` 圖示、「工作留在送出那台」）：一排 `flex-wrap`，每台一格 `data-pool={hostId}`；標頭是徽章（`resolveHostBadge(look, 'console')`，14 px）、alias、「N 在排・最久 m:ss」或「沒有在排」、`away` > 0 時「・k 在跑 ↓」；每件一列 `data-q={jobKey}`：類別、`jobLine`（truncate、title 是全文）、`mss(created_at)`（`long` 黃）；保留的淡化加「保留」；最下面是 why 行（`bad` 用 `text-status-error-text`）；`readable` false → 只寫「讀不到（{狀態字}）」。格子寬度隨件數（照設計稿的 `--g`：有件數 1.2＋min(4, n)×0.25、沒有 0.7，另設 `min-w`）。
   - 工作池（`HardDrives`、「格子是名額；徽章是來源」）：每格 `data-wk={hostId}`：標頭是徽章、alias、區域字（ok）或狀態字、「重 u/s・輕 u/s」（`total` null 寫「—」）；重、輕各一列：工作卡 `data-j={jobKey}`（來源主機的徽章 13 px、`jobLine`、執行中 `mss(elapsedSince)`、準備中寫「準備中」並用虛線框），空名額寫「空」，`busy` 時寫「不接」；`busy` 且還有空名額時，下面一行「重：{原因}」（`text-status-warning-text`）。`off` 不空 → 「{a}、{b} 的 worker 關著，打開後出現在工作池」。
   - 原因的字：`worker_off`／`code`／`full` → `reasonWord`（`words.ts:13-16`；`full` 用 `slots_full`）；`paused` → 新鍵 `watch.jobs.flow.paused_until`「暫停到 {{time}}（{{name}}）」（`until` 是 null 時用既有的 `pool.reason.exception`）；`unreadable` → `livenessWordOf`（`words.ts:59`）。why 行：`held`「保留中」、`no_host`「沒有開著的 worker 符合 {labels}」（沒有標籤時「沒有開著的 worker」）、`busy`「{alias} {原因}」以「・」相連；前綴「N 件等：」。
   - **靜態**（M28）：不用 `animate-*`、`transition-*`；執行中的卡沒有呼吸點（`JobLanes.tsx:28` 那顆不搬）。卡片與列是按鈕，按一下選取（`aria-pressed`，同 `JobQueue`），詳情開在旁邊（`JobsPane.tsx:92`）。
   - 測試（`QueueFlow.test.tsx`）：M27（mserv worker 打開 → 工作池多一格；關著 → 在 off 那行）；M28（整棵樹沒有 `animate-`／`transition` 的 class）；M31（不接＋原因行）；M32（why 行、紅字）；M34 的摘要；M24 的「讀不到」格；`data-pool`／`data-wk`／`data-j`／`data-q` 都在（5c 量位置用）；按一下選取、再按放掉。
3. **最近結束**（M34）：新檔 `RecentEnded.tsx`，5 件：結束時間（`whenText`）、結果（`CheckCircle`／`XCircle`＋`resultText`）、`jobLine`、來源徽章 → 執行主機的徽章與 alias、跑多久（`result.started_at` 到 `finished_at`）、「{類別}・{重｜輕}」。不放用量欄（G16）。測試併在 `QueueFlow.test.tsx`。
4. **`JobsPane` 換上**：`JobsPane.tsx:80-89` 的「佇列與執行中」換成 `<QueueFlow>`＋`<RecentEnded>`；主機來自 `useMonitorHosts()`（順序、狀態、外觀；整個資源池讀不到時是 W4 留著的清單、每台 `unknown` → 每格「讀不到」），工作來自 `usePoolJobs()`（MON-3a；保留已讀工作的規則照舊）；5a 的四個函式以 `(jobs, now, hosts)` memo。拿掉 `:74` 的 `jobs-count`（摘要列取代），`:76` 的錯誤行、`:77` 的 `jobs-no-pool`、歷史模式不動。`JobLanes`、`JobQueue` 這期之後只剩 `HostJobs` 在用（MON-6e 換掉；兩期中晚 merge 的那張刪這兩個檔）。測試：`JobsPane.test.tsx:55-142`（lanes、queue 兩段）改寫成 flow 的版本，其他段不動；加一案 M33：排隊中的「⋯ → 取消」直接送、執行中的先問（經真的 `JobDetail`）。
5. **分頁名「任務佇列」**：`zh-TW.json:3222` `watch.pane.jobs`「工作」→「任務佇列」、en "Job queue"；`watch.jobs.mode_label`「工作檢視」→「任務佇列檢視」。`watch-panes.test.ts` 斷言的是鍵，不必改。
6. **tab-hosted**：「佇列與執行中」的捲動位置放 `jobs-view-memory.ts`（`:14-18` 加 `liveScroll` 與 `readLiveScroll`／`writeLiveScroll`，`:36` 一併清掉）；`JobsPane` 掛上時還原。`JobsPane.tab-switch.test.tsx` 加一案：真的 `TabContent`，捲到下面、選一件執行中的工作，切到別的分頁再切回 → 捲動位置與選取都在。
- **Split**：task 3 移到 5c。

#### MON-5c — 路線與 hover、沙漏入口

依賴 MON-5b。檔案（9 個、約 740 行）：新檔 `lib/pool/queue-routes.ts`＋`queue-routes.test.ts`、新檔 `components/watch/jobs/QueueRoutes.tsx`＋`QueueRoutes.test.tsx`、`QueueFlow.tsx`、`QueueFlow.test.tsx`、`components/BackgroundSymbol.tsx`、`BackgroundSymbol.test.tsx`、`no-fleet-reads.guard.test.ts`（`POOL_FILES` 加 `queue-routes.ts`）。

1. **路線的幾何**（純函式；移植設計稿 `page.js:632-692` 的 `qLayout`／`roundPath`／`routeD`／`drawRoutes`；M30）
   - 輸入 `FlowLayout`：`box`（寬高）、`top`／`bot`（兩排的框）、`topLines`／`botLines`（每排的行：頂邊相差 2 px 內的格子算同一行）、`pools`／`workers`／`cards`（各格、各卡的框，相對於 flow）。全是數字，不碰 DOM。
   - `routeD(L, p, w, lane)`：佇列池在上排最後一行、工作池格在下排第一行 → 一條三次曲線；否則從格子底下進它那一行下方的空隙 → 走到比較近的一側的外側通道 → 沿通道下去 → 進工作池格那一行上方的空隙 → 到格子頂邊（不穿過任何格子）；共用一個空隙的路線依 `lane` 錯開（`((lane % 5) − 2) × 3`、`((lane % 3) − 1) × 2.5`），兩側通道各自計數錯開。
   - `routesOf(L, active)`：每件準備中或執行中的工作一條，`key = jobKey`；同一個佇列池出發的依卡片的 x 排序，起點沿格子底邊均分（`l + w·(k+1)/(n+1)`）；終點是卡片左緣 +18、同一格的第 i 條再 +34·i。`candidatesOf(L, poolHostId, workerHostIds)`：排隊中的工作 hover 時的虛線，起點佇列池格底邊中點、終點每個給定工作池格的頂邊中點。
   - 測試（框用字面值）：兩排都一行 → 一個 `C`、端點在兩格的邊上；上排折成兩行、佇列池在第一行 → 路線進第一、二行之間的空隙再走外側通道，**沿路線取樣的點沒有一點落在任何格子的框內**；兩條共用空隙的路線 y 不同；到同一格的終點相距 34；`candidatesOf` 只畫到給它的格子。
   - mutation：拿掉 `lane` 錯開（共用空隙那案轉紅）；左右側選反；不判斷折行、一律畫曲線（穿格子那案轉紅）。
2. **疊層與重算**（M29、M30）
   - `QueueRoutes({ flowRef, flow, hover, colorOf })`：flow 裡一層 `absolute inset-0 pointer-events-none` 的 SVG；量 `[data-pool]`／`[data-wk]`／`[data-j]` 與兩排的框（`getBoundingClientRect`，相對 flow）；`ResizeObserver(flow)`（同一個 animation frame 只算一次）、`document.fonts?.ready`、flow 的進行中工作或格子變了（`useLayoutEffect`，依它們的 key 字串）都重算；卸下時 disconnect。
   - 線：來源主機的主色（`resolveHostColors(look, 'console')?.main`；沒設顏色用 `text-text-muted` 的 currentColor）、平常淡（`stroke-opacity` 約 .35）、兩端各一個小點。沒有 `<animate>`、沒有 CSS transition（M28）。
   - 測試（jsdom 沒有版面：依 `data-*` mock `getBoundingClientRect`、stub `ResizeObserver` 與 `document.fonts`）：N 件進行中 → N 個 `g[data-r]`；觸發 resize 的 callback → `d` 重算；`fonts.ready` resolve → 重算；卸下 → disconnect；沒有 `animate`／`transition`。
3. **hover**（M29、P18）
   - hover 的對象放 `QueueFlow` 的 state（短暫的指標狀態，不是讀者的選擇，不放 view memory）；路線重算時保留（設計稿 `if (Q_HOVER) qFocus(Q_HOVER)`）。
   - 滑到工作卡 → 只有它的路線 `data-hl`（加粗、不透明、畫在最上面），它的佇列池格與工作池格加外框；**其他路線、卡片、格子的 class 與屬性一律不動**（其他不變暗，使用者 10-20）。滑到排隊中的列 → `candidatesOf` 的虛線（`stroke-dasharray`）到每個 `hostCanTake`（5a，`jobs-view.ts:79` 的完整條件：worker 開著而且標籤都有）為真的工作池格；沒有能接的就不畫。滑到佇列池格或工作池格的標頭 → 那一格相關的路線全部 `data-hl`。離開 → 清掉。
   - 測試：滑到一張執行中的卡 → 只有它的路線 `data-hl`，**其他每條路線、其他每張工作卡（執行中、準備中、排隊中的列）、其他每個佇列池格與工作池格的 class 與屬性，都與 hover 前逐一相同**（M29：其他不變暗；被 hover 的那件自己的兩格加外框不算）；滑到排隊中的列 → k 條虛線（k＝`hostCanTake` 為真的格數）；**worker 開著但標籤不符的格不畫虛線**（P18）；worker 關著的格不畫；標頭 → 那一格的全部；離開清掉。
   - mutation：把其他路線調淡（「其他不動」那案轉紅）；替其他工作卡加一個 dim 的 class（同一案轉紅）；候選只看 `worker.enabled`、不看標籤（標籤不符那案轉紅）；虛線畫到 worker 關著的格。
4. **沙漏入口**（M34、M40）
   - `BackgroundSymbol.tsx:32-38` `QueuedSymbol`：`waiters` 裡有工作（`isJobMark`，`queued-sessions.ts:46`）時，符號接 `onClick` 與 `onPointerDown`，兩者都 `stopPropagation`（分頁本體是帶 dnd `listeners` 的按鈕或 `role="tab"`，`SortableTab.tsx`；側欄的 `InlineTab` 同），按下去 `writeJobsMode('live')`、`writeSelectedJob(jobKey(第一件.origin, 第一件.jobId))`、`openWatchArea({ tab: 'jobs' })`。「第一件」是 marks 的順序（執行中 → 準備中 → 排隊中依名次，`queued-sessions.ts:52-75`）。只有舊閘門的 waiter 時不能按（照舊）。不做成巢狀的 `<button>`（分頁本身就是按鈕）。
   - 測試：按一下 → `openWatchArea` 收到 `{ tab: 'jobs' }`、選取寫進 view memory、外層的 `onClick` 沒被呼叫；`pointerdown` 不往上傳；只有 waiter → 按了沒事。
- **Split**：task 4 獨立一張（5d）。

### MON-6 — 主機頁骨架與「監控」（M12、M22、M41–M43、A7、W2、#3785）

**為什麼拆九張**：原估兩期各 700 行。細化後：分頁換 id 實際碰到 27 個檔（含 `open-watch-area.{,return,jobs,events}.test.ts`、`HistoryPage.watch.test.tsx`、`useTabDisplay.watch.test.ts` 這些用 fleet 快照或 `{ host }` 入口建主機分頁的測試）；主機頁現在的量表、圖表、行程表與它們的 lib 都 import `lib/fleet/`（`Gauge.tsx:3`、`MetricChart.tsx:5`、`ProcTable.tsx:4`、`lib/watch/metrics.ts:4-5`、`series.ts:3`、`view-memory.ts:4`；`lib/pool/history.ts:4`、`:68`、`:114` 與 `use-series-history.ts:6` 把讀數塞進 `lib/fleet/history.ts` 的 `fleetHistory`），守門測試擴到主機頁就不能沿用，讀數記憶、量表、小圖、「監控」的元件都要新寫（合計約 1900 行）；換殼另要改 `WatchSettings`（拿掉 fleet／probe 分支）與三個用舊頁的 tab-switch 測試。照 ≤ 800 行／20 檔切（修訂 3，codex 第 6、7 條）：
- 分頁：6a-1（讀者先認得 `poolHostId`，行為不變）→ 6a-2（reconciler 與入口換過去）→ 6b（順序、拖曳、徽章）。
- 主機頁：6c（讀數記憶）→ 6d-1（量表與小圖的資料模型）→ 6d-2（`MiniChart`）→ 6e（「監控」元件）→ 6f-1（子分頁記憶、入口、`WatchSettings`）→ 6f-2（換殼）。
- 6a-1、6c 互不依賴，可並行；6f-1 要 6a-2 與 6c，6f-2 要 6f-1 與 6e。
「監控」以外的三個子分頁在 6f-2 先放過渡內容（6f-2 task 2），MON-7、8、9a 換成正式的。#3785（非 ok 的主機仍帶 fleet 的 `slow` 佇列與行程表）在 6e、6f-2 處理。

#### MON-6a-1 — 主機分頁的讀者先認得 `poolHostId`（A7，行為不變）

依賴 MON-1c、MON-1d。可與 6c 並行。檔案（12 個、約 300 行）：`lib/watch/ids.ts`、`types/tab.ts`、`lib/pane-labels.ts`、`hooks/useTabDisplay.ts`、`hooks/useTabDisplay.watch.test.ts`、`lib/pane-utils.ts`、`lib/watch/watch-panes.test.ts`、`lib/pool/use-monitor-hosts.ts`、`lib/pool/fleet-bridge.ts`、`fleet-bridge.test.ts`、`components/watch/WatchHostPane.tsx`、`WatchHostPane.pool.test.tsx`。這期還沒有任何分頁帶 `poolHostId`（6a-2 才建），所以畫面不變；拆在前面是為了 6a-2 一建新分頁，標籤、比對與主機頁都已經讀得懂。

1. **id 與 pane content**
   - `ids.ts:11-25`：`watchHostTabId(poolHostId)`；`fleetHostIdOfTabId` 改名 `hostIdOfTabId`（只取字尾；呼叫端只有 `watch-area.ts:228`，同一個 commit 改名）。
   - `types/tab.ts:155-160`：`{ kind: 'watch-host'; poolHostId?: string; fleetHostId?: string }`。`poolHostId` 是 wwd 的 `host_id`；**名字照 `:155-156` 的理由不叫 `hostId`**（免得通用的 pane 讀者把它當 App 主機；88 定案（10-20））；`fleetHostId` 只有 MON-6a-2 以前建的舊分頁有（會被收掉）。兩個都可選：同步進來的舊 content 沒有 `poolHostId`，讀的地方一律判斷。tabs 區只檢查 `kind` 是字串（`lib/profile/sections.ts:167-172`），host id 換算只動 `tmux-session` 等幾種（`lib/profile/host-identity.ts:510-531`），**不升 tabs 的 ordinal、不進換算**。
   - 測試（`watch-panes.test.ts`）：id 來回；`hostIdOfTabId` 對非主機分頁回 null。
2. **標籤與比對**
   - `use-monitor-hosts.ts` 新匯出 `poolHostAliasOf(hostId)`（不是 hook）：pool store 的 hosts → W4 留著的清單（`:34-36` 的 `remembered`）→ `hostId.slice(0, 6)`。
   - `pane-labels.ts:71-72`：有 `poolHostId` → `poolHostAliasOf`；舊 content 照舊讀 fleet 快照的 alias（這期還只有舊分頁，標籤不能變；6a-2 拿掉）。`useTabDisplay.ts:135-136`：有 `poolHostId` 時改訂 `usePoolStore`（選出那台的 alias 字串），舊 content 照舊訂 fleet。`pane-utils.ts:61` 比 `poolHostId ?? fleetHostId`。
   - 測試（`watch-panes.test.ts:16-30` 一帶、`useTabDisplay.watch.test.ts`）：`poolHostId` 的 alias 來自 pool store、來自留著的清單、都沒有時是短 id，pool 的 alias 變了標籤跟著變；舊 content 的標籤不變；兩個同 `poolHostId` 的 content 相同。
3. **主機頁的過渡接法**（6f-2 換殼時刪）
   - `fleet-bridge.ts` 新增 `fleetViewOfPoolHost(views, poolHost, daemons)`：`matchPoolHost`（`:46-49`）的反向：daemon 的 fleet 主機，其 `hostConfigId` 是這台 pool 主機的 daemon → 它；否則 alias 相同；都沒有 → null。
   - `WatchHostPane.tsx:94`：有 `poolHostId` → pool store 找這台 → `fleetViewOfPoolHost` 拿 fleet id；只有 `fleetHostId`（舊分頁、既有的測試）→ 照舊。`:100-103` 的 view memory 用 `poolHostId ?? fleetHostId` 當 key。其他不動（6f-2 整個換掉）；既有七個 `WatchHostPane.*.test.tsx` 用 `fleetHostId` 建 content，照過。
   - 測試：`fleet-bridge.test.ts`（依 daemon、依 alias、都沒有）；`WatchHostPane.pool.test.tsx` 加一案：`poolHostId` 的 content 畫出那台。
   - mutation：反向比對只看 alias（daemon 那案轉紅）。

#### MON-6a-2 — reconciler 與入口換成 pool（A7、M13）

依賴 MON-6a-1、MON-2a、MON-2b。檔案（15 個、約 560 行）：`features/workspace/lib/watch-area.ts`、`watch-area.test.ts`、`open-watch-area.ts`、`open-watch-area.test.ts`、`open-watch-area.return.test.ts`、`open-watch-area.jobs.test.ts`、`open-watch-area.events.test.ts`、`components/HistoryPage.tsx`、`HistoryPage.watch.test.tsx`、`components/hosts/PoolHostPage.tsx`、`PoolHostPage.test.tsx`、`lib/pane-labels.ts`、`hooks/useTabDisplay.ts`、`lib/watch/watch-panes.test.ts`、`hooks/useTabDisplay.watch.test.ts`。`watch-area.{events,jobs,guardian}.test.ts` 直接呼叫 `reconcile(view())`，`view()` 多帶的 `stale` 不是物件字面值、不觸發多餘屬性檢查，不必改。

1. **reconciler 讀 pool**（A7、M13）
   - `watch-area.ts:25-29` `WatchFleetView` → `WatchHostsView { hostIds: readonly string[] }`（沒有 `stale`：pool 的答案就是現在的）；`:263-268` `readFleetView` → `readPoolView()`：`usePoolStore` 的 `sourceHostId` 是 null（W4，或還沒讀到）→ null（主機分頁都不動，同現在沒有 fleet 時）；否則 `orderPoolHosts(hosts, usePoolHostsStore.getState().order).map((h) => h.host_id)`。`:298-302` 的訂閱把 `useFleetStore` 換成 `usePoolStore`＋`usePoolHostsStore`，`:10` 的 import 拿掉。
   - `:216-222` 新分頁的 content `{ kind: 'watch-host', poolHostId: id }`；新的接在後面（照 `order` 排在 6b）。
   - **舊分頁**：content 沒有 `poolHostId` 的主機分頁＝舊版。每次 view 不是 null，對每個舊分頁的字尾 `lastListed.set(字尾, now)`（已有就不動），由 `:224-238` 的規則在 `ABSENT_REMOVE_MS` 後收掉；之後又從同步回來（還沒升級的裝置）也照這樣。原因寫進註解：`:231` 會留下「這台從沒列過」的分頁，升級（重開）之後沒有一台裝置列過 fleet id，不這樣做舊分頁永遠收不掉。88 定案（10-20）：接受還沒升級的裝置把舊分頁同步回來、兩台都升級才停的代價（兩台 Mac 一起升）。
   - 測試（`watch-area.test.ts` 改寫用 fleet view 的那幾案：`:35`、`:43`、`:60`、`:72`、`:79`、`:87`、`:135`、`:257`）：從 pool store 建分頁（`watch-host-<host_id>`、locked、pane id `<tab>.p`）；第二次不寫；pool 讀不到 → 一個都不收；離開 pool 的主機 60 秒後收；舊分頁 60 秒後收、60 秒內還在；舊分頁後來又同步進來 → 再 60 秒收；兩台裝置（兩個 reconciler、共用 store）pool 相同 → 收斂、不互推；沒讀到 pool 的那台不動別台建的分頁。
   - mutation：拿掉舊分頁的 `lastListed` 種子（舊分頁那案轉紅）；view 是 null 時照樣收。
2. **開分頁的入口**（M12、M40）
   - `open-watch-area.ts:10-19`：`WatchTarget = { tab: … } | { poolHost: { hostId: string; alias?: string }; sub?: HostPage }`，拿掉 `{ host }` 與 `fleetIdOfAlias`（MON-1c 的橋，連同 `:7` 的 fleet import）；`:32` 用 `readPoolView()`；`:35-38` 的目標是 `watchHostTabId(poolHost.hostId)`；有 `sub` 時先 `writeHostPage(hostId, sub)`（view memory 的 key 從這期起是 `host_id`）。`alias` 留著可選，`FleetCells.tsx:36` 不必改。
   - `PoolHostPage.tsx:26`：`openWatchArea({ poolHost: { hostId: row.host.host_id }, sub: 'settings' })`；`row.host` 是 null 時按鈕停用。`HistoryPage.tsx:36`：有 `poolHostId` → `{ poolHost: { hostId } }`；只有 `fleetHostId` 的舊紀錄 → `{ tab: 'guardian' }`（開在區域的第一個分頁）。
   - 測試：`open-watch-area.test.ts`（`{ host }` 的各案改成 pool store 與 `{ poolHost }`；不認得的 id 選第一個分頁）、`open-watch-area.return.test.ts`（MON-1c 那幾案改成依 `host_id`）、`open-watch-area.jobs.test.ts`、`open-watch-area.events.test.ts`（主機分頁改由 pool store 建）；`PoolHostPage.test.tsx`（按鈕開那台的「設定」、沒有 pool 列時停用）；`HistoryPage.watch.test.tsx`（新舊兩種紀錄）。
3. **舊分頁的標籤不再讀 fleet**：`pane-labels.ts:71-72` 舊 content → `fleetHostId ?? ''`、`:4` 的 fleet import 拿掉；`useTabDisplay.ts:135-136` 只訂 pool、`:10` 拿掉（舊分頁一分鐘內就收掉）。測試 `watch-panes.test.ts`、`useTabDisplay.watch.test.ts` 跟著改。

#### MON-6b — 主機分頁的順序、拖曳寫回、分頁徽章（W2、M9、M13）

依賴 MON-6a-2。檔案（6 個、約 300 行）：`features/workspace/lib/watch-area.ts`、`watch-area.test.ts`、`features/workspace/hooks.ts`、`hooks.test.ts`、`hooks/useTabHostBadge.ts`、`useTabHostBadge.test.ts`。

1. **分頁照 `order` 排**（M13）：reconcile 加一步 `orderHostTabs`：區域裡主機分頁（`poolHostId` 在 view 裡的）佔著的那幾個位置，依 view 的順序重新填；守門員、事件、任務佇列，以及 view 沒列的主機分頁（等著收的、同步進來的）**位置不動**。不同才 `reorderWorkspaceTabs`。測試：`[守門員, 事件, 任務佇列, mserv, mlab]`＋`order` [mlab, mserv] → `[…, mlab, mserv]`；讀者把守門員拖到兩台主機中間 → 它留在那；已經對 → 不寫；新主機照 M13 的位置排進去。mutation：連固定分頁一起排（「位置不動」那案轉紅）。
2. **拖曳寫回 `order`**（W2）：`watch-area.ts` 新匯出 `writeWatchHostOrder(tabIds)`：依拖完的分頁順序取出主機（content 的 `poolHostId`）→ `usePoolHostsStore.getState().setOrder([...拖完的順序, ...原 order 裡不在畫面上的])`。`hooks.ts:149-153` `handleReorderTabs`：`activeWorkspaceId === WATCH_WORKSPACE_ID` 時先呼叫它，再照舊 `reorderWorkspaceTabs`。測試（`hooks.test.ts`）：把 mserv 拖到 mlab 前 → `order` 開頭是 [mserv, mlab]、側欄（`useMonitorHosts`）跟著換；下一次 reconcile 不寫；兩台裝置：B 收到 A 的 `order` → B 的分頁跟著排、之後兩邊都不寫（不互推）；拖守門員不動 `order`。mutation：寫回時丟掉不在畫面上的 id。
3. **分頁上的主機徽章**（M9：監控分頁組的分頁一起變）：`useTabHostBadge.ts:32-48`：主 pane 是 `watch-host`（有 `poolHostId`）→ `resolveHostBadge(usePoolHostLook(poolHostId), 'console')`（hook 無條件呼叫，沒有 id 時傳 `''`）；其他分頁照舊。測試：watch-host 分頁拿到那台的顏色與圖示；`writePoolHostLook` 改了 → 徽章跟著變；tmux 分頁不受影響。

#### MON-6c — 主機頁的讀數記憶（5 秒 history 與 series）

依賴 MON-1b。可與 6a-1、6a-2 並行。檔案（6 個、約 500 行）：`lib/pool/types.ts`、`parse.ts`、`parse.test.ts`、新檔 `lib/pool/host-history.ts`＋`host-history.test.ts`、`no-fleet-reads.guard.test.ts`（`POOL_FILES` 加 `host-history.ts`）。取代 `lib/pool/history.ts`、`use-facts-history.ts`、`use-series-history.ts`（它們寫進 `fleetHistory`），舊的在 6f-2 之後沒人用、MON-12 刪。

1. **series 的 G-1 欄位**（`docs/specs/2026-10-20-wwd-host-metrics-spec.md` §2.3；Go：`internal/wwd/wire/series.go:30-41`）：`types.ts:83-92` `SeriesPoint` 加可選的 `load1`、`load5`、`swap_used_mb`、`swap_out_rate`、`worker_running?: Slots`、`queue_queued`、`disks?: { mount; used_pct; free_gb }[]`；`parse.ts:176-185` `point()` 逐欄解，壞的那欄缺席、點照留（null 與缺席同義）。G-2、G-4 的欄位留給 MON-7。測試：一筆 G-1 的點、一筆只有原 8 欄的點、`disks` 裡一筆壞掉。mutation：一欄壞掉整點丟掉。
2. **一個時間點的讀數**：`host-history.ts` 的 `HostSample = { at; cpuPct; load1; load5; ncpu; memUsedMb; memAvailMb; memTotalMb; pageOut; swapOut; swapUsedMb; diskUsedPct; diskFreeGb; runHeavy; runLight; slotHeavy; slotLight; workerOn; zone }`，每欄 `number | null`（`zone` 是 `PoolZone | null`、`workerOn` 是 `boolean | null`）。`sampleOfFacts(f)`（5 秒的完整紀錄，欄位都有；磁碟取 `used_pct` 最大那顆）；`sampleOfPoint(p, memTotalMb, ncpu)`（series 沒有 `worker.slots` 與開關 → `slot*`、`workerOn` 為 null；磁碟取 `disks` 裡 `used_pct` 最大那顆，沒有 `disks` 時用 `disk_max_used_pct`、`diskFreeGb` null）。測試：兩種來源的每一欄；缺欄給 null 不給 0。
3. **讀取與記憶**（M43）
   - `HostRange = '5m' | '15m' | '30m' | '1h' | '6h' | '24h'`、`RANGE_MS`、`readsFiveSecond(r)`（30 分以內）。
   - 每台（以 `host_id` 為 key）兩條軌：短軌（5 秒一筆，留 31 分）與長軌（1 分鐘一筆，留 24 小時），各帶 `load: 'idle' | 'ok' | 'host_not_local' | 'error'` 與 generation（寫法同 `history.ts:23-30`、`:45-48`：較早發出、較晚回來的答案不寫）。
   - `loadShort(source, alias, hostId, now)`：一次讀 30 分（`fetchFactsHistory(…, '5s')`，`api.ts:65-70`；since 多往前 1 分，同 `history.ts:16`），5／15／30 分都從這條切；併入時同一個 `at` 留已有的。`appendLive(hostId, facts)`：pool store 每拿到這台新的 `facts.at` 就接上。
   - `loadLong(source, alias, hostId, range, …)`：`fetchPoolSeries(…, '1m')`（`api.ts:57-62`），只在來源 daemon 宣告 `pool.series.v1` 時（`history.ts:76-79` 的判斷搬過來）；之後每 60 秒只讀 `since = 最後一點的 at`、併入（不重讀整段）。
   - 比這台時鐘快超過 5 秒的點丟掉（`history.ts:33`）。
   - hook `useHostSeries(hostId, alias, range, liveFacts)` → `{ samples（切到 [now − R, now]）; load; rev }`：短範圍掛上時讀一次、之後接 live；長範圍掛上時讀、每 60 秒補；卸下時 generation 加一（還在路上的答案不寫）、計時器清掉。不收 fleet。
   - 測試：5 秒的讀取種進短軌；live 去重；併入時 live 那幾點不被舊答案蓋；先發後到的答案不寫；`host_not_local`；長軌的補讀用 `since=最後一點`、併入；切範圍；未來的點丟掉；沒有 `pool.series.v1` 不讀長軌；卸下後回來的答案不寫。
   - mutation：併入時用舊答案蓋 live；補讀改成讀整段（since 那案轉紅）；拿掉 generation 檢查。

#### MON-6d-1 — 量表與小圖的資料模型（M41 的第 1、2 段，M43）

依賴 MON-6c。檔案（6 個、約 470 行，離上限留 40% 以上）：新檔 `lib/pool/host-charts.ts`＋`host-charts.test.ts`、`components/watch/Gauge.tsx`＋`Gauge.test.tsx`、`components/watch/WatchHostPane.tsx`（舊頁改傳讀數）、`no-fleet-reads.guard.test.ts`（`POOL_FILES` 加 `host-charts.ts`）。

1. **六個量表的讀數**（M41-1、M22）：`gaugesOf(facts | null): (GaugeView | null)[]`，依序 CPU（`100 − idle_pct`，70／90）、記憶體（%）、記憶體壓力（等級 1／2／4 → 33／66／100，字「正常／警告／嚴重」；等級 0 → 「—」不畫弧）、換頁率（`page_out_rate`，800 滿格，50／800）、磁碟（最滿那顆 `used_pct`，85／95）、名額使用（worker 開：「u/t 件」、比例 u/t、滿了黃；關：「關」、單位 worker）。門檻與 `lib/watch/metrics.ts:23-29`、`:53-65` 相同，但不 import 它（它 import fleet 型別）；`GaugeView` 與 metrics 的 `GaugeReading`（`:10`）同形 `{ value; unit?; pct; tone }`，舊頁 6f-2 之前照樣能傳。`facts` 是 null（狀態不是 ok）→ 六個都 null（「—」，M22）。測試：每個量表的值、單位、色調；邊界；null。mutation：換頁的 `>` 50 改 `>=`；壓力 0 畫弧。
2. **`Gauge` 改吃讀數**：`Gauge.tsx:3-5`、`:14-16` 的 props 改成 `{ label: string; reading: GaugeView | null; testId: string }`，不再 import `FleetFast` 與 `lib/watch/metrics`；舊頁 `WatchHostPane.tsx:55` 改傳 `label={t(d.shortKey)}`、`reading={offline || !fast ? null : gaugeOf(d.k, fast, t)}`、`testId={\`gauge-${d.k}\`}`（`gaugeOf` 照舊從 metrics 來，舊頁 6f-2 才換）。測試 `Gauge.test.tsx` 改寫，畫法不變（`data-tone`、弧長），舊頁的 `WatchHostPane.resources.test.tsx` 照過。
3. **六張小圖的定義**（M41-2、M42、M43）：`miniChartsOf(samples, facts, range)` → 六個 `ChartDef`，依序 CPU 使用率（%，面積，70／90 虛線）、系統負載（load1；load5 淡；核心數那條線、標「N 核」）、記憶體（已用＋可用疊起來，GB，上限＝總量，90% 線）、換頁／Swap（換頁寫出＋swap 寫出；大字是頁／秒＋swap GB）、磁碟剩餘（最滿那顆，GB，`disk_min_gb` 10 GB 線、標「disk_min_gb 10 GB」）、名額使用（階梯：重、輕執行中；重、輕名額虛線；worker 關的那段大字「worker 關」）。每個 series 帶 `values`（對齊 `samples`）、`area`／`stack`／`step`／`dash`／`faint` 與色（主線 `accent`、第二條 `text-muted`；名額用 MON-3a 的 `queue-heavy`／`queue-light`；不加新 token、不搬設計稿的 COL 調色盤）。**一個 series 在這個範圍整段都是 null → `pending`**：圖例寫「等 series API 補欄位」；整張都 null 時圖面寫「等 series API 補欄位（wwd 的每分鐘紀錄有這項，留 14 天）」。768 起 1 小時以上只剩名額虛線是這樣（series 沒有 `slots`，spec §3）。`bigValue(def, samples, i)`：第 i 點的大字（照設計稿 `miniVal`）。`ChartDef` 是 6d-2 的輸入，型別在這期定下。測試：5 分（全有）與 6 小時（G-1 的點：名額虛線 pending、其他有）兩組；舊 daemon 的 series（只有 8 欄）→ 負載、Swap、名額執行中都 pending；大字的格式；`disk_min_gb` 線。mutation：pending 改成「有一點 null 就算」；`stack` 不疊。

#### MON-6d-2 — `MiniChart` 的畫法、互動與幾何（M42、M22）

依賴 MON-6d-1。檔案（3 個、約 340 行，離上限留 50% 以上）：新檔 `components/watch/host/MiniChart.tsx`＋`MiniChart.test.tsx`、`no-fleet-reads.guard.test.ts`（glob 加 `components/watch/host/**`）。

1. **畫法**：卡片樣式、圖面約 90 px、標題與大字同一列；SVG（`preserveAspectRatio="none"`）畫格線、門檻虛線＋短標、各 series（面積、疊加、階梯、虛線、淡），null 斷線、相鄰兩點相隔超過 3 個 step 也斷；y 軸三個刻度；x 軸三個時間（`hh:mm`，範圍的頭、中、尾）；`pending` 的字。props `{ def: ChartDef; samples; range; now; hoverIndex: number | null; onHover(i: number | null); dead: { fromAt: number; tone: 'warning' | 'error' | 'muted' } | null }`。
2. **互動**：滑鼠在圖面上 → `onHover(最近的點)`，畫直線與時間；離開 → `onHover(null)`；`hoverIndex` 有值時大字換成那一點（同一分頁的同步由 6e 的 `MonitorTab` 做）。
3. **幾何**：點的 x 依時間落在 [now − R, now]、y 依 min／max；`dead` → 從 `fromAt` 到右緣畫同色斜線（M22）。
- 測試：每個 series 一條 path、null 斷成兩段、超過 3 個 step 也斷、階梯是水平加垂直、疊加的第二層加在第一層上、門檻線的 y、x 的時間對位、`onHover` 的 index（mock `getBoundingClientRect`）、離開送 null、`hoverIndex` 換大字、`pending` 的字、`dead` 的起點百分比。mutation：null 不斷線；斜線從左緣開始；`onHover` 取最近點改成取左邊那點。

#### MON-6e — 「監控」分頁的元件（M41 的第 3 段、M22、#3785）

依賴 MON-6d-2、MON-5a（task 1 的 `started_at`）。檔案（13 個、約 720 行）：新檔 `components/watch/host/MonitorTab.tsx`＋`MonitorTab.test.tsx`、`components/watch/jobs/HostJobs.tsx`＋`HostJobs.test.tsx`、`components/watch/ProcTable.tsx`、新檔 `lib/pool/host-extra.ts`＋`host-extra.test.ts`、`lib/pool/fleet-bridge.ts`、`fleet-bridge.test.ts`、`components/watch/AvailRow.tsx`、`locales/zh-TW.json`、`en.json`、`no-fleet-reads.guard.test.ts`（`POOL_FILES` 加 `host-extra.ts`）。

1. **`ProcTable` 不吃 fleet 型別**：`ProcTable.tsx:4`、`:7` 的 `ProcRow` 改成自己宣告的 `{ pid; name; cpu; mem_mb?; rss_mb?; mem_kind?; owner? }`（`rss_mb?` 先留：舊頁 6f-2 之前還傳 fleet 的 `slow.top_*`，`WatchHostPane.tsx:189-190`，結構相容、不 import）。
2. **`HostJobs` 壓縮版**（M41-3 左欄）：`HostJobs.tsx:42-94` 改成一欄：「在這台執行」（狀態字、別台送出時加來源徽章、`jobLine`、執行中的計時 `started_at ?? claim.since`、重／輕）、「從這台送出，排隊中」最多 3 件＋「還有 n 件在排，看任務佇列 →」、「最近在這台結束」（件數＝clamp(9 − 在跑 − min(3, 在排) − (有「還有」? 1 : 0), 3, 5)，讓這欄跟兩張行程表差不多高，照設計稿 `jobsCol`）、最後「這台的全部工作」（`:57-61` 的 `openAll` 不動）。拿掉 `Section` 外框與 `JobLanes`／`JobQueue`（MON-5b 已 merge 的話，這期刪那兩個檔；否則 5b 刪）。「還有 n 件」按下去開任務佇列（不篩）。測試 `HostJobs.test.tsx` 改寫：三段、3 件上限與「還有 n 件」、結束件數的 clamp、別台送出的徽章、`started_at` 的計時、「全部工作」寫 history filter 與 mode。
3. **`MonitorTab`**（M41、M42、M22、#3785）：props `{ host: MonitorHost; range: HostRange; now }`；一排 `Gauge`×6（`gaugesOf(ok ? facts : null)`；最右邊留給 MON-10c 的告警標記）；3×2 的 `MiniChart`（`useHostSeries`、`miniChartsOf`；`hoverIndex` 是這個元件的 state、六張共用 → M42 同一時間點）；「工作與行程」一個區塊三欄：`HostJobs`、CPU 前 5、記憶體前 5（`ProcTable`，從 `facts.procs` 排序取 5，同 `WatchHostPane.tsx:84-90` 的 `topProcs`）。**狀態不是 ok**：量表全「—」、小圖畫到最後一筆，之後 `dead`（色依狀態：`wwd_down` 黃、`host_down` 紅、`unknown` 灰；`fromAt = lastFactsAt`）、兩張行程表寫「離線，沒有資料」——**不讀任何 fleet 的 `slow`**（#3785）。這期還沒有頁面掛它（6f-2 掛）。測試：六個量表的值；非 ok → 六個「—」；六張圖的順序；在一張圖上 hover → 六張的大字都換成那一點（M42）；行程前 5 與主人標籤；非 ok 的行程表寫離線、沒有任何列；1 小時範圍的名額圖圖例有「等 series API 補欄位」。mutation：hover 只換自己那張；非 ok 時仍畫行程表的列。
4. **`host-extra`**（純搬移＋一個函式）：`fleet-bridge.ts:15-35` 的 `PoolCellExtra` 與 `poolCellModel`（`:73-93`）裡組 pool 那一半，搬到 `lib/pool/host-extra.ts` 的 `hostExtraOf(st: PoolHostState)`；`fleet-bridge.ts` 改呼叫它並 re-export `PoolCellExtra`（Hosts 頁與 `AvailRow.test.tsx` 不必改）；`AvailRow.tsx:5` 改從 `host-extra` import。`fleet-bridge.test.ts` 裡 pool 部分的案例搬去 `host-extra.test.ts`。6f-2 的「狀態」過渡內容用它（不經 fleet）。
- **Split**：task 2（`HostJobs` 壓縮版）獨立一張。

#### MON-6f-1 — 子分頁的記憶、入口、`WatchSettings` 拿掉 fleet／probe 分支

依賴 MON-6a-2、MON-6c（`HostRange` 型別）。可與 6d-1～6e 並行。檔案（11 個、約 380 行）：`lib/watch/view-memory.ts`＋`view-memory.test.ts`、`features/workspace/lib/open-watch-area.ts`、`components/fleet/FleetCells.tsx`、`FleetCells.test.tsx`、`components/hosts/PoolHostPage.tsx`、`PoolHostPage.test.tsx`、`components/watch/WatchSettings.tsx`、`WatchSettings.test.tsx`、`components/watch/WatchHostPane.tsx`（舊頁改用新的記憶與新的 `WatchSettings` props）、`WatchHostPane.tab-switch.test.tsx`（舊頁的範圍記憶）。

1. **view memory 以 `host_id` 為 key**（tab-hosted）：`view-memory.ts:4` 不再 import fleet；`:18-19`、`:26-30` 換成 `readHostTab`／`writeHostTab(hostId): HostSubTab`（`'monitor' | 'status' | 'procs' | 'settings'`，預設 `monitor`）、`readHostRange`／`writeHostRange(hostId): HostRange`（6c 的型別；預設 `30m`，照設計稿 `HP.range`）、`readHostScroll`／`writeHostScroll(hostId, sub)`。`queueTabs`、`historyClasses`（舊閘門的排隊段，`QueueSection` 在用）留到 MON-12。舊頁到 6f-2 之前：子分頁 `settings` 對 `settings`、其餘對 `status`；範圍不是 `5m`／`1h`／`6h`／`24h` 時當 `5m`。`open-watch-area.ts` 的 `sub` 型別改 `HostSubTab`、改呼叫 `writeHostTab`；`FleetCells.tsx:36` 傳 `sub: 'monitor'`（M12）；`PoolHostPage.tsx:26` 照舊傳 `sub: 'settings'`。測試：`view-memory.test.ts`（每台各自、預設值）、`FleetCells.test.tsx`（按主機格寫 `monitor`）、`PoolHostPage.test.tsx`、舊頁的 `WatchHostPane.tab-switch.test.tsx`（範圍照樣跨分頁保留）。
2. **`WatchSettings` 拿掉 fleet／probe 分支**（88 定案 6）：刪 `kind` prop、`:238-245` 的 probe 分支、`:12` 的 `ProbeHostEditor` import、`:232-233` 的 `onGotoProbe`；`fleetHostId` 改名 `hostId`（只當草稿的 key：`:105` 的 `GuardianSection`、`:210` 的 `watchDraftKey`）。四台一律 `WorkerSettings`＋`WorkerExceptions`＋守門員段；沒有 daemon 的主機（`hostConfigId` 缺席）在守門員段寫既有的 `watch-set-unmapped`，不再出現 `ProbeHostEditor`（Hosts 頁的觀察主機頁照舊到 MON-9b）。舊頁 `WatchHostPane.tsx:177-179` 改傳 `hostId`、`poolAlias`（四台都傳 pool 的 alias）、`hostConfigId`、`peerIds`。測試 `WatchSettings.test.tsx`：probe 的各案拿掉；新增「沒有 daemon 的主機：有 `WorkerSettings` 與 `WorkerExceptions`、守門員段寫 unmapped、沒有 `watch-set-probe`」；草稿以 `hostId` 為 key、換主機不串。mutation：`hostConfigId` 缺席時畫回 `ProbeHostEditor`（那案轉紅）。

#### MON-6f-2 — 主機頁換殼、橫幅、守門測試擴到主機頁

依賴 MON-6f-1、MON-6e（6b 建議在前）。檔案（14 個、約 640 行，不含刪掉的舊測試）：`components/watch/WatchHostPane.tsx`（重寫）、`WatchHostPane.test.tsx`（重寫）、`WatchHostPane.tab-switch.test.tsx`（重寫）、刪 `WatchHostPane.footprint.test.tsx`、`.jobs.test.tsx`、`.pool.test.tsx`、`.queue.test.tsx`、`.resources.test.tsx`（內容已在 6d-1、6d-2、6e 的測試裡；舊閘門的排隊段不再有）、`WatchSettings.tab-switch.test.tsx`、`WorkerSettings.tab-switch.test.tsx`、`WorkerExceptions.tab-switch.test.tsx`（三個都經 `TabContent` 開舊頁、用 `fleetHostId` 建分頁，改成 pool store＋`poolHostId`）、`no-fleet-reads.guard.test.ts`、`locales/zh-TW.json`、`en.json`。

1. **頁面殼**（M41 標頭、M22、M43）：`WatchHostPane` 重寫：
   - 讀 `content.poolHostId`；這台＝`useMonitorHosts()` 裡 `hostId` 相同的（W4 時是留著的清單、`unknown`）；pool 沒列 → 一行「資源池沒有列出這台；一分鐘內沒回來，這個分頁會收掉」（A7）；舊 content → 既有的 `watch.host.no_host`。
   - 標頭：徽章 20 px（`resolveHostBadge(look, 'console')`，`host_down` 40%、`unknown` 60%，比照側欄）、alias、區域字（ok）或狀態圖示＋字（`LinkBreak`／`Power`／`Question`）、`wwd {version}・{platform} {arch}・{facts 的 hh:mm:ss}`（非 ok：「最後一筆 hh:mm:ss・最後檢查 hh:mm:ss」）；右邊子分頁切換（監控｜狀態｜行程｜設定）與範圍切換（5 分｜15 分｜30 分｜1 小時｜6 小時｜24 小時，只在監控、狀態）。子分頁、範圍、捲動讀寫 6f-1 的記憶。
   - **橫幅**（M22、#3785）：狀態不是 ok → 標頭下一條，`wwd_down` 黃、`host_down` 紅、`unknown` 灰（`status-warning`／`status-error`／`text-muted`），圖示＋「{alias}：{狀態字}」（`wwd_down` 加「，主機還開著」），下一行「最後一筆讀數 hh:mm:ss・最後檢查 hh:mm:ss；圖停在最後一筆，之後畫斜線」（`livenessLines`，`words.ts:85-88`）。四個子分頁都有（spec M22、#3785；88 定案（10-20））：橫幅畫在子分頁的內容之外（標頭下、內容上），不放在任何一個子分頁裡。
   - **整頁不讀 fleet**：沒有 `useFleetStore`、`fleetHostsView`、`usePoolCellModels`、`matchPoolHost`、`fleetHistory`、`QueueSection`（舊閘門的排隊段，spec §6「重指令排隊直接拿掉」）、`slow.top_*`（#3785）；6a-1 的過渡接法 `fleetViewOfPoolHost` 刪掉（`fleet-bridge.ts` 只剩 Hosts 頁在用）。
   - 測試（`WatchHostPane.test.tsx`）：ok 主機的標頭與四個子分頁；**非 ok 主機（三種狀態各一）逐一切到監控、狀態、行程、設定，每一頁都有橫幅、顏色與字對、最後一筆與最後檢查的時間對**；pool 沒列的主機；舊 content。mutation：橫幅只放在「監控」子分頁（逐頁那案轉紅）；`wwd_down` 用紅。
2. **四個子分頁**
   - 監控：`<MonitorTab host range now />`（6e）。
   - 狀態（過渡，MON-7 換）：`AvailRow`（`hostExtraOf(state)`，6e）＋一行「完整的狀態（六張卡、facts 全部欄位、完整圖表）在之後的期」。
   - 行程（過渡，MON-8 換）：兩張 `ProcTable`（`facts.procs` 的 CPU 前 12、記憶體前 12）＋`ResidentsTable`；非 ok 寫離線。
   - 設定（過渡，MON-9a 換）：`<WatchSettings hostId hostConfigId={daemonHostId ?? undefined} poolAlias={alias} offline peerIds />`（6f-1 的 props；`peerIds` 是其他台的 `daemonHostId`）。
3. **tab-hosted**：`WatchHostPane.tab-switch.test.tsx`（真的 `TabContent`）：mserv 的分頁選「行程」、範圍切到 6 小時、捲到下面，切到別的分頁再切回 → 子分頁、範圍、捲動都在；mlab 的分頁是它自己的（預設監控、30 分）。`WatchSettings`／`WorkerSettings`／`WorkerExceptions` 的三個 tab-switch 測試改成從 pool store 開主機頁的「設定」，原本斷言的草稿與捲動照舊。
4. **守門測試擴到主機頁**：`no-fleet-reads.guard.test.ts:9-15` 的 glob 加 `components/watch/WatchHostPane.tsx`、`WatchSettings.tsx`（6f-1 已拿掉 `ProbeHostEditor`，codex 第 2 條）、`WorkerSettings.tsx`、`WorkerExceptions.tsx`、`Gauge.tsx`、`ProcTable.tsx`、`AvailRow.tsx`、`ResidentsTable.tsx`、`lib/watch/view-memory.ts`、`features/workspace/lib/watch-area.ts`、`open-watch-area.ts`（`components/watch/jobs/**` 在 5b、`components/watch/host/**` 在 6d-2 已加）；檔頭說明改成「`fleet-bridge.ts` 只剩 Hosts 頁一個讀者（MON-9b 刪）」。mutation：在 `WatchHostPane.tsx` 暫加 `import type { FleetFast } from '../../lib/fleet/types'`、在 `MonitorTab.tsx` 暫加一行 `useFleetStore`、在 `WatchSettings.tsx` 暫加一行 `import '../../lib/fleet/store'`，都要轉紅。
- 這期之後沒人用、留給 MON-12 刪的檔：`MetricChart.tsx`＋測試、`ZoneBand.tsx`＋測試、`lib/watch/series.ts`＋測試、`lib/watch/metrics.ts`＋測試、`lib/pool/history.ts`＋測試、`use-facts-history.ts`、`use-series-history.ts`＋測試，以及 MON-12 本來就列的 `QueueSection`、`QueueNow`、`QueueLane`、`QueueHistory`。
- **Split**：task 2 的「行程」過渡內容（兩張前 12 的表＋常駐程式）移到 MON-8 開頭；6f-2 的「行程」先寫一行「在之後的期」。

### MON-7 — 主機頁「狀態」（M44、M43）
- **目標**：六張卡、facts 全部欄位表、完整圖表分組（監控那六張不重複）、區域色帶、同步 hover。取代 6f-2「狀態」的過渡內容。
- **主要檔案**：`lib/pool/types.ts`、`parse.ts`（G-2：`cpu.load15`、`user_pct`、`system_pct`、`cores[]`；G-3：`disk_io[]`；G-4：`procs_total`、`boot_at`、`self`、`swap_total_mb`；series 的 G-2、G-4 欄位（6c 只做 G-1）；全部可選、null 安全）、`lib/pool/host-history.ts`（`HostSample` 加欄）、`host-charts.ts`（完整圖表的定義）、新的 `components/watch/host/StatusTab.tsx`、區域色帶新寫（讀 `HostSample.zone`；舊的 `ZoneBand.tsx` 讀 `lib/pool/history` 的 `ZonePoint`，6f-2 之後沒人用、MON-12 刪）、`AvailRow.tsx`（判斷卡，吃 6e 的 `hostExtraOf`）。圖表用 6d-2 的 `MiniChart`（標準高度）、定義加在 6d-1 的 `host-charts.ts`。每核 CPU 讀 5 秒 history（series 不帶 cores）；主機之間的延遲只畫當下值。
- **依賴**：MON-6f-2（頁面）、6c、6d-1、6d-2、6e；G-1～G-4（768）；G-3（768，沒趕上就 769，之前磁碟 IO 那張畫「A 線補」佔位）。
- **大小**：兩期（卡片與欄位表、完整圖表），各約 700 行。可與 MON-8、MON-9 並行。

### MON-8 — 主機頁「行程」（M45）
- **目標**：CPU 前 12／記憶體前 12（可拉時間軸）、常駐程式、未宣告的重指令、觀察紀錄（`next_until` 翻頁、依 id 去重）。取代 6f-2「行程」的過渡內容。
- **主要檔案**：`ProcTable.tsx`、`ResidentsTable.tsx`、`lib/pool/observations-api.ts`（MON-10b 建 `/observations`，這期加 `/undeclared`；若 10b 還沒 merge 就由這期先建 `/observations` 那半，10b 改成沿用；device token 403 時說明只有 admin token 能讀）、新的 `components/watch/host/ProcTab.tsx`。時間軸讀 1 小時內 5 秒一筆的完整紀錄（`fetchFactsHistory`，每筆帶 `procs`；6c 的短軌只存 `HostSample`、不存行程，這期在 `host-history.ts` 旁另存每筆的 `procs`，或拉軸時再讀）。
- **依賴**：MON-6f-2；MD-3（765）；MON-10b（或自建，見上）。
- **大小**：一期，約 600 行。

### MON-9 — 主機頁「設定」與 Hosts 頁（M46–M52、W7、W8）
- **目標**：9a 設定子分頁（取代 6f-2 的過渡內容）：接工作、名額、主人（沿用 `WorkerSettings`）、暫停（沿用 `WorkerExceptions`）、告警（唯讀門檻；兩個失聯開關等 W8 的 API，之前唯讀顯示現值）、推播總開關（M50）、側欄顯示（M51：顏色、圖示、形狀、資料、套用到四台、回到預設，寫 MON-2b 的 `writePoolHostLook`）；`WatchSettings` 的 watch 列（守門員段）整段拿掉——fleet／probe 分支與 `fleetHostId` prop 在 6f-1 已拿掉、守門測試在 6f-2 已含它，9a 改寫時照樣在範圍內；拿掉「設定 → 終端機」的「主機格指示」控制項（欄位與投影留到 MON-12 一起清，免得多升一次 ordinal）。9b Hosts 頁：拿掉資源池主機與觀察主機（M52），留一行連結。
- **主要檔案**：9a：新的 `components/watch/host/SettingsTab.tsx`、`WorkerSettings.tsx`、`WorkerExceptions.tsx`、`WatchSettings.tsx`（watch 列整段拿掉；6f-1 之後只剩這段）、新的 `lib/pool/push-switch.ts`（`GET /api/hostconfig`、`PUT /api/hostconfig/wwd_alerts`）、`HostColorField.tsx`／`HostIconField.tsx`（沿用）、`TerminalSection.tsx`。9b：刪 `components/hosts/` 的 `PoolHostsSection`、`PoolHostGroup`、`PoolHostPage`、`PoolHostRoute`、`PoolStatusDot`、`ProbeHostsSection`、`ProbeHostPage`、`ProbeHostEditor`、`ProbeHostForm`、`ProbeCheck`、`probe-rows.tsx`、`use-probe-rows.ts`、`useProbeEditor.ts`、`FleetRowEditor.tsx`、`pool-status-word.ts`，`lib/pool/use-pool-host-rows.ts`、`lib/pool/fleet-bridge.ts`（最後一個讀者走了）；改 `HostSidebar.tsx`、`OverviewSection.tsx`、`components/HostPage.tsx`。
- **依賴**：MON-6f-2、MON-2b；W8 的 API（只擋那兩個開關）。
- **大小**：9a 約 750 行；9b 以刪除為主（只算新增行）。可與 MON-7、MON-8 並行。

### MON-10 — 事件分頁、盾牌與分頁上的數字（M23、M35–M37、M40、M41-1 的告警標記）

**為什麼拆三期**：告警的讀取與數字（盾牌、分頁標籤跟著換）約 720 行；事件的模型（告警＋觀察、去重、篩選、分日）約 560；事件分頁本體約 720。
依賴（整條）：MON-1a、MON-1b（pool 的來源 daemon、主機狀態）、MON-1c（`useMonitorHosts` 的順序）；MD-2、MD-3（765）。10a、10b 與 MON-5、MON-6 並行；**10c 要 MON-6f-2**（`more` 列開那台的「行程」子分頁要 6f-1 的 `HostSubTab` 與 `open-watch-area` 的 `sub`、6f-2 的子分頁本體；告警標記在 6e 的 `MonitorTab`）。
盾牌與「事件」分頁的數字＝**進行中的推播級告警＋值班者還沒處理完的事**（M36）；後一半要 A 線的 S7／S8（MON-11），**第二波先只算告警，值班者那一半是常數 0**，程式裡留一個名字清楚的位置（`dutyOpenCount()`，註明 MON-11 接上）。App 上沒有「關掉事件」的按鈕（M35）；盾牌的實心／空心（守門員在不在）照舊讀 watch.v1 的 `/api/watch/status`（W9）。

#### MON-10a — 告警的資料、盾牌與「事件」分頁的數字

依賴 MON-1a、MON-1b。檔案（14 個、約 720 行）：新檔 `lib/pool/alerts-api.ts`＋`alerts-api.test.ts`、新檔 `lib/pool/alerts-store.ts`＋`alerts-store.test.ts`、新檔 `lib/pool/alerts-events.ts`、`hooks/useMultiHostEventWs.ts`、新檔 `hooks/useMultiHostEventWs.alerts.test.ts`、`lib/pool/use-cell-models.ts`、`features/workspace/components/WatchShieldButton.tsx`＋`WatchShieldButton.test.tsx`、`lib/pane-labels.ts`、`hooks/useTabDisplay.ts`、`lib/watch/watch-panes.test.ts`、`no-fleet-reads.guard.test.ts`（`POOL_FILES` 加三個新檔，glob 加 `WatchShieldButton.tsx`）。

1. **解析與讀取**（MD-2）
   - `alerts-api.ts`：`PoolAlert = { key; kind: string; level: string; since: number; last: number; clearedAt: number | null; detail: string; silence?: 'wwd_down' | 'host_down' | 'unknown' }`、`HostAlerts = { host; known; alerts }`。`parseHostAlerts(v)`：外層 `host` 字串、`known` 布林、`alerts` 陣列或 null（null 當空）；一列缺 `key`／`kind`／`level`／`since`／`last` 或時間壞掉 → 那列丟掉；`cleared_at` 是 null 或時間；`kind` 照原字（不認得的照樣是字）；`silence` 只在 `kind === 'silence'` 時讀，不認得或缺席 → `'unknown'`（`partner_down` 就是這樣，`internal/wwd/advisor/rules_silence.go:68-77`）。`fetchHostAlerts(sourceHostId, alias)` → `GET /api/pool/hosts/{alias}/alerts`（`readJson`，`api.ts:38-41`），讀不懂 → `unreadable`。Go 的形狀：`internal/module/pool/alerts.go:22-32`（`PoolAlert` 內嵌 `wire.Alert`＋`silence`）。
   - 測試：照 Go 形狀寫的字面值（一個 disk_low push、一個已清掉的 silence `host_down`、一個 orphans digest）；`known: false`；`alerts: null`；壞列丟掉、其他留；不認得的 kind。mutation：不認得的 level 當 push（task 3 的數字那案轉紅）。
2. **讀取的時機**（`alerts-store.ts`）
   - 狀態 `{ byHost: Record<alias, { kind: 'ok'; known; alerts; at } | { kind: 'error'; code: PoolErrorCode; at }>; source: string | null }`；`retainPoolAlerts()`（掛載計數，同 `use-cell-models.ts:12-23`）。**`useRunPool`（`use-cell-models.ts:25-35`）也 retain 它**：pool 在跑（側欄的主機列一直掛著）就讀告警，盾牌與分頁標籤的數字不必另外誰掛著。
   - 讀的是 pool store 的來源 daemon（`usePoolStore` 的 `sourceHostId`）與它列的主機：來源或主機清單變了 → 每台讀一次（新來源一個新 generation，舊來源晚到的答案不寫）；來源 daemon 的 `alerts.changed {host}` → 只讀那台；那個 daemon 的 host-events 連線打開 → 全部讀一次（`useMultiHostEventWs.ts:433` 的 `poolEvent` 旁）；**來源沒有宣告 `pool.alerts.v1`**（`internal/core/info_handler.go:172`，看 `daemonCapabilities`）→ 每 30 秒全部讀（daemon 自己也是每 30 秒讀一次其他台，`alerts.go:16-18`）；有宣告 → 不排計時器。pool 不再列的主機從 `byHost` 拿掉；整個資源池讀不到（來源 null）→ 留著最後的答案、不再讀（數字不歸零，同 W4 的精神）。
   - `alerts-events.ts`：`ALERTS_EVENT_TYPE = 'alerts.changed'`、`parseAlertsChanged(value)`（`{host}`，`host` 是非空字串）、`handleAlertsEvent(hostId, value)`；`useMultiHostEventWs.ts:367-372` 旁加一段（同樣先比對連線的 key）。
   - 測試（`alerts-store.test.ts`，假計時器）：每台經來源讀一次；來源的事件只重讀那台；別的 daemon 發的事件不理；沒有能力 → 30 秒輪詢、有 → 沒有計時器；來源換了 → 舊來源晚到的答案不寫；最後一個 release → 計時器清掉；離開 pool 的主機拿掉；連線打開 → 全部重讀。`useMultiHostEventWs.alerts.test.ts`（照 `.pool.test.ts`）：事件送到 store、連線的 key 不符不送。
   - mutation：任何 daemon 的事件都收；有能力時照樣輪詢。
3. **數字**（M36、M40）
   - `alertCountOf(byHost)`＝進行中（`clearedAt` null）、推播級（`level === 'push'`）的告警數，**同一個 `silence:<alias>` key 出現在幾台的清單裡只算一件**（每台 wwd 都替它的對等體開 `silence:<對等體>`，`cmd/wwd/advisor.go:51-59`；88 定案（10-20））＋`dutyOpenCount()`（0）。`usePoolAlertCount()`／`readPoolAlertCount()`。
   - `WatchShieldButton.tsx:22-24`、`:34`、`:40`：數字改讀 `usePoolAlertCount()`（取代 `pendingCount(useWatchEventsStore…)`）；有數字開事件、沒有開守門員（M40，照舊）；`aria-label` 沿用 `watch.shield.events`。實心／空心照舊讀 `useWatchStatusStore`。
   - `pane-labels.ts:65-68`：「事件 n」的 n 改 `readPoolAlertCount()`；`useTabDisplay.ts:137-138` 改訂 alerts store 的數字。這兩處、盾牌、事件分頁（10c）是 `lib/watch/events-store.ts` 的全部讀者，10c 之後它只剩寫入端，MON-12 刪。
   - 測試：`WatchShieldButton.test.tsx`（mlab 與 air26 的清單都有 `silence:mserv`＋mlab 一個 disk_low push＋一個 digest → 2；按一下開事件；0 → 開守門員）；`watch-panes.test.ts`（「事件 2」）。
   - mutation：digest 也算；silence 不去重。

#### MON-10b — 事件的資料（告警＋觀察）

依賴 MON-10a。檔案（5 個、約 560 行）：新檔 `lib/pool/observations-api.ts`＋`observations-api.test.ts`、新檔 `lib/pool/events-model.ts`＋`events-model.test.ts`、`no-fleet-reads.guard.test.ts`（`POOL_FILES` 加兩個）。

1. **觀察的讀取**（MD-3）：`observations-api.ts`：`PoolObservation = { id; host; session; via; command; signature; startedAt; endedAt: number | null; cpuSeconds; cpuPeak; memPeakMb; procsPeak }`（Go：`internal/wwd/wire/observation.go:21-36`）；`fetchObservations(sourceHostId, alias, { since, until? })` → `{ known; observations（照 wwd，舊的在前）; truncated; nextUntil: string | null }`——**`next_until` 保留原字串**、下一頁原樣送回（`internal/module/pool/observations.go:10-13`：小數秒切掉會漏紀錄；同一筆可能兩頁都有，依 id 去重）；`{ known: false }`（舊 wwd）→ 空；device token 的 403 → `PoolReadError` 的 `forbidden`（`errors.ts` 既有分類），呼叫端寫「只有 admin token 能讀」。`pool.observations.v1` 由呼叫端判斷（`info_handler.go:166`）。MON-8 在這個檔加 `/undeclared`。測試：照 Go 形狀的字面值；`known: false`；壞紀錄丟掉；`next_until` 帶小數秒、原樣回傳；403 的分類。mutation：`next_until` 經過 `Date` 轉換再回傳。
2. **事件的列**（M23、M35）：`events-model.ts`：
   - `alertRowsOf(byHost, hosts)`：每筆告警一列 `{ id; hostId; alias; at: since; source: 'alert'; level: 'push' | 'digest'; kind; silence?; live: clearedAt === null; endAt: clearedAt; title: detail 或種類的字; last }`。**主機歸屬**：`silence` 且 key 是 `silence:<alias>` → 那台（叫不到的主機，同設計稿 `DEMO_EVENTS` 的寫法）；**同一個 key、時間重疊的幾筆合成一列**（`at` 取最早；還有一筆進行中就是進行中；都清掉時 `endAt` 取最晚；88 定案（10-20））；其他（含 `partner_down`）→ 清單所屬的那台。
   - `observationRowsOf(byHost, hosts)`：每台最新 3 筆（`startedAt` 新的在前）各一列（`level: 'obs'`；標題「未宣告的重指令：{signature}」；細節：session（via）、CPU 秒、峰值核數（`cpuPeak / 100`）、記憶體峰值 MB、行程數；`endedAt` null 是進行中）；超過 3 筆 → 一列 `{ more: true; hostId; n }`（排在第 3 筆之前一點）。
   - `filterEvents(rows, { host, source, status })`：`host` 是 `host_id` 或 `all`；`source` 是 `all`／`alert`／`obs`；`status` 是 `all`／`live`／`done`；`more` 列只在主機相符、來源不是 `alert`、狀態不是 `live` 時出現（設計稿 `renderEvents`）。
   - `groupByDay(rows, now)`：新的在上；`today`／`yesterday`／`MM/DD`（跨午夜）。`liveCounts(rows)` → `{ push; other }`（頁首那行）。
   - 測試：M23 的例子（air19 06:31–07:19 `host_down`，mlab 與 air26 各一筆 → 一列、已結束、`endAt` 07:19）；**同一個 silence key、時間不重疊的兩段停機（06:31–07:19、09:02–09:10）→ 兩列、不合併**；M35 的例子（mlab 的 orphans digest 清掉 → done）；`partner_down` 歸清單的主機；不認得的 kind 照字；某台 5 筆觀察 → 3 列＋`more` n 2；三組篩選；分日跨午夜；`liveCounts`。
   - mutation：不去重；只看 key、不看時間重疊（兩段停機那案轉紅）；`more` 列在「wwd 告警」篩選下也出現；觀察舊的在前。

#### MON-10c — 事件分頁、分頁的建立條件、主機頁的告警標記

依賴 MON-10b、MON-6f-2。檔案（12 個、約 720 行）：`components/watch/WatchEventsPane.tsx`（重寫）、`WatchEventsPane.test.tsx`（重寫）、`WatchEventsPane.tab-switch.test.tsx`（重寫）、`lib/watch/view-memory.ts`＋`view-memory.test.ts`、`features/workspace/lib/watch-area.ts`、`watch-area.events.test.ts`、`components/watch/host/MonitorTab.tsx`＋`MonitorTab.test.tsx`、`locales/zh-TW.json`、`en.json`、`no-fleet-reads.guard.test.ts`（glob 加 `WatchEventsPane.tsx`）。

1. **事件分頁**（M23、M35、M37）：`WatchEventsPane.tsx` 重寫：
   - 資料：告警讀 alerts store（10a）；觀察在這個分頁掛著時讀（每台、經來源 daemon、`since` 24 小時前；掛上時與之後每 60 秒；來源沒有 `pool.observations.v1` 就不讀），讀到的放模組記憶（切回來立刻有上一次的）。主機與順序來自 `useMonitorHosts()`（M13）。
   - 畫面：標題「事件」；一行「推播級進行中 N・其他進行中 M」（N > 0 用 `text-status-error-text`）；三排篩選（主機：全部＋每台 alias；來源：全部／wwd 告警／觀察；狀態：全部／進行中／已結束；沿用 `ChipRow`，`:30-43`）；依日分段（今天（MM/DD）、昨天（MM/DD）、MM/DD）；每列：時間、等級（推播／摘要／觀察）、種類（silence 的兩種用 `LinkBreak` 黃、`Power` 紅，`unknown` 是 `Question` 灰，加字；其他用中性的框）、主機徽章與 alias、右端「進行中・{多久}」（推播紅、其他黃）或「已恢復 hh:mm」（silence）／「已結束 hh:mm」、標題、細節行；silence 列左邊 2 px 同色豎線（M23）。`more` 列：「{alias} 過去 24 小時還有 n 筆觀察 → {alias} 的「行程」分頁」，按下去 `openWatchArea({ poolHost: { hostId }, sub: 'procs' })`（6a-2 的入口、6f-1 的 `HostSubTab`、6f-2 的「行程」子分頁）；測試斷言開的是那台的分頁、子分頁是「行程」。讀不到某台的告警 → 一行「讀不到 {alias} 的告警（{原因}）」；觀察 403 → 一行「觀察紀錄只有 admin token 能讀」（只寫一次）。
   - **沒有任何改變事件的按鈕**（M35）；頁首原本的守門員那一行（`:154-160`、`:169-172`）拿掉（88 定案（10-20））。推播開關不在這頁（M37 的總開關在 MON-9a）。
   - 篩選放 `view-memory.ts:15-16`、`:24-25`（型別換成 `{ host; source; status }`，`lib/watch/events-view.ts` 的 `EventFilters` 不再用），新增 `readEventsScroll`／`writeEventsScroll`；篩的主機已經不在 pool → 當「全部」（同現在的 `:143-146`）。
   - 測試（`WatchEventsPane.test.tsx` 重寫）：告警＋觀察的列；silence 列的圖示、色、豎線與「已恢復 07:19」；進行中的時長；**每一列裡沒有按鈕**（`more` 列的連結除外）；三組篩選；篩的主機離開 pool → 全部；403 那行；觀察每 60 秒讀一次、卸下後不讀；沒有守門員那行。`WatchEventsPane.tab-switch.test.tsx` 重寫（真的 `TabContent`：選主機 mserv＋來源觀察、捲到下面，切走再切回 → 篩選與捲動都在）。
   - mutation：拿掉豎線；`more` 列的條件反過來。
2. **事件分頁的建立條件**：`watch-area.ts:143-155` `reconcileEvents` 的條件（`:145`，原本只看 watch.v1 的 `anyHostDeclaresWatch`）加「有一台連著的 daemon 宣告 `pool.alerts.v1`」；只建不刪，照舊。測試 `watch-area.events.test.ts`：只有 `pool.alerts.v1` 的 daemon → 建；都沒有 → 不建；建了之後能力消失 → 不刪。
3. **主機頁「監控」的告警標記**（M41-1）：`MonitorTab`（6e）量表列最右邊：這台進行中的告警（10b 的列，`hostId` 相符且 `live`，歸屬與事件頁相同）有 N 件 → 小按鈕「N 件告警」（有推播級用 `text-status-error-text`，否則 `text-status-warning-text`；`title` 列出各件標題），按一下 `writeEventFilters({ host: hostId, source: 'all', status: 'all' })` 再 `openWatchArea({ tab: 'events' })`。測試併在 `MonitorTab.test.tsx`。
- **Split**：task 3（告警標記）獨立一張。

### MON-11 — 守門員分頁（M38、M39）
- **目標**：值班卡、打開對話／叫醒／問問題、進行中的 incident、處理紀錄、提議的同意與拒絕；盾牌與「事件」分頁數字的值班者那一半（MON-10a 的 `dutyOpenCount()`）接上 S7。
- **主要檔案**：`components/watch/WatchGuardianPane.tsx`、`WatchLaunchPanel.tsx`、`lib/watch/status-store.ts`（lucia 期間仍讀 `/api/watch/status`、`/wake`，W9）、新的 incident client（S7 的路由）、`lib/pool/alerts-store.ts`（`dutyOpenCount`）。
- **依賴**：A 線的 S7、S8（8b；P7 (A) 由 1f 排）。等 A 線的 plan 出來再細化。
- **大小**：一～兩期。

### MON-12 — fleet 退場（A11、W9）
- **目標**：SPA 不再讀 `fleet.v1`、`gate.decisions.v1`；`watch.v1` 只留守門員的 status 與 wake（88 定案 W9：保留 `/api/watch/status`、`/api/watch/wake` 的讀者——守門員頁與盾牌——到 8b 的 incident 路由上線才拿掉）。
- **主要檔案**：刪 `lib/fleet/*`（store、ws、api、history、config-*、probe-*、parse、types、`use-fleet-*`；`bar-readings.ts`、`ring-driver.ts` 已在 MON-1c-0b 搬到 `lib/pool/`，留著，不刪）；`hooks/useMultiHostEventWs.ts:148-150`、`:354-357`（fleet 的訂閱與處理）；`hooks/useTabQueued.ts` 的 fleet 那半；`components/watch/QueueSection.tsx`、`QueueNow.tsx`、`QueueLane.tsx`、`QueueHistory.tsx`；`lib/watch/api.ts` 的 decisions、`decisions-types.ts`；`lib/pool/pool-look-migration.ts`；`cell-model.ts`、`use-cell-models.ts` 剩下的部分；`useUISettingsStore.fleetCellIndicator` 與它的投影（settings ordinal 再升一次）；fallback 與 fleet 的語系鍵。**修訂 2 加**：MON-6f-2 之後沒人用的 `MetricChart.tsx`、`ZoneBand.tsx`（舊）、`lib/watch/series.ts`、`lib/watch/metrics.ts`、`lib/pool/history.ts`、`use-facts-history.ts`、`use-series-history.ts`（各帶測試）；`lib/watch/queue-time.ts` 拆開（`mss`、`secondsSince`、`WAIT_LONG_S` 留，`holderKey`／`waiterKey` 與 fleet 型別的 import 刪）；MON-10c 之後沒人讀的 `lib/watch/events-store.ts`、`events-view.ts`、`events-types.ts`、`lib/watch/api.ts` 的 events／unhandled 讀取與 `useMultiHostEventWs` 的 `watch.events` 那半（`watch.changed` 與 status 留，W9）；`types/tab.ts` 的 `fleetHostId`（舊分頁的欄位）。守門測試改成全 repo 不 import `lib/fleet/`。
- **依賴**：MON-5c、6f-2、7、8、9、10c；時程跟 1f 的 WWD-9（daemon 端拿掉 `fleet.v1`、`gate.decisions.v1`）對齊。
- **大小**：一～兩期；純刪除只算新增行。

## 順序與分工

- 依賴：MON-1a、MON-1b、MON-2a 互不依賴；MON-1c-0（1a 之後）；MON-2b（1a、2a 之後）→ MON-1c（1a、1b、2b、1c-0 之後）→ MON-1d（1b、1c 之後）與 MON-3a（1a、1c 之後）可並行 → MON-3b（1c、3a 之後）→ MON-4（3b 之後）。MON-3a 不與 MON-1c 並行（兩期都改 `FleetCells` 的接線）。
- 建議兩位 Sonnet member：
  - 甲：MON-1a → MON-1c-0 → MON-2b → MON-1c → MON-3a → MON-3b → MON-4。
  - 乙：MON-2a → MON-1b → MON-1d（等 1c merge），之後接第二波（MON-10 或 MON-5 的細化）。
  - 只有一位時照 1a → 1c-0 → 1b → 2a → 2b → 1c → 1d → 3a → 3b → 4 疊分支做。
- 第二波（修訂 2）：
  - 任務佇列：5a（前置都已 merge）→ 5b → 5c（5d 若拆出）。
  - 主機頁：6a-1（1c、1d 之後）→ 6a-2（另要 2a、2b）→ 6b；6c（1b 之後，可與 6a-1、6a-2 並行）→ 6d-1 → 6d-2 → 6e（另要 5a 的 task 1）；6f-1（6a-2、6c 之後）；6f-2（6f-1、6e 之後；6b 建議在前）。
  - 事件：10a（1a、1b 之後）→ 10b → 10c（另要 6f-2）。
  - 三條線可同時開。同檔的地方：`pane-labels.ts`、`useTabDisplay.ts`（6a-1、6a-2 的主機標籤與 10a 的事件數字，相鄰幾行）、`watch-area.ts`（6a-2／6b 的主機分頁與 10c 的事件分頁，不同函式）、`view-memory.ts`（6f-1 的主機與 10c 的事件篩選，10c 在後）、`MonitorTab.tsx`（6e 建、10c 加標記）、`no-fleet-reads.guard.test.ts`、語系檔；後 merge 的那張併最新 `origin/main` 再跑 PR 層測試。
  - MON-7、MON-8、MON-9 在 MON-6f-2 之後並行（MON-8 另看 10b）；MON-11 等 A 線；MON-12 最後（5c、6f-2、7～9、10c 之後）。
  - 建議分工：甲（MON-4 之後）6c → 6d-1 → 6d-2 → 6a-1 → 6a-2 → 6b → 6f-1 → 6e → 6f-2；乙（MON-1d 之後）10a → 10b → 5a → 5b → 5c → 10c（等 6f-2）。有第三位時 MON-5 整條給他，乙做完 MON-10 接 MON-7。
- 改到 team 模組的不在本線範圍；本線全是純 SPA，88 自己放行（B 線 10-19 起的規則），bump 與部署歸 1f。MON-2a 升 settings ordinal，88 放行前先告訴 1f（同 TG-1a-1）。

## 待查與已定案

- **iOS（待查）**：purdex-ios 對 settings ordinal 13 與新 store 的反應（MON-2a task 4）。
- **88 定案（10-20）** W1–W9（spec §7.3），落在這些 task：W1 → MON-2a task 1、MON-2b task 1；W2 → MON-2b task 2、MON-6b；W3 → MON-1b task 2；W4 → MON-1c task 2、MON-3a task 3；W5 → MON-4 task 2；W6 → MON-3a task 5；W7 → MON-2b task 1、MON-9a；W8 → MON-9a；W9 → MON-11、MON-12。
- **A 線（不擋第一波）**：兩種失聯推播開關的寫入 API 由 88 請 1f 排（A 線小項，W8），上線前 MON-9a 唯讀顯示現值；series 補 `slots`、PSI、`sessions_room`、常駐程式（spec §3 最後一段）由 1f 排。
- **第二波細化（修訂 2）的定案**：細化時查到的 8 點，88 於 10-20 全部照 plan 原本的寫法定案：
  1. **pane content 的欄位名**（A7）：spec 原本寫 watch-host 的 pane content 加 `hostId`（指的是 wwd 的 `host_id`）；`types/tab.ts:155-156` 明寫這個 kind 的欄位**刻意不叫 `hostId`**，免得通用的 pane 讀者把它當 App 主機。88 定案（10-20）：`poolHostId`；spec A7 的欄位名同步改了（MON-6a-1 task 1）。
  2. **舊主機分頁收不掉**（A7）：spec 說舊分頁「照現行 60 秒缺席規則收掉」，但現行規則對「這台從沒列過」的分頁一律留著（`watch-area.ts:231`）；升級重開後沒有一台裝置列過 fleet id，舊分頁會永遠留著。88 定案（10-20）：沒有 `poolHostId` 的主機分頁一看到就記成剛列過，60 秒後收；還沒升級的裝置會把它們同步回來，再 60 秒收一次，直到兩台都升級；接受這個代價（兩台 Mac 一起升）（MON-6a-2 task 1）。
  3. **「監控」子分頁要不要畫橫幅**（M22）：spec M22 與 #3785 寫主機頁有橫幅；設計稿 `renderHost`（`page.js:1044`）是 `dead && HP.tab !== 'mon'` 才畫，「監控」改在標頭寫狀態、副字與最後檢查。88 定案（10-20）：照 spec，四個子分頁都畫（MON-6f-2 task 1，逐頁測試）。
  4. **silence 告警的去重與歸屬**（M23、M36）：每台 wwd 都替自己的對等體開 `silence:<對等體>`（`cmd/wwd/advisor.go:51-59`、`internal/wwd/advisor/rules_silence.go:29-59`），mserv 一停，mlab、air26（與醒著的 air19）各有一筆；照原樣讀，事件頁會有三列、盾牌數字 +3。88 定案（10-20）：同一個 key 合成一列、算一件，列在叫不到的那台底下（設計稿的示意列也是這樣）；主機頁的告警標記用同一套歸屬（設計稿 `alertsOf` 讀的是那台自己的清單，mserv 的 silence 會出現在 mlab 頁上）（MON-10a task 3、10b task 2、10c task 3）。
  5. **期數**：照 ≤ 800 行／20 檔切，MON-5 拆 3 期（5a～5c）、MON-6 拆 6 期（6a～6f）、MON-10 拆 3 期（10a～10c），原 plan／spec §8 寫的是約 2、約 2、1～2 期；理由寫在各期的「為什麼拆」。88 定案（10-20）：照這個拆法，spec §8 的分期表同步改了。修訂 3：MON-6 再拆成 9 張（6a-1、6a-2、6b、6c、6d-1、6d-2、6e、6f-1、6f-2，codex 第二波審查第 2、6、7 條），spec §8 一起改。
  6. **主機頁「設定」的過渡內容**：6f-1 起主機頁的「設定」一律走 `WatchSettings` 的 daemon 那支（`WorkerSettings`、`WorkerExceptions`、守門員段），`ProbeHostEditor`（`WatchSettings.tsx:238-245`；fleet 的觀察主機設定，含 SSH 別名）不再從主機頁出現；Hosts 頁的觀察主機頁還在，到 MON-9b。理由：它寫的是 fleet 的 probe 設定，MON-1c 起側欄已經不讀那份外觀，留著會讓人以為改得到。88 定案（10-20）：6f-1 起拿掉（`WatchSettings` 的 fleet／probe 分支刪掉），Hosts 頁的觀察主機頁照舊到 MON-9b（MON-6f-1 task 2）。
  7. **事件頁的守門員那一行**：現在的事件頁頁首寫守門員的狀態（`WatchEventsPane.tsx:154-160`、`:169-172`）；設計稿 r3 的事件頁沒有這行，spec M35 也沒提。88 定案（10-20）：拿掉（守門員頁有）（MON-10c task 1）。
  8. **原因的字**（M31）：spec M31 原本的例字（名額已滿、壓力偏高、主人在用、舊閘門名額滿（#3450））與既有的 `pool.reason.*`（名額都在用、負載偏高、主人正在這台操作、閘門的名額被占住，`zh-TW.json:3202-3211`）不同。88 定案（10-20）：沿用既有鍵（側欄卡片、主機頁、任務佇列同一套字），只新增「暫停到 hh:mm（名稱）」；spec M31 的例字已改成跟現有的字一致（MON-5b task 2）。
- **第二波細化時自己定的**（不是衝突；88 定案（10-20），七條都接受）：
  - 主機頁預設範圍 30 分（設計稿的 `HP.range`；現在是 5 分）。
  - 小圖的顏色只用既有 token（`accent`、`text-muted`、`queue-heavy`／`queue-light`），不搬設計稿的 COL 調色盤、不加新 token。
  - 任務佇列的工作列一律寫 `jobLine`（repo task args，過長截斷、title 是全文）；設計稿的 `taskLine` 寫死「repo 是 purdex 時省略」，不搬。
  - 任務佇列頁摘要列的節條跟著頁面自己的工作（保留已讀的工作），不套側欄的「pool 可讀」閘門（M25 寫的是側欄）。
  - worker 剛關、手上還有工作的主機，工作池照樣給它一格（M27 寫「worker 關著的主機不出現」，但「lane 不藏工作」是既有規則，正在跑的會跑完，M47）；讀不到 wwd 的主機（M24 的那一分鐘）同樣列出它手上的工作，名額寫「—」、原因寫狀態字。
  - 告警的讀取掛在 `useRunPool` 上（pool 在跑就讀），不另外在 `main.tsx` 啟動。
  - MON-6f-2 之後沒人用的舊主機頁檔留給 MON-12 刪（不在 6f-2 刪，免得超過 20 檔）；若 88 要早點清，另開一張純刪除的 PR。
- **核對程式時查到、與 spec 的描述不合但不影響設計的**（plan 已照程式寫）：
  - MD-5 已部署，但 SPA 的 `PoolJob` 還沒有 `started_at`（`jobs-types.ts:30-57`）→ MON-5a task 1。
  - G-1 已進 768，但 SPA 的 `SeriesPoint` 只解原 8 欄（`types.ts:83-92`、`parse.ts:176-185`）→ MON-6c task 1。
  - 原 plan 的 MON-6 說沿用 `lib/pool/history.ts`、`use-series-history.ts`、`MetricChart.tsx`、`Gauge.tsx`、`ProcTable.tsx`、`lib/watch/view-memory.ts`；它們都 import `lib/fleet/`（`history.ts` 把讀數寫進 `fleetHistory`），守門測試擴到主機頁就不能沿用 → MON-6c～6f-2 新寫或改型別。
  - M33（取消的確認規則）`JobMenu` 已經做好（`JobMenu.tsx:1-2`、`JobMenu.test.tsx:106`、`:123`），MON-5 不改。

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

## codex 第二波 plan 審查（task-mvgu8cfz-t8p13g）對照表

88 全部採納（10-20），修訂 3。

| # | 嚴重度 | 內容 | 處理 |
|---|---|---|---|
| 1 | critical | MON-10c 的 `more` 列要開「行程」子分頁，`HostSubTab` 與 `open-watch-area` 的 `sub` 在 6f，10c 卻只寫依賴 10b／6e | 10c 依賴改成 MON-6f-2；`more` 列的測試斷言開到「行程」；拿掉「6f 之前只開分頁」的過渡說法與 task 3 的等待 Split；第二波順序、spec §8 一起改 |
| 2 | important | 6f 的守門範圍沒有 `WatchSettings.tsx`，而它還 import `ProbeHostEditor` | 新增 6f-1 task 2：刪 `kind`、probe 分支、`ProbeHostEditor`、`onGotoProbe`，`fleetHostId` 改名 `hostId`，測試與 mutation 列在那裡；6f-2 的守門範圍加 `WatchSettings.tsx`（再加一個 mutation）；MON-9a 改成只拿掉守門員段 |
| 3 | important | 5c 的 hover 測試只看路線 | 測試加：其他工作卡、佇列池格、工作池格的 class 與屬性都不變；mutation 加「替其他卡加 dim class」 |
| 4 | important | 5c 的 P18 只測 worker 關著 | 5a 抽出 `hostCanTake`（`jobs-view.ts:79` 的完整條件），5a 的候選與 5c 的虛線共用；5c 測試加「worker 開著但標籤不符 → 不畫」，mutation 加「只看 `worker.enabled`」 |
| 5 | important | 6f 的橫幅只說四頁都有，沒有逐頁的測試 | 6f-2 task 1 測試：三種非 ok 狀態逐一切四個子分頁，每頁都有橫幅；mutation「橫幅只放在監控」 |
| 6 | important | 6a 的 Split（task 4、5 移到 6b）與 6b 的內容矛盾 | 重算後 6a 實際碰 27 個檔，真的拆：6a-1（讀者先認得 `poolHostId`，12 檔／300 行）、6a-2（reconciler 與入口，15 檔／560 行），各列檔案；6b 改依賴 6a-2 |
| 7 | important | 6d 約 790 行，貼著上限 | 拆成 6d-1（資料模型與 `Gauge`，6 檔／470 行）與 6d-2（`MiniChart` 的畫法、互動、幾何，3 檔／340 行），都留 15% 以上；6e 改依賴 6d-2，MON-7 改依賴 6d-1、6d-2 |
| 8 | minor | 10b 沒測「同一個 silence key、時間不重疊」 | 加一案：兩段停機 → 兩列、不合併；mutation「只看 key、不看時間重疊」 |

另外：spec M32 的例子「air26 舊閘門名額滿」改成「air26 閘門的名額被占住」，與 M31 的字一致；plan 5a 的同一個例子跟著改。
