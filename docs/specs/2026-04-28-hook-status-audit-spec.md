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

---

## 1. Audit 範圍與目標

### 1.1 範圍

三家 agent (cc / codex / opencode) × 5 status × 全部 hook 觸發路徑：

- 🔧 **Catalog event**：`events.go` 列出且 Handling 非 `Unsupported` / `Ignored` 的事件
- 🧬 **Subagent**：`SubagentStart` / `SubagentStop` 路徑（detail-only, Status=""）
- 📡 **Proxy**：daemon 在不同情境主動合成的事件（cc statusLine wrap、opencode plugin runtime filter 等）
- ⚠️ **Error**：`StopFailure` / Notification(error) / catalog miss reason 對應路徑
- 🔚 **SessionEnd / Clear**：對應 `status=clear` 的退場路徑
- ❓ **Catalog miss**：`DeriveResult{Valid: false, Reason: ...}` 路徑（含 known-but-unmappable 與 truly-unknown）

### 1.2 目標

每家 × 5 status 矩陣呈現四件事：

1. **可達性**：當前 hook 形態下 status X 可由哪些 event 達到
2. **bug**：實際運行中誤判 / 漏發 / 重發 / 競爭等 lights 不對的情境
3. **probe 缺口**：hook coverage 物理上不可能達到 status X 的縫隙（候選 W6）
4. **跨家不一致**：同一語意行為在三家 agent 表現差異

### 1.3 不在範圍

- ❌ 設計 / 修 bug / 寫 probe — 屬 W3-W6
- ❌ Inspector UI 視覺化 — 屬 W7
- ❌ Phase 0-3.5 已 ship 的設計 — 不重新討論
- ❌ Tab / SPA 端 status projection — 本 audit 只到 broadcast 入口，不追 SPA UI 渲染

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

`SubagentStart` / `SubagentStop`：`Valid=true` 但不改 lights — daemon 應正確忽略 status 變動，但仍記錄 subagent ref。

### 3.3 Proxy 路徑

daemon 端基於其他訊號合成或過濾 hook 事件投遞自家 catalog：

- **cc**：⚠️ 無 lights status proxy。`statusline-proxy`（`cmd/pdx/statusline_proxy.go`）只 ferry render data（model/cost/context）至 `/api/agent/status` 廣播 `agent.status` WS event 給 SPA UI；**不**經 DeriveStatus、**不**合成 lights `hook` event
- **opencode**：plugin 內 runtime filter（Decision 3：`session.status` filter `type==='idle'` → emit `Stop`；Decision 4 defer：busy/retry receive-but-no-op）
- **codex**：✗ 無 proxy / filter
- **將來**：W6 寫 ad-hoc ProbeIntent 時，daemon 主動觀察 process / TUI 並投遞合成 hook event 屬此分類

### 3.4 Error 路徑

- `StopFailure` → `status=error`
- `Notification` 子型未知 → `DeriveResult{Valid: false, Reason: "notification_unknown_type"}`
- `SessionStart` source=compact → `DeriveResult{Valid: false, Reason: "compact_ignored"}`
- catalog miss truly-unknown → `DeriveResult{Valid: false}`（Reason 空）

Reason 非空 = handler 仍記 trace；Reason 空 = catalog miss 計數但不記 trace（per `internal/agent/status.go:20-26`）。

### 3.5 SessionEnd / Clear 路徑

`SessionEnd` → `status=clear`；daemon 在此清掉 lights 並考慮是否 retain TraceStore frame。

### 3.6 Catalog miss 路徑

`Valid=false` 全部進此分類，Reason 區分兩子型：

- **known-but-unmappable**（Reason != ""）：catalog 認得 event name，但 payload 不能映射到 status
- **truly-unknown**（Reason == ""）：event name 不在 catalog

---

## 4. 三家 Agent 矩陣

每家共用以下 schema：

```
| Status | 主路徑 (catalog event) | Subagent | Proxy | Error | SessionEnd | Catalog miss | bug 標記 | probe 候選 |
```

每格內容：

- `✓` = 主路徑 / `✗` = 不可達 / `⚠` = 部分可達（多源 / 條件）/ `🐛` = 已知 bug
- `bug 標記` 欄寫 bug 描述（短）+ 引用 W5 工作池編號（W5-N）
- `probe 候選` 欄寫缺口描述 + 引用 W6 工作池編號（W6-N）

### 4.1 cc

