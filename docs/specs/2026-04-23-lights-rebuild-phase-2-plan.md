# Phase 2 TDD Plan v2 — L3 Subagent 升級 + Proxy + Frame Idle Sweep

- **Date**: 2026-04-24（v1 → v2 revision after codex adversarial review `review-mobwvdpq-w6kftz`）
- **Spec**: `docs/specs/2026-04-23-lights-rebuild-spec.md` §6
- **Worktree**: `lights-phase-2`（branch `worktree-lights-phase-2`）
- **依賴**: Phase 1（merged at `d1d60b2c`）+ Hook Events PR #616（merged at `fd9f8f8f`, alpha.217）
- **範圍**：`SubagentRef` 結構升級（含 SourcePID/SourceStartTime）+ PPID-first proxy 偵測 + SessionEnd proxy cleanup + frame idle sweep（conditional DELETE）+ SPA 型別 / 視覺升級
- **拆分決策**：**確定拆 PR-2a（schema + 型別）+ PR-2b（proxy + sweep + 視覺）**

## 0. 拆分策略（已確定）

### PR-2a — Schema + 型別升級（純骨架，無新行為）

- `SubagentRef` 型別定義（含 SourcePID/SourceStartTime/IsProxy 等欄位，但本 PR **不會有任何 ref.IsProxy=true 產生**）
- Frame.Subagents / SessionProjection.Subagents / m.subagents map / NormalizedEvent.Subagents / SPA store 型別同步升級
- `updateSubagents` 簽名升級，行為語意不變（SubagentStart 新增、SubagentStop 移除）
- SPA SubagentDots **維持 count-based API**（新視覺留 PR-2b）
- **行為不變**：SubagentStart/Stop 走既有路徑，產生 `SubagentRef{ID, Type=frame.AgentType, StartedAt=broadcastTs}` — IsProxy 永遠 false
- 預估 ~600 行（含測試 + plan）

### PR-2b — Proxy 偵測 + Idle Sweep + SubagentDots 新視覺

- `applyFrameEvent` PPID-first proxy 偵測分支（SessionStart only）
- SessionEnd proxy cleanup（從 parent.Subagents 移匹配 SourcePID+SourceStartTime 的 ref）
- Frame idle sweep（1h 閾值 + conditional DELETE `DeleteIfUnchanged` + orphan watcher clean）
- SubagentDots 視覺：type color（cc 藍 / codex 黃 / opencode 橘）+ proxy outline
- 預估 ~450 行（含測試）

PR-2a merge → 單獨 bump alpha → PR-2b 在 PR-2a 基礎上開 branch。

### 為何拆

Codex adversarial review 指出 schema 升級與 proxy 新行為**本質是兩個 review 主題**：
- 型別擴散驗證 vs proxy 識別正確性 / PPID 繼承 / sweep race
- 混在一起 reviewer 要同時 track 兩條線，出錯率上升

拆後 PR-2a 可快速 merge 固定 schema 基礎；PR-2b 集中火力守 proxy 語意。

---

## 1. 契約鎖定（共用）

### 1.1 `SubagentRef` 型別（新增）

位置：`internal/agent/subagent.go`（新檔，`internal/agent` package 根層，與 `status.go` 平行）

```go
type SubagentRef struct {
    ID              string `json:"id"`
    Type            string `json:"type"`              // agent type, e.g. "cc" / "codex" / "opencode"
    StartedAt       int64  `json:"started_at"`        // UnixNano，沿用 frame broadcast_ts 口徑
    SourcePID       int    `json:"source_pid"`        // 來源 process PID（SubagentStart 為 0 代表無；proxy 為 hook sender PID）
    SourceStartTime string `json:"source_start_time"` // 來源 process start time（proxy identity 鎖定，防 PID 重用）
    IsProxy         bool   `json:"is_proxy,omitempty"`// true = cross-agent-type hook 被收編
}
```

**欄位語意**：

| 情境 | ID | Type | SourcePID / SourceStartTime | IsProxy |
|---|---|---|---|---|
| SubagentStart (native) | `raw.agent_id` | `raw.agent_type` fallback parent `frame.AgentType` | `0` / `""` | `false` |
| Proxy attach | `proxyIDFor(req)` = `fmt.Sprintf("proxy:%s:%d:%s", req.AgentType, req.SenderPID, req.SenderStartTime)` | `req.AgentType` | `req.SenderPID` / `req.SenderStartTime` | `true` |

**設計要點**（對 codex #1 的修正）：
- Proxy identity 由 `SourcePID + SourceStartTime` 鎖定，不被 PID 重用污染
- Proxy ID 字串包含 `SenderStartTime`，進一步 harden 唯一性（同 PID 不同時期不撞）
- SessionEnd 清 proxy ref 用 `SourcePID + SourceStartTime` 匹配，不是 ID 字串（跨升級相容性考量）
- Native SubagentStart 的 SourcePID/SourceStartTime 設為零值 — 語意上是「無外部 process 對應」，與 proxy 區分
- `StartedAt` **只在首次新增時設定**，重複 SubagentStart 同 ID 不覆寫
- `IsProxy,omitempty`：JSON 僅在 true 時序列化；native SubagentStart 的 ref JSON 不會冒出該 key
- **不加** `LastSeenAt` per ref（Phase 2 避免 scope 蔓延）
- **不加** `Status` 欄位（SubagentStart 本來就 Status=""）

### 1.2 Frame.Subagents 升級（破壞式）

`internal/store/frames.go`:

- `Frame.Subagents []string` → `[]agentpkg.SubagentRef`
- SQL column `subagents_json` 不改，JSON shape 換掉即可
- `scanFrame` 的 `json.Unmarshal` 直接指向 `[]SubagentRef`，舊格式 row 會 unmarshal 失敗 → 冒出 error
- `Upsert` 的 `json.Marshal` 自動吃新型別
- nil 防護不變：`if frame.Subagents == nil { frame.Subagents = []SubagentRef{} }`

**升級策略**（對 codex #4 的修正 — 使用者確認單人使用，跳 migration）：
- **不寫** 舊格式 fallback、**不寫** 自動 migration、**不加** boot hard-fail
- 使用者升級 alpha 後**須手動重建 DB**（rm `~/.purdex/<host>/agent.sqlite` 或對應路徑）
- Bump PR 的 CHANGELOG.md **紅字大標** 警告
- 升級後首次 hook 請求若讀到舊 row 會 return error → daemon log noisy 但不崩潰；使用者看到錯誤重建即可
- 使用者場景：目前僅 wake@protype.tw 單人 alpha 使用，可接受短期陣痛；之後 beta 前 schema 穩定再做正式 migration framework

