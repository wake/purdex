# Lights Rebuild — Hook → Status → 燈號 對齊 Audit

- **Date**: 2026-04-28
- **Worktree**: `lights-w1-audit`（branch `worktree-lights-w1-audit`）
- **Work item**: W1（per `docs/specs/2026-04-28-lights-rebuild-fix-spec.md` §2）
- **Type**: 純 docs，無 code
- **Replaces**: 不取代任何文件；本 doc 是 fix-spec PR-2 產出

---

## 0. 來龍去脈

W1 是 fix-spec §4 PR-2 工作 — 在 W3 撤回 generic framework 之前，先盤點三家 agent 的 hook → status → 燈號 路徑，找出實際 bug 與 probe 缺口，作為 W5 / W6 工作池依據。

純 docs 無 code；audit 結果直接驅動：

- **W3** 撤回時 `manageActivityWatch` policy 改 per-agent gating 的依據
- **W4** dev log 補完路徑優先序的依據
- **W5** 燈號 bug 修復 PR 順序的依據
- **W6** ProbeIntent 設計缺口導向的依據

不做：自設計 / 自修 bug / 自寫 probe — 屬 W3-W6。

### 0.1 Audit Baseline（重要 framing）

**本 audit 描述的是 W3 撤回後的純 hook coverage 形態（post-W3 baseline）。**

當前 main（alpha.234+）有 PR-4a-1 ship 的 always-on activity probe（`internal/module/agent/module.go:469-501` `manageActivityWatch` 在 status ∈ {waiting, running, idle} 啟動 watcher，screen-change → running、screen-stable → idle）— 此 probe 對 §6 部分 W5 條目（如 W5-1 cc permission→running、W5-3 cc compact→idle）**目前提供不精準的補位**，所以 current main 的使用者**體感不到「lights 卡死」**。

W3 撤回 always-on policy 後，這些補位才會裸露。W5 / W6 工作池條目以**post-W3 baseline 為準**，因為 W5 / W6 PR 全部在 W3 撤回後執行（per fix-spec §4 PR 順序）。

### 0.2 Quick Paths

| 想看什麼 | 跳到 |
|----------|------|
| Audit 方法論 / hook 路徑分類 | §1, §3 |
| 5 status 定義 + legend | §2 |
| cc 矩陣 + 事件詳述 | §4.1 |
| codex 矩陣（FutureOnly 主缺口）| §4.2 |
| opencode 矩陣（plugin filter）| §4.3 |
| 跨家比對 | §5 |
| W5 燈號 bug 工作池（**canonical**）| §6 |
| W6 probe 缺口工作池（**canonical** + 設計約束 + 推薦順序）| §7 |
| Cross-cutting 共通 platform prerequisites | §8 |
| 結束條件 + 後續 hand-off | §9, §10 |

### 0.3 Symbol Legend（§4 矩陣 + §6/§7 統一使用）

| 符號 | 意義 |
|------|------|
| `✓` | 路徑可達（hook fire 且 DeriveStatus 推 Status）|
| `✗` | 路徑不可達 |
| `⚠` | 部分可達 / 多源 / 條件性 |
| `🐛` | 已知 bug（配對 W5-N）|
| `🚨` | 嚴重缺口（catalog 已宣告但 CLI 不 fire / 物理不可達）|
| `(FutureOnly)` | catalog FutureOnly 標識，CLI 0.124.0 不 fire |

---

## 1. Audit 範圍與目標

### 1.1 範圍

三家 agent (cc / codex / opencode) × 5 status × 全部 hook 觸發路徑：

- 🔧 **Catalog event**：`events.go` 列出且 Handling 非 `Unsupported` / `Ignored` 的事件
- 🧬 **Subagent**：`SubagentStart` / `SubagentStop` 路徑（detail-only, Status=""）
- 🧩 **Filter / 合成路徑**：plugin / proxy 端基於 upstream signal 過濾或合成投遞 catalog event（opencode plugin runtime filter；不含 cc statusline-proxy 的 render-data channel）
- ⚠️ **Error**：`StopFailure` / catalog miss known-but-unmappable reason（如 `notification_unknown_type`）對應路徑
- 🔚 **SessionEnd / Clear**：對應 `status=clear` 的退場路徑
- ❓ **Catalog miss**：`DeriveResult{Valid: false, Reason: ...}` 路徑（含 known-but-unmappable 與 truly-unknown）

### 1.2 目標

每家 × 5 status 矩陣呈現三件事（**不**驗證狀態機 transition；只列來源與可達性）：

1. **可達性**：post-W3 baseline 下 status X 可由哪些 catalog event / filter 路徑達到
2. **bug**：實際運行中誤判 / 漏發 / 重發 / 競爭等 lights 不對的情境（標 W5-N 引用至 §6）
3. **probe 缺口**：hook coverage 物理上不可能達到 status X 的縫隙（標 W6-N 引用至 §7）

跨家不一致整理在 §5。**真正的 transition 驗證需要 runtime trace evidence**（屬 W4 observability + W6 ad-hoc probe ship 後的事），不在本 audit 結束條件內。

### 1.3 不在範圍

- ❌ 設計 / 修 bug / 寫 probe — 屬 W3-W6
- ❌ Inspector UI 視覺化 — 屬 W7
- ❌ Phase 0-3.5 已 ship 的設計 — 不重新討論
- ❌ Tab / SPA 端 status projection — 本 audit 只到 broadcast 入口，不追 SPA UI 渲染
- ❌ Runtime trace transition 驗證 — 需 W4 + 實機觀察，本 audit 只列「理論可達性 + 文獻 + 已知 bug」

---

## 2. 5 Status 對齊框架

`internal/agent/status.go:6-11` 定義五個 status 常數：

