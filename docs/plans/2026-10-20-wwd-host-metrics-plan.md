# wwd 主機指標補齊（G 項）— plan

> spec：`docs/specs/2026-10-20-wwd-host-metrics-spec.md`（介面契約 §2 已經 B 線 88 同意，2026-10-20 16:3x）。lead 1f。
> 一期一張 PR（≤800 行／20 檔）。G-1 先做；G-2、G-3、G-4 彼此獨立，可以並行，但都排在 G-1 之後（共用 G-1 擴充過的 `SeriesPoint` 與 `SeriesOf`）。
> 執行者：Sonnet member，TDD，每個 task 一個 commit。
> 修訂 1（16:5x）：照 codex plan 審查（task-mvgey3o3-hupi2r）的 14 條修正；對照表在文末。

## 共通規則（每期都適用）

- **相容**：wire 只加可為 null 的欄位（指標或 slice；缺席與 `null` 同義）。不升 `FactsProto`，新欄位不受 `v` 的版本閘門影響：`Rollup`（`internal/wwd/facts/ring.go`）在最後一筆 `v < FactsProto` 時會清掉 proto 2 欄位（`DecompressRate`、`Queue`），新欄位**不可**加進那份清單。每期都要有「同一分鐘混 v1、v2 紀錄、最後一筆是 v1」的 Rollup 測試，證明新欄位照 spec §2.4 聚合。
- **`Clone`**（`internal/wwd/facts/clone.go`）：新增的指標與 slice 一律深拷貝；`reflect_test.go` 照樣要過（若以反射列舉欄位，新欄位要在它的清單裡）。
- **Rollup**：照 spec §2.4。平均只對「有值的那幾筆」取；整分鐘都沒有值就是 null。
- **`SeriesOf`**（`internal/wwd/wire/series.go`）是 wwd 與 Purdex 退路共用的唯一投影；series 的新欄位只在這裡加，`internal/module/pool/series.go` 不用改。Purdex 的 series 路由是 `/api/pool/facts/series?host=<alias>`。
- **速率與比例**：新增的速率／比例欄位保存上一筆的累計值**與取樣時間**，以實際間隔換算每秒；第一筆、計數器倒退、來源（磁碟、核心）新出現或消失後重現時為 null（重新建立基準），不回 0 或負數。現有的記憶體速率（第一筆為 0，`decompress_test.go` 鎖定）不改。
- **Validate**：wire 有列舉值的新欄位（`cores[].kind`）要在 `Facts.Validate` 檢查。
- **不 fork**：新收集器只用 sysctl、Mach、purego dlopen、`/proc`、`/sys`；G-1 的守門測試會擋。
- **測試分層**：開發中只跑改到的套件（`go test ./internal/wwd/facts/` 等，經入口走本機快速通道）。PR 前：`pdx test go-changed <改到的套件…>` 與 `pdx test lint`，兩個 job 的 snapshot tree 要等於 PR head 的 tree。另跑一次 Linux 交叉檢查：`GOOS=linux GOARCH=amd64 go vet <改到的套件>`（經閘門）。完整套件不跑。
- **mutation**：只針對新增的測試，一次一條，`go test -timeout 60s`，前後 `git status` 為空。
- **成本**（PR 說明附實測）：`bench_darwin_test.go` 的取樣 benchmark 前後對照；一筆分鐘紀錄（`json.Marshal(Rollup(...))`）的大小前後對照。預算：取樣增加 < 0.1% 單核，分鐘紀錄 < +2 KB。超過就停下回報 lead。部署後 lead 量 mlab 上 wwd 一分鐘的 CPU 秒數（部署腳本已印），與上一版對照。
- **codex**：R1 → R2（攻擊 → critic），`--model gpt-5.6-sol`，effort 維持 low；prompt 附「不要執行完整測試或建置；最多跑一個指名的套件」「不要對上層目錄 find／rg」與 Claude 側的測試證據。

## G-1：series 帶出 Facts 已有的欄位；不 fork 守門

檔案：`internal/wwd/wire/series.go`、對應的 series 測試檔、`internal/wwd/facts/nofork_test.go`（新）。

