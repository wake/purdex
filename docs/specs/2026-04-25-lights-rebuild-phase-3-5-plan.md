# Phase 3.5 Plan — Cold-start Proxy Canonicalization (v5 / Hybrid B+)

Baseline：`1.0.0-alpha.224`（main @ `75b4d166`）。
Worktree：`.claude/worktrees/lights-phase-3-5`（branch `worktree-lights-phase-3-5`）。
Branch base：`origin/main`（與 Phase 3 PR #638 並行；merge 順序 Phase 3 → rebase 3.5 → 一次 bump alpha.225）。

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

不變：獨立 PR；rebase 順序 Phase 3 → Phase 3.5；衝突點在 `applyFrameEvent` 不同區塊。

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

#### 2.2.2 Existing frame 路徑（line 256-272 之後）

**v5 I1 fix**：reset 前 snapshot 仍 live identity-verified IsProxy refs，reset 後 re-attach。reset 的原意是「old session's native subagent refs no longer apply」，但 cross-type IsProxy refs 指向同 pane 內仍 live 的 OS process — 不該在 parent's session-restart 時失蹤（descendant 沒 standalone 可補回）。

```go
// v5 I1: snapshot live IsProxy refs BEFORE reset. The store-level reset
// clears subagents_json wholesale; live cross-type proxies pointing to OS
// processes that still exist must survive the parent's SessionStart
// restart. Without this, an already-canonicalized proxy ref from a prior
// race window vanishes from projection — descendant scan can't recover
// it because the standalone child frame was deleted at canonicalize time.
var preservedProxies []agentpkg.SubagentRef
if req.EventName == "SessionStart" && frame != nil {
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
        preservedProxies = append(preservedProxies, ref)
    }
}

if req.EventName == "SessionStart" {
    updateErr = m.frames.UpdateHookPathAndResetSubagents(updated)
} else {
    updateErr = m.frames.UpdateHookPath(updated)
}
if updateErr != nil { return nil, FrameTraceMeta{}, updateErr }

reloaded, err := m.frames.GetByIdentity(req.TmuxPaneID, req.SenderPID, req.SenderStartTime)
if err != nil { return nil, FrameTraceMeta{}, err }
if reloaded == nil { return nil, FrameTraceMeta{}, nil }
stored = *reloaded

// v5 I1: re-attach preserved proxies via existing helper (atomic retry,
// merge-aware via updateSubagents). StartedAt refreshed to broadcastTs to
// reflect that they survived this SessionStart cycle.
for _, ref := range preservedProxies {
    ref.StartedAt = broadcastTs
    attached, parentStored, aerr := m.attachProxyRefWithRetry(stored, ref, broadcastTs)
    if aerr != nil { return nil, FrameTraceMeta{}, aerr }
    if attached {
        stored = parentStored
    }
}

// Phase 3.5: descendant scan (catches any cold-start race window children
// whose SessionStart raced with this existing-frame's SessionStart).
if req.EventName == "SessionStart" {
    updated, cerr := m.canonicalizeDescendantsAfterUpsert(stored, broadcastTs)
    if cerr != nil {
        return nil, FrameTraceMeta{}, cerr
    }
    stored = updated
}
```

**為什麼 re-attach 而不是「reset 不清 IsProxy」**：

- store-level `UpdateHookPathAndResetSubagents` 是 narrow update 設計（PR-2b R8）— 改它的語意（conditional reset）會把 store 層拉進 IsProxy semantics，違反 narrow update 哲學
- 在 module 接線層做 snapshot + re-attach 保留 store-level cleanliness；attachProxyRefWithRetry 的 RMW retry 邏輯也直接重用

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
| IT17 | `existing_frame_session_start_preserves_live_proxy_refs` (v5 I1) | (a) cc frame 已存在且 cc.Subagents 含 codex proxy ref（live + identity verified）；(b) cc 又收 SessionStart（existing path）→ pre-reset snapshot + reset + re-attach；(c) 之後 codex 進程仍 alive，無新 SessionStart | cc.Subagents 仍含 codex proxy ref（StartedAt 刷新到本次 broadcastTs）；projection dedup 也仍工作 |
| IT18 | `existing_frame_session_start_skips_dead_proxy_during_reset` (v5 I1 negative) | (a) cc frame 已存在含 codex proxy ref，但 codex 進程已死（isPidAliveFn = false）；(b) cc SessionStart | reset 後 cc.Subagents 不含該 dead proxy ref（preserve 不通過 identity gate）|
| IT19 | `existing_frame_session_start_skips_pid_reused_proxy` (v5 I1 negative) | (a) cc frame 含 codex proxy；(b) codex PID 已被 OS reuse（actualStart != ref.SourceStartTime）；(c) cc SessionStart | reset 後不 preserve 該 stale proxy（identity 不過 gate）|

### 3.2 Unit 測試