**CHANGELOG 模板**（alpha.218 bump PR 時使用）：

```markdown
## alpha.218

### ⚠ BREAKING CHANGE — Agent DB schema

`agent_frames.subagents_json` format upgraded from `["id"]` to
`[{"id","type","started_at",...}]`. Existing rows are NOT auto-migrated.

**Action required**: Remove the agent DB before upgrading:
    rm ~/Library/Application\ Support/Purdex/<host>/agent.sqlite
    # (path varies by OS; delete the whole `<host>` dir if unsure)

After rebuild, all frames will be empty and re-populate from live
hook events. No user data lost (frames are ephemeral telemetry).
```

### 1.3 `updateSubagents` 簽名升級

`internal/module/agent/frame_ops.go`:

```go
// before
func updateSubagents(current []string, eventName, agentID string) []string

// after
func updateSubagents(current []agentpkg.SubagentRef, eventName string, ref agentpkg.SubagentRef) []agentpkg.SubagentRef
```

行為：
- `SubagentStart`：若 `current` 無同 ID → append ref；若有同 ID → **不覆寫既有**（保留既有 StartedAt/SourcePID/SourceStartTime/IsProxy）
- `SubagentStop`：filter 掉同 ID 的 ref（Type 不參與比對，避免 proxy↔native 切換誤漏）

在 `applyFrameEvent` 的 SubagentStart/Stop 分支組出 `ref`：
```go
ref := agentpkg.SubagentRef{
    ID:        agentID,                         // from result.Detail["agent_id"]
    Type:      firstNonEmpty(
        strFromDetail(result.Detail, "agent_type"),
        frame.AgentType,                        // fallback to parent frame's type
    ),
    StartedAt: broadcastTs,
    // SourcePID / SourceStartTime 留零值（native subagent 無外部 process 對應）
    IsProxy: false,
}
```

### 1.4 Proxy 偵測分支（PR-2b 新增，PPID-first）

在 `applyFrameEvent` 當前「建/更 frame」主路徑**之前**插入 proxy 判斷：

**前置取得 parent**（重用既有程式碼路徑）：
```go
info, err := readProcessInfoFn(req.SenderPID)  // 已在現有 frame_ops 取得
if err != nil {
    return nil, FrameTraceMeta{}, err
}
parentCandidate, err := m.frames.FindByPanePID(req.TmuxPaneID, info.PPID)
if err != nil {
    return nil, FrameTraceMeta{}, err
}
```

**觸發條件**（**全部成立才算 proxy**）：
1. `req.EventName == "SessionStart"`（目前只對 SessionStart 偵測 proxy；其他事件走 legacy 路徑）
2. `frame == nil`（sender identity (pid, start) 無既有 frame，表「新來的」）
3. `parentCandidate != nil` — **PPID 單層在同 pane 找到 frame**
4. `parentCandidate.AgentType != req.AgentType` — 跨 agent type
5. `isPidAliveFn(parentCandidate.PID)` — parent 還活著（避免掛殭屍）

**命中行為**：
- 組 `ref`:
  ```go
  ref := agentpkg.SubagentRef{
      ID:              fmt.Sprintf("proxy:%s:%d:%s", req.AgentType, req.SenderPID, req.SenderStartTime),
      Type:            req.AgentType,
      StartedAt:       broadcastTs,
      SourcePID:       req.SenderPID,
      SourceStartTime: req.SenderStartTime,
      IsProxy:         true,
  }
  ```
- `parentCandidate.Subagents = updateSubagents(parentCandidate.Subagents, "SubagentStart", ref)`
- `parentCandidate.LastSeenAt = broadcastTs`
- `m.frames.Upsert(*parentCandidate)` — **不建新 frame**
- Trace meta：`Decision="updated_frame", Reason="proxy_subagent_attached"`，`FrameID/ParentFrameID` 指 parent
- projection 回傳 parent pane 的投影

**不命中條件 → fallback 建新 frame（既有路徑）**：
- `req.EventName != "SessionStart"` → 走既有 legacy（其他事件不會 trigger proxy）
- `frame != nil` → 已有 frame，走 Upsert 更新
- `parentCandidate == nil` → PPID 單層找不到 frame；**不用 pane-only fallback**（codex #2 指出的回歸）
- `parentCandidate.AgentType == req.AgentType` → 真的是同類 re-session（例 cc exit 後 cc 再起）
- parent PID 已死 → 走既有建 frame 路徑（不掛殭屍；死 parent 交由 sweep 清）

**PPID 單層的 trade-off**（對 codex #2 + 使用者決策 A 的註記）：
- codex-companion 若是 cc 的直接 child，則 codex 的 PPID = codex-companion PID，再上一階才是 cc；單層 PPID match 可能找不到 cc frame
- Phase 2 **接受此限制**：PPID 單層命中 ≥ 50% case 可驗證 UX；若實測顯示 codex-companion 中介造成 proxy 大量失手，Phase 3/4 再加 PPID tree walk（spec §11-5 已保留空間）
- 失手時不影響功能 — 走 fallback 建 codex frame（與目前行為一致，只是沒命中 proxy）

**非 SessionStart 的 proxy 事件**：Phase 2 不處理。觀察實際行為若 codex proxy 沒先送 SessionStart 再開工，Phase 3/4 補。

### 1.5 SessionEnd 對 Proxy Ref 的清理（PR-2b 新增）

SessionEnd 來自 proxy 來源 agent（例 codex 結束）時，現況是「`frame == nil`（無獨立 frame） → `session_end_without_frame`」，proxy ref 留在 parent.Subagents 沒人清。

**新行為**：在 `applyFrameEvent` SessionEnd 分支的 `frame == nil` 路徑加一段 proxy ref 掃描：

