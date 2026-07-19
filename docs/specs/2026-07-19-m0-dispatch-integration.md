# Spec: M0 — Ploom↔Purdex 派工整合（Walking Skeleton）

> **狀態**：草稿（待 codex 審 → plan）
> **里程碑**：M0（第一個有用的閉環；demo-able 但脆）。最小可用線在 M0→M1.5，M0 是起點。
> **範圍**：跨 Ploom + Purdex 兩 repo。傳輸走 **pull 模型**（Purdex daemon 輪詢 Ploom）。
> **關聯**：疊在 Ploom `docs/specs/2026-06-30-s6-purdex-dispatch-design.md`（PARKED，pull 骨架與 §7 拍板均在）之上，以五層資料模型 + `execution_id` 穩定 handle 擴充。

---

## 1. Objective

**把 work item 與 execution 對上、讓 session 不再瞬時。** 使用者在 Ploom issue 按「派工」，Purdex daemon 領工、在指定 repo 用既有 `claude -p` 引擎跑起一次 execution，狀態與產出（diff / transcript pointer）回填到 issue 活動流，並提供 deeplink 讓人跳進 Purdex 看即時。

- **User**：單人自用（dogfooding），單 daemon / 單 host。
- **成功長相**：Ploom issue 上看得到 `queued → running → completed/failed` 狀態列 + 一個可點的 deeplink + 完成後 diff 摘要回填。整條鏈路端到端跑通一次。
- **核心痛點解除**：session 做完就散、無處組織/追蹤 → issue 成為 execution 的家。

### 1.1 五層資料模型（本 spec 的骨幹）

```
issue ──> dispatch ──> execution ──> attempt ──> session
 (Ploom)   (Ploom)      (雙側投影)     (M0 恆=1)   (Purdex runtime)
```

- **穩定對外 handle = `execution_id`**（不是 `session_code`，不是 `dispatch_id`）。deeplink、狀態回報、Ploom 投影全綁 `execution_id`。
- **M0 attempt 恆為 1**（最薄）；attempt 一等概念的資料層留到 M3。但 `attempt_no` 欄位從第一天就在契約裡（=1），避免晚抽 handle。
- **SOT 分工**：**Purdex = runtime SOT**（execution 真實狀態機的權威）；**Ploom = projection SOT**（只投影 runtime 狀態、只產 dispatch intent，**不推進** runtime 狀態機）。

### 1.2 M0 刻意不做（defer 對照）

| 不做 | 去哪 |
|------|------|
| **自動** lease / heartbeat / timeout（斷線自動保命）| M1 |
| **手動 reclaim + startup reconcile sweep** | **M0 必做**（見 §5.4；防 crash wedge，codex R1 #1）|
| idempotent 重派、controller/observer **寫入權**分離 | M1 |
| codex launch adapter（provider 中立**接縫** M0 做，**live** 留 M1）| M1 |
| worktree 隔離 | M3（M0 靠 admission rule + per-repo lock 擋，見 §7）|
| attempt 資料層（多次嘗試）| M3 |
| inline diff review console / follow-up 迴圈 | M4 |
| `ExecutionControlRequest`（stop/interrupt）| M1（M0 契約 reserved/forward-ref，見 §4.4）|
| `plm daemon register/list` CLI | M1+（M0 手動 seed daemon row）|
| 內嵌執行 UI | M0 只給 deeplink（**observe-only**，見 §9）|

> **⚠️ M0 stdin 寫入權（codex R1 #7）**：現碼 `handler.go` 的 `SubscriberToRelay` 讓**每個 subscriber 都寫進 relay stdin**（多 writer race）。M0 dispatch execution 是 **headless `claude -p`**，deeplink attach **只觀察不寫入**（§9），故 M0 **不暴露 dispatch execution 的 stdin 給 deeplink 開的 view**——以此迴避 race，不需 M0 就做完整 controller/observer 分離。完整寫入權仲裁留 M1。

---

## 2. Tech Stack