| Status | 語意 | 主要觸發點 | UI（lights spec §4） |
|--------|------|-----------|----------------------|
| `running` | agent 正在處理使用者請求 | `UserPromptSubmit` | 藍 / 綠 |
| `waiting` | agent 暫停等使用者輸入 | `PermissionRequest`、`Notification(permission_prompt / elicitation_dialog)` | 黃 |
| `idle` | agent 處理完畢、可接受新 prompt | `Stop` / `SessionStart` / `Notification(idle_prompt / auth_success)` | 灰 / 暗 |
| `error` | agent 異常終止 | `StopFailure` | 紅 |
| `clear` | session 結束、無 agent 在線 | `SessionEnd` | 不顯燈 |

**狀態機假設**（lights spec §3）：`running → idle → running → waiting → running → ... → error / clear`，error/clear 為單向終態。

每家 audit 需驗證這假設在實際 hook 下是否成立。

---

## 3. Hook 路徑分類

### 3.1 主路徑（catalog event 直接 emit status）

`events.go` 中 `EmitsStatus != []` 的 entry，`DeriveStatus` 直接回 `Valid=true, Status=...`。

### 3.2 Subagent detail-only 路徑（Status=""）

`SubagentStart` / `SubagentStop`：`Valid=true` 但 Status 為空 — **不改 lights**，但 handler `applyFrameEvent` (`internal/module/agent/handler.go:202` + `frame_ops.go:133-174`) **會持久化 frame.Subagents membership**。  
邊界情境：

- `frame_meta.Decision != "updated_frame"`（如 `frame_missing` / `subagent_id_missing`）→ handler `handler.go:236-242` 早 return，**skip broadcast**
- 唯一 mutation 是 frame subagents list；`currentStatus` / `m.subagents` 不變動

### 3.3 Filter / 合成路徑（plugin / proxy 端）

agent-side plugin 或 proxy 端基於 upstream signal 過濾或合成投遞 catalog event。**這是 hook 入口的 _前置處理_，不是 daemon 端的 status 推斷**：

- **opencode**：plugin 內 runtime filter（Decision 3：`session.status` filter `type==='idle'` → emit `Stop`；Decision 4 defer：busy/retry receive-but-no-op）— filter 結果仍進 daemon catalog event 路徑
- **cc**：⚠️ **無** lights status filter。`statusline-proxy`（`cmd/pdx/statusline_proxy.go`）只 ferry render data（model/cost/context）至 `/api/agent/status`，廣播 `agent.status` WS event 給 SPA UI 顯示；**不**經 DeriveStatus、**不**進 lights hook 路徑
- **codex**：✗ 無 plugin / proxy / filter

> ⚠️ **W6 ProbeIntent 不屬此分類**。W6 走 spec §8.2 既定 `ProbeIntentProvider` 模型 — probe 產生 **probe signal**（如 process exit、TUI 樣式變化），經 per-agent detector 的 `on_signal` 映射為 status 更新；**不**偽裝為 hook event、**不**合成 `UserPromptSubmit` 之類 catalog event。詳 §7.1 設計約束。

### 3.4 Error 路徑

- `StopFailure` → `status=error`
- `Notification` 子型未知 → `DeriveResult{Valid: false, Reason: "notification_unknown_type"}`
- `SessionStart` source=compact → `DeriveResult{Valid: false, Reason: "compact_ignored"}`
- catalog miss truly-unknown → `DeriveResult{Valid: false, Reason: ""}` → handler `handler.go:155-158` 正規化為 `"event_not_in_catalog"`

**所有 invalid 結果均記 verify-kind trace**（`handler.go:159` `trace.Verify(req, "skipped", reason, nil)`）— Reason 空與否只決定 reason 字串；trace 都會寫，不存在「不記 trace」的子型別。Reason 非空 = 保留 provider 給的具體原因（如 `compact_ignored`）；Reason 空 = handler 補預設 `event_not_in_catalog`。

### 3.5 SessionEnd / Clear 路徑

`SessionEnd` → `status=clear`；daemon 在此清掉 lights 並考慮是否 retain TraceStore frame。

### 3.6 Catalog miss 路徑

`Valid=false` 全部進此分類，Reason 區分兩子型：

- **known-but-unmappable**（Reason != ""）：catalog 認得 event name，但 payload 不能映射到 status
- **truly-unknown**（Reason == ""）：event name 不在 catalog

---

## 4. 三家 Agent 矩陣

### 4.0 通用 Schema

每家 §4.X 子節結構一致：

- **§4.X.1 Inventory & Source**：catalog files / DeriveStatus / installer / 特殊結構（FutureOnly / plugin filter）
- **§4.X.2 Status Matrix**：5 status × 路徑分類（schema 三家統一）
- **§4.X.3 Event Details**：installable 事件層詳述
- **§4.X.4 W5 / W6 References**：列出 IDs，內容歸 §6 / §7 canonical 工作池（避免雙來源 drift）

矩陣 schema（三家統一）：

| Status | 主路徑 (catalog event) | Filter / Plugin 路徑 | Catalog miss reason | bug ref | probe ref |
|--------|-----------------------|----------------------|---------------------|---------|-----------|

每格符號統一見 §0.3 legend。`bug ref` / `probe ref` 只標 ID（W5-N / W6-N），詳情查 §6 / §7。

> **判讀規則補充**：
> - **EmitsStatus 多元 entry**（如 cc Notification → `[Waiting, Idle]`）：拆 polymorphic 子型獨立列入對應 status row
> - **FutureOnly entry**：`✓ (FutureOnly)` 標記，並評估「目前運行 hook 是否實際發送」— 若 CLI 不發 = 缺口候選
> - **Subagent / Proxy 路徑**：不入 status matrix（Status="" 不影響 lights；statusline-proxy 為獨立 channel）；說明歸 §4.X.1 / §4.X.3
> - **catalog miss reason**：列 provider 給的字串（`compact_ignored` / `notification_unknown_type`），truly-unknown 統一寫 `event_not_in_catalog`

### 4.1 cc

#### 4.1.1 Inventory & Source

