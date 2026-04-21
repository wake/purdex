# 燈號追蹤系統 — 目標 vs 現況對齊分析

- 日期：2026-04-21（alpha.196）
- 作者：Claude + Codex（parallel 調查）
- 調查依據：6 個 codex job（3 個前輪現況盤點 + 3 個本輪對齊分析）
- 目標：釐清「燈號傳遞過程可監控、hook 為主 probe 為輔、主 agent + 多 subagent + type、三 agent 對稱」的目標與現況差距

---

## 使用者目標（5 點）

1. 燈號傳遞過程可被監控（先 daemon 側）
2. 架構：主要 agent type + 多種/多個 subagent + subagent type
3. agent 原生 hook 事件為主要依歸，probe 為輔助修正判斷
4. 傳遞 by session + event（hook / probe 兩種）紀錄，每次 event 用流程圖呈現每一個判斷端口的 input-reason-output
5. 支援三種 agent：cc / codex / opencode

---

## 核心結論

**基礎骨架都有，但抽象層與主從規則是隱含的**。不是從零開始 — 要把現有的 hook path collector 擴成通用 event reasoning pipeline，並把 subagent / source / epoch 從隱含升級成 first-class schema。

總體評分 🟡 2/5（骨架在、抽象層需升級）

---

## 目標 vs 現況對齊矩陣

| 目標 | 現況 | 落差 | 關鍵證據 |
|---|---|---|---|
| 1. 燈號傳遞過程可被監控 | Trace schema 有（chain + step），但**只涵蓋 hook POST path**；probe:activity / sweep:\* / handoff 都沒落 trace | 🔴 大 | `hookTraceCollector` 綁死 `hook_post` root (`internal/module/agent/trace.go:74,99`)；probe/sweep 直接 broadcast 不寫 trace (`module.go:477-488`, `sweep.go:99-101`) |
| 2. hook 主、probe 輔 | 實務上 hook 建 frame、probe 補活動/救援；**但主從關係是隱含的** | 🟡 中 | 只有 Error Guard（`handler.go:116-138` + `module.go:467-472`）；沒有 hook_ts / epoch / source 明文規則；activity watcher 可能覆寫非 error 的 hook 狀態 |
| 3. 主 agent + 多 subagent + type | subagent 全程是 `[]string`，**只存 id 不存 type**；OpenCode 已在 hook 帶 `agent_type` 但被 projection 丟掉 | 🔴 大 | `NormalizedEvent.Subagents []string` (`status.go:27`)；`Frame.Subagents []string` (`frames.go:12-25`)；`applyFrameEvent` 只讀 `detail["agent_id"]` (`frame_ops.go:57-93`) |
| 4. by session + event 流程圖 input-reason-output | chain + step 有 `decision/reason/payload/before/after` 欄位；UI 是 **chain tree + raw JSON inspector**，不是流程圖；projection 是 **live 查詢不是歷史 snapshot** | 🟡 中 | `StepInspector.tsx:41-48` 顯示 JSON；`TmuxAgentMonitorSection.tsx:78,146` 右側 projection 另查 live |
| 5. 三 agent 對稱 | cc 最完整；codex 只有最小集；opencode 缺 readiness/operator/typed subagent；多處硬編 'cc' | 🔴 大 | 對稱矩陣見下 |

### 三 agent 對稱矩陣

| 面向 | cc | codex | opencode |
|---|---|---|---|
| Provider Identify | 完整 | 完整 | 完整 |
| Provider IsAlive（過渡殼） | 完整（轉呼 probe）| stub | stub |
| Hook status 支援 | running / waiting / idle / error / clear | running / idle（**缺 waiting/error/clear**） | running / waiting / idle / error / clear |
| Readiness Checker | 可判三態（`Allow+Deny→waiting` / `❯→idle` / 其他→running）| **stub 永遠 running** | **無 checker（未註冊）** |
| Operator (Interrupt/Exit) | 完整 | 無 | 無 |
| Hook event 豐富度 | 9 種（含 Notification）| 3 種（SessionStart/UserPromptSubmit/Stop）| 8 種（plugin 已 map） |
| SPA icon / metadata | 完整 | 完整 | 部分 |

