# Phase 1 TDD Plan — L2 狀態層對齊

- **Date**: 2026-04-23
- **Spec**: `docs/specs/2026-04-23-lights-rebuild-spec.md` §5
- **Worktree**: `lights-phase-1`（branch `worktree-lights-phase-1`）
- **依賴**: Phase 0（merged at `03925b1b`）— `StatusSupporter` interface + `Coverage()` helper 已就緒
- **範圍**：三家 provider 實作 `SupportedStatuses()` + codex 補齊 5 個事件 + opencode brand icon + 未知事件追蹤 + drift 測試

## 1. 契約鎖定

本節鎖定全 Phase 行為，避免實作期再決策。

### 1.1 SupportedStatuses() 宣告矩陣

每家 provider 在 `provider.go` 加 `SupportedStatuses()` 方法。宣告原則：**只列出該 provider `DeriveStatus` 在當前實作下可能 emit（且 `Valid=true`）的 Status 值**。Phase 1 結束時，drift 測試確保宣告 = 實作。

| Provider | 宣告 Statuses | 來源（DeriveStatus 中的 case → emit Status） |
|---|---|---|
| `cc` | `Running, Waiting, Idle, Error, Clear` | `UserPromptSubmit→Running` / `Notification(permission_prompt|elicitation_dialog)→Waiting` / `PermissionRequest→Waiting` / `SessionStart, Notification(idle_prompt|auth_success), Stop→Idle` / `StopFailure→Error` / `SessionEnd→Clear` |
| `codex` | `Running, Waiting, Idle, Error, Clear` | Phase 1 補齊後：`UserPromptSubmit→Running` / `Notification, PermissionRequest→Waiting` / `SessionStart, Notification(idle), Stop→Idle` / `StopFailure→Error` / `SessionEnd→Clear` |
| `opencode` | `Running, Waiting, Idle, Error, Clear` | 既有：`UserPromptSubmit→Running` / `PermissionRequest→Waiting` / `SessionStart, Stop→Idle` / `StopFailure→Error` / `SessionEnd→Clear` |

**不宣告的 Status 不得出現在 DeriveStatus emit 路徑中**。SubagentStart/Stop 在三家皆 `Valid=true` 但 `Status==""`，不影響宣告矩陣（drift 測試只看「宣告 vs 實際 emit 的非空 Status」）。

defensive copy 慣例：實作回傳 fresh slice（每次新 alloc），避免 `Coverage()` 重複 copy 與消費端誤 mutate。

### 1.2 Codex DeriveStatus 補齊

對照 cc 的既有 mapping，codex `status.go` 新增 5 個 case（保持與 cc 同型 + opencode 已示範的 detail 抽取）：

| Event | Status | Detail 抽取 |
|---|---|---|
| `Notification` | `notification_type=permission_prompt|elicitation_dialog → Waiting`；`idle_prompt|auth_success → Idle`；其他 → `Valid=false` | `{notification_type, message}` |
| `PermissionRequest` | `Waiting` | `{tool_name}` |
| `SubagentStart` / `SubagentStop` | `Valid=true`、`Status=""` | `{agent_id}` |
| `SessionEnd` | `Clear` | — |
| `StopFailure` | `Error` | `{error_details, error}` |

設計原則：codex 的 hook 事件 schema 對齊 cc（皆走 pdx hook CLI）— 直接複用 cc 的 raw key 名（`notification_type`, `tool_name`, `agent_id`, `error`, `error_details`）。Detail 欄位選擇與 opencode 對齊 `detailSubset()` 風格（只挑指定 keys）— 但 codex 沿用 cc 的 inline `map[string]any{key: raw[key]}` 寫法保持與既有檔案一致（一個 helper 抽不抽，留 follow-up issue 評估）。

**既有事件不動**：SessionStart / UserPromptSubmit / Stop 三個既有 case 保持原樣（Phase 0 不改之原則延續）。

### 1.3 OpenCode Brand Icon

`spa/src/lib/agent-icons.tsx` 加入 opencode brand icon。前置探查（subagent 第一步必做）：

