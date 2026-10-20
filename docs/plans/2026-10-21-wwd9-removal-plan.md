# WWD-9：移除舊系統（gate、assist、watch 事件、fleet 資料面、resources）— plan

> 2026-10-21 lead 1f（Opus）。依據：
> - 盤點 `docs/plans/2026-10-21-wwd9-inventory.md`（以下稱「盤點」，第 9 節 PR 表、第 11 節 lead 裁定）與三份附錄（同目錄 `-appx-9ab`／`-appx-9cd`／`-appx-9ef-cross`，逐檔 file:line）。
> - wwd plan `docs/plans/2026-10-18-wwd-plan.md` 的 WWD-9 與「Switch to wwd」兩節；spec `docs/specs/2026-10-18-wwd-spec.md`（§13.3 alert、1089–1158 送達者與叫醒）。
> - 使用者 10-20 定案：wwd 不與舊系統雙軌；切換優先、有問題在正式環境邊修；S4（本 plan）緊接 S2、S3，不等 C3。
> - 10-21 兩份唯讀查證（S0 前提、9d 設計事實），摘要在 1.1。
>
> **修訂 1**（codex plan review `task-mvh9fsh0-1pdxmo`，14 條，對照表在文末）：W3 與 S0 同版；保留 fleet 的角色選舉、只刪資料面（取代原 1.3「誰在誰叫」）；9d 的刪除順序改成每張都能編譯；S0 的部署步驟、CAS、測試矩陣補齊；9c 列出必須留下的符號。另併入：B 線版次（88，10-21）、守門員對 worker CLI 的需求與巡邏的替代（lucia，10-21，新增 S3-3）。
>
> **修訂 2**（codex 增量複審 `task-mvh9trld-fzid5w`：原 14 條中 9 條確認解決；3、7、10、11、13 未解決，另提 15–18 新問題）：D0 的 waiter 改成關卡；ack／log／config CLI 併進 9c-b-ii；角色引擎與 wwdalerts 規則維持現狀（不加 push_ready 接手）；9d-4 列出 module.go 的角色化改造；S0-c 改用設定內容的雜湊做 CAS（不改 schema）；S3-3a～c 補齊接點與設計。
>
> **修訂 3**（codex 第三輪 `task-mvhajjsx-nrdyiq`：無新 critical，剩 3 條 important 直接修，不再送審）：1.3 不再宣稱單一送達者；S3-3b 的判定定案；S3-3c 只由 primary 叫、不走帳本。另依 88（10-21）補 D1 定義與 9c-0。**定稿**。
>
> 測試分層照 CLAUDE.md：開發中只跑改到的套件；PR 前 `pdx test go-changed <套件>`＋`pdx test lint`（動到鎖／goroutine 的加 `pdx test go-race`；改 mod 或 `cmd/pdx/plugin` 的加 `./cmd/pdx/...` 那一層；碰 exec 白名單的加 `pdx test execstat-guard`），job 的 snapshot.tree＝PR head 的 tree。codex：R1→R2（攻擊→critic），sol、effort low；純搬移（逐宣告位元組比對）與 bump 不派。**純刪除照跑 R1／R2**，focus：仍存在的呼叫端、被一起刪掉的行為、對外介面的退化。
> 規模：每張 ≤ 800 行或 ≤ 20 檔；**純刪除**可放寬，PR 說明寫「純刪除、無新增邏輯」並附 `rg` 證明沒有呼叫端。
> **每張刪除 PR 都必須自己能編譯**：PR 說明列「因為還有誰在用而留下的符號 → 哪一張 PR 刪」；最後一個使用者被刪的那張負責刪它。

## 0. 現況（10-21 07:00）

- 已出貨：W1（snapshot 搬進 `internal/wwd/snapshot`，純搬移）、W2（#3821，fastlane 自有型別）、9d-1（#3822，休眠磁碟守衛）。S2 完成（entry rewrite、purdex 的 assist／mserv 腳本停用）；S3-1（#3817）、S3-2（#3818／#3819）隨 769 部署，`watch_rules` 三台已開；守門員 8c ④ 完成（host-watch 停、report 改 report-wwd.sh）。
- wwd 對 `internal/gate` 剩下的依賴：`cmd/wwd/main.go:195-223,283-288` 的 `hostGate`／gateTaker、claim loop 的 `TakeHeavy`（`internal/wwd/worker/loop/loop.go:360-375`）、`internal/wwd/gateheld/`（W3）、`internal/wwd/fastlane/types_gate_test.go`（W2 留下的對值測試，隨 9a-4 刪）。

## 1. 本 plan 定的事

### 1.1 查證結果（10-21）

