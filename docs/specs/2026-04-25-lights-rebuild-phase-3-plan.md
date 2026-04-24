# Phase 3 TDD Plan — L1 邊界補強

- **Date**: 2026-04-25
- **Spec**: `docs/specs/2026-04-23-lights-rebuild-spec.md` §7
- **Worktree**: `lights-phase-3`（branch `worktree-lights-phase-3`）
- **Baseline**: `1.0.0-alpha.221`（main @ `2633b88d`，Phase 2 PR-2b merged）
- **依賴**: Phase 2 PR-2b ✅（`SubagentRef` 結構 + `findProxyParent` PPID walk + `cachedDescendants` + `Prober.Identify` 已就緒）
- **範圍**：`applyFrameEvent` fallback chain 末端補回 + `no_parent_fallback` reason 顯式化
- **預估**：單 PR ~430 行 net（~80 prod + 250 test + 100 整合測試）

---

## 0. 設計依據

### 0.1 Codex 架構諮詢結論（採納）

委派 codex high-effort architectural consulting（job `af47a0fd51ca6667e`，2026-04-25）。**採納**項目：

| Codex 結論 | 本 plan 採納方式 |
|---|---|
| Q1：採 hook-triggered lazy rebuild，與 PR-2b `findProxyParent` 同型 | §1.2 主路徑 |
| Q2：類比 K8s controller reconciliation / Chrome DevTools target discovery「重連先 enumerate 再接 event」 | §0.2 思想框架 |
| Q4：stale 判 `pane + PID + startTime + descendants`（後半，descendants check） | §1.3 lazy rebuild 用 descendants 做 process 推斷 |
| Q5：`no_parent_fallback` trace 是未來 reparent / diagnostics / recovery quality 的資料邊界 | §1.4 reason 升格為一級 trace 標記，預留 Phase 4/5 reparent 入口 |

### 0.2 Codex 結論延後 / 不採納項目（含理由）

| Codex 結論 | 不採理由 |
|---|---|
| Q1：UI 引入 `unreconciled` 狀態 | SPA 工作；目前無 Inspector view 消費，YAGNI；frame.Subagents=[] 已天然代表「不確定」 |
| Q3：SPA on-demand inventory 第四方案 | Phase 5 Inspector 範圍；無 SPA query 端口 |
| Q4 前半：首 hook 順帶掃同 session 所有 pane inventory | 無消費端；inventory 概念需要新 frame 占位狀態，擴大 backend scope 而 SPA 端用不到 |
| `ListPanesForSession` tmux helper | 無消費端（hook 來了表示 pane 在運作，無需 daemon 驗證）；待 inventory feature 真做時再加 |

### 0.3 設計核心思想

採 **K8s reconciliation 對照組**：hook event = `watch event`、`cachedDescendants + Prober.Identify` = `live state read`、目前 fallback chain 失敗就 Upsert = `create-on-miss`。Phase 3 在「都未命中」與「Upsert 新 frame」之間插一條「先嘗試 process tree rebuild」的 reconciliation step，把 daemon downtime 期間遺失的 frame 識別補回。

**未補回 = no_parent_fallback**：明確標記，等 Phase 4/5 的 reparent loop / Inspector 消費。

---

## 1. 契約鎖定

### 1.1 `applyFrameEvent` 既有 fallback chain（PR-2b 後現況）

`internal/module/agent/frame_ops.go:36-310` 的查找順序：

1. **Line 45** — `GetByIdentity(paneID, senderPID, senderStartTime)` — 命中：直接走 existing-frame 路徑（line 239-272）
2. **Line 51-159** — Event-name 三分支：`SessionEnd` / `SubagentStart`+`Stop` / 其餘
3. **Line 168-207** — SessionStart 且 frame==nil：`findProxyParent` PPID walk → 命中掛 proxy / 未命中 fall through
4. **Line 209-228** — `readProcessInfoFn(senderPID)` → `FindByPanePID(paneID, info.PPID)` legacy parent lookup → 命中：parentFrameID=parent.FrameID / 未命中：parentFrameID=""
5. **Line 273-292** — frame==nil：`Upsert` 新 frame（subagents=[]）
6. **Line 294-309** — Trace meta：parentFrameID!="" → reason=`parent_frame_found`；否則 reason=`parent_frame_missing`

**Phase 3 插入點**：在 step 4 與 step 5 之間（line 228 之後、line 273 之前）插入 lazy rebuild。step 6 的 reason 同時升級。

### 1.2 Lazy rebuild — `tryRebuildFromProcessTree` 新 helper