- **Ploom** (`~/Workspace/wake/ploom`，0.1.29-dev)：Go net/http（httpx router→handler 直呼 store，無 service 層）；modernc SQLite 單寫連線 + OCC version；authz 純函式；React19+Vite8+RR7+TanStack Query v5 前端。
- **Purdex** (`bin/pdx`，1.0.0-alpha.324)：Go net/http + gorilla/websocket + creack/pty + modernc SQLite；既有 `claude -p` relay（`internal/relay/relay.go`）+ handoff（`internal/config/config.go:79`）+ sessions/stream API（`internal/module/stream/`）；Electron shell（`electron/main.ts`）。
- **傳輸**：HTTP，pull 方向（daemon → Ploom）。認證 Bearer（daemon 綁 is_agent 帳號 S4 token）。

---

## 3. 傳輸與 API（Pull 模型）

> Ploom 是純 server，**無 inbound dispatch endpoint 打到 Purdex**。所有動作 daemon 發起：輪詢領工 → claim → report。這修正了先前 push 草案的方向錯誤。

```
[Ploom :server] ── GET /daemon/dispatches?status=pending ─┐   輪詢領工(最小清單)
                ── POST /daemon/dispatches/{id}/claim ─────┤   原子 pending→claimed
                ── GET  /daemon/dispatches/{id} ───────────┤   兩段式②抓完整(issue+repo_location)
                ── POST /daemon/dispatches/{id}/report ────┘   回報 execution 狀態/artifact pointer
       ▲
       │ 起 claude -p、組 execution、擷取 diff —— 全在 Purdex daemon 端做
[Purdex daemon]
```

### 3.1 Ploom daemon 端點（authz = daemon 綁定帳號 token；dispatch 錨點 = issue project **editor+**）

| Endpoint | 作用 | Response body | 隔離 |
|----------|------|---------------|------|
| `GET /daemon/dispatches?status=pending` | 回**最小清單**〔兩段式①〕，**只回屬於 caller daemon 的** | `[{dispatch_id, issue_id, schema_version}]` | daemon_id + 帳號 membership 雙層 |
| `POST /daemon/dispatches/{id}/claim` | 原子 `pending→claimed`（寫 `claimed_at`, `daemon_id`）| **成功**：`200 {dispatch_id, status:"claimed"}`；**同 daemon 重複 claim**：`200`（冪等，回既有；若已有 execution 則帶 `execution_id`）；**他 daemon 已 claim**：`409` | — |
| `GET /daemon/dispatches/{id}` | 兩段式②：完整 issue + `repo_location` + `sandbox_profile`(request) | `{dispatch_id, issue{...}, repo_location{...}, sandbox_profile}` | 限 caller daemon |
| `POST /daemon/dispatches/{id}/report` | 回報執行狀態；Ploom 投影 + 組 deeplink | `200 {ack_seq}`（見 §3.3）；`accepted` 未先 ack 就發 lifecycle → `409 {code:"accepted_required"}` | seq 冪等 |

**Error code taxonomy（M0）**：`accepted_required`（lifecycle 先於 accepted）/ `dispatch_not_found` / `not_owner`（非 caller daemon）/ `already_claimed`（他 daemon）/ `schema_incompatible`（版本不合）/ `stale_seq`（seq ≤ ack，非錯誤、response 仍回 200 帶 ack_seq）。

### 3.2 兩段式抓取

- **①輪詢**：只回 `dispatch_id + issue_id`（省頻寬、快）。
- **②領工後**：`GET /daemon/dispatches/{id}` 才回完整 issue + repo_location + sandbox_profile。

### 3.3 Report 的可靠性語意（daemon → Ploom）

> **注意**：pull 模型下沒有 Ploom→Purdex callback。「回報」= daemon 主動 POST report。以下語意套在 report 呼叫上。

- **Auth**：daemon 帳號 S4 Bearer token。
- **Retry/backoff**：Ploom 回 5xx → daemon 指數退避重試；401/403 → **永久失敗**（不重試，記錄）。
- **Seq 冪等 + ack cursor（codex R1 #5）**：每筆 report 帶**單調遞增 `seq`**（per execution）。
  - Ploom 忽略 `seq ≤` 已接受的最大值（重複/亂序丟棄），並在 **report response 回 `ack_seq`**（目前已投影到的最大 seq）。
  - **`accepted` 是 lifecycle 的前置**：daemon 必須先讓 `accepted`（seq=1，攜帶 `head_at_start`/`dirty_at_start`/`effective_sandbox_profile`/deeplink seed 等**不可補的 immutable metadata**）被 ack，**才**發送 `running`/`completed`/`failed`。避免「accepted 掉包但 running 成功 → Ploom 知道在跑卻永遠缺 base metadata」的不可收斂缺洞。
  - **daemon 端持久化 outbound report**（含未 ack 的），daemon 重啟時**依 `ack_seq` replay 未確認的 report**。這是 M0 最小可靠投影，不是 M1 才補。