| 事實 | 結果 | 影響 |
|---|---|---|
| shim 裝在哪 | mlab、air26 有（`~/.zshenv` 區塊含 `_pdx_gate_front` precmd、`~/.config/pdx/shims` 10 個工具）；air19、mserv 沒有 pdx 也沒有 shim | S0 只做兩台 |
| `pdx lease run` 的 repo 外呼叫者 | aigora-v2 main 的 `desktop/scripts/walkthrough.sh:198,221,309`（#365 會拿掉，未 merge）；aigora-ios 已乾淨；mlab crontab／LaunchAgents 無 | 9f **部署**前 #365 要 merge |
| `pdx gate` 的 repo 外呼叫者 | lead 的 `release-769.sh:145-146`（`would-wait`）；守門員 `report-wwd.sh` 最後一段（ssh air26 `pdx gate status`，已加註、9a-3 時拿掉） | 9a-3 前改掉；9a-3 PR 送出時通知 lucia |
| air26 assist intake | 只剩 `host.mlab.assist-awake`（caffeinate）；兩台 config 沒有 `[assist]` | 9b 不受阻 |
| 守門員的調節手段 | 現在是 `pdx gate set --expect slots.*`；wwd 有 `pdx wwd worker on\|off [--heavy N] [--light N]`，但只管本機、沒有 CAS（`internal/wwd/jobscmd/wwd.go:200-214` GET 整份再 PUT 整份，server 整份取代 `internal/wwd/daemon/hoststate.go:211-228`） | S0 之後閘門名額不再管任何指令 → S0-c |
| fleet 角色的讀者 | 只有 wwdalerts（`deliverRule`、`wakeRule`、`m.peer` fallback）；角色引擎在 `internal/module/fleet/engine.go`，經 `/api/peers/fleet/role`、`/announce` 兩台互問 | 1.3：保留 |
| SPA 的 fleet 讀者 | `lib/fleet/*` 與約 30 個元件／hook；B 線開著的 5 張 MON PR 都沒動 | B 線 MON-9b、MON-12（第 5 節） |
| iOS | 不讀 `/api/fleet`、`fleet.v1`、`gate.decisions`、`watch.v1`；`/api/hostconfig` 只解 `projects`／`commands`（optional，`purdex-ios App/Sources/Net/SessionLaunch.swift:7-12`），`quickReplies` 寬鬆解析 | 9d 不受阻 |
| `/api/resources*` 的讀者 | SPA 沒有；`pdx team`（每 session CPU／MEM）、`pdx host report`；程序內：team 開席資源卡、monitor、patrol | 1.4 |

### 1.2 `pdx gate` 的退場方式（S0，與 W3 同版）

- **S0 一部署，閘門就不再收任何東西**：所有執行形式（shim 的 `--auto --tool X`、mod 改寫的 `--auto --`、腳本的 `[--class] --`）一律直接 exec 真工具，不連 daemon、不排隊、不佔名額；`would-wait` 的單次與批次（`--auto-batch`，`cmd/pdx/gate_cmd.go:512-525,575-585`；mod 的單次 spawn、批次 spawn、批次不可用時的逐筆退回，`hooks/gate.js:284-312`）一律答「不會等」。不留設定開關（不雙軌）。
- **W3 同一版部署**：wwd 的 claim 不再向閘門拿 heavy 槽、也不再看閘門的 admission，否則 S0 之後 wwd 仍受舊系統管（wwd plan「Switch to wwd」：一個 queue job 佔閘門一個 heavy 槽、`gate_full` 讓 headroom 為 0、claim 還問閘門的 admission）。部署時 daemon 先升、wwd 後升，中間的窗口閘門槽只剩 wwd 自己在拿，無害。
- shim 的防遞迴保險（#3671／#3677：環境變數帶 per-tool 深度、`shim.Resolve` 跳過 shims 目錄）**原樣保留**到 9a-3；HOME 被改過的情境照舊要過（10-20 事故，`reference_gate_shim_fake_home_recursion`）。
- `pdx setup --agent cc`（部署用 `setup-both.sh`；daemon 不在時的 `localSetup` 分支也一樣）從 S0 起不再安裝 shim，改成移除 `~/.zshenv` 區塊（`setupGateForUser` 的兩個呼叫點，`cmd/pdx/setup.go:111-138`）。**shims 目錄的檔案先留著**（現有 shell 的 PATH 與 precmd 還指著它，而它們此時已是直通），9a-1 才刪目錄。
- 9a-3 刪 `pdx gate` 子命令的前提：9a-1、9a-2 已在兩台部署（shims 目錄不在、mod 已重載），repo 外呼叫者已改（1.1）。

### 1.3 推播與叫醒的送達者：保留 fleet 的角色選舉，只刪 fleet 的資料面（取代修訂前的做法）

- spec 以 fleet 的 primary／standby 決定誰推、standby 在 primary 失聯或不能推時接手（spec 1089–1094、1110–1115），叫醒也沿用同一個角色（S3-1 的 `wakeRule`）。現行規則是「寧可重複不漏」：primary 一律推；standby 在不確定 primary 狀態時也推（`wwdalerts/module.go:248-269`），所以邊界情況會兩台都推。本 plan **不改這個取捨**，只確保拿掉 fleet 資料面後角色選舉照舊運作。fleet 現有的角色引擎已有測試、已在正式環境跑；換掉它要新寫選舉，風險比保留大。
- **保留**：`internal/fleet/role.go`（`RoleView`、`PeerPush` 等型別）、`internal/module/fleet` 的 `engine.go`、`state.go` 與角色相關的 wiring（`/api/peers/fleet/role`、`/announce`、`push_ready`、`state.json`）、hostconfig 的 `fleet` 列（只剩 `Order`／`Enabled` 的意義）、`push.AlertsReadableKey`。**名稱與路由都不改**：兩台 daemon 滾動部署時要能互問，改名會讓新舊版之間的角色交換失效。
- **刪（9d-4）**：資料面——aggregator、frames、load、probe、pool_source、session、`/api/fleet*`、`fleet.v1`、WS `fleet.changed`、`?fleet=v1`、`fleet.LocalSourceKey`。`/api/info` 不再宣告 `fleet.v1`（模組本身還在，但 capability 跟著資料面走）。
- **角色狀態機不改**：仍是可達性選舉（`engine.go:570-595`）。資料面拿掉後不再呼叫 `SetNewestAge`，`NewestAgeMS` 為 nil，照現行語意視為存活（「primary 活著但量測停了」這個訊號隨資料面消失）；**不新增**「`push_ready=false` 就接手」，避免兩台都不就緒時每個 heartbeat 互搶。
- **wwdalerts 不改**（原 9d-2 取消）：「primary 活著但推不出去或讀不到 wwd」照現行由 wwdalerts 的 `deliverRule`／`wakeRule` 依 `PeerPush` 處理（standby 不確定時推、確定 primary 不行時叫醒；S3-1／S3-2 已接受「寧可重複不漏」）。本 plan 不改變誰推、誰叫的現行行為。
- 測試：既有的角色與 wwdalerts 測試在 9d-4 前後**不改內容照過**；另加「資料面拿掉後角色回答與 `push_ready` 和拿掉前相同」與「`NewestAgeMS` 為 nil 時 standby 不接手」。

