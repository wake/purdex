# Phase 3.5 Plan — Cold-start Proxy Canonicalization

Baseline：`1.0.0-alpha.224`（main @ `75b4d166`）。
Worktree：`.claude/worktrees/lights-phase-3-5`（branch `worktree-lights-phase-3-5`）。
Branch base：`origin/main`（與 Phase 3 PR #638 並行；merge 順序 Phase 3 → rebase 3.5 → 一次 bump alpha.225）。

---

## 0. 來龍去脈（必讀）

### 0.1 修什麼

**Race 來源**：Phase 2 PR-2b 的 `findProxyParent`（`internal/module/agent/frame_ops.go:688`）只做 *pre-Upsert* PPID 走訪，無 post-Upsert 收斂。當兩個跨型別 SessionStart 在同一 pane 並發冷啟動（典型場景：daemon restart 後 cc + codex proxy 同時來），兩邊 walk 各自 miss → 各自 `Upsert` → 結果是兩個獨立 frame，PR-2b 的 proxy collapse 失效。

**最容易觸發的場景**（Phase 3 codex round 2 finding #1 提出）：daemon restart → cc 與 codex proxy 同時冷啟動 → cc 的 Upsert 與 codex 的 `findProxyParent` walk 交錯 → walk 在「cc Upsert 之前」執行 → codex 永久變成獨立 frame。Phase 3 加上 `tryRebuildFromProcessTree` 後 race window 變大，但 race 本身在 alpha.221 PR-2b 落地起就存在。

### 0.2 設計來源

Codex architectural consulting `task-modcbbhg-auxa36`（high-effort）採方案 **B'：post-Upsert canonicalization by ancestry**，捨棄 per-pane mutex / pane-level claim table / SQL UNIQUE / delay window 等 hack。

核心 invariant（**單向**，破解 secondary race）：

- Descendant **可**轉 proxy（被收編到 ancestor 的 SubagentRef 列表）
- Ancestor **不可**轉 descendant
- PID / `started_at` / frame_id 只在多個 ancestor candidate tie-break 時用，**不能**反過來定義 parent 語義

由此，cc + codex 並發冷啟動的 reconcile 階段保證收斂：

| Frame | reconcile 走 PPID 鏈往上找 | 動作 |
|---|---|---|
| cc 自己（cc 為頂層） | 找不到 codex（codex 不是 cc 的 ancestor） | no-op |
| codex（codex 為 cc descendant） | 找到 cc | attach codex 為 cc 的 proxy ref + delete 自己 standalone frame |

兩邊同時跑也不互換，因為「ancestor 不可轉 descendant」這條規則不對稱。

### 0.3 與 Phase 3 的關係

- **獨立但相關**：race 本身不依賴 Phase 3 的 `daemon_restart_recovery` 程式碼，而是 PR-2b（alpha.221）就存在的舊洞
- **接線位置同檔**：Phase 3 與 Phase 3.5 都動 `applyFrameEvent`（前者加 fallback chain rebuild，後者加 post-Upsert reconcile）— rebase 時可能在 fallback chain 區段衝突
- **獨立 PR**：依 kickoff 「切 Phase 3.5 獨立 PR」決策；Phase 3 PR #638 merge 後 rebase Phase 3.5

---

## 1. 既有原語（沿用，無新 store API）

PR-2b 已備齊所有需要的原語：

| 原語 | 位置 | 用途 |
|---|---|---|
| `findProxyParent(req)` | `frame_ops.go:688` | PPID 鏈走訪 + same-pane + live + identity-verified + cross-type 篩選；**reconcile 直接重用** |
| `attachProxyRefWithRetry(parent, ref, broadcastTs)` | `frame_ops.go:631` | optimistic concurrency attach（retry on conflict via `mutateSubagentsWithRetry`） |
| `FramesStore.DeleteIfUnchanged(frameID, lastSeenAt)` | `internal/store/frames.go:263` | atomic delete（last_seen_at unchanged 才刪）|
| `SubagentRef{ID, Type, StartedAt, SourcePID, SourceStartTime, IsProxy}` | `internal/agent/subagent.go` | proxy ref 型別（PR-2a 落地） |
| Proxy ID 格式 `proxy:%s:%d:%s`（agentType, pid, startTime） | `frame_ops.go:176`（applyFrameEvent 既有用法） | reconcile 必須沿用同格式以與 PR-2b 同型 |

