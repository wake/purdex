# Phase 3.5 Plan — Cold-start Proxy Canonicalization (v12 / Hybrid B+)

Baseline：`1.0.0-alpha.225`（main @ `92fb5d05`，Phase 3 已 merged）。
Worktree：`.claude/worktrees/lights-phase-3-5`（branch `worktree-lights-phase-3-5`，已 rebase 上 `92fb5d05`）。
Phase 3 PR #638：✅ squash-merged at `92fb5d05`（2026-04-26）。
Bump 策略：本 PR 系列 + Phase 3 一起 bump **alpha.226**（注意：alpha.225 已被 parallel session 為 SPA tooltip 功能 PR #643 占用，與 Phase 3 無關）。

**v11 → v12 由 PR-3.5a 第五輪 codex review 收斂**（§13；1 high finding 採納）：

- **T1 high / round 5** — v11 SessionStart filter-merge `initialNativeIDs` baseline 用 ID-only 識別。Native IDs 是 provider-supplied strings；SessionStart reset 並發 SubagentStart 若新 native ref reused 舊 session 同 ID，新 ref 會被 baseline match 誤當「舊 session 殘留」silently drop → 使用者新 spawn 的 subagent 靜默消失。**v12 修法**（C23）：baseline 改為 `(Type, ID, StartedAt)` 三元組；新 SubagentStart 的 StartedAt 是 fresh broadcastTs，與 baseline ref 不同 → 不會被誤當 baseline drop。新增 IT21c 守規（baseline native call-1 + 並發 SubagentStart 同 ID 不同 StartedAt）。**Round 5 是 PR-3.5a 最後一輪 review**（trend：round 4 → 5 各 1 high finding，per `feedback_codex_review_termination.md` 進 ship；不再 review）。

**v9 → v10 由 PR-3.5a 第三輪 codex review 收斂**（§13；2 unique finding 全採納）：

- **R1 high / round 3** — round 2 #Q1 fix（projection dedup 把 stateful child 留 visible）仍丟資訊：child 留 visible 後依 `StartedAt` 選 TopFrame，輸出只取單一 frame 的 `Subagents` 上 wire — child 較新→ parent 的 IsProxy ref 丟，parent 較新→ child 的 native ref 丟。**v10 修法**（C18）：dedup hide claimed standalones uniformly（不再 gate `len==0`），但 hide 階段把 stateful child 的 Subagents collect 起來，最後 merge 進 `projection.Subagents`（用既有 `subagentRefMatches` kind-aware identity dedup 避免重複）。最終 wire 同時保留 parent 的 IsProxy ref 與 child 的 native ref。PD4 改寫對齊新行為，新增 PD6（parent older）/ PD7（parent newer 平衡）/ PD8（merge dedup 邊界）。
- **R2 medium / round 3** — round 2 #O3 fix（per-ref fail-safe in pruneDeadProxyRefs）被 sweepOnce 上層繞過：當 owner 自己的 `processStartTimeFn(frame.PID)` 回 error，原本 bare `continue` 把 frame 丟出 survivors → pane 不進 `pruneDeadProxyRefs` → 即便 proxy source 確認 dead 也清不到。**v10 修法**（C19）：owner read error 時把 frame 加入 survivors（保守視為仍存活，不觸發 pid_reused destructive cleanup），讓 pane 仍進 prune；per-ref fail-safe 自己決定是否 detach。新增 IT13d（read error 仍 prune dead source）/ IT14c（read error 不 destructive delete owner）。

**v8 → v9 由 PR-3.5a 第二輪 codex review（standard + adversarial 三視角）收斂**（§13；5 unique finding 全採納）：

- **O1 high / round 2 attack** — filter-merge `prevWrittenNativeIDs` 在多 retry 下 regress：attempt 1 preserved 的 native ref 被寫進 prev → attempt 2 把它當 baseline drop。**v9 修法**（C13）：改用 **`initialNativeIDs` baseline**（loop 前 snapshot frame.Subagents 中的 native ID set），整個 retry 期間 immutable；任何 reload 後出現但不在 baseline 的 native 視為 concurrent attach，**永遠保留**。`prevWrittenNativeIDs` 機制移除。新增 IT21b 守規（2 conflict + 2 concurrent native）。
- **Q1 high / round 2 health** — projection dedup 隱藏 stateful child：reconcile partial（attach OK + delete fail）+ 並發 SubagentStart 給 child 加 native → child 留下且有 native subagent → dedup hide → SPA 看不到 child native。**v9 修法**（C14）：dedup hide 加 `len(frame.Subagents) == 0` guard；非空保留 visible（child 的 native subagent 不能消失於 SPA）。新增 PD4（stateful 保留）/ PD5（empty 仍 hide）。
- **O2 medium / round 2 defend** — `canonicalizeDescendantsAfterUpsert` candidate guard `len(Subagents) > 0` 太寬：stale-only dead IsProxy ref 也算 state 阻 fold；hot-path 漏網無 sweep canonicalize 兜底。**v9 修法**（C15）：分類掃 candidate.Subagents — 含 native ref 或 live identity-verified IsProxy → skip（保護 state）；只含 stale dead/PID-reuse IsProxy → 視同 race-window standalone，**允許 fold**；read error 防禦性 skip。新增 IT22b（fold stale-only）/ IT22c（skip live IsProxy 負例）。
- **O3 medium / round 2 attack** — `pruneDeadProxyRefs` `processStartTimeFn` 回 error 時 detach（fail-destructive）— 暫時 /proc 讀失敗會誤砍 live proxy。**v9 修法**（C16）：fail-safe；只在 `isPidAlive == false` 或 `actualStart != ref.SourceStartTime` confirmed mismatch 才 detach；read error → continue（保留 ref，下次 sweep 再嘗試）。新增 IT14b 守規。
- **P1 medium / round 2 health** — `pruneDeadProxyRefs` detach 成功後不 broadcast projection — `m.subagents` / `currentStatus` 不同步，SPA 看不到修正直到下個無關 hook。**v9 修法**（C17）：新增 `broadcastProxyPruned` helper（mirror `afterFrameCleared` 路徑），偵測到至少一個 detach 後在 pane 維度發出一次 `sweep:proxy_pruned` 廣播；多個 stale ref 在同 pane 自動 coalesce。新增 IT13c 守規。

**v7 → v8 由 PR-3.5a 第一輪 codex review 收斂**（§13；5 finding 採納 / 1 deferred 開 follow-up）：

- **M4 medium / round PR-2 attack** — `reconcileCreatedFrameAsProxy` partial 路徑回 `canonicalized=false` 走 `created_frame` trace（child 視角），但 projection_dedup 顯示 parent + proxy ref → trace ≠ projection。**v8 修法**：partial 仍回 `canonicalized=true`，caller 走 `updated_frame / post_upsert_canonicalization_self` 與 projection 一致；metric 仍 +1。
- **M3 high / round PR-2 attack** — filter-merge-retry「只保留 IsProxy」filter 在 conflict reload 時把並發 SubagentStart attach 的 native ref 一併 drop。**v8 修法**：改 prune-stale-IsProxy 語意；attempt 0 仍 reset baseline native（SessionStart 主語意），attempt N>0 透過 `prevWrittenNativeIDs` 區分 baseline vs concurrent attach，concurrent 保留 baseline 清掉。
- **M2 high / round PR-2 attack** — `canonicalizeDescendantsAfterUpsert` scan 直接 fold 任何 cross-type live PPID-descendant；若 candidate 已累積 native subagent / 自己 IsProxy ref，DELETE 會靜默吞掉。**v8 修法**：scan 跳過 `len(candidate.Subagents) > 0` 的 candidate（race-window standalone 必為空）。
- **L1 high / round PR-2 attack** — SessionEnd 走 delete-first + best-effort detach；detach 失敗（storage / retry exhaust / daemon crash 中段）→ child 沒了 + parent 留 stale proxy ref → 永久 lit dot；PR-3.5a 無 sweep prune 兜底。**v8 修法**：(a) SessionEnd 改 detach-first + propagate（detach error 不 delete）；(b) `pruneDeadProxyRefs` 從 PR-3.5b 移進 PR-3.5a sweep.go（sweepOnce 第三 pass）。
- **N1 high / round PR-2 attack（deferred）** — projection_dedup 信任 IsProxy claim 隱藏 standalone；codex 擔心 PID 重用 race。實作已用 (PID, StartTime) identity 比對防 PID reuse（startTime 是 jiffies precision 不會 collision），且 dedup 在 hot-path read 加 syscall 影響 perf。**不修，open follow-up**：PR-3.5b `pruneDeadProxyRefs` + 本 PR sweep 兜底已涵蓋 90% 場景；hot-path liveness 升級交給 metric 觸發。

**v6 → v7 由 codex round 6 收斂**（§13；1 high doc-gate finding 採納）：

- **K1 high** — §8 ship gate 寫「IT1-IT16 全綠」但 v5/v6 加了 IT17-IT20 沒同步，實作者照原 gate 可跳過 IT20（J1 regression test）→ race 可悄悄回歸；同時 IT17 描述沿用 v5 已被 v6 移除的「snapshot + reset + re-attach」語意。**v7 修法**：§8 改 IT1-IT20 全綠 + 更新 IT17 描述為 filter-merge-retry 語意。**設計層面六輪後完整收斂**（round 6 無任何架構/race/邏輯 finding）。

**v5 → v6 由 codex round 5 收斂**（§13；1 high finding 採納）：

- **J1 high** — v5 §2.2.2「snapshot live proxies → reset → re-attach」三步非原子，並發 child SessionStart attach 新 proxy + 刪 child standalone 後，我的 reset 把新 proxy 也清掉、re-attach 只還原舊 ref → 新 proxy 永久消失。**v6 修法**：合併成單一 **filter-merge-retry pattern**（與 PR-2b `mutateSubagentsWithRetry` 同型，但語意是 filter 而非 add），消除中間態 race window。新 IT20 守規。

**v4 → v5 由 codex round 4 收斂**（§13；2 finding 全採納）：