| 項目 | 位置 / 內容 |
|------|-------------|
| Catalog 來源 | `internal/agent/cc/events.go`：29 entries（9 installable + 20 Unsupported / Ignored） |
| DeriveStatus | `internal/agent/cc/status.go:9-93` |
| Hook installer | `internal/agent/cc/hooks.go:116-162`（mergeClaudeHooks 寫入 `~/.claude/settings.json` "hooks" key；每筆 `pdx hook --agent cc <event>`） |
| Filter / Plugin | ✗ 無 lights status filter |
| 獨立 channel | `statusline-proxy`（`cmd/pdx/statusline_proxy.go` + `internal/module/agent/handler.go:864-912`）— 只 ferry render data (model/context/cost) 至 `agent.status` WS event，**不**進 lights 路徑 |
| Operator probe | `cc/operator.go:14-34` `Interrupt` 用 `prober.CheckReadiness("cc",...)` 輪詢 idle |

#### 4.1.2 Status Matrix

| Status | 主路徑 (catalog event) | Filter / Plugin | Catalog miss reason | bug ref | probe ref |
|--------|-----------------------|-----------------|---------------------|---------|-----------|
| `running` | ✓ `UserPromptSubmit` | ✗ | — | 🐛 W5-1 | W6-1 |
| `waiting` | ⚠ **dual**：`PermissionRequest` + `Notification(permission_prompt / elicitation_dialog)` | ✗ | — | 🐛 W5-2 (downgraded) | — |
| `idle` | ⚠ **multi**：`Stop` / `SessionStart(non-compact)` / `Notification(idle_prompt / auth_success)` | ✗ | `compact_ignored` (known-but-unmappable) | 🐛 W5-3 | W6-2 |
| `error` | ✓ `StopFailure` | ✗ | `notification_unknown_type` (known-but-unmappable) | — | — |
| `clear` | ✓ `SessionEnd` | ✗ | — | — | — |

#### 4.1.3 Event Details（installable only）

1. **`SessionStart`** → `idle`（non-compact）/ `compact_ignored`（compact source）
   - DeriveStatus `cc/status.go:14-22`：`raw["source"] == "compact"` 走 invalid + reason
   - Handler `handler.go:154-173` 對 invalid + reason 走 verify-trace（`skipped` decision）+ 200 OK，**不改 lights**
   - `SessionStart` 觸發時 handler `handler.go:301-305` 清 subagents
2. **`UserPromptSubmit`** → `running`
   - DeriveStatus `cc/status.go:24-28`：無條件 running
   - Error guard 白名單（`handler.go:181`）：error 狀態下可清回 running
3. **`SubagentStart` / `SubagentStop`** → status="" (detail-only)
   - DeriveStatus `cc/status.go:85-89`：valid + detail
   - Handler `handler.go:235-258`：**會持久化 frame.Subagents membership**（`frame_ops.go:133-174`）；status 不變動；`frame_missing` / `subagent_id_missing` 時 skip broadcast 早 return
4. **`Stop`** → `idle`
   - DeriveStatus `cc/status.go:59-67`：infallible idle
   - Error guard 白名單（`handler.go:188`）：cc/codex error 狀態下可清回 idle（不適用 opencode）
5. **`StopFailure`** → `error`
   - DeriveStatus `cc/status.go:69-77`：infallible error
   - error 後續事件被 error guard 阻擋，需 `UserPromptSubmit` / `SessionStart` / `Stop`(cc/codex) / `SessionEnd` 之一才能離開
6. **`Notification`** → `waiting` (permission_prompt / elicitation_dialog) / `idle` (idle_prompt / auth_success) / `notification_unknown_type`（其他）
   - DeriveStatus `cc/status.go:30-48` 4 個已知子型 + reason fallback
7. **`PermissionRequest`** → `waiting`
   - DeriveStatus `cc/status.go:50-57`：無條件 waiting
   - **與 `Notification(permission_prompt)` 重複**：catalog 同時宣告兩條；runtime 是否兩條都送由 cc CLI 版本決定（W5-2，downgraded — needs trace evidence）
8. **`SessionEnd`** → `clear`
   - DeriveStatus `cc/status.go:79-83`：infallible clear
   - Handler `handler.go:278-285`：刪 currentStatus + subagents
   - Error guard 白名單（`handler.go:186`）：必過

#### 4.1.4 W5 / W6 References

- **W5**: W5-1, W5-2, W5-3 — 詳 §6
- **W6**: W6-1, W6-2 — 詳 §7

### 4.2 codex

#### 4.2.1 Inventory & Source

| 項目 | 位置 / 內容 |
|------|-------------|
| Catalog 來源 | `internal/agent/codex/events.go`：11 entries（9 installable + 2 Unsupported = PreToolUse / PostToolUse） |
| DeriveStatus | `internal/agent/codex/status.go:9-76`（mirror cc Notification + Stop/SubagentX detail-only） |
| Hook installer | `internal/agent/codex/hooks.go:108-128, 209-298`（installCodexHooks 寫入 `~/.codex/hooks.json` matcher-group + `~/.codex/config.toml` 啟用 `features.codex_hooks=true`） |
| Filter / Plugin | ✗ 無 plugin / proxy / filter；hooks 是唯一 status 入口 |
| Operator probe | ✗ 無（cf. cc 有；W6-3 / W6-4 完成可順帶 readiness 整合）|

**FutureOnly 結構特性**：codex 9 installable 中 **5 個為 FutureOnly**（`SubagentStart` / `SubagentStop` / `StopFailure` / `Notification` / `SessionEnd`） — catalog 已宣告、installer 已寫入、DeriveStatus 已能解析，**但 codex CLI 0.124.0 不主動 fire 這些事件**（cf. `internal/agent/codex/events.go:11-15` 註釋 "may not be emitted by the current codex CLI in every path"）。

| Active (4) | FutureOnly (5) — 0.124.0 不發 |
|------------|-------------------------------|
| `SessionStart`、`UserPromptSubmit`、`Stop`、`PermissionRequest` | `SubagentStart`、`SubagentStop`、`StopFailure`、`Notification`、`SessionEnd` |

