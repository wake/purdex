# L2 Proxy detach on Stop — Implementation Plan

> **Status**：v3（plan-review round 2 採納 1 medium + 2 low —— AC5/AC6/AC8 grep 移出 markdown table cell 走 raw pipe / row count wording 修正 22 labeled rows + 50 cases / T7b row 2 regression confirmation wording；plan freeze）
>
> v2 採納 round 1 全 10 條（2 blocker + 3 high + 3 medium + 2 low）；v3 採納 round 2 全 3 條（0 blocker + 0 high + 1 medium + 2 low）
> **依賴 spec**：`docs/specs/2026-05-01-lights-l2-proxy-detach-on-stop-spec.md` v5 final
> **Worktree**：`.claude/worktrees/lights-l2-proxy-detach` / branch `worktree-lights-l2-proxy-detach`
> **Base**：`origin/main` @ alpha.281（J3 `56b3ba55` + bump `5736f87e`）
> **拆分**：Phase 1（T1+T2+T3 純函式層 — Subagent A）→ Phase 2（T4+T5+T6 helper 層 — Subagent B，**Phase 1 全 commit 後啟動避 subagent.go 寫衝突**）→ Phase 3（T7a+T7b+T8a+T8b+T9 integration — 主 session）→ Phase 4（T10 doc + 整合驗證）→ PR

---

## 0. Plan 總覽

### 0.1 範圍重申（per spec §1.1 / §3）

L2 codex broker turn-aware proxy detach — long-lived broker 完成 logical dispatch（turn）時，detach 父 frame 的 proxy `SubagentRef` 讓燈滅，無需等 broker process 死。

**核心 in-scope**（spec §3 / §4）：
- #1：`SubagentRef.SourceTurnID` 欄位（`omitempty`，無 DB migration）
- #2：兩個獨立 lookup helper（`subagentRefMatches` turn-aware / `findProxyRefByBroker` process-level，註解互引避 DRY refactor）
- #3：codex 解析 `parseCodexTurnID`（fail-soft）
- #4：`upsertProxyRefForBroker` 新 helper（不 reuse `mutateSubagentsWithRetry`，避 v3 F1 race）
- #5：`removeProxyRefForSenderTurn` + `detachProxyRefForSenderTurnWithRetry` mirror 既有 helper pattern；**signature 對齊既有 `removeProxyRefForSender(paneID, pid, startTime, broadcastTs)` —— pane-scan 而非 parent-bound**（spec §3.3.D 表簽名所示）
- #6：`PdxPreToolUse` catalog change（`LifecycleNone` → `LifecycleUserPromptSubmit`，`Handling` 欄位 omitted → `EffectiveHookHandling` default `HookHandlingDetail`）+ `deriveCodexStatus` 新 case 回 `Valid: true`
- #7：`applyFrameEvent` 兩新 lifecycle case
  - `LifecycleUserPromptSubmit`：codex gate 後 attach/upsert（含 PreToolUse no-parent skip guard，per spec §3.3.C.1）—— attach 階段走 `findProxyParent`（PPID walk）對稱 SessionStart fast-path
  - `LifecycleStop, LifecycleStopFailure`：三 sub-case dispatch（per spec §3.3.D table）—— **不走 `findProxyParent`**，所有 detach 走 pane-scan helper 對稱既有 `removeProxyRefForSender`
- #8：完整 test matrix（spec §5 row 1-20，含 row 16 concurrency strict 三 forbidden state 斷言）

**Out-of-scope reaffirm**（spec §6）：
- 不動 governance P2/P3 broker kill/sweep
- 不動 standalone codex Stop 主 agent 燈號（J3 已 ship）
- 不動 native subagent SubagentStart/Stop 路徑
- 不動 cc/opencode turn-aware identity（spec §3.5 L3 forward constraint 不在本 PR 兌現）
- 不動 SessionEnd wildcard detach 路徑（cc/opencode 既有，codex 沒 SessionEnd）
- 不引 empty-tool turn dot extinction 修法（spec §3.5 L1 已知 limitation）

### 0.2 估計

- 總 production code：~280-380 行（含 spec §4 表的 7 file 改動，AC6 cap 對齊）
- 總 test code：~280-400 行（spec §5 全 20 row + concurrency pattern）
- 預估 PR diff：~525-735 raw / ≤850 effective（spec AC6 cap）
- 預估時間：6-9 小時 subagent TDD（Phase 1 ~2hr / Phase 2 ~2.5hr / Phase 3 ~3.5hr / Phase 4 ~1hr）+ 兩輪 codex review 2-3hr

### 0.3 鎖序與不變式（per spec §3.4）

實作時必持守：
- `mutateSubagentsWithRetry` etag-based optimistic concurrency 機制不改
- 新 `upsertProxyRefForBroker` reuse `UpsertIfUnchanged(frame, expected)` retry pattern（max `proxyUpsertMaxAttempts=3`）
- `findProxyRefByBroker` 與 `subagentRefMatches` 兩 helper 各自負責不同 intent，互不 reuse（註解明寫）
- `parseCodexTurnID` 為純函式，fail-soft（JSON parse error / 缺 field → ""）
- 新 lifecycle case 第一行 `if req.AgentType != "codex" { break }` —— cc/opencode 走 fall-through 既有 path 不變
- PreToolUse no-parent path **skip return early**，**不 fall through generic frame-creation**（spec §3.3.C.1 + §5 row 20）
- Stop case 對 detach 對象的查找走 **pane-scan**（`ListByPane` + `subagentsContainProxySender`-style filter），對齊既有 `removeProxyRefForSender:798` 設計；不走 `findProxyParent` PPID walk

### 0.4 Spec 收斂歷程備註

Spec v1 → v5 共 5 輪 codex review 收斂（核心 race → 設計缺陷 → impl typo → wording）。**不再審 spec**，剩餘風險靠 PR review 兜底（spec §10 兩輪 codex review）。

Plan v1 → v2 採納 codex round-1 review 全 10 finding（job `task-mondbqoz-liqpwd`）：B1 pane-scan 對齊 / B2 row 重分派 / H1 parse-failure 用 matching broker ref / H2 §7 spec 對齊 / H3 T7+T8 拆 a/b / M1-M3 wording / L1-L2 nitpick。

---

## 1. Phase 1：純函式層（Subagent A）

