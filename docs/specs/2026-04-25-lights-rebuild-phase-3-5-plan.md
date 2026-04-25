# Phase 3.5 Plan — Cold-start Proxy Canonicalization (v2)

Baseline：`1.0.0-alpha.224`（main @ `75b4d166`）。
Worktree：`.claude/worktrees/lights-phase-3-5`（branch `worktree-lights-phase-3-5`）。
Branch base：`origin/main`（與 Phase 3 PR #638 並行；merge 順序 Phase 3 → rebase 3.5 → 一次 bump alpha.225）。

**v1 → v2 由 codex adversarial round 1 收斂**（job 在 §13；3 個 high/medium findings 全採納）：
- F1 high — ancestor-late interleaving（單向 reconcile 不夠）→ 升 **bidirectional canonicalization**（§2.1.2）
- F2 high — DeleteIfUnchanged 失敗造成 SPA 雙顯示 → 加 **bounded retry + rollback**（§2.1.1）
- F3 medium — mock 測試掩蓋 race → integration test 從 optional 升 **必要 ship gate**（§3.1）

---

## 0. 來龍去脈（必讀）

### 0.1 修什麼

**Race 來源**：Phase 2 PR-2b 的 `findProxyParent`（`internal/module/agent/frame_ops.go:688`）只做 *pre-Upsert* PPID 走訪，無 post-Upsert 收斂。當兩個跨型別 SessionStart 在同一 pane 並發冷啟動（典型場景：daemon restart 後 cc + codex proxy 同時來），兩邊 walk 各自 miss → 各自 `Upsert` → 結果是兩個獨立 frame，PR-2b 的 proxy collapse 失效。

**最容易觸發的場景**（Phase 3 codex round 2 finding #1 提出）：daemon restart → cc 與 codex proxy 同時冷啟動。Phase 3 加上 `tryRebuildFromProcessTree` 後 race window 變大，但 race 本身在 alpha.221 PR-2b 落地起就存在。

### 0.2 設計來源 + v1→v2 演進

Codex architectural consulting `task-modcbbhg-auxa36`（high-effort）採方案 **B'：post-Upsert canonicalization by ancestry**，捨棄 per-pane mutex / pane-level claim table / SQL UNIQUE / delay window 等 hack。

**v1 設計**：descendant SessionStart Upsert 後做 reconcile（往上 walk 找 ancestor 收編自己）。

**v1 漏洞**（codex F1）：descendant 先到 → walk miss → Upsert → reconcile 仍 miss（ancestor 還沒 Upsert）→ ancestor 後到 → 依「ancestor 不可轉 descendant」規則不會收編 descendant → 兩個 frame 永久殘留。

**v2 修正**：Bidirectional canonicalization。SessionStart 做兩個動作（不衝突 secondary race，因為遵守相同單向規則）：

| Side | 動作 | Walk 方向 |
|---|---|---|
| Self-as-descendant（reconcile） | 找 cross-type ancestor，attach 自己為 ancestor 的 proxy + delete 自己 | 從 self 往上 walk PPID |
| Self-as-ancestor（descendant scan） | 掃同 pane 其他 standalone frames，PPID 鏈經過 self 且跨型別的 → attach 為 self 的 proxy + delete 該 frame | 從每個 candidate 往上 walk PPID 找 self.PID |

兩個動作都遵守單向規則（ancestor 收編 descendant，descendant 變 proxy，從不反向），不會引入 secondary race。例：cc + codex 並發冷啟動：

| 順序 | descendant reconcile | ancestor descendant-scan | 結果 |
|---|---|---|---|
| descendant first（codex Upsert，cc 還沒到） | codex walk no ancestor → no-op | （cc 還沒 SessionStart） | codex standalone |
| ↓ cc Upsert 後 | (cc 沒 ancestor，self-reconcile no-op) | cc scan 找到 codex（PPID 鏈經過 cc）→ attach + delete codex | 1 cc frame + codex proxy ✓ |
| ancestor first（cc Upsert，codex 還沒到） | cc walk no ancestor → no-op | cc scan，pane 內無其他 frame → no-op | cc standalone |
| ↓ codex Upsert 後 | codex walk 找到 cc → attach + delete codex | (codex 沒 descendant，scan 無動作) | 1 cc frame + codex proxy ✓ |
| 完全並發（混合 Upsert）| 兩邊都做：先到的 reconcile 可能 miss，後到的 reconcile 找到 ancestor + scan 收編 race window 內進來的 standalone | 兜底所有 ordering | final 1 cc frame ✓ |