#### 4.2.2 Status Matrix

| Status | 主路徑 (catalog event) | Filter / Plugin | Catalog miss reason | bug ref | probe ref |
|--------|-----------------------|-----------------|---------------------|---------|-----------|
| `running` | ✓ `UserPromptSubmit` | ✗ | — | — | W6-6（PermissionRequest reply 缺口）|
| `waiting` | ✓ `PermissionRequest` | ✗ | — | — | W6-6 |
| `idle` | ⚠ multi：`Stop` / `SessionStart`(無 compact 子型) | ✗ | `notification_unknown_type` (FutureOnly 觸發機率低) | — | — |
| `error` | 🚨 `StopFailure` (FutureOnly = ✗ 0.124.0 不發) | ✗ | — | 🐛 W5-4 | W6-3 |
| `clear` | 🚨 `SessionEnd` (FutureOnly = ✗ 0.124.0 不發) | ✗ | — | 🐛 W5-5 | W6-4 |

#### 4.2.3 Event Details

1. **`SessionStart`** → `idle`
   - DeriveStatus `codex/status.go:14-15`：infallible idle（**無 cc 的 compact subtype 處理 — codex 不發 compact**）
   - Handler 同 cc 路徑（清 subagents）
2. **`UserPromptSubmit`** → `running`
   - DeriveStatus `codex/status.go:17-18`：infallible running
3. **`Stop`** → `idle`
   - DeriveStatus `codex/status.go:51-52`：infallible idle
   - Error guard 白名單（`handler.go:188`）：codex 適用 — `req.AgentType != "opencode"` 走 idle 退場路徑
4. **`PermissionRequest`** → `waiting`
   - DeriveStatus `codex/status.go:42-49`：infallible waiting
   - ⚠️ catalog 無 `permission.replied` 等對應 reply event；user 答覆後無 hook → W6-6 probe 補位
5. **`SubagentStart` / `SubagentStop`** (FutureOnly)
   - DeriveStatus `codex/status.go:67-72`：detail-only（與 cc 對齊）
   - 今日不 fire
6. **`StopFailure`** (FutureOnly)
   - DeriveStatus `codex/status.go:54-62`：infallible error
   - 今日不 fire → W5-4 / W6-3
7. **`Notification`** (FutureOnly)
   - DeriveStatus `codex/status.go:20-40`：4 子型同 cc
   - 今日不 fire
8. **`SessionEnd`** (FutureOnly)
   - DeriveStatus `codex/status.go:64-65`：infallible clear
   - 今日不 fire → W5-5 / W6-4

#### 4.2.4 W5 / W6 References

- **W5**: W5-4, W5-5 — 詳 §6
- **W6**: W6-3, W6-4, W6-6 — 詳 §7

### 4.3 opencode

#### 4.3.1 Inventory & Source

| 項目 | 位置 / 內容 |
|------|-------------|
| Catalog 來源 | `internal/agent/opencode/events.go`：65 entries（8 installable + 20 Unsupported + 37 Ignored；只 8 installable 在 audit 範圍） |
| DeriveStatus | `internal/agent/opencode/status.go:9-31` |
| Hook installer | `internal/agent/opencode/hooks.go:33-39, 152-171`（writeManagedPlugin 寫入 `~/.config/opencode/plugins/pdx-agent-hooks.js`，**單檔 all-or-nothing managed template**） |
| Plugin template | `internal/agent/opencode/plugin_template.go:24-132`（JS plugin 訂閱 Bus event + strong hook，用 `Bun.spawn` 呼叫 `pdx hook --agent opencode <Name>`） |
| Filter / Plugin | ✓ plugin 內 runtime filter（Decision 3：`session.status` filter `type==='idle'`；Decision 4 defer：busy/retry receive-but-no-op） |
| Operator probe | ✗ 無 |

**結構差異**：opencode catalog **不含 `Notification`**（cc / codex 都有）。所有 polymorphic waiting 子型由 plugin 端兩個 Bus event 映射為同一個 `PermissionRequest`（permission.asked → request_type=permission；question.asked → request_type=question）。`idle_prompt` / `auth_success` 路徑不存在。

**Plugin 端事件映射表**（`plugin_template.go:48-129`）：

| upstream event | type filter | plugin emit | DeriveStatus → status |
|----------------|-------------|-------------|------------------------|
| `session.created` | — | `SessionStart` | `idle` |
| `session.error` | — | `StopFailure` + add to `suppressIdleForSession` Set | `error` |
| `session.status` | type==='idle' | `Stop`（`suppress` Set 命中時 skip）| `idle` |
| `session.status` | type==='busy'/'retry' | **receive-but-no-op**（Decision 4 defer）| ✗ |
| `session.deleted` | — | `SessionEnd` | `clear` |
| `permission.asked` | — | `PermissionRequest`(request_type=permission) | `waiting` |
| `question.asked` | — | `PermissionRequest`(request_type=question) | `waiting` |
| `permission.replied` | — | **Unsupported（events.go non-installable）— plugin 不 consume** | ✗ |
| `question.replied` | — | **Unsupported — plugin 不 consume** | ✗ |
| `question.rejected` | — | **Unsupported — plugin 不 consume** | ✗ |
| `chat.message`(strong hook) | — | `UserPromptSubmit` + clear `suppress` Set for sessionID | `running` |
| `tool.execute.before` | tool==='task' | `SubagentStart` | "" (detail-only) |
| `tool.execute.after` | tool==='task' | `SubagentStop` | "" (detail-only) |

**suppressIdleForSession 機制**（`plugin_template.go:28, 68, 76-79, 92`）：

- `session.error` fire 時把 sessionID 加入 Set
- 下一個 `session.status` idle 進來若 sessionID 在 Set → **skip emit Stop**（保留 error 狀態）
- `chat.message`（新 prompt cycle）開始時 delete sessionID（重設 — 否則下個正常 idle 會被 stale entry 吃掉）
- ⚠️ JS plugin in-process Set；plugin 重載 / opencode 重啟 → Set 清空 → race 中可能漏 suppress