```go
case "SessionEnd":
    if frame != nil {
        // ...既有 delete frame 路徑...
        return ...
    }
    // frame == nil — 可能是 proxy ref，掃 pane 內 frame 的 Subagents
    removed, parentFrame, err := m.removeProxyRefForSender(req.TmuxPaneID, req.SenderPID, req.SenderStartTime, broadcastTs)
    if err != nil {
        return nil, FrameTraceMeta{}, err
    }
    if removed {
        projection, err := m.projectPane(req.TmuxPaneID)
        return projection, FrameTraceMeta{
            FrameID:       parentFrame.FrameID,
            ParentFrameID: parentFrame.ParentFrameID,
            Decision:      "updated_frame",
            Reason:        "proxy_subagent_detached",
            Before:        ...,
            After:         summarizeFrame(&parentFrame),
        }, err
    }
    // 舊路徑 — 真正 orphan SessionEnd
    projection, err := m.projectPane(req.TmuxPaneID)
    return projection, FrameTraceMeta{Decision: "skipped", Reason: "session_end_without_frame", ...}, err
```

`removeProxyRefForSender` 實作：
1. `m.frames.ListByPane(paneID)` 取 pane 所有 frame
2. 迴圈 frame.Subagents 找 `ref.SourcePID == senderPID && ref.SourceStartTime == senderStartTime`
3. 命中 → filter 掉該 ref + `frame.LastSeenAt = broadcastTs` + `m.frames.Upsert(frame)` + return `true, frame`
4. 都不命中 → return `false, zeroFrame`

**為何用 SourcePID + SourceStartTime 而非 ref.ID 字串匹配**：更穩健 — SourcePID+SourceStartTime 是進程實體身份，ref.ID 是派生字串；兩者在當前實作一致但將來 ID 格式若變，identity 比對仍對。

### 1.6 Frame Idle Sweep 規則（PR-2b 新增，conditional DELETE）

`internal/module/agent/sweep.go`:

```go
const frameIdleThreshold = 1 * time.Hour
var nowFn = time.Now  // test seam
```

`sweepOnce` 迴圈每個 frame 檢查（現有兩條 + 新第三條）：
1. `!isPidAliveFn(frame.PID)` → `m.clearFrame(frame, "pid_dead")` — 既有
2. `processStartTimeFn(frame.PID) != frame.ProcessStartTime` → `m.clearFrame(frame, "pid_reused")` — 既有
3. **新**：`nowFn().UnixNano() - frame.LastSeenAt > frameIdleThreshold.Nanoseconds()` →
   **conditional DELETE**（對 codex #3 修正）：
   ```go
   deleted, err := m.frames.DeleteIfUnchanged(frame.FrameID, frame.LastSeenAt)
   if err != nil {
       return err
   }
   if !deleted {
       continue // 有 concurrent upsert refresh 了，skip 此 frame
   }
   // 手動觸發 clearFrame 的「post-delete side effects」（broadcast + orphan watcher clean）
   m.afterFrameCleared(frame, "idle_timeout")
   ```

### 1.7 `FramesStore.DeleteIfUnchanged` 新 method（PR-2b 新增）

`internal/store/frames.go`:

```go
// DeleteIfUnchanged removes the frame only if its last_seen_at matches the
// provided value — a concurrent Upsert that refreshed the row will bump
// last_seen_at and cause this DELETE to match 0 rows, returning (false, nil).
// Caller should treat (false, nil) as "frame got refreshed, skip this sweep".
func (s *FramesStore) DeleteIfUnchanged(frameID string, lastSeenAt int64) (bool, error) {
    res, err := s.db.Exec(`DELETE FROM agent_frames WHERE frame_id = ? AND last_seen_at = ?`, frameID, lastSeenAt)
    if err != nil {
        return false, err
    }
    affected, err := res.RowsAffected()
    if err != nil {
        return false, err
    }
    return affected > 0, nil
}
```

### 1.8 Sweep 的 clearFrame 拆分（PR-2b 重構）

當前 `clearFrame`：`m.frames.Delete(frameID)` + side effects（legacy event clean + broadcast + orphan watcher clean）。

拆兩半：
- `Delete(frameID)` 既有路徑維持給 `pid_dead`/`pid_reused`
- `DeleteIfUnchanged(...)` 給 `idle_timeout`
- 共用 side effects：抽成 `afterFrameCleared(frame, reason)`

實作：
```go
func (m *Module) clearFrame(frame store.Frame, reason string) error {
    if m.frames == nil {
        return nil
    }
    if err := m.frames.Delete(frame.FrameID); err != nil {
        return err
    }
    return m.afterFrameCleared(frame, reason)
}

func (m *Module) afterFrameCleared(frame store.Frame, reason string) error {
    sessionName, code := m.resolvePaneSession(frame.PaneID)
    if sessionName != "" && m.events != nil {
        if err := m.events.Delete(sessionName); err != nil {
            return err
        }
    }
    projection, err := m.projectionForSession(sessionName)
    if err != nil {
        return err
    }

    // Orphan watcher cleanup — for both pid_dead/pid_reused (existing bug fix)
    // and idle_timeout (new).
    var hadWatcher bool
    var watcherAgentType string
    m.mu.Lock()
    if sessionName != "" {
        syncProjectionState(m.currentStatus, m.subagents, sessionName, projection)
        if projection == nil || projection.TopFrame == nil {
            watcherAgentType, hadWatcher = m.activeWatchers[sessionName]
            delete(m.activeWatchers, sessionName)
        }
    }
    m.mu.Unlock()
    _ = watcherAgentType // only used to indicate we had one
    if hadWatcher && m.prober != nil {
        m.prober.StopWatch(sessionName + ":")
    }

    if code == "" || m.core == nil {
        return nil
    }
    normalized := buildProjectionNormalized(projection, frame.AgentType, "sweep:"+reason, nowFn().UnixNano(), agentpkg.DeriveResult{})
    payload, _ := json.Marshal(normalized)
    m.core.Events.Broadcast(code, "hook", string(payload))
    return nil
}
```

**順便修的既有 bug**（對 codex #3 + 原 plan §1.7 延續）：pid_dead / pid_reused 清 frame 時本來不呼叫 `prober.StopWatch`，僅 delete activeWatchers map 條目；goroutine 留在 prober 裡（onActivityDetected 回來時會 early-return，但 resource leak）。本次順便補。

### 1.9 SessionProjection.Subagents 升級（PR-2a）

`internal/module/agent/projection.go`:

- `SessionProjection.Subagents []string` → `[]agentpkg.SubagentRef`
- `buildPaneProjection` defensive copy 跟著升級

### 1.10 NormalizedEvent.Subagents 升級（PR-2a，WS wire break）

`internal/agent/status.go`:

```go
type NormalizedEvent struct {
    // ...
    Subagents []SubagentRef `json:"subagents"`
    // ...
}
```

- `handler.go` `buildNormalized` + `frame_ops.go` `buildProjectionNormalized` + `module.go` `onActivityDetected` 三處 NormalizedEvent 建構全跟上

**Wire compatibility**（對 codex #4 使用者決策的註記）：
- Daemon + SPA 同步一次升級（單 PR-2a 同時動 Go + TS）
- Electron dev update 若先升一邊（例 SPA HMR 先吃新型別但 daemon 仍送舊 wire），短暫 payload mismatch → SPA 顯示空 subagents（或 TS runtime warning）
- 使用者場景（單人）可接受；bump PR 時協調一次：先 daemon 重啟 → 再 SPA reload

### 1.11 m.subagents map 升級（PR-2a）

`internal/module/agent/module.go`:

- `subagents map[string][]string` → `map[string][]agentpkg.SubagentRef`
- `syncProjectionState` 參數型別同步
- `replayFromDB` / `sendSnapshot` 透過 `syncProjectionState` 間接升級

### 1.12 SPA 型別升級（PR-2a）

`spa/src/stores/useAgentStore.ts`:

```ts
export interface SubagentRef {
  id: string
  type: string
  started_at: number
  source_pid: number
  source_start_time: string
  is_proxy?: boolean
}

export interface NormalizedEvent {
  // ...
  subagents?: SubagentRef[]
}

interface AgentState {
  // ...
  subagents: Record<string, SubagentRef[]>
  // ...
}
```

`handleNormalizedEvent` 內 `if (event.subagents)` 分支邏輯不變。

### 1.13 SubagentDots 視覺升級（PR-2b）

`spa/src/components/SubagentDots.tsx`:

API：`{ count: number }` → `{ refs: SubagentRef[] }`

```ts
interface Props {
  refs: SubagentRef[]
  left?: number
}
```

視覺規則（Phase 2 最小）：
- Dot 數依 `refs.length` clamp 到 [0, 3]
- **Type 色**：每個 dot 依 `ref.type` 決定 backgroundColor；查表 inline：
  - `cc → #60a5fa` (blue 既有)
  - `codex → #facc15` (yellow)
  - `opencode → #f97316` (orange)
  - fallback → `#60a5fa`
- **Proxy 樣式**：`ref.is_proxy === true` → dot 用 outline（`backgroundColor: transparent` + `border: 1px solid <typeColor>`）；proxy=false/undefined → 實心不變
- `animationDelay` 公式不變

**TabIcon.tsx**（PR-2b）：`subagentCount: number` → `subagentRefs: SubagentRef[]`；dot/iconDot/badge 三模式全改吃 list；`refs.length > 0` 取代 `count > 0`。

**useTabDisplay.ts**（PR-2a 先改 `subagentCount = refs.length`，保 TabIcon 既有 prop；PR-2b 回傳 `subagentRefs`）：
- PR-2a：`useAgentStore((s) => s.subagents[ck]?.length ?? 0)` 改為從 `SubagentRef[]?.length`（型別自動變，行為不變）
- PR-2b：新增 `subagentRefs` 回傳欄位

**SubagentDots 測試**：保留既有 count-based case 概念（refs 長度取代 count input），新加兩個 case — type 色差 + proxy outline。

### 1.14 零改動邊界

以下檔案 Phase 2 **不得觸碰**：

- `internal/agent/provider.go` / `registry.go` / `coverage.go`（Phase 0）
- `internal/agent/{cc,codex,opencode}/*.go`（Phase 1 已對齊；SubagentStart detail 已備齊 `agent_id`）
- `internal/agent/probe/**`
- `internal/store/frames.go` SQL schema（column 不變；JSON shape 變）
- `internal/module/agent/verify.go` / `upload*.go` / `monitor.go` / `statusline*.go`
- `internal/agent/status.go` 除 NormalizedEvent.Subagents 型別
- `spa/src/**` 除 `useAgentStore.ts` / `SubagentDots.tsx` / `TabIcon.tsx` / `useTabDisplay.ts` + 測試 seed 檔
- `/api/agent/monitor/*` endpoint shape（Phase 5 Inspector 統一）

---

## 2. 測試案例清單

### 2.1 SubagentRef JSON round-trip（PR-2a）

`internal/agent/subagent_test.go`（新檔）：

| # | 名稱 | 斷言 |
|---|---|---|
| R1 | `TestSubagentRef_JSONRoundTripFull` | `{ID:"a", Type:"cc", StartedAt:123, SourcePID:1000, SourceStartTime:"t0", IsProxy:true}` marshal/unmarshal 相等；JSON 含 `"source_pid"`/`"source_start_time"`/`"is_proxy":true` |
| R2 | `TestSubagentRef_OmitsIsProxyWhenFalse` | `IsProxy:false` marshal JSON 不含 `is_proxy` key |
| R3 | `TestSubagentRef_NativeZeroSourceFieldsValid` | `{ID:"a", Type:"cc", StartedAt:1}` (SourcePID=0, SourceStartTime="") marshal/unmarshal OK — JSON 含 `"source_pid":0` / `"source_start_time":""` |

### 2.2 Frame store 升級（PR-2a）

`internal/store/frames_test.go`:

| # | 名稱 | 斷言 |
|---|---|---|
| F1 | `TestFrames_UpsertAndReadSubagentRefs` | Upsert `[]SubagentRef{{ID:"s1", Type:"cc", StartedAt:10}}` → GetByIdentity 讀回同值 |
| F2 | `TestFrames_EmptySubagentsPreserved` | Upsert `nil` Subagents → 讀回 `[]SubagentRef{}`（非 nil） |
| F3 | `TestFrames_SubagentsJSONShapeSmoke` | 讀回 frame 的 raw JSON 字串含 `"id"`、`"type"`、`"started_at"`、`"source_pid"`、`"source_start_time"` |

### 2.3 `DeleteIfUnchanged`（PR-2b）

`internal/store/frames_test.go`:

| # | 名稱 | 斷言 |
|---|---|---|
| F4 | `TestFrames_DeleteIfUnchanged_DeletesWhenMatching` | Upsert LastSeenAt=10 → DeleteIfUnchanged(id, 10) → returns (true, nil)，frame 消失 |
| F5 | `TestFrames_DeleteIfUnchanged_SkipsWhenStale` | Upsert LastSeenAt=10 → Upsert LastSeenAt=20 (concurrent refresh) → DeleteIfUnchanged(id, 10) → returns (false, nil)，frame 仍在 |
| F6 | `TestFrames_DeleteIfUnchanged_NotFound` | 不存在 frame id → returns (false, nil) |