可獨立完成。Subagent A 拿全部 T1+T2+T3，每 task 獨立 commit。Phase 1 全 commit 後再啟動 Subagent B（避免兩 subagent 同時改 `subagent.go` 衝突）。

### P1-T1 — `PdxPreToolUse` catalog 改 + `deriveCodexStatus` 新 case + 既有 fixture 修

**目標**：把 `PdxPreToolUse` 從 `LifecycleNone` + `HookHandlingUnsupported` 改成 `LifecycleUserPromptSubmit` + `Handling` 欄位 omitted（走 `EffectiveHookHandling` default `HookHandlingDetail`），並讓 `deriveCodexStatus` 對 `PdxPreToolUse` 回 `Valid: true`（避免 `handler.go:248` early-return）。

**改動**：

| 檔案 | 改動 |
|---|---|
| `internal/agent/codex/events.go:91-97` | `PdxPreToolUse`：`Lifecycle: LifecycleUserPromptSubmit`（was `LifecycleNone`）；**移除 `Handling` 欄位**（was `HookHandlingUnsupported`）→ 走 `provider.go:171-178` `EffectiveHookHandling` default `HookHandlingDetail`。註解：`// L2: PreToolUse 是 codex non-prompt turn 的 attach trigger（per spec §3.3.C strategy a）`。 |
| `internal/agent/codex/status.go` | `deriveCodexStatus` 加 `case "PdxPreToolUse"` 回 `agent.DeriveResult{Valid: true, Detail: map[string]any{...}}` 空 `Status` —— 仿 `status.go:67-72` `PdxSubagentStart, PdxSubagentStop` detail-only pattern。 |
| `internal/agent/codex/events_test.go` | 既有 catalog test fixture 期望從 `Lifecycle: LifecycleNone, Handling: HookHandlingUnsupported` 改成 `Lifecycle: LifecycleUserPromptSubmit, Handling: ""`（或 `HookHandlingDetail` after EffectiveHookHandling）。 |
| `internal/agent/codex/status_test.go` | 加 `TestDeriveCodexStatus_PdxPreToolUse` —— 純 unit test，input `{"hook":"PdxPreToolUse"}` raw → `result.Valid == true && result.Status == ""`。 |

**Test**：

- 先寫 `TestDeriveCodexStatus_PdxPreToolUse`（**TDD 順序：先測 fail，後 status.go 補 case → 綠**）
- 再修 `events_test.go` fixture（catalog change → 既有 fixture 失敗 → 改 expectation → 綠）
- `go test ./internal/agent/codex/...` 全綠

**Acceptance**：
- `go build ./...` 全綠
- `rg 'PdxPreToolUse|HookHandlingUnsupported|LifecycleNone' internal/agent/codex internal/agent/cc internal/agent/opencode` 結果：cc/opencode 不變，codex 內 `PdxPreToolUse` 不再出現 `LifecycleNone` / `HookHandlingUnsupported`（per spec AC8）
- `provider.go:80-83` `HookHandling` vocabulary **未被改動**（`HookHandlingHandled` 不存在，spec §3.3.C Part 1 已記）

**估計**：~30 行 production / ~10 行 test

**依賴**：none（首先做）

---

### P1-T2 — `SubagentRef.SourceTurnID` 欄位 + JSON round-trip test

**目標**：把 `SubagentRef` struct 加 `SourceTurnID string` field（`json:"source_turn_id,omitempty"`），確保 wire format backward-compatible（舊 row deserialize 為 zero-value ""）。

**改動**：

| 檔案 | 改動 |
|---|---|
| `internal/agent/subagent.go:9-16` | append `SourceTurnID string \`json:"source_turn_id,omitempty"\`` field 到 struct 末尾。註解：`// L2: codex turn_id；其他 provider fallback 到 (PID, StartTime)；無 DB migration（subagents_json TEXT opaque blob）`。 |
| `internal/agent/subagent_test.go`（若存在；不存在則新建） | `TestSubagentRef_JSONRoundTrip_SourceTurnID`：兩 case—（a）`SourceTurnID="t_a"` → marshal 含 `"source_turn_id":"t_a"`；（b）`SourceTurnID=""` → marshal **不含** `source_turn_id` key（omitempty）。 |

**Test**：

- TDD 順序：先寫 round-trip test → fail（field 不存在）→ 加 field → 綠
- `go test ./internal/agent/...` 全綠
- 既有 `frames.go:42` `subagents_json TEXT` 不需改 schema（opaque blob）

**Acceptance**：
- `omitempty` 行為驗證（兩 case 都要）
- 舊 JSON（無 `source_turn_id`）unmarshal 後 `SourceTurnID == ""`
- AC7 `SubagentRef` JSON round-trip preserves `SourceTurnID` when set, omits it when empty

**估計**：~5 行 production / ~30 行 test（兩 case + helper）

**依賴**：none（與 T1 並行）

---

### P1-T3 — `parseCodexTurnID` 純函式

**目標**：在 `internal/module/agent/raw_codex_event.go`（**新建**）寫 `parseCodexTurnID(rawEvent json.RawMessage) string` —— 從 raw event 解出 codex turn_id，fail-soft（JSON parse error / 缺 field / 非 string → ""）。

**改動**：

| 檔案 | 改動 |
|---|---|
| `internal/module/agent/raw_codex_event.go` *(新建)* | `parseCodexTurnID(raw json.RawMessage) string`：unmarshal 到 anonymous struct（only field `TurnID string \`json:"turn_id"\``），error 或空 → 回 ""。**AgentType pre-gating 在 caller**（spec §4 表）—— 此 helper 不 re-check。 |
| `internal/module/agent/raw_codex_event_test.go` *(新建)* | `TestParseCodexTurnID`：5 case—（a）valid `{"turn_id":"t_a"}` → "t_a"；（b）missing `turn_id` field → ""；（c）malformed JSON → ""；（d）`turn_id` 非 string（int / null）→ ""；（e）empty string `""` → ""。 |

**Test**：

- TDD 順序：先寫 5-case table-driven test → fail（檔不存在）→ 寫 helper → 綠
- 純函式測試，無 mock / 無 fixture file

**Acceptance**：
- 不引入 panic 或 error 回傳路徑（只回 string）
- 不新增 cap；`RawEvent` 來源沿用既有 hook ingestion 限制（`path_hint_extractor.go:15` 64KB cap 在 ingestion 上游已套），parser 本身只做 fail-soft unmarshal（**L1/v2 fix**）
- 5 case 全綠

