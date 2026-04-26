# Phase 3 TDD Plan — L1 邊界補強

- **Date**: 2026-04-25
- **Version**: v2（v1 → v2 經 1 輪 codex plan review，2 P2/P3 finding 全採納）
- **Spec**: `docs/specs/2026-04-23-lights-rebuild-spec.md` §7
- **Worktree**: `lights-phase-3`（branch `worktree-lights-phase-3`）
- **Baseline**: `1.0.0-alpha.221`（main @ `2633b88d`，Phase 2 PR-2b merged）
- **依賴**: Phase 2 PR-2b ✅（`SubagentRef` 結構 + `findProxyParent` PPID walk + `Prober.IsAliveFor` 已就緒；本 phase 新增 `Prober.FirstAliveAgentInTree` public method）
- **範圍**：`applyFrameEvent` fallback chain 末端補回 + `no_parent_fallback` reason 顯式化
- **預估**：單 PR ~580 行 net（~120 prod 含新 Prober method + 280 test + 120 整合測試 + 50 helper fixture）

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

採 **K8s reconciliation 對照組**：hook event = `watch event`、Prober tree query = `live state read`、目前 fallback chain 失敗就 Upsert = `create-on-miss`。Phase 3 在「都未命中」與「Upsert 新 frame」之間插一條「先嘗試 process tree rebuild」的 reconciliation step，把 daemon downtime 期間遺失的 frame 識別補回。

**未補回 = no_parent_fallback**：明確標記，等 Phase 4/5 的 reparent loop / Inspector 消費。

### 0.4 Plan v1 → v2 review 軌跡

Codex plan review round 1（job 由 `node codex-companion.mjs review --base origin/main --scope branch` 派發）抓到 2 個 finding，全採納：

| # | 嚴重 | 內容 | v2 fix |
|---|---|---|---|
| 1 | P2 | v1 §1.2 寫「直接呼叫 `cachedDescendants` / `Prober.Identify`」— 但 `cachedDescendants` 是 probe package 私有 method、`Prober` **沒有** `Identify`（只有 `RegisterIdentifier` + `IsAliveFor`，`Identify` 是 provider 層）。照 v1 實作會卡在 package boundary。 | 新增 `Prober.FirstAliveAgentInTree(target)` public method（與 `IsAliveFor(agentType, target)` 同型結構，內部用既有 `cachedDescendants` + `identifiers` map）；§1.6 邊界改寫為「probe package 允許新 export method，不改既有 API」 |
| 2 | P3 | v1 §6.1 引入第四態 reason `daemon_restart_recovery_mismatch`，但 §1.4 三態鎖定 + 測試矩陣只覆蓋三態 — 內部 contract 矛盾 | 刪除第四態。本 phase 信任 hook event AgentType 為 SOT；rebuild 命中只證實「該家 agent 有 alive process」。Mismatch 罕見 corner case 留 Phase 4/5 處理 |

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

### 1.2 Lazy rebuild — 新 Prober method + frame_ops 接線

#### 1.2.1 新 Prober method `FirstAliveAgentInTree`

**位置**：`internal/agent/probe/probe.go`（與 `IsAliveFor` 同檔，liveness.go 也可，依現有 method group 而定）

**簽名**：

```go
// FirstAliveAgentInTree walks the tmux target's pane PID descendant tree
// and returns the agent type of the first descendant matched by any
// registered identifier (in registry insertion order). Returns
// ("", 0, nil) when no descendant matches any identifier.
//
// Honors the same 250ms descendant cache as IsAliveFor (delegates to
// cachedDescendants internally).
//
// Used by frame_ops.tryRebuildFromProcessTree (Phase 3) to recover frame
// agent_type after daemon restart, when the hook event's lookup chain
// (GetByIdentity → findProxyParent → FindByPanePID) all miss.
func (p *Prober) FirstAliveAgentInTree(target string) (agentType string, matchedPID int, err error)
```

**內部實作**（pseudo）：
1. `panePID, _ := p.tmux.PanePID(target)` — 取 pane root PID
2. `descendants, err := p.cachedDescendants(target, panePID)` — 既有私有 method
3. iterate registered identifiers（有序 — see §1.2.3）→ 對每個 desc PID 跑 identify → 第一命中即返
4. 全不命中 → 回 `("", 0, nil)`

#### 1.2.2 frame_ops `tryRebuildFromProcessTree` helper

**位置**：`internal/module/agent/frame_ops.go`（與 `findProxyParent` 同檔）

**簽名**：