1. `ls node_modules/@lobehub/icons-static-svg/icons/ | grep -i opencode` 確認是否有官方 svg
2. 若有：以 `wrapSvg(OpenCodeSvg)` 模式接入；若無：以 `?` Phosphor icon fallback（與 codex 的 `OpenAiLogo` 結構同型）

`getAgentIcon('opencode', options)` 從回傳 `undefined` 改為回傳 `AgentIconComponent`。`GetAgentIconOptions` 是否需新增 `opencodeVariant` 欄位由 subagent 視 svg 數量決定：單一 svg → 不加；兩個變體（如品牌 vs 簡化）→ 加。**預設只加單一品牌 icon，不引入 variant 抽象**（避免 Phase 1 範圍蔓延）。

導出位置：與 `CC_ICON_VARIANTS` / `CODEX_ICON_VARIANTS` 對齊（若有 variant 才 export，無則跳過）。Settings 預覽不在 Phase 1 範圍。

### 1.4 未知事件追蹤（handler.go）

**現況**：`provider.DeriveStatus` 回傳 `Valid=false` 時，handler.go 沒有顯式 early-return；事件落入後續 frame/projection/broadcast 流程，僅靠分支內的 `result.Valid` guard 抑制 status 更新與 activity watch。結果是「未知事件 silently 走完管線」。

**新行為**：在 `provider.DeriveStatus` 回傳後立即檢查 `result.Valid`：

- `result.Valid == false` → 寫一筆 trace step（`kind="verify"`、`decision="skipped"`、`reason="event_not_in_catalog"`、`payload=raw req`）→ `trace.Finish("completed", "event_not_in_catalog")` → 200 OK 回 `{"status":"ok","reason":"event_not_in_catalog"}` → return（**不走 frame / projection / broadcast / activity watch**）
- `result.Valid == true` → 維持現有流程

trace 寫入路徑：擴充 `hookTraceCollector` 加 `Catalog(req EventRequest, eventName, reason string)` 方法（與 `Verify` 同樣 kind="verify"，但 decision="skipped" 表達 catalog miss）。或直接複用 `Verify(req, "skipped", "event_not_in_catalog", nil)`。

**選擇**：複用 `Verify(...)`，無需新方法（kind 一致即可），但 parent 必為 triggerStepID（與 verify 早退路徑同型）。注意 `Verify` 內部會把 step 串到 `triggerStepID` 並更新 `verifyStepID` — catalog miss 走這條也 OK，因為 catalog miss 後 trace 立即 Finish，verifyStepID 不會被後續 frame/projection/emit 用到。

trace 必須在 catalog miss 分支顯式 `trace.Finish("completed", "event_not_in_catalog")` + 設 `traceFinished=true`，確保 defer 不重複 Finish。

**錯誤回應**：catalog miss 不算 error — 回 200 OK 與 `{"status":"ok","reason":"event_not_in_catalog"}`，與既有 verify_rejected（202 Accepted）區分（verify_rejected 是真的拒絕、catalog miss 是「收到但不認識」）。Hook CLI 端不需特殊處理（既有非 200 才 retry）。

### 1.5 Drift 測試

新檔 `internal/agent/drift_test.go`（package `agent_test`），對每家 provider 驗證宣告 = 實作：

- 表驅動：`{provider, event_name, raw_event, expected_status}` fixture 集合，覆蓋 §1.1 表格中每個 emit 路徑
- 對 `Coverage()` 的每一 row：
  - 取出 `Declared` slice → 形成 `declaredSet`
  - 對該 provider 跑所有 fixture → 收集 `emittedSet`（filter `Valid==true && Status!=""`）
  - 斷言：`declaredSet == emittedSet`（雙向 — 不能有未宣告的 emit、也不能有宣告但無實作的 emit）

fixture 來源：直接寫 inline（不複用 cc 的 hook payload fixture，避免跨 package 依賴）。每個事件以最小可代表 raw payload 表達（例 `Notification` 兩種 `notification_type` 各一筆覆蓋 Waiting + Idle）。

實作進路：
- 在測試檔內建一個 `providerFixtures` map：`map[string][]eventCase{eventName, rawJSON, expectedStatus}`（key 為 agent type）
- 註冊三家 provider → 跑 `Coverage(r)` → 對每 row run fixtures → 比對 set