**沒有**新 store method、沒有新型別、沒有新 SQL。整個改動限於 `frame_ops.go` 加一個 helper + 接線一行。

---

## 2. 設計細節

### 2.1 `reconcileCreatedFrameAsProxy` helper

```go
// reconcileCreatedFrameAsProxy attempts to canonicalize a freshly-created
// SessionStart frame against an alive cross-type ancestor whose own frame
// landed in the database after this sender's findProxyParent walk completed
// (race window closed by post-Upsert reconcile).
//
// Returns:
//   - attached=true, parentStored populated when canonicalization succeeded
//     (parent has new proxy ref + child frame either deleted or left for
//     next-cycle cleanup if DeleteIfUnchanged conflicted).
//   - attached=false, zeroFrame, nil when no cross-type ancestor exists
//     (legitimate standalone frame — leave as-is).
//
// Single-direction rule (B' design): only walks UP from sender's PPID. The
// just-created frame at sender PID is never in the walk path, so reconcile
// cannot canonicalize itself away.
//
// Failure modes:
//   - findProxyParent error → propagate (storage failure).
//   - attachProxyRefWithRetry returns attached=false (parent vanished) → no-op
//     (next sweep / next hook canonicalizes).
//   - DeleteIfUnchanged returns deleted=false (child row was modified by a
//     concurrent writer between Upsert and delete) → log + leave (parent has
//     proxy ref, child remains; next reconcile or sweep cleans up).
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
        // Parent vanished mid-flight; leave standalone for next cycle.
        return false, store.Frame{}, nil
    }
    // Atomic delete: only if child's last_seen_at unchanged since Upsert.
    // Failure (deleted=false) means a concurrent writer touched the row
    // (probe status update, another reconcile, idle sweep). Don't retry —
    // idempotent canonicalization can resume from sweep / next hook.
    deleted, derr := m.frames.DeleteIfUnchanged(stored.FrameID, stored.LastSeenAt)
    if derr != nil {
        return false, store.Frame{}, derr
    }
    if !deleted {
        log.Printf("phase3.5: reconcile attached proxy ref but child frame %s delete lost race; left for next canonicalization", stored.FrameID)
    }
    return true, parentStored, nil
}
```

**為什麼 reconcile 直接呼 `findProxyParent` 就夠**：

`findProxyParent` 在 line 169（pre-Upsert）跑過一次 — 那是 race window 的「太早」端，當時 cc 的 frame 還沒入庫所以 walk miss。reconcile 在 post-Upsert 跑同一個 walk — 此時 race window 的「另一端」cc 的 Upsert 已完成（並發 Upsert 之間 SQLite 的 row-level 順序保證了至少一邊看到對方），所以 walk 會找到 cc。重用既有 walk 邏輯避免兩套 walk drift。

**為什麼不用擔心找到自己**：walk 從 `info.PPID`（sender PID 的 parent）開始，sender PID 本身不在 walk path 內。除非 sender PID == sender PPID（不可能），不然不會踩到自己剛 Upsert 的 frame。

### 2.2 接線（applyFrameEvent）

在新 frame 的 `Upsert` 成功後（`frame_ops.go:286-292` 之後、`projectPane` 之前）插入：

```go
} else {
    // New frame: insert via Upsert. ...
    stored, err = m.frames.Upsert(store.Frame{...})
    if err != nil {
        return nil, FrameTraceMeta{}, err
    }

    // Phase 3.5: post-Upsert canonicalization by ancestry.
    // Closes the race window between findProxyParent's pre-Upsert walk and
    // the cross-type ancestor's own SessionStart Upsert. Only fires for
    // SessionStart (the event with proxy semantics in PR-2b).
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
                Reason:        "post_upsert_canonicalization",
                Before:        before,
                After:         summarizeFrame(&parentStored),
            }, err
        }
    }
}
```