### 0.3 與 Phase 3 的關係

- **獨立但相關**：race 本身不依賴 Phase 3 的 `daemon_restart_recovery` 程式碼，而是 PR-2b（alpha.221）就存在的舊洞
- **接線位置同檔**：Phase 3 與 Phase 3.5 都動 `applyFrameEvent`（前者加 fallback chain rebuild，後者加 post-Upsert + post-Update reconcile）— rebase 時可能在 fallback chain 區段衝突
- **獨立 PR**：依 kickoff 「切 Phase 3.5 獨立 PR」決策；Phase 3 PR #638 merge 後 rebase Phase 3.5

---

## 1. 既有原語（沿用，無新 store API）

PR-2b 已備齊所有需要的原語：

| 原語 | 位置 | 用途 |
|---|---|---|
| `findProxyParent(req)` | `frame_ops.go:688` | PPID 鏈走訪 + same-pane + live + identity-verified + cross-type 篩選；reconcile 直接重用 |
| `attachProxyRefWithRetry(parent, ref, broadcastTs)` | `frame_ops.go:631` | optimistic concurrency attach |
| `detachProxyRefWithRetry(owner, senderPID, senderStartTime, broadcastTs)` | `frame_ops.go:640` | optimistic concurrency detach（**v2 新用於 rollback**）|
| `FramesStore.DeleteIfUnchanged(frameID, lastSeenAt)` | `internal/store/frames.go:263` | atomic delete |
| `FramesStore.ListByPane(paneID)` | 既有 | descendant scan 用 |
| `FramesStore.GetByIdentity(paneID, pid, startTime)` | 既有 | 重 reload child for retry |
| `SubagentRef{...}` | `internal/agent/subagent.go` | proxy ref 型別（PR-2a）|
| Proxy ID 格式 `proxy:%s:%d:%s` | `frame_ops.go:176` 既有 | reconcile 必須沿用同格式 |

**沒有**新 store method、沒有新型別、沒有新 SQL。

---

## 2. 設計細節

### 2.1 Helper 二件

#### 2.1.1 `reconcileCreatedFrameAsProxy`（self-as-descendant）

```go
const reconcileDeleteMaxAttempts = 3

// reconcileCreatedFrameAsProxy attempts to canonicalize a freshly-created
// SessionStart frame against an alive cross-type ancestor. If found, attaches
// proxy ref to ancestor + deletes self's standalone frame.
//
// Bounded retry on delete (v2 F2): if DeleteIfUnchanged returns deleted=false
// (concurrent writer touched child row, e.g. probe status update), reload
// child + retry up to reconcileDeleteMaxAttempts. If all retries fail, ROLLBACK
// the proxy attach via detachProxyRefWithRetry — final state is consistent
// (parent has no proxy + child still standalone) rather than the inconsistent
// (parent has proxy + child still standalone) double-display.
//
// Returns (canonicalized, parentStored, err):
//   - canonicalized=true with parentStored populated when attach + delete both
//     succeeded.
//   - canonicalized=false, zeroFrame, nil when no ancestor found, parent
//     vanished mid-attach, or delete-retry exhausted and rollback succeeded.
//   - non-nil error only for storage failures.
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
    // Bounded delete retry with reload: child's last_seen_at could have
    // moved if probe status update / SubagentStart hit between attach and
    // delete. Reload via GetByIdentity each retry.
    childPaneID := stored.PaneID
    childPID := stored.PID
    childStartTime := stored.ProcessStartTime
    expectedLastSeen := stored.LastSeenAt
    for attempt := 0; attempt < reconcileDeleteMaxAttempts; attempt++ {
        deleted, derr := m.frames.DeleteIfUnchanged(stored.FrameID, expectedLastSeen)
        if derr != nil {
            return false, store.Frame{}, derr
        }
        if deleted {
            return true, parentStored, nil
        }
        reloaded, rerr := m.frames.GetByIdentity(childPaneID, childPID, childStartTime)
        if rerr != nil {
            return false, store.Frame{}, rerr
        }
        if reloaded == nil {
            // Already gone (concurrent SessionEnd / sweep). Treat as success.
            return true, parentStored, nil
        }
        expectedLastSeen = reloaded.LastSeenAt
    }
    // All retries failed: rollback the proxy attach so we don't leave the
    // inconsistent (parent has proxy + child still standalone) state visible
    // to SPA. After rollback, child is canonical standalone (will be picked
    // up by ancestor-side descendant scan on next SessionStart, or by sweep).
    rollback, _, rerr := m.detachProxyRefWithRetry(parentStored, req.SenderPID, req.SenderStartTime, broadcastTs)
    if rerr != nil {
        return false, store.Frame{}, rerr
    }
    if !rollback {
        // Detach also lost (extreme: parent vanished between attach and
        // detach). Final state: parent gone (sweep cleared) or its proxy
        // already removed by another writer; child standalone — also
        // consistent. Fall through to canonicalized=false trace.
    }
    log.Printf("phase3.5: reconcile delete-retry exhausted for child frame %s; rolled back proxy attach on parent %s", stored.FrameID, parentStored.FrameID)
    return false, store.Frame{}, nil
}
```