### 2.4 `updateSubagents` 單元（PR-2a）

`internal/module/agent/frame_ops_test.go`:

| # | 名稱 | 斷言 |
|---|---|---|
| U1 | `TestUpdateSubagents_StartAddsRef` | 空 list + SubagentStart ref{ID:"a"} → list=[{ID:"a"}] |
| U2 | `TestUpdateSubagents_StartDuplicateIDKeepsExisting` | 已有 `{ID:"a", StartedAt:10}` + SubagentStart `{ID:"a", StartedAt:20}` → list 仍 1 筆，StartedAt=10 |
| U3 | `TestUpdateSubagents_StopRemovesByID` | list `[a, b]` + SubagentStop `{ID:"a"}` → list=[b] |
| U4 | `TestUpdateSubagents_StopIgnoresType` | list `[{ID:"a", Type:"cc"}]` + SubagentStop `{ID:"a", Type:"codex"}` → list=[]（Type 不參與比對） |
| U5 | `TestUpdateSubagents_StopMissingIsNoop` | list `[a]` + SubagentStop `{ID:"b"}` → list=[a] |

### 2.5 Proxy 偵測整合（PR-2b）

`internal/module/agent/frame_ops_test.go`:

| # | 名稱 | 情境 | 斷言 |
|---|---|---|---|
| PR1 | `TestProxySubagent_CodexHookWithCCPPIDAttachesToCCParent` | cc SessionStart (PID 100, pane %5) → codex SessionStart (PID 200, PPID 100, pane %5) | pane 有 1 frame（cc），cc.Subagents 有 ref `{Type:"codex", IsProxy:true, SourcePID:200, SourceStartTime:"..."}` |
| PR2 | `TestProxySubagent_SkipsWhenPPIDNotMatchFrame` | codex SessionStart (PPID=9999, pane 無 frame with PID 9999) | 建新 codex frame（fallback），不 proxy |
| PR3 | `TestProxySubagent_SkipsWhenParentSameType` | cc SessionStart → cc SessionStart 不同 pid/start，PPID 指第一個 cc | 建新 cc frame，不 proxy（same type） |
| PR4 | `TestProxySubagent_SkipsWhenParentPidDead` | cc frame 存在但 isPidAliveFn 回 false for cc PID → codex SessionStart PPID=cc.PID | 建新 codex frame，不 proxy |
| PR5 | `TestProxySubagent_SkipsWhenEventNotSessionStart` | cc frame 存在 → codex UserPromptSubmit PPID=cc.PID | 走 legacy 路徑（目前可能建 frame 可能不建，依 DeriveStatus result；不偏移現狀）；驗證不走 proxy 路徑 |
| PR6 | `TestProxySubagent_TraceMetaCorrect` | PR1 情境 | trace meta decision="updated_frame"、reason="proxy_subagent_attached"、FrameID=parent.FrameID |
| PR7 | `TestProxySubagent_DoesNotDoubleAttachOnReHook` | PR1 後再送同 codex SessionStart (same PID+StartTime) | cc.Subagents 仍 1 筆，StartedAt 為首次時間 |

### 2.6 SessionEnd Proxy Cleanup（PR-2b）

`internal/module/agent/frame_ops_test.go`:

| # | 名稱 | 斷言 |
|---|---|---|
| SE1 | `TestSessionEnd_RemovesProxyRefFromParent` | PR1 setup + codex SessionEnd (同 PID/StartTime) → cc.Subagents 變空；trace reason="proxy_subagent_detached" |
| SE2 | `TestSessionEnd_OrphanFallsBackToExistingSkip` | codex SessionEnd (pane 內無匹配 SourcePID frame/ref) → 走 session_end_without_frame |
| SE3 | `TestSessionEnd_OwnFrameDeletePreservesOtherProxyRefs` | parent frame A 有 proxy ref of codex B；SessionEnd 送給 A 本身 → A frame 刪除（既有），ref 跟著刪（副作用 OK） |

### 2.7 Frame Idle Sweep（PR-2b）

`internal/module/agent/sweep_test.go`:

| # | 名稱 | 斷言 |
|---|---|---|
| IS1 | `TestSweep_ClearsIdleFramesByLastSeen` | Upsert frame LastSeenAt=0，nowFn 回 `2*frameIdleThreshold` → sweepOnce 後 frame 被清、clearFrame reason="idle_timeout" broadcast |
| IS2 | `TestSweep_PreservesFreshFrames` | LastSeenAt=nowFn-1min → sweepOnce 後 frame 保留 |
| IS3 | `TestSweep_IdleClearStopsOrphanWatcher` | frame idle + session 有 activeWatcher → clearFrame 後 prober.StopWatch 被呼叫 |
| IS4 | `TestSweep_DeadPidAlsoStopsOrphanWatcher` | 既有 pid_dead 路徑也觸發 StopWatch（順勢 fix 的迴歸測試） |
| IS5 | `TestSweep_IdleConditionalDeleteSkipsOnConcurrentRefresh` | 在 sweepOnce 執行中，模擬 sweep 已讀 LastSeenAt=0，但 DELETE 前 Upsert 把 LastSeenAt 改成 nowFn-30min → DeleteIfUnchanged 回 (false, nil)，frame 仍在；測試用 fake store wrap `FramesStore`，在 ListAll → DeleteIfUnchanged 之間插入 refresh |

### 2.8 Handler 整合：broadcast 含 SubagentRef（PR-2a + PR-2b 各一）

`internal/module/agent/handler_test.go`:

| # | 名稱 | PR | 斷言 |
|---|---|---|---|
| HB1 | `TestHandleEvent_BroadcastPayloadCarriesSubagentRefs` | PR-2a | cc SessionStart + SubagentStart → WS broadcast payload 解析後 `subagents[0].type=="cc"`、`started_at` 非 0、無 `is_proxy`（native） |
| HB2 | `TestHandleEvent_ProxyBroadcastCarriesIsProxyTrue` | PR-2b | cc SessionStart → codex SessionStart PPID=cc.PID → 第二次 broadcast `subagents[0].is_proxy==true, type=="codex", source_pid==codex.PID` |