- **狀態單向**：Ploom 只投影 daemon 給的 runtime 狀態，**不自行推進**（projection SOT）。

---

## 4. 事件契約（邏輯層，映射到 pull 傳輸）

> 事件是**邏輯概念**，傳輸走 pull（不是 push message bus）。schema 帶版本，欄位穩定。

### 4.1 `schema_version`

所有 daemon↔Ploom payload 帶 `schema_version`（M0 = `1`）。Ploom 與 daemon 皆檢查；不相容版本 → 明確拒絕（非 silent）。

### 4.2 邏輯事件 ↔ pull 動作對照

| 邏輯事件 | pull 傳輸落點 | 內容 |
|----------|--------------|------|
| `DispatchRequested` | Ploom `dispatch` row（pending），daemon `GET pending` 讀到 | `dispatch_id, issue_id, schema_version` |
| `ExecutionAccepted` | daemon claim 後**首次** `report`（status=`accepted`, seq=1）| `execution_id, attempt_no(=1), dispatch_id(echo), repo_location(echo), effective_sandbox_profile, session_code(nullable), head_at_start, dirty_at_start` |
| `ExecutionLifecycle` | daemon `report`（status=`running`/`completed`/`failed`）**必在 accepted 被 ack 後** | `execution_id, status, seq, error?` |
| `ExecutionArtifact` | daemon `report` 帶 `artifacts[]`（**pointer-first**，見 §6）| `execution_id, artifacts[]{kind, pointer, meta}` |

**Ordering 硬規則**：`accepted`(seq=1) → ack → 之後才 `running`/terminal。artifact 只能在 `accepted` 之後。pointer scheme 見 §6.1。

### 4.3 冪等與 race（Ploom 先寫 dispatch row → daemon 動作）

- **建立**：Ploom 派工時**先 commit `dispatch` row（pending）**再無他事；daemon 之後才輪詢到。
- **execution 冪等（row）**：daemon 在**單一交易內**以 `dispatch_id` upsert execution 投影——若該 `dispatch_id` 已有 execution，回**既有 `execution_id`**（不重建）。
- **⚠️ Launch fence（codex R1 #3）**：row 冪等只擋「重複建 row」，**擋不住重複 launch side effect**。故 execution row 帶 **`launch_state`**（`none → launching → launched`）+ `session_code`：
  - daemon 起 `claude -p` **前**，於同交易把 `launch_state` 設 `launching`（fence 標記）。
  - recovery / 重派讀到 `launch_state ∈ {launching, launched}` → **不 relaunch**，改走 reconcile（§5.4）：檢查底層 session 是否還活，決定 `running` 或 `failed`。
  - 這防「首次已起 session A，daemon 在寫 session_code / 發 accepted 前崩潰 → 重用同 execution_id 又起 session B → 一條 execution 底下兩 session、diff/transcript/seq 全混」。
  - **M0 attempt=1 的安全前提**：一個 execution_id **至多綁一次成功 launch**。真要「重試/換做法」= **新 dispatch → 新 execution_id**（rerun 語意，非重用），attempt 資料層仍留 M3。
- **execution row 欄位**（Purdex 側 runtime SOT）：`execution_id, dispatch_id, repo_location(canonical), provider, launch_state, session_code(nullable), attempt_no, status, seq_reported, head_at_start, dirty_at_start, sandbox_profile, created_at, updated_at`。〔**刪除** `callback_target`——pull 模型下無 Ploom→Purdex callback，此為 push 殘留（codex R1 #8）〕
- **雙側兩表非雙寫**：Purdex 存 execution runtime row；Ploom 存 execution **projection** row（+ 寫進 `issue_event` append-only 活動流）。兩表各自權威（runtime vs projection），靠 report + ack cursor 同步，**不共用一張表、不雙寫**。

