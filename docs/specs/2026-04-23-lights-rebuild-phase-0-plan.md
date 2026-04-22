# Phase 0 TDD Plan — Status 對齊骨架

- **Date**: 2026-04-23
- **Spec**: `docs/specs/2026-04-23-lights-rebuild-spec.md` §4
- **Worktree**: `lights-rebuild-spec`（branch `worktree-lights-rebuild-spec`）
- **範圍**：新增 `StatusSupporter` optional interface + `Coverage` helper + TDD 測試；零改動既有程式碼

## 1. 契約鎖定

本節鎖定所有行為細節，避免實作期再決策。

### 1.1 `StatusSupporter` interface（`internal/agent/provider.go`）

- 位置：與既有 `HookInstaller` / `StatuslineInstaller` 同一「Optional capabilities」區塊
- 唯一方法：`SupportedStatuses()` 回傳 `[]Status`
- Optional：不實作者為合法（等同「未宣告」）
- 宣告空 slice 合法：代表 provider 主動宣告「我不支援任何 Status」，與「未實作 interface」不同
- Provider 實作回傳的 slice，Coverage 需 defensive copy（避免 provider 後續 mutate）

### 1.2 `CoverageRow` 型別（`internal/agent/coverage.go`）

三欄位：

| 欄位 | 型別 | 語意 |
|---|---|---|
| `AgentType` | `string` | provider 的 `Type()` 回傳值 |
| `Declares` | `bool` | provider 是否實作 `StatusSupporter`（type assertion 結果） |
| `Declared` | `[]Status` | 若 `Declares=true`，為 `SupportedStatuses()` 回傳值的 copy；若 `Declares=false`，為 `nil` |

### 1.3 `Coverage(r *Registry) []CoverageRow`

- 呼叫 `r.All()` 取得所有 provider
- 對每個 provider 做 type assertion 判斷是否實作 `StatusSupporter`
- 輸出順序：以 `AgentType` 字母序（ASCII）排序；重複 AgentType 保留 registration 順序作為 tie-breaker（不預期會發生，但不 panic）
- 空 registry 回傳 `nil`（不是 empty slice） — 與 Go 慣例對齊，`len()` 皆為 0
- 非 thread-safe 假設：呼叫期間 registry 不會被 mutate（與 `Registry.All()` 相同約束）

### 1.4 零改動邊界

以下檔案在 Phase 0 **不得觸碰**（違反即視為超出 Phase 0 範圍）：

- `internal/agent/{cc,codex,opencode}/*.go`（含 `provider.go` / `status.go` / `readiness.go`）
- `internal/agent/probe/*.go`
- `internal/agent/registry.go`、`status.go`、`hook_version*.go`、`process_info*.go`
- `internal/module/**`、`internal/store/**`
- `spa/**`

## 2. 測試案例清單

全部放在新檔 `internal/agent/coverage_test.go`（package `agent_test`，與 `registry_test.go` 同模式）。

共用 stub：`supporterStub` — 實作 `StatusSupporter` 的 fake provider；無 `StatusSupporter` 的 case 可直接重用既有 `fakeProvider`（或在本檔另開最小 stub）。

| # | 名稱 | 情境 | 斷言 |
|---|---|---|---|
| T1 | `TestCoverageEmpty` | `NewRegistry()` 未註冊任何 provider | `Coverage(r)` 回傳 nil 且 `len()==0` |
| T2 | `TestCoverageNoStatusSupporter` | 註冊一個未實作 `StatusSupporter` 的 provider | 單列 row，`Declares=false`、`Declared==nil`、`AgentType` 正確 |
| T3 | `TestCoverageWithStatusSupporter` | 註冊一個實作 `StatusSupporter` 的 provider，宣告 `[Running, Idle]` | 單列 row，`Declares=true`、`Declared` 元素 = `[Running, Idle]`、`AgentType` 正確 |
| T4 | `TestCoverageDeclaredIsCopy` | provider 回傳 slice 後，呼叫端 mutate `row.Declared` | provider 內部 slice 未被影響（defensive copy 驗證） |
| T5 | `TestCoverageEmptyDeclaration` | provider 實作 `StatusSupporter` 但回傳空 slice | `Declares=true`、`Declared` 為空 slice（非 nil）（若改為允許 nil 可調整此斷言，本 plan 偏向「空 slice，明確表達『宣告了沒有』」） |
| T6 | `TestCoverageSortedByAgentType` | 註冊三個 provider，註冊順序 `cc` → `codex` → `opencode`（字母序恰好一致）；另加一個反序案例 `zed` → `abc` → `mid` | 回傳順序為字母序 `abc, mid, zed` |
| T7 | `TestCoverageMixed` | 同時註冊 `StatusSupporter` 實作者 + 非實作者 | 兩列 row 各自 `Declares` 標記正確、排序正確 |