### 2.9 SPA 測試升級（PR-2a + PR-2b）

- **PR-2a**：所有 seed `subagents: {}` / `subagents: { key: [...] }` 的測試檔升級 mock 為 `SubagentRef[]`
  - `SortableTab.test.tsx` / `StatusBar.test.tsx` / `TerminalView.test.tsx` / `HookModuleCard.test.tsx` / `useNotificationDispatcher.test.ts` / `useTabDisplay.test.ts`
- **PR-2b**：`SubagentDots.test.tsx`（新檔）：
  - `TestSubagentDots_Count`：refs 1/2/3 → 對應 dot 數
  - `TestSubagentDots_TypeColors`：三家 type → 三個不同 backgroundColor
  - `TestSubagentDots_ProxyOutline`：ref with `is_proxy:true` → border 1px + backgroundColor transparent；`is_proxy:false/undefined` → solid background

---

## 3. TDD 執行順序

### PR-2a（commits 1-5）

#### Commit 1 — `feat(agent): add SubagentRef type with source identity`
- 紅：R1 + R2 + R3 → 失敗
- 綠：新檔 `internal/agent/subagent.go`
- 跑 `go test ./internal/agent/...` 綠

#### Commit 2 — `refactor(store+agent): atomic SubagentRef schema upgrade`
**對 codex #5 修正 — 原 plan 的 commit 2+3 合併為單一 atomic commit，保 `go build ./...` 在 commit 邊界綠。**
- 紅：F1-F3 + U1-U5 → 失敗
- 綠：同一 commit 完成：
  - `internal/store/frames.go` Frame.Subagents 型別升級
  - `internal/module/agent/frame_ops.go` updateSubagents 簽名 + 建構 ref 的三處
  - `internal/module/agent/projection.go` SessionProjection.Subagents 型別
  - `internal/module/agent/module.go` m.subagents map 型別
  - 所有既有 test seed 的 `Subagents: []string{...}` 改為 `[]SubagentRef{...}`
- 跑 `go build ./...` + `go test ./...` 綠

#### Commit 3 — `refactor(agent): upgrade NormalizedEvent.Subagents wire`
- 紅：HB1（PR-2a 版 — 不含 IsProxy） → 失敗
- 綠：`internal/agent/status.go` NormalizedEvent.Subagents 型別；handler.go `buildNormalized` / frame_ops.go `buildProjectionNormalized` / module.go 三處跟上
- 跑 `go test ./...` 綠

#### Commit 4 — `refactor(spa): upgrade useAgentStore subagents to SubagentRef[]`
- 紅：SPA 編譯錯誤（Record 型別 mismatch）
- 綠：`useAgentStore.ts` SubagentRef + NormalizedEvent + subagents Record 升級；既有 store 測試 seed 改
- `useTabDisplay.ts` 維持 `subagentCount = refs.length`（行為不變，只吃新型別）
- 跑 `cd spa && pnpm run lint && pnpm run build && npx vitest run` 綠

#### Commit 5 — `test(spa): update subagents mocks across seed sites`
- 紅：若上個 commit 遺漏測試檔 seed → 綠
- 綠：掃全 SPA 測試檔規範化 subagents seed 為 `SubagentRef[]`
- 跑 `cd spa && pnpm run lint && pnpm run build && npx vitest run` 綠

（PR-2a 結束：純 schema + 型別升級，行為無變化）

### PR-2b（commits 6-10）

#### Commit 6 — `feat(store): add DeleteIfUnchanged for optimistic sweep`
- 紅：F4 + F5 + F6 → 失敗
- 綠：`frames.go` 新增 method
- 跑 `go test ./internal/store/...` 綠

#### Commit 7 — `feat(module/agent): detect proxy subagents via PPID match`
- 紅：PR1-PR7 → 失敗
- 綠：`frame_ops.go` applyFrameEvent 加 proxy 偵測分支（PPID-first）+ proxyIDFor helper
- 跑 `go test ./internal/module/agent/...` 綠

#### Commit 8 — `feat(module/agent): remove proxy ref on SessionEnd`
- 紅：SE1 + SE2 + SE3 → 失敗
- 綠：`frame_ops.go` SessionEnd 分支加 `removeProxyRefForSender` fallback
- 跑 `go test ./internal/module/agent/...` 綠

#### Commit 9 — `feat(module/agent): idle sweep with conditional DELETE`
- 紅：IS1-IS5 + HB2 → 失敗
- 綠：`sweep.go` 加 `nowFn` + `frameIdleThreshold` + 第三條規則（用 `DeleteIfUnchanged`）+ `afterFrameCleared` 抽出 + orphan StopWatch 修正
- 跑 `go test ./internal/module/agent/...` 綠

#### Commit 10 — `feat(spa): SubagentDots takes SubagentRef[] with type color + proxy outline`
- 紅：SubagentDots.test.tsx 三個 case → 失敗
- 綠：SubagentDots API 升級；TabIcon 改吃 refs；useTabDisplay 回傳 refs
- 跑 `cd spa && pnpm run lint && pnpm run build && npx vitest run` 綠

#### Commit 11 — `docs: phase 2 plan retrospective notes`（可選）
若 §3 順序與實際執行有偏差，補歷史 note（Phase 0/1 plan 同型）。

---

## 4. 實作檔案預估

### PR-2a 改動

| 檔案 | 動作 | 預估行數 |
|---|---|---|
| `internal/agent/subagent.go` | 新檔 type | +30 |
| `internal/agent/subagent_test.go` | 新檔 R1-R3 | +60 |
| `internal/agent/status.go` | NormalizedEvent.Subagents 型別 | ~2 |
| `internal/store/frames.go` | Frame.Subagents 型別 + nil | ~10 |
| `internal/store/frames_test.go` | F1-F3 + 既有 seed 改 | +80 |
| `internal/module/agent/frame_ops.go` | updateSubagents 簽名 + 三處 ref 建構 | ~40 |
| `internal/module/agent/frame_ops_test.go` | U1-U5 + 既有 seed 改 | +120 |
| `internal/module/agent/projection.go` | SessionProjection.Subagents | ~4 |
| `internal/module/agent/projection_test.go` | seed 改 | ~10 |
| `internal/module/agent/module.go` | m.subagents map 型別 | ~4 |
| `internal/module/agent/handler.go` | buildNormalized 型別 | ~4 |
| `internal/module/agent/handler_test.go` | HB1 + 既有 seed | +60 |
| `internal/module/agent/sweep_test.go` | 既有 seed 改（引入 SubagentRef） | +15 |
| `spa/src/stores/useAgentStore.ts` | SubagentRef + Record 型別 | +12 |
| `spa/src/components/*.test.tsx` (6 檔) | seed 改 | ~35 |
| `spa/src/hooks/*.test.ts` (2 檔) | seed 改 | ~12 |
| `docs/specs/2026-04-23-lights-rebuild-phase-2-plan.md` | 本檔 | +550 |
| **PR-2a 合計（不含 plan）** | | **~500 行** |

