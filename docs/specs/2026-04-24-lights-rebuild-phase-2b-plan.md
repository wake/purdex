# Phase 2 PR-2b Delta Plan — Proxy + Idle Sweep + SubagentDots 視覺

Baseline：`1.0.0-alpha.218`（main @ PR-2a #622 squash `8bf955e7` + bump #624 `6b461c1b`）。
Worktree：`.claude/worktrees/lights-phase-2b`（branch `worktree-lights-phase-2b`）。

---

## 0. 這份 plan 的定位（必讀）

整個 Phase 2 的契約與 TDD 步驟 **已在 PR-2a plan 內完整定義**（經 5 輪 plan review + 4 輪 code review 收斂）。PR-2b 大量 reference 該文件：

**契約源文件**：`docs/specs/2026-04-23-lights-rebuild-phase-2-plan.md`（v6, 997 行）

| 主題 | 源章節 |
|---|---|
| PR-2a/2b 拆分策略 | §0 |
| `SubagentRef` 型別 | §1.1（**已在 PR-2a 落地，勿重新定義**） |
| Proxy 偵測 PPID 祖先鏈 walk（depth=5 + same-type hard-stop + start_time identity 三分支） | **§1.4** |
| SessionEnd proxy cleanup | **§1.5** |
| Frame idle sweep（1h + conditional DELETE） | **§1.6** |
| `FramesStore.DeleteIfUnchanged` 新 method | **§1.7** |
| `clearFrame` 拆分 + `afterFrameCleared` + orphan watcher fix | **§1.8** |
| SubagentDots API 升級（`count` → `refs`）+ type color + proxy outline | **§1.13** |
| 測試清單 F4-F6 / PR1-PR15 / SE1-SE3 / IS1-IS5 / HB2 | **§2.3, §2.5-2.9** |
| Commit 6-10 TDD 執行順序 | **§3（PR-2b 段）** |
| 行數預估 | §4（PR-2b 表） |
| 不做項目 | §5 |
| 驗收清單 | §7（PR-2b 段） |
| 風險清單 | §8 |
| Review focus 預期 | §9（PR-2b 段） |

**本 delta plan 的範圍**：只記錄 PR-2a 落地**之後**才確定的事，不重寫 v6 已鎖的契約。

---

## 1. PR-2a 落地狀態 vs v6 plan 假設（對照）

PR-2a 於 2026-04-24 squash-merged。以下為 PR-2b 需要鉤入的實際 landing state，**與 v6 plan 假設對照**，確認 §1.4-1.8 / §1.13 可直接施工無 drift。

### 1.1 `internal/agent/subagent.go`（16 行，PR-2a 新檔）

型別與 v6 §1.1 完全一致：

```go
type SubagentRef struct {
    ID              string `json:"id"`
    Type            string `json:"type"`
    StartedAt       int64  `json:"started_at"`
    SourcePID       int    `json:"source_pid"`
    SourceStartTime string `json:"source_start_time"`
    IsProxy         bool   `json:"is_proxy,omitempty"`
}
```

**落地註解要點**（PR-2a R1 review fix）：
- `Type` 固定取 `frame.AgentType`（canonical agent family：cc / codex / opencode），**不吃** `detail.agent_type`（opencode 的 per-subagent sub-variant 如 `"Explore"`）
- PR-2b proxy ref 組 `Type` 時同樣用 `req.AgentType`（sender agent family），**不是** parent.AgentType

### 1.2 `internal/module/agent/frame_ops.go`（378 行，PR-2a 擴到此規模）

**v6 §1.4 要求**：PR-2b 在 `applyFrameEvent` 的 **SessionStart 分支、建新 frame 之前** 插入 proxy 判斷。

**實際落地結構**：`applyFrameEvent` 目前有三個明確分支：
1. `SessionEnd` — line 35-56（`frame != nil` 刪除 vs `frame == nil` → `session_end_without_frame`）
2. `SubagentStart` / `SubagentStop` — line 57-107（必須 `frame != nil` 否則 `frame_missing`）
3. **其餘（SessionStart + 一般 hook 更新）** — line 109-176（`readProcessInfoFn` → `FindByPanePID(paneID, info.PPID)` single-layer parent lookup → `frames.Upsert`）

