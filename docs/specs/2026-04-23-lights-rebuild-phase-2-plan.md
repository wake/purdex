# Phase 2 TDD Plan — L3 Subagent 升級 + Proxy + Frame Idle Sweep

- **Date**: 2026-04-24
- **Spec**: `docs/specs/2026-04-23-lights-rebuild-spec.md` §6
- **Worktree**: `lights-phase-2`（branch `worktree-lights-phase-2`）
- **依賴**: Phase 1（merged at `d1d60b2c`）+ Hook Events PR #616（merged at `fd9f8f8f`, alpha.217）
- **範圍**：`SubagentRef` 結構升級 + proxy 偵測 + frame idle sweep + SPA 型別 / 視覺升級

## 0. 量體與拆分判斷

**本 plan 寫成單 PR**，實際改動落地後再由 review 負擔判斷是否拆 PR-2a（schema 升級，commits 1-7）+ PR-2b（proxy + sweep，commits 8-10）。

拆分時機（任一命中即拆）：
- Plan 階段 codex review 回 12+ findings
- 實作完成後 `git diff --stat` > 1200 行（含測試）
- 單一 reviewer 讀完需要超過 30 分鐘的預估量

拆分形式：PR-2a schema-only（純改型別，行為不變），PR-2b 新行為（proxy 偵測 + idle sweep + SubagentDots 視覺）。

## 1. 契約鎖定

### 1.1 `SubagentRef` 型別（新增）

位置：`internal/agent/subagent.go`（新檔，放在 `internal/agent` package 根層，與 `status.go` 平行）— 放 store 層不當因為 Type 語意綁 agent registry。

```go
type SubagentRef struct {
    ID        string `json:"id"`
    Type      string `json:"type"`                  // agent type, e.g. "cc" / "codex" / "opencode"
    StartedAt int64  `json:"started_at"`            // UnixNano，沿用 frame broadcast_ts 口徑
    IsProxy   bool   `json:"is_proxy,omitempty"`    // true 代表跨 agent_type 的同 pane hook 被收編
}
```

**設計要點**：
- `ID` 語意：SubagentStart 分支 = `raw.agent_id`；proxy 分支 = `"proxy:<agent_type>:<pid>"` 合成字串（確保唯一；不是 UUID，可讀性優先）
- `Type` 語意：SubagentStart 分支 = 盡量取 `raw.agent_type`（opencode 有；cc / codex 沒有則 fallback 為 parent frame 的 `AgentType`）；proxy 分支 = `req.AgentType`（新 hook 的 agent type）
- `StartedAt`：**只在首次新增時設定**；重複 SubagentStart 同 ID 不覆寫（保持早期時間戳，供 UI 排序）。proxy 重複 hook 同樣不刷 StartedAt
- `IsProxy,omitempty`：JSON 僅在 true 時序列化，方便舊前端 / 測試 snapshot 穩定
- **不加** `LastSeenAt` 欄位（Phase 2 避免 scope 蔓延；idle sweep 只看 frame.LastSeenAt 整體）
- **不加** `Status` 欄位（SubagentStart hook 本來就 Status=""；Phase 2 保持 detail-only 語意）

### 1.2 Frame.Subagents 升級

`internal/store/frames.go`:

- `Frame.Subagents []string` → `[]agentpkg.SubagentRef`
- SQL schema（column `subagents_json`）不改 — JSON 形狀換掉即可（alpha 階段允許；使用者重建 DB 即得新格式）
- `scanFrame` 的 `json.Unmarshal` 從 `[]string` 改成 `[]SubagentRef`
- `Upsert` 的 `json.Marshal` 自動吃新型別
- **nil 防護不變**：`if frame.Subagents == nil { frame.Subagents = []SubagentRef{} }`（空 slice 不是 nil）

**Breaking change 範圍**：現有 DB 中 `subagents_json` 若是舊格式（`["id1","id2"]`），Unmarshal 會失敗。解法：alpha 階段要求使用者重建 DB，或加一條 `scanFrame` fallback（嘗試舊格式→轉成最小 SubagentRef{ID: s}）。**取前者**（spec §6 明文允許破壞式升級），避免 fallback 在 code 裡留住 deprecated 路徑。

### 1.3 `updateSubagents` 簽名升級

`internal/module/agent/frame_ops.go`:

```go
// before
func updateSubagents(current []string, eventName, agentID string) []string

// after
func updateSubagents(current []agentpkg.SubagentRef, eventName string, ref agentpkg.SubagentRef) []agentpkg.SubagentRef
```

