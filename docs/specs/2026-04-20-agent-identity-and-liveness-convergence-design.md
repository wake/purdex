# Agent Identity / Liveness 收斂設計 Spec

> 日期：2026-04-20
> 狀態：Ready for Implementation (v2 — addresses review findings)
> 關聯：issue #487、PR #484、PR #486、branch `feat/agent-watch-alive`
> 參考：
> - `docs/superpowers/specs/2026-04-13-probe-chain-design.md`（既有 Probe Chain 架構）
> - Codex Hooks 官方文件 (https://developers.openai.com/codex/hooks)
> - Codex Issue #16732、PR #15118

---

## 1. 概述

Agent tab icon 的翻轉、hook 事件、liveness probe、periodic clear 四條鍊目前沒有共同的身份模型，彼此互相干擾。這導致：

1. Probe 被迫用字串比對（`pane_current_command`、`comm`、畫面內容）來猜身份，在 CC 把 argv[0] 改成版本號（`2.1.114`）或 node 套殼時失效
2. Daemon 無條件信任 hook payload 的 `tmux_session` 欄位（來自 ambient env），detached shared runtime 會冒名寫入錯誤的 session row
3. `agent_events` 以 `tmux_session` 為單一主鍵，多個 agent 的事件在同一 session 互相覆蓋
4. 多個 hook 缺口（CC Notification 後無回應 hook、Codex 無 Notification、Codex Ctrl+C 後無 Stop）讓 probe 必須補狀態，結果把身份判定也一起扯進來

本 spec 把四條鍊收斂成一致的模型：**Hook 為身份權威，Probe 為 liveness/activity 補強，Frame Store 保留多 agent 共存，驗證流擋掉 detached claim**。

---

## 2. 問題定義

### 2.1 已確認的 Bug

#### B1. Wrapped descendants 誤判 dead（已由 PR #484 修掉，保留）

行程樹 `zsh → node → codex`。舊版 probe 只看 foreground + 直接 child，codex 在孫層被判 dead。#484 加了 recursive descendant scan + bounded cache 解決。

#### B2. Detached Codex runtime 冒名 CC session（issue #487 原 bug）

1. 使用者在 `purdex-enhance` tmux session 跑 CC
2. 從 CC 觸發 Codex shared runtime（`codex-companion.mjs review`）
3. Runtime 已 detach（PPID=1），但繼承啟動當下的 `TMUX` env
4. Codex hook POST 帶 `tmux_session=purdex-enhance` + `agent_type=codex`
5. Daemon 直接信任 → 覆蓋 CC 的 row → tab icon 翻轉

#### B3. Periodic sweep 放大錯誤身份（PR #486 not-ship 的主因）

`feat/agent-watch-alive` 增加每 2 秒的 `checkAliveAll()`，但在身份未驗證前，錯誤冒名的 row 會被週期性 re-clear，把偶發翻轉變成常態。配合 4 路 Codex review 指出的 stale snapshot race、rename race、tmux 失聯大屠殺、Stop 不 join 等問題，這個 PR 在身份修正前不能 merge。

### 2.2 Status Gap 盤點（Codex/CC Hook 能力差異）

| Gap | Agent | 何時出現 | 現況影響 |
|-----|:-----:|----------|----------|
| Notification 後使用者回應無 hook | CC | 使用者按 yes/no 給權限、回答問題後 | 黃燈卡住直到下一個 PreToolUse/Stop |
| 完全沒 Notification hook | Codex | Codex 跳 approval prompt 要求許可 | 無任何 hook 信號，燈維持 running |
| PreToolUse/PostToolUse 只支援 Bash | Codex | Codex 用 apply_patch / edit 等非 Bash 工具（[Issue #16732](https://github.com/openai/codex/issues/16732)）| 期間狀態不變化 |
| Ctrl+C 中斷後無 Stop | Codex | 使用者 Ctrl+C 打斷回應 | 燈卡在 idle / running 回不來 |
| Agent crash（kill -9 / OOM / segfault）| 兩家 | 程序死但沒機會發 Stop/SessionEnd | 狀態永遠停在最後一個 |

**重要釐清**：Codex 的 `Stop` hook **是 per-turn 不是 per-session**（[PR #15118](https://github.com/openai/codex/pull/15118) 實證）。原先懷疑 Codex 缺 turn-end hook 是誤記。真正的 Codex gap 是上表的五項。

### 2.3 根本原因

四個結構問題疊加：

1. **Hook payload 的 `tmux_session` 來自 ambient env**，daemon 未驗證真實來源
2. **`agent_events` 以 `tmux_session` 為唯一主鍵**，所有 agent 共享同一列互相覆蓋
3. **Probe 被當身份來源**，但它只能看 pane 內 process tree，無法驗 detached runtime 的真實歸屬
4. **Probe 的身份比對依賴 `pane_current_command` / `comm`**，這兩者會被 argv[0] 改寫（CC 顯示 `2.1.114`）和 node 套殼（顯示 `node`）影響

---

## 3. 目標 / 非目標

### 3.1 目標

1. 修正 issue #487：detached Codex runtime 不得再覆蓋活著的 CC session
2. 保留 PR #484：pane 內 wrapped descendants 仍能被正確視為 alive
3. 讓 periodic sweep 安全出貨，取代 PR #486 的暫存版本
4. 將身份、liveness、activity、session projection 分層
5. 補 status gap 時不再污染身份層

### 3.2 非目標

1. **不重寫既有狀態模型**（`StatusRunning` / `StatusWaiting` / `StatusIdle` / `StatusError` / `StatusClear` 等保留）
2. **不新增 UI 狀態種類**（沒有「unknown」此種視覺狀態；識別不了 → 落回 pane 原 icon）
3. **不依賴 Codex upstream 先修**（Codex 缺 Notification、ApplyPatch 不發 hook 等都是 upstream 問題，本輪以 probe 補強而非等 upstream）
4. **不做學習模式**（daemon 不從 hook 動態學習 binary mapping；只支援明確實作 provider 的 agent）
5. **不考慮舊 hook payload 相容**（alpha 階段，直接切新 schema）
6. **不加 daemon-boot discovery**（第一次裝 pdx 或全新 pane 在第一個 hook 前顯示 pane icon，此為可接受行為非 regression；既有 session 有 DB frame 會正常 replay）

---

## 4. 設計原則

1. **Hook 對 `agent_type` 有權威性，對 `pane / session claim` 沒有**
   - Agent 自報 `cc` / `codex` 可接受
   - 「我屬於哪個 tmux pane / session」必須由 daemon 驗證

2. **Probe 不糾正 hook 的身份**
   - Probe 只回答系統級問題：PID 活嗎、畫面動嗎、PID 在哪個 pane 的樹內
   - Probe 不判定「這筆 hook 真不真」——那是 verify 流的事

3. **現有狀態模型不動**
   - 本輪是 workaround + 結構修正，不重寫狀態機
   - Activity 三規則是將畫面觀察映射到既有 status，不新增類型

4. **硬編碼關在 provider 內**
   - Daemon code 不含 `"cc"` / `"codex"` 字串常數
   - 每個 provider 自己知道如何識別自己（exe / argv / 必要時 content pattern）

5. **Frame 先於單列**
   - Store 從「一個 tmux_session 一列」改為「一個 pane 多 frame」
   - 支援 CC 呼叫 Codex 等 nested 情境，主從關係明確

6. **識別不了不硬顯示**
   - 沒 verified frame → 不渲染 agent icon → 落回 pane icon（terminal）
   - 不出現 `unknown` 標籤

---

## 5. 架構模型

### 5.1 Hook Payload Schema v2

新 schema（alpha 階段直接切，不考慮舊相容）：

```json
{
  "agent_type":    "cc",
  "event_name":    "UserPromptSubmit",
  "raw_event":     {...},

  "tmux_session":      "purdex-enhance",
  "tmux_pane_id":      "%5",
  "sender_pid":        36649,
  "sender_start_time": "Sun Apr 20 01:30:00 2026"
}
```

#### 欄位語意

| 欄位 | 意義 | 來源 |
|------|------|------|
| `agent_type` | Agent 自報類型（`cc` / `codex`）| Agent 給 |
| `event_name` | Hook 事件名稱 | Hook runtime 給 |
| `raw_event` | Hook 原始 payload | Hook runtime 給 |
| `tmux_session` | Claim：這筆事件屬於哪個 tmux session | pdx hook 從 env 組 |
| `tmux_pane_id` | Claim：這筆事件屬於哪個 tmux pane | pdx hook 從 `$TMUX_PANE` 取 |
| **`sender_pid`** | **Agent 本身的 PID**（不是 pdx hook 自己的 PID）| pdx hook 從祖先鏈解析出 agent |
| `sender_start_time` | Agent 程序的 start time，供 PID reuse 驗證 | pdx hook 從 `ps -p <pid> -o lstart=` 取 |

#### pdx hook 如何解析 sender_pid

**關鍵**：`pdx hook` 是 agent 的子程序（或孫程序，經 shim 包過）。若把 pdx hook 自己的 PID 當 `sender_pid`，verify 的 Q3（PID 是否在 pane descendant）永遠會過（pdx 本來就是 pane 的 descendant）→ B2 沒修。

**解析規則**：pdx hook 從 `os.Getppid()` 開始向上走祖先鏈，跳過已知 shim 層，找到第一個非 shim 的程序作為 agent PID：

```
pdx hook → 向上 walk PPID chain
  跳過的 shim 層（basename 命中即繼續向上）:
    - sh / bash / zsh / dash / fish  （hook 可能由 shell 命令啟動）
    - sh -c / bash -c                （若 ExePath basename 為 shell 且 Argv 第二個是 -c）
    - npx / yarn / pnpm              （node 包管理器 wrapper）
    - env                             （如 `env PATH=... pdx hook`）
  停止條件（找到非 shim 即為 agent PID）:
    - basename 不在 shim list
    - 到 PPID=1（拿 PPID=1 回報，標記為可疑）
```

若走到 PPID=1 都沒找到非 shim（代表 pdx 以 detached 狀態被啟動，罕見），回報 PPID=1 並在 payload 加 `"sender_uncertain": true` 欄位；daemon 接到後視為 unverifiable 直接拒絕。

此解析邏輯實作在 `cmd/pdx/hook_pid_resolver.go` 內。單元測試需覆蓋常見 shim 類型與多層 shim 組合。

### 5.2 ProcessInfo

```go
package agent

type ProcessInfo struct {
    PID         int
    PPID        int
    ExePath     string    // 已 resolve symlinks 的絕對路徑
    Argv        []string
    StartTime   time.Time // 對應 sender_start_time
}
```

#### 平台分支

| 平台 | ExePath 取得 | Argv 取得 | StartTime 取得 |
|------|------------|-----------|----------------|
| macOS | `ps -p PID -o comm=`（再做 path normalize / symlink resolve） | `ps -p PID -o args=` 分割 | `ps -p PID -o lstart=` |
| Linux | `readlink /proc/PID/exe` | `/proc/PID/cmdline`（null-separated）| `ps -p PID -o lstart=` 或 `/proc/PID/stat` 第 22 欄 |

**備註**：macOS 目前實作改用 `ps -p PID -o comm=` 取得 executable，再配合 `args=` 組 argv，避免直接信任 `argv[0]` 造成 Identify 誤判。

**Symlink 處理**：`ExePath` 取得後必須跑 `filepath.EvalSymlinks` 再 `filepath.Base`，避免 symlink wrapper（如 `~/.local/bin/claude → /opt/homebrew/.../claude`）造成 basename 不穩。

### 5.3 Frame Model

取代「一個 tmux_session 一列」的現有表。

```go
type Frame struct {
    FrameID          string     // uuid
    PaneID           string     // tmux pane id (e.g. "%5")
    AgentType        string
    PID              int
    PPID             int
    ProcessStartTime time.Time  // 解 PID reuse，與 sender_start_time 對應
    ParentFrameID    string     // 若 PPID 在另一 frame 的 PID descendants 中 → 主從
    Subagents        []string   // CC subagent ids；持久化時存成 JSON text
    Status           AgentStatus
    StartedAt        time.Time
    LastSeenAt       time.Time  // 最後一次 verified hook 時間
    Verified         bool
}
```

#### 主鍵與持久化

- **資料列主鍵**：`FrameID`
- **邏輯唯一鍵 / UNIQUE INDEX**：`(PaneID, PID, ProcessStartTime)`（PID reuse 保險）
- **索引**：`(PaneID)`、`(AgentType)`
- **持久化到 SQLite**（與現有 `agent_events` 同一 DB `agent_events.db`），table 名稱 `agent_frames`
- Daemon 啟動時 `replayFromDB()` 讀回所有 `Verified = true` 的 frame；對每個 frame 用 `ProcessStartTime` 驗證 PID 未被重用（若重用 → 丟棄該 frame）

#### Orphan 政策（parent frame 死掉時）

當 parent frame 的 PID `kill -0` 失敗被清除時：

- **該 frame 的所有 child frame（`ParentFrameID` 指向它者）的 `ParentFrameID` 設為 NULL**（成為 orphan top frame）
- **Orphan child frame 本身不清**（child PID 仍可能存活）
- 若後續 child frame 自己 PID 也死，正常走 per-frame sweep 清除

### 5.4 Session Projection

UI 不直接讀 frames，讀 daemon projection：

```go
type SessionProjection struct {
    PaneID       string
    PrimaryFrame *Frame  // stack bottom（第一個進場，或最久的 verified frame）
    TopFrame     *Frame  // stack top（最新進場的 agent，可能是 nested）
    Subagents    []string // 取自 TopFrame.Subagents
}
```

Tab icon 預設顯示 `TopFrame.AgentType`。若未來要「主從疊加呈現」是純 UI 調整，daemon 已有完整資訊。

**SPA 接口**：WS 廣播格式**不變**，`agent_type` 欄位改由 daemon 計算（= `TopFrame.AgentType`）。SPA 端 `agentTypes[ck]` 讀取邏輯不動。

### 5.5 Verify Flow

Daemon 收到 hook 時 **同步** 執行（verify 操作都很便宜：`kill -0`、`tmux display`、`ps`，總成本 <50ms，不會拖慢 hook POST 的 2s client timeout）：

```
1. 解 payload v2。若 schema 缺必填欄位 → 400 + log reason="schema_invalid"

2. kill -0 sender_pid
   失敗 → 202 rejected, reason="pid_dead"

3. 驗 sender_start_time 一致
   取 ps -p sender_pid -o lstart=，與 payload 比對
   不一致 → 202 rejected, reason="pid_reused"

4. 解 tmux_pane_id 的 pane_pid
   tmux display -p -t "$tmux_pane_id" '#{pane_pid}'
   失敗（tmux restart / pane 不存在）→ 202 rejected, reason="pane_unresolvable"

5. Q3: 向上 walk sender_pid 的祖先鏈，檢查 pane_pid 是否為 ancestor
   ps -ax -o pid=,ppid= 建表
   從 sender_pid 開始 parent = ppid_of(current)，直到 PPID=1
   若過程中任一步 == pane_pid → 通過
   否則 → 202 rejected, reason="pid_not_in_pane_tree"
   
   （B2 場景: detached runtime PPID=1，pane_pid 不在其 ancestor chain → 拒絕）
   （合法場景: CC 呼叫 Codex，Codex 的 ancestor 是 CC → CC 的 ancestor 是 pane_pid → 通過）

6. 取 ProcessInfo(sender_pid) 呼叫對應 provider.Identify
   payload.agent_type 對應的 provider
   Identify 回 false → 202 rejected, reason="identify_mismatch"

7. 全部通過 → 以 `(PaneID, PID, ProcessStartTime)` 做 upsert lookup；新 row 產生 `FrameID`，並標記 `Verified=true`
   計算 Session Projection，廣播 WS
```

驗證失敗的事件：
- 不覆蓋現有 projection
- 不投影到 UI
- Log rejection reason 供 debug（可配合 metrics）
- HTTP 200 / 202（非 500，非伺服器錯誤）

### 5.6 Probe 的新職責

只回答系統級問題，不判定身份真偽：

| 問題 | 實作 | 成本 |
|------|------|------|
| Q1：PID 還活嗎 | `kill -0 pid`（syscall）| 極低 |
| Q2：畫面在動嗎 | `capture-pane -e -S -5` + hash diff | 低 |
| Q3：PID 在 pane 的樹內嗎 | 向上 walk PPID chain（見 §5.5 step 5）| 低-中 |

#### Periodic Sweep 的 per-frame 行為

**重要**：sweep 以 **per-frame** 為單位清理，不是 per-pane：

```
every 2s:
  for each frame in active_frames (Verified=true):
    if not IsPidAlive(frame.PID):           # kill -0 失敗
      clear frame（從 store 刪除）
      recompute projection for frame.PaneID
      broadcast clear（若 TopFrame 變了）
    else if ps.start_time(frame.PID) != frame.ProcessStartTime:
      clear frame（PID reuse 偵測）
```

#### 已刪除的舊能力

- `RegisterProcessNames(agentType, []string{"claude"})` — 已移除
- `ContentMatcher` interface + `LooksLikeAgent` — 身份識別層刪除
- Liveness Layer 1a `pane_current_command` 比對 — 已刪除
- Liveness Layer 1d 內容 fallback 做身份判定 — 已刪除

### 5.7 Activity 三規則（補 Hook Gap）

Probe 的 activity watcher 看畫面狀態，映射到既有 status。**不做身份判定**、**不 pop frame**（pop frame 只由 §5.6 sweep 基於 `kill -0` 觸發）。

| Probe 觀察 | 觸發狀態轉換 | 解的 Gap |
|-----------|-------------|----------|
| 畫面 hash 穩定 ≥ 1.5s（3 次 500ms 取樣）| → `idle` | Codex asking（Gap 2）、其他 lazy hook |
| 畫面 hash 有變化 | → `running` | CC Notification 後使用者回應（Gap 1）|
| 畫面底部符合 shell prompt pattern **AND** frame.PID `kill -0` 失敗 | frame 進入 sweep 清除流程 | Codex Ctrl+C 回 shell（Gap 4）、session 結束無 SessionEnd |

**兩條件 AND**：單獨畫面像 shell prompt 不足以 pop frame（避免 CC/Codex 輸出的 markdown code block 或 prompt-like 文字誤觸發）。需加 PID 真的死掉才 pop。

實作要點：

- **capture-pane 必須加 `-e` flag**（含 ANSI escape，抓得到只換顏色不換字元的 spinner）
- **Shell prompt pattern** 通用規則：最後一非空行去除尾空白後，末字元是 `$` / `#` / `%` / `>`（不綁特定 shell）
- **Asking vs Idle 不區分**：UX 上都是「停了，等人動」，狀態模型保留既有 `StatusIdle`
- **ReadinessChecker 保留**：做「已知身份下的狀態細化」（例如 CC 的 `StatusWaiting` vs `StatusIdle`），與本 spec 正交

### 5.8 Hook 事件對 Frame 的影響

各 hook 事件映射到 frame 操作：

| Hook 事件 | Frame 操作 | 備註 |
|----------|-----------|------|
| `SessionStart` | **Upsert** frame（verify 通過後建立，若已存在則 LastSeenAt 更新）| CC/Codex 皆用 |
| `UserPromptSubmit` / `PreToolUse` / `PostToolUse` | **Update** frame.Status 為 `running`，LastSeenAt | 不建 / 不 pop |
| `Notification`（CC only）| **Update** frame.Status 為 `waiting` | |
| `Stop`（per turn）| **Update** frame.Status 為 `idle` | **不 pop frame**；frame 存活依賴 PID |
| `SessionEnd`（CC only）| **Pop** frame | Codex 無此 hook，靠 §5.6 sweep |
| `SubagentStart` / `SubagentStop`（CC only）| **Update** frame.Subagents[] | 不建 frame（subagent 不是獨立 frame）|

**Subagent 不映射到獨立 frame**。理由：
1. CC subagent 是 CC 內部概念，與 tmux pane / OS PID 的對應不清
2. 現有 UI 把 subagent 當 current frame 的附屬指標顯示（小圓點）
3. 保留既有 `Subagents []string` 欄位在 `SessionProjection`（見 §5.4）

---

## 6. 收斂後的 Probe Layer 對照

### 舊（現狀）

| Layer | 實作 | 問題 |
|:---:|------|------|
| 1a | `tmux '#{pane_current_command}'` | argv[0] 改寫 |
| 1b | `PaneChildCommands`（ps `comm`）| 同上 + CC 是 foreground 時漏 |
| 1c | `PaneDescendantCommands` 遞迴（#484）| 同上 + 誤撿 transient |
| 1d | `capture-pane` 內容 + `looksLikeCC` | 使用者可控 |

### 新（本 spec）

| Layer | 實作 | 角色 |
|:---:|------|------|
| Identity | `AgentProvider.Identify(ProcessInfo)` | 身份識別（exe basename + argv pattern，`ExePath` 已 resolve symlinks）|
| Q1 | `kill -0 pid` | PID liveness（per-frame）|
| Q2 | `capture-pane -e` hash diff | 畫面 activity（frame pop 需 Q1 AND shell prompt）|
| Q3 | 向上 walk PPID chain 驗證 pane_pid 為 ancestor | PID 歸屬驗證（Verify flow 使用） |

舊 Layer 1a、1d 整個刪除；1b、1c 的 `ps` 能力保留（被 Identify 和 Q3 使用），但不再用 `comm` 比對。

---

## 7. 風險與取捨

### 7.1 過度驗證導致合法事件被丟棄

**取捨**：寧可保守拒絕，也不讓 detached runtime 再覆蓋活著的 session。拒絕時必須留 log + reason 以便 debug。若出現合法 wrapper 場景被誤拒，透過 case-by-case 調整 shim list（§5.1）或 Identify 邏輯處理（provider 內部），不放寬 verify 主流程。

### 7.2 Frame model 讓 UI projection 更複雜

**取捨**：複雜度上升是必要成本。否則 `tmux_session` 單列模型永遠無法表示多 agent 現實。本 spec 限定只在 daemon 層做 projection，SPA 仍然看到單一 `agentTypes[ck]`（= TopFrame.AgentType），SPA 改動面接近零。

### 7.3 Codex ApplyPatch 期間狀態不變

**取捨**：這是 Codex upstream 缺 hook 的問題（Issue #16732），不在本 spec 解決。使用者體感上頂多是「running 多停一下」，不是錯誤狀態。待 upstream 修復或評估值得 probe 補再說。

### 7.4 Schema 遷移成本

**取捨**：alpha 階段接受直接切新 schema，舊 payload 被拒絕（log warning）。使用者重啟 agent 後就切到新流程。不做雙軌以避免複雜度。

### 7.5 PID reuse 風險

**取捨**：`ProcessStartTime` 欄位 + replay 驗證把 PID reuse 的誤判機率降到很低。仍保留殘留風險（lstart 秒級精度，極短時間內重用相同 PID 可能繞過），但實務上此窗口極窄，接受。

### 7.6 Verify 在 HTTP handler 同步執行

**取捨**：所有 verify 操作（`kill -0`、`tmux display`、`ps -ax`、`ps -o lstart=`、symlink resolve、basename compare）都是毫秒級，總和 <50ms 應該穩定。若某天 verify 路徑變重可考慮改 async + pending frame，但現階段同步更簡單且不拖慢 hook 的 2s timeout。

---

## 8. 驗收標準

達成以下條件才算此系列收斂完成：

1. Detached Codex runtime（PPID=1）不會再覆蓋活著的 CC session icon
2. Pane 內 wrapped descendants（node → codex）仍能被正確判定 alive
3. CC 把 argv[0] 改成 `2.1.114` 時，provider.Identify 仍能正確識別為 CC（使用 ExePath basename）
4. Node wrapper 啟動的 CC / Codex（`node .../claude-code/index.js`）能被 provider.Identify 正確識別（使用 argv pattern）
5. Periodic sweep 以 per-frame `kill -0` 為單位，不會因 tmux 失聯大屠殺（§7.x PR #486 review finding 3）
6. Daemon restart 後 replay verified frames 不產生錯誤 projection，PID reuse 情境下能正確丟棄過期 frame
7. CC / Codex / terminal 三種 icon 切換在手動驗證中穩定可重現
8. Codex Ctrl+C 回 shell 後 icon 在 1.5s（idle 偵測）+ 下一輪 2s sweep 視窗內回到 terminal（最慢約 3.5s）

---

## 9. 手動驗證情境

以下情境需逐一手動驗證：

| # | 場景 | 預期結果 |
|:---:|------|----------|
| 1 | tmux 內啟動 CC | icon 穩定為 cc |
| 2 | 從 CC 觸發 detached Codex review | **cc icon 不被覆蓋**；Codex 事件進不了 CC 的 session（reject reason=`pid_not_in_pane_tree`）|
| 3 | 新開純 Codex tmux session | icon 切為 codex |
| 4 | CC 內呼叫 Codex 作為 subprocess（非 detached）| CC + Codex 兩個 frame 共存；經過至少一輪 quiet period / sweep 後仍不誤清；Codex 結束後回 CC |
| 5 | CC argv[0]=`2.1.114` 情況下送 hook | Verify 通過，Identify 命中 cc（exe basename）|
| 6 | 從 node wrapper 啟動 CC / Codex（例如 `node /path/to/<agent>/cli.js`）| Verify 通過，Identify 命中對應 agent（argv pattern）|
| 7 | Codex 內 apply_patch 長時間編輯（無 PreToolUse hook）| icon 維持 running 或走 activity idle（畫面不動就 idle）|
| 8 | Codex Ctrl+C 退回 shell | icon 在 1.5s idle 偵測 + 下一輪 sweep 視窗內回到 terminal（最慢約 3.5s） |
| 9 | CC 觸發 Notification → 使用者 terminal 直接回應（不按按鈕）| icon 從 waiting 回到 running |
| 10 | kill -9 殺掉 CC 程序 | icon 在下個 sweep 內（≤2s）回到 terminal |
| 11 | Daemon restart 時有 3 個活躍 agent session | replay 後三個 icon 正確恢復 |
| 12 | Daemon restart 後，原 PID 在 OS 被重用（不同程序）| replay 時該 frame 被丟棄（start_time 不符）|
| 13 | Tmux session rename（`tmux rename-session`）或 tmux 短暫失聯 / pane 暫時無法解析 | 不因 tmux 名稱或 tmux 短暫失聯而大屠殺清除；rename 時 icon 不消失不閃爍 |
| 14 | 全新安裝、CC 已在某 tmux session 跑著 | icon 為 terminal 直到使用者觸發下個 hook（接受行為，§3.2）|

---

## 10. References

- Issue #487 — [Codex shared runtime 冒名 CC tmux_session](https://github.com/wake/purdex/issues/487)
- PR #484 — [fix(agent): probe wrapped descendants with bounded cache](https://github.com/wake/purdex/pull/484)
- PR #486 — [feat(agent): periodic liveness sweep via watchAlive](https://github.com/wake/purdex/pull/486)
- [Codex Hooks — OpenAI Developers](https://developers.openai.com/codex/hooks)
- [Codex Issue #16732 — ApplyPatchHandler doesn't emit PreToolUse/PostToolUse](https://github.com/openai/codex/issues/16732)
- [Codex PR #15118 — turn_id extension for Stop & UserPromptSubmit](https://github.com/openai/codex/pull/15118)
- `docs/superpowers/specs/2026-04-13-probe-chain-design.md`（既有 Probe Chain 架構）