**為什麼只在 SessionStart 觸發**：proxy 收編語義來自 session 邊界（PR-2b 已限定 line 168 fast-path 為 `SessionStart && frame == nil`）。其他 event（Notification / ToolUse 等）若在 frame == nil 路徑建 frame（少見邊界情境），由下一次 SessionStart 或 sweep canonicalize；不在 Phase 3.5 的 race scope 內。

**為什麼 `Before` 沿用原 before**：原 before（line 49 `summarizeFrame(frame)`）是 `frame == nil` 的空 map — 與 reconcile 「我本來不存在」的語義一致。`After` 是 parent 的新狀態（多了一個 proxy ref）。

### 2.3 Trace decision 詞彙

| 決策 | Reason | 觸發 |
|---|---|---|
| `updated_frame` | `proxy_subagent_attached` | PR-2b pre-Upsert proxy fast-path（既有，不改）|
| `created_frame` | `parent_frame_found` / `parent_frame_missing` | 一般 new frame 路徑（既有，不改）|
| `updated_frame` | `post_upsert_canonicalization` | **Phase 3.5 新**，reconcile 成功收編 |

不引入第四 reason（如 `created_then_canonicalized_failed_to_delete`）— delete 失敗是 fail-soft，trace 仍顯示 `post_upsert_canonicalization` 反映 canonical 結果（parent 視角）；child 殘留為非觀察事件，由 log + 下次 reconcile/sweep 處理。

---

## 3. 測試矩陣（race interleavings）

新增測試在 `internal/module/agent/frame_ops_test.go`，命名 `RC1`-`RC8`（Race Canonicalization）。沿用 PR-2b 的 fixture 與 process tree mocking 慣例（`readProcessInfoFn` / `isPidAliveFn` / `processStartTimeFn` 注入）。

| # | 名稱 | 場景 | 預期 |
|---|---|---|---|
| RC1 | `concurrent_cold_start_codex_then_cc` | 序列：codex SessionStart Upsert → cc SessionStart Upsert（codex walk 在 cc Upsert 前完成，pre-Upsert findProxyParent miss）；codex post-Upsert reconcile 跑 | 1 個 cc frame，subagents 含 codex proxy ref（IsProxy=true, SourcePID=codex.PID）；codex frame 已刪 |
| RC2 | `concurrent_cold_start_cc_then_codex` | 序列反過來：cc Upsert 先、codex Upsert 後 | cc reconcile no-op（找不到自己的 ancestor）；codex reconcile 找到 cc → attach + delete 自己 |
| RC3 | `concurrent_cold_start_three_way` | cc + codex + opencode 同 pane 並發冷啟動，process tree：opencode → opencode-companion → codex → codex-companion → cc | 1 個 cc frame，subagents 含 codex proxy ref + opencode proxy ref；codex/opencode standalone frame 都已刪 |
| RC4 | `reconcile_no_ancestor_noop` | 單一 SessionStart，PPID 鏈無跨型別 frame | 無 reconcile 動作（attached=false）；standalone frame 保留 |
| RC5 | `reconcile_attach_race_lost` | reconcile 走到 attachProxyRefWithRetry，但 parent 在 retry 期間被刪（mock UpsertIfUnchanged 持續回 false 直到耗盡 retry） | reconcile 回 attached=false；standalone frame 保留；無 panic |
| RC6 | `reconcile_delete_race_lost` | attach 成功，但 child frame 在 attach 期間被 probe status update 改了 last_seen_at，DeleteIfUnchanged 回 false | parent 含新 proxy ref；child frame 仍存在；trace decision = `updated_frame` reason `post_upsert_canonicalization`（不 fail）|
| RC7 | `non_session_start_no_reconcile` | Notification event 在 frame == nil 路徑建 frame | reconcile 不觸發（frame 殘留 standalone）— 防止 reason 用錯路徑 |
| RC8 | `findproxyparent_storage_error` | reconcile 內 findProxyParent 回 storage error | applyFrameEvent 整體錯誤 propagate（與既有 storage error 一致）|

