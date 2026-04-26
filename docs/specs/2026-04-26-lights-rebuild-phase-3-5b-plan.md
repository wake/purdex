# Lights Rebuild — Phase 3.5b Plan v1

**Status**: Draft, pending codex review
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
- 對應 metric：`MetricSweepCanonicalized`（已宣告於 `internal/agent/metrics.go:35`，目前無 caller，3.5b 接上）
- 對應 broadcast：成功 attach + delete 後 emit `sweep:proxy_canonicalized` hook 廣播，讓 SPA + `m.subagents` / `m.currentStatus` 同步（pattern 仿 `pruneDeadProxyRefs` 的 `broadcastProxyPruned`）
- 測試：IT10 + 補擴展矩陣（見 §3）

### Out of scope

- ❌ 改 `pruneDeadProxyRefs`（PR-3.5a 已 ship-ready）
- ❌ 改 hot-path `reconcileCreatedFrameAsProxy` / `canonicalizeDescendantsAfterUpsert` / projection dedup（PR-3.5a 範圍）
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
| 1 | docs | 本 plan v1 docs commit |
| 2 | test | 加 IT10 + IT10b/c/d/e/f（純 candidate/ancestor identity gate path）；compile 失敗（caller 未實作）|
| 3 | feat | 實作 `findCanonicalAncestor`（IT10b-f + RC6-RC11 全綠；canonicalizePane 仍未實作 → IT10 仍紅）|
| 4 | feat | 實作 `canonicalizePane` 主 body（不含 broadcast）+ sweepOnce wire；IT10/IT10g/IT10h/IT10i/IT10l/IT10m 綠 |
| 5 | feat | 加 `broadcastProxyCanonicalized` + canonicalizePane call 它；IT10j/IT10k 綠 |
| 6 | docs | plan v2（如 codex review 有調整）|

> Commit 2-3 拆原因：identity gate 邏輯獨立（plan v12 §4.2 內 candidate gate 與 findCanonicalAncestor 各自有 gate），先把 ancestor walk 與 unit test 鎖定，再接 canonicalize 主 flow。

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
| IT10/IT10b-m + RC6-RC11 | 全綠 |
| MetricSweepCanonicalized | 觀察 +1 在預期 case |
| Codex review | 一輪標準 cross-model（3.5a 5 輪 review 已涵蓋 §4.2 設計，3.5b 為實作 + 測試擴展，一輪足夠 — 如 round 1 verdict !== clean 才 escalate）|

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

---

## 8. LOC 預估

| 區塊 | LOC |
|---|---|
| `canonicalizePane` body | ~50 |
| `findCanonicalAncestor` body | ~40 |
| `broadcastProxyCanonicalized` body | ~25 |
| sweepOnce wire（1 行 + 註解） | ~5 |
| Tests（IT10×13 + RC6-11×6） | ~400-500 |
| Plan docs（本檔） | ~400 |
| **Net code（不含 plan docs / tests）** | **~120** |
| **Net code + tests** | **~600** |

對齊 plan v12 §14 預估「~150-250 LOC net 不含 tests」— 落在範圍。

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