---

## A. Main 分支 Probe Chain 主線完成度（前輪調查）

**分數：3/5**

| 層 | 狀態 | 關鍵位置 |
|---|---|---|
| **Liveness** | ✅ 完整 | `internal/agent/probe/liveness.go:23` — PanePID + descendant tree + identify，250ms TTL cache |
| **Activity** | ✅ 完整 | `internal/agent/probe/activity.go:17` — hash diff 500ms poll，3 次穩定觸發 idle/shell_prompt |
| **Readiness** | ⚠️ 不齊 | interface OK；CC 判三態；**Codex 是 stub** (`internal/agent/codex/readiness.go:18`)；**OpenCode 無 checker** |

**Chain 是被動 delegate**：`probe.CheckReadiness` 沒先做 liveness/activity gate，由 `module/stream/orchestrator.go:70-87` 與 `module/agent/handler.go:210-221` 各自決定順序。

**掛載點**：`internal/module/agent/module.go:114-126`（CC + Codex 註冊 identifier + readiness；**OpenCode 只註冊 identifier 沒註冊 readiness**）。

**黃燈救援路徑存在**：`module.go:456-461` 的 `onActivityDetected` 在 `shell_prompt + top frame PID 已死` 時觸發 `sweepOnce()`。

**舊 Detector 命名已清**（`detectCCSubState` / `Detector` 在 internal/ 下無殘留），但 `AgentProvider.IsAlive` 介面仍保留並與新 probe 並存（`agent/cc/provider.go:61-65` 已轉呼 `prober.IsAliveFor`）。

---

## B. watchAlive PR #486 分支狀態（前輪調查）

**結論：NOT ship-ready，建議 close PR + 刪分支**

🚨 **重大發現**：分支 HEAD 實際是 `87a9e210`（非記憶中 `73c236e7`）。`git diff --stat main..feat/agent-watch-alive` = **231 files, 1472 insertions, 39159 deletions**。分支不是 watchAlive patch，而是**落後 main 巨量的歷史 snapshot**。

5 race 2026-04-21 重檢結果：

| # | 問題 | 狀態 | 佐證 |
|---|---|---|---|
| B1 | Stale `ListAll()` snapshot → delete 新 hook | ❌ 未修 | `module/agent/module.go:340-385` 無 conditional delete / broadcast_ts 重讀 |
| B2 | Session rename race | ⚠️ 部分修 | rename 有 `m.mu` 鎖，但 `checkAliveAll` 沒共鎖 |
| B3 | tmux 失聯 `ListSessions()=nil` → orphan 全刪 | ❌ 未修 | 無 health gate，空結果仍走 orphan delete |
| B4 | Stop 不 join sweep | ⚠️ 部分修 | ticker ctx 繼承了，但 sweep 內用 `context.WithTimeout(Background(), 5s)`；`Stop()` 只 cancel 不 join |
| B5 | 測試 flaky | ❌ 未修 | `handler_test.go:457-489` 仍無同步點 |

不能 rebase 也不能 cherry-pick（倒退 39K 行會把 main 的 probe chain / identity / trace monitor / statusline 全數砍掉）。

---

## C. SPA 燈號顯示側（前輪調查）

**分數：3.5/5**

**元件分三層**：

| 層 | 元件 | 特性 |
|---|---|---|
| Tab（頂 / 側邊）| `TabStatusIndicator` + `TabIcon` + `renderInlineTabIcon` | hex 色 palette，running 呼吸動畫，error 改用 `WarningDiamond`，overlay/replace 兩模式 |
| Session row | `SessionStatusBadge`（SessionPanel）+ `SessionsSection` chip | Tailwind class，**SessionsSection 只有綠/紅 + 中性，缺 waiting 黃、idle 灰** |
| Workspace 聚合 | `ActivityBarNarrow` 內聯 5px dot | idle 被折成 undefined 不顯示 |