### 4.4 `ExecutionControlRequest` — M0 移除（V1）

stop / interrupt 等控制**整個移出 M0 契約**，改 **reserved / forward-ref**（欄位保留位、不實作行為）。M1 才做（需要 interactive lease 前置，見 M1 spec）。

---

## 5. Execution 生命週期 + `claude -p` 接線

### 5.1 狀態機（Purdex runtime SOT）

```
accepted ──> running ──> completed
   │            │
   └──> failed <┘          (failed: 起不來 / 執行錯 / -p 非零退出)
```

- Ploom projection 鏡像此狀態；issue 層「關單/done」是**人工 gate**（Ploom 不自動關 issue）。

### 5.2 沿用既有 `claude -p`（不新寫、不改 runner）

- **起 session**：沿用既有 handoff / stream 啟動路徑（`config.go:79` 的 `claude -p --verbose --input-format stream-json --output-format stream-json`）+ `relay.go` 橋接。M0 **不改 relay/runner 本體**。
- **execution wrapper**：新增薄層——以 `execution_id` 包住「一次 session 啟動」，記 runtime row、綁 `dispatch_id`。

### 5.3 ⚠️ Terminal 偵測 — 兩種來源，process-exit 為權威（codex R1 #2，前提已更正）

> **更正**：spec 初稿誤稱「relay 已 parse stream-json」。**現碼 `relay.go` 只逐行 tee stdout→WS，完全沒 parse**；ctx cancel→SIGTERM→5s→SIGKILL；subprocess 退出時送 WS close「subprocess exited」。故 `result` protocol event **不是**可靠的 terminal 觸發。

M0 terminal 偵測分**兩種來源**，明列為新 seam（非假設現成）：

| 來源 | 角色 | 現況 | M0 新工作 |
|------|------|------|-----------|
| **process exit（含 exit code）/ ws close** | **權威 terminal**——decide `completed`(exit 0) / `failed`(非 0 / 被 signal / 異常) | relay 已在 subprocess 退出時關 WS，但**未把 exit code 冒到 execution 層** | relay/bridge 把「subprocess 退出 + exit code」路由到 execution 層 → 觸發 terminal report |
| **`result` protocol event** | **僅 enrichment**（成功/失敗細節、usage、錯誤訊息）——**不**單獨判 terminal | 無 parse | 選擇性 parse `result` 充實 artifact/error，但**terminal 與否只認 process exit** |

- **異常路徑**：daemon 關閉 / relay 斷線 / context cancel → SIGTERM → 無 `result`。此時 process exit 仍發生 → execution → `failed`（若非乾淨退出）或由 §5.4 reconcile 判定。
- **不得**在收到 `result` 但 process 尚未退出時就標 `completed`（避免過早 terminal）。

### 5.4 Manual reclaim + startup reconcile sweep（codex R1 #1，**M0 必做**）

> 無此，claim 成功後 daemon 崩潰 / 主機重開 → dispatch 永停在 claimed/running，輪詢再也看不到（只吃 pending）→ **不可收斂殭屍**。

- **Startup reconcile**：daemon 啟動時掃自己的 execution runtime row 中 **非 terminal**（`accepted`/`running`，或 `launch_state ∈ {launching, launched}`）者，逐一 reconcile：
  - 底層 session 還活 → 續報 `running`（重掛 terminal 偵測）。
  - session 已不在 → 依 `launch_state` 判 `failed`（起一半崩）或 `completed`（已跑完但 report 沒送出，靠 outbound replay §3.3 補）。
- **Manual reclaim**：提供人工觸發的 reclaim（M0 可為 daemon 端點 / 手動指令），把卡住的 execution 拉回 reconcile。**自動 lease/heartbeat/timeout 留 M1**——M0 只保證「有辦法手動 + 啟動時自動 reconcile 一次」，不做常駐自動保命。
- Ploom 側：dispatch 卡在 claimed/running 且對應 execution 被 reconcile 成 terminal → 依 report 更新投影（人工關 issue gate 不變）。

