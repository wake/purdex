# 監控分頁組＋側欄主機列 — spec

> 2026-10-20 B 線 lead 88。來源：
> - 使用者 10-20 08:1x 的需求（經 1f 轉述；原文 `~/.local/share/purdex-lead/briefs/b-monitoring-redesign-2026-10-20.md`）：守門員一系列頁面保留、改接 wwd；主機監控「只會更多、不會更少」；側欄主機列的顏色、圖示、指標、件數與佇列條；這裡的設定取代 Hosts 頁的 wwd 設定。
> - 設計稿 r3：https://pages.mlab.host/wake/purdex-b-line/monitoring-design.html（原始檔在 B 線 worktree 的 `docs/pages/b-line/monitoring-design.html`＋`monitoring-data/page.js`）。使用者 10-20 18:0x 核准（「不用再給我看，直接開工」），同時給了三項修改 U1–U3（§5.2、§5.1 的 R8）。
> - 88 的定案（A1–A11、待決事項的答案；`~/.local/share/purdex-lead/drafts/2026-10-20-monitoring-decisions.md`）。
> - 資料由 A 線（lead 1f）供給：`docs/plans/2026-10-20-wwd-monitoring-data-plan.md`（MD-1～MD-5）、`docs/specs/2026-10-20-wwd-host-metrics-spec.md`（G-1～G-4）。
>
> plan：`docs/plans/2026-10-20-monitoring-tabs-plan.md`。行號以 `origin/main abaa46be5` 為準。
>
> **衝突時的優先序**：U1–U3 ＞ 88 的定案 ＞ 設計稿 r3 的畫面（各節的 render 函式）＞ 設計稿附錄的規則文字。附錄有幾條還是 r2 的字（R21 的「其他變淡」、R29 的「狀態列、6 張小圖、告警摘要」），本 spec 照 r3 的畫面改寫，並在那幾條註明。
> 標「88 定案（10-20，Wn）」的，是寫 spec 時查到、原本的定案沒涵蓋或與程式不合的地方，88 於 10-20 逐項定案（W1–W9，§7.3）。

## 1. 範圍

- **收**：
  - 側欄主機列（寬、窄）：主機格、右上角件數、指標、主機上方的佇列條。
  - 監控分頁組（現在的守門員專區，workspace `watch`）：守門員、事件、任務佇列、每台主機一頁（監控｜狀態｜行程｜設定）。
  - 主機外觀與順序改存 Profile Sync（A3）。
  - Hosts 頁拿掉資源池主機與觀察主機（A10）。
  - SPA 端拿掉 fleet／gate.decisions 的讀者，watch.v1 只留守門員的兩條（A11、§7.3 W9）。
- **不收**：網路吞吐、溫度、電池（G4、G6、G15，連佔位都不畫）；iOS（設計稿的範圍只有 Mac App）；告警門檻的寫入（P9 先唯讀）；incident 與值班者狀態的資料來源（S7、S8，A 線）。
- **硬性條件**：
  - 這組頁面與側欄**不讀 fleet store、不讀 SSH 讀數**（使用者 10-20：wwd 不雙軌）。唯一例外是 A3 的一次性外觀搬遷（§4 A3），它在 WWD-9 拿掉 fleet 時一起刪。
  - 本線各期不新增任何 fleet 讀者；每期加一條守門測試看住（plan 共通規則）。
  - 主機的主鍵是 wwd 的 `host_id`（`info.host_id`，16 位 hex，`cmd/wwd/main.go:341-357`），不是 Purdex daemon 的 `host_id`（帶 `:`，`internal/config/config.go:52`），兩者永遠不同。

## 2. 名詞

- **pool 主機**：`GET /api/pool/hosts` 的一列（`wire.PoolHost`）。主鍵 `host_id`；`alias` 用來顯示與當作路由參數（`/api/pool/facts/series?host=<alias>`、`/api/pool/hosts/{alias}/…`）。
- **pool 來源 daemon**：App 讀 `/api/pool/hosts` 的那台 Purdex daemon——依主機順序第一台連著、宣告 `pool.v1` 的（`spa/src/lib/pool/store.ts:45-49`）。它所在的主機叫「本機」。
- **有 daemon 的主機**：某個 HostConfig 的 `/api/info.wwd.host_id` 等於這台 pool 主機的 `host_id`（MD-1，`pool.identity.v1`）；那個 HostConfig 就是它的 daemon。現在是 mlab、air26；mserv、air19 沒有。
- **狀態（liveness）**：`ok`、`wwd_down`（wwd 不回應、主機還在）、`host_down`（wwd 和 22 埠都不回應）、`unknown`（還沒檢查過或檢查失敗）。判法在 M21。
- **在排**：`state` 為 `queued` 且不是保留（`manual` 不是 `hold`）。**保留**：`manual: hold`。**準備中**：`claimed`（被接走、正在準備工作目錄）。**執行中**：`running`。
- **重／輕**：`job.slot`（MD-4，`"heavy"`｜`"light"`）。已宣告的快速通道任務是輕，其他（佇列工作、未宣告的快速通道指令）是重（`wire.JobSlot`，`internal/wwd/wire/usage.go:32-36`）。
- **外觀**：主機的顏色與圖示（含圖示粗細）。**指標**：主機格徽章後面那一格的形狀（環形、單柱、雙柱）與資料項目。
- **件數**：主機格右上角的數字（M6）。**佇列條**：主機格上方整行的那條（M1–M5；窄側欄是 M15 的兩個色塊）。

## 3. 資料來源（每項資料放在哪）

| 資料 | 路由／欄位 | 能力 | 版本 | 用在哪 |
|---|---|---|---|---|
| 主機清單、facts、worker、labels、reachable、`headroom_reasons`、`load` | `GET /api/pool/hosts`；事件 `pool.changed` | `pool.v1`、`pool.assess.v1`、`pool.events.v1` | 已有 | 全部頁面 |
| `liveness`、`liveness_at`、`liveness_since` | 同上（只有對等體帶；本機沒有這三欄，`internal/wwd/daemon/pool.go:275`） | 同上 | issue #3676、PR #3683，763 | 側欄、主機頁、事件 |
| `rtt_ms` | 同上（只有當下值，不進 series） | 同上 | G-4，768（Purdex daemon 也要同版） | 狀態：主機與 wwd |
| 主機對到哪台 daemon | `GET /api/info` 的 `wwd {host_id, alias}` | `pool.identity.v1` | MD-1，765 | 外觀解析、jobs 的來源選擇、推播設定、從主機頁開 session |
| 工作 | `GET /api/pool/jobs`、`/api/pool/jobs/{id}`；事件 `jobs.changed` | `jobs.v1`、`jobs.control.v1` | 已有 | 件數、佇列條、任務佇列、主機頁 |
| 工作的重／輕 | job 的 `slot` | 同上 | MD-4，765 | 佇列條顏色、任務佇列名額格 |
| 開始執行的時間 | job 的 `started_at`（沒開始、回到佇列時缺席） | 同上 | MD-5，766 | 任務佇列「準備中 → 執行中」的計時 |
| 告警 | `GET /api/pool/hosts/{alias}/alerts` → `{host, known, alerts}`；`silence` 告警多一欄 `silence: wwd_down｜host_down｜unknown`；事件 `alerts.changed {host}` | `pool.alerts.v1` | MD-2，765 | 事件、盾牌與事件分頁的數字、主機頁「監控」的告警標記 |
| 觀察、未宣告的重指令 | `GET /api/pool/hosts/{alias}/observations?since=&until=`、`/undeclared?since=`（只有 admin token 能讀） | `pool.observations.v1` | MD-3，765 | 事件、主機頁「行程」 |
| 暫停（例外）與 holds | `GET /api/pool/hosts/{alias}/exceptions` | `pool.control.v1` | 已有 | 任務佇列的原因、主機頁「狀態」「設定」 |
| worker 開關、名額、主人規則；加入／結束暫停 | `PUT /api/pool/hosts/{alias}/worker`、`POST｜DELETE …/exceptions` | `pool.control.v1` | 已有 | 主機頁「設定」 |
| 5 秒一筆的完整 facts（1 小時內） | `GET /api/pool/facts/history?host=<alias>&step=5s` | `pool.v1` | 已有 | 主機頁 5／15／30 分、行程的時間軸、每核 CPU |
| 圖表序列（1 小時以上） | `GET /api/pool/facts/series?host=<alias>&since=&step=` | `pool.series.v1` | 原 8 欄已有；G-1 補的欄位 768 | 主機頁 1 小時、6 小時、24 小時 |
| CPU 細項：`load15`、`user_pct`、`system_pct`、每核 `cores[]`（含 `kind`） | facts；series 帶前三項 | — | G-2，768 | 狀態：CPU |
| 磁碟 IO `disk_io[]` | facts、series | — | G-3，768，沒趕上就 769 | 狀態：磁碟 |
| `procs_total`、`boot_at`、`self {cpu_pct, rss_mb}`、`swap_total_mb`、記憶體分項 | facts；series 除 `boot_at` 外都帶 | — | G-4，768 | 狀態：行程、主機與 wwd；Swap 圖的上限 |
| 推播開關 | 各 daemon 的 host setting `wwd_alerts.push`：`GET /api/hostconfig`、`PUT /api/hostconfig/wwd_alerts` | — | 已有（App 還沒用） | 主機頁「設定」的推播（P10） |
| 外觀與順序 | 新的 Profile Sync settings store `purdex-pool-hosts`（A3）；有 daemon 的主機的顏色與圖示讀寫 `useHostLookStore` | — | 本線 MON-2 | 側欄、主機頁標頭、分頁、任務佇列 |
| 佇列條節數（6／8／10／12） | 這台裝置的 UI 設定（不同步） | — | 本線 MON-3a | 寬側欄 |
| 守門員是誰、醒著沒、叫醒 | `/api/watch/status`、`/api/watch/wake`（WWD-9 保留這兩條，直到 8b，`docs/plans/2026-10-18-wwd-plan.md:447-449`） | `watch.v1` | 已有 | 守門員頁、盾牌 |
| incident 列表、問問題、提議的同意與拒絕、值班者狀態 | 還沒有（S7、S8） | — | A 線 8b；P7 選 (A)：A 線先做 incident 表，由 1f 排 | 守門員頁、盾牌的數字 |