### PR-2b 改動

| 檔案 | 動作 | 預估行數 |
|---|---|---|
| `internal/store/frames.go` | `DeleteIfUnchanged` method | +20 |
| `internal/store/frames_test.go` | F4-F6 | +50 |
| `internal/module/agent/frame_ops.go` | proxy 偵測分支 + `removeProxyRefForSender` | +100 |
| `internal/module/agent/frame_ops_test.go` | PR1-PR7 + SE1-SE3 | +240 |
| `internal/module/agent/sweep.go` | nowFn + threshold + 第三條規則 + afterFrameCleared 抽出 + StopWatch fix | +50 |
| `internal/module/agent/sweep_test.go` | IS1-IS5 | +160 |
| `internal/module/agent/handler_test.go` | HB2 | +40 |
| `spa/src/components/SubagentDots.tsx` | API + type color + proxy outline | +40 (-20) |
| `spa/src/components/SubagentDots.test.tsx` | 新檔 3 case | +110 |
| `spa/src/components/TabIcon.tsx` | count → refs | ~15 |
| `spa/src/hooks/useTabDisplay.ts` | 回傳 subagentRefs | ~8 |
| **PR-2b 合計** | | **~800 行** |

### 總計

**~1300 行 code**（不含 plan）— 比 v1 的 950 多 350 行主要是 proxy identity 欄位擴充、SessionEnd cleanup、DeleteIfUnchanged、afterFrameCleared 抽出、測試擴充。

---

## 5. 不做項目清單（防自動擴展）

以下衝動一律拒絕 — PR description / commit message 中明確聲明：

- **不做** schema migration（使用者單人，rm DB 即可；CHANGELOG 標紅字）
- **不做** boot hard-fail 舊格式偵測（同上，成本 > 收益）
- **不加** `SubagentRef.Status` / `SubagentRef.LastSeenAt`（Phase 4/5 再評估）
- **不做** proxy ref 的獨立 liveness 檢查（Phase 3 / 4b；SessionEnd 清理已涵蓋大多數 case）
- **不做** PPID 樹狀遍歷（Phase 3/4 再加；單層 PPID 命中率實測先看）
- **不處理** 非 SessionStart 的 proxy 事件（Phase 3/4）
- **不改** `/api/agent/monitor/*` endpoint shape（Phase 5 Inspector 統一）
- **不重整** `handler.go` / `module.go`（Phase 2 是 frame_ops / sweep 局部增加）
- **不抽** `frameIdleThreshold` 為 runtime flag（const 表達即可）
- **不加** idle sweep 的 broadcast reason 細分（共用 clearFrame 通道，reason="idle_timeout"）
- **不做** SubagentDots > 3 refs overflow 處理（保留既有 clamp 0-3 行為）
- **不抽** SubagentDots 的 TYPE_COLOR 表到 `agent-icons.tsx`（inline 可讀即可）
- **不做** PR-2a 單獨的 SubagentDots 視覺升級（保 count-based；PR-2b 一次到位）

---

## 6. Subagent 開發指引

- **cwd 強制前綴**：每個 Bash 指令以 `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/lights-phase-2 && ` 開頭（依 `feedback_subagent_cwd_enforcement.md`）
- **分支策略**：
  - PR-2a 在 `worktree-lights-phase-2` 直接 commit（已 push）
  - PR-2b 等 PR-2a merge 後，主 session 從 main rebase 開新 worktree `lights-phase-2b`，subagent 在該 worktree 接 PR-2b commits
- **Commit message 格式**：Conventional Commits + 結尾 `Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>`
- **TDD 嚴格紅綠**：每個 commit 內先寫測試跑紅再實作跑綠；**每個 commit 邊界都 `go build ./...` 綠**（PR-2a commit 2 是 atomic）
- **Codex sandbox 限制**：依 `feedback_codex_sandbox_no_install.md`，subagent 在 worktree 內 `pnpm install` 可能失敗；由主 session 手動跑一次 install，subagent 只跑 vitest / lint / build
- **回報**：完成後回報 commit hash 列表 + `git log --oneline -15` + `go test ./...` + `cd spa && pnpm run lint && pnpm run build && npx vitest run` 完整輸出

---

## 7. 驗收清單

### PR-2a

- [ ] 5 個 commits 符合規範（1-5）
- [ ] **每個 commit 邊界** `go build ./...` 綠（對 codex #5 修正）
- [ ] `go test ./...` 綠（R1-R3 / F1-F3 / U1-U5 / HB1 共 12 測試）
- [ ] `go vet ./...` 無 warning
- [ ] `cd spa && pnpm run lint && pnpm run build && npx vitest run` 綠
- [ ] PR diff 檔案：§4 PR-2a 表格列出的檔案，**其餘不得出現**
- [ ] PR description 含「不做項目」聲明 + CHANGELOG 重建 DB 警告預覽
- [ ] 手動場景：
  - `rm ~/.purdex/...agent.sqlite`（或相關路徑）後 daemon 啟動正常
  - cc SessionStart + SubagentStart cc → SPA SubagentDots 顯示 1 dot（count-based 維持）
  - `subagents_json` DB 欄位 peek 為 `[{"id":"...","type":"cc","started_at":...,"source_pid":0,...}]` 結構

### PR-2b

- [ ] 5 個 commits 符合規範（6-10）
- [ ] 每個 commit 邊界 `go build ./...` + tests 綠
- [ ] `go test ./...` 綠（F4-F6 / PR1-PR7 / SE1-SE3 / IS1-IS5 / HB2 共 19 測試）
- [ ] `cd spa && pnpm run lint && pnpm run build && npx vitest run` 綠
- [ ] PR diff 檔案：§4 PR-2b 表格列出的檔案
- [ ] 手動場景：
  - cc 啟動 → cc pane 內跑 `/codex:*` → SPA 只顯示 1 個 frame（cc），cc.Subagents 含 codex ref，dot 黃色 outline
  - 手造 idle frame（臨時改 threshold 到 1s 或 DB 改 LastSeenAt）→ sweep 後 frame 消失
  - codex SessionEnd（proxy 來源）→ cc.Subagents 空、dot 消失
  - cc native SubagentStart（Task 工具觸發）→ SPA 顯示藍色實心 dot

