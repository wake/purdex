# Lights Rebuild — Phase 3.5b Plan v3

**Status**: Draft, pending codex round 3
**Round 1**: needs-attention — high (canonicalizePane 漏 hasOwnedState 守衛 → child 自己的 native subagent state 被砍)
**Round 1 fix（v2）**: §2.2 加 candidate state classifier（mirror `canonicalizeDescendantsAfterUpsert` frame_ops.go:1098-1137）+ IT10n/IT10o/IT10p 新增驗證
**Round 2**: needs-attention — high (IT10n 漏掉 round 1 原始時序 — attach 成功 + DeleteIfUnchanged 失敗 + 後續 native SubagentStart 三步序列；ship gate §6 沒納入 v2 新增 IT/RC)
**Round 2 fix（v3）**: §3.1 IT10n 重寫為 round 1 原始時序組合 IT10m partial state（pre-existing parent proxy + child standalone with bumped LastSeenAt + native ref）；§6 ship gate 擴到 IT10/IT10b-p + RC6-RC16 全綠
**Round 1 verbatim** (`019dc937-beb0-7a83-a02b-42679ad4fbc1`):

> high — canonicalizePane can erase stateful child subagents during partial recovery (§2.2 lines 114-140). Plan §2.2 folds any live candidate with a canonical ancestor, then deletes the candidate row after attach. There is no candidate-owned-state guard before lines 136-140. This is unsafe for the exact partial state created when attach succeeds and DeleteIfUnchanged fails because the child was concurrently refreshed by SubagentStart: PR-3.5a projection currently hides that standalone child while merging its Subagents, but this sweep would attach only a proxy ref to the ancestor and then delete the child row, losing native child refs. Existing hot-path canonicalizeDescendantsAfterUpsert has an owned-state classification for this class of bug; the sweep plan does not carry it over, and IT10g/IT10m do not cover a stateful child with native refs.
> Recommendation: Add an owned-state check before attach/delete, mirroring the existing hot-path rule: skip candidates with native refs or live identity-verified IsProxy refs, allow only empty or stale-only proxy candidates. Alternatively, explicitly migrate candidate.Subagents into the ancestor with owner-aware dedup before DeleteIfUnchanged. Add an IT case for attach-success/delete-failure followed by a native SubagentStart on the child, proving sweep preserves the native ref after recovery.

**Round 2 verbatim** (`019dc93d-7f4e-7892-bdf6-45779388604f`):