**時程**：MD-1～MD-4 在 765、MD-5 在 766，都已部署。G-1（#3749）、G-2（#3755）、G-4（#3756）已 merge，進 768；G-3（disk_io）目標 768，沒趕上就 769。「狀態」分頁原本標「A 線補」的佔位，768 起有 G-1～G-4，G-3 若沒趕上則 769。新欄位在舊 wwd、舊紀錄上都是 null，App 一律當「這台／這段沒有資料」。

**還沒有、這一版也不等的**（第二波照設計稿寫「等 series API 補欄位」或不畫）：
- series 沒有 worker 名額（`slots`）、PSI、`sessions_room`、常駐程式的數目與記憶體、列出的行程數：1 小時以上的「名額使用」只畫執行中、不畫名額虛線；PSI、session 餘裕、常駐記憶體三張圖寫「等 series API 補欄位」（wwd 的分鐘紀錄有存，留 14 天）。要不要補由 1f 排，不擋本線。
- 每件工作用了多少（G16，`/v1/usage` 只開在 socket）：任務佇列「最近結束」不放用量欄。
- 每分鐘彙總的 stats（G17）：「狀態」不放「今天偏高幾分鐘」。
- 記憶體分項（G9）768 起有資料，設計稿寫「先不畫」，照設計稿；`swap_total_mb` 只當 Swap 圖的上限。
- mserv、air19 送出的工作沒有 `jobs.changed`（S12）：照現行每 10 秒輪詢。
- 等名額中的本機快速通道還沒登記（MD-4 的已知限制，L1）：佇列條實際上不會出現輕工作。

## 4. 架構（A1–A11）

- **A1 主機清單與身分**：清單＝`/api/pool/hosts`。主鍵 `host_id`；`alias` 用於顯示與路由。跟 Purdex daemon 的對應走 MD-1：一個 HostConfig 的 Nex 快取（`useNexHostStore`，`spa/src/lib/nex/nex-host-effects.ts:47-59` 讀 `/api/info` 的地方）多存 `wwd {host_id, alias}`；pool 主機的 daemon＝依主機順序第一個 `wwd.host_id` 相等、連著的 HostConfig。這個對應同時取代：
  - `matchPoolHost` 的 `kind === 'daemon' && (host_id === v.id || host_id === v.daemonId)`（`spa/src/lib/pool/cell-model.ts:37-39`）；
  - jobs driver 的 `ownDaemon`（`spa/src/lib/pool/jobs-driver-live.ts:28`），見下面的「現況缺陷」。
  - 整組頁面與側欄不讀 fleet store、不讀 SSH 讀數。
  - **現況缺陷（MON-1a 修）**：`spa/src/lib/pool/jobs-driver-live.ts:28` 以 `hostState.hosts[id]?.daemonId === h.host_id` 判斷一台 pool 主機自己的 daemon，拿的是 HostConfig 的 `daemonId`（Purdex daemon 的 `host_id`，帶 `:`，`internal/config/config.go:52`）去比 wwd 的 `host_id`（16 位 hex，`cmd/wwd/main.go:341-357`），兩者永遠不相等，所以 `ownDaemon` 一直是空的。結果每個 origin 都交給主機順序第一台 daemon 加 `?host=` 讀：那台自己的 origin 由本機回答（`internal/wwd/daemon/forward.go:41-45`），沒問題；**另一台有 daemon 的主機（air26）送出的工作，則是轉送到對等體拿到的視角，air26 送出的快速通道與未宣告工作在 App 上看不到**（任務佇列、主機頁的工作、分頁沙漏都受影響）。`matchPoolHost` 的 `host_id === v.daemonId` 分支（`spa/src/lib/pool/cell-model.ts:38`）也是同一個比法、永遠不成立，現在能對到全靠 alias。MON-1a 改用 MD-1 的 `/api/info.wwd.host_id` 對應，兩處一起修。
- **A2 狀態（liveness）**：解析 `liveness`、`liveness_at`、`liveness_since`；`poolHostView`（`spa/src/lib/pool/host-view.ts:30-38`）的 fallback 分支拿掉，狀態改成 `ok`／`wwd_down`／`host_down`／`unknown`，不再收 fleet 的輸入。判法見 M21。
- **A3 外觀與順序（P11）**：新的 Profile Sync settings store，storage key `purdex-pool-hosts`：
  ```ts
  interface PoolHostsState {
    order: string[]                        // host_id，側欄與分頁的順序
    looks: Record<string, PoolHostLook>    // key = host_id
  }
  interface PoolHostLook {
    colors?: HostConfig['colors']          // 定案寫 color?（單色），見 W1
    color?: string
    icon?: string
    iconWeight?: IconWeight
    indicator?: { shape: 'ring' | 'bar' | 'bars'; data: IndicatorItem[] }  // ring、bar 用 data[0]；bars 用 data[0]、data[1]
  }
  type IndicatorItem = 'load' | 'cpu' | 'mem' | 'pressure' | 'sysload' | 'disk' | 'slots'
  ```
  - `host_id` 是 wwd 的、每台裝置都一樣，所以這個 store **不需要** local／wire 的換算，也不進 host 改寫（`host-reresolve.ts`、`host-lifecycle.ts`）。跟著 settings 區同步，`SECTION_SCHEMA_ORDINAL.settings` 12 → 13（若別張 PR 先占了 13，順延；以 merge 當下的 `origin/main` 為準）；older ordinal 的 payload 沒有這個 store＝清除（`applier.ts:376` `CLEARED_WHEN_ABSENT`，同 TG-1a-1）。
  - **顏色與圖示的解析順序**：有 daemon 的主機（MD-1 對得到）→ 讀寫那台 Purdex 主機現有的外觀（`useHostLookStore`，經 `lib/host-look.ts` 的 resolver；跟分頁上的主機徽章同一份）；沒有 daemon 的主機（mserv、air19）→ 新 store。**指標一律在新 store**。
  - **一次性搬遷**：沒有 daemon 的主機現在的外觀只在 fleet wire（`lib/fleet/types.ts` 的 `colors`、`icon`、`icon_weight`，經 hostconfig `probe-hosts` 寫）。新 store 沒有這個 `host_id` 的外觀、而 fleet 快照（不 stale）有同 alias 的外觀時，複製一次進新 store，並在這台裝置記下已搬過（不同步的 marker，比照 `lib/host-look-migration.ts`），之後不再讀 fleet。WWD-9 拿掉 fleet 時這段一起刪。
  - **順序**：新 store 的 `order`；沒排到的依 pool 回傳順序接在後面（M13）。
  - 88 定案（10-20，W1）：原定案寫 `{ color?, icon? }`（單色），但現在的徽章與 fleet wire 都是三色組 `colors`（`HostConfig['colors']`，`stores/useHostStore.ts:51`），所以新 store 的外觀欄位與 `HostLookEntry` 相同（`colors`／`color`／`icon`／`iconWeight`）：搬遷不失真、徽章沿用同一個 `resolveHostBadge`、設定頁沿用 Hosts 頁的 `HostColorField`／`HostIconField`。
  - 88 定案（10-20，W2）：設計稿沒有改順序的地方，`order` 一開始是空的。一次性搬遷時也把 fleet 快照的 `order`（現在側欄的順序）換成 `host_id` 寫進 `order`（store 的 `order` 是空的才寫）；之後在監控分頁組拖曳主機分頁就寫回 `order`（MON-6 實作）。
