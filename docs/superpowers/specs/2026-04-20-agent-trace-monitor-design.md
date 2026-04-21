# Agent Hook Trace Monitor — Design

Date: 2026-04-20
Status: Draft (design phase)
Scope: daemon + SPA dev-mode settings monitor

## Problem

目前 agent 燈號 / agent type 的最終顯示，主要來自 daemon 收到 hook 後的一連串判斷：

- hook ingest
- verify
- frame upsert / delete
- projection recompute
- WS emit

但系統只保留「當前狀態」，沒有 append-only 的 hook 判斷紀錄。遇到 daemon
restart、hook arrival order 反轉、frame 關係晚到、projection 決策錯亂等情境時，
只能看到錯的最終結果，看不到它是怎麼被判成那樣。

這使得下列問題難以追：

- 某次 hook 為何被 accepted / rejected
- 某個 frame 為何被建立 / 更新 / 刪除
- projection 為何把某 agent 當 top
- 這次 emit 出去的 agent type / status 是哪個步驟決定的

## Goal

第一階段先做 hook-only 的可觀測性，不改現有顯示規則：

- daemon 端新增 append-only trace rail，只記錄 hook 傳遞鏈
- trace 持久化到 SQLite，daemon restart 後仍可回放
- 在 dev mode 的 settings 新增 monitor 頁面，只讀 trace / projection
- UI 以階層 block 顯示一次完整 hook 傳遞鏈，不做平面 log

## Non-goals

- 這一版**不**納入 probe trace
- 這一版**不**納入 sweep / replay / system trace
- 這一版**不**改寫 primary agent / child agent / top frame 規則
- 這一版**不**改 tab icon / status 的最終算法
- 這一版**不**把 monitor 做成正式使用者功能
- 這一版**不**做 event sourcing 重構

## Design Principles

### 1. 第一版只追 hook

monitor 第一版只追蹤 hook 鏈：

- 一次 hook POST → 一條 chain
- chain 內記錄 verify / frame / projection / emit

probe / sweep 後續再補，不先混進 schema 與 UI。

### 2. 先記錄，再修規則

這份 spec 先把「hook 進來後發生了什麼」記下來，讓後續 primary/child 規則修正有
可靠依據。這份 spec 不把 projection semantics 一起重寫。

### 3. Chain 是一次 hook 的完整傳遞鏈

第一版明確定義：

- 一次 hook → 一條 chain
- 這條 chain 從 `handleEvent` 開始，到最終 `emit / skip emit` 結束

### 4. block tree 要明確表達層級與判斷資訊

monitor 的核心不是時間排序，而是可視化一條 hook 經過哪些 block：

- Trigger
- Verify
- Frame
- Projection
- Emit

每個 block 都要能看見：

- 這一步做了什麼
- 判斷結果是什麼
- 理由是什麼
- before / after 差異是什麼

## Trace Model

### Chain

一條 chain 代表一個 hook root event。

建議型別：

```go
type TraceChain struct {
    ChainID         string
    StartedAt       int64
    TmuxSession     string
    PaneID          string
    RootAgentType   string
    RootEventName   string
    RootReason      string
}
```

### Step

一條 chain 由多個 step 組成，每個 step 是一個明確 block。

建議型別：

```go
type TraceStep struct {
    StepID           string
    ChainID          string
    ParentStepID     string
    Seq              int
    Kind             string // trigger | verify | frame | projection | emit
    TmuxSession      string
    PaneID           string
    AgentType        string
    FrameID          string
    ParentFrameID    string
    EventName        string
    Decision         string
    Reason           string
    PayloadJSON      string
    BeforeJSON       string
    AfterJSON        string
    CreatedAt        int64
}
```

## SQLite Persistence

trace 必須持久化到 DB。

原因：

- daemon restart 後仍可回看先前 hook 判斷鏈
- monitor 頁不用依賴 in-memory debug buffer
- 後續修 hierarchy / projection 規則時可直接回放真實案例

### Tables

新增兩張表：

#### `agent_trace_chains`

```sql
CREATE TABLE agent_trace_chains (
  chain_id            TEXT PRIMARY KEY,
  started_at          INTEGER NOT NULL,
  tmux_session        TEXT NOT NULL DEFAULT '',
  pane_id             TEXT NOT NULL DEFAULT '',
  root_agent_type     TEXT NOT NULL DEFAULT '',
  root_event_name     TEXT NOT NULL DEFAULT '',
  root_reason         TEXT NOT NULL DEFAULT ''
);
```

索引：

- `(started_at DESC)`
- `(tmux_session, started_at DESC)`
- `(pane_id, started_at DESC)`

#### `agent_trace_steps`

```sql
CREATE TABLE agent_trace_steps (
  step_id             TEXT PRIMARY KEY,
  chain_id            TEXT NOT NULL,
  parent_step_id      TEXT NOT NULL DEFAULT '',
  seq                 INTEGER NOT NULL,
  kind                TEXT NOT NULL,
  tmux_session        TEXT NOT NULL DEFAULT '',
  pane_id             TEXT NOT NULL DEFAULT '',
  agent_type          TEXT NOT NULL DEFAULT '',
  frame_id            TEXT NOT NULL DEFAULT '',
  parent_frame_id     TEXT NOT NULL DEFAULT '',
  event_name          TEXT NOT NULL DEFAULT '',
  decision            TEXT NOT NULL DEFAULT '',
  reason              TEXT NOT NULL DEFAULT '',
  payload_json        TEXT NOT NULL DEFAULT '{}',
  before_json         TEXT NOT NULL DEFAULT '{}',
  after_json          TEXT NOT NULL DEFAULT '{}',
  created_at          INTEGER NOT NULL,
  FOREIGN KEY(chain_id) REFERENCES agent_trace_chains(chain_id) ON DELETE CASCADE
);
```