**驗證 OFF-BY-ONE**：fixture 必須覆蓋每家 provider 宣告的**所有** Status（包含 cc 的 `Notification(idle_prompt)→Idle` 這種 sub-branch）。否則 drift 漏網 → false negative。Plan §3 的 TDD 紅綠循環會強制這點。

### 1.6 零改動邊界

以下檔案 Phase 1 **不得觸碰**：

- `internal/agent/provider.go`、`registry.go`、`status.go`、`coverage.go`（Phase 0 已完成）
- `internal/agent/{cc,codex,opencode}/hooks.go`（Codex 的 hook 安裝清單**不更新**，補齊 hook installer 是另一個 phase 的事）
- `internal/agent/{cc,opencode}/status.go`（只動 codex/status.go）
- `internal/agent/probe/**`
- `internal/store/**`（trace 寫入經 collector，不改 store schema）
- `internal/module/**` 除 `handler.go` 與必要的測試補強外不動
- `spa/**` 除 `agent-icons.tsx` 外不動

## 2. 測試案例清單

按檔案組織：

### 2.1 Coverage 宣告測試（既有 + 新增）

`internal/agent/coverage_test.go` 既有 8 case 不動。Phase 1 補一個整合 case：

| # | 名稱 | 情境 | 斷言 |
|---|---|---|---|
| C1 | `TestCoverageRealProviders` | 註冊真實 cc / codex / opencode provider | 三 row 皆 `Declares=true`、`Declared` 非空、皆包含 `Running, Waiting, Idle, Error, Clear` |

（不取代既有 stub-based 測試 — stub 測試守的是 `Coverage()` 邏輯本身，real-provider 測試守的是「真實 provider 已實作 interface」。）

### 2.2 Provider SupportedStatuses 單元測試

各 provider 的 `provider_test.go` 加：

| # | 檔案 | 名稱 | 斷言 |
|---|---|---|---|
| P1 | `cc/provider_test.go` | `TestCCSupportedStatuses` | 回傳 `[Running, Waiting, Idle, Error, Clear]`（順序由實作決定，斷言用 set 比對） |
| P2 | `codex/provider_test.go` | `TestCodexSupportedStatuses` | 同上 |
| P3 | `opencode/provider_test.go` | `TestOpenCodeSupportedStatuses` | 同上（注意 opencode 沒有現成 `provider_test.go` — 需新建） |
| P4 | 三家任一 | `TestSupportedStatusesReturnsFreshSlice` | 連續呼叫兩次回傳 slice 必為**不同 backing array**（mutate 第一次回傳不影響第二次） |

P4 只需在一家驗（cc 即可），三家用同樣的「直接 return literal」implementation pattern → 自然成立。

### 2.3 Codex DeriveStatus 5 個新事件測試

`codex/status_test.go` 新增：

| # | 名稱 | event / raw | 斷言 |
|---|---|---|---|
| CD1 | `TestCodexDeriveStatus_NotificationPermission` | `Notification` / `{notification_type:"permission_prompt"}` | `Valid=true, Status=Waiting, Detail.notification_type=="permission_prompt"` |
| CD2 | `TestCodexDeriveStatus_NotificationIdle` | `Notification` / `{notification_type:"idle_prompt"}` | `Valid=true, Status=Idle` |
| CD3 | `TestCodexDeriveStatus_NotificationUnknown` | `Notification` / `{notification_type:"weird"}` | `Valid=false`（與 cc 一致 — 未知 notification_type 視為 invalid） |
| CD4 | `TestCodexDeriveStatus_PermissionRequest` | `PermissionRequest` / `{tool_name:"Bash"}` | `Valid=true, Status=Waiting, Detail.tool_name=="Bash"` |
| CD5 | `TestCodexDeriveStatus_SubagentStart` | `SubagentStart` / `{agent_id:"abc"}` | `Valid=true, Status=="", Detail.agent_id=="abc"` |
| CD6 | `TestCodexDeriveStatus_SubagentStop` | `SubagentStop` / `{agent_id:"xyz"}` | 同 CD5（Status="") |
| CD7 | `TestCodexDeriveStatus_SessionEnd` | `SessionEnd` / `{}` | `Valid=true, Status=Clear` |
| CD8 | `TestCodexDeriveStatus_StopFailure` | `StopFailure` / `{error:"OOM"}` | `Valid=true, Status=Error, Detail.error=="OOM"` |