- **A4 右上角件數（R6、R7）**：從 jobs store 算（M6、M7）。側欄掛著時讓 jobs driver 跑（`watchJobs`，`spa/src/lib/pool/jobs-driver-live.ts:63-71`）：有 `jobs.changed` 的主機照事件，沒有事件的主機照現行 10 秒輪詢。
- **A5 佇列條（R1–R5）**：在排的工作，四台合起來，依 `queueEntries` 的顯示順序（`spa/src/lib/pool/jobs-lanes.ts:57-63`）；重／輕依 `job.slot`。寬側欄 M1–M5；窄側欄依 U2（M15）。
- **A6 寬側欄主機格**：徽章（主機顏色＋圖示）＋右上件數＋指標（每台各自的形狀與資料）；依 U3 沒有右下燈號；讀不到 wwd 時依 M17–M19。
- **A7 監控分頁組的分頁**：守門員、事件、任務佇列、每台主機一頁。主機分頁跟著 pool 清單與 A3 的順序；tab id 從 `watch-host-<fleet id>` 改成 `watch-host-<host_id>`，pane content 加 `hostId`（pane content 在 Profile Sync 的 tabs 區只檢查 `kind` 是字串，`lib/profile/sections.ts:167-172`，不升 tabs 的 ordinal）。從 fleet id 換過來時，舊分頁照現行規則收掉（這台裝置看過、之後連續 60 秒不在清單，`features/workspace/lib/watch-area.ts:23`）。換 tab id 與主機頁「監控」同一期（MON-6）；在那之前，側欄按主機格開的是舊的主機分頁（由 `open-watch-area.ts` 用 alias 對到 fleet id，MON-6 刪）。
- **A8 頁面**：主機頁四分頁（監控｜狀態｜行程｜設定）；任務佇列（靜態上下兩排、hover 只高亮路線、其他不變暗、排不下換行）；事件（alerts＋observations）。照設計稿與 §5。
- **A9 守門員頁**依賴 A 線的 S7、S8（incident 路由、值班者狀態），不在第一波；P7 選 (A)，由 1f 排。
- **A10 Hosts 頁**拿掉資源池主機與觀察主機段落，留一行連結到監控（P12）；SSH 備援整個拿掉。這是使用者自己的需求（「這裡的設定取代 Hosts 頁的 wwd 設定」）。
- **A11 fleet 退場**：本線各期都不再新增 fleet 讀者；SPA 端最後一張 PR 拿掉所有 fleet／gate.decisions 讀者，時程跟 1f 的 WWD-9 對齊。88 定案（10-20，W9）：wwd plan（`docs/plans/2026-10-18-wwd-plan.md:447-449`）定了 `/api/watch/status`、`/api/watch/wake` 保留到 8b，`watch.v1` 為這兩條繼續宣告；MON-12 拿掉其他 watch.v1 讀者，**保留 `/api/watch/status`、`/api/watch/wake` 的讀者（守門員頁與盾牌），到 8b 的 incident 路由上線才拿掉**。

## 5. 行為規則

每條附設計稿的規則編號（R1–R35，依附錄順序；設計稿只有 35 條）或來源。**R8 作廢**（U3）；**P14 的「2 排 × 5 節」被 U2 取代**。

### 5.1 側欄（寬）

- **M1**（R1）佇列條在主機格上方、整行。**一節＝一件在資源池排隊、還沒被接走的工作**，四台的佇列合起來，依任務佇列的顯示順序（M26）由左往右。重工作用 `queue-heavy`（紫），輕工作用 `queue-light`（青），空的節用 `border-default`（灰）。重／輕取 `job.slot`；舊回應沒有這欄時當作重（佇列裡的工作本來就不是快速通道）。例：mlab 送了 2 件、air26 送了 1 件，都還沒被接：前 3 節亮，後 7 節灰。
- **M2**（R2、P4）預設 10 節，6／8／10／12 是這台裝置的 UI 設定（不同步）。**超過時多出的不畫節，最右邊那節的右上角疊「+N」**（`CountBadge`），N 是超出的件數。例：排了 14 件：10 節全亮，右上角「+4」。88 定案（10-20，W6）：設定放在「設定 → 終端機」的「主機格指示」旁邊（`components/settings/TerminalSection.tsx:177-181`），存 `useLayoutStore`（它除了 `tabPosition` 都是裝置本機的）。
- **M3**（R3、P6）**被接走的（準備中、執行中）不在條上；保留的也不算**。例：lint 被 air26 接走，條上少一節；把 go-pkg 設成保留，也少一節。
- **M4**（R4）沒有排隊時**整行灰色、不收起**，主機格的位置不跳動。例：早上沒人送工作：一整條灰線還在。
- **M5**（R5）滑過佇列條浮出清單：第一行「資源池排隊 N 件（重 a・輕 b）」，接著前 10 件（序號、重／輕色點、工作 `repo task args`、來源主機、等了多久 m:ss），超過 10 件時加「… 另有 n 件」，最後一行「按一下開任務佇列」。沒有排隊時寫「資源池裡沒有在排隊的工作」。**按一下開監控分頁組的「任務佇列」**。例：滑過看到「1 go-all mlab 0:40」。
- **M6**（R6、P5）**右上角數字＝這台送出、還在排的件數（不含保留）＋在這台準備中或執行中的件數**（不論哪台送出，快速通道也算；以 `claim.worker` 為準）。0 不顯示，超過 99 寫「99+」。狀態不是 ok 時不顯示（M17–M19）。例：mlab 送了 2 件在排、air26 正在跑 mlab 送的 1 件：mlab 顯示 2，air26 顯示 1。
- **M7**（R7）數字平常中性灰；**這台送出、還在排的有一件等超過 2 分鐘 → 黃；有一件沒有任何「worker 開著且標籤符合」的主機能接 → 紅**；紅優先。暫停、名額滿、主人在用、鎖都是暫時的，不算「沒有主機能接」（同 `noHostToGoTo`，`spa/src/lib/pool/jobs-view.ts:77-81`）。例：mlab 送了 at-inwin phpunit（要 `os:linux`），mserv 的 worker 關著：mlab 的數字變紅。
- （**R8 作廢**，U3）主機格右下角**不放任何燈號**，寬窄都一樣。正在跑的件數已經算在 M6 的數字裡。現行的呼吸綠點（`components/fleet/HostCell.tsx:159`）拿掉。
- **M8**（R9）指標顏色：**綜合負載跟著區域上色**（正常綠、偏高黃、過載紅、未知灰）；**其他項目跟著自己的門檻**：CPU、記憶體 70% 黃、90% 紅；磁碟 85%、95%；記憶體壓力等級 1／2／4 → 三分之一綠、三分之二黃、滿格紅（等級 0 改看記憶體使用率）；系統負載（load1 ÷ 核心數）70% 黃、100% 紅；名額使用滿了黃、worker 關著灰。例：mlab 綜合負載 91（被磁碟撐高）但區域正常：環長到 91%，是綠的。
- **M9**（R10、P2）**外觀（顏色、圖示）與指標（形狀、資料）每台各自設定**，在主機頁「設定」的「側欄顯示」（M51），附「套用到四台」（只套形狀與資料）。形狀：環形、單柱顯示一項，雙柱顯示左右兩項；資料：綜合負載、CPU、記憶體、記憶體壓力、系統負載、磁碟、名額使用。改了，側欄、主機頁標頭、監控分頁組的分頁、任務佇列立刻一起變（有 daemon 的主機，分頁標籤上的主機徽章也是同一份）。例：把 mserv 設成紅色伺服器圖示、雙柱 CPU＋記憶體：四個地方的 mserv 都換掉。
- **M10**（A6）寬側欄每台一格：主機徽章（`HostBadge`，側欄徽章設定的大小、主機的顏色與圖示）、右上角件數（M6）、徽章後面一格的指標（M8、M9）。格子照現行的排法（`components/fleet/FleetCells.tsx:32`：flex-wrap，排不下換行）。
- **M11**（設計稿第 2 節的卡片）**滑過主機格浮出卡片**：標題列是徽章、名稱、區域字。讀數依序：綜合負載（含主因）、CPU、系統負載（load1／核心數）、記憶體（%，可用 GB）、記憶體壓力（Linux 附 PSI some10／full10）、換頁（頁／秒）、Swap（GB）、磁碟（最滿那顆 %，剩 GB）；指標用到的列加粗並標「環」或「柱」。下半部：Worker（開・重 a/b・輕 c/d，或「不接工作」）、可接（重、輕；重為 0 時寫原因）、工作（這台送出在排 N・在這台跑 M）、Session（還能開約 N 個）、wwd（版本・讀數時間）、連線（「正常・最後檢查 hh:mm:ss」；本機寫「正常・本機的 wwd」）。狀態不是 ok 時換成 M17–M19 的卡片。
- **M12**（設計稿第 1 節）**按主機格開監控分頁組裡那台的主機分頁、「監控」子分頁**（A7 的時程：MON-6 之前開的是舊主機分頁）。
- **M13**（A3）**主機的順序**：外觀 store 的 `order`；`order` 沒列到的 pool 主機依 `/api/pool/hosts` 的回傳順序接在後面；`order` 有、pool 沒列的不顯示。側欄、監控分頁組的主機分頁、任務佇列、事件的主機篩選都用這個順序。例：`order` 是 [mlab, air26]、pool 回 mlab、mserv、air26、air19：顯示 mlab、air26、mserv、air19。