- **I1 high** — existing-frame SessionStart 的 reset 把已成功 canonicalize 的 live proxy ref 清掉，descendant scan 無 standalone 可補回 → 永久漏顯 → §2.2.2 加 **pre-reset snapshot + re-attach live proxies**（identity gate 通過的才保留）；新增 IT17 守規
- **I2 medium** — v4 §2.6 聲稱 SLO 1/1000 觸發升級到 SQL tx，但 expvar 不接 endpoint + 無 denominator + daemon restart 歸零 → **觀察前提塌陷** → §2.6 改寫誠實版（in-process counter foundation；SLO 量測為 follow-up）；§0.2 設計論證改為三 evidence point（不再依賴「observable 為 ship gate」）

**v3 → v4 由兩 codex 平行架構 consulting 收斂**（§13；採 Side B Hybrid B+，捨棄 Side A SQL transaction）：

> 三輪 codex review 都抓到同類 partial-state finding（F2 → G1 → H1）= meta-drift signal（feedback_codex_meta_drift_signal.md）。停止 patch，派 high-effort consulting。Side A 提議 SQL transaction 根除 attach+delete partial 物理層；Side B 用 evidence-based reasoning 證明 partial 是 ephemeral telemetry 的合法狀態，sweep recovery 是 2s（不是 1h），projection 層 dedup 即可隱藏 user-visible 差異。

**v4 設計典範轉移**（不是再加 patch）：

| 層 | v3 路線 | **v4 路線** |
|---|---|---|
| Hot-path 一致性 | rollback 強制保證 final state 一致；rollback 失敗 propagate error + partial trace | **接受 partial state 為合法**；rollback 移除；trace 簡化 |
| User-visible | 依賴 rollback + sweep 兜底；trace partial signal 作觀察 | **Projection 層 dedup** 隱藏 partial；DB 一致性 = 軟標準 |
| SessionEnd | 依賴 sweep 收 partial（v3 漏：child 退出後 parent 殘留 proxy ref 永久不清） | **SessionEnd hot path 也清 parent proxy ref** + sweep prune dead proxy refs |
| Sweep 兜底 | canonicalizePane 補 standalone child（缺 candidate identity gate）| canonicalizePane 加 candidate identity gate（修 v3 H2 同類 bug）+ pruneDeadProxyRefs（修 Side B 抓的 SessionEnd 殘留洞）|
| Trace | 加 `partial_canonicalization` reason（v3 H3：handler.go err path 不會寫入）| **移除** partial reason（projection 看不到 partial → 不需 trace；順便解 H3）|
| Observability | partial trace（不可寫入）| **expvar 計數器**：partial_canonicalization_created / projection_dedup_hidden / sweep_canonicalized / sweep_pruned_proxy |

---

## 0. 來龍去脈

### 0.1 修什麼

**Race 來源**（不變）：Phase 2 PR-2b 的 `findProxyParent` 只做 *pre-Upsert* PPID 走訪。並發冷啟動的兩個跨型別 SessionStart 可能各自 walk-miss → 各自 Upsert → 兩個獨立 frame，PR-2b proxy collapse 失效。

**最容易觸發的場景**（不變）：daemon restart 後 cc + codex proxy 同時冷啟動。Phase 3 加 `tryRebuildFromProcessTree` 後 race window 變大，但 race 本身在 alpha.221 PR-2b 落地起就存在。

### 0.2 設計哲學（v4 核心轉折）

`agent_frames` 表是 **ephemeral telemetry**（store migration 註解明確說 legacy 偵測時可 lossless clear `internal/store/frames.go:65`）。它不是 durable domain state，row-level 跨表強一致性標準過高。

**partial state 在這張表是合法的中間狀態**，依三 evidence point（不依賴 SLO 量測，因 v5 I2 揭穿本 PR 內 SLO 不可量測）：

1. **Ephemeral telemetry 性質** — `agent_frames` 表 store migration 註解明確說 legacy 偵測時可 lossless clear（`internal/store/frames.go:65`），表本身不是 durable invariant
2. **User-visible（projection / SPA）永遠看不到 partial** — projection 層 dedup（§2.4）
3. **Eventual consistency 在 bounded time** — sweep 2s 一輪即收斂（`sweep.go:20` `sweepInterval = 2 * time.Second`，user-visible 觀察延遲 ≤ 2s）

Metrics 是 in-process 觀察基礎，**不是 ship gate**，也不是「partial state 接受性」的依據（前述三 evidence point 才是）。SLO-based 升級到強一致（Side A SQL transaction）是 follow-up phase 的決策，需先補上 metrics endpoint + denominator 才有量測前提。

依此設計分四層：

| 層 | 職責 | 實作 |
|---|---|---|
| **Hot path** | best-effort canonicalization；失敗不 retry/rollback；繼續 | `reconcileCreatedFrameAsProxy` + `canonicalizeDescendantsAfterUpsert` + SessionEnd `removeProxyRefForSender` + **existing-frame SessionStart preserve live proxies**（v5 I1 fix）|
| **Projection** | 隱藏 partial（**唯一 strongly consistent 邊界**）| `buildPaneProjection` dedup：parent.Subagents 含 IsProxy + (SourcePID, SourceStartTime) → 排除 matching standalone frame |
| **Sweep** | bounded-time recovery（2s 一輪）| `canonicalizePane` 補 hot-path 漏網 + `pruneDeadProxyRefs` 清死 proxy ref |
| **Observability**（in-process）| partial 發生率累計（daemon run 內）| expvar counters；**endpoint exposure 為 follow-up，本 PR 不掛 ship gate**（v5 I2 fix）|

### 0.3 與 Phase 3 的關係

Phase 3 PR #638 已 merged at `92fb5d05`（2026-04-26）。Phase 3.5 worktree 已 rebase 上新 main，docs commits replay 無衝突（docs-only 不動 code）。後續 3.5a 實作 commits 起於 alpha.225 era main，接線位置（applyFrameEvent fallback chain 由 Phase 3 加在 line ~228 後；Phase 3.5 接線在 new-frame Upsert 後 line ~286 + existing-frame Update 後 line ~272 + SessionEnd line ~53）— 區塊不同，無衝突。

---

## 1. 既有原語（沿用，無新 store API）

| 原語 | 位置 | 用途 |
|---|---|---|
| `findProxyParent(req)` | `frame_ops.go:688` | PPID 鏈走訪 + same-pane + live + identity-verified + cross-type；reconcile 重用 |
| `attachProxyRefWithRetry(parent, ref, broadcastTs)` | `frame_ops.go:631` | optimistic concurrency attach |
| `detachProxyRefWithRetry(owner, senderPID, senderStartTime, broadcastTs)` | `frame_ops.go:640` | optimistic concurrency detach |
| `removeProxyRefForSender(...)` | `frame_ops.go:546` | SessionEnd path 既有 proxy detach；v4 也用於 hot-path proxy cleanup |
| `FramesStore.DeleteIfUnchanged` | `internal/store/frames.go:263` | atomic delete |
| `FramesStore.ListByPane` / `GetByIdentity` | 既有 | scan + reload |
| `SubagentRef{...}` | `internal/agent/subagent.go` | proxy ref 型別（PR-2a）|
| Proxy ID 格式 `proxy:%s:%d:%s` | `frame_ops.go:176` | reconcile / scan / sweep 一致 |

**沒有**新 store method、沒有新型別、沒有 SQL transaction。

---

## 2. 設計細節

### 2.1 Hot-path helpers（簡化自 v3，移除 rollback）

#### 2.1.1 `reconcileCreatedFrameAsProxy`（self-as-descendant）

```go
// reconcileCreatedFrameAsProxy attempts to canonicalize a freshly-created
// SessionStart frame against an alive cross-type ancestor. If found, attaches
// proxy ref to ancestor + best-effort deletes self's standalone frame.
//
// v4 design: best-effort. If DeleteIfUnchanged fails (concurrent writer
// touched child row), increment partial counter and return; sweep
// canonicalize will retry within 2s (sweepInterval). projection_dedup
// hides the partial state from SPA in the meantime.
//
// Returns (canonicalized=true, parentStored, nil) when attach + delete
// both succeeded. Returns (false, zero, nil) for: no ancestor / parent
// vanished mid-attach / partial state (attach succeeded, delete failed —
// counted in metrics, repaired by sweep). Storage errors propagate.
func (m *Module) reconcileCreatedFrameAsProxy(
    stored store.Frame,
    req EventRequest,
    broadcastTs int64,
) (bool, store.Frame, error) {
    parent, err := m.findProxyParent(req)
    if err != nil {
        return false, store.Frame{}, err
    }
    if parent == nil {
        return false, store.Frame{}, nil
    }
    ref := agentpkg.SubagentRef{
        ID:              fmt.Sprintf("proxy:%s:%d:%s", req.AgentType, req.SenderPID, req.SenderStartTime),
        Type:            req.AgentType,
        StartedAt:       broadcastTs,
        SourcePID:       req.SenderPID,
        SourceStartTime: req.SenderStartTime,
        IsProxy:         true,
    }
    attached, parentStored, aerr := m.attachProxyRefWithRetry(*parent, ref, broadcastTs)
    if aerr != nil {
        return false, store.Frame{}, aerr
    }
    if !attached {
        return false, store.Frame{}, nil
    }
    deleted, derr := m.frames.DeleteIfUnchanged(stored.FrameID, stored.LastSeenAt)
    if derr != nil {
        return false, store.Frame{}, derr
    }
    if !deleted {
        // Partial state: parent has proxy + child still standalone.
        // Acceptable transient — projection_dedup hides; sweep canonicalize
        // (2s loop) repairs. Increment metric for observability.
        metricPartialCanonicalizationCreated.Add(1)
        log.Printf("phase3.5: reconcile partial state child %s parent %s (sweep will repair within 2s)", stored.FrameID, parentStored.FrameID)
        return false, store.Frame{}, nil
    }
    return true, parentStored, nil
}
```

**為什麼移除 v2/v3 的 retry + rollback**：