**Catalog 來源**：`internal/agent/cc/events.go`：29 entries（9 installable + 20 Unsupported / Ignored）  
**DeriveStatus 邏輯**：`internal/agent/cc/status.go:9-93`  
**Hook installer**：`internal/agent/cc/hooks.go:116-162`（mergeClaudeHooks 為 9 installable 事件寫入 `~/.claude/settings.json` "hooks" key，每筆對應 `pdx hook --agent cc <event>`）  
**Proxy 機制**：`internal/agent/cc/statusline.go` + `cmd/pdx/statusline_proxy.go` — `statusline-proxy` 由 cc CLI 每 ~300ms 觸發，POST 至 daemon `/api/agent/status`（**只傳 render data**：model / context / cost；`internal/module/agent/handler.go:864-912`），**不**經 DeriveStatus、**不**廣播 lights status 事件（broadcast 用 `agent.status` event 給 SPA 顯示，跟 lights 用的 `hook` event 是兩條獨立 channel）

#### 4.1.1 5 status 對齊矩陣

| Status | 主路徑 (catalog event) | Subagent | Proxy | Error | SessionEnd | Catalog miss | bug 標記 | probe 候選 |
|--------|-----------------------|----------|-------|-------|-----------|--------------|----------|-----------|
| `running` | ✓ `UserPromptSubmit` | ✗ | ✗ | ✗ | ✗ | — | 🐛 W5-1（permission 批准後不重發）| W6-1（permission 批准 → running 補位） |
| `waiting` | ⚠ **dual**：`PermissionRequest` + `Notification(permission_prompt / elicitation_dialog)` | ✗ | ✗ | ✗ | ✗ | — | 🐛 W5-2（dual-path 重發 / 競爭）| — |
| `idle` | ⚠ **multi**：`Stop` / `SessionStart`(non-compact) / `Notification(idle_prompt / auth_success)` | ✗ | ✗ | ✗ | ✗ | known-but-unmappable: `compact_ignored` | 🐛 W5-3（compact 結束無 idle 信號）| W6-2（compact 退場 idle 補位） |
| `error` | ✓ `StopFailure` | ✗ | ✗ | ✓ 主源 | ✗ | known-but-unmappable: `notification_unknown_type` | — | — |
| `clear` | ✓ `SessionEnd` | ✗ | ✗ | ✗ | ✓ 主源 | — | — | — |

判讀規則：

- **dual / multi**：同 status 由多個 catalog event 達到 — 跨路徑可能造成重複廣播或競爭（trace.Emit 會記 `decision` + `reason`，但 lights 可能閃爍）
- **Subagent / Proxy**：cc 的 `SubagentStart` / `SubagentStop` 為 detail-only（status=""），handler 早 return（`handler.go:235-258`）不影響 lights；`statusline-proxy` 是獨立 channel，不入此矩陣（不影響 lights）

#### 4.1.2 事件層詳述（installable only）

1. **`SessionStart`** → `idle`（non-compact）/ `compact_ignored`（compact source）
   - DeriveStatus `cc/status.go:14-22`：`raw["source"] == "compact"` 走 invalid + reason
   - Handler `handler.go:154-173` 對 invalid + reason 走「skipped, reason」trace + 200 OK，**不改 lights**
   - 配合 `SessionStart` 觸發時 handler `handler.go:301-305` 清 subagents
2. **`UserPromptSubmit`** → `running`
   - DeriveStatus `cc/status.go:24-28`：無條件 running
   - Error guard 白名單（`handler.go:181`）：error 狀態下可清回 running
3. **`SubagentStart` / `SubagentStop`** → status=""
   - DeriveStatus `cc/status.go:85-89`：valid + detail-only
   - Handler `handler.go:235-258`：transient broadcast，不持久化、不改 lights
4. **`Stop`** → `idle`
   - DeriveStatus `cc/status.go:59-67`：infallible idle
   - Error guard 白名單（`handler.go:188`）：error 狀態下可清回 idle（`req.AgentType != "opencode"`）
5. **`StopFailure`** → `error`
   - DeriveStatus `cc/status.go:69-77`：infallible error
   - error 後續事件被 error guard 阻擋，需 `UserPromptSubmit` / `SessionStart` / `Stop` / `SessionEnd` 之一才能離開
6. **`Notification`** → `waiting`（permission_prompt / elicitation_dialog）/ `idle`（idle_prompt / auth_success）/ `notification_unknown_type`（其他）
   - DeriveStatus `cc/status.go:30-48` 4 個已知子型 + reason fallback
7. **`PermissionRequest`** → `waiting`
   - DeriveStatus `cc/status.go:50-57`：無條件 waiting
   - **與 `Notification(permission_prompt)` 重複**：兩者語意均為「等使用者批准權限」 → 同個使用者操作可能依 cc CLI 版本同時 / 先後送兩條