---

## 6. Artifacts — Pointer-first（V2）

- **report 只帶 metadata / pointer，不 inline diff / transcript blob**。
- **M0 兩種 artifact**：
  - `diff`：pointer = daemon 端可取回的 diff 位址（deeplink 或 daemon-local ref）+ meta（檔案數、+/- 行數摘要）。issue 上顯示摘要，點 deeplink 看全貌。
  - `transcript`：pointer 指向 session transcript（既有 JSONL / stream 紀錄），非 blob 回填。
- **diff 擷取**：execution 完成後 daemon 端 `git diff`（相對 `head_at_start`）→ 產 pointer + summary meta。**diff 擷取本身 provider-agnostic**（claude/codex 皆同）。

### 6.1 Pointer scheme（M0）

- **格式**：`{kind, pointer, meta}`。`pointer` 為 **daemon-scoped opaque ref**（URI-like，如 `pdx://<daemon_id>/execution/<execution_id>/diff`），Ploom **只存不解析**，取全貌時由 deeplink 導到 Purdex 解析。
- **不 inline blob**（V2）：`meta` 只放摘要（`{files, add, del}` / transcript 行數），不放 diff/transcript 內容本身。
- Ploom projection 存 pointer + meta；issue 顯示 meta 摘要 + deeplink。

---

## 7. Admission Rule（V3）— 無 worktree 下的資料安全

> M0 不上 worktree（M3 才做），故需 admission rule 防 dirty tree / 併發資料事故。codex：不補後面一定 rework。

**派工被 daemon 受理（accepted）的前置條件**（任一不滿足 → `failed`，error 明列原因）：

1. **Canonical repo key（codex R1 #4 + §18.3，M0 必做）**：以 `repo_location.local_dir` 解出 **canonical 絕對路徑**（resolve symlink、`..`、trailing slash）當單一鍵。防「symlink 與真實路徑各派一次工 → 繞過單 live 規則」。canonical 失敗 / 逃出允許根 → 拒（`failed`）。
2. **repo 乾淨 OR 同 repo 單一 live execution**：以 canonical key 判定；目標 repo 已有一條 live（accepted/running/`launch_state≠none`）execution → 拒。單 canonical repo 同時只允許一條 live execution。
3. **⚠️ Per-repo lock 跨 accept→launch，非 point check（codex R1 #4 TOCTOU）**：admission 檢查與「起 session + 寫 `launch_state=launching` + 記 `head_at_start`」須在**同一把 per-canonical-repo lock** 內原子完成，避免「檢查乾淨後、launch 前」有第二派工插入或 repo 被改。lock 為 daemon 行程內（M0 單 daemon 足夠）。
4. **記錄 `head_at_start` + `dirty_at_start`**：受理時（持 lock）快照 repo HEAD commit 與 dirty 狀態，寫進 execution row（供 diff base 與事後稽核）。
5. **已知殘留（M0 限制，明列非漏）**：M0 無 worktree，execution **執行中**使用者/外部 process 若改同 repo 檔（`git pull`、手動編輯），`git diff`（相對 `head_at_start`）會把外部變更一起算進 artifact。M0 靠「單 live execution + `dirty_at_start` 稽核」降風險，**完全隔離留 M3 worktree**。此為刻意取捨，非未察缺陷。

---

## 8. Sandbox（A 軸）— M0 必須定，least privilege

> A 軸（sandbox / permission，防 agent 亂搞）≠ B 軸（worktree / base_commit，git 隔離）。worktree 不是 security sandbox。A 軸 M0 定，B 軸延 M3。

- **契約帶 `sandbox_profile`**：dispatch 只能 **request** 一個 profile；**Purdex daemon 持 host policy 為唯一權威**。

### 8.1 Profile enum + 偏序 + clamp 語意（codex R1 #6，M0 定死才能雙側 mock）

- **Enum（M0，全序，由嚴到寬）**：`read-only ⊏ ask ⊏ workspace-write ⊏ danger-full`。
  （`read-only`＝最嚴；`danger-full`＝最寬。M0 只需這 4 級，日後可插值。）