行為：
- `SubagentStart`：若 `current` 無同 ID → append ref；若有同 ID → 不覆寫既有（保留既有 StartedAt / IsProxy）
- `SubagentStop`：filter 掉同 ID 的 ref（Type 不參與比對，避免 proxy→native 切換時漏刪）

在 `applyFrameEvent` 的 SubagentStart/Stop 分支組出 `ref`：
```go
ref := agentpkg.SubagentRef{
    ID:        agentID,                         // from result.Detail["agent_id"]
    Type:      firstNonEmpty(
        strFromDetail(result.Detail, "agent_type"),
        frame.AgentType,                        // fallback to parent frame's type
    ),
    StartedAt: broadcastTs,
    IsProxy:   false,
}
```

### 1.4 Proxy 偵測分支（新）

在 `applyFrameEvent` 當前「建/更 frame」主路徑**之前**插入 proxy 判斷：

**觸發條件（全部成立）**：
1. `req.EventName` 是 frame-creating event（`SessionStart`）— 只看 SessionStart，其他 event（UserPromptSubmit 等）若沒 frame 本就走 legacy 路徑，不是 Phase 2 範圍
2. `frame == nil`（同 identity pid/start 無既有 frame，表「新來的」）
3. `m.frames.ListByPane(req.TmuxPaneID)` 存在至少一個 frame（parent 候選）且**該 parent.AgentType ≠ req.AgentType**
4. 候選 parent 需 PID alive（避免把 proxy 掛到殭屍 parent 上；使用 `isPidAliveFn`）— 若 parent 已死，不算命中 proxy（走既有建 frame 路徑）

**命中行為**：
- 選最新 StartedAt 的 alive、type-不同 parent 作為收編對象
- 組 `ref = SubagentRef{ID: proxyIDFor(req), Type: req.AgentType, StartedAt: broadcastTs, IsProxy: true}`
- `parent.Subagents = updateSubagents(parent.Subagents, "SubagentStart", ref)`
- `parent.LastSeenAt = broadcastTs`
- `m.frames.Upsert(parent)` — **不建新 frame**
- Trace meta：`Decision="updated_frame", Reason="proxy_subagent_attached"`，`FrameID/ParentFrameID` 指 parent
- projection 回傳 parent pane 的投影

`proxyIDFor(req EventRequest) string`：`fmt.Sprintf("proxy:%s:%d", req.AgentType, req.SenderPID)` — PID + agent_type 在 pane 層級可唯一；跨 pane 重複 PID 無所謂（proxy ID 的作用域是 parent.Subagents，只有同 pane 能撞）。

**不命中條件**：
- parent.AgentType == req.AgentType → 真的是同類 re-session，走原路徑建 frame（例：cc exit 後 cc 再起）
- pane 無其他 frame → 獨立新 session，走原路徑建 frame
- 所有候選 parent PID 都死 → 走原路徑建 frame（死 parent 由 sweep 處理）

**非 SessionStart 的 proxy 事件（UserPromptSubmit 等）**：Phase 2 不處理。觀察實際行為若 codex proxy 沒先送 SessionStart 再開工，Phase 3/4 補。

### 1.5 SessionEnd 對 proxy 的處理

若 proxy ref 的來源 agent 送了 SessionEnd：
- `applyFrameEvent` 的 SessionEnd 分支先查 `frame := GetByIdentity(pane, pid, startTime)`
- proxy 的 SubagentRef 沒有獨立 frame（掛在 parent），所以這條查詢會 `frame == nil`
- 對應到現有的 `session_end_without_frame` 分支 — Phase 2 不改這條
- 結果：proxy ref 不會被 SessionEnd 自動清，由 **parent frame 清理時連帶清**（parent SessionEnd / pid_dead / idle_timeout）或 **sweep 時 ref.ID 對應的 PID 死掉**（見 §1.6）

**Phase 2 不做**：proxy ref 的獨立 liveness 檢查（掃 subagents 裡有無 proxy ID 對應 dead PID 並移除）。原因：會把 sweep 改大、且實務上 parent frame 掛掉時 proxy ref 自然走 — 留 Phase 3 評估必要性。

### 1.6 Frame Idle Sweep 規則（新）

`internal/module/agent/sweep.go`：

```go
const frameIdleThreshold = 1 * time.Hour
```

`sweepOnce` 迴圈每個 frame 檢查順序（插入現有兩條之後第三條）：
1. `!isPidAliveFn(frame.PID)` → `clearFrame(frame, "pid_dead")` — 既有
2. `processStartTimeFn(frame.PID) != frame.ProcessStartTime` → `clearFrame(frame, "pid_reused")` — 既有
3. **新**：`time.Now().UnixNano() - frame.LastSeenAt > frameIdleThreshold.Nanoseconds()` → `clearFrame(frame, "idle_timeout")`