1. **測試先行（series）**：一筆 Facts 帶齊 load1／load5、`mem.swap_used_mb`、`compressor_mb`、`swap_out_rate`、`decompress_rate`、`worker.running`、`queue.queued`、兩顆 disk，`SeriesOf` 的點要帶出 spec §2.3 的 `load1`、`load5`、`swap_used_mb`、`compressor_mb`、`swap_out_rate`、`decompress_rate`、`worker_running`、`queue_queued`、`disks[{mount, used_pct, free_gb}]`；`queue` 缺席時 `queue_queued` 為 null；`mem` 的指標為 nil 時對應欄位為 null；現有 8 個欄位不變（既有測試照過）。
2. **實作**：`SeriesPoint` 加欄位（JSON 名照 spec §2.3），`SeriesOf` 填值；指標欄位要拷貝值再取址，不可指向來源紀錄。
3. **測試**：JSON 往返——舊格式的 point（只有 8 欄）以 `DecodeLenient` 解得開；新欄位缺席時解出 nil。
4. **不 fork 守門**：`nofork_test.go` 讀 `internal/wwd/facts/` 與 `internal/hostmem/` 下所有非 `_test.go` 的 `.go` 檔，出現 `exec.Command`、`execstat.`、`os.StartProcess`、`syscall.ForkExec`、`syscall.Exec` 任一字串就失敗並列出檔名。**例外清單**寫在測試裡、每項附理由：`facts/tools.go`（問工具版本，快取 10 分鐘，不在每次取樣上）、`hostmem/vmstat.go`（只給舊的 gate／resources／fleet／monitor，wwd 不呼叫，WWD-9 移除）。先用 `rg` 確認現況只有這兩個檔案命中；若有第三個，停下回報 lead。mutation：在一個非例外的來源檔暫時加 `_ = exec.Command` 要轉紅；把 tools.go 從例外拿掉要轉紅。
5. **文件**：wwd 文件中 series 的欄位表（若有）補上新欄位。

## G-2：CPU 細項

檔案：`internal/hostmem/mach_darwin.go`（`CPUTicks`）與其非 darwin stub、`internal/wwd/facts/sources_darwin.go`、`linux_parse.go`、`sources_linux.go`、`collector.go`（`RawCPU` 與組裝）、`internal/wwd/wire/facts.go`（`CPU`、`Validate`）、`clone.go`、`ring.go`、`wire/series.go`、新檔 `internal/wwd/facts/cpukind_darwin.go`＋`cpukind_other.go`。

1. **load15**：macOS `loadavg()` 已讀 24 位元組的 `vm.loadavg`，回傳第三個值；Linux `ParseLoadavg` 取第三欄。測試：固定的位元組／文字樣本。`wire.CPU.Load15 *float64`。
2. **全機與每核 user／system**：
   - macOS：`hostmem.CPUTicks` 目前把每核 user／system／idle／nice 加總成 busy、total；改成也回傳每核四種 tick（保留現有的 busy／total，呼叫端不變）。
   - Linux：`ParseCPUStat` 目前只讀 `cpu` 行；改成也回傳 `cpu` 行的 user、nice、system、irq、softirq，以及每個 `cpuN` 行的同一組欄位。
   - 比例＝兩次取樣的 tick 差：`user_pct = Δ(user+nice)/Δtotal×100`，`system_pct = Δ(system[+irq+softirq])/Δtotal×100`；每核同法，每核 `idle_pct` 用該核的 idle（Linux 含 iowait，與全機 `idle_pct` 一致）。第一筆、Δtotal ≤ 0、計數器倒退、核心數變動時為 null。
   - 測試：兩份固定 tick 算出預期比例，含核心數變動、計數器倒退。
3. **`cores[].kind`**（只有 macOS）：啟動時讀一次 IORegistry `IODeviceTree:/cpus` 下每個 `cpuN` 節點的 `cluster-type`（`E`／`P`），用 `owner_darwin.go` 已 dlopen 的 IOKit／CoreFoundation（purego），結果快取。對應方式：節點的 `logical-cpu-id`（或 `cpu-id`）屬性對上 `host_processor_info` 的索引；任一核對不上、屬性缺漏、或數量與 `ncpu` 不符 → 全部 kind 為 null。**不依編號順序推測**。測試：純函式（屬性表 → kind 陣列）用固定輸入測，含缺漏與數量不符；另加 darwin 限定對照測試，用 `ioreg -l -p IODeviceTree -n cpus`（測試檔可以 exec）比對，讀不到就 skip。
4. **wire**：`CPU` 加 `Load15 *float64`、`UserPct *float64`、`SystemPct *float64`、`Cores []Core`（`Core{UserPct, SystemPct, IdlePct *float64; Kind *string}`），JSON 名照 spec §2.1。`Facts.Validate`：`kind` 只能是 `"p"`、`"e"` 或 null；測試涵蓋其他字串被拒。
5. **Rollup**：load15、user、system 平均；`cores` 逐核平均（核心數不一致的分鐘取最後一筆的核心數，只平均同索引有值的），kind 取最後一筆。混 v1／v2 的測試（共通規則）。
6. **series**：`load15`、`cpu_user_pct`、`cpu_system_pct`；**不帶 cores**（spec §2.5）。
7. **成本**：benchmark 與分鐘紀錄大小（10 核約 +0.5 KB）。