- **映射到 executor flag（M0，claude）**：
  | profile | `claude --permission-mode` |
  |---------|----------------------------|
  | `read-only` | `plan`（唯讀/計畫，不落地變更）|
  | `ask` | `default`（每動作詢問）|
  | `workspace-write` | `acceptEdits`（自動接受工作區編輯）|
  | `danger-full` | `bypassPermissions`（全放行）|
  （codex 的映射 M1 再定；M0 只 claude。)
- **Clamp（只降不升）**：`effective = min(request, host_policy)`（依上面全序取較嚴者）。
- **Unknown / 缺省行為**：
  - request 為 **未知 enum** → **拒**（`failed`，error `unknown_sandbox_profile`）——不 silent 放寬也不 silent 收嚴。
  - request **省略** → 視為 host policy 預設（daemon 設定；建議預設 `ask`）。
  - host policy **未設** → daemon 預設最嚴可用值（`ask`），不預設 `danger-full`（least privilege）。
- `ExecutionAccepted` 回 `effective_sandbox_profile`（clamp 後實際值，供 Ploom 投影 + 稽核）。
- OS 級隔離（container/VM）與更細 host 邊界留後續；**契約欄位與 enum M0 就在**。
- 定調：sandbox = **blast-radius reduction，非硬隔離**。

---

## 9. Deeplink

- **格式**：`purdex://execution/<execution_id>`，預留 `?host=<hint>` query（單 daemon M0 可省，但格式保留）。
- **Ploom 側**：report `accepted` 後，Ploom 用 `daemon.host` + `execution_id` 組 deeplink 存 projection、顯示在 issue。
- **Purdex 側（OS protocol handler — M0 缺口，需實作）**：
  - `electron/main.ts` 現**無** `setAsDefaultProtocolClient('purdex')` / `requestSingleInstanceLock` / `open-url`（macOS）/ `second-instance`（win/linux）。M0 要補。
  - **resolver 沿用既有 notification-click 管線**（main 收 deeplink → broadcast 到 renderer → SPA 決定誰有 tab → `notification:focus-window` 回呼；模板 `electron/main.ts:78-103` + `useNotificationDispatcher.ts:281`）。
  - **M0 resolver 兩段落點（codex 採 §18.2）**：① execution **活著** → focus 對應 tab（**observe-only**，見下）；② **活不到** → 開 Purdex 並落到 **execution 詳情頁 / 搜尋結果**（穩定落點，不落空）。三段式完整（snapshot 重建 / JSONL 唯讀）留後續。
  - **⚠️ Observe-only attach（codex R1 #7）**：dispatch execution 是 headless `claude -p`，deeplink focus 的 view **只顯示輸出、不接受 stdin 寫入**。M0 **不把 deeplink-opened view 接上 `SubscriberToRelay` 的寫入路徑**，藉此迴避「多 subscriber 寫 stdin」race，無需 M0 就做完整 controller/observer 寫入權仲裁（留 M1）。free-text follow-up（要寫入）本就是 M1.5。

---

## 10. Provider 中立接縫（M0 做接縫，claude-only live）

- **execution row + 契約帶 `provider` 欄位**（M0 恆 `claude`）。
- **relay 輸出映射到中性內部事件型別**——不讓 claude stream-json 原始 JSON 往 execution 層 / Ploom 契約漏。
- **M0 只實作 claude launch**；codex launch adapter（第二套 `codex exec --json` parser + 正規化）留 M1。中立接縫的目的正是讓 M1 只需「加 parser + config entry」，非重構。

---

## 11. Commands

```
# Purdex（在 worktree 內）
Daemon build:   go build -o bin/pdx ./cmd/pdx        # 或既有 build 腳本
Daemon test:    go test ./...
SPA test:       cd spa && npx vitest run
SPA lint:       cd spa && pnpm run lint
SPA build:      cd spa && pnpm run build
Electron build: cd spa && pnpm run electron:build     # deeplink handler 改動需重打包

# Ploom（在其 repo）
Build/test:     go test ./...
Web:            cd web && pnpm ...（依 Ploom 慣例）
```

---

## 12. Project Structure（新增/觸及）