**估計**：~25 行 production / ~30 行 test

**依賴**：none（與 T1 / T2 並行）

---

## 2. Phase 2：Helper 層（Subagent B，Phase 1 全 commit 後啟動）

依賴 P1-T2 的 `SubagentRef.SourceTurnID` field。為避免 Subagent A / B 同時改 `subagent.go` 衝突，**Subagent B 等 Phase 1 三 commit 全進 worktree branch 後啟動**（保守序列）。主 session 在 Phase 1 全 commit 後 push HEAD 給 Subagent B。

### P2-T4 — `subagentRefMatches` turn-aware 升級 + `findProxyRefByBroker` 新增

**目標**：`subagentRefMatches`（`frame_ops.go:631-639`）升級為 turn-aware（per spec §3.2.A）；新增 `findProxyRefByBroker`（per spec §3.2.B）作 process-level lookup helper。兩 helper 註解互引以防後人 DRY refactor 重蹈 v3 F1 race。

**改動**：

| 檔案 | 改動 |
|---|---|
| `internal/module/agent/frame_ops.go:631-639` | 改 `subagentRefMatches` 對 `IsProxy=true` ref：兩端 `SourceTurnID` 任一為 "" → 回 process-level（PID + StartTime）；兩端非空 → 比 `SourceTurnID`。註解：`// turn-aware equality; 用於 Stop targeted detach。process-level lookup 走 findProxyRefByBroker（不 reuse 此 helper，避免 turn_id mismatch 被當 no-match → 重複 append duplicate ref，per spec §3.2 F1 fix）`。 |
| `internal/module/agent/frame_ops.go`（新增 helper）| `findProxyRefByBroker(refs []SubagentRef, pid int, startTime string) int` —— 純 process-level lookup，回 index 或 -1。註解：`// process-level lookup; 用於 attach/upsert（in-place 改 SourceTurnID）+ Stop empty-turnID parse-failure 分支判 SourceTurnID。turn-aware equality 走 subagentRefMatches（不 reuse 此 helper，避 Stop targeted detach 撞上 stale-turn ref，per spec §3.2 F1 fix）`。 |
| `internal/module/agent/frame_ops_test.go` | `TestSubagentRefMatches_TurnAware`：**7 case**—（a）兩端 turn_id 都 ""（process fallback）→ true；（b）一端 ""，另端非空 → true（fallback）；（c）兩端非空且相等 → true；（d）兩端非空但不等 → false；（e）IsProxy 一邊 true 一邊 false → false；**（f）native ref same `ID` → true**；**（g）native ref different `ID` → false**（**L2/v2 fix**）。`TestFindProxyRefByBroker`：3 case—（a）match 第一個 → 回 0；（b）match 第二個 → 回 1；（c）no match → 回 -1。 |

**Test**：

- TDD 順序：先寫兩個 test → fail → 改 helper → 綠
- 純函式測試，無 store mock

**Acceptance**：
- 兩 helper 註解明寫互不 reuse 的理由（spec §3.2 cross-reference 防 DRY refactor）
- 既有調用 `subagentRefMatches` 的 path 全部編譯通過（grep 確認）
- native `IsProxy=false` 路徑明確 pin 在 ID equality（L2/v2 fix）

**估計**：~40 行 production / ~60 行 test

**依賴**：P1-T2（`SourceTurnID` field 必需）

---

### P2-T5 — `upsertProxyRefForBroker` + retry helper

**目標**：新增 `upsertProxyRefForBroker(parent, pid, startTime, turnID, broadcastTs) (bool, store.Frame, error)` —— attach/upsert 邏輯，包 optimistic concurrency retry（per spec §3.4）。**不 reuse `mutateSubagentsWithRetry`**（避免 v3 F1 race，註解明寫原因）。

**改動**：

| 檔案 | 改動 |
|---|---|
| `internal/module/agent/frame_ops.go`（新增）| `upsertProxyRefForBroker`：(1) reload parent via `m.frames.GetByIdentity(...)`；(2) 呼 `findProxyRefByBroker` locate index；(3) found → in-place 改 `refs[i].SourceTurnID = newTurnID, refs[i].StartedAt = broadcastTs`；(4) not found → append `SubagentRef{IsProxy: true, SourcePID: pid, SourceStartTime: startTime, SourceTurnID: turnID, ID: fmt.Sprintf("proxy:codex:%d:%s", pid, startTime), StartedAt: broadcastTs, ...}`（ID format 對齊 `frame_ops.go:218` SessionStart fast-path）；(5) `UpsertIfUnchanged(frame, expected)` retry up to `proxyUpsertMaxAttempts=3`；(6) retry 時 re-read parent → re-run 2-5 fresh `findProxyRefByBroker`。註解：`// L2: 不 reuse mutateSubagentsWithRetry —— 該 helper 的 SubagentStart 分支用 subagentRefMatches（turn-aware），會把 (PID,StartTime,turn_b) 對 (PID,StartTime,turn_a) 當 no-match → append duplicate（spec §3.4 F1 fix）`。 |
| `internal/module/agent/frame_ops_test.go` | `TestUpsertProxyRefForBroker`：4 case—（a）首次 attach（refs 空）→ 1 ref，含完整 (PID, StartTime, turnID, ID)；（b）existing turn_id="" → in-place 改成 turnID（仍 1 ref）；（c）existing turn_id="t_a" → in-place 改成 turn_b（仍 1 ref，非 append）；（d）retry 路徑 mock：第一次 `UpsertIfUnchanged` 回 `ErrConflict` → 第二次成功（fake `frames` store with attempt counter）。 |

**Test**：

- TDD 順序：先寫 case (a)+(b)+(c) → fail → 寫 helper → 綠 → 加 case (d) retry → fail → 加 retry loop → 綠
- Retry 路徑 mock 仿 `mutateSubagentsWithRetry` test pattern

**Acceptance**：
- 不引入新的 store interface method（reuse `GetByIdentity` + `UpsertIfUnchanged`）
- ID 格式 `proxy:codex:%d:%s` 對齊既有 fast-path（spec §5 row 7 assertion）
- Race-mode test 全綠：`go test -race ./internal/module/agent/...`

**估計**：~50 行 production / ~80 行 test

**依賴**：P2-T4（`findProxyRefByBroker` 必需）

---

### P2-T6 — pane-scan `removeProxyRefForSenderTurn` + `detachProxyRefForSenderTurnWithRetry`