**簽名**（位置：`internal/module/agent/frame_ops.go`，與 `findProxyParent` 同檔）：

```go
// tryRebuildFromProcessTree attempts to recover a frame after daemon restart
// by inspecting the pane's live process tree. Triggered when the standard
// lookup chain (GetByIdentity → findProxyParent → FindByPanePID) all miss
// on a SessionStart, indicating either a fresh start or a daemon-downtime
// recovery scenario.
//
// Behavior:
//   - Walk pane PID tree via cachedDescendants(panePID) (250ms cache)
//   - For each descendant PID, call provider.Identify per registered agent type
//   - First match wins (registry order)
//   - Match → return constructed Frame fields; caller persists via Upsert
//   - No match → return (nil, false, nil); caller falls through to no_parent_fallback
//   - Any error during process scan → return (nil, false, err); caller surfaces
//
// Subagent list is intentionally NOT rebuilt — left as []. Subsequent
// SubagentStart hooks will populate refs naturally (kept simple per plan §1.3).
func (m *Module) tryRebuildFromProcessTree(req EventRequest, info ProcessInfo) (rebuilt *RebuildResult, ok bool, err error)

type RebuildResult struct {
    AgentType string  // detected agent family
    PID       int     // matched descendant PID (= req.SenderPID for happy path)
    PPID      int     // info.PPID (kept consistent with caller)
}
```

**呼叫點**（line 228 之後插入）：

```go
// pseudo-code
if frame == nil && parentFrameID == "" {
    rebuilt, ok, rerr := m.tryRebuildFromProcessTree(req, info)
    if rerr != nil {
        // fail-soft: log + fall through to no_parent_fallback
        m.logger.Warn("rebuild_from_process_tree_failed", "err", rerr, "pane", req.TmuxPaneID)
    }
    if ok {
        // Use rebuilt.AgentType in subsequent Upsert; mark trace as rebuilt
        // (req.AgentType still wins for the actual hook event's agent_type;
        // RebuildResult only confirms an alive process matches that family.)
        rebuiltMatched = true  // signals trace meta below
    }
}
```

**重要決策**（與 codex Q4 對齊）：
- **first-match-wins**：registry 註冊順序決定優先序（cc / codex / opencode 三家同時跑時取第一命中）。實際情境下三家很少在同 pane tree 共存，但需文件化。
- **不重建 SubagentRef**：`Frame.Subagents=[]`（見 §1.3）。
- **rebuild 命中 ≠ 取代 hook event AgentType**：`req.AgentType` 仍是事實源（hook 帶來的權威），rebuild 只**證實**「process tree 中確實有該家 agent alive」、給 trace 標記用。若 rebuilt.AgentType ≠ req.AgentType（罕見）優先信 req（hook 是 source of truth）。

### 1.3 SubagentRef rebuild 策略 — **不重建**

理由：
- pane PID tree 的 descendants 是「目前活著的 process」，無法分辨 cc 主進程、subagent 進程、或無關子進程
- subagent 識別需要 process 命名/啟動 pattern，這層複雜度遠超 Phase 3
- Phase 2 PR-2b 的 `SubagentStart`/`Stop` hook 機制已就位 — rebuild 後新 subagent 的 hook 會正常累積，不會永久缺失
- frame.Subagents=[] 的天然語意正是「unreconciled」（codex Q1 暗示，但不需新增 SPA state）

**驗收**：rebuild 後第一個 SubagentStart hook 進來會正確走 `mutateSubagentsWithRetry`，subagent list 自動補齊。Phase 3 測試需 cover 此 path（見 §2.1 R6）。

### 1.4 `no_parent_fallback` reason 升級

**現況**（line 294-297）：

```go
reason := "parent_frame_missing"
if stored.ParentFrameID != "" {
    reason = "parent_frame_found"
}
```

**Phase 3 改為**：

```go
reason := "no_parent_fallback"  // 升級：明確標記「降階用 hook event agent_type 為基準」
if stored.ParentFrameID != "" {
    reason = "parent_frame_found"
} else if rebuiltMatched {
    reason = "daemon_restart_recovery"  // rebuild 命中
}
```

三態 reason：
- `parent_frame_found` — legacy lookup 命中（line 220-228）
- `daemon_restart_recovery` — Item A rebuild 命中
- `no_parent_fallback` — 都未命中，降階用 hook agent_type 為基準（即原 `parent_frame_missing`）