**Purdex**
```
internal/module/dispatch/        → 新：dispatch 消費端（poll/claim/兩段式 fetch/report worker）
internal/module/execution/       → 新：execution runtime SOT（execution_id、狀態機、admission rule、diff 擷取）
  ├─ 中性事件型別（provider-neutral）
  └─ -p result 訊號 → execution terminal 接線
internal/relay/                  → 觸及：把 result event 訊號路由出來（不改橋接本體）
electron/main.ts                 → 觸及：purdex:// protocol handler + single-instance + resolver
spa/.../deeplink resolver        → 觸及：沿用 notification-click 管線定位 execution tab
docs/specs/2026-07-19-m0-dispatch-integration.md  → 本 spec
```

**Ploom**
```
internal/store/dispatch*.go      → 新：dispatch 表 + execution projection 表
internal/api/daemon_*.go         → 新：/daemon/dispatches[...] 4 端點（pull）
internal/api/...issue dispatch   → 新：issue「派工」intent 建立 dispatch row
web/src/routes/IssueEditor.tsx   → 觸及：派工按鈕 + execution 狀態列（execution → issue_event → IssueActivity 時間軸）
docs/specs/2026-06-30-s6-...md   → 更新：疊五層 + execution_id + pull（S6 §7 三題已拍板）
```

---

## 13. Code Style

- **Go**：match 既有——Purdex `internal/module/*` 分層、Ploom httpx→handler→store(OCC)→authz(純函式)。錯誤明確 wrap，不吞。
- **契約 payload**（JSON，`schema_version` 必帶）：
  ```json
  {
    "schema_version": 1,
    "execution_id": "exc_...",
    "dispatch_id": "dsp_...",
    "attempt_no": 1,
    "provider": "claude",
    "status": "running",
    "seq": 3,
    "effective_sandbox_profile": "...",
    "artifacts": [{"kind": "diff", "pointer": "...", "meta": {"files": 4, "add": 120, "del": 8}}]
  }
  ```

---

## 14. Testing Strategy

- **Purdex Go**：`go test ./...`。重點——admission rule（乾淨/dirty/已有 live execution 三態 + canonical/symlink 繞過 + per-repo lock TOCTOU）、execution 狀態機、**launch fence**（recovery 讀到 launching/launched 不 relaunch）、**terminal 兩來源**（process-exit 權威 vs `result` enrichment；異常 SIGTERM 無 result → failed）、seq 冪等 + ack cursor、**startup reconcile sweep**、diff 擷取相對 head_at_start、sandbox clamp（含 unknown→reject/缺省）。terminal 用 test seam 模擬 process exit + exit code。
- **Purdex SPA**：`vitest`。deeplink resolver 兩段落點（活著 observe-only focus / 活不到落詳情頁）。
- **Ploom Go**：`go test ./...`。daemon 端點 authz 隔離（只回 caller daemon）、claim 原子性/重複防護（同 daemon 冪等 / 他 daemon 409）、report seq 去重 + ack_seq、**accepted-before-lifecycle ordering（未 ack accepted 發 lifecycle→409）**、dispatch_id upsert 冪等（同 dispatch 回既有 execution）、error taxonomy。
- **端到端**：一次手動閉環驗證（seed daemon → issue 派工 → 觀察 issue 狀態列走 queued→running→completed + diff 摘要 + deeplink 可點）。
- **codex sandbox 無網路**：SPA 任務主 session 手動 `pnpm install` + vitest/lint/build 驗證。

---

## 15. Boundaries

- **Always**：先寫測試再實作（TDD）；每 task 獨立 commit；契約帶 `schema_version`；`execution_id` 當唯一對外 handle；report 帶單調 seq + 認 ack_seq；`accepted` 先 ack 才發 lifecycle；admission 持 per-repo lock 跨 accept→launch；launch 前寫 fence。
- **Ask first**：跨 repo 的 Ploom schema 變更順序（先 Ploom row 後 daemon 動作的 race 假設）；新增相依；host sandbox policy 的預設值（M0 建議 `ask`）。
- **Never**：讓 Ploom 自行推進 runtime 狀態機（違反 projection SOT）；雙寫 execution 表；inline diff/transcript blob 回填（違反 pointer-first）；同 execution_id 重複 launch（重試＝新 dispatch）；把 stop/interrupt 或寫入 stdin 塞進 M0 deeplink attach；unknown sandbox profile silent 放寬；直推 main（走 PR + codex 兩輪 review）；worktree 內 Edit/Write 漏帶 `.claude/worktrees/` 前綴。