8. **`SessionEnd`** → `clear`
   - DeriveStatus `cc/status.go:79-83`：infallible clear
   - Handler `handler.go:278-285`：刪 currentStatus + subagents
   - error guard 白名單（`handler.go:186`）：必過

#### 4.1.3 Operator 內部 probe 使用

`cc/operator.go:14-34` `Interrupt`：`prober.CheckReadiness("cc", ...)` 輪詢 idle 確認。  
此路徑與 lights 不直接相關（內部等待機制），但跟 W6 readiness 整合屬同類觀察手段。

#### 4.1.4 cc 已知 bug 候選（含原始位置）

- **W5-1**：cc Notification(permission_prompt) → waiting → 使用者於 cc TUI 點批准 → cc 繼續處理 → **無 hook fire** → lights 仍顯 waiting 直到 `Stop`（idle）；中間 spinner 期應為 running 但缺乏觸發點
- **W5-2**：cc 同時 fire `PermissionRequest` 與 `Notification(permission_prompt)`（2.x 觀察）→ handler 連續廣播兩次 waiting → SPA lights 短暫雙重事件；trace 會看到 `decision="broadcasted"` 兩筆連發
- **W5-3**：cc SessionStart source=compact 走 `compact_ignored` 不改 lights → 但 compact 結束後沒有 PostCompact handler（events.go 中 `PostCompact` 是 `HookHandlingIgnored` non-installable）→ 若 compact 在 running 狀態觸發，**理論上**可能未在 compact 期間正確顯示處理中（需運行驗證）

#### 4.1.5 cc probe 缺口候選

- **W6-1**（與 W5-1 配對）：permission 批准後無 hook，需 ad-hoc probe 觀察 cc TUI spinner 出現/消失，補位 waiting → running 的轉換
- **W6-2**（與 W5-3 配對）：compact 結束無 hook，需 ad-hoc probe 觀察 compact UI 退場後 cc 是否回到 idle 或繼續 running

### 4.2 codex

**Catalog 來源**：`internal/agent/codex/events.go`：11 entries（9 installable + 2 Unsupported = PreToolUse / PostToolUse）  
**DeriveStatus 邏輯**：`internal/agent/codex/status.go:9-76`（mirror cc Notification 邏輯 + Stop/SubagentX detail-only）  
**Hook installer**：`internal/agent/codex/hooks.go:108-128, 209-298`（installCodexHooks 寫入 `~/.codex/hooks.json` matcher-group 結構 + `~/.codex/config.toml` 啟用 `features.codex_hooks=true`，每筆 `pdx hook --agent codex <event>`）  
**Proxy 機制**：⚠️ **無**（codex 沒有 statusline-like proxy；hooks 是唯一 status 入口）

#### 4.2.1 FutureOnly 重要性

codex catalog 9 installable 中 **5 個為 FutureOnly**（`SubagentStart`、`SubagentStop`、`StopFailure`、`Notification`、`SessionEnd`），意指：catalog 已宣告、installer 已寫入 `hooks.json`、DeriveStatus 已能解析，**但目前 codex CLI 0.124.0 不主動 fire 這些事件**（cf. `internal/agent/codex/events.go:11-15` 註釋 "may not be emitted by the current codex CLI in every path"）。

| 事件 | Active / FutureOnly | 實際 fire 嗎 |
|------|---------------------|---------------|
| `SessionStart` | Active | ✓ |
| `UserPromptSubmit` | Active | ✓ |
| `SubagentStart` | FutureOnly | ✗ |
| `SubagentStop` | FutureOnly | ✗ |
| `Stop` | Active | ✓ |
| `StopFailure` | FutureOnly | ✗ |
| `Notification` | FutureOnly | ✗ |
| `PermissionRequest` | Active | ✓ |
| `SessionEnd` | FutureOnly | ✗ |

**結論**：今日 codex 實質只 fire 4 條 hook（SessionStart / UserPromptSubmit / Stop / PermissionRequest）。

#### 4.2.2 5 status 對齊矩陣

| Status | 主路徑 (catalog event) | Subagent | Proxy | Error | SessionEnd | Catalog miss | bug 標記 | probe 候選 |
|--------|-----------------------|----------|-------|-------|-----------|--------------|----------|-----------|
| `running` | ✓ `UserPromptSubmit` | ✗ (FutureOnly) | ✗ (無 proxy) | ✗ | ✗ | — | — | — |
| `waiting` | ✓ `PermissionRequest` | ✗ | ✗ | ✗ | ✗ | — | — | — |
| `idle` | ⚠ multi：`Stop` / `SessionStart`(無 compact 子型) | ✗ (FutureOnly) | ✗ | ✗ | ✗ | known-but-unmappable: `notification_unknown_type`（FutureOnly 觸發機率低）| — | — |
| `error` | 🚨 `StopFailure` (FutureOnly = ✗ 今日不發) | ✗ | ✗ | ✗ 主源缺失 | ✗ | — | 🐛 W5-4（error 物理不可達）| W6-3（error 探測 — 主缺口）|
| `clear` | 🚨 `SessionEnd` (FutureOnly = ✗ 今日不發) | ✗ | ✗ | ✗ | ✗ 主源缺失 | — | 🐛 W5-5（clear 物理不可達）| W6-4（session 結束探測 — 主缺口）|