**目標**：mirror 既有 `removeProxyRefForSender`（`frame_ops.go:798`）+ `detachProxyRefWithRetry`（`frame_ops.go:887-929`）pattern，**signature 對齊 pane-scan**（B1/v2 fix）：top-level `removeProxyRefForSenderTurn(paneID, pid, startTime, turnID, broadcastTs)` 走 `ListByPane` 找 owner frame，再呼叫 frame-level `detachProxyRefForSenderTurnWithRetry(frame, pid, startTime, turnID, broadcastTs)` 做 retry-safe detach。

**改動**：

| 檔案 | 改動 |
|---|---|
| `internal/module/agent/frame_ops.go`（新增 top-level helper）| `removeProxyRefForSenderTurn(paneID string, pid int, startTime, turnID string, broadcastTs int64) (bool, store.Frame, any, any, error)` —— **pane-scan**：(1) `ListByPane(paneID)` → frames；(2) for each frame → `subagentsContainProxySenderTurn(frame.Subagents, pid, startTime, turnID)` 預檢；(3) 命中 → 呼 `detachProxyRefForSenderTurnWithRetry(frame, pid, startTime, turnID, broadcastTs)`；(4) 找不到 owner → 回 `(false, zero, nil, nil, nil)`。**Signature / control flow 對齊 `removeProxyRefForSender:798-827`**。 |
| `internal/module/agent/frame_ops.go`（新增 frame-level helper）| `detachProxyRefForSenderTurnWithRetry(frame, pid, startTime, turnID, broadcastTs)` —— wrap retry loop，filter `refs[i].IsProxy && refs[i].SourcePID == pid && refs[i].SourceStartTime == startTime && refs[i].SourceTurnID == turnID` 三全 match 才 drop（process-level filter 用既有 `removeProxyRefForSender` path，turn-level filter 走此新 helper）。retry 仿 `detachProxyRefWithRetry:887-929` 結構（reload + filter + UpsertIfUnchanged + retry up to 3）。 |
| `internal/module/agent/frame_ops.go`（新增 pure filter helper）| `subagentsContainProxySenderTurn(refs []SubagentRef, pid int, startTime, turnID string) bool` —— mirror `subagentsContainProxySender:831-838`，多 turnID 三全 match 才 true。 |
| `internal/module/agent/frame_ops_test.go` | `TestRemoveProxyRefForSenderTurn_PaneScan`：5 case—（a）pane 內 1 frame 有 matching ref → 1 detach；（b）pane 內 2 frame 各有 matching broker 但 turnID 不同 → 只 detach 對應 turnID 那 frame；（c）pane 內無 frame match → 回 false；（d）IsProxy=false ref 即使 ID 字串巧合 → 不 drop；（e）turnID 不 match → 不 drop。`TestDetachProxyRefForSenderTurnWithRetry`：1 case retry 路徑（mock conflict-then-success）。 |

**Test**：

- TDD 順序：先寫 `subagentsContainProxySenderTurn` 純 helper → 4 case → 綠 → 寫 `detachProxyRefForSenderTurnWithRetry` retry → 綠 → 寫 top-level `removeProxyRefForSenderTurn` pane-scan → 5 case → 綠
- Race-mode test 包含

**Acceptance**：
- 不複用 `removeProxyRefForSender`（process-level）—— 兩函式並存（per spec §3.2 boundary）
- Top-level signature 是 `(paneID, pid, startTime, turnID, ts)` —— **pane-scan 對齊 spec §3.3.D 表所示簽名**（B1/v2 fix）
- Retry 邏輯與既有 `detachProxyRefWithRetry` 行為一致（max 3, etag-based）
- `go test -race` 全綠

**估計**：~70 行 production / ~80 行 test

**依賴**：P2-T4（`subagentRefMatches` turn-aware 完成；雖此 helper 不直接用，但 race semantics 對齊）

---

## 3. Phase 3：Integration（主 session）

T7a / T7b / T8a / T8b / T9 涉及 `applyFrameEvent` switch case 新增 + 行為串接，cross-cutting 高、需 spec / fast-path / generic path 全圖在腦中，**主 session 自己做不派 subagent**。

**Row 重新分配（B2/v2 fix）**：
- T7a UserPromptSubmit case body：rows **1, 5, 7, 17, 17b**
- T7b PreToolUse case wiring + no-parent guard：rows **2, 20**
- T8a codex Stop targeted detach + sub-cases：rows **3, 4, 6, 8, 9, 10**
- T8b regression / fallback：rows **11, 12, 13, 18**
- T9 advanced / concurrency / PID-reuse：rows **14, 15, 15b, 16, 19**

每個 task 的 acceptance 只認自己 row 全綠，不跨 task。

### P3-T7a — `LifecycleUserPromptSubmit` case body（codex gate + UserPromptSubmit attach/upsert）

**目標**：在 `applyFrameEvent`（`frame_ops.go:61`）lifecycle switch 新增 `case agentpkg.LifecycleUserPromptSubmit:` —— 處理 codex `PdxUserPromptSubmit`（PreToolUse 在 T7b 接同 case body）。codex AgentType gate + frame == nil + proxy parent 找 + upsert。

**改動**：

| 檔案 | 改動 |
|---|---|
| `internal/module/agent/frame_ops.go`（switch case 新增）| 第一行 `if req.AgentType != "codex" { break }`（cc/opencode fall through 到既有 generic path 不變，per spec §3.3.B + §5 row 17/17b）。第二行 `if frame != nil { break }`（既有 narrow `UpdateHookPath:440` 處理）。第三行 `parent := findProxyParent(req)`，若 nil → 對 `PdxUserPromptSubmit` 走 break 讓既有 generic path 處理（保持 v4 行為，per spec §3.3.C.1 區分）。第四行 `turnID := parseCodexTurnID(req.RawEvent)`（fail-soft → ""）。第五行 `upsertProxyRefForBroker(parent, req.SourcePID, req.SourceStartTime, turnID, req.BroadcastTs)`，trace `Reason: "proxy_subagent_attached_on_user_prompt"`（首次 attach）or `"proxy_subagent_upserted_on_user_prompt"`（in-place upsert）。 |
| `internal/module/agent/frame_ops_test.go`（test 新增 row 1, 5, 7, 17, 17b）| 5 row 從 spec §5 表抓 setup + action + expected + validates 寫 table-driven。每 row 用 store mock + helper assert frame.Subagents 結果 + trace reason 結果。 |

