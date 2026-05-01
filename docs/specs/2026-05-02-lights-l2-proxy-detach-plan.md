# L2 Proxy detach on Stop — Implementation Plan

> **Status**：v1（first draft，待 codex 1 輪 plan review）
> **依賴 spec**：`docs/specs/2026-05-01-lights-l2-proxy-detach-on-stop-spec.md` v5 final
> **Worktree**：`.claude/worktrees/lights-l2-proxy-detach` / branch `worktree-lights-l2-proxy-detach`
> **Base**：`origin/main` @ alpha.281（J3 `56b3ba55` + bump `5736f87e`）
> **拆分**：Phase 1（T1+T2+T3 純函式層 — Subagent A）→ Phase 2（T4+T5+T6 helper 層 — Subagent B 與 A 並行）→ Phase 3（T7+T8+T9 integration — 主 session）→ Phase 4（T10 doc + 整合驗證）→ PR

---

## 0. Plan 總覽

### 0.1 範圍重申（per spec §1.1 / §3）

L2 codex broker turn-aware proxy detach — long-lived broker 完成 logical dispatch（turn）時，detach 父 frame 的 proxy `SubagentRef` 讓燈滅，無需等 broker process 死。

**核心 in-scope**（spec §3 / §4）：
- #1：`SubagentRef.SourceTurnID` 欄位（`omitempty`，無 DB migration）
- #2：兩個獨立 lookup helper（`subagentRefMatches` turn-aware / `findProxyRefByBroker` process-level，註解互引避 DRY refactor）
- #3：codex 解析 `parseCodexTurnID`（fail-soft）
- #4：`upsertProxyRefForBroker` 新 helper（不 reuse `mutateSubagentsWithRetry`，避 v3 F1 race）
- #5：`removeProxyRefForSenderTurn` + `detachProxyRefForSenderTurnWithRetry` mirror 既有 helper pattern
- #6：`PdxPreToolUse` catalog change（`LifecycleNone` → `LifecycleUserPromptSubmit`，`Handling` 欄位 omitted → `EffectiveHookHandling` default `HookHandlingDetail`）+ `deriveCodexStatus` 新 case 回 `Valid: true`
- #7：`applyFrameEvent` 兩新 lifecycle case
  - `LifecycleUserPromptSubmit`：codex gate 後 attach/upsert（含 PreToolUse no-parent skip guard，per spec §3.3.C.1）
  - `LifecycleStop, LifecycleStopFailure`：三 sub-case dispatch（per spec §3.3.D table）
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
- 預估時間：6-9 小時 subagent TDD（Phase 1+2 並行 ~3hr / Phase 3 integration ~3hr / Phase 4 整合 ~1hr）+ 兩輪 codex review 2-3hr

### 0.3 鎖序與不變式（per spec §3.4）

實作時必持守：
- `mutateSubagentsWithRetry` etag-based optimistic concurrency 機制不改
- 新 `upsertProxyRefForBroker` reuse `UpsertIfUnchanged(frame, expected)` retry pattern（max `proxyUpsertMaxAttempts=3`）
- `findProxyRefByBroker` 與 `subagentRefMatches` 兩 helper 各自負責不同 intent，互不 reuse（註解明寫）
- `parseCodexTurnID` 為純函式，fail-soft（JSON parse error / 缺 field → ""）
- 新 lifecycle case 第一行 `if req.AgentType != "codex" { break }` —— cc/opencode 走 fall-through 既有 path 不變
- PreToolUse no-parent path **skip return early**，**不 fall through generic frame-creation**（spec §3.3.C.1 + §5 row 20）

### 0.4 Spec 收斂歷程備註

Spec v1 → v5 共 5 輪 codex review 收斂（核心 race → 設計缺陷 → impl typo → wording）。**不再審 spec**，剩餘風險靠 PR review 兜底（spec §10 兩輪 codex review）。

---

## 1. Phase 1：純函式層（Subagent A）

可獨立完成，與 Phase 2 並行。Subagent A 拿全部 T1+T2+T3，每 task 獨立 commit。

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
- Reuse `path_hint_extractor.go:15` 64KB cap 模式（spec §7 risk row 7）—— 但 `json.Unmarshal` 自身已 bounded，本 case 不需新 cap
- 5 case 全綠

**估計**：~25 行 production / ~30 行 test

**依賴**：none（與 T1 / T2 並行）

---

## 2. Phase 2：Helper 層（Subagent B，可與 Phase 1 並行）