**Race 模擬技巧**（沿用 PR-2b mutateSubagentsWithRetry 測試模式）：

- 不需要真正 goroutine：依賴 `findProxyParent` / `attachProxyRefWithRetry` / `DeleteIfUnchanged` 的 mock + 預設順序就能模擬 interleaving
- RC1/RC2/RC3 用 in-process 順序：先把 cc 的 frame `Upsert` 進 store，再呼 codex 的 `applyFrameEvent`（findProxyParent 在 line 169 走 — 已能找到 cc 但 PR-2b path 設定為 `req.EventName == "SessionStart" && frame == nil` 才走）

  **關鍵 mock 設定**：必須讓 line 169 的 pre-Upsert findProxyParent miss，line 286 的 post-Upsert reconcile 才會 hit。做法是 mock `m.frames.FindByPanePID` 第一次回 nil（模擬 cc 還沒 Upsert）、第二次回 cc frame。沿用 `framesStub` 既有的 call counter pattern（PR-2b RP1-RP15 已用過此 pattern）。

- RC5 mock `attachProxyRefWithRetry` 內部的 UpsertIfUnchanged 持續 false（已有 PR17 RACE regression test 同型）

### 3.1 整合測試（可選，但建議）

`module_test.go` 加 `TestApplyFrameEvent_PostUpsertCanonicalization` 系列：用真 sqlite store + 模擬 race 順序 + 斷言 final state。確保 store / handler / projection 三層 wiring 正確。

如果加會落在 `TestApplyFrameEvent_*` series 末尾（按時間先後）。**否則 race interleaving 測試已足夠 cover Helper 邏輯，可不加 integration**。施工時若 unit 測試容易寫就跳過 integration，沒必要重複。

### 3.2 不加的測試

- ❌ goroutine race detector 測試 — 太脆，現有 in-process interleaving 模擬已足夠
- ❌ Phase 3 daemon_restart_recovery 路徑測試 — 那是 Phase 3 scope；rebase 後若需要 cross-feature 測試另開 issue

---

## 4. Sweep canonicalization（**不做** — 延後）

Codex consulting 提到「optional：sweep.go 加 lightweight canonicalization pass」作為 defensive layer。本 PR **不做**：

- reconcile 已在每個 SessionStart 後做收斂；正常流程下不會殘留 mismatch
- 唯一漏網場景：reconcile attach 成功 + delete race 失敗（RC6） — child 殘留到下次 reconcile/sweep；下次 codex SessionStart 進 applyFrameEvent 時 `frame != nil`（GetByIdentity 命中既有 child），走 `frame != nil` UpdateHookPath 路徑 — 不會再嘗試 reconcile
- 因此確實有「殭屍 standalone frame 直到 idle sweep（1h）」的漏網期；但 idle sweep 已存在（PR-2b §1.6），1h 後會清除
- 加 sweep canonicalization 會把 ancestor walk 跑進 sweep loop（O(n) 變 O(n × walk depth)），增加 sweep 成本；先觀察 RC6 在 prod 是否真的發生再決定

如果 reviewer 強烈要求：開 follow-up issue 不在本 PR 處理。

---

## 5. 不做（明列）

- ❌ per-pane mutex / pane-level claim table（違背 PR-2b 無 lock atomic RMW 哲學）
- ❌ SQL UNIQUE constraint on `(pane_id, agent_type)`（cc + codex proxy 同 pane 是合法狀態）
- ❌ delay window / SessionStart deadline（hack，影響 hook latency）
- ❌ 引入第四 trace reason `created_then_canonicalized_failed_to_delete`（fail-soft 本身不是觀察點）
- ❌ Sweep canonicalization pass（§4）
- ❌ 對 non-SessionStart event 觸發 reconcile（不在 Phase 3.5 race scope）
- ❌ 修 Phase 3 fallback chain race（那條獨立路徑由 Phase 3 PR #638 處理）
- ❌ rebuild Inspector / SPA 變更（Phase 5 才做）