**Test**：

- TDD 順序：5 row 一輪寫 → 跑 fail → 補 case body → 綠
- Row 17（opencode break early）+ row 17b（cc break early）必驗 cc/opencode 走原有路徑不變（regression guard）

**Acceptance**：
- 5 row 全綠
- cc/opencode 既有 frame_ops 行為 zero regression（既有 cc/opencode 相關 test 也跑）
- `go test -race ./internal/module/agent/...` 全綠
- AC1 satisfied（無新 exported types）

**估計**：~50 行 production / ~100 行 test

**依賴**：P1-T1, P1-T2, P1-T3, P2-T4, P2-T5（全部 Phase 1+2 完）

---

### P3-T7b — PreToolUse wiring 進同 case body + no-parent guard

**目標**：T7a 同 lifecycle case 已 wire UserPromptSubmit；T7b 補 PreToolUse 走入同 case（catalog change 在 T1 已做，這裡是 case body 的差異化處理：no-parent guard）。

**改動**：

| 檔案 | 改動 |
|---|---|
| `internal/module/agent/frame_ops.go`（同 T7a case body 內加分支）| T7a 第三行的 `parent == nil` 分支：若 `req.Hook == "PdxPreToolUse"` → trace `Decision: "skipped", Reason: "pre_tool_without_proxy_parent"` **return early**（per spec §3.3.C.1 + §5 row 20，不 fall through `frame_ops.go:251` onward 的 generic frame 創建）。若是 `PdxUserPromptSubmit` 維持 break（T7a 既定行為）。 |
| `internal/module/agent/frame_ops_test.go`（test 新增 row 2, 20）| Row 2 verify PreToolUse from PID=42 raw turn_id="t_a" + parent 已存在 → 共用 T7a upsert 路徑得相同結果。Row 20 verify PreToolUse no-parent → frame 沒被創、`Subagents` 沒 mutate、無 broadcast、trace `pre_tool_without_proxy_parent`。 |

**Test**：

- **T7b 真正 red→green checkpoint 是 row 20**（PreToolUse no-parent guard）。Row 2 是 T7a shared case body 的 regression confirmation —— catalog wired in T1，case body in T7a，所以 row 2 在 T7b 起始時應已綠（純驗 PreToolUse 走進共用 upsert 路徑）
- TDD 順序：先補 row 20 → fail → 加 PreToolUse-specific skip return → 綠 → 補 row 2 regression check（應已綠）

**Acceptance**：
- 2 row 全綠
- Row 20 strict assert：`m.frames` count 不變、無 `frame_apply` log line for new frame、無 `currentStatus[tmuxSession]` write
- T7a 既有 5 row 仍綠（regression）

**估計**：~15 行 production / ~50 行 test

**依賴**：T7a

---

### P3-T8a — `LifecycleStop, LifecycleStopFailure` case body（codex Stop 三 sub-case）

**目標**：在 `applyFrameEvent` switch 新增 `case agentpkg.LifecycleStop, agentpkg.LifecycleStopFailure:` —— L2 核心 detach 邏輯，codex 路徑三 sub-case dispatch（per spec §3.3.D table）。**全程 pane-scan，不走 `findProxyParent`**（B1/v2 fix）。Non-codex 走 cc/opencode wildcard fallback（T8b 補 row 11/18 regression）。

**改動**：

| 檔案 | 改動 |
|---|---|
| `internal/module/agent/frame_ops.go`（switch case 新增）| 第一行 `if frame != nil { break }`（standalone agent，J3 dispatcher 處理，per spec §3.3.D + §5 row 12）。第二行 `if req.AgentType != "codex" { /* 走 wildcard fallback */ removeProxyRefForSender(req.PaneID, req.SourcePID, req.SourceStartTime, req.BroadcastTs); trace "proxy_subagent_detached_on_stop"; break }`。第三行 `turnID := parseCodexTurnID(req.RawEvent)`。第四行（codex 三 sub-case）：(a) `turnID != ""` → `removeProxyRefForSenderTurn(req.PaneID, req.SourcePID, req.SourceStartTime, turnID, req.BroadcastTs)`（pane-scan helper from T6），結果 detached → trace `proxy_subagent_detached_on_stop_turn`，未 detached → trace `proxy_subagent_stop_no_match`；(b) `turnID == ""` → **pane-scan 找 matching broker ref**：`ListByPane(paneID)` → for each frame `findProxyRefByBroker(frame.Subagents, req.SourcePID, req.SourceStartTime)` 找到 → 看該 ref 的 `SourceTurnID`；ref.SourceTurnID != "" → **skip detach**, trace `proxy_subagent_stop_parse_failed`（H1/v2 fix —— 用 matching broker ref 判，非任意 ref）；ref.SourceTurnID == "" → wildcard `removeProxyRefForSender`, trace `proxy_subagent_detached_on_stop`；(c) pane-scan 找不到 matching broker → break（無 detach 對象，trace 略）。 |
| `internal/module/agent/frame_ops_test.go`（test 新增 row 3, 4, 6, 8, 9, 10）| 6 row 表驅動。Row 6 是 StopFailure parity（必驗 LifecycleStop / LifecycleStopFailure 兩 enum 都 match 同 case body）。Row 10 multi-broker isolation（PID=42 detach 不影響 PID=43）。 |

**Test**：

- TDD 順序：6 row 一輪寫 → fail → 補三 sub-case dispatch → 綠
- Row 8（codex empty turnID + ref 非空 turnID）必驗 H1/v2：用 matching broker ref 判，不是任意 ref —— 加 mixed-broker test：parent 含 ref(PID=42, turnID="") + ref(PID=43, turnID="t_x")，sender PID=42 Stop 空 turnID → 該走 wildcard（PID=42 ref turnID 是 ""），不被 PID=43 的 turnID 影響

**Acceptance**：
- 6 row 全綠
- 三 sub-case trace reason 與 spec §3.3.D table 完全對應
- Mixed-broker parse-failure case 綠（H1/v2 fix）
- `go test -race ./internal/module/agent/...` 全綠

**估計**：~60 行 production / ~120 行 test

**依賴**：P2-T6（detach helper 必需）

---

### P3-T8b — Stop case regression / fallback rows