**資料鏈路**：`/ws/host-events` → `connectHostEvents` → `useMultiHostEventWs` 按 7 種 type 分派 → `hook` 走 `handleNormalizedEvent`，`agent.status` / `agent.status.cleared` 走 `agent-ws-dispatch.ts`（只寫 ccStatus / oscTitles，**不動 statuses**）。

**狀態列舉不一致**：
- Go `agent.Status` = 5 個（running/waiting/idle/error/clear）
- SPA `AgentStatus` = 4 個（**無 clear**）— 型別漏
- `clear` 在 SPA 只能靠 `if event.status === 'clear'` 特判（`useAgentStore.ts:110`）

**已知缺口**：
1. `clear` 有雙重語義：hook clear = 全 session 清；`agent.status.cleared` 只清 ccStatus
2. **顏色 SOT 分散在 5+ 處**（TabStatusIndicator / SessionStatusBadge / ActivityBarNarrow / SubagentDots / unread pip × 4）
3. `detail` 欄位 OpenCode 帶的 `request_type / permission / patterns / questions` 前端沒消費
4. statusline raw 裡的 `model` 無 UI 消費點

---

## D. Trace 基礎設施現況（本輪調查）

**差距評級：紅**

### D1. Trace Schema（只覆蓋 hook POST）

- `TraceChain`：`chain_id / started_at / completed_at / terminal_status / terminal_reason / tmux_session / pane_id / root_agent_type / root_event_name / root_reason / latest_step_kind / latest_decision / latest_step_reason / step_count`
- `TraceStep`：`step_id / chain_id / parent_step_id / seq / kind / tmux_session / pane_id / agent_type / frame_id / parent_frame_id / event_name / decision / reason / payload_json / before_json / after_json / created_at`
- chain 在 `handleEvent` 進來時建立，`root_reason` 固定 `"hook_post"`
- step 序列：`trigger → verify → frame → projection → emit`（線性鏈）
- **`status` 不是一級欄位**，只在 chain lifecycle 的 `terminal_status` 和 step payload_json 裡
- **`detail` 也沒有獨立欄位**，跟著 `NormalizedEvent.Detail` 序列化進 `payload_json/after_json`

### D2. 寫入點（只有 hook POST）

| 路徑 | 是否落 trace | 證據 |
|---|---|---|
| `handleEvent`（hook POST）| ✅ 是 | `handler.go:85` → `beginHookTrace` → `Finish` → `hookTraceSink.Enqueue` → `TraceStore.SaveChain` |
| `probe:activity`（watcher callback）| ❌ 否 | `module.go:477-488` 只 broadcast，不寫 trace |
| `sweep:pid_dead` / `sweep:pid_reused` | ❌ 否 | `sweep.go:99-101` 只 broadcast |
| `IsAliveFor` / `CheckReadiness`（operator + handoff）| ❌ 否 | `operator.go:28,76`, `orchestrator.go:72,78,200,224` |
| Projection top-frame 覆寫（`setProjectionTopStatus`）| ❌ 否（在 probe:activity 路徑）| `module.go:476` |
| hook path 的 projection 步驟 | ✅ 是 | `trace.Projection(...)` (`trace.go:195`) |

### D3. 讀取 API

路由：
- `GET /api/agent/monitor/chains`（支援 session / pane / agent_type / event_name / limit / cursor / before；**無 time-range**）
- `GET /api/agent/monitor/chains/{id}`
- `GET /api/agent/monitor/projection`

- cursor 用 `started_at + chain_id` 做 pagination
- 沒有 monitor WS/SSE
- SPA 端目前只手動 refresh，沒自動 poll
- API 有 `next_cursor` 但 UI 沒用到