依賴 P1-T2 的 `SubagentRef.SourceTurnID` field 才能 compile，但 Phase 1 / 2 可同時開兩 worktree branch 寫，T2 早完成 → T4 可開工。為簡化派發，**Subagent B 在 Phase 1 全 done 後再啟動**（避免兩 subagent 改 subagent.go 衝突）。

主 session 在 Phase 1 全 commit 後，把 worktree HEAD push 給 Subagent B，再 Subagent B 派 T4-T6。

### P2-T4 — `subagentRefMatches` turn-aware 升級 + `findProxyRefByBroker` 新增

**目標**：`subagentRefMatches`（`frame_ops.go:631-639`）升級為 turn-aware（per spec §3.2.A）；新增 `findProxyRefByBroker`（per spec §3.2.B）作 process-level lookup helper。兩 helper 註解互引以防後人 DRY refactor 重蹈 v3 F1 race。

**改動**：

| 檔案 | 改動 |
|---|---|
| `internal/module/agent/frame_ops.go:631-639` | 改 `subagentRefMatches` 對 `IsProxy=true` ref：兩端 `SourceTurnID` 任一為 "" → 回 process-level（PID + StartTime）；兩端非空 → 比 `SourceTurnID`。註解：`// turn-aware equality; 用於 Stop targeted detach。process-level lookup 走 findProxyRefByBroker（不 reuse 此 helper，避免 turn_id mismatch 被當 no-match → 重複 append duplicate ref，per spec §3.2 F1 fix）`。 |
| `internal/module/agent/frame_ops.go`（新增 helper）| `findProxyRefByBroker(refs []SubagentRef, pid int, startTime string) int` —— 純 process-level lookup，回 index 或 -1。註解：`// process-level lookup; 用於 attach/upsert（in-place 改 SourceTurnID）。turn-aware equality 走 subagentRefMatches（不 reuse 此 helper，避 Stop targeted detach 撞上 stale-turn ref，per spec §3.2 F1 fix）`。 |
| `internal/module/agent/frame_ops_test.go` | `TestSubagentRefMatches_TurnAware`：5 case—（a）兩端 turn_id 都 ""（process fallback）→ true；（b）一端 ""，另端非空 → true（fallback）；（c）兩端非空且相等 → true；（d）兩端非空但不等 → false；（e）IsProxy 一邊 true 一邊 false → false。`TestFindProxyRefByBroker`：3 case—（a）match 第一個 → 回 0；（b）match 第二個 → 回 1；（c）no match → 回 -1。 |

**Test**：

- TDD 順序：先寫兩個 test → fail → 改 helper → 綠
- 純函式測試，無 store mock

**Acceptance**：
- 兩 helper 註解明寫互不 reuse 的理由（spec §3.2 cross-reference 防 DRY refactor）
- 既有調用 `subagentRefMatches` 的 path 全部編譯通過（grep 確認）

**估計**：~40 行 production / ~50 行 test

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

### P2-T6 — `removeProxyRefForSenderTurn` + `detachProxyRefForSenderTurnWithRetry`

**目標**：mirror 既有 `removeProxyRefForSender`（`frame_ops.go:798`）+ `detachProxyRefWithRetry`（`frame_ops.go:887-929`）pattern，但 detach 條件改成「ALL three 身分欄位 (PID + StartTime + TurnID) 都 match」才 remove。

**改動**：

| 檔案 | 改動 |
|---|---|
| `internal/module/agent/frame_ops.go`（新增）| `removeProxyRefForSenderTurn(refs []SubagentRef, pid int, startTime, turnID string) ([]SubagentRef, bool)` —— filter 出 non-match；只當 ref `IsProxy && SourcePID == pid && SourceStartTime == startTime && SourceTurnID == turnID` 三全 match 才 drop。回新 slice + removed bool。 |
| `internal/module/agent/frame_ops.go`（新增）| `detachProxyRefForSenderTurnWithRetry(...)` —— wrap 上面 helper 的 retry loop，仿 `detachProxyRefWithRetry:887-929` 結構（reload + filter + UpsertIfUnchanged + retry up to 3）。 |
| `internal/module/agent/frame_ops_test.go` | `TestRemoveProxyRefForSenderTurn`：4 case—（a）三身分全 match → drop，retain 其他；（b）PID match 但 turn_id 不 match → keep；（c）turn_id match 但 PID 不 match → keep；（d）IsProxy=false ref → 永不 drop（即使三 ID 字串巧合 match）。`TestDetachProxyRefForSenderTurnWithRetry`：1 case retry 路徑（mock conflict-then-success）。 |