### 5.2 側欄（窄）

- **M14**（U1）**窄側欄的主機格不顯示主機圖示**，每台是一塊「四項狀態」小圖：四根直條，依序 CPU、記憶體、記憶體壓力、磁碟，各自照 M8 的門檻上色（壓力等級 0 改看記憶體使用率）。這塊的**底色是主機顏色的淡色**，與現在徽章的底色相同（`resolveHostBadge(look, 'terminal')` 的 light 層，`spa/src/lib/host-color.ts:309`）；沒設顏色的主機用中性底。**件數 badge 壓在這塊的右上角**（M6、M7）。辨識主機靠底色與滑過的卡片（M11，往右浮出）；按一下同 M12。指標的形狀與資料（M9）只用在寬側欄。例：mlab 綠色淡底、四根直條，有 2 件在排時右上角「2」。
- **M15**（U2；**取代 P14**）**窄側欄的佇列條換成兩個色塊＋數字**：重（`queue-heavy` 底）、輕（`queue-light` 底）各一塊，數字是在排的件數（M1、M3 的同一組工作，不受節數設定影響）；**0 件的那塊用中性灰底、數字照寫 0**；位置固定在主機格上方、不收起。滑過與按一下同 M5。寬側欄維持 M1–M5 的節條。例：重 3 件、輕 0 件：紫底「3」、灰底「0」。
- **M16**（88 定案（10-20，W5）：U1 沒寫這種情況）窄側欄的主機狀態不是 ok 時：四根直條換成 M17–M19 的同一個圖示（置中），小圖的淡化比照徽章（`wwd_down` 不淡、`host_down` 40%、`unknown` 60%），不顯示件數。

### 5.3 讀不到 wwd（不讀 SSH 數字；#3683 的 22 埠存活探測）

- **M17**（R11）**wwd 停了、機器還開著**（`wwd_down`：22 埠有回應，或 HTTP 有回應但不是那台 wwd 的答案）：寬側欄的**徽章照常亮，指標的位置換成黃色斷線圖示**（Phosphor `LinkBreak`，`status-warning`），不顯示讀數與件數。卡片寫「wwd 沒有回應（主機還在）」、最後一筆讀數的時間（多久前）、最後檢查時間，以及「worker 與名額不明；它在跑的工作約 1 分鐘後（租約到期）回到原來的佇列」。例：mserv 的 wwd 08:12 停了、機器還開著：mserv 那格是亮的徽章＋黃色斷線；滑過寫「最後一筆讀數 08:12:39・最後檢查 08:20:55」。
- **M18**（R12）**整台停了**（`host_down`）：**徽章淡到 40%，指標位置換成紅色電源圖示**（`Power`，`status-error`）；卡片寫「主機離線」與最後一筆讀數的時間。例：air19 闔上螢幕睡著：air19 那格變淡＋紅色電源。
- **M19**（R13）**還沒檢查過、或檢查本身失敗**（`unknown`）：**徽章淡到 60%、灰色問號**（`Question`，`text-muted`）；卡片寫「狀態不明」，並說明下一次檢查後會變成上面兩種之一。例：mlab 的 daemon 剛重啟、還沒探測：讀不到 wwd 的那台先顯示灰色問號。
- **M20**（R14）**最後檢查時間**（`liveness_at`）出現在主機格的卡片、主機頁的橫幅、事件頁那一列；**狀態是 ok 時不顯示「最後檢查」**。wwd 每 5 秒輪詢一次對等體、讀不到時每一輪都看 22 埠（`internal/wwd/daemon/pool.go:18`、`:105-121`），卡片寫「每 5 秒」（設計稿寫的「每 30 秒」不對）。Purdex daemon 的 `pool.changed` 不因 `liveness_at` 移動而通知（`internal/module/pool/events.go:73`），所以**有一台不是 ok 時，App 在事件模式下也每 5 秒讀一次 `/api/pool/hosts`**，最後檢查時間才不會停住。例：mserv 恢復後，主機格回到正常的環，卡片不再有「最後檢查」。
- **M21**（A2）**狀態怎麼判**：
  - 有 `liveness` 欄的（對等體）：`wwd_down`、`host_down`、`unknown` 照原值；`ok` 而且 facts 在 30 秒內（`LIVE_MS`）→ ok。
  - 沒有這欄的（本機，或還不帶這欄的 daemon）：`reachable` 而且 facts 在 30 秒內 → ok；否則 `unknown`。
  - 88 定案（10-20，W3）：`liveness` 是 `ok` 但 facts 缺或超過 30 秒（wwd 回應了、讀數卻停了，現行的 `stale`）→ 當 `unknown`，卡片寫「wwd 有回應，但讀數停在 hh:mm:ss」。
  - 「最後一筆讀數」的時間由 App 記（`usePoolStore` 新的 `lastFactsAt`，以 `host_id` 為 key，取代以 alias 為 key 的 `lastLiveAt`），讀不到時照樣顯示。
- **M22**（R15）主機頁：wwd 停了是**黃色橫幅**、主機離線是**紅色橫幅**、狀態不明是灰色橫幅，寫明是哪一種、最後一筆讀數與最後檢查時間；量表顯示「—」；**圖停在最後一筆讀數，之後畫同色斜線**。例：打開 mserv 頁：黃色橫幅「mserv：wwd 沒有回應，主機還開著」，30 分鐘的圖最後 8 分鐘是斜線。
- **M23**（R16）事件頁**分成兩種事件**：「wwd 沒有回應（主機還在）」黃色斷線、「主機離線」紅色電源，左邊有同色豎線；恢復時寫「已恢復 hh:mm」。來源是 `silence` 告警的 `silence` 欄（MD-2）。例：air19 06:31 睡著、07:19 醒來：一列「主機離線・已恢復 07:19」。
- **M24**（R17）任務佇列：**wwd 停了的那台，它的佇列池寫「讀不到」**（工作存在那台的 wwd 裡）；它**在跑的工作約 1 分鐘後（租約到期）回到原來的佇列重排**，等待時間照算。例：mserv 正在跑 phpunit 時 wwd 停了：約 1 分鐘後 phpunit 回到 mlab 的佇列。
- **M25**（88 定案（10-20，W4）：設計稿沒涵蓋）**整個資源池讀不到**（沒有一台 daemon 讀得到 `/api/pool/hosts`）：側欄保留這次 App 啟動以來最後一次讀到的清單，每台畫成 `unknown`，卡片寫「讀不到資源池（{原因}）」，佇列條整行灰；App 啟動後從沒讀到過時不畫主機格（同現在沒有 fleet 時）。

