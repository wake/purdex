# Purdex Kanban Agent Workflow Research

- 日期：2026-05-02
- 作者：OpenCode
- 目的：評估在 Purdex 內建簡易 Kanban 系統，並以該系統驅動 agent 開發工作的可行性與落地切法
- 依據：即時網路搜尋、GitHub live metadata、官方文件、Purdex 現有 daemon / SPA / agent module 程式碼盤點

---

## 核心結論

在 Purdex 內建簡易 Kanban 系統是可行且合理的方向，但應做成 **Purdex-native board/task workflow**，不要嵌入完整外部 Kanboard / Plane / OpenProject。

主要判斷：

- 外部生態顯示，成熟做法不是「找一個完整 kanban-agent 系統」，而是用 Kanban / issue 系統作 durable task state machine，再接 agent runtime / orchestration。
- Purdex 已經具備 agent telemetry、tmux session、stream handoff、send-keys、status / trace / frame 這些底層能力。
- Purdex 缺的是 durable task model、派工狀態、lease / heartbeat、artifact / review gate。
- Kanban UI 本身難度不高；真正要謹慎的是「用 board 自動驅動 agent 開發」所帶來的重複執行、卡死、錯 worktree、未經審核修改等風險。

建議做法：新增 `board` module 作任務真相來源，保留 `agent` module 作 runtime telemetry。第一階段只做人工 dispatch，第二階段再做自動 claim / lease / reviewer loop。

---

## 外部生態調查摘要

### 直接叫做 kanban-agent 的專案

GitHub live search 顯示 `kanban agent`、`AI agent workflow kanban` 這類直稱專案大多是 2026 新實驗 repo，星數多為 0 到個位數。

例外較值得參考者：

| 專案 | Live 狀態 | 判斷 |
|---|---:|---|
| `777genius/claude_agent_teams_ui` | 808 stars，AGPL-3.0，2026-05-02 更新 | 接近「CTO 看 Kanban，agents 作團隊」的產品型實驗 |
| `daggerhashimoto/openclaw-nerve` | 780 stars，MIT，2026-05-02 更新 | OpenClaw cockpit，含 agent automated kanban board、workspace/file control、sub-agent sessions |
| `L1AD/claude-task-viewer` | 586 stars，MIT，2026-04-30 更新 | Claude Code tasks 的 web Kanban viewer |
| `hallucinogen/agent-viewer` | 366 stars，2026-04-30 更新 | tmux 管 Claude Code agents 的 Kanban board |
| `BenPerel/github-kanban-agents` | 0 stars，2026-05-02 更新 | 概念貼近 GitHub Projects + agents，但不應作穩定依賴 |

結論：直稱 `kanban-agent` 的成熟度不足；更適合借鑑 workflow pattern，而非直接依賴。

### 成熟開源 Kanban / PM 系統

| 系統 | Live GitHub 狀態 | 對 Purdex 的參考價值 |
|---|---:|---|
| Kanboard | 9,569 stars，MIT，2026-05-01 更新 | JSON-RPC API、tasks、subtasks、metadata、automatic actions、plugin/events；適合作最小 task model 參考 |
| Plane | 48,626 stars，AGPL-3.0，2026-05-02 更新 | REST API 清楚，work items / states / comments / attachments / cycles / modules / custom properties；適合作現代產品化 API 參考 |
| OpenProject | 14,965 stars，GPL-3.0，2026-05-02 更新 | API v3、work packages、custom actions；MCP server 是 Enterprise add-on 且目前 read-only |
| Vikunja | 4,104 stars，AGPL-3.0，2026-05-02 更新 | OpenAPI / API token / webhooks；第三方 `vikunja-mcp` 顯示 task system + MCP 有需求 |
| Wekan | 20,918 stars，MIT，2026-05-02 更新 | Trello-like、real-time UI、openapi 目錄；可參考自架 board UX |
| Taiga | 824 stars，MPL-2.0，2026-04-29 更新 | REST API 含 stories / tasks / issues / statuses / bulk kanban order / webhooks |