**不新增 trace step**：採 reason-rename 方案（不擴 step 數）。理由：
- 既有 frame-kind step 已有 `reason` 欄位，Inspector group by 已能達成統計
- Trace step 數每 hook 已 5+，避免膨脹
- 「進入前寫一筆 trace step」spec 用語可解讀為「frame trace step 那一筆」（決策即 step）

若後續 Inspector / reparent loop 需要更細粒度（例如分「主動 fallback」vs「rebuild 失敗後 fallback」），追加 step 屬 Phase 4/5 演進，本 phase 不超前。

### 1.5 Trace 寫入路徑

無需動 collector。`applyFrameEvent` 已回傳 `FrameTraceMeta.Reason` 字串，handler.go 的 `c.Frame(req, meta)` 自動寫入 trace step。

**handler-side 影響**：handler.go 對 `meta.Reason == "parent_frame_missing"` 沒有特殊邏輯（只是傳到 trace），改名後既有 handler 路徑零影響。**但需 grep 全 codebase 確認無 hardcoded 字串依賴**（測試 / SPA / 文件）。

### 1.6 零改動邊界

以下 Phase 3 **不得觸碰**：

- `internal/agent/provider.go` / `registry.go` / `coverage.go`（Phase 0）
- `internal/agent/{cc,codex,opencode}/status.go`（Phase 1）
- `internal/agent/{cc,codex,opencode}/hooks.go`（Hook Events #616 已收）
- `internal/agent/probe/**`（lazy rebuild 透過 `Prober.Identify` + `cachedDescendants` 既有 API；新 helper 不入 probe package）
- `internal/agent/subagent.go`（PR-2a SubagentRef，不動）
- `internal/store/frames.go`（PR-2b 已加 `DeleteIfUnchanged` / `UpsertIfUnchanged` / narrow updates；Phase 3 不擴 schema）
- `internal/store/trace.go`（trace step 結構不動）
- `internal/module/agent/sweep.go`（idle sweep 規則不動）
- `internal/tmux/executor.go`（無 `ListPanesForSession`，YAGNI）
- `spa/**`（純 backend phase）

---

## 2. 測試案例清單

### 2.1 `internal/module/agent/frame_ops_test.go`（Phase 3 新增）

| # | 名稱 | 情境 | 斷言 |
|---|---|---|---|
| R1 | `TestRebuildFromProcessTree_HitFirstMatch` | descendants=[100,200,300]，stub Identify 在 PID=200 命中 cc | 回傳 `RebuildResult{AgentType:"cc",PID:200,...}`、`ok=true` |
| R2 | `TestRebuildFromProcessTree_NoMatch` | descendants=[100,200]，stub Identify 全部不命中 | 回傳 `(nil, false, nil)` |
| R3 | `TestRebuildFromProcessTree_DescendantsError` | `cachedDescendants` 回 err | 回傳 `(nil, false, err)`，err 非 nil |
| R4 | `TestRebuildFromProcessTree_RegistryOrder` | 同 PID 同時被 cc 和 codex 識別，cc 註冊在前 | 取 cc（first-match-wins） |
| R5 | `TestApplyFrameEvent_RebuildHit_TraceReason` | mock rebuild 命中 + 既有 lookup chain 全失敗 | trace meta `Reason="daemon_restart_recovery"`，新 frame 建出 |
| R6 | `TestApplyFrameEvent_RebuildHit_ThenSubagentStart` | rebuild 後立即進來 SubagentStart | subagent 正確累積（驗證 rebuild 後 native path 不壞） |
| N1 | `TestApplyFrameEvent_NoParentFallback_TraceReason` | rebuild 未命中 + 既有 lookup chain 全失敗 | trace meta `Reason="no_parent_fallback"`（取代既有 `parent_frame_missing` 測試） |
| N2 | `TestApplyFrameEvent_ParentFrameFound_Unchanged` | legacy `FindByPanePID` 命中 | trace meta `Reason="parent_frame_found"`（regression guard） |
| N3 | `TestApplyFrameEvent_RebuildSkipped_WhenParentFound` | parent 命中時不呼叫 rebuild helper | spy verify `tryRebuildFromProcessTree` call count == 0 |

### 2.2 整合測試（端到端，`handler_test.go` 或新檔）

模擬 spec §7 三情境：

| # | 名稱 | 情境 | 斷言 |
|---|---|---|---|
| I1 | `TestHandleEvent_ColdStart_RebuildRecovers` | 空 frames table + alive process tree（stub Identify 命中） + SessionStart hook | DB 多一 row、reason=`daemon_restart_recovery`、SPA broadcast `NormalizedEvent` 帶新 frame_id |
| I2 | `TestHandleEvent_DaemonRestart_RebuildRecoversForExistingPane` | 模擬 daemon 重啟（frames table reset） + 既有 pane 內 hook | rebuild 命中 + frame 重建、subagents=[] |
| I3 | `TestHandleEvent_MidConnectionGone_NoParentFallback` | frames 空 + Identify 全不命中 + SessionStart hook | reason=`no_parent_fallback`、frame 仍建（用 hook agent_type） |