### D4. 儲存策略

- SQLite（與 `agent_event` 共用 DB）+ WAL
- 上限：`10000 chains / 100000 steps`，SaveChain 後 prune 最舊
- **無 TTL / time-based compaction**
- 寫入是 async queue（buffer 256）；**queue 滿會 drop chain**
- 估算：hook event 每筆 2-5 個 step；30 hook/hour ≈ 30 chain + 150 step；probe/sweep 因無 trace = 0

### D5. UI 呈現能力

- `TmuxAgentMonitorSection` = chain list + single chain detail (`step_tree`)；**視角是 chain 不是單筆 event**
- `StepTree` 顯示 `kind / decision / reason / agent_type / frame_id / parent_frame_id`
- `StepInspector` 顯示 `payload_json / before_json / after_json`（**raw JSON 區塊 + reason 字串**，不是結構化 port/node）
- 未顯示的已儲存欄位：`terminal_status / terminal_reason / root_reason / latest_* / step_count / event_name / tmux_session / pane_id / parent_step_id / seq / created_at`
- **右側 projection 是 live 查詢**，選舊 chain 會看到今天的 top frame（歷史混live）

### D6. 差距分級

- **只需 UI 調整**：hook POST 已有 chain + parented steps + `decision/reason/payload/before/after`，足以把 `trigger → verify → frame → projection → emit` 改畫成流程圖
- **需 daemon 擴 schema + 寫入點**：probe:activity / sweep:\* / IsAliveFor / CheckReadiness / handoff 未 trace；status/detail/model/event category 不是一級欄位
- **需新抽象層**：`hookTraceCollector` 綁死 `hook_post`；需要不依附 hook POST 的通用 trace producer；projection 要變歷史 snapshot

---

## E. Hook / Probe 主從邊界現況（本輪調查）

**主從一致性評級：黃**

### E1. 狀態 SOT 不是 `m.currentStatus`

真正決定 session 可見 status 的主 sink 順序：

1. **持久 sink**：`store.Frame.Status` / DB `agent_frames.status`（`frames.go:12-25`）
2. **Session 投影 sink**：`projection.TopFrame.Status`（`projection.go:41-59`, `frame_ops.go:281-347`）
3. **記憶體鏡像**：`m.currentStatus[session]`（`module.go:40-43`）— 由 `syncProjectionState()` 以 TopFrame.Status 覆寫（`frame_ops.go:227-235`）
4. **對外 sink**：`NormalizedEvent.Status` 經 `buildProjectionNormalized()` → `emitHookToSession` 廣播（`frame_ops.go:237-257`, `handler.go:272-282`）
5. **SPA sink**：`useAgentStore.statuses[key]`（`useAgentStore.ts:138-151`）

### E2. Hook 主軌

`handleEvent` (`handler.go:68-245`) 流程：

```
HTTP POST /api/agent/event
  → verifyEvent (verify.go:40-78)
  → registry.Get(agent_type).DeriveStatus (handler.go:104-113)
      → cc/status.go:13-90 / codex/status.go:9-18 / opencode/status.go:9-30
  → Error Guard（error 狀態只允許白名單 hook 清掉）(handler.go:116-138)
  → applyFrameEvent（upsert/delete/subagents）(frame_ops.go:19-164)
  → projectionForSession (handler.go:149-160)
  → buildProjectionNormalized (handler.go:230-231)
  → syncProjectionState (handler.go:232-234)
  → emitHookToSession / Broadcast("hook") (handler.go:272-282)
  → WS → handleNormalizedEvent → statuses[key]
```

**關鍵**：provider 的 `DeriveStatus` 是 SOT，但 session 對外最終 status 仍以 `projection.TopFrame.Status` 為準。

### E3. Probe 寫入點（輔軌）