#### 2.1.2 `canonicalizeDescendantsAfterUpsert`（self-as-ancestor — v2 新增）

```go
// canonicalizeDescendantsAfterUpsert scans the pane for standalone frames
// whose PPID chain passes through self.PID and folds them as proxy refs on
// self, deleting their standalone rows. Closes the ancestor-late race window
// (codex F1): descendant's pre-Upsert walk + post-Upsert reconcile both miss
// because ancestor wasn't in the store yet — when ancestor's SessionStart
// finally arrives, descendant scan picks them up.
//
// Direction-safe: only attaches descendants to self (single-direction rule);
// never converts self into someone's descendant. Symmetric with reconcile-as-
// descendant — together they bracket all cold-start interleavings.
//
// Per-candidate failures (attach race lost / delete race lost / individual
// PPID walk error) are logged and skipped; canonicalization is best-effort
// and idempotent across SessionStart cycles. Storage errors abort the scan.
func (m *Module) canonicalizeDescendantsAfterUpsert(
    self store.Frame,
    broadcastTs int64,
) error {
    if m.frames == nil {
        return nil
    }
    frames, err := m.frames.ListByPane(self.PaneID)
    if err != nil {
        return err
    }
    for _, candidate := range frames {
        if candidate.FrameID == self.FrameID {
            continue
        }
        if candidate.AgentType == self.AgentType {
            // Same-type sibling: not a proxy candidate per PR-2b semantics.
            continue
        }
        if !pidIsAncestorOfWithCap(candidate.PID, self.PID, proxyMaxDepth) {
            continue
        }
        // Identity verify candidate is alive (else it's stale and will be
        // swept; not worth proxy'ing a dead frame onto self).
        if !isPidAliveFn(candidate.PID) {
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
        attached, parentStored, aerr := m.attachProxyRefWithRetry(self, ref, broadcastTs)
        if aerr != nil {
            return aerr
        }
        if !attached {
            // Self vanished (extreme); abort scan — caller's projection will
            // refresh on next ListByPane anyway.
            return nil
        }
        // Bounded delete retry mirrors §2.1.1; on failure rollback this single
        // proxy ref. Don't abort the whole scan — other candidates may still
        // canonicalize cleanly.
        if !m.deleteWithRetryOrRollback(candidate, parentStored, broadcastTs) {
            log.Printf("phase3.5: descendant scan rolled back proxy %s on parent %s (delete race lost)", ref.ID, self.FrameID)
            continue
        }
        // Refresh self for next iteration (subagents list grew).
        self = parentStored
    }
    return nil
}

// pidIsAncestorOfWithCap walks descendant's PPID chain (capped at depth)
// looking for ancestorPID. Returns true on hit, false on miss / depth
// exhaustion / process info error / loop detection (PPID == PID).
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

// deleteWithRetryOrRollback factors the bounded delete + rollback pattern
// shared by reconcile and descendant-scan. Returns true on successful delete
// (or already-gone), false on rolled-back attach (caller should treat the
// proxy ref as not-applied).
func (m *Module) deleteWithRetryOrRollback(
    candidate store.Frame,
    parent store.Frame,
    broadcastTs int64,
) bool {
    expectedLastSeen := candidate.LastSeenAt
    for attempt := 0; attempt < reconcileDeleteMaxAttempts; attempt++ {
        deleted, err := m.frames.DeleteIfUnchanged(candidate.FrameID, expectedLastSeen)
        if err != nil {
            return false
        }
        if deleted {
            return true
        }
        reloaded, rerr := m.frames.GetByIdentity(candidate.PaneID, candidate.PID, candidate.ProcessStartTime)
        if rerr != nil || reloaded == nil {
            return reloaded == nil  // gone is success
        }
        expectedLastSeen = reloaded.LastSeenAt
    }
    rollback, _, _ := m.detachProxyRefWithRetry(parent, candidate.PID, candidate.ProcessStartTime, broadcastTs)
    _ = rollback  // even if rollback also lost, final state is "no proxy + child standalone" once concurrent writers settle
    return false
}
```