---

## 16. Success Criteria（具體可測）

1. **閉環跑通**：seed 一個 daemon row → Ploom issue 按派工 → daemon 輪詢領到 → claim → 起 `claude -p` → issue 狀態列 `queued→running→completed`。
2. **execution_id 穩定 handle**：deeplink `purdex://execution/<id>` 可點，Purdex 開起並定位（execution 活著時 attach 對應 tab）。
3. **diff 回填**：完成後 issue 顯示 diff 摘要（檔案數 + 增刪行數），pointer 可取全貌。
4. **冪等**：同 dispatch 重複領/report 不產生第二條 execution；亂序/重複 seq 被丟棄。
5. **admission rule**：目標 repo 已有 live execution 時，新派工被拒且 error 明確。
6. **sandbox clamp**：request 一個比 host policy 寬的 profile → `effective_sandbox_profile` 被 clamp 到 host policy。
7. **SOT 分工**：Ploom 只投影、不推進；Purdex runtime 為狀態權威。
8. **crash 可收斂（reclaim）**：daemon 在 claim 後 / launch 後崩潰重啟 → startup reconcile 把卡住 execution 判成 running 或 terminal，dispatch 不成殭屍。
9. **launch fence**：同 execution_id 崩潰後 recovery **不重複起第二個 session**（重試＝新 dispatch/新 execution_id）。
10. **terminal 韌性**：SIGTERM/異常退出（無 `result`）仍能收到 process-exit → 標 `failed`；不在 `result`-before-exit 時過早標 completed。
11. **測試全綠**：Purdex `go test ./...` + `vitest` + `lint` + `build`；Ploom `go test ./...`。

---

## 17. PR / Phase 切法（codex R1 採定）

M0 跨兩 repo，合一 PR 不可行 → **拆兩 PR**。codex 採定順序：**先固定共享契約 + golden fixtures，再各做 repo；有序則先 Ploom（wire contract 是目前最不完整處，非 launch 細節）**。

- **PR-0（前置）**：**共享契約定稿 §4 + golden fixtures**（claim/report request-response JSON 範例、error taxonomy、seq/ack、ordering）。兩 repo 都對這組 fixture 寫 mock 測試。
- **PR-Ploom-M0**：dispatch 表 + execution projection 表 + 4 個 `/daemon/*` 端點（pull）+ accepted-ordering/ack_seq + issue 派工 intent + IssueEditor 狀態列。
- **PR-Purdex-M0**：dispatch 消費端（poll/claim/fetch/report worker + outbound replay）+ execution runtime SOT（狀態機/launch fence/admission+canonical+per-repo lock/reconcile sweep/diff/中性事件）+ **terminal 兩來源接線** + deeplink handler（observe-only）+ SPA resolver。
- 兩者靠**契約（§4）+ golden fixtures** 解耦，各用假對手獨立測。

> 每個 PR 內部再依 TDD task 切分（見後續 plan）。

---

## 18. Open Questions — codex R1 已收斂（記錄拍板）

1. **PR 切法** → **拆兩 PR + PR-0 契約/fixtures 前置，先 Ploom 契約端**（§17）。✅ 定。
2. **deeplink M0 落點深度** → **兩段足夠**（活著 observe-only focus / 活不到落 execution 詳情頁），snapshot 重建留後續；前提＝inactive 時有穩定落點不落空（§9）。✅ 定。
3. **repo_location canonical** → **M0 必做**最小 canonical + symlink escape（admission rule 正確性前置，非後續 hardening）（§7.1）。✅ 定。
4. **`-p` terminal seam** → 現況**無可用 seam**（relay 根本沒 parse result）；M0 **新開 seam**，且**分兩種 terminal source**（protocol result = enrichment / process-exit + ws-close = 權威）（§5.3）。✅ 定。

> 已無 open question 阻塞 plan。剩餘實作級細節（Ploom 端具體 schema、outbound replay 儲存形式）於 plan 階段展開。