---

## 6. Commit 順序（TDD）

| # | Commit | 範圍 | 測試 |
|---|---|---|---|
| 1 | `feat(agent): reconcileCreatedFrameAsProxy helper (unwired)` | helper 新 method + sig + walk reuse + delete + log | RC1（helper 直呼，不過 applyFrameEvent）|
| 2 | `feat(agent): wire reconcile into applyFrameEvent post-Upsert` | line 286 後接線 + EventName guard + trace reason `post_upsert_canonicalization` | RC2 / RC3 / RC4 / RC7 / RC8 |
| 3 | `test(agent): reconcile race-loss interleavings` | RC5 + RC6 race 模擬 | RC5 / RC6 |
| 4 | `docs: Phase 3.5 plan v1` | 此 plan 檔本身（已先 commit 過 placeholder 的話 amend；否則放最後）| — |

**Commit 1 unwired 模式**沿用 Phase 3 commit `f109b258`（`tryRebuildFromProcessTree helper (unwired)`）— 先讓 helper 測試獨立綠，再 commit 2 接線後測 wiring。降低 review 對 wire-and-test 同 commit 的審讀負擔。

**Commit 4 docs 位置**：依 kickoff 「先 commit docs placeholder → 再開工」原則，docs 應在 commit 1 之前。實作上：commit 1 = `docs: Phase 3.5 plan v1`，commit 2 = helper（unwired），commit 3 = wiring，commit 4 = race tests。重新編號：

| # | Commit | 範圍 |
|---|---|---|
| 1 | `docs: Phase 3.5 plan v1 — cold-start proxy canonicalization` | 此 plan 檔 |
| 2 | `feat(agent): reconcileCreatedFrameAsProxy helper (unwired)` | helper + RC1 |
| 3 | `feat(agent): wire reconcile into applyFrameEvent post-Upsert` | wiring + RC2/RC3/RC4/RC7/RC8 |
| 4 | `test(agent): reconcile race-loss interleavings` | RC5/RC6 |

如果 plan v1 codex 審後改 v2，docs commit 補一個 `docs: Phase 3.5 plan v2 — codex round 1 fixes` 在 v1 之上、helper 之前（與 Phase 3 plan v1→v2 流程同型）。

---

## 7. 行數預估

| 區塊 | 估計 |
|---|---|
| `frame_ops.go` reconcile helper | ~50 行 |
| `frame_ops.go` 接線 + trace meta | ~25 行 |
| `frame_ops_test.go` RC1-RC8 | ~250-350 行（依 fixture 重用程度）|
| `module_test.go` integration（**可選**）| 0 或 ~80 行 |
| Plan docs | ~330 行（此檔）|
| **總 net code（不含 plan docs）** | **~325-425 行** |

落在「中 PR」邊界（PR-2b 是 ~+1700 行 net）。Review 負擔比 PR-2b 小很多 — 修一個有 codex consulting backing 的單點 race，無新型別、無新 store API、無 SPA 變更。

---

## 8. 驗收 / Ship 條件

- 所有 RC1-RC8 + 既有 frame_ops_test 全綠
- `go build ./... && go vet ./... && go test ./...` 23 packages 全綠
- SPA 無變更（無需跑 spa lint/test，但 PR description 註明「SPA unchanged」便於 review router）
- 委派 codex 兩輪 review 收斂（標準 + adversarial 三視角）；重大 finding 全採納或開 follow-up issue
- Phase 3 PR #638 merged 後 rebase 本 PR 到 main，`applyFrameEvent` 衝突解到 Phase 3 fallback chain 之上、本 PR reconcile 接線之下

---

## 9. 風險清單