**目標**：補 spec §5 表的 cc/opencode wildcard regression rows（11, 18）+ standalone Stop short-circuit row（12）+ native isolation row（13）。覆蓋 T8a 的 non-codex fallback branch + frame != nil short-circuit + native ref isolation。

**改動**：

| 檔案 | 改動 |
|---|---|
| `internal/module/agent/frame_ops_test.go`（test 新增 row 11, 12, 13, 18）| Row 11 `parent opencode + 1 ref(PID=42, t1, turnID="", Type=cc)` cross-type proxy + AgentType=cc SessionEnd → wildcard `removeProxyRefForSender` 既有路徑 unchanged。Row 12 sender 自己 own frame → frame.Subagents 不變、無 detach attempt（first-line short-circuit）。Row 13 native ref（IsProxy=false）isolation —— even with PID/turnID 巧合 match string，不 drop。Row 18 cc Stop with no turn_id + ref 空 SourceTurnID → wildcard detach via §3.3.D fallback。 |

**Test**：

- TDD 順序：4 row 一輪寫 → 應 mostly 綠（regression coverage，T8a 已實作 fallback path）→ 任何 fail 修
- Row 11 必驗 SessionEnd 既有路徑 zero-touch（T8a 不加 SessionEnd handler）

**Acceptance**：
- 4 row 全綠
- AC8 grep 驗：cc/opencode 內 `PdxPreToolUse` reference（若有）不變

**估計**：~5 行 production（修補若有）/ ~80 行 test

**依賴**：T8a

---

### P3-T9 — Advanced / concurrency / PID-reuse tests

**目標**：補 5 個進階場景 test —— row 14 idempotency / row 15 sequential 全流程 / row 15b 同 turn 並發 upsert race-safety / row 16 turn-change vs Stop 並發 / row 19 PID-reuse safety。Row 15b / 16 用 `sync.WaitGroup` + 多 goroutine pattern（per spec §5.1 + 仿 `internal/store/agent_event_test.go:198`）。

**改動**：

| 檔案 | 改動 |
|---|---|
| `internal/module/agent/frame_ops_test.go`（test 新增 row 14, 15, 15b, 16, 19）| Row 14 sequential idempotent Stop（call 1 detach / call 2 no-op）。Row 15 完整 lifecycle（SessionStart attach → UserPromptSubmit upsert → PreToolUse upsert × 2 → Stop detach）。Row 15b 3 goroutine 同 turn upsert 並發 → exactly 1 ref 結果（forbidden states 三條斷言：≥2 refs / 0 refs / turnID != "t_a"）。Row 16 2 goroutine UserPromptSubmit(t_b) + Stop(t_a) 並發 → 兩 valid final state 之一（forbidden states 顯式列出）。Row 19 stale SourceStartTime（PID-reuse）→ ref kept + trace `proxy_subagent_stop_no_match`。 |

**Test**：

- TDD 順序：5 row 一輪寫 → fail → spec strict 斷言全綠才 PASS
- Row 15b / 16 必跑 race mode（`go test -race`）多次 stress（`-count=10`）
- Row 16 用 spec §5 表內 forbidden state 完整列：zero refs / 兩 ref / 單 ref turnID="t_a"

**Acceptance**：
- 5 row 全綠
- `go test -race -count=10 ./internal/module/agent/...` 不出 data race
- AC2 satisfied（spec §5 全 20 row 全綠，含 row 16 strict assertion）

**估計**：~120 行 test

**依賴**：T7a + T7b + T8a + T8b（全 case body 完）

---

## 4. Phase 4：文件 commit + 整合驗證

### P4-T10 — Plan freeze commit + spec drift check + 整合驗證

**目標**：本 plan freeze 版（v3）落 commit；最後跑 full test + lint + build；spec / plan / impl 三層一致性 check。

**改動**：

| 檔案 | 改動 |
|---|---|
| `docs/specs/2026-05-02-lights-l2-proxy-detach-plan.md` | 本檔 v3 commit |
| (verify only) | `go build ./...` / `go test ./...` / `go test -race ./internal/module/agent/...` / `cd spa && pnpm run lint && pnpm run build` 全綠 |
| (verify only) | spec AC5 / AC6 / AC8 grep —— 見下方 fenced code block（**M2/v3 fix —— 移出 table cell 走 raw pipe，避 markdown escape 踩雷**） |

**AC5 verify**（trace reason vocabulary，結果應只列 §7 vocabulary 無多餘 reason）：

```bash
rg '"(proxy_subagent_attached|proxy_subagent_detached|proxy_subagent_detached_on_stop|proxy_subagent_detached_on_stop_turn|proxy_subagent_stop_no_match|proxy_subagent_stop_parse_failed|proxy_subagent_upserted_on_user_prompt|proxy_subagent_attached_on_user_prompt|user_prompt_without_proxy_parent|pre_tool_without_proxy_parent)"' --type go
```

**AC6 verify**（per-file LOC cap + 總量 ≤850）：

```bash
git diff --numstat origin/main -- \
  internal/module/agent/frame_ops.go \
  internal/module/agent/raw_codex_event.go \
  internal/agent/codex/events.go \
  internal/module/agent/frame_ops_test.go \
  docs/specs/2026-05-02-lights-l2-proxy-detach-plan.md
git diff --shortstat origin/main
```

對 cap：`frame_ops.go` ≤ 280 / `raw_codex_event.go` ≤ 50 / `events.go` ≤ 10 / 新 test ≤ 400 / 總量 ≤ 850。

**AC8 verify**（catalog change cc/opencode 隔離）：

```bash
rg 'PdxPreToolUse|HookHandlingUnsupported|LifecycleNone' internal/agent/codex internal/agent/cc internal/agent/opencode
```

確認 cc/opencode 結果不變（與 baseline 對照）。

**Test**：N/A（純驗證）

**Acceptance**：
- 全部 verify 命令綠
- 三層一致性無 drift
- AC1-AC8 全條 satisfied

**估計**：~0 行 code（plan 已寫，commit only）

**依賴**：P3-T9（全部 impl 完）

---

## 5. PR 流程

PR 建立流程（per CLAUDE.md「完整開發流程」第 7-9 步 + spec §10）：