#### 4.3.2 Status Matrix

| Status | 主路徑 (catalog event) | Filter / Plugin | Catalog miss reason | bug ref | probe ref |
|--------|-----------------------|-----------------|---------------------|---------|-----------|
| `running` | ✓ `UserPromptSubmit` | ⚠ Decision 4 defer：busy/retry 變體無映射 | — | 🐛 W5-6, 🐛 W5-8 | W6-5 |
| `waiting` | ✓ `PermissionRequest`(request_type=permission OR question) | ✓ plugin filter（permission.asked / question.asked → 同 catalog event） | — | — | — |
| `idle` | ⚠ multi：`Stop`(via session.status filter) / `SessionStart` | ✓ plugin filter（session.status type==='idle' → Stop；suppressIdle 過濾）| — | 🐛 W5-7 | — |
| `error` | ✓ `StopFailure` | ✓ plugin filter（session.error → StopFailure + suppress armed）| — | 🐛 W5-7 | — |
| `clear` | ✓ `SessionEnd` | ✓ plugin filter（session.deleted）| — | — | — |

#### 4.3.3 Event Details（installable only）

1. **`SessionStart`** → `idle`
   - DeriveStatus `opencode/status.go:14-15`：infallible idle
   - plugin 來源：session.created Bus event
2. **`UserPromptSubmit`** → `running`
   - DeriveStatus `opencode/status.go:16-17`：infallible running，附 model
   - plugin 來源：chat.message strong hook（同時 clear suppressIdleForSession entry）
3. **`SubagentStart` / `SubagentStop`** → status="" (detail-only)
   - DeriveStatus `opencode/status.go:18-19`：detail-only with `agent_id` / `agent_type` / `description` / `prompt` / `title` / `output`
   - plugin 來源：tool.execute.before/after with input.tool==='task'
   - Handler `handler.go:235-258`：會持久化 frame.Subagents；`frame_missing` / `subagent_id_missing` skip broadcast
4. **`PermissionRequest`** → `waiting`
   - DeriveStatus `opencode/status.go:20-21`：infallible waiting，含 request_type 區分 permission / question
   - plugin 來源：permission.asked OR question.asked
   - ⚠️ user reply 後 upstream `permission.replied` / `question.replied` 不被 plugin consume → **無 waiting 退場 hook** → W5-8 plugin 補 mapping
5. **`Stop`** → `idle`
   - DeriveStatus `opencode/status.go:22-23`：infallible idle
   - plugin 來源：session.status with type==='idle' AND sessionID NOT in suppressIdleForSession
   - **error guard 不允許 opencode Stop 清 error**（`handler.go:187-189`）：`if req.AgentType != "opencode" { canClear = canClear || req.EventName == "Stop" }` — 與 plugin suppress 機制配合
6. **`StopFailure`** → `error`
   - DeriveStatus `opencode/status.go:24-25`：infallible error
   - plugin 來源：session.error（同步 arm suppressIdleForSession）
7. **`SessionEnd`** → `clear`
   - DeriveStatus `opencode/status.go:26-27`：infallible clear
   - plugin 來源：session.deleted

#### 4.3.4 W5 / W6 References

- **W5**: W5-6, W5-7, W5-8 — 詳 §6
- **W6**: W6-5 — 詳 §7

---

## 5. 跨家比對

### 5.1 共通結構（三家對齊）

- 5 status 同源 (`internal/agent/status.go`)
- DeriveResult / NormalizedEvent shape 同源
- Handler 入口同源 (`internal/module/agent/handler.go:91-323`)：DeriveStatus → invalid 處理 → error guard → frame → projection → broadcast
- catalog miss 兩類 (`Reason==""` truly-unknown / `Reason!=""` known-but-unmappable) 邏輯一致
- SubagentStart / SubagentStop 都是 detail-only（status=""）走 transient broadcast

### 5.2 兩家共有特性

| 特性 | cc | codex | opencode |
|------|:---:|:-----:|:--------:|
| `Notification` event（含 polymorphic 子型）| ✓ | ✓ FutureOnly | ✗ |
| `Notification` idle_prompt / auth_success → idle | ✓ | ✓ FutureOnly | ✗ |
| `Notification` permission_prompt / elicitation_dialog → waiting | ✓ | ✓ FutureOnly | ✗ |
| `SessionStart` source=compact subtype 處理 | ✓ (compact_ignored) | ✗ | ✗ |
| `PermissionRequest` 主路徑 → waiting | ✓ | ✓ | ✓ |
| Dual waiting path（PermissionRequest + Notification）| 🐛 W5-2 | 🐛 (FutureOnly 觸發後將同樣 dual) | ✗ (無 Notification) |
| Plugin / proxy mechanism | statusline-proxy（非 status channel）| ✗ | plugin（單檔 all-or-nothing） |
| Plugin 端 runtime filter | ✗ | ✗ | ✓（session.status type filter / suppressIdleForSession Set）|
| Operator → readiness probe | ✓ (`prober.CheckReadiness("cc",...)` cf. `cc/operator.go:14-34`) | ✗ | ✗ |
| Error guard：`Stop` 可清 error | ✓ | ✓ | ✗ (`handler.go:188`) |

### 5.3 各家獨有問題（精要）

**cc**：

- 雙路徑 waiting（W5-2，**已降級至「需 trace evidence」**，per R2 防守 D4）— catalog 同時宣告 `PermissionRequest` 與 `Notification(permission_prompt)`；無實機 trace 證明 dual fire 之前僅列為觀察項
- compact 退場無 hook（W5-3）— `SessionStart(compact)` 走 `compact_ignored` 不改 lights，但 compact 結束沒 PostCompact handler（`PostCompact` 為 `HookHandlingIgnored` non-installable）

**codex**：