### 5.4 任務佇列（分頁原名「工作」）

- **M26**（R18）佇列裡的順序是**顯示順序，不保證下一個被接**：手動「最前」→ 分數（release > merge > dev、等越久越前、來源主機偏高或過載加分、同一個 session 排越多越後面）→ 手動「最後」→ 保留（`displayOrder`，`spa/src/lib/pool/jobs-view.ts:30-55`）。例：1f 送 release 的 go-all，會排到 hana 的 merge spa-related 前面。
- **M27**（R19）版面是**上下兩排**：上排「佇列池」每台一格（工作留在送出那台）；下排「工作池」只列 worker 開著的主機，格子是名額（重、輕），裡面是它正在準備或執行的工作，徽章是來源。worker 關著的主機不出現，下方寫一行「{主機} 的 worker 關著，打開後出現在工作池」。例：mserv 的 worker 打開：工作池多一格 mserv。
- **M28**（R20）**畫面是靜態的**：沒有飛入、彈跳、閃爍、呼吸燈；資料更新時數字直接換掉。（需求原文要「分配、等待等動作要有微動畫」；設計稿 r3 改成靜態，使用者已核准。）例：air26 接走 go-changed：它從 mlab 的佇列池消失、出現在 air26 的重名額格，沒有動畫。
- **M29**（R21，依 r3 的使用者意見與 A8 改寫）上下之間的細線**只畫有工作在路上的**（某台佇列的工作正在某台準備或執行），平常淡淡的。**滑到一件工作，只有它的路線高亮成來源主機的顏色，其他工作與連線維持原樣、不變暗**（R21 原文的「其他變淡」作廢）。滑到排隊中的工作，畫虛線到能接它的主機（worker 開著、標籤符合；P18）。滑到佇列池或工作池的標頭，亮起那一格相關的所有路線。例：滑到 air26 格子裡的 spa-file：一條線從 mlab 的佇列池連下來，其他線不變。
- **M30**（r3 的使用者意見）佇列池那排、工作池那排**寬度不夠時各自換行**；換行之後連線仍接到正確的格子，不穿過別的格子（走格子間的空隙與兩側），視窗縮放或字型載入後依實際版面重算。例：視窗縮到約 1000 px：佇列池折成兩行，mserv 的連線從第二行繞下來。
- **M31**（R22）主機不接新工作時，**名額格寫「不接」，下面寫原因**：名額已滿、壓力偏高、主人在用、暫停到 hh:mm（名稱）、舊閘門名額滿（#3450，WWD-9 後消失）。例：mlab 暫停到 16:21：mlab 的重名額寫「不接」，下面寫「重：暫停到 16:21（watcher-766）」。
- **M32**（R23）佇列池的最下面寫**為什麼還在等**（同樣原因的合成一行，多件時前面寫件數）；等超過 2 分鐘時間變黃；**沒有主機能接寫紅字**。例：mlab 的 8 件：「8 件等：mlab 暫停到 16:21・air26 舊閘門名額滿」。
- **M33**（R24）**取消正在跑的工作要確認，排隊中的直接取消**（沿用 WWD-7 Q5）。例：按 lint 的「⋯ → 取消」：還在排就直接取消；已經在跑會先問。
- **M34**（設計稿第 5 節）頂端一列摘要：排隊 N（重 a・輕 b）、執行中、準備中、最久等、保留 N，右端一條 10 節的佇列條（M1）；下方「最近結束」5 件（時間、結果、工作、來源 → 執行的主機、跑多久、類別・重輕）。7b1 的歷史表、詳情、「⋯」選單沿用。準備中 → 執行中的計時讀 `started_at`（MD-5），沒有時從 `claim.since` 算。分頁標籤上的沙漏按一下，開「任務佇列」並選好那件工作（設計稿第 1 節的入口）。

### 5.5 事件與守門員

- **M35**（R25）事件頁列兩種：**wwd 告警**（各台 `/alerts`，由 wwd 依門檻升起與解除）與**觀察**（`/observations`，每台最新 3 筆，其餘一行連到那台主機頁的「行程」）。篩選：主機、來源（全部／wwd 告警／觀察）、狀態（全部／進行中／已結束）。依日期分段，最新在上。**App 上沒有「關掉事件」的按鈕**。例：mlab 的「dev 孤兒 4 個」是摘要級告警，孤兒清掉後自己變成「已結束」。
- **M36**（R26）**盾牌與「事件」分頁上的數字＝進行中的推播級告警＋值班者還沒處理完的事**。值班者那一半在 S7 上線前一律是 0。例：mlab 磁碟快滿（推播）＋你問的一個問題還在處理：數字 2。
- **M37**（R27、P10）推播只在**「推播到手機」打開時**送；關著時事件照樣記在事件頁。開關是一個總開關（M50）。例：推播關著時 mserv 反覆重啟：手機不響，事件頁有一筆。
- **M38**（R28；依賴 S7）顧問的**提議要你同意的（清磁碟這類）出現在守門員頁，有「同意／不要」**；回收 broker、開 issue 這類由負責的程式自己檢查後執行，只顯示結果。例：顧問提議清掉 go-build 快取：守門員頁那一列有兩個按鈕。
- **M39**（設計稿第 3 節；依賴 S7、S8）守門員頁：值班卡（現在是 lucia：在哪台、哪個 tmux session、模型、從何時值班；8b 之後是「顧問」：平常沒有常駐、事件來了開一個 session）、「打開對話」「叫醒」「問問題」、進行中的 incident（8b 之後）、處理紀錄（今天／7 天；每件寫發生什麼、怎麼處理、誰處理、用了多少）。名稱「顧問」是 P8，未定。
- **M40**（設計稿第 1 節）入口：盾牌有數字開「事件」、沒有開「守門員」；佇列條開「任務佇列」；主機格開那台的「監控」；分頁標籤上的沙漏開「任務佇列」並選好那件工作。

### 5.6 主機頁與設定

- **M41**（R29，依 r3 改寫；A8）主機頁分四個子分頁：**監控**、**狀態**、**行程**、**設定**。標頭：徽章（20 px）、名稱、區域字（或 M17–M19 的狀態）、`wwd 版本・平台・讀數時間`，右邊是子分頁切換與時間範圍（只在監控、狀態）。**監控**由上到下只有三段（R29 原文的「狀態列、6 張小圖、告警摘要」是 r2，作廢）：
  1. **一排量表**（`components/watch/Gauge.tsx`）：CPU、記憶體、記憶體壓力、換頁率、磁碟、名額使用（worker 關著寫「關」）；有未處理的告警時，這排最右邊放一個小標記「N 件告警」（有推播級時紅，否則黃），按一下開「事件」並篩這台。
  2. **六張指標圖，3 欄 × 2 列**（`MetricChart` 的卡片樣式，壓矮：圖表區約 90 px），標題與大字目前值在同一列：CPU 使用率、系統負載（附核心數門檻線）、記憶體、換頁／Swap、磁碟剩餘（附 `disk_min_gb` 10 GB 門檻線）、名額使用（重／輕執行中，名額虛線）。
  3. **工作與行程，一個區塊、左中右三欄**：左欄是工作（在這台執行、從這台送出排隊中最多 3 件＋「還有 n 件在排」、最近在這台結束，最後一個「這台的全部工作」連結到任務佇列並篩這台）；中欄 CPU 前 5 名；右欄記憶體前 5 名（PID、行程名、session／工作標記、數值）。
  目標：1440 × 900 的視窗（標題列與分頁列以下約 826 px）一頁看完。例：想知道 mlab 為什麼不接重工作：監控的名額量表寫「0/2」，卡片與「狀態」的「判斷」寫「暫停到 16:21」。