**PR-2b 插入點**（v6 §1.4 命中行為）：
- 在 line 109 `readProcessInfoFn(req.SenderPID)` **之前** 插 proxy fast-path
- 觸發條件：`req.EventName == "SessionStart"` && `frame == nil`
- 命中 → call `m.findProxyParent(req)` → `updateSubagents(parent.Subagents, "SubagentStart", proxyRef)` + `m.frames.Upsert(*parent)`，**早 return** trace meta `decision="updated_frame", reason="proxy_subagent_attached"`
- 不命中 → fall through 原 line 109+ legacy 建新 frame 路徑

**既有 single-layer parent lookup**（line 125-133）**保留不動** — 它是 non-proxy 的 `parent_frame_found` / `parent_frame_missing` 分支，與 proxy 偵測正交（proxy 命中時直接 early-return）。

**SessionEnd proxy cleanup 插入點**（v6 §1.5）：
- 改動現有 line 50-56 的 `frame == nil` 路徑
- 先 call `m.removeProxyRefForSender(req.TmuxPaneID, req.SenderPID, req.SenderStartTime, broadcastTs)`
- 命中（`removed=true`）→ 新 trace meta `decision="updated_frame", reason="proxy_subagent_detached"`
- 不命中 → fall through 原 `session_end_without_frame` 路徑

### 1.3 `internal/module/agent/sweep.go`（103 行，PR-2a 未動）

現況與 v6 §1.8 描述完全一致：
- `sweepOnce`（line 39-68）有兩條規則（`pid_dead` / `pid_reused`）
- `clearFrame`（line 70-103）為單一路徑，混合 Delete + side effects

**PR-2b 改動**（v6 §1.6, §1.8）：
- `sweepOnce` 加第三條：`nowFn().UnixNano() - frame.LastSeenAt > frameIdleThreshold.Nanoseconds()` → `DeleteIfUnchanged`（conditional，concurrent refresh 時 skip）
- `clearFrame` 拆出 `afterFrameCleared(frame, reason)`（broadcast + legacy event clean + orphan watcher StopWatch）
- 新增 `nowFn = time.Now`（test seam）
- **順便修既有 bug**：pid_dead / pid_reused 路徑目前（line 91）只 `delete(m.activeWatchers, sessionName)` 但沒 `m.prober.StopWatch(sessionName + ":")` → goroutine leak；PR-2b 順手補，新增 IS4 regression test

### 1.4 `internal/store/frames.go`（322 行）

v6 §1.7 `DeleteIfUnchanged` 為新 method，落地前不存在。落地後 method 座標建議：放在現有 `Delete(frameID string) error` 鄰近，共用同款 SQL pattern。

### 1.5 SPA consumer 檔案（實際 line 位置確認）

grep 當前 head（@ `6b461c1b`），v6 plan §1.14 表格列出的座標與實際一致：

| 檔案 | prop declaration | SubagentDots 呼叫 |
|---|---|---|
| `spa/src/components/TabIcon.tsx` (95 行) | line 29 `subagentCount: number` | line 59 / 70 / 92（dot/iconDot/badge）|
| `spa/src/components/SortableTab.tsx` (166 行) | line 43 prop destructure | TabIcon 調用 @ line 92 / 132 |
| `spa/src/features/workspace/components/InlineTab.tsx` (158 行) | line 42 prop destructure | line 111 轉介給 renderInlineTabIcon |
| `spa/src/features/workspace/lib/renderInlineTabIcon.tsx` (96 行) | line 11 prop decl / line 35 destructure | line 58 / 72 / 93（三模式）|
| `spa/src/hooks/useTabDisplay.ts` | line 27 return `subagentCount: number` / line 52 derive | line 83 return field |

### 1.6 Plan v6 §1.14 漏列項（delta 補齊）

v6 §1.14 SPA 允許改動表把 **`useTabDisplay.ts` 回傳擴充 `subagentRefs`** 歸為 PR-2b 改動（§1.13 段落確實提到），但 §1.14 的大表只列了下游 consumer 沒列 `useTabDisplay.ts` 自己。

**PR-2b 實際要改**：
- `spa/src/hooks/useTabDisplay.ts` — 加 `subagentRefs: SubagentRef[]` 回傳欄位（derive from `s.subagents[ck] ?? []`）。`subagentCount` 保留（後續 cleanup 時再決定是否移除；本 PR-2b 不碰，避免 scope 蔓延）
- 若 useTabDisplay 的 TypeScript 型別 export 被其他檔案 import，連帶加 `subagentRefs` 欄位