結論：Purdex 不需要複製完整 PM 系統，只需要 durable task + state transition + event log + artifact link。

### Agent orchestration framework pattern

| Framework | Live GitHub 狀態 | 可借鏡派工模式 |
|---|---:|---|
| LangGraph | 31,022 stars，MIT，2026-05-02 更新 | Subagents、Handoffs、Skills、Router、custom workflow；適合作 board 狀態機與 dispatcher graph 參考 |
| AutoGen | 57,651 stars，CC-BY-4.0，2026-05-02 更新 | RoundRobinGroupChat、SelectorGroupChat、MagenticOneGroupChat、Swarm；適合 reviewer / critic / handoff loop |
| CrewAI | 50,458 stars，MIT，2026-05-02 更新 | Sequential / hierarchical process、allow_delegation、delegate work、ask coworker、task context、guardrails |
| MetaGPT | 67,616 stars，MIT，2026-05-02 更新 | AI software company 角色流水線，適合 PM / architect / engineer / reviewer 固定流程 |
| CAMEL | 16,860 stars，Apache-2.0，2026-05-01 更新 | Society module、role-playing、task planner、critic-in-loop、turn-based collaboration |

結論：Purdex 應先採用 Board-as-State-Machine + Pull/Lease + Human Gate；不要一開始追完整 multi-agent society。

---

## Purdex 現況盤點

### 已具備的基礎

| 能力 | 現況證據 | 對 Kanban workflow 的意義 |
|---|---|---|
| daemon module 架構 | `internal/core/core.go` 定義 `Module` interface；`cmd/pdx/main.go:237` 註冊 modules | 可新增 `board` module，不需改核心架構 |
| HTTP route 註冊 | `Core.RegisterRoutes` 逐 module 掛路由 | board API 可獨立掛 `/api/board/*` |
| tmux session 管理 | `internal/module/session` | task 可綁定 session，必要時建立新 session |
| send-keys 注入 | `internal/module/session/handler.go:293` 的 `/api/sessions/{code}/send-keys` | 第一版 dispatch 可直接把 prompt 注入既有 session |
| stream handoff | `internal/module/stream/handler.go:153` | 可把 session 切到 stream mode 跑 preset / relay |
| agent status / hook | `internal/module/agent/handler.go`、`internal/module/agent/module.go` | task 可根據 agent runtime event 更新 activity |
| frames / traces | `internal/store/frames.go`、`internal/store/trace.go` | 可把 task 與 chain / frame / artifact 關聯 |
| SPA host API | `spa/src/lib/host-api.ts` | 可新增 board API client 與 board UI |

### 缺口

| 缺口 | 影響 |
|---|---|
| 沒有 durable task model | 現有 agent event 是 telemetry，不是工作真相來源 |
| 沒有 task status transition | 無法表示 Backlog / Ready / Running / Review / Done |
| 沒有 claim / lease | 自動派工時可能重複執行同一張卡 |
| 沒有 heartbeat / timeout | agent 卡死時 task 無法自動釋放或標記 failed |
| 沒有 artifact model | PR、patch、log、test output、trace chain 沒有統一掛回 task |
| 沒有 review gate | agent 產出無法自動進入可審核狀態 |
| 沒有 worktree / branch discipline | 對本 repo 的開發規範無法自動落實 |

---

## 建議架構

### 邊界

新增 `internal/module/board`，不要把 Kanban 放入 `internal/module/agent`。

責任切分：

| Component | 職責 |
|---|---|
| `board` module | durable tasks、columns/status、events、artifacts、claim/lease、dispatch state |
| `agent` module | runtime telemetry：hook event、status、frame、trace、projection |
| `session` module | tmux session CRUD、send-keys、terminal WS |
| `stream` module | relay、stream handoff、mode switching |
| `dispatcher` | board task -> target session / agent -> prompt injection -> state update |

建議資料流：

```text
SPA Board UI
  -> /api/board/tasks
  -> board module + board.db
  -> dispatch task
  -> session / stream injection
  -> agent runs in tmux
  -> agent hooks / statusline
  -> agent module broadcasts telemetry
  -> board module links telemetry back to task
```