判讀規則：

- **🚨 主源缺失**：catalog 宣告但 CLI 不 fire = lights 物理上達不到該 status；今日唯一退場路徑為 daemon 端外部清理（process 觀察 / WS 斷線）

#### 4.2.3 事件層詳述

1. **`SessionStart`** → `idle`
   - DeriveStatus `codex/status.go:14-15`：infallible idle（**無 cc 的 compact subtype 處理 — codex 不發 compact**）
   - Handler 同 cc 路徑（清 subagents）
2. **`UserPromptSubmit`** → `running`
   - DeriveStatus `codex/status.go:17-18`：infallible running
3. **`Stop`** → `idle`
   - DeriveStatus `codex/status.go:51-52`：infallible idle
   - Error guard 白名單（`handler.go:188`）：error 狀態下可清回 idle（`req.AgentType != "opencode"` → codex 適用）
4. **`PermissionRequest`** → `waiting`
   - DeriveStatus `codex/status.go:42-49`：infallible waiting
5. **`SubagentStart` / `SubagentStop`** (FutureOnly)
   - DeriveStatus `codex/status.go:67-72`：detail-only（與 cc 對齊）
   - 今日不 fire，無實際路徑
6. **`StopFailure`** (FutureOnly)
   - DeriveStatus `codex/status.go:54-62`：infallible error
   - 今日不 fire；error status 無觸發點
7. **`Notification`** (FutureOnly)
   - DeriveStatus `codex/status.go:20-40`：4 子型同 cc + reason fallback
   - 今日不 fire
8. **`SessionEnd`** (FutureOnly)
   - DeriveStatus `codex/status.go:64-65`：infallible clear
   - 今日不 fire；clear status 無觸發點

#### 4.2.4 codex 已知 bug 候選

- **W5-4**：codex 任何 error 情境（API 失敗、CLI crash、tool 執行 error）目前**完全沒有 lights error 信號** — `StopFailure` FutureOnly 不 fire。使用者只能從 codex TUI 看到 error，lights 永遠停在 running 或 waiting
- **W5-5**：codex session 結束（使用者關閉 CLI、或 sandbox 終止）**完全沒有 lights clear 信號** — `SessionEnd` FutureOnly 不 fire。lights 永遠停在最後一個 active status

#### 4.2.5 codex probe 缺口候選

- **W6-3**（與 W5-4 配對）：error 探測 — codex CLI exit code / stderr 樣式偵測 / TUI error 對話框偵測，補位 error status；**主缺口，P1 優先**
- **W6-4**（與 W5-5 配對）：session 結束探測 — process 結束 / tmux pane 退場 / TUI 關閉偵測，補位 clear status；**主缺口，P1 優先**
- 注意：codex `Interrupt` / `Exit` 操作目前無對應 readiness check 路徑（cf. cc operator 用 `prober.CheckReadiness("cc", ...)`），這是另一缺口（W6-3 / W6-4 修完可順帶 readiness 整合）

### 4.3 opencode

**Catalog 來源**：`internal/agent/opencode/events.go`：65 entries（8 installable + 20 Unsupported + 37 Ignored；只 8 installable 在 audit 範圍）  
**DeriveStatus 邏輯**：`internal/agent/opencode/status.go:9-31`  
**Hook installer**：`internal/agent/opencode/hooks.go:33-39, 152-171`（writeManagedPlugin 寫入 `~/.config/opencode/plugins/pdx-agent-hooks.js`，**單檔 all-or-nothing managed template**，`renderManagedPlugin` byte-exact 比對驗證）  
**Plugin template**：`internal/agent/opencode/plugin_template.go:24-132`（JS plugin 訂閱 opencode Bus event 與 strong hook 後，用 `Bun.spawn` 呼叫 `pdx hook --agent opencode <Name>` 投遞）  
**Proxy 機制**：plugin 內 runtime filter（Decision 3 — `session.status` filter `type === 'idle'`，其他 type 受 Decision 4 defer 為 receive-but-no-op）