- Side B consulting evidence：sweep 2s loop + projection dedup → partial 容忍度從「分鐘級」降「秒級且不可見」
- 三輪 codex review 都抓 partial state finding 不是 race 本身嚴重，而是「v1/v2/v3 把 ephemeral 表當 durable 標準」 — 修法是調標準，不是加 patch
- 移除 rollback → caller 邏輯簡化、無需 partial trace（H3 自然消失）、無需 propagate partial error（user-visible 已被 dedup 隱藏）

#### 2.1.2 `canonicalizeDescendantsAfterUpsert`（self-as-ancestor）

```go
// canonicalizeDescendantsAfterUpsert scans pane for standalone cross-type
// descendants whose PPID chain passes through self.PID + identity-verified
// alive, and folds them as proxy refs on self.
//
// v4: identity gate (alive + processStartTimeFn match) prevents PID-reuse
// stale row pollution (codex round 2 G2 + same logic applied to sweep H2).
// Best-effort delete; partial state acceptable (sweep + projection_dedup).
func (m *Module) canonicalizeDescendantsAfterUpsert(
    self store.Frame,
    broadcastTs int64,
) (store.Frame, error) {
    if m.frames == nil {
        return self, nil
    }
    frames, err := m.frames.ListByPane(self.PaneID)
    if err != nil {
        return self, err
    }
    current := self
    for _, candidate := range frames {
        if candidate.FrameID == current.FrameID {
            continue
        }
        if candidate.AgentType == current.AgentType {
            continue
        }
        if !pidIsAncestorOfWithCap(candidate.PID, current.PID, proxyMaxDepth) {
            continue
        }
        // Identity gate (PR-2b alignment + v3 G2 + sweep H2 unification).
        if !isPidAliveFn(candidate.PID) {
            continue
        }
        actualStart, sterr := processStartTimeFn(candidate.PID)
        if sterr != nil || actualStart != candidate.ProcessStartTime {
            continue
        }
        ref := agentpkg.SubagentRef{
            ID:              fmt.Sprintf("proxy:%s:%d:%s", candidate.AgentType, candidate.PID, candidate.ProcessStartTime),
            Type:            candidate.AgentType,
            StartedAt:       broadcastTs,
            SourcePID:       candidate.PID,
            SourceStartTime: candidate.ProcessStartTime,
            IsProxy:         true,
        }
        attached, parentStored, aerr := m.attachProxyRefWithRetry(current, ref, broadcastTs)
        if aerr != nil {
            return current, aerr
        }
        if !attached {
            return current, nil
        }
        deleted, derr := m.frames.DeleteIfUnchanged(candidate.FrameID, candidate.LastSeenAt)
        if derr != nil {
            return parentStored, derr
        }
        if !deleted {
            metricPartialCanonicalizationCreated.Add(1)
            log.Printf("phase3.5: descendant scan partial state child %s parent %s", candidate.FrameID, parentStored.FrameID)
            // Continue scanning others; this candidate retries in sweep.
        }
        current = parentStored
    }
    return current, nil
}

// pidIsAncestorOfWithCap walks descendant's PPID chain (capped at depth)
// looking for ancestorPID. Returns true on hit, false on miss / depth /
// info error / loop detection (PPID == PID).
func pidIsAncestorOfWithCap(descendantPID, ancestorPID, maxDepth int) bool {
    current := descendantPID
    for depth := 0; depth < maxDepth; depth++ {
        info, err := readProcessInfoFn(current)
        if err != nil {
            return false
        }
        if info.PPID == ancestorPID {
            return true
        }
        if info.PPID <= 1 || info.PPID == current {
            return false
        }
        current = info.PPID
    }
    return false
}
```

### 2.2 接線（applyFrameEvent，兩個 wire site）

#### 2.2.1 New frame 路徑（line 286-292 之後）

```go
} else {
    stored, err = m.frames.Upsert(store.Frame{...})
    if err != nil {
        return nil, FrameTraceMeta{}, err
    }

    // Phase 3.5: bidirectional canonicalization (best-effort).
    if req.EventName == "SessionStart" {
        canonicalized, parentStored, rerr := m.reconcileCreatedFrameAsProxy(stored, req, broadcastTs)
        if rerr != nil {
            return nil, FrameTraceMeta{}, rerr
        }
        if canonicalized {
            projection, err := m.projectPane(req.TmuxPaneID)
            return projection, FrameTraceMeta{
                FrameID:       parentStored.FrameID,
                ParentFrameID: parentStored.ParentFrameID,
                Decision:      "updated_frame",
                Reason:        "post_upsert_canonicalization_self",
                Before:        before,
                After:         summarizeFrame(&parentStored),
            }, err
        }
        // self stays standalone or partial: try descendant scan.
        updated, cerr := m.canonicalizeDescendantsAfterUpsert(stored, broadcastTs)
        if cerr != nil {
            return nil, FrameTraceMeta{}, cerr
        }
        stored = updated
    }
}
```

#### 2.2.2 Existing frame 路徑（line 256-272 區）

**v6 J1 fix**：替換「snapshot → reset → re-attach」三步非原子序列為**單一 filter-merge-retry pattern**。

語意：SessionStart 對 existing frame 的 reset 不該清掉「同 pane 仍 live 的 cross-type IsProxy refs」（v5 I1）。但三步法在 snapshot 後、reset 前的 race window 內，並發 child SessionStart 可能新 attach proxy 後被 reset 抹除（v5 J1）。

修法 — 在 retry loop 內 filter live IsProxy + 寫入：每次 retry 重讀 subagents（包含 race window 內的並發 attach），filter 後 UpsertIfUnchanged。concurrent attach 會 bump frame.LastSeenAt → 我的 IfUnchanged 看到 mismatch → reload → 新一輪 filter 保留新 ref → 收斂。**全程無中間態暴露**。

```go
} else {
    // SessionStart on existing frame: filter-merge-retry to preserve
    // live identity-verified IsProxy refs across reset semantics. Other
    // events use the existing UpdateHookPath narrow update.
    if req.EventName == "SessionStart" {
        // v6 J1 fix: replace "snapshot live proxies → bulk reset → re-attach"
        // (v5) which had a race window between snapshot and reset (concurrent
        // child SessionStart's attach + delete would be lost). Filter-merge-
        // retry reads frame.Subagents on every retry, so concurrent attach
        // is naturally preserved (next reload includes the new ref).
        var success bool
        for attempt := 0; attempt < proxyUpsertMaxAttempts; attempt++ {
            filtered := []agentpkg.SubagentRef{}
            for _, ref := range frame.Subagents {
                if !ref.IsProxy {
                    continue
                }
                if !isPidAliveFn(ref.SourcePID) {
                    continue
                }
                actualStart, sterr := processStartTimeFn(ref.SourcePID)
                if sterr != nil || actualStart != ref.SourceStartTime {
                    continue
                }
                filtered = append(filtered, ref)
            }

            candidate := *frame
            candidate.AgentType = req.AgentType
            candidate.PPID = info.PPID
            candidate.ParentFrameID = parentFrameID
            candidate.Status = status
            candidate.LastSeenAt = broadcastTs
            candidate.Verified = true
            candidate.Subagents = filtered

            ok, written, err := m.frames.UpsertIfUnchanged(candidate, frame.LastSeenAt)
            if err != nil {
                return nil, FrameTraceMeta{}, err
            }
            if ok {
                stored = written
                success = true
                break
            }
            // Conflict: concurrent writer touched the row (proxy attach,
            // SubagentStart hook, probe status update). Reload + retry —
            // re-read includes any new IsProxy ref the racer added, and
            // next iteration's filter preserves it.
            reloaded, rerr := m.frames.GetByIdentity(req.TmuxPaneID, req.SenderPID, req.SenderStartTime)
            if rerr != nil {
                return nil, FrameTraceMeta{}, rerr
            }
            if reloaded == nil {
                // Frame deleted mid-flight. Treat as frame_missing (caller
                // path will project pane and emit skipped trace).
                projection, perr := m.projectPane(req.TmuxPaneID)
                return projection, FrameTraceMeta{
                    Decision: "skipped",
                    Reason:   "frame_missing",
                    Before:   before,
                    After:    map[string]any{},
                }, perr
            }
            frame = reloaded
        }
        if !success {
            return nil, FrameTraceMeta{}, fmt.Errorf("session_start filter-merge: exceeded %d retries for frame %s", proxyUpsertMaxAttempts, frame.FrameID)
        }
    } else {
        // Non-SessionStart existing frame: original narrow update path
        // (PR-2b R8 — don't round-trip subagents).
        updated := *frame
        updated.AgentType = req.AgentType
        updated.PPID = info.PPID
        updated.ParentFrameID = parentFrameID
        updated.Status = status
        updated.LastSeenAt = broadcastTs
        updated.Verified = true
        if err := m.frames.UpdateHookPath(updated); err != nil {
            return nil, FrameTraceMeta{}, err
        }
        reloaded, rerr := m.frames.GetByIdentity(req.TmuxPaneID, req.SenderPID, req.SenderStartTime)
        if rerr != nil {
            return nil, FrameTraceMeta{}, rerr
        }
        if reloaded == nil {
            return nil, FrameTraceMeta{}, nil
        }
        stored = *reloaded
    }

    // Phase 3.5: descendant scan after successful filter-merge or narrow
    // update. Catches any cold-start race window children whose
    // SessionStart raced with this existing-frame's SessionStart.
    if req.EventName == "SessionStart" {
        updated, cerr := m.canonicalizeDescendantsAfterUpsert(stored, broadcastTs)
        if cerr != nil {
            return nil, FrameTraceMeta{}, cerr
        }
        stored = updated
    }
}
```

**為什麼用 UpsertIfUnchanged 而非 narrow update + reset helper**：

- v5 I1 fix 原本嘗試在 narrow update 邊界外做 snapshot + re-attach；J1 揭示三步法的非原子性根本問題
- v6 採 UpsertIfUnchanged 寫整個 frame（含 subagents），但因為 subagents 是「filtered live IsProxy」**有意決策值**，不是「stale baseline」 — 不違反 PR-2b R8 narrow update 哲學（避免 round-trip stale subagents）。我們是**主動決定**這個值，並透過 IfUnchanged 確保決策對應的 baseline 沒被搶先改
- 該 store-level helper `UpdateHookPathAndResetSubagents` 在 v6 SessionStart existing-frame 路徑不再使用；non-SessionStart 路徑沿用 `UpdateHookPath` 不變（narrow column update）
- 仍在 module 層解決，store layer 介面不擴展（不加新 helper）