**為什麼 walk 設計仍 reuse `findProxyParent`**：reconcile-as-descendant 走 sender 的 PPID 鏈，跟 PR-2b 既有 `findProxyParent` 完全同型。重用避免兩套 walk drift。

**為什麼 descendant scan 不重用 `findProxyParent`**：descendant scan 的 walk 起點是 *candidate*（standalone frame）的 PID，目標是看 PPID 鏈是否經過 self。findProxyParent 的目標是「找 ancestor」 — 語義不同。新抽 `pidIsAncestorOfWithCap` 是專注的 yes/no 查詢，比硬塞 findProxyParent 加旗標更乾淨。

### 2.2 接線（applyFrameEvent）

兩個 wiring point — SessionStart 命中 frame == nil（新建）與 frame != nil（既有 reset）。

#### 2.2.1 New frame 路徑（line 286-292 之後）

```go
} else {
    stored, err = m.frames.Upsert(store.Frame{...})
    if err != nil {
        return nil, FrameTraceMeta{}, err
    }

    // Phase 3.5: bidirectional canonicalization.
    if req.EventName == "SessionStart" {
        // Self-as-descendant: try collapse into ancestor.
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
        // Self-as-ancestor: try collapse standalone descendants into self.
        if cerr := m.canonicalizeDescendantsAfterUpsert(stored, broadcastTs); cerr != nil {
            return nil, FrameTraceMeta{}, cerr
        }
        // Reload self in case descendant scan attached refs.
        if reloaded, rerr := m.frames.GetByIdentity(req.TmuxPaneID, req.SenderPID, req.SenderStartTime); rerr == nil && reloaded != nil {
            stored = *reloaded
        }
    }
}
```

#### 2.2.2 Existing frame 路徑（line 256-272 之後）

```go
if req.EventName == "SessionStart" {
    updateErr = m.frames.UpdateHookPathAndResetSubagents(updated)
} else {
    updateErr = m.frames.UpdateHookPath(updated)
}
if updateErr != nil {
    return nil, FrameTraceMeta{}, updateErr
}
reloaded, err := m.frames.GetByIdentity(req.TmuxPaneID, req.SenderPID, req.SenderStartTime)
if err != nil {
    return nil, FrameTraceMeta{}, err
}
if reloaded == nil {
    return nil, FrameTraceMeta{}, nil
}
stored = *reloaded

// Phase 3.5: existing-frame SessionStart also runs descendant-scan. The
// race window here: this frame existed (e.g. cc was running before daemon
// restart, cc's frame survived rebuild via Phase 3 tryRebuildFromProcessTree)
// while a concurrent SessionStart from a descendant landed standalone
// because the rebuild raced. Reset already cleared subagents, so attach
// scan is safe.
//
// Note: self-as-descendant reconcile is NOT run here. An existing frame
// is already a stable identity; collapsing it post-hoc into another
// ancestor would orphan the user's session. Only descendant-scan applies.
if req.EventName == "SessionStart" {
    if cerr := m.canonicalizeDescendantsAfterUpsert(stored, broadcastTs); cerr != nil {
        return nil, FrameTraceMeta{}, cerr
    }
    if reloaded2, rerr := m.frames.GetByIdentity(req.TmuxPaneID, req.SenderPID, req.SenderStartTime); rerr == nil && reloaded2 != nil {
        stored = *reloaded2
    }
}
```

**為什麼 existing frame 不做 self-as-descendant**：existing frame 走進這條路徑時，frame 已是 stable 識別（先前的 SessionStart 建立）。再 collapse 進 ancestor 會把使用者已認知的 session 突然變成另一個 frame 的 sub-dot，UX 撕裂。
**為什麼 existing frame 仍做 descendant-scan**：daemon restart 後 Phase 3 `tryRebuildFromProcessTree` 可能把 ancestor frame 重建成 existing，若 race window 期間 descendant 已 standalone，這是唯一收編機會。