---

## 8. 風險與緩解

| 風險 | 機率 | 影響 | 緩解 |
|---|---|---|---|
| 使用者升級後忘了重建 DB | 高 | daemon log noisy 500 | CHANGELOG 紅字 + bump PR description 明示；使用者重建即解 |
| PPID 單層 match 命中率低（codex-companion 中介） | 中-高 | proxy 偵測大量 fallback 建新 frame | 接受 — Phase 2 以「能驗證 UX 為目標」；Phase 3/4 補 tree walk |
| Idle 1h 太短（使用者長跑任務） | 低 | 活躍 frame 誤清 | LastSeenAt 由 hook 刷新；真正長跑有 activity watcher 維持；觀察 1-2 週再調 |
| SubagentDots 新視覺 a11y 退化 | 低 | 色盲看不出 proxy | outline vs solid 雙通道（形狀+顏色），符合 WCAG |
| 測試 seed 遺漏 CI 紅 | 中 | subagent 反覆修 | Commit 5 專跑 SPA seed sweep；主 session push 前跑全測 |
| SPA + daemon skew（Electron dev update 時差） | 中 | subagents 欄位型別短暫 mismatch | 使用者單人，協調一次升級 |
| DeleteIfUnchanged 在 SQLite 的 race 邊界 | 低 | `DELETE ... WHERE` 在 SQLite 下是 atomic，行為可靠 | modernc.org/sqlite 的 serialization 保證；IS5 測試覆蓋 |
| PPID 為 0（init process）導致 `FindByPanePID(0)` 誤中 | 低 | 不可能（init 不會是 pane 內 frame） | 但加 guard：`if info.PPID <= 1 { parentCandidate = nil }` |

---

## 9. 兩輪 Codex Review 預期 focus

### PR-2a 第一輪（標準）

- SubagentRef 欄位是否齊全（SourcePID/SourceStartTime 是否該 pointer? 還是零值 sentinel? 本 plan 選零值；可接受性）
- Frame.Subagents JSON upgrade 的 breaking edge（舊 DB read error 行為）
- updateSubagents 是否正確處理 native vs proxy ref 混合 list
- HB1 broadcast payload 型別 smoke
- TS SubagentRef 欄位型別對齊 Go json tags（snake_case 正確）

### PR-2a 第二輪 3 parallel

- **攻擊**：Frame.Subagents JSON 一但 corrupted（手改 DB，格式亂掉）scanFrame 行為；並行 Upsert 造成 subagents_json 覆寫 race；Concurrent syncProjectionState on m.subagents map 無鎖保護？
- **防守**：TS / Go wire 對齊（`is_proxy,omitempty` 在 TS 側 `?: boolean` 對應正確）；useTabDisplay 從 length 過渡是否保留既有行為
- **體質**：`frame_ops.go` 升級後是否 >500 行；PR-2a 是否真的無行為改變

### PR-2b 第一輪（標準）

- Proxy 偵測 PPID-first 條件是否完整（PPID<=1 guard？info.PPID=req.SenderPID 自環？）
- SessionEnd proxy cleanup scanning 是否遺漏 multi-pane case
- DeleteIfUnchanged race window 設計是否正確
- `afterFrameCleared` 抽出是否破壞 clearFrame 既有語意
- SubagentDots type color 表（cc/codex/opencode）後續維護

### PR-2b 第二輪 3 parallel

- **攻擊**：proxy 與 native SubagentStart 撞 ID（native agent_id 用 `proxy:` 開頭？）/ 多 pane 同 sender process（不太可能但 guard？）/ sweep 執行時 proxy attach 的 TOCTOU / LastSeenAt 溢位
- **防守**：proxy 語意對 cc-inside-cc 巢狀（same type 拒絕） / SessionEnd cleanup 對 multi-ref 同 SourcePID 的行為 / 為何不加 per-ref LastSeenAt
- **體質**：`frame_ops.go` + `sweep.go` 是否過大需拆；type color 表應否 per-module；SubagentDots testing 是否過度（11x case）

---

## 10. Plan v2 relative v1 的變動摘要

| v1 → v2 改動 | 原因 |
|---|---|
| SubagentRef 新增 `SourcePID` + `SourceStartTime` 欄位 | Codex #1: PID 重用 collision-safe |
| Proxy 偵測改 PPID-first（+ §1.4 rewrite） | Codex #2: pane-only 是回歸 |
| SessionEnd 清 proxy ref 分支新增（§1.5 rewrite） | Codex #1: 死 proxy 回收路徑 |
| Idle sweep 改 `DeleteIfUnchanged`（新 store method） | Codex #3: TOCTOU race |
| 移除 migration / boot hard-fail 討論；改 plain rebuild DB | 使用者決策 |
| Commit 2+3 合併為 atomic schema upgrade | Codex #5: 每 commit 綠 |
| `afterFrameCleared` 抽出；順便修 pid_dead/pid_reused 的 orphan watcher bug | 共用到 idle_timeout 路徑的意外收益 |
| **確定拆 PR-2a + PR-2b**（§0 重寫） | Codex 建議 + 使用者確認 |
| `PPID <= 1` guard | v2 自行補的邊界 |
| 新增 IS5 `conditional DELETE race` 測試 | Codex #3 修正配套 |

---

## 11. 執行起點

此 plan 送第二輪 codex adversarial review，確認 findings 收斂為 P2/P3 以下後：
1. 主 session 派 subagent 跑 PR-2a commits 1-5
2. PR-2a codex 兩輪 review（標準 + 3 parallel）→ 修 → merge → bump alpha.218（CHANGELOG 紅字）
3. 主 session 從 main rebase 開 worktree `lights-phase-2b`，派 subagent 跑 PR-2b commits 6-10
4. PR-2b codex 兩輪 review → 修 → merge → bump alpha.219
5. 更新 `kickoff_lights_rebuild.md` 標 Phase 2 ✅，下一步 Phase 3