**對既有 helper 的影響**：

- `UpdateHookPathAndResetSubagents` 在 v6 後**仍存在**（store-level helper 不變），但 SessionStart existing-frame 路徑不再呼叫它
- 若仍有其他呼叫者（grep 確認只有 frame_ops.go SessionStart 路徑用），可在 v6 commit 9 把它從 store API 移除作為 cleanup；不確定的話留著無害
- 接線 commit 9 的 unwired-then-wired 模式仍維持：commit 9 同時完成 §2.2.1 + §2.2.2 接線

### 2.3 SessionEnd proxy cleanup（v4 新，修 Side B 抓的 SessionEnd 漏洞）

**問題**：當 partial state 已存在（parent 含 proxy ref + child standalone frame），child 進入 `SessionEnd` 時：

- 既有 `applyFrameEvent` 命中 child frame（GetByIdentity 找到）→ 走 line 53-65 `Delete(frame.FrameID)` 後 return
- **不會執行 line 70 的 `removeProxyRefForSender`**（那只在 frame == nil 時走）
- 結果：child frame 沒了，但 parent 上的 proxy ref 永久殘留 → SPA 顯示假 dot

**修法**：SessionEnd 命中 child frame 時，刪 frame 後再 best-effort 清同 pane 上對應的 proxy ref：

```go
case "SessionEnd":
    if frame != nil {
        if err := m.frames.Delete(frame.FrameID); err != nil {
            return nil, FrameTraceMeta{}, err
        }
        // v4: best-effort proxy cleanup for partial state where this
        // frame was simultaneously a standalone row AND a proxy ref on
        // an ancestor (cold-start race partial). Without this, parent's
        // proxy ref outlives child SessionEnd permanently.
        _, _, _, _, _ = m.removeProxyRefForSender(req.TmuxPaneID, req.SenderPID, req.SenderStartTime, broadcastTs)
        projection, err := m.projectPane(req.TmuxPaneID)
        return projection, FrameTraceMeta{
            FrameID:       frame.FrameID,
            ParentFrameID: frame.ParentFrameID,
            Decision:      "deleted_frame",
            Reason:        "session_end",
            Before:        before,
            After:         map[string]any{},
        }, err
    }
    // ... existing frame == nil path unchanged
```

**為什麼忽略 removeProxyRefForSender 結果**：partial state 是少數情況；nil/false 是常態（沒有殘留 proxy）。錯誤情境由 sweep `pruneDeadProxyRefs` 兜底（§4）。

### 2.4 Projection layer dedup（v4 新核心）

**`buildPaneProjection` 的當前邏輯**（`projection.go:38`）：

```go
sorted := frames sorted by StartedAt
top := sorted[len(sorted)-1]   // 最後 StartedAt 的 frame
subagents := top.Subagents
return SessionProjection{TopFrame: &top, Subagents: subagents}
```

**問題**：partial state 下，pane 內可能有：
- frame_cc（StartedAt = T1，Subagents 含 codex proxy ref）
- frame_codex_standalone（StartedAt = T2 > T1，因為它 race 後到的，Subagents 空）

排序後 top = frame_codex_standalone → SPA 看到 codex 是 primary，不見 cc + codex proxy 的正確 collapse。

**v4 dedup**：buildPaneProjection 排序前，先掃所有 frame 的 Subagents 收集 proxy 標記的 (SourcePID, SourceStartTime)，然後排除 matching frame：

```go
func buildPaneProjection(paneID string, frames []store.Frame) SessionProjection {
    if len(frames) == 0 {
        return SessionProjection{PaneID: paneID, Subagents: []agentpkg.SubagentRef{}}
    }

    // v4 dedup: collect proxy-claimed senders so we exclude their standalone
    // frame rows from TopFrame selection. Avoids partial-state visibility
    // where a SessionStart racer landed standalone but is also already
    // attached as a proxy ref on the canonical parent (cold-start race).
    type claim struct {
        pid       int
        startTime string
    }
    claimed := make(map[claim]bool)
    for _, frame := range frames {
        for _, ref := range frame.Subagents {
            if ref.IsProxy {
                claimed[claim{ref.SourcePID, ref.SourceStartTime}] = true
            }
        }
    }

    visible := make([]store.Frame, 0, len(frames))
    var hidden int
    for _, frame := range frames {
        if claimed[claim{frame.PID, frame.ProcessStartTime}] {
            hidden++
            continue
        }
        visible = append(visible, frame)
    }
    if hidden > 0 {
        metricProjectionDedupHidden.Add(int64(hidden))
    }
    if len(visible) == 0 {
        // All frames are claimed — extreme edge case where pane has only
        // proxy-attached frames and no canonical owner. Fall back to
        // unfiltered selection (avoid dropping pane entirely).
        visible = frames
    }

    sorted := append([]store.Frame(nil), visible...)
    sort.Slice(sorted, func(i, j int) bool {
        if sorted[i].StartedAt == sorted[j].StartedAt {
            return sorted[i].FrameID < sorted[j].FrameID
        }
        return sorted[i].StartedAt < sorted[j].StartedAt
    })

    primary := sorted[0]
    top := sorted[len(sorted)-1]
    subagents := append([]agentpkg.SubagentRef(nil), top.Subagents...)
    if subagents == nil {
        subagents = []agentpkg.SubagentRef{}
    }
    return SessionProjection{
        PaneID:       paneID,
        PrimaryFrame: &primary,
        TopFrame:     &top,
        Subagents:    subagents,
    }
}
```

**為什麼 fallback to unfiltered**：理論上 pane 內所有 frame 都是別人的 proxy 是不可能（總有一個 canonical），但若發生（極端 cycle），不能讓 pane 直接消失於 projection。fallback 保底 + metric 累加異常觀察。

**為什麼 dedup 在 daemon 不在 SPA**（Side B 修正點）：SPA `useAgentStore` 只收 NormalizedEvent，沒有完整 frame list 與 PID graph，無法 dedup；必須在 daemon `buildPaneProjection`。

### 2.5 Trace decision 詞彙（v4 簡化）

| 決策 | Reason | 觸發 |
|---|---|---|
| `updated_frame` | `proxy_subagent_attached` | PR-2b pre-Upsert proxy fast-path（既有，不改）|
| `created_frame` | `parent_frame_found` / `parent_frame_missing` | 一般 new frame 路徑（既有，不改）|
| `updated_frame` | `post_upsert_canonicalization_self` | self-as-descendant reconcile 收編成功（v2 引入，v4 保留）|

**v3 的 `partial_canonicalization` reason 移除**：

- 原意是 rollback 失敗時觀察 partial — v4 移除 rollback，分支不存在
- handler.go err path 不會呼 `trace.Frame` 寫入（v3 H3）— 結構性無法消費
- 替代：partial 透過 expvar metric 觀察（§2.6），不污染 trace

descendant scan 收編 standalone children 不改 trace（與 v3 一致）— scan 是 self 視角的 side effect，主決策仍是「建/更新此 frame」。

### 2.6 Metrics（v5 誠實版 — 移除 SLO 聲明）

用 Go stdlib `expvar` 累積 in-process counter（無新依賴；不掛 metrics endpoint，不在本 PR 範圍）：

```go
package agent

import "expvar"

var (
    metricPartialCanonicalizationCreated = expvar.NewInt("purdex_phase35_partial_canonicalization_created_total")
    metricProjectionDedupHidden          = expvar.NewInt("purdex_phase35_projection_dedup_hidden_total")
    metricSweepCanonicalized             = expvar.NewInt("purdex_phase35_sweep_canonicalized_total")
    metricSweepPrunedProxy               = expvar.NewInt("purdex_phase35_sweep_pruned_proxy_total")
)
```

**用途（in-process only）**：

- daemon 開發 / debug 期間可用 `runtime.ReadMemStats` 等 in-process 機制 inspect counter，或在單元/整合測試直接 `metric.Value()` 斷言
- 提供 future endpoint exposure 的 instrumentation 基礎（counter 已埋好，只需接 `expvar.Handler()` 或 Prometheus exporter）

**v5 I2 揭穿的限制**（誠實寫明）：

- ❌ **無 metrics endpoint**：本 PR 不掛 `/debug/vars` 或 Prometheus exporter；外部監控無法讀取
- ❌ **無 SessionStart denominator**：counter 是絕對值，不是 ratio；無法直接算 partial rate
- ❌ **daemon restart 歸零**：expvar 是 in-process variable，不持久化；長期 SLO 觀察需另外加 persisted counter（不在本 PR）

**因此 v5 不主張**「partial 發生率超 1/1000 觸發升級到 SQL transaction」這類 SLO 聲明。partial state 接受性的論證走 §0.2 三 evidence point（ephemeral 性質 + projection dedup + sweep 2s），與 metrics 量測無關。

**Follow-up issue（建議）**：補上 metrics endpoint + SessionStart denominator + 持久化 counter，是觀察 partial 真實發生率所必須。本 PR 留 instrumentation 鉤子但不承諾 SLO 量測機制。

### 2.7 Wire compatibility

不改 NormalizedEvent / SubagentRef / projection wire schema。SPA 看到的最終結果與既有 PR-2b proxy attach 完全同型。

---

## 3. 測試矩陣

### 3.1 Integration 測試（必要 ship gate）

新增 `internal/module/agent/canonicalize_test.go`（或加進 `module_test.go` 末尾）：