| 寫入點 | 觸發條件 | 寫入內容 | Guard |
|---|---|---|---|
| **activity watcher**（`module.go:440-488`）| hook 到 waiting/running/idle 後 arm watcher；pane 內容變動或穩定 3 次 | `m.currentStatus[session]=status` + `setProjectionTopStatus` + broadcast `probe:activity` | 只有 active watcher check + error guard；**無 hook_ts / epoch guard** |
| **sweep:pid_dead / pid_reused**（`sweep.go:47-101`）| 每 2s 輪詢 / shell_prompt + top pid dead；`frame.Verified && (!pidAlive \|\| startTime mismatch)` | 刪 frame + 重算 projection + `syncProjectionState` + broadcast `sweep:<reason>` | dead/reused 條件；無 hook 優先權 guard |
| **handoff**（`orchestrator.go`）| — | **只讀不寫** | — |

### E4. 主從邊界檢查

- **Error Guard 存在**：hook 路徑阻擋非白名單清掉 error（`handler.go:116-138`）；probe:activity 阻擋覆寫 `StatusError`（`module.go:467-472`）
- **沒有明文定義「hook 永遠贏」**
- **下游無 source-aware channel**：hook / probe / sweep 都走 `event.type === "hook"`；區分只靠 `raw_event_name` 字串（hook 事件名 / `probe:activity` / `sweep:<reason>` / `replay`）
- **NormalizedEvent 無 `source` / `priority` 一級欄位**
- **SPA store 單靠到達順序覆寫**：每個 `hook` event 都直接覆寫 `statuses[key]`，不看 `broadcast_ts` 或 source

### E5. 可疑點（race 風險）

- **activity watcher**：只用 `activeWatchers[session]` 判斷 active（`module.go:442-449`），**沒有 watcher generation**；但 prober 內其實有 per-watch `id` token（`activity.go:17-27,50-57`）。**舊 watcher callback 理論上可在新 hook 重啟 watcher 後誤寫新狀態**
- **sweep:pid_dead/reused**：合理救援（dead pid 代表 hook process 失效）
- **projection top frame 覆寫**：明確設計（TopFrame = 最新 StartedAt），不是 probe 越權；但代表「hook raw derive result ≠ session 最終 status」
- **SPA unread 規則**：只排除 `Notification`，`probe:activity` / `sweep:*` 的 idle/error/waiting 也可能被當成 actionable（`useAgentStore.ts:143-150`）

### E6. 若要符合「hook 永遠贏、probe 只在 hook 沉默時填空白或異常救援」

最少要動三塊：

1. daemon 加 `source / hook_ts / watch_generation / frame_id` 類型的權威狀態 metadata
2. `probe:activity` 只在「同一 watcher 世代且期間無新 hook」時可寫
3. SPA/store 加明確優先級，不能單靠到達順序覆寫

---

## F. 三 agent 對稱性 + Subagent 模型（本輪調查）

**對稱度評級：紅**

### F1. Status 列舉覆蓋

**Hook status 層**：

| agent | running | waiting | idle | error | clear |
|---|---|---|---|---|---|
| cc | ✅ UserPromptSubmit | ✅ Notification(permission_prompt\|elicitation_dialog) + PermissionRequest | ✅ SessionStart + Notification(idle_prompt\|auth_success) + Stop | ✅ StopFailure | ✅ SessionEnd |
| codex | ✅ UserPromptSubmit | ❌ | ✅ SessionStart + Stop | ❌ | ❌ |
| opencode | ✅ UserPromptSubmit | ✅ PermissionRequest | ✅ SessionStart + Stop | ✅ StopFailure | ✅ SessionEnd |

**Readiness 層**：

| agent | 可判別狀態 |
|---|---|
| cc | waiting / idle / running（依 `Allow`/`Deny` / `❯`）|
| codex | 永遠 running（stub）|
| opencode | 無 checker（未註冊）|

**Activity watcher 層**：任一被 watch 的 session 只會得到 running/idle，不補 waiting/error/clear。

### F2. Subagent 模型現況

**全程 `[]string`，只存 id 不存 type**：