**Test**：

- TDD 順序：先寫 `TestRemoveProxyRefForSenderTurn` 4 case → fail → 寫 pure helper → 綠 → 加 retry test → fail → 加 retry wrapper → 綠
- Race-mode test 包含

**Acceptance**：
- 不複用 `removeProxyRefForSender`（process-level）—— 兩函式並存（per spec §3.2 boundary）
- Retry 邏輯與既有 `detachProxyRefWithRetry` 行為一致（max 3, etag-based）
- `go test -race` 全綠

**估計**：~50 行 production / ~70 行 test

**依賴**：P2-T4（`subagentRefMatches` turn-aware 完成；雖此 helper 不直接用，但 race semantics 對齊）

---

## 3. Phase 3：Integration（主 session）

T7 / T8 / T9 涉及 `applyFrameEvent` switch case 新增 + 行為串接，cross-cutting 高、需 spec / fast-path / generic path 全圖在腦中，**主 session 自己做不派 subagent**。可派第三 subagent 但承擔協調成本。

### P3-T7 — `LifecycleUserPromptSubmit` case 新增（含 PreToolUse no-parent guard）

**目標**：在 `applyFrameEvent`（`frame_ops.go:61`）lifecycle switch 新增 `case agentpkg.LifecycleUserPromptSubmit:` —— 處理 codex `PdxUserPromptSubmit` + `PdxPreToolUse`（共用 case body）。codex AgentType gate + frame == nil + proxy parent + upsert。**PreToolUse no-parent 必 skip return early，不 fall through generic path**（spec §3.3.C.1 + §5 row 20）。

**改動**：

| 檔案 | 改動 |
|---|---|
| `internal/module/agent/frame_ops.go`（switch case 新增）| 第一行 `if req.AgentType != "codex" { break }`（cc/opencode fall through 到既有 generic path 不變，per spec §3.3.B + §5 row 17/17b）。第二行 `if frame != nil { break }`（既有 narrow `UpdateHookPath:440` 處理）。第三行 `parent := findProxyParent(req)`，若 `nil` AND req.Hook == "PdxPreToolUse" → trace `Decision: "skipped", Reason: "pre_tool_without_proxy_parent"` **return early**（per spec §3.3.C.1 + §5 row 20，不 fall through `frame_ops.go:251` onward 的 generic frame 創建）。第四行 `parent == nil` 且 hook 非 PreToolUse（即 UserPromptSubmit）→ break 讓既有 generic path 處理（保持 v4 行為）。第五行 `turnID := parseCodexTurnID(req.RawEvent)`（fail-soft → ""）。第六行 `upsertProxyRefForBroker(parent, req.SourcePID, req.SourceStartTime, turnID, req.BroadcastTs)`，trace `Reason: "proxy_subagent_attached_on_user_prompt"`（首次 attach）or `"proxy_subagent_upserted_on_user_prompt"`（in-place upsert）。 |
| `internal/module/agent/frame_ops_test.go`（test 新增 row 1, 2, 5, 6, 7, 17, 17b, 20）| 8 row 從 spec §5 表抓 setup + action + expected + validates 寫 table-driven。每 row 用 store mock + helper assert frame.Subagents 結果 + trace reason 結果。 |

**Test**：

- TDD 順序：8 row 一輪寫 → 跑 fail → 補 case body 路徑 → 綠
- Row 17（opencode break early）+ row 17b（cc break early）必驗 cc/opencode 走原有路徑不變（regression guard）
- Row 20（PreToolUse no-parent）必驗 frame 沒被創、`Subagents` 沒 mutate、無 broadcast

**Acceptance**：
- 8 row 全綠
- cc/opencode 既有 frame_ops 行為 zero regression（既有 cc/opencode 相關 test 也跑）
- `go test -race ./internal/module/agent/...` 全綠
- AC1 satisfied：無新 exported types，5 helper 都 unexported（per spec AC1）

**估計**：~80 行 production / ~150 行 test

**依賴**：P1-T1, P1-T2, P1-T3, P2-T4, P2-T5（全部 Phase 1+2 完）

---

### P3-T8 — `LifecycleStop, LifecycleStopFailure` case 新增（三 sub-case dispatch）

**目標**：在 `applyFrameEvent` switch 新增 `case agentpkg.LifecycleStop, agentpkg.LifecycleStopFailure:` —— L2 核心 detach 邏輯，三 sub-case dispatch（per spec §3.3.D table）。