| # | 名稱 | Sequence | 預期 final state |
|---|---|---|---|
| IT1 | `descendant_then_ancestor_canonicalizes_via_descendant_scan` | (a) codex SessionStart → standalone codex；(b) cc SessionStart → cc Upsert + descendant scan 收編 codex | 1 cc frame + codex proxy；codex frame 已刪；projection top = cc |
| IT2 | `ancestor_then_descendant_canonicalizes_via_pr2b_fast_path` | (a) cc SessionStart；(b) codex SessionStart → pre-Upsert findProxyParent 找到 cc → 既有 fast-path | 1 cc frame + codex proxy（sanity check）|
| IT3 | `concurrent_descendant_first_then_reconcile_hits_post_upsert` | codex applyFrameEvent，mock pre-walk miss + post reconcile hit 模擬 race | 1 cc frame + codex proxy via reconcile |
| IT4 | `concurrent_descendant_post_reconcile_also_misses_recovered_by_ancestor_scan` | codex 兩次 walk 都 miss → standalone；cc applyFrameEvent → descendant scan 收編 | 1 cc frame + codex proxy（**核心 ancestor-late race 驗證**）|
| IT5 | `partial_state_hidden_by_projection_dedup` | DB 直接塞 parent（含 codex proxy ref）+ codex standalone frame；call `buildPaneProjection` | TopFrame = parent（不是 codex）；dedup metric +1 |
| IT6 | `descendant_scan_partial_skip_does_not_block_others` | pane 內兩 standalone（codex + opencode）；mock `DeleteIfUnchanged` codex 失敗 / opencode 成功；cc descendant scan | cc.Subagents 含 opencode proxy（成功）+ codex proxy（partial，next sweep 修）；codex standalone 仍在 |
| IT7 | `existing_frame_session_start_runs_descendant_scan_only` | (a) cc SessionStart 建 frame；(b) codex 在 race window 內 standalone；(c) cc SessionStart 又一次 → reset 後 descendant scan 收編 codex | cc.Subagents 含 codex proxy |
| IT8 | `non_session_start_event_no_canonicalization` | codex Notification 在 frame == nil 路徑建 frame | reconcile + scan 都不觸發；trace 沿用 created_frame |
| IT9 | `findproxyparent_storage_error_aborts_apply` | reconcile 內 storage error | applyFrameEvent error propagate |
| IT10 | `existing_descendant_session_start_canonicalized_via_sweep` | (a) cc 已存在；(b) codex standalone（race 留下）；(c) codex SessionStart 又來（existing path → 不 self-reconcile）；(d) sweep canonicalize 一輪 | sweep 後 cc.Subagents 含 codex proxy；codex standalone 已刪 |
| IT11 | `descendant_scan_skips_pid_reuse_stale` | pane 內 codex stale standalone（PID reuse, actualStart != stored.ProcessStartTime）；cc SessionStart descendant scan | scan skip stale candidate（identity gate）；不收編；stale row 留給 sweep |
| IT12 | `session_end_clears_parent_proxy_ref` (v4 新，Side B 抓的洞) | (a) partial 狀態：cc.Subagents 含 codex proxy + codex standalone frame；(b) codex SessionEnd | cc.Subagents 不含 codex proxy（hot path 清乾淨）；codex frame 已刪 |
| IT13 | `sweep_prune_dead_proxy_ref_when_source_process_dead` (v4 新) | cc.Subagents 含 codex proxy（SourcePID 已死，process not alive）；no standalone frame for codex；sweep 跑一輪 | cc.Subagents 不含 codex proxy（pruneDeadProxyRefs 偵測 dead PID 後 detach）|
| IT14 | `sweep_prune_dead_proxy_ref_when_pid_reused` (v4 新) | cc.Subagents 含 codex proxy（SourcePID alive 但 actualStart != ref.SourceStartTime — PID reuse）；sweep | cc.Subagents 不含 codex proxy（identity mismatch 視為 dead）|
| IT15 | `partial_metric_increments_on_partial_state` (v4 新) | mock DeleteIfUnchanged 持續失敗；reconcile 走 partial path | metricPartialCanonicalizationCreated 對應 +1；trace decision 仍 `created_frame`（無 partial reason）|
| IT16 | `projection_dedup_metric_increments` (v4 新) | DB 多個 partial state；多 paneID call buildPaneProjection | metricProjectionDedupHidden 累加 |
| IT17 | `existing_frame_session_start_preserves_live_proxy_refs` (v5 I1 / v6 filter-merge) | (a) cc frame 已存在且 cc.Subagents 含 codex proxy ref（live + identity verified）；(b) cc 又收 SessionStart（existing path）→ filter-merge-retry pattern：filter 通過 identity gate 的 IsProxy 直接寫進 UpsertIfUnchanged；(c) 之後 codex 進程仍 alive，無新 SessionStart | cc.Subagents 仍含 codex proxy ref（subagents_json 含此 ref，新 LastSeenAt = broadcastTs）；projection dedup 仍工作 |
| IT18 | `existing_frame_session_start_skips_dead_proxy_during_reset` (v5 I1 negative) | (a) cc frame 已存在含 codex proxy ref，但 codex 進程已死（isPidAliveFn = false）；(b) cc SessionStart | reset 後 cc.Subagents 不含該 dead proxy ref（preserve 不通過 identity gate）|
| IT19 | `existing_frame_session_start_skips_pid_reused_proxy` (v5 I1 negative) | (a) cc frame 含 codex proxy；(b) codex PID 已被 OS reuse（actualStart != ref.SourceStartTime）；(c) cc SessionStart | reset 後不 preserve 該 stale proxy（identity 不過 gate）|
| IT20 | `existing_frame_session_start_preserves_concurrently_attached_proxy` (v6 J1) | (a) cc frame 已存在 + 一個 codex proxy ref；(b) cc SessionStart filter-merge attempt 1 — 同時 mock UpsertIfUnchanged 第 1 次回 conflict（模擬並發 child SessionStart 在 attempt 1 之間 attach 第二個 opencode proxy 並刪除其 standalone）；(c) attempt 2 reload 看到兩個 IsProxy ref，filter 通過 gate，UpsertIfUnchanged 成功 | cc.Subagents 含 codex 與 opencode 兩個 proxy ref；無 ref 在 race window 中遺失 |
| IT13d | `sweep_prune_runs_when_owner_start_time_read_errors` (v10 R2) | (a) cc owner PID 200 with stale codex IsProxy ref（SourcePID 300 confirmed dead）；(b) `processStartTimeFn(200)` 回 transient error；(c) sweepOnce 跑 | pane 仍進 prune；codex IsProxy ref 被 detach；MetricSweepPrunedProxy +1 |
| IT14c | `sweep_owner_read_error_preserves_frame` (v10 R2 boundary) | (a) cc owner PID 200, ProcessStartTime "A"；(b) `processStartTimeFn(200)` 持續 transient error；(c) sweepOnce 跑 | owner frame 完整保留（不被誤砍 pid_reused）；ProcessStartTime 仍 "A" |

### 3.2 Unit 測試

`frame_ops_test.go` 加 `RC1`-`RC5`：

| # | 名稱 |
|---|---|
| RC1 | `pidIsAncestorOfWithCap_depth_exhaustion` |
| RC2 | `pidIsAncestorOfWithCap_loop_detection_ppid_eq_pid` |
| RC3 | `pidIsAncestorOfWithCap_process_info_error` |
| RC4 | `canonicalizeDescendantsAfterUpsert_skips_same_type` |
| RC5 | `canonicalizeDescendantsAfterUpsert_skips_pid_reuse_via_identity_gate` |

`projection_test.go` 加 `PD1`-`PD9`：

| # | 名稱 |
|---|---|
| PD1 | `buildPaneProjection_dedup_excludes_proxy_claimed_standalone` |
| PD2 | `buildPaneProjection_fallback_when_all_frames_claimed` |
| PD3 | `buildPaneProjection_no_proxy_refs_unchanged_behavior` |
| PD4 | `dedup_keeps_claimed_standalone_with_native_subagents`（v9 Q1，**v10 R1 改寫**：claimed child 仍 hide，但 Subagents merge 進 projection；TopFrame = parent + Subagents 含 proxy ref + merged native ref） |
| PD5 | `dedup_still_hides_empty_standalone`（v9 Q1 boundary） |
| PD6 | `dedup_merges_hidden_stateful_child_subagents`（v10 R1：parent older, child newer with native, hide + merge）|
| PD7 | `dedup_merges_hidden_stateful_child_subagents_parent_newer`（v10 R1 對稱：parent newer, child older with native, merge 仍 run）|
| PD8 | `dedup_merge_avoids_duplicate_proxy_ref`（v10 R1 boundary：same proxy identity 兩側都有 → merge dedup 後只一個 entry）|
| PD9 | `cross_frame_native_id_collision_preserved`（v11 S1：cc 與 codex 各自的 native ref 同 ID 不同 Type，merge 不可誤砍 → 兩條 native ref + proxy ref 三條全留；驗 owner-aware dedup）|

### 3.3 不加的測試

- ❌ goroutine race detector — integration 順序模擬已足夠
- ❌ Phase 3 daemon_restart_recovery 路徑測試 — Phase 3 scope
- ❌ rollback retry exhaustion path（v3 有過，v4 已移除 rollback）
- ❌ partial_canonicalization trace 寫入測試（v4 已移除該 reason）

---

## 4. Sweep 兩 pass（canonicalize + prune）

### 4.1 Recovery bound 是 2s（v3 文檔修正）

`sweep.go:20` `sweepInterval = 2 * time.Second`（不是 1h）。`frameIdleThreshold = 1h` 是另一個概念（無 hook 活動的 frame 多久後刪），不是 sweep 頻率。

partial state 的最壞 user-visible 觀察延遲是「partial 發生 → 下次 sweep 跑」≤ 2s（且該觀察被 projection_dedup 隱藏，所以 SPA 永遠看不到）。

### 4.2 `canonicalizePane` pass

`internal/module/agent/sweep.go` `sweepOnce` 在現有 dead-frame + idle-timeout 兩 pass 後加第三 pass：

```go
func (m *Module) sweepOnce() error {
    // ... existing dead-frame + idle-timeout passes ...

    panes := uniquePaneIDsFromFrames(allFrames)
    broadcastTs := nowFn().UnixNano()
    for _, paneID := range panes {
        m.canonicalizePane(paneID, broadcastTs)
        m.pruneDeadProxyRefs(paneID, broadcastTs)
    }
    return nil
}
```

`canonicalizePane`：