- 5/9 installable 為 FutureOnly（SubagentStart/Stop / StopFailure / Notification / SessionEnd）— **error 與 clear 兩 status 物理上不可達**（W5-4 / W5-5）；CLI 0.124.0 只 fire 4 事件
- 無 Notification → polymorphic waiting / 多源 idle 路徑全缺；同時 `PermissionRequest` 後 user reply 也無 hook（W6-6 補位）

**opencode**：

- Plugin 端 in-process state（`suppressIdleForSession` Set）有 race 風險 — plugin 重啟 / opencode 重啟即遺失（合併入 W5-7）
- error guard 對 opencode Stop 特殊處理（`handler.go:187-189`）— 跟 plugin suppress 互鎖；plugin suppress 失效時 daemon 端阻擋會卡死（合併入 W5-7）
- Decision 4 defer：busy/retry 變體 receive-but-no-op（W5-6）— 已知 follow-up issue #661
- upstream `permission.replied` / `question.replied` / `question.rejected` 全 Unsupported，plugin 不 consume → user reply waiting → running 缺 plugin mapping（W5-8）

### 5.4 跨家觀察優先序

依 fix-spec §0 「probe 不是 always-on，是缺口導向」原則，三家 probe 缺口輕重排序：

1. **codex error / clear（W6-3 / W6-4）** — **P1** 主缺口，今日完全不可達
2. **codex PermissionRequest reply（W6-6）** — P2，常見路徑但有 plugin 補 mapping 替代方案
3. **cc permission / compact 退場（W6-1 / W6-2）** — P2 體感優化（W3 撤 always-on probe 後 W6-1 條件升 P1，per R1 F6）
4. **opencode busy/retry（W6-5）** — P3，可能 plugin 補 mapping 替代

> opencode plugin reconcile（原 W6-6）因「更像 RPC 不是 probe」（per R2 防守 D3，spec §1 邊界）已**移出 W6 工作池**，整併入 W5-7 plugin lifecycle reconciliation bug。

---

## 6. W5 燈號 Bug 工作池（**canonical**）

> 本表為 W5 工作池 single source of truth。§4 矩陣 `bug ref` 欄位只列 ID，詳情查此處。  
> Status / PR / decision 不在本表維護 — ship / 廢棄 / 拆分由對應 issue 與 PR 追蹤；本表只記**發現時的 bug 描述與修復方向**。

| ID | agent | 影響 status | 觸發條件 | 期望（post-W3 baseline） | 實際（post-W3 baseline） | 修復複雜度 | 配對 |
|----|-------|-------------|---------|------|------|------------|------|
| **W5-1** | cc | running | `Notification(permission_prompt)` 或 `PermissionRequest` → user 於 cc TUI 批准 → cc 繼續處理（無 hook fire 直到 Stop）| 批准後 lights 顯示 running | lights 仍顯 waiting 直到 Stop | L（依賴 W6-1 probe）| W6-1 |
| **W5-2** | cc | waiting | cc 是否同時 fire `PermissionRequest` 與 `Notification(permission_prompt)` 兩條 hook（catalog 已宣告 dual） | （unknown — needs trace evidence） | （unknown — needs trace evidence） | **降級為觀察項**（per R2 防守 D4）：先以 W4 dev log / TraceStore 收 trace；確認 dual fire 後才升 bug；修復限定 plumbing 層 idempotency（不改 catalog 主從關係，per spec §2.4.3/.4） | — |
| **W5-3** | cc | idle | `SessionStart(compact)` 走 `compact_ignored`；compact 結束無 PostCompact handler（events.go `PostCompact` = `HookHandlingIgnored`）| compact 結束後 lights 適切轉換到 idle / running | lights 停在 compact 前的最後狀態 | L（依賴 W6-2 probe + 評估改 catalog 把 PostCompact 從 ignored 移為 handled）| W6-2 |
| **W5-4** | codex | error | codex CLI 任何 error（API failure / tool error / crash）| lights 顯紅 | **永不顯紅** — `StopFailure` FutureOnly，CLI 0.124.0 不 fire | L（依賴 W6-3 probe；catalog 已備但 CLI 不發；W6-3 first PR 範圍**僅 process-exit + crash detection**，TUI / stderr API/tool error 屬同一 P1 缺口但**拆 follow-up PR**，per R1 F4 收斂）| W6-3 |
| **W5-5** | codex | clear | codex session 結束（CLI close / sandbox 終止）| lights 清空 | **永不清空** — `SessionEnd` FutureOnly，CLI 0.124.0 不 fire | L（依賴 W6-4 probe；catalog 已備但 CLI 不發）| W6-4 |
| **W5-6** | opencode | running（中介）| session.status type='busy' 或 'retry' | lights 反映重試 / 中介狀態（或保持 running） | plugin receive-but-no-op，狀態停滯 | S（plugin 加 mapping；可能 events.go 補 entry 即可；issue #661 已追蹤）| W6-5（替代方案）|
| **W5-7** | opencode | error / idle / lifecycle 多重交織（merged from R1 W5-7 + R1 W5-8 + R2 防守 D3 W6-6）| (a) session.error 後 plugin 重啟 → `suppressIdleForSession` Set 丟失 → 下個 session.status idle 不被 suppress → fire Stop → daemon error guard `handler.go:187-188` 阻擋；(b) 使用者不送新 prompt 也不結束 session 時 lights 永久卡 error；(c) plugin reconnect 時 daemon 缺 reconciliation 路徑 | plugin lifecycle 過渡期 lights 維持一致；user 有可預期的退場路徑 | (a) trace `error_guard_blocked`；(b) 唯一退場是手動 SessionEnd；(c) reconnect 後 daemon 與 plugin in-process state 不同步 | M-L（複合：plugin 補狀態 reconciliation + daemon 端可選擇放寬 opencode error guard 或加 reconnect RPC；建議拆 sub-PR 處理）| — (RPC 性質，per R2 防守 D3 不放 W6) |
| **W5-8** | opencode | running | user 於 opencode TUI 回答 permission / question → upstream `permission.replied` / `question.replied` / `question.rejected` fire 但 plugin 不 consume（events.go non-installable）→ 下次 hook 之前 lights 卡 waiting | reply 後 lights 顯示 running | lights 仍顯 waiting 直到下個 session.status idle 或 chat.message | S（plugin 補 emit mapping：reply → 合成 catalog event 進 hook；events.go 對應 entry 從 Unsupported 移為 installable；不需 probe）| — (plugin-side fix) |