### 2.3 Trace decision 詞彙

| 決策 | Reason | 觸發 |
|---|---|---|
| `updated_frame` | `proxy_subagent_attached` | PR-2b pre-Upsert proxy fast-path（既有，不改）|
| `created_frame` | `parent_frame_found` / `parent_frame_missing` | 一般 new frame 路徑（既有，不改）|
| `updated_frame` | `post_upsert_canonicalization_self` | **v2 新**，self 變 proxy 收編成功 |
| 沿用原 trace | 原 reason | descendant scan 不改 trace（scan 是 side effect of attaching descendants；主決策仍是「建/更新此 frame」）|

descendant scan 不改 trace 的原因：scan 把其他 frame 收編進來，但本 SessionStart 的主語義仍是「建立或更新 self」 — trace 應反映 self 視角。被收編的 descendant 留在 reconcile-as-descendant 那條路徑的 trace（前一次 SessionStart 留下的 created_frame trace）。

### 2.4 Wire compatibility

不改 NormalizedEvent / SubagentRef / projection wire schema。SPA 看到的最終結果：parent.subagents 列表多一個 IsProxy=true 的 ref，跟 PR-2b 既有 proxy attach 完全同型。

---

## 3. 測試矩陣

### 3.1 Integration 測試（**必要 ship gate** — v2 升級自 optional）

新增 `internal/module/agent/canonicalize_test.go`（或加進 `module_test.go` 末尾），用真 sqlite store + 真 process info mock + 真 sequence ordering，覆蓋下列 scenarios。

**Test harness**：沿用 `module_test.go` 的 `newTestModule(t)` pattern + `readProcessInfoFn` global injection（PR-2b 既有），不開 goroutine。每個 case 直接呼 `m.applyFrameEvent` 兩到三次模擬不同到達順序。

| # | 名稱 | Sequence | 預期 final state |
|---|---|---|---|
| IT1 | `descendant_then_ancestor_canonicalizes_via_descendant_scan` | (a) codex SessionStart applyFrameEvent — pane 內無 frame → standalone codex frame；(b) cc SessionStart applyFrameEvent — pane 內有 codex standalone → cc Upsert + descendant scan 找到 codex（PPID 鏈經過 cc）→ attach + delete codex | 1 cc frame，cc.Subagents 含 codex proxy ref；codex frame 已刪 |
| IT2 | `ancestor_then_descendant_canonicalizes_via_self_reconcile` | (a) cc SessionStart — standalone cc；(b) codex SessionStart — pre-Upsert findProxyParent 找到 cc → 走 PR-2b fast-path attach + 不建 codex frame **(this is the existing PR-2b path, sanity check)** | 1 cc frame，cc.Subagents 含 codex proxy ref |
| IT3 | `concurrent_with_descendant_pre_walk_miss_reconcile_hits` | (a) codex applyFrameEvent — mock `findProxyParent` pre-walk 第 1 次 call 回 nil（cc 未入庫）→ codex Upsert standalone；模擬 cc 同時 Upsert 完成；codex 內 reconcile 跑 post-Upsert findProxyParent 第 2 次 call 回 cc → attach codex 為 cc proxy + delete codex | 1 cc frame + codex proxy ref |
| IT4 | `concurrent_descendant_post_reconcile_also_misses_recovered_by_ancestor_scan` | (a) codex applyFrameEvent，mock 兩次 findProxyParent 都 miss（cc Upsert 真的還沒到）→ codex standalone 殘留；(b) cc applyFrameEvent → cc Upsert + descendant scan 收編 codex | 1 cc frame + codex proxy ref（cover ancestor-late race，F1 fix 的核心驗證） |
| IT5 | `delete_race_lost_then_rollback_keeps_consistency` | mock `DeleteIfUnchanged` 持續回 deleted=false（child last_seen_at 持續變動，模擬 probe status update）；reconcile 走 3 次 retry 後 rollback proxy ref | parent 不含 codex proxy ref（rollback 成功）；codex frame 仍 standalone；trace decision = `created_frame`（沒升級 canonicalization）；無雙顯示資料 |
| IT6 | `descendant_scan_partial_failure_skips_one_continues_others` | pane 內有兩個 standalone descendants（codex + opencode）；mock `DeleteIfUnchanged` 對 codex 持續失敗、對 opencode 成功；cc SessionStart descendant scan | cc.Subagents 含 opencode proxy ref（成功）；codex 仍 standalone（rollback）；scan 不因 codex 失敗中止 |
| IT7 | `existing_frame_session_start_runs_descendant_scan_only` | (a) cc SessionStart 建 frame；(b) codex 在 race window 內 standalone 進場；(c) cc SessionStart 又來一次（reset，frame != nil 路徑）→ UpdateHookPathAndResetSubagents 清 subagents（PR-2b 既有 `[]`）+ descendant scan 重新收編 codex | cc.Subagents 含 codex proxy ref（reset 後又 re-collapse） |
| IT8 | `non_session_start_event_does_not_trigger_canonicalization` | codex Notification 在 frame == nil 路徑建 frame（少見邊界） | reconcile + descendant scan 都不觸發；trace decision/reason 沿用既有 `created_frame` 路徑；scan side-effects 不殘留 |
| IT9 | `findproxyparent_storage_error_aborts_apply` | reconcile 內 `findProxyParent` mock 回 storage error | applyFrameEvent 整體錯誤 propagate（既有 storage error 一致）|