```go
func (m *Module) canonicalizePane(paneID string, broadcastTs int64) {
    frames, err := m.frames.ListByPane(paneID)
    if err != nil {
        return
    }
    framesByPID := make(map[int]store.Frame, len(frames))
    for _, frame := range frames {
        framesByPID[frame.PID] = frame
    }
    for _, candidate := range frames {
        // v4 candidate identity gate (修 v3 H2 同類 bug)
        if !isPidAliveFn(candidate.PID) {
            continue
        }
        actualStart, sterr := processStartTimeFn(candidate.PID)
        if sterr != nil || actualStart != candidate.ProcessStartTime {
            continue
        }
        ancestor, found := m.findCanonicalAncestor(candidate, framesByPID)
        if !found {
            continue
        }
        ref := agentpkg.SubagentRef{
            ID:              fmt.Sprintf("proxy:%s:%d:%s", candidate.AgentType, candidate.PID, candidate.ProcessStartTime),
            Type:            candidate.AgentType,
            StartedAt:       broadcastTs,
            SourcePID:       candidate.PID,
            SourceStartTime: candidate.ProcessStartTime,
            IsProxy:         true,
        }
        attached, parentStored, aerr := m.attachProxyRefWithRetry(ancestor, ref, broadcastTs)
        if aerr != nil || !attached {
            continue
        }
        deleted, _ := m.frames.DeleteIfUnchanged(candidate.FrameID, candidate.LastSeenAt)
        if deleted {
            metricSweepCanonicalized.Add(1)
        }
        // not deleted: partial — next sweep tick (2s) tries again. No
        // rollback needed; projection_dedup hides; eventual consistent.
        _ = parentStored
    }
}

func (m *Module) findCanonicalAncestor(candidate store.Frame, framesByPID map[int]store.Frame) (store.Frame, bool) {
    info, err := readProcessInfoFn(candidate.PID)
    if err != nil {
        return store.Frame{}, false
    }
    ppid := info.PPID
    for depth := 0; depth < proxyMaxDepth; depth++ {
        if ppid <= 1 {
            return store.Frame{}, false
        }
        ancestor, ok := framesByPID[ppid]
        if ok {
            if ancestor.AgentType == candidate.AgentType {
                return store.Frame{}, false
            }
            if isPidAliveFn(ancestor.PID) {
                actualStart, sterr := processStartTimeFn(ancestor.PID)
                if sterr == nil && actualStart == ancestor.ProcessStartTime {
                    return ancestor, true
                }
            }
        }
        ancestorInfo, err := readProcessInfoFn(ppid)
        if err != nil {
            return store.Frame{}, false
        }
        if ancestorInfo.PPID == ppid {
            return store.Frame{}, false
        }
        ppid = ancestorInfo.PPID
    }
    return store.Frame{}, false
}
```

### 4.3 `pruneDeadProxyRefs` pass（v4 新，修 SessionEnd 漏網）

```go
// pruneDeadProxyRefs detaches IsProxy SubagentRefs whose source process
// is gone or has been replaced (PID reuse). Without this, a partial-state
// SessionEnd path that skipped the hot-path cleanup leaves a stale proxy
// ref permanently lit on the parent.
//
// v4 design: this is the SessionEnd defensive fallback. Hot path
// (§2.3) covers the common case; this covers all permutations including
// daemon crash mid-SessionEnd, hot-path removeProxyRefForSender failure,
// or proxies whose source died via signal without emitting SessionEnd.
func (m *Module) pruneDeadProxyRefs(paneID string, broadcastTs int64) {
    frames, err := m.frames.ListByPane(paneID)
    if err != nil {
        return
    }
    for _, frame := range frames {
        for _, ref := range frame.Subagents {
            if !ref.IsProxy {
                continue
            }
            if isPidAliveFn(ref.SourcePID) {
                actualStart, sterr := processStartTimeFn(ref.SourcePID)
                if sterr == nil && actualStart == ref.SourceStartTime {
                    continue  // proxy source still alive + identity match — keep
                }
            }
            // Source dead or PID reused — detach.
            detached, _, derr := m.detachProxyRefWithRetry(frame, ref.SourcePID, ref.SourceStartTime, broadcastTs)
            if derr == nil && detached {
                metricSweepPrunedProxy.Add(1)
            }
        }
    }
}
```

### 4.4 Sweep 與 hot-path 不衝突

兩層共用 `attachProxyRefWithRetry` / `detachProxyRefWithRetry` / `DeleteIfUnchanged` — 同一 RMW 路徑，`UpsertIfUnchanged` 在並發下 retry 即可。Sweep 不上鎖、不擋 hot-path、partial 由 dedup 在 projection 層隱藏。

---

## 5. 不做（明列）

- ❌ SQL transaction（Side A 建議；Side B 證據壓倒，標準對 ephemeral 表過高）
- ❌ per-pane mutex / pane-level claim table（違背 PR-2b 無 lock atomic RMW 哲學）
- ❌ SQL UNIQUE constraint on `(pane_id, agent_type)`（cc + codex proxy 同 pane 是合法）
- ❌ delay window / SessionStart deadline（hack，影響 hook latency）
- ❌ rollback proxy attach 在 hot path（v2/v3 加 → v4 移除；partial 為合法狀態）
- ❌ partial_canonicalization trace reason（v3 加 → v4 移除；無法消費）
- ❌ existing frame self-as-descendant reconcile（§2.2.2）
- ❌ 對 non-SessionStart event 觸發 hot-path canonicalization（IT8 守邊界）
- ❌ Metrics endpoint exposure（本 PR 內部 expvar；endpoint follow-up）
- ❌ SPA-side dedup（dedup 在 daemon projection 統一處理；SPA 無資訊做 dedup）
- ❌ 修 Phase 3 fallback chain race（Phase 3 PR #638 處理）
- ❌ rebuild Inspector / SPA 變更（Phase 5）

---

## 6. Commit 順序（TDD）

| # | Commit |
|---|---|
| 1 | `docs: Phase 3.5 plan v1 — cold-start proxy canonicalization` ✅ `bb382870` |
| 2 | `docs: Phase 3.5 plan v2 — bidirectional + retry/rollback` ✅ `dd29be46` |
| 3 | `docs: Phase 3.5 plan v3 — sweep canonicalize + identity gate` ✅ `148a2309` |
| 4 | `docs: Phase 3.5 plan v4 — Hybrid B+ (consulting-driven redesign)` ✅ `a338f6d3` |
| 5 | `docs: Phase 3.5 plan v5 — preserve live proxies + honest metrics` ✅ `285599e4` |
| 5b | `docs: Phase 3.5 plan v6 — filter-merge-retry (codex round 5 J1 fix)` ✅ `d47d367d` |
| 5c | `docs: Phase 3.5 plan v7 — ship gate IT1-IT20 + IT17 wording (codex round 6 K1 fix)` 此檔 |
| 6 | `feat(agent): metrics counters for phase 3.5 canonicalization observability` | metric 4 個 + import 註冊（無 endpoint） |
| 7 | `feat(agent): pidIsAncestorOfWithCap + canonicalizeDescendantsAfterUpsert with identity gate (unwired)` | helper + RC1-RC5 unit |
| 8 | `feat(agent): reconcileCreatedFrameAsProxy best-effort (unwired)` | reconcile helper（無 rollback）|
| 9 | `feat(agent): wire bidirectional canonicalization + filter-merge-retry into applyFrameEvent` | §2.2.1 + §2.2.2（**v6 filter-merge-retry**，取代 v5 三步 snapshot + reset + re-attach）+ IT17/IT18/IT19/IT20 |
| 10 | `feat(agent): SessionEnd hot-path proxy cleanup` | §2.3 + IT12 |
| 11 | `feat(agent): buildPaneProjection dedup proxy-claimed standalone frames` | §2.4 + PD1-PD3 + IT5 |
| 12 | `feat(agent): sweep canonicalizePane with candidate identity gate` | §4.2 + IT10 |
| 13 | `feat(agent): sweep pruneDeadProxyRefs` | §4.3 + IT13 / IT14 |
| 14 | `test(agent): integration tests for cold-start race canonicalization end-to-end` | IT1-IT11 + IT15 / IT16 用真 sqlite |

---

## 7. 行數預估

| 區塊 | 估計 |
|---|---|
| metrics.go 4 counter + import | ~25 行 |
| `frame_ops.go` reconcile（無 rollback） | ~50 行 |
| `frame_ops.go` canonicalizeDescendantsAfterUpsert（含 identity gate） | ~85 行 |
| `frame_ops.go` pidIsAncestorOfWithCap | ~20 行 |
| `frame_ops.go` SessionEnd proxy cleanup | ~10 行（既有 case 改 4 行）|
| `frame_ops.go` 接線 §2.2.1 + §2.2.2（v6 filter-merge-retry）| ~85 行 |
| `projection.go` dedup（含 v10 R1 hide-and-merge）| ~70 行 |
| `sweep.go` canonicalizePane + findCanonicalAncestor + pruneDeadProxyRefs（含 v10 R2 owner-read-error survivor）| ~155 行 |
| `frame_ops_test.go` RC1-RC5 unit | ~150 行 |
| `projection_test.go` PD1-PD8 | ~280 行 |
| `module_test.go` / new file IT1-IT22 + IT13d/IT14c integration | ~1000-1100 行 |
| `sweep_test.go` 加 sweep canonicalize/prune/owner-read-error 測試 | ~250 行 |
| Plan docs（此檔 v10） | ~1090 行 |
| **總 net code（不含 plan docs）** | **~1820-1920 行** |

v10 LOC 比 v9（~1790-1890）+30：projection.go +20 行（hide + merge collect），sweep.go +12 行（owner-read-error 改 survivor + 註解），新增 PD6/PD7/PD8 + IT13d/IT14c 測試 +200 行；PD4 改寫並非新增。

---

## 8. 驗收 / Ship 條件