**移除**：既有 `TestCodexDeriveStatus_UnknownEvent`（line 37）測 `Notification` 為 invalid — 此 case 在 Phase 1 後 `Notification` 已被識別。改用 `{event:"FutureEvent"}` 作為新的 unknown event smoke test。

### 2.4 Drift 測試（新檔）

`internal/agent/drift_test.go`：

| # | 名稱 | 斷言 |
|---|---|---|
| D1 | `TestDriftDeclaredEqualsEmitted` | 對三家 provider，`declaredSet == emittedSet` |
| D2 | `TestDriftFixtureCoverageNonEmpty` | 防呆：每家 provider 的 fixture set 非空（避免有人 commit 一個空 fixture 然後 D1 假綠） |

D1 實作要點：
- 共用 `providerCases` table（map[agentType] → []case）
- 取 `Coverage(r)` 的每 row → 與 fixtures 比對
- 失敗訊息列出 declared / emitted set diff（方便 debug）

### 2.5 Handler Catalog Miss 測試

`internal/module/agent/handler_test.go` 加：

| # | 名稱 | 斷言 |
|---|---|---|
| H1 | `TestHandleEvent_CatalogMiss_WritesTraceAndReturnsOK` | 註冊 cc provider，POST 不認識的 event → response 200 + body `{"status":"ok","reason":"event_not_in_catalog"}` + trace chain 含 `verify` step with `decision=skipped, reason=event_not_in_catalog` + 無 frame/projection/emit step |
| H2 | `TestHandleEvent_CatalogMiss_NoStatusUpdate` | 同情境，`m.currentStatus[session]` 不變、`m.subagents[session]` 不變、無 broadcast |
| H3 | `TestHandleEvent_CatalogMiss_NoActivityWatch` | 同情境，`prober` 的 `StartWatch` 不被呼叫（用 mock 或計數器） |

H1/H2/H3 共用 setup：建一個 minimal Module（依 trace_test.go pattern），bypass verify（`verifyEventFn = always_accept`），mock prober，POST event → 斷言。

## 3. TDD 執行順序

Subagent 嚴格按以下順序執行；每個 step 一個 commit，commit message 用 Conventional Commits + `Co-Authored-By: Claude Opus 4.7 (1M context)`。

### Commit 1 — `feat(agent/cc): declare SupportedStatuses`
- 紅：寫 P1 → 失敗（cc.Provider 無 method）
- 綠：在 `internal/agent/cc/provider.go` 加 `SupportedStatuses()` return literal slice
- 跑 `go test ./internal/agent/cc/...` 綠

### Commit 2 — `feat(agent/opencode): declare SupportedStatuses`
- 紅：建 `internal/agent/opencode/provider_test.go` 寫 P3 → 失敗
- 綠：加 method
- 跑 `go test ./internal/agent/opencode/...` 綠

### Commit 3 — `feat(agent/codex): expand DeriveStatus to cover 5 events`
- 紅：寫 CD1-CD8 → 失敗（每個新 case）
- 綠：擴 `codex/status.go` 加 5 case + 修 unknown event smoke test
- 跑 `go test ./internal/agent/codex/...` 綠
- **不**在這個 commit 加 `SupportedStatuses` — 留 Commit 4

### Commit 4 — `feat(agent/codex): declare SupportedStatuses`
- 紅：寫 P2 → 失敗
- 綠：加 method
- 跑 `go test ./internal/agent/codex/...` 綠

（Commit 3/4 切分原因：DeriveStatus 補齊與宣告分離，方便 review 各看一塊；若 reviewer 要驗「宣告 = 真實實作」可看到 Commit 3 → 4 的 diff 是對應的。）

### Commit 5 — `test(agent): add coverage real-provider integration test`
- 紅：寫 C1（用真 provider 註冊 registry） → 應綠（前 4 commit 已實作）
- 此 commit 是「verification」性質 commit — 確保整合層綠