- **M42**（R30）每張圖都有**單位、門檻虛線、時間軸**；滑鼠移到任一張圖，**同一分頁的圖都顯示同一個時間點**（大數字或圖例換成那一點的值）。例：移到 mlab 15:30：六張小圖的大數字都換成 15:30 的值。
- **M43**（R31，依 768 的資料更新）範圍：5 分、15 分、30 分讀 5 秒一筆的 history；1 小時、6 小時、24 小時讀 1 分鐘 series（設計稿的 24 小時是因為快照只抓了 6 小時才停用，正式環境照開）。**series 還沒給的項目寫「等 series API 補欄位」**（§3 最後一段）。A 線補的指標（每核 CPU、user／system、load15、磁碟 IO、行程總數、開機時間、wwd 自身、主機之間的延遲）768 起有資料（磁碟 IO 可能 769），之前在「狀態」用虛線框佔位、標「A 線補」；**監控不放佔位**。網路、溫度、電池不收，連佔位都沒有。主機之間的延遲只有當下值（`rtt_ms` 不進 series），畫成數字不畫線。例：切到 6 小時：768 之前 Swap、負載、名額使用三張小圖寫「等 series API 補欄位」。
- **M44**（設計稿第 6 節「狀態」）上方六張卡：判斷（區域與它的依據、可接重／輕與原因、session 餘裕、綜合負載拆成五項並標主因）、Worker 與主人、連線（liveness：現在、從何時、最後檢查、失聯紀錄；本機寫「本機：daemon 直接讀自己的 wwd，不做存活探測」）、暫停、版本與身分（wwd 版本・proto・facts v、平台、`host_id`、有沒有 Purdex daemon）、標籤／工具／fingerprint。接著「facts 全部欄位」兩欄表，最後「完整圖表」依 CPU、記憶體、磁碟、資源池、行程、主機與 wwd 分組（監控那六張不重複），最上面一條區域色帶。
- **M45**（設計稿第 6 節「行程」）行程表（CPU 前 12、記憶體前 12，含歸屬；可拉時間軸看 1 小時內每 15 秒一筆）、常駐程式（只列出、不砍）、未宣告的重指令（7 天，依 signature 分組）、觀察紀錄（24 小時、依 id 去重、最新在上，預設 10 筆、可展開；往前翻用 `next_until`）。
- **M46**（R32）改 worker 開關、名額後，**先顯示「已送出，等這台回報」，下一筆讀數帶回新值才算數**；15 秒沒回來就提示。例：把 mlab 的重名額改成 2：先顯示送出中，約 5 秒後變回正常。
- **M47**（R33）**關掉 worker 時正在跑的會跑完**，確認框寫件數。例：air26 正在跑 1 件時關掉：確認框寫「正在跑的 1 件會跑完」。
- **M48**（R34）「暫停」只能設**有結束時間的**（1／3／8 小時、自訂）；可以選擋重或重＋輕、仍放行哪些類別（release、merge、dev）、標記（發版中；mserv 另有「預期壓力」）；到時間自動恢復。例：發版前暫停 mlab 8 小時、只放行 release：其他工作都改去別台。
- **M49**（P9、P16）**告警**：門檻寫死在 wwd，**先唯讀**（顯示值，輸入框停用）；觀察門檻（CPU 60 秒、記憶體 1024 MB）同樣唯讀。兩種失聯各一個開關：「wwd 沒有回應（主機還在）」與「主機離線」，**air19 的「主機離線」預設關**。88 定案（10-20，W8）：P9 要先開放這兩個開關，但現在沒有寫入 API——wwd 只有每台一個 `alert_unreachable`（`*bool`，config 檔），A 線的 MD plan 把「每台兩種開關可寫」歸 S9、延後（`docs/plans/2026-10-20-wwd-monitoring-data-plan.md:150`）。**兩種失聯推播開關的寫入 API 由 88 請 1f 排（A 線小項）；上線前這兩列唯讀顯示現值。**
- **M50**（P10）**推播**：一個總開關，寫每台有 daemon 的主機的 `wwd_alerts.push`（`PUT /api/hostconfig/wwd_alerts`，現在是 mlab、air26）；兩台不一致時開關顯示「部分開啟」並列出各台，按一下兩台都設成同一個值。沒有 daemon 的主機頁寫「{alias} 沒有 Purdex daemon，它的告警由 mlab 推（mlab 停擺時由 air26）」。
- **M51**（R10、P2）**側欄顯示**：顏色、圖示、指標形狀、指標資料（雙柱有左右兩個），附即時預覽、「形狀與資料套用到四台」與「回到預設」。預設：環形＋綜合負載。有 daemon 的主機改顏色與圖示，寫的是那台 Purdex 主機的外觀（A3），分頁上的主機徽章一起變。88 定案（10-20，W7）：現在有一個全部主機共用的「主機格指示」（`useUISettingsStore.fleetCellIndicator`，`components/settings/TerminalSection.tsx:177-181`）。第一波的每台預設值由它換算（環 → 環形＋綜合負載；雙柱 CPU＋壓力 → 雙柱 CPU＋記憶體壓力；雙柱 CPU＋記憶體 → 雙柱 CPU＋記憶體），使用者現在的選擇不會被重設；本分頁上線時（MON-9a）拿掉那個全域設定。
- **M52**（R35、A10、P12）**Hosts 頁不再有資源池主機與觀察主機**；主機的 wwd 設定只在這裡。Hosts 頁留一行「資源池主機的狀態與設定在監控分頁組」連過來。SSH 備援、SSH 別名、SSH 連線檢查都拿掉。例：要改 mserv 的名額：側欄點 mserv → 設定。

## 6. 推翻既有規格（原文 → 新文）

設計稿附錄「推翻既有規格」逐列（行號以 `origin/main abaa46be5` 重新核對；wwd spec 在設計稿依據的 `57df5d379` 之後有改，§10 以後的行號移了 46～85 行，其他三份沒變），加上 U1–U3 與寫 spec 時查到的。