**複雜度判準**：

- **S**：改 1–2 行 + 1 個 unit test，無新概念，無 probe
- **M**：跨 file / 改 catalog / 加新 unit test，可能 plugin 端改動
- **L**：依賴新 probe / DeriveStatus 邏輯結構性改動 / 需 W6 ad-hoc probe 先 ship

---

## 7. W6 Probe 缺口工作池（**canonical**）

> 本表為 W6 工作池 single source of truth。§4 矩陣 `probe ref` 欄位只列 ID，詳情查此處。

| ID | agent | 缺口 status | 缺口描述 | 補位構想（probe signal → status mapping，per spec §8.2）| 優先序 | 配對 W5 |
|----|-------|-------------|---------|-------------------------------------------------------|--------|---------|
| **W6-1** | cc | running | permission 批准後無 hook（W5-1）| cc provider 宣告 ProbeIntent；detector 觀察 cc TUI spinner 字元出現/消失（tmux capture）；signal `tui_spinner_visible` 在 status==waiting → status=running；spinner 消失於 status==running 且不在 grace window → 不變動（等 Stop hook） | **P2**（W3 撤 always-on probe 後可能升 **P1**，per R1 F6）| W5-1 |
| **W6-2** | cc | idle | compact 結束無 hook（W5-3，PostCompact ignored）| cc provider 宣告 ProbeIntent；detector 觀察 cc TUI compact 對話框退場 → 回到主 prompt；signal `tui_compact_exited` → status=idle；或評估改 catalog 把 PostCompact 從 ignored 移為 status emitting（純 hook 解，不需 probe）| P2 | W5-3 |
| **W6-3** | codex | error | codex error 物理不可達（W5-4）| codex provider 宣告 ProbeIntent；detector 觀察 codex 進程 exit code（非零）；signal `process_error_exit` → status=error。**約束（per R2 防守 D2）**：detector 實作歸 `internal/agent/codex/`；module 層只負責 plumbing；只在 status ∈ {running, waiting} 且 codex sender PID 已知時 watch；status==idle 或 PID 缺失時 unwatch；first PR 僅 process-exit + crash detection，TUI / stderr 偵測為延伸 PR | **P1** | W5-4 |
| **W6-4** | codex | clear | codex session 結束物理不可達（W5-5）| codex provider 宣告 ProbeIntent；detector 觀察 codex 進程結束（exit code 0 或 SIGTERM）+ tmux pane 退場；signal `process_normal_exit` → status=clear。**約束**：同 W6-3，per-agent only；只在 codex sender PID 已知 + session 仍 active 時 watch | **P1** | W5-5 |
| **W6-5** | opencode | running 中介 | busy/retry 變體無映射（W5-6）| **首選方案：W5-6 plugin 端補 mapping** — 不需 probe（issue #661）。若決議 probe：opencode provider 宣告 ProbeIntent；detector 觀察 opencode TUI spinner / retry 提示文字；signal `tui_retry_indicator_shown` 在 status==running 期間記為中介 sub-state（不改 lights status，僅作 detail observability）| P3 | W5-6 |
| **W6-6** | codex | running | `PermissionRequest` 後 user reply 無 hook（catalog 無 reply event；per R2 攻擊 A1）| codex provider 宣告 ProbeIntent；detector 觀察 codex TUI permission 對話框消失 + spinner 復現；signal `tui_permission_dismissed` 在 status==waiting → status=running。**約束**：per-agent only，detector 歸 `internal/agent/codex/`；status!=waiting 時 unwatch | P2 | — (新缺口) |

**優先序判準**：

- **P1**：影響核心使用情境（status 物理上不可達、使用者立即可感）
- **P2**：邊緣場景（特殊 agent 行為、罕見路徑、體感優化）；條件性可升 P1（如 W3 撤 always-on probe 後）
- **P3**：nice-to-have（觀察用、有 hook / plugin 替代方案）

### 7.1 設計約束（per fix-spec §0 + §1，與 lights-rebuild-spec §8.2）

#### 7.1.1 ProbeIntent 模型（per R2 防守 D1 / R1 F5）

W6 走 spec §8.2 既定 `ProbeIntentProvider` interface 模型。每個 W6 條目的 detector 產生 **probe signal**，經 per-agent 的 `on_signal` 邏輯映射為 status 更新：

```
detector → signal (e.g. process_error_exit / tui_spinner_visible)
  → per-agent on_signal handler
  → daemon plumbing 更新 currentStatus + broadcast
```

**禁止做的事**：

- ❌ probe 偽裝為 hook event（**不要**寫 "fire 合成 UserPromptSubmit-equivalent" 之類的描述）— probe 與 hook 是兩條獨立 channel；hook 權威、probe 推論
- ❌ 跨 agent 的中央 liveness watcher / 中央狀態轉移規則（如 generic `any→error`）
- ❌ generic ProbeProfileProvider / always-on policy（fix-spec §3 已列待 W3 撤回）

**該做的事**：

- ✅ 每個 W6 ProbeIntent **由對應 agent provider 透過 `ProbeIntentProvider` interface 宣告**（spec §8.2 既定）；detector 實作歸 `internal/agent/{cc,codex,opencode}/probe_intent_*.go`
- ✅ `internal/module/agent/` 只保留共用 plumbing / lifecycle（watcher 啟停、grace window、stale callback guard 等 PR-4a-1 ship 的 utilities）
- ✅ ProbeIntent gating 為條件式而非布林開關 — 例：W6-3 在 status ∈ {running, waiting} 且 sender PID 已知時 watch；status==idle 或 PID 缺失時 unwatch
- ✅ ProbeIntent interface lazy 設計 — 等 W6-3 實作時 finalize interface shape，不預先抽象