⚠️ **結構差異**：opencode catalog 不含 `Notification`（cc / codex 都有）。所有 polymorphic waiting 子型由 plugin 端兩個 Bus event 映射為同一個 `PermissionRequest`（permission.asked → request_type=permission；question.asked → request_type=question）。`idle_prompt` / `auth_success` 路徑不存在 — opencode `idle` 僅可由 `Stop` / `SessionStart` 達到。

#### 4.3.1 Plugin 端事件映射表（plugin_template.go:48-129）

| upstream event | type filter | plugin emit | DeriveStatus → status |
|----------------|-------------|-------------|------------------------|
| `session.created` | — | `SessionStart` | `idle` |
| `session.error` | — | `StopFailure` + add to `suppressIdleForSession` Set | `error` |
| `session.status` | type==='idle' | `Stop`（`suppress` Set 命中時 skip）| `idle` |
| `session.status` | type==='busy'/'retry' | **receive-but-no-op**（Decision 4 defer）| ✗ |
| `session.deleted` | — | `SessionEnd` | `clear` |
| `permission.asked` | — | `PermissionRequest`(request_type=permission) | `waiting` |
| `question.asked` | — | `PermissionRequest`(request_type=question) | `waiting` |
| `chat.message`(strong hook) | — | `UserPromptSubmit` + clear `suppress` Set for sessionID | `running` |
| `tool.execute.before` | tool==='task' | `SubagentStart` | "" (detail-only) |
| `tool.execute.after` | tool==='task' | `SubagentStop` | "" (detail-only) |

**suppressIdleForSession 機制**（plugin_template.go:28, 68, 76-79, 92）：

- `session.error` fire 時把 sessionID 加入 Set
- 下一個 `session.status` idle 進來若 sessionID 在 Set，**skip emit Stop**（保留 error 狀態）
- `chat.message`（新 prompt cycle）開始時 delete sessionID（重設 — 否則下個正常 idle 會被 stale entry 吃掉）
- 注意：JS plugin 的 in-process Set；plugin 重載 / opencode 重啟 → Set 清空 → race 中可能漏 suppress

#### 4.3.2 5 status 對齊矩陣

| Status | 主路徑 (catalog event) | Subagent | Proxy / Filter | Error | SessionEnd | Catalog miss | bug 標記 | probe 候選 |
|--------|-----------------------|----------|----------------|-------|-----------|--------------|----------|-----------|
| `running` | ✓ `UserPromptSubmit` | ✗ | ⚠ Decision 4 defer：busy/retry 變體無映射 | ✗ | ✗ | — | 🐛 W5-6（busy/retry 期間狀態停滯）| W6-5（busy/retry 路徑映射；可能僅 events.go 補 entry，無需 probe） |
| `waiting` | ✓ `PermissionRequest`(request_type=permission OR question) | ✗ | ✓ plugin filter（permission.asked / question.asked → 同 catalog event） | ✗ | ✗ | — | — | — |
| `idle` | ⚠ multi：`Stop`(via session.status filter) / `SessionStart` | ✗ | ✓ plugin filter（session.status type==='idle' → Stop；suppressIdle 過濾）| ✗ | ✗ | — | 🐛 W5-7（suppressIdle race：plugin 重啟後可能漏 suppress）| W6-6（plugin 重啟後 session 狀態 reconcile） |
| `error` | ✓ `StopFailure` | ✗ | ✓ plugin filter（session.error → StopFailure + suppress armed）| ✓ 主源 | ✗ | — | 🐛 W5-8（error guard 對 opencode 不允許 Stop 清 — 若 plugin suppress 失效則卡死）| W6-6 同上 |
| `clear` | ✓ `SessionEnd` | ✗ | ✓ plugin filter（session.deleted）| ✗ | ✓ 主源 | — | — | — |

#### 4.3.3 事件層詳述（installable only）

1. **`SessionStart`** → `idle`
   - DeriveStatus `opencode/status.go:14-15`：infallible idle
   - plugin 來源：session.created Bus event
2. **`UserPromptSubmit`** → `running`
   - DeriveStatus `opencode/status.go:16-17`：infallible running，附 model
   - plugin 來源：chat.message strong hook（同時 clear suppressIdleForSession entry）
3. **`SubagentStart` / `SubagentStop`** → status=""
   - DeriveStatus `opencode/status.go:18-19`：detail-only with `agent_id` / `agent_type` / `description` / `prompt` / `title` / `output`
   - plugin 來源：tool.execute.before/after with input.tool==='task'
4. **`PermissionRequest`** → `waiting`
   - DeriveStatus `opencode/status.go:20-21`：infallible waiting，含 request_type 區分 permission / question
   - plugin 來源：permission.asked OR question.asked
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

#### 4.3.4 opencode 已知 bug 候選