**為什麼 IT4 是核心驗證**：直接針對 codex F1 finding 的 ancestor-late interleaving。若 IT4 過綠則 v2 雙向設計成立。

### 3.2 Unit 測試（補充覆蓋 helper）

`frame_ops_test.go` 加 `RC1`-`RC4` 為 helper-level unit test（mock-driven，補 IT1-IT9 沒覆蓋的 helper edge case）：

| # | 名稱 | 場景 |
|---|---|---|
| RC1 | `pidIsAncestorOfWithCap_depth_exhaustion` | 走 PPID 鏈超過 maxDepth 仍未找到 → false |
| RC2 | `pidIsAncestorOfWithCap_loop_detection` | PPID == PID → false（防 init process） |
| RC3 | `pidIsAncestorOfWithCap_process_info_error` | readProcessInfoFn err → false |
| RC4 | `canonicalizeDescendantsAfterUpsert_skips_same_type` | pane 內有 same-type standalone → 不收編 |

### 3.3 不加的測試

- ❌ goroutine race detector 測試 — 太脆，integration 順序模擬已足夠
- ❌ Phase 3 daemon_restart_recovery 路徑測試 — 那是 Phase 3 scope；rebase 後若需要 cross-feature 測試另開 issue
- ❌ 三方 + race 排列組合（cc + codex + opencode + race） — IT3/IT4 + descendant scan 邏輯已 cover 雙方收斂；三方無新規則

---

## 4. Sweep canonicalization（**仍不做** — rollback 已兜底）

v1 plan 因為 delete 失敗只 log 而漏網，需要 sweep 補；v2 用 bounded retry + rollback 後，**delete 失敗永遠不會留下「parent 含 proxy + child standalone」的雙顯示狀態**：

- delete 成功 → child 沒了 ✓
- delete 失敗 + rollback 成功 → parent 不含 proxy + child 還在 → 下次 SessionStart descendant-scan 重新嘗試收編
- delete 失敗 + rollback 失敗（極端）→ 兩邊都 race 輸；下次 SessionStart 仍會重新 attempt

故 sweep canonicalization 在本 PR 不需要。如果 reviewer 強烈要求作 defense-in-depth：開 follow-up issue，定 SLO 後再決定是否 backport。

---

## 5. 不做（明列）

- ❌ per-pane mutex / pane-level claim table（違背 PR-2b 無 lock atomic RMW 哲學）
- ❌ SQL UNIQUE constraint on `(pane_id, agent_type)`（cc + codex proxy 同 pane 是合法）
- ❌ delay window / SessionStart deadline（hack，影響 hook latency）
- ❌ Sweep canonicalization pass（§4）
- ❌ 對 non-SessionStart event 觸發 canonicalization（IT8 守此邊界）
- ❌ 修 Phase 3 fallback chain race（那條獨立路徑由 Phase 3 PR #638 處理）
- ❌ rebuild Inspector / SPA 變更（Phase 5 才做）
- ❌ 對 existing frame 路徑做 self-as-descendant reconcile（§2.2.2 解釋）

---

## 6. Commit 順序（TDD）

