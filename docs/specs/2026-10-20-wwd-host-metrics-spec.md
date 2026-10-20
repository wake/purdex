# wwd 主機指標補齊（G 項）— spec

> 2026-10-20 lead 1f。來源：使用者「按照你評估重要的項目收」，B 線 88 的清單（主機詳細頁）。介面契約（§2）經 88 同意（16:3x）；plan 在 `docs/plans/2026-10-20-wwd-host-metrics-plan.md`，經 codex 審查（task-mvgey3o3-hupi2r）後修訂。

## 1. 範圍

- **收**：一、series 補上 Facts 已有的欄位；二、新收集 CPU 細項、磁碟 IO、行程總數、開機時間、wwd 自身用量、peer 往返時間。
- **不收**：網路吞吐、溫度、電池。
- **硬性條件**：一律在 wwd 行程內讀（sysctl、Mach、purego dlopen、/proc、/sys），**每次取樣都會跑的路徑不 fork、不 exec**。這一條加一個守門測試（§4）。現有的兩個例外照舊、寫進守門測試的例外清單：`facts/tools.go` 問工具版本會 fork，但結果快取 10 分鐘；`hostmem/vmstat.go` 只給舊的 gate、resources、fleet、monitor 用，wwd 不呼叫（WWD-9 移除）。
- **只做 Go 側**（wwd、wire、Purdex 轉送；series 沿用 wire 型別，pool 的 `PoolHost` 是手動組的，`rtt_ms` 要在 Purdex 補一行）。SPA 的型別與解析由 B 線在畫圖時一起改；新欄位在舊 wwd、舊紀錄上都是 null，App 以 null＝「這台／這段沒有資料」處理。

## 2. 介面契約（給 88 審）

原則：**所有新欄位都可為 null**（JSON 為 `null` 或缺席，兩者同義＝不知道：舊 wwd、舊的分鐘紀錄、該平台讀不到）；`0` 是量到的零。命名照現有慣例：snake_case，單位寫在名字裡。比例一律 0–100。速率一律「距上一筆取樣的平均每秒」。

### 2.1 Facts（`GET /v1/facts`、pool 的 `peers[].facts`、分鐘 rollup 的整筆紀錄）

| 位置 | 欄位 | 型別 | 意思 |
|---|---|---|---|
| `cpu` | `load15` | number\|null | 15 分鐘 load（已讀到、原本丟掉） |
| `cpu` | `user_pct` | number\|null | 全機 user（含 nice）佔全部核心時間的比例 |
| `cpu` | `system_pct` | number\|null | 全機 system（Linux 含 irq、softirq）比例；`idle_pct` 維持現有定義（Linux 含 iowait） |
| `cpu` | `cores` | array\|null | 每核一筆、依核心編號排序：`{user_pct, system_pct, idle_pct, kind}`，三個比例各自可為 null（第一筆、計數器倒退、核心數變動）；`kind` 是 `"p"`（效能核）、`"e"`（節能核）或 null。macOS 啟動時從 IORegistry 每顆 CPU 的 `cluster-type` 讀一次（行程內 IOKit）；讀不到確定的對應就是 null，**不依核心編號的順序推測**；Linux 一律 null |
| 頂層 | `disk_io` | array\|null | 每顆**實體磁碟**一筆：`{device, read_bps, write_bps, read_iops, write_iops}`。`device` 是系統的名字（macOS `disk0`；Linux `nvme0n1`、`sda`）。實體的判定：Linux 是 `/sys/block/<name>/device` 存在者（排除分割區、loop、ram、dm、md，避免邏輯裝置與底下的磁碟重複計）；macOS 是 `IOBlockStorageDriver` 底下 `Whole=true` 的 `IOMedia`。跟 `disk[]`（依掛載點）分開，因為 APFS 的卷與實體磁碟不是一對一 |
| 頂層 | `procs_total` | integer\|null | 取樣當下主機上的行程總數 |
| 頂層 | `boot_at` | RFC 3339\|null | 主機開機時間 |
| 頂層 | `self` | object\|null | wwd 自己：`{cpu_pct, rss_mb}`；`cpu_pct` 以一顆核心為 100 |
| `mem` | `swap_total_mb` | integer\|null | swap 總量（macOS `vm.swapusage` 的 total、Linux `SwapTotal`，同一次讀取，原本沒存） |
| `mem` | `wired_mb` | integer\|null | macOS wired（`vm_statistics64.wire_count`）；Linux 沒有對應值，一律 null |
| `mem` | `active_mb`、`inactive_mb` | integer\|null | macOS `active_count`、`inactive_count`；Linux `Active`、`Inactive` |
| `mem` | `file_cache_mb` | integer\|null | macOS `external_page_count`（檔案頁）；Linux `Cached` |