| 檔案：行 | 原文（摘要） | 新文 |
|---|---|---|
| `docs/plans/2026-10-18-wwd-plan.md:229-233` | 7a-2：主機格右上角的數字維持現狀，WWD-7 不加數字也不加顏色；之後整個重新設計 | 右上角數字＝這台送出還在排＋在這台準備中或執行中；平常灰、等超過 2 分鐘黃、沒有主機能接紅（M6、M7） |
| `docs/plans/2026-10-18-wwd-plan.md:229-230`（U3） | 7a-2：worker 有在跑時，徽章右下角一個呼吸綠點 | 拿掉；右下角不放燈號，寬窄都一樣（R8 作廢） |
| `docs/plans/2026-10-18-wwd-plan.md:387-389` | Q2：the cell's top-right count stays as it is today（「暫時不動，後續整個調整」） | 就是這次的「整個調整」：數字依 M6；主機格上方加佇列條（M1–M5，窄側欄 M15） |
| `docs/plans/2026-10-18-wwd-plan.md:238-243` | 7a-3：Hosts 頁「觀察主機」改「資源池主機」列四台；總覽變表格；資源池主機頁；沒有 daemon 的主機留「樣式與 SSH 備援」；Fleet 編輯器留到 WWD-9 | Hosts 頁**拿掉**資源池主機段落、資源池主機頁與觀察主機頁，留一行連結（M52）；內容搬到主機頁的監控與設定；樣式搬到「側欄顯示」（M51）；**SSH 備援整個拿掉** |
| `docs/plans/2026-10-18-wwd-plan.md:244-252` | 7a-4：主機分頁「狀態」＝標頭、可接列、facts 量表、區域色帶、行程表、常駐；1 小時以上用 App 記憶體的歷史；「重指令排隊」留著不顯示，WWD-9 拿掉 | 主機分頁改成「監控｜狀態｜行程｜設定」（M41–M45）；1 小時以上讀 1 分鐘 series；「重指令排隊」直接拿掉 |
| `docs/plans/2026-10-18-wwd-plan.md:253` | 7b1-2：守門員專區裡一個固定分頁「工作」（`watch-jobs`） | 分頁改名「**任務佇列**」，內容改成上排佇列池、下排工作池、中間的路線（M26–M34）；歷史表、詳情、「⋯」選單沿用 7b1 |
| `docs/plans/2026-10-18-wwd-plan.md:390` | Q3：the jobs list is a tab in the watcher area（「工作」） | 名稱改「任務佇列」（使用者 10-20 的用詞） |
| `docs/plans/2026-10-18-wwd-plan.md:278-279` | 7b1-5：worker 設定在主機分頁「設定」的最上面；守門員那段（watch 列）收合留在下面直到 WWD-8 | 「設定」：接工作、名額、主人、暫停、告警、推播、側欄顯示（M46–M51）；連線與主機資訊移到「狀態」；**watch 列整段拿掉** |
| `docs/plans/2026-10-18-wwd-plan.md:216-226` | 一個主機狀態 `poolHostView`：live／fallback（fleet 的 SSH 讀數）／offline／舊 daemon；pool 主機與 fleet 主機以 alias 對（daemon 主機也用 host_id） | **fallback 拿掉**；狀態是 `ok`／`wwd_down`／`host_down`／`unknown`＋最後檢查時間（M17–M21）；「舊 daemon」也拿掉；對 daemon 用 MD-1 的 `wwd.host_id`（A1） |
| `docs/plans/2026-10-18-wwd-plan.md:321` | Fallback readings and the gate: the fleet store as today, until WWD-9 | 這組頁面**完全不讀 fleet store**；骨架由 `/api/pool/hosts` 給 |
| `docs/specs/2026-10-18-wwd-spec.md:24` | D4：Hosts 頁的「觀察主機」改成「資源池主機」，列出四台 | Hosts 頁不列資源池主機；四台都在監控分頁組。D4 是使用者的決定；使用者 10-20 的需求原文（「這裡同時取代 Hosts 頁的設定，Hosts 頁不再需要設定 wwd 主機」）與 r3 的核准即是同意（A10） |
| `docs/specs/2026-10-18-wwd-spec.md:870-873` | §10：pool 讀不到某台時，那台改用舊的 SSH 探測讀；App 不能只因為 wwd 不在就把主機顯示成離線 | **不再用 SSH 讀數字**。讀不到 wwd 時只對 22 埠做 TCP 存活探測（#3683），結果放在 pool 主機資料。「不能只因為 wwd 不在就顯示離線」的精神保留：wwd 停了是黃色的「wwd 沒有回應（主機還在）」，只有 22 埠也不通才是紅色的「主機離線」 |
| `docs/specs/2026-10-18-wwd-spec.md:1139` | silence 告警：推播級；每台一個 `alert_unreachable`（air19 關）；key 是 `silence:<peer>` 與 `partner_down` | 依 MD-2 的 `silence` 欄分成兩種：「wwd 沒有回應（主機還在）」與「主機離線」；主機設定各一個開關，air19 的「主機離線」預設關（M49；寫入要 A 線的 API，W8） |
| `docs/specs/2026-10-18-wwd-spec.md:1130`、`:1154-1155` | App 對讀不到 wwd 的主機顯示 "wwd offline"，即使 §10 的 SSH 備援還讀得到 facts | 沒有 SSH 備援了；App 分三種顯示：「wwd 沒有回應（主機還在）」「主機離線」「狀態不明」，各自的圖示、顏色與最後檢查時間（M17–M20） |
| `docs/specs/2026-10-16-watcher-area-spec.md:12` | U1：分頁依序是守門員、事件、每台主機一頁 | 依序是守門員、事件、**任務佇列**、每台主機一頁；主機分頁跟著 `/api/pool/hosts` 與 A3 的順序（不再跟 fleet） |
| `docs/specs/2026-10-16-watcher-area-spec.md:70-72` | §3：主機分頁跟著 fleet 的主機清單（新的接在最後、使用者的拖曳順序保留）；tab id 是 fleet id | 跟著 pool 清單；tab id 改用 wwd 的 `host_id`（A7）；順序見 W2 |
| `docs/specs/2026-10-16-watcher-area-spec.md:14`、`:20` | U3／U9：事件不由人標為已處理；守門員確認時留一行說明；計數是「等守門員處理」 | 保留「人不能關事件」；事件改成 wwd 告警（依門檻自己解除）＋觀察；數字改成「進行中的推播級告警＋值班者還沒處理完的事」（M35、M36） |
| `docs/specs/2026-10-16-watcher-area-spec.md:15` | U4：主機分頁「狀態」＝五項讀數各一張折線圖（歷史只在 App 記憶體）、前幾名行程、daemon 主機的排隊「當下｜歷史」 | 四個子分頁（M41–M45）；歷史讀 wwd；排隊段拿掉，改看任務佇列 |
| `docs/specs/2026-10-16-watcher-area-spec.md:16` | U5：排隊歷史加四欄（平均核心數、記憶體峰值、換頁拖累、磁碟讀寫） | 舊閘門的決策歷史丟掉（1f 10-20）。每件工作用了多少改由 wwd 的 job usage 提供（G16，未提供），放在任務佇列「最近結束」 |
| `docs/specs/2026-10-16-watcher-area-spec.md:17`、`:21` | U6：設定存在各 daemon（watch 列、gate 的 `[gate]`）；每台 daemon 有「資源管制」開關。U10：守門員調名額沒有上下限 | gate 設定、資源管制開關、watch 列都拿掉（WWD-9）；名額是 wwd worker 的名額（`pool.control.v1`），四台都在主機頁「設定」改 |
| `docs/specs/2026-10-16-watcher-area-spec.md:25-36` | §2.1 盾牌：狀態來自 watch status（未啟動、讀不到、醒著、休息中）＋未處理事件數 | 盾牌的狀態讀值班者（lucia 期間仍是 `/api/watch/status`；8b 之後是 S8）；數字依 M36 |
| `docs/specs/2026-10-16-watcher-area-spec.md:45-56` | §2.3 代理圖示上的沙漏：來源是 fleet 的 `slow.queue.waiters[].session` | 來源只剩 pool 的工作（`submitter.session`；WWD-7b1-6 已經兩邊都讀，`hooks/useTabQueued.ts`），fleet 那半在 MON-12 拿掉；按一下開任務佇列並選好那件工作（M40） |
| `docs/specs/2026-10-16-watcher-area-spec.md:78-98` | §4：守門員分頁就是守門員 session 的完整分頁；沒有守門員時顯示啟動面板 | 改成值班卡＋處理紀錄（M39）；完整 session 分頁是「打開對話」之後的畫面；8b 之後列進行中的 incident |
| `docs/specs/2026-10-16-watcher-area-spec.md:100-128` | §5：事件種類 `unmanaged_heavy`、`pressure_high`、`slot_orphaned`、`slot_long`（watch.v1），E1 標籤 | 事件是 wwd 的告警種類（disk_low、disk_falling、pressure_held、thrash、resident_growth、orphans、worker_crash_loop、silence、mserv_service）＋觀察；篩選是主機、來源、狀態（M35） |
| `docs/specs/2026-10-16-watcher-area-spec.md:130-167` | §6、§6.1：標頭「量測：{primary}（主）」；五個量表、每項一張圖、前 5 行程；「重指令排隊」當下｜歷史 | 同 U4 那列（M41） |
| `docs/specs/2026-10-16-watcher-area-spec.md:168-189` | §6.2 daemon 主機設定（設定組、資源管制、gate 設定、守門員 watch 列）；§6.3 probe 主機設定（樣式、SSH 別名） | 主機頁的「設定」子分頁，四台同一個版面（M46–M51）；沒有 daemon 的主機的 wwd 設定經 pool 來源 daemon 轉送 |
| `docs/specs/2026-10-14-fleet-status-spec.md:241-244` | 主機徽章：probe 主機的外觀來自 wire；離線時徽章 40% | 外觀依 A3（有 daemon 讀 `useHostLookStore`，沒有讀新 store）；淡化依狀態（M17–M19） |
| `docs/specs/2026-10-14-fleet-status-spec.md:245-248` | 主機格右上角的排隊數字：只有 daemon 主機、只在有人等舊閘門時；2 分鐘或 3 人黃，放行被擋紅 | 依 M6、M7（wwd 的工作，四台都有） |
| `docs/specs/2026-10-14-fleet-status-spec.md:249-260` | 指標由「設定 → 終端機 → 主機格指示」決定（環／雙柱 CPU＋壓力／雙柱 CPU＋記憶體），全部主機一個設定 | 指標搬到主機頁「設定」的「側欄顯示」，**每台各自**選形狀（環形、單柱、雙柱）與資料（P2，附「套用到四台」）；全域設定在 MON-9a 拿掉（W7） |
| `docs/specs/2026-10-14-fleet-status-spec.md:264` | 窄側欄：主機格直排（徽章＋環） | 直排不變，但每台改成不帶圖示的四項狀態小圖（U1，M14）；上方的佇列條是兩個色塊＋數字（U2，M15；取代設計稿 P14 的 2 排 × 5 節） |
| `docs/specs/2026-10-14-fleet-status-spec.md:285-297` | §8.2 觀察主機有自己的頁：樣式與連線、連線檢查；總覽的觀察主機清單 | 整段拿掉；樣式搬到主機頁設定，**SSH 別名與 SSH 連線檢查一起拿掉**（10-20 定案）；主機活著沒有，看 #3683 的存活探測 |
| `docs/plans/2026-10-20-wwd-monitoring-data-plan.md:18` | S1 主機順序、S4 主機外觀：存 App profile，以 wwd 的 host_id 為 key，B 線做 | 細化：順序與指標存新 store；顏色與圖示，有 daemon 的主機仍在 `useHostLookStore`（A3） |
| `docs/plans/2026-10-20-wwd-monitoring-data-plan.md:150` | S15 的每台開關可寫：歸 S9，不在 WWD-9 前提內 | P9 要先開放兩種失聯的推播開關：請 1f 排（W8），不擋第一波 |