> high — Round 1 data-loss regression is not actually gated by the v2 plan (§3.1 359-431). §3.1 的 IT10n 只預先放一個帶 native ref 的 standalone child，沒有同時建立 round 1 high 的關鍵 partial state：ancestor 已經有 child 的 proxy ref，child 因 DeleteIfUnchanged 失敗仍存在，接著 child 收到 native SubagentStart。這會驗到 candidateHasOwnedState 的基本 guard，但沒有證明 sweep 在 attach-success/delete-failure 的既有 parent proxy 狀態下保留 child native state。更嚴重的是 §6 ship gate 仍只要求 IT10/IT10b-m + RC6-RC11，漏掉 v2 新增的 IT10n/o/p 與 RC12-RC16；實作即使沒有跑 round 1 closure tests 也能照 plan 過 gate。
> Recommendation: 把 IT10n 改成組合 IT10m 的 already-proxied partial：cc.Subagents 先含 codex IsProxy，codex standalone 的 LastSeenAt 模擬被 SubagentStart bump 並含 native ref；sweep 後 assert codex row/native ref 保留、parent proxy 不重複、MetricSweepCanonicalized 不增。同步把 §6 ship gate 改為要求 IT10/IT10b-p + RC6-RC16 全綠。
**Scope**: Sweep canonicalize defense-in-depth layer
**Base**: `main` @ alpha.226 (`78ae7273`)
**Branch**: `worktree-lights-phase-3-5b`
**Predecessor**: PR-3.5a (#644) — see `2026-04-25-lights-rebuild-phase-3-5-plan.md` v12

---

## 0. 來龍去脈

PR-3.5a 已在 alpha.226 落地 cold-start race fix + projection dedup + SessionEnd cleanup + sweep `pruneDeadProxyRefs`。User-visible correctness 已保證；DB-level partial state 由 `pruneDeadProxyRefs` 在 2s 內 detach 死 ref。

但 PR-3.5a 沒處理一種 partial state：**standalone child frame 未被 fold 進 ancestor**。發生情境：

1. Hot-path `reconcileCreatedFrameAsProxy` 跑了 `attachProxyRefWithRetry` → 成功（parent 已含 proxy ref），但接著 `DeleteIfUnchanged(child)` 失敗（concurrent refresh 撞 LastSeenAt baseline）→ 留下 child standalone frame
2. Hot-path `canonicalizeDescendantsAfterUpsert` 跑時 child 的 PPID 鏈 readProcessInfoFn transient error → skip → child 留 standalone
3. SessionStart 進 existing-frame path（v12 §2.2.2），這條路徑**不**對自己跑 self-as-descendant reconcile（避免 reset 衝突），standalone descendant 留下不收

projection dedup 在這些情境**已經**可以 hide 給 SPA 看（PR-3.5a §2.4）— 用戶層無感知。但 DB 層 standalone frame 持續存在直到：
- child 自己 SessionEnd → frame 被 delete
- child PID 死 → sweep `pid_dead` clear
- 1h idle timeout → sweep `idle_timeout` clear
- 直到此之前，DB 真實 row 數比 user-visible 多 → expvar 觀察 `partial_canonicalization_created` 計數會持續累加

PR-3.5b = 補一個 sweep pass 在 2s 內把這種 standalone child frame fold 進 ancestor。

**Defense-in-depth**：user-visible correctness 已由 PR-3.5a 保證；3.5b 是 DB-level eventual consistency，讓 partial 不在 DB 層長期殘留。

---

## 1. Scope

### In scope

- `internal/module/agent/sweep.go`：
  - `canonicalizePane(paneID, broadcastTs)` — 新 func，掃 pane 的 standalone live frame，找到 cross-type live identity-verified ancestor，attach proxy ref + DeleteIfUnchanged 自身 standalone row
  - `findCanonicalAncestor(candidate, framesByPID)` — 新 helper，PPID 鏈走 `proxyMaxDepth` 找 cross-type ancestor frame
  - `sweepOnce` 第三 pass：在現有 `pruneDeadProxyRefs` 之前加 `canonicalizePane` call（順序：canonicalize → prune，讓新 attached ref 在同 tick 可被 prune 驗證 — 雖然剛 attach 的識別不會觸發 prune，但邏輯上 canonicalize 先建立的 ref 是 hot-path 模擬，由 prune 守衛）
- `internal/module/agent/frame_ops.go`：
  - **v2**：抽出 `candidateHasOwnedState(candidate store.Frame) bool` helper（既有 `canonicalizeDescendantsAfterUpsert` 內 in-line block 1098-1137 改 call helper；零行為變更）— 共享給 sweep canonicalizePane 用，編譯期保證 hot-path 與 sweep 兩處 owned-state 語意一致
- 對應 metric：`MetricSweepCanonicalized`（已宣告於 `internal/agent/metrics.go:35`，目前無 caller，3.5b 接上）
- 對應 broadcast：成功 attach + delete 後 emit `sweep:proxy_canonicalized` hook 廣播，讓 SPA + `m.subagents` / `m.currentStatus` 同步（pattern 仿 `pruneDeadProxyRefs` 的 `broadcastProxyPruned`）
- 測試：IT10 + 補擴展矩陣（見 §3）

### Out of scope

- ❌ 改 `pruneDeadProxyRefs`（PR-3.5a 已 ship-ready）
- ❌ 改 hot-path `reconcileCreatedFrameAsProxy` / `canonicalizeDescendantsAfterUpsert` 行為（v2 注：抽 helper 是 pure refactor 不算行為變更，hot-path 既有 test 是 baseline 守衛）
- ❌ 改 projection dedup（PR-3.5a 範圍）
- ❌ 新 store API（沿用既有 `ListByPane` / `DeleteIfUnchanged` / `attachProxyRefWithRetry`）
- ❌ 新 SubagentRef field
- ❌ 新 trace decision reason
- ❌ Phase 4 probe 補強

---

## 2. 設計

### 2.1 sweepOnce wire site

現有 `sweepOnce`（sweep.go:50-128）已在最後做 `pruneDeadProxyRefs` 的 per-pane loop。改成：

```go
panes := uniquePaneIDs(survivors)
broadcastTs := nowFn().UnixNano()
for _, paneID := range panes {
    m.canonicalizePane(paneID, broadcastTs)   // PR-3.5b 新加
    m.pruneDeadProxyRefs(paneID, broadcastTs) // PR-3.5a 既有
}
```

順序 rationale：

- canonicalize 先 → 建立合法 proxy ref + 刪 standalone child
- prune 後 → 對同 pane 的所有 ref 跑 dead/reused 檢查；剛 attach 的 ref 因為 source live + identity-verified 會被 prune 保留（identity gate 一致）
- 反過來（prune 先 / canonicalize 後）也可（兩 pass 各自 identity-gated 不衝突），但 canonicalize-先序更直覺：先把 partial 修成 canonical，再驗 canonical 健康

### 2.2 canonicalizePane

```go
// canonicalizePane folds standalone live cross-type child frames into
// their canonical ancestor proxy ref within the same pane. Defense-in-
// depth backstop for PR-3.5a hot-path canonicalization paths that left
// a partial state (DeleteIfUnchanged failure / readProcessInfoFn
// transient error / existing-frame SessionStart path that does not run
// self-as-descendant reconcile).
//
// User-visible correctness is already guaranteed by projection dedup
// (PR-3.5a §2.4) — this pass closes the DB-level eventual consistency
// loop in ≤ 2s instead of waiting for the child's own SessionEnd /
// pid_dead / 1h idle timeout.
//
// Single-direction rule (matches hot path): descendant becomes proxy of
// ancestor; ancestor is never reverted to descendant. Identity gate
// applied to BOTH candidate and ancestor — stale (PID-reused) frames
// never participate, leaving them for pid_reused cleanup elsewhere.
//
// Best-effort: any storage error / failed gate / failed Upsert / failed
// DeleteIfUnchanged makes this candidate skip; next sweep tick (2s)
// retries. No rollback on partial.
func (m *Module) canonicalizePane(paneID string, broadcastTs int64) {
    if m.frames == nil {
        return
    }
    frames, err := m.frames.ListByPane(paneID)
    if err != nil {
        return
    }
    framesByPID := make(map[int]store.Frame, len(frames))
    for _, frame := range frames {
        framesByPID[frame.PID] = frame
    }
    canonicalizedAny := false
    var anyAncestor store.Frame
    for _, candidate := range frames {
        // Candidate identity gate — stale (dead PID / PID-reuse) frames
        // skip canonicalize; pid_dead / pid_reused passes handle them.
        if !isPidAliveFn(candidate.PID) {
            continue
        }
        actualStart, sterr := processStartTimeFn(candidate.PID)
        if sterr != nil || actualStart != candidate.ProcessStartTime {
            continue
        }
        // v2 round-1 fix — protect candidates that own LIVE state.
        // Mirrors hot-path canonicalizeDescendantsAfterUpsert
        // hasOwnedState classifier (frame_ops.go:1098-1137):
        //   - any native ref (IsProxy=false) → owned (real subagent state)
        //   - any live identity-verified IsProxy ref → owned
        //   - read-error during identity check → defensive treat as owned
        //   - empty Subagents OR only stale (dead/PID-reused) IsProxy
        //     refs → safe to fold; stale refs would be reaped by sweep
        //     prune anyway, and dropping them with the row is equivalent.
        //
        // Without this guard, sweep would erase native refs accumulated
        // by SubagentStart events arriving on the child between hot-path
        // attach and a failed DeleteIfUnchanged — projection_dedup merges
        // them into parent's projection at read time, but only because
        // the child row still exists. Deleting the row loses them.
        if candidateHasOwnedState(candidate) {
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
        if !deleted {
            // Partial — concurrent refresh / hot-path won the race. Next
            // sweep tick re-evaluates. Projection dedup already hides
            // this for SPA. No rollback.
            continue
        }
        agentpkg.MetricSweepCanonicalized.Add(1)
        canonicalizedAny = true
        anyAncestor = parentStored
    }
    if canonicalizedAny {
        m.broadcastProxyCanonicalized(anyAncestor)
    }
}
```

設計細節：

1. **Candidate identity gate**：dead PID / PID-reuse → skip。理由：candidate 不健康時不應該被 attach 成 proxy ref；交給 `pid_dead` / `pid_reused` 主 sweep pass 清掉本體。
2. **Ancestor identity gate（在 `findCanonicalAncestor` 內）**：ancestor 也必須 live + identity-verified。理由：避免把 child 收編到一個 stale parent，造成新一輪 partial。
3. **Same-type skip**：candidate.AgentType == ancestor.AgentType → not a valid proxy relationship（cc 之上不該掛 cc proxy；cross-type 才有意義，e.g. cc ancestor + codex descendant）。
4. **DeleteIfUnchanged failure → partial（不 rollback）**：hot path 同樣語意；下次 tick 再試；projection dedup 期間隱藏。
5. **Broadcast**：per-pane 而非 per-attach（與 `broadcastProxyPruned` granularity 對稱）；多個 ref 同 pane 收編合併成一次廣播。

### 2.2.1 candidateHasOwnedState (v2 新)

```go
// candidateHasOwnedState classifies whether a frame carries live state
// that must be preserved during canonicalization. Returns true when the
// frame has any native (IsProxy=false) subagent ref OR any live
// identity-verified IsProxy ref. Returns true conservatively on a
// processStartTime read error to avoid dropping state on uncertainty.
//
// Used by both hot-path canonicalizeDescendantsAfterUpsert and sweep
// canonicalizePane to decide whether folding a candidate as a proxy
// ref of an ancestor (and deleting its row) is safe.
//
// A candidate carrying ONLY stale (dead PID / PID-reused) IsProxy refs
// is considered free of owned state — those refs would be reaped by
// pruneDeadProxyRefs anyway, so dropping them with the row is
// equivalent to letting prune detach them later.
func candidateHasOwnedState(candidate store.Frame) bool {
    for _, ref := range candidate.Subagents {
        if !ref.IsProxy {
            return true // native ref → real owned state
        }
        if !isPidAliveFn(ref.SourcePID) {
            continue // stale (dead PID); reapable
        }
        actualStart, sterr := processStartTimeFn(ref.SourcePID)
        if sterr != nil {
            return true // read error → defensive
        }
        if actualStart != ref.SourceStartTime {
            continue // PID reuse; stale
        }
        return true // live + identity-verified IsProxy → owned
    }
    return false
}
```

**Scope decision — duplicate vs extract**:

PR-3.5a 的 `canonicalizeDescendantsAfterUpsert` 把同樣邏輯內聯在 frame_ops.go:1098-1137。本 plan 選擇**抽出共享 helper**（而非另寫一份），理由：

1. 兩處語意必須完全一致（任何 drift 都會造成 hot-path 與 sweep partial-state 收斂行為不同 → debug 噩夢）。共享 helper 用編譯期保證一致。
2. 抽 helper 是 pure refactor（無行為變更）— 既有 hot-path test 既能保護重構，也能驗證 helper。
3. helper 體積 ~25 行；不增加新 abstraction，只是命名一個既有概念。
4. 違反 §1 「out of scope: 改 hot-path canonicalize」？— 嚴格說屬重構（move-and-rename 既有 in-line block）；以下兩個保護降低風險：
   - hot-path test 是綠色 baseline；本 commit 只做名字搬家不改邏輯
   - 抽出後 hot-path call site `if hasOwnedState { ... }` 改成 `if candidateHasOwnedState(candidate) { continue }`，diff 一小塊明顯
5. 替代方案（duplicate）成本：~25 LOC 重複 + 未來 round 維護兩份 + 隱性同步義務 — net-negative。

**Where to place**：`frame_ops.go` 既有 `pidIsAncestorOfWithCap` helper 在 1040+ 已建立 helper section pattern；接著加 `candidateHasOwnedState` 為下一個 helper。`canonicalizeDescendantsAfterUpsert` 既有 in-line block 改 call helper。

### 2.3 findCanonicalAncestor

```go
// findCanonicalAncestor walks descendant's PPID chain looking for a
// cross-type frame in the same pane that is live and identity-verified.
// Caps walk at proxyMaxDepth (5) to bound syscall cost. Returns the
// frame and true on success.
//
// Returns false when:
//   - readProcessInfoFn errors mid-walk (transient — next sweep tick retries)
//   - PPID hits init (<=1) or self-loop (PPID == current PID)
//   - depth exhausted without finding a frame in the pane
//   - the matched ancestor is same-type as candidate (not a proxy relationship)
//   - the matched ancestor fails identity gate (dead / PID-reused)
//
// Note: framesByPID is keyed by PID, not (PID, paneID), but caller
// passes only frames from a single pane (ListByPane), so cross-pane
// collision is impossible.
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
                // Same-type — not a proxy relationship. Caller skips
                // (returning false here is conservative; a same-type
                // ancestor in chain means we're "inside" the same agent
                // tree and won't find a different cross-type ancestor
                // higher up either, by design).
                return store.Frame{}, false
            }
            if isPidAliveFn(ancestor.PID) {
                actualStart, sterr := processStartTimeFn(ancestor.PID)
                if sterr == nil && actualStart == ancestor.ProcessStartTime {
                    return ancestor, true
                }
            }
            // Ancestor matched in pane but failed identity gate — keep
            // walking; a deeper ancestor might still match.
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

### 2.4 broadcastProxyCanonicalized

對稱 `broadcastProxyPruned` (sweep.go:227)：

```go
// broadcastProxyCanonicalized emits a "hook" broadcast with reason=
// sweep:proxy_canonicalized after canonicalizePane attached at least one
// proxy ref + deleted at least one standalone child in a pane. Mirrors
// broadcastProxyPruned so SPA + m.subagents / m.currentStatus stay in
// sync without waiting for an unrelated hook.
//
// Best-effort: errors swallowed (next sweep tick re-evaluates). Single
// reference frame param picks any owner that successfully attached this
// tick — projection rebuild does not depend on which.
func (m *Module) broadcastProxyCanonicalized(reference store.Frame) {
    // Implementation pattern mirrors broadcastProxyPruned: project pane,
    // build NormalizedEvent with reason=sweep:proxy_canonicalized, push
    // through dispatchHook... (verify exact wiring against existing
    // broadcastProxyPruned at sweep.go:227 during impl).
}
```

> **Implementation note**：`broadcastProxyPruned` 完整 body 在 sweep.go:218+，TDD 階段 read 一次照 pattern 寫 `broadcastProxyCanonicalized`。可能可以抽 helper（`broadcastSweepHook(reference, reason string)`），但避免在 3.5b 拓展 scope — 先 duplicate，commit 結束附 simplifier 做共通化。

### 2.5 不新增 trace reason

Plan v12 §2.5 已決定 sweep canonicalize 不寫獨立 trace decision（消費端無法區別 hot-path vs sweep canonicalization，broadcast reason 已足夠）。3.5b 沿用此決策。

### 2.6 Wire compatibility

無 schema 變更；無新 SubagentRef field；無 SPA 變更；無新 store API。

---

## 3. 測試矩陣

新測試一律加進 `internal/module/agent/sweep_test.go`（PR-3.5a 已建檔），與 `pruneDeadProxyRefs` 既有測試同檔案。

### 3.1 Integration（必要 ship gate）

| # | 名稱 | Sequence | 預期 final state |
|---|---|---|---|
| **IT10** | `sweep_canonicalizes_standalone_descendant_into_ancestor` | (a) cc frame；(b) codex standalone frame（race 留下，PPID 鏈通到 cc PID）；(c) sweepOnce 跑一輪 | cc.Subagents 含 codex IsProxy ref；codex standalone 已刪；MetricSweepCanonicalized +1 |
| **IT10b** | `sweep_canonicalize_skips_pid_reuse_candidate` | codex standalone 但 actualStart != ProcessStartTime（PID 重用）；cc 健康；sweep 跑 | candidate 被 candidate identity gate skip；cc.Subagents 不變；codex standalone 留給 pid_reused pass；MetricSweepCanonicalized 不變 |
| **IT10c** | `sweep_canonicalize_skips_dead_candidate` | codex standalone 但 PID 已死；cc 健康；sweep 跑 | candidate gate skip；cc.Subagents 不變；codex standalone 由 pid_dead pass 清；MetricSweepCanonicalized 不變 |
| **IT10d** | `sweep_canonicalize_skips_when_ancestor_dead` | codex standalone live + identity-verified；cc frame 在 framesByPID 但 cc.PID 已死；sweep | candidate 跑 findCanonicalAncestor → ancestor identity gate skip → candidate 不被 fold；MetricSweepCanonicalized 不變；cc 由 pid_dead pass 清 |
| **IT10e** | `sweep_canonicalize_skips_same_type_ancestor` | 兩個 cc standalone（race 留下，後者 PPID 鏈到前者）；sweep | findCanonicalAncestor 命中 same-type → return false；不 fold；MetricSweepCanonicalized 不變（這是 plan §4.2 設計：same-type 不是 proxy 關係）|
| **IT10f** | `sweep_canonicalize_skips_when_no_ancestor_in_pane` | codex standalone live；pane 內無其他 frame（PPID 鏈不指向任何 frame.PID）；sweep | findCanonicalAncestor 走完鏈無 match → return false；codex frame 留存；MetricSweepCanonicalized 不變 |
| **IT10g** | `sweep_canonicalize_partial_when_delete_unchanged_fails` | (a) cc frame；(b) codex standalone；(c) mock `DeleteIfUnchanged` 對該 codex frame 回 deleted=false（concurrent refresh 撞 LastSeenAt baseline）；sweep | cc.Subagents 已含 codex IsProxy ref（attach 成功）；codex standalone 仍在；MetricSweepCanonicalized **不增加**（per plan：成功 = attach + delete 兩者皆成）；下次 tick 再試 |
| **IT10h** | `sweep_canonicalize_partial_when_attach_fails` | (a) cc frame；(b) codex standalone；(c) mock `attachProxyRefWithRetry` 回 attached=false（retry 耗盡）；sweep | cc.Subagents 不含 codex；codex standalone 仍在；MetricSweepCanonicalized 不變；下次 tick 再試 |
| **IT10i** | `sweep_canonicalize_then_prune_same_tick` | (a) cc frame.Subagents 含 stale codex IsProxy ref（SourcePID 已死）；(b) opencode standalone live + PPID 通到 cc；sweep 跑一輪 | canonicalize 先：cc.Subagents 加 opencode IsProxy；opencode standalone 刪。prune 後：cc.Subagents 移除 stale codex IsProxy。最終 cc.Subagents = [opencode IsProxy]；MetricSweepCanonicalized +1 + MetricSweepPrunedProxy +1 |
| **IT10j** | `sweep_canonicalize_emits_broadcast_when_any_succeeded` | 同 IT10 setup；驗 broadcast 通道有收到 reason=`sweep:proxy_canonicalized` 一次 | broadcast 觀察通道 +1（per-pane，不是 per-attach）|
| **IT10k** | `sweep_canonicalize_no_broadcast_when_nothing_succeeded` | 全部 candidate skip / partial；sweep | 無 `sweep:proxy_canonicalized` broadcast |
| **IT10l** | `sweep_canonicalize_cross_pane_isolation` | pane A 有 standalone codex 可 fold 進 cc；pane B 有 standalone codex 但 ancestor 不在；sweep | pane A canonicalize 成功；pane B 不影響；MetricSweepCanonicalized +1 |
| **IT10m** | `sweep_canonicalize_skips_already_proxied_candidate` | codex 既有 standalone frame（live）但同時 cc.Subagents 已含 codex IsProxy ref（hot-path 半成品 — attach 成 + delete 失敗的歷史 partial）；sweep | findCanonicalAncestor 找到 cc → attach 重試 → `attachProxyRefWithRetry` 內 RMW 看到既有 ref（同 SourcePID + SourceStartTime + IsProxy）需要驗：mutateSubagentsWithRetry 對 SubagentStart 用 updateSubagents replace by match → 不重複 → DeleteIfUnchanged 嘗試刪 standalone → 成 → cc.Subagents 仍只有一條 codex IsProxy；MetricSweepCanonicalized +1（這是 PR-3.5a partial recovery 的核心場景）|
| **IT10n** (v3 重寫) | `sweep_canonicalize_preserves_child_native_after_partial_recovery` | **Round 1 high 原始時序的精準回歸 guard**：(a) cc frame；(b) codex standalone live + identity-verified（PR-3.5a hot-path attach 成功後的 partial）；(c) cc.Subagents 已含 codex IsProxy ref（hot-path 的 successful attach）；(d) codex frame.LastSeenAt 被後續 SubagentStart 事件 bump 過（DeleteIfUnchanged failed 的原因）；(e) codex.Subagents 含 native task SubagentStart ref（IsProxy=false）；(f) sweep 跑 | candidate hasOwnedState=true（native ref 觸發）→ skip；codex standalone 列保留；codex.Subagents 的 native ref 保留；cc.Subagents 仍只一條 codex IsProxy（不重複 attach）；**MetricSweepCanonicalized 不變**（gated case 不算 progress）；projection dedup 在後續 read 時繼續 hide codex standalone 並 merge native ref 進 cc projection（PR-3.5a 既有行為延續）|
| **IT10o** (v2) | `sweep_canonicalize_folds_candidate_with_only_stale_proxy` | codex standalone live + identity-verified；codex.Subagents 含 IsProxy ref 但 SourcePID 已死（stale）；sweep | candidate hasOwnedState=false（stale-only proxy 不算 owned）→ fold；codex standalone 已刪；cc.Subagents 含 codex IsProxy；stale proxy 跟 codex frame 一起消失（與 sweep prune 等效）；MetricSweepCanonicalized +1 |
| **IT10p** (v2) | `sweep_canonicalize_skips_candidate_with_live_proxy` | codex standalone；codex.Subagents 含一個 IsProxy ref（SourcePID 對應某 opencode live 進程，identity verified — 表示 codex 自己當 ancestor 收編了 opencode）；sweep | candidate hasOwnedState=true（live IsProxy）→ skip；保 codex 自己作為某 sub-tree 的 ancestor 角色不被砍 |

### 3.2 Unit

`sweep_test.go` 加 `RC6`-`RC9`：

| # | 名稱 |
|---|---|
| **RC6** | `findCanonicalAncestor_walks_to_cross_type_match` |
| **RC7** | `findCanonicalAncestor_returns_false_on_same_type_immediate_match` |
| **RC8** | `findCanonicalAncestor_returns_false_on_dead_ancestor_identity_gate` |
| **RC9** | `findCanonicalAncestor_returns_false_on_depth_exhaustion` |
| **RC10** | `findCanonicalAncestor_returns_false_on_loop_detection_ppid_eq_pid` |
| **RC11** | `findCanonicalAncestor_returns_false_on_readProcessInfo_transient_error` |
| **RC12** (v2) | `candidateHasOwnedState_native_ref_returns_true` |
| **RC13** (v2) | `candidateHasOwnedState_live_proxy_returns_true` |
| **RC14** (v2) | `candidateHasOwnedState_only_stale_proxy_returns_false` |
| **RC15** (v2) | `candidateHasOwnedState_proxy_read_error_treats_as_owned` |
| **RC16** (v2) | `candidateHasOwnedState_empty_subagents_returns_false` |

### 3.3 不加的測試

- ❌ Goroutine race detector — IT 順序模擬 + identity gate 一致性已足
- ❌ Sweep tick interval 變動測試（sweepInterval 是 PR-3.5a constant，不在 3.5b scope）
- ❌ Phase 3 Prober 互動測試（Phase 3 scope）
- ❌ projection dedup 路徑（PR-3.5a 已涵蓋；3.5b 不變更 projection）

### 3.4 Test scaffolding 預期

- 3.5a 既有的 `sweep_test.go` 已建立 `pruneDeadProxyRefs` 測試 + mock 注入機制（`isPidAliveFn` / `processStartTimeFn` / `readProcessInfoFn`）。沿用。
- 預期需要 mock seam 注入 `attachProxyRefWithRetry` / `DeleteIfUnchanged` 的失敗路徑（IT10g, IT10h）。檢查現有 sweep_test.go 看是否已有 helper；若無，加 minimum mock seam（不引入新 store interface — 透過 fake `framesStore` 實作既有 method 即可）。

---

## 4. Commit 順序（TDD）

每個 commit 獨立、test-first、可獨立 review。

| # | Type | 說明 |
|---|---|---|
| 1 | docs | plan v1 → v2（v2 含 round 1 high finding 修法）|
| 2 | refactor | 抽 `candidateHasOwnedState` 從 `canonicalizeDescendantsAfterUpsert` in-line block 變共享 helper（pure refactor，0 行為變更；hot-path 既有 test 是 baseline 守衛）+ RC12-RC16 unit |
| 3 | test | 加 IT10 + IT10b/c/d/e/f + IT10n/o/p（identity gate + owned-state path）；compile 失敗（caller 未實作）|
| 4 | feat | 實作 `findCanonicalAncestor`（RC6-RC11 全綠；IT10 系列仍紅 — canonicalizePane 未實作）|
| 5 | feat | 實作 `canonicalizePane` 主 body（不含 broadcast）+ sweepOnce wire；IT10/IT10b-i/IT10l/IT10m/IT10n/IT10o/IT10p 綠 |
| 6 | feat | 加 `broadcastProxyCanonicalized` + canonicalizePane call 它；IT10j/IT10k 綠 |
| 7 | docs | plan v3（如 codex round 2 有 finding 才補）|

> **v2 Commit 順序變動**：原 v1 commit 2-5 拆 5 步，v2 把 helper 抽出 commit 提到最前（commit 2）— 理由：helper 抽出是 pure refactor 與 hot-path 既有 test 一起維持綠；後續 commit 3-6 才碰 sweep 新 surface area。每個 commit 仍可獨立 build / test。

---

## 5. 不做（明列 boundary）

- ❌ 改 `pruneDeadProxyRefs` 行為
- ❌ 改 hot-path canonicalize / reconcile 路徑
- ❌ 新 SubagentRef 欄位
- ❌ 新 trace decision reason（plan v12 §2.5 決策延用）
- ❌ projection layer 變更
- ❌ `frameIdleThreshold` / `sweepInterval` 調整
- ❌ Phase 4 probe 補強

---

## 6. Ship gate

| 檢查 | 要求 |
|---|---|
| `go build ./... && go vet ./... && go test ./...` | 全綠 |
| 23 packages 總體 | 0 regression |
| **IT10 + IT10b-IT10p（共 16 條 integration tests）** | **全綠**（v3 round 2 fix — 含 round 1 closure regression guard IT10n）|
| **RC6-RC16（共 11 條 unit tests）** | **全綠**（v3 round 2 fix — 含 candidateHasOwnedState 完整覆蓋 RC12-RC16）|
| MetricSweepCanonicalized | 觀察 +1 在預期 case；IT10g/IT10h/IT10n 確認**不**增（partial / gated case）|
| MetricSweepPrunedProxy | 既有 PR-3.5a 行為不 regress |
| Codex review | round 1 (high → fixed v2) + round 2 (high → fixed v3) → round 3 verdict 是 clean / nit-only 即可 ship；如再有 high/medium 繼續迭代直到收斂（per `feedback_codex_review_termination.md`）|

---

## 7. Risk

| Risk | 機率 | 緩解 |
|---|---|---|
| 同 sweep tick canonicalize 與 prune 互踩（先 attach 的 ref 立即被 prune）| **低** | identity gate 一致 — canonicalize 剛 attach 的 ref 是 live + identity-verified；prune 識別為「healthy proxy」會跳過。IT10i 守 |
| `framesByPID` map 依 PID 但 cross-pane PID 重複（OS PID 不保證 pane-unique）| **不存在** | `ListByPane` 限制 frames 同一 pane；map cross-pane collision 不可能 |
| `findCanonicalAncestor` 同 PID 出現兩次的 PPID loop（除 self-loop 外的長 loop）| **低** | depth cap = 5；self-loop 偵測；其他長 loop 在 depth 內自然耗盡 |
| canonicalizePane 與 hot-path `reconcileCreatedFrameAsProxy` 並發 → 雙方都試 attach 同 ref | **可控** | `mutateSubagentsWithRetry` 內 RMW 對 same-identity ref 是 idempotent（match 就 replace 不重複）；雙方 DeleteIfUnchanged 對同 frame 一邊成功一邊失敗 — 都符合既有語意，partial 由下一 tick 修復 |
| broadcast 風暴（每 2s 一次）| **低** | 只在「成功 attach + delete 至少一次」才 broadcast；穩態下 partial 為 0，broadcast 為 0 |
| 新 helper `broadcastProxyCanonicalized` 與 `broadcastProxyPruned` 重複| **預期** | 先 duplicate（避免 3.5b 拓展 scope），commit 結束附 simplifier 做 helper 抽出。或 codex review 建議共通化再做 |
| **v2 新增**：sweep 砍掉 child 自己的 native subagent state（codex round 1 high）| **已修** | §2.2.1 抽 `candidateHasOwnedState` 共享 helper；hot-path 與 sweep 編譯期保證語意一致；IT10n/IT10o/IT10p 三個 IT 守 |
| **v2 新增**：抽 helper 改動 hot-path 既有檔案（違反「out of scope: 改 hot-path」）| **可控** | pure refactor，0 行為變更；hot-path 既有 test（PR-3.5a R2 #O2 specifically tests stale-only proxy fold + native ref skip）是 baseline 守衛；diff 是「移動命名」可一目了然 |

---

## 8. LOC 預估

| 區塊 | LOC |
|---|---|
| `candidateHasOwnedState` 抽 helper（v2，淨 0 行為變更，純命名搬家）| ~25（其中 ~20 是 docstring/comment）|
| `canonicalizePane` body | ~55（v2 +5 一行 helper call + 註解）|
| `findCanonicalAncestor` body | ~40 |
| `broadcastProxyCanonicalized` body | ~25 |
| sweepOnce wire（1 行 + 註解） | ~5 |
| Tests（IT10×16 + RC6-RC16×11）| ~600-700 |
| Plan docs（本檔 v2） | ~520 |
| **Net code（不含 plan docs / tests）** | **~150** |
| **Net code + tests** | **~800** |

對齊 plan v12 §14 預估「~150-250 LOC net 不含 tests」— 落在範圍 lower bound。

---

## 9. Codex review focus 預期

一輪標準 review，重點：

1. canonicalize → prune 順序在同 tick 是否 sound（IT10i 是核心）
2. `findCanonicalAncestor` same-type early return 是否過於保守（會不會漏 fold 跨 same-type 中介的 cross-type 深 ancestor）
3. broadcast per-pane granularity 是否與 SPA 端 m.subagents / m.currentStatus 同步邏輯相容
4. partial state（attach 成 + delete 失敗）的 metric 計數是否誤導（IT10g 設計選擇）
5. RC6-RC11 unit 測試 coverage 是否漏邊角

---

## 10. 結束條件

- ship gate（§6）全綠
- codex round 1 review 收斂（無 critical/high/medium）
- main rebase clean
- bump alpha.227（PR-3.5b 單獨 bump，與 3.5a 切開以便 metric 觀察）

---

## 11. 文獻

- `2026-04-25-lights-rebuild-phase-3-5-plan.md` v12 — 5 輪 codex review，§4.2 設計母本
- `2026-04-23-lights-rebuild-spec.md` — Phase 3.5 整體 spec
- PR #644 — PR-3.5a merged at `fdac21c9`
- `kickoff_lights_rebuild.md` — Hybrid B+ 設計典範
- `feedback_codex_review_termination.md` — 一輪 review 終止條件