注入測試時鐘：新增 `var nowFn = time.Now`（與 `isPidAliveFn` / `processStartTimeFn` 同 pattern），測試 swap。

**注意**：LastSeenAt 是 UnixNano（見 `broadcastTs` 呼叫點），`time.Duration.Nanoseconds()` 可直接比較。

### 1.7 Clear Frame 的 Orphan Watcher 清理（已部分存在，補強）

既有 `clearFrame` 已經 `delete(m.activeWatchers, sessionName)`（module.go clear 流程已做）但**沒有呼叫 `prober.StopWatch`**。補一行：

```go
// sweep.go clearFrame 裡 m.mu.Unlock() 之後：
if hadWatcher && m.prober != nil {
    m.prober.StopWatch(sessionName + ":")
}
```

其中 `hadWatcher` 在 `m.mu.Lock()` 區塊內取 `_, hadWatcher := m.activeWatchers[sessionName]` 後再 delete。

**為何這個 fix 重要**：idle_timeout 清 frame 時，session 可能有 active watcher（waiting/running/idle 任一狀態觸發過）。不 StopWatch → watcher goroutine 漏留在 prober 裡，畫面變化還在餵空 session → 回 onActivityDetected 時 activeWatchers 已 delete 就 early-return（既有防護），功能上 OK，但資源 leak。

pid_dead / pid_reused 既有路徑也吃到這個修正（順勢清乾淨）— 非 Phase 2 新增 scope，是既有 bug 一併修。

### 1.8 SessionProjection.Subagents 升級

`internal/module/agent/projection.go`:

- `SessionProjection.Subagents []string` → `[]agentpkg.SubagentRef`
- `buildPaneProjection` 的 defensive copy 跟著升級

### 1.9 NormalizedEvent.Subagents 升級（WS wire）

`internal/agent/status.go`:

- `NormalizedEvent.Subagents []string` → `[]SubagentRef` — 注意 JSON tag 保持 `"subagents"` 一致
- `handler.go` `buildNormalized` + `frame_ops.go` `buildProjectionNormalized` + `module.go` `onActivityDetected` 裡三處建構 NormalizedEvent 的 copy 全跟著升級

**Wire compatibility**：舊 SPA 期望 `subagents: string[]`，新 daemon 會送 `subagents: SubagentRef[]`。Alpha 階段 daemon + SPA 同步升級一次到位，不留過渡層。Electron dev update 會先升級 daemon 或 SPA 之一 → 短暫的型別 mismatch → SPA 顯示空 subagents / throw（運行時測試會抓到，接受）。

### 1.10 m.subagents map 升級

`internal/module/agent/module.go`:

- `subagents map[string][]string` → `map[string][]agentpkg.SubagentRef`
- `syncProjectionState` 的 map 參數型別同步升級
- `replayFromDB` / `sendSnapshot` 透過 `syncProjectionState` 間接升級，無額外改動

### 1.11 SPA 型別升級

`spa/src/stores/useAgentStore.ts`:

```ts
export interface SubagentRef {
  id: string
  type: string
  started_at: number
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

`handleNormalizedEvent` 內 `if (event.subagents)` 分支邏輯不變（仍是 length>0 set / length==0 delete）。型別自動跟著 shift。

### 1.12 SubagentDots 視覺升級

`spa/src/components/SubagentDots.tsx`:

API 從 `{ count: number }` 改為 `{ refs: SubagentRef[] }`：

```ts
interface Props {
  refs: SubagentRef[]
  left?: number
}
```

視覺規則（Phase 2 最小）：
- Dot 數仍依 `refs.length`（最多 3）
- **Type 色**：每個 dot 依其 ref.type 決定顏色；查表：`cc → #60a5fa`（藍，既有）/ `codex → #facc15`（黃）/ `opencode → #f97316`（橘）/ fallback `#60a5fa`
- **Proxy 樣式**：`is_proxy === true` → dot 用 outline（背景 transparent + 1px solid type color）而非實心；proxy=false → 實心不變
- `animationDelay` 公式不變

**TabIcon.tsx**：`subagentCount: number` prop → `subagentRefs: SubagentRef[]` prop；dot 模式 / iconDot 模式 / badge 模式全改吃 list；`refs.length > 0` 取代 `count > 0`。

**useTabDisplay.ts**：`subagentCount = subagents[ck]?.length ?? 0` → `subagentRefs = subagents[ck] ?? []`；回傳值加 `subagentRefs` 欄位或改名（傾向改名，count 拿掉）。