- **W5-6**：opencode session.status type 為 'busy' / 'retry' 時 plugin receive-but-no-op（Decision 4 defer），lights 不更新。若 running → 內部 retry → 結束 idle，期間 lights 顯示停滯（沒有「正在重試」中介狀態）
- **W5-7**：opencode plugin 重啟後 `suppressIdleForSession` Set 清空 → 若 session.error 後 plugin 重啟，下個 session.status idle 不被 suppress → fire Stop → 但 daemon 端 error guard 阻擋（handler.go:187-189 對 opencode Stop 不放行）→ trace 顯示 `error_guard_blocked`，但**狀態仍是 error 卡住**直到下個 UserPromptSubmit / SessionStart / SessionEnd
- **W5-8**：在 W5-7 情境下，若使用者**沒有**送新 prompt 也沒結束 session，lights 永久停在 error；唯一退場路徑是 SessionEnd（使用者主動關 session）

#### 4.3.5 opencode probe 缺口候選

- **W6-5**（與 W5-6 配對）：busy/retry 變體映射 — **可能不需 probe**，純 catalog/plugin 補 mapping 即可（issue #661 已追蹤）；若決議走 probe（觀察 TUI spinner / 進度條），優先序 P2
- **W6-6**（與 W5-7 / W5-8 配對）：plugin 重啟後 session 狀態 reconcile — daemon 端在 plugin reconnect 時對所有 active session 查詢 opencode 內部狀態並補 sync；可能不純 probe（更像 RPC）；優先序 P2

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

- 雙路徑 waiting（W5-2）— catalog 同時宣告 `PermissionRequest` 與 `Notification(permission_prompt)`，runtime 由 cc CLI 決定先後 / 是否兩條都送
- compact 退場無 hook（W5-3）— `SessionStart(compact)` 走 `compact_ignored` 不改 lights，但 compact 結束沒 PostCompact 處理（events.go 中 `PostCompact` 為 `HookHandlingIgnored`），也沒 SessionStart non-compact

**codex**：

- 5/9 installable 為 FutureOnly（SubagentStart/Stop / StopFailure / Notification / SessionEnd）— **error 與 clear 兩 status 物理上不可達**（W5-4 / W5-5）；今日 codex CLI 0.124.0 只 fire 4 事件
- 無 Notification → polymorphic waiting / 多源 idle 路徑全缺（FutureOnly 標識的待補項）

**opencode**：

- Plugin 端 in-process state（`suppressIdleForSession` Set）有 race 風險（W5-7）— plugin 重啟 / opencode 重啟即遺失
- error guard 對 opencode Stop 特殊處理（`handler.go:187-189`）— 跟 plugin suppress 互鎖；plugin suppress 失效時 daemon 端阻擋會卡死（W5-8）
- Decision 4 defer：busy/retry 變體 receive-but-no-op（W5-6）— 已知 follow-up issue #661

### 5.4 跨家觀察優先序

依 fix-spec §0 「probe 不是 always-on，是缺口導向」原則，三家 probe 缺口輕重排序：

1. **codex error / clear（W6-3 / W6-4）** — P1 主缺口，今日完全不可達
2. **opencode error 卡死 / busy retry（W6-5 / W6-6）** — P2 邊緣但實機可重現
3. **cc permission/compact 退場（W6-1 / W6-2）** — P2 體感優化

---

## 6. W5 燈號 Bug 工作池