| # | Commit | 範圍 |
|---|---|---|
| 1 | `docs: Phase 3.5 plan v1 — cold-start proxy canonicalization` | 已 commit `bb382870` |
| 2 | `docs: Phase 3.5 plan v2 — bidirectional + retry/rollback (codex round 1 fixes)` | 此檔 |
| 3 | `feat(agent): pidIsAncestorOfWithCap + canonicalizeDescendantsAfterUpsert (unwired)` | 兩個新 helper + RC1-RC4 unit |
| 4 | `feat(agent): reconcileCreatedFrameAsProxy with bounded retry + rollback (unwired)` | reconcile helper（含 §2.1.1 retry/rollback）+ deleteWithRetryOrRollback shared helper |
| 5 | `feat(agent): wire bidirectional canonicalization into applyFrameEvent` | §2.2.1 + §2.2.2 接線 + trace meta |
| 6 | `test(agent): integration tests for cold-start race canonicalization` | IT1-IT9 用真 sqlite |

**Unwired 模式**（commits 3 + 4）沿用 Phase 3 commit `f109b258`，降低 review 對 wire-and-test 同 commit 的審讀負擔。

---

## 7. 行數預估

| 區塊 | 估計 |
|---|---|
| `frame_ops.go` reconcileCreatedFrameAsProxy（含 retry/rollback） | ~75 行 |
| `frame_ops.go` canonicalizeDescendantsAfterUpsert | ~70 行 |
| `frame_ops.go` pidIsAncestorOfWithCap | ~20 行 |
| `frame_ops.go` deleteWithRetryOrRollback shared helper | ~30 行 |
| `frame_ops.go` 接線 § 2.2.1 + §2.2.2 + trace | ~50 行 |
| `frame_ops_test.go` RC1-RC4 unit | ~120 行 |
| `module_test.go` / new file IT1-IT9 integration | ~450-550 行 |
| Plan docs（此檔 v2） | ~430 行 |
| **總 net code（不含 plan docs）** | **~815-915 行** |

落在「中-大 PR」邊界（PR-2b 是 ~+1700 行 net）。Review 負擔比 PR-2b 小但比 v1 estimate（~325-425 行）翻倍 — 主要在 integration 測試。

---

## 8. 驗收 / Ship 條件

- 所有 IT1-IT9 + RC1-RC4 + 既有 frame_ops_test 全綠
- `go build ./... && go vet ./... && go test ./...` 23 packages 全綠
- SPA 無變更
- 委派 codex 兩輪 review 收斂（標準 + adversarial 三視角）；high finding 全採納或開 follow-up issue
- Phase 3 PR #638 merged 後 rebase 本 PR 到 main，`applyFrameEvent` 衝突解到 Phase 3 fallback chain 之上、本 PR canonicalization 接線之下

---

## 9. 風險清單

| # | 風險 | 緩解 |
|---|---|---|
| R1 | descendant scan 找錯 ancestor（PPID 鏈經過 self 但邏輯上不該收編，例：user 手動 spawn 兩個獨立 cc + codex 巧合 PPID 共用） | 同 pane + 跨型別 + identity-verified（同 PR-2b findProxyParent 規則）；極小機率假陽性，由 next sweep / 使用者 SessionEnd 自然修復 |
| R2 | reconcile 與 idle sweep 競爭：sweep 正在刪 parent | `attachProxyRefWithRetry` 內部 `UpsertIfUnchanged` cover：sweep delete 後 attach 回 false → reconcile 回 false → standalone 保留 |
| R3 | reconcile/scan 與 SubagentStart hook 競爭 | `attachProxyRefWithRetry` 用 `mutateSubagentsWithRetry` 共享 retry，PR-2b R6 已驗 |
| R4 | rollback detach 也失敗（極端）| log + 接受；下次 SessionStart 仍會 re-attempt（雙向設計保證每次 SessionStart 都是新嘗試）|
| R5 | descendant scan 走 ListByPane O(n) + 每個 candidate walk O(depth) | proxyMaxDepth=5 + pane 內 frame 數一般 < 10；現代機器一次 scan < 5ms；SessionStart 頻率不高（user-initiated session）|
| R6 | rebase Phase 3 後 applyFrameEvent 衝突 | Phase 3 改 fallback chain（line 220 區塊），Phase 3.5 改 new-frame Upsert 後 + existing-frame Update 後（line 286 + line 272 區塊）；位置不同，git rebase 應可機械處理；極端衝突由施工者手動解 |
| R7 | descendant scan 的 ListByPane 與接線同事務未同步 | 接受 eventual consistency：scan 看到 standalone snapshot，attach 用 UpsertIfUnchanged。中間有新 standalone 進場 → 下次 SessionStart 才 canonicalize。可接受 |