記憶體分項只收「原本那次讀取裡就有的」：macOS 是讀 available 的同一次 `host_statistics64`（`internal/hostmem/mach_darwin.go`，`vm_statistics64` 的 active @4、wire @12、external_page_count），Linux 是同一份 `/proc/meminfo` 多解析幾個鍵。

### 2.2 pool（`GET /v1/pool` 的 `peers[]`，Purdex `/api/pool/hosts`）

| 欄位 | 型別 | 意思 |
|---|---|---|
| `rtt_ms` | number\|null | 這台 wwd 上一次輪詢該 peer 時，**只計 `GET /v1/facts` 那一個請求**的往返時間（不含先前的 `/v1/info`）；自己、連不上時為 null。不另外 ping。Purdex 的 `/api/pool/hosts` 由 `wire.Peer` 手動組成 `wire.PoolHost`，要一起帶出 |

### 2.3 series point（wwd `GET /v1/facts/series`、Purdex `GET /api/pool/facts/series?host=<alias>`）

現有 8 個欄位不變，新增（全部可為 null）：

| 欄位 | 來源 |
|---|---|
| `load1`、`load5`、`load15` | `cpu.*` |
| `cpu_user_pct`、`cpu_system_pct` | `cpu.*` |
| `swap_used_mb`、`compressor_mb`、`swap_out_rate`、`decompress_rate` | `mem.*` |
| `worker_running` `{heavy, light}` | `worker.running` |
| `queue_queued` | `queue.queued` |
| `disks` `[{mount, used_pct, free_gb}]` | `disk[]`（現有的 `disk_max_used_pct` 保留；`free_gb` 是為了畫 `disk_min_gb` 的門檻線） |
| `disk_io` `[{device, read_bps, write_bps, read_iops, write_iops}]` | `disk_io` |
| `procs_total` | `procs_total` |
| `swap_total_mb`、`mem_wired_mb`、`mem_active_mb`、`mem_inactive_mb`、`mem_file_cache_mb` | `mem.*` |
| `self_cpu_pct`、`self_rss_mb` | `self.*` |

`boot_at`、`rtt_ms` 不進 series（只看當下值）。**每核（`cpu.cores`）不進 series**：當下每核看 `/api/pool/hosts` 的 facts，1 小時內的變化讀 5 秒一筆的 `/facts/history`（完整紀錄本來就帶 `cpu.cores`）；分鐘 rollup 裡照 §2.4 平均存著，以後要用再開。

### 2.4 分鐘 rollup 的取值規則（`step=1m`）

| 欄位 | 規則 |
|---|---|
| load、各 CPU 比例、每核比例、磁碟 IO 速率、`self.cpu_pct` | 該分鐘平均 |
| `procs_total`、`self.rss_mb`、`swap_used_mb`、`compressor_mb`、`swap_total_mb` | 該分鐘最大 |
| `wired_mb`、`active_mb`、`inactive_mb`、`file_cache_mb` | 該分鐘平均 |
| `disk_io` 的磁碟清單 | 該分鐘出現過的磁碟聯集，每顆對自己出現過的取樣平均 |
| `boot_at` | 最後一筆 |