## 7. 待決事項

### 7.1 已定案（88，照設計稿 ASK）

| # | 問題 | 定案 |
|---|---|---|
| P2 | 指標每台各自或一個設定套全部 | 每台各自，附「套用到四台」（M9、M51） |
| P4 | 超過節數寫「+4」或「14」 | 「+N」（M2） |
| P5 | 右上角的數字算什麼 | 照設計：這台送出還在排＋在這台準備中或執行中（M6） |
| P6 | 佇列條算不算保留 | 不算（M3） |
| P7 | lucia 時期的處理紀錄從哪來 | (A) A 線先做 incident 表，由 1f 排 |
| P9 | 告警門檻能不能在 App 改 | 先唯讀；失聯推播的開關開放（M49；API 見 W8） |
| P10 | 推播開關放哪 | 加一個總開關（M50） |
| P11 | 外觀與順序存哪 | A3 |
| P12 | Hosts 頁完全拿掉或留連結 | 留一行連結（M52） |
| P14 | 窄側欄的佇列條 | 被 U2 取代（M15） |
| P16 | air19 的「主機離線」要不要推播 | 照預設：關（M49） |
| P17 | wwd 停了時主機格要不要灰顯最後一筆讀數 | 不顯示（M17） |
| P18 | 排隊中的工作 hover 畫什麼 | 照設計：虛線到能接的主機（M29） |

### 7.2 尚未定

- **P8**：8b 之後的名稱用不用「顧問」（spec §13.5 待使用者確認）。不擋。
- **S7、S8**：incident 路由與值班者狀態（A 線 8b；P7 (A) 由 1f 排）。守門員頁（MON-11）與盾牌數字的值班者那一半等它。
- **S9 的兩種失聯開關寫入**（W8，88 請 1f 排）、**series 的剩餘欄位**（§3 最後一段）：A 線排，不擋本線。

### 7.3 寫 spec 時查到、88 已定案（10-20）

| # | 事項 | 結論 |
|---|---|---|
| W1 | A3 的外觀欄位寫 `color?`（單色），現行徽章是三色組 `colors` | 新 store 用 `HostLookEntry` 同樣的欄位，搬遷不失真、徽章與設定控制項沿用（A3） |
| W2 | 設計稿沒有改順序的地方；`order` 一開始是空的；監控分頁組的主機分頁本來可以拖 | 搬遷時把 fleet 快照的順序寫進 `order`（空的才寫）；拖主機分頁寫回 `order`（MON-6） |
| W3 | `liveness: ok` 但 facts 缺或超過 30 秒 | 當 `unknown`，卡片寫讀數停在何時（M21） |
| W4 | 整個資源池讀不到 | 保留這次啟動以來最後的清單，全部畫 `unknown`（M25） |
| W5 | 窄側欄主機不是 ok 時怎麼畫（U1 沒寫） | 圖示取代四根直條，淡化比照徽章（M16） |
| W6 | 佇列條節數的設定放哪 | 「設定 → 終端機」主機格指示旁，存 `useLayoutStore`（M2） |
| W7 | 第一波沒有每台的指標設定畫面 | 每台預設由現行全域「主機格指示」換算；MON-9a 拿掉全域設定（M51） |
| W8 | 兩種失聯推播開關沒有寫入 API | 寫入 API 由 88 請 1f 排（A 線小項）；上線前唯讀顯示現值（M49） |
| W9 | A11 要拿掉 watch.v1 讀者，但 wwd plan 保留 `/api/watch/status`、`/wake` 到 8b | MON-12 保留這兩條的讀者（守門員頁與盾牌），到 8b 的 incident 路由上線才拿掉（A11） |

## 8. 分期總表

一期一張 PR（≤ 800 行或 ≤ 20 檔，測試也算）。第一波細到 task（plan）；第二波在 plan 只寫到期，開工前 plan 再細化一版。

| 期 | 內容 | 依賴 | 可否並行 | 檔數／行數（估） |
|---|---|---|---|---|
| **MON-1a** | 資料：`job.slot` 解析；`/api/info.wwd` 存進 Nex 快取；pool 主機 → daemon 的對應（MD-1）；`matchPoolHost` 與 jobs `ownDaemon` 改用它 | — | 與 1b、2a 並行 | 16／520 |
| **MON-1b** | 資料：`liveness`、`liveness_at`、`liveness_since`（與 `rtt_ms`）解析；新的主機狀態模型（沒有 fallback）；store 的最後讀數改以 `host_id` 為 key、非 ok 時 5 秒讀；狀態的字 | — | 與 1a、2a 並行 | 11／470 |
| **MON-1c** | 所有 `poolHostView` 的讀者改用新模型；刪 fallback（`poolHostView`、`fallbackWord`、HostCell 的斷線角標） | 1a、1b | — | 20／460 |
| **MON-2a** | 外觀與順序 store `purdex-pool-hosts`：Profile Sync（ordinal 13、collector、applier、absent＝清除、收斂測試） | — | 與 1a、1b 並行 | 15／600 |
| **MON-2b** | 外觀解析（daemon 主機 → `useHostLookStore`，其他 → 新 store）、排序、從 fleet 一次性搬遷外觀與順序 | 1a、2a | 與 1c、3a 並行 | 7／450 |
| **MON-3a** | 寬側欄的佇列條（M1–M5）、件數與顏色的計算（M6、M7）、側欄掛著就跑 jobs driver、節數設定、`queue-heavy／light` 色 token | 1a | 與 1c、2b 並行 | 19／640 |
| **MON-3b** | 寬側欄主機格與卡片改用新模型與新外觀：每台指標、liveness 圖示、沒有右下燈號、新卡片；側欄不讀 fleet 的守門測試 | 1c、2b、3a | — | 20／780 |
| **MON-4** | 窄側欄：四項狀態小圖（U1）、兩個色塊＋數字（U2）、非 ok 的畫法 | 3b | — | 10／430 |
| MON-5 | 任務佇列分頁重做（M26–M34）、`started_at`、沙漏入口 | 1a、3a | 與 6–10 並行 | 約 2 期 |
| MON-6 | 主機頁骨架與「監控」（M41–M43）；主機分頁 tab id 改 `host_id`（A7）、拖曳寫 `order` | 1c、2b、3b | 與 5、10 並行 | 約 2 期 |
| MON-7 | 主機頁「狀態」（M44）、完整圖表 | 6；G-1～G-4（768）、G-3（768／769） | 與 8、9 並行 | 約 2 期 |
| MON-8 | 主機頁「行程」（M45） | 6；MD-3 | 與 7、9 並行 | 1 期 |
| MON-9 | 主機頁「設定」（M46–M51）＋Hosts 頁瘦身（M52） | 6、2b；W8 的 API（只擋兩個開關） | 與 7、8 並行 | 2 期（9a、9b） |
| MON-10 | 事件分頁（M35–M37）、盾牌與分頁上的數字 | 1a、1b；MD-2、MD-3 | 與 5、6 並行 | 1～2 期 |
| MON-11 | 守門員分頁（M38、M39） | A 線 S7、S8 | — | 1～2 期 |
| MON-12 | fleet 退場（A11）：拿掉 fleet／gate.decisions 讀者與 A3 的搬遷，watch.v1 只留守門員兩條（W9） | 5–10；1f 的 WWD-9 | 最後 | 1～2 期（純刪除只算新增行） |