- 所有 **IT1-IT22 + IT21b + IT21c + IT22b + IT22c + IT13/IT13b/IT13c/IT13d + IT14/IT14b/IT14c** + RC1-RC5 + PD1-PD9 + 既有測試全綠（IT17-IT22, IT21b/IT21c/IT22b/IT22c/IT13c/IT13d/IT14b/IT14c/PD4-PD9 為 race-fix regression guards 不可跳過 — codex round 6 K1 + round 2 v9 fix + round 3 v10 fix + round 4 v11 fix + round 5 v12 fix）
- `go build/vet/test ./...` 23 packages 全綠
- SPA 無變更
- 委派 codex round 2（v8）+ round 2 second wave（v9）+ round 3（v10）+ round 4（v11）+ round 5（v12）review 收斂；採納或合理 deferred all findings；round 5 為最後一輪（trend：round 4 / round 5 各 1 high finding；per `feedback_codex_review_termination.md` 進 ship）
- Phase 3 PR #638 merged 後 rebase Phase 3.5 到 main

---

## 9. 風險清單

| # | 風險 | 緩解 |
|---|---|---|
| R1 | Projection dedup 把不該 hide 的 frame hide 掉（false positive）| Identity 用 (PID, ProcessStartTime) 比對；只 hide standalone PID 等於 proxy 的 SourcePID — 同 process；理論上不可能 false positive |
| R2 | `pruneDeadProxyRefs` 把 alive 但 startTime 讀錯的 proxy 誤砍 | identity gate fail-safe：read error → skip（不砍）；只在 actualStart != ref.SourceStartTime 確定 mismatch 才砍 |
| R3 | Sweep canonicalize 走 ListByPane O(n) per pane per 2s | proxyMaxDepth=5 + pane frame 數一般 < 10；2s 一輪總成本 < 50ms；可接受 |
| R4 | ~~rebase Phase 3 後 applyFrameEvent 衝突~~ | ✅ 已解決：Phase 3 merged at `92fb5d05`（2026-04-26），Phase 3.5 worktree rebase 完成 docs commits 無衝突。3.5a 實作開工後接線位置（line 286 / 272 / 53）與 Phase 3 fallback chain（line ~228）區塊不同 — 預期無衝突 |
| R5 | metric 累加在熱 path 影響 latency | expvar.Int.Add 是 atomic，sub-microsecond；可忽略 |
| R6 | partial state 在 metric SLO 突破時要升級為強一致 | metric 已埋；超過 1/1000 的話開 follow-up phase 升級到 Side A SQL transaction |
| R7 | dedup 邏輯改 buildPaneProjection 影響既有 BuildSessionProjections / projectPane / projectionForSession 所有 caller | dedup 是純 frame filter；caller 看到的 SessionProjection 結構不變；既有測試（projection_test.go）守 regression |

---

## 10. Codex review focus 預期

施工後委派 codex round 4 review focus：

**第一輪（standard）**：
> Phase 3.5 v4 — Hybrid B+ design。三輪 review + 兩 codex consulting 後選 Side B（accept partial state + projection dedup + sweep eventual consistency）。重點審：
> 1. Projection dedup 是否會 hide 該顯示的 frame（false positive）— 特別當 proxy ref 自己是 stale 時
> 2. SessionEnd hot-path proxy cleanup 是否與既有 frame == nil path 重複觸發 removeProxyRefForSender
> 3. sweep `pruneDeadProxyRefs` identity gate 與 hot-path identity gate 的細節是否一致
> 4. partial 用 metric 替代 trace 的觀察性是否充分（無 SLO 累積機制）
> 5. v4 移除 rollback / partial trace / IT12 後是否仍 cover 所有 v1/v2/v3 修過的 scenario
> 6. fallback「all frames claimed」邊界是否真的 unreachable，還是 v4 還沒抓到

**第二輪（adversarial 三視角）**：
- **攻擊方**：projection dedup race（buildPaneProjection 與 attachProxyRefWithRetry 同時跑）；SessionEnd 與 sweep 同時 detach 同一 proxy 的競賽；metric counter 在 daemon restart 後 reset 對 SLO 的影響
- **防守方**：v4 與 v3 的 partial state 處理路線是真分層 vs 偽 patch 的判斷
- **檔案體質**：projection.go 62 → ~100 行 OK；frame_ops.go 752 → ~900 行；sweep.go 152 → ~290 行 — 三檔同步加大時是否該重組

---

## 11. Open questions

1. metrics endpoint exposure：本 PR 內部 expvar，外部 monitoring/debug 接 follow-up phase？採納
2. partial state SLO 累積觸發升級到 SQL tx 的閾值（v4 暫定 1/1000）— 是否該寫進 spec？暫不寫，metric 觀察期再決定
3. v4 `pruneDeadProxyRefs` 與 `idleSweep` 的責任邊界：dead-PID frame 由 idle sweep 清，dead-PID proxy 由 prune 清；是否該合併？採目前分開設計，責任清晰

---

## 12. 結束條件

PR-3.5a 跑過兩輪 codex review 收斂後：

1. Phase 3 PR #638 merge 到 main ✅ 完成（`92fb5d05`）
2. Rebase 本 PR 到 main ✅ 完成（docs-only rebase 無衝突）
3. 本 PR-3.5a merge 到 main（不 bump）
4. 開 bump PR alpha.226 + CHANGELOG（涵蓋 Phase 3 + Phase 3.5a）
5. Bump merge → main @ alpha.226
6. PR-3.5b 視 metric 決定 ship 時機（可延後）
7. ExitWorktree 清掉 `lights-phase-3-5` worktree（最後 PR 完成後）
8. 更新 kickoff_lights_rebuild.md：標 Phase 3 ✅ + Phase 3.5a/b 個別狀態

---

## 13. Codex review + consulting 軌跡

| Round | Job | Verdict | Findings | Resolution |
|---|---|---|---|---|
| Plan v1 round 1（adversarial）| inline | needs-attention | F1 ancestor-late / F2 partial delete / F3 mock test | v2 全採納 |
| Plan v2 round 2（adversarial）| inline | needs-attention | G1 rollback 也會失敗 / G2 descendant scan 缺 identity / G3 existing descendant 漏網 | v3 全採納 |
| Plan v3 round 3（adversarial）| inline | needs-attention | H1 rollback helper 同類 partial / H2 sweep 缺 identity / H3 partial trace 不會寫入 | **三輪 partial state meta-drift signal 觸發** → consulting |
| Side A consulting | task-moeupstr-z0oilf | (technical spec) | 提議 SQL transaction 根除 partial 物理層 | 不採納（標準對 ephemeral 表過高），但細節（BEGIN IMMEDIATE / busy code / FK）保留參考 |
| Side B consulting | task-moeuqbnn-jart97 | (adversarial argument) | 證據壓倒：sweep 2s 不是 1h / agent_frames 是 ephemeral / projection 是 user-visible 邊界 / SessionEnd 漏 cleanup proxy | **採納為 v4 核心**（Hybrid B+）|
| Plan v4 round 4（adversarial）| inline | needs-attention | I1 high existing-frame reset 清掉 live proxy / I2 medium SLO 不可量測 | v5 全採納（§2.2.2 preserve live proxies + IT17/IT18/IT19；§2.6 移除 SLO 聲明，改三 evidence point；§0.2 設計論證重整）|
| Plan v5 round 5（adversarial）| inline | needs-attention | J1 high snapshot/reset/re-attach 三步非原子，並發 attach 在 race window 內被抹除 | v6 採納（§2.2.2 改為 filter-merge-retry pattern，取代三步法；新 IT20 守規）|
| Plan v6 round 6（adversarial）| review-mof9wjzs-7q8wrs | needs-attention | K1 high §8 ship gate 漏列 IT17-IT20（含 J1 regression test）+ IT17 描述沿用 v5 已移除路徑 | v7 採納（純文檔修；§8 改 IT1-IT20 全綠 + IT17 描述更新）。**設計層面六輪後完整收斂 — round 6 無架構/race/邏輯 finding**；依 feedback_codex_review_termination.md 進實作 |
| PR-3.5a code review round 1（standard）| mofcixya-889fj5 | (review) | 標準 cross-model 二意見 | findings 併入 round 2 彙整 |
| PR-3.5a code review round 2（adversarial 三視角）| attack mofcj6pe-pxh09c / defend mofcjln6-f0hq8x / health mofcjerd-sm4p6m | needs-attention | M4 medium reconcile partial trace 反 projection / M3 high filter-merge 丟 native concurrent / M2 high descendant scan fold 有狀態 candidate / L1 high SessionEnd delete-first 留 orphan / N1 high dedup 信任 IsProxy（過嚴 deferred）| v8 採納 4/5（M4/M3/M2/L1 fix + sweep prune 從 PR-3.5b 移進 PR-3.5a）；N1 deferred 開 follow-up issue |
| PR-3.5a 第二輪 code review round 1（standard）| (round 2 standard) | (review) | 標準 cross-model 二意見（v8 patch 後）| findings 併入 round 2 adversarial 彙整 |
| PR-3.5a 第二輪 code review round 2（adversarial 三視角）| attack / defend / health（round 2）| needs-attention | O1 high filter-merge prevWrittenNativeIDs 多 retry 下 regress 丟 native / Q1 high projection dedup 隱藏 stateful child（partial+並發 SubagentStart）/ O2 medium descendant scan candidate guard 過寬把 stale-only IsProxy 算 state / O3 medium pruneDeadProxyRefs read-error 時 fail-destructive / P1 medium prune detach 後不 broadcast | **v9 全採納**：C13 initialNativeIDs baseline + IT21b / C14 dedup len==0 guard + PD4/PD5 / C15 candidate state classification + IT22b/IT22c / C16 prune fail-safe + IT14b / C17 broadcastProxyPruned + IT13c |
| PR-3.5a 第三輪 code review round 3 | (round 3) | needs-attention | R1 high projection dedup Q1 fix 仍丟資訊（child 留 visible 後 TopFrame 選一→另一邊 Subagents 丟）／ R2 medium sweepOnce owner-identity read error 時 bare continue 把 frame 丟出 survivors → pane 不進 prune → O3 fail-safe 被繞過 | **v10 全採納**：C18 dedup hide-and-merge + PD4 改寫 + PD6/PD7/PD8 / C19 owner-read-error 加入 survivors + IT13d/IT14c |
| PR-3.5a code review round 4（adversarial sanity）| review-mofh79kq-djlhr3 | needs-attention | S1 high R1 merge dedup 用 `subagentRefMatches`（native by ID-only），跨 frame 不同 agent family 的 native ID collision 被誤判 duplicate → 隱藏 child 的 native ref 從 wire 輸出消失 | **v11 採納**：merge 改 owner-aware inline dedup（proxy by `SourcePID+SourceStartTime`、native by `Type+ID`）；新增 PD9 守規。`subagentRefMatches` 語義不變（within-frame caller in `frame_ops.go` 不受影響）|
| PR-3.5a code review round 5（adversarial sanity）| review-mofi1dy2-ppe860 | needs-attention | T1 high SessionStart filter-merge `initialNativeIDs` baseline 用 ID-only 識別，並發 SubagentStart 同 ID 不同 StartedAt 被誤當 baseline drop → 使用者新 spawn subagent 靜默消失 | **v12 採納**：baseline 改 `(Type, ID, StartedAt)` 三元組；新增 IT21c 守規。**Round 5 為 PR-3.5a 最後一輪 review**（trend：round 4 → 5 各 1 high finding；per `feedback_codex_review_termination.md` convergence pattern 進 ship；不再 review）|