### MVP data model

`board_tasks`：

| 欄位 | 用途 |
|---|---|
| `id` | task id |
| `title` | 顯示標題 |
| `description` | 任務內容 / prompt basis |
| `status` | kanban 狀態 |
| `priority` | 排序 / dispatch priority |
| `workspace` | 對應 Purdex workspace 或 repo path |
| `session_code` | 綁定 tmux session |
| `agent_type` | `cc` / `codex` / `opencode` |
| `claimed_by` | worker / agent claim id |
| `lease_until` | 防止永久占用 |
| `acceptance_criteria` | review 判定依據 |
| `created_at` / `updated_at` | lifecycle timestamps |

`board_events`：

| 欄位 | 用途 |
|---|---|
| `id` | event id |
| `task_id` | task foreign key |
| `kind` | `created` / `status_changed` / `dispatched` / `agent_event` / `artifact_added` / `reviewed` |
| `message` | human-readable log |
| `payload_json` | raw structured payload |
| `created_at` | timestamp |

`board_artifacts`：

| 欄位 | 用途 |
|---|---|
| `id` | artifact id |
| `task_id` | task foreign key |
| `kind` | `trace_chain` / `patch` / `commit` / `pr` / `log` / `test_result` |
| `ref` | URL、path、chain id、commit sha |
| `created_at` | timestamp |

### MVP status set

建議第一版欄位：

```text
backlog -> ready -> claimed -> running -> review -> done
```

保留異常欄位：

```text
blocked, failed, needs_human
```

### API surface

第一階段 API：

| Method | Path | 用途 |
|---|---|---|
| `GET` | `/api/board/tasks` | list tasks |
| `POST` | `/api/board/tasks` | create task |
| `GET` | `/api/board/tasks/{id}` | task detail |
| `PATCH` | `/api/board/tasks/{id}` | update title/status/metadata |
| `POST` | `/api/board/tasks/{id}/events` | append event |
| `POST` | `/api/board/tasks/{id}/dispatch` | manual dispatch to session / agent |

第二階段 API：

| Method | Path | 用途 |
|---|---|---|
| `POST` | `/api/board/tasks/{id}/claim` | claim with lease |
| `POST` | `/api/board/tasks/{id}/heartbeat` | renew lease |
| `POST` | `/api/board/tasks/{id}/release` | release claim |
| `POST` | `/api/board/tasks/{id}/artifacts` | attach artifact |
| `POST` | `/api/board/dispatch/next` | scheduler pull next task |

---

## 驅動 agent 開發的建議流程

### Phase 1：人工 dispatch

流程：

1. 使用者建立 task。
2. 使用者把 task 移到 `ready`。
3. 使用者選 target agent 與 session。
4. Purdex 呼叫 dispatch endpoint。
5. dispatch endpoint 產生 prompt，透過 session send-keys 或 stream handoff 注入。
6. task 進 `running`。
7. agent hook / statusline 寫入 board event log。
8. 使用者手動把 task 移到 `review` 或 `done`。

這個階段不做自動 claim，可以先驗證 board UI、task/event model、prompt injection 是否符合工作流。

### Phase 2：Pull + lease

流程：

1. dispatcher 找 `ready` task。
2. 用 transaction 將 task 改成 `claimed`，寫入 `claimed_by` 與 `lease_until`。
3. dispatcher 建 session 或選既有 session。
4. 注入 prompt 後改成 `running`。
5. agent 或 dispatcher 定期 heartbeat。
6. lease 過期則 task 回 `ready` 或 `failed`。

這個階段處理重複執行與卡死問題。

### Phase 3：review / artifact gate

流程：

1. agent 完成工作後 task 進 `review`。
2. reviewer agent 或 human reviewer 檢查 artifact。
3. artifact 包含 patch、commit、PR、test log、trace chain。
4. 通過才進 `done`。
5. 未通過則回 `ready` 或 `blocked`，並附 review feedback。