**SubagentDots 測試**：保留既有 count-based 測試，但輸入改建 mock refs；新加兩個 case — type 色差 + proxy outline。

### 1.13 零改動邊界

以下檔案 Phase 2 **不得觸碰**：

- `internal/agent/provider.go` / `registry.go` / `status.go`（除 NormalizedEvent 欄位升級）/ `coverage.go`
- `internal/agent/{cc,codex,opencode}/*.go`（SubagentStart detail 已備齊 `agent_id`）
- `internal/agent/probe/**`
- `internal/store/frames.go` 的 SQL schema（column 不變；僅 JSON shape 變）
- `internal/module/agent/verify.go` / `upload*.go` / `monitor.go` / `statusline*.go`
- `spa/src/**` 除 `useAgentStore.ts` / `SubagentDots.tsx` / `TabIcon.tsx` / `useTabDisplay.ts` + 測試 seed 檔外不動
- `/api/agent/monitor/*` endpoint shape — 不改（Phase 5 Inspector 再統一）

## 2. 測試案例清單

### 2.1 SubagentRef JSON round-trip

`internal/agent/subagent_test.go`（新檔）：

| # | 名稱 | 斷言 |
|---|---|---|
| R1 | `TestSubagentRef_JSONRoundTrip` | `{ID:"a", Type:"cc", StartedAt:123, IsProxy:true}` marshal/unmarshal 相等；JSON bytes 含 `"is_proxy":true` |
| R2 | `TestSubagentRef_OmitsIsProxyWhenFalse` | `IsProxy:false` 的 marshal JSON bytes **不含** `is_proxy` key（omitempty 驗證） |

### 2.2 Frame store 升級

`internal/store/frames_test.go` 補：

| # | 名稱 | 斷言 |
|---|---|---|
| F1 | `TestFrames_UpsertAndReadSubagentRefs` | Upsert 帶 `[]SubagentRef{{ID:"s1", Type:"cc", StartedAt:10}}` → GetByIdentity 讀回同值 |
| F2 | `TestFrames_EmptySubagentsPreserved` | Upsert 帶 `nil` Subagents → 讀回 `[]SubagentRef{}`（非 nil） |
| F3 | `TestFrames_SubagentsJSONFormatContainsExpectedKeys` | 讀回 frame 後 marshal `[]SubagentRef` 的 JSON 字串包含 `"id"` / `"type"` / `"started_at"`（smoke test 對齊 §1.1） |

既有測試涉及 `Subagents: []string{...}` seed 全改為 `[]SubagentRef{...}`。

### 2.3 updateSubagents 單元

`internal/module/agent/frame_ops_test.go` 補（或分檔 `subagents_test.go`）：

| # | 名稱 | 斷言 |
|---|---|---|
| U1 | `TestUpdateSubagents_StartAddsRef` | 空 list + SubagentStart ref → list 有 1 個 ref |
| U2 | `TestUpdateSubagents_StartDuplicateIDKeepsExisting` | 已有 `{ID:"a", StartedAt:10}` + SubagentStart `{ID:"a", StartedAt:20}` → list 仍為 1 個，StartedAt=10（不覆寫） |
| U3 | `TestUpdateSubagents_StopRemovesByID` | list `[a, b]` + SubagentStop `{ID:"a"}` → list = `[b]` |
| U4 | `TestUpdateSubagents_StopIgnoresType` | list `[{ID:"a", Type:"cc"}]` + SubagentStop `{ID:"a", Type:"codex"}` → list 為空（Type 不參與比對） |
| U5 | `TestUpdateSubagents_StopMissingIsNoop` | list `[a]` + SubagentStop `{ID:"b"}` → list = `[a]` |

### 2.4 Proxy 偵測整合

`internal/module/agent/frame_ops_test.go`:

| # | 名稱 | 情境 | 斷言 |
|---|---|---|---|
| PR1 | `TestProxySubagent_CodexHookAttachesToCCParent` | cc SessionStart → codex SessionStart 同 pane | pane 只有 1 個 frame（cc），cc.Subagents 有一筆 `{Type:"codex", IsProxy:true}` |
| PR2 | `TestProxySubagent_SkipsWhenParentSameType` | cc SessionStart → cc SessionStart 同 pane 但不同 pid/start | pane 有 2 個 frame（新舊 cc），不觸發 proxy |
| PR3 | `TestProxySubagent_SkipsWhenParentPidDead` | cc frame 已存在但 isPidAliveFn 回 false → codex SessionStart | 走既有路徑建新 codex frame，不觸發 proxy |
| PR4 | `TestProxySubagent_SkipsWhenNoParent` | 空 pane → codex SessionStart | 建新 codex frame |
| PR5 | `TestProxySubagent_TraceMetaCorrect` | PR1 情境 | trace meta decision="updated_frame", reason="proxy_subagent_attached", FrameID=parent.FrameID |
| PR6 | `TestProxySubagent_DoesNotDoubleAttachOnReHook` | PR1 後再送一次 codex SessionStart（同 PID）| cc.Subagents 仍為 1 筆，StartedAt 為首次時間 |