### 1.4 `module/resources` 的讀者去留

| 讀者 | 處理 |
|---|---|
| team 開席資源卡（`HostReaderKey`）、monitor（`LocalFactsKey`） | 9d-3b：改讀 `module/pool` 新增的程序內服務（搬 `module/resources/poolhost.go` 組 host 列的部分），**無條件註冊**（現行只在 fleet source＝pool 且有 `FleetConfigKey` 時才給，`poolhost.go:126-160,192-209`；pool 已是唯一來源）。`Trend` 在 pool 模式本來就是 unknown |
| `pool/assess.go` 借 `fleet.LoadScore`／`LocalFast` | 9d-3a：搬進 `module/pool`（純搬移） |
| fleet 資料面讀 resources 的本機來源（`resources/fleet_local.go`、`module.go:23`） | 9d-4 與資料面同一張刪 |
| `pdx team` 每 session 的 CPU／MEM | 9d-6 刪欄：wwd 沒有 session 層的行程樹資料；該呼叫本來就是 best-effort |
| `pdx host report` | 9d-6 改成只讀 wwd（`report_pool.go` 已合併 wwd minutes）；閘門時代的 light／gate／heavy 欄刪 |
| `host_minutes` 與兩張無人讀寫的表（原 9e-1、9e-2） | 隨 9d-6 刪程式；`resources.db` 檔案留在磁碟不動，舊版 daemon 退回時會自己建表（盤點 5.9）。DROP／刪檔另議（9e） |
| `pressure.json`（`gate_input.go` 寫） | 唯一讀者是 `pdx gate` 的壓力檢查；S0 之後沒有讀者，隨 9d-6 刪寫入端。`hostmem/cache.go` 的檔案層不動 |
| watch 偵測器（`module/resources/watch.go`、`sampler_loop.go` 的餵入） | 隨 9c-b-ii 刪 |

### 1.5 巡邏退場前的替代（S3-3；lucia 10-21，翻 10-20、10-21 日誌）

daemon 的例行巡檢（patrol）抓到、但現行 12 條 wwd 警報不會響的五項，補齊之後 9c-c 才刪 patrol：

1. 佇列停滯：有可接的空位（某台 worker 開著、有 headroom、標籤對得上），≥ 5 分鐘沒人領。
2. 單件 job 一直失敗：`claim.attempt` ≥ 3。
3. 佇列積壓：最老的 queued 等待 ≥ 20 分鐘。
4. 沒人接得了：queued 的 job 要的標籤，worker 開著的主機都沒有，持續 ≥ 10 分鐘。
5. 使用者回到電腦前：離開 ≥ 30 分鐘後回來時叫醒守門員一次（守門員趁這時問等使用者決定的事）。

(1)～(4) 由佇列所在主機（origin）的 wwd advisor 評估，等級 `watch`（只叫醒守門員，不推、不進摘要）；門檻是初始值，之後由使用者決定。(5) 是 Purdex daemon 的新叫醒理由，由 fleet primary 叫（同 `wakeRule`）。

## 2. 順序與部署關卡

```
（已完成）W1 W2 9d-1
組一（現在可寫，同一版出）：9f、S0-a、S0-b、S0-c、W3
組一'（現在可寫，可與組一同版）：S3-3a → S3-3b、S3-3c
   └─ D0：組一部署（第 4 節）＋ aigora-v2 #365 已 merge；S3-3 的規則在四台 wwd 都升級後才打開
組二（D0 之後）：9a-1、9a-2、9b-1 → 9b-2 → 9b-3
組三（9b-3 之後）：9c-a → 9c-b-i、9c-b-ii → 9c-c（S3-3 打開並觀察 ≥ 1 天後）
組四（現在可寫）：9d-3a、9d-3b
組四'（現在可寫，小）：9c-0（watch 列的舊欄位改選填，B 線 MON-11 的前提）
   └─ D1：B 線第 N 版＝MON-9b-1、MON-12a-1、MON-12b-1 merge 並出版（SPA 只讀 pool；使用者更新 Mac App）（88，10-21）
組五（第 N+1 版起）：9d-4 → 9d-6-i → 9d-6-ii；9b-4 只要 MON-12a-1 出版即可（6f-2 之後 QueueSection 已不掛 gate.decisions）
組六：9a-3（9a-1、9a-2 部署後）；9a-4-i、9a-4-ii（組三、組五之後）
   └─ D2：C4 驗收；之後另議 9e（DROP 舊表／刪 resources.db）
```

每組的 PR 照常隨版出，不為 WWD-9 另外停版；D0 有額外部署步驟（第 4 節）；組五等 B 線說「SPA 好了，而且已經出版」（88，10-21）。

## 3. 各 PR

逐檔位置以盤點附錄為準，實作者開工先對 origin/main 重查（行號會漂）。

### 組一（同一版，D0 部署）