## G-3：磁碟 IO

檔案：新檔 `internal/wwd/facts/diskio.go`（共用速率）、`diskio_darwin.go`、`diskio_linux.go`、`linux_parse.go`（`/proc/diskstats` 解析）、`collector.go`、`wire/facts.go`（`DiskIO`）、`clone.go`、`ring.go`、`wire/series.go`。

1. **共用速率**：`diskio.go` 依 device 保存上一筆的累計值（bytes、ops）**與取樣時間**，以實際間隔算 `read_bps`、`write_bps`、`read_iops`、`write_iops`；第一筆、device 新出現、計數器倒退、device 消失後以同名重現時，該 device 的速率為 null（device 仍列出）。測試：固定兩筆累計值，間隔用 5 秒與非 5 秒（例如 7.3 秒）各一；消失再重現；倒退。
2. **Linux**：解析 `/proc/diskstats`（reads completed、sectors read、writes completed、sectors written；sector 固定 512 bytes）。實體磁碟＝`/sys/block/<name>/device` 存在者（排除分割區、loop、ram、zram、dm、md）；`/sys` 讀不到時整個 `disk_io` 為 null。測試：真實的 diskstats 樣本（nvme0n1 與其分割區、sda／sda1、loop0、dm-0）配一個假的 `/sys/block` 目錄。
3. **macOS**：purego 呼叫 IOKit：`IOServiceMatching("IOBlockStorageDriver")` → 逐一取 `Statistics` 字典的 `Bytes (Read)`、`Bytes (Write)`、`Operations (Read)`、`Operations (Write)`；device 名稱取其子節點中 `Whole=true` 的 `IOMedia` 的 `BSD Name`（例如 `disk0`），找不到 `Whole=true` 的就略過該 driver。所有 IOKit／CF 物件要 release。測試：字典轉值寫成純函式用固定輸入測；darwin 限定對照測試用 `ioreg -c IOBlockStorageDriver -r -l`（測試檔可以 exec）比較 device 清單，讀不到就 skip。
4. **wire**：頂層 `DiskIO []DiskIO`（`{Device string; ReadBps, WriteBps, ReadIOPS, WriteIOPS *float64}`），JSON 名照 spec §2.1。
5. **Rollup**：device 聯集，各自平均有值的取樣。混 v1／v2 測試。
6. **series**：`disk_io`。
7. **成本**：特別量 macOS 的 IOKit 列舉耗時（每 5 秒一次）；超過 1 ms 就回報 lead。

## G-4：行程總數、開機時間、wwd 自身、peer 往返、swap 總量、記憶體分項

檔案：`internal/wwd/facts/procs.go`（`ProcSnapshot`）、`procs_darwin.go`、`procs_procfs.go`、`sources_darwin.go`、`sources_linux.go`、`linux_parse.go`、`internal/hostmem/mach_darwin.go`（`VMCounters`）、`collector.go`、`wire/facts.go`、`wire/api.go`（`Peer`、`PoolHost`）、`internal/wwd/daemon/pool.go`、`internal/module/pool/module.go`（組 `PoolHost` 的地方）、`clone.go`、`ring.go`、`wire/series.go`。