```go
// tryRebuildFromProcessTree attempts to recover a frame after daemon
// restart by inspecting the pane's live process tree via the Prober.
// Triggered when the standard lookup chain (GetByIdentity →
// findProxyParent → FindByPanePID) all miss on a SessionStart.
//
// Behavior:
//   - Delegates to prober.FirstAliveAgentInTree(target)
//   - Match → return (agentType, true, nil); caller marks trace
//     reason="daemon_restart_recovery"
//   - No match → return ("", false, nil); caller falls through to
//     no_parent_fallback
//   - Any error → return ("", false, err); caller logs + falls through
//     to no_parent_fallback (fail-soft, see §6.3)
//
// Subagent list intentionally NOT rebuilt — left []. Subsequent
// SubagentStart hooks populate refs naturally (see §1.3).
func (m *Module) tryRebuildFromProcessTree(req EventRequest) (matchedAgentType string, ok bool, err error)
```

#### 1.2.3 Registry 註冊順序保證

`FirstAliveAgentInTree` 第一命中規則需要穩定的 identifier 順序。Go map iteration 是無序的，故 `Prober.identifiers` 不能直接用 `range map`。

**實作要求**：`Prober` 需維護 identifier insertion order — 加 `identifierOrder []string` slice 在 `RegisterIdentifier` 時 append；`FirstAliveAgentInTree` 用 slice 順序 iterate。

**測試覆蓋**：R4 顯式驗證 cc 註冊在前 → 同 PID 取 cc。

#### 1.2.4 `applyFrameEvent` 接線（line 228 之後插入）

```go
// pseudo-code
rebuiltMatched := false
if frame == nil && parentFrameID == "" {
    matchedType, ok, rerr := m.tryRebuildFromProcessTree(req)
    if rerr != nil {
        // fail-soft: log + fall through to no_parent_fallback
        m.logger.Warn("rebuild_from_process_tree_failed", "err", rerr, "pane", req.TmuxPaneID)
    }
    if ok {
        rebuiltMatched = true  // signals trace meta below (§1.4)
        _ = matchedType        // not consumed by Upsert; req.AgentType is SOT (see below)
    }
}
```

**重要決策**：
- **first-match-wins**：registry 註冊順序（§1.2.3）
- **不重建 SubagentRef**：`Frame.Subagents=[]`（§1.3）
- **rebuild 命中 ≠ 取代 hook event AgentType**：`req.AgentType` 是 SOT；rebuild 只**證實**「process tree 中該家 agent alive」、給 trace 標記用。`matchedAgentType` 在本 phase 不寫入 frame（故變數丟棄）— 若未來需要追蹤 mismatch 再從 `_` 接出來。Phase 3 不處理 mismatch（v1→v2 codex P3 fix 後簡化）。

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
- `internal/agent/{cc,codex,opencode}/provider.go` 的 `Identify` method 簽名（既有，不改）
- `internal/agent/subagent.go`（PR-2a SubagentRef，不動）
- `internal/store/frames.go`（PR-2b 已加 `DeleteIfUnchanged` / `UpsertIfUnchanged` / narrow updates；Phase 3 不擴 schema）
- `internal/store/trace.go`（trace step 結構不動）
- `internal/module/agent/sweep.go`（idle sweep 規則不動）
- `internal/tmux/executor.go`（無 `ListPanesForSession`，YAGNI）
- `spa/**`（純 backend phase）

**有限改動（明列）**：
- `internal/agent/probe/probe.go` — 加 `FirstAliveAgentInTree` public method（§1.2.1）+ `identifierOrder` slice 維護註冊順序（§1.2.3）；既有 `RegisterIdentifier` / `IsAliveFor` / `cachedDescendants` 行為**不變**
- `internal/module/agent/frame_ops.go` — 加 `tryRebuildFromProcessTree` helper + `applyFrameEvent` line 228 後插入點（§1.2.2 + §1.2.4）；line 294-297 reason 改字串（§1.4）

---

## 2. 測試案例清單

### 2.1 `internal/agent/probe/probe_test.go`（新 method 單元測試）

| # | 名稱 | 情境 | 斷言 |
|---|---|---|---|
| P1 | `TestFirstAliveAgentInTree_HitFirstMatch` | stub tmux PanePID + descendants=[100,200,300]，stub identifier 在 PID=200 命中 cc | 回傳 `("cc", 200, nil)` |
| P2 | `TestFirstAliveAgentInTree_NoMatch` | descendants=[100,200]，所有 identifier 全部不命中 | 回傳 `("", 0, nil)` |
| P3 | `TestFirstAliveAgentInTree_DescendantsError` | tmux PanePID 失敗 / descendants query err | 回傳 `("", 0, err)` |
| P4 | `TestFirstAliveAgentInTree_RegistryOrder` | cc 與 codex 都能識別同 PID=200，cc 先註冊 | 取 cc |
| P5 | `TestRegisterIdentifier_PreservesOrder` | 註冊 codex/cc/opencode 三家 | iteration 順序為註冊順序（驗證 `identifierOrder` slice 機制） |