### 1.7 PR-2a 新增的 test seams（對 PR-2b 測試有幫助，可選用）

PR-2a R2 + R4 code review 修復中引入：

| Seam | 位置 | PR-2b 用途 |
|---|---|---|
| `store.AgentEventStore.ExecRawForTest(q, args...)` | `internal/store/agent_event.go` | 跨 package 測試 seed（目前 PR-2b 測試不需，仍可用）|
| `framesInitFn` / `tracesInitFn` | `internal/module/agent/module.go` | `Module.New` 測試注入（PR-2b 的 proxy/sweep 測試可照舊用 subagent fixture 直接 seed，不需）|

---

## 2. PR-2b 施工項目（reference v6 §1.4-§1.8, §1.13）

以下以 commit 為單位對照 v6 §3，只列 delta / 補充。契約細節**以 v6 為準**。

### Commit 6 — `feat(store): add DeleteIfUnchanged for optimistic sweep`
- 契約：v6 §1.7
- 測試：v6 §2.3（F4/F5/F6）
- Delta：無

### Commit 7 — `feat(module/agent): detect proxy subagents via PPID ancestor walk`
- 契約：v6 §1.4（含 v4 same-type hard-stop、v5 start_time error abort walk 三分支）
- 測試：v6 §2.5（PR1-PR15 共 15 測試）
- Delta：
  - **插入點**：`applyFrameEvent` line 109 `readProcessInfoFn` 之前（§1.2 描述）
  - **proxy ID 字串**：v6 §1.4 寫 `fmt.Sprintf("proxy:%s:%d:%s", req.AgentType, req.SenderPID, req.SenderStartTime)` — 沿用
  - **Type 欄位 canonical rule**：PR-2a R1 已確立 `Type = frame.AgentType`；PR-2b proxy ref 的 `Type = req.AgentType`（新進 hook 的 sender agent family，**非** parent 的 type）— v6 §1.4 命中行為 code block 已寫對
  - **test seam 取名**：`readProcessInfoFn` / `isPidAliveFn` / `processStartTimeFn` 已是 module-level 變數可直接替換；不需另開 seam
  - **Trace meta 欄位**：`Before`/`After` 兩者都 `summarizeFrame(parent)` 前後值，**不要** 用 proxy ref 自己的 map

### Commit 8 — `feat(module/agent): remove proxy ref on SessionEnd`
- 契約：v6 §1.5
- 測試：v6 §2.6（SE1/SE2/SE3）
- Delta：
  - **`removeProxyRefForSender` helper 位置**：放 `frame_ops.go` 內部 method，private
  - **scan 範圍**：`m.frames.ListByPane(req.TmuxPaneID)` + inner loop `frame.Subagents` — v6 §1.5 已明確
  - **matching key**：`ref.SourcePID == req.SenderPID && ref.SourceStartTime == req.SenderStartTime`，**不** 吃 `ref.ID` 字串（穩健性）

### Commit 9 — `feat(module/agent): idle sweep with conditional DELETE`
- 契約：v6 §1.6 + §1.8
- 測試：v6 §2.7（IS1-IS5）+ §2.8（HB2）
- Delta：
  - **`afterFrameCleared` 職責**：原 `clearFrame` line 77-102 的 side effects 整塊搬過去（保順序）— session_name / code resolve → event Delete → projection → syncProjectionState → activeWatchers cleanup → **新增 StopWatch** → broadcast
  - **StopWatch bug fix 涵蓋路徑**：不只新 `idle_timeout`，pid_dead / pid_reused 都走同一條 `afterFrameCleared` 所以三路徑都修到
  - **broadcast reason 字串**：`"sweep:" + reason`（`"sweep:pid_dead"` / `"sweep:pid_reused"` / `"sweep:idle_timeout"`）— 保既有格式，reviewer 不會看到 hook event_name 被「換皮」
  - **`nowFn` 導入但不 re-export**：module 內 private 變數，test 用 `nowFn = func() time.Time { return fakeNow }` 覆寫

