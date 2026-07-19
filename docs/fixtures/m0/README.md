# M0 Golden Fixtures

依 `docs/specs/m0-contract.md`（SOT，對應 spec commit `19e622b`）。兩 repo（Go/TS）載入同組 fixture 驗證各自的 encode/decode 與 handler 行為，確保假對手測試與真對手一致。

## Fixture envelope schema（精確定義，loader 可穩定 parse）

每個 `.json` 為自描述物件。**必要欄位**：
- `name` (string)、`endpoint` (`E1|E2|E3|E4|sandbox|any`)、`kind` (`request|response|cases`)、`note` (string)

**可選欄位**（依 kind 出現）：
- `http_status` (int) — `kind:request|response` 有；`kind:cases` 無
- `payload` (object) — `kind:request|response` 的主體；帶 `schema_version` 或 `error`
- `response` (object `{http_status, payload}`) — 當本檔是 request 且要附帶對應 response
- `request_that_triggers` (object) — 當本檔是 error response 且要示範觸發它的 request
- `precondition` (object) — 情境前置狀態（如 `{current_ack_seq:3}`）
- `cases` (array) + `order` (array) — 僅 `kind:cases`（多情境表，如 sandbox clamp）

**Loader smoke test 規則**：
- 必要欄位齊全；`endpoint`/`kind` 為合法 enum。
- 若有 `payload` 且含 `schema_version` → assert `== 1`。
- 若有 `payload` 且 `kind:request` → 可 unmarshal 進對應契約 struct（依 endpoint+status）。
- `kind:cases` 略過 payload 檢查，改驗 `cases[]` 結構。

## 覆蓋（每端點 ≥1 成功 + ≥1 錯誤 + mandatory error paths）

| 檔案 | 端點 | 情境 |
|------|------|------|
| `e1_pending_list.success.json` | E1 | 輪詢最小清單（envelope `{schema_version,dispatches:[]}`）|
| `e2_claim.success.json` | E2 | 首次 claim 成功 |
| `e2_claim.duplicate_same_daemon.json` | E2 | 同 daemon 冪等重複 claim（回既有 execution_id）|
| `e2_claim.conflict_other_daemon.json` | E2 | claim race 他 claimer 已 claim（409 already_claimed）|
| `e3_fetch.success.json` | E3 | 兩段式②完整 issue+repo_location |
| `e3_fetch.dispatch_not_found.json` | E3/E2 | 不存在 OR 非本 daemon（404 dispatch_not_found，fail-closed，共用）|
| `e4_report.accepted.json` | E4 | accepted(seq=1) request+response；**含 repo_location echo** |
| `e4_report.running.json` | E4 | running(seq=2) |
| `e4_report.completed_artifacts.json` | E4 | completed(seq=3)+diff/transcript artifact |
| `e4_report.accepted_to_completed.json` | E4 | 直接 accepted(1)→completed(2)（seq 值非固定 2/3）|
| `e4_report.failed.json` | E4 | failed + error |
| `e4_report.accepted_required.json` | E4 | lifecycle 先於 accepted ack（409）|
| `e4_report.stale_seq.json` | E4 | seq≤ack 丟棄（回 200+ack_seq，非錯誤）|
| `x_schema_incompatible.json` | any | schema_version 不相容（400，適用全 endpoint）|
| `sandbox.clamp.json` | sandbox | clamp 多情境（`kind:cases`）|
| `sandbox.unknown.json` | sandbox | unknown enum（422 unknown_sandbox_profile）|

> `not_owner` 已移除（codex PR-0 #3）：fail-closed 下「不存在」與「非本 daemon」回相同 `dispatch_not_found`。

## Loader smoke test（各 repo 自寫，屬各自 PR）

Go：`internal/.../fixtures_test.go` 載入全 `*.json`、依上規則驗證。
TS：`vitest` 載入同目錄、同規則。