**改動**：

| 檔案 | 改動 |
|---|---|
| `internal/module/agent/frame_ops.go`（switch case 新增）| 第一行 `if frame != nil { break }`（standalone agent，J3 dispatcher 處理，per spec §3.3.D）。第二行 `parent := findProxyParent(req)`，若 nil break（無 proxy parent，無 detach 對象）。第三行 `if req.AgentType != "codex" { wildcard detach via removeProxyRefForSender; trace "proxy_subagent_detached_on_stop"; break }`。第四行 `turnID := parseCodexTurnID(req.RawEvent)`。第五行（codex 三 sub-case）：(a) `turnID != ""` → `detachProxyRefForSenderTurnWithRetry(...)`，trace `proxy_subagent_detached_on_stop_turn` or `proxy_subagent_stop_no_match`（找不到 match）；(b) `turnID == ""` AND parent 內 ref 有 `SourceTurnID != ""` → **skip**, trace `proxy_subagent_stop_parse_failed`；(c) `turnID == ""` AND parent 內 ref `SourceTurnID == ""` → wildcard `removeProxyRefForSender`, trace `proxy_subagent_detached_on_stop`。 |
| `internal/module/agent/frame_ops_test.go`（test 新增 row 3, 4, 6, 8, 9, 10, 11, 12, 13）| 9 row 表驅動。Row 11 用 `parent opencode + 1 ref(PID=42, t1, turnID="", Type=cc)` 跨 type proxy ref（spec §5 row 11 description）。Row 13 native ref（IsProxy=false）isolation guard。 |

**Test**：

- TDD 順序：9 row 一輪寫 → fail → 補 sub-case dispatch → 綠
- Row 11（opencode SessionEnd 既有路徑 unchanged）regression guard 是必跑
- Row 12（standalone Stop, frame != nil）必驗 frame.Subagents zero touch
- Row 13（native ref）必驗 IsProxy gate 工作

**Acceptance**：
- 9 row 全綠
- 三 sub-case trace reason 與 spec §3.3.D table 完全對應
- AC8 grep 驗：cc/opencode 內 `PdxPreToolUse` reference（若有）不變
- `go test -race ./internal/module/agent/...` 全綠

**估計**：~80 行 production / ~150 行 test

**依賴**：P3-T7（共用 case body 結構）+ P2-T6（detach helper 必需）

---

### P3-T9 — Concurrent test 補（row 14, 15, 15b, 19）

**目標**：補 4 個進階場景 test —— row 14 idempotency / row 15 sequential 全流程 / row 15b 同 turn 並發 upsert race-safety / row 19 PID-reuse safety。Row 15b / 16 用 `sync.WaitGroup` + 2 goroutine pattern（per spec §5.1 + 仿 `internal/store/agent_event_test.go:198`）。

**改動**：

| 檔案 | 改動 |
|---|---|
| `internal/module/agent/frame_ops_test.go`（test 新增 row 14, 15, 15b, 16, 18, 19）| Row 14 sequential idempotent Stop。Row 15 完整 lifecycle（SessionStart attach → UserPromptSubmit upsert → PreToolUse upsert × 2 → Stop detach）。Row 15b 3 goroutine 同 turn upsert 並發 → exactly 1 ref 結果（forbidden states 三條斷言：≥2 refs / 0 refs / turnID != "t_a"）。Row 16 2 goroutine UserPromptSubmit(t_b) + Stop(t_a) 並發 → 兩 valid final state 之一（forbidden states 顯式列出）。Row 18 cc Stop fallback。Row 19 stale SourceStartTime（PID-reuse）→ ref kept + trace `proxy_subagent_stop_no_match`。 |

**Test**：

- TDD 順序：6 row 一輪寫 → fail → spec strict 斷言全綠才 PASS
- Row 15b / 16 必跑 race mode（`go test -race`）多次 stress（建議 `-count=10`）
- Row 16 用 spec §5 表內 forbidden state 完整列：zero refs / 兩 ref / 單 ref turnID="t_a"

**Acceptance**：
- 6 row 全綠
- `go test -race -count=10 ./internal/module/agent/...` 不出 data race
- AC2 satisfied（spec §5 全 20 row 全綠，含 row 16 strict assertion）

**估計**：~120 行 test

**依賴**：P3-T7 + P3-T8（兩 case body 都完）

---

## 4. Phase 4：文件 commit + 整合驗證

### P4-T10 — Plan v1 commit + spec drift check + 整合驗證