### Commit 10 — `feat(spa): SubagentDots takes SubagentRef[] with type color + proxy outline`
- 契約：v6 §1.13
- 測試：v6 §2.9 PR-2b 段
- Delta：
  - **atomic commit**：TypeScript 型別的 prop rename 必須一次改完所有 consumer，否則 compile 破。Commit 10 改動：
    1. `SubagentDots.tsx` API `{count}` → `{refs}`
    2. `TabIcon.tsx` prop `subagentCount: number` → `subagentRefs: SubagentRef[]`（3 處 render）
    3. `SortableTab.tsx` prop rename + 兩處 TabIcon 呼叫（line 92, 132）
    4. `InlineTab.tsx` prop rename（line 42 destructure, line 111 轉介）
    5. `renderInlineTabIcon.tsx` prop rename + 三處 SubagentDots 呼叫
    6. `useTabDisplay.ts` 擴充 `subagentRefs` 回傳（derive from `s.subagents[ck] ?? []`）
  - **`subagentCount` 保留否**：建議**移除**（PR-2a 後 useTabDisplay 仍 export `subagentCount`，但 consumer 都 rename 吃 `subagentRefs`，`subagentCount` orphan；`refs.length` 一行取代）— PR-2b 連帶 clean up 避免死 field
  - **TYPE_COLOR 位置**：v6 §5 「不做」已寫不抽到 `agent-icons.tsx`，**在 SubagentDots.tsx 內 inline const table**：
    ```ts
    const TYPE_COLOR: Record<string, string> = {
      cc: '#60a5fa',
      codex: '#facc15',
      opencode: '#f97316',
    }
    const colorFor = (type: string) => TYPE_COLOR[type] ?? TYPE_COLOR.cc
    ```
  - **Proxy outline 實作**：
    ```ts
    const dotStyle = (ref: SubagentRef): CSSProperties => {
      const color = colorFor(ref.type)
      return ref.is_proxy
        ? { backgroundColor: 'transparent', border: `1px solid ${color}` }
        : { backgroundColor: color }
    }
    ```

### Commit 11（可選）— `docs: phase 2 plan retrospective notes`
若 §3 順序與實際執行有偏差（例 IS4 拆出 commit / proxy ID 公式微調），補 retrospective note。

---

## 3. PR-2a code review 教訓對 PR-2b 的 impact

PR-2a 4 輪 code review 修掉的問題中，**三項對 PR-2b 有延伸 impact**：

| PR-2a finding | Fix commit | PR-2b 延伸 |
|---|---|---|
| R1 P1：legacy DB 全域 hook 500（handler 沒早退 invalid result） | `67745bdc` | Proxy 偵測的 early-return 要走 handler 能消化的 trace meta shape；不要塞 malformed decision 字串 |
| R2 P1：`LIMIT 1` probe miss mixed table（migration 不能只看一列判型別） | `2efec87b` | 新測試 IS5 用 fake store wrap `FramesStore` 模擬 concurrent refresh — fake store 必須掃全表行為一致（不是 `LIMIT 1` probe） |
| R3→R4 P1：`Module.New` frames err 吞 vs traces err best-effort 的區別 | `72e2f840`+`74796145` | `sweep.go` 新 `DeleteIfUnchanged` 回 `(false, nil)` 時 **不是 error** 要 `continue`；不要 wrap 成 `return err`（否則 sweep 遇 concurrent 就當掉） |

---

## 4. PR-2b 與 PR-2a 的 wire / schema 面向（不用動）

- **Schema**：PR-2a 已立 `subagents_json` JSON shape（`SubagentRef[]`）；PR-2b 寫入帶 `IsProxy=true` 的 ref 時 **沿用同 shape**，migrateFramesDB 的三態偵測仍能 pass new schema
- **Wire**：PR-2a 已升級 `NormalizedEvent.Subagents` 為 `[]SubagentRef`；PR-2b 的 proxy ref 與 native ref **wire 型別一致**，SPA 不需再改 parse 邏輯（只改 render）
- **Bump 版本**：PR-2b merge 後 bump `alpha.219`；CHANGELOG 標「proxy 偵測 + idle sweep 啟用」但**不需**標 schema breaking（已在 alpha.218 入過）

---

## 5. 風險補充（vs v6 §8）

v6 §8 的風險清單仍全部適用。PR-2a 落地後新增兩條觀察：