### 2.5 Frame Idle Sweep

`internal/module/agent/sweep_test.go`:

| # | 名稱 | 斷言 |
|---|---|---|
| IS1 | `TestSweep_ClearsIdleFramesByLastSeen` | Upsert frame LastSeenAt=0，nowFn 回固定 `2*frameIdleThreshold` → sweepOnce 後 frame 被清、clearFrame reason="idle_timeout" 走 broadcast |
| IS2 | `TestSweep_PreservesFreshFrames` | LastSeenAt=nowFn-1min → sweepOnce 後 frame 保留 |
| IS3 | `TestSweep_IdleClearStopsOrphanWatcher` | frame idle + session 有 activeWatcher → clearFrame 後 prober.StopWatch 被呼叫（fake prober 記 call） |
| IS4 | `TestSweep_DeadPidAlsoStopsOrphanWatcher` | 既有 pid_dead 路徑也觸發 StopWatch（順勢 fix 的迴歸測試） |

### 2.6 Handler 整合：broadcast 含 SubagentRef

`internal/module/agent/handler_test.go`:

| # | 名稱 | 斷言 |
|---|---|---|
| HB1 | `TestHandleEvent_BroadcastPayloadCarriesSubagentRefs` | SessionStart + SubagentStart cc → WS broadcast payload 解析後 `subagents[0]` 含 `type`、`started_at`、無 `is_proxy`（omitempty） |
| HB2 | `TestHandleEvent_ProxyBroadcastCarriesIsProxyTrue` | cc SessionStart → codex SessionStart → 第二次 broadcast `subagents[0].is_proxy==true, type=="codex"` |

### 2.7 SPA 測試升級

- `useAgentStore.test.ts`（若存在）+ 所有 seed `subagents: {}` / `subagents: { key: [...] }` 的測試檔更新 mock 為 `SubagentRef[]`
- `SubagentDots.test.tsx`（新檔）：
  - `TestSubagentDots_Count`：refs 1/2/3 → 對應 dot 數
  - `TestSubagentDots_TypeColors`：三家 type → 三個不同 backgroundColor
  - `TestSubagentDots_ProxyOutline`：ref with `is_proxy:true` → border 1px + background transparent；`is_proxy:false/undefined` → solid background
- `useTabDisplay.test.ts` line 160 的 `subagents: { 'h1:sc1': [{ id: 's1' }] as never }` 改為正規 SubagentRef

## 3. TDD 執行順序

Subagent 嚴格按以下順序執行；每個 step 一個 commit。Commit message 用 Conventional Commits + `Co-Authored-By: Claude Opus 4.7 (1M context)`。

### Commit 1 — `feat(agent): add SubagentRef type`
- 紅：寫 R1 + R2 → 失敗（type 不存在）
- 綠：新檔 `internal/agent/subagent.go`
- 跑 `go test ./internal/agent/...` 綠

### Commit 2 — `refactor(store): upgrade Frame.Subagents to []SubagentRef`
- 紅：寫 F1 + F2 + F3 → 失敗（型別不符）
- 綠：`internal/store/frames.go` 型別升級 + nil 防護；更新既有 frames_test seed
- 跑 `go test ./internal/store/...` 綠；`go build ./...` 期望**失敗**（frame_ops 仍吃 `[]string`）— 這是刻意的，下個 commit 接手

### Commit 3 — `refactor(module/agent): upgrade frame_ops/projection for SubagentRef`
- 紅：寫 U1-U5 → 失敗
- 綠：`frame_ops.go` updateSubagents 簽名升級；`projection.go` SessionProjection 升級；`m.subagents` map 升級；`buildProjectionNormalized` 跟上；既有 frame_ops_test / projection_test / sweep_test 的 seed 全改
- 跑 `go build ./...` + `go test ./internal/module/agent/... ./internal/store/...` 綠

### Commit 4 — `refactor(agent): upgrade NormalizedEvent.Subagents wire`
- 紅：HB1 期望 payload 新欄位 → 失敗
- 綠：`internal/agent/status.go` NormalizedEvent.Subagents 型別升級；handler.go `buildNormalized` 跟上
- 跑 `go test ./...` 綠