1. P1 (T1+T2+T3) Subagent A 跑 → 3 commit
2. P2 (T4+T5+T6) Subagent B 跑（**Phase 1 三 commit 全進 worktree branch 後啟動**避 subagent.go 寫衝突）→ 3 commit
3. P3 (T7a+T7b+T8a+T8b+T9) 主 session integration → 5 commit
4. P4 (T10) plan commit + 全綠 verify → 1 commit
5. `gh pr create` — title `[L2] Codex broker turn-aware proxy detach on Stop`；body 含 §summary（spec §1 + 5 輪 review 收斂） / §test plan（spec §5 全 20 row + spec §9 mlab 7 場景）/ §spec / §plan
6. **Round 1 standard codex review**：`/codex:review --base origin/main --background`，focus per spec §10：identity model correctness / in-place upsert race-safety / fallback cross-provider / catalog migration safety
7. 收斂 round 1 finding（per `feedback_codex_pr_review_spec_alignment` —— **必對照 spec 採納**，spec 5 輪收斂結果不可被 round 1 倒退）
8. **Round 2 三平行 adversarial codex review**（per CLAUDE.md PR Review 兩輪制 + spec §10）：
   - 攻擊方：race（pre-grace 與 row 15b/16 boundary）/ PID-reuse / catalog-consumer break / 跨 provider isolation 漏 / parser bypass
   - 防守方：spec §3.5 4 條 known limitation 是否被 review 提議倒退（如建議改 wildcard detach 即倒退 v3 H1 race，必反對）/ resumeThread coverage / strategy a+b boundary / cc opencode 不變 regression
   - 檔案體質：`frame_ops.go` 是否撐爆（spec §4 表估 +200-280 LOC，加上既有 ~1500 → 約 1800，應提 follow-up issue 切分）/ `raw_codex_event.go` 為 new file SRP / `frame_ops_test.go` 切分合理性
9. 收斂 round 2 finding 直到 0 critical/P1（per `feedback_codex_review_termination`）；medium 屬 known issue 化追蹤入 `gh issue`
10. squash merge → main
11. 起 bump PR alpha.282（**注意：W6-6 並行 session 也準備 alpha.282；若 W6-6 先 ship，本 PR 改用下個編號**）（`bump-alpha-XXX` 獨立 worktree；VERSION + package.json + spa/package.json + CHANGELOG.md 同步；CHANGELOG 加 L2 entry）

---

## 6. Test plan summary

### Unit test（隔離測試）

| Test | 來源 task | row count |
|---|---|---|
| `TestDeriveCodexStatus_PdxPreToolUse` | P1-T1 | 1 |
| `TestSubagentRef_JSONRoundTrip_SourceTurnID` | P1-T2 | 2 |
| `TestParseCodexTurnID` | P1-T3 | 5 |
| `TestSubagentRefMatches_TurnAware` | P2-T4 | 7（含 native 2 case，L2/v2 fix） |
| `TestFindProxyRefByBroker` | P2-T4 | 3 |
| `TestUpsertProxyRefForBroker` | P2-T5 | 4 |
| `TestRemoveProxyRefForSenderTurn_PaneScan` | P2-T6 | 5 |
| `TestDetachProxyRefForSenderTurnWithRetry` | P2-T6 | 1 |
| `TestApplyFrameEvent_TurnAwareProxyDetach` (spec §5 全表) | P3-T7a+T7b+T8a+T8b+T9 | 20 (row 1-20) |

**新增 test 小計**：50 cases（其中 spec §5 全部 labeled rows 22 個（rows 1-20 加 b-row extension 15b + 17b）+ 28 helper-level case）

### Regression（既有 test zero regression）

- `internal/agent/codex/events_test.go` —— `PdxPreToolUse` fixture 改（P1-T1）
- 其他 `internal/agent/codex/` test —— 全綠不變
- `internal/module/agent/` 既有 `applyFrameEvent` test —— cc/opencode path 全綠不變
- SessionEnd path（`frame_ops.go:79-127`）—— 既有 cc/opencode wildcard detach test 全綠不變

### Live verify（per spec §9）

mlab post-merge daemon update 後 7 場景：
1. Single dispatch with prompt（trace: attached → upserted_on_user_prompt → detached_on_stop_turn）
2. Sequential dispatches（t_1 → t_2 transition, dot 不滅）
3. Resume-thread same broker（無 SessionStart trace）
4. Rapid-fire 3 dispatch（final state 無 zero/dup ref）
5. Tool-only turn（review/compact）—— PreToolUse upsert 路徑驗證
6. Empty-tool turn（rare）—— `proxy_subagent_stop_parse_failed` trace + 燈滯後驗證 §3.5 L1
7. Concurrent dispatch on different brokers —— multi-broker isolation

### Test 總計

- ~48 新 unit test + ~0 既有 test 修正（除 events_test fixture 1 處）+ 7 mlab 場景

---

## 7. Known issues / risks（H2/v2 對齊 spec §7）

### 已知接受限制（spec §3.5 已寫，non-blocking for ship）

1. **L1 empty-tool turn stale-light**：極少見（無 UserMessage + 所有 tool `pre_tool_use_payload=None`）；Stop 撞 turnID mismatch → skip detach；燈滯到下個 upsert / 治理 P2/P3 cleanup
2. **L2 out-of-order attach**：dispatch 2 先到 dispatch 1 Stop → ref 直接過渡到 t_2，dispatch 1 完成的明確 trace 信號丟失（intentional，UI 體驗無 flicker）
3. **L3 cc/opencode 未來若加 long-lived broker**：必先補 dispatch identity（forward constraint，spec §3.5 L3）
4. **L4 codex schema 演化**：`parseCodexTurnID` fail-soft 到 ""，最壞 case 退化到 v1 行為的 §3.3.D conservative（非 wildcard detach，per F3 fix）

### 風險與對策（H2/v2 fix —— 對齊 spec §7 一對一搬入 + 額外 plan-side 風險）