### 2.2 `internal/module/agent/frame_ops_test.go`（Phase 3 新增）

| # | 名稱 | 情境 | 斷言 |
|---|---|---|---|
| R1 | `TestTryRebuildFromProcessTree_Hit` | stub `Prober.FirstAliveAgentInTree` 回 `("cc", 200, nil)` | helper 回傳 `("cc", true, nil)` |
| R2 | `TestTryRebuildFromProcessTree_Miss` | stub 回 `("", 0, nil)` | helper 回傳 `("", false, nil)` |
| R3 | `TestTryRebuildFromProcessTree_Error` | stub 回 err | helper 回傳 `("", false, err)`，err 非 nil |
| R5 | `TestApplyFrameEvent_RebuildHit_TraceReason` | mock rebuild 命中 + 既有 lookup chain 全失敗 | trace meta `Reason="daemon_restart_recovery"`，新 frame 建出 |
| R6 | `TestApplyFrameEvent_RebuildHit_ThenSubagentStart` | rebuild 後立即進來 SubagentStart | subagent 正確累積（驗證 rebuild 後 native path 不壞） |
| R7 | `TestApplyFrameEvent_ProxyHit_SkipsRebuild` | SessionStart 走 `findProxyParent` 命中 | rebuild helper call count == 0（PR-2b proxy 路徑優先） |
| R8 | `TestApplyFrameEvent_RebuildErrorFailsSoft` | rebuild 回 err | trace meta `Reason="no_parent_fallback"`（fail-soft，不返錯） |
| N1 | `TestApplyFrameEvent_NoParentFallback_TraceReason` | rebuild 未命中 + 既有 lookup chain 全失敗 | trace meta `Reason="no_parent_fallback"`（取代既有 `parent_frame_missing` 測試） |
| N2 | `TestApplyFrameEvent_ParentFrameFound_Unchanged` | legacy `FindByPanePID` 命中 | trace meta `Reason="parent_frame_found"`（regression guard） |
| N3 | `TestApplyFrameEvent_RebuildSkipped_WhenParentFound` | parent 命中時不呼叫 rebuild helper | spy verify `tryRebuildFromProcessTree` call count == 0 |

### 2.3 整合測試（端到端，`handler_test.go` 或新檔）

模擬 spec §7 三情境：

| # | 名稱 | 情境 | 斷言 |
|---|---|---|---|
| I1 | `TestHandleEvent_ColdStart_RebuildRecovers` | 空 frames table + stub `FirstAliveAgentInTree` 命中 + SessionStart hook | DB 多一 row、reason=`daemon_restart_recovery`、SPA broadcast `NormalizedEvent` 帶新 frame_id |
| I2 | `TestHandleEvent_DaemonRestart_RebuildRecoversForExistingPane` | 模擬 daemon 重啟（frames table reset） + 既有 pane 內 hook | rebuild 命中 + frame 重建、subagents=[] |
| I3 | `TestHandleEvent_MidConnectionGone_NoParentFallback` | frames 空 + stub 全不命中 + SessionStart hook | reason=`no_parent_fallback`、frame 仍建（用 hook agent_type） |

### 2.4 Regression guard

- **PR-2b PPID proxy walk regression**：R7 顯式驗證 SessionStart 走 `findProxyParent` 命中時**不**進入 rebuild 分支
- **既有 reason 字串依賴**：commit 3 第一步跑 `grep -rn "parent_frame_missing"` 確認 codebase 無依賴字串（測試以外）；如有需同步更新

---

## 3. TDD Commit 順序

| # | Commit | 範圍 | 紅綠循環 |
|---|---|---|---|
| 1 | `feat(probe): FirstAliveAgentInTree + ordered identifiers` | 新 Prober method + `identifierOrder` slice + P1-P5 | P1-P5 紅 → 實作 → 綠 |
| 2 | `feat(agent): tryRebuildFromProcessTree helper` | frame_ops helper（不接線）+ R1-R3 | R1-R3 紅 → 實作 → 綠 |
| 3 | `feat(agent): wire rebuild into applyFrameEvent fallback chain` | applyFrameEvent 插入 + R5/R6/R7/R8/N3 + I1/I2 整合測試 | 紅 → 實作 → 綠 |
| 4 | `refactor(agent): no_parent_fallback reason explicit` | 既有 reason 改字串 + N1/N2 + I3 + grep guard | N1/N2/I3 紅 → 改字串 + 既有 `parent_frame_missing` 測試重命名 → 綠 |