**目標**：本 plan 落 commit；最後跑 full test + lint + build；spec / plan / impl 三層一致性 check。

**改動**：

| 檔案 | 改動 |
|---|---|
| `docs/specs/2026-05-02-lights-l2-proxy-detach-plan.md` | 本檔 commit |
| (verify only) | `go build ./...` / `go test ./...` / `go test -race ./internal/module/agent/...` / `cd spa && pnpm run lint && pnpm run build` 全綠 |
| (verify only) | spec AC5 grep：`rg '"(proxy_subagent_attached\|proxy_subagent_detached\|proxy_subagent_detached_on_stop\|proxy_subagent_detached_on_stop_turn\|proxy_subagent_stop_no_match\|proxy_subagent_stop_parse_failed\|proxy_subagent_upserted_on_user_prompt\|proxy_subagent_attached_on_user_prompt\|user_prompt_without_proxy_parent\|pre_tool_without_proxy_parent)"' --type go`（注意：shell 內 `\|` 改 `|`）—— 結果應只列 §7 vocabulary，無多餘 reason 字串 |
| (verify only) | spec AC6 LOC bound：`git diff origin/main --stat` 確認 `frame_ops.go` ≤ 280 / `raw_codex_event.go` ≤ 50 / `events.go` ≤ 10 / 新 test ≤ 400 / 總 PR diff ≤ 850 |
| (verify only) | spec AC8 grep：`rg 'PdxPreToolUse|HookHandlingUnsupported|LifecycleNone' internal/agent/codex internal/agent/cc internal/agent/opencode` —— 確認 cc/opencode 不變 |

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
2. P2 (T4+T5+T6) Subagent B 跑（Phase 1 後啟動避衝突）→ 3 commit
3. P3 (T7+T8+T9) 主 session integration → 3 commit
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
11. 起 bump PR alpha.282（`bump-alpha-282` 獨立 worktree；VERSION + package.json + spa/package.json + CHANGELOG.md 同步；CHANGELOG 加 L2 entry）

---

## 6. Test plan summary

### Unit test（隔離測試）

| Test | 來源 task | row count |
|---|---|---|
| `TestDeriveCodexStatus_PdxPreToolUse` | P1-T1 | 1 |
| `TestSubagentRef_JSONRoundTrip_SourceTurnID` | P1-T2 | 2 |
| `TestParseCodexTurnID` | P1-T3 | 5 |
| `TestSubagentRefMatches_TurnAware` | P2-T4 | 5 |
| `TestFindProxyRefByBroker` | P2-T4 | 3 |
| `TestUpsertProxyRefForBroker` | P2-T5 | 4 |
| `TestRemoveProxyRefForSenderTurn` | P2-T6 | 4 |
| `TestDetachProxyRefForSenderTurnWithRetry` | P2-T6 | 1 |
| `TestApplyFrameEvent_TurnAwareProxyDetach` (spec §5 全表) | P3-T7+T8+T9 | 20 (row 1-20) |

**新增 test 小計**：45 cases（其中 spec §5 full table 20 row + 25 helper-level case）

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

- ~45 新 unit test + ~0 既有 test 修正（除 events_test fixture 1 處）+ 7 mlab 場景

---

## 7. Known issues / risks

### 已知接受限制（spec §3.5 已寫，non-blocking for ship）

1. **L1 empty-tool turn stale-light**：極少見（無 UserMessage + 所有 tool `pre_tool_use_payload=None`）；Stop 撞 turnID mismatch → skip detach；燈滯到下個 upsert / 治理 P2/P3 cleanup
2. **L2 out-of-order attach**：dispatch 2 先到 dispatch 1 Stop → ref 直接過渡到 t_2，dispatch 1 完成的明確 trace 信號丟失（intentional，UI 體驗無 flicker）
3. **L3 cc/opencode 未來若加 long-lived broker**：必先補 dispatch identity（forward constraint，spec §3.5 L3）
4. **L4 codex schema 演化**：`parseCodexTurnID` fail-soft 到 ""，最壞 case 退化到 v1 行為的 §3.3.D conservative（非 wildcard detach，per F3 fix）

### 風險與對策