---

## 10. Codex review focus 預期

施工後委派 codex 兩輪 review 時的 focus 文字建議：

**第一輪（標準 review）**：
> Phase 3.5 v2 — bidirectional canonicalization + retry/rollback。修 PR-2b 的 cold-start race。重點檢查：
> 1. v2 雙向設計的單向規則論證是否封閉（self-as-descendant + self-as-ancestor 兩條路徑會否互相觸發 secondary race）
> 2. `reconcileCreatedFrameAsProxy` 的 bounded retry + rollback 在所有失敗組合下是否確保 final state 一致
> 3. `canonicalizeDescendantsAfterUpsert` 的 `pidIsAncestorOfWithCap` walk 是否會誤抓 grandparent 的 sibling 等非真 ancestor
> 4. existing frame 路徑只跑 descendant-scan 不跑 self-reconcile 的判斷是否正確（SessionStart reset 後是否該重新 evaluate ancestry）
> 5. trace meta 在 IT4/IT5/IT6 各路徑下是否與 reviewer 對 SPA 顯示的預期吻合
> 6. IT 測試是否確實用真 sqlite（不退化成 mock-driven）

**第二輪（adversarial 三視角）**：
- **攻擊方**：尋找 race interleaving 死角；雙向設計是否有「scan + reconcile 同一 SessionStart 同時撞 attach」的內部 race；rollback detach 失敗後的二階段一致性
- **防守方**：v2 `pidIsAncestorOfWithCap` 與 `findProxyParent` 邏輯重複是否是好分離還是該合併
- **檔案體質**：`frame_ops.go` 752 → ~975 行後是否觸發 SRP；是否該抽 `canonicalize.go`

---

## 11. Open questions（plan v2 review 會解）

1. v2 雙向設計後，是否仍需把 `reconcileCreatedFrameAsProxy` 和 `canonicalizeDescendantsAfterUpsert` 的接線同放在 frame == nil 路徑，還是該分到不同 phase？（plan 採同 wire site，理由：兩個是同一 SessionStart 的 self 視角兩面）
2. `pidIsAncestorOfWithCap` 是否該抽到獨立 `process_tree.go` 檔（process tree util 集中）？（plan 留在 frame_ops.go，因為它是 race fix 的內部 helper，獨立檔太重）
3. existing frame 路徑的 scan 觸發位置在 `UpdateHookPathAndResetSubagents` 之後 — 若 reset 失敗則整個 SessionStart 失敗，scan 不跑；OK？（plan 採此設計：reset 失敗本就要回錯）
4. IT4 的 mock 設計（兩次 findProxyParent miss）是否能完整模擬「ancestor 真的還沒入庫」場景？或需要在 sqlite 層直接控制 ListByPane 結果？（plan 用第二種，更接近真 race）

---

## 12. 結束條件

PR 跑過兩輪 codex review 收斂後：

1. Phase 3 PR #638 merge 到 main（不 bump）
2. Rebase 本 PR 到 main（解 applyFrameEvent 衝突）
3. 本 PR merge 到 main（不 bump）
4. 開 bump PR 把 VERSION + package.json + spa/package.json 同步到 alpha.225 + CHANGELOG 條目（涵蓋 Phase 3 + Phase 3.5）
5. Bump merge → main @ alpha.225
6. ExitWorktree 清掉 `lights-phase-3-5` worktree
7. 更新 kickoff_lights_rebuild.md：標 Phase 3 + Phase 3.5 ✅ at alpha.225；下一步 Phase 4 probe audit

---

## 13. Codex review 軌跡

| Round | Job | Verdict | Findings | Resolution |
|---|---|---|---|---|
| Plan v1 round 1（adversarial） | 在 §0 摘要的 adversarial run（內聯結果，無 background job ID）| needs-attention | F1 high ancestor-late race / F2 high delete-race 雙顯示 / F3 medium mock test 不足 | v2 全採納（§0.2 / §2.1.1+2.1.2 / §3.1 升 ship gate）|
| Plan v2 round 2 | (待執行) | (pending) | — | — |