**9f — 刪 `pdx lease run` alias**（盤點第 9 節 #6）
- 刪 `cmd/pdx/lease_cmd.go`、`lease_run_alias.go`、`lease_mode.go` 與兩個測試；還在用的符號搬家：`splitRunArgs`／`leaseStdinIsTTY`／`classByClassifier`／`classByKind` → `gate_cmd.go`，`leaseClient*` → `host_report.go`，`getResources` → `team_show_cmd.go`，測試輔助 `fakeResourcesDaemon`／`fakeSnapshot` → 共用測試檔；刪 `main.go` 的 lease 分派、`exitcodes.go` 的 `ExitQueued`、`embed_test.go` 與 pdx-gate skill 的 lease 字句；改 `execstat/guard_test.go` 的描述字串（`reap_children.go` 保留）。
- 淨刪約 600，約 12 檔。測試：`./cmd/pdx/...` 那一層（含 plugin）、execstat-guard。驗收：`pdx lease run …` 回 unknown command；`pdx team`、`pdx host report` 照常。

**S0-a — 閘門直通**（1.2）
- 實作者先列**入口表**：shim `--tool`、`--auto --`、`--class … --`、其他執行別名、`would-wait` 單次（CLI 文字／JSON）、`--auto-batch`、daemon 的 would-wait 路由，與 mod 端的單次 spawn、批次 spawn、批次失敗的逐筆退回。每一列一條測試。
- 全部直接 exec 真工具、exit code 與 stdin／stdout／stderr 原樣傳回；would-wait 恆為不會等（批次：長度與順序對應輸入、全部 false）；mod 拿到多個候選時不背景化。`pdx gate status` 照常讀（過渡用）。daemon 端 admission 程式留到 9a-4。
- 約 150／300，約 7 檔。測試另含：PATH 含 shims 目錄且 HOME 被改時不遞迴（沿用 #3677 測試）；daemon 不在時照樣執行；mod 測試（`gate.test.ts` 的 would-wait 段）。mutation：任一入口改回排隊 → 紅。

**S0-b — `pdx setup` 不再裝 shim、移除 `~/.zshenv` 區塊**（1.2）
- `setupGateForUser` 的兩個呼叫點（daemon 在、不在）改成移除區塊；只動 `# >>> pdx gate >>>`～`# <<< pdx gate <<<` 之間，其他內容逐位元不變；冪等；沒有區塊時無動作；區塊缺結尾標記時不刪任何東西並警告；shims 目錄不動。`--uninstall-gate` 保留到 9a-1。
- 約 60／150，約 4 檔。測試要走到真正的入口：`pdx setup --agent cc` 在 daemon 可達與不可達兩條路徑上都移除區塊（不是只測 helper）。

**S0-c — 守門員改調 wwd 的名額**（1.1；lucia 的需求）
- wwd：`GET /v1/worker` 多回一個 `etag`＝整份設定（enabled／slots／owner_rule／disk_min_gb）的正規化內容雜湊，**不存資料庫、不改 schema**（退版不受影響）；`PUT /v1/worker` 收選填的 `expect_etag`，與當下設定的雜湊不符回 409、原值不動；比對與寫入在同一把鎖內；不帶時照舊（App、Purdex daemon 的 pool 模組相容）。改了又改回原值（ABA）對設定無害，接受。
- CLI：擴充現有 `pdx wwd worker`：`--host <alias>`（走 `pdx wwd hold <host>` 那條跨主機路徑，四台都不必 ssh、mserv 不用 socket）；無動作時印現值與 etag；`on|off [--heavy N] [--light N] [--disk-min-gb GB] --expect <etag>` 寫入後印讀回值；409 → 結束碼 3（比照 `pdx gate set`）；對端是不認得 `expect_etag` 的舊 wwd（strict decode 回 400）時，印清楚的錯誤、不重試成不帶 expect。不設上下限、不限速（使用者 10-16）。
- 約 200／300，約 9 檔。測試：expect 相符寫入、不符 409 且整份不變（包含 owner_rule 在兩次請求之間被改的情境）、雜湊與欄位順序無關、不帶 expect 照舊、跨主機讀寫、對端舊版的錯誤訊息、CLI 結束碼。
- 守門員 skill 的替換稿（lucia 寫、subagent 開 PR，10-21）隨同版或前一版；S0 部署後再補「調 wwd 名額改用 `--host`／`--expect`」的段落，閘門名額一節刪掉。

**W3 — 拔 gateheld**（盤點 #3，與 S0 同版）
- 刪 `internal/wwd/gateheld/`、`cmd/wwd` 建 `hostGate` 與注入的部分、`loop.Gate`／`GateSlot` 與 claim 的 `TakeHeavy`、`sampler.GateHeavyFull`、`derived.GateHeavyFull`、`.wwd/tasks.toml` `wwd-linux` 清單裡的 `./internal/wwd/gateheld/`、`manifest/purdex_gate_test.go` 對應行、`worker_gate_test.go`；`wire` 的 `gate_full` 字串保留但不再產生。
- 約 −1250，約 20 檔（純刪除放寬）。測試：wwd-linux、go-changed（wwd 受影響套件）。驗收：部署後 headroom 不再出現 `gate_full`，worker 在閘門滿時照樣領工作。

### 組一'（S3-3，可與組一同版）

**S3-3a — reader 寬鬆＋認得新 kind**
- **接點只在 alert 專屬的路徑**，不放寬通用解碼（`wire/decode.go` 的 `DecodeLenient` 與其他 response 的整份驗證不動）：
  - `client.Alerts`（`internal/wwd/client/alerts.go:16-29`）：envelope 照常驗證；`items` 先解成 raw 陣列，逐則解碼＋`Validate`，不認得的 kind／level 略過並經 client 的 logger 記一行（帶 kind 字串），其他錯誤照舊整份失敗。
  - wwd 的 active list（`internal/wwd/store/alerts.go:12-37`）：逐列解碼，不認得的 kind／level 略過並記 log（store 取得 daemon 的 logger）；其他錯誤照舊。