| 新風險 | 機率 | 影響 | 緩解 |
|---|---|---|---|
| `frame_ops.go` 施工後 >500 行（目前 378 + ~145 估 = ~520） | 中 | Review R2 體質檢查會警告 | 可接受；若 reviewer 強烈要求，拆 `proxy.go` + `frame_ops.go`（private helper 搬過去不換 receiver）但不在本 PR 預先拆 |
| sweepOnce 新加 `idle_timeout` 第三條規則的位置 | 低 | 若放在第一條規則（pid check）之前會錯過 dead/reused 的提前清理 | 保第三條位置（v6 §1.6 已寫），IS1-IS5 測試覆蓋 |

---

## 6. 驗收 checklist（PR-2b）

照 v6 §7 PR-2b 段執行，總結如下：

- [ ] 5 個 commits（6-10）符合規範，每個 commit 邊界 `go build ./...` + tests 綠
- [ ] `go test ./...` 綠；必跑 **27 個新測試**：F4-F6 / PR1-PR15 / SE1-SE3 / IS1-IS5 / HB2
- [ ] `go vet ./...` 無 warning
- [ ] `cd spa && pnpm run lint && pnpm run build && npx vitest run` 綠
- [ ] PR diff 檔案：v6 §4 PR-2b 表格 + 本 delta §1.6（useTabDisplay.ts）列出的檔案；**其餘不得出現**
- [ ] PR description 含 v6 §5「不做項目」聲明
- [ ] 手動場景 4 項：
  - cc 啟動 → cc pane 內跑 `/codex:*` → SPA 只顯示 1 個 frame（cc），cc.Subagents 含 codex ref，SubagentDots 黃色 outline
  - 手造 idle frame（臨時改 threshold 到 1s 或 DB 改 LastSeenAt）→ sweep 後 frame 消失、broadcast reason `sweep:idle_timeout`
  - codex SessionEnd（proxy 來源）→ cc.Subagents 空、dot 消失
  - cc native SubagentStart（Task 工具觸發）→ SPA 顯示藍色實心 dot

---

## 7. Review cadence 預期

### PR-2b plan review（本檔）

**目標**：1-2 輪收斂（因為契約已在 PR-2a v6 定過）
- **Round 1**：標準 review — 重點檢 delta plan 是否有漏 v6 plan 已鎖的 contract、是否誤改 contract
- **Round 2**（若需要）：3-parallel — 防守方可能挑 delta 章節編排、攻擊方挑 proxy ID collision / stale watcher edge case

### PR-2b code review

**目標**：2-4 輪收斂（proxy 語意 + sweep race 是重點）
- **R1 標準**：proxy 偵測 PPID walk 的邊界條件（info 讀 error / self-cycle / depth+1 超界）、`DeleteIfUnchanged` race window、`afterFrameCleared` 抽出後 clearFrame 語意等價
- **R2 3-parallel**：
  - **攻擊**：proxy ID 與 native SubagentStart 撞（native agent_id 若開頭為 `proxy:` 會誤判？）、 同 pane 多 sender process、sweep 執行中 attach 的 TOCTOU
  - **防守**：SubagentDots type color table 的可維護性、為何不加 per-ref LastSeenAt
  - **體質**：`frame_ops.go` >500 行、SubagentDots tests 是否過度（11+ case）
- **R3-R4 sanity**：收斂剩餘 P1

---

## 8. Open Questions

1. **`subagentCount` 是否完全移除**：本 delta §2 Commit 10 建議 `useTabDisplay.ts` 不再 return `subagentCount`；如果 reviewer 覺得 breaking 太大或有 external consumer 讀這欄位，改為 deprecated 保留（兩路都可）
2. **proxy ref 的 agent_id collision**：v6 §9 PR-2b R2 攻擊預期有這問題 — 當 native SubagentStart 的 `agent_id` 字串碰巧開頭是 `proxy:`（非常罕見但非零機率）時，SessionEnd 的 `removeProxyRefForSender` 會不會誤刪？**不會**，因為 matching key 是 `SourcePID+SourceStartTime`（native ref 這兩欄為 0/空），不是 ID 字串。此答案寫進 PR description 防 R2 重問
3. **dev update skew 期間 SPA 若吃到舊 daemon（alpha.218）送的 native-only subagents，新 SubagentDots 渲染**：fallback color 行為正確（`TYPE_COLOR[ref.type] ?? cc`），proxy 欄位 undefined 走 solid dot；不需額外 guard