| ID | agent | 影響 status | 觸發條件 | 期望 | 實際 | 修復複雜度 (S/M/L) | 配對 W6 |
|----|-------|-------------|---------|------|------|--------------------|---------|
| **W5-1** | cc | running | Notification(permission_prompt) → 使用者於 cc TUI 點批准 → cc 繼續處理（無 hook fire 直到 Stop）| 批准後 lights 顯示 running | lights 仍顯 waiting 直到 Stop | L（依賴 probe）| W6-1 |
| **W5-2** | cc | waiting | cc 同時 fire `PermissionRequest` 與 `Notification(permission_prompt)` 兩條 hook | 單次 lights 變化 | trace 看到兩筆 broadcast，SPA 雙重事件 | M（handler dedup OR catalog 取捨主從） | — |
| **W5-3** | cc | idle | SessionStart source=compact 觸發 compact_ignored，compact 結束無 PostCompact handler | compact 結束後 lights 適切轉換到 idle / running | lights 停在 compact 前的最後狀態 | L（依賴 probe + 可能改 catalog 把 PostCompact / PreCompact 從 ignored 移為 handled） | W6-2 |
| **W5-4** | codex | error | codex CLI 任何 error（API failure / tool error / crash）| lights 顯紅 | **永不顯紅** — `StopFailure` FutureOnly = 0.124.0 不 fire | L（依賴 probe；catalog 已備但 CLI 不發） | W6-3 |
| **W5-5** | codex | clear | codex session 結束（CLI close / sandbox 終止）| lights 清空 | **永不清空** — `SessionEnd` FutureOnly = 0.124.0 不 fire | L（依賴 probe；catalog 已備但 CLI 不發） | W6-4 |
| **W5-6** | opencode | running（中介）| session.status type='busy' 或 'retry' | lights 反映重試 / 中介狀態（或保持 running） | plugin receive-but-no-op，狀態停滯 | S（plugin 加 mapping；可能 events.go 補 entry 即可，無需 probe；issue #661 已追蹤）| W6-5 |
| **W5-7** | opencode | error / idle 互鎖 | session.error 後 plugin 重啟 → suppressIdleForSession Set 丟失 → 下個 session.status idle 不被 suppress → fire Stop | 保持 error 狀態（待 user 動作）| daemon error guard `handler.go:187-188` 阻擋 Stop（trace=`error_guard_blocked`），但若使用者不送新 prompt，error **卡死直到 SessionEnd** | M（plugin 補狀態 reconciliation；OR 改 daemon 端 error guard 對 opencode 加退場路徑）| W6-6 |
| **W5-8** | opencode | error 卡死 | W5-7 後 / plugin restart 後 / 使用者不送新 prompt 也不結束 session | 提供退場路徑 | 永久卡 error；唯一退場是手動 SessionEnd | M（同 W5-7 修復路徑） | W6-6 |

**複雜度判準**：

- **S**：改 1–2 行 + 1 個 unit test，無新概念，無 probe
- **M**：跨 file / 改 catalog / 加新 unit test，可能 plugin 端改動
- **L**：依賴新 probe / DeriveStatus 邏輯結構性改動 / 需 W6 ad-hoc probe 先 ship

---

## 7. W6 Probe 缺口工作池

| ID | agent | 缺口 status | 缺口描述 | 補位構想（不細到 ProbeIntent 介面） | 優先序 (P1/P2/P3) | 配對 W5 |
|----|-------|-------------|---------|-------------------------------------|-------------------|---------|
| **W6-1** | cc | running | permission 批准後無 hook，spinner 期 lights 卡 waiting | tmux pane 觀察 cc TUI spinner 字元出現/消失 → 推 ProbeIntent("waiting→running")；批准 / spinner 消失即 fire 合成 UserPromptSubmit-equivalent（避免 dual emit） | P2 | W5-1 |
| **W6-2** | cc | idle | compact 結束無 hook（PostCompact ignored）| 觀察 cc TUI compact 對話框退場 / 回到主 prompt → 推 idle；OR 改 catalog 把 PostCompact 從 ignored 移為 status emitting | P2 | W5-3 |
| **W6-3** | codex | error | codex CLI 任何 error 完全沒 lights 信號 | exit code / stderr 樣式偵測 / TUI error 對話框偵測 → 推 ProbeIntent("any→error")；可結合 process 結束碼 與 TUI 文字觀察 | **P1**（主缺口）| W5-4 |
| **W6-4** | codex | clear | codex session 結束完全沒 lights 信號 | process 結束 / tmux pane 退場 / TUI 關閉偵測 → 推 ProbeIntent("any→clear")；最簡實作觀察 codex CLI 進程退出 | **P1**（主缺口）| W5-5 |
| **W6-5** | opencode | running 中介 | busy/retry 變體無映射 | **可能不需 probe** — plugin 端補 mapping 即可（issue #661 已追蹤）；若決議走 probe，觀察 opencode TUI spinner / retry 提示文字 | P3 | W5-6 |
| **W6-6** | opencode | error/idle reconcile | plugin 重啟後 in-process state 丟失 | daemon 端在 plugin reconnect 時對所有 active session 查詢 opencode 狀態並補 sync；**更像 RPC 而非 probe**；若走 probe，觀察 tmux pane 內 opencode TUI 當前指示器 | P2 | W5-7 / W5-8 |

**優先序判準**：

- **P1**：影響核心使用情境（status 物理上不可達、使用者立即可感）
- **P2**：邊緣場景（特殊 agent 行為、罕見路徑、體感優化）
- **P3**：nice-to-have（觀察用、可能 plugin 改動取代 probe）

### 7.1 設計約束（per fix-spec §0 + §1）