- 同版認得 S3-3b 的四個新 kind；wwdalerts 的 kind 名稱對照表加四項。舊 App 本來就保留未知 kind（`spa/src/lib/pool/alerts-api.ts`）。以後加規則不必再分兩版。
- 測試：混一則未知 kind、一則未知 level 的清單 → 其餘照常解出、各記一行 log；envelope 壞掉 → 整份失敗（不變）；新 kind 解得出；未知 level 不推；其他 wire response 的驗證不受影響（挑一個既有測試證明）。

**S3-3b — 佇列規則**（1.5 的 (1)～(4)）
- origin 的 advisor 評估四條，`watch` 級；設定 `[advisor] queue_rules = true` 才評估，預設關。**打開時機**：四台 wwd 與兩台 daemon 都升到含 S3-3a 的版本後，lead 逐台打開（寫進 `docs/wwd-deploy.md`，比照 `watch_rules`）。
- **資料來源**：advisor 現在只吃主機 facts（`cmd/wwd/advisor.go:21-41,91-106`），要加一份「佇列快照」輸入，由 origin 的 queue store 新增唯讀 accessor 提供（`internal/wwd/queue`）：每件 queued job 的 `created_at`（提交時間；`updated_at` 會被排序、取消等更新，不用）、要求的 labels、`attempts`、`lapses`、是否被手動 hold。queued 狀態的 wire `Claim` 是 nil（`queue/read.go:147-175`、`wire/jobs.go:34-38,90-115`），所以這些都從 queue DB 讀。
- **判定**（定案）：
  - (2) 單件反覆被放回：`attempts + lapses ≥ 3`（`attempts` 計 infra release，`lapses` 計 lease 到期，`queue/claim.go:17-19,122-167`；10-21 的 #3841 是前者）。
  - (3) 積壓：未被手動 hold 的 queued job 裡最老的 `now − created_at ≥ 20 分鐘`。
  - (1) 停滯：存在可接的空位（某台 worker 開著、有 headroom、標籤對得上），而未被 hold 的 queued job 中有一件 ≥ 5 分鐘沒被領（以 `created_at` 或最後一次放回時間的較晚者起算）。
  - (4) 沒人接得了：某件未被 hold 的 queued job 要的標籤，worker 開著的主機都沒有（含被 quarantine），持續 ≥ 10 分鐘。
  - 手動 hold 的 job 不進 (1)(3)(4)。「可接的空位」與「標籤」用 pool 的 worker 開關、headroom 與 labels 判斷。
- 測試：四條各自 raise／clear；(2) 在 `attempts + lapses` 為 2 與 3 的邊界；worker 全關（例如發版保留）時 (4) 會響；正常領取中的佇列不響；手動 hold 的 job 不觸發；(1) 與 (4) 不對同一個 job 重複叫；計數在 wwd 重啟後仍正確（來自 DB）。

**S3-3c — 叫醒理由「使用者回到電腦前」**（1.5 的 (5)）
- **訊號來源**：`internal/module/push/presence.go:86-113` 的 `AtComputer() bool`（瞬時值；patrol 也是讀它，`patrol.go:41-59`），不在 patrol 裡，9c-c 之後仍在。
- **住在 wwdalerts、但不走警報帳本**：在模組裡新增一個獨立的「在場追蹤」goroutine（納入模組的 WaitGroup，Stop 時取消並等待）：每 60 秒讀一次 `AtComputer`，在記憶體裡記 `awaySince`；由不在變成在、且 `now − awaySince ≥ 30 分鐘` 時直接呼叫 `WakeWatcher(ctx, "user_back", …)`（`internal/watch/wake.go` 的 `reasonLabel` 加「使用者回到電腦前」）。它不是 alert episode，不進 S3-1 的 wake ledger 與 `stillPending` 過濾（`wake.go:187-231,274-320`），每次離開期間最多叫一次，失敗不重送（它是提醒，不是警報）。
- **只有 fleet 角色為 primary 的那台叫**（直接看 `RoleView`，不用 `wakeRule`——`wakeRule` 在 standby 判定 primary 不行時也為真，可能兩台各叫一次）。primary 掛掉時就沒有這則提醒，接受。
- daemon 重啟或角色切換：狀態從頭開始，第一次讀值只當基準、不叫（可能漏叫一次，接受）。讀不到 presence（push 模組沒載入）時不叫、不報錯。
- 測試：離開 30 分鐘以上回來叫一次；離開不到 30 分鐘不叫；剛啟動時讀到「在」不叫；standby（包含它的 `wakeRule` 為真時）不叫；WakeWatcher 失敗不重送；讀不到 presence 不 panic；Stop 會等 goroutine 結束。

### 組二（D0 之後）

**9a-1 — 刪 shim**：刪 `internal/gate/shim`、`cmd/pdx/setup_gate.go`、`--uninstall-gate`；`pdx setup --agent cc` 刪 shims 目錄（只刪我們寫的 10 個檔名，目錄非空就留）；直通需要的 `Resolve` 與深度保險先搬進 `cmd/pdx`。測試：S0-a 的不遞迴測試在新位置照過；setup 冪等。文件寫「舊 shell 若報找不到指令，`rehash` 或開新 shell」。