### 2.3 Drift / regression guard

- **PR-2b PPID proxy walk regression**：保證 SessionStart 走 `findProxyParent` 命中時**不**進入 rebuild 分支（rebuild 在 fallback 末端，proxy walk 是 fallback 前段）。新增 R7 `TestApplyFrameEvent_ProxyHit_SkipsRebuild`。
- **既有 reason 字串依賴**：實作 commit 1 跑 `grep -rn "parent_frame_missing"` 確認 codebase 無依賴字串（測試以外）；如有需同步更新。

---

## 3. TDD Commit 順序

| # | Commit | 範圍 | 紅綠循環 |
|---|---|---|---|
| 1 | `feat(agent): tryRebuildFromProcessTree helper + Identify dispatch` | 新 helper + RebuildResult struct + R1-R4 + R7 | R1-R4 + R7 紅 → 實作 → 綠 |
| 2 | `feat(agent): wire rebuild into applyFrameEvent fallback chain` | applyFrameEvent 插入 + R5/R6/N3 + I1/I2 整合測試 | 紅 → 實作（含 stub provider 註冊 hooks）→ 綠 |
| 3 | `refactor(agent): no_parent_fallback reason explicit` | 既有 reason 改字串 + N1/N2 + I3 + grep guard | N1/N2/I3 紅 → 改字串 + 既有 `parent_frame_missing` 測試重命名 → 綠 |

**Commit 1 → Commit 2 dependency**：commit 1 落地後 helper 存在但未接線；commit 2 接 wiring。中間 main 可保持 build green（helper 有 unit test 但 applyFrameEvent 未叫）。

**Commit 3 獨立**：可單獨 review，與 commit 1/2 無 code 依賴（純字串改名 + 測試）。但放最後因為 N1 測試的命名假設 commit 1/2 已落地（rebuild reason `daemon_restart_recovery` 已存在）。

---

## 4. 行數預估

| 項目 | 估計行數 |
|---|---|
| `frame_ops.go` — `tryRebuildFromProcessTree` + `RebuildResult` + applyFrameEvent insertion | +90 / -3 |
| `frame_ops_test.go` — R1-R7 + N1-N3 | +280 |
| `handler_test.go` — I1-I3 | +120 |
| 整合測試 helper（stub Prober + descendants） | +50 |
| 文件（本 plan） | ~600 |
| **Total code 淨增** | **~540** |
| **Total（含 plan）** | **~1140** |

---

## 5. 不做（明列）

- ❌ SPA 端 `unreconciled` state UI（Phase 5 Inspector 範圍）
- ❌ SPA on-demand inventory query / dev panel session list（Phase 5）
- ❌ `tmux.ListPanesForSession` helper（無消費端，YAGNI）
- ❌ 既有 frame 的 stale check（Phase 4/5；Phase 2 idle sweep 部分覆蓋）
- ❌ SubagentRef rebuild（lazy 由後續 SubagentStart hook 補齊）
- ❌ Periodic poll reconciliation（無需求；hook event 已是事件驅動）
- ❌ Reparent loop（reason=`no_parent_fallback` 已鋪資料邊界，loop 本身待 Phase 5）
- ❌ 同 session 跨 pane inventory 廉價掃（Codex Q4 前半，無消費端不做）
- ❌ 新增 trace step type / collector method（reason 字串升級已足）
- ❌ 改 `frame.Subagents=[]` 的天然語意（不引入 unreconciled bit）

---

## 6. 風險與護欄

### 6.1 `Prober.Identify` 跨 provider 順序

**風險**：cc / codex / opencode 三家若同時 match 同 PID（例如 cc 內呼 codex），rebuild 取的不一定是 hook event 的真正 owner。

**護欄**：
- registry 註冊順序文件化（首登記者優先）；本 phase 不改既有註冊順序
- Rebuild 結果僅用於 trace 標記 + 證實 process tree 中該家 agent alive；frame.AgentType 仍取 `req.AgentType`（hook event 是 SOT）
- 若 rebuilt.AgentType ≠ req.AgentType（mismatch），記入 trace meta `reason="daemon_restart_recovery_mismatch"`（**追加分支**）— Phase 3 觀察用，未來資料統計足夠多再決定行為

