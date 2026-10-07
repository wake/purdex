# http.Server 的 ReadHeaderTimeout／IdleTimeout 與上傳 body 停滯偵測（#1523）— 計畫

日期：2026-10-08　負責：purdex-37　方案已由 purdex-d3 同意（2026-10-08）
關聯：#1523（D-1 review 發現、daemon 全域既有問題）。配額與清理拆到 #1806（design）。

## 1. 量測（拋棄式 httptest 測試，已刪）

| 情境 | 結果 |
|---|---|
| gorilla WebSocket（同 `internal/terminal`、`internal/core/events` 的 Upgrader），`ReadTimeout=1s`，閒置 3 s | 仍可寫入，**不被砍**（gorilla 在 Upgrade 時清掉 conn deadline） |
| SSE 串流 6 個事件／3 s，`ReadTimeout=1s` | 全數收到，**不被砍**（ReadTimeout 不管回應寫出） |
| 慢速上傳（3 s 內分 6 次各 1 byte），`ReadTimeout=1s` | **被砍**（讀到 2 byte，i/o timeout）→ 全域 `ReadTimeout` 會誤砍大檔上傳（nex 50 MiB、agent 256 MiB 走慢網路），不可設 |
| 慢 header（2 s 才送完），`ReadHeaderTimeout=1s` | 被拒（空回應） |
| 同樣的慢速上傳，只設 `ReadHeaderTimeout=1s` | body 完整收完，**不受影響** |

## 2. 修法

1. `cmd/pdx`：抽 `newHTTPServer(addr, handler) *http.Server`，設 `ReadHeaderTimeout = 10s`、`IdleTimeout = 120s`；**不設**全域 `ReadTimeout`／`WriteTimeout`。`runServe` 改用它。`ReadHeaderTimeout` 只管 header（對 WS／SSE／上傳 body 零影響），擋 slowloris header；`IdleTimeout` 為 keep-alive 閒置連線上限（Go 在兩者皆 0 時無上限）。
2. 上傳 body 的慢速攻擊用**停滯偵測**而非總時限：新的小 helper `StallTimeoutBody(w, r, d)`（放 `internal/middleware`，無新依賴），把 `r.Body` 包成每次 `Read` 前以 `http.NewResponseController(w).SetReadDeadline(now+d)` 延長讀取期限的 reader（`d = 30s`）；持續有資料就不會被砍、停滯超過 30 s 才讀取失敗（總時間不限）；`EOF`／`Close` 時把 deadline 歸零。`SetReadDeadline` 不支援（例如 `httptest.ResponseRecorder`）時視為 no-op。
3. 套用在兩個上傳路由：agent `/api/agent/upload`（`handleUpload`，在 `ParseMultipartForm` 之前）與 nex `/api/nex/executions/{id}/uploads`（`internal/module/nex/upload.go`，包在既有 `MaxBytesReader` 內層，順序：`MaxBytesReader(StallTimeoutBody(...))`）。

## 3. 任務（TDD：先紅）
- T1 `newHTTPServer`：欄位值（`ReadHeaderTimeout`、`IdleTimeout` 為指定值；`ReadTimeout`／`WriteTimeout` 為 0）。
- T2 `StallTimeoutBody` 單元／整合（真 TCP，短 `d`）：持續慢速（每 100 ms 一個 byte、總時間 > d）**不被砍**；停滯（送一個 byte 後沉默 > d）讀取失敗；`EOF` 後 deadline 歸零（同一連線 keep-alive 第二個請求不受影響）；不支援 deadline 的 ResponseWriter 不 panic。
- T3 兩個上傳 handler 都套用了 helper：以 httptest server（用 `newHTTPServer` 的設定值縮短為毫秒級）對實際 handler 做「停滯 body → 失敗、持續慢速 → 成功」。
- T4 WS／SSE／長請求在 `ReadHeaderTimeout`／`IdleTimeout` 設定下不被砍（用 gorilla 與 SSE handler，閒置超過 `ReadHeaderTimeout` 數倍）。
- 變異驗證：拿掉 deadline 延長（`StallTimeoutBody` 退化成 no-op）→ T2／T3 的停滯案例紅；`newHTTPServer` 誤設 `ReadTimeout` → T1 紅、T3 的慢速成功案例紅。

## 4. 不做
- 不設全域 `ReadTimeout`／`WriteTimeout`；不加配額與清理（#1806）；不改上傳大小上限。

## 5. 驗收／回滾
- 單元／整合測試（`-race`）；部署後觀察上傳與 WS 正常。回滾：單一 PR revert。

## 6. 規模
約 150–250 行（含測試）；一個 PR。