| # | 風險 | 緩解 |
|---|---|---|
| R1 | reconcile 找錯 ancestor（同 pane 但邏輯上不該收編，例如 user 手動 spawn 的 cc + codex 並非 parent-child 關係） | `findProxyParent` 已要求 PPID 鏈確切 ancestor + same-pane + live + identity-verified；reuse 同邏輯保護不變 |
| R2 | reconcile 與 idle sweep 競爭：sweep 正在刪 parent，reconcile 同時 attach | `attachProxyRefWithRetry` 內部 `UpsertIfUnchanged` 已 cover：sweep delete 後 attach 回 attached=false（parent 不存在）→ reconcile 回 false → standalone 保留 |
| R3 | reconcile 與 SubagentStart hook 競爭：cc 的 SubagentStart 同時在改 cc.Subagents | `attachProxyRefWithRetry` 用 `mutateSubagentsWithRetry` 共享 retry 機制；兩個並發 mutation 會收斂到 final state（PR-2b R6 已驗）|
| R4 | DeleteIfUnchanged 的 `deleted=false` 殘留累積 | 同 §4 分析：常態下不發生；發生時 idle sweep 1h 兜底；極端情境下次 SessionStart 不會再嘗試 reconcile（frame != nil 走 update path）— 接受此漏網期 |
| R5 | `findProxyParent` walk 在 reconcile 階段成本（每個新 frame 都 walk）| walk depth 上限 5；現代機器一次 walk < 1ms；SessionStart 頻率不高（每個 user-initiated session）— 可忽略 |
| R6 | rebase Phase 3 後 applyFrameEvent 衝突 | Phase 3 改 fallback chain（line 220 區塊），Phase 3.5 改 new-frame Upsert 後（line 286 區塊）；位置不同，git rebase 應可機械處理；極端衝突由施工者手動解 |

---

## 10. Codex review focus 預期

施工後委派 codex 兩輪 review 時的具體 focus 文字建議：

**第一輪（標準 review）**：
> Phase 3.5 — post-Upsert proxy canonicalization. 修 PR-2b 的 cold-start race。重點檢查：
> 1. `reconcileCreatedFrameAsProxy` 對 `findProxyParent` 的重用是否會拿到自己的 frame（自我收編）
> 2. `DeleteIfUnchanged` 失敗時的後續一致性（standalone 殘留 + parent 已含 proxy ref 的偏差是否會造成 SPA 顯示異常）
> 3. trace meta 的 `Before` / `After` 表達在 reconcile 成功後是否與既有 trace step 慣例一致
> 4. Phase 3.5 與 Phase 3 fallback chain 的互動（rebase 後接線位置會不會造成同 SessionStart 雙觸發）

**第二輪（adversarial 三視角）**：
- **攻擊方**：找 race interleaving 的死角；`findProxyParent` 在 reconcile 階段是否有可能返回剛被另一邊 reconcile 收編走的 ghost ancestor；DeleteIfUnchanged 與 idle sweep / probe status update 的競賽角度
- **防守方**：B' 單向收斂規則的論證是否在所有 ancestor 拓撲下都成立；`findProxyParent` 重用 vs. 重寫的 trade-off
- **檔案體質**：`frame_ops.go` 已 752 行，加 ~75 行後是否觸及 SRP 邊界；reconcile 是否該抽到獨立檔（如 `frame_canonicalize.go`）

---

## 11. Open questions（plan review 會解）

1. 是否該把 reconcile 同步擴展到 non-SessionStart 路徑？（plan 目前不擴展，等 review 質疑再說）
2. 是否需要 sweep canonicalization 作為 defensive layer？（plan §4 不做，等 review 投票）
3. trace decision 是否該新增 `created_then_canonicalized` 與 `updated_frame` 區分？（plan 採 `updated_frame` + reason 區分，因為 final state 是 parent updated）
4. `reconcileCreatedFrameAsProxy` 是否該抽到獨立檔？（plan 留在 `frame_ops.go`，理由：frame_ops 已是同檔的 race 修復集合）

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
