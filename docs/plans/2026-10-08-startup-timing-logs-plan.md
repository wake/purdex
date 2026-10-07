# daemon 啟動耗時日誌 — 計畫（#1767 修正 (3)）

日期：2026-10-08　負責：purdex-37　審查：purdex-d3
背景：#1767。重啟 5 s → 16 s → 28 s 的拆解裡，8 s 已由殭屍回收（#1769）解掉；剩下的 5 s 基線與負載造成的 5–13 s 變動落在「`[dev] update endpoints enabled` 到 `[agent] hook event endpoint registered`」之間（agent 模組 `Start`），隔離環境只要 0.6 s，無法在不碰正式 tmux 的前提下重現。**現在日誌只有零星的「endpoints enabled」行，看不出每個模組各花多久**。目標：下次重啟直接從日誌讀出時間花在哪，不再靠猜、也不必隔離重現。純觀測，不改行為。

## 1. 設計

### 1.1 core：每個模組 Init／Start 各一行耗時
- `core.InitModules` 與 `StartModules` 的迴圈量每個模組的 wall time，寫**一行彙總**（每個階段一行，而不是每個模組一行，避免洗版）：
  `startup: init 24 modules in 412ms: agent=380ms peers=12ms session=9ms …`（依耗時降序、只列 ≥ 5 ms 的；其餘併成 `others=Nms`）。Start 階段同樣：`startup: start 24 modules in 8123ms: agent=5210ms peers=2850ms …`。
- 任一模組單次耗時 ≥ 1 s 另記一行警示：`startup: slow module start: agent took 5210ms`（閾值 `slowModuleThreshold = time.Second`，常數）。
- 失敗（`Init`／`Start` 回錯）時，錯誤訊息照舊，**之前**先把已跑完的模組耗時記出（方便看啟動失敗卡在哪）。
- 時鐘與 logger 注入：`Core` 新增非導出欄位 `now func() time.Time`（預設 `time.Now`）與 `logf func(string, ...any)`（預設 `log.Printf`），測試以假時鐘與收集器驗證；不改任何導出介面，既有測試與建構（`New(CoreDeps)`）不變。

### 1.2 cmd/pdx：整體啟動時間
- `runServe` 開頭記 `bootStart := time.Now()`；在既有 `log.Printf("pdx daemon listening on %s", addr)` 之前加一行：
  `startup: ready in 7123ms (init=412ms start=6350ms, process start to ready)`。`restart` 路徑（exec）的 new image 同樣有，因為每個 image 的 `runServe` 都跑。
- 若環境能取得「上一個 image 發出 restart request 的時間」（`last-shutdown.json` 或 restart 紀錄）可得到端到端重啟耗時；本 PR **不做**（不新增檔案格式），日誌的 `restart requested` 與 `listening` 兩行時間戳已足夠（#1767 的做法）。

### 1.3 agent 模組 Start 的子步驟
- `internal/module/agent/module.go` 的 `Start`：對 `sweepOnce`、`replayFromDB`、`startSweep`、`probeIntentDisp.replayStatus` 四步各量時，結尾一行：
  `[agent] start: sweepOnce=12ms replayFromDB=3ms startSweep=0ms replayStatus=5190ms`。這是 #1767 懷疑的 5–13 s 段落；有了它就能知道是 tmux／ps 呼叫還是別的。
- 同樣在 **peers 模組 `Start`**（Sweep 與其他初始化）與 **session 模組的 tmux hook 安裝**（日誌已有兩行相隔 5 s 的 `installed tmux hooks`）各補一行子步驟耗時，只加量時，不改順序。實作前先 `rg` 這兩處的 `Start`，若結構不適合再縮減範圍（回報）。

## 2. 任務（TDD：先紅）

### T1 core 模組耗時彙總
- 測試（假時鐘、假模組，各自在 `Init`／`Start` 時推進假時鐘）：(a) 彙總行包含總耗時與依耗時降序的模組名；(b) < 5 ms 的模組併入 `others`；(c) ≥ 1 s 的模組另有 `slow module` 警示行；(d) 某模組 `Start` 失敗時，錯誤照舊回傳，且在回傳前已記出已完成模組的耗時；(e) 沒有模組時不記無意義的行；(f) 既有 `Core` 測試不動、全綠。
- 實作：`Core` 加 `now`／`logf` 欄位（`New` 設預設）、`InitModules`／`StartModules` 量時並呼叫一個小的 `formatModuleTimings(durs []moduleTiming, total time.Duration) string`（純函式，單獨測：排序、門檻、`others`、時間格式 `Nms`）。

### T2 runServe 的 ready 行
- `cmd/pdx/main.go`：`bootStart` 與 init／start 耗時（由 `InitModules`／`StartModules` 前後各量一次，不必從 core 回傳）；在 `listening` 前記 `startup: ready in …`。測試：`runServe` 難以單元測試，抽一個純函式 `startupReadyLine(total, init, start time.Duration) string` 測格式；整合驗證靠 `cmd/pdx` 既有的重啟整合測試（`restart_integration_test.go`）新增一個斷言：helper 的 stderr／日誌含 `startup: ready in`（若該測試能取得 helper 輸出；否則只做格式測試並在回報說明）。

### T3 agent／peers／session 子步驟耗時
- agent：把 `Start` 的四步包成量時，格式化函式與測試同 T1 的 `formatStepTimings`（可共用 core 內的純函式並 export 為 `core.FormatStepTimings`，或各自一個小 helper——以不新增跨模組依賴為準；`internal/module/agent` 已 import `internal/core`，可共用）。測試：假時鐘＋假步驟，確認格式與順序固定。
- peers／session：依 §1.3，量時＋一行日誌，不加新測試的情況下需以既有測試全綠為準；若有 helper 可純函式測就一起測。

### T4 文件
- `docs/` 若有日誌欄位說明（`rg -n "endpoints enabled" docs` 先查）就補一段「啟動耗時行」的解讀；沒有就不新增文件，只在 CHANGELOG 說明。

## 3. 不做
- 不改啟動順序、不改任何模組行為、不加新設定／旗標；日誌一律預設開啟（量時成本是幾次 `time.Now`）。
- 不處理「為什麼 agent Start 慢」本身——這個 PR 只提供證據，是否要修另開。
- 不新增 metrics／endpoint；`/api/info` 不變。

## 4. 驗收
- 部署後重啟一次，日誌應出現 `startup: init …`、`startup: start …`、`[agent] start: …`、`startup: ready in …`；把這幾行貼回 #1767 即可定位 5–13 s 的來源。
- 回滾：單一 PR revert，無資料格式變動。

## 5. 規模
約 150–200 行（含測試）；一個 PR。