### 2.1 T5 邊界決議

實作 `StatusSupporter` 但回傳 nil：在 `Coverage` 內統一 normalize 為空 slice（`[]Status{}`），使 `Declares=true && Declared==nil` 不會出現 — 消除呼叫端判斷歧義。

## 3. TDD 執行順序

兩個 commit（邏輯分離、方便 review）：

### Commit 1 — `feat(agent): add StatusSupporter optional interface`

- 改動：`internal/agent/provider.go` 加 `StatusSupporter` interface（含 doc comment 說明 optional）
- 無獨立測試（interface 本身無行為，由 Commit 2 的 Coverage 測試間接覆蓋）
- 驗收：`go build ./...` 綠、`go vet ./...` 無 warning

### Commit 2 — `feat(agent): add Coverage helper for Status declaration matrix`

TDD 循序：

1. **紅**：新增 `coverage_test.go`，寫 T1（`TestCoverageEmpty`） → 執行失敗（`coverage.go` 尚不存在）
2. **綠**：建 `coverage.go`，最小實作：`Coverage` 回傳 nil → T1 綠
3. **紅**：加 T2（未實作 provider） → 失敗
4. **綠**：`Coverage` 走 `r.All()`、無 type assertion 檢查，填 `AgentType` + `Declares=false` + `Declared=nil` → T2 綠
5. **紅**：加 T3（實作 provider） → 失敗
6. **綠**：加 type assertion + 呼叫 `SupportedStatuses()` + defensive copy → T3 綠
7. **紅**：加 T4（defensive copy） → 應綠（已在 6 實作）；若未綠，補 copy 邏輯
8. **紅**：加 T5（空 slice normalize） → 根據實作可能綠或需補 normalize
9. **紅**：加 T6（排序穩定）+ T7（混合） → 失敗
10. **綠**：加 `sort.Slice` 按 `AgentType` 字母序 → T6 + T7 綠
11. **refactor**：抽取 `classify(provider)` helper 若有助於可讀性；否則保持內聯

驗收：
- `go test ./internal/agent/...` 全綠（新增 7 個測試 + 既有測試）
- `go vet ./...` 無 warning
- `go build ./...` 綠

## 4. 實作檔案預估

| 檔案 | 動作 | 預估行數 |
|---|---|---|
| `internal/agent/provider.go` | 加 `StatusSupporter` interface + doc comment | +6 |
| `internal/agent/coverage.go` | 新檔：`CoverageRow` + `Coverage()` + normalize | ~35 |
| `internal/agent/coverage_test.go` | 新檔：T1–T7 + stub | ~100 |
| **合計** | | **~140 行** |

（超出 spec 原本估的 ~75 行，原因是測試涵蓋率加厚 + stub 樣板 — 仍在 Phase 0 範圍內合理。）

## 5. 不做項目清單（防自動擴展）

以下在 subagent 執行期間若出現衝動，一律拒絕：

- 不幫任何 provider 實作 `SupportedStatuses()`（留 Phase 1）
- 不新增 `/api/agent/monitor/coverage` endpoint（留 Phase 5）
- 不寫 Coverage 結果的 JSON 序列化（留 Phase 5）
- 不加 drift 測試（留 Phase 1）
- 不 refactor `registry.go` / `provider.go` 既有部分
- 不把 `HookInstaller` / `StatuslineInstaller` 包成通用 optional capability 抽象

## 6. Subagent 開發指引

- **cwd 強制前綴**：每個 Bash 指令以 `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/lights-rebuild-spec && ` 開頭（依 `feedback_subagent_cwd_enforcement.md`）
- **分支**：已在 `worktree-lights-rebuild-spec`，不另切
- **Commit message 格式**：Conventional Commits，結尾加 `Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>`
- **不 push**：由主 session 負責 push + PR（Task 3）
- **回報**：完成後回報 commit hash 列表 + `git log --oneline -3` 輸出 + `go test ./internal/agent/...` 最後輸出摘要

## 7. 驗收清單（完整 Phase 0）

- [ ] 兩個 commit 符合上述 message 規範
- [ ] `go build ./...` 綠
- [ ] `go test ./internal/agent/...` 綠（新增 7 個測試全通過）
- [ ] `go vet ./...` 無 warning
- [ ] `git diff main..HEAD` 只涉及三個檔案（`provider.go` / `coverage.go` / `coverage_test.go`）
- [ ] `Coverage()` 對空 registry 回傳 nil、對未實作 provider 標 `Declares=false`、對實作 provider 標 `Declares=true` 且 `Declared` 為 defensive copy