#### 7.1.2 W6 Platform Prerequisite（per R2 攻擊 A2）

**Daemon restart / reconnect 後 watcher 不恢復** — `internal/module/agent/module.go:217` `Start()` → `replayFromDB()` 只重建 `currentStatus`，**不**呼叫 `manageActivityWatch()` 恢復 `activeWatchers`。所有 W6 ProbeIntent 在 daemon 重啟後**直到下個 hook 才會重新掛**。

此非 W6 工作池條目（屬 plumbing 層而非 ProbeIntent），但**所有 W6 在 daemon restart 場景下的功能性依賴此修復**。建議：

- 列為 W4 observability 完成後接著處理的 platform plumbing
- 或於 W6-3 ship 時順帶補 startup replay → projection top status → re-arm gated watchers
- 開 follow-up issue 追蹤（W1 audit ship 後立即開）

#### 7.1.3 五大 Bloat 徵兆 Self-Check

警覺 `feedback_skeleton_convergence` 五大 bloat 徵兆 — 每個 W6 PR 設計時自我檢查：

- 把 working code 變 data
- parallel registry
- 統一抽象（generic framework）
- refactor working code without functional reason
- config flag

任一冒出 → 停手 surface（per fix-spec §7）。

### 7.2 W6 PR 推薦實作順序

1. **W6-3 codex error**（最簡：純 process exit 觀察）→ 第一個 ProbeIntent，藉此 finalize interface shape；first PR 範圍**僅含 process-exit + crash detection**（含 exit code），TUI / stderr 偵測 API/tool error 屬同一 P1 缺口但**拆 follow-up PR 處理**（per R1 F4 收斂）
2. **W6-4 codex clear**（同 W6-3 機制延伸）— 沿用 W6-3 確立的 interface
3. **W6-6 codex PermissionRequest reply**（沿用 W6-3 / W6-4 interface，TUI 文字觀察為主）
4. **W6-5 opencode busy/retry**（**首選 W5-6 plugin 補 mapping，不 ship W6**；若仍需 probe，沿用 interface）
5. **W6-1 cc permission spinner**（W3 撤 always-on probe 後優先序可能升 P1；TUI 樣式觀察）
6. **W6-2 cc compact**（同 W6-1 機制延伸；可評估改 catalog PostCompact 取代 probe）

---

## 8. 結束條件

當下列全達成時 W1 視為完成：

1. ✅ §4 三家 × 5 status 矩陣全填，schema 一致
2. ✅ §5 跨家比對列出共通結構 / 兩家共有特性 / 各家獨有問題 / 跨家觀察優先序
3. ✅ §6 W5 工作池為 canonical 版本（§4 不重複描述）
4. ✅ §7 W6 工作池為 canonical 版本，含設計約束（§7.1）+ Platform Prerequisite（§7.1.2）+ 推薦順序（§7.2）
5. ✅ doc 過 codex review 兩輪（per CLAUDE.md PR Review 兩輪制）

**結束條件不含的事**（per R2 防守 D5）：

- ❌ 狀態機 transition 驗證 — 屬 W4 observability + W6 ad-hoc probe ship 後的 runtime 觀察
- ❌ W5 / W6 條目修復 — 屬 W3-W6 PR 工作

---

## 9. 後續 Hand-off

- **W2** 不依賴本 audit 結果（catalog naming 是 input/output 邊界整理，跟 audit 結果正交）；但**注意命名 disclaimer**（per R2 防守 D6）：本 doc §4 矩陣與 §6/§7 工作池中的 `SessionStart` / `Stop` / 等 catalog event name 是 **pre-W2 current command-arg / logical event label**；W2 之後 daemon 內部一律使用 PurdexName，UpstreamKey 只出現在 installer / plugin 邊界（per fix-spec §1）。W7 hand-off 到 endpoint payload 時必須使用 PurdexName，不可直接照抄本 doc 用詞
- **W3** 撤回 framework 時，`manageActivityWatch` policy 改 per-agent gating，**初始 disable list 含三家**（W6 工作池項目都還沒 ship）
- **W4** dev log 補完路徑優先序：先補 W6 缺口涉及的路徑（codex process exit / cc TUI 觀察 / opencode plugin lifecycle），再補 §5 跨家比對發現不一致的路徑
- **W5 / W6** PR 拆分以本 doc §6 / §7 為準；S 複雜度 batch 入單 PR，M / L 各自獨立 PR
- **W7** Inspector UI 的 Coverage Matrix 視覺化結構照本 doc §4 矩陣 schema；endpoint payload 字段命名遵守 W2 PurdexName 規則（不直接照抄本 doc）；§3 路徑分類為 endpoint enum 來源
- **Platform Prerequisite**（per §7.1.2）：W4 / W6 過程中處理 daemon restart watcher recovery；ship 後本 audit doc 開 follow-up issue 追蹤

---

## 10. 文獻

- 上層 spec：`docs/specs/2026-04-23-lights-rebuild-spec.md`
- Fix-spec：`docs/specs/2026-04-28-lights-rebuild-fix-spec.md`
- 5 status 定義：`internal/agent/status.go:6-11`
- catalog：`internal/agent/cc/events.go` / `internal/agent/codex/events.go` / `internal/agent/opencode/events.go`
- DeriveStatus：`internal/agent/cc/status.go` / `internal/agent/codex/status.go` / `internal/agent/opencode/status.go`
- statusline (cc proxy)：`internal/agent/cc/statusline.go`
- plugin template (opencode proxy / runtime filter)：`internal/agent/opencode/plugin_template.go`

<!-- §11 Audit Methodology Notes 已移到 §4.0 通用 Schema，避免讀者跨段對照 (per R2 體質 Q6) -->