**Commit chain dependency**：
- Commit 1 落地：probe API ready，frame_ops 尚未消費，main build green
- Commit 2 落地：helper 存在但未接線，main build green（helper 有 unit test 但 applyFrameEvent 未叫）
- Commit 3 落地：wiring 完成，daemon_restart_recovery 路徑通
- Commit 4 落地：reason rename + 既有 testname 同步

每 commit 後跑全套 `go test ./...` 確保 0 regression。

---

## 4. 行數預估

| 項目 | 估計行數 |
|---|---|
| `probe.go` — `FirstAliveAgentInTree` + `identifierOrder` 維護 | +50 / -2 |
| `probe_test.go` — P1-P5 | +120 |
| `frame_ops.go` — `tryRebuildFromProcessTree` + applyFrameEvent insertion + reason rename | +70 / -5 |
| `frame_ops_test.go` — R1-R3, R5-R8, N1-N3 | +280 |
| `handler_test.go` — I1-I3 | +120 |
| 整合測試 helper（stub Prober interface） | +50 |
| 文件（本 plan v2） | ~700 |
| **Total code 淨增** | **~690** |
| **Total（含 plan）** | **~1390** |

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

### 6.1 `FirstAliveAgentInTree` 跨 provider 順序

**風險**：cc / codex / opencode 三家若同時 match 同 PID（例如 cc 內呼 codex），rebuild 取的不一定是 hook event 的真正 owner。

**護欄**：
- `Prober.identifierOrder` slice 維護註冊順序（§1.2.3），保證 iteration deterministic
- 既有 module init 註冊順序保持不變（cc → codex → opencode；本 phase 不改）
- Rebuild 結果僅用於 trace 標記 + 證實 process tree 中該家 agent alive；frame.AgentType 仍取 `req.AgentType`（hook event 是 SOT）
- **不追蹤 mismatch**（v1→v2 codex P3 fix 後簡化）：若 `matchedAgentType ≠ req.AgentType`，本 phase 信任 hook event、丟棄 rebuild 結果中的 type 資訊；mismatch corner case 留 Phase 4/5 處理（Inspector 出現後再評估資料統計需求）

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
- ✅ rebuild fail-soft 不吞錯（log + fall through，§6.3 + R8 測試）
- ✅ Identifier 跨 provider 順序穩定性（§1.2.3 + §6.1 + P5 測試）
- ✅ reason 改名 grep guard（§6.4 + commit 4 第一步）
- ✅ SubagentRef 不重建決策論述（§1.3）
- ✅ Phase 2 0 regression 顯式驗證（R7）
- ✅ Package boundary 修正（§1.6 「有限改動」+ §1.2.1 新 public method；v1→v2 codex P2 fix）
- ✅ Mismatch reason 不引入第四態（§6.1 + v1→v2 codex P3 fix）

可能被抓到（待 review 看）：
- I1/I2/I3 測試的 stub Prober 是否真實 emulate 了 daemon 重啟條件（測試 fixture 的真實度）
- `Prober.FirstAliveAgentInTree` 介面 vs frame_ops 透過 interface 抽象（dependency inversion 是否需做）
- `identifierOrder` slice 與 `identifiers` map 雙寫的並發安全（既有 RegisterIdentifier 是否有 mutex）

---

## 9. 相關檔案速查

- 主改動：
  - `internal/agent/probe/probe.go`（新 `FirstAliveAgentInTree` + `identifierOrder`）
  - `internal/module/agent/frame_ops.go`（applyFrameEvent insertion + helper + reason rename）
- 主測試：
  - `internal/agent/probe/probe_test.go`（P1-P5）
  - `internal/module/agent/frame_ops_test.go`（R1-R3, R5-R8, N1-N3）
  - `internal/module/agent/handler_test.go`（I1-I3）
- 既有依賴（不改）：
  - `internal/agent/probe/probe.go` — `RegisterIdentifier` / `IsAliveFor` 既有行為不變
  - `internal/agent/probe/liveness.go` — `cachedDescendants(target, panePID)` 私有 method 透過新 public method 走
  - `internal/agent/{cc,codex,opencode}/provider.go` — `Identify(ProcessInfo) bool` 既有
  - `internal/agent/registry.go` — provider 註冊順序
  - `internal/store/frames.go` — `Upsert` / `GetByIdentity` / `FindByPanePID`
  - `internal/module/agent/trace.go` — `hookTraceCollector.Frame()` 自動寫 reason
- Spec / Discussion：
  - `docs/specs/2026-04-23-lights-rebuild-spec.md` §7
  - `docs/research/2026-04-22-lights-rebuild-discussion.md`（架構討論）
- Codex consulting：
  - architectural review job `af47a0fd51ca6667e`（2026-04-25 high-effort）— 設計依據
  - plan v1 review job — 抓 P2 package boundary + P3 reason contract，全採納（§0.4）