| 層 | 型別 | 證據 |
|---|---|---|
| `NormalizedEvent.Subagents` | `[]string` | `status.go:22-30` |
| `store.Frame.Subagents` | `[]string` | `frames.go:12-25` |
| `SessionProjection.Subagents` | `[]string` | `projection.go:9-14` |
| `applyFrameEvent` | 只讀 `detail["agent_id"]` | `frame_ops.go:57-93,202-224` |
| SPA `useAgentStore.subagents` | `Record<string, string[]>` | `useAgentStore.ts:35-44` |
| SPA 消費 | 只取 `length` | `useTabDisplay.ts:50-53` |
| `SubagentDots` | 最多 3 顆固定藍點 | `SubagentDots.tsx:11-22` |

**三 agent 的 subagent 來源**：
- **cc**：hook 有 `SubagentStart/Stop`，但 derive 只保留 `agent_id`
- **codex**：**無 subagent hook event**
- **opencode**：plugin 把 `tool.execute.before/after` 映為 `SubagentStart/Stop`，payload **已帶 `agent_type / description / prompt / title / output`** — 但被 projection 丟掉（只留 detail）

### F3. 共用 hook event 抽象

可共用：`SessionStart / UserPromptSubmit / PermissionRequest / Stop / StopFailure / SessionEnd / SubagentStart / SubagentStop`

不對稱：
- **`Notification` 只有 cc 有**（`cc/hooks.go:13-16`, `cc/status.go:30-48`）
- **codex 缺 PermissionRequest / StopFailure / SessionEnd / Subagent\* / Notification**

### F4. 硬編碼 'cc' 的位置（新增第 4 種 agent 要改的地方）

- daemon provider 註冊：`internal/module/agent/module.go:117-145`（手動列舉）
- `/api/agents/detect` 硬編清單：`internal/module/agent/handler.go:673-677`
- Statusline installer：`internal/module/agent/handler.go:35-51`（只接受 cc）
- `/api/agent/status` 只接受 `agent_type == "cc"`：`handler.go:566-617`
- Stream module 只依賴 `cc.operator`：`internal/module/stream/module.go:23-27`
- Orchestrator 直接寫死 `"cc"`：`internal/module/stream/orchestrator.go:70-103`
- SPA metadata：`spa/src/lib/agent-metadata.ts:1-5`
- SPA icon list：`spa/src/lib/agent-icons.tsx:23-44`（只 cc/codex）
- SPA hook modules：`spa/src/lib/hook-modules.ts:52-127`（顯式列舉）
- SPA `agent.status` WS dispatch 只處理 cc：`spa/src/lib/agent-ws-dispatch.ts:24-55`

---

## G. 實作落差清單（要動的地方）

### 🅐 Trace 擴寫入 + schema 升級（對應目標 1+4）

- **抽 collector 抽象**：`hookTraceCollector` 綁死 `hook_post`，要通用化能吃 probe/sweep/handoff 的 decision
- **擴寫入點**：`probe:activity` / `sweep:*` / `IsAliveFor` / `CheckReadiness` / handoff 決策
- **升級 schema**：`status` / `source` / `detail` / `model` 從 payload_json 升為一級欄位
- **Projection freeze**：每 chain 完成時凍結一份，讓歷史查詢不用 live projection
- **Monitor API** 加 time-range + WS streaming（目前只能 REST + 手動 refresh）

### 🅑 主從邊界明文化（對應目標 2）

- `NormalizedEvent` 加 `source: "hook" | "probe" | "sweep"` 欄位
- 加 `hook_epoch` / `watch_generation` metadata
- **activity watcher 加 epoch guard**：只在同一 generation + 期間無新 hook 才可寫
- SPA store 依 source + timestamp + epoch 判斷優先級

### 🅒 Subagent 模型升級（對應目標 3）