**9a-2 — 修剪 mod**：`hooks/gate.js` 刪 `prefixGated`／`askWouldWait*`／`planGate` 後半，留分詞與 `entry`；`gate.test.ts` 對應；刪 pdx-gate skill（`embed_test.go` 的檔案清單與斷言同步）；pdx-test skill「快速通道向 gate 拿槽」那段；`CLAUDE.md` 測試分層的「重指令一律經過閘門」改寫成 wwd 的說法；`docs/wwd-deploy.md` §13。測試：mod 測試、`./cmd/pdx/...` 那一層。

**9b-1 → 9b-2 → 9b-3**：照盤點第 9 節 #10–#12。9b-3 刪 `module/assist` 時只刪 `internal/watch/wake.go` 裡 assist 的理由字串，`WakeWatcher`、`Plain` 不動（§11 #4）。

### 組三（9b-3 之後）

- **9c-a — 搬出要留下的部分**（盤點 #13＋§11 #2–#4、#7）。**必須留下**（9c-b 不得刪）：`internal/watch` 的 `Waker`、`WakerKey`、`ValidateWake`、`WakeText`（`wake.go`）、`Plain`（`message.go`）、`HostSignals`（`types.go`，`module/resources/poolhost.go` 還在用，9d-6 才刪）、holder／session／pane／prompts、locate／asker／remote；`module/watch` 的 `WakeWatcher`、`handleWake`／`wakeReply`／`requireAdmin`、`sessionCtl`、`Reconcile`、`handleStatus`／`Status`、`/api/watch/start`、`notifyWatcher`、`peerNotifier`、`handlePeerNote`、`onRegistered` 的掛點。現役使用者：`wwdalerts/module.go`、`wwdalerts/wake.go`、`team/watch_wake.go`、`resources/poolhost.go`。`recoverSession`（只由 patrol 驅動）：實作者先讀它的職責，若 wake 路徑的 `EnsureSession` 已涵蓋就刪，否則掛到 reconcile 現有的 tick，不新增 goroutine。
- **9c-b-i／ii**：照盤點 #14，但刪除清單排除上面的符號；ii 連同 `module/resources/watch.go` 與 `sampler_loop.go` 餵偵測器的部分，**以及 CLI 的 `pdx watch ack`／`log`／`config`**（它們用 `watch.AckResult`、`ValidateNote`、`ReadLogDiag`、`ReadAcks`、`EventView` 等，`cmd/pdx/watch_ack.go`、`watch_cmd.go`；同張刪才能編譯），pdx-watch skill 的閘門事件與 ack 段、`embed_test.go` 對應的斷言同張改。SPA 事件頁收到 404 顯示空（盤點第 4 節），接受。測試：wwdalerts、team、resources、watch 留下部分、`./cmd/pdx/...` 那一層都在 go-changed 裡。
- **9c-c**：照盤點 #15 刪 patrol、rest、單機版 twoawake；**`team/watch_wake.go` 不刪**（§11 #7 保留無人值守通知）；前提：S3-3（含 S3-3c）打開並觀察 ≥ 1 天。`watch.v1` 的宣告文字改成只含 start／wake／status。

### 組四（現在可寫）

- **9c-0 — host-config `watch` 列的舊欄位改選填**（88，10-21）：PUT 是嚴格解碼且 `x_cores`、`y_mb`、`t_s`、`pressure_level`、`pressure_t_s`、`page_rate`、`page_t_s` 必填（`internal/module/hostconfig/watch.go:76,83-90`），App 拿掉畫面後若停止寫回就會被拒。改成：這些偵測器欄位與 `patrol_*`、`disk_low_gb`、`auto_wake` 在 PUT 都是選填，沒送就沿用現值；GET 照回；其他欄位與未知欄位的驗證不變。出版後 B 線 MON-11 才停止寫回。之後 9c-b-ii（偵測器、`auto_wake`）、9c-c（`patrol_*`、`disk_low_gb`）從儲存與 GET 拿掉這些欄位，PUT 仍接受並忽略（舊 App 相容，不設期限）。約 40／80，2 檔。測試：只送其餘欄位 → 成功且舊值不變；送全部 → 照舊；未知欄位仍拒。
- **9d-3a — `LoadScore`／`LocalFast`／`levelScore` 搬進 `module/pool`**：純搬移，位元組比對，免 codex。
- **9d-3b — pool 的程序內 host 服務**（1.4）：無條件註冊；team 開席資源卡與 monitor 改讀它；取消 `resources.host-reader`／`local-facts` 的註冊。測試：registry 裡沒有 `FleetConfigKey` 時照樣提供；wwd 可讀、不可讀（回 false，monitor 退回自取樣）；開席失敗備註的欄位不變。

### 組五（第 N+1 版起，B 線說好之後）