`frame_ops_test.go` 加 `RC1`-`RC5`：

| # | 名稱 |
|---|---|
| RC1 | `pidIsAncestorOfWithCap_depth_exhaustion` |
| RC2 | `pidIsAncestorOfWithCap_loop_detection_ppid_eq_pid` |
| RC3 | `pidIsAncestorOfWithCap_process_info_error` |
| RC4 | `canonicalizeDescendantsAfterUpsert_skips_same_type` |
| RC5 | `canonicalizeDescendantsAfterUpsert_skips_pid_reuse_via_identity_gate` |

`projection_test.go` 加 `PD1`-`PD3`：

| # | 名稱 |
|---|---|
| PD1 | `buildPaneProjection_dedup_excludes_proxy_claimed_standalone` |
| PD2 | `buildPaneProjection_fallback_when_all_frames_claimed` |
| PD3 | `buildPaneProjection_no_proxy_refs_unchanged_behavior` |

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
| 5 | `docs: Phase 3.5 plan v5 — preserve live proxies + honest metrics` 此檔 |
| 6 | `feat(agent): metrics counters for phase 3.5 canonicalization observability` | metric 4 個 + import 註冊（無 endpoint） |
| 7 | `feat(agent): pidIsAncestorOfWithCap + canonicalizeDescendantsAfterUpsert with identity gate (unwired)` | helper + RC1-RC5 unit |
| 8 | `feat(agent): reconcileCreatedFrameAsProxy best-effort (unwired)` | reconcile helper（無 rollback）|
| 9 | `feat(agent): wire bidirectional canonicalization + preserve live proxies into applyFrameEvent` | §2.2.1 + §2.2.2（含 v5 I1 preserve）+ IT17/IT18/IT19 |
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
| `frame_ops.go` 接線 §2.2.1 + §2.2.2（含 v5 preserve live proxies）| ~75 行 |
| `projection.go` dedup | ~40 行 |
| `sweep.go` canonicalizePane + findCanonicalAncestor + pruneDeadProxyRefs | ~140 行 |
| `frame_ops_test.go` RC1-RC5 unit | ~150 行 |
| `projection_test.go` PD1-PD3 | ~80 行 |
| `module_test.go` / new file IT1-IT19 integration | ~800-900 行 |
| `sweep_test.go` 加 sweep canonicalize/prune 測試 | ~120 行 |
| Plan docs（此檔 v5） | ~870 行 |
| **總 net code（不含 plan docs）** | **~1595-1695 行** |

v5 LOC 比 v4（~1460-1560）+135：preserve live proxies wiring（~35 行）+ IT17/IT18/IT19（~100 行）。但複雜度仍比 v3 低（單一 best-effort 路徑 + projection dedup boundary，不是 retry/rollback/propagate maze）。

---

## 8. 驗收 / Ship 條件

- 所有 IT1-IT16 + RC1-RC5 + PD1-PD3 + 既有測試全綠
- `go build/vet/test ./...` 23 packages 全綠
- SPA 無變更
- 委派 codex round 4 review 收斂；採納或合理 deferred all findings
- Phase 3 PR #638 merged 後 rebase Phase 3.5 到 main

---

## 9. 風險清單

| # | 風險 | 緩解 |
|---|---|---|
| R1 | Projection dedup 把不該 hide 的 frame hide 掉（false positive）| Identity 用 (PID, ProcessStartTime) 比對；只 hide standalone PID 等於 proxy 的 SourcePID — 同 process；理論上不可能 false positive |
| R2 | `pruneDeadProxyRefs` 把 alive 但 startTime 讀錯的 proxy 誤砍 | identity gate fail-safe：read error → skip（不砍）；只在 actualStart != ref.SourceStartTime 確定 mismatch 才砍 |
| R3 | Sweep canonicalize 走 ListByPane O(n) per pane per 2s | proxyMaxDepth=5 + pane frame 數一般 < 10；2s 一輪總成本 < 50ms；可接受 |
| R4 | rebase Phase 3 後 applyFrameEvent 衝突 | Phase 3 改 fallback chain（line 220 區塊），Phase 3.5 改 new-frame Upsert 後 + existing-frame Update 後 + SessionEnd（line 286 + 272 + 53）；位置不同；極端衝突手動解 |
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

PR 跑過 codex round 4 review 收斂後：

1. Phase 3 PR #638 merge 到 main（不 bump）
2. Rebase 本 PR 到 main（解 applyFrameEvent + SessionEnd 衝突）
3. 本 PR merge 到 main（不 bump）
4. 開 bump PR alpha.225 + CHANGELOG
5. Bump merge → main @ alpha.225
6. ExitWorktree 清掉 `lights-phase-3-5` worktree
7. 更新 kickoff_lights_rebuild.md：標 Phase 3 + Phase 3.5 ✅ at alpha.225

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
| Plan v5 round 5 | (待執行) | (pending) | — | — |