- `[]string` → `[]SubagentRef{id, type, ...}`（NormalizedEvent / Frame / Projection 三層同步）
- `DeriveResult` 加 `SubagentDelta` 明確結構（不要靠 Detail 偷渡）
- SPA store + `SubagentDots` 升級支援 type

### 🅓 三 agent 對稱化（對應目標 5）

- Codex hook provider 補 `PermissionRequest` / `StopFailure` / `SessionEnd` / `Subagent*`
- Codex readiness 補實作（不要永遠回 running）
- OpenCode readiness 補實作 + 註冊
- OpenCode typed subagent：plugin 已帶 `agent_type`，從 detail 提升到 projection
- 硬編 'cc' 拆除：statusline installer / agent.status handler / stream orchestrator / SPA icon list / detect list

---

## H. 綜合評分

| 維度 | 分數 |
|---|---|
| 監控能力（目標 1+4）| 🟡 2/5（hook 可監控，probe/sweep 未覆蓋）|
| 主從邊界（目標 2）| 🟡 3/5（實務優先，規則隱含）|
| Subagent 模型（目標 3）| 🔴 1/5（只有 count，沒 type）|
| 三 agent 對稱（目標 5）| 🔴 2/5（cc > opencode > codex）|
| **總體** | **🟡 2/5** — 骨架有，抽象層需升級 |

---

## I. 下一步建議（依優先級）

### 建議順序

1. **先**：寫一份 design spec（定義 `source` / `epoch` / `SubagentRef` schema + probe 落 trace 抽象 + 主從規則明文化）。這是跨 5+ 層的改動，沒 spec 容易發散
2. **同時**：把「輕清理」處理掉 → 降低 noise
   - Codex Readiness stub 補實作
   - Close PR #486 + 刪 `feat/agent-watch-alive` 分支
3. **然後**：按 Phase 實作：
   - Phase 1：Trace schema 升級 + 擴寫入點（daemon-only）
   - Phase 2：主從邊界明文化（`source` / `epoch` / guard）
   - Phase 3：Subagent 模型升級（typed ref）
   - Phase 4：三 agent 對稱化（codex 補完、opencode readiness、硬編碼拆解）
   - Phase 5：SPA UI 重構（流程圖 + 歷史 projection）

### 可選待討論

- `Notification` 只 cc 有 — 要不要升為共用抽象？還是保留 cc 獨有？
- Codex / OpenCode 要不要做 Operator（Interrupt/Exit）？還是 cc-only？
- Subagent 要不要畫「type-specific 顏色/icon」？還是保留 count-only 簡單視覺？
- Projection 歷史 snapshot 要存哪（trace 內？獨立表？）— 空間 vs 查詢方便的 trade-off

---

## 附錄：調查來源

### 前輪 Codex jobs（2026-04-21 早上）

| Job ID | Duration | 範圍 |
|---|---|---|
| `task-mo8p3czc-ugnc39` | 3m10s | A 節 — main Probe Chain 主線 |
| `task-mo8p4108-ufdfd0` | 2m32s | B 節 — watchAlive 分支 5 race |
| `task-mo8p4hqi-fd2y2u` | 6m32s | C 節 — SPA 燈號顯示 |

### 本輪 Codex jobs（2026-04-21 下午）

| Job ID | Duration | 範圍 |
|---|---|---|
| `task-mo8qekrw-hjj2lh` | 5m23s | D 節 — Trace 基礎設施 |
| `task-mo8qf0qe-9tkaso` | 7m01s | E 節 — Hook/probe 主從邊界 |
| `task-mo8qfg1q-7ye5eo` | 7m18s | F 節 — 三 agent 對稱性 + subagent 模型 |

### Memory 檔案

- `project_probe_architecture.md` — 原始架構設計意圖（8 天前）
- `project_watch_alive_review_findings.md` — PR #486 5 race + 本日重檢
- `project_lights_current_status.md` — 前輪現況盤點（Probe 3/5 + watchAlive NO + SPA 3.5/5）