### Commit 6 — `test(agent): add declared-vs-emitted drift test`
- 紅：寫 D1 + D2 → 應綠（fixture 對齊 §1.1 表格）
- 若 D1 紅：補 fixture 或修 provider declaration（直到對齊）

### Commit 7 — `feat(agent): track unknown events as catalog miss`
- 紅：寫 H1-H3 → 失敗（handler 仍走後續流程）
- 綠：在 `handler.go` 的 `provider.DeriveStatus` 後加 `if !result.Valid` early-return + trace.Verify(...) + Finish + 200 OK response
- 跑 `go test ./internal/module/agent/...` 綠

### Commit 8 — `feat(spa): add OpenCode brand icon`
- 探：`ls spa/node_modules/@lobehub/icons-static-svg/icons/ | grep -i opencode`
- 改：`spa/src/lib/agent-icons.tsx`
- 跑 `cd spa && npx vitest run` 綠（無新測試 — icon 是純元件，靠 lint + build 把關）
- 跑 `cd spa && pnpm run lint && pnpm run build` 綠

### Commit 9 — `docs: phase 1 plan retrospective notes`（可選）
若 §3 順序與實際執行有偏差，補一段歷史事實註記（仿 Phase 0 plan §8）。

## 4. 實作檔案預估

| 檔案 | 動作 | 預估行數 |
|---|---|---|
| `internal/agent/cc/provider.go` | +`SupportedStatuses()` | +5 |
| `internal/agent/codex/provider.go` | +`SupportedStatuses()` | +5 |
| `internal/agent/codex/status.go` | +5 case + helper | +35 |
| `internal/agent/codex/status_test.go` | +CD1-CD8、修 unknown smoke | +60 |
| `internal/agent/opencode/provider.go` | +`SupportedStatuses()` | +5 |
| `internal/agent/opencode/provider_test.go` | 新檔 +P3 | +25 |
| `internal/agent/cc/provider_test.go` | +P1 + P4 | +30 |
| `internal/agent/codex/provider_test.go` | +P2 | +15 |
| `internal/agent/coverage_test.go` | +C1 | +35 |
| `internal/agent/drift_test.go` | 新檔 +D1 + D2 | +130 |
| `internal/module/agent/handler.go` | catalog miss early-return | +20 |
| `internal/module/agent/handler_test.go` | +H1-H3 | +120 |
| `spa/src/lib/agent-icons.tsx` | +opencode icon | +5 |
| `docs/specs/2026-04-23-lights-rebuild-phase-1-plan.md` | 本檔 | +250 |
| **合計** | | **~740 行**（含 plan 文件） |

## 5. 不做項目清單（防自動擴展）

以下衝動一律拒絕 — 由 subagent 在 PR description / commit message 中**明確聲明拒絕**：

- 不更新 `codexHookEvents` 清單加入新 5 個事件（hook installer 是 separate concern，等 codex CLI / proxy 路徑成熟）
- 不寫 OpenCode icon 的 variant 系統（單一 brand icon 即可，預設無 variant）
- 不抽 `detailSubset()` helper 到 `internal/agent` 共用（codex 沿用既有 inline 寫法）
- 不調整 cc / opencode 既有 DeriveStatus（即使發現可優化）
- 不加 `/api/agent/monitor/coverage` endpoint（留 Phase 5）
- 不改 trace.go schema（catalog miss 走既有 verify-kind 通道）
- 不引入 `Catalog()` 新 collector method（複用 `Verify()`）
- 不重整 `handler.go`（catalog miss 是局部增加，不順手 refactor）
- 不加 SubagentStart/Stop 的 Status（cc/opencode 既有設計 Status="" 表 detail-only — codex 沿用）

## 6. Subagent 開發指引