1. **procs_total**：`ProcSnapshot` 加 `Total int`＝這次列舉的原始筆數（macOS `readTable()` 的列數；Linux `/proc` 的數字目錄數），包含讀取失敗或 PID 不穩定而沒進 `Procs` 的。在既有的那一次掃描裡計數，不另外掃。Collector 從 snapshot 取。測試：假列舉來源，含讀取失敗的項目。
2. **boot_at**：macOS `unix.SysctlTimeval("kern.boottime")`（啟動讀一次、快取）；Linux 讓 `ProcfsReader` 以方法公開已解析的 `btime`，Collector 經來源介面取得。測試：固定 btime；macOS 的 timeval 轉換用純函式測。
3. **self**：`syscall.Getrusage(RUSAGE_SELF)` 的 user+system 時間差 ÷ 實際取樣間隔 → `cpu_pct`（一核＝100），第一筆為 null；`rss_mb`：macOS 用 `hostmem` 既有的 `proc_pid_rusage` 取自己，Linux 讀 `/proc/self/statm`。測試：假 rusage 兩筆。
4. **swap_total_mb**：macOS `swapUsedBytes` 已讀的 `xsw_usage` 一併回傳 total（offset 0）；Linux `SwapTotal`。
5. **記憶體分項**：macOS `VMCounters` 從同一次 `host_statistics64` 取 `active_count`（byte offset 4）、`wire_count`（12）、`external_page_count`（**136**，xnu `vm_statistics64`）；讀取的長度檢查提高到至少 140 bytes。測試：**合成 buffer**（在各偏移放已知值）驗證解析；另加 darwin 限定的合理性測試（各分項 ≤ 總記憶體），不以 `vm_stat` 的數字逐一比對（非同時取樣）。Linux 解析 `Active`、`Inactive`、`Cached`；`wired_mb` 固定 null。
6. **peer rtt_ms**：`daemon/pool.go` 的 `pollPeer` 在送出 `GET /v1/facts` **之前**開始計時、收到回應後停止（不沿用函式開頭、`/v1/info` 之前的 `started`），存進該 peer 的狀態，`/v1/pool` 輸出 `rtt_ms`；失敗或自己為 null。Purdex：`internal/module/pool/module.go` 組 `wire.PoolHost` 的地方帶出 `rtt_ms`。測試：假 clock／假 transport（info 慢、facts 快時，rtt 只等於 facts 那段）；Purdex 端一個轉送測試。
7. **wire**：照 spec §2.1、§2.2。
8. **Rollup**：照 spec §2.4（procs_total、rss、swap_total 最大；記憶體分項平均；boot_at 最後一筆）。混 v1／v2 測試。
9. **series**：`procs_total`、`self_cpu_pct`、`self_rss_mb`、`swap_total_mb`、`mem_wired_mb`、`mem_active_mb`、`mem_inactive_mb`、`mem_file_cache_mb`。

## 完成後

- 每期 merge 後由 lead 決定進哪一版。部署照現行順序：**每台先升 Purdex daemon、再升 wwd**（舊 Purdex 會用舊型別解碼並丟掉新欄位）。G-4 的 `rtt_ms` 需要 Purdex 程式一起出版；其他欄位 Purdex 不必改程式，但要升到同版才會轉送。
- 通知 B 線 88：哪一版起有哪些欄位。
- WWD-9 plan 要寫明：9e 刪的 `host_minutes` 是 Purdex `resources.db` 的，不是 wwd.db 的分鐘紀錄；`gate.WorkingTree`／`SnapshotTree` 在 9b 拆 `internal/gate` 前要搬出。

## codex plan 審查（task-mvgey3o3-hupi2r）處理對照

| # | 嚴重度 | 內容 | 處理 |
|---|---|---|---|
| 1 | critical | 不 fork 守門現況就會紅（tools.go、vmstat.go） | G-1 #4 加例外清單與理由；範圍改「每次取樣的路徑」 |
| 2 | critical | `Core` 的比例是 float64，表示不了 null | G-2 #4 改 `*float64` |
| 3 | critical | rtt 計時點在 `/v1/info` 之前 | G-4 #6 改在 facts 請求前後計時 |
| 4 | critical | `rtt_ms` 沒送到 Purdex（`PoolHost` 手動組） | G-4 #6 加 `module/pool/module.go`；完成段改寫 |
| 5 | important | Purdex series 路由寫錯 | spec §2.3、共通規則改 `/api/pool/facts/series?host=` |
| 6 | important | 現有記憶體速率第一筆是 0 | spec §4、共通規則：只對新欄位要求 null，現有不改 |
| 7 | important | FactsProto 版本閘門的交互 | 共通規則：新欄位不進閘門清單，每期混 v1／v2 測試 |
| 8 | important | procs_total、btime 沒有現成接線 | G-4 #1、#2 加 `ProcSnapshot.Total` 與 btime 公開方法 |
| 9 | important | 速率沒存取樣時間 | 共通規則、G-3 #1：存時間、非 5 秒間隔與重現的測試 |
| 10 | important | 實體磁碟判定不嚴 | spec §2.1、G-3 #2、#3：Linux `/sys/block/<name>/device`；macOS `Whole=true` |
| 11 | important | `kind` 沒有 Validate | G-2 #4 加 Validate 與測試 |
| 12 | important | external_page_count 偏移與長度檢查 | G-4 #5：offset 136、長度 ≥140、合成 buffer 測試 |
| 13 | important | 部署順序 | 完成段與 spec §4：每台先 Purdex daemon 再 wwd |
| 14 | important | 漏了 wwd 每分鐘 CPU 的對照 | spec §4、共通規則：部署後由 lead 量 |