### Commit 5 — `refactor(spa): upgrade useAgentStore subagents to SubagentRef[]`
- 紅：SPA 編譯錯誤（型別 mismatch 會卡 lint/build）
- 綠：`useAgentStore.ts` SubagentRef + NormalizedEvent + subagents Record 升級；既有 store 測試 seed 改
- 跑 `cd spa && pnpm run lint && pnpm run build && npx vitest run` 綠（此 commit 結束時 TabIcon/SubagentDots 仍吃舊 count，下個 commit 修）

**註**：為了讓中間 commit 保持 SPA build 綠，commit 5 先把 `useTabDisplay.ts` 的 subagentCount 從 `length` 維持（SubagentRef[].length 仍 work），僅把 store 型別升級。Component API 升級留 commit 6。

### Commit 6 — `feat(spa): SubagentDots takes SubagentRef[] with type color + proxy outline`
- 紅：寫 SubagentDots.test.tsx 三個 case → 失敗
- 綠：SubagentDots API 升級；TabIcon 改吃 refs；useTabDisplay 回傳 refs
- 跑 `cd spa && pnpm run lint && pnpm run build && npx vitest run` 綠

### Commit 7 — `test(module/agent): schema升級 integration coverage`
- 紅：HB1 → 失敗（handler broadcast 尚未真實帶 SubagentRef，但上幾個 commit 已升級，實際應綠）
- 綠（驗證性）：跑過一次確認整合
- 如果 HB1 已在 commit 4 加入就併進去；這個 commit 可變可選

### Commit 8 — `feat(module/agent): detect proxy subagents for cross-type hooks`
- 紅：PR1-PR6 → 失敗
- 綠：`frame_ops.go` applyFrameEvent 加 proxy 偵測分支 + proxyIDFor helper
- 跑 `go test ./internal/module/agent/...` 綠

### Commit 9 — `feat(module/agent): sweep idle frames after 1h LastSeenAt`
- 紅：IS1-IS4 → 失敗
- 綠：`sweep.go` 加 `nowFn` + `frameIdleThreshold` const + 第三條規則 + `clearFrame` 補 StopWatch
- 跑 `go test ./internal/module/agent/...` 綠

### Commit 10 — `test(spa): update subagents mocks across seed sites`
- 紅：若仍有測試檔 seed `{ key: [{ id }] as never }` 舊格式 → 升級後會漏
- 綠：掃全 SPA 測試檔，把 subagents seed 規範化為 `SubagentRef[]`
- 跑 `cd spa && pnpm run lint && pnpm run build && npx vitest run` 綠

### Commit 11 — `docs: phase 2 plan retrospective notes`（可選）
若 §3 順序與實際執行偏差，補 Phase 0/1 plan 同型的歷史 note。

## 4. 實作檔案預估

| 檔案 | 動作 | 預估行數 |
|---|---|---|
| `internal/agent/subagent.go` | 新檔 type + comments | +25 |
| `internal/agent/subagent_test.go` | 新檔 R1+R2 | +40 |
| `internal/agent/status.go` | NormalizedEvent.Subagents 型別改 | +1 (-1) |
| `internal/store/frames.go` | Frame.Subagents 型別 + nil 防護 | ~10 |
| `internal/store/frames_test.go` | F1-F3 + 既有 seed 改 | +60 |
| `internal/module/agent/frame_ops.go` | updateSubagents 簽名 + proxy 分支 + helpers | +110 |
| `internal/module/agent/frame_ops_test.go` | U1-U5 + PR1-PR6 + 既有 seed 改 | +220 |
| `internal/module/agent/projection.go` | SessionProjection.Subagents 型別 | ~4 |
| `internal/module/agent/projection_test.go` | seed 改 | ~10 |
| `internal/module/agent/module.go` | m.subagents map 型別 | ~4 |
| `internal/module/agent/handler.go` | buildNormalized Subagents 型別 | ~4 |
| `internal/module/agent/handler_test.go` | HB1+HB2 + 既有 seed | +100 |
| `internal/module/agent/sweep.go` | nowFn + frameIdleThreshold + 規則 + StopWatch | +30 |
| `internal/module/agent/sweep_test.go` | IS1-IS4 + 既有 seed | +140 |
| `internal/module/agent/trace.go` | (若 summary 有 list) | 0 / ~5 |
| `spa/src/stores/useAgentStore.ts` | SubagentRef + Record 型別 | +10 |
| `spa/src/components/SubagentDots.tsx` | API + type colors + proxy outline | +40 (-20) |
| `spa/src/components/SubagentDots.test.tsx` | 新檔 3 cases | +110 |
| `spa/src/components/TabIcon.tsx` | count → refs | ~15 |
| `spa/src/hooks/useTabDisplay.ts` | refs 回傳 | ~8 |
| `spa/src/components/*.test.tsx` (6 檔) | seed 改 | ~30 |
| `spa/src/hooks/*.test.ts` (2 檔) | seed 改 | ~10 |
| `docs/specs/2026-04-23-lights-rebuild-phase-2-plan.md` | 本檔 | +500 |
| **合計（不含 plan）** | | **~950 行** |
| **合計（含 plan）** | | **~1450 行** |