### 6.2 `cachedDescendants` 250ms cache 可能 stale

**風險**：rebuild 觸發瞬間，descendants cache 仍是 daemon 啟動前的（理論上應為空，但 cache 存在的情況下…）

**評估**：daemon 啟動時 cache 為空（map init），首次 `cachedDescendants` 必走實際 query；250ms 後續 hook 命中 cache 也是「已 alive」的最新 snapshot。Phase 3 不需特殊處理。

### 6.3 Rebuild 失敗 fail-soft

**風險**：`cachedDescendants` 系統呼叫失敗 / Identify panic / 其他 process 操作異常。

**護欄**：
- helper 簽名包含 err 回傳；caller 收到 err **不返錯**，僅 `m.logger.Warn` 後 fall through 到 `no_parent_fallback`
- 任何 panic 在 helper 內 recover（defer 包），保證 hook 路徑不中斷
- Test R3 驗證 err path

### 6.4 Reason 字串改名的 grep 依賴

**風險**：`parent_frame_missing` 字串可能被 SPA / 測試 / 其他 daemon 模組依賴。

**護欄**：commit 3 第一步先跑 `grep -rn "parent_frame_missing"`，列出所有依賴點同步更新或論述為何不需動。

### 6.5 Phase 2 PR-2b 行為 0 regression

**風險**：插入點在 PR-2b proxy walk **之後**，但需確認 R5（rebuild）/ N1（no_parent_fallback）測試不破壞既有 PPID proxy 邏輯。

**護欄**：R7 顯式驗證「proxy 命中時 rebuild 不被呼叫」（spy 計數 == 0）。同時 commit 2 跑全套 PR-2b 既有測試確保 0 regression。

---

## 7. 驗收清單

- [ ] `go build ./...` 綠
- [ ] `go vet ./...` 無 warning
- [ ] `go test ./...` 全綠（含新增 R1-R7、N1-N3、I1-I3）
- [ ] PR-2a/2b 既有測試 0 regression（28 + 29 = 57 既有 case 全綠）
- [ ] `grep -rn "parent_frame_missing"` 結果空（commit 3 後）
- [ ] 手動測試（reviewer 驗）：
  - daemon 重啟 → 既有 cc session pane 按 enter → SPA 顯示 frame
  - daemon 重啟 → cc 內 `/codex:*` proxy 仍正確掛回 cc.Subagents
  - SQL `select decision, reason, count(*) from agent_trace_steps where kind='frame' group by 1,2` 可看到三態 reason
- [ ] PR description 列三情境 manual verification 結果

---

## 8. Review focus 預期（Round 1 standard）

可能被抓到（已預先處理）：
- ✅ rebuild fail-soft 不吞錯（log + fall through，§6.3）
- ✅ Identify 跨 provider 順序文件化（§6.1）
- ✅ reason 改名 grep guard（§6.4 + commit 3 第一步）
- ✅ SubagentRef 不重建決策論述（§1.3）
- ✅ Phase 2 0 regression 顯式驗證（R7）

可能被抓到（待 review 看）：
- rebuild 命中時 `RebuildResult.AgentType` 與 `req.AgentType` mismatch 的處理是否完整（§6.1 提到追加 trace reason，但細節留 review）
- I1/I2/I3 測試的 stub Prober 是否真實 emulate 了 daemon 重啟條件（測試 fixture 的真實度）
- `Prober.Identify` 是否被 export 到 module package（package boundary）

---

## 9. 相關檔案速查

- 主改動：`internal/module/agent/frame_ops.go`（applyFrameEvent + 新 helper）
- 主測試：`internal/module/agent/frame_ops_test.go` + `handler_test.go`
- 既有依賴：
  - `internal/agent/probe/probe.go` — `Prober.Identify(agentType, pid)`
  - `internal/agent/probe/liveness.go` — `cachedDescendants(target, panePID)` + `ProcessStartTime` + `IsPidAlive`
  - `internal/agent/registry.go` — provider 註冊順序
  - `internal/store/frames.go` — `Upsert` / `GetByIdentity` / `FindByPanePID`
  - `internal/module/agent/trace.go` — `hookTraceCollector.Frame()` 自動寫 reason
- Spec / Discussion：
  - `docs/specs/2026-04-23-lights-rebuild-spec.md` §7
  - `docs/research/2026-04-22-lights-rebuild-discussion.md`（架構討論）
- Codex consulting：job `af47a0fd51ca6667e`（2026-04-25 high-effort architectural review）
