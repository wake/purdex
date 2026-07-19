# M0 Golden Fixtures

依 `docs/specs/m0-contract.md`（SOT，對應 spec commit `19e622b`）。兩 repo（Go/TS）載入同組 fixture 驗證各自的 encode/decode 與 handler 行為，確保假對手測試與真對手一致。

## 結構

每個 `.json` 為自描述物件：
```json
{ "name": "...", "endpoint": "E1|E2|E3|E4|sandbox",
  "kind": "request|response", "http_status": 200,
  "note": "情境說明", "payload": { ... } }
```

## 覆蓋（每端點 ≥1 成功 + ≥1 錯誤）

| 檔案 | 端點 | 情境 |
|------|------|------|
| `e1_pending_list.success.json` | E1 | 輪詢最小清單 |
| `e2_claim.success.json` | E2 | 首次 claim 成功 |
| `e2_claim.duplicate_same_daemon.json` | E2 | 同 daemon 冪等重複 claim（回既有 execution_id）|
| `e2_claim.conflict_other_daemon.json` | E2 | 他 daemon 已 claim（409 error）|
| `e3_fetch.success.json` | E3 | 兩段式②完整 issue+repo_location |
| `e3_fetch.not_owner.json` | E3 | 非本 daemon（404 error）|
| `e4_report.accepted.json` | E4 | accepted(seq=1) request+response(ack_seq) |
| `e4_report.running.json` | E4 | running(seq=2) |
| `e4_report.completed_artifacts.json` | E4 | completed(seq=3)+diff artifact |
| `e4_report.failed.json` | E4 | failed + error |
| `e4_report.accepted_required.json` | E4 | lifecycle 先於 accepted ack（409 error）|
| `e4_report.stale_seq.json` | E4 | seq≤ack 丟棄（回 200+ack_seq，非錯誤）|
| `sandbox.clamp.json` | sandbox | request 寬→clamp 到 host policy |
| `sandbox.unknown.json` | sandbox | unknown enum（422 error）|

## Loader smoke test（各 repo 自寫）

Go：`internal/.../fixtures_test.go` 載入全 `*.json`、assert `schema_version==1`、payload 可 unmarshal 進契約 struct。
TS：`vitest` 載入同目錄、assert 同上。