| 風險 | 機率 | 影響 | 對策 |
|---|---|---|---|
| codex round 2 adversarial 提議倒退 spec §3.5 L1 / L4 已知 limitation | medium | low | spec 5 輪收斂結果為基線；倒退提議必須有新證據才採納，spec drift 防線（per `feedback_codex_pr_review_spec_alignment`）|
| `frame_ops.go` 撐爆過大（既有 ~1500 行 + 280 → ~1800）| medium | low | round 2 file-health 視角必驗；若被 flag 則開 follow-up issue 切分 helper 到獨立 file（不在本 PR 兌現）|
| `findProxyRefByBroker` 與 `subagentRefMatches` 後人 DRY refactor 重蹈 v3 F1 race | low | high | 兩 helper 註解互引（spec §3.2 cross-reference）；PR review checklist 確認兩 helper 並存；test row 5 strict assertion（同 turn upsert 不 dup）|
| `PdxPreToolUse` catalog change 破壞既有 codex consumer | low | medium | spec AC8 grep + P1-T1 既有 fixture 修；cc/opencode 隔離（不動其 events.go）|
| Row 15b / 16 race-mode flake | medium | medium | `-count=10` stress 跑；spec §5.1 pattern 仿 `agent_event_test.go:198`；用 store fake mutex 而非 production etag race |
| `req.RawEvent` parser bypass attack | low | low | reuse `path_hint_extractor.go:15` 64KB cap；`parseCodexTurnID` 純 unmarshal anonymous struct，bounded |
| out-of-order attach（spec §3.5 L2）導致使用者 confusion | low | low | trace `proxy_subagent_stop_no_match` 在 daemon log 可看；spec §9 verification 場景 4 含驗證 |

### Rollback 計畫

若 ship 後發現 regression：
1. revert merge commit
2. 起 hotfix bump
3. issue 追蹤 root cause
4. 重新 plan 修法 → 重新 PR

---

## 8. Bump PR

Phase 4 PR squash merge 後獨立 bump PR：
- VERSION: alpha.281 → alpha.282
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
- bump PR 用獨立 worktree：`bump-alpha-282`
- L2 ship 後下個 phase 候選：L1 OpenCode subagent idle filter / 治理 P2+P3（per kickoff）

---

## 9. 完成檢核總表

| Phase | 檢核 | 狀態 |
|---|---|---|
| Phase 1 P1-T1~T3 | 3 task 全 commit + Subagent A 全綠 + race 全綠 | ⏳ |
| Phase 2 P2-T4~T6 | 3 task 全 commit + Subagent B 全綠 + race 全綠 | ⏳ |
| Phase 3 P3-T7~T9 | 3 task 全 commit + spec §5 全 20 row 全綠 + race -count=10 全綠 | ⏳ |
| Phase 4 P4-T10 | plan commit + AC1-AC8 全 satisfied + grep 結果乾淨 | ⏳ |
| PR + codex review 兩輪 | 0 critical/P1 + known issue 追蹤化 | ⏳ |
| Squash merge → main | branch 刪除 + worktree 清理 | ⏳ |
| Bump PR alpha.282 | merge | ⏳ |
| mlab live verify §9 7 場景 | post-merge 跑（ship gate 為場景 1+2，其餘 follow-up 觀察） | ⏳ |

---

## 10. Subagent 派發摘要（重要）

每個 subagent **每個 Bash 都帶 `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/lights-l2-proxy-detach && ` 前綴**（per `feedback_subagent_cwd_enforcement`），否則 commit 落主 repo。

**Subagent A（Phase 1，可獨立）**：
- T1：`PdxPreToolUse` catalog 改 + `deriveCodexStatus` 新 case + fixture 修
- T2：`SubagentRef.SourceTurnID` field + JSON round-trip test
- T3：`parseCodexTurnID` 純函式 + 5-case test
- 完成 criteria：3 commit + `go test ./internal/agent/...` 綠 + `go build ./...` 綠

**Subagent B（Phase 2，Phase 1 後啟動）**：
- T4：`subagentRefMatches` turn-aware + `findProxyRefByBroker` 新增
- T5：`upsertProxyRefForBroker` + retry helper
- T6：`removeProxyRefForSenderTurn` + `detachProxyRefForSenderTurnWithRetry`
- 完成 criteria：3 commit + `go test -race ./internal/module/agent/...` 綠 + `go build ./...` 綠

**主 session（Phase 3+4）**：
- T7：`LifecycleUserPromptSubmit` case + 8 row test
- T8：`LifecycleStop, LifecycleStopFailure` case + 9 row test
- T9：concurrent test row 14 / 15 / 15b / 16 / 18 / 19
- T10：plan commit + 全綠 verify + AC1-AC8 check
- 完成 criteria：spec §5 全 20 row + helper test 全綠 + race -count=10 + lint + build 全綠