## 5. 不做項目清單（防自動擴展）

以下衝動一律拒絕 — PR description / commit message 中明確聲明：

- **不加** SubagentRef.Status 欄位（SubagentStart 本來就 Status=""；待 Phase 4/5 有需求再評估）
- **不加** SubagentRef.LastSeenAt 欄位（idle 判斷用 frame.LastSeenAt；個別 ref 的活性 Phase 3/4 再看）
- **不寫** 舊格式 JSON fallback（alpha 階段重建 DB 即可，避免 deprecated 路徑留在 code）
- **不做** proxy ref 的獨立 liveness 檢查（留 Phase 3 / 4b）
- **不改** `/api/agent/monitor/*` endpoint shape（Phase 5 Inspector 統一）
- **不處理** 非 SessionStart 的 proxy 事件（codex proxy 實測都走 SessionStart；若不符再 Phase 3/4 補）
- **不重整** `handler.go` / `module.go`（proxy 偵測是 frame_ops 局部增加，不順手 refactor）
- **不抽** `frameIdleThreshold` 為 runtime flag（const 表達即可，實際觀察需要再改）
- **不加** idle sweep 的 broadcast reason 細分（與 `pid_dead` / `pid_reused` 共用 clearFrame 通道）
- **不做** SubagentDots 的 > 3 refs 的 overflow 處理（超過 3 仍只顯示 3，與既有 clamp 一致）
- **不抽** SubagentDots 的 TYPE_COLOR 表到 `agent-icons.tsx`（inline 即可，型別對應單純）

## 6. Subagent 開發指引

- **cwd 強制前綴**：每個 Bash 指令以 `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/lights-phase-2 && ` 開頭（依 `feedback_subagent_cwd_enforcement.md`）
- **分支**：已在 `worktree-lights-phase-2`，不另切；不 push（主 session 負責）
- **Commit message 格式**：Conventional Commits + 結尾加 `Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>`
- **TDD 嚴格紅綠**：每個 commit 內先寫測試跑紅再實作跑綠 — 不可批次寫完所有 test + 一次實作
- **中間 commit 可以 build 失敗**（commit 2 → 3 之間）：這是型別升級的必然；commit 3 結束必須 `go build ./...` + `go test ./...` 綠
- **SPA 每個 commit 都要 build 綠**：pnpm install 由主 session 處理一次，subagent 只跑 vitest / lint / build
- **回報**：完成後回報 commit hash 列表 + `git log --oneline -15` + `go test ./...` 完整輸出 + `cd spa && pnpm run lint && pnpm run build && npx vitest run` 結果
- **Codex sandbox 限制**：依 `feedback_codex_sandbox_no_install.md`，SPA 任務若 subagent 跑不動 pnpm install，由主 session 手動跑

## 7. 驗收清單（完整 Phase 2）

- [ ] 10-11 個 commits 符合 message 規範
- [ ] `go build ./...` 綠（每個 commit 結束時）
- [ ] `go test ./...` 綠（新增 R1-R2 / F1-F3 / U1-U5 / PR1-PR6 / IS1-IS4 / HB1-HB2 共 22 個測試）
- [ ] `go vet ./...` 無 warning
- [ ] `cd spa && pnpm run lint && pnpm run build` 綠
- [ ] `cd spa && npx vitest run` 綠（新增 SubagentDots 3 case + 舊檔 seed 升級）
- [ ] PR diff 涉及檔案：§4 表格列出的 ~22 檔 + plan 文件，**其餘不得出現**
- [ ] PR description 含「不做項目」§5 聲明 + 拆分判斷（§0）結論（本 PR 單推 or 拆成 2a/2b）
- [ ] 手動場景（PR description 測試清單）：
  - cc session 啟動 → `/codex:*` → codex 以 proxy attach 到 cc.Subagents（SPA 上只見 1 個 frame，subagent dot 帶黃色 outline）
  - 手造超過 1h 閒置 frame（DB 改 LastSeenAt 或臨時改 threshold）→ sweep 後 frame 消失
  - cc SubagentStart 仍照常（native subagent 藍色實心）
  - SPA 重新整理後狀態可 replay（frame.Subagents JSON 讀回正確）