這個階段才適合接近「agent 開發自動化」。

---

## 本 repo 的特殊約束

Purdex 的 `AGENTS.md` 規範會直接影響 board-agent workflow 設計。

需要變成 task gate 的規則：

| 規則 | Board / dispatcher 對應 |
|---|---|
| 新工作一律從最新 `origin/main` 開獨立 worktree | task 必須記錄 `base_ref`、`worktree_path`、`branch` |
| 先寫 failing test 再實作 | task template 應要求 test-first，review gate 檢查 test evidence |
| 每個 task 一個 commit | task done gate 應關聯 commit sha |
| 不要直推 main | dispatcher 不應自動 push / merge main |
| VERSION + CHANGELOG 是 PR merge 後才補 | task prompt 應明確禁止一般功能 PR 修改版本檔 |
| Locale 變更要雙語同步 | task template / review gate 可加 locale completeness check |

MVP 不應自動 commit / PR / merge。先讓 agent 產出 patch / log / summary，由人確認。

---

## 風險與緩解

| 風險 | 影響 | 緩解 |
|---|---|---|
| task 重複執行 | 兩個 agent 改同一工作 | `claim` + transaction + `lease_until` |
| agent 卡死 | task 永遠 running | heartbeat + timeout + reclaim |
| session 被刪或 rename | task 綁定失效 | task 綁定 immutable session code，並監聽 session deletion |
| 錯 worktree / branch | 修改錯基線 | task metadata 必填 `worktree_path`、`branch`、`base_ref` |
| prompt 不足 | agent 做錯範圍 | task template 強制包含 goal / constraints / acceptance criteria / forbidden actions |
| telemetry 與 task 狀態混淆 | board 狀態被 hook noise 污染 | board 只接收高階映射事件，不直接把每個 raw hook 當狀態 |
| 過早全自動 | 破壞 repo / 產生不可審核變更 | Phase 1/2 使用 human gate；commit/PR automation 延後 |

---

## 建議實作切分

### PR 1：Board storage + API + basic UI

範圍：

- `internal/module/board`
- SQLite store + migration
- task CRUD
- status transition
- board event log
- SPA `spa/src/features/board`
- host API client functions

不包含：

- agent dispatch
- auto claim
- lease
- reviewer agent

### PR 2：Manual dispatch

範圍：

- `POST /api/board/tasks/{id}/dispatch`
- 選擇既有 session 或建立 session
- 透過 `send-keys` 注入 prompt
- task 綁定 `session_code` / `agent_type`
- agent status / trace chain link 回 task event log

不包含：

- scheduler 自動拉 task
- 自動 commit / PR

### PR 3：Lease + scheduler

範圍：

- claim / heartbeat / release
- lease timeout reclaim
- scheduler pull next task
- failed / retry 狀態

### PR 4：Review + artifacts

範圍：

- artifact model
- trace chain / patch / commit / PR / log links
- review status
- human gate UI
- optional reviewer-agent dispatch

---

## 不建議事項

- 不建議嵌入完整外部 Kanboard / Plane，會增加部署、權限、資料同步與 UX split-brain。
- 不建議把 board schema 混入 `agent_events.db` 的現有 telemetry 表；任務是 durable workflow state，應有獨立 store 或至少獨立 module schema。
- 不建議第一版做全自動 agent swarm；應先驗證 task -> dispatch -> event log -> review 的基本閉環。
- 不建議第一版自動 commit / push / PR；先產出 artifact，交由 human gate。

---

## 最終建議

Purdex 應做一個簡易但 durable 的 board module，定位是「agent work control plane」，不是一般 PM 工具。

第一個可交付版本只需要做到：

- 任務列表與 Kanban 狀態
- task detail / event log
- manual dispatch 到既有 tmux session
- agent runtime telemetry 回寫 task activity

這會把 Purdex 現有 session / stream / agent monitor 能力串成可操作的開發工作流，同時把風險控制在人工審核邊界內。後續再逐步引入 lease、scheduler、reviewer、artifact gate。