- **9d-4 — fleet 資料面**（1.3）：一張內同時刪資料面、`resources/fleet_local.go` 與 `resources/module.go` 對 `internal/fleet` 的 import、hostconfig 的 `probe_hosts` 列（含 `hostconfig/probehosts.go` 的 import 與 PUT 路由）、`/api/fleet*`、`fleet.v1`（`core/info_handler.go`）、`?fleet=v1`（`core/events.go`）、peers 白名單的資料面路由（`policy.go:59` 裡角色與 announce 以外的部分）；`cmd/pdx/main.go` 仍註冊 fleet 模組（只剩角色）。**`module.go` 角色化**：`Module`／`Start`／`Stop`／`RegisterRoutes` 拿掉 `Measure` 與資料面路由，只留角色路由；`Dependencies` 拿掉 `resources`；`Init` 不再讀 `fleet.LocalSourceKey`、不再 `SetNewestAge`；`push_ready`（`module.go:100-120`）與 `engine.go` 原樣保留；`push.go`（`fleet.changed` 的推送，資料面）刪。`internal/fleet` 留 `role.go` 與測試輔助 `role_fake.go`（wwdalerts 的測試用 `fleet.NewFakeRole`）。純刪除放寬；若超過約 50 檔，拆成「module/fleet 資料面」與「internal/fleet＋core／peers／hostconfig」兩張，**每張都要能編譯**（前一張先留 import 用的空殼或保留型別，PR 說明列出）。測試：角色選舉測試照過（1.3 的情境）、`/api/info` 不宣告 `fleet.v1`、`./cmd/pdx/...` 那一層。
- **9b-4／9d-7**：照盤點 #19（後端；SPA 的歷史分頁由 B 線在 MON-12 移除）。
- **9d-6-i／ii — `module/resources` 其餘**（盤點 #20 的 `resources` 列＋#21＋原 9e-1、9e-2）：`/api/resources[/report]`、`resources.changed`、hostconfig 的 `resources` 列、`pdx team` 的每 session CPU／MEM、`pdx host report` 改只讀 wwd、`pressure.json` 寫入端、`host_minutes` 與兩張舊表的程式、`watch.HostSignals`（最後一個使用者）。i＝程式與路由；ii＝CLI 與測試資料。驗收：`resources.db` 檔案留在磁碟、新版不再開它。

### 組六

- **9a-3 — 刪 `pdx gate` 子命令**（盤點 #22）。前提見 1.2 最後一點；PR 說明附兩台 `ls ~/.config/pdx/shims` 不存在、repo 外呼叫者 `rg` 清空的證據；**PR 送出時通知 lucia**（`report-wwd.sh` 的舊閘門段）。
- **9a-4-i／ii — 刪 `internal/gate` 核心**（盤點 #23）；連同 `fastlane/types_gate_test.go`、`execstat` 白名單的 gate 條目、`gatetest.Main`（`cmd/pdx/main_test.go`、`restart_integration_test.go` 的 TestMain 換掉）。前提：9c-b（偵測器的 `gate.ClassifyReason`）、9d-4、9d-6、9b-4、9a-3 都已 merge。

## 4. D0：組一的部署步驟（寫進 `docs/wwd-deploy.md`）

1. 前一天：lead 通知 aino「明天撤 shim」，aino merge aigora-v2 #365；aino 確認 aigora-v2／aigora-ios 的 swift／xcodebuild 走 `pdx test`（wwd 的 task），否則撤 shim 後在 air26 不再排隊。
2. lead 的發版腳本把 `pdx gate would-wait` 換成 wwd 的「RUNNING 0」檢查（`/v1/jobs`）。
3. 照常閘門：lucia go；`relays_active = 0`（重啟前、`pdx setup` 前各查一次）；另查兩台 `pdx gate status`：**waiter 必須為 0**（不是 0 就等它清空，等不到就延後切換，不硬切），holder 只能是 wwd 的 job。
4. 兩台 daemon＋pdx 升版 → `setup-both.sh`（`pdx setup --agent cc`；air19、mserv 沒有 pdx，跳過）：確認 `~/.zshenv` 區塊已移除、`type go` 在新 shell 指向真工具、舊 shell 裡的 `go version` 照常（走直通）。
5. 四台 wwd 升版（W3、S0-c 的 wwd 端、S3-3a）；`pdx wwd pool` 四台版本一致後，lucia 才開始用 `pdx wwd worker --host … --expect …`。
6. 觀察一小時：wwd 的觀察偵測與 S3 的壓力警報是唯一的限流訊號，lucia 盯。
7. S3-3b：四台都升級後，lead 逐台打開 `queue_rules`（每台先問 lucia RUNNING 0）。

退版：重新 `pdx setup --agent cc`（舊版會裝回 shim）並把 wwd 退回上一版（W3 前的 wwd 會重新向閘門拿槽）。組一沒有 wwd.db 的 schema 變更（S0-c 的 etag 不落地），wwd 退版不必還原資料庫；若同版還有別的 schema 變更，照 `docs/wwd-deploy.md` 的退版程序（備份、停機還原、WAL／SHM）。S3-3b 的退版照 `watch_rules` 的做法（先關規則、等新 kind 清除）。

## 5. 對外協調

| 對象 | 內容 | 時點 |
|---|---|---|
| aino（aigora） | #365 merge、swift／xcodebuild 走 wwd | D0 前一天 |
| lucia | skill 替換稿（已收）；改用 `pdx wwd worker --host --expect`；9a-3 PR 送出時通知；S3-3 方案過目 | S0 前；9a-3 時 |
| 88（B 線） | MON-12 plan（PR #3855）：D1＝MON-9b-1、12a-1、12b-1 merge 並出版（第 N 版＋使用者更新 Mac App），**第 N+1 版**才 merge 9d-4（9d-6 跟在後）；9b-4 只等 12a-1 出版；12b-2／3、12c、12d 是死碼清理不擋。12c 升 settings ordinal 前 88 先通知 lead。watch 列舊欄位：A 線 9c-0 出版後 MON-11 才停寫 | 進行中；N 由 88 告知 |
| iOS | 已核，不受影響（1.1） | — |

## 6. 風險

- **撤 shim 後非 Claude Code 的重指令不排隊**（Codex、一般終端機、腳本）：§11 #1 已接受；只剩 wwd 的觀察與 S3 警報。air26 的 swift／xcodebuild 最重，靠第 4 節第 1 點。
- **S0-a 的入口表若有遺漏**，那個入口會繼續排隊（不壞，只是沒切乾淨）：方案先列表，PR 層逐列測。
- **S0 與 W3 同版**：任一方延誤就整組延；W3 是純刪除、風險低，先寫。
- **組五卡在 B 線**：9d-4、9d-6、9a-4 跟著晚；舊系統此時已不收任何東西，剩下的是死碼，不影響「不雙軌」。
- **純刪除的大 PR** 與其他線衝突（`module/team`、`core`、`cmd/pdx/main.go`）：每張開工前併最新 main；merge 前照「main 在 base 之後改到被 import 的套件就重跑」。