- ❌ **不**做 always-on probe — 每個 W6 條目都 ad-hoc，gating 條件由各 agent 自己宣告
- ❌ **不**抽 generic framework — ProbeIntent interface lazy 設計，**等實作第一個（建議 W6-3 codex error，最簡單可單純 process 退出觀察）才 finalize interface shape**
- ✅ 利用 PR-4a-1 ship 的 shared utilities（`probe.Watch` / `WatchOptions` / `tmux.CapturePaneTopLines` / `LooksLikeShellPrompt` / orchestrator graceWindow / Error guard / stale-callback / transition gate / recordHookAt）
- ✅ 每個 ProbeIntent 寫在 agent module 內（`internal/agent/{cc,codex,opencode}/probe_intent_*.go`），不集中於 `internal/module/agent`
- ✅ ProbeIntent gating 不是布林開關，而是「滿足某些條件才 watch」 — 例：W6-3 在 status==running OR waiting 時 watch process exit；status==idle 時 unwatch
- ⚠️ 警覺 `feedback_skeleton_convergence` 五大 bloat 徵兆（把 working code 變 data / parallel registry / 統一抽象 / refactor working code / config flag）— 任何 W6 PR 設計時 self-check

### 7.2 W6 PR 推薦實作順序

1. **W6-3 codex error**（最簡 — 純 process 觀察）→ 第一個 ProbeIntent，藉此 finalize interface shape
2. **W6-4 codex clear**（同 W6-3 機制延伸）— 沿用 W6-3 確立的 interface
3. **W6-5 opencode busy/retry**（決議 plugin 補 vs probe；後者沿用 interface）
4. **W6-6 opencode reconcile**（特殊 — 可能走 RPC 路徑，再評估是否套 ProbeIntent）
5. **W6-1 cc permission spinner**（最複雜 — TUI 樣式觀察）
6. **W6-2 cc compact**（同 W6-1 機制延伸）

---

## 8. 結束條件

當下列全達成時 W1 視為完成：

1. ✅ §4 三家 × 5 status 矩陣全填
2. ✅ §5 跨家比對列出至少共通 / 各家獨有兩類條目
3. ✅ §6 W5 工作池列出 audit 中發現的 bug（也可能 0 條 — 若無，§5 須提出解釋）
4. ✅ §7 W6 工作池列至少 1 條（per fix-spec §0 — fix-spec 存在的前提就是有缺口）
5. ✅ doc 過 codex review 兩輪（per CLAUDE.md PR Review 兩輪制）

---

## 9. 後續 Hand-off

- **W2** 不依賴本 audit 結果（catalog naming 是 input/output 邊界整理，跟 audit 結果正交）
- **W3** 撤回 framework 時，per-agent gating 的初始 disable list 直接列為三家 — 因為 W6 工作池項目都還沒 ship
- **W4** dev log 補完路徑優先序：先補 W6 缺口涉及的路徑，再補 §5 跨家比對發現不一致的路徑
- **W5 / W6** PR 拆分以本 doc §6 / §7 工作池為準；S 複雜度可 batch 入單 PR，M / L 各自獨立 PR
- **W7** Inspector UI 的 Coverage Matrix 視覺化結構直接照本 doc §4 矩陣 schema；endpoint payload 照 §3 路徑分類

---

## 10. 文獻

- 上層 spec：`docs/specs/2026-04-23-lights-rebuild-spec.md`
- Fix-spec：`docs/specs/2026-04-28-lights-rebuild-fix-spec.md`
- 5 status 定義：`internal/agent/status.go:6-11`
- catalog：`internal/agent/cc/events.go` / `internal/agent/codex/events.go` / `internal/agent/opencode/events.go`
- DeriveStatus：`internal/agent/cc/status.go` / `internal/agent/codex/status.go` / `internal/agent/opencode/status.go`
- statusline (cc proxy)：`internal/agent/cc/statusline.go`
- plugin template (opencode proxy / runtime filter)：`internal/agent/opencode/plugin_template.go`

---

## 11. Audit Methodology Notes

執行 §4 矩陣時的判讀規則（避免落差）：

- **EmitsStatus 多元 entry**（如 cc Notification → `[Waiting, Idle]`）：拆 polymorphic 子型獨立列入對應 status row
- **FutureOnly entry**（如 codex 6 個 FutureOnly）：`✓ (FutureOnly)` 標記，並評估「目前運行 hook 是否實際發送」— 若 CLI 不發 = 缺口候選
- **Handling=Unsupported / Ignored**：不入 audit 範圍，但 events.go 描述若揭示 fall-through 行為（如 cc PostToolUseFailure），記入 §3.6 catalog miss 路徑
- **proxy 路徑**：審 statusline.go / plugin_template.go 的合成事件來源；列出每條 proxy 投遞的 catalog event name + 觸發條件
- **catalog miss reason 列表**：盤點所有 `Reason != ""` 字串，作為 §3.4 / §3.6 完整列舉