索引：

- `(chain_id, seq ASC)`
- `(tmux_session, created_at DESC)`
- `(pane_id, created_at DESC)`
- `(frame_id, created_at DESC)`

### Retention

trace 是 dev monitor 用資料，不做永久保存。

第一版 retention：

- `agent_trace_chains` 保留最近 `10_000` 筆
- `agent_trace_steps` 保留最近 `100_000` 筆

實作方式：

- 每次新增 chain 後，若超出 cap，刪最舊的 chain
- step 依 `ON DELETE CASCADE` 一起刪

不做時間型 TTL，先做 count cap，行為可預期且易測。

## Step Taxonomy

第一版 block 種類與常見 decision / reason：

### Trigger

- `kind = trigger`
- 代表 hook POST 進來

decision 範例：

- `received`

reason 範例：

- `hook_post`

### Verify

- `kind = verify`
- 對應 hook verify gate

decision 範例：

- `accepted`
- `rejected`

reason 範例：

- `pid_not_in_pane_tree`
- `identify_mismatch`
- `pid_reused`
- `sender_uncertain`

### Frame

- `kind = frame`
- 記錄 frame create / update / delete / relink

decision 範例：

- `created_frame`
- `updated_frame`
- `deleted_frame`
- `attached_to_parent`
- `left_orphan`

reason 範例：

- `parent_frame_missing`
- `parent_frame_found`
- `session_end`

### Projection

- `kind = projection`
- 記錄 projection recompute 的結果

decision 範例：

- `projection_changed`
- `projection_unchanged`

reason 範例：

- `top_frame_recomputed`
- `frame_removed`
- `frame_upserted`

### Emit

- `kind = emit`
- 記錄最終對外 emit 的結果

decision 範例：

- `broadcasted`
- `skipped`

reason 範例：

- `session_code_resolved`
- `session_code_missing`

## Snapshots

每個 step 都可攜帶 `before_json` / `after_json`。

第一版 snapshot 只要求局部結構化，不要求全域完整 dump。

### Trigger payload

```json
{
  "tmux_session": "work",
  "pane_id": "%7",
  "agent_type": "codex",
  "event_name": "UserPromptSubmit",
  "sender_pid": 1234
}
```

### Frame snapshot

```json
{
  "frame": {
    "frame_id": "f1",
    "agent_type": "codex",
    "pid": 1234,
    "ppid": 1200,
    "parent_frame_id": ""
  }
}
```

### Projection snapshot

```json
{
  "projection": {
    "pane_id": "%7",
    "primary_frame_id": "f-cc",
    "top_frame_id": "f-codex",
    "top_agent_type": "codex",
    "subagent_count": 1
  }
}
```

### Emit snapshot

```json
{
  "normalized_event": {
    "agent_type": "codex",
    "status": "running",
    "raw_event_name": "UserPromptSubmit"
  }
}
```

## Daemon Integration Points

第一版 trace rail 插在既有 hook 判斷點旁路，不改原本行為。

- `handleEvent`
  - trigger step
  - verify step
- `applyFrameEvent`
  - frame step
- `projectionForSession` / `projectPane`
  - projection step
- `broadcastToSession`
  - emit step

trace 寫入失敗只記 log，不阻斷主流程。

## API

第一版只做 read-only API。

### `GET /api/agent/monitor/chains`

查詢條件：

- `session`
- `pane`
- `agent_type`
- `event_name`
- `limit`
- `cursor` 或 `before`

回傳 chain list + latest step summary。

### `GET /api/agent/monitor/chains/{id}`

回傳：

- chain metadata
- ordered step tree

### `GET /api/agent/monitor/projection`

查詢條件：

- `session` 或 `pane`

回傳目前 live projection summary，供 monitor 頁頂部顯示。

第一版不做 WS stream；monitor 頁用手動 refresh 或短 polling 即可。

## SPA Dev Monitor

### Placement

新增 settings section：

- id: `tmux-agent-monitor`
- label: dev-only
- order: 與 `dev-environment` 同級，放在 dev 區塊

顯示條件：

- 只有 dev flag 開啟時出現

### Layout

三欄：

#### 左欄：Chain List

- chain id
- started at
- session / pane
- root event name
- root agent type

#### 中欄：Step Tree

以 block tree 顯示，不做平面 log。

每個 block 顯示：

- `kind`
- `decision`
- `reason`
- `agent_type`
- `frame_id`
- `parent_frame_id`

#### 右欄：Inspector

顯示 selected step 的：

- payload JSON
- before JSON
- after JSON

另加 current projection summary：

- current primary frame
- current top frame
- current top agent type
- latest chain id

## Risks

### 1. Trace 寫入量過高

解法：

- 第一版只記 hook 關鍵步驟，不記每個小函式
- 加 count cap retention

### 2. JSON snapshot 太大

解法：

- 只存局部 snapshot
- 不 dump 全 store / 全 session list

### 3. Monitor 反而影響 hot path

解法：

- trace store 寫入獨立於主邏輯
- 寫 trace 失敗只記 log，不阻斷主流程

## Acceptance

- daemon 新增 trace tables 與 retention
- hook 傳遞鏈都能留下 chain + steps
- monitor 頁可依 session / pane / agent type / event name 篩選
- 每條 chain 以 block tree 顯示，不是平面 log
- 每個 block 可檢視 decision / reason / before / after
- daemon restart 後仍能看到先前 hook trace
- 第一版不改現有 tab icon / status 顯示規則