- **cwd 強制前綴**：每個 Bash 指令以 `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/lights-phase-1 && ` 開頭（依 `feedback_subagent_cwd_enforcement.md`）
- **分支**：已在 `worktree-lights-phase-1`，不另切；不 push（主 session 負責）
- **Commit message 格式**：Conventional Commits + 結尾加 `Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>`
- **TDD 嚴格紅綠**：每個 commit 內先寫測試跑紅再實作跑綠 — 不可批次寫完所有 test + 一次實作
- **回報**：完成後回報 commit hash 列表 + `git log --oneline -10` + `go test ./internal/agent/... ./internal/module/agent/...` 完整輸出 + `pnpm install` 與 `cd spa && pnpm run lint && pnpm run build` 結果
- **Codex sandbox 限制**：Codex sandbox 無網路 — subagent 在 worktree 內執行 `pnpm install`（依 `feedback_codex_sandbox_no_install.md`）。若失敗，由主 session 接手手動跑

## 7. 驗收清單（完整 Phase 1）

- [ ] 9 個 commits 符合上述 message 規範（Commit 9 可選）
- [ ] `go build ./...` 綠
- [ ] `go test ./...` 綠（覆蓋新增 P1-P4 / CD1-CD8 / C1 / D1-D2 / H1-H3 共 18 個測試）
- [ ] `go vet ./...` 無 warning
- [ ] `cd spa && pnpm run lint && pnpm run build` 綠
- [ ] `cd spa && npx vitest run` 綠
- [ ] PR diff 涉及檔案：§4 表格列出的 13 個檔 + plan 文件，**其餘不得出現**
- [ ] PR description 含「不做項目」§5 的明確聲明
- [ ] 三家 provider 在 `Coverage()` 回傳 `Declares=true`、`Declared` 非空、覆蓋 §1.1 表格 status set
- [ ] codex `DeriveStatus` 對 5 個新 event 產出 §1.2 表格 status / detail
- [ ] handler 對 `Valid=false` 早退寫 trace + 回 200 OK `event_not_in_catalog`
- [ ] OpenCode 在 SPA TabIcon 顯示 brand icon（手動驗證寫進 PR description checklist）

## 8. 風險與緩解

| 風險 | 機率 | 影響 | 緩解 |
|---|---|---|---|
| codex hook 不會 emit 新 5 事件 | 高 | DeriveStatus 寫了沒人觸發 | 接受 — 為未來 codex 版本鋪路；drift 測試保證宣告與實作一致 |
| `@lobehub/icons-static-svg` 無 opencode svg | 中 | icon fallback 需另尋 | subagent 第一步探查；若無，用 `?` Phosphor icon + 註記 follow-up issue 待官方 svg |
| handler catalog miss 改動破壞既有 contract | 低 | 已 deploy 的 hook CLI client 收到 200 OK 但 status reason 改變 | hook CLI 既有非 200 才 retry — 200 OK 視為「事件已收到」即可，reason 是 informational |
| drift 測試 fixture 不完整導致 D1 假綠 | 中 | 宣告/實作 mismatch 漏網 | D2 守 fixture 非空；§1.5 強調 fixture 必須覆蓋每個宣告 status 的所有 emit 路徑 |
| codex Notification 行為與 cc 不一致 | 中 | 假設 codex hook payload schema 對齊 cc 實際不同 | Phase 1 假設一致（單一 pdx hook CLI）；若 Phase 2-3 觀察到 schema 不同，另起 issue |

## 9. 兩輪 Codex Review 預期 focus

**第一輪（標準）**：
- Drift test 是否真能抓到漏網（試把 cc/codex/opencode 任一 declared status 拿掉一個，drift 應紅）
- Handler catalog miss early-return 是否影響既有測試（回歸 `handler_test.go` 全套）
- OpenCode icon 是否在三個 ccVariant × 兩個 codexVariant 組合下都不衝突

**第二輪 3 parallel**：
- **攻擊**：catalog miss 路徑 race / panic / SubagentStart 走到 catalog miss（不可能）/ trace finish 雙呼叫
- **防守**：宣告矩陣與 Phase 0 `Coverage()` 契約一致性、drift 測試的 fixture 覆蓋是否真完整、codex 5 個新 case 對照 cc 是否漏 corner（如 `Notification` 的 `auth_success`）
- **體質**：`drift_test.go` 是否過大（>200 行考慮拆 fixture 到獨立檔）、`handler.go` catalog miss 邏輯是否該抽 helper、`agent-icons.tsx` 是否該按 agent type 拆檔