- [ ] DB 重建注意：若升級過程使用者已有舊 `subagents_json` 格式，scanFrame 會回 error；需在 CHANGELOG / bump PR note 標示「alpha.218 要求重建 DB」

## 8. 風險與緩解

| 風險 | 機率 | 影響 | 緩解 |
|---|---|---|---|
| 舊 DB `subagents_json` 格式不相容 | 高 | 既有使用者升級後 scanFrame 失敗 | alpha 階段明示重建；bump PR CHANGELOG 標註；或加一次性 startup migration（Phase 2 不做，spec §6 允許破壞式升級） |
| proxy 偵測誤判（parent alive 但已非使用中） | 中 | codex hook 掛錯 parent | Phase 2 只看 alive PID；若使用者 reports 誤判再加時間窗 / PPID 驗證（spec §11-5） |
| frame_idle_threshold = 1h 太短（使用者長跑任務） | 中 | 活躍 frame 被誤清 | LastSeenAt 每次 hook 都更新；idle 1h 意味完全無 hook 1h；真正長跑任務應有 activity probe watcher 維持活性。觀察 1-2 週若仍誤清，放大到 4h |
| SPA + daemon 升級不同步（Electron dev update 先後） | 中 | 短暫 subagents 欄位型別 mismatch，畫面 dot 消失 | 接受（alpha 階段允許）；使用者重開即同步 |
| SubagentDots 新視覺 accessibility 退化（color-only proxy） | 低 | 色盲使用者看不出 proxy | Phase 2 用 outline vs solid（形狀 + 顏色雙通道），不是純顏色；符合 WCAG |
| 測試 seed 遺漏導致 CI 紅 | 中 | subagent 要反覆修 | Commit 10 專跑 SPA seed sweep；主 session commit push 前跑全測 |
| proxy ID `"proxy:<type>:<pid>"` 與 SubagentStart 的 agent_id 撞（理論） | 低 | SubagentStop 誤刪 proxy ref | agent_id 由 CLI 生成（通常 UUID 或 short-hash），不會撞 `proxy:` 前綴；若未來 CLI 改格式再評估 |

## 9. 兩輪 Codex Review 預期 focus

**第一輪（標準）**：
- SubagentRef JSON shape 是否穩定、omitempty 語意、wire 改 break 有無遺漏
- Proxy 偵測條件是否完整（§1.4 五條觸發 + 三條不命中）是否正確
- idle sweep 的 nowFn 注入是否干擾其他測試（並行 swap 風險）
- SubagentDots type color 表是否與 agent-icons 其他地方衝突
- Commit 2 刻意讓 `go build` 失敗的策略是否接受（vs. 合併 commit 2+3）

**第二輪 3 parallel**：
- **攻擊**：proxy 偵測 race（並發 hook 寫同 parent.Subagents）/ SubagentStart 同時 proxy 同 ID 撞 / sweep 執行中 handler 正好 upsert frame 的 time-of-check-to-time-of-use / LastSeenAt 溢位（long-lived frame） / Nil pointer on `m.prober` in sweep when prober not wired
- **防守**：proxy 語意（cross agent_type 的條件是否對「cc inside cc」這種嵌套成立？）/ SessionEnd 對 proxy 的處理策略是否正確（§1.5）/ 為什麼不處理非 SessionStart proxy 事件 / 為什麼不加 LastSeenAt per-ref / 破壞式 schema 升級對使用者可接受性
- **體質**：`frame_ops.go` 升級後是否 >400 行需拆 / `SubagentDots.tsx` + test 是否過大 / type color 表應不應該集中到 `agent-icons.tsx` 管理 / sweep.go 第三條規則是否 sweep 檔過胖需拆 idle_sweep.go

## 10. 拆分決策 checklist（plan review 後）

收到 codex plan review 回饋後，決定單 PR 或拆 2a+2b：

- [ ] findings 數 ≤ 10 且皆落在 §1.1-§1.7 的 schema / §1.8-§1.13 型別擴散 → 單 PR 可吞
- [ ] findings 有 ≥ 3 個集中在 §1.4 proxy 語意 或 §1.6 sweep 規則 → 拆 2b
- [ ] findings 觸及 WS wire compatibility（§1.9）且 reviewer 要求平滑升級 → 拆 2a 單獨 merge + bump 一次
- [ ] plan 本身 > 550 行 → 拆 2a/2b 兩份 plan 分跑 review

預設：寫完 plan 送一次 codex review，依回饋再決定。