| Risk | Source | Mitigation |
|---|---|---|
| **Codex schema drops/renames `turn_id`** | spec §7 | `parseCodexTurnID` fail-soft；§3.3.D table 4 parse outcome 全 cover；followup phase if upstream changes |
| **`findProxyRefByBroker` and `subagentRefMatches` semantically diverge over time** | spec §7 | Inline cross-reference comments（spec §3.2 + §4 (g)）；PR review checklist 確認兩 helper 並存不被 merge |
| **In-place upsert in `upsertProxyRefForBroker` introduces new retry path** | spec §7 | Reuses existing `UpsertIfUnchanged` etag mechanism；row 16 explicitly tests concurrency |
| **Catalog change for `PdxPreToolUse` (HookHandlingUnsupported → omitted Handling) breaks existing code paths** | spec §7 | grep `HookHandlingUnsupported` and `LifecycleNone` consumers in `internal/agent/codex/`；AC8 grep 強制 |
| **Empty-tool turns leave stale-light** | spec §7 / §3.5 L1 | 已知 limitation；followup metric: count `proxy_subagent_stop_parse_failed` and `proxy_subagent_stop_no_match` traces |
| **Trace reason vocabulary expansion** | spec §7 | AC5 enforces `rg` checklist；plan §6 P4-T10 跑 grep |
| **`req.RawEvent` parser bypass attack** | spec §7 | reuses existing 64KB cap in `path_hint_extractor.go:15`；新 parser shares same Unmarshal path |
| **Provider drift between cc/opencode/codex paths in same code** | spec §7 | Codex-specific logic isolated in `raw_codex_event.go` and gated by `req.AgentType == "codex"`；cc/opencode paths 走既有 wildcard helpers |
| **Hook propagation latency unclear** | spec §7 | §9 verification uses **trace timestamps**（daemon-side）rather than wall-clock guesses |
| **codex round 2 adversarial 提議倒退 spec §3.5 known limitation** | plan-added | spec 5 輪收斂結果為基線；倒退提議必須有新證據才採納（per `feedback_codex_pr_review_spec_alignment`）|
| **`frame_ops.go` 撐爆過大（既有 ~1500 行 + 280 → ~1800）** | plan-added | round 2 file-health 視角必驗；若被 flag 則開 follow-up issue 切分 helper 到獨立 file（不在本 PR 兌現）|
| **Row 15b / 16 race-mode flake** | plan-added | `-count=10` stress 跑；spec §5.1 pattern 仿 `agent_event_test.go:198`；用 store fake mutex 而非 production etag race |
| **out-of-order attach (spec §3.5 L2) 導致使用者 confusion** | plan-added | trace `proxy_subagent_stop_no_match` 在 daemon log 可看；spec §9 verification 場景 4 含驗證 |

### Rollback 計畫

若 ship 後發現 regression：
1. revert merge commit
2. 起 hotfix bump
3. issue 追蹤 root cause
4. 重新 plan 修法 → 重新 PR

---

## 8. Bump PR

Phase 4 PR squash merge 後獨立 bump PR：
- VERSION: alpha.281 → alpha.282（**或 W6-6 ship 後改下個編號**）
- spa/package.json + package.json 同步
- CHANGELOG.md 加：
  ```
  ### lights
  - **L2**: Codex broker turn-aware proxy detach on Stop
    - long-lived codex broker 完成 logical turn 時，detach 父 frame 的 proxy 燈號
    - turn-aware identity（codex `turn_id`），cc/opencode 維持 process-level fallback
    - 三層 attach trigger（SessionStart / UserPromptSubmit / PreToolUse）+ Stop targeted detach
    - empty-tool turn 已知 limitation（spec §3.5 L1，等下個 upsert / governance P2/P3 cleanup）
  ```
- bump PR 用獨立 worktree：`bump-alpha-XXX`
- L2 ship 後下個 phase 候選：L1 OpenCode subagent idle filter / 治理 P2+P3（per kickoff）

---

## 9. 完成檢核總表

| Phase | 檢核 | 狀態 |
|---|---|---|
| Phase 1 P1-T1~T3 | 3 task 全 commit + Subagent A 全綠 + race 全綠 | ⏳ |
| Phase 2 P2-T4~T6 | 3 task 全 commit + Subagent B 全綠 + race 全綠 | ⏳ |
| Phase 3 P3-T7a/T7b/T8a/T8b/T9 | 5 task 全 commit + spec §5 全 20 row 全綠 + race -count=10 全綠 | ⏳ |
| Phase 4 P4-T10 | plan v3 freeze commit + AC1-AC8 全 satisfied + grep 結果乾淨 | ⏳ |
| PR + codex review 兩輪 | 0 critical/P1 + known issue 追蹤化 | ⏳ |
| Squash merge → main | branch 刪除 + worktree 清理 | ⏳ |
| Bump PR alpha.282 (or next) | merge | ⏳ |
| mlab live verify §9 7 場景 | post-merge 跑（ship gate 為場景 1+2，其餘 follow-up 觀察） | ⏳ |

---

## 10. Subagent 派發摘要（重要）

每個 subagent **每個 Bash 都帶 `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/lights-l2-proxy-detach && ` 前綴**（per `feedback_subagent_cwd_enforcement`），否則 commit 落主 repo。

**Subagent A（Phase 1，可獨立）**：
- T1：`PdxPreToolUse` catalog 改 + `deriveCodexStatus` 新 case + fixture 修
- T2：`SubagentRef.SourceTurnID` field + JSON round-trip test
- T3：`parseCodexTurnID` 純函式 + 5-case test
- 完成 criteria：3 commit + `go test ./internal/agent/...` 綠 + `go build ./...` 綠

**Subagent B（Phase 2，Phase 1 三 commit 全進 worktree branch 後啟動）**：
- T4：`subagentRefMatches` turn-aware + `findProxyRefByBroker` 新增（含 native 2 case，L2/v2 fix）
- T5：`upsertProxyRefForBroker` + retry helper
- T6：pane-scan `removeProxyRefForSenderTurn` + `detachProxyRefForSenderTurnWithRetry`（B1/v2 fix —— signature 對齊 `removeProxyRefForSender(paneID, ...)`）
- 完成 criteria：3 commit + `go test -race ./internal/module/agent/...` 綠 + `go build ./...` 綠

**主 session（Phase 3+4）**：
- T7a：`LifecycleUserPromptSubmit` case body + 5 row test (1, 5, 7, 17, 17b)
- T7b：PreToolUse wiring 進同 case body + no-parent guard + 2 row test (2, 20)
- T8a：`LifecycleStop, LifecycleStopFailure` case body + codex 三 sub-case + 6 row test (3, 4, 6, 8, 9, 10)
- T8b：Stop case regression / fallback 4 row test (11, 12, 13, 18)
- T9：advanced / concurrency / PID-reuse 5 row test (14, 15, 15b, 16, 19)
- T10：plan commit + 全綠 verify + AC1-AC8 check
- 完成 criteria：spec §5 全 20 row + helper test 全綠 + race -count=10 + lint + build 全綠