（現有欄位的規則不變：idle、load 平均，available 最小，pressure 與各種 rate 最大，queue 與 running 取峰值。）

### 2.5 88 的決定（2026-10-20 16:3x）

1. 每核不進 series，也沒有 `?cores=1` 參數。
2. series 的 `disks` 帶 `{mount, used_pct, free_gb}`。
3. 採納 88 的建議：`cores[].kind`（見 §2.1）。

## 3. 分期（一期一張 PR，≤800 行／20 檔）

| 期 | 內容 | 平台 |
|---|---|---|
| **G-1** | series 補 §2.3 中「Facts 已有」的欄位（load1／load5、mem 四項、worker_running、queue_queued、disks）；不 fork 守門測試 | 不分 |
| **G-2** | CPU：`load15`、`user_pct`、`system_pct`、`cores`（含 `kind`）；rollup；series 的 `load15`、`cpu_user_pct`、`cpu_system_pct` | macOS（`hostmem` 的 `host_processor_info` 已取每核 tick，只是被加總）、Linux（`/proc/stat` 的 `cpu`、`cpuN` 行） |
| **G-3** | 磁碟 IO：`disk_io`；rollup；series | macOS（IOKit `IOBlockStorageDriver` 的 Statistics，purego，照 `facts/owner_darwin.go` 已 dlopen IOKit 的作法）、Linux（`/proc/diskstats`） |
| **G-4** | `procs_total`、`boot_at`、`self`、peer `rtt_ms`、`swap_total_mb`、記憶體分項；rollup；series | macOS（`kern.proc.all` 的筆數、`kern.boottime`、`getrusage`、`vm.swapusage` 的 total、`vm_statistics64` 的分項）、Linux（`/proc` 數目錄、已解析的 `btime`、`getrusage`、`/proc/meminfo` 多解析幾個鍵） |

G-1 不需要新收集，最先做；G-2 到 G-4 互不依賴，可並行。

## 4. 共通要求

- **不 fork 守門**（G-1）：掃 `internal/wwd/facts` 與取樣器的非測試檔，出現 `exec.Command`、`execstat.`、`os.StartProcess`、`syscall.ForkExec` 就紅。
- **成本**：每期 PR 說明附 `Sample()` benchmark 前後對照與一筆分鐘紀錄的大小前後對照；部署後由 lead 量 mlab 上 wwd 一分鐘的 CPU 秒數，與上一版對照（部署腳本已印 wwd 相隔一分鐘的兩次 CPU 時間）。預算：取樣成本增加 < 0.1% 單核，每筆分鐘紀錄 < +2 KB。
- **相容**：wire 只加可為 null 的欄位（765 起全體讀者都是 DecodeLenient）；分鐘紀錄是整筆 JSON，不需要資料表遷移。`Clone` 要深拷貝新增的指標與 slice。不升 `FactsProto`：新欄位不受 `v` 的版本閘門影響（取樣器在 proto 2 的條件不成立時會寫 `v=1`，Rollup 依最後一筆的版本清掉 proto 2 欄位；新欄位不在那份清單裡）。
- **部署順序**：每台先升 Purdex daemon、再升 wwd（現行做法）。舊 Purdex 會用舊的 wire 型別解碼並丟掉新欄位，wwd 先升的那段時間 App 會看不到新欄位，但不會壞。
- **速率的第一筆**：新增的速率與比例欄位在沒有上一筆可比時為 null，不是 0；以實際的兩筆取樣間隔換算每秒，不假設 5 秒。現有的記憶體速率（`page_out_rate` 等）維持現行行為（第一筆為 0，已有測試鎖定），這次不改。
- **WWD-9 註記**：WWD-9e 要刪的 `host_minutes` 是 Purdex `resources.db` 的那張，不是 wwd.db 的分鐘紀錄（同名）；WWD-9 plan 要寫明。