## 7. 驗收（C4，正式環境）

- App（Mac、iOS）的側欄、Hosts 頁、守門員頁（status／wake／start）、監控分頁組照常；entry 改寫照常；`pdx test`／`pdx jobs` 照常。
- `rg` 證明沒有人讀被移除的路由、capability、registry key；兩台 `/api/info` 不再宣告 `fleet.v1`、`gate.decisions.v1`。
- 兩台沒有 shim、`~/.zshenv` 沒有區塊；`pdx gate`、`pdx lease` 回 unknown command。
- 守門員被 wwd 警報、S3-3 的佇列規則與「使用者回到電腦前」各叫醒過一次（實測）；推播（若已開）在兩台都正常時由 primary 送出；primary 停掉時 standby 接手推播與叫醒（實測一次）。行為與 9d-4 之前相同（「寧可重複不漏」的邊界情況不在驗收內）。

## 附：修訂 1 對照（codex `task-mvh9fsh0-1pdxmo`）

| # | finding | 處理 |
|---|---|---|
| 1 | W3 排在 S0 之後＝wwd 仍受閘門 admission 管 | 成立：W3 改與 S0 同版（1.2、組一） |
| 2 | D0 沒升 wwd 就用帶 expect 的 CLI；舊 wwd strict decode | 成立：第 4 節第 5 步，四台升完才用；CLI 對舊版給清楚錯誤 |
| 3 | 舊 gate waiter／holder 在切換時還會被放行 | 成立（過渡）：第 4 節第 3 步先查 |
| 4 | 無參數 `pdx setup` 會報錯，走不到撤區塊 | 成立：改寫 `pdx setup --agent cc`，測試走真入口 |
| 5 | CAS 用部分欄位快照，owner_rule 會被覆寫 | 成立：改整份設定的 revision |
| 6 | 測試矩陣缺 would-wait 批次與 mod 退回路徑 | 成立：S0-a 入口表 |
| 7 | 9c-b 會刪掉留下流程在用的 `internal/watch` 符號 | 成立：9c-a 列出必留清單 |
| 8–11 | 「守門員所在主機叫醒、本機就緒就推」會漏叫、與 spec 單一送達者衝突 | 成立：改為保留 fleet 角色選舉（1.3），原 9d-2 取消 |
| 12 | 9d-2 刪 `AlertsReadableKey` 會破壞 fleet | 隨 1.3 消失（保留該 key） |
| 13 | 9d-4 刪 fleet 時 hostconfig／resources／cmd/pdx 還 import | 成立：9d-4 同張處理 `fleet_local`、`probe_hosts`；cmd/pdx 保留註冊；每張要能編譯 |
| 14 | 9d-3b 的服務依賴 `FleetConfigKey` | 成立：無條件註冊並測 |

## 附：修訂 2 對照（codex `task-mvh9trld-fzid5w`）

| # | finding | 處理 |
|---|---|---|
| 3 | 「記下即可」不是關卡 | 第 4 節第 3 步：waiter 必須為 0，否則延後 |
| 7 | 9c-b 與 9c-c 之間 CLI 還用被刪的符號 | ack／log／config CLI 與 skill 段併進 9c-b-ii |
| 10 | 「push_ready=false 就接手」與現行狀態機不符、會互搶 | 狀態機不改；NewestAge 為 nil＝存活（現行語意） |
| 11 | wwdalerts 不改不等於只有 primary 推 | plan 不再宣稱；行為維持現行（寧可重複不漏），測試改為前後一致 |
| 13 | 9d-4 沒寫 module.go 的角色化、會刪到 `role_fake.go` | 9d-4 列出改造點；保留 `role_fake.go`；`push.go` 屬資料面刪 |
| 15 | revision 落地要改 schema、退版不可行 | S0-c 改內容雜湊 etag，不落地 |
| 16 | S3-3a 的解碼接點未定 | 只在 `client.Alerts` 與 store 的 active list 逐則解碼；通用解碼不動 |
| 17 | queued job 沒有 Claim，attempt 不在 wire | queue store 新增唯讀 accessor 提供放回次數；advisor 加佇列快照輸入 |
| 18 | S3-3c 沒有狀態與去重設計 | 住 wwdalerts、60 秒輪詢、記憶體 awaySince、重啟不叫（修訂 3 再改去重方式） |

## 附：修訂 3 對照（codex `task-mvhajjsx-nrdyiq`：6 條確認解決、3 條 important 未解決、無新 critical → 依停止條件不再送審，直接修）

| # | finding | 處理 |
|---|---|---|
| 11 | 需求寫「單一送達者」、驗收寫「只收到一則」，與現行「寧可重複不漏」矛盾 | 1.3 改寫為維持現行取捨；第 7 節驗收改成 primary 正常時由它送、失聯時 standby 接手，邊界重複不在驗收內 |
| 17 | 放回次數的判定交給實作者；`enqueued_at` 不存在 | S3-3b 定案：`attempts + lapses ≥ 3`；等待用 `created_at`；手動 hold 排除；邊界測試 |
| 18 | `wakeRule` 兩台可能同時為真；`user_back` 會被帳本過濾 | S3-3c 改為只有 `RoleView` 為 primary 的那台叫；獨立 goroutine、不進帳本、每次離開最多一次、失敗不重送 |