**PR-3.5a review 累計**：5 輪 standard + adversarial review；landed findings：v8 (4)+ v9 (5) + v10 (2) + v11 (1) + v12 (1) = 13 採納 / deferred follow-up：N1（hot-path liveness syscall 成本）+ IT12b 衍生 store interface abstraction 需求。Ship-ready @ v12。

---

## 14. Delivery plan：2-PR split（kickoff scope vs v7 規模對齊）

v7 規模相對 kickoff 原始設計擴張 ~7-10 倍（150-250 LOC → 1620-1720 LOC），單 PR review 負擔過大。設計分層天然可拆 — 依 **user-visible correctness 邊界** 切兩 PR：

### PR-3.5a「Cold-start race fix + projection dedup + SessionEnd cleanup + sweep prune」

**目標**：消除 user-visible incorrectness。SPA 永遠看到正確的 frame collapse（並發冷啟動下也是）；codex SessionEnd 不留 orphan lit dot；sweep eventual-consistency 兜底 SessionEnd 漏網場景。**獨立可 ship，無需依賴 PR-3.5b**。

**範圍**（commits 6/7/8/9/10/11/12 + v8 fix commits + v9 fix commits C13-C17）：

- `internal/agent/metrics.go`（新檔）— 4 個 expvar counter
- `internal/module/agent/frame_ops.go`：
  - `pidIsAncestorOfWithCap` helper
  - `canonicalizeDescendantsAfterUpsert`（含 identity gate **+ v9 O2 candidate state classification（native / live IsProxy / stale IsProxy 分類）**；舊 v8 M2 `len > 0` guard 已被 v9 取代）
  - `reconcileCreatedFrameAsProxy`（best-effort，無 rollback；**v8 M4 partial 回 canonicalized=true**）
  - applyFrameEvent §2.2.1 接線（new-frame post-Upsert）
  - applyFrameEvent §2.2.2 接線（existing-frame **filter-merge-retry + v9 O1 initialNativeIDs baseline**；舊 v8 M3 `prevWrittenNativeIDs` 機制已被 v9 取代）
  - applyFrameEvent SessionEnd **detach-first + propagate**（§2.3 + v8 L1）
- `internal/module/agent/projection.go`：`buildPaneProjection` dedup（§2.4）**+ v9 Q1 stateful-child guard（`len(frame.Subagents) == 0` 才 hide）**
- `internal/module/agent/sweep.go`：`pruneDeadProxyRefs` + sweepOnce 第三 pass（§4.3，**v8 L1 從 PR-3.5b 移進 PR-3.5a**）**+ v9 O3 fail-safe（read-error 時 keep）+ v9 P1 broadcastProxyPruned（detach 後 emit `sweep:proxy_pruned` 廣播）**

**測試**（必跑 ship gate）：

- Unit：RC1-RC5 + PD1-PD5（v9 加 PD4/PD5）
- Integration：IT1-IT9, IT11, IT12, IT13, IT13b, **IT13c**, IT14, **IT14b**, IT15-IT22, **IT21b**, **IT22b**, **IT22c**（**IT4 + IT5 + IT12 + IT13/IT14 + IT17-IT22 + v9 新增 IT21b/IT22b/IT22c/IT13c/IT14b 是 race-fix regression guards 不可跳**）

**LOC 預估**：~1300-1400 行 net（v9 比 v8 多 ~200 LOC：5 finding 修法 + IT13c/IT14b/IT21b/IT22b/IT22c/PD4/PD5）

**為什麼 SessionEnd cleanup + projection dedup 必須在 PR-3.5a 而不能延到 3.5b**：

- projection_dedup 處理「parent 含 proxy ref + child standalone」雙顯示，是 hot-path canonicalization 的 user-visible 必要兜底
- SessionEnd cleanup 處理「source 死後 parent 仍含 proxy ref → 永久 lit dot」 — projection_dedup 對此**無能為力**（沒有 standalone 可 hide），必須走 detach
- 兩者缺一，PR-3.5a 出貨就有 user-visible bug

### PR-3.5b「Sweep canonicalize layer」

**目標**：背景 eventual-consistency repair，補 PR-3.5a hot path 漏網的 partial state（DeleteIfUnchanged failure / existing-descendant 漏網等）。**Defense-in-depth；user-visible correctness 已由 3.5a 保證 + sweep prune 已在 3.5a**，3.5b 是穩健性升級。

**範圍**（commit 12 — sweep canonicalize；prune 在 v8 L1 修法中已移進 3.5a）：

- `internal/module/agent/sweep.go`：
  - `canonicalizePane`（含 candidate identity gate，§4.2）
  - `findCanonicalAncestor`
  - sweepOnce 加 canonicalize pass

**測試**：Integration IT10 + sweep-specific tests

**LOC 預估**：~150-250 行 net（v8 比 v7 PR-3.5b 少 ~100-150 LOC：prune + IT13/IT14 已移進 PR-3.5a）

**Ship 時機選擇**：
- 立即接 3.5a 後出貨（保守）
- 觀察 3.5a metric `partial_canonicalization_created` rate；如低於 SLO 可延後
- 不 ship 也是合法決策（3.5a 已 user-correct，3.5b 是優化）

### Release 策略（取代 §12）

| 階段 | 動作 | 狀態 |
|---|---|---|
| 1 | Phase 3 PR #638 squash merge（不 bump） | ✅ 完成（`92fb5d05`，2026-04-26）|
| 2 | Rebase Phase 3.5 worktree onto new main | ✅ 完成（8 docs commits replay 無衝突）|
| 3 | PR-3.5a TDD 實作 + push | ⏳ pending（subagent driven）|
| 4 | PR-3.5a 開 PR + 兩輪 codex review + merge（不 bump） | ⏳ pending |
| 5 | 開 bump PR **alpha.226**（涵蓋 Phase 3 + Phase 3.5a） | ⏳ pending |
| 6 | Bump merge → main @ alpha.226 | ⏳ pending |
| 7 | （依 metric 與優先級決定）開 **PR-3.5b** + review + merge → bump alpha.227 | optional |
| 8 | ExitWorktree 清 `lights-phase-3-5`（最後 PR 完成後） | optional |
| 9 | 更新 kickoff：標 Phase 3 + Phase 3.5（a/b 個別狀態） | pending |

**注意 alpha 編號變化**：原 plan §14 寫「bump alpha.225」是 v7 撰寫時 main 在 alpha.224；rebase 後發現 alpha.225 已被 parallel session（PR #643 SPA tooltip）占用，本 PR 系列改 bump alpha.226。此屬 main 並發前進的常態，依 `feedback_concurrent_session_safety.md` 不假設 branch 穩定。

### Branch 策略

`worktree-lights-phase-3-5` 同 worktree 連續開兩 PR：

- PR-3.5a branch：可沿用既有 `worktree-lights-phase-3-5`，head reset 到 v7 docs commit `cbd66c3c` 後直接做 PR-3.5a 實作 commits
- PR-3.5b branch：merged PR-3.5a 後，rebase fresh main，新 commits

或起新 branch `worktree-lights-phase-3-5b` for 3.5b — 兩種都可，視 PR-3.5a 實作完成時 main 進度而定。

### 對 v7 plan 的 §6/§7/§8 影響

§6 commits 表的對應 PR boundary：

| # | Commit | PR |
|---|---|---|
| 1-5c | docs commits（v1-v7）| 3.5a 帶 |
| 6 | metrics counters | 3.5a |
| 7 | pidIsAncestorOfWithCap + canonicalizeDescendantsAfterUpsert | 3.5a |
| 8 | reconcileCreatedFrameAsProxy | 3.5a |
| 9 | wire bidirectional + filter-merge-retry | 3.5a |
| 10 | SessionEnd hot-path proxy cleanup | 3.5a |
| 11 | buildPaneProjection dedup | 3.5a |
| 12 | sweep canonicalizePane | 3.5b |
| 13 | sweep pruneDeadProxyRefs | **3.5a**（v8 L1 fix；從 3.5b 移進）|
| 14 | integration tests | **拆**：IT1-9/11/12/13/13b/14/15-22 進 3.5a；IT10 進 3.5b |
| C8-C12 | v8 fix commits（M4/M3/M2/L1 + sweep prune）| 3.5a |

§8 ship gate 對應 PR：

- **PR-3.5a ship gate**：IT1-IT9, IT11, IT12, IT13, IT13b, IT14, IT15-IT22 + RC1-RC5 + PD1-PD3 全綠 + go build/vet/test 全綠 + SPA 無變更 + codex 兩輪 review 收斂
- **PR-3.5b ship gate**：IT10 + sweep-specific tests 全綠 + 不 regress 3.5a 既有測試

§7 LOC 預估維持（總和），但分 PR：
- 3.5a：~1100-1200 LOC + plan docs（v8 含 sweep prune + 5 IT 新增）
- 3.5b：~150-250 LOC（v8 只剩 canonicalizePane）
